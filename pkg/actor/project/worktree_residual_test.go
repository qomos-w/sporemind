package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestMergeIntoBase_LockedDirDegradesToResidual pins the workflow_stop
// degrade semantics for a locked worktree directory (a process still holds a
// handle inside it — the Windows sharing violation): the merge call succeeds
// and reports the residue path, the registry keeps a "residual" entry instead
// of wedging the workflow, and the periodic sweep retries until the handle
// releases and then completes the teardown (drop branch ref, purge entry).
func TestMergeIntoBase_LockedDirDegradesToResidual(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "locked-merge-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "child.txt"), []byte("child"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	gitRunMust(t, wt.Path, "add", "-A")
	gitRunMust(t, wt.Path, "commit", "-m", "child work")

	// Simulate a holder keeping the directory locked for the merge removal
	// and the first sweep, then releasing: removal fails twice with a
	// sharing violation, the third attempt (and the real delete) succeed.
	failures := 2
	prev := gitWorktreeRemoveHook
	gitWorktreeRemoveHook = func(root, path string) error {
		if failures > 0 {
			failures--
			return fmt.Errorf("remove %s: The process cannot access the file because it is being used by another process", path)
		}
		return safeRemoveWorktreeDir(root, path)
	}
	defer func() { gitWorktreeRemoveHook = prev }()

	residue, err := a.mergeWorktreeIntoBase(nil, wt.ID, "")
	if err != nil {
		t.Fatalf("merge must succeed despite the locked dir: %v", err)
	}
	if residue != wt.Path {
		t.Fatalf("residue = %q, want the worktree path %q", residue, wt.Path)
	}
	// The merge itself landed on the default branch.
	if _, err := os.Stat(filepath.Join(dir, "child.txt")); err != nil {
		t.Fatalf("merged content missing on default branch: %v", err)
	}
	// The entry degrades to residual; the directory and branch ref stay.
	a.worktreeParentMu.RLock()
	entry, kept := a.worktrees[wt.ID]
	a.worktreeParentMu.RUnlock()
	if !kept || entry.Status != "residual" {
		t.Fatalf("entry = %+v (kept=%v), want status residual", entry, kept)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("locked dir must survive the failed removal: %v", err)
	}
	if !branchExists(t, dir, "locked-merge-wt") {
		t.Error("branch ref deleted while the worktree checkout still holds it")
	}

	// Sweep 1: still locked → entry stays residual for the next tick.
	a.sweepResidualWorktrees()
	a.worktreeParentMu.RLock()
	_, kept = a.worktrees[wt.ID]
	a.worktreeParentMu.RUnlock()
	if !kept {
		t.Fatal("residual entry purged while the dir is still locked")
	}

	// Sweep 2: handle released → teardown completes.
	a.sweepResidualWorktrees()
	a.worktreeParentMu.RLock()
	_, kept = a.worktrees[wt.ID]
	a.worktreeParentMu.RUnlock()
	if kept {
		t.Error("residual entry survived the successful sweep")
	}
	if _, err := os.Stat(wt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree dir survived sweep: %v", err)
	}
	if branchExists(t, dir, "locked-merge-wt") {
		t.Error("branch ref survived sweep")
	}
	defaultBranchStillPresent(t, dir)
}

// TestWorkflowStopMergeWorktree_LockedDirReportsResidue covers the callable
// surface: a locked directory yields status "merged" with ResiduePath set,
// not "conflict" — the workflow can complete while the sweep converges.
func TestWorkflowStopMergeWorktree_LockedDirReportsResidue(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "locked-stop-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	gitRunMust(t, wt.Path, "commit", "--allow-empty", "-m", "owner work")

	prev := gitWorktreeRemoveHook
	gitWorktreeRemoveHook = func(root, path string) error {
		return fmt.Errorf("remove %s: sharing violation", path)
	}
	defer func() { gitWorktreeRemoveHook = prev }()

	resp, err := a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{WorktreeID: wt.ID})
	if err != nil {
		t.Fatalf("stop-merge must not fail on a locked dir: %v", err)
	}
	if resp.Status != "merged" {
		t.Fatalf("status = %q, want merged", resp.Status)
	}
	if resp.ResiduePath != wt.Path {
		t.Fatalf("ResiduePath = %q, want %q", resp.ResiduePath, wt.Path)
	}
}
