package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// snapshotCheckingCtx wraps FakeCtx and asserts the actor snapshot already
// contains the completed/cancelled turn when the terminal turn event is emitted.
type snapshotCheckingCtx struct {
	*testutil.FakeCtx
	a       *Actor
	checked bool
	turnID  string
}

func (c *snapshotCheckingCtx) EmitEvent(kind string, payload any) error {
	if kind == "turn" {
		ev, ok := payload.(domain.TurnEvent)
		if ok && ev.TurnID == c.turnID {
			switch ev.Kind {
			case domain.TurnCompleted, domain.TurnFailed, domain.TurnCancelled:
				snap := c.a.snapshot.Load()
				if snap == nil {
					panic("snapshot nil at turn terminal event emit")
				}
				found := false
				for _, t := range snap.turns {
					if t.ID == c.turnID {
						found = true
						break
					}
				}
				if !found {
					panic("snapshot missing terminal turn at event emit")
				}
				if snap.activeTurnRef != "" {
					panic("activeTurnRef should be cleared before terminal event emit")
				}
				c.checked = true
			}
		}
	}
	return c.FakeCtx.EmitEvent(kind, payload)
}

func (c *snapshotCheckingCtx) Lifecycle() context.Context { return context.Background() }

var _ actor.Context = (*snapshotCheckingCtx)(nil)

func TestHandleTurnComplete_SnapshotBeforeEvent(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed"},
			},
		},
		RawSession: domain.RawSession{NextSeq: 1},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-1",
			State:          "running",
			StartStepCount: 0,
		},
	}
	checkCtx := &snapshotCheckingCtx{FakeCtx: ctx, a: a, turnID: "turn-1"}

	err := a.handleTurnComplete(checkCtx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-1",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}
	if !checkCtx.checked {
		t.Fatalf("terminal turn event was not emitted or snapshot was not checked")
	}

	snap := a.snapshot.Load()
	if snap == nil {
		t.Fatalf("snapshot nil after handleTurnComplete")
	}
	if len(snap.turns) != 2 {
		t.Fatalf("expected 2 turns in snapshot, got %d", len(snap.turns))
	}
	last := snap.turns[len(snap.turns)-1]
	if last.ID != "turn-1" {
		t.Fatalf("expected last turn ID turn-1, got %s", last.ID)
	}
	if last.State != "completed" {
		t.Fatalf("expected last turn state completed, got %s", last.State)
	}
}

// TestHandleTurnComplete_MergesResumedTurn verifies that when a resumed turn
// (one that already owns a Session.Turns entry, e.g. after crash-recovery
// resume) completes, handleTurnComplete merges the terminal metadata into the
// EXISTING entry instead of appending a duplicate. The turn's original
// StartedAt / TurnOrder identity must be preserved so the conversation shows
// one continuous turn envelope from pause through resume to completion.
func TestHandleTurnComplete_MergesResumedTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed"},
				{ID: "turn-2", Role: "assistant", State: "running", StartedAt: "orig-start", TurnOrder: 7},
			},
		},
		RawSession: domain.RawSession{NextSeq: 5},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 1},
			{ID: "s2", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 2},
		},
		status: turnStatus{
			TurnID:         "turn-2",
			State:          "running",
			StartStepCount: 0,
		},
	}

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-2",
			Role:        "assistant",
			State:       "completed",
			StartedAt:   "NEW-start", // differs from the entry's preserved value
			CompletedAt: "2026-01-01T00:00:01Z",
			Timestamp:   "2026-01-01T00:00:01Z",
			Usage:       &domain.UsageData{InputTokens: 100, OutputTokens: 50},
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	// No duplicate: the resumed turn merges into its existing entry.
	if got := len(a.Session.Turns); got != 2 {
		t.Fatalf("Session.Turns len = %d, want 2 (merged, no duplicate)", got)
	}
	var merged *domain.Turn
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == "turn-2" {
			merged = &a.Session.Turns[i]
			break
		}
	}
	if merged == nil {
		t.Fatal("turn-2 entry missing after complete")
	}
	if merged.State != "completed" {
		t.Errorf("merged turn state = %q, want completed", merged.State)
	}
	// Original identity preserved — the resume's StartedAt must NOT overwrite it.
	if merged.StartedAt != "orig-start" {
		t.Errorf("merged StartedAt = %q, want orig-start (preserved)", merged.StartedAt)
	}
	if merged.TurnOrder != 7 {
		t.Errorf("merged TurnOrder = %d, want 7 (preserved)", merged.TurnOrder)
	}
	if merged.CompletedAt != "2026-01-01T00:00:01Z" {
		t.Errorf("merged CompletedAt = %q, want updated", merged.CompletedAt)
	}
	// Active turn ref cleared after completion.
	if a.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef = %q, want empty after complete", a.ActiveTurnRef)
	}
}

func TestHandleTurnCancel_SnapshotBeforeEvent(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed"},
			},
		},
		RawSession: domain.RawSession{NextSeq: 1},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: false, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-1",
			State:          "running",
			StartStepCount: 0,
		},
	}
	checkCtx := &snapshotCheckingCtx{FakeCtx: ctx, a: a, turnID: "turn-1"}

	err := a.handleTurnCancel(checkCtx)
	if err != nil {
		t.Fatalf("handleTurnCancel error: %v", err)
	}
	if !checkCtx.checked {
		t.Fatalf("terminal turn event was not emitted or snapshot was not checked")
	}

	snap := a.snapshot.Load()
	if snap == nil {
		t.Fatalf("snapshot nil after handleTurnCancel")
	}
	if len(snap.turns) != 2 {
		t.Fatalf("expected 2 turns in snapshot, got %d", len(snap.turns))
	}
	last := snap.turns[len(snap.turns)-1]
	if last.ID != "turn-1" {
		t.Fatalf("expected last turn ID turn-1, got %s", last.ID)
	}
	if last.State != "cancelled" {
		t.Fatalf("expected last turn state cancelled, got %s", last.State)
	}
}

// TestHandleTurnComplete_OrphanedUserTurn verifies that when a user message
// is submitted during a turn's finalize phase (AcceptingInjects()==false),
// the user turn is not lost. The user turn ends up between oldActiveHead and
// the just-completed assistant turn in Session.Turns. Without the fix,
// maybeAutoStartTurn cannot find it (it only scans AFTER ActiveHead), and
// the UI hangs indefinitely waiting for an assistant response.
func TestHandleTurnComplete_OrphanedUserTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		// Session state simulates the finalize-phase race:
		//   [userT1(started turn-1), userM(submitted during finalize)]
		// ActiveHead still points to userT1 (the turn-finishing path
		// in handleChatSubmit does not update ActiveHead).
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed"},
				{ID: "user-turn-2", Role: "user", State: "completed", UserInput: "orphaned message"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{NextSeq: 10},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-1",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
	}
	a.takeSnapshot()

	// handleTurnComplete will append the assistant turn for turn-1, then
	// scan for orphaned user turns between oldActiveHead(0) and the new
	// assistant turn. It should find user-turn-2 at index 1 and attempt
	// to start a new turn for it. startTurn will fail (no aggregator) but
	// ActiveHead should be set to the orphaned user turn's index.
	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-1",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	// The assistant turn for turn-1 should have been appended.
	if len(a.Session.Turns) < 3 {
		t.Fatalf("expected at least 3 turns (userT1, userM, assistantT1), got %d", len(a.Session.Turns))
	}

	// ActiveHead should point to the orphaned user turn (index 1), not the
	// assistant turn (index 2). This proves the orphaned-turn scan ran.
	if a.Session.ActiveHead != 1 {
		t.Errorf("ActiveHead = %d, want 1 (orphaned user turn index)", a.Session.ActiveHead)
	}

	// The orphaned user turn should be at index 1.
	if a.Session.Turns[1].Role != "user" {
		t.Errorf("Turns[1].Role = %q, want 'user'", a.Session.Turns[1].Role)
	}
	if a.Session.Turns[1].UserInput != "orphaned message" {
		t.Errorf("Turns[1].UserInput = %q, want 'orphaned message'", a.Session.Turns[1].UserInput)
	}
}

func TestRecordGoalTurnCompletion(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{Goal: &gen.SessionGoal{
		Confirmed: true,
		MaxTurns:  2,
		TurnCount: 0,
	}}}
	a.recordGoalTurnCompletion()
	if got := a.RawSession.Goal.TurnCount; got != 1 {
		t.Fatalf("TurnCount = %d, want 1", got)
	}
	a.recordGoalTurnCompletion()
	if got := a.RawSession.Goal.TurnCount; got != 2 {
		t.Fatalf("TurnCount = %d, want 2", got)
	}
	a.recordGoalTurnCompletion()
	if got := a.RawSession.Goal.TurnCount; got != 2 {
		t.Fatalf("TurnCount = %d after budget, want 2", got)
	}
}

func TestRecordGoalTurnCompletionRequiresConfirmation(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{Goal: &gen.SessionGoal{
		MaxTurns:  2,
		TurnCount: 0,
	}}}
	a.recordGoalTurnCompletion()
	if got := a.RawSession.Goal.TurnCount; got != 0 {
		t.Fatalf("unconfirmed goal TurnCount = %d, want 0", got)
	}
}

// normal case (no orphaned user turns), handleTurnComplete falls through to
// maybeAutoStartTurn without falsely triggering the orphan scan.
func TestMaybeAutoStartTurn_NoOrphanWhenNormalCompletion(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{NextSeq: 10},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-1",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
	}
	a.takeSnapshot()

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:        "turn-1",
			Role:      "assistant",
			State:     "completed",
			Timestamp: "2026-01-01T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	// Normal case: 2 turns (userT1 + assistantT1), ActiveHead = 1.
	if len(a.Session.Turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(a.Session.Turns))
	}
	// ActiveHead should point to the assistant turn (last turn).
	if a.Session.ActiveHead != 1 {
		t.Errorf("ActiveHead = %d, want 1 (assistant turn)", a.Session.ActiveHead)
	}
	// No new turn should have been started.
	if a.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef = %q, want empty (no auto-started turn)", a.ActiveTurnRef)
	}
}

// TestStartTurnWithName_EmitsPendingSkillStepAfterTurnStarted verifies that a
// slash-skill step created with TurnID = turnName is emitted after
// turn.started via pendingSkillStepID, and that the field is cleared.
func TestStartTurnWithName_EmitsPendingSkillStepAfterTurnStarted(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		// Pre-populate aggregator cache so the early availability check passes;
		// the turn still fails later because no model target resolves.
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{tag: "system"},
		},
	}
	turnName := "turn-1"
	skillStepID := "turn-1-tool-001"

	// Simulate a slash-skill step already synthesized with TurnID = turnName.
	a.steps = append(a.steps, domain.Step{
		ID:        skillStepID,
		Role:      "assistant",
		Type:      "tool_call",
		TurnID:    turnName,
		Closed:    true,
		Timestamp: "2026-07-22T00:00:00Z",
		Seq:       1,
		Content: []domain.ContentBlock{
			{Type: domain.ContentBlockToolUse, ToolName: "agent_skill_use"},
			{Type: domain.ContentBlockToolResult, Text: "ok"},
		},
	})
	a.pendingSkillStepID = skillStepID

	_, err := a.startTurnWithName(ctx, domain.TurnInput{Text: "test"}, turnName)
	// With a FakeCtx the turn may complete without hitting a live model; the
	// point of this test is event ordering, not the error outcome.
	_ = err

	// Find turn.started.
	startedIdx := -1
	for i, e := range ctx.EmittedEvents {
		if e.Kind != "turn" {
			continue
		}
		ev, ok := e.Payload.(domain.TurnEvent)
		if ok && ev.Kind == domain.TurnStarted {
			startedIdx = i
			break
		}
	}
	if startedIdx < 0 {
		t.Fatal("expected turn.started to be emitted")
	}

	// Find the skill step.opened after turn.started.
	skillOpenedIdx := -1
	for i := startedIdx + 1; i < len(ctx.EmittedEvents); i++ {
		e := ctx.EmittedEvents[i]
		if e.Kind != "step" {
			continue
		}
		ev, ok := e.Payload.(domain.StepEvent)
		if ok && ev.Kind == "step.opened" && ev.StepID == skillStepID && ev.TurnID == turnName {
			skillOpenedIdx = i
			break
		}
	}
	if skillOpenedIdx < 0 {
		t.Fatalf("expected skill step.opened (stepID=%s turnID=%s) after turn.started, events: %+v", skillStepID, turnName, ctx.EmittedEvents)
	}

	if a.pendingSkillStepID != "" {
		t.Fatalf("expected pendingSkillStepID to be cleared, got %q", a.pendingSkillStepID)
	}
}

// TestStartTurnWithName_NoAggregator_FailsWithoutZombieState verifies the fix
// for the conversation-hang bug: when the aggregator hasn't started yet,
// startTurnWithName must fail early WITHOUT setting ActiveTurnRef or emitting
// turn.started. Previously it set ActiveTurnRef first, then failed on the
// aggregator check, leaving a zombie "running" turn that blocked all
// subsequent messages.
func TestStartTurnWithName_NoAggregator_FailsWithoutZombieState(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{} // no aggRefCache — aggregator unavailable

	_, err := a.startTurnWithName(ctx, domain.TurnInput{Text: "hello"}, "turn-zombie")
	if err == nil {
		t.Fatal("expected startTurnWithName to fail without aggregator")
	}

	// ActiveTurnRef must not be set — this is the zombie state that caused
	// the conversation to hang.
	if a.ActiveTurnRef != "" {
		t.Fatalf("ActiveTurnRef = %q, want empty (zombie turn state after aggregator failure)", a.ActiveTurnRef)
	}

	// turn.started must NOT have been emitted — no turn actually started.
	for _, e := range ctx.EmittedEvents {
		if e.Kind != "turn" {
			continue
		}
		ev, ok := e.Payload.(domain.TurnEvent)
		if ok && (ev.Kind == domain.TurnStarted || ev.Kind == domain.TurnResumed) {
			t.Fatalf("unexpected %s event emitted without aggregator", ev.Kind)
		}
	}
}

// TestHandleTurnComplete_ReadyForReview verifies that when an agent with a
// confirmed bound goal declares ready_for_review, the goal status is set to
// "ready_for_review" but the goal is NOT cleared (unlike complete_candidate).
func TestHandleTurnComplete_ReadyForReview(t *testing.T) {
	planner := &goalCardProjectPlanner{}
	ctx := newGoalCardApprovalCtx(planner)
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)

	a := &Actor{
		actorID:       "agent-rfr",
		agentKind:     "worker",
		parentAgentID: "parent-agent-1",
		ActiveTurnRef: "turn-rfr",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-rfr", Role: "user", State: "completed"},
			},
		},
		RawSession: domain.RawSession{
			NextSeq: 1,
			Goal: &gen.SessionGoal{
				Condition:       "do the thing",
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: "ticket-rfr",
				MaxTurns:        10,
				TurnCount:       1,
			},
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-rfr", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-rfr",
			State:          "running",
			StartStepCount: 0,
		},
	}

	evidence := []string{"evidence-one", "evidence-two"}
	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-rfr",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
			Assessment: &gen.TurnAssessment{
				Decision: "ready_for_review",
				Reason:   "work complete, awaiting review",
				Evidence: evidence,
				Outputs: map[string]any{
					"report": "done",
					"score":  float64(42),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	// Goal must NOT be cleared (unlike complete_candidate).
	if a.RawSession.Goal == nil {
		t.Fatal("goal was cleared on ready_for_review, should be preserved")
	}
	// Goal status must be set to ready_for_review.
	if a.RawSession.Goal.Status != "ready_for_review" {
		t.Errorf("Goal.Status = %q, want ready_for_review", a.RawSession.Goal.Status)
	}
	// The worker's typed JSON outputs must be captured onto the goal so the
	// review path can validate them against the task card's data.outputs.
	if got := a.RawSession.Goal.Outputs; got == nil || got["report"] != "done" || got["score"] != float64(42) {
		t.Errorf("Goal.Outputs = %+v, want report=done score=42", got)
	}
	// The bound card must be moved to pending_review and the assessment
	// evidence must be forwarded to the project card store.
	if planner.claimed == nil {
		t.Fatal("expected wiki_set_status call for the bound card")
	}
	if planner.claimed.ID != "ticket-rfr" || planner.claimed.Status != "pending_review" {
		t.Fatalf("bound card status = %+v, want ID=ticket-rfr Status=pending_review", planner.claimed)
	}
	if planner.claimed.ExpectedStatus != "doing" {
		t.Fatalf("bound card CAS expected = %q, want doing (a racing disposition must not be rolled back)", planner.claimed.ExpectedStatus)
	}
	if len(planner.claimed.Evidence) != len(evidence) || planner.claimed.Evidence[0] != evidence[0] || planner.claimed.Evidence[1] != evidence[1] {
		t.Fatalf("ready_for_review evidence = %v, want %v", planner.claimed.Evidence, evidence)
	}
}

// TestHandleTurnComplete_ReadyForReview_CASMissKeepsDisposition verifies the
// approve/pending_review rollback race fix: when the worker's pending_review
// CAS write loses because the card was concurrently disposed (e.g. an approve
// already set done), handleTurnComplete must swallow the CAS error and keep
// the agent paused in ready_for_review — never rewrite the card.
func TestHandleTurnComplete_ReadyForReview_CASMissKeepsDisposition(t *testing.T) {
	planner := &goalCardProjectPlanner{
		claimErr: fmt.Errorf(`project.wiki.set_status: expected status "doing", got "done"`),
	}
	ctx := newGoalCardApprovalCtx(planner)
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)

	a := &Actor{
		actorID:       "agent-rfr-cas",
		agentKind:     "worker",
		parentAgentID: "parent-agent-1",
		ActiveTurnRef: "turn-rfr-cas",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-rfr-cas", Role: "user", State: "completed"},
			},
		},
		RawSession: domain.RawSession{
			NextSeq: 1,
			Goal: &gen.SessionGoal{
				Condition:       "do the thing",
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: "ticket-rfr-cas",
				MaxTurns:        10,
				TurnCount:       1,
			},
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-rfr-cas", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-rfr-cas",
			State:          "running",
			StartStepCount: 0,
		},
	}

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:    "turn-rfr-cas",
			Role:  "assistant",
			State: "completed",
			Assessment: &gen.TurnAssessment{
				Decision: "ready_for_review",
				Reason:   "work complete, awaiting review",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete must survive the CAS miss (error only logged): %v", err)
	}
	if a.RawSession.Goal == nil || a.RawSession.Goal.Status != "ready_for_review" {
		t.Fatalf("goal must stay paused in ready_for_review, got %+v", a.RawSession.Goal)
	}
	if planner.claimed != nil {
		t.Fatalf("CAS-missed write must not be recorded as applied: %+v", planner.claimed)
	}
}

// TestHandleTurnComplete_ReadyForReview_PausesForReview reproduces the
// "stuck pending" bug from the old behavior, but with the corrected
// expectation: after ready_for_review, the agent intentionally does NOT
// auto-start queued user turns. The agent is paused awaiting external
// review; queued messages resume only via resume_from_review (map owner
// reject) or are discarded on teardown (approve).
func TestHandleTurnComplete_ReadyForReview_PausesForReview(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a := &Actor{
		actorID:       "agent-rfr-drain",
		agentKind:     "worker",
		ActiveTurnRef: "turn-rfr-drain",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-rfr-drain", Role: "user", State: "completed"},
				{ID: "turn-rfr-drain", Role: "assistant", State: "running"},
				{ID: "user-stuck", Role: "user", State: "completed", UserInput: "queued during finalize"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			NextSeq: 1,
			Goal: &gen.SessionGoal{
				Condition: "do the thing",
				Status:    "active",
				Confirmed: true,
				MaxTurns:  10,
				TurnCount: 1,
			},
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-rfr-drain", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-rfr-drain",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
	}
	a.takeSnapshot()

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-rfr-drain",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
			Assessment: &gen.TurnAssessment{
				Decision: "ready_for_review",
				Reason:   "work complete, awaiting review",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	if a.RawSession.Goal == nil || a.RawSession.Goal.Status != "ready_for_review" {
		t.Fatalf("goal not preserved as ready_for_review: %+v", a.RawSession.Goal)
	}

	// After ready_for_review, the agent must NOT start a new turn from
	// queued messages. ActiveTurnRef should be cleared (no active turn),
	// and the queued user turn remains pending until resume_from_review.
	if a.ActiveTurnRef != "" {
		t.Fatalf("agent should be paused after ready_for_review, but ActiveTurnRef=%q (turn was auto-started)", a.ActiveTurnRef)
	}
}

// TestHandleTurnComplete_CompleteCandidate_DrainsQueuedUserTurn is the symmetric
// case for complete_candidate: the branch also returned before the shared
// maybeAutoStartTurn, so a finalize-window user message was orphaned even though
// the goal completed.
func TestHandleTurnComplete_CompleteCandidate_DrainsQueuedUserTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a := &Actor{
		actorID:       "agent-cc-drain",
		agentKind:     "worker",
		ActiveTurnRef: "turn-cc-drain",
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{tag: "system"},
		},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-cc-drain", Role: "user", State: "completed"},
				{ID: "turn-cc-drain", Role: "assistant", State: "running"},
				{ID: "user-stuck", Role: "user", State: "completed", UserInput: "queued during finalize"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			NextSeq: 1,
			Goal: &gen.SessionGoal{
				Condition: "do the thing",
				Status:    "active",
				Confirmed: true,
				MaxTurns:  10,
				TurnCount: 1,
			},
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-cc-drain", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-cc-drain",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
	}
	a.takeSnapshot()

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-cc-drain",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
			Assessment: &gen.TurnAssessment{
				Decision: "complete_candidate",
				Reason:   "goal achieved",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	// Goal must be cleared on complete_candidate.
	if a.RawSession.Goal != nil {
		t.Fatalf("goal not cleared on complete_candidate: %+v", a.RawSession.Goal)
	}

	// The queued user turn must be drained (goal cleared, fresh turn started).
	if a.ActiveTurnRef == "" {
		t.Fatal("queued user turn orphaned after complete_candidate: ActiveTurnRef empty, message stuck pending")
	}
}

// TestHandleTurnComplete_CompleteCandidate_BoundRootAgent verifies the
// no-parent completion path for a goal bound to a task card: the card is
// marked done and the goal is cleared (unbinding the card and unmounting it
// from context), instead of pausing on a non-existent reviewer.
func TestHandleTurnComplete_CompleteCandidate_BoundRootAgent(t *testing.T) {
	planner := &goalCardProjectPlanner{}
	ctx := newGoalCardApprovalCtx(planner)
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)

	a := &Actor{
		actorID:       "agent-cc-bound",
		agentKind:     "coder",
		ActiveTurnRef: "turn-cc-bound",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-cc-bound", Role: "user", State: "completed"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			NextSeq: 1,
			Goal: &gen.SessionGoal{
				Condition:       "do the thing",
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: "ticket-bound-cc",
				MaxTurns:        10,
				TurnCount:       1,
			},
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-cc-bound", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-cc-bound",
			State:          "running",
			StartStepCount: 0,
		},
	}

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-cc-bound",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
			Assessment: &gen.TurnAssessment{
				Decision: "complete_candidate",
				Reason:   "task card resolved",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	// The bound card must be marked done before the binding is cleared.
	if planner.claimed == nil {
		t.Fatal("expected wiki_set_status call for the bound card")
	}
	if planner.claimed.ID != "ticket-bound-cc" || planner.claimed.Status != "done" {
		t.Fatalf("bound card status = %+v, want ID=ticket-bound-cc Status=done", planner.claimed)
	}
	if planner.claimed.Evidence != nil {
		t.Fatalf("complete_candidate should pass nil evidence, got %v", planner.claimed.Evidence)
	}

	// Goal must be cleared: the card is unbound and unmounted from context.
	if a.RawSession.Goal != nil {
		t.Fatalf("goal not cleared on complete_candidate: %+v", a.RawSession.Goal)
	}
}

// TestHandleTurnComplete_OrphanedUserTurnCreatesSuccessor verifies that a user
// message submitted while the previous turn was finalizing (the "orphaned user
// turn" path) gets a successor assistant turn with a new TurnID/TurnOrder, while
// the previous terminal record remains untouched.
func TestHandleTurnComplete_OrphanedUserTurnCreatesSuccessor(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-2", Role: "assistant", State: "running", TurnOrder: 2},
				{ID: "user-turn-3", Role: "user", State: "completed", TurnOrder: 3, UserInput: "orphaned message"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			NextSeq:       1,
			NextTurnOrder: 4,
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-2",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{tag: "system"},
		},
	}
	a.takeSnapshot()

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-2",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	var terminal, successor *domain.Turn
	for i := range a.Session.Turns {
		t := &a.Session.Turns[i]
		if t.ID == "turn-2" {
			terminal = t
			continue
		}
		if t.Role == "assistant" {
			if successor == nil || t.TurnOrder > successor.TurnOrder {
				successor = t
			}
		}
	}
	if terminal == nil {
		t.Fatal("original terminal turn-2 missing")
	}
	if terminal.State != "completed" {
		t.Errorf("terminal turn state = %q, want completed", terminal.State)
	}
	if terminal.TurnOrder != 2 {
		t.Errorf("terminal TurnOrder = %d, want 2", terminal.TurnOrder)
	}

	if a.Session.ActiveHead != 2 {
		t.Errorf("ActiveHead = %d, want 2 (orphaned user turn)", a.Session.ActiveHead)
	}

	if successor == nil {
		t.Fatal("no successor assistant turn created for orphaned user turn")
	}
	if successor.ID == "" || successor.ID == terminal.ID {
		t.Errorf("successor assistant ID = %q, want new ID", successor.ID)
	}
	if successor.TurnOrder <= terminal.TurnOrder {
		t.Errorf("successor assistant TurnOrder = %d, want > terminal %d", successor.TurnOrder, terminal.TurnOrder)
	}
}

// TestStartTurnWithName_TerminalRecordDoesNotResurrect verifies that when a
// terminal assistant turn already occupies the generated turn name, a fresh
// assistant record is created instead of reviving the terminal one. The
// terminal record keeps its original state/order.
func TestStartTurnWithName_TerminalRecordDoesNotResurrect(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-2", Role: "assistant", State: "completed", TurnOrder: 2},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			NextSeq:       1,
			NextTurnOrder: 3,
		},
		snapshotReady: true,
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{tag: "system"},
		},
	}
	a.takeSnapshot()

	_, err := a.startTurnWithName(ctx, domain.TurnInput{Text: "new input"}, "turn-2")
	// The lifecycle reducer rejects Started on a terminal record, so the call
	// fails rather than resurrecting turn-2. ActiveTurnRef must be cleared so the
	// agent is not left pointing at a dead turn.
	if err == nil {
		t.Fatal("startTurnWithName should error when turn name collides with a terminal record")
	}
	if a.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef = %q, want empty after lifecycle rejection", a.ActiveTurnRef)
	}

	var terminal *domain.Turn
	for i := range a.Session.Turns {
		t := &a.Session.Turns[i]
		if t.ID == "turn-2" {
			terminal = t
			break
		}
	}
	if terminal == nil {
		t.Fatal("terminal turn-2 missing")
	}
	if terminal.State != "completed" {
		t.Errorf("terminal turn state = %q, want completed", terminal.State)
	}
	if terminal.TurnOrder != 2 {
		t.Errorf("terminal TurnOrder = %d, want 2", terminal.TurnOrder)
	}
}

// TestHandleTurnComplete_ResidualPendingSubmitsCreateSuccessor verifies that
// pending submits keyed by the assistant turn ID (residuals that arrived after
// the engine stopped accepting injects but before completion) are promoted into
// a new user turn and a successor assistant turn with distinct ID/order.
func TestHandleTurnComplete_ResidualPendingSubmitsCreateSuccessor(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-2", Role: "assistant", State: "running", TurnOrder: 2},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			NextSeq:       1,
			NextTurnOrder: 3,
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-2",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{tag: "system"},
		},
	}
	a.takeSnapshot()
	a.pendingSubmits = map[string][]domain.PendingSubmit{
		"turn-2": {{ID: "ps-1", Text: "follow-up message"}},
	}

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-2",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	if _, ok := a.pendingSubmits["turn-2"]; ok {
		t.Errorf("pendingSubmits[turn-2] not cleared after promotion")
	}

	var terminal, successor *domain.Turn
	for i := range a.Session.Turns {
		t := &a.Session.Turns[i]
		if t.ID == "turn-2" {
			terminal = t
			continue
		}
		if t.Role == "assistant" {
			if successor == nil || t.TurnOrder > successor.TurnOrder {
				successor = t
			}
		}
	}
	if terminal == nil {
		t.Fatal("original terminal turn-2 missing")
	}
	if terminal.State != "completed" {
		t.Errorf("terminal turn state = %q, want completed", terminal.State)
	}
	if terminal.TurnOrder != 2 {
		t.Errorf("terminal TurnOrder = %d, want 2", terminal.TurnOrder)
	}

	newUserTurn := a.Session.Turns[a.Session.ActiveHead]
	if newUserTurn.Role != "user" {
		t.Errorf("ActiveHead turn role = %q, want user", newUserTurn.Role)
	}
	if newUserTurn.TurnOrder <= terminal.TurnOrder {
		t.Errorf("successor user turn TurnOrder = %d, want > terminal %d", newUserTurn.TurnOrder, terminal.TurnOrder)
	}
	if newUserTurn.UserInput != "follow-up message" {
		t.Errorf("successor user input = %q, want %q", newUserTurn.UserInput, "follow-up message")
	}

	if successor == nil {
		t.Fatal("no successor assistant turn created")
	}
	if successor.ID == "" || successor.ID == terminal.ID {
		t.Errorf("successor assistant ID = %q, want new ID", successor.ID)
	}
	if successor.TurnOrder <= terminal.TurnOrder {
		t.Errorf("successor assistant TurnOrder = %d, want > terminal %d", successor.TurnOrder, terminal.TurnOrder)
	}
	if successor.State != "running" {
		t.Errorf("successor assistant state = %q, want running", successor.State)
	}
}

// TestHandleTurnComplete_ResidualPendingSubmitsMergeByMeta verifies that the
// promotion path applies the same meta-based merge rule as consumePendingSubmits:
// same-meta submits join into one user turn with newline-separated text,
// distinct-meta submits become separate user turns, and the first user turn
// gets a successor assistant turn.
func TestHandleTurnComplete_ResidualPendingSubmitsMergeByMeta(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-2", Role: "assistant", State: "running", TurnOrder: 2},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			NextSeq:       1,
			NextTurnOrder: 3,
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-2",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{tag: "system"},
		},
	}
	a.takeSnapshot()
	a.pendingSubmits = map[string][]domain.PendingSubmit{
		"turn-2": {
			{ID: "ps-a1", Text: "first", Meta: "user|alice|Alice"},
			{ID: "ps-a2", Text: "second", Meta: "user|alice|Alice"},
			{ID: "ps-b1", Text: "third", Meta: "user|bob|Bob"},
		},
	}

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-2",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	var userTurns []domain.Turn
	for i := range a.Session.Turns {
		if a.Session.Turns[i].Role == "user" {
			userTurns = append(userTurns, a.Session.Turns[i])
		}
	}
	if len(userTurns) != 3 {
		t.Fatalf("got %d user turns, want 3 (original + 2 promoted groups)", len(userTurns))
	}

	aliceTurn := userTurns[1]
	if aliceTurn.UserInput != "first\nsecond" {
		t.Errorf("alice merged input = %q, want %q", aliceTurn.UserInput, "first\nsecond")
	}
	bobTurn := userTurns[2]
	if bobTurn.UserInput != "third" {
		t.Errorf("bob input = %q, want %q", bobTurn.UserInput, "third")
	}

	if a.Session.ActiveHead != int32(len(a.Session.Turns)-3) {
		t.Errorf("ActiveHead = %d, want %d (first promoted user turn)", a.Session.ActiveHead, len(a.Session.Turns)-3)
	}

	last := a.Session.Turns[len(a.Session.Turns)-1]
	if last.Role != "assistant" || last.State != "running" {
		t.Errorf("last turn = %q/%q, want assistant/running", last.Role, last.State)
	}
}

// TestPendingSubmitsToTurnInputs verifies the merge helper used by the
// residual promotion path: same meta merges text with newlines, distinct meta
// stays separate, and images/attachments are concatenated in queue order.
func TestPendingSubmitsToTurnInputs(t *testing.T) {
	inputs := pendingSubmitsToTurnInputs([]domain.PendingSubmit{
		{ID: "ps-1", Text: "one", Meta: "user|alice|Alice"},
		{ID: "ps-2", Text: "two", Meta: "user|alice|Alice"},
		{ID: "ps-3", Text: "three", Meta: "user|bob|Bob"},
	})
	if len(inputs) != 2 {
		t.Fatalf("got %d inputs, want 2", len(inputs))
	}
	if inputs[0].Text != "one\ntwo" {
		t.Errorf("first group text = %q, want %q", inputs[0].Text, "one\ntwo")
	}
	if inputs[0].Meta != "user|alice|Alice" {
		t.Errorf("first group meta = %q, want %q", inputs[0].Meta, "user|alice|Alice")
	}
	if inputs[1].Text != "three" {
		t.Errorf("second group text = %q, want %q", inputs[1].Text, "three")
	}
	if inputs[1].Meta != "user|bob|Bob" {
		t.Errorf("second group meta = %q, want %q", inputs[1].Meta, "user|bob|Bob")
	}
}

// TestGoalContinueText verifies the randomized goal continuation prompt:
// variant selection wraps, the turn progress marker is appended, and repeated
// turns never produce byte-identical text.
func TestGoalContinueText(t *testing.T) {
	if got := goalContinueText(0, 2, 10); got != "We'll continue working toward the active goal. (turn 3 of 10)" {
		t.Fatalf("goalContinueText(0, 2, 10) = %q", got)
	}
	if goalContinueText(len(goalContinuePool), 0, 5) != goalContinueText(0, 0, 5) {
		t.Fatal("goalContinueText should wrap idx into the variant pool")
	}
	if got := goalContinueText(1, 0, 0); got != goalContinuePool[1] {
		t.Fatalf("goalContinueText with maxTurns<=0 = %q, want bare variant %q", got, goalContinuePool[1])
	}
	// All variants are distinct so random picks actually vary the prompt.
	seen := map[string]bool{}
	for _, v := range goalContinuePool {
		if seen[v] {
			t.Fatalf("duplicate variant %q", v)
		}
		seen[v] = true
	}
	// Even with the same variant picked twice, consecutive turns differ via
	// the progress marker.
	if goalContinueText(0, 3, 10) == goalContinueText(0, 4, 10) {
		t.Fatal("consecutive turns should not share byte-identical prompts")
	}
}

// readyReviewProjectPlanner is a test double for the parent project actor's
// planner surface exercised by the ready_for_review worktree-clean gate: it
// answers project.workflow_worktree_clean_check from configurable state,
// records wiki_set_status calls, and flags review_changeset_freeze invocations.
type readyReviewProjectPlanner struct {
	clean         bool     // clean-check response
	dirtyFiles    []string // dirty-file list returned when !clean
	cleanCheckErr error    // when set, the clean-check invoke fails

	claimed      *domain.WikiSetStatusReq
	claimErr     error
	freezeCalled bool
}

func (p *readyReviewProjectPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, errors.New("not implemented")
}

func (p *readyReviewProjectPlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return promise.Reject[any](errors.New("not implemented"))
}

func (p *readyReviewProjectPlanner) Call(_ context.Context, _ ref.Ref, callID string, payload any) *promise.Promise[any] {
	switch callID {
	case "project.workflow_worktree_clean_check":
		if p.cleanCheckErr != nil {
			return promise.Reject[any](p.cleanCheckErr)
		}
		return promise.Async(func(resolve func(any), _ func(any)) {
			resolve(gen.ProjectWorktreeCleanCheckResp{Clean: p.clean, DirtyFiles: p.dirtyFiles})
		})
	case "project.wiki_set_status":
		if p.claimErr != nil {
			return promise.Reject[any](p.claimErr)
		}
		req := domain.WikiSetStatusReq{}
		switch v := payload.(type) {
		case domain.WikiSetStatusReq:
			req = v
		case *domain.WikiSetStatusReq:
			req = *v
		case []byte:
			_ = json.Unmarshal(v, &req)
		}
		copyReq := req
		p.claimed = &copyReq
		return promise.Async(func(resolve func(any), _ func(any)) {
			resolve(domain.WikiSetStatusResp{PreviousStatus: "doing"})
		})
	case "project.review_changeset_freeze":
		p.freezeCalled = true
		return promise.Async(func(resolve func(any), _ func(any)) {
			resolve(nil)
		})
	default:
		return promise.Reject[any](fmt.Errorf("unexpected call %q", callID))
	}
}

// newReadyReviewCtx wires a FakeCtx whose parent resolves to a project actor
// served by the given planner.
func newReadyReviewCtx(planner *readyReviewProjectPlanner) *testutil.FakeCtx {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.PlannerFn = func() actor.Planner { return planner }
	return ctx
}

// TestCheckWorktreeClean covers the helper's contract: fail-open when there is
// no parent project actor, no planner, or the invoke fails; clean passthrough
// for a clean response; dirty passthrough with the dirty-file list.
func TestCheckWorktreeClean(t *testing.T) {
	a := &Actor{actorID: "agent-clean"}

	t.Run("no parent fails open", func(t *testing.T) {
		ctx := testutil.AnonCtx(testutil.GenActorID())
		// no ParentRef, no PlannerFn
		clean, files := a.checkWorktreeClean(ctx)
		if !clean || files != nil {
			t.Fatalf("checkWorktreeClean without parent = (%v, %v), want (true, nil)", clean, files)
		}
	})

	t.Run("invoke error fails open", func(t *testing.T) {
		planner := &readyReviewProjectPlanner{cleanCheckErr: errors.New("project unreachable")}
		ctx := newReadyReviewCtx(planner)
		clean, files := a.checkWorktreeClean(ctx)
		if !clean || files != nil {
			t.Fatalf("checkWorktreeClean with invoke error = (%v, %v), want (true, nil)", clean, files)
		}
	})

	t.Run("unexpected response type fails open", func(t *testing.T) {
		// planner returns a wrong-typed promise for the clean check.
		planner := &readyReviewProjectPlanner{clean: true}
		ctx := newReadyReviewCtx(planner)
		ctx.PlannerFn = func() actor.Planner {
			return &stubPlannerWrongCleanResp{}
		}
		clean, files := a.checkWorktreeClean(ctx)
		if !clean || files != nil {
			t.Fatalf("checkWorktreeClean with wrong resp type = (%v, %v), want (true, nil)", clean, files)
		}
	})

	t.Run("clean resp passes through", func(t *testing.T) {
		planner := &readyReviewProjectPlanner{clean: true}
		ctx := newReadyReviewCtx(planner)
		clean, files := a.checkWorktreeClean(ctx)
		if !clean || files != nil {
			t.Fatalf("checkWorktreeClean clean = (%v, %v), want (true, nil)", clean, files)
		}
	})

	t.Run("dirty resp returns dirty files", func(t *testing.T) {
		planner := &readyReviewProjectPlanner{clean: false, dirtyFiles: []string{"a.go", "b.go"}}
		ctx := newReadyReviewCtx(planner)
		clean, files := a.checkWorktreeClean(ctx)
		if clean {
			t.Fatal("checkWorktreeClean dirty returned clean=true")
		}
		if len(files) != 2 || files[0] != "a.go" || files[1] != "b.go" {
			t.Fatalf("checkWorktreeClean dirtyFiles = %v, want [a.go b.go]", files)
		}
	})

	t.Run("dirty resp passes agent id", func(t *testing.T) {
		planner := &readyReviewProjectPlanner{}
		ctx := newReadyReviewCtx(planner)
		ctx.PlannerFn = func() actor.Planner {
			return &stubPlannerCleanReq{capture: func(req any) {
				r, ok := req.(gen.ProjectWorktreeCleanCheckReq)
				if !ok || r.AgentActorID != "agent-clean" {
					t.Errorf("clean check req agent id = %+v, want AgentActorID=agent-clean", req)
				}
			}}
		}
		a.checkWorktreeClean(ctx)
	})
}

// stubPlannerWrongCleanResp resolves the clean check with an unexpected type
// to exercise the fail-open branch.
type stubPlannerWrongCleanResp struct{}

func (p *stubPlannerWrongCleanResp) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, errors.New("not implemented")
}
func (p *stubPlannerWrongCleanResp) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return promise.Reject[any](errors.New("not implemented"))
}
func (p *stubPlannerWrongCleanResp) Call(_ context.Context, _ ref.Ref, callID string, _ any) *promise.Promise[any] {
	if callID == "project.workflow_worktree_clean_check" {
		return promise.Async(func(resolve func(any), _ func(any)) {
			resolve("not-a-resp")
		})
	}
	return promise.Reject[any](fmt.Errorf("unexpected call %q", callID))
}

// stubPlannerCleanReq resolves the clean check with a clean resp and forwards a
// copy of the request payload to capture.
type stubPlannerCleanReq struct {
	capture func(req any)
}

func (p *stubPlannerCleanReq) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, errors.New("not implemented")
}
func (p *stubPlannerCleanReq) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return promise.Reject[any](errors.New("not implemented"))
}
func (p *stubPlannerCleanReq) Call(_ context.Context, _ ref.Ref, callID string, payload any) *promise.Promise[any] {
	if callID == "project.workflow_worktree_clean_check" {
		if p.capture != nil {
			p.capture(payload)
		}
		return promise.Async(func(resolve func(any), _ func(any)) {
			resolve(gen.ProjectWorktreeCleanCheckResp{Clean: true})
		})
	}
	return promise.Reject[any](fmt.Errorf("unexpected call %q", callID))
}

// newReadyReviewBounceActor returns a worker actor in the same shape the
// existing ready_for_review tests use, plus the aggregator cache + snapshot
// preconditions needed for maybeAutoStartTurn to actually start the bounce
// turn (mirrors the complete_candidate drain test).
//
// The actor carries a non-empty RawSession.ActiveWorkflow.WorktreeID so the
// changeset freeze gate (which skips when no worktree is bound) is engaged;
// otherwise the no-worktree worker path would silently skip
// review_changeset_freeze and break CleanWorktreePasses's freezeCalled
// assertion.
func newReadyReviewBounceActor() *Actor {
	a := &Actor{
		actorID:       "agent-bounce",
		agentKind:     "worker",
		parentAgentID: "parent-agent-1",
		ActiveTurnRef: "turn-bounce",
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{tag: "system"},
		},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-bounce", Role: "user", State: "completed"},
				{ID: "turn-bounce", Role: "assistant", State: "running"},
			},
		},
		RawSession: domain.RawSession{
			NextSeq: 1,
			Goal: &gen.SessionGoal{
				Condition:       "do the thing",
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: "ticket-bounce",
				MaxTurns:        10,
				TurnCount:       1,
			},
			ActiveWorkflow: &gen.ActiveWorkflow{
				MapCardID:  "map-bounce",
				WorktreeID: "wt-bounce",
			},
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-bounce", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-bounce",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
	}
	a.takeSnapshot()
	return a
}

func readyForReviewTurnReq() domain.AgentTurnCompleteReq {
	return domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-bounce",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
			Assessment: &gen.TurnAssessment{
				Decision: "ready_for_review",
				Reason:   "work complete, awaiting review",
				Evidence: []string{"evidence-one"},
				Outputs:  map[string]any{"report": "done"},
			},
		},
	}
}

// TestHandleTurnComplete_ReadyForReview_DirtyWorktreeBounces verifies the clean
// gate: when the bound worktree has uncommitted changes, the declaration is
// bounced back — Goal.Status is NOT set to ready_for_review, a bounce user
// message is enqueued (and auto-started), and the card is left untouched.
func TestHandleTurnComplete_ReadyForReview_DirtyWorktreeBounces(t *testing.T) {
	planner := &readyReviewProjectPlanner{clean: false, dirtyFiles: []string{"a.go", "b.go"}}
	ctx := newReadyReviewCtx(planner)
	a := newReadyReviewBounceActor()

	err := a.handleTurnComplete(ctx, readyForReviewTurnReq())
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	// Goal must survive and must NOT enter ready_for_review.
	if a.RawSession.Goal == nil {
		t.Fatal("goal was cleared on bounced ready_for_review, should survive")
	}
	if a.RawSession.Goal.Status == "ready_for_review" {
		t.Fatalf("Goal.Status = ready_for_review, want it to stay active after bounce")
	}
	if a.RawSession.Goal.Status != "active" {
		t.Fatalf("Goal.Status = %q, want active (unchanged)", a.RawSession.Goal.Status)
	}
	// The bound card must NOT be moved to pending_review.
	if planner.claimed != nil {
		t.Fatalf("wiki_set_status must not be called on a bounced declaration, got %+v", planner.claimed)
	}
	if planner.freezeCalled {
		t.Fatal("review_changeset_freeze must not be triggered on a bounced declaration")
	}

	// The bounce message must be queued as a user turn. The gate auto-starts
	// it, so the assistant turn for the bounce sits after it in the session —
	// locate the user turn by its message text instead of assuming it is last.
	var bounce *domain.Turn
	for i := range a.Session.Turns {
		t := &a.Session.Turns[i]
		if t.Role == "user" && strings.Contains(t.UserInput, "ready_for_review rejected") {
			bounce = t
			break
		}
	}
	if bounce == nil {
		t.Fatal("bounce message not found in Session.Turns")
	}
	if !strings.Contains(bounce.UserInput, "a.go, b.go") {
		t.Errorf("bounce message = %q, missing dirty-file preview", bounce.UserInput)
	}
	if !strings.Contains(bounce.UserInput, "(2 files:") {
		t.Errorf("bounce message = %q, missing file count", bounce.UserInput)
	}
	// The turn count must still have been recorded for the completed turn
	// before the bounce (recordGoalTurnCompletion ran before the gate).
	if a.RawSession.Goal.TurnCount != 2 {
		t.Errorf("Goal.TurnCount = %d, want 2 (completion counted before bounce)", a.RawSession.Goal.TurnCount)
	}

	// maybeAutoStartTurn must have picked the queued message up and started a
	// goal turn for it (ActiveTurnRef non-empty).
	if a.ActiveTurnRef == "" {
		t.Fatal("bounce message orphaned: maybeAutoStartTurn did not start a turn (ActiveTurnRef empty)")
	}
}

// TestHandleTurnComplete_ReadyForReview_CleanWorktreePasses verifies the clean
// gate lets a clean declaration through unchanged: Goal.Status becomes
// ready_for_review, the bound card moves to pending_review, and the changeset
// freeze is triggered.
func TestHandleTurnComplete_ReadyForReview_CleanWorktreePasses(t *testing.T) {
	planner := &readyReviewProjectPlanner{clean: true}
	ctx := newReadyReviewCtx(planner)
	a := newReadyReviewBounceActor()

	err := a.handleTurnComplete(ctx, readyForReviewTurnReq())
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	if a.RawSession.Goal == nil {
		t.Fatal("goal was cleared on ready_for_review, should survive")
	}
	if a.RawSession.Goal.Status != "ready_for_review" {
		t.Fatalf("Goal.Status = %q, want ready_for_review", a.RawSession.Goal.Status)
	}
	if a.RawSession.Goal.Outputs == nil || a.RawSession.Goal.Outputs["report"] != "done" {
		t.Fatalf("Goal.Outputs = %+v, want report=done", a.RawSession.Goal.Outputs)
	}
	if planner.claimed == nil || planner.claimed.ID != "ticket-bounce" || planner.claimed.Status != "pending_review" {
		t.Fatalf("bound card status = %+v, want ID=ticket-bounce Status=pending_review", planner.claimed)
	}
	if !planner.freezeCalled {
		t.Fatal("review_changeset_freeze was not triggered on a clean ready_for_review")
	}
}

// TestHandleTurnComplete_ReadyForReview_CleanCheckErrorFailOpen verifies that
// an invoke failure of the clean check fails open: the declaration proceeds to
// ready_for_review instead of being blocked or stuck.
func TestHandleTurnComplete_ReadyForReview_CleanCheckErrorFailOpen(t *testing.T) {
	planner := &readyReviewProjectPlanner{cleanCheckErr: errors.New("project unreachable")}
	ctx := newReadyReviewCtx(planner)
	a := newReadyReviewBounceActor()

	err := a.handleTurnComplete(ctx, readyForReviewTurnReq())
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	if a.RawSession.Goal == nil || a.RawSession.Goal.Status != "ready_for_review" {
		t.Fatalf("invoke failure must fail open into ready_for_review, got goal=%+v", a.RawSession.Goal)
	}
	if planner.claimed == nil || planner.claimed.Status != "pending_review" {
		t.Fatalf("bound card status = %+v, want pending_review", planner.claimed)
	}
}

// TestHandleTurnComplete_ReadyForReview_NoWorktreeSkipsFreeze pins the
// agent-side no-worktree freeze skip: a worker with no workflow worktree
// (e.g. main-repo worker writing directly into the trunk) declares
// ready_for_review normally — the bound card moves to pending_review,
// Goal.Status becomes ready_for_review — but review_changeset_freeze is
// NOT invoked. The project-side freeze handler applies the same skip as
// depth-in-defense (see TestReviewChangeset_FreezeNoWorktreeSkipsFreeze).
// Mirrors how checkWorktreeClean already treats no-worktree as Clean=true:
// the freeze path is gated on the same condition.
func TestHandleTurnComplete_ReadyForReview_NoWorktreeSkipsFreeze(t *testing.T) {
	planner := &readyReviewProjectPlanner{clean: true}
	ctx := newReadyReviewCtx(planner)
	a := newReadyReviewBounceActor()
	// No workflow worktree bound: this is the no-git / main-repo worker
	// path. ActiveWorkflow is cleared so activeWorkflowWorktreeID() == "".
	a.RawSession.ActiveWorkflow = nil

	err := a.handleTurnComplete(ctx, readyForReviewTurnReq())
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	if a.RawSession.Goal == nil || a.RawSession.Goal.Status != "ready_for_review" {
		t.Fatalf("no-worktree ready_for_review must still proceed, got goal=%+v", a.RawSession.Goal)
	}
	if planner.claimed == nil || planner.claimed.Status != "pending_review" {
		t.Fatalf("bound card status = %+v, want pending_review", planner.claimed)
	}
	if planner.freezeCalled {
		t.Fatal("review_changeset_freeze must be skipped when no workflow worktree is bound")
	}
}
