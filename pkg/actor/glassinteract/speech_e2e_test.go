package glassinteract_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// fakeVoiceActor stands in for the real voice actor so the e2e can verify the
// direct glassinteract -> voice.recognize submission path (hotwords + PCM).
type fakeVoiceActor struct {
	actor.Host
	mu    sync.Mutex
	calls []gen.VoiceRecognizeReq
}

func (f *fakeVoiceActor) Type() string { return "voice" }

func (f *fakeVoiceActor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("voice.recognize", func(_ actor.PureContext, req gen.VoiceRecognizeReq) (gen.VoiceRecognizeResp, error) {
		f.mu.Lock()
		f.calls = append(f.calls, req)
		f.mu.Unlock()
		return gen.VoiceRecognizeResp{Text: "e2e transcript"}, nil
	}, actor.Public()); err != nil {
		return err
	}
	return ctx.RegisterDomain("voice").Expose()
}

func (f *fakeVoiceActor) callsSnapshot() []gen.VoiceRecognizeReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gen.VoiceRecognizeReq(nil), f.calls...)
}

// fakeWorkspaceActor stands in for the workspace so coordinator nickname
// hotwords can be resolved over the real runtime.
type fakeWorkspaceActor struct {
	actor.Host
}

func (f *fakeWorkspaceActor) Type() string { return "workspace" }

func (f *fakeWorkspaceActor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("workspace.coordinator_lookup", func(_ actor.PureContext) (gen.WorkspaceCoordinatorLookupResp, error) {
		return gen.WorkspaceCoordinatorLookupResp{Found: true, Nickname: "小明", ActorID: "coord-1"}, nil
	}, actor.Internal()); err != nil {
		return err
	}
	if err := ctx.Register("workspace.coordinator_peek", func(_ actor.PureContext) (gen.WorkspaceCoordinatorLookupResp, error) {
		return gen.WorkspaceCoordinatorLookupResp{Found: true, Nickname: "小明", ActorID: "coord-1"}, nil
	}, actor.Internal()); err != nil {
		return err
	}
	return ctx.RegisterDomain("workspace").Expose()
}

// TestGlassSpeechEndToEnd drives the full utterance protocol over the real
// runtime: bootstrap + claim, speech.start/chunk/end, direct submission to
// voice.recognize with injected nickname hotwords, and the transcript handoff
// through the internal glass_interact.transcript.latest callable.
func TestGlassSpeechEndToEnd(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	t.Setenv("SPOREMIND_GLASS_KEY", "e2e-key")

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	voice := &fakeVoiceActor{}
	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "glassinteract", Factory: func() actor.Actor { return &glassinteract.Actor{} }, RequirePersistent: true, Role: "system", Planner: true},
			{Name: "voice", Factory: func() actor.Actor { return voice }, Role: "system"},
			{Name: "workspace", Factory: func() actor.Actor { return &fakeWorkspaceActor{} }, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()
	time.Sleep(500 * time.Millisecond)

	// Bootstrap + claim a glass session (same flow as the session e2e).
	raw, err := invokeCall(t, handle, "glass_interact.bootstrap", map[string]any{
		"Key":      "e2e-key",
		"DeviceId": "dev-e2e",
	}, "", "")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	var boot struct {
		SessionID string `json:"SessionId"`
	}
	if err := json.Unmarshal(raw, &boot); err != nil {
		t.Fatalf("bootstrap parse: %v", err)
	}
	if _, err := invokeCall(t, handle, "glass_interact.session_claim", map[string]any{
		"SessionId": boot.SessionID,
		"DeviceId":  "dev-e2e",
	}, "glass", boot.SessionID); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// speech.start
	startRaw, err := invokeCall(t, handle, "glass_interact.speech_start", map[string]any{
		"SessionId":     boot.SessionID,
		"Generation":    1,
		"UtteranceId":   "utt_e2e",
		"SampleRate":    16000,
		"Channels":      1,
		"BitsPerSample": 16,
	}, "glass", boot.SessionID)
	if err != nil {
		t.Fatalf("speech.start: %v", err)
	}
	var startResp struct {
		Ack struct {
			HighestContiguousSeq int64 `json:"HighestContiguousSeq"`
		} `json:"Ack"`
	}
	if err := json.Unmarshal(startRaw, &startResp); err != nil {
		t.Fatalf("speech.start parse: %v", err)
	}
	if startResp.Ack.HighestContiguousSeq != -1 {
		t.Fatalf("fresh utterance ack = %d, want -1", startResp.Ack.HighestContiguousSeq)
	}

	// speech.chunk (2 chunks: seq 0 and 1, each 640 bytes = 20ms)
	pcm := make([]byte, 640)
	for _, seq := range []int64{0, 1} {
		ackRaw, err := invokeCall(t, handle, "glass_interact.speech_chunk", map[string]any{
			"SessionId":   boot.SessionID,
			"Generation":  1,
			"UtteranceId": "utt_e2e",
			"Sequence":    seq,
			"Pcm":         pcm,
		}, "glass", boot.SessionID)
		if err != nil {
			t.Fatalf("speech.chunk %d: %v", seq, err)
		}
		var ack struct {
			HighestContiguousSeq int64 `json:"HighestContiguousSeq"`
		}
		if err := json.Unmarshal(ackRaw, &ack); err != nil {
			t.Fatalf("speech.chunk parse: %v", err)
		}
		if ack.HighestContiguousSeq != seq {
			t.Fatalf("chunk %d ack = %d, want %d", seq, ack.HighestContiguousSeq, seq)
		}
	}

	// speech.end
	endRaw, err := invokeCall(t, handle, "glass_interact.speech_end", map[string]any{
		"SessionId":   boot.SessionID,
		"Generation":  1,
		"UtteranceId": "utt_e2e",
	}, "glass", boot.SessionID)
	if err != nil {
		t.Fatalf("speech.end: %v", err)
	}
	var endResp struct {
		Ack struct {
			Accepted bool   `json:"Accepted"`
			Reason   string `json:"Reason"`
		} `json:"Ack"`
	}
	if err := json.Unmarshal(endRaw, &endResp); err != nil {
		t.Fatalf("speech.end parse: %v", err)
	}
	if !endResp.Ack.Accepted {
		t.Fatalf("end ack not accepted: %+v", endResp)
	}

	// The voice actor received exactly one recognize call with PCM audio and
	// the automatic nickname hotwords.
	calls := voice.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("voice.recognize calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.Audio.AudioType != 2 { // voice.AudioTypePCM
		t.Fatalf("audio type = %d, want 2", call.Audio.AudioType)
	}
	if !bytes.Equal(call.Audio.Data, append(pcm, pcm...)) {
		t.Fatalf("submitted pcm len %d, want %d", len(call.Audio.Data), 2*len(pcm))
	}
	wantHotwords := []string{"小明", "@小明", "小明助手"}
	if len(call.Hotwords) != len(wantHotwords) {
		t.Fatalf("hotwords = %v, want %v", call.Hotwords, wantHotwords)
	}
	for i := range wantHotwords {
		if call.Hotwords[i] != wantHotwords[i] {
			t.Fatalf("hotword[%d] = %q, want %q", i, call.Hotwords[i], wantHotwords[i])
		}
	}

	// Transcript handoff is readable through the internal latest callable.
	trRaw, err := invokeCall(t, handle, "glass_interact.transcript_latest", map[string]any{}, "", "")
	if err != nil {
		t.Fatalf("transcript.latest: %v", err)
	}
	var latest struct {
		Transcript *struct {
			SessionID   string `json:"SessionId"`
			UtteranceID string `json:"UtteranceId"`
			Text        string `json:"Text"`
			DeviceID    string `json:"DeviceId"`
		} `json:"Transcript"`
	}
	if err := json.Unmarshal(trRaw, &latest); err != nil {
		t.Fatalf("transcript.latest parse: %v", err)
	}
	if latest.Transcript == nil {
		t.Fatal("transcript.latest returned no transcript")
	}
	if latest.Transcript.Text != "e2e transcript" || latest.Transcript.UtteranceID != "utt_e2e" || latest.Transcript.SessionID != boot.SessionID || latest.Transcript.DeviceID != "dev-e2e" {
		t.Fatalf("transcript = %+v", latest.Transcript)
	}
}
