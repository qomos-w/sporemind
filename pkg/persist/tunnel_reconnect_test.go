package persist

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// killableForwarder is a 127.0.0.1 listener that pipes every connection to
// target — the same shape as sshmanager's ssh -L local listener (tunnel.go
// binds 127.0.0.1:0 and forwards over direct-tcpip). Unlike the static
// forwarders in netbackend_test.go it can be killed (listener AND every
// established connection, like an SSH connection dropping) and revived on
// the SAME address (like sshmanager re-establishing the SSH session while
// keeping the local listener).
type killableForwarder struct {
	target string

	mu    sync.Mutex
	ln    net.Listener
	conns map[net.Conn]struct{}
	dead  bool
}

func newKillableForwarder(t *testing.T, target string) *killableForwarder {
	t.Helper()
	kf := &killableForwarder{target: target, conns: map[net.Conn]struct{}{}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("forwarder listen: %v", err)
	}
	kf.ln = ln
	go kf.acceptLoop()
	return kf
}

func (kf *killableForwarder) acceptLoop() {
	for {
		c, err := kf.ln.Accept()
		if err != nil {
			return
		}
		kf.mu.Lock()
		if kf.dead {
			kf.mu.Unlock()
			_ = c.Close()
			return
		}
		kf.conns[c] = struct{}{}
		kf.mu.Unlock()
		go kf.pipe(c)
	}
}

func (kf *killableForwarder) pipe(c net.Conn) {
	defer func() {
		_ = c.Close()
		kf.mu.Lock()
		delete(kf.conns, c)
		kf.mu.Unlock()
	}()
	rc, err := net.DialTimeout("tcp", kf.target, 10*time.Second)
	if err != nil {
		return
	}
	defer rc.Close()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(rc, c)
		if cw, ok := rc.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(c, rc)
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
}

// addr returns the forwarder's dial address.
func (kf *killableForwarder) addr() string { return kf.ln.Addr().String() }

// kill closes the listener and every established connection: from the
// backend's point of view the tunnel (and its live connections) is gone.
func (kf *killableForwarder) kill() {
	kf.mu.Lock()
	defer kf.mu.Unlock()
	kf.dead = true
	_ = kf.ln.Close()
	for c := range kf.conns {
		_ = c.Close()
		delete(kf.conns, c)
	}
}

// revive re-listens on the same address, modelling sshmanager keeping the
// local forward across an SSH reconnect. It must only be called after kill.
func (kf *killableForwarder) revive(t *testing.T) {
	t.Helper()
	kf.mu.Lock()
	defer kf.mu.Unlock()
	ln, err := net.Listen("tcp", kf.addr())
	if err != nil {
		t.Fatalf("forwarder revive on %s: %v", kf.addr(), err)
	}
	kf.ln = ln
	kf.dead = false
	go kf.acceptLoop()
}

// TestTunnelDisconnectReconnect is the B7 acceptance scenario for a
// connection-oriented backend behind a B3 tunnel: while the tunnel is down
// every store operation must surface an explicit error (never panic, never
// a silent miss like Load returning ErrNotExist), and once the tunnel is
// back at the same local address the store must recover without being
// reconstructed. Redis (a persistent-connection client against a real
// container) is the canary; skips when no redis container answers.
func TestTunnelDisconnectReconnect(t *testing.T) {
	target := redisTestAddr(t)

	kf := newKillableForwarder(t, target)
	t.Cleanup(func() { kf.kill() })
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, targetAddr string) (string, error) {
		if ref != "host-1" {
			return "", fmt.Errorf("unexpected tunnel ref %q", ref)
		}
		return kf.addr(), nil
	})

	p, err := New(PersistConfig{
		Backend:   BackendRedis,
		Endpoint:  target,
		Prefix:    "tun-" + freshNamespace(t),
		TunnelRef: "host-1",
	})
	if err != nil {
		t.Fatalf("New(redis through tunnel): %v", err)
	}
	if err := p.Save("doc", map[string]int{"v": 1}); err != nil {
		t.Fatalf("Save through live tunnel: %v", err)
	}

	// Tunnel drops (SSH connection dies, local forward torn down).
	kf.kill()

	// Every operation must error — and the error must be a real I/O error,
	// not ErrNotExist masquerading as "document missing".
	deadline := time.Now().Add(15 * time.Second)
	var saveErr, loadErr error
	for time.Now().Before(deadline) {
		saveErr = p.Save("doc", map[string]int{"v": 2})
		var v map[string]int
		loadErr = p.Load("doc", &v)
		if saveErr != nil && loadErr != nil {
			break
		}
		// Connections can take a moment to notice the kill (in-flight
		// requests on a dying-but-not-yet-closed conn); keep probing.
		time.Sleep(100 * time.Millisecond)
	}
	if saveErr == nil {
		t.Fatal("Save succeeded while the tunnel was down (silent?)")
	}
	if loadErr == nil {
		t.Fatal("Load succeeded while the tunnel was down (silent?)")
	}
	if loadErr == ErrNotExist {
		t.Fatalf("Load while tunnel down returned ErrNotExist — a down tunnel must not look like a missing document: %v", loadErr)
	}

	// Tunnel is re-established at the same local address; the store must
	// recover on its own (client reconnect), no reconstruction needed.
	kf.revive(t)
	recovered := false
	for time.Now().Before(deadline) {
		var v map[string]int
		if err := p.Load("doc", &v); err == nil && v["v"] == 1 {
			recovered = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !recovered {
		t.Fatal("store did not recover after the tunnel was re-established")
	}
	if err := p.Save("doc", map[string]int{"v": 3}); err != nil {
		t.Fatalf("Save after tunnel recovery: %v", err)
	}
	var v map[string]int
	if err := p.Load("doc", &v); err != nil || v["v"] != 3 {
		t.Fatalf("roundtrip after recovery: v=%v err=%v", v, err)
	}
}
