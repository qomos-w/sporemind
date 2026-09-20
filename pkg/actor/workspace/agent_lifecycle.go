package workspace

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

type agentLifecycleEvent struct {
	Kind      string
	Priority  string
	DedupeKey string
	Payload   string
}

type agentLifecyclePayload struct {
	AgentActorID        string `json:"agent_actor_id"`
	AgentKind           string `json:"agent_kind"`
	Title               string `json:"title"`
	PreviousState       string `json:"previous_state"`
	State               string `json:"state"`
	TaskSummary         string `json:"task_summary,omitempty"`
	Error               string `json:"error,omitempty"`
	LastTurnCompletedAt string `json:"last_turn_completed_at,omitempty"`
	OccurredAt          string `json:"occurred_at"`
}

func lifecycleEventForAgent(previous, current gen.AgentRuntimeState, item domain.AgentRef, now time.Time) (agentLifecycleEvent, bool) {
	kind, priority := lifecycleTransition(previous, current)
	if kind == "" {
		return agentLifecycleEvent{}, false
	}
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = strings.TrimSpace(item.DisplayName)
	}
	payload, err := json.Marshal(agentLifecyclePayload{
		AgentActorID:        item.ActorID,
		AgentKind:           item.AgentKind,
		Title:               title,
		PreviousState:       previous.State,
		State:               current.State,
		TaskSummary:         strings.TrimSpace(current.CurrentTaskSummary),
		Error:               lifecycleErrorSummary(current.Error),
		LastTurnCompletedAt: current.LastTurnCompletedAt,
		OccurredAt:          now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return agentLifecycleEvent{}, false
	}
	return agentLifecycleEvent{
		Kind:      kind,
		Priority:  priority,
		DedupeKey: item.ActorID + ":" + kind,
		Payload:   string(payload),
	}, true
}

func lifecycleTransition(previous, current gen.AgentRuntimeState) (kind, priority string) {
	wasBlocked := agentBlocked(previous)
	isBlocked := agentBlocked(current)
	switch {
	case previous.State != "running" && current.State == "running":
		return "agent.started", "low"
	case previous.State == "running" && current.State == "failed":
		return "agent.failed", "high"
	case previous.State == "running" && current.State != "running" && !isBlocked:
		return "agent.completed", "normal"
	case !wasBlocked && isBlocked:
		return "agent.blocked", "normal"
	default:
		return "", ""
	}
}

func agentBlocked(state gen.AgentRuntimeState) bool {
	return state.State == "paused" || state.ApprovalPending || state.PlanApprovalPending || state.AskUserPending || state.GoalSubmitPending
}

func (a *Actor) enqueueAgentLifecycle(ctx actor.PureContext, event agentLifecycleEvent) {
	glassRef, ok := ctx.LookupService("glass_interact")
	if !ok {
		return
	}
	planner := ctx.Planner()
	if planner == nil {
		return
	}
	planner.Call(ctx.Lifecycle(), glassRef, "glass_interact.event_enqueue", gen.GlassEventEnqueueReq{
		Source:    "agent",
		Kind:      event.Kind,
		Priority:  event.Priority,
		Payload:   event.Payload,
		DedupeKey: event.DedupeKey,
	}).AsCallback(func(_ any, err error) {
		if err != nil {
			ctx.Logger().Warn("workspace: enqueue agent lifecycle event", "kind", event.Kind, "err", err)
		}
	})
}

func lifecycleErrorSummary(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	return "Agent reported an error; inspect diagnostics for details."
}
