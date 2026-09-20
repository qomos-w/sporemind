import { describe, it, expect } from 'vitest'
import {
  BOARD_CAPACITY,
  LAYOUT_HYSTERESIS,
  MAIN_SLOT,
  MAIN_SLOT_NO_TERM,
  MAX_SLOT,
  MAX_SLOT_WITH_TERM,
  RAIL_SLOT_CAPACITY,
  SIDE_SLOT_CAPACITY,
  TERMINAL_SLOT,
  computeLayout,
  railSlot,
  sideSlots,
  stableOrder,
  type LayoutCard,
} from './cardLayout'

const card = (id: string, score: number, extra: Partial<LayoutCard> = {}): LayoutCard => ({
  id,
  score,
  pinned: false,
  ...extra,
})

describe('sideSlots', () => {
  it('returns nothing for an empty set', () => {
    expect(sideSlots(0, 61)).toEqual([])
  })

  it('fills a two-column grid starting at the left edge', () => {
    const one = sideSlots(1, 61)
    expect(one).toHaveLength(1)
    expect(one[0]).toMatchObject({ x: 3, y: 3, w: 20, h: 61 })

    const two = sideSlots(2, 61)
    expect(two.map(s => s.x)).toEqual([3, 77])

    const six = sideSlots(6, 61)
    expect(six).toHaveLength(6)
    // 3 rows of 2 → row height (61 - 2*3) / 3.
    const rowHeight = (61 - 6) / 3
    expect(six[0]!.h).toBeCloseTo(rowHeight)
    expect(six[2]!.y).toBeCloseTo(3 + rowHeight + 3)
    expect(six[4]!.x).toBe(3)
    expect(six[5]!.x).toBe(77)
  })

  it('caps the grid at the canonical layouts: no extra row is ever added', () => {
    const nine = sideSlots(9, 61)
    expect(nine).toHaveLength(SIDE_SLOT_CAPACITY)
    // Same geometry as the full 3-row layout — a 9th card changes nothing.
    expect(nine).toEqual(sideSlots(6, 61))
  })
})

describe('railSlot', () => {
  it('splits the rail span across the cards and never shrinks below the floor', () => {
    const [a, b] = [railSlot(0, 2, 0, 100), railSlot(1, 2, 0, 100)]
    expect(a).toMatchObject({ x: 84, w: 16 })
    expect(b.y).toBeCloseTo(50)

    const many = Array.from({ length: 40 }, (_, i) => railSlot(i, 40, 0, 100))
    expect(many.every(s => s.h >= 8)).toBe(true)
  })
})

describe('stableOrder', () => {
  it('keeps the previous order when no challenger leads by the hysteresis threshold', () => {
    const cards = [card('a', 20), card('b', 30)]
    // b leads a by 10 (< 15) → previous order [a, b] is preserved.
    expect(stableOrder(cards, ['a', 'b'])).toEqual(['a', 'b'])
  })

  it('swaps adjacent cards once the challenger leads by more than the threshold', () => {
    const cards = [card('a', 20), card('b', 20 + LAYOUT_HYSTERESIS + 1)]
    expect(stableOrder(cards, ['a', 'b'])).toEqual(['b', 'a'])
  })

  it('starts from insertion order and runs a single bubble pass without a previous order', () => {
    const cards = [card('a', 5), card('b', 30), card('c', 12)]
    // Reference semantics: without a carried order the list keeps the incoming
    // order and only adjacent pairs beyond the threshold swap (one pass).
    expect(stableOrder(cards, null)).toEqual(['b', 'a', 'c'])
  })

  it('pulls a pinned card to the front regardless of score', () => {
    const cards = [card('a', 50), card('b', 1, { pinned: true })]
    expect(stableOrder(cards, ['a', 'b'])).toEqual(['b', 'a'])
  })
})

describe('computeLayout', () => {
  it('places the stable-order leader in the main slot and the rest in the side grid', () => {
    const result = computeLayout([card('a', 30), card('b', 20), card('c', 10)])
    expect(result.maximizedId).toBeNull()
    expect(result.visibleTerminal).toBe(false)
    expect(result.order).toEqual(['a', 'b', 'c'])
    expect(result.placements.a).toMatchObject({ slot: MAIN_SLOT_NO_TERM, zone: 'main', rank: 0 })
    expect(result.placements.b).toMatchObject({ zone: 'side', rank: 1, slot: { x: 3, w: 20 } })
    expect(result.placements.c).toMatchObject({ zone: 'side', rank: 2, slot: { x: 77, w: 20 } })
  })

  it('reserves the terminal strip and shrinks the main slot when the terminal is up', () => {
    const result = computeLayout([
      card('a', 30),
      card('b', 20),
      card('term', 34, { terminal: true }),
    ])
    expect(result.visibleTerminal).toBe(true)
    expect(result.placements.a).toMatchObject({ slot: MAIN_SLOT, zone: 'main' })
    expect(result.placements.term).toMatchObject({ slot: TERMINAL_SLOT, zone: 'term' })
    // The terminal never appears in the stable order / side grid.
    expect(result.order).toEqual(['a', 'b'])
  })

  it('drops a hidden terminal from the board', () => {
    const result = computeLayout([card('a', 30), card('term', 34, { terminal: true, hidden: true })])
    expect(result.visibleTerminal).toBe(false)
    expect(result.placements.term).toBeUndefined()
    expect(result.placements.a).toMatchObject({ slot: MAIN_SLOT_NO_TERM })
  })

  it('captures a card to the full board and rails the rest while the terminal is retreated', () => {
    const result = computeLayout([card('a', 30), card('b', 20)], { maximizedId: 'b' })
    expect(result.maximizedId).toBe('b')
    expect(result.placements.b).toMatchObject({ slot: MAX_SLOT, zone: 'max', rank: 0 })
    expect(result.placements.a).toMatchObject({ zone: 'rail', rank: 1 })
  })

  it('compresses a capture to the upper band while the terminal is up', () => {
    const result = computeLayout(
      [card('a', 30), card('b', 20), card('term', 34, { terminal: true })],
      { maximizedId: 'b' },
    )
    expect(result.placements.b).toMatchObject({ slot: MAX_SLOT_WITH_TERM, zone: 'max' })
    expect(result.placements.a).toMatchObject({ zone: 'rail' })
    expect(result.placements.term).toMatchObject({ slot: TERMINAL_SLOT, zone: 'term' })
    expect(result.order).toEqual([])
  })

  it('ignores a capture request for an unknown or hidden card', () => {
    const result = computeLayout([card('a', 30)], { maximizedId: 'ghost' })
    expect(result.maximizedId).toBeNull()
    expect(result.placements.a).toMatchObject({ zone: 'main' })
  })

  it('places only the canonical layout: overflow cards stay off the board', () => {
    const many = Array.from({ length: BOARD_CAPACITY + 5 }, (_, i) => card(`c${i}`, 100 - i))
    const result = computeLayout(many)
    // main + side rows cap the board; the extra cards are never appended.
    expect(Object.keys(result.placements)).toHaveLength(BOARD_CAPACITY)
    expect(result.placements.c0).toMatchObject({ zone: 'main' })
    expect(result.placements[`c${BOARD_CAPACITY}`]).toBeUndefined()
    expect(result.placements[`c${BOARD_CAPACITY - 1}`]).toMatchObject({ zone: 'side' })
    // The full order is still reported (attention truth), it just is not placed.
    expect(result.order).toHaveLength(BOARD_CAPACITY + 5)
  })

  it('caps the capture rail at the canonical count', () => {
    const many = Array.from({ length: RAIL_SLOT_CAPACITY + 4 }, (_, i) => card(`c${i}`, 100 - i))
    const result = computeLayout(many, { maximizedId: 'c0' })
    const rails = Object.values(result.placements).filter(p => p.zone === 'rail')
    expect(rails).toHaveLength(RAIL_SLOT_CAPACITY)
    expect(result.placements[`c${RAIL_SLOT_CAPACITY + 1}`]).toBeUndefined()
    expect(SIDE_SLOT_CAPACITY).toBe(6)
  })
})
