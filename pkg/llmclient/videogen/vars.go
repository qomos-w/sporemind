package videogen

import (
	"fmt"
	"net/http"
	"time"

	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/version"
)

// TODO(actor-ownership): migrate to actor-owned state.

// httpClient has no client-level timeout; the total budget is governed by the
// caller's context so that long video polling is not prematurely cut.
var httpClient = &http.Client{}

// httpClientForParams selects the HTTP client for a request: the shared
// per-proxy client pool when Params.Proxy is set, or the package default for
// direct connections.
func httpClientForParams(p Params) (*http.Client, error) {
	if p.Proxy == "" {
		return httpClient, nil
	}
	c, err := llmclient.HTTPClientForProxy(p.Proxy)
	if err != nil {
		return nil, fmt.Errorf("videogen: %w", err)
	}
	return c, nil
}

// defaultUserAgent identifies this client in HTTP requests to video-generation
// providers.
var defaultUserAgent = "SporeMind/" + version.Version

// pollInterval is the delay between operation status polls. It defaults to 2s
// (within the 2-5s guideline) and may be shortened for tests.
var pollInterval = 2 * time.Second
