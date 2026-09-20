package project

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestNoGitModeFlag_DefaultsFalse verifies the flag starts unset on a fresh
// project actor.
func TestNoGitModeFlag_DefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	a, _ := worktreeTestActor(t, dir)
	if a.noGitMode() {
		t.Fatal("noGitMode: expected false by default")
	}
}

// TestNoGitModeFlag_PersistRoundtrip pins the persistence wiring: the flag
// survives a setNoGitMode → fresh Load cycle through the config card, exactly
// like Roots/MountedCardRefs.
func TestNoGitModeFlag_PersistRoundtrip(t *testing.T) {
	dir := t.TempDir()
	a, _ := worktreeTestActor(t, dir)

	a.setNoGitMode(true)
	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// A fresh actor over the same persist store must see the persisted flag.
	b := NewActor(t.TempDir())().(*Actor)
	b.actorID = a.actorID
	b.persistStore = a.persistStore
	if err := b.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !b.noGitMode() {
		t.Fatal("reloaded actor: noGitMode lost after Save/Load")
	}

	// Turning it off again must persist too.
	b.setNoGitMode(false)
	if err := b.Save(); err != nil {
		t.Fatalf("save off: %v", err)
	}
	c := NewActor(t.TempDir())().(*Actor)
	c.actorID = a.actorID
	c.persistStore = a.persistStore
	if err := c.Load(); err != nil {
		t.Fatalf("load c: %v", err)
	}
	if c.noGitMode() {
		t.Fatal("reloaded actor: noGitMode still true after disabling")
	}
}

// TestHandleNoGitModeGet_NonGitRoot reports the auto-detection half: a project
// root without a git repository yields HasGitRepo=false while NoGitMode stays
// whatever the persisted flag says (false here).
func TestHandleNoGitModeGet_NonGitRoot(t *testing.T) {
	dir := t.TempDir()
	a, ctx := worktreeTestActor(t, dir)

	resp, err := a.handleNoGitModeGet(ctx, gen.ProjectNoGitModeGetReq{})
	if err != nil {
		t.Fatalf("no_git_mode_get: %v", err)
	}
	if resp.NoGitMode {
		t.Error("NoGitMode: expected false")
	}
	if resp.HasGitRepo {
		t.Error("HasGitRepo: expected false for non-git root")
	}
}

// TestHandleNoGitModeGet_GitRoot reports HasGitRepo=true once the root is an
// actual git repository.
func TestHandleNoGitModeGet_GitRoot(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	resp, err := a.handleNoGitModeGet(ctx, gen.ProjectNoGitModeGetReq{})
	if err != nil {
		t.Fatalf("no_git_mode_get: %v", err)
	}
	if !resp.HasGitRepo {
		t.Error("HasGitRepo: expected true for git root")
	}
}

// TestHandleNoGitModeSet_PersistsAndReflectsInGet drives the set callable and
// verifies the response, the in-memory flag, and the persisted value.
func TestHandleNoGitModeSet_PersistsAndReflectsInGet(t *testing.T) {
	dir := t.TempDir()
	a, ctx := worktreeTestActor(t, dir)

	resp, err := a.handleNoGitModeSet(ctx, gen.ProjectNoGitModeSetReq{NoGitMode: true})
	if err != nil {
		t.Fatalf("no_git_mode_set: %v", err)
	}
	if !resp.NoGitMode {
		t.Error("set response: NoGitMode expected true")
	}
	if !a.noGitMode() {
		t.Error("in-memory flag not set after no_git_mode_set")
	}

	got, err := a.handleNoGitModeGet(ctx, gen.ProjectNoGitModeGetReq{})
	if err != nil {
		t.Fatalf("no_git_mode_get: %v", err)
	}
	if !got.NoGitMode {
		t.Error("no_git_mode_get: NoGitMode expected true after set")
	}
}

// TestHandleWorkflowCreateWorktree_NoGitModeSkips pins the workflow behavior in
// explicitly enabled no-git mode: the callable returns an empty WorktreeID and
// creates nothing (no worktree, no binding).
func TestHandleWorkflowCreateWorktree_NoGitModeSkips(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir) // the root IS a repo; the skip must come from the flag
	a, ctx := worktreeTestActor(t, dir)
	a.setNoGitMode(true)

	resp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-nogit",
		AgentActorID:  "owner-agent-1",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree in no-git mode: %v", err)
	}
	if resp.WorktreeID != "" {
		t.Fatalf("WorktreeID = %q, want empty in no-git mode", resp.WorktreeID)
	}
	if len(a.worktrees) != 0 {
		t.Fatalf("expected no worktrees created, got %d", len(a.worktrees))
	}
	if _, ok := a.callerBoundWorktree("owner-agent-1"); ok {
		t.Fatal("owner agent must not be bound to a worktree in no-git mode")
	}
}

// TestHandleWorkflowCreateWorktree_SkipsWhenRootNotGit pins the auto-detection
// half: a project root without a git repository skips worktree creation even
// when the explicit flag is off.
func TestHandleWorkflowCreateWorktree_SkipsWhenRootNotGit(t *testing.T) {
	dir := t.TempDir() // no initGitRepo
	a, ctx := worktreeTestActor(t, dir)

	resp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-nogit-auto",
		AgentActorID:  "owner-agent-2",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree on non-git root: %v", err)
	}
	if resp.WorktreeID != "" {
		t.Fatalf("WorktreeID = %q, want empty for non-git root", resp.WorktreeID)
	}
	if len(a.worktrees) != 0 {
		t.Fatalf("expected no worktrees created, got %d", len(a.worktrees))
	}
}
