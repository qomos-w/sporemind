package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/sporemind/pkg/logging"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const crashEventName = "sporemind:crash"

// SetCrashApp wires the Wails application handle so the panic handler can
// display a crash dialog. Called from App.Bind after application.New.
func SetCrashApp(app *application.App) {
	crashAppMu.Lock()
	crashApp = app
	crashAppMu.Unlock()
}

// SetCrashWindow wires the primary window so crash events can be emitted to
// the frontend before the process exits.
func SetCrashWindow(win *application.WebviewWindow) {
	crashAppMu.Lock()
	crashWindow = win
	crashAppMu.Unlock()
}

// SetCrashBrowserSuppressor wires the desktop-side hook invoked from the panic
// path to hide — and keep hidden — the native browser child windows, so the
// live crash UI raised from the same panic is never occluded. Called from
// App.Bind.
func SetCrashBrowserSuppressor(fn func()) {
	crashAppMu.Lock()
	crashSuppressBrowsers = fn
	crashAppMu.Unlock()
}

// PanicHandler returns a Wails PanicHandler that writes a crash report to disk,
// logs to the ring, emits a crash event to the frontend, and attempts to show
// a native crash dialog before the process exits.
func PanicHandler(logRing *logging.Ring) func(*application.PanicDetails) {
	return func(d *application.PanicDetails) {
		crashOnce.Do(func() {
			writeCrashReport(d)
			if logRing != nil {
				logRing.Append(gateway.LogEntry{
					Timestamp: d.Time.Format(time.RFC3339Nano),
					Level:     "error",
					Message:   fmt.Sprintf("panic: %v", d.Error),
					Fields:    map[string]any{"stack": d.StackTrace},
				})
			}
			emitCrashEvent(d)
			suppressCrashBrowserWindows()
			showCrashDialog(d)
		})
	}
}

func writeCrashReport(d *application.PanicDetails) {
	dir := crashStoreDir()
	path := filepath.Join(dir, fmt.Sprintf("%s-%d-%s.log", time.Now().Format("20060102-150405"), os.Getpid(), CrashKindPanic))
	content := fmt.Sprintf(
		"sporemind crash report\nTime: %s\nError: %v\n\nStackTrace:\n%s\n\nFullStackTrace:\n%s\n",
		d.Time.Format(time.RFC3339Nano), d.Error, d.StackTrace, d.FullStackTrace,
	)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(path, []byte(content), 0644)
	fmt.Fprintln(os.Stderr, content)
}

// suppressCrashBrowserWindows invokes the wired suppression hook (nil until
// App.Bind runs). The process is tearing down, so the suppression stays raised:
// the frontend crash overlay never dismisses and the watchdog is moot.
func suppressCrashBrowserWindows() {
	crashAppMu.RLock()
	fn := crashSuppressBrowsers
	crashAppMu.RUnlock()
	if fn != nil {
		fn()
	}
}

func emitCrashEvent(d *application.PanicDetails) {
	crashAppMu.RLock()
	win := crashWindow
	crashAppMu.RUnlock()
	if win == nil {
		return
	}
	win.DispatchWailsEvent(&application.CustomEvent{
		Name: crashEventName,
		Data: map[string]any{
			"error":     d.Error.Error(),
			"time":      d.Time.Format(time.RFC3339Nano),
			"stack":     d.StackTrace,
			"fullStack": d.FullStackTrace,
		},
	})
}

func showCrashDialog(d *application.PanicDetails) {
	crashAppMu.RLock()
	app := crashApp
	crashAppMu.RUnlock()
	if app == nil {
		return
	}
	msg := fmt.Sprintf("sporemind encountered a fatal error and must close.\n\nError: %v\n\nA crash report has been saved to the logs directory.", d.Error)
	dialog := app.Dialog.Error()
	dialog.SetTitle("sporemind — Crash")
	dialog.SetMessage(msg)
	dialog.AddButton("Close")
	dialog.Show()
}
