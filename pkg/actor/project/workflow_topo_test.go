package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ── pure helper unit tests ──

func TestTopoAddNode(t *testing.T) {
	g := WorkflowTopoGraph{Nodes: []WorkflowTopoNode{}, Edges: []WorkflowTopoEdge{}}

	// Insert new.
	topoAddNode(&g, WorkflowTopoNode{ID: "a", Status: "backlog"})
	if len(g.Nodes) != 1 || g.Nodes[0].ID != "a" {
		t.Fatalf("after add: %+v", g.Nodes)
	}

	// Update existing in-place.
	topoAddNode(&g, WorkflowTopoNode{ID: "a", Status: "done", Tags: []string{"x"}})
	if len(g.Nodes) != 1 {
		t.Fatalf("duplicate insert: %+v", g.Nodes)
	}
	if g.Nodes[0].Status != "done" || len(g.Nodes[0].Tags) != 1 {
		t.Errorf("node not updated in-place: %+v", g.Nodes[0])
	}
}

func TestTopoSetNodeEdges(t *testing.T) {
	g := WorkflowTopoGraph{
		Nodes: []WorkflowTopoNode{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []WorkflowTopoEdge{
			{From: "a", To: "b", Kind: "depends_on"},
			{From: "c", To: "a", Kind: "depends_on"},
		},
	}

	// Replace a's deps: a now depends on c (instead of b).
	topoSetNodeEdges(&g, "a", []string{"c"})
	deps := topoDependencies(&g, "a")
	if len(deps) != 1 || deps[0] != "c" {
		t.Errorf("a deps = %v, want [c]", deps)
	}
	// c's edge must survive (not from a).
	deps = topoDependencies(&g, "c")
	if len(deps) != 1 || deps[0] != "a" {
		t.Errorf("c deps = %v, want [a]", deps)
	}

	// De-duplicate.
	topoSetNodeEdges(&g, "a", []string{"c", "c", ""})
	deps = topoDependencies(&g, "a")
	if len(deps) != 1 {
		t.Errorf("dedup a deps = %v, want 1", deps)
	}

	// Clear all a's deps.
	topoSetNodeEdges(&g, "a", nil)
	deps = topoDependencies(&g, "a")
	if len(deps) != 0 {
		t.Errorf("cleared a deps = %v, want empty", deps)
	}
	// Other edges survive.
	deps = topoDependencies(&g, "c")
	if len(deps) != 1 {
		t.Errorf("c deps after clear = %v, want [a]", deps)
	}
}

func TestTopoRemoveNode(t *testing.T) {
	g := WorkflowTopoGraph{
		Nodes: []WorkflowTopoNode{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []WorkflowTopoEdge{
			{From: "a", To: "b", Kind: "depends_on"},
			{From: "b", To: "c", Kind: "depends_on"},
		},
	}
	topoRemoveNode(&g, "b")
	ids := topoNodeIDs(&g)
	if len(ids) != 2 {
		t.Fatalf("nodes after remove = %v, want 2", ids)
	}
	if len(g.Edges) != 0 {
		t.Errorf("edges after remove = %v, want 0 (both touched b)", g.Edges)
	}
}

func TestTopoNodeIDs(t *testing.T) {
	g := WorkflowTopoGraph{Nodes: []WorkflowTopoNode{{ID: "x"}, {ID: "y"}}}
	ids := topoNodeIDs(&g)
	if len(ids) != 2 || ids[0] != "x" || ids[1] != "y" {
		t.Errorf("node ids = %v, want [x y]", ids)
	}
}

// ── double-write integration tests ──

// TestWorkflowTopo_CreateMapInitsGraphSnapshot verifies that create_map
// initializes an empty authoritative graph snapshot.
func TestWorkflowTopo_CreateMapInitsGraphSnapshot(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "topo-map"})

	g, rev := a.loadWorkflowTopo("topo-map")
	if rev == "" {
		t.Fatal("create_map should initialize a workflow_topo graph snapshot with a revision")
	}
	if len(g.Nodes) != 0 {
		t.Errorf("new map graph should have zero nodes, got %d", len(g.Nodes))
	}
	if len(g.Edges) != 0 {
		t.Errorf("new map graph should have zero edges, got %d", len(g.Edges))
	}
}

// TestWorkflowTopo_CreateMapGraphIdempotent verifies that re-initializing a
// map that already has a snapshot does not clobber it.
func TestWorkflowTopo_CreateMapGraphIdempotent(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "idem-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "idem-map", Title: "t1", Question: "Q",
	})

	// Simulate re-open: initWorkflowTopo again (nil emitter: direct internal
	// call, not a handler path).
	a.initWorkflowTopo(nil, "idem-map")

	g, _ := a.loadWorkflowTopo("idem-map")
	if len(g.Nodes) != 1 {
		t.Errorf("idempotent init clobbered existing snapshot: nodes = %v", g.Nodes)
	}
}

// TestWorkflowTopo_CreateTaskCardWritesNodeAndEdges verifies that
// create_task_card double-writes the node + depends_on edges into the graph.
func TestWorkflowTopo_CreateTaskCardWritesNodeAndEdges(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "dw-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "dw-map", Title: "leaf", Question: "Q1",
	})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:     "dw-map",
		Title:     "branch",
		Question:  "Q2",
		DependsOn: []string{"leaf"},
	})

	g, rev := a.loadWorkflowTopo("dw-map")
	if rev == "" {
		t.Fatal("graph snapshot missing after create_task_card")
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(g.Nodes))
	}
	// "branch" node should exist with status mirror.
	var branch *WorkflowTopoNode
	for i := range g.Nodes {
		if g.Nodes[i].ID == "branch" {
			branch = &g.Nodes[i]
		}
	}
	if branch == nil {
		t.Fatal("branch node not found in graph")
	}
	if branch.Status != "todo" {
		t.Errorf("branch status = %q, want todo", branch.Status)
	}
	// Edge: branch depends_on leaf.
	deps := topoDependencies(&g, "branch")
	if len(deps) != 1 || deps[0] != "leaf" {
		t.Errorf("branch deps = %v, want [leaf]", deps)
	}
	// leaf should have no outgoing edges.
	deps = topoDependencies(&g, "leaf")
	if len(deps) != 0 {
		t.Errorf("leaf deps = %v, want empty", deps)
	}
}

// TestWorkflowTopo_SetTaskDependenciesUpdatesEdges verifies that
// set_task_dependencies updates the graph edges (authoritative) and that
// frontmatter stays consistent (projection).
func TestWorkflowTopo_SetTaskDependenciesUpdatesEdges(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "rewire-topo"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "rewire-topo", Title: "p", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "rewire-topo", Title: "q", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "rewire-topo", Title: "r", Question: "Q"})

	// r depends on p.
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "rewire-topo", TaskID: "r", DependsOn: []string{"p"},
	})
	g, _ := a.loadWorkflowTopo("rewire-topo")
	deps := topoDependencies(&g, "r")
	if len(deps) != 1 || deps[0] != "p" {
		t.Fatalf("after set deps: r deps = %v, want [p]", deps)
	}

	// Replace: r now depends on q.
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "rewire-topo", TaskID: "r", DependsOn: []string{"q"},
	})
	g, _ = a.loadWorkflowTopo("rewire-topo")
	deps = topoDependencies(&g, "r")
	if len(deps) != 1 || deps[0] != "q" {
		t.Errorf("after replace: r deps = %v, want [q]", deps)
	}
	// Stale edge to p must be gone.
	deps = topoDependencies(&g, "r")
	for _, d := range deps {
		if d == "p" {
			t.Error("stale edge to p survived after replace")
		}
	}

	// Clear deps.
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "rewire-topo", TaskID: "r", DependsOn: []string{},
	})
	g, _ = a.loadWorkflowTopo("rewire-topo")
	deps = topoDependencies(&g, "r")
	if len(deps) != 0 {
		t.Errorf("after clear: r deps = %v, want empty", deps)
	}
}

// TestWorkflowTopo_GraphFrontmatterConsistency verifies that the graph
// (authoritative) and frontmatter depends_on (projection) agree after each
// mutation path.
func TestWorkflowTopo_GraphFrontmatterConsistency(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "consist-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "consist-map", Title: "c1", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "consist-map", Title: "c2", Question: "Q", DependsOn: []string{"c1"},
	})

	// Graph edges should match frontmatter depends_on.
	fmDeps := a.collectDependsOn("consist-map")
	g, _ := a.loadWorkflowTopo("consist-map")
	for id, expected := range fmDeps {
		graphDeps := topoDependencies(&g, id)
		if !sameSet(expected, graphDeps) {
			t.Errorf("mismatch for %q: frontmatter=%v graph=%v", id, expected, graphDeps)
		}
	}
}

func sameSet(a, b []string) bool {
	ma := make(map[string]bool, len(a))
	for _, v := range a {
		ma[v] = true
	}
	for _, v := range b {
		if !ma[v] {
			return false
		}
		delete(ma, v)
	}
	return len(ma) == 0
}

// ── mirror-sync integration tests (status/tags drift prevention) ──

// TestWorkflowTopo_SetStatusMirrorsNode verifies that set_status double-writes
// the new status onto the task node in the workflow_topo graph.
func TestWorkflowTopo_SetStatusMirrorsNode(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "mirror-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "mirror-map", Title: "m1", Question: "Q"})

	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "m1", Status: "doing"}); err != nil {
		t.Fatalf("set_status doing: %v", err)
	}
	g, _ := a.loadWorkflowTopo("mirror-map")
	if n := findTopoNode(&g, "m1"); n == nil || n.Status != "doing" {
		t.Fatalf("after doing: node = %+v, want status doing", g.Nodes)
	}

	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "m1", Status: "done"}); err != nil {
		t.Fatalf("set_status done: %v", err)
	}
	g, _ = a.loadWorkflowTopo("mirror-map")
	if n := findTopoNode(&g, "m1"); n == nil || n.Status != "done" {
		t.Fatalf("after done: node = %+v, want status done", g.Nodes)
	}
}

// TestWorkflowTopo_ClaimTaskCardMirrorsNode verifies the fused claim path
// (claim_task_card) also mirrors the transition into the graph.
func TestWorkflowTopo_ClaimTaskCardMirrorsNode(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "claim-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "claim-map", Title: "k1", Question: "Q"})

	if _, err := a.handleWikiClaimTaskCard(ctx, domain.WikiClaimTaskCardReq{
		ID: "k1", Status: "doing", ExpectedStatuses: []string{"todo", "backlog"},
	}); err != nil {
		t.Fatalf("claim_task_card: %v", err)
	}
	g, _ := a.loadWorkflowTopo("claim-map")
	if n := findTopoNode(&g, "k1"); n == nil || n.Status != "doing" {
		t.Fatalf("after claim: node = %+v, want status doing", g.Nodes)
	}
}

// TestWorkflowTopo_EditCardMirrorsStatus verifies that a full-raw edit that
// changes the frontmatter status re-mirrors onto the graph node.
func TestWorkflowTopo_EditCardMirrorsStatus(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "edit-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "edit-map", Title: "e1", Question: "Q"})

	card, err := a.store.Get("e1")
	if err != nil {
		t.Fatal(err)
	}
	raw := setCardStatusInRaw(card.Raw, "done")
	if _, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{ID: "e1", Raw: raw}); err != nil {
		t.Fatalf("editCard: %v", err)
	}
	g, _ := a.loadWorkflowTopo("edit-map")
	if n := findTopoNode(&g, "e1"); n == nil || n.Status != "done" {
		t.Fatalf("after edit: node = %+v, want status done", g.Nodes)
	}
}

// TestWorkflowTopo_DeleteCardRemovesNode verifies that delete_card drops the
// node and every touching edge from the graph.
func TestWorkflowTopo_DeleteCardRemovesNode(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "del-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "del-map", Title: "d1", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "del-map", Title: "d2", Question: "Q", DependsOn: []string{"d1"},
	})

	if _, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "d1"}); err != nil {
		t.Fatalf("deleteCard: %v", err)
	}
	g, _ := a.loadWorkflowTopo("del-map")
	if findTopoNode(&g, "d1") != nil {
		t.Errorf("deleted card node survived: %+v", g.Nodes)
	}
	if deps := topoDependencies(&g, "d2"); len(deps) != 0 {
		t.Errorf("edges touching deleted node survived: d2 deps = %v", deps)
	}
	if findTopoNode(&g, "d2") == nil {
		t.Error("sibling node must survive the deletion")
	}
}

// TestWorkflowTopo_ReconcileReturnsFreshCardState verifies that reconcile
// always returns the current card status and frontmatter edges — the graph
// is recomputed from cards on every call.
func TestWorkflowTopo_ReconcileReturnsFreshCardState(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "heal-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "heal-map", Title: "h1", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "heal-map", Title: "h2", Question: "Q", DependsOn: []string{"h1"},
	})
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "h1", Status: "done"}); err != nil {
		t.Fatal(err)
	}

	// Cards are the source of truth: the graph recomputes fresh status and
	// edges on every read, so reconcile always returns the current card state.
	g := a.reconcileWorkflowTopo("heal-map")
	if n := findTopoNode(&g, "h1"); n == nil || n.Status != "done" {
		t.Errorf("card status not reflected: %+v", g.Nodes)
	}
	if deps := topoDependencies(&g, "h2"); len(deps) != 1 || deps[0] != "h1" {
		t.Errorf("frontmatter deps not reflected: h2 deps = %v", deps)
	}
}

// TestWorkflowTopo_GraphGetServesFreshState verifies that graph.get for a
// workflow_topo graph always returns the current card status — the graph is
// recomputed from cards on every read, so there is no drift to heal.
func TestWorkflowTopo_GraphGetServesFreshState(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "get-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "get-map", Title: "g1", Question: "Q"})
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "g1", Status: "done"}); err != nil {
		t.Fatal(err)
	}

	// graph.get recomputes from cards, so it always serves fresh card status.
	resp, err := a.handleGraphGet(ctx, gen.ProjectGraphGetReq{GraphKind: GraphKindWorkflowTopo, ID: "get-map"})
	if err != nil {
		t.Fatal(err)
	}
	var g WorkflowTopoGraph
	if err := json.Unmarshal([]byte(resp.EnvelopeText), &g); err != nil {
		t.Fatal(err)
	}
	if n := findTopoNode(&g, "g1"); n == nil || n.Status != "done" {
		t.Errorf("graph.get served stale mirror: %+v", g.Nodes)
	}
}

// TestWorkflowTopo_GraphGetReflectsExternalEdit verifies that when a card
// file is modified directly on disk, the cardstore watcher invalidates the
// cache and the next graph_get reflects the new card status — no dirty flag
// or reconcile cycle is needed in the stateless model.
func TestWorkflowTopo_GraphGetReflectsExternalEdit(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "ext-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "ext-map", Title: "e1", Question: "Q"})
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e1", Status: "done"})

	// Directly edit the backing card file to simulate an external change.
	path := filepath.Join(tmp, wikiDir, cardFileName("e1"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Replace(string(data), "status: done", "status: backlog", 1)
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for the watcher to invalidate the store cache.
	var card *CardRecord
	for i := 0; i < 50; i++ {
		card, err = a.store.Get("e1")
		if err == nil && card.Status == "backlog" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || card == nil || card.Status != "backlog" {
		t.Fatal("expected store cache to reflect external edit after watcher invalidation")
	}

	// graph_get should reflect the new card status.
	resp, err := a.handleGraphGet(ctx, gen.ProjectGraphGetReq{GraphKind: GraphKindWorkflowTopo, ID: "ext-map"})
	if err != nil {
		t.Fatal(err)
	}
	var g WorkflowTopoGraph
	if err := json.Unmarshal([]byte(resp.EnvelopeText), &g); err != nil {
		t.Fatal(err)
	}
	if n := findTopoNode(&g, "e1"); n == nil || n.Status != "backlog" {
		t.Errorf("graph did not reflect external edit: got %+v", g.Nodes)
	}
}

func findTopoNode(g *WorkflowTopoGraph, id string) *WorkflowTopoNode {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

// ── cycle detection / dangling edge validation ──

func TestTopoHasCycle(t *testing.T) {
	// Linear DAG: a → b → c — no cycle.
	g := WorkflowTopoGraph{
		Nodes: []WorkflowTopoNode{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []WorkflowTopoEdge{
			{From: "a", To: "b", Kind: "depends_on"},
			{From: "b", To: "c", Kind: "depends_on"},
		},
	}
	if topoHasCycle(&g) {
		t.Error("linear DAG should have no cycle")
	}

	// Two-node cycle: a → b → a.
	g2 := WorkflowTopoGraph{
		Nodes: []WorkflowTopoNode{{ID: "a"}, {ID: "b"}},
		Edges: []WorkflowTopoEdge{
			{From: "a", To: "b", Kind: "depends_on"},
			{From: "b", To: "a", Kind: "depends_on"},
		},
	}
	if !topoHasCycle(&g2) {
		t.Error("a→b→a should be detected as cycle")
	}

	// Self-loop: a → a.
	g3 := WorkflowTopoGraph{
		Nodes: []WorkflowTopoNode{{ID: "a"}},
		Edges: []WorkflowTopoEdge{{From: "a", To: "a", Kind: "depends_on"}},
	}
	if !topoHasCycle(&g3) {
		t.Error("self-loop should be detected as cycle")
	}

	// Three-node cycle: a → b → c → a.
	g4 := WorkflowTopoGraph{
		Nodes: []WorkflowTopoNode{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []WorkflowTopoEdge{
			{From: "a", To: "b", Kind: "depends_on"},
			{From: "b", To: "c", Kind: "depends_on"},
			{From: "c", To: "a", Kind: "depends_on"},
		},
	}
	if !topoHasCycle(&g4) {
		t.Error("a→b→c→a should be detected as cycle")
	}

	// Empty graph — no cycle.
	g5 := WorkflowTopoGraph{Nodes: []WorkflowTopoNode{}, Edges: []WorkflowTopoEdge{}}
	if topoHasCycle(&g5) {
		t.Error("empty graph should have no cycle")
	}

	// Non-dep edges should be ignored for cycle detection.
	g6 := WorkflowTopoGraph{
		Nodes: []WorkflowTopoNode{{ID: "a"}, {ID: "b"}},
		Edges: []WorkflowTopoEdge{
			{From: "a", To: "b", Kind: "data_flow"},
			{From: "b", To: "a", Kind: "data_flow"},
		},
	}
	if topoHasCycle(&g6) {
		t.Error("non-dep edges should not count for cycle detection")
	}
}

func TestTopoDanglingEdges(t *testing.T) {
	g := WorkflowTopoGraph{
		Nodes: []WorkflowTopoNode{{ID: "a"}, {ID: "b"}},
		Edges: []WorkflowTopoEdge{
			{From: "a", To: "b", Kind: "depends_on"},       // ok
			{From: "a", To: "ghost", Kind: "depends_on"},   // dangling
			{From: "b", To: "phantom", Kind: "depends_on"}, // dangling
		},
	}
	dangling := topoDanglingEdges(&g)
	if len(dangling) != 2 {
		t.Fatalf("dangling edges = %d, want 2", len(dangling))
	}

	// No dangling.
	g2 := WorkflowTopoGraph{
		Nodes: []WorkflowTopoNode{{ID: "a"}, {ID: "b"}},
		Edges: []WorkflowTopoEdge{{From: "a", To: "b", Kind: "depends_on"}},
	}
	if d := topoDanglingEdges(&g2); len(d) != 0 {
		t.Errorf("expected 0 dangling, got %d", len(d))
	}
}

// ── graph-authoritative integration tests ──

// TestSetTaskDependenciesRejectsCycle verifies that set_task_dependencies
// rejects an edge that would create a cycle, leaving both graph and frontmatter
// unchanged.
func TestSetTaskDependenciesRejectsCycle(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "cycle-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "cycle-map", Title: "x", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "cycle-map", Title: "y", Question: "Q"})

	// x depends on y — valid.
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "cycle-map", TaskID: "x", DependsOn: []string{"y"},
	})

	// y depends on x → would create cycle x→y→x.
	_, err := a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "cycle-map", TaskID: "y", DependsOn: []string{"x"},
	})
	if err == nil {
		t.Fatal("setting deps that create a cycle should be rejected")
	}

	// Graph must not contain the rejected edge.
	g, _ := a.loadWorkflowTopo("cycle-map")
	if deps := topoDependencies(&g, "y"); len(deps) != 0 {
		t.Errorf("graph should not contain rejected cycle edge: y deps = %v", deps)
	}
	// Frontmatter must also be unchanged (no projection written).
	yCard, _ := a.store.Get("y")
	if len(toStringSlice(yCard.Data["depends_on"])) != 0 {
		t.Errorf("frontmatter should not have been modified after cycle rejection")
	}
}

// TestFrontierReadsGraphNotFrontmatter verifies that frontier reads dependencies
// from frontmatter (the stateless authority — the graph is recomputed from
// cards). After set_task_dependencies removes r's dependency on p, frontier
// should immediately include r.
func TestFrontierReadsGraphNotFrontmatter(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "auth-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "auth-map", Title: "p", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "auth-map", Title: "q", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "auth-map", Title: "r", Question: "Q"})

	// r depends on p (handler writes frontmatter — the stateless authority).
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "auth-map", TaskID: "r", DependsOn: []string{"p"},
	})

	// Frontier excludes r (p not done).
	frontier, _ := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "auth-map"})
	for _, tc := range frontier.TaskCards {
		if tc.ID == "r" {
			t.Fatal("r should not be in frontier (r depends on p, not done)")
		}
	}

	// Remove r's dependency on p via the handler (writes frontmatter).
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "auth-map", TaskID: "r", DependsOn: nil,
	})

	// Frontier should now include r — no deps.
	frontier, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "auth-map"})
	found := false
	for _, tc := range frontier.TaskCards {
		if tc.ID == "r" {
			found = true
		}
	}
	if !found {
		t.Fatal("r should be in frontier — no deps now")
	}
}

// TestReconcileWorkflowTopoMigration verifies that a map created before
// workflow_topo (no wfTopoCardData) gets its graph lazily read from
// frontmatter when frontier is first computed — no persist needed.
func TestReconcileWorkflowTopoMigration(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	// Simulate a pre-graph map: write map + task cards directly to the store
	// without going through handlers (so no wfTopoCardData is created).
	mapRaw := "---\nid: legacy-map\ntype: workflow\nstatus: doing\n" +
		"data:\n  scope:\n    include:\n      - legacy-a\n      - legacy-b\n---\n\n# Legacy\n"
	a.store.Save(&CardRecord{Title: "legacy-map", Raw: mapRaw})

	taskARaw := "---\nid: legacy-a\ntype: task\nstatus: done\nparent: legacy-map\n" +
		"tags: [legacy-map]\ndata:\n  depends_on: []\n---\n\nQ\n"
	a.store.Save(&CardRecord{Title: "legacy-a", Raw: taskARaw})

	taskBRaw := "---\nid: legacy-b\ntype: task\nstatus: backlog\nparent: legacy-map\n" +
		"tags: [legacy-map]\ndata:\n  depends_on:\n    - legacy-a\n---\n\nQ\n"
	a.store.Save(&CardRecord{Title: "legacy-b", Raw: taskBRaw})

	// Compute frontier — triggers graph read from frontmatter (no persist).
	frontier := a.computeFrontier("legacy-map", []string{"legacy-a", "legacy-b"})

	// legacy-b depends on legacy-a (done) → should be in frontier.
	found := false
	for _, tc := range frontier {
		if tc.ID == "legacy-b" {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy-b should be in frontier after migration read")
	}

	// Verify the graph was built from frontmatter (read fresh, not persisted).
	g, _ := a.loadWorkflowTopo("legacy-map")
	if len(g.Nodes) != 2 {
		t.Fatalf("migrated graph should have 2 nodes, got %d", len(g.Nodes))
	}
	deps := topoDependencies(&g, "legacy-b")
	if len(deps) != 1 || deps[0] != "legacy-a" {
		t.Errorf("migrated graph deps for legacy-b = %v, want [legacy-a]", deps)
	}
}

// ── data-flow bindings (pure helpers + integration) ──

// TestTopoSetNodeBindings verifies that topoSetNodeBindings replaces all
// bindings originating from nodeID with the supplied slice, de-duplicates
// (Input, FromNode, FromOutput) tuples, drops entries with empty Input or
// FromNode, and leaves bindings for other nodes untouched. Note: the
// FromOutput-empty check is the responsibility of validateBindings (called by
// syncTaskDepsToTopo / syncTaskCardToTopo), not topoSetNodeBindings itself
// (which is a raw mutator).
func TestTopoSetNodeBindings(t *testing.T) {
	g := WorkflowTopoGraph{
		Nodes: []WorkflowTopoNode{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []WorkflowTopoEdge{},
	}

	// Set a's bindings: two inputs sourced from different upstreams.
	topoSetNodeBindings(&g, "a", []WorkflowTopoBinding{
		{Input: "x", FromNode: "b", FromOutput: "out"},
		{Input: "y", FromNode: "c", FromOutput: "out"},
	})
	got := topoNodeBindings(&g, "a")
	if len(got) != 2 {
		t.Fatalf("a bindings = %d, want 2", len(got))
	}

	// De-dup: same (input, fromNode, fromOutput) triple must collapse.
	topoSetNodeBindings(&g, "a", []WorkflowTopoBinding{
		{Input: "x", FromNode: "b", FromOutput: "out"},
		{Input: "x", FromNode: "b", FromOutput: "out"},
	})
	if n := len(topoNodeBindings(&g, "a")); n != 1 {
		t.Errorf("dedup should leave 1 binding, got %d", n)
	}

	// Drop entries with empty Input or FromNode. FromOutput is not dropped
	// here — that's validateBindings' job — but the bindings are otherwise
	// kept. Empty Input or FromNode are filtered.
	topoSetNodeBindings(&g, "a", []WorkflowTopoBinding{
		{Input: "", FromNode: "b", FromOutput: "out"},
		{Input: "x", FromNode: "", FromOutput: "out"},
	})
	if n := len(topoNodeBindings(&g, "a")); n != 0 {
		t.Errorf("empty-Input or empty-FromNode bindings should be dropped, got %d", n)
	}

	// Setting for one node must not touch another node's bindings.
	topoSetNodeBindings(&g, "a", []WorkflowTopoBinding{{Input: "x", FromNode: "b", FromOutput: "out"}})
	topoSetNodeBindings(&g, "c", []WorkflowTopoBinding{{Input: "y", FromNode: "b", FromOutput: "out"}})
	if n := len(topoNodeBindings(&g, "a")); n != 1 {
		t.Errorf("a bindings clobbered by setting c: %d", n)
	}
	if n := len(topoNodeBindings(&g, "c")); n != 1 {
		t.Errorf("c bindings = %d, want 1", n)
	}

	// Clearing: nil/empty input removes all bindings for the node.
	topoSetNodeBindings(&g, "a", nil)
	if n := len(topoNodeBindings(&g, "a")); n != 0 {
		t.Errorf("cleared a bindings: %d, want 0", n)
	}
	if n := len(topoNodeBindings(&g, "c")); n != 1 {
		t.Errorf("clearing a should not affect c: %d", n)
	}
}

// TestValidateBindings covers the binding validity rule: every binding's
// FromNode must appear in the deps list (the upstream must complete before
// the downstream claims).
func TestValidateBindings(t *testing.T) {
	// Empty bindings always valid.
	if err := validateBindings([]string{"a", "b"}, nil); err != nil {
		t.Errorf("empty bindings should be valid: %v", err)
	}

	// All FromNodes in deps → valid.
	if err := validateBindings(
		[]string{"a", "b"},
		[]WorkflowTopoBinding{
			{Input: "x", FromNode: "a", FromOutput: "out"},
			{Input: "y", FromNode: "b", FromOutput: "out"},
		},
	); err != nil {
		t.Errorf("valid binding set: %v", err)
	}

	// FromNode not in deps → invalid.
	if err := validateBindings(
		[]string{"a"},
		[]WorkflowTopoBinding{{Input: "x", FromNode: "ghost", FromOutput: "out"}},
	); err == nil {
		t.Error("binding with FromNode outside deps should be invalid")
	}

	// Empty FromOutput → invalid (cannot wire to nothing).
	if err := validateBindings(
		[]string{"a"},
		[]WorkflowTopoBinding{{Input: "x", FromNode: "a", FromOutput: ""}},
	); err == nil {
		t.Error("binding with empty FromOutput should be invalid")
	}

	// Empty Input → invalid.
	if err := validateBindings(
		[]string{"a"},
		[]WorkflowTopoBinding{{Input: "", FromNode: "a", FromOutput: "out"}},
	); err == nil {
		t.Error("binding with empty Input should be invalid")
	}

	// Empty FromNode → invalid.
	if err := validateBindings(
		[]string{"a"},
		[]WorkflowTopoBinding{{Input: "x", FromNode: "", FromOutput: "out"}},
	); err == nil {
		t.Error("binding with empty FromNode should be invalid")
	}
}

// TestWorkflowTopo_CreateTaskCardWithBindings verifies that the bindings
// argument to create_task_card is persisted into the authoritative graph
// snapshot along with the node + depends_on edges.
func TestWorkflowTopo_CreateTaskCardWithBindings(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "bind-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "bind-map", Title: "src", Question: "Q",
	})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:     "bind-map",
		Title:     "dst",
		Question:  "Q2",
		DependsOn: []string{"src"},
		Bindings: []gen.TaskDataBinding{
			{Input: "x", FromNode: "src", FromOutput: "out"},
		},
	})

	g, _ := a.loadWorkflowTopo("bind-map")
	bindings := topoNodeBindings(&g, "dst")
	if len(bindings) != 1 {
		t.Fatalf("dst bindings = %d, want 1", len(bindings))
	}
	b := bindings[0]
	if b.Node != "dst" || b.Input != "x" || b.FromNode != "src" || b.FromOutput != "out" {
		t.Errorf("dst binding shape = %+v, want {dst, x, src, out}", b)
	}
}

// TestWorkflowTopo_SetTaskDependenciesWithBindings verifies that
// set_task_dependencies persists the new bindings into the graph and replaces
// any pre-existing bindings for the same node.
func TestWorkflowTopo_SetTaskDependenciesWithBindings(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "bind-set-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "bind-set-map", Title: "u1", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "bind-set-map", Title: "u2", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "bind-set-map", Title: "down", Question: "Q",
	})

	// First set: down depends on u1, with one binding (x ← u1.out).
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "bind-set-map", TaskID: "down",
		DependsOn: []string{"u1"},
		Bindings: []gen.TaskDataBinding{
			{Input: "x", FromNode: "u1", FromOutput: "out"},
		},
	})
	g, _ := a.loadWorkflowTopo("bind-set-map")
	if n := len(topoNodeBindings(&g, "down")); n != 1 {
		t.Fatalf("after first set: down bindings = %d, want 1", n)
	}

	// Second set: down now depends on both u1 and u2, with two bindings.
	// The old u1 binding survives, the new u2 binding is added.
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "bind-set-map", TaskID: "down",
		DependsOn: []string{"u1", "u2"},
		Bindings: []gen.TaskDataBinding{
			{Input: "x", FromNode: "u1", FromOutput: "out"},
			{Input: "y", FromNode: "u2", FromOutput: "score"},
		},
	})
	g, _ = a.loadWorkflowTopo("bind-set-map")
	if n := len(topoNodeBindings(&g, "down")); n != 2 {
		t.Fatalf("after second set: down bindings = %d, want 2", n)
	}

	// Third set: replace with empty bindings — clear.
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "bind-set-map", TaskID: "down",
		DependsOn: []string{"u1", "u2"},
		Bindings:  nil,
	})
	g, _ = a.loadWorkflowTopo("bind-set-map")
	if n := len(topoNodeBindings(&g, "down")); n != 0 {
		t.Fatalf("after clear: down bindings = %d, want 0", n)
	}
}

// TestSetTaskDependencies_RejectsBindingNotInDeps verifies that
// set_task_dependencies rejects a binding whose FromNode is not in DependsOn —
// this protects the invariant that every binding's upstream actually precedes
// the downstream (it is one of the declared deps).
func TestSetTaskDependencies_RejectsBindingNotInDeps(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "reject-bind"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "reject-bind", Title: "upstream", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "reject-bind", Title: "down", Question: "Q"})

	// Binding FromNode "ghost" is NOT in DependsOn → reject.
	_, err := a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "reject-bind", TaskID: "down",
		DependsOn: []string{"upstream"},
		Bindings: []gen.TaskDataBinding{
			{Input: "x", FromNode: "ghost", FromOutput: "out"},
		},
	})
	if err == nil {
		t.Fatal("binding with FromNode outside deps should be rejected")
	}
	if !strings.Contains(err.Error(), "not in depends_on") {
		t.Errorf("rejection error should mention depends_on, got: %v", err)
	}

	// Graph must not contain the rejected binding.
	g, _ := a.loadWorkflowTopo("reject-bind")
	if n := len(topoNodeBindings(&g, "down")); n != 0 {
		t.Errorf("rejected binding leaked into graph: %d", n)
	}
	// Frontmatter projection must also be untouched.
	card, _ := a.store.Get("down")
	if len(toStringSlice(card.Data["depends_on"])) != 0 {
		t.Errorf("rejected deps leaked into frontmatter projection")
	}
}

// TestWorkflowTopo_CreateTaskCardWithBadBinding verifies that create_task_card
// rejects a binding whose FromNode is not in DependsOn.
func TestWorkflowTopo_CreateTaskCardWithBadBinding(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "bad-bind-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "bad-bind-map", Title: "real-up", Question: "Q"})

	_, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "bad-bind-map", Title: "down", Question: "Q",
		DependsOn: []string{"real-up"},
		Bindings: []gen.TaskDataBinding{
			{Input: "x", FromNode: "ghost", FromOutput: "out"},
		},
	})
	if err == nil {
		t.Fatal("create_task_card with FromNode outside deps should fail")
	}
	if !strings.Contains(err.Error(), "not in depends_on") {
		t.Errorf("error should mention depends_on, got: %v", err)
	}
}

// ── data binding + frontier end-to-end ──

// TestResolveTaskBindings_ResolvesUpstreamOutputs verifies that
// resolveTaskBindings reads declared bindings from the graph and resolves
// upstream task_outputs into the downstream inputs map. The graph is the
// authoritative source; data.task_outputs is the upstream persistence.
func TestResolveTaskBindings_ResolvesUpstreamOutputs(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "resolve-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "resolve-map", Title: "u1", Question: "Q1"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "resolve-map", Title: "u2", Question: "Q2"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "resolve-map", Title: "down", Question: "Q3",
		DependsOn: []string{"u1", "u2"},
		Bindings: []gen.TaskDataBinding{
			{Input: "x", FromNode: "u1", FromOutput: "report"},
			{Input: "y", FromNode: "u2", FromOutput: "score"},
		},
	})

	// Mark upstreams done with persisted outputs.
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "u1", Status: "done"})
	a.handleWikiSetTaskOutputs(ctx, domain.WikiSetTaskOutputsReq{
		CardID: "u1", Outputs: map[string]any{"report": "audit"},
	})
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "u2", Status: "done"})
	a.handleWikiSetTaskOutputs(ctx, domain.WikiSetTaskOutputsReq{
		CardID: "u2", Outputs: map[string]any{"score": 0.95},
	})

	inputs := a.resolveTaskBindings("resolve-map", "down")
	if inputs == nil {
		t.Fatal("resolveTaskBindings returned nil; want populated inputs")
	}
	if inputs["x"] != "audit" {
		t.Errorf("inputs[x] = %v, want \"audit\"", inputs["x"])
	}
	if inputs["y"] != 0.95 {
		t.Errorf("inputs[y] = %v, want 0.95", inputs["y"])
	}
}

// TestResolveTaskBindings_MissingOutputSkipped verifies that an upstream task
// with no persisted outputs (or an output not in the binding) yields a nil /
// partial inputs map. The claim still succeeds — review-path validation
// catches missing declared outputs at approve time.
func TestResolveTaskBindings_MissingOutputSkipped(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "missing-out"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "missing-out", Title: "u", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "missing-out", Title: "down", Question: "Q",
		DependsOn: []string{"u"},
		Bindings: []gen.TaskDataBinding{
			{Input: "x", FromNode: "u", FromOutput: "report"},
		},
	})

	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "u", Status: "done"})
	// No set_task_outputs — upstream never produced a report.

	inputs := a.resolveTaskBindings("missing-out", "down")
	if inputs != nil {
		t.Errorf("expected nil inputs when upstream output missing, got %+v", inputs)
	}
}

// TestResolveTaskBindings_NoBindingsReturnsNil verifies that a task with no
// declared bindings (no typed data flow) yields nil — the worker just gets
// the goal condition.
func TestResolveTaskBindings_NoBindingsReturnsNil(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "no-bind"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "no-bind", Title: "leaf", Question: "Q"})

	inputs := a.resolveTaskBindings("no-bind", "leaf")
	if inputs != nil {
		t.Errorf("expected nil for leaf without bindings, got %+v", inputs)
	}
}

// TestResolveTaskBindings_PartialOutputs verifies that partial upstream
// outputs yield partial inputs — present fields resolve, absent fields are
// skipped silently.
func TestResolveTaskBindings_PartialOutputs(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "partial-out"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "partial-out", Title: "u", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "partial-out", Title: "down", Question: "Q",
		DependsOn: []string{"u"},
		Bindings: []gen.TaskDataBinding{
			{Input: "x", FromNode: "u", FromOutput: "report"},
			{Input: "y", FromNode: "u", FromOutput: "score"},
		},
	})

	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "u", Status: "done"})
	a.handleWikiSetTaskOutputs(ctx, domain.WikiSetTaskOutputsReq{
		CardID: "u", Outputs: map[string]any{"report": "audit"},
		// score missing
	})

	inputs := a.resolveTaskBindings("partial-out", "down")
	if inputs == nil {
		t.Fatal("expected partial inputs map, got nil")
	}
	if inputs["x"] != "audit" {
		t.Errorf("inputs[x] = %v, want \"audit\"", inputs["x"])
	}
	if _, present := inputs["y"]; present {
		t.Errorf("inputs[y] should be absent, got %v", inputs["y"])
	}
}

// TestHandleWikiSetTaskOutputs_PersistsToFrontmatter verifies that the
// outputs are stored on the task card's frontmatter as a JSON string under
// data.task_outputs and read back as a parsed map.
func TestHandleWikiSetTaskOutputs_PersistsToFrontmatter(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Set up a task card with status=doing (typical state when worker emits outputs).
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "out-persist"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "out-persist", Title: "t", Question: "Q"})
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "t", Status: "doing"})

	out := map[string]any{
		"report": "audit text",
		"score":  0.91,
		"tags":   []any{"a", "b"},
	}
	if _, err := a.handleWikiSetTaskOutputs(ctx, domain.WikiSetTaskOutputsReq{
		CardID: "t", Outputs: out,
	}); err != nil {
		t.Fatalf("set_task_outputs: %v", err)
	}

	card, _ := a.store.Get("t")
	got := a.cardTaskOutputs("t")
	if got == nil {
		t.Fatal("cardTaskOutputs returned nil after set_task_outputs")
	}
	if got["report"] != "audit text" {
		t.Errorf("cardTaskOutputs[report] = %v, want %q", got["report"], "audit text")
	}
	if got["score"] != 0.91 {
		t.Errorf("cardTaskOutputs[score] = %v, want 0.91", got["score"])
	}
	tags, ok := got["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Errorf("cardTaskOutputs[tags] = %v, want [a b]", got["tags"])
	}

	// Frontmatter must contain the task_outputs field for human inspection.
	if !strings.Contains(card.Raw, "task_outputs:") {
		t.Errorf("frontmatter should contain task_outputs: field")
	}
}

// TestClaimTaskCard_ReturnsInputsFromUpstreamOutputs verifies the project-side
// claim handler populates Inputs from the upstream-output binding resolver.
func TestClaimTaskCard_ReturnsInputsFromUpstreamOutputs(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "claim-in"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "claim-in", Title: "u", Question: "Q1"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "claim-in", Title: "down", Question: "Q2",
		DependsOn: []string{"u"},
		Bindings: []gen.TaskDataBinding{
			{Input: "x", FromNode: "u", FromOutput: "report"},
		},
	})
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "u", Status: "done"})
	a.handleWikiSetTaskOutputs(ctx, domain.WikiSetTaskOutputsReq{
		CardID: "u", Outputs: map[string]any{"report": "audit"},
	})

	resp, err := a.handleWikiClaimTaskCard(ctx, domain.WikiClaimTaskCardReq{
		ID: "down", Status: "doing", ExpectedStatuses: []string{"todo"},
	})
	if err != nil {
		t.Fatalf("claim task card: %v", err)
	}
	if resp.Inputs == nil {
		t.Fatal("claim response Inputs = nil; want populated")
	}
	if resp.Inputs["x"] != "audit" {
		t.Errorf("Inputs[x] = %v, want \"audit\"", resp.Inputs["x"])
	}
}

// TestClaimTaskCard_NoBindingsYieldsNilInputs verifies the negative path:
// when there are no bindings, the claim still succeeds and Inputs is nil
// (not an empty map) so the downstream can treat it as "no typed inputs".
func TestClaimTaskCard_NoBindingsYieldsNilInputs(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "claim-no"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "claim-no", Title: "leaf", Question: "Q"})

	resp, err := a.handleWikiClaimTaskCard(ctx, domain.WikiClaimTaskCardReq{
		ID: "leaf", Status: "doing", ExpectedStatuses: []string{"todo"},
	})
	if err != nil {
		t.Fatalf("claim task card: %v", err)
	}
	if resp.Inputs != nil {
		t.Errorf("Inputs should be nil for unbound task, got %+v", resp.Inputs)
	}
}

// TestClaimGuardReadsGraph verifies the claim guard (unmetDependencies) reads
// deps from the authoritative graph, blocking claims while deps are unresolved.
func TestClaimGuardReadsGraph(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "guard-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "guard-map", Title: "g1", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "guard-map", Title: "g2", Question: "Q", DependsOn: []string{"g1"}})

	// g2 depends on g1 (not done) → claiming g2 should fail.
	_, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "g2", Status: "doing"})
	if err == nil {
		t.Fatal("should not be able to claim g2 while g1 is not done")
	}

	// Complete g1 → now claiming g2 should succeed.
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "g1", Status: "done"})
	_, err = a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "g2", Status: "doing"})
	if err != nil {
		t.Fatalf("should be able to claim g2 after g1 is done: %v", err)
	}
}

// ── graph_changed emission from the double-write path ──

// TestWorkflowTopoSaveEmitsGraphChanged verifies the save path emits
// graph_changed on success (with the workflow_topo kind, map id, and the new
// revision) and never emits on failure paths: optimistic-lock mismatch, a
// missing map card, or a nil emitter.
func TestWorkflowTopoSaveEmitsGraphChanged(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "emit-map"})
	if events := graphChangedEvents(ctx); len(events) != 1 {
		t.Fatalf("create_map: expected 1 graph_changed event, got %d", len(events))
	}

	g := WorkflowTopoGraph{Nodes: []WorkflowTopoNode{{ID: "n1"}}, Edges: []WorkflowTopoEdge{}}
	if err := a.saveWorkflowTopo(ctx, "emit-map", g, ""); err != nil {
		t.Fatalf("save: %v", err)
	}
	events := graphChangedEvents(ctx)
	if len(events) != 2 {
		t.Fatalf("expected 2 graph_changed events after successful save, got %d", len(events))
	}
	_, rev := a.loadWorkflowTopo("emit-map")
	if events[1] != (gen.GraphChangedEvent{GraphKind: GraphKindWorkflowTopo, ID: "emit-map", Revision: rev}) {
		t.Fatalf("unexpected graph_changed payload: %+v", events[1])
	}

	// Second save with the correct expected revision emits again with the
	// bumped revision.
	g.Nodes = append(g.Nodes, WorkflowTopoNode{ID: "n2"})
	if err := a.saveWorkflowTopo(ctx, "emit-map", g, rev); err != nil {
		t.Fatalf("second save: %v", err)
	}
	events = graphChangedEvents(ctx)
	if len(events) != 3 {
		t.Fatalf("expected 3 graph_changed events, got %d", len(events))
	}
	_, rev2 := a.loadWorkflowTopo("emit-map")
	if events[2] != (gen.GraphChangedEvent{GraphKind: GraphKindWorkflowTopo, ID: "emit-map", Revision: rev2}) {
		t.Fatalf("unexpected payload after second save: %+v", events[2])
	}
	// Note: revisions are actor-scoped monotonic, so every successful save —
	// including back-to-back ones — bumps the revision; the guarantee under
	// test is one event per successful save carrying the persisted revision.

	// Failure: optimistic-lock mismatch must not emit.
	if err := a.saveWorkflowTopo(ctx, "emit-map", g, "stale"); err == nil {
		t.Fatal("expected optimistic-lock error")
	}
	if events = graphChangedEvents(ctx); len(events) != 3 {
		t.Fatalf("expected no event on lock failure, got %d", len(events))
	}

	// Failure: missing map card must not emit (Get fails inside
	// saveWorkflowTopo before any persist happens).
	if err := a.saveWorkflowTopo(ctx, "unknown-map", g, ""); err == nil {
		t.Fatal("expected error for missing map card")
	}
	if events = graphChangedEvents(ctx); len(events) != 3 {
		t.Fatalf("expected no event on missing map, got %d", len(events))
	}

	// Nil emitter: save succeeds without emission or panic.
	if err := a.saveWorkflowTopo(nil, "emit-map", g, rev2); err != nil {
		t.Fatalf("save with nil emitter: %v", err)
	}
	if events = graphChangedEvents(ctx); len(events) != 3 {
		t.Fatalf("nil emitter must not emit, got %d events", len(events))
	}
}

// TestWorkflowTopoDoubleWriteHandlersEmitGraphChanged verifies the main
// frontend scenario end to end at the handler layer: every callable that
// double-writes the workflow_topo graph notifies graph_changed subscribers,
// and a rejected topology write (dependency cycle) stays silent.
func TestWorkflowTopoDoubleWriteHandlersEmitGraphChanged(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// create_map initializes the graph snapshot → emits.
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "emit-map"})
	events := graphChangedEvents(ctx)
	if len(events) != 1 {
		t.Fatalf("create_map: expected 1 graph_changed event, got %d", len(events))
	}
	_, rev := a.loadWorkflowTopo("emit-map")
	if events[0] != (gen.GraphChangedEvent{GraphKind: GraphKindWorkflowTopo, ID: "emit-map", Revision: rev}) {
		t.Fatalf("create_map event payload: %+v", events[0])
	}

	// create_task_card double-writes node + edges → emits.
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "emit-map", Title: "t1", Question: "Q1"})
	events = graphChangedEvents(ctx)
	if len(events) != 2 {
		t.Fatalf("create_task_card: expected 2 graph_changed events total, got %d", len(events))
	}
	_, rev = a.loadWorkflowTopo("emit-map")
	if events[1] != (gen.GraphChangedEvent{GraphKind: GraphKindWorkflowTopo, ID: "emit-map", Revision: rev}) {
		t.Fatalf("create_task_card event payload: %+v", events[1])
	}

	// set_task_dependencies rewires edges → emits.
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "emit-map", Title: "t2", Question: "Q2"})
	if _, err := a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "emit-map", TaskID: "t2", DependsOn: []string{"t1"},
	}); err != nil {
		t.Fatalf("set_task_dependencies: %v", err)
	}
	events = graphChangedEvents(ctx)
	if len(events) != 4 {
		t.Fatalf("set_task_dependencies: expected 4 graph_changed events total, got %d", len(events))
	}
	_, rev = a.loadWorkflowTopo("emit-map")
	if events[3] != (gen.GraphChangedEvent{GraphKind: GraphKindWorkflowTopo, ID: "emit-map", Revision: rev}) {
		t.Fatalf("set_task_dependencies event payload: %+v", events[3])
	}

	// set_map_inputs writes the map-level inputs table → emits.
	if _, err := a.handleWikiSetMapInputs(ctx, domain.WikiSetMapInputsReq{
		MapID: "emit-map", Inputs: map[string]any{"k": "v"},
	}); err != nil {
		t.Fatalf("set_map_inputs: %v", err)
	}
	if events = graphChangedEvents(ctx); len(events) != 5 {
		t.Fatalf("set_map_inputs: expected 5 graph_changed events total, got %d", len(events))
	}

	// Rejected topology write (t1 → t2 closes the cycle t1→t2→t1) must not emit.
	if _, err := a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "emit-map", TaskID: "t1", DependsOn: []string{"t2"},
	}); err == nil {
		t.Fatal("expected cycle rejection")
	}
	if events = graphChangedEvents(ctx); len(events) != 5 {
		t.Fatalf("cycle rejection must not emit, got %d events", len(events))
	}
}
