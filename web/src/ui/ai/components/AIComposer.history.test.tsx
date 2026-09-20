import { describe, it, expect } from 'vitest'
import { sortHistory, limitHistory, MAX_HISTORY_SIZE, addHistoryEntry, toggleHistoryFavorite, removeHistoryItem } from './AIComposer'
import type { ComposerHistoryItem } from './AIComposer'

function makeItem(overrides: Partial<ComposerHistoryItem>): ComposerHistoryItem {
  return {
    id: 'id',
    text: 'text',
    timestamp: 0,
    isFavorite: false,
    ...overrides,
  }
}

describe('AIComposer history helpers', () => {
  it('sortHistory puts favorites first, then newest first', () => {
    const items = [
      makeItem({ id: '1', timestamp: 100, isFavorite: false }),
      makeItem({ id: '2', timestamp: 50, isFavorite: true }),
      makeItem({ id: '3', timestamp: 200, isFavorite: false }),
      makeItem({ id: '4', timestamp: 10, isFavorite: true }),
    ]
    const sorted = sortHistory(items)
    expect(sorted.map(i => i.id)).toEqual(['2', '4', '3', '1'])
  })

  it('limitHistory keeps only the top MAX_HISTORY_SIZE after sorting', () => {
    const items = Array.from({ length: MAX_HISTORY_SIZE + 10 }, (_, i) =>
      makeItem({ id: String(i), timestamp: i, isFavorite: false })
    )
    const limited = limitHistory(items)
    expect(limited.length).toBe(MAX_HISTORY_SIZE)
    expect(limited[0]?.id).toBe(String(MAX_HISTORY_SIZE + 9))
  })

  it('limitHistory preserves favorites over older non-favorites', () => {
    const items = [
      makeItem({ id: 'old', timestamp: 1, isFavorite: false }),
      ...Array.from({ length: MAX_HISTORY_SIZE }, (_, i) =>
        makeItem({ id: String(i), timestamp: i + 10, isFavorite: false })
      ),
      makeItem({ id: 'fav', timestamp: 0, isFavorite: true }),
    ]
    const limited = limitHistory(items)
    expect(limited.some(i => i.id === 'fav')).toBe(true)
    expect(limited.some(i => i.id === 'old')).toBe(false)
  })

  it('addHistoryEntry appends a new item and sorts it to the front', () => {
    const items = [makeItem({ id: 'a', text: 'old prompt', timestamp: 100 })]
    const next = addHistoryEntry(items, 'new prompt')
    expect(next.map(i => i.text)).toEqual(['new prompt', 'old prompt'])
    expect(next[0]?.isFavorite).toBe(false)
    expect(next[0]?.id).toBeTruthy()
  })

  it('addHistoryEntry dedups by text and refreshes the timestamp', () => {
    const items = [
      makeItem({ id: '1', text: 'hello', timestamp: 100 }),
      makeItem({ id: '2', text: 'other', timestamp: 200 }),
    ]
    const next = addHistoryEntry(items, 'hello')
    expect(next).toHaveLength(2)
    const hello = next.find(i => i.text === 'hello')
    expect(hello?.id).toBe('1')
    expect(hello?.timestamp).toBeGreaterThan(200)
  })

  it('toggleHistoryFavorite flips the flag and hoists the item', () => {
    const items = [
      makeItem({ id: '1', timestamp: 300 }),
      makeItem({ id: '2', timestamp: 200 }),
    ]
    const next = toggleHistoryFavorite(items, '2')
    expect(next[0]?.id).toBe('2')
    expect(next[0]?.isFavorite).toBe(true)
    expect(next[1]?.isFavorite).toBe(false)
    expect(toggleHistoryFavorite(next, '2')[0]?.isFavorite).toBe(false)
  })

  it('removeHistoryItem drops only the target item', () => {
    const items = [
      makeItem({ id: '1', timestamp: 300 }),
      makeItem({ id: '2', timestamp: 200 }),
    ]
    const next = removeHistoryItem(items, '1')
    expect(next.map(i => i.id)).toEqual(['2'])
  })
})
