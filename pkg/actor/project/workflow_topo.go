package project

import (
	"errors"
	"fmt"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// GraphKindWorkflowTopo is the graph kind for workflow topology snapshots.
// Each workflow map card owns one workflow_topo graph (keyed by map id).
// The graph is a pure view derived from cards: nodes are task card ids in the
// map's scope.include, edges are each task card's frontmatter data.depends_on,
// and bindings/inputs live in the map card's Data["workflowTopo"].
// Frontmatter data.depends_on and data.scope.include are the card-side sources
// of truth; the computed graph never drifts from them.
const GraphKindWorkflowTopo = "workflow_topo"

// WorkflowTopoSchemaVersion is the schema version stamp embedded in every
// workflow_topo graph snapshot's metadata.
const WorkflowTopoSchemaVersion = "workflow_topo.v1"

// WorkflowTopoNode is a node in the workflow topology graph. Each node
// corresponds to a task card scoped by the parent map. Status is a mirror of
// the card's frontmatter status (kept in sync during double-write); it is a
// denormalized projection, not an independent field.
type WorkflowTopoNode struct {
	ID     string   `json:"id"`
	Status string   `json:"status,omitempty"`
	Tags   []string `json:"tags,omitempty"`
}

// WorkflowTopoEdge is a directed dependency edge in the topology graph.
// Semantics: the From node depends on the To node — To must reach "done"
// before From can be claimed (status → doing). Kind distinguishes dependency
// edges ("depends_on") from future data-flow edge kinds.
type WorkflowTopoEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// WorkflowTopoBinding declares a data-flow binding: the named node's input
// field is sourced from an upstream node's output field. This is the structure
// for typed I/O flow between task cards; it is empty during the initial
// double-write phase and will be populated by the typed-inputs system.
type WorkflowTopoBinding struct {
	Node       string `json:"node"`
	Input      string `json:"input"`
	FromNode   string `json:"from_node"`
	FromOutput string `json:"from_output"`
}

// MapInputSourceNode is the sentinel FromNode value in a WorkflowTopoBinding
// that marks the binding as sourced from the map-level inputs table rather than
// from an upstream task's outputs. When FromNode == MapInputSourceNode, the
// binding's FromOutput is the key into WorkflowTopoGraph.Inputs. This sentinel
// is the convergence point of the three typed-inputs channels: regardless of
// whether an input value was written via startup text parsing (set_map_inputs),
// instantiation parameters (template_instantiate.Inputs), or intake grilling
// output promotion (promote_node_outputs), downstream tasks consume it through
// the same binding mechanism — they never read the channels directly.
const MapInputSourceNode = "$map"

// WorkflowTopoGraph is the envelope content for graph kind "workflow_topo".
// It is serialized to JSON and stored as EnvelopeText in the graph snapshot
// (gen.ProjectGraphEnvelopeResp). Nodes carry task-card metadata; edges encode
// the dependency DAG; bindings encode typed data flow; Inputs is the map-level
// binding table populated by the three typed-inputs channels.
type WorkflowTopoGraph struct {
	Nodes    []WorkflowTopoNode    `json:"nodes"`
	Edges    []WorkflowTopoEdge    `json:"edges"`
	Bindings []WorkflowTopoBinding `json:"bindings,omitempty"`
	// Inputs is the map-level inputs table. Three channels converge here:
	// (a) set_map_inputs writes parsed startup text;
	// (b) template_instantiate writes validated instantiation params;
	// (c) promote_node_outputs promotes an intake node's outputs.
	// Downstream tasks read via bindings whose FromNode == MapInputSourceNode.
	Inputs map[string]any `json:"inputs,omitempty"`
}

// ── graph snapshot load / save (card-backed, stateless) ──

// loadWorkflowTopo computes the workflow_topo graph for mapID from the card
// hierarchy. Nodes are derived from the map card's scope.include (each task
// card provides id, status, tags). Edges are read from each task card's
// frontmatter data.depends_on (the projection that is kept in sync by
// projectTopoToFrontmatter). Bindings and map-level inputs are read from the
// map card's Data["workflowTopo"] (wfTopoCardData). The revision is the
// "rev-N" counter stored alongside — it is bumped on every saveWorkflowTopo
// call and provides optimistic-lock CAS.
//
// This replaces the old a.Graphs persistent snapshot. There is no fast-path
// cache: every call recomputes from cards. Callers that need the graph without
// mutations should call loadWorkflowTopo; callers that need to write bindings
// or inputs should call saveWorkflowTopo after loading.
func (a *Actor) loadWorkflowTopo(mapID string) (WorkflowTopoGraph, string) {
	mapCard, err := a.store.Get(mapID)
	if err != nil {
		return WorkflowTopoGraph{Nodes: []WorkflowTopoNode{}, Edges: []WorkflowTopoEdge{}}, ""
	}

	// Read bindings and inputs from the map card's wfTopoCardData.
	cd := readWfTopoCardData(mapCard)
	rev := wfTopoRevisionFromCard(mapCard)

	g := WorkflowTopoGraph{
		Nodes:    []WorkflowTopoNode{},
		Edges:    []WorkflowTopoEdge{},
		Bindings: cd.Bindings,
		Inputs:   cd.Inputs,
	}

	// Build nodes and edges from the card hierarchy.
	includeIDs := scopeIncludeIDs(mapCard)
	for _, id := range includeIDs {
		card, err := a.store.Get(id)
		if err != nil {
			continue
		}
		topoAddNode(&g, WorkflowTopoNode{ID: id, Status: card.Status, Tags: card.Tags})
		topoSetNodeEdges(&g, id, toStringSlice(card.Data["depends_on"]))
	}
	return g, rev
}

// loadWorkflowTopoLocked is a no-op stub retained for compilation — the old
// a.Graphs-based implementation required a lock; the new card-backed
// implementation is lock-free. Callers should use loadWorkflowTopo directly.
func (a *Actor) loadWorkflowTopoLocked(mapID string) (WorkflowTopoGraph, string) {
	return a.loadWorkflowTopo(mapID)
}

// graphChangeEmitter is the minimal emit capability saveWorkflowTopo needs to
// notify graph_changed subscribers after a persisted topology change (the
// project actor's handleGraphSave emits the same event for the generic
// graph.save callable). actor.Context satisfies it, so handler call sites pass
// their ctx directly; nil disables emission (tests that have no live context,
// or direct graph edits in tests).
type graphChangeEmitter interface {
	EmitEvent(kind string, payload any) error
}

// saveWorkflowTopo persists the non-derivable part of the workflow_topo graph
// (bindings and map-level inputs) to the map card's Data["workflowTopo"] and
// bumps the revision counter. The CARDS are the authoritative source for nodes
// and edges — this function only stores what cannot be recomputed from cards.
//
// Optimistic-lock: expectedRev is compared against the current revision; on
// mismatch ErrWorkflowTopoLockMismatch is returned. After a successful save
// the graph_changed event is emitted through emit (nil = no emit).
// ErrWorkflowTopoLockMismatch is returned by saveWorkflowTopo when the
// ExpectedRevision guard fails — another writer changed the bindings/inputs
// between this caller's load and save. Callers retry with a fresh load.
var ErrWorkflowTopoLockMismatch = errors.New("workflow_topo: optimistic lock")

func (a *Actor) saveWorkflowTopo(emit graphChangeEmitter, mapID string, g WorkflowTopoGraph, expectedRev string) error {
	// Read current map card and rev.
	mapCard, err := a.store.Get(mapID)
	if err != nil {
		return fmt.Errorf("workflow_topo: get map card %s: %w", mapID, err)
	}
	currentRev := wfTopoRevisionFromCard(mapCard)

	// CAS check.
	if expectedRev != "" && currentRev != expectedRev {
		return fmt.Errorf("%w for %s: expected %q, got %q",
			ErrWorkflowTopoLockMismatch, mapID, expectedRev, currentRev)
	}

	// Bump revision.
	cd := readWfTopoCardData(mapCard)
	cd.Rev++
	if cd.Rev == 0 {
		cd.Rev = 1 // overflow guard
	}
	cd.Bindings = g.Bindings
	cd.Inputs = g.Inputs

	raw, err := writeWfTopoCardData(mapCard.Raw, cd)
	if err != nil {
		return err
	}
	if err := a.store.Save(&CardRecord{Title: mapID, Raw: raw}); err != nil {
		return fmt.Errorf("workflow_topo: save map card %s: %w", mapID, err)
	}

	newRev := fmt.Sprintf("rev-%d", cd.Rev)
	if emit != nil {
		_ = emit.EmitEvent("graph_changed", gen.GraphChangedEvent{
			GraphKind: GraphKindWorkflowTopo,
			ID:        mapID,
			Revision:  newRev,
		})
	}
	return nil
}

// ── node / edge mutation helpers (pure functions on *WorkflowTopoGraph) ──

// topoAddNode adds node to g, or updates it in-place if a node with the same
// id already exists.
func topoAddNode(g *WorkflowTopoGraph, node WorkflowTopoNode) {
	for i, n := range g.Nodes {
		if n.ID == node.ID {
			g.Nodes[i] = node
			return
		}
	}
	g.Nodes = append(g.Nodes, node)
}

// topoSetNodeEdges replaces all depends_on edges originating from nodeID with
// new edges to the given dependency ids. Duplicate and empty ids are
// de-duplicated. If deps is empty or nil, all outgoing depends_on edges are
// removed.
func topoSetNodeEdges(g *WorkflowTopoGraph, nodeID string, deps []string) {
	filtered := make([]WorkflowTopoEdge, 0, len(g.Edges))
	for _, e := range g.Edges {
		if e.From == nodeID && e.Kind == "depends_on" {
			continue
		}
		filtered = append(filtered, e)
	}
	seen := make(map[string]bool, len(deps))
	for _, dep := range deps {
		if dep == "" || seen[dep] {
			continue
		}
		seen[dep] = true
		filtered = append(filtered, WorkflowTopoEdge{From: nodeID, To: dep, Kind: "depends_on"})
	}
	g.Edges = filtered
}

// topoRemoveNode removes nodeID and all edges touching it from g.
func topoRemoveNode(g *WorkflowTopoGraph, nodeID string) {
	nodes := make([]WorkflowTopoNode, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.ID != nodeID {
			nodes = append(nodes, n)
		}
	}
	g.Nodes = nodes
	edges := make([]WorkflowTopoEdge, 0, len(g.Edges))
	for _, e := range g.Edges {
		if e.From != nodeID && e.To != nodeID {
			edges = append(edges, e)
		}
	}
	g.Edges = edges
}

// topoDependencies returns the depends_on dependency list for nodeID from the
// topology graph, in edge order.
func topoDependencies(g *WorkflowTopoGraph, nodeID string) []string {
	var deps []string
	for _, e := range g.Edges {
		if e.From == nodeID && e.Kind == "depends_on" {
			deps = append(deps, e.To)
		}
	}
	return deps
}

// topoNodeIDs returns the set of node ids in the topology graph.
func topoNodeIDs(g *WorkflowTopoGraph) []string {
	ids := make([]string, len(g.Nodes))
	for i, n := range g.Nodes {
		ids[i] = n.ID
	}
	return ids
}

// topoSetNodeBindings replaces all data-flow bindings originating from nodeID
// with new bindings. A binding declares that nodeID's named Input is sourced
// from FromNode's named FromOutput. If bindings is empty or nil, all bindings
// for nodeID are removed. Duplicate (Input, FromNode, FromOutput) tuples are
// de-duplicated; bindings with an empty Input or FromNode are dropped.
func topoSetNodeBindings(g *WorkflowTopoGraph, nodeID string, bindings []WorkflowTopoBinding) {
	if g.Bindings == nil {
		g.Bindings = []WorkflowTopoBinding{}
	}
	filtered := make([]WorkflowTopoBinding, 0, len(g.Bindings))
	for _, b := range g.Bindings {
		if b.Node == nodeID {
			continue // drop existing bindings for this node
		}
		filtered = append(filtered, b)
	}
	seen := make(map[string]bool, len(bindings))
	for _, b := range bindings {
		if b.Node == "" {
			b.Node = nodeID
		}
		if b.Node != nodeID || b.Input == "" || b.FromNode == "" {
			continue
		}
		key := b.Input + "\x00" + b.FromNode + "\x00" + b.FromOutput
		if seen[key] {
			continue
		}
		seen[key] = true
		filtered = append(filtered, b)
	}
	g.Bindings = filtered
}

// topoNodeBindings returns the data-flow bindings declared for nodeID, in
// declaration order.
func topoNodeBindings(g *WorkflowTopoGraph, nodeID string) []WorkflowTopoBinding {
	var out []WorkflowTopoBinding
	for _, b := range g.Bindings {
		if b.Node == nodeID {
			out = append(out, b)
		}
	}
	return out
}

// validateBindings checks that every binding's FromNode is in the deps list
// (the upstream must be a declared dependency so it completes before the
// downstream claims), OR is MapInputSourceNode (the map-level inputs sentinel,
// which has no ordering dependency — map-level inputs are available from the
// start). Returns a descriptive error for the first violation, or nil when all
// bindings are well-formed (or when bindings is empty).
func validateBindings(deps []string, bindings []WorkflowTopoBinding) error {
	if len(bindings) == 0 {
		return nil
	}
	depSet := make(map[string]bool, len(deps))
	for _, d := range deps {
		depSet[d] = true
	}
	for _, b := range bindings {
		if b.Input == "" {
			return fmt.Errorf("workflow_topo: binding for node %q has empty Input", b.Node)
		}
		if b.FromNode == "" {
			return fmt.Errorf("workflow_topo: binding (input %q) has empty FromNode", b.Input)
		}
		// Map-level input bindings ($map) are exempt from the deps check: they
		// source from the graph's Inputs table, not an upstream task.
		if b.FromNode == MapInputSourceNode {
			if b.FromOutput == "" {
				return fmt.Errorf("workflow_topo: binding (input %q ← $map) has empty FromOutput", b.Input)
			}
			continue
		}
		if !depSet[b.FromNode] {
			return fmt.Errorf("workflow_topo: binding input %q references FromNode %q which is not in depends_on", b.Input, b.FromNode)
		}
		if b.FromOutput == "" {
			return fmt.Errorf("workflow_topo: binding (input %q ← %q) has empty FromOutput", b.Input, b.FromNode)
		}
	}
	return nil
}

// topoSetMapInputs replaces the map-level inputs table on g with the given
// values. A nil or empty inputs map clears the table (sets it to nil so the
// JSON omitempty drops it). This is the write primitive for channel (a)
// (set_map_inputs) and is reused by template_instantiate (channel b) and
// promote_node_outputs (channel c).
func topoSetMapInputs(g *WorkflowTopoGraph, inputs map[string]any) {
	if len(inputs) == 0 {
		g.Inputs = nil
		return
	}
	g.Inputs = make(map[string]any, len(inputs))
	for k, v := range inputs {
		g.Inputs[k] = v
	}
}

// topoMergeMapInputs merges the given values into the map-level inputs table on
// g, overwriting existing keys. This is the merge primitive used by
// promote_node_outputs (channel c): promoted keys replace existing ones, keys
// not in the promotion are preserved.
func topoMergeMapInputs(g *WorkflowTopoGraph, values map[string]any) {
	if len(values) == 0 {
		return
	}
	if g.Inputs == nil {
		g.Inputs = make(map[string]any)
	}
	for k, v := range values {
		g.Inputs[k] = v
	}
}

// ── graph validation (cycle detection, dangling edges) ──

// topoHasCycle reports whether the depends_on edges in g contain a directed
// cycle, using three-color DFS (white=unvisited, gray=on-stack, black=done).
// A cycle means the dependency relation is not a DAG; frontier for nodes in the
// cycle is undefined. set_task_dependencies rejects cycles before they enter
// the graph, so this function is both a write-time guard and a defense-in-depth
// read-time check.
func topoHasCycle(g *WorkflowTopoGraph) bool {
	adj := make(map[string][]string)
	nodeSet := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeSet[n.ID] = true
	}
	for _, e := range g.Edges {
		if e.Kind == "depends_on" {
			adj[e.From] = append(adj[e.From], e.To)
		}
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(nodeSet))
	var dfs func(id string) bool
	dfs = func(id string) bool {
		color[id] = gray
		for _, succ := range adj[id] {
			switch color[succ] {
			case gray:
				return true // back edge → cycle
			case white:
				if dfs(succ) {
					return true
				}
			}
		}
		color[id] = black
		return false
	}
	for id := range nodeSet {
		if color[id] == white && dfs(id) {
			return true
		}
	}
	return false
}

// topoDanglingEdges returns depends_on edges whose To target is not a known
// node in g. Such edges point to cards that are deleted, in another map, or
// never existed. Frontier treats dangling deps as permanently unmet (the target
// status is never "done"), so affected tasks stay out of the frontier — but the
// edges are still reported for diagnostics.
func topoDanglingEdges(g *WorkflowTopoGraph) []WorkflowTopoEdge {
	known := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	var dangling []WorkflowTopoEdge
	for _, e := range g.Edges {
		if e.Kind == "depends_on" && !known[e.To] {
			dangling = append(dangling, e)
		}
	}
	return dangling
}

// ── double-write integration methods ──

// initWorkflowTopo initializes the workflow topo card data on a newly-created
// map (rev=1). Idempotent: if the map card already has a rev counter it is
// left untouched. emit (nil-able) is forwarded so an initializing save can
// notify graph_changed subscribers.
func (a *Actor) initWorkflowTopo(emit graphChangeEmitter, mapID string) {
	rev := a.initWfTopoCardData(mapID)
	if rev == "" || emit == nil {
		return
	}
	_ = emit.EmitEvent("graph_changed", gen.GraphChangedEvent{
		GraphKind: GraphKindWorkflowTopo,
		ID:        mapID,
		Revision:  rev,
	})
}

// syncTaskCardToTopo adds (or updates) a task-card node and its depends_on
// edges in the workflow_topo graph snapshot for mapID. Called from
// handleWikiCreateTaskCard after the frontmatter write, so that the graph
// snapshot (authoritative) and frontmatter (projection) stay in sync. Bindings
// (data-flow declarations) are validated against the declared deps before
// being stored, so a binding referencing a node that is not in DependsOn is
// rejected synchronously rather than leaking into the graph. emit (nil-able)
// is forwarded to saveWorkflowTopo: the successful persist emits graph_changed;
// validation failures return before any save and never emit.
func (a *Actor) syncTaskCardToTopo(emit graphChangeEmitter, mapID, taskID, status string, tags []string, deps []string, bindings []WorkflowTopoBinding) error {
	g, rev := a.loadWorkflowTopo(mapID)
	topoAddNode(&g, WorkflowTopoNode{ID: taskID, Status: status, Tags: tags})
	topoSetNodeEdges(&g, taskID, deps)
	if len(bindings) > 0 {
		if err := validateBindings(deps, bindings); err != nil {
			return err
		}
		topoSetNodeBindings(&g, taskID, bindings)
	}
	return a.saveWorkflowTopo(emit, mapID, g, rev)
}

// syncCardMirrorToTopo re-mirrors a task card's status and tags onto its node
// in the parent map's workflow_topo graph snapshot. Called from every card
// mutation path that can change status or tags (set_status, claim_task_card,
// edit_card) so the denormalized node mirror never drifts from the card — the
// frontend renders map progress from these mirrored statuses. No-op when the
// card has no parent map, the parent has no snapshot yet (graph creation
// belongs to create_map), or the node is absent (membership belongs to
// create_task_card / reconcile). Returns true when a change was persisted.
func (a *Actor) syncCardMirrorToTopo(emit graphChangeEmitter, cardID string) bool {
	card, err := a.store.Get(cardID)
	if err != nil || card.Parent == "" {
		return false
	}
	g, rev := a.loadWorkflowTopo(card.Parent)
	if rev == "" {
		return false
	}
	for i := range g.Nodes {
		if g.Nodes[i].ID != cardID {
			continue
		}
		if g.Nodes[i].Status == card.Status && topoTagsEqual(g.Nodes[i].Tags, card.Tags) {
			return false
		}
		g.Nodes[i].Status = card.Status
		g.Nodes[i].Tags = card.Tags
		return a.saveWorkflowTopo(emit, card.Parent, g, rev) == nil
	}
	return false
}

// removeCardNodeFromTopo drops a deleted task card's node from the parent
// map's workflow topology. Since the graph is recomputed from cards, the
// function also cleans up:
//   - The deleted card from each sibling card's frontmatter depends_on.
//   - The deleted card from the parent map's scope.include (if present).
//
// This ensures the recomputed graph has no dangling edges from deleted cards.
// No-op when the parent is empty, has no snapshot, or the node is already
// absent.
func (a *Actor) removeCardNodeFromTopo(emit graphChangeEmitter, parent, cardID string) bool {
	if parent == "" {
		return false
	}
	g, rev := a.loadWorkflowTopo(parent)
	if rev == "" {
		return false
	}
	// Clean up sibling cards whose frontmatter depends_on includes the
	// deleted card.
	for _, node := range g.Nodes {
		if node.ID == cardID {
			continue
		}
		deps := topoDependencies(&g, node.ID)
		// Remove cardID from deps.
		newDeps := make([]string, 0, len(deps))
		for _, d := range deps {
			if d != cardID {
				newDeps = append(newDeps, d)
			}
		}
		if len(newDeps) != len(deps) {
			card, err := a.store.Get(node.ID)
			if err != nil {
				continue
			}
			raw := setDependsOnInDataBlock(card.Raw, newDeps)
			raw = ensureCardMeta(node.ID, raw, time.Now().UTC().Format(time.RFC3339))
			_ = a.store.Save(&CardRecord{Title: node.ID, Raw: raw})
		}
	}
	// Remove the deleted card from the parent's scope.include if present.
	mapCard, err := a.store.Get(parent)
	if err == nil {
		includeIDs := scopeIncludeIDs(mapCard)
		newInclude := make([]string, 0, len(includeIDs))
		for _, id := range includeIDs {
			if id != cardID {
				newInclude = append(newInclude, id)
			}
		}
		if len(newInclude) != len(includeIDs) {
			mapCard.Raw = removeFromScopeInclude(mapCard.Raw, cardID)
			_ = a.store.Save(&CardRecord{Title: parent, Raw: mapCard.Raw})
		}
	}
	// Bump revision (no g changes to persist — edges are now cleaned from
	// frontmatter and the next loadWorkflowTopo will reflect the change).
	return a.saveWorkflowTopo(emit, parent, g, rev) == nil
}

// topoTagsEqual reports whether two node tag lists are element-wise equal.
func topoTagsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// syncTaskDepsToTopo replaces the depends_on edges (and optional data-flow
// bindings) for taskID in the workflow_topo graph snapshot for mapID. It
// reconciles the graph from frontmatter first (lazy migration), applies the new
// edges + bindings, validates bindings against the new deps, rejects if the
// result would contain a cycle, and persists. Returns the saved graph so the
// caller can project it to frontmatter. emit (nil-able) is forwarded to
// saveWorkflowTopo: the successful persist emits graph_changed; cycle and
// binding-validation failures return before any save and never emit.
func (a *Actor) syncTaskDepsToTopo(emit graphChangeEmitter, mapID, taskID string, deps []string, bindings []WorkflowTopoBinding) (WorkflowTopoGraph, error) {
	g := a.reconcileWorkflowTopo(mapID)
	_, rev := a.loadWorkflowTopo(mapID)
	topoSetNodeEdges(&g, taskID, deps)
	topoSetNodeBindings(&g, taskID, bindings)
	if topoHasCycle(&g) {
		return g, fmt.Errorf("workflow_topo: dependency cycle detected when setting deps for %q", taskID)
	}
	if err := validateBindings(topoDependencies(&g, taskID), topoNodeBindings(&g, taskID)); err != nil {
		return g, err
	}
	return g, a.saveWorkflowTopo(emit, mapID, g, rev)
}

// ── reconciliation and projection (graph-authoritative) ──

// reconcileWorkflowTopo self-heals scope drift, then returns the graph
// computed from cards. Nodes/edges are recomputed from the card hierarchy on
// every call (loadWorkflowTopo), so the only drift that matters is membership:
// a task card whose Parent names mapID is definitionally in scope (that is the
// ground truth create_task_card writes), yet the map's data.scope.include
// projection can miss it when the card was created through the generic
// wiki_create_card instead of wiki_create_task_card (the exact failure the
// workflow-tools bundle forbids) or when a concurrent-create race lost the
// append. adoptOrphanTaskCards repairs the projection before the load, so
// frontier, claim guard, graph.get, and mapTaskIDs all converge without
// special-casing. Adoption persists the healed include list; bindings and
// inputs persist via saveWorkflowTopo.
func (a *Actor) reconcileWorkflowTopo(mapID string) WorkflowTopoGraph {
	a.adoptOrphanTaskCards(mapID)
	g, _ := a.loadWorkflowTopo(mapID)
	return g
}

// adoptOrphanTaskCards appends task cards parented to mapID but missing from
// its data.scope.include to the include list. No-op when the map card is
// missing or is not a workflow map. The missing set is computed from a fresh
// read taken under includeAppendMu, so this cannot last-writer-win against a
// concurrent create_task_card (or healTaskCardMapMembership) append, and it
// cannot duplicate an id a concurrent writer just added. Only type=task cards
// are adopted: scope.include is a task-card list, and allTaskCardsDone treats
// every entry as a task.
func (a *Actor) adoptOrphanTaskCards(mapID string) {
	mapCard, err := a.store.Get(mapID)
	if err != nil || mapCard.Type != "workflow" {
		return
	}

	a.includeAppendMu.Lock()
	defer a.includeAppendMu.Unlock()
	// Fresh read inside the lock: a concurrent create_task_card or heal may
	// have appended between the type check above and the lock.
	mapCard, err = a.store.Get(mapID)
	if err != nil {
		return
	}
	inInclude := make(map[string]bool)
	for _, id := range scopeIncludeIDs(mapCard) {
		inInclude[id] = true
	}
	cards, err := a.store.List()
	if err != nil {
		return
	}
	var adopted []string
	for _, c := range cards {
		if c.Title == mapID || c.Type != "task" || c.Parent != mapID || inInclude[c.Title] {
			continue
		}
		adopted = append(adopted, c.Title)
	}
	if len(adopted) == 0 {
		return
	}
	raw := mapCard.Raw
	for _, id := range adopted {
		raw = appendIncludeID(raw, id)
	}
	raw = setCardStatusInRaw(raw, mapCard.Status)
	if err := validateCard(mapID, raw); err != nil {
		return
	}
	if err := a.store.Save(&CardRecord{Title: mapID, Raw: raw}); err != nil {
		return
	}
	if a.logger != nil {
		a.logger.Warn("workflow: adopted orphan task cards into map scope (created outside wiki_create_task_card)",
			"map", mapID, "tasks", adopted)
	}
}

// projectTopoToFrontmatter writes the depends_on edges for taskID (derived from
// the authoritative graph g) back to the task card's frontmatter, making
// data.depends_on a read-only projection of the graph. Called after every
// topology mutation so frontmatter stays human-readable and backward compatible
// with tools that still parse it.
func (a *Actor) projectTopoToFrontmatter(taskID string, g WorkflowTopoGraph) error {
	deps := topoDependencies(&g, taskID)
	card, err := a.store.Get(taskID)
	if err != nil {
		return err
	}
	raw := setDependsOnInDataBlock(card.Raw, deps)
	raw = ensureCardMeta(taskID, raw, time.Now().UTC().Format(time.RFC3339))
	if err := validateCard(taskID, raw); err != nil {
		return err
	}
	return a.store.Save(&CardRecord{Title: taskID, Raw: raw})
}
