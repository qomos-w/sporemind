package persist

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestMarkdownPersistRoundTripAndDelete(t *testing.T) {
	root := t.TempDir()
	store := NewMarkdownPersist(root)
	if err := store.Save("session/node-1", Markdown{Raw: "---\nid: node-1\n---\nbody\n"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "session", "node-1.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected markdown file: %v", err)
	}
	var got Markdown
	if err := store.Load("session/node-1", &got); err != nil {
		t.Fatal(err)
	}
	if got.Raw != "---\nid: node-1\n---\nbody\n" {
		t.Fatalf("unexpected markdown %q", got.Raw)
	}
	if err := store.Delete("session/node-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Load("session/node-1", &got); !errors.Is(err, ErrNotExist) {
		t.Fatalf("load after delete = %v, want ErrNotExist", err)
	}
}

func TestMarkdownPersistRejectsTraversal(t *testing.T) {
	store := NewMarkdownPersist(t.TempDir())
	for _, name := range []string{"../outside", "/absolute", "."} {
		if err := store.Save(name, Markdown{Raw: "x"}); err == nil {
			t.Errorf("Save(%q) should reject invalid name", name)
		}
	}
}

func TestMarkdownPersistList(t *testing.T) {
	root := t.TempDir()
	store, ok := NewMarkdownPersist(root).(*MarkdownPersist)
	if !ok {
		t.Fatal("NewMarkdownPersist must return *MarkdownPersist")
	}
	for _, name := range []string{"session/node-1", "session/node-2", "ontology/role-x"} {
		if err := store.Save(name, Markdown{Raw: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	// Transient atomic-write temp file must be skipped.
	if err := os.WriteFile(filepath.Join(root, "session", "node-1.md.tmp"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// Foreign file must be skipped.
	if err := os.WriteFile(filepath.Join(root, "session", "notes.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	all, err := store.List("")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(all)
	if want := []string{"ontology/role-x", "session/node-1", "session/node-2"}; !reflect.DeepEqual(all, want) {
		t.Fatalf("List(\"\") = %v, want %v", all, want)
	}

	sub, err := store.List("session")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(sub)
	if want := []string{"session/node-1", "session/node-2"}; !reflect.DeepEqual(sub, want) {
		t.Fatalf("List(\"session\") = %v, want %v", sub, want)
	}

	empty, err := store.List("nonexistent")
	if err != nil {
		t.Fatalf("List on missing prefix should not error: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("List on missing prefix = %v, want empty", empty)
	}

	var _ Lister = store
}
