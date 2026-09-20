package desktop

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// These tests cover contract §4: NavigateBrowserWindow must, when the
// session or its window is missing, create the session and then load the URL
// (equivalent to the Open path) instead of silently returning — and must never
// leave a dangling host pending.
//
// The native window-creation path (createBrowserSessionLocked →
// a.app.Window.NewWithOptions) requires a live Wails app and is exercised at the
// integration level. The *decision* logic it is built on — resolveNavigateLocked
// (create-vs-navigate routing + kind/bounds derivation) and resolveCreateKind
// (global/independent/default) — is pure and unit-tested here, including the
// "no leftover pending" invariant: the decision must not begin a host pending.

func newTestApp() *App {
	return &App{browserSessions: make(map[string]*browserSession)}
}

// TestResolveCreateKind covers the kind resolution for an auto-created session
// (contract §4): retained kind wins, then persisted independent config, else the
// shared global tab.
func TestResolveCreateKind(t *testing.T) {
	cases := []struct {
		name                 string
		prev                 *browserSession
		hasIndependentConfig bool
		want                 string
	}{
		{"no signals -> global default", nil, false, "global"},
		{"persisted independent config", nil, true, "independent"},
		{"retained global kind", &browserSession{kind: "global"}, true, "global"},
		{"retained independent kind", &browserSession{kind: "independent"}, false, "independent"},
		{"retained app kind", &browserSession{kind: "app"}, true, "app"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveCreateKind(tc.prev, tc.hasIndependentConfig); got != tc.want {
				t.Fatalf("resolveCreateKind = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveNavigateCreateGlobal covers a missing (global) session: Navigate must
// create with kind "global" and default bounds. No browsermanager / no persisted
// config is wired in the test App, so the default resolves to "global".
func TestResolveNavigateCreateGlobal(t *testing.T) {
	app := newTestApp()
	// Session not present at all.
	action := app.resolveNavigateLocked("session-1", "https://example.com", nil)
	if !action.create {
		t.Fatal("missing session: action.create = false, want true (contract §4)")
	}
	if action.kind != "global" {
		t.Fatalf("missing session: kind = %q, want global", action.kind)
	}
	if action.sameURL {
		t.Fatal("create action must not flag sameURL")
	}
	if action.width != defaultBrowserWidth || action.height != defaultBrowserHeight {
		t.Fatalf("create bounds = %dx%d, want %dx%d", action.width, action.height, defaultBrowserWidth, defaultBrowserHeight)
	}
}

// TestResolveNavigateCreateIndependent covers an independent session whose window
// was destroyed but whose state survived: Navigate must recreate it, preserving
// its kind ("independent") and retained document.
func TestResolveNavigateCreateIndependent(t *testing.T) {
	app := newTestApp()
	prev := &browserSession{
		id:           "inst-1",
		kind:         "independent",
		confirmedURL: "https://retained.example.com",
	}
	action := app.resolveNavigateLocked("inst-1", "https://new.example.com", prev)
	if !action.create {
		t.Fatal("nil-window session: action.create = false, want true (contract §4)")
	}
	if action.kind != "independent" {
		t.Fatalf("recreate kind = %q, want independent", action.kind)
	}
}

// TestResolveNavigateExistingWindow covers an existing window: Navigate must NOT
// create, and must pick Reload for the same document vs SetURL for a different
// one.
func TestResolveNavigateExistingWindow(t *testing.T) {
	app := newTestApp()
	s := &browserSession{
		id:           "session-1",
		kind:         "global",
		window:       &application.WebviewWindow{},
		confirmedURL: "https://example.com/a",
	}

	// Same document (trailing slash normalised) -> Reload.
	action := app.resolveNavigateLocked("session-1", "https://example.com/a/", s)
	if action.create {
		t.Fatal("existing window: action.create = true, want false")
	}
	if !action.sameURL {
		t.Fatal("same document: sameURL = false, want true (Reload path)")
	}

	// Different document -> SetURL.
	action = app.resolveNavigateLocked("session-1", "https://other.example.com", s)
	if action.create {
		t.Fatal("existing window: action.create = true, want false")
	}
	if action.sameURL {
		t.Fatal("different document: sameURL = true, want false (SetURL path)")
	}
}

// TestResolveNavigateDoesNotLeakPending is the "no leftover pending" guard
// (contract §4 / §2.5.1). The decision must not begin a host pending on ANY
// branch — beginHostNavigation is the Navigate caller's job, done only when a
// native primitive will actually fire. The old code began an address-bar pending
// on a nil-window session and then silently returned, stranding it; this test
// pins that the decision leaves the session untouched.
func TestResolveNavigateDoesNotLeakPending(t *testing.T) {
	app := newTestApp()

	// nil-window session: previously the leak vector.
	prev := &browserSession{id: "session-1", kind: "global"}
	_ = app.resolveNavigateLocked("session-1", "https://example.com", prev)
	if prev.pendingNavigation != nil {
		t.Fatal("resolveNavigateLocked must not allocate a pending (nil-window case); got a leaked pending")
	}
	if prev.navigationStatus == navStatusLoading {
		t.Fatal("resolveNavigateLocked must not flip status to loading (nil-window case)")
	}

	// Existing window: also must not begin the pending here.
	s := &browserSession{
		id:           "session-2",
		window:       &application.WebviewWindow{},
		confirmedURL: "https://example.com",
	}
	_ = app.resolveNavigateLocked("session-2", "https://example.com/x", s)
	if s.pendingNavigation != nil {
		t.Fatal("resolveNavigateLocked must not allocate a pending (existing-window case); the caller issues it")
	}

	// Entirely missing session: nothing to mutate.
	_ = app.resolveNavigateLocked("session-3", "https://example.com", nil)
}
