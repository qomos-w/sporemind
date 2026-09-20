package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRequestPause_SetsFlag verifies requestPause sets the pauseRequested flag.
func TestRequestPause_SetsFlag(t *testing.T) {
	e := &turnEngine{
		done:        make(chan struct{}),
		resumeCh:    make(chan struct{}, 1),
		stepEventMu: sync.Mutex{},
	}
	e.requestPause()

	e.mu.Lock()
	got := e.pauseRequested
	e.mu.Unlock()
	if !got {
		t.Errorf("pauseRequested = false, want true after requestPause")
	}
}

// TestDispatchCanceledByPause verifies the pause-vs-cancel classification used
// by phaseDispatch: a context.Canceled surfacing from the dispatch stream while
// a pause was requested (and the engine was not cancelled) must classify as a
// pause so the main loop enters LoopPaused instead of failTurn's LoopCancelled.
func TestDispatchCanceledByPause(t *testing.T) {
	wrapped := fmt.Errorf("start: %w", context.Canceled)

	e := &turnEngine{
		done:        make(chan struct{}),
		resumeCh:    make(chan struct{}, 1),
		stepEventMu: sync.Mutex{},
	}
	if e.dispatchCanceledByPause(wrapped) {
		t.Errorf("dispatchCanceledByPause = true without requestPause; want false")
	}

	e.requestPause()
	if !e.dispatchCanceledByPause(wrapped) {
		t.Errorf("dispatchCanceledByPause = false after requestPause; want true")
	}
	// planner/pl cancellation surfaces as context.Canceled (translated
	// since gospore 9f6ec27); the bare invoke.ErrCallCancelled sentinel
	// survives only for an explicit Cancel without a ctx cause. Both must
	// classify as pause or the dispatch loop retries through the pause and
	// the turn keeps running.
	if !e.dispatchCanceledByPause(invoke.ErrCallCancelled) {
		t.Errorf("dispatchCanceledByPause = false for invoke.ErrCallCancelled after requestPause; want true")
	}
	if e.dispatchCanceledByPause(errPaused) {
		t.Errorf("dispatchCanceledByPause = true for non-cancel error; want false")
	}

	// A real stop (handleTurnCancel -> e.cancel) must win over pauseRequested.
	e.cancel()
	if e.dispatchCanceledByPause(wrapped) {
		t.Errorf("dispatchCanceledByPause = true after engine cancel; want false")
	}
}

// TestResumeUserPause_SendsSignal verifies resumeUserPause sends on resumeCh.
func TestResumeUserPause_SendsSignal(t *testing.T) {
	e := &turnEngine{
		done:     make(chan struct{}),
		resumeCh: make(chan struct{}, 1),
	}
	e.resumeUserPause(context.Background())

	select {
	case <-e.resumeCh:
		// success
	case <-time.After(100 * time.Millisecond):
		t.Errorf("resumeCh not signaled after resumeUserPause")
	}
}

// TestLoopPaused_String verifies the state string.
func TestLoopPaused_String(t *testing.T) {
	if LoopPaused.String() != "paused" {
		t.Errorf("LoopPaused.String() = %q, want %q", LoopPaused.String(), "paused")
	}
}

// TestHandleTurnPause_NoActiveTurn verifies that handleTurnPause returns an
// error when no turn engine is active.
func TestHandleTurnPause_NoActiveTurn(t *testing.T) {
	a := &Actor{}
	err := a.handleTurnPause(nil)
	if err == nil {
		t.Fatalf("expected error when no active turn engine")
	}
}

// TestHandleTurnPause_NoOpDuringInteraction verifies that pausing a turn
// blocked on a user interaction is a silent no-op (returns nil, never reaches
// the engine) so the pending question is not abandoned. This holds even with
// no live engine (crash-recovery path), where the interaction record survives
// in the mailbox.
func TestHandleTurnPause_NoOpDuringInteraction(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		ActiveTurnRef: "turn-1",
		pendingInteraction: &pendingInteractionState{
			TurnID: "turn-1",
			Type:   "ask_user",
		},
	}
	// No engine set: if the guard did not fire first, this would error with
	// "no active turn to pause". It must instead no-op.
	if err := a.handleTurnPause(ctx); err != nil {
		t.Fatalf("handleTurnPause during interaction should be a no-op, got error: %v", err)
	}
	if a.pendingInteraction == nil {
		t.Fatal("pendingInteraction must not be cleared by a pause")
	}
}

// TestHandleTurnResume_NoActiveTurnOrPausedRef verifies that handleTurnResume
// returns an error when there's neither an active engine nor a paused turn.
func TestHandleTurnResume_NoActiveTurnOrPausedRef(t *testing.T) {
	a := &Actor{}
	err := a.handleTurnResume(nil)
	if err == nil {
		t.Fatalf("expected error when nothing to resume")
	}
}

// TestHandleTurnResume_CrashRecoveryPausedTurn verifies the crash-recovery path
// of handleTurnResume: no active engine, paused turn in Session.Turns. Resume
// must continue the SAME turn (reuse its ID, flip it back to running) rather
// than spawning a new turn.
func TestHandleTurnResume_CrashRecoveryPausedTurn(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		ActiveTurnRef: "crashed-turn",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-1", Role: "user", State: "completed"},
				{ID: "crashed-turn", Role: "assistant", State: "paused"},
			},
			ActiveHead: 1,
		},
		RawSession: domain.RawSession{
			NextTurnOrder: 3,
		},
		status: turnStatus{
			State:  "paused",
			TurnID: "crashed-turn",
		},
	}

	err := a.handleTurnResume(ctx)
	if err != nil {
		t.Fatalf("handleTurnResume returned error: %v", err)
	}

	// ActiveTurnRef is kept as the SAME turn id — the paused turn continues
	// rather than being replaced by a new turn.
	wantTurn := "crashed-turn"
	if a.ActiveTurnRef != wantTurn {
		t.Errorf("ActiveTurnRef = %q, want %q (reused)", a.ActiveTurnRef, wantTurn)
	}
	if a.status.TurnID != wantTurn {
		t.Fatalf("status.TurnID = %q, want %q", a.status.TurnID, wantTurn)
	}
	if a.status.State != "running" {
		t.Fatalf("status.State = %q, want running", a.status.State)
	}
	// No new turn must be appended.
	if got := len(a.Session.Turns); got != 2 {
		t.Errorf("Session.Turns len = %d, want 2 (no new turn spawned)", got)
	}

	// The continued turn must be flipped back to running (NOT marked as a
	// terminal "resumed") and announced live so the UI flips the TurnTail
	// without waiting for a session summary.
	foundRunning := false
	for _, turn := range a.Session.Turns {
		if turn.ID == "crashed-turn" && turn.State == "running" {
			foundRunning = true
			break
		}
	}
	if !foundRunning {
		t.Fatalf("crashed-turn not flipped to running in Session.Turns")
	}
	foundEvent := false
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind != "turn" {
			continue
		}
		te, ok := ev.Payload.(domain.TurnEvent)
		if !ok {
			continue
		}
		if te.Kind == domain.TurnResumed && te.TurnID == "crashed-turn" {
			foundEvent = true
			if state, _ := te.Payload["state"].(string); state != "running" {
				t.Fatalf("turn.resumed payload state = %v, want running", te.Payload["state"])
			}
			break
		}
	}
	if !foundEvent {
		t.Fatalf("expected turn.resumed event for crashed-turn")
	}
}

// TestHandleTurnResume_CrashRecoveryEmptyActiveTurnRef verifies the fallback
// path: ActiveTurnRef is empty (e.g. cleared by a prior resume then crashed
// again) but Session.Turns still has a paused turn. Resume should find it.
func TestHandleTurnResume_CrashRecoveryEmptyActiveTurnRef(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		ActiveTurnRef: "",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-1", Role: "user", State: "completed"},
				{ID: "crashed-turn", Role: "assistant", State: "paused"},
			},
			ActiveHead: 1,
		},
		RawSession: domain.RawSession{
			NextTurnOrder: 3,
		},
		status: turnStatus{
			State:  "paused",
			TurnID: "crashed-turn",
		},
	}

	err := a.handleTurnResume(ctx)
	if err != nil {
		t.Fatalf("handleTurnResume returned error: %v", err)
	}

	// ActiveTurnRef is set to the SAME paused turn id — it continues.
	wantTurn := "crashed-turn"
	if a.ActiveTurnRef != wantTurn {
		t.Errorf("ActiveTurnRef = %q, want %q (reused)", a.ActiveTurnRef, wantTurn)
	}
	if a.status.State != "running" {
		t.Errorf("status.State = %q, want running", a.status.State)
	}
	// The paused turn should be flipped back to running.
	var found bool
	for _, turn := range a.Session.Turns {
		if turn.ID == "crashed-turn" && turn.State == "running" {
			found = true
		}
	}
	if !found {
		t.Errorf("crashed-turn not flipped to running in Session.Turns")
	}
}

// TestTakeSnapshot_UserPauseKind verifies that takeSnapshot propagates the
// user pause kind.
func TestTakeSnapshot_UserPauseKind(t *testing.T) {
	a := &Actor{
		status: turnStatus{
			State:     "paused",
			PauseKind: "user",
		},
	}
	a.takeSnapshot()
	if a.TurnPauseKind != "user" {
		t.Errorf("TurnPauseKind = %q, want user", a.TurnPauseKind)
	}
	if a.TurnState != "paused" {
		t.Errorf("TurnState = %q, want paused", a.TurnState)
	}
}

// TestTakeSnapshot_TaskPauseKindFallback verifies that when PauseKind is empty
// but Tasks exist, the task pause kind is still used (backward compat).
func TestTakeSnapshot_TaskPauseKindFallback(t *testing.T) {
	a := &Actor{
		status: turnStatus{
			State:     "paused",
			PauseKind: "",
		},
		RawSession: domain.RawSession{
			Tasks: []gen.TurnTask{{ID: "t1", Status: "pending"}},
		},
	}
	a.takeSnapshot()
	if a.TurnPauseKind != "task" {
		t.Errorf("TurnPauseKind = %q, want task (fallback)", a.TurnPauseKind)
	}
}

// TestApplyPendingUnitChange_SwitchesUnit verifies that the engine consumes a
// staged unit change and applies it to the next dispatch iteration.
func TestApplyPendingUnitChange_SwitchesUnit(t *testing.T) {
	oldUnit := domain.ModelUnit{Model: "old-model", Provider: "old-provider"}
	newUnit := domain.ModelUnit{Model: "new-model", Provider: "new-provider"}

	var changedUnit *domain.ModelUnit
	e := &turnEngine{
		startReq: domain.TurnStartReq{
			Input: domain.TurnInput{
				Unit: agentUnitPtr(oldUnit),
			},
		},
		// Simulate the real turn-start snapshot: a hard-locked primary slot.
		// The mid-turn switch must rebuild the slot as hard-locked so the new
		// model is pinned on dispatch.
		primarySlot: slotFromUnit(oldUnit),
		consumePendingUnitChange: func() *domain.ModelUnit {
			return &newUnit
		},
		onUnitChanged: func(unit domain.ModelUnit) {
			changedUnit = &unit
		},
	}

	e.applyPendingUnitChange()

	if e.startReq.Input.Unit == nil {
		t.Fatalf("startReq.Input.Unit is nil after applying pending change")
	}
	if e.startReq.Input.Unit.Model != newUnit.Model || e.startReq.Input.Unit.Provider != newUnit.Provider {
		t.Errorf("startReq.Input.Unit = %+v, want %+v", e.startReq.Input.Unit, newUnit)
	}
	// The primary slot is what resolveTargets actually reads to pin a unit; if
	// it lags behind startReq.Input.Unit, selectDispatchStream overrides the
	// new unit with the stale turn-start one and the switch is lost.
	if got := slotFirstUnit(e.primarySlot); got != newUnit {
		t.Errorf("primarySlot unit = %+v, want %+v (slot must stay in sync)", got, newUnit)
	}
	if !isUnitLockedSlot(e.primarySlot) {
		t.Errorf("primarySlot must be hard-locked after switch (unit-locked, no auto tail): %+v", e.primarySlot)
	}
	if tail := e.primarySlot.Candidates[len(e.primarySlot.Candidates)-1]; tail.Kind != modelRefKindUnit {
		t.Errorf("primarySlot tail = %+v, want unit (hard-locked)", tail)
	}
	if changedUnit == nil {
		t.Errorf("onUnitChanged was not called")
	} else if changedUnit.Model != newUnit.Model || changedUnit.Provider != newUnit.Provider {
		t.Errorf("onUnitChanged got %+v, want %+v", *changedUnit, newUnit)
	}
}

// TestApplyPendingUnitChange_NoPending verifies no-op when no unit is staged.
func TestApplyPendingUnitChange_NoPending(t *testing.T) {
	oldUnit := domain.ModelUnit{Model: "old-model", Provider: "old-provider"}

	callCount := 0
	e := &turnEngine{
		startReq: domain.TurnStartReq{
			Input: domain.TurnInput{
				Unit: agentUnitPtr(oldUnit),
			},
		},
		consumePendingUnitChange: func() *domain.ModelUnit {
			return nil
		},
		onUnitChanged: func(unit domain.ModelUnit) {
			callCount++
		},
	}

	e.applyPendingUnitChange()

	if e.startReq.Input.Unit == nil {
		t.Fatalf("startReq.Input.Unit is nil")
	}
	if e.startReq.Input.Unit.Model != oldUnit.Model || e.startReq.Input.Unit.Provider != oldUnit.Provider {
		t.Errorf("startReq.Input.Unit changed to %+v, want %+v", e.startReq.Input.Unit, oldUnit)
	}
	if callCount != 0 {
		t.Errorf("onUnitChanged called %d times, want 0", callCount)
	}
}

// TestHandleConfigure_StagesPendingUnitWhenRunning verifies that a unit change
// while a turn is running is staged as pending instead of waiting for the next turn.
func TestHandleConfigure_StagesPendingUnitWhenRunning(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID: "agent-1",
		primary: slotFromUnit(domain.ModelUnit{Model: "old-model", Provider: "old-provider"}),
	}
	a.turnEngineStore(&turnEngine{done: make(chan struct{}), runExited: make(chan struct{})})

	newPrimary := slotFromUnit(domain.ModelUnit{Model: "new-model", Provider: "new-provider"})
	req := domain.AgentConfigureReq{
		Primary: &newPrimary,
	}
	if err := a.handleConfigure(ctx, req); err != nil {
		t.Fatalf("handleConfigure failed: %v", err)
	}

	if got := slotFirstUnit(a.primary); got.Model != "new-model" {
		t.Errorf("primary slot unit = %+v, want new model", got)
	}
	pending := a.pendingChangeUnit.Load()
	if pending == nil {
		t.Fatalf("pendingChangeUnit is nil, want staged unit")
	}
	if pending.Model != "new-model" || pending.Provider != "new-provider" {
		t.Errorf("pendingChangeUnit = %+v, want new-model/new-provider", *pending)
	}
}

// TestHandleConfigure_DoesNotStageWhenIdle verifies that a unit change when no
// turn is running does not leave a pending unit behind.
func TestHandleConfigure_DoesNotStageWhenIdle(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID: "agent-1",
		primary: slotFromUnit(domain.ModelUnit{Model: "old-model", Provider: "old-provider"}),
	}

	newPrimary := slotFromUnit(domain.ModelUnit{Model: "new-model", Provider: "new-provider"})
	req := domain.AgentConfigureReq{
		Primary: &newPrimary,
	}
	if err := a.handleConfigure(ctx, req); err != nil {
		t.Fatalf("handleConfigure failed: %v", err)
	}

	if a.pendingChangeUnit.Load() != nil {
		t.Errorf("pendingChangeUnit should be nil when no turn is running")
	}
}

// TestHandleConfigure_NoPendingWhenUnitUnchanged verifies that repeated
// configuration with the same unit does not stage a pending change.
func TestHandleConfigure_NoPendingWhenUnitUnchanged(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID: "agent-1",
		primary: slotFromUnit(domain.ModelUnit{Model: "same-model", Provider: "same-provider"}),
	}
	a.turnEngineStore(&turnEngine{done: make(chan struct{}), runExited: make(chan struct{})})

	samePrimary := slotFromUnit(domain.ModelUnit{Model: "same-model", Provider: "same-provider"})
	req := domain.AgentConfigureReq{
		Primary: &samePrimary,
	}
	if err := a.handleConfigure(ctx, req); err != nil {
		t.Fatalf("handleConfigure failed: %v", err)
	}

	if a.pendingChangeUnit.Load() != nil {
		t.Errorf("pendingChangeUnit should be nil when unit is unchanged")
	}
}

// TestRequestPause_CancelsPauseCtx verifies that requestPause cancels pauseCtx,
// enabling immediate interruption of in-flight tool calls.
func TestRequestPause_CancelsPauseCtx(t *testing.T) {
	e := &turnEngine{
		done:        make(chan struct{}),
		resumeCh:    make(chan struct{}, 1),
		stepEventMu: sync.Mutex{},
	}
	e.pauseCtx, e.pauseCancel = backgroundPauseCtx()

	e.requestPause()

	select {
	case <-e.pauseCtx.Done():
		// success — pauseCtx was cancelled
	default:
		t.Errorf("pauseCtx.Done() not closed after requestPause")
	}
}

// TestResumeUserPause_RecreatesPauseCtx verifies that resumeUserPause creates
// a fresh pauseCtx so subsequent pauses can interrupt tools again.
func TestResumeUserPause_RecreatesPauseCtx(t *testing.T) {
	e := &turnEngine{
		done:        make(chan struct{}),
		resumeCh:    make(chan struct{}, 1),
		stepEventMu: sync.Mutex{},
	}
	e.pauseCtx, e.pauseCancel = backgroundPauseCtx()

	// Simulate a prior pause that cancelled the old ctx.
	e.requestPause()
	select {
	case <-e.pauseCtx.Done():
	default:
		t.Fatal("pauseCtx should be cancelled after requestPause")
	}

	// Resume recreates pauseCtx.
	e.resumeUserPause(context.Background())

	// New pauseCtx must NOT be done.
	select {
	case <-e.pauseCtx.Done():
		t.Errorf("pauseCtx should be fresh (not done) after resumeUserPause")
	default:
		// success
	}

	// And resumeCh must have been signaled.
	select {
	case <-e.resumeCh:
		// success
	default:
		t.Errorf("resumeCh not signaled after resumeUserPause")
	}
}

// TestRewritePausedResults_ReplacesCancelledMsg verifies that tool results with
// the "turn is shutting down" cancellation message are rewritten to "paused by
// user" when pauseRequested is true, and left untouched when not paused.
func TestRewritePausedResults_ReplacesCancelledMsg(t *testing.T) {
	// Case 1: pauseRequested = true → rewrite
	e := &turnEngine{
		stepEventMu: sync.Mutex{},
	}
	e.pauseCtx, e.pauseCancel = backgroundPauseCtx()
	e.requestPause() // sets pauseRequested = true

	results := []toolExecutionResult{
		{out: cancelledToolMsg, isErr: true},
		{out: "real output", isErr: false},
		{out: "some other error", isErr: true},
	}
	e.rewritePausedResults(results)

	if results[0].out != pausedToolMsg {
		t.Errorf("results[0].out = %q, want %q", results[0].out, pausedToolMsg)
	}
	if results[1].out != "real output" {
		t.Errorf("results[1].out = %q, want %q (should be unchanged)", results[1].out, "real output")
	}
	if results[2].out != "some other error" {
		t.Errorf("results[2].out = %q, want %q (non-cancel errors should be unchanged)", results[2].out, "some other error")
	}

	// Case 2: pauseRequested = false → no rewrite
	e2 := &turnEngine{stepEventMu: sync.Mutex{}}
	e2.pauseCtx, e2.pauseCancel = backgroundPauseCtx()

	results2 := []toolExecutionResult{
		{out: cancelledToolMsg, isErr: true},
	}
	e2.rewritePausedResults(results2)

	if results2[0].out != cancelledToolMsg {
		t.Errorf("results2[0].out = %q, want %q (should NOT be rewritten when not paused)", results2[0].out, cancelledToolMsg)
	}
}

// TestLifecycleWithCancel_PauseInterruptsTool verifies that lifecycleWithCancel
// cancels the tool context when pauseCtx is cancelled.
func TestLifecycleWithCancel_PauseInterruptsTool(t *testing.T) {
	e := &turnEngine{
		done:        make(chan struct{}),
		stepEventMu: sync.Mutex{},
	}
	e.pauseCtx, e.pauseCancel = backgroundPauseCtx()

	// Get a tool lifecycle context.
	toolCtx, toolCancel := e.lifecycleWithCancel(e.pauseCtx)
	defer toolCancel()

	// Request pause → should cancel toolCtx.
	e.requestPause()

	select {
	case <-toolCtx.Done():
		// success — tool context was cancelled by pause
	case <-time.After(200 * time.Millisecond):
		t.Errorf("toolCtx was not cancelled within 200ms of requestPause")
	}
}

// backgroundPauseCtx is a test helper that creates a fresh pause context pair.
func backgroundPauseCtx() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// TestApplyPendingUnitChange_RebuildsTools verifies that when the model unit
// changes mid-turn, the engine rebuilds its tool list so provider-native tools
// from the old model (e.g. glm web_search) are not carried over to the new
// model (e.g. Kimi).
func TestApplyPendingUnitChange_RebuildsTools(t *testing.T) {
	base := []domain.ToolSpec{
		{
			Name:        "filesystem_read",
			Description: "Read a file",
			InputSchema: `{"type":"object","properties":{"path":{"type":"string"}}}`,
		},
	}
	oldUnit := domain.ModelUnit{Model: "glm-5.1", Provider: "bigmodel"}
	newUnit := domain.ModelUnit{Model: "kimi-k2", Provider: "kimi"}

	e := &turnEngine{
		startReq: domain.TurnStartReq{
			Input: domain.TurnInput{
				Unit: agentUnitPtr(oldUnit),
			},
			CompiledContext: domain.CompiledContext{
				Capabilities: &domain.CompiledCapabilities{
					// Include a native web_search declaration; initTools should strip
					// and re-apply it based on the current model.
					PrimitiveTools: append(base, domain.ToolSpec{
						Name: "web_search",
						Type: "web_search",
						NativeConfig: map[string]any{
							"web_search": map[string]any{"enable": true},
						},
					}),
				},
			},
		},
		consumePendingUnitChange: func() *domain.ModelUnit { return &newUnit },
	}
	e.initTools()

	// glm should carry the native web_search tool (plus base + ask_user).
	var sawWebSearch bool
	for _, tool := range e.tools {
		if tool.Name == "web_search" {
			sawWebSearch = true
			break
		}
	}
	if !sawWebSearch {
		t.Fatalf("expected web_search for glm model, tools=%+v", e.tools)
	}

	e.applyPendingUnitChange()

	// After switching to Kimi, web_search should be gone (only base + ask_user).
	for _, tool := range e.tools {
		if tool.Name == "web_search" {
			t.Fatalf("web_search native tool should be removed after switching to Kimi, tools=%+v", e.tools)
		}
	}
	if len(e.tools) != len(base)+1 {
		t.Fatalf("expected %d tools after switch, got %d: %+v", len(base)+1, len(e.tools), e.tools)
	}
}
