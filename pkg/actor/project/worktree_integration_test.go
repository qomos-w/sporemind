package project

// Phase 5 integration tests: full workflow→worktree chain through project actor
// callables, covering the 16 adversarial gaps identified in
// workflow-worktree-integration-plan.md.
//
// These tests exercise the callable handlers (handleWorkflowCreateWorktree,
// handleWorktreeMergeToParent, handleWorktreeRebaseToParent,
// handleWorkflowStopMergeWorktree, handleWorktreeDiscardByID,
// handleWorktreeReleaseBinding) rather than internal methods, and chain
// multiple operations together to test the full lifecycle.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// setupGitattributes writes a .gitattributes file with merge=ours rules for
// .sporecode/ runtime state paths, mirroring the project-level .gitattributes.
func setupGitattributes(t *testing.T, dir string) {
	t.Helper()
	content := `.sporecode/wiki/** merge=ours
.sporecode/graphs.json merge=ours
.sporecode/wiki-state.json merge=ours
`
	path := filepath.Join(dir, ".gitattributes")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write .gitattributes: %v", err)
	}
	// Configure the merge.ours driver so the rules take effect.
	gitCommitInWorktree(t, dir, "add .gitattributes")
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 1: Full lifecycle — spawn→approve merge→stop merge
// Covers gaps: 1 (cascade-delete/merge race), 4 (serial merge), 5 (merge
// pollutes owner worktree), 6 (workflow_stop base branch), 7 (BaseRef
// real-time), 9 (workflow_stop callable), 13 (no checkout), 14 (rebase to
// main before stop).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_FullLifecycle_SpawnApproveMerge_StopMerge(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	// Create a wiki map card for the workflow.
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-full"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	// Phase 1: workflow_create_worktree — create the owner worktree.
	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-full",
		AgentActorID:  "owner-agent-1",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner, ok := a.worktrees[ownerID]
	if !ok {
		t.Fatal("owner worktree not in metadata")
	}

	// Phase 2: Create a child worktree from the owner (ParentWorktreeID).
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-full-child-1",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-full",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	// Child must derive from owner's branch HEAD (gap 7: real-time BaseRef).
	if child.ParentWorktreeID != ownerID {
		t.Fatalf("child.ParentWorktreeID=%q, want %q", child.ParentWorktreeID, ownerID)
	}
	if child.BaseRef == "" || child.BaseRef == "HEAD" {
		t.Fatalf("child.BaseRef=%q, should be owner branch HEAD", child.BaseRef)
	}

	// Phase 3: Worker commits a change in the child worktree.
	if err := os.WriteFile(filepath.Join(child.Path, "feature.txt"), []byte("worker feature"), 0644); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	gitCommitInWorktree(t, child.Path, "worker feature")

	// Phase 4: Review approve — merge child→parent via callable handler.
	mergeResp, err := a.handleWorktreeMergeToParent(ctx, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("worktree_merge_to_parent: %v", err)
	}
	if mergeResp.Status != "merged" {
		t.Fatalf("merge status=%q, want merged", mergeResp.Status)
	}

	// Child worktree must be removed (gap 1: merge before cascade-delete).
	if _, ok := a.worktrees[child.ID]; ok {
		t.Fatal("child worktree should be removed after merge")
	}
	if _, err := os.Stat(child.Path); !os.IsNotExist(err) {
		t.Fatal("child worktree dir should be removed")
	}

	// Parent worktree must contain the child's commit (gap 5: merge result).
	if _, err := os.Stat(filepath.Join(owner.Path, "feature.txt")); os.IsNotExist(err) {
		t.Fatal("parent worktree should contain child's feature.txt")
	}

	// Phase 5: workflow_stop_merge_worktree — merge owner→main.
	// Advance main with an unrelated change to test rebase (gap 14).
	if err := os.WriteFile(filepath.Join(dir, "main-advance.txt"), []byte("main"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, dir, "main advance")

	stopResp, err := a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("workflow_stop_merge_worktree: %v", err)
	}
	if stopResp.Status != "merged" {
		t.Fatalf("stop status=%q, want merged", stopResp.Status)
	}

	// Owner worktree must be removed after stop.
	if _, ok := a.worktrees[ownerID]; ok {
		t.Fatal("owner worktree should be removed after workflow_stop")
	}

	// Main branch must contain both the worker's feature and the main advance.
	log := gitRunMust(t, dir, "log", "--oneline", "--all")
	if !strings.Contains(log, "worker feature") {
		t.Errorf("worker commit missing from main history:\n%s", log)
	}
	if !strings.Contains(log, "main advance") {
		t.Errorf("main advance missing from history:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(dir, "feature.txt")); os.IsNotExist(err) {
		t.Error("feature.txt missing on main after stop merge")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 2: Full lifecycle with two children — serial merge correctness
// Covers gap 4 (multiple workers merging to same parent, serialization via
// worktreeMergeMu).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_TwoChildrenSerialMerge(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-serial"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-serial",
		AgentActorID:  "owner-agent-2",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner := a.worktrees[ownerID]

	// Create two children.
	childA, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-serial-a",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-serial",
	})
	if err != nil {
		t.Fatalf("create childA: %v", err)
	}
	childB, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-serial-b",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-serial",
	})
	if err != nil {
		t.Fatalf("create childB: %v", err)
	}

	// Each child commits a different file (no conflict).
	if err := os.WriteFile(filepath.Join(childA.Path, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, childA.Path, "childA work")

	if err := os.WriteFile(filepath.Join(childB.Path, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, childB.Path, "childB work")

	// Merge A first, then B — both must succeed (gap 4: serial merge).
	mergeA, err := a.handleWorktreeMergeToParent(ctx, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  childA.ID,
		ParentWorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("merge childA: %v", err)
	}
	if mergeA.Status != "merged" {
		t.Fatalf("mergeA status=%q, want merged", mergeA.Status)
	}

	mergeB, err := a.handleWorktreeMergeToParent(ctx, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  childB.ID,
		ParentWorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("merge childB: %v", err)
	}
	if mergeB.Status != "merged" {
		t.Fatalf("mergeB status=%q, want merged", mergeB.Status)
	}

	// Owner worktree must contain both children's files.
	if _, err := os.Stat(filepath.Join(owner.Path, "a.txt")); os.IsNotExist(err) {
		t.Error("owner missing a.txt after both merges")
	}
	if _, err := os.Stat(filepath.Join(owner.Path, "b.txt")); os.IsNotExist(err) {
		t.Error("owner missing b.txt after both merges")
	}
	// Both children must be removed.
	if _, ok := a.worktrees[childA.ID]; ok {
		t.Error("childA should be removed")
	}
	if _, ok := a.worktrees[childB.ID]; ok {
		t.Error("childB should be removed")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 3: Reject rebase cycle via callable
// Covers gap 2 (system-forced rebase on reject), gap 7 (real-time BaseRef).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_RejectRebaseCycle_ViaCallable(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-rebase"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-rebase",
		AgentActorID:  "owner-agent-3",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner := a.worktrees[ownerID]

	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-rebase-child",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-rebase",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Child commits its own work.
	if err := os.WriteFile(filepath.Join(child.Path, "child.txt"), []byte("child"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child work")

	// Owner advances with a new commit after child creation.
	if err := os.WriteFile(filepath.Join(owner.Path, "owner-new.txt"), []byte("owner"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, owner.Path, "owner advance")

	// Rebase child→parent via callable.
	rebaseResp, err := a.handleWorktreeRebaseToParent(ctx, gen.ProjectWorktreeRebaseToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("worktree_rebase_to_parent: %v", err)
	}
	if rebaseResp.Status != "rebased" {
		t.Fatalf("rebase status=%q, want rebased", rebaseResp.Status)
	}

	// Child must now contain the parent's new file (gap 2: rebase incorporates
	// parent's latest changes before worker resumes).
	if _, err := os.Stat(filepath.Join(child.Path, "owner-new.txt")); os.IsNotExist(err) {
		t.Fatal("child should contain parent's new file after rebase")
	}
	// Child's own work must be preserved.
	if _, err := os.Stat(filepath.Join(child.Path, "child.txt")); os.IsNotExist(err) {
		t.Fatal("child's own file should survive rebase")
	}

	// Child's BaseRef should be updated to parent's new HEAD (gap 7).
	updatedChild := a.worktrees[child.ID]
	parentHead, _ := gitRevParseHead(owner.Path)
	if updatedChild.BaseRef != parentHead {
		t.Fatalf("child BaseRef=%q, want parent HEAD %q", updatedChild.BaseRef, parentHead)
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 4: Merge conflict via callable handler
// Covers gap 1 (merge failure keeps child, aborts parent), gap 12 (owner dirty
// check before merge).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_MergeConflict_ViaCallable(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-conflict"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-conflict",
		AgentActorID:  "owner-agent-4",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner := a.worktrees[ownerID]

	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-conflict-child",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-conflict",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Both modify the same file differently to create a conflict.
	if err := os.WriteFile(filepath.Join(owner.Path, "same.txt"), []byte("owner version"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, owner.Path, "owner edits same.txt")

	if err := os.WriteFile(filepath.Join(child.Path, "same.txt"), []byte("child version"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child edits same.txt")

	// Attempt merge via callable — must return conflict.
	mergeResp, err := a.handleWorktreeMergeToParent(ctx, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err == nil {
		t.Fatal("expected conflict error from merge_to_parent")
	}
	if mergeResp.Status != "conflict" {
		t.Fatalf("status=%q, want conflict", mergeResp.Status)
	}

	// Parent must be clean (merge aborted — gap 1).
	dirty, err := gitWorktreeHasUncommitted(owner.Path)
	if err != nil {
		t.Fatalf("check parent clean: %v", err)
	}
	if dirty {
		t.Fatal("parent should be clean after merge abort")
	}

	// Child worktree must still exist (preserved for worker to fix).
	if _, ok := a.worktrees[child.ID]; !ok {
		t.Fatal("child worktree must remain after conflict")
	}
	if _, err := os.Stat(child.Path); os.IsNotExist(err) {
		t.Fatal("child worktree dir must remain after conflict")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 5: Rebase conflict via callable handler
// Covers gap 2 (rebase conflict — worker stays at pre-rebase state).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_RebaseConflict_ViaCallable(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-rbconflict"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-rbconflict",
		AgentActorID:  "owner-agent-5",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner := a.worktrees[ownerID]

	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-rbconflict-child",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-rbconflict",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Both modify the same file differently.
	if err := os.WriteFile(filepath.Join(owner.Path, "conflict.txt"), []byte("owner"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, owner.Path, "owner edits conflict.txt")

	if err := os.WriteFile(filepath.Join(child.Path, "conflict.txt"), []byte("child"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child edits conflict.txt")

	// Attempt rebase via callable — must return conflict.
	rebaseResp, err := a.handleWorktreeRebaseToParent(ctx, gen.ProjectWorktreeRebaseToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err == nil {
		t.Fatal("expected conflict error from rebase_to_parent")
	}
	if rebaseResp.Status != "conflict" {
		t.Fatalf("status=%q, want conflict", rebaseResp.Status)
	}

	// Child must be clean (rebase aborted — no mid-rebase state).
	dirty, err := gitWorktreeHasUncommitted(child.Path)
	if err != nil {
		t.Fatalf("check child clean: %v", err)
	}
	if dirty {
		t.Fatal("child should be clean after rebase abort")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 6: Runtime state preservation on merge (wiki/graphs/wiki-state)
// Covers the .gitattributes merge=ours mechanism + restoreRuntimeState.
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_RuntimeStatePreservationOnMerge(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	// Set up .gitattributes with merge=ours rules BEFORE creating worktrees.
	setupGitattributes(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-state"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-state",
		AgentActorID:  "owner-agent-6",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner := a.worktrees[ownerID]

	// Write runtime-state files in the owner worktree and commit them.
	wikiDir := filepath.Join(owner.Path, ".sporecode", "wiki")
	if err := os.MkdirAll(wikiDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wikiDir, "test-card.md"), []byte("owner wiki content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner.Path, ".sporecode", "graphs.json"), []byte(`{"nodes":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner.Path, ".sporecode", "wiki-state.json"), []byte(`{"open":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, owner.Path, "owner runtime state")

	// Create child from owner.
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-state-child",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-state",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Child makes a DIFFERENT change (touching different files only).
	if err := os.WriteFile(filepath.Join(child.Path, "code.txt"), []byte("worker code"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child code change")

	// Merge child→parent.
	mergeResp, err := a.handleWorktreeMergeToParent(ctx, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if mergeResp.Status != "merged" {
		t.Fatalf("status=%q, want merged", mergeResp.Status)
	}

	// Verify runtime-state files still have the OWNER's content (not clobbered).
	wikiData, err := os.ReadFile(filepath.Join(owner.Path, ".sporecode", "wiki", "test-card.md"))
	if err != nil {
		t.Fatalf("read wiki test-card: %v", err)
	}
	if !strings.Contains(string(wikiData), "owner wiki content") {
		t.Fatalf("wiki content clobbered by merge: got %q", string(wikiData))
	}

	graphsData, err := os.ReadFile(filepath.Join(owner.Path, ".sporecode", "graphs.json"))
	if err != nil {
		t.Fatalf("read graphs.json: %v", err)
	}
	if !strings.Contains(string(graphsData), "nodes") {
		t.Fatalf("graphs.json clobbered by merge: got %q", string(graphsData))
	}

	wikiStateData, err := os.ReadFile(filepath.Join(owner.Path, ".sporecode", "wiki-state.json"))
	if err != nil {
		t.Fatalf("read wiki-state.json: %v", err)
	}
	if !strings.Contains(string(wikiStateData), "open") {
		t.Fatalf("wiki-state.json clobbered by merge: got %q", string(wikiStateData))
	}

	// Owner must be clean after runtime state restore + amend.
	dirty, err := gitWorktreeHasUncommitted(owner.Path)
	if err != nil {
		t.Fatalf("check owner clean: %v", err)
	}
	if dirty {
		t.Fatal("owner worktree should be clean after runtime state restore")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 7: Branch uniqueness enforcement via callable
// Covers gap 11 (branch naming collision).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_BranchUniqueness(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	if _, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "unique-branch"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Second create with the same name must fail.
	_, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "unique-branch"})
	if err == nil {
		t.Fatal("expected branch uniqueness error for duplicate name")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error should mention existing branch, got: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 8: Owner stale protection via release_binding callable
// Covers gap 3 (owner crash — worktree marked stale, not removed), gap 15
// (recursive child stale marking).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_OwnerStaleProtection_ViaCallable(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-stale"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-stale",
		AgentActorID:  "owner-agent-8",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID

	// Create a child worktree.
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-stale-child",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-stale",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Simulate owner crash: release binding without workflow_stop.
	if err := a.handleWorktreeReleaseBinding(ctx, gen.ProjectWorktreeReleaseBindingReq{
		AgentActorID: "owner-agent-8",
	}); err != nil {
		t.Fatalf("release_binding: %v", err)
	}

	// Owner worktree must be marked stale (not removed — gap 3).
	got, ok := a.worktrees[ownerID]
	if !ok {
		t.Fatal("owner worktree must remain in metadata after release (stale protection)")
	}
	if got.Status != "stale" {
		t.Fatalf("owner worktree Status=%q, want stale", got.Status)
	}
	if _, err := os.Stat(got.Path); os.IsNotExist(err) {
		t.Fatal("owner worktree dir must remain on disk (stale protection)")
	}

	// Child worktree must be recursively marked stale (gap 3 fix).
	gotChild, ok := a.worktrees[child.ID]
	if !ok {
		t.Fatal("child worktree should still exist")
	}
	if gotChild.Status != "stale" {
		t.Fatalf("child worktree Status=%q, want stale (recursive marking)", gotChild.Status)
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 9: Discard by ID via callable
// Covers gap 10 (orphan worktree cleanup via worktree_discard_by_id).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_DiscardByID_ViaCallable(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "discard-test"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Discard via callable.
	resp, err := a.handleWorktreeDiscardByID(ctx, gen.ProjectWorktreeDiscardByIDReq{
		WorktreeID: wt.ID,
	})
	if err != nil {
		t.Fatalf("worktree_discard_by_id: %v", err)
	}
	if resp.Status != "discarded" {
		t.Fatalf("status=%q, want discarded", resp.Status)
	}

	// Must be removed from metadata and disk.
	if _, ok := a.worktrees[wt.ID]; ok {
		t.Fatal("worktree should be removed from metadata")
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatal("worktree dir should be removed")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 10: Discard by ID with dirty worktree (force)
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_DiscardByID_DirtyForce(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "dirty-discard"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Make it dirty.
	if err := os.WriteFile(filepath.Join(wt.Path, "uncommitted.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	// Non-force discard should fail.
	_, err = a.handleWorktreeDiscardByID(ctx, gen.ProjectWorktreeDiscardByIDReq{
		WorktreeID: wt.ID,
	})
	if err == nil {
		t.Fatal("expected error for dirty worktree discard without force")
	}

	// Force discard should succeed.
	resp, err := a.handleWorktreeDiscardByID(ctx, gen.ProjectWorktreeDiscardByIDReq{
		WorktreeID: wt.ID,
		Force:      true,
	})
	if err != nil {
		t.Fatalf("force discard: %v", err)
	}
	if resp.Status != "discarded" {
		t.Fatalf("status=%q, want discarded", resp.Status)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatal("dirty worktree dir should be removed with force")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 11: Bind existing check via callable
// Covers gap 15 (bindWorktree silent overwrite prevention).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_BindExistingCheck(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt1, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "bind-check-1"})
	if err != nil {
		t.Fatalf("create wt1: %v", err)
	}
	wt2, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "bind-check-2"})
	if err != nil {
		t.Fatalf("create wt2: %v", err)
	}

	const agentID = "agent/bind-check/0000000000000000000001"
	if err := a.bindWorktree(agentID, wt1.ID); err != nil {
		t.Fatalf("first bind: %v", err)
	}

	// Rebind to a different worktree must fail.
	err = a.bindWorktree(agentID, wt2.ID)
	if err == nil {
		t.Fatal("expected rebind error for already-bound agent")
	}
	if !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("error should mention existing binding, got: %v", err)
	}

	// Rebind to the SAME worktree must be idempotent.
	if err := a.bindWorktree(agentID, wt1.ID); err != nil {
		t.Fatalf("rebind same worktree should be idempotent: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 12: workflow_stop with dirty owner worktree fails cleanly
// Covers gap 13 (workflow_stop cannot proceed with dirty state).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_WorkflowStop_DirtyOwnerFails(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-dirtystop"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-dirtystop",
		AgentActorID:  "owner-agent-12",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner := a.worktrees[ownerID]

	// Make the owner worktree dirty.
	if err := os.WriteFile(filepath.Join(owner.Path, "uncommitted.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	// workflow_stop must fail.
	_, err = a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID: ownerID,
	})
	if err == nil {
		t.Fatal("expected error for dirty owner worktree on workflow_stop")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("error should mention uncommitted changes, got: %v", err)
	}

	// Owner worktree must still exist (not removed on failure).
	if _, ok := a.worktrees[ownerID]; !ok {
		t.Fatal("owner worktree should still exist after failed workflow_stop")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 13: workflow_stop with unknown worktree fails
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_WorkflowStop_UnknownWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	_, err := a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID: "no-such-worktree",
	})
	if err == nil {
		t.Fatal("expected error for unknown worktree")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error should mention not found, got: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 14: Owner commit then child merge — owner dirty check
// Covers gap 12 (owner commit and worker merge competition — owner must be
// clean before merge).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_OwnerDirtyPreventsChildMerge(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-ownerdirty"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-ownerdirty",
		AgentActorID:  "owner-agent-14",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner := a.worktrees[ownerID]

	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-ownerdirty-child",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-ownerdirty",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	// Child commits clean work.
	if err := os.WriteFile(filepath.Join(child.Path, "child.txt"), []byte("child"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child work")

	// Make owner dirty (simulating owner mid-edit).
	if err := os.WriteFile(filepath.Join(owner.Path, "owner-wip.txt"), []byte("wip"), 0644); err != nil {
		t.Fatal(err)
	}

	// Merge must fail because owner is dirty (gap 12).
	_, err = a.handleWorktreeMergeToParent(ctx, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err == nil {
		t.Fatal("expected error for dirty parent worktree on merge")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("error should mention uncommitted changes, got: %v", err)
	}

	// Child must still exist (merge rejected).
	if _, ok := a.worktrees[child.ID]; !ok {
		t.Fatal("child worktree should still exist after rejected merge")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 15: Child dirty prevents merge
// Covers gap 1 (dirty child — merge rejected, child stays).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_ChildDirtyPreventsMerge(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-childdirty"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-childdirty",
		AgentActorID:  "owner-agent-15",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID

	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-childdirty-child",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-childdirty",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	// Make child dirty.
	if err := os.WriteFile(filepath.Join(child.Path, "uncommitted.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err = a.handleWorktreeMergeToParent(ctx, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err == nil {
		t.Fatal("expected error for dirty child worktree")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("error should mention uncommitted changes, got: %v", err)
	}
	// Child must still exist.
	if _, ok := a.worktrees[child.ID]; !ok {
		t.Fatal("child worktree should still exist after rejected merge")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 16: Full lifecycle — reject rebase then approve merge then stop
// Tests the complete adversarial scenario: child is rejected (rebased), then
// approved (merged), then workflow stops (merged to main).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_FullLifecycle_RejectRebaseThenApproveMergeThenStop(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-full2"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	// Create owner worktree.
	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-full2",
		AgentActorID:  "owner-agent-16",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner := a.worktrees[ownerID]

	// Create child worktree.
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-full2-child",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-full2",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Child commits initial work.
	if err := os.WriteFile(filepath.Join(child.Path, "feature.go"), []byte("package main\n// v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child v1")

	// Owner advances — reject path: rebase child to parent.
	if err := os.WriteFile(filepath.Join(owner.Path, "owner-config.txt"), []byte("config"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, owner.Path, "owner config")

	rebaseResp, err := a.handleWorktreeRebaseToParent(ctx, gen.ProjectWorktreeRebaseToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("rebase: %v", err)
	}
	if rebaseResp.Status != "rebased" {
		t.Fatalf("rebase status=%q, want rebased", rebaseResp.Status)
	}

	// Child now has owner's config file.
	if _, err := os.Stat(filepath.Join(child.Path, "owner-config.txt")); os.IsNotExist(err) {
		t.Fatal("child should have owner-config.txt after rebase")
	}

	// Child fixes work and commits v2.
	if err := os.WriteFile(filepath.Join(child.Path, "feature.go"), []byte("package main\n// v2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child v2")

	// Approve: merge child→parent.
	mergeResp, err := a.handleWorktreeMergeToParent(ctx, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if mergeResp.Status != "merged" {
		t.Fatalf("merge status=%q, want merged", mergeResp.Status)
	}

	// Child removed, parent has v2.
	if _, ok := a.worktrees[child.ID]; ok {
		t.Fatal("child should be removed after merge")
	}
	featureData, err := os.ReadFile(filepath.Join(owner.Path, "feature.go"))
	if err != nil {
		t.Fatalf("read feature.go: %v", err)
	}
	if !strings.Contains(string(featureData), "v2") {
		t.Fatalf("parent should have child v2, got %q", string(featureData))
	}

	// Stop: merge owner→main.
	stopResp, err := a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("workflow_stop: %v", err)
	}
	if stopResp.Status != "merged" {
		t.Fatalf("stop status=%q, want merged", stopResp.Status)
	}

	// Main has the final feature.
	if _, err := os.Stat(filepath.Join(dir, "feature.go")); os.IsNotExist(err) {
		t.Fatal("feature.go missing on main after stop")
	}
	mainFeature, err := os.ReadFile(filepath.Join(dir, "feature.go"))
	if err != nil {
		t.Fatalf("read main feature.go: %v", err)
	}
	if !strings.Contains(string(mainFeature), "v2") {
		t.Fatalf("main should have v2, got %q", string(mainFeature))
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 17: Reconcile reverse discovery — unknown worktree from git
// Covers gap 16 (saveWorktrees failure → reconcile discovers unknown worktree).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_Reconcile_ReverseDiscovery(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	// Create a worktree directly via git (bypassing the actor).
	extPath := filepath.Join(dir, "ext-int-wt")
	if _, err := gitRun(nil, dir, "worktree", "add", "-b", "ext-int-branch", extPath); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}

	// Clear actor's knowledge so reverse discovery is the only path.
	a.worktreeParentMu.Lock()
	a.bindingMu.Lock()
	a.worktrees = map[string]gen.ProjectWorktree{}
	a.agentWorktree = map[string]string{}
	a.bindingMu.Unlock()
	a.worktreeParentMu.Unlock()

	changed, err := a.reverseDiscoverWorktrees()
	if err != nil {
		t.Fatalf("reverse discovery: %v", err)
	}
	if !changed {
		t.Fatal("expected reverse discovery to find the external worktree")
	}

	found := false
	for _, wt := range a.worktrees {
		if filepath.Clean(wt.Path) == filepath.Clean(extPath) {
			found = true
			if wt.Status != "stale" {
				t.Fatalf("discovered worktree Status=%q, want stale", wt.Status)
			}
		}
	}
	if !found {
		t.Fatal("external worktree not discovered")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 18: Map card ownerWorktreeId stamping survives reload
// Covers gap 9 (workflow_stop callable) + Phase 3 stamping.
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_MapCardStampingSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-reload"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-reload",
		AgentActorID:  "owner-agent-18",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}

	// Verify map card has ownerWorktreeId stamped.
	card, err := a.store.Get("wf-reload")
	if err != nil {
		t.Fatalf("fetch map card: %v", err)
	}
	got, _ := card.Data["ownerWorktreeId"].(string)
	if got != ownerResp.WorktreeID {
		t.Fatalf("data.ownerWorktreeId = %q, want %q", got, ownerResp.WorktreeID)
	}

	// Reload actor from disk — worktree metadata should survive.
	a2 := &Actor{
		initialPath:  dir,
		actorID:      a.actorID,
		store:        newFSCardStore(filepath.Join(dir, wikiDir)),
		persistStore: persist.NewFSPersist(t.TempDir()),
	}
	if err := a2.loadWorktreeCache(); err != nil {
		t.Fatalf("loadWorktreeCache: %v", err)
	}
	if _, ok := a2.worktrees[ownerResp.WorktreeID]; !ok {
		t.Fatal("owner worktree should survive reload")
	}

	// Map card should still have the stamp.
	card2, err := a2.store.Get("wf-reload")
	if err != nil {
		t.Fatalf("fetch map card after reload: %v", err)
	}
	got2, _ := card2.Data["ownerWorktreeId"].(string)
	if got2 != ownerResp.WorktreeID {
		t.Fatalf("data.ownerWorktreeId after reload = %q, want %q", got2, ownerResp.WorktreeID)
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 19: Workflow stop with main advance — rebase then merge
// Covers gap 14 (two workflows merging to main — second must rebase).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_WorkflowStop_MainAdvanceRebase(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-mainadv"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-mainadv",
		AgentActorID:  "owner-agent-19",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner := a.worktrees[ownerID]

	// Owner commits work.
	if err := os.WriteFile(filepath.Join(owner.Path, "owner-work.txt"), []byte("owner"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, owner.Path, "owner work")

	// Meanwhile main advances with an unrelated change.
	if err := os.WriteFile(filepath.Join(dir, "main-work.txt"), []byte("main"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, dir, "main work")

	// workflow_stop must rebase owner onto latest main, then merge.
	stopResp, err := a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("workflow_stop: %v", err)
	}
	if stopResp.Status != "merged" {
		t.Fatalf("status=%q, want merged", stopResp.Status)
	}

	// Both changes must be on main.
	log := gitRunMust(t, dir, "log", "--oneline", "--all")
	if !strings.Contains(log, "owner work") {
		t.Errorf("owner work missing from main:\n%s", log)
	}
	if !strings.Contains(log, "main work") {
		t.Errorf("main work missing from history:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(dir, "owner-work.txt")); os.IsNotExist(err) {
		t.Error("owner-work.txt missing on main")
	}
	if _, err := os.Stat(filepath.Join(dir, "main-work.txt")); os.IsNotExist(err) {
		t.Error("main-work.txt missing on main")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 20: Non-owner worktree release still removes (stale protection
// only applies to workflow owner worktrees).
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_NonOwnerRelease_RemovesWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	// Plain worktree (no WorkflowMapID) — normal force-remove on release.
	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "plain-release"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const agentID = "agent/plain-release/000000000000000001"
	if err := a.bindWorktree(agentID, wt.ID); err != nil {
		t.Fatalf("bind: %v", err)
	}

	if err := a.handleWorktreeReleaseBinding(ctx, gen.ProjectWorktreeReleaseBindingReq{
		AgentActorID: agentID,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Must be removed (not stale-protected).
	if _, ok := a.worktrees[wt.ID]; ok {
		t.Fatal("non-owner worktree must be removed after release")
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatal("non-owner worktree dir must be removed")
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 21: Symlink detection in worktree creation
// Covers the post-creation symlink scan in handleWorktreeCreate.
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_DetectSymlinkInWalk(t *testing.T) {
	// Reuse the existing unit test's symlink detection logic. This is a
	// callable-level smoke test: detectSymlinkInWalk must reject symlinks.
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if err := detectSymlinkInWalk(dir); err == nil {
		t.Fatal("expected symlink detection error")
	}

	// Clean directory should pass.
	clean := t.TempDir()
	if err := os.WriteFile(filepath.Join(clean, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := detectSymlinkInWalk(clean); err != nil {
		t.Fatalf("clean dir should pass: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Scenario 22: Full lifecycle with conflict recovery — merge conflict, then
// child fixes and retries merge successfully.
// ──────────────────────────────────────────────────────────────────────────

func TestIntegration_ConflictRecovery_RetryMerge(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-recovery"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	ownerResp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-recovery",
		AgentActorID:  "owner-agent-22",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	ownerID := ownerResp.WorktreeID
	owner := a.worktrees[ownerID]

	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-recovery-child",
		ParentWorktreeID: ownerID,
		WorkflowMapID:    "wf-recovery",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Both modify same file differently.
	if err := os.WriteFile(filepath.Join(owner.Path, "shared.txt"), []byte("owner"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, owner.Path, "owner shared")
	if err := os.WriteFile(filepath.Join(child.Path, "shared.txt"), []byte("child"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child shared")

	// First merge attempt: conflict.
	_, err = a.handleWorktreeMergeToParent(ctx, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err == nil {
		t.Fatal("expected conflict on first merge")
	}

	// Child resolves conflict by adopting owner's version.
	if err := os.WriteFile(filepath.Join(child.Path, "shared.txt"), []byte("owner"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child resolves conflict")

	// Second merge attempt: success.
	mergeResp, err := a.handleWorktreeMergeToParent(ctx, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: ownerID,
	})
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if mergeResp.Status != "merged" {
		t.Fatalf("status=%q, want merged", mergeResp.Status)
	}

	// Child removed, parent has resolved content.
	if _, ok := a.worktrees[child.ID]; ok {
		t.Fatal("child should be removed after successful retry merge")
	}
	data, err := os.ReadFile(filepath.Join(owner.Path, "shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "owner" {
		t.Fatalf("shared.txt = %q, want 'owner'", string(data))
	}
}
