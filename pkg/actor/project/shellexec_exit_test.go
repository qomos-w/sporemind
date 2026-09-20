package project

import (
	"context"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// stuckReader yields one chunk of output and then blocks until released,
// imitating a stdout/stderr pipe that a descendant process keeps open after the
// direct child has exited (or a slow consumer stalling the emit path). This is
// the condition under which finalizeDual used to give the sender only
// shellDrainWait to flush trailing output: the main goroutine returned first,
// the framework emitted KindEnd, and the later exit chunk was dropped —
// surfacing as "shell: stream ended without exit chunk".
type stuckReader struct {
	mu        sync.Mutex
	sent      bool
	release   chan struct{}
	blockedCh chan struct{}
	blockOnce sync.Once
	releaseOn sync.Once
}

func newStuckReader() *stuckReader {
	return &stuckReader{release: make(chan struct{}), blockedCh: make(chan struct{})}
}

func (r *stuckReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	first := !r.sent
	if first {
		r.sent = true
	}
	r.mu.Unlock()
	if first {
		n := copy(p, "started\n")
		return n, nil
	}
	r.blockOnce.Do(func() { close(r.blockedCh) })
	<-r.release
	return 0, io.EOF
}

func (r *stuckReader) unblock() { r.releaseOn.Do(func() { close(r.release) }) }

// exitZeroCommand returns a command that starts and exits immediately; the
// stalled pipe is supplied separately so the test is deterministic.
func exitZeroCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", "exit 0"}
	}
	return "sh", []string{"-c", "exit 0"}
}

// TestShellExec_PTYSingleStream drives the go-pty path directly (the
// shellExecUsePTY dispatch toggle defaults to pipe). It verifies the merged
// single-stream emission and, critically, the ProcessState-based exit code
// extraction: go-pty's Wait returns nil for non-zero exits on Windows, so a
// plain waitErr check would always report 0.
func TestShellExec_PTYSingleStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		testPTYStream(t, "cmd", []string{"/c", "echo pty-ok & exit /b 3"}, 3, "pty-ok")
	} else {
		testPTYStream(t, "sh", []string{"-c", "echo pty-ok; exit 3"}, 3, "pty-ok")
	}
}

func testPTYStream(t *testing.T, cmd string, args []string, wantCode int32, wantOut string) {
	t.Helper()
	tmp := t.TempDir()
	a, ctx := freshProject(t, tmp)

	emit := testutil.NewFakeEmitter()
	if err := a.handleShellExecPTY(ctx, domain.ShellExecReq{Command: cmd, Args: args}, emit); err != nil {
		t.Fatalf("handleShellExecPTY failed: %v", err)
	}

	var stdout string
	var gotExit bool
	for _, c := range emit.Chunks {
		ch, ok := c.(domain.ShellChunk)
		if !ok {
			t.Fatalf("unexpected chunk type %T", c)
		}
		switch ch.Kind {
		case "stdout":
			stdout += ch.Text
		case "stderr":
			// PTY merges streams; stderr-kind chunks must not appear.
			t.Fatalf("unexpected stderr-kind chunk in PTY mode: %q", ch.Text)
		case "exit":
			gotExit = true
			if ch.ExitCode != wantCode {
				t.Fatalf("exit code = %d, want %d (ProcessState extraction; stdout=%q stderr=%q)", ch.ExitCode, wantCode, ch.Stdout, ch.Stderr)
			}
		default:
			t.Fatalf("unexpected chunk kind %q", ch.Kind)
		}
	}
	if !gotExit {
		t.Fatal("no exit chunk emitted")
	}
	if !strings.Contains(stdout, wantOut) {
		t.Fatalf("stdout missing %q: %q", wantOut, stdout)
	}
}

// TestShellExec_PTYMergesStderr pins the PTY contract that stderr writes are
// delivered as stdout-kind chunks (a pty has no separate stderr channel).
func TestShellExec_PTYMergesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		testPTYStream(t, "cmd", []string{"/c", "echo err-stderr 1>&2"}, 0, "err-stderr")
	} else {
		testPTYStream(t, "sh", []string{"-c", "echo err-stderr 1>&2"}, 0, "err-stderr")
	}
}

// TestShellExec_PipeTimeout pins the interruptible-timeout contract: a command
// exceeding req.Timeout is cut off and reported as exit code 124 with
// Interrupted=true.
func TestShellExec_PipeTimeout(t *testing.T) {
	tmp := t.TempDir()
	a, ctx := freshProject(t, tmp)

	// sleep well past the timeout; Timeout is milliseconds (MinShellTimeoutMs
	// = 1000 silently upgrades sub-second values to the 30s default, so use
	// a value above the guard).
	cmd, args := sleepCommand(8)
	emit := testutil.NewFakeEmitter()
	err := a.handleShellExecPipe(ctx, domain.ShellExecReq{Command: cmd, Args: args, Timeout: 2000}, emit)
	if err != nil {
		t.Fatalf("handleShellExecPipe failed: %v", err)
	}

	for _, c := range emit.Chunks {
		ch, ok := c.(domain.ShellChunk)
		if !ok {
			t.Fatalf("unexpected chunk type %T", c)
		}
		if ch.Kind == "exit" {
			if ch.ExitCode != 124 {
				t.Fatalf("exit code = %d, want 124 (timeout)", ch.ExitCode)
			}
			if !ch.Interrupted {
				t.Fatal("exit chunk not marked Interrupted")
			}
			return
		}
	}
	t.Fatal("no exit chunk emitted")
}

// sleepCommand builds a command that sleeps for the given number of seconds.
// It must be the direct child process (no cmd/sh wrapper): the context kill
// then takes out the sleeper itself instead of orphaning it with the
// working directory still locked.
func sleepCommand(secs int) (string, []string) {
	if runtime.GOOS == "windows" {
		return "ping", []string{"-n", strconv.Itoa(secs + 1), "127.0.0.1"}
	}
	return "sleep", []string{strconv.Itoa(secs)}
}

// TestShellExec_ExitChunkSurvivesOpenPipe pins the contract that runDualStream
// returns only after the sender has emitted the exit chunk, even while the
// stdout/stderr pipe stays open. Without the fix the main goroutine bailed out
// after shellDrainWait and the exit chunk raced the handler's KindEnd.
func TestShellExec_ExitChunkSurvivesOpenPipe(t *testing.T) {
	name, args := exitZeroCommand()
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}

	emit := testutil.NewFakeEmitter()
	r := newStuckReader()
	defer r.unblock()

	done := make(chan error, 1)
	go func() {
		done <- runDualStream(cmd, context.Background(), emit, r, r, time.Second, "")
	}()

	select {
	case <-r.blockedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("reader never reached the stalled state")
	}

	// The handler must not return until the sender has emitted the exit chunk.
	// The pipe is still open here, so a handler that bailed out after
	// shellDrainWait would return with the exit chunk still missing.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runDualStream: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runDualStream did not return")
	}

	sawExit := false
	for _, c := range emit.Chunks {
		if ch, ok := c.(domain.ShellChunk); ok && ch.Kind == "exit" {
			sawExit = true
		}
	}
	if !sawExit {
		t.Fatal("exit chunk lost: handler returned while the pipe was still open")
	}
}
