package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Scenario: the workflow owner worktree (deterministic name wf-<mapID>) was
// physically deleted, but references remain in the project. Re-activating the
// workflow via workflow_create_worktree must clean the dangling references and
// proceed instead of failing with "branch already exists".

// TestWorkflowCreateWorktree_CleansDanglingMetadataAndBranch removes the
// worktree via git (dir gone, branch ref + metadata entry remain) and verifies
// re-activation succeeds with a fresh worktree and a re-bound agent.
func TestWorkflowCreateWorktree_CleansDanglingMetadataAndBranch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-dangle"}); err != nil {
		t.Fatalf("create map: %v", err)
	}
	resp1, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-dangle",
		AgentActorID:  "owner-agent-d1",
	})
	if err != nil {
		t.Fatalf("first workflow_create_worktree: %v", err)
	}

	// Physically delete the worktree: git removes the dir and the worktree
	// registration but keeps the branch ref. Metadata entry remains.
	oldWt1 := a.worktrees[resp1.WorktreeID]
	gitRunMust(t, dir, "worktree", "remove", "--force", oldWt1.Path)
	if _, present := a.worktrees[resp1.WorktreeID]; !present {
		t.Fatal("precondition: metadata entry should still exist")
	}
	if !branchExists(t, dir, "wf-wf-dangle") {
		t.Fatal("precondition: branch ref should still exist after git worktree remove")
	}

	resp2, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-dangle",
		AgentActorID:  "owner-agent-d1",
	})
	if err != nil {
		t.Fatalf("re-activation should clean dangling refs and proceed, got: %v", err)
	}
	if resp2.WorktreeID == resp1.WorktreeID {
		t.Fatalf("expected a fresh worktree, got same ID %q", resp2.WorktreeID)
	}
	wt2, ok := a.worktrees[resp2.WorktreeID]
	if !ok || wt2.Status != "active" {
		t.Fatalf("new worktree must be active, got %+v", wt2)
	}
	if _, still := a.worktrees[resp1.WorktreeID]; still {
		t.Error("dangling metadata entry was not removed")
	}
	if bound := a.agentWorktree["owner-agent-d1"]; bound != resp2.WorktreeID {
		t.Errorf("agent binding = %q, want new worktree %q", bound, resp2.WorktreeID)
	}
	// The fresh branch exists (recreated), and no second stale entry lingers.
	count := 0
	for _, wt := range a.worktrees {
		if wt.Name == "wf-wf-dangle" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 metadata entry for wf-wf-dangle, got %d", count)
	}
}

// TestWorkflowCreateWorktree_CleansDanglingBranchOnly clears the metadata but
// leaves the branch ref; re-activation must drop the ref so `git worktree add
// -b` succeeds.
func TestWorkflowCreateWorktree_CleansDanglingBranchOnly(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	resp1, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-dangle2",
		AgentActorID:  "owner-agent-d2",
	})
	if err != nil {
		t.Fatalf("first workflow_create_worktree: %v", err)
	}

	// Simulate: metadata cleaned but branch ref left behind.
	oldWt2 := a.worktrees[resp1.WorktreeID]
	gitRunMust(t, dir, "worktree", "remove", "--force", oldWt2.Path)
	a.worktreeParentMu.Lock()
	a.bindingMu.Lock()
	delete(a.worktrees, resp1.WorktreeID)
	delete(a.agentWorktree, "owner-agent-d2")
	a.bindingMu.Unlock()
	a.worktreeParentMu.Unlock()
	if !branchExists(t, dir, "wf-wf-dangle2") {
		t.Fatal("precondition: branch ref should still exist")
	}

	resp2, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-dangle2",
		AgentActorID:  "owner-agent-d2",
	})
	if err != nil {
		t.Fatalf("re-activation should drop the dangling branch ref and proceed, got: %v", err)
	}
	if _, ok := a.worktrees[resp2.WorktreeID]; !ok {
		t.Fatal("new worktree metadata missing")
	}
}

// TestWorkflowCreateWorktree_CardAnchoredReuseRebindsLostAgent pins the
// card-anchored attach: with the stamp live and the worktree on disk, a lost
// agentWorktree entry (e.g. crash-restart bookkeeping) is repaired by
// re-binding instead of failing on the deterministic name.
func TestWorkflowCreateWorktree_CardAnchoredReuseRebindsLostAgent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "rebind-map"}); err != nil {
		t.Fatalf("create map: %v", err)
	}
	agent := bindKey(t, "019fa777770000000000000000000008")

	resp1, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{WorkflowMapID: "rebind-map", AgentActorID: agent})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Simulate lost agent-side bookkeeping while the card stamp survives.
	a.bindingMu.Lock()
	delete(a.agentWorktree, agent)
	a.bindingMu.Unlock()

	resp2, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{WorkflowMapID: "rebind-map", AgentActorID: agent})
	if err != nil {
		t.Fatalf("re-create must re-attach from the card stamp: %v", err)
	}
	if resp2.WorktreeID != resp1.WorktreeID {
		t.Errorf("re-attach returned new worktree %q, want reuse of %q", resp2.WorktreeID, resp1.WorktreeID)
	}
	if bound := a.agentWorktree[agent]; bound != resp1.WorktreeID {
		t.Errorf("agent binding not repaired: got %q, want %q", bound, resp1.WorktreeID)
	}
}

// TestWorkflowMergeClearsOwnerWorktreeStamp pins the merge-time lifecycle: a
// successful workflow merge removes the ownerWorktreeId stamp so the next
// workflow_start creates fresh instead of treating the merged id as dangling.
func TestWorkflowMergeClearsOwnerWorktreeStamp(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "mergeclear-map"}); err != nil {
		t.Fatalf("create map: %v", err)
	}
	agent := bindKey(t, "019fa888880000000000000000000009")

	resp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{WorkflowMapID: "mergeclear-map", AgentActorID: agent})
	if err != nil {
		t.Fatalf("workflow create: %v", err)
	}
	if got := a.mapOwnerWorktreeID("mergeclear-map"); got != resp.WorktreeID {
		t.Fatalf("precondition: stamp %q != worktree %q", got, resp.WorktreeID)
	}

	wt := a.worktrees[resp.WorktreeID]
	if err := os.WriteFile(filepath.Join(wt.Path, "merged.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, wt.Path, "add", "-A")
	gitOut(t, wt.Path, "commit", "-m", "wf work")

	if _, err := a.mergeWorktreeIntoBase(nil, resp.WorktreeID, ""); err != nil {
		t.Fatalf("merge into base: %v", err)
	}
	if got := a.mapOwnerWorktreeID("mergeclear-map"); got != "" {
		t.Errorf("stamp must be cleared after merge, got %q", got)
	}
	// The cleared stamp must not linger as an empty key in the raw frontmatter.
	card, err := a.store.Get("mergeclear-map")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(card.Raw, "ownerWorktreeId") {
		t.Errorf("ownerWorktreeId residue in card raw:\n%s", card.Raw)
	}
}

// TestWorkflowCreateWorktree_StaleWorktreeReactivatedOnRetry reproduces the
// production deadlock: workflow_stop parked the owner worktree as stale (via
// releaseWorktreeBinding) and the card stamp was cleared (partial merge
// cleanup). A fresh workflow_start with a new agent must re-adopt the stale
// worktree instead of failing on the deterministic branch name collision.
func TestWorkflowCreateWorktree_StaleWorktreeReactivatedOnRetry(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "stale-retry-map"}); err != nil {
		t.Fatalf("create map: %v", err)
	}
	agent1 := bindKey(t, "019fa99999000000000000000000000a")

	resp1, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{WorkflowMapID: "stale-retry-map", AgentActorID: agent1})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Simulate the post-stop state: worktree parked stale, stamp cleared,
	// agent binding released (owner agent died).
	a.worktreeParentMu.Lock()
	a.bindingMu.Lock()
	wt := a.worktrees[resp1.WorktreeID]
	wt.Status = "stale"
	a.worktrees[resp1.WorktreeID] = wt
	delete(a.agentWorktree, agent1)
	a.bindingMu.Unlock()
	a.worktreeParentMu.Unlock()
	a.clearMapOwnerWorktree("stale-retry-map")

	if got := a.mapOwnerWorktreeID("stale-retry-map"); got != "" {
		t.Fatalf("precondition: stamp should be cleared, got %q", got)
	}

	// New agent, same map — must re-adopt the stale worktree.
	agent2 := bindKey(t, "019faaaaaa000000000000000000000b")
	resp2, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{WorkflowMapID: "stale-retry-map", AgentActorID: agent2})
	if err != nil {
		t.Fatalf("retry must re-adopt stale worktree, got: %v", err)
	}
	if resp2.WorktreeID != resp1.WorktreeID {
		t.Errorf("retry returned new worktree %q, want reuse of %q", resp2.WorktreeID, resp1.WorktreeID)
	}
	a.worktreeParentMu.RLock()
	reactivated := a.worktrees[resp1.WorktreeID]
	a.worktreeParentMu.RUnlock()
	if reactivated.Status != "active" {
		t.Errorf("stale worktree must be reactivated to active, got %q", reactivated.Status)
	}
	if a.agentWorktree[agent2] != resp1.WorktreeID {
		t.Errorf("new agent not bound to reactivated worktree")
	}
	if got := a.mapOwnerWorktreeID("stale-retry-map"); got != resp1.WorktreeID {
		t.Errorf("stamp not healed after re-adopt, got %q", got)
	}
}

// TestWorkflowCreateWorktree_LiveWorktreeReusedNotDuplicated verifies that a
// second workflow_create_worktree call for the same map+agent reuses the
// existing live worktree (idempotent retry) instead of failing on the
// deterministic name collision. The worktree entry stays singular and active.
func TestWorkflowCreateWorktree_LiveWorktreeReusedNotDuplicated(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	resp1, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-live",
		AgentActorID:  "owner-agent-d3",
	})
	if err != nil {
		t.Fatalf("first workflow_create_worktree: %v", err)
	}

	resp2, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-live",
		AgentActorID:  "owner-agent-d3",
	})
	if err != nil {
		t.Fatalf("second workflow_create_worktree should reuse, got: %v", err)
	}
	if resp2.WorktreeID != resp1.WorktreeID {
		t.Errorf("second call returned new worktree %q, want reuse of %q", resp2.WorktreeID, resp1.WorktreeID)
	}
	found := 0
	for _, wt := range a.worktrees {
		if wt.Name == "wf-wf-live" && wt.Status == "active" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 active worktree entry, found %d", found)
	}
}
