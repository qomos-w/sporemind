package llmclient

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// The 2026-09-05 plugin llm.complete silent wedge: a pooled HTTP/2 connection
// silently dropped by the provider's LB swallowed the next stream-open —
// x/net/http2 ignores http.Transport.ResponseHeaderTimeout, so the request
// waited for response headers until the caller's reverse budget killed it.
// The transports must therefore carry HTTP/2 health checking so dead pooled
// connections are detected and retried instead of stalling.
func TestDefaultTransportHasHTTP2Keepalive(t *testing.T) {
	tr, ok := httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("default client transport is %T, want *http.Transport", httpClient.Transport)
	}
	if tr.HTTP2 == nil {
		t.Fatal("default transport has no HTTP2 config")
	}
	if tr.HTTP2.SendPingTimeout == 0 {
		t.Error("SendPingTimeout must be set, otherwise dead pooled connections are never detected")
	}
	if tr.HTTP2.PingTimeout == 0 {
		t.Error("PingTimeout must be set, otherwise an unanswered ping never closes the connection")
	}
	if tr.ResponseHeaderTimeout == 0 {
		t.Error("ResponseHeaderTimeout must stay set for the HTTP/1 fallback path")
	}
}

func TestProxiedTransportHasHTTP2Keepalive(t *testing.T) {
	c, err := HTTPClientForProxy("http://h2-keepalive-test:3128")
	if err != nil {
		t.Fatalf("HTTPClientForProxy: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("proxied transport is %T, want *http.Transport", c.Transport)
	}
	if tr.HTTP2 == nil {
		t.Fatal("proxied transport has no HTTP2 config")
	}
	if tr.HTTP2.SendPingTimeout != 15*time.Second {
		t.Errorf("SendPingTimeout = %v, want 15s", tr.HTTP2.SendPingTimeout)
	}
	if tr.HTTP2.PingTimeout != 10*time.Second {
		t.Errorf("PingTimeout = %v, want 10s", tr.HTTP2.PingTimeout)
	}
}

// IsRetryableError must treat the keepalive-detected death as transient so
// the stream-open retry loop recovers with a fresh connection.
func TestIsRetryableError_H2ClientConnectionLost(t *testing.T) {
	if !IsRetryableError(&clientConnectionLostError{}) {
		t.Error("client connection lost must be retryable")
	}
	if !IsRetryableError(errors.New("net/http: HTTP/1.x transport connection broken: http2: client connection lost")) {
		t.Error("wrapped client connection lost must be retryable")
	}
}

type clientConnectionLostError struct{}

func (clientConnectionLostError) Error() string { return "http2: client connection lost" }
