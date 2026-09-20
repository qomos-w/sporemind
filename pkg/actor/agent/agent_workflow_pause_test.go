package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// makeWorkflowPauseCtx returns a test context with a fake workspace that reports
// the supplied agents, and fake child refs resolvable by ActorID via LookupIDFn.
func makeWorkflowPauseCtx(t *testing.T, agents []domain.AgentRef, childRefs map[string]ref.Ref) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.AnonCtx(testutil.GenActorID())
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if callID == "workspace.list_agents" {
			return domain.AgentRefListResp{Items: agents}
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if r, ok := childRefs[aid.String()]; ok {
			return r, true
		}
		return nil, false
	}
	return ctx
}

// newWaitingOwner returns an agent actor in the waiting workflow state with a
// waiting turn record ready for the handler.
func newWaitingOwner(t *testing.T, ownerID, turnID string) *Actor {
	t.Helper()
	a := &Actor{
		actorID:   ownerID,
		agentKind: "coder",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: turnID, Role: "assistant", State: domain.TurnStateWaiting, TurnOrder: 3, Revision: 1, StartedAt: "2026-08-23T00:00:00Z"},
			},
		},
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		status: turnStatus{
			State:  domain.TurnStateWaiting,
			TurnID: turnID,
		},
	}
	a.setActiveTurnRef(turnID)
	a.setActiveTurnOrder(3)
	return a
}

// TestHandleWorkflowPauseAll_GuardNoWorkflow verifies the handler rejects when
// no workflow is active, returning an error and producing no side effects.
func TestHandleWorkflowPauseAll_GuardNoWorkflow(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	const ownerID = "owner-actor-1"
	ctx := makeWorkflowPauseCtx(t, nil, nil)
	a := &Actor{
		actorID:    ownerID,
		agentKind:  "coder",
		RawSession: gen.RawSession{},
		status:     turnStatus{State: domain.TurnStateWaiting},
	}
	_, err := a.handleWorkflowPauseAll(ctx, domain.AgentWorkflowPauseAllReq{})
	if err == nil || !strings.Contains(err.Error(), "no active workflow") {
		t.Fatalf("expected no-active-workflow error, got %v", err)
	}
}

// TestHandleWorkflowPauseAll_GuardNotWaiting verifies the handler rejects when
// a workflow is active but the owner's turn is not waiting. It must not pause
// children or mutate the owner record.
func TestHandleWorkflowPauseAll_GuardNotWaiting(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	const (
		ownerID = "owner-actor-1"
		turnID  = "turn-running"
	)
	ctx := makeWorkflowPauseCtx(t, nil, nil)
	a := &Actor{
		actorID:   ownerID,
		agentKind: "coder",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: turnID, Role: "assistant", State: domain.TurnStateRunning, TurnOrder: 2, Revision: 1},
			},
		},
		RawSession: gen.RawSession{ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"}},
		status:     turnStatus{State: domain.TurnStateRunning, TurnID: turnID},
	}
	a.setActiveTurnRef(turnID)
	a.setActiveTurnOrder(2)
	_, err := a.handleWorkflowPauseAll(ctx, domain.AgentWorkflowPauseAllReq{})
	if err == nil || !strings.Contains(err.Error(), "want") {
		t.Fatalf("expected not-waiting error, got %v", err)
	}
	if rec := findTurn(a, turnID); rec == nil || rec.State != domain.TurnStateRunning {
		t.Fatalf("turn record mutated unexpectedly: %+v", rec)
	}
}

// TestHandleWorkflowPauseAll_PausesChildrenAndOwner verifies the happy path:
// only children owned by this owner and not being deleted are dispatched
// turn_pause; the owner waiting turn transitions to paused/user; status mirrors
// the pause; counts are returned.
func TestHandleWorkflowPauseAll_PausesChildrenAndOwner(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	const (
		ownerID = "owner-actor-1"
		turnID  = "turn-wait"
	)
	seq := id.NewCanonical(1, 0, func() uint64 { return 1 })
	childAID := seq.Next().String()
	otherChildAID := seq.Next().String()
	deletingChildAID := seq.Next().String()

	var paused []string
	childRefs := map[string]ref.Ref{
		childAID: testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
			if callID == "turn_pause" {
				paused = append(paused, "child")
			}
			return nil
		}),
		otherChildAID: testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
			if callID == "turn_pause" {
				paused = append(paused, "other")
			}
			return nil
		}),
		deletingChildAID: testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
			if callID == "turn_pause" {
				paused = append(paused, "deleting")
			}
			return nil
		}),
	}

	agents := []domain.AgentRef{
		{ID: "Worker#0001", ActorID: childAID, ParentAgentID: ownerID, LifecycleScope: "workflow"},
		{ID: "Worker#0002", ActorID: otherChildAID, ParentAgentID: ownerID, LifecycleScope: "workflow", DeletionStatus: "deleting"},
		{ID: "Stranger#0001", ActorID: deletingChildAID, ParentAgentID: "someone-else", LifecycleScope: "workflow"},
	}
	ctx := makeWorkflowPauseCtx(t, agents, childRefs)
	a := newWaitingOwner(t, ownerID, turnID)

	resp, err := a.handleWorkflowPauseAll(ctx, domain.AgentWorkflowPauseAllReq{})
	if err != nil {
		t.Fatalf("handleWorkflowPauseAll: %v", err)
	}
	if resp.PausedCount != 1 || resp.SkippedCount != 2 {
		t.Fatalf("resp = %+v, want PausedCount=1 SkippedCount=2", resp)
	}

	if len(paused) != 1 || paused[0] != "child" {
		t.Fatalf("paused children = %v, want [child]", paused)
	}

	rec := findTurn(a, turnID)
	if rec == nil || rec.State != domain.TurnStatePaused || rec.PauseReason != domain.PauseReasonUser || rec.Revision != 2 {
		t.Fatalf("owner turn record = %+v, want paused/user rev=2", rec)
	}
	if a.status.State != domain.TurnStatePaused || a.status.PauseKind != domain.PauseReasonUser {
		t.Fatalf("status = %+v, want paused/user", a.status)
	}
}

// TestHandleWorkflowPauseAll_IdempotentSecondCall verifies a second call after
// the owner is already paused returns an error and does not dispatch any further
// turn_pause calls.
func TestHandleWorkflowPauseAll_IdempotentSecondCall(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	const (
		ownerID = "owner-actor-1"
		turnID  = "turn-wait"
	)
	seq := id.NewCanonical(1, 0, func() uint64 { return 1 })
	childAID := seq.Next().String()
	var paused int
	childRefs := map[string]ref.Ref{
		childAID: testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
			if callID == "turn_pause" {
				paused++
			}
			return nil
		}),
	}
	agents := []domain.AgentRef{
		{ID: "Worker#0001", ActorID: childAID, ParentAgentID: ownerID, LifecycleScope: "workflow"},
	}
	ctx := makeWorkflowPauseCtx(t, agents, childRefs)
	a := newWaitingOwner(t, ownerID, turnID)

	if _, err := a.handleWorkflowPauseAll(ctx, domain.AgentWorkflowPauseAllReq{}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	firstCount := paused

	_, err := a.handleWorkflowPauseAll(ctx, domain.AgentWorkflowPauseAllReq{})
	if err == nil || !strings.Contains(err.Error(), "want") {
		t.Fatalf("expected second call to fail with waiting guard, got %v", err)
	}
	if paused != firstCount {
		t.Fatalf("second call dispatched more pauses: paused=%d, want %d", paused, firstCount)
	}
}
