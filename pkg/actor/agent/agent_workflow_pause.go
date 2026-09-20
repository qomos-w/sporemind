package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// childPauseDispatchTimeout bounds the fire-and-forget turn_pause invoke to a
// child agent. The invoke only has to be accepted for delivery, not completed.
const childPauseDispatchTimeout = 5 * time.Second

// handleWorkflowPauseAll is the aggregate pause callable for a workflow owner
// whose turn is waiting. It dispatches turn_pause to every live child agent
// (workspace topology ParentAgentId == self), then transitions the owner's own
// waiting turn to paused with PauseReason "user" so the workflow can be
// reviewed and resumed later.
//
// Guarded on workflowActive && status waiting: the frontend only shows the
// pause button in that state, but the backend must defend itself too. The
// guard error path is also the idempotency story — repeated calls or calls in
// any other state return an error and produce no side effects.
func (a *Actor) handleWorkflowPauseAll(ctx actor.Context, _ domain.AgentWorkflowPauseAllReq) (domain.AgentWorkflowPauseAllResp, error) {
	if !a.workflowActive() {
		return domain.AgentWorkflowPauseAllResp{}, fmt.Errorf("agent.workflow_pause_all: no active workflow")
	}
	if a.status.State != domain.TurnStateWaiting {
		return domain.AgentWorkflowPauseAllResp{}, fmt.Errorf(
			"agent.workflow_pause_all: owner turn state %q, want %q", a.status.State, domain.TurnStateWaiting)
	}
	owner := a.actorID
	if owner == "" {
		owner = ctx.Self().ID().String()
	}
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		return domain.AgentWorkflowPauseAllResp{}, fmt.Errorf("agent.workflow_pause_all: workspace is unavailable")
	}
	call := wsRef.Invoke(ctx.Lifecycle(), "workspace.list_agents", domain.WorkspaceListAgentsReq{ProjectID: parentProjectID(ctx)})
	if call == nil {
		return domain.AgentWorkflowPauseAllResp{}, fmt.Errorf("agent.workflow_pause_all: list agents failed")
	}
	defer call.Close()
	listCtx, cancelList := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancelList()
	result, err := call.Final(listCtx)
	if err != nil {
		return domain.AgentWorkflowPauseAllResp{}, fmt.Errorf("agent.workflow_pause_all: list agents: %w", err)
	}
	var list domain.AgentRefListResp
	switch value := result.(type) {
	case domain.AgentRefListResp:
		list = value
	case *domain.AgentRefListResp:
		if value == nil {
			return domain.AgentWorkflowPauseAllResp{}, fmt.Errorf("agent.workflow_pause_all: list agents returned nil")
		}
		list = *value
	default:
		return domain.AgentWorkflowPauseAllResp{}, fmt.Errorf("agent.workflow_pause_all: unexpected list agents response %T", result)
	}

	var resp domain.AgentWorkflowPauseAllResp
	for _, ag := range list.Items {
		if ag.ParentAgentID != owner || ag.DeletionStatus == "deleting" {
			resp.SkippedCount++
			continue
		}
		childRef := resolveChildRef(ctx, ag.ActorID)
		if childRef == nil {
			resp.SkippedCount++
			ctx.Logger().Warn("agent.workflow_pause_all: child ref unavailable, skipping",
				"child", ag.ID, "actorID", ag.ActorID)
			continue
		}
		if dispatchChildTurnPause(ctx.Lifecycle(), childRef) {
			resp.PausedCount++
		} else {
			resp.SkippedCount++
		}
	}

	// Owner self-transition waiting→paused. The turn record keeps its
	// canonical history via applyTurnLifecycle; status mirrors it for the
	// workspace projection. A rejected transition is logged and leaves the
	// record untouched (reducer semantics).
	turnID := a.getActiveTurnRef()
	if turnID != "" {
		if _, lcErr := a.applyTurnLifecycle(ctx, domain.TurnLifecyclePaused, turnID, nil, turnLifecycleOptions{
			role:        "assistant",
			turnOrder:   a.getActiveTurnOrder(),
			pauseReason: domain.PauseReasonUser,
		}); lcErr != nil {
			ctx.Logger().Error("agent.workflow_pause_all: pause turn lifecycle rejected; record retains prior state",
				"turn", turnID, "error", lcErr)
		}
	}
	a.status.State = domain.TurnStatePaused
	a.status.PauseKind = domain.PauseReasonUser
	a.notifyWorkspaceStatus(ctx)
	return resp, nil
}

// dispatchChildTurnPause delivers turn_pause to a child agent without waiting
// for its response (fire-and-forget, mirroring the updater's
// dispatchOwnerChatSubmit pattern). Returns true when the invoke was accepted
// for delivery.
func dispatchChildTurnPause(parent context.Context, target ref.Ref) bool {
	callCtx, cancel := context.WithTimeout(parent, childPauseDispatchTimeout)
	defer cancel()
	call := target.Invoke(callCtx, "turn_pause", nil)
	if call == nil {
		return false
	}
	defer call.Close()
	return true
}
