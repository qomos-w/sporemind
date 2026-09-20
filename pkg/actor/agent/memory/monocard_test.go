package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/sporemind/pkg/persist"
)

func TestMonocardCheckpointRestoreAndDelete(t *testing.T) {
	root := t.TempDir()
	store := persist.NewMarkdownPersist(root)
	g := New(Config{})
	first := g.AddNode("first", NodeTypeSession, "first body", "first head", 3)
	second := g.AddNode("second", NodeTypeExperience, "second body", "second: head\nwith detail", 4)
	if err := g.AddEdge(first.ID, second.ID, EdgeTypeCross); err != nil {
		t.Fatal(err)
	}
	manifest, err := g.CheckpointMonocards(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"session/first.md", "experience/second.md"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	restored := New(Config{})
	if err := restored.RestoreMonocards(store, manifest); err != nil {
		t.Fatal(err)
	}
	if got := restored.Node("first"); got == nil || got.Label != "first body" || got.Head != "first head" || got.Tokens != 3 {
		t.Fatalf("unexpected restored node %#v", got)
	}
	if got := restored.Node("second"); got == nil || got.Head != "second: head\nwith detail" {
		t.Fatalf("head with YAML-sensitive content did not restore: %#v", got)
	}
	if restored.EdgeCount() != 1 {
		t.Fatalf("restored graph edge mismatch: got %d, want 1", restored.EdgeCount())
	}
	if !g.RemoveNode("first") {
		t.Fatal("remove first")
	}
	if _, err := g.CheckpointMonocards(store, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "session", "first.md")); !os.IsNotExist(err) {
		t.Fatalf("removed node file still exists: %v", err)
	}
}

func TestMonocardBackwardCompatNoHead(t *testing.T) {
	// Old-format monocard without "head" field should decode with Head="" and not lose Label.
	old := "---\nid: old-node\nlayer: session\ntokens: 5\nenergy: 100\ncreated_at: 2026-01-01T00:00:00Z\naccessed_at: 2026-01-01T00:00:00Z\nmarked_for_death: false\nedges: []\n---\noriginal body content\n"
	node, edges, err := func() (*Node, []monocardEdge, error) {
		// Use decodeMonocard from the package
		return decodeMonocard(old)
	}()
	if err != nil {
		t.Fatalf("decode old monocard: %v", err)
	}
	if node.Head != "" {
		t.Errorf("old-format head should be empty, got %q", node.Head)
	}
	if node.Label != "original body content" {
		t.Errorf("old-format Label should be preserved: got %q", node.Label)
	}
	if len(edges) != 0 || node.Tokens != 5 {
		t.Error("other fields should decode correctly")
	}
}

func TestMonocardBackwardCompatNoProtected(t *testing.T) {
	// Old-format monocard without "protected" field should decode with Protected=false.
	old := "---\nid: old-node\nlayer: session\ntokens: 5\nenergy: 100\ncreated_at: 2026-01-01T00:00:00Z\naccessed_at: 2026-01-01T00:00:00Z\nmarked_for_death: false\nhead_json: \"old head\"\nedges: []\n---\noriginal body content\n"
	node, _, err := decodeMonocard(old)
	if err != nil {
		t.Fatalf("decode old monocard: %v", err)
	}
	if node.Protected {
		t.Errorf("old-format protected should default to false, got %v", node.Protected)
	}
	if node.Head != "old head" {
		t.Errorf("old-format head should be preserved: got %q", node.Head)
	}
}

func TestMonocardRoundTripProtected(t *testing.T) {
	root := t.TempDir()
	store := persist.NewMarkdownPersist(root)
	g := New(Config{})
	addNode(g, "protected-node", NodeTypeOntology, "anchor body", 0)
	g.nodes["protected-node"].Protected = true
	g.nodes["protected-node"].Energy = 500

	manifest, err := g.CheckpointMonocards(store, nil)
	if err != nil {
		t.Fatal(err)
	}

	restored := New(Config{})
	if err := restored.RestoreMonocards(store, manifest); err != nil {
		t.Fatal(err)
	}
	n := restored.Node("protected-node")
	if n == nil || !n.Protected {
		t.Error("protected flag should survive monocard round-trip")
	}
	if n.Energy != 500 {
		t.Errorf("energy should survive round-trip: got %.2f", n.Energy)
	}
}

func TestMonocardLabelEscaping(t *testing.T) {
	// Labels containing "---" on its own line must not corrupt the YAML
	// frontmatter boundary. label_json in the frontmatter carries the
	// JSON-escaped label safely.
	root := t.TempDir()
	store := persist.NewMarkdownPersist(root)
	g := New(Config{})
	dangerous := "line1\n---\n injected YAML\n---\nline4"
	n := g.AddNode("esc-node", NodeTypeSession, dangerous, "head", 1)
	if n == nil {
		t.Fatal("add node failed")
	}
	manifest, err := g.CheckpointMonocards(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	restored := New(Config{})
	if err := restored.RestoreMonocards(store, manifest); err != nil {
		t.Fatal(err)
	}
	got := restored.Node("esc-node")
	if got == nil {
		t.Fatal("node not restored")
	}
	if got.Label != dangerous {
		t.Errorf("label with \\n---\\n not preserved:\n  want: %q\n  got:  %q", dangerous, got.Label)
	}
}

func TestMonocardRestoreSkipsCorruptFiles(t *testing.T) {
	// When one monocard file is corrupt/unreadable, RestoreMonocards
	// should skip it and restore the remaining nodes.
	root := t.TempDir()
	store := persist.NewMarkdownPersist(root)
	g := New(Config{})
	g.AddNode("good-node", NodeTypeSession, "good body", "good head", 2)
	bad := g.AddNode("bad-node", NodeTypeSession, "bad body", "bad head", 3)

	manifest, err := g.CheckpointMonocards(store, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the bad-node file on disk.
	badPath := filepath.Join(root, filepath.FromSlash(monocardPath(monocardNodeRef{ID: bad.ID, Layer: bad.Type}))+".md")
	if err := os.WriteFile(badPath, []byte("not a valid monocard"), 0644); err != nil {
		t.Fatal(err)
	}

	restored := New(Config{})
	if err := restored.RestoreMonocards(store, manifest); err != nil {
		t.Fatalf("RestoreMonocards should not return error for corrupt file: %v", err)
	}
	if got := restored.Node("good-node"); got == nil || got.Label != "good body" {
		t.Errorf("good node should be restored: got %#v", got)
	}
	if got := restored.Node("bad-node"); got != nil {
		t.Error("corrupt node should have been skipped, not restored")
	}
}

// Regression: the previous-manifest diff cannot reach orphan files left by an
// unparseable/empty previous manifest — e.g. the first checkpoint after a
// process restart where the graph came back fresh but monocard files from an
// earlier life remain on disk. A sweep (Lister store) must delete them.
func TestMonocardCheckpointSweepsOrphansFromEarlierLife(t *testing.T) {
	root := t.TempDir()
	store := persist.NewMarkdownPersist(root)

	// Earlier life: graph with node "old" checkpointed.
	g1 := New(Config{})
	g1.AddNode("old", NodeTypeSession, "old body", "old head", 1)
	if _, err := g1.CheckpointMonocards(store, nil); err != nil {
		t.Fatal(err)
	}

	// New life: fresh graph with only node "new"; previous manifest lost
	// (nil) — the exact leak scenario observed on a live agent.
	g2 := New(Config{})
	g2.AddNode("new", NodeTypeSession, "new body", "new head", 1)
	if _, err := g2.CheckpointMonocards(store, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(root, "session", "old.md")); !os.IsNotExist(err) {
		t.Fatalf("orphan file from earlier life still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "session", "new.md")); err != nil {
		t.Fatalf("referenced file missing: %v", err)
	}

	names, err := store.(persist.Lister).List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "session/new" {
		t.Fatalf("store should contain only session/new, got %v", names)
	}
}

// DeleteMonocards (unmount) must sweep strays the manifest does not
// reference, so unmount/remount cycles cannot leak files.
func TestDeleteMonocardsSweepsStrays(t *testing.T) {
	root := t.TempDir()
	store := persist.NewMarkdownPersist(root)

	g := New(Config{})
	g.AddNode("kept", NodeTypeSession, "kept body", "kept head", 1)
	manifest, err := g.CheckpointMonocards(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an orphan from a missed cleanup.
	if err := store.Save("session/stray", persist.Markdown{Raw: "stray"}); err != nil {
		t.Fatal(err)
	}

	if err := DeleteMonocards(store, manifest); err != nil {
		t.Fatal(err)
	}

	names, err := store.(persist.Lister).List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("unmount should leave the store empty, got %v", names)
	}
}
