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
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// goalCardProjectPlanner is a configurable actor.Planner stub for
// goal_card_submit approval tests. It resolves existing task cards,
// records wiki_set_status calls, and resolves the workspace kind-config lookup.
type goalCardProjectPlanner struct {
	lookupErr error
	claimErr  error
	maxTurns  int32

	lookedUpIDs []string
	claimed     *domain.WikiSetStatusReq
	kindCalled  bool
}

func (p *goalCardProjectPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, errors.New("not implemented")
}

func (p *goalCardProjectPlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return promise.Reject[any](errors.New("not implemented"))
}

func (p *goalCardProjectPlanner) Call(_ context.Context, _ ref.Ref, callID string, payload any) *promise.Promise[any] {
	switch callID {
	case "project.wiki_get_card":
		if p.lookupErr != nil {
			return promise.Reject[any](p.lookupErr)
		}
		req := domain.WikiGetCardReq{}
		switch v := payload.(type) {
		case domain.WikiGetCardReq:
			req = v
		case *domain.WikiGetCardReq:
			req = *v
		case []byte:
			_ = json.Unmarshal(v, &req)
		}
		p.lookedUpIDs = append(p.lookedUpIDs, req.ID)
		return promise.Async(func(resolve func(any), _ func(any)) {
			resolve(domain.WikiGetCardResp{ID: req.ID, Raw: "---\ntype: task\nstatus: todo\n---\nImplement the tracking dashboard"})
		})
	case "project.wiki_get_card_hierarchy":
		req := domain.WikiGetCardHierarchyReq{}
		switch v := payload.(type) {
		case domain.WikiGetCardHierarchyReq:
			req = v
		case *domain.WikiGetCardHierarchyReq:
			req = *v
		case []byte:
			_ = json.Unmarshal(v, &req)
		}
		return promise.Async(func(resolve func(any), _ func(any)) {
			resolve(domain.WikiGetCardHierarchyResp{Tree: "└─ " + req.ID + " [doing] [current]"})
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
			resolve(domain.WikiSetStatusResp{Card: gen.MonoCardListItem{ID: req.ID, Type: "task"}, PreviousStatus: "todo"})
		})
	case "workspace.get_agent_kind_config":
		p.kindCalled = true
		return promise.Async(func(resolve func(any), _ func(any)) {
			resolve(domain.AgentKindConfig{Kind: "coder", MaxTurns: p.maxTurns})
		})
	default:
		return promise.Reject[any](fmt.Errorf("unexpected call %q", callID))
	}
}

// newGoalCardApprovalCtx returns a FakeCtx whose project + workspace services
// resolve through the given planner.
func newGoalCardApprovalCtx(planner *goalCardProjectPlanner) *testutil.FakeCtx {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		switch name {
		case "project", "workspace":
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner { return planner }
	return ctx
}

func TestApplyGoalCardSubmit(t *testing.T) {
	ctx := newGoalCardApprovalCtx(&goalCardProjectPlanner{})
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "build a tracking dashboard", MaxTurns: 20},
		},
	}

	requestID, err := a.applyGoalCardSubmit(ctx, gen.AgentGoalCardSubmitReq{
		CardID:          "Build tracking dashboard",
		InterpretedGoal: "Ship a dashboard with progress bars and drill-down",
	})
	if err != nil {
		t.Fatalf("applyGoalCardSubmit: %v", err)
	}
	if requestID == "" {
		t.Fatal("expected non-empty requestID")
	}
	if !a.goalCardSubmitPending {
		t.Fatal("expected goalCardSubmitPending to be true")
	}
	if a.goalCardSubmitRequestID != requestID {
		t.Fatalf("expected goalCardSubmitRequestID %q, got %q", requestID, a.goalCardSubmitRequestID)
	}
	p := a.goalCardProposal
	if p == nil {
		t.Fatal("expected proposal to be stored")
	}
	if p.CardID != "Build tracking dashboard" {
		t.Fatalf("unexpected proposal: %+v", p)
	}
	if p.InterpretedGoal != "Ship a dashboard with progress bars and drill-down" {
		t.Fatalf("unexpected InterpretedGoal: %q", p.InterpretedGoal)
	}
	if a.RawSession.Goal.Confirmed {
		t.Fatal("goal must stay unconfirmed while the proposal is pending")
	}
}

func TestApplyGoalCardSubmitNoGoal(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{RawSession: gen.RawSession{}}

	_, err := a.applyGoalCardSubmit(ctx, gen.AgentGoalCardSubmitReq{CardID: "t"})
	if err == nil {
		t.Fatal("expected error when no active goal")
	}
}

func TestApplyGoalCardSubmitAlreadyConfirmed(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "x", InterpretedGoal: "confirmed", Confirmed: true, MaxTurns: 20},
		},
	}

	_, err := a.applyGoalCardSubmit(ctx, gen.AgentGoalCardSubmitReq{CardID: "t"})
	if err == nil {
		t.Fatal("expected error when goal already confirmed")
	}
}

func TestApplyGoalCardSubmitEmptyTitle(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{RawSession: gen.RawSession{Goal: &gen.SessionGoal{Condition: "x", MaxTurns: 20}}}

	_, err := a.applyGoalCardSubmit(ctx, gen.AgentGoalCardSubmitReq{CardID: "  "})
	if err == nil {
		t.Fatal("expected error for empty Title")
	}
}

func TestResolveGoalCardSubmitReject(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "build dashboard", MaxTurns: 20},
		},
		goalCardSubmitPending:   true,
		goalCardSubmitRequestID: "req-1",
		goalCardProposal: &goalCardProposalState{
			CardID: "Build dashboard",
		},
	}

	decision, requestID, feedback, cardID, err := a.resolveGoalCardSubmit(ctx, `{"decision":"reject","feedback":"too broad"}`)
	if err != nil {
		t.Fatalf("reject must not fail: %v", err)
	}
	if decision != "reject" {
		t.Fatalf("expected reject, got %q", decision)
	}
	if requestID != "req-1" {
		t.Fatalf("expected req-1, got %q", requestID)
	}
	if feedback != "too broad" {
		t.Fatalf("unexpected feedback: %q", feedback)
	}
	if cardID != "" {
		t.Fatalf("expected no card on reject, got %q", cardID)
	}
	if a.RawSession.Goal.Confirmed {
		t.Fatal("goal must stay unconfirmed after reject")
	}
	if a.goalCardSubmitPending {
		t.Fatal("expected goalCardSubmitPending to be false after resolve")
	}
	if a.goalCardProposal != nil {
		t.Fatal("proposal should be consumed after resolve")
	}
}

func TestResolveGoalCardSubmitApprove(t *testing.T) {
	planner := &goalCardProjectPlanner{maxTurns: 12}
	ctx := newGoalCardApprovalCtx(planner)
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "build dashboard", MaxTurns: 20},
		},
		goalCardSubmitPending:   true,
		goalCardSubmitRequestID: "req-2",
		goalCardProposal: &goalCardProposalState{
			CardID:          "Build tracking dashboard",
			InterpretedGoal: "Ship a dashboard with progress bars",
		},
	}

	decision, requestID, _, cardID, err := a.resolveGoalCardSubmit(ctx, `{"decision":"approve"}`)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if decision != "approve" {
		t.Fatalf("expected approve, got %q", decision)
	}
	if requestID != "req-2" {
		t.Fatalf("expected req-2, got %q", requestID)
	}
	if cardID != "Build tracking dashboard" {
		t.Fatalf("expected created card id, got %q", cardID)
	}

	// The existing task card was verified before it was claimed.
	found := false
	for _, id := range planner.lookedUpIDs {
		found = found || id == "Build tracking dashboard"
	}
	if !found {
		t.Fatalf("did not verify task card: %v", planner.lookedUpIDs)
	}
	// And claimed todo → doing.
	if planner.claimed == nil {
		t.Fatal("expected claim (wiki_set_status) call")
	}
	if planner.claimed.ID != "Build tracking dashboard" || planner.claimed.Status != "doing" || planner.claimed.ExpectedStatus != "todo" {
		t.Fatalf("unexpected claim: %+v", planner.claimed)
	}

	// The current agent is bound and confirmed, mirroring internal assign.
	g := a.RawSession.Goal
	if g == nil || !g.Confirmed {
		t.Fatal("goal must be confirmed after approve")
	}
	if g.Status != "active" {
		t.Fatalf("expected Status active, got %q", g.Status)
	}
	if g.BoundTaskCardID != "Build tracking dashboard" {
		t.Fatalf("expected BoundTaskCardID, got %q", g.BoundTaskCardID)
	}
	if g.MaxTurns != 12 {
		t.Fatalf("expected kind-config MaxTurns 12, got %d", g.MaxTurns)
	}
	if g.Condition != "Implement the tracking dashboard" {
		t.Fatalf("expected condition from task card body, got %q", g.Condition)
	}
	if g.InterpretedGoal != "Ship a dashboard with progress bars" {
		t.Fatalf("unexpected interpreted goal: %q", g.InterpretedGoal)
	}
	if a.title != "Ship a dashboard with progress bars" || a.Title != a.title {
		t.Fatalf("expected title mirrored from approved card intent, got title=%q Title=%q", a.title, a.Title)
	}
	// Goal cards must be mounted for the binding; workflow mode is mounted
	// only by workflow_start, not by a card binding.
	if !hasMountedCard(a, "builtin:mode:goal") {
		t.Fatalf("expected goal mode mounted, cardRefs=%v", a.cardRefs)
	}
	if hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatalf("expected workflow mode NOT mounted from card binding, cardRefs=%v", a.cardRefs)
	}
	if a.goalCardProposal != nil {
		t.Fatal("proposal should be consumed after approve")
	}
}

func TestResolveGoalCardSubmitApproveProjectUnavailable(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID()) // no project service
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "x", MaxTurns: 20},
		},
		goalCardSubmitPending:   true,
		goalCardSubmitRequestID: "req-3",
		goalCardProposal: &goalCardProposalState{
			CardID: "T",
		},
	}

	_, _, _, _, err := a.resolveGoalCardSubmit(ctx, `{"decision":"approve"}`)
	if err == nil {
		t.Fatal("expected error when project service is unavailable")
	}
	if !strings.Contains(err.Error(), "project service unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.RawSession.Goal.Confirmed {
		t.Fatal("goal must stay unconfirmed when card creation fails")
	}
	if a.goalCardProposal != nil {
		t.Fatal("failed proposal should be consumed so a retry submits fresh")
	}
}

func TestResolveGoalCardSubmitApproveCreateFails(t *testing.T) {
	planner := &goalCardProjectPlanner{lookupErr: errors.New("card already exists")}
	ctx := newGoalCardApprovalCtx(planner)
	a := &Actor{
		RawSession:              gen.RawSession{Goal: &gen.SessionGoal{Condition: "x", MaxTurns: 20}},
		goalCardSubmitPending:   true,
		goalCardSubmitRequestID: "req-4",
		goalCardProposal:        &goalCardProposalState{CardID: "T"},
	}

	_, _, _, _, err := a.resolveGoalCardSubmit(ctx, `{"decision":"approve"}`)
	if err == nil {
		t.Fatal("expected error when card creation fails")
	}
	if a.RawSession.Goal.Confirmed {
		t.Fatal("goal must not be bound when card creation fails")
	}
}

func TestResolveGoalCardSubmitApproveClaimFails(t *testing.T) {
	planner := &goalCardProjectPlanner{claimErr: errors.New("claim conflict")}
	ctx := newGoalCardApprovalCtx(planner)
	a := &Actor{
		RawSession:              gen.RawSession{Goal: &gen.SessionGoal{Condition: "x", MaxTurns: 20}},
		goalCardSubmitPending:   true,
		goalCardSubmitRequestID: "req-5",
		goalCardProposal:        &goalCardProposalState{CardID: "T"},
	}

	_, _, _, _, err := a.resolveGoalCardSubmit(ctx, `{"decision":"approve"}`)
	if err == nil {
		t.Fatal("expected error when claim fails")
	}
	if a.RawSession.Goal.Confirmed {
		t.Fatal("goal must not be bound when the claim fails")
	}
	found := false
	for _, id := range planner.lookedUpIDs {
		found = found || id == "T"
	}
	if !found {
		t.Fatalf("card should have been verified before claim: %v", planner.lookedUpIDs)
	}
}

func TestExpireGoalCardSubmit(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		goalCardSubmitPending:   true,
		goalCardSubmitRequestID: "req-1",
		goalCardProposal:        &goalCardProposalState{CardID: "T"},
	}

	a.expireGoalCardSubmit(ctx, "req-1")
	if a.goalCardSubmitPending {
		t.Fatal("expected goalCardSubmitPending to be false after expire")
	}
	if a.goalCardSubmitRequestID != "" {
		t.Fatal("expected goalCardSubmitRequestID to be empty after expire")
	}
	if a.goalCardProposal != nil {
		t.Fatal("expected proposal to be dropped after expire")
	}
}

func TestEmitGoalCardSubmitEvent(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{NextSeq: 1},
		goalCardProposal: &goalCardProposalState{
			CardID:          "Build tracking dashboard",
			InterpretedGoal: "Ship a dashboard with progress bars",
		},
	}

	a.emitGoalCardSubmitEvent(ctx, "turn-1", "req-1")

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
	if se.InteractionType != "goal_card_submit" {
		t.Fatalf("expected InteractionType goal_card_submit, got %q", se.InteractionType)
	}
	if se.RequestID != "req-1" {
		t.Fatalf("expected RequestID req-1, got %q", se.RequestID)
	}
	if se.Task == nil {
		t.Fatal("expected Task payload")
	}
	if se.Task["cardId"] != "Build tracking dashboard" {
		t.Fatalf("unexpected title payload: %v", se.Task["cardId"])
	}
	if se.Task["interpretedGoal"] != "Ship a dashboard with progress bars" {
		t.Fatalf("unexpected interpretedGoal payload: %v", se.Task["interpretedGoal"])
	}
	if _, ok := se.Task["goalCondition"]; ok {
		t.Fatalf("goalCondition must not be in task payload: %v", se.Task)
	}
	// The interaction must be recorded for restart recovery.
	if a.pendingInteraction == nil || a.pendingInteraction.Type != "goal_card_submit" {
		t.Fatalf("pending interaction not recorded: %+v", a.pendingInteraction)
	}
	if a.pendingInteraction.RequestID != "req-1" {
		t.Fatalf("unexpected pending requestID: %+v", a.pendingInteraction)
	}
}

func TestGoalCardProposalFromTaskRoundtrip(t *testing.T) {
	p := goalCardProposalFromTask(goalCardProposalPayload(&goalCardProposalState{
		CardID:          "T",
		InterpretedGoal: "I",
	}))
	if p == nil {
		t.Fatal("expected proposal restored from payload")
	}
	if p.CardID != "T" || p.InterpretedGoal != "I" {
		t.Fatalf("roundtrip mismatch: %+v", p)
	}

	if got := goalCardProposalFromTask(nil); got != nil {
		t.Fatalf("nil payload should yield nil proposal, got %+v", got)
	}
	if got := goalCardProposalFromTask(map[string]any{"foo": "bar"}); got != nil {
		t.Fatalf("empty payload should yield nil proposal, got %+v", got)
	}
}

func TestRecoverPendingInteractionGoalCardSubmit(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{Turns: []domain.Turn{
			{ID: "t1", Role: "assistant", State: "running"},
		}},
		pendingInteraction: &pendingInteractionState{
			TurnID:    "t1",
			StepID:    "t1-goal-card-submit-r1",
			RequestID: "r1",
			Type:      "goal_card_submit",
			Task: goalCardProposalPayload(&goalCardProposalState{
				CardID: "T",
			}),
		},
	}

	persist := a.recoverPendingInteraction(ctx)
	if !persist {
		t.Fatal("expected recovery to canonicalize the interaction turn as paused/interaction")
	}
	if !a.goalCardSubmitPending {
		t.Fatal("expected goalCardSubmitPending restored")
	}
	if a.goalCardSubmitRequestID != "r1" {
		t.Fatalf("expected requestID r1, got %q", a.goalCardSubmitRequestID)
	}
	if a.status.State != "paused" || a.status.PauseKind != "interaction" {
		t.Fatalf("status = %q/%q, want paused/interaction", a.status.State, a.status.PauseKind)
	}
	if a.Session.Turns[0].State != "paused" || a.Session.Turns[0].PauseReason != "interaction" {
		t.Fatalf("turn t1 not canonicalized to paused/interaction: %+v", a.Session.Turns[0])
	}
	if a.goalCardProposal == nil || a.goalCardProposal.CardID != "T" {
		t.Fatalf("expected proposal restored from Task payload, got %+v", a.goalCardProposal)
	}
}

func TestHandleTurnAnswer_NoEngine_GoalCardSubmitResolves(t *testing.T) {
	planner := &goalCardProjectPlanner{}
	ctx := newGoalCardApprovalCtx(planner)
	a := &Actor{
		RawSession: gen.RawSession{Goal: &gen.SessionGoal{Condition: "x", MaxTurns: 20}},
		pendingInteraction: &pendingInteractionState{
			TurnID:    "t1",
			StepID:    "t1-goal-card-submit-r1",
			RequestID: "r1",
			Type:      "goal_card_submit",
			Task: goalCardProposalPayload(&goalCardProposalState{
				CardID: "Build tracking dashboard",
			}),
		},
		goalCardSubmitPending:   true,
		goalCardSubmitRequestID: "r1",
		goalCardProposal: &goalCardProposalState{
			CardID: "Build tracking dashboard",
		},
	}

	if err := a.handleTurnAnswer(ctx, domain.TurnAnswerReq{
		RequestID:   "r1",
		AnswersJSON: `{"decision":"approve"}`,
	}); err != nil {
		t.Fatalf("handleTurnAnswer: %v", err)
	}
	if a.RawSession.Goal == nil || !a.RawSession.Goal.Confirmed {
		t.Fatal("Goal.Confirmed = false, want true after restart-path approval")
	}
	if a.RawSession.Goal.BoundTaskCardID != "Build tracking dashboard" {
		t.Fatalf("unexpected binding: %q", a.RawSession.Goal.BoundTaskCardID)
	}
	if a.goalCardSubmitPending {
		t.Fatal("goalCardSubmitPending should be cleared after resolution")
	}
	if a.pendingInteraction != nil {
		t.Fatal("pendingInteraction should be cleared after resolution")
	}
}

func TestEnsureLocalInteractionCallablesIncludesGoalCardSubmit(t *testing.T) {
	callables := ensureLocalInteractionCallables(map[string]domain.CallableInterface{
		"goal_submit": {Name: "goal_submit"},
	})
	gc, ok := callables["goal_card_submit"]
	if !ok {
		t.Fatal("expected goal_card_submit to be added when missing")
	}
	if gc.Description == "" {
		t.Fatal("expected goal_card_submit description")
	}
	cardIDRequired := false
	for _, p := range gc.Params {
		if p.Name == "CardId" && p.Required {
			cardIDRequired = true
		}
		if p.Name == "GoalCondition" {
			t.Fatalf("GoalCondition param must be removed, got %+v", gc.Params)
		}
	}
	if !cardIDRequired {
		t.Fatalf("expected required CardId param, got %+v", gc.Params)
	}
	// The explicit goal_submit entry must be preserved untouched.
	if callables["goal_submit"].Description != "" {
		t.Fatal("expected existing goal_submit entry to be preserved")
	}
}

func TestBuildGoalBlockBoundTaskCard(t *testing.T) {
	planner := &goalCardProjectPlanner{}
	ctx := newGoalCardApprovalCtx(planner)
	a := &Actor{
		parentAgentID: "parent-agent-1",
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				BoundTaskCardID: "Build tracking dashboard",
				Confirmed:       true,
				Status:          "active",
				MaxTurns:        20,
				TurnCount:       2,
			},
		},
	}

	block := a.buildGoalBlock(ctx)
	if block == nil {
		t.Fatal("expected bound task goal block, got nil")
	}
	for _, want := range []string{
		"Bound Task Card",
		"Card ID: Build tracking dashboard",
		"Task card body",
		"Implement the tracking dashboard",
		"Card status tree",
		"└─ Build tracking dashboard [doing] [current]",
		"Turn limit: 20",
		"Turns used: 2",
		"Status: active",
		"wiki_set_status",
		"ready_for_review",
	} {
		if !strings.Contains(block.Text, want) {
			t.Errorf("bound goal block missing %q:\n%s", want, block.Text)
		}
	}
	if strings.Contains(block.Text, `Decision: "complete_candidate"`) {
		t.Errorf("bound goal must use ready_for_review, not complete_candidate:\n%s", block.Text)
	}
}

// TestBuildGoalBlockBoundTaskCard_NoParent verifies that a root agent (no
// parent agent) bound to a task card is instructed to complete directly via
// complete_candidate — there is no map-owner reviewer to pause for, and
// completion clears the goal, unbinds the card, and unmounts it from context.
func TestBuildGoalBlockBoundTaskCard_NoParent(t *testing.T) {
	planner := &goalCardProjectPlanner{}
	ctx := newGoalCardApprovalCtx(planner)
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				BoundTaskCardID: "Build tracking dashboard",
				Confirmed:       true,
				Status:          "active",
				MaxTurns:        20,
				TurnCount:       2,
			},
		},
	}

	block := a.buildGoalBlock(ctx)
	if block == nil {
		t.Fatal("expected bound task goal block, got nil")
	}
	for _, want := range []string{
		"Bound Task Card",
		"Card ID: Build tracking dashboard",
		"wiki_set_status",
		`Decision: "complete_candidate"`,
	} {
		if !strings.Contains(block.Text, want) {
			t.Errorf("root bound goal block missing %q:\n%s", want, block.Text)
		}
	}
	if strings.Contains(block.Text, "ready_for_review") {
		t.Errorf("root bound goal must NOT instruct ready_for_review:\n%s", block.Text)
	}
}

// TestBuildGoalBlockBoundTaskCard_KindAgnostic verifies that the bound task
// goal block instructs ready_for_review regardless of agent kind when the
// agent has a parent reviewer — matching the validateAssessDecision guard
// which keys off BoundTaskCardID + parent presence.
func TestBuildGoalBlockBoundTaskCard_KindAgnostic(t *testing.T) {
	planner := &goalCardProjectPlanner{}
	ctx := newGoalCardApprovalCtx(planner)
	for _, kind := range []string{domain.AgentKindWorker, domain.AgentKindCoder, domain.AgentKindCoordinator} {
		a := &Actor{
			agentKind:     kind,
			parentAgentID: "parent-agent-1",
			RawSession: gen.RawSession{
				Goal: &gen.SessionGoal{
					BoundTaskCardID: "task-impl-auth",
					Confirmed:       true,
					Status:          "active",
					MaxTurns:        12,
					TurnCount:       1,
				},
			},
		}

		block := a.buildGoalBlock(ctx)
		if block == nil {
			t.Fatalf("expected bound task goal block (kind=%s), got nil", kind)
		}
		if !strings.Contains(block.Text, "ready_for_review") {
			t.Errorf("bound goal block (kind=%s) must instruct ready_for_review:\n%s", kind, block.Text)
		}
		if strings.Contains(block.Text, `Decision: "complete_candidate"`) {
			t.Errorf("bound goal block (kind=%s) must NOT instruct complete_candidate:\n%s", kind, block.Text)
		}
	}
}

// hasMountedCard reports whether cardID appears in the actor's mount list.
func hasMountedCard(a *Actor, cardID string) bool {
	if a.cardRefs == nil {
		return false
	}
	for _, c := range a.cardRefs {
		if c.ID == cardID {
			return true
		}
	}
	return false
}
