package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// parseGitPorcelainZ must keep CJK paths verbatim; newline-mode porcelain
// double-quotes them, which rendered octal escapes in the git panel and
// dropdown.
func TestParseGitPorcelainZ_CJKAndRename(t *testing.T) {
	in := " M 中文文档.md\x00R  新名称.md\x00old-name.md\x00"
	files := parseGitPorcelainZ(in)
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: %+v", len(files), files)
	}
	if files[0].Path != "中文文档.md" || files[0].Staging != " " || files[0].Worktree != "M" {
		t.Errorf("file 0 = %+v, want path=中文文档.md staging=' ' worktree=M", files[0])
	}
	// The rename record's second bare field (original path) must be skipped.
	if files[1].Path != "新名称.md" || files[1].Staging != "R" {
		t.Errorf("rename file = %+v, want path=新名称.md staging=R", files[1])
	}
}

// TestGitStatus_CJKFilenames is a real-git regression for the git panel and
// dropdown: a CJK-named dirty file must surface with its verbatim path.
func TestGitStatus_CJKFilenames(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "中文文档.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshActor(t)
	projectID := genID()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: dir, ActorID: projectID}}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return nil, false
	}

	resp, err := a.handleGitStatus(ctx, domain.WorkspaceGitStatusReq{ProjectID: "p1"})
	if err != nil {
		t.Fatalf("git_status: %v", err)
	}
	if resp.IsClean {
		t.Fatal("expected dirty repo with 中文文档.md")
	}
	found := false
	for _, f := range resp.Files {
		if f.Path == "中文文档.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("中文文档.md not in Files (mangled?): %+v", resp.Files)
	}
}
