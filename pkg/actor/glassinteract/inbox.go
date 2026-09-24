// Stage 5: the online glass event inbox and the session-gated Coordinator
// updater.
//
// Explicit event sources (turn/diagnostic/build) enqueue through the internal
// glass_interact.event.enqueue callable; there is no generic actor-to-actor
// event bus. While a glass session is online the inbox is persisted in the
// actor's durable state, ordered by priority then creation time. The
// Coordinator completes each delivered event via the internal
// glass_interact.event.complete callable with one of handled/ignored/defer/
// failed. An in_flight event with no receipt for 2 minutes returns to pending
// and increments retries; TTLs are 30m/10m/2m for high/normal/low priority
// and defer never exceeds the event TTL.
//
// The updater runs while a session is online: it polls the Coordinator every
// 5 seconds (and immediately on enqueue/claim/complete) and delivers at most
// one event at a time, only when the Coordinator reports not running and no
// active call/turn. Delivery is an explicit internal Coordinator callable
// (coordinator.ingest_glass_event) carrying GlassEventDeliverReq; every
// attempt is recorded on the bounded delivery log and emitted/observed through
// the debug surface. The Coordinator-side intake callable is a documented
// protocol contract; if the running Coordinator does not expose it, delivery
// fails safely and the event is requeued with a bounded retry.
package glassinteract

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	callableEventEnqueue  = "glass_interact.event_enqueue"
	callableEventComplete = "glass_interact.event_complete"
	callableInboxState    = "glass_interact.inbox_state"
	callableUpdaterTick   = "glass_interact.updater_tick"
)

const (
	eventEnqueued  = "glass.event.enqueued"
	eventDelivered = "glass.event.delivered"
	eventCompleted = "glass.event.completed"
)

// coordinatorEventIntake is the explicit internal Coordinator callable that
// receives each delivered event. It is part of the Stage 5 delivery protocol;
// the Coordinator agent implements it (out of scope for the glassinteract
// actor). It is deliberately not a broad agent call.
const coordinatorEventIntake = "coordinator_ingest_glass_event"

// Inbox priority bands and their fixed TTLs.
const (
	priorityHigh   = "high"
	priorityNormal = "normal"
	priorityLow    = "low"

	ttlHigh   = 30 * time.Minute
	ttlNormal = 10 * time.Minute
	ttlLow    = 2 * time.Minute
)

const (
	// inflightRetryWindow is how long an in_flight event waits for a receipt
	// before returning to pending with an incremented retry counter.
	inflightRetryWindow = 2 * time.Minute
	// retryBackoff is the base delay before a failed delivery/outcome is
	// eligible again.
	retryBackoff = 2 * time.Minute
	// deferWindow is the base delay for a "defer" outcome. Defer never pushes
	// past the event TTL.
	deferWindow = 30 * time.Second
	// updaterInterval is the session-gated Coordinator poll period.
	updaterInterval = 5 * time.Second

	// inboxMaxEvents bounds the retained event history (terminal + active).
	inboxMaxEvents = 256
)

// inboxState is the persisted event state.
type inboxState string

const (
	statePending   inboxState = "pending"
	stateInflight  inboxState = "in_flight"
	stateCompleted inboxState = "completed"
	stateIgnored   inboxState = "ignored"
	stateExpired   inboxState = "expired"
)

func (s inboxState) terminal() bool {
	switch s {
	case stateCompleted, stateIgnored, stateExpired:
		return true
	}
	return false
}

// Coordinator completion outcomes (wire values of GlassEventCompleteReq).
const (
	outcomeHandled = "handled"
	outcomeIgnored = "ignored"
	outcomeDefer   = "defer"
	outcomeFailed  = "failed"
)

// inboxEvent is one inbox item. Only the fields carried by inboxEventDurable
// are persisted; the struct itself is runtime state.
type inboxEvent struct {
	EventID     string
	Source      string
	Kind        string
	Priority    string
	DedupeKey   string
	Payload     string
	State       inboxState
	CreatedAt   time.Time
	TTLAt       time.Time
	RetryAt     time.Time
	Retries     int
	InFlightAt  time.Time
	CompletedAt time.Time
	Outcome     string
}

func (e *inboxEvent) toGen() gen.GlassEvent {
	return gen.GlassEvent{
		EventID:     e.EventID,
		Source:      e.Source,
		Kind:        e.Kind,
		Priority:    e.Priority,
		Payload:     e.Payload,
		DedupeKey:   e.DedupeKey,
		State:       string(e.State),
		CreatedAt:   formatTime(e.CreatedAt),
		TTLAt:       formatTime(e.TTLAt),
		RetryAt:     formatTime(e.RetryAt),
		Retries:     int64(e.Retries),
		InFlightAt:  formatTime(e.InFlightAt),
		CompletedAt: formatTime(e.CompletedAt),
		Outcome:     e.Outcome,
	}
}

func (e *inboxEvent) clone() *inboxEvent {
	c := *e
	return &c
}

// priorityRank orders the fixed priority bands: high sorts before normal
// before low; unknown priorities sort last.
func priorityRank(p string) int {
	switch p {
	case priorityHigh:
		return 0
	case priorityNormal:
		return 1
	case priorityLow:
		return 2
	}
	return 3
}

func ttlForPriority(p string) time.Duration {
	switch p {
	case priorityHigh:
		return ttlHigh
	case priorityLow:
		return ttlLow
	}
	return ttlNormal
}

// lessThan reports whether a is scheduled before b: priority first, then
// creation time, with EventID as a deterministic tie-break.
func lessThan(a, b *inboxEvent) bool {
	ar, br := priorityRank(a.Priority), priorityRank(b.Priority)
	if ar != br {
		return ar < br
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.EventID < b.EventID
}

// inboxManager is the persisted event queue. It is safe for concurrent use;
// production handlers run on a single actor mailbox anyway.
type inboxManager struct {
	mu      sync.Mutex
	events  []*inboxEvent
	counter uint64
}

func newInboxManager() *inboxManager {
	return &inboxManager{}
}

// enqueue validates and queues one event, or merges/refreshes an existing
// non-terminal event with the same dedupe key. An empty priority defaults to
// normal.
func (m *inboxManager) enqueue(now time.Time, req gen.GlassEventEnqueueReq) (gen.GlassEventEnqueueResp, error) {
	src := strings.TrimSpace(req.Source)
	kind := strings.TrimSpace(req.Kind)
	prio := strings.TrimSpace(req.Priority)
	payload := req.Payload
	if src == "" || kind == "" {
		return gen.GlassEventEnqueueResp{}, fmt.Errorf("source and kind are required")
	}
	if payload == "" {
		return gen.GlassEventEnqueueResp{}, fmt.Errorf("payload is required")
	}
	if prio == "" {
		prio = priorityNormal
	}
	if prio != priorityHigh && prio != priorityNormal && prio != priorityLow {
		return gen.GlassEventEnqueueResp{}, fmt.Errorf("priority %q is not one of high|normal|low", prio)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if key := strings.TrimSpace(req.DedupeKey); key != "" {
		for _, ev := range m.events {
			if ev.DedupeKey == key && !ev.State.terminal() {
				// Dedupe merge refresh: replace the payload and restart the
				// event's TTL window; in_flight stays in_flight.
				ev.Source = src
				ev.Kind = kind
				ev.Payload = payload
				ev.CreatedAt = now
				ev.TTLAt = now.Add(ttlForPriority(prio))
				ev.RetryAt = time.Time{}
				ev.Retries = 0
				ev.Outcome = ""
				return gen.GlassEventEnqueueResp{Accepted: true, EventID: ev.EventID}, nil
			}
		}
	}

	m.counter++
	ev := &inboxEvent{
		EventID:   fmt.Sprintf("ge_%d", m.counter),
		Source:    src,
		Kind:      kind,
		Priority:  prio,
		DedupeKey: strings.TrimSpace(req.DedupeKey),
		Payload:   payload,
		State:     statePending,
		CreatedAt: now,
		TTLAt:     now.Add(ttlForPriority(prio)),
	}
	m.events = append(m.events, ev)
	m.pruneLocked()
	return gen.GlassEventEnqueueResp{Accepted: true, EventID: ev.EventID}, nil
}

// pruneLocked bounds the retained history by dropping the oldest terminal
// events past inboxMaxEvents.
func (m *inboxManager) pruneLocked() {
	if len(m.events) <= inboxMaxEvents {
		return
	}
	// Keep all non-terminal events; drop oldest terminal ones.
	var active []*inboxEvent
	for _, ev := range m.events {
		if !ev.State.terminal() {
			active = append(active, ev)
		}
	}
	if len(active) <= inboxMaxEvents {
		kept := make([]*inboxEvent, 0, inboxMaxEvents)
		dropped := len(m.events) - inboxMaxEvents
		for _, ev := range m.events {
			if dropped > 0 && ev.State.terminal() {
				dropped--
				continue
			}
			kept = append(kept, ev)
		}
		m.events = kept
		return
	}
	m.events = active
}

// get returns a clone of the event by id, or nil.
func (m *inboxManager) get(eventID string) *inboxEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ev := range m.events {
		if ev.EventID == eventID {
			return ev.clone()
		}
	}
	return nil
}

// housekeep expires overdue events and returns in_flight events past the
// receipt window to pending (retries incremented). It returns the counts of
// each transition.
func (m *inboxManager) housekeep(now time.Time) (expired, requeued int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ev := range m.events {
		if !ev.State.terminal() && !ev.TTLAt.IsZero() && !ev.TTLAt.After(now) {
			ev.State = stateExpired
			ev.CompletedAt = now
			expired++
			continue
		}
		if ev.State == stateInflight && !ev.InFlightAt.IsZero() && now.Sub(ev.InFlightAt) >= inflightRetryWindow {
			ev.State = statePending
			ev.RetryAt = now
			ev.Retries++
			requeued++
		}
	}
	return expired, requeued
}

// next returns the highest-priority eligible pending event (priority then
// creation order) whose retry time has passed and whose TTL has not elapsed,
// or nil when none is eligible. It never yields while an event is in_flight:
// one delivery at a time, and high priority never preempts an in_flight event.
func (m *inboxManager) next(now time.Time) *inboxEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ev := range m.events {
		if ev.State == stateInflight {
			return nil
		}
	}
	var best *inboxEvent
	for _, ev := range m.events {
		if ev.State != statePending {
			continue
		}
		if !ev.TTLAt.IsZero() && !ev.TTLAt.After(now) {
			continue
		}
		if !ev.RetryAt.IsZero() && ev.RetryAt.After(now) {
			continue
		}
		if best == nil || lessThan(ev, best) {
			best = ev
		}
	}
	if best == nil {
		return nil
	}
	return best.clone()
}

// hasInflight reports whether any event is awaiting a Coordinator receipt.
// High priority never preempts an in_flight delivery.
func (m *inboxManager) hasInflight() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ev := range m.events {
		if ev.State == stateInflight {
			return true
		}
	}
	return false
}

// beginDelivery marks a pending event in_flight. It refuses events that are
// expired, not yet eligible, or not pending.
func (m *inboxManager) beginDelivery(eventID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ev := m.findLocked(eventID)
	if ev == nil {
		return fmt.Errorf("event %s not found", eventID)
	}
	if ev.State != statePending {
		return fmt.Errorf("event %s is %s, not pending", eventID, ev.State)
	}
	if !ev.TTLAt.IsZero() && !ev.TTLAt.After(now) {
		return fmt.Errorf("event %s expired", eventID)
	}
	if !ev.RetryAt.IsZero() && ev.RetryAt.After(now) {
		return fmt.Errorf("event %s not eligible until %s", eventID, formatTime(ev.RetryAt))
	}
	ev.State = stateInflight
	ev.InFlightAt = now
	return nil
}

// requeue returns a delivered event to pending after a failed delivery
// attempt. The event becomes eligible again after retryBackoff.
func (m *inboxManager) requeue(eventID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ev := m.findLocked(eventID)
	if ev == nil {
		return fmt.Errorf("event %s not found", eventID)
	}
	if ev.State != stateInflight {
		return fmt.Errorf("event %s is %s, not in_flight", eventID, ev.State)
	}
	ev.State = statePending
	ev.RetryAt = now.Add(retryBackoff)
	ev.Retries++
	return nil
}

// complete applies a Coordinator receipt. handled and ignored are terminal;
// defer and failed return the event to pending (defer within its TTL). Receipts
// are only accepted for in_flight events; terminal receipts are idempotent
// only for the matching outcome.
func (m *inboxManager) complete(eventID, outcome, reason string, now time.Time) (inboxState, error) {
	switch outcome {
	case outcomeHandled, outcomeIgnored, outcomeDefer, outcomeFailed:
	default:
		return "", fmt.Errorf("outcome %q is not one of handled|ignored|defer|failed", outcome)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ev := m.findLocked(eventID)
	if ev == nil {
		return "", fmt.Errorf("event %s not found", eventID)
	}
	if ev.State == stateCompleted && outcome == outcomeHandled {
		return ev.State, nil
	}
	if ev.State == stateIgnored && outcome == outcomeIgnored {
		return ev.State, nil
	}
	if ev.State != stateInflight {
		return "", fmt.Errorf("event %s is %s, not in_flight", eventID, ev.State)
	}
	ev.Outcome = outcome
	ev.CompletedAt = now
	switch outcome {
	case outcomeHandled:
		ev.State = stateCompleted
	case outcomeIgnored:
		ev.State = stateIgnored
	case outcomeDefer:
		retry := now.Add(deferWindow)
		if retry.After(ev.TTLAt) {
			retry = ev.TTLAt
		}
		ev.State = statePending
		ev.RetryAt = retry
	case outcomeFailed:
		retry := now.Add(retryBackoff)
		if retry.After(ev.TTLAt) {
			retry = ev.TTLAt
		}
		ev.State = statePending
		ev.RetryAt = retry
		ev.Retries++
	}
	return ev.State, nil
}

// clear drops the whole queue. It is called on formal offline and on new
// session replacement; the id counter is preserved so event ids stay unique.
func (m *inboxManager) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = nil
}

// snapshot builds the secret-free inbox projection ordered by scheduling
// priority (priority then creation), newest-last like the queue.
func (m *inboxManager) snapshot() gen.GlassInboxSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	events := make([]*inboxEvent, len(m.events))
	copy(events, m.events)
	sort.SliceStable(events, func(i, j int) bool { return lessThan(events[i], events[j]) })
	snap := gen.GlassInboxSnapshot{}
	for _, ev := range events {
		snap.Total++
		switch ev.State {
		case statePending:
			snap.Pending++
		case stateInflight:
			snap.InFlight++
		case stateCompleted:
			snap.Completed++
		case stateIgnored:
			snap.Ignored++
		case stateExpired:
			snap.Expired++
		}
		snap.Events = append(snap.Events, ev.toGen())
	}
	return snap
}

func (m *inboxManager) findLocked(eventID string) *inboxEvent {
	for _, ev := range m.events {
		if ev.EventID == eventID {
			return ev
		}
	}
	return nil
}

// inboxEventDurable is the persisted projection of one inbox event.
type inboxEventDurable struct {
	EventID     string    `json:"EventId"`
	Source      string    `json:"Source"`
	Kind        string    `json:"Kind"`
	Priority    string    `json:"Priority"`
	DedupeKey   string    `json:"DedupeKey,omitempty"`
	Payload     string    `json:"Payload"`
	State       string    `json:"State"`
	CreatedAt   time.Time `json:"CreatedAt"`
	TTLAt       time.Time `json:"TTLAt"`
	RetryAt     time.Time `json:"RetryAt,omitempty"`
	Retries     int       `json:"Retries"`
	InFlightAt  time.Time `json:"InFlightAt,omitempty"`
	CompletedAt time.Time `json:"CompletedAt,omitempty"`
	Outcome     string    `json:"Outcome,omitempty"`
}

// inboxDurable is the persisted shape of the inbox.
type inboxDurable struct {
	Events  []*inboxEventDurable `json:"Events,omitempty"`
	Counter uint64               `json:"Counter"`
}

func (m *inboxManager) export() inboxDurable {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := inboxDurable{Counter: m.counter}
	for _, ev := range m.events {
		d.Events = append(d.Events, &inboxEventDurable{
			EventID:     ev.EventID,
			Source:      ev.Source,
			Kind:        ev.Kind,
			Priority:    ev.Priority,
			DedupeKey:   ev.DedupeKey,
			Payload:     ev.Payload,
			State:       string(ev.State),
			CreatedAt:   ev.CreatedAt,
			TTLAt:       ev.TTLAt,
			RetryAt:     ev.RetryAt,
			Retries:     ev.Retries,
			InFlightAt:  ev.InFlightAt,
			CompletedAt: ev.CompletedAt,
			Outcome:     ev.Outcome,
		})
	}
	return d
}

func (m *inboxManager) importState(d inboxDurable) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter = d.Counter
	for _, e := range d.Events {
		if e == nil {
			continue
		}
		m.events = append(m.events, &inboxEvent{
			EventID:     e.EventID,
			Source:      e.Source,
			Kind:        e.Kind,
			Priority:    e.Priority,
			DedupeKey:   e.DedupeKey,
			Payload:     e.Payload,
			State:       inboxState(e.State),
			CreatedAt:   e.CreatedAt,
			TTLAt:       e.TTLAt,
			RetryAt:     e.RetryAt,
			Retries:     e.Retries,
			InFlightAt:  e.InFlightAt,
			CompletedAt: e.CompletedAt,
			Outcome:     e.Outcome,
		})
	}
}

// deliveryLogEntry is one bounded delivery attempt record.
type deliveryLogEntry struct {
	EventID   string
	Success   bool
	Detail    string
	Timestamp time.Time
}

// deliveryLog is a runtime-only bounded record of delivery attempts. It is
// intentionally not persisted: it is a debug surface, not the source of truth.
type deliveryLog struct {
	mu      sync.Mutex
	entries []deliveryLogEntry
}

const deliveryLogMax = 32

func newDeliveryLog() *deliveryLog {
	return &deliveryLog{}
}

func (l *deliveryLog) record(success bool, eventID, detail string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, deliveryLogEntry{
		EventID:   eventID,
		Success:   success,
		Detail:    detail,
		Timestamp: now,
	})
	if len(l.entries) > deliveryLogMax {
		l.entries = l.entries[len(l.entries)-deliveryLogMax:]
	}
}

func (l *deliveryLog) snapshot() []gen.GlassDeliveryLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]gen.GlassDeliveryLogEntry, 0, len(l.entries))
	for _, e := range l.entries {
		out = append(out, gen.GlassDeliveryLogEntry{
			EventID:   e.EventID,
			Success:   e.Success,
			Detail:    e.Detail,
			Timestamp: formatTime(e.Timestamp),
		})
	}
	return out
}

// hudManager owns the full HUD state and detects changes for incremental
// sync. The HUD bar is an independent channel from Coordinator render frames:
// Glass Interact composites connection, battery, and agent status here and
// emits glass.hud.update only when a field actually changes.
//
// Each mutator returns changed=true when the snapshot differs from the
// previous one; the caller then calls emitHudUpdate. This keeps emit logic
// out of the manager (which has no PureContext) while avoiding redundant
// events for no-op updates.
type hudManager struct {
	mu            sync.Mutex
	connection    string // "online" | "reconnecting" | "offline"
	batteryLevel  int    // 0-100; -1 = not reported
	charging      bool
	batterySet    bool // false until first telemetry
	agentRunning  bool
	agentName     string
	agentState    string
	agentOutcome  string // "completed" | "failed" | ""
}

func newHudManager() *hudManager {
	return &hudManager{
		connection:   "offline",
		batteryLevel: -1,
	}
}

const hudConnectionOnline = "online"
const hudConnectionOffline = "offline"

// setConnection updates the connection state. Returns changed.
func (h *hudManager) setConnection(conn string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connection == conn {
		return false
	}
	h.connection = conn
	return true
}

// setBattery updates battery telemetry. Returns changed.
func (h *hudManager) setBattery(level int, charging bool, reported bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !reported && !h.batterySet {
		return false
	}
	if reported && h.batterySet && h.batteryLevel == level && h.charging == charging {
		return false
	}
	h.batteryLevel = level
	h.charging = charging
	h.batterySet = reported
	return true
}

// setAgent updates agent status. Derives outcome from running→stopped
// transitions: running→error = "failed", running→idle = "completed".
// Returns changed.
func (h *hudManager) setAgent(status gen.AgentStatusResp) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	wasRunning := h.agentRunning
	newRunning := status.State == agentStateRunning

	outcome := h.agentOutcome
	if wasRunning && !newRunning {
		if status.State == "error" {
			outcome = "failed"
		} else {
			outcome = "completed"
		}
	}

	if h.agentRunning == newRunning &&
		h.agentName == status.DisplayName &&
		h.agentState == status.State &&
		h.agentOutcome == outcome {
		return false
	}

	h.agentRunning = newRunning
	h.agentName = status.DisplayName
	h.agentState = status.State
	h.agentOutcome = outcome
	return true
}

// snapshot assembles the full HUD projection for the current session state.
func (h *hudManager) snapshot() gen.GlassHudStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	hud := gen.GlassHudStatus{
		Connection:   h.connection,
		AgentRunning: h.agentRunning,
		AgentState:   h.agentState,
		AgentOutcome: h.agentOutcome,
	}
	if h.agentName != "" {
		hud.AgentName = h.agentName
	}
	if h.batterySet {
		batt := int32(h.batteryLevel)
		if batt < 0 {
			batt = 0
		}
		hud.BatteryLevel = batt
		hud.Charging = h.charging
	}
	return hud
}

// emitHudUpdate sends the current HUD snapshot as a glass.hud.update event.
// Called only when a hudManager mutator reports a change.
func (a *Actor) emitHudUpdate(ctx actor.PureContext) {
	if !ctx.HasEventSubscribers(eventHud) {
		return
	}
	hud := a.hud.snapshot()
	hud.Timestamp = formatTime(a.now())
	if err := ctx.EmitEvent(eventHud, gen.GlassHudStatus(hud)); err != nil {
		ctx.Logger().Error("glassinteract: emit hud update", "err", err)
	}
}

// agentStateRunning is the agent.status State value that means the Coordinator
// is busy executing a turn. Anything else (empty/idle/paused/failed) means the
// updater may deliver the next event.
const agentStateRunning = "running"

// coordinatorGate is the compact status protocol the updater checks before a
// delivery. It is derived from the Coordinator agent's existing agent.status
// surface: Running when the Coordinator is mid-turn, ActiveCall when a turn
// ref is live.
func (a *Actor) coordinatorGate(ctx actor.Context) (gen.GlassCoordinatorGate, bool) {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		return gen.GlassCoordinatorGate{}, false
	}
	planner := ctx.Planner()
	if planner == nil {
		return gen.GlassCoordinatorGate{}, false
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()

	result, err := planner.Call(callCtx, wsRef, "workspace.coordinator_lookup", nil).Await()
	if err != nil {
		ctx.Logger().Debug("glassinteract: coordinator lookup failed; skipping delivery", "err", err)
		return gen.GlassCoordinatorGate{}, false
	}
	lookup, ok := decodeCoordinatorLookup(result)
	if !ok || !lookup.Found || lookup.ActorID == "" {
		ctx.Logger().Debug("glassinteract: coordinator not found; skipping delivery")
		return gen.GlassCoordinatorGate{}, false
	}
	coordRef, ok := lookupCoordinatorRef(ctx, lookup.ActorID)
	if !ok {
		ctx.Logger().Debug("glassinteract: coordinator actor not resolvable; skipping delivery", "actorID", lookup.ActorID)
		return gen.GlassCoordinatorGate{}, false
	}
	result, err = planner.Call(callCtx, coordRef, "agent_status", nil).Await()
	if err != nil {
		ctx.Logger().Debug("glassinteract: coordinator agent.status failed; skipping delivery", "err", err)
		return gen.GlassCoordinatorGate{}, false
	}
	status, ok := decodeAgentStatus(result)
	if !ok {
		ctx.Logger().Debug("glassinteract: unexpected agent.status payload; skipping delivery", "type", fmt.Sprintf("%T", result))
		return gen.GlassCoordinatorGate{}, false
	}
	if a.hud.setAgent(status) {
		a.emitHudUpdate(ctx)
	}
	return gen.GlassCoordinatorGate{
		Running:    status.State == agentStateRunning,
		ActiveCall: status.ActiveTurnRef != "",
	}, true
}

// decodeAgentStatus decodes an agent.status result that may arrive as the
// typed struct, raw JSON bytes, or a generic map.
func decodeAgentStatus(result any) (gen.AgentStatusResp, bool) {
	switch v := result.(type) {
	case gen.AgentStatusResp:
		return v, true
	case []byte:
		var resp gen.AgentStatusResp
		if err := json.Unmarshal(v, &resp); err != nil {
			return gen.AgentStatusResp{}, false
		}
		return resp, true
	case map[string]interface{}:
		b, err := json.Marshal(v)
		if err != nil {
			return gen.AgentStatusResp{}, false
		}
		var resp gen.AgentStatusResp
		if err := json.Unmarshal(b, &resp); err != nil {
			return gen.AgentStatusResp{}, false
		}
		return resp, true
	default:
		return gen.AgentStatusResp{}, false
	}
}
