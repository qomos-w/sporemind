import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import type { MonoCardListItem } from '../../../domain/mono-types'
import type { MonoStoreState } from '../../panels/mono-store'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// ── Pure helpers (imported statically; vi.mock below is hoisted above it) ──
import { workflowTopoDeps, applyWorkflowTopoDeps, useWorkflowTopoDeps } from './workflowTopoGraphs'

// ── mono-store mock backing the hook ───────────────────────────────

const mocks = vi.hoisted(() => {
  const listeners = new Set<(s: unknown) => void>()
  const getGraph = vi.fn()
  const stateRef: { current: unknown } = { current: null }
  return { listeners, getGraph, stateRef }
})

vi.mock('../../panels/mono-store', () => ({
  monoStore: {
    getState: () => mocks.stateRef.current,
    subscribe: (listener: (s: unknown) => void) => {
      mocks.listeners.add(listener)
      listener(mocks.stateRef.current)
      return () => { mocks.listeners.delete(listener) }
    },
    getGraph: (...args: unknown[]) => mocks.getGraph(...args),
  },
  graphCacheKey: (kind: string, id: string) => `${kind}/${id}`,
}))

// ── workflowTopoDeps ───────────────────────────────────────────────

describe('workflowTopoDeps', () => {
  it('maps depends_on edges (from depends on to) onto per-task lists', () => {
    const env = JSON.stringify({
      nodes: [{ id: 't1' }, { id: 't2' }],
      edges: [
        { from: 't2', to: 't1', kind: 'depends_on' },
        { from: 't2', to: 't0', kind: 'depends_on' },
      ],
    })
    expect(workflowTopoDeps(env)).toEqual(new Map([
      ['t1', []],
      ['t2', ['t1', 't0']],
    ]))
  })

  it('gives every node an authoritative entry, even with zero edges', () => {
    const env = JSON.stringify({ nodes: [{ id: 't1' }, { id: 't2' }], edges: [] })
    expect(workflowTopoDeps(env)).toEqual(new Map([['t1', []], ['t2', []]]))
  })

  it('ignores non-depends_on edge kinds and self loops, and dedups', () => {
    const env = JSON.stringify({
      nodes: [{ id: 't1' }],
      edges: [
        { from: 't1', to: 't2', kind: 'data_flow' },
        { from: 't1', to: 't1', kind: 'depends_on' },
        { from: 't1', to: 't2' },
        { from: 't1', to: 't2' },
      ],
    })
    expect(workflowTopoDeps(env)).toEqual(new Map([['t1', ['t2']]]))
  })

  it('degrades to an empty map on missing or malformed envelopes', () => {
    expect(workflowTopoDeps(undefined)).toEqual(new Map())
    expect(workflowTopoDeps('')).toEqual(new Map())
    expect(workflowTopoDeps('not json')).toEqual(new Map())
    expect(workflowTopoDeps('{"edges":"nope"}')).toEqual(new Map())
  })
})

// ── applyWorkflowTopoDeps ──────────────────────────────────────────

describe('applyWorkflowTopoDeps', () => {
  const card = (id: string, data?: Record<string, unknown>): MonoCardListItem => ({
    id, type: 'task', tags: [], list: [], modified: 'now', data,
  })

  it('overrides depends_on only for tasks present in the graph map', () => {
    const cards = [card('t1', { depends_on: ['stale'] }), card('t2', { depends_on: ['keep'] })]
    const merged = applyWorkflowTopoDeps(cards, new Map([['t1', ['fresh-dep']]]))
    expect(merged[0]?.data?.depends_on).toEqual(['fresh-dep'])
    expect(merged[1]?.data?.depends_on).toEqual(['keep'])
  })

  it('clears depends_on for a graph node whose authoritative list is empty', () => {
    const cards = [card('t1', { depends_on: ['stale'] })]
    const merged = applyWorkflowTopoDeps(cards, new Map([['t1', []]]))
    expect(merged[0]?.data?.depends_on).toEqual([])
  })

  it('returns the input array untouched for an empty deps map', () => {
    const cards = [card('t1')]
    expect(applyWorkflowTopoDeps(cards, new Map())).toBe(cards)
  })
})

// ── Hook: refetch on graph_changed-driven refresh key bumps ────────

describe('useWorkflowTopoDeps', () => {
  function HookSpy({ cards, expandedMapIds, onDeps }: { cards: MonoCardListItem[]; expandedMapIds: ReadonlySet<string>; onDeps: (d: Map<string, string[]>) => void }) {
    onDeps(useWorkflowTopoDeps(cards, expandedMapIds))
    return null
  }

  const envelope = (graph: unknown) => ({ Meta: {}, EnvelopeText: JSON.stringify(graph) })
  const mapCard = (id: string): MonoCardListItem => ({ id, type: 'workflow', tags: [], list: [], modified: 'now' })

  let container: HTMLDivElement
  let root: Root

  const freshState = (): MonoStoreState => ({
    projectId: 'p1', cards: [], openCards: [], loading: false, error: null,
    cardRefreshKey: {}, graphRefreshKey: {},
  })

  beforeEach(() => {
    mocks.listeners.clear()
    mocks.getGraph.mockReset()
    mocks.stateRef.current = freshState()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('loads the workflow_topo graph of each visible map and merges its deps', async () => {
    mocks.getGraph.mockResolvedValueOnce(envelope({
      nodes: [{ id: 'a' }],
      edges: [{ from: 'a', to: 'b', kind: 'depends_on' }],
    }))
    const seen: Map<string, string[]>[] = []
    await act(async () => {
      root.render(<HookSpy cards={[mapCard('map-1')]} expandedMapIds={new Set(['map-1'])} onDeps={d => seen.push(d)} />)
    })
    expect(mocks.getGraph).toHaveBeenCalledWith('workflow_topo', 'map-1')
    expect(seen.at(-1)).toEqual(new Map([['a', ['b']]]))
  })

  it('re-pulls after a graph_changed refresh key bump (remote multi-client save)', async () => {
    mocks.getGraph.mockResolvedValue(envelope({ nodes: [{ id: 'a' }], edges: [{ from: 'a', to: 'b', kind: 'depends_on' }] }))
    const seen: Map<string, string[]>[] = []
    await act(async () => {
      root.render(<HookSpy cards={[mapCard('map-1')]} expandedMapIds={new Set(['map-1'])} onDeps={d => seen.push(d)} />)
    })
    expect(mocks.getGraph).toHaveBeenCalledTimes(1)
    mocks.getGraph.mockClear()
    mocks.getGraph.mockResolvedValue(envelope({ nodes: [{ id: 'a' }], edges: [] }))
    // graph_changed for this map → monoStore bumps graphRefreshKey['workflow_topo/map-1'].
    await act(async () => {
      const prev = mocks.stateRef.current as MonoStoreState
      mocks.stateRef.current = { ...prev, graphRefreshKey: { 'workflow_topo/map-1': 1 } }
      mocks.listeners.forEach(l => l(mocks.stateRef.current))
    })
    expect(mocks.getGraph).toHaveBeenCalledWith('workflow_topo', 'map-1')
    expect(seen.at(-1)).toEqual(new Map([['a', []]]))
  })

  it('does not re-pull when a refresh key bumps for a graph not in view', async () => {
    mocks.getGraph.mockResolvedValue(envelope({ nodes: [], edges: [] }))
    await act(async () => {
      root.render(<HookSpy cards={[mapCard('map-1')]} expandedMapIds={new Set(['map-1'])} onDeps={() => {}} />)
    })
    mocks.getGraph.mockClear()
    await act(async () => {
      const prev = mocks.stateRef.current as MonoStoreState
      mocks.stateRef.current = { ...prev, graphRefreshKey: { 'workflow_topo/other-map': 1 } }
      mocks.listeners.forEach(l => l(mocks.stateRef.current))
    })
    expect(mocks.getGraph).not.toHaveBeenCalled()
  })

  it('does not fetch the graph for a collapsed workflow map', async () => {
    mocks.getGraph.mockResolvedValue(envelope({ nodes: [{ id: 'a' }], edges: [] }))
    const seen: Map<string, string[]>[] = []
    await act(async () => {
      root.render(<HookSpy cards={[mapCard('map-1')]} expandedMapIds={new Set<string>()} onDeps={d => seen.push(d)} />)
    })
    expect(mocks.getGraph).not.toHaveBeenCalled()
    expect(seen.at(-1)).toEqual(new Map())
  })

  it('fetches the graph when a previously collapsed map is expanded', async () => {
    mocks.getGraph.mockResolvedValue(envelope({ nodes: [{ id: 'a' }], edges: [{ from: 'a', to: 'b', kind: 'depends_on' }] }))
    const seen: Map<string, string[]>[] = []
    let currentExpanded = new Set<string>()

    await act(async () => {
      root.render(<HookSpy cards={[mapCard('map-1')]} expandedMapIds={currentExpanded} onDeps={d => seen.push(d)} />)
    })
    // Collapsed on first render — no fetch.
    expect(mocks.getGraph).not.toHaveBeenCalled()

    // Expand the map — the graph should now be fetched.
    mocks.getGraph.mockClear()
    currentExpanded = new Set(['map-1'])
    await act(async () => {
      root.render(<HookSpy cards={[mapCard('map-1')]} expandedMapIds={currentExpanded} onDeps={d => seen.push(d)} />)
    })
    expect(mocks.getGraph).toHaveBeenCalledWith('workflow_topo', 'map-1')
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect(seen.at(-1)).toEqual(new Map([['a', ['b']]]))
  })

  it('still re-pulls on graphRefreshKey bump for an expanded map', async () => {
    mocks.getGraph.mockResolvedValue(envelope({ nodes: [{ id: 'a' }], edges: [{ from: 'a', to: 'b', kind: 'depends_on' }] }))
    const expanded = new Set(['map-1'])
    const seen: Map<string, string[]>[] = []
    await act(async () => {
      root.render(<HookSpy cards={[mapCard('map-1')]} expandedMapIds={expanded} onDeps={d => seen.push(d)} />)
    })
    expect(mocks.getGraph).toHaveBeenCalledTimes(1)
    mocks.getGraph.mockClear()
    mocks.getGraph.mockResolvedValue(envelope({ nodes: [{ id: 'a' }], edges: [] }))
    await act(async () => {
      const prev = mocks.stateRef.current as MonoStoreState
      mocks.stateRef.current = { ...prev, graphRefreshKey: { 'workflow_topo/map-1': 1 } }
      mocks.listeners.forEach(l => l(mocks.stateRef.current))
    })
    expect(mocks.getGraph).toHaveBeenCalledWith('workflow_topo', 'map-1')
    expect(seen.at(-1)).toEqual(new Map([['a', []]]))
  })

  it('fetches workflow_topo graphs for multiple expanded maps in parallel', async () => {
    mocks.getGraph
      .mockResolvedValueOnce(envelope({ nodes: [{ id: 'a' }], edges: [{ from: 'a', to: 'b', kind: 'depends_on' }] }))
      .mockResolvedValueOnce(envelope({ nodes: [{ id: 'c' }], edges: [{ from: 'c', to: 'd', kind: 'depends_on' }] }))
    const seen: Map<string, string[]>[] = []
    await act(async () => {
      root.render(<HookSpy cards={[mapCard('map-1'), mapCard('map-2')]} expandedMapIds={new Set(['map-1', 'map-2'])} onDeps={d => seen.push(d)} />)
    })
    expect(mocks.getGraph).toHaveBeenCalledTimes(2)
    expect(mocks.getGraph).toHaveBeenNthCalledWith(1, 'workflow_topo', 'map-1')
    expect(mocks.getGraph).toHaveBeenNthCalledWith(2, 'workflow_topo', 'map-2')
    expect(seen.at(-1)).toEqual(new Map([
      ['a', ['b']],
      ['c', ['d']],
    ]))
  })

  it('does not apply merged deps after the component unmounts (cancel semantics)', async () => {
    let resolveGetGraph: (() => void) | null = null
    mocks.getGraph.mockImplementation(() => new Promise<void>(resolve => { resolveGetGraph = resolve }))
    const seen: Map<string, string[]>[] = []
    await act(async () => {
      root.render(<HookSpy cards={[mapCard('map-1')]} expandedMapIds={new Set(['map-1'])} onDeps={d => seen.push(d)} />)
    })
    expect(mocks.getGraph).toHaveBeenCalledTimes(1)

    act(() => { root.unmount() })
    // Resolve the in-flight fetch after unmount; the cancelled effect must not update state.
    await act(async () => {
      resolveGetGraph?.()
      await new Promise(r => setTimeout(r, 0))
    })
    expect(seen.at(-1)).toEqual(new Map())
  })
})
