package runtime

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/gospore/projection"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/resource"
	"github.com/qomos-w/gospore/schema"
	spore "github.com/qomos-w/spore/schema"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// TopologyKey is the resource key for the topology provider.
var TopologyKey = resource.Key[TopologyProvider]{Name: "topology"}

// TopologyProvider returns a cached snapshot of the live actor tree topology.
// The snapshot is rebuilt only when the underlying tree changes (spawn/stop).
type TopologyProvider interface {
	Snapshot() []ActorNode
	// OnChange registers a callback invoked when the topology changes.
	OnChange(callback func())

	// UnifiedGraph returns the current actor topology as a UnifiedGraph,
	// rebuilding lazily if the tree has changed since the last call.
	UnifiedGraph() domain.UnifiedGraph
	// CurrentEpoch returns the current topology epoch counter.
	CurrentEpoch() int32
	// Sync returns the data a client needs to reach the current epoch from
	// clientEpoch. Implemented internally by TopologyHistory.
	Sync(clientEpoch int32) domain.TopologySyncResp
	// HistoryEntries returns metadata for all stored patches.
	HistoryEntries() []domain.TopologyHistoryEntry
	// OnEpochChange registers a callback invoked when the topology epoch
	// advances (i.e. a patch was generated).
	OnEpochChange(callback func(epoch int32, patch *domain.GraphPatch))
	// EnablePush activates push-model epoch advancement. Before this call,
	// markDirty only sets the dirty flag; after this call, markDirty also
	// triggers UnifiedGraph immediately. Callers (observation.OnStart) should
	// invoke this AFTER registering their OnEpochChange callback so the
	// initial epoch includes all actors and the callback fires.
	EnablePush()
}

// EventSlot describes one event kind registered by an actor.
type EventSlot struct {
	Kind    string
	Tooltip string
	Fields  []domain.CallableParam
}

// ActorNode describes one actor in the topology snapshot.
type ActorNode struct {
	ID       string
	ParentID string
	Label    string
	// Kind is the actor's Type() string (e.g. "agent", "workspace", "turn").
	// Used as a callable-name prefix so callers can target convention-prefixed
	// callables like "<kind>.inspect.pages".
	Kind       string
	Callables  []domain.CallableInterface
	Components []ComponentSlot
	Events     []EventSlot
}

// ComponentSlot describes a projection component attached to an actor.
type ComponentSlot struct {
	Name     string
	TypeName string
	Fields   []domain.CallableParam
}

// nodeMeta holds the ID, parent ID, and type for one actor.
type nodeMeta struct {
	id        string
	parentID  string
	actorType string
}

const (
	snapshotInterval = 200
	maxSnapshots     = 5
	// maxPatches caps the patch history retained for incremental client
	// sync. Clients older than the oldest patch fall back to a full graph
	// snapshot (see Sync). Without this cap, long-running sessions with
	// frequent topology changes accumulate patches indefinitely.
	maxPatches = 500
)

type snapshotEntry struct {
	Epoch int32
	Graph domain.UnifiedGraph
}

// topologyProvider implements TopologyProvider using the gospore App.
// It caches the snapshot and only rebuilds when the tree changes.
type topologyProvider struct {
	app    app.App
	mu     sync.RWMutex
	dirty  atomic.Bool
	cached []ActorNode

	// Epoch history (migrated from observation.TopologyHistory).
	currentEpoch int32
	currentGraph domain.UnifiedGraph
	snapshots    []snapshotEntry
	patches      []domain.GraphPatch

	// Callbacks.
	onChange      []func()
	onEpochChange []func(epoch int32, patch *domain.GraphPatch)

	// pushEnabled gates push-model epoch advancement. False during batch
	// init so epochs are not created before the rootActor registers its
	// OnEpochChange callback. Flipped to true by EnablePush().
	pushEnabled atomic.Bool

	// agentSubs holds Watch subscriptions for agent actors. Subscribing
	// makes the Cell's HasSubscribers gate pass, so agent components
	// (DisplayName, etc.) are auto-published to the projection store.
	agentSubs map[string]actor.Subscription[projection.Update]
	// mcpSubs holds Watch subscriptions for mcpinstance actors. Subscribing
	// makes each instance's ServerViews component (identity + connection
	// state) publish, so mcp-server nodes can be enriched without blocking
	// child invokes.
	mcpSubs map[string]actor.Subscription[projection.Update]
	// workspaceSub watches the workspace actor's projection.
	workspaceSub actor.Subscription[projection.Update]
	// aimanagerSub watches the aimanager actor's projection.
	aimanagerSub actor.Subscription[projection.Update]
	// subMu protects agentSubs and service projection subscriptions.
	subMu sync.Mutex

	// rebuildMu serializes the rebuild path in UnifiedGraph so that
	// concurrent markDirty calls from multiple watch goroutines don't
	// overlap and mutate shared state (e.g. Detail maps from
	// currentGraph) at the same time.
	rebuildMu sync.Mutex

	// manifest-derived caches: rebuilt when the manifest fingerprint
	// changes. The fingerprint includes ServiceName content so backfills
	// (Register before RegisterDomain) invalidate the cache.
	manifestFP         string
	callablesByKind    map[string][]domain.CallableInterface
	componentsByKind   map[string][]ComponentSlot
	eventsByKind       map[string][]EventSlot
	cardByKind         map[string]*domain.TopologyCard
	manifestCacheMu    sync.Mutex
}

func newTopologyProvider(appObj app.App) *topologyProvider {
	tp := &topologyProvider{app: appObj, agentSubs: make(map[string]actor.Subscription[projection.Update]), mcpSubs: make(map[string]actor.Subscription[projection.Update])}
	tp.dirty.Store(true) // force initial build
	return tp
}

func (t *topologyProvider) OnChange(callback func()) {
	t.mu.Lock()
	t.onChange = append(t.onChange, callback)
	t.mu.Unlock()
}

func (t *topologyProvider) OnEpochChange(callback func(epoch int32, patch *domain.GraphPatch)) {
	t.mu.Lock()
	t.onEpochChange = append(t.onEpochChange, callback)
	t.mu.Unlock()
}

// Close shuts down all agent projection subscriptions. Callers must call
// Close when the topology provider is no longer needed to avoid leaking
// goroutines.
func (t *topologyProvider) Close() error {
	t.subMu.Lock()
	defer t.subMu.Unlock()
	for id, sub := range t.agentSubs {
		sub.Close()
		delete(t.agentSubs, id)
	}
	if t.workspaceSub != nil {
		t.workspaceSub.Close()
		t.workspaceSub = nil
	}
	if t.aimanagerSub != nil {
		t.aimanagerSub.Close()
		t.aimanagerSub = nil
	}
	for id, sub := range t.mcpSubs {
		sub.Close()
		delete(t.mcpSubs, id)
	}
	return nil
}

func (t *topologyProvider) fireChange() {
	t.mu.RLock()
	cbs := make([]func(), len(t.onChange))
	copy(cbs, t.onChange)
	t.mu.RUnlock()
	for _, cb := range cbs {
		cb()
	}
}

func (t *topologyProvider) fireEpochChange(epoch int32, patch *domain.GraphPatch) {
	t.mu.RLock()
	cbs := make([]func(int32, *domain.GraphPatch), len(t.onEpochChange))
	copy(cbs, t.onEpochChange)
	t.mu.RUnlock()
	for _, cb := range cbs {
		cb(epoch, patch)
	}
}

// markDirty is registered as a tree.OnChange callback.
// During batch init (pushEnabled=false) it only sets the dirty flag.
// After EnablePush(), every tree mutation immediately rebuilds the graph
// and advances the epoch so subscribers see changes instantly.
func (t *topologyProvider) markDirty() {
	t.dirty.Store(true)
	t.fireChange()
	if t.pushEnabled.Load() {
		t.UnifiedGraph()
	}
}

func (t *topologyProvider) EnablePush() {
	t.pushEnabled.Store(true)
	t.ensureServiceSubscriptions()
	t.UnifiedGraph()
}

func (t *topologyProvider) Snapshot() []ActorNode {
	if t.app == nil {
		return nil
	}
	// Even when the tree hasn't changed (dirty=false), the manifest's
	// ServiceName fields may have been backfilled by RegisterDomain after
	// the initial export. Re-check the manifest fingerprint to catch that.
	if !t.dirty.Load() {
		if gm, err := t.app.ExportGosporeManifest(); err == nil {
			t.manifestCacheMu.Lock()
			stale := manifestFingerprintOf(gm) != t.manifestFP
			t.manifestCacheMu.Unlock()
			if stale {
				t.dirty.Store(true)
			}
		}
	}
	if !t.dirty.Load() {
		t.mu.RLock()
		cached := t.cached
		t.mu.RUnlock()
		return cached
	}
	result := t.rebuild()
	t.mu.Lock()
	t.cached = result
	t.dirty.Store(false)
	t.mu.Unlock()
	return result
}

// CurrentEpoch returns the current topology epoch.
func (t *topologyProvider) CurrentEpoch() int32 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentEpoch
}

// UnifiedGraph returns the current actor topology as a UnifiedGraph.
// If the tree has changed since the last call, it rebuilds the snapshot,
// computes the incremental patch, updates epoch history, and fires
// OnEpochChange callbacks.
func (t *topologyProvider) UnifiedGraph() domain.UnifiedGraph {
	if t.app == nil {
		return domain.UnifiedGraph{}
	}

	// Fast path: not dirty, return cached graph under read lock.
	if !t.dirty.Load() {
		t.mu.RLock()
		g := t.currentGraph
		t.mu.RUnlock()
		return g
	}

	// Serialize rebuilds so concurrent markDirty calls from multiple watch
	// goroutines don't overlap and mutate shared state.
	t.rebuildMu.Lock()
	defer t.rebuildMu.Unlock()

	// Re-check dirty after acquiring lock — another goroutine may have
	// already rebuilt.
	if !t.dirty.Load() {
		t.mu.RLock()
		g := t.currentGraph
		t.mu.RUnlock()
		return g
	}

	// Rebuild both the ActorNode snapshot and the UnifiedGraph.
	nodes := t.rebuild()
	graph := t.rebuildGraph(nodes)

	t.mu.Lock()
	prev := t.currentGraph
	var patch *domain.GraphPatch
	var epoch int32
	if !graphsEqual(prev, graph) {
		t.currentEpoch++
		p := diffGraphs(prev, graph)
		p.Epoch = t.currentEpoch
		p.Timestamp = time.Now().UTC().Format(time.RFC3339)
		t.patches = append(t.patches, p)
		t.currentGraph = graph
		patch = &p
		epoch = t.currentEpoch

		// Snapshot at interval; prune every epoch so patch history
		// stays bounded between snapshot boundaries.
		if t.currentEpoch%snapshotInterval == 1 {
			t.snapshots = append(t.snapshots, snapshotEntry{
				Epoch: t.currentEpoch,
				Graph: deepCopyGraph(graph),
			})
		}
		t.prune()
	}
	t.cached = nodes
	t.dirty.Store(false)
	t.mu.Unlock()

	if patch != nil {
		t.fireEpochChange(epoch, patch)
	}
	return graph
}

// Sync returns the data a client needs to reach the current epoch from
// clientEpoch. Preference order:
//  1. Full current graph (clientEpoch == 0, client has no base)
//  2. Patches only (if patches cover clientEpoch+1 onwards)
//  3. Oldest snapshot + patches after it (if client is too old for patches)
//  4. Full current graph (fallback)
func (t *topologyProvider) Sync(clientEpoch int32) domain.TopologySyncResp {
	// Ensure graph is up-to-date before computing sync response.
	_ = t.UnifiedGraph()

	t.mu.RLock()
	defer t.mu.RUnlock()

	resp := domain.TopologySyncResp{
		CurrentEpoch: t.currentEpoch,
		Patches:      []domain.GraphPatch{},
	}

	// Collect patches after clientEpoch.
	var patches []domain.GraphPatch
	for _, p := range t.patches {
		if p.Epoch > clientEpoch {
			patches = append(patches, p)
		}
	}

	// Client has no base graph — always return full snapshot.
	if clientEpoch == 0 {
		resp.Snapshot = &domain.UnifiedGraph{}
		*resp.Snapshot = t.currentGraph
		// The snapshot already IS the current graph, so full historical
		// patches are redundant for graph reconstruction — and expensive: each
		// patch's UpdatedNodes carries complete node descriptions (one heavy
		// session replayed ~46MB of them to a fresh client). Clients only use
		// epoch-0 patches for lifecycle facts, so send ID-only stubs.
		stubs := make([]domain.GraphPatch, len(patches))
		for i, p := range patches {
			stub := domain.GraphPatch{Epoch: p.Epoch, Timestamp: p.Timestamp, RemovedNodes: p.RemovedNodes}
			if len(p.AddedNodes) > 0 {
				stub.AddedNodes = make([]domain.UnifiedGraphNode, len(p.AddedNodes))
				for j, n := range p.AddedNodes {
					stub.AddedNodes[j] = domain.UnifiedGraphNode{ID: n.ID}
				}
			}
			stubs[i] = stub
		}
		resp.Patches = stubs
		return resp
	}

	if t.currentEpoch == 0 || clientEpoch >= t.currentEpoch {
		return resp
	}

	// If patches cover the gap, return patches only.
	if len(patches) > 0 && patches[0].Epoch == clientEpoch+1 {
		resp.Patches = patches
		return resp
	}

	// Client is too old for patches alone. Find the best snapshot.
	if len(t.snapshots) > 0 {
		best := t.snapshots[0]
		for _, s := range t.snapshots {
			if s.Epoch > clientEpoch {
				best = s
				break
			}
			best = s
		}
		resp.Snapshot = &domain.UnifiedGraph{}
		*resp.Snapshot = best.Graph
		var afterSnap []domain.GraphPatch
		for _, p := range t.patches {
			if p.Epoch > best.Epoch {
				afterSnap = append(afterSnap, p)
			}
		}
		resp.Patches = afterSnap
		return resp
	}

	// No snapshots — return current graph.
	resp.Snapshot = &domain.UnifiedGraph{}
	*resp.Snapshot = t.currentGraph
	return resp
}

// HistoryEntries returns metadata for all stored patches.
func (t *topologyProvider) HistoryEntries() []domain.TopologyHistoryEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	entries := make([]domain.TopologyHistoryEntry, len(t.patches))
	for i, p := range t.patches {
		entries[i] = domain.TopologyHistoryEntry{
			Epoch:       p.Epoch,
			Timestamp:   p.Timestamp,
			ChangeCount: int32(len(p.AddedNodes) + len(p.RemovedNodes) + len(p.UpdatedNodes)),
		}
	}
	return entries
}

func (t *topologyProvider) prune() {
	// Hard cap on patch history. Clients older than the oldest retained
	// patch fall back to a full snapshot via Sync.
	if len(t.patches) > maxPatches {
		cut := len(t.patches) - maxPatches
		t.patches = t.patches[cut:]
	}
	for len(t.snapshots) > maxSnapshots {
		t.snapshots = t.snapshots[1:]
		newOldest := t.snapshots[0].Epoch
		// Keep patches from newOldest onwards (including the snapshot
		// epoch patch). Clients just before newOldest can apply patches
		// without pulling a full snapshot.
		cut := 0
		for i, p := range t.patches {
			if p.Epoch >= newOldest {
				cut = i
				break
			}
			cut = i + 1
		}
		t.patches = t.patches[cut:]
	}
}

func (t *topologyProvider) rebuild() []ActorNode {
	// Walk the actor tree for topology.
	actors := make(map[string]nodeMeta)
	t.app.Tree().Walk(func(r ref.Ref) bool {
		actorType := t.app.ActorType(r.ID())
		if actorType == "" {
			return true // actor cell gone — skip dead refs
		}
		parentID := ""
		if parent, ok := t.app.Tree().Parent(r); ok && parent != nil && !parent.ID().IsZero() {
			parentID = parent.ID().String()
		}
		actors[r.ID().String()] = nodeMeta{id: r.ID().String(), parentID: parentID, actorType: actorType}
		return true
	})

	// Get manifest for callable/component/event info.
	gm, err := t.app.ExportGosporeManifest()
	if err != nil {
		return buildBasicNodes(actors)
	}

	// Build lookup tables from manifest (cached by fingerprint).
	callablesByKind, componentsByKind, eventsByKind := t.ensureManifestCache(gm)

	// Merge into ActorNode list sorted by actor ID.
	ids := make([]string, 0, len(actors))
	for id := range actors {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make([]ActorNode, 0, len(ids))
	for _, id := range ids {
		meta := actors[id]
		node := ActorNode{
			ID:         meta.id,
			ParentID:   meta.parentID,
			Label:      meta.actorType,
			Kind:       meta.actorType,
			Callables:  callablesByKind[meta.actorType],
			Components: componentsByKind[meta.actorType],
			Events:     eventsByKind[meta.actorType],
		}
		result = append(result, node)
	}
	return result
}

// manifestFingerprintOf returns a stable signature for a manifest's
// callable / component / event shape. Counts detect schema regeneration
// (new/removed callables); the Service hash detects additive ServiceName
// changes that happen when the cell backfills invoker.ServiceName after
// RegisterDomain (Register before RegisterDomain ordering). Without the
// Service hash the cache would serve stale empty ServiceNames forever.
func manifestFingerprintOf(gm schema.GosporeManifest) string {
	var svc strings.Builder
	for _, c := range gm.Callables {
		svc.WriteString(c.Service)
		svc.WriteByte(0)
	}
	return fmt.Sprintf("c=%d|p=%d|e=%d|s=%d|svc=%d:%s",
		len(gm.Callables), len(gm.Projections), len(gm.Events), len(gm.Manifest.Schemas),
		svc.Len(), svc.String())
}

// ensureManifestCache returns the cached group maps for gm, rebuilding them
// only when the manifest fingerprint changes. All three maps are pure
// functions of the manifest, so caching by fingerprint is correct.
// Card cache is invalidated alongside since cards reference these maps.
func (t *topologyProvider) ensureManifestCache(gm schema.GosporeManifest) (
	map[string][]domain.CallableInterface,
	map[string][]ComponentSlot,
	map[string][]EventSlot,
) {
	fp := manifestFingerprintOf(gm)
	t.manifestCacheMu.Lock()
	defer t.manifestCacheMu.Unlock()
	if fp == t.manifestFP && t.callablesByKind != nil {
		return t.callablesByKind, t.componentsByKind, t.eventsByKind
	}
	t.callablesByKind = groupCallablesByKind(gm)
	t.componentsByKind = groupComponentsByKind(gm, gm.Manifest)
	t.eventsByKind = groupEventsByKind(gm)
	t.cardByKind = make(map[string]*domain.TopologyCard)
	t.manifestFP = fp
	return t.callablesByKind, t.componentsByKind, t.eventsByKind
}

// cardForActor returns the cached TopologyCard for an actor's kind. Cards
// depend only on the actor's Kind (Callables/Components/Events are looked
// up by kind), so one card per kind is correct. The cache is invalidated
// alongside the manifest cache in ensureManifestCache.
func (t *topologyProvider) cardForActor(an ActorNode) *domain.TopologyCard {
	t.manifestCacheMu.Lock()
	defer t.manifestCacheMu.Unlock()
	if c, ok := t.cardByKind[an.Kind]; ok {
		return c
	}
	c := buildCard(an)
	t.cardByKind[an.Kind] = c
	return c
}

// rebuildGraph converts an ActorNode snapshot into a UnifiedGraph.
// Missing parent nodes are synthesised as placeholder nodes so the DAG
// always has roots. Existing node statuses are preserved from the previous
// graph so that enriched runtime states (turn status, agent status) are
// not overwritten by topology patches.
func (t *topologyProvider) rebuildGraph(snapshot []ActorNode) domain.UnifiedGraph {
	// Ensure service projection subscriptions are established once. These
	// single-instance actors publish workspace/aimanager state used to
	// enrich topology nodes without synchronous callables.
	t.ensureServiceSubscriptions()

	// Index previous node statuses to preserve enriched states.
	t.mu.RLock()
	prevNodes := indexNodes(t.currentGraph.Nodes)
	t.mu.RUnlock()

	// Build set of existing actor IDs and collect missing parents.
	existing := make(map[string]bool, len(snapshot))
	for _, an := range snapshot {
		existing[an.ID] = true
	}
	placeholders := make(map[string]domain.UnifiedGraphNode)
	for _, an := range snapshot {
		if an.ParentID != "" && !existing[an.ParentID] {
			if _, ok := placeholders[an.ParentID]; !ok {
				status := "running"
				if prev, ok := prevNodes[an.ParentID]; ok && prev.Status != "" {
					status = prev.Status
				}
				placeholders[an.ParentID] = domain.UnifiedGraphNode{
					ID:       an.ParentID,
					Kind:     "actor",
					Label:    "actor",
					Status:   status,
					ParentID: "",
				}
			}
		}
	}

	nodes := make([]domain.UnifiedGraphNode, 0, len(snapshot)+len(placeholders))
	edges := make([]domain.UnifiedGraphEdge, 0, len(snapshot))

	// Emit placeholder root nodes first so roots precede children.
	for _, n := range placeholders {
		nodes = append(nodes, n)
	}

	for _, an := range snapshot {
		status := "running"
		if an.Kind == "agent" {
			// Agent nodes default to idle until the agent publishes its turnState
			// component. The generic "running" default made idle agents look active
			// after restart or between turns.
			status = "idle"
		} else if an.Kind == "mcpinstance" {
			// MCP servers default to disconnected until the instance's published
			// ServerViews projection reports a live status.
			status = "disconnected"
		}
		if prev, ok := prevNodes[an.ID]; ok && prev.Status != "" {
			status = prev.Status
		}
		// Preserve enriched Detail from previous graph (workspace metadata,
		// agent details, etc.) so topology-only rebuilds don't clear data set
		// by projection-driven enrichment. Deep-copy so concurrent rebuilds
		// never share the same map.
		var detail map[string]any
		if prev, ok := prevNodes[an.ID]; ok && prev.Detail != nil {
			detail = copyDetail(prev.Detail)
		}
		// Read label from projection store for actor types that publish
		// their own DAG identity via gospore components.
		label := an.Label
		projLabel := readProjectionLabel(t.app, an)
		if projLabel != "" {
			label = projLabel
		} else if prev, ok := prevNodes[an.ID]; ok && prev.Label != "" && prev.Label != an.Kind {
			// Preserve enriched Label from previous graph for types
			// that are still enriched externally (workspace, etc.).
			label = prev.Label
		}
		// mcpinstance actors surface as their own node kind so the frontend
		// can give MCP servers dedicated visuals. The underlying actor type
		// stays "mcpinstance" for actor-level tooling (inspect, context menus).
		kind := an.Kind
		if an.Kind == "mcpinstance" {
			kind = "mcp-server"
		}
		node := domain.UnifiedGraphNode{
			ID:        an.ID,
			Kind:      kind,
			ActorType: an.Kind,
			Label:     label,
			ActorID:   an.ID,
			Status:    status,
			Detail:    detail,
			ParentID:  an.ParentID,
		}

		// Add callable interfaces.
		if len(an.Callables) > 0 {
			node.Interfaces = an.Callables
		}

		// Build card with detail sections (cached per kind).
		card := t.cardForActor(an)
		if card != nil {
			node.Card = card
		}

		nodes = append(nodes, node)

		// Add edge from parent.
		if an.ParentID != "" {
			edges = append(edges, domain.UnifiedGraphEdge{
				ID:   "e:" + an.ID,
				From: an.ParentID,
				To:   an.ID,
				Kind: "child",
			})
		}
	}

	// Enrich nodes from service projections before returning. This replaces
	// synchronous invoke-based enrichment for workspace metadata and
	// aggregator names.
	nodes = t.fillFromWorkspaceProjection(nodes)
	nodes = t.fillFromAimanagerProjection(nodes)
	nodes = t.fillFromAgentProjection(nodes)
	nodes = t.fillFromMcpProjection(nodes)

	// Subscribe to agent projections so the Cell publishes component
	// state (DisplayName, etc.) via applyProjectionIfObserved.
	t.syncAgentSubscriptions(snapshot)
	// Same for mcpinstance projections (ServerViews component) so mcp-server
	// nodes carry live identity + connection state.
	t.syncMcpSubscriptions(snapshot)

	// Override physical project→agent child edges with logical agent_child
	// edges for workflow agents that have a ParentAgentId. This rewrites the
	// node ParentId so the frontend hierarchy places children below their
	// logical parent agent, not below the project actor.
	nodes, edges = t.applyAgentChildEdges(nodes, edges)

	// Model the agent→mcp-server capability: every agent can call the tools
	// of every connected MCP server (resolveTools injects connected servers'
	// tools into agent ToolSpecs; per-agent filtering will follow per-agent
	// MCP config).
	nodes, edges = t.addMcpCallEdges(nodes, edges)

	return domain.UnifiedGraph{
		Version: fmt.Sprintf("%d", time.Now().Unix()),
		Nodes:   nodes,
		Edges:   edges,
	}
}

// fillFromWorkspaceProjection enriches project/agent nodes from the workspace
// actor's published component fields (Mounts, Agents). This avoids synchronous
// workspace.list_project / workspace.agents invokes in the sync callable path.
func (t *topologyProvider) fillFromWorkspaceProjection(nodes []domain.UnifiedGraphNode) []domain.UnifiedGraphNode {
	workspaceRef, ok := t.app.LookupService("workspace")
	if !ok {
		return nodes
	}
	projStore := t.app.Projections()
	if projStore == nil {
		return nodes
	}

	var mounts []domain.ProjectRef
	if vp, ok := projStore.GetField(workspaceRef.ID(), "mounts"); ok {
		mounts = decodeProjectRefs(vp.Fields["value"])
	}

	var agents []domain.AgentRef
	if vp, ok := projStore.GetField(workspaceRef.ID(), "agents"); ok {
		agents = decodeAgentRefs(vp.Fields["value"])
	}

	idxByID := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if n.ID != "" {
			idxByID[n.ID] = i
		}
	}

	for _, m := range mounts {
		if m.ActorID == "" {
			continue
		}
		idx, ok := idxByID[m.ActorID]
		if !ok {
			continue
		}
		nodes[idx].Label = m.Name
		if nodes[idx].Detail == nil {
			nodes[idx].Detail = make(map[string]any)
		}
		nodes[idx].Detail["mounts"] = m.Mounts
		nodes[idx].Detail["project_id"] = m.ActorID
		if m.Path != "" {
			nodes[idx].Detail["root_path"] = m.Path
		}
	}

	for _, ag := range agents {
		if ag.ActorID == "" {
			continue
		}
		idx, ok := idxByID[ag.ActorID]
		if !ok {
			continue
		}
		if nodes[idx].Label == nodes[idx].Kind && ag.DisplayName != "" {
			nodes[idx].Label = ag.DisplayName
		}
		if nodes[idx].Detail == nil {
			nodes[idx].Detail = make(map[string]any)
		}
		nodes[idx].Detail["agent_kind"] = ag.AgentKind
		nodes[idx].Detail["agent_spawn_name"] = ag.ID
		if ag.ProjectID != "" {
			nodes[idx].Detail["project_id"] = ag.ProjectID
		}
	}

	return nodes
}

// fillFromAimanagerProjection sets aiaggregator node labels from the
// aimanager actor's published AggregatorNames component.
func (t *topologyProvider) fillFromAimanagerProjection(nodes []domain.UnifiedGraphNode) []domain.UnifiedGraphNode {
	aimanagerRef, ok := t.app.LookupService("aimanager")
	if !ok {
		return nodes
	}
	projStore := t.app.Projections()
	if projStore == nil {
		return nodes
	}

	var names map[string]string
	if vp, ok := projStore.GetField(aimanagerRef.ID(), "aggregatorNames"); ok {
		if v, ok := vp.Fields["value"].(map[string]string); ok {
			names = v
		}
	}
	if len(names) == 0 {
		return nodes
	}

	for i, node := range nodes {
		if node.Kind != "aiaggregator" {
			continue
		}
		if name, ok := names[node.ID]; ok && name != "" {
			nodes[i].Label = name
		}
	}

	return nodes
}

// fillFromAgentProjection enriches agent nodes from the agent's published
// turn-state components (TurnState, TurnPauseKind, ActiveTurnRef). This
// replaces synchronous agent.turn.status invokes for turn-node enrichment.
func (t *topologyProvider) fillFromAgentProjection(nodes []domain.UnifiedGraphNode) []domain.UnifiedGraphNode {
	projStore := t.app.Projections()
	if projStore == nil {
		return nodes
	}

	for i, node := range nodes {
		if node.Kind != "agent" || node.ID == "" {
			continue
		}
		aid, err := parseActorID(node.ID)
		if err != nil {
			continue
		}

		var turnState, turnPauseKind string
		if vp, ok := projStore.GetField(aid, "turnState"); ok {
			turnState, _ = vp.Fields["value"].(string)
		}
		if vp, ok := projStore.GetField(aid, "turnPauseKind"); ok {
			turnPauseKind, _ = vp.Fields["value"].(string)
		}

		// Use the agent's published turn state whenever it is available. The
		// component is published as "idle" when no turn is active, so the node
		// no longer falls back to the generic "running" default between turns or
		// after a completed turn.
		if turnState != "" {
			nodes[i].Status = turnState
		}
		if turnPauseKind != "" {
			if nodes[i].Detail == nil {
				nodes[i].Detail = make(map[string]any)
			}
			nodes[i].Detail["pause_kind"] = turnPauseKind
		}
	}

	return nodes
}

// fillFromMcpProjection enriches mcp-server nodes from each mcpinstance
// child's published ServerViews component (identity + live connection
// state). The projection is a read-safe view — env/header values never
// leave the instance actor, so nothing secret reaches the graph.
func (t *topologyProvider) fillFromMcpProjection(nodes []domain.UnifiedGraphNode) []domain.UnifiedGraphNode {
	projStore := t.app.Projections()
	if projStore == nil {
		return nodes
	}

	for i, node := range nodes {
		if node.Kind != "mcp-server" || node.ID == "" {
			continue
		}
		aid, err := parseActorID(node.ID)
		if err != nil {
			continue
		}
		vp, ok := projStore.GetField(aid, "serverViews")
		if !ok {
			continue
		}
		views := decodeMcpServerViews(vp.Fields["value"])
		if len(views) == 0 {
			continue
		}
		view := views[0]
		if view.Name != "" {
			nodes[i].Label = view.Name
		}
		nodes[i].Status = mcpServerNodeStatus(view.Status)
		if nodes[i].Detail == nil {
			nodes[i].Detail = make(map[string]any)
		}
		nodes[i].Detail["connected"] = view.Status.Connected
		nodes[i].Detail["tool_count"] = int(view.Status.ToolCount)
		if view.Status.Error != "" {
			nodes[i].Detail["error"] = view.Status.Error
		}
		if view.Transport != "" {
			nodes[i].Detail["transport"] = view.Transport
		}
		nodes[i].Detail["enabled"] = view.Enabled
	}

	return nodes
}

// syncAgentSubscriptions ensures the topology provider is subscribed to
// every agent actor's projection store. Subscribing makes the Cell's
// HasSubscribers check pass, so agent components are auto-published.
// Each subscription is drained in a background goroutine; projection
// updates mark the topology dirty so label changes are reflected in
// real-time patches.
func (t *topologyProvider) syncAgentSubscriptions(snapshot []ActorNode) {
	projStore := t.app.Projections()
	if projStore == nil {
		return
	}

	t.subMu.Lock()
	defer t.subMu.Unlock()

	active := make(map[string]bool, len(snapshot))
	for _, an := range snapshot {
		if an.Kind != "agent" || an.ID == "" {
			continue
		}
		active[an.ID] = true
		if _, ok := t.agentSubs[an.ID]; ok {
			continue
		}
		aid, err := parseActorID(an.ID)
		if err != nil {
			continue
		}
		sub, err := projStore.Watch(aid)
		if err != nil {
			continue
		}
		t.agentSubs[an.ID] = sub
		go t.watchAgentProjection(sub)
	}

	for id, sub := range t.agentSubs {
		if !active[id] {
			sub.Close()
			delete(t.agentSubs, id)
		}
	}
}

// syncMcpSubscriptions ensures the topology provider is subscribed to every
// mcpinstance actor's projection store. Subscribing makes the Cell's
// HasSubscribers gate pass, so the ServerViews component is published after
// the next invoke; projection updates mark the topology dirty so mcp-server
// identity/status changes reach the graph without blocking child invokes.
func (t *topologyProvider) syncMcpSubscriptions(snapshot []ActorNode) {
	projStore := t.app.Projections()
	if projStore == nil {
		return
	}

	t.subMu.Lock()
	defer t.subMu.Unlock()

	active := make(map[string]bool, len(snapshot))
	for _, an := range snapshot {
		if an.Kind != "mcpinstance" || an.ID == "" {
			continue
		}
		active[an.ID] = true
		if _, ok := t.mcpSubs[an.ID]; ok {
			continue
		}
		aid, err := parseActorID(an.ID)
		if err != nil {
			continue
		}
		sub, err := projStore.Watch(aid)
		if err != nil {
			continue
		}
		t.mcpSubs[an.ID] = sub
		go t.watchServiceProjection(sub)
	}

	for id, sub := range t.mcpSubs {
		if !active[id] {
			sub.Close()
			delete(t.mcpSubs, id)
		}
	}
}

// watchAgentProjection drains a projection subscription and marks the
// topology dirty on every update. The goroutine exits when the
// subscription is closed.
func (t *topologyProvider) watchAgentProjection(sub actor.Subscription[projection.Update]) {
	for {
		_, err := sub.Recv()
		if err != nil {
			return
		}
		t.markDirty()
	}
}

// ensureServiceSubscriptions watches the single-instance service actors
// (workspace, aimanager) so topology rebuilds pick up their projection
// updates (mounted projects, agents, aggregator names) without polling
// callables.
func (t *topologyProvider) ensureServiceSubscriptions() {
	t.subMu.Lock()
	defer t.subMu.Unlock()

	projStore := t.app.Projections()
	if projStore == nil {
		return
	}

	if t.workspaceSub == nil {
		if r, ok := t.app.LookupService("workspace"); ok {
			if sub, err := projStore.Watch(r.ID()); err == nil {
				t.workspaceSub = sub
				go t.watchServiceProjection(sub)
			}
		}
	}

	if t.aimanagerSub == nil {
		if r, ok := t.app.LookupService("aimanager"); ok {
			if sub, err := projStore.Watch(r.ID()); err == nil {
				t.aimanagerSub = sub
				go t.watchServiceProjection(sub)
			}
		}
	}
}

// watchServiceProjection drains a service projection subscription and marks
// the topology dirty on every update.
func (t *topologyProvider) watchServiceProjection(sub actor.Subscription[projection.Update]) {
	for {
		_, err := sub.Recv()
		if err != nil {
			return
		}
		t.markDirty()
	}
}

func (t *topologyProvider) rebuildActorNodes() []ActorNode {
	return t.rebuild()
}

// buildBasicNodes creates ActorNodes without callable info (fallback).
func buildBasicNodes(actors map[string]nodeMeta) []ActorNode {
	ids := make([]string, 0, len(actors))
	for id := range actors {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make([]ActorNode, 0, len(ids))
	for _, id := range ids {
		meta := actors[id]
		result = append(result, ActorNode{
			ID:       meta.id,
			ParentID: meta.parentID,
			Label:    meta.actorType,
			Kind:     meta.actorType,
		})
	}
	return result
}

// BuildCard constructs a TopologyCard with expandable sections for an actor.
func BuildCard(an ActorNode) *domain.TopologyCard {
	return buildCard(an)
}

func buildCard(an ActorNode) *domain.TopologyCard {
	var sections []domain.TopologyCardSection

	// Basic info section.
	basicRows := []domain.TopologyCardRow{
		{Label: "Name", Value: an.Label},
		{Label: "Kind", Value: an.Kind, Mono: true},
		{Label: "ID", Value: an.ID, Mono: true, Truncate: true},
	}
	sections = append(sections, domain.TopologyCardSection{
		Key:   "basic",
		Title: "Basic Info",
		Rows:  basicRows,
	})

	// Callables section.
	if len(an.Callables) > 0 {
		var rows []domain.TopologyCardRow
		for _, ci := range an.Callables {
			badge := "I"
			if ci.Stream {
				badge = "S"
			}
			row := domain.TopologyCardRow{
				Label:   ci.Name,
				Value:   ci.FinalType,
				Badge:   badge,
				Tooltip: formatCallableTooltip(ci),
			}
			// Expandable rows for request/response/chunk structure.
			var expandable []domain.TopologyCardRow
			addFieldGroup := func(label string, typeName string, badge string, fields []domain.CallableParam) []domain.TopologyCardRow {
				if len(fields) == 0 {
					return nil
				}
				var rows []domain.TopologyCardRow
				for _, p := range fields {
					value := p.Type
					if p.Required {
						value += " (required)"
					}
					rows = append(rows, domain.TopologyCardRow{
						Label:   p.Name,
						Value:   value,
						Mono:    true,
						Tooltip: p.Description,
					})
				}
				return rows
			}
			requestRows := addFieldGroup("request", ci.ReqType, "Q", ci.Params)
			responseRows := addFieldGroup("response", ci.FinalType, "R", ci.FinalFields)
			chunkRows := addFieldGroup("chunk", ci.NextType, "S", ci.NextFields)
			// Only add group row if there are fields.
			if requestRows != nil {
				expandable = append(expandable, domain.TopologyCardRow{
					Label:        "request",
					Value:        ci.ReqType,
					Badge:        "Q",
					Mono:         true,
					ExpandedRows: requestRows,
				})
			}
			if responseRows != nil {
				expandable = append(expandable, domain.TopologyCardRow{
					Label:        "response",
					Value:        ci.FinalType,
					Badge:        "R",
					Mono:         true,
					ExpandedRows: responseRows,
				})
			}
			if chunkRows != nil {
				expandable = append(expandable, domain.TopologyCardRow{
					Label:        "chunk",
					Value:        ci.NextType,
					Badge:        "S",
					Mono:         true,
					ExpandedRows: chunkRows,
				})
			}
			row.ExpandedRows = expandable
			rows = append(rows, row)
		}
		sections = append(sections, domain.TopologyCardSection{
			Key:   "callables",
			Title: fmt.Sprintf("Callables (%d)", len(an.Callables)),
			Rows:  rows,
		})
	}

	// Components section.
	if len(an.Components) > 0 {
		var rows []domain.TopologyCardRow
		for _, comp := range an.Components {
			row := domain.TopologyCardRow{
				Label:   comp.Name,
				Value:   comp.TypeName,
				Mono:    true,
				Tooltip: formatComponentTooltip(comp),
			}
			// Expandable rows for component fields.
			if len(comp.Fields) > 0 {
				var fieldRows []domain.TopologyCardRow
				for _, f := range comp.Fields {
					fieldRows = append(fieldRows, domain.TopologyCardRow{
						Label: f.Name,
						Value: f.Type,
						Mono:  true,
					})
				}
				row.ExpandedRows = fieldRows
			}
			rows = append(rows, row)
		}
		sections = append(sections, domain.TopologyCardSection{
			Key:   "components",
			Title: fmt.Sprintf("Components (%d)", len(an.Components)),
			Rows:  rows,
		})
	}

	// Events section.
	if len(an.Events) > 0 {
		var rows []domain.TopologyCardRow
		for _, slot := range an.Events {
			row := domain.TopologyCardRow{
				Label:   slot.Kind,
				Value:   "event",
				Badge:   "E",
				Tooltip: slot.Tooltip,
			}
			if len(slot.Fields) > 0 {
				var fieldRows []domain.TopologyCardRow
				for _, f := range slot.Fields {
					fieldRows = append(fieldRows, domain.TopologyCardRow{
						Label: f.Name,
						Value: f.Type,
						Mono:  true,
					})
				}
				row.ExpandedRows = fieldRows
			}
			rows = append(rows, row)
		}
		sections = append(sections, domain.TopologyCardSection{
			Key:   "events",
			Title: fmt.Sprintf("Events (%d)", len(an.Events)),
			Rows:  rows,
		})
	}

	if len(sections) == 0 {
		return nil
	}
	return &domain.TopologyCard{
		DetailSections: sections,
	}
}

// groupCallablesByKind maps actor kinds to their CallableInterface list.
// The manifest groups callables by Namespace; we map the first namespace
// segment to actor kind because actor Type() is flat while callables may be
// nested (e.g. project.wiki.* belongs to the project actor).
func groupCallablesByKind(gm schema.GosporeManifest) map[string][]domain.CallableInterface {
	// Build schemaID → ObjectDesc lookup for field extraction.
	objBySchemaID := make(map[uint64]spore.ObjectDesc)
	for _, s := range gm.Manifest.Schemas {
		objBySchemaID[s.SchemaID] = s.Object
	}

	// Deduplicate by full callable name; the manifest may contain repeated
	// registrations from host + SDK codegen bundles.
	slots := make(map[string]map[string]domain.CallableInterface)
	for _, c := range gm.Callables {
		callName := c.Name
		if c.Namespace != "" {
			callName = c.Namespace + "." + c.Name
		}
	ci := domain.CallableInterface{
		Name:          callName,
		Kind:          c.Mode,
		Description:   c.Description,
		FinalType:     typeDescName(c.Final),
		FinalDesc:     c.FinalDesc,
		ChunkDesc:     c.ChunkDesc,
		Stream:        c.Mode == string(spore.CallableModeStreaming),
		Permission:    c.Visibility,
		EffectKind:    c.Effect,
		ServiceName:   c.Service,
		ToolName:      c.ToolName,
		ReqSchemaID:   int32(c.ReqSchemaID),
		FinalSchemaID: int32(c.FinalSchemaID),
	}
		if c.Chunk != nil {
			ci.NextType = typeDescName(*c.Chunk)
		}
		// Extract request type name from request schema.
		if c.ReqSchemaID != 0 {
			if obj, ok := objBySchemaID[c.ReqSchemaID]; ok {
				ci.ReqType = obj.Name
			}
		}
		// Extract params from request schema.
		if c.ReqSchemaID != 0 {
			if obj, ok := objBySchemaID[c.ReqSchemaID]; ok {
				for _, f := range obj.Fields {
					ci.Params = append(ci.Params, domain.CallableParam{
						Name:        f.Name,
						Type:        typeDescName(f.Type),
						Description: f.Description,
						Required:    !f.Optional,
					})
				}
			}
		}
		// Extract response fields from final schema.
		if c.FinalSchemaID != 0 {
			if obj, ok := objBySchemaID[c.FinalSchemaID]; ok {
				for _, f := range obj.Fields {
					ci.FinalFields = append(ci.FinalFields, domain.CallableParam{
						Name:        f.Name,
						Type:        typeDescName(f.Type),
						Description: f.Description,
					})
				}
			}
		}
		// Extract chunk fields from chunk schema for streaming callables.
		if c.ChunkSchemaID != 0 {
			if obj, ok := objBySchemaID[c.ChunkSchemaID]; ok {
				for _, f := range obj.Fields {
					ci.NextFields = append(ci.NextFields, domain.CallableParam{
						Name:        f.Name,
						Type:        typeDescName(f.Type),
						Description: f.Description,
					})
				}
			}
		}
		// Actor Type() is the first segment of the dotted namespace.
		// For actor-local callables (empty namespace) the manifest carries
		// the actor type in ActorType; fall back to that so local callables
		// are grouped under the correct actor kind.
		kind := c.Namespace
		if idx := strings.Index(c.Namespace, "."); idx >= 0 {
			kind = c.Namespace[:idx]
		}
		if kind == "" && c.ActorType != "" {
			kind = c.ActorType
		}
		if slots[kind] == nil {
			slots[kind] = make(map[string]domain.CallableInterface)
		}
		slots[kind][ci.Name] = ci
	}

	result := make(map[string][]domain.CallableInterface, len(slots))
	for kind, byName := range slots {
		list := make([]domain.CallableInterface, 0, len(byName))
		for _, ci := range byName {
			list = append(list, ci)
		}
		// Sort for stable output.
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		result[kind] = list
	}
	return result
}

// groupComponentsByKind maps actor kinds to their ComponentSlot list.
func groupComponentsByKind(gm schema.GosporeManifest, m spore.Manifest) map[string][]ComponentSlot {
	// Build schemaID → (name, fields) lookup.
	type schemaInfo struct {
		name   string
		fields []spore.FieldDesc
	}
	schemaByID := make(map[uint64]schemaInfo)
	for _, s := range m.Schemas {
		schemaByID[s.SchemaID] = schemaInfo{name: s.Name, fields: s.Object.Fields}
	}

	// Deduplicate by (namespace, component); projections can be repeated across
	// host and SDK schema bundles.
	slots := make(map[string]map[string]ComponentSlot)
	for _, p := range gm.Projections {
		info, ok := schemaByID[p.SchemaID]
		if !ok {
			continue
		}
		slot := ComponentSlot{
			Name:     p.Component,
			TypeName: info.name,
		}
		for _, f := range info.fields {
			slot.Fields = append(slot.Fields, domain.CallableParam{
				Name: f.Name,
				Type: typeDescName(f.Type),
			})
		}
		if slots[p.Namespace] == nil {
			slots[p.Namespace] = make(map[string]ComponentSlot)
		}
		slots[p.Namespace][p.Component] = slot
	}

	result := make(map[string][]ComponentSlot, len(slots))
	for ns, byComp := range slots {
		list := make([]ComponentSlot, 0, len(byComp))
		for _, slot := range byComp {
			list = append(list, slot)
		}
		// Sort for stable output.
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		result[ns] = list
	}
	return result
}

// groupEventsByKind maps actor kinds to their event kind list.
// The manifest groups events by Namespace; we map the first namespace segment
// to actor kind because actor Type() is flat while events may be nested
// (e.g. project.wiki.* belongs to the project actor).
func groupEventsByKind(gm schema.GosporeManifest) map[string][]EventSlot {
	// Build schemaID → ObjectDesc lookup for payload field extraction.
	objBySchemaID := make(map[uint64]spore.ObjectDesc)
	for _, s := range gm.Manifest.Schemas {
		objBySchemaID[s.SchemaID] = s.Object
	}

	// Deduplicate by (namespace, kind); events can be repeated across host and
	// SDK schema bundles.
	slots := make(map[string]map[string]EventSlot)
	for _, e := range gm.Events {
		if slots[e.Namespace] == nil {
			slots[e.Namespace] = make(map[string]EventSlot)
		}
		var payload []string
		fields := []domain.CallableParam{}
		if obj, ok := objBySchemaID[e.SchemaID]; ok {
			for _, f := range obj.Fields {
				req := ""
				if !f.Optional {
					req = " (required)"
				}
				payload = append(payload, fmt.Sprintf("%s: %s%s", f.Name, typeDescName(f.Type), req))
				fields = append(fields, domain.CallableParam{
					Name:     f.Name,
					Type:     typeDescName(f.Type),
					Required: !f.Optional,
				})
			}
		}
		lines := []string{fmt.Sprintf("event: %s.%s", e.Namespace, e.Kind)}
		if e.Visibility != "" {
			lines = append(lines, fmt.Sprintf("visibility: %s", e.Visibility))
		}
		if len(payload) > 0 {
			lines = append(lines, "payload:")
			for _, p := range payload {
				lines = append(lines, "  "+p)
			}
		}
		slots[e.Namespace][e.Kind] = EventSlot{
			Kind:    e.Kind,
			Tooltip: strings.Join(lines, "\n"),
			Fields:  fields,
		}
	}

	result := make(map[string][]EventSlot, len(slots))
	for ns, kinds := range slots {
		list := make([]EventSlot, 0, len(kinds))
		for _, slot := range kinds {
			list = append(list, slot)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Kind < list[j].Kind })
		result[ns] = list
	}
	return result
}

// formatCallableTooltip returns a concise, newline-separated schema summary
// for a callable, including request parameters, response type, and optional
// stream/chunk type.
func formatCallableTooltip(ci domain.CallableInterface) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("callable: %s", ci.Name))
	if ci.Description != "" {
		lines = append(lines, fmt.Sprintf("description: %s", ci.Description))
	}
	if ci.Kind != "" {
		lines = append(lines, fmt.Sprintf("mode: %s", ci.Kind))
	}
	if ci.Permission != "" {
		lines = append(lines, fmt.Sprintf("visibility: %s", ci.Permission))
	}
	if len(ci.Params) > 0 {
		lines = append(lines, "request:")
		for _, p := range ci.Params {
			req := ""
			if p.Required {
				req = " (required)"
			}
			desc := ""
			if p.Description != "" {
				desc = fmt.Sprintf(" - %s", p.Description)
			}
			lines = append(lines, fmt.Sprintf("  %s: %s%s%s", p.Name, p.Type, req, desc))
		}
	} else {
		lines = append(lines, "request: void")
	}
	if ci.FinalType != "" {
		lines = append(lines, fmt.Sprintf("response: %s", ci.FinalType))
	}
	if ci.NextType != "" {
		lines = append(lines, fmt.Sprintf("chunk: %s", ci.NextType))
	}
	return strings.Join(lines, "\n")
}

// formatComponentTooltip returns a concise schema summary for a projection
// component, listing its public fields.
func formatComponentTooltip(comp ComponentSlot) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("component: %s", comp.Name))
	if comp.TypeName != "" {
		lines = append(lines, fmt.Sprintf("type: %s", comp.TypeName))
	}
	if len(comp.Fields) > 0 {
		lines = append(lines, "fields:")
		for _, f := range comp.Fields {
			lines = append(lines, fmt.Sprintf("  %s: %s", f.Name, f.Type))
		}
	}
	return strings.Join(lines, "\n")
}

// typeDescName returns a human-readable name for a TypeDesc.
func typeDescName(td spore.TypeDesc) string {
	switch td.Kind {
	case spore.TypeKindVoid:
		return "void"
	case spore.TypeKindScalar:
		if td.Name == "" {
			return "any"
		}
		return td.Name
	case spore.TypeKindArray:
		if td.Element != nil {
			return typeDescName(*td.Element) + "[]"
		}
		return "any[]"
	case spore.TypeKindMap:
		return "map"
	case spore.TypeKindStruct:
		if td.ClassName != "" {
			return td.ClassName
		}
		return td.Name
	}
	return string(td.Kind)
}

// ---------------------------------------------------------------------------
// MCP server graph helpers
// ---------------------------------------------------------------------------

// mcpServerNodeStatus maps an MCP instance status to the node Status string
// the frontend colour mapping understands: "connected" / "disconnected" /
// "error". Error wins over connected so a dead session with a recorded
// error still renders red.
func mcpServerNodeStatus(st domain.McpServerStatus) string {
	if st.Error != "" {
		return "error"
	}
	if st.Connected {
		return "connected"
	}
	return "disconnected"
}

// addMcpCallEdges appends agent→mcp-server call edges for every connected
// MCP server: resolveTools injects the tools of all connected servers into
// every agent's ToolSpec (per-agent filtering will follow per-agent MCP
// config), so the topology models that capability as one call edge per
// agent per connected server.
func (t *topologyProvider) addMcpCallEdges(nodes []domain.UnifiedGraphNode, edges []domain.UnifiedGraphEdge) ([]domain.UnifiedGraphNode, []domain.UnifiedGraphEdge) {
	var agentIDs, serverIDs []string
	for _, n := range nodes {
		switch {
		case n.Kind == "agent":
			agentIDs = append(agentIDs, n.ID)
		case n.Kind == "mcp-server" && n.Status == "connected":
			serverIDs = append(serverIDs, n.ID)
		}
	}
	return nodes, append(edges, mcpCallEdges(agentIDs, serverIDs)...)
}

// mcpCallEdges builds deterministic agent→mcp-server call edges. Both input
// slices are treated as read-only; outputs are sorted by agent ID then server
// ID so repeated builds produce identical patches.
func mcpCallEdges(agentIDs, serverIDs []string) []domain.UnifiedGraphEdge {
	if len(agentIDs) == 0 || len(serverIDs) == 0 {
		return nil
	}
	agents := append([]string(nil), agentIDs...)
	servers := append([]string(nil), serverIDs...)
	sort.Strings(agents)
	sort.Strings(servers)
	edges := make([]domain.UnifiedGraphEdge, 0, len(agents)*len(servers))
	for _, a := range agents {
		for _, s := range servers {
			edges = append(edges, domain.UnifiedGraphEdge{
				ID:   "e:" + a + "→" + s,
				From: a,
				To:   s,
				Kind: "call",
			})
		}
	}
	return edges
}

// ---------------------------------------------------------------------------
// Agent child (logical parent) edges
// ---------------------------------------------------------------------------

// agentChildEdgeKind is the edge kind used for logical parent→child agent
// relationships. These override the physical project→agent child edge so
// the topology graph reflects the workspace-level ParentAgentId.
const agentChildEdgeKind = "agent_child"

// readWorkspaceAgents reads the agents list from the workspace actor's
// projection store. Returns nil if the workspace or projection is unavailable.
func (t *topologyProvider) readWorkspaceAgents() []domain.AgentRef {
	workspaceRef, ok := t.app.LookupService("workspace")
	if !ok {
		return nil
	}
	projStore := t.app.Projections()
	if projStore == nil {
		return nil
	}
	if vp, ok := projStore.GetField(workspaceRef.ID(), "agents"); ok {
		return decodeAgentRefs(vp.Fields["value"])
	}
	return nil
}

// applyAgentChildEdges overrides the physical project→agent child edge with a
// logical agent_child edge for every workflow agent whose workspace projection
// carries a non-empty ParentAgentId.
//
// Workflow agents are physically spawned under the project actor, but their
// logical parent is another agent. This function rewrites the node ParentId,
// suppresses the physical child edge, and emits an agent_child edge carrying
// LifecycleScope and Status so the topology reflects the logical relationship.
//
// Only agents present in the graph AND whose logical parent is also present are
// rewired; a missing logical parent leaves the physical edge intact so the node
// is never orphaned.
func (t *topologyProvider) applyAgentChildEdges(nodes []domain.UnifiedGraphNode, edges []domain.UnifiedGraphEdge) ([]domain.UnifiedGraphNode, []domain.UnifiedGraphEdge) {
	return rewireAgentChildEdges(nodes, edges, t.readWorkspaceAgents())
}

// rewireAgentChildEdges is the pure core of applyAgentChildEdges. It takes the
// current graph nodes/edges and the workspace agents list, then rewrites
// physical child edges to agent_child edges where a logical ParentAgentId
// exists. Extracted as a standalone function for unit testing without a live
// app/projection store.
func rewireAgentChildEdges(nodes []domain.UnifiedGraphNode, edges []domain.UnifiedGraphEdge, agents []domain.AgentRef) ([]domain.UnifiedGraphNode, []domain.UnifiedGraphEdge) {
	if len(agents) == 0 {
		return nodes, edges
	}

	type logicalParent struct {
		parent string
		scope  string
	}
	logicalParents := make(map[string]logicalParent, len(agents))
	for _, ag := range agents {
		if ag.ActorID != "" && ag.ParentAgentID != "" {
			logicalParents[ag.ActorID] = logicalParent{parent: ag.ParentAgentID, scope: ag.LifecycleScope}
		}
	}
	if len(logicalParents) == 0 {
		return nodes, edges
	}

	nodeIdx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if n.ID != "" {
			nodeIdx[n.ID] = i
		}
	}

	// Collect agents to rewire, sorted by agent ID for deterministic output.
	type rewire struct {
		agentID string
		parent  string
		scope   string
	}
	var rewires []rewire
	suppress := make(map[string]bool)
	for agentID, lp := range logicalParents {
		if agentID == lp.parent {
			continue // self-loop guard
		}
		if _, ok := nodeIdx[agentID]; !ok {
			continue // agent not in graph
		}
		if _, parentExists := nodeIdx[lp.parent]; !parentExists {
			continue // logical parent not in graph — keep physical edge
		}
		rewires = append(rewires, rewire{agentID: agentID, parent: lp.parent, scope: lp.scope})
		suppress[agentID] = true
	}
	if len(rewires) == 0 {
		return nodes, edges
	}
	sort.Slice(rewires, func(i, j int) bool { return rewires[i].agentID < rewires[j].agentID })

	// Update node display ParentId and detail for rewired agents.
	for _, rw := range rewires {
		idx := nodeIdx[rw.agentID]
		originalParent := nodes[idx].ParentID
		nodes[idx].ParentID = rw.parent
		if nodes[idx].Detail == nil {
			nodes[idx].Detail = make(map[string]any)
		}
		if rw.scope != "" {
			nodes[idx].Detail["lifecycle_scope"] = rw.scope
		}
		nodes[idx].Detail["logical_parent_agent_id"] = rw.parent
		if originalParent != "" {
			nodes[idx].Detail["physical_parent_id"] = originalParent
		}
	}

	// Filter out physical child edges for suppressed agents; append agent_child edges.
	filtered := make([]domain.UnifiedGraphEdge, 0, len(edges)+len(rewires))
	for _, e := range edges {
		if e.Kind == "child" && suppress[e.To] {
			continue
		}
		filtered = append(filtered, e)
	}
	for _, rw := range rewires {
		idx := nodeIdx[rw.agentID]
		filtered = append(filtered, domain.UnifiedGraphEdge{
			ID:             "ac:" + rw.agentID,
			From:           rw.parent,
			To:             rw.agentID,
			Kind:           agentChildEdgeKind,
			LifecycleScope: rw.scope,
			Status:         nodes[idx].Status,
		})
	}

	return nodes, filtered
}

// DiffGraphs computes the delta between two UnifiedGraph snapshots.
func DiffGraphs(prev, next domain.UnifiedGraph) domain.GraphPatch {
	return diffGraphs(prev, next)
}

func diffGraphs(prev, next domain.UnifiedGraph) domain.GraphPatch {
	prevNodes := indexNodes(prev.Nodes)
	nextNodes := indexNodes(next.Nodes)

	added := make([]domain.UnifiedGraphNode, 0)
	updated := make([]domain.UnifiedGraphNode, 0)
	removed := make([]string, 0)

	nextSeen := make(map[string]struct{}, len(nextNodes))
	for id, n := range nextNodes {
		nextSeen[id] = struct{}{}
		if pn, ok := prevNodes[id]; ok {
			if !reflect.DeepEqual(n, pn) {
				updated = append(updated, n)
			}
		} else {
			added = append(added, n)
		}
	}
	for id := range prevNodes {
		if _, ok := nextSeen[id]; !ok {
			removed = append(removed, id)
		}
	}

	prevEdges := indexEdges(prev.Edges)
	nextEdges := indexEdges(next.Edges)

	addedEdges := make([]domain.UnifiedGraphEdge, 0)
	removedEdges := make([]string, 0)

	nextEdgeSeen := make(map[string]struct{}, len(nextEdges))
	for id, e := range nextEdges {
		nextEdgeSeen[id] = struct{}{}
		if _, ok := prevEdges[id]; !ok {
			addedEdges = append(addedEdges, e)
		}
	}
	for id := range prevEdges {
		if _, ok := nextEdgeSeen[id]; !ok {
			removedEdges = append(removedEdges, id)
		}
	}

	return domain.GraphPatch{
		AddedNodes:   added,
		RemovedNodes: removed,
		UpdatedNodes: updated,
		AddedEdges:   addedEdges,
		RemovedEdges: removedEdges,
	}
}

func applyPatch(graph domain.UnifiedGraph, patch domain.GraphPatch) domain.UnifiedGraph {
	nodeMap := indexNodes(graph.Nodes)
	edgeMap := indexEdges(graph.Edges)

	for _, id := range patch.RemovedNodes {
		delete(nodeMap, id)
	}
	for _, n := range patch.AddedNodes {
		nodeMap[n.ID] = n
	}
	for _, n := range patch.UpdatedNodes {
		nodeMap[n.ID] = n
	}

	for _, id := range patch.RemovedEdges {
		delete(edgeMap, id)
	}
	for _, e := range patch.AddedEdges {
		edgeMap[e.ID] = e
	}

	nodes := make([]domain.UnifiedGraphNode, 0, len(nodeMap))
	for _, n := range nodeMap {
		nodes = append(nodes, n)
	}
	edges := make([]domain.UnifiedGraphEdge, 0, len(edgeMap))
	for _, e := range edgeMap {
		edges = append(edges, e)
	}

	return domain.UnifiedGraph{
		Version: fmt.Sprintf("%d", patch.Epoch),
		Nodes:   nodes,
		Edges:   edges,
	}
}

func applyPatches(graph domain.UnifiedGraph, patches []domain.GraphPatch) domain.UnifiedGraph {
	for _, p := range patches {
		graph = applyPatch(graph, p)
	}
	return graph
}

func graphsEqual(a, b domain.UnifiedGraph) bool {
	if len(a.Nodes) != len(b.Nodes) || len(a.Edges) != len(b.Edges) {
		return false
	}
	return reflect.DeepEqual(indexNodes(a.Nodes), indexNodes(b.Nodes)) &&
		reflect.DeepEqual(indexEdges(a.Edges), indexEdges(b.Edges))
}

func indexNodes(nodes []domain.UnifiedGraphNode) map[string]domain.UnifiedGraphNode {
	m := make(map[string]domain.UnifiedGraphNode, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n
	}
	return m
}

func indexEdges(edges []domain.UnifiedGraphEdge) map[string]domain.UnifiedGraphEdge {
	m := make(map[string]domain.UnifiedGraphEdge, len(edges))
	for _, e := range edges {
		m[e.ID] = e
	}
	return m
}

// copyDetail returns a shallow copy of a Detail map. The values are
// expected to be JSON-compatible scalars/slices/maps that are treated
// as read-only after enrichment, so a one-level copy suffices to prevent
// concurrent writes to the same map header.
func copyDetail(d map[string]any) map[string]any {
	out := make(map[string]any, len(d))
	for k, v := range d {
		out[k] = v
	}
	return out
}

func deepCopyGraph(g domain.UnifiedGraph) domain.UnifiedGraph {
	nodes := make([]domain.UnifiedGraphNode, len(g.Nodes))
	copy(nodes, g.Nodes)
	for i := range nodes {
		if nodes[i].Detail != nil {
			nodes[i].Detail = copyDetail(nodes[i].Detail)
		}
	}
	edges := make([]domain.UnifiedGraphEdge, len(g.Edges))
	copy(edges, g.Edges)
	return domain.UnifiedGraph{
		Version: fmt.Sprintf("%d", time.Now().Unix()),
		Nodes:   nodes,
		Edges:   edges,
	}
}
