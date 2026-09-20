// Speech utterance protocol for Glass (Stage 3). MentraOS performs local VAD
// and uploads only voiced segments over the shared /ws. Control frames
// (speech.start / speech.end) and binary audio chunks are delivered as
// glass_interact actor callables; the fixed MVP audio format is 16 kHz mono
// signed 16-bit PCM with 20-40 ms chunks.
//
// Reliability model:
//   - Every chunk carries a zero-based sequence; the server acknowledges the
//     highest contiguous sequence (cumulative ACK), so duplicates are harmless
//     and the client can resend only the tail after a short network blip.
//   - Chunks may arrive out of order; only the contiguous prefix advances the
//     ACK. speech.end submits to sporemind STT only when the received audio
//     sequence is gap-free.
//   - An utterance is capped at 30 seconds of PCM; audio bytes are runtime-only
//     state and are never persisted.
//
// The final transcription is the Coordinator handoff payload. Glass input must
// not call ordinary agents; the transcript is exposed via the glass.transcript
// event and the internal glass_interact.transcript.latest callable. Direct
// Coordinator turn creation from a glass transcript is a Stage 4/5 adapter
// concern (the Coordinator's nickname-gated coordinator.route intake does not
// fit raw glass utterances).
package glassinteract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	// speechSampleRate, speechChannels and speechBitsPerSample are the fixed
	// MVP audio parameters for Glass utterances.
	speechSampleRate    = 16000
	speechChannels      = 1
	speechBitsPerSample = 16

	// speechChunkBytesMin/Max bound a 20-40 ms chunk at 16 kHz mono 16-bit:
	// 16000 samples/s * 2 bytes/sample = 32000 B/s.
	speechChunkBytesMin = 640  // 20 ms
	speechChunkBytesMax = 1280 // 40 ms

	// speechMaxDuration caps one utterance at 30 seconds of PCM.
	speechMaxDuration = 30 * time.Second
	speechMaxBytes    = speechSampleRate * speechChannels * (speechBitsPerSample / 8) * 30 // 960000

	// glassAudioTypePCM must stay in sync with pkg/actor/voice.AudioTypePCM
	// (raw 16-bit signed little-endian PCM). The value is part of the shared
	// audio wire contract.
	glassAudioTypePCM = 2
)

// utterance is the runtime-only buffer of one in-flight utterance. It is
// intentionally not persisted: audio bytes are transient by design.
type utterance struct {
	SessionID     string
	Generation    uint64
	UtteranceID   string
	SampleRate    int
	Channels      int
	BitsPerSample int
	StartedAt     time.Time
	LastChunkAt   time.Time
	chunks        map[int64][]byte
	highest       int64 // highest contiguous sequence; -1 when none accepted
	maxSeq        int64 // highest sequence seen; -1 when none
	ended         bool
	accepted      bool   // end accepted a gap-free utterance (STT submitted)
	reason        string // rejection reason when not accepted
	totalBytes    int64
}

// ack assembles the cumulative ACK projection of the utterance.
func (u *utterance) ack() gen.GlassSpeechAck {
	return gen.GlassSpeechAck{
		SessionID:            u.SessionID,
		UtteranceID:          u.UtteranceID,
		HighestContiguousSeq: u.highest,
		EndReceived:          u.ended,
		Accepted:             u.accepted,
		Reason:               u.reason,
	}
}

// gather returns the PCM bytes of the contiguous prefix in sequence order.
func (u *utterance) gather() []byte {
	total := 0
	for i := int64(0); i <= u.highest; i++ {
		total += len(u.chunks[i])
	}
	buf := make([]byte, 0, total)
	for i := int64(0); i <= u.highest; i++ {
		buf = append(buf, u.chunks[i]...)
	}
	return buf
}

// gap describes the first missing sequence of an incomplete utterance.
func (u *utterance) gap() string {
	for i := int64(0); i <= u.maxSeq; i++ {
		if _, ok := u.chunks[i]; !ok {
			return fmt.Sprintf("missing seq %d", i)
		}
	}
	return "missing final chunks"
}

// speechManager owns the runtime-only utterance protocol state: the active
// utterance buffer, per-sequence ACK, and the latest completed transcript.
type speechManager struct {
	mu     sync.Mutex
	active *utterance
	latest *gen.GlassTranscript
}

func newSpeechManager() *speechManager {
	return &speechManager{}
}

// validateChunk checks the fixed 20-40 ms chunk size for 16 kHz mono 16-bit
// PCM: 16000 samples/s * 2 bytes/sample * 0.020..0.040 s = 640..1280 bytes.
func validateChunk(pcm []byte) error {
	if len(pcm)%2 != 0 {
		return fmt.Errorf("pcm chunk length %d is not an even number of 16-bit samples", len(pcm))
	}
	if len(pcm) < speechChunkBytesMin {
		return fmt.Errorf("pcm chunk %d bytes is shorter than 20ms at 16kHz mono 16-bit (min %d)", len(pcm), speechChunkBytesMin)
	}
	if len(pcm) > speechChunkBytesMax {
		return fmt.Errorf("pcm chunk %d bytes is longer than 40ms at 16kHz mono 16-bit (max %d)", len(pcm), speechChunkBytesMax)
	}
	return nil
}

// start begins a fresh utterance, aborting any in-flight one. It validates the
// fixed MVP audio parameters (zero values mean "use the MVP default").
func (m *speechManager) start(now time.Time, sessionID string, generation uint64, utteranceID string, sampleRate, channels, bitsPerSample int) (gen.GlassSpeechAck, error) {
	if strings.TrimSpace(utteranceID) == "" {
		return gen.GlassSpeechAck{}, fmt.Errorf("utterance id is required")
	}
	if sampleRate != 0 && sampleRate != speechSampleRate {
		return gen.GlassSpeechAck{}, fmt.Errorf("sample rate %d is not supported (fixed MVP: %d)", sampleRate, speechSampleRate)
	}
	if channels != 0 && channels != speechChannels {
		return gen.GlassSpeechAck{}, fmt.Errorf("channels %d is not supported (fixed MVP: %d)", channels, speechChannels)
	}
	if bitsPerSample != 0 && bitsPerSample != speechBitsPerSample {
		return gen.GlassSpeechAck{}, fmt.Errorf("bits per sample %d is not supported (fixed MVP: %d)", bitsPerSample, speechBitsPerSample)
	}
	if sampleRate == 0 {
		sampleRate = speechSampleRate
	}
	if channels == 0 {
		channels = speechChannels
	}
	if bitsPerSample == 0 {
		bitsPerSample = speechBitsPerSample
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	u := &utterance{
		SessionID:     sessionID,
		Generation:    generation,
		UtteranceID:   utteranceID,
		SampleRate:    sampleRate,
		Channels:      channels,
		BitsPerSample: bitsPerSample,
		StartedAt:     now,
		chunks:        make(map[int64][]byte),
		highest:       -1,
		maxSeq:        -1,
	}
	m.active = u
	return u.ack(), nil
}

// chunk accepts one PCM chunk and returns the updated cumulative ACK. It is
// duplicate-safe (re-sent chunks leave the ACK unchanged) and gap-tolerant
// (out-of-order chunks are buffered until the gap is filled).
func (m *speechManager) chunk(now time.Time, sessionID string, generation uint64, utteranceID string, seq int64, pcm []byte) (gen.GlassSpeechAck, error) {
	if err := validateChunk(pcm); err != nil {
		return gen.GlassSpeechAck{}, err
	}
	if seq < 0 {
		return gen.GlassSpeechAck{}, fmt.Errorf("sequence %d must be >= 0", seq)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.active
	if u == nil || u.SessionID != sessionID || u.Generation != generation || u.UtteranceID != utteranceID {
		return gen.GlassSpeechAck{}, fmt.Errorf("no active utterance for session %s gen %d utterance %s", sessionID, generation, utteranceID)
	}
	if u.ended {
		return u.ack(), fmt.Errorf("utterance %s already ended", utteranceID)
	}
	// Duplicate: either already contiguous or already buffered out of order.
	if seq <= u.highest {
		return u.ack(), nil
	}
	if _, ok := u.chunks[seq]; ok {
		return u.ack(), nil
	}
	if u.totalBytes+int64(len(pcm)) > speechMaxBytes {
		return u.ack(), fmt.Errorf("utterance %s exceeds the 30s cap (%d bytes)", utteranceID, speechMaxBytes)
	}

	u.chunks[seq] = pcm
	u.totalBytes += int64(len(pcm))
	u.LastChunkAt = now
	if seq > u.maxSeq {
		u.maxSeq = seq
	}
	for i := u.highest + 1; ; i++ {
		if _, ok := u.chunks[i]; !ok {
			break
		}
		u.highest = i
	}
	return u.ack(), nil
}

// end finalizes the utterance. The first call computes completeness; later
// calls are idempotent and return the stored result with no PCM (so the
// handler never double-submits STT). complete reports a gap-free audio
// sequence; pcm is non-nil only on the first end of a complete utterance.
func (m *speechManager) end(sessionID string, generation uint64, utteranceID string) (ack gen.GlassSpeechAck, complete bool, pcm []byte, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.active
	if u == nil || u.SessionID != sessionID || u.Generation != generation || u.UtteranceID != utteranceID {
		return gen.GlassSpeechAck{}, false, nil, fmt.Errorf("no active utterance for session %s gen %d utterance %s", sessionID, generation, utteranceID)
	}
	if u.ended {
		return u.ack(), u.maxSeq >= 0 && u.highest == u.maxSeq, nil, nil
	}
	u.ended = true
	ack = u.ack()
	if u.maxSeq >= 0 && u.highest == u.maxSeq {
		u.accepted = true
		ack.Accepted = true
		return ack, true, u.gather(), nil
	}
	u.reason = "audio gap: " + u.gap()
	ack.Reason = u.reason
	return ack, false, nil, nil
}

// setLatestTranscript records the newest completed transcript (runtime handoff
// state).
func (m *speechManager) setLatestTranscript(tr *gen.GlassTranscript) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latest = tr
}

// latestTranscript returns the newest completed transcript, or nil.
func (m *speechManager) latestTranscript() *gen.GlassTranscript {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.latest
}

// ackFor returns the current ACK of the active utterance, or nil.
func (m *speechManager) ackFor(sessionID string, generation uint64, utteranceID string) *gen.GlassSpeechAck {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.active
	if u == nil || u.SessionID != sessionID || u.Generation != generation || u.UtteranceID != utteranceID {
		return nil
	}
	a := u.ack()
	return &a
}

// activeAck returns the current cumulative ACK of the in-flight utterance, or
// nil when no utterance is active. It backs the debug state projection.
func (m *speechManager) activeAck() *gen.GlassSpeechAck {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.active
	if u == nil {
		return nil
	}
	a := u.ack()
	return &a
}

// hotwordVariants builds the canonical Glass STT hotword variants for a
// coordinator nickname: the nickname itself, "@nickname", and "nickname助手".
// Duplicates (e.g. a nickname that already contains "@") are removed while
// preserving order.
func hotwordVariants(nickname string) []string {
	nick := strings.TrimSpace(nickname)
	if nick == "" {
		return nil
	}
	candidates := []string{nick, "@" + nick, nick + "助手"}
	seen := make(map[string]bool, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, v := range candidates {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// coordinatorHotwords fetches the Coordinator nickname from the workspace
// (explicit lookup, no cross-actor shared mutable state) and returns the
// automatic hotword variants for Glass STT requests. Failures degrade to nil
// so STT still proceeds without nickname hints.
func (a *Actor) coordinatorHotwords(ctx actor.Context) []string {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		ctx.Logger().Warn("glassinteract: workspace service unavailable; skipping nickname hotwords")
		return nil
	}
	planner := ctx.Planner()
	if planner == nil {
		ctx.Logger().Warn("glassinteract: planner unavailable; skipping nickname hotwords")
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	// coordinator_peek is the pure read-only variant: hotwords only need the
	// nickname, so they must not pay the Coordinator spawn that
	// coordinator_lookup triggers when it is unloaded.
	result, err := planner.Call(callCtx, wsRef, "workspace.coordinator_peek", nil).Await()
	if err != nil {
		ctx.Logger().Warn("glassinteract: workspace.coordinator.peek failed; skipping nickname hotwords", "err", err)
		return nil
	}
	resp, ok := decodeCoordinatorLookup(result)
	if !ok || !resp.Found || strings.TrimSpace(resp.Nickname) == "" {
		return nil
	}
	return hotwordVariants(resp.Nickname)
}

// submitSTT submits the contiguous PCM utterance to the existing voice actor's
// sporemind STT provider path (voice.recognize) with the injected nickname
// hotwords.
func (a *Actor) submitSTT(ctx actor.Context, pcm []byte) (string, error) {
	voiceRef, ok := ctx.LookupService("voice")
	if !ok {
		return "", fmt.Errorf("voice service unavailable")
	}
	planner := ctx.Planner()
	if planner == nil {
		return "", fmt.Errorf("planner unavailable")
	}
	req := gen.VoiceRecognizeReq{
		Audio:    gen.VoiceAudio{AudioType: glassAudioTypePCM, Data: pcm},
		Hotwords: a.coordinatorHotwords(ctx),
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, voiceRef, "voice.recognize", req).Await()
	if err != nil {
		return "", fmt.Errorf("voice.recognize: %w", err)
	}
	resp, ok := decodeVoiceRecognizeResp(result)
	if !ok {
		return "", fmt.Errorf("voice.recognize returned unexpected payload type %T", result)
	}
	return resp.Text, nil
}

// finalizeUtterance records the transcript handoff for a completed utterance:
// it submits STT, stores the runtime-only transcript, and broadcasts the
// glass.transcript event. An STT failure still produces a transcript with
// Error set so the Coordinator adapter can observe and retry at its layer.
func (a *Actor) finalizeUtterance(ctx actor.Context, sessionID string, generation uint64, utteranceID string, pcm []byte) {
	text, err := a.submitSTT(ctx, pcm)
	tr := &gen.GlassTranscript{
		SessionID:   sessionID,
		UtteranceID: utteranceID,
		Generation:  int64(generation),
		DeviceID:    a.sess.deviceID(),
		Timestamp:   formatTime(a.now()),
	}
	if err != nil {
		tr.Error = err.Error()
		a.debug.record(debugKindError, "stt: "+err.Error(), a.now())
		ctx.Logger().Error("glassinteract: STT submission failed", "utterance", utteranceID, "err", err)
	} else {
		tr.Text = text
		a.debug.record(debugKindTranscript, utteranceID, a.now())
		ctx.Logger().Info("glassinteract: utterance transcribed", "utterance", utteranceID, "textLen", len(text))
	}
	a.speech.setLatestTranscript(tr)
	if err == nil {
		a.deliverTranscriptToCoordinator(ctx, *tr)
	}
	if emitErr := ctx.EmitEvent(eventTranscript, gen.GlassTranscriptEvent{Transcript: *tr}); emitErr != nil {
		ctx.Logger().Error("glassinteract: emit transcript event", "err", emitErr)
	}
}

// deliverTranscriptToCoordinator explicitly hands a successful final transcript
// to the process-unique Coordinator. The event remains an observation copy;
// it is not the semantic delivery path.
func (a *Actor) deliverTranscriptToCoordinator(ctx actor.Context, transcript gen.GlassTranscript) {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		ctx.Logger().Error("glassinteract: workspace service unavailable for transcript delivery")
		return
	}
	planner := ctx.Planner()
	if planner == nil {
		ctx.Logger().Error("glassinteract: planner unavailable for transcript delivery")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, wsRef, "workspace.coordinator_lookup", nil).Await()
	if err != nil {
		ctx.Logger().Error("glassinteract: lookup coordinator for transcript delivery", "err", err)
		return
	}
	lookup, ok := decodeCoordinatorLookup(result)
	if !ok || !lookup.Found || lookup.ActorID == "" {
		ctx.Logger().Error("glassinteract: coordinator unavailable for transcript delivery")
		return
	}
	coordRef, ok := lookupCoordinatorRef(ctx, lookup.ActorID)
	if !ok {
		ctx.Logger().Error("glassinteract: coordinator actor unavailable for transcript delivery", "actorID", lookup.ActorID)
		return
	}
	if _, err := planner.Call(callCtx, coordRef, "coordinator_ingest_glass_transcript", transcript).Await(); err != nil {
		ctx.Logger().Error("glassinteract: deliver transcript to coordinator", "err", err)
	}
}

func lookupCoordinatorRef(ctx actor.Context, actorID string) (ref.Ref, bool) {
	cid, err := identity.ParseCanonicalID(actorID)
	if err != nil {
		return nil, false
	}
	return ctx.LookupID(id.From(cid))
}

// decodeCoordinatorLookup decodes a workspace.coordinator.lookup result that
// may arrive as the typed struct, raw JSON bytes, or a generic map.
func decodeCoordinatorLookup(result any) (gen.WorkspaceCoordinatorLookupResp, bool) {
	switch v := result.(type) {
	case gen.WorkspaceCoordinatorLookupResp:
		return v, true
	case []byte:
		var resp gen.WorkspaceCoordinatorLookupResp
		if err := json.Unmarshal(v, &resp); err != nil {
			return gen.WorkspaceCoordinatorLookupResp{}, false
		}
		return resp, true
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		var resp gen.WorkspaceCoordinatorLookupResp
		_ = json.Unmarshal(b, &resp)
		return resp, true
	default:
		return gen.WorkspaceCoordinatorLookupResp{}, false
	}
}

// decodeVoiceRecognizeResp decodes a voice.recognize result that may arrive as
// the typed struct, raw JSON bytes, or a generic map.
func decodeVoiceRecognizeResp(result any) (gen.VoiceRecognizeResp, bool) {
	switch v := result.(type) {
	case gen.VoiceRecognizeResp:
		return v, true
	case []byte:
		var resp gen.VoiceRecognizeResp
		if err := json.Unmarshal(v, &resp); err != nil {
			return gen.VoiceRecognizeResp{}, false
		}
		return resp, true
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		var resp gen.VoiceRecognizeResp
		_ = json.Unmarshal(b, &resp)
		return resp, true
	default:
		return gen.VoiceRecognizeResp{}, false
	}
}

// authorizeGlassFrame verifies the caller is a glass-authenticated /ws session
// whose token subject matches the session id and that the frame targets the
// current active session generation (stale frames from replaced sessions are
// dropped).
func (a *Actor) authorizeGlassFrame(ctx actor.PureContext, sessionID string, generation int64) error {
	ident := ctx.Identity()
	if ident.Role != "glass" {
		return fmt.Errorf("requires a glass-authenticated /ws session (role=glass)")
	}
	if ident.Subject != sessionID {
		return fmt.Errorf("token subject does not match session id")
	}
	sid, gen, online := a.sess.activeIdentity()
	if sid == "" || !online {
		return fmt.Errorf("no active online glass session")
	}
	if sid != sessionID || int64(gen) != generation {
		return fmt.Errorf("stale frame: active session/generation mismatch (session=%s gen=%d)", sid, gen)
	}
	return nil
}

// handleSpeechStart begins (or restarts) one utterance.
func (a *Actor) handleSpeechStart(ctx actor.PureContext, req gen.GlassSpeechStartReq) (gen.GlassSpeechStartResp, error) {
	if err := a.authorizeGlassFrame(ctx, req.SessionID, req.Generation); err != nil {
		return gen.GlassSpeechStartResp{}, fmt.Errorf("%s: %w", callableSpeechStart, err)
	}
	ack, err := a.speech.start(a.now(), req.SessionID, uint64(req.Generation), req.UtteranceID, int(req.SampleRate), int(req.Channels), int(req.BitsPerSample))
	if err != nil {
		return gen.GlassSpeechStartResp{}, fmt.Errorf("%s: %w", callableSpeechStart, err)
	}
	a.debug.record(debugKindUtterance, req.UtteranceID, a.now())
	return gen.GlassSpeechStartResp{SessionID: req.SessionID, UtteranceID: req.UtteranceID, Ack: ack}, nil
}

// handleSpeechChunk accepts one PCM audio chunk and returns the cumulative ACK.
func (a *Actor) handleSpeechChunk(ctx actor.PureContext, req gen.GlassSpeechChunkReq) (gen.GlassSpeechAck, error) {
	if err := a.authorizeGlassFrame(ctx, req.SessionID, req.Generation); err != nil {
		return gen.GlassSpeechAck{}, fmt.Errorf("%s: %w", callableSpeechChunk, err)
	}
	ack, err := a.speech.chunk(a.now(), req.SessionID, uint64(req.Generation), req.UtteranceID, req.Sequence, req.Pcm)
	if err != nil {
		return ack, fmt.Errorf("%s: %w", callableSpeechChunk, err)
	}
	return ack, nil
}

// handleSpeechEnd finalizes the utterance and, when the audio sequence is
// gap-free, submits it to sporemind STT and records the transcript handoff.
func (a *Actor) handleSpeechEnd(ctx actor.Context, req gen.GlassSpeechEndReq) (gen.GlassSpeechEndResp, error) {
	if err := a.authorizeGlassFrame(ctx, req.SessionID, req.Generation); err != nil {
		return gen.GlassSpeechEndResp{}, fmt.Errorf("%s: %w", callableSpeechEnd, err)
	}
	ack, complete, pcm, err := a.speech.end(req.SessionID, uint64(req.Generation), req.UtteranceID)
	if err != nil {
		return gen.GlassSpeechEndResp{}, fmt.Errorf("%s: %w", callableSpeechEnd, err)
	}
	if complete && pcm != nil {
		a.finalizeUtterance(ctx, req.SessionID, uint64(req.Generation), req.UtteranceID, pcm)
	}
	return gen.GlassSpeechEndResp{SessionID: req.SessionID, UtteranceID: req.UtteranceID, Ack: ack}, nil
}

// handleTranscriptLatest returns the newest completed transcript for the
// Coordinator adapter. Internal-only; audio transcripts are runtime state.
func (a *Actor) handleTranscriptLatest(_ actor.PureContext, _ gen.GlassTranscriptLatestReq) (gen.GlassTranscriptLatestResp, error) {
	return gen.GlassTranscriptLatestResp{Transcript: a.speech.latestTranscript()}, nil
}
