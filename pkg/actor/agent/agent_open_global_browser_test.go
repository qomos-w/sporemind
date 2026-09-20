package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestResolveGlobalBrowserURL(t *testing.T) {
	root := t.TempDir()
	if err := writeFile(t, filepath.Join(root, "docs", "readme.md"), "hello"); err != nil {
		t.Fatal(err)
	}

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a.cachedProjectRoot.Store(&root)

	cases := []struct {
		input string
		want  string
	}{
		{"https://example.com", "https://example.com"},
		{"http://example.com", "http://example.com"},
		{"file:///C:/docs/readme.md", "file:///C:/docs/readme.md"},
		{"C:\\docs\\readme.md", "file:///C:/docs/readme.md"},
		{"D:/docs/readme.md", "file:///D:/docs/readme.md"},
		{"/usr/share/doc/readme.md", "file:///usr/share/doc/readme.md"},
		{"docs/readme.md", "file:///" + filepath.ToSlash(filepath.Join(root, "docs", "readme.md"))},
		{"example.com", "example.com"},
	}
	for _, c := range cases {
		got, err := a.resolveGlobalBrowserURL(ctx, c.input)
		if err != nil {
			t.Fatalf("resolveGlobalBrowserURL(%q): unexpected error: %v", c.input, err)
		}
		if got != c.want {
			t.Errorf("resolveGlobalBrowserURL(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestResolveGlobalBrowserURL_RelativeEscapesProjectRoot(t *testing.T) {
	root := t.TempDir()
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a.cachedProjectRoot.Store(&root)

	_, err := a.resolveGlobalBrowserURL(ctx, "../etc/passwd")
	if err == nil {
		t.Fatal("expected error for relative path escaping project root")
	}
}

func writeFile(t *testing.T, path, content string) error {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
