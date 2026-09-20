package sdk

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// eventHub manages the set of connected event clients (SSE or WebSocket) for
// one HTTP listener. The hub is owned by an *HTTPServer; EmitEvent reaches it
// through the package-level httpState so a handler (which carries no Context)
// can fan an event out to every connected frontend without holding a server
// reference.
//
// Concurrency model: each connected client is served by its own handler
// goroutine (net/http spawns one per request). That goroutine (plus, for
// WebSocket, its write pump) is the single writer for the connection — it
// reads from the connection's buffered channel and writes one frame at a
// time under the connection's mutex, so writes to one client never block
// writes to another. Broadcast is non-blocking: a client whose channel is
// full (a slow consumer) has the event dropped and is left for its handler
// goroutine to detect a dead connection on the next write and unregister. A
// disconnected client is cleaned up promptly because net/http cancels the
// request context when the underlying connection closes, which unblocks the
// handler's select and runs the deferred unregister.
type sseHub struct {
	mu     sync.Mutex
	conns  map[eventConn]struct{}
	closed bool
}

// eventConn is one connected event client. push is a non-blocking buffered
// send from Broadcast; a full buffer drops the event for that client only.
type eventConn interface {
	push(msg sseMsg)
}

func newSSEHub() *sseHub { return &sseHub{conns: make(map[eventConn]struct{})} }

func (h *sseHub) register(c eventConn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
}

func (h *sseHub) unregister(c eventConn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

// count returns the number of connected clients. Test/diagnostic helper.
func (h *sseHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

// close marks the hub shutting down so handler goroutines exit; it does not
// force-close connections (the server Shutdown closes the listener, which
// tears down handler goroutines via request-context cancellation).
func (h *sseHub) close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
}

// sseMsg is one SSE event pending delivery to a connection.
type sseMsg struct {
	kind string
	data []byte
}

// sseConn is one connected SSE client. ch is a bounded buffer the hub pushes
// to; the handler goroutine drains it and writes to w under mu.
type sseConn struct {
	w  http.ResponseWriter
	mu sync.Mutex
	ch chan sseMsg
}

const sseSendBuffer = 32

// push implements eventConn for SSE connections.
func (c *sseConn) push(msg sseMsg) {
	select {
	case c.ch <- msg:
	default:
		// Slow client: drop this event rather than block the broadcaster.
		// The handler goroutine will unregister the connection when its
		// next write fails or the request context cancels.
		Log(LogLevelWarn, "sse: dropping event %q for stalled client", msg.kind)
	}
}

// Broadcast writes one event to every connected client (SSE or WebSocket). It
// is safe to call from any goroutine (including a handler's EmitEvent). A
// full send buffer drops the event for that client only — the broadcaster is
// never blocked by a single stalled consumer. Payload is the already-marshaled
// JSON event body (EmitEvent marshals once and reuses it for both the local
// and host paths).
func (h *sseHub) Broadcast(kind string, payload []byte) {
	msg := sseMsg{kind: kind, data: payload}
	h.mu.Lock()
	conns := make([]eventConn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		c.push(msg)
	}
}

// writeFrame writes one SSE event frame to the connection and flushes. The
// caller is the connection's handler goroutine, so only one writeFrame is in
// flight per connection; mu still serializes against a potential heartbeat
// writer if one is added later.
func (c *sseConn) writeFrame(msg sseMsg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	if msg.kind != "" {
		fmt.Fprintf(&b, "event: %s\n", msg.kind)
	}
	// SSE data: prefix every line with "data: " per the spec; a JSON payload
	// is a single line, but multi-line payloads are handled correctly too.
	for _, line := range strings.Split(string(msg.data), "\n") {
		fmt.Fprintf(&b, "data: %s\n", line)
	}
	b.WriteByte('\n')
	if _, err := c.w.Write([]byte(b.String())); err != nil {
		return err
	}
	if f, ok := c.w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
