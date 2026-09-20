import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AIStatsDashboard } from './AIStatsDashboard'
import { I18nProvider } from '../../../i18n'
import type { AIStatsRecord, AIStatsSeriesResp } from '../../../gen-types/aistats'

function renderDashboard(root: Root) {
  act(() => { root.render(<I18nProvider initialLocale="en-US"><AIStatsDashboard /></I18nProvider>) })
}

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const chartOptions: Record<string, unknown>[] = []
vi.mock('./ECharts', () => ({
  ECharts: ({ className, option }: { className?: string; option: Record<string, unknown> }) => {
    chartOptions.push(option)
    return <div className={className} data-testid="echarts" />
  },
}))
vi.mock('../../../application/generated-client', () => ({ client: {} }))

const queryMock = vi.fn()
const seriesMock = vi.fn()
vi.mock('../../../gen-clients/aistats/client', () => ({
  query: (...args: unknown[]) => queryMock(...args),
  series: (...args: unknown[]) => seriesMock(...args),
}))
const actorIdMock = vi.fn()
vi.mock('../../../gen-clients/workspace/client', () => ({
  aistatsActorId: (...args: unknown[]) => actorIdMock(...args),
}))

function record(over: Partial<AIStatsRecord> = {}): AIStatsRecord {
  const ts = new Date(Date.now() - 60_000).toISOString()
  return {
    Id: 'r',
    WorkspaceId: 'ws',
    ProjectId: 'p',
    AgentId: 'a',
    SessionId: 's',
    TurnId: 't',
    RequestId: 'rq',
    Provider: 'openai',
    Model: 'gpt-4o',
    StartedAt: ts,
    CompletedAt: ts,
    Usage: { InputTokens: 100, OutputTokens: 50, CostTotal: 0.01, CacheReadInputTokens: 10 } as any,
    LatencyMs: 500,
    FirstTokenMs: 120,
    ...over,
  }
}

describe('AIStatsDashboard', () => {
  let baseSeries: AIStatsSeriesResp

  beforeEach(() => {
    chartOptions.length = 0
    queryMock.mockReset()
    seriesMock.mockReset()
    actorIdMock.mockReset()
    actorIdMock.mockResolvedValue({ ActorId: 'aistats-1' })
    baseSeries = {
      Buckets: [
        {
          Start: Date.now() - 60_000,
          End: Date.now(),
          Requests: 42,
          Errors: 1,
          InputTokens: 1000,
          OutputTokens: 500,
          CacheRead: 200,
          CacheWrite: 20,
          ReasoningTokens: 0,
          Cost: 0.031,
          LatencyP50: 100,
          LatencyP95: 200,
          LatencySumMs: 500,
          TtftP50: 50,
          ErrorCodes: { rate_limit: 1 },
        } as any,
      ],
      ModelStats: [
        { Provider: 'openai', Model: 'gpt-4o', Requests: 30, Errors: 0, InputTokens: 800, OutputTokens: 400, CacheRead: 100, CacheWrite: 10, CostTotal: 0.025, CostInput: 0.01, CostOutput: 0.01, CostCacheRead: 0.005, CostCacheWrite: 0, LatencyP95: 200, TtftP50: 50, LatencySumMs: 1000 } as any,
        { Provider: 'anthropic', Model: 'claude', Requests: 12, Errors: 1, InputTokens: 200, OutputTokens: 100, CacheRead: 20, CacheWrite: 5, CostTotal: 0.006, CostInput: 0.003, CostOutput: 0.002, CostCacheRead: 0.001, CostCacheWrite: 0, LatencyP95: 300, TtftP50: 80, LatencySumMs: 500 } as any,
      ],
      OverallLatencyP50: 100,
      OverallLatencyP95: 200,
      OverallTtftP50: 50,
      OverallLatencySumMs: 1500,
      OverallOutputTokens: 500,
    }
    seriesMock.mockResolvedValue(baseSeries)
    queryMock.mockResolvedValue({
      Records: [
        record({ Id: 'r1' }),
        record({ Id: 'r2', StopReason: 'error', ErrorCode: 'rate_limit', ErrorMessage: '429' }),
      ],
      Counters: { RequestCount: 2 },
    })
  })

  it('renders summary metrics, split tokens, cache details and collapsed model pills', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    renderDashboard(root)
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    const html = container.innerHTML
    expect(html).toContain('Model Stats')
    expect(html).toContain('42') // request count (from series buckets)
    // Token split into Input/Output summary metrics.
    expect(html).toContain('Input Tokens')
    expect(html).toContain('Output Tokens')
    // Cache detail metric with read/write.
    expect(html).toContain('Cache Hit')
    expect(html).toContain('read ')
    expect(html).toContain('write ')
    // Latency metric surfaces TTFT.
    expect(html).toContain('ttft')
    expect(html).toContain('Cache Hit Rate') // dedicated cache trend chart
    expect(html).toContain('Token Speed') // token speed chart (replaced tokens bar)
    // Models section is collapsed by default and shows pill buttons.
    expect(html).toContain('gpt-4o')
    expect(html).toContain('Rate Limit') // error code distribution label

    const series = chartOptions.flatMap(option => option.series as { type?: string; areaStyle?: unknown }[] ?? [])
    expect(series.filter(item => item.type === 'line')).toHaveLength(8)
    expect(series.filter(item => item.type === 'line' && item.areaStyle)).toHaveLength(2)
    expect(series.filter(item => item.type === 'pie')).toHaveLength(2)

    // Collapsed pills hide low-volume models: gpt-4o (30 req) shows,
    // claude (12 req < 30) is hidden until the section is expanded.
    const pills = container.querySelectorAll('.ai-aistats-model-pill')
    expect(pills.length).toBe(1)
    expect(pills[0]!.textContent).toContain('gpt-4o')
    expect(container.querySelector('.ai-aistats-models-pills')!.textContent).not.toContain('claude')
    expect(container.querySelectorAll('.ai-aistats-models tbody tr').length).toBe(0)

    act(() => { root.unmount() })
    document.body.removeChild(container)
  })

  it('expands model section and exposes the full model table', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    renderDashboard(root)
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    const expandBtn = container.querySelector('.ai-aistats-models-toggle') as HTMLElement
    expect(expandBtn).toBeTruthy()
    expect(expandBtn.textContent).toContain('Expand')
    act(() => { expandBtn.click() })
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    // Model table exposes split token columns and per-model sample metrics.
    const modelHeaders = container.querySelectorAll('.ai-aistats-models th')
    const headerText = Array.from(modelHeaders).map(th => th.textContent)
    expect(headerText).toContain('In')
    expect(headerText).toContain('Out')
    expect(headerText).toContain('Cache%')
    expect(headerText).toContain('TTFT')
    expect(headerText).toContain('Tok/s')
    expect(headerText).toContain('Fails')
    expect(headerText).toContain('Success%')

    const rows = container.querySelectorAll('.ai-aistats-models tbody tr')
    expect(rows.length).toBe(2)

    act(() => { root.unmount() })
    document.body.removeChild(container)
  })

  it('applies model filter on pill click while collapsed', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    // Claude needs >= 30 requests to appear as a collapsed pill.
    seriesMock.mockResolvedValue({
      ...baseSeries,
      ModelStats: [
        baseSeries.ModelStats[0]!,
        { ...baseSeries.ModelStats[1]!, Requests: 40 },
      ],
    })
    renderDashboard(root)
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    // Workspace total surfaces in the summary before filtering.
    const summaryBefore = container.querySelector('.ai-aistats-summary')
    expect(summaryBefore?.textContent).toContain('42')

    // Model-scoped series returns claude-only buckets (12 requests).
    seriesMock.mockImplementation(async (_client: unknown, req: { Scope: string }) => {
      if (req.Scope !== 'model') {
        return seriesMock.mock.results[0]?.value
      }
      return {
        Buckets: [
          {
            Start: Date.now() - 60_000,
            End: Date.now(),
            Requests: 12,
            Errors: 1,
            InputTokens: 200,
            OutputTokens: 100,
            CacheRead: 20,
            CacheWrite: 5,
            ReasoningTokens: 0,
            Cost: 0.006,
            LatencyP50: 120,
            LatencyP95: 300,
            LatencySumMs: 500,
            TtftP50: 80,
            ErrorCodes: { rate_limit: 1 },
          } as any,
        ],
        ModelStats: [],
        OverallLatencyP50: 120,
        OverallLatencyP95: 300,
        OverallTtftP50: 80,
        OverallLatencySumMs: 500,
        OverallOutputTokens: 100,
      }
    })

    const optionsBefore = chartOptions.length

    // Click the claude pill in the collapsed models section.
    const pills = container.querySelectorAll('.ai-aistats-model-pill')
    const claudePill = Array.from(pills).find(p => p.textContent?.includes('claude')) as HTMLElement
    expect(claudePill).toBeTruthy()
    act(() => { claudePill.click() })
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    // Filter badge reflects the selected model.
    expect(container.innerHTML).toContain('Filtered')
    expect(container.innerHTML).toContain('anthropic/claude')
    // The summary now reflects the selected model (claude: 12 requests),
    // not the workspace total (42).
    const summaryAfter = container.querySelector('.ai-aistats-summary')
    expect(summaryAfter?.textContent).toContain('12')
    expect(summaryAfter?.textContent).not.toContain('42')
    // Other models remain as pills — selection must not clear them.
    expect(container.innerHTML).toContain('gpt-4o')

    // A model-scoped series request was issued.
    const modelCall = seriesMock.mock.calls.some(([, r]) => r.Scope === 'model' && r.ScopeId === 'anthropic/claude')
    expect(modelCall).toBe(true)

    // Charts rendered after selection consume the model-scoped buckets:
    // the request-volume line carries 12, never the global 42.
    const pushed = chartOptions.slice(optionsBefore)
    const lineData = pushed
      .flatMap(o => (o.series as { type?: string; data?: unknown[] }[] | undefined) ?? [])
      .filter(s => s.type === 'line')
      .flatMap(s => s.data ?? [])
    expect(lineData).toContain(12)
    expect(lineData).not.toContain(42)

    act(() => { root.unmount() })
    document.body.removeChild(container)
  })

  it('applies model filter on row click in expanded table', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    renderDashboard(root)
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    // Expand the models section first.
    const expandBtn = container.querySelector('.ai-aistats-models-toggle') as HTMLElement
    act(() => { expandBtn.click() })
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    // Workspace total surfaces in the summary before filtering.
    const summaryBefore = container.querySelector('.ai-aistats-summary')
    expect(summaryBefore?.textContent).toContain('42')

    // Click the claude model row.
    const rows = container.querySelectorAll('.ai-aistats-models tbody tr')
    const claudeRow = Array.from(rows).find(tr => tr.textContent?.includes('claude')) as HTMLElement
    expect(claudeRow).toBeTruthy()
    act(() => { claudeRow.click() })
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    // Filter badge reflects the selected model.
    expect(container.innerHTML).toContain('Filtered')
    expect(container.innerHTML).toContain('anthropic/claude')
    // The summary now reflects the selected model (claude: 12 requests),
    // not the workspace total (42).
    const summaryAfter = container.querySelector('.ai-aistats-summary')
    expect(summaryAfter?.textContent).toContain('12')
    expect(summaryAfter?.textContent).not.toContain('42')
    // Other models remain in the table — selection must not clear them.
    expect(container.innerHTML).toContain('gpt-4o')

    act(() => { root.unmount() })
    document.body.removeChild(container)
  })

  it('filters models by name in collapsed pills on blur', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    // Claude needs >= 30 requests to appear as a collapsed pill.
    seriesMock.mockResolvedValue({
      ...baseSeries,
      ModelStats: [
        baseSeries.ModelStats[0]!,
        { ...baseSeries.ModelStats[1]!, Requests: 40 },
      ],
    })
    renderDashboard(root)
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    // Both models visible initially.
    expect(container.innerHTML).toContain('gpt-4o')
    expect(container.innerHTML).toContain('claude')

    const input = container.querySelector('.ai-aistats-filter-input') as HTMLInputElement
    expect(input).toBeTruthy()
    const nativeSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!

    // Typing alone must NOT filter — commit happens on blur.
    act(() => {
      input.focus()
      nativeSetter.call(input, 'gpt')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    const pillsBeforeBlur = container.querySelector('.ai-aistats-models-pills')!
    expect(pillsBeforeBlur.textContent).toContain('gpt-4o')
    expect(pillsBeforeBlur.textContent).toContain('claude')

    // Blur commits the filter: model list filters instantly, and the
    // committed value is forwarded to useAIStats so charts filter too.
    act(() => { input.blur() })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })

    expect(container.innerHTML).toContain('gpt-4o')
    const pillsSection = container.querySelector('.ai-aistats-models-pills')!
    expect(pillsSection.textContent).toContain('gpt-4o')
    expect(pillsSection.textContent).not.toContain('claude')
    const wsCalls = seriesMock.mock.calls.filter(([, r]) => r.Scope === 'workspace')
    expect(wsCalls[wsCalls.length - 1]![1]).toMatchObject({ ModelFilter: 'gpt' })

    // Clear filter (empty + blur) restores both.
    act(() => {
      nativeSetter.call(input, '')
      input.dispatchEvent(new Event('input', { bubbles: true }))
      input.dispatchEvent(new Event('blur', { bubbles: true }))
    })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    expect(container.innerHTML).toContain('gpt-4o')
    expect(container.innerHTML).toContain('claude')

    act(() => { root.unmount() })
    document.body.removeChild(container)
  })

  it('filters models by name in expanded table on blur', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    renderDashboard(root)
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    // Expand the models section.
    const expandBtn = container.querySelector('.ai-aistats-models-toggle') as HTMLElement
    act(() => { expandBtn.click() })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })

    // Both rows present initially.
    expect(container.querySelectorAll('.ai-aistats-models tbody tr').length).toBe(2)

    // Typing alone must NOT filter — commit happens on blur.
    const input = container.querySelector('.ai-aistats-filter-input') as HTMLInputElement
    const nativeSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    act(() => {
      input.focus()
      nativeSetter.call(input, 'claude')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    expect(container.querySelectorAll('.ai-aistats-models tbody tr').length).toBe(2)

    // Blur commits the filter.
    act(() => { input.blur() })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })

    const rows = container.querySelectorAll('.ai-aistats-models tbody tr')
    expect(rows.length).toBe(1)
    expect(rows[0]!.textContent).toContain('claude')
    expect(rows[0]!.textContent).not.toContain('gpt-4o')

    act(() => { root.unmount() })
    document.body.removeChild(container)
  })

  it('applies a custom date range picked from the calendar', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    renderDashboard(root)
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    // Open the calendar popover from the Custom range button.
    const customBtn = Array.from(container.querySelectorAll('.ai-aistats-range-btn'))
      .find(b => b.textContent === 'Custom') as HTMLElement
    expect(customBtn).toBeTruthy()
    act(() => { customBtn.click() })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })

    const pop = container.querySelector('.ai-aistats-cal-pop') as HTMLElement
    expect(pop).toBeTruthy()

    // Pick first and last day of the visible month (first click resets the
    // prefilled range, second click sets the end).
    const days = Array.from(pop.querySelectorAll('.ai-aistats-cal-day')) as HTMLElement[]
    expect(days.length).toBeGreaterThan(20)
    act(() => { days[0]!.click() })
    act(() => { days[days.length - 1]!.click() })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })

    const applyBtn = pop.querySelector('.ai-aistats-cal-btn.primary') as HTMLButtonElement
    expect(applyBtn.disabled).toBe(false)
    act(() => { applyBtn.click() })
    await act(async () => { await new Promise(r => setTimeout(r, 20)) })

    // Popover closes and the Custom button shows the picked range.
    expect(container.querySelector('.ai-aistats-cal-pop')).toBeNull()
    const customBtnAfter = Array.from(container.querySelectorAll('.ai-aistats-range-btn'))
      .find(b => b.className.includes('ai-aistats-range-custom')) as HTMLElement
    expect(customBtnAfter.className).toContain('active')
    expect(customBtnAfter.textContent).toMatch(/^\d{2}-\d{2}~\d{2}-\d{2}$/)

    // The last workspace series request carries Since AND Until, in order.
    const calls = seriesMock.mock.calls.filter(([, r]) => r.Scope === 'workspace')
    const last = calls[calls.length - 1]![1]
    expect(last.Since).toMatch(/^\d{4}-\d{2}-\d{2}T/)
    expect(last.Until).toMatch(/^\d{4}-\d{2}-\d{2}T/)
    expect(new Date(last.Until).getTime()).toBeGreaterThan(new Date(last.Since).getTime())
    // Preset requests do not send Until.
    const first = calls[0]![1]
    expect(first.Until).toBeUndefined()

    act(() => { root.unmount() })
    document.body.removeChild(container)
  })
})
