package glassinteract

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// fakePlanner is a minimal actor.Planner that dispatches Call to a test hook.
type fakePlanner struct {
	callFunc func(context.Context, ref.Ref, string, any) (any, error)
}

func (f fakePlanner) Plan(_ ref.Ref, _ string, _ any, _ ...plan.Option) (plan.Node, error) {
	return nil, nil
}

func (f fakePlanner) Call(ctx context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	result, err := f.callFunc(ctx, target, callID, payload)
	if err != nil {
		return promise.Reject[any](err)
	}
	return promise.Resolve[any](result)
}

func (f fakePlanner) Stream(_ context.Context, _ ref.Ref, _ string, _ any, _ func(any) error) *promise.Promise[any] {
	return nil
}

// validPCM returns a valid chunk of the requested byte length (zeros).
func validPCM(n int) []byte {
	return make([]byte, n)
}

// claimedSpeechActor returns an actor whose session manager has an active
// online glass session (gs_aaa / dev-1, generation 1).
func claimedSpeechActor(t *testing.T) *Actor {
	t.Helper()
	a, _ := freshTestActor(t)
	res := a.sess.claim(testTime(), "gs_aaa", "dev-1", []gen.GlassCapability{{Name: "audio"}})
	if res.Rejected || res.Session == nil {
		t.Fatalf("claim failed: %+v", res)
	}
	return a
}

// speechCtx returns a glass-authenticated FakeCtx for gs_aaa wired to the
// provided planner and service refs.
func speechCtx(planner actor.Planner, voiceRef, wsRef ref.Ref) *testutil.FakeCtx {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.Identity_ = id.Identity{Role: "glass", Subject: "gs_aaa"}
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		switch name {
		case "voice":
			return voiceRef, true
		case "workspace":
			return wsRef, true
		}
		return nil, false
	}
	return ctx
}

// ---------------------------------------------------------------------------
// Codec / chunk validation
// ---------------------------------------------------------------------------

func TestValidateChunkBounds(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want bool
	}{
		{"20ms exact", 640, true},
		{"40ms exact", 1280, true},
		{"below 20ms", 639, false},
		{"above 40ms", 1281, false},
		{"odd length 20ms+1", 641, false},
		{"zero", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateChunk(validPCM(c.n))
			if (err == nil) != c.want {
				t.Errorf("validateChunk(%d) err = %v, want err? %v", c.n, err, c.want)
			}
		})
	}
}

func TestSpeechStartValidatesMVPParams(t *testing.T) {
	a := claimedSpeechActor(t)
	base := gen.GlassSpeechStartReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1"}

	if _, err := a.handleSpeechStart(speechCtx(nil, nil, nil), base); err != nil {
		t.Fatalf("valid start rejected: %v", err)
	}

	for name, mutate := range map[string]func(*gen.GlassSpeechStartReq){
		"sample rate": func(r *gen.GlassSpeechStartReq) { r.SampleRate = 8000 },
		"channels":    func(r *gen.GlassSpeechStartReq) { r.Channels = 2 },
		"bits":        func(r *gen.GlassSpeechStartReq) { r.BitsPerSample = 8 },
		"empty id":    func(r *gen.GlassSpeechStartReq) { r.UtteranceID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			mutate(&req)
			if _, err := a.handleSpeechStart(speechCtx(nil, nil, nil), req); err == nil {
				t.Errorf("start(%s) accepted invalid params", name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Sequencing: gaps, duplicates, cumulative ACK
// ---------------------------------------------------------------------------

func TestSpeechCumulativeAck(t *testing.T) {
	m := newSpeechManager()
	now := testTime()
	if _, err := m.start(now, "gs_aaa", 1, "utt_1", 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	// Sequential chunks 0..2 advance the contiguous prefix.
	for seq := int64(0); seq <= 2; seq++ {
		ack, err := m.chunk(now, "gs_aaa", 1, "utt_1", seq, validPCM(640))
		if err != nil {
			t.Fatalf("chunk %d: %v", seq, err)
		}
		if ack.HighestContiguousSeq != seq {
			t.Fatalf("after chunk %d ack = %d, want %d", seq, ack.HighestContiguousSeq, seq)
		}
	}

	// A gap: seq 4 is buffered but does not advance the ACK.
	ack, err := m.chunk(now, "gs_aaa", 1, "utt_1", 4, validPCM(640))
	if err != nil {
		t.Fatal(err)
	}
	if ack.HighestContiguousSeq != 2 {
		t.Fatalf("gap ack = %d, want 2", ack.HighestContiguousSeq)
	}

	// Filling the gap advances through the buffered chunk.
	ack, err = m.chunk(now, "gs_aaa", 1, "utt_1", 3, validPCM(640))
	if err != nil {
		t.Fatal(err)
	}
	if ack.HighestContiguousSeq != 4 {
		t.Fatalf("after gap fill ack = %d, want 4", ack.HighestContiguousSeq)
	}

	// Duplicates never regress or change the ACK.
	ack, err = m.chunk(now, "gs_aaa", 1, "utt_1", 2, validPCM(640))
	if err != nil {
		t.Fatal(err)
	}
	if ack.HighestContiguousSeq != 4 {
		t.Fatalf("duplicate changed ack to %d, want 4", ack.HighestContiguousSeq)
	}
	if m.active.totalBytes != 5*640 {
		t.Fatalf("duplicate stored audio: totalBytes = %d, want %d", m.active.totalBytes, 5*640)
	}
}

func TestSpeechOutOfOrderChunks(t *testing.T) {
	m := newSpeechManager()
	if _, err := m.start(testTime(), "gs_aaa", 1, "utt_1", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	seqs := []int64{3, 1, 0, 2}
	wantHighest := []int64{-1, -1, 1, 3}
	for i, seq := range seqs {
		ack, err := m.chunk(testTime(), "gs_aaa", 1, "utt_1", seq, validPCM(640))
		if err != nil {
			t.Fatalf("chunk %d: %v", seq, err)
		}
		if ack.HighestContiguousSeq != wantHighest[i] {
			t.Fatalf("step %d (seq %d) ack = %d, want %d", i, seq, ack.HighestContiguousSeq, wantHighest[i])
		}
	}
}

func TestSpeechEndWithGapIsNotSubmitted(t *testing.T) {
	a := claimedSpeechActor(t)
	ctx := speechCtx(nil, nil, nil)
	if _, err := a.handleSpeechStart(ctx, gen.GlassSpeechStartReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1"}); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []int64{0, 1, 3} {
		if _, err := a.handleSpeechChunk(ctx, gen.GlassSpeechChunkReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1", Sequence: seq, Pcm: validPCM(640)}); err != nil {
			t.Fatalf("chunk %d: %v", seq, err)
		}
	}
	resp, err := a.handleSpeechEnd(ctx, gen.GlassSpeechEndReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Ack.Accepted {
		t.Fatalf("end with gap accepted: %+v", resp.Ack)
	}
	if !strings.Contains(resp.Ack.Reason, "missing seq 2") {
		t.Fatalf("gap reason = %q, want mention of seq 2", resp.Ack.Reason)
	}
	// Chunks after a gapped end are refused (no partial STT).
	if _, err := a.handleSpeechChunk(ctx, gen.GlassSpeechChunkReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1", Sequence: 2, Pcm: validPCM(640)}); err == nil {
		t.Fatal("chunk after end accepted, want refusal")
	}
}

func TestSpeechEndCompleteAndIdempotent(t *testing.T) {
	m := newSpeechManager()
	if _, err := m.start(testTime(), "gs_aaa", 1, "utt_1", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	chunks := [][]byte{validPCM(640), validPCM(640), validPCM(640)}
	for i, pcm := range chunks {
		if _, err := m.chunk(testTime(), "gs_aaa", 1, "utt_1", int64(i), pcm); err != nil {
			t.Fatal(err)
		}
	}
	ack, complete, pcm, err := m.end("gs_aaa", 1, "utt_1")
	if err != nil {
		t.Fatal(err)
	}
	if !complete || !ack.Accepted || !ack.EndReceived {
		t.Fatalf("end = complete=%v ack=%+v", complete, ack)
	}
	want := bytes.Join(chunks, nil)
	if !bytes.Equal(pcm, want) {
		t.Fatalf("assembled pcm len %d, want %d", len(pcm), len(want))
	}

	// Second end is idempotent: no PCM is returned, so no double submission.
	ack2, complete2, pcm2, err := m.end("gs_aaa", 1, "utt_1")
	if err != nil {
		t.Fatal(err)
	}
	if !complete2 || ack2.Accepted != ack.Accepted {
		t.Fatalf("second end = complete=%v ack=%+v", complete2, ack2)
	}
	if pcm2 != nil {
		t.Fatal("second end returned pcm; handler would double-submit STT")
	}
}

func TestSpeechUnknownUtteranceErrors(t *testing.T) {
	m := newSpeechManager()
	if _, err := m.start(testTime(), "gs_aaa", 1, "utt_1", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.chunk(testTime(), "gs_aaa", 1, "utt_other", 0, validPCM(640)); err == nil {
		t.Fatal("chunk for unknown utterance accepted")
	}
	if _, _, _, err := m.end("gs_aaa", 1, "utt_other"); err == nil {
		t.Fatal("end for unknown utterance accepted")
	}
	if _, err := m.chunk(testTime(), "gs_aaa", 2, "utt_1", 0, validPCM(640)); err == nil {
		t.Fatal("chunk for stale generation accepted")
	}
}

// ---------------------------------------------------------------------------
// Max duration
// ---------------------------------------------------------------------------

func TestSpeechMaxDurationEnforced(t *testing.T) {
	m := newSpeechManager()
	if _, err := m.start(testTime(), "gs_aaa", 1, "utt_1", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	// 640 bytes/chunk at 16kHz mono 16-bit is exactly 20ms of audio.
	chunkBytes := int64(640)
	chunks := speechMaxBytes / chunkBytes // 1500 chunks == 30s
	for i := int64(0); i < chunks; i++ {
		if _, err := m.chunk(testTime(), "gs_aaa", 1, "utt_1", i, validPCM(640)); err != nil {
			t.Fatalf("chunk %d within 30s rejected: %v", i, err)
		}
	}
	// One more chunk pushes past the 30s cap and is rejected.
	if _, err := m.chunk(testTime(), "gs_aaa", 1, "utt_1", chunks, validPCM(640)); err == nil {
		t.Fatal("chunk past 30s accepted")
	}
}

// ---------------------------------------------------------------------------
// Hotwords
// ---------------------------------------------------------------------------

func TestHotwordVariants(t *testing.T) {
	got := hotwordVariants("小明")
	want := []string{"小明", "@小明", "小明助手"}
	if len(got) != len(want) {
		t.Fatalf("variants = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("variant[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if v := hotwordVariants(""); v != nil {
		t.Fatalf("empty nickname produced %v, want nil", v)
	}
	if v := hotwordVariants("  小明  "); len(v) != 3 || v[0] != "小明" {
		t.Fatalf("untrimmed nickname variants = %v", v)
	}
}

func TestCoordinatorHotwordsFetchedExplicitly(t *testing.T) {
	a := claimedSpeechActor(t)
	var lookedUp bool
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	planner := fakePlanner{callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
		if callID == "workspace.coordinator_peek" {
			lookedUp = true
			return gen.WorkspaceCoordinatorLookupResp{Found: true, Nickname: "小明", ActorID: "coord-1"}, nil
		}
		return nil, nil
	}}
	ctx := speechCtx(planner, nil, wsRef)

	got := a.coordinatorHotwords(ctx)
	if !lookedUp {
		t.Fatal("workspace.coordinator.peek was not invoked")
	}
	want := []string{"小明", "@小明", "小明助手"}
	if len(got) != len(want) {
		t.Fatalf("hotwords = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hotword[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// STT submission + Coordinator handoff payload
// ---------------------------------------------------------------------------

func TestSpeechEndSubmitsSTTAndHandoff(t *testing.T) {
	a := claimedSpeechActor(t)

	var recognizeReq gen.VoiceRecognizeReq
	var delivered gen.GlassTranscript
	voiceRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	coordID := testutil.GenActorID()
	coordRef := testutil.NewFakeRef(coordID, nil)
	planner := fakePlanner{callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
		switch callID {
		case "workspace.coordinator_peek":
			return gen.WorkspaceCoordinatorLookupResp{Found: true, Nickname: "小明", ActorID: coordID.String()}, nil
		case "workspace.coordinator_lookup":
			return gen.WorkspaceCoordinatorLookupResp{Found: true, Nickname: "小明", ActorID: coordID.String()}, nil
		case "voice.recognize":
			recognizeReq, _ = payload.(gen.VoiceRecognizeReq)
			return gen.VoiceRecognizeResp{Text: "查一下天气"}, nil
		case "coordinator_ingest_glass_transcript":
			delivered, _ = payload.(gen.GlassTranscript)
			return nil, nil
		}
		return nil, nil
	}}
	ctx := speechCtx(planner, voiceRef, wsRef)
	ctx.LookupIDFn = func(got id.ActorID) (ref.Ref, bool) {
		return coordRef, got == coordID
	}

	if _, err := a.handleSpeechStart(ctx, gen.GlassSpeechStartReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1"}); err != nil {
		t.Fatal(err)
	}
	pcm := []byte{0x00, 0x00, 0x01, 0x00, 0x02, 0x00, 0x03, 0x00}
	// 8 bytes is too short; pad to a valid 640-byte chunk with the marker prefix.
	chunk := append(append([]byte{}, pcm...), validPCM(speechChunkBytesMin-len(pcm))...)
	if _, err := a.handleSpeechChunk(ctx, gen.GlassSpeechChunkReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1", Sequence: 0, Pcm: chunk}); err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleSpeechEnd(ctx, gen.GlassSpeechEndReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Ack.Accepted || !resp.Ack.EndReceived {
		t.Fatalf("end ack = %+v, want accepted", resp.Ack)
	}

	// STT submission: existing voice.recognize path with PCM audio type and
	// injected nickname hotwords.
	if recognizeReq.Audio.AudioType != glassAudioTypePCM {
		t.Fatalf("audio type = %d, want %d", recognizeReq.Audio.AudioType, glassAudioTypePCM)
	}
	if !bytes.Equal(recognizeReq.Audio.Data, chunk) {
		t.Fatalf("STT pcm mismatch: len %d want %d", len(recognizeReq.Audio.Data), len(chunk))
	}
	wantHotwords := []string{"小明", "@小明", "小明助手"}
	if len(recognizeReq.Hotwords) != len(wantHotwords) {
		t.Fatalf("hotwords = %v, want %v", recognizeReq.Hotwords, wantHotwords)
	}
	for i := range wantHotwords {
		if recognizeReq.Hotwords[i] != wantHotwords[i] {
			t.Fatalf("hotword[%d] = %q, want %q", i, recognizeReq.Hotwords[i], wantHotwords[i])
		}
	}

	// Transcript handoff: event emitted + latest callable returns it.
	tr := a.speech.latestTranscript()
	if tr == nil {
		t.Fatal("no transcript recorded")
	}
	if tr.Text != "查一下天气" || tr.SessionID != "gs_aaa" || tr.UtteranceID != "utt_1" {
		t.Fatalf("transcript = %+v", tr)
	}
	if tr.Generation != 1 || tr.DeviceID != "dev-1" || tr.Timestamp == "" || tr.Error != "" {
		t.Fatalf("transcript meta = %+v", tr)
	}
	if delivered.Text != "查一下天气" || delivered.SessionID != "gs_aaa" || delivered.UtteranceID != "utt_1" {
		t.Fatalf("coordinator delivery = %+v", delivered)
	}

	var ev gen.GlassTranscriptEvent
	evFound := false
	for i := range ctx.EmittedEvents {
		if ctx.EmittedEvents[i].Kind == eventTranscript {
			ev, evFound = ctx.EmittedEvents[i].Payload.(gen.GlassTranscriptEvent)
		}
	}
	if !evFound || ev.Transcript.Text != "查一下天气" {
		t.Fatalf("transcript event = %+v (found=%v)", ev, evFound)
	}

	latest, err := a.handleTranscriptLatest(nil, gen.GlassTranscriptLatestReq{})
	if err != nil {
		t.Fatal(err)
	}
	if latest.Transcript == nil || latest.Transcript.Text != "查一下天气" {
		t.Fatalf("transcript.latest = %+v", latest)
	}
}

func TestSpeechEndSTTFailureRecordsError(t *testing.T) {
	a := claimedSpeechActor(t)
	voiceRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	planner := fakePlanner{callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
		if callID == "voice.recognize" {
			return nil, context.DeadlineExceeded
		}
		return gen.WorkspaceCoordinatorLookupResp{Found: true, Nickname: "小明", ActorID: "coord-1"}, nil
	}}
	ctx := speechCtx(planner, voiceRef, wsRef)

	if _, err := a.handleSpeechStart(ctx, gen.GlassSpeechStartReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleSpeechChunk(ctx, gen.GlassSpeechChunkReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1", Sequence: 0, Pcm: validPCM(640)}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleSpeechEnd(ctx, gen.GlassSpeechEndReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1"}); err != nil {
		t.Fatal(err)
	}
	tr := a.speech.latestTranscript()
	if tr == nil || tr.Error == "" {
		t.Fatalf("STT failure did not record transcript error: %+v", tr)
	}
	if tr.Text != "" {
		t.Fatalf("failed transcript has text %q", tr.Text)
	}
}

// ---------------------------------------------------------------------------
// Frame authorization
// ---------------------------------------------------------------------------

func TestAuthorizeGlassFrame(t *testing.T) {
	a := claimedSpeechActor(t)

	okCtx := testutil.AnonCtx(testutil.GenActorID())
	okCtx.Identity_ = id.Identity{Role: "glass", Subject: "gs_aaa"}
	if err := a.authorizeGlassFrame(okCtx, "gs_aaa", 1); err != nil {
		t.Fatalf("valid frame rejected: %v", err)
	}

	cases := []struct {
		name    string
		role    string
		subject string
		sessID  string
		gen     int64
	}{
		{"human role", "human", "gs_aaa", "gs_aaa", 1},
		{"mismatched subject", "glass", "gs_other", "gs_aaa", 1},
		{"stale session", "glass", "gs_old", "gs_old", 1},
		{"stale generation", "glass", "gs_aaa", "gs_aaa", 99},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := testutil.AnonCtx(testutil.GenActorID())
			ctx.Identity_ = id.Identity{Role: id.Role(c.role), Subject: c.subject}
			if err := a.authorizeGlassFrame(ctx, c.sessID, c.gen); err == nil {
				t.Errorf("frame %s accepted, want rejection", c.name)
			}
		})
	}
}

func TestSpeechHandlersRequireGlassIdentity(t *testing.T) {
	a := claimedSpeechActor(t)
	ctx := testutil.HumanCtx(testutil.GenActorID()) // role=human
	if _, err := a.handleSpeechStart(ctx, gen.GlassSpeechStartReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1"}); err == nil {
		t.Fatal("speech.start from non-glass caller accepted")
	}
	if _, err := a.handleSpeechChunk(ctx, gen.GlassSpeechChunkReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1", Sequence: 0, Pcm: validPCM(640)}); err == nil {
		t.Fatal("speech.chunk from non-glass caller accepted")
	}
	if _, err := a.handleSpeechEnd(ctx, gen.GlassSpeechEndReq{SessionID: "gs_aaa", Generation: 1, UtteranceID: "utt_1"}); err == nil {
		t.Fatal("speech.end from non-glass caller accepted")
	}
}

// TestInteractionAndTelemetryRequireGlassIdentity pins the device-frame auth
// contract for interaction_report / telemetry_report: like speech.*, they
// authenticate the caller as the active glass session (role=glass, token
// subject = session id). An anonymous caller with an empty session id must not
// slide through by matching the active session.
func TestInteractionAndTelemetryRequireGlassIdentity(t *testing.T) {
	a := claimedSpeechActor(t)
	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleInteractionReport(anonCtx, gen.GlassInteractionReportReq{SessionID: "gs_aaa", Generation: 1, ElementID: "opt0", Action: "select"}); err == nil {
		t.Fatal("interaction.report from anonymous caller accepted")
	}
	if _, err := a.handleInteractionReport(anonCtx, gen.GlassInteractionReportReq{ElementID: "opt0", Action: "select"}); err == nil {
		t.Fatal("interaction.report with empty session from anonymous caller accepted")
	}
	if _, err := a.handleTelemetryReport(anonCtx, gen.GlassTelemetryReq{SessionID: "gs_aaa", Generation: 1, BatteryLevel: 80}); err == nil {
		t.Fatal("telemetry.report from anonymous caller accepted")
	}
	// The glass-authenticated device is accepted.
	glassCtx := testutil.AnonCtx(testutil.GenActorID())
	glassCtx.Identity_ = id.Identity{Role: "glass", Subject: "gs_aaa"}
	if _, err := a.handleTelemetryReport(glassCtx, gen.GlassTelemetryReq{SessionID: "gs_aaa", Generation: 1, BatteryLevel: 80}); err != nil {
		t.Fatalf("telemetry.report from glass session rejected: %v", err)
	}
}
