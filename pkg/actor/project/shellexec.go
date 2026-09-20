package project

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	gopt "github.com/aymanbagabas/go-pty"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/shell"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/policy"
	"github.com/qomos-w/sporemind/pkg/util"
)

const (
	shellMaxStdoutBytes = 30000
	shellMaxStderrBytes = 10000
	shellExecUsePTY     = false

	// shellDrainWait bounds cleanup after cancellation. Descendants can keep
	// inherited stdout/stderr handles open after the direct process exits.
	shellDrainWait = time.Second
)

func (a *Actor) handleShellExec(ctx actor.PureContext, req domain.ShellExecReq, emit actor.Emitter) error {
	if shellExecUsePTY {
		return a.handleShellExecPTY(ctx, req, emit)
	}
	return a.handleShellExecPipe(ctx, req, emit)
}

func (a *Actor) handleShellExecPipe(ctx actor.PureContext, req domain.ShellExecReq, emit actor.Emitter) error {
	if err := a.denyUnboundSpawnChild("project.shell_exec", callerID(ctx)); err != nil {
		return err
	}
	if err := policy.RequireOperatorOrInternal(ctx.Identity().Role); err != nil {
		return err
	}
	rootPath, err := a.rootPathFor(callerID(ctx))
	if err != nil {
		return err
	}

	wd := rootPath
	if req.Dir != "" {
		resolved, err := a.resolveAbsolutePathConfirmFor(callerID(ctx), req.Dir, req.Confirm)
		if err != nil {
			return fmt.Errorf("project.shell_exec: resolve dir: %w", err)
		}
		wd = resolved
	}
	resolvedDir, dirWarning, err := shell.ResolveShellDir(wd)
	if err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("project.shell_exec: %v", err), ExitCode: 1})
		return nil
	}
	wd = resolvedDir

	cmd := req.Command
	args := req.Args

	if err := shell.CheckBashSafety(cmd, args); err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: err.Error(), ExitCode: 126})
		return nil
	}

	timeout := shell.ResolveShellTimeout(req.Timeout)
	execCtx, cancel := context.WithTimeout(ctx.Lifecycle(), timeout)
	defer cancel()

	go propagateCancel(emit, execCtx, cancel)

	sh := defaultShell()
	c, err := buildCmd(execCtx, sh, cmd, args, wd)
	if err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("project.shell_exec: %v", err), ExitCode: 127})
		return nil
	}

	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("project.shell_exec: stdout pipe: %v", err), ExitCode: 1})
		return nil
	}
	stderrPipe, err := c.StderrPipe()
	if err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("project.shell_exec: stderr pipe: %v", err), ExitCode: 1})
		return nil
	}

	if err := c.Start(); err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("project.shell_exec: start (dir=%q): %v", c.Dir, err), ExitCode: 127})
		return nil
	}

	return runDualStream(c, execCtx, emit, stdoutPipe, stderrPipe, timeout, dirWarning)
}

// handleShellExecPTY also runs as a stateless (PureContext) handler. PTY
// cleanup is tied to the command process, not to actor mailbox state, and
// ctx.Lifecycle() (available on PureContext) is sufficient to cancel the
// per-command context when the actor stops. No actor.Context-only lifecycle
// operations are required here.
func (a *Actor) handleShellExecPTY(ctx actor.PureContext, req domain.ShellExecReq, emit actor.Emitter) error {
	if err := a.denyUnboundSpawnChild("project.shell_exec", callerID(ctx)); err != nil {
		return err
	}
	if err := policy.RequireOperatorOrInternal(ctx.Identity().Role); err != nil {
		return err
	}
	rootPath, err := a.rootPathFor(callerID(ctx))
	if err != nil {
		return err
	}

	wd := rootPath
	if req.Dir != "" {
		resolved, err := a.resolveAbsolutePathConfirmFor(callerID(ctx), req.Dir, req.Confirm)
		if err != nil {
			return fmt.Errorf("project.shell_exec: resolve dir: %w", err)
		}
		wd = resolved
	}
	resolvedDir, dirWarning, err := shell.ResolveShellDir(wd)
	if err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("project.shell_exec: %v", err), ExitCode: 1})
		return nil
	}
	wd = resolvedDir

	cmd := req.Command
	args := req.Args

	if err := shell.CheckBashSafety(cmd, args); err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: err.Error(), ExitCode: 126})
		return nil
	}

	timeout := shell.ResolveShellTimeout(req.Timeout)
	execCtx, cancel := context.WithTimeout(ctx.Lifecycle(), timeout)
	defer cancel()

	go propagateCancel(emit, execCtx, cancel)

	sh := defaultShell()
	c, err := buildCmd(execCtx, sh, cmd, args, wd)
	if err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("project.shell_exec: %v", err), ExitCode: 127})
		return nil
	}
	// util.HideWindow is intentionally skipped for PTY mode so that Windows
	// ConPTY can create and manage the pseudo-console without conflicting
	// with CREATE_NO_WINDOW — buildCmd's SysProcAttr is therefore not carried
	// over to the go-pty command (Path/Args/Env/Dir are).
	p, err := gopt.New()
	if err != nil {
		if errors.Is(err, gopt.ErrUnsupported) {
			return a.handleShellExecPipe(ctx, req, emit)
		}
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("project.shell_exec: pty new: %v", err), ExitCode: 1})
		return nil
	}
	pc := p.CommandContext(execCtx, c.Path, c.Args[1:]...)
	pc.Dir = c.Dir
	pc.Env = c.Env
	if err := pc.Start(); err != nil {
		_ = p.Close()
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("project.shell_exec: pty start (dir=%q): %v", pc.Dir, err), ExitCode: 1})
		return nil
	}
	defer p.Close() // exactly-once close; aliasing the master into stdin double-closes ConPTY

	// PTY merges stdout/stderr into a single stream — emit everything as
	// Kind="stdout". Demuxing would require ANSI sequence parsing.
	return runSingleStream(pc, execCtx, emit, p, timeout, dirWarning)
}

// procOutput accumulates untruncated stdout/stderr for exit-chunk assembly.
// Goroutine-safe; reader goroutines append while the main goroutine reads
// snapshot after Wait.
type procOutput struct {
	mu     sync.Mutex
	stdout strings.Builder
	stderr strings.Builder
}

func (p *procOutput) appendStdout(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stdout.WriteString(s)
}

func (p *procOutput) appendStderr(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stderr.WriteString(s)
}

func (p *procOutput) snapshot() (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stdout.String(), p.stderr.String()
}

// chunkStream is the reader→sender wire for streaming stdout/stderr bytes.
type chunkStream struct {
	kind string
	text string
}

func emitExit(emit actor.Emitter, chunk domain.ShellChunk) {
	_ = emit.Send(chunk)
}

// propagateCancel cancels execCtx when emit.Done closes, so a consumer
// that stops listening also kills the running process. Returns once
// either side fires.
func propagateCancel(emit actor.Emitter, execCtx context.Context, cancel context.CancelFunc) {
	select {
	case <-emit.Done():
		cancel()
	case <-execCtx.Done():
	}
}

func buildCmd(execCtx context.Context, sh shellSpec, cmd string, args []string, wd string) (*exec.Cmd, error) {
	var c *exec.Cmd
	if len(args) > 0 {
		binPath := lookPathWithExtras(cmd, sh.extraBin)
		if binPath == "" {
			binPath = cmd
		}
		c = util.CommandContext(execCtx, binPath, args...)
	} else {
		c = util.CommandContext(execCtx, sh.path, sh.flag, cmd)
	}
	c.Dir = wd
	if env := envWithExtras(sh.extraBin); env != nil {
		c.Env = env
	}
	return c, nil
}

// runDualStream is the pipe-based streaming loop with separate stdout/stderr.
// Single sender goroutine owns emit.Send.
func runDualStream(c *exec.Cmd, execCtx context.Context, emit actor.Emitter, stdoutPipe, stderrPipe io.Reader, timeout time.Duration, dirWarning string) error {
	var procOut procOutput
	stdoutCh := make(chan chunkStream, 16)
	stderrCh := make(chan chunkStream, 16)

	var readWg sync.WaitGroup
	readWg.Add(2)

	go readPipe(stdoutPipe, "stdout", &procOut, stdoutCh, execCtx, &readWg)
	go readPipe(stderrPipe, "stderr", &procOut, stderrCh, execCtx, &readWg)

	return finalizeDual(c, execCtx, emit, &procOut, stdoutCh, stderrCh, &readWg, timeout, dirWarning)
}

// readPipe pumps bytes from r into both procOut (for exit-chunk assembly)
// and ch (for streaming chunks). Closes ch on EOF or execCtx cancellation.
func readPipe(r io.Reader, kind string, procOut *procOutput, ch chan<- chunkStream, execCtx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	defer close(ch)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s := shell.SanitizeUTF8(string(buf[:n]))
			if kind == "stdout" {
				procOut.appendStdout(s)
			} else {
				procOut.appendStderr(s)
			}
			select {
			case ch <- chunkStream{kind, s}:
			case <-execCtx.Done():
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// finalizeDual drains stdout/stderr channels via a single sender goroutine
// and emits the terminal exit chunk once the process exits and readers
// finish.
func finalizeDual(c *exec.Cmd, execCtx context.Context, emit actor.Emitter, procOut *procOutput, stdoutCh, stderrCh chan chunkStream, readWg *sync.WaitGroup, timeout time.Duration, dirWarning string) error {
	type exitInfo struct {
		code                   int32
		stdout, stderr         string
		truncated, interrupted bool
		dirWarning             string
	}
	exitCh := make(chan exitInfo, 1)
	senderDone := make(chan struct{})
	// chunksDone stops the sender waiting on the stdout/stderr channels once
	// the process has exited but a descendant still holds its pipe open (or a
	// backlogged consumer is stalling emit.Send). Closing it lets the sender
	// abandon any remaining output and emit the terminal exit chunk, so the
	// exit chunk is never dropped by the main goroutine returning first.
	chunksDone := make(chan struct{})
	var chunksDoneOnce sync.Once
	stopChunks := func() { chunksDoneOnce.Do(func() { close(chunksDone) }) }

	go func() {
		defer close(senderDone)
	drain:
		for stdoutCh != nil || stderrCh != nil {
			select {
			case <-emit.Done():
				return
			case <-chunksDone:
				break drain
			case c, ok := <-stdoutCh:
				if !ok {
					stdoutCh = nil
					continue
				}
				_ = emit.Send(domain.ShellChunk{Kind: c.kind, Text: c.text})
			case c, ok := <-stderrCh:
				if !ok {
					stderrCh = nil
					continue
				}
				_ = emit.Send(domain.ShellChunk{Kind: c.kind, Text: c.text})
			}
		}
		select {
		case info := <-exitCh:
			_ = emit.Send(domain.ShellChunk{
				Kind:        "exit",
				ExitCode:    info.code,
				Stdout:      info.stdout,
				Stderr:      info.stderr,
				Truncated:   info.truncated,
				Interrupted: info.interrupted,
				DirWarning:  info.dirWarning,
			})
		case <-emit.Done():
		}
	}()

	waitErr := c.Wait()
	readDone := make(chan struct{})
	go func() {
		readWg.Wait()
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(shellDrainWait):
		// A descendant may still hold a pipe open. Never block the actor
		// handler indefinitely after the process itself has exited; stop
		// forwarding its late output so the exit chunk can still be emitted.
		stopChunks()
	}

	stdoutStr, stderrStr := procOut.snapshot()
	exitCode := int32(0)
	interrupted := false
	if waitErr != nil {
		switch {
		case errors.Is(execCtx.Err(), context.DeadlineExceeded):
			exitCode = 124
			stderrStr = fmt.Sprintf("project.shell_exec: timed out after %v", timeout)
			interrupted = true
		case errors.Is(execCtx.Err(), context.Canceled):
			exitCode = 130
			stderrStr = "project.shell_exec: cancelled"
			interrupted = true
		default:
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				exitCode = int32(exitErr.ExitCode())
				if stderrStr == "" {
					stderrStr = string(exitErr.Stderr)
				}
			} else {
				exitCode = 127
				stderrStr = waitErr.Error()
			}
		}
	}

	if exitCode != 0 && stdoutStr == "" && stderrStr == "" {
		stderrStr = fmt.Sprintf("[sporemind diagnostic: command exited with code %d but produced no output; common causes: command not found, missing PATH entry, or killed before output]", exitCode)
	}

	stdoutStr = shell.SanitizeUTF8(stdoutStr)
	stdoutStr, stdoutTruncated := truncateHeadTail(stdoutStr, shellMaxStdoutBytes)
	if dirWarning != "" {
		stderrStr = dirWarning + "\n" + stderrStr
	}
	stderrStr = shell.SanitizeUTF8(stderrStr)
	stderrStr, stderrTruncated := truncateHeadTail(stderrStr, shellMaxStderrBytes)
	truncated := stdoutTruncated || stderrTruncated

	exitCh <- exitInfo{
		code:        exitCode,
		stdout:      stdoutStr,
		stderr:      stderrStr,
		truncated:   truncated,
		interrupted: interrupted,
		dirWarning:  dirWarning,
	}
	// Wait for the sender to emit the exit chunk before returning. The handler
	// runs on a forked goroutine (PureContext), so waiting here does not block
	// the actor loop; emit.Send is either bounded (the reply enqueue has its own
	// ceiling) or unblocked by emit.Done. This guarantees the exit chunk
	// precedes the framework's KindEnd, so a backlogged consumer never sees
	// "stream ended without exit chunk".
	select {
	case <-senderDone:
	case <-emit.Done():
	}
	return nil
}

// runSingleStream is the PTY streaming loop with merged stdout/stderr
// (everything emitted as Kind="stdout"). It drives a go-pty command: real
// exit codes come from ProcessState — go-pty's Wait returns nil for non-zero
// exits on Windows.
func runSingleStream(pc *gopt.Cmd, execCtx context.Context, emit actor.Emitter, r io.Reader, timeout time.Duration, dirWarning string) error {
	var procOut procOutput
	stdoutCh := make(chan chunkStream, 16)

	var readWg sync.WaitGroup
	readWg.Add(1)
	go readPipe(r, "stdout", &procOut, stdoutCh, execCtx, &readWg)

	type exitInfo struct {
		code                   int32
		stdout, stderr         string
		truncated, interrupted bool
		dirWarning             string
	}
	exitCh := make(chan exitInfo, 1)
	senderDone := make(chan struct{})
	// chunksDone stops the sender waiting on stdout once the process has
	// exited but a descendant still holds the PTY pipe open, so the exit chunk
	// is still emitted before the handler returns.
	chunksDone := make(chan struct{})
	var chunksDoneOnce sync.Once
	stopChunks := func() { chunksDoneOnce.Do(func() { close(chunksDone) }) }

	go func() {
		defer close(senderDone)
	drain:
		for stdoutCh != nil {
			select {
			case <-emit.Done():
				return
			case <-chunksDone:
				break drain
			case c, ok := <-stdoutCh:
				if !ok {
					stdoutCh = nil
					continue
				}
				_ = emit.Send(domain.ShellChunk{Kind: c.kind, Text: c.text})
			}
		}
		select {
		case info := <-exitCh:
			_ = emit.Send(domain.ShellChunk{
				Kind:        "exit",
				ExitCode:    info.code,
				Stdout:      info.stdout,
				Stderr:      info.stderr,
				Truncated:   info.truncated,
				Interrupted: info.interrupted,
				DirWarning:  info.dirWarning,
			})
		case <-emit.Done():
		}
	}()

	waitErr := pc.Wait()
	readDone := make(chan struct{})
	go func() {
		readWg.Wait()
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(shellDrainWait):
		// A descendant may still hold a pipe open. Never block the actor
		// handler indefinitely after the process itself has exited; stop
		// forwarding its late output so the exit chunk can still be emitted.
		stopChunks()
	}

	stdoutStr, _ := procOut.snapshot()
	exitCode := int32(0)
	interrupted := false
	if ps := pc.ProcessState; ps != nil {
		exitCode = int32(ps.ExitCode())
	}
	switch {
	case errors.Is(execCtx.Err(), context.DeadlineExceeded):
		exitCode = 124
		stdoutStr = fmt.Sprintf("project.shell_exec: timed out after %v", timeout)
		interrupted = true
	case errors.Is(execCtx.Err(), context.Canceled):
		exitCode = 130
		stdoutStr = "project.shell_exec: cancelled"
		interrupted = true
	case waitErr != nil && pc.ProcessState == nil:
		// Wait failed without populating ProcessState — an infrastructure
		// error, not a command exit. Real exits are already covered by the
		// ProcessState extraction above (go-pty's Wait returns nil for
		// non-zero exits on Windows).
		exitCode = 127
		stdoutStr = waitErr.Error()
	}

	if exitCode != 0 && stdoutStr == "" {
		stdoutStr = fmt.Sprintf("[sporemind diagnostic: command exited with code %d but produced no output; common causes: command not found, missing PATH entry, or killed before output]", exitCode)
	}

	stdoutStr = shell.SanitizeUTF8(stdoutStr)
	stdoutStr, stdoutTruncated := truncateHeadTail(stdoutStr, shellMaxStdoutBytes)
	var stderrStr string
	if dirWarning != "" {
		stderrStr = dirWarning
	}

	exitCh <- exitInfo{
		code:        exitCode,
		stdout:      stdoutStr,
		stderr:      stderrStr,
		truncated:   stdoutTruncated,
		interrupted: interrupted,
		dirWarning:  dirWarning,
	}
	// Wait for the sender to emit the exit chunk before returning (see
	// finalizeDual for the rationale).
	select {
	case <-senderDone:
	case <-emit.Done():
	}
	return nil
}

// shellSpec describes the system shell to use for bare commands.
type shellSpec struct {
	path     string
	flag     string
	extraBin []string
}

// defaultShell returns the shell used for project.shell_exec. It prefers the
// same shell advertised to agents via util.DetectEnvironment so the prompt
// and execution agree. On Windows, if no POSIX shell is found it falls back to
// PowerShell instead of the legacy cmd.exe.
func defaultShell() shellSpec {
	info := util.DetectShellInfo()
	if info.IsAvailable() {
		return shellSpec{path: info.Executable, flag: info.ExecFlag(), extraBin: info.ExtraBin}
	}
	if runtime.GOOS == "windows" {
		return shellSpec{path: "powershell.exe", flag: "-Command"}
	}
	return shellSpec{path: "/bin/sh", flag: "-c"}
}

// lookPathWithExtras resolves cmd using PATH plus the shell's auxiliary bin
// directories (e.g. Git Bash usr/bin). On Windows it appends .exe.
func lookPathWithExtras(cmd string, extras []string) string {
	if filepath.Base(cmd) != cmd {
		if _, err := exec.LookPath(cmd); err == nil {
			return cmd
		}
		return ""
	}
	for _, d := range extras {
		for _, ext := range pathExts() {
			candidate := filepath.Join(d, cmd+ext)
			if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
				return candidate
			}
		}
	}
	if p, err := exec.LookPath(cmd); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath(cmd + ".exe"); err == nil {
			return p
		}
	}
	return ""
}

func pathExts() []string {
	if runtime.GOOS != "windows" {
		return []string{""}
	}
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		return []string{".exe", ".cmd", ".bat"}
	}
	var out []string
	for _, ext := range strings.Split(pathext, ";") {
		out = append(out, strings.ToLower(strings.TrimSpace(ext)))
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func envWithExtras(extras []string) []string {
	if len(extras) == 0 {
		return nil
	}
	env := os.Environ()
	pathKey := "PATH"
	pathIdx := -1
	for i, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key := kv[:eq]
		if runtime.GOOS == "windows" {
			if strings.EqualFold(key, "PATH") {
				pathKey = key
				pathIdx = i
				break
			}
		} else if key == "PATH" {
			pathIdx = i
			break
		}
	}
	sep := string(os.PathListSeparator)
	prepend := strings.Join(extras, sep)
	if pathIdx >= 0 {
		existing := env[pathIdx][len(pathKey)+1:]
		env[pathIdx] = pathKey + "=" + prepend + sep + existing
	} else {
		env = append(env, pathKey+"="+prepend)
	}
	return env
}

func truncateHeadTail(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	half := maxBytes / 2
	headEnd := utf8Bound(s[:half])
	tailStart := utf8BoundFrom(s, len(s)-half)
	dropped := len(s) - headEnd - (len(s) - tailStart)
	return s[:headEnd] + fmt.Sprintf("\n... [%d bytes truncated] ...\n", dropped) + s[tailStart:], true
}

func utf8Bound(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return 0
}

func utf8BoundFrom(s string, from int) int {
	for from < len(s) {
		if utf8.RuneStart(s[from]) {
			return from
		}
		from++
	}
	return len(s)
}
