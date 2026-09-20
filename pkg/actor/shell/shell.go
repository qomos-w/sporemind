package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/util"
)

// Actor provides sandboxed shell execution through native Go builtins.
// Commands operate on a VFS interface so the backing store can be swapped
// (real disk, in-memory, remote) without changing command logic.
//
// It also owns interactive shell sessions (shell.session_*): a volatile,
// in-actor registry of live shell subprocesses. The registry is actor state
// (no package-level variables) and is never persisted — sessions die with
// the actor.
type Actor struct {
	actor.Host
	lifecycleCtx context.Context

	// sessionCtx is the OnStart actor.Context retained for background event
	// emission from pump/reap goroutines (mcpinstance.watchSession pattern).
	sessionCtx actor.Context

	sessionsMu sync.Mutex
	sessions   map[string]*session
	// sessionForcePipe disables the PTY attempt so tests can exercise the
	// pipe fallback deterministically. Always false in production.
	sessionForcePipe bool
}

// DefaultShellTimeoutMs is the default timeout for shell.exec / shell.bash /
// project.shell_exec when the caller omits Timeout or passes an unusable
// value. It is not a maximum; callers may request longer durations.
const DefaultShellTimeoutMs = 30000

// MinShellTimeoutMs guards against callers that pass seconds by mistake.
// A 30ms timeout fires before most processes produce any output, leaving
// the agent with ExitCode=1 and empty stderr/stdout.
const MinShellTimeoutMs = 1000

// ResolveShellTimeout interprets an incoming Timeout field (milliseconds).
// Values at or above MinShellTimeoutMs are honored as-is, so callers can
// request durations longer than the default. Values below the minimum
// (including zero and negatives) fall back to DefaultShellTimeoutMs.
// Shared by shell.exec / shell.bash / project.shell_exec so timeout
// semantics cannot drift between them.
func ResolveShellTimeout(reqMs int32) time.Duration {
	if reqMs <= 0 || reqMs < MinShellTimeoutMs {
		return DefaultShellTimeoutMs * time.Millisecond
	}
	return time.Duration(reqMs) * time.Millisecond
}

// ResolveShellDir validates a caller-provided working directory for
// shell.exec / shell.bash / project.shell_exec.
//
// Returns:
//   - dir == "": ( "", "", nil ) so the caller applies its own default.
//   - dir is a directory: ( dir, "", nil ).
//   - dir is a file: ( parent, warning, nil ). Common case when an LLM passes
//     the file it is working on as Dir. The command still runs in the parent
//     directory; the warning is surfaced in stderr so the caller can correct.
//   - dir does not exist or is inaccessible: ( "", "", err ). Failing here
//     yields a clear message, instead of forwarding CreateProcess's
//     "The directory name is invalid." (Windows ERROR_DIRECTORY) which
//     obscures which input was wrong.
func ResolveShellDir(dir string) (resolved, warning string, err error) {
	if dir == "" {
		return "", "", nil
	}
	info, statErr := os.Stat(dir)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return "", "", fmt.Errorf("dir %q does not exist", dir)
		}
		return "", "", fmt.Errorf("dir %q: %w", dir, statErr)
	}
	if info.IsDir() {
		return dir, "", nil
	}
	parent := filepath.Dir(dir)
	pinfo, perr := os.Stat(parent)
	if perr != nil {
		if os.IsNotExist(perr) {
			return "", "", fmt.Errorf("dir %q is a file and parent %q does not exist", dir, parent)
		}
		return "", "", fmt.Errorf("dir %q is a file and parent %q: %w", dir, parent, perr)
	}
	if !pinfo.IsDir() {
		return "", "", fmt.Errorf("dir %q is a file and parent %q is not a directory", dir, parent)
	}
	return parent, fmt.Sprintf("[sporemind diagnostic: Dir %q is a file; ran in parent %q]", dir, parent), nil
}

func (a *Actor) Type() string { return "shell" }

func (a *Actor) OnInit(ctx actor.Context) error {
	return nil
}

func (a *Actor) OnStart(ctx actor.Context) error {
	a.lifecycleCtx = ctx.Lifecycle()
	a.sessionCtx = ctx
	ctx.Logger().Info("shell: starting", "id", ctx.Self().ID().String())
	if err := ctx.Register("shell.exec", a.handleExec, actor.Public(),
		actor.Streaming[domain.ShellChunk](),
		actor.WithDescription(`Execute a command. Built-in file commands (ls, cat, mkdir, rm, cp, mv, pwd, echo, write) run in a sandboxed VFS. Allowlisted external binaries (go, python, npm, git, make, etc.) run via the system shell. grep and find run as real system binaries. Dangerous commands (wget, nc, ssh, sudo, etc.) are blocked; curl is allowed.

For file operations, dedicated project tools are preferred:
- File search: use project.glob (NOT find)
- Content search: use project.grep (NOT grep)
- Read files: use project.read (NOT cat/head/tail)
- Edit files: use project.edit (NOT sed/awk)
- Write files: use project.write (NOT echo >)
- List directories: use project.list (NOT ls)`),
	); err != nil {
		return fmt.Errorf("shell: register exec: %w", err)
	}
	if err := ctx.Register("shell.bash", a.handleBash, actor.Public(),
		actor.Streaming[domain.ShellChunk](),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription(`Execute a bash command in the project directory.

IMPORTANT: Avoid using this tool to run file operations that have dedicated tools. Instead, use the appropriate tool for better results:
- File search: use project.glob (NOT find or ls)
- Content search: use project.grep (NOT grep or rg)
- Read files: use project.read (NOT cat/head/tail)
- Edit files: use project.edit (NOT sed/awk)
- Write files: use project.write (NOT echo >)
- List directories: use project.list (NOT ls)`),
		actor.WithParams(
			actor.ParamDesc{Name: "command", Description: "The command to execute"},
			actor.ParamDesc{Name: "args", Description: "Arguments for the command"},
			actor.ParamDesc{Name: "dir", Description: "Working directory; defaults to project root"},
			actor.ParamDesc{Name: "timeout", Description: "Timeout in milliseconds; default 30000 (30s); values below 1000 are clamped to default; larger values are allowed"},
		),
	); err != nil {
		return fmt.Errorf("shell: register bash: %w", err)
	}
	if err := ctx.RegisterDomain("shell").Expose(); err != nil {
		return fmt.Errorf("shell: expose service: %w", err)
	}
	if err := ctx.Register("shell.session_open", a.handleSessionOpen, actor.Public(),
		actor.WithDescription(`Open a persistent interactive shell session for the terminal panel. Launches the user's preferred shell (workspace shell preference, "" = auto) with PTY/ConPTY first and plain pipes as fallback. Returns a sessionId plus the effective Mode ("pty" or "pipe"); output arrives on the shell.session_output event and is filtered by sessionId. Close with shell.session_close when the terminal panel closes.`),
	); err != nil {
		return fmt.Errorf("shell: register session_open: %w", err)
	}
	if err := ctx.Register("shell.session_write", a.handleSessionWrite, actor.Public(),
		actor.WithDescription("Write bytes to a shell session's stdin."),
	); err != nil {
		return fmt.Errorf("shell: register session_write: %w", err)
	}
	if err := ctx.Register("shell.session_resize", a.handleSessionResize, actor.Public(),
		actor.WithDescription("Resize a shell session's pty window. No-op in pipe mode."),
	); err != nil {
		return fmt.Errorf("shell: register session_resize: %w", err)
	}
	if err := ctx.Register("shell.session_close", a.handleSessionClose, actor.Public(),
		actor.WithDescription("Close a shell session: kill the process, emit the terminal exit chunk, and release resources."),
	); err != nil {
		return fmt.Errorf("shell: register session_close: %w", err)
	}
	if err := ctx.Register("shell.sessions_close_by_path", a.handleSessionsCloseByPath, actor.Internal(),
		actor.WithDescription("Close every interactive session whose working directory is at or under Path. Internal: used to release process handles before worktree directory deletion."),
	); err != nil {
		return fmt.Errorf("shell: register sessions_close_by_path: %w", err)
	}
	if err := ctx.Register("shell.session_fetch", a.handleSessionFetch, actor.Public(),
		actor.WithDescription("Replay a shell session's buffered output from FromIdx. Returns chunks with Idx >= FromIdx plus the next Idx to fetch from; Truncated is true when the ring buffer overwrote history so the window is non-contiguous. Lets a consumer that subscribed late or reconnected backfill the output it missed."),
	); err != nil {
		return fmt.Errorf("shell: register session_fetch: %w", err)
	}
	if err := ctx.RegisterEventKind(SessionOutputEventKind, domain.ShellSessionOutputEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("shell: register event %s: %w", SessionOutputEventKind, err)
	}
	return nil
}

func (a *Actor) OnStop(ctx actor.Context) error {
	a.closeAllSessions()
	return nil
}

func (a *Actor) handleExec(ctx actor.PureContext, req domain.ShellExecReq, emit actor.Emitter) error {
	// Build a sandboxed VFS rooted at the requested directory.
	// Empty Dir defaults to the current working directory.
	vfs := NewRealVFS(req.Dir)

	cmd := req.Command
	args := req.Args

	fn, ok := builtins[cmd]
	if !ok {
		// Not a builtin: delegate to external binary execution (safety
		// check + system shell), the same path shell.bash uses.
		return a.handleBash(ctx, domain.ShellBashReq{
			Command: cmd,
			Args:    args,
			Dir:     req.Dir,
			Timeout: req.Timeout,
		}, emit)
	}

	// Apply timeout if requested (>0).
	timeout := ResolveShellTimeout(req.Timeout)

	type result struct {
		stdout, stderr string
		code           int
	}
	resCh := make(chan result, 1)
	doneCh := make(chan struct{})
	go func() {
		stdout, stderr, code := fn(vfs, args)
		select {
		case resCh <- result{stdout, stderr, code}:
		case <-doneCh:
		}
	}()

	var res result
	select {
	case <-emit.Done():
		close(doneCh)
		return nil
	case <-ctx.Done():
		close(doneCh)
		_ = emit.Send(domain.ShellChunk{
			Kind:     "exit",
			Stderr:   fmt.Sprintf("shell: %s: actor context cancelled", cmd),
			ExitCode: 124,
		})
		return nil
	case <-time.After(timeout):
		close(doneCh)
		_ = emit.Send(domain.ShellChunk{
			Kind:     "exit",
			Stderr:   fmt.Sprintf("shell: %s: timed out after %v", cmd, timeout),
			ExitCode: 124,
		})
		return nil
	case res = <-resCh:
	}

	// Builtin path: emit one stdout chunk (if non-empty), then the exit
	// chunk carrying the LLM-facing result. Builtin output is small enough
	// to skip head/tail truncation.
	if res.stdout != "" {
		_ = emit.Send(domain.ShellChunk{Kind: "stdout", Text: res.stdout})
	}
	_ = emit.Send(domain.ShellChunk{
		Kind:     "exit",
		Stdout:   res.stdout,
		Stderr:   res.stderr,
		ExitCode: int32(res.code),
	})
	return nil
}

// procOutput accumulates untruncated stdout/stderr during execution for
// exit-chunk assembly. Goroutine-safe; reader goroutines call append* while
// the main goroutine reads snapshot after Wait().
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

// chunkOut is the reader→sender wire for streaming stdout/stderr bytes.
type chunkOut struct {
	kind string
	text string
}

// emitExit send the terminal exit chunk. Returns nil so the streaming
// protocol closes cleanly — failures during execution are surfaced via
// the exit chunk's Stderr / ExitCode, not via Go error.
func emitExit(emit actor.Emitter, chunk domain.ShellChunk) {
	_ = emit.Send(chunk)
}

// maxStdoutBytes / maxStderrBytes cap the LLM-facing result in the exit
// chunk. Streaming stdout/stderr chunks are untruncated — UI buffers
// apply their own budgets.
const (
	maxStdoutBytes = 30000
	maxStderrBytes = 10000

	// shellDrainWait bounds cleanup after cancellation. A command may leave a
	// descendant holding a stdout/stderr pipe open; never let that keep the
	// actor handler blocked forever.
	shellDrainWait = time.Second
)

func (a *Actor) handleBash(ctx actor.PureContext, req domain.ShellBashReq, emit actor.Emitter) error {
	cmd := req.Command
	args := req.Args

	// Resolve working directory.
	wd := req.Dir
	if wd == "" {
		var err error
		wd, err = os.Getwd()
		if err != nil {
			emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("shell.bash: %v", err), ExitCode: 1})
			return nil
		}
	}
	absWd, err := filepath.Abs(wd)
	if err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("shell.bash: %v", err), ExitCode: 1})
		return nil
	}
	resolvedDir, dirWarning, err := ResolveShellDir(absWd)
	if err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("shell.bash: %v", err), ExitCode: 1})
		return nil
	}

	// Command safety check.
	if err := CheckBashSafety(cmd, args); err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: err.Error(), ExitCode: 126})
		return nil
	}

	// Apply timeout.
	timeout := ResolveShellTimeout(req.Timeout)

	parentCtx := a.lifecycleCtx
	if parentCtx == nil {
		// Unit tests and direct callers may construct Actor without OnStart.
		parentCtx = context.Background()
	}
	execCtx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	// Propagate consumer cancellation (emit.Done) into execCtx so the
	// process is killed when the caller stops listening. Without this
	// watcher, a cancelled consumer leaves the process running.
	go func() {
		select {
		case <-emit.Done():
			cancel()
		case <-execCtx.Done():
		}
	}()

	sh, shErr := resolveShell()

	var c *exec.Cmd
	if len(args) > 0 {
		binPath := lookPathWithExtras(cmd, sh.extraBin)
		if binPath == "" {
			binPath = cmd
		}
		c = util.CommandContext(execCtx, binPath, args...)
	} else {
		if shErr != nil {
			emitExit(emit, domain.ShellChunk{
				Kind:                     "exit",
				Stderr:                   fmt.Sprintf("shell.bash: %v", shErr),
				ExitCode:                 127,
				ReturnCodeInterpretation: "no shell available",
			})
			return nil
		}
		c = util.CommandContext(execCtx, sh.path, sh.flag, cmd)
	}
	c.Dir = resolvedDir
	if shErr == nil {
		if env := envWithExtras(sh.extraBin); env != nil {
			c.Env = env
		}
	}

	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("shell.bash: stdout pipe: %v", err), ExitCode: 1})
		return nil
	}
	stderrPipe, err := c.StderrPipe()
	if err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("shell.bash: stderr pipe: %v", err), ExitCode: 1})
		return nil
	}

	if err := c.Start(); err != nil {
		emitExit(emit, domain.ShellChunk{Kind: "exit", Stderr: fmt.Sprintf("shell.bash: start (dir=%q): %v", c.Dir, err), ExitCode: 127})
		return nil
	}

	// Goroutine layout:
	//   - stdout reader: tees bytes into procOut + chunk channel
	//   - stderr reader: identical shape, feeds stderrCh
	//   - sender (single owner of emit): drains both channels, emits
	//     stdout/stderr chunks, then the exit chunk once Wait completes
	//
	// Emitter.Send is NOT goroutine-safe (mutates seq without a lock), so
	// the single-sender invariant is mandatory.
	var procOut procOutput
	stdoutCh := make(chan chunkOut, 16)
	stderrCh := make(chan chunkOut, 16)

	var readWg sync.WaitGroup
	readWg.Add(2)

	go func() {
		defer readWg.Done()
		defer close(stdoutCh)
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				s := SanitizeUTF8(string(buf[:n]))
				procOut.appendStdout(s)
				select {
				case stdoutCh <- chunkOut{"stdout", s}:
				case <-execCtx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		defer readWg.Done()
		defer close(stderrCh)
		buf := make([]byte, 4096)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				s := SanitizeUTF8(string(buf[:n]))
				procOut.appendStderr(s)
				select {
				case stderrCh <- chunkOut{"stderr", s}:
				case <-execCtx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	type exitInfo struct {
		code                       int32
		stdout, stderr             string
		truncated, interrupted     bool
		interpretation, dirWarning string
	}
	exitCh := make(chan exitInfo, 1)
	senderDone := make(chan struct{})
	// chunksDone stops the sender waiting on the stdout/stderr channels once
	// the process has exited but a descendant still holds its pipe open, so the
	// terminal exit chunk is still emitted before the handler returns. Without
	// it the main goroutine bailed out after shellDrainWait and the exit chunk
	// was dropped (the caller saw "stream ended without exit chunk").
	chunksDone := make(chan struct{})
	var chunksDoneOnce sync.Once
	stopChunks := func() { chunksDoneOnce.Do(func() { close(chunksDone) }) }

	// Sender drains stdout/stderr chunks, then emits the exit chunk
	// delivered by the main goroutine after Wait completes.
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
				Kind:                     "exit",
				ExitCode:                 info.code,
				Stdout:                   info.stdout,
				Stderr:                   info.stderr,
				Truncated:                info.truncated,
				Interrupted:              info.interrupted,
				ReturnCodeInterpretation: info.interpretation,
				DirWarning:               info.dirWarning,
			})
		case <-emit.Done():
		}
	}()

	waitErr := c.Wait()
	// Close our pipe ends explicitly so a reader blocked by an inherited
	// descendant handle is released promptly after the direct process exits.
	_ = stdoutPipe.Close()
	_ = stderrPipe.Close()
	readDone := make(chan struct{})
	go func() {
		readWg.Wait()
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(shellDrainWait):
		// A descendant may still hold a pipe open. The command result is
		// already known; do not block the actor handler indefinitely. Stop
		// forwarding its late output so the exit chunk can still be emitted.
		stopChunks()
	}

	// Compute exit chunk fields.
	stdoutStr, stderrStr := procOut.snapshot()
	exitCode := int32(0)
	interrupted := false
	var interpretation string
	if waitErr != nil {
		// ctx state must be checked first: exec.CommandContext kills the
		// process on ctx deadline, so Wait() returns *exec.ExitError (not
		// DeadlineExceeded). Without this ordering the timeout branch is
		// dead code and the agent sees ExitCode=1 with empty stderr.
		switch {
		case errors.Is(execCtx.Err(), context.DeadlineExceeded):
			exitCode = 124
			stderrStr = fmt.Sprintf("shell.bash: timed out after %v", timeout)
			interrupted = true
			interpretation = "timeout"
		case errors.Is(execCtx.Err(), context.Canceled):
			exitCode = 130
			stderrStr = "shell.bash: cancelled"
			interrupted = true
			interpretation = "cancelled"
		default:
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				exitCode = int32(exitErr.ExitCode())
				interpretation = interpretExitCode(exitCode)
				if stderrStr == "" {
					stderrStr = string(exitErr.Stderr)
				}
			} else {
				exitCode = 127
				stderrStr = waitErr.Error()
				interpretation = "command not found"
			}
		}
	}

	// Diagnostic hint when the command failed silently — common with PATH
	// misses or processes killed before flush. Prefix makes it clear this
	// is platform diagnosis, not command output.
	if exitCode != 0 && stdoutStr == "" && stderrStr == "" {
		stderrStr = fmt.Sprintf("[sporemind diagnostic: command exited with code %d but produced no output; common causes: command not found, missing PATH entry, or killed before output]", exitCode)
	}

	stdoutStr = SanitizeUTF8(stdoutStr)
	stdoutStr, stdoutTruncated := truncateHeadTail(stdoutStr, maxStdoutBytes)
	if dirWarning != "" {
		stderrStr = dirWarning + "\n" + stderrStr
	}
	stderrStr = SanitizeUTF8(stderrStr)
	stderrStr, stderrTruncated := truncateHeadTail(stderrStr, maxStderrBytes)
	truncated := stdoutTruncated || stderrTruncated

	exitCh <- exitInfo{
		code:           exitCode,
		stdout:         stdoutStr,
		stderr:         stderrStr,
		truncated:      truncated,
		interrupted:    interrupted,
		interpretation: interpretation,
		dirWarning:     dirWarning,
	}
	// Wait for the sender to emit the exit chunk before returning (see the
	// project shell_exec finalize path for the rationale: bailing out after
	// shellDrainWait could drop the exit chunk, surfacing as "stream ended
	// without exit chunk").
	select {
	case <-senderDone:
	case <-emit.Done():
	}
	return nil
}

// SanitizeUTF8 replaces invalid UTF-8 byte sequences with U+FFFD. Shell
// output is arbitrary bytes — GBK-encoded text from a Windows child process,
// a multibyte character split by a byte-boundary truncation, raw binary —
// and the JSON reply codec rejects invalid UTF-8 (jsontext: invalid UTF-8).
// Left unhandled, the encode failure drops every affected chunk; for the
// terminal exit chunk that discards the entire command result and surfaces to
// the caller as "stream ended without exit chunk" with no output.
func SanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

// truncateHeadTail keeps the head and tail of s when it exceeds maxBytes.
// Returns the (possibly truncated) string and whether truncation occurred.
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

// utf8Bound returns n clamped to a valid UTF-8 boundary (<= n).
func utf8Bound(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return 0
}

// utf8BoundFrom returns the first position >= from that is a valid UTF-8 start byte.
func utf8BoundFrom(s string, from int) int {
	for from < len(s) {
		if utf8.RuneStart(s[from]) {
			return from
		}
		from++
	}
	return len(s)
}

// interpretExitCode returns a human-readable meaning for common exit codes.
func interpretExitCode(code int32) string {
	switch code {
	case 0:
		return "success"
	case 1:
		return "general error"
	case 2:
		return "misuse of shell builtin"
	case 126:
		return "command not executable"
	case 127:
		return "command not found"
	case 128:
		return "invalid exit argument"
	case 130:
		return "interrupted (Ctrl+C)"
	case 137:
		return "killed (SIGKILL)"
	case 139:
		return "segmentation fault"
	default:
		if code > 128 {
			return fmt.Sprintf("fatal signal %d", code-128)
		}
		return ""
	}
}

// CheckBashSafety checks whether a bash command is allowed to run.
func CheckBashSafety(cmd string, args []string) error {
	base := filepath.Base(cmd)
	if DangerousCmds[base] {
		return fmt.Errorf("shell: command %q is not allowed", base)
	}
	// Block rm -rf / or similar.
	if base == "rm" {
		for _, a := range args {
			if a == "-rf" || a == "-fr" || a == "--recursive" {
				for _, b := range args {
					if b == "/" || b == "/*" || strings.HasPrefix(b, "/") && len(b) <= 2 {
						return fmt.Errorf("shell: dangerous rm invocation blocked")
					}
				}
			}
		}
	}
	return nil
}

func cmdLs(vfs VFS, args []string) (string, string, int) {
	target := "."
	dashDashSeen := false
	for _, a := range args {
		if dashDashSeen {
			target = a
			break
		}
		if a == "--" {
			dashDashSeen = true
			continue
		}
		if !strings.HasPrefix(a, "-") {
			target = a
		}
	}
	entries, err := vfs.ReadDir(target)
	if err != nil {
		return "", err.Error(), 1
	}
	var out strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		out.WriteString(name)
		out.WriteByte('\n')
	}
	return out.String(), "", 0
}

func cmdCat(vfs VFS, args []string) (string, string, int) {
	if len(args) == 0 {
		return "", "cat: missing file argument", 1
	}
	var out strings.Builder
	for _, p := range args {
		data, err := vfs.ReadFile(p)
		if err != nil {
			return "", fmt.Sprintf("cat: %s: %v", p, err), 1
		}
		out.Write(data)
	}
	return out.String(), "", 0
}

func cmdMkdir(vfs VFS, args []string) (string, string, int) {
	if len(args) == 0 {
		return "", "mkdir: missing directory argument", 1
	}
	for _, p := range args {
		if err := vfs.MkdirAll(p, 0755); err != nil {
			return "", fmt.Sprintf("mkdir: %s: %v", p, err), 1
		}
	}
	return "", "", 0
}

func cmdRm(vfs VFS, args []string) (string, string, int) {
	recursive := false
	paths := make([]string, 0, len(args))
	dashDashSeen := false
	for _, a := range args {
		if dashDashSeen {
			paths = append(paths, a)
			continue
		}
		if a == "--" {
			dashDashSeen = true
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			for _, c := range a[1:] {
				if c == 'r' {
					recursive = true
				}
				// ignore 'f' and any unknown flags
			}
			continue
		}
		paths = append(paths, a)
	}
	if len(paths) == 0 {
		return "", "rm: missing operand", 1
	}
	for _, p := range paths {
		var err error
		if recursive {
			err = vfs.RemoveAll(p)
		} else {
			err = vfs.Remove(p)
		}
		if err != nil {
			return "", fmt.Sprintf("rm: %s: %v", p, err), 1
		}
	}
	return "", "", 0
}

func cmdCp(vfs VFS, args []string) (string, string, int) {
	if len(args) < 2 {
		return "", "cp: missing source or destination", 1
	}
	src := args[0]
	dst := args[1]

	info, err := vfs.Stat(src)
	if err != nil {
		return "", fmt.Sprintf("cp: %s: %v", src, err), 1
	}
	if info.IsDir() {
		if err := vfs.MkdirAll(dst, uint32(info.Mode().Perm())); err != nil {
			return "", fmt.Sprintf("cp: %s: %v", dst, err), 1
		}
		if err := copyDir(vfs, src, dst); err != nil {
			return "", fmt.Sprintf("cp: %v", err), 1
		}
		return "", "", 0
	}

	data, err := vfs.ReadFile(src)
	if err != nil {
		return "", fmt.Sprintf("cp: %s: %v", src, err), 1
	}
	perm := uint32(info.Mode().Perm())
	if err := vfs.WriteFile(dst, data, perm); err != nil {
		return "", fmt.Sprintf("cp: %s: %v", dst, err), 1
	}
	return "", "", 0
}

func copyDir(vfs VFS, src, dst string) error {
	entries, err := vfs.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			info, _ := vfs.Stat(srcPath)
			perm := uint32(0755)
			if info != nil {
				perm = uint32(info.Mode().Perm())
			}
			if err := vfs.MkdirAll(dstPath, perm); err != nil {
				return err
			}
			if err := copyDir(vfs, srcPath, dstPath); err != nil {
				return err
			}
		} else {
			data, err := vfs.ReadFile(srcPath)
			if err != nil {
				return err
			}
			info, _ := vfs.Stat(srcPath)
			perm := uint32(0644)
			if info != nil {
				perm = uint32(info.Mode().Perm())
			}
			if err := vfs.WriteFile(dstPath, data, perm); err != nil {
				return err
			}
		}
	}
	return nil
}

func cmdMv(vfs VFS, args []string) (string, string, int) {
	if len(args) < 2 {
		return "", "mv: missing source or destination", 1
	}
	src := args[0]
	dst := args[1]

	info, err := vfs.Stat(src)
	if err != nil {
		return "", fmt.Sprintf("mv: %s: %v", src, err), 1
	}

	// POSIX: if dst exists and is a directory, move src INTO dst.
	dstInfo, _ := vfs.Stat(dst)
	if dstInfo != nil && dstInfo.IsDir() {
		dst = filepath.Join(dst, filepath.Base(src))
	}

	if info.IsDir() {
		if err := vfs.MkdirAll(dst, uint32(info.Mode().Perm())); err != nil {
			return "", fmt.Sprintf("mv: %s: %v", dst, err), 1
		}
		if err := copyDir(vfs, src, dst); err != nil {
			return "", fmt.Sprintf("mv: %v", err), 1
		}
		if err := vfs.RemoveAll(src); err != nil {
			return "", fmt.Sprintf("mv: remove %s: %v", src, err), 1
		}
		return "", "", 0
	}

	data, err := vfs.ReadFile(src)
	if err != nil {
		return "", fmt.Sprintf("mv: %s: %v", src, err), 1
	}
	perm := uint32(info.Mode().Perm())
	if err := vfs.WriteFile(dst, data, perm); err != nil {
		return "", fmt.Sprintf("mv: %s: %v", dst, err), 1
	}
	if err := vfs.Remove(src); err != nil {
		return "", fmt.Sprintf("mv: remove %s: %v", src, err), 1
	}
	return "", "", 0
}

func cmdPwd(vfs VFS, args []string) (string, string, int) {
	return util.NormalizePath(vfs.Root()), "", 0
}

func cmdEcho(vfs VFS, args []string) (string, string, int) {
	return strings.Join(args, " ") + "\n", "", 0
}

func cmdWrite(vfs VFS, args []string) (string, string, int) {
	if len(args) < 2 {
		return "", "write: missing file or content", 1
	}
	path := args[0]
	content := strings.Join(args[1:], " ")
	if err := vfs.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Sprintf("write: %s: %v", path, err), 1
	}
	return "", "", 0
}
