package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestExpandBraces(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"*.ts", []string{"*.ts"}},
		{"*.{ts,tsx}", []string{"*.ts", "*.tsx"}},
		{"a{b,c}d{e,f}", []string{"abde", "abdf", "acde", "acdf"}},
		{"{a,b,c}", []string{"a", "b", "c"}},
		{"no.braces.here", []string{"no.braces.here"}},
		{"foo\\{bar,baz\\}", []string{"foo{bar,baz}"}},
		{"{a,b\\,c}", []string{"a", "b,c"}},
	}
	for _, c := range cases {
		got, err := expandBraces(c.in)
		if err != nil {
			t.Errorf("expandBraces(%q) error: %v", c.in, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("expandBraces(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("expandBraces(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestHandleGlob_BraceExpansion(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.ts"), []byte{}, 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.tsx"), []byte{}, 0644)
	_ = os.WriteFile(filepath.Join(dir, "c.go"), []byte{}, 0644)

	resolved := a.resolvePath(dir)
	t.Logf("dir: %q, resolved: %q", dir, resolved)
	resp, err := a.handleGlob(ctx, domain.FileSystemGlobReq{Pattern: "*.{ts,tsx}", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("got files: %v", resp.Files)
	if len(resp.Files) != 2 {
		t.Errorf("expected 2 matches, got %v", resp.Files)
	}
}

func TestDoublestarGlob_BraceExpansion(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.ts"), []byte{}, 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.tsx"), []byte{}, 0644)
	expanded, err := expandBraces("*.{ts,tsx}")
	if err != nil {
		t.Fatal(err)
	}
	for _, pat := range expanded {
		matches, err := doublestarGlob(dir, pat, 0, "", "")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("pattern %q matched %v", pat, matches)
	}
}

func TestHandleGlob_CommaSeparated(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.ts"), []byte{}, 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.tsx"), []byte{}, 0644)
	_ = os.WriteFile(filepath.Join(dir, "c.go"), []byte{}, 0644)

	resp, err := a.handleGlob(ctx, domain.FileSystemGlobReq{Pattern: "*.ts, *.tsx", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 2 {
		t.Errorf("expected 2 matches, got %v", resp.Files)
	}
}

func TestDoublestarGlob_ExcludeSkipsSubtree(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "target.go"), []byte{}, 0644)
	_ = os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "target.go"), []byte{}, 0644)
	_ = os.MkdirAll(filepath.Join(dir, "vendor"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "vendor", "target.go"), []byte{}, 0644)

	matches, err := doublestarGlob(dir, "**/target.go", 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 3 {
		t.Errorf("expected 3 matches without exclude, got %v", matches)
	}

	matches, err = doublestarGlob(dir, "**/target.go", 0, "", "node_modules/**,vendor/**")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0] != "target.go" {
		t.Errorf("expected only top-level target.go, got %v", matches)
	}
}
