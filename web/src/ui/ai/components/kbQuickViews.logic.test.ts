import { describe, it, expect } from 'vitest'
import {
  KB_QUICK_VIEWS,
  cardRefKey,
  dedupeCardRefs,
  filterCardRefs,
  interleaveRecentRefs,
  isKbQuickView,
  makeCardRef,
  sortProjectsByRecent,
  type KbCardRef,
  type KbProjectRef,
} from './kbQuickViews.logic'

const ref = (projectId: string, cardId: string, projectName = projectId): KbCardRef =>
  makeCardRef(projectId, projectName, cardId)

describe('KbQuickView protocol', () => {
  it('lists the three views in order', () => {
    expect([...KB_QUICK_VIEWS]).toEqual(['home', 'search', 'starred'])
  })

  it('narrows known ids and rejects everything else', () => {
    expect(isKbQuickView('home')).toBe(true)
    expect(isKbQuickView('starred')).toBe(true)
    expect(isKbQuickView('swimlane')).toBe(false)
    expect(isKbQuickView(undefined)).toBe(false)
  })
})

describe('cardRefKey', () => {
  it('is unique per project + card', () => {
    expect(cardRefKey(ref('p1', 'a'))).not.toBe(cardRefKey(ref('p2', 'a')))
    expect(cardRefKey(ref('p1', 'a'))).not.toBe(cardRefKey(ref('p1', 'b')))
    expect(cardRefKey(ref('p1', 'a'))).toBe(cardRefKey(ref('p1', 'a')))
  })
})

describe('dedupeCardRefs', () => {
  it('keeps the first occurrence of each project+card', () => {
    const out = dedupeCardRefs([ref('p1', 'a'), ref('p2', 'a'), ref('p1', 'a'), ref('p1', 'b')])
    expect(out.map(r => `${r.projectId}/${r.cardId}`)).toEqual(['p1/a', 'p2/a', 'p1/b'])
    expect(out[0]).toEqual(ref('p1', 'a'))
  })
})

describe('filterCardRefs', () => {
  const refs = [ref('proj-alpha', 'design', 'Alpha'), ref('proj-beta', 'notes', 'Beta')]

  it('returns every ref for an empty query', () => {
    expect(filterCardRefs(refs, '   ')).toHaveLength(2)
  })

  it('matches the card id case-insensitively', () => {
    expect(filterCardRefs(refs, 'DESIGN').map(r => r.cardId)).toEqual(['design'])
  })

  it('matches the owning project name', () => {
    expect(filterCardRefs(refs, 'beta').map(r => r.cardId)).toEqual(['notes'])
  })

  it('returns nothing when no field matches', () => {
    expect(filterCardRefs(refs, 'zzz')).toEqual([])
  })
})

describe('sortProjectsByRecent', () => {
  const projects: KbProjectRef[] = [
    { projectId: 'b', projectName: 'Beta', lastOpenedAt: '2026-01-01T00:00:00Z' },
    { projectId: 'a', projectName: 'Alpha', lastOpenedAt: '2026-03-01T00:00:00Z' },
    { projectId: 'c', projectName: 'Gamma' },
    { projectId: 'd', projectName: 'Delta', lastOpenedAt: '2026-02-01T00:00:00Z' },
  ]

  it('orders most-recently-opened first and undated last', () => {
    expect(sortProjectsByRecent(projects).map(p => p.projectId)).toEqual(['a', 'd', 'b', 'c'])
  })

  it('does not mutate the input array', () => {
    const input = [...projects]
    sortProjectsByRecent(input)
    expect(input.map(p => p.projectId)).toEqual(['b', 'a', 'c', 'd'])
  })
})

describe('interleaveRecentRefs', () => {
  it('round-robins groups so projects mix', () => {
    const out = interleaveRecentRefs(
      [
        [ref('p1', 'a1'), ref('p1', 'a2')],
        [ref('p2', 'b1'), ref('p2', 'b2')],
      ],
      4,
    )
    expect(out.map(r => r.cardId)).toEqual(['a1', 'b1', 'a2', 'b2'])
  })

  it('caps at max', () => {
    const out = interleaveRecentRefs([[ref('p1', 'a1'), ref('p1', 'a2')], [ref('p2', 'b1')]], 2)
    expect(out).toHaveLength(2)
    expect(out.map(r => r.cardId)).toEqual(['a1', 'b1'])
  })

  it('drops duplicates across groups', () => {
    const out = interleaveRecentRefs(
      [
        [ref('p1', 'a1')],
        [ref('p1', 'a1'), ref('p2', 'b1')],
      ],
      5,
    )
    expect(out.map(r => r.cardId)).toEqual(['a1', 'b1'])
  })

  it('returns empty for a non-positive cap or no groups', () => {
    expect(interleaveRecentRefs([[ref('p1', 'a1')]], 0)).toEqual([])
    expect(interleaveRecentRefs([], 5)).toEqual([])
  })
})
