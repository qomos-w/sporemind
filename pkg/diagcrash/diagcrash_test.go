package diagcrash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
)

func setTempDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	return dir
}

func TestWrite_CreatesFileUnderLogs(t *testing.T) {
	setTempDataDir(t)
	path, err := Write("boom")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), "crash-") {
		t.Errorf("path base %q should start with crash-", filepath.Base(path))
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "boom" {
		t.Errorf("content = %q, want boom", got)
	}
}

func TestPending_ReturnsOnlyTopLevelCrashFiles(t *testing.T) {
	dir := setTempDataDir(t)
	// Three crash files at top level.
	if _, err := Write("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Write("b"); err != nil {
		t.Fatal(err)
	}
	// An unrelated file.
	if err := os.WriteFile(filepath.Join(dir, "logs", "other.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// An already-replayed file (in the replayed subdir).
	replayedDir := filepath.Join(dir, "logs", ReplayedDir)
	if err := os.MkdirAll(replayedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replayedDir, "crash-old.log"), []byte("replayed"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Pending = %v, want 2 files", got)
	}
	for _, p := range got {
		if !strings.HasPrefix(filepath.Base(p), "crash-") {
			t.Errorf("unexpected file in Pending: %s", p)
		}
	}
}

func TestPending_EmptyWhenLogsDirMissing(t *testing.T) {
	setTempDataDir(t)
	got, err := Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Pending = %v, want empty", got)
	}
}

func TestMarkReplayed_MovesFile(t *testing.T) {
	dir := setTempDataDir(t)
	path, err := Write("move me")
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkReplayed(path); err != nil {
		t.Fatalf("MarkReplayed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("source still exists after MarkReplayed: %v", err)
	}
	moved := filepath.Join(dir, "logs", ReplayedDir, filepath.Base(path))
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("moved file missing: %v", err)
	}
	// Pending no longer lists it.
	got, err := Pending()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if p == path {
			t.Errorf("Pending still lists replayed file: %s", p)
		}
	}
}

func TestMarkReplayed_MissingFileIsNoOp(t *testing.T) {
	setTempDataDir(t)
	if err := MarkReplayed("/no/such/file"); err != nil {
		t.Errorf("MarkReplayed on missing file should be no-op, got: %v", err)
	}
	if err := MarkReplayed(""); err != nil {
		t.Errorf("MarkReplayed on empty path should be no-op, got: %v", err)
	}
}
