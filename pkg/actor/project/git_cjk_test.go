package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// parseGitPorcelainZ must keep non-ASCII (CJK) paths verbatim: newline-mode
// porcelain double-quotes them, which used to render octal escapes in the
// git panel and dropdown. Rename records carry the original path as a second
// bare NUL-terminated field.
func TestParseGitPorcelainZ_CJKAndRename(t *testing.T) {
	in := " M 中文文档.md\x00R  新名称.md\x00old-name.md\x00?? 未跟踪.txt\x00"
	records := parseGitPorcelainZ(in)
	if len(records) != 3 {
		t.Fatalf("got %d records, want 3: %+v", len(records), records)
	}
	if records[0].path != "中文文档.md" || records[0].staging != " " || records[0].worktree != "M" {
		t.Errorf("record 0 = %+v, want path=中文文档.md staging=' ' worktree=M", records[0])
	}
	if records[1].path != "新名称.md" || records[1].oldPath != "old-name.md" {
		t.Errorf("rename record = %+v, want path=新名称.md oldPath=old-name.md", records[1])
	}
	if records[2].path != "未跟踪.txt" || records[2].staging != "?" {
		t.Errorf("untracked record = %+v", records[2])
	}
	if strings.Contains(records[0].path, "\\") || strings.Contains(records[0].path, "\"") {
		t.Errorf("CJK path carried quoting/escapes: %q", records[0].path)
	}
}

func TestGitShortStatus_RenameFormat(t *testing.T) {
	records := []porcelainRecord{
		{staging: " ", worktree: "M", path: "中文文档.md"},
		{staging: "R", worktree: " ", path: "新名称.md", oldPath: "old-name.md"},
	}
	got := gitShortStatus(records)
	want := " M 中文文档.md\nR  old-name.md -> 新名称.md"
	if got != want {
		t.Errorf("gitShortStatus = %q, want %q", got, want)
	}
}

// TestGitStatus_CJKFilenames is a real-git regression: the status handler
// must list a CJK-named dirty file with its verbatim path (no quotes, no
// octal escapes) in both Files and the display Status string.
func TestGitStatus_CJKFilenames(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "中文文档.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, ctx := worktreeTestActor(t, dir)
	resp, err := a.handleGitStatus(ctx, domain.ProjectGitStatusReq{})
	if err != nil {
		t.Fatalf("git_status: %v", err)
	}
	found := false
	for _, f := range resp.Files {
		if f.Path == "中文文档.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("中文文档.md not in Files: %+v", resp.Files)
	}
	if !strings.Contains(resp.Status, "中文文档.md") || strings.Contains(resp.Status, "\\344") {
		t.Errorf("Status mangled CJK path: %q", resp.Status)
	}
}
// TestGitStatus_UnstagedModifiedNotStaged is a real-git regression for the
// round-6 external TOTP run: a tracked file modified WITHOUT staging
// produces a porcelain record " M <path>" whose first byte is a legitimate
// space. TrimSpace on the -z output used to eat it, shifting the XY columns
// so the file was reported as staged (and its path lost the first byte).
func TestGitStatus_UnstagedModifiedNotStaged(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	seed := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(seed, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "seed")
	if err := os.WriteFile(seed, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, ctx := worktreeTestActor(t, dir)
	resp, err := a.handleGitStatus(ctx, domain.ProjectGitStatusReq{})
	if err != nil {
		t.Fatalf("git_status: %v", err)
	}
	var f *domain.ProjectGitFileStatus
	for i := range resp.Files {
		if resp.Files[i].Path == "tracked.txt" {
			f = &resp.Files[i]
		}
	}
	if f == nil {
		t.Fatalf("tracked.txt not in Files: %+v", resp.Files)
	}
	if f.Staging != " " || f.Worktree != "M" {
		t.Errorf("status codes = staging %q worktree %q, want staging ' ' worktree 'M'", f.Staging, f.Worktree)
	}
	if !strings.Contains(resp.Status, " M tracked.txt") {
		t.Errorf("Status display mangled: %q", resp.Status)
	}
}

// TestGitDiffAtRoot_CJKFilenames covers the working-tree diff path for
// CJK-named files: the file must be found via -z status parsing and the
// synthetic diff header must carry the verbatim path.
func TestGitDiffAtRoot_CJKFilenames(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	seed := filepath.Join(dir, "中文文档.md")
	if err := os.WriteFile(seed, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "seed")
	if err := os.WriteFile(seed, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// handleGitDiffAtRoot reads only the repo on disk, not actor state.
	a := &Actor{}
	resp, err := a.handleGitDiffAtRoot(nil, dir, domain.ProjectGitDiffReq{})
	if err != nil {
		t.Fatalf("git_diff: %v", err)
	}
	joined := strings.Join(resp.Diff, "\n")
	if !strings.Contains(joined, "diff --git a/中文文档.md b/中文文档.md") {
		t.Errorf("diff missing verbatim CJK header; diff=%q", joined)
	}
	if strings.Contains(joined, "\\344") {
		t.Errorf("diff header carries octal escapes; diff=%q", joined)
	}
}

// The commit-hash diff path must render CJK diff headers literally
// (quotepath=false) instead of octal-escaping them.
func TestGitDiffAtRoot_CommitHashCJKHeader(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "中文文档.md"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "seed")
	// `git diff <hash>` compares the commit to the working tree, so dirty
	// the file afterwards to produce a non-empty diff.
	if err := os.WriteFile(filepath.Join(dir, "中文文档.md"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	a := &Actor{}
	resp, err := a.handleGitDiffAtRoot(nil, dir, domain.ProjectGitDiffReq{CommitHash: hash})
	if err != nil {
		t.Fatalf("git_diff(commit): %v", err)
	}
	joined := strings.Join(resp.Diff, "\n")
	if !strings.Contains(joined, "中文文档.md") {
		t.Errorf("commit diff missing CJK path: %q", joined)
	}
	if strings.Contains(joined, "\\344") {
		t.Errorf("commit diff header carries octal escapes: %q", joined)
	}
}
