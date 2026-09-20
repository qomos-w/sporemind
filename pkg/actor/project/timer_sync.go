package project

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// syncSchedulerCard registers or unregisters a scheduler-prefixed card with
// the scheduler actor. Called from wiki create/update/delete handlers.
func (a *Actor) syncSchedulerCard(ctx actor.PureContext, cardID, raw string) {
	if !strings.HasPrefix(cardID, schedulerCardPrefix) {
		return
	}

	schedRef, ok := ctx.LookupService("scheduler")
	if !ok || schedRef == nil {
		ctx.Logger().Warn("project: scheduler service not found, skipping timer sync", "card", cardID)
		return
	}

	schedule := parseScheduleFromFrontmatter(raw)
	if schedule.Cron == "" && schedule.Expression == "" {
		// No schedule in frontmatter; unregister if previously registered and
		// cut the agent binding (lifecycle: unbind on schedule removal).
		a.unregisterFromScheduler(ctx, schedRef, cardID)
		a.applySchedulerUnbind(ctx, cardID, raw)
		return
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	schedRef.Invoke(callCtx, "scheduler.register", gen.SchedulerRegisterReq{
		ProjectID: a.actorID,
		CardID:    cardID,
		Schedule:  schedule,
	})
	// Binding lifecycle: a scheduler card that became (or stayed) bound gets
	// its scheduler mode mounted + ActiveScheduler entry written on the target
	// agent. Skipped quietly when the agent is not loaded — the agent_task
	// fire branch performs the same mount at run time.
	a.applySchedulerBind(ctx, cardID, raw)
}

// applySchedulerBind is the binding-lifecycle "mount" hook. When a scheduler
// card declares the bound mode and names a bound agent that is currently
// loaded, scheduler_bind is fire-and-forgotten to the agent: it mounts
// builtin:mode:scheduler (badge/execution surface) and upserts the
// ActiveScheduler entry. Unloaded agents are skipped — the fire branch mounts
// on the next run.
func (a *Actor) applySchedulerBind(ctx actor.PureContext, cardID, raw string) {
	card := decodeCard(cardID, raw)
	mode, explicit := explicitBindMode(card)
	if !explicit || mode != schedulerBindModeBound {
		return
	}
	agentRef := boundAgentOf(card)
	if agentRef == "" {
		return
	}
	ag, found := a.agentRegistryEntry(ctx, agentRef)
	if !found || ag.ActorID == "" {
		return
	}
	r, ok := a.liveAgentRef(ctx, ag.ActorID)
	if !ok {
		return
	}
	schedName := frontmatterValue(raw, "title")
	if schedName == "" {
		schedName = cardID
	}
	a.fireAndForgetAgentCall(ctx, r, "scheduler_bind", gen.AgentSchedulerBindReq{
		SchedulerCardID: cardID,
		SchedulerName:   schedName,
	})
}

// applySchedulerUnbind is the binding-lifecycle "unmount" hook. A scheduler
// card that is deleted or loses its schedule unbinds its bound agent:
// scheduler_unbind removes the ActiveScheduler entry (and unmounts the
// scheduler mode once the agent holds no other scheduler bindings).
//
// A registered-but-unloaded agent (gray AgentRef after workspace.agent_unload
// or a process restart) is loaded first so the unbind can reach its persisted
// session: otherwise the ActiveScheduler entry + mounted scheduler mode survive
// in the mailbox and a lazy reload revives a stale badge for a card that was
// deleted or rebound. Only a truly missing registry row is skipped.
func (a *Actor) applySchedulerUnbind(ctx actor.PureContext, cardID, raw string) {
	card := decodeCard(cardID, raw)
	agentRef := boundAgentOf(card)
	if agentRef == "" {
		return
	}
	ag, found := a.agentRegistryEntry(ctx, agentRef)
	if !found || ag.ActorID == "" {
		return
	}
	r, ok := a.liveAgentRef(ctx, ag.ActorID)
	if !ok {
		loaded, err := a.ensureSchedulerAgentLoaded(ctx, ag)
		if err != nil {
			// The agent cannot be reached to clear the binding. Log rather than
			// silently returning so the leftover binding is diagnosable.
			ctx.Logger().Warn("project: scheduler unbind skipped (agent not addressable)", "card", cardID, "agent", agentRef, "error", err)
			return
		}
		r = loaded
	}
	a.fireAndForgetAgentCall(ctx, r, "scheduler_unbind", gen.AgentSchedulerUnbindReq{SchedulerCardID: cardID})
}

// unbindSchedulerAgent fire-and-forgets scheduler_unbind to a run agent. It is
// the completion-side counterpart of applySchedulerUnbind: an ephemeral agent
// must be unbound before workspace.agent_unload destroys it, or the persisted
// ActiveScheduler entry + mounted scheduler mode survive and revive a stale
// badge on lazy reload. A missing/undeaddressable agent is a no-op (the actor is
// gone anyway).
func (a *Actor) unbindSchedulerAgent(ctx actor.PureContext, agentActorID, cardID string) {
	r, ok := a.lookupAgentRef(ctx, agentActorID)
	if !ok {
		return
	}
	a.fireAndForgetAgentCall(ctx, r, "scheduler_unbind", gen.AgentSchedulerUnbindReq{SchedulerCardID: cardID})
}

// maybeUnbindOldSchedulerAgent compares the previous and new raw of an edited
// scheduler card and unbinds the previously bound agent when the binding moved
// to a different agent or was removed entirely. Called from the wiki edit path
// before syncSchedulerCard applies the new binding.
func (a *Actor) maybeUnbindOldSchedulerAgent(ctx actor.PureContext, oldRaw, newRaw string) {
	oldCard := decodeCard("", oldRaw)
	newCard := decodeCard("", newRaw)
	oldAgent := boundAgentOf(oldCard)
	newAgent := boundAgentOf(newCard)
	if oldAgent == "" || oldAgent == newAgent {
		return
	}
	a.applySchedulerUnbind(ctx, oldCard.Title, oldRaw)
}

// liveAgentRef resolves a canonical ActorID to an addressable live actor ref.
func (a *Actor) liveAgentRef(ctx actor.PureContext, actorID string) (ref.Ref, bool) {
	cid, err := identity.ParseCanonicalID(actorID)
	if err != nil {
		return nil, false
	}
	r, ok := ctx.LookupID(id.From(cid))
	return r, ok && r != nil
}

// unregisterFromScheduler tells the scheduler to stop tracking a card.
func (a *Actor) unregisterFromScheduler(ctx actor.PureContext, schedRef ref.Ref, cardID string) {
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	schedRef.Invoke(callCtx, "scheduler.unregister", gen.SchedulerUnregisterReq{
		ProjectID: a.actorID,
		CardID:    cardID,
	})
}

// parseScheduleFromFrontmatter extracts cron/expression from a card's YAML
// frontmatter. Returns an empty TimerSchedule if no schedule block is found.
func parseScheduleFromFrontmatter(raw string) gen.TimerSchedule {
	card := decodeCard("", raw)
	if value, ok := card.Data["schedule"].(map[string]any); ok {
		return timerScheduleFromData(value)
	}
	return parseTopLevelSchedule(raw)
}

func timerScheduleFromData(data map[string]any) gen.TimerSchedule {
	return gen.TimerSchedule{
		Cron:       stringValue(data["cron"]),
		Expression: stringValue(data["expression"]),
		Timezone:   stringValue(data["timezone"]),
		Enabled:    boolValue(data["enabled"], true),
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func boolValue(value any, def bool) bool {
	if value == nil {
		return def
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return def
		}
		return b
	}
	return def
}

func parseTopLevelSchedule(raw string) gen.TimerSchedule {
	sched := gen.TimerSchedule{Enabled: true}
	if !strings.HasPrefix(raw, "---") {
		return sched
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return sched
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")

	inSchedule := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" {
			inSchedule = false
			continue
		}
		indented := line != trimmed
		if !indented {
			key, _, _ := strings.Cut(trimmed, ":")
			inSchedule = strings.TrimSpace(key) == "schedule"
			continue
		}
		if !inSchedule {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, "\"'")
		switch key {
		case "cron":
			sched.Cron = value
		case "expression":
			sched.Expression = value
		case "timezone":
			sched.Timezone = value
		case "enabled":
			sched.Enabled = boolValue(value, true)
		}
	}
	return sched
}
