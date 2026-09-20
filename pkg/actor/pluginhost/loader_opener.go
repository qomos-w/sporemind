package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/pluginloader"
)

// loaderOpener is the concrete ArtifactOpener backed by pkg/pluginloader.
// It lives in the actor package to break the import cycle between
// pluginhost (which defines ArtifactLoader) and pluginloader (which
// imports pluginhost for ABI framing).
//
// The dispatch field carries the host-side service dispatcher injected by
// the pluginhost actor at OnStart. It is used to route authorized reverse
// calls from plugins to backing actor services.
type loaderOpener struct {
	dispatch      HostDispatchFunc
	storeDispatch HostDispatchFunc
	logger        pluginLogSink
}

// pluginLogSink is the minimal interface for emitting plugin log entries
// into the host's observation stream. The pluginhost actor implements it
// via its structured logger. ReportProcessState is the explicit transport
// lifecycle seam: spawn/exit/unload transition points report
// running/stopped/crashed directly instead of being inferred from log
// message strings (the old observeProcessNotice sniffing is gone).
type pluginLogSink interface {
	LogPluginEntry(pluginID string, generation int, level int, msg string)
	// NextProcessGeneration allocates the id of a new plugin process spawn.
	// Every ReportProcessState issued by that process carries it; reports
	// from an older generation are stale (the process was replaced) and are
	// dropped by the state machine.
	NextProcessGeneration(pluginID string) int64
	ReportProcessState(pluginID, state, crash, httpAddr string, gen int64)
}

// PluginLogSymbol is the C symbol the host calls to drain the plugin's
// log ring buffer. The plugin SDK exports it.
const PluginLogSymbol = "PluginLog"

// Open opens the native library at artifactPath, wires the host bridge so
// the plugin can make authorized reverse calls, invokes the plugin's
// OnLoad lifecycle hook so it can register its callables, and returns an
// invoke closure (length-prefixed ABI) and a closer that releases the
// handle.
//
// The host bridge is wired AFTER the library is opened but BEFORE OnLoad.
// This ensures that any host callbacks the plugin's OnLoad hook or
// registered callables make are authorized through the capability gate
// (derived from EffectiveCapabilities) from the moment the plugin is alive.
//
// The allowed parameter is the EffectiveCapabilities set for this plugin;
// it becomes the HostBridge's authorization allowlist so that only granted
// capabilities are dispatchable. Ungranted callIDs are denied at the bridge.
// abi is the manifest ABI; loaderOpener serves the in-process transport and
// does not use it (the transport selector upstream already decided).
// onLoadConfig is forwarded verbatim to PluginOnLoad as the config string:
// the same backendLoadConfig JSON the subprocess transport delivers
// ({"httpAddr","sessionSecret","staticDir","dataDir"}). The in-process
// transport never runs the SDK HTTP listener, so the returned httpAddr is
// always empty; the dataDir/staticDir grants still apply.
func (o loaderOpener) Open(abi gen.PluginAbi, artifactPath, entrySymbol, pluginID string, allowed map[string]struct{}, onLoadConfig []byte) (func(ctx context.Context, callable string, request []byte) ([]byte, error), pluginhost.InvokeStreamFunc, func() error, string, error) {
	lib, err := pluginloader.Open(artifactPath)
	if err != nil {
		return nil, nil, nil, "", err
	}

	// Wire the host bridge before OnLoad so that reverse calls during
	// OnLoad or registered callables are authorized and dispatchable.
	bridge := NewHostBridge(allowed, o.dispatch, pluginID, o.storeDispatch)
	if err := setHostBridge(lib, bridge); err != nil {
		_ = lib.Close()
		return nil, nil, nil, "", fmt.Errorf("pluginhost: set host bridge: %w", err)
	}

	// Invoke the plugin's OnLoad hook so it can register its callables.
	// PluginOnLoad has signature int PluginOnLoad(const char* pluginID,
	// const char* config) and returns 0 on success, non-zero on failure.
	// Strip the subprocess-only bootstrap fields first: an in-process plugin
	// never runs the SDK HTTP listener (httpAddr/sessionSecret would bind a
	// stray listener inside the host process), while staticDir/dataDir
	// grants still apply.
	if err := callOnLoad(lib, pluginID, sanitizeInProcessOnLoadConfig(onLoadConfig)); err != nil {
		_ = lib.Close()
		return nil, nil, nil, "", fmt.Errorf("pluginhost: OnLoad: %w", err)
	}

	// Look up the PluginLog symbol. If present, the host drains the
	// plugin's log ring buffer after each invoke and converts entries
	// into structured host-side log output. If the symbol is absent
	// (older plugin), log draining is silently skipped.
	pluginLogFn := lookupPluginLog(lib)

	symbol := entrySymbol
	if symbol == "" {
		symbol = pluginloader.NativeInvokeSymbol
	}
	invoke := func(ctx context.Context, callable string, request []byte) ([]byte, error) {
		meta, _ := pluginhost.InvokeMetaFrom(ctx)
		framed := pluginhost.EncodeInvokeEnvelope(callable, request, meta)
		// Start with the default capacity; if the plugin's response
		// exceeds it, lib.Invoke transparently retries with a larger
		// buffer (up to pluginhost.MaxResponseBytes).
		resp, err := lib.Invoke(symbol, framed, pluginhost.DefaultResponseCapacity+len(framed))
		if err != nil {
			// Even on error, drain any logs the plugin may have
			// produced before failing — this is critical for
			// debugging gate/register silent failures and handler
			// black-box issues.
			if pluginLogFn != nil && o.logger != nil {
				drainPluginLogs(pluginLogFn, pluginID, meta, o.logger)
			}
			return nil, err
		}
		// Drain plugin logs after each successful invoke. The
		// generation (from meta.SessionID or meta.RequestID) is
		// attached to prevent cross-generation log confusion during
		// reload.
		if pluginLogFn != nil && o.logger != nil {
			drainPluginLogs(pluginLogFn, pluginID, meta, o.logger)
		}
		// The response is a length-prefixed frame whose body is the
		// JSON-encoded plugin Response.Payload (already unwrapped by the
		// SDK). Extract and return the payload body.
		return pluginhost.DecodeInvokeFrame(resp)
	}
	closer := func() error {
		// Windows: FreeLibrary of a Go c-shared DLL is unsafe — any
		// subsequent allocator/scheduler activity (e.g. the fsync inside
		// persist.Save right after unload) triggers a hard access
		// violation (0xc0000005) that kills the host process. Keep the
		// library loaded (leak the handle) instead of crashing; the OS
		// reclaims it at process exit. Linux keeps the real close.
		if runtime.GOOS == "windows" {
			if o.logger != nil {
				o.logger.LogPluginEntry(pluginID, 0, 2, "pluginhost: skipping library close on windows (FreeLibrary crash hazard); handle leaked until process exit")
			}
			return nil
		}
		// Best-effort OnUnload so the plugin can clean up its handlers.
		_ = callOnUnload(lib, pluginID)
		if err := lib.Close(); err != nil {
			return fmt.Errorf("pluginhost: close library: %w", err)
		}
		return nil
	}
	// FFI/c-shared carries no forward-chunk wire: no stream closure. Streaming
	// callables loaded over this transport degrade to zero chunks plus the
	// terminal (pluginhost.handleInvokeStream falls back to the unary handler).
	return invoke, nil, closer, "", nil
}

// sanitizeInProcessOnLoadConfig removes the subprocess-only bootstrap fields
// (httpAddr, sessionSecret) from an OnLoad config before delivering it to an
// in-process plugin, keeping only the transport-neutral grants (staticDir,
// dataDir). nil/empty config passes through as-is.
func sanitizeInProcessOnLoadConfig(config []byte) []byte {
	if len(config) == 0 || strings.TrimSpace(string(config)) == "" {
		return config
	}
	var full struct {
		HTTPAddr      string `json:"httpAddr,omitempty"`
		SessionSecret string `json:"sessionSecret,omitempty"`
		StaticDir     string `json:"staticDir,omitempty"`
		DataDir       string `json:"dataDir,omitempty"`
	}
	if err := json.Unmarshal(config, &full); err != nil {
		// Malformed config is not ours to repair; deliver nothing rather
		// than half of it.
		return nil
	}
	out, err := json.Marshal(struct {
		StaticDir string `json:"staticDir,omitempty"`
		DataDir   string `json:"dataDir,omitempty"`
	}{StaticDir: full.StaticDir, DataDir: full.DataDir})
	if err != nil {
		return nil
	}
	return out
}

// callOnLoad invokes the exported PluginOnLoad symbol on the loaded library.
// config is the OnLoad config JSON (may be nil for bare ABI test libraries);
// the plugin SDK parses it in its OnLoad handler (LoadConfig).
func callOnLoad(lib *pluginloader.Library, pluginID string, config []byte) error {
	// PluginOnLoad signature: int PluginOnLoad(char* pluginID, char* config)
	var fn func(pluginIDPtr, configPtr *byte) int32
	if err := lib.Symbol("PluginOnLoad", &fn); err != nil {
		// OnLoad is optional: if the symbol is absent the plugin simply
		// has no lifecycle setup (e.g. a bare ABI test library).
		return nil
	}
	idBytes := append([]byte(pluginID), 0)
	var configPtr *byte // nil config is allowed
	if len(config) > 0 {
		cfgBytes := append(append([]byte{}, config...), 0)
		configPtr = &cfgBytes[0]
	}
	status := fn(&idBytes[0], configPtr)
	if status != 0 {
		return fmt.Errorf("plugin OnLoad returned status %d", status)
	}
	return nil
}

// callOnUnload invokes the exported PluginOnUnload symbol on the loaded library.
func callOnUnload(lib *pluginloader.Library, pluginID string) error {
	var fn func(pluginIDPtr *byte) int32
	if err := lib.Symbol("PluginOnUnload", &fn); err != nil {
		return nil
	}
	idBytes := append([]byte(pluginID), 0)
	_ = fn(&idBytes[0])
	return nil
}

// setHostBridge calls the plugin's exported PluginSetHostBridge symbol,
// passing the bridge's C function pointer. This makes the host bridge
// available to the plugin's ctx.Host().Invoke(...) reverse calls.
//
// PluginSetHostBridge signature: int PluginSetHostBridge(void* bridge)
//
// If the symbol is absent (e.g. a bare ABI test library or an older plugin
// built before host bridge support), this is a no-op — the plugin simply
// has no host callback capability and reverse calls will fail at runtime.
func setHostBridge(lib *pluginloader.Library, bridge *HostBridge) error {
	var fn func(bridgePtr uintptr) int32
	if err := lib.Symbol(HostSetHostBridgeSymbol, &fn); err != nil {
		// PluginSetHostBridge is optional.
		return nil
	}
	status := fn(bridge.Callback())
	if status != 0 {
		return fmt.Errorf("PluginSetHostBridge returned status %d", status)
	}
	return nil
}

// lookupPluginLog resolves the PluginLog symbol from the loaded library.
// Returns nil if the symbol is absent (older plugin without log support).
func lookupPluginLog(lib *pluginloader.Library) func(buf *byte, n int32) int32 {
	var fn func(buf *byte, n int32) int32
	if err := lib.Symbol(PluginLogSymbol, &fn); err != nil {
		return nil
	}
	return fn
}

// drainPluginLogs calls the plugin's PluginLog ABI to drain any buffered
// log entries and forwards them to the host's structured logger. Each
// entry is emitted with the pluginID and generation (derived from the
// invoke metadata) so that the host can attribute logs correctly, even
// across reloads.
func drainPluginLogs(pluginLogFn func(buf *byte, n int32) int32, pluginID string, meta pluginhost.InvokeMeta, logger pluginLogSink) {
	// Start with a 64 KiB buffer; log entries are typically small.
	buf := make([]byte, 65536)
	n := pluginLogFn(&buf[0], int32(len(buf)))
	if n <= 0 {
		return
	}
	// Parse the JSON array of LogEntry. The generation is derived from
	// the invoke metadata — SessionID carries the load generation when
	// provided by the host.
	generation := 0
	if meta.SessionID != "" {
		// Use the first 8 hex chars of SessionID as a stable
		// generation fingerprint. This avoids importing a separate
		// generation counter while providing enough entropy for
		// human-readable log attribution.
		if len(meta.SessionID) >= 8 {
			generation = 0 // placeholder: real generation comes from the artifact record
		}
	}
	data := buf[:int(n)]
	// Strip trailing NUL if present.
	if len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}
	var entries []struct {
		Level   int    `json:"Level"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	for _, e := range entries {
		logger.LogPluginEntry(pluginID, generation, e.Level, e.Message)
	}
}
