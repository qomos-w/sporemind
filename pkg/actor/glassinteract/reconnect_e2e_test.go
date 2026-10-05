package glassinteract_test

// Reconnect scenario over the real gateway, driven by glasssim: an abrupt
// network drop mid-utterance, then the real client's resume path — re-dial
// /ws with the SAME glass token, claim the SAME session — asserting the
// server-side reconnect contract:
//
//   - the 30s reconnect grace: a claim right after the drop RECONNECTS
//     (same generation, lastFrame restored) instead of a fresh session;
//   - audio tail resend: duplicate chunk sequences are idempotent and the
//     cumulative ACK never regresses; new sequences extend it; end completes.
//
// The grace expiry itself (formal offline at LastSeen+30s) is pinned at unit
// level (TestFormalOfflineEventAndPostGraceFresh) — the e2e covers the wire
// path within the grace window.

import (
	"context"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract/glasssim"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestGlassReconnectOverRealGateway(t *testing.T) {
	addr, _, _ := startGlassRuntime(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dev, err := glasssim.Connect(ctx, addr, glassConnKey, "dev-reconnect-e2e")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer dev.Close()

	claim, err := dev.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.Reconnected || claim.Replaced || claim.Generation != 1 {
		t.Fatalf("first claim = %+v, want fresh gen 1", claim)
	}
	firstFrame := claim.LastFrame

	// Open an utterance and push two chunks.
	utt := "reconnect-tail-1"
	if _, err := dev.SpeechStart(utt); err != nil {
		t.Fatalf("speech start: %v", err)
	}
	ack, err := dev.SpeechChunk(utt, 0, glasssim.Silence(40))
	if err != nil || ack.HighestContiguousSeq != 0 {
		t.Fatalf("chunk 0: ack=%+v err=%v", ack, err)
	}
	ack, err = dev.SpeechChunk(utt, 1, glasssim.Silence(40))
	if err != nil || ack.HighestContiguousSeq != 1 {
		t.Fatalf("chunk 1: ack=%+v err=%v", ack, err)
	}

	// Network drops mid-utterance.
	dev.Drop()

	// Resume: same token, same session. Subscriptions died with the socket,
	// so re-subscribe, then prove the push path is live before claiming
	// (subscription setup races the claim's event emit otherwise).
	if err := dev.Reconnect(ctx); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if err := dev.SubscribeEvents(); err != nil {
		t.Fatalf("resubscribe: %v", err)
	}
	subReady := false
	deadline := time.Now().Add(8 * time.Second)
	for !subReady {
		if err := dev.Telemetry(51, false); err != nil {
			t.Fatalf("probe telemetry: %v", err)
		}
	wait:
		for {
			select {
			case ev, ok := <-dev.Events():
				if !ok {
					t.Fatal("events channel closed during probe")
				}
				if ev.Kind == "glass.hud.update" {
					subReady = true
					break wait
				}
			case <-time.After(2 * time.Second):
				break wait
			}
		}
		if !subReady && time.Now().After(deadline) {
			t.Fatal("push path never came back after reconnect")
		}
	}

	rc, err := dev.Claim()
	if err != nil {
		t.Fatalf("claim after reconnect: %v", err)
	}

	// The 30s reconnect grace held: same generation, reconnected flag,
	// lastFrame restored — no fresh session, no replace.
	if !rc.Reconnected {
		t.Fatalf("claim after drop: Reconnected=false, want session resume (grace=%v)", 30*time.Second)
	}
	if rc.Generation != 1 {
		t.Fatalf("generation changed across reconnect: %d, want 1", rc.Generation)
	}
	if rc.LastFrame == nil || firstFrame == nil || rc.LastFrame.Text != firstFrame.Text {
		t.Fatalf("lastFrame not restored: first=%+v after=%+v", firstFrame, rc.LastFrame)
	}
	ev := waitEvent(t, dev, "glass.reconnected", 5*time.Second)
	le, ok := ev.Payload.(*gen.GlassLifecycleEvent)
	if !ok || le.SessionID != dev.SessionID {
		t.Fatalf("reconnected lifecycle push = %+v (%T)", ev.Payload, ev.Payload)
	}

	// Audio tail resend: the client replays chunk 1 (duplicate — the server
	// may or may not have persisted it) then continues with 2. The cumulative
	// ACK must never regress, and the utterance must still complete.
	ack, err = dev.SpeechChunk(utt, 1, glasssim.Silence(40))
	if err != nil {
		t.Fatalf("resend chunk 1: %v", err)
	}
	if ack.HighestContiguousSeq < 1 {
		t.Fatalf("duplicate chunk regressed ACK: highest=%d, want >=1", ack.HighestContiguousSeq)
	}
	ack, err = dev.SpeechChunk(utt, 2, glasssim.Silence(40))
	if err != nil {
		t.Fatalf("chunk 2 after reconnect: %v", err)
	}
	if ack.HighestContiguousSeq != 2 {
		t.Fatalf("chunk 2 did not extend ACK: highest=%d, want 2", ack.HighestContiguousSeq)
	}
	endAck, err := dev.SpeechEnd(utt)
	if err != nil {
		t.Fatalf("speech end after reconnect: %v", err)
	}
	if !endAck.EndReceived {
		t.Fatalf("end ack = %+v, want EndReceived", endAck)
	}
}
