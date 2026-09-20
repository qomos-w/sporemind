import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AIConversationPage } from './AIConversationPage'
import { I18nProvider } from '../../../i18n'
import type { TurnEnvelope } from '../model/frame-types'

const saveScrollAnchor = vi.fn()
const restoreScrollAnchor = vi.fn()

vi.mock('../hooks/useScrollLock', () => ({
  useScrollLock: () => ({
    canScrollDown: false,
    scrollToBottom: vi.fn(),
    saveScrollAnchor,
    restoreScrollAnchor,
  }),
}))

vi.mock('../hooks/useSlotRegistry', () => ({
  useSlotRegistry: () => ({ slots: new Map(), registerFrames: vi.fn(), clear: vi.fn() }),
}))

vi.mock('../hooks/useViewportAware', () => ({
  clearViewportHeightCache: vi.fn(),
  ViewportObserverProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useViewportSlot: () => ({ ref: { current: null }, isVisible: true, cachedHeight: 0 }),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../panels/git-store', () => ({
  gitStore: {
    subscribe: vi.fn(() => () => {}),
    getVersion: vi.fn(() => 0),
    refresh: vi.fn(),
    startMountListener: vi.fn(),
    loadProjects: vi.fn(async () => undefined),
    state: { projects: [] },
  },
}))

vi.mock('./parts/file-reference', () => ({
  FileReferenceProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('../context/AIShellContext', async () => {
  const React = await import('react')
  const cache = new Map<string, unknown>()
  const stub: Record<string, unknown> = new Proxy({}, {
    get: (_t, prop) => {
      if (typeof prop !== 'string') return undefined
      if (!cache.has(prop)) cache.set(prop, vi.fn())
      return cache.get(prop)
    },
  })
  return {
    AIShellContext: React.createContext(stub),
    useAIShellContext: () => stub,
  }
})

const env: TurnEnvelope = {
  id: 'env-1',
  role: 'assistant',
  frames: [],
  timestamp: '2026-01-01T00:00:00Z',
  completed: true,
  metadata: { turnId: 'turn-1' },
} as unknown as TurnEnvelope

function renderPage(props: Partial<React.ComponentProps<typeof AIConversationPage>>) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root: Root = createRoot(host)
  const base: React.ComponentProps<typeof AIConversationPage> = {
    envelopes: [env],
    isStreaming: false,
    timelineLoading: false,
    activeConversationId: 'conv-1',
    hasMoreHistory: true,
    loadingMoreHistory: false,
    loadOlderTurns: vi.fn(),
    ...props,
  }
  const rerender = (next: Partial<React.ComponentProps<typeof AIConversationPage>>) => {
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <AIConversationPage {...base} {...next} />
        </I18nProvider>,
      )
    })
  }
  rerender({})
  return { host, root, rerender, base }
}

describe('AIConversationPage load-more history', () => {
  let container: HTMLDivElement | null = null

  beforeEach(() => {
    vi.useFakeTimers()
    saveScrollAnchor.mockClear()
    restoreScrollAnchor.mockClear()
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(() => {
    vi.useRealTimers()
    container?.remove()
    container = null
  })

  it('re-arms the button when a click does not start a load (manager no-op)', () => {
    const loadOlderTurns = vi.fn()
    const page = renderPage({ loadOlderTurns })
    const btn = page.host.querySelector('.ai-load-more-btn') as HTMLButtonElement
    expect(btn).toBeTruthy()

    // First click: manager no-ops (e.g. no anchorable envelope) —
    // loadingMoreHistory never flips.
    act(() => { btn.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(loadOlderTurns).toHaveBeenCalledTimes(1)
    expect(saveScrollAnchor).toHaveBeenCalledTimes(1)

    // Before the fix the armed ref swallowed this second click forever.
    act(() => { vi.advanceTimersByTime(200) })
    act(() => { btn.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(loadOlderTurns).toHaveBeenCalledTimes(2)
  })

  it('keeps the ref armed while a load is in flight and restores the anchor after', () => {
    const loadOlderTurns = vi.fn()
    const page = renderPage({ loadOlderTurns })
    const btn = page.host.querySelector('.ai-load-more-btn') as HTMLButtonElement

    act(() => { btn.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    // Load started: prop flips true before the disarm timeout fires.
    page.rerender({ loadingMoreHistory: true })
    act(() => { vi.advanceTimersByTime(200) })
    // Clicks while loading are ignored (button disabled + armed ref).
    act(() => { btn.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(loadOlderTurns).toHaveBeenCalledTimes(1)

    // Load finished: anchor restored once, ref disarmed by the effect.
    page.rerender({ loadingMoreHistory: false })
    expect(restoreScrollAnchor).toHaveBeenCalledTimes(1)
    act(() => { btn.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(loadOlderTurns).toHaveBeenCalledTimes(2)
  })
})
