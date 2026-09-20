package project

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// TestGitCmd_StdinIsolated asserts gitCmd wires stdin to a closed reader so a
// git subcommand can never block waiting for an interactive passphrase /
// credential prompt (the classic cause of project-actor freezes).
func TestGitCmd_StdinIsolated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := gitCmd(ctx, ".", "rev-parse", "--git-dir")
	if cmd.Stdin == nil {
		t.Fatal("gitCmd: cmd.Stdin is nil; git could block on a prompt")
	}
	// The reader must yield EOF immediately (empty bytes.Reader).
	r, ok := cmd.Stdin.(io.Reader)
	if !ok {
		t.Fatalf("gitCmd: cmd.Stdin is %T, not an io.Reader", cmd.Stdin)
	}
	buf := make([]byte, 1)
	n, err := r.Read(buf)
	if err != io.EOF || n != 0 {
		t.Fatalf("gitCmd: stdin should be EOF-only, got n=%d err=%v", n, err)
	}
}

// TestGitCtx_AppliesTimeout asserts gitCtx returns a context with a deadline
// within gitTimeout, so a hung git operation is bounded.
func TestGitCtx_AppliesTimeout(t *testing.T) {
	ctx, cancel := gitCtx(nil)
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("gitCtx: context has no deadline")
	}
	remaining := time.Until(dl)
	if remaining <= 0 || remaining > gitTimeout {
		t.Fatalf("gitCtx: deadline not within %s; remaining=%s", gitTimeout, remaining)
	}
}

// TestGitCtx_CellDoneCancels asserts that closing the done channel (cell
// shutdown) cancels the derived context.
func TestGitCtx_CellDoneCancels(t *testing.T) {
	done := make(chan struct{})
	ctx, cancel := gitCtx(done)
	defer cancel()
	close(done)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("gitCtx: closing done did not cancel the context within 1s")
	}
}

// TestGitRun_TimeoutSurfacesClearError verifies that when the hard git
// timeout fires, gitRun returns an error that explicitly names the timeout
// instead of the generic empty/exit-error message it would otherwise produce.
func TestGitRun_TimeoutSurfacesClearError(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	_, err := gitRunWithTimeout(1*time.Nanosecond, nil, dir, "rev-parse", "--git-dir")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "timed out") {
		t.Fatalf("timeout error should explicitly mention 'timed out', got: %q", msg)
	}
	if !strings.Contains(msg, "1ns") {
		t.Fatalf("timeout error should mention the timeout duration, got: %q", msg)
	}
}

// TestGitRun_DoesNotHangOnPrompt exercises the full gitRun path against a real
// repo, ensuring the command completes promptly (stdin isolation + timeout).
func TestGitRun_DoesNotHangOnPrompt(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	start := time.Now()
	out, err := gitRun(nil, dir, "rev-parse", "--git-dir")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("gitRun rev-parse: %v", err)
	}
	if out == "" {
		t.Fatal("gitRun: empty output")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("gitRun took %s; expected sub-second (timeout/stdin not wired?)", elapsed)
	}
}
