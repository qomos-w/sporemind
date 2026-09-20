package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func TestLogRingPushDrain(t *testing.T) {
	r := newLogRing()
	r.push(LogLevelInfo, "hello")
	r.push(LogLevelWarn, "warning")

	entries, dropped := r.drain()
	if dropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", dropped)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Message != "hello" || entries[0].Level != LogLevelInfo {
		t.Fatalf("entry[0] mismatch: %+v", entries[0])
	}
	if entries[1].Message != "warning" || entries[1].Level != LogLevelWarn {
		t.Fatalf("entry[1] mismatch: %+v", entries[1])
	}

	// Second drain should be empty.
	entries2, dropped2 := r.drain()
	if len(entries2) != 0 || dropped2 != 0 {
		t.Fatalf("expected empty second drain, got %d entries, %d dropped", len(entries2), dropped2)
	}
}

func TestLogRingOverflowDropsOldest(t *testing.T) {
	// Use a small-capacity ring by creating a custom one.
	r := &logRing{
		entries: make([]LogEntry, 3),
	}
	// Fill to capacity (3 entries).
	r.push(LogLevelInfo, "msg0")
	r.push(LogLevelInfo, "msg1")
	r.push(LogLevelInfo, "msg2")

	// Push 2 more — should drop the 2 oldest.
	r.push(LogLevelInfo, "msg3")
	r.push(LogLevelInfo, "msg4")

	entries, dropped := r.drain()
	if dropped != 2 {
		t.Fatalf("expected 2 dropped, got %d", dropped)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (capacity), got %d", len(entries))
	}
	// After dropping msg0 and msg1, the buffer should contain msg2, msg3, msg4.
	if entries[0].Message != "msg2" {
		t.Fatalf("expected msg2 first, got %q", entries[0].Message)
	}
	if entries[1].Message != "msg3" {
		t.Fatalf("expected msg3 second, got %q", entries[1].Message)
	}
	if entries[2].Message != "msg4" {
		t.Fatalf("expected msg4 third, got %q", entries[2].Message)
	}
}

func TestLogRingConcurrentPush(t *testing.T) {
	r := newLogRing()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.push(LogLevelInfo, fmt.Sprintf("concurrent-%d", n))
		}(i)
	}
	wg.Wait()

	entries, dropped := r.drain()
	total := len(entries) + dropped
	if total != 100 {
		t.Fatalf("expected 100 total (entries+dropped), got %d entries + %d dropped = %d", len(entries), dropped, total)
	}
}

func TestHandlePluginLogNoState(t *testing.T) {
	// Without a registered+loaded plugin, HandlePluginLog should return -1.
	n := HandlePluginLog(nil, 0)
	if n != -1 {
		t.Fatalf("expected -1 with no state, got %d", n)
	}
}

func TestHandlePluginLogRoundTrip(t *testing.T) {
	// Register and load a plugin so currentState works.
	Register(&Plugin{
		Manifest: Manifest{ID: "test.log", Name: "TestLog", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			// Emit a log entry from inside the handler.
			ctx.Log(LogLevelInfo, "test log message %d", 42)
			ctx.Log(LogLevelError, "something went wrong")
			return nil
		},
	})

	// Trigger OnLoad to initialize state and push log entries.
	id := append([]byte("test.log"), 0)
	status := HandleOnLoad(unsafe.Pointer(&id[0]), nil)
	if status != 0 {
		t.Fatalf("HandleOnLoad failed: %d", status)
	}

	// Drain the log entries via HandlePluginLog.
	buf := make([]byte, 8192)
	n := HandlePluginLog(unsafe.Pointer(&buf[0]), int32(len(buf)))
	if n <= 0 {
		t.Fatalf("HandlePluginLog returned %d", n)
	}

	data := buf[:int(n)]
	// Strip trailing NUL if present.
	if len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}

	var entries []LogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal log entries: %v\ndata: %s", err, string(data))
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Message != "test log message 42" || entries[0].Level != LogLevelInfo {
		t.Fatalf("entry[0] mismatch: %+v", entries[0])
	}
	if entries[1].Message != "something went wrong" || entries[1].Level != LogLevelError {
		t.Fatalf("entry[1] mismatch: %+v", entries[1])
	}

	// Second drain should be empty.
	n2 := HandlePluginLog(unsafe.Pointer(&buf[0]), int32(len(buf)))
	if n2 <= 0 {
		// An empty array "[]" is 2 bytes + NUL = 3, so n2 should be 2.
		t.Fatalf("second drain should return at least empty array, got %d", n2)
	}
	data2 := buf[:int(n2)]
	if len(data2) > 0 && data2[len(data2)-1] == 0 {
		data2 = data2[:len(data2)-1]
	}
	var empty []LogEntry
	if err := json.Unmarshal(data2, &empty); err != nil {
		t.Fatalf("unmarshal second drain: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected 0 entries on second drain, got %d", len(empty))
	}

	// Cleanup: unload the plugin.
	HandleOnUnload(unsafe.Pointer(&id[0]))
}

func TestHandlePluginLogBufferTooSmall(t *testing.T) {
	Register(&Plugin{
		Manifest: Manifest{ID: "test.toosmall", Name: "TestTooSmall", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			ctx.Log(LogLevelInfo, "this is a log message that won't fit")
			return nil
		},
	})

	id := append([]byte("test.toosmall"), 0)
	status := HandleOnLoad(unsafe.Pointer(&id[0]), nil)
	if status != 0 {
		t.Fatalf("HandleOnLoad failed: %d", status)
	}

	// Provide a tiny buffer — should return -1.
	buf := make([]byte, 2)
	n := HandlePluginLog(unsafe.Pointer(&buf[0]), int32(len(buf)))
	if n != -1 {
		t.Fatalf("expected -1 for too-small buffer, got %d", n)
	}

	HandleOnUnload(unsafe.Pointer(&id[0]))
}

func TestHandlePluginLogDroppedWarning(t *testing.T) {
	Register(&Plugin{
		Manifest: Manifest{ID: "test.dropped", Name: "TestDropped", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			// Push more than logRingCapacity entries to trigger drops.
			for i := 0; i < logRingCapacity+10; i++ {
				ctx.Log(LogLevelInfo, "overflow entry %d", i)
			}
			return nil
		},
	})

	id := append([]byte("test.dropped"), 0)
	status := HandleOnLoad(unsafe.Pointer(&id[0]), nil)
	if status != 0 {
		t.Fatalf("HandleOnLoad failed: %d", status)
	}

	buf := make([]byte, 1<<20)
	n := HandlePluginLog(unsafe.Pointer(&buf[0]), int32(len(buf)))
	if n <= 0 {
		t.Fatalf("HandlePluginLog returned %d", n)
	}

	data := buf[:int(n)]
	if len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}

	var entries []LogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The first entry should be the synthetic dropped warning.
	if len(entries) == 0 {
		t.Fatal("expected at least 1 entry")
	}
	if entries[0].Level != LogLevelWarn {
		t.Fatalf("expected first entry to be Warn level, got %d", entries[0].Level)
	}
	// The remaining entries should be logRingCapacity (the max).
	if len(entries) != logRingCapacity+1 {
		t.Fatalf("expected %d entries (capacity + 1 warning), got %d", logRingCapacity+1, len(entries))
	}

	HandleOnUnload(unsafe.Pointer(&id[0]))
}

// TestPluginLogExportCompiles verifies that the PluginLog export function
// signature compiles correctly when used as a C export. This is a compile-
// time check: if the function signature is wrong, the c-shared build will
// fail.
func TestPluginLogExportCompiles(t *testing.T) {
	// Verify HandlePluginLog has the expected signature by calling it
	// with the same types the export wrapper uses.
	var buf unsafe.Pointer
	var n int32
	_ = HandlePluginLog(buf, n)

	// Verify the log levels are defined and ordered.
	if LogLevelDebug != 0 || LogLevelInfo != 1 || LogLevelWarn != 2 || LogLevelError != 3 {
		t.Fatalf("log levels not in expected order: debug=%d info=%d warn=%d error=%d",
			LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError)
	}
}

// TestContextLogNilRingIsSafe verifies that calling Log on a context with
// a nil logRing is a safe no-op (e.g. when the context is constructed
// outside the normal lifecycle).
func TestContextLogNilRingIsSafe(t *testing.T) {
	ctx := &pluginContext{pluginID: "test.nil", manifest: Manifest{}}
	// Should not panic.
	ctx.Log(LogLevelInfo, "this should be a no-op")
}

// TestPackageLogNoStateIsSafe verifies the package-level Log is a silent
// no-op when no plugin lifecycle state is active (best-effort logging).
func TestPackageLogNoStateIsSafe(t *testing.T) {
	// Reset global state so currentState errors.
	Register(nil)
	SetProcessLogWriter(nil)
	// Should not panic.
	Log(LogLevelInfo, "no active state: %s", "dropped")
}

// TestPackageLogReachesRing verifies package-level Log lands in the active
// plugin's log ring, the path handler bodies use (no Context parameter on
// codegen handler stubs).
func TestPackageLogReachesRing(t *testing.T) {
	Register(&Plugin{
		Manifest: Manifest{ID: "test.pkglog", Name: "TestPkgLog", Version: "1.0.0"},
		OnLoad:   func(ctx Context) error { return nil },
	})
	id := append([]byte("test.pkglog"), 0)
	if status := HandleOnLoad(unsafe.Pointer(&id[0]), nil); status != 0 {
		t.Fatalf("HandleOnLoad failed: %d", status)
	}

	Log(LogLevelWarn, "handler says %d", 7)

	buf := make([]byte, 8192)
	n := HandlePluginLog(unsafe.Pointer(&buf[0]), int32(len(buf)))
	if n <= 0 {
		t.Fatalf("HandlePluginLog returned %d", n)
	}
	data := buf[:int(n)]
	if len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}
	var entries []LogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal log entries: %v\ndata: %s", err, string(data))
	}
	if len(entries) != 1 || entries[0].Message != "handler says 7" || entries[0].Level != LogLevelWarn {
		t.Fatalf("entries mismatch: %+v", entries)
	}
}

// Init-stage Log calls (before RunProcess installs the writer and before
// OnLoad activates a state) must not vanish: they buffer and flush as the
// first 0x05 frames when SetProcessLogWriter runs.
func TestEarlyLogsFlushOnWriterInstall(t *testing.T) {
	SetProcessLogWriter(nil)
	Register(nil)
	resetEarlyLogsForTest()
	Log(LogLevelWarn, "init-stage message %d", 1)
	Log(LogLevelError, "init-stage message 2")
	var buf bytes.Buffer
	SetProcessLogWriter(&buf)
	defer SetProcessLogWriter(nil)
	var seen []string
	for {
		typ, _, payload, err := readFrame(&buf)
		if err != nil {
			break
		}
		if typ == 0x05 {
			seen = append(seen, string(payload))
		}
	}
	if len(seen) != 2 || !strings.Contains(seen[0], "init-stage message 1") || !strings.Contains(seen[1], "init-stage message 2") {
		t.Fatalf("early logs not flushed: %v", seen)
	}
}

func resetEarlyLogsForTest() {
	activeState.Lock()
	activeState.state = nil
	activeState.Unlock()
	earlyLogs.Lock()
	earlyLogs.entries = nil
	earlyLogs.Unlock()
}
