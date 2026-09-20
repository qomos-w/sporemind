package mediagen

import (
	"net/http"
	"net/url"
	"testing"
)

func TestHTTPClientForParams_EmptyProxyReturnsDefault(t *testing.T) {
	c, err := httpClientForParams(Params{})
	if err != nil {
		t.Fatalf("httpClientForParams(empty): %v", err)
	}
	if c != httpClient {
		t.Fatal("empty proxy must return the package default client")
	}
}

func TestHTTPClientForParams_ProxyDialsThroughProxy(t *testing.T) {
	c, err := httpClientForParams(Params{Proxy: "http://mediagen-proxy-test:3128"})
	if err != nil {
		t.Fatalf("httpClientForParams(proxy): %v", err)
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
	if u == nil || u.Host != "mediagen-proxy-test:3128" {
		t.Fatalf("transport must dial through mediagen-proxy-test:3128, got %v", u)
	}
}

func TestHTTPClientForParams_InvalidProxyErrors(t *testing.T) {
	if _, err := httpClientForParams(Params{Proxy: "ftp://nope:21"}); err == nil {
		t.Fatal("httpClientForParams(invalid) must error")
	}
}
