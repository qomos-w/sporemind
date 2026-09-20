package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// guardTestActor builds a project actor over a fresh git repo and registers a
// spawn worker (agentParent recorded) with the given worktree binding state.
func guardTestActor(t *testing.T, bindWorktree bool) (*Actor, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	workerHex := "019fa333330000000000000000000003"
	workerKey := bindKey(t, workerHex)
	parentKey := "019fa444440000000000000000000004"
	a.worktreeParentMu.Lock()
	if a.agentParent == nil {
		a.agentParent = make(map[string]string)
	}
	a.agentParent[workerKey] = parentKey
	a.worktreeParentMu.Unlock()

	wtPath := ""
	if bindWorktree {
		wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "guard-worker-wt", BaseRef: "HEAD"})
		if err != nil {
			t.Fatalf("create worktree: %v", err)
		}
		if err := a.bindWorktree(workerKey, wt.ID); err != nil {
			t.Fatalf("bind worktree: %v", err)
		}
		wtPath = wt.Path
	}
	return a, workerHex, wtPath, dir
}

// TestGitWriteGuard_UnboundSpawnChild_FailClosed reproduces the incident
// shape: a spawn worker whose worktree binding entry has vanished (worktree
// metadata cleanup) must NOT silently fall through to the main repo root —
// git writes are rejected instead of committing to master.
func TestGitWriteGuard_UnboundSpawnChild_FailClosed(t *testing.T) {
	a, workerHex, _, dir := guardTestActor(t, false)
	ctx := callerCtx(t, workerHex)

	if err := os.WriteFile(filepath.Join(dir, "leak.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.handleGitAdd(ctx, gen.ProjectGitAddReq{Paths: []string{"leak.txt"}}); err == nil {
		t.Error("git_add: expected rejection for unbound spawn worker, got nil")
	} else if !strings.Contains(err.Error(), "lost its worktree binding") {
		t.Errorf("git_add: unexpected error: %v", err)
	}
	if _, err := a.handleGitCommit(ctx, gen.ProjectGitCommitReq{Message: "leak"}); err == nil {
		t.Error("git_commit: expected rejection for unbound spawn worker, got nil")
	} else if !strings.Contains(err.Error(), "lost his worktree binding") && !strings.Contains(err.Error(), "lost its worktree binding") {
		t.Errorf("git_commit: unexpected error: %v", err)
	}
	if err := a.handleGitCheckout(ctx, gen.ProjectGitCheckoutReq{Branch: "main", Create: true}); err == nil {
		t.Error("git_checkout: expected rejection for unbound spawn worker, got nil")
	}
	if err := a.handleGitStashSave(ctx, gen.ProjectGitStashSaveReq{}); err == nil {
		t.Error("git_stash_save: expected rejection for unbound spawn worker, got nil")
	}

	// Main root must be untouched: no staged file, no extra commit.
	if log := gitLogSubjects(t, dir); len(log) != 1 {
		t.Errorf("main root log changed: %v", log)
	}
}

// TestGitWriteGuard_ReadsAllowedForUnboundSpawnChild pins that read-only git
// callables keep working for an unbound spawn worker (e.g. fork_explore
// children inspecting the main root); only writes are fail-closed.
func TestGitWriteGuard_ReadsAllowedForUnboundSpawnChild(t *testing.T) {
	a, workerHex, _, _ := guardTestActor(t, false)
	ctx := callerCtx(t, workerHex)

	if _, err := a.handleGitStatus(ctx, domain.ProjectGitStatusReq{}); err != nil {
		t.Errorf("git_status should stay allowed for unbound spawn worker: %v", err)
	}
	if _, err := a.handleGitLog(ctx, gen.ProjectGitLogReq{}); err != nil {
		t.Errorf("git_log should stay allowed for unbound spawn worker: %v", err)
	}
}

// TestGitWriteGuard_BoundSpawnChild_CommitsInWorktree verifies the guard is
// silent when the binding is active: the worker commits inside its worktree
// and the main root stays clean.
func TestGitWriteGuard_BoundSpawnChild_CommitsInWorktree(t *testing.T) {
	a, workerHex, wtPath, dir := guardTestActor(t, true)
	ctx := callerCtx(t, workerHex)

	if err := os.WriteFile(filepath.Join(wtPath, "worker-file.txt"), []byte("w"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.handleGitAdd(ctx, gen.ProjectGitAddReq{Paths: []string{"worker-file.txt"}}); err != nil {
		t.Fatalf("git_add in worktree: %v", err)
	}
	if _, err := a.handleGitCommit(ctx, gen.ProjectGitCommitReq{Message: "worker commit"}); err != nil {
		t.Fatalf("git_commit in worktree: %v", err)
	}
	if log := gitLogSubjects(t, dir); contains(log, "worker commit") {
		t.Errorf("main root leaked worker commit: %v", log)
	}
}

// TestGitWriteGuard_RootAgent_MainRootAllowed verifies root agents (no
// agentParent record) keep unrestricted main-root git writes.
func TestGitWriteGuard_RootAgent_MainRootAllowed(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	rootHex := "019fa555550000000000000000000005"
	ctx := callerCtx(t, rootHex)

	if err := os.WriteFile(filepath.Join(dir, "root-file.txt"), []byte("r"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.handleGitAdd(ctx, gen.ProjectGitAddReq{Paths: []string{"root-file.txt"}}); err != nil {
		t.Fatalf("git_add on main root: %v", err)
	}
	if _, err := a.handleGitCommit(ctx, gen.ProjectGitCommitReq{Message: "root commit"}); err != nil {
		t.Fatalf("git_commit on main root: %v", err)
	}
	if log := gitLogSubjects(t, dir); !contains(log, "root commit") {
		t.Errorf("root agent commit missing from main root: %v", log)
	}
}
