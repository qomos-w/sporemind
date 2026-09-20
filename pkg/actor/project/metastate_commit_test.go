package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

// gitOut runs git in dir and fails the test on error, returning combined output.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	util.HideWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

func repoStatusClean(t *testing.T, dir string) bool {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")) == ""
}

func cardRaw(title string) string {
	return "---\nid: " + title + "\ntags: [task]\ncreated: 2026-08-18T00:00:00Z\nmodified: 2026-08-18T00:00:00Z\nstatus: todo\n---\n\nbody for " + title + "\n"
}

// TestMergeIntoBaseReconcilesRuntimeStateDirty reproduces the workflow-stop
// deadlock scenario: runtime-state writes have left the main repo dirty while
// the owner worktree is ready to merge. The merge must reconcile the runtime
// state with a scoped commit, succeed, and leave the repo clean.
func TestMergeIntoBaseReconcilesRuntimeStateDirty(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "reconcile-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "feature.txt"), []byte("work"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	gitOut(t, wt.Path, "add", "-A")
	gitOut(t, wt.Path, "commit", "-m", "work")
	// Simulate runtime-state writes landing in the main repo after the
	// worktree was created: an untracked wiki card and a tracked, modified
	// graphs.json.
	if err := os.MkdirAll(filepath.Join(dir, ".sporecode", "wiki"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".sporecode", "wiki", "coord.md"), []byte(cardRaw("coord")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".sporecode", "graphs.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", ".sporecode/graphs.json")
	gitOut(t, dir, "commit", "-m", "seed graphs")
	if err := os.WriteFile(filepath.Join(dir, ".sporecode", "graphs.json"), []byte(`{"target\u0000g1":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.mergeWorktreeIntoBase(nil, wt.ID, ""); err != nil {
		t.Fatalf("merge into base with runtime-state dirt: %v", err)
	}
	if !repoStatusClean(t, dir) {
		t.Errorf("merge left main repo dirty:\n%s", gitOut(t, dir, "status", "--porcelain"))
	}
	if _, err := os.Stat(filepath.Join(dir, "feature.txt")); err != nil {
		t.Errorf("merged content missing: %v", err)
	}
	// The reconciled runtime files must be tracked, not left untracked.
	files := gitOut(t, dir, "ls-files")
	if !strings.Contains(files, ".sporecode/wiki/coord.md") {
		t.Errorf("coord card not tracked after reconcile; ls-files=%q", files)
	}
}

// snapshotRefs returns the current refs/sporemind/snapshot/* names.
func snapshotRefs(t *testing.T, dir string) []string {
	t.Helper()
	out := gitOut(t, dir, "for-each-ref", "--format=%(refname)", "refs/sporemind/snapshot/")
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			refs = append(refs, line)
		}
	}
	return refs
}

// requireNoSnapshotRefs fails if any workflow snapshot refs are left behind.
func requireNoSnapshotRefs(t *testing.T, dir string) {
	t.Helper()
	if got := snapshotRefs(t, dir); len(got) != 0 {
		t.Errorf("snapshot refs remain after merge: %v", got)
	}
}

// requireNoSporemindStash fails if a legacy workflow-stop auto-stash entry remains.
func requireNoSporemindStash(t *testing.T, dir string) {
	t.Helper()
	if out := gitOut(t, dir, "stash", "list"); strings.Contains(out, "sporemind-workflow-stop-auto") {
		t.Errorf("legacy sporemind-workflow-stop-auto stash remains: %q", out)
	}
}

// TestMergeIntoBaseSnapshotsUserDirty ensures the pre-check snapshots
// uncommitted changes outside the runtime-state family into a named ref so the
// merge proceeds, then restores them and deletes the ref. The worktree is
// merged and removed.
func TestMergeIntoBaseSnapshotsUserDirty(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "userdirty-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	// Commit something to the worktree branch so the merge has content.
	if err := os.WriteFile(filepath.Join(wt.Path, "feature.txt"), []byte("new feature"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitRun(nil, wt.Path, "add", "feature.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitRun(nil, wt.Path, "commit", "-m", "worktree feature"); err != nil {
		t.Fatal(err)
	}
	// Create uncommitted user changes in the main repo.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = a.mergeWorktreeIntoBase(nil, wt.ID, "")
	if err != nil {
		t.Fatalf("merge should snapshot user dirt and succeed, got: %v", err)
	}
	if _, exists := a.worktrees[wt.ID]; exists {
		t.Error("worktree must be removed after successful merge")
	}
	// The snapshot must be restored — README.md should be dirty again.
	data, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# modified" {
		t.Errorf("snapshotted changes should be restored after merge, got %q", string(data))
	}
	if branchExists(t, dir, "userdirty-wt") {
		t.Error("worktree branch should be deleted after merge")
	}
	requireNoSnapshotRefs(t, dir)
	requireNoSporemindStash(t, dir)
}

// TestMergeIntoBaseCJKCardNames is a regression for the 2026-08-19 incident:
// newline-mode porcelain always double-quotes non-ASCII paths (core.quotepath=false
// only disables octal escaping, not the quoting), so the old parser carried
// literal quotes into the pathspec and workflow_stop failed with
// "did not match any file(s) known to git". With -z parsing, a CJK-named
// runtime card is reconciled via commit and a CJK-named tracked user file is
// snapshotted and restored with a clean pathspec.
func TestMergeIntoBaseCJKCardNames(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "cjk-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "feature.txt"), []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, wt.Path, "add", "-A")
	gitOut(t, wt.Path, "commit", "-m", "work")
	// Runtime-state dirt with a CJK card name (the incident's exact shape).
	if err := os.MkdirAll(filepath.Join(dir, ".sporecode", "wiki"), 0755); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(dir, ".sporecode", "wiki", "T2 移植 UI 原语.md")
	if err := os.WriteFile(card, []byte(cardRaw("T2 移植 UI 原语")), 0o644); err != nil {
		t.Fatal(err)
	}
	// Tracked user dirt with a CJK name: must be snapshotted, not committed.
	user := filepath.Join(dir, "说明文档.md")
	if err := os.WriteFile(user, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", "--", "说明文档.md")
	gitOut(t, dir, "commit", "-m", "seed docs")
	if err := os.WriteFile(user, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.mergeWorktreeIntoBase(nil, wt.ID, ""); err != nil {
		t.Fatalf("merge with CJK dirty paths: %v", err)
	}
	// The runtime card must be reconciled into a commit, not left dirty.
	// ls-files quotes non-ASCII paths; quotepath=false keeps the CJK bytes
	// literal so the substring check below can match.
	files := gitOut(t, dir, "-c", "core.quotepath=false", "ls-files")
	if !strings.Contains(files, ".sporecode/wiki/T2 移植 UI 原语.md") {
		t.Errorf("CJK card not tracked after reconcile; ls-files=%q", files)
	}
	// The user file must keep its snapshotted edit after the post-merge restore.
	data, err := os.ReadFile(user)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v2" {
		t.Errorf("CJK user file lost snapshotted edit; got %q, want %q", string(data), "v2")
	}
	requireNoSnapshotRefs(t, dir)
	requireNoSporemindStash(t, dir)
}

// TestMergeIntoBaseSnapshotConflictGuard verifies that when the merge itself
// touches a path that was also snapshotted as user-dirty, the snapshot is *not*
// restored. The snapshot ref must be preserved so a later workflow can inspect
// or discard it.
func TestMergeIntoBaseSnapshotConflictGuard(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	// Seed a tracked file that both the worktree branch and the main repo will edit.
	shared := filepath.Join(dir, "shared.txt")
	if err := os.WriteFile(shared, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", "shared.txt")
	gitOut(t, dir, "commit", "-m", "seed shared")
	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "conflict-guard-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "shared.txt"), []byte("branch v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, wt.Path, "add", "shared.txt")
	gitOut(t, wt.Path, "commit", "-m", "branch edit")
	// Uncommitted user edit to the same path in the main repo.
	if err := os.WriteFile(shared, []byte("user dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.mergeWorktreeIntoBase(nil, wt.ID, ""); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// The merge result must win; restoring the snapshot would silently revert it.
	data, err := os.ReadFile(shared)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "branch v2" {
		t.Errorf("shared.txt = %q, want branch v2", string(data))
	}
	refs := snapshotRefs(t, dir)
	if len(refs) == 0 {
		t.Error("snapshot ref should be preserved when restore conflicts")
	}
	if !strings.Contains(refs[0], wt.ID) {
		t.Errorf("snapshot ref should match worktree ID; got %q", refs)
	}
	if branchExists(t, dir, "conflict-guard-wt") {
		t.Error("worktree branch should be deleted after merge")
	}
	requireNoSporemindStash(t, dir)
}

// TestWorkflowCreateWorktreeReusesStampedWorktree pins the retry path after a
// failed workflow_stop exited workflow mode: re-activation must reuse the
// map's stamped owner worktree instead of failing on the deterministic name.
func TestWorkflowCreateWorktreeReusesStampedWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "reuse-map"}); err != nil {
		t.Fatalf("create map: %v", err)
	}
	agent := bindKey(t, "019fa666660000000000000000000007")
	resp1, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{WorkflowMapID: "reuse-map", AgentActorID: agent})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if got := a.mapOwnerWorktreeID("reuse-map"); got != resp1.WorktreeID {
		t.Fatalf("map card not stamped with worktree id; stamped=%q created=%q", got, resp1.WorktreeID)
	}
	before := len(a.worktrees)
	resp2, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{WorkflowMapID: "reuse-map", AgentActorID: agent})
	if err != nil {
		t.Fatalf("re-create (reuse path): %v", err)
	}
	if resp2.WorktreeID != resp1.WorktreeID {
		t.Errorf("reuse must return the same worktree; got %q want %q", resp2.WorktreeID, resp1.WorktreeID)
	}
	if len(a.worktrees) != before {
		t.Errorf("reuse must not create a new worktree; before=%d after=%d", before, len(a.worktrees))
	}
}
