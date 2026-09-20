package desktop

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// TestWebViewNavigationEventsContract pins the event-constant contract that the
// Wails fork (§11.A) exposes and that sporemind depends on: a distinct, assigned
// NavigationStarting event alongside the existing NavigationCompleted event.
// If the fork regresses (drops/renumber the event), this test fails before
// runtime.
func TestWebViewNavigationEventsContract(t *testing.T) {
	starting := events.Windows.WebViewNavigationStarting
	completed := events.Windows.WebViewNavigationCompleted

	if uint(starting) == 0 {
		t.Fatal("events.Windows.WebViewNavigationStarting must be assigned a non-zero ID")
	}
	if starting == completed {
		t.Fatal("WebViewNavigationStarting must be a distinct event from WebViewNavigationCompleted")
	}
}

// TestNavigationContextGettersContract pins the navigation payload getter API
// (§11.A / §2.5) that the host consumes from WindowEventContext. A payload-less
// context must yield safe zero-value defaults so legacy event handlers are
// unaffected, while NavStarted defaults to true.
func TestNavigationContextGettersContract(t *testing.T) {
	// A zero-value context mirrors the "no payload" case.
	var ctx application.WindowEventContext

	if ctx.NavigationID() != 0 {
		t.Errorf("NavigationID zero default = %d, want 0", ctx.NavigationID())
	}
	if ctx.URL() != "" {
		t.Errorf("URL zero default = %q, want empty", ctx.URL())
	}
	if ctx.CommandToken() != 0 {
		t.Errorf("CommandToken zero default = %d, want 0", ctx.CommandToken())
	}
	if ctx.IsSuccess() {
		t.Error("IsSuccess zero default = true, want false")
	}
	if ctx.WebErrorStatus() != 0 {
		t.Errorf("WebErrorStatus zero default = %d, want 0", ctx.WebErrorStatus())
	}
	if !ctx.NavStarted() {
		t.Error("NavStarted zero default = false, want true (payload-less events unaffected)")
	}

	// Compile-time pin: every getter the host relies on is reachable.
	var _ uint64 = ctx.NavigationID()
	var _ uint64 = ctx.CommandToken()
	var _ string = ctx.URL()
	var _ bool = ctx.IsRedirected()
	var _ bool = ctx.IsUserInitiated()
	var _ bool = ctx.IsSuccess()
	var _ int32 = ctx.WebErrorStatus()
}
