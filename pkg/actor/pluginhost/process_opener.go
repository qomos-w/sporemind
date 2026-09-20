package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/util"
)

// ReverseHandler routes a plugin reverse bridge call received during an
// invoke (a 0x03 reverse-req frame). It is the T5/T6 seam contract from the
// dual-transport workflow (workflow-plan-native-plugin-000181): T5
// (process_opener.go) consumes it in the invoke read loop, T6 (host_bridge.go
// IPC adaptation) supplies an implementation over the existing
// HostBridge.Allows/Dispatch path, and T7 wires an instance in. The contract
// is structural — *HostBridge already satisfies it via Dispatch
// (host_bridge.go:117); T6 does not need to reference this type, only match
// the method signature.
//
// Dispatch implementations MUST return the raw JSON response body on success
// and a Go error on failure; the process opener formats failures as the
// {"__host_error__": "..."} envelope (mirroring bridge.serve), never as a
// dropped pipe.
type ReverseHandler interface {
	Dispatch(callID string, req []byte) ([]byte, error)
}

// StreamReverseHandler is the optional streaming extension of ReverseHandler.
// A reverse handler that also implements it can deliver intermediate chunks
// for streaming callables (llm.chat / llm.complete): onChunk fires zero or
// more times from the dispatch goroutine — synchronously, in stream order —
// before the terminal response is returned. An onChunk error aborts the
// dispatch. Handlers that do not implement it degrade to unary (onChunk
// never fires), which is exactly the old-host contract from the plugin's
// perspective.
type StreamReverseHandler interface {
	DispatchContextStream(dctx DispatchContext, callID string, req []byte, onChunk func([]byte) error) ([]byte, error)
}

// reverseReq is the wire body of a 0x03 reverse-req frame, mirroring the SDK
// bridgeCall shape {callID, JSON payload} (sporemind-plugin-sdk/bridge.go) as
// verified by the T1 spike. Stream negotiates chunked delivery: the SDK sets
// it for InvokeStream calls; hosts that predate the field ignore it and
// answer with the terminal 0x04 only (bidirectional compatibility).
type reverseReq struct {
	CallID  string          `json:"callID"`
	Payload json.RawMessage `json:"payload"`
	Stream  bool            `json:"stream,omitempty"`
}

// processOpener is the sibling ArtifactOpener to loaderOpener for the
// subprocess transport (dev mode): instead of dlopen-ing a c-shared library
// it spawns the plugin as an independent OS process and speaks the duplex
// framing protocol from process_frame.go over stdin/stdout pipes. Killing the
// process tears the plugin's embedded Go runtime down safely with it — no
// FreeLibrary 0xc0000005 hazard.
//
// It implements pkg/pluginhost/artifact.go:93-95 ArtifactOpener: Open returns
// an invoke func(ctx, callable, req) ([]byte, error) and a closer func()
// error that terminates the process.
//
// Concurrency: invoke-reqs are single-flight per process (frameSession's
// invokeMu). The session reader loop (process_session.go) owns the stdout
// side exclusively; all stdin writes go through the session's writeMu.
// Reverse bridge calls are answered by bounded session workers and may
// interleave with (or arrive entirely outside of) invokes — full-duplex per
// the protocol-v2 design.
type processOpener struct {
	// dispatch/storeDispatch mirror loaderOpener's fields: they build the
	// default ReverseHandler (a *HostBridge) when reverse is not injected.
	dispatch HostDispatchFunc
	// dispatchStream is the streaming llm.* route installed on the default
	// HostBridge via SetStreamDispatch; nil keeps reverse calls unary.
	dispatchStream HostDispatchStreamFunc
	storeDispatch  HostDispatchFunc
	logger         pluginLogSink  // optional; forwards 0x05 log frames
	reverse        ReverseHandler // optional override; defaults to the capability-gated HostBridge
	// env, when non-nil, is the child process environment (passed to
	// exec.Cmd.Env). nil inherits the host environment. It is a general
	// spawn seam (plugin config, sandbox overrides) also used by tests to
	// flag a re-exec test binary as the plugin process.
	env []string
	// onLoadConfig is the per-instance host→plugin bootstrap payload
	// ({"httpAddr","sessionSecret"}) delivered as the OnLoad invoke payload
	// so the SDK can bind its HTTP listener and enable cookie auth. Empty
	// keeps the legacy no-config behavior. Config-carrying like env: cloned
	// into each per-load opener by transportOpener.
	onLoadConfig []byte

	// settleGrace overrides the frame session's settle grace (<=0 → default).
	// Test seam: the timeout-kill tests shrink it so the wedge policy fires
	// fast instead of waiting the production 5s.
	settleGrace time.Duration

	// observeClone, when set, is invoked with every processOpener instance
	// the transportOpener creates from this prototype (see
	// transport_select.go: each load gets its own opener so multiple
	// subprocess plugins can coexist). E2E tests use it to grab the live
	// spawn handle — the prototype itself stays process-less.
	observeClone func(*processOpener)

	// Transport state, set by Open and mutated only under killMu.
	pluginID string
	cmd      *exec.Cmd
	stdin    io.WriteCloser // host -> plugin direction
	stdout   io.Reader      // plugin -> host direction
	stderr   io.ReadCloser  // plugin stderr -> host log pump; closed on kill
	// httpAddr is the bound host:port of the plugin's own HTTP listener,
	// parsed from the OnLoad response after a config-driven spawn; "" when
	// the plugin runs no listener.
	httpAddr string
	// gen is this spawn's process generation (claimed from the log sink at
	// spawn); carried by every state report this process issues.
	gen int64

	// session is the v2 frame session owning the stdout read loop and all
	// stdin writes for this spawn. Created in Open, replaced on respawn.
	session *frameSession

	// killMu guards kill/close/reap — idempotent so concurrent invoke
	// failure paths and the ArtifactLoader closer never double-reap or
	// race the pipe fds. Invoked while holding invokeMu is safe: killMu is
	// never acquired in the reverse order.
	killMu sync.Mutex
	exited bool  // process has been killed/reaped (by close or a failure path)
	crash  error // non-nil when the process died abnormally (EOF mid-invoke / protocol error)
}

// Open spawns the plugin executable at artifactPath and returns the invoke
// transport and a closer that kills the process. entrySymbol is a no-op in
// subprocess mode — there is no C symbol to bind; the child executable is the
// whole program.
//
// stderr of the child is drained into the host log sink as error-level
// entries: per the design card stderr is reserved for Go runtime panics and
// uncaught output and is never mixed with data frames; the pipe also lets
// the spawn suppress console windows (CREATE_NO_WINDOW) without losing
// diagnostics.
//
// The returned invoke speaks the T1-spike wire contract verbatim:
//
//   - invoke-req (0x01) payload is the JSON PluginAbiInvokeEnvelope
//     (Callable/Payload/RequestId/SessionId/CallSeq) — the FFI path's extra
//     4-byte inner length prefix (EncodeInvokeEnvelope) is an ABI detail that
//     does not apply here, the transport frame already carries the length.
//   - invoke-resp (0x02) payload is the raw response payload (a plugin
//     handler error arrives as {"error": ...} inside it, exactly like the
//     FFI path) and is returned verbatim.
//   - reverse-req (0x03) is answered in-line by the read loop: dispatch
//     through the ReverseHandler, write back 0x04, keep reading for 0x02.
//
// abi is the manifest ABI; the subprocess transport serves it directly and
// does not use the ABI beyond what the transport selector already decided.
func (o *processOpener) Open(abi gen.PluginAbi, artifactPath, entrySymbol, pluginID string, allowed map[string]struct{}, onLoadConfig []byte) (func(ctx context.Context, callable string, request []byte) ([]byte, error), pluginhost.InvokeStreamFunc, func() error, string, error) {
	if strings.TrimSpace(artifactPath) == "" {
		return nil, nil, nil, "", fmt.Errorf("pluginhost: process opener: artifact path is required")
	}
	o.killMu.Lock()
	alreadySpawned := o.cmd != nil && !o.exited
	o.killMu.Unlock()
	if alreadySpawned {
		return nil, nil, nil, "", fmt.Errorf("pluginhost: process opener: plugin %q already spawned", pluginID)
	}

	// host -> plugin pipe: host writes hostStdinW, child reads pluginStdinR.
	pluginStdinR, hostStdinW, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("pluginhost: create stdin pipe: %w", err)
	}
	// plugin -> host pipe: child writes pluginStdoutW, host reads hostStdoutR.
	hostStdoutR, pluginStdoutW, err := os.Pipe()
	if err != nil {
		_ = pluginStdinR.Close()
		_ = hostStdinW.Close()
		return nil, nil, nil, "", fmt.Errorf("pluginhost: create stdout pipe: %w", err)
	}
	// stderr pipe: stderr is reserved for Go runtime panics and uncaught
	// output (never mixed with data frames), so the host drains it into the
	// log sink instead of inheriting the host's stderr handle. A pipe keeps
	// the child working when the host has no console (release builds use
	// -H windowsgui) and prevents a chatty child from blocking on a full
	// stderr pipe nobody reads.
	hostStderrR, pluginStderrW, err := os.Pipe()
	if err != nil {
		_ = pluginStdinR.Close()
		_ = hostStdinW.Close()
		_ = hostStdoutR.Close()
		_ = pluginStdoutW.Close()
		return nil, nil, nil, "", fmt.Errorf("pluginhost: create stderr pipe: %w", err)
	}
	// util.Command applies HideWindow + CREATE_NO_WINDOW on Windows: without
	// it a GUI-subsystem host pops a cmd console window per plugin process.
	cmd := util.Command(artifactPath)
	cmd.Env = o.env
	cmd.Stdin = pluginStdinR
	cmd.Stdout = pluginStdoutW
	cmd.Stderr = pluginStderrW
	if err := cmd.Start(); err != nil {
		_ = pluginStdinR.Close()
		_ = hostStdinW.Close()
		_ = hostStdoutR.Close()
		_ = pluginStdoutW.Close()
		_ = hostStderrR.Close()
		_ = pluginStderrW.Close()
		return nil, nil, nil, "", fmt.Errorf("pluginhost: spawn plugin process: %w", err)
	}
	// Parent drops the child-owned ends; the host now holds hostStdinW
	// (write), hostStdoutR (read) and hostStderrR (read, pumped below).
	_ = pluginStdinR.Close()
	_ = pluginStdoutW.Close()
	_ = pluginStderrW.Close()

	// Default reverse handler = the capability-gated HostBridge built from
	// the same dispatch wires loaderOpener uses (per-plugin isolation:
	// EffectiveCapabilities are intersected before Open is called). An
	// injected reverse overrides it (T6/T7 seam).
	handler := o.reverse
	if handler == nil {
		bridge := NewHostBridge(allowed, o.dispatch, pluginID, o.storeDispatch)
		if o.dispatchStream != nil {
			bridge.SetStreamDispatch(o.dispatchStream)
		}
		handler = bridge
	}

	o.killMu.Lock()
	o.pluginID = pluginID
	o.reverse = handler
	o.cmd = cmd
	o.stdin = hostStdinW
	o.stdout = bufio.NewReader(hostStdoutR)
	o.stderr = hostStderrR
	// Claim this spawn's process generation before any report: every state
	// transition this process later reports (running/exit/close) carries it,
	// and the state machine drops reports from superseded generations
	// (hot reload: the old process's late exit must not overwrite the
	// replacement's verdict).
	if o.logger != nil {
		o.gen = o.logger.NextProcessGeneration(pluginID)
	}
	// A fresh process starts a fresh lifecycle: clear the previous spawn's
	// exit state so this opener instance can respawn (kill+respawn reuse of
	// a processOpener, or a hot reload that reuses the opener object).
	// Without the reset every invoke on the new process — including the
	// OnLoad hook below — would be rejected with the previous lifecycle's
	// "process exited" condition. (transportOpener.Open now hands each load
	// a fresh processOpener, so respawn-via-same-instance is rare, but the
	// reset keeps the opener self-consistent either way.)
	o.exited = false
	o.crash = nil
	o.httpAddr = ""
	o.onLoadConfig = onLoadConfig
	o.killMu.Unlock()
	if o.logger != nil {
		o.logger.LogPluginEntry(pluginID, 0, 0, "pluginhost: subprocess spawned")
	}
	// Drain stderr in the background for the child's whole lifetime (see the
	// stderr pipe comment above); exits on child exit or when killLocked
	// closes the read end.
	go o.pumpStderr(hostStderrR, pluginID)

	// The frame session owns the stdout read loop from here on: a permanent
	// demux that delivers invoke responses to the single in-flight invoke
	// and answers reverse bridge calls from bounded workers, at any time.
	// Its fatal conditions (EOF, plugin 0x06, protocol violation) funnel
	// into recordExit so the existing crash/respawn machinery is unchanged.
	o.session = newFrameSession(frameSessionConfig{
		PluginID:    pluginID,
		Read:        o.stdout,
		Write:       o.stdin,
		Reverse:     handler,
		SettleGrace: o.settleGrace,
		OnLog:       o.forwardLog,
		OnReverseError: func(svcCallID string, err error) {
			if o.logger != nil {
				o.logger.LogPluginEntry(pluginID, 0, 2, fmt.Sprintf("reverse dispatch %s failed: %v", svcCallID, err))
			}
		},
		OnFatal: func(err error) {
			// Host-initiated unload is not a crash; close() already marks
			// the process exited via its own path.
			if errors.Is(err, errSessionClosedByHost) {
				return
			}
			o.recordExit(err)
		},
	})
	o.session.start()

	// Run the plugin's OnLoad lifecycle hook as the first invoke-req after
	// spawn — the subprocess mirror of loaderOpener.Open's callOnLoad for the
	// FFI path. The SDK's process transport intercepts the reserved "onLoad"
	// callable (ProcessOnLoadCallable) and runs HandleOnLoad, so the plugin's
	// OnLoad callables are registered before Open reports success. The invoke
	// payload is the host-pushed per-instance config (onLoadConfig): the SDK
	// applies it (HTTP listener address + session cookie secret) and reports
	// the bound listener address back in the response as {"httpAddr": ...}.
	// A plugin whose OnLoad fails reports {"error": ...} in-band in the
	// invoke-resp and is treated as a load failure exactly like the FFI
	// path's non-zero status: the process is killed and Open fails so the
	// loader surfaces the error and the host can schedule a respawn.
	httpAddr, err := o.runOnLoad()
	if err != nil {
		o.recordExit(fmt.Errorf("plugin OnLoad failed: %w", err))
		return nil, nil, nil, "", fmt.Errorf("pluginhost: onLoad %s: %w", pluginID, err)
	}
	o.killMu.Lock()
	o.httpAddr = httpAddr
	o.killMu.Unlock()
	// Report "running" only after OnLoad bound (or declined) the listener,
	// carrying the fresh addr: appmanager re-attaches the gateway proxy from
	// it, which is what cold-start restore needs (reload/plugin_load attach
	// through their own responses).
	if o.logger != nil {
		o.logger.ReportProcessState(pluginID, "running", "", httpAddr, o.gen)
	}

	invoke := func(ctx context.Context, callable string, request []byte) ([]byte, error) {
		return o.invoke(ctx, callable, request)
	}
	// The stream closure shares o (same session/pipes) with the unary invoke
	// above by construction — both come from this one Open call.
	invokeStream := func(ctx context.Context, callable string, request []byte, onChunk func([]byte) error) ([]byte, error) {
		return o.invokeStream(ctx, callable, request, onChunk)
	}
	closer := func() error { return o.close() }
	return invoke, invokeStream, closer, httpAddr, nil
}

// subprocessOnLoadCallable is the reserved callable name the host addresses
// for the plugin's OnLoad lifecycle hook over the subprocess transport,
// sent as the first invoke-req after spawn. It mirrors the SDK's
// ProcessOnLoadCallable (sporemind-plugin-sdk/process_transport.go); the host
// deliberately does not import the SDK module (separate module), so the name
// is mirrored here like the host-side IsolationSubprocess constant. Keep the
// two in sync.
const subprocessOnLoadCallable = "onLoad"

// runOnLoad drives the plugin's OnLoad hook over the transport, bounded by
// invokeTimeout so a plugin that hangs in OnLoad is killed and reported
// instead of blocking Open forever. (The FFI path cannot bound callOnLoad — a
// C call cannot be preempted — but the subprocess path can and must: an
// unresponsive child would otherwise wedge the loader.)
//
// The returned address is the bound host:port of the plugin's HTTP listener,
// parsed from a successful OnLoad response ({"httpAddr": "..."}); "" when the
// plugin started no listener (no config pushed, or the SDK is pre-HTTP).
func (o *processOpener) runOnLoad() (string, error) {
	onLoadCtx, cancel := context.WithTimeout(context.Background(), invokeTimeout)
	defer cancel()
	resp, err := o.invoke(onLoadCtx, subprocessOnLoadCallable, o.onLoadConfig)
	if err != nil {
		return "", err
	}
	// A successful OnLoad returns an empty response ({}), or
	// {"httpAddr": "<bound>"} when the SDK started an HTTP listener from the
	// pushed config; a failed OnLoad is reported in-band as {"error": ...} —
	// the same handler-error envelope as any other callable, so it is
	// smuggled past the transport layer and must be checked here.
	var failed struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(resp, &failed) == nil && strings.TrimSpace(failed.Error) != "" {
		return "", fmt.Errorf("plugin reported OnLoad failure: %s", truncateForLog([]byte(failed.Error)))
	}
	return parseOnLoadHTTPAddr(resp), nil
}

// parseOnLoadHTTPAddr extracts the bound listener address from a successful
// OnLoad response body. Anything that is not a JSON object with a non-empty
// httpAddr string (empty response, old SDK, malformed payload) yields "" —
// the absence of an address never fails a load.
func parseOnLoadHTTPAddr(resp []byte) string {
	var body struct {
		HTTPAddr string `json:"httpAddr"`
	}
	if json.Unmarshal(resp, &body) != nil {
		return ""
	}
	return strings.TrimSpace(body.HTTPAddr)
}

// HTTPAddr returns the bound host:port of the plugin's own HTTP listener
// reported by its OnLoad response ("" when no listener runs). Safe to call
// concurrently with close/invoke.
func (o *processOpener) HTTPAddr() string {
	o.killMu.Lock()
	defer o.killMu.Unlock()
	return o.httpAddr
}

// invoke sends one invoke-req and waits for its invoke-resp through the
// frame session. Reverse-reqs from the plugin are answered concurrently by
// the session's bounded workers (process_session.go), whether they interleave
// with this invoke or arrive between invokes.
//
// Timeout: when ctx expires the plugin process is killed and reaped so no
// process leaks ("超时即 kill"); the session settles via pipe EOF and any
// other waiter fails cleanly.
//
// Crash detection: a session-fatal condition (EOF, plugin 0x06, protocol
// violation) has already funneled into recordExit via the session's OnFatal,
// so this call's error carries the recorded cause.
func (o *processOpener) invoke(ctx context.Context, callable string, request []byte) ([]byte, error) {
	o.killMu.Lock()
	if o.exited {
		cause := o.crash
		id := o.pluginID
		o.killMu.Unlock()
		if cause != nil {
			return nil, fmt.Errorf("pluginhost: plugin %s: process exited: %w", id, cause)
		}
		return nil, fmt.Errorf("pluginhost: plugin %s: process is not running (unloaded)", id)
	}
	o.killMu.Unlock()

	meta, _ := pluginhost.InvokeMetaFrom(ctx)
	env := gen.PluginAbiInvokeEnvelope{
		Callable:  callable,
		Payload:   request,
		RequestID: meta.RequestID,
		SessionID: meta.SessionID,
		CallSeq:   meta.CallSeq,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: encode invoke envelope: %w", err)
	}
	resp, err := o.session.call(ctx, body)
	if err != nil {
		if errors.Is(err, errInvokeQueued) || errors.Is(err, errInvokeOverran) {
			// Single-flight admission failure (errInvokeQueued) or an admitted
			// invoke that overran its budget (errInvokeOverran): neither is a
			// hung plugin by itself — fail the invoke without tearing it down.
			// An overrun whose settle grace expired DID kill the process (the
			// desync settle fires onFatal → recordExit before this returns);
			// surface that so callers see the death, not just the timeout.
			if errors.Is(err, errInvokeOverran) {
				o.killMu.Lock()
				crashed := o.crash != nil
				o.killMu.Unlock()
				if crashed {
					return nil, fmt.Errorf("pluginhost: invoke %s: %w (plugin killed)", callable, err)
				}
			}
			return nil, fmt.Errorf("pluginhost: invoke %s: %w", callable, err)
		}
		if ctx.Err() != nil {
			o.recordExit(fmt.Errorf("killed on invoke timeout: %w", ctx.Err()))
			return nil, fmt.Errorf("pluginhost: invoke %s: %w (plugin killed)", callable, ctx.Err())
		}
		o.killMu.Lock()
		crashed := o.crash != nil
		o.killMu.Unlock()
		if crashed {
			return nil, fmt.Errorf("pluginhost: invoke %s: plugin process exited mid-invoke: %w", callable, err)
		}
		return nil, fmt.Errorf("pluginhost: invoke %s: %w", callable, err)
	}
	return resp, nil
}

// invokeStream is the streaming variant of invoke: the plugin may emit any
// number of 0x08 forward-chunk frames before its terminal 0x02; onChunk fires
// per chunk payload in wire order and the returned bytes are the terminal
// payload. The exit/crash/timeout policy is identical to invoke. A streaming
// callable whose transport lacks the forward-chunk wire (FFI/c-shared)
// degrades to zero chunks plus the terminal.
func (o *processOpener) invokeStream(ctx context.Context, callable string, request []byte, onChunk func([]byte) error) ([]byte, error) {
	o.killMu.Lock()
	if o.exited {
		cause := o.crash
		id := o.pluginID
		o.killMu.Unlock()
		if cause != nil {
			return nil, fmt.Errorf("pluginhost: plugin %s: process exited: %w", id, cause)
		}
		return nil, fmt.Errorf("pluginhost: plugin %s: process is not running (unloaded)", id)
	}
	o.killMu.Unlock()

	meta, _ := pluginhost.InvokeMetaFrom(ctx)
	env := gen.PluginAbiInvokeEnvelope{
		Callable:  callable,
		Payload:   request,
		RequestID: meta.RequestID,
		SessionID: meta.SessionID,
		CallSeq:   meta.CallSeq,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: encode invoke envelope: %w", err)
	}
	resp, err := o.session.callStream(ctx, body, onChunk)
	if err != nil {
		if errors.Is(err, errInvokeQueued) || errors.Is(err, errInvokeOverran) {
			// Same policy as the unary path: queue starvation and late-settled
			// overruns are not a hung plugin — fail without killing.
			return nil, fmt.Errorf("pluginhost: invoke-stream %s: %w", callable, err)
		}
		if ctx.Err() != nil {
			o.recordExit(fmt.Errorf("killed on invoke-stream timeout: %w", ctx.Err()))
			return nil, fmt.Errorf("pluginhost: invoke-stream %s: %w (plugin killed)", callable, ctx.Err())
		}
		o.killMu.Lock()
		crashed := o.crash != nil
		o.killMu.Unlock()
		if crashed {
			return nil, fmt.Errorf("pluginhost: invoke-stream %s: plugin process exited mid-invoke: %w", callable, err)
		}
		return nil, fmt.Errorf("pluginhost: invoke-stream %s: %w", callable, err)
	}
	return resp, nil
}

// reverseBudgetContext bounds ONE nested reverse bridge call by the remaining
// outer invoke budget (ctx deadline propagation) — task-card T5 choice for
// the nested-timeout gap, recorded in the card. Rationale: host-side
// invokeTimeout (pluginhost.go:575) and the reverse-call timeout
// (hostBridgeInvokeTimeout, host_bridge.go:23) were peers at 30s each, so an
// invoke with a slow reverse call could run 2x the outer budget, and the
// outer kill then truncated the still-in-flight reverse call. Deriving the
// reverse budget from the outer deadline bounds the TOTAL wall time of an
// invoke (including all nested reverse calls) by the outer timeout and lets
// the reverse fail on its own derived budget instead of being cut off by a
// process kill. The alternative (an independently shortened reverse timeout)
// was rejected: it still permits invoke+reverse to exceed the outer budget in
// aggregate.
//
// When the outer ctx carries no deadline (HTTP-data-path origin: the plugin
// handler serves a gateway request, so no framed invoke — and hence no
// invoke ctx — exists), the cap comes from the route's Budget when the
// callID registered one (LLM generation runs minutes; the generic 30s cap
// kills it mid-stream), falling back to the standalone hostBridgeInvokeTimeout.
//
// reverseHeadroom is subtracted from the remaining outer budget when a
// deadline exists: after the derived reverse budget fires, the plugin needs a
// small window to abandon its handler and flush its 0x02 error response
// before the outer kill would truncate it. Without the headroom the derived
// budget equals the outer deadline and the kill can still race the unwind.
const reverseHeadroom = 250 * time.Millisecond

func reverseBudgetContext(ctx context.Context, svcCallID string) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(ctx, deadline.Add(-reverseHeadroom))
	}
	if budget := appbinding.StreamRouteBudget(svcCallID); budget > 0 {
		return context.WithTimeout(ctx, budget)
	}
	if budget := appbinding.HostCallBudget(svcCallID); budget > 0 {
		return context.WithTimeout(ctx, budget)
	}
	return context.WithTimeout(ctx, hostBridgeInvokeTimeout)
}

// dispatchWithinBudget runs a reverse dispatch on a goroutine and bounds the
// wait by dctx. The underlying HostBridge.Dispatch / NewServiceDispatch do
// not accept a caller context (they time out from context.Background with
// hostBridgeInvokeTimeout), so the derived budget is enforced here by
// selection. On budget expiry the goroutine finishes in the background and
// its result is discarded via the buffered channel — no goroutine leak — and
// the plugin is NOT killed mid-call: the caller answers the reverse-req with
// a __host_error__ envelope so the plugin unwinds cleanly.
//
// onChunk != nil selects the streaming path: the handler must implement
// StreamReverseHandler for chunks to flow; otherwise the call degrades to
// unary (onChunk never fires — the new-plugin/old-handler mirror of the
// new-plugin/old-host degradation). onChunk is invoked synchronously from
// the dispatch goroutine, in stream order; an error return aborts the
// dispatch with that error.
func dispatchWithinBudget(dctx context.Context, handler ReverseHandler, rctx DispatchContext, callID string, req []byte, onChunk func([]byte) error) ([]byte, error) {
	type dispatchResult struct {
		resp []byte
		err  error
	}
	ch := make(chan dispatchResult, 1)
	go func() {
		// Panic net: a panicking capability handler must reach the plugin as a
		// __host_error__ result, not escape this goroutine and kill the host
		// process. Same recovery contract as the FFI dispatchPayload path.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("pluginhost: reverse dispatch panic",
					"callID", callID, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
				ch <- dispatchResult{nil, fmt.Errorf("pluginhost: reverse call %q panicked: %v", callID, r)}
			}
		}()
		var resp []byte
		var err error
		if onChunk != nil {
			if sh, ok := handler.(StreamReverseHandler); ok {
				resp, err = sh.DispatchContextStream(rctx, callID, req, onChunk)
			} else {
				// No streaming support in the injected handler: unary
				// degradation; onChunk never fires.
				resp, err = handler.Dispatch(callID, req)
			}
		} else if bridge, ok := handler.(*HostBridge); ok {
			// The default reverse handler is a *HostBridge: prefer its
			// context-aware entry so per-invoke caller identity reaches
			// llm.* dispatches. Test-injected handlers only implement
			// Dispatch.
			resp, err = bridge.DispatchContext(rctx, callID, req)
		} else {
			resp, err = handler.Dispatch(callID, req)
		}
		ch <- dispatchResult{resp, err}
	}()
	select {
	case r := <-ch:
		return r.resp, r.err
	case <-dctx.Done():
		return nil, fmt.Errorf("pluginhost: reverse call %q: %w", callID, dctx.Err())
	}
}

// pumpStderr drains the plugin's stderr pipe into the host log sink, one
// error-level entry per line (512-byte truncation rule). With no logger
// configured the pipe is still drained so the child never blocks writing to
// a full stderr pipe. The pump returns when the child exits (EOF) or when
// killLocked closes the read end.
func (o *processOpener) pumpStderr(r io.Reader, pluginID string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for sc.Scan() {
		if o.logger == nil {
			continue
		}
		o.logger.LogPluginEntry(pluginID, 0, 3, "plugin stderr: "+truncateForLog(sc.Bytes()))
	}
}

// forwardLog forwards a 0x05 log frame to the host log sink. The payload is
// either a JSON object {"Level": n, "Message": "..."} (SDK ctx.Log over the
// IPC path) or a raw message string (T1 spike convention). Long messages are
// truncated to the project's 512-byte log-field ceiling.
func (o *processOpener) forwardLog(payload []byte) {
	if o.logger == nil {
		return
	}
	var entry struct {
		Level   int    `json:"Level"`
		Message string `json:"Message"`
	}
	msg := string(payload)
	level := 1 // LogLevelInfo default for raw frames
	if json.Unmarshal(payload, &entry) == nil && entry.Message != "" {
		msg = entry.Message
		level = entry.Level
	}
	o.logger.LogPluginEntry(o.pluginID, 0, level, truncateForLog([]byte(msg)))
}

// stdinWriter returns the current host->plugin pipe writer, captured under
// killMu so a concurrent close (which nils the field) never races the read.
// A nil result means the process is gone or being torn down.
func (o *processOpener) stdinWriter() io.Writer {
	o.killMu.Lock()
	defer o.killMu.Unlock()
	return o.stdin
}

// recordExit marks the process as exited (first caller wins) and reaps it via
// kill. cause is recorded as the crash explanation when non-nil; close() uses
// the same machinery with a nil cause to mark a normal unload.
func (o *processOpener) recordExit(cause error) {
	o.killMu.Lock()
	defer o.killMu.Unlock()
	if o.exited {
		return
	}
	o.exited = true
	if cause != nil && o.crash == nil {
		o.crash = cause
	}
	o.killLocked()
	if o.logger != nil {
		msg := "pluginhost: subprocess terminated"
		if cause != nil {
			msg = "pluginhost: subprocess terminated abnormally: " + truncateForLog([]byte(cause.Error()))
		}
		o.logger.LogPluginEntry(o.pluginID, 0, 3, msg)
		// Explicit state report: abnormal exits (session EOF, protocol
		// violations, invoke-timeout kills) mark the plugin crashed with
		// the cause; a nil cause is a plain stop.
		if cause != nil {
			o.logger.ReportProcessState(o.pluginID, "crashed", cause.Error(), "", o.gen)
		} else {
			o.logger.ReportProcessState(o.pluginID, "stopped", "", "", o.gen)
		}
	}
}

// close is the ArtifactLoader closer: kill the plugin process. The Go runtime
// embedded in the child tears down safely with process exit — no FreeLibrary
// problem — and kill+Wait reaps it so no zombie or leaked handle remains.
// Idempotent and safe to call concurrently with an in-flight invoke (the
// invoke's blocked read unblocks via pipe EOF).
func (o *processOpener) close() error {
	if o.session != nil {
		o.session.close()
	}
	o.killMu.Lock()
	if o.exited {
		o.killMu.Unlock()
		return nil
	}
	o.exited = true
	o.killLocked()
	o.killMu.Unlock()
	if o.logger != nil {
		o.logger.LogPluginEntry(o.pluginID, 0, 1, "pluginhost: subprocess terminated by unload")
		o.logger.ReportProcessState(o.pluginID, "stopped", "", "", o.gen)
	}
	return nil
}

// killLocked terminates the process and reaps it. Caller must hold killMu and
// must have marked exited first so recordExit/close stay idempotent. On
// Windows this is TerminateProcess; on Unix SIGKILL.
func (o *processOpener) killLocked() {
	if o.stdin != nil {
		_ = o.stdin.Close()
		o.stdin = nil
	}
	if o.stderr != nil {
		_ = o.stderr.Close()
		o.stderr = nil
	}
	if o.cmd != nil && o.cmd.Process != nil {
		_ = o.cmd.Process.Kill()
		_ = o.cmd.Wait()
		o.cmd = nil
	}
}

// truncateForLog caps a log field at 512 bytes per the project's truncation
// rule (long upstream bodies must be pre-truncated by the sender).
func truncateForLog(s []byte) string {
	// stdin/stderr bytes are arbitrary: coerce to valid UTF-8 before the
	// result reaches the plugin log ring or the host log stream, both of
	// which are encoded by a strict-UTF-8 JSON codec downstream.
	s = []byte(strings.ToValidUTF8(string(s), "�"))
	if len(s) > 512 {
		cut := 512
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return string(s[:cut]) + "...(truncated)"
	}
	return string(s)
}
