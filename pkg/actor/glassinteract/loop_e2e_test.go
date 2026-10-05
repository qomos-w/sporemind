package glassinteract_test

// Full interaction-loop and STT happy-path over the real gateway, driven by
// the glasssim device simulator plus fake coordinator / voice children:
//
//   coordinator (fake) --render/speak(Internal)--> glassinteract
//        ^                                               | glass.render push
//        |                                               v
//        +--coordinator_ingest_glass_reply-- interaction_report (over /ws)
//
// and speech: glasssim PCM uplink → glassinteract (glass_stt lane) →
// voice.recognize (fake) → glass.transcript push + coordinator transcript
// intake. Everything the device touches goes through the real wire.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract"
	"github.com/qomos-w/sporemind/pkg/actor/glassinteract/glasssim"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// fakeVoiceActor is defined in speech_e2e_test.go (returns "e2e transcript").

// loopCoordinatorActor records the glass reply and transcript intakes and
// exposes a test driver that renders an interactive frame through the real
// glass_interact.render / speak Internal callables.
type loopCoordinatorActor struct {
	actor.Host
	mu         sync.Mutex
	replies    []gen.GlassInteractionEvent
	transcripts []gen.GlassTranscript
	drove      bool
}

func (f *loopCoordinatorActor) Type() string { return "agent" }

type driveWearableReq struct{}

func (f *loopCoordinatorActor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("agent_status", func(_ actor.PureContext) (gen.AgentStatusResp, error) {
		return gen.AgentStatusResp{State: "idle"}, nil
	}, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("coordinator_ingest_glass_reply", func(_ actor.Context, ev gen.GlassInteractionEvent) (map[string]any, error) {
		f.mu.Lock()
		f.replies = append(f.replies, ev)
		f.mu.Unlock()
		return map[string]any{"received": true}, nil
	}, actor.Internal()); err != nil {
		return err
	}
	if err := ctx.Register("coordinator_ingest_glass_transcript", func(_ actor.Context, tr gen.GlassTranscript) (map[string]any, error) {
		f.mu.Lock()
		f.transcripts = append(f.transcripts, tr)
		f.mu.Unlock()
		return map[string]any{"received": true}, nil
	}, actor.Internal()); err != nil {
		return err
	}
	// Test driver: mirrors what the real Coordinator does for
	// coordinator_wearable_call — speak the prompt and render the frame via
	// the Internal glass_interact surface.
	if err := ctx.Register("fake_drive_wearable", func(c actor.Context, _ driveWearableReq) (map[string]any, error) {
		glassRef, ok := c.LookupService("glass_interact")
		if !ok {
			return nil, context.Canceled
		}
		lc := c.Lifecycle()
		frame := gen.GlassRenderFrame{
			Layout: "columns",
			Scene: &gen.GlassScene{
				Tick: 7,
				Elements: []gen.GlassSceneElement{
					{ID: "ask-prompt", Type: "text", Text: "要继续吗？", Box: gen.GlassSceneBox{X: 0, Y: 0, W: 280, H: 120}},
					{ID: "opt-list", Type: "list", Box: gen.GlassSceneBox{X: 296, Y: 0, W: 280, H: 120},
						Options: []gen.GlassSceneOption{{ID: "opt-0", Text: "继续"}, {ID: "opt-1", Text: "取消"}}},
				},
				FocusID: "opt-list",
			},
		}
		call := glassRef.Invoke(lc, "glass_interact.render", gen.GlassRenderReq{Frame: frame})
		if _, err := call.Final(lc); err != nil {
			return nil, err
		}
		speak := glassRef.Invoke(lc, "glass_interact.speak", gen.GlassSpeakReq{Text: "请选择", MessageID: "wi_e2e_1"})
		if _, err := speak.Final(lc); err != nil {
			return nil, err
		}
		f.mu.Lock()
		f.drove = true
		f.mu.Unlock()
		return map[string]any{"ok": true}, nil
	}, actor.Public()); err != nil {
		return err
	}
	return ctx.RegisterDomain("coordinator").Expose()
}

func (f *loopCoordinatorActor) snapshot() (replies []gen.GlassInteractionEvent, transcripts []gen.GlassTranscript, drove bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gen.GlassInteractionEvent(nil), f.replies...),
		append([]gen.GlassTranscript(nil), f.transcripts...), f.drove
}

func TestGlassInteractionLoopOverRealGateway(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	t.Setenv("SPOREMIND_GLASS_KEY", "conn-e2e-key")

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	coord := &loopCoordinatorActor{}
	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "glassinteract", Factory: func() actor.Actor { return &glassinteract.Actor{} }, RequirePersistent: true, Role: "system", Planner: true},
			{Name: "coordinator", Factory: func() actor.Actor { return coord }, Role: "system"},
			{Name: "workspace", Factory: func() actor.Actor { return &fakeWorkspaceCoordinator{} }, Role: "system"},
			{Name: "voice", Factory: func() actor.Actor { return &fakeVoiceActor{} }, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()
	select {
	case <-handle.GatewayReady():
	case <-time.After(5 * time.Second):
		t.Fatal("gateway never became ready")
	}
	addr := handle.App().GatewayServer().Addr()

	// Device half: real wire.
	dev, err := glasssim.Connect(ctx, addr, "conn-e2e-key", "dev-loop-e2e")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer dev.Close()
	if _, err := dev.Claim(); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := dev.SubscribeEvents(); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Coordinator half: drive the wearable render through the real Internal
	// surface (what coordinator_wearable_call does under the hood).
	coordRef, ok := handle.App().LookupService("coordinator")
	if !ok {
		t.Fatal("coordinator service not found")
	}
	call := coordRef.Invoke(context.Background(), "fake_drive_wearable", driveWearableReq{})
	if call == nil {
		t.Fatal("drive invoke returned nil")
	}
	defer call.Close()
	if _, err := call.RecvRaw(); err != nil {
		t.Fatalf("drive wearable: %v", err)
	}

	// The device receives the render and speak pushes over the wire.
	renderEv := waitEvent(t, dev, "glass.render", 5*time.Second)
	re, ok := renderEv.Payload.(*gen.GlassRenderEvent)
	if !ok || re.Frame.Scene == nil || len(re.Frame.Scene.Elements) != 2 || re.Frame.Scene.FocusID != "opt-list" {
		t.Fatalf("render push = %+v (payload %T)", renderEv.Payload, renderEv.Payload)
	}
	_ = waitEvent(t, dev, "glass.speak", 5*time.Second)

	// User gesture: the device reports the selection over the wire.
	if err := dev.Interact("opt-list", "select", "opt-0"); err != nil {
		t.Fatalf("interact: %v", err)
	}
	_ = waitEvent(t, dev, "glass.interaction", 5*time.Second)

	// The reply arrives at the coordinator intake with the element id.
	var replies []gen.GlassInteractionEvent
	deadline := time.Now().Add(5 * time.Second)
	for {
		replies, _, _ = coord.snapshot()
		if len(replies) >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(replies) != 1 {
		t.Fatalf("coordinator replies = %d, want 1", len(replies))
	}
	if replies[0].ElementID != "opt-list" || replies[0].Action != "select" || replies[0].Value != "opt-0" {
		t.Fatalf("reply = %+v", replies[0])
	}
	if replies[0].SessionID != dev.SessionID || replies[0].Generation != dev.Generation {
		t.Fatalf("reply session/gen = %s/%d, want %s/%d", replies[0].SessionID, replies[0].Generation, dev.SessionID, dev.Generation)
	}
}

func TestGlassSpeechHappyPathOverRealGateway(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	t.Setenv("SPOREMIND_GLASS_KEY", "conn-e2e-key")

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	coord := &loopCoordinatorActor{}
	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "glassinteract", Factory: func() actor.Actor { return &glassinteract.Actor{} }, RequirePersistent: true, Role: "system", Planner: true},
			{Name: "coordinator", Factory: func() actor.Actor { return coord }, Role: "system"},
			{Name: "workspace", Factory: func() actor.Actor { return &fakeWorkspaceCoordinator{} }, Role: "system"},
			{Name: "voice", Factory: func() actor.Actor { return &fakeVoiceActor{} }, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()
	select {
	case <-handle.GatewayReady():
	case <-time.After(5 * time.Second):
		t.Fatal("gateway never became ready")
	}
	addr := handle.App().GatewayServer().Addr()

	dev, err := glasssim.Connect(ctx, addr, "conn-e2e-key", "dev-stt-e2e")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer dev.Close()
	if _, err := dev.Claim(); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := dev.SubscribeEvents(); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Speech uplink: with the fake voice actor present the transcript must be
	// the happy path — text present, no Error.
	if err := dev.Speech(glasssim.Silence(200), 0); err != nil {
		t.Fatalf("speech: %v", err)
	}
	trEv := waitEvent(t, dev, "glass.transcript", 5*time.Second)
	tr, ok := trEv.Payload.(*gen.GlassTranscriptEvent)
	if !ok {
		t.Fatalf("transcript payload %T", trEv.Payload)
	}
	if tr.Transcript.Text != "e2e transcript" || tr.Transcript.Error != "" {
		t.Fatalf("transcript = %+v, want text=e2e transcript no error", tr.Transcript)
	}

	// The transcript also reaches the coordinator intake.
	var transcripts []gen.GlassTranscript
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, transcripts, _ = coord.snapshot()
		if len(transcripts) >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(transcripts) != 1 || transcripts[0].Text != "e2e transcript" {
		t.Fatalf("coordinator transcripts = %+v", transcripts)
	}
}
