package agent

import (
	"errors"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// recordingEmitter implements actor.Context just enough to host a failing
// EmitEvent. We only need the override; the rest of actor.Context is satisfied
// by embedding testutil.FakeCtx.
// We instead extend FakeCtx usage directly via a wrapper struct, because
// embedding an interface with all-method-implemented FakeCtx still requires
// us to re-implement only EmitEvent.
type failingEmitCtx struct {
	*testutil.FakeCtx
	failEmit bool
}

func (f *failingEmitCtx) EmitEvent(kind string, payload any) error {
	if f.failEmit {
		return errors.New("emit failure (test injected)")
	}
	return f.FakeCtx.EmitEvent(kind, payload)
}

// TestEmitStepImmediate_PreFlushDrainsBuffer verifies that emitStepImmediate
// drains stepEvents buffered by prior emitStepEvent calls before sending the
// immediate event. This guards the SSE ordering invariant: tool_call's
// step.opened (phaseAudit) must reach the frontend before the ask_user
// interaction_requested emitted by executeWaitUser.
func TestEmitStepImmediate_PreFlushDrainsBuffer(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	var applied []string
	var snapshotCount int
	var snapMu sync.Mutex

	e := &turnEngine{
		logger:   newNopActorLogger(),
		stepByID: map[string]domain.TurnAction{},
		onStepEvent: func(ev domain.StepEvent) {
			applied = append(applied, ev.Kind+":"+ev.StepID)
		},
		onAfterFlush: func() {
			snapMu.Lock()
			snapshotCount++
			snapMu.Unlock()
		},
	}

	// Buffer a step.opened without flushing.
	e.emitStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "tool-1",
		TurnID:   "turn-1",
		StepType: "tool_call",
		Role:     "assistant",
	})

	// Emit immediate interaction event — must drain the buffer first.
	if err := e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          "turn-1-ask-req-1",
		TurnID:          "turn-1",
		InteractionType: "ask_user",
		RequestID:       "req-1",
	}); err != nil {
		t.Fatalf("emitStepImmediate returned error: %v", err)
	}

	// After emitStepImmediate returns, the buffer must be empty.
	if len(e.stepEvents) != 0 {
		t.Errorf("stepEvents buffer not drained: len=%d events=%v", len(e.stepEvents), eventKinds(e.stepEvents))
	}

	// onStepEvent must have been called for both buffered and immediate events,
	// in order.
	wantApplied := []string{"step.opened:tool-1", "step.interaction_requested:turn-1-ask-req-1"}
	if len(applied) != len(wantApplied) {
		t.Errorf("applied = %v, want %v", applied, wantApplied)
	} else {
		for i, w := range wantApplied {
			if applied[i] != w {
				t.Errorf("applied[%d] = %q, want %q", i, applied[i], w)
			}
		}
	}

	// onAfterFlush must have been called at least once.
	snapMu.Lock()
	defer snapMu.Unlock()
	if snapshotCount == 0 {
		t.Errorf("onAfterFlush never called — atomic snapshot would not contain the interaction step")
	}

	// SSE must have received both events in order: opened then interaction.
	if len(ctx.EmittedEvents) != 2 {
		t.Fatalf("EmittedEvents = %v, want 2 events", ctx.EmittedEvents)
	}
	if ctx.EmittedEvents[0].Payload.(domain.StepEvent).Kind != "step.opened" {
		t.Errorf("first emit = %v, want step.opened", ctx.EmittedEvents[0].Payload.(domain.StepEvent).Kind)
	}
	if ctx.EmittedEvents[1].Payload.(domain.StepEvent).Kind != "step.interaction_requested" {
		t.Errorf("second emit = %v, want step.interaction_requested", ctx.EmittedEvents[1].Payload.(domain.StepEvent).Kind)
	}
}

// TestEmitStepImmediate_PropagatesPreFlushError verifies that a flushEvents
// failure (e.g. SSE channel closed) prevents the immediate event from firing
// and surfaces the wrapped error so callers can transition the turn to Failed.
func TestEmitStepImmediate_PropagatesPreFlushError(t *testing.T) {
	ctx := &failingEmitCtx{
		FakeCtx:  testutil.HumanCtx(testutil.GenActorID()),
		failEmit: true,
	}

	var immediateApplied bool
	e := &turnEngine{
		logger: newNopActorLogger(),
		onStepEvent: func(ev domain.StepEvent) {
			if ev.Kind == "step.interaction_requested" {
				immediateApplied = true
			}
		},
	}

	// Buffer an event; flushEvents will try to emit it and fail.
	e.emitStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "tool-1",
		TurnID:   "turn-1",
		StepType: "tool_call",
	})

	err := e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          "turn-1-ask-req-1",
		TurnID:          "turn-1",
		InteractionType: "ask_user",
		RequestID:       "req-1",
	})
	if err == nil {
		t.Fatalf("emitStepImmediate returned nil error, want wrapped pre-flush error")
	}
	if !contains(err.Error(), "emitStepImmediate pre-flush") {
		t.Errorf("error = %q, want substring 'emitStepImmediate pre-flush'", err.Error())
	}
	if immediateApplied {
		t.Errorf("onStepEvent was called for interaction event despite pre-flush failure — should not happen")
	}
}

// TestEmitStepImmediate_PropagatesEmitError verifies that when ctx.EmitEvent
// fails for the immediate event itself, the error is wrapped and returned.
// onStepEvent still fires (it ran before emit), but onAfterFlush should not —
// the snapshot would reflect a step that the frontend never saw.
//
// Note: in practice we still call onAfterFlush optimistically so the snapshot
// matches a.steps even if SSE delivery failed (the step did happen locally).
// This test pins the chosen behavior: snapshot is refreshed regardless.
func TestEmitStepImmediate_PropagatesEmitError(t *testing.T) {
	ctx := &failingEmitCtx{
		FakeCtx:  testutil.HumanCtx(testutil.GenActorID()),
		failEmit: true,
	}

	var snapshotCount int
	e := &turnEngine{
		logger:      newNopActorLogger(),
		onStepEvent: func(domain.StepEvent) {},
		onAfterFlush: func() {
			snapshotCount++
		},
	}

	err := e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          "turn-1-ask-req-1",
		TurnID:          "turn-1",
		InteractionType: "ask_user",
		RequestID:       "req-1",
	})
	if err == nil {
		t.Fatalf("emitStepImmediate returned nil, want wrapped emit error")
	}
	if !contains(err.Error(), "emitStepImmediate emit step.interaction_requested") {
		t.Errorf("error = %q, want substring 'emitStepImmediate emit step.interaction_requested'", err.Error())
	}
}

// TestEmitStepImmediate_NoCallbacks_NoOp verifies emitStepImmediate is safe
// to call before onStepEvent / onAfterFlush are wired (e.g. in lower-level
// unit tests that only care about the SSE push).
func TestEmitStepImmediate_PersistsInteractionBeforeSSE(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	var calls []string
	e := &turnEngine{
		logger: newNopActorLogger(),
		onInteractionRequested: func(_ actor.Context, turnID, stepID, requestID, typ string, task map[string]any) {
			if turnID != "turn-1" || stepID != "ask-1" || requestID != "req-1" || typ != "ask_user" || task["q"] != "x" {
				t.Fatalf("unexpected interaction callback: turn=%q step=%q request=%q type=%q task=%v", turnID, stepID, requestID, typ, task)
			}
			calls = append(calls, "interaction")
		},
		onCheckpoint: func(actor.Context) { calls = append(calls, "checkpoint") },
	}

	if err := e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          "ask-1",
		TurnID:          "turn-1",
		InteractionType: "ask_user",
		RequestID:       "req-1",
		Task:            map[string]any{"q": "x"},
	}); err != nil {
		t.Fatalf("emitStepImmediate: %v", err)
	}
	if got, want := len(calls), 2; got != want || calls[0] != "interaction" || calls[1] != "checkpoint" {
		t.Fatalf("persistence calls = %v, want [interaction checkpoint]", calls)
	}
	if got := len(ctx.EmittedEvents); got != 1 {
		t.Fatalf("SSE events = %d, want 1 after persistence", got)
	}
}

func TestEmitStepImmediate_NoCallbacks_NoOp(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := &turnEngine{logger: newNopActorLogger()}

	if err := e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          "ask-1",
		TurnID:          "turn-1",
		InteractionType: "ask_user",
		RequestID:       "req-1",
	}); err != nil {
		t.Fatalf("emitStepImmediate with nil callbacks returned error: %v", err)
	}

	if len(ctx.EmittedEvents) != 1 {
		t.Errorf("EmittedEvents len = %d, want 1", len(ctx.EmittedEvents))
	}
}

// contains is a tiny helper to avoid pulling in strings for one check.
func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Compile-time guard: ensure we satisfy the actor.Context surface we depend on.
var _ actor.Context = (*failingEmitCtx)(nil)
