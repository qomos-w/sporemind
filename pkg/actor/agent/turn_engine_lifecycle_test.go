package agent

import (
	"fmt"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// newLifecycleEngine returns a minimal turnEngine usable for lifecycle tests.
// Events are suppressed to avoid FakeCtx event-subscriber bookkeeping.
func newLifecycleEngine(t *testing.T) (*turnEngine, actor.Context) {
	t.Helper()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := &turnEngine{
		turnID:            "turn-lifecycle",
		logger:            ctx.Logger(),
		suppressEventEmit: true,
		pendingMessages:   make([]domain.ChatMessage, 0),
	}
	return e, ctx
}

func TestTurnLoopState_IsTerminal(t *testing.T) {
	cases := []struct {
		state    TurnLoopState
		terminal bool
	}{
		{LoopDispatch, false},
		{LoopAudit, false},
		{LoopExecute, false},
		{LoopForcedDispatch, false},
		{LoopPaused, false},
		{LoopCompleted, true},
		{LoopFailed, true},
		{LoopCancelled, true},
		{TurnLoopState(99), false}, // unknown value
	}
	for _, tc := range cases {
		if got := tc.state.IsTerminal(); got != tc.terminal {
			t.Errorf("IsTerminal(%v) = %v, want %v", tc.state, got, tc.terminal)
		}
	}
}

func TestTurnLoopState_CanTransition(t *testing.T) {
	active := []TurnLoopState{LoopDispatch, LoopAudit, LoopExecute, LoopForcedDispatch, LoopPaused}
	terminal := []TurnLoopState{LoopCompleted, LoopFailed, LoopCancelled}

	// Same-state is always allowed.
	for _, s := range append(active, terminal...) {
		if !s.CanTransition(s) {
			t.Errorf("CanTransition(%v -> %v) = false, want true (same-state)", s, s)
		}
	}

	// Active states can transition to any other state, including terminal.
	for _, from := range active {
		for _, to := range append(active, terminal...) {
			if from == to {
				continue
			}
			if !from.CanTransition(to) {
				t.Errorf("CanTransition(%v -> %v) = false, want true (active source)", from, to)
			}
		}
	}

	// Terminal states cannot transition back to active states.
	for _, from := range terminal {
		for _, to := range active {
			if got := from.CanTransition(to); got {
				t.Errorf("CanTransition(%v -> %v) = true, want false (terminal revival)", from, to)
			}
		}
	}

	// Terminal states cannot transition to any other terminal state either.
	for _, from := range terminal {
		for _, to := range terminal {
			if from == to {
				continue
			}
			if from.CanTransition(to) {
				t.Errorf("CanTransition(%v -> %v) = true, want false (terminal-to-terminal)", from, to)
			}
		}
	}
}

func TestSetLoopState_AllowsLegalTransitions(t *testing.T) {
	e, ctx := newLifecycleEngine(t)

	// Active-to-active.
	e.setLoopState(ctx, e.turnID, LoopAudit)
	if e.loopState != LoopAudit {
		t.Fatalf("Dispatch -> Audit failed, got %v", e.loopState)
	}

	// Active-to-terminal.
	e.setLoopState(ctx, e.turnID, LoopCompleted)
	if e.loopState != LoopCompleted {
		t.Fatalf("Audit -> Completed failed, got %v", e.loopState)
	}
}

func TestSetLoopState_RejectsCompletedToActive(t *testing.T) {
	for _, to := range []TurnLoopState{LoopDispatch, LoopAudit, LoopExecute, LoopPaused} {
		t.Run(fmt.Sprintf("completed->%s", to), func(t *testing.T) {
			e, ctx := newLifecycleEngine(t)
			e.loopState = LoopCompleted
			e.setLoopState(ctx, e.turnID, to)
			if e.loopState != LoopCompleted {
				t.Fatalf("Completed -> %v was accepted, want rejected", to)
			}
		})
	}
}

func TestSetLoopState_AllowsSameStateTransitions(t *testing.T) {
	e, ctx := newLifecycleEngine(t)

	for _, state := range []TurnLoopState{LoopDispatch, LoopAudit, LoopExecute, LoopPaused, LoopCompleted, LoopFailed, LoopCancelled} {
		e.loopState = state
		e.setLoopState(ctx, e.turnID, state)
		if e.loopState != state {
			t.Fatalf("same-state %v -> %v changed to %v", state, state, e.loopState)
		}
	}
}

func TestSetLoopState_RejectsCompletedToTerminal(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	e.loopState = LoopCompleted

	for _, to := range []TurnLoopState{LoopFailed, LoopCancelled} {
		e.setLoopState(ctx, e.turnID, to)
		if e.loopState != LoopCompleted {
			t.Fatalf("Completed -> %v was accepted, want rejected", to)
		}
	}
}

func TestSetLoopState_RejectsTerminalToTerminal(t *testing.T) {
	for _, from := range []TurnLoopState{LoopCompleted, LoopFailed, LoopCancelled} {
		for _, to := range []TurnLoopState{LoopCompleted, LoopFailed, LoopCancelled} {
			if from == to {
				continue
			}
			t.Run(fmt.Sprintf("%s->%s", from, to), func(t *testing.T) {
				e, ctx := newLifecycleEngine(t)
				e.loopState = from
				e.setLoopState(ctx, e.turnID, to)
				if e.loopState != from {
					t.Fatalf("%v -> %v was accepted, want rejected", from, to)
				}
			})
		}
	}
}

func TestSetLoopState_RejectsFailedAndCancelledToActive(t *testing.T) {
	for _, from := range []TurnLoopState{LoopFailed, LoopCancelled} {
		for _, to := range []TurnLoopState{LoopDispatch, LoopAudit, LoopExecute, LoopPaused} {
			t.Run(fmt.Sprintf("%s->%s", from, to), func(t *testing.T) {
				e, ctx := newLifecycleEngine(t)
				e.loopState = from
				e.setLoopState(ctx, e.turnID, to)
				if e.loopState != from {
					t.Fatalf("%v -> %v was accepted, want rejected", from, to)
				}
			})
		}
	}
}

func TestPhaseCommit_CompletedWithPending_DoesNotRevive(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	e.loopState = LoopCompleted
	e.turnError = "stale error"
	e.pendingMessages = append(e.pendingMessages, domain.ChatMessage{})

	done, err := e.phaseCommit(ctx, e.turnID)
	if err != nil {
		t.Fatalf("phaseCommit error: %v", err)
	}
	if !done {
		t.Fatalf("done = false, want true (Completed must stay terminal)")
	}
	if e.loopState != LoopCompleted {
		t.Fatalf("loopState = %v, want LoopCompleted", e.loopState)
	}
	if e.turnError != "stale error" {
		t.Fatalf("turnError = %q, want retained %q", e.turnError, "stale error")
	}
}

func TestPhaseCommit_CompletedWithoutPending_Exits(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	e.loopState = LoopCompleted

	done, err := e.phaseCommit(ctx, e.turnID)
	if err != nil {
		t.Fatalf("phaseCommit error: %v", err)
	}
	if !done {
		t.Fatalf("done = false, want true")
	}
	if e.loopState != LoopCompleted {
		t.Fatalf("loopState changed to %v, want LoopCompleted", e.loopState)
	}
}

func TestPhaseCommit_FailedWithPending_DoesNotRevive(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	e.loopState = LoopFailed
	e.turnError = "upstream failure"
	e.pendingMessages = append(e.pendingMessages, domain.ChatMessage{})

	done, err := e.phaseCommit(ctx, e.turnID)
	if err != nil {
		t.Fatalf("phaseCommit error: %v", err)
	}
	if !done {
		t.Fatalf("done = false, want true (Failed must stay terminal)")
	}
	if e.loopState != LoopFailed {
		t.Fatalf("loopState = %v, want LoopFailed", e.loopState)
	}
	if e.turnError != "upstream failure" {
		t.Fatalf("turnError = %q, want retained %q", e.turnError, "upstream failure")
	}
}

func TestPhaseCommit_CancelledWithPending_DoesNotRevive(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	e.loopState = LoopCancelled
	e.pendingMessages = append(e.pendingMessages, domain.ChatMessage{})

	done, err := e.phaseCommit(ctx, e.turnID)
	if err != nil {
		t.Fatalf("phaseCommit error: %v", err)
	}
	if !done {
		t.Fatalf("done = false, want true (Cancelled must stay terminal)")
	}
	if e.loopState != LoopCancelled {
		t.Fatalf("loopState = %v, want LoopCancelled", e.loopState)
	}
}

func TestPhaseCommit_FailedWithoutPending_Exits(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	e.loopState = LoopFailed
	e.turnError = "upstream failure"

	done, err := e.phaseCommit(ctx, e.turnID)
	if err != nil {
		t.Fatalf("phaseCommit error: %v", err)
	}
	if !done {
		t.Fatalf("done = false, want true")
	}
	if e.turnError != "upstream failure" {
		t.Fatalf("turnError = %q, want retained", e.turnError)
	}
}

func TestPhaseCommit_CancelledWithoutPending_Exits(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	e.loopState = LoopCancelled

	done, err := e.phaseCommit(ctx, e.turnID)
	if err != nil {
		t.Fatalf("phaseCommit error: %v", err)
	}
	if !done {
		t.Fatalf("done = false, want true")
	}
}

func TestTurnErrorForState_OnlyFailedReturnsError(t *testing.T) {
	e, _ := newLifecycleEngine(t)
	e.turnError = "stored error"

	for _, state := range []TurnLoopState{LoopDispatch, LoopAudit, LoopExecute, LoopPaused, LoopCompleted, LoopCancelled} {
		e.loopState = state
		if got := e.turnErrorForState(); got != "" {
			t.Errorf("turnErrorForState() in %v = %q, want empty", state, got)
		}
	}

	e.loopState = LoopFailed
	if got := e.turnErrorForState(); got != "stored error" {
		t.Errorf("turnErrorForState() in Failed = %q, want %q", got, "stored error")
	}
}

func TestTurnErrorForState_FailedPendingRetainsError(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	e.loopState = LoopFailed
	e.turnError = "upstream failure"
	e.pendingMessages = append(e.pendingMessages, domain.ChatMessage{})

	e.phaseCommit(ctx, e.turnID)
	if got := e.turnErrorForState(); got != "upstream failure" {
		t.Fatalf("turnErrorForState() after Failed+pending = %q, want %q", got, "upstream failure")
	}
}
