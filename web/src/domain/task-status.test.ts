import { describe, expect, it } from 'vitest'
import type { MonoCardListItem } from './mono-types'
import {
  CANONICAL_TASK_STATUSES,
  DEFAULT_STATUS_ALIASES,
  STATUS_MAP_CARD_TAG,
  findStatusMapCard,
  normalizeTaskStatus,
  parseStatusAliases,
  resolveStatusAliases,
  buildStatusMapCardData,
} from './task-status'

function card(overrides: Partial<MonoCardListItem> = {}): MonoCardListItem {
  return {
    id: 'C',
    tags: [],
    list: [],
    modified: '',
    ...overrides,
  }
}

describe('normalizeTaskStatus', () => {
  it('keeps canonical statuses unchanged', () => {
    for (const s of CANONICAL_TASK_STATUSES) {
      expect(normalizeTaskStatus(s)).toBe(s)
    }
  })

  it('maps default aliases to canonical', () => {
    expect(normalizeTaskStatus('in_progress')).toBe('doing')
    expect(normalizeTaskStatus('open')).toBe('todo')
    expect(normalizeTaskStatus('approved')).toBe('todo')
    expect(normalizeTaskStatus('completed')).toBe('done')
    expect(normalizeTaskStatus('rejected')).toBe('cancelled')
    expect(normalizeTaskStatus('canceled')).toBe('cancelled')
  })

  it('falls back to backlog for unknown statuses', () => {
    expect(normalizeTaskStatus('whatever')).toBe('backlog')
  })
})

describe('parseStatusAliases', () => {
  it('parses array aliases', () => {
    expect(parseStatusAliases({ aliases: ['a:b', ' c : d '] })).toEqual({ a: 'b', c: 'd' })
  })

  it('parses multiline string aliases', () => {
    expect(parseStatusAliases({ aliases: 'a:b\nc:d' })).toEqual({ a: 'b', c: 'd' })
  })

  it('ignores malformed entries', () => {
    expect(parseStatusAliases({ aliases: ['no-colon', 'ok:yes'] })).toEqual({ ok: 'yes' })
  })
})

describe('resolveStatusAliases', () => {
  it('merges map card aliases over defaults', () => {
    const cards: MonoCardListItem[] = [
      card({ tags: [STATUS_MAP_CARD_TAG], data: { aliases: ['in_progress:blocked'] } }),
    ]
    const map = resolveStatusAliases(cards)
    expect(map.in_progress).toBe('blocked')
    expect(map.open).toBe('todo')
  })

  it('uses defaults when no map card exists', () => {
    expect(resolveStatusAliases([])).toEqual(DEFAULT_STATUS_ALIASES)
  })
})

describe('findStatusMapCard', () => {
  it('finds the card by tag', () => {
    const c = card({ tags: [STATUS_MAP_CARD_TAG] })
    expect(findStatusMapCard([card(), c])).toBe(c)
  })

  it('finds the card by title', () => {
    const c = card({ id: 'Task status map' })
    expect(findStatusMapCard([card(), c])).toBe(c)
  })
})

describe('buildStatusMapCardData', () => {
  it('serializes aliases as colon-separated entries', () => {
    expect(buildStatusMapCardData({ a: 'b', c: 'd' })).toEqual({ aliases: ['a:b', 'c:d'] })
  })
})
