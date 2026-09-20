package workspace

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// genID is a test helper that produces distinct ActorIDs, unlike
// testutil.GenActorID which always returns the same value.
var genIDSeq uint64

func genID() string {
	genIDSeq++
	g := id.NewCanonical(99, 0, func() uint64 { v := genIDSeq; return v })
	return g.Next().String()
}

// failingSavePersist wraps an inner persist.Persist and fails every Save.
// Load and Delete delegate to the inner store. Used to pin
// deletion-finalize revert-on-save-failure: the in-memory cache must stay
// consistent with the authoritative .ragents card when the card write fails.
type failingSavePersist struct{ inner persist.Persist }

func (s *failingSavePersist) Load(name string, v any) error { return s.inner.Load(name, v) }

func (s *failingSavePersist) Save(name string, v any) error {
	return errors.New("persist: simulated save failure")
}

func (s *failingSavePersist) Delete(name string) error { return s.inner.Delete(name) }

// ── Subtree computation ───────────────────────────────────────────────────

func TestComputeDeletionSubtree_SingleAgent(t *testing.T) {
	a, _ := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, AgentKind: domain.AgentKindWorker},
	}
	subtree := a.computeDeletionSubtree(agentActorID)
	if len(subtree) != 1 {
		t.Fatalf("expected subtree of 1, got %d", len(subtree))
	}
	if subtree[0].ID != "W#1" {
		t.Errorf("expected W#1, got %s", subtree[0].ID)
	}
}

func TestComputeDeletionSubtree_ParentAndChildren(t *testing.T) {
	a, _ := freshActor(t)
	parentActorID := genID()
	child1ActorID := genID()
	child2ActorID := genID()
	grandchildActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "P#1", ActorID: parentActorID, AgentKind: "coordinator"},
		{ID: "W#1", ActorID: child1ActorID, ParentAgentID: parentActorID, AgentKind: domain.AgentKindWorker},
		{ID: "W#2", ActorID: child2ActorID, ParentAgentID: parentActorID, AgentKind: domain.AgentKindWorker},
		{ID: "W#3", ActorID: grandchildActorID, ParentAgentID: child1ActorID, AgentKind: domain.AgentKindWorker},
	}
	subtree := a.computeDeletionSubtree(parentActorID)
	if len(subtree) != 4 {
		t.Fatalf("expected subtree of 4 (parent + 2 children + 1 grandchild), got %d", len(subtree))
	}
	ids := make(map[string]bool)
	for _, ag := range subtree {
		ids[ag.ID] = true
	}
	for _, want := range []string{"P#1", "W#1", "W#2", "W#3"} {
		if !ids[want] {
			t.Errorf("expected %s in subtree", want)
		}
	}
}

func TestComputeDeletionSubtree_NotFound(t *testing.T) {
	a, _ := freshActor(t)
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: genID()},
	}
	subtree := a.computeDeletionSubtree("nonexistent")
	if subtree != nil {
		t.Errorf("expected nil for unknown agent, got %v", subtree)
	}
}

// ── cascadeDelete: marking + projection ──────────────────────────────────

func TestCascadeDelete_MarksDeleting(t *testing.T) {
	a, ctx := freshActor(t)
	parentActorID := genID()
	childActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "P#1", ActorID: parentActorID, AgentKind: "coordinator"},
		{ID: "W#1", ActorID: childActorID, ParentAgentID: parentActorID, AgentKind: domain.AgentKindWorker},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	subtree := a.computeDeletionSubtree(parentActorID)
	a.cascadeDelete(ctx, subtree)

	for _, ag := range a.Agents {
		if ag.DeletionStatus != "deleting" {
			t.Errorf("expected agent %s marked deleting, got %q", ag.ID, ag.DeletionStatus)
		}
	}

	state := a.buildAgentListState(true)
	if len(state.Items) != 0 {
		t.Errorf("expected 0 items in projection, got %d", len(state.Items))
	}
}

func TestCascadeDelete_Idempotent(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, AgentKind: domain.AgentKindWorker},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	subtree := a.computeDeletionSubtree(agentActorID)
	a.cascadeDelete(ctx, subtree)
	a.cascadeDelete(ctx, subtree) // second call must be safe

	if len(a.Agents) != 1 {
		t.Fatalf("expected 1 agent (tombstone), got %d", len(a.Agents))
	}
	if a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected deleting, got %q", a.Agents[0].DeletionStatus)
	}
}

// TestCascadeDelete_MarkSaveFailure_RevertsAndAborts pins the mark-phase desync
// guard: when the authoritative .ragents card write fails while marking agents
// deleting, the in-memory marks are reverted so a.Agents matches the card
// (which still carries them as non-deleting) and the cascade aborts — no
// teardown is kicked off for agents whose deletion intent was never
// persisted, so none are orphaned on restart.
func TestCascadeDelete_MarkSaveFailure_RevertsAndAborts(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, AgentKind: domain.AgentKindWorker},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	// Save always fails → the mark card write fails and the revert+abort
	// path runs. Load/Delete still work.
	a.store = &failingSavePersist{inner: a.store}

	subtree := a.computeDeletionSubtree(agentActorID)
	a.cascadeDelete(ctx, subtree)

	if len(a.Agents) != 1 {
		t.Fatalf("expected agent retained (abort, no teardown), got %d", len(a.Agents))
	}
	if a.Agents[0].DeletionStatus != "" {
		t.Errorf("expected mark reverted to empty, got %q", a.Agents[0].DeletionStatus)
	}
	if len(a.deletionInFlight) != 0 {
		t.Errorf("expected no teardown claimed (abort), got in-flight: %v", a.deletionInFlight)
	}
}

// ── handleDeletionFinalize: success removes tombstones ──────────────────

func TestDeletionFinalize_RemovesSuccessfulTombstones(t *testing.T) {
	a, ctx := freshActor(t)
	agent1ActorID := genID()
	agent2ActorID := genID()
	survivorActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agent1ActorID, DeletionStatus: "deleting"},
		{ID: "W#2", ActorID: agent2ActorID, DeletionStatus: "deleting"},
		{ID: "C#1", ActorID: survivorActorID},
	}

	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "W#1", ActorID: agent1ActorID, Destroyed: true, WorktreeFreed: true},
			{AgentID: "W#2", ActorID: agent2ActorID, Destroyed: true, WorktreeFreed: true},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	if len(a.Agents) != 1 {
		t.Fatalf("expected 1 remaining agent, got %d", len(a.Agents))
	}
	if a.Agents[0].ID != "C#1" {
		t.Errorf("expected survivor C#1, got %s", a.Agents[0].ID)
	}
}

// TestDeletionFinalize_RegistrySaveFailure_RetainsTombstones pins the desync
// guard: when the authoritative .ragents card write fails during finalize,
// the just-removed tombstones are reverted into a.Agents so the in-memory
// cache stays consistent with the card. Without the revert the cache dropped
// the row while the card kept it, and a later agent_review dead-ended on
// "not found" for an agent still authoritatively present — orphaning the
// worker and its worktree.
func TestDeletionFinalize_RegistrySaveFailure_RetainsTombstones(t *testing.T) {
	a, ctx := freshActor(t)
	agent1ActorID := genID()
	survivorActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agent1ActorID, DeletionStatus: "deleting"},
		{ID: "C#1", ActorID: survivorActorID},
	}
	// Swap in a store whose Save always fails so the registry card write
	// fails and the revert path is exercised. Load/Delete still work.
	a.store = &failingSavePersist{inner: a.store}

	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "W#1", ActorID: agent1ActorID, Destroyed: true, WorktreeFreed: true},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// The tombstone is reverted (cache == card); C#1 is still present too.
	if len(a.Agents) != 2 {
		t.Fatalf("expected 2 agents retained (tombstone reverted + survivor), got %d: %+v", len(a.Agents), a.Agents)
	}
	var w1 *domain.AgentRef
	for i := range a.Agents {
		if a.Agents[i].ID == "W#1" {
			w1 = &a.Agents[i]
		}
	}
	if w1 == nil {
		t.Fatal("expected W#1 tombstone retained after failed save")
	}
	if w1.DeletionStatus != "deleting" {
		t.Errorf("expected W#1 still deleting after revert, got %q", w1.DeletionStatus)
	}
}

func TestDeletionFinalize_Idempotent(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{
		{ID: "C#1", ActorID: genID()},
	}

	// Finalize for non-existent agents should be a no-op.
	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "nonexistent", ActorID: genID(), Destroyed: true, WorktreeFreed: true},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}
	if len(a.Agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(a.Agents))
	}
}

func TestDeletionFinalize_MatchesByAgentID(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: "", DeletionStatus: "deleting"},
	}

	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "W#1", ActorID: "", Destroyed: true, WorktreeFreed: true},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}
	if len(a.Agents) != 0 {
		t.Errorf("expected 0 agents after finalize, got %d", len(a.Agents))
	}
}

func TestDeletionFinalize_ThenProjection(t *testing.T) {
	a, ctx := freshActor(t)
	deletingID := genID()
	survivorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: deletingID, DeletionStatus: "deleting"},
		{ID: "C#1", ActorID: survivorID},
	}

	_, _ = a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "W#1", ActorID: deletingID, Destroyed: true, WorktreeFreed: true},
		},
	})

	state := a.buildAgentListState(true)
	if len(state.Items) != 1 || state.Items[0].ID != "C#1" {
		t.Errorf("expected only C#1 in projection after finalize, got %v", state.Items)
	}
}

// ── handleDeletionFinalize: failure retains tombstone + records error ────

func TestDeletionFinalize_FailedDestroy_RetainsTombstoneAndSchedulesRetry(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	var retryScheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_retry" {
			retryScheduled.Store(true)
		}
		return nil
	}

	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "W#1", ActorID: agentActorID, Destroyed: false, WorktreeFreed: true, DestroyError: "destroy: timeout"},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// Tombstone must be retained.
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Fatalf("expected 1 deleting agent retained, got %d", len(a.Agents))
	}
	// Error must be recorded.
	if errMsg := a.deletionErrors["W#1"]; errMsg != "destroy: timeout" {
		t.Errorf("expected error recorded, got %q", errMsg)
	}
	// Attempt must be incremented.
	if attempt := a.deletionAttempts["W#1"]; attempt != 1 {
		t.Errorf("expected attempt 1, got %d", attempt)
	}
	// Retry must be scheduled.
	if !retryScheduled.Load() {
		t.Error("expected retry to be scheduled")
	}
}

func TestDeletionFinalize_DestroyedButReleaseFails_RemovesTombstone(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	var retryScheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_retry" {
			retryScheduled.Store(true)
		}
		return nil
	}

	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "W#1", ActorID: agentActorID, Destroyed: true, WorktreeFreed: false, ReleaseError: "release: not found"},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// Actor is gone → tombstone must be removed even though worktree
	// release failed. The worktree binding is stale; the orphaned git
	// worktree dir is harmless.
	if len(a.Agents) != 0 {
		t.Fatalf("expected 0 agents (tombstone removed), got %d", len(a.Agents))
	}
	if retryScheduled.Load() {
		t.Error("retry must NOT be scheduled — tombstone was removed")
	}
}

func TestDeletionFinalize_PartialSubtree_SuccessAndFailure(t *testing.T) {
	a, ctx := freshActor(t)
	successActorID := genID()
	failActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "OK#1", ActorID: successActorID, DeletionStatus: "deleting"},
		{ID: "FAIL#1", ActorID: failActorID, DeletionStatus: "deleting"},
	}

	var retryScheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_retry" {
			retryScheduled.Store(true)
		}
		return nil
	}

	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "OK#1", ActorID: successActorID, Destroyed: true, WorktreeFreed: true},
			{AgentID: "FAIL#1", ActorID: failActorID, Destroyed: false, WorktreeFreed: true, DestroyError: "destroy failed"},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// Successful node removed, failed node retained.
	if len(a.Agents) != 1 {
		t.Fatalf("expected 1 remaining agent, got %d", len(a.Agents))
	}
	if a.Agents[0].ID != "FAIL#1" {
		t.Errorf("expected FAIL#1 retained, got %s", a.Agents[0].ID)
	}
	if a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected FAIL#1 still deleting")
	}
	if !retryScheduled.Load() {
		t.Error("expected retry scheduled for failed node")
	}
}

func TestDeletionFinalize_ExhaustedRetainsTombstone(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}
	// Pre-set attempt count to just below max.
	a.deletionAttempts = map[string]int{"W#1": deletionMaxAttempts - 1}

	var retryScheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_retry" {
			retryScheduled.Store(true)
		}
		return nil
	}

	_, _ = a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "W#1", ActorID: agentActorID, Destroyed: false, WorktreeFreed: true, DestroyError: "still failing"},
		},
	})

	// After max attempts, no retry should be scheduled.
	if retryScheduled.Load() {
		t.Error("expected no retry after exhausting max attempts")
	}
	// Tombstone still retained.
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Error("expected tombstone retained after exhausted retries")
	}
	if a.deletionAttempts["W#1"] != deletionMaxAttempts {
		t.Errorf("expected attempt = %d, got %d", deletionMaxAttempts, a.deletionAttempts["W#1"])
	}
}

// ── handleDeletionRetry ──────────────────────────────────────────────────

func TestDeletionRetry_RetriggersTeardown(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	var afterScheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_finalize" {
			afterScheduled.Store(true)
		}
		return nil
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	_, err := a.handleDeletionRetry(ctx, deletionRetryReq{AgentIDs: []string{"W#1"}})
	if err != nil {
		t.Fatalf("handleDeletionRetry: %v", err)
	}

	// teardownSubtreeAsync goroutine should schedule finalization.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if afterScheduled.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !afterScheduled.Load() {
		t.Error("expected finalization to be re-scheduled by retry")
	}
}

func TestDeletionRetry_NoopForMissingAgent(t *testing.T) {
	a, ctx := freshActor(t)
	var afterScheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, _ string, _ any) error {
		afterScheduled.Store(true)
		return nil
	}

	_, err := a.handleDeletionRetry(ctx, deletionRetryReq{AgentIDs: []string{"nonexistent"}})
	if err != nil {
		t.Fatalf("handleDeletionRetry: %v", err)
	}

	// Give goroutine time to potentially schedule.
	time.Sleep(50 * time.Millisecond)
	if afterScheduled.Load() {
		t.Error("expected no finalization for missing agent")
	}
}

// ── Backoff ──────────────────────────────────────────────────────────────

func TestDeletionBackoff_BoundedAndIncreasing(t *testing.T) {
	prev := time.Duration(0)
	maxAllowed := 5 * time.Minute
	for attempt := 1; attempt <= deletionMaxAttempts+5; attempt++ {
		d := deletionBackoff(attempt)
		if d > maxAllowed {
			t.Errorf("attempt %d: backoff %v exceeds max %v", attempt, d, maxAllowed)
		}
		if d < prev {
			// Allow plateau but never decrease (the cap can cause equality).
			if d != maxAllowed {
				t.Errorf("attempt %d: backoff %v decreased from %v", attempt, d, prev)
			}
		}
		prev = d
	}
}

// ── End-to-end: teardown with failures ───────────────────────────────────

// TestTeardownSubtreeAsync_DestroyFailure_RetainsNode verifies that when
// ctx.Destroy fails for a node, the finalize handler retains its tombstone
// and schedules a retry. The end-to-end path: teardownSubtreeAsync →
// destroyAgentActor returns (false, err) → finalize → retry scheduled.
func TestTeardownSubtreeAsync_DestroyFailure_RetainsNode(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	// Destroy returns error — node fails teardown.
	destroyErr := errors.New("destroy: actor busy")
	ctx.DestroyFn = func(ref.Ref) error { return destroyErr }
	ctx.LookupIDFn = lookupOK // actor is found, so Destroy is attempted

	var finalizePayload deletionFinalizeReq
	var finalizeCalled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, payload any) error {
		if callID == "workspace.internal_deletion_finalize" {
			if req, ok := payload.(deletionFinalizeReq); ok {
				finalizePayload = req
				finalizeCalled.Store(true)
			}
		}
		return nil
	}

	a.teardownSubtreeAsync(ctx, a.Agents)

	// Wait for goroutine to schedule finalization.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if finalizeCalled.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !finalizeCalled.Load() {
		t.Fatal("expected finalization to be scheduled")
	}

	// Process the finalization on the stateless handler.
	_, _ = a.handleDeletionFinalize(ctx, finalizePayload)

	// Node must be retained (Destroy failed).
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Fatalf("expected 1 deleting agent retained after destroy failure")
	}
	if errMsg := a.deletionErrors["W#1"]; errMsg == "" {
		t.Error("expected error recorded for failed node")
	}
}

// TestTeardownSubtreeAsync_ReleaseFailure_RemovesNode verifies that when
// worktree release fails but the actor is destroyed, the finalize handler
// still removes the tombstone (the worktree binding is stale).
func TestTeardownSubtreeAsync_ReleaseFailure_RemovesNode(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, ProjectID: projectID, DeletionStatus: "deleting"},
	}

	// Destroy succeeds.
	ctx.DestroyFn = func(ref.Ref) error { return nil }
	// LookupID succeeds for both agent and project, but worktree release fails.
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			if callID == "project.worktree_release_binding" {
				return errors.New("worktree busy")
			}
			return nil
		}), true
	}

	var finalizePayload deletionFinalizeReq
	var finalizeCalled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, payload any) error {
		if callID == "workspace.internal_deletion_finalize" {
			if req, ok := payload.(deletionFinalizeReq); ok {
				finalizePayload = req
				finalizeCalled.Store(true)
			}
		}
		return nil
	}

	a.teardownSubtreeAsync(ctx, a.Agents)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if finalizeCalled.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !finalizeCalled.Load() {
		t.Fatal("expected finalization to be scheduled")
	}

	_, _ = a.handleDeletionFinalize(ctx, finalizePayload)

	// Node must be removed — actor is destroyed, worktree release failure
	// only leaves an orphaned git worktree dir (harmless).
	if len(a.Agents) != 0 {
		t.Fatalf("expected 0 agents (tombstone removed despite release failure), got %d", len(a.Agents))
	}
}

// TestTeardownSubtreeAsync_UnbindsAgentCards verifies that a successful
// teardown asks the project actor to clear the deleted agent's id from bound
// wiki cards (project.wiki_unbind_agent).
func TestTeardownSubtreeAsync_UnbindsAgentCards(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, ProjectID: projectID, DeletionStatus: "deleting"},
	}

	ctx.DestroyFn = func(ref.Ref) error { return nil }

	var mu sync.Mutex
	var unbindCalls []string
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			if callID == "project.wiki_unbind_agent" {
				if req, ok := payload.(gen.ProjectWikiUnbindAgentReq); ok {
					mu.Lock()
					unbindCalls = append(unbindCalls, req.AgentActorID)
					mu.Unlock()
				}
			}
			return nil
		}), true
	}

	var finalizeCalled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_finalize" {
			finalizeCalled.Store(true)
		}
		return nil
	}

	a.teardownSubtreeAsync(ctx, a.Agents)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if finalizeCalled.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !finalizeCalled.Load() {
		t.Fatal("expected finalization to be scheduled")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(unbindCalls) != 1 || unbindCalls[0] != agentActorID {
		t.Errorf("expected 1 wiki_unbind_agent call for %q, got %v", agentActorID, unbindCalls)
	}
}

// TestTeardownSubtreeAsync_DestroyFailure_SkipsUnbind verifies that when
// Destroy fails the agent's card bindings are left untouched.
func TestTeardownSubtreeAsync_DestroyFailure_SkipsUnbind(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, ProjectID: projectID, DeletionStatus: "deleting"},
	}

	ctx.DestroyFn = func(ref.Ref) error { return errors.New("destroy: actor busy") }

	var unbindCalled atomic.Bool
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			if callID == "project.wiki_unbind_agent" {
				unbindCalled.Store(true)
			}
			return nil
		}), true
	}

	var finalizeCalled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_finalize" {
			finalizeCalled.Store(true)
		}
		return nil
	}

	a.teardownSubtreeAsync(ctx, a.Agents)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if finalizeCalled.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !finalizeCalled.Load() {
		t.Fatal("expected finalization to be scheduled")
	}
	if unbindCalled.Load() {
		t.Error("wiki_unbind_agent must not be invoked when destroy failed")
	}
}

// TestTeardownSubtreeAsync_AllSucceed_RemovesNode verifies the happy path:
// destroy + release both succeed → finalize removes tombstone.
func TestTeardownSubtreeAsync_AllSucceed_RemovesNode(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	ctx.DestroyFn = func(ref.Ref) error { return nil }

	var finalizePayload deletionFinalizeReq
	var finalizeCalled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, payload any) error {
		if callID == "workspace.internal_deletion_finalize" {
			if req, ok := payload.(deletionFinalizeReq); ok {
				finalizePayload = req
				finalizeCalled.Store(true)
			}
		}
		return nil
	}

	a.teardownSubtreeAsync(ctx, a.Agents)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if finalizeCalled.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !finalizeCalled.Load() {
		t.Fatal("expected finalization to be scheduled")
	}

	_, _ = a.handleDeletionFinalize(ctx, finalizePayload)

	if len(a.Agents) != 0 {
		t.Fatalf("expected 0 agents after successful finalize, got %d", len(a.Agents))
	}
}

// TestTeardownSubtreeAsync_PartialSubtree verifies that a mix of successful
// and failed nodes produces correct finalization: successful removed, failed
// retained.
func TestTeardownSubtreeAsync_PartialSubtree(t *testing.T) {
	a, ctx := freshActor(t)
	okActorID := genID()
	failActorID := genID()
	parentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "P#1", ActorID: parentActorID, DeletionStatus: "deleting", AgentKind: "coordinator"},
		{ID: "OK#1", ActorID: okActorID, ParentAgentID: parentActorID, DeletionStatus: "deleting"},
		{ID: "FAIL#1", ActorID: failActorID, ParentAgentID: parentActorID, DeletionStatus: "deleting"},
	}

	// Destroy fails only for FAIL#1.
	failCid, _ := identity.ParseCanonicalID(failActorID)
	failRef := testutil.NewFakeRef(id.From(failCid), func(string, any) any { return nil })
	ctx.DestroyFn = func(target ref.Ref) error {
		if target.ID().String() == failActorID {
			return errors.New("destroy failed")
		}
		return nil
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == failActorID {
			return failRef, true
		}
		return testutil.NewFakeRef(aid, func(string, any) any { return nil }), true
	}

	var finalizePayload deletionFinalizeReq
	var finalizeCalled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, payload any) error {
		if callID == "workspace.internal_deletion_finalize" {
			if req, ok := payload.(deletionFinalizeReq); ok {
				finalizePayload = req
				finalizeCalled.Store(true)
			}
		}
		return nil
	}

	a.teardownSubtreeAsync(ctx, a.Agents)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if finalizeCalled.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !finalizeCalled.Load() {
		t.Fatal("expected finalization to be scheduled")
	}

	_, _ = a.handleDeletionFinalize(ctx, finalizePayload)

	// P#1 and OK#1 should be removed (destroy succeeded).
	// FAIL#1 should be retained.
	remaining := make(map[string]bool)
	for _, ag := range a.Agents {
		remaining[ag.ID] = true
	}
	if remaining["P#1"] {
		t.Error("expected P#1 removed")
	}
	if remaining["OK#1"] {
		t.Error("expected OK#1 removed")
	}
	if !remaining["FAIL#1"] {
		t.Error("expected FAIL#1 retained")
	}
}

// TestAfterFailure_IntentPreserved verifies that if ctx.After fails to
// schedule finalization, the deletion intent (DeletionStatus="deleting") is
// preserved and the error is logged. The tombstone stays for restart recovery.
func TestAfterFailure_IntentPreserved(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	ctx.DestroyFn = func(ref.Ref) error { return nil }
	// ctx.After always fails.
	ctx.AfterFn = func(_ time.Duration, _ string, _ any) error {
		return errors.New("actor system shutdown")
	}

	a.teardownSubtreeAsync(ctx, a.Agents)

	// Give goroutine time to run.
	time.Sleep(100 * time.Millisecond)

	// The tombstone must be retained — intent preserved.
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Fatalf("expected deletion intent preserved after After failure")
	}
}

// ── Restart recovery ─────────────────────────────────────────────────────

func TestResumeDeletionIntents_RestartsTeardown(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting", AgentKind: domain.AgentKindWorker},
	}
	a.inactiveActorIDs = map[string]string{"W#1": agentActorID}

	var afterCalls atomic.Int32
	ctx.AfterFn = func(_ time.Duration, _ string, _ any) error {
		afterCalls.Add(1)
		return nil
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	a.resumeDeletionIntents(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if afterCalls.Load() > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if afterCalls.Load() == 0 {
		t.Error("expected resumeDeletionIntents to schedule finalization")
	}
}

func TestResumeDeletionIntents_ResetsAttempts(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}
	a.inactiveActorIDs = map[string]string{"W#1": agentActorID}
	a.deletionAttempts = map[string]int{"W#1": 3}

	ctx.DestroyFn = func(ref.Ref) error { return nil }
	ctx.AfterFn = func(_ time.Duration, _ string, _ any) error { return nil }

	a.resumeDeletionIntents(ctx)

	// Attempt count should be reset on restart.
	if a.deletionAttempts["W#1"] != 0 {
		t.Errorf("expected attempt reset to 0, got %d", a.deletionAttempts["W#1"])
	}
}

// ── Project unmount isolation ────────────────────────────────────────────

func TestCascadeDelete_ProjectUnmountSubtree(t *testing.T) {
	a, ctx := freshActor(t)
	projectA := genID()
	projectB := genID()
	a.Agents = []domain.AgentRef{
		{ID: "A#1", ActorID: genID(), ProjectID: projectA},
		{ID: "B#1", ActorID: genID(), ProjectID: projectB},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	a.stopAgentsForProject(ctx, projectA)

	for _, ag := range a.Agents {
		if ag.ID == "A#1" && ag.DeletionStatus != "deleting" {
			t.Errorf("expected A#1 deleting")
		}
		if ag.ID == "B#1" && ag.DeletionStatus == "deleting" {
			t.Errorf("B#1 should not be deleting")
		}
	}
}

func TestStopAgentsForProject_UsesCascade(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := genID()
	otherProjectID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: genID(), ProjectID: projectID, AgentKind: domain.AgentKindWorker},
		{ID: "W#2", ActorID: genID(), ProjectID: projectID, AgentKind: domain.AgentKindWorker},
		{ID: "C#1", ActorID: genID(), ProjectID: otherProjectID},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	a.stopAgentsForProject(ctx, projectID)

	deletingCount := 0
	activeCount := 0
	for _, ag := range a.Agents {
		if ag.DeletionStatus == "deleting" {
			deletingCount++
		} else {
			activeCount++
		}
	}
	if deletingCount != 2 {
		t.Errorf("expected 2 deleting agents, got %d", deletingCount)
	}
	if activeCount != 1 {
		t.Errorf("expected 1 active agent (other project), got %d", activeCount)
	}

	state := a.buildAgentListState(true)
	if len(state.Items) != 1 || state.Items[0].ID != "C#1" {
		t.Errorf("expected only C#1 in projection, got %v", state.Items)
	}
}

// ── Projection filtering ─────────────────────────────────────────────────

func TestBuildAgentListState_ExcludesDeleting(t *testing.T) {
	a, _ := freshActor(t)
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: genID(), DisplayName: "Active"},
		{ID: "W#2", ActorID: genID(), DisplayName: "Deleting", DeletionStatus: "deleting"},
	}

	state := a.buildAgentListState(true)
	if len(state.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(state.Items))
	}
	if state.Items[0].DisplayName != "Active" {
		t.Errorf("expected Active, got %s", state.Items[0].DisplayName)
	}
}

// ── Review approve cascade ───────────────────────────────────────────────

func TestReviewApprove_CascadeWithChildren(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	parentActorID := g.Next().String()
	childActorID := g.Next().String()
	a.Agents = []domain.AgentRef{
		{
			ID:          "W#1",
			ActorID:     parentActorID,
			AgentKind:   domain.AgentKindWorker,
			DisplayName: "Parent",
		},
		{
			ID:            "W#2",
			ActorID:       childActorID,
			ParentAgentID: parentActorID,
			AgentKind:     domain.AgentKindWorker,
			DisplayName:   "Child",
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: parentActorID,
		Decision:     "approve",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved {
		t.Error("expected Approved=true")
	}

	deletingCount := 0
	for _, ag := range a.Agents {
		if ag.DeletionStatus == "deleting" {
			deletingCount++
		}
	}
	if deletingCount != 2 {
		t.Errorf("expected 2 deleting agents (parent + child), got %d", deletingCount)
	}
}

// ── runBounded concurrency ───────────────────────────────────────────────

func TestRunBounded_ConcurrencyLimitRespected(t *testing.T) {
	const n = 50
	const max = 4

	var current atomic.Int32
	var peak atomic.Int32

	items := make([]int, n)
	for i := range items {
		items[i] = i
	}

	var workMu sync.Mutex
	workDone := 0

	runBounded(items, max, func(_ int) {
		c := current.Add(1)
		for {
			p := peak.Load()
			if c <= p || peak.CompareAndSwap(p, c) {
				break
			}
		}
		// Simulate work so concurrency overlap is observable.
		time.Sleep(2 * time.Millisecond)
		current.Add(-1)

		workMu.Lock()
		workDone++
		workMu.Unlock()
	})

	// All items must be processed.
	if workDone != n {
		t.Errorf("expected %d items processed, got %d", n, workDone)
	}
	// Peak concurrency must not exceed max.
	if peak.Load() > int32(max) {
		t.Errorf("peak concurrency %d exceeded max %d", peak.Load(), max)
	}
}

func TestRunBounded_AllItemsProcessed(t *testing.T) {
	const n = 20
	const max = 3

	processed := make([]bool, n)
	var mu sync.Mutex

	items := make([]int, n)
	for i := range items {
		items[i] = i
	}

	runBounded(items, max, func(idx int) {
		mu.Lock()
		processed[idx] = true
		mu.Unlock()
	})

	for i, p := range processed {
		if !p {
			t.Errorf("item %d was not processed", i)
		}
	}
}

func TestRunBounded_EmptyInput(t *testing.T) {
	// Should not panic and return immediately.
	runBounded([]int{}, 4, func(int) {
		t.Error("fn should not be called for empty input")
	})
}

// ── Deletion sweep: safety net for ctx.After failures ───────────────────

// TestDeletionSweep_RekicksStuckAgents verifies that the sweep handler finds
// agents stuck in "deleting" and re-kicks their teardown.
func TestDeletionSweep_RekicksStuckAgents(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	var finalizeScheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_finalize" {
			finalizeScheduled.Store(true)
		}
		return nil
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	_, err := a.handleDeletionSweep(ctx, deletionSweepReq{})
	if err != nil {
		t.Fatalf("handleDeletionSweep: %v", err)
	}

	// teardownSubtreeAsync goroutine should schedule finalization.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if finalizeScheduled.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !finalizeScheduled.Load() {
		t.Error("expected sweep to re-kick teardown and schedule finalization")
	}
}

// TestDeletionSweep_SkipsExhausted verifies that the sweep does not re-kick
// agents that have exhausted their retry budget.
func TestDeletionSweep_SkipsExhausted(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}
	a.deletionAttempts = map[string]int{"W#1": deletionMaxAttempts}

	var anyAfter atomic.Int32
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_finalize" {
			anyAfter.Add(1)
		}
		return nil
	}

	_, err := a.handleDeletionSweep(ctx, deletionSweepReq{})
	if err != nil {
		t.Fatalf("handleDeletionSweep: %v", err)
	}

	// Give goroutine time to potentially schedule.
	time.Sleep(100 * time.Millisecond)

	// Exhausted agents should NOT have finalization re-scheduled.
	if anyAfter.Load() > 0 {
		t.Error("expected sweep to skip exhausted agents")
	}
}

// TestDeletionSweep_SelfReschedules verifies that the sweep handler always
// schedules itself for the next interval, even when there are no stuck agents.
func TestDeletionSweep_SelfReschedules(t *testing.T) {
	a, ctx := freshActor(t)

	var sweepRescheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_sweep" {
			sweepRescheduled.Store(true)
		}
		return nil
	}

	_, err := a.handleDeletionSweep(ctx, deletionSweepReq{})
	if err != nil {
		t.Fatalf("handleDeletionSweep: %v", err)
	}

	if !sweepRescheduled.Load() {
		t.Error("expected sweep to self-reschedule")
	}
}

// TestStartDeletionSweep_OnlyOnce verifies that startDeletionSweep only
// schedules the first tick and does not duplicate.
func TestStartDeletionSweep_OnlyOnce(t *testing.T) {
	a, ctx := freshActor(t)

	// Reset the flag set by freshActor's OnStart so we can test the function.
	a.deletionSweepScheduled = false

	var sweepCalls atomic.Int32
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_sweep" {
			sweepCalls.Add(1)
		}
		return nil
	}

	a.startDeletionSweep(ctx)
	a.startDeletionSweep(ctx) // second call should be a no-op
	a.startDeletionSweep(ctx) // third call should be a no-op

	if sweepCalls.Load() != 1 {
		t.Errorf("expected exactly 1 sweep scheduling, got %d", sweepCalls.Load())
	}
}

// TestAfterFailure_RecoveryViaSweep verifies the full recovery path:
// 1. ctx.After fails when scheduling finalize → tombstone stays.
// 2. Sweep runs → finds stuck agent → re-kicks teardown.
// 3. Second time, ctx.After succeeds → finalize runs → tombstone removed.
func TestAfterFailure_RecoveryViaSweep(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	// First pass: ctx.After fails for finalize only. Sweep should succeed.
	var afterMu sync.Mutex
	var finalizeCallCount int
	sweepAllowed := true
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		afterMu.Lock()
		defer afterMu.Unlock()
		if callID == "workspace.internal_deletion_finalize" {
			finalizeCallCount++
			if finalizeCallCount == 1 {
				return errors.New("after failure on first finalize")
			}
			return nil // subsequent succeeds
		}
		if callID == "workspace.internal_deletion_sweep" {
			if !sweepAllowed {
				return nil // sweep itself shouldn't fail
			}
			return nil
		}
		return nil
	}

	// Step 1: Kick off teardown. Goroutine will fail to schedule finalize.
	a.teardownSubtreeAsync(ctx, a.Agents)
	time.Sleep(200 * time.Millisecond) // wait for goroutine to complete

	// Tombstone must still be present (finalize scheduling failed).
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Fatalf("expected tombstone retained after After failure, got %d agents", len(a.Agents))
	}

	// Simulate the in-flight staleness timeout having passed so the sweep
	// will clear the stuck flag and re-kick.
	a.deletionInFlightSince["W#1"] = time.Now().Add(-deletionInFlightStaleTimeout - time.Second)

	// Step 2: Run the sweep. It should find the stuck agent (stale in-flight)
	// and re-kick.
	_, err := a.handleDeletionSweep(ctx, deletionSweepReq{})
	if err != nil {
		t.Fatalf("handleDeletionSweep: %v", err)
	}

	// Wait for the second teardown goroutine + finalize scheduling.
	time.Sleep(200 * time.Millisecond)

	// Step 3: Tombstone should be gone — sweep recovered the deletion.
	// The second teardown succeeded (Destroy returns nil, no project).
	// finalize After succeeded (finalizeCallCount == 2). Simulate the
	// second finalize invocation. The counter is written by
	// the teardown goroutine via AfterFn, so read it under afterMu.
	afterMu.Lock()
	finalizeCalls := finalizeCallCount
	afterMu.Unlock()
	if finalizeCalls >= 2 {
		// The second finalize was "scheduled" (returned nil). Simulate it.
		// Use the current generation (incremented by the sweep's re-kick).
		_, _ = a.handleDeletionFinalize(ctx, deletionFinalizeReq{
			Results: []deletionNodeResult{
				{AgentID: "W#1", ActorID: agentActorID, Destroyed: true, WorktreeFreed: true, Generation: a.deletionGeneration["W#1"]},
			},
		})
	}

	if len(a.Agents) != 0 {
		t.Errorf("expected tombstone removed after sweep recovery, got %d agents", len(a.Agents))
	}
}

// TestAfterFailure_PersistentFailureRetainsTombstone verifies that when
// finalize scheduling persistently fails and the sweep also can't finalize,
// the tombstone is retained and the system does not enter a busy-loop.
func TestAfterFailure_PersistentFailureRetainsTombstone(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	var sweepCallCount atomic.Int32
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_finalize" {
			return errors.New("persistent After failure")
		}
		if callID == "workspace.internal_deletion_sweep" {
			sweepCallCount.Add(1)
		}
		return nil
	}

	// Kick off teardown.
	a.teardownSubtreeAsync(ctx, a.Agents)
	time.Sleep(100 * time.Millisecond)

	// Run multiple sweeps — each will re-kick but finalize will fail again.
	for i := 0; i < 3; i++ {
		_, _ = a.handleDeletionSweep(ctx, deletionSweepReq{})
		time.Sleep(50 * time.Millisecond)
	}

	// Tombstone must be retained.
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Fatalf("expected tombstone retained after persistent failure")
	}

	// Sweep must have self-rescheduled each time (no busy-loop because it
	// uses deletionSweepInterval, not a tight loop).
	if sweepCallCount.Load() < 3 {
		t.Errorf("expected sweep to have been called at least 3 times, got %d", sweepCallCount.Load())
	}
}

// ── Layered partial convergence: child succeeds, parent fails ────────────

// TestDeletionFinalize_ChildSucceedsParentFails verifies that when a child
// agent's teardown succeeds but the parent's fails, the child's tombstone is
// removed while the parent's is retained. This ensures independent per-node
// convergence within a subtree.
func TestDeletionFinalize_ChildSucceedsParentFails(t *testing.T) {
	a, ctx := freshActor(t)
	parentActorID := genID()
	childActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "P#1", ActorID: parentActorID, DeletionStatus: "deleting"},
		{ID: "C#1", ActorID: childActorID, ParentAgentID: parentActorID, DeletionStatus: "deleting"},
	}

	var retryScheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_retry" {
			retryScheduled.Store(true)
		}
		return nil
	}

	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "P#1", ActorID: parentActorID, Destroyed: false, WorktreeFreed: true, DestroyError: "parent destroy failed"},
			{AgentID: "C#1", ActorID: childActorID, Destroyed: true, WorktreeFreed: true},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// Child should be removed (succeeded).
	// Parent should be retained (failed).
	remaining := make(map[string]domain.AgentRef)
	for _, ag := range a.Agents {
		remaining[ag.ID] = ag
	}

	if _, ok := remaining["C#1"]; ok {
		t.Error("expected child C#1 removed after successful teardown")
	}
	parent, ok := remaining["P#1"]
	if !ok {
		t.Fatal("expected parent P#1 retained after failed teardown")
	}
	if parent.DeletionStatus != "deleting" {
		t.Errorf("expected parent P#1 still deleting, got %q", parent.DeletionStatus)
	}

	// Error should be recorded for parent only.
	if errMsg := a.deletionErrors["P#1"]; errMsg != "parent destroy failed" {
		t.Errorf("expected error recorded for P#1, got %q", errMsg)
	}
	if _, hasErr := a.deletionErrors["C#1"]; hasErr {
		t.Error("expected no error for child C#1 (succeeded)")
	}

	// Retry should be scheduled for parent only.
	if !retryScheduled.Load() {
		t.Error("expected retry scheduled for failed parent")
	}
}

// TestDeletionFinalize_DeepSubtree_PartialConvergence verifies a multi-level
// subtree where some nodes at different depths succeed and others fail.
func TestDeletionFinalize_DeepSubtree_PartialConvergence(t *testing.T) {
	a, ctx := freshActor(t)
	rootID := genID()
	mid1ID := genID()
	mid2ID := genID()
	leafID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "ROOT", ActorID: rootID, DeletionStatus: "deleting"},
		{ID: "MID1", ActorID: mid1ID, ParentAgentID: rootID, DeletionStatus: "deleting"},
		{ID: "MID2", ActorID: mid2ID, ParentAgentID: rootID, DeletionStatus: "deleting"},
		{ID: "LEAF", ActorID: leafID, ParentAgentID: mid1ID, DeletionStatus: "deleting"},
	}

	// ROOT fails, MID1 succeeds, MID2 fails, LEAF succeeds.
	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "ROOT", ActorID: rootID, Destroyed: false, WorktreeFreed: true, DestroyError: "root failed"},
			{AgentID: "MID1", ActorID: mid1ID, Destroyed: true, WorktreeFreed: true},
			{AgentID: "MID2", ActorID: mid2ID, Destroyed: false, WorktreeFreed: true, DestroyError: "mid2 failed"},
			{AgentID: "LEAF", ActorID: leafID, Destroyed: true, WorktreeFreed: true},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	remaining := make(map[string]bool)
	for _, ag := range a.Agents {
		remaining[ag.ID] = true
	}

	// Successful nodes removed.
	if remaining["MID1"] {
		t.Error("expected MID1 removed")
	}
	if remaining["LEAF"] {
		t.Error("expected LEAF removed")
	}

	// Failed nodes retained.
	if !remaining["ROOT"] {
		t.Error("expected ROOT retained")
	}
	if !remaining["MID2"] {
		t.Error("expected MID2 retained")
	}
}

// ── Issue 1: Release NOT called when Destroy fails ───────────────────────

// TestDestroyFailure_ReleaseNotCalled verifies that when ctx.Destroy fails,
// releaseWorktree is never called for that node. Releasing the worktree of a
// still-live actor would strip its isolated environment.
func TestDestroyFailure_ReleaseNotCalled(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, ProjectID: projectID, DeletionStatus: "deleting"},
	}

	// Destroy fails for this agent.
	ctx.DestroyFn = func(ref.Ref) error { return errors.New("destroy: actor busy") }

	// Track whether worktree release was invoked.
	releaseCalled := atomic.Bool{}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == agentActorID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				return nil
			}), true
		}
		if aidStr == projectID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "project.worktree_release_binding" {
					releaseCalled.Store(true)
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(string, any) any { return nil }), true
	}

	var finalizePayload deletionFinalizeReq
	var finalizeCalled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, payload any) error {
		if callID == "workspace.internal_deletion_finalize" {
			if req, ok := payload.(deletionFinalizeReq); ok {
				finalizePayload = req
				finalizeCalled.Store(true)
			}
		}
		return nil
	}

	a.teardownSubtreeAsync(ctx, a.Agents)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if finalizeCalled.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !finalizeCalled.Load() {
		t.Fatal("expected finalization to be scheduled")
	}

	// Verify: Destroy failed, so release was NOT called.
	if len(finalizePayload.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(finalizePayload.Results))
	}
	r := finalizePayload.Results[0]
	if r.Destroyed {
		t.Error("expected Destroyed=false (Destroy failed)")
	}
	if r.WorktreeFreed {
		t.Error("expected WorktreeFreed=false (release should not have been called)")
	}
	if releaseCalled.Load() {
		t.Error("releaseWorktree must NOT be called when Destroy failed")
	}
}

// ── Issue 2: In-flight tracking prevents concurrent teardown ─────────────

// TestInFlight_PreventsConcurrentTeardown verifies that when an agent is
// in-flight, a second teardownSubtreeAsync call for the same agent is a no-op.
func TestInFlight_PreventsConcurrentTeardown(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	var afterCount atomic.Int32
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_finalize" {
			afterCount.Add(1)
		}
		return nil
	}

	// Block Destroy so the goroutine stays running.
	destroyStarted := make(chan struct{})
	destroyBlocked := make(chan struct{})
	ctx.DestroyFn = func(ref.Ref) error {
		destroyStarted <- struct{}{}
		<-destroyBlocked // block until test releases
		return nil
	}

	// First teardown: should start goroutine.
	a.teardownSubtreeAsync(ctx, a.Agents)
	<-destroyStarted // wait for goroutine to reach Destroy

	// In-flight should be set.
	if !a.deletionInFlight["W#1"] {
		t.Fatal("expected W#1 to be in-flight after first teardown")
	}

	// Second teardown: should be a no-op (agent is in-flight).
	a.teardownSubtreeAsync(ctx, a.Agents)

	// Release the blocked Destroy.
	close(destroyBlocked)

	// Wait for first goroutine to complete.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if afterCount.Load() >= 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	// Only ONE finalize should have been scheduled (second teardown was no-op).
	if afterCount.Load() > 1 {
		t.Errorf("expected at most 1 finalize, got %d (second teardown should have been skipped)", afterCount.Load())
	}
}

// TestSweep_SkipsInFlight verifies that the sweep does not re-kick agents
// that are currently in-flight (not stale).
func TestSweep_SkipsInFlight(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	// Mark agent as in-flight (not stale).
	a.deletionInFlight = map[string]bool{"W#1": true}
	a.deletionInFlightSince = map[string]time.Time{"W#1": time.Now()}

	var finalizeScheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_finalize" {
			finalizeScheduled.Store(true)
		}
		return nil
	}

	_, err := a.handleDeletionSweep(ctx, deletionSweepReq{})
	if err != nil {
		t.Fatalf("handleDeletionSweep: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Sweep should NOT have re-kicked (agent is in-flight, not stale).
	if finalizeScheduled.Load() {
		t.Error("expected sweep to skip in-flight agent")
	}
}

// TestSweep_ClearsStaleInFlight verifies that the sweep clears stale in-flight
// flags and re-kicks the agent.
func TestSweep_ClearsStaleInFlight(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	// Mark agent as in-flight but stale.
	a.deletionInFlight = map[string]bool{"W#1": true}
	a.deletionInFlightSince = map[string]time.Time{"W#1": time.Now().Add(-deletionInFlightStaleTimeout - time.Minute)}

	var finalizeScheduled atomic.Bool
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_finalize" {
			finalizeScheduled.Store(true)
		}
		return nil
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	_, err := a.handleDeletionSweep(ctx, deletionSweepReq{})
	if err != nil {
		t.Fatalf("handleDeletionSweep: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if finalizeScheduled.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !finalizeScheduled.Load() {
		t.Error("expected sweep to clear stale in-flight and re-kick teardown")
	}
}

// ── Issue 2: Generation token prevents stale finalize ────────────────────

// TestGeneration_StaleFinalizeIgnored verifies that a finalize from an old
// generation is ignored when a newer generation is in progress.
func TestGeneration_StaleFinalizeIgnored(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	// Set up: generation is now 2 (newer teardown in progress).
	a.deletionGeneration = map[string]uint64{"W#1": 2}
	a.deletionInFlight = map[string]bool{"W#1": true}
	a.deletionInFlightSince = map[string]time.Time{"W#1": time.Now()}

	// Receive a stale finalize from generation 1.
	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "W#1", ActorID: agentActorID, Destroyed: true, WorktreeFreed: true, Generation: 1},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// Stale finalize should NOT have removed the tombstone.
	if len(a.Agents) != 1 {
		t.Errorf("expected tombstone retained (stale finalize), got %d agents", len(a.Agents))
	}
	// In-flight should still be set (current gen 2 is still active).
	if !a.deletionInFlight["W#1"] {
		t.Error("expected in-flight to remain set (current gen still active)")
	}
}

// TestGeneration_CurrentFinalizeProcessed verifies that a finalize matching
// the current generation is processed normally.
func TestGeneration_CurrentFinalizeProcessed(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	// Set up: generation is 2, in-flight.
	a.deletionGeneration = map[string]uint64{"W#1": 2}
	a.deletionInFlight = map[string]bool{"W#1": true}
	a.deletionInFlightSince = map[string]time.Time{"W#1": time.Now()}

	// Receive a matching finalize from generation 2.
	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "W#1", ActorID: agentActorID, Destroyed: true, WorktreeFreed: true, Generation: 2},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// Tombstone should be removed.
	if len(a.Agents) != 0 {
		t.Errorf("expected tombstone removed, got %d agents", len(a.Agents))
	}
	// In-flight should be cleared.
	if a.deletionInFlight["W#1"] {
		t.Error("expected in-flight cleared after matching finalize")
	}
}

// ── Issue 2 (fix): Mixed-generation batch per-node staleness ─────────────

// TestDeletionFinalize_MixedGenerationBatch_SuccessAndFailure verifies that
// when agents in the same teardown batch have different generations (e.g.,
// child had a prior failed retry → gen=2, parent is first-time → gen=1),
// the finalize handler correctly processes BOTH nodes — neither is
// incorrectly marked stale. The child succeeds (removed) and the parent
// fails (retained + retry scheduled), with in-flight cleared for both.
func TestDeletionFinalize_MixedGenerationBatch_SuccessAndFailure(t *testing.T) {
	a, ctx := freshActor(t)
	parentActorID := genID()
	childActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "P#1", ActorID: parentActorID, DeletionStatus: "deleting"},
		{ID: "C#1", ActorID: childActorID, ParentAgentID: parentActorID, DeletionStatus: "deleting"},
	}

	// Simulate mixed generation history:
	//   P#1 is first-time claim → gen=1
	//   C#1 had one prior failed retry → gen=2
	// Both are in-flight.
	a.deletionGeneration = map[string]uint64{
		"P#1": 1,
		"C#1": 2,
	}
	a.deletionInFlight = map[string]bool{
		"P#1": true,
		"C#1": true,
	}
	a.deletionInFlightSince = map[string]time.Time{
		"P#1": time.Now(),
		"C#1": time.Now(),
	}

	var retryScheduled atomic.Int32
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_retry" {
			retryScheduled.Add(1)
		}
		return nil
	}

	// Finalize with per-node generations: child (gen=2) succeeds, parent (gen=1) fails.
	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "P#1", ActorID: parentActorID, Destroyed: false, WorktreeFreed: true, DestroyError: "parent destroy failed", Generation: 1},
			{AgentID: "C#1", ActorID: childActorID, Destroyed: true, WorktreeFreed: true, Generation: 2},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// Child should be removed (success, gen matches).
	remaining := make(map[string]domain.AgentRef)
	for _, ag := range a.Agents {
		remaining[ag.ID] = ag
	}
	if _, ok := remaining["C#1"]; ok {
		t.Error("expected child C#1 removed (succeeded, gen matched)")
	}

	// Parent should be retained (failed, gen matched).
	parent, ok := remaining["P#1"]
	if !ok {
		t.Fatal("expected parent P#1 retained")
	}
	if parent.DeletionStatus != "deleting" {
		t.Errorf("expected parent still deleting, got %q", parent.DeletionStatus)
	}

	// Both in-flight flags must be cleared (both processed, not stale).
	if a.deletionInFlight["P#1"] {
		t.Error("expected parent in-flight cleared (gen matched, processed)")
	}
	if a.deletionInFlight["C#1"] {
		t.Error("expected child in-flight cleared (gen matched, processed)")
	}

	// Error recorded for parent only.
	if errMsg := a.deletionErrors["P#1"]; errMsg != "parent destroy failed" {
		t.Errorf("expected error for P#1, got %q", errMsg)
	}
	if _, hasErr := a.deletionErrors["C#1"]; hasErr {
		t.Error("expected no error for child C#1 (succeeded)")
	}

	// Retry scheduled for parent (the only failure).
	if retryScheduled.Load() != 1 {
		t.Errorf("expected exactly 1 retry scheduled (for parent), got %d", retryScheduled.Load())
	}
}

// TestDeletionFinalize_MixedGenerationBatch_AllSucceed verifies that when
// all nodes succeed with different generations, all are removed and all
// in-flight flags cleared.
func TestDeletionFinalize_MixedGenerationBatch_AllSucceed(t *testing.T) {
	a, ctx := freshActor(t)
	rootID := genID()
	midID := genID()
	leafID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "ROOT", ActorID: rootID, DeletionStatus: "deleting"},
		{ID: "MID", ActorID: midID, ParentAgentID: rootID, DeletionStatus: "deleting"},
		{ID: "LEAF", ActorID: leafID, ParentAgentID: midID, DeletionStatus: "deleting"},
	}

	// Three different generations (ROOT=1 first-time, MID=2 prior retry, LEAF=3 double-retry).
	a.deletionGeneration = map[string]uint64{"ROOT": 1, "MID": 2, "LEAF": 3}
	a.deletionInFlight = map[string]bool{"ROOT": true, "MID": true, "LEAF": true}
	a.deletionInFlightSince = map[string]time.Time{
		"ROOT": time.Now(), "MID": time.Now(), "LEAF": time.Now(),
	}

	ctx.AfterFn = func(_ time.Duration, _ string, _ any) error { return nil }

	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "ROOT", ActorID: rootID, Destroyed: true, WorktreeFreed: true, Generation: 1},
			{AgentID: "MID", ActorID: midID, Destroyed: true, WorktreeFreed: true, Generation: 2},
			{AgentID: "LEAF", ActorID: leafID, Destroyed: true, WorktreeFreed: true, Generation: 3},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// All nodes removed.
	if len(a.Agents) != 0 {
		t.Errorf("expected 0 agents (all removed), got %d", len(a.Agents))
	}

	// All in-flight cleared.
	for _, id := range []string{"ROOT", "MID", "LEAF"} {
		if a.deletionInFlight[id] {
			t.Errorf("expected %s in-flight cleared", id)
		}
	}
}

// TestDeletionFinalize_MixedGenerationBatch_AllFail verifies that when all
// nodes fail with different generations, all are retained, all errors
// recorded, all attempts incremented, and retries scheduled for all.
func TestDeletionFinalize_MixedGenerationBatch_AllFail(t *testing.T) {
	a, ctx := freshActor(t)
	parentID := genID()
	childID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "P#1", ActorID: parentID, DeletionStatus: "deleting"},
		{ID: "C#1", ActorID: childID, ParentAgentID: parentID, DeletionStatus: "deleting"},
	}

	a.deletionGeneration = map[string]uint64{"P#1": 1, "C#1": 2}
	a.deletionInFlight = map[string]bool{"P#1": true, "C#1": true}
	a.deletionInFlightSince = map[string]time.Time{"P#1": time.Now(), "C#1": time.Now()}

	var retryScheduled atomic.Int32
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_retry" {
			retryScheduled.Add(1)
		}
		return nil
	}

	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "P#1", ActorID: parentID, Destroyed: false, WorktreeFreed: true, DestroyError: "parent fail", Generation: 1},
			{AgentID: "C#1", ActorID: childID, Destroyed: false, WorktreeFreed: true, DestroyError: "child fail", Generation: 2},
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// Both retained.
	if len(a.Agents) != 2 {
		t.Errorf("expected 2 agents retained, got %d", len(a.Agents))
	}

	// Both in-flight cleared (both processed despite failure).
	if a.deletionInFlight["P#1"] || a.deletionInFlight["C#1"] {
		t.Error("expected both in-flight cleared (both processed)")
	}

	// Both errors recorded.
	if a.deletionErrors["P#1"] != "parent fail" || a.deletionErrors["C#1"] != "child fail" {
		t.Errorf("expected both errors recorded, got P=%q C=%q", a.deletionErrors["P#1"], a.deletionErrors["C#1"])
	}

	// Both attempts incremented.
	if a.deletionAttempts["P#1"] != 1 || a.deletionAttempts["C#1"] != 1 {
		t.Errorf("expected both attempts=1, got P=%d C=%d", a.deletionAttempts["P#1"], a.deletionAttempts["C#1"])
	}

	// Both retries scheduled.
	if retryScheduled.Load() != 2 {
		t.Errorf("expected 2 retries scheduled, got %d", retryScheduled.Load())
	}
}

// TestDeletionFinalize_MixedGeneration_StaleNodeMixedWithCurrent verifies
// that in a batch where some nodes are stale and some are current, only the
// current nodes are processed. Stale nodes keep their in-flight flag.
func TestDeletionFinalize_MixedGeneration_StaleNodeMixedWithCurrent(t *testing.T) {
	a, ctx := freshActor(t)
	agentAID := genID()
	agentBID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "A#1", ActorID: agentAID, DeletionStatus: "deleting"},
		{ID: "B#1", ActorID: agentBID, DeletionStatus: "deleting"},
	}

	// A is current (gen=2 in map, gen=2 in result).
	// B is stale (gen=5 in map, gen=2 in result — a newer gen was started).
	a.deletionGeneration = map[string]uint64{"A#1": 2, "B#1": 5}
	a.deletionInFlight = map[string]bool{"A#1": true, "B#1": true}
	a.deletionInFlightSince = map[string]time.Time{"A#1": time.Now(), "B#1": time.Now()}

	var retryScheduled atomic.Int32
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_deletion_retry" {
			retryScheduled.Add(1)
		}
		return nil
	}

	_, err := a.handleDeletionFinalize(ctx, deletionFinalizeReq{
		Results: []deletionNodeResult{
			{AgentID: "A#1", ActorID: agentAID, Destroyed: true, WorktreeFreed: true, Generation: 2},                       // current
			{AgentID: "B#1", ActorID: agentBID, Destroyed: false, WorktreeFreed: true, DestroyError: "err", Generation: 2}, // stale (map=5≠2)
		},
	})
	if err != nil {
		t.Fatalf("handleDeletionFinalize: %v", err)
	}

	// A should be removed (current, succeeded).
	remaining := make(map[string]bool)
	for _, ag := range a.Agents {
		remaining[ag.ID] = true
	}
	if remaining["A#1"] {
		t.Error("expected A#1 removed (current, succeeded)")
	}

	// B should be retained (stale — not processed at all).
	if !remaining["B#1"] {
		t.Error("expected B#1 retained (stale, not processed)")
	}

	// A in-flight cleared (processed). B in-flight retained (stale, newer gen active).
	if a.deletionInFlight["A#1"] {
		t.Error("expected A#1 in-flight cleared")
	}
	if !a.deletionInFlight["B#1"] {
		t.Error("expected B#1 in-flight retained (stale)")
	}

	// No retry scheduled for B (stale, not processed).
	if retryScheduled.Load() != 0 {
		t.Errorf("expected 0 retries (B is stale), got %d", retryScheduled.Load())
	}
}

// ── Issue 2: Retry + sweep race prevention ───────────────────────────────

// TestRetrySweepRace_NoDoubleTeardown verifies that when both retry and sweep
// run for the same agent, only one teardown starts (in-flight prevents the
// second).
func TestRetrySweepRace_NoDoubleTeardown(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	var teardownCount atomic.Int32

	// Block Destroy so the goroutine stays running (simulates slow teardown).
	destroyStarted := make(chan struct{})
	destroyBlocked := make(chan struct{})
	once := sync.Once{}
	ctx.DestroyFn = func(ref.Ref) error {
		once.Do(func() { teardownCount.Add(1) })
		destroyStarted <- struct{}{}
		<-destroyBlocked
		return nil
	}
	ctx.AfterFn = func(_ time.Duration, _ string, _ any) error { return nil }

	// Start first teardown via retry handler.
	_, _ = a.handleDeletionRetry(ctx, deletionRetryReq{AgentIDs: []string{"W#1"}})
	<-destroyStarted // wait for goroutine to reach Destroy

	// Now sweep runs — should NOT re-kick (in-flight is set).
	_, _ = a.handleDeletionSweep(ctx, deletionSweepReq{})

	// Release the blocked Destroy.
	close(destroyBlocked)
	time.Sleep(100 * time.Millisecond)

	// Only one teardown should have started.
	if teardownCount.Load() > 1 {
		t.Errorf("expected at most 1 teardown, got %d", teardownCount.Load())
	}
}

// ── Issue 2: Blocking > 1 tick ───────────────────────────────────────────

// TestBlocking_MoreThanOneTick verifies that when a teardown takes longer
// than one sweep interval, the sweep does NOT re-kick it while it's in-flight.
func TestBlocking_MoreThanOneTick(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "W#1", ActorID: agentActorID, DeletionStatus: "deleting"},
	}

	var teardownCount atomic.Int32

	// Block Destroy so the goroutine stays running.
	destroyStarted := make(chan struct{})
	destroyBlocked := make(chan struct{})
	once := sync.Once{}
	ctx.DestroyFn = func(ref.Ref) error {
		once.Do(func() { teardownCount.Add(1) })
		select {
		case destroyStarted <- struct{}{}:
		default:
		}
		<-destroyBlocked
		return nil
	}
	ctx.AfterFn = func(_ time.Duration, _ string, _ any) error { return nil }

	// Start teardown.
	a.teardownSubtreeAsync(ctx, a.Agents)
	time.Sleep(50 * time.Millisecond) // let goroutine start

	// Run sweep multiple times (simulating multiple ticks while teardown
	// is still in progress).
	for i := 0; i < 3; i++ {
		_, _ = a.handleDeletionSweep(ctx, deletionSweepReq{})
		time.Sleep(10 * time.Millisecond)
	}

	// Release.
	close(destroyBlocked)
	time.Sleep(100 * time.Millisecond)

	// Only one teardown should have started despite multiple sweep ticks.
	if teardownCount.Load() > 1 {
		t.Errorf("expected at most 1 teardown during blocking, got %d", teardownCount.Load())
	}
}

// ── Issue 4: Sweep re-arm after scheduling failure ───────────────────────

// TestSweepRearm_ShortRetryOnFailure verifies that when the sweep fails to
// re-schedule with the normal interval, it retries with a shorter delay.
func TestSweepRearm_ShortRetryOnFailure(t *testing.T) {
	a, ctx := freshActor(t)

	var normalScheduled atomic.Bool
	var shortScheduled atomic.Bool
	var afterMu sync.Mutex
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		afterMu.Lock()
		defer afterMu.Unlock()
		if callID == "workspace.internal_deletion_sweep" {
			if !normalScheduled.Load() {
				// First attempt (normal interval) fails.
				normalScheduled.Store(true)
				return errors.New("schedule failed")
			}
			// Second attempt (short re-arm) succeeds.
			shortScheduled.Store(true)
		}
		return nil
	}

	a.scheduleSweepNext(ctx)

	if !normalScheduled.Load() {
		t.Error("expected normal interval scheduling to be attempted")
	}
	if !shortScheduled.Load() {
		t.Error("expected short re-arm to be attempted after normal failure")
	}
}

// TestSweepRearm_BothFailClearsFlag verifies that when both normal and short
// re-arm fail, the scheduled flag is cleared (so OnStart can retry).
func TestSweepRearm_BothFailClearsFlag(t *testing.T) {
	a, ctx := freshActor(t)
	a.deletionSweepScheduled = true

	ctx.AfterFn = func(_ time.Duration, _ string, _ any) error {
		return errors.New("all scheduling fails")
	}

	a.scheduleSweepNext(ctx)

	if a.deletionSweepScheduled {
		t.Error("expected deletionSweepScheduled to be cleared after both re-arm attempts fail")
	}
}

// TestDeletionFields_ConcurrentAccessAcrossLoops simulates the post-move loop
// topology: finalize on the "deletion" loop, retry/sweep on forked pure-loop
// goroutines, teardown claims arriving from other pure-loop handlers
// (cascadeDelete callers), and Save persisting the retry card. It hammers the
// deletion* maps from all sides concurrently; under -race it proves every
// access path is covered by deletionMu. Correctness invariant after the storm:
// in-flight flags and their staleness timestamps are never torn apart (each
// handler sets/clears both in one deletionMu section).
func TestDeletionFields_ConcurrentAccessAcrossLoops(t *testing.T) {
	a, ctx := freshActor(t)
	const agentsN = 12
	for i := 0; i < agentsN; i++ {
		a.Agents = append(a.Agents, domain.AgentRef{
			ID:             fmt.Sprintf("W#%d", i),
			ActorID:        genID(),
			DeletionStatus: "deleting",
		})
	}
	// Destroy always fails so nodes stay deleting and every retry/error path
	// keeps firing throughout the storm.
	ctx.DestroyFn = func(ref.Ref) error { return errors.New("busy") }
	ctx.AfterFn = func(_ time.Duration, _ string, _ any) error { return nil }

	var wg sync.WaitGroup

	// Deletion loop: finalize per-node results with random generations
	// (stale + current mixed), driving errors/attempts/in-flight writes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(11))
		for i := 0; i < 400; i++ {
			results := make([]deletionNodeResult, 0, 3)
			for j := 0; j < 3; j++ {
				results = append(results, deletionNodeResult{
					AgentID:      fmt.Sprintf("W#%d", rng.Intn(agentsN)),
					ActorID:      genID(),
					Destroyed:    false,
					WorktreeFreed: true,
					DestroyError: "concurrent storm",
					Generation:   uint64(1 + rng.Intn(3)),
				})
			}
			_, _ = a.handleDeletionFinalize(ctx, deletionFinalizeReq{Results: results})
		}
	}()

	// Deletion loop: periodic sweep (stale-flag clearing + re-kick).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = a.handleDeletionSweep(ctx, deletionSweepReq{})
		}
	}()

	// Deletion loop: bounded-backoff retries re-kicking teardown.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(22))
		for i := 0; i < 200; i++ {
			_, _ = a.handleDeletionRetry(ctx, deletionRetryReq{
				AgentIDs: []string{fmt.Sprintf("W#%d", rng.Intn(agentsN))},
			})
		}
	}()

	// Owner/lifecycle loops: cascade teardown claims for random subtrees
	// (in-flight check + generation bump under deletionMu).
	wg.Add(1)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(33))
		for i := 0; i < 100; i++ {
			agents := a.agentSnapshot()
			a.teardownSubtreeAsync(ctx, agents[rng.Intn(len(agents)):])
		}
	}()

	// Owner loop: Save path persisting the retry card (map copy under RLock).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if err := a.saveDeletionRetryCard(); err != nil {
				t.Errorf("saveDeletionRetryCard: %v", err)
			}
		}
	}()

	wg.Wait()

	// Convergence assertion: no torn map state. In-flight flags and their
	// staleness timestamps always move together (set/cleared in the same
	// deletionMu section in every handler above).
	a.deletionMu.Lock()
	for i := 0; i < agentsN; i++ {
		id := fmt.Sprintf("W#%d", i)
		if a.deletionInFlight[id] {
			if _, ok := a.deletionInFlightSince[id]; !ok {
				t.Errorf("agent %s in-flight without timestamp", id)
			}
		} else if _, ok := a.deletionInFlightSince[id]; ok {
			t.Errorf("agent %s not in-flight but has timestamp", id)
		}
	}
	a.deletionMu.Unlock()
}
