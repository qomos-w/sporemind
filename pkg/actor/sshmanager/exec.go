package sshmanager

import (
	"bytes"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"golang.org/x/crypto/ssh"
)

// ---------------------------------------------------------------------------
// sshmanager.exec — one-shot synchronous remote command execution
// ---------------------------------------------------------------------------

const (
	// defaultExecTimeoutMs is the default timeout (ms) when the caller omits
	// Timeout or passes an unusable value. Mirrors shell.exec semantics.
	defaultExecTimeoutMs = 30000
	// minExecTimeoutMs guards against callers passing seconds by mistake; a
	// 30ms timeout fires before most remote commands produce output.
	minExecTimeoutMs = 1000
	// LLM-facing response budgets (head+tail keep). Same values as shell.
	maxExecStdoutBytes = 30000
	maxExecStderrBytes = 10000
	// maxExecCollectBytes caps in-memory collection of remote output to guard
	// against runaway commands; comfortably above the response budgets so the
	// head/tail Truncated flag stays accurate.
	maxExecCollectBytes = 256 * 1024

	// execIdleTimeout is how long a cached exec connection may sit idle before
	// the reclamation goroutine closes it.
	execIdleTimeout = 10 * time.Minute
	// execReclaimInterval is the sweep cadence.
	execReclaimInterval = time.Minute

	// timeoutExitCode mirrors the convention used by shell.exec / coreutils
	// `timeout` for a killed-by-timeout command.
	timeoutExitCode = 124
)

// execConn is a cached one-shot exec connection. inFlight counts how many
// exec calls are currently using it so the idle reclaimer never closes a
// connection mid-command.
type execConn struct {
	client   *ssh.Client
	lastUsed time.Time
	inFlight int
}

// resolveExecTimeout interprets an incoming Timeout field (milliseconds) using
// the same clamp semantics as shell.exec: values at or above minExecTimeoutMs
// are honored as-is, while smaller values (including zero/negative) fall back
// to the default.
func resolveExecTimeout(reqMs int32) time.Duration {
	if reqMs <= 0 || reqMs < minExecTimeoutMs {
		return defaultExecTimeoutMs * time.Millisecond
	}
	return time.Duration(reqMs) * time.Millisecond
}

// execConnKey builds the cache key for a (caller, host) exec connection.
func execConnKey(owner, hostID string) string {
	return owner + "\x00" + hostID
}

// dialExec resolves the dial function, defaulting to buildSSHClient when no
// override is set (e.g. in unit tests).
func (a *Actor) dialExec(host *domain.SshHost) (*ssh.Client, error) {
	if a.execDial != nil {
		return a.execDial(host)
	}
	return buildSSHClient(host)
}

// acquireExecConn returns a usable *ssh.Client for (owner, hostID), reusing a
// cached connection when present and dialing a fresh one otherwise. The
// returned release func must be called when the caller is done executing.
// Dialing happens outside the lock (a slow handshake must not block other
// callers); a re-check after dial discards a duplicate if a concurrent caller
// won the race, so no connection is leaked.
func (a *Actor) acquireExecConn(owner string, host *domain.SshHost) (client *ssh.Client, key string, release func(), err error) {
	key = execConnKey(owner, host.ID)

	a.mu.Lock()
	if a.execConns == nil {
		a.execConns = make(map[string]*execConn)
	}
	if ec := a.execConns[key]; ec != nil {
		ec.inFlight++
		ec.lastUsed = time.Now()
		client = ec.client
		a.mu.Unlock()
		return client, key, a.releaseFunc(key), nil
	}
	a.mu.Unlock()

	// Cold path: dial outside the lock.
	client, err = a.dialExec(host)
	if err != nil {
		return nil, key, nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if existing := a.execConns[key]; existing != nil {
		// Concurrent caller populated the entry first; discard our duplicate.
		_ = client.Close()
		existing.inFlight++
		existing.lastUsed = time.Now()
		return existing.client, key, a.releaseFunc(key), nil
	}
	if a.execConns == nil {
		a.execConns = make(map[string]*execConn)
	}
	a.execConns[key] = &execConn{client: client, lastUsed: time.Now(), inFlight: 1}
	return client, key, a.releaseFunc(key), nil
}

// releaseFunc builds the deferred inFlight decrement for a cache key.
func (a *Actor) releaseFunc(key string) func() {
	return func() { a.decrExecInFlight(key) }
}

func (a *Actor) decrExecInFlight(key string) {
	a.mu.Lock()
	if ec := a.execConns[key]; ec != nil {
		ec.inFlight--
		if ec.inFlight < 0 {
			ec.inFlight = 0
		}
	}
	a.mu.Unlock()
}

// dropExecConn closes and removes a cached connection (e.g. after the client
// is found to be dead). Safe to call when the key is absent.
func (a *Actor) dropExecConn(key string) {
	a.mu.Lock()
	ec := a.execConns[key]
	delete(a.execConns, key)
	a.mu.Unlock()
	if ec != nil && ec.client != nil {
		_ = ec.client.Close()
	}
}

// reclaimIdleExecConns periodically closes cached exec connections that have
// been idle longer than execIdleTimeout and have no in-flight command.
func (a *Actor) reclaimIdleExecConns() {
	if a.execCtx == nil {
		return
	}
	ticker := time.NewTicker(execReclaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.execCtx.Done():
			return
		case <-ticker.C:
			a.sweepIdleExecConns()
		}
	}
}

func (a *Actor) sweepIdleExecConns() {
	now := time.Now()
	a.mu.Lock()
	var toClose []*execConn
	for k, ec := range a.execConns {
		if ec.inFlight == 0 && now.Sub(ec.lastUsed) > execIdleTimeout {
			toClose = append(toClose, ec)
			delete(a.execConns, k)
		}
	}
	a.mu.Unlock()
	for _, ec := range toClose {
		if ec.client != nil {
			_ = ec.client.Close()
		}
	}
}

// execResult holds the outcome of a single command execution attempt.
type execResult struct {
	stdout   string
	stderr   string
	exitCode int32
	timedOut bool
}

// execOnConn runs cmd on client with a timeout. A non-nil error indicates a
// connection/transport failure (caller should reconnect once); a normal
// non-zero exit is returned via execResult.exitCode with err == nil.
func (a *Actor) execOnConn(client *ssh.Client, cmd string, timeout time.Duration) (execResult, error) {
	sess, err := client.NewSession()
	if err != nil {
		return execResult{}, err // transport likely dead
	}
	defer sess.Close()

	stdout := &cappedBuffer{cap: maxExecCollectBytes}
	stderr := &cappedBuffer{cap: maxExecCollectBytes}
	sess.Stdout = stdout
	sess.Stderr = stderr

	type doneMsg struct{ err error }
	done := make(chan doneMsg, 1)
	go func() {
		done <- doneMsg{err: sess.Run(cmd)}
	}()

	var res execResult
	select {
	case <-time.After(timeout):
		// Best-effort kill: signal the remote process then close the channel
		// so the goroutine unblocks. We do not treat this as a connection
		// error — the client may still be healthy for the next command.
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		<-done
		res = execResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: timeoutExitCode, timedOut: true}
		return res, nil
	case msg := <-done:
		if msg.err == nil {
			return execResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: 0}, nil
		}
		if ee, ok := msg.err.(*ssh.ExitError); ok {
			return execResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: int32(ee.ExitStatus())}, nil
		}
		// Non-exit error ⇒ transport failure (EOF, channel closed, …).
		return execResult{stdout: stdout.String(), stderr: stderr.String()}, msg.err
	}
}

// handleExec runs Command on the host identified by HostId and returns
// stdout/stderr/exit code. It reuses a cached connection keyed by
// (caller, host); if that connection has dropped it reconnects once and
// retries. Output is head+tail truncated to the response budgets.
func (a *Actor) handleExec(ctx actor.PureContext, req domain.SshExecReq) (domain.SshExecResp, error) {
	owner, err := requireSSHToolCaller(ctx)
	if err != nil {
		return domain.SshExecResp{}, err
	}
	host := a.findHost(req.HostID)
	if host == nil {
		return domain.SshExecResp{}, fmt.Errorf("host %q not found", req.HostID)
	}
	if !a.hostVisibleToCaller(ctx, req.HostID) {
		return domain.SshExecResp{}, fmt.Errorf("host %q not found", req.HostID)
	}
	if strings.TrimSpace(req.Command) == "" {
		return domain.SshExecResp{}, fmt.Errorf("command is required")
	}

	resolved := a.hostWithCredential(host)
	timeout := resolveExecTimeout(req.Timeout)
	start := time.Now()

	client, key, release, err := a.acquireExecConn(owner, &resolved)
	if err != nil {
		return domain.SshExecResp{}, fmt.Errorf("ssh exec: connect: %w", err)
	}

	res, runErr := a.execOnConn(client, req.Command, timeout)
	release()

	if runErr != nil {
		// Transport failure: the cached connection has dropped. Discard it,
		// reconnect once (via acquireExecConn so the same race protection and
		// inFlight bookkeeping apply), and retry the command exactly once.
		a.dropExecConn(key)
		client2, _, release2, dialErr := a.acquireExecConn(owner, &resolved)
		if dialErr != nil {
			return domain.SshExecResp{
				Stderr:     fmt.Sprintf("ssh exec: connection lost (%v) and reconnect failed: %v", runErr, dialErr),
				ExitCode:   -1,
				DurationMs: int32(time.Since(start) / time.Millisecond),
			}, nil
		}
		res2, runErr2 := a.execOnConn(client2, req.Command, timeout)
		release2()
		if runErr2 != nil {
			a.dropExecConn(execConnKey(owner, host.ID))
			return domain.SshExecResp{
				Stderr:     fmt.Sprintf("ssh exec: %v", runErr2),
				ExitCode:   -1,
				DurationMs: int32(time.Since(start) / time.Millisecond),
			}, nil
		}
		res = res2
	}

	stdout, outTrunc := truncateExecOutput(res.stdout, maxExecStdoutBytes)
	stderr, errTrunc := truncateExecOutput(res.stderr, maxExecStderrBytes)
	if res.timedOut {
		toMsg := fmt.Sprintf("ssh exec: timed out after %v", timeout)
		if stderr == "" {
			stderr = toMsg
		} else {
			stderr = stderr + "\n" + toMsg
		}
	}
	return domain.SshExecResp{
		Stdout:     stdout,
		Stderr:     stderr,
		ExitCode:   res.exitCode,
		Truncated:  outTrunc || errTrunc,
		DurationMs: int32(time.Since(start) / time.Millisecond),
	}, nil
}

// ---------------------------------------------------------------------------
// Output truncation + capped collection
// ---------------------------------------------------------------------------

// truncateExecOutput keeps the head and tail of s when it exceeds maxBytes,
// inserting a marker for the dropped middle. Returns the (possibly truncated)
// string and whether truncation occurred. Mirrors shell.truncateHeadTail.
func truncateExecOutput(s string, maxBytes int) (string, bool) {
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

// cappedBuffer is a bytes.Buffer that stops accepting data once cap is reached,
// silently discarding overflow (so sess.Run never aborts on a write error).
type cappedBuffer struct {
	buf  bytes.Buffer
	cap  int
	full bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.full {
		return len(p), nil
	}
	remaining := b.cap - b.buf.Len()
	if remaining <= 0 {
		b.full = true
		return len(p), nil
	}
	if len(p) <= remaining {
		return b.buf.Write(p)
	}
	b.buf.Write(p[:remaining])
	b.full = true
	return len(p), nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }
