package agent

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// schedulerModeCardID is the builtin scheduler mode card mounted on a bound
// agent by the project execution engine.
const schedulerModeCardID = "builtin:mode:scheduler"

// upsertActiveSchedulerEntry adds or replaces a scheduler card binding in
// RawSession.ActiveScheduler, keyed by SchedulerCardID. It preserves any other
// bindings the agent already holds (one agent may be bound to several cards).
func (a *Actor) upsertActiveSchedulerEntry(entry gen.ActiveSchedulerEntry) {
	entries := a.RawSession.ActiveScheduler
	for i := range entries {
		if entries[i].SchedulerCardID == entry.SchedulerCardID {
			entries[i] = entry
			a.RawSession.ActiveScheduler = entries
			return
		}
	}
	a.RawSession.ActiveScheduler = append(entries, entry)
}

// removeActiveSchedulerEntry removes the binding for SchedulerCardID, returning
// the remaining entries (nil when none remain).
func (a *Actor) removeActiveSchedulerEntry(cardID string) []gen.ActiveSchedulerEntry {
	entries := a.RawSession.ActiveScheduler
	out := entries[:0]
	for _, e := range entries {
		if e.SchedulerCardID != cardID {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		out = nil
	}
	a.RawSession.ActiveScheduler = out
	return out
}

// handleSchedulerBind records one bound-mode scheduler card on the agent. It
// ensures builtin:mode:scheduler is mounted (the badge/execution surface) and
// upserts the binding into RawSession.ActiveScheduler. Called by the project
// execution engine from the bound fire branch and the binding lifecycle (card
// create/update to bound). Idempotent: re-binding the same card is a no-op
// beyond the entry refresh.
func (a *Actor) handleSchedulerBind(ctx actor.Context, req gen.AgentSchedulerBindReq) (gen.AgentSchedulerBindResp, error) {
	if req.SchedulerCardID == "" {
		return gen.AgentSchedulerBindResp{}, fmt.Errorf("agent.scheduler_bind: SchedulerCardId is required")
	}

	// Ensure the scheduler mode is mounted so the badge/execution surface is
	// live. Mounting re-enters the component channel (saveMailbox + status
	// notify), so skip it entirely when already mounted to avoid a redundant
	// revision bump.
	if !a.cardRefEnabled(schedulerModeCardID) {
		if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{
			CardID: schedulerModeCardID, Enabled: true, Scope: "user",
		}); err != nil {
			return gen.AgentSchedulerBindResp{}, fmt.Errorf("agent.scheduler_bind: mount scheduler mode: %w", err)
		}
	}

	a.upsertActiveSchedulerEntry(gen.ActiveSchedulerEntry{
		SchedulerCardID: req.SchedulerCardID,
		SchedulerName:   req.SchedulerName,
	})
	a.saveMailbox(ctx)
	a.notifyWorkspaceStatus(ctx)
	return gen.AgentSchedulerBindResp{ActiveScheduler: a.RawSession.ActiveScheduler}, nil
}

// handleSchedulerUnbind removes one scheduler card binding. When the agent no
// longer holds any scheduler bindings the scheduler mode is unmounted (which
// also clears the now-empty ActiveScheduler list via onModeUnmounted). When
// other bindings remain the mode stays mounted so the badge persists.
func (a *Actor) handleSchedulerUnbind(ctx actor.Context, req gen.AgentSchedulerUnbindReq) (gen.AgentSchedulerUnbindResp, error) {
	if req.SchedulerCardID == "" {
		return gen.AgentSchedulerUnbindResp{}, fmt.Errorf("agent.scheduler_unbind: SchedulerCardId is required")
	}
	remaining := a.removeActiveSchedulerEntry(req.SchedulerCardID)
	if len(remaining) == 0 && a.cardRefEnabled(schedulerModeCardID) {
		if _, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: schedulerModeCardID}); err != nil {
			// Degrade to a log: the in-memory binding list is already cleared,
			// so returning early here would leave memory and the persisted
			// mailbox divergent (the agent would keep the scheduler binding
			// after a reload). Always flush and notify; a failed unmount is
			// still surfaced by the log line.
			ctx.Logger().Error("agent.scheduler_unbind: unmount scheduler mode failed", "error", err)
		}
	}
	a.saveMailbox(ctx)
	a.notifyWorkspaceStatus(ctx)
	return gen.AgentSchedulerUnbindResp{ActiveScheduler: remaining}, nil
}
