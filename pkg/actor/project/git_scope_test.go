package project

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// boundRootTestActor builds a project actor plus a ROOT agent (no agentParent
// record) bound to a live worktree — the workflow-owner shape. Repo:"" routes
// such a caller to its worktree; Repo:"main" is the owner's recovery channel
// into the main repo.
func boundRootTestActor(t *testing.T) (*Actor, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	rootHex := "019fa666660000000000000000000006"
	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "owner-wf-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := a.bindWorktree(bindKey(t, rootHex), wt.ID); err != nil {
		t.Fatalf("bind worktree: %v", err)
	}
	// go-git v5's dotGitCommonDirectory leaks the commondir file handle on
	// Windows (no defer close — fixed in v6). The leaked os.File handle
	// blocks t.TempDir cleanup from deleting the worktree's commondir file.
	// Force GC so the finalizer closes the handle before TempDir cleanup.
	t.Cleanup(func() {
		runtime.GC()
	})
	return a, rootHex, wt.Path, dir
}

// TestGitScope_MainDeniedToSpawnedChild pins the sandbox rule: Repo:"main"
// must never let a spawned worker (agentParent recorded) escape its worktree,
// across all six Repo-aware callables.
func TestGitScope_MainDeniedToSpawnedChild(t *testing.T) {
	a, workerHex, _, _ := guardTestActor(t, true)
	ctx := callerCtx(t, workerHex)

	if _, err := a.handleGitStatus(ctx, domain.ProjectGitStatusReq{Repo: "main"}); err == nil {
		t.Error("git_status: expected rejection, got nil")
	} else if !strings.Contains(err.Error(), "restricted to root agents") {
		t.Errorf("git_status: unexpected error: %v", err)
	}
	if _, err := a.handleGitLog(ctx, gen.ProjectGitLogReq{Repo: "main"}); err == nil {
		t.Error("git_log: expected rejection, got nil")
	}
	if _, err := a.handleGitDiff(ctx, gen.ProjectGitDiffReq{Repo: "main"}); err == nil {
		t.Error("git_diff: expected rejection, got nil")
	}
	if err := a.handleGitStashSave(ctx, gen.ProjectGitStashSaveReq{Repo: "main"}); err == nil {
		t.Error("git_stash_save: expected rejection, got nil")
	}
	if err := a.handleGitStashPop(ctx, gen.ProjectGitStashPopReq{Repo: "main"}); err == nil {
		t.Error("git_stash_pop: expected rejection, got nil")
	}
	if _, err := a.handleGitStashList(ctx, gen.ProjectGitStashListReq{Repo: "main"}); err == nil {
		t.Error("git_stash_list: expected rejection, got nil")
	}
}

// TestGitScope_MainAllowsBoundRootAgentToReachMainRepo pins the routing
// contract for the workflow-owner shape: the same bound root agent sees its
// worktree with Repo:"" and the main repo with Repo:"main".
func TestGitScope_MainAllowsBoundRootAgentToReachMainRepo(t *testing.T) {
	a, rootHex, _, dir := boundRootTestActor(t)
	ctx := callerCtx(t, rootHex)

	wtResp, err := a.handleGitStatus(ctx, domain.ProjectGitStatusReq{})
	if err != nil {
		t.Fatalf("git_status (worktree scope): %v", err)
	}
	mainResp, err := a.handleGitStatus(ctx, domain.ProjectGitStatusReq{Repo: "main"})
	if err != nil {
		t.Fatalf("git_status (main scope): %v", err)
	}
	if wtResp.Branch == mainResp.Branch {
		t.Errorf("Repo \"\" and Repo \"main\" must resolve differently for a bound owner: both = %q", wtResp.Branch)
	}
	mainBranch := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if strings.TrimSpace(mainBranch) != mainResp.Branch {
		t.Errorf("main scope branch = %q, want main repo branch %q", mainResp.Branch, strings.TrimSpace(mainBranch))
	}
}

// TestGitScope_StashSavePopViaMain pins the workflow-stop recovery loop the
// Repo field exists for: owner stashes the dirty main repo via Repo:"main",
// the tree becomes clean, stash list reflects the entry, pop restores it.
func TestGitScope_StashSavePopViaMain(t *testing.T) {
	a, rootHex, _, dir := boundRootTestActor(t)
	ctx := callerCtx(t, rootHex)

	blocker := filepath.Join(dir, "user-draft.txt")
	if err := os.WriteFile(blocker, []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.handleGitStashSave(ctx, gen.ProjectGitStashSaveReq{Repo: "main", Message: "workflow blocker"}); err != nil {
		t.Fatalf("stash_save via main: %v", err)
	}
	if status := gitOut(t, dir, "status", "--porcelain"); strings.Contains(status, "user-draft.txt") {
		t.Errorf("stash_save via main left the blocker dirty:\n%s", status)
	}
	listResp, err := a.handleGitStashList(ctx, gen.ProjectGitStashListReq{Repo: "main"})
	if err != nil {
		t.Fatalf("stash_list via main: %v", err)
	}
	if len(listResp.Stashes) != 1 || !strings.Contains(listResp.Stashes[0].Message, "workflow blocker") {
		t.Errorf("stash_list via main = %+v, want one 'workflow blocker' entry", listResp.Stashes)
	}
	if err := a.handleGitStashPop(ctx, gen.ProjectGitStashPopReq{Repo: "main"}); err != nil {
		t.Fatalf("stash_pop via main: %v", err)
	}
	if _, err := os.Stat(blocker); err != nil {
		t.Fatalf("stash_pop via main did not restore the blocker file: %v", err)
	}
}

// TestGitScope_DiffRoutesByRepo pins the diff routing: a bound owner's
// Repo:"" diff shows worktree changes only, Repo:"main" shows main-repo
// changes only.
func TestGitScope_DiffRoutesByRepo(t *testing.T) {
	a, rootHex, wtPath, dir := boundRootTestActor(t)
	ctx := callerCtx(t, rootHex)

	if err := os.WriteFile(filepath.Join(wtPath, "wt-only.txt"), []byte("w"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main-only.txt"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}

	wtDiff, err := a.handleGitDiff(ctx, gen.ProjectGitDiffReq{})
	if err != nil {
		t.Fatalf("git_diff (worktree scope): %v", err)
	}
	wtDiffText := strings.Join(wtDiff.Diff, "\n")
	if !strings.Contains(wtDiffText, "wt-only.txt") || strings.Contains(wtDiffText, "main-only.txt") {
		t.Errorf("worktree-scope diff leaked across repos:\n%s", wtDiffText)
	}
	mainDiff, err := a.handleGitDiff(ctx, gen.ProjectGitDiffReq{Repo: "main"})
	if err != nil {
		t.Fatalf("git_diff (main scope): %v", err)
	}
	mainDiffText := strings.Join(mainDiff.Diff, "\n")
	if !strings.Contains(mainDiffText, "main-only.txt") || strings.Contains(mainDiffText, "wt-only.txt") {
		t.Errorf("main-scope diff leaked across repos:\n%s", mainDiffText)
	}
}

// TestGitScope_MainBypassesStaleBinding pins the recovery property: a root
// agent whose worktree binding has gone stale (worktree removed behind the
// actor's back) can still reach the main repo via Repo:"main", even though
// the default scope fails closed.
func TestGitScope_MainBypassesStaleBinding(t *testing.T) {
	a, rootHex, wtPath, dir := boundRootTestActor(t)
	ctx := callerCtx(t, rootHex)

	gitOut(t, dir, "worktree", "remove", "--force", wtPath)

	if _, err := a.handleGitStatus(ctx, domain.ProjectGitStatusReq{Repo: "main"}); err != nil {
		t.Errorf("git_status via main should survive a stale binding: %v", err)
	}
	if err := a.handleGitStashSave(ctx, gen.ProjectGitStashSaveReq{Repo: "main"}); err != nil {
		t.Errorf("stash_save via main should survive a stale binding: %v", err)
	}
}

// TestGitScope_UnknownRepoValueRejected pins explicit validation: only ""
// and "main" are accepted.
func TestGitScope_UnknownRepoValueRejected(t *testing.T) {
	a, rootHex, _, _ := boundRootTestActor(t)
	ctx := callerCtx(t, rootHex)

	_, err := a.handleGitStatus(ctx, domain.ProjectGitStatusReq{Repo: "bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown Repo") {
		t.Errorf("expected 'unknown Repo' rejection, got: %v", err)
	}
}

// TestGitScope_LogAndBranchOnWorktree pins that git_log and git_branch work on
// a bound worktree. Without EnableDotGitCommonDir, go-git's PlainOpen cannot
// resolve HEAD → refs/heads/<branch> on linked worktrees because the branch
// refs live in the common .git directory, not the per-worktree .git dir.
func TestGitScope_LogAndBranchOnWorktree(t *testing.T) {
	a, rootHex, _, _ := boundRootTestActor(t)
	ctx := callerCtx(t, rootHex)

	logResp, err := a.handleGitLog(ctx, gen.ProjectGitLogReq{Limit: 5})
	if err != nil {
		t.Fatalf("git_log on worktree: %v", err)
	}
	if len(logResp.Commits) == 0 {
		t.Error("git_log on worktree returned no commits; worktree HEAD may be unresolved")
	}

	branchResp, err := a.handleGitBranch(ctx, gen.ProjectGitBranchReq{})
	if err != nil {
		t.Fatalf("git_branch on worktree: %v", err)
	}
	found := false
	for _, b := range branchResp.Branches {
		if b.Current {
			found = true
			if b.Name == "" {
				t.Error("current branch has empty Name")
			}
		}
	}
	if !found {
		t.Errorf("git_branch on worktree found no current branch; branches = %+v", branchResp.Branches)
	}
}
