package workspace

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/util"
)

// gitCLITimeout is the ceiling for any git CLI invocation spawned by workspace
// git handlers. It prevents a hung git subprocess from blocking a stateless
// handler goroutine indefinitely.
const gitCLITimeout = 120 * time.Second

// gitCLI runs `git -C dir args...` with a hard timeout and stdin isolated,
// returning combined output. Mirrors the project package's gitRun helper so
// worktree-routed operations (where go-git cannot resolve a linked
// worktree's HEAD) behave identically on project roots and worktrees.
func gitCLI(dir string, args ...string) (string, error) {
	return gitCLIWithTimeout(gitCLITimeout, dir, args...)
}

// gitCLIWithTimeout is the parameterized version of gitCLI. Tests use it to
// exercise the timeout-error path with a sub-second deadline without mutating
// the production gitCLITimeout constant.
func gitCLIWithTimeout(timeout time.Duration, dir string, args ...string) (string, error) {
	out, err := gitCLIRawWithTimeout(timeout, dir, args...)
	return strings.TrimSpace(out), err
}

// gitCLIRaw is gitCLI WITHOUT whitespace trimming. Porcelain -z records
// legitimately start with a space (" M <path>" is a worktree-only
// modification); TrimSpace eats that byte, shifts the XY columns and makes
// the parser report unstaged changes as staged (observed in the round-6
// external TOTP run).
func gitCLIRaw(dir string, args ...string) (string, error) {
	return gitCLIRawWithTimeout(gitCLITimeout, dir, args...)
}

// gitCLIRawWithTimeout is the untrimmed core behind gitCLI/gitCLIRaw.
func gitCLIRawWithTimeout(timeout time.Duration, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := util.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(nil)
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return strings.TrimSpace(string(out)), fmt.Errorf("git %s: timed out after %s (subprocess killed; likely hung on a credential prompt, lock file, or unreachable remote)", strings.Join(args, " "), timeout)
	}
	return string(out), err
}

// gitCLIOut is a convenience wrapper around gitCLI that discards errors.
func gitCLIOut(dir string, args ...string) string {
	out, _ := gitCLI(dir, args...)
	return out
}

// gitRepoAt checks whether the given directory is inside a git repository.
func gitRepoAt(dir string) bool {
	_, err := gitCLI(dir, "rev-parse", "--git-dir")
	return err == nil
}

// parseGitPorcelainZ parses `git status -z --porcelain` output into
// GitFileStatus entries. -z produces NUL-terminated records with bare (never
// quoted) paths; newline-mode porcelain double-quotes non-ASCII paths, which
// used to render octal escapes (e.g. CJK filenames) in the git panel and
// dropdown. Rename/copy records carry the original path as a second bare
// NUL-terminated field, which is skipped here.
func parseGitPorcelainZ(z string) []domain.GitFileStatus {
	if z == "" {
		return nil
	}
	fields := strings.Split(strings.TrimRight(z, "\x00"), "\x00")
	var files []domain.GitFileStatus
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 3 {
			continue
		}
		files = append(files, domain.GitFileStatus{
			Path:     f[3:],
			Staging:  f[:1],
			Worktree: f[1:2],
		})
		if f[0] == 'R' || f[0] == 'C' {
			i++ // skip the original-path field
		}
	}
	return files
}

// gitRootResolver abstracts the two lookups resolveGitRoot performs so the
// routing rule is a pure function unit-testable without live actors.
type gitRootResolver struct {
	// projectRoot resolves the project mount root (legacy path).
	projectRoot func(projectID string) (string, error)
	// worktreeRoot resolves a worktree path owned by the project.
	worktreeRoot func(projectID, worktreeID string) (string, error)
}

// resolveGitRoot returns the git root a workspace git callable operates on.
// worktreeID == "" → project mount root (existing behavior); non-empty → the
// worktree path from the project's worktree registry (the worktree must
// belong to the project and be active).
func resolveGitRoot(projectID, worktreeID string, r gitRootResolver) (string, error) {
	if worktreeID == "" {
		return r.projectRoot(projectID)
	}
	return r.worktreeRoot(projectID, worktreeID)
}

// gitRootFor builds resolveGitRoot's resolver from the actor state and the
// call context, then resolves the root for a workspace git callable.
func (a *Actor) gitRootFor(ctx actor.PureContext, projectID, worktreeID string) (string, error) {
	return resolveGitRoot(projectID, worktreeID, gitRootResolver{
		projectRoot: a.findMountPath,
		worktreeRoot: func(pid, wid string) (string, error) {
			return a.worktreeRootFor(ctx, pid, wid)
		},
	})
}

// projectActorIDFor returns the canonical actor ID for a project given its
// mount name or actor ID. Returns the first match; empty projectID falls
// back to the first mount.
func (a *Actor) projectActorIDFor(projectID string) (string, error) {
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	if projectID != "" {
		for _, m := range a.Mounts {
			if m.ActorID == projectID {
				return m.ActorID, nil
			}
		}
		for _, m := range a.Mounts {
			if strings.EqualFold(m.Name, projectID) {
				return m.ActorID, nil
			}
		}
	}
	if len(a.Mounts) > 0 {
		return a.Mounts[0].ActorID, nil
	}
	return "", fmt.Errorf("project %q not found", projectID)
}

// worktreeRootFor resolves a worktree path via the project actor's
// project.worktree_get registry and validates it belongs to the project and
// is active. No state is copied into the workspace actor (Actor-first
// constraint): the project actor remains the single source of truth.
func (a *Actor) worktreeRootFor(ctx actor.PureContext, projectID, worktreeID string) (string, error) {
	if worktreeID == "" {
		return "", fmt.Errorf("worktree ID is empty")
	}
	actorID, err := a.projectActorIDFor(projectID)
	if err != nil {
		return "", err
	}
	cid, err := identity.ParseCanonicalID(actorID)
	if err != nil {
		return "", fmt.Errorf("invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return "", fmt.Errorf("project actor %q unavailable", projectID)
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 15*time.Second)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.worktree_get", gen.ProjectWorktreeGetReq{WorktreeID: worktreeID})
	if call == nil {
		return "", fmt.Errorf("project.worktree_get invoke returned nil")
	}
	raw, err := call.Final(callCtx)
	if err != nil {
		return "", fmt.Errorf("project.worktree_get: %w", err)
	}
	wt, ok := raw.(gen.ProjectWorktree)
	if !ok {
		return "", fmt.Errorf("project.worktree_get returned %T", raw)
	}
	if wt.Status != "active" {
		return "", fmt.Errorf("worktree %q is %s, not active", worktreeID, wt.Status)
	}
	if wt.Path == "" {
		return "", fmt.Errorf("worktree %q has no path", worktreeID)
	}
	return wt.Path, nil
}
