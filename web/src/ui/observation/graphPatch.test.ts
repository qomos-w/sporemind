import { describe, it, expect } from 'vitest'
import { applyPatch, applyPatches } from './graphPatch'
import type { UnifiedGraph, GraphPatch, UnifiedGraphNode, UnifiedGraphEdge } from '../../gen-types/observation'

function node(id: string, overrides?: Partial<UnifiedGraphNode>): UnifiedGraphNode {
  return { Id: id, Kind: 'actor', Label: id, Status: 'running', ...overrides }
}

function edge(id: string, from: string, to: string, overrides?: Partial<UnifiedGraphEdge>): UnifiedGraphEdge {
  return { Id: id, From: from, To: to, Kind: 'link', ...overrides }
}

function patch(epoch: number, overrides?: Partial<GraphPatch>): GraphPatch {
  return {
    Epoch: epoch,
    Timestamp: '2026-01-01T00:00:00Z',
    AddedNodes: [],
    RemovedNodes: [],
    UpdatedNodes: [],
    AddedEdges: [],
    RemovedEdges: [],
    ...overrides,
  }
}

function graph(nodes: UnifiedGraphNode[] = [], edges: UnifiedGraphEdge[] = []): UnifiedGraph {
  return { Version: '1', Nodes: nodes, Edges: edges }
}

describe('applyPatch', () => {
  it('adds nodes', () => {
    const g = graph([node('a')])
    const p = patch(2, { AddedNodes: [node('b'), node('c')] })
    const result = applyPatch(g, p)
    expect(result.Nodes.map(n => n.Id)).toEqual(['a', 'b', 'c'])
    expect(result.Version).toBe('2')
  })

  it('removes nodes by id', () => {
    const g = graph([node('a'), node('b'), node('c')])
    const p = patch(3, { RemovedNodes: ['b'] })
    const result = applyPatch(g, p)
    expect(result.Nodes.map(n => n.Id)).toEqual(['a', 'c'])
  })

  it('updates nodes by replacing same id', () => {
    const g = graph([node('a', { Label: 'old', Status: 'running' })])
    const p = patch(4, {
      UpdatedNodes: [node('a', { Label: 'new', Status: 'stopped' })],
    })
    const result = applyPatch(g, p)
    expect(result.Nodes).toHaveLength(1)
    expect(result.Nodes[0]!.Label).toBe('new')
    expect(result.Nodes[0]!.Status).toBe('stopped')
  })

  it('adds edges', () => {
    const g = graph([node('a'), node('b')], [edge('e1', 'a', 'b')])
    const p = patch(2, { AddedEdges: [edge('e2', 'b', 'a')] })
    const result = applyPatch(g, p)
    expect(result.Edges.map(e => e.Id)).toEqual(['e1', 'e2'])
  })

  it('removes edges by id', () => {
    const g = graph([], [edge('e1', 'a', 'b'), edge('e2', 'b', 'c')])
    const p = patch(3, { RemovedEdges: ['e1'] })
    const result = applyPatch(g, p)
    expect(result.Edges.map(e => e.Id)).toEqual(['e2'])
  })

  it('applies multiple change types in one patch', () => {
    const g = graph(
      [node('a'), node('b'), node('c')],
      [edge('e1', 'a', 'b'), edge('e2', 'b', 'c')],
    )
    const p = patch(5, {
      RemovedNodes: ['b'],
      AddedNodes: [node('d')],
      UpdatedNodes: [node('a', { Label: 'updated' })],
      RemovedEdges: ['e1'],
      AddedEdges: [edge('e3', 'a', 'd')],
    })
    const result = applyPatch(g, p)
    expect(result.Nodes.map(n => n.Id)).toEqual(['a', 'c', 'd'])
    expect(result.Nodes[0]!.Label).toBe('updated')
    expect(result.Edges.map(e => e.Id)).toEqual(['e2', 'e3'])
  })

  it('returns a new graph object (does not mutate input)', () => {
    const g = graph([node('a')])
    const p = patch(2, { AddedNodes: [node('b')] })
    const result = applyPatch(g, p)
    expect(result).not.toBe(g)
    expect(g.Nodes).toHaveLength(1)
  })
})

describe('applyPatches', () => {
  it('applies patches in order', () => {
    const g = graph([node('a')])
    const patches = [
      patch(2, { AddedNodes: [node('b')] }),
      patch(3, { AddedNodes: [node('c')], RemovedNodes: ['a'] }),
      patch(4, { UpdatedNodes: [node('b', { Label: 'changed' })] }),
    ]
    const result = applyPatches(g, patches)
    expect(result.Nodes.map(n => n.Id)).toEqual(['b', 'c'])
    expect(result.Nodes[0]!.Label).toBe('changed')
    expect(result.Version).toBe('4')
  })

  it('handles empty patch list', () => {
    const g = graph([node('a')])
    const result = applyPatches(g, [])
    expect(result.Nodes.map(n => n.Id)).toEqual(['a'])
    expect(result.Version).toBe('1')
  })

  it('overwrites version with last patch epoch', () => {
    const g = graph([], [],)
    const patches = [
      patch(10, { AddedNodes: [node('x')] }),
      patch(20, { AddedNodes: [node('y')] }),
    ]
    const result = applyPatches(g, patches)
    expect(result.Version).toBe('20')
  })
})
