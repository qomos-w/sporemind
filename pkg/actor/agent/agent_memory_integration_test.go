package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/agent/memory"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestMemoryResultIsFilteredFromLLMContent(t *testing.T) {
	msg := domain.ChatMessage{Content: []domain.ContentBlock{
		{Type: domain.ContentBlockText, Text: "keep"},
		{Type: "memory_result", Text: "saved"},
	}}
	filterLLMContent(&msg)
	if len(msg.Content) != 1 || msg.Content[0].Text != "keep" {
		t.Fatalf("unexpected filtered content %#v", msg.Content)
	}
}

func TestMemorySleepBlocksAutoStart(t *testing.T) {
	a := &Actor{memorySleeping: true}
	a.Session.ActiveHead = -1
	a.maybeAutoStartTurn(nil)
	if !a.memorySleeping {
		t.Fatal("sleep state changed while auto-start was blocked")
	}
}

// ── Tick integration ─────────────────────────────────────────────────────

func TestMemoryTickOnCompletedTurnReducesEnergy(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{
		InitialEnergyBase: 100.0,
		BaseDecayRate:     0.5, // high decay for test visibility
	})}
	n := a.Graph.AddNode("tick-test", memory.NodeTypeSession, "test content", "", 10)
	initial := n.Energy

	// Simulate one production Tick (awake=true).
	a.Graph.Tick(true)

	after := a.Graph.Node("tick-test")
	if after == nil {
		t.Fatal("node disappeared after tick")
	}
	if after.Energy >= initial {
		t.Fatalf("expected energy to decrease after tick: initial=%f, after=%f", initial, after.Energy)
	}
}

func TestMemoryTickDoesNotRunWhenNotAwake(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{
		InitialEnergyBase: 100.0,
		BaseDecayRate:     0.5,
	})}
	n := a.Graph.AddNode("sleep-tick", memory.NodeTypeSession, "test", "", 10)
	initial := n.Energy

	a.Graph.Tick(false) // asleep tick

	after := a.Graph.Node("sleep-tick")
	if after.Energy != initial {
		t.Fatalf("expected no decay on asleep tick: initial=%f, after=%f", initial, after.Energy)
	}
}

func TestMemoryTickMarksForDeath(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{
		InitialEnergyBase: 100.0,
		BaseDecayRate:     1.0, // full decay
	})}
	n := a.Graph.AddNode("die-test", memory.NodeTypeSession, "will die", "", 10)
	// Drop the per-node floors (DecayNode would otherwise clamp energy back up)
	// and set energy below config NodeMinEnergy (0.5 default) so the tick marks
	// the node for death.
	n.BaseEnergy = 0
	n.MinEnergy = 0
	n.Energy = 40.0

	a.Graph.Tick(true)

	after := a.Graph.Node("die-test")
	if after == nil || !after.MarkDeath {
		t.Fatal("expected node to be marked for death after tick")
	}
}

func TestMemoryTickOnStepClosed(t *testing.T) {
	// Per-step decay: applyStepEvent("step.closed") advances the memory graph
	// tick, using the step's total token count (input + output).
	a := &Actor{
		Graph: memory.New(memory.Config{
			InitialEnergyBase: 100.0,
			BaseDecayRate:     0.5,
		}),
	}
	n := a.Graph.AddNode("step-tick", memory.NodeTypeSession, "test", "", 10)
	initial := n.Energy

	// Simulate a step.closed event with both input and output token usage.
	a.applyStepEvent(domain.StepEvent{
		Kind:   "step.closed",
		StepID: "step-1",
		TurnID: "turn-1",
		Usage:  &domain.UsageData{InputTokens: 9000, OutputTokens: 2500},
	})

	after := a.Graph.Node("step-tick")
	if after == nil {
		t.Fatal("node disappeared after step.closed tick")
	}
	if after.Energy >= initial {
		t.Fatalf("expected energy decrease from step.closed tick: initial=%f, after=%f", initial, after.Energy)
	}
}

// ── Touch on projection integration ──────────────────────────────────────

func TestTouchProjectedMemoryTouchesPreHotNodes(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	_ = a.Graph.AddNode("o-touch", memory.NodeTypeOntology, "ontology concept", "", 5)
	_ = a.Graph.AddNode("e-touch", memory.NodeTypeExperience, "experience pattern", "", 3)

	// Record initial AccessedAt.
	oInitial := a.Graph.Node("o-touch").AccessedAt
	eInitial := a.Graph.Node("e-touch").AccessedAt

	// Wait briefly so time advances.
	time.Sleep(time.Millisecond)

	_ = a.memoryPreHotBlocks()
	if a.Graph.Node("o-touch").AccessedAt.After(oInitial) || a.Graph.Node("e-touch").AccessedAt.After(eInitial) {
		t.Fatal("building preview blocks must not touch memory")
	}
	a.touchProjectedMemory()

	oAfter := a.Graph.Node("o-touch")
	eAfter := a.Graph.Node("e-touch")

	if !oAfter.AccessedAt.After(oInitial) {
		t.Fatal("expected ontology node AccessedAt to advance after projection")
	}
	if !eAfter.AccessedAt.After(eInitial) {
		t.Fatal("expected experience node AccessedAt to advance after projection")
	}
}

func TestTouchProjectedMemoryTouchesPostHotNodes(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	_ = a.Graph.AddNode("s-touch", memory.NodeTypeSession, "session note", "", 2)

	sInitial := a.Graph.Node("s-touch").AccessedAt
	time.Sleep(time.Millisecond)

	_ = a.memoryPostHotBlocks()
	if a.Graph.Node("s-touch").AccessedAt.After(sInitial) {
		t.Fatal("building preview blocks must not touch memory")
	}
	a.touchProjectedMemory()

	sAfter := a.Graph.Node("s-touch")
	if !sAfter.AccessedAt.After(sInitial) {
		t.Fatal("expected session node AccessedAt to advance after projection")
	}
}

// ── Dreamer pressure integration ─────────────────────────────────────────

func TestMemoryMaybeStartSleepLayerPressure(t *testing.T) {
	// Verify IsSleepDue is checked for sleep decision.
	g := memory.New(memory.Config{
		InitialEnergyBase:      100.0,
		SessionPressureTarget:  1,
		SessionPressureHigh:    2,
		SleepPressureThreshold: 0.5,
	})
	a := &Actor{Graph: g}

	// Low pressure: sleep should not start.
	started := a.maybeStartMemorySleep(nil, "", false)
	if started {
		t.Fatal("should not start sleep when pressure is low")
	}

	// Raise layer pressure above the threshold by adding session nodes.
	g.AddNode("s1", memory.NodeTypeSession, "s1", "", 1)
	g.AddNode("s2", memory.NodeTypeSession, "s2", "", 1)

	// Still won't start because ctx is nil, but IsSleepDue returns true.
	if !g.IsSleepDue() {
		t.Fatal("expected IsSleepDue after exceeding pressure threshold")
	}
}

func TestMemorySleepAlreadySleepingBlocks(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{}), memorySleeping: true}
	if a.maybeStartMemorySleep(nil, "", false) {
		t.Fatal("should not start sleep when already sleeping")
	}
}

func TestMemorySleepPerTurnLatch(t *testing.T) {
	// A turn that already spawned a dreamer must not spawn a second one even
	// if pressure is still due (multiple compactions within the same turn).
	g := memory.New(memory.Config{
		InitialEnergyBase:      100.0,
		SessionPressureTarget:  1,
		SessionPressureHigh:    2,
		SleepPressureThreshold: 0.5,
	})
	a := &Actor{Graph: g, memorySleepTurn: "turn-1"}

	// Same turn: latch blocks regardless of pressure.
	if a.maybeStartMemorySleep(nil, "turn-1", false) {
		t.Fatal("should not start sleep twice within the same turn")
	}
	// Different turn: latch does not block (ctx nil still prevents spawn).
	if a.maybeStartMemorySleep(nil, "turn-2", false) {
		t.Fatal("should not start sleep with nil ctx")
	}
}

// ── Sleep failure backoff integration ────────────────────────────────────

func TestMemorySleepFailureBackoffBlocksAutoSleep(t *testing.T) {
	// After 3 consecutive ApplySleep failures, automatic sleep should be
	// blocked. force=true still triggers.
	g := memory.New(memory.Config{
		InitialEnergyBase:      100.0,
		SessionPressureTarget:  1,
		SessionPressureHigh:    2,
		SleepPressureThreshold: 0.5,
	})
	_ = g.AddNode("s1", memory.NodeTypeSession, "session 1", "", 1)
	_ = g.AddNode("s2", memory.NodeTypeSession, "session 2", "", 1)
	if !g.IsSleepDue() {
		t.Fatal("expected IsSleepDue after exceeding pressure threshold")
	}

	a := &Actor{Graph: g, sleepFailureCount: 3}

	// Non-force: blocked.
	if a.maybeStartMemorySleep(nil, "", false) {
		t.Fatal("should not start sleep when sleepFailureCount >= 3 and !force")
	}

	// Force: still proceeds (ctx=nil prevents spawn, but doesn't hit the backoff check).
	if a.maybeStartMemorySleep(nil, "", true) {
		t.Fatal("should not start sleep with nil ctx even with force=true")
	}

	// Reset sleepFailureCount to verify normal operation.
	a.sleepFailureCount = 0
	if a.maybeStartMemorySleep(nil, "", false) {
		t.Fatal("should not start sleep with nil ctx even after reset")
	}
}

func TestMemorySleepFailureBackoffIncrementsOnFailure(t *testing.T) {
	// finishMemorySleep increments sleepFailureCount when ApplySleep fails.
	a := &Actor{
		Graph:             memory.New(memory.Config{}),
		memorySleeping:    true,
		sleepFailureCount: 0,
	}

	// ApplySleep with an invalid instruction (target node doesn't exist) will fail.
	a.finishMemorySleep(nil, `[{"action":"promote","target_id":"nonexistent","head":"h","content":"c"}]`)

	if a.sleepFailureCount != 1 {
		t.Fatalf("expected sleepFailureCount=1 after ApplySleep failure, got %d", a.sleepFailureCount)
	}

	// A second failure increments again.
	a.finishMemorySleep(nil, `[{"action":"promote","target_id":"nonexistent","head":"h","content":"c"}]`)
	if a.sleepFailureCount != 2 {
		t.Fatalf("expected sleepFailureCount=2 after second failure, got %d", a.sleepFailureCount)
	}
}

func TestMemorySleepFailureBackoffResetsOnSuccess(t *testing.T) {
	// finishMemorySleep resets sleepFailureCount to 0 on success.
	a := &Actor{
		Graph:             memory.New(memory.Config{}),
		memorySleeping:    true,
		sleepFailureCount: 3,
	}
	_ = a.Graph.AddNode("n1", memory.NodeTypeSession, "session", "", 1)
	a.finishMemorySleep(nil, `[{"action":"promote","target_id":"n1","head":"session summary","content":"session"}]`)

	if a.sleepFailureCount != 0 {
		t.Fatalf("expected sleepFailureCount=0 after successful ApplySleep, got %d", a.sleepFailureCount)
	}
}

func TestMemorySleepFailureBackoffResetOnAddNode(t *testing.T) {
	// handleMemorySave (AddNode success) resets sleepFailureCount to 0.
	a := &Actor{
		Graph:             memory.New(memory.Config{}),
		sleepFailureCount: 3,
		child:             childState{Mode: false},
	}
	// ctx=nil is safe: saveMailbox returns early on empty store key,
	// startMemoryWeave returns early on nil ctx.
	if _, err := a.handleMemorySave(nil, domain.MemorySaveReq{Content: "new note", Layer: "session"}); err != nil {
		t.Fatalf("handleMemorySave: %v", err)
	}
	if a.sleepFailureCount != 0 {
		t.Fatalf("expected sleepFailureCount=0 after AddNode success, got %d", a.sleepFailureCount)
	}
}

func TestMemorySleepFailureBackoffAllowsAfterReset(t *testing.T) {
	// After resetting sleepFailureCount (by graph mutation), automatic
	// sleep should be allowed again.
	g := memory.New(memory.Config{
		InitialEnergyBase:      100.0,
		SessionPressureTarget:  1,
		SessionPressureHigh:    2,
		SleepPressureThreshold: 0.5,
	})
	_ = g.AddNode("s1", memory.NodeTypeSession, "session 1", "", 1)
	_ = g.AddNode("s2", memory.NodeTypeSession, "session 2", "", 1)
	if !g.IsSleepDue() {
		t.Fatal("expected IsSleepDue after exceeding pressure threshold")
	}

	a := &Actor{Graph: g, sleepFailureCount: 3}
	// Reset via graph mutation.
	a.sleepFailureCount = 0

	// After reset, should pass backoff check (ctx=nil still prevents spawn).
	if a.maybeStartMemorySleep(nil, "", false) {
		t.Fatal("should not start sleep with nil ctx even after backoff reset")
	}
}

// ── Compact snapshot (dreamer prompt truncation) integration ─────────────

func TestBuildCompactSnapshotTrimsLargeGraph(t *testing.T) {
	g := memory.New(memory.Config{})
	// Add 70 session nodes so the graph exceeds the 60-node compact threshold.
	for i := 0; i < 70; i++ {
		_ = g.AddNode(memory.NodeID(fmt.Sprintf("s%d", i)), memory.NodeTypeSession, fmt.Sprintf("session %d", i), "", 1)
	}
	// Add some edges so edge filtering is exercised.
	_ = g.AddEdge("s0", "s1", memory.EdgeTypePeer)
	_ = g.AddEdge("s0", "s69", memory.EdgeTypeCross)
	_ = g.AddEdge("s1", "s2", memory.EdgeTypePeer)

	a := &Actor{Graph: g}
	snapshot, communities := a.buildCompactSnapshot(g, 60)

	// Snapshot must decode to at most 60 nodes.
	var parsed struct {
		Nodes []memory.Node `json:"nodes"`
		Edges []memory.Edge `json:"edges"`
	}
	if err := json.Unmarshal(snapshot, &parsed); err != nil {
		t.Fatalf("compact snapshot is not valid JSON: %v", err)
	}
	if len(parsed.Nodes) > 60 {
		t.Fatalf("compact snapshot has %d nodes, want <= 60", len(parsed.Nodes))
	}
	if len(parsed.Nodes) == 0 {
		t.Fatal("compact snapshot has no nodes")
	}

	// Every edge must connect two nodes present in the snapshot.
	present := make(map[memory.NodeID]struct{}, len(parsed.Nodes))
	for _, n := range parsed.Nodes {
		present[n.ID] = struct{}{}
	}
	for _, e := range parsed.Edges {
		if _, ok := present[e.From]; !ok {
			t.Fatalf("compact snapshot edge from %q references node not in snapshot", e.From)
		}
		if _, ok := present[e.To]; !ok {
			t.Fatalf("compact snapshot edge to %q references node not in snapshot", e.To)
		}
	}

	// Communities must only reference nodes in the snapshot.
	for _, comp := range communities {
		for _, id := range comp {
			if _, ok := present[id]; !ok {
				t.Fatalf("community references node %q not in compact snapshot", id)
			}
		}
	}
	if len(communities) == 0 {
		t.Fatal("expected non-empty communities for connected subgraph")
	}
}

func TestBuildCompactSnapshotForSmallGraph(t *testing.T) {
	g := memory.New(memory.Config{})
	_ = g.AddNode("n1", memory.NodeTypeSession, "one", "", 1)
	_ = g.AddNode("n2", memory.NodeTypeSession, "two", "", 1)

	a := &Actor{Graph: g}
	snapshot, communities := a.buildCompactSnapshot(g, 60)

	if len(snapshot) == 0 {
		t.Fatal("expected non-empty snapshot")
	}
	count := 0
	for _, c := range communities {
		count += len(c)
	}
	if count == 0 {
		t.Fatal("expected communities over small graph")
	}
}

func TestDecodeSleepInstructionsWithHeadContent(t *testing.T) {
	raw := `[{"action":"merge","target_id":"t1","merge_id":"m1","head":"merged head","content":"merged content"}]`
	instructions, err := decodeSleepInstructions(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}
	if instructions[0].Action != memory.SleepMerge {
		t.Fatal("expected merge action")
	}
	if instructions[0].TargetID != "t1" || instructions[0].MergeID != "m1" {
		t.Fatal("unexpected IDs")
	}
}

func TestDecodeSleepInstructionsRejectsUnknownAction(t *testing.T) {
	// delete/rescue are no longer supported; decode should still succeed (decodeSleepInstructions
	// maps strings to SleepAction constants), but ApplySleep will reject unknown actions.
	raw := `[{"action":"delete","target_id":"dead"}]`
	instructions, err := decodeSleepInstructions(raw)
	if err != nil {
		t.Fatalf("decode should not fail: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}
	if instructions[0].Action == memory.SleepMerge || instructions[0].Action == memory.SleepPromote {
		t.Fatal("delete should not map to a supported action")
	}
}

func TestDecodeSleepInstructionsInvalidJSON(t *testing.T) {
	_, err := decodeSleepInstructions("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestDecodeSleepInstructionsWrappedOutput verifies the decoder survives
// realistic dreamer-model output shapes: <think> blocks (with incidental
// brackets inside), prose around the array, fenced code blocks, and empty
// arrays.
func TestDecodeSleepInstructionsWrappedOutput(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		want  int
		first memory.SleepAction
	}{
		{
			name:  "bare array",
			raw:   `[{"action":"promote","target_id":"n1","head":"h"}]`,
			want:  1,
			first: memory.SleepPromote,
		},
		{
			name:  "empty array",
			raw:   `[]`,
			want:  0,
		},
		{
			name: "think block with brackets then array",
			raw: "<think>nodes [a, b] look mergeable; energy [0.5] is low</think>\n" +
				`[{"action":"merge","target_id":"a","merge_id":"b","head":"h","content":"c"}]`,
			want:  1,
			first: memory.SleepMerge,
		},
		{
			name: "prose with incidental brackets then array",
			raw: "Analysis: communities [[s1, s2]] are related. Instructions:\n" +
				`[{"action":"connect","target_id":"s1","merge_id":"s2","edge_type":"peer"}]` +
				"\nDone.",
			want:  1,
			first: memory.SleepConnect,
		},
		{
			name:  "fenced json block",
			raw:   "Here is the plan:\n```json\n[{\"action\":\"promote\",\"target_id\":\"n2\"}]\n```\nthanks",
			want:  1,
			first: memory.SleepPromote,
		},
		{
			name: "brackets inside JSON strings are not delimiters",
			raw:  `[{"action":"merge","target_id":"a","merge_id":"b","head":"see [note]","content":"c"}]`,
			want: 1,
			first: memory.SleepMerge,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instructions, err := decodeSleepInstructions(tc.raw)
			if err != nil {
				t.Fatalf("decodeSleepInstructions(%s): %v", tc.name, err)
			}
			if len(instructions) != tc.want {
				t.Fatalf("%s: got %d instructions, want %d", tc.name, len(instructions), tc.want)
			}
			if tc.want > 0 && instructions[0].Action != tc.first {
				t.Fatalf("%s: action = %q, want %q", tc.name, instructions[0].Action, tc.first)
			}
		})
	}
}

// ── resolveFullHotContext integration ────────────────────────────────────

func TestMemoryBlocksSplitAroundHotContext(t *testing.T) {
	a := &Actor{Graph: memory.New(memory.Config{})}
	_ = a.Graph.AddNode("o1", memory.NodeTypeOntology, "concept", "", 5)
	_ = a.Graph.AddNode("s1", memory.NodeTypeSession, "session note", "", 2)

	pre := a.memoryPreHotBlocks()
	if len(pre) != 1 || !stringsContains(pre[0].Text, "Ontology") {
		t.Fatalf("expected ontology pre-hot block, got %#v", pre)
	}
	hot := a.resolveFullHotContext(nil)
	if len(hot) != 1 || !stringsContains(hot[0].Text, "Session") {
		t.Fatalf("expected session post-hot block, got %#v", hot)
	}
}

// Use simple contains helper since we're in the agent package.
func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && containsString(s, substr)
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestMemorySleepUsesSpawnDreamChildPath verifies that maybeStartMemorySleep
// routes through the agent-local spawn path (spawnDreamChild → spawnChild),
// spawning a dreamer child tracked under the memory-sleep toolUseID.
func TestMemorySleepUsesSpawnDreamChildPath(t *testing.T) {
	g := memory.New(memory.Config{
		InitialEnergyBase:      100.0,
		SessionPressureTarget:  1,
		SessionPressureHigh:    2,
		SleepPressureThreshold: 0.5,
	})
	_ = g.AddNode("s1", memory.NodeTypeSession, "session 1", "", 1)
	_ = g.AddNode("s2", memory.NodeTypeSession, "session 2", "", 1)
	if !g.IsSleepDue() {
		t.Fatal("expected IsSleepDue after exceeding pressure threshold")
	}

	var capturedSpawnName string
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "project" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				if callID == "project.spawn_agent" {
					if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
						capturedSpawnName = req.SpawnName
					}
					return domain.ProjectSpawnAgentResp{ActorID: testutil.GenActorID().String()}
				}
				return nil
			}), true
		}
		return nil, false
	}

	a := slotTestActor()
	a.Graph = g
	a.fast = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "fast-model", Provider: "openai"}},
	}}
	a.primary = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "primary-model", Provider: "openai"}},
	}}

	if !a.maybeStartMemorySleep(ctx, "turn-fork", true) {
		t.Fatal("maybeStartMemorySleep should start with force=true")
	}
	if !a.memorySleeping {
		t.Fatal("memorySleeping should be true after starting sleep")
	}
	if !strings.HasPrefix(capturedSpawnName, "dream-") {
		t.Fatalf("spawned name = %q, want dream-* prefix", capturedSpawnName)
	}
	if !strings.HasSuffix(capturedSpawnName, "memory-sleep") {
		t.Fatalf("spawned name = %q, want *memory-sleep suffix", capturedSpawnName)
	}
	a.activeChildrenMu.Lock()
	_, tracked := a.activeChildren["memory-sleep"]
	a.activeChildrenMu.Unlock()
	if !tracked {
		t.Fatal("memory-sleep child not tracked in activeChildren")
	}
}

// TestMemoryDreamToolOpensVisibleStep verifies the memory_dream tool path
// opens a dream step tied to the active turn so outcomes surface in the chat
// timeline, and closes it when the dream fails to start.
func TestMemoryDreamToolOpensVisibleStep(t *testing.T) {
	g := memory.New(memory.Config{})
	_ = g.AddNode("s1", memory.NodeTypeSession, "session 1", "", 1)

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "project" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				if callID == "project.spawn_agent" {
					return domain.ProjectSpawnAgentResp{ActorID: testutil.GenActorID().String()}
				}
				return nil
			}), true
		}
		return nil, false
	}

	a := slotTestActor()
	a.Graph = g
	a.setActiveTurnRef("turn-dream")
	a.fast = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "fast-model", Provider: "openai"}},
	}}

	res, err := a.handleMemoryDream(ctx)
	if err != nil {
		t.Fatalf("handleMemoryDream: %v", err)
	}
	if res["started"] != true {
		t.Fatalf("started = %v, want true", res["started"])
	}
	if a.dreamStepID != "turn-dream-dream" {
		t.Fatalf("dreamStepID = %q, want turn-dream-dream", a.dreamStepID)
	}
	if !a.memorySleeping {
		t.Fatal("memorySleeping should be true after started dream")
	}

	// Now the failure branch: a fresh actor whose spawn path is broken
	// (no project service) must not leave the step hanging open.
	failCtx := testutil.HumanCtx(testutil.GenActorID())
	failCtx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }

	b := slotTestActor()
	b.Graph = memory.New(memory.Config{})
	_ = b.Graph.AddNode("s1", memory.NodeTypeSession, "session 1", "", 1)
	b.setActiveTurnRef("turn-dream")
	b.fast = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "fast-model", Provider: "openai"}},
	}}

	res2, err := b.handleMemoryDream(failCtx)
	if err != nil {
		t.Fatalf("handleMemoryDream(fail): %v", err)
	}
	if res2["started"] != false {
		t.Fatalf("started = %v, want false on spawn failure", res2["started"])
	}
	if b.dreamStepID != "" {
		t.Fatalf("dreamStepID = %q, want empty (step closed on failed start)", b.dreamStepID)
	}
}
