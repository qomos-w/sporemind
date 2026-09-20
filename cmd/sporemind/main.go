package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/qomos-w/sporemind/cmd/internal/actorset"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/diagcrash"
	"github.com/qomos-w/sporemind/pkg/i18n"
	"github.com/qomos-w/sporemind/pkg/logging"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// 这个不是agent用来交互式debug的入口,如果需要交互式debug,请使用make dev-desktop
func main() {
	// Soft memory limit. Override with SPOREMIND_MEMORY_LIMIT (e.g. "2GiB");
	// see pkg/config.MemoryLimit for resolution.
	debug.SetMemoryLimit(config.MemoryLimit())

	// Catch any panic that escapes the actor tree (bare goroutines, native
	// code, cgo) so the headless process leaves a crash file behind and
	// exits with a trace instead of dying silently. The crash file is
	// picked up on the next run by the oracle actor and surfaced as a
	// diagnostic via /debug/problems.
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			content := fmt.Sprintf(
				"sporemind headless crash report\nTime: %s\nPanic: %v\n\nStack:\n%s\n",
				time.Now().Format(time.RFC3339Nano), r, stack,
			)
			if _, err := diagcrash.Write(content); err != nil {
				// Best-effort: if the crash file write itself fails, at
				// least make the trace visible on stderr.
				fmt.Fprintln(os.Stderr, "sporemind: failed to write crash file:", err)
			}
			fmt.Fprintln(os.Stderr, content)
			log.Fatalf("sporemind: panic: %v\n%s", r, stack)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log.Println("sporemind: starting")

	i18n.SetDefault(i18n.ParseLocale(config.Locale()))

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

	// Prune logs older than the configured retention window. Run once at
	// startup (blocking, as before), then re-run on a 24h ticker for the
	// lifetime of the process; the goroutine exits when ctx is cancelled.
	retention := time.Duration(config.LogsRetentionDays()) * 24 * time.Hour
	cleanupLogs := func() {
		if err := logStore.Cleanup(retention); err != nil {
			log.Printf("sporemind: log cleanup: %v", err)
		}
		if err := consoleStore.Cleanup(retention); err != nil {
			log.Printf("sporemind: console log cleanup: %v", err)
		}
	}
	cleanupLogs()
	go func() {
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
	tail, _ := logStore.Tail(2000)
	ring := logging.NewRing(2000)
	ring.Restore(tail)
	ring.SetPersist(logStore)
	logging.CaptureStdlib(ring)

	streamer := logging.NewLogStreamer(logging.DefaultStreamBatchSize, logging.DefaultStreamWindow)
	ring.SetStreamer(streamer)
	consoleStore.SetStreamer(streamer)

	cfg := runtime.Config{
		GatewayAddr:      config.GatewayAddr(),
		GatewayBindAddrs: config.GatewayBindAddrs(),
		Namespace:        config.Namespace(),
		Children:         actorset.Default(),
		LogRing:          ring,
		LogStore:         logStore,
		ConsoleLogSource: consoleStore,
		LogStreamer:      streamer,
	}
	if err := runtime.Run(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("sporemind: %v", err)
	}
	log.Println("sporemind: stopped")
}
