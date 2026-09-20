package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	gopt "github.com/aymanbagabas/go-pty"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/util"
)

// SessionOutputEventKind is the app-scoped event kind for shell session output.
// Registered in OnStart; payload type is domain.ShellSessionOutputEvent.
const SessionOutputEventKind = "shell.session_output"

// sessionWaitTimeout bounds how long session_close waits for the process
// teardown (wait goroutine + cleanup) before returning. Descendants can keep
// inherited pipes open after the direct process exits; never block the actor
// handler indefinitely.
const sessionWaitTimeout = 2 * time.Second

// sessionBufferCap bounds the per-session ring buffer of emitted output
// chunks. When the ring is full, the oldest chunk is overwritten and the
// session's truncated flag is set so session_fetch can report that the
// consumer's window is not contiguous. The cap is a hard upper bound — it is
// not a budget applied per call.
const sessionBufferCap = 4096

// session holds one interactive shell subprocess and its wire endpoints.
//
// Volatile: never persisted; lives only while the process runs. The registry
// (map[string]*session on the shell Actor) is the only owner — no package-level
// state. A session is torn down (process killed + streams closed) when the
// consumer calls shell.session_close or the actor stops.
type session struct {
	id   string
	mode string // "pty" | "pipe"
	// dir is the resolved working directory the session was started in ("" =
	// the actor's process cwd). Used by sessions_close_by_path to find
	// sessions living inside a directory about to be deleted.
	dir string

	cmd *exec.Cmd
	// pty is the pseudo-terminal (go-pty, ConPTY on Windows); nil in pipe
	// mode. It is both the stdin sink (writes drive the controlling
	// terminal) and the merged output stream read by the pump goroutine.
	pty gopt.Pty
	// ptyCmd is the process attached to pty; nil in pipe mode.
	ptyCmd *gopt.Cmd

	// stdin is the process stdin pipe; nil in pty mode (the pty is the
	// write target, see session.write).
	stdin io.WriteCloser
	// stdout/stderr are the read ends used by the pump goroutines. In pty
	// mode both are nil (the merged stream is read from pty).
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu     sync.Mutex
	closed bool
	// writeMu serializes stdin writes so interleaved session_write calls
	// cannot tear terminal input apart mid-sequence.
	writeMu sync.Mutex
	// wait is closed by the reap goroutine after the process has exited and
	// the session has been removed from the registry. session_close waits on
	// it so the caller knows the teardown is complete.
	wait chan struct{}

	// outputBuf is a ring buffer of emitted ShellSessionOutputEvent chunks and
	// nextIdx is the monotonic ordinal that will be assigned to the next chunk.
	// Both are guarded by mu. emitSessionEvent (called from the pump/reap
	// goroutines) appends under mu; handleSessionFetch reads under the same
	// lock, so replay is exactly equivalent to the subscribed event stream
	// (each emitted chunk appears at its Idx, in order). The ring overwrites
	// the oldest chunk once it reaches its capacity; when that happens the
	// oldest retained Idx advances and session_fetch reports Truncated=true
	// for any consumer whose FromIdx fell below it.
	outputBuf   []domain.ShellSessionOutputEvent
	nextIdx     int64
	ringFull    bool
	// bufferCap is the ring capacity in chunks; defaults to sessionBufferCap.
	// Kept on the struct (not a package constant) so tests can shrink it to
	// exercise the overflow/truncation branch without emitting thousands of
	// chunks.
	bufferCap int
}

func newSession() *session {
	return &session{wait: make(chan struct{}), bufferCap: sessionBufferCap}
}

func (s *session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// close kills the process and releases stream ends. Idempotent; safe to call
// from shell.session_close and from the reap goroutine's cleanup path. The
// process is killed before closing the read ends so a descendant holding an
// inherited pipe cannot keep a pump goroutine blocked forever.
func (s *session) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	stdin := s.stdin
	pty := s.pty
	stdout := s.stdout
	stderr := s.stderr
	s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.ptyCmd != nil && s.ptyCmd.Process != nil {
		_ = s.ptyCmd.Process.Kill()
	}
	if stdin != nil {
		_ = stdin.Close()
	}
	if pty != nil {
		_ = pty.Close()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	if stderr != nil {
		_ = stderr.Close()
	}
}

// write sends bytes to the session's stdin. In pty mode this is the pty
// master (writes drive the controlling terminal); in pipe mode it is the
// process stdin pipe. Returns an error once the session is closed so callers
// see the teardown instead of a raw pipe error.
func (s *session) write(data string) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, errors.New("session closed")
	}
	pty := s.pty
	in := s.stdin
	s.mu.Unlock()
	if pty != nil {
		in = pty
	}
	if in == nil {
		return 0, errors.New("session has no stdin")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return io.WriteString(in, data)
}

// resize sets the pty window size. Pipe mode is a no-op (the task spec).
func (s *session) resize(cols, rows int32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("session closed")
	}
	if s.pty == nil {
		return nil // pipe mode: no-op
	}
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols > 65535 {
		cols = 65535
	}
	if rows > 65535 {
		rows = 65535
	}
	return s.pty.Resize(int(cols), int(rows))
}

// normalizeTermSize clamps caller-supplied terminal dimensions. A zero value
// means "let the pty pick a default" (StartWithSize receives nil).
func normalizeTermSize(cols, rows int32) (int32, int32) {
	if cols < 1 || cols > 65535 {
		cols = 0
	}
	if rows < 1 || rows > 65535 {
		rows = 0
	}
	return cols, rows
}

// buildSessionCmd constructs the exec.Cmd for an interactive session of the
// resolved shell. No command string/flag is passed: the shell runs
// interactively (bash.exe, pwsh.exe, cmd.exe, sh …). The user's shell
// preference is honored because resolveShell() delegates to
// util.DetectShellInfo(), which respects util.SetShellPreference() applied by
// the workspace actor from accountPrefs.Preferences["shell"].
func interactiveShellArgs(name string) []string {
	switch name {
	case "powershell", "pwsh":
		return []string{"-NoLogo", "-NoExit"}
	case "cmd":
		return []string{"/Q"}
	case "bash", "gitbash", "sh":
		// No "-i": a pipe-backed stdin without a tty makes job-control bash
		// abort with "cannot set terminal process group". Interactivity is
		// driven by the caller; PTY mode passes a real tty already.
		return nil
	default:
		return nil
	}
}

func buildSessionCmd(parentCtx context.Context, sh systemShell, workingDirectory string) *exec.Cmd {
	args := interactiveShellArgs(sh.name)
	c := util.CommandContext(parentCtx, sh.path, args...)
	if workingDirectory != "" {
		c.Dir = workingDirectory
	} else if wd, err := os.Getwd(); err == nil && wd != "" {
		c.Dir = wd
	}
	if env := envWithExtras(sh.extraBin); env != nil {
		c.Env = env
	}
	return c
}

// startPTYSession starts the shell attached to a pseudo-terminal (ConPTY on
// Windows via aymanbagabas/go-pty — creack/pty has no Windows support, which
// previously forced every Windows session into non-interactive pipe mode).
// The caller is responsible for distinguishing ErrUnsupported (fall back to
// pipe) from hard failures.
func startPTYSession(parentCtx context.Context, sh systemShell, workingDirectory string, cols, rows int32) (*session, error) {
	p, err := gopt.New()
	if err != nil {
		return nil, err
	}
	if cols > 0 && rows > 0 {
		if err := p.Resize(int(cols), int(rows)); err != nil {
			_ = p.Close()
			return nil, fmt.Errorf("pty resize: %w", err)
		}
	}
	dir := workingDirectory
	if dir == "" {
		if wd, err := os.Getwd(); err == nil && wd != "" {
			dir = wd
		}
	}
	pc := p.CommandContext(parentCtx, sh.path, interactiveShellArgs(sh.name)...)
	pc.Dir = dir
	pc.Env = envWithExtras(sh.extraBin)
	if err := pc.Start(); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("pty start: %w", err)
	}
	s := newSession()
	s.pty = p
	s.ptyCmd = pc
	// stdin stays nil in pty mode: the pty itself is the write target (see
	// session.write); aliasing it into stdin would make close() tear the
	// ConPTY down twice (heap corruption on Windows).
	return s, nil
}

// startPipeSession starts the shell on plain stdin/stdout/stderr pipes. The
// shell is not a controlling terminal, so resize is a no-op (see session.resize).
func startPipeSession(c *exec.Cmd) (*session, error) {
	stdinPipe, err := c.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := c.StderrPipe()
	if err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := c.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return nil, fmt.Errorf("start (dir=%q): %w", c.Dir, err)
	}
	s := newSession()
	s.cmd = c
	s.stdin = stdinPipe
	s.stdout = stdoutPipe
	s.stderr = stderrPipe
	return s, nil
}

// pumpSessionOutput streams one output stream into shell.session_output
// events until EOF or session close. In pty mode stdout and stderr are merged
// on the master and emitted as "stdout" (same convention as project.shell_exec
// PTY mode); demuxing would require ANSI sequence parsing.
func (a *Actor) pumpSessionOutput(s *session, kind string, r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			a.emitSessionEvent(domain.ShellSessionOutputEvent{
				SessionID: s.id,
				Kind:      kind,
				Data:      SanitizeUTF8(string(buf[:n])),
			})
		}
		if err != nil {
			return
		}
	}
}

// reapSession waits for the process to exit, emits the terminal exit chunk,
// and removes the session from the registry. Exactly one reap goroutine per
// session owns closing s.wait.
func (a *Actor) reapSession(s *session) {
	defer close(s.wait)
	exitCode := int32(0)
	reason := "session exited"

	if s.ptyCmd != nil {
		waitErr := s.ptyCmd.Wait()
		switch {
		case waitErr != nil:
			var ee *exec.ExitError
			if errors.As(waitErr, &ee) {
				exitCode = int32(ee.ExitCode())
				reason = fmt.Sprintf("session exited with code %d", exitCode)
			} else {
				exitCode = 127
				reason = waitErr.Error()
			}
		case s.ptyCmd.ProcessState != nil:
			// go-pty's Wait returns nil even for non-zero exits on Windows;
			// the code lives in ProcessState.
			if code := s.ptyCmd.ProcessState.ExitCode(); code != 0 {
				exitCode = int32(code)
				reason = fmt.Sprintf("session exited with code %d", code)
			}
		}
	} else {
		waitErr := s.cmd.Wait()
		if waitErr != nil {
			var ee *exec.ExitError
			if errors.As(waitErr, &ee) {
				exitCode = int32(ee.ExitCode())
				reason = fmt.Sprintf("session exited with code %d", exitCode)
			} else {
				exitCode = 127
				reason = waitErr.Error()
			}
		}
	}
	// If the session was torn down explicitly (shell.session_close or actor
	// stop), surface that as the reason regardless of the wait error.
	if s.isClosed() {
		reason = "session closed"
	}

	// Release stream ends so a pump goroutine blocked on a descendant-held
	// pipe is unblocked. Idempotent; no-op if close already ran.
	s.close()

	a.emitSessionEvent(domain.ShellSessionOutputEvent{
		SessionID: s.id,
		Kind:      "exit",
		Data:      reason,
		ExitCode:  exitCode,
		Mode:      s.mode,
	})
	a.removeSession(s.id)
}

// emitSessionEvent publishes one session output event to
// shell.session_output subscribers and appends it to the per-session ring
// buffer under the session lock, so a later session_fetch replays exactly the
// chunks a subscriber received. Safe for concurrent calls (the session mu
// serializes append; EmitEvent itself is thread-safe). Uses the OnStart
// context retained on the actor so background pump/reap goroutines can emit
// outside a handler invocation — the same pattern mcpinstance.watchSession
// uses.
func (a *Actor) emitSessionEvent(ev domain.ShellSessionOutputEvent) {
	if a.sessionCtx == nil {
		return
	}
	s, err := a.lookupSession(ev.SessionID)
	if err == nil {
		// appendOutput assigns the monotonic Idx and returns it so the
		// emitted event carries the same Idx the buffer stores — replay is
		// exactly equivalent to the subscribed stream.
		ev.Idx = s.appendOutput(ev)
	} else {
		// The session was removed from the registry (reap completed); there
		// is no live buffer to append to, and subscribers should already be
		// done. Still surface the event for any in-flight subscriber.
		a.sessionCtx.Logger().Warn("shell.session_output emit for unknown session",
			"sessionId", ev.SessionID, "kind", ev.Kind)
	}
	if err := a.sessionCtx.EmitEvent(SessionOutputEventKind, ev); err != nil {
		a.sessionCtx.Logger().Warn("shell.session_output emit failed",
			"sessionId", ev.SessionID, "kind", ev.Kind, "error", err)
	}
}

// appendOutput pushes one chunk into the session's ring buffer, assigns its
// monotonic Idx, and returns that Idx. Must hold s.mu. When the ring is full
// the oldest chunk is overwritten (ringFull stays true); session_fetch then
// reports Truncated=true for any consumer whose FromIdx fell below the new
// oldest retained Idx.
func (s *session) appendOutput(ev domain.ShellSessionOutputEvent) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev.Idx = s.nextIdx
	s.nextIdx++
	if len(s.outputBuf) < s.bufferCap {
		s.outputBuf = append(s.outputBuf, ev)
		return ev.Idx
	}
	// Ring full: shift left by one (dropping the oldest at index 0) and write
	// the new chunk at the tail. outputBuf keeps length == s.bufferCap.
	copy(s.outputBuf, s.outputBuf[1:])
	s.outputBuf[len(s.outputBuf)-1] = ev
	s.ringFull = true
	return ev.Idx
}

// fetchOutput returns the buffered chunks with Idx >= from, the next Idx to
// fetch from, and whether the returned window is non-contiguous with the
// consumer's view (i.e. the ring overwrote chunks with Idx < from, so the
// consumer's requested window starts before the oldest retained chunk). Must
// hold s.mu.
func (s *session) fetchOutput(from int64) ([]domain.ShellSessionOutputEvent, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from < 0 {
		from = 0
	}
	if len(s.outputBuf) == 0 {
		return nil, from, false
	}
	// The ring holds chunks with contiguous Idx. The smallest buffered Idx is
	// the first element's Idx; if from is below it, the ring overwrote chunks
	// the consumer is asking for, so the returned window is non-contiguous and
	// we flag truncation. A consumer paging forward with NextIdx never hits
	// this because NextIdx always stays within the retained range.
	firstIdx := s.outputBuf[0].Idx
	trunc := from < firstIdx
	start := 0
	if from > firstIdx {
		// The buffer is ordered by Idx, so the requested window starts at
		// offset from-firstIdx if that chunk is still buffered.
		offset := from - firstIdx
		if offset < int64(len(s.outputBuf)) {
			start = int(offset)
		} else {
			// from is beyond everything buffered: nothing matches.
			return nil, from, trunc
		}
	}
	chunks := s.outputBuf[start:]
	next := s.nextIdx
	return chunks, next, trunc
}

func (a *Actor) registerSession(s *session) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	if a.sessions == nil {
		a.sessions = map[string]*session{}
	}
	a.sessions[s.id] = s
}

func (a *Actor) lookupSession(id string) (*session, error) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	s, ok := a.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return s, nil
}

func (a *Actor) removeSession(id string) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	delete(a.sessions, id)
}

// handleSessionOpen starts a persistent interactive shell session. PTY
// (ConPTY where supported) is attempted first; on failure the session falls
// back to plain pipes and reports Mode accordingly. Output is delivered on
// shell.session_output; the frontend filters by SessionId.
func (a *Actor) handleSessionOpen(ctx actor.PureContext, req domain.ShellSessionOpenReq) (domain.ShellSessionOpenResp, error) {
	sh, err := resolveShell()
	if err != nil {
		return domain.ShellSessionOpenResp{}, fmt.Errorf("shell.session_open: %w", err)
	}

	parentCtx := a.lifecycleCtx
	if parentCtx == nil {
		// Direct callers and unit tests may construct the Actor without
		// running OnStart; mirror handleBash's fallback.
		parentCtx = context.Background()
	}

	workingDirectory, _, err := ResolveShellDir(req.WorkingDirectory)
	if err != nil {
		return domain.ShellSessionOpenResp{}, fmt.Errorf("shell.session_open: %w", err)
	}
	cols, rows := normalizeTermSize(req.Cols, req.Rows)

	var (
		s    *session
		mode = "pipe"
	)
	if !a.sessionForcePipe {
		if ptySess, ptyErr := startPTYSession(parentCtx, sh, workingDirectory, cols, rows); ptyErr == nil {
			s = ptySess
			mode = "pty"
		} else if !errors.Is(ptyErr, gopt.ErrUnsupported) {
			// Hard PTY failure: surface it rather than silently masking it
			// with a pipe fallback that would only also fail.
			return domain.ShellSessionOpenResp{}, fmt.Errorf("shell.session_open: %w", ptyErr)
		}
		// ErrUnsupported → fall through to pipe mode with a fresh cmd.
	}
	if s == nil {
		c := buildSessionCmd(parentCtx, sh, workingDirectory)
		pipeSess, err := startPipeSession(c)
		if err != nil {
			return domain.ShellSessionOpenResp{}, fmt.Errorf("shell.session_open: %w", err)
		}
		s = pipeSess
	}

	s.id = ctx.NewID().String()
	s.mode = mode
	s.dir = workingDirectory
	a.registerSession(s)

	if mode == "pty" {
		go a.pumpSessionOutput(s, "stdout", s.pty)
	} else {
		go a.pumpSessionOutput(s, "stdout", s.stdout)
		go a.pumpSessionOutput(s, "stderr", s.stderr)
	}
	go a.reapSession(s)

	return domain.ShellSessionOpenResp{SessionID: s.id, Mode: mode}, nil
}

// handleSessionsCloseByPath closes every session whose working directory is
// at or under req.Path. Internal callable used by the project actor to
// release process handles before deleting a worktree directory: on Windows a
// live process cwd keeps the directory locked, so merge/discard teardown
// would otherwise fail with a sharing violation.
func (a *Actor) handleSessionsCloseByPath(_ actor.PureContext, req gen.ShellSessionsCloseByPathReq) (gen.ShellSessionsCloseByPathResp, error) {
	if strings.TrimSpace(req.Path) == "" {
		return gen.ShellSessionsCloseByPathResp{}, fmt.Errorf("shell.sessions_close_by_path: path required")
	}
	a.sessionsMu.Lock()
	var matched []*session
	for _, s := range a.sessions {
		if dirUnderPath(s.dir, req.Path) {
			matched = append(matched, s)
		}
	}
	a.sessionsMu.Unlock()
	resp := gen.ShellSessionsCloseByPathResp{}
	for _, s := range matched {
		s.close()
		resp.Closed = append(resp.Closed, s.id)
		select {
		case <-s.wait:
		case <-time.After(sessionWaitTimeout):
		}
	}
	return resp, nil
}

// dirUnderPath reports whether the session cwd dir is root or a descendant
// of root (see util.PathWithin; folded for Windows case-insensitivity).
func dirUnderPath(dir, root string) bool {
	return util.PathWithin(dir, root)
}

// handleSessionWrite writes bytes to a session's stdin.
func (a *Actor) handleSessionWrite(ctx actor.PureContext, req domain.ShellSessionWriteReq) (domain.ShellSessionWriteResp, error) {
	if req.SessionID == "" {
		return domain.ShellSessionWriteResp{}, errors.New("sessionId required")
	}
	s, err := a.lookupSession(req.SessionID)
	if err != nil {
		return domain.ShellSessionWriteResp{}, err
	}
	if _, err := s.write(req.Data); err != nil {
		return domain.ShellSessionWriteResp{}, fmt.Errorf("session %q: write: %w", s.id, err)
	}
	return domain.ShellSessionWriteResp{}, nil
}

// handleSessionResize resizes a session's pty. Pipe mode is a no-op.
func (a *Actor) handleSessionResize(ctx actor.PureContext, req domain.ShellSessionResizeReq) (domain.ShellSessionResizeResp, error) {
	if req.SessionID == "" {
		return domain.ShellSessionResizeResp{}, errors.New("sessionId required")
	}
	s, err := a.lookupSession(req.SessionID)
	if err != nil {
		return domain.ShellSessionResizeResp{}, err
	}
	if err := s.resize(req.Cols, req.Rows); err != nil {
		return domain.ShellSessionResizeResp{}, fmt.Errorf("session %q: resize: %w", s.id, err)
	}
	return domain.ShellSessionResizeResp{}, nil
}

// handleSessionClose tears down a session: kills the process, emits the
// terminal exit chunk via the reap goroutine, and removes the session from
// the registry. This is the "consumer disconnected" path — the terminal panel
// calls it when the user closes the tab, which propagates the kill that
// shellexec.go:196-209's propagateCancel achieves for one-shot streams.
func (a *Actor) handleSessionClose(ctx actor.PureContext, req domain.ShellSessionCloseReq) (domain.ShellSessionCloseResp, error) {
	if req.SessionID == "" {
		return domain.ShellSessionCloseResp{}, errors.New("sessionId required")
	}
	s, err := a.lookupSession(req.SessionID)
	if err != nil {
		return domain.ShellSessionCloseResp{}, err
	}
	s.close()
	select {
	case <-s.wait:
	case <-time.After(sessionWaitTimeout):
	}
	return domain.ShellSessionCloseResp{}, nil
}

// handleSessionFetch replays a session's buffered output from FromIdx.
// Stateless callable (actor.PureContext, concurrent-safe): the ring buffer and
// Idx counter live on the per-session struct and are read under the same
// session mu that emitSessionEvent's append uses, so the returned chunks are
// exactly the events a subscriber received, in order. Truncated is true when
// the ring overwrote chunks below FromIdx (or any earlier history), meaning
// the consumer's view is non-contiguous and it should treat the replay as a
// fresh baseline.
func (a *Actor) handleSessionFetch(ctx actor.PureContext, req domain.ShellSessionFetchReq) (domain.ShellSessionFetchResp, error) {
	if req.SessionID == "" {
		return domain.ShellSessionFetchResp{}, errors.New("sessionId required")
	}
	s, err := a.lookupSession(req.SessionID)
	if err != nil {
		return domain.ShellSessionFetchResp{}, err
	}
	chunks, nextIdx, truncated := s.fetchOutput(req.FromIdx)
	return domain.ShellSessionFetchResp{
		Chunks:    chunks,
		NextIdx:   nextIdx,
		Truncated: truncated,
	}, nil
}

// closeAllSessions kills every live session. Called from OnStop so a
// restarting/stopping shell actor does not leak child processes. The reap
// goroutines emit the terminal exit chunk; events emitted after the actor
// stops are dropped by the runtime (DiagEventAfterStop) and ignored here.
func (a *Actor) closeAllSessions() {
	a.sessionsMu.Lock()
	sessions := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.sessionsMu.Unlock()
	for _, s := range sessions {
		s.close()
	}
}
