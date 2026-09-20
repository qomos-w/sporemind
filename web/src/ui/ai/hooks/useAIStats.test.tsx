import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import type { ReactNode } from 'react'
import {
  rangeConfig,
  toSince,
  toUntil,
  useAIStats,
  useAIStatsRecords,
  type AIStatsTimeRange,
} from './useAIStats'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../application/generated-client', () => ({ client: {} }))

const seriesMock = vi.fn()
const queryMock = vi.fn()
vi.mock('../../../gen-clients/aistats/client', () => ({
  series: (...args: unknown[]) => seriesMock(...args),
  query: (...args: unknown[]) => queryMock(...args),
}))

const actorIdMock = vi.fn()
vi.mock('../../../gen-clients/workspace/client', () => ({
  aistatsActorId: (...args: unknown[]) => actorIdMock(...args),
}))

describe('rangeConfig / toSince', () => {
  it('returns bucket sizes per range', () => {
    expect(rangeConfig('1h').bucketMs).toBe(5 * 60_000)
    expect(rangeConfig('24h').bucketMs).toBe(60 * 60_000)
    expect(rangeConfig('all').sinceMs).toBeNull()
  })

  it('toSince is undefined for all, ISO otherwise', () => {
    expect(toSince('all')).toBeUndefined()
    expect(toSince('1h')).toMatch(/^\d{4}-\d{2}-\d{2}T/)
  })

  it('custom range clamps bucket width and emits ISO since/until', () => {
    const hour = 60 * 60_000
    // 2h span → 2min buckets via ~60-bucket target.
    expect(rangeConfig({ sinceMs: 0, untilMs: 2 * hour }).bucketMs).toBe(2 * 60_000)
    // 1min span clamps up to the 1min floor.
    expect(rangeConfig({ sinceMs: 0, untilMs: 30_000 }).bucketMs).toBe(60_000)
    // 90-day span clamps down to 24h buckets.
    expect(rangeConfig({ sinceMs: 0, untilMs: 90 * 24 * hour }).bucketMs).toBe(24 * hour)
    const since = toSince({ sinceMs: Date.UTC(2026, 0, 2, 3, 4), untilMs: Date.UTC(2026, 0, 9) })
    const until = toUntil({ sinceMs: Date.UTC(2026, 0, 2, 3, 4), untilMs: Date.UTC(2026, 0, 9) })
    expect(since).toBe('2026-01-02T03:04:00.000Z')
    expect(until).toBe('2026-01-09T00:00:00.000Z')
    expect(toUntil('24h')).toBeUndefined()
  })
})

describe('useAIStats hook', () => {
  beforeEach(() => {
    seriesMock.mockReset()
    actorIdMock.mockReset()
    seriesMock.mockResolvedValue({
      Buckets: [
        { Start: 1, End: 2, Requests: 5, Errors: 1, LatencyP50: 200, LatencyP95: 500, TtftP50: 100, LatencySumMs: 1000, OutputTokens: 50, ErrorCodes: { rate_limit: 1 } },
      ],
      ModelStats: [],
      OverallLatencyP50: 200,
      OverallLatencyP95: 500,
      OverallTtftP50: 100,
      OverallLatencySumMs: 1000,
      OverallOutputTokens: 50,
    })
    actorIdMock.mockResolvedValue({ ActorId: 'aistats-1' })
  })

  it('resolves the aistats actor id and fetches series with target', async () => {
    let captured: ReturnType<typeof useAIStats> | null = null
    function Probe(): ReactNode {
      captured = useAIStats({ timeRange: '1h', autoRefresh: false })
      return null
    }
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(<Probe />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })

    expect(actorIdMock).toHaveBeenCalledTimes(1)
    expect(seriesMock).toHaveBeenCalledTimes(1)
    const req = seriesMock.mock.calls[0]![1]
    expect(req).toMatchObject({ Scope: 'workspace', ScopeId: '', BucketMs: 5 * 60_000 })
    expect(captured!.series).not.toBeNull()
    expect(captured!.series!.Buckets.length).toBe(1)
    expect(captured!.modelSeries).toBeNull()
    expect(captured!.loading).toBe(false)
    expect(captured!.error).toBeNull()

    act(() => {
      root.unmount()
    })
  })

  it('fetches a model-scoped series when a model is selected', async () => {
    let captured: ReturnType<typeof useAIStats> | null = null
    function Probe(): ReactNode {
      captured = useAIStats({ timeRange: '1h', autoRefresh: false, selectedModel: { provider: 'anthropic', model: 'claude' } })
      return null
    }
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(<Probe />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })

    expect(seriesMock).toHaveBeenCalledTimes(2)
    const modelReq = seriesMock.mock.calls[1]![1]
    expect(modelReq).toMatchObject({ Scope: 'model', ScopeId: 'anthropic/claude', BucketMs: 5 * 60_000 })
    expect(captured!.modelSeries).not.toBeNull()
    expect(captured!.modelSeries!.Buckets.length).toBe(1)

    act(() => {
      root.unmount()
    })
  })

  it('passes modelFilter to every series request and refetches only when it changes', async () => {
    let captured: ReturnType<typeof useAIStats> | null = null
    function Probe({ filter }: { filter?: string }): ReactNode {
      captured = useAIStats({
        timeRange: '1h',
        autoRefresh: false,
        selectedModel: { provider: 'anthropic', model: 'claude' },
        modelFilter: filter,
      })
      return null
    }
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(<Probe filter="claude" />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })

    expect(seriesMock).toHaveBeenCalledTimes(2)
    expect(seriesMock.mock.calls[0]![1]).toMatchObject({ Scope: 'workspace', ModelFilter: 'claude' })
    expect(seriesMock.mock.calls[1]![1]).toMatchObject({ Scope: 'model', ScopeId: 'anthropic/claude', ModelFilter: 'claude' })

    // Re-render with the same filter value must not re-request.
    seriesMock.mockClear()
    act(() => {
      root.render(<Probe filter="claude" />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })
    expect(seriesMock).not.toHaveBeenCalled()
    expect(captured!.series).not.toBeNull()

    // A changed filter value triggers fresh requests carrying the new value.
    seriesMock.mockClear()
    act(() => {
      root.render(<Probe filter="claude-3" />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })
    expect(seriesMock).toHaveBeenCalledTimes(2)
    expect(seriesMock.mock.calls[0]![1]).toMatchObject({ Scope: 'workspace', ModelFilter: 'claude-3' })
    expect(seriesMock.mock.calls[1]![1]).toMatchObject({ Scope: 'model', ScopeId: 'anthropic/claude', ModelFilter: 'claude-3' })

    // Clearing the filter reverts to unfiltered requests (empty = no filter).
    seriesMock.mockClear()
    act(() => {
      root.render(<Probe filter="" />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })
    expect(seriesMock).toHaveBeenCalledTimes(2)
    expect(seriesMock.mock.calls[0]![1]).toMatchObject({ Scope: 'workspace', ModelFilter: '' })
    expect(seriesMock.mock.calls[1]![1]).toMatchObject({ Scope: 'model', ScopeId: 'anthropic/claude', ModelFilter: '' })

    act(() => {
      root.unmount()
    })
  })

  it('clears loading when a run is cancelled without a successor (slow-backend wedge)', async () => {
    // Reproduces the eternal-Loading panel: a request is cancelled (e.g. by the
    // next auto-refresh tick or a dependency change) before it settles, and no
    // newer run has started. The cancelled run's finally must still clear
    // loading, otherwise the panel spins forever.
    seriesMock.mockImplementationOnce(() => new Promise(() => {}))
    let captured: ReturnType<typeof useAIStats> | null = null
    function Probe({ range }: { range: AIStatsTimeRange }): ReactNode {
      captured = useAIStats({ timeRange: range, autoRefresh: false })
      return null
    }
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(<Probe range="1h" />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })
    expect(captured!.loading).toBe(true)

    // Dependency change cancels run #1 while its series promise is pending.
    // Run #2 (the successor) settles immediately via the default mock.
    act(() => {
      root.render(<Probe range="24h" />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })
    expect(captured!.loading).toBe(false)

    act(() => {
      root.unmount()
    })
  })

  it('late-settling cancelled run must not clear the successor run loading (token ownership)', async () => {
    // Reproduces the auto-refresh overlap (interval < invoke timeout): run #1
    // is cancelled by run #2 while its series promise is still pending. When
    // run #1 settles late it must NOT clear loading — run #2 owns it. Run #2
    // settling must clear it. Guards the run-token ownership semantics.
    let resolveRun1!: (v: unknown) => void
    let resolveRun2!: (v: unknown) => void
    let resolveRun3!: (v: unknown) => void
    seriesMock
      .mockImplementationOnce(() => new Promise(resolve => { resolveRun1 = resolve }))
      .mockImplementationOnce(() => new Promise(resolve => { resolveRun2 = resolve }))
      .mockImplementationOnce(() => new Promise(resolve => { resolveRun3 = resolve }))
    let captured: ReturnType<typeof useAIStats> | null = null
    function Probe({ range }: { range: AIStatsTimeRange }): ReactNode {
      captured = useAIStats({ timeRange: range, autoRefresh: false })
      return null
    }
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(<Probe range="1h" />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })
    expect(captured!.loading).toBe(true)

    // Dependency change starts run #2; run #1 is cancelled but still pending.
    act(() => {
      root.render(<Probe range="24h" />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })
    expect(captured!.loading).toBe(true)

    // Run #1 settles late — its finally must not clear the successor loading.
    await act(async () => {
      resolveRun1({ Buckets: [], ModelStats: [] })
      await new Promise(r => setTimeout(r, 10))
    })
    expect(captured!.loading).toBe(true)

    // Run #2 settles — loading clears and its data lands.
    await act(async () => {
      resolveRun2({ Buckets: [{ Start: 1 }], ModelStats: [] })
      await new Promise(r => setTimeout(r, 10))
    })
    expect(captured!.loading).toBe(false)
    expect(captured!.series).not.toBeNull()

    // A run pending with no successor: the finally must release loading.
    act(() => {
      root.render(<Probe range="7d" />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })
    expect(captured!.loading).toBe(true)
    await act(async () => {
      resolveRun3({ Buckets: [], ModelStats: [] })
      await new Promise(r => setTimeout(r, 10))
    })
    expect(captured!.loading).toBe(false)

    act(() => {
      root.unmount()
    })
  })
})

describe('useAIStatsRecords hook', () => {
  beforeEach(() => {
    queryMock.mockReset()
    actorIdMock.mockReset()
    queryMock.mockResolvedValue({
      Records: [{ Id: 'r1' }, { Id: 'r2' }],
      Counters: { RequestCount: 2 },
      Total: 42,
    })
    actorIdMock.mockResolvedValue({ ActorId: 'aistats-1' })
  })

  it('calls query with desc order and pagination params', async () => {
    let captured: ReturnType<typeof useAIStatsRecords> | null = null
    function Probe(): ReactNode {
      captured = useAIStatsRecords({ pageSize: 50, page: 0 })
      return null
    }
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(<Probe />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })

    expect(queryMock).toHaveBeenCalledTimes(1)
    const req = queryMock.mock.calls[0]![1]
    expect(req).toMatchObject({ Scope: 'workspace', Order: 'desc', Limit: 50, Offset: 0 })
    expect(captured!.records.length).toBe(2)
    expect(captured!.total).toBe(42)

    act(() => {
      root.unmount()
    })
  })
})
