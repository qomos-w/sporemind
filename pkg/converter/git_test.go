package converter

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestGitDiffConverter(t *testing.T) {
	lines := []string{
		"diff --git a/foo.go b/foo.go",
		"--- a/foo.go",
		"+++ b/foo.go",
		"@@ -1,3 +1,3 @@",
		"-old",
		"+new",
	}
	resp := domain.ProjectGitDiffResp{Diff: lines}
	out, err := projectGitDiffConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "diff --git a/foo.go b/foo.go") {
		t.Errorf("expected diff header in output, got:\n%s", out)
	}
	if !strings.Contains(out, "-old") || !strings.Contains(out, "+new") {
		t.Errorf("expected -old/+new in output, got:\n%s", out)
	}
	// must be plain text, not a JSON array
	if strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("expected plain text, got JSON array:\n%s", out)
	}
}

func TestGitDiffConverterEmpty(t *testing.T) {
	resp := domain.WorkspaceGitDiffResp{Diff: nil}
	out, err := workspaceGitDiffConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "(no changes)" {
		t.Errorf("expected '(no changes)', got %q", out)
	}
}

func TestProjectGitStatusConverter(t *testing.T) {
	resp := domain.ProjectGitStatusResp{
		Branch: "main",
		IsGit:  true,
		Files: []domain.ProjectGitFileStatus{
			{Path: "b.go", Staging: " ", Worktree: "M"},
			{Path: "a.go", Staging: "A", Worktree: " "},
			{Path: "c.go", Staging: "?", Worktree: "?"},
		},
	}
	out, err := projectGitStatusConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "On branch main") {
		t.Errorf("expected 'On branch main', got:\n%s", out)
	}
	// output sorted by path: a.go before b.go
	if strings.Index(out, "a.go") > strings.Index(out, "b.go") {
		t.Errorf("expected a.go before b.go, got:\n%s", out)
	}
	if !strings.Contains(out, "untracked") {
		t.Errorf("expected 'untracked' label, got:\n%s", out)
	}
	if !strings.Contains(out, "added, staged") {
		t.Errorf("expected 'added, staged' label, got:\n%s", out)
	}
	if !strings.Contains(out, "(3 files changed)") {
		t.Errorf("expected summary, got:\n%s", out)
	}
	// redundant fields must NOT leak into the LLM output
	if strings.Contains(out, "MainBranch") || strings.Contains(out, "UserName") {
		t.Errorf("redundant field leaked into output, got:\n%s", out)
	}
}

func TestGitStatusConverterClean(t *testing.T) {
	resp := domain.WorkspaceGitStatusResp{
		Branch:  "dev",
		IsGit:   true,
		IsClean: true,
		Files:   nil,
	}
	out, err := workspaceGitStatusConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "(no uncommitted changes)") {
		t.Errorf("expected clean marker, got:\n%s", out)
	}
}

func TestGitStatusConverterNotGit(t *testing.T) {
	resp := domain.ProjectGitStatusResp{IsGit: false}
	out, err := projectGitStatusConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "(not a git repository)" {
		t.Errorf("expected not-git marker, got %q", out)
	}
}

func TestGitFileLabel(t *testing.T) {
	cases := []struct {
		staging, worktree, want string
	}{
		{"?", "?", "untracked"},
		{" ", "M", "modified, unstaged"},
		{"M", " ", "modified, staged"},
		{"M", "M", "modified, staged; modified, unstaged"},
		{"A", " ", "added, staged"},
		{" ", "D", "deleted, unstaged"},
		{"R", " ", "renamed, staged"},
		{" ", " ", "modified"},
	}
	for _, c := range cases {
		got := gitFileLabel(c.staging, c.worktree)
		if got != c.want {
			t.Errorf("gitFileLabel(%q,%q) = %q, want %q", c.staging, c.worktree, got, c.want)
		}
	}
}
