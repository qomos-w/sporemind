package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleTurnAnswer_NoEngine_RejectsUnmatched verifies that on the restart
// path, an answer whose RequestID does not match the pending interaction is
// rejected rather than applied to the wrong interaction.
func TestHandleTurnAnswer_NoEngine_RejectsUnmatched(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		// actorID empty -> saveMailbox is a no-op (key "" early return).
		pendingInteraction: &pendingInteractionState{
			RequestID: "req-pending",
			Type:      "ask_user",
		},
	}
	err := a.handleTurnAnswer(ctx, domain.TurnAnswerReq{RequestID: "req-other", AnswersJSON: "{}"})
	if err == nil {
		t.Fatal("expected error for mismatched requestID, got nil")
	}
	if a.pendingInteraction == nil {
		t.Fatal("pendingInteraction should not be cleared on mismatch")
	}
}

// TestHandleTurnAnswer_NoEngine_GoalSubmitResolves verifies that answering a
// pending goal_submit on the restart path resolves the goal (Confirmed=true)
// without needing a live turn engine.
func TestHandleTurnAnswer_NoEngine_GoalSubmitResolves(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: domain.RawSession{Goal: &gen.SessionGoal{InterpretedGoal: "g"}},
		pendingInteraction: &pendingInteractionState{
			RequestID: "req-g1",
			Type:      "goal_submit",
		},
		goalSubmitPending:   true,
		goalSubmitRequestID: "req-g1",
	}
	if err := a.handleTurnAnswer(ctx, domain.TurnAnswerReq{
		RequestID:   "req-g1",
		AnswersJSON: `{"decision":"approve"}`,
	}); err != nil {
		t.Fatalf("handleTurnAnswer: %v", err)
	}
	if a.RawSession.Goal == nil || !a.RawSession.Goal.Confirmed {
		t.Fatal("Goal.Confirmed = false, want true after restart-path approval")
	}
	if a.goalSubmitPending {
		t.Fatal("goalSubmitPending should be cleared after resolution")
	}
	if a.pendingInteraction != nil {
		t.Fatal("pendingInteraction should be cleared after resolution")
	}
}

// TestHandleTurnAnswer_NoEngine_AskUserContinuesTurn verifies that answering a
// pending ask_user on the restart path CONTINUES the same paused turn (reuses
// pi.TurnID, flips it back to running, emits turn.resumed) instead of spawning
// a fresh turn below it. The live engine is gone after restart, but the turn's
// accumulated tail is preserved.
func TestHandleTurnAnswer_NoEngine_AskUserContinuesTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		pendingInteraction: &pendingInteractionState{
			TurnID:    "t1",
			StepID:    "t1-ask-r1",
			RequestID: "r1",
			Type:      "ask_user",
		},
		pendingAskUser: true,
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "u1", Role: "user", State: "completed"},
				{ID: "t1", Role: "assistant", State: "paused"},
			},
			ActiveHead: 1,
		},
	}
	if err := a.handleTurnAnswer(ctx, domain.TurnAnswerReq{
		RequestID:   "r1",
		AnswersJSON: `{"answers":[{"text":"yes"}]}`,
	}); err != nil {
		t.Fatalf("handleTurnAnswer: %v", err)
	}
	if a.pendingAskUser {
		t.Fatal("pendingAskUser should be cleared after restart-path answer")
	}
	if a.pendingInteraction != nil {
		t.Fatal("pendingInteraction should be cleared after restart-path answer")
	}
	// The SAME paused turn id is reused — no new turn is spawned below it.
	if a.ActiveTurnRef != "t1" {
		t.Errorf("ActiveTurnRef = %q, want t1 (reused)", a.ActiveTurnRef)
	}
	if got := len(a.Session.Turns); got != 2 {
		t.Errorf("Session.Turns len = %d, want 2 (no new turn spawned)", got)
	}
	var continued *domain.Turn
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == "t1" {
			continued = &a.Session.Turns[i]
			break
		}
	}
	if continued == nil {
		t.Fatal("turn t1 not found in Session.Turns")
	}
	if continued.State != "running" {
		t.Errorf("continued turn state = %q, want running", continued.State)
	}
	// A non-terminal turn.resumed (state running) must be emitted so the UI
	// re-activates the existing envelope.
	foundEvent := false
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind != "turn" {
			continue
		}
		te, ok := ev.Payload.(domain.TurnEvent)
		if !ok {
			continue
		}
		if te.Kind == domain.TurnResumed && te.TurnID == "t1" {
			foundEvent = true
			if state, _ := te.Payload["state"].(string); state != "running" {
				t.Errorf("turn.resumed payload state = %v, want running", te.Payload["state"])
			}
			break
		}
	}
	if !foundEvent {
		t.Fatal("expected turn.resumed event for t1")
	}
}

// TestHandleTurnAnswer_NoEngine_AskUserWithMatchingStep verifies the persisted
// interaction step is marked resolved with the answer.
func TestHandleTurnAnswer_NoEngine_AskUserWithMatchingStep(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		pendingInteraction: &pendingInteractionState{
			TurnID:    "t1",
			StepID:    "step-1",
			RequestID: "r1",
			Type:      "ask_user",
		},
		steps: []domain.Step{
			{ID: "step-1", InteractionStatus: "pending", Closed: false},
			{ID: "step-2"},
		},
	}
	if err := a.handleTurnAnswer(ctx, domain.TurnAnswerReq{
		RequestID:   "r1",
		AnswersJSON: `{"answer":"42"}`,
	}); err != nil {
		t.Fatalf("handleTurnAnswer: %v", err)
	}
	if !a.steps[0].Closed || a.steps[0].InteractionStatus != "resolved" {
		t.Errorf("step-0 not resolved: Closed=%v InteractionStatus=%q", a.steps[0].Closed, a.steps[0].InteractionStatus)
	}
	if a.steps[1].Closed {
		t.Error("step-1 (unrelated) should not have been touched")
	}
}

// TestHandleTurnAnswer_NoEngine_NoPending verifies that with no pending
// interaction and no engine, the answer is rejected.
func TestRecoverPendingInteraction_RebuildsMissingStep(t *testing.T) {
	a := &Actor{
		pendingInteraction: &pendingInteractionState{
			TurnID:    "t1",
			StepID:    "t1-ask-r1",
			RequestID: "r1",
			Type:      "ask_user",
			Task:      map[string]any{"questions": []any{map[string]any{"question": "Continue?"}}},
		},
		Session: domain.Session{Turns: []domain.Turn{{ID: "t1", Role: "assistant", State: "running", Revision: 1}}},
	}

	if !a.recoverPendingInteraction(nil) {
		t.Fatal("recoverPendingInteraction = false, want mutation")
	}
	if len(a.steps) != 1 {
		t.Fatalf("steps = %d, want rebuilt interaction step", len(a.steps))
	}
	step := a.steps[0]
	if step.ID != "t1-ask-r1" || step.TurnID != "t1" || step.RequestID != "r1" || step.Type != "ask_user" || step.Closed || step.InteractionStatus != "pending" {
		t.Fatalf("rebuilt step = %+v, want open pending ask_user step", step)
	}
	if a.Session.Turns[0].State != "paused" || a.Session.Turns[0].PauseReason != domain.PauseReasonInteraction {
		t.Fatalf("turn = %+v, want paused/interaction", a.Session.Turns[0])
	}
	if a.status.PauseKind != domain.PauseReasonInteraction {
		t.Fatalf("status.PauseKind = %q, want interaction", a.status.PauseKind)
	}
}

func TestHandleTurnAnswer_NoEngine_NoPending(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{}
	err := a.handleTurnAnswer(ctx, domain.TurnAnswerReq{RequestID: "r1", AnswersJSON: "{}"})
	if err == nil {
		t.Fatal("expected error when no pending interaction and no engine, got nil")
	}
}

// TestAnswerAsUserMessage covers the formatting helper.
func TestAnswerAsUserMessage(t *testing.T) {
	cases := []struct {
		typ, answers, want string
	}{
		{"ask_user", `{"answers":[{"text":"yes"}]}`, `{"answers":[{"text":"yes"}]}`},
		{"permission", "", "[permission denied]"},
		{"ask_user", "", "[answered]"},
	}
	for _, c := range cases {
		got := answerAsUserMessage(c.typ, c.answers)
		if got != c.want {
			t.Errorf("answerAsUserMessage(%q,%q) = %q, want %q", c.typ, c.answers, got, c.want)
		}
		if strings.TrimSpace(got) == "" {
			t.Errorf("answerAsUserMessage(%q,%q) returned empty", c.typ, c.answers)
		}
	}
}
