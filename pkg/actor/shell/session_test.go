package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// newSessionTestActor wires an Actor through OnStart with a FakeCtx so the
// session handlers run against a real in-actor registry and events land in
// the fake context's emitted-event log (no live EventBus needed).
func TestInteractiveShellArgs(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"bash", nil},
		{"gitbash", nil},
		{"sh", nil},
		{"powershell", []string{"-NoLogo", "-NoExit"}},
		{"cmd", []string{"/Q"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := interactiveShellArgs(tc.name)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("interactiveShellArgs(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestBuildSessionCmdUsesWorkingDirectory(t *testing.T) {
	sh, err := resolveShell()
	if err != nil {
		t.Skipf("no shell available on this machine: %v", err)
	}
	workingDirectory := t.TempDir()
	cmd := buildSessionCmd(context.Background(), sh, workingDirectory)
	if cmd.Dir != workingDirectory {
		t.Fatalf("command directory = %q, want %q", cmd.Dir, workingDirectory)
	}
}

func newSessionTestActor(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	if _, err := resolveShell(); err != nil {
		t.Skipf("no shell available on this machine: %v", err)
	}
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if err := a.OnStart(ctx); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	t.Cleanup(a.closeAllSessions)
	return a, ctx
}

// waitSessionEvent polls the fake context's emitted-event log until cond
// matches at least one shell.session_output event. Fails the test on timeout.
func waitSessionEvent(
	t *testing.T,
	ctx *testutil.FakeCtx,
	timeout time.Duration,
	what string,
	cond func(domain.ShellSessionOutputEvent) bool,
) domain.ShellSessionOutputEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, ev := range ctx.EmittedEventsSnapshot() {
			if ev.Kind != SessionOutputEventKind {
				continue
			}
			payload, ok := ev.Payload.(domain.ShellSessionOutputEvent)
			if !ok {
				continue
			}
			if cond(payload) {
				return payload
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
	return domain.ShellSessionOutputEvent{}
}

// waitSessionGone polls until the session id is no longer in the registry.
func waitSessionGone(t *testing.T, a *Actor, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := a.lookupSession(id); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for session %q to be removed from the registry", id)
}

// TestSessionLifecycle_OpenWriteReadClose is the acceptance lifecycle test:
// open a session, write a simple echo command, read the output from the
// shell.session_output event stream, then close it. It runs in whatever mode
// the host supports (PTY incl. Windows ConPTY, or the pipe fallback).
func TestSessionLifecycle_OpenWriteReadClose(t *testing.T) {
	a, ctx := newSessionTestActor(t)

	resp, err := a.handleSessionOpen(ctx, domain.ShellSessionOpenReq{})
	if err != nil {
		t.Fatalf("session_open: %v", err)
	}
	if resp.SessionID == "" {
		t.Fatal("session_open: empty sessionId")
	}
	if resp.Mode != "pty" && resp.Mode != "pipe" {
		t.Fatalf("session_open: unexpected mode %q", resp.Mode)
	}
	if _, err := a.lookupSession(resp.SessionID); err != nil {
		t.Fatalf("session not registered after open: %v", err)
	}

	// `echo <text>` behaves identically across bash/sh/pwsh/cmd (the resolved
	// shell family depends on the host platform); both the echoed command and
	// its output contain the marker.
	const marker = "hello-session"
	if _, err := a.handleSessionWrite(ctx, domain.ShellSessionWriteReq{
		SessionID: resp.SessionID,
		Data:      "echo " + marker + "\r\n",
	}); err != nil {
		t.Fatalf("session_write: %v", err)
	}

	got := waitSessionEvent(t, ctx, 10*time.Second,
		"output containing "+marker,
		func(ev domain.ShellSessionOutputEvent) bool {
			return ev.SessionID == resp.SessionID &&
				(ev.Kind == "stdout" || ev.Kind == "stderr") &&
				strings.Contains(ev.Data, marker)
		})
	t.Logf("session %s (mode=%s) produced output chunk kind=%q data=%q",
		got.SessionID, resp.Mode, got.Kind, got.Data)

	if _, err := a.handleSessionClose(ctx, domain.ShellSessionCloseReq{
		SessionID: resp.SessionID,
	}); err != nil {
		t.Fatalf("session_close: %v", err)
	}

	exit := waitSessionEvent(t, ctx, 5*time.Second,
		"exit chunk after close",
		func(ev domain.ShellSessionOutputEvent) bool {
			return ev.SessionID == resp.SessionID && ev.Kind == "exit"
		})
	if exit.Mode != resp.Mode {
		t.Errorf("exit chunk Mode = %q, want %q", exit.Mode, resp.Mode)
	}
	waitSessionGone(t, a, resp.SessionID, 5*time.Second)
}

// TestSessionPipeFallback_Lifecycle forces the pipe path (the "PTY unavailable
// → fallback" route) deterministically on every platform and asserts the
// Mode is "pipe", resize is a no-op, output is delivered, and close cleans up.
func TestSessionPipeFallback_Lifecycle(t *testing.T) {
	a, ctx := newSessionTestActor(t)
	a.sessionForcePipe = true

	resp, err := a.handleSessionOpen(ctx, domain.ShellSessionOpenReq{})
	if err != nil {
		t.Fatalf("session_open: %v", err)
	}
	if resp.Mode != "pipe" {
		t.Fatalf("forced-pipe open: Mode = %q, want %q", resp.Mode, "pipe")
	}

	// Resize is a documented no-op in pipe mode (no controlling terminal).
	if _, err := a.handleSessionResize(ctx, domain.ShellSessionResizeReq{
		SessionID: resp.SessionID, Cols: 120, Rows: 40,
	}); err != nil {
		t.Fatalf("session_resize in pipe mode: %v", err)
	}

	const marker = "hello-pipe"
	if _, err := a.handleSessionWrite(ctx, domain.ShellSessionWriteReq{
		SessionID: resp.SessionID,
		Data:      "echo " + marker + "\r\n",
	}); err != nil {
		t.Fatalf("session_write: %v", err)
	}
	waitSessionEvent(t, ctx, 10*time.Second,
		"pipe output containing "+marker,
		func(ev domain.ShellSessionOutputEvent) bool {
			return ev.SessionID == resp.SessionID && strings.Contains(ev.Data, marker)
		})

	if _, err := a.handleSessionClose(ctx, domain.ShellSessionCloseReq{
		SessionID: resp.SessionID,
	}); err != nil {
		t.Fatalf("session_close: %v", err)
	}
	waitSessionEvent(t, ctx, 5*time.Second,
		"pipe exit chunk after close",
		func(ev domain.ShellSessionOutputEvent) bool {
			return ev.SessionID == resp.SessionID && ev.Kind == "exit"
		})
	waitSessionGone(t, a, resp.SessionID, 5*time.Second)
}

// TestSessionSelfExit_EmitsExitAndCleansUp drives the process to exit on its
// own (terminal `exit` plus stdin EOF) and verifies the reap goroutine emits
// the exit chunk and removes the session — without an explicit session_close.
func TestSessionSelfExit_EmitsExitAndCleansUp(t *testing.T) {
	a, ctx := newSessionTestActor(t)
	a.sessionForcePipe = true

	resp, err := a.handleSessionOpen(ctx, domain.ShellSessionOpenReq{})
	if err != nil {
		t.Fatalf("session_open: %v", err)
	}

	// `exit` terminates bash/sh/pwsh/cmd; closing stdin guarantees termination
	// regardless of how the shell parses the command (EOF-driven exit).
	if _, err := a.handleSessionWrite(ctx, domain.ShellSessionWriteReq{
		SessionID: resp.SessionID,
		Data:      "exit\r\n",
	}); err != nil {
		t.Fatalf("session_write: %v", err)
	}
	s, err := a.lookupSession(resp.SessionID)
	if err != nil {
		t.Fatalf("lookup session: %v", err)
	}
	_ = s.stdin.Close()

	exit := waitSessionEvent(t, ctx, 10*time.Second,
		"exit chunk after self-exit",
		func(ev domain.ShellSessionOutputEvent) bool {
			return ev.SessionID == resp.SessionID && ev.Kind == "exit"
		})
	// A self-exit (not an explicit close) must NOT be reported as "session
	// closed" — that reason is reserved for session_close / actor stop.
	if exit.Data == "session closed" {
		t.Errorf("self-exit reported explicit-close reason: %q", exit.Data)
	}
	waitSessionGone(t, a, resp.SessionID, 5*time.Second)
}

// TestSessionWrite_UnknownSession and TestSessionClose_UnknownSession verify
// the registry's not-found error surfaces to callers.
func TestSessionWrite_UnknownSession(t *testing.T) {
	a, ctx := newSessionTestActor(t)
	_, err := a.handleSessionWrite(ctx, domain.ShellSessionWriteReq{
		SessionID: "does-not-exist", Data: "x",
	})
	if err == nil {
		t.Fatal("expected error writing to unknown session")
	}
}

func TestSessionClose_UnknownSession(t *testing.T) {
	a, ctx := newSessionTestActor(t)
	_, err := a.handleSessionClose(ctx, domain.ShellSessionCloseReq{
		SessionID: "does-not-exist",
	})
	if err == nil {
		t.Fatal("expected error closing unknown session")
	}
}

// TestSessionResize_PTYAppliesWhenAvailable exercises the real pty resize
// path on platforms where PTY is supported (Linux/macOS/Windows ConPTY).
func TestSessionResize_PTYAppliesWhenAvailable(t *testing.T) {
	a, ctx := newSessionTestActor(t)

	resp, err := a.handleSessionOpen(ctx, domain.ShellSessionOpenReq{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("session_open: %v", err)
	}
	if resp.Mode != "pty" {
		t.Skipf("host did not provide a PTY (mode=%q); skipping pty resize test", resp.Mode)
	}
	if _, err := a.handleSessionResize(ctx, domain.ShellSessionResizeReq{
		SessionID: resp.SessionID, Cols: 100, Rows: 40,
	}); err != nil {
		t.Fatalf("session_resize (pty): %v", err)
	}
	if _, err := a.handleSessionClose(ctx, domain.ShellSessionCloseReq{
		SessionID: resp.SessionID,
	}); err != nil {
		t.Fatalf("session_close: %v", err)
	}
}
