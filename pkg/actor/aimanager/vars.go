package aimanager

// This file consolidates package-level variable declarations for the
// aimanager package.

// --- Protocol support ---

// supportedProtocols is the set of wire protocols with a registered client in
// pkg/llmclient (anthropic/openai/responses/endpoint). It must stay in sync
// with the descriptors registered by aiaggregator.newBuiltinRegistry; the
// TestSupportedProtocols (this package) and TestBuiltinRegistry_SupportedProtocols
// (aiaggregator) tests lock that invariant. Declaring it here lets aimanager fail
// unsupported protocols at config-build time instead of deep inside dispatch.
// "gemini" is intentionally absent: no chat client exists.
var supportedProtocols = map[string]bool{
	"anthropic": true,
	"openai":    true,
	"responses": true,
	"endpoint":  true,
}
