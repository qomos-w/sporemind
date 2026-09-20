package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// callerCtx builds a FakeCtx whose Caller() returns a ref carrying the given
// agent actor id, so callerID(ctx) returns that agent's actor id string.
func copyCallerCtx(t *testing.T, agentIDHex string) *testutil.FakeCtx {
	t.Helper()
	cid, err := identity.ParseCanonicalID(agentIDHex)
	if err != nil {
		t.Fatalf("callerCtx: parse %q: %v", agentIDHex, err)
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.CallerRef = testutil.NewFakeRef(id.From(cid), nil)
	return ctx
}

func TestHandleWorktreeCopy_CopiesFileIntoBoundWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "copy-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentID := "019fa666660000000000000000000006"
	agentKey := bindKey(t, agentID)
	if err := a.bindWorktree(agentKey, wt.ID); err != nil {
		t.Fatalf("bind worktree: %v", err)
	}

	writeFileIn(t, dir, "seed.txt", "seed from main repo")

	ctx := copyCallerCtx(t, agentID)
	resp, err := a.handleWorktreeCopy(ctx, gen.ProjectWorktreeCopyReq{
		WorktreeID: wt.ID,
		Sources:    []string{"seed.txt"},
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if len(resp.Copied) != 1 || resp.Copied[0].Source != "seed.txt" {
		t.Fatalf("expected 1 copied source, got %+v", resp.Copied)
	}
	if resp.Copied[0].Dest != "seed.txt" {
		t.Fatalf("expected dest seed.txt, got %q", resp.Copied[0].Dest)
	}
	if resp.Copied[0].Overwritten {
		t.Fatal("expected Overwritten=false for new file")
	}
	if len(resp.Skipped) != 0 {
		t.Fatalf("expected 0 skipped, got %+v", resp.Skipped)
	}

	got, err := os.ReadFile(filepath.Join(wt.Path, "seed.txt"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != "seed from main repo" {
		t.Fatalf("copied content = %q, want %q", got, "seed from main repo")
	}
}

func TestHandleWorktreeCopy_RejectsEmptySources(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "empty-src-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentID := "019fa666660000000000000000000007"
	if err := a.bindWorktree(bindKey(t, agentID), wt.ID); err != nil {
		t.Fatalf("bind worktree: %v", err)
	}

	ctx := copyCallerCtx(t, agentID)
	_, err = a.handleWorktreeCopy(ctx, gen.ProjectWorktreeCopyReq{WorktreeID: wt.ID, Sources: []string{}})
	if err == nil {
		t.Fatal("expected error for empty Sources")
	}
}

func TestHandleWorktreeCopy_RejectsUnboundCaller(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "unbound-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}

	ctx := testutil.AdminCtx(testutil.GenActorID()) // no caller
	_, err = a.handleWorktreeCopy(ctx, gen.ProjectWorktreeCopyReq{
		WorktreeID: wt.ID,
		Sources:    []string{"x.txt"},
	})
	if err == nil {
		t.Fatal("expected error for unbound caller")
	}
}

func TestHandleWorktreeCopy_RejectsWorktreeNotBoundToCaller(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt1, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "wt1", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create wt1: %v", err)
	}
	wt2, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "wt2", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create wt2: %v", err)
	}
	agentID := "019fa666660000000000000000000008"
	if err := a.bindWorktree(bindKey(t, agentID), wt1.ID); err != nil {
		t.Fatalf("bind agent to wt1: %v", err)
	}

	ctx := copyCallerCtx(t, agentID)
	_, err = a.handleWorktreeCopy(ctx, gen.ProjectWorktreeCopyReq{
		WorktreeID: wt2.ID,
		Sources:    []string{"x.txt"},
	})
	if err == nil {
		t.Fatal("expected error when caller bound to a different worktree")
	}
}

func TestHandleWorktreeCopy_OverwriteGuardSkipsModifiedDestination(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	writeFileIn(t, dir, "shared.txt", "main repo version")
	gitCommitIn(t, dir, "add shared.txt", "shared.txt")

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "guard-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentID := "019fa666660000000000000000000009"
	if err := a.bindWorktree(bindKey(t, agentID), wt.ID); err != nil {
		t.Fatalf("bind worktree: %v", err)
	}

	// Worker modifies the file in the worktree.
	if err := os.WriteFile(filepath.Join(wt.Path, "shared.txt"), []byte("worker changed"), 0644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}

	// Main repo keeps its own copy and modifies it, then copies into worktree.
	if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("main changed"), 0644); err != nil {
		t.Fatalf("write main repo file: %v", err)
	}

	ctx := copyCallerCtx(t, agentID)
	resp, err := a.handleWorktreeCopy(ctx, gen.ProjectWorktreeCopyReq{
		WorktreeID: wt.ID,
		Sources:    []string{"shared.txt"},
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if len(resp.Copied) != 0 {
		t.Fatalf("expected 0 copied, got %+v", resp.Copied)
	}
	if len(resp.Skipped) != 1 {
		t.Fatalf("expected 1 skipped, got %+v", resp.Skipped)
	}
	if !strings.Contains(resp.Skipped[0].Reason, "Force=true") {
		t.Fatalf("expected skip reason to mention Force=true, got %q", resp.Skipped[0].Reason)
	}

	got, err := os.ReadFile(filepath.Join(wt.Path, "shared.txt"))
	if err != nil {
		t.Fatalf("read worktree file: %v", err)
	}
	if string(got) != "worker changed" {
		t.Fatalf("worktree file was overwritten without force: %q", got)
	}
}

func TestHandleWorktreeCopy_ForceOverwritesModifiedDestination(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	writeFileIn(t, dir, "shared.txt", "main repo version")
	gitCommitIn(t, dir, "add shared.txt", "shared.txt")

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "force-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentID := "019fa66666000000000000000000000a"
	if err := a.bindWorktree(bindKey(t, agentID), wt.ID); err != nil {
		t.Fatalf("bind worktree: %v", err)
	}

	if err := os.WriteFile(filepath.Join(wt.Path, "shared.txt"), []byte("worker changed"), 0644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("main changed"), 0644); err != nil {
		t.Fatalf("write main repo file: %v", err)
	}

	ctx := copyCallerCtx(t, agentID)
	resp, err := a.handleWorktreeCopy(ctx, gen.ProjectWorktreeCopyReq{
		WorktreeID: wt.ID,
		Sources:    []string{"shared.txt"},
		Force:      true,
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if len(resp.Copied) != 1 || !resp.Copied[0].Overwritten {
		t.Fatalf("expected 1 overwritten copy, got %+v", resp.Copied)
	}
	if len(resp.Skipped) != 0 {
		t.Fatalf("expected 0 skipped, got %+v", resp.Skipped)
	}

	got, err := os.ReadFile(filepath.Join(wt.Path, "shared.txt"))
	if err != nil {
		t.Fatalf("read worktree file: %v", err)
	}
	if string(got) != "main changed" {
		t.Fatalf("worktree file not overwritten: %q", got)
	}
}

func TestHandleWorktreeCopy_DestDir(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "destdir-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentID := "019fa66666000000000000000000000b"
	if err := a.bindWorktree(bindKey(t, agentID), wt.ID); err != nil {
		t.Fatalf("bind worktree: %v", err)
	}

	writeFileIn(t, dir, "seed.txt", "seed")

	ctx := copyCallerCtx(t, agentID)
	resp, err := a.handleWorktreeCopy(ctx, gen.ProjectWorktreeCopyReq{
		WorktreeID: wt.ID,
		Sources:    []string{"seed.txt"},
		DestDir:    "backup",
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if len(resp.Copied) != 1 || resp.Copied[0].Dest != filepath.Join("backup", "seed.txt") {
		t.Fatalf("expected dest backup/seed.txt, got %+v", resp.Copied)
	}

	got, err := os.ReadFile(filepath.Join(wt.Path, "backup", "seed.txt"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != "seed" {
		t.Fatalf("copied content = %q, want %q", got, "seed")
	}
}

func TestHandleWorktreeCopy_SkipsDependencyTreeSource(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "skip-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentID := "019fa66666000000000000000000000c"
	if err := a.bindWorktree(bindKey(t, agentID), wt.ID); err != nil {
		t.Fatalf("bind worktree: %v", err)
	}

	writeFileIn(t, dir, "node_modules/pkg/index.js", "module")

	ctx := copyCallerCtx(t, agentID)
	resp, err := a.handleWorktreeCopy(ctx, gen.ProjectWorktreeCopyReq{
		WorktreeID: wt.ID,
		Sources:    []string{"node_modules/pkg/index.js"},
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if len(resp.Copied) != 0 || len(resp.Skipped) != 1 {
		t.Fatalf("expected 1 skipped, got copied=%+v skipped=%+v", resp.Copied, resp.Skipped)
	}
}

func TestTruncatePathList(t *testing.T) {
	paths := []string{
		"pkg/actor/project/worktree.go",
		"pkg/actor/project/worktree_lifecycle.go",
		"pkg/actor/project/worktree_copy.go",
	}
	got := truncatePathList(paths, 512)
	if len(got) > 512+len("...(truncated)") {
		t.Fatalf("truncated string too long: %d", len(got))
	}

	long := make([]string, 100)
	for i := range long {
		long[i] = "pkg/very/long/path/component/file_" + string(rune('a'+i%26)) + ".go"
	}
	got = truncatePathList(long, 512)
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}
