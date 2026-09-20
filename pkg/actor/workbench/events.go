package workbench

import (
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Event kinds the actor ingests. These are declared by their emitters
// (agent registers "step"/"turn"; appmanager registers "app_lifecycle";
// workspace registers "agent_list_state").
const (
	eventAppLifecycle   = "app_lifecycle"
	eventStep           = "step"
	eventTurn           = "turn"
	eventAgentListState = "agent_list_state"
)

// terminalKeywords summon the terminal card when they appear in agent activity
// (step text / tool name / command input). Matching is case-insensitive.
var terminalKeywords = []string{
	"$ ",
	"shell",
	"terminal",
	"bash",
	"powershell",
	"cmd.exe",
	"go run",
	"go test",
	"npm ",
	"pnpm ",
	"yarn ",
	"pytest",
	"cargo ",
	"命令",
	"终端",
}

// subscribeEvents wires the three score sources onto the lane-routed ingress
// callables. The callbacks only forward (fire-and-forget) — they never mutate
// state, so the event drain goroutine is never blocked and every write lands on
// the workbench_score lane in order.
func (a *Actor) subscribeEvents(ctx actor.Context) error {
	subs := []struct {
		kind   string
		handle func(actor.EventEnvelope)
	}{
		{eventAppLifecycle, func(env actor.EventEnvelope) {
			ev, ok := env.Payload.(gen.AppLifecycleEvent)
			if !ok {
				return
			}
			a.dispatch(callIngestLifecycle, gen.WorkbenchLifecycleIngestReq{Event: ev})
		}},
		{eventStep, func(env actor.EventEnvelope) {
			ev, ok := env.Payload.(gen.StepEvent)
			if !ok {
				return
			}
			a.dispatch(callIngestStep, gen.WorkbenchStepIngestReq{EmitterID: env.ActorId, Step: ev})
		}},
		{eventTurn, func(env actor.EventEnvelope) {
			ev, ok := env.Payload.(gen.TurnEvent)
			if !ok {
				return
			}
			a.dispatch(callIngestTurn, gen.WorkbenchTurnIngestReq{EmitterID: env.ActorId, Turn: ev})
		}},
		{eventAgentListState, func(env actor.EventEnvelope) {
			ev, ok := env.Payload.(gen.WorkspaceAgentListStateEvent)
			if !ok {
				return
			}
			items := make([]gen.WorkbenchAgentModeInfo, 0, len(ev.State.Items))
			for _, it := range ev.State.Items {
				if it.ActorID == "" {
					continue
				}
				items = append(items, gen.WorkbenchAgentModeInfo{
					ActorID:        it.ActorID,
					Kind:           it.AgentKind,
					DisplayName:    it.DisplayName,
					WorkflowActive: it.Mode != nil && it.Mode.ActiveWorkflowMapCardID != "",
					BoundTask:      it.Mode != nil && it.Mode.BoundTaskCardID != "",
				})
			}
			a.dispatch(callIngestAgentList, gen.WorkbenchAgentListIngestReq{Items: items})
		}},
	}
	for _, sub := range subs {
		cancel, err := ctx.SubscribeEventKind(sub.kind, sub.handle)
		if err != nil {
			return err
		}
		a.cancelSubs = append(a.cancelSubs, cancel)
	}
	return nil
}

// handleIngestLifecycle creates / retires an app card from an app lifecycle
// event. Active states raise the card; terminal states retire it. Repeated
// identical events (heartbeats, re-announcements) are no-ops: a boost per
// event would make the scores (and therefore the layout) churn forever.
func (a *Actor) handleIngestLifecycle(ctx actor.Context, req gen.WorkbenchLifecycleIngestReq) error {
	ev := req.Event
	if ev.ID == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	id := appCardPrefix + ev.ID
	c := a.cards[id]
	isNew := c == nil
	if isNew {
		c = a.newCardLocked(id, kindApp)
		c.title = ev.ID
	}
	status := lifecycleStatus(ev)
	label := "应用 " + lifecycleLabel(ev)
	active := activeLifecycle(ev.Kind, ev.State)
	changed := isNew
	if c.status != status {
		c.status = status
		changed = true
	}
	if active {
		// Running only establishes presence at the base (raise-only): a
		// steady-state heartbeat must not move the board at all.
		if c.hidden {
			c.hidden = false
			if c.score < DefaultScore {
				c.score = DefaultScore
			}
			c.lastActive = time.Now().UnixNano()
			changed = true
		}
	} else if !c.hidden {
		c.hidden = true
		changed = true
	}
	if !changed {
		return nil
	}
	c.why = label
	a.appendRecentLocked("", attentionKindLifecycle, "应用 "+ev.ID+" "+lifecycleLabel(ev))
	a.refreshLocked()
	if err := a.saveLocked(); err != nil {
		ctx.Logger().Warn("workbench: persist lifecycle ingest failed", "err", err)
	}
	return nil
}

// handleIngestStep records agent activity for the awareness ring. Steps are
// presence-only: an already-visible card is NOT re-scored and its lastActive is
// NOT refreshed, so background conversation can never churn the layout. A new
// or hidden card surfaces at its mode's base score.
func (a *Actor) handleIngestStep(_ actor.Context, req gen.WorkbenchStepIngestReq) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if req.EmitterID != "" {
		a.touchAgentCardLocked(req.EmitterID)
		a.appendRecentLocked(req.EmitterID, attentionKindStep, stepSummary(req.Step))
	}
	if kw := matchTerminalKeyword(&req.Step); kw != "" {
		a.summonTerminalLocked(kw)
	}
	a.refreshLocked()
	return nil
}

// handleIngestTurn records turn activity — same presence-only contract as
// handleIngestStep.
func (a *Actor) handleIngestTurn(_ actor.Context, req gen.WorkbenchTurnIngestReq) error {
	if req.EmitterID == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	a.touchAgentCardLocked(req.EmitterID)
	a.appendRecentLocked(req.EmitterID, attentionKindTurn, "turn "+req.Turn.Kind)
	a.refreshLocked()
	return nil
}

// touchAgentCardLocked establishes the emitting agent's card: created (or
// surfaced) at its mode's presence base, untouched when already visible. The
// mode comes from the cached workspace agent-list projection (normal until the
// first agent_list_state event arrives).
func (a *Actor) touchAgentCardLocked(emitterID string) {
	id := agentCardPrefix + emitterID
	c := a.cards[id]
	if c == nil {
		c = a.newCardLocked(id, kindChat)
		c.title = "Agent"
		c.icon = "bot"
		if info, ok := a.agentModes[emitterID]; ok {
			c.mode = modeFromAgentInfo(info)
			if info.DisplayName != "" {
				c.title = info.DisplayName
			}
		}
		c.score = baseForMode(c.mode)
		c.why = "agent 活动"
	}
	if c.hidden {
		c.hidden = false
		if c.score < baseForMode(c.mode) {
			c.score = baseForMode(c.mode)
		}
		c.lastActive = time.Now().UnixNano()
	}
}

// handleIngestAgentList applies the workspace agent-list snapshot: it caches
// per-agent attention modes and wires the global Coordinator — its card is
// materialized (visible, highest base) as soon as the singleton exists, so the
// board's default stage is the coordinator conversation even before any
// activity. Mode flips only lift a resting card to its new base; they never
// lower a user-promoted score.
func (a *Actor) handleIngestAgentList(_ actor.Context, req gen.WorkbenchAgentListIngestReq) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.agentModes == nil {
		a.agentModes = make(map[string]gen.WorkbenchAgentModeInfo, len(req.Items))
	}
	changed := false
	for _, info := range req.Items {
		a.agentModes[info.ActorID] = info
		mode := modeFromAgentInfo(info)

		id := agentCardPrefix + info.ActorID
		c := a.cards[id]
		if c == nil {
			if mode != modeCoordinator {
				// Non-coordinator cards materialize on first activity, not on
				// list presence — an idle agent list must not flood the board.
				continue
			}
			c = a.newCardLocked(id, kindChat)
			c.title = "Coordinator"
			if info.DisplayName != "" {
				c.title = info.DisplayName
			}
			c.icon = "bot"
			c.mode = mode
			c.score = baseForMode(mode)
			c.lastActive = time.Now().UnixNano()
			c.why = "coordinator"
			changed = true
		}
		if info.DisplayName != "" && (c.title == "Agent" || c.title == id || c.title == "Coordinator") {
			c.title = info.DisplayName
			changed = true
		}
		if c.mode != mode {
			applyModeLocked(c, mode)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	a.refreshLocked()
	if err := a.saveLocked(); err != nil {
		// agentModes is already updated; a persist failure only risks re-applying
		// on the next event. Log-free: the lane has no logger in scope.
		_ = err
	}
	return nil
}

// modeFromAgentInfo derives the attention mode from the workspace agent kind
// and mode state: the global Coordinator singleton, workflow owners, task-bound
// workers, everything else normal.
func modeFromAgentInfo(info gen.WorkbenchAgentModeInfo) string {
	switch {
	case info.Kind == "coordinator":
		return modeCoordinator
	case info.Kind == "worker" || info.BoundTask:
		return modeWorker
	case info.WorkflowActive:
		return modeWorkflow
	default:
		return modeNormal
	}
}

// summonTerminalLocked restores the terminal card, raises its score and resets
// the retreat clock.
func (a *Actor) summonTerminalLocked(keyword string) {
	c := a.cards[terminalCardID]
	if c == nil {
		c = a.newCardLocked(terminalCardID, kindTerminal)
		c.title = "终端"
		c.icon = "terminal"
	}
	c.hidden = false
	c.score = capScore(c.score + summonBoost)
	c.why = "终端召唤（" + keyword + "）"
	c.lastActive = time.Now().UnixNano()
}

// matchTerminalKeyword returns the first terminal keyword found in the step's
// text / tool name / command input / task map, or "" when none match.
func matchTerminalKeyword(ev *gen.StepEvent) string {
	if ev == nil {
		return ""
	}
	var b strings.Builder
	if ev.Block != nil {
		b.WriteString(ev.Block.Text)
		b.WriteByte(' ')
		b.WriteString(ev.Block.ToolName)
		b.WriteByte(' ')
		b.WriteString(ev.Block.Input)
		b.WriteByte(' ')
	}
	b.WriteString(ev.Delta)
	for _, v := range ev.Task {
		if s, ok := v.(string); ok {
			b.WriteByte(' ')
			b.WriteString(s)
		}
	}
	text := strings.ToLower(b.String())
	if strings.TrimSpace(text) == "" {
		return ""
	}
	for _, kw := range terminalKeywords {
		if strings.Contains(text, kw) {
			return strings.TrimSpace(kw)
		}
	}
	return ""
}

// activeLifecycle reports whether the lifecycle kind/state names a live app.
func activeLifecycle(kind, state string) bool {
	for _, v := range []string{kind, state} {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "running", "reloaded", "registered", "loaded", "start", "started":
			return true
		}
	}
	return false
}

func lifecycleLabel(ev gen.AppLifecycleEvent) string {
	label := ev.Kind
	if label == "" {
		label = ev.State
	}
	if label == "" {
		label = "unknown"
	}
	return label
}

// lifecycleStatus is the compact status text: the state, with a truncated error
// detail appended when the event carries one (log/field truncation discipline).
func lifecycleStatus(ev gen.AppLifecycleEvent) string {
	status := ev.State
	if status == "" {
		status = ev.Kind
	}
	if ev.Error != "" {
		errText := ev.Error
		if len(errText) > 120 {
			errText = errText[:120] + "...(truncated)"
		}
		if status == "" {
			return errText
		}
		return status + ": " + errText
	}
	return status
}
