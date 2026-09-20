package sdk

import (
	"context"
	"encoding/json"
)

// Host is the client used by a plugin to call host capabilities.
//
// Session semantics: when the host invokes a plugin (PluginInvokeReq), the
// SessionId field is audit/correlation metadata only and must not be used as an
// authorization basis. Authorization is established by the host before the
// plugin is invoked; a plugin must not re-derive trust from SessionId.
//
// Invoke/InvokeStream are the ONLY host-call primitives. Typed callers
// (CallXxx / StreamXxx) are generated per-app by dev_generate from the
// callIDs the app declared in .appdef permissions — there are no hand-written
// capability clients: an undeclared callID cannot be called at all, and every
// declared callID gets a compile-time-typed caller in hostproto.gen.go.
type Host interface {
	// Invoke calls a host callable by callID.
	// Example: Invoke("llm.complete", map[string]any{"prompt": "hello"})
	Invoke(callID string, payload any) ([]byte, error)

	// InvokeStream calls a host callable that supports chunked delivery.
	// Intermediate chunks are passed to onChunk in arrival order as raw wire
	// bytes (the generic host envelope {"kind", "data"}); the return value is
	// the terminal aggregated response. Hosts without streaming support
	// degrade to zero intermediate chunks (the FFI c-shared transport
	// delivers the full response as a single chunk before returning). An
	// error returned by onChunk aborts the stream.
	InvokeStream(callID string, payload any, onChunk func([]byte) error) ([]byte, error)
}

// CanceledHost is the optional cancellation extension of Host, implemented by
// transports with an abort channel to the host (the subprocess IPC transport
// today). Context expiry mid-call sends a 0x09 reverse-cancel frame so the
// HOST aborts its upstream dispatch (the running LLM stream stops billing)
// instead of running to completion on a stream nobody consumes. Hosts that
// do not implement it (FFI c-shared: synchronous C ABI, no channel) simply
// let the caller stop waiting — the legacy abandoned-stream behavior.
type CanceledHost interface {
	Host
	// InvokeStreamCtx is InvokeStream with a cancellation root. ctx expiry
	// aborts the stream from the plugin side: no further chunks are
	// delivered, the error returned wraps ctx.Err(), and the host is asked
	// (best-effort) to abort its dispatch. A nil ctx means background.
	InvokeStreamCtx(ctx context.Context, callID string, payload any, onChunk func([]byte) error) ([]byte, error)
}

// LLMChunk is one intermediate streaming chunk delivered to an onChunk
// callback, decoded from the generic host wire envelope
// {"kind": "<kind>", "data": {...}}:
//
//	{"kind": "text_delta", "data": {"text": "..."}}      incremental output text
//	{"kind": "reasoning_delta", "data": {"text": "..."}} incremental reasoning text
//	{"kind": "usage", "data": {...usage object...}}      final usage snapshot
//
// The envelope's kind vocabulary is the backing LLM service's, verbatim; the
// data payload shape is per-kind. Other kinds may flow through on future
// host versions — unknown kinds arrive with Data set and Text/Usage empty,
// and consumers must ignore kinds they do not understand.
//
// This is a shared wire type (consumed by the generated StreamLLM* callers
// via ForwardLLMChunks), not part of a capability client: the SDK has no
// hand-written LLM surface.
type LLMChunk struct {
	Kind string `json:"kind"`
	// Text carries the delta for text_delta / reasoning_delta kinds.
	Text string `json:"text,omitempty"`
	// Usage carries the usage object for the usage kind.
	Usage json.RawMessage `json:"usage,omitempty"`
	// Data is the raw envelope payload for kinds outside the known set —
	// forward compatibility so new host kinds reach consumers verbatim.
	Data json.RawMessage `json:"data,omitempty"`
}
