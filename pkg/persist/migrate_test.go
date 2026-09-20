package persist

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// writeLegacyState seeds the legacy layout: <root>/<name>/state.json.
func writeLegacyState(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateFSLegacyLayout_OldLayout(t *testing.T) {
	root := t.TempDir()
	writeLegacyState(t, root, "actor-1", `{"v":1}`)
	writeLegacyState(t, root, "actor-2", `{"v":2}`)

	if err := MigrateFSLegacyLayout(root); err != nil {
		t.Fatalf("MigrateFSLegacyLayout: %v", err)
	}

	// Documents moved to <name>.json and loadable through the new store.
	s := NewFSPersist(root)
	var got struct {
		V int `json:"v"`
	}
	if err := s.Load("actor-1", &got); err != nil {
		t.Fatalf("Load actor-1 after migration: %v", err)
	}
	if got.V != 1 {
		t.Errorf("actor-1: got v=%d, want 1", got.V)
	}
	if err := s.Load("actor-2", &got); err != nil {
		t.Fatalf("Load actor-2 after migration: %v", err)
	}
	if got.V != 2 {
		t.Errorf("actor-2: got v=%d, want 2", got.V)
	}

	// Legacy directories must be gone.
	for _, name := range []string{"actor-1", "actor-2"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("legacy dir %q should be removed after migration (stat err %v)", name, err)
		}
	}
}

func TestMigrateFSLegacyLayout_SubPathName(t *testing.T) {
	root := t.TempDir()
	// A hierarchical name maps to a nested legacy directory.
	writeLegacyState(t, root, "session/node-1", `{"id":"node-1"}`)

	if err := MigrateFSLegacyLayout(root); err != nil {
		t.Fatalf("MigrateFSLegacyLayout: %v", err)
	}

	s := NewFSPersist(root)
	var got struct {
		ID string `json:"id"`
	}
	if err := s.Load("session/node-1", &got); err != nil {
		t.Fatalf("Load session/node-1 after migration: %v", err)
	}
	if got.ID != "node-1" {
		t.Errorf("got id=%q, want node-1", got.ID)
	}
	if _, err := os.Stat(filepath.Join(root, "session", "node-1")); !os.IsNotExist(err) {
		t.Errorf("legacy nested dir should be removed (stat err %v)", err)
	}
}

func TestMigrateFSLegacyLayout_MixedOldAndNew(t *testing.T) {
	root := t.TempDir()
	// actor-1 exists only in the legacy layout; actor-2 exists in BOTH.
	writeLegacyState(t, root, "actor-1", `{"v":1}`)
	writeLegacyState(t, root, "actor-2", `{"v":2}`)
	s := NewFSPersist(root)
	if err := s.Save("actor-2", map[string]int{"v": 22}); err != nil {
		t.Fatal(err)
	}

	if err := MigrateFSLegacyLayout(root); err != nil {
		t.Fatalf("MigrateFSLegacyLayout: %v", err)
	}

	var got struct {
		V int `json:"v"`
	}
	if err := s.Load("actor-1", &got); err != nil {
		t.Fatalf("Load actor-1: %v", err)
	}
	if got.V != 1 {
		t.Errorf("actor-1: got v=%d, want 1", got.V)
	}
	// New-layout document wins over the legacy source for actor-2.
	if err := s.Load("actor-2", &got); err != nil {
		t.Fatalf("Load actor-2: %v", err)
	}
	if got.V != 22 {
		t.Errorf("actor-2: got v=%d, want 22 (new layout wins)", got.V)
	}
}

func TestMigrateFSLegacyLayout_Idempotent(t *testing.T) {
	root := t.TempDir()
	writeLegacyState(t, root, "actor-1", `{"v":1}`)

	if err := MigrateFSLegacyLayout(root); err != nil {
		t.Fatalf("first MigrateFSLegacyLayout: %v", err)
	}
	// Capture the migrated bytes, run again, and confirm nothing changed.
	doc := filepath.Join(root, "actor-1.json")
	first, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateFSLegacyLayout(root); err != nil {
		t.Fatalf("second MigrateFSLegacyLayout: %v", err)
	}
	second, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("migration not idempotent: first=%q second=%q", first, second)
	}

	// And the migrated document still loads.
	s := NewFSPersist(root)
	var got struct {
		V int `json:"v"`
	}
	if err := s.Load("actor-1", &got); err != nil {
		t.Fatalf("Load after idempotent re-run: %v", err)
	}
	if got.V != 1 {
		t.Errorf("got v=%d, want 1", got.V)
	}
}

func TestMigrateFSLegacyLayout_MissingRoot(t *testing.T) {
	// A root that does not exist (fresh install) must not error.
	if err := MigrateFSLegacyLayout(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatalf("MigrateFSLegacyLayout on missing root: %v", err)
	}
}

func TestMigrateFSLegacyLayout_LeavesForeignFiles(t *testing.T) {
	root := t.TempDir()
	// Legacy state alongside an unrelated markdown memory sub-tree that must
	// survive the migration (the source dir is not empty, so it stays).
	writeLegacyState(t, root, "actor-1", `{"v":1}`)
	memDir := filepath.Join(root, "actor-1", "memory", "session")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "node-1.md"), []byte("body"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateFSLegacyLayout(root); err != nil {
		t.Fatalf("MigrateFSLegacyLayout: %v", err)
	}

	if _, err := os.Stat(filepath.Join(memDir, "node-1.md")); err != nil {
		t.Errorf("markdown memory must survive migration: %v", err)
	}
	s := NewFSPersist(root)
	var got struct {
		V int `json:"v"`
	}
	if err := s.Load("actor-1", &got); err != nil {
		t.Fatalf("Load actor-1: %v", err)
	}
	if got.V != 1 {
		t.Errorf("got v=%d, want 1", got.V)
	}
}

// TestFSPersist_List verifies the Lister capability of FSPersist: names are
// returned extension-stripped, forward-slash relative, and transient/corrupt
// artifacts are skipped.
func TestFSPersist_List(t *testing.T) {
	root := t.TempDir()
	s := NewFSPersist(root)
	var _ Lister = s

	for _, name := range []string{"node-1", "session/node-2", "session/node-3"} {
		if err := s.Save(name, map[string]int{"v": 1}); err != nil {
			t.Fatal(err)
		}
	}
	// Transient + corrupt artifacts must be skipped.
	if err := os.WriteFile(filepath.Join(root, "node-1.json.tmp"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "session", "node-2.json.corrupt-20260101T000000Z"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	all, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(all)
	want := []string{"node-1", "session/node-2", "session/node-3"}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("List(\"\") = %v, want %v", all, want)
	}

	sub, err := s.List("session")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(sub)
	wantSub := []string{"session/node-2", "session/node-3"}
	if !reflect.DeepEqual(sub, wantSub) {
		t.Errorf("List(\"session\") = %v, want %v", sub, wantSub)
	}

	missing, err := s.List("nonexistent")
	if err != nil {
		t.Fatalf("List on missing prefix should not error: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("List on missing prefix = %v, want empty", missing)
	}
}

// TestFSPersist_DeleteCascade verifies the cascade convention: Delete(name)
// removes <name>.json AND the same-named <name>/ directory holding child
// documents.
func TestFSPersist_DeleteCascade(t *testing.T) {
	root := t.TempDir()
	s := NewFSPersist(root)

	if err := s.Save("actor-1", map[string]int{"v": 1}); err != nil {
		t.Fatal(err)
	}
	// Child documents live under <name>/ per the "<actorID>/xxx" convention.
	childDir := filepath.Join(root, "actor-1", "memory")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "node.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete("actor-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "actor-1.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("document actor-1.json should be removed (stat err %v)", err)
	}
	if _, err := os.Stat(filepath.Join(root, "actor-1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("cascade dir actor-1/ should be removed (stat err %v)", err)
	}

	// Load must report ErrNotExist after the cascade delete.
	var got map[string]int
	if err := s.Load("actor-1", &got); !errors.Is(err, ErrNotExist) {
		t.Errorf("Load after cascade delete = %v, want ErrNotExist", err)
	}
}
