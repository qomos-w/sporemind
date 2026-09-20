import { describe, it, expect, vi } from 'vitest'
import {
  aggregateKbSearch,
  filterOutlineNodes,
  flattenOutlineNodes,
  isVirtualMountNode,
  nodeMatchesQuery,
  outlineRootsFromTocNodes,
  searchLoadedOutlines,
  setExpanded,
  toKbOutlineProject,
  toggleId,
  type KbOutlineNode,
  type KbLoadedOutline,
  type KbOutlineProject,
  type KbOutlineSearchHit,
} from './knowledgeLeftOutline.logic'
import type { ProjectRef, WikiCardTreeNode } from '../../../gen-clients/system/types'

const node = (id: string, children: KbOutlineNode[] = []): KbOutlineNode => ({
  id,
  title: id,
  type: 'wiki',
  status: '',
  modified: '',
  children,
})

const treeNode = (id: string, children: WikiCardTreeNode[] = []): WikiCardTreeNode => ({
  Id: id,
  Title: id,
  Type: 'wiki',
  Status: '',
  Modified: '',
  Children: children,
})

describe('isVirtualMountNode', () => {
  it('flags __builtin_*__ bucket ids', () => {
    expect(isVirtualMountNode('__builtin_task__')).toBe(true)
    expect(isVirtualMountNode('__builtin_todo__')).toBe(true)
    expect(isVirtualMountNode('my-card')).toBe(false)
    expect(isVirtualMountNode('toc')).toBe(false)
  })
})

describe('toKbOutlineProject', () => {
  it('maps a mounted project ref to its actor id + name', () => {
    const ref = { Name: 'alpha', Path: '/a', Root: true, ActorId: 'actor-1' } as ProjectRef
    expect(toKbOutlineProject(ref)).toEqual({ projectId: 'actor-1', name: 'alpha' })
  })

  it('returns null for a ref with no actor id', () => {
    const ref = { Name: 'alpha', Path: '/a', Root: true } as ProjectRef
    expect(toKbOutlineProject(ref)).toBeNull()
  })

  it('falls back to the actor id as the label when unnamed', () => {
    const ref = { Name: '', Path: '/a', Root: true, ActorId: 'actor-9' } as ProjectRef
    expect(toKbOutlineProject(ref)).toEqual({ projectId: 'actor-9', name: 'actor-9' })
  })
})

describe('outlineRootsFromTocNodes', () => {
  it('unwraps a single toc root to its children', () => {
    const roots = outlineRootsFromTocNodes([treeNode('toc', [treeNode('a'), treeNode('b')])])
    expect(roots.map(n => n.id)).toEqual(['a', 'b'])
  })

  it('drops virtual mount nodes recursively', () => {
    const roots = outlineRootsFromTocNodes([
      treeNode('toc', [treeNode('a', [treeNode('__builtin_task__')]), treeNode('__builtin_todo__')]),
    ])
    expect(roots.map(n => n.id)).toEqual(['a'])
    expect(roots[0]!.children).toEqual([])
  })

  it('normalizes an already-unwrapped list as-is', () => {
    const roots = outlineRootsFromTocNodes([treeNode('a'), treeNode('b')])
    expect(roots.map(n => n.id)).toEqual(['a', 'b'])
  })

  it('uses the id as the title when the node has none', () => {
    const roots = outlineRootsFromTocNodes([
      treeNode('toc', [{ ...treeNode('a'), Title: '' }]),
    ])
    expect(roots[0]!.title).toBe('a')
  })

  it('returns an empty list for an empty response', () => {
    expect(outlineRootsFromTocNodes([])).toEqual([])
  })
})

describe('flattenOutlineNodes', () => {
  it('lists every node depth-first', () => {
    const tree = [node('a', [node('a1'), node('a2')]), node('b')]
    expect(flattenOutlineNodes(tree).map(n => n.id)).toEqual(['a', 'a1', 'a2', 'b'])
  })
})

describe('toggleId / setExpanded', () => {
  it('toggles without mutating the input set', () => {
    const base = new Set(['a'])
    const added = toggleId(base, 'b')
    expect([...added].sort()).toEqual(['a', 'b'])
    expect([...base]).toEqual(['a'])
    expect(toggleId(added, 'a').has('a')).toBe(false)
  })

  it('explicitly sets and unsets an id', () => {
    expect([...setExpanded(new Set(), 'x', true)]).toEqual(['x'])
    expect([...setExpanded(new Set(['x']), 'x', false)]).toEqual([])
  })
})

describe('nodeMatchesQuery', () => {
  it('matches title or id case-insensitively', () => {
    expect(nodeMatchesQuery(node('Alpha'), 'alp')).toBe(true)
    expect(nodeMatchesQuery(node('Alpha'), 'bet')).toBe(false)
    expect(nodeMatchesQuery(node('Alpha'), '  ')).toBe(true)
  })
})

describe('filterOutlineNodes', () => {
  it('keeps matching nodes and their ancestors', () => {
    const tree = [node('parent', [node('child-match'), node('other')]), node('sibling')]
    const filtered = filterOutlineNodes(tree, 'match')
    expect(filtered.map(n => n.id)).toEqual(['parent'])
    expect(filtered[0]!.children.map(n => n.id)).toEqual(['child-match'])
  })

  it('returns the full tree for an empty query', () => {
    const tree = [node('a', [node('b')])]
    expect(filterOutlineNodes(tree, '')).toHaveLength(1)
    expect(filterOutlineNodes(tree, '')[0]!.children).toHaveLength(1)
  })
})

describe('searchLoadedOutlines', () => {
  it('returns nothing for an empty query', () => {
    const loaded: KbLoadedOutline[] = [{ projectId: 'p1', projectName: 'P1', nodes: [node('alpha')] }]
    expect(searchLoadedOutlines('', loaded)).toEqual([])
  })

  it('tags hits with the owning project and the local origin', () => {
    const loaded: KbLoadedOutline[] = [
      { projectId: 'p1', projectName: 'P1', nodes: [node('alpha'), node('beta')] },
      { projectId: 'p2', projectName: 'P2', nodes: [node('alpha-two')] },
    ]
    const hits = searchLoadedOutlines('alpha', loaded)
    expect(hits).toEqual([
      { projectId: 'p1', projectName: 'P1', cardId: 'alpha', title: 'alpha', origin: 'local' },
      { projectId: 'p2', projectName: 'P2', cardId: 'alpha-two', title: 'alpha-two', origin: 'local' },
    ])
  })
})

describe('aggregateKbSearch', () => {
  const projects = [
    { projectId: 'p1', name: 'P1' },
    { projectId: 'p2', name: 'P2' },
  ]

  it('answers loaded projects locally and never calls the remote search', async () => {
    const remote = vi.fn(async () => [])
    const hits = await aggregateKbSearch(
      'alpha',
      projects,
      { p1: [node('alpha')], p2: [node('alpha-two')] },
      remote,
    )
    expect(remote).not.toHaveBeenCalled()
    expect(hits.map(h => h.cardId)).toEqual(['alpha', 'alpha-two'])
  })

  it('delegates only not-yet-loaded projects to the remote search', async () => {
    const remote = vi.fn(async (_q: string, ps: readonly { projectId: string }[]) =>
      ps.map(p => ({
        projectId: p.projectId,
        projectName: p.projectId,
        cardId: 'remote-' + p.projectId,
        title: 'remote-' + p.projectId,
        origin: 'remote' as const,
      })),
    )
    const hits = await aggregateKbSearch('alpha', projects, { p1: [node('alpha')] }, remote)
    expect(remote).toHaveBeenCalledTimes(1)
    expect(remote.mock.calls[0]![1].map(p => p.projectId)).toEqual(['p2'])
    expect(hits.map(h => h.cardId)).toEqual(['alpha', 'remote-p2'])
  })

  it('keeps local hits when the remote search fails', async () => {
    const remote = vi.fn(async () => { throw new Error('offline') })
    const hits = await aggregateKbSearch('alpha', projects, { p1: [node('alpha')] }, remote)
    expect(hits.map(h => h.cardId)).toEqual(['alpha'])
  })

  it('treats an undefined entry as not-loaded', async () => {
    const remote = vi.fn(async (_q: string, _ps: readonly KbOutlineProject[]) => [] as KbOutlineSearchHit[])
    await aggregateKbSearch('alpha', projects, { p1: undefined, p2: [node('alpha')] }, remote)
    expect(remote).toHaveBeenCalledTimes(1)
    expect(remote.mock.calls[0]![1].map(p => p.projectId)).toEqual(['p1'])
  })
})
