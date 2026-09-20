package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/gateway"
)

// DefaultMinLevel is the explicit default minimum log level captured by the
// hook. Levels below this (notably debug) are dropped before they reach the
// ring buffer, the file store, or stdout, which keeps the high-volume
// per-event debug traces produced by the event bus and gateway session out of
// persistent storage.
const DefaultMinLevel = "info"

var levelRank = map[string]int{
	"debug": 0,
	"info":  1,
	"warn":  2,
	"error": 3,
}

// levelEnabled reports whether level is at or above min. Unknown levels are
// treated as info so they are never silently dropped.
func levelEnabled(min, level string) bool {
	ml, ok := levelRank[strings.ToLower(min)]
	if !ok {
		ml = levelRank["info"]
	}
	l, ok := levelRank[strings.ToLower(level)]
	if !ok {
		l = levelRank["info"]
	}
	return l >= ml
}

// NewHook returns an actor.LogHook that writes JSON lines to stdout and
// appends structured entries to the given ring buffer. Entries below minLevel
// are dropped entirely.
//
// The hook is registered with gospore via app.WithLogHook so the runtime
// itself captures the caller at the correct stack depth and forwards the
// fully-structured entry.  This eliminates the fragile runtime.Caller(2)
// depth in sporemind's own Logger and the CaptureStdlib pipe hack.
func NewHook(ring LogRing, minLevel string) actor.LogHook {
	return func(level string, msg string, caller string, fields map[string]any) {
		if !levelEnabled(minLevel, level) {
			return
		}
		entry := gateway.LogEntry{
			Timestamp: time.Now().Format(time.RFC3339Nano),
			Level:     level,
			Caller:    caller,
			Message:   sanitizeUTF8(msg),
			Fields:    sanitizeFields(fields),
		}
		if ring != nil {
			ring.Append(entry)
		}
		// Write stdout in a non-blocking goroutine. In Wails desktop mode the
		// stdout pipe can fill up when the UI produces many frame events,
		// blocking the actor that called logger.Info/Warn/Error and causing
		// child agent turns to hang indefinitely.
		go func() {
			b, _ := json.Marshal(entry)
			fmt.Fprintln(os.Stdout, string(b))
		}()
	}
}

// sanitizeUTF8 coerces a log string to valid UTF-8. Raw byte slices read
// off IPC (plugin stderr, upstream bodies) can carry invalid sequences;
// the live log stream encodes entries with a strict-UTF-8 JSON codec and
// one bad message would fail the whole batch.
func sanitizeUTF8(s string) string {
	return strings.ToValidUTF8(s, "�")
}

// sanitizeFields converts error values to their message strings so JSON
// marshaling does not collapse them to "{}" (error implementations are
// structs with unexported fields) and coerces string values to valid
// UTF-8 for the same reason as sanitizeUTF8.
func sanitizeFields(fields map[string]any) map[string]any {
	for k, v := range fields {
		if err, ok := v.(error); ok {
			fields[k] = sanitizeUTF8(err.Error())
			continue
		}
		if s, ok := v.(string); ok {
			fields[k] = sanitizeUTF8(s)
		}
	}
	return fields
}
