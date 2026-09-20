package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"time"

	"github.com/qomos-w/sporemind/cmd/internal/actorset"
	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/desktop"
	"github.com/qomos-w/sporemind/pkg/i18n"
	"github.com/qomos-w/sporemind/pkg/logging"
	"github.com/qomos-w/sporemind/pkg/runtime"
	webassets "github.com/qomos-w/sporemind/pkg/web"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func devProxy() string {
	return os.Getenv("SPOREMIND_DEV_PROXY")
}

func main() {
	freeConsole()

	// Suppress stdlib log output to os.Stderr — in a GUI-subsystem binary the
	// stderr handle is invalid, and writing to it can trigger Windows to
	// allocate a console window. Discard until CaptureStdlib redirects log
	// output into the ring buffer. Dev builds keep the console (see
	// console_windows.go) and mirror logs to stderr instead, so
	// `make dev-desktop` / `wails3 dev` terminals show startup logs.
	var stdlibTee io.Writer = io.Discard
	if buildinfo.IsDev() {
		stdlibTee = os.Stderr
	}
	log.SetOutput(stdlibTee)

	// Soft memory limit. Override with SPOREMIND_MEMORY_LIMIT (e.g. "2GiB");
	// see pkg/config.MemoryLimit for resolution. The Wails frontend has its
	// own JS heap on top of this but lives in a separate process.
	debug.SetMemoryLimit(config.MemoryLimit())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log.Println("sporemind: starting")

	i18n.SetDefault(i18n.ParseLocale(config.Locale()))

	// Dev hot-reload takeover: ask a previous dev instance to shut down
	// gracefully (no process killing). Release builds are deduplicated by the
	// Wails SingleInstance mutex instead.
	if !releaseBuild {
		desktop.ClosePreviousInstance()
	}

	var (
		logStore logging.LogStore
		err      error
	)
	switch config.ScopedBackend("logs") {
	case "goleveldb":
		logStore, err = logging.NewLevelDBLogStore(config.DataDir())
	default:
		logStore, err = logging.NewFileStore(filepath.Join(config.DataDir(), "logs"))
	}
	if err != nil {
		log.Fatalf("sporemind: log store: %v", err)
	}
	defer logStore.Close()

	consoleStore, err := logging.NewConsoleStore(filepath.Join(config.DataDir(), "logs"))
	if err != nil {
		log.Fatalf("sporemind: console store: %v", err)
	}
	defer consoleStore.Close()

	tail, _ := logStore.Tail(2000)
	ring := logging.NewRing(2000)
	ring.Restore(tail)
	ring.SetPersist(logStore)
	logging.CaptureStdlib(ring, stdlibTee)

	streamer := logging.NewLogStreamer(logging.DefaultStreamBatchSize, logging.DefaultStreamWindow)
	ring.SetStreamer(streamer)
	consoleStore.SetStreamer(streamer)

	// Native crash capture (SEH filter + minidump) and the crash session
	// marker must be in place before anything can fault — including early
	// startup — so every crash leaves a trace for the next boot.
	desktop.InstallNativeCrashHandler()
	claimed := desktop.ClaimCrashSession(os.Getpid())

	cfg := runtime.Config{
		GatewayAddr:      config.GatewayAddr(),
		GatewayBindAddrs: config.GatewayBindAddrs(),
		Namespace:        config.Namespace(),
		WebStatic:        webassets.Assets(),
		DevProxy:         devProxy(),
		Children:         actorset.Default(),
		LogRing:          ring,
		LogStore:         logStore,
		ConsoleLogSource: consoleStore,
		LogStreamer:      streamer,
	}
	handle, err := runtime.Bootstrap(ctx, cfg)
	if err != nil {
		log.Fatalf("sporemind: %v", err)
	}

	// Defer log cleanup to a background goroutine so disk I/O (scanning and
	// deleting old files) doesn't block startup or delay backend readiness.
	// Prune logs older than the configured retention window once at startup,
	// then re-run on a 24h ticker; the goroutine exits when ctx is cancelled
	// on shutdown.
	retention := time.Duration(config.LogsRetentionDays()) * 24 * time.Hour
	go func() {
		cleanupLogs := func() {
			if err := logStore.Cleanup(retention); err != nil {
				log.Printf("sporemind: log cleanup: %v", err)
			}
			if err := consoleStore.Cleanup(retention); err != nil {
				log.Printf("sporemind: console log cleanup: %v", err)
			}
		}
		cleanupLogs()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanupLogs()
			}
		}
	}()

	appSvc := desktop.New(handle, cancel, ring, logStore, consoleStore)

	// Resolve the persisted theme before the window and webview exist so the
	// native surface pre-paints the splash colour instead of flashing white.
	themeMode, themeJSON := desktop.InitialTheme(ctx, handle)
	themeDark := themeMode == "dark"

	name := appName()

	app := application.New(application.Options{
		Name:           name,
		SingleInstance: singleInstanceOptions(appSvc),
		PanicHandler:   desktop.PanicHandler(ring),
		ErrorHandler:   desktop.CrashErrorHandler,
		Services: []application.Service{
			application.NewServiceWithOptions(appSvc, application.ServiceOptions{Name: "desktop"}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(webassets.Assets()),
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Pre-paint theme boot script: sets data-theme before any
					// HTML paints, so the splash never renders the wrong
					// palette while the async theme load is in flight.
					if r.URL.Path == "/__theme.js" {
						body := fmt.Sprintf("window.__SPOREMIND_THEME__=%s\ndocument.documentElement.setAttribute('data-theme',%q)\n", themeJSON, themeMode)
						w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
						w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
						w.Header().Set("Pragma", "no-cache")
						w.Header().Set("Expires", "0")
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(body))
						return
					}
					w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
					w.Header().Set("Pragma", "no-cache")
					w.Header().Set("Expires", "0")
					next.ServeHTTP(w, r)
				})
			},
		},
		RawMessageHandler: appSvc.RawMessageHandler(),
		OnShutdown:        func() { appSvc.Shutdown() },
	})

	// Assemble the previous session's crash report. FinishBootCrashReport
	// runs after application.New (the duplicate-instance exit point) so only
	// the surviving instance surfaces it.
	appSvc.SetBootCrashReport(desktop.FinishBootCrashReport(claimed, tail))

	appSvc.Bind(app)

	windowOpts := application.WebviewWindowOptions{
		Title:         appName(),
		Width:         1280,
		Height:        800,
		DisableResize: false,
		// Enable OS-level file drop (OS → app drag-in). The wails fork then
		// lets JS dragenter/dragover/drop fire for external file drags; the
		// frontend prevents default navigation in attachOsFileDrop.
		EnableFileDrop: true,
		Frameless:      goruntime.GOOS != "darwin",
		// Solid background tinted to the persisted theme (same treatment as
		// the browser child windows) so the WebView2 surface shows the splash
		// colour before the first HTML paint instead of flashing white.
		BackgroundType:   application.BackgroundTypeSolid,
		BackgroundColour: desktop.ShellBackgroundColour(themeDark),
	}
	if goruntime.GOOS == "darwin" {
		windowOpts.Mac = application.MacWindow{
			TitleBar: application.MacTitleBarHidden,
		}
	}

	win := app.Window.NewWithOptions(windowOpts)
	appSvc.SetWindow(win)

	win.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		appSvc.BeforeClose()
	})
	// Observe geometry changes during normal operation so the latest bounds
	// are on disk before the WindowClosing race with Wails' internal
	// destroy listener can zero them out.
	for _, ev := range []events.WindowEventType{
		events.Common.WindowDidResize,
		events.Common.WindowDidMove,
		events.Common.WindowMaximise,
		events.Common.WindowUnMaximise,
		events.Common.WindowRestore,
	} {
		win.OnWindowEvent(ev, func(_ *application.WindowEvent) {
			appSvc.RecordWindowGeometry()
		})
	}

	if err := app.Run(); err != nil {
		log.Fatalf("sporemind: %v", err)
	}

	log.Println("sporemind: stopped")
}
