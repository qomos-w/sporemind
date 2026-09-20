package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// goalCardProposalState is the pending task-card proposal awaiting user
// confirmation. It is kept in memory while a goal_card_submit interaction is
// blocked and restored from the persisted pendingInteraction Task payload
// after a restart (setPendingInteraction → saveMailbox).
type goalCardProposalState struct {
	CardID          string `json:"cardId"`
	InterpretedGoal string `json:"interpretedGoal,omitempty"`
}

// goalCardProposalFromTask reconstructs a proposal from a persisted
// interaction Task payload; returns nil when the payload lacks the required
// fields.
func goalCardProposalFromTask(task map[string]any) *goalCardProposalState {
	if task == nil {
		return nil
	}
	p := &goalCardProposalState{}
	p.CardID, _ = task["cardId"].(string)
	p.InterpretedGoal, _ = task["interpretedGoal"].(string)
	if p.CardID == "" {
		return nil
	}
	return p
}

// goalCardProposalPayload renders the proposal as the
// step.interaction_requested Task payload, mirroring the shape used by the
// frontend and restart recovery.
func goalCardProposalPayload(p *goalCardProposalState) map[string]any {
	if p == nil {
		return nil
	}
	payload := map[string]any{
		"cardId": p.CardID,
	}
	if p.InterpretedGoal != "" {
		payload["interpretedGoal"] = p.InterpretedGoal
	}
	return payload
}

// handleGoalCardSubmit is the callable handler for goal_card_submit, registered
// so the tool appears in the component-driven tool discovery chain. The turn
// engine intercepts goal_card_submit calls via onGoalCardSubmit for interactive
// confirmation, mirroring goal_submit.
func (a *Actor) handleGoalCardSubmit(ctx actor.Context, req gen.AgentGoalCardSubmitReq) (string, error) {
	return a.applyGoalCardSubmit(ctx, req)
}

// applyGoalCardSubmit stores the LLM's proposed task card and returns a request
// ID for the interaction step. The turn engine blocks on this until the user
// resolves.
func (a *Actor) applyGoalCardSubmit(ctx actor.Context, req gen.AgentGoalCardSubmitReq) (string, error) {
	if a.RawSession.Goal == nil {
		return "", fmt.Errorf("goal_card_submit: no active goal")
	}
	if a.RawSession.Goal.Confirmed {
		return "", fmt.Errorf("goal_card_submit: goal already confirmed (interpretedGoal=%q)", a.RawSession.Goal.InterpretedGoal)
	}
	cardID := strings.TrimSpace(req.CardID)
	if cardID == "" {
		return "", fmt.Errorf("goal_card_submit: CardId is empty")
	}
	if _, err := a.goalCardTaskContent(ctx, cardID); err != nil {
		return "", err
	}
	a.goalCardProposal = &goalCardProposalState{
		CardID:          cardID,
		InterpretedGoal: strings.TrimSpace(req.InterpretedGoal),
	}
	a.invalidateComponentSnapshot(ctx)
	requestID := ctx.NewID().String()
	a.goalCardSubmitPending = true
	a.goalCardSubmitRequestID = requestID
	// Persist immediately: the turn pauses here waiting for the user to
	// confirm. If the program crashes during this pause, the proposal must
	// survive so the agent can resume the goal_card_submit flow after restart
	// instead of losing it. The proposal itself is persisted with the pending
	// interaction record (setPendingInteraction → saveMailbox).
	a.saveMailbox(ctx)
	a.takeSnapshot()
	return requestID, nil
}

// emitGoalCardSubmitEvent emits the goal_card_submit interaction step event so
// the frontend can render a task-card confirmation card.
func (a *Actor) emitGoalCardSubmitEvent(ctx actor.Context, turnID, requestID string) {
	if a.goalCardProposal == nil {
		return
	}
	stepID := turnID + "-goal-card-submit-" + requestID
	payload := goalCardProposalPayload(a.goalCardProposal)
	ev := domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "goal_card_submit",
		RequestID:       requestID,
		Task:            payload,
		Seq:             a.allocSeq(),
	}
	a.applyStepEvent(ev)
	// Record the pending interaction before advertising the step so restart
	// recovery cannot downgrade this confirmation to a generic pause.
	a.setPendingInteraction(ctx, turnID, stepID, requestID, "goal_card_submit", payload)
	_ = a.emitActorStepEvent(ctx, ev)
	a.takeSnapshot()
}

// resolveGoalCardSubmit processes the user's goal_card_submit decision. On
// approval it creates the task card, binds the current agent to it, and starts
// execution consistently with the internal assign-goal semantics. Returns the
// decision ("approve" / "reject"), the request ID, any user feedback, the
// created task card ID (approve path), and the creation error (if any).
func (a *Actor) resolveGoalCardSubmit(ctx actor.Context, answer string) (decision, requestID, feedback, cardID string, err error) {
	a.goalCardSubmitPending = false
	requestID = a.goalCardSubmitRequestID
	a.goalCardSubmitRequestID = ""

	var ans struct {
		Decision string `json:"decision"`
		Feedback string `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(answer), &ans); err != nil {
		// Fallback: treat plain text as approve.
		decision = "approve"
	} else {
		decision = ans.Decision
		feedback = ans.Feedback
	}
	if decision == "" {
		decision = "approve"
	}

	if decision == "approve" || decision == "edit" {
		cardID, err = a.applyGoalCardApproval(ctx)
	}
	// The proposal is consumed either way: a rejected or failed proposal must
	// not leak into the next interaction, and a successful one is already
	// reflected in the goal binding.
	a.goalCardProposal = nil
	return decision, requestID, feedback, cardID, err
}

// applyGoalCardApproval creates the approved task card, claims it, binds the
// current agent to it, and starts execution. It mirrors handleInternalAssignGoal
// (the workspace spawn_assign path): Confirmed=true from the outset so the
// agent begins autonomous execution immediately, with the goal mounted and the
// workflow binding (BoundTaskCardID) in place.
func (a *Actor) applyGoalCardApproval(ctx actor.Context) (string, error) {
	proposal := a.goalCardProposal
	if proposal == nil {
		return "", fmt.Errorf("goal_card_submit: no pending card proposal")
	}
	content, err := a.goalCardTaskContent(ctx, proposal.CardID)
	if err != nil {
		return "", err
	}
	condition := content
	if err := a.claimGoalCardTaskCard(ctx, proposal.CardID); err != nil {
		return "", err
	}

	maxTurns := int32(DefaultGoalMaxTurns)
	if cfg := a.fetchAgentKindConfig(ctx); cfg.MaxTurns > 0 {
		maxTurns = cfg.MaxTurns
	}
	a.RawSession.Goal = &gen.SessionGoal{
		Condition:       condition,
		MaxTurns:        maxTurns,
		TurnCount:       0,
		InterpretedGoal: proposal.InterpretedGoal,
		Confirmed:       true,
		Status:          "active",
		BoundTaskCardID: proposal.CardID,
	}
	a.ensureGoalCardMounted(ctx)
	a.invalidateComponentSnapshot(ctx)
	// Persist immediately: the binding is a critical state transition that
	// gates autonomous execution. If the program crashes after approval but
	// before the next turn checkpoint, the binding must survive.
	a.saveMailbox(ctx)
	a.takeSnapshot()
	a.applyGoalTitle(ctx, proposal.InterpretedGoal, condition)
	return proposal.CardID, nil
}

func (a *Actor) goalCardTaskContent(ctx actor.Context, cardID string) (string, error) {
	projectRef, ok := ctx.LookupService("project")
	if !ok || ctx.Planner() == nil {
		return "", fmt.Errorf("goal_card_submit: project service unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := ctx.Planner().Call(callCtx, projectRef, "project.wiki_get_card", domain.WikiGetCardReq{ID: cardID}).Await()
	if err != nil || result == nil {
		if err == nil {
			err = fmt.Errorf("not found")
		}
		return "", fmt.Errorf("goal_card_submit: task card %q unavailable: %w", cardID, err)
	}
	resp, ok := result.(domain.WikiGetCardResp)
	if !ok {
		return "", fmt.Errorf("goal_card_submit: task card %q returned %T", cardID, result)
	}
	return taskCardBody(resp.Raw), nil
}

func taskCardBody(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "---\n") {
		if end := strings.Index(raw[4:], "\n---"); end >= 0 {
			raw = strings.TrimSpace(raw[end+8:])
		}
	}
	return raw
}

// claimGoalCardTaskCard transitions the existing task card from todo to
// doing, mirroring the workspace spawn_assign claim step so the workflow
// topology does not double-claim a card that a bound agent is already working.
func (a *Actor) claimGoalCardTaskCard(ctx actor.Context, cardID string) error {
	projectRef, ok := ctx.LookupService("project")
	if !ok || ctx.Planner() == nil {
		return fmt.Errorf("goal_card_submit: project service unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := ctx.Planner().Call(callCtx, projectRef, "project.wiki_set_status", domain.WikiSetStatusReq{
		ID:             cardID,
		Status:         "doing",
		ExpectedStatus: "todo",
	}).Await()
	if err != nil || result == nil {
		if err == nil {
			err = fmt.Errorf("empty response")
		}
		return fmt.Errorf("goal_card_submit: claim task card: %w", err)
	}
	return nil
}

// expireGoalCardSubmit degrades a pending goal_card_submit when the turn is
// cancelled. The goal stays active but unconfirmed, so the next turn can
// re-submit a fresh proposal.
func (a *Actor) expireGoalCardSubmit(ctx actor.Context, requestID string) {
	if a.goalCardSubmitRequestID == requestID {
		a.goalCardSubmitPending = false
		a.goalCardSubmitRequestID = ""
		a.goalCardProposal = nil
	}
}
