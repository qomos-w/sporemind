package desktop

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

// navStatus is the lifecycle phase of a session's current navigation
// (contract §1).
type navStatus string

const (
	navStatusIdle    navStatus = "idle"
	navStatusLoading navStatus = "loading"
	navStatusLoaded  navStatus = "loaded"
	navStatusFailed  navStatus = "failed"
)

// navSource records the originator of a navigation (contract §1 / §4). It is
// diagnostic only — it does not participate in commit attribution.
type navSource string

const (
	navSourceOpen        navSource = "open"
	navSourceAddressBar  navSource = "address-bar"
	navSourceReload      navSource = "reload"
	navSourceBackForward navSource = "back-forward"
	navSourceInPage      navSource = "in-page"
	navSourceAgent       navSource = "agent"
)

// pendingNav is the in-flight navigation descriptor (contract §1 / §2.5). It is
// allocated when a navigation begins (host command or page-initiated) and
// cleared when the navigation commits or is resolved as a no-navigation.
//
// nativeNavID is bound from the NavigationStarting payload (§2.5.3 for host
// commands, §2.5.4 for page-initiated). commandToken is the adapter's
// host-command correlation token (§2.5.2): non-zero for host-command starts,
// zero for page-initiated starts. superseded is set when a newer page-initiated
// navigation replaces this one (§2.5.4); a superseded pending's Completed is
// always rejected (§2.5.5).
type pendingNav struct {
	id           uint64    // == navigationID at allocation (host-side epoch)
	requestedURL string    // URL the host command requested; "" for page-initiated
	source       navSource // originator
	nativeNavID  uint64    // WebView2 NavigationId, bound from Starting; 0=unbound
	commandToken uint64    // adapter correlation token; 0=page-initiated or pending delivery
	superseded   bool      // replaced by a newer in-page navigation

	// Repair bookkeeping (watchdog / show-reissue). startedAt anchors the
	// loading age; reissued records that a repair already re-issued this
	// pending once (never twice); watchdogExts counts grace extensions granted
	// while the document was still redirecting or the renderer suspended.
	startedAt    time.Time
	reissued     bool
	watchdogExts int
}

// normalizeURL canonicalises a URL for same-document comparison so that
// fragment-only differences, default ports and trailing slashes — which differ
// between a NavigationCompleted's GetSource final URL and the injected
// location.href — do not cause a legitimate current-document message to be
// rejected as stale.
func normalizeURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		// Non-parseable / schemeless value: fall back to a trimmed compare so a
		// genuinely different URL is never accepted by accident.
		return strings.TrimRight(raw, "/")
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.Host = strings.ToLower(parsed.Hostname())
	// Canonicalize a Windows drive-letter host in a file URL to the
	// three-slash form Chromium reports as GetSource()/location.href:
	// file://D:/x.html and file:///D:/x.html are the same document, and the
	// raw two-slash form (common when typed by hand) must not fail the
	// same-document comparison.
	if parsed.Scheme == "file" && isWindowsDriveHost(parsed.Host) {
		parsed.Path = "/" + parsed.Host + ":" + parsed.Path
		parsed.Host = ""
	}
	// Drive letters are case-insensitive on Windows: align the case so a
	// typed file:///d:/… matches Chromium's canonical file:///D:/… in
	// same-document comparisons.
	if parsed.Scheme == "file" && len(parsed.Path) >= 3 && parsed.Path[2] == ':' &&
		((parsed.Path[1] >= 'A' && parsed.Path[1] <= 'Z') || (parsed.Path[1] >= 'a' && parsed.Path[1] <= 'z')) {
		parsed.Path = "/" + strings.ToLower(parsed.Path[1:3]) + parsed.Path[3:]
	}
	// Drop an explicit default port.
	switch {
	case parsed.Scheme == "http" && strings.HasSuffix(parsed.Host, ":80"):
		parsed.Host = strings.TrimSuffix(parsed.Host, ":80")
	case parsed.Scheme == "https" && strings.HasSuffix(parsed.Host, ":443"):
		parsed.Host = strings.TrimSuffix(parsed.Host, ":443")
	}
	// Collapse a trailing slash on the path (keep root "/").
	if len(parsed.Path) > 1 {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}
	return parsed.String()
}

// sameDocument reports whether two URLs refer to the same document after
// normalisation. It is the stale-event gate: an injected page message is only
// allowed to update metadata when its URL matches the confirmed document.
func sameDocument(a, b string) bool {
	return normalizeURL(a) == normalizeURL(b)
}

// isWindowsDriveHost reports whether a file URL host is a bare Windows drive
// letter ("d" or "d:"). Per the WHATWG file-URL parsing rules (implemented by
// Chromium), such a host is not a host at all — the drive letter belongs to
// the path, i.e. file://D:/x.html is file:///D:/x.html.
func isWindowsDriveHost(host string) bool {
	if host == "" {
		return false
	}
	c := host[0] | 0x20
	if c < 'a' || c > 'z' {
		return false
	}
	return len(host) == 1 || (len(host) == 2 && host[1] == ':')
}

// beginHostNavigation allocates a pending for a host-initiated command (contract
// §2.5.1 / §2.5.3). It must be called BEFORE invoking the adapter tokenized
// primitive (SetURL / Reload / GoBack / GoForward). The commandToken is left at
// 0 until the adapter delivers it in the NavigationStarting payload; binding
// happens in onNavigationStarting.
//
// Caller must hold browserMu.
func (s *browserSession) beginHostNavigation(source navSource, requestedURL string) {
	s.navigationID++
	s.pendingNavigation = &pendingNav{
		id:           s.navigationID,
		requestedURL: requestedURL,
		source:       source,
		nativeNavID:  0, // bound in onNavigationStarting
		commandToken: 0, // delivered by adapter in NavigationStarting
		startedAt:    time.Now(),
	}
	s.navigationStatus = navStatusLoading
	s.lastError = ""
	s.attemptedURL = ""
}

// onNavigationStarting processes a NavigationStarting event and binds the native
// navigation identity per the commandToken correlation rules (contract §2.5).
//
// Host-command binding (§2.5.3): a Starting with commandToken != 0 belongs to
// the outstanding host command. With §2.5.1 serialization there is at most one
// such pending; we write the token and bind nativeNavID so the later
// NavigationCompleted can be attributed (§2.5.5).
//
// Page-initiated supersede (§2.5.4): a Starting with commandToken == 0 is
// page-initiated (link/form/script). If a host pending is outstanding it is
// marked superseded; a new in-page pending is allocated and bound.
//
// Redirect hops (IsRedirected == true) share the active navigation's
// NavigationId and do not start a new epoch (§7).
//
// Caller must hold browserMu.
func (s *browserSession) onNavigationStarting(nativeID uint64, commandToken uint64, isRedirected bool) {
	// Diagnostic only (§2.5.4): track the highest observed native NavId for
	// logging; never used for attribution.
	if nativeID > s.lastSeenNativeNavID {
		s.lastSeenNativeNavID = nativeID
	}

	// Redirect hop (§2.5.4 / §7): same navigation progressing. An orphaned
	// redirect (different NavId) is also a no-op — it does not start a new
	// epoch or supersede.
	if isRedirected {
		return
	}

	if commandToken != 0 {
		// Host command correlation (§2.5.3): bind to the current host pending.
		// With §2.5.1 serialization there is at most one non-superseded pending.
		if s.pendingNavigation != nil && !s.pendingNavigation.superseded {
			s.pendingNavigation.commandToken = commandToken
			s.pendingNavigation.nativeNavID = nativeID
			s.nativeNavID = nativeID
		}
		// No outstanding pending (should not happen with §2.5.1): discard.
		return
	}

	// Page-initiated navigation (commandToken == 0, §2.5.4): supersede.
	if s.pendingNavigation != nil {
		s.pendingNavigation.superseded = true
	}
	s.navigationID++
	s.pendingNavigation = &pendingNav{
		id:           s.navigationID,
		source:       navSourceInPage,
		nativeNavID:  nativeID,
		commandToken: 0,
		startedAt:    time.Now(),
	}
	s.nativeNavID = nativeID
	s.navigationStatus = navStatusLoading
	s.lastError = ""
	s.attemptedURL = ""
}

// resolveNoNavigation resolves a host command that produced no top-level
// navigation (contract §2.5.2 step 4): the adapter replied NavStarted == false
// (e.g. GoBack when CanGoBack == false, or SetURL hitting same-origin no-nav).
// The pending is cleared and the status returns to idle (or loaded if a
// document was previously confirmed), so the session is not stuck in loading.
//
// Caller must hold browserMu.
func (s *browserSession) resolveNoNavigation() {
	s.pendingNavigation = nil
	if s.navigationStatus == navStatusLoading {
		if s.confirmedURL != "" {
			s.navigationStatus = navStatusLoaded
		} else {
			s.navigationStatus = navStatusIdle
		}
	}
}

// commitNavigation applies a NavigationCompleted result (contract §3 / §2.5.5).
// It returns true when the result was committed and false when it was rejected
// as stale — its NavigationId does not match the active navigation, in which
// case confirmedURL, the title and persisted state are left untouched.
//
// Commit attribution (§2.5.5): only a Completed whose NavigationId matches the
// active navigation's nativeNavID (and whose pending is not superseded) commits.
// The nativeNavID was bound exclusively via the commandToken correlation (host
// command) or the page-initiated supersede path — never by raw NavId frontier
// comparison.
//
// Success writes the post-redirect final URL into confirmedURL and transitions
// to loaded; failure transitions to failed and records the error and the
// attempted URL (for the address-bar projection), leaving confirmedURL at the
// last good value.
//
// Caller must hold browserMu.
func (s *browserSession) commitNavigation(navID uint64, finalURL string, success bool, webErrorStatus int32) bool {
	s.navAdopted = false
	// Stale rejection (§2.5.5): a Completed whose native identity is known and
	// differs from the active navigation belongs to a superseded navigation.
	if navID != 0 && s.nativeNavID != 0 && navID != s.nativeNavID {
		// Repair (unbound-pending adoption): the mismatch is only conclusive
		// when the ACTIVE pending carries its own bound native identity. When
		// the active host pending never bound one (its NavigationStarting was
		// lost, or its commandToken was consumed by an in-page navigation that
		// then superseded nothing), this Completed is the best available
		// candidate for OUR navigation — adopting it is the only way the
		// session ever leaves loading, because no other event will arrive for
		// a navigation whose Starting we already missed. A bound or superseded
		// pending is never adopted: those keep the strict stale rejection
		// (double-reload late completions, superseded documents).
		if s.pendingNavigation == nil || s.pendingNavigation.superseded || s.pendingNavigation.nativeNavID != 0 {
			return false
		}
		s.pendingNavigation.nativeNavID = navID
		s.nativeNavID = navID
		s.navAdopted = true
	}
	// Defence-in-depth: a Completed belonging to a superseded pending's own
	// native identity is rejected even if the NavId frontier check above did not
	// catch it (e.g. navID == 0 in an early race window).
	if s.pendingNavigation != nil && s.pendingNavigation.superseded &&
		navID != 0 && s.pendingNavigation.nativeNavID == navID {
		return false
	}

	pendingRequestedURL := ""
	if s.pendingNavigation != nil {
		pendingRequestedURL = s.pendingNavigation.requestedURL
	}
	s.pendingNavigation = nil
	if success {
		if finalURL != "" {
			s.confirmedURL = finalURL
		}
		s.lastError = ""
		s.attemptedURL = ""
		s.navigationStatus = navStatusLoaded
	} else {
		if webErrorStatus != 0 {
			s.lastError = "web_error_" + strconv.Itoa(int(webErrorStatus))
		} else {
			s.lastError = "navigation_failed"
		}
		// The address bar must reflect the failed document faithfully: prefer
		// the event's URL, falling back to the host-requested URL when the
		// event carries none (e.g. in-page navigations that never started).
		s.attemptedURL = finalURL
		if s.attemptedURL == "" {
			s.attemptedURL = pendingRequestedURL
		}
		s.navigationStatus = navStatusFailed
	}
	return true
}

// browserStatePayload builds the normalized navigation-state projection (contract
// §5 main event) from the session. The frontend derives loading (= status ==
// loading), the address bar (= attemptedURL when failed, else confirmedURL),
// the title and the error indicator exclusively from this map; the legacy
// browser:navigated / navigation-error / page-loaded events are
// transition-compat only and must not drive frontend state.
//
// Caller must hold browserMu.
func (s *browserSession) browserStatePayload() map[string]string {
	// A freshly-created session has a zero-value status (""); normalize it to
	// "idle" so the frontend always receives a well-formed status string.
	status := s.navigationStatus
	if status == "" {
		status = navStatusIdle
	}
	payload := map[string]string{
		"status":       string(status),
		"confirmedURL": s.confirmedURL,
	}
	if s.title != "" {
		payload["title"] = s.title
	}
	if s.lastError != "" {
		payload["error"] = s.lastError
	}
	// attemptedURL is emitted only for a failed navigation: the webview is
	// showing that URL's error page, so the address bar renders it instead of
	// the stale confirmedURL. cleared on the next navigation (any source).
	if s.attemptedURL != "" {
		payload["attemptedURL"] = s.attemptedURL
	}
	return payload
}

// acceptInjectedMeta decides whether an injected page message (navigated /
// page-info / painted) carrying msgURL may update document metadata — title,
// favicon, background or the first-paint signal — for the current document.
//
// It rejects:
//   - during an in-flight navigation (status loading): the document is not yet
//     confirmed, so provisional metadata must not be persisted or surfaced as
//     authoritative (contract §6);
//   - when msgURL is non-empty and does not match the confirmed document: a
//     stale emission from a previous page that arrived after a newer navigation
//     committed (the race this state machine exists to close).
//
// A title-only message (empty URL) or one arriving before any confirmed
// document (e.g. the very first about:blank) is accepted so initial metadata
// still syncs.
//
// Injected messages never write confirmedURL itself.
//
// Caller must hold browserMu.
func (s *browserSession) acceptInjectedMeta(msgURL string) bool {
	if s.navigationStatus == navStatusLoading {
		return false
	}
	if msgURL == "" {
		return true
	}
	if s.confirmedURL == "" {
		return true
	}
	return sameDocument(msgURL, s.confirmedURL)
}

// navWatchdogAction is the repair decision when the completion watchdog fires
// on a still-loading pending, reconciled against the WebView2 ground truth
// (the webview's current Source URL).
type navWatchdogAction int

const (
	navWatchdogCommit  navWatchdogAction = iota // Source matches the request → the document IS the target: commit as loaded
	navWatchdogReissue                          // Source still shows the old document → navigation was dropped: re-issue once
	navWatchdogExtend                           // Source is elsewhere (redirect chain in progress) → one more grace round
	navWatchdogFail                             // no repair left → resolve as failed, never stick in loading
)

// navWatchdogVerdict is the pure reconciliation core of the completion
// watchdog. It never guesses: every branch derives from comparing the
// webview's actual document URL against the pending's requested URL and the
// session's confirmed document. A watchdog that cannot positively reconcile
// grants at most one extension, then fails.
func navWatchdogVerdict(sourceURL, requestedURL, confirmedURL string, reissued bool, extensions int) navWatchdogAction {
	if requestedURL != "" && sameDocument(sourceURL, requestedURL) {
		return navWatchdogCommit
	}
	if confirmedURL != "" && sameDocument(sourceURL, confirmedURL) {
		if reissued {
			return navWatchdogFail
		}
		return navWatchdogReissue
	}
	if extensions < 1 {
		return navWatchdogExtend
	}
	return navWatchdogFail
}

// reissuePrimitive plans the tokenized primitive for a repair re-issue of a
// pending. Reload pendings re-reload; URL pendings re-navigate to their
// requested URL. A back/forward pending carries no requested URL and has no
// safe re-issue primitive — re-issuing GoBack/GoForward could double-step the
// history stack — so it is not re-issuable (the caller then lets the watchdog
// resolve it as failed).
func reissuePrimitive(p *pendingNav) (reload bool, url string, ok bool) {
	if p == nil {
		return false, "", false
	}
	switch {
	case p.source == navSourceReload:
		return true, "", true
	case p.requestedURL != "":
		return false, p.requestedURL, true
	default:
		return false, "", false
	}
}
