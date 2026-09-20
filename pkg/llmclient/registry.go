package llmclient

import (
	"fmt"
	"sort"
)

// Factory constructs a protocol client from an endpoint URL and an auth
// token. It mirrors the signature of the existing New*Client constructors
// (NewAnthropicClient, NewOpenAIClient, NewEndpointClient) so descriptors
// can wrap them without adapter boilerplate.
type Factory func(endpoint, authToken string) Client

// ExtraBodyPolicy returns provider-specific request body extensions for the
// given model name, or nil if none apply. It replaces the previous
// providerExtraBody switch in aiaggregator: protocol/model rules now live
// alongside the descriptor that owns the protocol instead of in a separate
// heuristic function.
type ExtraBodyPolicy func(model string) map[string]any

// Descriptor describes one wire protocol: how to build its client and what
// (if anything) to merge into request bodies on a per-model basis. It is a
// value type; descriptors are constructed by callers and handed to a
// Registry. Descriptors intentionally carry no mutable state.
type Descriptor struct {
	// Protocol is the wire protocol identifier matched against
	// ProviderModel.Protocol / Provider.Kind (e.g. "anthropic", "openai",
	// "endpoint"). It must be non-empty.
	Protocol string

	// Factory builds the base Client for this protocol. Required.
	Factory Factory

	// ExtraBody is an optional per-model body extension policy. May be nil,
	// in which case no extensions are applied.
	ExtraBody ExtraBodyPolicy
}

// Registry is an immutable-by-convention catalogue of protocol descriptors.
// It is constructed explicitly (typically by the aiaggregator actor at
// startup) and carries no package-level state: there is no init() and no
// global map. Callers obtain a Registry via NewRegistry and Register.
type Registry struct {
	descriptors map[string]Descriptor
}

// NewAnthropicFactory wraps NewAnthropicClient to match the Factory signature.
// It exists so descriptors can register concrete client constructors without
// each call site writing its own adapter.
func NewAnthropicFactory() Factory {
	return func(endpoint, authToken string) Client {
		return NewAnthropicClient(endpoint, authToken)
	}
}

// NewOpenAIFactory wraps NewOpenAIClient to match the Factory signature.
func NewOpenAIFactory() Factory {
	return func(endpoint, authToken string) Client {
		return NewOpenAIClient(endpoint, authToken)
	}
}

// NewResponsesFactory wraps NewResponsesClient to match the Factory signature.
func NewResponsesFactory() Factory {
	return func(endpoint, authToken string) Client {
		return NewResponsesClient(endpoint, authToken)
	}
}

// NewEndpointFactory wraps NewEndpointClient to match the Factory signature.
func NewEndpointFactory() Factory {
	return func(endpoint, authToken string) Client {
		return NewEndpointClient(endpoint, authToken)
	}
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{descriptors: make(map[string]Descriptor)}
}

// Register stores a descriptor keyed by its Protocol. Registering the same
// protocol twice overwrites the previous entry; callers that want to detect
// collisions should check Supported/Get first. A descriptor with an empty
// Protocol is rejected with a non-nil error.
func (r *Registry) Register(d Descriptor) error {
	if r == nil {
		return fmt.Errorf("llmclient: register into nil registry")
	}
	if d.Protocol == "" {
		return fmt.Errorf("llmclient: descriptor missing Protocol")
	}
	if d.Factory == nil {
		return fmt.Errorf("llmclient: descriptor %q missing Factory", d.Protocol)
	}
	r.descriptors[d.Protocol] = d
	return nil
}

// MustRegister panics on registration error. Intended for static, startup
// descriptor tables where a malformed descriptor is a programming bug.
func (r *Registry) MustRegister(d Descriptor) {
	if err := r.Register(d); err != nil {
		panic(err)
	}
}

// Get returns the descriptor for a protocol. The ok flag is false when the
// protocol is empty or has no registered descriptor.
func (r *Registry) Get(protocol string) (Descriptor, bool) {
	if r == nil || protocol == "" {
		return Descriptor{}, false
	}
	d, ok := r.descriptors[protocol]
	return d, ok
}

// NewClient builds a client for the given protocol, applying the descriptor
// Factory. When proxyURL is non-empty it replaces the client's default HTTP
// transport with a proxied one (HTTP(S)/SOCKS5). It wraps the result with
// NewRetryClient and (when maxConcurrency > 0) NewConcurrencyClient, preserving
// the previous newClient decoration order so behaviour is unchanged.
func (r *Registry) NewClient(protocol, endpoint, authToken, proxyURL string, maxConcurrency int32) (Client, error) {
	d, ok := r.Get(protocol)
	if !ok {
		return nil, fmt.Errorf("unsupported protocol %q", protocol)
	}
	base := d.Factory(endpoint, authToken)
	if proxyURL != "" {
		hc, err := HTTPClientForProxy(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("llmclient: proxy for protocol %q: %w", protocol, err)
		}
		if carrier, ok := base.(HTTPClientCarrier); ok {
			carrier.SetHTTPClient(hc)
		}
	}
	base = NewRetryClient(base)
	if maxConcurrency > 0 {
		return NewConcurrencyClient(base, endpoint, authToken, int(maxConcurrency)), nil
	}
	return base, nil
}

// ExtraBody returns the per-model body extension for a protocol. If the
// protocol is unknown or its descriptor declares no ExtraBody policy, nil is
// returned (no extension). This is the single entry point for provider
// extra-body rules; callers must not branch on model-name prefixes.
func (r *Registry) ExtraBody(protocol, model string) map[string]any {
	d, ok := r.Get(protocol)
	if !ok || d.ExtraBody == nil {
		return nil
	}
	return d.ExtraBody(model)
}

// Supported reports whether a protocol has a registered descriptor. Use it
// to validate provider/model protocol configuration at config-build time
// rather than failing deep inside dispatch.
func (r *Registry) Supported(protocol string) bool {
	_, ok := r.Get(protocol)
	return ok
}

// Protocols returns the sorted list of registered protocol identifiers. The
// sort is lexical and stable, which keeps error messages and diagnostics
// deterministic.
func (r *Registry) Protocols() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.descriptors))
	for k := range r.descriptors {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
