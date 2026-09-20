package agent

import (
	"encoding/json"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// TestMemoryPersistRoundtrip verifies that a session memory saved by one
// actor instance is correctly restored after a simulated restart (new Actor
// with the same storeKey, loading from the same state.json + monocard files).
func TestMemoryPersistRoundtrip(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	dir := t.TempDir()
	config.SetDataDirForTest(dir)

	// --- Phase 1: First life — mount memory, save a session node ---
	a1 := &Actor{
		actorID: "persist-test",
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:mode:memory", Enabled: true},
		},
	}
	a1.syncCanonicalCardRefs()
	a1.syncMemoryMountState(nil)
	if a1.Graph == nil {
		t.Fatal("Phase 1: Graph is nil after mounting memory")
	}

	resp, err := a1.handleMemorySave(nil, domain.MemorySaveReq{Content: "user prefers dark mode", Layer: "session"})
	if err != nil {
		t.Fatalf("Phase 1: handleMemorySave: %v", err)
	}
	savedID := resp.Node.ID
	t.Logf("Phase 1: saved node ID=%s, memoryPersisted=%d bytes", savedID, len(a1.memoryPersisted))

	if len(a1.memoryPersisted) == 0 {
		t.Fatal("Phase 1: memoryPersisted is empty after save — persistMemorySnapshot failed silently")
	}

	// Verify monocard files exist on disk
	store := persist.NewMarkdownPersist(config.ActorDataDir() + "/agent/persist-test/memory")
	var doc persist.Markdown
	if err := store.Load("session/"+savedID, &doc); err != nil {
		t.Fatalf("Phase 1: monocard file not found on disk: %v", err)
	}

	// Verify state.json has the memory manifest
	var snap struct {
		Memory json.RawMessage `json:"memory,omitempty"`
	}
	if err := agentStore.Load("persist-test", &snap); err != nil {
		t.Fatalf("Phase 1: agentStore.Load: %v", err)
	}
	if len(snap.Memory) == 0 {
		t.Fatal("Phase 1: state.json has no memory manifest")
	}

	// --- Phase 2: Second life — new Actor, load from persisted state ---
	a2 := &Actor{
		actorID: "persist-test",
	}
	a2.loadMailbox(nil)

	if !a2.memoryEnabled() {
		t.Fatal("Phase 2: memory mode mount was not restored")
	}
	if len(a2.memoryPersisted) == 0 {
		t.Fatal("Phase 2: memoryPersisted is empty after loadMailbox")
	}

	a2.syncMemoryMountState(nil)
	if a2.Graph == nil {
		t.Fatal("Phase 2: Graph is nil after syncMemoryMountState")
	}

	// Verify the session node was restored
	snapResp, err := a2.handleMemorySnapshot(nil, domain.MemorySnapshotReq{})
	if err != nil {
		t.Fatalf("Phase 2: handleMemorySnapshot: %v", err)
	}

	found := false
	for _, n := range snapResp.Nodes {
		if n.ID == savedID && n.Content == "user prefers dark mode" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Phase 2: saved session node %q not found in restored snapshot (%d nodes)", savedID, len(snapResp.Nodes))
	}
	t.Logf("Phase 2: restored %d nodes, found saved node", len(snapResp.Nodes))
}

// TestMemorySurvivesSaveBeforeMount reproduces the production cold-start
// killer: during OnStart, syncSkillMounts (and any other component
// reconciliation) can call saveMailbox BEFORE syncMemoryMountState creates
// the graph. That intermediate save used to nil memoryPersisted, so
// state.json lost the memory manifest and the subsequent restore started
// from an empty graph — every restart silently wiped long-term memory.
func TestMemorySurvivesSaveBeforeMount(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	dir := t.TempDir()
	config.SetDataDirForTest(dir)

	// --- First life: mount memory, save a node, persist state ---
	a1 := &Actor{
		actorID: "startup-save-test",
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:mode:memory", Enabled: true},
		},
	}
	a1.syncCanonicalCardRefs()
	a1.syncMemoryMountState(nil)
	resp, err := a1.handleMemorySave(nil, domain.MemorySaveReq{Content: "long-term fact: database is utc-only", Layer: "session"})
	if err != nil {
		t.Fatalf("first life: handleMemorySave: %v", err)
	}
	savedID := resp.Node.ID

	// --- Second life: load, then save BEFORE the graph is mounted ---
	a2 := &Actor{actorID: "startup-save-test"}
	a2.loadMailbox(nil)
	if len(a2.memoryPersisted) == 0 {
		t.Fatal("second life: memoryPersisted empty after loadMailbox")
	}

	// The killer: an intermediate saveMailbox while Graph == nil (exactly
	// what syncSkillMounts does during OnStart before syncMemoryMountState).
	a2.saveMailbox(nil)

	if len(a2.memoryPersisted) == 0 {
		t.Fatal("REGRESSION: intermediate pre-mount save wiped the persisted memory manifest")
	}

	// Manifest pointer in state.json must also survive.
	var snap struct {
		Memory json.RawMessage `json:"memory,omitempty"`
	}
	if err := agentStore.Load("startup-save-test", &snap); err != nil {
		t.Fatalf("second life: agentStore.Load: %v", err)
	}
	if len(snap.Memory) == 0 {
		t.Fatal("REGRESSION: state.json lost the memory field after pre-mount save")
	}

	// Mount the graph and verify the node restores.
	a2.syncMemoryMountState(nil)
	if a2.Graph == nil {
		t.Fatal("second life: Graph nil after syncMemoryMountState")
	}
	snapResp, err := a2.handleMemorySnapshot(nil, domain.MemorySnapshotReq{})
	if err != nil {
		t.Fatalf("second life: handleMemorySnapshot: %v", err)
	}
	found := false
	for _, n := range snapResp.Nodes {
		if n.ID == savedID && n.Content == "long-term fact: database is utc-only" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("REGRESSION: node %q lost across pre-mount save + remount (%d nodes restored)", savedID, len(snapResp.Nodes))
	}
}
