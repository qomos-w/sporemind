import { describe, it, expect } from 'vitest'
import {
  KB_PROJECT_ROOT_CARD_ID,
  isLaneChild,
  laneCollapsedIds,
  laneKey,
  resolveLaneCards,
} from './knowledgeMode.logic'
import type { LaneStackCard } from './knowledgeMode.logic'
import type { SwimlaneCard } from './knowledgeSwimlane.logic'

function card(id: string, list: string[] = [], extra: Partial<SwimlaneCard> = {}): SwimlaneCard {
  return { id, tags: [], list, modified: '', body: '', ...extra }
}

describe('laneKey', () => {
  it('distinguishes project lanes from focused-card lanes', () => {
    expect(laneKey({ projectId: 'p1', projectName: 'Alpha' })).not.toBe(laneKey({ projectId: 'p1', projectName: 'Alpha', cardId: 'a' }))
  })

  it('is empty for a null lane', () => {
    expect(laneKey(null)).toBe('')
    expect(laneKey(undefined)).toBe('')
  })
})

describe('isLaneChild', () => {
  it('rejects virtual mount buckets, standalone and component/runtime cards', () => {
    expect(isLaneChild(undefined)).toBe(false)
    expect(isLaneChild(card('__builtin_task__'))).toBe(false)
    expect(isLaneChild(card('a', [], { standalone: true }))).toBe(false)
    expect(isLaneChild(card('a', [], { visibility: 'component' }))).toBe(false)
    expect(isLaneChild(card('a', [], { visibility: 'runtime' }))).toBe(false)
    expect(isLaneChild(card('a'))).toBe(true)
  })
})

describe('resolveLaneCards', () => {
  it('resolves the toc subtree as the display stack; toc itself is never a stack card', () => {
    const cards = [
      card(KB_PROJECT_ROOT_CARD_ID, ['a']),
      card('a', ['a1']),
      card('a1'),
      card('b'),
    ]
    const lane = resolveLaneCards(cards)
    expect(lane?.root.id).toBe(KB_PROJECT_ROOT_CARD_ID)
    expect(lane?.cards.map(c => c.id)).toEqual(['a', 'a1', 'b'])
  })

  it('annotates each stack card with its tree depth', () => {
    const cards = [
      card(KB_PROJECT_ROOT_CARD_ID, ['a']),
      card('a', ['a1']),
      card('a1', ['a2']),
      card('a2'),
    ]
    const byDepth: Record<string, number> = {}
    for (const c of resolveLaneCards(cards)?.cards ?? []) byDepth[c.id] = c.depth
    expect(byDepth).toEqual({ a: 0, a1: 1, a2: 2 })
  })

  it('mirrors backend mounting: parent-attached cards and parentless wiki defaults under toc', () => {
    // toc.list is empty; a is attached via parent, project_info via the
    // well-known rule, fresh is a parentless wiki card → default-mounted.
    const cards = [
      card(KB_PROJECT_ROOT_CARD_ID),
      card('a', [], { parent: KB_PROJECT_ROOT_CARD_ID }),
      card('project_info', [], { modified: '2026-01-03T00:00:00Z' }),
      card('fresh', [], { modified: '2026-01-02T00:00:00Z' }),
    ]
    const lane = resolveLaneCards(cards)
    expect(lane?.cards.map(c => c.id)).toEqual(['project_info', 'fresh', 'a'])
  })

  it('does not default-mount cards that are listed by another card or have a parent', () => {
    const cards = [
      card(KB_PROJECT_ROOT_CARD_ID, ['listed']),
      card('listed', [], { type: 'wiki' }),
      card('task-card', [], { type: 'task' }),
      card('standalone', [], { type: 'wiki', standalone: true }),
      card('runtime', [], { type: 'wiki', visibility: 'runtime' }),
      card('child', [], { type: 'wiki', parent: 'listed' }),
    ]
    const lane = resolveLaneCards(cards)
    // 'child' keeps its explicit parent edge: it nests under 'listed', not toc.
    expect(lane?.cards.map(c => c.id)).toEqual(['listed', 'child'])
  })

  it('sorts siblings by modified desc at every level', () => {
    const cards = [
      card(KB_PROJECT_ROOT_CARD_ID),
      card('old', [], { parent: KB_PROJECT_ROOT_CARD_ID, modified: '2026-01-01T00:00:00Z' }),
      card('new', [], { parent: KB_PROJECT_ROOT_CARD_ID, modified: '2026-02-01T00:00:00Z' }),
      card('old-kid', [], { parent: 'old', modified: '2026-01-01T00:00:00Z' }),
      card('new-kid', [], { parent: 'old', modified: '2026-03-01T00:00:00Z' }),
    ]
    const lane = resolveLaneCards(cards)
    expect(lane?.cards.map(c => c.id)).toEqual(['new', 'old', 'new-kid', 'old-kid'])
  })

  it('returns null when the toc container is missing', () => {
    expect(resolveLaneCards([card('a')])).toBeNull()
  })

  it('filters non-renderable children and ignores ghost list entries', () => {
    const cards = [
      card(KB_PROJECT_ROOT_CARD_ID, ['a', 'a', 'ghost', '__builtin_todo__', 'comp', 'solo']),
      card('a'),
      card('comp', [], { visibility: 'component' }),
      card('solo', [], { standalone: true }),
    ]
    const lane = resolveLaneCards(cards)
    expect(lane?.cards.map(c => c.id)).toEqual(['a'])
  })

  it('guards against cycles', () => {
    const cards = [
      card(KB_PROJECT_ROOT_CARD_ID, ['a']),
      card('a', [KB_PROJECT_ROOT_CARD_ID, 'b']),
      card('b', ['a']),
    ]
    const lane = resolveLaneCards(cards)
    expect(lane?.cards.map(c => c.id)).toEqual(['a', 'b'])
  })
})

describe('laneCollapsedIds', () => {
  const stack: LaneStackCard[] = [
    { ...card('a'), depth: 0 },
    { ...card('b'), depth: 0 },
  ]

  it('collapses every card by default', () => {
    const collapsed = laneCollapsedIds(stack)
    expect([...collapsed].sort()).toEqual(['a', 'b'])
  })

  it('opens the focused card (accordion: only it stays expanded)', () => {
    const collapsed = laneCollapsedIds(stack, 'a')
    expect(collapsed.has('a')).toBe(false)
    expect(collapsed.has('b')).toBe(true)
  })

  it('ignores a focus id that is not in the lane', () => {
    expect([...laneCollapsedIds(stack, 'ghost')].sort()).toEqual(['a', 'b'])
  })
})

describe('SwimlaneCard type re-export sanity', () => {
  it('LaneStackCard extends SwimlaneCard', () => {
    const c: LaneStackCard = { ...card('a'), depth: 0 }
    const s: SwimlaneCard = c
    expect(s.id).toBe('a')
  })
})
