package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestMemoryMountSaveInjectRoundtrip covers the full mount → save →
// snapshot → inject path end-to-end (rather than bypassing the mount with a
// pre-built Actor{Graph}). It guards against regressions where a saved
// session node disappears from the snapshot callable output or the session
// context injection. The frontend reads both of these surfaces, so a break in
// either one looks like "memory_save did nothing".
func TestMemoryMountSaveInjectRoundtrip(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{
		actorID: "roundtrip-test",
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:mode:memory", Enabled: true},
		},
	}
	a.syncCanonicalCardRefs()
	a.syncMemoryMountState(nil)
	if a.Graph == nil {
		t.Fatal("Graph is nil after mounting builtin:mode:memory")
	}

	resp, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: "user prefers concise answers", Layer: "session"})
	if err != nil {
		t.Fatalf("handleMemorySave: %v", err)
	}
	if resp.Node.Layer != "session" {
		t.Fatalf("saved node layer = %q, want session", resp.Node.Layer)
	}

	// Snapshot is the data source for the frontend brain graph.
	snap, err := a.handleMemorySnapshot(nil, domain.MemorySnapshotReq{})
	if err != nil {
		t.Fatalf("handleMemorySnapshot: %v", err)
	}
	if !snap.Mounted {
		t.Fatal("snapshot reports unmounted despite an active graph")
	}
	if got := len(snap.Nodes); got != 1 {
		t.Fatalf("snapshot nodes: want 1, got %d", len(snap.Nodes))
	}
	if snap.Nodes[0].Layer != "session" || snap.Nodes[0].Content != "user prefers concise answers" {
		t.Fatalf("snapshot node mismatch: %+v", snap.Nodes[0])
	}

	// Session nodes must be injected into the post-hot context block.
	blocks := a.memoryPostHotBlocks()
	if len(blocks) != 1 {
		t.Fatalf("post-hot blocks: want 1, got %d", len(blocks))
	}
	if blocks[0].Text == "" {
		t.Fatal("post-hot session block is empty")
	}

	// recall injection (shared by touch + context projection) must include it.
	if _, _, sess := a.Graph.RecallInjection(); len(sess) != 1 {
		t.Fatalf("recall injection session: want 1, got %d", len(sess))
	}
}
