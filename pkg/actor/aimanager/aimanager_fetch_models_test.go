package aimanager

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestFetchAnthropicModels_Success(t *testing.T) {
	var gotPath, gotAPIKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "claude-opus-4-7"},
				{"id": "claude-sonnet-4-5"},
			},
		})
	}))
	defer srv.Close()

	models, ok := fetchAnthropicModels(srv.URL, "test-key", "")
	if !ok {
		t.Fatal("expected ok=true, got false")
	}
	if gotPath != "/v1/models" {
		t.Fatalf("got path %q, want /v1/models", gotPath)
	}
	if gotAPIKey != "test-key" {
		t.Fatalf("got x-api-key %q, want test-key", gotAPIKey)
	}
	if gotVersion != "2023-06-01" {
		t.Fatalf("got anthropic-version %q, want 2023-06-01", gotVersion)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].Name != "claude-opus-4-7" {
		t.Fatalf("got first model %q", models[0].Name)
	}
}

func TestFetchAnthropicModels_EndpointWithV1(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "claude-sonnet-4-5"}},
		})
	}))
	defer srv.Close()

	if _, ok := fetchAnthropicModels(srv.URL+"/v1", "k", ""); !ok {
		t.Fatal("expected ok=true")
	}
	if gotPath != "/v1/models" {
		t.Fatalf("got path %q, want /v1/models (no double v1)", gotPath)
	}
}

func TestFetchAnthropicModels_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, ok := fetchAnthropicModels(srv.URL, "k", "")
	if ok {
		t.Fatal("expected ok=false on 500")
	}
}

func TestFetchAnthropicModels_Unreachable(t *testing.T) {
	_, ok := fetchAnthropicModels("http://127.0.0.1:1", "k", "")
	if ok {
		t.Fatal("expected ok=false on unreachable host")
	}
}

func TestProbeResponsesSupport_ShapeAndAuth(t *testing.T) {
	var gotMethod, gotPath, gotCT, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ok := probeResponsesSupport(context.Background(), srv.URL, "sk-test", "")
	if !ok {
		t.Fatal("expected probe=true on 200")
	}
	if gotMethod != "POST" {
		t.Errorf("expected POST, got %q", gotMethod)
	}
	if gotPath != "/responses" {
		t.Errorf("expected /responses, got %q", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("expected application/json content-type, got %q", gotCT)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("expected Bearer sk-test, got %q", gotAuth)
	}
	if gotBody != `{"model":"__probe__","input":"ping","max_output_tokens":1}` {
		t.Errorf("unexpected probe body: %q", gotBody)
	}
}

func TestProbeResponsesSupport_200Supported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if !probeResponsesSupport(context.Background(), srv.URL, "k", "") {
		t.Fatal("expected probe=true on 200")
	}
}

func TestProbeResponsesSupport_400Supported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	if !probeResponsesSupport(context.Background(), srv.URL, "k", "") {
		t.Fatal("expected probe=true on 400 (route exists)")
	}
}

func TestProbeResponsesSupport_404NotSupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if probeResponsesSupport(context.Background(), srv.URL, "k", "") {
		t.Fatal("expected probe=false on 404")
	}
}

func TestProbeResponsesSupport_405NotSupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	if probeResponsesSupport(context.Background(), srv.URL, "k", "") {
		t.Fatal("expected probe=false on 405")
	}
}

func TestProbeResponsesSupport_NotFoundBodyStillUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not found"}`)
	}))
	defer srv.Close()

	if probeResponsesSupport(context.Background(), srv.URL+"///", "k", "") {
		t.Fatal("expected probe=false on 404 with not-found body")
	}
}
func TestProbeResponsesSupport_NetworkErrorNotSupported(t *testing.T) {
	if probeResponsesSupport(context.Background(), "http://127.0.0.1:1", "k", "") {
		t.Fatal("expected probe=false on network error")
	}
}

// proxyEchoServer is an httptest server that mimics a transparent HTTP proxy:
// it inspects the inbound request and responds with the JSON payload expected
// for a /models listing, and with 200 OK for the POST /responses probe. The
// direct endpoint, which would 500, must never be hit when proxyURL routes
// traffic here.
func proxyEchoServer(t *testing.T) (proxySrv *httptest.Server, proxyHits *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method == http.MethodPost {
			// ProbeResponsesSupport POSTs /responses; 200 means "supported".
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "via-proxy"}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func brokenDirectServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestFetchAnthropicModels_UsesProxy verifies that when proxyURL is set the
// GET /v1/models request is sent through the proxy and the direct endpoint is
// never dialed.
func TestFetchAnthropicModels_UsesProxy(t *testing.T) {
	proxySrv, proxyHits := proxyEchoServer(t)
	directSrv, directHits := brokenDirectServer(t)

	models, ok := fetchAnthropicModels(directSrv.URL, "k", proxySrv.URL)
	if !ok {
		t.Fatal("expected ok=true via proxy, got false")
	}
	if *directHits != 0 {
		t.Errorf("direct endpoint hit %d times; expected 0 (should have gone via proxy)", *directHits)
	}
	if *proxyHits == 0 {
		t.Errorf("proxy server never received the request")
	}
	if len(models) != 1 || models[0].Name != "via-proxy" {
		t.Errorf("expected one model 'via-proxy', got %+v", models)
	}
}

// TestProbeResponsesSupport_UsesProxy verifies the same routing for the
// responses-API sniff probe.
func TestProbeResponsesSupport_UsesProxy(t *testing.T) {
	proxySrv, proxyHits := proxyEchoServer(t)
	directSrv, directHits := brokenDirectServer(t)

	supported := probeResponsesSupport(context.Background(), directSrv.URL, "k", proxySrv.URL)
	if !supported {
		t.Fatal("expected probe=true via proxy (200 response), got false")
	}
	if *directHits != 0 {
		t.Errorf("direct endpoint hit %d times; expected 0 (should have gone via proxy)", *directHits)
	}
	if *proxyHits == 0 {
		t.Errorf("proxy server never received the probe request")
	}
}

// TestFetchModels_ProviderProxyHonored verifies the integration: when a
// matching entry in a.Providers has a Proxy set, the openai-kind fetch-models
// path routes both the GET /models listing and the POST /responses probe
// through that proxy, and the configured direct endpoint is never dialed.
func TestFetchModels_ProviderProxyHonored(t *testing.T) {
	proxySrv, proxyHits := proxyEchoServer(t)
	directSrv, directHits := brokenDirectServer(t)

	a := &Actor{Providers: []domain.Provider{
		{Name: "p1", Proxy: proxySrv.URL},
	}}
	resp, err := a.handleProviderFetchModels(nil, domain.AIManagerProviderFetchModelsReq{
		Name:     "p1",
		Kind:     "openai",
		Endpoint: directSrv.URL,
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if *directHits != 0 {
		t.Errorf("direct endpoint hit %d times; expected 0 (should have gone via proxy)", *directHits)
	}
	if *proxyHits != 2 {
		t.Errorf("proxy server should have been hit twice (GET /models + POST /responses), got %d", *proxyHits)
	}
	if len(resp.Models) != 1 || resp.Models[0].Name != "via-proxy" {
		t.Fatalf("expected one model 'via-proxy' from proxy, got %+v", resp.Models)
	}
	if resp.Models[0].Protocol != "responses" {
		t.Errorf("expected protocol responses (probe succeeded via proxy), got %q", resp.Models[0].Protocol)
	}
}

// TestFetchModels_ReqProxyUnsavedProvider covers the add-provider flow: the
// actor has no stored Providers entry yet (form not saved), but the request
// carries the proxy the user just typed. The fetch must still route through it.
func TestFetchModels_ReqProxyUnsavedProvider(t *testing.T) {
	proxySrv, proxyHits := proxyEchoServer(t)
	directSrv, directHits := brokenDirectServer(t)

	a := &Actor{}
	resp, err := a.handleProviderFetchModels(nil, domain.AIManagerProviderFetchModelsReq{
		Name:     "openai", // form name empty → kind fallback, matches nothing
		Kind:     "openai",
		Endpoint: directSrv.URL,
		Proxy:    proxySrv.URL,
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if *directHits != 0 {
		t.Errorf("direct endpoint hit %d times; expected 0 (should have gone via proxy)", *directHits)
	}
	if *proxyHits != 2 {
		t.Errorf("proxy server should have been hit twice (GET /models + POST /responses), got %d", *proxyHits)
	}
	if len(resp.Models) != 1 || resp.Models[0].Name != "via-proxy" {
		t.Errorf("expected one model 'via-proxy', got %+v", resp.Models)
	}
}

// TestFetchModels_ReqProxyOverridesStored verifies req.Proxy (fresh form
// value) wins over the stored Provider.Proxy when both are present.
func TestFetchModels_ReqProxyOverridesStored(t *testing.T) {
	proxySrv, proxyHits := proxyEchoServer(t)
	storedSrv, storedHits := brokenDirectServer(t)
	directSrv, directHits := brokenDirectServer(t)

	a := &Actor{Providers: []domain.Provider{
		{Name: "p1", Proxy: storedSrv.URL}, // stored proxy points at a dead end
	}}
	_, err := a.handleProviderFetchModels(nil, domain.AIManagerProviderFetchModelsReq{
		Name:     "p1",
		Kind:     "openai",
		Endpoint: directSrv.URL,
		Proxy:    proxySrv.URL, // req proxy wins
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if *storedHits != 0 {
		t.Errorf("stored proxy endpoint hit %d times; expected 0 (req.Proxy should override)", *storedHits)
	}
	if *directHits != 0 {
		t.Errorf("direct endpoint hit %d times; expected 0", *directHits)
	}
	if *proxyHits == 0 {
		t.Errorf("req proxy server never received the request")
	}
}

// TestFetchModels_AnthropicProviderProxyHonored mirrors the earlier proxy test
// for the anthropic kind (which calls fetchAnthropicModels directly).
func TestFetchModels_AnthropicProviderProxyHonored(t *testing.T) {
	proxySrv, proxyHits := proxyEchoServer(t)
	directSrv, directHits := brokenDirectServer(t)

	a := &Actor{Providers: []domain.Provider{
		{Name: "p-anth", Proxy: proxySrv.URL},
	}}
	resp, err := a.handleProviderFetchModels(nil, domain.AIManagerProviderFetchModelsReq{
		Name:     "p-anth",
		Kind:     "anthropic",
		Endpoint: directSrv.URL,
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if *directHits != 0 {
		t.Errorf("direct endpoint hit %d times; expected 0 (should have gone via proxy)", *directHits)
	}
	if *proxyHits == 0 {
		t.Errorf("proxy server never received the request")
	}
	if len(resp.Models) != 1 || resp.Models[0].Name != "via-proxy" {
		t.Errorf("expected one model 'via-proxy', got %+v", resp.Models)
	}
}
