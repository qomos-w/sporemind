package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestHandleTurnAssess(t *testing.T) {
	a := &Actor{parentAgentID: "parent-agent-1"}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	if got, err := a.handleTurnAssess(ctx, gen.TurnAssessment{Decision: "complete_candidate"}); err != nil || got == "" {
		t.Fatalf("valid assessment failed: result=%q err=%v", got, err)
	}
	if _, err := a.handleTurnAssess(ctx, gen.TurnAssessment{Decision: "continue"}); err == nil {
		t.Fatal("expected \"continue\" to be rejected — not calling turn.assess is the way to continue")
	}
	if got, err := a.handleTurnAssess(ctx, gen.TurnAssessment{Decision: "ready_for_review"}); err != nil || got == "" {
		t.Fatalf("ready_for_review assessment failed: result=%q err=%v", got, err)
	}
	if _, err := a.handleTurnAssess(ctx, gen.TurnAssessment{Decision: "done"}); err == nil {
		t.Fatal("expected invalid assessment decision to fail")
	}
}

func TestHandleTurnAssess_BoundGoalRejectsCompleteCandidate(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())

	// Unbound goal: complete_candidate is allowed (existing behavior).
	unbound := &Actor{}
	unbound.RawSession.Goal = &gen.SessionGoal{Condition: "c", Confirmed: true}
	if _, err := unbound.handleTurnAssess(ctx, gen.TurnAssessment{Decision: "complete_candidate"}); err != nil {
		t.Fatalf("unbound goal should allow complete_candidate, got %v", err)
	}

	// Bound goal with a parent agent (any kind): complete_candidate must be
	// rejected — the agent may not self-complete and unbind from its task card
	// before external review, regardless of agent kind.
	for _, kind := range []string{domain.AgentKindWorker, domain.AgentKindCoder, domain.AgentKindCoordinator} {
		bound := &Actor{agentKind: kind, parentAgentID: "parent-agent-1"}
		bound.RawSession.Goal = &gen.SessionGoal{Condition: "c", Confirmed: true, BoundTaskCardID: "ticket-42"}
		if _, err := bound.handleTurnAssess(ctx, gen.TurnAssessment{Decision: "complete_candidate"}); err == nil {
			t.Fatalf("bound goal (kind=%s) should reject complete_candidate", kind)
		} else if !strings.Contains(err.Error(), "ready_for_review") {
			t.Fatalf("error should point at ready_for_review, got %v", err)
		}
		if _, err := bound.handleTurnAssess(ctx, gen.TurnAssessment{Decision: "ready_for_review"}); err != nil {
			t.Fatalf("bound goal (kind=%s) should allow ready_for_review, got %v", kind, err)
		}
	}
}

// TestHandleTurnAssess_BoundGoalNoParent verifies that a root agent (no
// parent agent) bound to a task card completes directly: complete_candidate
// is allowed, and ready_for_review is rejected because no reviewer exists to
// resume it.
func TestHandleTurnAssess_BoundGoalNoParent(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	bound := &Actor{agentKind: domain.AgentKindCoder}
	bound.RawSession.Goal = &gen.SessionGoal{Condition: "c", Confirmed: true, BoundTaskCardID: "ticket-42"}
	if _, err := bound.handleTurnAssess(ctx, gen.TurnAssessment{Decision: "complete_candidate"}); err != nil {
		t.Fatalf("root bound goal should allow complete_candidate, got %v", err)
	}
	if _, err := bound.handleTurnAssess(ctx, gen.TurnAssessment{Decision: "ready_for_review"}); err == nil {
		t.Fatal("root agent should reject ready_for_review")
	} else if !strings.Contains(err.Error(), "complete_candidate") {
		t.Fatalf("error should point at complete_candidate, got %v", err)
	}
}

// TestRunOneCall_TurnAssessValidator verifies the engine-side turn_assess
// interception honors assessValidator (the path the LLM actually hits —
// the engine intercepts turn_assess before generic dispatch).
func TestRunOneCall_TurnAssessValidator(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	call := pendingToolCall{ID: "t1", CallableID: "turn_assess", Input: `{"Decision":"complete_candidate"}`}

	bound := &Actor{agentKind: domain.AgentKindCoder, parentAgentID: "parent-agent-1"}
	bound.RawSession.Goal = &gen.SessionGoal{Condition: "c", Confirmed: true, BoundTaskCardID: "ticket-42"}
	e := &turnEngine{assessValidator: bound.validateAssessDecision}
	res := e.runOneCall(ctx, nil, call, nil, "step-1")
	if !res.isErr {
		t.Fatal("engine should reject complete_candidate for a bound goal")
	}
	if !strings.Contains(res.out, "ready_for_review") {
		t.Fatalf("error output should point at ready_for_review, got %q", res.out)
	}
	if e.assessment != nil {
		t.Fatal("rejected assessment must not be recorded")
	}

	// Nil validator (unbound path): complete_candidate records as before.
	e2 := &turnEngine{}
	res2 := e2.runOneCall(ctx, nil, call, nil, "step-1")
	if res2.isErr {
		t.Fatalf("nil validator should allow complete_candidate, got %q", res2.out)
	}
	if e2.assessment == nil || e2.assessment.Decision != "complete_candidate" {
		t.Fatalf("assessment not recorded: %+v", e2.assessment)
	}
}

func TestGoalSummary(t *testing.T) {
	a := &Actor{}
	if s := a.goalSummary(); s != nil {
		t.Fatalf("nil goal should produce nil summary, got %+v", s)
	}
	if id := a.boundTaskCardID(); id != "" {
		t.Fatalf("nil goal should produce empty boundTaskCardID, got %q", id)
	}
	a.RawSession.Goal = &gen.SessionGoal{
		Condition:       "implement feature X",
		Status:          "ready_for_review",
		Confirmed:       true,
		BoundTaskCardID: "ticket-42",
		TurnCount:       3,
		MaxTurns:        10,
	}
	s := a.goalSummary()
	if s == nil {
		t.Fatal("active goal should produce non-nil summary")
	}
	if s.Condition != "implement feature X" || s.Status != "ready_for_review" || !s.Confirmed {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if s.BoundTaskCardID != "ticket-42" || s.TurnCount != 3 || s.MaxTurns != 10 {
		t.Fatalf("unexpected summary fields: %+v", s)
	}
	if id := a.boundTaskCardID(); id != "ticket-42" {
		t.Fatalf("boundTaskCardID = %q, want ticket-42", id)
	}
}

func TestApplyGoalSubmit(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())

	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition: "refactor auth module",
				MaxTurns:  20,
				TurnCount: 0,
			},
		},
	}

	requestID, err := a.applyGoalSubmit(ctx, gen.AgentGoalSubmitReq{
		InterpretedGoal: "Migrate authentication from session-based to JWT tokens, maintain API compatibility, ensure test coverage >= 80%",
	})
	if err != nil {
		t.Fatalf("applyGoalSubmit: %v", err)
	}
	if requestID == "" {
		t.Fatal("expected non-empty requestID")
	}
	if a.RawSession.Goal.InterpretedGoal != "Migrate authentication from session-based to JWT tokens, maintain API compatibility, ensure test coverage >= 80%" {
		t.Fatalf("unexpected InterpretedGoal: %q", a.RawSession.Goal.InterpretedGoal)
	}
	if !a.goalSubmitPending {
		t.Fatal("expected goalSubmitPending to be true")
	}
	if a.goalSubmitRequestID != requestID {
		t.Fatalf("expected goalSubmitRequestID %q, got %q", requestID, a.goalSubmitRequestID)
	}
	if len(a.RawSession.Goal.PlanCardIDs) != 0 {
		t.Fatalf("goal_submit must not link plan cards, got %v", a.RawSession.Goal.PlanCardIDs)
	}
}

func TestApplyGoalSubmitNoGoal(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{RawSession: gen.RawSession{}}

	_, err := a.applyGoalSubmit(ctx, gen.AgentGoalSubmitReq{InterpretedGoal: "test"})
	if err == nil {
		t.Fatal("expected error when no active goal")
	}
}

func TestApplyGoalSubmitAlreadyConfirmed(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition:       "refactor auth",
				InterpretedGoal: "already confirmed goal",
				Confirmed:       true,
				MaxTurns:        20,
			},
		},
	}

	_, err := a.applyGoalSubmit(ctx, gen.AgentGoalSubmitReq{InterpretedGoal: "new interpretation"})
	if err == nil {
		t.Fatal("expected error when goal already confirmed")
	}
}

func TestApplyGoalSubmitEmpty(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "test", MaxTurns: 20},
		},
	}

	_, err := a.applyGoalSubmit(ctx, gen.AgentGoalSubmitReq{InterpretedGoal: "  "})
	if err == nil {
		t.Fatal("expected error for empty InterpretedGoal")
	}
}

func TestResolveGoalSubmitApprove(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition:       "refactor auth",
				InterpretedGoal: "Migrate to JWT",
				MaxTurns:        20,
			},
		},
		goalSubmitPending:   true,
		goalSubmitRequestID: "req-1",
	}

	decision, requestID, _ := a.resolveGoalSubmit(ctx, `{"decision":"approve"}`)
	if decision != "approve" {
		t.Fatalf("expected approve, got %q", decision)
	}
	if requestID != "req-1" {
		t.Fatalf("expected req-1, got %q", requestID)
	}
	if !a.RawSession.Goal.Confirmed {
		t.Fatal("expected Confirmed to be true after approve")
	}
	if a.goalSubmitPending {
		t.Fatal("expected goalSubmitPending to be false after resolve")
	}
	if a.title != "Migrate to JWT" || a.Title != "Migrate to JWT" {
		t.Fatalf("expected title mirrored from confirmed intent, got title=%q Title=%q", a.title, a.Title)
	}
}

func TestResolveGoalSubmitReject(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition:       "refactor auth",
				InterpretedGoal: "Migrate to JWT",
				MaxTurns:        20,
			},
		},
		goalSubmitPending:   true,
		goalSubmitRequestID: "req-1",
	}

	decision, _, feedback := a.resolveGoalSubmit(ctx, `{"decision":"reject","feedback":"don't touch the database schema"}`)
	if decision != "reject" {
		t.Fatalf("expected reject, got %q", decision)
	}
	if feedback != "don't touch the database schema" {
		t.Fatalf("unexpected feedback: %q", feedback)
	}
	if a.RawSession.Goal.Confirmed {
		t.Fatal("expected Confirmed to remain false after reject")
	}
}

func TestExpireGoalSubmit(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		goalSubmitPending:   true,
		goalSubmitRequestID: "req-1",
	}

	a.expireGoalSubmit(ctx, "req-1")
	if a.goalSubmitPending {
		t.Fatal("expected goalSubmitPending to be false after expire")
	}
	if a.goalSubmitRequestID != "" {
		t.Fatal("expected goalSubmitRequestID to be empty after expire")
	}
}

func TestEmitGoalSubmitEvent(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			NextSeq: 1,
			Goal: &gen.SessionGoal{
				Condition:       "refactor auth module",
				InterpretedGoal: "Migrate auth from session to JWT tokens",
				MaxTurns:        20,
			},
		},
	}

	a.emitGoalSubmitEvent(ctx, "turn-1", "req-1")

	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(ctx.EmittedEvents))
	}
	ev := ctx.EmittedEvents[0]
	if ev.Kind != "step" {
		t.Fatalf("expected step event, got %q", ev.Kind)
	}
	se, ok := ev.Payload.(domain.StepEvent)
	if !ok {
		t.Fatalf("expected StepEvent payload, got %T", ev.Payload)
	}
	if se.InteractionType != "goal_submit" {
		t.Fatalf("expected InteractionType goal_submit, got %q", se.InteractionType)
	}
	if se.RequestID != "req-1" {
		t.Fatalf("expected RequestID req-1, got %q", se.RequestID)
	}
}

// ── goalCondition tests ──

func TestGoalConditionPrefersInterpretedGoal(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition:       "refactor auth",
				InterpretedGoal: "Migrate to JWT tokens with 80% test coverage",
				Confirmed:       true,
			},
		},
	}

	if got := a.goalCondition(ctx); got != "Migrate to JWT tokens with 80% test coverage" {
		t.Fatalf("expected InterpretedGoal, got %q", got)
	}
}

func TestGoalConditionFallsBackToCondition(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition: "refactor auth",
			},
		},
	}

	if got := a.goalCondition(ctx); got != "refactor auth" {
		t.Fatalf("expected Condition fallback, got %q", got)
	}
}

// ── buildGoalBlock tests ──

func TestBuildGoalBlockUnconfirmed(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition: "refactor auth",
				MaxTurns:  20,
				TurnCount: 0,
			},
		},
	}

	block := a.buildGoalBlock(ctx)
	if block == nil {
		t.Fatal("expected goal block, got nil")
	}
	if !strings.Contains(block.Text, "not yet submitted") {
		t.Fatalf("expected unconfirmed goal guidance in goal block, got %q", block.Text)
	}
	if !strings.Contains(block.Text, "goal_submit") {
		t.Fatalf("expected goal_submit guidance in unconfirmed goal block, got %q", block.Text)
	}
}

func TestBuildGoalBlockIncludesPreviousAssessment(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{Turns: []domain.Turn{
			{Role: "assistant", Assessment: &gen.TurnAssessment{
				Decision: "ready_for_review",
				Reason:   "integration test still fails",
				Evidence: []string{"go test ./pkg/actor/agent/... failed"},
			}},
		}},
		RawSession: gen.RawSession{Goal: &gen.SessionGoal{
			Condition: "finish integration tests",
			Confirmed: true,
			MaxTurns:  3,
			TurnCount: 1,
		}},
	}

	block := a.buildGoalBlock(ctx)
	if block == nil {
		t.Fatal("expected goal block, got nil")
	}
	for _, want := range []string{"Previous Turn Assessment", "Decision: ready_for_review", "integration test still fails", "go test ./pkg/actor/agent/... failed"} {
		if !strings.Contains(block.Text, want) {
			t.Errorf("goal block missing %q: %s", want, block.Text)
		}
	}
}
func TestBuildGoalBlockConfirmed(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition:       "refactor auth",
				InterpretedGoal: "Migrate to JWT tokens",
				Confirmed:       true,
				MaxTurns:        20,
				TurnCount:       3,
			},
		},
	}

	block := a.buildGoalBlock(ctx)
	if block == nil {
		t.Fatal("expected goal block, got nil")
	}
	if !strings.Contains(block.Text, "Migrate to JWT tokens") {
		t.Fatalf("expected InterpretedGoal in goal block, got %q", block.Text)
	}
	if strings.Contains(block.Text, "not yet submitted") {
		t.Fatal("confirmed goal should not show unconfirmed guidance")
	}
}

func TestClearGoalRemovesGoalAndUnmountsCard(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition: "refactor auth",
				Confirmed: true,
				MaxTurns:  20,
				TurnCount: 3,
				Status:    "active",
			},
		},
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:mode:goal", Enabled: true, Scope: "user"},
			{CardID: "builtin:bundle:goal", Enabled: true, Scope: "user"},
		},
	}

	a.clearGoal(ctx)

	if a.RawSession.Goal != nil {
		t.Fatalf("expected Goal to be nil after clearGoal, got Status=%q", a.RawSession.Goal.Status)
	}
	for _, m := range a.ComponentMounts {
		if m.CardID == "builtin:mode:goal" || m.CardID == "builtin:bundle:goal" {
			t.Fatalf("expected goal mode and bundle to be unmounted, got %+v", m)
		}
	}
}

// TestClearGoalSyncsCardRefs guards the composer-badge bug: when cardRefs is
// non-nil (the canonical mount source), handleComponentList rebuilds the mount
// list from it. clearGoal must remove the goal entry from cardRefs, otherwise
// the goal mount — and the composer badge — would be resurrected on the next
// agent.component.list poll.
func TestClearGoalSyncsCardRefs(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "refactor auth", Confirmed: true, MaxTurns: 20, Status: "active"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:goal", Scope: "user"},
			{ID: "builtin:mode:goal", Scope: "user"},
			{ID: "skill:plan-module", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	a.clearGoal(ctx)

	if a.RawSession.Goal != nil {
		t.Fatalf("expected Goal to be nil after clearGoal")
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:goal" || ref.ID == "builtin:bundle:goal" {
			t.Fatalf("expected goal cards to be removed from cardRefs, got %+v", ref)
		}
	}
	for _, m := range a.ComponentMounts {
		if m.CardID == "builtin:mode:goal" || m.CardID == "builtin:bundle:goal" {
			t.Fatalf("expected goal cards to be removed from ComponentMounts, got %+v", m)
		}
	}
	list, err := a.handleComponentList(ctx, domain.AgentComponentListReq{})
	if err != nil {
		t.Fatalf("handleComponentList: %v", err)
	}
	for _, m := range list.Items {
		if m.CardID == "builtin:mode:goal" || m.CardID == "builtin:bundle:goal" {
			t.Fatalf("handleComponentList must not report goal cards after clearGoal, got %+v", m)
		}
	}
	// Non-goal mounts must survive the clear.
	if len(list.Items) != 1 || list.Items[0].CardID != "skill:plan-module" {
		t.Fatalf("expected only skill:plan-module to remain, got %+v", list.Items)
	}
}

func TestEmitGoalCompletedStepAppendsSystemStep(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}

	a.emitGoalCompletedStep(ctx, "turn-1", "All tests pass.")

	if len(a.steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(a.steps))
	}
	s := a.steps[0]
	if s.Role != "system" || !strings.Contains(s.Content[0].Text, "Goal completed") {
		t.Fatalf("unexpected step: %+v", s)
	}
	if !strings.Contains(s.Content[0].Text, "All tests pass") {
		t.Fatalf("expected summary in step text, got %q", s.Content[0].Text)
	}
}

func TestBuildGoalBlockExcludesPlanCards(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition:   "refactor auth",
				Confirmed:   true,
				MaxTurns:    20,
				TurnCount:   3,
				PlanCardIDs: []string{"plan-1", "plan-2"},
			},
		},
	}

	block := a.buildGoalBlock(ctx)
	if block == nil {
		t.Fatal("expected goal block, got nil")
	}
	if strings.Contains(block.Text, "Supporting plans") {
		t.Fatalf("goal block must not list supporting plans in hot context, got %q", block.Text)
	}
	if strings.Contains(block.Text, "plan-1") || strings.Contains(block.Text, "plan-2") {
		t.Fatalf("goal block must not contain plan card IDs, got %q", block.Text)
	}
}

// TestEnsureGoalCardMounted_RestoresMissingGoalMount verifies that when an
// active goal exists but builtin:mode:goal is not mounted (for example after a
// reload where cardRefs lost the entry), ensureGoalCardMounted adds it back so
// the composer badge and goal tools are restored.
func TestEnsureGoalCardMounted_RestoresMissingGoalMount(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "refactor auth", Confirmed: true, MaxTurns: 20},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if !a.ensureGoalCardMounted(ctx) {
		t.Fatal("expected ensureGoalCardMounted to make a change")
	}

	found := false
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:goal" && ref.Scope == "user" && !ref.Disabled {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected builtin:mode:goal added to cardRefs, got %+v", a.cardRefs)
	}
	found = false
	for _, m := range a.ComponentMounts {
		if m.CardID == "builtin:mode:goal" && m.Enabled && m.Scope == "user" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected builtin:mode:goal added to ComponentMounts, got %+v", a.ComponentMounts)
	}
}

// TestEnsureGoalCardMounted_ReenablesDisabledGoalMount verifies that a disabled
// goal card is re-enabled so goal mode stays active.
func TestEnsureGoalCardMounted_ReenablesDisabledGoalMount(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "refactor auth", Confirmed: true, MaxTurns: 20},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
			{ID: "builtin:mode:goal", Scope: "user", Disabled: true},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if !a.ensureGoalCardMounted(ctx) {
		t.Fatal("expected ensureGoalCardMounted to make a change")
	}

	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:goal" && ref.Disabled {
			t.Fatalf("expected builtin:mode:goal to be re-enabled, got %+v", ref)
		}
	}
	for _, m := range a.ComponentMounts {
		if m.CardID == "builtin:mode:goal" && !m.Enabled {
			t.Fatalf("expected builtin:mode:goal mount to be enabled, got %+v", m)
		}
	}
}

// TestEnsureGoalCardMounted_NoGoalDoesNothing verifies that without an active
// goal the function does not mount the goal card.
func TestEnsureGoalCardMounted_NoGoalDoesNothing(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if a.ensureGoalCardMounted(ctx) {
		t.Fatal("expected ensureGoalCardMounted to make no change when goal is nil")
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:goal" {
			t.Fatalf("expected no goal card ref to be added, got %+v", ref)
		}
	}
}

// ── handleInternalAssignGoal / handleInternalResumeFromReview tests ──
//
// The handlers persist via saveMailbox (no-op when actorID is empty) and
// schedule the next turn via ctx.After (no-op in testutil.FakeCtx), so a bare
// &Actor{} with an anonymous context exercises the full state transition.

func TestHandleInternalAssignGoal(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}

	resp, err := a.handleInternalAssignGoal(ctx, domain.AgentInternalAssignGoalReq{
		Condition:       "implement OAuth2 login",
		InterpretedGoal: "Implement OAuth2 login with refresh token rotation",
		MaxTurns:        12,
		BoundTaskCardID: "task-7",
	})
	if err != nil {
		t.Fatalf("handleInternalAssignGoal: %v", err)
	}

	g := a.RawSession.Goal
	if g == nil {
		t.Fatal("expected Goal to be set")
	}
	if !g.Confirmed {
		t.Fatal("expected Confirmed=true")
	}
	if g.Status != "active" {
		t.Fatalf("expected Status active, got %q", g.Status)
	}
	if g.BoundTaskCardID != "task-7" {
		t.Fatalf("expected BoundTaskCardID task-7, got %q", g.BoundTaskCardID)
	}
	if g.MaxTurns != 12 {
		t.Fatalf("expected MaxTurns 12, got %d", g.MaxTurns)
	}
	if g.TurnCount != 0 {
		t.Fatalf("expected TurnCount 0, got %d", g.TurnCount)
	}
	if g.InterpretedGoal != "Implement OAuth2 login with refresh token rotation" {
		t.Fatalf("unexpected InterpretedGoal: %q", g.InterpretedGoal)
	}

	// Response must carry a summary of the assigned goal.
	if resp.Goal.Condition != "implement OAuth2 login" || !resp.Goal.Confirmed || resp.Goal.Status != "active" {
		t.Fatalf("unexpected resp.Goal: %+v", resp.Goal)
	}
	if resp.Goal.BoundTaskCardID != "task-7" || resp.Goal.TurnCount != 0 || resp.Goal.MaxTurns != 12 {
		t.Fatalf("unexpected resp.Goal fields: %+v", resp.Goal)
	}

	// A user turn carrying the goal condition must be created and made active.
	if len(a.Session.Turns) != 1 {
		t.Fatalf("expected 1 user turn, got %d", len(a.Session.Turns))
	}
	if a.Session.ActiveHead != 0 {
		t.Fatalf("expected ActiveHead 0, got %d", a.Session.ActiveHead)
	}
	if first := a.Session.Turns[0]; first.Role != "user" || first.UserInput != "implement OAuth2 login" {
		t.Fatalf("unexpected turn: %+v", first)
	}

	// The goal card must be mounted so the composer badge and goal tools appear.
	found := false
	for _, m := range a.ComponentMounts {
		if m.CardID == "builtin:mode:goal" && m.Enabled {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected builtin:mode:goal mounted, got %+v", a.ComponentMounts)
	}

	// The assigned interpreted goal must become the agent title.
	if a.title != "Implement OAuth2 login with refresh token rotation" {
		t.Fatalf("expected title from InterpretedGoal, got %q", a.title)
	}
}

func TestHandleInternalAssignGoal_TitleFallbackAndTruncation(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}

	longCondition := strings.Repeat("长", MaxTitleLen+10)
	_, err := a.handleInternalAssignGoal(ctx, domain.AgentInternalAssignGoalReq{
		Condition: longCondition,
	})
	if err != nil {
		t.Fatalf("handleInternalAssignGoal: %v", err)
	}
	if a.title != truncateTitle(longCondition) {
		t.Fatalf("expected truncated condition title (%d runes), got %d runes", MaxTitleLen, len([]rune(a.title)))
	}
	if a.Title != a.title {
		t.Fatalf("public Title projection not mirrored: title=%q Title=%q", a.title, a.Title)
	}
}

// TestApplySpawnGoalSetsTitle covers the workspace.agent.spawn_assign path:
// the goal threaded through the spawn chain is self-assigned in OnStart via
// applySpawnGoal, which must mirror the goal text into the agent title just
// like handleInternalAssignGoal does.
func TestApplySpawnGoalSetsTitle(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}

	a.applySpawnGoal(ctx, &domain.AgentInternalAssignGoalReq{
		Condition:       "implement OAuth2 login",
		InterpretedGoal: "worker-oauth2",
		MaxTurns:        12,
		BoundTaskCardID: "task-7",
	})

	g := a.RawSession.Goal
	if g == nil {
		t.Fatal("expected Goal to be set")
	}
	if !g.Confirmed || g.Status != "active" || g.BoundTaskCardID != "task-7" || g.MaxTurns != 12 {
		t.Fatalf("unexpected goal: %+v", g)
	}
	if a.title != "worker-oauth2" {
		t.Fatalf("expected title from InterpretedGoal, got %q", a.title)
	}
	if a.Title != a.title {
		t.Fatalf("public Title projection not mirrored: title=%q Title=%q", a.title, a.Title)
	}
	if len(a.Session.Turns) != 1 {
		t.Fatalf("expected 1 user turn, got %d", len(a.Session.Turns))
	}
}

func TestApplySpawnGoalTitleFallsBackToCondition(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}

	longCondition := strings.Repeat("长", MaxTitleLen+10)
	a.applySpawnGoal(ctx, &domain.AgentInternalAssignGoalReq{
		Condition: longCondition,
	})
	if a.title != truncateTitle(longCondition) {
		t.Fatalf("expected truncated condition title (%d runes), got %d runes", MaxTitleLen, len([]rune(a.title)))
	}
}

func TestHandleInternalAssignGoalDefaultMaxTurns(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}

	_, err := a.handleInternalAssignGoal(ctx, domain.AgentInternalAssignGoalReq{
		Condition: "fix flaky tests",
		MaxTurns:  0,
	})
	if err != nil {
		t.Fatalf("handleInternalAssignGoal: %v", err)
	}
	if got := a.RawSession.Goal.MaxTurns; got != DefaultGoalMaxTurns {
		t.Fatalf("expected DefaultGoalMaxTurns (%d), got %d", DefaultGoalMaxTurns, got)
	}
}

func TestHandleInternalAssignGoalKindConfigMaxTurns(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}
	a.kindConfig.Store(&domain.AgentKindConfig{MaxTurns: 50})

	_, err := a.handleInternalAssignGoal(ctx, domain.AgentInternalAssignGoalReq{
		Condition: "refactor module",
		MaxTurns:  0,
	})
	if err != nil {
		t.Fatalf("handleInternalAssignGoal: %v", err)
	}
	if got := a.RawSession.Goal.MaxTurns; got != 50 {
		t.Fatalf("expected kind config MaxTurns 50, got %d", got)
	}
}

func TestHandleInternalResumeFromReview(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition: "implement feature X",
				Status:    "ready_for_review",
				Confirmed: true,
				MaxTurns:  20,
				TurnCount: 7,
			},
		},
	}

	err := a.handleInternalResumeFromReview(ctx, domain.AgentInternalResumeFromReviewReq{
		Feedback: "tests are failing, fix them",
	})
	if err != nil {
		t.Fatalf("handleInternalResumeFromReview: %v", err)
	}

	g := a.RawSession.Goal
	if g.Status != "active" {
		t.Fatalf("expected Status active, got %q", g.Status)
	}
	if g.TurnCount != 0 {
		t.Fatalf("expected TurnCount reset to 0, got %d", g.TurnCount)
	}
	// Feedback must be appended as a user turn and set as the active head.
	if len(a.Session.Turns) != 1 {
		t.Fatalf("expected 1 user turn, got %d", len(a.Session.Turns))
	}
	if a.Session.ActiveHead != 0 {
		t.Fatalf("expected ActiveHead 0, got %d", a.Session.ActiveHead)
	}
	if a.Session.Turns[0].UserInput != "tests are failing, fix them" {
		t.Fatalf("unexpected feedback turn: %+v", a.Session.Turns[0])
	}
	// The reject-feedback user step must carry the review_reject meta so the
	// frontend can distinguish it from a normal user message.
	if len(a.steps) != 1 || a.steps[0].Meta != "review_reject" {
		t.Fatalf("expected user step meta review_reject, got steps=%+v", a.steps)
	}
}

func TestHandleInternalResumeFromReviewNoGoal(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}

	err := a.handleInternalResumeFromReview(ctx, domain.AgentInternalResumeFromReviewReq{
		Feedback: "revise",
	})
	if err == nil {
		t.Fatal("expected error when no goal exists")
	}
	if !strings.Contains(err.Error(), "no active goal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleInternalResumeFromReviewDefaultFeedback(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition: "feature",
				Status:    "ready_for_review",
				Confirmed: true,
				MaxTurns:  20,
				TurnCount: 4,
			},
		},
	}

	if err := a.handleInternalResumeFromReview(ctx, domain.AgentInternalResumeFromReviewReq{}); err != nil {
		t.Fatalf("handleInternalResumeFromReview: %v", err)
	}
	if len(a.Session.Turns) != 1 {
		t.Fatalf("expected 1 user turn, got %d", len(a.Session.Turns))
	}
	if got := a.Session.Turns[0].UserInput; got != "Your previous work was rejected. Please revise." {
		t.Fatalf("unexpected default feedback: %q", got)
	}
}

func TestHandleInternalAssignGoalWithPrelude(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}

	_, err := a.handleInternalAssignGoal(ctx, domain.AgentInternalAssignGoalReq{
		Condition:       "implement feature Z",
		BoundTaskCardID: "ticket-prelude",
		PromptPrelude:   "You are a temporary executor bound to task card \"ticket-prelude\".",
		MaxTurns:        5,
	})
	if err != nil {
		t.Fatalf("handleInternalAssignGoal: %v", err)
	}

	// The first user turn must contain the prelude prepended to the condition.
	if len(a.Session.Turns) != 1 {
		t.Fatalf("expected 1 user turn, got %d", len(a.Session.Turns))
	}
	msg := a.Session.Turns[0].UserInput
	if !strings.Contains(msg, "temporary executor") {
		t.Errorf("first turn missing prelude, got: %q", msg)
	}
	if !strings.Contains(msg, "implement feature Z") {
		t.Errorf("first turn missing condition, got: %q", msg)
	}

	g := a.RawSession.Goal
	if g == nil || !g.Confirmed || g.Status != "active" {
		t.Fatalf("goal not properly set: %+v", g)
	}
	if g.BoundTaskCardID != "ticket-prelude" {
		t.Errorf("BoundTaskCardID = %q, want ticket-prelude", g.BoundTaskCardID)
	}
}

func TestHandleInternalAssignGoalWithoutPrelude(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}

	_, err := a.handleInternalAssignGoal(ctx, domain.AgentInternalAssignGoalReq{
		Condition: "just a condition",
		MaxTurns:  3,
	})
	if err != nil {
		t.Fatalf("handleInternalAssignGoal: %v", err)
	}
	if len(a.Session.Turns) != 1 {
		t.Fatalf("expected 1 user turn, got %d", len(a.Session.Turns))
	}
	if got := a.Session.Turns[0].UserInput; got != "just a condition" {
		t.Errorf("without prelude, first turn should be exactly the condition, got: %q", got)
	}
}

// TestDeactivateMode_PreservesBuiltinScopeBundle verifies that
// deactivateMode removes the mode and its required bundles that have
// scope "user" or "dependency", but preserves bundles mounted with
// scope "builtin" (e.g. from kind config as a permission proxy).
func TestDeactivateMode_PreservesBuiltinScopeBundle(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:goal", Scope: "builtin"},
			{ID: "builtin:mode:goal", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	changed := a.deactivateMode(ctx, "builtin:mode:goal")
	if !changed {
		t.Fatal("expected deactivateMode to make a change")
	}

	modeFound := false
	bundleFound := false
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:goal" {
			modeFound = true
		}
		if ref.ID == "builtin:bundle:goal" {
			bundleFound = true
		}
	}
	if modeFound {
		t.Fatal("expected builtin:mode:goal to be removed")
	}
	if !bundleFound {
		t.Fatal("expected builtin:bundle:goal with scope \"builtin\" to be preserved")
	}
}

// TestDeactivateMode_RemovesUserScopeBundle verifies that
// deactivateMode removes bundles with scope "user" (mounted by
// the mode activation) along with the mode card itself.
func TestDeactivateMode_RemovesUserScopeBundle(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:goal", Scope: "user"},
			{ID: "builtin:mode:goal", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	changed := a.deactivateMode(ctx, "builtin:mode:goal")
	if !changed {
		t.Fatal("expected deactivateMode to make a change")
	}

	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:goal" || ref.ID == "builtin:bundle:goal" {
			t.Fatalf("expected both mode and user-scope bundle removed, got %+v", ref)
		}
	}
}

// TestEnsureModeActive_PreservesExistingBuiltinScopeBundle verifies
// that ensureModeActive does not downgrade an existing "builtin" scope
// bundle to "user" when activating a mode whose requires includes it.
func TestEnsureModeActive_PreservesExistingBuiltinScopeBundle(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:goal", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	changed := a.ensureModeActive(ctx, "builtin:mode:goal")
	if !changed {
		t.Fatal("expected ensureModeActive to add the mode card")
	}

	bundleScope := ""
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:bundle:goal" {
			bundleScope = ref.Scope
		}
	}
	if bundleScope != "builtin" {
		t.Fatalf("expected builtin:bundle:goal scope preserved as \"builtin\", got %q", bundleScope)
	}
}
