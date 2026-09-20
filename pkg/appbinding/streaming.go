package appbinding

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"
)

// StreamChunkEnvelope is the generic on-wire shape of one streaming chunk
// forwarded by the host bridge to a plugin's Host.InvokeStream onChunk
// callback. Kind is the backing callable's chunk-kind vocabulary (opaque to
// the host — it never interprets this field); Data is the
// callable-specific JSON payload the plugin decodes via its appdef-declared
// chunk schema. The host only routes; it does not own either field's
// semantics.
//
// Replacing the previous llm-specific {Kind,Text,Usage} wire shape lets a
// second streaming callable (shell.exec stdout, file tail, ...) ride the
// same 0x07 reverse-chunk path with its own kind vocabulary and data schema,
// without a host-side switch or a new wire contract.
type StreamChunkEnvelope struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data,omitempty"`
}

// StreamChunkEncoder adapts one backing-actor chunk value (the interface{}
// delivered by gospore Invoke Next) into the generic envelope. One encoder is
// registered per streaming callID. Encoders are callID-specific because they
// know the backing actor's typed chunk shape; the host bridge stays free of
// that domain type import.
type StreamChunkEncoder func(v any) (StreamChunkEnvelope, error)

// StreamAggregator accumulates chunk state across one stream session and
// produces the call's terminal value (the JSON object returned by
// Host.InvokeStream to the plugin). Owning the callID's domain semantics (how
// chunks compose into a final result) keeps aggregation out of the transport
// layer. A route with a nil Aggregate factory means the host passes the
// backing call's Final value through verbatim (callables that emit a complete
// terminal themselves).
type StreamAggregator interface {
	Push(v any) error
	Terminal() any
}

// StreamRoute describes one streaming-capable host callID the bridge can
// forward. The catalog of routes replaces the previous llm.* prefix gate and
// the inline llm consumer in the bridge: a callID streams iff it has a
// registered route, and the route carries every callID-specific concern
// (target service, payload adaptation, chunk encoding, terminal aggregation,
// caller-context injection) so the bridge forwarder is a single generic path.
//
// Routes should be registered through RegisterTypedStreamRoute so the chunk
// and terminal wire types are declared as type parameters (checked at the
// registration site, stamped into ChunkType/TerminalType) instead of being
// re-asserted inside closures. The backing actor's actor.Streaming[T]()
// declaration — not this struct — remains the authority for streaming-ness
// and the backing chunk type; the advisory fields exist so codegen and CI
// can cross-check the two without importing domain types.
type StreamRoute struct {
	// Service is the target actor service domain (resolved via LookupService),
	// e.g. "aiaggregator".
	Service string
	// Callable is the target callable name, e.g. "aiaggregator.dispatch".
	Callable string
	// Encode adapts a backing-actor chunk into the wire envelope. Required.
	Encode StreamChunkEncoder
	// Aggregate builds a fresh per-session aggregator; nil means the host
	// passes the backing call's Final value through verbatim.
	Aggregate func() StreamAggregator
	// AdaptReq rewrites the SDK-facing request payload into the backing
	// callable's request shape; nil means pass the request through unchanged.
	AdaptReq func(req []byte) ([]byte, error)
	// InjectCallerContext is true when the bridge should merge the per-invoke
	// caller identity (__AgentId / __WorkspaceId) and outer deadline
	// (__DeadlineAt) into the request payload before forwarding. LLM routes
	// set this to attribute usage and inherit budget.
	InjectCallerContext bool
	// Budget is the transport-level reverse-call ceiling for this route when
	// no outer invoke deadline is available (HTTP-data-path origin: the plugin
	// handler serves a gateway request, no framed invoke is in flight, so
	// serveReverseAsync has no ctx to derive from). Routes whose backing
	// actor legitimately runs minutes (LLM generation) must set this above the
	// generic hostBridgeInvokeTimeout, otherwise long generations are killed
	// at that fixed cap. Zero means "use the generic cap".
	Budget time.Duration
	// ChunkType is the backing chunk type this route's Encode expects.
	// Advisory: the bridge never reflects on it; it exists so codegen and
	// contract tests can verify the route agrees with the backing actor's
	// actor.Streaming[T]() declaration. nil on untyped legacy routes.
	ChunkType reflect.Type
	// TerminalType is the wire shape of the aggregated terminal this route
	// produces. Advisory, same consumers as ChunkType; contract tests assert
	// it equals the SDKCallCatalog RespType for the callID.
	TerminalType reflect.Type
}

var (
	streamRoutesMu sync.RWMutex
	streamRoutes   = map[string]StreamRoute{}
)

// RegisterStreamRoute registers (or replaces) a streaming route for a callID.
// Called from init() in the package that owns the callID's domain — the host
// bridge never imports that domain. Registering the same callID twice with
// the same route is idempotent; a different route replaces it (the last
// writer wins, matching a single source of truth per callID).
func RegisterStreamRoute(callID string, route StreamRoute) {
	streamRoutesMu.Lock()
	defer streamRoutesMu.Unlock()
	streamRoutes[callID] = route
}

// TypedStreamAggregator is the typed form of StreamAggregator: the chunk and
// terminal types are type parameters, so the backing chunk shape is checked
// at the registration site (compile time) and the runtime type-assertion
// lives once in the generic adapter instead of inside every domain closure.
type TypedStreamAggregator[C, R any] interface {
	Push(C) error
	Terminal() R
}

// typedAggregatorAdapter adapts a TypedStreamAggregator to the bridge-facing
// StreamAggregator. The single v.(C) runtime assertion lives here.
type typedAggregatorAdapter[C, R any] struct {
	agg TypedStreamAggregator[C, R]
}

func (a typedAggregatorAdapter[C, R]) Push(v any) error {
	chunk, ok := v.(C)
	if !ok {
		return fmt.Errorf("stream aggregate: unexpected chunk type %T (want %T)", v, *new(C))
	}
	return a.agg.Push(chunk)
}

func (a typedAggregatorAdapter[C, R]) Terminal() any {
	return a.agg.Terminal()
}

// RegisterTypedStreamRoute is the standard way to register a streaming route:
// encode receives the backing chunk already typed (no closure-level
// v.(domain.X) re-assertion), the aggregator is typed the same way, and the
// route's ChunkType/TerminalType advisory fields are stamped from the type
// parameters so contract tests can verify agreement with the backing actor's
// actor.Streaming[T]() declaration and the SDKCallCatalog RespType.
//
// route carries only the routing/boundary fields (Service, Callable,
// AdaptReq, InjectCallerContext) — its Encode/Aggregate are replaced by the
// typed wrappers. aggregate may be nil for callables whose terminal is the
// backing call's Final value verbatim.
func RegisterTypedStreamRoute[C, R any](callID string, route StreamRoute, encode func(C) (StreamChunkEnvelope, error), aggregate func() TypedStreamAggregator[C, R]) {
	route.Encode = func(v any) (StreamChunkEnvelope, error) {
		chunk, ok := v.(C)
		if !ok {
			return StreamChunkEnvelope{}, fmt.Errorf("stream %s: unexpected chunk type %T (want %T)", callID, v, *new(C))
		}
		return encode(chunk)
	}
	if aggregate != nil {
		route.Aggregate = func() StreamAggregator { return typedAggregatorAdapter[C, R]{agg: aggregate()} }
	}
	route.ChunkType = reflect.TypeOf((*C)(nil)).Elem()
	route.TerminalType = reflect.TypeOf((*R)(nil)).Elem()
	RegisterStreamRoute(callID, route)
}

// LookupStreamRoute returns the route registered for callID. A callID is
// streaming-capable iff ok is true; this is the single check the host bridge
// prefix gate collapses into.
func LookupStreamRoute(callID string) (StreamRoute, bool) {
	streamRoutesMu.RLock()
	defer streamRoutesMu.RUnlock()
	route, ok := streamRoutes[callID]
	return route, ok
}

// IsStreamingCallable reports whether callID has a registered stream route.
// Convenience wrapper over LookupStreamRoute for the bridge's gate check.
func IsStreamingCallable(callID string) bool {
	_, ok := LookupStreamRoute(callID)
	return ok
}

// StreamRouteInjectsCallerContext reports whether the route for callID wants
// the bridge to inject per-invoke caller identity and deadline into its
// payload. Returns false for unknown callIDs (unary callers never receive
// injected context). Used by both the unary and streaming bridge entry
// points so injection stays a catalog decision, not a prefix check.
func StreamRouteInjectsCallerContext(callID string) bool {
	streamRoutesMu.RLock()
	defer streamRoutesMu.RUnlock()
	route, ok := streamRoutes[callID]
	return ok && route.InjectCallerContext
}

// StreamRouteBudget returns the transport-level reverse-call ceiling
// registered for callID. Zero when the callID is unknown or its route
// relies on the generic cap — callers fall back to their own default.
func StreamRouteBudget(callID string) time.Duration {
	streamRoutesMu.RLock()
	defer streamRoutesMu.RUnlock()
	return streamRoutes[callID].Budget
}
