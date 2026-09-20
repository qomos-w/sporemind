package sshmanager

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// ---------------------------------------------------------------------------
// sshmanager.shell_run — synchronous command execution on an interactive PTY
// session. Writes the command to the session's stdin (appended with a sentinel
// marker that captures the exit code), drains PTY output from the broadcaster
// until the sentinel arrives or the timeout expires, strips ANSI escapes and
// the echoed command line, and returns the plain-text output + exit code.
// ---------------------------------------------------------------------------

const (
	// shellRunSentinel is the unique marker written after the command to
	// detect command completion and capture the exit code. The format on
	// the wire is: __SR_EOF__<exitcode>__
	shellRunSentinel = "__SR_EOF__"

	// defaultShellRunTimeoutMs mirrors exec's default timeout.
	defaultShellRunTimeoutMs = 30000
	// minShellRunTimeoutMs mirrors exec's minimum.
	minShellRunTimeoutMs = 1000

	// maxShellRunOutputBytes is the LLM-facing response budget (head+tail).
	maxShellRunOutputBytes = 30000

	// maxShellRunCollectBytes caps in-memory accumulation to guard against
	// runaway output before the sentinel arrives.
	maxShellRunCollectBytes = 256 * 1024

	// shellRunDrainInterval is the polling cadence for checking the
	// accumulated output for the sentinel pattern.
	shellRunDrainInterval = 50 * time.Millisecond
)

// sentinelRe matches __SR_EOF__<exitcode>__ anywhere in the output, possibly
// with a trailing \r before the closing __.
var sentinelRe = regexp.MustCompile(regexp.QuoteMeta(shellRunSentinel) + `(-?\d+)__`)

// handleShellRun writes Command to the PTY session's stdin with a sentinel
// echo, drains the broadcaster for new output until the sentinel or timeout,
// and returns the plain-text result.
func (a *Actor) handleShellRun(ctx actor.PureContext, req domain.SshShellRunReq) (domain.SshShellRunResp, error) {
	sess, err := a.toolSession(ctx, req.SessionID)
	if err != nil {
		return domain.SshShellRunResp{}, err
	}
	if !sess.isConnected() {
		return domain.SshShellRunResp{}, fmt.Errorf("session %q is not connected", req.SessionID)
	}

	sess.runMu.Lock()
	defer sess.runMu.Unlock()

	timeout := resolveShellRunTimeout(req.TimeoutMs)

	// Subscribe to the broadcaster. replay holds bytes that arrived before
	// this call (banner, previous command output); we skip those by length.
	ch, replay := sess.out.subscribe()
	defer sess.out.unsubscribe(ch)
	replayLen := 0
	for _, b := range replay {
		replayLen += len(b)
	}

	// Write command + sentinel. The `;` after the command ensures the
	// sentinel runs even if the command has trailing content, and
	// `printf` is used for broad shell compatibility.
	fullCmd := req.Command + " ; printf '" + shellRunSentinel + "%d__\\n' $?\n"
	if _, err := sess.stdin.Write([]byte(fullCmd)); err != nil {
		return domain.SshShellRunResp{}, fmt.Errorf("write command: %w", err)
	}

	// Drain live output from the channel until we see the sentinel or
	// the timeout fires.
	var buf []byte
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(shellRunDrainInterval)
	defer ticker.Stop()

	for {
		// Check accumulated buffer for the sentinel.
		if loc := sentinelRe.FindIndex(buf); loc != nil {
			return buildShellRunResp(buf[:loc[0]], loc, buf, req)
		}

		// Cap in-memory accumulation.
		if len(buf) >= maxShellRunCollectBytes {
			return buildShellRunResp(buf, nil, buf, req)
		}

		select {
		case chunk, ok := <-ch:
			if !ok {
				// Session closed mid-drain; return whatever we have.
				return buildShellRunResp(buf, nil, buf, req)
			}
			buf = append(buf, chunk...)
		case <-ticker.C:
			if time.Now().After(deadline) {
				finalOut, _ := truncateShellRunOutput(stripANSI(stripEchoPrefix(string(buf), req.Command)))
				return domain.SshShellRunResp{
					Output:    finalOut,
					ExitCode:  -1,
					Truncated: len(buf) > maxShellRunOutputBytes,
					TimedOut:  true,
				}, nil
			}
		}
	}
}

// buildShellRunResp assembles the final response from the accumulated buffer.
// If loc is non-nil the sentinel was found: output is everything before it,
// and the exit code is extracted from the sentinel match.
func buildShellRunResp(output []byte, loc []int, fullBuf []byte, req domain.SshShellRunReq) (domain.SshShellRunResp, error) {
	var exitCode int
	if loc != nil {
		match := sentinelRe.FindSubmatch(fullBuf[loc[0]:loc[1]])
		if len(match) >= 2 {
			exitCode, _ = strconv.Atoi(string(match[1]))
		}
	} else {
		exitCode = -1
	}

	raw := string(output)
	stripped := stripANSI(raw)
	result := stripEchoPrefix(stripped, req.Command)
	finalOut, truncated := truncateShellRunOutput(result)

	return domain.SshShellRunResp{
		Output:    finalOut,
		ExitCode:  int32(exitCode),
		Truncated: truncated,
		TimedOut:  false,
	}, nil
}

// resolveShellRunTimeout clamps the requested timeout the same way exec does.
func resolveShellRunTimeout(reqMs int32) time.Duration {
	if reqMs <= 0 || reqMs < minShellRunTimeoutMs {
		return defaultShellRunTimeoutMs * time.Millisecond
	}
	return time.Duration(reqMs) * time.Millisecond
}

// stripANSI removes ANSI escape sequences (CSI and OSC) from the output so
// the LLM receives plain text instead of terminal control codes.
func stripANSI(s string) string {
	return ansiEscapeSeq.ReplaceAllString(s, "")
}

// stripEchoPrefix removes the PTY-echoed command line from the beginning of
// the output. PTY echo produces the command text followed by \r\n before the
// actual command output. If the first line matches the command it is removed;
// otherwise the output is returned as-is (echo may be off).
func stripEchoPrefix(output, command string) string {
	// Normalize: PTY uses \r\n, we split on either.
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	lines := strings.SplitN(normalized, "\n", 2)
	if len(lines) == 0 {
		return output
	}
	first := strings.TrimSpace(lines[0])
	cmd := strings.TrimSpace(command)
	if first == cmd {
		if len(lines) > 1 {
			return lines[1]
		}
		return ""
	}
	return output
}

// truncateShellRunOutput applies head+tail truncation mirroring exec's
// truncateExecOutput, using the shell_run budget.
func truncateShellRunOutput(s string) (string, bool) {
	return truncateExecOutput(s, maxShellRunOutputBytes)
}