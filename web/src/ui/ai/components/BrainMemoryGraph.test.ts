import { describe, expect, it } from 'vitest'
import { buildBrainVisGraph } from './BrainMemoryGraph'
import type { MemorySnapshotResp } from '../../../gen-clients/system/types'

const snapshot: MemorySnapshotResp = {
  Mounted: true,
  SleepDue: false,
  Nodes: [
    { Id: 'a', Layer: 'session', Content: 'first memory', Head: '', Tokens: 3, Energy: 2, CreatedAt: '2026-01-01', AccessedAt: '2026-01-02', MarkedForDeath: false, Protected: false, LastAccessTick: 0 },
    { Id: 'b', Layer: 'experience', Content: 'second memory body', Head: 'second memory head', Tokens: 5, Energy: 1, CreatedAt: '2026-01-01', AccessedAt: '2026-01-03', MarkedForDeath: true, Protected: false, LastAccessTick: 0 },
  ],
  Edges: [{ From: 'a', To: 'b', Type: 'antagonist' }],
}

describe('buildBrainVisGraph', () => {
  it('maps the backend snapshot without adding mutations', () => {
    const graph = buildBrainVisGraph(snapshot)
    expect(graph.nodes).toHaveLength(6)
    expect(graph.edges).toHaveLength(6)
    expect(graph.nodes[0]).toMatchObject({ id: 'builtin:brain', label: 'Brain', x: 0, y: 0 })
    expect(graph.nodes[4]).toMatchObject({ id: 'a', label: 'first memory' })
    expect(graph.nodes[5]).toMatchObject({ id: 'b', label: 'second memory head', borderWidth: 3 })
    expect(graph.edges[5]).toMatchObject({ from: 'a', to: 'b', dashes: [6, 4] })
  })
})
