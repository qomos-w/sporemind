package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/qomos-w/sporemind/pkg/testutil"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestWorktreeVerifyMerged_Merged verifies that after the child branch is
// merged into its parent, the verify callable reports "merged" and runs
// cleanup (worktree removed, branch deleted, registry entry gone).
func TestWorktreeVerifyMerged_Merged(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	owner, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "owner-merged", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "child-merged", ParentWorktreeID: owner.ID, BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Commit a file in the child worktree.
	if err := os.WriteFile(filepath.Join(child.Path, "child.txt"), []byte("child content"), 0644); err != nil {
		t.Fatalf("write child file: %v", err)
	}
	gitCommitInWorktree(t, child.Path, "child commit")

	// Manually merge the child branch into the parent branch (simulates external
	// merge done by the agent owner / human caller).
	cmd := exec.Command("git", "merge", "--no-ff", "-m", "merge child", child.Branch)
	cmd.Dir = owner.Path
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("merge child into parent: %v\n%s", err, out)
	}

	resp, err := a.handleWorktreeVerifyMergedToParent(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeVerifyMergedReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: owner.ID,
	})
	if err != nil {
		t.Fatalf("verify merged: %v", err)
	}
	if resp.Status != "merged" {
		t.Fatalf("expected status merged, got %q", resp.Status)
	}
	if resp.Branch != child.Branch {
		t.Fatalf("expected branch %q, got %q", child.Branch, resp.Branch)
	}
	if resp.ChildHead == "" || resp.ParentHead == "" {
		t.Fatalf("expected non-empty heads, got child=%q parent=%q", resp.ChildHead, resp.ParentHead)
	}

	// Cleanup should have removed the worktree entry and the branch.
	a.worktreeParentMu.RLock()
	_, exists := a.worktrees[child.ID]
	a.worktreeParentMu.RUnlock()
	if exists {
		t.Fatalf("child worktree entry should have been removed from registry")
	}
	if _, err := os.Stat(child.Path); !os.IsNotExist(err) {
		t.Fatalf("child worktree directory should have been removed: %v", err)
	}
}

// TestWorktreeVerifyMerged_NotMerged verifies that an independent child commit
// that has not been merged into the parent reports "not_merged" and leaves the
// child worktree and branch untouched.
func TestWorktreeVerifyMerged_NotMerged(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	owner, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "owner-notmerged", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "child-notmerged", ParentWorktreeID: owner.ID, BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	if err := os.WriteFile(filepath.Join(child.Path, "child-only.txt"), []byte("child only"), 0644); err != nil {
		t.Fatalf("write child file: %v", err)
	}
	gitCommitInWorktree(t, child.Path, "child-only commit")

	resp, err := a.handleWorktreeVerifyMergedToParent(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeVerifyMergedReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: owner.ID,
	})
	if err != nil {
		t.Fatalf("verify merged: %v", err)
	}
	if resp.Status != "not_merged" {
		t.Fatalf("expected status not_merged, got %q", resp.Status)
	}
	if resp.Branch != child.Branch {
		t.Fatalf("expected branch %q, got %q", child.Branch, resp.Branch)
	}
	if resp.ChildHead == "" || resp.ParentHead == "" {
		t.Fatalf("expected non-empty heads, got child=%q parent=%q", resp.ChildHead, resp.ParentHead)
	}

	a.worktreeParentMu.RLock()
	_, exists := a.worktrees[child.ID]
	a.worktreeParentMu.RUnlock()
	if !exists {
		t.Fatalf("child worktree entry should still exist")
	}
	if _, err := os.Stat(child.Path); err != nil {
		t.Fatalf("child worktree directory should still exist: %v", err)
	}
}

// TestWorktreeVerifyMerged_Gone verifies that when the child worktree directory
// has already been deleted, the verify callable purges the stale registry entry
// and reports "gone".
func TestWorktreeVerifyMerged_Gone(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	owner, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "owner-gone", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "child-gone", ParentWorktreeID: owner.ID, BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Delete the child worktree directory out-of-band.
	if err := os.RemoveAll(child.Path); err != nil {
		t.Fatalf("remove child worktree: %v", err)
	}

	resp, err := a.handleWorktreeVerifyMergedToParent(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeVerifyMergedReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: owner.ID,
	})
	if err != nil {
		t.Fatalf("verify merged: %v", err)
	}
	if resp.Status != "gone" {
		t.Fatalf("expected status gone, got %q", resp.Status)
	}

	a.worktreeParentMu.RLock()
	_, exists := a.worktrees[child.ID]
	a.worktreeParentMu.RUnlock()
	if exists {
		t.Fatalf("stale child worktree entry should have been purged")
	}
}

// TestWorkflowWorktreeCleanCheck_NoBinding reports clean when the agent has no
// bound worktree.
func TestWorkflowWorktreeCleanCheck_NoBinding(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	resp, err := a.handleWorkflowWorktreeCleanCheck(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCleanCheckReq{AgentActorID: "no-such-agent"})
	if err != nil {
		t.Fatalf("clean check: %v", err)
	}
	if !resp.Clean {
		t.Fatalf("expected clean=true for unbound agent, got clean=%v", resp.Clean)
	}
	if len(resp.DirtyFiles) != 0 {
		t.Fatalf("expected no dirty files, got %v", resp.DirtyFiles)
	}
}

// TestWorkflowWorktreeCleanCheck_Clean reports clean for a bound worktree with
// no uncommitted changes.
func TestWorkflowWorktreeCleanCheck_Clean(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "wt-clean", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentID := "019fa111110000000000000000000001"
	if err := a.bindWorktree(bindKey(t, agentID), wt.ID); err != nil {
		t.Fatalf("bind agent: %v", err)
	}

	resp, err := a.handleWorkflowWorktreeCleanCheck(callerCtx(t, agentID), gen.ProjectWorktreeCleanCheckReq{AgentActorID: agentID})
	if err != nil {
		t.Fatalf("clean check: %v", err)
	}
	if !resp.Clean {
		t.Fatalf("expected clean=true, got clean=%v dirty=%v", resp.Clean, resp.DirtyFiles)
	}
}

// TestWorkflowWorktreeCleanCheck_Dirty reports dirty with up to 10 file paths
// when the bound worktree has uncommitted changes.
func TestWorkflowWorktreeCleanCheck_Dirty(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "wt-dirty", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentID := "019fa222220000000000000000000002"
	if err := a.bindWorktree(bindKey(t, agentID), wt.ID); err != nil {
		t.Fatalf("bind agent: %v", err)
	}

	// Create an uncommitted file in the bound worktree.
	if err := os.WriteFile(filepath.Join(wt.Path, "dirty.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	resp, err := a.handleWorkflowWorktreeCleanCheck(callerCtx(t, agentID), gen.ProjectWorktreeCleanCheckReq{AgentActorID: agentID})
	if err != nil {
		t.Fatalf("clean check: %v", err)
	}
	if resp.Clean {
		t.Fatalf("expected clean=false")
	}
	if len(resp.DirtyFiles) != 1 || resp.DirtyFiles[0] != "dirty.txt" {
		t.Fatalf("expected [dirty.txt], got %v", resp.DirtyFiles)
	}
}
