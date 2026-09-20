import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useCallback } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import * as projectClient from '../../../gen-clients/project/client'
import * as browserManager from '../../../application/browser-manager'
import type { BrowserManagerEvent } from '../../../gen-types/browser'
import { parsePrefixToken, parseCardMention, parseFileMention, parseAgentMention, parseBrowserMention, formatCardLinks, formatFileRefs, rankMentionResults, rankFileMentionResults, rankBrowserMentionResults, useCardMention, useFileMention, useBrowserMention, MENTION_SEARCH_DEBOUNCE_MS, type UseCardMentionResult, type UseFileMentionResult, type UseBrowserMentionResult } from './useCardMention'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/project/client', () => ({
  wikiListCards: vi.fn(),
  glob: vi.fn(),
}))
vi.mock('../../../application/browser-manager', () => ({
  listBrowserWindows: vi.fn(),
  onBrowserManagerEvent: vi.fn(() => () => {}),
}))

const mockCard = (Id: string, overrides?: Partial<Record<string, unknown>>) => ({
  Id,
  Type: 'wiki',
  Source: 'project',
  Storage: 'file',
  Visibility: 'public',
  Tags: [],
  List: [],
  Created: '',
  Modified: '',
  Protected: false,
  Editable: true,
  Deletable: true,
  Raw: '',
  ...overrides,
})

describe('parseCardMention', () => {
  it('detects a leading # token', () => {
    expect(parseCardMention('#card')).toEqual({ start: 0, query: 'card' })
  })

  it('detects # at a token boundary after whitespace', () => {
    expect(parseCardMention('hello #card')).toEqual({ start: 6, query: 'card' })
  })

  it('prefers the trailing open # token over an earlier closed one', () => {
    // The earlier '#foo bar' is closed by the space; the trailing '#baz' wins.
    expect(parseCardMention('#foo bar #baz')).toEqual({ start: 9, query: 'baz' })
  })

  it('treats a bare # as an active mention with an empty query', () => {
    expect(parseCardMention('#')).toEqual({ start: 0, query: '' })
  })

  it('returns null for an embedded # that is not at a token boundary', () => {
    expect(parseCardMention('a#b')).toBeNull()
  })

  it('returns null once the # token is closed by trailing whitespace', () => {
    expect(parseCardMention('#card ')).toBeNull()
    expect(parseCardMention('hello #card world')).toBeNull()
  })

  it('returns null when there is no # at all', () => {
    expect(parseCardMention('hello world')).toBeNull()
    expect(parseCardMention('')).toBeNull()
  })

  it('does not trigger on @ tokens (those are file mentions)', () => {
    expect(parseCardMention('@file')).toBeNull()
  })
})

describe('parseFileMention', () => {
  it('detects a leading $ token', () => {
    expect(parseFileMention('$main.go')).toEqual({ start: 0, query: 'main.go' })
  })

  it('detects $ at a token boundary after whitespace', () => {
    expect(parseFileMention('see $main.go')).toEqual({ start: 4, query: 'main.go' })
  })

  it('treats a bare $ as an active mention with an empty query', () => {
    expect(parseFileMention('$')).toEqual({ start: 0, query: '' })
  })

  it('returns null for an embedded $ that is not at a token boundary', () => {
    expect(parseFileMention('a$b')).toBeNull()
  })

  it('returns null once the $ token is closed by trailing whitespace', () => {
    expect(parseFileMention('$main.go ')).toBeNull()
  })

  it('does not trigger on # tokens (those are card mentions)', () => {
    expect(parseFileMention('#card')).toBeNull()
  })

  it('does not trigger on @ tokens (those are agent mentions)', () => {
    expect(parseFileMention('@agent')).toBeNull()
  })
})

describe('parseAgentMention', () => {
  it('detects a leading @ token', () => {
    expect(parseAgentMention('@alpha')).toEqual({ start: 0, query: 'alpha' })
  })

  it('treats a bare @ as an active mention with an empty query', () => {
    expect(parseAgentMention('@')).toEqual({ start: 0, query: '' })
  })

  it('does not trigger on $ tokens (those are file mentions)', () => {
    expect(parseAgentMention('$main.go')).toBeNull()
  })
})

describe('parsePrefixToken', () => {
  it('escapes regex metacharacters in the prefix', () => {
    expect(parsePrefixToken('hello *query', '*')).toEqual({ start: 6, query: 'query' })
  })
})

describe('useCardMention', () => {
  let container: HTMLDivElement
  let root: Root
  let captured: UseCardMentionResult | null = null

  function TestComponent({ enabled, query, pid }: { enabled: boolean; query: string; pid: string }) {
    const state = useCardMention(enabled, query, pid)
    const capture = useCallback(() => {
      captured = state
    }, [state])
    return <button type="button" onClick={capture}>capture</button>
  }

  const render = (enabled: boolean, query: string, pid = 'proj-1') => {
    act(() => {
      root.render(<TestComponent enabled={enabled} query={query} pid={pid} />)
    })
    // surface initial render state via the capture button
    act(() => {
      container.querySelector('button')!.click()
    })
  }

  beforeEach(() => {
    vi.useFakeTimers()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    captured = null
    vi.mocked(projectClient.wikiListCards).mockReset()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.useRealTimers()
  })

  it('does not search while disabled', () => {
    render(false, 'card')
    expect(projectClient.wikiListCards).not.toHaveBeenCalled()
    expect(captured!.results).toEqual([])
    expect(captured!.loading).toBe(false)
  })

  it('debounces and returns results when enabled', async () => {
    const cards = [mockCard('Alpha'), mockCard('Beta')]
    vi.mocked(projectClient.wikiListCards).mockResolvedValue({ Cards: cards, Total: 2, Tree: '', Nodes: [] })

    render(true, 'al')
    expect(captured!.loading).toBe(true)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })

    expect(projectClient.wikiListCards).toHaveBeenCalledWith({}, { Flat: true, Query: 'al', OrderBy: '-modified', Limit: 10 }, { target: 'proj-1' })
    expect(captured!.results.map((c) => c.Id)).toEqual(['Alpha', 'Beta'])
    expect(captured!.loading).toBe(false)
  })

  it('requests latest-modified ordering so a bare # lists recently edited cards', async () => {
    vi.mocked(projectClient.wikiListCards).mockResolvedValue({ Cards: [], Total: 0, Tree: '', Nodes: [] })
    render(true, '')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    expect(projectClient.wikiListCards).toHaveBeenCalledWith({}, { Flat: true, Query: '', OrderBy: '-modified', Limit: 10 }, { target: 'proj-1' })
  })

  it('re-ranks backend results by match position for a non-empty query', async () => {
    const cards = [mockCard('MyAlpha'), mockCard('AlphaBeta')]
    vi.mocked(projectClient.wikiListCards).mockResolvedValue({ Cards: cards, Total: 2, Tree: '', Nodes: [] })
    render(true, 'alpha')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results.map((c) => c.Id)).toEqual(['AlphaBeta', 'MyAlpha'])
  })

  it('resets results when disabled after a search', async () => {
    vi.mocked(projectClient.wikiListCards).mockResolvedValue({ Cards: [mockCard('X')], Total: 1, Tree: '', Nodes: [] })
    render(true, 'x')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results.map((c) => c.Id)).toEqual(['X'])

    render(false, 'x')
    expect(captured!.results).toEqual([])
    expect(captured!.loading).toBe(false)
  })

  it('filters agent cards out of the mention dropdown', async () => {
    const cards = [
      mockCard('agent:Coder', { Type: 'agent', Source: 'workspace', Storage: 'external', Tags: ['agent'] }),
      mockCard('RealCard'),
    ]
    vi.mocked(projectClient.wikiListCards).mockResolvedValue({ Cards: cards, Total: 2, Tree: '', Nodes: [] })
    render(true, '')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results.map((c) => c.Id)).toEqual(['RealCard'])
  })

  it('re-runs the search when the project changes', async () => {
    vi.mocked(projectClient.wikiListCards).mockResolvedValue({ Cards: [], Total: 0, Tree: '', Nodes: [] })
    render(true, 'x', 'proj-1')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    expect(projectClient.wikiListCards).toHaveBeenCalledTimes(1)

    render(true, 'x', 'proj-2')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    expect(projectClient.wikiListCards).toHaveBeenCalledTimes(2)
  })
})

describe('useFileMention', () => {
  let container: HTMLDivElement
  let root: Root
  let captured: UseFileMentionResult | null = null

  function TestComponent({ enabled, query, pid }: { enabled: boolean; query: string; pid: string }) {
    const state = useFileMention(enabled, query, pid)
    const capture = useCallback(() => {
      captured = state
    }, [state])
    return <button type="button" onClick={capture}>capture</button>
  }

  const render = (enabled: boolean, query: string, pid = 'proj-1') => {
    act(() => {
      root.render(<TestComponent enabled={enabled} query={query} pid={pid} />)
    })
    act(() => {
      container.querySelector('button')!.click()
    })
  }

  beforeEach(() => {
    vi.useFakeTimers()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    captured = null
    vi.mocked(projectClient.glob).mockReset()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.useRealTimers()
  })

  it('does not search while disabled', () => {
    render(false, 'main')
    expect(projectClient.glob).not.toHaveBeenCalled()
    expect(captured!.results).toEqual([])
  })

  it('does not search for a bare @ (empty query)', async () => {
    render(true, '')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    expect(projectClient.glob).not.toHaveBeenCalled()
    expect(captured!.results).toEqual([])
  })

  it('passes a bare term through (backend does recursive substring match)', async () => {
    vi.mocked(projectClient.glob).mockResolvedValue({ Files: ['cmd/main.go', 'pkg/domain.go'], NumFiles: 2, Truncated: false })
    render(true, 'main')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(projectClient.glob).toHaveBeenCalledWith({}, { Pattern: 'main', Order_by: '-modified' }, { target: 'proj-1' })
    expect(captured!.results).toEqual(['cmd/main.go', 'pkg/domain.go'])
  })

  it('wraps slash queries in wildcard framing', async () => {
    vi.mocked(projectClient.glob).mockResolvedValue({ Files: [], NumFiles: 0, Truncated: false })
    render(true, 'cmd/spo')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    expect(projectClient.glob).toHaveBeenCalledWith({}, { Pattern: '**/*cmd/spo*', Order_by: '-modified' }, { target: 'proj-1' })
  })

  it('strips glob metacharacters from the query', async () => {
    vi.mocked(projectClient.glob).mockResolvedValue({ Files: [], NumFiles: 0, Truncated: false })
    render(true, 'a*b?')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    expect(projectClient.glob).toHaveBeenCalledWith({}, { Pattern: 'ab', Order_by: '-modified' }, { target: 'proj-1' })
  })

  it('keeps the backend mtime order for equal match ranks (recency tie-break)', async () => {
    // Backend returns newest-mtime first; both basenames match at the same
    // position, so the stable rerank must preserve that recency order.
    vi.mocked(projectClient.glob).mockResolvedValue({ Files: ['fresh/Hit.ts', 'stale/Hit.ts'], NumFiles: 2, Truncated: false })
    render(true, 'hit')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results).toEqual(['fresh/Hit.ts', 'stale/Hit.ts'])
  })

  it('re-ranks results by basename match position', async () => {
    vi.mocked(projectClient.glob).mockResolvedValue({ Files: ['pkg/mymain.go', 'cmd/main.go'], NumFiles: 2, Truncated: false })
    render(true, 'main')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results).toEqual(['cmd/main.go', 'pkg/mymain.go'])
  })

  it('caps results at the mention search limit', async () => {
    const files = Array.from({ length: 20 }, (_, i) => `f${i}.go`)
    vi.mocked(projectClient.glob).mockResolvedValue({ Files: files, NumFiles: 20, Truncated: false })
    render(true, 'f')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results).toHaveLength(10)
  })

  it('returns empty results when the glob call fails', async () => {
    vi.mocked(projectClient.glob).mockRejectedValue(new Error('boom'))
    render(true, 'x')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results).toEqual([])
    expect(captured!.loading).toBe(false)
  })
})

describe('parseBrowserMention', () => {
  it('detects a leading % token', () => {
    expect(parseBrowserMention('%docs')).toEqual({ start: 0, query: 'docs' })
  })

  it('treats a bare % as an active mention with an empty query', () => {
    expect(parseBrowserMention('%')).toEqual({ start: 0, query: '' })
  })

  it('detects % at a token boundary after whitespace', () => {
    expect(parseBrowserMention('open %docs')).toEqual({ start: 5, query: 'docs' })
  })

  it('returns null for an embedded % that is not at a token boundary', () => {
    expect(parseBrowserMention('100%docs')).toBeNull()
  })

  it('does not trigger on @ tokens (those are agent mentions)', () => {
    expect(parseBrowserMention('@agent')).toBeNull()
  })
})

describe('rankBrowserMentionResults', () => {
  const items = [
    { instanceId: 'i1', name: 'My Docs', url: 'https://a.example.com' },
    { instanceId: 'i2', name: 'Docs Portal', url: 'https://b.example.com' },
    { instanceId: 'i3', name: 'Zebra', url: 'https://c.example.com' },
  ]

  it('returns the input untouched for an empty query', () => {
    expect(rankBrowserMentionResults(items, '')).toBe(items)
    expect(rankBrowserMentionResults(items, '  ')).toBe(items)
  })

  it('ranks earlier name matches ahead and keeps non-matches last in input order', () => {
    const ranked = rankBrowserMentionResults(items, 'docs')
    expect(ranked.map((b) => b.instanceId)).toEqual(['i2', 'i1', 'i3'])
  })
})

describe('useBrowserMention', () => {
  let container: HTMLDivElement
  let root: Root
  let captured: UseBrowserMentionResult | null = null
  let eventHandler: ((e: BrowserManagerEvent) => void) | null = null

  function TestComponent({ enabled, query }: { enabled: boolean; query: string }) {
    const state = useBrowserMention(enabled, query)
    const capture = useCallback(() => {
      captured = state
    }, [state])
    return <button type="button" onClick={capture}>capture</button>
  }

  const render = (enabled: boolean, query: string) => {
    act(() => {
      root.render(<TestComponent enabled={enabled} query={query} />)
    })
    act(() => {
      container.querySelector('button')!.click()
    })
  }

  const inst = (Id: string, over: Record<string, unknown> = {}) => ({
    Config: { Id, Name: `Browser ${Id}`, Url: `https://${Id}.example.com`, Mode: 'tab', ...over },
    Status: { Open: true, Url: `https://${Id}.example.com/live` },
  })

  beforeEach(() => {
    vi.useFakeTimers()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    captured = null
    eventHandler = null
    vi.mocked(browserManager.listBrowserWindows).mockReset()
    vi.mocked(browserManager.onBrowserManagerEvent).mockReset()
    vi.mocked(browserManager.onBrowserManagerEvent).mockImplementation((handler) => {
      eventHandler = handler
      return () => {}
    })
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.useRealTimers()
  })

  it('does not list instances while disabled', () => {
    render(false, 'doc')
    expect(browserManager.listBrowserWindows).not.toHaveBeenCalled()
    expect(captured!.results).toEqual([])
    expect(captured!.loading).toBe(false)
  })

  it('returns open tab-mode instances only', async () => {
    vi.mocked(browserManager.listBrowserWindows).mockResolvedValue([
      inst('open-tab'),
      { ...inst('closed-tab'), Status: { Open: false, Url: '' } },
      { ...inst('window-mode'), Config: { Id: 'window-mode', Name: 'Window', Url: '', Mode: 'window' } },
    ] as any)
    render(true, '')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results).toEqual([
      { instanceId: 'open-tab', name: 'Browser open-tab', url: 'https://open-tab.example.com/live' },
    ])
  })

  it('re-ranks by name match for a non-empty query', async () => {
    vi.mocked(browserManager.listBrowserWindows).mockResolvedValue([
      inst('a', { Name: 'My Docs' }),
      inst('b', { Name: 'Docs Portal' }),
    ] as any)
    render(true, 'docs')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results.map((b) => b.instanceId)).toEqual(['b', 'a'])
  })

  it('refreshes immediately when a browser-manager event arrives', async () => {
    vi.mocked(browserManager.listBrowserWindows).mockResolvedValue([] as any)
    render(true, '')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results).toEqual([])

    vi.mocked(browserManager.listBrowserWindows).mockResolvedValue([inst('new-tab')] as any)
    await act(async () => {
      eventHandler?.({} as BrowserManagerEvent)
      await Promise.resolve()
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results.map((b) => b.instanceId)).toEqual(['new-tab'])
  })

  it('returns empty results when the list call fails', async () => {
    vi.mocked(browserManager.listBrowserWindows).mockRejectedValue(new Error('boom'))
    render(true, 'x')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(MENTION_SEARCH_DEBOUNCE_MS + 50)
    })
    act(() => { container.querySelector('button')!.click() })
    expect(captured!.results).toEqual([])
    expect(captured!.loading).toBe(false)
  })
})

describe('rankMentionResults', () => {
  it('returns the input untouched for an empty query', () => {
    const cards = [mockCard('Beta'), mockCard('Alpha')]
    expect(rankMentionResults(cards, '')).toBe(cards)
    expect(rankMentionResults(cards, '   ')).toBe(cards)
  })

  it('ranks prefix matches ahead of later substring matches', () => {
    const cards = [mockCard('MyCard'), mockCard('CardTwo')]
    expect(rankMentionResults(cards, 'card').map((c) => c.Id)).toEqual(['CardTwo', 'MyCard'])
  })

  it('matches case-insensitively', () => {
    const cards = [mockCard('xALPHA'), mockCard('Alpha')]
    expect(rankMentionResults(cards, 'ALPHA').map((c) => c.Id)).toEqual(['Alpha', 'xALPHA'])
  })

  it('keeps the input order within the same rank (stable, recency tiebreak)', () => {
    const cards = [mockCard('MatchFirst'), mockCard('MatchLast')]
    expect(rankMentionResults(cards, 'match').map((c) => c.Id)).toEqual(['MatchFirst', 'MatchLast'])
  })

  it('does not mutate the input array', () => {
    const cards = [mockCard('MyCard'), mockCard('CardTwo')]
    rankMentionResults(cards, 'card')
    expect(cards.map((c) => c.Id)).toEqual(['MyCard', 'CardTwo'])
  })

  it('ranks stamp-less cards last even when their query match is better', () => {
    const stamped = { ...mockCard('xAlpha'), Created: '2026-01-01T00:00:00Z' }
    const timeless = mockCard('Alpha')
    expect(rankMentionResults([stamped, timeless], 'alpha').map((c) => c.Id)).toEqual(['xAlpha', 'Alpha'])
  })

  it('keeps relative order among stamp-less cards (stable)', () => {
    const a = mockCard('Alpha')
    const b = mockCard('xAlpha')
    const stamped = { ...mockCard('zzAlpha'), Modified: '2026-01-01T00:00:00Z' }
    expect(rankMentionResults([a, stamped, b], 'alpha').map((c) => c.Id)).toEqual(['zzAlpha', 'Alpha', 'xAlpha'])
  })
})

describe('rankFileMentionResults', () => {
  it('returns the input untouched for an empty query', () => {
    const paths = ['b.go', 'a.go']
    expect(rankFileMentionResults(paths, '')).toBe(paths)
  })

  it('ranks basename prefix matches ahead of later matches', () => {
    const paths = ['pkg/mymain.go', 'cmd/main.go']
    expect(rankFileMentionResults(paths, 'main')).toEqual(['cmd/main.go', 'pkg/mymain.go'])
  })

  it('matches against the basename, not directory segments', () => {
    const paths = ['main/util.go', 'cmd/xmain.go']
    expect(rankFileMentionResults(paths, 'main')).toEqual(['cmd/xmain.go', 'main/util.go'])
  })
})

describe('formatCardLinks', () => {
  it('returns the text unchanged when no card IDs are provided', () => {
    expect(formatCardLinks('hello world', [])).toBe('hello world')
  })

  it('appends a single card link on a new line', () => {
    expect(formatCardLinks('hello world', ['ProjectSummary'])).toBe('hello world\n[[ProjectSummary]]')
  })

  it('appends multiple card links space-joined on one line', () => {
    expect(formatCardLinks('hello', ['Alpha', 'Beta'])).toBe('hello\n[[Alpha]] [[Beta]]')
  })

  it('returns only the links when the text is empty', () => {
    expect(formatCardLinks('', ['Alpha', 'Beta'])).toBe('[[Alpha]] [[Beta]]')
  })

  it('returns only the links when the text is whitespace', () => {
    expect(formatCardLinks('   ', ['Alpha'])).toBe('[[Alpha]]')
  })

  it('trims trailing whitespace from the text before appending', () => {
    expect(formatCardLinks('hello   ', ['Alpha'])).toBe('hello\n[[Alpha]]')
  })

  it('filters out empty card IDs', () => {
    expect(formatCardLinks('hello', ['Alpha', '', 'Beta'])).toBe('hello\n[[Alpha]] [[Beta]]')
  })
})

describe('formatFileRefs', () => {
  it('returns the text unchanged when no paths are provided', () => {
    expect(formatFileRefs('hello world', [])).toBe('hello world')
  })

  it('appends backticked paths on a new line', () => {
    expect(formatFileRefs('hello', ['cmd/main.go'])).toBe('hello\n`cmd/main.go`')
  })

  it('space-joins multiple paths', () => {
    expect(formatFileRefs('hello', ['a.go', 'b.go'])).toBe('hello\n`a.go` `b.go`')
  })

  it('returns only the refs when the text is empty', () => {
    expect(formatFileRefs('', ['a.go'])).toBe('`a.go`')
  })

  it('filters out empty paths', () => {
    expect(formatFileRefs('hello', ['a.go', ''])).toBe('hello\n`a.go`')
  })
})
