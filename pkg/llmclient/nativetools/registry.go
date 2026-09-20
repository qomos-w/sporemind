package nativetools

import (
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// Provider adapts provider-native tools (e.g. web_search) for a specific
// LLM provider. The provider is selected by model name via Match.
type Provider interface {
	// Name returns the provider identifier, e.g. "anthropic" / "openai" / "bigmodel".
	Name() string

	// Match reports whether the given model name belongs to this provider.
	Match(model string) bool

	// WebSearch returns the native web_search tool spec for this provider.
	// If the provider does not support web_search, ok is false.
	WebSearch() (domain.ToolSpec, bool)

	// Protocol returns the wire protocol on which this provider's native
	// tools are valid. A native tool is only sent to an endpoint whose
	// resolved protocol matches; e.g. bigmodel's web_search is valid on the
	// "bigmodel" protocol, so a glm model reached via the openai/endpoint
	// protocol drops it (those wire schemas only accept function/plugin tools).
	Protocol() string
}

// Registry holds a list of Provider adapters and selects the first one whose
// Match returns true.
type Registry struct {
	providers []Provider
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a Provider to the registry. Providers are evaluated in
// registration order.
func (r *Registry) Register(p Provider) {
	r.providers = append(r.providers, p)
}

// AppendWebSearch appends a provider-native web_search tool to tools if a
// registered Provider matches the model name. If no provider matches, tools is
// returned unchanged.
//
// Note: this is a best-effort hint keyed on model name only — the caller does
// not know the wire protocol at compile time. The authoritative filter is
// Reconcile, applied at dispatch where the resolved unit's protocol is known.
func (r *Registry) AppendWebSearch(tools []domain.ToolSpec, model string) []domain.ToolSpec {
	if strings.TrimSpace(model) == "" {
		return tools
	}
	for _, p := range r.providers {
		if p.Match(model) {
			if spec, ok := p.WebSearch(); ok {
				return append(tools, spec)
			}
			return tools
		}
	}
	return tools
}

// Reconcile drops provider-native tool declarations (Type != "") whose type is
// not valid on the resolved wire protocol. Standard function tools (Type == "")
// are always kept. This is the authoritative filter applied at dispatch — the
// single point that knows the resolved unit's protocol — so a native tool like
// web_search is never sent to an endpoint whose wire schema only accepts
// "function"/"plugin" tools (e.g. a glm model reached via the openai/endpoint
// protocol). Only the tools array is filtered; the message history is untouched
// so conversation context structure is preserved across model switches.
func (r *Registry) Reconcile(tools []domain.ToolSpec, protocol string) []domain.ToolSpec {
	if len(tools) == 0 {
		return tools
	}
	validTypes := r.nativeTypesByProtocol()
	out := make([]domain.ToolSpec, 0, len(tools))
	for _, t := range tools {
		if t.Type == "" {
			out = append(out, t)
			continue
		}
		// Keep a native tool only when its type is explicitly declared by a
		// registered provider for this exact protocol. Unknown native types are
		// dropped (conservative) to avoid sending unsupported tool types.
		if p, ok := validTypes[t.Type]; ok && p == protocol {
			out = append(out, t)
		}
	}
	return out
}

// nativeTypesByProtocol builds a type -> protocol map from registered
// providers' native tool declarations.
func (r *Registry) nativeTypesByProtocol() map[string]string {
	m := make(map[string]string, len(r.providers))
	for _, p := range r.providers {
		if spec, ok := p.WebSearch(); ok && spec.Type != "" {
			m[spec.Type] = p.Protocol()
		}
	}
	return m
}
