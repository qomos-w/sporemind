package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRegistrySaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "registry.json")
	r, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load (empty): %v", err)
	}
	if err := r.Record("svc-a", "0123456789abcdef"); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Reload from disk and verify the entry survived.
	r2, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := r2.Lookup("svc-a"); got != "0123456789abcdef" {
		t.Fatalf("Lookup: got %q, want 0123456789abcdef", got)
	}
	if r2.NodeID != "1" {
		t.Fatalf("NodeID: got %q, want 1", r2.NodeID)
	}
}

// TestRegistrySaveAtomic verifies that Save is atomic: no stray .tmp file is
// left behind on success, and the on-disk content decodes cleanly.
func TestRegistrySaveAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	r, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := r.Record("svc", "abc"); err != nil {
		t.Fatalf("record: %v", err)
	}
	// No leftover temp file.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("leftover .tmp file: %v", err)
	}
	// The file decodes cleanly (not torn).
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// TestRegistryCorruptBackup mirrors FSPersist.Load: a registry.json that
// fails to decode must be renamed to a .corrupt-<timestamp> backup so the
// original bytes survive for manual recovery, and the original path no
// longer exists (overwriting it on next Save would silently lose the data).
func TestRegistryCorruptBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")

	garbage := []byte("{this is not valid json")
	if err := os.WriteFile(path, garbage, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("expected decode error for corrupt registry, got nil")
	}
	if !strings.Contains(err.Error(), "corrupt file backed up to") {
		t.Fatalf("error should mention corrupt backup, got: %v", err)
	}

	// Original path must be gone (it was renamed to the backup).
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original corrupt file should have been renamed away: %v", err)
	}

	// A single .corrupt-<timestamp> backup must exist with the original bytes.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var backup string
	for _, e := range entries {
		matched, _ := regexp.MatchString(`^registry\.json\.corrupt-\d{8}T\d{6}Z$`, e.Name())
		if matched {
			backup = filepath.Join(dir, e.Name())
		}
	}
	if backup == "" {
		t.Fatalf("no corrupt-<timestamp> backup found; entries: %v", entries)
	}
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != string(garbage) {
		t.Fatalf("backup content mismatch: got %q, want %q", got, garbage)
	}
}

// TestRegistryCorruptBackupMissingFile tests the not-exist path: a missing
// registry.json is a normal first-start, not an error, and never triggers a
// corrupt backup.
func TestRegistryLoadMissing(t *testing.T) {
	dir := t.TempDir()
	r, err := LoadRegistry(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if len(r.Services) != 0 {
		t.Fatalf("expected empty services, got %d", len(r.Services))
	}
}

// TestRegistryCorruptEmptyFile: a zero-length file decodes to the empty
// registry (not an error), matching LoadRegistry's len(data)==0 fast path.
func TestRegistryLoadEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(r.Services) != 0 {
		t.Fatalf("expected empty services, got %d", len(r.Services))
	}
}
