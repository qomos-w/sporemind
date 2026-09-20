package sshmanager

import (
	"crypto/ed25519"
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"golang.org/x/crypto/ssh"
)

// ---------------------------------------------------------------------------
// In-process SSH server with direct-tcpip forwarding
// ---------------------------------------------------------------------------

// forwardRequest is the payload of a direct-tcpip channel open.
type forwardRequest struct {
	DestAddr   string
	DestPort   uint32
	OriginAddr string
	OriginPort uint32
}

// tunnelTestServer is an SSH server that authenticates with a fixed password
// and honors direct-tcpip channels by dialing the requested destination and
// piping both directions — enough to exercise open→dial→forward→close.
type tunnelTestServer struct {
	listener net.Listener
	password string
	stopOnce sync.Once
}

func newTunnelTestServer(t *testing.T, password string) *tunnelTestServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, priv, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	srv := &tunnelTestServer{listener: ln, password: password}
	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) != password {
				return nil, errors.New("invalid password")
			}
			return nil, nil
		},
	}
	config.AddHostKey(signer)

	go func() {
		for {
			nConn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(nConn, config)
		}
	}()
	return srv
}

func (s *tunnelTestServer) close() {
	s.stopOnce.Do(func() { _ = s.listener.Close() })
}

func (s *tunnelTestServer) handleConn(nConn net.Conn, config *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nConn, config)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for newChannel := range chans {
		switch newChannel.ChannelType() {
		case "direct-tcpip":
			go s.handleDirectTCPIP(newChannel)
		default:
			_ = newChannel.Reject(ssh.UnknownChannelType, "only direct-tcpip")
		}
	}
}

func (s *tunnelTestServer) handleDirectTCPIP(newChannel ssh.NewChannel) {
	var req forwardRequest
	if err := ssh.Unmarshal(newChannel.ExtraData(), &req); err != nil {
		_ = newChannel.Reject(ssh.Prohibited, "bad forward request")
		return
	}
	channel, reqs, err := newChannel.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)

	target, err := net.Dial("tcp", net.JoinHostPort(req.DestAddr, fmt.Sprintf("%d", req.DestPort)))
	if err != nil {
		_ = channel.Close()
		return
	}
	go func() {
		_, _ = io.Copy(target, channel)
		_ = target.Close()
	}()
	go func() {
		_, _ = io.Copy(channel, target)
		_ = channel.Close()
	}()
}

// ---------------------------------------------------------------------------
// Echo target (stands in for redis/pg on the "remote" side)
// ---------------------------------------------------------------------------

func newEchoServer(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func newTunnelTestActor(t *testing.T, srv *tunnelTestServer) *Actor {
	t.Helper()
	a := &Actor{
		store:       persist.NewFSPersist(t.TempDir()),
		Credentials: make(map[string]sshCredential),
		Commands:    []domain.SshCommandSnippet{},
		History:     []string{},
		execConns:   make(map[string]*execConn),
	}
	host, port := splitHostPort(t, srv.listener.Addr().String())
	a.Hosts = append(a.Hosts, domain.SshHost{
		ID: testHostID, Name: "test", Host: host, Port: port,
		User: "u", AuthMethod: "password",
	})
	a.Credentials[testHostID] = sshCredential{Password: srv.password}
	a.execDial = func(h *domain.SshHost) (*ssh.Client, error) {
		return buildSSHClient(h)
	}
	return a
}

func tunnelTargetOf(t *testing.T, a *Actor, ctx sshTestCtx, target string) string {
	t.Helper()
	resp, err := a.handleTunnelOpen(ctx, domain.SshTunnelOpenReq{HostID: testHostID, TargetAddr: target})
	if err != nil {
		t.Fatalf("tunnel_open: %v", err)
	}
	if resp.LocalAddr == "" {
		t.Fatal("tunnel_open returned empty LocalAddr")
	}
	return resp.LocalAddr
}

// ---------------------------------------------------------------------------
// Full path: open → dial local → forward through SSH → close
// ---------------------------------------------------------------------------

func TestTunnelOpenDialForwardClose(t *testing.T) {
	srv := newTunnelTestServer(t, "pw")
	defer srv.close()
	echoAddr, closeEcho := newEchoServer(t)
	defer closeEcho()

	a := newTunnelTestActor(t, srv)
	localAddr := tunnelTargetOf(t, a, adminCtx(), echoAddr)

	// The DB-client view: a plain TCP dial to the local address.
	conn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial local tunnel addr: %v", err)
	}
	defer conn.Close()

	payload := []byte("PING\r\n")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write through tunnel: %v", err)
	}
	buf := make([]byte, len(payload))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read through tunnel: %v", err)
	}
	if string(buf) != string(payload) {
		t.Fatalf("echo mismatch: got %q want %q", buf, payload)
	}
	_ = conn.Close()

	// Close the tunnel; the local listener must stop accepting.
	resp, err := a.handleTunnelClose(adminCtx(), domain.SshTunnelCloseReq{HostID: testHostID, TargetAddr: echoAddr})
	if err != nil {
		t.Fatalf("tunnel_close: %v", err)
	}
	if !resp.Closed || resp.RefCount != 0 {
		t.Fatalf("expected tunnel torn down, got %+v", resp)
	}
	if _, err := net.DialTimeout("tcp", localAddr, 500*time.Millisecond); err == nil {
		t.Fatal("tunnel listener still accepting after close")
	}

	list, err := a.handleTunnelList(adminCtx(), domain.SshTunnelListReq{})
	if err != nil {
		t.Fatalf("tunnel_list: %v", err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("expected no tunnels after close, got %+v", list.Items)
	}
}

// ---------------------------------------------------------------------------
// Reference counting: shared target merges, last close tears down
// ---------------------------------------------------------------------------

func TestTunnelRefCountMerge(t *testing.T) {
	srv := newTunnelTestServer(t, "pw")
	defer srv.close()
	echoAddr, closeEcho := newEchoServer(t)
	defer closeEcho()

	a := newTunnelTestActor(t, srv)
	first, err := a.handleTunnelOpen(adminCtx(), domain.SshTunnelOpenReq{HostID: testHostID, TargetAddr: echoAddr})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	second, err := a.handleTunnelOpen(agentCtx(), domain.SshTunnelOpenReq{HostID: testHostID, TargetAddr: echoAddr})
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if first.LocalAddr != second.LocalAddr {
		t.Fatalf("same target must share one tunnel: %q vs %q", first.LocalAddr, second.LocalAddr)
	}
	if first.RefCount != 1 || second.RefCount != 2 {
		t.Fatalf("refcount mismatch: first=%d second=%d", first.RefCount, second.RefCount)
	}

	// One close keeps the tunnel alive.
	mid, err := a.handleTunnelClose(adminCtx(), domain.SshTunnelCloseReq{HostID: testHostID, TargetAddr: echoAddr})
	if err != nil {
		t.Fatalf("first close: %v", err)
	}
	if mid.Closed || mid.RefCount != 1 {
		t.Fatalf("tunnel must survive first close: %+v", mid)
	}
	conn, err := net.DialTimeout("tcp", first.LocalAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("tunnel must still accept after first close: %v", err)
	}
	_ = conn.Close()

	// Last close tears it down.
	last, err := a.handleTunnelClose(agentCtx(), domain.SshTunnelCloseReq{HostID: testHostID, TargetAddr: echoAddr})
	if err != nil {
		t.Fatalf("last close: %v", err)
	}
	if !last.Closed || last.RefCount != 0 {
		t.Fatalf("last close must tear down: %+v", last)
	}
}

// ---------------------------------------------------------------------------
// Validation, auth, and visibility
// ---------------------------------------------------------------------------

func TestTunnelOpenRejectsBadTargetAddr(t *testing.T) {
	a := newTestActor(t)
	a.Hosts = append(a.Hosts, domain.SshHost{ID: testHostID, Name: "t", Host: "127.0.0.1", Port: 22, AuthMethod: "password"})
	for _, bad := range []string{"", "   ", "no-port", ":6379", "host:"} {
		if _, err := a.handleTunnelOpen(adminCtx(), domain.SshTunnelOpenReq{HostID: testHostID, TargetAddr: bad}); err == nil {
			t.Fatalf("targetAddr %q must be rejected", bad)
		}
	}
}

func TestTunnelOpenRejectsUnknownHost(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleTunnelOpen(adminCtx(), domain.SshTunnelOpenReq{HostID: "missing", TargetAddr: "127.0.0.1:1"}); err == nil {
		t.Fatal("unknown host must be rejected")
	}
}

func TestTunnelRejectsAnonymous(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleTunnelOpen(anonCtx(), domain.SshTunnelOpenReq{HostID: testHostID, TargetAddr: "127.0.0.1:1"}); err == nil {
		t.Fatal("anonymous tunnel_open must be rejected")
	}
	if _, err := a.handleTunnelClose(anonCtx(), domain.SshTunnelCloseReq{HostID: testHostID, TargetAddr: "127.0.0.1:1"}); err == nil {
		t.Fatal("anonymous tunnel_close must be rejected")
	}
	if _, err := a.handleTunnelList(anonCtx(), domain.SshTunnelListReq{}); err == nil {
		t.Fatal("anonymous tunnel_list must be rejected")
	}
}

func TestTunnelListHidesAgentInvisibleHosts(t *testing.T) {
	srv := newTunnelTestServer(t, "pw")
	defer srv.close()
	echoAddr, closeEcho := newEchoServer(t)
	defer closeEcho()

	a := newTunnelTestActor(t, srv)
	a.Hosts[0].AgentInvisible = true

	tunnelTargetOf(t, a, adminCtx(), echoAddr)

	// Admin sees everything.
	adminList, err := a.handleTunnelList(adminCtx(), domain.SshTunnelListReq{})
	if err != nil {
		t.Fatalf("admin tunnel_list: %v", err)
	}
	if len(adminList.Items) != 1 {
		t.Fatalf("admin must see the tunnel, got %+v", adminList.Items)
	}
	if adminList.Items[0].LocalAddr == "" || adminList.Items[0].RefCount != 1 {
		t.Fatalf("unexpected tunnel info: %+v", adminList.Items[0])
	}

	// Agent callers get the AgentInvisible host fully hidden.
	agentList, err := a.handleTunnelList(agentCtx(), domain.SshTunnelListReq{})
	if err != nil {
		t.Fatalf("agent tunnel_list: %v", err)
	}
	if len(agentList.Items) != 0 {
		t.Fatalf("agent must not see AgentInvisible host tunnels, got %+v", agentList.Items)
	}
	if _, err := a.handleTunnelOpen(agentCtx(), domain.SshTunnelOpenReq{HostID: testHostID, TargetAddr: "127.0.0.1:1"}); err == nil {
		t.Fatal("agent tunnel_open on AgentInvisible host must be rejected")
	}
	if _, err := a.handleTunnelClose(agentCtx(), domain.SshTunnelCloseReq{HostID: testHostID, TargetAddr: echoAddr}); err == nil {
		t.Fatal("agent tunnel_close on AgentInvisible host must be rejected")
	}
}

func TestTunnelCloseNotFound(t *testing.T) {
	a := newTestActor(t)
	a.Hosts = append(a.Hosts, domain.SshHost{ID: testHostID, Name: "t", Host: "127.0.0.1", Port: 22, AuthMethod: "password"})
	if _, err := a.handleTunnelClose(adminCtx(), domain.SshTunnelCloseReq{HostID: testHostID, TargetAddr: "127.0.0.1:6379"}); err == nil {
		t.Fatal("close of a nonexistent tunnel must error")
	}
}

// ---------------------------------------------------------------------------
// Lifecycle: host removal and actor stop tear tunnels down
// ---------------------------------------------------------------------------

func TestHostRemoveClosesTunnels(t *testing.T) {
	srv := newTunnelTestServer(t, "pw")
	defer srv.close()
	echoAddr, closeEcho := newEchoServer(t)
	defer closeEcho()

	a := newTunnelTestActor(t, srv)
	localAddr := tunnelTargetOf(t, a, adminCtx(), echoAddr)

	if _, err := a.handleHostRemove(adminCtx(), domain.SshHostRemoveReq{ID: testHostID}); err != nil {
		t.Fatalf("host_remove: %v", err)
	}
	if _, err := net.DialTimeout("tcp", localAddr, 500*time.Millisecond); err == nil {
		t.Fatal("tunnel must be closed when its host is removed")
	}
}

func TestOnStopClosesTunnels(t *testing.T) {
	srv := newTunnelTestServer(t, "pw")
	defer srv.close()
	echoAddr, closeEcho := newEchoServer(t)
	defer closeEcho()

	a := newTunnelTestActor(t, srv)
	localAddr := tunnelTargetOf(t, a, adminCtx(), echoAddr)

	if err := a.OnStop(nil); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	if _, err := net.DialTimeout("tcp", localAddr, 500*time.Millisecond); err == nil {
		t.Fatal("tunnel must be closed on actor stop")
	}
}

// TestTunnelSurvivesUnreachableTarget ensures a forwarded connection to an
// unreachable target must not kill the tunnel.
func TestTunnelSurvivesUnreachableTarget(t *testing.T) {
	srv := newTunnelTestServer(t, "pw")
	defer srv.close()

	// Grab a definitely-closed local port for the target.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	deadAddr := probe.Addr().String()
	_ = probe.Close()

	a := newTunnelTestActor(t, srv)
	localAddr := tunnelTargetOf(t, a, adminCtx(), deadAddr)

	conn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	// The forward attempt fails; the connection just closes.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Read(make([]byte, 1))
	_ = conn.Close()

	// The tunnel itself is still listed and accepting.
	list, err := a.handleTunnelList(adminCtx(), domain.SshTunnelListReq{})
	if err != nil {
		t.Fatalf("tunnel_list: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("tunnel must survive failed forwards, got %+v", list.Items)
	}
	conn2, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("tunnel must still accept after failed forward: %v", err)
	}
	_ = conn2.Close()
}

// ---------------------------------------------------------------------------
// Protocol-shaped e2e: real redis RESP wire bytes through the tunnel
// ---------------------------------------------------------------------------

// respPongServer answers RESP PING with +PONG — just enough of the redis
// wire protocol to prove the tunnel forwards protocol-shaped traffic
// transparently (container redis/pg e2e is a manual step; see the task card).
func respPongServer(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resp listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if strings.Contains(string(buf[:n]), "PING") {
						if _, err := c.Write([]byte("+PONG\r\n")); err != nil {
							return
						}
					}
				}
			}()
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func TestTunnelRedisPingOverRESP(t *testing.T) {
	srv := newTunnelTestServer(t, "pw")
	defer srv.close()
	respAddr, closeResp := respPongServer(t)
	defer closeResp()

	a := newTunnelTestActor(t, srv)
	localAddr := tunnelTargetOf(t, a, adminCtx(), respAddr)

	// What a real redis client sends for PING, verbatim.
	conn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		t.Fatalf("write RESP PING: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 7)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read RESP reply: %v", err)
	}
	if string(buf) != "+PONG\r\n" {
		t.Fatalf("expected +PONG, got %q", buf)
	}
}
