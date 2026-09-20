package sdk

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket event channel (GET /events with an Upgrade header).
//
// Why WebSocket exists alongside SSE: the desktop SPA embeds one iframe per
// plugin panel, all pointed at the same gateway origin. Chromium caps
// same-partition HTTP/1.1 connections at 6 per host, so panels holding a
// permanent SSE stream each exhausted the shared pool and every transient
// request (invoke/poll) stalled forever (2026-09-14 incident). WebSocket
// connections use a separate browser connection pool and leave the HTTP pool
// to transient traffic. SSE remains for standalone browser opens and as the
// fallback for older SDKs (an old listener answers the handshake with a plain
// 200 stream, which the client detects and falls back from).

const (
	// wsPingInterval must stay well below middlebox idle timeouts (30-60s).
	wsPingInterval = 25 * time.Second
	// wsReadDeadline allows losing 2 consecutive pongs before the server
	// gives up on the client.
	wsReadDeadline = 75 * time.Second
	// wsWriteControlDeadline bounds each ping write.
	wsWriteControlDeadline = 5 * time.Second
)

// wsUpgrader has no CheckOrigin policy of its own: authorizeRequest already
// enforced the origin/cookie/token rules before the upgrade is attempted, and
// the gateway proxy scrubs Origin entirely (its injected token is the auth).
var wsUpgrader = websocket.Upgrader{
	CheckOrigin:      func(*http.Request) bool { return true },
	ReadBufferSize:   512,
	WriteBufferSize:  2048,
}

// wsEnvelope is the single frame format on the WebSocket event channel: one
// JSON object per event, data carried as raw JSON (never double-encoded).
type wsEnvelope struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// wsConn is one connected WebSocket event client. The handler goroutine runs
// the read loop (deadline + pong handling); a single write-pump goroutine is
// the only writer (envelopes + pings) — gorilla requires serialized writes.
type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex // serializes WriteMessage/WriteControl
	ch   chan sseMsg
}

func (c *wsConn) push(msg sseMsg) {
	select {
	case c.ch <- msg:
	default:
		// Slow client: drop this event rather than block the broadcaster.
		// The write pump unregisters the connection when its next write
		// fails or the read deadline expires.
		Log(LogLevelWarn, "ws: dropping event %q for stalled client", msg.kind)
	}
}

func (c *wsConn) writeEnvelope(msg sseMsg) error {
	raw, err := json.Marshal(wsEnvelope{Event: msg.kind, Data: json.RawMessage(msg.data)})
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, raw)
}

// serveEventsWS upgrades the request and streams hub events as JSON
// envelopes. Auth already ran in handleEvents; on upgrade failure the
// upgrader has written the HTTP error itself.
func (s *HTTPServer) serveEventsWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &wsConn{conn: conn, ch: make(chan sseMsg, sseSendBuffer)}
	s.hub.register(c)
	defer s.hub.unregister(c)

	pumpDone := make(chan struct{})
	defer close(pumpDone)
	go func() {
		// Single-writer pump: envelopes from the hub + keepalive pings.
		ping := time.NewTicker(wsPingInterval)
		defer ping.Stop()
		for {
			select {
			case msg := <-c.ch:
				if err := c.writeEnvelope(msg); err != nil {
					_ = conn.Close()
					return
				}
			case <-ping.C:
				c.mu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteControlDeadline))
				c.mu.Unlock()
				if err != nil {
					_ = conn.Close()
					return
				}
			case <-pumpDone:
				return
			}
		}
	}()

	// Read loop: client data frames are ignored (the channel is push-only),
	// but reads are required to process pongs and detect a closed peer.
	// Any read error (close, deadline, TCP reset) tears the connection down
	// and unblocks the pump via conn.Close.
	_ = conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			_ = conn.Close()
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	}
}
