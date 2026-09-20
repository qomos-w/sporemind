package glassinteract

import (
	"reflect"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func testTime() time.Time {
	return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
}

func mustClaim(t *testing.T, m *sessionManager, now time.Time, sessionID, deviceID string) claimResult {
	t.Helper()
	res := m.claim(now, sessionID, deviceID, nil)
	if res.Rejected {
		t.Fatalf("claim(%s) unexpectedly rejected: %s", sessionID, res.Reason)
	}
	if res.Session == nil {
		t.Fatalf("claim(%s) returned nil session", sessionID)
	}
	return res
}

// TestClaimFirstSession verifies the first claim starts a fresh online session
// with generation 1 and no reconnect/replaced flags.
func TestClaimFirstSession(t *testing.T) {
	m := newSessionManager()
	now := testTime()

	res := mustClaim(t, m, now, "gs_aaa", "dev-1")
	if !res.Fresh || res.Reconnected || res.Replaced {
		t.Fatalf("first claim flags = fresh=%v reconnected=%v replaced=%v, want fresh only", res.Fresh, res.Reconnected, res.Replaced)
	}
	if res.Session.SessionID != "gs_aaa" || res.Session.DeviceID != "dev-1" {
		t.Fatalf("session identity = %+v", res.Session)
	}
	if res.Session.Generation != 1 {
		t.Fatalf("generation = %d, want 1", res.Session.Generation)
	}
	if res.Session.Status != statusOnline {
		t.Fatalf("status = %q, want online", res.Session.Status)
	}
}

// TestClaimSameSessionReconnect verifies a same-session claim is a reconnect:
// generation and lastFrame are preserved.
func TestClaimSameSessionReconnect(t *testing.T) {
	m := newSessionManager()
	now := testTime()

	res1 := mustClaim(t, m, now, "gs_aaa", "dev-1")
	frame := gen.GlassRenderFrame{Text: "hello glass", Layout: "center"}
	m.active.LastFrame = frame
	m.active.HasLastFrame = true

	now2 := now.Add(10 * time.Second)
	res2 := mustClaim(t, m, now2, "gs_aaa", "dev-1")
	if !res2.Reconnected || res2.Fresh || res2.Replaced {
		t.Fatalf("reconnect flags = fresh=%v reconnected=%v replaced=%v, want reconnected only", res2.Fresh, res2.Reconnected, res2.Replaced)
	}
	if res2.Session.Generation != res1.Session.Generation {
		t.Fatalf("generation changed on reconnect: %d -> %d", res1.Session.Generation, res2.Session.Generation)
	}
	if !reflect.DeepEqual(res2.Session.LastFrame, frame) {
		t.Fatalf("lastFrame not preserved on reconnect: got %+v want %+v", res2.Session.LastFrame, frame)
	}
}

// TestClaimReplaceSession verifies a different session replaces the active one
// and that the replaced session cannot reclaim within its token lifetime.
func TestClaimReplaceSession(t *testing.T) {
	m := newSessionManager()
	now := testTime()

	mustClaim(t, m, now, "gs_old", "dev-1")
	res := mustClaim(t, m, now.Add(1*time.Second), "gs_new", "dev-2")

	if !res.Replaced || !res.Fresh {
		t.Fatalf("replace flags = fresh=%v replaced=%v, want both", res.Fresh, res.Replaced)
	}
	if res.Previous == nil || res.Previous.SessionID != "gs_old" {
		t.Fatalf("previous = %+v, want gs_old", res.Previous)
	}
	if res.Previous.Status != statusReplaced {
		t.Fatalf("previous status = %q, want replaced", res.Previous.Status)
	}
	if res.Session.SessionID != "gs_new" || res.Session.Generation != 2 {
		t.Fatalf("new session = %+v, want gs_new generation 2", res.Session)
	}

	// The replaced old session must be rejected, not ping-pong back.
	rejected := m.claim(now.Add(2*time.Second), "gs_old", "dev-1", nil)
	if !rejected.Rejected {
		t.Fatalf("replaced session claim was not rejected: %+v", rejected)
	}
}

// TestFormalOfflineAfterGrace verifies the timer check formally offline a
// session idle past ReconnectGrace and that a later same-session claim starts
// fresh without inheriting lastFrame.
func TestFormalOfflineAfterGrace(t *testing.T) {
	m := newSessionManager()
	now := testTime()

	mustClaim(t, m, now, "gs_aaa", "dev-1")
	m.active.LastFrame = gen.GlassRenderFrame{Text: "stale frame"}
	m.active.HasLastFrame = true

	// Activity within grace keeps the session online.
	if tr := m.check(now.Add(ReconnectGrace - time.Second)); tr != nil {
		t.Fatalf("check within grace produced transition %+v", tr)
	}
	// Exactly at the deadline the session formally goes offline.
	tr := m.check(now.Add(ReconnectGrace))
	if tr == nil {
		t.Fatal("check at grace deadline produced no offline transition")
	}
	if tr.Kind != eventOffline {
		t.Fatalf("transition kind = %q, want %q", tr.Kind, eventOffline)
	}
	if tr.Session.Status != statusOffline {
		t.Fatalf("session status after check = %q, want offline", tr.Session.Status)
	}

	// A claim after formal offline is a fresh session: no lastFrame restore.
	res := mustClaim(t, m, now.Add(ReconnectGrace+time.Second), "gs_aaa", "dev-1")
	if !res.Fresh {
		t.Fatalf("post-offline claim not fresh")
	}
	if res.Session.Generation != 2 {
		t.Fatalf("post-offline generation = %d, want 2 (advanced)", res.Session.Generation)
	}
	if res.Session.HasLastFrame {
		t.Fatal("post-offline session inherited lastFrame, want none")
	}
}

// TestSameSessionFreshAfterReplacedTerminal ensures a replaced session is
// terminal: even after the active session goes offline, a claim from the
// replaced session stays rejected.
func TestReplacedStaysRejected(t *testing.T) {
	m := newSessionManager()
	now := testTime()

	mustClaim(t, m, now, "gs_old", "dev-1")
	mustClaim(t, m, now.Add(time.Second), "gs_new", "dev-2")

	rejected := m.claim(now.Add(2*time.Second), "gs_old", "dev-1", nil)
	if !rejected.Rejected {
		t.Fatalf("replaced session reclaimed after replacement: %+v", rejected)
	}
}

// TestCheckNoSession returns no transition when idle.
func TestCheckNoSession(t *testing.T) {
	m := newSessionManager()
	if tr := m.check(testTime()); tr != nil {
		t.Fatalf("check with no session produced %+v", tr)
	}
	if !m.nextDeadline().IsZero() {
		t.Fatal("nextDeadline with no session is not zero")
	}
}

// TestNextDeadline tracks the reconnect window.
func TestNextDeadline(t *testing.T) {
	m := newSessionManager()
	now := testTime()
	mustClaim(t, m, now, "gs_aaa", "dev-1")
	deadline := m.nextDeadline()
	want := now.Add(ReconnectGrace)
	if !deadline.Equal(want) {
		t.Fatalf("nextDeadline = %v, want %v", deadline, want)
	}
}

// TestPersistRoundtrip verifies durable state survives export/import and that
// lastFrame is restored for a session that was online at shutdown.
func TestPersistRoundtrip(t *testing.T) {
	m := newSessionManager()
	now := testTime()

	res := mustClaim(t, m, now, "gs_aaa", "dev-1")
	frame := gen.GlassRenderFrame{Text: "persisted frame", Layout: "top"}
	m.active.LastFrame = frame
	m.active.HasLastFrame = true
	m.active.Capabilities = []gen.GlassCapability{{Name: "native_text"}}

	active, genCounter, replaced := m.export()
	d := durableState{GenCounter: genCounter, Replaced: replaced}
	if active != nil {
		d.Active = &sessionDurable{
			SessionID: active.SessionID, DeviceID: active.DeviceID,
			Generation: active.Generation, Status: string(active.Status),
			LastSeen: active.LastSeen, LastFrame: active.LastFrame,
			HasLastFrame: active.HasLastFrame, Capabilities: active.Capabilities,
		}
	}

	// Simulate process restart: restore with a fresh clock.
	m2 := newSessionManager()
	restart := now.Add(5 * time.Second)
	m2.importState(restart, d)

	if m2.active == nil {
		t.Fatal("restored session is nil")
	}
	if m2.active.SessionID != "gs_aaa" || m2.active.Generation != res.Session.Generation {
		t.Fatalf("restored identity = %+v", m2.active)
	}
	if !reflect.DeepEqual(m2.active.LastFrame, frame) || !m2.active.HasLastFrame {
		t.Fatalf("restored lastFrame = %+v has=%v, want %+v true", m2.active.LastFrame, m2.active.HasLastFrame, frame)
	}
	if m2.active.Status != statusOnline {
		t.Fatalf("restored status = %q, want online (fresh grace window)", m2.active.Status)
	}

	// A same-session claim after restart within grace reconnects and restores.
	rc := mustClaim(t, m2, restart.Add(10*time.Second), "gs_aaa", "dev-1")
	if !rc.Reconnected {
		t.Fatalf("restart reconnect not flagged reconnected")
	}
	if !reflect.DeepEqual(rc.Session.LastFrame, frame) {
		t.Fatalf("restart reconnect lost lastFrame: got %+v", rc.Session.LastFrame)
	}
}

// TestCapabilitiesReasserted verifies claim updates capabilities.
func TestCapabilitiesReasserted(t *testing.T) {
	m := newSessionManager()
	now := testTime()
	caps := []gen.GlassCapability{{Name: "audio", Version: "1"}}

	mustClaim(t, m, now, "gs_aaa", "dev-1")
	res := m.claim(now.Add(time.Second), "gs_aaa", "dev-1", caps)
	if res.Rejected || res.Session == nil {
		t.Fatalf("second claim rejected: %+v", res)
	}
	if len(res.Session.Capabilities) != 1 || res.Session.Capabilities[0].Name != "audio" {
		t.Fatalf("capabilities not re-asserted: %+v", res.Session.Capabilities)
	}
}
