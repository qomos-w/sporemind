package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

type globCase struct {
	name    string
	pattern string
	absent  []string
	present []string
}

func runGlobCases(t *testing.T, a *Actor, ctx *testutil.FakeCtx, cases []globCase) {
	t.Helper()
	for _, tc := range cases {
		resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{Pattern: tc.pattern})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for _, sub := range tc.absent {
			if containsStr(resp.Files, sub) {
				t.Errorf("%s: expected %q filtered, got %v", tc.name, sub, resp.Files)
			}
		}
		for _, sub := range tc.present {
			if !containsStr(resp.Files, sub) {
				t.Errorf("%s: expected %q present, got %v", tc.name, sub, resp.Files)
			}
		}
	}
}

// TestFileSystemGlobRespectsGitignore pins the gitignore behavior of
// filesystem glob using the exact pattern shapes the composer @-mention
// sends: a bare search term (recursive substring fallback) and a slash
// query framed as **/*term*. Nested .gitignore files and anchored root
// patterns must filter exactly like the file browser listing does.
func TestFileSystemGlobRespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.MkdirAll(filepath.Join(dir, "dist"), 0755)
	os.WriteFile(filepath.Join(dir, "root.log"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "ignored.log"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "inner.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "ok.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "dist", "bundle.js"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.log\n/dist/\n"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", ".gitignore"), []byte("inner.txt\n"), 0644)

	a, ctx := freshProject(t, dir)

	runGlobCases(t, a, ctx, []globCase{
		{name: "extension glob", pattern: "**/*.log", absent: []string{"ignored.log"}, present: []string{"root.log"}},
		{name: "bare substring term", pattern: "ignored", absent: []string{"ignored.log"}},
		{name: "substring term glob-framed", pattern: "**/*ignored*", absent: []string{"ignored.log"}},
		{name: "nested gitignore", pattern: "**/*.txt", absent: []string{"inner.txt"}, present: []string{"ok.txt"}},
		{name: "anchored dir pattern", pattern: "**/*.js", absent: []string{"dist"}},
	})

	// NoIgnore bypasses every ignore layer.
	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{Pattern: "**/*.log", NoIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(resp.Files, "ignored.log") {
		t.Errorf("NoIgnore: expected ignored.log present, got %v", resp.Files)
	}
}

// TestFileSystemGlobOrderByModified verifies Order_by=-modified returns
// matches newest-mtime first with lexicographic ties, and that the default
// (unset) keeps lexicographic order.
func TestFileSystemGlobOrderByModified(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("x"), 0644)
	newest := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	middle := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	os.Chtimes(filepath.Join(dir, "c.txt"), newest, newest)
	os.Chtimes(filepath.Join(dir, "b.txt"), middle, middle)
	os.Chtimes(filepath.Join(dir, "a.txt"), older, older)

	a, ctx := freshProject(t, dir)

	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{Pattern: "**/*.txt", OrderBy: "-modified"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 3 {
		t.Fatalf("files=%v", resp.Files)
	}
	if !reflect.DeepEqual(resp.Files, []string{"c.txt", "b.txt", "a.txt"}) {
		t.Errorf("mtime order = %v, want [c.txt b.txt a.txt]", resp.Files)
	}

	plain, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{Pattern: "**/*.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain.Files, []string{"a.txt", "b.txt", "c.txt"}) {
		t.Errorf("default order = %v, want lexicographic", plain.Files)
	}
}

// TestFileSystemGlobRepoGitignoreShapes replays the sporemind repo's actual
// .gitignore (unanchored file/dir patterns, extension patterns, negation
// re-includes under a ** ignore, .sporecode runtime state) against glob to
// surface any shape the @-mention dropdown would leak.
func TestFileSystemGlobRepoGitignoreShapes(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{
		"bin", "web/node_modules/pkg", "web/dist", "pkg/web/dist", "data",
		"cmd/sporemind-desktop/web", "cmd/sporemind-desktop/build/windows",
		"cmd/app/out", ".sporecode/.actors/x", ".sporecode/data", ".sporecode/logs",
		".sporecode/wiki/.migration-backup", ".claude/worktrees/w1",
		"mobile/www/app", "src",
	} {
		os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0755)
	}
	files := map[string]string{
		"package-lock.json":                             "x",
		"web/package-lock.json":                         "x",
		"go.sum":                                        "x",
		"app.log":                                       "x",
		"bundle.zip":                                    "x",
		"tool.exe":                                      "x",
		"web/node_modules/pkg/index.js":                 "x",
		"web/dist/app.js":                               "x",
		"pkg/web/dist/.gitkeep":                         "x",
		"pkg/web/dist/README.md":                        "x",
		"pkg/web/dist/leak.js":                          "x",
		"data/state.json":                               "x",
		"cmd/sporemind-desktop/web/app.js":              "x",
		"cmd/sporemind-desktop/build/other.bin":         "x",
		"cmd/app/out/keep.yml":                          "x",
		"cmd/app/out/junk.bin":                          "x",
		".sporecode/jwt-secret":                         "x",
		".sporecode/registry.json":                      "x",
		".sporecode/.actors/x/state.json":               "x",
		".sporecode/logs/run.log":                       "x",
		".sporecode/wiki/.migration-backup/a.md":        "x",
		".claude/worktrees/w1/main.go":                  "x",
		".claude/settings.local.json":                   "x",
		"mobile/www/app/main.js":                        "x",
		"test-llm.yaml":                                 "x",
		".env":                                          "x",
		"src/main.go":                                   "x",
		".sporecode/wiki/card.md":                       "x",
		"cmd/sporemind-desktop/build/windows/info.json": "x",
	}
	for rel, content := range files {
		os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(content), 0644)
	}
	gitignore := `.env
.idea/
/bin/
/build/
**/build/snapshots/
/cmd/sporemind-desktop/web/
package-lock.json
bun.lock
go.sum
*.zip
*.log
windows.syso
resource_windows_amd64.syso
wails_windows_amd64.syso
bind_windows_amd64.go
go.work
go.work.sum
AUDIT.md
*.exe
web/node_modules/
web/dist/
cmd/sporemind-desktop/build/**
!cmd/sporemind-desktop/build/Taskfile.yml
!cmd/sporemind-desktop/build/config.yml
!cmd/sporemind-desktop/build/windows/
!cmd/sporemind-desktop/build/windows/Taskfile.yml
!cmd/sporemind-desktop/build/windows/info.json
!cmd/sporemind-desktop/build/windows/wails.exe.manifest
cmd/app/out/**
!cmd/app/out/keep.yml
node_modules/
dist/
pkg/web/dist/*
!pkg/web/dist/.gitkeep
!pkg/web/dist/README.md
.mystore/
web/tsconfig.tsbuildinfo
.sporecode/wiki/.migration-backup/
test-llm.yaml
.claude/worktrees/
.claude/settings.local.json
data/
mobile/www/app/
mobile/android/app/src/main/res/mipmap-*/ic_launcher*.png
/package.json
/package-lock.json
cmd/sporemind-desktop/versioninfo.json
.sporecode/jwt-secret
.sporecode/.actors/
.sporecode/data/
.sporecode/logs/
.sporecode/registry.json
`
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0644)

	a, ctx := freshProject(t, dir)

	runGlobCases(t, a, ctx, []globCase{
		{name: "ignored logs", pattern: "**/*.log", absent: []string{"app.log", ".sporecode/logs/run.log"}},
		{name: "unanchored lockfile at depth", pattern: "**/*package-lock*", absent: []string{"package-lock.json"}},
		{name: "ignored exe", pattern: "**/*.exe", absent: []string{"tool.exe"}},
		{name: "data dir", pattern: "state", absent: []string{"data"}},
		{name: "env secret", pattern: ".env", absent: []string{".env"}},
		{name: "registry", pattern: "registry", absent: []string{"registry.json"}},
		{name: "jwt secret", pattern: "jwt", absent: []string{"jwt-secret"}},
		{name: "actors state", pattern: "state.json", absent: []string{".actors"}},
		{name: "claude worktrees", pattern: "worktrees", absent: []string{"worktrees"}},
		{name: "migration backup", pattern: "migration", absent: []string{"migration-backup"}},
		{name: "tracked wiki card still visible", pattern: "card.md", present: []string{".sporecode/wiki/card.md"}},
		{name: "negation re-include under dir/**", pattern: "keep.yml", present: []string{"keep.yml"}},
		{name: "dir/** still hides non-reincluded", pattern: "junk.bin", absent: []string{"junk.bin"}},
		// build/ is a defaultSkipDir: pruned by name regardless of any
		// gitignore negation re-including its contents.
		{name: "skipDir build hidden despite negation", pattern: "info.json", absent: []string{"info.json"}},
		// pkg/web/dist is pruned by the unanchored `dist/` rule — per git,
		// a file cannot be re-included when its parent directory is
		// excluded, so leak.js AND the negation targets stay hidden.
		{name: "pkg web dist fully hidden", pattern: "**/pkg/web/dist/*", absent: []string{".gitkeep", "README.md", "leak.js"}},
	})
}
