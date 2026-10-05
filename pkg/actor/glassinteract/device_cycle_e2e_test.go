package glassinteract_test

// Full device-cycle simulation over the real gateway, using the glasssim
// package (the Go mirror of the MentraOS client). Proves the whole no-hardware
// loop a developer can drive: claim → event subscription → telemetry (with
// the HUD push arriving as a subscribed chunk) → speech PCM uplink with
// cumulative ACKs → interaction report → get_state.

import (
	"context"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract/glasssim"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// waitEvent reads Events() until a matching kind arrives or the timeout hits.
func waitEvent(t *testing.T, d *glasssim.Device, kind string, timeout time.Duration) glasssim.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				t.Fatalf("events channel closed while waiting for %s", kind)
			}
			if ev.Kind == kind {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %s", kind)
		}
	}
}

func TestGlassDeviceCycleOverRealGateway(t *testing.T) {
	addr, _, _ := startGlassRuntime(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dev, err := glasssim.Connect(ctx, addr, glassConnKey, "dev-cycle-e2e")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer dev.Close()

	claim, err := dev.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !claim.Online || claim.Generation != 1 {
		t.Fatalf("claim resp = %+v", claim)
	}

	if err := dev.SubscribeEvents(); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Telemetry must both ACK and fan out a glass.hud.update push. The
	// subscription is established by a separate server worker; retry briefly
	// so the first push cannot race the subscribe handshake.
	var hud *gen.GlassHudStatus
	deadline := time.Now().Add(8 * time.Second)
	for hud == nil {
		if err := dev.Telemetry(77, false); err != nil {
			t.Fatalf("telemetry: %v", err)
		}
		wait:
		for {
			select {
			case ev, ok := <-dev.Events():
				if !ok {
					t.Fatal("events channel closed waiting for hud push")
				}
				if ev.Kind != "glass.hud.update" {
					continue
				}
				hud, ok = ev.Payload.(*gen.GlassHudStatus)
				if !ok {
					t.Fatalf("hud push payload %T", ev.Payload)
				}
				break wait
			case <-time.After(2 * time.Second):
				break wait // resubmit telemetry and try again
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for event glass.hud.update")
		}
	}
	if hud.BatteryLevel != 77 {
		t.Fatalf("hud push battery = %d, want 77", hud.BatteryLevel)
	}

	// Speech cycle: silence PCM, start → 2 chunks → end, cumulative ACKs.
	// STT is absent in this minimal runtime, so the transcript event carries
	// Error set — the push itself is what proves the pipeline.
	if err := dev.Speech(glasssim.Silence(200), 0); err != nil {
		t.Fatalf("speech: %v", err)
	}
	trEv := waitEvent(t, dev, "glass.transcript", 5*time.Second)
	tr, ok := trEv.Payload.(*gen.GlassTranscriptEvent)
	if !ok || tr.Transcript.SessionID != dev.SessionID {
		t.Fatalf("transcript push = %+v (payload %T)", trEv.Payload, trEv.Payload)
	}

	// Interaction report → ACK + glass.interaction observation push.
	if err := dev.Interact("opt-0", "select", "0"); err != nil {
		t.Fatalf("interact: %v", err)
	}
	intEv := waitEvent(t, dev, "glass.interaction", 5*time.Second)
	ie, ok := intEv.Payload.(*gen.GlassInteractionEvent)
	if !ok || ie.ElementID != "opt-0" || ie.Action != "select" {
		t.Fatalf("interaction push = %+v (payload %T)", intEv.Payload, intEv.Payload)
	}

	state, err := dev.State()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !state.Active || state.Session == nil || state.Session.SessionID != dev.SessionID {
		t.Fatalf("state resp = %+v", state)
	}
}
