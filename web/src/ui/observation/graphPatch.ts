import type { UnifiedGraph, GraphPatch } from '../../gen-types/observation'

export function applyPatch(graph: UnifiedGraph, patch: GraphPatch): UnifiedGraph {
  const nodeMap = new Map(graph.Nodes.map(n => [n.Id, n]))
  const edgeMap = new Map(graph.Edges.map(e => [e.Id, e]))

  for (const id of patch.RemovedNodes ?? []) nodeMap.delete(id)
  for (const id of patch.RemovedEdges ?? []) edgeMap.delete(id)
  for (const n of patch.AddedNodes ?? []) nodeMap.set(n.Id, n)
  for (const n of patch.UpdatedNodes ?? []) nodeMap.set(n.Id, n)
  for (const e of patch.AddedEdges ?? []) edgeMap.set(e.Id, e)

  return {
    Version: String(patch.Epoch),
    Nodes: Array.from(nodeMap.values()),
    Edges: Array.from(edgeMap.values()),
  }
}

export function applyPatches(graph: UnifiedGraph, patches: GraphPatch[]): UnifiedGraph {
  for (const p of patches) {
    graph = applyPatch(graph, p)
  }
  return graph
}
