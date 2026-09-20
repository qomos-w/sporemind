package memory

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// snapshotData is the serializable representation of the graph state.
type snapshotData struct {
	Nodes     []snapshotNode `json:"nodes"`
	Edges     []snapshotEdge `json:"edges"`
	TickCount int            `json:"tick_count"`
}

type snapshotNode struct {
	ID             NodeID    `json:"id"`
	Type           NodeType  `json:"type"`
	Label          string    `json:"label"`
	Head           string    `json:"head"`
	Tokens         int       `json:"tokens"`
	Energy         float64   `json:"energy"`
	BaseEnergy     float64   `json:"base_energy"`
	MinEnergy      float64   `json:"min_energy"`
	CreatedAt      time.Time `json:"created_at"`
	AccessedAt     time.Time `json:"accessed_at"`
	LastAccessTick int       `json:"last_access_tick"`
	MarkDeath      bool      `json:"mark_death"`
	Protected      bool      `json:"protected"`
}

type snapshotEdge struct {
	From NodeID   `json:"from"`
	To   NodeID   `json:"to"`
	Type EdgeType `json:"type"`
}

// Snapshot serializes the entire graph state to JSON bytes.
// Nodes and edges are sorted by ID for deterministic output.
func (g *MemoryGraph) Snapshot() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Collect and sort nodes by ID.
	nodeList := make([]*Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		nodeList = append(nodeList, n)
	}
	sort.Slice(nodeList, func(i, j int) bool {
		return nodeList[i].ID < nodeList[j].ID
	})

	sd := snapshotData{
		Nodes:     make([]snapshotNode, 0, len(g.nodes)),
		Edges:     make([]snapshotEdge, 0, len(g.edges)),
		TickCount: g.TickCount,
	}
	for _, n := range nodeList {
		sd.Nodes = append(sd.Nodes, snapshotNode{
			ID:             n.ID,
			Type:           n.Type,
			Label:          n.Label,
			Head:           n.Head,
			Tokens:         n.Tokens,
			Energy:         n.Energy,
			BaseEnergy:     n.BaseEnergy,
			MinEnergy:      n.MinEnergy,
			CreatedAt:      n.CreatedAt,
			AccessedAt:     n.AccessedAt,
			LastAccessTick: n.LastAccessTick,
			MarkDeath:      n.MarkDeath,
			Protected:      n.Protected,
		})
	}

	// Collect and sort edges by (from, to, type).
	edgeList := make([]Edge, len(g.edges))
	copy(edgeList, g.edges)
	sort.Slice(edgeList, func(i, j int) bool {
		if edgeList[i].From != edgeList[j].From {
			return edgeList[i].From < edgeList[j].From
		}
		if edgeList[i].To != edgeList[j].To {
			return edgeList[i].To < edgeList[j].To
		}
		return edgeList[i].Type < edgeList[j].Type
	})

	for _, e := range edgeList {
		sd.Edges = append(sd.Edges, snapshotEdge{
			From: e.From,
			To:   e.To,
			Type: e.Type,
		})
	}

	return json.Marshal(sd)
}

// Restore deep-replaces the current graph state from a snapshot.
// The config is kept from the existing graph, not from the snapshot.
func (g *MemoryGraph) Restore(data []byte) error {
	var sd snapshotData
	if err := json.Unmarshal(data, &sd); err != nil {
		return fmt.Errorf("memory restore: %w", err)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	newNodes := make(map[NodeID]*Node, len(sd.Nodes))
	for _, sn := range sd.Nodes {
		newNodes[sn.ID] = &Node{
			ID:             sn.ID,
			Type:           sn.Type,
			Label:          sn.Label,
			Head:           sn.Head,
			Tokens:         sn.Tokens,
			Energy:         sn.Energy,
			BaseEnergy:     sn.BaseEnergy,
			MinEnergy:      sn.MinEnergy,
			CreatedAt:      sn.CreatedAt,
			AccessedAt:     sn.AccessedAt,
			LastAccessTick: sn.LastAccessTick,
			MarkDeath:      sn.MarkDeath,
			Protected:      sn.Protected,
		}
	}

	newEdges := make([]Edge, len(sd.Edges))
	for i, se := range sd.Edges {
		newEdges[i] = Edge{
			From: se.From,
			To:   se.To,
			Type: se.Type,
		}
	}

	g.nodes = newNodes
	g.edges = newEdges
	g.TickCount = sd.TickCount
	return nil
}
