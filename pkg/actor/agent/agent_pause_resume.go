package agent

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleAgentPause is the agent-local dispatch entry for pause requests
// forwarded by workspace.agent_pause after resolving the target agent.
//
// Routing by lifecycle state:
//
//  1. Turn engine running → user-level pause via handleTurnPause (the engine
//     pauses at the next safe point; resume goes through turn_resume).
//  2. Workflow-active owner whose turn is waiting (blocked on a child or an
//     interaction-free wait) → aggregate pause via handleWorkflowPauseAll:
//     every live direct child gets turn_pause and the owner transitions
//     waiting→paused with PauseReason "user".
//  3. Any other state → idempotent no-op returning Sent=false, no error.
func (a *Actor) handleAgentPause(ctx actor.Context, req gen.AgentPauseReq) (gen.AgentPauseResp, error) {
	_ = req // Reason is accepted for the workspace contract but not forwarded.

	// Branch 1: live turn engine — user-level pause at the next safe point.
	if eng := a.activeTurnEngine(); eng != nil {
		ctx.Logger().Info("agent: agent_pause dispatching user-level turn_pause", "turn", a.getActiveTurnRef())
		if err := a.handleTurnPause(ctx); err != nil {
			return gen.AgentPauseResp{}, fmt.Errorf("agent.agent_pause: %w", err)
		}
		return gen.AgentPauseResp{Sent: true}, nil
	}

	// Branch 2: workflow owner waiting — cascade pause to all children + owner.
	if a.workflowActive() && a.status.State == domain.TurnStateWaiting {
		resp, err := a.handleWorkflowPauseAll(ctx, domain.AgentWorkflowPauseAllReq{})
		if err != nil {
			return gen.AgentPauseResp{}, fmt.Errorf("agent.agent_pause: %w", err)
		}
		ctx.Logger().Info("agent: agent_pause dispatched workflow pause-all",
			"paused", resp.PausedCount, "skipped", resp.SkippedCount)
		return gen.AgentPauseResp{Sent: true}, nil
	}

	// Branch 3: any other state — idempotent no-op, log the current state.
	ctx.Logger().Info("agent: agent_pause idempotent no-op",
		"state", a.status.State, "workflowActive", a.workflowActive())
	return gen.AgentPauseResp{Sent: false}, nil
}

// handleAgentResume is the agent-local dispatch entry for resume requests
// forwarded by workspace.agent_resume. It delegates unconditionally to
// handleTurnResume, which internally handles:
//   - Workflow-active owner: lazy-loads + cascades turn_resume to all children
//     via resumeChildAgents (see agent_turn.go:1139).
//   - Active engine paused by user: wakes the engine.
//   - Paused-from-waiting workflow owner: restores waiting state.
//   - Crash-recovery paused turn: continues in place.
//
// handleTurnResume is idempotent for non-paused children (no-op) and safe
// to call unconditionally.
func (a *Actor) handleAgentResume(ctx actor.Context, req gen.AgentResumeReq) (gen.AgentResumeResp, error) {
	_ = req // Reason is accepted for the workspace contract but not forwarded.

	if err := a.handleTurnResume(ctx); err != nil {
		return gen.AgentResumeResp{}, fmt.Errorf("agent.agent_resume: %w", err)
	}
	return gen.AgentResumeResp{Sent: true}, nil
}
