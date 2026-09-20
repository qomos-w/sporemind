package project

import (
	"path/filepath"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newWriteGuardActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{initialPath: t.TempDir(), persistStore: persist.NewFSPersist(t.TempDir())}
}

// newScopedWriteGuardActor returns an actor whose binding maps carry one
// active worktree "wt-1" bound to agentA, without any real git checkout —
// protectedScope / protectedPathsFor only consult the in-memory maps.
func newScopedWriteGuardActor(t *testing.T) (*Actor, string, string) {
	t.Helper()
	a := newWriteGuardActor(t)
	const agentA = "019f00000000000000000000000000a1"
	const wtID = "wt-1"
	a.agentWorktree = map[string]string{agentA: wtID}
	a.worktrees = map[string]gen.ProjectWorktree{
		wtID: {ID: wtID, Name: "feature", Status: "active", Path: t.TempDir()},
	}
	return a, agentA, wtID
}

func TestSetProtectedFiles(t *testing.T) {
	a := newWriteGuardActor(t)
	resp, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{
		Files: []string{"main.gen.go", "app.manifest.json", "schemas_gen.go", "client.gen.ts"},
	})
	if err != nil {
		t.Fatalf("setProtectedFiles: %v", err)
	}
	if resp.Count != 4 {
		t.Errorf("expected 4 protected files, got %d", resp.Count)
	}
	if _, blocked := a.isProtected("", t.TempDir(), "main.gen.go"); !blocked {
		t.Error("main.gen.go should be protected")
	}
	if _, blocked := a.isProtected("", t.TempDir(), "handlers.go"); blocked {
		t.Error("handlers.go should NOT be protected")
	}
}

func TestSetProtectedFilesClearsAll(t *testing.T) {
	a := newWriteGuardActor(t)
	_, _ = a.setProtectedFiles(nil, gen.SetProtectedFilesReq{Files: []string{"main.gen.go"}})

	resp, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{Files: nil})
	if err != nil {
		t.Fatalf("setProtectedFiles: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("expected 0 protected files, got %d", resp.Count)
	}
	if _, blocked := a.isProtected("", t.TempDir(), "main.gen.go"); blocked {
		t.Error("empty list should clear all protections")
	}
}

func TestPersistProtectedFiles(t *testing.T) {
	store := persist.NewFSPersist(t.TempDir())
	a := &Actor{initialPath: t.TempDir(), persistStore: store}
	a.actorID = "test-project"

	// Set and save (write-through to the config card).
	if _, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{Files: []string{"main.gen.go", "app.manifest.json"}}); err != nil {
		t.Fatalf("setProtectedFiles: %v", err)
	}

	// Load into a fresh actor sharing the same persist store.
	a2 := &Actor{initialPath: t.TempDir(), persistStore: store}
	a2.actorID = a.actorID
	if _, blocked := a2.isProtected("", t.TempDir(), "main.gen.go"); !blocked {
		t.Error("main.gen.go should be protected after reload from config card")
	}
	if _, blocked := a2.isProtected("", t.TempDir(), "app.manifest.json"); !blocked {
		t.Error("app.manifest.json should be protected after reload from config card")
	}
}

func TestIsProtected(t *testing.T) {
	a := newWriteGuardActor(t)
	_, _ = a.setProtectedFiles(nil, gen.SetProtectedFilesReq{Files: []string{"main.gen.go"}})

	root := t.TempDir()

	// Protected file.
	if _, blocked := a.isProtected("", root, "main.gen.go"); !blocked {
		t.Error("main.gen.go should be protected")
	}

	// Non-protected file.
	if _, blocked := a.isProtected("", root, "handlers.go"); blocked {
		t.Error("handlers.go should NOT be protected")
	}
}

func TestProtectedFilesPathIsolate(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, ".sporecode", "generated-files.json")
	a := &Actor{initialPath: dir}
	got := a.protectedFilesPath()
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestSetProtectedFiles_ScopedToWorktree is the core worktree-scoping
// contract: a bound caller's set writes only that worktree's bucket, leaving
// the global (main-tree) list untouched; the bound caller's guard consults
// the union of global list and its own bucket; unbound callers never see the
// bucket.
func TestSetProtectedFiles_ScopedToWorktree(t *testing.T) {
	a, agentA, wtID := newScopedWriteGuardActor(t)

	// Main-tree regen declares the global list.
	if _, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{
		Files: []string{"main.gen.go", "app.manifest.json"},
	}); err != nil {
		t.Fatalf("unbound setProtectedFiles: %v", err)
	}

	// Worktree-bound regen declares a divergent view.
	resp, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{
		Files:         []string{"main.gen.go", "main_run.gen.go"},
		CallerAgentID: agentA,
	})
	if err != nil {
		t.Fatalf("bound setProtectedFiles: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("bound set: Count = %d, want 2", resp.Count)
	}

	// Global list must be untouched by the bound write.
	c, err := a.configSnapshot()
	if err != nil {
		t.Fatalf("configSnapshot: %v", err)
	}
	if len(c.ProtectedFiles) != 2 || c.ProtectedFiles[1] != "app.manifest.json" {
		t.Errorf("global list clobbered by bound write: %+v", c.ProtectedFiles)
	}
	if got := c.ProtectedFilesByWorktree[wtID]; len(got) != 2 || got[1] != "main_run.gen.go" {
		t.Errorf("worktree bucket = %+v, want divergent view", got)
	}

	root := t.TempDir()
	// Bound caller sees global + bucket.
	if _, blocked := a.isProtected(agentA, root, "app.manifest.json"); !blocked {
		t.Error("bound caller should still see global-list protections (union)")
	}
	if _, blocked := a.isProtected(agentA, root, "main_run.gen.go"); !blocked {
		t.Error("bound caller should see its worktree bucket")
	}
	// Unbound caller sees only the global list.
	if _, blocked := a.isProtected("", root, "main_run.gen.go"); blocked {
		t.Error("unbound caller must not see the worktree bucket")
	}
	if _, blocked := a.isProtected("unbound-agent", root, "main_run.gen.go"); blocked {
		t.Error("other unbound caller must not see the worktree bucket")
	}
}

// TestSetProtectedFiles_BoundEmptyClearsBucket verifies the bound-caller
// empty list deletes its bucket (not the global list).
func TestSetProtectedFiles_BoundEmptyClearsBucket(t *testing.T) {
	a, agentA, wtID := newScopedWriteGuardActor(t)

	if _, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{Files: []string{"main.gen.go"}}); err != nil {
		t.Fatalf("unbound set: %v", err)
	}
	if _, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{
		Files:         []string{"main_run.gen.go"},
		CallerAgentID: agentA,
	}); err != nil {
		t.Fatalf("bound set: %v", err)
	}

	resp, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{Files: nil, CallerAgentID: agentA})
	if err != nil {
		t.Fatalf("bound clear: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("bound clear: Count = %d, want 0", resp.Count)
	}

	c, err := a.configSnapshot()
	if err != nil {
		t.Fatalf("configSnapshot: %v", err)
	}
	if _, ok := c.ProtectedFilesByWorktree[wtID]; ok {
		t.Error("empty bound list should delete the bucket")
	}
	if len(c.ProtectedFiles) != 1 {
		t.Errorf("global list must survive a bound clear: %+v", c.ProtectedFiles)
	}
}

// TestSetProtectedFiles_StaleBindingFailsClosed: a caller bound to a
// missing/inactive worktree must not write the global list — that would
// escape the isolation fence into main-tree constraint state.
func TestSetProtectedFiles_StaleBindingFailsClosed(t *testing.T) {
	a := newWriteGuardActor(t)
	a.agentWorktree = map[string]string{"agent-stale": "wt-gone"}
	a.worktrees = map[string]gen.ProjectWorktree{}

	if _, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{
		Files:         []string{"main.gen.go"},
		CallerAgentID: "agent-stale",
	}); err == nil {
		t.Fatal("stale binding must fail closed")
	}

	c, err := a.configSnapshot()
	if err != nil {
		t.Fatalf("configSnapshot: %v", err)
	}
	if len(c.ProtectedFiles) != 0 {
		t.Errorf("failed bound write must not touch the global list: %+v", c.ProtectedFiles)
	}
}

// TestProtectedFilesBucketPersistsAcrossReload verifies the bucket survives a
// config-card round trip and that a card written before the field existed
// (no buckets) loads cleanly.
func TestProtectedFilesBucketPersistsAcrossReload(t *testing.T) {
	store := persist.NewFSPersist(t.TempDir())
	a, agentA, wtID := newScopedWriteGuardActor(t)
	a.persistStore = store
	a.actorID = "test-project"

	if _, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{
		Files:         []string{"main_run.gen.go"},
		CallerAgentID: agentA,
	}); err != nil {
		t.Fatalf("bound set: %v", err)
	}

	a2 := &Actor{initialPath: t.TempDir(), persistStore: store}
	a2.actorID = a.actorID
	a2.agentWorktree = map[string]string{agentA: wtID}
	a2.worktrees = map[string]gen.ProjectWorktree{
		wtID: {ID: wtID, Name: "feature", Status: "active", Path: t.TempDir()},
	}
	if _, blocked := a2.isProtected(agentA, t.TempDir(), "main_run.gen.go"); !blocked {
		t.Error("bucket entry should survive a config-card reload")
	}
	if _, blocked := a2.isProtected("unbound", t.TempDir(), "main_run.gen.go"); blocked {
		t.Error("reloaded bucket must stay invisible to unbound callers")
	}
}

// TestProtectedFilesBucketCascadeDelete verifies that removing a worktree
// drops its protected-files bucket along with the manifest.
func TestProtectedFilesBucketCascadeDelete(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{
		Name:    "feature",
		BaseRef: "HEAD",
	})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentA := "019f00000000000000000000000000a2"
	if err := a.bindWorktree(agentA, wt.ID); err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}

	if _, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{
		Files:         []string{"main_run.gen.go"},
		CallerAgentID: agentA,
	}); err != nil {
		t.Fatalf("bound set: %v", err)
	}
	c, err := a.configSnapshot()
	if err != nil {
		t.Fatalf("configSnapshot: %v", err)
	}
	if _, ok := c.ProtectedFilesByWorktree[wt.ID]; !ok {
		t.Fatal("bucket should exist before worktree removal")
	}

	if err := a.deleteWorktreeState(wt.ID); err != nil {
		t.Fatalf("deleteWorktreeState: %v", err)
	}
	c, err = a.configSnapshot()
	if err != nil {
		t.Fatalf("configSnapshot after delete: %v", err)
	}
	if _, ok := c.ProtectedFilesByWorktree[wt.ID]; ok {
		t.Error("worktree removal must cascade-delete its protected-files bucket")
	}
}

// TestProtectedPathsForUnboundIgnoresBuckets guards the read side: an unbound
// caller with buckets present in the card gets exactly the global list.
func TestProtectedPathsForUnboundIgnoresBuckets(t *testing.T) {
	a, agentA, _ := newScopedWriteGuardActor(t)
	if _, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{Files: []string{"main.gen.go"}}); err != nil {
		t.Fatalf("unbound set: %v", err)
	}
	if _, err := a.setProtectedFiles(nil, gen.SetProtectedFilesReq{
		Files:         []string{"wt-only.gen.go"},
		CallerAgentID: agentA,
	}); err != nil {
		t.Fatalf("bound set: %v", err)
	}

	got := a.protectedPathsFor("")
	if len(got) != 1 || got[0] != "main.gen.go" {
		t.Errorf("unbound protectedPathsFor = %+v, want global list only", got)
	}
	got = a.protectedPathsFor("never-bound")
	if len(got) != 1 || got[0] != "main.gen.go" {
		t.Errorf("unknown caller protectedPathsFor = %+v, want global list only", got)
	}
}
