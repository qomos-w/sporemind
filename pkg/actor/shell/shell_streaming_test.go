package shell

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// fakeEmitter collects emitted chunks for assertion. Goroutine-safe:
// Send is invoked from the sender goroutine while tests read chunks
// after handler return.
type fakeEmitter struct {
	mu       sync.Mutex
	chunks   []domain.ShellChunk
	doneCh   chan struct{}
	doneOnce sync.Once
}

func newFakeEmitter() *fakeEmitter {
	return &fakeEmitter{doneCh: make(chan struct{})}
}

func (e *fakeEmitter) Send(chunk any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.chunks = append(e.chunks, chunk.(domain.ShellChunk))
	return nil
}

func (e *fakeEmitter) Done() <-chan struct{} { return e.doneCh }

func (e *fakeEmitter) close() { e.doneOnce.Do(func() { close(e.doneCh) }) }

func (e *fakeEmitter) snapshot() []domain.ShellChunk {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]domain.ShellChunk, len(e.chunks))
	copy(out, e.chunks)
	return out
}

func (e *fakeEmitter) exitChunk() (domain.ShellChunk, bool) {
	for _, c := range e.snapshot() {
		if c.Kind == "exit" {
			return c, true
		}
	}
	return domain.ShellChunk{}, false
}

func (e *fakeEmitter) stdoutText() string {
	var b strings.Builder
	for _, c := range e.snapshot() {
		if c.Kind == "stdout" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func (e *fakeEmitter) stderrText() string {
	var b strings.Builder
	for _, c := range e.snapshot() {
		if c.Kind == "stderr" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// fakeShellContext embeds actor.Context (nil) and overrides only the
// methods handleBash/handleExec touch: Lifecycle, Done, Logger.
// Pattern copied from pkg/actor/agent/compaction_test.go:639.
type fakeShellContext struct {
	actor.Context
	lifecycleCtx context.Context
}

func (c *fakeShellContext) Lifecycle() context.Context { return c.lifecycleCtx }
func (c *fakeShellContext) Done() <-chan struct{} {
	return c.lifecycleCtx.Done()
}
func (c *fakeShellContext) Logger() actor.Logger { return fakeShellLogger{} }
func (c *fakeShellContext) Self() ref.Ref        { return fakeShellRef{} }

type fakeShellRef struct{}

func (fakeShellRef) ID() id.ActorID {
	aid, _ := id.Parse("00000000000000000000000000000001")
	return aid
}
func (fakeShellRef) Service() (string, bool) { return "", false }
func (fakeShellRef) Invoke(context.Context, string, any, ...map[string]string) *invoke.Call {
	return nil
}

type fakeShellLogger struct{}

func (fakeShellLogger) Debug(string, ...any) {}
func (fakeShellLogger) Info(string, ...any)  {}
func (fakeShellLogger) Warn(string, ...any)  {}
func (fakeShellLogger) Error(string, ...any) {}

func newCtx() *fakeShellContext {
	return &fakeShellContext{lifecycleCtx: context.Background()}
}

// TestHandleBashStreaming_HelloEmitsExitChunk verifies the happy path:
// echo hello produces at least one stdout chunk and a single exit chunk
// with ExitCode=0 and Stdout containing the echoed text.
func TestHandleBashStreaming_HelloEmitsExitChunk(t *testing.T) {
	a := &Actor{}
	emit := newFakeEmitter()
	err := a.handleBash(newCtx(), domain.ShellBashReq{
		Command: "echo hello",
	}, emit)
	if err != nil {
		t.Fatalf("handleBash returned error: %v", err)
	}
	exit, ok := emit.exitChunk()
	if !ok {
		t.Fatalf("expected exit chunk, got: %+v", emit.snapshot())
	}
	if exit.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", exit.ExitCode)
	}
	if !strings.Contains(exit.Stdout, "hello") {
		t.Errorf("exit.Stdout = %q, want substring %q", exit.Stdout, "hello")
	}
	if !strings.Contains(emit.stdoutText(), "hello") {
		t.Errorf("streamed stdout = %q, want substring %q", emit.stdoutText(), "hello")
	}
}

// TestHandleBashStreaming_SeparateStdoutStderr verifies that stdout and
// stderr arrive as separate chunks with the right Kind.
func TestHandleBashStreaming_SeparateStdoutStderr(t *testing.T) {
	a := &Actor{}
	emit := newFakeEmitter()
	err := a.handleBash(newCtx(), domain.ShellBashReq{
		Command: "sh",
		Args:    []string{"-c", "echo out; echo err 1>&2; exit 3"},
	}, emit)
	if err != nil {
		t.Fatalf("handleBash returned error: %v", err)
	}
	exit, ok := emit.exitChunk()
	if !ok {
		t.Fatalf("missing exit chunk")
	}
	if exit.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", exit.ExitCode)
	}
	if !strings.Contains(emit.stdoutText(), "out") {
		t.Errorf("stdout chunks = %q, want 'out'", emit.stdoutText())
	}
	if !strings.Contains(emit.stderrText(), "err") {
		t.Errorf("stderr chunks = %q, want 'err'", emit.stderrText())
	}
	if strings.Contains(emit.stdoutText(), "err") {
		t.Errorf("stdout leaked stderr: %q", emit.stdoutText())
	}
}

// TestHandleBashStreaming_TruncatesLargeOutput verifies the exit chunk
// applies head/tail truncation while streamed chunks carry the full output.
func TestHandleBashStreaming_TruncatesLargeOutput(t *testing.T) {
	a := &Actor{}
	emit := newFakeEmitter()
	err := a.handleBash(newCtx(), domain.ShellBashReq{
		Command: "sh",
		Args:    []string{"-c", "yes hello | head -c 50000"},
	}, emit)
	if err != nil {
		t.Fatalf("handleBash returned error: %v", err)
	}
	exit, ok := emit.exitChunk()
	if !ok {
		t.Fatalf("missing exit chunk")
	}
	if !exit.Truncated {
		t.Errorf("Truncated = false, want true (output > 30KB)")
	}
	if len(exit.Stdout) > maxStdoutBytes+200 { // allow truncation marker slack
		t.Errorf("exit.Stdout len = %d, want <= %d+slack", len(exit.Stdout), maxStdoutBytes)
	}
	if !strings.Contains(exit.Stdout, "bytes truncated") {
		t.Errorf("exit.Stdout = %q, want truncation marker", exit.Stdout)
	}
	streamed := emit.stdoutText()
	if len(streamed) < 40000 {
		t.Errorf("streamed stdout len = %d, want >= 40000 (untruncated)", len(streamed))
	}
}

// TestHandleBashStreaming_TimeoutExitCode verifies that a timeout fires
// emits an exit chunk with ExitCode=124 and Interrupted=true.
func TestHandleBashStreaming_TimeoutExitCode(t *testing.T) {
	a := &Actor{}
	emit := newFakeEmitter()
	err := a.handleBash(newCtx(), domain.ShellBashReq{
		Command: "sleep 5",
		Timeout: 1000, // 1s — minimum allowed
	}, emit)
	if err != nil {
		t.Fatalf("handleBash returned error: %v", err)
	}
	exit, ok := emit.exitChunk()
	if !ok {
		t.Fatalf("missing exit chunk")
	}
	if exit.ExitCode != 124 {
		t.Errorf("ExitCode = %d, want 124", exit.ExitCode)
	}
	if !exit.Interrupted {
		t.Errorf("Interrupted = false, want true")
	}
	if exit.ReturnCodeInterpretation != "timeout" {
		t.Errorf("ReturnCodeInterpretation = %q, want 'timeout'", exit.ReturnCodeInterpretation)
	}
}

// TestHandleBashStreaming_EmitDoneCancels verifies that closing emit.Done()
// cancels the running process and the handler returns.
func TestHandleBashStreaming_EmitDoneCancels(t *testing.T) {
	a := &Actor{}
	emit := newFakeEmitter()
	ctx := newCtx()

	done := make(chan struct{})
	go func() {
		_ = a.handleBash(ctx, domain.ShellBashReq{
			Command: "sleep 10",
		}, emit)
		close(done)
	}()
	// Give the handler a moment to start the process.
	time.Sleep(200 * time.Millisecond)
	emit.close()

	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("handleBash did not return after emit.Done closed")
	}
}

// TestHandleExecStreaming_BuiltinEmitsExitChunk verifies the VFS builtin path
// emits one stdout chunk and one exit chunk.
func TestHandleExecStreaming_BuiltinEmitsExitChunk(t *testing.T) {
	a := &Actor{}
	emit := newFakeEmitter()
	tmp := t.TempDir()
	err := a.handleExec(newCtx(), domain.ShellExecReq{
		Command: "ls",
		Dir:     tmp,
	}, emit)
	if err != nil {
		t.Fatalf("handleExec returned error: %v", err)
	}
	exit, ok := emit.exitChunk()
	if !ok {
		t.Fatalf("missing exit chunk")
	}
	if exit.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", exit.ExitCode)
	}
}
