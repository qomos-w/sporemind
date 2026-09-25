package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

// OpenDirectory only reaches the platform opener for an existing directory;
// these tests pin the guard rails without ever launching a file manager.
func TestOpenDirectoryRejectsInvalidPaths(t *testing.T) {
	a := &App{}
	if err := a.OpenDirectory(""); err == nil {
		t.Fatal("empty path: want error")
	}
	if err := a.OpenDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing path: want error")
	}
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.OpenDirectory(file); err == nil {
		t.Fatal("regular file: want error")
	}
}
