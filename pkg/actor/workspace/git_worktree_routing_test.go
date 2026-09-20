package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

// ── resolveGitRoot pure function tests ──

func TestResolveGitRoot_EmptyWorktreeUsesProjectRoot(t *testing.T) {
	projectCalled := false
	r := gitRootResolver{
		projectRoot: func(pid string) (string, error) {
			projectCalled = true
			if pid != "p1" {
				t.Errorf("projectRoot pid=%q, want p1", pid)
			}
			return "/proj", nil
		},
		worktreeRoot: func(pid, wid string) (string, error) {
			t.Fatal("worktreeRoot must not be called")
			return "", nil
		},
	}
	got, err := resolveGitRoot("p1", "", r)
	if err != nil {
		t.Fatalf("resolveGitRoot: %v", err)
	}
	if got != "/proj" {
		t.Errorf("got %q, want /proj", got)
	}
	if !projectCalled {
		t.Error("projectRoot was not called")
	}
}

func TestResolveGitRoot_WorktreeRoutesToWorktreePath(t *testing.T) {
	var gotPID, gotWID string
	r := gitRootResolver{
		projectRoot: func(pid string) (string, error) { return "/proj", nil },
		worktreeRoot: func(pid, wid string) (string, error) {
			gotPID, gotWID = pid, wid
			return "/wt", nil
		},
	}
	got, err := resolveGitRoot("p1", "wt-1", r)
	if err != nil {
		t.Fatalf("resolveGitRoot: %v", err)
	}
	if got != "/wt" {
		t.Errorf("got %q, want /wt", got)
	}
	if gotPID != "p1" || gotWID != "wt-1" {
		t.Errorf("worktreeRoot called with pid=%q wid=%q, want p1/wt-1", gotPID, gotWID)
	}
}

func TestResolveGitRoot_WorktreeErrorPropagates(t *testing.T) {
	r := gitRootResolver{
		projectRoot: func(pid string) (string, error) { return "/proj", nil },
		worktreeRoot: func(pid, wid string) (string, error) {
			return "", errors.New("worktree not found")
		},
	}
	_, err := resolveGitRoot("p1", "wt-1", r)
	if err == nil || !strings.Contains(err.Error(), "worktree not found") {
		t.Fatalf("expected 'worktree not found' error, got: %v", err)
	}
}

// ── worktreeRootFor with FakeRef ──

func TestWorktreeRootFor_ActiveWorktree(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := genID()
	wtPath := filepath.Join(t.TempDir(), "wt")
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == projectID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "project.worktree_get" {
					return gen.ProjectWorktree{ID: "wt-1", Path: wtPath, Branch: "wt-branch", Status: "active"}
				}
				return nil
			}), true
		}
		return nil, false
	}
	got, err := a.worktreeRootFor(ctx, projectID, "wt-1")
	if err != nil {
		t.Fatalf("worktreeRootFor: %v", err)
	}
	if got != wtPath {
		t.Errorf("got %q, want %q", got, wtPath)
	}
}

func TestWorktreeRootFor_InactiveWorktreeRejected(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := genID()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == projectID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				return gen.ProjectWorktree{ID: "wt-1", Path: "/tmp", Status: "stale"}
			}), true
		}
		return nil, false
	}
	_, err := a.worktreeRootFor(ctx, projectID, "wt-1")
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("expected 'not active' error, got: %v", err)
	}
}

// ── Handler-level routing with a real git repo + linked worktree ──

func TestWtGitStatus_RoutesToWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	wtPath := filepath.Join(t.TempDir(), "wt")
	wa := exec.Command("git", "worktree", "add", "-b", "wt-branch", wtPath)
	util.HideWindow(wa)
	wa.Dir = dir
	if o, e := wa.CombinedOutput(); e != nil {
		t.Fatalf("worktree add: %v\n%s", e, o)
	}

	a, ctx := freshActor(t)
	projectID := genID()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: dir, ActorID: projectID}}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == projectID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "project.worktree_get" {
					return gen.ProjectWorktree{ID: "wt-1", Path: wtPath, Branch: "wt-branch", Status: "active"}
				}
				return nil
			}), true
		}
		return nil, false
	}

	// Dirty the worktree
	if err := os.WriteFile(filepath.Join(wtPath, "wt-file.txt"), []byte("wt"), 0644); err != nil {
		t.Fatal(err)
	}
	// Dirty the main root
	if err := os.WriteFile(filepath.Join(dir, "root-file.txt"), []byte("root"), 0644); err != nil {
		t.Fatal(err)
	}

	// Worktree scope: should see wt-branch and the wt dirty file
	wtResp, err := a.handleGitStatus(ctx, gen.WorkspaceGitStatusReq{ProjectID: "p1", WorktreeID: "wt-1"})
	if err != nil {
		t.Fatalf("git_status (worktree): %v", err)
	}
	if !wtResp.IsGit {
		t.Fatal("worktree IsGit=false")
	}
	if wtResp.Branch != "wt-branch" {
		t.Errorf("worktree branch=%q, want wt-branch", wtResp.Branch)
	}
	if wtResp.IsClean {
		t.Error("worktree is clean, expected dirty (wt-file.txt)")
	}
	found := false
	for _, f := range wtResp.Files {
		if strings.HasSuffix(f.Path, "wt-file.txt") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("wt-file.txt not in worktree files: %+v", wtResp.Files)
	}

	// Project root scope: should see master and only the root dirty file
	rootResp, err := a.handleGitStatus(ctx, gen.WorkspaceGitStatusReq{ProjectID: "p1"})
	if err != nil {
		t.Fatalf("git_status (root): %v", err)
	}
	if !rootResp.IsGit {
		t.Fatal("root IsGit=false")
	}
	if rootResp.Branch == "wt-branch" {
		t.Errorf("root branch=%q, should not be the worktree branch", rootResp.Branch)
	}
	for _, f := range rootResp.Files {
		if strings.HasSuffix(f.Path, "wt-file.txt") {
			t.Errorf("root status LEAKED wt-file.txt: %+v", f)
		}
	}
}

func TestWtGitLog_RoutesToWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	wtPath := filepath.Join(t.TempDir(), "wt")
	wa := exec.Command("git", "worktree", "add", "-b", "wt-branch", wtPath)
	util.HideWindow(wa)
	wa.Dir = dir
	if o, e := wa.CombinedOutput(); e != nil {
		t.Fatalf("worktree add: %v\n%s", e, o)
	}

	// Commit on the worktree branch
	wtCommit := exec.Command("git", "commit", "--allow-empty", "-m", "worktree-commit")
	util.HideWindow(wtCommit)
	wtCommit.Dir = wtPath
	if o, e := wtCommit.CombinedOutput(); e != nil {
		t.Fatalf("wt commit: %v\n%s", e, o)
	}

	a, ctx := freshActor(t)
	projectID := genID()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: dir, ActorID: projectID}}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == projectID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "project.worktree_get" {
					return gen.ProjectWorktree{ID: "wt-1", Path: wtPath, Branch: "wt-branch", Status: "active"}
				}
				return nil
			}), true
		}
		return nil, false
	}

	wtLog, err := a.handleGitLog(ctx, gen.WorkspaceGitLogReq{ProjectID: "p1", WorktreeID: "wt-1", Limit: 5})
	if err != nil {
		t.Fatalf("git_log (worktree): %v", err)
	}
	rootLog, err := a.handleGitLog(ctx, gen.WorkspaceGitLogReq{ProjectID: "p1", Limit: 5})
	if err != nil {
		t.Fatalf("git_log (root): %v", err)
	}

	wtMsgs := commitMessages(wtLog.Commits)
	rootMsgs := commitMessages(rootLog.Commits)

	if !contains(wtMsgs, "worktree-commit") {
		t.Errorf("worktree log missing worktree-commit; got %v", wtMsgs)
	}
	if contains(rootMsgs, "worktree-commit") {
		t.Errorf("root log LEAKED worktree-commit; got %v", rootMsgs)
	}
}

func TestWtGitBranch_RoutesToWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	wtPath := filepath.Join(t.TempDir(), "wt")
	wa := exec.Command("git", "worktree", "add", "-b", "wt-branch", wtPath)
	util.HideWindow(wa)
	wa.Dir = dir
	if o, e := wa.CombinedOutput(); e != nil {
		t.Fatalf("worktree add: %v\n%s", e, o)
	}

	a, ctx := freshActor(t)
	projectID := genID()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: dir, ActorID: projectID}}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == projectID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "project.worktree_get" {
					return gen.ProjectWorktree{ID: "wt-1", Path: wtPath, Branch: "wt-branch", Status: "active"}
				}
				return nil
			}), true
		}
		return nil, false
	}

	wtBranches, err := a.handleGitBranch(ctx, gen.WorkspaceGitBranchReq{ProjectID: "p1", WorktreeID: "wt-1"})
	if err != nil {
		t.Fatalf("git_branch (worktree): %v", err)
	}
	rootBranches, err := a.handleGitBranch(ctx, gen.WorkspaceGitBranchReq{ProjectID: "p1"})
	if err != nil {
		t.Fatalf("git_branch (root): %v", err)
	}

	// Worktree scope: current branch is wt-branch
	wtCur := findCurrent(wtBranches.Branches)
	if wtCur != "wt-branch" {
		t.Errorf("worktree current branch=%q, want wt-branch; all=%+v", wtCur, wtBranches.Branches)
	}
	// Root scope: current branch is master (the default)
	rootCur := findCurrent(rootBranches.Branches)
	if rootCur != "master" {
		t.Errorf("root current branch=%q, want master; all=%+v", rootCur, rootBranches.Branches)
	}
}

// ── helpers ──

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q")
	util.HideWindow(cmd)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	for _, kv := range []string{"user.name test", "user.email test@test"} {
		p := strings.SplitN(kv, " ", 2)
		c := exec.Command("git", "config", p[0], p[1])
		util.HideWindow(c)
		c.Dir = dir
		if o, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git config %s: %v\n%s", p[0], e, o)
		}
	}
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# test"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "initial"}} {
		c := exec.Command("git", args...)
		util.HideWindow(c)
		c.Dir = dir
		if o, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %v\n%s", args, e, o)
		}
	}
}

func commitMessages(commits []domain.GitCommitInfo) []string {
	msgs := make([]string, len(commits))
	for i, c := range commits {
		msgs[i] = strings.TrimSpace(c.Message)
	}
	return msgs
}

func contains(msgs []string, target string) bool {
	for _, m := range msgs {
		if m == target {
			return true
		}
	}
	return false
}

func findCurrent(branches []domain.GitBranchInfo) string {
	for _, b := range branches {
		if b.Current {
			return b.Name
		}
	}
	return ""
}

// TestGitCLIWithTimeout_TimeoutSurfacesClearError verifies that when the hard
// git timeout fires, gitCLIWithTimeout returns an error that explicitly names
// the timeout instead of a bare exit error, mirroring the project package's
// gitRunWithTimeout behavior.
func TestGitCLIWithTimeout_TimeoutSurfacesClearError(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	_, err := gitCLIWithTimeout(1*time.Nanosecond, dir, "rev-parse", "--git-dir")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "timed out") {
		t.Fatalf("timeout error should explicitly mention 'timed out', got: %q", msg)
	}
	if !strings.Contains(msg, "1ns") {
		t.Fatalf("timeout error should mention the timeout duration, got: %q", msg)
	}
}