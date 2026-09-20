package runtime

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// Sync with ClientEpoch==0 must return the full snapshot plus lifecycle-only
// patch stubs: Epoch/Timestamp/AddedNodes-IDs/RemovedNodes, but never the
// heavy UpdatedNodes payloads that were previously replayed in full (~46MB
// on a long-running session).
func TestSyncEpochZeroStripsUpdatedNodes(t *testing.T) {
	p := newTopologyProvider(nil)
	p.currentEpoch = 3
	p.patches = []domain.GraphPatch{
		{
			Epoch:      1,
			Timestamp:  "2026-09-01T00:00:00Z",
			AddedNodes: []domain.UnifiedGraphNode{{ID: "a", Kind: "agent"}},
			RemovedNodes: []string{"gone"},
		},
		{
			Epoch:        2,
			Timestamp:    "2026-09-01T00:00:01Z",
			UpdatedNodes: []domain.UnifiedGraphNode{{ID: "a", Kind: "agent", Label: "heavy"}},
		},
	}
	p.currentGraph = domain.UnifiedGraph{
		Nodes: []domain.UnifiedGraphNode{{ID: "a", Kind: "agent"}},
		Edges: []domain.UnifiedGraphEdge{},
	}

	resp := p.Sync(0)

	if resp.CurrentEpoch != 3 {
		t.Fatalf("CurrentEpoch = %d, want 3", resp.CurrentEpoch)
	}
	if resp.Snapshot == nil || len(resp.Snapshot.Nodes) != 1 || resp.Snapshot.Nodes[0].ID != "a" {
		t.Fatalf("snapshot = %+v, want single node a", resp.Snapshot)
	}
	if len(resp.Patches) != 2 {
		t.Fatalf("patches = %d, want 2 (lifecycle stubs retained)", len(resp.Patches))
	}

	p0 := resp.Patches[0]
	if p0.Epoch != 1 || len(p0.AddedNodes) != 1 || p0.AddedNodes[0].ID != "a" || p0.AddedNodes[0].Kind != "" {
		t.Fatalf("stub patch 0 = %+v, want ID-only added node", p0)
	}
	if len(p0.RemovedNodes) != 1 || p0.RemovedNodes[0] != "gone" {
		t.Fatalf("stub patch 0 RemovedNodes = %v, want [gone]", p0.RemovedNodes)
	}
	if p0.Timestamp != "2026-09-01T00:00:00Z" {
		t.Fatalf("stub patch 0 Timestamp = %q", p0.Timestamp)
	}

	p1 := resp.Patches[1]
	if p1.Epoch != 2 {
		t.Fatalf("stub patch 1 Epoch = %d, want 2", p1.Epoch)
	}
	if len(p1.UpdatedNodes) != 0 {
		t.Fatalf("stub patch 1 must not carry UpdatedNodes, got %d", len(p1.UpdatedNodes))
	}
}

// Non-zero client epochs keep the previous behavior: full patches when they
// cover the gap, snapshot fallback otherwise.
func TestSyncIncrementalUnchanged(t *testing.T) {
	p := newTopologyProvider(nil)
	p.currentEpoch = 3
	p.patches = []domain.GraphPatch{
		{Epoch: 2, Timestamp: "t2", UpdatedNodes: []domain.UnifiedGraphNode{{ID: "a", Kind: "agent"}}},
		{Epoch: 3, Timestamp: "t3"},
	}
	p.currentGraph = domain.UnifiedGraph{
		Nodes: []domain.UnifiedGraphNode{{ID: "a"}},
		Edges: []domain.UnifiedGraphEdge{},
	}

	// Client at epoch 1 — patches cover 2..3.
	resp := p.Sync(1)
	if resp.Snapshot != nil {
		t.Fatalf("incremental sync returned a snapshot unexpectedly")
	}
	if len(resp.Patches) != 2 || len(resp.Patches[0].UpdatedNodes) != 1 {
		t.Fatalf("incremental patches must be complete, got %+v", resp.Patches)
	}

	// Client at current epoch — nothing to send.
	resp = p.Sync(3)
	if len(resp.Patches) != 0 {
		t.Fatalf("sync at current epoch should send no patches, got %d", len(resp.Patches))
	}
}