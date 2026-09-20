package desktop

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// --- beginHostNavigation (contract §2.5.1 / §2.5.3) ---

// TestBeginHostNavigation verifies a host pending is allocated with loading
// status and the commandToken left at 0 (delivered later by the adapter in
// NavigationStarting).
func TestBeginHostNavigation(t *testing.T) {
	s := &browserSession{}
	if s.navigationStatus != "" {
		t.Fatalf("zero-value status = %q, want empty", s.navigationStatus)
	}

	s.beginHostNavigation(navSourceAddressBar, "https://example.com/a")
	if s.navigationStatus != navStatusLoading {
		t.Errorf("status = %q, want loading", s.navigationStatus)
	}
	if s.pendingNavigation == nil {
		t.Fatal("pendingNavigation must be non-nil after beginHostNavigation")
	}
	if s.pendingNavigation.source != navSourceAddressBar {
		t.Errorf("source = %q, want address-bar", s.pendingNavigation.source)
	}
	if s.pendingNavigation.requestedURL != "https://example.com/a" {
		t.Errorf("requestedURL = %q", s.pendingNavigation.requestedURL)
	}
	if s.pendingNavigation.commandToken != 0 {
		t.Errorf("commandToken = %d, want 0 (pending adapter delivery)", s.pendingNavigation.commandToken)
	}
	if s.pendingNavigation.nativeNavID != 0 {
		t.Errorf("nativeNavID = %d, want 0 (pending NavigationStarting)", s.pendingNavigation.nativeNavID)
	}
	if s.navigationID != 1 {
		t.Errorf("navigationID = %d, want 1", s.navigationID)
	}
}

// --- onNavigationStarting: host-command bind (§2.5.3) ---

// TestOnNavigationStartingHostBind verifies a Starting with commandToken != 0
// binds to the outstanding host pending: the pending's nativeNavID and
// commandToken are written, and s.nativeNavID is set for commit attribution.
func TestOnNavigationStartingHostBind(t *testing.T) {
	s := &browserSession{}
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/a")

	s.onNavigationStarting(100, 7, false)

	if s.pendingNavigation.nativeNavID != 100 {
		t.Errorf("pending.nativeNavID = %d, want 100", s.pendingNavigation.nativeNavID)
	}
	if s.pendingNavigation.commandToken != 7 {
		t.Errorf("pending.commandToken = %d, want 7", s.pendingNavigation.commandToken)
	}
	if s.nativeNavID != 100 {
		t.Errorf("s.nativeNavID = %d, want 100", s.nativeNavID)
	}
}

// --- onNavigationStarting: page-initiated supersede (§2.5.4) ---

// TestOnNavigationStartingPageSupersede verifies a Starting with commandToken
// == 0 supersedes the current host pending and allocates a new in-page pending.
func TestOnNavigationStartingPageSupersede(t *testing.T) {
	s := &browserSession{}
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/a")
	s.onNavigationStarting(100, 7, false) // host bind

	// Page-initiated navigation (user clicks a link): commandToken == 0.
	s.onNavigationStarting(200, 0, false)

	if s.pendingNavigation == nil {
		t.Fatal("pendingNavigation must be non-nil after page supersede")
	}
	if s.pendingNavigation.superseded {
		t.Error("the NEW (current) pending must not be superseded")
	}
	if s.pendingNavigation.nativeNavID != 200 {
		t.Errorf("pending.nativeNavID = %d, want 200", s.pendingNavigation.nativeNavID)
	}
	if s.pendingNavigation.commandToken != 0 {
		t.Errorf("pending.commandToken = %d, want 0 (page-initiated)", s.pendingNavigation.commandToken)
	}
	if s.pendingNavigation.source != navSourceInPage {
		t.Errorf("source = %q, want in-page", s.pendingNavigation.source)
	}
	if s.nativeNavID != 200 {
		t.Errorf("s.nativeNavID = %d, want 200", s.nativeNavID)
	}
}

// TestOnNavigationStartingPageSupersedeMarksOldPending verifies the old pending
// is marked superseded so its late Completed is rejected (§2.5.5).
func TestOnNavigationStartingPageSupersedeMarksOldPending(t *testing.T) {
	s := &browserSession{}
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/a")
	s.onNavigationStarting(100, 7, false)

	// Snapshot the old pending reference before supersede.
	old := s.pendingNavigation
	s.onNavigationStarting(200, 0, false)

	if !old.superseded {
		t.Error("old pending must be marked superseded")
	}
	// The old pending's Completed (navID 100) must be rejected.
	if ok := s.commitNavigation(100, "https://example.com/a", true, 0); ok {
		t.Error("superseded pending's Completed must be rejected")
	}
}

// --- onNavigationStarting: redirect no-op (§7) ---

// TestOnNavigationStartingRedirectNoop verifies a redirect hop (IsRedirected ==
// true) does not start a new epoch or supersede.
func TestOnNavigationStartingRedirectNoop(t *testing.T) {
	s := &browserSession{}
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/a")
	s.onNavigationStarting(100, 7, false)
	pendingBefore := s.pendingNavigation

	s.onNavigationStarting(100, 0, true) // redirect hop, same NavId

	if s.pendingNavigation != pendingBefore {
		t.Error("redirect hop must not replace the pending")
	}
	if s.pendingNavigation.superseded {
		t.Error("redirect hop must not supersede the pending")
	}
}

// --- resolveNoNavigation (§2.5.2 step 4) ---

// TestResolveNoNavigation verifies a NavStarted == false answer clears the
// pending and resolves status to idle (no prior confirmed document) or loaded
// (prior confirmed document), preventing stuck-loading.
func TestResolveNoNavigation(t *testing.T) {
	// No prior confirmed document -> idle.
	s := &browserSession{}
	s.beginHostNavigation(navSourceBackForward, "")
	s.resolveNoNavigation()
	if s.pendingNavigation != nil {
		t.Error("pendingNavigation must be nil after resolveNoNavigation")
	}
	if s.navigationStatus != navStatusIdle {
		t.Errorf("status = %q, want idle", s.navigationStatus)
	}

	// With a prior confirmed document -> loaded.
	s2 := &browserSession{}
	s2.confirmedURL = "https://example.com/old"
	s2.navigationStatus = navStatusLoaded
	s2.beginHostNavigation(navSourceBackForward, "")
	if s2.navigationStatus != navStatusLoading {
		t.Fatal("expected loading")
	}
	s2.resolveNoNavigation()
	if s2.navigationStatus != navStatusLoaded {
		t.Errorf("status = %q, want loaded (prior confirmed document)", s2.navigationStatus)
	}
}

// --- commitNavigation: success / failure / stale / graceful ---

func TestCommitNavigationSuccess(t *testing.T) {
	s := &browserSession{lastError: "old"}
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/a")
	s.onNavigationStarting(7, 1, false)

	ok := s.commitNavigation(7, "https://example.com/final", true, 0)
	if !ok {
		t.Fatal("commit of the active navigation must return true")
	}
	if s.confirmedURL != "https://example.com/final" {
		t.Errorf("confirmedURL = %q, want https://example.com/final", s.confirmedURL)
	}
	if s.navigationStatus != navStatusLoaded {
		t.Errorf("status = %q, want loaded", s.navigationStatus)
	}
	if s.lastError != "" {
		t.Errorf("lastError = %q, want empty on success", s.lastError)
	}
	if s.pendingNavigation != nil {
		t.Error("pendingNavigation must be nil after commit")
	}
}

func TestCommitNavigationFailure(t *testing.T) {
	s := &browserSession{}
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/good")
	s.onNavigationStarting(7, 1, false)
	if ok := s.commitNavigation(7, "https://example.com/good", true, 0); !ok {
		t.Fatal("initial commit must succeed")
	}

	// A subsequent failed navigation must not overwrite confirmedURL.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/bad")
	s.onNavigationStarting(8, 2, false)
	if ok := s.commitNavigation(8, "https://example.com/bad", false, -3); !ok {
		t.Fatal("failed commit of the active navigation must still return true")
	}
	if s.confirmedURL != "https://example.com/good" {
		t.Errorf("confirmedURL = %q, want to stay at the last good value", s.confirmedURL)
	}
	if s.navigationStatus != navStatusFailed {
		t.Errorf("status = %q, want failed", s.navigationStatus)
	}
	if s.lastError == "" {
		t.Error("lastError must be set on failure")
	}
	// The attempted URL must be recorded so the address bar can reflect the
	// failed document (state-payload projection).
	if s.attemptedURL != "https://example.com/bad" {
		t.Errorf("attemptedURL = %q, want the failed navigation's URL", s.attemptedURL)
	}
	payload := s.browserStatePayload()
	if payload["attemptedURL"] != "https://example.com/bad" {
		t.Errorf("payload attemptedURL = %q, want the failed navigation's URL", payload["attemptedURL"])
	}
	if payload["status"] != string(navStatusFailed) {
		t.Errorf("payload status = %q, want failed", payload["status"])
	}
}

// TestCommitNavigationFailureFallsBackToRequestedURL covers failure commits
// whose NavigationCompleted event carries no URL: the host-requested URL is
// the best available attempted URL.
func TestCommitNavigationFailureFallsBackToRequestedURL(t *testing.T) {
	s := &browserSession{}
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/bad")
	s.onNavigationStarting(8, 2, false)
	if ok := s.commitNavigation(8, "", false, -3); !ok {
		t.Fatal("failed commit of the active navigation must still return true")
	}
	if s.attemptedURL != "https://example.com/bad" {
		t.Errorf("attemptedURL = %q, want the requested URL fallback", s.attemptedURL)
	}
}

// TestAttemptedURLClearedOnNextNavigation verifies that the failed-attempt
// projection is transient: any later navigation (success or a new attempt)
// clears it so the address bar never shows a stale failed URL.
func TestAttemptedURLClearedOnNextNavigation(t *testing.T) {
	s := &browserSession{}
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/bad")
	s.onNavigationStarting(8, 2, false)
	if ok := s.commitNavigation(8, "https://example.com/bad", false, -3); !ok {
		t.Fatal("failed commit must return true")
	}
	if s.attemptedURL == "" {
		t.Fatal("attemptedURL must be set after failure")
	}

	s.beginHostNavigation(navSourceAddressBar, "https://example.com/next")
	s.onNavigationStarting(9, 3, false)
	if s.attemptedURL != "" {
		t.Errorf("attemptedURL = %q, want cleared when a new navigation begins", s.attemptedURL)
	}
	if ok := s.commitNavigation(9, "https://example.com/next", true, 0); !ok {
		t.Fatal("success commit must return true")
	}
	if _, present := s.browserStatePayload()["attemptedURL"]; present {
		t.Error("payload must not carry attemptedURL after a successful commit")
	}
}

// TestCommitNavigationRejectsStale verifies the core invariant: a
// NavigationCompleted whose NavigationId belongs to a superseded navigation is
// rejected and never touches confirmedURL.
func TestCommitNavigationRejectsStale(t *testing.T) {
	s := &browserSession{}
	// First navigation commits.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/first")
	s.onNavigationStarting(5, 1, false)
	if ok := s.commitNavigation(5, "https://example.com/first", true, 0); !ok {
		t.Fatal("first commit must succeed")
	}

	// Newer navigation supersedes and commits first.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/second")
	s.onNavigationStarting(6, 2, false)
	if ok := s.commitNavigation(6, "https://example.com/second", true, 0); !ok {
		t.Fatal("second commit must succeed")
	}
	if s.confirmedURL != "https://example.com/second" {
		t.Fatalf("confirmedURL = %q, want second", s.confirmedURL)
	}

	// Stale Completed from nav 5 arrives late.
	ok := s.commitNavigation(5, "https://example.com/first", true, 0)
	if ok {
		t.Fatal("stale commit must return false")
	}
	if s.confirmedURL != "https://example.com/second" {
		t.Errorf("stale commit regressed confirmedURL = %q, want second", s.confirmedURL)
	}
	if s.navigationStatus != navStatusLoaded {
		t.Errorf("stale commit changed status = %q, want loaded", s.navigationStatus)
	}
}

// TestCommitNavigationGracefulWithoutStarting verifies a Completed arriving
// without a prior Starting binding (nativeNavID == 0) still commits rather than
// being falsely rejected.
func TestCommitNavigationGracefulWithoutStarting(t *testing.T) {
	s := &browserSession{} // nativeNavID == 0
	ok := s.commitNavigation(9, "https://example.com/initial", true, 0)
	if !ok {
		t.Fatal("commit with no prior Starting must not be falsely rejected")
	}
	if s.confirmedURL != "https://example.com/initial" {
		t.Errorf("confirmedURL = %q, want initial", s.confirmedURL)
	}
}

// --- acceptInjectedMeta gate (§6) ---

func TestAcceptInjectedMetaGate(t *testing.T) {
	// Before any commit (empty confirmedURL): accept so initial metadata syncs.
	s := &browserSession{}
	if !s.acceptInjectedMeta("https://example.com") {
		t.Error("pre-commit message must be accepted")
	}

	// Established confirmed document.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/a")
	s.onNavigationStarting(1, 1, false)
	if ok := s.commitNavigation(1, "https://example.com/a", true, 0); !ok {
		t.Fatal("commit must succeed")
	}

	// Current document matches.
	if !s.acceptInjectedMeta("https://example.com/a") {
		t.Error("message for the confirmed document must be accepted")
	}
	// Trailing-slash / fragment variant of the same document still matches.
	if !s.acceptInjectedMeta("https://example.com/a/#frag") {
		t.Error("same-document variant must be accepted")
	}
	// Title-only message (no URL): accepted as metadata for the current doc.
	if !s.acceptInjectedMeta("") {
		t.Error("title-only message must be accepted")
	}

	// Stale message from a previous page: rejected.
	if s.acceptInjectedMeta("https://example.com/old") {
		t.Error("stale message (different document) must be rejected")
	}

	// During an in-flight navigation: rejected regardless of URL.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/b")
	if s.navigationStatus != navStatusLoading {
		t.Fatal("expected loading status")
	}
	if s.acceptInjectedMeta("https://example.com/a") {
		t.Error("in-flight message must be rejected during loading")
	}
	if s.acceptInjectedMeta("") {
		t.Error("even title-only message must be rejected during loading")
	}
}

// --- end-to-end stale event race ---

// TestStaleInjectedEventsDoNotCorruptState is the end-to-end reproduction of
// the old-page-late-event race: after a new navigation commits, delayed
// navigated / page-info messages from the previous page must not overwrite
// confirmedURL, the title or the persisted URL.
func TestStaleInjectedEventsDoNotCorruptState(t *testing.T) {
	s := &browserSession{kind: "independent", cfg: domainBrowserInstanceConfig("inst")}
	s.beginHostNavigation(navSourceOpen, "https://example.com/a")
	s.onNavigationStarting(1, 0, false)
	if ok := s.commitNavigation(1, "https://example.com/a", true, 0); !ok {
		t.Fatal("first commit must succeed")
	}
	s.title = "Page A"

	// New navigation to /b commits.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/b")
	s.onNavigationStarting(2, 1, false)
	if ok := s.commitNavigation(2, "https://example.com/b", true, 0); !ok {
		t.Fatal("second commit must succeed")
	}
	s.title = "Page B"

	// Delayed navigated / page-info from page A arrives (stale).
	if s.acceptInjectedMeta("https://example.com/a") {
		t.Fatal("stale navigated from page A must be rejected by the gate")
	}
	if s.confirmedURL != "https://example.com/b" {
		t.Errorf("confirmedURL = %q, want /b", s.confirmedURL)
	}
	if s.title != "Page B" {
		t.Errorf("title = %q, want Page B", s.title)
	}

	// A current-document page-info from page B is accepted.
	if !s.acceptInjectedMeta("https://example.com/b") {
		t.Fatal("current-document page-info must be accepted")
	}
	if s.confirmedURL != "https://example.com/b" {
		t.Errorf("accepted page-info must not change confirmedURL = %q", s.confirmedURL)
	}
}

// --- commandToken pending state machine: integrated scenarios ---

// TestHostCommandBindAndCommit exercises the full host-command lifecycle:
// beginHostNavigation -> onNavigationStarting (commandToken bind) -> commit.
func TestHostCommandBindAndCommit(t *testing.T) {
	s := &browserSession{}

	// Host issues Navigate.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/target")
	if s.navigationStatus != navStatusLoading {
		t.Fatal("expected loading after beginHostNavigation")
	}

	// Adapter delivers the Starting with commandToken = 1.
	s.onNavigationStarting(42, 1, false)
	if s.pendingNavigation.commandToken != 1 {
		t.Errorf("commandToken = %d, want 1", s.pendingNavigation.commandToken)
	}
	if s.nativeNavID != 42 {
		t.Errorf("nativeNavID = %d, want 42", s.nativeNavID)
	}

	// Commit succeeds.
	if ok := s.commitNavigation(42, "https://example.com/target", true, 0); !ok {
		t.Fatal("commit must succeed")
	}
	if s.confirmedURL != "https://example.com/target" {
		t.Errorf("confirmedURL = %q", s.confirmedURL)
	}
	if s.pendingNavigation != nil {
		t.Error("pendingNavigation must be nil after commit")
	}
}

// TestPageInitiatedNavAfterHostCommandSupersedes verifies the race where the
// page navigates before the host command's Starting arrives: the host pending
// is superseded, and the page-initiated navigation commits.
func TestPageInitiatedNavAfterHostCommandSupersedes(t *testing.T) {
	s := &browserSession{}
	s.confirmedURL = "https://example.com/old"
	s.navigationStatus = navStatusLoaded

	// Host issues Navigate.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/host")

	// Page-initiated navigation arrives first (user clicks link): commandToken == 0.
	s.onNavigationStarting(99, 0, false)

	if s.pendingNavigation.source != navSourceInPage {
		t.Errorf("source = %q, want in-page", s.pendingNavigation.source)
	}
	if s.nativeNavID != 99 {
		t.Errorf("nativeNavID = %d, want 99", s.nativeNavID)
	}

	// The page-initiated navigation commits.
	if ok := s.commitNavigation(99, "https://example.com/page", true, 0); !ok {
		t.Fatal("page-initiated commit must succeed")
	}
	if s.confirmedURL != "https://example.com/page" {
		t.Errorf("confirmedURL = %q, want page", s.confirmedURL)
	}
}

// TestNoNavigationResolvePreventsStuckLoading verifies that a NavStarted == false
// answer resolves the pending so the session returns to loaded, not stuck loading.
func TestNoNavigationResolvePreventsStuckLoading(t *testing.T) {
	s := &browserSession{}
	s.confirmedURL = "https://example.com/here"
	s.navigationStatus = navStatusLoaded

	// Host issues Back (but CanGoBack == false -> no navigation).
	s.beginHostNavigation(navSourceBackForward, "")
	if s.navigationStatus != navStatusLoading {
		t.Fatal("expected loading")
	}

	// Adapter answers NavStarted == false.
	s.resolveNoNavigation()
	if s.navigationStatus != navStatusLoaded {
		t.Errorf("status = %q, want loaded (resolved to prior confirmed document)", s.navigationStatus)
	}
	if s.pendingNavigation != nil {
		t.Error("pendingNavigation must be nil after resolveNoNavigation")
	}
}

// TestLastSeenNativeNavIDDiagnostic verifies the diagnostic counter tracks the
// highest observed NavId but is never used for attribution.
func TestLastSeenNativeNavIDDiagnostic(t *testing.T) {
	s := &browserSession{}
	s.onNavigationStarting(50, 0, false)
	if s.lastSeenNativeNavID != 50 {
		t.Errorf("lastSeenNativeNavID = %d, want 50", s.lastSeenNativeNavID)
	}
	s.onNavigationStarting(30, 0, true) // redirect, lower id
	if s.lastSeenNativeNavID != 50 {
		t.Errorf("lastSeenNativeNavID = %d, want 50 (max retained)", s.lastSeenNativeNavID)
	}
}

// TestBrowserStatePayload verifies the normalized navigation-state projection
// (contract §5 main event) that the frontend derives loading / address-bar /
// title / error from exclusively.
func TestBrowserStatePayload(t *testing.T) {
	// Idle / empty session: only status + confirmedURL present.
	s := &browserSession{}
	p := s.browserStatePayload()
	if p["status"] != string(navStatusIdle) {
		t.Errorf("idle status = %q, want %q", p["status"], navStatusIdle)
	}
	if p["confirmedURL"] != "" {
		t.Errorf("empty confirmedURL = %q, want empty", p["confirmedURL"])
	}
	if _, ok := p["title"]; ok {
		t.Error("idle session must not carry title")
	}
	if _, ok := p["error"]; ok {
		t.Error("idle session must not carry error")
	}

	// Loading: host command begins, status flips.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com")
	p = s.browserStatePayload()
	if p["status"] != string(navStatusLoading) {
		t.Errorf("loading status = %q, want %q", p["status"], navStatusLoading)
	}

	// Loaded: commit writes the final URL, title and error clear.
	s.onNavigationStarting(1, 1, false)
	if ok := s.commitNavigation(1, "https://example.com/final", true, 0); !ok {
		t.Fatal("commit of the active navigation must return true")
	}
	s.title = "Example"
	p = s.browserStatePayload()
	if p["status"] != string(navStatusLoaded) {
		t.Errorf("loaded status = %q, want %q", p["status"], navStatusLoaded)
	}
	if p["confirmedURL"] != "https://example.com/final" {
		t.Errorf("confirmedURL = %q, want https://example.com/final", p["confirmedURL"])
	}
	if p["title"] != "Example" {
		t.Errorf("title = %q, want Example", p["title"])
	}
	if _, ok := p["error"]; ok {
		t.Error("loaded session must not carry error")
	}

	// Failed: status flips, error set, confirmedURL keeps the last good value.
	s.beginHostNavigation(navSourceReload, "")
	s.onNavigationStarting(2, 2, false)
	if ok := s.commitNavigation(2, "", false, 3); !ok {
		t.Fatal("commit of the failed navigation must return true")
	}
	p = s.browserStatePayload()
	if p["status"] != string(navStatusFailed) {
		t.Errorf("failed status = %q, want %q", p["status"], navStatusFailed)
	}
	if p["confirmedURL"] != "https://example.com/final" {
		t.Errorf("confirmedURL on failure = %q, want last good", p["confirmedURL"])
	}
	if p["error"] == "" {
		t.Error("failed session must carry a non-empty error")
	}
}

func domainBrowserInstanceConfig(id string) domain.BrowserInstanceConfig {
	return domain.BrowserInstanceConfig{ID: id}
}

// --- stale-adoption repair (commitNavigation unbound-pending path) ---

// TestCommitAdoptsUnboundStaleCompleted reproduces the permanent
// stuck-in-loading wedge: a host pending whose NavigationStarting never bound
// (token lost or misattributed), while the session still carries the previous
// document's nativeNavID. The mismatched Completed is the only event that will
// ever arrive for our navigation — it must be adopted and committed.
func TestCommitAdoptsUnboundStaleCompleted(t *testing.T) {
	s := &browserSession{}
	// First navigation completes normally: nativeNavID = 42, confirmedURL set.
	s.beginHostNavigation(navSourceOpen, "https://example.com/a")
	s.onNavigationStarting(42, 1, false)
	if ok := s.commitNavigation(42, "https://example.com/a", true, 0); !ok {
		t.Fatal("first commit must succeed")
	}

	// Second navigation: Starting is lost. Only a mismatched Completed(43) arrives.
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/b")
	if ok := s.commitNavigation(43, "https://example.com/b", true, 0); !ok {
		t.Fatal("mismatched Completed for an unbound pending must be adopted, not rejected")
	}
	if !s.navAdopted {
		t.Error("navAdopted must record the adoption for diagnostics")
	}
	if s.navigationStatus != navStatusLoaded {
		t.Errorf("status = %q, want loaded (session must not stay stuck in loading)", s.navigationStatus)
	}
	if s.confirmedURL != "https://example.com/b" {
		t.Errorf("confirmedURL = %q, want https://example.com/b", s.confirmedURL)
	}
	if s.nativeNavID != 43 {
		t.Errorf("nativeNavID = %d, want 43 (adopted identity)", s.nativeNavID)
	}
	if s.pendingNavigation != nil {
		t.Error("pendingNavigation must be cleared after adoption")
	}
}

// TestCommitRejectsStaleWhenBound pins the safety half of the repair: once the
// active pending carries its own bound nativeNavID, a mismatched Completed is
// still rejected (double-reload late completion).
func TestCommitRejectsStaleWhenBound(t *testing.T) {
	s := &browserSession{}
	s.beginHostNavigation(navSourceOpen, "https://example.com/a")
	s.onNavigationStarting(42, 1, false)
	s.commitNavigation(42, "https://example.com/a", true, 0)

	// Second navigation binds 43; the late Completed(42) of the FIRST reload
	// round must be rejected, the matching Completed(43) commits.
	s.beginHostNavigation(navSourceReload, "")
	s.onNavigationStarting(43, 2, false)
	if ok := s.commitNavigation(42, "https://example.com/a", true, 0); ok {
		t.Fatal("bound pending + mismatched navID must stay rejected")
	}
	if s.navAdopted {
		t.Error("no adoption for a bound pending")
	}
	if ok := s.commitNavigation(43, "https://example.com/a", true, 0); !ok {
		t.Fatal("matching Completed must commit")
	}
	if s.navigationStatus != navStatusLoaded {
		t.Errorf("status = %q, want loaded", s.navigationStatus)
	}
}

// TestCommitRejectsSupersededNoAdopt: a superseded pending is never adopted,
// even unbound — the newer in-page navigation owns the session.
func TestCommitRejectsSupersededNoAdopt(t *testing.T) {
	s := &browserSession{}
	s.beginHostNavigation(navSourceOpen, "https://example.com/a")
	s.onNavigationStarting(42, 1, false)
	s.commitNavigation(42, "https://example.com/a", true, 0)

	s.beginHostNavigation(navSourceAddressBar, "https://example.com/b")
	// An in-page Starting arrives first and supersedes the host pending.
	s.onNavigationStarting(50, 0, false)
	if ok := s.commitNavigation(43, "https://example.com/b", true, 0); ok {
		t.Fatal("superseded pending must not adopt a mismatched Completed")
	}
	if s.navigationStatus != navStatusLoading {
		t.Errorf("status = %q, want loading (the in-page 50 still owns it)", s.navigationStatus)
	}
}

// --- watchdog verdict / reissue planning (pure functions) ---

func TestNavWatchdogVerdict(t *testing.T) {
	cases := []struct {
		name              string
		source, requested string
		confirmed         string
		reissued          bool
		exts              int
		want              navWatchdogAction
	}{
		{"source matches request", "https://example.com/b", "https://example.com/b", "https://example.com/a", false, 0, navWatchdogCommit},
		{"source matches request after redirect", "https://example.com/b/#frag", "https://example.com/b", "https://example.com/a", false, 0, navWatchdogCommit},
		{"still at old document", "https://example.com/a", "https://example.com/b", "https://example.com/a", false, 0, navWatchdogReissue},
		{"still at old document, already reissued", "https://example.com/a", "https://example.com/b", "https://example.com/a", true, 0, navWatchdogFail},
		{"elsewhere extends once", "https://other.example/x", "https://example.com/b", "https://example.com/a", false, 0, navWatchdogExtend},
		{"elsewhere exhausted", "https://other.example/x", "https://example.com/b", "https://example.com/a", false, 1, navWatchdogFail},
		{"empty source extends once", "", "https://example.com/b", "", false, 0, navWatchdogExtend},
		{"empty source exhausted", "", "https://example.com/b", "", false, 1, navWatchdogFail},
		{"back/forward stuck at confirmed", "https://example.com/a", "", "https://example.com/a", false, 0, navWatchdogReissue},
		{"back/forward stuck, reissued", "https://example.com/a", "", "https://example.com/a", true, 0, navWatchdogFail},
	}
	for _, tc := range cases {
		got := navWatchdogVerdict(tc.source, tc.requested, tc.confirmed, tc.reissued, tc.exts)
		if got != tc.want {
			t.Errorf("%s: verdict = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestReissuePrimitive(t *testing.T) {
	if reload, url, ok := reissuePrimitive(&pendingNav{source: navSourceReload}); !ok || !reload || url != "" {
		t.Errorf("reload pending: (%v, %q, %v), want (true, \"\", true)", reload, url, ok)
	}
	if reload, url, ok := reissuePrimitive(&pendingNav{source: navSourceAddressBar, requestedURL: "https://example.com/b"}); !ok || reload || url != "https://example.com/b" {
		t.Errorf("url pending: (%v, %q, %v), want (false, url, true)", reload, url, ok)
	}
	if _, _, ok := reissuePrimitive(&pendingNav{source: navSourceBackForward}); ok {
		t.Error("back/forward pending must not be re-issuable (no safe primitive)")
	}
	if _, _, ok := reissuePrimitive(nil); ok {
		t.Error("nil pending must not be re-issuable")
	}
}

// --- repair bookkeeping ---

func TestPendingStampsStartedAt(t *testing.T) {
	s := &browserSession{}
	before := time.Now()
	s.beginHostNavigation(navSourceAddressBar, "https://example.com/a")
	if s.pendingNavigation.startedAt.Before(before) {
		t.Error("host pending must stamp startedAt at allocation")
	}
	s.onNavigationStarting(7, 0, false) // in-page supersede allocates a fresh pending
	if s.pendingNavigation.source != navSourceInPage {
		t.Fatalf("source = %q, want in-page", s.pendingNavigation.source)
	}
	if s.pendingNavigation.startedAt.Before(before) {
		t.Error("in-page pending must stamp startedAt at allocation")
	}
}
