package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateWorktreeGoWork(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "sporemind")
	wtPath := filepath.Join(tmp, "wt-base", "test-uuid")

	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a go.mod with replace directives.
	goMod := `module github.com/qomos-w/sporemind

go 1.25.0

require (
	github.com/qomos-w/gospore v0.0.0
)

replace github.com/qomos-w/gospore => ../gospore

replace github.com/qomos-w/spore => ../spore
`
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a main repo go.work with use directives.
	goWork := `go 1.25.0

use ./.

use ./sporemind-plugin-sdk
`
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte(goWork), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := generateWorktreeGoWork(root, wtPath); err != nil {
		t.Fatalf("generateWorktreeGoWork: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wtPath, "go.work"))
	if err != nil {
		t.Fatalf("read go.work: %v", err)
	}
	content := string(data)

	// Must contain use directives from the main repo's go.work.
	if !strings.Contains(content, "use ./.") {
		t.Errorf("go.work missing 'use ./.', got:\n%s", content)
	}
	if !strings.Contains(content, "use ./sporemind-plugin-sdk") {
		t.Errorf("go.work missing 'use ./sporemind-plugin-sdk', got:\n%s", content)
	}

	// Must contain rewritten replace directives using dynamic relative paths.
	// wtPath is at tmp/wt-base/test-uuid, root is at tmp/sporemind.
	// ../gospore from root = tmp/gospore. Relative from wtPath: ../../sporemind/../gospore
	relGospore, _ := filepath.Rel(wtPath, filepath.Join(root, "..", "gospore"))
	relSpore, _ := filepath.Rel(wtPath, filepath.Join(root, "..", "spore"))
	wantGospore := "replace github.com/qomos-w/gospore => " + filepath.ToSlash(relGospore)
	wantSpore := "replace github.com/qomos-w/spore => " + filepath.ToSlash(relSpore)
	if !strings.Contains(content, wantGospore) {
		t.Errorf("go.work missing rewritten gospore replace %q, got:\n%s", wantGospore, content)
	}
	if !strings.Contains(content, wantSpore) {
		t.Errorf("go.work missing rewritten spore replace %q, got:\n%s", wantSpore, content)
	}

	// Must NOT contain the original ../gospore path.
	if strings.Contains(content, "=> ../gospore") {
		t.Errorf("go.work contains non-rewritten ../gospore, got:\n%s", content)
	}
}

func TestGenerateWorktreeGoWork_NoGoMod(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "sporemind")
	wtPath := filepath.Join(tmp, "wt-base", "test-uuid")

	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// No go.mod — should be a no-op (no error).
	if err := generateWorktreeGoWork(root, wtPath); err != nil {
		t.Fatalf("expected nil error with no go.mod, got: %v", err)
	}

	// go.work should not exist.
	if _, err := os.Stat(filepath.Join(wtPath, "go.work")); err == nil {
		t.Fatal("go.work should not be written when go.mod is absent")
	}
}

func TestAdjustReplacePath(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "sporemind")
	wtPath := filepath.Join(tmp, "wt-base", "test-uuid")

	// Calculate expected values dynamically using filepath.Rel so the test
	// is platform-independent.
	relGospore, _ := filepath.Rel(wtPath, filepath.Join(root, "..", "gospore"))
	relSpore, _ := filepath.Rel(wtPath, filepath.Join(root, "..", "spore"))
	wantGospore := filepath.ToSlash(relGospore)
	wantSpore := filepath.ToSlash(relSpore)

	tests := []struct {
		target   string
		expected string
	}{
		{"../gospore", wantGospore},
		{"../spore", wantSpore},
		{"v1.0.0", "v1.0.0"},
		{"github.com/foo/bar", "github.com/foo/bar"},
	}
	for _, tt := range tests {
		got := adjustReplacePath(root, wtPath, tt.target)
		if got != tt.expected {
			t.Errorf("adjustReplacePath(%q) = %q, want %q", tt.target, got, tt.expected)
		}
	}
}