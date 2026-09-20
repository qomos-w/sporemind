package sdk

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Log levels for Context.Log. These mirror standard severity ordering so
// the host can filter at the structured-logger level.
const (
	LogLevelDebug int = 0
	LogLevelInfo  int = 1
	LogLevelWarn  int = 2
	LogLevelError int = 3
)

// LogEntry is a single structured log record produced by a plugin's
// Context.Log call and drained by the host through the PluginLog ABI.
// JSON tags are PascalCase to match the host-side mirror type.
type LogEntry struct {
	Level   int    `json:"Level"`
	Message string `json:"Message"`
}

// logRingCapacity is the maximum number of entries the ring buffer holds.
// When full, the oldest entry is overwritten and droppedCount is
// incremented. This is non-state data: the buffer lives in the plugin
// process memory and is never persisted.
const logRingCapacity = 256

// processLog holds the direct log-frame writer installed by the subprocess
// transport entry point (process_main). When non-nil, Context.Log emits a
// 0x05 log frame immediately instead of buffering in the ring — the ring
// only exists to serve the c-shared PluginLog drain handshake, which the
// process transport does not use (see the T4 decision D1 on the task card).
var processLog = struct {
	sync.RWMutex
	w io.Writer
}{}

// earlyLogs buffers Log calls made before either sink is available — most
// commonly package-init stage of handlers.go, which runs before RunProcess
// installs the 0x05 writer and before OnLoad activates a lifecycle state
// (the admin-tools migration could not see init logs at all). SetProcessLog
// Writer flushes the buffer as its first frames; cap bounds a runaway init.
var earlyLogs = struct {
	sync.Mutex
	entries []LogEntry
}{}

const earlyLogCap = 64

func pushEarlyLog(level int, msg string) {
	earlyLogs.Lock()
	defer earlyLogs.Unlock()
	if len(earlyLogs.entries) >= earlyLogCap {
		earlyLogs.entries = earlyLogs.entries[1:]
	}
	earlyLogs.entries = append(earlyLogs.entries, LogEntry{Level: level, Message: msg})
}

func takeEarlyLogs() []LogEntry {
	earlyLogs.Lock()
	defer earlyLogs.Unlock()
	entries := earlyLogs.entries
	earlyLogs.entries = nil
	return entries
}

// SetProcessLogWriter installs a writer for direct 0x05 log-frame emission.
// The subprocess entry point passes its frame-serialized stdout writer;
// passing nil restores the ring-buffer behavior used by the c-shared
// transport. Installing the writer flushes any early logs (init-stage
// entries) as its first frames so they reach the host log ring instead of
// vanishing.
func SetProcessLogWriter(w io.Writer) {
	if w != nil {
		for _, e := range takeEarlyLogs() {
			_ = writeLogFrame(w, e.Level, e.Message)
		}
	}
	processLog.Lock()
	processLog.w = w
	processLog.Unlock()
}

func processLogWriter() io.Writer {
	processLog.RLock()
	w := processLog.w
	processLog.RUnlock()
	return w
}

// Log appends an entry to the active plugin's log ring. It is the
// package-level counterpart to Context.Log for use inside handler bodies,
// which (by codegen contract) carry no Context — handlers receive only
// (sdk.Request) and must reach the host via sdk.ActiveHost(). On a
// subprocess-transport plugin the entry is emitted immediately as a 0x05
// log frame via the process log writer; on a c-shared plugin it goes into
// the ring for the PluginLog drain handshake. When no lifecycle state is
// active the entry is dropped silently (best-effort logging, matching
// pluginContext.Log semantics when its ring is nil).
func Log(level int, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if w := processLogWriter(); w != nil {
		_ = writeLogFrame(w, level, msg)
		return
	}
	state, err := currentState()
	if err != nil || state == nil || state.logRing == nil {
		pushEarlyLog(level, msg)
		return
	}
	state.logRing.push(level, msg)
}

// writeLogFrame emits a single 0x05 log frame whose payload is one LogEntry
// JSON object — the same entry shape the FFI PluginLog drain produces, so
// the host can decode both transports with the same parser. The caller must
// serialize writes to w (processTransport's stdout lock does this); a write
// failure is returned so the transport can escalate it, though Context.Log
// treats logging as best-effort.
func writeLogFrame(w io.Writer, level int, message string) error {
	data, err := json.Marshal(LogEntry{Level: level, Message: message})
	if err != nil {
		return err
	}
	return writeFrame(w, msgLog, "", data)
}

// logRing is a fixed-capacity circular buffer for plugin log entries.
// It is drained by the host through PluginLog. Push is called from
// within handler code (including panic-recovered paths); drain is called
// by the host after each invoke.
type logRing struct {
	mu           sync.Mutex
	entries      []LogEntry
	head         int // next write position when buffer is full
	count        int // current number of entries
	droppedCount int // entries overwritten since last drain
}

func newLogRing() *logRing {
	return &logRing{
		entries: make([]LogEntry, logRingCapacity),
	}
}

// push appends a log entry. If the buffer is full, the oldest entry is
// overwritten and droppedCount is incremented.
func (r *logRing) push(level int, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cap := len(r.entries)
	if r.count < cap {
		pos := (r.head + r.count) % cap
		r.entries[pos] = LogEntry{Level: level, Message: msg}
		r.count++
	} else {
		r.entries[r.head] = LogEntry{Level: level, Message: msg}
		r.head = (r.head + 1) % cap
		r.droppedCount++
	}
}

// drain returns all pending entries and clears the buffer. It also
// returns the count of entries that were dropped (overwritten) since the
// last drain. The caller (HandlePluginLog) uses dropped to emit a
// synthetic warning so the host sees the loss signal.
func (r *logRing) drain() (entries []LogEntry, dropped int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cap := len(r.entries)
	if r.count == 0 {
		dropped = r.droppedCount
		r.droppedCount = 0
		return nil, dropped
	}
	out := make([]LogEntry, r.count)
	for i := 0; i < r.count; i++ {
		out[i] = r.entries[(r.head+i)%cap]
	}
	dropped = r.droppedCount
	r.head = 0
	r.count = 0
	r.droppedCount = 0
	return out, dropped
}
