package glassinteract

import (
	"testing"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestHudManager_Connection(t *testing.T) {
	h := newHudManager()

	if snap := h.snapshot(); snap.Connection != "offline" {
		t.Fatalf("initial connection = %q, want offline", snap.Connection)
	}

	if !h.setConnection("online") {
		t.Fatal("setConnection(online) should report changed=true")
	}
	if snap := h.snapshot(); snap.Connection != "online" {
		t.Fatalf("after online, connection = %q", snap.Connection)
	}

	// Same value → no change
	if h.setConnection("online") {
		t.Fatal("setConnection(online) repeated should report changed=false")
	}

	if !h.setConnection("offline") {
		t.Fatal("setConnection(offline) should report changed=true")
	}
}

func TestHudManager_Battery(t *testing.T) {
	h := newHudManager()

	// Before any telemetry, snapshot should not include battery.
	snap := h.snapshot()
	if snap.BatteryLevel != 0 || snap.Charging {
		t.Fatalf("initial battery = %d/%v, want absent", snap.BatteryLevel, snap.Charging)
	}

	if !h.setBattery(85, false, true) {
		t.Fatal("first setBattery should report changed")
	}
	snap = h.snapshot()
	if snap.BatteryLevel != 85 || snap.Charging {
		t.Fatalf("after setBattery(85,false), got %d/%v", snap.BatteryLevel, snap.Charging)
	}

	// Same values → no change
	if h.setBattery(85, false, true) {
		t.Fatal("repeated setBattery should report no change")
	}

	// Changed charging → change
	if !h.setBattery(85, true, true) {
		t.Fatal("setBattery charging=true should report changed")
	}
	snap = h.snapshot()
	if !snap.Charging {
		t.Fatal("charging not reflected in snapshot")
	}
}

func TestHudManager_AgentOutcomeTransition(t *testing.T) {
	h := newHudManager()

	// Agent starts running
	if !h.setAgent(gen.AgentStatusResp{
		State:       "running",
		DisplayName: "coordinator",
	}) {
		t.Fatal("first setAgent should report changed")
	}

	snap := h.snapshot()
	if !snap.AgentRunning || snap.AgentName != "coordinator" || snap.AgentState != "running" {
		t.Fatalf("running snapshot = %+v", snap)
	}
	if snap.AgentOutcome != "" {
		t.Fatalf("outcome during running = %q, want empty", snap.AgentOutcome)
	}

	// Agent goes idle → outcome should be "completed"
	if !h.setAgent(gen.AgentStatusResp{
		State:       "idle",
		DisplayName: "coordinator",
	}) {
		t.Fatal("idle transition should report changed")
	}
	snap = h.snapshot()
	if snap.AgentRunning || snap.AgentOutcome != "completed" {
		t.Fatalf("after idle, snapshot = %+v", snap)
	}

	// Agent runs again, then errors → outcome "failed"
	h.setAgent(gen.AgentStatusResp{State: "running", DisplayName: "coordinator"})
	h.setAgent(gen.AgentStatusResp{State: "error", DisplayName: "coordinator"})
	snap = h.snapshot()
	if snap.AgentOutcome != "failed" {
		t.Fatalf("after error, outcome = %q, want failed", snap.AgentOutcome)
	}
}

func TestHudManager_NoChangeDetection(t *testing.T) {
	h := newHudManager()
	h.setAgent(gen.AgentStatusResp{State: "idle", DisplayName: "coord"})
	h.setConnection("online")
	h.setBattery(50, false, true)

	// Repeat everything → no change
	if h.setAgent(gen.AgentStatusResp{State: "idle", DisplayName: "coord"}) {
		t.Fatal("identical setAgent should not report change")
	}
	if h.setConnection("online") {
		t.Fatal("identical setConnection should not report change")
	}
	if h.setBattery(50, false, true) {
		t.Fatal("identical setBattery should not report change")
	}
}

// TestHandleTelemetryReport verifies the telemetry callable updates HUD
// battery state and emits a glass.hud.update event on change.
func TestHandleTelemetryReport(t *testing.T) {
	a := claimedActorWithInbox(t)
	ctx := testutilWithHud()

	// First report — should update and emit
	resp, err := a.handleTelemetryReport(ctx, gen.GlassTelemetryReq{
		SessionID:    "gs_aaa",
		Generation:   1,
		BatteryLevel: 72,
		Charging:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted {
		t.Fatal("telemetry not accepted")
	}

	// Verify HUD state updated
	snap := a.hud.snapshot()
	if snap.BatteryLevel != 72 || !snap.Charging {
		t.Fatalf("hud battery = %d/%v, want 72/true", snap.BatteryLevel, snap.Charging)
	}

	// Verify event emitted
	found := false
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind == eventHud {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("glass.hud.update event not emitted")
	}

	// Same telemetry — should not emit again
	evCount := len(ctx.EmittedEvents)
	a.handleTelemetryReport(ctx, gen.GlassTelemetryReq{
		SessionID:    "gs_aaa",
		Generation:   1,
		BatteryLevel: 72,
		Charging:     true,
	})
	if len(ctx.EmittedEvents) != evCount {
		t.Fatalf("expected no new events for identical telemetry, got %d → %d", evCount, len(ctx.EmittedEvents))
	}
}

// TestTelemetryWrongSession verifies telemetry from the wrong session is rejected.
func TestTelemetryWrongSession(t *testing.T) {
	a := claimedActorWithInbox(t)
	ctx := testutilWithHud()

	_, err := a.handleTelemetryReport(ctx, gen.GlassTelemetryReq{
		SessionID:  "wrong_session",
		Generation: 1,
	})
	if err == nil {
		t.Fatal("telemetry with wrong session should fail")
	}
}

// testutilWithHud returns a FakeCtx for HUD emit tests.
func testutilWithHud() *testutil.FakeCtx {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.HasEventSubscribersFn = func(kind string) bool { return true }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }
	return ctx
}
