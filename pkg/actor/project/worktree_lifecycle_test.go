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

// gitCommitIn runs an add+commit inside dir for the given paths. Used by the
// lifecycle tests to stage agent work inside a worktree before merging.
func gitCommitIn(t *testing.T, dir, message string, paths ...string) {
	t.Helper()
	add := exec.Command("git", append([]string{"add"}, paths...)...)
	util.HideWindow(add)
	add.Dir = dir
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add in %s: %v\n%s", dir, err, out)
	}
	commit := exec.Command("git", "commit", "-m", message)
	util.HideWindow(commit)
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit in %s: %v\n%s", dir, err, out)
	}
}

// writeFileIn writes content to a path relative to dir.
func writeFileIn(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// TestWorktreeEnter_Idempotent proves enter creates a worktree on first call and
// returns the same binding (Created=false) on the second call.
func TestWorktreeEnter_Idempotent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	agent := "019fb001000000000000000000000001"
	ctx := callerCtx(t, agent)

	first, err := a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{Name: "feat"})
	if err != nil {
		t.Fatalf("enter #1: %v", err)
	}
	if !first.Created {
		t.Fatal("enter #1: expected Created=true")
	}
	second, err := a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{Name: "feat"})
	if err != nil {
		t.Fatalf("enter #2: %v", err)
	}
	if second.Created {
		t.Fatal("enter #2: expected Created=false (already bound)")
	}
	if second.Worktree.ID != first.Worktree.ID {
		t.Fatalf("enter #2: worktree %q != first %q", second.Worktree.ID, first.Worktree.ID)
	}
}

// TestWorktreeEnter_RoutesCallerToWorktree proves enter actually binds: after
// enter, effectiveRoots returns the worktree path for the caller.
func TestWorktreeEnter_RoutesCallerToWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	agent := "019fb002000000000000000000000002"
	ctx := callerCtx(t, agent)

	res, err := a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{Name: "isolation"})
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	roots := a.effectiveRoots(bindKey(t, agent))
	if len(roots) != 1 || roots[0].Path != res.Worktree.Path {
		t.Fatalf("after enter, effectiveRoots = %+v, want [%s]", roots, res.Worktree.Path)
	}
}

// TestWorktreeExit_Discard proves the discard path: worktree is deleted, the
// binding is cleared, and the caller routes back to the main repo.
func TestWorktreeExit_Discard(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	agent := "019fb003000000000000000000000003"
	ctx := callerCtx(t, agent)

	res, err := a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{Name: "tmp"})
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	wtPath := res.Worktree.Path

	out, err := a.handleWorktreeExit(ctx, gen.ProjectWorktreeExitReq{Mode: "discard", Force: true})
	if err != nil {
		t.Fatalf("exit discard: %v", err)
	}
	if out.Status != "completed" {
		t.Fatalf("exit discard: Status=%q, want completed", out.Status)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("exit discard: worktree dir still exists at %s", wtPath)
	}
	// Binding cleared → caller routes to main roots again.
	if got := a.effectiveRoots(bindKey(t, agent)); len(got) == 0 || got[0].Path == wtPath {
		t.Fatalf("exit discard: effectiveRoots still routed to worktree: %+v", got)
	}
}

// TestWorktreeExit_Merge_Autonomous proves an autonomous (coder) agent merges
// its branch into the main repo's base branch and the worktree is removed.
func TestWorktreeExit_Merge_Autonomous(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	agent := "019fb004000000000000000000000004"
	ctx := callerCtx(t, agent)

	res, err := a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{Name: "feature-merge"})
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	// enter ignores Name and derives a per-agent exclusive name <callerID>-<random>.
	agentKey := bindKey(t, agent)
	if !strings.HasPrefix(res.Worktree.Name, agentKey+"-") {
		t.Fatalf("enter: Name=%q, want prefix %q (callerID-derived)", res.Worktree.Name, agentKey+"-")
	}
	if res.Worktree.Name == "feature-merge" {
		t.Fatalf("enter: Name parameter should be ignored, got %q", res.Worktree.Name)
	}
	// Commit a new file in the worktree so the branch has work to merge.
	writeFileIn(t, res.Worktree.Path, "new.txt", "merged content\n")
	gitCommitIn(t, res.Worktree.Path, "add new.txt", "new.txt")

	out, err := a.handleWorktreeExit(ctx, gen.ProjectWorktreeExitReq{Mode: "merge"})
	if err != nil {
		t.Fatalf("exit merge autonomous: %v", err)
	}
	if out.Status != "completed" {
		t.Fatalf("exit merge: Status=%q, want completed", out.Status)
	}
	// The merged file must be present on the main repo's working tree.
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatalf("merged file missing on main repo: %v", err)
	}
	if _, err := os.Stat(res.Worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("exit merge: worktree dir still exists at %s", res.Worktree.Path)
	}
	// The base branch must contain the merge commit referencing the file.
	show := exec.Command("git", "log", "--oneline")
	util.HideWindow(show)
	show.Dir = dir
	if logOut, err := show.CombinedOutput(); err != nil {
		t.Fatalf("git log on main: %v\n%s", err, logOut)
	} else if !strings.Contains(string(logOut), "merge worktree ") {
		t.Fatalf("merge commit not found on main:\n%s", logOut)
	}
}

// TestWorktreeExit_Merge_ConflictKeepsWorktree proves a conflicting merge fails
// WITHOUT deleting the worktree, leaving it for the agent to resolve.
func TestWorktreeExit_Merge_ConflictKeepsWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	agent := "019fb005000000000000000000000005"
	ctx := callerCtx(t, agent)

	res, err := a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{Name: "conflict-feat"})
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	// Diverging change in the worktree on README.md.
	writeFileIn(t, res.Worktree.Path, "README.md", "# worktree side\n")
	gitCommitIn(t, res.Worktree.Path, "worktree edits README", "README.md")
	// Conflicting change on the main repo's README.md.
	writeFileIn(t, dir, "README.md", "# main side\n")
	gitCommitIn(t, dir, "main edits README", "README.md")

	_, err = a.handleWorktreeExit(ctx, gen.ProjectWorktreeExitReq{Mode: "merge"})
	if err == nil {
		t.Fatal("exit merge: expected conflict error, got nil")
	}
	// Worktree must be kept so the agent can resolve the conflict.
	if _, err := os.Stat(res.Worktree.Path); os.IsNotExist(err) {
		t.Fatal("exit merge conflict: worktree was deleted; should be kept")
	}
	// Main repo must NOT be left mid-merge.
	status := exec.Command("git", "status", "--porcelain")
	util.HideWindow(status)
	status.Dir = dir
	stOut, _ := status.CombinedOutput()
	if strings.Contains(string(stOut), "Unmerged paths") || strings.Contains(string(stOut), "UU") {
		t.Fatalf("main repo left mid-merge after conflict:\n%s", stOut)
	}
}

// TestMergeIntoBase_ConflictRestoresSnapshot proves that when a worktree merge
// conflicts, the snapshotted user changes are restored to the main repo's
// working tree. The snapshotted file must reappear as dirty after the failed
// merge, and no snapshot ref may remain.
func TestMergeIntoBase_ConflictRestoresSnapshot(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "conflict-stash-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	// Conflicting change on README.md in the worktree (committed).
	writeFileIn(t, wt.Path, "README.md", "# worktree side\n")
	gitCommitIn(t, wt.Path, "worktree edits README", "README.md")
	// Conflicting change on README.md in the main repo (committed).
	writeFileIn(t, dir, "README.md", "# main side\n")
	gitCommitIn(t, dir, "main edits README", "README.md")
	// Uncommitted, non-conflicting user change in the main repo — this is
	// the dirt that the snapshot must handle and then restore after the
	// merge conflict.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("user note"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitRun(nil, dir, "add", "notes.txt"); err != nil {
		t.Fatal(err)
	}

	_, err = a.mergeWorktreeIntoBase(nil, wt.ID, "")
	if err == nil {
		t.Fatal("expected merge conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "merge branch") {
		t.Fatalf("expected merge error, got: %v", err)
	}
	// Worktree must be kept (merge failed).
	if _, exists := a.worktrees[wt.ID]; !exists {
		t.Error("worktree must be kept when merge conflicts")
	}
	// The snapshotted user change must be restored.
	data, rerr := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if rerr != nil {
		t.Fatalf("notes.txt should exist after snapshot restore: %v", rerr)
	}
	if string(data) != "user note" {
		t.Errorf("notes.txt content should be restored, got %q", string(data))
	}
	// The snapshot ref must have been consumed (restore succeeded).
	out, _ := gitRun(nil, dir, "for-each-ref", "--format=%(refname)", "refs/sporemind/snapshot/")
	if strings.TrimSpace(out) != "" {
		t.Errorf("snapshot refs should be empty after restore, got: %s", out)
	}
	// Main repo must not be left mid-merge.
	stOut, _ := gitRun(nil, dir, "status", "--porcelain")
	if strings.Contains(string(stOut), "UU") {
		t.Fatalf("main repo left mid-merge after conflict:\n%s", stOut)
	}
}

// TestMergeIntoBase_SnapshotRestoresMixedModAndDelete is a regression for the
// deleted-file poisoning case: a snapshot can capture both modifications and
// deletions, and restoreWorkflowSnapshot must handle both. If it blindly runs
// `git checkout <sha> -- <paths>` for a deleted path, git fatal-errors and the
// whole batch fails.
func TestMergeIntoBase_SnapshotRestoresMixedModAndDelete(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "mixed-snapshot-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "feature.txt"), []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, wt.Path, "add", "feature.txt")
	gitOut(t, wt.Path, "commit", "-m", "work")

	// Tracked files on main: one will be modified, one deleted.
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", "keep.txt", "gone.txt")
	gitOut(t, dir, "commit", "-m", "seed files")

	// User dirty: modify keep.txt, delete gone.txt.
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "rm", "gone.txt")

	if _, err := a.mergeWorktreeIntoBase(nil, wt.ID, ""); err != nil {
		t.Fatalf("merge with mixed user dirty: %v", err)
	}

	// Modified file must be restored to v2.
	data, err := os.ReadFile(filepath.Join(dir, "keep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v2" {
		t.Errorf("keep.txt content should be restored, got %q", string(data))
	}
	// Deleted file must stay deleted.
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); !os.IsNotExist(err) {
		t.Errorf("gone.txt should remain deleted, got err=%v", err)
	}
	// Snapshot ref must be consumed.
	out, _ := gitRun(nil, dir, "for-each-ref", "--format=%(refname)", "refs/sporemind/snapshot/")
	if strings.TrimSpace(out) != "" {
		t.Errorf("snapshot refs should be empty after restore, got: %s", out)
	}
}

// TestMergeIntoBase_PreflightRestoresOrphanSnapshot proves that a snapshot ref
// left behind by a crashed merge is restored (and its ref deleted) before the
// current merge starts, so it does not corrupt the main repo state.
func TestMergeIntoBase_PreflightRestoresOrphanSnapshot(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	// Ignore the actor's runtime-state directory so a clean status check below
	// does not see untracked .sporecode files.
	writeFileIn(t, dir, ".gitignore", ".sporecode/\n")
	gitCommitIn(t, dir, "ignore .sporecode", ".gitignore")

	// Seed a tracked file on main.
	writeFileIn(t, dir, "shared.txt", "base\n")
	gitCommitIn(t, dir, "add shared.txt", "shared.txt")

	// Create an orphan snapshot capturing an unstaged modification to shared.txt.
	writeFileIn(t, dir, "shared.txt", "orphan dirty\n")
	gitOut(t, dir, "add", "-u")
	tree := strings.TrimSpace(gitOut(t, dir, "write-tree"))
	commit := strings.TrimSpace(gitOut(t, dir, "commit-tree", tree, "-p", "HEAD", "-m", "orphan snapshot"))
	orphanRef := "refs/sporemind/snapshot/orphan"
	gitOut(t, dir, "update-ref", orphanRef, commit)
	gitOut(t, dir, "reset", "--hard", "HEAD")

	st, _ := gitRun(nil, dir, "status", "--porcelain")
	if strings.TrimSpace(st) != "" {
		t.Fatalf("main repo not clean before merge: %s", st)
	}
	refsBefore, _ := gitRun(nil, dir, "for-each-ref", "--format=%(refname)", "refs/sporemind/snapshot/")
	if !strings.Contains(refsBefore, orphanRef) {
		t.Fatalf("orphan snapshot ref missing before merge: %s", refsBefore)
	}

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "post-orphan-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	writeFileIn(t, wt.Path, "feature.txt", "work\n")
	gitCommitIn(t, wt.Path, "add feature.txt", "feature.txt")

	if _, err := a.mergeWorktreeIntoBase(nil, wt.ID, ""); err != nil {
		t.Fatalf("merge after orphan preflight: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "shared.txt"))
	if err != nil {
		t.Fatalf("shared.txt missing after merge: %v", err)
	}
	got := strings.ReplaceAll(string(data), "\r\n", "\n")
	if got != "orphan dirty\n" {
		t.Errorf("shared.txt content = %q, want %q", string(data), "orphan dirty\n")
	}
	out, _ := gitRun(nil, dir, "for-each-ref", "--format=%(refname)", "refs/sporemind/snapshot/")
	if strings.TrimSpace(out) != "" {
		t.Errorf("snapshot refs should be empty after merge, got: %s", out)
	}
}

// TestMergeIntoBase_PreflightDoesNotBlockOnInvalidSnapshot proves that an orphan
// snapshot ref pointing at a non-commit object is cleaned up and does not stop
// the current merge from succeeding.
func TestMergeIntoBase_PreflightDoesNotBlockOnInvalidSnapshot(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	blobSHA := strings.TrimSpace(gitOut(t, dir, "hash-object", "-w", "--stdin"))
	orphanRef := "refs/sporemind/snapshot/invalid"
	gitOut(t, dir, "update-ref", orphanRef, blobSHA)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "after-invalid-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	writeFileIn(t, wt.Path, "feature.txt", "work\n")
	gitCommitIn(t, wt.Path, "add feature.txt", "feature.txt")

	if _, err := a.mergeWorktreeIntoBase(nil, wt.ID, ""); err != nil {
		t.Fatalf("merge with invalid orphan snapshot: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "feature.txt")); err != nil {
		t.Fatalf("merged file missing: %v", err)
	}
	out, _ := gitRun(nil, dir, "for-each-ref", "--format=%(refname)", "refs/sporemind/snapshot/")
	if strings.TrimSpace(out) != "" {
		t.Errorf("invalid snapshot ref should be deleted, got: %s", out)
	}
}

// TestWorktreeExit_StaleBindingIsIdempotent proves that when a worktree is
// removed out-of-band (e.g. admin discard), a subsequent exit call by the bound
// agent clears the stale binding and returns success instead of erroring.
func TestWorktreeExit_StaleBindingIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	agent := "019fb006000000000000000000000006"
	ctx := callerCtx(t, agent)

	res, err := a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{Name: "stale"})
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	// Simulate out-of-band removal: admin discards the worktree directly.
	if err := a.handleWorktreeDiscard(nil, gen.ProjectWorktreeDiscardReq{
		WorktreeID: res.Worktree.ID,
		Force:      true,
	}); err != nil {
		t.Fatalf("admin discard: %v", err)
	}
	// The agent's binding should already be cleared by the admin discard.
	if _, ok := a.callerBoundWorktree(bindKey(t, agent)); ok {
		t.Fatal("admin discard did not clear agent binding")
	}
	// Exit should succeed idempotently (no error, no cascade).
	out, err := a.handleWorktreeExit(ctx, gen.ProjectWorktreeExitReq{Mode: "discard"})
	if err != nil {
		t.Fatalf("exit after stale removal: unexpected error: %v", err)
	}
	if out.Status != "completed" {
		t.Fatalf("exit after stale removal: Status=%q, want completed", out.Status)
	}
}

// TestWorktreeExit_NotBoundIsSuccess proves that calling exit when the agent
// is not bound to any worktree returns success (idempotent) rather than an
// error, preventing the error cascade on retry after a prior cleanup.
func TestWorktreeExit_NotBoundIsSuccess(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	agent := "019fb007000000000000000000000007"
	ctx := callerCtx(t, agent)

	out, err := a.handleWorktreeExit(ctx, gen.ProjectWorktreeExitReq{Mode: "discard"})
	if err != nil {
		t.Fatalf("exit not-bound: unexpected error: %v", err)
	}
	if out.Status != "completed" {
		t.Fatalf("exit not-bound: Status=%q, want completed", out.Status)
	}
}
