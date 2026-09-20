package agent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// makeAgentPauseCtx returns a test context with a fake workspace that reports
// the supplied agents, and fake child refs resolvable by ActorID via LookupIDFn.
// Reuses the same pattern as makeWorkflowPauseCtx.
func makeAgentPauseCtx(t *testing.T, agents []domain.AgentRef, childRefs map[string]ref.Ref) *testutil.FakeCtx {
	t.Helper()
	return makeWorkflowPauseCtx(t, agents, childRefs)
}

// ── Branch 1: turn engine running ──────────────────────────────────────────

// TestHandleAgentPause_EngineRunningDelegatesToTurnPause verifies that when a
// turn engine is running, agent_pause delegates to handleTurnPause (which
// sets the engine's pauseRequested flag) and returns Sent=true.
func TestHandleAgentPause_EngineRunningDelegatesToTurnPause(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		status:    turnStatus{State: domain.TurnStateRunning},
	}
	// Inject a minimal fake turn engine so activeTurnEngine() returns non-nil.
	te := &turnEngine{}
	a.turnEngineStore(te)

	resp, err := a.handleAgentPause(ctx, gen.AgentPauseReq{})
	if err != nil {
		t.Fatalf("handleAgentPause: %v", err)
	}
	if !resp.Sent {
		t.Fatal("resp.Sent = false, want true")
	}
	te.mu.Lock()
	paused := te.pauseRequested
	te.mu.Unlock()
	if !paused {
		t.Fatal("engine.pauseRequested = false, want true (handleTurnPause was not called)")
	}
}

// TestHandleAgentPause_EngineRunningWithInteractionReturnsSentTrue verifies
// that when the turn engine is active but blocked on a pending interaction,
// handleAgentPause still returns Sent=true. handleTurnPause is a no-op for
// interaction-blocked turns (it returns nil early without setting the flag),
// but agent_pause still reports success.
func TestHandleAgentPause_EngineRunningWithInteractionReturnsSentTrue(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		actorID:            "agent-1",
		agentKind:          "coder",
		status:             turnStatus{State: domain.TurnStateRunning},
		pendingInteraction: &pendingInteractionState{Type: "ask_user"},
	}
	te := &turnEngine{}
	a.turnEngineStore(te)

	resp, err := a.handleAgentPause(ctx, gen.AgentPauseReq{})
	if err != nil {
		t.Fatalf("handleAgentPause: %v", err)
	}
	if !resp.Sent {
		t.Fatal("resp.Sent = false, want true (handleTurnPause is a no-op but returns nil)")
	}
	// handleTurnPause should have returned early; engine should NOT be paused.
	te.mu.Lock()
	paused := te.pauseRequested
	te.mu.Unlock()
	if paused {
		t.Fatal("engine.pauseRequested = true, want false (pause should be skipped for interaction)")
	}
}

// ── Branch 2: workflow-active waiting ──────────────────────────────────────

// TestHandleAgentPause_WorkflowWaitingCascadesPause verifies that when the
// agent is a workflow owner with a waiting turn, agent_pause cascades to
// children via handleWorkflowPauseAll and returns Sent=true.
func TestHandleAgentPause_WorkflowWaitingCascadesPause(t *testing.T) {
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
	ctx := makeAgentPauseCtx(t, agents, childRefs)
	a := newWaitingOwner(t, ownerID, turnID)

	resp, err := a.handleAgentPause(ctx, gen.AgentPauseReq{})
	if err != nil {
		t.Fatalf("handleAgentPause: %v", err)
	}
	if !resp.Sent {
		t.Fatal("resp.Sent = false, want true")
	}
	if paused != 1 {
		t.Fatalf("child pause count = %d, want 1", paused)
	}
	// Owner must be paused.
	if a.status.State != domain.TurnStatePaused {
		t.Fatalf("owner state = %q, want %q", a.status.State, domain.TurnStatePaused)
	}
}

// TestHandleAgentPause_WorkflowWaitingNoChildren verifies that the cascade
// succeeds even when there are no children, returning Sent=true with the
// owner transitioned to paused.
func TestHandleAgentPause_WorkflowWaitingNoChildren(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	const (
		ownerID = "owner-actor-1"
		turnID  = "turn-wait"
	)
	ctx := makeAgentPauseCtx(t, nil, nil)
	a := newWaitingOwner(t, ownerID, turnID)

	resp, err := a.handleAgentPause(ctx, gen.AgentPauseReq{})
	if err != nil {
		t.Fatalf("handleAgentPause: %v", err)
	}
	if !resp.Sent {
		t.Fatal("resp.Sent = false, want true")
	}
	if a.status.State != domain.TurnStatePaused {
		t.Fatalf("owner state = %q, want %q", a.status.State, domain.TurnStatePaused)
	}
}

// ── Branch 3: idempotent no-op (other states) ──────────────────────────────

// TestHandleAgentPause_IdempotentOnNonPausableState verifies that calling
// agent_pause in a state that is neither engine-running nor
// workflow-active-waiting returns Sent=false with no error and no mutation.
func TestHandleAgentPause_IdempotentOnNonPausableState(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.AnonCtx(testutil.GenActorID())
	tests := []struct {
		name  string
		state string
		wf    bool
	}{
		{"no workflow, completed turn", domain.TurnStateCompleted, false},
		{"no workflow, running state (anomaly)", domain.TurnStateRunning, false},
		{"workflow active but running (engine nil)", domain.TurnStateRunning, true},
		{"workflow active but already paused", domain.TurnStatePaused, true},
		{"workflow active, failed state", domain.TurnStateFailed, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Actor{
				actorID:   "agent-1",
				agentKind: "coder",
				status:    turnStatus{State: tc.state},
			}
			if tc.wf {
				a.RawSession = gen.RawSession{
					ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
				}
			}
			beforeState := a.status.State
			resp, err := a.handleAgentPause(ctx, gen.AgentPauseReq{})
			if err != nil {
				t.Fatalf("handleAgentPause: %v (expected nil error for idempotent no-op)", err)
			}
			if resp.Sent {
				t.Fatal("resp.Sent = true, want false for idempotent no-op")
			}
			if a.status.State != beforeState {
				t.Fatalf("status.State mutated: before=%q after=%q", beforeState, a.status.State)
			}
		})
	}
}

// TestHandleAgentPause_BranchOrderingEngineBeforeWorkflow verifies that when
// BOTH a turn engine is running AND a workflow is active with a waiting state,
// branch 1 (engine running) takes precedence. This guards against a
// workflow-active owner in an inconsistent running+waiting state being
// routed to the workflow pause cascade instead of the user-level pause.
func TestHandleAgentPause_BranchOrderingEngineBeforeWorkflow(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "owner-1",
		agentKind: "coder",
		status:    turnStatus{State: domain.TurnStateWaiting},
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	te := &turnEngine{}
	a.turnEngineStore(te)

	resp, err := a.handleAgentPause(ctx, gen.AgentPauseReq{})
	if err != nil {
		t.Fatalf("handleAgentPause: %v", err)
	}
	if !resp.Sent {
		t.Fatal("resp.Sent = false, want true")
	}
	// Branch 1 should have fired handleTurnPause.
	te.mu.Lock()
	paused := te.pauseRequested
	te.mu.Unlock()
	if !paused {
		t.Fatal("engine.pauseRequested = false, want true (branch 1 engine should win over branch 2)")
	}
}

// ── agent_resume ───────────────────────────────────────────────────────────

// TestHandleAgentResume_NoPausedTurnReturnsError verifies that agent_resume
// delegates to handleTurnResume, which returns an error (wrapped) when there
// is no paused turn to resume.
func TestHandleAgentResume_NoPausedTurnReturnsError(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		status:    turnStatus{State: domain.TurnStateRunning},
	}
	_, err := a.handleAgentResume(ctx, gen.AgentResumeReq{})
	if err == nil {
		t.Fatal("handleAgentResume: expected error for no paused turn, got nil")
	}
	if !strings.Contains(err.Error(), "agent.agent_resume") {
		t.Fatalf("error should wrap agent.agent_resume prefix, got: %v", err)
	}
}

// TestHandleAgentResume_WorkflowOwnerCascadesResume verifies that agent_resume
// on a workflow owner cascades turn_resume to children via the
// resumeChildAgents lazy-load path inside handleTurnResume (see
// agent_workflow_waiting_recovery_test.go:282 behavior contract).
func TestHandleAgentResume_WorkflowOwnerCascadesResume(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	selfID := testutil.GenActorID()
	projectID := testutil.GenActorID()
	childAID := testutil.GenActorID()

	var mu sync.Mutex
	var ops []string

	childRef := testutil.NewFakeRef(childAID, func(callID string, _ any) any {
		if callID == "turn_resume" {
			mu.Lock()
			ops = append(ops, "turn_resume")
			mu.Unlock()
		}
		return nil
	})

	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		switch callID {
		case "workspace.list_agents":
			return gen.AgentRefListResp{Items: []gen.AgentRef{{
				ID:        "Worker#0001",
				ActorID:   childAID.String(),
				LoadState: "unloaded",
			}}}
		case "workspace.load_agent":
			mu.Lock()
			ops = append(ops, "load_agent")
			mu.Unlock()
			return gen.AgentRef{ID: "Worker#0001", ActorID: childAID.String(), LoadState: "loaded"}
		}
		return nil
	})

	ctx := testutil.HumanCtx(selfID)
	ctx.ParentRef = testutil.NewFakeRef(projectID, nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == childAID.String() {
			return childRef, true
		}
		return nil, false
	}

	a := &Actor{
		actorID: selfID.String(),
		child:   childState{Mode: true},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				// Paused mid-run: NO CompletedAt — the crash-recovery path.
				{ID: "t2", Role: "assistant", State: "paused", TurnOrder: 2},
			},
		},
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	a.ActiveTurnRef = "t2"
	a.status.State = domain.TurnStatePaused
	a.status.PauseKind = domain.PauseReasonUser
	a.status.TurnID = "t2"
	a.setActiveTurnOrder(2)

	resp, err := a.handleAgentResume(ctx, gen.AgentResumeReq{})
	if err != nil {
		t.Fatalf("handleAgentResume: %v (child cascade should still be dispatched even if owner resume hits an edge)", err)
	}
	if !resp.Sent {
		t.Fatal("resp.Sent = false, want true")
	}

	// resumeChildAgents runs on a background goroutine; wait for the cascade.
	mu.Lock()
	for i := 0; i < 200 && len(ops) < 2; i++ {
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
	}
	want := []string{"load_agent", "turn_resume"}
	if len(ops) != len(want) {
		t.Fatalf("ops = %v, want %v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Fatalf("ops = %v, want %v", ops, want)
		}
	}
	mu.Unlock()
}
