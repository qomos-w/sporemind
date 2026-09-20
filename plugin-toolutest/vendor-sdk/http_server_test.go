package sdk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serveTestHTTP starts an HTTP listener on an ephemeral port (no secret, dev
// mode) and registers cleanup that shuts it down and resets the shared
// package-level state so tests do not leak handlers, secrets, or a stale
// server into each other.
func serveTestHTTP(t *testing.T) *HTTPServer {
	t.Helper()
	srv, err := ServeHTTP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		SetSessionSecret("")
		clearHTTPHandlers()
		clearFileDropState()
		SetHost(nil)
	})
	return srv
}

func baseURL(srv *HTTPServer) string { return "http://" + srv.Addr() }

// postJSON issues a POST with the given body and returns the status, body
// bytes, and any error. The body string is sent as-is (assumed valid JSON).
func postJSON(t *testing.T, url, body string, cookie *http.Cookie) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, b
}

func TestHTTPInvokeDispatch(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)

	RegisterHTTPHandler("echo", func(payload json.RawMessage) (any, error) {
		return map[string]string{"got": string(payload)}, nil
	})
	RegisterHTTPHandler("boom", func(payload json.RawMessage) (any, error) {
		return nil, errors.New("kaboom")
	})

	// Happy path: handler receives the raw JSON body, returns a value that is
	// JSON-encoded into the response.
	code, body := postJSON(t, base+"/invoke/echo", `{"x":1}`, nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", code, body)
	}
	var resp map[string]string
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, body)
	}
	if resp["got"] != `{"x":1}` {
		t.Fatalf("response = %q, want {\"got\":\"{\\\"x\\\":1}\"}", resp["got"])
	}
	if ct := contentType(t, base, "POST", "/invoke/echo"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}

	// Unknown callable -> 404.
	code, body = postJSON(t, base+"/invoke/missing", `{}`, nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown callable status = %d, want 404; body=%s", code, body)
	}

	// Handler error -> 500 with the message.
	code, body = postJSON(t, base+"/invoke/boom", `{}`, nil)
	if code != http.StatusInternalServerError {
		t.Fatalf("handler error status = %d, want 500; body=%s", code, body)
	}
	if !strings.Contains(string(body), "kaboom") {
		t.Fatalf("error body = %q, want to contain kaboom", body)
	}
}

// TestHostEventDeliveryMirrorsToSSE pins the local-mirror behavior of
// RegisterEventListener: a host-delivered event (reverse invoke under
// `__event__:<kind>`) reaches the registered Go handler AND is broadcast on
// the plugin's own /events SSE stream so panel frontends can subscribe to
// listen kinds — no host round-trip on the mirror path.
func TestHostEventDeliveryMirrorsToSSE(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)

	var gotStep string
	RegisterEventListener("step", func(payload json.RawMessage) error {
		var ev struct {
			StepID string `json:"StepId"`
			Delta  string `json:"Delta"`
		}
		if err := json.Unmarshal(payload, &ev); err != nil {
			return err
		}
		gotStep = ev.StepID + "|" + ev.Delta
		return nil
	})
	t.Cleanup(func() {
		unregisterCallable("", EventCallID("step"))
	})

	c1, cancel1 := connectSSE(t, base)
	defer cancel1()
	waitForConns(t, srv, 1)

	raw := []byte(`{"Kind":"step","StepId":"s1","TurnId":"t1","Delta":"hi"}`)
	if _, err := Dispatch("", EventCallID("step"), raw); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	select {
	case ev := <-c1:
		if ev.kind != "step" {
			t.Errorf("SSE kind = %q, want step", ev.kind)
		}
		if !strings.Contains(ev.data, `"StepId":"s1"`) || !strings.Contains(ev.data, `"Delta":"hi"`) {
			t.Errorf("SSE data = %q, want StepId s1 / Delta hi", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE mirror of host-delivered event")
	}
	if gotStep != "s1|hi" {
		t.Errorf("handler saw %q, want s1|hi", gotStep)
	}
}

func contentType(t *testing.T, base, method, path string) string {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.Header.Get("Content-Type")
}

// TestEmitEventDualPath verifies EmitEvent fans the event out over local SSE
// (both connected frontends receive it) AND forwards it to the host bridge
// (app.emit). Two clients connect, EmitEvent fires once, both receive the
// event with the correct kind and data; the host received exactly one app.emit
// call carrying the marshaled payload.
func TestEmitEventDualPath(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)

	var hostCalls int
	var lastEvent string
	var lastPayload []byte
	SetHost(&captureHost{invoke: func(callID string, payload any) ([]byte, error) {
		hostCalls++
		req, ok := payload.(appEmitReq)
		if !ok || callID != "app.emit" {
			t.Errorf("host call = (%s, %T), want (app.emit, appEmitReq)", callID, payload)
		}
		lastEvent = req.Event
		lastPayload = req.Payload
		return []byte(`{"Accepted":true}`), nil
	}})

	c1, cancel1 := connectSSE(t, base)
	c2, cancel2 := connectSSE(t, base)
	defer cancel1()
	defer cancel2()
	waitForConns(t, srv, 2)

	if err := EmitEvent("todo_changed", map[string]any{"done": true}); err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}

	for _, ch := range []chan sseEvent{c1, c2} {
		select {
		case ev := <-ch:
			if ev.kind != "todo_changed" {
				t.Errorf("event kind = %q, want todo_changed", ev.kind)
			}
			if ev.data != `{"done":true}` {
				t.Errorf("event data = %q, want {\"done\":true}", ev.data)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for SSE event")
		}
	}

	if hostCalls != 1 {
		t.Fatalf("host app.emit calls = %d, want 1", hostCalls)
	}
	if lastEvent != "todo_changed" {
		t.Errorf("host event = %q, want todo_changed", lastEvent)
	}
	if string(lastPayload) != `{"done":true}` {
		t.Errorf("host payload = %q, want {\"done\":true}", lastPayload)
	}
}

// TestSSEBroadcastMultiConn connects two clients, broadcasts, then
// disconnects one and broadcasts again: the remaining client still receives
// and the broadcaster never errors (slow/disconnected cleanup).
func TestSSEBroadcastMultiConn(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	// captureHost accepts everything so EmitEvent's host path does not fail.
	SetHost(&captureHost{invoke: func(string, any) ([]byte, error) {
		return []byte(`{"Accepted":true}`), nil
	}})

	c1, cancel1 := connectSSE(t, base)
	c2, cancel2 := connectSSE(t, base)
	waitForConns(t, srv, 2)

	if err := EmitEvent("first", nil); err != nil {
		t.Fatalf("EmitEvent first: %v", err)
	}
	for _, ch := range []chan sseEvent{c1, c2} {
		select {
		case ev := <-ch:
			if ev.kind != "first" {
				t.Errorf("first kind = %q, want first", ev.kind)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for first event")
		}
	}

	// Disconnect c2: the hub should drop it on the next context cancel.
	cancel2()
	waitForConns(t, srv, 1)

	// Broadcast again: only c1 receives; no error, no hang.
	if err := EmitEvent("second", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("EmitEvent second: %v", err)
	}
	select {
	case ev := <-c1:
		if ev.kind != "second" || ev.data != `{"k":"v"}` {
			t.Errorf("second event = %+v, want kind=second data={\"k\":\"v\"}", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("c1 did not receive second event after c2 disconnected")
	}
	// c2 channel should have nothing new (it was cancelled).
	select {
	case <-c2:
		t.Fatal("c2 received an event after disconnect")
	default:
	}
	cancel1()
}

type sseEvent struct {
	kind string
	data string
}

// connectSSE opens a GET /events stream and returns a channel of decoded SSE
// events plus a cancel func. The goroutine parses multi-line data fields.
func connectSSE(t *testing.T, base string) (chan sseEvent, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sse connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("sse connect status = %d, want 200", resp.StatusCode)
	}
	events := make(chan sseEvent, 8)
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.kind = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if ev.data != "" {
					ev.data += "\n"
				}
				ev.data += strings.TrimPrefix(line, "data: ")
			case line == "":
				if ev.kind != "" || ev.data != "" {
					events <- ev
					ev = sseEvent{}
				}
			}
		}
	}()
	return events, cancel
}

func waitForConns(t *testing.T, srv *HTTPServer, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Hub().count() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d sse connections, have %d", want, srv.Hub().count())
}

// TestHTTPCookieAuth covers cookie rejection: with a secret configured, a
// request without a cookie or with a tampered cookie is rejected (401) on
// both /invoke and /events; a validly-minted token is accepted.
func TestHTTPCookieAuth(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	RegisterHTTPHandler("ping", func(payload json.RawMessage) (any, error) {
		return "pong", nil
	})

	secret := "test-hmac-secret-key"
	SetSessionSecret(secret)
	secretBytes := sessionSecret()
	if len(secretBytes) == 0 {
		t.Fatal("sessionSecret not set")
	}
	valid := MintSessionToken(secretBytes, "sess-1")

	// No cookie -> 401 on /invoke.
	code, _ := postJSON(t, base+"/invoke/ping", `{}`, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("no-cookie /invoke status = %d, want 401", code)
	}

	// Tampered cookie -> 401 on /invoke.
	bad := &http.Cookie{Name: SessionCookieName, Value: valid + "x"}
	code, _ = postJSON(t, base+"/invoke/ping", `{}`, bad)
	if code != http.StatusUnauthorized {
		t.Fatalf("tampered /invoke status = %d, want 401", code)
	}

	// Malformed cookie (no dot) -> 401.
	malformed := &http.Cookie{Name: SessionCookieName, Value: "no-dot-here"}
	code, _ = postJSON(t, base+"/invoke/ping", `{}`, malformed)
	if code != http.StatusUnauthorized {
		t.Fatalf("malformed /invoke status = %d, want 401", code)
	}

	// Valid cookie -> 200.
	good := &http.Cookie{Name: SessionCookieName, Value: valid}
	code, body := postJSON(t, base+"/invoke/ping", `{}`, good)
	if code != http.StatusOK {
		t.Fatalf("valid /invoke status = %d, want 200; body=%s", code, body)
	}

	// No cookie on /events -> 401.
	req, _ := http.NewRequest(http.MethodGet, base+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("events no-cookie: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-cookie /events status = %d, want 401", resp.StatusCode)
	}
}

// TestHTTPOriginGuard covers the defense-in-depth Origin check layered on
// top of the cookie: a request whose Origin header names a different origin
// is rejected (403) even with a valid cookie, on /invoke, /events, and the
// /session/bootstrap cookie-installing endpoint. Same-origin requests and
// requests with no Origin header (top-level navigations) pass. The guard is
// inert in dev mode (no secret configured).
func TestHTTPOriginGuard(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	RegisterHTTPHandler("ping", func(payload json.RawMessage) (any, error) {
		return "pong", nil
	})

	secret := "origin-guard-secret"
	SetSessionSecret(secret)
	secretBytes := sessionSecret()
	valid := MintSessionToken(secretBytes, "sess-origin")
	good := &http.Cookie{Name: SessionCookieName, Value: valid}

	// Same-origin /invoke (Origin matches the listener host) -> 200.
	sameReq, _ := http.NewRequest(http.MethodPost, base+"/invoke/ping", strings.NewReader(`{}`))
	sameReq.Header.Set("Content-Type", "application/json")
	sameReq.Header.Set("Origin", base) // http://127.0.0.1:<port> == own origin
	sameReq.AddCookie(good)
	resp, err := http.DefaultClient.Do(sameReq)
	if err != nil {
		t.Fatalf("same-origin invoke: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("same-origin /invoke status = %d, want 200", resp.StatusCode)
	}

	// Cross-origin /invoke (Origin names a foreign host) -> 403, even with
	// a valid cookie. SameSite=Lax is the cookie-layer protection; this is
	// defense-in-depth.
	crossReq, _ := http.NewRequest(http.MethodPost, base+"/invoke/ping", strings.NewReader(`{}`))
	crossReq.Header.Set("Content-Type", "application/json")
	crossReq.Header.Set("Origin", "http://evil.example")
	crossReq.AddCookie(good)
	resp, err = http.DefaultClient.Do(crossReq)
	if err != nil {
		t.Fatalf("cross-origin invoke: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin /invoke status = %d, want 403", resp.StatusCode)
	}

	// Cross-origin /events (EventSource-equivalent GET) -> 403.
	evReq, _ := http.NewRequest(http.MethodGet, base+"/events", nil)
	evReq.Header.Set("Origin", "http://evil.example")
	evReq.AddCookie(good)
	resp, err = http.DefaultClient.Do(evReq)
	if err != nil {
		t.Fatalf("cross-origin events: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin /events status = %d, want 403", resp.StatusCode)
	}

	// Cross-origin /session/bootstrap with a valid token -> 403 (no cookie set).
	bsReq, _ := http.NewRequest(http.MethodPost, base+"/session/bootstrap", strings.NewReader(`{"token":"`+valid+`"}`))
	bsReq.Header.Set("Content-Type", "application/json")
	bsReq.Header.Set("Origin", "http://evil.example")
	resp, err = http.DefaultClient.Do(bsReq)
	if err != nil {
		t.Fatalf("cross-origin bootstrap: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin /session/bootstrap status = %d, want 403", resp.StatusCode)
	}
	if findSetCookie(resp, SessionCookieName) != nil {
		t.Fatal("cross-origin bootstrap must not set a cookie")
	}

	// No Origin header (top-level navigation / plain GET) -> still gated by
	// the cookie, not the Origin check.
	noOriginReq, _ := http.NewRequest(http.MethodPost, base+"/invoke/ping", strings.NewReader(`{}`))
	noOriginReq.Header.Set("Content-Type", "application/json")
	noOriginReq.AddCookie(good)
	resp, err = http.DefaultClient.Do(noOriginReq)
	if err != nil {
		t.Fatalf("no-origin invoke: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-origin /invoke status = %d, want 200", resp.StatusCode)
	}

	// Dev mode (no secret): cross-origin Origin is not enforced.
	SetSessionSecret("")
	crossReq2, _ := http.NewRequest(http.MethodPost, base+"/invoke/ping", strings.NewReader(`{}`))
	crossReq2.Header.Set("Content-Type", "application/json")
	crossReq2.Header.Set("Origin", "http://evil.example")
	resp, err = http.DefaultClient.Do(crossReq2)
	if err != nil {
		t.Fatalf("dev-mode cross-origin invoke: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dev-mode cross-origin /invoke status = %d, want 200", resp.StatusCode)
	}
}

// TestHTTPStaticFile covers GET / serving files from the configured app dir,
// including a 404 for a missing file (same-origin static assets).
func TestHTTPStaticFile404(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hello</h1>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	srv, err := ServeHTTP("127.0.0.1:0", WithStaticDir(dir))
	if err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	})
	base := baseURL(srv)

	resp, err := http.Get(base + "/index.html")
	if err != nil {
		t.Fatalf("get index.html: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index.html status = %d, want 200", resp.StatusCode)
	}

	resp2, err := http.Get(base + "/this-file-does-not-exist.html")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("missing file status = %d, want 404", resp2.StatusCode)
	}
}

// TestHTTPStaticBootstrapSnippet covers the direct-HTTP management plane:
// every served HTML document carries the bridge bootstrap snippet (so the
// host shell can attach console/DOM/theme management), while non-HTML assets
// are served untouched.
func TestHTTPStaticBootstrapSnippet(t *testing.T) {
	dir := t.TempDir()
	html := `<html><head><title>t</title></head><body>hello</body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}
	srv, err := ServeHTTP("127.0.0.1:0", WithStaticDir(dir))
	if err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	})
	base := baseURL(srv)

	for _, p := range []string{"/", "/index.html"} {
		resp, err := http.Get(base + p)
		if err != nil {
			t.Fatalf("get %s: %v", p, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", p, resp.StatusCode)
		}
		if !strings.Contains(string(body), "sporemind:ready-for-bootstrap") {
			t.Fatalf("%s: HTML missing bridge bootstrap snippet", p)
		}
		if !strings.Contains(string(body), "hello") {
			t.Fatalf("%s: original content lost", p)
		}
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("%s: content-type = %q, want text/html", p, resp.Header.Get("Content-Type"))
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: cache-control = %q, want no-store", p, resp.Header.Get("Cache-Control"))
		}
	}

	// Non-HTML assets pass through without injection.
	resp, err := http.Get(base + "/app.js")
	if err != nil {
		t.Fatalf("get app.js: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "sporemind:ready-for-bootstrap") {
		t.Fatal("app.js must not contain the bridge snippet")
	}

	// HEAD on the entry document: headers, no body.
	req, _ := http.NewRequest(http.MethodHead, base+"/", nil)
	headResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("head /: %v", err)
	}
	headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD / status = %d, want 200", headResp.StatusCode)
	}
	if ct := headResp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("HEAD / content-type = %q, want text/html", ct)
	}
}

// TestHTTPSessionBootstrap covers the cookie bootstrap: the iframe posts the
// host-minted token here and gets back an HttpOnly cookie that authorizes
// the direct-HTTP data path (/invoke, /events, media tags).
func TestHTTPSessionBootstrap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	srv, err := ServeHTTP("127.0.0.1:0", WithStaticDir(dir))
	if err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	})
	base := baseURL(srv)

	// Deterministic secret state: start auth-disabled for the dev-mode leg,
	// then enable it; always leave the package-level secret cleared behind.
	SetSessionSecret("")
	t.Cleanup(func() { SetSessionSecret("") })

	RegisterHTTPHandler("ping", func(payload json.RawMessage) (any, error) {
		return "pong", nil
	})

	// Auth disabled (dev mode): any token is accepted and set.
	code, resp := postBootstrap(t, base, "anything.goes")
	if code != http.StatusNoContent {
		t.Fatalf("dev-mode bootstrap status = %d, want 204", code)
	}
	if c := findSetCookie(resp, SessionCookieName); c == nil || c.Value != "anything.goes" || !c.HttpOnly {
		t.Fatalf("dev-mode Set-Cookie = %+v, want HttpOnly spore_session=anything.goes", c)
	}

	// With a secret configured: wrong token rejected, no cookie.
	SetSessionSecret("bootstrap-secret")
	secretBytes := sessionSecret()
	valid := MintSessionToken(secretBytes, "sess-1")

	code, resp = postBootstrap(t, base, "forged.token")
	if code != http.StatusUnauthorized {
		t.Fatalf("forged bootstrap status = %d, want 401", code)
	}
	if findSetCookie(resp, SessionCookieName) != nil {
		t.Fatal("forged bootstrap must not set a cookie")
	}

	// Valid token: 204 + HttpOnly SameSite cookie; the cookie then
	// authorizes /invoke.
	code, resp = postBootstrap(t, base, valid)
	if code != http.StatusNoContent {
		t.Fatalf("valid bootstrap status = %d, want 204", code)
	}
	cookie := findSetCookie(resp, SessionCookieName)
	if cookie == nil || cookie.Value != valid || !cookie.HttpOnly {
		t.Fatalf("valid Set-Cookie = %+v, want HttpOnly spore_session", cookie)
	}
	code, _ = postJSON(t, base+"/invoke/ping", `{}`, cookie)
	if code != http.StatusOK {
		t.Fatalf("post-bootstrap /invoke status = %d, want 200", code)
	}

	// Malformed body: no token to validate — with auth enabled that is a
	// rejection, not a silent pass.
	req, _ := http.NewRequest(http.MethodPost, base+"/session/bootstrap", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	mResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("malformed bootstrap: %v", err)
	}
	mResp.Body.Close()
	if mResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("malformed bootstrap status = %d, want 401", mResp.StatusCode)
	}
	if findSetCookie(mResp, SessionCookieName) != nil {
		t.Fatal("malformed bootstrap must not set a cookie")
	}
}

func postBootstrap(t *testing.T, base, token string) (int, *http.Response) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := http.Post(base+"/session/bootstrap", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("bootstrap post: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp
}

func findSetCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestSessionToken unit-covers mint/verify including tampering and malformed
// inputs, independent of the HTTP layer.
func TestSessionToken(t *testing.T) {
	secret := []byte("super-secret")
	token := MintSessionToken(secret, "session-42")
	if !validSessionToken(secret, token) {
		t.Fatal("minted token failed validation")
	}
	// Round-trip reconstructs the same token.
	if MintSessionToken(secret, "session-42") != token {
		t.Fatal("mint is not deterministic")
	}
	// Wrong secret rejects.
	if validSessionToken([]byte("other-secret"), token) {
		t.Fatal("token validated with wrong secret")
	}
	// Tampered session id rejects.
	idx := strings.IndexByte(token, '.')
	tampered := "evil." + token[idx+1:]
	if validSessionToken(secret, tampered) {
		t.Fatal("tampered session id validated")
	}
	// Tampered MAC rejects.
	tamperedMAC := token[:idx+1] + "deadbeef"
	if validSessionToken(secret, tamperedMAC) {
		t.Fatal("tampered mac validated")
	}
	// Malformed (no dot) rejects.
	if validSessionToken(secret, "nodothere") {
		t.Fatal("token without dot validated")
	}
	// Malformed hex rejects.
	if validSessionToken(secret, "id.not-hex!!") {
		t.Fatal("non-hex mac validated")
	}
}

// TestOnLoadConfigAppliesSecretAndAutoStartsServer exercises the OnLoad config
// contract end to end through HandleOnLoad: a host-pushed {httpAddr,
// sessionSecret} config installs the secret and starts an HTTP listener whose
// bound address is observable. Unload stops the listener.
func TestOnLoadConfigAppliesSecretAndAutoStartsServer(t *testing.T) {
	Register(&Plugin{
		Manifest: Manifest{ID: "app.http.cfg", Name: "HTTP Cfg", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			// Simulate generated server.gen.go registering an HTTP handler.
			RegisterHTTPHandler("health", func(payload json.RawMessage) (any, error) {
				return map[string]string{"status": "ok"}, nil
			})
			return nil
		},
	})
	t.Cleanup(func() {
		unregisterPluginCallables("app.http.cfg")
	})

	// No running server before load.
	if srv := currentHTTPServer(); srv != nil {
		t.Fatalf("server already running before load: %s", srv.Addr())
	}
	if len(sessionSecret()) != 0 {
		t.Fatal("secret set before load")
	}

	cfg := cstr(`{"httpAddr":"127.0.0.1:0","sessionSecret":"abc123"}`)
	id := cstr("app.http.cfg")
	if got := HandleOnLoad(id, cfg); got != 0 {
		t.Fatalf("HandleOnLoad = %d, want 0", got)
	}

	srv := currentHTTPServer()
	if srv == nil {
		t.Fatal("HTTP listener was not started by OnLoad config")
	}
	if len(sessionSecret()) == 0 {
		t.Fatal("session secret not applied from config")
	}
	// The registered handler is reachable (auto-start ran after OnLoad).
	code, body := postJSON(t, "http://"+srv.Addr()+"/invoke/health", `{}`, nil)
	// secret is set -> dev mode OFF -> no cookie -> 401, proving auth active.
	if code != http.StatusUnauthorized {
		t.Fatalf("health without cookie status = %d, want 401 (auth active); body=%s", code, body)
	}
	// With a valid cookie, the handler responds.
	token := MintSessionToken(sessionSecret(), "cfg-sess")
	cookie := &http.Cookie{Name: SessionCookieName, Value: token}
	code, body = postJSON(t, "http://"+srv.Addr()+"/invoke/health", `{}`, cookie)
	if code != http.StatusOK {
		t.Fatalf("health with cookie status = %d, want 200; body=%s", code, body)
	}

	// Unload must stop the listener.
	if got := HandleOnUnload(id); got != 0 {
		t.Fatalf("HandleOnUnload = %d, want 0", got)
	}
	if srv := currentHTTPServer(); srv != nil {
		t.Fatalf("server still running after unload: %s", srv.Addr())
	}
}

// TestApplyLoadConfigBackwardCompatible pins that empty/null/blank config
// (what every host sends today) is a no-op: no secret, no auto-start.
func TestApplyLoadConfigBackwardCompatible(t *testing.T) {
	cases := []json.RawMessage{nil, []byte(""), []byte("null"), []byte("   ")}
	for i, raw := range cases {
		if got := applyLoadConfig(raw); got != "" {
			t.Errorf("case %d: pendingHTTPAddr = %q, want empty", i, got)
		}
	}
}

// TestApplyLoadConfigDataDir pins the app.data grant surface: a config with
// dataDir populates DataDir() for the app's own storage (files, embedded
// databases), and configs without the field leave the getter empty.
func TestApplyLoadConfigDataDir(t *testing.T) {
	t.Cleanup(func() { dataDir.Store("") })
	if got := applyLoadConfig(json.RawMessage(`{"dataDir":"D/app/.sporecode/appdata"}`)); got != "" {
		t.Fatalf("pendingHTTPAddr = %q, want empty", got)
	}
	if got := DataDir(); got != "D/app/.sporecode/appdata" {
		t.Fatalf("DataDir() = %q, want the granted appdata dir", got)
	}
	dataDir.Store("")
	_ = applyLoadConfig(json.RawMessage(`{"httpAddr":"127.0.0.1:0"}`))
	if got := DataDir(); got != "" {
		t.Fatalf("DataDir() = %q after a config without the grant, want empty", got)
	}
}

// TestServeHTTPIdempotentWhileRunning: a second ServeHTTP while one is running
// returns the existing server (no double-bind, no error).
func TestServeHTTPIdempotentWhileRunning(t *testing.T) {
	srv := serveTestHTTP(t)
	srv2, err := ServeHTTP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("second ServeHTTP: %v", err)
	}
	if srv2 != srv {
		t.Fatalf("second ServeHTTP returned %p, want the running server %p", srv2, srv)
	}
}

// gatewayTestServer starts a listener serving a temp dir with index.html and
// app.js, and registers full cleanup (shutdown + secret reset + handler
// clear) so the shared package-level state does not leak between tests.
func gatewayTestServer(t *testing.T) (*HTTPServer, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hello</h1>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}
	srv, err := ServeHTTP("127.0.0.1:0", WithStaticDir(dir))
	if err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		SetSessionSecret("")
		clearHTTPHandlers()
		clearFileDropState()
		SetHost(nil)
	})
	return srv, baseURL(srv)
}

// getResource issues a GET with optional headers and cookie, returning the
// status and body bytes.
func getResource(t *testing.T, url string, headers map[string]string, cookie *http.Cookie) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, b
}

// postWithHeaders issues a POST /invoke with optional headers and cookie.
func postWithHeaders(t *testing.T, url, body string, headers map[string]string, cookie *http.Cookie) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, b
}

// TestHTTPGatewayTokenAuth covers the trusted gateway-proxy caller: a valid
// X-Spore-Gateway-Token authorizes /invoke, /events, and static assets with
// no cookie, and skips the Origin check entirely (the gateway's forwarded
// Origin is unrelated to the plugin's own origin). A forged or malformed
// token is rejected with 401 on both /invoke and static.
func TestHTTPGatewayTokenAuth(t *testing.T) {
	_, base := gatewayTestServer(t)
	RegisterHTTPHandler("ping", func(payload json.RawMessage) (any, error) {
		return "pong", nil
	})

	SetSessionSecret("gateway-secret")
	gw := map[string]string{GatewayTokenHeader: MintSessionToken(sessionSecret(), "gateway-proxy")}

	// /invoke with a valid gateway header, no cookie -> 200.
	code, body := postWithHeaders(t, base+"/invoke/ping", `{}`, gw, nil)
	if code != http.StatusOK {
		t.Fatalf("gateway /invoke status = %d, want 200; body=%s", code, body)
	}

	// /events with a valid gateway header, no cookie -> 200 (stream attaches).
	evReq, err := http.NewRequest(http.MethodGet, base+"/events", nil)
	if err != nil {
		t.Fatalf("new events request: %v", err)
	}
	evReq.Header.Set(GatewayTokenHeader, gw[GatewayTokenHeader])
	evResp, err := http.DefaultClient.Do(evReq)
	if err != nil {
		t.Fatalf("gateway /events: %v", err)
	}
	if evResp.StatusCode != http.StatusOK {
		evResp.Body.Close()
		t.Fatalf("gateway /events status = %d, want 200", evResp.StatusCode)
	}
	evResp.Body.Close()

	// GET / (static) with a valid gateway header, no cookie -> 200.
	code, body = getResource(t, base+"/", gw, nil)
	if code != http.StatusOK {
		t.Fatalf("gateway static / status = %d, want 200; body=%s", code, body)
	}
	if !strings.Contains(string(body), "hello") {
		t.Fatalf("gateway static / body lost content: %s", body)
	}

	// Gateway header + cross-origin Origin: the gateway is a trusted
	// internal caller, so its forwarded Origin must NOT be checked -> 200.
	cross := map[string]string{
		GatewayTokenHeader: gw[GatewayTokenHeader],
		"Origin":           "http://foreign-gateway-host.example",
	}
	code, body = postWithHeaders(t, base+"/invoke/ping", `{}`, cross, nil)
	if code != http.StatusOK {
		t.Fatalf("gateway cross-origin /invoke status = %d, want 200 (Origin exempt); body=%s", code, body)
	}
	code, body = getResource(t, base+"/", cross, nil)
	if code != http.StatusOK {
		t.Fatalf("gateway cross-origin static status = %d, want 200; body=%s", code, body)
	}

	// Forged gateway token (valid shape, wrong secret) -> 401 on /invoke
	// and static.
	forged := map[string]string{GatewayTokenHeader: MintSessionToken([]byte("other-secret"), "gateway-proxy")}
	code, _ = postWithHeaders(t, base+"/invoke/ping", `{}`, forged, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("forged gateway /invoke status = %d, want 401", code)
	}
	code, _ = getResource(t, base+"/", forged, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("forged gateway static status = %d, want 401", code)
	}

	// Malformed gateway token -> 401 on /invoke and static.
	malformed := map[string]string{GatewayTokenHeader: "no-dot-here"}
	code, _ = postWithHeaders(t, base+"/invoke/ping", `{}`, malformed, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("malformed gateway /invoke status = %d, want 401", code)
	}
	code, _ = getResource(t, base+"/", malformed, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("malformed gateway static status = %d, want 401", code)
	}
}

// TestHTTPStaticAuthLockdown covers the static-asset closure: with a secret
// configured, a request without a gateway header or cookie is rejected with
// 401; a valid session cookie still authorizes (the direct-HTTP iframe
// path); with no secret (dev mode) static stays public.
func TestHTTPStaticAuthLockdown(t *testing.T) {
	_, base := gatewayTestServer(t)

	// Secret configured, no header, no cookie: static is no longer public.
	SetSessionSecret("static-lockdown-secret")
	for _, p := range []string{"/", "/index.html", "/app.js"} {
		code, _ := getResource(t, base+p, nil, nil)
		if code != http.StatusUnauthorized {
			t.Fatalf("no-auth static %s status = %d, want 401", p, code)
		}
	}

	// A valid session cookie (no header) still authorizes static assets.
	cookie := &http.Cookie{Name: SessionCookieName, Value: MintSessionToken(sessionSecret(), "sess-static")}
	code, body := getResource(t, base+"/", nil, cookie)
	if code != http.StatusOK {
		t.Fatalf("cookie static / status = %d, want 200; body=%s", code, body)
	}

	// Dev mode (no secret): static is public again (regression).
	SetSessionSecret("")
	code, body = getResource(t, base+"/", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("dev-mode static / status = %d, want 200; body=%s", code, body)
	}
	if !strings.Contains(string(body), "hello") {
		t.Fatalf("dev-mode static / body lost content: %s", body)
	}
}
