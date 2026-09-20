package persist

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFSPersistContract runs the reusable contract suite against FSPersist.
// This is the canonical entry point: every backend must pass RunContractTests.
func TestFSPersistContract(t *testing.T) {
	RunContractTests(t, func(t *testing.T) Persist {
		return NewFSPersist(t.TempDir())
	})
}

// TestNew_ValidBackends verifies that New accepts the only supported backend
// ("fs") and the empty string (which defaults to fs), returning a non-nil
// Persist and nil error.
func TestNew_ValidBackends(t *testing.T) {
	cases := []BackendType{"fs", ""}
	for _, backend := range cases {
		t.Run(string(backend), func(t *testing.T) {
			p, err := New(PersistConfig{Backend: backend, DataDir: t.TempDir(), Prefix: "test"})
			if err != nil {
				t.Fatalf("New(backend=%q) error = %v, want nil", backend, err)
			}
			if p == nil {
				t.Fatalf("New(backend=%q) returned nil Persist", backend)
			}
		})
	}
}

// TestNew_InvalidBackend verifies that New rejects unknown or unimplemented
// backends with a diagnosable error instead of silently falling back to fs.
// This is the core of T3's constraint面: a typo must be visible.
func TestNew_InvalidBackend(t *testing.T) {
	// mongo/mysql/postgres are now registered (B5); their alias typos and the
	// still-unimplemented backends must remain diagnosable rejects.
	invalid := []BackendType{"mongodb", "postgresql", "oss", "monga", "filesystem", "s3"}
	for _, backend := range invalid {
		t.Run(string(backend), func(t *testing.T) {
			p, err := New(PersistConfig{Backend: backend, DataDir: t.TempDir(), Prefix: "test"})
			if err == nil {
				t.Fatalf("New(backend=%q) expected error, got nil and Persist %v", backend, p)
			}
			if p != nil {
				t.Errorf("New(backend=%q) should return nil Persist on error, got %v", backend, p)
			}
			if !strings.Contains(err.Error(), string(backend)) {
				t.Errorf("error %q should contain backend name %q", err, backend)
			}
		})
	}
}

// TestMustNew_PanicsOnInvalidBackend verifies that MustNew panics for an
// invalid backend, providing a hard-fail for package-level init where error
// propagation is impossible.
func TestMustNew_PanicsOnInvalidBackend(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustNew with invalid backend should panic")
		}
	}()
	_ = MustNew(PersistConfig{Backend: "mongo", DataDir: t.TempDir(), Prefix: "test"})
}

// TestMustNew_ValidBackend verifies that MustNew returns a non-nil Persist for
// the valid fs backend.
func TestMustNew_ValidBackend(t *testing.T) {
	p := MustNew(PersistConfig{Backend: "fs", DataDir: t.TempDir(), Prefix: "test"})
	if p == nil {
		t.Fatal("MustNew(fs) returned nil")
	}
}

// TestFSPersist_NoTempResidue verifies the FS-specific atomic-write contract:
// after a successful Save, the .tmp staging file must not survive on disk.
// This complements the interface-level suite, which cannot inspect the
// filesystem.
func TestFSPersist_NoTempResidue(t *testing.T) {
	dir := t.TempDir()
	s := NewFSPersist(dir)

	if err := s.Save("a", map[string]int{"v": 1}); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "a.json.tmp")
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("a.json.tmp should be renamed away after Save")
	}
}

func TestLoadOrZero(t *testing.T) {
	dir := t.TempDir()
	s := NewFSPersist(dir)

	// Missing state — should return nil (treated as first start).
	var got string
	if err := LoadOrZero(s, "nonexistent", &got); err != nil {
		t.Errorf("LoadOrZero on missing: expected nil, got %v", err)
	}

	// Existing state — should load normally.
	type payload struct{ V string }
	if err := s.Save("a", payload{V: "x"}); err != nil {
		t.Fatal(err)
	}
	var got2 payload
	if err := LoadOrZero(s, "a", &got2); err != nil {
		t.Errorf("LoadOrZero on existing: %v", err)
	}
	if got2.V != "x" {
		t.Errorf("LoadOrZero didn't restore value: got %q", got2.V)
	}
}

func TestLoad_CorruptBacksUp(t *testing.T) {
	dir := t.TempDir()
	s := NewFSPersist(dir)

	docPath := filepath.Join(dir, "a.json")
	// Simulate a torn write: truncated JSON.
	if err := os.WriteFile(docPath, []byte(`{"mounts": [{"Name": "sporecod`), 0644); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	err := s.Load("a", &got)
	if err == nil {
		t.Fatal("expected decode error for corrupt state")
	}
	if errors.Is(err, ErrNotExist) {
		t.Fatal("corrupt state must not be reported as ErrNotExist")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("error should mention the backup path, got %v", err)
	}

	// Original bytes must survive in an a.json.corrupt-* file.
	matches, globErr := filepath.Glob(filepath.Join(dir, "a.json.corrupt-*"))
	if globErr != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one corrupt backup, got %v (err %v)", matches, globErr)
	}
	data, readErr := os.ReadFile(matches[0])
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != `{"mounts": [{"Name": "sporecod` {
		t.Error("backup must preserve the original corrupt bytes")
	}

	// A subsequent Save must succeed and produce a fresh valid state.
	if err := s.Save("a", map[string]int{"v": 1}); err != nil {
		t.Fatal(err)
	}
	var restored map[string]int
	if err := s.Load("a", &restored); err != nil {
		t.Fatal(err)
	}
	if restored["v"] != 1 {
		t.Errorf("expected 1, got %d", restored["v"])
	}
}
