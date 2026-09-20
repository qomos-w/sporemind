package pluginhost

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Per-plugin log capture: a bounded ring per plugin fed by the LogPluginEntry
// funnel (handler sdk.Log output, stderr pump, subprocess exit notices) and
// by pluginhost.plugin_log_put (frontend console entries forwarded by the
// host shell). pluginhost.plugin_logs is the agent-facing query surface.
//
// The rings and process states are sync-guarded — like pluginErrStates —
// because the funnel is called from stateless invoke goroutines, not the
// owner lane.
const (
	pluginLogRingCapacity = 512
	pluginLogMaxMessage   = 512

	logSourceBackend  = "backend"
	logSourceFrontend = "frontend"
)

type pluginLogRing struct {
	mu      sync.Mutex
	entries []gen.PluginLogEntry
	dropped int
}

func (r *pluginLogRing) push(level, source, msg string) {
	// The ring feeds pluginhost.plugin_logs, whose response codec rejects
	// invalid UTF-8; a single bad entry would make the whole query fail.
	// Producers hand us raw bytes (stderr pump, frontend console strings),
	// so coerce to valid UTF-8 and truncate on a rune boundary.
	msg = strings.ToValidUTF8(msg, "�")
	if len(msg) > pluginLogMaxMessage {
		cut := pluginLogMaxMessage
		for cut > 0 && !utf8.RuneStart(msg[cut]) {
			cut--
		}
		msg = msg[:cut] + "...(truncated)"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, gen.PluginLogEntry{
		Time:    time.Now().UTC().Format(time.RFC3339Nano),
		Level:   level,
		Source:  source,
		Message: msg,
	})
	if overflow := len(r.entries) - pluginLogRingCapacity; overflow > 0 {
		r.entries = r.entries[overflow:]
		r.dropped += overflow
	}
}

// snapshot returns the newest `limit` entries in chronological order (oldest
// first). limit <= 0 returns the whole ring.
func (r *pluginLogRing) snapshot(limit int) ([]gen.PluginLogEntry, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > len(r.entries) {
		limit = len(r.entries)
	}
	out := make([]gen.PluginLogEntry, limit)
	copy(out, r.entries[len(r.entries)-limit:])
	return out, r.dropped
}

func pluginLogLevelName(level int) string {
	switch level {
	case 0:
		return "debug"
	case 1:
		return "info"
	case 2:
		return "warn"
	default:
		return "error"
	}
}

// pluginProcessInfo is the live runtime verdict for one plugin, reported
// explicitly by the transport transition points (processOpener spawn /
// recordExit / close) through the pluginLogSink ReportProcessState seam —
// no log-string inference. No separate loader seam is grown for this.
type pluginProcessInfo struct {
	mu       sync.Mutex
	state    string // running | stopped | crashed
	crash    string
	httpAddr string // listener host:port while running ("" when none)
	gen      int64  // process generation that reported this state
}

func (a *Actor) appendPluginLog(pluginID, level, source, msg string) {
	if pluginID == "" {
		return
	}
	a.logRingsMu.Lock()
	ring, ok := a.logRings[pluginID]
	if !ok {
		ring = &pluginLogRing{}
		if a.logRings == nil {
			a.logRings = map[string]*pluginLogRing{}
		}
		a.logRings[pluginID] = ring
	}
	a.logRingsMu.Unlock()
	ring.push(level, source, msg)
}

func (a *Actor) setProcessState(pluginID, state, crash, httpAddr string, gen int64) {
	if pluginID == "" {
		return
	}
	// Stale-generation guard: a report from a process that has already been
	// replaced must not overwrite the current verdict (hot reload ordering —
	// the old process's late exit vs the replacement's spawn report).
	if gen > 0 {
		if genAny, ok := a.processGenerations.Load(pluginID); ok {
			if latest := *genAny.(*int64); gen < latest {
				return
			}
		}
	}
	infoAny, loaded := a.pluginProcessStates.LoadOrStore(pluginID, &pluginProcessInfo{state: state})
	info := infoAny.(*pluginProcessInfo)
	info.mu.Lock()
	transition := !loaded || info.state != state
	info.state = state
	if crash != "" {
		info.crash = crash
	} else if state != "crashed" {
		info.crash = ""
	}
	if state == "running" && httpAddr != "" {
		info.httpAddr = httpAddr
	} else if state != "running" {
		info.httpAddr = ""
	}
	info.gen = gen
	onTransition := a.processStateOnTransition
	info.mu.Unlock()
	// onTransition is the T2 seam (wired to appmanager there); nil is a
	// no-op. It fires only on an actual state change, not on repeats.
	if transition && onTransition != nil {
		onTransition(pluginID, state, crash, httpAddr)
	}
}

// NextProcessGeneration allocates the id for a new plugin process spawn. The
// counter is monotonic per plugin for the actor's lifetime: a hot reload's
// replacement process always claims a strictly larger generation than the
// process it replaces.
func (a *Actor) NextProcessGeneration(pluginID string) int64 {
	if pluginID == "" {
		return 0
	}
	genAny, _ := a.processGenerations.LoadOrStore(pluginID, new(int64))
	return atomic.AddInt64(genAny.(*int64), 1)
}

// processGeneration returns the latest allocated process generation for the
// plugin (0 when none was spawned).
func (a *Actor) processGeneration(pluginID string) int64 {
	if genAny, ok := a.processGenerations.Load(pluginID); ok {
		return *genAny.(*int64)
	}
	return 0
}

func (a *Actor) processState(pluginID string) (state, crash string) {
	infoAny, ok := a.pluginProcessStates.Load(pluginID)
	if !ok {
		return "running", ""
	}
	info := infoAny.(*pluginProcessInfo)
	info.mu.Lock()
	defer info.mu.Unlock()
	return info.state, info.crash
}

// resetPluginLogs drops the ring and process verdict for a plugin; called on
// artifact unload so a stopped plugin does not keep accumulating state.
func (a *Actor) resetPluginLogs(pluginID string) {
	a.logRingsMu.Lock()
	delete(a.logRings, pluginID)
	a.logRingsMu.Unlock()
	a.pluginProcessStates.Delete(pluginID)
}

// handlePluginLogs is the agent-facing query: newest ring entries plus the
// live process verdict, so crash causes are visible next to the tail.
func (a *Actor) handlePluginLogs(_ actor.PureContext, req gen.PluginLogsReq) (gen.PluginLogsResp, error) {
	a.logRingsMu.Lock()
	ring := a.logRings[req.PluginID]
	a.logRingsMu.Unlock()
	state, crash := a.processState(req.PluginID)
	resp := gen.PluginLogsResp{PluginID: req.PluginID, Generation: int32(a.processGeneration(req.PluginID))}
	if ring == nil {
		resp.ProcessState = state
		resp.Crash = crash
		return resp, nil
	}
	entries, dropped := ring.snapshot(int(req.Limit))
	if len(entries) > 0 {
		resp.Entries = entries
	}
	resp.Dropped = int32(dropped)
	resp.ProcessState = state
	resp.Crash = crash
	return resp, nil
}

// handlePluginLogPut persists one frontend console entry forwarded by the
// host shell on behalf of a plugin iframe. Log-only surface: no state beyond
// the ring, so Public matches the invoke surface.
func (a *Actor) handlePluginLogPut(_ actor.PureContext, req gen.PluginLogPutReq) (gen.PluginLogsResp, error) {
	level := req.Level
	switch level {
	case "debug", "info", "warn", "error":
	default:
		level = "info"
	}
	a.appendPluginLog(req.PluginID, level, logSourceFrontend, req.Message)
	return gen.PluginLogsResp{PluginID: req.PluginID}, nil
}
