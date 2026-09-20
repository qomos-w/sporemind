import { describe, it, expect } from 'vitest'
import {
  defaultCollapsedIds,
  isCardCollapsed,
  normalizeAccordion,
  setCardCollapsed,
  summarizeBody,
  swimlaneCardIds,
  toggleCollapsed,
  type SwimlaneCard,
} from './knowledgeSwimlane.logic'

const card = (id: string, extra: Partial<SwimlaneCard> = {}): SwimlaneCard => ({
  id,
  tags: [],
  list: [],
  modified: '',
  ...extra,
})

describe('swimlaneCardIds', () => {
  it('lists the stack cards in order', () => {
    expect(swimlaneCardIds([card('a'), card('b'), card('c')])).toEqual(['a', 'b', 'c'])
  })

  it('drops duplicate ids', () => {
    expect(swimlaneCardIds([card('a'), card('a'), card('b')])).toEqual(['a', 'b'])
  })
})

describe('defaultCollapsedIds', () => {
  it('collapses every card of the stack', () => {
    const collapsed = defaultCollapsedIds([card('a'), card('b')])
    expect([...collapsed].sort()).toEqual(['a', 'b'])
  })

  it('is empty for an empty stack', () => {
    expect(defaultCollapsedIds([]).size).toBe(0)
  })
})

describe('normalizeAccordion', () => {
  const ids = ['a', 'b', 'c']

  it('keeps at most one expanded card, first in lane order wins', () => {
    // a collapsed, b and c expanded -> c gets collapsed, b stays expanded
    const next = normalizeAccordion(new Set(['a']), ids)
    expect([...next].sort()).toEqual(['a', 'c'])
    expect(isCardCollapsed(next, 'b')).toBe(false)
  })

  it('preserves an all-collapsed set', () => {
    const next = normalizeAccordion(new Set(ids), ids)
    expect([...next].sort()).toEqual([...ids].sort())
  })

  it('drops ids that are no longer in the lane', () => {
    const next = normalizeAccordion(new Set(['a', 'stale']), ids)
    expect(next.has('stale')).toBe(false)
  })
})

describe('setCardCollapsed / toggleCollapsed', () => {
  const ids = ['a', 'b', 'c']

  it('expanding a card collapses every other card (accordion)', () => {
    const next = setCardCollapsed(new Set(ids), ids, 'b', false)
    expect(isCardCollapsed(next, 'b')).toBe(false)
    expect(isCardCollapsed(next, 'a')).toBe(true)
    expect(isCardCollapsed(next, 'c')).toBe(true)
  })

  it('collapsing a card leaves the others untouched', () => {
    const next = setCardCollapsed(new Set(['a', 'c']), ids, 'b', true)
    expect([...next].sort()).toEqual(['a', 'b', 'c'])
  })

  it('ignores unknown ids', () => {
    const before = new Set(['a'])
    expect([...setCardCollapsed(before, ids, 'nope', true)]).toEqual(['a'])
  })

  it('toggle flips the card within accordion semantics', () => {
    const collapsed = new Set(ids)
    const expanded = toggleCollapsed(collapsed, ids, 'b')
    expect(isCardCollapsed(expanded, 'b')).toBe(false)
    expect(isCardCollapsed(expanded, 'a')).toBe(true)
    const collapsedAgain = toggleCollapsed(expanded, ids, 'b')
    expect(isCardCollapsed(collapsedAgain, 'b')).toBe(true)
  })
})

describe('summarizeBody', () => {
  it('returns the first meaningful line', () => {
    expect(summarizeBody('\n\nFirst line here\nSecond line')).toBe('First line here')
  })

  it('strips heading, list and quote markers', () => {
    expect(summarizeBody('## Heading text')).toBe('Heading text')
    expect(summarizeBody('- bullet item')).toBe('bullet item')
    expect(summarizeBody('> quoted')).toBe('quoted')
    expect(summarizeBody('1. numbered')).toBe('numbered')
  })

  it('unwraps link syntax and emphasis and skips blank bodies', () => {
    expect(summarizeBody('[label](https://x.com) and **bold**')).toBe('label and bold')
    expect(summarizeBody('')).toBe('')
    expect(summarizeBody(undefined)).toBe('')
    expect(summarizeBody('   \n\n  ')).toBe('')
  })
})
