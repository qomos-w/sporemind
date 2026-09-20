package glassinteract

import (
	"fmt"
	"strings"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// claimedActor returns an actor whose session manager has an active online
// glass session (gs_aaa / dev-1, generation 1) with the given capabilities.
func claimedActor(t *testing.T, caps []gen.GlassCapability) *Actor {
	t.Helper()
	a, _ := freshTestActor(t)
	res := a.sess.claim(testTime(), "gs_aaa", "dev-1", caps)
	if res.Rejected || res.Session == nil {
		t.Fatalf("claim failed: %+v", res)
	}
	return a
}

// ---------------------------------------------------------------------------
// speak duplicate suppression (runtime-only)
// ---------------------------------------------------------------------------

func TestSpeakDuplicateSuppression(t *testing.T) {
	m := newSpeakManager()
	now := testTime()
	if m.suppress(now, "msg_1") {
		t.Fatal("first speak of msg_1 reported duplicate")
	}
	if !m.suppress(now, "msg_1") {
		t.Fatal("second speak of msg_1 was not suppressed")
	}
	if m.suppress(now, "msg_2") {
		t.Fatal("first speak of msg_2 reported duplicate")
	}
}

func TestSpeakDedupWindowEviction(t *testing.T) {
	m := newSpeakManager()
	now := testTime()
	if m.suppress(now, "msg_old") {
		t.Fatal("first speak reported duplicate")
	}
	// Past the dedup window the id is treated as fresh again.
	later := now.Add(speakDedupWindow + time.Second)
	if m.suppress(later, "msg_old") {
		t.Fatal("id outside dedup window was still suppressed")
	}
}

func TestSpeakDedupCapEviction(t *testing.T) {
	m := newSpeakManager()
	now := testTime()
	ids := make([]string, speakDedupMax+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("msg_%d", i)
		m.suppress(now, ids[i])
	}
	// The newest id is still suppressed, so the set stayed bounded.
	if !m.suppress(now, ids[speakDedupMax]) {
		t.Fatal("newest id evicted despite cap")
	}
	// The oldest id was evicted when the cap was exceeded.
	if m.suppress(now, ids[0]) {
		t.Fatal("oldest id still suppressed after cap eviction")
	}
	if len(m.seen) > speakDedupMax {
		t.Fatalf("dedup set grew past cap: %d", len(m.seen))
	}
}

// ---------------------------------------------------------------------------
// debug observability
// ---------------------------------------------------------------------------

func TestDebugManagerStatsAndBoundedTimeline(t *testing.T) {
	m := newDebugManager()
	now := testTime()
	for i := 0; i < 10; i++ {
		m.record(debugKindRender, "", now)
		m.record(debugKindSpeak, "m", now)
		m.record(debugKindSpeakDup, "m", now)
		m.record(debugKindUtterance, "u", now)
		m.record(debugKindTranscript, "u", now)
	}
	m.record(debugKindError, "boom", now)

	stats, timeline := m.snapshot()
	if stats.Renders != 10 || stats.Speaks != 10 || stats.SpeakDeduped != 10 ||
		stats.Utterances != 10 || stats.Transcripts != 10 || stats.Errors != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(timeline) != debugTimelineMax {
		t.Fatalf("timeline length = %d, want %d", len(timeline), debugTimelineMax)
	}
	if timeline[len(timeline)-1].Kind != debugKindError {
		t.Fatalf("newest timeline entry = %+v, want error", timeline[len(timeline)-1])
	}
	if timeline[0].Kind != debugKindTranscript {
		t.Fatalf("oldest retained entry = %+v, want transcript (ring eviction)", timeline[0])
	}
}

// ---------------------------------------------------------------------------
// session target authorization + latest-only frame
// ---------------------------------------------------------------------------

func TestAuthorizeTargetAndSetFrame(t *testing.T) {
	m := newSessionManager()
	if _, _, err := m.authorizeTarget("", 0); err == nil {
		t.Fatal("authorizeTarget without session succeeded")
	}
	mustClaim(t, m, testTime(), "gs_aaa", "dev-1")

	sid, genID, err := m.authorizeTarget("", 0)
	if err != nil || sid != "gs_aaa" || genID != 1 {
		t.Fatalf("authorizeTarget(empty) = %s/%d/%v", sid, genID, err)
	}
	if _, _, err := m.authorizeTarget("gs_other", 0); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("authorizeTarget(wrong session) err = %v", err)
	}
	if _, _, err := m.authorizeTarget("", 2); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("authorizeTarget(wrong generation) err = %v", err)
	}

	if err := m.setFrame(gen.GlassRenderFrame{Text: "a", Layout: "top"}); err != nil {
		t.Fatalf("setFrame: %v", err)
	}
	if !m.active.HasLastFrame || m.active.LastFrame.Text != "a" {
		t.Fatalf("setFrame did not persist to active session: %+v", m.active)
	}
	// Latest-only: a newer frame overwrites the previous one.
	if err := m.setFrame(gen.GlassRenderFrame{Text: "b", Layout: "bottom"}); err != nil {
		t.Fatalf("second setFrame: %v", err)
	}
	if m.active.LastFrame.Text != "b" || m.active.LastFrame.Layout != "bottom" {
		t.Fatalf("latest frame did not overwrite: %+v", m.active.LastFrame)
	}
}

// ---------------------------------------------------------------------------
// render handler
// ---------------------------------------------------------------------------

func TestHandleRenderAppliesFrameAndEmitsEvent(t *testing.T) {
	a := claimedActor(t, []gen.GlassCapability{{Name: "native_text"}, {Name: "audio"}})
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleRender(ctx, gen.GlassRenderReq{Frame: gen.GlassRenderFrame{Text: "天气晴朗", Layout: "top"}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !resp.Applied || resp.SessionID != "gs_aaa" || resp.Generation != 1 {
		t.Fatalf("render resp = %+v", resp)
	}
	if !a.sess.active.HasLastFrame || a.sess.active.LastFrame.Text != "天气晴朗" {
		t.Fatalf("frame not stored on session: %+v", a.sess.active)
	}
	found := false
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind != eventRender {
			continue
		}
		found = true
		evt, ok := ev.Payload.(gen.GlassRenderEvent)
		if !ok || evt.SessionID != "gs_aaa" || evt.Generation != 1 || evt.Frame.Text != "天气晴朗" {
			t.Fatalf("render event payload = %#v", ev.Payload)
		}
	}
	if !found {
		t.Fatal("no glass.render event emitted")
	}
}

func TestHandleRenderLatestOnlyOverwrite(t *testing.T) {
	a := claimedActor(t, nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	for _, frame := range []gen.GlassRenderFrame{
		{Text: "first", Layout: "top"},
		{Text: "second", Layout: "bottom"},
		{Text: "third", Layout: "full"},
	} {
		if _, err := a.handleRender(ctx, gen.GlassRenderReq{Frame: frame}); err != nil {
			t.Fatalf("render %q: %v", frame.Text, err)
		}
	}
	if a.sess.active.LastFrame.Text != "third" || a.sess.active.LastFrame.Layout != "full" {
		t.Fatalf("lastFrame = %+v, want latest frame only", a.sess.active.LastFrame)
	}
	// Debug stats/timeline reflect all three renders.
	stats, _ := a.debug.snapshot()
	if stats.Renders != 3 {
		t.Fatalf("render count = %d, want 3", stats.Renders)
	}
}

func TestHandleRenderRequiresActiveSession(t *testing.T) {
	a, ctx := freshTestActor(t) // no session claimed

	if _, err := a.handleRender(ctx, gen.GlassRenderReq{Frame: gen.GlassRenderFrame{Text: "x"}}); err == nil {
		t.Fatal("render without active session succeeded")
	}
}

func TestHandleRenderRejectsStaleSessionTarget(t *testing.T) {
	a := claimedActor(t, nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	if _, err := a.handleRender(ctx, gen.GlassRenderReq{SessionID: "gs_other", Generation: 1, Frame: gen.GlassRenderFrame{Text: "x"}}); err == nil {
		t.Fatal("render with mismatched session id succeeded")
	}
	if _, err := a.handleRender(ctx, gen.GlassRenderReq{Generation: 2, Frame: gen.GlassRenderFrame{Text: "x"}}); err == nil {
		t.Fatal("render with stale generation succeeded")
	}
}

// ---------------------------------------------------------------------------
// speak handler
// ---------------------------------------------------------------------------

func TestHandleSpeakAcceptsAndSuppressesDuplicate(t *testing.T) {
	a := claimedActor(t, nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleSpeak(ctx, gen.GlassSpeakReq{Text: "收到", MessageID: "msg_1"})
	if err != nil {
		t.Fatalf("speak: %v", err)
	}
	if resp.Duplicate || resp.MessageID != "msg_1" || resp.SessionID != "gs_aaa" || resp.Generation != 1 {
		t.Fatalf("first speak resp = %+v", resp)
	}

	resp2, err := a.handleSpeak(ctx, gen.GlassSpeakReq{Text: "收到", MessageID: "msg_1"})
	if err != nil {
		t.Fatalf("duplicate speak: %v", err)
	}
	if !resp2.Duplicate {
		t.Fatalf("duplicate speak not suppressed: %+v", resp2)
	}

	// Both decisions are observable through events and debug state.
	events := map[string]int{}
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind == eventSpeak {
			events["count"]++
			if evt, ok := ev.Payload.(gen.GlassSpeakEvent); ok && evt.Duplicate {
				events["dup"]++
			}
		}
	}
	if events["count"] != 2 || events["dup"] != 1 {
		t.Fatalf("speak events = %v, want 2 total with 1 duplicate", events)
	}
	stats, _ := a.debug.snapshot()
	if stats.Speaks != 1 || stats.SpeakDeduped != 1 {
		t.Fatalf("speak stats = %+v, want speaks=1 deduped=1", stats)
	}
}

func TestHandleSpeakValidation(t *testing.T) {
	a := claimedActor(t, nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	if _, err := a.handleSpeak(ctx, gen.GlassSpeakReq{Text: "  ", MessageID: "m1"}); err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("empty text err = %v", err)
	}
	if _, err := a.handleSpeak(ctx, gen.GlassSpeakReq{Text: "hi", MessageID: ""}); err == nil || !strings.Contains(err.Error(), "message id is required") {
		t.Fatalf("empty message id err = %v", err)
	}

	a2, _ := freshTestActor(t) // no session
	if _, err := a2.handleSpeak(ctx, gen.GlassSpeakReq{Text: "hi", MessageID: "m2"}); err == nil {
		t.Fatal("speak without active session succeeded")
	}
}

// ---------------------------------------------------------------------------
// capabilities + debug state callables
// ---------------------------------------------------------------------------

func TestHandleCapabilitiesFromClaim(t *testing.T) {
	caps := []gen.GlassCapability{
		{Name: "native_text", Version: "1.0"},
		{Name: "audio", Features: []string{"vad", "pcm"}},
	}
	a := claimedActor(t, caps)

	resp, err := a.handleCapabilities(nil, gen.GlassCapabilitiesReq{})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	st := resp.State
	if !st.Active || st.SessionID != "gs_aaa" || st.DeviceID != "dev-1" || st.Generation != 1 {
		t.Fatalf("capabilities state = %+v", st)
	}
	if len(st.Capabilities) != 2 || st.Capabilities[1].Name != "audio" {
		t.Fatalf("capabilities = %+v", st.Capabilities)
	}
}

func TestHandleCapabilitiesNoSession(t *testing.T) {
	a, _ := freshTestActor(t)
	resp, err := a.handleCapabilities(nil, gen.GlassCapabilitiesReq{})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if resp.State.Active {
		t.Fatal("capabilities state active without a session")
	}
}

func TestHandleDebugStateProjection(t *testing.T) {
	a := claimedActor(t, []gen.GlassCapability{{Name: "audio"}})
	ctx := testutil.HumanCtx(testutil.GenActorID())

	// Render a frame and speak (accepted + duplicate) to populate observables.
	if _, err := a.handleRender(ctx, gen.GlassRenderReq{Frame: gen.GlassRenderFrame{Text: "当前帧", Layout: "top"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleSpeak(ctx, gen.GlassSpeakReq{Text: "好的", MessageID: "m1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleSpeak(ctx, gen.GlassSpeakReq{Text: "好的", MessageID: "m1"}); err != nil {
		t.Fatal(err)
	}
	// Start an utterance so the debug state carries a live audio ACK.
	sctx := glassCtx(testutil.GenActorID(), "gs_aaa")
	if _, err := a.handleSpeechStart(sctx, gen.GlassSpeechStartReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleSpeechChunk(sctx, gen.GlassSpeechChunkReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1", Sequence: 0, Pcm: validPCM(640)}); err != nil {
		t.Fatal(err)
	}

	resp, err := a.handleDebugState(nil, gen.GlassDebugReq{})
	if err != nil {
		t.Fatalf("debug state: %v", err)
	}
	st := resp.State
	if st.Session == nil || st.Session.SessionID != "gs_aaa" || !st.Session.Online {
		t.Fatalf("debug session = %+v", st.Session)
	}
	if len(st.Capabilities) != 1 || st.Capabilities[0].Name != "audio" {
		t.Fatalf("debug capabilities = %+v", st.Capabilities)
	}
	if st.CurrentFrame == nil || st.CurrentFrame.Text != "当前帧" {
		t.Fatalf("debug current frame = %+v", st.CurrentFrame)
	}
	if st.AudioAck == nil || st.AudioAck.HighestContiguousSeq != 0 || st.AudioAck.UtteranceID != "utt_1" {
		t.Fatalf("debug audio ack = %+v", st.AudioAck)
	}
	if st.Stats.Renders != 1 || st.Stats.Speaks != 1 || st.Stats.SpeakDeduped != 1 || st.Stats.Utterances != 1 {
		t.Fatalf("debug stats = %+v", st.Stats)
	}
	if len(st.Timeline) == 0 {
		t.Fatal("debug timeline empty")
	}
	// Timeline carries the most recent entries in order.
	last := st.Timeline[len(st.Timeline)-1]
	if last.Kind != debugKindUtterance || last.Detail != "utt_1" {
		t.Fatalf("newest timeline entry = %+v", last)
	}
}

func TestHandleDebugStateNoSession(t *testing.T) {
	a, _ := freshTestActor(t)
	resp, err := a.handleDebugState(nil, gen.GlassDebugReq{})
	if err != nil {
		t.Fatalf("debug state: %v", err)
	}
	st := resp.State
	// No session: Session and AudioAck are nil, but CurrentFrame shows the
	// default idle frame (the actor maintains a display from startup).
	if st.Session != nil || st.AudioAck != nil {
		t.Fatalf("debug state with no session leaked session/audio fields: %+v", st)
	}
	if st.CurrentFrame == nil || st.CurrentFrame.Scene == nil {
		t.Fatalf("debug state should show default idle frame even without session: %+v", st.CurrentFrame)
	}
	if len(st.Timeline) != 0 || len(st.Capabilities) != 0 {
		t.Fatalf("debug state with no session not empty: %+v", st)
	}
}
