package project

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleWikiDispatchPlan reads a plan card and submits it to a target agent
// for execution. The agent receives the plan body as a chat message.
func (a *Actor) handleWikiDispatchPlan(ctx actor.PureContext, req gen.WikiDispatchPlanReq) (gen.WikiDispatchPlanResp, error) {
	if req.ID == "" {
		return gen.WikiDispatchPlanResp{}, fmt.Errorf("project.wiki.dispatch_plan: card id is required")
	}
	if req.AgentID == "" {
		return gen.WikiDispatchPlanResp{}, fmt.Errorf("project.wiki.dispatch_plan: agent id is required")
	}

	card, err := a.store.Get(req.ID)
	if err != nil {
		return gen.WikiDispatchPlanResp{}, fmt.Errorf("project.wiki.dispatch_plan: read card %s: %w", req.ID, err)
	}

	// Verify this is a plan card.
	if !containsString(card.Tags, "plan") {
		return gen.WikiDispatchPlanResp{}, fmt.Errorf("project.wiki.dispatch_plan: card %s is not a plan card (missing 'plan' tag)", req.ID)
	}

	// Resolve the target agent.
	agentRef, ok := a.lookupAgentRef(ctx, req.AgentID)
	if !ok {
		return gen.WikiDispatchPlanResp{}, fmt.Errorf("project.wiki.dispatch_plan: agent %s not found", req.AgentID)
	}

	// Build the dispatch message: plan body + optional feedback.
	msg := strings.TrimSpace(card.Body)
	if req.Feedback != "" {
		msg += "\n\n---\n" + strings.TrimSpace(req.Feedback)
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 30*time.Second)
	defer cancel()
	call := agentRef.Invoke(callCtx, "chat_submit", gen.AgentChatSubmitReq{
		Text: msg,
	})
	if call == nil {
		return gen.WikiDispatchPlanResp{}, fmt.Errorf("project.wiki.dispatch_plan: agent.chat.submit returned nil call")
	}

	ctx.Logger().Info("project: dispatched plan card to agent",
		"card", req.ID, "agent", req.AgentID)

	return gen.WikiDispatchPlanResp{
		ID:      req.ID,
		AgentID: req.AgentID,
	}, nil
}
