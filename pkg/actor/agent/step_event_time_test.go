package agent

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func assertRFC3339Nano(t *testing.T, got string) {
	t.Helper()
	if got == "" {
		t.Fatalf("StepEvent.Time is empty")
	}
	if _, err := time.Parse(time.RFC3339Nano, got); err != nil {
		t.Fatalf("StepEvent.Time %q is not RFC3339Nano: %v", got, err)
	}
}

func TestEmitStepEvent_SetsTime(t *testing.T) {
	e := &turnEngine{
		logger:   newNopActorLogger(),
		stepByID: map[string]domain.TurnAction{},
	}
	e.emitStepEvent(domain.StepEvent{
		Kind:   "step.opened",
		StepID: "s1",
		TurnID: "t1",
		Role:   "assistant",
	})
	if len(e.stepEvents) != 1 {
		t.Fatalf("expected 1 buffered event, got %d", len(e.stepEvents))
	}
	assertRFC3339Nano(t, e.stepEvents[0].Time)
}

func TestEmitStepImmediate_SetsTime(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := &turnEngine{
		logger:   newNopActorLogger(),
		stepByID: map[string]domain.TurnAction{},
	}
	err := e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:   "step.error",
		StepID: "s1",
		TurnID: "t1",
	})
	if err != nil {
		t.Fatalf("emitStepImmediate returned error: %v", err)
	}
	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(ctx.EmittedEvents))
	}
	ev := ctx.EmittedEvents[0].Payload.(domain.StepEvent)
	assertRFC3339Nano(t, ev.Time)
}

func TestEmitActorStepEvent_SetsTime(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: domain.RawSession{NextSeq: 1},
	}
	err := a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.task_created",
		StepID: "task-1",
		TurnID: "t1",
	})
	if err != nil {
		t.Fatalf("emitActorStepEvent returned error: %v", err)
	}
	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(ctx.EmittedEvents))
	}
	ev := ctx.EmittedEvents[0].Payload.(domain.StepEvent)
	assertRFC3339Nano(t, ev.Time)
}
