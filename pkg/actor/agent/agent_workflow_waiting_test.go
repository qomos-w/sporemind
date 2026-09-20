package agent

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// newWaitingTestActor builds an actor whose active assistant turn "turn-1" is
// mid-flight, with an optional active workflow.
func newWaitingTestActor(workflowActive bool) *Actor {
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-1", Role: "assistant", State: "running", TurnOrder: 2, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)},
			},
			ActiveHead: 0,
		},
		RawSession:    domain.RawSession{NextIdx: 2, NextSeq: 1, NextTurnOrder: 3},
		status:        turnStatus{TurnID: "turn-1", State: "running"},
		snapshotReady: true,
	}
	if workflowActive {
		a.RawSession.ActiveWorkflow = &gen.ActiveWorkflow{MapCardID: "map-1"}
	}
	return a
}

// TestHandleTurnComplete_WorkflowOwnerParksWaiting verifies that a workflow
// map owner's successfully finished turn lands in the waiting state: the turn
// record carries State=waiting plus CompletedAt, while ActiveTurnRef,
// status.TurnID and status.State keep pointing at the parked turn so
// maybeAutoStartTurn cannot pick up queued work behind the owner's back.
func TestHandleTurnComplete_WorkflowOwnerParksWaiting(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := newWaitingTestActor(true)
	defer a.stopStatusNotifications()

	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-1",
			Role:        "assistant",
			State:       "completed",
			StartedAt:   a.Session.Turns[1].StartedAt,
			CompletedAt: completedAt,
		},
	}); err != nil {
		t.Fatalf("handleTurnComplete: %v", err)
	}

	rec := findTurn(a, "turn-1")
	if rec == nil {
		t.Fatal("turn record missing")
	}
	if rec.State != domain.TurnStateWaiting {
		t.Fatalf("record state = %q, want %q", rec.State, domain.TurnStateWaiting)
	}
	if rec.CompletedAt == "" {
		t.Fatal("waiting record must carry CompletedAt (lastTurnCompletedAt badge)")
	}
	if _, ok := lastLifecycleEvent(ctx, "turn-1", domain.TurnLifecycleWaiting); !ok {
		t.Fatal("expected a turn.waiting lifecycle event")
	}
	if got := a.getActiveTurnRef(); got != "turn-1" {
		t.Fatalf("ActiveTurnRef = %q, want preserved %q", got, "turn-1")
	}
	if a.status.TurnID != "turn-1" {
		t.Fatalf("status.TurnID = %q, want preserved %q", a.status.TurnID, "turn-1")
	}
	if a.status.State != domain.TurnStateWaiting {
		t.Fatalf("status.State = %q, want %q", a.status.State, domain.TurnStateWaiting)
	}

	// maybeAutoStartTurn must stay blocked while the owner parks in waiting.
	head := int(a.Session.ActiveHead)
	for i := head; i < len(a.Session.Turns); i++ {
		a.Session.Turns[i].Role = "assistant"
	}
	a.maybeAutoStartTurn(ctx)
	if a.getActiveTurnRef() != "turn-1" {
		t.Fatalf("waiting owner auto-started work: ActiveTurnRef = %q", a.getActiveTurnRef())
	}
}

// TestHandleTurnComplete_WorkflowOwnerFailedStaysFailed pins that failed and
// cancelled terminal states are untouched by the waiting parking: they remain
// terminal and release the active ref like before.
func TestHandleTurnComplete_WorkflowOwnerFailedStaysFailed(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := newWaitingTestActor(true)
	defer a.stopStatusNotifications()

	if err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:    "turn-1",
			Role:  "assistant",
			State: domain.TurnStateFailed,
			Error: "boom",
		},
	}); err != nil {
		t.Fatalf("handleTurnComplete: %v", err)
	}

	rec := findTurn(a, "turn-1")
	if rec.State != domain.TurnStateFailed {
		t.Fatalf("record state = %q, want failed", rec.State)
	}
	if got := a.getActiveTurnRef(); got != "" {
		t.Fatalf("ActiveTurnRef = %q, want cleared after failure", got)
	}
	if a.status.TurnID != "" {
		t.Fatalf("status.TurnID = %q, want cleared after failure", a.status.TurnID)
	}
}

// TestHandleTurnComplete_NonWorkflowStillCompletes guards the non-owner path:
// without an active workflow the completion lifecycle is unchanged
// (turn.completed, ref/status cleared) so completion badges keep working.
func TestHandleTurnComplete_NonWorkflowStillCompletes(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := newWaitingTestActor(false)
	defer a.stopStatusNotifications()

	if err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-1",
			Role:        "assistant",
			State:       "completed",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		},
	}); err != nil {
		t.Fatalf("handleTurnComplete: %v", err)
	}

	rec := findTurn(a, "turn-1")
	if rec.State != domain.TurnStateCompleted {
		t.Fatalf("record state = %q, want completed", rec.State)
	}
	if _, ok := lastLifecycleEvent(ctx, "turn-1", domain.TurnLifecycleCompleted); !ok {
		t.Fatal("expected a turn.completed lifecycle event")
	}
	if got := a.getActiveTurnRef(); got != "" {
		t.Fatalf("ActiveTurnRef = %q, want cleared", got)
	}
	if a.status.TurnID != "" {
		t.Fatalf("status.TurnID = %q, want cleared", a.status.TurnID)
	}
	if a.lastTurnCompletedAt() == "" {
		t.Fatal("lastTurnCompletedAt empty after normal completion")
	}
}

// TestHandleTurnComplete_ChildModeNeverWaits proves the workflowActive guard
// naturally excludes child-mode agents: children never own an ActiveWorkflow,
// so even with child.Mode set their completions go through turn.completed and
// release the ref (the explore_complete handoff depends on this).
func TestHandleTurnComplete_ChildModeNeverWaits(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := newWaitingTestActor(false)
	a.child = childState{Mode: true}
	defer a.stopStatusNotifications()

	if err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-1",
			Role:        "assistant",
			State:       "completed",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		},
	}); err != nil {
		t.Fatalf("handleTurnComplete: %v", err)
	}

	rec := findTurn(a, "turn-1")
	if rec.State != domain.TurnStateCompleted {
		t.Fatalf("child-mode record state = %q, want completed", rec.State)
	}
	if got := a.getActiveTurnRef(); got != "" {
		t.Fatalf("child-mode ActiveTurnRef = %q, want cleared", got)
	}
}

// TestChatSubmitFinalizesWaitingTurnBeforeNewTurn verifies the updater path:
// when ActiveTurnRef points at a waiting turn, chat.submit finalizes it
// (waiting→completed), clears the ref/status, and proceeds to start a fresh
// turn instead of being blocked by the parked owner.
func TestChatSubmitFinalizesWaitingTurnBeforeNewTurn(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
			NextIdx:        2,
			NextSeq:        1,
			NextTurnOrder:  3,
		},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-1", Role: "assistant", State: domain.TurnStateWaiting, TurnOrder: 2, CompletedAt: time.Now().UTC().Format(time.RFC3339Nano)},
			},
			ActiveHead: 0,
		},
		status:        turnStatus{TurnID: "turn-1", State: domain.TurnStateWaiting},
		snapshotReady: true,
	}

	resp, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "review done, next frontier task"})
	if err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}
	if resp.MessageID == "" || resp.TurnActorID == "" {
		t.Fatalf("resp incomplete: %+v — updater message must not be swallowed", resp)
	}

	rec := findTurn(a, "turn-1")
	if rec.State != domain.TurnStateCompleted {
		t.Fatalf("waiting record state = %q, want finalized completed", rec.State)
	}
	if got := a.getActiveTurnRef(); got != "" {
		t.Fatalf("ActiveTurnRef = %q, want cleared after finalize", got)
	}
	if a.status.TurnID != "" {
		t.Fatalf("status.TurnID = %q, want cleared after finalize", a.status.TurnID)
	}
	if len(a.Session.Turns) < 3 {
		t.Fatalf("expected the new user turn appended, turns = %d", len(a.Session.Turns))
	}
	last := a.Session.Turns[len(a.Session.Turns)-1]
	if last.Role != "user" || last.UserInput != "review done, next frontier task" {
		t.Fatalf("last turn = %+v, want the submitted user message", last)
	}
}

// TestChatSubmitDuringTeardownOfWaitingOwner reproduces the 2026-08-28
// Render Chonk stall: the workflow owner's turn was parked waiting inside
// handleTurnComplete, but the turn engine pointer is only cleared after
// handleTurnComplete returns on agent.exec. A chat.submit arriving in that
// window saw the stale non-accepting engine and queued its user turn as
// "next cycle" — but the parked ref suppresses maybeAutoStartTurn forever,
// orphaning the message. The submit must finalize the waiting turn and
// start a fresh turn instead.
func TestChatSubmitDuringTeardownOfWaitingOwner(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
			NextIdx:        2,
			NextSeq:        1,
			NextTurnOrder:  3,
		},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-1", Role: "assistant", State: domain.TurnStateWaiting, TurnOrder: 2, CompletedAt: time.Now().UTC().Format(time.RFC3339Nano)},
			},
			ActiveHead: 0,
		},
		status:        turnStatus{TurnID: "turn-1", State: domain.TurnStateWaiting},
		snapshotReady: true,
	}
	// Stale engine pointer: run() exited (not accepting injects) and the
	// turn was never cancelled — exactly the teardown window state.
	a.turnEngineStore(&turnEngine{done: make(chan struct{}), runExited: make(chan struct{})})

	resp, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "worker ready for review"})
	if err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}
	if resp.MessageID == "" || resp.TurnActorID == "" {
		t.Fatalf("resp incomplete: %+v — submit must not be swallowed by the teardown window", resp)
	}

	rec := findTurn(a, "turn-1")
	if rec.State != domain.TurnStateCompleted {
		t.Fatalf("parked record state = %q, want finalized completed", rec.State)
	}
	if got := a.getActiveTurnRef(); got != "" {
		t.Fatalf("ActiveTurnRef = %q, want cleared after finalize", got)
	}
	if a.status.TurnID != "" {
		t.Fatalf("status.TurnID = %q, want cleared after finalize", a.status.TurnID)
	}
	last := a.Session.Turns[len(a.Session.Turns)-1]
	if last.Role != "user" || last.UserInput != "worker ready for review" {
		t.Fatalf("last turn = %+v, want the submitted user message", last)
	}
}

// TestChatSubmitStaleRunningRefStillCancelled keeps the pre-existing supersede
// behavior for non-waiting refs: when ActiveTurnRef points at a running turn
// with no inline engine, chat.submit still cancels it rather than leaving a
// stale ref behind. Only waiting records are finalized to completed.
func TestChatSubmitStaleRunningRefStillCancelled(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-running",
		RawSession:    domain.RawSession{NextIdx: 2, NextSeq: 1, NextTurnOrder: 3},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-running", Role: "assistant", State: domain.TurnStateRunning, TurnOrder: 2},
			},
			ActiveHead: 0,
		},
		status:        turnStatus{TurnID: "turn-running", State: domain.TurnStateRunning},
		snapshotReady: true,
	}

	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "supersede"}); err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}

	rec := findTurn(a, "turn-running")
	if rec.State != domain.TurnStateCancelled {
		t.Fatalf("stale running record state = %q, want cancelled (unchanged behavior)", rec.State)
	}
}

// TestWorkflowStopNormalizesWaitingTurns verifies workflow_stop flips leftover
// waiting records back to completed while clearing ActiveWorkflow, including
// releasing the active ref when it points at one of them.
func TestWorkflowStopNormalizesWaitingTurns(t *testing.T) {
	ctx := makeWorkflowCtx(t, nil, nil)
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-waiting",
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:workflow", Scope: "user"},
			{ID: "builtin:bundle:workflow-tools", Scope: "user"},
		},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed"},
				{ID: "turn-done", Role: "assistant", State: domain.TurnStateCompleted},
				{ID: "turn-waiting", Role: "assistant", State: domain.TurnStateWaiting, CompletedAt: time.Now().UTC().Format(time.RFC3339Nano)},
				{ID: "turn-failed", Role: "assistant", State: domain.TurnStateFailed},
			},
		},
		status: turnStatus{TurnID: "turn-waiting", State: domain.TurnStateWaiting},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if _, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{}); err != nil {
		t.Fatalf("handleWorkflowStop: %v", err)
	}

	waitingRec := findTurn(a, "turn-waiting")
	if waitingRec.State != domain.TurnStateCompleted {
		t.Fatalf("leftover waiting record = %q, want normalized completed", waitingRec.State)
	}
	if findTurn(a, "turn-done").State != domain.TurnStateCompleted {
		t.Fatal("terminal completed record was disturbed")
	}
	if findTurn(a, "turn-failed").State != domain.TurnStateFailed {
		t.Fatal("failed record must not be revived by normalization")
	}
	if got := a.getActiveTurnRef(); got != "" {
		t.Fatalf("ActiveTurnRef = %q, want released on stop", got)
	}
	if a.workflowActive() {
		t.Fatal("workflow must be inactive after stop")
	}
}
