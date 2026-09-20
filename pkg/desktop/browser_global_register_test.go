package desktop

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/actor/browserinstance"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// These tests cover the Global-tab registration path: createBrowserSessionLocked
// registers the kind="global" session's WebView2 window in desktopWindowOperator
// under the stable "global" key (registerGlobalWindowLocked), session destruction
// either hands the key to a surviving global session or drops it
// (refreshGlobalWindowRegistrationLocked), and handleBrowserMessage forwards
// the operator's observe/use postMessages so browsermanager.use can automate the
// Global tab.

// fakeExternalWindow is an ExternalWindow that is NOT a *application.WebviewWindow.
// RegisterExternal must reject it with a type error instead of storing it.
type fakeExternalWindow struct{}

func (f fakeExternalWindow) ExecJS(script string) {}
func (f fakeExternalWindow) Close()               {}

func TestOperatorRegisterExternal(t *testing.T) {
	op := newDesktopWindowOperator(nil, nil, nil)

	win := &application.WebviewWindow{}
	if err := op.RegisterExternal(globalWindowKey, win); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}
	bw, ok := op.windows[globalWindowKey]
	if !ok || bw == nil {
		t.Fatal("expected window registered under the global key")
	}
	if bw.win != win {
		t.Errorf("registered window = %p, want %p", bw.win, win)
	}

	// A non-Wails ExternalWindow must be rejected and must not replace tracking.
	bad := fakeExternalWindow{}
	if err := op.RegisterExternal(globalWindowKey, bad); err == nil {
		t.Error("expected error for non-WebviewWindow ExternalWindow")
	}
	bw, _ = op.windows[globalWindowKey]
	if bw.win != win {
		t.Errorf("failed register must not clobber existing entry, got window %p", bw.win)
	}

	// RemoveWindow drops tracking without touching the window handle.
	op.RemoveWindow(globalWindowKey)
	if _, ok := op.windows[globalWindowKey]; ok {
		t.Error("expected registration removed after RemoveWindow")
	}
	op.RemoveWindow(globalWindowKey) // idempotent
}

func TestGlobalWindowRegistration(t *testing.T) {
	app := newTestApp()
	op := newDesktopWindowOperator(nil, nil, nil)
	app.browserOp = op

	winA := &application.WebviewWindow{}
	winB := &application.WebviewWindow{}

	// Non-global sessions are never registered under the global key.
	app.registerGlobalWindowLocked(&browserSession{id: "inst-1", kind: "independent", window: winA})
	if _, ok := op.windows[globalWindowKey]; ok {
		t.Error("independent session must not be registered as global")
	}

	// A global session registers its window under the stable key.
	app.registerGlobalWindowLocked(&browserSession{id: "global-agent", kind: "global", window: winA})
	bw, ok := op.windows[globalWindowKey]
	if !ok || bw.win != winA {
		t.Fatalf("global window not registered: ok=%v win=%p want=%p", ok, bw.win, winA)
	}

	// The most recently created global session wins the key.
	app.registerGlobalWindowLocked(&browserSession{id: "global-2", kind: "global", window: winB})
	bw, _ = op.windows[globalWindowKey]
	if bw.win != winB {
		t.Errorf("latest global session must win the key, got window %p want %p", bw.win, winB)
	}

	// After the newest global session is destroyed, a surviving global session
	// takes over the key.
	app.browserSessions["global-agent"] = &browserSession{id: "global-agent", kind: "global", window: winA}
	app.refreshGlobalWindowRegistrationLocked()
	bw, _ = op.windows[globalWindowKey]
	if bw.win != winA {
		t.Errorf("surviving global session must regain the key, got window %p want %p", bw.win, winA)
	}

	// With no global session left the registration is dropped.
	delete(app.browserSessions, "global-agent")
	app.refreshGlobalWindowRegistrationLocked()
	if _, ok := op.windows[globalWindowKey]; ok {
		t.Error("expected the global registration removed when no global session survives")
	}
}

// TestIndependentWindowRegistration pins the %-mount crawl surface:
// independent right-panel session windows (browsermanager tab-mode instances,
// the browser-chat:<instanceId> mount targets) register in the operator under
// their instance ID so browsermanager.use — and the crawl engine — can drive
// them; CloseBrowserSession drops the registration.
func TestIndependentWindowRegistration(t *testing.T) {
	app := newTestApp()
	op := newDesktopWindowOperator(nil, nil, nil)
	app.browserOp = op

	win := &application.WebviewWindow{}
	app.browserSessions["inst-7"] = &browserSession{id: "inst-7", kind: "independent", window: win}
	if err := op.RegisterExternal("inst-7", win); err != nil {
		t.Fatalf("RegisterExternal(inst-7): %v", err)
	}
	bw, ok := op.windows["inst-7"]
	if !ok || bw.win != win {
		t.Fatalf("independent window not registered under its instance id: ok=%v win=%p want=%p", ok, bw.win, win)
	}

	// Closing the session drops the operator registration; the persisted
	// instance record survives (tab close only hides).
	app.browserSessions["inst-7"].window = win
	app.CloseBrowserSession("inst-7")
	if _, ok := op.windows["inst-7"]; ok {
		t.Error("expected the independent registration removed on session close")
	}
	if _, ok := app.browserSessions["inst-7"]; ok {
		t.Error("expected the session removed on close")
	}
}

func TestHandleBrowserMessageForwardsOperatorResponses(t *testing.T) {
	app := newTestApp()
	op := newDesktopWindowOperator(nil, nil, nil)
	app.browserOp = op

	t.Run("browser-observe", func(t *testing.T) {
		ch := make(chan *domain.BrowserPageObservation, 1)
		op.mu.Lock()
		op.observeCallbacks["obs-global-1"] = ch
		op.mu.Unlock()

		msg := `{"type":"browser-observe","obsId":"obs-global-1","url":"https://example.com","title":"Example","loadState":"complete","timestamp":"t","historyLength":2,"viewportWidth":800,"viewportHeight":600,"scrollX":0,"scrollY":0,"totalElementCount":1,"elements":[]}`
		app.handleBrowserMessage("global-agent", msg)

		select {
		case obs := <-ch:
			if obs == nil || obs.URL != "https://example.com" {
				t.Errorf("forwarded observation = %+v, want url https://example.com", obs)
			}
		default:
			t.Error("browser-observe message was not forwarded to the operator callback")
		}
	})

	t.Run("browser-use-result", func(t *testing.T) {
		useCalled := false
		cancel := op.snapshots.RegisterCallback("use-global-7", func(r BrowserUseResult) {
			useCalled = true
		})
		defer cancel()

		msg := `{"type":"browser-use-result","obsId":"use-global-7","success":true,"message":"clicked e1"}`
		app.handleBrowserMessage("global-agent", msg)
		if !useCalled {
			t.Error("browser-use-result message was not forwarded to the operator callback")
		}
	})

	t.Run("page-state not hijacked", func(t *testing.T) {
		ch := make(chan *domain.BrowserPageObservation, 1)
		op.mu.Lock()
		op.observeCallbacks["obs-ps"] = ch
		op.mu.Unlock()

		// A page-state postMessage must not be consumed as an observe response.
		app.handleBrowserMessage("global-agent", `{"type":"page-state","scrollX":12,"scrollY":34}`)
		op.mu.Lock()
		_, stillPending := op.observeCallbacks["obs-ps"]
		op.mu.Unlock()
		if !stillPending {
			t.Error("page-state message consumed the browser-observe forwarding path")
		}
	})
}

var _ browserinstance.ExternalWindow = fakeExternalWindow{}
