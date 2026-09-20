import { describe, it, expect } from 'vitest'
import type { AgentInfo } from '../hooks/agentInfoStore'
import { applyParentChildOrder } from './agent-order'

function makeAgent(partial: Partial<AgentInfo>): AgentInfo {
  return {
    Id: 'agent-1',
    ActorId: 'actor-1',
    DisplayName: 'Agent',
    Title: 'Agent',
    HasTitle: false,
    AgentKind: 'coder',
    ProjectId: 'proj-1',
    ProjectName: 'Project',
    Status: 'idle',
    StatusLabel: 'idle',
    IsWorking: false,
    IsError: false,
    IsCompleted: false,
    IsAskUserPermission: false,
    IsAskUser: false,
    IsAskPermission: false,
    IsPlanApproval: false,
    CompactionPolicyLoading: false,
    CanDelete: true,
    ...partial,
  } as AgentInfo
}

const ids = (agents: AgentInfo[]) => agents.map(a => a.Id)

describe('applyParentChildOrder', () => {
  it('returns input unchanged for empty or single-agent lists', () => {
    expect(applyParentChildOrder([])).toEqual([])
    const single = [makeAgent({ Id: 'a', ActorId: 'A' })]
    expect(applyParentChildOrder(single)).toBe(single)
  })

  it('returns input unchanged when there are no parent-child links', () => {
    const agents = [
      makeAgent({ Id: 'a', ActorId: 'A' }),
      makeAgent({ Id: 'b', ActorId: 'B' }),
    ]
    expect(applyParentChildOrder(agents)).toBe(agents)
  })

  it('moves a child that precedes its parent to after the parent', () => {
    const parent = makeAgent({ Id: 'parent', ActorId: 'P' })
    const child = makeAgent({ Id: 'child', ActorId: 'C', ParentAgentId: 'P' })
    expect(ids(applyParentChildOrder([child, parent]))).toEqual(['parent', 'child'])
  })

  it('leaves already-correct order untouched', () => {
    const parent = makeAgent({ Id: 'parent', ActorId: 'P' })
    const child = makeAgent({ Id: 'child', ActorId: 'C', ParentAgentId: 'P' })
    expect(ids(applyParentChildOrder([parent, child]))).toEqual(['parent', 'child'])
  })

  it('expands the full subtree before the next root (depth-first)', () => {
    const a = makeAgent({ Id: 'a', ActorId: 'A' })
    const parent = makeAgent({ Id: 'parent', ActorId: 'P' })
    const c1 = makeAgent({ Id: 'c1', ActorId: 'C1', ParentAgentId: 'P' })
    const c2 = makeAgent({ Id: 'c2', ActorId: 'C2', ParentAgentId: 'P' })
    const b = makeAgent({ Id: 'b', ActorId: 'B' })
    // Roots keep input order; the parent's whole subtree follows it before b.
    expect(ids(applyParentChildOrder([a, c1, parent, b, c2]))).toEqual([
      'a', 'parent', 'c1', 'c2', 'b',
    ])
  })

  it('handles nested chains (grandchild after child after parent)', () => {
    const root = makeAgent({ Id: 'root', ActorId: 'R' })
    const mid = makeAgent({ Id: 'mid', ActorId: 'M', ParentAgentId: 'R' })
    const leaf = makeAgent({ Id: 'leaf', ActorId: 'L', ParentAgentId: 'M' })
    expect(ids(applyParentChildOrder([leaf, mid, root]))).toEqual([
      'root', 'mid', 'leaf',
    ])
  })

  it('treats an agent as a root when its parent is absent from the set', () => {
    const orphan = makeAgent({ Id: 'orphan', ActorId: 'O', ParentAgentId: 'GHOST' })
    const other = makeAgent({ Id: 'other', ActorId: 'X' })
    expect(ids(applyParentChildOrder([orphan, other]))).toEqual(['orphan', 'other'])
  })

  it('interleaves branches depth-first instead of trailing children at the end', () => {
    const p1 = makeAgent({ Id: 'p1', ActorId: 'P1' })
    const p2 = makeAgent({ Id: 'p2', ActorId: 'P2' })
    const c1 = makeAgent({ Id: 'c1', ActorId: 'C1', ParentAgentId: 'P1' })
    const c2 = makeAgent({ Id: 'c2', ActorId: 'C2', ParentAgentId: 'P2' })
    expect(ids(applyParentChildOrder([p1, p2, c1, c2]))).toEqual([
      'p1', 'c1', 'p2', 'c2',
    ])
  })

  it('expands nested subtrees depth-first across sibling roots', () => {
    const p1 = makeAgent({ Id: 'p1', ActorId: 'P1' })
    const c1 = makeAgent({ Id: 'c1', ActorId: 'C1', ParentAgentId: 'P1' })
    const g1 = makeAgent({ Id: 'g1', ActorId: 'G1', ParentAgentId: 'C1' })
    const p2 = makeAgent({ Id: 'p2', ActorId: 'P2' })
    expect(ids(applyParentChildOrder([p1, p2, c1, g1]))).toEqual([
      'p1', 'c1', 'g1', 'p2',
    ])
  })

  it('orders children by the parent Children projection when present', () => {
    const parent = makeAgent({
      Id: 'parent',
      ActorId: 'P',
      Children: [
        { Id: 'c2', ActorId: 'C2' },
        { Id: 'c1', ActorId: 'C1' },
      ],
    })
    const c1 = makeAgent({ Id: 'c1', ActorId: 'C1', ParentAgentId: 'P' })
    const c2 = makeAgent({ Id: 'c2', ActorId: 'C2', ParentAgentId: 'P' })
    // Input order disagrees with the projection; the projection wins.
    expect(ids(applyParentChildOrder([parent, c1, c2]))).toEqual([
      'parent', 'c2', 'c1',
    ])
  })

  it('breaks cycles by appending unreachable nodes in input order', () => {
    const a = makeAgent({ Id: 'a', ActorId: 'A', ParentAgentId: 'B' })
    const b = makeAgent({ Id: 'b', ActorId: 'B', ParentAgentId: 'A' })
    const root = makeAgent({ Id: 'root', ActorId: 'R' })
    expect(ids(applyParentChildOrder([a, b, root]))).toEqual(['root', 'a', 'b'])
  })

  it('ignores a self-referencing ParentAgentId', () => {
    const a = makeAgent({ Id: 'a', ActorId: 'A', ParentAgentId: 'A' })
    const b = makeAgent({ Id: 'b', ActorId: 'B' })
    expect(ids(applyParentChildOrder([a, b]))).toEqual(['a', 'b'])
  })
})
