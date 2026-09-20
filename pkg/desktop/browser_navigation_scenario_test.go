package desktop

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// These tests verify the browser navigation state machine end-to-end across the
// twelve contract scenarios listed in [[验证浏览器重构端到端行为]]. Each test
// stitches the full lifecycle (beginHostNavigation → onNavigationStarting →
// commitNavigation → browserStatePayload, plus acceptInjectedMeta for stale
// events) to confirm the contract invariants hold for realistic event
// sequences, not just isolated methods.
//
// They exercise the pure browserSession methods directly — the only layer that
// is unit-testable without a live WebView2 window (createBrowserSessionLocked
// needs a Wails app). The app.go wiring of these methods is reviewed statically
// and covered by browser_navigate_create_test.go for the create/routing decisions.

// freshSession returns a browserSession seeded with an already-committed first
// document, so subsequent scenarios start from a realistic "loaded" baseline.
func freshSession(t *testing.T, url string) *browserSession {
	t.Helper()
	s := &browserSession{}
	s.beginHostNavigation(navSourceOpen, url)
	s.onNavigationStarting(1, 0, false)
	if ok := s.commitNavigation(1, url, true, 0); !ok {
		t.Fatalf("seed commit failed for %q", url)
	}
	if s.navigationStatus != navStatusLoaded {
		t.Fatalf("seed status = %q, want loaded", s.navigationStatus)
	}
	return s
}

func stateStatus(s *browserSession) string {
	p := s.browserStatePayload()
	return p["status"]
}

// --- 1. 首次打开 (first open) ---

func TestScenarioFirstOpen(t *testing.T) {
	s := &browserSession{}
	// Open path allocates an open-sourced pending and flips to loading.
	s.beginHostNavigation(navSourceOpen, "https://example.com")
	if s.navigationStatus != navStatusLoading {
		t.Fatalf("after open begin: status = %q, want loading", s.navigationStatus)
	}
	p := s.browserStatePayload()
	if p["status"] != "loading" {
		t.Fatalf("state payload status = %q, want loading", p["status"])
	}
	if p["confirmedURL"] != "" {
		t.Fatalf("pre-commit confirmedURL = %q, want empty (no optimistic write)", p["confirmedURL"])
	}

	// NewWithOptions initial nav may carry commandToken 0 (no tokenized command).
	s.onNavigationStarting(10, 0, false)
	// Commit the initial document.
	if ok := s.commitNavigation(10, "https://example.com", true, 0); !ok {
		t.Fatal("first-open commit must succeed")
	}
	if s.confirmedURL != "https://example.com" {
		t.Fatalf("confirmedURL = %q", s.confirmedURL)
	}
	if stateStatus(s) != "loaded" {
		t.Fatalf("status = %q, want loaded", stateStatus(s))
	}
}

// --- 2. 空白新标签 (blank new tab) ---
//
// The first address-bar submit from a blank tab must create the session (routing
// decision) and then load the URL as an open-source navigation. The pure decision
// (resolveNavigateLocked create vs navigate) is covered by
// browser_navigate_create_test.go; this test confirms the resulting open lifecycle
// loads the URL correctly.

func TestScenarioBlankNewTabCreatesAndLoads(t *testing.T) {
	app := newTestApp()
	// No session exists — Navigate must decide to create.
	action := app.resolveNavigateLocked("global-1", "https://blank.example.com", nil)
	if !action.create {
		t.Fatal("blank tab Navigate: expected create decision")
	}
	if action.kind != "global" {
		t.Fatalf("blank global tab kind = %q, want global", action.kind)
	}

	// Simulate the create-then-load lifecycle the create path performs
	// (beginHostNavigation(navSourceOpen, url) inside createBrowserSessionLocked).
	s := &browserSession{id: "global-1", kind: action.kind}
	s.beginHostNavigation(navSourceOpen, "https://blank.example.com")
	s.onNavigationStarting(1, 0, false)
	if ok := s.commitNavigation(1, "https://blank.example.com", true, 0); !ok {
		t.Fatal("blank-tab load commit must succeed")
	}
	if s.confirmedURL != "https://blank.example.com" {
		t.Fatalf("confirmedURL = %q", s.confirmedURL)
	}
}

// --- 3. 新 URL (navigate to a new URL) ---

func TestScenarioNewURL(t *testing.T) {
	s := freshSession(t, "https://example.com/a")
	// Address-bar submit to a new URL.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/b")
	s.onNavigationStarting(20, 1, false) // tokenized SetURL command
	if stateStatus(s) != "loading" {
		t.Fatalf("status = %q, want loading during navigation", stateStatus(s))
	}
	// During loading confirmedURL is still the previous document (no optimistic write).
	if s.browserStatePayload()["confirmedURL"] != "https://example.com/a" {
		t.Fatalf("confirmedURL changed during loading")
	}
	if ok := s.commitNavigation(20, "https://example.com/b", true, 0); !ok {
		t.Fatal("new-URL commit must succeed")
	}
	if s.confirmedURL != "https://example.com/b" {
		t.Fatalf("confirmedURL = %q, want /b", s.confirmedURL)
	}
}

// --- 4. 同 URL 刷新 (same-URL reload) ---

func TestScenarioSameURLReload(t *testing.T) {
	s := freshSession(t, "https://example.com/a")
	// Reload of the same document: the decision uses sameDocument normalisation.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/a")
	s.onNavigationStarting(30, 1, false)
	// Reload commits the same URL.
	if ok := s.commitNavigation(30, "https://example.com/a", true, 0); !ok {
		t.Fatal("reload commit must succeed")
	}
	if s.confirmedURL != "https://example.com/a" {
		t.Fatalf("confirmedURL = %q, want /a", s.confirmedURL)
	}
	if stateStatus(s) != "loaded" {
		t.Fatalf("status = %q, want loaded after reload", stateStatus(s))
	}
}

// --- 5. 重定向 (redirect) ---
//
// A redirect hop shares the active navigation's NavId and must NOT start a new
// epoch or supersede. The final commit carries the post-redirect URL, which
// becomes confirmedURL even though it differs from the requested URL.

func TestScenarioRedirect(t *testing.T) {
	s := freshSession(t, "https://example.com/old")
	s.beginHostNavigation(navSourceAddressBar, "https://short.example")
	s.onNavigationStarting(40, 1, false) // host command starts
	pendingBefore := s.pendingNavigation

	// Redirect hop (same NavId, IsRedirected): no-op.
	s.onNavigationStarting(40, 0, true)
	if s.pendingNavigation != pendingBefore {
		t.Fatal("redirect hop must not replace the pending")
	}
	if s.pendingNavigation.superseded {
		t.Fatal("redirect hop must not supersede the pending")
	}
	// nativeNavID stays bound to the host command's NavId.
	if s.nativeNavID != 40 {
		t.Fatalf("nativeNavID = %d, want 40", s.nativeNavID)
	}

	// Commit carries the post-redirect final URL.
	if ok := s.commitNavigation(40, "https://long.example/page", true, 0); !ok {
		t.Fatal("redirect commit must succeed")
	}
	if s.confirmedURL != "https://long.example/page" {
		t.Fatalf("confirmedURL = %q, want post-redirect URL", s.confirmedURL)
	}
	if stateStatus(s) != "loaded" {
		t.Fatalf("status = %q, want loaded after redirect", stateStatus(s))
	}
}

// --- 6. 失败 (navigation failure) ---

func TestScenarioFailureKeepsLastGood(t *testing.T) {
	s := freshSession(t, "https://example.com/good")
	s.beginHostNavigation(navSourceAddressBar, "https://unreachable.example")
	s.onNavigationStarting(50, 1, false)
	if ok := s.commitNavigation(50, "https://unreachable.example", false, -271); !ok {
		t.Fatal("failed-nav commit must still return true (it is the active navigation)")
	}
	if s.confirmedURL != "https://example.com/good" {
		t.Fatalf("confirmedURL = %q, want last good value on failure", s.confirmedURL)
	}
	if s.navigationStatus != navStatusFailed {
		t.Fatalf("status = %q, want failed", s.navigationStatus)
	}
	p := s.browserStatePayload()
	if p["status"] != "failed" {
		t.Fatalf("state status = %q, want failed", p["status"])
	}
	if p["error"] == "" {
		t.Fatal("state payload must carry an error on failure")
	}
}

// --- 7. 页面内跳转 (page-initiated navigation) ---

func TestScenarioInPageNavigation(t *testing.T) {
	s := freshSession(t, "https://example.com/a")
	// A link click on the page fires NavigationStarting with commandToken == 0.
	s.onNavigationStarting(60, 0, false)
	if s.pendingNavigation == nil {
		t.Fatal("in-page navigation must allocate a pending")
	}
	if s.pendingNavigation.source != navSourceInPage {
		t.Fatalf("source = %q, want in-page", s.pendingNavigation.source)
	}
	if stateStatus(s) != "loading" {
		t.Fatalf("status = %q, want loading for in-page nav", stateStatus(s))
	}
	if ok := s.commitNavigation(60, "https://example.com/page2", true, 0); !ok {
		t.Fatal("in-page commit must succeed")
	}
	if s.confirmedURL != "https://example.com/page2" {
		t.Fatalf("confirmedURL = %q, want /page2", s.confirmedURL)
	}
}

// --- 8. 后退/前进 (back/forward) ---

func TestScenarioBackForwardCommit(t *testing.T) {
	s := freshSession(t, "https://example.com/a")
	// Navigate forward to /b so there is history.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/b")
	s.onNavigationStarting(70, 1, false)
	if ok := s.commitNavigation(70, "https://example.com/b", true, 0); !ok {
		t.Fatal("forward commit must succeed")
	}

	// Back: requestedURL is empty (unknown target), but the commit carries the
	// back-target URL.
	s.beginHostNavigation(navSourceBackForward, "")
	if s.pendingNavigation.requestedURL != "" {
		t.Fatalf("back-forward requestedURL = %q, want empty", s.pendingNavigation.requestedURL)
	}
	s.onNavigationStarting(71, 2, false) // tokenized GoBack command
	if ok := s.commitNavigation(71, "https://example.com/a", true, 0); !ok {
		t.Fatal("back commit must succeed")
	}
	if s.confirmedURL != "https://example.com/a" {
		t.Fatalf("confirmedURL = %q, want /a (back target)", s.confirmedURL)
	}
}

// Back/forward when there is no history: NavStarted == false must resolve to the
// prior loaded document, not strand loading.

func TestScenarioBackNoHistoryResolvesIdle(t *testing.T) {
	s := freshSession(t, "https://example.com/a")
	s.beginHostNavigation(navSourceBackForward, "")
	s.resolveNoNavigation()
	if s.pendingNavigation != nil {
		t.Fatal("pending must be cleared after no-nav resolve")
	}
	if s.navigationStatus != navStatusLoaded {
		t.Fatalf("status = %q, want loaded (prior confirmed document)", s.navigationStatus)
	}
}

// --- 9. 标签切换 (tab switching) ---
//
// Tab switching is an AIShellLayout concern (it hides inactive windows). At the
// state-machine level the contract is that sessions are fully independent: the
// visibility/wanted flag does not interact with navigation state. This test
// confirms a wanted=false session still commits navigation results correctly.

func TestScenarioTabSwitchDoesNotBreakNavigation(t *testing.T) {
	s := freshSession(t, "https://example.com/a")
	s.wanted = false // hidden (background tab)
	// A background tab can still receive a page-initiated navigation that commits.
	s.onNavigationStarting(80, 0, false)
	if ok := s.commitNavigation(80, "https://example.com/bg", true, 0); !ok {
		t.Fatal("background-tab commit must succeed")
	}
	if s.confirmedURL != "https://example.com/bg" {
		t.Fatalf("confirmedURL = %q, want /bg", s.confirmedURL)
	}
	// Visibility is separate from navigation status.
	if stateStatus(s) != "loaded" {
		t.Fatalf("status = %q, want loaded", stateStatus(s))
	}
	if s.wanted {
		t.Fatal("wanted must remain false — visibility is independent of navigation")
	}
}

// --- 10. global agent 路由 (global agent routing) ---
//
// An agent open_global call with no persisted independent config must resolve to
// the shared global kind so it reuses the workspace browser context, never
// silently dropping or creating an independent instance.

func TestScenarioGlobalAgentRouting(t *testing.T) {
	app := newTestApp()
	// Agent triggers Navigate on a session that does not exist.
	action := app.resolveNavigateLocked("global-agent", "https://agent.example.com", nil)
	if !action.create {
		t.Fatal("agent Navigate on missing session must create (contract §4)")
	}
	if action.kind != "global" {
		t.Fatalf("agent route kind = %q, want global", action.kind)
	}
	// A second agent call reusing the same stable session id, once a window
	// exists, must navigate rather than create.
	s := &browserSession{id: "global-agent", kind: "global", window: &application.WebviewWindow{}, confirmedURL: "https://agent.example.com"}
	action2 := app.resolveNavigateLocked("global-agent", "https://agent.example.com/2", s)
	if action2.create {
		t.Fatal("existing global-agent window must navigate, not create")
	}
	if action2.sameURL {
		t.Fatal("different URL must not flag sameURL")
	}
}

// --- 11. independent 隔离 (independent isolation) ---
//
// Two independent instances keep fully separate navigation state: a commit to one
// never touches the other, and injected events are gated per-session.

func TestScenarioIndependentIsolation(t *testing.T) {
	instA := &browserSession{id: "inst-a", kind: "independent", cfg: domain.BrowserInstanceConfig{ID: "inst-a"}}
	instB := &browserSession{id: "inst-b", kind: "independent", cfg: domain.BrowserInstanceConfig{ID: "inst-b"}}

	// instA commits a document.
	instA.beginHostNavigation(navSourceOpen, "https://a.example.com")
	instA.onNavigationStarting(1, 0, false)
	if ok := instA.commitNavigation(1, "https://a.example.com", true, 0); !ok {
		t.Fatal("instA commit must succeed")
	}
	instA.title = "A Title"

	// instB is unaffected: empty confirmedURL and title.
	if instB.confirmedURL != "" {
		t.Fatalf("instB confirmedURL = %q, want empty (isolation)", instB.confirmedURL)
	}
	if instB.title != "" {
		t.Fatalf("instB title = %q, want empty (isolation)", instB.title)
	}

	// instB commits a different document.
	instB.beginHostNavigation(navSourceOpen, "https://b.example.com")
	instB.onNavigationStarting(2, 0, false)
	if ok := instB.commitNavigation(2, "https://b.example.com", true, 0); !ok {
		t.Fatal("instB commit must succeed")
	}
	instB.title = "B Title"

	// instA still shows its own document (not corrupted by instB's commit).
	if instA.confirmedURL != "https://a.example.com" {
		t.Fatalf("instA confirmedURL = %q, want a.example.com (isolation)", instA.confirmedURL)
	}
	if instA.title != "A Title" {
		t.Fatalf("instA title = %q, want A Title (isolation)", instA.title)
	}

	// A stale injected event from instA's page (old URL) is rejected by instB's
	// gate because instB's confirmed document differs.
	if instB.acceptInjectedMeta("https://a.example.com") {
		t.Fatal("instB must reject instA's stale injected event (cross-instance isolation)")
	}
	// instB accepts its own current document's event.
	if !instB.acceptInjectedMeta("https://b.example.com") {
		t.Fatal("instB must accept its own current document's event")
	}
}

// Independent sessions use distinct profile paths (no cookie/storage leak).
func TestScenarioIndependentProfileIsolation(t *testing.T) {
	a := browserProfilePath("independent", "inst-a")
	b := browserProfilePath("independent", "inst-b")
	if a == b {
		t.Fatalf("independent profiles collide: %q", a)
	}
	// Global shares one profile; independent never collides with it.
	g := browserProfilePath("global", "global-agent")
	if a == g || b == g {
		t.Fatal("independent profile must not collide with the shared global profile")
	}
}

// --- 12. 旧事件竞态 (stale event race) ---
//
// After a new navigation commits, delayed injected messages and a late stale
// NavigationCompleted from the previous page must not regress confirmedURL,
// title or the persisted URL. This is the core race the refactor closes.

func TestScenarioStaleEventRace(t *testing.T) {
	s := &browserSession{kind: "independent", cfg: domain.BrowserInstanceConfig{ID: "inst-1"}}
	// First document /a commits.
	s.beginHostNavigation(navSourceOpen, "https://example.com/a")
	s.onNavigationStarting(1, 0, false)
	if ok := s.commitNavigation(1, "https://example.com/a", true, 0); !ok {
		t.Fatal("first commit must succeed")
	}
	s.title = "Page A"
	s.cfg.State.URL = "https://example.com/a"

	// New navigation to /b commits.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/b")
	s.onNavigationStarting(2, 1, false)
	if ok := s.commitNavigation(2, "https://example.com/b", true, 0); !ok {
		t.Fatal("second commit must succeed")
	}
	s.title = "Page B"
	s.cfg.State.URL = "https://example.com/b"

	// Stale injected navigated from page A arrives late — must be rejected.
	if s.acceptInjectedMeta("https://example.com/a") {
		t.Fatal("stale injected navigated from page A must be rejected")
	}
	if s.confirmedURL != "https://example.com/b" {
		t.Fatalf("confirmedURL = %q, want /b", s.confirmedURL)
	}
	if s.title != "Page B" {
		t.Fatalf("title = %q, want Page B", s.title)
	}

	// Stale NavigationCompleted from nav 1 (page A) arrives late — must be rejected.
	if ok := s.commitNavigation(1, "https://example.com/a", true, 0); ok {
		t.Fatal("stale Completed from page A must be rejected")
	}
	if s.confirmedURL != "https://example.com/b" {
		t.Fatalf("stale Completed regressed confirmedURL = %q, want /b", s.confirmedURL)
	}
	if s.navigationStatus != navStatusLoaded {
		t.Fatalf("stale Completed changed status = %q, want loaded", s.navigationStatus)
	}
	if s.cfg.State.URL != "https://example.com/b" {
		t.Fatalf("persisted URL regressed = %q, want /b", s.cfg.State.URL)
	}
}

// A page-initiated navigation that supersedes a host command must cause the
// host command's late Completed to be rejected, so a slow host navigation never
// overwrites the page the user actually arrived at.

func TestScenarioPageSupersedeHostStaleCompleted(t *testing.T) {
	s := freshSession(t, "https://example.com/a")
	// Host command to /b starts.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/b")
	s.onNavigationStarting(90, 1, false) // host NavId 90

	// But the page navigates to /c first (commandToken == 0): host pending superseded.
	s.onNavigationStarting(91, 0, false)
	if s.pendingNavigation.source != navSourceInPage || s.pendingNavigation.nativeNavID != 91 {
		t.Fatalf("current pending must be the in-page nav (source=%q navID=%d)", s.pendingNavigation.source, s.pendingNavigation.nativeNavID)
	}
	// Page /c commits.
	if ok := s.commitNavigation(91, "https://example.com/c", true, 0); !ok {
		t.Fatal("in-page commit must succeed")
	}

	// The host command's late Completed (NavId 90) must be rejected.
	if ok := s.commitNavigation(90, "https://example.com/b", true, 0); ok {
		t.Fatal("superseded host Completed must be rejected")
	}
	if s.confirmedURL != "https://example.com/c" {
		t.Fatalf("confirmedURL = %q, want /c (page the user arrived at)", s.confirmedURL)
	}
}
