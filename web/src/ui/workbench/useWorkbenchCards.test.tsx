import { describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import type { WorkbenchSnapshot } from '../../gen-clients/system/types'
import { normalizeSnapshot } from './useWorkbenchCards'

const snapshotWithOmittedCards = { Generation: 3 } as unknown as WorkbenchSnapshot

vi.mock('../../gen-clients/workbench/projection-client', () => ({
  getSnapshot: vi.fn(async () => snapshotWithOmittedCards),
  watchSnapshot: vi.fn(() => ({
    async *[Symbol.asyncIterator]() {
      // never yields; the initial fetch alone drives this test
    },
  })),
}))

vi.mock('../../gen-clients/workbench/client', () => ({
  promote: vi.fn(),
  setPinned: vi.fn(),
  setHidden: vi.fn(),
  upsertCard: vi.fn(),
}))

const { useWorkbenchCards } = await import('./useWorkbenchCards')

describe('useWorkbenchCards snapshot normalization', () => {
  it('tolerates a snapshot whose Cards field is omitted (empty board, omitempty)', async () => {
    const { result } = renderHook(() => useWorkbenchCards(true))

    await waitFor(() => expect(result.current.ready).toBe(true))

    expect(Array.isArray(result.current.cards)).toBe(true)
    expect(result.current.cards).toEqual([])
    expect(result.current.layout).toBeDefined()
    expect(result.current.generation).toBe(3)
  })

  it('pascalizes the lowerFirst projection wire shape (gospore snapshotStruct contract)', () => {
    const wire = {
      cards: [
        {
          id: 'agent:a1',
          kind: 'chat',
          title: 'Agent',
          score: 40,
          slot: 'main',
          pinned: false,
          visual: { icon: 'sparkles', accent: 'flame' },
        },
      ],
      generation: 110310,
      frozen: false,
      maximized: '',
    }

    const snap = normalizeSnapshot(wire as unknown as WorkbenchSnapshot)

    expect(snap.Cards).toHaveLength(1)
    expect(snap.Cards[0]!.Id).toBe('agent:a1')
    expect(snap.Cards[0]!.Score).toBe(40)
    expect(snap.Cards[0]!.Visual?.Icon).toBe('sparkles')
    expect(snap.Generation).toBe(110310)
  })

  it('keeps PascalCase (schema-encoded callable replies) intact', () => {
    const snap = normalizeSnapshot({
      Cards: [{ Id: 'app:x', Kind: 'app', Title: 'X', Icon: '', Score: 5, Slot: 'side', Pinned: true }],
      Generation: 7,
      Frozen: false,
      Maximized: '',
    })

    expect(snap.Cards[0]!.Id).toBe('app:x')
    expect(snap.Cards[0]!.Score).toBe(5)
    expect(snap.Generation).toBe(7)
  })

  it('drops card states without a usable Id instead of crashing id-driven consumers', () => {
    const wire = {
      Cards: [
        { Kind: 'chat', Score: 40, Slot: 'main' }, // no Id — the `iD` wire regression shape
        { Id: 'app:x', Kind: 'app', Title: 'X', Icon: '', Score: 5, Slot: 'side', Pinned: false },
      ],
      Generation: 9,
    }

    const snap = normalizeSnapshot(wire as unknown as WorkbenchSnapshot)

    expect(snap.Cards).toHaveLength(1)
    expect(snap.Cards[0]!.Id).toBe('app:x')
  })

  it('normalizes an empty/undefined snapshot without crashing', () => {
    expect(normalizeSnapshot(undefined)).toEqual({
      Cards: [],
      Generation: 0,
      Frozen: false,
      Maximized: '',
    })
  })
})
