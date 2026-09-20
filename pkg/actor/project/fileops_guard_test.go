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

// TestFileWriteGuard_UnboundSpawnChild_FailClosed verifies that a spawned
// worker whose worktree binding is missing cannot write/edit/rm files on the
// main repo root. The git write guard (denyUnboundSpawnChild) already covers
// git operations; this extends the same guard to file operations.
func TestFileWriteGuard_UnboundSpawnChild_FailClosed(t *testing.T) {
	a, workerHex, _, dir := guardTestActor(t, false)
	ctx := callerCtx(t, workerHex)

	// write
	if _, err := a.handleFileWrite(ctx, domain.FileSystemWriteReq{Path: "leak.txt", Content: "x"}); err == nil {
		t.Error("project.write: expected rejection for unbound spawn worker, got nil")
	} else if !strings.Contains(err.Error(), "lost its worktree binding") {
		t.Errorf("project.write: unexpected error: %v", err)
	}

	// edit — needs an existing file to attempt
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleFileEdit(ctx, domain.FileSystemEditReq{Path: "existing.txt", OldString: "old", NewString: "new"}); err == nil {
		t.Error("project.edit: expected rejection for unbound spawn worker, got nil")
	} else if !strings.Contains(err.Error(), "lost its worktree binding") {
		t.Errorf("project.edit: unexpected error: %v", err)
	}

	// rm
	if _, err := a.handleFileRm(ctx, domain.FileSystemRmReq{Path: "existing.txt", Force: true}); err == nil {
		t.Error("project.rm: expected rejection for unbound spawn worker, got nil")
	} else if !strings.Contains(err.Error(), "lost its worktree binding") {
		t.Errorf("project.rm: unexpected error: %v", err)
	}

	// Main root must be untouched: leak.txt must not exist.
	if _, err := os.Stat(filepath.Join(dir, "leak.txt")); !os.IsNotExist(err) {
		t.Errorf("main root leaked: leak.txt was created")
	}
	// existing.txt must still have its original content (edit/rm were blocked).
	data, err := os.ReadFile(filepath.Join(dir, "existing.txt"))
	if err != nil {
		t.Fatalf("existing.txt: %v", err)
	}
	if string(data) != "old" {
		t.Errorf("existing.txt content changed: got %q, want %q", data, "old")
	}
}

// TestFileWriteGuard_BoundSpawnChild_WritesInWorktree verifies the guard is
// silent when the binding is active: the worker writes inside its worktree
// and the main root stays clean.
func TestFileWriteGuard_BoundSpawnChild_WritesInWorktree(t *testing.T) {
	a, workerHex, wtPath, dir := guardTestActor(t, true)
	ctx := callerCtx(t, workerHex)

	if _, err := a.handleFileWrite(ctx, domain.FileSystemWriteReq{Path: "worker-file.txt", Content: "w"}); err != nil {
		t.Fatalf("project.write in worktree: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wtPath, "worker-file.txt"))
	if err != nil {
		t.Fatalf("read worktree file: %v", err)
	}
	if string(data) != "w" {
		t.Errorf("worktree file content: got %q, want %q", data, "w")
	}
	// Main root must not have the file.
	if _, err := os.Stat(filepath.Join(dir, "worker-file.txt")); !os.IsNotExist(err) {
		t.Errorf("main root leaked: worker-file.txt was created in main root")
	}
}

// TestFileWriteGuard_RootAgent_MainRootAllowed verifies root agents (no
// agentParent record) keep unrestricted main-root file writes.
func TestFileWriteGuard_RootAgent_MainRootAllowed(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	rootHex := "019fa555550000000000000000000005"
	ctx := callerCtx(t, rootHex)

	if _, err := a.handleFileWrite(ctx, domain.FileSystemWriteReq{Path: "root-file.txt", Content: "r"}); err != nil {
		t.Fatalf("project.write on main root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "root-file.txt"))
	if err != nil {
		t.Fatalf("read main root file: %v", err)
	}
	if string(data) != "r" {
		t.Errorf("main root file content: got %q, want %q", data, "r")
	}
}

// TestFileReadGuard_UnboundSpawnChild_ReadsAllowed pins that read-only file
// callables keep working for an unbound spawn worker (e.g. fork_explore
// children inspecting the main root); only writes are fail-closed.
func TestFileReadGuard_UnboundSpawnChild_ReadsAllowed(t *testing.T) {
	a, workerHex, _, dir := guardTestActor(t, false)
	ctx := callerCtx(t, workerHex)

	if err := os.WriteFile(filepath.Join(dir, "readable.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	// file_list should work on the main root.
	if _, err := a.handleFileList(ctx, domain.FileSystemListReq{Path: "."}); err != nil {
		t.Errorf("file_list should stay allowed for unbound spawn worker: %v", err)
	}
	// file_read should work on the main root.
	resp, err := a.handleFileRead(ctx, domain.FileSystemReadReq{Path: "readable.txt"})
	if err != nil {
		t.Errorf("file_read should stay allowed for unbound spawn worker: %v", err)
	}
	if strings.TrimSpace(resp.Content) != "data" {
		t.Errorf("file_read content: got %q, want %q", resp.Content, "data")
	}
}

// TestShellGuard_UnboundSpawnChild_FailClosed verifies that a spawned worker
// whose worktree binding is missing cannot run shell commands on the main
// repo root.
func TestShellGuard_UnboundSpawnChild_FailClosed(t *testing.T) {
	a, workerHex, _, _ := guardTestActor(t, false)
	ctx := callerCtx(t, workerHex)

	err := a.handleShellExecPipe(ctx, domain.ShellExecReq{Command: "echo", Args: []string{"leak"}}, nil)
	if err == nil {
		t.Error("shell_exec: expected rejection for unbound spawn worker, got nil")
	} else if !strings.Contains(err.Error(), "lost its worktree binding") {
		t.Errorf("shell_exec: unexpected error: %v", err)
	}
}

// TestShellGuard_RootAgent_MainRootAllowed verifies root agents keep
// unrestricted shell access on the main root.
func TestShellGuard_RootAgent_MainRootAllowed(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	rootHex := "019fa555550000000000000000000005"
	ctx := callerCtx(t, rootHex)
	emit := testutil.NewFakeEmitter()

	if err := a.handleShellExecPipe(ctx, domain.ShellExecReq{Command: "echo", Args: []string{"ok"}}, emit); err != nil {
		t.Errorf("shell_exec on main root: %v", err)
	}
}

// TestWorktreeExit_RejectsSpawnedWorker verifies that a spawned worker cannot
// exit its own worktree via project.worktree_exit. The worktree lifecycle is
// managed by the workflow owner, not the worker.
func TestWorktreeExit_RejectsSpawnedWorker(t *testing.T) {
	a, workerHex, _, _ := guardTestActor(t, true)
	ctx := callerCtx(t, workerHex)

	_, err := a.handleWorktreeExit(ctx, gen.ProjectWorktreeExitReq{Mode: "discard", Force: true})
	if err == nil {
		t.Fatal("worktree_exit: expected rejection for spawned worker, got nil")
	}
	if !strings.Contains(err.Error(), "cannot exit its worktree") {
		t.Errorf("worktree_exit: unexpected error: %v", err)
	}

	// Binding must still be active after the rejected exit.
	workerKey := bindKey(t, workerHex)
	wtID, ok := a.callerBoundWorktree(workerKey)
	if !ok {
		t.Fatal("worktree binding was lost after rejected exit")
	}
	if wtID == "" {
		t.Fatal("worktree binding ID is empty after rejected exit")
	}
}

// TestWorktreeExit_RootAgent_Allowed verifies that root agents (no agentParent)
// can still exit their worktree normally.
func TestWorktreeExit_RootAgent_Allowed(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	rootHex := "019fa555550000000000000000000005"
	ctx := callerCtx(t, rootHex)

	// Root agent enters a worktree first.
	if _, err := a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{Name: "root-wt"}); err != nil {
		t.Fatalf("worktree_enter: %v", err)
	}

	// Root agent exits via discard.
	resp, err := a.handleWorktreeExit(ctx, gen.ProjectWorktreeExitReq{Mode: "discard", Force: true})
	if err != nil {
		t.Fatalf("worktree_exit for root agent: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("worktree_exit status: got %q, want %q", resp.Status, "completed")
	}
}