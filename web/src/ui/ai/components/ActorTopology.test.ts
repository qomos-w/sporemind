import { describe, expect, it } from 'vitest'
import type { UnifiedGraph, UnifiedGraphNode, UnifiedGraphEdge } from '../../../gen-types/observation'
import { nodeKindBucket, stateColor, buildTopologyEdges } from './ActorTopology'

function node(kind: string, overrides?: Partial<UnifiedGraphNode>): UnifiedGraphNode {
  return { Id: 'n1', Kind: kind, Label: 'x', Status: 'running', ...overrides }
}

function graph(nodes: UnifiedGraphNode[], edges: UnifiedGraphEdge[]): UnifiedGraph {
  return { Version: '1', Nodes: nodes, Edges: edges }
}

function edge(from: string, to: string, kind: string, overrides?: Partial<UnifiedGraphEdge>): UnifiedGraphEdge {
  return { Id: `${from}→${to}`, From: from, To: to, Kind: kind, ...overrides }
}

describe('nodeKindBucket', () => {
  it('classifies mcp-server nodes into their own bucket', () => {
    expect(nodeKindBucket(node('mcp-server'))).toBe('mcp-server')
  })

  it('keeps existing bucket classification', () => {
    expect(nodeKindBucket(node('project'))).toBe('project')
    expect(nodeKindBucket(node('actor', { ActorType: 'agent' }))).toBe('agent')
    expect(nodeKindBucket(node('actor', { ActorType: 'aggregator' }))).toBe('aggregator')
    expect(nodeKindBucket(node('actor', { ActorType: 'executor' }))).toBe('executor')
    expect(nodeKindBucket(node('turn'))).toBe('other')
    expect(nodeKindBucket(node('actor'))).toBe('other')
  })
})

describe('stateColor', () => {
  it('renders mcp-server connected state green', () => {
    const c = stateColor('connected')
    expect(c.bg).toBeTruthy()
    // Same bucket as the running state colour: green.
    expect(stateColor('connected')).toEqual(stateColor('running'))
  })

  it('renders mcp-server disconnected state neutral gray', () => {
    expect(stateColor('disconnected')).toEqual(stateColor('stopped'))
  })

  it('renders mcp-server error state red', () => {
    expect(stateColor('error')).toEqual(stateColor('failed'))
  })
})

describe('buildTopologyEdges', () => {
  it('processes explicit edges before parent-child fallback edges', () => {
    // The node has ParentId=proj, but there is also an explicit agent_child edge
    // from parent-agent. The explicit edge should win (deduped by from→to id).
    const g = graph(
      [
        { Id: 'proj', Kind: 'project', Label: 'p', Status: 'running' },
        { Id: 'parent-agent', Kind: 'agent', Label: 'pa', Status: 'idle', ParentId: 'proj' },
        { Id: 'child-agent', Kind: 'agent', Label: 'ca', Status: 'running', ParentId: 'parent-agent' },
      ],
      [
        edge('parent-agent', 'child-agent', 'agent_child', { LifecycleScope: 'workflow' }),
      ],
    )

    const { edges } = buildTopologyEdges(g)
    const childEdge = edges.find(e => e.to === 'child-agent')
    expect(childEdge).toBeDefined()
    // The explicit agent_child edge should win (not the child fallback).
    expect(childEdge!.title).toBe('agent child (logical parent)')
  })

  it('gives agent_child edges distinct purple styling', () => {
    const g = graph([], [edge('parent', 'child', 'agent_child')])
    const { edges } = buildTopologyEdges(g)
    expect(edges).toHaveLength(1)
    expect(edges[0]!.color.color).not.toBe(getCssVarBorder())
    expect(edges[0]!.title).toBe('agent child (logical parent)')
    expect(edges[0]!.dashes).toBeUndefined()
  })

  it('renders call edges as dashed', () => {
    const g = graph([], [edge('agent-1', 'mcp-1', 'call')])
    const { edges } = buildTopologyEdges(g)
    expect(edges[0]!.dashes).toBe(true)
    expect(edges[0]!.title).toBe('tool call')
  })

  it('adds parent-child fallback edges for nodes without explicit edges', () => {
    const g = graph(
      [
        { Id: 'ws', Kind: 'workspace', Label: 'ws', Status: 'running' },
        { Id: 'proj', Kind: 'project', Label: 'proj', Status: 'running', ParentId: 'ws' },
      ],
      [],
    )
    const { edges } = buildTopologyEdges(g)
    expect(edges.find(e => e.from === 'ws' && e.to === 'proj')).toBeDefined()
  })

  it('deduplicates parent-child and explicit edges with the same from→to', () => {
    const g = graph(
      [{ Id: 'a', Kind: 'agent', Label: 'a', Status: 'idle', ParentId: 'root' }],
      [edge('root', 'a', 'agent_child')],
    )
    const { edges } = buildTopologyEdges(g)
    // Only one edge root→a, and it should be the agent_child (explicit, processed first).
    const aEdges = edges.filter(e => e.to === 'a')
    expect(aEdges).toHaveLength(1)
    expect(aEdges[0]!.title).toBe('agent child (logical parent)')
  })

  it('skips self-loop edges', () => {
    const g = graph(
      [{ Id: 'a', Kind: 'agent', Label: 'a', Status: 'idle', ParentId: 'a' }],
      [edge('a', 'a', 'child')],
    )
    const { edges } = buildTopologyEdges(g)
    expect(edges).toHaveLength(0)
  })

  it('produces neutral styling for plain child edges', () => {
    const g = graph(
      [
        { Id: 'ws', Kind: 'workspace', Label: 'ws', Status: 'running' },
        { Id: 'proj', Kind: 'project', Label: 'proj', Status: 'running', ParentId: 'ws' },
      ],
      [],
    )
    const { edges } = buildTopologyEdges(g)
    const childEdge = edges.find(e => e.from === 'ws' && e.to === 'proj')!
    expect(childEdge.title).toBeUndefined()
    expect(childEdge.dashes).toBeUndefined()
  })
})

/** Helper to read the border CSS var for comparison in tests. */
function getCssVarBorder(): string {
  if (typeof window === 'undefined') return '#475569'
  return getComputedStyle(document.documentElement).getPropertyValue('--border-default').trim() || '#475569'
}
