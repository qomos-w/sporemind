package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

// branchExists reports whether refs/heads/<branch> exists in the repo at dir.
func branchExists(t *testing.T, dir, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "branch", "--list", branch)
	util.HideWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list %s: %v", branch, err)
	}
	return strings.Contains(string(out), branch)
}

// defaultBranchExists confirms the repo default branch (main/master) is still
// present — the guard against deleting the main checkout's branch.
func defaultBranchStillPresent(t *testing.T, dir string) string {
	t.Helper()
	for _, name := range []string{"main", "master"} {
		if branchExists(t, dir, name) {
			return name
		}
	}
	t.Fatal("no default branch found after cleanup")
	return ""
}

// TestWorktreeDiscard_DeletesBranchRef is the regression test for the residue
// report: deleting a worker removes its worktree, but .git/refs/heads kept
// every worker branch forever. discardWorktreeByID must delete the branch.
func TestWorktreeDiscard_DeletesBranchRef(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "residue-worker-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if !branchExists(t, dir, "residue-worker-wt") {
		t.Fatal("precondition: branch must exist after create")
	}

	if err := a.discardWorktreeByID(nil, wt.ID, true); err != nil {
		t.Fatalf("discard: %v", err)
	}

	if branchExists(t, dir, "residue-worker-wt") {
		t.Error("branch ref survived worktree discard; refs/heads residue")
	}
	defaultBranchStillPresent(t, dir)
}

// TestWorktreeReleaseBinding_DeletesBranchRef covers the agent-destroy path
// (workspace.delete_agent → worktree.release_binding): worktree removed, so
// the branch ref must go with it.
func TestWorktreeReleaseBinding_DeletesBranchRef(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "residue-release-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentKey := bindKey(t, "019fa666660000000000000000000006")
	if err := a.bindWorktree(agentKey, wt.ID); err != nil {
		t.Fatalf("bind: %v", err)
	}

	if err := a.releaseWorktreeBinding(ctx, agentKey, false); err != nil {
		t.Fatalf("release binding: %v", err)
	}

	if branchExists(t, dir, "residue-release-wt") {
		t.Error("branch ref survived binding release; refs/heads residue")
	}
	defaultBranchStillPresent(t, dir)
}

// TestMergeChildIntoParent_DeletesChildBranchRef covers the review-approve
// path: after the child worktree is merged into the parent branch and
// removed, the child branch ref must not linger in refs/heads.
func TestMergeChildIntoParent_DeletesChildBranchRef(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "residue-parent-wt"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{
		Name:             "residue-child-wt",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	merged, _, err := a.mergeChildIntoParent(child.ID, parent.ID)
	if err != nil || !merged {
		t.Fatalf("merge child into parent: merged=%v err=%v", merged, err)
	}

	if branchExists(t, dir, "residue-child-wt") {
		t.Error("child branch ref survived merge into parent; refs/heads residue")
	}
	defaultBranchStillPresent(t, dir)
}

// TestDeleteWorktreeBranchRef_NeverDeletesDefaultBranch pins the safety
// guard: even if a worktree somehow ends up on the default branch name, the
// main checkout's branch must survive.
func TestDeleteWorktreeBranchRef_NeverDeletesDefaultBranch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	def := defaultBranchStillPresent(t, dir)
	deleteWorktreeBranchRef(dir, def)
	if !branchExists(t, dir, def) {
		t.Fatalf("default branch %q was deleted; must never happen", def)
	}
}

// TestMergeIntoBase_DeletesBranchRef covers the exit(merge) path
// (mergeWorktreeIntoBase): after the worktree branch is merged into the
// default branch and the worktree removed, the branch ref must not linger.
func TestMergeIntoBase_DeletesBranchRef(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "residue-mergebase-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	// Commit work in the worktree so the merge has content to bring in.
	if err := os.WriteFile(filepath.Join(wt.Path, "child.txt"), []byte("child"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	gitRunMust(t, wt.Path, "add", "-A")
	gitRunMust(t, wt.Path, "commit", "-m", "child work")

	if _, err := a.mergeWorktreeIntoBase(nil, wt.ID, ""); err != nil {
		t.Fatalf("merge into base: %v", err)
	}

	if branchExists(t, dir, "residue-mergebase-wt") {
		t.Error("branch ref survived merge into base; refs/heads residue")
	}
	if _, err := os.Stat(filepath.Join(dir, "child.txt")); err != nil {
		t.Errorf("merged content missing on default branch: %v", err)
	}
	defaultBranchStillPresent(t, dir)
}

// TestDiscard_BranchRefDeleteFailure_StillSucceeds pins the best-effort
// contract of deleteWorktreeBranchRef: the delete step is a void helper that
// swallows git errors, so a failure to delete the branch ref must never abort
// a completed discard.
func TestDiscard_BranchRefDeleteFailure_StillSucceeds(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "residue-fail-discard-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	// Simulate the branch ref already being gone (e.g. an earlier partial
	// cleanup), so the delete step inside discard is the sole failure.
	cmd := exec.Command("git", "update-ref", "-d", "refs/heads/residue-fail-discard-wt")
	util.HideWindow(cmd)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("remove branch ref: %v\n%s", err, out)
	}

	if err := a.discardWorktreeByID(nil, wt.ID, true); err != nil {
		t.Fatalf("discard aborted by branch-ref delete failure: %v", err)
	}

	a.worktreeParentMu.RLock()
	_, kept := a.worktrees[wt.ID]
	a.worktreeParentMu.RUnlock()
	if kept {
		t.Error("worktree metadata survived discard despite ref-delete failure")
	}
	defaultBranchStillPresent(t, dir)
}

// TestMergeIntoBase_BranchRefDeleteFailure_StillSucceeds pins the same
// best-effort contract on the exit(merge) path: a stale ref lock makes
// `git branch -D` fail after the merge and worktree removal, and the merge
// must still complete — a leftover ref is cosmetic compared to aborting a
// finished merge.
func TestMergeIntoBase_BranchRefDeleteFailure_StillSucceeds(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "residue-fail-merge-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "child.txt"), []byte("child"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	gitRunMust(t, wt.Path, "add", "-A")
	gitRunMust(t, wt.Path, "commit", "-m", "child work")

	// A stale ref lock blocks only `git branch -D`; the merge and the
	// worktree removal proceed normally, so the branch-ref delete step is
	// the sole failure.
	lock := filepath.Join(dir, ".git", "refs", "heads", "residue-fail-merge-wt.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatalf("create ref lock: %v", err)
	}

	if _, err := a.mergeWorktreeIntoBase(nil, wt.ID, ""); err != nil {
		t.Fatalf("merge aborted by branch-ref delete failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "child.txt")); err != nil {
		t.Errorf("merged content missing on default branch: %v", err)
	}
	a.worktreeParentMu.RLock()
	_, kept := a.worktrees[wt.ID]
	a.worktreeParentMu.RUnlock()
	if kept {
		t.Error("worktree metadata survived merge despite ref-delete failure")
	}
	if !branchExists(t, dir, "residue-fail-merge-wt") {
		t.Error("precondition broken: ref delete unexpectedly succeeded despite the lock; failure injection did not run")
	}
	defaultBranchStillPresent(t, dir)
}
