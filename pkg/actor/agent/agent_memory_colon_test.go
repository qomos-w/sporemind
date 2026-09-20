package agent

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/qomos-w/sporemind/pkg/actor/agent/memory"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// TestPersistMemorySnapshotWithColonInNodeID verifies that CheckpointMonocards
// succeeds when a node ID contains ':' (e.g. role:coder), which is reserved
// on Windows file systems.
func TestPersistMemorySnapshotWithColonInNodeID(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{
		actorID: "colon-test",
		child:   childState{Mode: false},
	}
	a.graphMu.Lock()
	a.Graph = memory.New(memory.DefaultConfig())
	a.graphMu.Unlock()

	// Add a node with ':' in the ID (like role:coder)
	ontologyNode := a.Graph.UpsertNode(
		memory.NodeID("role:coder"),
		memory.NodeTypeOntology,
		"role prompt content",
		"Agent role: coder",
		10,
	)
	if ontologyNode == nil {
		t.Fatal("failed to upsert ontology node with ':' in ID")
	}

	// Also add a normal session node
	a.Graph.AddNode("", memory.NodeTypeSession, "session content", "", 5)

	// Call persistMemorySnapshot — must not fail due to ':' in the file path
	if err := a.persistMemorySnapshot(); err != nil {
		t.Fatalf("persistMemorySnapshot with ':' in node ID: %v", err)
	}

	if len(a.memoryPersisted) == 0 {
		t.Fatal("memoryPersisted is empty after persistMemorySnapshot with ':' in node ID")
	}

	// Verify monocard file is stored at sanitized path (role_coder, not role:coder)
	store := persist.NewMarkdownPersist(config.ActorDataDir() + "/agent/colon-test/memory")
	var doc persist.Markdown
	if err := store.Load("ontology/role_coder", &doc); err != nil {
		t.Fatalf("failed to load ontology monocard for role_coder: %v", err)
	}
	if doc.Raw == "" {
		t.Fatal("ontology monocard is empty")
	}

	// Verify payload round-trip includes memory key
	payload := map[string]any{"rawSession": struct{}{}}
	if a.memoryPersisted != nil {
		payload["memory"] = a.memoryPersisted
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}
	var check map[string]json.RawMessage
	if err := json.Unmarshal(raw, &check); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if _, ok := check["memory"]; !ok {
		t.Fatal("memory key missing from payload after marshal/unmarshal")
	}

	_ = os.RemoveAll(config.ActorDataDir() + "/agent/colon-test")
}
