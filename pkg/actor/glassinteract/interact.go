// Stage 4: Coordinator-internal render/speak, capabilities projection and the
// secret-free debug surface.
//
// The Coordinator (process-unique system manager agent) is the only semantic
// driver of Glass output. render overwrites the active session's lastFrame
// (latest-only, persisted as lastFrame only); speak is ordered at-most-once
// via runtime-only message-id duplicate suppression and is never replayed
// after a reconnect (nothing is queued or persisted). Capabilities come from
// the session claim/snapshots. The debug state is a bounded, secret-free
// projection safe for the existing authenticated /ws subscription surface.
package glassinteract

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/policy"
)

const (
	callableRender            = "glass_interact.render"
	callableSpeak             = "glass_interact.speak"
	callableCapabilities      = "glass_interact.capabilities_get"
	callableDebugState        = "glass_interact.debug_state"
	callableInteractionReport = "glass_interact.interaction_report"
	callableTelemetry         = "glass_interact.telemetry_report"
)

const (
	eventRender      = "glass.render"
	eventSpeak       = "glass.speak"
	eventInteraction = "glass.interaction"
	eventHud         = "glass.hud.update"
)

// speakManager owns the runtime-only speak duplicate suppression set. It is
// intentionally not persisted: after a process restart every message id is
// considered fresh, and nothing here is ever replayed.
type speakManager struct {
	mu    sync.Mutex
	seen  map[string]time.Time // message id -> first seen
	order []string             // insertion order for bounded eviction
}

const (
	// speakDedupMax bounds the in-memory duplicate set.
	speakDedupMax = 256
	// speakDedupWindow is how long a message id stays suppressed.
	speakDedupWindow = 5 * time.Minute
)

func newSpeakManager() *speakManager {
	return &speakManager{seen: make(map[string]time.Time)}
}

// suppress reports whether messageID is already known (duplicate). Fresh ids
// are recorded and bounded. Empty ids are never recorded so the handler can
// reject them explicitly.
func (m *speakManager) suppress(now time.Time, messageID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune(now)
	if _, ok := m.seen[messageID]; ok {
		return true
	}
	m.seen[messageID] = now
	m.order = append(m.order, messageID)
	if len(m.order) > speakDedupMax {
		delete(m.seen, m.order[0])
		m.order = m.order[1:]
	}
	return false
}

func (m *speakManager) prune(now time.Time) {
	cutoff := now.Add(-speakDedupWindow)
	for id, t := range m.seen {
		if t.Before(cutoff) {
			delete(m.seen, id)
		}
	}
	kept := m.order[:0]
	for _, id := range m.order {
		if _, ok := m.seen[id]; ok {
			kept = append(kept, id)
		}
	}
	m.order = kept
}

// debugManager is the secret-free runtime observability projection: bounded
// aggregate stats and a bounded timeline. Nothing is persisted.
type debugManager struct {
	mu       sync.Mutex
	stats    gen.GlassDebugStats
	timeline []gen.GlassDebugTimelineEntry
}

const debugTimelineMax = 32

func newDebugManager() *debugManager {
	return &debugManager{}
}

// Timeline kind values surfaced to the debug UI.
const (
	debugKindRender     = "render"
	debugKindSpeak      = "speak"
	debugKindSpeakDup   = "speak_deduped"
	debugKindUtterance  = "utterance"
	debugKindTranscript = "transcript"
	debugKindError      = "error"
)

func (m *debugManager) record(kind, detail string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch kind {
	case debugKindRender:
		m.stats.Renders++
	case debugKindSpeak:
		m.stats.Speaks++
	case debugKindSpeakDup:
		m.stats.SpeakDeduped++
	case debugKindUtterance:
		m.stats.Utterances++
	case debugKindTranscript:
		m.stats.Transcripts++
	case debugKindError:
		m.stats.Errors++
	}
	m.timeline = append(m.timeline, gen.GlassDebugTimelineEntry{
		Kind:      kind,
		Detail:    detail,
		Timestamp: formatTime(now),
	})
	if len(m.timeline) > debugTimelineMax {
		m.timeline = m.timeline[len(m.timeline)-debugTimelineMax:]
	}
}

func (m *debugManager) snapshot() (gen.GlassDebugStats, []gen.GlassDebugTimelineEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats, append([]gen.GlassDebugTimelineEntry(nil), m.timeline...)
}

// authorizeTarget validates that an active online glass session exists and
// that optional session/generation targets match it. It returns the active
// session id and generation.
func (m *sessionManager) authorizeTarget(sessionID string, generation int64) (string, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.active
	if a == nil || a.Status != statusOnline {
		return "", 0, fmt.Errorf("no active online glass session")
	}
	if sessionID != "" && sessionID != a.SessionID {
		return "", 0, fmt.Errorf("session %q does not match active session %q", sessionID, a.SessionID)
	}
	if generation != 0 && int64(a.Generation) != generation {
		return "", 0, fmt.Errorf("generation %d does not match active generation %d", generation, a.Generation)
	}
	return a.SessionID, a.Generation, nil
}

// setFrame overwrites the active session's last frame. Latest-only semantics:
// an older unconfirmed target frame is replaced by the newest one, and only
// lastFrame is persisted for reconnect restore.
func (m *sessionManager) setFrame(frame gen.GlassRenderFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.active
	if a == nil || a.Status != statusOnline {
		return fmt.Errorf("no active online glass session")
	}
	a.LastFrame = frame
	a.HasLastFrame = true
	return nil
}

// setFrameForSession validates the session/generation and sets the frame in a
// single atomic critical section, eliminating the TOCTOU window where a
// concurrent handleClaim could replace the session between authorize and set.
func (m *sessionManager) setFrameForSession(sessionID string, generation int64, frame gen.GlassRenderFrame) (string, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.active
	if a == nil || a.Status != statusOnline {
		return "", 0, fmt.Errorf("no active online glass session")
	}
	if sessionID != "" && sessionID != a.SessionID {
		return "", 0, fmt.Errorf("session %q does not match active session %q", sessionID, a.SessionID)
	}
	if generation != 0 && int64(a.Generation) != generation {
		return "", 0, fmt.Errorf("generation %d does not match active generation %d", generation, a.Generation)
	}
	a.LastFrame = frame
	a.HasLastFrame = true
	return a.SessionID, a.Generation, nil
}

// handleRender applies a Coordinator render command to the active session.
// The handler is internal (Coordinator-only); session/generation safety is
// enforced against the active session when targets are supplied.
func (a *Actor) handleRender(ctx actor.PureContext, req gen.GlassRenderReq) (gen.GlassRenderResp, error) {
	sid, genID, err := a.sess.setFrameForSession(req.SessionID, req.Generation, req.Frame)
	if err != nil {
		a.debug.record(debugKindError, "render: "+err.Error(), a.now())
		return gen.GlassRenderResp{}, fmt.Errorf("%s: %w", callableRender, err)
	}
	a.setDisplayFrame(req.Frame, frameModeOther)
	if err := a.Save(); err != nil {
		ctx.Logger().Error("glassinteract: persist lastFrame", "err", err)
	}
	now := a.now()
	a.debug.record(debugKindRender, renderDetail(req.Frame), now)
	if emitErr := ctx.EmitEvent(eventRender, gen.GlassRenderEvent{
		SessionID:  sid,
		Generation: int64(genID),
		Frame:      req.Frame,
		Timestamp:  formatTime(now),
	}); emitErr != nil {
		ctx.Logger().Error("glassinteract: emit render event", "err", emitErr)
	}
	return gen.GlassRenderResp{
		SessionID:  sid,
		Generation: int64(genID),
		Applied:    true,
		Timestamp:  formatTime(now),
	}, nil
}

// handleSpeak accepts an ordered at-most-once Coordinator speak command.
// Duplicate message ids are suppressed (runtime-only) but remain observable
// through the glass.speak event and the debug state.
func (a *Actor) handleSpeak(ctx actor.PureContext, req gen.GlassSpeakReq) (gen.GlassSpeakResp, error) {
	sid, genID, err := a.sess.authorizeTarget(req.SessionID, req.Generation)
	if err != nil {
		a.debug.record(debugKindError, "speak: "+err.Error(), a.now())
		return gen.GlassSpeakResp{}, fmt.Errorf("%s: %w", callableSpeak, err)
	}
	if strings.TrimSpace(req.Text) == "" {
		a.debug.record(debugKindError, "speak: empty text", a.now())
		return gen.GlassSpeakResp{}, fmt.Errorf("%s: text is required", callableSpeak)
	}
	if strings.TrimSpace(req.MessageID) == "" {
		a.debug.record(debugKindError, "speak: empty message id", a.now())
		return gen.GlassSpeakResp{}, fmt.Errorf("%s: message id is required", callableSpeak)
	}
	now := a.now()
	dup := a.speak.suppress(now, req.MessageID)
	kind := debugKindSpeak
	if dup {
		kind = debugKindSpeakDup
	}
	a.debug.record(kind, req.MessageID, now)
	if emitErr := ctx.EmitEvent(eventSpeak, gen.GlassSpeakEvent{
		SessionID:  sid,
		Generation: int64(genID),
		MessageID:  req.MessageID,
		Text:       req.Text,
		Duplicate:  dup,
		Timestamp:  formatTime(now),
	}); emitErr != nil {
		ctx.Logger().Error("glassinteract: emit speak event", "err", emitErr)
	}
	return gen.GlassSpeakResp{
		SessionID:  sid,
		Generation: int64(genID),
		MessageID:  req.MessageID,
		Duplicate:  dup,
		Timestamp:  formatTime(now),
	}, nil
}

// handleCapabilities returns the capability projection sourced from the
// session claim/snapshots. Internal (Coordinator/debug tooling) surface.
func (a *Actor) handleCapabilities(_ actor.PureContext, _ gen.GlassCapabilitiesReq) (gen.GlassCapabilitiesResp, error) {
	state := gen.GlassCapabilitiesState{Active: false}
	if active, st := a.sess.snapshot(); active {
		state = gen.GlassCapabilitiesState{
			Active:       true,
			SessionID:    st.SessionID,
			DeviceID:     st.DeviceID,
			Generation:   st.Generation,
			Capabilities: st.Capabilities,
		}
	}
	return gen.GlassCapabilitiesResp{State: state}, nil
}

// handleDebugState assembles the secret-free debug projection: session,
// capabilities, current frame, current audio ack, bounded stats and timeline,
// plus the inbox snapshot, HUD whitelist projection and delivery log.
// Developer-gated: the projection carries inbox payload text and frame content
// that must not be readable by anonymous / glass-role callers. In-process
// system callers (service refs) are trusted readers like the developer panel.
func (a *Actor) handleDebugState(ctx actor.PureContext, _ gen.GlassDebugReq) (gen.GlassDebugResp, error) {
	if err := policy.RequireDeveloperOrManager(ctx.Identity().Role); err != nil {
		return gen.GlassDebugResp{}, fmt.Errorf("%s: %w", callableDebugState, err)
	}
	stats, timeline := a.debug.snapshot()
	state := gen.GlassDebugState{Stats: stats, Timeline: timeline}
	if active, st := a.sess.snapshot(); active {
		state.Session = &st
		state.Capabilities = st.Capabilities
	}
	df := a.getDisplayFrame()
	state.CurrentFrame = &df
	state.AudioAck = a.speech.activeAck()
	hud := a.hud.snapshot()
	state.Hud = &hud
	inbox := a.inbox.snapshot()
	state.Inbox = &inbox
	state.Deliveries = a.deliveryLog.snapshot()
	return gen.GlassDebugResp{State: state}, nil
}

// handleInteractionReport accepts a confirmed user interaction from MentraOS.
// It authenticates the caller as the active glass session (role=glass, token
// subject = session id), emits a glass.interaction event, and hands the reply
// to the Coordinator intake so a pending coordinator_wearable interaction can
// be correlated. The handler runs on the glass_updater lane: the coordinator
// round-trip is the same shape as the updater's delivery work and must not
// occupy the pure pool.
func (a *Actor) handleInteractionReport(ctx actor.Context, req gen.GlassInteractionReportReq) (gen.GlassInteractionReportResp, error) {
	if err := a.authorizeGlassFrame(ctx, req.SessionID, req.Generation); err != nil {
		a.debug.record(debugKindError, "interaction: "+err.Error(), a.now())
		return gen.GlassInteractionReportResp{}, fmt.Errorf("%s: %w", callableInteractionReport, err)
	}
	now := a.now()
	a.debug.record(debugKindRender, fmt.Sprintf("interaction %s action=%s value=%s tick=%d", req.ElementID, req.Action, req.Value, req.Tick), now)
	ev := gen.GlassInteractionEvent{
		SessionID:  req.SessionID,
		Generation: req.Generation,
		Tick:       req.Tick,
		ElementID:  req.ElementID,
		Action:     req.Action,
		Value:      req.Value,
		Timestamp:  formatTime(now),
	}
	if emitErr := ctx.EmitEvent(eventInteraction, ev); emitErr != nil {
		ctx.Logger().Error("glassinteract: emit interaction event", "err", emitErr)
	}
	a.deliverInteractionToCoordinator(ctx, ev)
	return gen.GlassInteractionReportResp{
		SessionID:    req.SessionID,
		Generation:   req.Generation,
		Acknowledged: true,
	}, nil
}

// deliverInteractionToCoordinator hands one confirmed interaction to the
// process-unique Coordinator so a pending coordinator_wearable interaction can
// be resolved. Runs on the glass_updater lane; failures are logged and the
// reply is dropped (the device does not retry, and unmatched interactions are
// inert on the Coordinator side).
func (a *Actor) deliverInteractionToCoordinator(ctx actor.Context, ev gen.GlassInteractionEvent) {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		ctx.Logger().Error("glassinteract: workspace service unavailable for interaction delivery")
		return
	}
	planner := ctx.Planner()
	if planner == nil {
		ctx.Logger().Error("glassinteract: planner unavailable for interaction delivery")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, wsRef, "workspace.coordinator_peek", nil).Await()
	if err != nil {
		ctx.Logger().Error("glassinteract: lookup coordinator for interaction delivery", "err", err)
		return
	}
	lookup, ok := decodeCoordinatorLookup(result)
	if !ok || !lookup.Found || lookup.ActorID == "" {
		ctx.Logger().Error("glassinteract: coordinator unavailable for interaction delivery")
		return
	}
	coordRef, ok := lookupCoordinatorRef(ctx, lookup.ActorID)
	if !ok {
		ctx.Logger().Error("glassinteract: coordinator actor unavailable for interaction delivery", "actorID", lookup.ActorID)
		return
	}
	if _, err := planner.Call(callCtx, coordRef, "coordinator_ingest_glass_reply", ev).Await(); err != nil {
		ctx.Logger().Error("glassinteract: deliver interaction reply to coordinator", "err", err)
	}
}

// renderDetail builds a compact debug detail for a rendered frame.
func renderDetail(f gen.GlassRenderFrame) string {
	var parts []string
	if f.Text != "" {
		parts = append(parts, "text="+f.Text)
	}
	if f.ImageURL != "" {
		parts = append(parts, "image=yes")
	}
	if f.Scene != nil {
		parts = append(parts, fmt.Sprintf("scene tick=%d elements=%d", f.Scene.Tick, len(f.Scene.Elements)))
	}
	if f.Layout != "" {
		parts = append(parts, "layout="+f.Layout)
	}
	return strings.Join(parts, " ")
}

// handleTelemetryReport accepts client-reported device telemetry (battery,
// charging). Glass-authenticated like every device frame path (role=glass,
// token subject = session id). The handler is stateless: it mutates only the
// hudManager, which is mutex-guarded, and emits a HUD update only on change.
func (a *Actor) handleTelemetryReport(ctx actor.PureContext, req gen.GlassTelemetryReq) (gen.GlassTelemetryResp, error) {
	if err := a.authorizeGlassFrame(ctx, req.SessionID, req.Generation); err != nil {
		return gen.GlassTelemetryResp{}, fmt.Errorf("%s: %w", callableTelemetry, err)
	}
	if a.hud.setBattery(int(req.BatteryLevel), req.Charging, true) {
		a.emitHudUpdate(ctx)
	}
	return gen.GlassTelemetryResp{Accepted: true}, nil
}
