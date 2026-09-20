package agent

import (
	"testing"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// findTurn returns a pointer to the turn record with the given id, or nil.
func findTurn(a *Actor, id string) *domain.Turn {
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == id {
			return &a.Session.Turns[i]
		}
	}
	return nil
}

func lastLifecycleEvent(ctx *testutil.FakeCtx, turnID, kind string) (domain.TurnEvent, bool) {
	for i := len(ctx.EmittedEvents) - 1; i >= 0; i-- {
		ev := ctx.EmittedEvents[i]
		if ev.Kind != "turn" {
			continue
		}
		te, ok := ev.Payload.(domain.TurnEvent)
		if !ok {
			continue
		}
		if te.TurnID == turnID && te.Kind == kind {
			return te, true
		}
	}
	return domain.TurnEvent{}, false
}

// TestApplyTurnLifecycle_SameRecordAcrossTransitions verifies the adapter keeps
// a single Session.Turns record across the start→pause→resume→complete
// lifecycle, bumping Revision monotonically and enforcing the canonical field
// constraints (pause carries PauseReason, terminal clears it, failed carries
// Error).
func TestApplyTurnLifecycle_SameRecordAcrossTransitions(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
	}

	turnID := "turn-1"

	// start: creates the record in "running".
	rec, err := a.applyTurnLifecycle(ctx, domain.TurnLifecycleStarted, turnID, nil, turnLifecycleOptions{
		role:      "assistant",
		turnOrder: 5,
		startedAt: "2026-01-01T00:00:00Z",
		timestamp: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if rec.State != domain.TurnStateRunning || rec.Revision != 1 || rec.TurnOrder != 5 {
		t.Fatalf("start record = %+v", rec)
	}
	if len(a.Session.Turns) != 1 {
		t.Fatalf("expected 1 turn after start, got %d", len(a.Session.Turns))
	}

	// pause: flips to paused, requires PauseReason, keeps the SAME record.
	rec, err = a.applyTurnLifecycle(ctx, domain.TurnLifecyclePaused, turnID, nil, turnLifecycleOptions{
		pauseReason: domain.PauseReasonUser,
	})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if rec.State != domain.TurnStatePaused || rec.Revision != 2 || rec.PauseReason != domain.PauseReasonUser {
		t.Fatalf("pause record = %+v", rec)
	}
	if len(a.Session.Turns) != 1 {
		t.Fatalf("expected 1 turn after pause, got %d", len(a.Session.Turns))
	}

	// A paused record must reject a transition that omits PauseReason.
	if _, err := a.applyTurnLifecycle(ctx, domain.TurnLifecyclePaused, turnID, nil, turnLifecycleOptions{}); err == nil {
		t.Fatalf("expected error pausing without a reason")
	}

	// resume: back to running, clears PauseReason.
	rec, err = a.applyTurnLifecycle(ctx, domain.TurnLifecycleResumed, turnID, nil, turnLifecycleOptions{})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if rec.State != domain.TurnStateRunning || rec.PauseReason != "" || rec.Revision != 3 {
		t.Fatalf("resume record = %+v", rec)
	}

	// succeed: terminal, clears PauseReason/Error, sets CompletedAt.
	rec, err = a.applyTurnLifecycle(ctx, domain.TurnLifecycleCompleted, turnID, func(r *domain.Turn) {
		r.Usage = &domain.UsageData{InputTokens: 10}
	}, turnLifecycleOptions{completedAt: "2026-01-01T00:00:09Z"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if rec.State != domain.TurnStateCompleted || rec.PauseReason != "" || rec.Error != "" || rec.Revision != 4 || rec.CompletedAt != "2026-01-01T00:00:09Z" {
		t.Fatalf("complete record = %+v", rec)
	}
	if rec.Usage == nil || rec.Usage.InputTokens != 10 {
		t.Fatalf("prepare hook usage not applied: %+v", rec.Usage)
	}
	if len(a.Session.Turns) != 1 {
		t.Fatalf("expected 1 turn after complete, got %d", len(a.Session.Turns))
	}

	// A terminal record must not be revived.
	if _, err := a.applyTurnLifecycle(ctx, domain.TurnLifecycleResumed, turnID, nil, turnLifecycleOptions{}); err == nil {
		t.Fatalf("expected error reviving a completed turn")
	}
}

// TestApplyTurnLifecycle_FailedRequiresError verifies the failed transition
// rejects an empty Error and, when provided, stamps it on the record.
func TestApplyTurnLifecycle_FailedRequiresError(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{actorID: "agent-2", agentKind: "coder"}

	if _, err := a.applyTurnLifecycle(ctx, domain.TurnLifecycleFailed, "turn-x", nil, turnLifecycleOptions{}); err == nil {
		t.Fatalf("expected error failing without an error message")
	}
	rec, err := a.applyTurnLifecycle(ctx, domain.TurnLifecycleFailed, "turn-x", nil, turnLifecycleOptions{
		errorMsg: "boom",
	})
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if rec.State != domain.TurnStateFailed || rec.Error != "boom" {
		t.Fatalf("failed record = %+v", rec)
	}
	if te, ok := lastLifecycleEvent(ctx, "turn-x", domain.TurnLifecycleFailed); !ok || te.Payload["error"] != "boom" {
		t.Fatalf("failed event missing error in payload: %+v ok=%v", te, ok)
	}
}

// TestHandleTurnComplete_TerminalRejectionPreservesRecord verifies the terminal
// path is not silent and does not corrupt the record when the reducer rejects
// the transition. The turn is pre-seeded in the "failed" terminal state, so
// applying "completed" is illegal (terminal cannot transition to another
// terminal). handleTurnComplete must: keep the record in "failed" (explicit
// rollback by the adapter — no mutation committed on rejection), emit no false
// turn.completed, and continue so the turn's content is still persisted.
func TestHandleTurnComplete_TerminalRejectionPreservesRecord(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-term",
		agentKind: "coder",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-term", Role: "assistant", State: domain.TurnStateFailed, Error: "prior", TurnOrder: 3},
			},
		},
		RawSession: domain.RawSession{NextSeq: 1},
		status: turnStatus{
			TurnID:         "turn-term",
			State:          domain.TurnStateRunning,
			StartStepCount: 0,
		},
	}

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:        "turn-term",
			Role:      "assistant",
			State:     domain.TurnStateCompleted,
			Timestamp: "2026-01-01T00:00:00Z",
			StartedAt: "2026-01-01T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete should continue (not return) on terminal rejection, got error: %v", err)
	}

	// The record must retain its pre-terminal state — no silent corruption.
	rec := findTurn(a, "turn-term")
	if rec == nil {
		t.Fatal("turn record missing after terminal rejection")
	}
	if rec.State != domain.TurnStateFailed || rec.Error != "prior" {
		t.Fatalf("record corrupted by rejected terminal transition = %+v, want state=failed error=prior", rec)
	}

	// No false terminal event must have been emitted (the adapter skips emit on
	// rejection), proving the failure was not silently treated as completed.
	for _, e := range ctx.EmittedEvents {
		if e.Kind != "turn" {
			continue
		}
		if te, ok := e.Payload.(domain.TurnEvent); ok && te.TurnID == "turn-term" && te.Kind == domain.TurnLifecycleCompleted {
			t.Fatalf("unexpected turn.completed emitted after reducer rejection")
		}
	}
}

// TestStartTurnWithName_StartRejectionBlocksAndClearsActiveTurnRef verifies the
// start path BLOCKS when the reducer rejects turn.started: the engine must not
// run, and the ActiveTurnRef set just before must be rolled back so it does not
// become a zombie that blocks all subsequent chat.submit. The turn is pre-seeded
// in the "failed" terminal state, so started (failed→running) is illegal.
func TestStartTurnWithName_StartRejectionBlocksAndClearsActiveTurnRef(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	// Seed aggRefCache so the aggregator-availability check passes and we reach
	// the lifecycle call (refreshAggRefs is a no-op without a topology, leaving
	// the seeded cache intact).
	a := &Actor{
		actorID:   "agent-start",
		agentKind: "coder",
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{tag: "system"},
		},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-start", Role: "assistant", State: domain.TurnStateFailed, Error: "prior", TurnOrder: 7},
			},
		},
	}

	_, err := a.startTurnWithName(ctx, domain.TurnInput{Text: "hello"}, "turn-start")
	if err == nil {
		t.Fatal("expected startTurnWithName to block (return error) when start lifecycle is rejected")
	}

	// ActiveTurnRef must be rolled back — the zombie-prevention invariant.
	if ref := a.getActiveTurnRef(); ref != "" {
		t.Fatalf("ActiveTurnRef = %q after rejected start, want empty (zombie turn state)", ref)
	}

	// turn.started must NOT have been emitted — no turn actually started.
	for _, e := range ctx.EmittedEvents {
		if e.Kind != "turn" {
			continue
		}
		if te, ok := e.Payload.(domain.TurnEvent); ok && te.TurnID == "turn-start" && (te.Kind == domain.TurnStarted || te.Kind == domain.TurnResumed) {
			t.Fatalf("unexpected %s emitted after rejected start", te.Kind)
		}
	}

	// The record must retain its pre-start state (failed), not be corrupted.
	rec := findTurn(a, "turn-start")
	if rec == nil || rec.State != domain.TurnStateFailed {
		t.Fatalf("record corrupted by rejected start = %+v, want state=failed", rec)
	}
}
