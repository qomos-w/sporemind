package desktop

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// These tests pin the overlay suppression contract: while an HTML overlay is
// open (HideAllBrowserWindows raised by the frontend BrowserOverlayManager),
// no browser child window may become visible — including one that is created
// and first-painted during that window (the boot-time tab restore racing the
// crash overlay). The last overlay closing (ShowAllBrowserWindows) restores
// the sessions' wanted visibility.
//
// The native Show/Hide calls require a live Wails app, so the decision logic is
// extracted into the pure browserSessionShouldShow and unit-tested here, while
// the flag plumbing is driven through the App methods (zero-value
// *application.WebviewWindow tolerates Hide/Show without a live app).

func TestBrowserSessionShouldShow(t *testing.T) {
	cases := []struct {
		name                              string
		wanted, active, resizing, overlay bool
		want                              bool
	}{
		{"all clear -> show", true, true, false, false, true},
		{"not wanted -> hide", false, true, false, false, false},
		{"main window inactive -> hide", true, false, false, false, false},
		{"resizing -> hide", true, true, true, false, false},
		{"overlay open -> hide", true, true, false, true, false},
		{"overlay open beats every other signal", true, true, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := browserSessionShouldShow(tc.wanted, tc.active, tc.resizing, tc.overlay)
			if got != tc.want {
				t.Errorf("browserSessionShouldShow(%v,%v,%v,%v) = %v, want %v",
					tc.wanted, tc.active, tc.resizing, tc.overlay, got, tc.want)
			}
		})
	}
}

// TestOverlaySuppressionLifecycle pins the flag plumbing: the first overlay
// opening raises the suppression, the last one closing clears it.
func TestOverlaySuppressionLifecycle(t *testing.T) {
	app := newTestApp()

	if app.browserOverlaySuppressed {
		t.Fatal("suppression must be off before any overlay opens")
	}

	app.HideAllBrowserWindows()
	if !app.browserOverlaySuppressed {
		t.Fatal("HideAllBrowserWindows must raise the overlay suppression")
	}

	app.ShowAllBrowserWindows()
	if app.browserOverlaySuppressed {
		t.Fatal("ShowAllBrowserWindows must clear the overlay suppression")
	}
}

// TestOverlaySuppressionBlocksFirstPaint is the G1 regression: a session that
// was created (wanted=true) and then first-paints while the overlay is open
// must stay hidden — its first paint must not reveal a native window over the
// overlay. Without the suppression flag this paint would Show() the window.
func TestOverlaySuppressionBlocksFirstPaint(t *testing.T) {
	app := newTestApp()
	app.mainWindowActive = true

	s := &browserSession{id: "s1", window: &application.WebviewWindow{}, wanted: true}
	app.browserSessions["s1"] = s

	// Overlay opens: every existing session is hidden and suppression raised.
	app.HideAllBrowserWindows()
	if s.shown {
		t.Fatal("session must be hidden while the overlay is open")
	}

	// The restored tab finishes loading and reports its first paint.
	app.markSessionPainted("s1")

	if !s.painted {
		t.Fatal("markSessionPainted must record the painted state")
	}
	if s.shown {
		t.Fatal("a session that first-paints while the overlay is open must stay hidden")
	}
}

// TestDestroyAllBrowserWindowsClearsOverlaySuppression pins the stale-flag
// guard: tearing down every session must drop the suppression, otherwise a
// frontend reload while the crash overlay was open would leave every future
// session hidden with no overlay to restore.
func TestDestroyAllBrowserWindowsClearsOverlaySuppression(t *testing.T) {
	app := newTestApp()
	app.HideAllBrowserWindows()
	if !app.browserOverlaySuppressed {
		t.Fatal("setup: suppression must be raised")
	}

	app.DestroyAllBrowserWindows()

	if app.browserOverlaySuppressed {
		t.Fatal("DestroyAllBrowserWindows must clear the overlay suppression")
	}
}

// TestCrashSuppressorWiring pins the live-crash path (G4): the panic handler
// invokes the App-supplied suppressor so the native browser child windows are
// hidden before the crash UI is presented.
func TestCrashSuppressorWiring(t *testing.T) {
	calls := 0
	SetCrashBrowserSuppressor(func() { calls++ })
	t.Cleanup(func() { SetCrashBrowserSuppressor(nil) })

	suppressCrashBrowserWindows()

	if calls != 1 {
		t.Fatalf("suppressor invoked %d times, want 1", calls)
	}
}
