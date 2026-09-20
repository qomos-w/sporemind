package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleRun_AbortsWhenActiveTurnClearedBeforeStart reproduces the
// "stop button did nothing" race: handleTurnCancel commits a cancelled turn
// and clears ActiveTurnRef in the window after agent.run was queued on the
// agent.exec loop but before agent.exec actually ran handleRun. handleRun must
// abort instead of fabricating a new turn ID and running the turn anyway.
func TestHandleRun_AbortsWhenActiveTurnClearedBeforeStart(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID: "agent-1",
		// ActiveTurnRef empty simulates the voided-cancel state.
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "assistant", State: "cancelled"},
			},
			ActiveHead: 0,
		},
	}

	if err := a.handleRun(ctx, domain.TurnStartReq{TurnID: "turn-1"}); err != nil {
		t.Fatalf("handleRun returned error: %v", err)
	}

	if a.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef = %q, want empty (turn must not start after voided cancel)", a.ActiveTurnRef)
	}
	if a.activeTurnEngine() != nil {
		t.Errorf("turn engine created despite voided cancel")
	}
	if len(a.Session.Turns) != 1 {
		t.Errorf("Session.Turns has %d entries, want 1 (no new turn appended)", len(a.Session.Turns))
	}
}

// TestCancel_SetsAcceptingInjectsFalse verifies that cancel() flips
// acceptingInjects to false. Without this, a user message submitted during the
// cancel race window (engine pointer not yet cleared) is routed into the dying
// engine via queuePendingSubmit and silently lost, leaving the agent stuck.
func TestCancel_SetsAcceptingInjectsFalse(t *testing.T) {
	e := &turnEngine{
		done:             make(chan struct{}),
		runExited:        make(chan struct{}),
		acceptingInjects: true,
	}
	if !e.AcceptingInjects() {
		t.Fatal("precondition: engine should accept injects before cancel")
	}
	e.cancel()
	if !e.isCancelled() {
		t.Fatal("cancel() did not set cancelled flag")
	}
	if e.AcceptingInjects() {
		t.Fatal("AcceptingInjects() = true after cancel, want false (cancelled engine must not swallow injects)")
	}
}

// TestChatSubmit_CancelledEngineStartsNewTurn reproduces the "stop then send"
// stuck bug: handleTurnCancel cleared ActiveTurnRef but the turnEngine pointer
// is still non-nil in the brief handoff before handleRun clears it. Before the
// fix the new message fell into the generic "turn
// finishing" orphan path, which returns an empty TurnActorID and relies on
// maybeAutoStartTurn — but the cancelled turn never calls it, so the UI hangs.
// After the fix, a cancelled engine triggers an explicit start_turn and returns
// a non-empty TurnActorID.
func TestChatSubmit_CancelledEngineStartsNewTurn(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	eng := &turnEngine{
		done:             make(chan struct{}),
		runExited:        make(chan struct{}),
		acceptingInjects: true,
	}
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		// ActiveTurnRef already cleared by handleTurnCancel; only the engine
		// pointer lingers (the race window).
		ActiveTurnRef: "",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "assistant", State: "cancelled"},
			},
			ActiveHead: 0,
		},
		RawSession:    domain.RawSession{NextIdx: 1},
		snapshotReady: true,
	}
	a.turnEngineStore(eng)
	eng.cancel() // cancelled engine, pointer not yet cleared

	resp, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "next message"})
	if err != nil {
		t.Fatalf("handleChatSubmit error: %v", err)
	}
	if resp.TurnActorID == "" {
		t.Fatal("TurnActorID empty for cancelled engine: message would be orphaned (UI stuck)")
	}

	// Contrast: a normally-finishing engine (not cancelled) must keep the old
	// behaviour — return without TurnActorID and defer to maybeAutoStartTurn.
	engFinishing := &turnEngine{
		done:             make(chan struct{}),
		runExited:        make(chan struct{}),
		acceptingInjects: false,
	}
	a.turnEngineStore(engFinishing)
	resp2, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "another message"})
	if err != nil {
		t.Fatalf("handleChatSubmit (finishing) error: %v", err)
	}
	if resp2.TurnActorID != "" {
		t.Fatalf("TurnActorID = %q for finishing engine, want empty (defer to maybeAutoStartTurn)", resp2.TurnActorID)
	}
}

func TestStopThenSubmit_UnblocksTurnIOAndStartsFreshTurn(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	var statusMu sync.Mutex
	var lastStatus gen.WorkspaceAgentStatusUpdateReq
	workspaceRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID != "workspace.agent_status_update" {
			return nil
		}
		statusMu.Lock()
		defer statusMu.Unlock()
		switch v := payload.(type) {
		case gen.WorkspaceAgentStatusUpdateReq:
			lastStatus = v
		case *gen.WorkspaceAgentStatusUpdateReq:
			if v != nil {
				lastStatus = *v
			}
		}
		return nil
	})
	planner := newBlockingPlanner()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return workspaceRef, true
		}
		return nil, false
	}

	eng := &turnEngine{
		done:             make(chan struct{}),
		runExited:        make(chan struct{}),
		turnID:           "turn-1",
		startedAt:        time.Now(),
		logger:           newNopActorLogger(),
		openStepEvents:   make(map[string][]domain.StepEvent),
		acceptingInjects: true,
	}
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-1", Role: "assistant", State: "running", TurnOrder: 2},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{NextIdx: 1, NextSeq: 1, NextTurnOrder: 3},
		status: turnStatus{
			TurnID:         "turn-1",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
	}
	a.turnEngineStore(eng)

	ioDone := make(chan toolExecutionResult, 1)
	go func() {
		call := pendingToolCall{
			ID: "tool-1", LLMName: "shell_exec", CallableID: "shell.exec", ServiceName: "shell",
			Input: `{"Command":"grep","Args":["needle","a.txt"]}`,
		}
		result := eng.runOneCall(ctx, planner, call, map[string]ref.Ref{
			"project": testutil.NewFakeRef(testutil.GenActorID(), nil),
			"shell":   testutil.NewFakeRef(testutil.GenActorID(), nil),
		}, "step-1")
		eng.mu.Lock()
		eng.loopState = LoopCancelled
		eng.mu.Unlock()
		ioDone <- result
		close(eng.runExited)
	}()
	started := waitPlannerCall(t, planner, "project.grep")

	cancelDone := make(chan error, 1)
	go func() { cancelDone <- a.handleTurnCancel(ctx) }()
	select {
	case err := <-cancelDone:
		if err != nil {
			t.Fatalf("handleTurnCancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop remained blocked behind cancellable turn I/O")
	}
	select {
	case <-started.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel the shell interception context")
	}
	select {
	case result := <-ioDone:
		if !result.isErr {
			t.Fatal("cancelled shell interception returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("old engine I/O did not exit")
	}

	if a.ActiveTurnRef != "" || a.status.State != "cancelled" {
		t.Fatalf("backend terminal state = (%q, %q), want empty/cancelled", a.ActiveTurnRef, a.status.State)
	}
	snapshot := a.snapshot.Load()
	if snapshot == nil || len(snapshot.turns) == 0 || snapshot.turns[len(snapshot.turns)-1].State != "cancelled" {
		t.Fatal("terminal snapshot does not contain the cancelled turn")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		statusMu.Lock()
		update := lastStatus
		statusMu.Unlock()
		if update.State == "cancelled" {
			if update.ActiveTurnRef != "" {
				t.Fatalf("workspace status ActiveTurnRef = %q, want empty", update.ActiveTurnRef)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancelled workspace status was not sent; last = %q", update.State)
		}
		time.Sleep(10 * time.Millisecond)
	}

	resp, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "next message"})
	if err != nil {
		t.Fatalf("submit after stop: %v", err)
	}
	if resp.TurnActorID == "" {
		t.Fatal("submit after stop did not schedule a fresh turn")
	}
	if len(a.pendingSubmits) != 0 {
		t.Fatalf("submit after stop was left pending: %+v", a.pendingSubmits)
	}
}

// TestHandleTurnCancel_ResetsMemorySleepingWhenDreamerCancelled verifies that
// cancelling a turn while a memory dreamer is in-flight resets memorySleeping.
// The dreamer self-destructs on turn_cancel without sending explore_complete,
// so finishMemorySleep (the only normal reset path) never fires. Without the
// reset, memorySleeping stays true and blocks maybeAutoStartTurn forever — the
// user cannot send any new message after manually stopping a turn.
func TestHandleTurnCancel_ResetsMemorySleepingWhenDreamerCancelled(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	dreamerRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a := &Actor{
		actorID:       "agent-1",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-1", Role: "assistant", State: "running", TurnOrder: 2},
			},
			ActiveHead: 1,
		},
		RawSession:      domain.RawSession{NextIdx: 1, NextSeq: 1, NextTurnOrder: 3},
		memorySleeping:  true,
		memorySleepTurn: "turn-1",
		activeChildren:  map[string]ref.Ref{"memory-sleep": dreamerRef},
		snapshotReady:   true,
	}

	if err := a.handleTurnCancel(ctx); err != nil {
		t.Fatalf("handleTurnCancel: %v", err)
	}

	if a.memorySleeping {
		t.Fatal("memorySleeping = true after cancel, want false (dreamer cancelled without explore_complete; finishMemorySleep will never fire)")
	}
	if a.memorySleepTurn != "" {
		t.Fatalf("memorySleepTurn = %q, want empty after cancel", a.memorySleepTurn)
	}
	if _, ok := a.activeChildren["memory-sleep"]; ok {
		t.Fatal("memory-sleep child still tracked after cancel")
	}
}
