package sdk

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// connectEventsWS dials the WS event channel on the test server and returns
// the connection plus a decoder channel of wsEnvelope frames.
func connectEventsWS(t *testing.T, base string) (*websocket.Conn, chan wsEnvelope) {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	wsURL := "ws://" + u.Host + "/events"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	out := make(chan wsEnvelope, 8)
	go func() {
		defer close(out)
		for {
			var env wsEnvelope
			if err := conn.ReadJSON(&env); err != nil {
				return
			}
			out <- env
		}
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return conn, out
}

// setCaptureHost installs the test host bridge that accepts every call so
// EmitEvent's host path (app.emit) does not fail during local fanout tests.
func setCaptureHost(t *testing.T) {
	t.Helper()
	SetHost(&captureHost{invoke: func(string, any) ([]byte, error) {
		return []byte(`{"Accepted":true}`), nil
	}})
	t.Cleanup(func() { SetHost(nil) })
}

// TestEventsWebSocketBroadcast pins the WS event channel: a panel-style
// client upgrades GET /events, EmitEvent reaches it as one JSON envelope with
// the event id and raw-JSON data — the same broadcast the SSE path carries,
// on a connection outside the browser's HTTP/1.1 pool.
func TestEventsWebSocketBroadcast(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	setCaptureHost(t)

	_, envs := connectEventsWS(t, base)
	waitForConns(t, srv, 1)

	if err := EmitEvent("usage.updated", map[string]any{"PlanId": "glm", "Used": 3}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	select {
	case env := <-envs:
		if env.Event != "usage.updated" {
			t.Errorf("envelope event = %q, want usage.updated", env.Event)
		}
		if g := string(env.Data); !strings.Contains(g, `"PlanId":"glm"`) || !strings.Contains(g, `"Used":3`) {
			t.Errorf("envelope data = %s, want PlanId glm / Used 3", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for WS envelope")
	}
}

// TestEventsWebSocketAndSSEShareHub pins mixed-transport fanout: one SSE
// client and one WS client registered on the same hub both receive a single
// EmitEvent.
func TestEventsWebSocketAndSSEShareHub(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	setCaptureHost(t)

	sseCh, cancelSSE := connectSSE(t, base)
	defer cancelSSE()
	_, envs := connectEventsWS(t, base)
	waitForConns(t, srv, 2)

	if err := EmitEvent("tick", map[string]any{"N": 1}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	select {
	case env := <-envs:
		if env.Event != "tick" {
			t.Errorf("ws envelope event = %q, want tick", env.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for WS envelope")
	}
	select {
	case ev := <-sseCh:
		if ev.kind != "tick" {
			t.Errorf("sse kind = %q, want tick", ev.kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE frame")
	}
}

// TestEventsWebSocketClientCloseUnregisters pins zombie-socket hygiene: a
// closed WS client leaves the hub promptly so server-side ping writes to a
// dead peer cannot accumulate (the 2026-09-14 reload-storm scenario).
func TestEventsWebSocketClientCloseUnregisters(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)

	conn, _ := connectEventsWS(t, base)
	waitForConns(t, srv, 1)

	_ = conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.hub.count() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("hub still holds %d connection(s) after client close", srv.hub.count())
}

// TestEventsWebSocketAuthPreUpgrade pins that the WS handshake goes through
// the same auth as every other route: with a secret configured and no cookie
// or gateway token the handshake is refused 401 before any upgrade bytes.
func TestEventsWebSocketAuthPreUpgrade(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	SetSessionSecret("6b6579")
	t.Cleanup(func() { SetSessionSecret("") })

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(base + "/events") // sanity: non-upgrade also 401
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("SSE no-auth status = %d, want 401", resp.StatusCode)
	}

	// gorilla.Dialer returns the handshake response error for non-101.
	_, hr, err := websocket.DefaultDialer.Dial("ws://"+u.Host+"/events", nil)
	if err == nil {
		t.Fatal("unauthenticated WS dial unexpectedly succeeded")
	}
	if hr == nil || hr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated WS handshake resp = %+v, want 401", hr)
	}
}
