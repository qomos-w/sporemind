package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/util"
)

// gitRun executes git in dir and returns combined output.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	util.HideWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

func gitRepoActor(t *testing.T) (*Actor, string) {
	t.Helper()
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := freshActor(t)
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: dir, ActorID: genID()}}
	return a, dir
}

func freshTestCtx(t *testing.T) actor.PureContext {
	t.Helper()
	_, ctx := freshActor(t)
	return ctx
}

func firstMessage(commits []domain.GitCommitInfo) string {
	if len(commits) == 0 {
		return ""
	}
	return strings.TrimSpace(commits[0].Message)
}

// TestGitStatus_UnstagedModifiedNotStaged is a real-git regression for the
// round-6 external TOTP run: a tracked file modified WITHOUT staging
// produces a porcelain record " M <path>" whose first byte is a legitimate
// space. TrimSpace on the -z output used to eat it, shifting the XY columns
// so the file was reported as staged (and its path lost the first byte).
func TestGitStatus_UnstagedModifiedNotStaged(t *testing.T) {
	a, dir := gitRepoActor(t)
	f := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(f, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "tracked.txt")
	gitRun(t, dir, "commit", "-m", "seed")
	if err := os.WriteFile(f, []byte("v2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, err := a.handleGitStatus(freshTestCtx(t), domain.WorkspaceGitStatusReq{ProjectID: "p1"})
	if err != nil {
		t.Fatalf("handleGitStatus: %v", err)
	}
	if resp.IsClean {
		t.Fatalf("repo reported clean despite modified tracked.txt")
	}
	var st *domain.GitFileStatus
	for i := range resp.Files {
		if resp.Files[i].Path == "tracked.txt" {
			st = &resp.Files[i]
		}
	}
	if st == nil {
		t.Fatalf("tracked.txt not in Files: %+v", resp.Files)
	}
	if st.Staging != " " || st.Worktree != "M" {
		t.Errorf("status codes = staging %q worktree %q, want staging ' ' worktree 'M'", st.Staging, st.Worktree)
	}
}

func TestGitShow_Structured(t *testing.T) {
	a, dir := gitRepoActor(t)
	f := filepath.Join(dir, "src.go")
	if err := os.WriteFile(f, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "src.go")
	gitRun(t, dir, "commit", "-m", "introduce src.go")

	resp, err := a.handleGitShow(freshTestCtx(t), domain.WorkspaceGitShowReq{ProjectID: "p1", Ref: "HEAD"})
	if err != nil {
		t.Fatalf("handleGitShow: %v", err)
	}
	if resp.Commit.Message != "introduce src.go" {
		t.Errorf("Commit.Message = %q, want %q", resp.Commit.Message, "introduce src.go")
	}
	if resp.Commit.Hash == "" || resp.Commit.Short == "" {
		t.Errorf("commit hash/short missing: %+v", resp.Commit)
	}
	if len(resp.Files) != 1 || resp.Files[0].Path != "src.go" || resp.Files[0].Status != "added" {
		t.Errorf("Files = %+v, want [{Path:src.go Status:added}]", resp.Files)
	}
}

func TestGitShow_Rename(t *testing.T) {
	a, dir := gitRepoActor(t)
	oldPath := filepath.Join(dir, "old.go")
	newPath := filepath.Join(dir, "new.go")
	if err := os.WriteFile(oldPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "old.go")
	gitRun(t, dir, "commit", "-m", "add old")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "rename")

	resp, err := a.handleGitShow(freshTestCtx(t), domain.WorkspaceGitShowReq{ProjectID: "p1", Ref: "HEAD"})
	if err != nil {
		t.Fatalf("handleGitShow: %v", err)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("Files = %+v", resp.Files)
	}
	if resp.Files[0].Path != "new.go" || resp.Files[0].Status != "renamed" || resp.Files[0].OldPath != "old.go" {
		t.Errorf("rename file = %+v", resp.Files[0])
	}
}

func TestGitShow_CJKPath(t *testing.T) {
	a, dir := gitRepoActor(t)
	cjk := "中文文件.txt"
	f := filepath.Join(dir, cjk)
	if err := os.WriteFile(f, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", cjk)
	gitRun(t, dir, "commit", "-m", "add cjk file")

	resp, err := a.handleGitShow(freshTestCtx(t), domain.WorkspaceGitShowReq{ProjectID: "p1", Ref: "HEAD"})
	if err != nil {
		t.Fatalf("handleGitShow: %v", err)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("Files = %+v", resp.Files)
	}
	got := resp.Files[0].Path
	if got != cjk {
		t.Errorf("CJK path = %q, want %q", got, cjk)
	}
	if strings.Contains(got, `"`) || strings.Contains(got, "\\") {
		t.Errorf("CJK path contains quote or octal escape: %q", got)
	}
}

func TestGitDiscard(t *testing.T) {
	a, dir := gitRepoActor(t)
	f := filepath.Join(dir, "dirty.txt")
	if err := os.WriteFile(f, []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "dirty.txt")
	gitRun(t, dir, "commit", "-m", "add dirty")
	if err := os.WriteFile(f, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := a.handleGitDiscard(freshTestCtx(t), domain.WorkspaceGitDiscardReq{ProjectID: "p1", Paths: []string{"dirty.txt"}}); err != nil {
		t.Fatalf("handleGitDiscard: %v", err)
	}

	got, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "dirty" {
		t.Errorf("after discard got %q, want %q", got, "dirty")
	}
}

func TestGitAmend(t *testing.T) {
	a, dir := gitRepoActor(t)
	gitRun(t, dir, "commit", "--allow-empty", "-m", "old")

	if err := a.handleGitAmend(freshTestCtx(t), domain.WorkspaceGitAmendReq{ProjectID: "p1", Message: "new"}); err != nil {
		t.Fatalf("handleGitAmend: %v", err)
	}

	log, err := a.handleGitLog(freshTestCtx(t), domain.WorkspaceGitLogReq{ProjectID: "p1", Limit: 1})
	if err != nil {
		t.Fatalf("git_log: %v", err)
	}
	if len(log.Commits) != 1 || log.Commits[0].Message != "new" {
		t.Errorf("amended message = %q, want %q; commits=%+v", firstMessage(log.Commits), "new", log.Commits)
	}
}

func TestGitAmend_NoEdit(t *testing.T) {
	a, dir := gitRepoActor(t)
	gitRun(t, dir, "commit", "--allow-empty", "-m", "keep-me")

	if err := a.handleGitAmend(freshTestCtx(t), domain.WorkspaceGitAmendReq{ProjectID: "p1", NoEdit: true}); err != nil {
		t.Fatalf("handleGitAmend no-edit: %v", err)
	}

	log, err := a.handleGitLog(freshTestCtx(t), domain.WorkspaceGitLogReq{ProjectID: "p1", Limit: 1})
	if err != nil {
		t.Fatalf("git_log: %v", err)
	}
	if firstMessage(log.Commits) != "keep-me" {
		t.Errorf("message after no-edit amend = %q, want %q", firstMessage(log.Commits), "keep-me")
	}
}

func TestGitTagCreateAndListAndDelete(t *testing.T) {
	a, dir := gitRepoActor(t)
	gitRun(t, dir, "commit", "--allow-empty", "-m", "tag-target")

	if err := a.handleGitTagCreate(freshTestCtx(t), domain.WorkspaceGitTagCreateReq{ProjectID: "p1", Name: "v1", Message: "release v1"}); err != nil {
		t.Fatalf("handleGitTagCreate: %v", err)
	}

	list, err := a.handleGitTagList(freshTestCtx(t), domain.WorkspaceGitTagListReq{ProjectID: "p1"})
	if err != nil {
		t.Fatalf("handleGitTagList: %v", err)
	}
	if len(list.Tags) != 1 || list.Tags[0].Name != "v1" {
		t.Errorf("tags = %+v, want one v1", list.Tags)
	}

	if err := a.handleGitTagDelete(freshTestCtx(t), domain.WorkspaceGitTagDeleteReq{ProjectID: "p1", Name: "v1"}); err != nil {
		t.Fatalf("handleGitTagDelete: %v", err)
	}

	list2, err := a.handleGitTagList(freshTestCtx(t), domain.WorkspaceGitTagListReq{ProjectID: "p1"})
	if err != nil {
		t.Fatalf("handleGitTagList after delete: %v", err)
	}
	if len(list2.Tags) != 0 {
		t.Errorf("expected no tags after delete, got %+v", list2.Tags)
	}
}

func TestGitFetch(t *testing.T) {
	a, dir := gitRepoActor(t)

	origin := t.TempDir()
	gitRun(t, origin, "init", "--bare", "-q")
	gitRun(t, dir, "remote", "add", "origin", origin)

	f := filepath.Join(dir, "from-upstream.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "from-upstream.txt")
	gitRun(t, dir, "commit", "-m", "upstream")
	gitRun(t, dir, "push", "-u", "origin", "master")

	if err := a.handleGitFetch(freshTestCtx(t), domain.WorkspaceGitFetchReq{ProjectID: "p1", Remote: "origin"}); err != nil {
		t.Fatalf("handleGitFetch: %v", err)
	}
	show, err := a.handleGitShow(freshTestCtx(t), domain.WorkspaceGitShowReq{ProjectID: "p1", Ref: "origin/master"})
	if err != nil {
		t.Fatalf("show origin/master after fetch: %v", err)
	}
	if show.Commit.Message != "upstream" {
		t.Errorf("fetched origin/master missing upstream commit; got: %+v", show.Commit)
	}
}

func TestGitMerge_FastForward(t *testing.T) {
	a, dir := gitRepoActor(t)
	gitRun(t, dir, "checkout", "-b", "feature")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "feature")
	gitRun(t, dir, "checkout", "master")

	resp, err := a.handleGitMerge(freshTestCtx(t), domain.WorkspaceGitMergeReq{ProjectID: "p1", Branch: "feature"})
	if err != nil {
		t.Fatalf("handleGitMerge: %v", err)
	}
	if resp.Status != "merged" {
		t.Errorf("merge status = %q, want merged", resp.Status)
	}
	log, err := a.handleGitLog(freshTestCtx(t), domain.WorkspaceGitLogReq{ProjectID: "p1", Limit: 1})
	if err != nil {
		t.Fatalf("git_log: %v", err)
	}
	if firstMessage(log.Commits) != "feature" {
		t.Errorf("master head message = %q, want feature", firstMessage(log.Commits))
	}
}

func TestGitMerge_Conflict(t *testing.T) {
	a, dir := gitRepoActor(t)
	f := filepath.Join(dir, "shared.txt")
	if err := os.WriteFile(f, []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "shared.txt")
	gitRun(t, dir, "commit", "-m", "base")

	gitRun(t, dir, "checkout", "-b", "feature")
	if err := os.WriteFile(f, []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-am", "feature-change")

	gitRun(t, dir, "checkout", "master")
	if err := os.WriteFile(f, []byte("master\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-am", "master-change")

	resp, err := a.handleGitMerge(freshTestCtx(t), domain.WorkspaceGitMergeReq{ProjectID: "p1", Branch: "feature"})
	if err != nil {
		t.Fatalf("handleGitMerge conflict: %v", err)
	}
	if resp.Status != "conflict" {
		t.Errorf("merge status = %q, want conflict", resp.Status)
	}
	if len(resp.ConflictFiles) != 1 || resp.ConflictFiles[0] != "shared.txt" {
		t.Errorf("conflict files = %v, want [shared.txt]", resp.ConflictFiles)
	}
	if dirty := strings.TrimSpace(gitRun(t, dir, "status", "--porcelain")); dirty != "" {
		t.Errorf("repo still dirty after conflict abort: %s", dirty)
	}
}

func TestGitLog_BranchFilter(t *testing.T) {
	a, dir := gitRepoActor(t)
	gitRun(t, dir, "checkout", "-b", "feature")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "feature-only")
	gitRun(t, dir, "checkout", "master")

	featureLog, err := a.handleGitLog(freshTestCtx(t), domain.WorkspaceGitLogReq{ProjectID: "p1", Branch: "feature", Limit: 5})
	if err != nil {
		t.Fatalf("git_log feature: %v", err)
	}
	if !contains(commitMessages(featureLog.Commits), "feature-only") {
		t.Errorf("feature branch log missing feature-only; got %v", commitMessages(featureLog.Commits))
	}

	rootLog, err := a.handleGitLog(freshTestCtx(t), domain.WorkspaceGitLogReq{ProjectID: "p1", Limit: 5})
	if err != nil {
		t.Fatalf("git_log root: %v", err)
	}
	if contains(commitMessages(rootLog.Commits), "feature-only") {
		t.Errorf("root log should not contain unmerged feature commit; got %v", commitMessages(rootLog.Commits))
	}
}

func TestGitLog_AllRefs(t *testing.T) {
	a, dir := gitRepoActor(t)
	gitRun(t, dir, "checkout", "-b", "feature")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "feature-only")
	gitRun(t, dir, "checkout", "master")

	allLog, err := a.handleGitLog(freshTestCtx(t), domain.WorkspaceGitLogReq{ProjectID: "p1", All: true, Limit: 10})
	if err != nil {
		t.Fatalf("git_log all: %v", err)
	}
	if !contains(commitMessages(allLog.Commits), "feature-only") {
		t.Errorf("--all log missing unmerged feature-only; got %v", commitMessages(allLog.Commits))
	}
}

func TestGitDiff_BaseCommitHashAndFilePath(t *testing.T) {
	a, dir := gitRepoActor(t)
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("a1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "a.txt")
	gitRun(t, dir, "commit", "-m", "c1")
	baseHash := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
	if err := os.WriteFile(f, []byte("a2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-am", "c2")
	headHash := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))

	resp, err := a.handleGitDiff(freshTestCtx(t), domain.WorkspaceGitDiffReq{
		ProjectID:      "p1",
		BaseCommitHash: baseHash,
		CommitHash:     headHash,
		FilePath:       "a.txt",
	})
	if err != nil {
		t.Fatalf("handleGitDiff base..head file: %v", err)
	}
	joined := strings.Join(resp.Diff, "\n")
	if !strings.Contains(joined, "a1") || !strings.Contains(joined, "a2") {
		t.Errorf("diff missing a1/a2; got:\n%s", joined)
	}
}

func TestGitDiff_CommitHashVsWorkingTree(t *testing.T) {
	a, dir := gitRepoActor(t)
	f := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(f, []byte("committed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "b.txt")
	gitRun(t, dir, "commit", "-m", "add b")
	headHash := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
	if err := os.WriteFile(f, []byte("working\n"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, err := a.handleGitDiff(freshTestCtx(t), domain.WorkspaceGitDiffReq{ProjectID: "p1", CommitHash: headHash, FilePath: "b.txt"})
	if err != nil {
		t.Fatalf("handleGitDiff commit vs working tree: %v", err)
	}
	joined := strings.Join(resp.Diff, "\n")
	if !strings.Contains(joined, "committed") || !strings.Contains(joined, "working") {
		t.Errorf("diff missing committed/working; got:\n%s", joined)
	}
}

func TestGitBranch_IncludesRemoteTrackingBranches(t *testing.T) {
	a, dir := gitRepoActor(t)

	origin := t.TempDir()
	gitRun(t, origin, "init", "--bare", "-q")
	gitRun(t, dir, "remote", "add", "origin", origin)

	gitRun(t, dir, "checkout", "-b", "feature")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "feature")
	gitRun(t, dir, "push", "-u", "origin", "feature")
	gitRun(t, dir, "checkout", "master")

	branches, err := a.handleGitBranch(freshTestCtx(t), domain.WorkspaceGitBranchReq{ProjectID: "p1"})
	if err != nil {
		t.Fatalf("handleGitBranch: %v", err)
	}

	var local, remote, master *domain.GitBranchInfo
	for i := range branches.Branches {
		b := &branches.Branches[i]
		if b.Name == "feature" && !b.IsRemote {
			local = b
		}
		if b.Name == "origin/feature" && b.IsRemote {
			remote = b
		}
		if b.Name == "master" && !b.IsRemote {
			master = b
		}
	}
	if local == nil {
		t.Errorf("local feature branch missing; branches=%+v", branches.Branches)
	}
	if master == nil {
		t.Errorf("master branch missing; branches=%+v", branches.Branches)
	} else if !master.Current {
		t.Errorf("master should be current after checkout; got Current=%v", master.Current)
	}
	if remote == nil {
		t.Errorf("remote-tracking origin/feature missing; branches=%+v", branches.Branches)
	} else {
		if remote.Remote != "origin" {
			t.Errorf("remote.Remote = %q, want origin", remote.Remote)
		}
		if remote.Current {
			t.Errorf("remote-tracking branch should never be Current")
		}
	}
}
