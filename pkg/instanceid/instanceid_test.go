package instanceid

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAtGeneratesAndPersistsStableID(t *testing.T) {
	dir := t.TempDir()
	first := resolveAt(dir)
	if len(first) != 32 {
		t.Fatalf("expected 32-hex id, got %q", first)
	}
	if second := resolveAt(dir); second != first {
		t.Fatalf("id not stable: %q vs %q", first, second)
	}
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("id file not written: %v", err)
	}
	if strings.TrimSpace(string(data)) != first {
		t.Fatalf("persisted id %q != returned %q", string(data), first)
	}
}

func TestResolveAtIgnoresMalformedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("garbage"), 0600); err != nil {
		t.Fatal(err)
	}
	id := resolveAt(dir)
	if len(id) != 32 || id == "garbage" {
		t.Fatalf("expected fresh id for malformed file, got %q", id)
	}
}

func TestLoadOrGenerateEnvOverride(t *testing.T) {
	t.Setenv("SPOREMIND_INSTANCE_ID", "fixed-test-id")
	if got := loadOrGenerate(); got != "fixed-test-id" {
		t.Fatalf("env override ignored: %q", got)
	}
}
