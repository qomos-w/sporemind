package project

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// worktreeEntry is a parsed row from `git worktree list --porcelain`.
type worktreeEntry struct {
	Path   string
	HEAD   string
	Branch string // empty when detached
}

// ── Git CLI helpers ──

func gitWorktreeAdd(root, branch, path, baseRef string) error {
	args := []string{"worktree", "add", "--no-track", "-b", branch, path}
	if baseRef != "" {
		args = append(args, baseRef)
	} else {
		args = append(args, "HEAD")
	}
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, root, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// gitWorktreeRemove deletes the worktree checkout at path from disk and
// deregisters it from git. The directory is removed locally via
// safeRemoveWorktreeDir — which unlinks symlinks and junctions instead of
// recursing through them (the 2026-08-14 incident) — and git's
// administrative registration is dropped afterwards with `git worktree
// prune`, which only touches entries whose checkout directory is already
// gone. `git worktree remove` is never invoked: its recursive delete
// follows junctions and can destroy trees outside the worktree.
//
// The removal is convergent: a dir already absent is a no-op, so orphan
// checkouts left behind by earlier failures (git deregistered but the dir
// lingered, or the reverse) are cleaned on retry.
func gitWorktreeRemove(root, path string) error {
	// Fail before touching the filesystem when root is not a git repository:
	// prune there would be meaningless, and deleting the checkout would
	// strand git's registration in the real repo.
	if _, err := gitWorktreeList(root); err != nil {
		return err
	}
	remove := safeRemoveWorktreeDir
	if gitWorktreeRemoveHook != nil {
		// Test seam: simulate a locked directory (Windows sharing violation)
		// deterministically without racing a real process handle.
		remove = gitWorktreeRemoveHook
	}
	if err := remove(root, path); err != nil {
		return fmt.Errorf("remove worktree dir: %w", err)
	}
	if out, err := gitRun(nil, root, "worktree", "prune"); err != nil {
		return fmt.Errorf("git worktree prune failed: %s", strings.TrimSpace(out))
	}
	return nil
}

// gitWorktreeRemoveHook, when non-nil, replaces the physical directory
// removal inside gitWorktreeRemove. nil in production; tests use it to
// simulate a sharing violation (a process still holding a handle inside the
// worktree) which cannot be created portably in a unit test.
var gitWorktreeRemoveHook func(root, path string) error

// safeRemoveWorktreeDir deletes the worktree checkout at path without ever
// following symlinks or junctions. Reparse points encountered during the
// walk are unlinked (the link itself is removed), never recursed through.
//
// This is the guard against a repeat of the 2026-08-14 incident, where a
// junction inside a worktree pointed at the main repo's node_modules (and
// other real trees: gospore/web-client, spore/ts, ...) and a recursive
// removal followed it and destroyed the targets. The walk is Lstat-based,
// so on Windows both symlinks and directory junctions are reported as
// ModeSymlink and unlinked only — the contents of the link target are never
// touched.
//
// Guards: path must be absolute; it must not be the repo root or any
// ancestor of it; it must not be the filesystem root. Removal is scoped to
// path and its descendants only — siblings of path are never touched.
func safeRemoveWorktreeDir(root, path string) error {
	abs := filepath.Clean(path)
	if !filepath.IsAbs(abs) {
		return fmt.Errorf("refusing to remove relative path %q", path)
	}
	if filepath.Dir(abs) == abs {
		return fmt.Errorf("refusing to remove filesystem root %q", abs)
	}
	rootAbs := filepath.Clean(root)
	if abs == rootAbs || strings.HasPrefix(rootAbs, abs+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove repo root or its ancestor %q", abs)
	}
	if err := safeDeleteTree(abs); err != nil {
		return err
	}
	// Windows: the now-empty directory can still be held as a live
	// process's cwd, so the final rmdir may fail with a sharing violation
	// even after every child is gone. Retry the rmdir with backoff; once
	// the holder releases, the empty dir vanishes.
	for i := 0; i < 3; i++ {
		if _, err := os.Lstat(abs); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 250 * time.Millisecond)
		if err := os.Remove(abs); err == nil {
			return nil
		}
	}
	if _, err := os.Lstat(abs); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("worktree dir %q still held after retries", abs)
}

// safeDeleteTree is the recursive primitive used by safeRemoveWorktreeDir.
// Symlinks and Windows junctions (both ModeSymlink) are unlinked only and
// never followed; regular files are removed; directories are emptied then
// removed.
func safeDeleteTree(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return os.Remove(p)
	}
	if !fi.IsDir() {
		return os.Remove(p)
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := safeDeleteTree(filepath.Join(p, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(p)
}

// gitBranchDelete removes a branch ref. git worktree remove only deletes the
// worktree directory; without this, every discarded worker worktree leaves a
// residue branch under .git/refs/heads forever.
func gitBranchDelete(root, branch string) error {
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, root, "branch", "-D", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch -D %s failed: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

// deleteWorktreeBranchRef best-effort deletes the branch behind a worktree
// being torn down. Skips the repo's default branch (the main checkout's
// branch must never be removed) and empty names. Callers treat failure as
// non-fatal: the worktree itself is already gone; a leftover ref is cosmetic
// compared to aborting a completed merge/discard.
func deleteWorktreeBranchRef(root, branch string) {
	if branch == "" {
		return
	}
	if def, err := defaultBranch(root); err == nil && branch == def {
		return
	}
	_ = gitBranchDelete(root, branch)
}

func gitWorktreeList(root string) ([]worktreeEntry, error) {
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, root, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("git worktree list failed: %s", exitErr.Stderr)
		}
		return nil, fmt.Errorf("git worktree list failed: %w", err)
	}
	return parseWorktreePorcelain(string(out)), nil
}

func parseWorktreePorcelain(s string) []worktreeEntry {
	var entries []worktreeEntry
	var current *worktreeEntry
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			if current != nil {
				entries = append(entries, *current)
				current = nil
			}
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			if current != nil {
				entries = append(entries, *current)
			}
			current = &worktreeEntry{Path: strings.TrimPrefix(line, "worktree ")}
		} else if strings.HasPrefix(line, "HEAD ") {
			if current != nil {
				current.HEAD = strings.TrimPrefix(line, "HEAD ")
			}
		} else if strings.HasPrefix(line, "branch ") {
			if current != nil {
				current.Branch = strings.TrimPrefix(line, "branch ")
			}
		}
	}
	if current != nil {
		entries = append(entries, *current)
	}
	return entries
}

func gitBranchShowCurrent(path string) (string, error) {
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, path, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("git branch --show-current failed: %s", exitErr.Stderr)
		}
		return "", fmt.Errorf("git branch --show-current failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func gitRevParseHead(path string) (string, error) {
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, path, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("git rev-parse HEAD failed: %s", exitErr.Stderr)
		}
		return "", fmt.Errorf("git rev-parse HEAD failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitIsAncestor reports whether commit `ancestor` is an ancestor of commit
// `descendant` (i.e. `descendant` contains `ancestor`). Uses `git merge-base
// --is-ancestor`. Returns false (with nil error) if the check fails because
// one of the refs does not exist.
func gitIsAncestor(dir, ancestor, descendant string) (bool, error) {
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, dir, "merge-base", "--is-ancestor", ancestor, descendant)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor failed: %w", err)
}

func gitWorktreeHasUncommitted(path string) (bool, error) {
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, path, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return false, fmt.Errorf("git status --porcelain failed: %s", exitErr.Stderr)
		}
		return false, fmt.Errorf("git status --porcelain failed: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// gitDirtyFiles returns up to limit file paths that git reports as dirty in
// path. Renames report their destination path. Paths are taken from
// `git status --porcelain` output.
func gitDirtyFiles(path string, limit int) ([]string, error) {
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, path, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("git status --porcelain failed: %s", exitErr.Stderr)
		}
		return nil, fmt.Errorf("git status --porcelain failed: %w", err)
	}
	var files []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 4 {
			continue
		}
		// git status --porcelain format: XY <path> or XY <path> -> <newpath>
		// Take the rightmost path (destination for renames).
		rest := line[3:]
		if idx := strings.LastIndex(rest, " -> "); idx >= 0 {
			rest = rest[idx+4:]
		}
		files = append(files, rest)
		if len(files) >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan git status output: %w", err)
	}
	return files, nil
}

// ── Persistence ──

// Per-worktree ground-truth manifests live at
// <worktreesStateDir>/manifests/<id>/state.json via persist.Persist; see
// worktree_manifest.go. This directory previously held a single bulk
// worktrees-state.json written via raw file IO — that format is migrated on
// startup and no longer written.

// worktreeBaseDir returns the root directory for worktree checkouts. Each
// worktree is created as <worktreeBaseDir>/<uuid>. The directory lives under
// the project actor's settings directory (<ActorDataDir>/project/<actor-id>/)
// so it co-locates with the persist store and is removed when the actor is
// destroyed. This keeps worktrees out of .git, making them visible to tools
// like vite that walk up looking for .git.
func (a *Actor) worktreeBaseDir() string {
	return filepath.Join(config.ActorDataDir(), "project", a.actorID, "worktree")
}

// worktreesStateDir is the project actor's settings directory
// (<ActorDataDir>/project/<actor-id>/), holding the per-worktree manifests and
// co-locating with the worktree checkouts so everything is removed together
// when the actor is destroyed.
func (a *Actor) worktreesStateDir() string {
	return filepath.Join(config.ActorDataDir(), "project", a.actorID)
}

// ── Stale reconciliation ──

func (a *Actor) reconcileWorktrees() (bool, error) {
	a.worktreeParentMu.RLock()
	hasWorktrees := len(a.worktrees) > 0
	a.worktreeParentMu.RUnlock()
	if !hasWorktrees {
		// Even with no known worktrees, run reverse discovery to detect
		// unknown worktrees created externally (e.g. by a crashed process).
		return a.reverseDiscoverWorktrees()
	}
	root, err := a.rootPath()
	if err != nil {
		return false, err
	}
	live, err := gitWorktreeList(root)
	if err != nil {
		return false, fmt.Errorf("project: reconcile worktrees: %w", err)
	}
	livePaths := make(map[string]bool, len(live))
	for _, e := range live {
		livePaths[filepath.Clean(e.Path)] = true
	}
	changed := false
	a.worktreeParentMu.Lock()
	for id, wt := range a.worktrees {
		if wt.Status == "discarded" || wt.Status == "residual" {
			// "residual" entries are retired by the periodic sweep
			// (sweepResidualWorktrees), not by stale-marking: their
			// directory is still locked by a live process and must not be
			// flipped to "stale".
			continue
		}
		// Directory missing or not in git worktree list -> stale
		if _, ok := livePaths[filepath.Clean(wt.Path)]; !ok {
			wt.Status = "stale"
			a.worktrees[id] = wt
			changed = true
			continue
		}
		// Also check if the directory physically exists but git removed it
		if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
			wt.Status = "stale"
			a.worktrees[id] = wt
			changed = true
		}
	}
	a.worktreeParentMu.Unlock()

	// Reverse discovery: check git worktree list for entries not in our
	// metadata. These are worktrees created externally (crash recovery,
	// manual git worktree add, etc.) that we should track.
	discovered, err := a.reverseDiscoverWorktrees()
	if err != nil {
		return changed, fmt.Errorf("project: reconcile worktrees: reverse discovery: %w", err)
	}
	if discovered {
		changed = true
	}

	return changed, nil
}

// reverseDiscoverWorktrees scans the git worktree list for entries that are
// not in our metadata. Any unknown worktree is added with status "stale" so
// it is visible but not usable until manually reconciled.
//
// Agent→worktree bindings are intentionally NOT cleaned here. After a crash
// or app exit agents come back lazy-unloaded, so an actor-system lookup cannot
// distinguish a dead agent from an unloaded one — clearing bindings here would
// silently unmount every workflow owner/worker worktree across a restart.
// Bindings are released only by explicit calls (agent deletion cascade,
// workflow stop merge/discard, project.worktree_release_binding).
func (a *Actor) reverseDiscoverWorktrees() (bool, error) {
	root, err := a.rootPath()
	if err != nil {
		return false, err
	}
	live, err := gitWorktreeList(root)
	if err != nil {
		return false, nil // non-fatal: skip reverse discovery on git error
	}

	a.worktreeParentMu.Lock()
	changed := false

	// Build a set of known paths for quick lookup.
	knownPaths := make(map[string]bool, len(a.worktrees))
	for _, wt := range a.worktrees {
		knownPaths[filepath.Clean(wt.Path)] = true
	}

	// Discover unknown worktrees from git's list.
	cleanRoot := filepath.Clean(root)
	for _, e := range live {
		cleanPath := filepath.Clean(e.Path)
		if knownPaths[cleanPath] {
			continue
		}
		// Skip the main repo root itself (git lists it as the first entry).
		if cleanPath == cleanRoot {
			continue
		}
		// Unknown worktree: add it as stale.
		id := uuid.New().String()
		wt := gen.ProjectWorktree{
			ID:         id,
			Name:       filepath.Base(e.Path),
			Path:       e.Path,
			Branch:     e.Branch,
			BaseRef:    e.HEAD,
			Status:     "stale",
			CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
			LastUsedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		if a.worktrees == nil {
			a.worktrees = make(map[string]gen.ProjectWorktree)
		}
		a.worktrees[id] = wt
		changed = true
	}
	a.worktreeParentMu.Unlock()

	return changed, nil
}

// ── Lifecycle handlers ──

func (a *Actor) handleWorktreeCreate(_ actor.PureContext, req gen.ProjectWorktreeCreateReq) (gen.ProjectWorktree, error) {
	if req.Name == "" {
		return gen.ProjectWorktree{}, fmt.Errorf("project.worktree.create: Name required")
	}
	root, err := a.rootPath()
	if err != nil {
		return gen.ProjectWorktree{}, fmt.Errorf("project.worktree.create: %w", err)
	}

	// Branch uniqueness pre-check: no other active worktree may use the same
	// branch name. This prevents duplicate branch collisions when multiple
	// worktrees are created from the same parent.
	a.worktreeParentMu.RLock()
	for _, wt := range a.worktrees {
		if wt.Branch == req.Name && wt.Status != "discarded" {
			a.worktreeParentMu.RUnlock()
			return gen.ProjectWorktree{}, fmt.Errorf("project.worktree.create: branch %q already exists (worktree %q)", req.Name, wt.ID)
		}
	}
	a.worktreeParentMu.RUnlock()

	id := uuid.New().String()
	branch := req.Name
	wtPath := filepath.Join(a.worktreeBaseDir(), id)

	// Resolve baseRef: when ParentWorktreeID is set, use the parent's branch
	// HEAD as the base reference so the new worktree starts from the parent's
	// current state rather than the main repo's HEAD.
	baseRef := req.BaseRef
	if req.ParentWorktreeID != "" && baseRef == "" {
		a.worktreeParentMu.RLock()
		parent, parentOk := a.worktrees[req.ParentWorktreeID]
		a.worktreeParentMu.RUnlock()
		if !parentOk {
			return gen.ProjectWorktree{}, fmt.Errorf("project.worktree.create: parent worktree %q not found", req.ParentWorktreeID)
		}
		head, err := gitRevParseHead(parent.Path)
		if err != nil {
			return gen.ProjectWorktree{}, fmt.Errorf("project.worktree.create: resolve parent branch HEAD: %w", err)
		}
		baseRef = head
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}

	if err := os.MkdirAll(a.worktreeBaseDir(), 0755); err != nil {
		return gen.ProjectWorktree{}, fmt.Errorf("project.worktree.create: mkdir worktree base dir: %w", err)
	}

	if err := gitWorktreeAdd(root, branch, wtPath, baseRef); err != nil {
		return gen.ProjectWorktree{}, fmt.Errorf("project.worktree.create: %w", err)
	}

	// Post-creation symlink detection: scan the worktree directory for any
	// symlinks or junctions. If found, remove the worktree and reject the
	// creation. Symlinks in a worktree can escape the sandbox or be used to
	// inject malicious content.
	if err := detectSymlinkInWalk(wtPath); err != nil {
		_ = gitWorktreeRemove(root, wtPath)
		deleteWorktreeBranchRef(root, branch)
		return gen.ProjectWorktree{}, fmt.Errorf("project.worktree.create: worktree contains symlinks or junctions: %w", err)
	}

	// Generate a worktree-local go.work so go tools compile the worktree's
	// own files instead of being hijacked by the main repo's go.work.
	_ = generateWorktreeGoWork(root, wtPath)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	wt := gen.ProjectWorktree{
		ID:               id,
		Name:             req.Name,
		Path:             wtPath,
		Branch:           branch,
		BaseRef:          baseRef,
		Status:           "active",
		CreatedAt:        now,
		LastUsedAt:       now,
		ParentWorktreeID: req.ParentWorktreeID,
		WorkflowMapID:    req.WorkflowMapID,
	}
	a.worktreeParentMu.Lock()
	if a.worktrees == nil {
		a.worktrees = make(map[string]gen.ProjectWorktree)
	}
	a.worktrees[id] = wt
	a.worktreeParentMu.Unlock()
	if err := a.persistWorktreeManifest(id); err != nil {
		// Roll back the git worktree on save failure
		_ = gitWorktreeRemove(root, wtPath)
		deleteWorktreeBranchRef(root, branch)
		a.worktreeParentMu.Lock()
		delete(a.worktrees, id)
		a.worktreeParentMu.Unlock()
		_ = a.deleteWorktreeState(id)
		return gen.ProjectWorktree{}, fmt.Errorf("project.worktree.create: %w", err)
	}
	return wt, nil
}

func (a *Actor) handleWorktreeList(_ actor.PureContext, _ gen.ProjectWorktreeListReq) (gen.ProjectWorktreeListResp, error) {
	a.worktreeParentMu.RLock()
	list := make([]gen.ProjectWorktree, 0, len(a.worktrees))
	for _, wt := range a.worktrees {
		list = append(list, wt)
	}
	a.worktreeParentMu.RUnlock()
	return gen.ProjectWorktreeListResp{Worktrees: list}, nil
}

func (a *Actor) handleWorktreeGet(_ actor.PureContext, req gen.ProjectWorktreeGetReq) (gen.ProjectWorktree, error) {
	a.worktreeParentMu.RLock()
	wt, ok := a.worktrees[req.WorktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return gen.ProjectWorktree{}, fmt.Errorf("project.worktree.get: worktree %q not found", req.WorktreeID)
	}
	return wt, nil
}

func (a *Actor) handleWorktreeDiscard(_ actor.PureContext, req gen.ProjectWorktreeDiscardReq) error {
	a.worktreeParentMu.RLock()
	wt, ok := a.worktrees[req.WorktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return fmt.Errorf("project.worktree.discard: worktree %q not found", req.WorktreeID)
	}
	root, err := a.rootPath()
	if err != nil {
		return fmt.Errorf("project.worktree.discard: %w", err)
	}

	if !req.Force {
		dirty, err := gitWorktreeHasUncommitted(wt.Path)
		if err != nil {
			return fmt.Errorf("project.worktree.discard: %w", err)
		}
		if dirty {
			return fmt.Errorf("project.worktree.discard: worktree has uncommitted changes, requires Force=true")
		}
	}

	if err := gitWorktreeRemove(root, wt.Path); err != nil {
		return fmt.Errorf("project.worktree.discard: %w", err)
	}
	deleteWorktreeBranchRef(root, wt.Branch)

	a.worktreeParentMu.Lock()
	a.bindingMu.Lock()
	delete(a.worktrees, req.WorktreeID)
	for agentID, bound := range a.agentWorktree {
		if bound == req.WorktreeID {
			delete(a.agentWorktree, agentID)
		}
	}
	a.bindingMu.Unlock()
	a.worktreeParentMu.Unlock()
	if err := a.deleteWorktreeState(req.WorktreeID); err != nil {
		return fmt.Errorf("project.worktree.discard: %w", err)
	}
	return nil
}

// handleWorktreeReleaseBinding is the agent-destroy hook. Called by
// workspace.delete_agent so the project can drop the binding and remove the
// worktree if it is no longer referenced. Internal-only.
func (a *Actor) handleWorktreeReleaseBinding(ctx actor.PureContext, req gen.ProjectWorktreeReleaseBindingReq) error {
	if req.AgentActorID == "" {
		return fmt.Errorf("project.worktree.release_binding: AgentActorID required")
	}
	return a.releaseWorktreeBinding(ctx, req.AgentActorID, req.ForceDelete)
}

// handleWorktreeBoundCheck reports whether a specific agent has an active
// worktree binding. Internal-only: consumed by appmanager's dev surface to
// route a caller bound to a worktree — plugin dev reads and writes the
// project tree, and such a caller's dev calls target the caller's own
// checkout instead of the shared main tree. Unlike worktree_agent_bindings
// (caller-scoped), this takes an explicit AgentActorID; the turn engine
// injects that field unforgeably for appmanager dev callables, so an agent
// cannot probe other agents through it.
func (a *Actor) handleWorktreeBoundCheck(_ actor.PureContext, req gen.ProjectWorktreeBoundCheckReq) (gen.ProjectWorktreeBoundCheckResp, error) {
	if req.AgentActorID == "" {
		return gen.ProjectWorktreeBoundCheckResp{}, fmt.Errorf("project.worktree.bound_check: AgentActorID required")
	}
	a.bindingMu.RLock()
	wtID, ok := a.agentWorktree[req.AgentActorID]
	a.bindingMu.RUnlock()
	if !ok {
		return gen.ProjectWorktreeBoundCheckResp{}, nil
	}
	a.worktreeParentMu.RLock()
	wt, exists := a.worktrees[wtID]
	a.worktreeParentMu.RUnlock()
	if !exists || wt.Status != "active" {
		return gen.ProjectWorktreeBoundCheckResp{}, nil
	}
	return gen.ProjectWorktreeBoundCheckResp{Bound: true, WorktreePath: wt.Path}, nil
}

// handleWorktreeAgentBindings returns ONLY the caller's own binding. An agent
// must not observe other agents' worktree bindings. With no caller identity
// (system/internal calls) or an unbound caller, the response is empty.
func (a *Actor) handleWorktreeAgentBindings(ctx actor.PureContext, req gen.ProjectWorktreeAgentBindingsReq) (gen.ProjectWorktreeAgentBindingsResp, error) {
	cid := callerID(ctx)
	if cid == "" {
		return gen.ProjectWorktreeAgentBindingsResp{Bindings: []gen.ProjectWorktreeAgentBinding{}}, nil
	}
	a.bindingMu.RLock()
	wtID, ok := a.agentWorktree[cid]
	a.bindingMu.RUnlock()
	if !ok {
		return gen.ProjectWorktreeAgentBindingsResp{Bindings: []gen.ProjectWorktreeAgentBinding{}}, nil
	}
	a.worktreeParentMu.RLock()
	wt, exists := a.worktrees[wtID]
	a.worktreeParentMu.RUnlock()
	if !exists {
		return gen.ProjectWorktreeAgentBindingsResp{Bindings: []gen.ProjectWorktreeAgentBinding{}}, nil
	}
	return gen.ProjectWorktreeAgentBindingsResp{Bindings: []gen.ProjectWorktreeAgentBinding{{
		AgentActorID: cid,
		WorktreeID:   wtID,
		WorktreePath: wt.Path,
		Branch:       wt.Branch,
		Status:       wt.Status,
		Name:         wt.Name,
	}}}, nil
}

// ── agent→worktree binding (方案 B 的隔离核心) ──
//
// worktree 不是独立 actor，而是 project 内的一张只读映射 agentActorID → worktree。
// file/git handler 通过 caller id 反查这张映射，命中则把 rootPath/Roots 重写为
// worktree 路径。绑定在 spawn 时写、agent 销毁时删、运行时只读。
//
// 注：project actor 的 owner loop 是单 goroutine，但 file handler 是 PureContext
// 并发执行。bindingMu 保护 agentWorktree 在并发读 + 偶尔写（spawn/release）下的安全；
// worktreeParentMu 保护 worktrees 与 agentParent。

// callerID extracts the caller's actor ID string from a context, nil-safe.
// Returns "" when there is no caller (system/internal calls), in which case
// effectiveRoots falls back to the project's main Roots.
func callerID(ctx interface{ Caller() ref.Ref }) string {
	if ctx == nil {
		return ""
	}
	r := ctx.Caller()
	if r == nil {
		return ""
	}
	return r.ID().String()
}

// worktreeDo resolves the caller's bound worktree path under a read lock, then
// runs fn with the lock released so a long git operation does not block
// concurrent worktree enter/exit. The path is captured before release.
func (a *Actor) worktreeDo(callerID string, fn func(string) error) (bool, error) {
	if callerID == "" {
		return false, nil
	}
	a.bindingMu.RLock()
	wtID, ok := a.agentWorktree[callerID]
	a.bindingMu.RUnlock()
	if !ok {
		return false, nil
	}
	a.worktreeParentMu.RLock()
	wt, exists := a.worktrees[wtID]
	a.worktreeParentMu.RUnlock()
	if !exists || wt.Status != "active" {
		return false, nil
	}
	path := wt.Path
	return true, fn(path)
}

// gitRun executes `git <args...>` with cwd=dir and returns combined output.
// Shared CLI helper for worktree-scoped git operations. Separate args, never
// shell-concat (Windows-safe). done (may be nil) links cancellation to the
// owning cell; a hard timeout always applies. When the context deadline fires
// the returned error explicitly names the timeout so callers can distinguish
// a hung subprocess from a conflict or exit-code failure.
func gitRun(done <-chan struct{}, dir string, args ...string) (string, error) {
	return gitRunWithTimeout(gitTimeout, done, dir, args...)
}

// gitRunWithTimeout is the parameterized version of gitRun. Tests use it to
// exercise the timeout-error path with a sub-second deadline without mutating
// the production gitTimeout constant.
func gitRunWithTimeout(timeout time.Duration, done <-chan struct{}, dir string, args ...string) (string, error) {
	ctx, cancel := gitCtxWithTimeout(timeout, done)
	defer cancel()
	cmd := gitCmd(ctx, dir, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return string(out), fmt.Errorf("git %s: timed out after %s (subprocess killed; likely hung on a credential prompt, lock file, or unreachable remote)", strings.Join(args, " "), timeout)
		}
		stderr := string(out)
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			stderr = string(ee.Stderr)
		}
		return stderr, fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLine(stderr))
	}
	return string(out), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// bindWorktree records that agentActorID operates inside the given worktree.
// Called from handleSpawnAgent when req.WorktreeID is set. Returns an error
// if the agent is already bound to a different worktree.
func (a *Actor) bindWorktree(agentActorID, worktreeID string) error {
	a.worktreeParentMu.RLock()
	wt, ok := a.worktrees[worktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return fmt.Errorf("project: bind worktree: worktree %q not found", worktreeID)
	}
	if wt.Status != "active" {
		return fmt.Errorf("project: bind worktree: worktree %q is %s, not active", worktreeID, wt.Status)
	}
	// Existing binding check: if the agent is already bound to a worktree,
	// refuse to bind to a different one. The agent must first exit the
	// current binding via releaseWorktreeBinding.
	a.bindingMu.Lock()
	if existing, bound := a.agentWorktree[agentActorID]; bound && existing != worktreeID {
		a.bindingMu.Unlock()
		return fmt.Errorf("project: bind worktree: agent %q already bound to worktree %q", agentActorID, existing)
	}
	if a.agentWorktree == nil {
		a.agentWorktree = make(map[string]string)
	}
	a.agentWorktree[agentActorID] = worktreeID
	a.bindingMu.Unlock()
	return nil
}

// releaseWorktreeBinding drops the binding for agentActorID. Called when the
// agent is destroyed (workspace.delete_agent → project.worktree.release_binding).
// If no other agent remains bound to that worktree, the worktree is removed.
// Owner worktree stale protection: if the worktree is a workflow owner worktree
// (ParentWorktreeID empty, WorkflowMapID non-empty), it is marked stale instead
// of force-removed, and child worktrees are recursively marked stale — UNLESS
// forceDelete is true (explicit agent deletion), in which case the worktree is
// physically removed and the map card's data.ownerWorktreeId is cleared.
func (a *Actor) releaseWorktreeBinding(ctx actor.PureContext, agentActorID string, forceDelete bool) error {
	a.worktreeParentMu.Lock()
	a.bindingMu.Lock()
	wtID, ok := a.agentWorktree[agentActorID]
	if ok {
		delete(a.agentWorktree, agentActorID)
	}
	delete(a.agentParent, agentActorID)
	a.bindingMu.Unlock()
	a.worktreeParentMu.Unlock()

	// Drop any review changeset state — the worktree (and thus the agent's
	// work context) is being released. The authoritative snapshot lives on
	// the task card; the agent→card pointer and generation counter live in a
	// persisted review-binding record (persist.Persist). Delete the binding,
	// clear the card data it points at, and drop the transient file-contents
	// cache. Deleting the binding bumps nothing — any in-flight finalize
	// finds no card entry (already cleared) and no matching counter, so it is
	// rejected as stale.
	binding := a.loadReviewBinding(agentActorID)
	if binding.TaskCardID != "" {
		a.clearReviewChangesetCardData(binding.TaskCardID)
	}
	a.deleteReviewBinding(agentActorID)
	a.reviewFileContentsMu.Lock()
	delete(a.reviewFileContents, agentActorID)
	a.reviewFileContentsMu.Unlock()
	if !ok {
		return nil
	}
	// If any other agent still bound to this worktree, keep it.
	a.bindingMu.RLock()
	for _, id := range a.agentWorktree {
		if id == wtID {
			a.bindingMu.RUnlock()
			return nil
		}
	}
	a.bindingMu.RUnlock()

	// No remaining bindings: remove the worktree.
	a.worktreeParentMu.RLock()
	wt, exists := a.worktrees[wtID]
	a.worktreeParentMu.RUnlock()
	if !exists {
		return nil
	}

	// Owner worktree stale protection: if this is a workflow owner worktree
	// (has WorkflowMapID but no parent), mark stale instead of force-removing
	// so that workflow data is not lost when the owner agent is destroyed
	// without a proper workflow_stop.
	// Exception: when forceDelete is true (explicit agent deletion), the
	// worktree is physically removed and the map card's ownerWorktreeId is
	// cleared — the user intended to delete the agent and its worktree.
	if wt.ParentWorktreeID == "" && wt.WorkflowMapID != "" && !forceDelete {
		wt.Status = "stale"
		a.worktreeParentMu.Lock()
		a.worktrees[wtID] = wt
		// Recursively mark child worktrees as stale.
		a.markChildWorktreesStale(wtID)
		a.worktreeParentMu.Unlock()
		a.persistWorktreeManifests(a.staleWorktreeIDs(wtID))
		ctx.Logger().Info("project: release binding: workflow owner worktree marked stale (no workflow_stop)",
			"worktree", wtID, "workflowMapID", wt.WorkflowMapID)
		return nil
	}

	root, err := a.rootPath()
	if err != nil {
		return fmt.Errorf("project: release binding: %w", err)
	}
	// Force-remove on agent destroy: uncommitted changes in an orphaned
	// worktree should not block cleanup. The agent is gone; nobody will
	// resolve these changes.
	if err := gitWorktreeRemove(root, wt.Path); err != nil {
		ctx.Logger().Warn("project: release binding: failed to remove orphaned worktree",
			"worktree", wtID, "error", err)
	} else {
		deleteWorktreeBranchRef(root, wt.Branch)
		a.worktreeParentMu.Lock()
		delete(a.worktrees, wtID)
		a.worktreeParentMu.Unlock()
		_ = a.deleteWorktreeState(wtID)
		// When force-deleting a workflow owner worktree, clear the
		// ownerWorktreeId stamp from the map card so it doesn't
		// reference a deleted worktree.
		if forceDelete && wt.WorkflowMapID != "" {
			a.clearMapOwnerWorktree(wt.WorkflowMapID)
		}
	}
	return nil
}

// effectiveRoots returns the Roots view a caller should resolve paths against.
// If the caller is bound to a worktree, returns that worktree's path as the
// single root; otherwise returns the project's configured Roots.
func (a *Actor) effectiveRoots(callerID string) []domain.RootDirEntry {
	if callerID != "" {
		a.bindingMu.RLock()
		wtID, ok := a.agentWorktree[callerID]
		a.bindingMu.RUnlock()
		if ok {
			a.worktreeParentMu.RLock()
			wt, exists := a.worktrees[wtID]
			a.worktreeParentMu.RUnlock()
			if exists && wt.Status == "active" {
				return []domain.RootDirEntry{{Name: wt.Name, Path: wt.Path}}
			}
		}
	}
	roots, err := a.rootsSnapshot()
	if err != nil {
		return nil
	}
	return roots
}

// bindingState classifies a caller's worktree binding for isolation fencing.
//   - bindingUnbound: no binding; caller operates on the main repo root and
//     keeps browse-anywhere.
//   - bindingActive: caller is bound to an active worktree; confined to it.
//   - bindingInactive: caller is bound but the worktree is stale / pending /
//     missing. Such a caller MUST NOT fall back to the main repo (that would
//     leak), and cannot use the worktree dir. File/shell/git operations are
//     rejected until the caller explicitly exits.
type bindingState int

const (
	bindingUnbound bindingState = iota
	bindingActive
	bindingInactive
)

// classifyBinding returns the caller's binding state together with the active
// worktree path (empty unless bindingActive). One lock acquisition covers the
// classification so a concurrent enter/exit cannot flip the state mid-check.
func (a *Actor) classifyBinding(callerID string) (bindingState, string) {
	if callerID == "" {
		return bindingUnbound, ""
	}
	a.bindingMu.RLock()
	wtID, ok := a.agentWorktree[callerID]
	a.bindingMu.RUnlock()
	if !ok {
		return bindingUnbound, ""
	}
	a.worktreeParentMu.RLock()
	wt, exists := a.worktrees[wtID]
	a.worktreeParentMu.RUnlock()
	if !exists || wt.Status != "active" {
		return bindingInactive, ""
	}
	return bindingActive, wt.Path
}

// denyUnboundSpawnChild fail-closes git write handlers for spawned child
// agents (agentParent recorded) whose worktree binding entry has vanished
// (e.g. worktree metadata cleanup removed it while the agent lives). Without
// this guard the handler would fall through to the main repo root and commit
// there — the exact master-pollution incident this prevents. Root agents (no
// parent record) keep main-root git access; a child with an entry that merely
// points at an inactive worktree is already rejected via errWorktreeInactive.
func (a *Actor) denyUnboundSpawnChild(callable, cid string) error {
	if cid == "" {
		return nil
	}
	a.worktreeParentMu.RLock()
	parent := a.agentParent[cid]
	a.worktreeParentMu.RUnlock()
	a.bindingMu.RLock()
	_, bound := a.agentWorktree[cid]
	a.bindingMu.RUnlock()
	if parent == "" || bound {
		return nil
	}
	return fmt.Errorf("%s: spawned worker (parent %s) lost its worktree binding; refusing to operate on the main repo root", callable, parent)
}

// errWorktreeInactive is returned when a bound caller's worktree is not active
// (stale / pending / missing). Such callers are denied all file/shell/git
// operations rather than silently demoted to main-repo access.
// rootPathFor returns the primary root path for the caller. Used by git/shell
// handlers to route to the caller's bound worktree. A bound caller whose
// worktree is not active is rejected (no fallthrough to the main repo, which
// would leak isolation); it must exit before operating again.
func (a *Actor) rootPathFor(callerID string) (string, error) {
	switch state, wtPath := a.classifyBinding(callerID); state {
	case bindingActive:
		return wtPath, nil
	case bindingInactive:
		return "", errWorktreeInactive
	}
	roots := a.effectiveRoots(callerID)
	if len(roots) == 0 {
		return "", fmt.Errorf("project: no roots configured")
	}
	return roots[0].Path, nil
}

// resolvePathFor resolves a caller path against callerID's effective roots.
// Mirror of resolvePath but scoped to the caller's bound worktree when present.
func (a *Actor) resolvePathFor(callerID, p string) (domain.RootDirEntry, string, error) {
	roots := a.effectiveRoots(callerID)
	if len(roots) == 0 {
		return domain.RootDirEntry{}, "", fmt.Errorf("project: no roots configured")
	}
	p = filepath.Clean(p)

	// Explicit root-name prefix.
	for _, r := range roots {
		prefix := r.Name + string(filepath.Separator)
		if strings.HasPrefix(p, prefix) || p == r.Name {
			rel := strings.TrimPrefix(p, prefix)
			if rel == "" {
				rel = "."
			}
			return r, rel, nil
		}
	}

	// Absolute path inside a configured root.
	if filepath.IsAbs(p) {
		for _, r := range roots {
			absRoot, err := filepath.Abs(r.Path)
			if err != nil {
				continue
			}
			if absRoot == p || strings.HasPrefix(p, absRoot+string(filepath.Separator)) {
				rel, err := filepath.Rel(absRoot, p)
				if err != nil {
					continue
				}
				return r, rel, nil
			}
		}
		return domain.RootDirEntry{}, "", fmt.Errorf("%w: %q", errOutsideRoots, p)
	}

	return roots[0], p, nil
}

// ── Symlink/junction detection ──

// detectSymlinkInWalk walks dir recursively and returns an error if any
// symlink or reparse point (junction) is found. This is a security check:
// symlinks inside a worktree can escape the sandbox or inject malicious
// content via git worktree add.
func detectSymlinkInWalk(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink found: %s", path)
		}
		if info.Mode()&os.ModeDevice != 0 {
			return fmt.Errorf("non-regular special file found: %s", path)
		}
		return nil
	})
}

// ── Child worktree stale marking ──

// markChildWorktreesStale recursively marks all direct children of the given
// worktree as stale. This is called when a parent worktree is being abandoned
// (owner agent destroyed without workflow_stop) so that any child worktrees
// that depended on it are not accidentally used. The caller must hold
// worktreeParentMu (write lock).
func (a *Actor) markChildWorktreesStale(parentID string) {
	for id, wt := range a.worktrees {
		if wt.ParentWorktreeID == parentID && wt.Status == "active" {
			wt.Status = "stale"
			a.worktrees[id] = wt
		}
	}
}

// ── New callable handlers (Phase 2: workflow-worktree integration) ──

// handleWorkflowCreateWorktree creates a workflow owner worktree, binds the
// owner agent to it, and stamps data.ownerWorktreeId into the workflow map
// card's frontmatter so the binding survives restart. Called by agent actor
// during activateWorkflow.
func (a *Actor) handleWorkflowCreateWorktree(ctx actor.PureContext, req gen.ProjectWorkflowCreateWorktreeReq) (gen.ProjectWorkflowCreateWorktreeResp, error) {
	if req.WorkflowMapID == "" {
		return gen.ProjectWorkflowCreateWorktreeResp{}, fmt.Errorf("project.workflow_create_worktree: WorkflowMapID required")
	}
	if req.AgentActorID == "" {
		return gen.ProjectWorkflowCreateWorktreeResp{}, fmt.Errorf("project.workflow_create_worktree: AgentActorID required")
	}
	// No-git mode (explicitly set via project.no_git_mode_set, or auto-detected
	// because the project root has no git repository) skips owner-worktree
	// creation. The empty WorktreeID is stored by activateWorkflow into
	// ActiveWorkflow.WorktreeID, and every downstream step already branches on
	// an empty ID (workflow_stop merge, goal auto-merge, worktree status
	// refresh, clean check), so no git operation runs for the workflow.
	root, rootErr := a.rootPath()
	if rootErr != nil || a.noGitMode() || !isGitRepo(root) {
		return gen.ProjectWorkflowCreateWorktreeResp{WorktreeID: ""}, nil
	}
	name := "wf-" + req.WorkflowMapID
	// Stamp-anchored attach: data.ownerWorktreeId on the map card is the
	// authoritative source of truth. A live (or stale-but-on-disk) stamped
	// worktree re-attaches — reactivating a parked entry, rebinding the owner
	// agent (repairing a lost agentWorktree entry) and healing the stamp. This
	// is the retry path after a failed workflow_stop left the worktree in
	// place.
	if stamped := a.mapOwnerWorktreeID(req.WorkflowMapID); stamped != "" {
		if resp, usable, err := a.reattachOwnerWorktree(ctx, req.WorkflowMapID, req.AgentActorID, stamped); usable || err != nil {
			return resp, err
		}
		// Stamp whose worktree is gone (merged, discarded, or physically
		// deleted) is dangling metadata — fall through; a fresh create
		// overwrites the stale stamp.
	}
	// Caller-binding heal: the owner is still bound to the map's worktree
	// although the card stamp never landed (stamp failures are logged
	// non-fatally at creation). Reuse the binding and heal the stamp now so
	// the two anchors converge.
	if wtID, ok := a.callerBoundWorktree(req.AgentActorID); ok {
		a.worktreeParentMu.RLock()
		wt, exists := a.worktrees[wtID]
		match := exists && wt.WorkflowMapID == req.WorkflowMapID
		a.worktreeParentMu.RUnlock()
		if match {
			if resp, usable, err := a.reattachOwnerWorktree(ctx, req.WorkflowMapID, req.AgentActorID, wtID); usable || err != nil {
				return resp, err
			}
		}
	}
	// Metadata-anchored reuse: the map's owner worktree exists in project
	// metadata — its card stamp was lost (a failed workflow_stop cleared it
	// after a partial merge) or its owner agent died and
	// releaseWorktreeBinding parked it as stale — but the worktree is still
	// live on disk. Re-adopt it instead of failing on the deterministic
	// wf-<mapID> name collision. Only an owner worktree (no parent) is
	// considered; children carry the same WorkflowMapID but belong to a
	// spawned worker. A worktree that is genuinely gone falls through to the
	// dangling cleanup + fresh create.
	if wtID := a.findMapOwnerWorktree(req.WorkflowMapID, name); wtID != "" {
		if resp, usable, err := a.reattachOwnerWorktree(ctx, req.WorkflowMapID, req.AgentActorID, wtID); usable || err != nil {
			return resp, err
		}
	}
	// The owner worktree name is deterministic, so a previous run that was
	// physically deleted while leaving the project metadata entry and/or the
	// branch ref behind blocks re-activation (branch uniqueness check /
	// git worktree add -b both fail with "already exists"). Remove those
	// dangling references first; a worktree that still physically exists is
	// a real conflict and is left untouched so it surfaces as an error.
	a.cleanupDanglingOwnerWorktree(ctx, name)
	baseRef := req.BaseRef
	if baseRef == "" {
		baseRef = "HEAD"
	}
	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:          name,
		BaseRef:       baseRef,
		WorkflowMapID: req.WorkflowMapID,
	})
	if err != nil {
		return gen.ProjectWorkflowCreateWorktreeResp{}, fmt.Errorf("project.workflow_create_worktree: %w", err)
	}
	// Bind the owner agent to the newly created worktree.
	if err := a.bindWorktree(req.AgentActorID, wt.ID); err != nil {
		_ = a.discardWorktreeByID(ctx, wt.ID, true)
		return gen.ProjectWorkflowCreateWorktreeResp{}, fmt.Errorf("project.workflow_create_worktree: bind: %w", err)
	}
	if err := a.persistWorktreeManifest(wt.ID); err != nil {
		ctx.Logger().Error("project.workflow_create_worktree: persist manifest", "error", err)
	}
	// Stamp data.ownerWorktreeId into the map card frontmatter so the
	// owner-worktree binding survives agent restart and is queryable from the
	// card itself. Failure here is logged but does not fail activation: the
	// worktree is created and the agent is bound; restart recovery reconciles
	// worktree metadata from git.
	if err := a.stampMapOwnerWorktree(ctx, req.WorkflowMapID, wt.ID); err != nil {
		ctx.Logger().Error("project.workflow_create_worktree: stamp map ownerWorktreeId", "error", err)
	}
	return gen.ProjectWorkflowCreateWorktreeResp{WorktreeID: wt.ID}, nil
}

// findMapOwnerWorktree searches project metadata for the owner worktree of
// mapID when both card stamp and agent binding are missing — the "lost
// anchor" recovery path. A match must be an owner entry (ParentWorktreeID
// empty — children spawned for the same map carry the same WorkflowMapID but
// have a parent) that is active or stale, and either carries the map's ID or
// was created under the deterministic wf-<mapID> name/branch (covering
// entries from older sessions or reverse discovery that lack the map link).
// Returns "" when no such entry exists.
func (a *Actor) findMapOwnerWorktree(mapID, name string) string {
	a.worktreeParentMu.RLock()
	defer a.worktreeParentMu.RUnlock()
	for id, wt := range a.worktrees {
		if wt.ParentWorktreeID != "" || (wt.Status != "active" && wt.Status != "stale") {
			continue
		}
		if wt.WorkflowMapID == mapID || wt.Name == name || wt.Branch == name {
			return id
		}
	}
	return ""
}

// reattachOwnerWorktree re-adopts an existing owner worktree for a new
// workflow activation. It handles three states:
//
//   - active + on disk: rebind the agent and heal the stamp.
//   - stale + on disk: reactivate (status → active), rebind, heal the stamp.
//     This is the path for a worktree parked stale by releaseWorktreeBinding
//     when the owner agent died between workflow_stop and workflow_start.
//   - gone from disk: not usable; the caller falls through to cleanup +
//     fresh create.
//
// Returns (resp, true, nil) when the worktree was re-attached, (resp, false,
// nil) when it is not on disk and the caller should proceed to create, and
// (resp, false, err) on a hard failure (bind error). The stamp is healed
// best-effort: a failure to write the card does not fail re-attachment — the
// binding is the operational anchor and a missing stamp self-heals on the
// next retry.
func (a *Actor) reattachOwnerWorktree(ctx actor.PureContext, mapID, agentID, worktreeID string) (gen.ProjectWorkflowCreateWorktreeResp, bool, error) {
	a.worktreeParentMu.Lock()
	wt, exists := a.worktrees[worktreeID]
	a.worktreeParentMu.Unlock()
	if !exists {
		return gen.ProjectWorkflowCreateWorktreeResp{}, false, nil
	}
	if _, statErr := os.Stat(wt.Path); statErr != nil {
		return gen.ProjectWorkflowCreateWorktreeResp{}, false, nil // gone from disk
	}
	// Reactivate a stale worktree: the directory is live, so the entry was
	// parked by releaseWorktreeBinding (owner agent died). Bring it back.
	if wt.Status == "stale" {
		wt.Status = "active"
		wt.LastUsedAt = time.Now().UTC().Format(time.RFC3339Nano)
		a.worktreeParentMu.Lock()
		a.worktrees[worktreeID] = wt
		a.worktreeParentMu.Unlock()
	}
	if err := a.bindWorktree(agentID, worktreeID); err != nil {
		return gen.ProjectWorkflowCreateWorktreeResp{}, false, fmt.Errorf("project.workflow_create_worktree: re-attach: %w", err)
	}
	if err := a.persistWorktreeManifest(worktreeID); err != nil {
		ctx.Logger().Error("project.workflow_create_worktree: persist manifest", "error", err)
	}
	if err := a.stampMapOwnerWorktree(ctx, mapID, worktreeID); err != nil {
		ctx.Logger().Error("project.workflow_create_worktree: heal map ownerWorktreeId", "error", err)
	}
	return gen.ProjectWorkflowCreateWorktreeResp{WorktreeID: worktreeID}, true, nil
}

// stampMapOwnerWorktree writes data.ownerWorktreeId into the workflow map
// card's frontmatter via the same data-block mechanism used by
// handleWikiSetMapOwner. The store is owned by the project actor, so the write
// is serialized with other card mutations.
func (a *Actor) stampMapOwnerWorktree(ctx actor.PureContext, mapID, worktreeID string) error {
	existing, err := a.store.Get(mapID)
	if err != nil {
		return err
	}
	raw := setKeyInDataBlock(existing.Raw, "ownerWorktreeId", worktreeID)
	if raw == existing.Raw {
		return nil // already stamped (idempotent re-activation)
	}
	if err := validateCard(mapID, raw); err != nil {
		return err
	}
	raw = ensureCardMeta(mapID, raw, time.Now().UTC().Format(time.RFC3339))
	card := &CardRecord{Title: mapID, Raw: raw}
	if err := a.store.Save(card); err != nil {
		return err
	}
	saved, _ := a.store.Get(mapID)
	_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
		ID:       saved.Title,
		Modified: saved.Modified,
	})
	return nil
}

// clearMapOwnerWorktree removes data.ownerWorktreeId from the workflow map
// card once the owner worktree has been merged away. Best-effort: a stale
// stamp self-heals on the next workflow_create_worktree (the dangling
// cleanup path), so a store failure here is logged, not propagated.
func (a *Actor) clearMapOwnerWorktree(mapID string) {
	existing, err := a.store.Get(mapID)
	if err != nil || existing == nil {
		return
	}
	raw := removeKeyFromDataBlock(existing.Raw, "ownerWorktreeId")
	if raw == existing.Raw {
		return
	}
	if err := validateCard(mapID, raw); err != nil {
		if a.logger != nil {
			a.logger.Warn("project: clear ownerWorktreeId stamp: invalid card", "map", mapID, "error", err)
		}
		return
	}
	raw = ensureCardMeta(mapID, raw, time.Now().UTC().Format(time.RFC3339))
	if err := a.store.Save(&CardRecord{Title: mapID, Raw: raw}); err != nil && a.logger != nil {
		a.logger.Warn("project: clear ownerWorktreeId stamp", "map", mapID, "error", err)
	}
}

// cleanupDanglingOwnerWorktree removes references left behind by a workflow
// owner worktree that was physically deleted (gone from git's worktree list
// and from disk) while the project metadata entry and/or the branch ref
// still exist. Such references block workflow re-activation: the branch
// uniqueness check in handleWorktreeCreate rejects the deterministic
// wf-<mapID> name, and git worktree add -b fails on the leftover branch ref.
// A worktree that still physically exists is never touched — that is a real
// conflict and must surface as an error instead of being cleaned up.
func (a *Actor) cleanupDanglingOwnerWorktree(ctx actor.PureContext, name string) {
	root, err := a.rootPath()
	if err != nil {
		return
	}
	live, err := gitWorktreeList(root)
	if err != nil {
		return
	}
	livePaths := make(map[string]bool, len(live))
	liveBranches := make(map[string]bool, len(live))
	for _, e := range live {
		livePaths[filepath.Clean(e.Path)] = true
		if e.Branch != "" {
			liveBranches[e.Branch] = true
		}
	}

	a.worktreeParentMu.Lock()
	a.bindingMu.Lock()
	removedIDs := make(map[string]bool)
	for id, wt := range a.worktrees {
		if wt.Branch != name && wt.Name != name {
			continue
		}
		if livePaths[filepath.Clean(wt.Path)] {
			continue // still tracked by git — real conflict, do not touch
		}
		if _, statErr := os.Stat(wt.Path); statErr == nil {
			continue // directory still on disk — do not touch
		}
		delete(a.worktrees, id)
		removedIDs[id] = true
		for agentID, bound := range a.agentWorktree {
			if bound == id {
				delete(a.agentWorktree, agentID)
				delete(a.agentParent, agentID)
			}
		}
	}
	a.bindingMu.Unlock()
	a.worktreeParentMu.Unlock()
	if len(removedIDs) > 0 {
		var delErrs []error
		for id := range removedIDs {
			if err := a.deleteWorktreeManifest(id); err != nil {
				delErrs = append(delErrs, err)
			}
		}
		if err := errors.Join(delErrs...); err != nil {
			ctx.Logger().Error("project.workflow_create_worktree: delete dangling worktree manifests failed", "error", err)
		} else {
			ctx.Logger().Info("project.workflow_create_worktree: removed dangling worktree references",
				"branch", name, "entries", len(removedIDs))
		}
	}
	// A leftover branch ref with no live worktree on it also blocks creation
	// (git worktree add -b fails with "branch already exists"). Drop it so
	// the fresh worktree can be created. deleteWorktreeBranchRef skips the
	// repo's default branch, so the main checkout is never at risk.
	if !liveBranches[name] {
		if _, err := gitRun(nil, root, "show-ref", "--verify", "--quiet", "refs/heads/"+name); err == nil {
			deleteWorktreeBranchRef(root, name)
			ctx.Logger().Info("project.workflow_create_worktree: removed dangling worktree branch ref", "branch", name)
		}
	}
}

// handleWorktreeMergeToParent merges a child worktree's branch into its parent
// worktree. Called by workspace actor during review approve.
func (a *Actor) handleWorktreeMergeToParent(_ actor.PureContext, req gen.ProjectWorktreeMergeToParentReq) (gen.ProjectWorktreeMergeToParentResp, error) {
	if req.ChildWorktreeID == "" || req.ParentWorktreeID == "" {
		return gen.ProjectWorktreeMergeToParentResp{}, fmt.Errorf("project.worktree_merge_to_parent: ChildWorktreeID and ParentWorktreeID required")
	}
	merged, conflictFiles, err := a.mergeChildIntoParent(req.ChildWorktreeID, req.ParentWorktreeID)
	if err != nil {
		if errors.Is(err, errWorktreeGone) {
			// Child already removed by an earlier completion (its registry
			// entry is deleted exactly by that path). Report "gone" without
			// an error so review approve proceeds to teardown instead of
			// bouncing the card with a false-positive conflict.
			return gen.ProjectWorktreeMergeToParentResp{Status: "gone"}, nil
		}
		return gen.ProjectWorktreeMergeToParentResp{Status: "conflict", ConflictFiles: conflictFiles}, err
	}
	if !merged {
		return gen.ProjectWorktreeMergeToParentResp{Status: "conflict", ConflictFiles: conflictFiles}, fmt.Errorf("project.worktree_merge_to_parent: merge failed")
	}
	return gen.ProjectWorktreeMergeToParentResp{Status: "merged"}, nil
}

// handleWorktreeRebaseToParent rebases a child worktree's branch onto the
// parent worktree's branch HEAD. Called by workspace actor during review
// reject (to incorporate parent's latest changes).
func (a *Actor) handleWorktreeRebaseToParent(_ actor.PureContext, req gen.ProjectWorktreeRebaseToParentReq) (gen.ProjectWorktreeRebaseToParentResp, error) {
	if req.ChildWorktreeID == "" || req.ParentWorktreeID == "" {
		return gen.ProjectWorktreeRebaseToParentResp{}, fmt.Errorf("project.worktree_rebase_to_parent: ChildWorktreeID and ParentWorktreeID required")
	}
	conflictFiles, err := a.rebaseToParent(req.ChildWorktreeID, req.ParentWorktreeID)
	if err != nil {
		return gen.ProjectWorktreeRebaseToParentResp{Status: "conflict", ConflictFiles: conflictFiles}, err
	}
	return gen.ProjectWorktreeRebaseToParentResp{Status: "rebased"}, nil
}

// handleWorktreeVerifyMergedToParent checks whether the child worktree's branch
// has already been merged into its parent. When it has, it runs the same cleanup
// as a successful merge (remove worktree, drop branch, update registry) and
// reports "merged". Otherwise it reports "not_merged" without mutating anything.
// "gone" means the child worktree is already absent or its checkout is dead.
func (a *Actor) handleWorktreeVerifyMergedToParent(_ actor.PureContext, req gen.ProjectWorktreeVerifyMergedReq) (gen.ProjectWorktreeVerifyMergedResp, error) {
	if req.ChildWorktreeID == "" || req.ParentWorktreeID == "" {
		return gen.ProjectWorktreeVerifyMergedResp{}, fmt.Errorf("project.worktree_verify_merged_to_parent: ChildWorktreeID and ParentWorktreeID required")
	}
	a.worktreeParentMu.RLock()
	child, childOK := a.worktrees[req.ChildWorktreeID]
	parent, parentOK := a.worktrees[req.ParentWorktreeID]
	a.worktreeParentMu.RUnlock()

	if !childOK {
		return gen.ProjectWorktreeVerifyMergedResp{Status: "gone"}, nil
	}
	if !parentOK {
		return gen.ProjectWorktreeVerifyMergedResp{}, fmt.Errorf("project.worktree_verify_merged_to_parent: parent worktree %q not found", req.ParentWorktreeID)
	}
	if !isGitWorkingTree(child.Path) {
		a.purgeWorktreeEntry(req.ChildWorktreeID)
		return gen.ProjectWorktreeVerifyMergedResp{Status: "gone"}, nil
	}

	childHead, err := gitRevParseHead(child.Path)
	if err != nil {
		return gen.ProjectWorktreeVerifyMergedResp{}, fmt.Errorf("project.worktree_verify_merged_to_parent: resolve child HEAD: %w", err)
	}
	parentHead, err := gitRevParseHead(parent.Path)
	if err != nil {
		return gen.ProjectWorktreeVerifyMergedResp{}, fmt.Errorf("project.worktree_verify_merged_to_parent: resolve parent HEAD: %w", err)
	}

	// Ensure merge.ours.driver is configured idempotently.
	if root, err := a.rootPath(); err == nil {
		_, _ = gitRun(nil, root, "config", "merge.ours.driver", "true")
	}

	merged := false
	if childHead == parentHead {
		merged = true
	} else {
		isAncestor, err := gitIsAncestor(parent.Path, childHead, parentHead)
		if err != nil {
			return gen.ProjectWorktreeVerifyMergedResp{}, fmt.Errorf("project.worktree_verify_merged_to_parent: ancestry check: %w", err)
		}
		merged = isAncestor
	}
	if merged {
		if _, _, err := a.cleanupMergedChild(req.ChildWorktreeID, req.ParentWorktreeID, child, parent); err != nil {
			return gen.ProjectWorktreeVerifyMergedResp{}, fmt.Errorf("project.worktree_verify_merged_to_parent: cleanup merged child: %w", err)
		}
		return gen.ProjectWorktreeVerifyMergedResp{
			Status:     "merged",
			Branch:     child.Branch,
			ChildHead:  childHead,
			ParentHead: parentHead,
		}, nil
	}

	return gen.ProjectWorktreeVerifyMergedResp{
		Status:     "not_merged",
		Branch:     child.Branch,
		ChildHead:  childHead,
		ParentHead: parentHead,
	}, nil
}

// handleWorkflowWorktreeCleanCheck reports whether the worktree bound to the
// given agent has uncommitted changes. No binding is treated as clean.
func (a *Actor) handleWorkflowWorktreeCleanCheck(_ actor.PureContext, req gen.ProjectWorktreeCleanCheckReq) (gen.ProjectWorktreeCleanCheckResp, error) {
	_, wtPath, err := a.resolveWorktreeForAgent(req.AgentActorID)
	if err != nil {
		return gen.ProjectWorktreeCleanCheckResp{Clean: true}, nil
	}
	dirty, err := gitWorktreeHasUncommitted(wtPath)
	if err != nil {
		return gen.ProjectWorktreeCleanCheckResp{}, fmt.Errorf("project.workflow_worktree_clean_check: check dirty: %w", err)
	}
	if !dirty {
		return gen.ProjectWorktreeCleanCheckResp{Clean: true}, nil
	}
	files, err := gitDirtyFiles(wtPath, 10)
	if err != nil {
		return gen.ProjectWorktreeCleanCheckResp{}, fmt.Errorf("project.workflow_worktree_clean_check: list dirty files: %w", err)
	}
	return gen.ProjectWorktreeCleanCheckResp{Clean: false, DirtyFiles: files}, nil
}

// handleWorkflowStopMergeWorktree merges a workflow owner worktree into the
// main repo's base branch, discarding the worktree after a successful merge.
// Called by agent actor during clearWorkflow.
//
// Two modes share this callable. RequireSynced=false (the goal-completion
// auto-merge path) rebases the worktree onto the latest base first — no agent
// turn follows that path, so it must be automatic. RequireSynced=true
// (workflow_stop) never rebases: the owner is expected to have rebased its
// worktree itself (resolving conflicts with full context and running tests
// before declaring the goal met). The handler verifies the worktree branch
// contains the latest base HEAD and rejects with Status="outdated" otherwise.
func (a *Actor) handleWorkflowStopMergeWorktree(ctx actor.PureContext, req gen.ProjectWorkflowStopMergeWorktreeReq) (gen.ProjectWorkflowStopMergeWorktreeResp, error) {
	if req.WorktreeID == "" {
		return gen.ProjectWorkflowStopMergeWorktreeResp{}, fmt.Errorf("project.workflow_stop_merge_worktree: WorktreeID required")
	}
	switch {
	case req.RequireSynced:
		// Verify the owner already rebased: the worktree branch must contain
		// the latest base HEAD. After this check the merge below cannot
		// conflict (barring a concurrent base advance, which still surfaces
		// as "conflict" and can be retried).
		if err := a.requireWorktreeContainsBase(req.WorktreeID, req.BaseBranch); err != nil {
			return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "outdated"}, fmt.Errorf("project.workflow_stop_merge_worktree: %w", err)
		}
	default:
		// Rebase the owner worktree onto the latest main HEAD first so the merge
		// back into the main repo stays fast-forward where possible and picks up
		// concurrent main changes (parallel workflows merged meanwhile).
		if err := a.rebaseWorktreeToMain(req.WorktreeID, req.BaseBranch); err != nil {
			return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "conflict"}, fmt.Errorf("project.workflow_stop_merge_worktree: rebase to main: %w", err)
		}
	}
	// Then merge the worktree branch into the main repo. residue != "" means
	// the merge landed but the directory is locked; the workflow can still
	// complete — the leftover is swept in the background.
	residue, err := a.mergeWorktreeIntoBase(ctx, req.WorktreeID, req.BaseBranch)
	if err != nil {
		return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "conflict"}, fmt.Errorf("project.workflow_stop_merge_worktree: %w", err)
	}
	// Clean up all agent bindings pointing at the merged worktree so the
	// owner (and any other bound agents) are not left with a stale binding
	// that blocks file/git operations or prevents re-entering a new worktree.
	a.unbindAllForWorktree(ctx, req.WorktreeID)
	return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "merged", ResiduePath: residue}, nil
}

// rebaseWorktreeToMain rebases the given worktree's branch onto the latest
// main/master HEAD so workflow_stop's merge into the base branch incorporates
// main's newest commits (including merges from parallel workflows). On any
// failure it aborts the rebase and returns the error, leaving the worktree
// intact for manual conflict resolution.
func (a *Actor) rebaseWorktreeToMain(worktreeID, baseBranch string) error {
	a.worktreeParentMu.RLock()
	wt, ok := a.worktrees[worktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return fmt.Errorf("worktree %q not found", worktreeID)
	}
	root, err := a.rootPath()
	if err != nil {
		return err
	}
	base := strings.TrimSpace(baseBranch)
	if base == "" {
		base, err = defaultBranch(root)
		if err != nil {
			return err
		}
	}
	// A clean worktree is required: rebase would refuse dirty changes anyway,
	// and the subsequent merge requires cleanliness too. Fail early with a
	// clear message instead of a bare git error.
	if dirty, err := gitWorktreeHasUncommitted(wt.Path); err != nil {
		return fmt.Errorf("check worktree clean: %w", err)
	} else if dirty {
		return fmt.Errorf("worktree %q has uncommitted changes; commit or discard them before stop", wt.Name)
	}
	// If base has not advanced since the worktree was created, it is already an
	// ancestor of the worktree HEAD. Skipping rebase preserves the owner's
	// commit identities; rebase is only needed when parallel work advanced base.
	baseIsAncestor, err := gitIsAncestor(wt.Path, base, "HEAD")
	if err != nil {
		return fmt.Errorf("check whether worktree %q contains %q: %w", wt.Name, base, err)
	}
	if baseIsAncestor {
		return nil
	}
	if _, err := gitRun(nil, wt.Path, "rebase", base); err != nil {
		// Conflict: abort to restore the worktree untouched; the owner can
		// resolve manually and retry workflow_stop.
		_, _ = gitRun(nil, wt.Path, "rebase", "--abort")
		return fmt.Errorf("rebase worktree %q onto %q: %w", wt.Name, base, err)
	}
	return nil
}

// requireWorktreeContainsBase verifies the worktree's branch already contains
// the latest base HEAD — i.e. the owner rebased (or base never advanced). It
// performs no git state changes: workflow_stop's RequireSynced mode relies on
// the owner doing the rebase itself so conflicts are resolved with full agent
// context, not aborted blindly by a one-shot syscall.
func (a *Actor) requireWorktreeContainsBase(worktreeID, baseBranch string) error {
	a.worktreeParentMu.RLock()
	wt, ok := a.worktrees[worktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return fmt.Errorf("worktree %q not found", worktreeID)
	}
	root, err := a.rootPath()
	if err != nil {
		return err
	}
	base := strings.TrimSpace(baseBranch)
	if base == "" {
		if base, err = defaultBranch(root); err != nil {
			return err
		}
	}
	contains, err := gitIsAncestor(wt.Path, base, "HEAD")
	if err != nil {
		return fmt.Errorf("check whether worktree %q contains %q: %w", wt.Name, base, err)
	}
	if !contains {
		return fmt.Errorf("worktree %q does not contain the latest %q HEAD; commit all work, rebase the worktree onto %q (git rebase %s), resolve conflicts, verify tests, then retry workflow_stop", wt.Name, base, base, base)
	}
	return nil
}

// handleWorktreeDiscardByID discards a worktree by ID. Unlike
// handleWorktreeDiscard, this is an Internal callable that returns a
// structured response. Called by workspace actor for orphan worktree cleanup.
func (a *Actor) handleWorktreeDiscardByID(ctx actor.PureContext, req gen.ProjectWorktreeDiscardByIDReq) (gen.ProjectWorktreeDiscardByIDResp, error) {
	if req.WorktreeID == "" {
		return gen.ProjectWorktreeDiscardByIDResp{}, fmt.Errorf("project.worktree_discard_by_id: WorktreeID required")
	}
	if err := a.discardWorktreeByID(ctx, req.WorktreeID, req.Force); err != nil {
		return gen.ProjectWorktreeDiscardByIDResp{}, fmt.Errorf("project.worktree_discard_by_id: %w", err)
	}
	return gen.ProjectWorktreeDiscardByIDResp{Status: "discarded"}, nil
}
