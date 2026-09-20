package sshmanager

import (
	"bytes"
	"crypto/ed25519"
	crand "crypto/rand"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"golang.org/x/crypto/ssh"
)

// ---------------------------------------------------------------------------
// Pure-function tests
// ---------------------------------------------------------------------------

func TestResolveExecTimeout_DefaultAndClamp(t *testing.T) {
	def := defaultExecTimeoutMs * time.Millisecond
	cases := []struct {
		name string
		ms   int32
		want time.Duration
	}{
		{"zero falls back to default", 0, def},
		{"negative falls back to default", -10, def},
		{"below-min clamps to default", 1, def},
		{"near-min clamps to default", 999, def},
		{"min boundary honored", minExecTimeoutMs, time.Duration(minExecTimeoutMs) * time.Millisecond},
		{"explicit large honored", 30000, 30 * time.Second},
		{"above default honored", 120000, 2 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveExecTimeout(tc.ms); got != tc.want {
				t.Fatalf("resolveExecTimeout(%d) = %v, want %v", tc.ms, got, tc.want)
			}
		})
	}
}

func TestTruncateExecOutput_NoTruncation(t *testing.T) {
	got, truncated := truncateExecOutput("short", 100)
	if truncated || got != "short" {
		t.Fatalf("expected no truncation, got %q truncated=%v", got, truncated)
	}
	// Exactly at budget: no truncation.
	got, truncated = truncateExecOutput(strings.Repeat("a", 100), 100)
	if truncated || len(got) != 100 {
		t.Fatalf("at-budget should not truncate, got len=%d truncated=%v", len(got), truncated)
	}
}

func TestTruncateExecOutput_HeadTailKeep(t *testing.T) {
	const budget = 100
	s := strings.Repeat("x", budget+500) // well over budget
	got, truncated := truncateExecOutput(s, budget)
	if !truncated {
		t.Fatal("expected truncation flag set")
	}
	// Head and tail are preserved (utf8Boundary may trim one byte off the
	// head on pure ASCII, so assert with a safe margin rather than budget/2).
	margin := strings.Repeat("x", budget/4)
	if !strings.HasPrefix(got, margin) {
		t.Fatalf("expected head preserved")
	}
	if !strings.HasSuffix(got, margin) {
		t.Fatalf("expected tail preserved")
	}
	if !strings.Contains(got, "bytes truncated") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	// Result must be well under the original length.
	if len(got) >= len(s) {
		t.Fatalf("result not shorter: %d >= %d", len(got), len(s))
	}
}

func TestTruncateExecOutput_UTF8Boundary(t *testing.T) {
	// Build a string where a multibyte rune sits right at the head boundary.
	const budget = 8
	s := "a" + strings.Repeat("é", 20) // é is 2 bytes
	got, truncated := truncateExecOutput(s, budget)
	if !truncated {
		t.Fatal("expected truncation")
	}
	// Must not split a rune: result must be valid UTF-8.
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output is not valid UTF-8: %q", got)
	}
}

func TestCappedBuffer_OverflowDiscarded(t *testing.T) {
	b := &cappedBuffer{cap: 16}
	n, _ := b.Write([]byte("0123456789")) // 10 bytes
	if n != 10 {
		t.Fatalf("wrote %d, want 10", n)
	}
	n, _ = b.Write([]byte("ABCDEFGHIJ")) // would exceed cap
	if n != 10 {
		t.Fatalf("overflow write should report full length, got %d", n)
	}
	if b.String() != "0123456789ABCDEF" {
		t.Fatalf("unexpected buffer %q", b.String())
	}
	if !b.full {
		t.Fatal("expected full flag set")
	}
	// Further writes are silently dropped, no error.
	if _, err := b.Write([]byte("XYZ")); err != nil {
		t.Fatalf("post-overflow write should not error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// In-process SSH test server
// ---------------------------------------------------------------------------

// testSSHServer is a minimal SSH server that authenticates with a fixed
// password and dispatches "exec" requests through a tiny portable interpreter
// (no system shell dependency). Each accepted connection serves one exec.
type testSSHServer struct {
	listener net.Listener
	addr     string
	password string
	stopOnce sync.Once
	stopCh   chan struct{}

	mu          sync.Mutex
	connCount   int
	execCount   int
}

const testHostID = "host-1"
const testOwner = "admin-1"

func newTestSSHServer(t *testing.T, password string) *testSSHServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	_ = pub

	srv := &testSSHServer{listener: ln, addr: ln.Addr().String(), password: password, stopCh: make(chan struct{})}

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
			srv.mu.Lock()
			srv.connCount++
			srv.mu.Unlock()
			go srv.handleConn(nConn, config)
		}
	}()
	return srv
}

func (s *testSSHServer) close() {
	s.stopOnce.Do(func() {
		s.listener.Close()
	})
}

func (s *testSSHServer) handleConn(nConn net.Conn, config *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nConn, config)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		channel, chanReqs, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(channel, chanReqs)
	}
}

func (s *testSSHServer) handleSession(channel ssh.Channel, in <-chan *ssh.Request) {
	defer channel.Close()
	for req := range in {
		switch req.Type {
		case "exec":
			s.mu.Lock()
			s.execCount++
			s.mu.Unlock()
			var msg struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &msg)
			req.Reply(true, nil)
			stdout, stderr, code := interpretTestCommand(msg.Command)
			if len(stdout) > 0 {
				channel.Write(stdout)
			}
			if len(stderr) > 0 {
				channel.Stderr().Write(stderr)
			}
			channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
			return
		case "shell":
			req.Reply(false, nil) // not supported
		case "env":
			req.Reply(true, nil)
		default:
			req.Reply(false, nil)
		}
	}
}

// interpretTestCommand is a tiny portable command interpreter covering exactly
// the commands the tests need, avoiding any system-shell dependency.
func interpretTestCommand(cmd string) (stdout, stderr []byte, exitCode int) {
	cmd = strings.TrimSpace(cmd)
	switch {
	case strings.HasPrefix(cmd, "echo "):
		return []byte(strings.TrimPrefix(cmd, "echo ") + "\n"), nil, 0
	case strings.HasPrefix(cmd, "errln "):
		return nil, []byte(strings.TrimPrefix(cmd, "errln ") + "\n"), 0
	case strings.HasPrefix(cmd, "exit "):
		var code int
		_, _ = simpleAtoi(strings.TrimPrefix(cmd, "exit "), &code)
		return nil, nil, code
	case strings.HasPrefix(cmd, "sleep "):
		var secs int
		_, _ = simpleAtoi(strings.TrimPrefix(cmd, "sleep "), &secs)
		time.Sleep(time.Duration(secs) * time.Second)
		return []byte("slept\n"), nil, 0
	case strings.HasPrefix(cmd, "bigout "):
		var n int
		_, _ = simpleAtoi(strings.TrimPrefix(cmd, "bigout "), &n)
		return bytes.Repeat([]byte("z"), n), nil, 0
	case cmd == "fail":
		return nil, nil, 2
	default:
		return nil, []byte("unknown test command\n"), 127
	}
}

func simpleAtoi(s string, out *int) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(r-'0')
	}
	*out = n
	return n, nil
}

// ---------------------------------------------------------------------------
// Test actor + host fixture
// ---------------------------------------------------------------------------

func newExecTestActor(t *testing.T, srv *testSSHServer, dialCounter *int) *Actor {
	t.Helper()
	a := &Actor{
		store:       persist.NewFSPersist(t.TempDir()),
		Credentials: make(map[string]sshCredential),
		Commands:    []domain.SshCommandSnippet{},
		History:     []string{},
		execConns:   make(map[string]*execConn),
	}
	host, port := splitHostPort(t, srv.addr)
	a.Hosts = append(a.Hosts, domain.SshHost{
		ID: testHostID, Name: "test", Host: host, Port: port,
		User: "u", AuthMethod: "password",
	})
	a.Credentials[testHostID] = sshCredential{Password: srv.password}

	a.execDial = func(h *domain.SshHost) (*ssh.Client, error) {
		if dialCounter != nil {
			*dialCounter++
		}
		return buildSSHClient(h)
	}
	return a
}

func splitHostPort(t *testing.T, addr string) (string, int32) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	var port int
	if _, err := simpleAtoi(p, &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return h, int32(port)
}

// ---------------------------------------------------------------------------
// Integration tests against the in-process SSH server
// ---------------------------------------------------------------------------

func TestExec_SuccessStdoutExitCode(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	dials := 0
	a := newExecTestActor(t, srv, &dials)

	resp, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "echo hello"})
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if resp.ExitCode != 0 || resp.Stdout != "hello\n" {
		t.Fatalf("unexpected resp: %+v", resp)
	}
	if resp.DurationMs < 0 {
		t.Fatalf("duration must be non-negative, got %d", resp.DurationMs)
	}
	if dials != 1 {
		t.Fatalf("expected 1 dial, got %d", dials)
	}
}

func TestExec_NonZeroExitCode(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	a := newExecTestActor(t, srv, nil)

	resp, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "fail"})
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if resp.ExitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", resp.ExitCode)
	}
	// No error returned: non-zero exit is a normal result, not a connection error.
}

func TestExec_StderrSeparated(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	a := newExecTestActor(t, srv, nil)

	resp, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "errln boom"})
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if resp.ExitCode != 0 || resp.Stderr != "boom\n" || resp.Stdout != "" {
		t.Fatalf("unexpected resp: %+v", resp)
	}
}

func TestExec_ConnectionReusedAcrossCalls(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	dials := 0
	a := newExecTestActor(t, srv, &dials)

	if _, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "echo one"}); err != nil {
		t.Fatalf("first exec: %v", err)
	}
	if _, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "echo two"}); err != nil {
		t.Fatalf("second exec: %v", err)
	}
	if dials != 1 {
		t.Fatalf("expected connection reuse (1 dial), got %d dials", dials)
	}
	// Cache should hold exactly one connection for this owner/host.
	a.mu.Lock()
	n := len(a.execConns)
	a.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 cached exec conn, got %d", n)
	}
}

func TestExec_ReconnectAfterDisconnect(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	dials := 0
	a := newExecTestActor(t, srv, &dials)

	// First call establishes + caches the connection.
	if _, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "echo first"}); err != nil {
		t.Fatalf("first exec: %v", err)
	}
	if dials != 1 {
		t.Fatalf("expected 1 dial after first exec, got %d", dials)
	}

	// Simulate a dropped connection: close the cached client.
	a.mu.Lock()
	ec := a.execConns[execConnKey(testOwner, testHostID)]
	a.mu.Unlock()
	if ec == nil || ec.client == nil {
		t.Fatalf("expected cached connection")
	}
	ec.client.Close()

	// Next exec must detect the dead client, reconnect once, and succeed.
	resp, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "echo second"})
	if err != nil {
		t.Fatalf("reconnect exec failed: %v", err)
	}
	if resp.ExitCode != 0 || resp.Stdout != "second\n" {
		t.Fatalf("unexpected resp after reconnect: %+v", resp)
	}
	if dials != 2 {
		t.Fatalf("expected 2 dials (1 initial + 1 reconnect), got %d", dials)
	}
}

func TestExec_TimeoutKillsCommand(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	a := newExecTestActor(t, srv, nil)

	start := time.Now()
	resp, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "sleep 10", Timeout: 1000})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if resp.ExitCode != timeoutExitCode {
		t.Fatalf("expected timeout exit code %d, got %d", timeoutExitCode, resp.ExitCode)
	}
	if !strings.Contains(resp.Stderr, "timed out") {
		t.Fatalf("expected timeout message in stderr, got %q", resp.Stderr)
	}
	// Should return well before the 10s sleep completes.
	if elapsed > 5*time.Second {
		t.Fatalf("timeout did not fire promptly, elapsed %v", elapsed)
	}
}

func TestExec_LargeOutputTruncated(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	a := newExecTestActor(t, srv, nil)

	// Request more than the stdout budget.
	resp, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "bigout 60000"})
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if !resp.Truncated {
		t.Fatal("expected Truncated=true")
	}
	if len(resp.Stdout) >= 60000 {
		t.Fatalf("stdout not truncated, len=%d", len(resp.Stdout))
	}
	if !strings.Contains(resp.Stdout, "bytes truncated") {
		t.Fatalf("expected truncation marker, got %q", resp.Stdout)
	}
}

func TestExec_RejectsAnonymous(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	a := newExecTestActor(t, srv, nil)

	if _, err := a.handleExec(anonCtx(), domain.SshExecReq{HostID: testHostID, Command: "echo x"}); err == nil {
		t.Fatal("anonymous caller must be rejected")
	}
}

func TestExec_HostNotFound(t *testing.T) {
	a := &Actor{execConns: make(map[string]*execConn)}
	if _, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: "nope", Command: "echo x"}); err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestExec_EmptyCommandRejected(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	a := newExecTestActor(t, srv, nil)
	if _, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "   "}); err == nil {
		t.Fatal("empty command must be rejected")
	}
}

// TestExec_ConcurrentColdStartNoDuplicateEntry launches several execs against
// the same (owner, host) simultaneously before any connection is cached. Even
// if multiple dials race, the cache must end with exactly one entry (no leaked
// duplicate connections).
func TestExec_ConcurrentColdStartNoDuplicateEntry(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	a := newExecTestActor(t, srv, nil)

	const n = 6
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "echo c"}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent exec failed: %v", err)
	}
	a.mu.Lock()
	entries := len(a.execConns)
	a.mu.Unlock()
	if entries != 1 {
		t.Fatalf("expected exactly 1 cached exec conn after concurrent cold start, got %d", entries)
	}
}

// ---------------------------------------------------------------------------
// Lifecycle: OnStop + idle reclamation
// ---------------------------------------------------------------------------

func TestOnStop_ClosesExecConnections(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	dials := 0
	a := newExecTestActor(t, srv, &dials)

	if _, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "echo x"}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	a.mu.Lock()
	had := len(a.execConns)
	a.mu.Unlock()
	if had != 1 {
		t.Fatalf("expected 1 cached conn before stop, got %d", had)
	}

	if err := a.OnStop(nil); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	a.mu.Lock()
	n := len(a.execConns)
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected exec cache cleared after OnStop, got %d", n)
	}
}

func TestSweepIdleExecConns_EvictsIdleKeepsInFlight(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	a := newExecTestActor(t, srv, nil)

	// Establish a real connection to cache.
	if _, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "echo x"}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	key := execConnKey(testOwner, testHostID)

	// Make it look long-idle but in-flight → must be kept.
	a.mu.Lock()
	ec := a.execConns[key]
	ec.lastUsed = time.Now().Add(-2 * execIdleTimeout)
	ec.inFlight = 1
	a.mu.Unlock()
	a.sweepIdleExecConns()
	a.mu.Lock()
	if a.execConns[key] == nil {
		t.Fatal("in-flight idle conn must not be evicted")
	}
	// Now mark idle and not in-flight → must be evicted.
	ec.inFlight = 0
	a.mu.Unlock()
	a.sweepIdleExecConns()
	a.mu.Lock()
	gone := a.execConns[key] == nil
	a.mu.Unlock()
	if !gone {
		t.Fatal("idle, not-in-flight conn must be evicted")
	}
}

func TestSweepIdleExecConns_KeepsRecent(t *testing.T) {
	srv := newTestSSHServer(t, "pw")
	defer srv.close()
	a := newExecTestActor(t, srv, nil)

	if _, err := a.handleExec(adminCtx(), domain.SshExecReq{HostID: testHostID, Command: "echo x"}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	// lastUsed is "now" → not idle; sweep must keep it.
	a.sweepIdleExecConns()
	a.mu.Lock()
	n := len(a.execConns)
	a.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected recent conn kept, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// Ensure required context types still satisfy interfaces (compile-time guard)
// ---------------------------------------------------------------------------

var _ actor.PureContext = sshTestCtx{}
var _ id.Identity = id.Identity{}

// io.Reader/Writer sanity for the capped buffer.
var _ io.Writer = (*cappedBuffer)(nil)
