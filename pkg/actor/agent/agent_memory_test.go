package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/agent/memory"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// ── Zero allocation unmounted ────────────────────────────────────────────

func TestMemoryGraphNilWhenUnmounted(t *testing.T) {
	a := &Actor{}
	if a.Graph != nil {
		t.Fatal("expected nil Graph when no memory mode")
	}
}

func TestMemoryHandlersErrorWhenUnmounted(t *testing.T) {
	a := &Actor{}
	_, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: "test"})
	if err == nil || !strings.Contains(err.Error(), "not mounted") {
		t.Fatalf("expected 'not mounted' error, got %v", err)
	}
	_, err = a.handleMemoryRecall(nil, domain.MemoryRecallReq{})
	if err == nil || !strings.Contains(err.Error(), "not mounted") {
		t.Fatalf("expected 'not mounted' error, got %v", err)
	}
}

func TestMemoryHotContextUnmountedHasZeroOutput(t *testing.T) {
	a := &Actor{}
	if got := a.memoryHotContext(); got != nil {
		t.Fatalf("expected nil hot context, got %#v", got)
	}
}

func TestMemorySnapshotReturnsUnmounted(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleMemorySnapshot(nil, domain.MemorySnapshotReq{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Mounted {
		t.Fatal("expected Mounted=false when unmounted")
	}
}

// ── Mount / unmount ──────────────────────────────────────────────────────

func TestMemoryMountAndUnmount(t *testing.T) {
	a := &Actor{}
	a.Graph = memory.New(memory.Config{})
	if a.Graph == nil {
		t.Fatal("expected non-nil graph after mount")
	}
	_ = a.Graph.AddNode("test-1", memory.NodeTypeSession, "hello", "", 5)

	// Unmount: nil graph and persist nil.
	a.unmountMemory(nil)
	if a.Graph != nil {
		t.Fatal("expected nil graph after unmount")
	}
	if a.memoryPersisted != nil {
		t.Fatal("expected nil memoryPersisted after unmount")
	}
}

// ── Save / recall ────────────────────────────────────────────────────────

func TestMemorySaveAndRecall(t *testing.T) {
	a := &Actor{}
	a.Graph = memory.New(memory.Config{})

	// Save
	resp, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: "important fact", Layer: "session"})
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if resp.Node.ID == "" {
		t.Fatal("expected non-empty node ID")
	}
	if resp.Node.Content != "important fact" {
		t.Fatalf("expected content 'important fact', got %q", resp.Node.Content)
	}
	if resp.Node.Energy <= 0 {
		t.Fatal("expected positive energy")
	}

	// Recall by ID
	recallResp, err := a.handleMemoryRecall(nil, domain.MemoryRecallReq{Query: resp.Node.ID})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if len(recallResp.Nodes) == 0 {
		t.Fatal("expected at least 1 node")
	}
	if recallResp.Nodes[0].ID != resp.Node.ID {
		t.Fatalf("expected id %q, got %q", resp.Node.ID, recallResp.Nodes[0].ID)
	}

	// Recall by content substring
	recallResp, err = a.handleMemoryRecall(nil, domain.MemoryRecallReq{Query: "important"})
	if err != nil {
		t.Fatalf("recall by content failed: %v", err)
	}
	if len(recallResp.Nodes) == 0 {
		t.Fatal("expected at least 1 node from content search")
	}
}

func TestMemoryHotContextRecallsGraph(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	_ = a.Graph.AddNode("fact-1", memory.NodeTypeExperience, "remember this fact", "", 3)
	block := a.memoryHotContext()
	if block == nil || !strings.Contains(block.Text, "remember this fact") || !strings.Contains(block.Text, "Experience") {
		t.Fatalf("unexpected hot context: %#v", block)
	}
}

func TestDecodeSleepInstructions(t *testing.T) {
	instructions, err := decodeSleepInstructions("```json\n[{\"action\":\"promote\",\"target_id\":\"n1\"}]\n```")
	if err != nil || len(instructions) != 1 || instructions[0].Action != memory.SleepPromote || instructions[0].TargetID != "n1" {
		t.Fatalf("unexpected instructions %#v, err=%v", instructions, err)
	}
}

func TestFinishMemorySleepAppliesAtomically(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{}), memorySleeping: true}
	_ = a.Graph.AddNode("n1", memory.NodeTypeSession, "session", "", 1)
	a.finishMemorySleep(nil, `[{"action":"promote","target_id":"n1","head":"session summary","content":"session"}]`)
	if a.memorySleeping {
		t.Fatal("sleeping flag not cleared")
	}
	if got := a.Graph.Node("n1"); got == nil || got.Type != memory.NodeTypeExperience {
		t.Fatalf("node not promoted: %#v", got)
	}
}

func TestDecodeMemoryWeaveDecisions(t *testing.T) {
	// Batch format: multiple edges.
	edges := decodeMemoryWeaveDecisions("```json\n{\"edges\":[{\"decision\":\"peer\",\"target_node_id\":\"a\"},{\"decision\":\"cross\",\"target_node_id\":\"b\"}]}\n```")
	if len(edges) != 2 || edges[0].Decision != "peer" || edges[0].TargetNodeID != "a" || edges[1].Decision != "cross" || edges[1].TargetNodeID != "b" {
		t.Fatalf("batch decode failed: %+v", edges)
	}
	// Backward compat: old single-decision format.
	edges = decodeMemoryWeaveDecisions("{\"decision\":\"peer\",\"target_node_id\":\"old\"}")
	if len(edges) != 1 || edges[0].Decision != "peer" || edges[0].TargetNodeID != "old" {
		t.Fatalf("single compat decode failed: %+v", edges)
	}
	// Invalid JSON returns nil.
	edges = decodeMemoryWeaveDecisions("not json")
	if edges != nil {
		t.Fatalf("invalid response should return nil, got %+v", edges)
	}
}

func TestMemoryWeaveApply(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	_ = a.Graph.AddNode("new", memory.NodeTypeSession, "new", "", 1)
	_ = a.Graph.AddNode("old", memory.NodeTypeSession, "old", "", 1)
	if err := a.handleMemoryWeaveApply(nil, domain.MemoryWeaveApplyReq{NewNodeID: "new", Decision: "peer", TargetNodeID: "old"}); err != nil {
		t.Fatal(err)
	}
	if edges := a.Graph.Edges(); len(edges) != 1 || edges[0].Type != memory.EdgeTypePeer {
		t.Fatalf("unexpected edges: %#v", edges)
	}
}

func TestMemorySaveEmptyContent(t *testing.T) {
	a := &Actor{}
	a.Graph = memory.New(memory.Config{})
	_, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: "  "})
	if err == nil || !strings.Contains(err.Error(), "content is required") {
		t.Fatalf("expected content required error, got %v", err)
	}
}

func TestMemorySaveRejectsExperience(t *testing.T) {
	a := &Actor{}
	a.Graph = memory.New(memory.Config{})
	_, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: "test", Layer: "experience"})
	if err == nil || !strings.Contains(err.Error(), "cannot save directly") {
		t.Fatalf("expected cannot save directly error, got %v", err)
	}
}

func TestMemorySaveDefaultsToSession(t *testing.T) {
	a := &Actor{}
	a.Graph = memory.New(memory.Config{})
	resp, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: "test"})
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if resp.Node.Layer != "session" {
		t.Fatalf("expected session layer, got %q", resp.Node.Layer)
	}
}

func TestMemoryRecallByLayer(t *testing.T) {
	a := &Actor{}
	a.Graph = memory.New(memory.Config{})
	_, _ = a.handleMemorySave(nil, domain.MemorySaveReq{Content: "session note"})
	_ = a.Graph.AddNode("exp-1", memory.NodeTypeExperience, "experience note", "", 3)

	// Session only
	resp, err := a.handleMemoryRecall(nil, domain.MemoryRecallReq{Layer: "session"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	for _, n := range resp.Nodes {
		if n.Layer != "session" {
			t.Fatalf("expected only session nodes, got layer %q", n.Layer)
		}
	}
}

// ── Snapshot ─────────────────────────────────────────────────────────────

func TestMemorySnapshotContent(t *testing.T) {
	a := &Actor{}
	a.Graph = memory.New(memory.Config{})

	_, _ = a.handleMemorySave(nil, domain.MemorySaveReq{Content: "alpha"})
	_, _ = a.handleMemorySave(nil, domain.MemorySaveReq{Content: "beta"})
	nodes := a.Graph.Nodes()
	if len(nodes) >= 2 {
		_ = a.Graph.AddEdge(nodes[0].ID, nodes[1].ID, memory.EdgeTypePeer)
	}

	resp, err := a.handleMemorySnapshot(nil, domain.MemorySnapshotReq{})
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if !resp.Mounted {
		t.Fatal("expected Mounted=true")
	}
	if len(resp.Nodes) < 2 {
		t.Fatalf("expected at least 2 nodes, got %d", len(resp.Nodes))
	}
}

// ── Persistence round-trip ───────────────────────────────────────────────

func TestMemoryPersistAndRestore(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())
	a := &Actor{actorID: "memory-roundtrip"}
	a.Graph = memory.New(memory.Config{})
	_, _ = a.handleMemorySave(nil, domain.MemorySaveReq{Content: "persist me"})

	// Persist to bytes
	if err := a.persistMemorySnapshot(); err != nil {
		t.Fatalf("persistMemorySnapshot: %v", err)
	}
	if len(a.memoryPersisted) == 0 {
		t.Fatal("expected non-empty persisted data")
	}

	// Create fresh graph and restore
	a.Graph = memory.New(memory.Config{})
	if err := a.restoreMemoryFromPersisted(); err != nil {
		t.Fatalf("restoreMemoryFromPersisted: %v", err)
	}

	// Saved nodes should be restored
	recallResp, err := a.handleMemoryRecall(nil, domain.MemoryRecallReq{Query: "persist"})
	if err != nil {
		t.Fatalf("recall after restore failed: %v", err)
	}
	if len(recallResp.Nodes) == 0 {
		t.Fatal("expected nodes after restore")
	}
}

func TestMemoryUnmountClearsDurable(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	root := t.TempDir()
	config.SetDataDirForTest(root)
	a := &Actor{actorID: "memory-unmount"}
	a.Graph = memory.New(memory.Config{})
	resp, _ := a.handleMemorySave(nil, domain.MemorySaveReq{Content: "ephemeral"})
	if err := a.persistMemorySnapshot(); err != nil {
		t.Fatalf("persistMemorySnapshot: %v", err)
	}
	path := filepath.Join(root, ".actors", "agent", a.agentStoreKey(), "memory", "session", resp.Node.ID+".md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected persisted monocard: %v", err)
	}

	a.unmountMemory(nil)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected monocard deletion, stat err=%v", err)
	}
	if a.memoryPersisted != nil {
		t.Fatal("expected memoryPersisted to be nil after unmount")
	}

	// Re-mount and recall should find nothing
	a.memoryPersisted = nil
	a.Graph = memory.New(memory.Config{})
	recallResp, err := a.handleMemoryRecall(nil, domain.MemoryRecallReq{})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if len(recallResp.Nodes) != 0 {
		t.Fatal("expected no nodes after unmount/remount")
	}
}

// ── Disabled behavior ────────────────────────────────────────────────────

func TestMemoryDisabledCard(t *testing.T) {
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:mode:memory", Enabled: false},
		},
	}
	a.syncCanonicalCardRefs()
	if a.memoryEnabled() {
		t.Fatal("expected memory disabled")
	}
	// Graph should be nil even if syncMemoryMountState is called
	a.syncMemoryMountState(nil)
	if a.Graph != nil {
		t.Fatal("expected nil Graph when disabled")
	}
}

func TestMemoryEnabledCard(t *testing.T) {
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:mode:memory", Enabled: true},
		},
	}
	a.syncCanonicalCardRefs()
	if !a.memoryEnabled() {
		t.Fatal("expected memory enabled")
	}
	a.syncMemoryMountState(nil)
	if a.Graph == nil {
		t.Fatal("expected non-nil Graph when enabled")
	}
}

// ── Estimate tokens ──────────────────────────────────────────────────────

func TestEstimateTokensLocal(t *testing.T) {
	if n := estimateTokensLocal(""); n != 1 {
		t.Fatalf("expected 1 for empty, got %d", n)
	}
	if n := estimateTokensLocal("hello world"); n <= 0 {
		t.Fatalf("expected positive token count, got %d", n)
	}
	// Longer text
	long := strings.Repeat("hello world ", 100)
	if n := estimateTokensLocal(long); n <= 0 {
		t.Fatalf("expected positive token count for long text, got %d", n)
	}
}

// ── AgentStore key round-trip ─────────────────────────────────────────────

func TestMemoryPersistInPayload(t *testing.T) {
	// Verify memory fields survive JSON marshal/unmarshal in the agentStore
	// payload structure.
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "mem-persist-test-id", child: childState{Mode: false}}
	a.Graph = memory.New(memory.Config{})
	_, _ = a.handleMemorySave(nil, domain.MemorySaveReq{Content: "roundtrip test"})

	if err := a.persistMemorySnapshot(); err != nil {
		t.Fatalf("persistMemorySnapshot: %v", err)
	}
	if len(a.memoryPersisted) == 0 {
		t.Fatal("expected memory persisted data")
	}

	// Simulate what saveMailbox does.
	payload := map[string]any{}
	if a.memoryPersisted != nil {
		payload["memory"] = a.memoryPersisted
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	// Unmarshal into the same structure that loadMailbox uses.
	var restored struct {
		Memory json.RawMessage `json:"memory,omitempty"`
	}
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(restored.Memory) == 0 {
		t.Fatal("expected memory to survive JSON round-trip")
	}

	// Restore into new graph using the same durable agent key.
	a2 := &Actor{actorID: "mem-persist-test-id", child: childState{Mode: false}}
	a2.memoryPersisted = restored.Memory
	a2.Graph = memory.New(memory.Config{})
	if err := a2.restoreMemoryFromPersisted(); err != nil {
		t.Fatalf("restoreMemoryFromPersisted: %v", err)
	}

	if a2.Graph.NodeCount() == 0 {
		t.Fatal("expected nodes after restore from JSON round-trip")
	}
}

// ── memory_save rune limit boundary ──────────────────────────────────────

func TestMemorySaveRuneBoundaryASCII(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	// Exactly 500 ASCII chars.
	ok := strings.Repeat("a", 500)
	_, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: ok})
	if err != nil {
		t.Fatalf("expected OK for 500 ASCII chars, got %v", err)
	}

	// 501 ASCII chars: should be rejected.
	over := strings.Repeat("b", 501)
	_, err = a.handleMemorySave(nil, domain.MemorySaveReq{Content: over})
	if err == nil || !strings.Contains(err.Error(), "exceeds 500 characters") {
		t.Fatalf("expected 'exceeds 500 characters' error, got %v", err)
	}
}

func TestMemorySaveRuneBoundaryChinese(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	// 499 Chinese chars (each 3 bytes but 1 rune).
	chinese499 := ""
	for i := 0; i < 499; i++ {
		chinese499 += "中"
	}
	_, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: chinese499})
	if err != nil {
		t.Fatalf("expected OK for 499 Chinese chars, got %v", err)
	}

	// 500 Chinese chars: should be OK (exactly at boundary).
	chinese500 := chinese499 + "中"
	_, err = a.handleMemorySave(nil, domain.MemorySaveReq{Content: chinese500})
	if err != nil {
		t.Fatalf("expected OK for 500 Chinese chars, got %v", err)
	}

	// 501 Chinese chars: rejected.
	chinese501 := chinese500 + "中"
	_, err = a.handleMemorySave(nil, domain.MemorySaveReq{Content: chinese501})
	if err == nil || !strings.Contains(err.Error(), "exceeds 500 characters") {
		t.Fatalf("expected 'exceeds 500 characters' error, got %v", err)
	}
}

func TestMemorySaveRuneBoundaryEmoji(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	// Emoji are multi-byte but single rune. 500 emoji.
	emoji500 := strings.Repeat("😀", 500)
	_, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: emoji500})
	if err != nil {
		t.Fatalf("expected OK for 500 emoji, got %v", err)
	}

	// 501 emoji: rejected.
	emoji501 := emoji500 + "😀"
	_, err = a.handleMemorySave(nil, domain.MemorySaveReq{Content: emoji501})
	if err == nil || !strings.Contains(err.Error(), "exceeds 500 characters") {
		t.Fatalf("expected 'exceeds 500 characters' error, got %v", err)
	}
}

func TestMemorySaveTrimsSpacesBeforeCounting(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	// 500 visible chars + surrounding spaces = valid after trim.
	content := "  " + strings.Repeat("x", 500) + "  "
	_, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: content})
	if err != nil {
		t.Fatalf("expected OK after trim, got %v", err)
	}
}

// ── Touch on recall ──────────────────────────────────────────────────────

func TestMemoryRecallTouchesNodes(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	// Use explicit IDs for deterministic test.
	_ = a.Graph.AddNode("session-touch", memory.NodeTypeSession, "touch test", "", 2)
	_ = a.Graph.AddNode("exp-touch", memory.NodeTypeExperience, "pattern", "", 3)

	// Ensure time advances between operations.
	time.Sleep(10 * time.Millisecond)

	tSession := a.Graph.Node("session-touch").AccessedAt
	tExp := a.Graph.Node("exp-touch").AccessedAt

	// Recall by ID should Touch the session node.
	_, _ = a.handleMemoryRecall(nil, domain.MemoryRecallReq{Query: "session-touch"})
	after := a.Graph.Node("session-touch")
	if !after.AccessedAt.After(tSession) {
		t.Fatal("expected AccessedAt to advance after recall by ID")
	}

	time.Sleep(10 * time.Millisecond)

	// Recall by substring should Touch matched nodes.
	_, _ = a.handleMemoryRecall(nil, domain.MemoryRecallReq{Query: "touch"})
	n0 := a.Graph.Node("session-touch")
	if !n0.AccessedAt.After(after.AccessedAt) {
		t.Fatal("expected AccessedAt to advance after substring recall")
	}

	time.Sleep(10 * time.Millisecond)

	// Recall by layer should Touch experience nodes.
	_, _ = a.handleMemoryRecall(nil, domain.MemoryRecallReq{Layer: "experience"})
	e1 := a.Graph.Node("exp-touch")
	if !e1.AccessedAt.After(tExp) {
		t.Fatal("expected AccessedAt to advance after layer recall")
	}
}

// ── Memory blocks helpers ─────────────────────────────────────────────────

func TestNodeHead(t *testing.T) {
	tests := []struct {
		label  string
		expect string
	}{
		{"", ""},
		{"short", "short"},
		{strings.Repeat("a", 100), strings.Repeat("a", 60) + "..."},
		{"first line\nsecond line", "first line"},
	}
	for _, tc := range tests {
		n := &memory.Node{Label: tc.label}
		got := nodeHead(n)
		if got != tc.expect {
			t.Fatalf("nodeHead(%q) = %q, want %q", tc.label, got, tc.expect)
		}
	}
}

func TestNodesByType(t *testing.T) {
	g := memory.New(memory.Config{})
	_ = g.AddNode("s1", memory.NodeTypeSession, "session", "", 1)
	_ = g.AddNode("e1", memory.NodeTypeExperience, "experience", "", 2)
	_ = g.AddNode("o1", memory.NodeTypeOntology, "ontology", "", 3)

	sessions := nodesByType(g, memory.NodeTypeSession)
	if len(sessions) != 1 || sessions[0].ID != "s1" {
		t.Fatal("expected 1 session node")
	}
	experiences := nodesByType(g, memory.NodeTypeExperience)
	if len(experiences) != 1 || experiences[0].ID != "e1" {
		t.Fatal("expected 1 experience node")
	}
	ontologies := nodesByType(g, memory.NodeTypeOntology)
	if len(ontologies) != 1 || ontologies[0].ID != "o1" {
		t.Fatal("expected 1 ontology node")
	}
}

func TestMemoryPreHotBlocksOrder(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	_ = a.Graph.AddNode("o1", memory.NodeTypeOntology, "concept", "", 5)
	_ = a.Graph.AddNode("e1", memory.NodeTypeExperience, "pattern detected here", "", 3)

	blocks := a.memoryPreHotBlocks()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 pre-hot blocks (ontology + experience), got %d", len(blocks))
	}
	if !strings.Contains(blocks[0].Text, "## Active Memory (Ontology)") {
		t.Fatal("first block should be ontology")
	}
	if !strings.Contains(blocks[1].Text, "## Active Memory (Experience Heads)") {
		t.Fatal("second block should be experience heads")
	}
}

func TestMemoryPostHotBlocks(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	_ = a.Graph.AddNode("s1", memory.NodeTypeSession, "session note", "", 2)

	blocks := a.memoryPostHotBlocks()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 post-hot block, got %d", len(blocks))
	}
	if !strings.Contains(blocks[0].Text, "## Active Memory (Session)") {
		t.Fatal("block should be session")
	}
}

func TestMemoryHotContextBackwardCompat(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	_ = a.Graph.AddNode("o1", memory.NodeTypeOntology, "concept", "", 5)
	_ = a.Graph.AddNode("e1", memory.NodeTypeExperience, "pattern", "", 3)

	block := a.memoryHotContext()
	if block == nil {
		t.Fatal("expected non-nil backward compat block")
	}
	if !strings.Contains(block.Text, "Ontology") || !strings.Contains(block.Text, "Experience") {
		t.Fatal("backward compat block should contain both ontology and experience")
	}
}

func TestMemoryHotContextNilWhenEmpty(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	if block := a.memoryHotContext(); block != nil {
		t.Fatal("expected nil when graph is empty")
	}
}

func TestMemoryPostHotBlocksNilWhenEmpty(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	if blocks := a.memoryPostHotBlocks(); len(blocks) != 0 {
		t.Fatal("expected empty when no session nodes")
	}
}

func TestMemoryPreHotBlocksNilWhenUnmounted(t *testing.T) {
	a := &Actor{}
	if blocks := a.memoryPreHotBlocks(); len(blocks) != 0 {
		t.Fatal("expected empty when unmounted")
	}
}

func TestAppendMemoryBaseAddsOntologyToInstructions(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	_ = a.Graph.AddNode("o1", memory.NodeTypeOntology, "core concept", "", 5)
	_ = a.Graph.AddNode("e1", memory.NodeTypeExperience, "pattern", "short head", 3)

	inst := &domain.CompiledInstructions{Base: []string{"role prompt"}}
	a.appendMemoryBase(inst)

	if len(inst.Base) != 2 {
		t.Fatalf("expected 2 base entries (ontology + role), got %d: %v", len(inst.Base), inst.Base)
	}
	if !strings.Contains(inst.Base[0], "Active Memory (Ontology)") {
		t.Fatalf("first base entry should be ontology (prepended), got %q", inst.Base[0])
	}
	if inst.Base[1] != "role prompt" {
		t.Fatalf("original base should be preserved after ontology, got %q", inst.Base[1])
	}

	// Experience goes to Resolved tail, not Base.
	a.appendMemoryExperience(inst)
	if len(inst.Resolved) != 1 {
		t.Fatalf("expected 1 resolved entry (experience), got %d: %v", len(inst.Resolved), inst.Resolved)
	}
	if !strings.Contains(inst.Resolved[0], "Active Memory (Experience Heads)") {
		t.Fatalf("resolved entry should be experience heads, got %q", inst.Resolved[0])
	}
}

func TestAppendMemoryBaseNoopWhenUnmounted(t *testing.T) {
	a := &Actor{}
	inst := &domain.CompiledInstructions{Base: []string{"role"}}
	a.appendMemoryBase(inst)
	if len(inst.Base) != 1 {
		t.Fatalf("expected unchanged base when unmounted, got %v", inst.Base)
	}
}

func TestAppendMemoryBaseNilSafe(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	a.appendMemoryBase(nil)
}

// TestMemoryPreHotBlocksRespectsCaps verifies the agent-layer injection
// truncates to the per-layer injection cap and is ranked by RecallScore
// (highest-energy / first-added nodes first when AccessedAt is equal).
func TestMemoryPreHotBlocksRespectsCaps(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{InjectOntologyCap: 2, InjectExperienceCap: 1})}
	o1 := a.Graph.AddNode("o1", memory.NodeTypeOntology, "highest", "", 0)
	o2 := a.Graph.AddNode("o2", memory.NodeTypeOntology, "mid", "", 0)
	o3 := a.Graph.AddNode("o3", memory.NodeTypeOntology, "lowest", "", 0)
	if o1 == nil || o2 == nil || o3 == nil {
		t.Fatal("AddNode returned nil")
	}
	a.Graph.AddNode("e1", memory.NodeTypeExperience, "exp", "", 0)

	blocks := a.memoryPreHotBlocks()
	var ontologyBlock string
	for _, b := range blocks {
		if strings.Contains(b.Text, "Ontology") {
			ontologyBlock = b.Text
		}
	}
	if ontologyBlock == "" {
		t.Fatalf("expected an ontology block, got %#v", blocks)
	}
	// Cap is 2: exactly two ontology entries, ranked o1 then o2; o3 excluded.
	for _, want := range []string{"highest", "mid"} {
		if !strings.Contains(ontologyBlock, want) {
			t.Fatalf("ontology block missing ranked entry %q: %s", want, ontologyBlock)
		}
	}
	if strings.Contains(ontologyBlock, "lowest") {
		t.Fatalf("lowest-energy node should be excluded by cap: %s", ontologyBlock)
	}
	// Verify the rank order: o1 line must appear before o2 line.
	if strings.Index(ontologyBlock, "highest") > strings.Index(ontologyBlock, "mid") {
		t.Fatalf("ontology block not ranked by score (highest should precede mid): %s", ontologyBlock)
	}
}

// TestTouchProjectedMemoryOnlyTouchesInjected verifies that only the injected
// (capped) nodes get their AccessedAt refreshed; non-injected nodes keep their
// original AccessedAt. This proves the antagonist-siphon side effect no longer
// fires on the whole graph every turn.
func TestTouchProjectedMemoryOnlyTouchesInjected(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{InjectSessionCap: 1})}
	// Two session nodes; first-added has higher energy → injected, second is excluded by the cap.
	hi := a.Graph.AddNode("hi", memory.NodeTypeSession, "kept", "", 0)
	lo := a.Graph.AddNode("lo", memory.NodeTypeSession, "dropped", "", 0)
	if hi == nil || lo == nil {
		t.Fatal("AddNode returned nil")
	}

	hiBefore := a.Graph.Node("hi").AccessedAt
	loBefore := a.Graph.Node("lo").AccessedAt
	// Ensure a measurable gap so AccessedAt advancement is detectable.
	time.Sleep(2 * time.Millisecond)

	a.touchProjectedMemory()

	hiAfter := a.Graph.Node("hi").AccessedAt
	loAfter := a.Graph.Node("lo").AccessedAt

	if !hiAfter.After(hiBefore) {
		t.Fatalf("injected (top-ranked) node should have AccessedAt refreshed: before=%s after=%s", hiBefore, hiAfter)
	}
	if !loAfter.Equal(loBefore) {
		t.Fatalf("non-injected node AccessedAt must NOT change: before=%s after=%s", loBefore, loAfter)
	}
}

// TestMemoryInjectionBounded verifies total injection is bounded: more nodes
// than every cap never expand the injected block beyond the cap.
func TestMemoryInjectionBounded(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{InjectSessionCap: 3})}
	for i := 0; i < 10; i++ {
		a.Graph.AddNode(memory.NodeID("s"+string(rune('a'+i))), memory.NodeTypeSession, "session note", "", 0)
	}
	blocks := a.memoryPostHotBlocks()
	if len(blocks) != 1 {
		t.Fatalf("expected one session block, got %d", len(blocks))
	}
	// Each node contributes one "- [id]" entry; cap is 3 so the block must hold exactly 3.
	if got := strings.Count(blocks[0].Text, "- ["); got != 3 {
		t.Fatalf("session block should be capped at 3 entries, got %d: %s", got, blocks[0].Text)
	}
}
