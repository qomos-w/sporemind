package runtime_test

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/gateway"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// diagSlowActor exposes one quick and one deliberately slow callable, used to
// drive the gateway unary-invoke timeout path end to end.
type diagSlowActor struct{ actor.Host }

func (a *diagSlowActor) OnStart(ctx actor.Context) error {
	if err := ctx.RegisterDomain("diag").Expose(); err != nil {
		return err
	}
	if err := ctx.Register("diag.quick", func(_ actor.Context) (string, error) {
		return "quick-ok", nil
	}); err != nil {
		return err
	}
	return ctx.Register("diag.slow", func(_ actor.Context) (string, error) {
		time.Sleep(2 * time.Second)
		return "slow-done", nil
	})
}

// memFrameConn is an in-memory gateway.FrameConn speaking the binary wire
// protocol — the same surface the desktop's wails transport implements.
type memFrameConn struct {
	mu     sync.Mutex
	in     chan *gateway.WireFrame
	out    chan *gateway.WireFrame
	closed bool
}

func newMemFrameConn() *memFrameConn {
	return &memFrameConn{
		in:  make(chan *gateway.WireFrame, 8),
		out: make(chan *gateway.WireFrame, 8),
	}
}

func (c *memFrameConn) Recv() (*gateway.WireFrame, error) {
	f, ok := <-c.in
	if !ok {
		return nil, io.EOF
	}
	return f, nil
}

func (c *memFrameConn) Send(f *gateway.WireFrame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return io.ErrClosedPipe
	}
	c.out <- f
	return nil
}

func (c *memFrameConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.in)
	}
	return nil
}

func (c *memFrameConn) send(f *gateway.WireFrame) {
	c.in <- f
}

func (c *memFrameConn) read(t *testing.T) *gateway.WireFrame {
	t.Helper()
	select {
	case f, ok := <-c.out:
		if !ok {
			t.Fatal("outbound channel closed")
		}
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for outbound frame")
		return nil
	}
}

func invokeFrame(corID uint64, callID string) *gateway.WireFrame {
	return &gateway.WireFrame{
		Flags:  gateway.MakeFlags(gateway.EncodingBinary, gateway.CompressionNone),
		Type:   gateway.FrameTypeInvoke,
		CorID:  corID,
		CallID: callID,
	}
}

func waitService(t *testing.T, h *runtime.Handle, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.App().LookupService(name); ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("service %q did not appear within 5s", name)
}

// TestGatewayUnaryInvoke_TimeoutSurfacesAsError reproduces the 2026-08-29
// incident chain at full fidelity: a unary invoke whose handler outlives the
// gateway timeout must reach the client as an ERROR frame ("gateway timeout"),
// never as a success reply with an empty payload (which the frontend decoded
// to undefined, surfacing as "Cannot read properties of undefined").
// It also pins the desktop wiring: runtime.New raises the cap to 120s to
// match the frontend session's invokeTimeoutMs.
func TestGatewayUnaryInvoke_TimeoutSurfacesAsError(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	h, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "diag", Factory: func() actor.Actor { return &diagSlowActor{} }},
		},
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = h.Wait()
	}()

	// Wiring check: runtime.New aligns the server cap with the frontend's
	// 120s invokeTimeoutMs (web/src/application/generated-client.ts).
	if gateway.GatewayInvokeTimeout != 120*time.Second {
		t.Fatalf("GatewayInvokeTimeout = %v, want 2m (frontend alignment)", gateway.GatewayInvokeTimeout)
	}

	gw := h.App().GatewayServer()
	if gw == nil {
		t.Fatal("GatewayServer() returned nil")
	}

	conn := newMemFrameConn()
	defer conn.Close()
	sessionDone := make(chan struct{})
	go func() {
		gw.ServeSession(ctx, conn, gateway.SessionOptions{Role: "user"})
		close(sessionDone)
	}()

	// Success path first: a callable well under the timeout must reply.
	waitService(t, h, "diag")
	conn.send(invokeFrame(1, "diag.quick"))
	reply := conn.read(t)
	if reply.Type != gateway.FrameTypeReply {
		t.Fatalf("diag.quick: expected reply frame, got %v (err=%q)", reply.Type, reply.ErrorMsg)
	}
	if !strings.Contains(string(reply.Payload), "quick-ok") {
		t.Fatalf("diag.quick payload = %q, want quick-ok", reply.Payload)
	}

	// Timeout path: shrink the cap so diag.slow (2s) exceeds it, and assert
	// the client sees an error frame instead of an empty success reply.
	prev := gateway.GatewayInvokeTimeout
	gateway.GatewayInvokeTimeout = 150 * time.Millisecond
	defer func() { gateway.GatewayInvokeTimeout = prev }()

	conn.send(invokeFrame(2, "diag.slow"))
	reply = conn.read(t)
	if reply.Type == gateway.FrameTypeReply {
		t.Fatalf("BUG: gateway timeout masked as success reply (payload=%q)", reply.Payload)
	}
	if reply.Type != gateway.FrameTypeError {
		t.Fatalf("diag.slow: expected error frame, got %v", reply.Type)
	}
	if !strings.Contains(reply.ErrorMsg, "gateway timeout") {
		t.Fatalf("diag.slow error = %q, want gateway timeout", reply.ErrorMsg)
	}
}
