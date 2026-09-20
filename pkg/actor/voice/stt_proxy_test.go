package voice

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestSttHTTPClient_EmptyProxyUsesDirectClient(t *testing.T) {
	c, err := sttHTTPClient("")
	if err != nil {
		t.Fatalf("sttHTTPClient(empty): %v", err)
	}
	if c.Timeout != 60*time.Second {
		t.Fatalf("timeout = %v, want 60s", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if ok && tr.Proxy != nil {
		t.Fatal("direct client must not dial through an explicit proxy")
	}
}

func TestSttHTTPClient_ProxyKeepsTimeoutAndDialsThroughProxy(t *testing.T) {
	c, err := sttHTTPClient("http://stt-proxy-test:3128")
	if err != nil {
		t.Fatalf("sttHTTPClient(proxy): %v", err)
	}
	if c.Timeout != 60*time.Second {
		t.Fatalf("timeout = %v, want 60s preserved on proxied client", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatal("proxied transport must be *http.Transport")
	}
	target, _ := url.Parse("https://api.example.com")
	u, err := tr.Proxy(&http.Request{URL: target})
	if err != nil {
		t.Fatalf("transport Proxy(): %v", err)
	}
	if u == nil || u.Host != "stt-proxy-test:3128" {
		t.Fatalf("transport must dial through stt-proxy-test:3128, got %v", u)
	}
}

func TestSttHTTPClient_InvalidProxyErrors(t *testing.T) {
	for _, bad := range []string{"ftp://nope:21", "http://%zz"} {
		if _, err := sttHTTPClient(bad); err == nil {
			t.Fatalf("sttHTTPClient(%q) must error", bad)
		}
	}
}
