package pluginhost

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestProxyAttachForwardsToBackend is the routing/headers acceptance test:
// after proxy_attach, /plugin/{id}/invoke/x reaches the backend as /invoke/x
// carrying the gateway token and no browser Cookie/Origin headers.
func TestProxyAttachForwardsToBackend(t *testing.T) {
	a, router := newAssetsTestActor(t)

	type seen struct {
		path   string
		query  string
		token  string
		cookie string
		origin string
	}
	var mu sync.Mutex
	var got seen
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = seen{
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			token:  r.Header.Get("X-Spore-Gateway-Token"),
			cookie: r.Header.Get("Cookie"),
			origin: r.Header.Get("Origin"),
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(backend.Close)
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{
		PluginID: "app.demo",
		Addr:     backendAddr,
		Token:    "gateway-proxy.test-token",
	}); err != nil {
		t.Fatalf("proxy_attach: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/plugin/app.demo/invoke/x?n=1", strings.NewReader(`{"hello":1}`))
	req.Header.Set("Cookie", "spore_session=browser-cookie")
	req.Header.Set("Origin", "http://frontend.local")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if got.path != "/invoke/x" {
		t.Fatalf("backend path = %q, want /invoke/x", got.path)
	}
	if got.query != "n=1" {
		t.Fatalf("backend query = %q, want n=1", got.query)
	}
	if got.token != "gateway-proxy.test-token" {
		t.Fatalf("gateway token = %q, want injected token", got.token)
	}
	if got.cookie != "" {
		t.Fatalf("Cookie leaked to backend: %q", got.cookie)
	}
	if got.origin != "" {
		t.Fatalf("Origin leaked to backend: %q", got.origin)
	}
}

// TestProxyAttachValidation rejects incomplete attach requests.
func TestProxyAttachValidation(t *testing.T) {
	a, _ := newAssetsTestActor(t)
	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{Addr: "127.0.0.1:1", Token: "t"}); err == nil {
		t.Fatal("expected error for empty plugin id")
	}
	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{PluginID: "app.x", Token: "t"}); err == nil {
		t.Fatal("expected error for empty addr")
	}
	if _, err := a.handleProxyDetach(nil, gen.PluginProxyDetachReq{}); err == nil {
		t.Fatal("expected error for empty plugin id on detach")
	}
}

// sdkProxyEnv is a pluginhost actor + gateway server with one plugin attached
// to a real SDK HTTP listener whose cookie auth is armed. The gateway token
// is minted exactly the way appmanager will mint it (MintSessionToken with
// the "gateway-proxy" session id).
type sdkProxyEnv struct {
	a           *Actor
	gateway     *httptest.Server
	token       string
	backendAddr string
}

// sdkCaptureHost accepts every host call so EmitEvent's host-bridge path
// (app.emit) never fails during tests.
type sdkCaptureHost struct{}

func (sdkCaptureHost) Invoke(string, any) ([]byte, error) { return []byte(`{"Accepted":true}`), nil }

func (sdkCaptureHost) InvokeStream(string, any, func([]byte) error) ([]byte, error) {
	return []byte(`{"Accepted":true}`), nil
}

// newSDKProxyEnv starts a real SDK listener (T1 gateway-token auth active),
// attaches it to the gateway router, and returns the test environment. When
// the SDK copy in use predates T1 (workspace-mode builds resolve the SDK to
// the module cache; GOWORK=off resolves the in-repo ./sporemind-plugin-sdk),
// the minted token is rejected and the auth assertions are skipped — run with
// GOWORK=off to exercise them against the T1 SDK.
func newSDKProxyEnv(t *testing.T, pluginID string) *sdkProxyEnv {
	t.Helper()

	secretBytes := []byte("proxy-test-hmac-secret-" + pluginID)
	sdk.SetSessionSecret(hex.EncodeToString(secretBytes))
	t.Cleanup(func() { sdk.SetSessionSecret("") })

	srv, err := sdk.ServeHTTP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("sdk ServeHTTP: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		sdk.SetHost(nil)
	})

	sdk.RegisterHTTPHandler("proxytest.ping", func(payload json.RawMessage) (any, error) {
		return map[string]string{"pong": string(payload)}, nil
	})
	sdk.SetHost(sdkCaptureHost{})

	a, _ := newAssetsTestActor(t)
	gateway := httptest.NewServer(a.router)
	t.Cleanup(gateway.Close)

	env := &sdkProxyEnv{a: a, gateway: gateway, token: sdk.MintSessionToken(secretBytes, "gateway-proxy"), backendAddr: srv.Addr()}
	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{
		PluginID: pluginID, Addr: srv.Addr(), Token: env.token,
	}); err != nil {
		t.Fatalf("proxy_attach: %v", err)
	}
	return env
}

// gatewayAuthActive probes whether the SDK accepts the injected gateway
// token; false means the SDK copy predates T1.
func (e *sdkProxyEnv) gatewayAuthActive(t *testing.T, pluginID string) bool {
	t.Helper()
	resp, err := http.Post(e.gateway.URL+"/plugin/"+pluginID+"/invoke/proxytest.ping",
		"application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("probe invoke: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// TestProxyAttachRealSDKListener runs the proxy against a real SDK HTTP
// listener (T1's gateway-token auth active) to prove the injected
// X-Spore-Gateway-Token authorizes invoke through the gateway path and that a
// bogus token is rejected. Requires the worktree SDK copy (GOWORK=off).
func TestProxyAttachRealSDKListener(t *testing.T) {
	env := newSDKProxyEnv(t, "app.sdk")
	if !env.gatewayAuthActive(t, "app.sdk") {
		t.Skip("SDK copy in use predates T1 gateway-token auth; rerun with GOWORK=off against ./sporemind-plugin-sdk")
	}

	// Valid gateway token: invoke succeeds although the browser sends no
	// cookie and a foreign Origin — the proxy scrubs both and the SDK
	// accepts the token (Origin check skipped for gateway callers).
	req, err := http.NewRequest(http.MethodPost, env.gateway.URL+"/plugin/app.sdk/invoke/proxytest.ping",
		strings.NewReader(`{"n":1}`))
	if err != nil {
		t.Fatalf("build invoke: %v", err)
	}
	req.Header.Set("Origin", "http://frontend.local")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("invoke via gateway: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invoke code = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"pong"`) {
		t.Fatalf("invoke body = %s, want pong", body)
	}

	// A backend attached with a bogus token must be rejected by the SDK
	// (401 through the gateway), proving the token — not the gateway path
	// itself — is the authorization.
	if _, err := env.a.handleProxyAttach(nil, gen.PluginProxyAttachReq{
		PluginID: "app.bad", Addr: env.backendAddr, Token: "gateway-proxy.bogus",
	}); err != nil {
		t.Fatalf("proxy_attach bogus: %v", err)
	}
	resp2, err := http.Post(env.gateway.URL+"/plugin/app.bad/invoke/proxytest.ping",
		"application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("invoke bogus via gateway: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bogus token code = %d, want 401", resp2.StatusCode)
	}
}

// TestProxySSEImmediateDelivery proves FlushInterval=-1: an event emitted by
// the backend reaches the browser through /plugin/{id}/events without
// buffering delay.
func TestProxySSEImmediateDelivery(t *testing.T) {
	env := newSDKProxyEnv(t, "app.sse")
	if !env.gatewayAuthActive(t, "app.sse") {
		t.Skip("SDK copy in use predates T1 gateway-token auth; rerun with GOWORK=off against ./sporemind-plugin-sdk")
	}

	resp, err := http.Get(env.gateway.URL + "/plugin/app.sse/events")
	if err != nil {
		t.Fatalf("events via gateway: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events code = %d, want 200", resp.StatusCode)
	}
	defer resp.Body.Close()

	lineCh := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lineCh <- sc.Text()
		}
	}()

	// StatusCode 200 already confirms the stream is open through the proxy.
	// Emit immediately: with proxy buffering (FlushInterval != -1) the event
	// would sit in the flush window and miss the deadline below.
	if err := sdk.EmitEvent("proxytest.tick", map[string]int{"n": 42}); err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}

	timeout := time.After(2 * time.Second)
	var gotEvent, gotData bool
	for !(gotEvent && gotData) {
		select {
		case line := <-lineCh:
			if strings.HasPrefix(line, "event: proxytest.tick") {
				gotEvent = true
			}
			if strings.HasPrefix(line, "data:") && strings.Contains(line, `"n":42`) {
				gotData = true
			}
		case <-timeout:
			t.Fatal("SSE event not delivered through gateway within 2s (FlushInterval buffering?)")
		}
	}
}

// TestProxyDetachFallsBackToAssets covers the detach semantics: with a
// persisted asset bundle the route falls back to static assets; without one
// the route 404s.
func TestProxyDetachFallsBackToAssets(t *testing.T) {
	a, router := newAssetsTestActor(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("backend"))
	}))
	t.Cleanup(backend.Close)
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	// Assets first, proxy on top (attach replaces the assets handler).
	if _, err := a.handleAssetsPut(nil, gen.PluginAssetsPutReq{
		PluginID: "app.fallback",
		Assets:   map[string][]byte{"index.html": []byte("<html>assets</html>")},
	}); err != nil {
		t.Fatalf("assets_put: %v", err)
	}
	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{
		PluginID: "app.fallback", Addr: backendAddr, Token: "t",
	}); err != nil {
		t.Fatalf("proxy_attach: %v", err)
	}
	if rec := doPluginGet(t, router, "/plugin/app.fallback/index.html"); rec.Code != http.StatusOK ||
		rec.Body.String() != "backend" {
		t.Fatalf("attached: code = %d body = %q, want proxied backend", rec.Code, rec.Body.String())
	}

	// Detach → assets handler back as the fallback.
	if _, err := a.handleProxyDetach(nil, gen.PluginProxyDetachReq{PluginID: "app.fallback"}); err != nil {
		t.Fatalf("proxy_detach: %v", err)
	}
	if rec := doPluginGet(t, router, "/plugin/app.fallback/index.html"); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "assets") {
		t.Fatalf("after detach: code = %d body = %q, want assets fallback", rec.Code, rec.Body.String())
	}

	// No assets → detach drops the route entirely (404).
	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{
		PluginID: "app.bare", Addr: backendAddr, Token: "t",
	}); err != nil {
		t.Fatalf("proxy_attach bare: %v", err)
	}
	if rec := doPluginGet(t, router, "/plugin/app.bare/invoke/x"); rec.Code != http.StatusOK {
		t.Fatalf("attached bare: code = %d, want 200", rec.Code)
	}
	if _, err := a.handleProxyDetach(nil, gen.PluginProxyDetachReq{PluginID: "app.bare"}); err != nil {
		t.Fatalf("proxy_detach bare: %v", err)
	}
	if rec := doPluginGet(t, router, "/plugin/app.bare/invoke/x"); rec.Code != http.StatusNotFound {
		t.Fatalf("after detach bare: code = %d, want 404", rec.Code)
	}

	// Detach is idempotent for plugins without an attached proxy.
	if _, err := a.handleProxyDetach(nil, gen.PluginProxyDetachReq{PluginID: "app.bare"}); err != nil {
		t.Fatalf("second detach: %v", err)
	}
}

// TestProxyDetachOnProcessStateReport wires the lifecycle seam: a crash
// (or unload stop) reported by the transport detaches the proxy so the
// gateway route falls back to assets instead of proxying to a dead backend.
func TestProxyDetachOnProcessStateReport(t *testing.T) {
	a, router := newAssetsTestActor(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("backend"))
	}))
	t.Cleanup(backend.Close)
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	if _, err := a.handleAssetsPut(nil, gen.PluginAssetsPutReq{
		PluginID: "app.crash",
		Assets:   map[string][]byte{"index.html": []byte("<html>assets</html>")},
	}); err != nil {
		t.Fatalf("assets_put: %v", err)
	}
	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{
		PluginID: "app.crash", Addr: backendAddr, Token: "t",
	}); err != nil {
		t.Fatalf("proxy_attach: %v", err)
	}

	// "running" must NOT detach (appmanager owns attach with the token).
	a.ReportProcessState("app.crash", "running", "", "", 0)
	if rec := doPluginGet(t, router, "/plugin/app.crash/index.html"); rec.Body.String() != "backend" {
		t.Fatalf("after running report: body = %q, want proxied backend", rec.Body.String())
	}

	// Crash detaches; assets fall back.
	a.ReportProcessState("app.crash", "crashed", "session read: EOF", "", 0)
	if rec := doPluginGet(t, router, "/plugin/app.crash/index.html"); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "assets") {
		t.Fatalf("after crash report: code = %d body = %q, want assets fallback", rec.Code, rec.Body.String())
	}

	// Re-attach then explicit stop report: same detach semantics.
	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{
		PluginID: "app.crash", Addr: backendAddr, Token: "t",
	}); err != nil {
		t.Fatalf("re-attach: %v", err)
	}
	a.ReportProcessState("app.crash", "stopped", "", "", 0)
	if rec := doPluginGet(t, router, "/plugin/app.crash/index.html"); !strings.Contains(rec.Body.String(), "assets") {
		t.Fatalf("after stop report: body = %q, want assets fallback", rec.Body.String())
	}
}

// TestProxyUnknownPlugin404 guards the router contract for plugins with
// neither an attached proxy nor an assets bundle.
func TestProxyUnknownPlugin404(t *testing.T) {
	_, router := newAssetsTestActor(t)
	for _, path := range []string{
		"/plugin/never.attached/invoke/x",
		"/plugin/never.attached/events",
		"/plugin/never.attached/index.html",
	} {
		if rec := doPluginGet(t, router, path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: code = %d, want 404", path, rec.Code)
		}
	}
}

// TestProxyAttachReplacesOldBackend covers reload semantics: attaching a new
// addr for the same plugin replaces the previous proxy (no stale backend).
func TestProxyAttachReplacesOldBackend(t *testing.T) {
	a, router := newAssetsTestActor(t)
	mkBackend := func(tag string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, tag)
		}))
		t.Cleanup(srv.Close)
		return strings.TrimPrefix(srv.URL, "http://")
	}
	oldAddr, newAddr := mkBackend("old"), mkBackend("new")

	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{PluginID: "app.swap", Addr: oldAddr, Token: "t1"}); err != nil {
		t.Fatalf("attach old: %v", err)
	}
	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{PluginID: "app.swap", Addr: newAddr, Token: "t2"}); err != nil {
		t.Fatalf("attach new: %v", err)
	}
	if rec := doPluginGet(t, router, "/plugin/app.swap/index.html"); rec.Body.String() != "new" {
		t.Fatalf("after re-attach: body = %q, want new backend", rec.Body.String())
	}
	if a.proxies["app.swap"].token != "t2" {
		t.Fatalf("attach table token = %q, want t2", a.proxies["app.swap"].token)
	}
}
