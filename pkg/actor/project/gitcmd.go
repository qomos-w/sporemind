package project

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"github.com/qomos-w/sporemind/pkg/util"
)

// gitTimeout is the ceiling for any git CLI invocation spawned by project
// handlers. It prevents a hung git subcommand (SSH passphrase prompt,
// unreachable remote, credential helper) from blocking a stateless handler
// goroutine indefinitely.
const gitTimeout = 120 * time.Second

// gitCtx derives a context that is cancelled when the owning cell is done
// (done == nil => no cell linkage), combined with a hard timeout.
func gitCtx(done <-chan struct{}) (context.Context, context.CancelFunc) {
	return gitCtxWithTimeout(gitTimeout, done)
}

// gitCtxWithTimeout is the parameterized version of gitCtx. It lets tests (and
// gitRunWithTimeout) inject a short deadline without making gitTimeout mutable.
func gitCtxWithTimeout(timeout time.Duration, done <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if done == nil {
		return ctx, cancel
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-done:
			cancel()
		case <-stop:
		}
	}()
	// Wrap cancel so the watcher goroutine also exits.
	return ctx, func() {
		close(stop)
		cancel()
	}
}

// gitCmd builds a `git -C dir args...` command bound to ctx with stdin wired
// to /dev/null, so git can never block on an interactive passphrase /
// credential prompt. Returns the ready-to-run cmd.
func gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := util.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(nil)
	return cmd
}
