package llmclient

import (
	"context"
	"errors"
	"testing"
)

// registryStub is a minimal Client used to verify the registry picks the right
// factory and decorates it. Its Stream records the request model so tests
// can assert wiring without a real HTTP server.
type registryStub struct {
	endpoint string
	token    string
	model    string
}

func (s *registryStub) Stream(ctx context.Context, req Request) (Stream, error) {
	s.model = req.Model
	return nil, errors.New("registryStub: not implemented")
}

// registryStubFactory returns a factory that builds a registryStub tagged with
// the protocol, so tests can distinguish which factory was selected.
func registryStubFactory(tag string) Factory {
	return func(endpoint, authToken string) Client {
		return &registryStub{endpoint: endpoint, token: authToken + "|" + tag}
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Descriptor{
		Protocol: "anthropic",
		Factory:  registryStubFactory("anthropic"),
	}); err != nil {
		t.Fatalf("register anthropic: %v", err)
	}
	if err := r.Register(Descriptor{
		Protocol: "openai",
		Factory:  registryStubFactory("openai"),
	}); err != nil {
		t.Fatalf("register openai: %v", err)
	}

	d, ok := r.Get("anthropic")
	if !ok {
		t.Fatal("expected anthropic to be registered")
	}
	if d.Protocol != "anthropic" {
		t.Errorf("protocol = %q, want anthropic", d.Protocol)
	}

	if _, ok := r.Get("gemini"); ok {
		t.Fatal("gemini must not be registered")
	}
}

func TestRegistry_RegisterRejectsInvalid(t *testing.T) {
	r := NewRegistry()

	if err := r.Register(Descriptor{Factory: registryStubFactory("x")}); err == nil {
		t.Fatal("empty Protocol must be rejected")
	}
	if err := r.Register(Descriptor{Protocol: "openai"}); err == nil {
		t.Fatal("missing Factory must be rejected")
	}
}

func TestRegistry_RegisterOverwritesDuplicate(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Descriptor{Protocol: "openai", Factory: registryStubFactory("v1")})
	r.MustRegister(Descriptor{Protocol: "openai", Factory: registryStubFactory("v2")})

	if _, ok := r.Get("openai"); !ok {
		t.Fatal("openai should be registered after overwrite")
	}
	// Reuse the same descriptor's Factory directly to confirm v2 replaced v1.
	d, _ := r.Get("openai")
	c := d.Factory("ep", "tk")
	stub, ok := c.(*registryStub)
	if !ok {
		t.Fatalf("expected *stubClient, got %T", c)
	}
	if !endsWith(stub.token, "|v2") {
		t.Errorf("expected overwritten factory v2, token=%q", stub.token)
	}
}

func TestRegistry_NewClient_UnknownProtocol(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Descriptor{Protocol: "openai", Factory: registryStubFactory("openai")})

	if _, err := r.NewClient("gemini", "ep", "tok", "", 0); err == nil {
		t.Fatal("unknown protocol must return error")
	}
	if _, err := r.NewClient("", "ep", "tok", "", 0); err == nil {
		t.Fatal("empty protocol must return error")
	}
}

func TestRegistry_NewClient_BuildsAndDecorates(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Descriptor{Protocol: "openai", Factory: registryStubFactory("openai")})

	// maxConcurrency=0 -> retry decorator only.
	c, err := r.NewClient("openai", "https://ep", "tok", "", 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c == nil {
		t.Fatal("client must be non-nil")
	}

	// maxConcurrency>0 -> concurrency decorator wraps retry.
	c2, err := r.NewClient("openai", "https://ep", "tok", "", 4)
	if err != nil {
		t.Fatalf("NewClient with concurrency: %v", err)
	}
	if c2 == nil {
		t.Fatal("concurrency client must be non-nil")
	}
}

func TestRegistry_NewResponsesFactory_BuildsAndDecorates(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Descriptor{Protocol: "responses", Factory: NewResponsesFactory()})

	// The registered factory must construct a real ResponsesClient.
	d, ok := r.Get("responses")
	if !ok {
		t.Fatal("responses should be registered")
	}
	if _, ok := d.Factory("https://ep", "tok").(*ResponsesClient); !ok {
		t.Fatalf("expected factory to build *ResponsesClient, got %T", d.Factory("https://ep", "tok"))
	}

	// NewClient applies the retry decorator on top of the responses client.
	c, err := r.NewClient("responses", "https://ep", "tok", "", 0)
	if err != nil {
		t.Fatalf("NewClient(responses): %v", err)
	}
	rc, ok := c.(*RetryClient)
	if !ok {
		t.Fatalf("expected *RetryClient, got %T", c)
	}
	if _, ok := rc.inner.(*ResponsesClient); !ok {
		t.Fatalf("expected retry to wrap *ResponsesClient, got %T", rc.inner)
	}
}

func TestRegistry_ExtraBody_AppliesPolicy(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Descriptor{
		Protocol: "openai",
		Factory:  registryStubFactory("openai"),
		ExtraBody: func(model string) map[string]any {
			if model == "kimi-k2-5" {
				return map[string]any{"chat_template_args": map[string]any{"enable_thinking": true}}
			}
			return nil
		},
	})

	got := r.ExtraBody("openai", "kimi-k2-5")
	if got == nil {
		t.Fatal("expected extra body for kimi-k2-5")
	}
	args, ok := got["chat_template_args"].(map[string]any)
	if !ok {
		t.Fatalf("expected chat_template_args map, got %T", got["chat_template_args"])
	}
	if args["enable_thinking"] != true {
		t.Error("enable_thinking should be true")
	}

	if got := r.ExtraBody("openai", "gpt-4o"); got != nil {
		t.Fatalf("expected nil for non-kimi model, got %v", got)
	}
}

func TestRegistry_ExtraBody_NoPolicyOrUnknown(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Descriptor{Protocol: "anthropic", Factory: registryStubFactory("anthropic")})

	if got := r.ExtraBody("anthropic", "claude-opus-4"); got != nil {
		t.Fatalf("descriptor without policy must return nil, got %v", got)
	}
	if got := r.ExtraBody("gemini", "gemini-pro"); got != nil {
		t.Fatalf("unknown protocol must return nil, got %v", got)
	}
}

func TestRegistry_SupportedAndProtocols(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Descriptor{Protocol: "endpoint", Factory: registryStubFactory("endpoint")})
	r.MustRegister(Descriptor{Protocol: "anthropic", Factory: registryStubFactory("anthropic")})
	r.MustRegister(Descriptor{Protocol: "openai", Factory: registryStubFactory("openai")})

	if !r.Supported("openai") {
		t.Error("openai should be supported")
	}
	if r.Supported("gemini") {
		t.Error("gemini must not be supported")
	}
	if r.Supported("") {
		t.Error("empty protocol must not be supported")
	}

	got := r.Protocols()
	want := []string{"anthropic", "endpoint", "openai"}
	if len(got) != len(want) {
		t.Fatalf("Protocols = %v, want %v", got, want)
	}
	for i, p := range want {
		if got[i] != p {
			t.Errorf("Protocols[%d] = %q, want %q", i, got[i], p)
		}
	}
}

func TestNilRegistryIsSafe(t *testing.T) {
	var r *Registry
	if r.Supported("openai") {
		t.Error("nil registry must report unsupported")
	}
	if _, ok := r.Get("openai"); ok {
		t.Error("nil registry Get must return false")
	}
	if got := r.ExtraBody("openai", "m"); got != nil {
		t.Errorf("nil registry ExtraBody must return nil, got %v", got)
	}
	if got := r.Protocols(); got != nil {
		t.Errorf("nil registry Protocols must return nil, got %v", got)
	}
	if err := r.Register(Descriptor{Protocol: "x", Factory: registryStubFactory("x")}); err == nil {
		t.Error("nil registry Register must error")
	}
	if _, err := r.NewClient("openai", "ep", "tok", "", 0); err == nil {
		t.Error("nil registry NewClient must error")
	}
}

// endsWith is a tiny local helper to avoid importing strings just for one
// assertion in this test file.
func endsWith(s, suffix string) bool {
	if len(suffix) > len(s) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}
