package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/appbinding"
)

// HostSetHostBridgeSymbol is the C symbol the host calls to pass the host
// bridge function pointer into the plugin. The plugin SDK exports it.
const HostSetHostBridgeSymbol = "PluginSetHostBridge"

// hostBridgeInvokeTimeout caps a single host-bridge reverse call (e.g. an
// LLM completion invoked from inside a plugin callable).
const hostBridgeInvokeTimeout = 30 * time.Second

// HostDispatchFunc routes an authorized host bridge call to its backing
// service. It receives the callID and the raw JSON request bytes from the
// plugin, and returns the raw JSON response bytes.
type HostDispatchFunc func(callID string, req []byte) ([]byte, error)

// HostDispatchStreamFunc is the streaming variant of HostDispatchFunc: the
// backing service may deliver intermediate chunks by invoking onChunk
// (synchronously, in stream order, from the dispatch goroutine) before the
// terminal response returns. An onChunk error aborts the dispatch. Only
// streaming-capable callIDs (llm.*) take this path; everything else is
// routed through the unary HostDispatchFunc.
type HostDispatchStreamFunc func(dctx DispatchContext, callID string, req []byte, onChunk func([]byte) error) ([]byte, error)

// HostBridge is the host-side security gate for native plugin reverse calls.
// It is the Go implementation behind the function pointer passed to the
// plugin via PluginSetHostBridge (FFI/c-shared transport, Callback) and
// behind the IPC process transport's reverse-req frames (ServeReverse).
// Every reverse call (ctx.Host().Invoke(...)) flows through the bridge,
// which enforces that the callID's required capability is in the granted
// EffectiveCapabilities set before dispatching.
//
// A separate HostBridge instance is created per plugin load, each carrying
// that plugin's granted capabilities and its own purego callback. This
// guarantees per-plugin capability isolation: a plugin granted only fs.read
// cannot reach llm.invoke even when another plugin was granted it.
type HostBridge struct {
	allowed       map[string]struct{}
	dispatch      HostDispatchFunc
	pluginID      string
	storeDispatch HostDispatchFunc
	// dispatchStream routes streaming-capable callIDs (llm.*) with chunked
	// delivery. nil degrades DispatchContextStream to plain DispatchContext
	// (zero intermediate chunks, terminal value intact).
	dispatchStream HostDispatchStreamFunc
	// allowedBundleCalls holds the exact plugin.* callIDs the app declared
	// in its permissions list; nil means none were declared (all plugin.*
	// calls denied). DerivedCapabilities keeps plugin.* entries verbatim in
	// the manifest permissions, so the standard allowed set (built from the
	// manifest by manifestCapabilitySet) carries them — this field is a
	// copy taken at construction for the exact-match gate.
	allowedBundleCalls map[string]bool
	// bundleDispatch routes plugin.* callIDs to the target plugin's process
	// via the appmanager/pluginhost actor. nil denies all bundle calls even
	// when the callID is in allowedBundleCalls (the host actor did not wire
	// a bundle dispatch — e.g. a test bridge).
	bundleDispatch HostDispatchFunc
}

// DispatchContext carries per-invoke metadata from the outer plugin invoke
// into bridge-routed calls (llm.* / state.*). An empty value is valid —
// dispatch functions tolerate zero fields.
type DispatchContext struct {
	AgentID     string
	WorkspaceID string
	// DeadlineAt is the outer invoke deadline as Unix milliseconds. It is
	// forwarded into llm.* reverse payloads as __DeadlineAt so the
	// synchronous aggregator call can inherit the outer invoke budget instead
	// of falling back to the fixed 30s host-bridge cap. Zero means no
	// deadline (direct transport / tests).
	DeadlineAt int64
	// Parent is the reverse dispatch's cancellation root (process transport
	// only). When set, the streaming forwarder derives its invoke context
	// from it instead of a bare background context, so a plugin-issued
	// reverse-cancel (0x09) unwinds the upstream LLM stream and releases the
	// reverse slot — the abandoned-stream cost-leak fix. Nil (FFI transport,
	// tests) keeps the pre-existing __DeadlineAt-only budget semantics.
	Parent context.Context
}

// NewHostBridge creates a bridge that authorizes calls against the given
// allowed capability set and dispatches authorized calls through dispatch.
// The allowed set is copied so later mutations to the source map do not
// affect authorization decisions. pluginID is the calling plugin's identity,
// injected into state.* payloads before storeDispatch routes them to the
// pluginhost actor's per-app storage callables. storeDispatch may be nil when
// the host bridge is created for testing or when app.state is not granted.
func NewHostBridge(allowed map[string]struct{}, dispatch HostDispatchFunc, pluginID string, storeDispatch HostDispatchFunc) *HostBridge {
	caps := make(map[string]struct{}, len(allowed))
	bundle := make(map[string]bool)
	for k := range allowed {
		// plugin.* entries are per-callID bundle gates (kept verbatim by
		// DerivedCapabilities); everything else is a capability string.
		if strings.HasPrefix(k, "plugin.") {
			bundle[k] = true
			continue
		}
		caps[k] = struct{}{}
	}
	return &HostBridge{allowed: caps, dispatch: dispatch, pluginID: pluginID, storeDispatch: storeDispatch, allowedBundleCalls: bundle}
}

// callIDToCapability maps a host bridge callID to the host capability it
// requires. The mapping lives in pkg/appbinding (HostCallCapability) as the
// single source of truth shared with the appmanager protocol query/extraction;
// this wrapper keeps the call site readable.
func callIDToCapability(callID string) string {
	return appbinding.HostCallCapability(callID)
}

// mappedHostCallID returns the capability required by a known SDK host
// callID and reports whether the callID maps to any host service at all.
// It lets Dispatch distinguish "unknown callID" from "known but not
// granted" instead of collapsing both into the same denial message.
func mappedHostCallID(callID string) (capability string, mapped bool) {
	capability = callIDToCapability(callID)
	return capability, capability != ""
}

// Allows reports whether the callID's required capability is in the granted
// set. Unknown callIDs (no capability mapping) are always denied. For
// plugin.* callIDs (bundle invoke), the capability-level check is augmented
// by an exact-callID gate: only the specific plugin.* callIDs declared in
// the app's permissions are allowed, preventing a plugin granted
// plugin.X.translate from reaching plugin.Y.other.
func (b *HostBridge) Allows(callID string) bool {
	if strings.HasPrefix(callID, "plugin.") {
		return b.allowedBundleCalls[callID]
	}
	capability, mapped := mappedHostCallID(callID)
	if !mapped {
		return false
	}
	_, ok := b.allowed[capability]
	return ok
}

// Dispatch authorizes and routes callID through the backing dispatch
// function. It is the Go-level entry point shared by both the C callback
// and direct Go callers (e.g. tests). For state.* callIDs, the plugin's
// identity is injected into the JSON payload before routing through
// storeDispatch, so the plugin SDK does not need to pass its own ID.
func (b *HostBridge) Dispatch(callID string, req []byte) ([]byte, error) {
	return b.DispatchContext(DispatchContext{}, callID, req)
}

// DispatchContext is Dispatch with per-invoke caller metadata attached. For
// llm.* callIDs the AgentID/WorkspaceID from the outer invoke are forwarded
// to the aiaggregator dispatch so usage stats and provider assignment can be
// attributed to the originating agent instead of being dropped.
func (b *HostBridge) DispatchContext(dctx DispatchContext, callID string, req []byte) ([]byte, error) {
	if strings.HasPrefix(callID, "plugin.") {
		if !b.Allows(callID) {
			return nil, fmt.Errorf("pluginhost: bundle call not granted for callID %q", callID)
		}
		if b.bundleDispatch == nil {
			return nil, fmt.Errorf("pluginhost: bundle dispatch not configured for %q", callID)
		}
		return b.bundleDispatch(callID, req)
	}
	capability, mapped := mappedHostCallID(callID)
	if !mapped {
		return nil, fmt.Errorf("pluginhost: no host-service mapping for callID %q", callID)
	}
	if !b.Allows(callID) {
		return nil, fmt.Errorf("pluginhost: capability not granted (%q) for callID %q", capability, callID)
	}
	if strings.HasPrefix(callID, "state.") || callID == "app.emit" ||
		callID == "image.generate" || callID == "video.generate" ||
		appbinding.HostCallCapability(callID) == appbinding.CapWikiRead {
		if b.storeDispatch == nil {
			return nil, fmt.Errorf("pluginhost: local dispatch not configured for %q", callID)
		}
		injected, err := injectPluginID(req, b.pluginID)
		if err != nil {
			return nil, fmt.Errorf("pluginhost: inject plugin ID into %s request: %w", callID, err)
		}
		return b.storeDispatch(callID, injected)
	}
	if b.dispatch == nil {
		return nil, fmt.Errorf("pluginhost: host bridge dispatch not configured")
	}
	if appbinding.StreamRouteInjectsCallerContext(callID) && (dctx.AgentID != "" || dctx.WorkspaceID != "") {
		req = injectLLMCallerContext(req, dctx)
	}
	return b.dispatch(callID, req)
}

// SetStreamDispatch installs the streaming dispatch route (llm.* with chunked
// delivery). Called by the process transport wiring when the host actor
// provides a streaming-capable dispatch; without it DispatchContextStream
// degrades to unary.
func (b *HostBridge) SetStreamDispatch(fn HostDispatchStreamFunc) {
	b.dispatchStream = fn
}

// SetBundleDispatch installs the bundle call dispatch route (plugin.* callIDs
// routed to target plugin processes via the appmanager/pluginhost actor).
// Called by the opener wiring when the manifest declares bundle dependencies.
// Without it, plugin.* calls are denied even when the callID is in
// allowedBundleCalls.
func (b *HostBridge) SetBundleDispatch(fn HostDispatchFunc) {
	b.bundleDispatch = fn
}

// DispatchContextStream is the streaming entry point of the bridge and the
// *HostBridge implementation of StreamReverseHandler. Authorization mirrors
// DispatchContext exactly (same capability gate, same caller-context
// injection). Only callIDs with a registered appbinding stream route stream;
// everything else — and any bridge without a streaming dispatch, or a nil
// onChunk — routes through plain DispatchContext so non-streaming reverse
// calls are byte-identical to the unary path.
func (b *HostBridge) DispatchContextStream(dctx DispatchContext, callID string, req []byte, onChunk func([]byte) error) ([]byte, error) {
	if b.dispatchStream == nil || onChunk == nil || !appbinding.IsStreamingCallable(callID) {
		return b.DispatchContext(dctx, callID, req)
	}
	capability, mapped := mappedHostCallID(callID)
	if !mapped {
		return nil, fmt.Errorf("pluginhost: no host-service mapping for callID %q", callID)
	}
	if !b.Allows(callID) {
		return nil, fmt.Errorf("pluginhost: capability not granted (%q) for callID %q", capability, callID)
	}
	if appbinding.StreamRouteInjectsCallerContext(callID) && (dctx.AgentID != "" || dctx.WorkspaceID != "") {
		req = injectLLMCallerContext(req, dctx)
	}
	return b.dispatchStream(dctx, callID, req, onChunk)
}

// Callback returns a C function pointer (via purego.NewCallback) suitable
// for passing to PluginSetHostBridge. It is the FFI/c-shared transport's
// reverse-call entry point only; the IPC process transport uses ServeReverse
// instead. Each HostBridge instance produces a unique callback so
// per-plugin capabilities are isolated.
//
// The C signature is:
//
//	int host_invoke(char* callID, char* req, char* res, int resLen)
//
// It returns the number of response bytes written (excluding the NUL
// terminator), or negative on error.
//
// On Windows, purego.NewCallback delegates to syscall.NewCallback which
// requires all parameters and the result to be uintptr-sized. We use
// uintptr throughout and convert to typed pointers inside the callback;
// the x64 calling convention makes this ABI-compatible with the C
// signature above (pointers and int are register-sized on all supported
// platforms).
func (b *HostBridge) Callback() uintptr {
	return purego.NewCallback(func(callID, req, res uintptr, resLen uintptr) uintptr {
		return uintptr(b.serve(
			(*byte)(unsafe.Pointer(callID)),
			(*byte)(unsafe.Pointer(req)),
			(*byte)(unsafe.Pointer(res)),
			int32(resLen),
		))
	})
}

// serve is invoked when a plugin makes a reverse call through the host
// bridge function pointer (the FFI/c-shared transport). It reads the
// NUL-terminated callID and request, authorizes via the capability gate,
// dispatches, and writes the JSON response into res as a NUL-terminated
// string. The IPC process transport uses ServeReverse instead, which shares
// dispatchPayload so both transports produce byte-identical wire payloads.
func (b *HostBridge) serve(callIDPtr, reqPtr, resPtr *byte, resLen int32) int32 {
	callID := cStringFromBytePtr(callIDPtr)
	req := cStringFromBytePtr(reqPtr)
	return writeBridgeResult(resPtr, resLen, b.dispatchPayload(callID, []byte(req)))
}

// dispatchPayload runs the authorize-then-dispatch pipeline and returns the
// exact bytes that appear on the wire for this reverse call:
//
//   - the raw JSON response, verbatim — SDK-side wire quirks (state.set
//     Value as base64 text, project.write_file content as a JSON string)
//     are produced by the SDK client before the bridge is reached, and the
//     host never re-encodes request or response payloads;
//   - "{}" when the response is empty;
//   - a {"__host_error__": ...} envelope when dispatch fails or the
//     capability is not granted — the SDK bridgeResult converts this into a
//     Go error; a plain {"error": ...} shape is not used because legitimate
//     callable responses may carry a top-level error field.
//
// Both transports share this single envelope codec: the FFI serve path
// writes the result into the C response buffer, and the IPC ServeReverse
// path returns it as the 0x04 reverse-resp frame body, keeping dev/prod
// wire formats aligned.
func (b *HostBridge) dispatchPayload(callID string, req []byte) []byte {
	resp, err := b.dispatchSafely(callID, req)
	if err != nil {
		errResp, _ := json.Marshal(map[string]string{"__host_error__": err.Error()})
		return errResp
	}
	if len(resp) == 0 {
		resp = []byte("{}")
	}
	return resp
}

// dispatchSafely runs the authorize-then-dispatch pipeline with a panic net.
// A panicking host capability handler (e.g. browser.cookies_export walking
// WebView2 COM state) must surface as a __host_error__ envelope for the plugin,
// not as an unrecovered panic on the reverse-call goroutine — which kills the
// whole host process with no trace (GUI-subsystem binaries have no stderr).
func (b *HostBridge) dispatchSafely(callID string, req []byte) (resp []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("pluginhost: host bridge dispatch panic",
				"callID", callID, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			err = fmt.Errorf("pluginhost: host call %q panicked: %v", callID, r)
		}
	}()
	return b.Dispatch(callID, req)
}

// writeBridgeResult writes data as a NUL-terminated string into the res
// buffer. Returns the number of bytes written (excluding NUL), or -1 if the
// buffer is nil or too small.
func writeBridgeResult(resPtr *byte, resLen int32, data []byte) int32 {
	if resPtr == nil || int32(len(data))+1 > resLen {
		return -1
	}
	dst := (*[0x7fffffff]byte)(unsafe.Pointer(resPtr))
	copy(dst[:len(data)], data)
	dst[len(data)] = 0
	return int32(len(data))
}

// cStringFromBytePtr reads a NUL-terminated C string starting at p.
func cStringFromBytePtr(p *byte) string {
	if p == nil {
		return ""
	}
	var n int
	for ptr := unsafe.Pointer(p); *(*byte)(ptr) != 0; ptr = unsafe.Pointer(uintptr(ptr) + 1) {
		n++
	}
	return string(unsafe.Slice(p, n))
}

// --- IPC branch: process transport reverse calls ---

// ReverseReqBody is the wire body of a 0x03 reverse-req frame, mirroring
// the SDK bridgeCall client contract {callID, JSON payload}
// (sporemind-plugin-sdk/bridge.go) so both transports decode reverse calls
// with the same shape. Payload is the raw JSON value the SDK marshaled from
// its typed request (e.g. the base64-encoded state.set Value, the
// JSON-string project.write_file content); the host forwards those bytes
// verbatim into Dispatch and never re-encodes them.
type ReverseReqBody struct {
	CallID  string          `json:"callID"`
	Payload json.RawMessage `json:"payload"`
}

// DecodeReverseReq parses a 0x03 reverse-req frame body into its callID
// and raw JSON payload. Only a malformed body (not valid {callID, payload}
// JSON) is an error: an empty or unknown callID intentionally flows through
// Dispatch, which denies it with a __host_error__ envelope exactly like the
// FFI serve path handles unknown callIDs.
func DecodeReverseReq(frameBody []byte) (callID string, payload json.RawMessage, err error) {
	var body ReverseReqBody
	if err := json.Unmarshal(frameBody, &body); err != nil {
		return "", nil, fmt.Errorf("pluginhost: decode reverse-req: %w", err)
	}
	return body.CallID, body.Payload, nil
}

// ServeReverse is the frame-body-level reverse-call entry for 0x03 reverse-req
// frames (the decoded-level entry is Dispatch). It
// runs the same capability gate + dispatch as the FFI serve path and
// returns the 0x04 reverse-resp frame body: the raw JSON payload, or a
// {"__host_error__": ...} envelope for denied/failed dispatches (with a nil
// error — the SDK bridgeResult converts that envelope into a Go error,
// preserving the empty-field success contract). A non-nil error means the
// frame body itself was not decodable; the transport should treat it as a
// protocol violation.
func (b *HostBridge) ServeReverse(frameBody []byte) ([]byte, error) {
	callID, req, err := DecodeReverseReq(frameBody)
	if err != nil {
		return nil, err
	}
	return b.dispatchPayload(callID, req), nil
}

// Compile-time assertion: HostBridge satisfies the process opener's
// ReverseHandler seam (process_opener.go) via Dispatch, alongside its FFI C callback.
var _ ReverseHandler = (*HostBridge)(nil)

// NewServiceDispatch builds a HostDispatchFunc that routes callIDs to actor
// services by their prefix. The service name is extracted from the callID
// (the segment before the first dot) and resolved via the provided service
// refs. This mirrors the sporebridge dispatch pattern: the callID prefix is
// the service name, the full callID is the callable.
//
// If the service is not in the map (not captured at startup), the dispatch
// returns a structured error. This makes capability-granted but unbacked
// calls fail explicitly rather than silently.
func NewServiceDispatch(services map[string]ref.Ref) HostDispatchFunc {
	refs := make(map[string]ref.Ref, len(services))
	for k, v := range services {
		refs[k] = v
	}
	return func(callID string, req []byte) ([]byte, error) {
		service := serviceFromHostCallID(callID)
		target, ok := refs[service]
		if !ok || target == nil {
			return nil, fmt.Errorf("pluginhost: host service %q not available for %q", service, callID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), hostBridgeInvokeTimeout)
		defer cancel()
		call := target.Invoke(ctx, callID, req)
		if call == nil {
			return nil, fmt.Errorf("pluginhost: invoke %q returned nil", callID)
		}
		defer call.Close()
		raw, err := call.RecvRaw()
		if err != nil {
			return nil, fmt.Errorf("pluginhost: host invoke %q: %w", callID, err)
		}
		return raw, nil
	}
}

// NewTranslatedServiceDispatch wraps a HostDispatchFunc with an SDK callID to
// actual-callable translation table. The returned HostDispatchFunc rewrites the
// callID before invoking the base dispatch. If a callID is not present in the
// table it is forwarded unchanged, preserving behavior for SDK callIDs whose
// prefix already matches a real callable name.
func NewTranslatedServiceDispatch(base HostDispatchFunc, translations map[string]string) HostDispatchFunc {
	return func(callID string, req []byte) ([]byte, error) {
		if target, ok := translations[callID]; ok {
			return base(target, req)
		}
		return base(callID, req)
	}
}

// injectLLMCallerContext merges the per-invoke outer deadline (and workspace
// scope) into a reverse-call payload for streaming-catalog routes that
// request it, so the target's payload adapter can inherit the outer invoke
// budget. The SDK request shapes are arbitrary JSON objects
// (CompleteReq/ChatReq); we add reserved fields "__WorkspaceId" /
// "__DeadlineAt" that the route's AdaptReq consumes and strips before
// sending downstream.
//
// The caller's AgentID is deliberately NOT injected: plugin LLM usage is the
// plugin's own, not the invoking agent's. Carrying the agent id made the
// aggregator pin the plugin to the agent's (agentID, slot) affinity unit —
// including aggregator-ref entries — so a panel-triggered deepseek call
// entered the nested child-aggregator path and hung on its open wait while
// the agent-scoped pool ground through cooldowns (2026-09-05 novel hang).
// Budget (__DeadlineAt) and workspace scoping stay: neither creates agent
// affinity.
func injectLLMCallerContext(req []byte, dctx DispatchContext) []byte {
	if len(req) == 0 {
		return req
	}
	var m map[string]any
	if err := json.Unmarshal(req, &m); err != nil {
		return req
	}
	if m == nil {
		m = map[string]any{}
	}
	if dctx.WorkspaceID != "" {
		m["__WorkspaceId"] = dctx.WorkspaceID
	}
	if dctx.DeadlineAt != 0 {
		m["__DeadlineAt"] = dctx.DeadlineAt
	}
	data, err := json.Marshal(m)
	if err != nil {
		return req
	}
	return data
}

func serviceFromHostCallID(callID string) string {
	if i := strings.Index(callID, "."); i > 0 {
		return callID[:i]
	}
	return callID
}

// injectPluginID parses req as a JSON object, sets the "Plugin" field to
// pluginID, and returns the re-serialized bytes. This ensures the
// pluginhost storage handler receives the calling plugin's identity
// without trusting the SDK payload.
func injectPluginID(req []byte, pluginID string) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(req, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	m["Plugin"] = pluginID
	return json.Marshal(m)
}
