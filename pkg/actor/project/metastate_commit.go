package project

import (
	"fmt"
	"path/filepath"
	"strings"
)

// This file keeps the main repository working tree mergeable for workflow
// merges despite the fact that sporemind runtime state (wiki cards, graph
// snapshots, open-card state) is shared coordination data that the project
// actor writes directly into the main repo — bypassing git worktree
// isolation by design — and therefore leaves the main repo "dirty".
//
// Reconciliation is strictly merge-time (no periodic background commits):
// ensureMainRepoMergeable, called before mergeWorktreeIntoBase checks out
// the base branch, commits any runtime-state dirt with a single scoped
// bookkeeping commit. Other tracked uncommitted user changes are snapshotted
// into a named ref and restored after the merge (see restoreWorkflowSnapshot).
// Untracked files outside the runtime-state family are left to plain git
// semantics (checkout/merge refuse only if they would clobber such files).

// runtimeStateFamily lists the repo-relative paths that belong to sporemind
// runtime state. Writes to these are tolerated at merge time and reconciled
// with a scoped commit; writes anywhere else are the user's own work and
// must not be touched.
//
// The list mirrors worktreePathsToRestore (the paths restored from the parent
// after a child→parent merge) so both code paths agree on what counts as
// runtime state.
func isRuntimeStateRel(rel string) bool {
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, ".sporecode/wiki/") || rel == ".sporecode/wiki" {
		return true
	}
	switch rel {
	case ".sporecode/graphs.json", ".sporecode/wiki-state.json":
		return true
	}
	return false
}

// commitRuntimePaths stages and commits exactly the given repo-relative paths
// in the repo at root. It uses pathspec-scoped staging/add and partial commit,
// so it never consumes staged or unstaged changes to other paths. Hooks are
// bypassed (--no-verify) and GPG signing is disabled, because these are
// machine bookkeeping commits. A dedicated identity is injected per-invocation
// via -c so the commit succeeds even when the repo has no user.name configured.
//
// Best-effort contract: a git failure returns an error that callers log
// without blocking the state write that triggered the commit.
func commitRuntimePaths(root string, relPaths []string, msg string) error {
	if len(relPaths) == 0 {
		return nil
	}
	// Pre-filter: derive the actual commit set from porcelain output over the
	// pathspec. This both skips the no-op case and drops paths that no longer
	// exist on disk (deleted before ever being committed), which `git add`
	// would reject as pathspec errors.
	// -z: NUL-terminated records, never quoted or escaped. Newline-mode
	// porcelain always double-quotes non-ASCII paths (even with
	// core.quotepath=false), which used to silently skip CJK-named cards.
	statusArgs := append([]string{"status", "-z", "--porcelain", "-uall", "--"}, relPaths...)
	out, err := gitRun(nil, root, statusArgs...)
	if err != nil {
		return fmt.Errorf("inspect runtime state: %w", err)
	}
	if out == "" {
		return nil
	}
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	changed := make([]string, 0, len(relPaths))
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 3 {
			continue
		}
		changed = append(changed, f[3:])
		if f[0] == 'R' || f[0] == 'C' {
			// Rename/copy records carry the original path as a second
			// bare NUL-terminated field.
			if i+1 < len(fields) && fields[i+1] != "" {
				changed = append(changed, fields[i+1])
			}
			i++
		}
	}
	if len(changed) == 0 {
		return nil
	}
	// -A stages create/modify/delete for the pathspec; -A is required so that
	// deleting a tracked card records the deletion rather than leaving it
	// unstaged.
	addArgs := append([]string{"add", "-A", "--"}, changed...)
	if _, err := gitRun(nil, root, addArgs...); err != nil {
		return fmt.Errorf("stage runtime state: %w", err)
	}
	commitArgs := []string{
		"-c", "user.name=sporemind",
		"-c", "user.email=sporemind@sporemind.local",
		"-c", "commit.gpgsign=false",
		"commit", "--no-verify", "-m", msg, "--",
	}
	commitArgs = append(commitArgs, changed...)
	if _, err := gitRun(nil, root, commitArgs...); err != nil {
		// "nothing to commit" (e.g. content identical after stage) is a no-op.
		if strings.Contains(err.Error(), "nothing to commit") {
			return nil
		}
		return fmt.Errorf("commit runtime state: %w", err)
	}
	return nil
}

// ensureMainRepoMergeable reconciles the main repo's working tree before a
// workflow/exit merge checks out the base branch. Any runtime-state changes
// are committed with a scoped bookkeeping commit. Any other tracked
// uncommitted changes are snapshotted into a named ref so the merge can
// proceed without blocking the workflow owner; the caller restores the
// snapshot after the merge completes (see restoreWorkflowSnapshot). Untracked
// files outside the runtime-state family are left to plain git semantics
// (checkout/merge refuse only if they would clobber such files).
//
// Returns (snapRef, origBranch, error): snapRef is the named ref
// refs/sporemind/snapshot/<worktreeID> when tracked user changes were
// snapshotted and must be restored after the merge; origBranch is the branch
// the main repo was on before checkout so the caller can restore it.
func (a *Actor) ensureMainRepoMergeable(root, worktreeID string) (string, string, error) {
	// -z: NUL-terminated records, never quoted or escaped. Newline-mode
	// porcelain always double-quotes non-ASCII paths (even with
	// core.quotepath=false); the old parser carried those literal quotes
	// into the pathspec and workflow_stop failed with
	// "did not match any file(s)".
	// -uall lists untracked files individually: with the default -unormal,
	// a fully-untracked directory collapses to a single `?? dir/` entry
	// that defeats the runtime-state path check below.
	out, err := gitRun(nil, root, "status", "-z", "--porcelain", "-uall")
	if err != nil {
		return "", "", fmt.Errorf("check main repo state before merge: %w", err)
	}
	origBranch, _ := gitRun(nil, root, "rev-parse", "--abbrev-ref", "HEAD")
	origBranch = strings.TrimSpace(origBranch)
	if out == "" {
		return "", origBranch, nil
	}
	var runtimeDirty, userDirty, userDirtyUnstaged []string
	seen := make(map[string]bool)
	seenUnstaged := make(map[string]bool)
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 3 {
			continue
		}
		code := f[:2]
		paths := []string{f[3:]}
		if f[0] == 'R' || f[0] == 'C' {
			// Rename/copy records carry the original path as a second
			// bare NUL-terminated field.
			if i+1 < len(fields) && fields[i+1] != "" {
				paths = append(paths, fields[i+1])
			}
			i++
		}
		for _, p := range paths {
			if isRuntimeStateRel(p) {
				appendUnique(&runtimeDirty, seen, p)
			} else if code != "??" {
				// Tracked user changes block checkout/merge. Snapshot them
				// into a named ref so the merge proceeds; the caller restores
				// them afterwards.
				appendUnique(&userDirty, seen, p)
				// Paths whose index side is blank are not yet staged; we
				// need to stage them before write-tree. Paths already staged
				// (including deletions created by `git rm`) must not be
				// re-added with a pathspec, which git rejects for deleted
				// files.
				if code[0] == ' ' {
					appendUnique(&userDirtyUnstaged, seenUnstaged, p)
				}
			}
		}
	}
	var snapRef string
	if len(userDirty) > 0 {
		// Snapshot only the tracked dirty paths (not untracked files, not
		// runtime-state which is committed separately below). A recognizable
		// message so it can be inspected if restore fails.
		label := "sporemind: snapshot user changes before worktree merge"

		// Stage any unstaged user changes so write-tree captures the working
		// tree content exactly as it is now. Already-staged paths (including
		// deletions created by `git rm` and new staged files) must not be
		// re-added with a pathspec because git rejects deleted paths and -A
		// ignores not-yet-tracked files.
		if len(userDirtyUnstaged) > 0 {
			if _, err := gitRun(nil, root, append([]string{"add", "-u", "--"}, userDirtyUnstaged...)...); err != nil {
				return "", origBranch, fmt.Errorf("stage user changes before snapshot (%s): %w", strings.Join(userDirtyUnstaged, ", "), err)
			}
		}
		treeSHA, err := gitRun(nil, root, "write-tree")
		if err != nil {
			return "", origBranch, fmt.Errorf("write snapshot tree: %w", err)
		}
		treeSHA = strings.TrimSpace(treeSHA)

		// If the staged tree is identical to HEAD, there is nothing to snapshot
		// (e.g. staged-but-identical content). Drop the staged changes and leave
		// snapRef empty.
		headTree, err := gitRun(nil, root, "rev-parse", "HEAD^{tree}")
		if err != nil {
			return "", origBranch, fmt.Errorf("resolve HEAD tree: %w", err)
		}
		if strings.TrimSpace(headTree) == treeSHA {
			// Nothing to snapshot; just reset the staged paths to HEAD.
			if err := resetSnapshotPaths(root, userDirty); err != nil {
				return "", origBranch, fmt.Errorf("reset no-op user changes: %w", err)
			}
		} else {
			commitSHA, err := gitRun(nil, root, "commit-tree", treeSHA, "-p", "HEAD", "-m", label)
			if err != nil {
				return "", origBranch, fmt.Errorf("create snapshot commit: %w", err)
			}
			commitSHA = strings.TrimSpace(commitSHA)
			snapRef = "refs/sporemind/snapshot/" + worktreeID
			if _, err := gitRun(nil, root, "update-ref", snapRef, commitSHA); err != nil {
				return "", origBranch, fmt.Errorf("create snapshot ref %s: %w", snapRef, err)
			}
			if err := resetSnapshotPaths(root, userDirty); err != nil {
				return snapRef, origBranch, fmt.Errorf("reset working tree after snapshot: %w", err)
			}
		}
	}
	if len(runtimeDirty) > 0 {
		if err := commitRuntimePaths(root, runtimeDirty, "sporemind: reconcile runtime state before worktree merge"); err != nil {
			return snapRef, origBranch, fmt.Errorf("reconcile runtime state before merge: %w", err)
		}
	}
	return snapRef, origBranch, nil
}

// resetSnapshotPaths cleans tracked and staged-new paths from the working tree
// and index after a snapshot. Tracked paths are reset to HEAD with
// `git checkout HEAD`; staged files that do not yet exist in HEAD are removed
// from the index and disk with `git rm -f` because checkout cannot restore a
// path that is absent at HEAD.
func resetSnapshotPaths(root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	headOut, err := gitRun(nil, root, append([]string{"ls-tree", "-r", "-z", "--name-only", "HEAD", "--"}, paths...)...)
	if err != nil {
		return fmt.Errorf("list HEAD paths for cleanup: %w", err)
	}
	headSet := make(map[string]bool)
	for _, p := range strings.Split(strings.TrimRight(headOut, "\x00"), "\x00") {
		if p != "" {
			headSet[p] = true
		}
	}
	var headPaths, newPaths []string
	for _, p := range paths {
		if headSet[p] {
			headPaths = append(headPaths, p)
		} else {
			newPaths = append(newPaths, p)
		}
	}
	if len(headPaths) > 0 {
		args := []string{"checkout", "HEAD", "--"}
		args = append(args, headPaths...)
		if _, err := gitRun(nil, root, args...); err != nil {
			return fmt.Errorf("reset tracked paths: %w", err)
		}
	}
	if len(newPaths) > 0 {
		args := []string{"rm", "-f", "--"}
		args = append(args, newPaths...)
		if _, err := gitRun(nil, root, args...); err != nil {
			return fmt.Errorf("remove new staged paths: %w", err)
		}
	}
	return nil
}

func appendUnique(dst *[]string, seen map[string]bool, p string) {
	if seen[p] {
		return
	}
	seen[p] = true
	*dst = append(*dst, p)
}

// mapOwnerWorktreeID returns the worktree ID stamped in the map card's data
// block (data.ownerWorktreeId), or "" if unset/unavailable.
func (a *Actor) mapOwnerWorktreeID(mapID string) string {
	card, err := a.store.Get(mapID)
	if err != nil || card == nil {
		return ""
	}
	id, _ := card.Data["ownerWorktreeId"].(string)
	return id
}
