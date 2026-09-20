import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AppConsoleTab } from './AppConsoleTab'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  logsQuery: vi.fn(),
}))

vi.mock('../../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../../gen-clients/workspace/client', () => ({
  logsQuery: hoisted.logsQuery,
}))

interface Entry {
  Timestamp: string
  Level: string
  Message: string
  Caller?: string
  Fields?: Record<string, unknown>
}

function entry(ts: string, level: string, message: string, caller?: string): Entry {
  return { Timestamp: ts, Level: level, Message: message, Caller: caller }
}

const T0 = '2026-08-21T10:00:00.000Z'
const T1 = '2026-08-21T10:00:01.000Z'
const T2 = '2026-08-21T10:00:02.000Z'

const lastCall = () => hoisted.logsQuery.mock.calls[hoisted.logsQuery.mock.calls.length - 1]

describe('AppConsoleTab', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    hoisted.t.mockClear()
    hoisted.logsQuery.mockReset()
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
    vi.useRealTimers()
  })

  // ── 加载渲染 ──
  it('renders loaded console entries in chronological order with level badges and meta', async () => {
    hoisted.logsQuery.mockResolvedValueOnce({
      Items: [
        entry(T0, 'info', 'mounted'),
        entry(T1, 'warn', 'slow request', 'net.ts:12'),
        entry(T2, 'error', 'boom'),
      ],
      Truncated: false,
    })

    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })

    expect(hoisted.logsQuery).toHaveBeenCalledTimes(1)
    expect(hoisted.logsQuery).toHaveBeenCalledWith({}, { Source: 'console', Limit: 200 })

    const rows = container.querySelectorAll('.app-console-row')
    expect(rows.length).toBe(3)
    expect(rows[0]!.querySelector('.app-console-msg')!.textContent).toBe('mounted')
    expect(rows[2]!.querySelector('.app-console-msg')!.textContent).toBe('boom')
    // level badge text reflects the entry level
    expect(rows[0]!.querySelector('.app-console-level')!.textContent).toBe('info')
    expect(rows[2]!.querySelector('.app-console-level')!.textContent).toBe('error')
    // caller rendered as a meta badge
    expect(rows[1]!.querySelector('.app-console-caller')!.textContent).toBe('net.ts:12')
  })

  it('pretty-prints a JSON object payload in the message', async () => {
    hoisted.logsQuery.mockResolvedValueOnce({
      Items: [entry(T0, 'info', '{"a":1,"b":[1,2]}')],
      Truncated: false,
    })
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })
    const msg = container.querySelector('.app-console-msg')!.textContent!
    expect(msg).toBe('{\n  "a": 1,\n  "b": [\n    1,\n    2\n  ]\n}')
  })

  // ── 向上翻页 ──
  it('pages older entries via the load-older button and reaches the no-more state', async () => {
    // first page: newest 2, truncated with a NextBefore cursor
    hoisted.logsQuery.mockResolvedValueOnce({
      Items: [entry(T1, 'info', 'page1-a'), entry(T2, 'info', 'page1-b')],
      Truncated: true,
      NextBefore: T1,
    })
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })

    const loadBtn = () => container.querySelector('.app-console-load-btn') as HTMLButtonElement | null
    expect(loadBtn()).not.toBeNull()

    // second page: strictly older 2, not truncated → no more
    hoisted.logsQuery.mockResolvedValueOnce({
      Items: [entry(T0, 'info', 'page0-a'), entry(T0, 'info', 'page0-b')],
      Truncated: false,
    })

    await act(async () => { loadBtn()!.click() })

    // second call carried the Before cursor
    expect(lastCall()![1]).toEqual({ Source: 'console', Limit: 200, Before: T1 })

    const rows = container.querySelectorAll('.app-console-row')
    expect(rows.length).toBe(4)
    // prepended older entries come first
    expect(rows[0]!.querySelector('.app-console-msg')!.textContent).toBe('page0-a')
    expect(rows[3]!.querySelector('.app-console-msg')!.textContent).toBe('page1-b')
    // cursor exhausted → no-more hint replaces the button
    expect(container.querySelector('.app-console-no-more')).not.toBeNull()
    expect(container.querySelector('.app-console-load-btn')).toBeNull()
  })

  it('pages older entries when scrolled to the top', async () => {
    hoisted.logsQuery.mockResolvedValueOnce({
      Items: [entry(T1, 'info', 'a'), entry(T2, 'info', 'b')],
      Truncated: true,
      NextBefore: T1,
    })
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })

    hoisted.logsQuery.mockResolvedValueOnce({
      Items: [entry(T0, 'info', 'x')],
      Truncated: false,
    })

    const scroll = container.querySelector('.app-console-scroll')! as HTMLElement
    await act(async () => {
      scroll.scrollTop = 0
      scroll.dispatchEvent(new Event('scroll', { bubbles: true }))
    })
    expect(lastCall()![1]).toEqual({ Source: 'console', Limit: 200, Before: T1 })
    expect(container.querySelectorAll('.app-console-row').length).toBe(3)
  })

  // ── 过滤 ──
  it('filters rows by level client-side', async () => {
    hoisted.logsQuery.mockResolvedValueOnce({
      Items: [entry(T0, 'info', 'keep-i'), entry(T1, 'error', 'drop-e'), entry(T2, 'info', 'keep-i2')],
      Truncated: false,
    })
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })
    expect(container.querySelectorAll('.app-console-row').length).toBe(3)

    const chips = container.querySelectorAll('.app-console-level-chip')
    // FILTER_LEVELS order: error, warn, info, debug
    await act(async () => { (chips[0] as HTMLButtonElement).click() }) // error
    expect(container.querySelectorAll('.app-console-row').length).toBe(1)
    expect(container.querySelector('.app-console-msg')!.textContent).toBe('drop-e')

    await act(async () => { (chips[0] as HTMLButtonElement).click() }) // clear error filter
    expect(container.querySelectorAll('.app-console-row').length).toBe(3)
  })

  const setInput = (value: string) => {
    const input = container.querySelector('.app-console-filter-text input')! as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    act(() => {
      setter.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  it('filters rows by text substring (message + caller)', async () => {
    hoisted.logsQuery.mockResolvedValueOnce({
      Items: [
        entry(T0, 'info', 'started', 'boot.ts:1'),
        entry(T1, 'warn', 'slow', 'net.ts:9'),
        entry(T2, 'error', 'boom', 'net.ts:3'),
      ],
      Truncated: false,
    })
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })

    setInput('net')
    const rows = container.querySelectorAll('.app-console-row')
    expect(rows.length).toBe(2)
    expect(rows[0]!.querySelector('.app-console-msg')!.textContent).toBe('slow')
    expect(rows[1]!.querySelector('.app-console-msg')!.textContent).toBe('boom')
  })

  it('shows the filtered-empty hint when no rows match', async () => {
    hoisted.logsQuery.mockResolvedValueOnce({
      Items: [entry(T0, 'info', 'hello'), entry(T1, 'error', 'world')],
      Truncated: false,
    })
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })

    setInput('nope')
    expect(container.textContent).toContain('terminalPanel.appConsole.empty.filtered')
    expect(container.querySelectorAll('.app-console-row').length).toBe(0)
  })

  // ── 空态 / 降级 ──
  it('shows the degraded guidance when the console source is unavailable', async () => {
    hoisted.logsQuery.mockRejectedValueOnce(
      new Error('workspace.logs_query: console log source not available'),
    )
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })

    expect(container.textContent).toContain('terminalPanel.appConsole.empty.title')
    expect(container.textContent).toContain('terminalPanel.appConsole.empty.hint')
    expect(container.querySelectorAll('.app-console-row').length).toBe(0)
  })

  it('shows the empty state when there are no console entries', async () => {
    hoisted.logsQuery.mockResolvedValueOnce({ Items: [], Truncated: false })
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })
    expect(container.textContent).toContain('terminalPanel.appConsole.empty.title')
    expect(container.querySelectorAll('.app-console-row').length).toBe(0)
  })

  it('shows a retryable error state on a generic failure', async () => {
    hoisted.logsQuery.mockRejectedValueOnce(new Error('rpc unavailable'))
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })
    expect(container.textContent).toContain('terminalPanel.appConsole.error')
    expect(container.textContent).toContain('rpc unavailable')

    // retry refetches and now succeeds
    hoisted.logsQuery.mockResolvedValueOnce({ Items: [entry(T0, 'info', 'ok')], Truncated: false })
    await act(async () => { (container.querySelector('.app-console-retry') as HTMLButtonElement).click() })
    expect(container.querySelectorAll('.app-console-row').length).toBe(1)
  })

  // ── 轮询 ──
  it('polls for new logs and appends deduped entries', async () => {
    hoisted.logsQuery.mockResolvedValueOnce({ Items: [entry(T0, 'info', 'a')], Truncated: false })
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} active />) })
    expect(container.querySelectorAll('.app-console-row').length).toBe(1)

    // poll returns the existing entry plus a new one → append + dedupe
    hoisted.logsQuery.mockResolvedValueOnce({
      Items: [entry(T0, 'info', 'a'), entry(T1, 'info', 'b')],
      Truncated: false,
    })
    await act(async () => { vi.advanceTimersByTime(3000) })
    expect(hoisted.logsQuery).toHaveBeenCalledTimes(2)
    expect(lastCall()![1]).toEqual({ Source: 'console', Limit: 200 })
    const rows = container.querySelectorAll('.app-console-row')
    expect(rows.length).toBe(2)
    expect(rows[1]!.querySelector('.app-console-msg')!.textContent).toBe('b')
  })

  it('pauses polling when active=false', async () => {
    hoisted.logsQuery.mockResolvedValueOnce({ Items: [entry(T0, 'info', 'a')], Truncated: false })
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} active={false} />) })
    const callsAfterMount = hoisted.logsQuery.mock.calls.length
    await act(async () => { vi.advanceTimersByTime(9000) })
    expect(hoisted.logsQuery.mock.calls.length).toBe(callsAfterMount)
  })

  it('stops polling on unmount', async () => {
    hoisted.logsQuery.mockResolvedValueOnce({ Items: [entry(T0, 'info', 'a')], Truncated: false })
    await act(async () => { root.render(<AppConsoleTab tab={{ id: "t1", type: "app-console" }} />) })
    const callsBefore = hoisted.logsQuery.mock.calls.length
    act(() => root.unmount())
    await act(async () => { vi.advanceTimersByTime(9000) })
    expect(hoisted.logsQuery.mock.calls.length).toBe(callsBefore)
  })
})
