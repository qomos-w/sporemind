package pluginhost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// wsTestUpgrader accepts anything: the gateway director scrubs browser
// Origin/Cookie headers and injects the gateway token, so backend-side
// origin checks would only fight the proxy.
var wsTestUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// TestProxyWebSocketUpgradePassthrough pins the full panel-event path: a
// WebSocket dial to /plugin/{id}/events passes through the gateway proxy's
// header rewrite (Cookie/Origin scrubbed, gateway token injected) without
// breaking the 101 upgrade, and frames flow both ways. Regression guard for
// the WS-first event transport (2026-09-14 pool-exhaustion fix).
func TestProxyWebSocketUpgradePassthrough(t *testing.T) {
	a, router := newAssetsTestActor(t)

	var gotToken atomic.Value
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken.Store(r.Header.Get("X-Spore-Gateway-Token"))
		c, err := wsTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		mt, msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		if err := c.WriteMessage(mt, append([]byte("echo:"), msg...)); err != nil {
			return
		}
		// Hold the connection open until the client hangs up.
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(backend.Close)
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{
		PluginID: "app.wsdemo",
		Addr:     backendAddr,
		Token:    "gateway-proxy.ws-token",
	}); err != nil {
		t.Fatalf("proxy_attach: %v", err)
	}

	front := httptest.NewServer(router)
	t.Cleanup(front.Close)
	wsURL := "ws" + strings.TrimPrefix(front.URL, "http") + "/plugin/app.wsdemo/events"

	// Browser-like headers: the director scrubs Cookie/Origin; the upgrade
	// must survive that rewrite.
	hdr := map[string][]string{
		"Cookie": {"spore_session=browser"},
		"Origin": {"http://localhost:3000"},
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial through proxy: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(msg) != "echo:ping" {
		t.Fatalf("echo = %q, want echo:ping", msg)
	}
	if tok, _ := gotToken.Load().(string); tok != "gateway-proxy.ws-token" {
		t.Fatalf("backend gateway token = %q, want injected proxy token", tok)
	}
}

// TestProxyWebSocketBackendDeathClosesClient pins zombie-socket hygiene: when
// the backend (plugin process) dies mid-stream, the proxied client connection
// must close too — otherwise dead panels permanently occupy the browser's
// connection pool (the 9-reload storm during the 2026-09-14 investigation).
func TestProxyWebSocketBackendDeathClosesClient(t *testing.T) {
	a, router := newAssetsTestActor(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := wsTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Simulate plugin-process death: hard-close the TCP connection.
		_ = c.UnderlyingConn().Close()
	}))
	t.Cleanup(backend.Close)
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	if _, err := a.handleProxyAttach(nil, gen.PluginProxyAttachReq{
		PluginID: "app.wsdead",
		Addr:     backendAddr,
		Token:    "gateway-proxy.dead",
	}); err != nil {
		t.Fatalf("proxy_attach: %v", err)
	}

	front := httptest.NewServer(router)
	t.Cleanup(front.Close)
	wsURL := "ws" + strings.TrimPrefix(front.URL, "http") + "/plugin/app.wsdead/events"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial through proxy: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("client still reading after backend hard-close: proxied socket leaked")
	}
}
