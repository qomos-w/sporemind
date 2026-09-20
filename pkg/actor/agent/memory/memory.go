package memory

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NodeID uniquely identifies a memory node.
type NodeID string

// NodeType classifies the abstraction layer of a node.
type NodeType int

const (
	NodeTypeSession    NodeType = iota // raw episodic trace
	NodeTypeExperience                 // interpreted pattern
	NodeTypeOntology                   // abstract concept
)

func (t NodeType) String() string {
	switch t {
	case NodeTypeSession:
		return "session"
	case NodeTypeExperience:
		return "experience"
	case NodeTypeOntology:
		return "ontology"
	default:
		return "unknown"
	}
}

func (t NodeType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

func (t *NodeType) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		switch v {
		case "session":
			*t = NodeTypeSession
		case "experience":
			*t = NodeTypeExperience
		case "ontology":
			*t = NodeTypeOntology
		default:
			*t = NodeTypeSession
		}
	case float64:
		*t = NodeType(int(v))
	default:
		*t = NodeTypeSession
	}
	return nil
}

// EdgeType classifies the relationship between two nodes.
type EdgeType int

const (
	EdgeTypePeer       EdgeType = iota // same-layer association
	EdgeTypeCross                      // cross-layer linkage
	EdgeTypeAntagonist                 // blocking / negative relation
)

func (t EdgeType) String() string {
	switch t {
	case EdgeTypePeer:
		return "peer"
	case EdgeTypeCross:
		return "cross"
	case EdgeTypeAntagonist:
		return "antagonist"
	default:
		return "unknown"
	}
}

func (t EdgeType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

func (t *EdgeType) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		switch v {
		case "peer":
			*t = EdgeTypePeer
		case "cross":
			*t = EdgeTypeCross
		case "antagonist":
			*t = EdgeTypeAntagonist
		default:
			*t = EdgeTypePeer
		}
	case float64:
		*t = EdgeType(int(v))
	default:
		*t = EdgeTypePeer
	}
	return nil
}

// Node is a single memory vertex in the graph.
type Node struct {
	ID         NodeID
	Type       NodeType
	Label      string
	Head       string // short summary; Label holds full Content
	Tokens     int
	Energy     float64
	BaseEnergy float64 // legacy decay floor; retained for compatibility (DecayNode no longer clamps to it)
	MinEnergy  float64 // death line: Energy below this triggers MarkDeath
	CreatedAt      time.Time
	AccessedAt     time.Time
	LastAccessTick int  // tick count at last access; drives recency instead of wall-clock
	MarkDeath      bool // flagged for deletion at next sleep
	Protected      bool // anchor node — immune to decay and merge removal
}

// Edge is a directed relationship between two nodes.
type Edge struct {
	From NodeID
	To   NodeID
	Type EdgeType
}

// Config controls all thermodynamic and behavioral parameters of the graph.
// Zero value is usable (DefaultConfig applied at New).
type Config struct {
	InitialEnergyBase    float64 // base energy assigned to a new node before suppression
	InitialSuppressionK  float64 // attenuates initial energy by 1/(1 + K * nodeCount)
	BaseDecayRate        float64 // per-tick fractional energy loss when awake (before layer multiplier)
	TopologyProtectionK  float64 // reduces decay for well-connected nodes: eff *= 1/(1+K*edgeCount)
	OntologyImmunityFrac float64 // fraction of decay applied to ontology nodes (0 = immune)
	ConnectionEnergy     float64 // energy stored on peer/cross edges; no longer injected into node energy
	NodeBaseEnergy       float64 // legacy energy floor; no longer used by DecayNode (kept for compatibility)
	NodeMinEnergy        float64 // death line: energy below this triggers MarkDeath
	SiphonEnergy         float64 // amount transferred from antagonist loser to winner on trigger
	MaxRecallBudget      int     // caps the number of nodes returned by Recall
	MergeEnergyTransfer  float64 // fraction of mergee energy transferred to the survivor

	// Per-layer injection caps bound the number of nodes injected into the
	// system prompt per turn. Each is applied before MaxRecallBudget, so the
	// total context injection is bounded and ranked by RecallScore.
	InjectOntologyCap   int
	InjectExperienceCap int
	InjectSessionCap    int

	SessionPressureTarget    int
	SessionPressureHigh      int
	ExperiencePressureTarget int
	ExperiencePressureHigh   int
	OntologyPressureTarget   int
	OntologyPressureHigh     int
	SleepPressureThreshold   float64 // total layer pressure above which sleep is due

	// Per-layer temperature: decay multiplier = BaseDecayRate * LayerTemp.
	// baseTemp is the per-node temp when the layer is empty; growthK adds
	// incremental pressure per node in the layer.
	//   session:     baseTemp highest, growthK lowest
	//   ontology:    baseTemp lowest,  growthK highest
	SessionBaseTemp     float64
	SessionGrowthK      float64
	ExperienceBaseTemp  float64
	ExperienceGrowthK   float64
	OntologyBaseTemp    float64
	OntologyGrowthK     float64

	// TickDecayK scales recency loss per tick step in RecallScore:
	// recency = 1 / (1 + ticksSinceLastAccess * TickDecayK).
	TickDecayK float64

	// TokenQuantum is the number of tokens that equals one decay tick step.
	// When the agent finishes a turn, decay advances by max(1, usage/TokenQuantum)
	// steps so that token-heavy turns age memories more than lightweight ones.
	TokenQuantum int64
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		InitialEnergyBase:        100.0,
		InitialSuppressionK:      0.1,
		BaseDecayRate:            0.02,
		TopologyProtectionK:      1.0,
		OntologyImmunityFrac:     0.0,
		ConnectionEnergy:         5.0,
		NodeBaseEnergy:           10.0,
		NodeMinEnergy:            0.5,
		SiphonEnergy:             10.0,
		MaxRecallBudget:          20,
	MergeEnergyTransfer:      0.8,
		InjectOntologyCap:        5,
		InjectExperienceCap:      8,
		InjectSessionCap:         7,
		SessionPressureTarget:    12,
		SessionPressureHigh:      24,
		ExperiencePressureTarget: 40,
		ExperiencePressureHigh:   50,
		OntologyPressureTarget:   12,
		OntologyPressureHigh:     16,
		SleepPressureThreshold:   1.2,
		SessionBaseTemp:          1.5,
		SessionGrowthK:           0.02,
		ExperienceBaseTemp:       1.0,
		ExperienceGrowthK:        0.05,
		OntologyBaseTemp:         0.5,
		OntologyGrowthK:          0.10,
		TickDecayK:               0.05,
		TokenQuantum:             1000,
	}
}

// MemoryGraph is a deterministic, in-memory directed graph of memory nodes
// governed by thermodynamic rules. Safe for concurrent access.
type MemoryGraph struct {
	mu        sync.RWMutex
	nodes     map[NodeID]*Node
	edges     []Edge
	cfg       Config
	TickCount int // monotonic step counter, advanced by Tick()
}

// New creates an empty MemoryGraph with the given config. Any zero-valued
// field is filled with its DefaultConfig value, so both fully-zero and
// partially-specified configs are usable. NodeBaseEnergy is the exception:
// 0 is a legal value (DecayNode no longer clamps to BaseEnergy), so only
// negative values are replaced with the default.
func New(cfg Config) *MemoryGraph {
	defaults := DefaultConfig()
	if cfg.InitialEnergyBase <= 0 {
		cfg.InitialEnergyBase = defaults.InitialEnergyBase
	}
	if cfg.InitialSuppressionK <= 0 {
		cfg.InitialSuppressionK = defaults.InitialSuppressionK
	}
	if cfg.BaseDecayRate <= 0 {
		cfg.BaseDecayRate = defaults.BaseDecayRate
	}
	if cfg.TopologyProtectionK <= 0 {
		cfg.TopologyProtectionK = defaults.TopologyProtectionK
	}
	if cfg.OntologyImmunityFrac < 0 {
		cfg.OntologyImmunityFrac = defaults.OntologyImmunityFrac
	}
	if cfg.ConnectionEnergy <= 0 {
		cfg.ConnectionEnergy = defaults.ConnectionEnergy
	}
	if cfg.NodeBaseEnergy < 0 {
		cfg.NodeBaseEnergy = defaults.NodeBaseEnergy
	}
	if cfg.NodeMinEnergy <= 0 {
		cfg.NodeMinEnergy = defaults.NodeMinEnergy
	}
	if cfg.SiphonEnergy <= 0 {
		cfg.SiphonEnergy = defaults.SiphonEnergy
	}
	if cfg.MaxRecallBudget <= 0 {
		cfg.MaxRecallBudget = defaults.MaxRecallBudget
	}
	if cfg.MergeEnergyTransfer <= 0 {
		cfg.MergeEnergyTransfer = defaults.MergeEnergyTransfer
	}
	if cfg.InjectOntologyCap <= 0 {
		cfg.InjectOntologyCap = defaults.InjectOntologyCap
	}
	if cfg.InjectExperienceCap <= 0 {
		cfg.InjectExperienceCap = defaults.InjectExperienceCap
	}
	if cfg.InjectSessionCap <= 0 {
		cfg.InjectSessionCap = defaults.InjectSessionCap
	}
	if cfg.SessionPressureTarget <= 0 {
		cfg.SessionPressureTarget = defaults.SessionPressureTarget
	}
	if cfg.SessionPressureHigh <= cfg.SessionPressureTarget {
		cfg.SessionPressureHigh = max(defaults.SessionPressureHigh, cfg.SessionPressureTarget*2)
	}
	if cfg.ExperiencePressureTarget <= 0 {
		cfg.ExperiencePressureTarget = defaults.ExperiencePressureTarget
	}
	if cfg.ExperiencePressureHigh <= cfg.ExperiencePressureTarget {
		cfg.ExperiencePressureHigh = max(defaults.ExperiencePressureHigh, cfg.ExperiencePressureTarget+10)
	}
	if cfg.OntologyPressureTarget <= 0 {
		cfg.OntologyPressureTarget = defaults.OntologyPressureTarget
	}
	if cfg.OntologyPressureHigh <= cfg.OntologyPressureTarget {
		cfg.OntologyPressureHigh = max(defaults.OntologyPressureHigh, cfg.OntologyPressureTarget+4)
	}
	if cfg.SleepPressureThreshold <= 0 {
		cfg.SleepPressureThreshold = defaults.SleepPressureThreshold
	}
	if cfg.SessionBaseTemp <= 0 {
		cfg.SessionBaseTemp = defaults.SessionBaseTemp
	}
	if cfg.SessionGrowthK <= 0 {
		cfg.SessionGrowthK = defaults.SessionGrowthK
	}
	if cfg.ExperienceBaseTemp <= 0 {
		cfg.ExperienceBaseTemp = defaults.ExperienceBaseTemp
	}
	if cfg.ExperienceGrowthK <= 0 {
		cfg.ExperienceGrowthK = defaults.ExperienceGrowthK
	}
	if cfg.OntologyBaseTemp <= 0 {
		cfg.OntologyBaseTemp = defaults.OntologyBaseTemp
	}
	if cfg.OntologyGrowthK <= 0 {
		cfg.OntologyGrowthK = defaults.OntologyGrowthK
	}
	if cfg.TickDecayK <= 0 {
		cfg.TickDecayK = defaults.TickDecayK
	}
	if cfg.TokenQuantum <= 0 {
		cfg.TokenQuantum = defaults.TokenQuantum
	}
	return &MemoryGraph{
		nodes: make(map[NodeID]*Node),
		cfg:   cfg,
	}
}

// genID returns a crypto-random hex NodeID. If id is non-empty it is used directly.
func genID(id NodeID) NodeID {
	if id != "" {
		return id
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("memory: crypto/rand failed: %v", err))
	}
	return NodeID(fmt.Sprintf("%x", b))
}

// AddNode inserts a new node. If id is empty a crypto-random ID is generated.
// Initial energy is count-suppressed: base / (1 + K * nodeCount).
// Returns nil if a node with the same ID already exists.
func (g *MemoryGraph) AddNode(id NodeID, typ NodeType, label string, head string, tokens int) *Node {
	g.mu.Lock()
	defer g.mu.Unlock()

	nid := genID(id)
	if _, exists := g.nodes[nid]; exists {
		return nil
	}

	n := g.nodeCountLocked()
	initial := g.cfg.InitialEnergyBase / (1.0 + g.cfg.InitialSuppressionK*float64(n))

	now := time.Now()
	node := &Node{
		ID:             nid,
		Type:           typ,
		Label:          label,
		Head:           head,
		Tokens:         tokens,
		Energy:         initial,
		BaseEnergy:     g.cfg.NodeBaseEnergy,
		MinEnergy:      g.cfg.NodeMinEnergy,
		CreatedAt:      now,
		AccessedAt:     now,
		LastAccessTick: g.TickCount,
	}
	g.nodes[nid] = node
	return node
}

// UpsertNode inserts a new node or updates an existing one with matching ID.
// If the node exists, its Label, Head, and Tokens are updated; other fields
// (Type, Energy, CreatedAt) are preserved. New nodes get the same energy
// calculation as AddNode. Returns the node (always non-nil on success).
func (g *MemoryGraph) UpsertNode(id NodeID, typ NodeType, label string, head string, tokens int) *Node {
	g.mu.Lock()
	defer g.mu.Unlock()

	nid := genID(id)
	if existing, ok := g.nodes[nid]; ok {
		existing.Label = label
		existing.Head = head
		existing.Tokens = tokens
		existing.AccessedAt = time.Now()
		existing.LastAccessTick = g.TickCount
		return existing
	}

	n := g.nodeCountLocked()
	initial := g.cfg.InitialEnergyBase / (1.0 + g.cfg.InitialSuppressionK*float64(n))

	now := time.Now()
	node := &Node{
		ID:             nid,
		Type:           typ,
		Label:          label,
		Head:           head,
		Tokens:         tokens,
		Energy:         initial,
		BaseEnergy:     g.cfg.NodeBaseEnergy,
		MinEnergy:      g.cfg.NodeMinEnergy,
		CreatedAt:      now,
		AccessedAt:     now,
		LastAccessTick: g.TickCount,
	}
	g.nodes[nid] = node
	return node
}

// Node returns a read-only copy of the node or nil.
func (g *MemoryGraph) Node(id NodeID) *Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.nodeLocked(id)
}

// RemoveNode deletes a node and all incident edges.
func (g *MemoryGraph) RemoveNode(id NodeID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[id]; !ok {
		return false
	}
	delete(g.nodes, id)
	filtered := g.edges[:0]
	for _, e := range g.edges {
		if e.From != id && e.To != id {
			filtered = append(filtered, e)
		}
	}
	g.edges = filtered
	return true
}

// AddEdge creates an edge between two nodes.
// Layer invariants: peer/antagonist require same layer; cross requires differing layers.
// Duplicate edges (same from, to, type) are rejected.
// ConnectionEnergy is stored on peer/cross edges but is not injected into the
// source node's Energy (connectivity is protected via topologyProtection).
func (g *MemoryGraph) AddEdge(from, to NodeID, typ EdgeType) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addEdgeLocked(from, to, typ)
}

// addEdgeLocked is the lock-held implementation of AddEdge. It also serves
// the sleep connect path (ApplySleep already holds the lock).
func (g *MemoryGraph) addEdgeLocked(from, to NodeID, typ EdgeType) error {
	nA, okA := g.nodes[from]
	nB, okB := g.nodes[to]
	if !okA || !okB {
		return fmt.Errorf("memory: one or both nodes not found (%s, %s)", from, to)
	}
	if from == to {
		return fmt.Errorf("memory: self-loop not allowed")
	}

	// Layer invariant validation.
	switch typ {
	case EdgeTypePeer, EdgeTypeAntagonist:
		if nA.Type != nB.Type {
			return fmt.Errorf("memory: %s edge requires same layer (%s=%s, %s=%s)",
				typ, from, nA.Type, to, nB.Type)
		}
	case EdgeTypeCross:
		if nA.Type == nB.Type {
			return fmt.Errorf("memory: cross edge requires differing layers (%s=%s, %s=%s)",
				from, nA.Type, to, nB.Type)
		}
		// Cross edges are only allowed between adjacent layers:
		// session↔experience, experience↔ontology.
		// A session→ontology jump skips the experience layer and is forbidden.
		diff := nA.Type - nB.Type
		if diff < 0 {
			diff = -diff
		}
		if diff > 1 {
			return fmt.Errorf("memory: cross edge requires adjacent layers (%s=%s, %s=%s, gap=%d)",
				from, nA.Type, to, nB.Type, diff)
		}
	}

	// Reject duplicate semantic edge (same from, to, type).
	for _, e := range g.edges {
		if e.From == from && e.To == to && e.Type == typ {
			return fmt.Errorf("memory: duplicate %s edge %s->%s", typ, from, to)
		}
	}

	// ConnectionEnergy is not injected into the source node's Energy.
	// Edge.Energy has been removed; edges carry no energy field.

	g.edges = append(g.edges, Edge{From: from, To: to, Type: typ})

	// Establishing a peer connection triggers antagonist siphon.
	if typ == EdgeTypePeer {
		g.triggerSiphonLocked(from)
	}

	return nil
}

// parseEdgeType converts a string to EdgeType. Returns -1 for unknown.
func parseEdgeType(s string) EdgeType {
	switch strings.ToLower(s) {
	case "peer":
		return EdgeTypePeer
	case "cross":
		return EdgeTypeCross
	case "antagonist":
		return EdgeTypeAntagonist
	default:
		return -1
	}
}

// RemoveEdge removes the first matching edge with the given from, to, and type.
func (g *MemoryGraph) RemoveEdge(from, to NodeID, typ EdgeType) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, e := range g.edges {
		if e.From == from && e.To == to && e.Type == typ {
			g.edges = append(g.edges[:i], g.edges[i+1:]...)
			return true
		}
	}
	return false
}

// Touch records access time and triggers antagonist siphon for this node as winner.
func (g *MemoryGraph) Touch(id NodeID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	n, ok := g.nodes[id]
	if !ok {
		return false
	}
	n.AccessedAt = time.Now()
	n.LastAccessTick = g.TickCount
	g.triggerSiphonLocked(id)
	return true
}

// triggerSiphonLocked fires the antagonist siphon event for winnerID:
// for each antagonist edge from winnerID->loser, transfer min(SiphonEnergy, loser.Energy)
// from loser to winner. Does not mark nodes for death or drain winner.
// Caller must hold g.mu write lock.
func (g *MemoryGraph) triggerSiphonLocked(winnerID NodeID) {
	for _, e := range g.edges {
		if e.From == winnerID && e.Type == EdgeTypeAntagonist {
			loser, ok := g.nodes[e.To]
			if !ok || loser.MarkDeath {
				continue
			}
			winner, ok := g.nodes[winnerID]
			if !ok {
				continue
			}
			transfer := AntagonistDrain(g.cfg.SiphonEnergy, loser.Energy)
			loser.Energy -= transfer
			winner.Energy += transfer
		}
	}
}

// Tick advances time by one unit. When awake, decay is applied per-layer
// using LayerTemp as a multiplier. Nodes whose energy falls below
// NodeMinEnergy are marked for death. Protected nodes are immune to decay.
func (g *MemoryGraph) Tick(awake bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tickLocked(awake)
}

// TickTokens advances decay by the number of token-quantum steps implied by
// the given token count: steps = max(1, tokens/TokenQuantum) when tokens > 0,
// otherwise a single step. Each step applies the same per-layer decay as Tick.
func (g *MemoryGraph) TickTokens(tokens int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	steps := int64(1)
	if tokens > 0 {
		steps = tokens / g.cfg.TokenQuantum
		if steps < 1 {
			steps = 1
		}
	}
	for i := int64(0); i < steps; i++ {
		g.tickLocked(true)
	}
}

// tickLocked is the lock-free decay body; caller must hold g.mu.
func (g *MemoryGraph) tickLocked(awake bool) {
	g.TickCount++
	if !awake {
		return
	}

	counts := countByLayerLocked(g.nodes)

	layerTemp := func(typ NodeType) float64 {
		switch typ {
		case NodeTypeSession:
			return LayerTemp(counts[NodeTypeSession], g.cfg.SessionBaseTemp, g.cfg.SessionGrowthK)
		case NodeTypeExperience:
			return LayerTemp(counts[NodeTypeExperience], g.cfg.ExperienceBaseTemp, g.cfg.ExperienceGrowthK)
		case NodeTypeOntology:
			return LayerTemp(counts[NodeTypeOntology], g.cfg.OntologyBaseTemp, g.cfg.OntologyGrowthK)
		}
		return 1.0
	}

	// Precompute edge counts for topology protection.
	// Antagonist edges are excluded: blocking relations must not grant
	// decay protection.
	outDegree := make(map[NodeID]int)
	for _, e := range g.edges {
		if e.Type == EdgeTypeAntagonist {
			continue
		}
		outDegree[e.From]++
		outDegree[e.To]++
	}

	for _, n := range g.nodes {
		if n.Energy <= 0 || n.Protected {
			continue
		}

		deg := outDegree[n.ID]
		protection := 1.0 / (1.0 + g.cfg.TopologyProtectionK*float64(deg))
		decay := g.cfg.BaseDecayRate * layerTemp(n.Type) * protection
		if n.Type == NodeTypeOntology && g.cfg.OntologyImmunityFrac > 0 {
			decay *= g.cfg.OntologyImmunityFrac
		}
		// No min clamp here: decay must pierce below NodeMinEnergy so
		// ShouldEvaporate (strict <) can flag dead nodes — the decay purge
		// is the only cleanup path (see sleep.go). Clamping at n.MinEnergy
		// (== NodeMinEnergy at creation) parks energy at exactly the death
		// line, which is never crossed.
		n.Energy = DecayNode(n.Energy, decay, n.BaseEnergy, 0, true)

		if ShouldEvaporate(n.Energy, g.cfg.NodeMinEnergy) && !n.MarkDeath {
			n.MarkDeath = true
		}
	}
}

// MarkForDeath flags a node for deletion during the next sleep cycle.
func (g *MemoryGraph) MarkForDeath(id NodeID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	if !ok {
		return false
	}
	n.MarkDeath = true
	return true
}

// CurrentTick returns the current global tick count.
func (g *MemoryGraph) CurrentTick() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.TickCount
}

// TickDecayK returns the config's tick-decay constant used by RecallScore
// (recency = 1 / (1 + ticksSinceLastAccess * TickDecayK)).
func (g *MemoryGraph) TickDecayK() float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cfg.TickDecayK
}

// IsSleepDue returns true if total layer pressure exceeds SleepPressureThreshold.
func (g *MemoryGraph) IsSleepDue() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	counts := countByLayerLocked(g.nodes)
	pressure := LayerPressure(counts[NodeTypeSession], g.cfg.SessionPressureTarget, g.cfg.SessionPressureHigh) +
		LayerPressure(counts[NodeTypeExperience], g.cfg.ExperiencePressureTarget, g.cfg.ExperiencePressureHigh) +
		LayerPressure(counts[NodeTypeOntology], g.cfg.OntologyPressureTarget, g.cfg.OntologyPressureHigh)
	return pressure >= g.cfg.SleepPressureThreshold
}

// countLocked returns the number of nodes. Caller must hold at least RLock.
func (g *MemoryGraph) nodeCountLocked() int { return len(g.nodes) }

// nodeLocked returns a shallow copy of a node. Caller must hold at least RLock.
func (g *MemoryGraph) nodeLocked(id NodeID) *Node {
	n, ok := g.nodes[id]
	if !ok {
		return nil
	}
	c := *n
	return &c
}

// Nodes returns a shallow copy of all non-marked nodes.
func (g *MemoryGraph) Nodes() []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		if n.MarkDeath {
			continue
		}
		c := *n
		out = append(out, &c)
	}
	return out
}

// Edges returns a copy of all edges.
func (g *MemoryGraph) Edges() []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Edge, len(g.edges))
	copy(out, g.edges)
	return out
}

// NodeCount returns the number of nodes (including marked for death).
func (g *MemoryGraph) NodeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}

// EdgeCount returns the number of edges.
func (g *MemoryGraph) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.edges)
}

// Config returns a copy of the graph configuration.
func (g *MemoryGraph) Config() Config {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cfg
}

// CountByLayer returns the number of live (non-marked) nodes per layer.
func (g *MemoryGraph) CountByLayer() map[NodeType]int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return countByLayerLocked(g.nodes)
}

// countByLayerLocked counts live nodes per layer. Caller must hold at least RLock.
func countByLayerLocked(nodes map[NodeID]*Node) map[NodeType]int {
	out := map[NodeType]int{NodeTypeSession: 0, NodeTypeExperience: 0, NodeTypeOntology: 0}
	for _, n := range nodes {
		if n.MarkDeath {
			continue
		}
		out[n.Type]++
	}
	return out
}

// LayerPressureSummary returns a human-readable pressure report for each layer,
// intended for inclusion in dreamer prompts.
func (g *MemoryGraph) LayerPressureSummary() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	counts := countByLayerLocked(g.nodes)
	targets := map[NodeType][2]int{
		NodeTypeSession:    {g.cfg.SessionPressureTarget, g.cfg.SessionPressureHigh},
		NodeTypeExperience: {g.cfg.ExperiencePressureTarget, g.cfg.ExperiencePressureHigh},
		NodeTypeOntology:   {g.cfg.OntologyPressureTarget, g.cfg.OntologyPressureHigh},
	}
	out := ""
	for _, typ := range []NodeType{NodeTypeSession, NodeTypeExperience, NodeTypeOntology} {
		rangeForLayer := targets[typ]
		pressure := LayerPressure(counts[typ], rangeForLayer[0], rangeForLayer[1])
		out += typ.String() + ": count=" + itoa(counts[typ]) + ", stable_max=" + itoa(rangeForLayer[0]) + ", high=" + itoa(rangeForLayer[1]) + ", pressure=" + strconv.FormatFloat(pressure, 'f', 2, 64) + "; "
	}
	return out
}

// itoa is a simple non-import int-to-string for the pressure summary.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	d := ""
	for n > 0 {
		d = string(rune('0'+n%10)) + d
		n /= 10
	}
	return d
}
