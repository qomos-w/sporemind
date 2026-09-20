package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/resource"
	"github.com/qomos-w/sporemind/pkg/actor/browserinstance"
	"github.com/qomos-w/sporemind/pkg/actor/user"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/debug"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/logging"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const desktopWindowStateKey = "desktop-window"

// globalWindowKey is the stable WindowOperator key under which the shared
// Global browser tab's WebView2 window is registered, matching the "global"
// InstanceID default of browsermanager.use.
const globalWindowKey = "global"

const (
	gmemMoveable = 0x0002
	gmemZeroInit = 0x0040
	cfDIB        = 8
	cfHDROP      = 15
)

type App struct {
	handle       *runtime.Handle
	cancel       context.CancelFunc
	app          *application.App
	window       *application.WebviewWindow
	winState     domain.DesktopWindowState
	logRing      *logging.Ring
	logStore     logging.LogStore
	consoleStore *logging.ConsoleStore

	// Rate limiter for console-store write failure logging (WriteConsoleLog).
	consoleWriteErrMu      sync.Mutex
	lastConsoleWriteErrLog time.Time

	// bootCrash is the previous session's crash report, captured by
	// BeginCrashSession in main before the window exists. Read-only from
	// then on; surfaced to the frontend via BootCrashReport.
	bootCrash *BootCrashReport

	evalJSMu  sync.Mutex
	evalJSChs map[string]chan string

	// dragOutMu serialises native drag-out operations — DoDragDrop is modal
	// and captures the mouse, so a second concurrent drag is meaningless.
	dragOutMu sync.Mutex

	winStateMu         sync.Mutex
	winStateFlushTimer *time.Timer

	screenshotWindow *application.WebviewWindow
	screenshotData   *ScreenshotData

	browserSessions    map[string]*browserSession
	browserMu          sync.Mutex
	mainWindowActive   bool // main window is focused & not minimised
	mainWindowResizing bool // main window is being resized; suppress showing child browsers
	browserDark        bool // right-panel browser windows render dark mode
	// browserOverlaySuppressed is raised while an HTML overlay is open (last
	// overlay push, cleared by the last pop). Unlike wanted it is persistent:
	// sessions created or first-painted while it is set (e.g. the boot-time
	// tab restore racing the crash overlay) stay hidden instead of covering
	// the overlay on their first paint.
	browserOverlaySuppressed bool

	browserOp *desktopWindowOperator

	wailsTransport *wailsTransportManager

	// loginCallbackServer receives the OAuth token redirect from the cloud
	// side after the user completes login in the system default browser.
	loginCallbackMu        sync.Mutex
	loginCallbackServer    *http.Server
	loginCallbackURL       string
	loginCallbackStartedAt time.Time
	loginCallbackTimer     *time.Timer
	loginCallbackTimeout   time.Duration                           // 0 => default 5m; settable for tests
	loginCallbackTimerFn   func(time.Duration, func()) *time.Timer // test seam
}

// browserSession tracks one child browser window and its desired visibility.
type browserSession struct {
	id             string // unique session identifier
	kind           string // "global" | "independent" | "app"
	window         *application.WebviewWindow
	wanted         bool                         // frontend's desired visibility
	painted        bool                         // current page has rendered at least one frame
	paintFallback  *time.Timer                  // reveal window if the first-paint signal is lost
	pageStateFlush *time.Timer                  // debounced page-state persist (scroll/zoom/data)
	shown          bool                         // window is currently visible on screen
	title          string                       // last known page title (current confirmed document only)
	canBack        bool                         // last known history availability
	canForward     bool                         // last known history availability
	cfg            domain.BrowserInstanceConfig // cached instance config (independent only)

	// Navigation state machine (contract §1 / §2.5). confirmedURL is the single
	// source of truth for the current document URL and the only URL written back
	// to the address bar or persisted; it is set exclusively by a
	// NavigationCompleted commit (§3). pendingNavigation is non-nil while a
	// navigation is in flight and carries the commandToken correlation identity
	// (§2.5). nativeNavID is the active navigation's WebView2 NavigationId, bound
	// via commandToken (host command, §2.5.3) or page-initiated supersede (§2.5.4).
	confirmedURL        string
	pendingNavigation   *pendingNav // non-nil while loading; nil at idle/loaded/failed
	navigationID        uint64      // host-side monotonic nav epoch
	nativeNavID         uint64      // active navigation's native NavigationId; 0 = unknown
	lastSeenNativeNavID uint64      // highest observed native NavId (diagnostic only, §2.5.4)
	navigationStatus    navStatus   // idle | loading | loaded | failed
	lastError           string
	// attemptedURL is the URL of the last FAILED navigation. The address bar
	// must faithfully reflect what the webview is showing: after a failed
	// navigation the webview sits on the attempted URL's error page, so the
	// state payload carries it separately from confirmedURL (which stays at
	// the last good document for reload/back-forward semantics).
	attemptedURL string

	// navAdopted records that the last commitNavigation adopted a NavId-mismatched
	// Completed for an unbound pending (repair path). Diagnostic only, read by
	// the Completed handler for logging.
	navAdopted bool

	// navWatchdog is the completion-watchdog timer: armed whenever a pending
	// starts (begin or Starting bind), it reconciles a still-loading pending
	// against the webview's Source when its NavigationCompleted never arrives.
	navWatchdog *time.Timer
}

// windowStateFlushDebounce coalesces the burst of DidResize/DidMove events
// fired during a drag into a single store write.
const windowStateFlushDebounce = 400 * time.Millisecond

func New(handle *runtime.Handle, cancel context.CancelFunc, logRing *logging.Ring, logStore logging.LogStore, consoleStore *logging.ConsoleStore) *App {
	app := &App{
		handle:          handle,
		cancel:          cancel,
		logRing:         logRing,
		logStore:        logStore,
		consoleStore:    consoleStore,
		evalJSChs:       make(map[string]chan string),
		browserSessions: make(map[string]*browserSession),
		wailsTransport:  newWailsTransportManager(handle),
	}
	if err := app.loadWindowState(); err != nil {
		app.desktopLog("warn", "load window state failed", map[string]any{"err": err})
	}
	return app
}

// RawMessageHandler returns the Wails v3 RawMessageHandler closure that feeds
// frontend raw messages into the gospore gateway via a strictly-ordered queue.
func (a *App) RawMessageHandler() func(window application.Window, message string, originInfo *application.OriginInfo) {
	return a.wailsTransport.RawMessageHandler()
}

// Bind wires the Wails v3 application handle so the service can reach the
// event/dialog/clipboard/browser managers. Called from main.go after
// application.New but before app.Run.
func (a *App) Bind(app *application.App) {
	a.app = app
	SetCrashApp(app)
	SetCrashBrowserSuppressor(a.HideAllBrowserWindows)
}

// SetWindow wires the primary webview window. Called from main.go after the
// window is created but before app.Run so ServiceStartup can use it.
func (a *App) SetWindow(win *application.WebviewWindow) {
	a.window = win
	SetCrashWindow(win)
}

// FocusMainWindow re-activates the main window. Invoked in the first instance
// when a second instance launches (Wails SingleInstance).
func (a *App) FocusMainWindow() {
	if a.window == nil {
		return
	}
	a.window.Show()
	a.window.Restore()
	a.window.Focus()
}

// desktopLog writes a structured entry to the ring when available,
// otherwise falls back to stdlib log so the message is never lost.
func (a *App) desktopLog(level, msg string, fields map[string]any) {
	if a.logRing != nil {
		a.logRing.Append(gateway.LogEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Level:     level,
			Message:   msg,
			Fields:    fields,
		})
	} else {
		log.Printf("sporemind: %s", msg)
	}
}

// SetBootCrashReport wires the previous session's crash scan result.
// Called from main after application.New, before any window exists.
func (a *App) SetBootCrashReport(r *BootCrashReport) {
	a.bootCrash = r
}

// BootCrashReport returns how the previous session ended. Called by the
// frontend once per webview load, so the dynamic parts are re-derived from the
// crash store: acknowledged records and error-tail lines stay dismissed
// instead of replaying the boot-time snapshot on every refresh.
func (a *App) BootCrashReport() *BootCrashReport {
	if a.bootCrash == nil {
		return &BootCrashReport{}
	}
	r := *a.bootCrash
	r.Records, r.AbnormalExit = crashRecordsView(crashStoreDir(), a.bootCrash.AbnormalExit, a.bootCrash.PreviousSession)
	r.ErrorsTail = crashErrorsTailLines(crashStoreDir(), crashErrorsTailN)
	return &r
}

// AckCrashRecords marks the given crash records as acknowledged so they stop
// being reported on subsequent startups.
func (a *App) AckCrashRecords(ids []string) {
	ackCrashRecords(crashStoreDir(), ids)
}

// Shutdown cancels the gospore context and waits for the actor tree to stop.
// Called from Wails OnShutdown so the process exits when the window closes.
// If the actor tree does not stop within 5s the process is force-killed.
func (a *App) Shutdown() {
	if a.browserOp != nil {
		a.browserOp.shuttingDown = true
	}
	if a.wailsTransport != nil {
		a.wailsTransport.signalCloseAll()
	}
	start := time.Now()
	if a.cancel != nil {
		a.cancel()
	}
	done := make(chan struct{})
	go func() {
		_ = a.handle.Wait()
		close(done)
	}()
	select {
	case <-done:
		elapsed := time.Since(start)
		a.desktopLog("info", "shutdown completed", map[string]any{"elapsed_ms": elapsed.Milliseconds()})
		fmt.Fprintf(os.Stderr, "sporemind: shutdown completed in %s\n", elapsed)
	case <-time.After(5 * time.Second):
		elapsed := time.Since(start)
		a.desktopLog("error", "shutdown timed out, forcing exit", map[string]any{"elapsed_ms": elapsed.Milliseconds()})
		fmt.Fprintf(os.Stderr, "sporemind: shutdown timed out after %s, forcing exit\n", elapsed)
	}
	// A shutdown completing (even via the force-kill timeout branch) is a
	// deliberate exit, not a crash — release the session marker so the next
	// startup does not report an abnormal exit.
	EndCrashSession()
	os.Exit(0)
}

// ClosePreviousInstance sends a shutdown request to any previously running
// sporemind instance via the /debug/shutdown endpoint. Failures are logged
// but not fatal — there may simply be no previous instance.
// Call this BEFORE Bootstrap to release the gateway port.
// Only used in dev mode (hot-reload needs the new process to take over the
// gateway port). Release builds use Wails SingleInstance (named mutex) instead.
func ClosePreviousInstance() {
	addr := config.GatewayAddr()
	hostPort := addr
	if strings.HasPrefix(addr, ":") {
		hostPort = "localhost" + addr
	}
	url := "http://" + hostPort + "/debug/shutdown"

	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Println("sporemind: closed previous instance, waiting for port release")
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
			resp2, err := http.Get("http://" + hostPort + "/healthz")
			if err != nil {
				return
			}
			resp2.Body.Close()
		}
		log.Println("sporemind: timeout waiting for previous instance")
	}
}

// ServiceStartup is the Wails v3 service lifecycle hook. Runs after the app
// and primary window are created, before app.Run enters the event loop.
func (a *App) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	a.restoreWindowState()
	a.registerMainWindowEvents()

	debug.EvalJS = func(c context.Context, script string) (any, error) {
		return a.evalJS(c, script)
	}

	appbinding.DesktopFilePicker = a.openFileDialogPicker
	appbinding.DesktopFolderPicker = a.openFolderDialogPicker
	appbinding.DesktopFileSavePicker = a.saveFileDialogPicker
	appbinding.DesktopClipboardWriter = a.clipboardWrite
	appbinding.DesktopClipboardReader = a.clipboardRead

	a.browserOp = newDesktopWindowOperator(a.app, a.handle, func(id, url, title string) {
		if IsBrowserInternalURL(url) {
			return
		}
		if url != "" || title != "" {
			a.browserOp.SyncPageState(id, url, title)
		}
		if url != "" {
			a.app.Event.Emit("browser:navigated:"+id, url)
		}
		if url != "" || title != "" {
			a.app.Event.Emit("browser:page-info:"+id, map[string]string{"u": url, "t": title})
		}
	})
	browserinstance.SetWindowOperator(a.browserOp)

	a.startUpdateLoop()

	return nil
}

// RegisterExternal hands an externally-owned WebView2 window (e.g. the desktop
// Global tab) to the operator for observation/automation WITHOUT creating a
// new window. The window keeps its owner's lifecycle (MessageHandler,
// WindowClosing, geometry events); the operator only tracks it so
// Observe/Use/Navigate/Close find it by id.
func (o *desktopWindowOperator) RegisterExternal(id string, win browserinstance.ExternalWindow) error {
	w, ok := win.(*application.WebviewWindow)
	if !ok {
		return fmt.Errorf("desktop: register external window %q: unexpected window type %T", id, win)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.windows[id] = &browserWindow{win: w, id: id}
	return nil
}

// RemoveWindow drops an externally-registered window from operator tracking
// without closing it — the owning browser session system remains
// responsible for the window lifecycle.
func (o *desktopWindowOperator) RemoveWindow(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	bw, ok := o.windows[id]
	if !ok || bw == nil {
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
	delete(o.windows, id)
}

func (a *App) SelectFolder() (string, error) {
	if a.app == nil {
		return "", fmt.Errorf("desktop: not started")
	}
	return a.app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
}

func (a *App) SelectFile() (string, error) {
	if a.app == nil {
		return "", fmt.Errorf("desktop: not started")
	}
	return a.app.Dialog.OpenFile().
		CanChooseDirectories(false).
		CanChooseFiles(true).
		PromptForSingleSelection()
}

// InstallSourceSelection is the result of an install picker: the chosen path
// plus whether it points at a package directory (true) or a regular file such
// as a .zip package (false). Empty Path means cancelled.
type InstallSourceSelection struct {
	Path  string
	IsDir bool
}

// SelectAppPackageFile opens a file picker filtered to .zip app packages.
// Windows IFileDialog cannot pick files and folders in one dialog
// (FOS_PICKFOLDERS greys out files), so the install flow uses one picker
// per source kind.
func (a *App) SelectAppPackageFile() (InstallSourceSelection, error) {
	if a.app == nil {
		return InstallSourceSelection{}, fmt.Errorf("desktop: not started")
	}
	path, err := a.app.Dialog.OpenFile().
		CanChooseDirectories(false).
		CanChooseFiles(true).
		AddFilter("App package (*.zip)", "*.zip").
		PromptForSingleSelection()
	if err != nil {
		return InstallSourceSelection{}, err
	}
	if path == "" {
		return InstallSourceSelection{}, nil
	}
	return InstallSourceSelection{Path: path}, nil
}

// SelectAppPackageFolder opens a folder-only picker for a package directory.
func (a *App) SelectAppPackageFolder() (InstallSourceSelection, error) {
	if a.app == nil {
		return InstallSourceSelection{}, fmt.Errorf("desktop: not started")
	}
	path, err := a.app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
	if err != nil {
		return InstallSourceSelection{}, err
	}
	if path == "" {
		return InstallSourceSelection{}, nil
	}
	return InstallSourceSelection{Path: path, IsDir: true}, nil
}

func (a *App) DesktopWindowState() (domain.DesktopWindowState, error) {
	return a.winState, nil
}

func (a *App) SaveDesktopWindowState(state domain.DesktopWindowState) error {
	a.winState = state
	return store.Save(desktopWindowStateKey, state)
}

func (a *App) loadWindowState() error {
	return persist.LoadOrZero(store, desktopWindowStateKey, &a.winState)
}

// restoreWindowState applies the persisted geometry to the Wails window.
// Order matters: set normal bounds first, then re-apply maximised.
func (a *App) restoreWindowState() {
	if a.window == nil {
		return
	}
	if a.winState.Width > 0 && a.winState.Height > 0 {
		a.window.SetSize(int(a.winState.Width), int(a.winState.Height))
	}
	if a.winState.X > -10000 && a.winState.Y > -10000 && (a.winState.X != 0 || a.winState.Y != 0) {
		a.window.SetPosition(int(a.winState.X), int(a.winState.Y))
	}
	if a.winState.Maximised {
		a.window.Maximise()
	}
}

// RecordWindowGeometry samples the live window bounds and schedules a
// debounced save. Called from WindowDidMove / DidResize / Maximise /
// UnMaximise / Restore handlers so the latest normal bounds are always
// on disk. This is necessary because the BeforeClose hook races with
// Wails v3's internal close listener, which marks the window destroyed
// before user listeners run — at which point Width/Height/Position/
// IsMaximised all return zero.
func (a *App) RecordWindowGeometry() {
	if a.window == nil {
		return
	}
	isMaximised := a.window.IsMaximised()
	a.winStateMu.Lock()
	if !isMaximised {
		x, y := a.window.Position()
		if x > -10000 && y > -10000 && (x != 0 || y != 0) {
			a.winState.Width = int32(a.window.Width())
			a.winState.Height = int32(a.window.Height())
			a.winState.X = int32(x)
			a.winState.Y = int32(y)
		}
	}
	a.winState.Maximised = isMaximised
	a.scheduleWindowStateFlushLocked()
	a.winStateMu.Unlock()
}

// scheduleWindowStateFlushLocked assumes winStateMu is held.
func (a *App) scheduleWindowStateFlushLocked() {
	if a.winStateFlushTimer != nil {
		a.winStateFlushTimer.Stop()
	}
	a.winStateFlushTimer = time.AfterFunc(windowStateFlushDebounce, a.flushWindowState)
}

func (a *App) flushWindowState() {
	a.winStateMu.Lock()
	state := a.winState
	a.winStateFlushTimer = nil
	a.winStateMu.Unlock()
	if err := store.Save(desktopWindowStateKey, state); err != nil {
		a.desktopLog("warn", "save window state failed", map[string]any{"err": err})
	}
}

// BeforeClose persists the latest observed window geometry. The reactive
// observers in RecordWindowGeometry keep winState current, so this only
// cancels any pending debounced write and flushes synchronously to
// guarantee the save completes before the window is destroyed.
func (a *App) BeforeClose() {
	if a.browserOp != nil {
		a.browserOp.shuttingDown = true
	}
	if a.wailsTransport != nil && a.window != nil {
		a.wailsTransport.signalCloseSessionByName(a.window.Name())
	}
	a.winStateMu.Lock()
	state := a.winState
	if a.winStateFlushTimer != nil {
		a.winStateFlushTimer.Stop()
		a.winStateFlushTimer = nil
	}
	a.winStateMu.Unlock()
	if err := store.Save(desktopWindowStateKey, state); err != nil {
		a.desktopLog("warn", "save window state failed", map[string]any{"err": err})
	}
	// Secondary webview windows (right-panel browsers, screenshot) are hidden
	// and/or reparented as Win32 children of the main window, so Windows
	// destroys them silently without running their WM_CLOSE handler — the
	// fork's windowMap never empties and "quit on last window closed" never
	// fires. Quit explicitly so OnShutdown -> Shutdown -> os.Exit always runs.
	if a.app != nil {
		a.app.Quit()
	}
}

// GetGatewayAddr exposes the gateway host:port to the frontend so the
// WebSocket client can target the in-process gateway even when
// sporemind.yaml overrides the default :18080.
func (a *App) GetGatewayAddr() string {
	addr := config.GatewayAddr()
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

// GetRawGatewayAddr returns the effective gateway address — the env override
// or the flavor-remapped config value (e.g. ":18080"; devrelease builds
// report 18081), not necessarily the literal sporemind.yaml text. Used by
// the developer settings form.
func (a *App) GetRawGatewayAddr() string {
	return config.GatewayAddr()
}

// WaitGatewayReady blocks until the in-process gateway listener is accepting
// connections, or the given timeout (ms) elapses. The HTTP listener binds only
// after every actor finished OnStart, so a ws-transport frontend that dials
// before this returns gets connection-refused and lands in the client's
// 0.5–1.5s reconnect backoff — the dominant part of the ws-vs-wails startup
// gap. Returns true when the gateway is (already) accepting.
func (a *App) WaitGatewayReady(timeoutMs int) bool {
	// handle can be nil only when the service was constructed without a bound
	// runtime (tests); report not-ready so callers fall back to blind dial.
	if a.handle == nil || a.handle.App() == nil {
		return false
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	return waitGatewayChan(a.handle.GatewayReady(), timeout)
}

// waitGatewayChan resolves the gateway-readiness select. A nil channel means
// no gateway is configured — nothing to wait for.
func waitGatewayChan(ready <-chan struct{}, timeout time.Duration) bool {
	if ready == nil {
		return true
	}
	select {
	case <-ready:
		return true
	case <-time.After(timeout):
		return false
	}
}

// GetDesktopTransport returns the configured desktop transport mode
// ("wails" or "ws"). Used by the frontend to decide which transport to
// create on startup.
func (a *App) GetDesktopTransport() string {
	return config.DesktopTransport()
}

// SetGatewayAddr updates the in-memory gateway address. The change is
// persisted to sporemind.yaml by SaveConfig.
func (a *App) SetGatewayAddr(addr string) {
	config.SetGatewayAddr(addr)
}

// SetGatewayBindAddrs updates the in-memory extra gateway bind addresses.
// Each address gets its own listener at runtime, enabling dual binding
// (loopback + LAN IP) without 0.0.0.0. Persisted by SaveConfig.
func (a *App) SetGatewayBindAddrs(addrs []string) {
	config.SetGatewayBindAddrs(addrs)
}

// GetGatewayBindAddrs returns the extra gateway bind addresses configured
// in sporemind.yaml. Empty when LAN access is off.
func (a *App) GetGatewayBindAddrs() []string {
	return config.GatewayBindAddrs()
}

// SetDesktopTransport updates the in-memory desktop transport mode. Valid
// values are "wails" and "ws". The change is persisted to sporemind.yaml
// by SaveConfig.
func (a *App) SetDesktopTransport(transport string) {
	config.SetDesktopTransport(transport)
}

// StorageSettings is the settings-UI view of the storage configuration:
// scoped backends (raw yaml values — "" follows the goleveldb default) and
// retention windows (effective values with defaults applied). Backend and
// retention changes take effect after a restart; persisting to yaml happens
// via SetStorageSettings + SaveConfig.
type StorageSettings struct {
	// Raw backend_aistats value: "" (default goleveldb) / "fs" / "goleveldb".
	BackendAIStats string `json:"backendAistats"`
	// Raw backend_logs value: "" (default goleveldb) / "fs" / "goleveldb".
	BackendLogs string `json:"backendLogs"`
	// Effective backend-log retention window in days.
	LogsRetentionDays int `json:"logsRetentionDays"`
	// Effective raw aistats record retention window in days.
	AistatsRawDays int `json:"aistatsRawDays"`
	// Effective daily-rollup aistats retention window in days.
	AistatsDailyDays int `json:"aistatsDailyDays"`
}

// GetStorageSettings returns the storage settings for the settings UI.
// Backend fields are the raw yaml values ("" = follow the default); the
// retention fields carry the effective values with defaults applied.
func (a *App) GetStorageSettings() StorageSettings {
	return StorageSettings{
		BackendAIStats:    config.ScopedBackendRaw("aistats"),
		BackendLogs:       config.ScopedBackendRaw("logs"),
		LogsRetentionDays: config.LogsRetentionDays(),
		AistatsRawDays:    config.AistatsRawDays(),
		AistatsDailyDays:  config.AistatsDailyDays(),
	}
}

// SetStorageSettings validates and applies the settings-UI storage form to
// the in-memory config. Callers persist with SaveConfig. Invalid backend
// values are rejected with an error — never a silent fallback.
func (a *App) SetStorageSettings(s StorageSettings) error {
	if err := config.SetScopedBackend("aistats", s.BackendAIStats); err != nil {
		return fmt.Errorf("backend_aistats: %w", err)
	}
	if err := config.SetScopedBackend("logs", s.BackendLogs); err != nil {
		return fmt.Errorf("backend_logs: %w", err)
	}
	config.SetLogsRetentionDays(s.LogsRetentionDays)
	config.SetAistatsRawDays(s.AistatsRawDays)
	config.SetAistatsDailyDays(s.AistatsDailyDays)
	return nil
}

// SaveConfig persists the current in-memory configuration to sporemind.yaml.
func (a *App) SaveConfig() error {
	return config.Save()
}

// GetLocalNetworkGatewayURL returns an http:// URL that a phone on the same
// local network can use to reach the gateway. It prefers the first non-loopback
// IPv4 address and falls back to an empty string if none is available.
func (a *App) GetLocalNetworkGatewayURL() string {
	if !isGatewayAccessibleOnLAN() {
		return ""
	}
	port := gatewayPort()
	ip, err := localIPv4()
	if err != nil {
		return ""
	}
	return fmt.Sprintf("http://%s:%s", ip, port)
}

// GetLocalIPv4 returns the best candidate local IPv4 address for LAN access,
// or an empty string if none is available. Exposed so the frontend can bind
// the gateway to a specific LAN IP instead of 0.0.0.0.
func (a *App) GetLocalIPv4() string {
	ip, err := localIPv4()
	if err != nil {
		return ""
	}
	return ip
}

func gatewayPort() string {
	addr := config.GatewayAddr()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		_, port, _ = net.SplitHostPort("localhost" + addr)
	}
	if port == "" {
		port = strconv.Itoa(config.DefaultGatewayPort)
	}
	return port
}

func localIPv4() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	var best struct {
		addr  string
		score int
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil {
			continue
		}
		// 169.254.0.0/16 are link-local APIPA addresses; they cannot be used
		// by another device on the LAN to reach this machine.
		if ip4[0] == 169 && ip4[1] == 254 {
			continue
		}
		score := ipv4LANPriority(ip4)
		if score > best.score {
			best.score = score
			best.addr = ip4.String()
		}
	}
	if best.addr != "" {
		return best.addr, nil
	}
	return "", fmt.Errorf("no non-loopback IPv4 address found")
}

// ipv4LANPriority scores a local IPv4 address so we prefer the network segment
// most likely to be the user's actual LAN. 192.168.x.x is most common for home
// Wi-Fi routers, 10.x.x.x is often corporate/VPN, 172.16-31.x.x is less common.
func ipv4LANPriority(ip net.IP) int {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	if ip4[0] == 192 && ip4[1] == 168 {
		return 30
	}
	if ip4[0] == 10 {
		return 20
	}
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return 10
	}
	return 1
}

func isGatewayAccessibleOnLAN() bool {
	// Check extra bind addresses first — when dual binding is enabled the
	// primary addr stays 127.0.0.1 but a LAN IP is in the extra list.
	for _, addr := range config.GatewayBindAddrs() {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		if host == "" || host == "0.0.0.0" {
			return true
		}
		ip := net.ParseIP(host)
		if ip != nil && !ip.IsLoopback() {
			return true
		}
	}
	addr := config.GatewayAddr()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		addr = "localhost" + addr
		host, _, err = net.SplitHostPort(addr)
		if err != nil {
			return true
		}
	}
	// Empty host, ":18080" or "0.0.0.0" means all interfaces (LAN accessible).
	// "127.0.0.1" or "localhost" means loopback only.
	if host == "" || host == "0.0.0.0" {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return false
	}
	return true
}

// SetClipboardText writes text to the system clipboard.
func (a *App) SetClipboardText(text string) error {
	if a.app == nil {
		return fmt.Errorf("desktop: not started")
	}
	a.app.Clipboard.SetText(text)
	return nil
}

// SetClipboardImage decodes a data:image/png;base64,... URL and writes the image
// to the system clipboard in two formats:
//   - CF_DIB  — for pasting into Paint, Word, etc.
//   - CF_HDROP — for pasting as a file in Explorer
func (a *App) SetClipboardImage(dataURL string) error {
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		return fmt.Errorf("set-clipboard-image: unsupported data URL prefix")
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[len(prefix):])
	if err != nil {
		return fmt.Errorf("set-clipboard-image: base64 decode: %w", err)
	}

	// Write the raw PNG to a temp file; Explorer paste (CF_HDROP) needs a
	// physical file on disk.
	tmpDir := os.TempDir()
	tmpPath := filepath.Join(tmpDir, "Sporemind_Screenshot_"+time.Now().Format("20060102_150405")+".png")
	if err := os.WriteFile(tmpPath, raw, 0644); err != nil {
		return fmt.Errorf("set-clipboard-image: write temp file: %w", err)
	}

	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("set-clipboard-image: png decode: %w", err)
	}

	return writeClipboardMulti(img, tmpPath)
}

// ---------------------------------------------------------------------------
// Native OS drag-out (files out of the app into Explorer / desktop)
// ---------------------------------------------------------------------------

// FileDragOutRequest is the payload of the StartFileDragOut binding. At least
// one source must be provided: LocalPaths, ArchiveTarGzBase64, or both.
type FileDragOutRequest struct {
	// LocalPaths are absolute local file or directory paths to drag out as-is.
	LocalPaths []string `json:"localPaths"`
	// ArchiveTarGzBase64, when non-empty, is a tar.gz archive (standard
	// base64). It is extracted to a temp directory and the resulting folder is
	// dragged out together with LocalPaths. Entries with absolute paths or
	// ".." traversal components are rejected.
	ArchiveTarGzBase64 string `json:"archiveTarGzBase64"`
	// ArchiveRootName is the folder name under which the archive is
	// extracted (the name the drop target sees). It must be a single path
	// component. Defaults to "sporemind-dragout" when empty.
	ArchiveRootName string `json:"archiveRootName"`
}

// StartFileDragOut starts a native OS drag-out: the given files/folders are
// offered to whatever drop target (Explorer, desktop, another app) the user
// drops them on, using a copy-only native drag. The call blocks until the
// drag completes (drop, or cancel via Esc / button release outside a target);
// cancelling the drag is a success, not an error. Dragging out extracted
// archives cleans up the temp directory afterwards.
func (a *App) StartFileDragOut(req FileDragOutRequest) error {
	if !a.dragOutMu.TryLock() {
		return fmt.Errorf("desktop: a drag-out is already in progress")
	}
	defer a.dragOutMu.Unlock()
	return startFileDragOut(req)
}

// ---------------------------------------------------------------------------
// Win32 clipboard helpers (DIB / HDROP construction)
// ---------------------------------------------------------------------------

func writeClipboardMulti(img image.Image, filePath string) error {
	// Same snapshot as the clipboard.write host call; the screenshot copy
	// path has no raw PNG bytes, so only CF_DIB + CF_HDROP are set (the
	// DIB carries the pixels).
	return writeClipboardFormats(img, nil, filePath, "")
}

// allocDIB builds a CF_DIB (BITMAPINFOHEADER + bottom-up BGRA pixels) in a
// moveable global memory block.
func allocDIB(img image.Image, w, h int) (uintptr, error) {
	headerSize := 40
	rowBytes := w * 4
	total := headerSize + rowBytes*h

	hMem, _, _ := procGlobalAlloc.Call(uintptr(gmemMoveable|gmemZeroInit), uintptr(total))
	if hMem == 0 {
		return 0, fmt.Errorf("GlobalAlloc failed")
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return 0, fmt.Errorf("GlobalLock failed")
	}
	dib := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), total)

	// BITMAPINFOHEADER
	binary.LittleEndian.PutUint32(dib[0:4], 40)                   // biSize
	binary.LittleEndian.PutUint32(dib[4:8], uint32(w))            // biWidth
	binary.LittleEndian.PutUint32(dib[8:12], uint32(h))           // biHeight (bottom-up)
	putUI16(dib[12:14], 1)                                        // biPlanes
	putUI16(dib[14:16], 32)                                       // biBitCount
	binary.LittleEndian.PutUint32(dib[16:20], 0)                  // biCompression = BI_RGB
	binary.LittleEndian.PutUint32(dib[20:24], uint32(rowBytes*h)) // biSizeImage

	// Pixel data: bottom-up → invert Y & convert RGBA→BGRA
	for row := 0; row < h; row++ {
		srcY := h - 1 - row
		dst := dib[headerSize+row*rowBytes:]
		for col := 0; col < w; col++ {
			r, g, b, a := img.At(col, srcY).RGBA()
			dst[col*4+0] = byte(b >> 8)
			dst[col*4+1] = byte(g >> 8)
			dst[col*4+2] = byte(r >> 8)
			dst[col*4+3] = byte(a >> 8)
		}
	}

	procGlobalUnlock.Call(hMem)
	return hMem, nil
}

// allocHDROP builds a CF_HDROP (DROPFILES + double-null-terminated UTF-16
// file list) in a moveable global memory block. Multiple paths are packed
// into one list; each path is NUL-terminated with a final extra NUL.
func allocHDROP(paths ...string) (uintptr, error) {
	if len(paths) == 0 {
		return 0, fmt.Errorf("allocHDROP: empty file list")
	}
	var list []uint16
	for _, p := range paths {
		if p == "" {
			return 0, fmt.Errorf("allocHDROP: empty path in file list")
		}
		list = append(list, utf16.Encode([]rune(p))...)
		list = append(list, 0)
	}
	list = append(list, 0) // list terminator (double null overall)

	dfSize := 20 // sizeof(DROPFILES)
	listBytes := len(list) * 2
	total := dfSize + listBytes

	hMem, _, _ := procGlobalAlloc.Call(uintptr(gmemMoveable|gmemZeroInit), uintptr(total))
	if hMem == 0 {
		return 0, fmt.Errorf("GlobalAlloc failed")
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return 0, fmt.Errorf("GlobalLock failed")
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), total)

	// DROPFILES
	binary.LittleEndian.PutUint32(data[0:4], uint32(dfSize)) // pFiles
	binary.LittleEndian.PutUint32(data[4:8], 0)              // pt.x
	binary.LittleEndian.PutUint32(data[8:12], 0)             // pt.y
	putUI32(data[12:16], 0)                                  // fNC
	putUI32(data[16:20], 1)                                  // fWide = Unicode

	// File list (UTF-16LE)
	dst := data[dfSize:]
	for i, ch := range list {
		dst[i*2+0] = byte(ch)
		dst[i*2+1] = byte(ch >> 8)
	}

	procGlobalUnlock.Call(hMem)
	return hMem, nil
}

func putUI16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

func putUI32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

// ---------------------------------------------------------------------------
// Frontend JS evaluation (debug/eval-js endpoint).
// ---------------------------------------------------------------------------

// EvalJSResult is the Wails binding that the injected JavaScript calls back
// into after executing a script. It delivers the JSON-serialised result to the
// waiting evalJS call via a channel.
func (a *App) EvalJSResult(id string, result string) error {
	a.evalJSMu.Lock()
	ch, ok := a.evalJSChs[id]
	a.evalJSMu.Unlock()
	if !ok {
		return fmt.Errorf("eval-js request %s not found or timed out", id)
	}
	ch <- result
	return nil
}

// evalJS executes JavaScript in the frontend webview and waits for the result
// to be delivered through EvalJSResult. It times out after 5 seconds.
func (a *App) evalJS(ctx context.Context, script string) (any, error) {
	if a.window == nil {
		return nil, fmt.Errorf("desktop: not started")
	}

	id := fmt.Sprintf("eval-js-%d", time.Now().UnixNano())
	ch := make(chan string, 1)

	a.evalJSMu.Lock()
	a.evalJSChs[id] = ch
	a.evalJSMu.Unlock()

	defer func() {
		a.evalJSMu.Lock()
		delete(a.evalJSChs, id)
		a.evalJSMu.Unlock()
	}()

	scriptJSON, _ := json.Marshal(script)
	idJSON, _ := json.Marshal(id)

	// The frontend registers window.__sporemindEvalJSResult at module boot
	// (web/src/application/wails-bridge.ts), pointing at the generated
	// EvalJSResult binding. Using the registered global keeps the injected
	// script independent of the deep generated-bindings path.
	js := fmt.Sprintf(`(async function() {
		try {
			const result = eval(%s);
			const resolved = await Promise.resolve(result);
			window.__sporemindEvalJSResult(%s, JSON.stringify(resolved));
		} catch (err) {
			window.__sporemindEvalJSResult(%s, JSON.stringify({__eval_error__: err.message}));
		}
	})()`, scriptJSON, idJSON, idJSON)

	a.window.ExecJS(js)

	select {
	case result := <-ch:
		var errResult struct {
			EvalError string `json:"__eval_error__"`
		}
		if json.Unmarshal([]byte(result), &errResult) == nil && errResult.EvalError != "" {
			return nil, fmt.Errorf("js error: %s", errResult.EvalError)
		}
		var parsed any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			return result, nil
		}
		return parsed, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("eval-js timeout")
	}
}

func (a *App) OpenDevTools() {
	if os.Getenv("SPOREMIND_DEVTOOLS") != "1" {
		return
	}
	if openDevToolsImpl != nil {
		openDevToolsImpl()
	}
}

// GetLogs returns the most recent backend log entries.
func (a *App) GetLogs(limit int) []gateway.LogEntry {
	if a.logRing == nil {
		return nil
	}
	return a.logRing.QueryLogs(gateway.LogQuery{Limit: limit})
}

// GetLogsBefore returns up to limit backend log entries older than before.
// The before timestamp is RFC3339Nano; entries with an equal timestamp are
// excluded so the caller can use the oldest already-loaded entry as a cursor.
// If the ring does not contain enough older entries, the file store is queried.
func (a *App) GetLogsBefore(before string, limit int) []gateway.LogEntry {
	if a.logRing == nil || before == "" || limit <= 0 {
		return nil
	}
	entries := a.logRing.All()
	var result []gateway.LogEntry
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Timestamp < before {
			result = append(result, entries[i])
			if len(result) >= limit {
				break
			}
		}
	}

	if len(result) < limit && a.logStore != nil {
		fromStore, err := a.logStore.QueryBefore(before, limit-len(result))
		if err == nil && len(fromStore) > 0 {
			result = append(fromStore, result...)
		}
	}

	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// WriteConsoleLog persists a frontend console log entry.
func (a *App) WriteConsoleLog(level string, message string, location string, ts int64) {
	if a.consoleStore == nil {
		return
	}
	if err := a.consoleStore.Write(logging.ConsoleEntry{
		Level:    level,
		Message:  message,
		Time:     ts,
		Location: location,
	}); err != nil {
		// The frontend swallows binding errors by design, so a failing store
		// would otherwise freeze the console capture silently (2026-09-14
		// incident). Surface it rate-limited in the backend log instead.
		now := time.Now()
		a.consoleWriteErrMu.Lock()
		if now.Sub(a.lastConsoleWriteErrLog) > 30*time.Second {
			a.lastConsoleWriteErrLog = now
			a.consoleWriteErrMu.Unlock()
			log.Printf("desktop: console store write failed (rate-limited 30s): %v", err)
			return
		}
		a.consoleWriteErrMu.Unlock()
	}
}

// GetConsoleLogs returns the most recent persisted console log entries.
func (a *App) GetConsoleLogs(limit int) []logging.ConsoleEntry {
	if a.consoleStore == nil {
		return nil
	}
	logs, _ := a.consoleStore.Tail(limit)
	return logs
}

// Log appends a structured log entry and emits it to the frontend.
func (a *App) Log(level string, msg string, caller string, fields map[string]any) gateway.LogEntry {
	entry := gateway.LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level,
		Caller:    caller,
		Message:   msg,
		Fields:    fields,
	}
	if a.logRing != nil {
		a.logRing.Append(entry)
	}
	if a.app != nil {
		a.app.Event.Emit("sporemind:log", entry)
	}
	return entry
}

// GetAdminToken returns a JWT for the local admin account. Wails uses this
// to auto-login on startup without a password — the desktop binary lives
// in the same process as the user actor, so anything that can call this
// already has process-level trust.
//
// The token issuer is exposed by the user actor as a typed resource at
// startup; this method is just a thin lookup that never crosses the
// transport boundary.
//
// The Wails frontend may call this binding before the actor tree finished
// starting (the binding bypasses the mailbox, so the registry-freeze
// ordering guarantee does not apply) — the lookup therefore waits, bounded,
// for the user actor to publish the issuer instead of failing instantly.
func (a *App) GetAdminToken() (string, error) {
	if a.handle == nil || a.handle.App() == nil {
		return "", fmt.Errorf("desktop: runtime handle not available")
	}
	issuer, err := waitDesktopTokenIssuer(a.handle.App().Resources(), 30*time.Second, 150*time.Millisecond)
	if err != nil {
		return "", err
	}
	token, err := issuer()
	if err != nil {
		return "", fmt.Errorf("desktop: issue admin token: %w", err)
	}
	return token, nil
}

// waitDesktopTokenIssuer polls the resource registry until the user actor
// publishes DesktopTokenIssuerKey or the timeout elapses. On timeout the
// error carries a diagnostic hint: the issuer is published in user.OnStart,
// so its absence after this long means the actor failed to start rather
// than a mere startup race.
func waitDesktopTokenIssuer(reg resource.Registry, timeout, poll time.Duration) (user.DesktopTokenIssuer, error) {
	if issuer, ok := resource.Get(reg, user.DesktopTokenIssuerKey); ok {
		return issuer, nil
	}
	deadline := time.Now().Add(timeout)
	for {
		if issuer, ok := resource.Get(reg, user.DesktopTokenIssuerKey); ok {
			return issuer, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("desktop: user token issuer not registered after %s — user actor may have failed to start", timeout)
		}
		time.Sleep(poll)
	}
}

// ---------------------------------------------------------------------------
// Screenshot functionality
// ---------------------------------------------------------------------------

// StartScreenshot captures all visible windows and the full screen, then opens
// a new borderless, always-on-top, fullscreen window for the screenshot editor.
func (a *App) StartScreenshot() (*ScreenshotData, error) {
	windows, err := captureWindows()
	if err != nil {
		return nil, fmt.Errorf("capture windows: %w", err)
	}

	img, err := captureScreen()
	if err != nil {
		return nil, fmt.Errorf("capture screen: %w", err)
	}

	base64Str, err := encodeImageToPNGBase64(img)
	if err != nil {
		return nil, fmt.Errorf("encode image: %w", err)
	}

	// Frameless windows on Windows need explicit position before fullscreen
	// to avoid title-bar offset. Capture first, then create window.
	data := &ScreenshotData{
		ImageBase64: "data:image/png;base64," + base64Str,
		Windows:     windows,
	}

	a.screenshotData = data

	// Create the overlay window asynchronously so the RPC returns fast.
	// By the time the frontend has loaded the image and calls
	// ShowScreenshotWindow the window is usually ready.
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	go func() {
		opts := application.WebviewWindowOptions{
			Title:            "Screenshot",
			Width:            width,
			Height:           height,
			Frameless:        true,
			AlwaysOnTop:      true,
			DisableResize:    true,
			X:                0,
			Y:                0,
			BackgroundType:   application.BackgroundTypeTransparent,
			BackgroundColour: application.RGBA{Red: 0, Green: 0, Blue: 0, Alpha: 0},
			URL:              "/index.html#screenshot",
		}
		win := a.app.Window.NewWithOptions(opts)
		win.Hide()
		a.screenshotWindow = win
	}()

	return data, nil
}

func (a *App) RefreshScreenshotForWindow(handle uintptr) (*ScreenshotData, error) {
	if a.screenshotWindow == nil {
		return nil, fmt.Errorf("no screenshot window")
	}
	a.screenshotWindow.Hide()
	if err := activateWindow(handle); err != nil {
		a.screenshotWindow.Show()
		return nil, fmt.Errorf("activate window: %w", err)
	}
	time.Sleep(150 * time.Millisecond)

	windows, err := captureWindows()
	if err != nil {
		a.screenshotWindow.Show()
		return nil, fmt.Errorf("capture windows: %w", err)
	}
	img, err := captureScreen()
	if err != nil {
		a.screenshotWindow.Show()
		return nil, fmt.Errorf("capture screen: %w", err)
	}
	base64Str, err := encodeImageToPNGBase64(img)
	if err != nil {
		a.screenshotWindow.Show()
		return nil, fmt.Errorf("encode image: %w", err)
	}

	data := &ScreenshotData{
		ImageBase64: "data:image/png;base64," + base64Str,
		Windows:     windows,
	}
	a.screenshotData = data
	a.screenshotWindow.Show()
	return data, nil
}

// GetScreenshotData returns the data captured by the most recent StartScreenshot.
func (a *App) GetScreenshotData() (*ScreenshotData, error) {
	if a.screenshotData == nil {
		return nil, fmt.Errorf("no screenshot data")
	}
	return a.screenshotData, nil
}

// CloseScreenshotWindow closes the screenshot overlay window and clears data.
func (a *App) CloseScreenshotWindow() {
	if a.screenshotWindow != nil {
		a.screenshotWindow.Close()
		a.screenshotWindow = nil
	}
	a.screenshotData = nil
}

// ShowScreenshotWindow reveals the previously hidden screenshot window
// once the frontend has loaded the image, for a seamless visual transition.
// The window is created asynchronously in StartScreenshot; poll briefly if
// it hasn't been created yet.
func (a *App) ShowScreenshotWindow() {
	for i := 0; i < 100 && a.screenshotWindow == nil; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if a.screenshotWindow == nil {
		return
	}
	a.screenshotWindow.SetPosition(0, 0)
	a.screenshotWindow.Fullscreen()
	a.screenshotWindow.Show()
}

// browserProfilePath returns the WebView2 user-data directory for a session.
// browserProfilePath returns the WebView2 user-data directory for a session.
// The global browser shares one profile across all sessions so login state
// survives app restarts. Independent sessions use the same per-instance
// directory as standalone windows; app sessions are isolated per-session.
// Independent sessions additionally redirect their HTTP disk cache to
// sharedBrowserCacheDir so their profile dirs hold only login state.
func browserProfilePath(kind, sessionID string) string {
	switch kind {
	case "global":
		return filepath.Join(config.DataDir(), "browser-profiles", "global")
	case "app":
		return filepath.Join(config.DataDir(), "browser-profiles", "app-"+sessionID)
	default: // independent
		return filepath.Join(config.DataDir(), "browser-profiles", sessionID)
	}
}

// browserInjectScript is injected into every right-panel browser webview.
// It forces target="_blank" links, window.open() and target="_blank" form
// submissions to open as frontend tabs instead of unmanaged WebView2 popups.
// It also reports URL/title changes back to the Go backend via postMessage.
func browserInjectScript() string {
	return `(function() {
	if (window.__sporemindBrowserInjected) return;
	// Skip injection in iframes: Cloudflare Turnstile runs its challenge in
	// cross-origin iframes and our postMessage hooks / interval timers can
	// interfere with its sandboxed script execution and origin checks.
	if (window.top !== window.self) return;
	window.__sporemindBrowserInjected = true;
	// External-URL pages never load the Wails JS runtime, so WebviewWindow
	// .ExecJS stays gated behind runtimeLoaded and every injected operator
	// script (observe/use/reader) sits in the pendingJS queue forever — the
	// crawl engine's observe timed out at the 15s operator deadline on every
	// task. Announcing readiness here un-gates ExecJS for external pages.
	try {
		if (window.chrome && window.chrome.webview) {
			window.chrome.webview.postMessage('wails:runtime:ready');
		}
	} catch(e) {}
	function send(type, payload) {
		try {
			if (window.chrome && window.chrome.webview) {
				window.chrome.webview.postMessage('wails:' + JSON.stringify({type: type, payload: payload}));
			}
		} catch(e) {}
	}
	function getFavicon() {
		var links = document.querySelectorAll('link[rel~="icon"], link[rel="shortcut icon"], link[rel="apple-touch-icon"]');
		for (var i = 0; i < links.length; i++) {
			var href = links[i].href;
			if (href) return href;
		}
		return location.origin + '/favicon.ico';
	}
	function isSystemProtocol(url) {
		if (!url) return false;
		try {
			var u = new URL(url, location.href);
			return u.protocol === 'mailto:' || u.protocol === 'tel:' || u.protocol === 'sms:' || u.protocol === 'intent:';
		} catch(e) { return false; }
	}
	function isJavaScriptUrl(url) {
		return typeof url === 'string' && /^\s*javascript:/i.test(url);
	}
	function canOpenAsTab(url) {
		if (!url) return false;
		try {
			var u = new URL(url, location.href);
			return u.protocol === 'http:' || u.protocol === 'https:' || u.protocol === 'about:' || u.protocol === 'data:' || u.protocol === 'blob:' || u.protocol === 'file:';
		} catch(e) { return false; }
	}
	var origOpen = window.open;
	window.open = function(url, target, features) {
		if (target === '_self' || target === '_top' || target === '_parent') {
			return origOpen.apply(this, arguments);
		}
		if (isSystemProtocol(url)) {
			return origOpen.apply(this, arguments);
		}
		if (isJavaScriptUrl(url)) {
			return null;
		}
		if (url) {
			try {
				send('new-tab', { url: new URL(url, location.href).href });
			} catch(e) {}
		} else {
			send('new-tab', { url: '' });
		}
		return window;
	};
	function hasRel(rel) {
		if (!rel) return false;
		var parts = rel.split(/\s+/);
		for (var i = 0; i < parts.length; i++) {
			if (parts[i] === 'noopener' || parts[i] === 'noreferrer') return true;
		}
		return false;
	}
	document.addEventListener('click', function(e) {
		var a = e.target.closest('a');
		if (!a || !a.href) return;
		if (a.target !== '_blank' && !hasRel(a.getAttribute('rel'))) return;
		if (isSystemProtocol(a.href)) return;
		if (isJavaScriptUrl(a.href)) {
			e.preventDefault();
			return;
		}
		if (!canOpenAsTab(a.href)) return;
		e.preventDefault();
		try {
			send('new-tab', { url: new URL(a.href, location.href).href });
		} catch(e) {}
	}, true);
	document.addEventListener('submit', function(e) {
		var form = e.target;
		if (!form || form.tagName !== 'FORM' || form.target !== '_blank') return;
		if (!form.action || isSystemProtocol(form.action) || isJavaScriptUrl(form.action)) return;
		if (!canOpenAsTab(form.action)) return;
		e.preventDefault();
		var url = form.action;
		var method = (form.method || 'GET').toLowerCase();
		if (method === 'get') {
			var params = new URLSearchParams();
			var elements = form.elements;
			for (var i = 0; i < elements.length; i++) {
				var el = elements[i];
				if (!el.name || el.disabled) continue;
				var type = (el.type || '').toLowerCase();
				if (type === 'checkbox' || type === 'radio') {
					if (el.checked) params.append(el.name, el.value);
				} else if (el.tagName === 'SELECT') {
					for (var j = 0; j < el.options.length; j++) {
						if (el.options[j].selected) params.append(el.name, el.options[j].value);
					}
				} else if (type !== 'submit' && type !== 'button' && type !== 'image' && type !== 'file' && type !== 'reset') {
					params.append(el.name, el.value);
				}
			}
			url = url + (url.indexOf('?') >= 0 ? '&' : '?') + params.toString();
		}
		try {
			send('new-tab', { url: new URL(url, location.href).href });
		} catch(e) {}
	}, true);
	function sendPageInfo() {
		var bg = '';
		try {
			var el = document.body;
			while (el) {
				var c = getComputedStyle(el).backgroundColor;
				if (c && c !== 'rgba(0, 0, 0, 0)' && c !== 'transparent') { bg = c; break; }
				el = el.parentElement;
			}
			if (!bg) {
				var dc = getComputedStyle(document.documentElement).backgroundColor;
				if (dc && dc !== 'rgba(0, 0, 0, 0)' && dc !== 'transparent') bg = dc;
			}
		} catch(e) {}
		send('page-info', {url: location.href, title: document.title, icon: getFavicon(), bg: bg});
	}
	var lastUrl = location.href;
	var lastTitle = '';
	var lastFavicon = '';
	var tick = 0;
	setInterval(function() {
		tick++;
		if (location.href !== lastUrl) {
			lastUrl = location.href;
			send('navigated', {url: location.href});
		}
		var curTitle = document.title;
		var curFavicon = getFavicon();
		// The host silently drops page-info emitted while a navigation is
		// in flight (stale/in-flight gate) and sends no acknowledgement back,
		// so a static page whose title/icon never change again would never
		// deliver its favicon. Re-send on a slow cadence regardless of change.
		if (curTitle !== lastTitle || curFavicon !== lastFavicon || tick % 10 === 0) {
			lastTitle = curTitle;
			lastFavicon = curFavicon;
			sendPageInfo();
		}
	}, 300);
	send('navigated', {url: location.href});
	sendPageInfo();
	// Report first paint so the host can reveal the window only after the
	// page has actually rendered, avoiding a black WebView2 surface. The
	// double requestAnimationFrame waits for one composited frame.
	function signalPainted() {
		try {
			requestAnimationFrame(function() {
				requestAnimationFrame(function() {
					send('painted', {url: location.href});
				});
			});
		} catch(e) {}
	}
	if (document.readyState === 'loading') {
		document.addEventListener('DOMContentLoaded', signalPainted, {once: true});
	} else {
		signalPainted();
	}
	// bfcache restores do not re-run this init script, so re-signal paint.
	window.addEventListener('pageshow', function(ev) {
		if (ev.persisted) signalPainted();
	});
	// As soon as the current page is unloaded for a navigation, tell the host
	// to hide the window so no blank/black surface shows before the next paint.
	window.addEventListener('pagehide', function() {
		send('navigating', {});
	});
})();`
}

// browserStateScript captures per-page browsing state (scroll position, zoom,
// and an optional opaque data blob) and sends it back to the Go backend so it
// can be persisted per tab and restored after refresh.
func browserStateScript() string {
	return `(function() {
	if (window.__sporemindPageStateInjected) return;
	if (window.top !== window.self) return;
	window.__sporemindPageStateInjected = true;
	function sendPageState() {
		var data = '';
		try {
			if (window.__sporemindPageStateData) {
				data = JSON.stringify(window.__sporemindPageStateData);
			}
		} catch(e) {}
		var payload = {
			type: 'page-state',
			scrollX: window.scrollX || 0,
			scrollY: window.scrollY || 0,
			zoom: parseFloat(document.documentElement.style.zoom) || 1,
			data: data
		};
		try {
			if (window.chrome && window.chrome.webview) {
				window.chrome.webview.postMessage('wails:' + JSON.stringify(payload));
			}
		} catch(e) {}
	}
	window.addEventListener('scroll', sendPageState, {passive: true});
	window.addEventListener('beforeunload', sendPageState);
	setInterval(sendPageState, 3000);
})();`
}

func (a *App) handleBrowserMessage(sessionID, message string) {
	const prefix = "wails:"
	// Automation scripts (observe/use/state) also prefix their postMessages
	// with "wails:" so the Wails dispatcher routes them to this handler
	// instead of the gospore gateway transport. Strip the prefix; lifecycle
	// types return inside the switch, everything else falls through to the
	// automation branches below with the stripped payload.
	raw := message
	if strings.HasPrefix(message, prefix) {
		raw = message[len(prefix):]
		var payload struct {
			Type    string `json:"type"`
			Payload any    `json:"payload"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return
		}
		switch payload.Type {
		case "navigated":
			if m, ok := payload.Payload.(map[string]any); ok {
				if url, ok := m["url"].(string); ok && url != "" {
					if IsBrowserInternalURL(url) {
						a.app.Event.Emit("browser:navigation-error:"+sessionID, url)
						return
					}
					// Injected navigated never writes confirmedURL (contract §6). It
					// only re-projects the confirmed URL for the current document;
					// stale emissions (a previous page's delayed message) and
					// in-flight messages are dropped so they cannot regress the
					// address bar back to an old URL.
					a.browserMu.Lock()
					s := a.browserSessions[sessionID]
					accept := s != nil && s.acceptInjectedMeta(url)
					emitURL := ""
					if accept {
						if s.confirmedURL != "" {
							emitURL = s.confirmedURL
						} else {
							emitURL = url
						}
					}
					a.browserMu.Unlock()
					if emitURL != "" {
						a.app.Event.Emit("browser:navigated:"+sessionID, emitURL)
						a.browserMu.Lock()
						sForHist := a.browserSessions[sessionID]
						a.browserMu.Unlock()
						if sForHist != nil && sForHist.window != nil {
							a.emitHistoryState(sessionID, sForHist)
						}
					}
				}
			}
			return
		case "page-info":
			if m, ok := payload.Payload.(map[string]any); ok {
				url, _ := m["url"].(string)
				title, _ := m["title"].(string)
				icon, _ := m["icon"].(string)
				bg, _ := m["bg"].(string)
				if url != "" && IsBrowserInternalURL(url) {
					a.app.Event.Emit("browser:navigation-error:"+sessionID, url)
					return
				}
				// Gate on the confirmed document (contract §6): stale or
				// in-flight page-info cannot write the title or persisted state.
				// Only the title (and icon/bg projection) is taken from an
				// accepted message; the URL always projects confirmedURL.
				var (
					emitU, emitT, emitIcon, emitBG string
					cfg                            domain.BrowserInstanceConfig
					hasEmit                        bool
				)
				a.browserMu.Lock()
				if s := a.browserSessions[sessionID]; s != nil && s.acceptInjectedMeta(url) {
					if title != "" {
						s.title = title
					}
					emitU = s.confirmedURL
					if emitU == "" {
						emitU = url
					}
					emitT = title
					if emitT == "" {
						emitT = s.title
					}
					emitIcon = icon
					emitBG = bg
					if s.kind == "independent" {
						if title != "" {
							s.cfg.State.Title = title
							updateLastHistoryTitle(s.cfg.State.History, s.confirmedURL, title)
						}
						cfg = s.cfg
					}
					hasEmit = emitU != "" || emitT != ""
				}
				a.browserMu.Unlock()
				if hasEmit {
					if cfg.ID != "" {
						_ = a.syncSessionState(sessionID, cfg)
					}
					// Re-emit the normalized state so the frontend's title projection
					// (browser:state.title) stays in sync with an accepted
					// page-info title update (contract §5).
					a.emitBrowserState(sessionID)
					a.app.Event.Emit("browser:page-info:"+sessionID, map[string]string{"u": emitU, "t": emitT, "ic": emitIcon, "bg": emitBG})
				}
			}
			return
		case "new-tab":
			if m, ok := payload.Payload.(map[string]any); ok {
				if url, ok := m["url"].(string); ok && url != "" && !IsBrowserInternalURL(url) {
					a.app.Event.Emit("browser:new-tab:"+sessionID, url)
				}
			}
			return
		case "painted":
			// Gate first-paint on the confirmed document (contract §6): a stale
			// paint signal from a previous page must not mark a not-yet-painted
			// new document as painted. The paintFallback timer (armed on
			// NavigationCompleted) remains the safety net.
			var paintURL string
			if m, ok := payload.Payload.(map[string]any); ok {
				paintURL, _ = m["url"].(string)
			}
			a.browserMu.Lock()
			accept := false
			if s := a.browserSessions[sessionID]; s != nil {
				accept = s.acceptInjectedMeta(paintURL)
			}
			a.browserMu.Unlock()
			if accept {
				a.markSessionPainted(sessionID)
			}
			return
		case "navigating":
			a.markSessionNavigating(sessionID)
			return
		}
	}

	// Raw postMessage (e.g. page-state from browserStateScript).
	// Forward automation responses from the operator's injected observe/use
	// scripts (browser-observe / browser-use-result) so browsermanager.use can
	// observe/act on the Global tab: its window executes the operator scripts,
	// whose results post back to this session's MessageHandler. The callbacks
	// are keyed by observation ID, so forwarding from any session is safe.
	if a.browserOp != nil {
		if a.browserOp.handleBrowserObserveMessage(globalWindowKey, raw) {
			return
		}
		if a.browserOp.snapshots.handleBrowserUseResult(raw) {
			return
		}
	}

	var rawMap map[string]any
	if err := json.Unmarshal([]byte(raw), &rawMap); err != nil {
		return
	}
	if rawMap["type"] != "page-state" {
		return
	}
	a.browserMu.Lock()
	s := a.browserSessions[sessionID]
	if s == nil || s.kind != "independent" || s.cfg.ID == "" {
		a.browserMu.Unlock()
		return
	}
	state := s.cfg.State
	if x, ok := rawMap["scrollX"].(float64); ok {
		state.PageState.ScrollX = int32(x)
	}
	if y, ok := rawMap["scrollY"].(float64); ok {
		state.PageState.ScrollY = int32(y)
	}
	if z, ok := rawMap["zoom"].(float64); ok {
		state.PageState.Zoom = z
	}
	if d, ok := rawMap["data"].(string); ok {
		state.PageState.Data = d
	}
	s.cfg.State = state
	a.scheduleBrowserPageStateFlushLocked(s)
	a.browserMu.Unlock()
}

// scheduleBrowserPageStateFlushLocked debounces the persist of in-page
// state (scroll/zoom/data). The injected page-state script reports on every
// scroll event; without a debounce each message costs an actor invoke plus a
// synchronous persist.Save on the browsermanager lane. Mirrors
// browserWindow.pageStateFlushTimer in browser_window_operator.go.
// Caller must hold a.browserMu.
func (a *App) scheduleBrowserPageStateFlushLocked(s *browserSession) {
	if s.pageStateFlush != nil {
		s.pageStateFlush.Stop()
	}
	s.pageStateFlush = time.AfterFunc(windowStateFlushDebounce, func() {
		a.browserMu.Lock()
		if a.browserSessions[s.id] != s {
			a.browserMu.Unlock()
			return
		}
		if s.pageStateFlush != nil {
			s.pageStateFlush.Stop()
			s.pageStateFlush = nil
		}
		cfg := s.cfg
		a.browserMu.Unlock()
		if cfg.ID != "" {
			_ = a.syncSessionState(s.id, cfg)
		}
	})
}

func (a *App) browserManagerRef() (ref.Ref, bool) {
	if a.handle == nil {
		return nil, false
	}
	return a.handle.App().Service().Lookup("browsermanager")
}

func (a *App) browserInstanceConfig(id string) (domain.BrowserInstanceConfig, bool) {
	ref, ok := a.browserManagerRef()
	if !ok {
		return domain.BrowserInstanceConfig{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), browserInvokeTimeout)
	defer cancel()
	stream := ref.Invoke(ctx, "browsermanager.list", nil)
	if stream == nil {
		return domain.BrowserInstanceConfig{}, false
	}
	defer stream.Close()
	raw, err := stream.RecvRaw()
	if err != nil {
		return domain.BrowserInstanceConfig{}, false
	}
	var resp domain.BrowserManagerListResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return domain.BrowserInstanceConfig{}, false
	}
	for _, inst := range resp.Items {
		if inst.Config.ID == id {
			return inst.Config, true
		}
	}
	return domain.BrowserInstanceConfig{}, false
}

func (a *App) syncSessionState(sessionID string, cfg domain.BrowserInstanceConfig) error {
	ref, ok := a.browserManagerRef()
	if !ok {
		return fmt.Errorf("desktop: browsermanager service not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), browserInvokeTimeout)
	defer cancel()
	stream := ref.Invoke(ctx, "browsermanager.internal_update_state", cfg)
	if stream == nil {
		return fmt.Errorf("desktop: browsermanager.internal.update_state invoke returned nil")
	}
	defer stream.Close()
	_, err := stream.RecvRaw()
	return err
}

func (a *App) restoreBrowserPageState(sessionID string) {
	a.browserMu.Lock()
	s := a.browserSessions[sessionID]
	ps := s.cfg.State.PageState
	a.browserMu.Unlock()
	if s == nil || s.kind != "independent" || s.window == nil {
		return
	}
	if ps.ScrollX == 0 && ps.ScrollY == 0 && ps.Zoom == 0 && ps.Data == "" {
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
	s.window.ExecJS(script)
}

// paintFallbackTimeout bounds how long a session waits for the page's
// first-paint signal before revealing the window unconditionally. Guarantees
// the window never gets stuck hidden if the injected paint script cannot run
// (e.g. opaque navigation-error pages, or scripts blocked by CSP).
const paintFallbackTimeout = 1500 * time.Millisecond

// OpenBrowserSession creates or reuses a child Wails window for the given
// session and positions it over the right panel tab area. kind determines the
// user-data profile: "global" (shared singleton), "independent" (isolated),
// or "app" (plugin page).
func (a *App) OpenBrowserSession(sessionID, kind, url string, x, y, width, height int) error {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()

	if a.app == nil {
		return fmt.Errorf("desktop: app not bound")
	}

	if s := a.browserSessions[sessionID]; s != nil && s.window != nil {
		// Don't call SetURL: the child window retains its own navigation state
		// (link clicks, form submissions). Re-navigating would reload the page.
		s.window.SetBounds(application.Rect{X: x, Y: y, Width: width, Height: height})
		s.wanted = true
		a.mainWindowActive = true
		// Hide all other sessions first so only this one becomes visible.
		// This prevents stale Show() calls from leaked async open() operations
		// on previously-active tabs from leaving multiple windows on screen.
		for id, other := range a.browserSessions {
			if id != sessionID {
				other.wanted = false
			}
		}
		a.applyBrowserVisibility()
		// Sync the current confirmed URL/title/history-state back to the frontend
		// so the address bar, tab label and toolbar buttons reflect the actual
		// page. Only confirmedURL (the last committed document) is projected.
		go func(url, title string) {
			if url != "" {
				a.app.Event.Emit("browser:navigated:"+sessionID, url)
			}
			if title != "" {
				a.app.Event.Emit("browser:page-info:"+sessionID, map[string]string{"u": url, "t": title})
			}
			a.emitHistoryState(sessionID, s)
		}(s.confirmedURL, s.title)
		return nil
	}

	_, err := a.createBrowserSessionLocked(sessionID, kind, url, x, y, width, height, nil)
	return err
}

// registerGlobalWindowLocked registers the just-created Global tab window in
// the operator under the stable "global" key so browsermanager.use can
// observe/automate it. Multiple global sessions (global-agent, global-<ts>)
// share one profile; the latest created wins the key, which matches the
// visible tab because creation hides all other sessions. Caller must hold
// a.browserMu.
func (a *App) registerGlobalWindowLocked(s *browserSession) {
	if a.browserOp == nil || s == nil || s.window == nil || s.kind != "global" {
		return
	}
	if err := a.browserOp.RegisterExternal(globalWindowKey, s.window); err != nil {
		a.desktopLog("error", "register global browser window failed", map[string]any{"err": err})
	}
}

// refreshGlobalWindowRegistrationLocked re-selects the operator's "global"
// window after a global session was destroyed: if any global session survives,
// its window takes over the stable key; otherwise the registration is removed.
// Caller must hold a.browserMu.
func (a *App) refreshGlobalWindowRegistrationLocked() {
	if a.browserOp == nil {
		return
	}
	for _, s := range a.browserSessions {
		if s.kind == "global" && s.window != nil {
			if err := a.browserOp.RegisterExternal(globalWindowKey, s.window); err != nil {
				a.desktopLog("error", "re-register global browser window failed", map[string]any{"err": err})
			}
			return
		}
	}
	a.browserOp.RemoveWindow(globalWindowKey)
}

// createBrowserSessionLocked creates a new native WebView2 child window for
// the session and loads url. It is the shared creation path used by Open
// (explicit) and by Navigate when the session or its window is missing (contract
// §4: Navigate(session==nil|window==nil) must create the session then load the
// URL — equivalent to the Open path — so agent/API callers and non-BrowserView
// callers are not silently dropped). When prev is non-nil (a session whose window
// was destroyed but whose retained state survives), the new session preserves
// kind/cfg/confirmedURL/title so a recreate continues where the prior window left
// off instead of resetting to a blank document.
//
// Caller must hold a.browserMu.
func (a *App) createBrowserSessionLocked(sessionID, kind, url string, x, y, width, height int, prev *browserSession) (*browserSession, error) {
	if a.app == nil {
		return nil, fmt.Errorf("desktop: app not bound")
	}

	userDataPath := browserProfilePath(kind, sessionID)
	if err := os.MkdirAll(userDataPath, 0755); err != nil {
		return nil, fmt.Errorf("desktop: create right browser profile dir: %w", err)
	}

	// Resolve the instance config before window creation: the proxy must be
	// passed via AdditionalBrowserArgs at creation time (it cannot be applied
	// to an already-created WebView2 environment).
	var cfg domain.BrowserInstanceConfig
	var hasCfg bool
	if prev != nil {
		cfg, hasCfg = prev.cfg, true
	} else if kind == "independent" {
		cfg, hasCfg = a.browserInstanceConfig(sessionID)
	}
	var browserArgs []string = slices.Clone(browserBaseArgs)
	if hasCfg {
		browserArgs = appendProxyArgs(browserArgs, cfg)
	}
	if kind == "independent" {
		// Independent profiles keep only login state; the HTTP disk cache is
		// centralized under the global profile directory.
		browserArgs = appendSharedCacheArg(browserArgs)
	}

	opts := application.WebviewWindowOptions{
		Name:            "browser-" + sessionID,
		Title:           "Browser",
		Width:           width,
		Height:          height,
		URL:             url,
		JS:              browserInjectScript() + "\n" + browserStateScript(),
		Hidden:          true,
		Frameless:       true,
		DisableResize:   true,
		InitialPosition: application.WindowXY,
		// Solid background tinted to the current theme so the WebView2 surface
		// never shows black before the first paint. Transparent backgrounds are
		// ignored by the platform (forced to 0,0,0,0) and were the root cause of
		// the black flash. Updated live via SetBackgroundColour on theme change.
		BackgroundType:   application.BackgroundTypeSolid,
		BackgroundColour: browserBackgroundColour(a.browserDark),
		Windows: application.WindowsWindow{
			HiddenOnTaskbar:                   true,
			WebviewUserDataPath:               userDataPath,
			DisableFramelessWindowDecorations: true,
			AdditionalBrowserArgs:             browserArgs,
		},
	}

	win := a.app.Window.NewWithOptions(opts)
	win.MessageHandler = func(message string) {
		a.handleBrowserMessage(sessionID, message)
	}
	s := &browserSession{id: sessionID, kind: kind, window: win, wanted: true}
	// Preserve retained state from a destroyed-window session (Navigate recreate
	// path, prev != nil): kind/cfg/confirmedURL/title survive a window close so a
	// recreate continues where the previous window left off instead of resetting
	// to a blank document.
	if prev != nil {
		s.cfg = prev.cfg
		s.confirmedURL = prev.confirmedURL
		s.title = prev.title
	}
	if kind == "independent" && prev == nil && hasCfg {
		s.cfg = cfg
		if cfg.State.URL != "" {
			// Seed the confirmed document from the persisted last-known-good
			// URL so the address bar and stale-event gate have a baseline
			// before the first NavigationCompleted commit lands.
			s.confirmedURL = cfg.State.URL
		}
		if cfg.State.Title != "" {
			s.title = cfg.State.Title
		}
	}
	// Allocate a host pending for the initial URL load (contract §4: source=open,
	// status=loading). The adapter's first NavigationStarting (which may carry
	// commandToken 0 for NewWithOptions) will bind or supersede this pending;
	// either way the NavigationCompleted commits the initial document.
	if url != "" {
		s.beginHostNavigation(navSourceOpen, url)
		a.armNavWatchdogLocked(s)
	}
	a.browserSessions[sessionID] = s
	a.mainWindowActive = true
	// Hide all other sessions so only this new one becomes visible.
	for id, other := range a.browserSessions {
		if id != sessionID {
			other.wanted = false
		}
	}
	a.reparentSession(s)
	win.SetBounds(application.Rect{X: x, Y: y, Width: width, Height: height})

	// The Global tab is not a browsermanager child actor (open_global emits an
	// event instead of spawning one), so register its window in the operator
	// under the stable "global" key to make it observable/automatable via
	// browsermanager.use.
	if kind == "global" {
		a.registerGlobalWindowLocked(s)
	}
	// Independent right-panel windows are the browsermanager tab-mode
	// instances the composer %-mention mounts (browser-chat:<instanceId>): the
	// browserinstance child actor skips op.Create for Mode "tab" (the frontend
	// owns the window), so without registering the session window here the
	// crawl engine's browsermanager.use(InstanceID) finds no operator window
	// and every crawl.start on a mounted browser fails at the first navigate.
	if kind == "independent" {
		if a.browserOp != nil {
			if err := a.browserOp.RegisterExternal(sessionID, s.window); err != nil {
				a.desktopLog("error", "register independent browser window failed", map[string]any{"id": sessionID, "err": err.Error()})
			}
		}
	}

	// NavigationStarting is the authoritative navigation-start signal (contract
	// §2.5). It carries the adapter's commandToken (§2.5.2): non-zero for a host
	// command, zero for a page-initiated navigation. The host binds the pending's
	// native identity via this token — it never reads or compares the raw native
	// NavId frontier (§2.5.2 reliability argument).
	//
	// NavStarted == false (§2.5.2 step 4) is the adapter's "no navigation"
	// correlation answer (e.g. GoBack when CanGoBack == false): the pending is
	// resolved to idle so the session is not stuck loading.
	win.OnWindowEvent(events.Windows.WebViewNavigationStarting, func(evt *application.WindowEvent) {
		var (
			navID      uint64
			cmdToken   uint64
			isRedirect bool
			navStarted = true
		)
		if ctx := evt.Context(); ctx != nil {
			navID = ctx.NavigationID()
			cmdToken = ctx.CommandToken()
			isRedirect = ctx.IsRedirected()
			navStarted = ctx.NavStarted()
		}
		a.browserMu.Lock()
		if !navStarted {
			// No-navigation correlation answer (§2.5.2 step 4): the host command
			// produced no top-level navigation. Resolve the pending to idle/loaded.
			s.resolveNoNavigation()
			a.stopNavWatchdogLocked(s)
		} else {
			s.onNavigationStarting(navID, cmdToken, isRedirect)
			// Re-arm the completion watchdog on every start event: host binds,
			// in-page supersedes and redirect hops all legitimately extend the
			// loading window.
			a.armNavWatchdogLocked(s)
		}
		a.browserMu.Unlock()
		// Emit the normalized state (contract §5). This covers page-initiated
		// navigations too — NavigationStarting fires for links/forms/scripts, so
		// the frontend sees loading even without a host command. Redirect hops
		// and no-nav resolves are no-ops on status, so a redundant emit is harmless.
		a.emitBrowserState(sessionID)
	})

	// NavigationCompleted is the commit boundary (contract §3 / §2.5.5). Only a
	// Completed whose NavigationId matches the active navigation's nativeNavID
	// (bound via commandToken, §2.5.3/§2.5.4) commits: success writes the
	// post-redirect final URL into confirmedURL, failure records the error and
	// leaves confirmedURL at the last good value. A stale Completed (a superseded
	// navigation finishing late) is rejected and never touches confirmedURL, the
	// title or persisted state. This is what prevents old-page events from
	// overwriting the current document.
	//
	// The window still stays hidden until the page reports first paint (see the
	// "painted" message): showing at commit reveals the bare WebView2 surface,
	// which flashes black before the page paints.
	win.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(evt *application.WindowEvent) {
		var (
			navID        uint64
			finalURL     string
			success      = true
			webErrStatus int32
		)
		if ctx := evt.Context(); ctx != nil {
			navID = ctx.NavigationID()
			finalURL = ctx.URL()
			success = ctx.IsSuccess()
			webErrStatus = ctx.WebErrorStatus()
		}
		docTitle := s.window.GetDocumentTitle()

		a.browserMu.Lock()
		committed := s.commitNavigation(navID, finalURL, success, webErrStatus)
		adopted := s.navAdopted
		var (
			emitURL, emitTitle string
			cfg                domain.BrowserInstanceConfig
			persist            bool
		)
		if committed {
			// The pending is resolved: its watchdog has nothing left to guard.
			a.stopNavWatchdogLocked(s)
			// Title is authoritative from the native document on commit.
			if docTitle != "" {
				s.title = docTitle
			}
			emitURL = s.confirmedURL
			emitTitle = s.title
			if s.kind == "independent" {
				if success && finalURL != "" {
					s.cfg.State.URL = finalURL
					s.cfg.State.History = appendBrowserHistory(s.cfg.State.History, finalURL, docTitle, time.Now())
				}
				if docTitle != "" {
					s.cfg.State.Title = docTitle
				}
				cfg = s.cfg
				persist = true
			}
			// Safety net: if no first-paint signal arrives, reveal the window
			// anyway so it is never stuck hidden.
			if !s.painted && s.paintFallback == nil {
				s.paintFallback = time.AfterFunc(paintFallbackTimeout, func() {
					a.markSessionPainted(sessionID)
				})
			}
		}
		dark := a.browserDark
		a.browserMu.Unlock()

		if !committed {
			return // stale NavigationCompleted: drop silently.
		}
		if adopted {
			a.desktopLog("warn", "browser nav: adopted unbound stale NavigationCompleted", map[string]any{
				"session": sessionID, "navID": navID, "url": finalURL,
			})
		}
		s.window.SetPreferredColorScheme(dark)
		// The normalized state event (contract §5) is the authoritative projection
		// for loading/address-bar/title/error. Emit it first; the legacy events
		// below are transition-compat only.
		a.emitBrowserState(sessionID)
		if success {
			a.app.Event.Emit("browser:page-loaded:"+sessionID, nil)
		} else {
			// Surface the URL that failed (the attempted finalURL) so the error
			// is informative; the address bar keeps the last confirmed document.
			errURL := finalURL
			if errURL == "" {
				errURL = emitURL
			}
			a.app.Event.Emit("browser:navigation-error:"+sessionID, errURL)
		}
		if emitURL != "" {
			a.app.Event.Emit("browser:navigated:"+sessionID, emitURL)
		}
		if emitURL != "" || emitTitle != "" {
			a.app.Event.Emit("browser:page-info:"+sessionID, map[string]string{"u": emitURL, "t": emitTitle})
		}
		if persist && cfg.ID != "" {
			_ = a.syncSessionState(sessionID, cfg)
		}
		a.restoreBrowserPageState(sessionID)
	})

	win.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		a.browserMu.Lock()
		var wasGlobal bool
		if s := a.browserSessions[sessionID]; s != nil {
			wasGlobal = s.kind == "global"
			if s.paintFallback != nil {
				s.paintFallback.Stop()
				s.paintFallback = nil
			}
			if s.pageStateFlush != nil {
				s.pageStateFlush.Stop()
				s.pageStateFlush = nil
			}
			a.stopNavWatchdogLocked(s)
		}
		delete(a.browserSessions, sessionID)
		// Drop the closed independent window's operator registration so
		// browsermanager.use fails fast with operator_not_bound instead of
		// driving a destroyed window handle.
		if !wasGlobal && a.browserOp != nil {
			a.browserOp.RemoveWindow(sessionID)
		}
		// If the closed session was the Global tab, hand the stable "global"
		// operator key to the next surviving global session (or drop it).
		if wasGlobal {
			a.refreshGlobalWindowRegistrationLocked()
		}
		a.browserMu.Unlock()
	})

	return s, nil
}

// UpdateBrowserWindow resizes and repositions the child browser window for
// the given session. Coordinates are relative to the main window's client area.
func (a *App) UpdateBrowserWindow(sessionID string, x, y, width, height int) {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()

	s := a.browserSessions[sessionID]
	if s == nil || s.window == nil {
		return
	}
	s.window.SetBounds(application.Rect{X: x, Y: y, Width: width, Height: height})
}

// Default bounds for an auto-created right browser window (Navigate create path,
// contract §4). No UI frame is available for an agent/API/non-BrowserView caller,
// so a sensible right-panel size is used; the frontend re-syncs exact bounds via
// UpdateBrowserWindow once its BrowserView mounts.
const (
	defaultBrowserWidth  = 480
	defaultBrowserHeight = 720
)

// navigateAction is the resolved intent for NavigateBrowserWindow. It is
// computed by resolveNavigateLocked WITHOUT mutating session state.
type navigateAction struct {
	create              bool   // session/window missing → create then load (contract §4)
	kind                string // creation kind (create == true only)
	x, y, width, height int    // creation bounds (create == true only)
	sameURL             bool   // existing window + same document → Reload path
}

// resolveCreateKind determines the kind for an auto-created session from the
// available signals, independent of I/O. It is the pure, unit-testable core of
// resolveBrowserKind. Order:
//  1. a retained session (window destroyed but state survives) keeps its kind;
//  2. a persisted independent instance config → "independent";
//  3. otherwise the shared "global" tab (safe default for agent/API callers).
func resolveCreateKind(prev *browserSession, hasIndependentConfig bool) string {
	if prev != nil && prev.kind != "" {
		return prev.kind
	}
	if hasIndependentConfig {
		return "independent"
	}
	return "global"
}

// resolveBrowserKind resolves the kind for an auto-created session (contract
// §4 Navigate-create path).
func (a *App) resolveBrowserKind(sessionID string, prev *browserSession) string {
	_, hasIndep := a.browserInstanceConfig(sessionID)
	return resolveCreateKind(prev, hasIndep)
}

// resolveNavigateLocked resolves the Navigate intent for a session WITHOUT
// mutating session state (contract §4). Caller must hold a.browserMu.
//
// Crucially it never begins a host pending: beginHostNavigation is the caller's
// responsibility and is performed ONLY on the existing-window branches. The
// create branch performs its own beginHostNavigation(navSourceOpen) inside the
// shared creation path, so no host pending is ever left without a corresponding
// native command (no dangling pending / "stuck loading"). This closes the old
// bug where Navigate began an address-bar pending on a session whose window was
// nil and then silently returned, stranding the pending.
func (a *App) resolveNavigateLocked(sessionID, url string, s *browserSession) navigateAction {
	if s == nil || s.window == nil {
		return navigateAction{
			create: true,
			kind:   a.resolveBrowserKind(sessionID, s),
			x:      0,
			y:      0,
			width:  defaultBrowserWidth,
			height: defaultBrowserHeight,
		}
	}
	return navigateAction{
		sameURL: s.confirmedURL != "" && sameDocument(url, s.confirmedURL),
	}
}

// NavigateBrowserSession changes the URL of a session's child browser window.
//
// Contract §4: when the session or its window does not exist (session==nil or
// window==nil) it MUST create the session and then load url — equivalent to the
// Open path — rather than silently returning. This is the basis of the empty-tab
// first-navigation fix: an agent/API call or any non-BrowserView caller that
// navigates a session that was never opened now creates it (reusing the Open
// creation path, which performs its own beginHostNavigation(navSourceOpen)).
//
// For an existing window it allocates a host pending (contract §2.5.1/§2.5.3)
// BEFORE invoking the tokenized primitive, so the session enters loading and the
// pending can be bound when the adapter's NavigationStarting carries the
// commandToken. It uses the tokenized host primitives (contract §8): SetURL for a
// new URL, Reload for the same URL. ExecJS(window.location.href/reload) is
// forbidden for host navigation because it carries commandToken 0 and would be
// misclassified as a page-initiated navigation, losing host correlation. The
// navigation flows through the native NavigationStarting/Completed lifecycle, so
// confirmedURL is set exclusively by the commit — the address bar is never
// optimistically updated here.
func (a *App) NavigateBrowserSession(sessionID, url string) {
	a.browserMu.Lock()
	s := a.browserSessions[sessionID]
	action := a.resolveNavigateLocked(sessionID, url, s)
	if action.create {
		// Contract §4: create the session then load url. The creation path issues
		// its own beginHostNavigation(navSourceOpen), so no address-bar pending is
		// allocated here and none is left dangling. prev carries retained state.
		_, err := a.createBrowserSessionLocked(sessionID, action.kind, url, action.x, action.y, action.width, action.height, s)
		a.browserMu.Unlock()
		if err != nil {
			a.desktopLog("warn", "navigate auto-create session failed", map[string]any{"id": sessionID, "url": url, "err": err.Error()})
		}
		return
	}
	// Existing window: begin the host pending now that a native primitive will
	// actually be issued (win != nil is guaranteed by resolveNavigateLocked).
	s.beginHostNavigation(navSourceAddressBar, url)
	a.armNavWatchdogLocked(s)
	win := s.window
	a.browserMu.Unlock()
	// Do NOT hide the window or clear painted here. Hiding a WebView2 window
	// suspends its renderer (requestAnimationFrame pauses, navigation may be
	// deferred), so the new page never loads while hidden — the "stuck loading"
	// symptom. Keeping the window visible lets the navigation and paint proceed
	// normally; the old page simply stays on screen until the new one loads,
	// which is standard browser behaviour.
	if action.sameURL {
		win.Reload()
	} else {
		win.SetURL(url)
	}
}

// BrowserHistoryBack navigates the session's child browser window back
// one entry in its native WebView2 history stack.
func (a *App) BrowserHistoryBack(sessionID string) error {
	a.browserMu.Lock()
	s := a.browserSessions[sessionID]
	if s == nil || s.window == nil {
		a.browserMu.Unlock()
		return fmt.Errorf("desktop: right browser session %q not found", sessionID)
	}
	s.beginHostNavigation(navSourceBackForward, "")
	a.armNavWatchdogLocked(s)
	a.browserMu.Unlock()
	s.window.GoBack()
	a.emitHistoryState(sessionID, s)
	return nil
}

// BrowserHistoryForward navigates the session's child browser window
// forward one entry in its native WebView2 history stack.
func (a *App) BrowserHistoryForward(sessionID string) error {
	a.browserMu.Lock()
	s := a.browserSessions[sessionID]
	if s == nil || s.window == nil {
		a.browserMu.Unlock()
		return fmt.Errorf("desktop: right browser session %q not found", sessionID)
	}
	s.beginHostNavigation(navSourceBackForward, "")
	a.armNavWatchdogLocked(s)
	a.browserMu.Unlock()
	s.window.GoForward()
	a.emitHistoryState(sessionID, s)
	return nil
}

// emitBrowserState pushes the normalized navigation state (contract §5 main
// event) to the frontend as browser:state:<id>{status,confirmedURL,
// attemptedURL?,title?,error?}. The frontend projects loading / address-bar /
// title / error exclusively from this event. The legacy navigated /
// navigation-error / page-loaded events are still emitted alongside for
// transition compatibility but are derived from the same commit and must not
// drive frontend state.
func (a *App) emitBrowserState(sessionID string) {
	a.browserMu.Lock()
	s := a.browserSessions[sessionID]
	if s == nil {
		a.browserMu.Unlock()
		return
	}
	payload := s.browserStatePayload()
	a.browserMu.Unlock()
	a.app.Event.Emit("browser:state:"+sessionID, payload)
}

// emitHistoryState queries the native WebView2 CanGoBack/CanGoForward,
// caches the result on the session, and pushes it to the frontend.
func (a *App) emitHistoryState(sessionID string, s *browserSession) {
	canBack := s.window.CanGoBack()
	canForward := s.window.CanGoForward()
	a.browserMu.Lock()
	if s := a.browserSessions[sessionID]; s != nil {
		s.canBack = canBack
		s.canForward = canForward
	}
	a.browserMu.Unlock()
	a.app.Event.Emit("browser:history-state:"+sessionID, map[string]bool{"canBack": canBack, "canForward": canForward})
}

// CloseBrowserSession closes and destroys a session's child browser window.
// It does NOT remove the persisted browser instance — closing a tab only
// hides the window. The frontend separately calls closeBrowserInstance to
// set Open=false so the instance survives and can be reopened later.
func (a *App) CloseBrowserSession(sessionID string) {
	a.browserMu.Lock()
	s := a.browserSessions[sessionID]
	var wasGlobal bool
	if s != nil {
		wasGlobal = s.kind == "global"
		if s.paintFallback != nil {
			s.paintFallback.Stop()
			s.paintFallback = nil
		}
		if s.pageStateFlush != nil {
			s.pageStateFlush.Stop()
			s.pageStateFlush = nil
		}
		a.stopNavWatchdogLocked(s)
	}
	delete(a.browserSessions, sessionID)
	// Independent windows unregister from the operator on close (the crawl
	// surface must not keep a stale handle).
	if !wasGlobal && a.browserOp != nil {
		a.browserOp.RemoveWindow(sessionID)
	}
	// If the closed session was the Global tab, hand the stable "global"
	// operator key to the next surviving global session (or drop it).
	if wasGlobal {
		a.refreshGlobalWindowRegistrationLocked()
	}
	a.browserMu.Unlock()

	if s != nil && s.window != nil {
		s.window.Close()
	}
}

// SetBrowserWindowVisible shows or hides a session's child browser window
// without destroying it, so state is preserved when switching tabs.
func (a *App) SetBrowserWindowVisible(sessionID string, visible bool) {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()

	s := a.browserSessions[sessionID]
	if s == nil {
		return
	}
	s.wanted = visible
	a.applyBrowserVisibility()
}

// CaptureBrowserWindow captures the current on-screen content of a
// session's child browser window and returns it as a data: URL. Used so the
// frontend can show a frozen snapshot while the real window is temporarily
// hidden (e.g. for dropdown menus).
func (a *App) CaptureBrowserWindow(sessionID string) (string, error) {
	a.browserMu.Lock()
	s := a.browserSessions[sessionID]
	hwnd := unsafe.Pointer(nil)
	if s != nil && s.window != nil {
		hwnd = s.window.NativeWindow()
	}
	a.browserMu.Unlock()

	if hwnd == nil {
		return "", fmt.Errorf("desktop: browser session %q not found", sessionID)
	}
	return captureWindowRegion(hwnd)
}

// HideAllBrowserWindows hides every browser session immediately, regardless of
// its wanted state, and raises the persistent overlay suppression so sessions
// created or first-painted while the overlay is open (e.g. the boot-time tab
// restore racing the crash overlay) stay hidden too. Driven only by the
// frontend BrowserOverlayManager when the first HTML overlay opens.
func (a *App) HideAllBrowserWindows() {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()
	a.browserOverlaySuppressed = true
	for _, s := range a.browserSessions {
		if s.window != nil {
			s.shown = false
			s.window.Hide()
		}
	}
}

// ShowAllBrowserWindows clears the overlay suppression and re-applies the
// sessions' wanted state. Driven by the frontend BrowserOverlayManager when the
// last HTML overlay closes.
func (a *App) ShowAllBrowserWindows() {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()
	a.browserOverlaySuppressed = false
	a.applyBrowserVisibility()
}

// DestroyAllBrowserWindows closes and removes every browser session.
// Called on frontend startup to clean up any child windows left over from
// a previous run or a hot-reload.
func (a *App) DestroyAllBrowserWindows() {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()
	// Every session is torn down, so any stale overlay suppression (e.g. a
	// frontend reload while the crash overlay was open) is cleared too — a
	// leftover flag would keep every future session hidden with no overlay.
	a.browserOverlaySuppressed = false
	for id, s := range a.browserSessions {
		if s.paintFallback != nil {
			s.paintFallback.Stop()
			s.paintFallback = nil
		}
		if s.pageStateFlush != nil {
			s.pageStateFlush.Stop()
			s.pageStateFlush = nil
		}
		a.stopNavWatchdogLocked(s)
		if s.window != nil {
			s.window.Close()
		}
		if s.kind == "independent" && a.browserOp != nil {
			a.browserOp.RemoveWindow(id)
		}
		delete(a.browserSessions, id)
	}
	// No sessions remain; drop the "global" operator registration.
	a.refreshGlobalWindowRegistrationLocked()
}

// browserBackgroundColour returns the WebView2 background colour matching the
// theme. Applied as a solid background so the surface shows a sane colour
// (white / dark grey) instead of black before the first paint.
func browserBackgroundColour(dark bool) application.RGBA {
	if dark {
		return application.RGBA{Red: 30, Green: 30, Blue: 30, Alpha: 255}
	}
	return application.RGBA{Red: 255, Green: 255, Blue: 255, Alpha: 255}
}

// SetBrowserColorScheme updates the preferred color scheme of every
// right-panel browser window so pages honour prefers-color-scheme. mode is
// "light" or "dark".
func (a *App) SetBrowserColorScheme(mode string) {
	dark := mode == "dark"
	a.browserMu.Lock()
	a.browserDark = dark
	sessions := make([]*browserSession, 0, len(a.browserSessions))
	for _, s := range a.browserSessions {
		if s.window != nil {
			sessions = append(sessions, s)
		}
	}
	a.browserMu.Unlock()

	bg := browserBackgroundColour(dark)
	for _, s := range sessions {
		s.window.SetPreferredColorScheme(dark)
		// Repaint the solid background to the new theme so the pre-paint
		// surface matches even before the page re-renders.
		s.window.SetBackgroundColour(bg)
	}
}

// markSessionPainted records that the session's page has rendered a frame and
// reveals the window if the frontend wants it visible. Idempotent.
func (a *App) markSessionPainted(sessionID string) {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()
	s := a.browserSessions[sessionID]
	if s == nil {
		return
	}
	if s.paintFallback != nil {
		s.paintFallback.Stop()
		s.paintFallback = nil
	}
	s.painted = true
	a.applyBrowserVisibility()
}

// markSessionNavigating is called when the current page reports it is being
// unloaded (pagehide). It records the navigating state but does NOT hide the
// window: hiding a WebView2 window suspends its renderer, which prevents the
// new page from loading. Keeping the window visible lets the navigation
// proceed naturally.
func (a *App) markSessionNavigating(sessionID string) {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()
	s := a.browserSessions[sessionID]
	if s == nil {
		return
	}
}

// navWatchdogTimeout bounds how long a navigation may stay in loading without
// a NavigationCompleted before the watchdog reconciles the pending against the
// webview's actual document URL (Source).
const navWatchdogTimeout = 15 * time.Second

// navShowReissueMinAge is the loading age above which re-showing a hidden
// window re-issues the in-flight navigation. A suspended renderer may have
// dropped it, and the show transition is the earliest moment the renderer is
// guaranteed active again. Younger pendings are left alone so a healthy
// in-flight load is never restarted by a quick tab switch.
const navShowReissueMinAge = 5 * time.Second

// armNavWatchdogLocked (re)arms the completion watchdog for a session's
// in-flight pending. Armed at every navigation start (host begin and every
// NavigationStarting, so long redirect chains legitimately extend the window)
// and stopped wherever the pending resolves. Caller must hold browserMu.
func (a *App) armNavWatchdogLocked(s *browserSession) {
	if s.navWatchdog != nil {
		s.navWatchdog.Stop()
	}
	s.navWatchdog = time.AfterFunc(navWatchdogTimeout, func() { a.onNavWatchdog(s.id) })
}

// stopNavWatchdogLocked cancels the completion watchdog. Called wherever the
// pending is resolved or the session is torn down. Caller must hold
// browserMu.
func (a *App) stopNavWatchdogLocked(s *browserSession) {
	if s.navWatchdog != nil {
		s.navWatchdog.Stop()
		s.navWatchdog = nil
	}
}

// reissuePendingNavigationLocked repairs a wedged in-flight navigation by
// re-invoking its tokenized primitive (at most once per pending — the
// reissued flag is the loop breaker). The primitive is invoked on a fresh
// goroutine so the COM marshal to the main thread never happens under
// browserMu (the main thread may itself be waiting for the mutex in a
// binding). Caller must hold browserMu.
func (a *App) reissuePendingNavigationLocked(s *browserSession) bool {
	p := s.pendingNavigation
	if p == nil || p.reissued || s.window == nil {
		return false
	}
	reload, url, ok := reissuePrimitive(p)
	if !ok {
		return false
	}
	p.reissued = true
	win := s.window
	if reload {
		go win.Reload()
	} else {
		go win.SetURL(url)
	}
	a.desktopLog("warn", "browser nav: re-issued dropped navigation", map[string]any{
		"session": s.id, "source": string(p.source), "url": url, "reload": reload,
	})
	return true
}

// onNavWatchdog fires when a pending has been loading longer than
// navWatchdogTimeout without a NavigationCompleted. It reconciles the pending
// against the webview's actual document URL (Source) and repairs the session
// so it can never stick in loading forever:
//
//   - Source == requested URL → the document IS the target: commit as loaded.
//   - Source == old confirmed document → the navigation was dropped
//     (suspended renderer, lost event): re-issue it once with a fresh token.
//   - Source elsewhere → a redirect chain is progressing: grant one more
//     grace round, then resolve as failed.
//
// While the window is hidden the renderer is suspended, so a slow pending is
// legitimately deferred rather than lost: the watchdog only extends and lets
// the show transition perform the repair.
func (a *App) onNavWatchdog(sessionID string) {
	a.browserMu.Lock()
	s := a.browserSessions[sessionID]
	if s == nil {
		a.browserMu.Unlock()
		return
	}
	s.navWatchdog = nil
	if s.pendingNavigation == nil || s.navigationStatus != navStatusLoading {
		a.browserMu.Unlock()
		return
	}
	if !s.shown {
		// Renderer suspended while hidden: extend, do not adjudicate. The
		// show transition re-issues aged pendings; a later watchdog round with
		// the window visible performs the full reconciliation.
		s.pendingNavigation.watchdogExts++
		a.armNavWatchdogLocked(s)
		a.browserMu.Unlock()
		return
	}
	pendingID := s.pendingNavigation.id
	requested := s.pendingNavigation.requestedURL
	reissued := s.pendingNavigation.reissued
	exts := s.pendingNavigation.watchdogExts
	confirmed := s.confirmedURL
	win := s.window
	a.browserMu.Unlock()

	// Ground-truth query outside the lock (same pattern as GetDocumentTitle in
	// the NavigationCompleted handler).
	sourceURL := ""
	if win != nil {
		sourceURL = win.Source()
	}
	verdict := navWatchdogVerdict(sourceURL, requested, confirmed, reissued, exts)

	a.browserMu.Lock()
	s = a.browserSessions[sessionID]
	if s == nil || s.pendingNavigation == nil || s.pendingNavigation.id != pendingID ||
		s.navigationStatus != navStatusLoading {
		// A newer navigation (or teardown) raced the reconciliation: its own
		// lifecycle owns the session now.
		a.browserMu.Unlock()
		return
	}
	s.navWatchdog = nil
	var (
		persistCfg domain.BrowserInstanceConfig
		persist    bool
		emit       bool
	)
	switch verdict {
	case navWatchdogCommit:
		a.desktopLog("warn", "browser nav watchdog: committed lost completion from Source", map[string]any{
			"session": sessionID, "url": sourceURL,
		})
		s.commitNavigation(0, sourceURL, true, 0)
		if s.kind == "independent" && sourceURL != "" {
			s.cfg.State.URL = sourceURL
			persistCfg, persist = s.cfg, true
		}
		if !s.painted && s.paintFallback == nil {
			s.paintFallback = time.AfterFunc(paintFallbackTimeout, func() {
				a.markSessionPainted(sessionID)
			})
		}
		emit = true
	case navWatchdogReissue:
		if a.reissuePendingNavigationLocked(s) {
			a.armNavWatchdogLocked(s)
			break
		}
		// Not re-issuable (back/forward pending): fall through to failure.
		s.pendingNavigation = nil
		s.lastError = "navigation_watchdog_timeout"
		s.attemptedURL = requested
		s.navigationStatus = navStatusFailed
		emit = true
		a.desktopLog("warn", "browser nav watchdog: stuck back/forward navigation resolved as failed", map[string]any{
			"session": sessionID, "source": sourceURL,
		})
	case navWatchdogExtend:
		s.pendingNavigation.watchdogExts++
		a.armNavWatchdogLocked(s)
	case navWatchdogFail:
		s.pendingNavigation = nil
		s.lastError = "navigation_watchdog_timeout"
		s.attemptedURL = requested
		s.navigationStatus = navStatusFailed
		emit = true
		a.desktopLog("warn", "browser nav watchdog: resolved stuck loading as failed", map[string]any{
			"session": sessionID, "requested": requested, "source": sourceURL,
		})
	}
	a.browserMu.Unlock()
	if persist && persistCfg.ID != "" {
		_ = a.syncSessionState(sessionID, persistCfg)
	}
	if emit {
		a.emitBrowserState(sessionID)
	}
}

// browserSessionShouldShow decides whether a session's native child window may
// be visible: the frontend wants it, the main window is active, and neither a
// main-window resize nor an open HTML overlay suppresses child windows.
// First-paint is deliberately NOT a condition: the WebView2 surface is
// theme-coloured at creation (Solid background + SetBackgroundColour), so
// showing before the page paints reveals the theme colour, not a black or
// white flash. Gating on paint also deadlocks under WebView2 runtimes that
// suspend rendering for hidden controllers — the page can never paint while
// the window waits for paint. Suppression still wins over every other signal.
func browserSessionShouldShow(wanted, mainWindowActive, mainWindowResizing, overlaySuppressed bool) bool {
	return wanted && mainWindowActive && !mainWindowResizing && !overlaySuppressed
}

// applyBrowserVisibility reconciles visibility of all sessions with the
// frontend's desired state and the main window's activity. Caller must hold
// browserMu.
func (a *App) applyBrowserVisibility() {
	for _, s := range a.browserSessions {
		if s.window == nil {
			continue
		}
		// During a main-window resize we intentionally suppress showing child
		// browsers (they are re-shown by WindowEndResize), but we still allow
		// hiding so that closing/minimising the panel works mid-resize.
		shouldShow := browserSessionShouldShow(s.wanted, a.mainWindowActive, a.mainWindowResizing, a.browserOverlaySuppressed)
		switch {
		case shouldShow && !s.shown:
			s.shown = true
			s.window.Show()
			// Tell the frontend the native window is now on screen so it can
			// drop its loading overlay exactly when the page covers it.
			a.app.Event.Emit("browser:shown:"+s.id, nil)
			// Repair: a navigation deferred while the renderer was suspended
			// (tab inactive / overlay open) may have been dropped by Chromium.
			// The window is visible again, so the renderer is active: re-issue
			// an aged in-flight pending; the completion watchdog still bounds
			// the session. Younger pendings are left alone.
			if s.pendingNavigation != nil && s.navigationStatus == navStatusLoading &&
				!s.pendingNavigation.reissued &&
				time.Since(s.pendingNavigation.startedAt) >= navShowReissueMinAge {
				if a.reissuePendingNavigationLocked(s) {
					a.armNavWatchdogLocked(s)
				}
			}
		case !shouldShow && s.shown:
			s.shown = false
			s.window.Hide()
		}
	}
}

// reparentSession makes the session's browser window a true child of the main
// window via Win32 SetParent. Once parented, Windows handles move/clip/z-order
// automatically. Position becomes relative to the main window's client area.
func (a *App) reparentSession(s *browserSession) {
	if s.window == nil || a.window == nil {
		return
	}
	setChildWindow(s.window.NativeWindow(), a.window.NativeWindow())
}

// setMainWindowActive updates the active flag and re-applies child visibility.
func (a *App) setMainWindowActive(active bool) {
	a.browserMu.Lock()
	a.mainWindowActive = active
	a.applyBrowserVisibility()
	a.browserMu.Unlock()
}

// registerMainWindowEvents wires main-window lifecycle events. The child
// browser window is parented to the main window via SetParent, so Windows
// handles move/clip/z-order automatically — no move/focus tracking needed.
// We handle minimise/hide (hide child) and main-window resize (suppress show
// via a dedicated flag, then re-apply visibility when resizing ends).
func (a *App) registerMainWindowEvents() {
	if a.window == nil {
		return
	}
	a.mainWindowActive = true

	a.window.OnWindowEvent(events.Common.WindowMinimise, func(_ *application.WindowEvent) {
		a.setMainWindowActive(false)
	})
	a.window.OnWindowEvent(events.Common.WindowHide, func(_ *application.WindowEvent) {
		a.setMainWindowActive(false)
	})
	a.window.OnWindowEvent(events.Common.WindowUnMinimise, func(_ *application.WindowEvent) {
		a.setMainWindowActive(true)
	})
	a.window.OnWindowEvent(events.Common.WindowRestore, func(_ *application.WindowEvent) {
		a.setMainWindowActive(true)
	})

	// Resize: hide the child browser windows while the OS main window is being
	// resized, then re-show them when resizing completes. We use a dedicated
	// mainWindowResizing flag rather than mutating mainWindowActive so that
	// right-panel handle drags (pure frontend DOM operations) are not mistaken
	// for a main-window resize and do not toggle visibility.
	if a.app != nil {
		a.window.OnWindowEvent(events.Windows.WindowStartResize, func(_ *application.WindowEvent) {
			a.browserMu.Lock()
			a.mainWindowResizing = true
			a.applyBrowserVisibility()
			a.browserMu.Unlock()
			a.app.Event.Emit("browser:layout-changing", nil)
		})
		a.window.OnWindowEvent(events.Windows.WindowEndResize, func(_ *application.WindowEvent) {
			a.browserMu.Lock()
			a.mainWindowResizing = false
			a.applyBrowserVisibility()
			a.browserMu.Unlock()
			a.app.Event.Emit("browser:layout-settled", nil)
		})
	}
}

// SaveScreenshotDialog opens a save dialog and writes the provided base64 image.
func (a *App) SaveScreenshotDialog(base64Data, defaultName string) error {
	if a.app == nil {
		return fmt.Errorf("desktop: not started")
	}
	path, err := a.app.Dialog.SaveFile().
		SetFilename(defaultName).
		AddFilter("PNG Image", "*.png").
		PromptForSingleSelection()
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	// Strip data URL prefix
	prefix := "data:image/png;base64,"
	if strings.HasPrefix(base64Data, prefix) {
		base64Data = base64Data[len(prefix):]
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

// BrowserPageSnapshot is the DTO returned to the frontend.
type BrowserPageSnapshot struct {
	URL            string            `json:"url"`
	Title          string            `json:"title"`
	Text           string            `json:"text"`
	Cookies        string            `json:"cookies"`
	LocalStorage   map[string]string `json:"local_storage"`
	SessionStorage map[string]string `json:"session_storage"`
}

// GetBrowserPageSnapshot injects a reader script into the browser window, waits
// for the page data to arrive via postMessage, and returns it. This lets the
// frontend inspect the DOM text, cookies, and web storage of any open instance.
func (a *App) GetBrowserPageSnapshot(id string) (BrowserPageSnapshot, error) {
	if a.browserOp == nil {
		return BrowserPageSnapshot{}, fmt.Errorf("desktop: browser operator not initialised")
	}
	if err := a.browserOp.RequestSnapshot(id); err != nil {
		return BrowserPageSnapshot{}, err
	}

	// The reader script runs async; poll for up to 2s for the snapshot to arrive.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := a.browserOp.GetSnapshot(id)
		if err == nil && snap != nil {
			return BrowserPageSnapshot{
				URL:            snap.URL,
				Title:          snap.Title,
				Text:           snap.Text,
				Cookies:        snap.Cookies,
				LocalStorage:   snap.LocalStorage,
				SessionStorage: snap.SessionStorage,
			}, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return BrowserPageSnapshot{}, fmt.Errorf("desktop: snapshot timeout for instance %q", id)
}

// GetBuildInfo returns the build metadata snapshot for the current binary.
// Bound to the frontend via Wails v3 `wails3 generate bindings -ts`.
func (a *App) GetBuildInfo() buildinfo.Info {
	return buildinfo.Get()
}

// GetFeatureFlags returns the feature flags gated by the current build type.
// Bound to the frontend via Wails v3 `wails3 generate bindings -ts`.
func (a *App) GetFeatureFlags() buildinfo.FeatureFlags {
	return buildinfo.GetFeatureFlags()
}
