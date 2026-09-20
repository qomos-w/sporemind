package memory

import (
	"sort"
)

// RecallByID returns a shallow copy of the node with the exact ID, or nil.
// Returns nil if the node is marked for death.
func (g *MemoryGraph) RecallByID(id NodeID) *Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n := g.nodeLocked(id)
	if n != nil && n.MarkDeath {
		return nil
	}
	return n
}

// RecallOneHop returns all non-marked nodes adjacent to id, plus the edges
// themselves. Peer/cross relations are symmetric, so both outgoing edges
// (e.From == id) and incoming edges (e.To == id) are traversed. Incoming
// edges are returned in their original direction; callers distinguish them
// via e.From == id.
func (g *MemoryGraph) RecallOneHop(id NodeID) ([]*Node, []Edge) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var nodes []*Node
	var edges []Edge
	seen := make(map[NodeID]bool)

	for _, e := range g.edges {
		if e.From == id {
			edges = append(edges, e)
			if n, ok := g.nodes[e.To]; ok && !seen[e.To] && !n.MarkDeath {
				c := *n
				nodes = append(nodes, &c)
				seen[e.To] = true
			}
		} else if e.To == id {
			// Reverse edge: peer/cross relations are symmetric, so an
			// incoming edge also makes the source node a neighbor. The edge
			// itself is returned in its original direction; callers judge
			// direction via e.From == id.
			edges = append(edges, e)
			if n, ok := g.nodes[e.From]; ok && !seen[e.From] && !n.MarkDeath {
				c := *n
				nodes = append(nodes, &c)
				seen[e.From] = true
			}
		}
	}
	// Deterministic order by node ID.
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return nodes, edges
}

type recallCandidate struct {
	node  *Node
	score float64
}

// Recall filters and ranks non-marked nodes by type and returns at most limit results.
// Ranking: energy * recency, with deterministic tie-break by node ID.
// limit <= 0 means use graph config budget. limit > maxBudget is clamped.
func (g *MemoryGraph) Recall(typ NodeType, limit int) []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	budget := g.cfg.MaxRecallBudget
	if limit > 0 && limit < budget {
		budget = limit
	}
	if budget <= 0 {
		return nil
	}

	tick := g.TickCount
	candidates := make([]recallCandidate, 0, len(g.nodes))

	for _, n := range g.nodes {
		if n.Type != typ || n.MarkDeath {
			continue
		}
		candidates = append(candidates, recallCandidate{
			node:  n,
			score: RecallScore(n, tick, g.cfg.TickDecayK),
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].node.ID < candidates[j].node.ID
	})

	if len(candidates) > budget {
		candidates = candidates[:budget]
	}

	out := make([]*Node, len(candidates))
	for i, c := range candidates {
		cp := *c.node
		out[i] = &cp
	}
	return out
}

// RecallInjection returns the ranked, per-layer quota-capped nodes used to
// build context injection into the system prompt. Each layer is capped by its
// Config injection cap (InjectOntologyCap/InjectExperienceCap/InjectSessionCap)
// before MaxRecallBudget, so the injected set is bounded and RecallScore-sorted.
// This is the single entry point that the agent-layer context injection and
// touch projection share, guaranteeing they operate on identical node sets.
func (g *MemoryGraph) RecallInjection() (ontology, experience, session []*Node) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ontology = g.recallLayerLocked(NodeTypeOntology, g.cfg.InjectOntologyCap)
	experience = g.recallLayerLocked(NodeTypeExperience, g.cfg.InjectExperienceCap)
	session = g.recallLayerLocked(NodeTypeSession, g.cfg.InjectSessionCap)

	// Cross-edge expansion: pull experience nodes linked to injected session
	// nodes via cross edges. Existing experience nodes are not replaced when
	// the cap is reached.
	expIDs := make(map[NodeID]bool)
	for _, n := range experience {
		expIDs[n.ID] = true
	}
	for _, s := range session {
		for _, e := range g.edges {
			if e.Type != EdgeTypeCross {
				continue
			}
			var crossNodeID NodeID
			if e.From == s.ID && g.nodes[e.To] != nil && g.nodes[e.To].Type == NodeTypeExperience {
				crossNodeID = e.To
			} else if e.To == s.ID && g.nodes[e.From] != nil && g.nodes[e.From].Type == NodeTypeExperience {
				crossNodeID = e.From
			}
			if crossNodeID != "" && !expIDs[crossNodeID] && len(experience) < g.cfg.InjectExperienceCap {
				if n, ok := g.nodes[crossNodeID]; ok && !n.MarkDeath {
					cp := *n // shallow copy, consistent with recallLayerLocked
					experience = append(experience, &cp)
					expIDs[crossNodeID] = true
				}
			}
		}
	}
	return
}

// recallLayerLocked ranks non-marked nodes of typ by RecallScore and truncates
// to the smaller of cap and MaxRecallBudget. Caller must hold g.mu (at least RLock).
func (g *MemoryGraph) recallLayerLocked(typ NodeType, cap int) []*Node {
	budget := g.cfg.MaxRecallBudget
	if cap > 0 && cap < budget {
		budget = cap
	}
	if budget <= 0 {
		return nil
	}

	now := g.TickCount
	candidates := make([]recallCandidate, 0, len(g.nodes))
	for _, n := range g.nodes {
		if n.Type != typ || n.MarkDeath {
			continue
		}
		candidates = append(candidates, recallCandidate{
			node:  n,
			score: RecallScore(n, now, g.cfg.TickDecayK),
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].node.ID < candidates[j].node.ID
	})

	if len(candidates) > budget {
		candidates = candidates[:budget]
	}

	out := make([]*Node, len(candidates))
	for i, c := range candidates {
		cp := *c.node
		out[i] = &cp
	}
	return out
}

// RecallMixed returns non-marked nodes of any type, ranked and budgeted.
func (g *MemoryGraph) RecallMixed(limit int) []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	budget := g.cfg.MaxRecallBudget
	if limit > 0 && limit < budget {
		budget = limit
	}
	if budget <= 0 {
		return nil
	}

	now := g.TickCount
	candidates := make([]recallCandidate, 0, len(g.nodes))
	for _, n := range g.nodes {
		if n.MarkDeath {
			continue
		}
		candidates = append(candidates, recallCandidate{
			node:  n,
			score: RecallScore(n, now, g.cfg.TickDecayK),
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].node.ID < candidates[j].node.ID
	})

	if len(candidates) > budget {
		candidates = candidates[:budget]
	}

	out := make([]*Node, len(candidates))
	for i, c := range candidates {
		cp := *c.node
		out[i] = &cp
	}
	return out
}
