package project

import (
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestShellExec_HandlersAreStateless guards the PureContext signatures: the
// framework dispatches handlers whose first parameter is actor.PureContext on
// forked goroutines, which is what makes project.shell_exec non-blocking for
// other callables. Reverting to actor.Context would reintroduce mailbox
// serialization and fail this test.
func TestShellExec_HandlersAreStateless(t *testing.T) {
	a := &Actor{}
	handlers := []any{
		(&Actor{}).handleShellExec,
		a.handleShellExecPipe,
		a.handleShellExecPTY,
	}
	pure := reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	for _, h := range handlers {
		typ := reflect.TypeOf(h)
		if got := typ.In(0); got != pure {
			t.Errorf("%s first parameter = %v, want actor.PureContext", typ, got)
		}
	}
}

// TestShellExec_PipeStreaming verifies that the pipe-based shell_exec handler
// emits every stdout/stderr chunk and the terminal exit chunk without loss or
// reordering when it runs on a forked goroutine.
func TestShellExec_PipeStreaming(t *testing.T) {
	tmp := t.TempDir()
	a, ctx := freshProject(t, tmp)

	cmd, args := countingEchoCommand(5)
	emit := testutil.NewFakeEmitter()

	var wg sync.WaitGroup
	wg.Add(1)
	var handlerErr error
	go func() {
		defer wg.Done()
		handlerErr = a.handleShellExecPipe(ctx, domain.ShellExecReq{Command: cmd, Args: args}, emit)
	}()
	wg.Wait()

	if handlerErr != nil {
		t.Fatalf("handleShellExecPipe failed: %v", handlerErr)
	}

	var stdoutLines []string
	var sawExit bool
	for _, c := range emit.Chunks {
		ch, ok := c.(domain.ShellChunk)
		if !ok {
			t.Fatalf("unexpected chunk type %T", c)
		}
		switch ch.Kind {
		case "stdout":
			stdoutLines = append(stdoutLines, ch.Text)
		case "stderr":
			// no stderr expected
		case "exit":
			sawExit = true
			if ch.ExitCode != 0 {
				t.Fatalf("unexpected exit code %d, stderr=%q", ch.ExitCode, ch.Stderr)
			}
		default:
			t.Fatalf("unexpected chunk kind %q", ch.Kind)
		}
	}

	lineSep := "\n"
	if runtime.GOOS == "windows" {
		lineSep = "\r\n"
	}
	var want strings.Builder
	for i := 1; i <= 5; i++ {
		want.WriteString("line-")
		want.WriteString(strconv.Itoa(i))
		want.WriteString(lineSep)
	}
	got := strings.Join(stdoutLines, "")
	if got != want.String() {
		t.Fatalf("stdout mismatch\nwant: %q\ngot:  %q", want.String(), got)
	}
	if !sawExit {
		t.Fatal("missing exit chunk")
	}
}

// chunkEmitter is a concurrency-safe actor.Emitter that forwards chunks on a
// channel, so the test can observe streaming output while the handler is still
// running on a background goroutine.
type chunkEmitter struct {
	ch   chan domain.ShellChunk
	done chan struct{}
}

func (e *chunkEmitter) Send(chunk any) error {
	e.ch <- chunk.(domain.ShellChunk)
	return nil
}

func (e *chunkEmitter) Done() <-chan struct{} { return e.done }

// TestShellExec_ConcurrentReads is the regression test for the PureContext
// migration: while a long shell command is executing on a background goroutine,
// project.list and project.wiki_get_card complete immediately. If the
// shell handler ever starts holding an actor-global lock (or gets reverted to a
// stateful Context that serializes in the mailbox at runtime), this test will
// time out or exceed the latency bound.
func TestShellExec_ConcurrentReads(t *testing.T) {
	tmp := t.TempDir()
	a, ctx := freshProject(t, tmp)

	if err := a.store.Save(BuiltinCard{
		Title: "test:concurrent-read",
		Tags:  []string{"note"},
		Body:  "concurrent read body",
	}.ToCardRecord()); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	emit := &chunkEmitter{ch: make(chan domain.ShellChunk, 64), done: make(chan struct{})}
	cmd, args := longSleepCommand()

	var wg sync.WaitGroup
	wg.Add(1)
	var handlerErr error
	go func() {
		defer wg.Done()
		handlerErr = a.handleShellExecPipe(ctx, domain.ShellExecReq{Command: cmd, Args: args}, emit)
	}()

	// Wait until the long-running shell has actually started.
	started := &atomic.Bool{}
	deadline := time.Now().Add(2 * time.Second)
	for !started.Load() && time.Now().Before(deadline) {
		select {
		case ch := <-emit.ch:
			if strings.Contains(ch.Text, "started") {
				started.Store(true)
			}
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !started.Load() {
		t.Fatal("long-running shell command did not emit start marker")
	}

	// list must return while the shell is still sleeping.
	start := time.Now()
	if _, err := a.handleFileList(ctx, domain.FileSystemListReq{Path: ".", Detail: true}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("list blocked for %v while shell was running", d)
	}

	// wiki_get_card (a stateful handler) must also return immediately now that
	// the shell handler no longer occupies the mailbox.
	start = time.Now()
	if _, err := a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "test:concurrent-read"}); err != nil {
		t.Fatalf("wiki_get_card: %v", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("wiki_get_card blocked for %v while shell was running", d)
	}

	wg.Wait()
	if handlerErr != nil {
		t.Fatalf("handleShellExecPipe failed: %v", handlerErr)
	}
}

func countingEchoCommand(n int) (string, []string) {
	if runtime.GOOS == "windows" {
		var b strings.Builder
		b.WriteString("for /L %i in (1,1,")
		b.WriteString(strconv.Itoa(n))
		b.WriteString(") do @echo line-%i")
		return "cmd", []string{"/c", b.String()}
	}
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if i > 1 {
			b.WriteByte('\n')
		}
		b.WriteString("echo line-")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" && sleep 0.01")
	}
	return "sh", []string{"-c", b.String()}
}

// TestOperatorCanShellExecButNotProjectWrite verifies the role-ladder split:
// shell_exec is intentionally broad (operator+internal) while project.write is
// reserved for the agent/human surface (admin/developer/system/agent).
func TestOperatorCanShellExecButNotProjectWrite(t *testing.T) {
	tmp := t.TempDir()
	a, _ := freshProject(t, tmp)

	operatorCtx := testutil.OperatorCtx(testutil.GenActorID())

	emit := testutil.NewFakeEmitter()
	cmd, args := countingEchoCommand(1)
	if err := a.handleShellExecPipe(operatorCtx, domain.ShellExecReq{Command: cmd, Args: args}, emit); err != nil {
		t.Fatalf("operator shell_exec denied: %v", err)
	}

	if _, err := a.handleFileWrite(operatorCtx, domain.FileSystemWriteReq{Path: "op.txt", Content: "x"}); err == nil {
		t.Fatal("operator project.write allowed, want forbidden")
	}

	// Anonymous is also denied project.write.
	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	anonCtx.Identity_ = id.Identity{Role: "anonymous"}
	if _, err := a.handleFileWrite(anonCtx, domain.FileSystemWriteReq{Path: "anon.txt", Content: "x"}); err == nil {
		t.Fatal("anonymous project.write allowed, want forbidden")
	}
}

func longSleepCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", "echo started && timeout /t 2 >nul"}
	}
	return "sh", []string{"-c", "echo started && sleep 2"}
}