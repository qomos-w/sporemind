package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	browserWindowStateFlushDebounce = 400 * time.Millisecond
	browserInvokeTimeout            = 5 * time.Second
)

// browserWindow wraps one Wails WebviewWindow plus pending state-flush bookkeeping.
type browserWindow struct {
	win                 *application.WebviewWindow
	id                  string
	cfg                 domain.BrowserInstanceConfig
	mu                  sync.Mutex
	flushTimer          *time.Timer
	pageStateFlushTimer *time.Timer
}

// desktopWindowOperator implements browserinstance.WindowOperator using the
// Wails application and the gospore actor tree.
type desktopWindowOperator struct {
	app              *application.App
	handle           *runtime.Handle
	windows          map[string]*browserWindow
	snapshots        *snapshotStore
	onEvent          func(id, url, title string)
	mu               sync.Mutex
	shuttingDown     bool
	observeCallbacks map[string]chan *domain.BrowserPageObservation
	navWaiters       map[string][]chan struct{}
}

func newDesktopWindowOperator(app *application.App, handle *runtime.Handle, onEvent func(id, url, title string)) *desktopWindowOperator {
	return &desktopWindowOperator{
		app:              app,
		handle:           handle,
		windows:          make(map[string]*browserWindow),
		snapshots:        newSnapshotStore(),
		onEvent:          onEvent,
		observeCallbacks: make(map[string]chan *domain.BrowserPageObservation),
		navWaiters:       make(map[string][]chan struct{}),
	}
}

// navWaitTimeout bounds how long a native navigate action waits for
// WebViewNavigationCompleted before returning anyway. It stays under the
// operator-level 15s deadline so the timeout wrapper, not this wait, defines
// the hard cap.
const navWaitTimeout = 10 * time.Second

// registerNavWaiter parks a one-shot channel that is closed on the window's
// next WebViewNavigationCompleted event. The returned cancel removes the
// waiter if the caller gives up first.
func (o *desktopWindowOperator) registerNavWaiter(id string) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	o.mu.Lock()
	o.navWaiters[id] = append(o.navWaiters[id], ch)
	o.mu.Unlock()
	return ch, func() {
		o.mu.Lock()
		waiters := o.navWaiters[id]
		for i, w := range waiters {
			if w == ch {
				o.navWaiters[id] = slices.Delete(waiters, i, i+1)
				break
			}
		}
		o.mu.Unlock()
	}
}

// notifyNavCompleted releases every waiter parked for the window's navigation
// completion. Redirect chains fire the event per hop; releasing on the first
// hop is acceptable for the observe-after-navigate pattern.
func (o *desktopWindowOperator) notifyNavCompleted(id string) {
	o.mu.Lock()
	waiters := o.navWaiters[id]
	delete(o.navWaiters, id)
	o.mu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
}

func profileDir(id string) string {
	return filepath.Join(config.DataDir(), "browser-profiles", id)
}

// currentPage returns the page a window-mode instance should (re)open at: the
// current navigation page (State.URL), falling back to the settings-page URL
// for legacy conflated records where State.URL was never written.
func currentPage(cfg domain.BrowserInstanceConfig) string {
	if cfg.State.URL != "" {
		return cfg.State.URL
	}
	return cfg.URL
}

func (o *desktopWindowOperator) Create(id string, cfg domain.BrowserInstanceConfig) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if bw, ok := o.windows[id]; ok && bw.win != nil {
		// Window already exists; just navigate to the current URL.
		bw.win.SetURL(currentPage(cfg))
		return nil
	}

	return o.createWindow(id, cfg, false)
}

func (o *desktopWindowOperator) Close(id string) error {
	o.mu.Lock()
	bw, ok := o.windows[id]
	o.mu.Unlock()
	if !ok || bw == nil || bw.win == nil {
		return nil
	}
	bw.win.Close()
	return nil
}

func (o *desktopWindowOperator) Navigate(id, url string) error {
	o.mu.Lock()
	bw, ok := o.windows[id]
	o.mu.Unlock()
	if !ok || bw.win == nil {
		return fmt.Errorf("desktop: browser window %q not found", id)
	}
	bw.win.SetURL(url)
	return nil
}

// inPlaceNavigationURL returns the URL a page-initiated new-window request
// (from the injected browser script's new-tab message) should navigate the
// standalone window to. Empty and browser-internal URLs (e.g. chrome-error
// pages) return "" so they are dropped instead of navigated.
func inPlaceNavigationURL(payload any) string {
	m, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	url, _ := m["url"].(string)
	if url == "" || IsBrowserInternalURL(url) {
		return ""
	}
	return url
}

// navigateInPlace routes a page-initiated new-window request back into the
// same standalone browser window: there are no frontend tabs here, and the
// previous behavior silently dropped the request.
func (o *desktopWindowOperator) navigateInPlace(id, url string) {
	o.mu.Lock()
	bw, ok := o.windows[id]
	o.mu.Unlock()
	if !ok || bw == nil || bw.win == nil {
		return
	}
	bw.win.SetURL(url)
}

func (o *desktopWindowOperator) UpdateConfig(id string, cfg domain.BrowserInstanceConfig) error {
	o.mu.Lock()
	bw, ok := o.windows[id]
	o.mu.Unlock()
	if !ok {
		return nil
	}
	bw.mu.Lock()
	bw.cfg = cfg
	bw.mu.Unlock()
	return nil
}

// closeWindow records final geometry, preserves or clears the Open flag, and
// removes the window from operator tracking without deleting the persisted
// instance. When preserveOpen is true (app shutdown) the window is restored on
// the next start; when false (user close) it stays closed until reopened.
func (o *desktopWindowOperator) closeWindow(id string, win *application.WebviewWindow, preserveOpen bool) {
	o.mu.Lock()
	bw, ok := o.windows[id]
	if !ok || bw == nil {
		o.mu.Unlock()
		return
	}
	if bw.flushTimer != nil {
		bw.flushTimer.Stop()
		bw.flushTimer = nil
	}
	if bw.pageStateFlushTimer != nil {
		bw.pageStateFlushTimer.Stop()
		bw.pageStateFlushTimer = nil
	}
	if win != nil {
		isMaximised := win.IsMaximised()
		state := bw.cfg.State
		state.Maximised = isMaximised
		if !isMaximised {
			x, y := win.Position()
			if x > -10000 && y > -10000 {
				state.X = int32(x)
				state.Y = int32(y)
				state.Width = int32(win.Width())
				state.Height = int32(win.Height())
			}
		}
		bw.cfg.State = state
	}
	if !preserveOpen {
		bw.cfg.Open = false
	}
	cfg := bw.cfg
	delete(o.windows, id)
	o.mu.Unlock()
	o.syncState(id, cfg)
}

func (o *desktopWindowOperator) recordGeometry(id string, win *application.WebviewWindow) {
	o.mu.Lock()
	bw, ok := o.windows[id]
	o.mu.Unlock()
	if !ok {
		return
	}

	isMaximised := win.IsMaximised()
	bw.mu.Lock()
	state := bw.cfg.State
	if !isMaximised {
		x, y := win.Position()
		if x > -10000 && y > -10000 {
			state.X = int32(x)
			state.Y = int32(y)
			state.Width = int32(win.Width())
			state.Height = int32(win.Height())
		}
	}
	state.Maximised = isMaximised
	bw.cfg.State = state
	cfg := bw.cfg
	bw.mu.Unlock()

	bw.mu.Lock()
	if bw.flushTimer != nil {
		bw.flushTimer.Stop()
	}
	bw.flushTimer = time.AfterFunc(browserWindowStateFlushDebounce, func() {
		o.syncState(id, cfg)
	})
	bw.mu.Unlock()
}

func (o *desktopWindowOperator) schedulePageStateFlushLocked(bw *browserWindow) {
	if bw.pageStateFlushTimer != nil {
		bw.pageStateFlushTimer.Stop()
	}
	bw.pageStateFlushTimer = time.AfterFunc(browserWindowStateFlushDebounce, func() {
		o.flushPageState(bw)
	})
}

func (o *desktopWindowOperator) flushPageState(bw *browserWindow) {
	bw.mu.Lock()
	if bw.pageStateFlushTimer != nil {
		bw.pageStateFlushTimer.Stop()
		bw.pageStateFlushTimer = nil
	}
	cfg := bw.cfg
	bw.mu.Unlock()
	o.syncState(bw.id, cfg)
}

func (o *desktopWindowOperator) removeInstance(id string) error {
	ref, ok := o.managerRef()
	if !ok {
		return fmt.Errorf("desktop: browsermanager service not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), browserInvokeTimeout)
	defer cancel()
	stream := ref.Invoke(ctx, "browsermanager.internal_remove", domain.BrowserManagerRemoveReq{ID: id})
	if stream == nil {
		return fmt.Errorf("desktop: browsermanager.remove invoke returned nil")
	}
	defer stream.Close()
	_, err := stream.RecvRaw()
	return err
}

func (o *desktopWindowOperator) DeleteProfile(id string) error {
	if err := os.RemoveAll(profileDir(id)); err != nil {
		return fmt.Errorf("desktop: delete browser profile %s: %w", id, err)
	}
	return nil
}

func (o *desktopWindowOperator) syncState(id string, cfg domain.BrowserInstanceConfig) {
	_ = id
	ref, ok := o.managerRef()
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), browserInvokeTimeout)
		defer cancel()
		stream := ref.Invoke(ctx, "browsermanager.internal_update_state", cfg)
		if stream == nil {
			return
		}
		defer stream.Close()
		_, _ = stream.RecvRaw()
	}()
}

func (o *desktopWindowOperator) managerRef() (ref.Ref, bool) {
	if o.handle == nil {
		return nil, false
	}
	return o.handle.App().Service().Lookup("browsermanager")
}

func (o *desktopWindowOperator) SyncPageState(id, url, title string) {
	o.mu.Lock()
	bw := o.windows[id]
	o.mu.Unlock()
	if bw == nil {
		return
	}
	bw.mu.Lock()
	if url != "" && !IsBrowserInternalURL(url) {
		// Page state tracks the current navigation page (State.URL) only;
		// cfg.URL is the settings page and must survive navigation.
		bw.cfg.State.URL = url
	}
	if title != "" {
		bw.cfg.State.Title = title
	}
	o.schedulePageStateFlushLocked(bw)
	bw.mu.Unlock()
}

// RequestSnapshot injects the reader script into the given browser window,
// which causes a page-snapshot to be sent back via postMessage.
func (o *desktopWindowOperator) RequestSnapshot(id string) error {
	o.mu.Lock()
	bw, ok := o.windows[id]
	o.mu.Unlock()
	if !ok || bw.win == nil {
		return fmt.Errorf("desktop: browser window %q not found", id)
	}
	bw.win.ExecJS(readerScript)
	return nil
}

// GetSnapshot returns the latest captured page snapshot for the given instance.
func (o *desktopWindowOperator) GetSnapshot(id string) (*PageSnapshot, error) {
	snap := o.snapshots.get(id)
	if snap == nil {
		return nil, fmt.Errorf("desktop: no snapshot for instance %q yet", id)
	}
	return snap, nil
}

// handlePageStateMessage parses a page-state postMessage from a webview and
// persists it in the instance's BrowserWindowState.PageState.
func (o *desktopWindowOperator) handlePageStateMessage(id, message string) bool {
	var raw map[string]any
	if err := json.Unmarshal([]byte(message), &raw); err != nil {
		return false
	}
	if raw["type"] != "page-state" {
		return false
	}

	o.mu.Lock()
	bw, ok := o.windows[id]
	o.mu.Unlock()
	if !ok || bw == nil {
		return true
	}

	bw.mu.Lock()
	state := bw.cfg.State
	if x, ok := raw["scrollX"].(float64); ok {
		state.PageState.ScrollX = int32(x)
	}
	if y, ok := raw["scrollY"].(float64); ok {
		state.PageState.ScrollY = int32(y)
	}
	if z, ok := raw["zoom"].(float64); ok {
		state.PageState.Zoom = z
	}
	if d, ok := raw["data"].(string); ok {
		state.PageState.Data = d
	}
	bw.cfg.State = state
	o.schedulePageStateFlushLocked(bw)
	bw.mu.Unlock()
	return true
}

// restorePageState injects JS that restores the previously persisted scroll
// position, zoom, and optional page data after navigation/refresh.
func (o *desktopWindowOperator) restorePageState(id string, ps domain.BrowserPageState) {
	if ps.ScrollX == 0 && ps.ScrollY == 0 && ps.Zoom == 0 && ps.Data == "" {
		return
	}
	o.mu.Lock()
	bw := o.windows[id]
	o.mu.Unlock()
	if bw == nil || bw.win == nil {
		return
	}
	zoom := ps.Zoom
	if zoom == 0 {
		zoom = 1
	}
	script := fmt.Sprintf(`(function() {
	try {
		window.scrollTo(%d, %d);
		document.documentElement.style.zoom = %q;
		if (%q) {
			window.__sporemindPageStateData = JSON.parse(%q);
			var event = document.createEvent('Event');
			event.initEvent('sporemindPageStateData', true, true);
			window.dispatchEvent(event);
		}
	} catch(e) {}
})();`, ps.ScrollX, ps.ScrollY, strconv.FormatFloat(zoom, 'f', -1, 64), ps.Data, ps.Data)
	bw.win.ExecJS(script)
}

// handleBrowserObserveMessage parses a browser-observe postMessage and delivers
// the parsed BrowserPageObservation to the waiting Observe call. Returns true if
// the message was a browser-observe payload and was handled.
func (o *desktopWindowOperator) handleBrowserObserveMessage(id, message string) bool {
	var raw map[string]any
	if err := json.Unmarshal([]byte(message), &raw); err != nil {
		return false
	}
	if raw["type"] != "browser-observe" {
		return false
	}
	obsID, _ := raw["obsId"].(string)
	if obsID == "" {
		return true
	}

	o.mu.Lock()
	ch, ok := o.observeCallbacks[obsID]
	delete(o.observeCallbacks, obsID)
	o.mu.Unlock()
	if !ok || ch == nil {
		return true // recognized but no one waiting
	}

	if errMsg, _ := raw["error"].(string); errMsg != "" {
		ch <- nil
		return true
	}

	obs := &domain.BrowserPageObservation{
		ObservationID:     getString(raw, "obsId"),
		URL:               getString(raw, "url"),
		Title:             getString(raw, "title"),
		LoadState:         getString(raw, "loadState"),
		Timestamp:         getString(raw, "timestamp"),
		HistoryLength:     int32(getFloat(raw, "historyLength")),
		ViewportWidth:     int32(getFloat(raw, "viewportWidth")),
		ViewportHeight:    int32(getFloat(raw, "viewportHeight")),
		ScrollX:           int32(getFloat(raw, "scrollX")),
		ScrollY:           int32(getFloat(raw, "scrollY")),
		TotalElementCount: int32(getFloat(raw, "totalElementCount")),
	}

	if els, ok := raw["elements"].([]any); ok {
		for _, e := range els {
			if m, ok := e.(map[string]any); ok {
				el := domain.BrowserElement{
					ID:               getString(m, "Id"),
					TagName:          getString(m, "TagName"),
					Selector:         getString(m, "Selector"),
					XPath:            getString(m, "XPath"),
					Text:             getString(m, "Text"),
					Placeholder:      getString(m, "Placeholder"),
					Role:             getString(m, "Role"),
					AriaLabel:        getString(m, "AriaLabel"),
					InputType:        getString(m, "InputType"),
					Value:            getString(m, "Value"),
					Src:              getString(m, "Src"),
					Href:             getString(m, "Href"),
					Color:            getString(m, "Color"),
					BackgroundColor:  getString(m, "BackgroundColor"),
					ActionHint:       getString(m, "ActionHint"),
					IsVisible:        getBool(m, "IsVisible"),
					IsEnabled:        getBool(m, "IsEnabled"),
					IsFocusable:      getBool(m, "IsFocusable"),
					IsChecked:        getBool(m, "IsChecked"),
					FontSize:         getFloat(m, "FontSize"),
					FontWeight:       getFloat(m, "FontWeight"),
					ActionConfidence: getFloat(m, "ActionConfidence"),
					ChildCount:       int32(getFloat(m, "ChildCount")),
					SiblingIndex:     int32(getFloat(m, "SiblingIndex")),
					ParentID:         getString(m, "ParentId"),
				}
				if rect, ok := m["Rect"].(map[string]any); ok {
					el.Rect = domain.BrowserElementRect{
						X:      int32(getFloat(rect, "x")),
						Y:      int32(getFloat(rect, "y")),
						Width:  int32(getFloat(rect, "width")),
						Height: int32(getFloat(rect, "height")),
					}
				}
				obs.Elements = append(obs.Elements, el)
			}
		}
	}

	ch <- obs
	return true
}

// observeCallScript invokes the observeScript-installed entrypoint with the
// Go-generated correlation ID (double-quoted so it round-trips as a JS string).
// The entrypoint guard keeps the call a no-op if the setup script was wiped by
// a navigation between the two ExecJS calls.
func observeCallScript(obsID string) string {
	return fmt.Sprintf("window.__sporemindObserve && window.__sporemindObserve(%q);", obsID)
}

// Observe injects the observeScript to extract a structured page observation with
// element refs. It waits for the JS response with a timeout.
func (o *desktopWindowOperator) Observe(id string) (*domain.BrowserPageObservation, error) {
	o.mu.Lock()
	bw, ok := o.windows[id]
	o.mu.Unlock()
	if !ok || bw.win == nil {
		return nil, fmt.Errorf("desktop: browser window %q not found", id)
	}

	// Register a pending callback before injecting the script.
	resultCh := make(chan *domain.BrowserPageObservation, 1)
	obsID := fmt.Sprintf("obs-%d", time.Now().UnixNano())
	o.mu.Lock()
	o.observeCallbacks[obsID] = resultCh
	o.mu.Unlock()

	// Cancel on timeout.
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()

	// Inject the observe entrypoint, then invoke it with the Go-generated
	// correlation ID; the script echoes it back via chrome.webview.postMessage
	// so handleBrowserObserveMessage resolves the pending callback above.
	bw.win.ExecJS(observeScript)
	bw.win.ExecJS(observeCallScript(obsID))

	select {
	case obs := <-resultCh:
		if obs == nil {
			return nil, fmt.Errorf("desktop: observe %q: script error", id)
		}
		// Attach a native window capture; best-effort (see attachScreenshot).
		o.attachScreenshot(bw, obs)
		return obs, nil
	case <-timeout.C:
		o.mu.Lock()
		delete(o.observeCallbacks, obsID)
		o.mu.Unlock()
		return nil, fmt.Errorf("desktop: observe %q: timeout", id)
	}
}

// Use executes a constrained browser action (click/type/scroll/wait) using a
// structured request. It builds a JS payload and injects it, then waits for
// the result and returns an updated observation.
func (o *desktopWindowOperator) Use(id string, req domain.BrowserUseReq) (*domain.BrowserUseResp, error) {
	o.mu.Lock()
	bw, ok := o.windows[id]
	o.mu.Unlock()
	if !ok || bw.win == nil {
		return nil, fmt.Errorf("desktop: browser window %q not found", id)
	}

	// Validate navigate URL scheme before reaching the page-side script.
	if msg := validateNavigateURL(req.Action, req.URL, req.NavigateMode); msg != "" {
		return &domain.BrowserUseResp{
			Success:   false,
			Message:   msg,
			ErrorCode: "invalid_url",
		}, nil
	}

	// Validate per-action required fields before reaching the page-side script.
	if msg := validateUseAction(req); msg != "" {
		return &domain.BrowserUseResp{
			Success:   false,
			Message:   msg,
			ErrorCode: "invalid_request",
		}, nil
	}

	// URL navigations run natively (SetURL) and wait for the navigation to
	// complete before returning. The page-side useScript path sets
	// location.href and posts its result from a document that is about to be
	// torn down — the postMessage can be lost, leaving every navigate to time
	// out at the 15s operator deadline (observed on both mounted and hidden
	// windows). Returning after WebViewNavigationCompleted also makes the
	// observe-after-navigate closed loop read the NEW document. History
	// operations (back/forward/reload) need the page context and keep the JS
	// path.
	if req.Action == "navigate" && req.NavigateMode != "back" && req.NavigateMode != "forward" && req.NavigateMode != "reload" {
		waitCh, cancelWait := o.registerNavWaiter(id)
		defer cancelWait()
		bw.win.SetURL(req.URL)
		select {
		case <-waitCh:
			return &domain.BrowserUseResp{
				Success: true,
				Message: "navigate: " + req.URL,
			}, nil
		case <-time.After(navWaitTimeout):
			return &domain.BrowserUseResp{
				Success: true,
				Message: "navigate: " + req.URL + " (navigation still in progress after " + navWaitTimeout.String() + ")",
			}, nil
		}
	}

	// Build the constrained JS payload from the structured request.
	payload := buildUsePayload(req)

	// Inject the useScript setup (once per window lifetime is fine, idempotent).
	bw.win.ExecJS(useScript)

	// Register callback before injecting the action payload. payload.ObsID is
	// already prefixed ("use-<nanos>") and is echoed verbatim by the page-side
	// useScript as result.obsId; handleBrowserUseResult looks up by that exact
	// value, so the key must NOT be re-prefixed (a stale "use-"+payload.ObsID
	// left every interactive action timing out at the 15s operator deadline).
	useResultCh := make(chan BrowserUseResult, 1)
	cancel := o.snapshots.RegisterCallback(payload.ObsID, func(r BrowserUseResult) {
		useResultCh <- r
	})
	defer cancel()

	// Run the action via the injected __sporemindUse.run function.
	actionScript := fmt.Sprintf(`window.__sporemindUse.run(%s);`, payload.JSON)
	bw.win.ExecJS(actionScript)

	// Wait for the result with timeout.
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()

	select {
	case result := <-useResultCh:
		resp := &domain.BrowserUseResp{
			Success: result.Success,
			Message: result.Message,
		}
		return resp, nil
	case <-timeout.C:
		return &domain.BrowserUseResp{
			Success:   false,
			Message:   "browser.use: timeout",
			ErrorCode: "timeout",
		}, nil
	}
}

// ExportCookies enumerates all cookies from the WebView2 CookieManager for the
// given instance and returns them grouped by domain. If the browser window is
// not open, a hidden temporary window is created for cookie access and then
// torn down, leaving the original open state intact.
func (o *desktopWindowOperator) ExportCookies(id string) (map[string][]domain.BrowserCookieEntry, error) {
	win, cleanup, err := o.cookieWindow(id)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return exportCookiesForWindow(win)
}

// ImportCookies writes the domain-grouped cookies into the WebView2
// CookieManager for the given instance. It returns the number of cookies
// successfully written. If the browser window is not open, a hidden temporary
// window is created for cookie access and then torn down.
func (o *desktopWindowOperator) ImportCookies(id string, cookies map[string][]domain.BrowserCookieEntry) (int64, error) {
	win, cleanup, err := o.cookieWindow(id)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	return importCookiesForWindow(win, cookies)
}

// cookieWindow returns the browser window for the given instance, creating a
// hidden temporary window if none is currently open. The returned cleanup
// function must be called to tear down the temp window (if one was created) and
// prevent its state from leaking into the persisted manager record.
func (o *desktopWindowOperator) cookieWindow(id string) (win *application.WebviewWindow, cleanup func(), err error) {
	o.mu.Lock()
	bw, ok := o.windows[id]
	o.mu.Unlock()
	if ok && bw != nil && bw.win != nil {
		// Existing window — return it with a no-op cleanup.
		return bw.win, func() {}, nil
	}

	// Create a hidden temporary window for cookie access.
	tempCfg := domain.BrowserInstanceConfig{
		ID:     id,
		Name:   "cookie-access",
		URL:    "about:blank",
		Open:   true,
		Hidden: true,
		Mode:   "window",
		State:  domain.BrowserWindowState{URL: "about:blank", Title: "cookie-access", Width: 800, Height: 600},
	}
	o.mu.Lock()
	err = o.createWindow(id, tempCfg, true)
	o.mu.Unlock()
	if err != nil {
		return nil, nil, fmt.Errorf("desktop: create hidden cookie window %q: %w", id, err)
	}
	o.mu.Lock()
	bw, ok = o.windows[id]
	o.mu.Unlock()
	if !ok || bw == nil || bw.win == nil {
		o.closeTempWindow(id)
		return nil, nil, fmt.Errorf("desktop: hidden cookie window %q not ready", id)
	}
	return bw.win, func() { o.closeTempWindow(id) }, nil
}

// closeTempWindow closes a temporary browser window without syncing its state
// to the browsermanager, preventing the temp config from corrupting the
// persisted record. The window is removed from the operator tracking map first
// so that the Wails WindowClosing event handler finds no entry and skips sync.
func (o *desktopWindowOperator) closeTempWindow(id string) {
	o.mu.Lock()
	bw, ok := o.windows[id]
	if !ok || bw == nil {
		o.mu.Unlock()
		return
	}
	if bw.flushTimer != nil {
		bw.flushTimer.Stop()
		bw.flushTimer = nil
	}
	if bw.pageStateFlushTimer != nil {
		bw.pageStateFlushTimer.Stop()
		bw.pageStateFlushTimer = nil
	}
	win := bw.win
	delete(o.windows, id)
	o.mu.Unlock()
	if win != nil {
		win.Close()
	}
}

// createWindow is the shared window-creation helper. When temp is true the
// window is created hidden and is not intended to survive past cookie operations.
func (o *desktopWindowOperator) createWindow(id string, cfg domain.BrowserInstanceConfig, temp bool) error {
	userDataPath := profileDir(id)
	if err := os.MkdirAll(userDataPath, 0755); err != nil {
		return fmt.Errorf("desktop: create browser profile dir for %s: %w", id, err)
	}

	opts := application.WebviewWindowOptions{
		Title:  cfg.Name,
		Width:  int(cfg.State.Width),
		Height: int(cfg.State.Height),
		X:      int(cfg.State.X),
		Y:      int(cfg.State.Y),
		URL:    currentPage(cfg),
		Hidden: cfg.Hidden,
		Windows: application.WindowsWindow{
			WebviewUserDataPath: userDataPath,
			HiddenOnTaskbar:     cfg.Hidden,
		},
	}
	if opts.Width <= 0 {
		opts.Width = 1024
	}
	if opts.Height <= 0 {
		opts.Height = 768
	}
	if cfg.State.Maximised {
		opts.StartState = application.WindowStateMaximised
	}

	// Independent profile: cookies/login state only; the HTTP disk cache is
	// centralized under the global profile directory.
	browserArgs := appendSharedCacheArg(appendProxyArgs(slices.Clone(browserBaseArgs), cfg))
	// Temp cookie windows load about:blank and (below) never get a
	// MessageHandler; injecting here would make the page post wails:
	// lifecycle messages into wails' unknown-message error branch.
	if !temp {
		opts.JS = browserInjectScript() + "\n" + browserStateScript()
	}
	opts.Windows.AdditionalBrowserArgs = browserArgs

	win := o.app.Window.NewWithOptions(opts)
	bw := &browserWindow{win: win, id: id, cfg: cfg}
	o.windows[id] = bw

	// Temp windows exist solely to host the WebView2 cookie manager — they never
	// need page messages. Registering the handler let their page-state messages
	// flow into syncState and overwrite the persisted instance record with the
	// temp cfg (cookie-access/about:blank/open=true), so skip it entirely.
	if !temp {
		win.MessageHandler = func(message string) {
			const prefix = "wails:"
			// Automation scripts (observe/use/reader/state) prefix their
			// postMessages with "wails:" so the Wails dispatcher routes them
			// here (unprefixed messages go to the gospore gateway transport
			// and are swallowed as unknown envelopes). Strip the prefix and
			// fall through to the automation handlers below when the message
			// is not a recognised lifecycle type (navigated/page-info/new-tab).
			raw := message
			if strings.HasPrefix(message, prefix) {
				raw = message[len(prefix):]
				var payload struct {
					Type    string `json:"type"`
					Payload any    `json:"payload"`
				}
				if err := json.Unmarshal([]byte(raw), &payload); err == nil {
					switch payload.Type {
					case "navigated":
						if m, ok := payload.Payload.(map[string]any); ok {
							if url, ok := m["url"].(string); ok && url != "" {
								o.onEvent(id, url, "")
							}
						}
						return
					case "page-info":
						if m, ok := payload.Payload.(map[string]any); ok {
							url, _ := m["url"].(string)
							title, _ := m["title"].(string)
							if url != "" || title != "" {
								o.onEvent(id, url, title)
							}
						}
						return
					case "new-tab":
						if url := inPlaceNavigationURL(payload.Payload); url != "" {
							o.navigateInPlace(id, url)
						}
						return
					}
				}
			}
			if o.handlePageStateMessage(id, raw) {
				return
			}
			if o.handleBrowserObserveMessage(id, raw) {
				return
			}
			if o.snapshots.handleBrowserUseResult(raw) {
				return
			}
			o.snapshots.handleSnapshotMessage(id, raw)
		}
	}

	// Only register lifecycle events for non-temp windows. Temp windows are
	// short-lived and their state must not be synced to the manager.
	if !temp {
		win.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(_ *application.WindowEvent) {
			o.notifyNavCompleted(id)
			o.restorePageState(id, bw.cfg.State.PageState)
		})

		for _, ev := range []events.WindowEventType{
			events.Common.WindowDidResize,
			events.Common.WindowDidMove,
			events.Common.WindowMaximise,
			events.Common.WindowUnMaximise,
			events.Common.WindowRestore,
		} {
			win.OnWindowEvent(ev, func(_ *application.WindowEvent) {
				o.recordGeometry(id, win)
			})
		}

		win.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
			o.closeWindow(id, win, o.shuttingDown)
		})
	}
	return nil
}

// usePayload carries the data sent to the page-side useScript.
type usePayload struct {
	JSON        string
	ObsID       string
	WaitMs      int32
	IsAsyncWait bool
}

func buildUsePayload(req domain.BrowserUseReq) usePayload {
	// Build a minimal JSON payload with only the fields relevant to the action.
	m := map[string]any{
		"action": req.Action,
	}
	if req.ElementID != "" {
		m["elementId"] = req.ElementID
	}
	if req.ElementText != "" {
		m["elementText"] = req.ElementText
	}
	if req.ElementSelector != "" {
		m["elementSelector"] = req.ElementSelector
	}
	if req.ClickX != 0 {
		m["clickX"] = req.ClickX
	}
	if req.ClickY != 0 {
		m["clickY"] = req.ClickY
	}
	if req.Text != "" {
		m["text"] = req.Text
	}
	if req.Append {
		m["append"] = true
	}
	if req.Submit {
		m["submit"] = true
	}
	if req.ScrollX != 0 {
		m["scrollX"] = req.ScrollX
	}
	if req.ScrollY != 0 {
		m["scrollY"] = req.ScrollY
	}
	if req.ScrollTarget != "" {
		m["scrollTarget"] = req.ScrollTarget
	}
	if req.URL != "" {
		m["url"] = req.URL
	}
	if req.NavigateMode != "" {
		m["navigateMode"] = req.NavigateMode
	}
	if req.Key != "" {
		m["key"] = req.Key
	}
	if req.WaitMs != 0 {
		m["waitMs"] = req.WaitMs
	}
	if req.WaitFor != "" {
		m["waitFor"] = req.WaitFor
	}
	if req.WaitElementID != "" {
		m["waitElementId"] = req.WaitElementID
	}
	if len(req.Modifiers) > 0 {
		m["modifiers"] = req.Modifiers
	}
	if req.FilePath != "" {
		m["filePath"] = req.FilePath
	}
	if req.DragTargetElementID != "" {
		m["dragTargetElementId"] = req.DragTargetElementID
	}
	// req.TimeoutMs is intentionally not mapped: the page-side useScript has no
	// consumer for it (dead field — same status WaitFor had before I1
	// activated it), so forwarding it would be silent noise.

	// obsID must match the pattern used by useScript (prefixed)
	obsID := fmt.Sprintf("use-%d", time.Now().UnixNano())
	m["obsId"] = obsID

	jsonBytes, _ := json.Marshal(m)
	return usePayload{
		JSON:   string(jsonBytes),
		ObsID:  obsID,
		WaitMs: req.WaitMs,
	}
}

// validateNavigateURL checks that a navigate action targeting a URL uses an
// http, https or file scheme. Returns an error message string (empty = valid).
// back/forward/reload modes do not require a URL.
func validateNavigateURL(action, rawURL, navigateMode string) string {
	if action != "navigate" {
		return ""
	}
	switch navigateMode {
	case "back", "forward", "reload":
		return ""
	}
	if rawURL == "" {
		return "navigate: url required"
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return "navigate: url has no scheme"
	}
	switch u.Scheme {
	case "http", "https", "file":
	default:
		return "navigate: url scheme must be http, https or file (got " + u.Scheme + ")"
	}
	return ""
}

// validateUseAction checks per-action required fields for the constrained
// browser actions. Returns an error message string (empty = valid). Unknown
// actions fall through to the page-side script, which reports them itself.
func validateUseAction(req domain.BrowserUseReq) string {
	switch req.Action {
	case "drag":
		if req.DragTargetElementID == "" && (req.ClickX == 0 || req.ClickY == 0) {
			return "drag: dragTargetElementId or clickX+clickY required"
		}
	case "scroll_to":
		if req.ElementID == "" {
			return "scroll_to: elementId required"
		}
	case "hover", "focus":
		if req.ElementID == "" {
			return req.Action + ": elementId required"
		}
	case "file_upload":
		if req.FilePath == "" {
			return "file_upload: filePath required"
		}
	}
	return ""
}

// attachScreenshot captures the browser window content via the native GDI path
// (win.NativeWindow() + captureWindowRegion, see screenshot_windows.go) and
// fills the observation's Screenshot bytes plus pixel dimensions. It is
// best-effort: a failed capture (hidden window, non-Windows build, occluded
// window) leaves the Screenshot fields empty rather than failing the
// observation — the page observation itself is still valid.
func (o *desktopWindowOperator) attachScreenshot(bw *browserWindow, obs *domain.BrowserPageObservation) {
	if bw == nil || bw.win == nil || obs == nil {
		return
	}
	dataURL, err := captureWindowRegion(bw.win.NativeWindow())
	if err != nil {
		return
	}
	pngBytes, width, height, err := decodeScreenshotDataURL(dataURL)
	if err != nil {
		return
	}
	obs.Screenshot = pngBytes
	obs.ScreenshotWidth = width
	obs.ScreenshotHeight = height
}

// decodeScreenshotDataURL parses a "data:image/png;base64,..." screenshot data
// URL into raw PNG bytes plus the image's pixel dimensions.
func decodeScreenshotDataURL(dataURL string) (data []byte, width, height int32, err error) {
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		return nil, 0, 0, fmt.Errorf("desktop: unexpected screenshot data URL prefix")
	}
	data, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("desktop: decode screenshot base64: %w", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("desktop: decode screenshot dimensions: %w", err)
	}
	return data, int32(cfg.Width), int32(cfg.Height), nil
}

// getFloat extracts a float64 from a map, returning 0 if absent or wrong type.
func getFloat(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	if v, ok := m[key].(int); ok {
		return float64(v)
	}
	if v, ok := m[key].(int64); ok {
		return float64(v)
	}
	return 0
}
