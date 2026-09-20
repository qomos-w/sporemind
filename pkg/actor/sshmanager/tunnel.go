package sshmanager

// ---------------------------------------------------------------------------
// sshmanager.tunnel — ssh -L local port forwarding
//
// A tunnel binds 127.0.0.1:0 on this machine and forwards every accepted
// connection through the real SSH connection to a target address as seen from
// the SSH host (direct-tcpip under the hood). Callers (DB backends, redis
// clients, …) dial LocalAddr with a plain TCP client — no SSH on their side,
// and no net.Conn ever crosses the actor/schema boundary.
//
// Tunnels are shared per (hostID, targetAddr) with reference counting:
// multiple backends opening the same target merge onto one tunnel and each
// open must be matched by exactly one close. The last close tears the tunnel
// down. Tunnels are ephemeral (never persisted); handleHostRemove and OnStop
// close them in full.
// ---------------------------------------------------------------------------

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"golang.org/x/crypto/ssh"
)

// tunnel is one shared local port forward. refCount and lastUsed are guarded
// by Actor.mu (the same lock that owns the tunnels map); client swaps are
// guarded by t.mu; active counts forwarded connections in flight.
type tunnel struct {
	hostID     string
	targetAddr string
	localAddr  string
	listener   net.Listener

	mu     sync.Mutex
	client *ssh.Client

	refCount int
	lastUsed time.Time

	active    atomic.Int32
	done      chan struct{}
	closeOnce sync.Once
}

// tunnelKey builds the share key for a (host, target) tunnel.
func tunnelKey(hostID, targetAddr string) string {
	return hostID + "\x00" + targetAddr
}

// validateTunnelTargetAddr ensures addr is a usable host:port as seen from
// the SSH host. Loopback and private targets are explicitly allowed — that is
// the whole point of an ssh -L tunnel.
func validateTunnelTargetAddr(addr string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return fmt.Errorf("targetAddr %q must be host:port: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("targetAddr %q has an empty host", addr)
	}
	if port == "" {
		return fmt.Errorf("targetAddr %q has an empty port", addr)
	}
	return nil
}

// close tears the tunnel down: stop accepting, close the listener and the
// SSH client. Idempotent.
func (t *tunnel) close() {
	t.closeOnce.Do(func() {
		close(t.done)
		_ = t.listener.Close()
		t.mu.Lock()
		c := t.client
		t.client = nil
		t.mu.Unlock()
		if c != nil {
			_ = c.Close()
		}
	})
}

// dialTarget opens one forwarded connection to the tunnel target. When the
// cached SSH client has dropped, it is discarded and re-established exactly
// once (mirroring the exec reconnect policy); a fresh client that loses a
// second race to a concurrent reconnector is closed immediately.
func (t *tunnel) dialTarget(a *Actor, host *domain.SshHost) (net.Conn, error) {
	t.mu.Lock()
	client := t.client
	t.mu.Unlock()
	if client == nil {
		return nil, fmt.Errorf("tunnel %s: closed", t.localAddr)
	}

	conn, err := client.Dial("tcp", t.targetAddr)
	if err == nil {
		return conn, nil
	}

	// Transport failure: reconnect once and retry.
	_ = client.Close()
	fresh, dialErr := a.dialExec(host)
	if dialErr != nil {
		return nil, fmt.Errorf("tunnel dial %s: %w (reconnect failed: %v)", t.targetAddr, err, dialErr)
	}
	t.mu.Lock()
	if t.client == nil {
		t.mu.Unlock()
		_ = fresh.Close()
		return nil, fmt.Errorf("tunnel %s: closed", t.localAddr)
	}
	if t.client != client {
		// A concurrent forwarder already reconnected; keep the winner.
		_ = fresh.Close()
	} else {
		t.client = fresh
	}
	cur := t.client
	t.mu.Unlock()

	conn, err = cur.Dial("tcp", t.targetAddr)
	if err != nil {
		return nil, fmt.Errorf("tunnel dial %s after reconnect: %w", t.targetAddr, err)
	}
	return conn, nil
}

// forward pipes one accepted local connection through the SSH channel to the
// target and back. Both directions close the peer when they finish so a
// half-closed stream (e.g. a DB driver that shuts down its write side)
// terminates the other copy promptly.
func (t *tunnel) forward(a *Actor, host *domain.SshHost, c net.Conn) {
	t.active.Add(1)
	defer t.active.Add(-1)
	a.touchTunnel(t.hostID, t.targetAddr)

	defer c.Close()
	rc, err := t.dialTarget(a, host)
	if err != nil {
		return
	}
	defer rc.Close()

	go func() {
		_, _ = io.Copy(rc, c)
		if tc, ok := rc.(interface{ CloseWrite() error }); ok {
			_ = tc.CloseWrite()
		} else {
			_ = rc.Close()
		}
	}()
	_, _ = io.Copy(c, rc)
}

// serve accepts local connections until the tunnel is closed.
func (t *tunnel) serve(a *Actor, host *domain.SshHost) {
	for {
		c, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.done:
				return
			default:
			}
			// Transient accept failure on a live tunnel: keep serving.
			continue
		}
		go t.forward(a, host, c)
	}
}

// touchTunnel refreshes lastUsed on a live tunnel (best effort).
func (a *Actor) touchTunnel(hostID, targetAddr string) {
	a.mu.Lock()
	if t := a.tunnels[tunnelKey(hostID, targetAddr)]; t != nil {
		t.lastUsed = time.Now()
	}
	a.mu.Unlock()
}

// openTunnel returns a usable tunnel for (host, target), creating and serving
// one on first use. Dialing and listening happen outside the lock (a slow
// SSH handshake must not block other callers); a re-check after setup
// discards a duplicate when a concurrent caller won the race.
func (a *Actor) openTunnel(host *domain.SshHost, targetAddr string) (localAddr string, refCount int, err error) {
	key := tunnelKey(host.ID, targetAddr)

	a.mu.Lock()
	if a.tunnels == nil {
		a.tunnels = make(map[string]*tunnel)
	}
	if t := a.tunnels[key]; t != nil {
		t.refCount++
		t.lastUsed = time.Now()
		a.mu.Unlock()
		return t.localAddr, t.refCount, nil
	}
	a.mu.Unlock()

	client, err := a.dialExec(host)
	if err != nil {
		return "", 0, fmt.Errorf("tunnel open: connect: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = client.Close()
		return "", 0, fmt.Errorf("tunnel open: listen: %w", err)
	}
	t := &tunnel{
		hostID:     host.ID,
		targetAddr: targetAddr,
		localAddr:  ln.Addr().String(),
		listener:   ln,
		client:     client,
		refCount:   1,
		lastUsed:   time.Now(),
		done:       make(chan struct{}),
	}

	a.mu.Lock()
	if existing := a.tunnels[key]; existing != nil {
		a.mu.Unlock()
		t.close() // discard our duplicate
		existing.refCount++
		existing.lastUsed = time.Now()
		return existing.localAddr, existing.refCount, nil
	}
	a.tunnels[key] = t
	a.mu.Unlock()

	go t.serve(a, host)
	return t.localAddr, 1, nil
}

// closeTunnel releases one reference on the (host, target) tunnel. The tunnel
// is torn down when the last reference goes away.
func (a *Actor) closeTunnel(hostID, targetAddr string) (closed bool, refCount int, err error) {
	key := tunnelKey(hostID, targetAddr)

	a.mu.Lock()
	t := a.tunnels[key]
	if t == nil {
		a.mu.Unlock()
		return false, 0, fmt.Errorf("tunnel %q -> %q not found", hostID, targetAddr)
	}
	t.refCount--
	if t.refCount > 0 {
		n := t.refCount
		a.mu.Unlock()
		return false, n, nil
	}
	delete(a.tunnels, key)
	a.mu.Unlock()

	t.close()
	return true, 0, nil
}

// closeTunnelsForHost tears down every tunnel bound to hostID. Used by
// handleHostRemove so a removed host leaves no dangling forwards.
func (a *Actor) closeTunnelsForHost(hostID string) {
	a.mu.Lock()
	var doomed []*tunnel
	for k, t := range a.tunnels {
		if t.hostID == hostID {
			doomed = append(doomed, t)
			delete(a.tunnels, k)
		}
	}
	a.mu.Unlock()
	for _, t := range doomed {
		t.close()
	}
}

// closeAllTunnels tears down every tunnel. Used by OnStop.
func (a *Actor) closeAllTunnels() {
	a.mu.Lock()
	doomed := make([]*tunnel, 0, len(a.tunnels))
	for k, t := range a.tunnels {
		doomed = append(doomed, t)
		delete(a.tunnels, k)
	}
	a.mu.Unlock()
	for _, t := range doomed {
		t.close()
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleTunnelOpen opens (or references) a local port forward to targetAddr
// via the given host and returns the dialable local address. Agent-accessible
// like exec: security is enforced by the turn engine's effect policy.
// resolvePersistTunnel implements the persist package's process-wide
// TunnelResolver (installed at OnStart): a database backend with
// PersistConfig.TunnelRef = hostID dials through an ssh -L forward instead of
// the endpoint directly. targetAddr is the database endpoint as reachable
// from the SSH host (host:port, same rule as SshTunnelOpenReq.TargetAddr).
//
// Tunnel lifecycle stays with sshmanager: this opens (or reuses a refcounted)
// forward and returns its local dial address; host removal, OnStop, and idle
// reaping close tunnels in full. Backends hold no close hook, so a resolved
// forward is held for the backend's lifetime — teardown is the actor's.
func (a *Actor) resolvePersistTunnel(ref, targetAddr string) (string, error) {
	host := a.findHost(ref)
	if host == nil {
		return "", fmt.Errorf("sshmanager: tunnel ref %q: host not found", ref)
	}
	targetAddr = strings.TrimSpace(targetAddr)
	if err := validateTunnelTargetAddr(targetAddr); err != nil {
		return "", err
	}
	resolved := a.hostWithCredential(host)
	localAddr, _, err := a.openTunnel(&resolved, targetAddr)
	return localAddr, err
}

func (a *Actor) handleTunnelOpen(ctx actor.PureContext, req domain.SshTunnelOpenReq) (domain.SshTunnelOpenResp, error) {
	if _, err := requireSSHToolCaller(ctx); err != nil {
		return domain.SshTunnelOpenResp{}, err
	}
	host := a.findHost(req.HostID)
	if host == nil || !a.hostVisibleToCaller(ctx, req.HostID) {
		return domain.SshTunnelOpenResp{}, fmt.Errorf("host %q not found", req.HostID)
	}
	targetAddr := strings.TrimSpace(req.TargetAddr)
	if err := validateTunnelTargetAddr(targetAddr); err != nil {
		return domain.SshTunnelOpenResp{}, err
	}

	resolved := a.hostWithCredential(host)
	localAddr, refCount, err := a.openTunnel(&resolved, targetAddr)
	if err != nil {
		return domain.SshTunnelOpenResp{}, err
	}
	return domain.SshTunnelOpenResp{LocalAddr: localAddr, RefCount: int32(refCount)}, nil
}

// handleTunnelClose releases one reference on a tunnel. The tunnel is closed
// when the last reference is released.
func (a *Actor) handleTunnelClose(ctx actor.PureContext, req domain.SshTunnelCloseReq) (domain.SshTunnelCloseResp, error) {
	if _, err := requireSSHToolCaller(ctx); err != nil {
		return domain.SshTunnelCloseResp{}, err
	}
	if !a.hostVisibleToCaller(ctx, req.HostID) {
		return domain.SshTunnelCloseResp{}, fmt.Errorf("host %q not found", req.HostID)
	}
	targetAddr := strings.TrimSpace(req.TargetAddr)
	closed, refCount, err := a.closeTunnel(req.HostID, targetAddr)
	if err != nil {
		return domain.SshTunnelCloseResp{}, err
	}
	return domain.SshTunnelCloseResp{Closed: closed, RefCount: int32(refCount)}, nil
}

// handleTunnelList lists live tunnels. Agent callers only see tunnels whose
// host is visible to them (AgentInvisible hosts are fully hidden).
func (a *Actor) handleTunnelList(ctx actor.PureContext, req domain.SshTunnelListReq) (domain.SshTunnelListResp, error) {
	if _, err := requireSSHToolCaller(ctx); err != nil {
		return domain.SshTunnelListResp{}, err
	}
	a.mu.Lock()
	items := make([]domain.SshTunnelInfo, 0, len(a.tunnels))
	for _, t := range a.tunnels {
		if !a.hostVisibleToCaller(ctx, t.hostID) {
			continue
		}
		items = append(items, domain.SshTunnelInfo{
			HostID:      t.hostID,
			TargetAddr:  t.targetAddr,
			LocalAddr:   t.localAddr,
			RefCount:    int32(t.refCount),
			ActiveConns: t.active.Load(),
		})
	}
	a.mu.Unlock()
	return domain.SshTunnelListResp{Items: items}, nil
}
