package desktop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// ── buildUsePayload ──

func TestBuildUsePayload_ClickAction(t *testing.T) {
	req := domain.BrowserUseReq{
		Action:    "click",
		ElementID: "e1",
		ClickX:    100,
		ClickY:    200,
	}
	p := buildUsePayload(req)
	if p.ObsID == "" {
		t.Fatal("expected non-empty obsID")
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("invalid JSON payload: %v", err)
	}
	if m["action"] != "click" {
		t.Errorf("expected action click, got %v", m["action"])
	}
	if m["elementId"] != "e1" {
		t.Errorf("expected elementId e1, got %v", m["elementId"])
	}
	// JSON numbers unmarshal as float64
	if m["clickX"].(float64) != 100 {
		t.Errorf("expected clickX 100, got %v", m["clickX"])
	}
	if m["clickY"].(float64) != 200 {
		t.Errorf("expected clickY 200, got %v", m["clickY"])
	}
	if m["obsId"] != p.ObsID {
		t.Errorf("expected obsId %s, got %v", p.ObsID, m["obsId"])
	}
}

func TestBuildUsePayload_TypeAction(t *testing.T) {
	req := domain.BrowserUseReq{
		Action:    "type",
		ElementID: "e2",
		Text:      "hello world",
		Append:    true,
		Submit:    true,
	}
	p := buildUsePayload(req)

	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["action"] != "type" {
		t.Errorf("expected action type, got %v", m["action"])
	}
	if m["text"] != "hello world" {
		t.Errorf("expected text, got %v", m["text"])
	}
	if m["append"] != true {
		t.Errorf("expected append true, got %v", m["append"])
	}
	if m["submit"] != true {
		t.Errorf("expected submit true, got %v", m["submit"])
	}
}

func TestBuildUsePayload_ScrollAction(t *testing.T) {
	req := domain.BrowserUseReq{
		Action:       "scroll",
		ScrollX:      0,
		ScrollY:      500,
		ScrollTarget: "window",
	}
	p := buildUsePayload(req)

	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["action"] != "scroll" {
		t.Errorf("expected action scroll, got %v", m["action"])
	}
	if m["scrollY"].(float64) != 500 {
		t.Errorf("expected scrollY 500, got %v", m["scrollY"])
	}
	if m["scrollTarget"] != "window" {
		t.Errorf("expected scrollTarget window, got %v", m["scrollTarget"])
	}
	// scrollX is 0 and should be omitted
	if _, ok := m["scrollX"]; ok {
		t.Errorf("expected scrollX omitted when 0, got %v", m["scrollX"])
	}
}

func TestBuildUsePayload_WaitAction(t *testing.T) {
	req := domain.BrowserUseReq{
		Action:        "wait",
		WaitMs:        2000,
		WaitFor:       "element",
		WaitElementID: "e3",
	}
	p := buildUsePayload(req)

	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["action"] != "wait" {
		t.Errorf("expected action wait, got %v", m["action"])
	}
	if m["waitMs"].(float64) != 2000 {
		t.Errorf("expected waitMs 2000, got %v", m["waitMs"])
	}
	if m["waitFor"] != "element" {
		t.Errorf("expected waitFor element, got %v", m["waitFor"])
	}
	if m["waitElementId"] != "e3" {
		t.Errorf("expected waitElementId e3, got %v", m["waitElementId"])
	}
}

func TestBuildUsePayload_ObsIDPrefix(t *testing.T) {
	p := buildUsePayload(domain.BrowserUseReq{Action: "observe"})
	if len(p.ObsID) < 5 || p.ObsID[:4] != "use-" {
		t.Errorf("expected obsID to start with 'use-', got %q", p.ObsID)
	}
}

// TestObserveScriptCorrelation pins the observe obsID contract end to end at the
// script level: the observeScript must install a __sporemindObserve(obsId)
// entrypoint that echoes the Go-passed correlation ID, the invocation script
// must embed that exact ID as a quoted JS string, and the echoed message must
// resolve the pending Observe callback. Before this contract existed the script
// self-generated its obsId, the callback lookup always missed, and every
// browsermanager.use observe (crawl engine included) timed out after 15s.
func TestObserveScriptCorrelation(t *testing.T) {
	if !strings.Contains(observeScript, "window.__sporemindObserve = function(obsIdArg)") {
		t.Error("observeScript must install window.__sporemindObserve(obsIdArg)")
	}
	if !strings.Contains(observeScript, "obsIdArg ||") {
		t.Error("observeScript must prefer the passed correlation ID (fallback only when omitted)")
	}
	if !strings.Contains(observeScript, "postMessage('wails:' + msg)") {
		t.Error("observeScript must prefix its postMessage with \"wails:\" — unprefixed messages are routed to the gospore gateway transport and swallowed")
	}
	if !strings.Contains(observeScript, "postMessage('wails:' + JSON.stringify({type:'browser-observe'") {
		t.Error("observeScript error branch must also carry the \"wails:\" prefix")
	}
	if strings.Contains(observeScript, "(function() {") {
		t.Error("observeScript must not be a self-executing IIFE; Observe needs to pass the correlation ID")
	}

	obsID := "obs-1789420459000123456"
	call := observeCallScript(obsID)
	wantCall := `window.__sporemindObserve && window.__sporemindObserve("` + obsID + `");`
	if call != wantCall {
		t.Errorf("observeCallScript(%q) = %q, want %q", obsID, call, wantCall)
	}

	// The echoed message must resolve the registered callback, not miss it.
	o := newTestOperator()
	ch := make(chan *domain.BrowserPageObservation, 1)
	o.observeCallbacks[obsID] = ch
	msg := `{"type":"browser-observe","obsId":"` + obsID + `","url":"https://example.com","title":"Example","totalElementCount":0}`
	if !o.handleBrowserObserveMessage("crawl-7", msg) {
		t.Fatal("expected message to be handled")
	}
	select {
	case obs := <-ch:
		if obs == nil || obs.URL != "https://example.com" {
			t.Errorf("expected observation for https://example.com, got %+v", obs)
		}
	default:
		t.Fatal("correlation ID from the page message did not resolve the pending Observe callback")
	}
	if _, stillPending := o.observeCallbacks[obsID]; stillPending {
		t.Error("callback must be removed after delivery")
	}
}

// ── handleBrowserObserveMessage ──

func newTestOperator() *desktopWindowOperator {
	return &desktopWindowOperator{
		windows:          make(map[string]*browserWindow),
		snapshots:        newSnapshotStore(),
		observeCallbacks: make(map[string]chan *domain.BrowserPageObservation),
	}
}

func TestHandleBrowserObserveMessage_ParsesElements(t *testing.T) {
	o := newTestOperator()
	obsID := "obs-123"
	ch := make(chan *domain.BrowserPageObservation, 1)
	o.observeCallbacks[obsID] = ch

	msg := `{"type":"browser-observe","obsId":"obs-123","url":"https://example.com","title":"Example","viewportWidth":1920,"viewportHeight":1080,"scrollX":10,"scrollY":20,"loadState":"complete","totalElementCount":1,"historyLength":3,"timestamp":"2026-01-01T00:00:00Z","elements":[{"Id":"e1","TagName":"button","Selector":"#btn","Text":"Submit","IsVisible":true,"IsEnabled":true,"ActionHint":"click","ActionConfidence":0.9,"Rect":{"x":10,"y":20,"width":100,"height":40}}]}`

	if !o.handleBrowserObserveMessage("inst-0", msg) {
		t.Fatal("expected message to be handled")
	}

	obs := <-ch
	if obs == nil {
		t.Fatal("expected non-nil observation")
	}
	if obs.ObservationID != "obs-123" {
		t.Errorf("expected observationID obs-123, got %q", obs.ObservationID)
	}
	if obs.URL != "https://example.com" {
		t.Errorf("expected URL, got %q", obs.URL)
	}
	if obs.Title != "Example" {
		t.Errorf("expected title Example, got %q", obs.Title)
	}
	if obs.ViewportWidth != 1920 {
		t.Errorf("expected viewportWidth 1920, got %d", obs.ViewportWidth)
	}
	if obs.ViewportHeight != 1080 {
		t.Errorf("expected viewportHeight 1080, got %d", obs.ViewportHeight)
	}
	if obs.ScrollX != 10 {
		t.Errorf("expected scrollX 10, got %d", obs.ScrollX)
	}
	if obs.ScrollY != 20 {
		t.Errorf("expected scrollY 20, got %d", obs.ScrollY)
	}
	if obs.LoadState != "complete" {
		t.Errorf("expected loadState complete, got %q", obs.LoadState)
	}
	if obs.TotalElementCount != 1 {
		t.Errorf("expected totalElementCount 1, got %d", obs.TotalElementCount)
	}
	if obs.HistoryLength != 3 {
		t.Errorf("expected historyLength 3, got %d", obs.HistoryLength)
	}
	if len(obs.Elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(obs.Elements))
	}
	el := obs.Elements[0]
	if el.ID != "e1" {
		t.Errorf("expected element ID e1, got %q", el.ID)
	}
	if el.TagName != "button" {
		t.Errorf("expected tagName button, got %q", el.TagName)
	}
	if el.Selector != "#btn" {
		t.Errorf("expected selector #btn, got %q", el.Selector)
	}
	if el.Text != "Submit" {
		t.Errorf("expected text Submit, got %q", el.Text)
	}
	if !el.IsVisible {
		t.Errorf("expected IsVisible true")
	}
	if !el.IsEnabled {
		t.Errorf("expected IsEnabled true")
	}
	if el.ActionHint != "click" {
		t.Errorf("expected actionHint click, got %q", el.ActionHint)
	}
	if el.ActionConfidence != 0.9 {
		t.Errorf("expected actionConfidence 0.9, got %f", el.ActionConfidence)
	}
	if el.Rect.X != 10 || el.Rect.Y != 20 || el.Rect.Width != 100 || el.Rect.Height != 40 {
		t.Errorf("unexpected rect: %+v", el.Rect)
	}
}

func TestHandleBrowserObserveMessage_Error(t *testing.T) {
	o := newTestOperator()
	obsID := "obs-err"
	ch := make(chan *domain.BrowserPageObservation, 1)
	o.observeCallbacks[obsID] = ch

	msg := `{"type":"browser-observe","obsId":"obs-err","error":"script failure"}`
	if !o.handleBrowserObserveMessage("inst-0", msg) {
		t.Fatal("expected message to be handled")
	}
	obs := <-ch
	if obs != nil {
		t.Errorf("expected nil observation on error, got %+v", obs)
	}
	// Callback should be cleaned up.
	if _, ok := o.observeCallbacks[obsID]; ok {
		t.Errorf("expected callback removed after error")
	}
}

func TestHandleBrowserObserveMessage_NonObserveMessage(t *testing.T) {
	o := newTestOperator()
	if o.handleBrowserObserveMessage("inst-0", `{"type":"page-snapshot"}`) {
		t.Error("expected false for non-observe message")
	}
}

func TestHandleBrowserObserveMessage_InvalidJSON(t *testing.T) {
	o := newTestOperator()
	if o.handleBrowserObserveMessage("inst-0", `{invalid`) {
		t.Error("expected false for invalid JSON")
	}
}

func TestHandleBrowserObserveMessage_NoCallback(t *testing.T) {
	o := newTestOperator()
	// No callback registered for this obsId.
	msg := `{"type":"browser-observe","obsId":"obs-orphan","url":"https://example.com"}`
	if !o.handleBrowserObserveMessage("inst-0", msg) {
		t.Fatal("expected true (recognized but no waiter)")
	}
}

func TestHandleBrowserObserveMessage_EmptyObsID(t *testing.T) {
	o := newTestOperator()
	// Empty obsId should return true without panicking.
	msg := `{"type":"browser-observe","obsId":"","url":"https://example.com"}`
	if !o.handleBrowserObserveMessage("inst-0", msg) {
		t.Fatal("expected true for empty obsId")
	}
}

// ── Observe / Use window-not-found ──

func TestObserve_WindowNotFound(t *testing.T) {
	o := newTestOperator()
	_, err := o.Observe("missing-inst")
	if err == nil {
		t.Fatal("expected error for missing window")
	}
}

func TestUse_WindowNotFound(t *testing.T) {
	o := newTestOperator()
	_, err := o.Use("missing-inst", domain.BrowserUseReq{Action: "click"})
	if err == nil {
		t.Fatal("expected error for missing window")
	}
}

// ── handleBrowserUseResult (snapshotStore) ──

func TestHandleBrowserUseResult_Success(t *testing.T) {
	store := newSnapshotStore()
	received := make(chan BrowserUseResult, 1)
	obsID := "use-999"
	cancel := store.RegisterCallback(obsID, func(r BrowserUseResult) {
		received <- r
	})
	defer cancel()

	msg := `{"type":"browser-use-result","obsId":"use-999","success":true,"message":"clicked e1"}`
	if !store.handleBrowserUseResult(msg) {
		t.Fatal("expected message to be handled")
	}

	r := <-received
	if !r.Success {
		t.Errorf("expected success true")
	}
	if r.Message != "clicked e1" {
		t.Errorf("expected message 'clicked e1', got %q", r.Message)
	}
	if r.ObservationID != "use-999" {
		t.Errorf("expected observationID use-999, got %q", r.ObservationID)
	}
}

func TestHandleBrowserUseResult_NonUseMessage(t *testing.T) {
	store := newSnapshotStore()
	if store.handleBrowserUseResult(`{"type":"page-snapshot"}`) {
		t.Error("expected false for non-use-result message")
	}
}

// TestUseResultRoundTrip pins the contract between the Go-side Use flow and the
// page-side useScript: buildUsePayload puts payload.ObsID into the payload JSON
// as obsId, Use registers the result callback under that same payload.ObsID
// (no extra prefix), and the page echoes obsId verbatim — so a result message
// carrying the payload's obsId must resolve the pending callback. A re-added
// prefix on the registration key (the "use-use-<nanos>" regression) left every
// interactive browser action timing out.
func TestUseResultRoundTrip(t *testing.T) {
	p := buildUsePayload(domain.BrowserUseReq{Action: "navigate", URL: "https://example.com", NavigateMode: "navigate"})

	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("payload JSON invalid: %v", err)
	}
	obsID, _ := m["obsId"].(string)
	if obsID == "" {
		t.Fatal("payload JSON carries no obsId")
	}

	store := newSnapshotStore()
	received := make(chan BrowserUseResult, 1)
	cancel := store.RegisterCallback(p.ObsID, func(r BrowserUseResult) { received <- r })
	defer cancel()

	echo, err := json.Marshal(map[string]any{
		"type":    "browser-use-result",
		"obsId":   obsID,
		"success": true,
		"message": "navigate: navigate https://example.com",
	})
	if err != nil {
		t.Fatalf("marshal echo message: %v", err)
	}
	if !store.handleBrowserUseResult(string(echo)) {
		t.Fatal("expected echo message to be handled")
	}
	select {
	case r := <-received:
		if !r.Success || r.ObservationID != obsID {
			t.Fatalf("unexpected result: %+v", r)
		}
	default:
		t.Fatal("echoed obsId did not resolve the callback registered under payload.ObsID")
	}
}

func TestHandleBrowserUseResult_InvalidJSON(t *testing.T) {
	store := newSnapshotStore()
	if store.handleBrowserUseResult(`{invalid`) {
		t.Error("expected false for invalid JSON")
	}
}

func TestHandleBrowserUseResult_NoCallback(t *testing.T) {
	store := newSnapshotStore()
	// No callback registered.
	msg := `{"type":"browser-use-result","obsId":"use-orphan","success":true}`
	// Should not panic; returns true because the message type matched.
	if !store.handleBrowserUseResult(msg) {
		t.Fatal("expected true (recognized type) even without callback")
	}
}

// ── navigation waiters ──

// TestNavWaiters_NotifyReleasesAndClears pins the bridge between the native
// URL-navigate path (Use waits on registerNavWaiter) and the
// WebViewNavigationCompleted hook (notifyNavCompleted). URL navigations used
// to fall through to the page-side useScript, whose postMessage races against
// the document being torn down — every navigate then timed out at the 15s
// operator deadline. Native navigate + navWaiters must release promptly.
func TestNavWaiters_NotifyReleasesAndClears(t *testing.T) {
	o := &desktopWindowOperator{}
	o.windows = make(map[string]*browserWindow)
	o.observeCallbacks = make(map[string]chan *domain.BrowserPageObservation)
	o.navWaiters = make(map[string][]chan struct{})

	waitCh, cancel := o.registerNavWaiter("inst-1")
	defer cancel()

	select {
	case <-waitCh:
		t.Fatal("waiter released before notify")
	default:
	}

	o.notifyNavCompleted("inst-1")

	select {
	case <-waitCh:
	default:
		t.Fatal("waiter not released after notify")
	}

	// notify is idempotent and clears the waiter list.
	o.notifyNavCompleted("inst-1")
	o.mu.Lock()
	if _, ok := o.navWaiters["inst-1"]; ok {
		t.Error("waiter list not cleared after notify")
	}
	o.mu.Unlock()
}

func TestNavWaiters_CancelRemovesWaiter(t *testing.T) {
	o := &desktopWindowOperator{}
	o.windows = make(map[string]*browserWindow)
	o.observeCallbacks = make(map[string]chan *domain.BrowserPageObservation)
	o.navWaiters = make(map[string][]chan struct{})

	_, cancel := o.registerNavWaiter("inst-2")
	cancel()

	o.mu.Lock()
	remaining := len(o.navWaiters["inst-2"])
	o.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected 0 waiters after cancel, got %d", remaining)
	}
}

// ── getString / getBool / getFloat helpers ──

func TestGetString(t *testing.T) {
	m := map[string]any{"key": "value", "num": 42}
	if got := getString(m, "key"); got != "value" {
		t.Errorf("expected 'value', got %q", got)
	}
	if got := getString(m, "num"); got != "" {
		t.Errorf("expected empty for non-string, got %q", got)
	}
	if got := getString(m, "absent"); got != "" {
		t.Errorf("expected empty for absent key, got %q", got)
	}
}

func TestGetBool(t *testing.T) {
	m := map[string]any{"flag": true, "num": 1}
	if !getBool(m, "flag") {
		t.Errorf("expected true for 'flag'")
	}
	if getBool(m, "num") {
		t.Errorf("expected false for non-bool")
	}
	if getBool(m, "absent") {
		t.Errorf("expected false for absent key")
	}
}

func TestGetFloat(t *testing.T) {
	m := map[string]any{"f": float64(3.14), "i": 42, "i64": int64(7), "s": "abc"}
	if got := getFloat(m, "f"); got != 3.14 {
		t.Errorf("expected 3.14, got %f", got)
	}
	if got := getFloat(m, "i"); got != 42 {
		t.Errorf("expected 42, got %f", got)
	}
	if got := getFloat(m, "i64"); got != 7 {
		t.Errorf("expected 7, got %f", got)
	}
	if got := getFloat(m, "s"); got != 0 {
		t.Errorf("expected 0 for string, got %f", got)
	}
	if got := getFloat(m, "absent"); got != 0 {
		t.Errorf("expected 0 for absent, got %f", got)
	}
}

// ── navigate/press/select payload fields ──

func TestBuildUsePayload_NavigateAction(t *testing.T) {
	req := domain.BrowserUseReq{
		Action:       "navigate",
		URL:          "https://example.com/page",
		NavigateMode: "navigate",
	}
	p := buildUsePayload(req)
	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["action"] != "navigate" {
		t.Errorf("expected action navigate, got %v", m["action"])
	}
	if m["url"] != "https://example.com/page" {
		t.Errorf("expected url, got %v", m["url"])
	}
	if m["navigateMode"] != "navigate" {
		t.Errorf("expected navigateMode, got %v", m["navigateMode"])
	}
}

func TestBuildUsePayload_NavigateBackMode(t *testing.T) {
	req := domain.BrowserUseReq{
		Action:       "navigate",
		NavigateMode: "back",
	}
	p := buildUsePayload(req)
	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["navigateMode"] != "back" {
		t.Errorf("expected navigateMode back, got %v", m["navigateMode"])
	}
}

func TestBuildUsePayload_PressAction(t *testing.T) {
	req := domain.BrowserUseReq{
		Action:    "press",
		Key:       "Enter",
		ElementID: "e5",
	}
	p := buildUsePayload(req)
	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["action"] != "press" {
		t.Errorf("expected action press, got %v", m["action"])
	}
	if m["key"] != "Enter" {
		t.Errorf("expected key Enter, got %v", m["key"])
	}
	if m["elementId"] != "e5" {
		t.Errorf("expected elementId e5, got %v", m["elementId"])
	}
}

func TestBuildUsePayload_SelectAction(t *testing.T) {
	req := domain.BrowserUseReq{
		Action:    "select",
		ElementID: "e9",
		Text:      "option2",
	}
	p := buildUsePayload(req)
	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["action"] != "select" {
		t.Errorf("expected action select, got %v", m["action"])
	}
	if m["text"] != "option2" {
		t.Errorf("expected text option2, got %v", m["text"])
	}
	if m["elementId"] != "e9" {
		t.Errorf("expected elementId e9, got %v", m["elementId"])
	}
}

// ── validateNavigateURL ──

func TestValidateNavigateURL_HTTPS(t *testing.T) {
	if msg := validateNavigateURL("navigate", "https://example.com", "navigate"); msg != "" {
		t.Errorf("expected valid, got %q", msg)
	}
}

func TestValidateNavigateURL_HTTP(t *testing.T) {
	if msg := validateNavigateURL("navigate", "http://localhost:3000", "navigate"); msg != "" {
		t.Errorf("expected valid, got %q", msg)
	}
}

func TestValidateNavigateURL_FileScheme(t *testing.T) {
	if msg := validateNavigateURL("navigate", "file:///D:/web/page.html", "navigate"); msg != "" {
		t.Errorf("expected valid, got %q", msg)
	}
	if msg := validateNavigateURL("navigate", "file:///home/user/page.html", "navigate"); msg != "" {
		t.Errorf("expected valid, got %q", msg)
	}
}

func TestValidateNavigateURL_JavaScriptScheme(t *testing.T) {
	msg := validateNavigateURL("navigate", "javascript:alert(1)", "navigate")
	if msg == "" {
		t.Error("expected error for javascript: scheme")
	}
}

func TestValidateNavigateURL_EmptyURL(t *testing.T) {
	msg := validateNavigateURL("navigate", "", "navigate")
	if msg == "" {
		t.Error("expected error for empty url")
	}
}

func TestValidateNavigateURL_BackModeNoURL(t *testing.T) {
	if msg := validateNavigateURL("navigate", "", "back"); msg != "" {
		t.Errorf("expected valid (back needs no url), got %q", msg)
	}
}

func TestValidateNavigateURL_ForwardModeNoURL(t *testing.T) {
	if msg := validateNavigateURL("navigate", "", "forward"); msg != "" {
		t.Errorf("expected valid (forward needs no url), got %q", msg)
	}
}

func TestValidateNavigateURL_ReloadModeNoURL(t *testing.T) {
	if msg := validateNavigateURL("navigate", "", "reload"); msg != "" {
		t.Errorf("expected valid (reload needs no url), got %q", msg)
	}
}

func TestValidateNavigateURL_NoScheme(t *testing.T) {
	msg := validateNavigateURL("navigate", "example.com/path", "navigate")
	if msg == "" {
		t.Error("expected error for URL without scheme")
	}
}

func TestValidateNavigateURL_NotNavigateAction(t *testing.T) {
	if msg := validateNavigateURL("click", "", ""); msg != "" {
		t.Errorf("expected valid for non-navigate action, got %q", msg)
	}
}
