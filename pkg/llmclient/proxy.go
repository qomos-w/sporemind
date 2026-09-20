package llmclient

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// proxiedClients caches one *http.Client per distinct proxy URL so all
// providers sharing a proxy share its connection pool.
var proxiedClients sync.Map // proxyURL string -> *http.Client

// HTTPClientCarrier is implemented by protocol clients that own an injectable
// *http.Client, so a per-provider proxy can replace the package default. It
// keeps the Factory signature unchanged: NewClient applies the carrier after
// construction.
type HTTPClientCarrier interface {
	SetHTTPClient(c *http.Client)
}

// HTTPClientForProxy returns an HTTP client whose transport dials through
// proxyURL. Supported schemes: http, https, socks5, socks5h. The result is
// cached per proxy URL; an empty or invalid proxyURL yields an error so the
// failure surfaces at client-construction time instead of deep inside dispatch.
func HTTPClientForProxy(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return nil, fmt.Errorf("llmclient: empty proxy url")
	}
	if c, ok := proxiedClients.Load(proxyURL); ok {
		return c.(*http.Client), nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("llmclient: parse proxy url %q: %w", proxyURL, err)
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("llmclient: unsupported proxy scheme %q in %q", u.Scheme, proxyURL)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(u),
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			MaxConnsPerHost:     20,
			IdleConnTimeout:     90 * time.Second,
			// ResponseHeaderTimeout is intentionally unlimited (0): synchronous
			// image generation can take minutes before the first response byte,
			// and the caller's context bounds each call's overall budget.
			ResponseHeaderTimeout: 0,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
			HTTP2: &http.HTTP2Config{
				// Same dead-pooled-connection detection as the default
				// transport (vars.go): x/net/http2 ignores
				// ResponseHeaderTimeout, so without a ping the wait for
				// response headers on a silently-dropped connection is
				// unbounded.
				SendPingTimeout: 15 * time.Second,
				PingTimeout:     10 * time.Second,
			},
		},
	}
	actual, _ := proxiedClients.LoadOrStore(proxyURL, client)
	return actual.(*http.Client), nil
}
