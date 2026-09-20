package memory

import "fmt"

// SleepAction enumerates the supported sleep instruction types.
// Only merge and promote are supported — cleanup (delete) is handled
// by the decay algorithm's MarkDeath purge, not by the dreamer.
type SleepAction string

const (
	SleepMerge   SleepAction = "merge"
	SleepPromote SleepAction = "promote"
	SleepConnect SleepAction = "connect"
)

// SleepInstruction describes a single graph modification to apply during sleep.
type SleepInstruction struct {
	Action   SleepAction
	TargetID NodeID
	MergeID  NodeID // used by merge (mergee) and connect (second endpoint)
	Head     string // rewritten head (required for merge/promote-to-experience)
	Content  string // rewritten content / Label (required for merge)
	EdgeType string // used by connect: "peer" | "cross" | "antagonist"
}

// ApplySleep applies all instructions atomically: if any fails, the graph
// is rolled back to its original state. Marked-for-death nodes are purged
// after all instructions succeed.
func (g *MemoryGraph) ApplySleep(instructions []SleepInstruction) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Snapshot current state for rollback.
	snapNodes := copyNodes(g.nodes)
	snapEdges := copyEdges(g.edges)

	for _, inst := range instructions {
		if err := g.applyOne(inst); err != nil {
			// Rollback.
			g.nodes = snapNodes
			g.edges = snapEdges
			return err
		}
	}

	for id, node := range g.nodes {
		if node.MarkDeath {
			delete(g.nodes, id)
		}
	}
	filtered := g.edges[:0]
	for _, edge := range g.edges {
		if g.nodes[edge.From] != nil && g.nodes[edge.To] != nil {
			filtered = append(filtered, edge)
		}
	}
	g.edges = filtered
	return nil
}

func (g *MemoryGraph) applyOne(inst SleepInstruction) error {
	switch inst.Action {
	case SleepMerge:
		target, okT := g.nodes[inst.TargetID]
		mergee, okM := g.nodes[inst.MergeID]
		if !okT || !okM {
			return fmt.Errorf("sleep merge: target %s or mergee %s not found",
				inst.TargetID, inst.MergeID)
		}
		if inst.TargetID == inst.MergeID {
			return fmt.Errorf("sleep merge: target and mergee are the same node")
		}
		// Protected mergee cannot be removed (even if target is protected).
		if mergee.Protected {
			return fmt.Errorf("sleep merge: mergee %s is a protected anchor and cannot be removed", inst.MergeID)
		}
		// Content fidelity: merge must provide rewritten Head and Content.
		if inst.Head == "" || inst.Content == "" {
			return fmt.Errorf("sleep merge: Head and Content required for merge, got empty")
		}

		transfer := mergee.Energy * g.cfg.MergeEnergyTransfer
		target.Energy += transfer

		// Apply rewritten content.
		target.Head = inst.Head
		target.Label = inst.Content

		// Rewire mergee's edges to target.
		for i, e := range g.edges {
			if e.From == inst.MergeID {
				g.edges[i].From = inst.TargetID
			}
			if e.To == inst.MergeID {
				g.edges[i].To = inst.TargetID
			}
		}
		// Remove self-loops and semantic duplicates (same from, to, type).
		seen := make(map[[3]any]bool)
		filtered := g.edges[:0]
		for _, e := range g.edges {
			if e.From == e.To {
				continue
			}
			key := [3]any{e.From, e.To, e.Type}
			if seen[key] {
				continue
			}
			seen[key] = true
			filtered = append(filtered, e)
		}
		g.edges = filtered

		delete(g.nodes, inst.MergeID)
		return nil

	case SleepPromote:
		n, ok := g.nodes[inst.TargetID]
		if !ok {
			return fmt.Errorf("sleep promote: node %s not found", inst.TargetID)
		}
		if n.Type >= NodeTypeOntology {
			return fmt.Errorf("sleep promote: node %s already at highest type", inst.TargetID)
		}
		// Promote to Experience requires a rewritten Head.
		if n.Type == NodeTypeSession && inst.Head == "" {
			return fmt.Errorf("sleep promote: Head required when promoting session to experience")
		}
		// Apply rewritten Head/Content if provided (may be empty for Experience->Ontology).
		if inst.Head != "" {
			n.Head = inst.Head
		}
		if inst.Content != "" {
			n.Label = inst.Content
		}
		n.Type++
		return nil

	case SleepConnect:
		// Connect is non-destructive: ALL failures are silently skipped
		// to avoid rolling back successful merge/promote instructions.
		if inst.TargetID == inst.MergeID {
			return nil
		}
		if _, ok := g.nodes[inst.TargetID]; !ok {
			return nil
		}
		if _, ok := g.nodes[inst.MergeID]; !ok {
			return nil
		}
		et := parseEdgeType(inst.EdgeType)
		if et < 0 {
			return nil
		}
		_ = g.addEdgeLocked(inst.TargetID, inst.MergeID, et)
		return nil

	default:
		return fmt.Errorf("sleep: unknown action %q", inst.Action)
	}
}

// copyNodes returns a deep-ish copy of the node map.
func copyNodes(src map[NodeID]*Node) map[NodeID]*Node {
	dst := make(map[NodeID]*Node, len(src))
	for k, v := range src {
		c := *v
		dst[k] = &c
	}
	return dst
}

// copyEdges returns a copy of an edge slice.
func copyEdges(src []Edge) []Edge {
	dst := make([]Edge, len(src))
	copy(dst, src)
	return dst
}
