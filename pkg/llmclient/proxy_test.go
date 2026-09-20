package llmclient

import (
	"net/http"
	"testing"
)

func TestHTTPClientForProxy_CachesPerURL(t *testing.T) {
	a, err := HTTPClientForProxy("http://cache-test:3128")
	if err != nil {
		t.Fatalf("HTTPClientForProxy: %v", err)
	}
	b, err := HTTPClientForProxy("http://cache-test:3128")
	if err != nil {
		t.Fatalf("HTTPClientForProxy second call: %v", err)
	}
	if a != b {
		t.Fatal("the same proxy URL must return the cached client instance")
	}
}

func TestHTTPClientForProxy_TransportDialsThroughProxy(t *testing.T) {
	c, err := HTTPClientForProxy("http://transport-test:8080")
	if err != nil {
		t.Fatalf("HTTPClientForProxy: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", c.Transport)
	}
	if tr.Proxy == nil {
		t.Fatal("transport must carry a Proxy func")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/v1/messages", nil)
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("transport Proxy(): %v", err)
	}
	if u == nil || u.String() != "http://transport-test:8080" {
		t.Fatalf("request would dial through %v, want http://transport-test:8080", u)
	}
}

func TestHTTPClientForProxy_RejectsInvalid(t *testing.T) {
	for _, bad := range []string{"", "ftp://nope:21", "not a url://"} {
		if _, err := HTTPClientForProxy(bad); err == nil {
			t.Errorf("proxy %q must be rejected", bad)
		}
	}
}

func TestRegistry_NewClient_AppliesProxyToCarrier(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Descriptor{Protocol: "anthropic", Factory: NewAnthropicFactory()})

	c, err := r.NewClient("anthropic", "https://ep", "tok", "http://registry-test:7890", 0)
	if err != nil {
		t.Fatalf("NewClient with proxy: %v", err)
	}
	rc, ok := c.(*RetryClient)
	if !ok {
		t.Fatalf("expected *RetryClient, got %T", c)
	}
	ac, ok := rc.inner.(*AnthropicClient)
	if !ok {
		t.Fatalf("expected retry to wrap *AnthropicClient, got %T", rc.inner)
	}
	if ac.HTTPClient == nil || ac.HTTPClient == httpClient {
		t.Fatal("proxied client must replace the package default HTTP client")
	}
	tr, ok := ac.HTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", ac.HTTPClient.Transport)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/v1/messages", nil)
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("transport Proxy(): %v", err)
	}
	if u == nil || u.String() != "http://registry-test:7890" {
		t.Fatalf("client dials through %v, want http://registry-test:7890", u)
	}
}

func TestRegistry_NewClient_InvalidProxyErrors(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Descriptor{Protocol: "openai", Factory: NewOpenAIFactory()})

	if _, err := r.NewClient("openai", "https://ep", "tok", "ftp://bad:21", 0); err == nil {
		t.Fatal("invalid proxy scheme must fail at client construction")
	}
}

func TestHTTPClientForProxy_ResponseHeaderTimeoutIsUnlimited(t *testing.T) {
	c, err := HTTPClientForProxy("http://timeout-test:3128")
	if err != nil {
		t.Fatalf("HTTPClientForProxy: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", c.Transport)
	}
	if tr.ResponseHeaderTimeout != 0 {
		t.Fatalf("ResponseHeaderTimeout = %v, want 0 (unlimited; ctx governs)", tr.ResponseHeaderTimeout)
	}
}

func TestRegistry_NewClient_EmptyProxyKeepsDefault(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Descriptor{Protocol: "anthropic", Factory: NewAnthropicFactory()})

	c, err := r.NewClient("anthropic", "https://ep", "tok", "", 0)
	if err != nil {
		t.Fatalf("NewClient without proxy: %v", err)
	}
	rc := c.(*RetryClient)
	ac := rc.inner.(*AnthropicClient)
	if ac.HTTPClient != httpClient {
		t.Fatal("empty proxy must keep the default direct client")
	}
}
