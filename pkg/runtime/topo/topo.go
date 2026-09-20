// Package topo provides dependency-driven topological sorting shared by both
// the system actor bootstrap (ChildSpec.DependsOn) and the native plugin
// runtime (manifest Dependencies). The algorithm is Kahn's: in-degree-based,
// deterministic, and produces a stable order when ties are broken by input
// order. Cycles and missing dependencies are reported as structured errors.
package topo

import (
	"fmt"
	"strings"
)

// CycleError reports a dependency cycle.
type CycleError struct {
	// Path contains the node names forming the cycle, ending with the first
	// repeated node (e.g. ["a", "b", "c", "a"]).
	Path []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("dependency cycle detected: %s", strings.Join(e.Path, " → "))
}

// MissingError reports a dependency that references a non-existent node.
type MissingError struct {
	// Node is the node whose dependency is missing.
	Node string
	// Missing is the dependency name that could not be resolved.
	Missing string
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("dependency %q is missing (required by %q)", e.Missing, e.Node)
}

// Sort computes a topological ordering of nodes. deps maps each node name to
// the list of node names it depends on. The returned slice preserves input
// order for nodes at the same topological level, making the result stable and
// deterministic. Returns CycleError or MissingError on failure.
func Sort(nodes []string, deps map[string][]string) ([]string, error) {
	if len(nodes) == 0 {
		return nil, nil
	}

	// Index all known nodes.
	known := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		known[n] = true
	}

	// Validate that every dependency references a known node.
	for node, depList := range deps {
		for _, d := range depList {
			if !known[d] {
				return nil, &MissingError{Node: node, Missing: d}
			}
		}
	}

	// Build in-degree map and adjacency list (dep → dependents).
	// Iterate over nodes in input order so dependents[d] preserves the
	// order in which nodes declared their dependency on d. This keeps
	// Kahn's algorithm stable and deterministic.
	inDegree := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		inDegree[n] = 0
	}
	for _, node := range nodes {
		depList := deps[node]
		inDegree[node] = len(depList)
		for _, d := range depList {
			dependents[d] = append(dependents[d], node)
		}
	}

	// Seed the queue with nodes that have zero in-degree, in input order.
	queue := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if inDegree[n] == 0 {
			queue = append(queue, n)
		}
	}

	result := make([]string, 0, len(nodes))
	for len(queue) > 0 {
		// Pop front (stable: input order among zero-degree nodes).
		n := queue[0]
		queue = queue[1:]
		result = append(result, n)

		for _, dep := range dependents[n] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(result) != len(nodes) {
		// Cycle exists. Walk the remaining nodes to find one cycle path.
		remaining := make(map[string]bool, len(nodes)-len(result))
		for _, n := range nodes {
			if inDegree[n] > 0 {
				remaining[n] = true
			}
		}
		cycle := findCycle(nodes, deps, remaining)
		return nil, &CycleError{Path: cycle}
	}

	return result, nil
}

// findCycle traces one concrete cycle through the remaining (in-degree > 0)
// nodes using DFS. Returns a path like ["a", "b", "c", "a"].
func findCycle(nodes []string, deps map[string][]string, remaining map[string]bool) []string {
	visited := make(map[string]int) // 0=unvisited, 1=in-progress, 2=done
	parent := make(map[string]string)
	var cycleStart string

	var dfs func(string) bool
	dfs = func(n string) bool {
		visited[n] = 1
		for _, dep := range deps[n] {
			if !remaining[dep] {
				continue
			}
			if visited[dep] == 1 {
				cycleStart = dep
				parent[dep] = n // close the cycle
				return true
			}
			if visited[dep] == 0 {
				parent[dep] = n
				if dfs(dep) {
					return true
				}
			}
		}
		visited[n] = 2
		return false
	}

	for _, n := range nodes {
		if remaining[n] && visited[n] == 0 {
			if dfs(n) {
				break
			}
		}
	}

	if cycleStart == "" {
		return nil
	}

	// Reconstruct the cycle path.
	path := []string{cycleStart}
	cur := parent[cycleStart]
	for cur != cycleStart {
		path = append(path, cur)
		cur = parent[cur]
	}
	path = append(path, cycleStart)

	// Reverse to get start→...→start order.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}

	return path
}
