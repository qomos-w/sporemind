package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestSessionsCloseByPath closes sessions whose working directory is at or
// under the given path and leaves sessions elsewhere running. This is the
// handle-release path the project actor calls before deleting a worktree
// directory.
func TestSessionsCloseByPath(t *testing.T) {
	a, ctx := newSessionTestActor(t)

	wtDir := t.TempDir()
	subDir := filepath.Join(wtDir, "pkg")
	otherDir := t.TempDir()
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", subDir, err)
	}

	inWorktree, err := a.handleSessionOpen(ctx, domain.ShellSessionOpenReq{WorkingDirectory: subDir})
	if err != nil {
		t.Fatalf("session_open(worktree): %v", err)
	}
	outside, err := a.handleSessionOpen(ctx, domain.ShellSessionOpenReq{WorkingDirectory: otherDir})
	if err != nil {
		t.Fatalf("session_open(outside): %v", err)
	}

	resp, err := a.handleSessionsCloseByPath(ctx, gen.ShellSessionsCloseByPathReq{Path: wtDir})
	if err != nil {
		t.Fatalf("sessions_close_by_path: %v", err)
	}
	if len(resp.Closed) != 1 || resp.Closed[0] != inWorktree.SessionID {
		t.Fatalf("Closed = %v, want [%s]", resp.Closed, inWorktree.SessionID)
	}
	if _, err := a.lookupSession(inWorktree.SessionID); err == nil {
		t.Error("worktree session must be removed from the registry after close")
	}
	if _, err := a.lookupSession(outside.SessionID); err != nil {
		t.Fatalf("outside session must survive: %v", err)
	}

	// Path with no sessions is not an error: best-effort teardown.
	resp, err = a.handleSessionsCloseByPath(ctx, gen.ShellSessionsCloseByPathReq{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("sessions_close_by_path(empty dir): %v", err)
	}
	if len(resp.Closed) != 0 {
		t.Fatalf("Closed = %v, want none", resp.Closed)
	}

	if _, err := a.handleSessionsCloseByPath(ctx, gen.ShellSessionsCloseByPathReq{Path: ""}); err == nil {
		t.Fatal("empty path must be rejected")
	}

	// Close the surviving session before t.TempDir cleanup removes its cwd
	// (TempDir cleanups run LIFO and would otherwise hit the same sharing
	// violation this whole mechanism exists to prevent).
	if _, err := a.handleSessionsCloseByPath(ctx, gen.ShellSessionsCloseByPathReq{Path: otherDir}); err != nil {
		t.Fatalf("close outside session: %v", err)
	}
}
