package sshmanager

import (
	"runtime"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// ---------------------------------------------------------------------------
// Pure-function tests
// ---------------------------------------------------------------------------

func TestStripEchoPrefix(t *testing.T) {
	cases := []struct {
		name   string
		output string
		cmd    string
		want   string
	}{
		{
			name:   "echoed command removed",
			output: "echo hello\r\nhello\r\n$ ",
			cmd:    "echo hello",
			want:   "hello\n$ ",
		},
		{
			name:   "no echo prefix (echo off)",
			output: "hello\r\n$ ",
			cmd:    "echo hello",
			want:   "hello\r\n$ ",
		},
		{
			name:   "empty output",
			output: "",
			cmd:    "ls",
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripEchoPrefix(tc.output, tc.cmd)
			if got != tc.want {
				t.Errorf("stripEchoPrefix(%q, %q) = %q, want %q", tc.output, tc.cmd, got, tc.want)
			}
		})
	}
}

func TestStripANSI(t *testing.T) {
	input := "\x1b[32mgreen\x1b[0m text \x1b[1;31mbold red\x1b[0m"
	want := "green text bold red"
	if got := stripANSI(input); got != want {
		t.Errorf("stripANSI = %q, want %q", got, want)
	}
}

func TestResolveShellRunTimeout(t *testing.T) {
	cases := []struct {
		reqMs int32
		want  int64 // milliseconds
	}{
		{0, 30000},      // default
		{-1, 30000},     // negative → default
		{500, 30000},     // below min → default
		{1000, 1000},    // exactly min
		{5000, 5000},    // normal
		{120000, 120000}, // large
	}
	for _, tc := range cases {
		got := resolveShellRunTimeout(tc.reqMs).Milliseconds()
		if got != tc.want {
			t.Errorf("resolveShellRunTimeout(%d) = %dms, want %dms", tc.reqMs, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Handler integration tests (mock PTY session)
// ---------------------------------------------------------------------------

// mockStdinPipe is an io.WriteCloser that captures written bytes for test
// inspection without a real SSH connection.
type mockStdinPipe struct {
	written []byte
}

func (m *mockStdinPipe) Write(p []byte) (int, error) {
	m.written = append(m.written, p...)
	return len(p), nil
}

func (m *mockStdinPipe) Close() error { return nil }

// hasCommand returns true once the handler has written the command (with
// sentinel) to stdin, signalling the test goroutine that the broadcaster is
// subscribed and PTY output can be produced.
func (m *mockStdinPipe) hasCommand() bool {
	return len(m.written) > 0
}

// newMockSession creates a session with a mock stdin, a real broadcaster, and
// a real *ssh.Client (dialed against a minimal test SSH server) so that
// isConnected() returns true. Tests write PTY output to sess.out to simulate
// command output, then call handleShellRun which reads from the broadcaster.
// The *ssh.Client is not used by shell_run — it only satisfies the
// isConnected() guard (s.client != nil).
func newMockSession(t *testing.T) *session {
	t.Helper()
	srv := newTestSSHServer(t, "test")
	t.Cleanup(srv.close)
	host, port := splitHostPort(t, srv.addr)
	client, err := buildSSHClient(&domain.SshHost{
		Host: host, Port: port, User: "u", AuthMethod: "password", Password: "test",
	})
	if err != nil {
		t.Fatalf("dial test SSH server: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return &session{
		id:        "test-run-session",
		out:       newShellBroadcaster(),
		stdin:     &mockStdinPipe{},
		client:    client,
		connected: true,
	}
}

// TestShellRunHappyPath verifies the core flow: shell_run writes the command
// with sentinel to stdin, the PTY produces echoed command + output + sentinel,
// and the handler returns the output with the correct exit code.
func TestShellRunHappyPath(t *testing.T) {
	sess := newMockSession(t)
	defer sess.close()

	a := &Actor{
		sessions: map[string]*session{sess.id: sess},
	}

	// Simulate the remote PTY response in a goroutine: after a brief delay,
	// write the echoed command + output + sentinel to the broadcaster.
	go func() {
		// Wait for the handler to subscribe + write the command before
		// producing PTY output, so output arrives on the live channel
		// (not the replay buffer that the handler skips).
		for !sess.stdin.(*mockStdinPipe).hasCommand() {
			runtime.Gosched()
		}
		ptyOutput := "echo hello\r\nhello\r\n__SR_EOF__0__\r\n"
		sess.out.Write([]byte(ptyOutput))
	}()

	resp, err := a.handleShellRun(adminCtx(), domain.SshShellRunReq{
		SessionID: sess.id,
		Command:  "echo hello",
	})
	if err != nil {
		t.Fatalf("handleShellRun failed: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", resp.ExitCode)
	}
	if resp.TimedOut {
		t.Errorf("TimedOut = true, want false")
	}
	if !strings.Contains(resp.Output, "hello") {
		t.Errorf("Output = %q, want to contain 'hello'", resp.Output)
	}
	if strings.Contains(resp.Output, "__SR_EOF__") {
		t.Errorf("Output must not contain sentinel: %q", resp.Output)
	}
}

// TestShellRunNonZeroExitCode verifies the sentinel captures a non-zero exit
// code from the command.
func TestShellRunNonZeroExitCode(t *testing.T) {
	sess := newMockSession(t)
	defer sess.close()

	a := &Actor{
		sessions: map[string]*session{sess.id: sess},
	}

	go func() {
		for !sess.stdin.(*mockStdinPipe).hasCommand() {
			runtime.Gosched()
		}
		ptyOutput := "false\r\n__SR_EOF__1__\r\n"
		sess.out.Write([]byte(ptyOutput))
	}()

	resp, err := a.handleShellRun(adminCtx(), domain.SshShellRunReq{
		SessionID: sess.id,
		Command:  "false",
	})
	if err != nil {
		t.Fatalf("handleShellRun failed: %v", err)
	}
	if resp.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", resp.ExitCode)
	}
}

// TestShellRunTimeout verifies that when the sentinel never arrives, the
// handler returns after the timeout with TimedOut=true.
func TestShellRunTimeout(t *testing.T) {
	sess := newMockSession(t)
	defer sess.close()

	a := &Actor{
		sessions: map[string]*session{sess.id: sess},
	}

	// No goroutine writing output — the sentinel never arrives.
	resp, err := a.handleShellRun(adminCtx(), domain.SshShellRunReq{
		SessionID: sess.id,
		Command:   "sleep 999",
		TimeoutMs: 1000, // 1s
	})
	if err != nil {
		t.Fatalf("handleShellRun failed: %v", err)
	}
	if !resp.TimedOut {
		t.Errorf("TimedOut = false, want true")
	}
	if resp.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", resp.ExitCode)
	}
}

// TestShellRunRejectsAnonymous verifies that anonymous callers are rejected
// by requireSSHToolCaller (same auth gate as shell_input).
func TestShellRunRejectsAnonymous(t *testing.T) {
	sess := newMockSession(t)
	defer sess.close()

	a := &Actor{
		sessions: map[string]*session{sess.id: sess},
	}

	if _, err := a.handleShellRun(anonCtx(), domain.SshShellRunReq{
		SessionID: sess.id,
		Command:   "ls",
	}); err == nil {
		t.Fatal("anonymous caller must be rejected")
	}
}

// TestShellRunAcceptsAgentRole verifies that agent-role callers pass the
// auth gate (the whole point of shell_run: agent-accessible session commands).
func TestShellRunAcceptsAgentRole(t *testing.T) {
	sess := newMockSession(t)
	defer sess.close()

	a := &Actor{
		sessions: map[string]*session{sess.id: sess},
	}

	go func() {
		for !sess.stdin.(*mockStdinPipe).hasCommand() {
			runtime.Gosched()
		}
		sess.out.Write([]byte("whoami\r\nroot\r\n__SR_EOF__0__\r\n"))
	}()

	resp, err := a.handleShellRun(agentCtx(), domain.SshShellRunReq{
		SessionID: sess.id,
		Command:   "whoami",
	})
	if err != nil {
		t.Fatalf("agent caller rejected: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", resp.ExitCode)
	}
}

// TestShellRunDisconnectedSession verifies that a disconnected session
// returns an error without attempting to write.
func TestShellRunDisconnectedSession(t *testing.T) {
	sess := newMockSession(t)
	sess.connected = false
	defer sess.close()

	a := &Actor{
		sessions: map[string]*session{sess.id: sess},
	}

	if _, err := a.handleShellRun(adminCtx(), domain.SshShellRunReq{
		SessionID: sess.id,
		Command:   "ls",
	}); err == nil {
		t.Fatal("expected error for disconnected session")
	}
}

// TestShellRunSessionNotFound verifies that an unknown session ID is rejected.
func TestShellRunSessionNotFound(t *testing.T) {
	a := &Actor{
		sessions: map[string]*session{},
	}

	if _, err := a.handleShellRun(adminCtx(), domain.SshShellRunReq{
		SessionID: "nonexistent",
		Command:   "ls",
	}); err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}