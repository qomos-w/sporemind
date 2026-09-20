package memory

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

// ── Helpers ──────────────────────────────────────────────────────────────

func newTestGraph() *MemoryGraph {
	return New(DefaultConfig())
}

func addNode(g *MemoryGraph, id NodeID, typ NodeType, label string, tokens int) *Node {
	n := g.AddNode(id, typ, label, "", tokens)
	if n == nil {
		panic("addNode returned nil (duplicate?)")
	}
	return n
}

func mustFloat(t *testing.T, got, want, tol float64) {
	t.Helper()
	if got < want-tol || got > want+tol {
		t.Errorf("mismatch: got %.4f, want %.4f (±%.4f)", got, want, tol)
	}
}

// ── Config ───────────────────────────────────────────────────────────────

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.InitialEnergyBase <= 0 {
		t.Error("InitialEnergyBase should be positive")
	}
	if c.MaxRecallBudget <= 0 {
		t.Error("MaxRecallBudget should be positive")
	}
	if c.NodeBaseEnergy <= 0 {
		t.Error("NodeBaseEnergy should be positive")
	}
	if c.NodeMinEnergy <= 0 {
		t.Error("NodeMinEnergy should be positive")
	}
	if c.SleepPressureThreshold <= 0 {
		t.Error("SleepPressureThreshold should be positive")
	}
}

func TestZeroConfigUsesDefaults(t *testing.T) {
	g := New(Config{})
	if g.cfg.InitialEnergyBase != DefaultConfig().InitialEnergyBase {
		t.Error("zero config should be replaced with defaults")
	}
}

// TestPartialConfigFillsScalarDefaults verifies that a partially-specified
// Config (some fields non-zero, the rest zero) still fills every thermodynamic
// scalar with its default. Without per-field fill, SleepPressureThreshold=0
// would make sleep always due. NodeBaseEnergy is the exception: 0 is a legal
// zero value (DecayNode no longer clamps to BaseEnergy) and is preserved.
func TestPartialConfigFillsScalarDefaults(t *testing.T) {
	// Only set one non-zero field; everything else should be filled.
	g := New(Config{SleepPressureThreshold: 123})
	def := DefaultConfig()

	if g.cfg.SleepPressureThreshold != 123 {
		t.Errorf("explicit field overridden: SleepPressureThreshold=%v want 123", g.cfg.SleepPressureThreshold)
	}
	// NodeBaseEnergy=0 is a legal value and must not be replaced by the default.
	if g.cfg.NodeBaseEnergy != 0 {
		t.Errorf("NodeBaseEnergy=0 must be preserved, got %v", g.cfg.NodeBaseEnergy)
	}
	// All other thermodynamic scalars must take defaults, not zero.
	for _, tc := range []struct {
		name string
		got  float64
		want float64
	}{
		{"InitialEnergyBase", g.cfg.InitialEnergyBase, def.InitialEnergyBase},
		{"InitialSuppressionK", g.cfg.InitialSuppressionK, def.InitialSuppressionK},
		{"BaseDecayRate", g.cfg.BaseDecayRate, def.BaseDecayRate},
		{"TopologyProtectionK", g.cfg.TopologyProtectionK, def.TopologyProtectionK},
		{"ConnectionEnergy", g.cfg.ConnectionEnergy, def.ConnectionEnergy},
		{"NodeMinEnergy", g.cfg.NodeMinEnergy, def.NodeMinEnergy},
		{"SessionBaseTemp", g.cfg.SessionBaseTemp, def.SessionBaseTemp},
		{"SessionGrowthK", g.cfg.SessionGrowthK, def.SessionGrowthK},
		{"ExperienceBaseTemp", g.cfg.ExperienceBaseTemp, def.ExperienceBaseTemp},
		{"ExperienceGrowthK", g.cfg.ExperienceGrowthK, def.ExperienceGrowthK},
		{"OntologyBaseTemp", g.cfg.OntologyBaseTemp, def.OntologyBaseTemp},
		{"OntologyGrowthK", g.cfg.OntologyGrowthK, def.OntologyGrowthK},
		{"SiphonEnergy", g.cfg.SiphonEnergy, def.SiphonEnergy},
		{"MergeEnergyTransfer", g.cfg.MergeEnergyTransfer, def.MergeEnergyTransfer},
	} {
		if tc.got != tc.want {
			t.Errorf("%s not filled: got %v want %v", tc.name, tc.got, tc.want)
		}
	}
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"MaxRecallBudget", g.cfg.MaxRecallBudget, def.MaxRecallBudget},
		{"InjectOntologyCap", g.cfg.InjectOntologyCap, def.InjectOntologyCap},
		{"InjectExperienceCap", g.cfg.InjectExperienceCap, def.InjectExperienceCap},
		{"InjectSessionCap", g.cfg.InjectSessionCap, def.InjectSessionCap},
	} {
		if tc.got != tc.want {
			t.Errorf("%s not filled: got %d want %d", tc.name, tc.got, tc.want)
		}
	}
	if g.cfg.TokenQuantum != def.TokenQuantum {
		t.Errorf("TokenQuantum not filled: got %d want %d", g.cfg.TokenQuantum, def.TokenQuantum)
	}
}

// ── AddNode ──────────────────────────────────────────────────────────────

func TestAddNodeWithGeneratedID(t *testing.T) {
	g := newTestGraph()
	n := addNode(g, "", NodeTypeSession, "test", 10)
	if n.ID == "" {
		t.Fatal("generated ID should not be empty")
	}
	if g.NodeCount() != 1 {
		t.Fatal("expected 1 node")
	}
}

func TestAddNodeWithDeterministicID(t *testing.T) {
	g := newTestGraph()
	id := NodeID("my-node-1")
	n := addNode(g, id, NodeTypeExperience, "deterministic", 5)
	if n.ID != id {
		t.Fatalf("expected id %q, got %q", id, n.ID)
	}
}

func TestAddNodeDuplicateID(t *testing.T) {
	g := newTestGraph()
	addNode(g, "dup", NodeTypeSession, "first", 0)
	dup := g.AddNode("dup", NodeTypeSession, "second", "", 0)
	if dup != nil {
		t.Fatal("duplicate add should return nil")
	}
}

func TestAddNodeCountSuppressedEnergy(t *testing.T) {
	g := newTestGraph()
	n0 := addNode(g, "n0", NodeTypeSession, "", 0)
	mustFloat(t, n0.Energy, 100.0, 0.01)

	n1 := addNode(g, "n1", NodeTypeSession, "", 0)
	mustFloat(t, n1.Energy, 100.0/1.1, 0.01)

	for i := 2; i <= 10; i++ {
		addNode(g, NodeID(rune('a'+i)), NodeTypeSession, "", 0)
	}
	n10 := addNode(g, "n10", NodeTypeSession, "", 0)
	mustFloat(t, n10.Energy, 100.0/2.1, 0.01)
}

// ── RemoveNode ───────────────────────────────────────────────────────────

func TestRemoveNode(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	_ = g.AddEdge("a", "b", EdgeTypePeer)

	if !g.RemoveNode("a") {
		t.Fatal("RemoveNode returned false")
	}
	if g.Node("a") != nil {
		t.Fatal("node should be gone")
	}
	if g.EdgeCount() != 0 {
		t.Fatal("incident edge should be removed")
	}
	if g.RemoveNode("nonexistent") {
		t.Fatal("removing non-existent should return false")
	}
}

// ── Node accessor ────────────────────────────────────────────────────────

func TestNodeReturnsCopy(t *testing.T) {
	g := newTestGraph()
	addNode(g, "x", NodeTypeSession, "orig", 0)
	n1 := g.Node("x")
	n1.Energy = 999
	n2 := g.Node("x")
	if n2.Energy == 999 {
		t.Fatal("Node should return a copy, not a reference")
	}
}

// ── Touch ────────────────────────────────────────────────────────────────

func TestTouchRecordsAccess(t *testing.T) {
	g := newTestGraph()
	n := addNode(g, "h", NodeTypeSession, "hot", 10)
	before := n.AccessedAt
	time.Sleep(time.Millisecond)
	if !g.Touch("h") {
		t.Fatal("Touch should return true")
	}
	if !g.Node("h").AccessedAt.After(before) {
		t.Fatal("Touch should update access time")
	}
}

func TestTouchNonexistent(t *testing.T) {
	g := newTestGraph()
	if g.Touch("nowhere") {
		t.Fatal("Touch on missing node should return false")
	}
}

// ── Tick / Awake Decay ───────────────────────────────────────────────────

func TestTickAsleepDoesNothing(t *testing.T) {
	g := newTestGraph()
	addNode(g, "s", NodeTypeSession, "", 0)
	orig := g.Node("s").Energy
	g.Tick(false)
	mustFloat(t, g.Node("s").Energy, orig, 0.001)
}

func TestTickAwakeDecay(t *testing.T) {
	g := newTestGraph()
	addNode(g, "d", NodeTypeSession, "", 0)
	orig := g.Node("d").Energy
	g.Tick(true)
	post := g.Node("d").Energy
	if post >= orig {
		t.Fatal("energy should decrease after awake tick")
	}
}

func TestOntologyImmunity(t *testing.T) {
	g := newTestGraph()
	addNode(g, "onto", NodeTypeOntology, "immune", 0)
	addNode(g, "sess", NodeTypeSession, "normal", 0)
	origOnto := g.Node("onto").Energy
	origSess := g.Node("sess").Energy

	for i := 0; i < 10; i++ {
		g.Tick(true)
	}

	ontoPost := g.Node("onto").Energy
	sessPost := g.Node("sess").Energy
	ontoDecay := origOnto - ontoPost
	sessDecay := origSess - sessPost
	t.Logf("onto: %.4f -> %.4f (decay %.4f)", origOnto, ontoPost, ontoDecay)
	t.Logf("sess: %.4f -> %.4f (decay %.4f)", origSess, sessPost, sessDecay)
	if sessDecay <= 0 {
		t.Error("session node should have decayed")
	}
	// Ontology has a lower layer temperature (OntologyBaseTemp=0.5) than
	// session (SessionBaseTemp=1.5), so it must decay slower.
	if ontoDecay >= sessDecay {
		t.Error("ontology node should decay slower than session node")
	}
}

func TestTopologyProtection(t *testing.T) {
	g := newTestGraph()
	addNode(g, "hub", NodeTypeSession, "hub", 0)
	addNode(g, "leaf", NodeTypeSession, "leaf", 0)
	addNode(g, "extra", NodeTypeSession, "extra", 0)
	_ = g.AddEdge("hub", "leaf", EdgeTypePeer)
	_ = g.AddEdge("hub", "extra", EdgeTypePeer)

	g.mu.Lock()
	g.nodes["hub"].Energy = 100
	g.nodes["leaf"].Energy = 100
	g.mu.Unlock()

	for i := 0; i < 5; i++ {
		g.Tick(true)
	}

	hubPost := g.Node("hub").Energy
	leafPost := g.Node("leaf").Energy
	if hubPost <= leafPost {
		t.Logf("hub=%.4f leaf=%.4f", hubPost, leafPost)
		t.Error("hub (2 edges) should decay less than leaf (0 edges)")
	}
}

// TestTopologyProtectionExcludesAntagonist verifies antagonist edges do not
// count toward the outDegree used by topology protection: a node whose only
// edge is an antagonist relation must decay exactly like an isolated node.
func TestTopologyProtectionExcludesAntagonist(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	addNode(g, "iso", NodeTypeSession, "iso", 0)
	_ = g.AddEdge("a", "b", EdgeTypeAntagonist)

	g.mu.Lock()
	g.nodes["a"].Energy = 100
	g.nodes["b"].Energy = 100
	g.nodes["iso"].Energy = 100
	g.mu.Unlock()

	for i := 0; i < 5; i++ {
		g.Tick(true)
	}

	antagonistNode := g.Node("a").Energy
	isolatedNode := g.Node("iso").Energy
	mustFloat(t, antagonistNode, isolatedNode, 0.0001)
}

// ── Death marking on low energy during Tick ──────────────────────────────

func TestTickMarksForDeathBelowThreshold(t *testing.T) {
	g := newTestGraph()
	addNode(g, "low", NodeTypeSession, "low", 0)
	g.mu.Lock()
	// The baseEnergy clamp is gone, so decay penetrates below BaseEnergy.
	// Drop the per-node MinEnergy floor so DecayNode cannot clamp energy back
	// up, then set energy below the config death line (NodeMinEnergy).
	g.nodes["low"].MinEnergy = 0
	g.nodes["low"].Energy = g.cfg.NodeMinEnergy * 0.5 // below threshold
	g.mu.Unlock()

	g.Tick(true)

	if !g.Node("low").MarkDeath {
		t.Error("node with energy below threshold should be marked for death after tick")
	}
}

// Regression: nodes created via AddNode get MinEnergy == cfg.NodeMinEnergy.
// Tick decay must not clamp at that floor — energy has to pierce below the
// death line (strict <) or nodes park at exactly NodeMinEnergy forever and
// the decay purge never fires.
func TestTickDecayPiercesDeathLineForCreatedNodes(t *testing.T) {
	g := newTestGraph()
	addNode(g, "zombie", NodeTypeSession, "zombie", 0)
	if n := g.Node("zombie"); n.MinEnergy != g.cfg.NodeMinEnergy {
		t.Fatalf("setup: MinEnergy=%v want %v", n.MinEnergy, g.cfg.NodeMinEnergy)
	}
	g.mu.Lock()
	g.nodes["zombie"].Energy = g.cfg.NodeMinEnergy * 1.01 // just above the line
	g.mu.Unlock()

	g.Tick(true)

	if e := g.Node("zombie").Energy; e >= g.cfg.NodeMinEnergy {
		t.Fatalf("energy %v should decay below the death line %v", e, g.cfg.NodeMinEnergy)
	}
	if !g.Node("zombie").MarkDeath {
		t.Error("node decaying through the death line should be marked for death")
	}
}

func TestTickDoesNotMarkAboveThreshold(t *testing.T) {
	g := newTestGraph()
	addNode(g, "high", NodeTypeSession, "high", 0)
	// Default energy is 100 which is well above NodeMinEnergy (0.5)
	g.Tick(true)

	if g.Node("high").MarkDeath {
		t.Error("node above threshold should not be marked")
	}
}

// ── TickTokens (token-driven decay) ──────────────────────────────────────

func TestTickTokensAdvancesTickCount(t *testing.T) {
	g := newTestGraph()
	// Default TokenQuantum = 1000. 5000 tokens → 5 steps.
	g.TickTokens(5000)
	if g.TickCount != 5 {
		t.Fatalf("TickCount = %d, want 5", g.TickCount)
	}
}

func TestTickTokensMinOneStep(t *testing.T) {
	g := newTestGraph()
	// 500 tokens < TokenQuantum(1000) → still 1 step.
	g.TickTokens(500)
	if g.TickCount != 1 {
		t.Fatalf("TickCount = %d, want 1", g.TickCount)
	}
}

func TestTickTokensZeroIsOneStep(t *testing.T) {
	g := newTestGraph()
	g.TickTokens(0)
	if g.TickCount != 1 {
		t.Fatalf("TickCount = %d, want 1", g.TickCount)
	}
}

func TestTickTokensDecaysMoreThanSingleTick(t *testing.T) {
	g1 := newTestGraph()
	g2 := newTestGraph()
	addNode(g1, "a", NodeTypeSession, "", 0)
	addNode(g2, "a", NodeTypeSession, "", 0)

	g1.Tick(true)           // 1 step
	g2.TickTokens(5000)     // 5 steps
	if g2.Node("a").Energy >= g1.Node("a").Energy {
		t.Fatalf("5 steps should decay more than 1: g1=%.4f g2=%.4f",
			g1.Node("a").Energy, g2.Node("a").Energy)
	}
}

func TestTickTokensUpdatesRecencyProportionally(t *testing.T) {
	g := newTestGraph()
	n := addNode(g, "a", NodeTypeSession, "", 0)
	n.LastAccessTick = 0

	g.TickTokens(10000) // 10 steps → TickCount=10

	score := RecallScore(n, g.TickCount, g.cfg.TickDecayK)
	// recency = 1/(1+10*0.05) = 1/1.5 ≈ 0.667
	want := n.Energy / (1.0 + 10.0*g.cfg.TickDecayK)
	if score > want*1.01 || score < want*0.99 {
		t.Fatalf("recency after 10 steps: score=%.4f want≈%.4f", score, want)
	}
}

// ── AddEdge Layer Invariants ────────────────────────────────────────────

func TestAddEdgePeerRequiresSameLayer(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeExperience, "b", 0)
	err := g.AddEdge("a", "b", EdgeTypePeer)
	if err == nil {
		t.Fatal("peer edge between different layers should fail")
	}
}

func TestAddEdgeAntagonistRequiresSameLayer(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeOntology, "b", 0)
	err := g.AddEdge("a", "b", EdgeTypeAntagonist)
	if err == nil {
		t.Fatal("antagonist edge between different layers should fail")
	}
}

func TestAddEdgeCrossRequiresDifferingLayers(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	err := g.AddEdge("a", "b", EdgeTypeCross)
	if err == nil {
		t.Fatal("cross edge between same layer should fail")
	}
}

func TestAddEdgeCrossDifferentLayersOK(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeExperience, "b", 0)
	if err := g.AddEdge("a", "b", EdgeTypeCross); err != nil {
		t.Fatalf("cross edge between different layers should succeed: %v", err)
	}
}

func TestAddEdgePeerSameLayerOK(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	if err := g.AddEdge("a", "b", EdgeTypePeer); err != nil {
		t.Fatalf("peer edge same layer should succeed: %v", err)
	}
}

func TestAddEdgeAntagonistSameLayerOK(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	if err := g.AddEdge("a", "b", EdgeTypeAntagonist); err != nil {
		t.Fatalf("antagonist edge same layer should succeed: %v", err)
	}
}

func TestAddEdgeAntagonistCrossLayerFails(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeExperience, "b", 0)
	err := g.AddEdge("a", "b", EdgeTypeAntagonist)
	if err == nil {
		t.Fatal("antagonist across layers should fail")
	}
}

// ── AddEdge Duplicate Rejection ─────────────────────────────────────────

func TestAddEdgeDuplicateRejected(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	if err := g.AddEdge("a", "b", EdgeTypePeer); err != nil {
		t.Fatal(err)
	}
	err := g.AddEdge("a", "b", EdgeTypePeer)
	if err == nil {
		t.Fatal("duplicate peer edge should be rejected")
	}
}

func TestAddEdgeSamePairDifferentTypeAllowed(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	if err := g.AddEdge("a", "b", EdgeTypePeer); err != nil {
		t.Fatal(err)
	}
	// Antagonist between same pair is a different semantic type, so allowed.
	if err := g.AddEdge("a", "b", EdgeTypeAntagonist); err != nil {
		t.Fatal("antagonist on same pair as peer should be allowed")
	}
}

// ── AddEdge ConnectionEnergy (no injection) ──────────────────────────────

func TestAddEdgePeerDoesNotInjectEnergy(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)

	preA := g.Node("a").Energy
	preB := g.Node("b").Energy

	if err := g.AddEdge("a", "b", EdgeTypePeer); err != nil {
		t.Fatal(err)
	}
	postA := g.Node("a").Energy
	postB := g.Node("b").Energy

	// ConnectionEnergy is no longer injected into the source node.
	mustFloat(t, postA, preA, 0.01)
	// Target unchanged too.
	mustFloat(t, postB, preB, 0.01)
}

func TestAddEdgeCrossDoesNotInjectEnergy(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeExperience, "b", 0)

	preA := g.Node("a").Energy
	if err := g.AddEdge("a", "b", EdgeTypeCross); err != nil {
		t.Fatal(err)
	}
	postA := g.Node("a").Energy
	mustFloat(t, postA, preA, 0.01)
}

func TestAddEdgeCrossRejectsNonAdjacentLayers(t *testing.T) {
	g := newTestGraph()
	addNode(g, "s", NodeTypeSession, "session node", 0)
	addNode(g, "o", NodeTypeOntology, "ontology node", 0)

	// session→ontology is a 2-level gap; cross edge must be rejected.
	if err := g.AddEdge("s", "o", EdgeTypeCross); err == nil {
		t.Fatal("expected error for cross edge between non-adjacent layers (session→ontology)")
	}

	// Adjacent layers (session→experience) should succeed.
	addNode(g, "e", NodeTypeExperience, "experience node", 0)
	if err := g.AddEdge("s", "e", EdgeTypeCross); err != nil {
		t.Fatalf("expected success for adjacent cross edge: %v", err)
	}

	// Adjacent layers (experience→ontology) should succeed.
	if err := g.AddEdge("e", "o", EdgeTypeCross); err != nil {
		t.Fatalf("expected success for adjacent cross edge: %v", err)
	}
}

func TestAddEdgeAntagonistNoEnergyInjection(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)

	preA := g.Node("a").Energy
	_ = g.AddEdge("a", "b", EdgeTypeAntagonist)
	postA := g.Node("a").Energy
	mustFloat(t, postA, preA, 0.01)
}

// ── AddEdge Missing / Self-loop ──────────────────────────────────────────

func TestAddEdgeMissingNode(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	if err := g.AddEdge("a", "nowhere", EdgeTypePeer); err == nil {
		t.Fatal("expected error for missing target node")
	}
}

func TestAddEdgeSelfLoop(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	if err := g.AddEdge("a", "a", EdgeTypePeer); err == nil {
		t.Fatal("expected error for self-loop")
	}
}

// ── Antagonist Siphon (event-triggered) ──────────────────────────────────

func TestSiphonTriggersOnTouch(t *testing.T) {
	g := newTestGraph()
	addNode(g, "winner", NodeTypeSession, "winner", 10)
	addNode(g, "loser", NodeTypeSession, "loser", 0)
	g.mu.Lock()
	g.nodes["loser"].Energy = 100
	g.mu.Unlock()
	_ = g.AddEdge("winner", "loser", EdgeTypeAntagonist)

	winnerPre := g.Node("winner").Energy
	loserPre := g.Node("loser").Energy

	g.Touch("winner")

	winnerPost := g.Node("winner").Energy
	loserPost := g.Node("loser").Energy

	// SiphonEnergy transferred from loser to winner.
	transfer := g.cfg.SiphonEnergy
	mustFloat(t, winnerPost, winnerPre+transfer, 0.01)
	mustFloat(t, loserPost, loserPre-transfer, 0.01)
}

func TestSiphonBoundedByLoserEnergy(t *testing.T) {
	g := newTestGraph()
	addNode(g, "winner", NodeTypeSession, "winner", 0)
	addNode(g, "loser", NodeTypeSession, "loser", 0)
	g.mu.Lock()
	g.nodes["loser"].Energy = 1.0 // less than SiphonEnergy (10)
	g.mu.Unlock()
	_ = g.AddEdge("winner", "loser", EdgeTypeAntagonist)

	winnerPre := g.Node("winner").Energy
	g.Touch("winner")

	winnerPost := g.Node("winner").Energy
	loserPost := g.Node("loser").Energy

	// Should transfer only 1.0 (loser's energy), not full SiphonEnergy.
	mustFloat(t, winnerPost, winnerPre+1.0, 0.01)
	if loserPost != 0 {
		t.Errorf("loser should be drained to 0, got %.4f", loserPost)
	}
}

func TestSiphonDoesNotMarkForDeath(t *testing.T) {
	g := newTestGraph()
	addNode(g, "winner", NodeTypeSession, "winner", 0)
	addNode(g, "loser", NodeTypeSession, "loser", 0)
	g.mu.Lock()
	g.nodes["loser"].Energy = 100
	g.mu.Unlock()
	_ = g.AddEdge("winner", "loser", EdgeTypeAntagonist)

	g.Touch("winner")

	if g.Node("loser").MarkDeath {
		t.Error("siphon should not mark loser for death")
	}
	if g.Node("winner").MarkDeath {
		t.Error("siphon should not mark winner for death")
	}
}

func TestSiphonDoesNotDrainWinner(t *testing.T) {
	g := newTestGraph()
	addNode(g, "winner", NodeTypeSession, "winner", 0)
	addNode(g, "loser", NodeTypeSession, "loser", 0)
	g.mu.Lock()
	g.nodes["loser"].Energy = 100
	g.mu.Unlock()
	_ = g.AddEdge("winner", "loser", EdgeTypeAntagonist)

	winnerPre := g.Node("winner").Energy
	g.Touch("winner")
	winnerPost := g.Node("winner").Energy

	// Winner should gain energy, not lose it.
	if winnerPost < winnerPre {
		t.Error("siphon should not drain winner")
	}
}

func TestSiphonDoesNotTriggerOnAntagonistWithoutTouch(t *testing.T) {
	g := newTestGraph()
	addNode(g, "winner", NodeTypeSession, "winner", 0)
	addNode(g, "loser", NodeTypeSession, "loser", 0)
	g.mu.Lock()
	g.nodes["loser"].Energy = 100
	g.mu.Unlock()
	_ = g.AddEdge("winner", "loser", EdgeTypeAntagonist)

	// No Touch call - siphon should not fire.
	winnerPost := g.Node("winner").Energy
	loserPost := g.Node("loser").Energy

	if winnerPost != 100.0 {
		t.Error("siphon should not fire without trigger")
	}
	if loserPost != 100.0 {
		t.Error("siphon should not fire without trigger")
	}
}

func TestSiphonTriggersOnPeerConnection(t *testing.T) {
	g := newTestGraph()
	addNode(g, "winner", NodeTypeSession, "winner", 0)
	addNode(g, "loser", NodeTypeSession, "loser", 0)
	addNode(g, "friend", NodeTypeSession, "friend", 0)
	g.mu.Lock()
	g.nodes["loser"].Energy = 100
	g.mu.Unlock()
	_ = g.AddEdge("winner", "loser", EdgeTypeAntagonist)

	winnerPre := g.Node("winner").Energy
	// Establish a peer connection from winner -> this triggers siphon.
	_ = g.AddEdge("winner", "friend", EdgeTypePeer)

	winnerPost := g.Node("winner").Energy
	loserPost := g.Node("loser").Energy

	// SiphonEnergy should be transferred from loser to winner; the peer edge
	// itself no longer injects ConnectionEnergy into the winner.
	transfer := g.cfg.SiphonEnergy
	mustFloat(t, winnerPost, winnerPre+transfer, 0.01)
	mustFloat(t, loserPost, 100.0-transfer, 0.01)
}

// ── MarkForDeath ─────────────────────────────────────────────────────────

func TestMarkForDeath(t *testing.T) {
	g := newTestGraph()
	addNode(g, "m", NodeTypeSession, "m", 0)
	if !g.MarkForDeath("m") {
		t.Fatal("MarkForDeath should return true")
	}
	if !g.Node("m").MarkDeath {
		t.Fatal("node should be marked")
	}
	if g.MarkForDeath("nonexistent") {
		t.Fatal("MarkForDeath on missing node should return false")
	}
}

// ── Recall Exclusion of Marked Nodes ─────────────────────────────────────

func TestRecallByIDExcludesMarked(t *testing.T) {
	g := newTestGraph()
	addNode(g, "target", NodeTypeSession, "find-me", 0)
	g.MarkForDeath("target")

	if g.RecallByID("target") != nil {
		t.Fatal("RecallByID should return nil for marked node")
	}
}

func TestRecallByIDReturnsUnmarked(t *testing.T) {
	g := newTestGraph()
	addNode(g, "alive", NodeTypeSession, "alive", 0)
	n := g.RecallByID("alive")
	if n == nil || n.ID != "alive" {
		t.Fatal("RecallByID should find unmarked node")
	}
}

func TestRecallOneHopExcludesMarked(t *testing.T) {
	g := newTestGraph()
	addNode(g, "root", NodeTypeSession, "root", 0)
	addNode(g, "c1", NodeTypeSession, "c1", 0)
	addNode(g, "c2", NodeTypeSession, "c2", 0)
	_ = g.AddEdge("root", "c1", EdgeTypePeer)
	_ = g.AddEdge("root", "c2", EdgeTypePeer)

	g.MarkForDeath("c2")

	nodes, _ := g.RecallOneHop("root")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 unmarked node, got %d", len(nodes))
	}
	if nodes[0].ID != "c1" {
		t.Errorf("expected c1, got %s", nodes[0].ID)
	}
}

// TestRecallOneHopTraversesIncomingEdges verifies RecallOneHop walks both
// directions: peer/cross relations are symmetric, so a node with an incoming
// edge (e.To == id) must also be returned as a neighbor. The incoming edge is
// returned in its original direction so callers can judge it via e.From == id.
func TestRecallOneHopTraversesIncomingEdges(t *testing.T) {
	g := newTestGraph()
	addNode(g, "root", NodeTypeSession, "root", 0)
	addNode(g, "out", NodeTypeSession, "out", 0) // root -> out (outgoing)
	addNode(g, "in", NodeTypeSession, "in", 0)   // in -> root (incoming)
	_ = g.AddEdge("root", "out", EdgeTypePeer)
	_ = g.AddEdge("in", "root", EdgeTypePeer)

	nodes, edges := g.RecallOneHop("root")
	if len(nodes) != 2 {
		t.Fatalf("expected 2 neighbors (outgoing + incoming), got %d", len(nodes))
	}
	got := make(map[NodeID]bool)
	for _, n := range nodes {
		got[n.ID] = true
	}
	if !got["out"] || !got["in"] {
		t.Errorf("expected neighbors out and in, got %v", got)
	}

	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}
	// The incoming edge must be returned unmodified (From="in", To="root"),
	// not reversed into From="root".
	foundIncoming := false
	for _, e := range edges {
		if e.From == "in" && e.To == "root" {
			foundIncoming = true
		}
	}
	if !foundIncoming {
		t.Errorf("expected incoming edge in original direction in %#v", edges)
	}
}

func TestRecallByTypeExcludesMarked(t *testing.T) {
	g := newTestGraph()
	addNode(g, "s1", NodeTypeSession, "s1", 0)
	addNode(g, "s2", NodeTypeSession, "s2", 0)
	g.MarkForDeath("s2")

	sessions := g.Recall(NodeTypeSession, 10)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session (one marked), got %d", len(sessions))
	}
	if sessions[0].ID != "s1" {
		t.Errorf("expected only s1, got %s", sessions[0].ID)
	}
}

func TestRecallMixedExcludesMarked(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	g.MarkForDeath("b")

	all := g.RecallMixed(10)
	if len(all) != 1 {
		t.Fatalf("expected 1 unmarked, got %d", len(all))
	}
}

// ── Recall Budget & Determinism ──────────────────────────────────────────

func TestRecallBudget(t *testing.T) {
	g := newTestGraph()
	for i := 0; i < 10; i++ {
		addNode(g, NodeID(rune('a'+i)), NodeTypeSession, "", 0)
	}
	results := g.Recall(NodeTypeSession, 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results with limit=3, got %d", len(results))
	}
}

func TestRecallBudgetClampedToConfig(t *testing.T) {
	g := newTestGraph()
	g.cfg.MaxRecallBudget = 5
	for i := 0; i < 10; i++ {
		addNode(g, NodeID(rune('a'+i)), NodeTypeSession, "", 0)
	}
	results := g.Recall(NodeTypeSession, 100)
	if len(results) != 5 {
		t.Fatalf("expected 5 (config budget), got %d", len(results))
	}
}

func TestRecallDeterministicTieBreak(t *testing.T) {
	g := newTestGraph()
	addNode(g, "b", NodeTypeSession, "b", 0)
	addNode(g, "a", NodeTypeSession, "a", 0)
	now := time.Now()
	g.mu.Lock()
	g.nodes["a"].Energy = 50
	g.nodes["a"].AccessedAt = now
	g.nodes["b"].Energy = 50
	g.nodes["b"].AccessedAt = now
	g.mu.Unlock()

	r1 := g.Recall(NodeTypeSession, 10)
	if len(r1) != 2 {
		t.Fatalf("expected 2 results, got %d", len(r1))
	}
	if r1[0].ID != "a" || r1[1].ID != "b" {
		t.Errorf("expected order a,b got %s,%s", r1[0].ID, r1[1].ID)
	}
	r2 := g.Recall(NodeTypeSession, 10)
	if r2[0].ID != "a" || r2[1].ID != "b" {
		t.Error("result should be deterministic")
	}
}

func TestRecallMixed(t *testing.T) {
	g := newTestGraph()
	addNode(g, "s", NodeTypeSession, "s", 0)
	addNode(g, "e", NodeTypeExperience, "e", 0)
	addNode(g, "o", NodeTypeOntology, "o", 0)
	all := g.RecallMixed(10)
	if len(all) != 3 {
		t.Fatalf("expected 3 mixed results, got %d", len(all))
	}
}

// TestRecallInjectionCapsAndRanking verifies the per-layer injection caps
// truncate the result and that the returned nodes are ranked by RecallScore.
// Because AddNode applies count-suppression, the first-added node has the
// highest energy and (with equal AccessedAt) the highest RecallScore.
func TestRecallInjectionCapsAndRanking(t *testing.T) {
	g := New(Config{InjectOntologyCap: 2, InjectExperienceCap: 1, InjectSessionCap: 1})
	// Three ontology nodes; cap is 2 so one is excluded.
	o1 := addNode(g, "o1", NodeTypeOntology, "one", 0)
	o2 := addNode(g, "o2", NodeTypeOntology, "two", 0)
	o3 := addNode(g, "o3", NodeTypeOntology, "three", 0)
	addNode(g, "e1", NodeTypeExperience, "exp", 0)
	addNode(g, "s1", NodeTypeSession, "ses", 0)

	ontology, experience, session := g.RecallInjection()
	if len(ontology) != 2 {
		t.Fatalf("ontology: expected cap=2, got %d", len(ontology))
	}
	if len(experience) != 1 || len(session) != 1 {
		t.Fatalf("experience/session: expected 1 each, got %d/%d", len(experience), len(session))
	}

	// Highest-energy first: o1 (added first) > o2 > o3. Cap excludes o3.
	if ontology[0].ID != o1.ID || ontology[1].ID != o2.ID {
		t.Fatalf("ontology ranking: got %s,%s want %s,%s", ontology[0].ID, ontology[1].ID, o1.ID, o2.ID)
	}
	// o3 must be absent from the injected set.
	for _, n := range ontology {
		if n.ID == o3.ID {
			t.Fatalf("o3 (lowest score) should be excluded by cap, got %s", n.ID)
		}
	}
}

// TestRecallInjectionExcludesMarked verifies marked-for-death nodes are never injected.
func TestRecallInjectionExcludesMarked(t *testing.T) {
	g := New(Config{InjectSessionCap: 5})
	s1 := addNode(g, "s1", NodeTypeSession, "keep", 0)
	s2 := addNode(g, "s2", NodeTypeSession, "drop", 0)
	if !g.MarkForDeath(s2.ID) {
		t.Fatal("MarkForDeath failed")
	}
	_, _, session := g.RecallInjection()
	if len(session) != 1 || session[0].ID != s1.ID {
		t.Fatalf("expected only the non-marked session node, got %#v", session)
	}
}

// TestRecallInjectionCrossExpansion verifies that RecallInjection pulls
// experience nodes linked to the injected session nodes via cross edges.
// MaxRecallBudget < InjectExperienceCap leaves spare capacity in the
// experience layer after the independent recall, so the cross-linked node
// (which ranked below the recall budget) is appended without displacing the
// independently selected nodes.
func TestRecallInjectionCrossExpansion(t *testing.T) {
	g := New(Config{MaxRecallBudget: 2, InjectExperienceCap: 5, InjectSessionCap: 5})
	addNode(g, "s1", NodeTypeSession, "session one", 0)
	addNode(g, "e1", NodeTypeExperience, "exp low", 0)
	addNode(g, "e2", NodeTypeExperience, "exp high", 0)
	addNode(g, "e3", NodeTypeExperience, "exp mid", 0)
	if err := g.AddEdge("s1", "e1", EdgeTypeCross); err != nil {
		t.Fatalf("cross edge should succeed: %v", err)
	}

	// e2 > e3 > e1 in energy → independent recall picks e2, e3 (budget 2);
	// cross expansion appends e1 because it is linked to injected session s1.
	g.mu.Lock()
	g.nodes["e1"].Energy = 30
	g.nodes["e2"].Energy = 300
	g.nodes["e3"].Energy = 200
	g.mu.Unlock()

	_, experience, session := g.RecallInjection()
	if len(session) != 1 || session[0].ID != "s1" {
		t.Fatalf("expected session s1 injected, got %#v", session)
	}
	if len(experience) != 3 {
		t.Fatalf("expected 3 experience nodes (2 recalled + 1 cross-expanded), got %d: %#v", len(experience), experience)
	}
	if experience[0].ID != "e2" || experience[1].ID != "e3" || experience[2].ID != "e1" {
		t.Errorf("expected order e2,e3,e1 (independent recall then cross expansion), got %s,%s,%s",
			experience[0].ID, experience[1].ID, experience[2].ID)
	}
}

// TestRecallInjectionCrossExpansionNoReplaceWhenFull verifies the guard: when
// the independent recall already fills the experience cap, a cross-linked
// experience node below the ranking cutoff is NOT added (no replacement of
// independently selected nodes).
func TestRecallInjectionCrossExpansionNoReplaceWhenFull(t *testing.T) {
	g := New(Config{InjectExperienceCap: 2, InjectSessionCap: 5})
	addNode(g, "s1", NodeTypeSession, "session one", 0)
	addNode(g, "e1", NodeTypeExperience, "exp low", 0)
	addNode(g, "e2", NodeTypeExperience, "exp high", 0)
	addNode(g, "e3", NodeTypeExperience, "exp mid", 0)
	if err := g.AddEdge("s1", "e1", EdgeTypeCross); err != nil {
		t.Fatalf("cross edge should succeed: %v", err)
	}

	g.mu.Lock()
	g.nodes["e1"].Energy = 30
	g.nodes["e2"].Energy = 300
	g.nodes["e3"].Energy = 200
	g.mu.Unlock()

	_, experience, _ := g.RecallInjection()
	if len(experience) != 2 {
		t.Fatalf("expected cap=2 experience nodes, got %d", len(experience))
	}
	for _, n := range experience {
		if n.ID == "e1" {
			t.Fatalf("cross-linked e1 must not displace independently selected nodes: got %#v", experience)
		}
	}
}

// TestRecallInjectionCrossExpansionSkipsMarked verifies a marked-for-death
// experience node is never pulled in by cross expansion.
func TestRecallInjectionCrossExpansionSkipsMarked(t *testing.T) {
	g := New(Config{MaxRecallBudget: 1, InjectExperienceCap: 5, InjectSessionCap: 5})
	addNode(g, "s1", NodeTypeSession, "session one", 0)
	addNode(g, "e1", NodeTypeExperience, "exp linked", 0)
	addNode(g, "e2", NodeTypeExperience, "exp high", 0)
	if err := g.AddEdge("s1", "e1", EdgeTypeCross); err != nil {
		t.Fatalf("cross edge should succeed: %v", err)
	}

	g.mu.Lock()
	g.nodes["e1"].Energy = 30
	g.nodes["e1"].MarkDeath = true
	g.nodes["e2"].Energy = 300
	g.mu.Unlock()

	_, experience, _ := g.RecallInjection()
	// e1 is marked for death: independent recall skips it and cross expansion
	// must not resurrect it.
	for _, n := range experience {
		if n.ID == "e1" {
			t.Fatalf("marked e1 must not be injected, got %#v", experience)
		}
	}
	if len(experience) != 1 || experience[0].ID != "e2" {
		t.Fatalf("expected only e2, got %#v", experience)
	}
}


// ── IsSleepDue ───────────────────────────────────────────────────────────

func TestIsSleepDueBelowThreshold(t *testing.T) {
	g := newTestGraph()
	if g.IsSleepDue() {
		t.Fatal("should not be due for sleep with no layer pressure")
	}
}

func TestIsSleepDueAboveThreshold(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SessionPressureTarget = 1
	cfg.SessionPressureHigh = 2
	cfg.ExperiencePressureTarget = 1
	cfg.ExperiencePressureHigh = 2
	g := New(cfg)
	// Push session and experience layers to high pressure; total pressure 2.0
	// exceeds SleepPressureThreshold (1.2).
	addNode(g, "s1", NodeTypeSession, "s1", 0)
	addNode(g, "s2", NodeTypeSession, "s2", 0)
	addNode(g, "e1", NodeTypeExperience, "e1", 0)
	addNode(g, "e2", NodeTypeExperience, "e2", 0)
	if !g.IsSleepDue() {
		t.Fatal("should be due for sleep when layer pressure exceeds threshold")
	}
}

func TestIsSleepDueSingleLayerAtHighNotTriggered(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SessionPressureTarget = 2
	cfg.SessionPressureHigh = 4
	g := New(cfg)
	// Single layer at full pressure (4/4 >= high) → pressure 1.0, which is
	// below the default threshold 1.2: one hot session layer must not
	// monopolize sleep triggering.
	addNode(g, "s1", NodeTypeSession, "s1", 0)
	addNode(g, "s2", NodeTypeSession, "s2", 0)
	addNode(g, "s3", NodeTypeSession, "s3", 0)
	addNode(g, "s4", NodeTypeSession, "s4", 0)
	if g.IsSleepDue() {
		t.Fatal("single layer at high pressure should not trigger sleep with default threshold 1.2")
	}
}

func TestIsSleepDueSingleLayerMidPressureNotTriggered(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SessionPressureTarget = 2
	cfg.SessionPressureHigh = 4
	g := New(cfg)
	// 3 sessions: pressure = (3-2)/(4-2) = 0.5 < 1.2 → no sleep.
	addNode(g, "s1", NodeTypeSession, "s1", 0)
	addNode(g, "s2", NodeTypeSession, "s2", 0)
	addNode(g, "s3", NodeTypeSession, "s3", 0)
	if g.IsSleepDue() {
		t.Fatal("mid-pressure single layer should not trigger sleep")
	}
}

// ── Communities ──────────────────────────────────────────────────────────

func TestCommunitiesIsolated(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	comps := g.Communities()
	if len(comps) != 2 {
		t.Fatalf("expected 2 components, got %d", len(comps))
	}
}

func TestCommunitiesConnected(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	addNode(g, "c", NodeTypeSession, "c", 0)
	_ = g.AddEdge("a", "b", EdgeTypePeer)
	_ = g.AddEdge("b", "c", EdgeTypePeer)

	comps := g.Communities()
	if len(comps) != 1 {
		t.Fatalf("expected 1 component, got %d", len(comps))
	}
	if len(comps[0]) != 3 {
		t.Fatalf("expected 3 nodes in component, got %d", len(comps[0]))
	}
	if comps[0][0] != "a" || comps[0][1] != "b" || comps[0][2] != "c" {
		t.Errorf("unexpected component order: %v", comps[0])
	}
}

func TestCommunitiesDeterministic(t *testing.T) {
	g := newTestGraph()
	addNode(g, "z", NodeTypeSession, "z", 0)
	addNode(g, "y", NodeTypeSession, "y", 0)
	comps1 := g.Communities()

	g2 := newTestGraph()
	addNode(g2, "z", NodeTypeSession, "z", 0)
	addNode(g2, "y", NodeTypeSession, "y", 0)
	comps2 := g2.Communities()

	if len(comps1) != len(comps2) {
		t.Fatal("non-deterministic community count")
	}
	for i := range comps1 {
		if len(comps1[i]) != len(comps2[i]) {
			t.Fatal("non-deterministic component size")
		}
		for j := range comps1[i] {
			if comps1[i][j] != comps2[i][j] {
				t.Fatal("non-deterministic component ordering")
			}
		}
	}
}

func TestCommunitiesEmpty(t *testing.T) {
	g := newTestGraph()
	comps := g.Communities()
	if comps != nil {
		t.Fatal("empty graph should return nil")
	}
}

// ── Snapshot / Restore ───────────────────────────────────────────────────

func TestSnapshotDeterministic(t *testing.T) {
	g := newTestGraph()
	addNode(g, "z", NodeTypeSession, "z", 0)
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "m", NodeTypeSession, "m", 0)

	data1, _ := g.Snapshot()
	data2, _ := g.Snapshot()

	if string(data1) != string(data2) {
		t.Fatal("snapshot should be deterministic (same output twice)")
	}
}

func TestSnapshotRestore(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "label-a", 10)
	addNode(g, "b", NodeTypeExperience, "label-b", 5)
	addNode(g, "c", NodeTypeExperience, "label-c", 0)
	_ = g.AddEdge("a", "b", EdgeTypeCross)
	_ = g.AddEdge("b", "c", EdgeTypePeer)
	g.Tick(true) // advance tick count so restore has state to preserve

	data, err := g.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("snapshot should not be empty")
	}

	g2 := newTestGraph()
	if err := g2.Restore(data); err != nil {
		t.Fatal(err)
	}

	if g2.NodeCount() != 3 {
		t.Fatalf("expected 3 nodes after restore, got %d", g2.NodeCount())
	}
	if g2.EdgeCount() != 2 {
		t.Fatalf("expected 2 edges after restore, got %d", g2.EdgeCount())
	}
	if g2.TickCount != g.TickCount {
		t.Fatalf("expected restored tick count %d, got %d", g.TickCount, g2.TickCount)
	}

	a := g2.Node("a")
	if a == nil || a.Label != "label-a" || a.Tokens != 10 {
		t.Error("restored node a has wrong data")
	}
	srcA := g.Node("a")
	if a.BaseEnergy != srcA.BaseEnergy || a.MinEnergy != srcA.MinEnergy {
		t.Errorf("BaseEnergy/MinEnergy lost in restore: got %.2f/%.2f want %.2f/%.2f",
			a.BaseEnergy, a.MinEnergy, srcA.BaseEnergy, srcA.MinEnergy)
	}
}

func TestRestoreCorruptData(t *testing.T) {
	g := newTestGraph()
	if err := g.Restore([]byte("{corrupt")); err == nil {
		t.Fatal("expected error restoring corrupt data")
	}
}

func TestSnapshotEmpty(t *testing.T) {
	g := newTestGraph()
	data, err := g.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal("empty snapshot should be valid JSON")
	}
}

// ── Sleep Instructions ───────────────────────────────────────────────────

func TestSleepGarbageCollectsUnrescuedMarkedNodes(t *testing.T) {
	g := newTestGraph()
	addNode(g, "dying", NodeTypeSession, "dying", 0)
	addNode(g, "living", NodeTypeSession, "living", 0)
	_ = g.AddEdge("dying", "living", EdgeTypePeer)
	g.MarkForDeath("dying")
	if err := g.ApplySleep(nil); err != nil {
		t.Fatal(err)
	}
	if g.Node("dying") != nil || g.EdgeCount() != 0 {
		t.Fatal("unrescued marked node and incident edge should be collected")
	}
	if g.Node("living") == nil {
		t.Fatal("unmarked node should survive")
	}
}

func TestSleepMerge(t *testing.T) {
	g := newTestGraph()
	addNode(g, "survivor", NodeTypeSession, "survivor", 0)
	addNode(g, "mergee", NodeTypeSession, "mergee", 0)
	addNode(g, "other", NodeTypeSession, "other", 0)
	g.mu.Lock()
	g.nodes["mergee"].Energy = 100
	g.mu.Unlock()
	_ = g.AddEdge("mergee", "other", EdgeTypePeer)

	survPre := g.Node("survivor").Energy
	mergeeEnergy := g.Node("mergee").Energy

	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepMerge, TargetID: "survivor", MergeID: "mergee", Head: "merged head", Content: "merged content"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if g.Node("mergee") != nil {
		t.Error("mergee should be removed")
	}
	survPost := g.Node("survivor").Energy
	expected := survPre + mergeeEnergy*g.cfg.MergeEnergyTransfer
	mustFloat(t, survPost, expected, 0.01)

	nodes, _ := g.RecallOneHop("survivor")
	if len(nodes) != 1 || nodes[0].ID != "other" {
		t.Errorf("mergee's edge should be rewired to survivor")
	}
}

func TestSleepPromote(t *testing.T) {
	g := newTestGraph()
	addNode(g, "p", NodeTypeSession, "promote-me", 0)

	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepPromote, TargetID: "p", Head: "experience head", Content: "experience content"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if g.Node("p").Type != NodeTypeExperience {
		t.Error("session should be promoted to experience")
	}

	err = g.ApplySleep([]SleepInstruction{
		{Action: SleepPromote, TargetID: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if g.Node("p").Type != NodeTypeOntology {
		t.Error("experience should be promoted to ontology")
	}

	err = g.ApplySleep([]SleepInstruction{
		{Action: SleepPromote, TargetID: "p"},
	})
	if err == nil {
		t.Error("promoting ontology should fail")
	}
}

func TestSleepConnect(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	addNode(g, "e", NodeTypeExperience, "e", 0)

	// Peer edge: same layer.
	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepConnect, TargetID: "a", MergeID: "b", EdgeType: "peer"},
	})
	if err != nil {
		t.Fatalf("peer connect failed: %v", err)
	}
	if g.EdgeCount() != 1 {
		t.Fatalf("expected 1 edge after peer connect, got %d", g.EdgeCount())
	}

	// Cross edge: adjacent different layers (session→experience).
	err = g.ApplySleep([]SleepInstruction{
		{Action: SleepConnect, TargetID: "a", MergeID: "e", EdgeType: "cross"},
	})
	if err != nil {
		t.Fatalf("cross connect failed: %v", err)
	}
	if g.EdgeCount() != 2 {
		t.Fatalf("expected 2 edges after cross connect, got %d", g.EdgeCount())
	}

	// Invalid edge type — silently skipped, no error, no new edge.
	preCount := g.EdgeCount()
	err = g.ApplySleep([]SleepInstruction{
		{Action: SleepConnect, TargetID: "a", MergeID: "b", EdgeType: "bogus"},
	})
	if err != nil {
		t.Fatalf("invalid edge type should be silently skipped, got: %v", err)
	}
	if g.EdgeCount() != preCount {
		t.Fatalf("invalid edge type should not add an edge, got %d (want %d)", g.EdgeCount(), preCount)
	}
}

func TestSleepRollbackOnFailure(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)

	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepMerge, TargetID: "a", MergeID: "b", Head: "merged", Content: "merged"},
		{Action: SleepMerge, TargetID: "a", MergeID: "nonexistent", Head: "x", Content: "x"}, // fails -> rollback
	})
	if err == nil {
		t.Fatal("expected error for non-existent merge target")
	}

	if g.Node("b") == nil {
		t.Error("merge should have been rolled back, 'b' should still exist")
	}
}

// ── Edge Cases ───────────────────────────────────────────────────────────

func TestConcurrentReadSafety(t *testing.T) {
	g := newTestGraph()
	for i := 0; i < 10; i++ {
		id := NodeID(rune('a' + i))
		addNode(g, id, NodeTypeSession, "", 0)
	}
	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			g.NodeCount()
			g.Nodes()
			g.Edges()
		}
		done <- true
	}()
	go func() {
		for i := 0; i < 100; i++ {
			_ = g.AddNode("", NodeTypeSession, "", "", 0)
			g.Tick(true)
			g.Touch("a")
		}
		done <- true
	}()
	<-done
	<-done
}

func TestNodesEdgesCopies(t *testing.T) {
	g := newTestGraph()
	addNode(g, "x", NodeTypeSession, "", 0)
	nodes := g.Nodes()
	nodes[0].Energy = 999
	if g.Node("x").Energy == 999 {
		t.Error("Nodes() should return copies")
	}
}

func TestSortedOutputs(t *testing.T) {
	g := newTestGraph()
	ids := []NodeID{"z", "m", "a"}
	for _, id := range ids {
		addNode(g, id, NodeTypeSession, "", 0)
	}
	now := time.Now()
	g.mu.Lock()
	for _, id := range ids {
		g.nodes[id].Energy = 100
		g.nodes[id].AccessedAt = now
	}
	g.mu.Unlock()

	results := g.Recall(NodeTypeSession, 10)
	if len(results) != 3 {
		t.Fatalf("expected 3 results")
	}
	if results[0].ID != "a" || results[1].ID != "m" || results[2].ID != "z" {
		t.Errorf("expected a,m,z got %s,%s,%s", results[0].ID, results[1].ID, results[2].ID)
	}
}

// ── Head field ─────────────────────────────────────────────────────────────

func TestAddNodeWithHead(t *testing.T) {
	g := newTestGraph()
	n := g.AddNode("h1", NodeTypeSession, "full content here", "short head", 5)
	if n == nil {
		t.Fatal("AddNode returned nil")
	}
	if n.Head != "short head" {
		t.Errorf("Head = %q, want %q", n.Head, "short head")
	}
	if n.Label != "full content here" {
		t.Errorf("Label = %q, want %q", n.Label, "full content here")
	}
}

func TestHeadPreservedThroughSnapshot(t *testing.T) {
	g := newTestGraph()
	g.AddNode("a", NodeTypeSession, "the body", "the head", 0)
	data, err := g.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	g2 := newTestGraph()
	if err := g2.Restore(data); err != nil {
		t.Fatal(err)
	}
	n := g2.Node("a")
	if n == nil || n.Head != "the head" || n.Label != "the body" {
		t.Errorf("Head/Label lost in restore: Head=%q Label=%q", n.Head, n.Label)
	}
}

func TestHeadEmptyByDefault(t *testing.T) {
	g := newTestGraph()
	n := addNode(g, "x", NodeTypeSession, "some body", 0)
	if n.Head != "" {
		t.Errorf("Head should be empty by default, got %q", n.Head)
	}
}

// ── CountByLayer ──────────────────────────────────────────────────────────

func TestCountByLayer(t *testing.T) {
	g := newTestGraph()
	addNode(g, "s1", NodeTypeSession, "s1", 0)
	addNode(g, "s2", NodeTypeSession, "s2", 0)
	addNode(g, "e1", NodeTypeExperience, "e1", 0)
	addNode(g, "o1", NodeTypeOntology, "o1", 0)
	counts := g.CountByLayer()
	if counts[NodeTypeSession] != 2 {
		t.Errorf("sessions: got %d, want 2", counts[NodeTypeSession])
	}
	if counts[NodeTypeExperience] != 1 {
		t.Errorf("experience: got %d, want 1", counts[NodeTypeExperience])
	}
	if counts[NodeTypeOntology] != 1 {
		t.Errorf("ontology: got %d, want 1", counts[NodeTypeOntology])
	}
}

func TestCountByLayerSkipsMarked(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	g.MarkForDeath("a")
	counts := g.CountByLayer()
	if counts[NodeTypeSession] != 1 {
		t.Errorf("marked nodes should be excluded: got %d, want 1", counts[NodeTypeSession])
	}
}

// ── Layer pressure & IsSleepDue ───────────────────────────────────────────

func TestNewFillsPressureDefaultsForPartialConfig(t *testing.T) {
	g := New(Config{SleepPressureThreshold: 10})
	cfg := g.Config()
	if cfg.SleepPressureThreshold != 10 {
		t.Errorf("explicit SleepPressureThreshold overridden: got %v", cfg.SleepPressureThreshold)
	}
	if cfg.SessionPressureTarget != 12 || cfg.SessionPressureHigh != 24 {
		t.Fatalf("pressure defaults not filled: %#v", cfg)
	}
}

func TestIsSleepDueByLayerPressure(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SessionPressureTarget = 2
	cfg.SessionPressureHigh = 4
	cfg.SleepPressureThreshold = 0.5
	g := New(cfg)
	// Layer pressure from excess sessions should trigger.
	addNode(g, "s1", NodeTypeSession, "s1", 0)
	addNode(g, "s2", NodeTypeSession, "s2", 0)
	addNode(g, "s3", NodeTypeSession, "s3", 0)
	addNode(g, "s4", NodeTypeSession, "s4", 0) // 4 sessions, target=2 => 2x => pressure
	if !g.IsSleepDue() {
		t.Error("IsSleepDue should be true when session count >= 2x target")
	}
}

func TestIsSleepDueNoPressureWithinTarget(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SessionPressureTarget = 10
	cfg.SessionPressureHigh = 20
	g := New(cfg)
	addNode(g, "s1", NodeTypeSession, "s1", 0)
	addNode(g, "s2", NodeTypeSession, "s2", 0) // 2 sessions, target=10 => no pressure
	if g.IsSleepDue() {
		t.Error("IsSleepDue should be false below 2x target")
	}
}

// ── Sleep content fidelity: merge ─────────────────────────────────────────

func TestSleepMergeRequiresHeadAndContent(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a", 0)
	addNode(g, "b", NodeTypeSession, "b", 0)
	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepMerge, TargetID: "a", MergeID: "b"}, // missing Head/Content
	})
	if err == nil {
		t.Fatal("merge without Head/Content should be rejected")
	}
}

func TestSleepMergeAppliesRewrittenContent(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "a content", 0)
	addNode(g, "b", NodeTypeSession, "b content", 0)
	g.mu.Lock()
	g.nodes["b"].Energy = 50
	g.mu.Unlock()
	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepMerge, TargetID: "a", MergeID: "b", Head: "merged", Content: "merged content"},
	})
	if err != nil {
		t.Fatal(err)
	}
	n := g.Node("a")
	if n.Head != "merged" || n.Label != "merged content" {
		t.Errorf("rewritten Head/Label not applied: Head=%q Label=%q", n.Head, n.Label)
	}
	if g.Node("b") != nil {
		t.Error("mergee should be removed")
	}
}

func TestSleepMergeRollbackPreservesOriginal(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "orig a", 0)
	addNode(g, "b", NodeTypeSession, "orig b", 0)
	g.mu.Lock()
	g.nodes["a"].Head = "orig head"
	g.mu.Unlock()
	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepMerge, TargetID: "a", MergeID: "b", Head: "new", Content: "new"},
		{Action: SleepPromote, TargetID: "nonexistent"}, // will fail -> rollback
	})
	if err == nil {
		t.Fatal("expected rollback error")
	}
	n := g.Node("a")
	if n.Head != "orig head" || n.Label != "orig a" {
		t.Errorf("Head/Label should roll back: Head=%q Label=%q", n.Head, n.Label)
	}
	if g.Node("b") == nil {
		t.Error("mergee should survive rollback")
	}
}

// ── Sleep content fidelity: promote ───────────────────────────────────────

func TestSleepPromoteSessionRequiresHead(t *testing.T) {
	g := newTestGraph()
	addNode(g, "p", NodeTypeSession, "content", 0)
	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepPromote, TargetID: "p"}, // missing Head
	})
	if err == nil {
		t.Fatal("promote session without Head should be rejected")
	}
}

func TestSleepPromoteExperienceAppliesHead(t *testing.T) {
	g := newTestGraph()
	addNode(g, "p", NodeTypeSession, "old content", 0)
	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepPromote, TargetID: "p", Head: "exp head", Content: "exp content"},
	})
	if err != nil {
		t.Fatal(err)
	}
	n := g.Node("p")
	if n.Type != NodeTypeExperience {
		t.Error("should be promoted to experience")
	}
	if n.Head != "exp head" {
		t.Errorf("Head not applied: got %q", n.Head)
	}
	if n.Label != "exp content" {
		t.Errorf("Content not applied: got %q", n.Label)
	}
}

func TestSleepPromoteOntologyPreservesHead(t *testing.T) {
	g := newTestGraph()
	addNode(g, "p", NodeTypeExperience, "old content", 0)
	g.mu.Lock()
	g.nodes["p"].Head = "old head"
	g.mu.Unlock()
	// Promote Experience -> Ontology, no Head required, no Content rewrite.
	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepPromote, TargetID: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	n := g.Node("p")
	if n.Type != NodeTypeOntology {
		t.Error("should be promoted to ontology")
	}
	if n.Head != "old head" {
		t.Errorf("old Head should be preserved: got %q", n.Head)
	}
	if n.Label != "old content" {
		t.Errorf("old Label should be preserved: got %q", n.Label)
	}
}

// ── LayerPressureSummary ──────────────────────────────────────────────────

func TestLayerPressureSummary(t *testing.T) {
	g := newTestGraph()
	addNode(g, "s1", NodeTypeSession, "s1", 0)
	addNode(g, "e1", NodeTypeExperience, "e1", 0)
	addNode(g, "e2", NodeTypeExperience, "e2", 0)
	summary := g.LayerPressureSummary()
	if summary == "" {
		t.Fatal("summary should not be empty")
	}
	if !contains(summary, "session") || !contains(summary, "experience") || !contains(summary, "ontology") {
		t.Errorf("summary should mention all layers: %s", summary)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestNodeTypeString(t *testing.T) {
	if NodeTypeSession.String() != "session" {
		t.Error("bad session string")
	}
	if NodeTypeExperience.String() != "experience" {
		t.Error("bad experience string")
	}
	if NodeTypeOntology.String() != "ontology" {
		t.Error("bad ontology string")
	}
}

func TestEdgeTypeString(t *testing.T) {
	if EdgeTypePeer.String() != "peer" {
		t.Error("bad peer string")
	}
	if EdgeTypeCross.String() != "cross" {
		t.Error("bad cross string")
	}
	if EdgeTypeAntagonist.String() != "antagonist" {
		t.Error("bad antagonist string")
	}
}

func TestZeroValueGraphIsUsable(t *testing.T) {
	g := New(Config{})
	if g.NodeCount() != 0 {
		t.Error("zero-value graph should start empty")
	}
}

func TestRemoveEdge(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "", 0)
	addNode(g, "b", NodeTypeSession, "", 0)
	_ = g.AddEdge("a", "b", EdgeTypePeer)
	if !g.RemoveEdge("a", "b", EdgeTypePeer) {
		t.Fatal("RemoveEdge should return true")
	}
	if g.EdgeCount() != 0 {
		t.Fatal("edge should be removed")
	}
	if g.RemoveEdge("a", "b", EdgeTypePeer) {
		t.Fatal("removing removed edge should return false")
	}
}

func TestRemoveEdgeMatchesType(t *testing.T) {
	g := newTestGraph()
	addNode(g, "a", NodeTypeSession, "", 0)
	addNode(g, "b", NodeTypeSession, "", 0)
	addNode(g, "c", NodeTypeExperience, "", 0)
	addNode(g, "d", NodeTypeOntology, "", 0)
	_ = g.AddEdge("a", "b", EdgeTypePeer)
	_ = g.AddEdge("c", "d", EdgeTypeCross)
	// Peer edge must not be removed by a cross-type removal.
	if g.RemoveEdge("a", "b", EdgeTypeCross) {
		t.Fatal("RemoveEdge with wrong type should return false")
	}
	if g.EdgeCount() != 2 {
		t.Fatal("edge count should stay 2 after type mismatch removal attempt")
	}
	if !g.RemoveEdge("a", "b", EdgeTypePeer) {
		t.Fatal("RemoveEdge with matching type should return true")
	}
	if g.EdgeCount() != 1 {
		t.Fatal("only the peer edge should be removed")
	}
}

func TestSortImported(t *testing.T) {
	s := []int{3, 1, 2}
	sort.Ints(s)
}

// ── Integration: end-to-end sleep then snapshot ──────────────────────────

func TestSleepThenSnapshotRoundTrip(t *testing.T) {
	g := newTestGraph()
	addNode(g, "s1", NodeTypeSession, "session1", 5)
	addNode(g, "s2", NodeTypeSession, "session2", 3)
	addNode(g, "e1", NodeTypeExperience, "exp1", 0)
	_ = g.AddEdge("s1", "e1", EdgeTypeCross)

	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepPromote, TargetID: "e1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := g.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	g2 := newTestGraph()
	if err := g2.Restore(data); err != nil {
		t.Fatal(err)
	}

	if g2.Node("e1").Type != NodeTypeOntology {
		t.Error("promoted node type should persist through snapshot/restore")
	}
	if g2.EdgeCount() != 1 {
		t.Error("edge should persist through snapshot/restore")
	}
	if g2.cfg.InitialEnergyBase != DefaultConfig().InitialEnergyBase {
		t.Error("config should not be overwritten by snapshot")
	}
}

// ── UpsertNode ──────────────────────────────────────────────────────────

func TestUpsertNodeCreatesNew(t *testing.T) {
	g := newTestGraph()
	n := g.UpsertNode("anchor", NodeTypeOntology, "label", "head", 5)
	if n == nil {
		t.Fatal("UpsertNode should create node")
	}
	if n.ID != "anchor" || n.Type != NodeTypeOntology || n.Label != "label" || n.Head != "head" || n.Tokens != 5 {
		t.Errorf("unexpected node fields: %+v", n)
	}
	if g.NodeCount() != 1 {
		t.Fatalf("expected 1 node, got %d", g.NodeCount())
	}
}

func TestUpsertNodeUpdatesExisting(t *testing.T) {
	g := newTestGraph()
	first := g.UpsertNode("anchor", NodeTypeOntology, "original", "orig head", 5)
	preEnergy := first.Energy
	preCreated := first.CreatedAt
	time.Sleep(time.Millisecond)
	updated := g.UpsertNode("anchor", NodeTypeSession, "updated", "new head", 10)
	if updated == nil {
		t.Fatal("UpsertNode should return existing node")
	}
	if updated.Label != "updated" || updated.Head != "new head" || updated.Tokens != 10 {
		t.Errorf("unexpected updated fields: Label=%q Head=%q Tokens=%d", updated.Label, updated.Head, updated.Tokens)
	}
	if updated.Type != NodeTypeOntology {
		t.Errorf("Type should be preserved (Ontology), got %v", updated.Type)
	}
	if updated.Energy != preEnergy {
		t.Errorf("Energy should be preserved (%.2f), got %.2f", preEnergy, updated.Energy)
	}
	if updated.CreatedAt != preCreated {
		t.Errorf("CreatedAt should be preserved, got %v vs %v", updated.CreatedAt, preCreated)
	}
	if updated.AccessedAt.Before(preCreated) || updated.AccessedAt.Equal(preCreated) {
		t.Error("AccessedAt should be refreshed on update")
	}
}

// ── Protected node (merge rejected) ──────────────────────────────────────

func TestSleepMergeRejectsProtectedMergee(t *testing.T) {
	g := newTestGraph()
	addNode(g, "target", NodeTypeSession, "target", 0)
	addNode(g, "mergee", NodeTypeSession, "mergee", 0)
	g.nodes["mergee"].Protected = true
	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepMerge, TargetID: "target", MergeID: "mergee", Head: "h", Content: "c"},
	})
	if err == nil {
		t.Fatal("SleepMerge with protected mergee should fail")
	}
	if g.Node("mergee") == nil {
		t.Fatal("protected mergee should not be removed")
	}
}

func TestSleepMergeAllowsProtectedTarget(t *testing.T) {
	g := newTestGraph()
	addNode(g, "target", NodeTypeOntology, "target", 0)
	addNode(g, "mergee", NodeTypeSession, "mergee", 0)
	g.nodes["target"].Protected = true
	g.nodes["mergee"].Energy = 100

	err := g.ApplySleep([]SleepInstruction{
		{Action: SleepMerge, TargetID: "target", MergeID: "mergee", Head: "merged head", Content: "merged content"},
	})
	if err != nil {
		t.Fatalf("SleepMerge with protected target should succeed: %v", err)
	}
	if g.Node("mergee") != nil {
		t.Error("mergee should be removed")
	}
	target := g.Node("target")
	if target == nil || target.Label != "merged content" {
		t.Error("protected target should be updated with merged content")
	}
	if !target.Protected {
		t.Error("target should remain protected")
	}
}

func TestProtectedPreservedThroughSnapshot(t *testing.T) {
	g := newTestGraph()
	addNode(g, "anchor", NodeTypeOntology, "anchor body", 0)
	g.nodes["anchor"].Protected = true
	g.nodes["anchor"].Energy = 500

	data, err := g.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	g2 := newTestGraph()
	if err := g2.Restore(data); err != nil {
		t.Fatal(err)
	}
	n := g2.Node("anchor")
	if n == nil || !n.Protected {
		t.Error("Protected field should survive snapshot/restore")
	}
	if n.Energy != 500 {
		t.Errorf("Energy should survive restore: got %.2f", n.Energy)
	}
}

// ── Full cycle: add, connect, decay, recall ─────────────────────────────

func TestFullCycle(t *testing.T) {
	g := newTestGraph()

	addNode(g, "n1", NodeTypeSession, "first", 10)
	addNode(g, "n2", NodeTypeSession, "second", 20)
	addNode(g, "n3", NodeTypeExperience, "pattern", 0)
	addNode(g, "n4", NodeTypeOntology, "concept", 0)
	_ = g.AddEdge("n1", "n2", EdgeTypePeer)
	_ = g.AddEdge("n2", "n3", EdgeTypeCross)
	_ = g.AddEdge("n3", "n4", EdgeTypeCross)

	g.Touch("n1")
	g.Touch("n2")
	n1BeforeTick := g.Node("n1").Energy
	n2BeforeTick := g.Node("n2").Energy

	g.Tick(true)

	if g.Node("n1").Energy >= n1BeforeTick {
		t.Error("n1 should have decayed")
	}
	if g.Node("n2").Energy >= n2BeforeTick {
		t.Error("n2 should have decayed")
	}

	sessions := g.Recall(NodeTypeSession, 5)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	nodes, edges := g.RecallOneHop("n2")
	if len(nodes) < 1 || len(edges) < 1 {
		t.Error("one-hop should return neighbors")
	}

	comps := g.Communities()
	if len(comps) != 1 {
		t.Errorf("expected 1 community, got %d", len(comps))
	}
}
