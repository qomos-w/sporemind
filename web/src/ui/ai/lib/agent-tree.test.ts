import { describe, it, expect } from 'vitest'
import type { AgentChildRef } from '../../../gen-types/aigen.part2'
import type { AgentInfo } from '../hooks/agentInfoStore'
import { buildAgentForest, flattenAgentForest } from './agent-tree'

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

function makeChildRef(partial: Partial<AgentChildRef>): AgentChildRef {
  return {
    Id: 'child-1',
    ActorId: 'child-actor-1',
    ...partial,
  }
}

describe('buildAgentForest', () => {
  it('returns empty array for empty input', () => {
    expect(buildAgentForest([])).toEqual([])
  })

  it('returns flat roots when no parent-child relationships', () => {
    const agents = [
      makeAgent({ Id: 'a', ActorId: 'A', DisplayName: 'Alpha' }),
      makeAgent({ Id: 'b', ActorId: 'B', DisplayName: 'Beta' }),
    ]
    const forest = buildAgentForest(agents)
    expect(forest).toHaveLength(2)
    expect(forest[0]!.id).toBe('a')
    expect(forest[0]!.children).toEqual([])
    expect(forest[1]!.id).toBe('b')
    expect(forest[1]!.children).toEqual([])
  })

  it('builds a two-level tree from Children projection', () => {
    const parent = makeAgent({
      Id: 'parent', ActorId: 'P', DisplayName: 'Parent',
      Children: [
        makeChildRef({ Id: 'child', ActorId: 'C', DisplayName: 'Child' }),
      ],
    })
    const child = makeAgent({
      Id: 'child', ActorId: 'C', DisplayName: 'Child',
      ParentAgentId: 'P',
    })
    const forest = buildAgentForest([parent, child])
    expect(forest).toHaveLength(1)
    const root = forest[0]!
    expect(root.id).toBe('parent')
    expect(root.children).toHaveLength(1)
    expect(root.children[0]!.agent).toBe(child)
    expect(root.children[0]!.id).toBe('child')
  })

  it('prevents cycles by pruning branches that revisit an ancestor', () => {
    const agentA = makeAgent({
      Id: 'a', ActorId: 'A', DisplayName: 'Alpha',
      Children: [
        makeChildRef({ Id: 'b', ActorId: 'B', DisplayName: 'Beta' }),
      ],
    })
    const agentB = makeAgent({
      Id: 'b', ActorId: 'B', DisplayName: 'Beta',
      ParentAgentId: 'A',
      Children: [
        makeChildRef({ Id: 'a', ActorId: 'A', DisplayName: 'Alpha' }),
      ],
    })
    const forest = buildAgentForest([agentA, agentB])
    expect(forest).toHaveLength(1)
    const root = forest[0]!
    expect(root.id).toBe('a')
    expect(root.children).toHaveLength(1)
    expect(root.children[0]!.id).toBe('b')
    // B → A is pruned (cycle)
    expect(root.children[0]!.children).toEqual([])
  })

  it('supports deep nesting (3+ levels)', () => {
    const root = makeAgent({
      Id: 'root', ActorId: 'R', DisplayName: 'Root',
      Children: [
        makeChildRef({ Id: 'mid', ActorId: 'M', DisplayName: 'Mid' }),
      ],
    })
    const mid = makeAgent({
      Id: 'mid', ActorId: 'M', DisplayName: 'Mid',
      ParentAgentId: 'R',
      Children: [
        makeChildRef({ Id: 'leaf', ActorId: 'L', DisplayName: 'Leaf' }),
      ],
    })
    const leaf = makeAgent({
      Id: 'leaf', ActorId: 'L', DisplayName: 'Leaf',
      ParentAgentId: 'M',
    })
    const forest = buildAgentForest([root, mid, leaf])
    expect(forest).toHaveLength(1)
    expect(forest[0]!.depth).toBe(0)
    expect(forest[0]!.children[0]!.id).toBe('mid')
    expect(forest[0]!.children[0]!.depth).toBe(1)
    expect(forest[0]!.children[0]!.children[0]!.id).toBe('leaf')
    expect(forest[0]!.children[0]!.children[0]!.depth).toBe(2)
  })

  it('sets isLastChild and guideLines correctly', () => {
    const parent = makeAgent({
      Id: 'parent', ActorId: 'P', DisplayName: 'Parent',
      Children: [
        makeChildRef({ Id: 'c1', ActorId: 'C1', DisplayName: 'Child 1' }),
        makeChildRef({ Id: 'c2', ActorId: 'C2', DisplayName: 'Child 2' }),
      ],
    })
    const c1 = makeAgent({ Id: 'c1', ActorId: 'C1', DisplayName: 'Child 1', ParentAgentId: 'P' })
    const c2 = makeAgent({ Id: 'c2', ActorId: 'C2', DisplayName: 'Child 2', ParentAgentId: 'P' })
    const forest = buildAgentForest([parent, c1, c2])
    const root = forest[0]!
    expect(root.isLastChild).toBe(true)
    expect(root.guideLines).toEqual([])
    expect(root.children[0]!.isLastChild).toBe(false)
    expect(root.children[0]!.guideLines).toEqual([false])
    expect(root.children[1]!.isLastChild).toBe(true)
    expect(root.children[1]!.guideLines).toEqual([false])
  })

  it('promotes orphan agents (missing parent) to root level', () => {
    // Agent has ParentAgentId but the parent is not in the list at all.
    const orphan = makeAgent({
      Id: 'orphan', ActorId: 'O', DisplayName: 'Orphan',
      ParentAgentId: 'MISSING-PARENT',
    })
    const root = makeAgent({
      Id: 'root', ActorId: 'R', DisplayName: 'Root',
    })
    const forest = buildAgentForest([orphan, root])
    // Root is the natural root; orphan is promoted because its parent
    // doesn't exist in the list.
    expect(forest).toHaveLength(2)
    const ids = forest.map(n => n.id).sort()
    expect(ids).toEqual(['orphan', 'root'])
    // Orphan retains its ParentAgentId on the underlying AgentInfo.
    const orphanNode = forest.find(n => n.id === 'orphan')!
    expect(orphanNode.agent?.ParentAgentId).toBe('MISSING-PARENT')
    expect(orphanNode.depth).toBe(0)
    expect(orphanNode.children).toEqual([])
  })

  it('promotes orphan agents (parent exists but Children projection stale) to root level', () => {
    // Parent exists but its Children array does not reference the child.
    const parent = makeAgent({
      Id: 'parent', ActorId: 'P', DisplayName: 'Parent',
      Children: [
        // References a different child, not 'orphan-child'
        makeChildRef({ Id: 'other-child', ActorId: 'OC', DisplayName: 'Other' }),
      ],
    })
    const orphanChild = makeAgent({
      Id: 'orphan-child', ActorId: 'OC2', DisplayName: 'Orphan Child',
      ParentAgentId: 'P',
    })
    const otherChild = makeAgent({
      Id: 'other-child', ActorId: 'OC', DisplayName: 'Other',
      ParentAgentId: 'P',
    })
    const forest = buildAgentForest([parent, orphanChild, otherChild])
    // parent is root, other-child is mounted under parent's Children,
    // orphan-child is NOT in parent's Children → promoted to root.
    expect(forest).toHaveLength(2)
    const parentNode = forest.find(n => n.id === 'parent')!
    expect(parentNode.children).toHaveLength(1)
    expect(parentNode.children[0]!.id).toBe('other-child')
    const orphanNode = forest.find(n => n.id === 'orphan-child')!
    expect(orphanNode.depth).toBe(0)
    expect(orphanNode.agent?.ParentAgentId).toBe('P')
  })

  it('sets guideLines for grandchild of non-last child', () => {
    const rootA = makeAgent({
      Id: 'ra', ActorId: 'RA', DisplayName: 'Root A',
      Children: [
        makeChildRef({ Id: 'ma', ActorId: 'MA', DisplayName: 'Mid A' }),
      ],
    })
    const rootB = makeAgent({
      Id: 'rb', ActorId: 'RB', DisplayName: 'Root B',
    })
    const midA = makeAgent({
      Id: 'ma', ActorId: 'MA', DisplayName: 'Mid A',
      ParentAgentId: 'RA',
      Children: [
        makeChildRef({ Id: 'la', ActorId: 'LA', DisplayName: 'Leaf A' }),
      ],
    })
    const leafA = makeAgent({
      Id: 'la', ActorId: 'LA', DisplayName: 'Leaf A',
      ParentAgentId: 'MA',
    })
    const forest = buildAgentForest([rootA, rootB, midA, leafA])
    expect(forest).toHaveLength(2)
    expect(forest[0]!.isLastChild).toBe(false)
    const mid = forest[0]!.children[0]!
    expect(mid.depth).toBe(1)
    expect(mid.guideLines).toEqual([true])
    const leaf = mid.children[0]!
    expect(leaf.depth).toBe(2)
    expect(leaf.guideLines).toEqual([true, false])
  })
})

describe('flattenAgentForest', () => {
  it('flattens in depth-first order', () => {
    const root = makeAgent({
      Id: 'root', ActorId: 'R', DisplayName: 'Root',
      Children: [
        makeChildRef({ Id: 'c1', ActorId: 'C1', DisplayName: 'C1' }),
        makeChildRef({ Id: 'c2', ActorId: 'C2', DisplayName: 'C2' }),
      ],
    })
    const c1 = makeAgent({ Id: 'c1', ActorId: 'C1', DisplayName: 'C1', ParentAgentId: 'R' })
    const c2 = makeAgent({ Id: 'c2', ActorId: 'C2', DisplayName: 'C2', ParentAgentId: 'R' })
    const forest = buildAgentForest([root, c1, c2])
    const flat = flattenAgentForest(forest)
    expect(flat.map(n => n.id)).toEqual(['root', 'c1', 'c2'])
  })

  it('handles nested flattening', () => {
    const root = makeAgent({
      Id: 'root', ActorId: 'R', DisplayName: 'Root',
      Children: [
        makeChildRef({ Id: 'mid', ActorId: 'M', DisplayName: 'Mid' }),
      ],
    })
    const mid = makeAgent({
      Id: 'mid', ActorId: 'M', DisplayName: 'Mid',
      ParentAgentId: 'R',
      Children: [
        makeChildRef({ Id: 'leaf', ActorId: 'L', DisplayName: 'Leaf' }),
      ],
    })
    const leaf = makeAgent({ Id: 'leaf', ActorId: 'L', DisplayName: 'Leaf', ParentAgentId: 'M' })
    const forest = buildAgentForest([root, mid, leaf])
    const flat = flattenAgentForest(forest)
    expect(flat.map(n => n.id)).toEqual(['root', 'mid', 'leaf'])
  })
})
