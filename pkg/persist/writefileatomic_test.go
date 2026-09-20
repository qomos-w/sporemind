package persist

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "turn-1.json")

	if err := WriteFileAtomic(target, []byte(`{"id":"turn-1"}`), 0644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != `{"id":"turn-1"}` {
		t.Errorf("content = %q, want %q", got, `{"id":"turn-1"}`)
	}
}

func TestWriteFileAtomic_OverwriteReplacesContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "turn-1.json")

	if err := os.WriteFile(target, []byte("OLD"), 0644); err != nil {
		t.Fatalf("seed old: %v", err)
	}
	if err := WriteFileAtomic(target, []byte("NEW"), 0644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "NEW" {
		t.Errorf("content = %q, want NEW", got)
	}
}

func TestWriteFileAtomic_NoTempLeftBehind(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.json")

	if err := WriteFileAtomic(target, []byte("data"), 0644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file should be gone after success; stat err = %v", err)
	}
}

// TestWriteFileAtomic_FailingDirectoryCleansTemp locks the contract that a
// write that fails before the rename (here: target in a missing directory
// because WriteFileAtomic does not create dirs) must not leave a stray temp
// file in an unrelated existing location. We point the target at a path whose
// directory does not exist; OpenFile fails and nothing is written.
func TestWriteFileAtomic_FailingDirectoryCleansTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing-subdir", "turn.json")

	if err := WriteFileAtomic(target, []byte("data"), 0644); err == nil {
		t.Fatalf("expected error writing into missing directory, got nil")
	}
	// Nothing should have been created.
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("target should not exist after failed write; stat err = %v", err)
	}
}
