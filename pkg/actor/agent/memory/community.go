package memory

import "sort"

// Communities partitions the node set into connected components
// (undirected, via all edge types). Results are deterministic:
// each component's node IDs are sorted, and components are sorted
// by their smallest node ID.
func (g *MemoryGraph) Communities() [][]NodeID {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.nodes) == 0 {
		return nil
	}

	// Build undirected adjacency, skipping edges whose endpoints are missing.
	adj := make(map[NodeID][]NodeID)
	for _, e := range g.edges {
		if _, ok := g.nodes[e.From]; !ok {
			continue
		}
		if _, ok := g.nodes[e.To]; !ok {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}
	// Ensure isolated nodes have an entry.
	for id := range g.nodes {
		if _, ok := adj[id]; !ok {
			adj[id] = nil
		}
	}

	visited := make(map[NodeID]bool)
	var components [][]NodeID

	// Collect all node IDs for deterministic starting order.
	allIDs := make([]NodeID, 0, len(g.nodes))
	for id := range g.nodes {
		allIDs = append(allIDs, id)
	}
	sort.Slice(allIDs, func(i, j int) bool { return allIDs[i] < allIDs[j] })

	for _, start := range allIDs {
		if visited[start] {
			continue
		}
		// BFS with queue.
		comp := []NodeID{start}
		visited[start] = true
		queue := []NodeID{start}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, nb := range adj[cur] {
				if !visited[nb] {
					visited[nb] = true
					comp = append(comp, nb)
					queue = append(queue, nb)
				}
			}
		}
		sort.Slice(comp, func(i, j int) bool { return comp[i] < comp[j] })
		components = append(components, comp)
	}

	// Sort components by their smallest (first) node ID.
	sort.Slice(components, func(i, j int) bool {
		return components[i][0] < components[j][0]
	})

	return components
}
