// Package glassinteract owns the process-unique active Glass session: session
// id/generation, capabilities, lastFrame and lifecycle. It is the sole
// authority for bootstrap tokens, session claim/replacement/reconnect/offline
// transitions, and persisted logical session state.
//
// Transport model: Glass reuses the existing /ws gateway. The client calls
// glass_interact.bootstrap with the pre-shared sporemind_key to obtain a
// short-lived glass JWT bound to a server-generated session_id; it then opens
// /ws?token=... (role=glass, subject=session_id) and calls
// glass_interact.session.claim to attach. The actor does NOT own raw
// WebSocket connections — connection liveness is inferred from claim/activity
// recency against the 30s reconnect grace window.
package glassinteract

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"context"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/auth"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

const (
	// ReconnectGrace is how long a logical Glass session survives without
	// activity before it is formally offline. A same-session claim inside the
	// window restores the logical session and its lastFrame; a claim after the
	// window starts a fresh logical session that inherits nothing.
	ReconnectGrace = 30 * time.Second

	// BootstrapKeyEnv is the environment variable holding the pre-shared
	// bootstrap key (the sporemind_key stored in MentraOS settings). It is
	// only used at bootstrap time; connections use the issued glass token.
	BootstrapKeyEnv = "SPOREMIND_GLASS_KEY"
)

const (
	callableBootstrap        = "glass_interact.bootstrap"
	callableClaim            = "glass_interact.session_claim"
	callableGetState         = "glass_interact.session_get_state"
	callableCheck            = "glass_interact.check_connection"
	callableSpeechStart      = "glass_interact.speech_start"
	callableSpeechChunk      = "glass_interact.speech_chunk"
	callableSpeechEnd        = "glass_interact.speech_end"
	callableTranscriptLatest = "glass_interact.transcript_latest"
	callableIdleRenderTick   = "glass_interact.idle_render_tick"
	callableAgentPollTick    = "glass_interact.agent_poll_tick"
	// callableAgentPollApply is the glass_poll-lane continuation of
	// agent_poll_tick: the background fetch dispatches its result back to
	// this callable so state mutation stays on the poll lane while the
	// workspace round-trip never holds the owner lane.
	callableAgentPollApply = "glass_interact.agent_poll_apply"
)

// Dedicated stateful lanes for the self-scheduling glass tick handlers. The
// owner lane must stay µs-fast for bootstrap / session claim / session reads,
// so the 5s agent-state poll chain and the session-gated Coordinator updater
// run on their own single-goroutine lanes (Owner Lane 禁阻塞). Each lane
// serialises the handlers routed to it, keeping its shared tables (agent
// monitor / display frame / inbox) single-writer within the lane while the
// lane's cross-actor Await never touches the owner lane.
const (
	loopGlassPoll = "glass_poll"
	// loopGlassUpdater serialises the coordinator round-trips: the session-
	// gated inbox delivery tick and the interaction-report reply delivery.
	loopGlassUpdater = "glass_updater"
	// loopGlassSTT carries the speech.end finalization: the coordinator
	// hotword peek plus the voice.recognize STT call are seconds-long
	// cross-actor awaits that must never occupy the owner lane.
	loopGlassSTT = "glass_stt"
)

// IdleRenderInterval is how often the idle frame is refreshed so the clock
// and HUD stay current. Kept long to avoid unnecessary BLE traffic.
const IdleRenderInterval = 60 * time.Second

// AgentPollInterval is how often the Glass actor refreshes workspace agent
// state for the idle HUD. Near-real-time without hammering the workspace.
const AgentPollInterval = 5 * time.Second

const (
	eventOnline      = "glass.online"
	eventOffline     = "glass.offline"
	eventReplaced    = "glass.replaced"
	eventReconnected = "glass.reconnected"
	eventTranscript  = "glass.transcript"
)

// frameMode values track the semantic purpose of the current displayFrame.
const (
	frameModeIdle         = "idle"
	frameModeNotification = "notification"
	frameModeAskUser      = "askuser"
	frameModeTranscript   = "transcript"
	frameModeOther        = "other"
)

// sessionStatus is the persisted lifecycle state of a logical session.
type sessionStatus string

const (
	statusIdle     sessionStatus = "idle"
	statusOnline   sessionStatus = "online"
	statusReplaced sessionStatus = "replaced"
	statusOffline  sessionStatus = "offline"
)

// session is the full logical session record. Only the fields carried by
// durableState are persisted; the struct itself is runtime state.
type session struct {
	SessionID    string
	DeviceID     string
	Generation   uint64
	Status       sessionStatus
	LastSeen     time.Time
	OfflineAt    time.Time
	LastFrame    gen.GlassRenderFrame
	HasLastFrame bool
	Capabilities []gen.GlassCapability
}

func (s *session) clone() *session {
	if s == nil {
		return nil
	}
	c := *s
	c.Capabilities = append([]gen.GlassCapability(nil), s.Capabilities...)
	return &c
}

// claimResult describes what a claim did to the session manager.
type claimResult struct {
	Fresh       bool   // a new logical session started (generation advanced)
	Reconnected bool   // the same session recovered (kept generation + lastFrame)
	Replaced    bool   // a different active session was replaced
	Rejected    bool   // the claim was refused (replaced session within token TTL)
	Reason      string // rejection reason
	Session     *session
	Previous    *session
}

// transition is a state change the caller should broadcast as a lifecycle
// event (currently only the timer-driven formal offline).
type transition struct {
	Kind    string
	Session *session
}

// sessionManager is the pure session state machine. It is safe for
// concurrent use; production handlers run on a single actor mailbox anyway.
type sessionManager struct {
	mu         sync.Mutex
	active     *session
	genCounter uint64
	// replaced remembers session ids that were superseded so a stale token
	// cannot ping-pong-replace the active session within its token lifetime.
	replaced map[string]time.Time
}

func newSessionManager() *sessionManager {
	return &sessionManager{replaced: make(map[string]time.Time)}
}

// claim applies a session claim at now.
//
//   - No active session → fresh online (generation advanced).
//   - Same session id, not formally terminated → reconnect/refresh; the
//     generation and lastFrame are preserved.
//   - Same session id, formally offline/replaced → fresh start, inheriting
//     nothing.
//   - Different session id → the active session is replaced and marked
//     rejected for its remaining token lifetime; the new session starts fresh.
func (m *sessionManager) claim(now time.Time, sessionID, deviceID string, caps []gen.GlassCapability) claimResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	if t, ok := m.replaced[sessionID]; ok && now.Before(t.Add(auth.GlassTokenTTL)) {
		return claimResult{Rejected: true, Reason: "session was replaced; re-bootstrap to start a new session"}
	}
	m.pruneReplaced(now)

	active := m.active
	if active == nil || active.SessionID == "" {
		return m.startFresh(now, sessionID, deviceID, caps)
	}

	if active.SessionID == sessionID {
		if active.Status == statusOffline || active.Status == statusReplaced {
			return m.startFresh(now, sessionID, deviceID, caps)
		}
		// Every claim on an already-live session means a new connection, so it
		// is a reconnect from the actor's perspective.
		active.Status = statusOnline
		active.LastSeen = now
		active.DeviceID = deviceID
		active.Capabilities = caps
		return claimResult{Reconnected: true, Session: active.clone()}
	}

	// Different session id: the active session is replaced.
	old := active.clone()
	old.Status = statusReplaced
	old.OfflineAt = now
	m.replaced[old.SessionID] = now
	res := m.startFresh(now, sessionID, deviceID, caps)
	res.Replaced = true
	res.Previous = old
	return res
}

func (m *sessionManager) startFresh(now time.Time, sessionID, deviceID string, caps []gen.GlassCapability) claimResult {
	prev := m.active
	m.genCounter++
	s := &session{
		SessionID:    sessionID,
		DeviceID:     deviceID,
		Generation:   m.genCounter,
		Status:       statusOnline,
		LastSeen:     now,
		Capabilities: caps,
	}
	m.active = s
	return claimResult{Fresh: true, Session: s.clone(), Previous: prev.clone()}
}

// check formally offline a session that has been idle past ReconnectGrace.
// Returns nil when no transition occurred.
func (m *sessionManager) check(now time.Time) *transition {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.active
	if a == nil {
		return nil
	}
	if a.Status == statusOnline && now.Sub(a.LastSeen) >= ReconnectGrace {
		a.Status = statusOffline
		a.OfflineAt = now
		return &transition{Kind: eventOffline, Session: a.clone()}
	}
	return nil
}

// nextDeadline returns when the active online session must next be checked
// for formal offline, or the zero time when no session is active.
func (m *sessionManager) nextDeadline() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.active
	if a == nil || a.Status != statusOnline {
		return time.Time{}
	}
	return a.LastSeen.Add(ReconnectGrace)
}

// activeIdentity returns the active session id, its generation, and whether it
// is formally online. It backs frame authorization for the speech protocol.
func (m *sessionManager) activeIdentity() (string, uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.active
	if a == nil {
		return "", 0, false
	}
	return a.SessionID, a.Generation, a.Status == statusOnline
}

// deviceID returns the active session's device id (empty when none).
func (m *sessionManager) deviceID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return ""
	}
	return m.active.DeviceID
}

func (m *sessionManager) snapshot() (bool, gen.GlassSessionState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.active
	if a == nil {
		return false, gen.GlassSessionState{}
	}
	return true, gen.GlassSessionState{
		SessionID:    a.SessionID,
		DeviceID:     a.DeviceID,
		Generation:   int64(a.Generation),
		Online:       a.Status == statusOnline,
		LastSeen:     formatTime(a.LastSeen),
		OfflineAt:    formatTime(a.OfflineAt),
		LastFrame:    framePtr(a.LastFrame),
		Capabilities: a.Capabilities,
	}
}

func (m *sessionManager) pruneReplaced(now time.Time) {
	for id, t := range m.replaced {
		if now.Sub(t) >= auth.GlassTokenTTL {
			delete(m.replaced, id)
		}
	}
}

// export returns the persistable view of the manager.
func (m *sessionManager) export() (active *session, genCounter uint64, replaced map[string]time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rep := make(map[string]time.Time, len(m.replaced))
	for id, t := range m.replaced {
		rep[id] = t
	}
	return m.active.clone(), m.genCounter, rep
}

// importState restores the manager from persisted data. A session that was
// online at shutdown is restored as online with LastSeen=now so a same-session
// reconnect inside a fresh grace window restores lastFrame.
func (m *sessionManager) importState(now time.Time, d durableState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.genCounter = d.GenCounter
	for id, t := range d.Replaced {
		m.replaced[id] = t
	}
	if d.Active != nil && d.Active.SessionID != "" && d.Active.Status == string(statusOnline) {
		m.active = &session{
			SessionID:    d.Active.SessionID,
			DeviceID:     d.Active.DeviceID,
			Generation:   d.Active.Generation,
			Status:       statusOnline,
			LastSeen:     now,
			LastFrame:    d.Active.LastFrame,
			HasLastFrame: d.Active.HasLastFrame,
			Capabilities: d.Active.Capabilities,
		}
	}
}

// sessionDurable is the persisted projection of one logical session.
type sessionDurable struct {
	SessionID    string                `json:"SessionId"`
	DeviceID     string                `json:"DeviceId"`
	Generation   uint64                `json:"Generation"`
	Status       string                `json:"Status"`
	LastSeen     time.Time             `json:"LastSeen"`
	OfflineAt    time.Time             `json:"OfflineAt,omitempty"`
	LastFrame    gen.GlassRenderFrame  `json:"LastFrame"`
	HasLastFrame bool                  `json:"HasLastFrame"`
	Capabilities []gen.GlassCapability `json:"Capabilities,omitempty"`
}

// durableState is the persisted shape of the session manager. Live WebSocket
// connections are runtime-only and are never serialized.
type durableState struct {
	Active     *sessionDurable      `json:"Active,omitempty"`
	GenCounter uint64               `json:"GenCounter"`
	Replaced   map[string]time.Time `json:"Replaced,omitempty"`
	Inbox      *inboxDurable        `json:"Inbox,omitempty"`
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// framePtr converts a value frame to the generated pointer field.
func framePtr(f gen.GlassRenderFrame) *gen.GlassRenderFrame {
	return &f
}

// Actor manages the process-unique Glass session.
type Actor struct {
	actor.Host
	store persist.Persist
	// stateLoaded is set once Load() has completed (first start included);
	// OnStop skips the save before that (gospore ForceCleanup may stop us
	// mid-OnInit, and saving zero-value durable state would wipe the record).
	stateLoaded atomic.Bool
	actorID     string
	key         string
	jwt         *auth.Manager
	now         func() time.Time
	sess        *sessionManager
	speech      *speechManager
	speak       *speakManager
	debug       *debugManager
	inbox       *inboxManager
	deliveryLog *deliveryLog
	hud         *hudManager
	agentMon    *agentMonitor
	// displayFrame is the authoritative "what's on screen now" state, held
	// from actor start regardless of whether a glass is connected. It is
	// initialized to IdleFrame and updated by every Coordinator render. Both
	// handleDebugState (pure) and handleRender (pure) access it, so displayMu
	// guards it.
	displayMu    sync.RWMutex
	displayFrame gen.GlassRenderFrame
	// displayMode records the semantic mode of displayFrame. It is used by
	// the idle render tick to decide whether it is safe to overwrite the
	// current frame with a fresh clock (idle mode only).
	displayMode string
	// saveMu serializes Save() calls across concurrent stateless handlers.
	// Without it, two pure goroutines calling Save() simultaneously race
	// on the same state.json.tmp file and can corrupt durable state.
	saveMu sync.Mutex
	// updaterArmed guards the single outstanding periodic updater tick.
	updaterArmed atomic.Bool
}

// Type returns the actor type identifier.
func (a *Actor) Type() string { return "glassinteract" }

// getDisplayFrame returns the current display frame under the read lock.
func (a *Actor) getDisplayFrame() gen.GlassRenderFrame {
	a.displayMu.RLock()
	defer a.displayMu.RUnlock()
	return a.displayFrame
}

// getDisplayMode returns the current display mode under the read lock.
func (a *Actor) getDisplayMode() string {
	a.displayMu.RLock()
	defer a.displayMu.RUnlock()
	return a.displayMode
}

// setDisplayFrame replaces the display frame and mode under the write lock.
func (a *Actor) setDisplayFrame(f gen.GlassRenderFrame, mode string) {
	a.displayMu.Lock()
	defer a.displayMu.Unlock()
	a.displayFrame = f
	a.displayMode = mode
}

// OnInit initializes persistence, credentials, and restores durable state.
func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("glassinteract"))
		if err != nil {
			return err
		}
	}
	if a.jwt == nil {
		a.jwt = auth.NewManager(auth.DefaultJWTConfig())
	}
	if a.key == "" {
		a.key = os.Getenv(BootstrapKeyEnv)
	}
	if a.now == nil {
		a.now = time.Now
	}
	a.actorID = ctx.Self().ID().String()
	a.sess = newSessionManager()
	a.speech = newSpeechManager()
	a.speak = newSpeakManager()
	a.debug = newDebugManager()
	a.inbox = newInboxManager()
	a.deliveryLog = newDeliveryLog()
	a.hud = newHudManager()
	a.agentMon = newAgentMonitor()
	a.displayFrame = IdleFrame(a.now(), a.agentMon, a.hud.batteryBar(), a.hud.connectionGlyph())
	a.displayMode = frameModeIdle
	if err := a.Load(); err != nil {
		ctx.Logger().Error("glassinteract: load durable state failed", "err", err)
	}
	return nil
}

// OnStart registers callables and lifecycle event kinds.
func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("glassinteract: starting", "id", a.actorID)

	// Dedicated lanes first: Register validates that every WithLoop target is
	// already declared. glass_poll carries the self-scheduling agent-state
	// poll chain (tick + apply); glass_updater carries the session-gated
	// Coordinator updater tick. Both are stateful single-goroutine lanes, so
	// the owner lane is never held by their cross-actor Await.
	if err := ctx.RegisterLoop(loopGlassPoll, actor.ModeStateful); err != nil {
		return fmt.Errorf("glassinteract: register loop %s: %w", loopGlassPoll, err)
	}
	if err := ctx.RegisterLoop(loopGlassUpdater, actor.ModeStateful); err != nil {
		return fmt.Errorf("glassinteract: register loop %s: %w", loopGlassUpdater, err)
	}
	if err := ctx.RegisterLoop(loopGlassSTT, actor.ModeStateful); err != nil {
		return fmt.Errorf("glassinteract: register loop %s: %w", loopGlassSTT, err)
	}

	if err := ctx.Register(callableBootstrap, a.handleBootstrap, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableBootstrap, err)
	}
	if err := ctx.Register(callableClaim, a.handleClaim, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableClaim, err)
	}
	if err := ctx.Register(callableGetState, a.handleGetState, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableGetState, err)
	}
	if err := ctx.Register(callableCheck, a.handleCheck, actor.Internal()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableCheck, err)
	}
	if err := ctx.Register(callableIdleRenderTick, a.handleIdleRenderTick, actor.Internal()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableIdleRenderTick, err)
	}
	if err := ctx.Register(callableAgentPollTick, a.handleAgentPollTick, actor.Internal(), actor.WithLoop(loopGlassPoll)); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableAgentPollTick, err)
	}
	if err := ctx.Register(callableAgentPollApply, a.handleAgentPollApply, actor.Internal(), actor.WithLoop(loopGlassPoll)); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableAgentPollApply, err)
	}
	if err := ctx.Register(callableSpeechStart, a.handleSpeechStart, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableSpeechStart, err)
	}
	if err := ctx.Register(callableSpeechChunk, a.handleSpeechChunk, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableSpeechChunk, err)
	}
	if err := ctx.Register(callableSpeechEnd, a.handleSpeechEnd, actor.Public(), actor.WithLoop(loopGlassSTT)); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableSpeechEnd, err)
	}
	if err := ctx.Register(callableTranscriptLatest, a.handleTranscriptLatest, actor.Internal()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableTranscriptLatest, err)
	}
	if err := ctx.Register(callableRender, a.handleRender, actor.Internal()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableRender, err)
	}
	if err := ctx.Register(callableSpeak, a.handleSpeak, actor.Internal()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableSpeak, err)
	}
	if err := ctx.Register(callableCapabilities, a.handleCapabilities, actor.Internal()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableCapabilities, err)
	}
	if err := ctx.Register(callableDebugState, a.handleDebugState, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableDebugState, err)
	}
	if err := ctx.Register(callableDebugSimulate, a.handleDebugSimulate, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableDebugSimulate, err)
	}
	if err := ctx.Register(callableInteractionReport, a.handleInteractionReport, actor.Public(), actor.WithLoop(loopGlassUpdater)); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableInteractionReport, err)
	}
	if err := ctx.Register(callableEventEnqueue, a.handleEventEnqueue, actor.Internal()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableEventEnqueue, err)
	}
	if err := ctx.Register(callableEventComplete, a.handleEventComplete, actor.Internal()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableEventComplete, err)
	}
	if err := ctx.Register(callableInboxState, a.handleInboxState, actor.Internal()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableInboxState, err)
	}
	if err := ctx.Register(callableUpdaterTick, a.handleUpdaterTick, actor.Internal(), actor.WithLoop(loopGlassUpdater)); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableUpdaterTick, err)
	}
	if err := ctx.Register(callableTelemetry, a.handleTelemetryReport, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register %s: %w", callableTelemetry, err)
	}

	for _, kind := range []string{eventOnline, eventOffline, eventReplaced, eventReconnected} {
		if err := ctx.RegisterEventKind(kind, gen.GlassLifecycleEvent{}, actor.Public()); err != nil {
			return fmt.Errorf("glassinteract: register event kind %s: %w", kind, err)
		}
	}
	if err := ctx.RegisterEventKind(eventTranscript, gen.GlassTranscriptEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register event kind %s: %w", eventTranscript, err)
	}
	if err := ctx.RegisterEventKind(eventRender, gen.GlassRenderEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register event kind %s: %w", eventRender, err)
	}
	if err := ctx.RegisterEventKind(eventSpeak, gen.GlassSpeakEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register event kind %s: %w", eventSpeak, err)
	}
	if err := ctx.RegisterEventKind(eventInteraction, gen.GlassInteractionEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register event kind %s: %w", eventInteraction, err)
	}
	if err := ctx.RegisterEventKind(eventHud, gen.GlassHudStatus{}, actor.Public()); err != nil {
		return fmt.Errorf("glassinteract: register event kind %s: %w", eventHud, err)
	}
	for kind, payload := range map[string]any{
		eventEnqueued:  gen.GlassEventEnqueuedEvent{},
		eventDelivered: gen.GlassEventDeliveredEvent{},
		eventCompleted: gen.GlassEventCompletedEvent{},
	} {
		if err := ctx.RegisterEventKind(kind, payload, actor.Public()); err != nil {
			return fmt.Errorf("glassinteract: register event kind %s: %w", kind, err)
		}
	}

	if err := ctx.RegisterDomain("glass_interact").Expose(); err != nil {
		return fmt.Errorf("glassinteract: expose service: %w", err)
	}

	a.scheduleCheck(ctx)
	a.scheduleUpdater(ctx)
	a.scheduleIdleRenderTick(ctx)
	a.scheduleAgentPollTick(ctx)
	return nil
}

// OnStop persists durable state.
func (a *Actor) OnStop(ctx actor.Context) error {
	if a.stateLoaded.Load() {
		if err := a.Save(); err != nil {
			ctx.Logger().Error("glassinteract: save durable state on stop", "err", err)
		}
	}
	// Stopped mid-OnInit (gospore ForceCleanup): saving zero-value durable
	// state would wipe the record — skip.
	return nil
}

// Save implements persist.Persistent.
func (a *Actor) Save() error {
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	active, genCounter, replaced := a.sess.export()
	d := durableState{GenCounter: genCounter, Replaced: replaced}
	if active != nil {
		d.Active = &sessionDurable{
			SessionID:    active.SessionID,
			DeviceID:     active.DeviceID,
			Generation:   active.Generation,
			Status:       string(active.Status),
			LastSeen:     active.LastSeen,
			OfflineAt:    active.OfflineAt,
			LastFrame:    active.LastFrame,
			HasLastFrame: active.HasLastFrame,
			Capabilities: active.Capabilities,
		}
	}
	inbox := a.inbox.export()
	d.Inbox = &inbox
	return a.store.Save(a.actorID, d)
}

// Load implements persist.Persistent.
func (a *Actor) Load() (err error) {
	// Mark state as load-complete only after a successful pass (first start
	// included) — the OnStop wipe guard depends on it.
	defer func() {
		if err == nil {
			a.stateLoaded.Store(true)
		}
	}()
	var d durableState
	if err := persist.LoadOrZero(a.store, a.actorID, &d); err != nil {
		return err
	}
	a.sess.importState(a.now(), d)
	if d.Inbox != nil {
		a.inbox.importState(*d.Inbox)
	}
	return nil
}

// scheduleCheck arms the formal-offline timer for the next activity deadline.
func (a *Actor) scheduleCheck(ctx actor.PureContext) {
	deadline := a.sess.nextDeadline()
	if deadline.IsZero() {
		return
	}
	d := deadline.Sub(a.now())
	if d < 0 {
		d = 0
	}
	if err := ctx.After(d, callableCheck, nil); err != nil {
		ctx.Logger().Error("glassinteract: schedule connection check failed", "err", err)
	}
}

// scheduleIdleRenderTick arms the next idle-frame refresh tick. The tick only
// re-renders when the current mode is idle, so notifications/askuser/transcript
// frames are never overwritten.
func (a *Actor) scheduleIdleRenderTick(ctx actor.PureContext) {
	if err := ctx.After(IdleRenderInterval, callableIdleRenderTick, nil); err != nil {
		ctx.Logger().Error("glassinteract: schedule idle render tick failed", "err", err)
	}
}

// handleIdleRenderTick re-renders the idle frame if the display is still in
// idle mode, then reschedules itself.
func (a *Actor) handleIdleRenderTick(ctx actor.PureContext) error {
	if a.getDisplayMode() == frameModeIdle {
		now := a.now()
		frame := IdleFrame(now, a.agentMon, a.hud.batteryBar(), a.hud.connectionGlyph())
		a.setDisplayFrame(frame, frameModeIdle)
		a.debug.record(debugKindRender, renderDetail(frame), now)
		if emitErr := ctx.EmitEvent(eventRender, gen.GlassRenderEvent{
			SessionID:  "",
			Generation: 0,
			Frame:      frame,
			Timestamp:  formatTime(now),
		}); emitErr != nil {
			ctx.Logger().Error("glassinteract: emit idle render event", "err", emitErr)
		}
	}
	a.scheduleIdleRenderTick(ctx)
	return nil
}

// scheduleAgentPollTick arms the next workspace agent-state refresh. The
// delayed self-call inherits the handler's glass_poll lane route, so the tick
// chain never re-enters the owner lane.
func (a *Actor) scheduleAgentPollTick(ctx actor.PureContext) {
	if err := ctx.After(AgentPollInterval, callableAgentPollTick, nil); err != nil {
		ctx.Logger().Error("glassinteract: schedule agent poll tick failed", "err", err)
	}
}

// handleAgentPollTick refreshes the workspace agent list for the idle HUD. It
// runs on the dedicated glass_poll lane, so its cross-actor Await never holds
// the owner lane. The handler re-arms the next tick and dispatches the
// workspace fetch onto a background goroutine; the fetch result comes back to
// the same lane as a fire-and-forget self-invoke (the scheduler.handleFire
// pattern: send the message, never wait for it).
func (a *Actor) handleAgentPollTick(ctx actor.Context) error {
	// Self-reschedule first so the timer chain survives any fetch failure.
	a.scheduleAgentPollTick(ctx)

	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		return nil
	}
	planner := ctx.Planner()
	if planner == nil {
		return nil
	}
	lifecycle := ctx.Lifecycle()
	self := ctx.Self()
	logger := ctx.Logger()
	panicprobe.SafeGo(ctx, "glass_agent_poll_fetch", func() {
		callCtx, cancel := context.WithTimeout(lifecycle, domain.DefaultInvokeTimeout)
		result, err := planner.Call(callCtx, wsRef, "workspace.agent_list_state", nil).Await()
		cancel()
		if err != nil {
			logger.Warn("glassinteract: agent list poll failed", "err", err)
			return
		}
		state, ok := result.(gen.WorkspaceAgentListState)
		if !ok {
			logger.Warn("glassinteract: agent list poll returned unexpected type", "type", typeName(result))
			return
		}
		if self == nil {
			return
		}
		if call := self.Invoke(lifecycle, callableAgentPollApply, state); call != nil {
			_ = call.Close()
		}
	})
	return nil
}

// handleAgentPollApply is the glass_poll-lane continuation of agent_poll_tick:
// it applies the workspace agent state fetched by the background goroutine,
// updates the monitor, and re-renders the idle frame if the display is still
// idle so HUD contents stay near-real-time. The monitor, display frame, and
// debug timeline it touches are all mutex-guarded, so this lane can run
// concurrently with the owner lane without a data race.
func (a *Actor) handleAgentPollApply(ctx actor.Context, state gen.WorkspaceAgentListState) error {
	now := a.now()
	a.agentMon.update(state, now)

	if a.getDisplayMode() == frameModeIdle {
		frame := IdleFrame(now, a.agentMon, a.hud.batteryBar(), a.hud.connectionGlyph())
		a.setDisplayFrame(frame, frameModeIdle)
		a.debug.record(debugKindRender, renderDetail(frame), now)
		if emitErr := ctx.EmitEvent(eventRender, gen.GlassRenderEvent{
			SessionID:  "",
			Generation: 0,
			Frame:      frame,
			Timestamp:  formatTime(now),
		}); emitErr != nil {
			ctx.Logger().Error("glassinteract: emit idle render event", "err", emitErr)
		}
	}
	return nil
}

func typeName(v any) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T", v)
}

func (a *Actor) emitLifecycle(ctx actor.PureContext, kind string, ev gen.GlassLifecycleEvent) {
	if err := ctx.EmitEvent(kind, ev); err != nil {
		ctx.Logger().Error("glassinteract: emit lifecycle event", "kind", kind, "err", err)
	}
}

func lifecycleEvent(kind string, s *session, reason string, now time.Time) gen.GlassLifecycleEvent {
	return gen.GlassLifecycleEvent{
		Kind:       kind,
		SessionID:  s.SessionID,
		DeviceID:   s.DeviceID,
		Generation: int64(s.Generation),
		Reason:     reason,
		Timestamp:  formatTime(now),
	}
}

// handleBootstrap exchanges credentials for a short-lived glass token bound to
// a server-generated session_id. Two auth modes (admin credentials take
// precedence when both are supplied):
//   - Username + Password: verified via RPC to user.auth_login; the account
//     must have the admin role.
//   - Key: compared against SPOREMIND_GLASS_KEY (headless/automated provisioning).
//
// This is a stateless (PureContext) handler: the user.auth_login RPC (up to a
// 10s timeout) runs on a forked goroutine with no mailbox serialization, so
// bootstrap never holds the glassinteract owner lane (OwnerLane blocking
// audit 2026-08-27 P1). The handler reads no mutable actor state — the
// bootstrap key and JWT manager are immutable after OnInit.
func (a *Actor) handleBootstrap(ctx actor.PureContext, req gen.GlassBootstrapReq) (gen.GlassBootstrapResp, error) {
	if req.Username != "" || req.Password != "" {
		if err := a.verifyAdminCredentials(ctx, req.Username, req.Password); err != nil {
			return gen.GlassBootstrapResp{}, fmt.Errorf("%s: %w", callableBootstrap, err)
		}
	} else {
		if a.key == "" {
			return gen.GlassBootstrapResp{}, fmt.Errorf("%s: no credentials provided and no bootstrap key configured (set %s or supply Username/Password)", callableBootstrap, BootstrapKeyEnv)
		}
		if !secureCompare(req.Key, a.key) {
			return gen.GlassBootstrapResp{}, fmt.Errorf("%s: invalid key", callableBootstrap)
		}
	}
	sessionID := newSessionID()
	deviceID := req.DeviceID
	if deviceID == "" {
		deviceID = "unknown"
	}
	pair, err := a.jwt.IssueGlass(sessionID, deviceID, auth.GlassTokenTTL)
	if err != nil {
		return gen.GlassBootstrapResp{}, fmt.Errorf("%s: issue token: %w", callableBootstrap, err)
	}
	return gen.GlassBootstrapResp{
		SessionID: sessionID,
		DeviceID:  deviceID,
		Token:     pair.Token,
		ExpiresAt: pair.ExpiresAt.UTC().Format(time.RFC3339),
		Kind:      auth.GlassScope,
		Scope:     auth.GlassScope,
	}, nil
}

// verifyAdminCredentials RPCs to user.auth_login and checks that the account
// has the admin role. This reuses the existing bcrypt verification without
// duplicating password logic in the glass actor.
func (a *Actor) verifyAdminCredentials(ctx actor.PureContext, username, password string) error {
	userRef, ok := ctx.LookupService("user")
	if !ok {
		return fmt.Errorf("user service unavailable")
	}
	planner := ctx.Planner()
	if planner == nil {
		return fmt.Errorf("planner unavailable")
	}
	loginReq := gen.AuthLoginReq{Username: username, Password: password}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 10*time.Second)
	defer cancel()
	result, err := planner.Call(callCtx, userRef, "user.auth_login", loginReq).Await()
	if err != nil {
		return fmt.Errorf("auth failed: %w", err)
	}
	loginResp, ok := result.(gen.AuthLoginResp)
	if !ok {
		return fmt.Errorf("auth: unexpected response type")
	}
	isAdmin := false
	for _, role := range loginResp.Account.Roles {
		if role == "admin" {
			isAdmin = true
			break
		}
	}
	if !isAdmin {
		return fmt.Errorf("admin role required")
	}
	return nil
}

// handleClaim attaches/refreshes the process-unique Glass session. The caller
// must be a glass-authenticated /ws connection whose token subject matches the
// claimed session id.
func (a *Actor) handleClaim(ctx actor.Context, req gen.GlassSessionClaimReq) (gen.GlassSessionClaimResp, error) {
	ident := ctx.Identity()
	if ident.Role != "glass" {
		return gen.GlassSessionClaimResp{}, fmt.Errorf("%s: requires a glass-authenticated /ws session (role=glass)", callableClaim)
	}
	if ident.Subject != req.SessionID {
		return gen.GlassSessionClaimResp{}, fmt.Errorf("%s: token subject does not match session id", callableClaim)
	}

	now := a.now()
	res := a.sess.claim(now, req.SessionID, req.DeviceID, req.Capabilities)
	if res.Rejected {
		return gen.GlassSessionClaimResp{}, fmt.Errorf("%s: %s", callableClaim, res.Reason)
	}

	if res.Replaced && res.Previous != nil {
		a.emitLifecycle(ctx, eventReplaced, lifecycleEvent("replaced", res.Previous, "replaced by new session", now))
		// A new session replacement clears the event inbox: the previous
		// session's queue belongs to a different device context.
		a.clearInbox(ctx, "session replaced")
	}
	if res.Fresh {
		a.emitLifecycle(ctx, eventOnline, lifecycleEvent("online", res.Session, "", now))
	}
	if res.Reconnected {
		a.emitLifecycle(ctx, eventReconnected, lifecycleEvent("reconnected", res.Session, "", now))
	}

	// Connection is now online — sync HUD baseline so the client's HUD bar
	// reflects current state immediately.
	if a.hud.setConnection(hudConnectionOnline) {
		a.emitHudUpdate(ctx)
	}

	// For a fresh session with no persisted frame, seed it from the current
	// display frame so the client sees the idle (or last-rendered) screen
	// immediately instead of a blank.
	if res.Fresh && !res.Session.HasLastFrame {
		display := a.getDisplayFrame()
		if a.getDisplayMode() == frameModeIdle {
			// Make sure the seeded idle frame uses the current time/agent state.
			display = IdleFrame(a.now(), a.agentMon, a.hud.batteryBar(), a.hud.connectionGlyph())
			a.setDisplayFrame(display, frameModeIdle)
		}
		a.sess.setFrame(display)
		res.Session.LastFrame = display
		res.Session.HasLastFrame = true
	}

	if err := a.Save(); err != nil {
		ctx.Logger().Error("glassinteract: persist session state", "err", err)
	}
	a.scheduleCheck(ctx)
	// The session is online: arm the periodic updater and kick an immediate
	// check on the glass_updater lane so queued events are delivered without
	// waiting for the first tick — without holding the owner lane on the
	// coordinator round-trip.
	a.scheduleUpdater(ctx)
	a.kickUpdater(ctx)

	return gen.GlassSessionClaimResp{
		SessionID:   res.Session.SessionID,
		DeviceID:    res.Session.DeviceID,
		Generation:  int64(res.Session.Generation),
		Online:      true,
		Reconnected: res.Reconnected,
		Replaced:    res.Replaced,
		LastFrame:   framePtr(res.Session.LastFrame),
	}, nil
}

// handleGetState returns the current logical session projection. Glass-only:
// the projection is device-facing (session id/generation/lastFrame) and is not
// part of the developer debug surface (debug_state covers that).
func (a *Actor) handleGetState(ctx actor.PureContext, _ gen.GlassGetStateReq) (gen.GlassGetStateResp, error) {
	if ctx.Identity().Role != "glass" {
		return gen.GlassGetStateResp{}, fmt.Errorf("%s: requires a glass-authenticated /ws session (role=glass)", callableGetState)
	}
	active, st := a.sess.snapshot()
	return gen.GlassGetStateResp{Active: active, Session: &st}, nil
}

// handleCheck is the timer-driven formal-offline evaluator.
func (a *Actor) handleCheck(ctx actor.PureContext) error {
	tr := a.sess.check(a.now())
	if tr != nil {
		a.emitLifecycle(ctx, tr.Kind, lifecycleEvent("offline", tr.Session, "reconnect grace elapsed", a.now()))
		// Formal offline clears the persisted event inbox; a 30s network blip
		// inside the grace window keeps it intact.
		a.clearInbox(ctx, "session offline")
		// HUD connection goes offline too.
		if a.hud.setConnection(hudConnectionOffline) {
			a.emitHudUpdate(ctx)
		}
		if err := a.Save(); err != nil {
			ctx.Logger().Error("glassinteract: persist session state", "err", err)
		}
		return nil
	}
	a.scheduleCheck(ctx)
	return nil
}

func newSessionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "gs_" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("gs_%d", time.Now().UnixNano())
}

// secureCompare compares two strings in constant time.
func secureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
