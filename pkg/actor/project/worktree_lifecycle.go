package project

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"

	"github.com/qomos-w/spore/identity"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ── Self-service worktree lifecycle (enter / exit) ──
//
// These are the agent-facing entry points that make a worktree a loadable,
// in-session state. enter() self-binds the caller (creating a worktree when
// none exists for the name); exit() returns the caller to the main repo via
// one of two paths:
//   - discard: delete the worktree and its branch (reuses the discard logic);
//   - merge:   land the worktree branch onto the main repo's base branch, then
//     delete the worktree.

// handleWorktreeEnter binds the caller to a worktree, creating one when the
// caller is unbound. It is idempotent: a caller already bound to a usable
// worktree gets that worktree back with Created=false. The worktree is
// per-agent exclusive: its name/branch is always <callerID>-<random>, so it
// never collides (even after exit+re-enter, since the random suffix differs
// and a discarded worktree's branch may linger). The Name parameter is ignored.
func (a *Actor) handleWorktreeEnter(ctx actor.PureContext, req gen.ProjectWorktreeEnterReq) (gen.ProjectWorktreeEnterResp, error) {
	cid := callerID(ctx)
	if cid == "" {
		return gen.ProjectWorktreeEnterResp{}, fmt.Errorf("project.worktree.enter: no caller (system/internal calls cannot self-bind a worktree)")
	}

	// 1. Already bound to a usable worktree? Idempotent return.
	if wtID, ok := a.callerBoundWorktree(cid); ok {
		a.worktreeParentMu.RLock()
		wt, exists := a.worktrees[wtID]
		a.worktreeParentMu.RUnlock()
		if exists && wt.Status == "active" {
			return gen.ProjectWorktreeEnterResp{Worktree: wt, Created: false}, nil
		}
	}

	// 2. Per-agent exclusive worktree: name = <callerID>-<random>. The Name
	//    parameter is intentionally ignored — isolation is per-agent, not
	//    share-by-name.
	name := cid + "-" + shortID()

	// 3. Create a fresh worktree and bind the caller.
	wt, err := a.handleWorktreeCreate(ctx, gen.ProjectWorktreeCreateReq{Name: name, BaseRef: req.BaseRef})
	if err != nil {
		return gen.ProjectWorktreeEnterResp{}, fmt.Errorf("project.worktree.enter: %w", err)
	}
	if err := a.bindWorktree(cid, wt.ID); err != nil {
		// Best-effort cleanup of the freshly created worktree on bind failure.
		_ = a.discardWorktreeByID(ctx, wt.ID, true)
		return gen.ProjectWorktreeEnterResp{}, fmt.Errorf("project.worktree.enter: %w", err)
	}
	// The create persisted the metadata; rewrite so the manifest also carries
	// the new agent binding.
	if err := a.persistWorktreeManifest(wt.ID); err != nil {
		ctx.Logger().Error("project.worktree.enter: persist manifest", "error", err)
	}
	a.notifyBoundAgentStatus(ctx, cid)
	return gen.ProjectWorktreeEnterResp{Worktree: wt, Created: true}, nil
}

// handleWorktreeExit returns the caller to the main repo. discard deletes the
// worktree outright; merge lands its branch on the main repo's base branch.
func (a *Actor) handleWorktreeExit(ctx actor.PureContext, req gen.ProjectWorktreeExitReq) (gen.ProjectWorktreeExitResp, error) {
	cid := callerID(ctx)
	if cid == "" {
		return gen.ProjectWorktreeExitResp{}, fmt.Errorf("project.worktree.exit: no caller")
	}
	// Spawned workers cannot exit their worktree — the lifecycle is managed
	// by the workflow owner. Without this check a worker could call
	// project.worktree_exit to unbind itself, fall to bindingUnbound, and
	// silently write to the main repo root.
	a.worktreeParentMu.RLock()
	parent := a.agentParent[cid]
	a.worktreeParentMu.RUnlock()
	if parent != "" {
		return gen.ProjectWorktreeExitResp{}, fmt.Errorf("project.worktree.exit: spawned worker (parent %s) cannot exit its worktree; lifecycle is managed by the workflow owner", parent)
	}
	wtID, ok := a.callerBoundWorktree(cid)
	if !ok {
		// Already unbound: nothing to exit. Return success so callers can
		// retry safely after a stale-binding cleanup without a cascade of
		// errors.
		return gen.ProjectWorktreeExitResp{Status: "completed"}, nil
	}
	a.worktreeParentMu.RLock()
	_, exists := a.worktrees[wtID]
	a.worktreeParentMu.RUnlock()
	if !exists {
		// Stale binding: the worktree was already removed (e.g. via admin
		// discard or merge). Clear the binding and return success — the
		// caller's intent (exit the worktree) is already satisfied.
		a.unbindCaller(cid)
		a.notifyBoundAgentStatus(ctx, cid)
		return gen.ProjectWorktreeExitResp{WorktreeID: wtID, Mode: req.Mode, Status: "completed"}, nil
	}

	switch req.Mode {
	case "discard":
		if err := a.discardWorktreeByID(ctx, wtID, req.Force); err != nil {
			return gen.ProjectWorktreeExitResp{}, fmt.Errorf("project.worktree.exit: %w", err)
		}
		a.unbindCaller(cid)
		a.notifyBoundAgentStatus(ctx, cid)
		return gen.ProjectWorktreeExitResp{WorktreeID: wtID, Mode: "discard", Status: "completed"}, nil

	case "merge":
		// When ParentWorktreeID is set, merge to parent worktree instead of
		// the main repo's base branch.
		if req.ParentWorktreeID != "" {
			_, _, err := a.mergeChildIntoParent(wtID, req.ParentWorktreeID)
			if err != nil && !errors.Is(err, errWorktreeGone) {
				return gen.ProjectWorktreeExitResp{}, fmt.Errorf("project.worktree.exit: merge to parent failed (worktree kept): %w", err)
			}
			// errWorktreeGone: the child was already removed by an earlier
			// completion — re-merge is a no-op; satisfy the exit intent.
			a.unbindCaller(cid)
			a.notifyBoundAgentStatus(ctx, cid)
			return gen.ProjectWorktreeExitResp{WorktreeID: wtID, Mode: "merge", Status: "completed"}, nil
		}
		// Autonomous: merge now. Conflict → keep worktree, surface error.
		if _, err := a.mergeWorktreeIntoBase(ctx, wtID, req.BaseBranch); err != nil {
			return gen.ProjectWorktreeExitResp{}, fmt.Errorf("project.worktree.exit: merge failed (worktree kept): %w", err)
		}
		a.unbindCaller(cid)
		a.notifyBoundAgentStatus(ctx, cid)
		return gen.ProjectWorktreeExitResp{WorktreeID: wtID, Mode: "merge", Status: "completed"}, nil

	default:
		return gen.ProjectWorktreeExitResp{}, fmt.Errorf("project.worktree.exit: unknown mode %q (want discard|merge)", req.Mode)
	}
}

func (a *Actor) notifyBoundAgentStatus(ctx actor.PureContext, agentActorID string) {
	cid, err := identity.ParseCanonicalID(agentActorID)
	if err != nil {
		return
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	agentRef.Invoke(callCtx, "status_notify", gen.AgentStatusNotifyReq{})
}

// ── helpers ──

// callerBoundWorktree reports the worktreeID the caller is bound to. The
// worktree may be in any status; callers inspect status before acting.
func (a *Actor) callerBoundWorktree(callerID string) (string, bool) {
	if callerID == "" {
		return "", false
	}
	a.bindingMu.RLock()
	defer a.bindingMu.RUnlock()
	wtID, ok := a.agentWorktree[callerID]
	return wtID, ok
}

func (a *Actor) unbindCaller(callerID string) {
	a.bindingMu.Lock()
	delete(a.agentWorktree, callerID)
	a.bindingMu.Unlock()
}

// unbindAllForWorktree removes every agentWorktree and agentParent entry that
// points at the given worktree ID. Called after a workflow_stop merge removes
// the worktree, so stale bindings don't block the owner's subsequent file/git
// operations or prevent re-entering a new worktree.
func (a *Actor) unbindAllForWorktree(ctx actor.PureContext, worktreeID string) {
	var affected []string
	a.worktreeParentMu.Lock()
	a.bindingMu.Lock()
	for agentID, wtID := range a.agentWorktree {
		if wtID == worktreeID {
			delete(a.agentWorktree, agentID)
			if a.agentParent != nil {
				delete(a.agentParent, agentID)
			}
			affected = append(affected, agentID)
		}
	}
	a.bindingMu.Unlock()
	a.worktreeParentMu.Unlock()
	if err := a.deleteWorktreeState(worktreeID); err != nil {
		ctx.Logger().Error("project: delete worktree manifest after unbind failed", "worktree", worktreeID, "error", err)
	}
	for _, agentID := range affected {
		a.notifyBoundAgentStatus(ctx, agentID)
	}
}

// purgeWorktreeEntry drops a worktree's registry entry and every agent
// binding pointing at it, then persists. Used when a worktree is discovered
// already removed (dead checkout) so the registry converges with reality.
func (a *Actor) purgeWorktreeEntry(worktreeID string) {
	a.worktreeParentMu.Lock()
	a.bindingMu.Lock()
	delete(a.worktrees, worktreeID)
	for agentID, wtID := range a.agentWorktree {
		if wtID == worktreeID {
			delete(a.agentWorktree, agentID)
		}
	}
	a.bindingMu.Unlock()
	a.worktreeParentMu.Unlock()
	_ = a.deleteWorktreeState(worktreeID)
}

// markWorktreeResidual flags a worktree whose merge already landed on base but
// whose checkout directory could not be removed (a process still holds a
// handle inside it — the Windows sharing violation that would otherwise wedge
// workflow_stop). The periodic sweep retries the removal until the holders
// release.
func (a *Actor) markWorktreeResidual(worktreeID string) {
	a.worktreeParentMu.Lock()
	if wt, ok := a.worktrees[worktreeID]; ok {
		wt.Status = "residual"
		a.worktrees[worktreeID] = wt
	}
	a.worktreeParentMu.Unlock()
	_ = a.persistWorktreeManifest(worktreeID)
}

// sweepResidualWorktrees retries removal of worktree directories that stayed
// behind after a successful merge because a live process kept a handle locked.
// Called on every timer tick; a no-op when nothing is residual. Converges
// once the holders exit, and then completes the merge teardown (drop the
// branch ref and the registry entry).
func (a *Actor) sweepResidualWorktrees() {
	a.worktreeParentMu.RLock()
	type residualEntry struct {
		id, branch, path, mapID string
	}
	var pending []residualEntry
	for id, wt := range a.worktrees {
		if wt.Status == "residual" {
			pending = append(pending, residualEntry{id: id, branch: wt.Branch, path: wt.Path, mapID: wt.WorkflowMapID})
		}
	}
	a.worktreeParentMu.RUnlock()
	if len(pending) == 0 {
		return
	}
	root, err := a.rootPath()
	if err != nil {
		return
	}
	for _, r := range pending {
		if err := gitWorktreeRemove(root, r.path); err != nil {
			if a.logger != nil {
				a.logger.Warn("project: residual worktree sweep failed; will retry next tick", "path", r.path, "error", err)
			}
			continue
		}
		deleteWorktreeBranchRef(root, r.branch)
		if r.mapID != "" {
			a.clearMapOwnerWorktree(r.mapID)
		}
		a.purgeWorktreeEntry(r.id)
	}
}

// notifyWorktreeHolders asks the shell and lsp services to release processes
// living inside path before the directory is deleted: interactive sessions
// with cwd inside it, and LSP engines rooted under it. On Windows a live
// process handle keeps the directory locked (sharing violation), which used
// to fail workflow_stop after the merge had already landed. Best-effort:
// failures are logged and removal proceeds regardless.
func (a *Actor) notifyWorktreeHolders(ctx actor.PureContext, path string) {
	if ctx == nil || strings.TrimSpace(path) == "" {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	if shellRef, ok := ctx.LookupService("shell"); ok && shellRef != nil {
		if call := shellRef.Invoke(callCtx, "shell.sessions_close_by_path", gen.ShellSessionsCloseByPathReq{Path: path}); call != nil {
			if _, err := call.Final(callCtx); err != nil && a.logger != nil {
				a.logger.Warn("project: close shell sessions in worktree failed", "path", path, "error", err)
			}
		}
	}
	if lspRef, ok := ctx.LookupService("lsp"); ok && lspRef != nil {
		if call := lspRef.Invoke(callCtx, "lsp.shutdown_by_root", gen.LspShutdownByRootReq{RootPath: path}); call != nil {
			if _, err := call.Final(callCtx); err != nil && a.logger != nil {
				a.logger.Warn("project: shut down lsp engines rooted in worktree failed", "path", path, "error", err)
			}
		}
	}
}

// isGitWorkingTree reports whether git still recognizes path as a working
// tree (a linked-worktree checkout holds a .git file and stays registered).
func isGitWorkingTree(path string) bool {
	_, err := gitRun(nil, path, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

// discardWorktreeByID is the shared discard core used by both the AdminOnly
// project.worktree.discard and the self-service exit(discard). It refuses to
// delete a dirty worktree unless force is set.
func (a *Actor) discardWorktreeByID(ctx actor.PureContext, worktreeID string, force bool) error {
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
	if !force {
		dirty, err := gitWorktreeHasUncommitted(wt.Path)
		if err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("worktree has uncommitted changes, requires Force=true")
		}
	}
	// Release processes living inside the worktree before deleting it, so a
	// live handle does not fail the removal with a sharing violation.
	a.notifyWorktreeHolders(ctx, wt.Path)
	if err := gitWorktreeRemove(root, wt.Path); err != nil {
		return err
	}
	deleteWorktreeBranchRef(root, wt.Branch)
	a.worktreeParentMu.Lock()
	delete(a.worktrees, worktreeID)
	a.worktreeParentMu.Unlock()
	a.bindingMu.Lock()
	for agentID, wtID := range a.agentWorktree {
		if wtID == worktreeID {
			delete(a.agentWorktree, agentID)
		}
	}
	a.bindingMu.Unlock()
	return a.deleteWorktreeState(worktreeID)
}

// mergeWorktreeIntoBase checks out baseBranch (or the repo default) on the main
// repo and merges the worktree's branch with --no-ff. On conflict it aborts the
// merge and returns an error; the worktree is left intact so the caller can
// resolve conflicts in place and retry. The worktree must be clean (its work
// committed) before merging.
//
// Returns residue: when the merge itself landed but the worktree directory
// could not be removed (a process still holds a handle inside it — the Windows
// sharing violation), the entry is marked "residual" for the periodic sweep,
// residue carries the leftover path, and the error is nil: the merge the
// caller asked for succeeded, and failing here used to wedge workflow_stop
// retrying a merge that already happened.
func (a *Actor) mergeWorktreeIntoBase(ctx actor.PureContext, worktreeID, baseBranch string) (residue string, err error) {
	a.worktreeParentMu.RLock()
	wt, ok := a.worktrees[worktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return "", fmt.Errorf("worktree %q not found", worktreeID)
	}
	// Refuse to merge a dirty worktree: uncommitted work would be lost when the
	// worktree is deleted after a successful merge.
	if dirty, derr := gitWorktreeHasUncommitted(wt.Path); derr != nil {
		return "", fmt.Errorf("check worktree clean: %w", derr)
	} else if dirty {
		return "", fmt.Errorf("worktree %q has uncommitted changes; commit them before merge", wt.Name)
	}
	root, err := a.rootPath()
	if err != nil {
		return "", err
	}
	base := strings.TrimSpace(baseBranch)
	if base == "" {
		base, err = defaultBranch(root)
		if err != nil {
			return "", err
		}
	}
	// Serialize owner→main merges: the main repo's working tree, stash stack
	// and HEAD are shared state. Concurrent mergeWorktreeIntoBase calls
	// (two workflow owners stopping at once) would race on checkout/stash
	// and corrupt each other's state.
	a.worktreeMergeMu.Lock()
	defer a.worktreeMergeMu.Unlock()
	// Any snapshot refs left by a crashed merge are orphans while the merge
	// lock is held. Restore them (with conflict guard) and delete their refs
	// before we start the current merge so a stale snapshot does not corrupt
	// the main repo state.
	a.preflightOrphanWorkflowSnapshots(root)
	// The main repo must be clean enough to check out the base branch and
	// merge. Runtime-state writes (wiki cards, graph snapshots) land directly
	// in the main repo by design; reconcile them with a scoped commit. Any
	// other tracked uncommitted change is snapshotted into a named ref and
	// restored after the merge so the workflow owner is not blocked by
	// unrelated dirty files.
	snapRef, origBranch, err := a.ensureMainRepoMergeable(root, worktreeID)
	if err != nil {
		a.restoreWorkflowSnapshot(root, snapRef)
		return "", err
	}
	// Move the main repo onto the base branch, then merge the worktree branch.
	if _, err := gitRun(nil, root, "checkout", base); err != nil {
		a.restoreAndPop(root, origBranch, snapRef)
		return "", fmt.Errorf("checkout base %q: %w", base, err)
	}
	if _, err := gitRun(nil, root, "merge", "--no-ff", "-m", "merge worktree "+wt.Name, wt.Branch); err != nil {
		// Conflict or failure: abort so the main repo is not left mid-merge,
		// then restore the original branch and snapshotted user changes.
		_, _ = gitRun(nil, root, "merge", "--abort")
		a.restoreAndPop(root, origBranch, snapRef)
		return "", fmt.Errorf("merge branch %q into %q: %w", wt.Branch, base, err)
	}
	// The merge commit is on base now. Release processes living inside the
	// worktree (shell sessions with cwd there, LSP engines rooted there) so
	// the directory delete does not hit a Windows sharing violation.
	a.notifyWorktreeHolders(ctx, wt.Path)
	// Success: remove the worktree and its metadata.
	if err := gitWorktreeRemove(root, wt.Path); err != nil {
		// The merge landed but the directory is locked (a process still holds
		// a handle inside it). Degrade instead of failing: the caller's merge
		// succeeded, and failing here wedged workflow_stop into retrying a
		// merge that already happened. Keep the entry as residual; the
		// periodic sweep retries the removal.
		if a.logger != nil {
			a.logger.Warn("project: remove merged worktree failed; left for background sweep",
				"worktree", wt.Name, "path", wt.Path, "error", err)
		}
		a.markWorktreeResidual(worktreeID)
		a.restoreAndPop(root, origBranch, snapRef)
		return wt.Path, nil
	}
	// Branch content is now merged into base; drop the residue ref.
	deleteWorktreeBranchRef(root, wt.Branch)
	// Clear the card-anchored stamp so the next workflow_start creates a
	// fresh worktree instead of treating the merged id as dangling metadata.
	if wt.WorkflowMapID != "" {
		a.clearMapOwnerWorktree(wt.WorkflowMapID)
	}
	a.worktreeParentMu.Lock()
	delete(a.worktrees, worktreeID)
	a.worktreeParentMu.Unlock()
	if err := a.deleteWorktreeState(worktreeID); err != nil {
		a.restoreAndPop(root, origBranch, snapRef)
		return "", err
	}
	// Merge succeeded and metadata is saved: restore the original branch
	// and the snapshotted user changes. A restore conflict is logged but
	// does not fail the merge — the snapshot ref remains for manual recovery.
	a.restoreAndPop(root, origBranch, snapRef)
	return "", nil
}

// restoreAndPop restores the main repo to its original branch and restores
// the snapshotted user changes. If origBranch differs from the current branch
// (e.g. merge moved HEAD onto base), checkout origBranch first so the
// restored changes land on the branch the user was on before workflow_stop. A
// checkout failure or restore conflict is logged but does not return an error
// — the snapshot ref is retained for manual recovery so the merge result is
// not rolled back.
func (a *Actor) restoreAndPop(root, origBranch, snapRef string) {
	if origBranch != "" && origBranch != "HEAD" {
		if cur, _ := gitRun(nil, root, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(cur) != origBranch {
			if _, cerr := gitRun(nil, root, "checkout", origBranch); cerr != nil && a.logger != nil {
				a.logger.Error("project: restore original branch failed after merge; staying on base branch",
					"origBranch", origBranch, "error", cerr)
			}
		}
	}
	a.restoreWorkflowSnapshot(root, snapRef)
}

// restoreWorkflowSnapshot restores changes snapshotted by ensureMainRepoMergeable.
// snapRef is the named ref (refs/sporemind/snapshot/<worktreeID>). It is
// resolved to a commit SHA; the paths captured by the snapshot are listed by
// diffing the commit against its parent, then each path is checked for conflicts
// with the current HEAD before being checked out from the snapshot and unstaged.
// Paths that the merge modified are skipped and the ref is retained for manual
// recovery. A restore error is logged but does not return an error so the merge
// result is not rolled back.
// It reports whether the snapshot was fully restored and its ref deleted (or
// there was nothing to restore). Callers that need to drop the ref regardless
// can use the return value to avoid a redundant delete.
func (a *Actor) restoreWorkflowSnapshot(root, snapRef string) bool {
	if snapRef == "" {
		return true
	}
	sha, err := gitRun(nil, root, "rev-parse", snapRef)
	if err != nil {
		if a.logger != nil {
			a.logger.Error("project: resolve snapshot ref failed; ref retained for manual recovery",
				"error", err, "snapRef", snapRef)
		}
		return false
	}
	sha = strings.TrimSpace(sha)

	pathsOut, err := gitRun(nil, root, "diff", "-z", "--name-only", sha+"^", sha)
	if err != nil {
		if a.logger != nil {
			a.logger.Error("project: list snapshot paths failed; ref retained for manual recovery",
				"error", err, "snapRef", snapRef, "sha", sha)
		}
		return false
	}
	var paths []string
	for _, p := range strings.Split(strings.TrimRight(pathsOut, "\x00"), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		// Nothing to restore; delete the ref.
		if _, err := gitRun(nil, root, "update-ref", "-d", snapRef); err != nil {
			if a.logger != nil {
				a.logger.Error("project: delete empty snapshot ref failed",
					"error", err, "snapRef", snapRef)
			}
			return false
		}
		return true
	}

	currentHead, err := gitRun(nil, root, "rev-parse", "HEAD")
	if err != nil {
		if a.logger != nil {
			a.logger.Error("project: resolve current HEAD for snapshot restore; ref retained",
				"error", err, "snapRef", snapRef)
		}
		return false
	}
	currentHead = strings.TrimSpace(currentHead)

	// Conflict guard: skip paths that the merge modified between the snapshot
	// parent (original HEAD before the merge) and the current HEAD.
	conflictArgs := []string{"diff", "-z", "--name-only", sha + "^", currentHead, "--"}
	conflictArgs = append(conflictArgs, paths...)
	conflictOut, err := gitRun(nil, root, conflictArgs...)
	if err != nil {
		if a.logger != nil {
			a.logger.Error("project: snapshot conflict guard failed; ref retained for manual recovery",
				"error", err, "snapRef", snapRef, "sha", sha)
		}
		return false
	}
	conflictSet := make(map[string]bool)
	for _, p := range strings.Split(strings.TrimRight(conflictOut, "\x00"), "\x00") {
		if p != "" {
			conflictSet[p] = true
		}
	}

	var safePaths []string
	for _, p := range paths {
		if conflictSet[p] {
			if a.logger != nil {
				a.logger.Warn("project: snapshot path conflicts with merge; skipping and retaining ref",
					"path", p, "snapRef", snapRef)
			}
			continue
		}
		safePaths = append(safePaths, p)
	}

	if len(safePaths) > 0 {
		// A snapshot can capture either modifications or deletions. Paths that
		// exist in the snapshot tree are checked out from the snapshot; paths
		// that are absent from the snapshot tree represent deletions and are
		// restored with git rm -f.
		existingOut, err := gitRun(nil, root, append([]string{"ls-tree", "-r", "-z", "--name-only", sha, "--"}, safePaths...)...)
		if err != nil {
			if a.logger != nil {
				a.logger.Error("project: list snapshot tree paths failed; ref retained for manual recovery",
					"error", err, "snapRef", snapRef, "sha", sha)
			}
			return false
		}
		existingSet := make(map[string]bool)
		for _, p := range strings.Split(strings.TrimRight(existingOut, "\x00"), "\x00") {
			if p != "" {
				existingSet[p] = true
			}
		}
		var existingPaths, deletedPaths []string
		for _, p := range safePaths {
			if existingSet[p] {
				existingPaths = append(existingPaths, p)
			} else {
				deletedPaths = append(deletedPaths, p)
			}
		}

		if len(existingPaths) > 0 {
			checkoutArgs := []string{"checkout", sha, "--"}
			checkoutArgs = append(checkoutArgs, existingPaths...)
			if _, err := gitRun(nil, root, checkoutArgs...); err != nil {
				if a.logger != nil {
					a.logger.Error("project: restore snapshot paths failed; ref retained for manual recovery",
						"error", err, "snapRef", snapRef, "sha", sha)
				}
				return false
			}
		}
		if len(deletedPaths) > 0 {
			rmArgs := []string{"rm", "-f", "--"}
			rmArgs = append(rmArgs, deletedPaths...)
			if _, err := gitRun(nil, root, rmArgs...); err != nil {
				if a.logger != nil {
					a.logger.Error("project: restore snapshot deletions failed; ref retained for manual recovery",
						"error", err, "snapRef", snapRef, "sha", sha)
				}
				return false
			}
		}
		resetArgs := []string{"reset", "HEAD", "--"}
		resetArgs = append(resetArgs, safePaths...)
		if _, err := gitRun(nil, root, resetArgs...); err != nil {
			if a.logger != nil {
				a.logger.Error("project: unstage restored snapshot paths failed",
					"error", err, "snapRef", snapRef, "sha", sha)
			}
			return false
		}
	}

	// Only delete the ref when every captured path was restored safely.
	if len(safePaths) == len(paths) {
		if _, err := gitRun(nil, root, "update-ref", "-d", snapRef); err != nil {
			if a.logger != nil {
				a.logger.Error("project: delete snapshot ref after restore failed",
					"error", err, "snapRef", snapRef)
			}
			return false
		}
		return true
	}
	return false
}

// preflightOrphanWorkflowSnapshots cleans up snapshot refs left behind by a
// crashed merge. Because worktreeMergeMu serializes merges, any
// refs/sporemind/snapshot/* ref present at the start of a merge is an orphan.
// Each orphan is restored with the same conflict guard used by
// restoreWorkflowSnapshot and then deleted; failures are logged and do not
// block the current merge.
func (a *Actor) preflightOrphanWorkflowSnapshots(root string) {
	refsOut, err := gitRun(nil, root, "for-each-ref", "--format=%(refname)", "refs/sporemind/snapshot/*")
	if err != nil {
		if a.logger != nil {
			a.logger.Error("project: list orphan workflow snapshot refs failed",
				"error", err)
		}
		return
	}
	for _, ref := range strings.Split(strings.TrimSpace(refsOut), "\n") {
		if ref == "" {
			continue
		}
		if a.logger != nil {
			a.logger.Info("project: restoring orphaned workflow snapshot",
				"snapRef", ref)
		}
		a.restoreWorkflowSnapshot(root, ref)
		if _, err := gitRun(nil, root, "update-ref", "-d", ref); err != nil {
			if a.logger != nil {
				a.logger.Error("project: delete orphan snapshot ref failed",
					"error", err, "snapRef", ref)
			}
		}
	}
}

// defaultBranch resolves the repo's default branch, preferring "main" then
// "master".
func defaultBranch(root string) (string, error) {
	for _, name := range []string{"main", "master"} {
		if _, err := gitRun(nil, root, "show-ref", "--verify", "--quiet", "refs/heads/"+name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("no default branch found (tried main, master); specify BaseBranch")
}

// shortID returns an 8-char hex suffix for generated worktree names.
func shortID() string {
	return uuid.New().String()[:8]
}

// ── Child→parent merge/rebase (Phase 2: workflow-worktree integration) ──

// worktreePathsToRestore is the list of runtime-state paths that must be
// restored from the parent's version after a child→parent merge, because
// these files track the parent's runtime state (wiki cards, graphs, open
// cards) and must not be overwritten by the child's version.
var worktreePathsToRestore = []string{
	".sporecode/wiki/",
	".sporecode/graphs.json",
	".sporecode/wiki-state.json",
}

// mergeChildIntoParent merges the child worktree's branch into the parent
// worktree's branch. The child must be clean (all changes committed). On
// success the child worktree is removed. On conflict the merge is aborted
// and the parent is left intact.
//
// The merge is serialized by worktreeMergeMu so that concurrent
// mergeChildIntoParent calls from different review-approve paths do not
// compete for the same parent worktree's git lock.
// errWorktreeGone signals that a child worktree was already handled by an
// earlier completion (its registry entry deleted by the merge/discard that
// carried its content, or its checkout removed out-of-band). Callers treat it
// as success-with-cleanup: re-merging is a no-op and must not be reported as
// a conflict.
var errWorktreeGone = errors.New("worktree already removed")

// mergeChildIntoParent merges a child worktree's branch into the parent
// worktree's branch. On success the child worktree is removed and its
// registry entry deleted. When the child is already gone (registry entry
// missing or checkout dead), the registry is reconciled and errWorktreeGone
// is returned for callers to treat as completed.
// cleanupMergedChild removes a child worktree whose branch is already merged
// into the parent (same HEAD or child is ancestor of parent). It removes the
// worktree, drops the branch ref, and updates the registry — the same cleanup
// as a successful merge, but without running `git merge`.
func (a *Actor) cleanupMergedChild(childWorktreeID, parentWorktreeID string, child, parent gen.ProjectWorktree) (bool, []string, error) {
	root, err := a.rootPath()
	if err != nil {
		return false, nil, fmt.Errorf("mergeChildIntoParent: root path: %w", err)
	}
	if err := gitWorktreeRemove(root, child.Path); err != nil {
		return false, nil, fmt.Errorf("mergeChildIntoParent: remove child worktree: %w", err)
	}
	deleteWorktreeBranchRef(root, child.Branch)
	parent.LastUsedAt = time.Now().UTC().Format(time.RFC3339Nano)
	a.worktreeParentMu.Lock()
	delete(a.worktrees, childWorktreeID)
	a.worktrees[parentWorktreeID] = parent
	a.worktreeParentMu.Unlock()
	if err := a.deleteWorktreeState(childWorktreeID); err != nil {
		return false, nil, fmt.Errorf("mergeChildIntoParent: delete child manifest: %w", err)
	}
	if err := a.persistWorktreeManifest(parentWorktreeID); err != nil {
		return false, nil, fmt.Errorf("mergeChildIntoParent: persist parent manifest: %w", err)
	}
	return true, nil, nil
}

func (a *Actor) mergeChildIntoParent(childWorktreeID, parentWorktreeID string) (merged bool, conflictFiles []string, err error) {
	a.worktreeMergeMu.Lock()
	defer a.worktreeMergeMu.Unlock()

	a.worktreeParentMu.RLock()
	child, ok := a.worktrees[childWorktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return false, nil, fmt.Errorf("child worktree %q not found: %w", childWorktreeID, errWorktreeGone)
	}
	a.worktreeParentMu.RLock()
	parent, ok := a.worktrees[parentWorktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return false, nil, fmt.Errorf("parent worktree %q not found", parentWorktreeID)
	}

	// Dead checkout: git no longer recognizes the path (pruned or removed
	// out-of-band). The content was already carried by the earlier
	// merge/discard that removed it — reconcile the registry and report gone
	// instead of failing "must be run in a work tree" forever.
	if !isGitWorkingTree(child.Path) {
		a.purgeWorktreeEntry(childWorktreeID)
		return false, nil, fmt.Errorf("child worktree %q checkout is gone: %w", childWorktreeID, errWorktreeGone)
	}

	// Pre-check: child worktree must be clean.
	if dirty, err := gitWorktreeHasUncommitted(child.Path); err != nil {
		return false, nil, fmt.Errorf("check child worktree clean: %w", err)
	} else if dirty {
		return false, nil, fmt.Errorf("child worktree %q has uncommitted changes; commit them before merge", child.Name)
	}

	// Pre-check: parent must not be dirty.
	if dirty, err := gitWorktreeHasUncommitted(parent.Path); err != nil {
		return false, nil, fmt.Errorf("check parent worktree clean: %w", err)
	} else if dirty {
		return false, nil, fmt.Errorf("parent worktree %q has uncommitted changes; resolve before merge", parent.Name)
	}

	// Already-merged check: if the child branch is already an ancestor of
	// the parent HEAD (e.g. a previous partial merge or out-of-band merge
	// carried the commits), the child worktree is stale — skip the merge and
	// treat it as already merged. Without this, `git merge --no-ff` would
	// produce an empty merge or conflict, causing a false-positive
	// auto-reject on review approve.
	childHead, err := gitRevParseHead(child.Path)
	if err != nil {
		return false, nil, fmt.Errorf("mergeChildIntoParent: resolve child HEAD: %w", err)
	}
	parentHead, err := gitRevParseHead(parent.Path)
	if err != nil {
		return false, nil, fmt.Errorf("mergeChildIntoParent: resolve parent HEAD: %w", err)
	}
	if childHead == parentHead {
		return a.cleanupMergedChild(childWorktreeID, parentWorktreeID, child, parent)
	}
	if isAncestor, err := gitIsAncestor(parent.Path, childHead, parentHead); err == nil && isAncestor {
		return a.cleanupMergedChild(childWorktreeID, parentWorktreeID, child, parent)
	}

	// Ensure merge.ours.driver is configured (idempotent). This makes
	// .gitattributes `merge=ours` rules take effect during the merge.
	if root, err := a.rootPath(); err == nil {
		_, _ = gitRun(nil, root, "config", "merge.ours.driver", "true")
	}

	// Merge child branch into parent worktree.
	if _, err := gitRun(nil, parent.Path, "merge", "--no-ff", "-m",
		"merge "+child.Name, child.Branch); err != nil {
		// Conflict: abort the merge so the parent is not left mid-merge.
		// The child worktree is kept intact for the caller to resolve.
		_, _ = gitRun(nil, parent.Path, "merge", "--abort")
		// Try to parse conflict files from stderr.
		conflictFiles = parseConflictFiles(err.Error())
		return false, conflictFiles, fmt.Errorf("merge branch %q into parent %q: %w", child.Branch, parent.Name, err)
	}

	// Restore runtime state: after merge, the parent's wiki/graphs/wiki-state
	// may have been overwritten by the child's version. Restore the parent's
	// pre-merge versions from ORIG_HEAD, then amend the merge commit to
	// include them so the worktree stays clean.
	if err := restoreRuntimeState(parent.Path, "mergeChildIntoParent"); err != nil {
		// Non-fatal for the merge itself, but the parent's runtime state
		// may be stale. Return a descriptive error so the caller can log it.
		return false, nil, fmt.Errorf("mergeChildIntoParent: restore runtime state: %w", err)
	}

	// Success: remove the child worktree and its metadata.
	root, err := a.rootPath()
	if err != nil {
		return false, nil, fmt.Errorf("mergeChildIntoParent: root path: %w", err)
	}
	if err := gitWorktreeRemove(root, child.Path); err != nil {
		return false, nil, fmt.Errorf("mergeChildIntoParent: remove child worktree: %w", err)
	}
	// Child content is merged into the parent branch; drop the residue ref.
	deleteWorktreeBranchRef(root, child.Branch)
	a.worktreeParentMu.Lock()
	delete(a.worktrees, childWorktreeID)
	// Update parent's last used time.
	parent.LastUsedAt = time.Now().UTC().Format(time.RFC3339Nano)
	a.worktrees[parentWorktreeID] = parent
	a.worktreeParentMu.Unlock()
	if err := a.deleteWorktreeState(childWorktreeID); err != nil {
		return false, nil, fmt.Errorf("mergeChildIntoParent: delete child manifest: %w", err)
	}
	if err := a.persistWorktreeManifest(parentWorktreeID); err != nil {
		return false, nil, fmt.Errorf("mergeChildIntoParent: persist parent manifest: %w", err)
	}
	return true, nil, nil
}

// rebaseToParent rebases the child worktree's branch onto the parent
// worktree's branch HEAD. This is used when a review is rejected and the
// child needs to incorporate the parent's latest changes before continuing.
func (a *Actor) rebaseToParent(childWorktreeID, parentWorktreeID string) (conflictFiles []string, err error) {
	a.worktreeParentMu.RLock()
	child, ok := a.worktrees[childWorktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("child worktree %q not found", childWorktreeID)
	}
	a.worktreeParentMu.RLock()
	parent, ok := a.worktrees[parentWorktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("parent worktree %q not found", parentWorktreeID)
	}

	// Pre-check: child worktree must be clean.
	if dirty, err := gitWorktreeHasUncommitted(child.Path); err != nil {
		return nil, fmt.Errorf("check child worktree clean: %w", err)
	} else if dirty {
		return nil, fmt.Errorf("child worktree %q has uncommitted changes; commit them before rebase", child.Name)
	}

	// Rebase child branch onto parent branch.
	if _, err := gitRun(nil, child.Path, "rebase", parent.Branch); err != nil {
		// Conflict: abort the rebase so the child is not left mid-rebase.
		_, _ = gitRun(nil, child.Path, "rebase", "--abort")
		conflictFiles = parseConflictFiles(err.Error())
		return conflictFiles, fmt.Errorf("rebase child %q onto parent %q: %w", child.Branch, parent.Branch, err)
	}

	// Update the child's BaseRef to the new parent HEAD.
	parentHead, err := gitRevParseHead(parent.Path)
	if err == nil {
		child.BaseRef = parentHead
		a.worktreeParentMu.Lock()
		a.worktrees[childWorktreeID] = child
		a.worktreeParentMu.Unlock()
		_ = a.persistWorktreeManifest(childWorktreeID)
	}

	return nil, nil
}

// restoreRuntimeState restores the parent's runtime state files from
// ORIG_HEAD after a merge. ORIG_HEAD points to the parent's pre-merge
// commit, so this recovers the parent's runtime state (wiki cards, graphs,
// open cards) that the child's version would have clobbered.
// After checkout, the restored files are staged and the merge commit is
// amended to include them, keeping the worktree clean.
// If no runtime-state paths exist in ORIG_HEAD, this is a no-op (e.g.
// in a minimal test repo that has no .sporecode state).
func restoreRuntimeState(wtPath, logPrefix string) error {
	// Restore each path individually from ORIG_HEAD. Git aborts the entire
	// checkout if any pathspec doesn't match, so we handle them separately.
	restored := false
	for _, path := range worktreePathsToRestore {
		out, err := gitRun(nil, wtPath, "checkout", "ORIG_HEAD", "--", path)
		if err != nil {
			// If the error is "pathspec did not match", the path doesn't
			// exist in ORIG_HEAD — skip it (non-fatal).
			if strings.Contains(out, "did not match any file") || strings.Contains(err.Error(), "did not match any file") {
				continue
			}
			return fmt.Errorf("%s: checkout ORIG_HEAD for %q: %w", logPrefix, path, err)
		}
		restored = true
	}
	if !restored {
		// No runtime-state paths existed in ORIG_HEAD — nothing to do.
		return nil
	}

	// Stage any restored files so they are part of the merge commit.
	// Handle each path individually since some may not exist in the worktree.
	for _, path := range worktreePathsToRestore {
		// git add will fail if the path doesn't exist; that's fine.
		_, _ = gitRun(nil, wtPath, "add", "--", path)
	}

	// Check if there are staged changes to amend. If nothing was staged,
	// skip the amend to avoid a "no changes" error.
	if _, err := gitRun(nil, wtPath, "diff", "--cached", "--quiet"); err == nil {
		// Exit code 0 = no staged changes, nothing to amend.
		return nil
	}

	// Amend the merge commit to include the runtime state restoration.
	// --no-edit reuses the merge commit message.
	if _, err := gitRun(nil, wtPath, "commit", "--amend", "--no-edit"); err != nil {
		return fmt.Errorf("%s: amend merge commit with runtime state: %w", logPrefix, err)
	}
	return nil
}

// parseConflictFiles attempts to parse a list of conflicting file paths from
// a git merge error message. Returns nil on failure (best-effort).
func parseConflictFiles(errMsg string) []string {
	var files []string
	lines := strings.Split(errMsg, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Git merge conflict messages typically contain "CONFLICT" lines.
		if strings.Contains(line, "CONFLICT") {
			// Extract the file path from lines like:
			// CONFLICT (content): Merge conflict in <file>
			const prefix = "Merge conflict in "
			if idx := strings.Index(line, prefix); idx >= 0 {
				file := strings.TrimSpace(line[idx+len(prefix):])
				if file != "" {
					files = append(files, file)
				}
			}
		}
	}
	if len(files) == 0 {
		return nil
	}
	return files
}
