package nativetools

// DefaultRegistry returns a registry with the built-in provider adapters
// (anthropic, bigmodel, openai) pre-registered.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(anthropicProvider{})
	r.Register(bigmodelProvider{})
	r.Register(openaiProvider{})
	return r
}
