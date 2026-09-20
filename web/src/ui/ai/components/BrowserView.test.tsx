import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { BrowserView } from './BrowserView'
import { I18nProvider } from '../../../i18n'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const bridge = vi.hoisted(() => ({
  openBrowserSession: vi.fn(() => Promise.resolve()),
  closeBrowserSession: vi.fn(() => Promise.resolve()),
  navigateBrowserSession: vi.fn(() => Promise.resolve()),
  goBackBrowserWindow: vi.fn(() => Promise.resolve()),
  goForwardBrowserWindow: vi.fn(() => Promise.resolve()),
  updateBrowserWindow: vi.fn(() => Promise.resolve()),
  setBrowserWindowVisible: vi.fn(() => Promise.resolve()),
  splashDone: vi.fn(() => Promise.resolve()),
}))

vi.mock('../../../application/wails-bridge', () => bridge)

const handlers = vi.hoisted(() => new Map<string, ((...args: unknown[]) => void)[]>())
const onWailsEvent = vi.hoisted(
  () =>
    vi.fn((name: string, callback: (...args: unknown[]) => void) => {
      const list = handlers.get(name) ?? []
      list.push(callback)
      handlers.set(name, list)
      return () => {
        const arr = handlers.get(name)
        if (!arr) return
        const i = arr.indexOf(callback)
        if (i >= 0) arr.splice(i, 1)
      }
    }) as any
)

const emitWailsEvent = (name: string, data?: unknown) => {
  const ev = { data }
  handlers.get(name)?.forEach((cb) => cb(ev))
}

// Emit the normalized navigation state (contract §5 main event) that the
// frontend projects loading / address-bar / title / error from exclusively.
const emitState = (
  data: { status: string; confirmedURL?: string; attemptedURL?: string; title?: string; error?: string },
  id = 'session-1',
) => {
  emitWailsEvent(`browser:state:${id}`, data)
}

vi.mock('../../../application/wails-runtime', () => ({ onWailsEvent }))

vi.mock('../../../application/browser-manager', () => ({
  listBrowserWindows: vi.fn(() => Promise.resolve([])),
  openBrowserWindow: vi.fn(() => Promise.resolve({ Instance: null })),
  closeBrowserWindow: vi.fn(() => Promise.resolve()),
  updateBrowserWindow: vi.fn(() => Promise.resolve()),
  onBrowserManagerEvent: vi.fn(() => () => {}),
  updateBrowserWindowUrl: vi.fn(() => Promise.resolve()),
  openBrowserInstance: vi.fn(() => Promise.resolve()),
  exportBrowserCookies: vi.fn(() => Promise.resolve({})),
  importBrowserCookies: vi.fn(() => Promise.resolve(0)),
  effectiveProxyMode: (cfg: { Proxy: string; ProxyMode: string }) =>
    cfg.ProxyMode === 'none' ? 'none'
      : cfg.ProxyMode === 'custom' ? (cfg.Proxy ? 'custom' : 'system')
        : (cfg.Proxy ? 'custom' : 'system'),
}))

describe('BrowserView', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    handlers.clear()
    vi.clearAllMocks()
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
    handlers.clear()
  })

  const renderView = async (props: Partial<React.ComponentProps<typeof BrowserView>> = {}) => {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <BrowserView sessionId="session-1" kind="global" initialUrl="https://example.com" {...props} />
        </I18nProvider>
      )
    })
  }

  const inputValue = () =>
    (container.querySelector('.browser-address-input') as HTMLInputElement | null)?.value

  // Flush the mount-time open() async chain (await splashDone /
  // openBrowserSession) so its calls do not pollute navigate/reload asserts.
  const flushMount = () => act(async () => { await new Promise(r => setTimeout(r, 10)) })

  // Drive a session to the loaded + shown state (loading=false, shown=true) so
  // the loading overlay mirrors the `loading` state exactly.
  const completeInitialLoad = async (url = 'https://example.com', id = 'session-1') => {
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: url }, id)
      emitWailsEvent(`browser:shown:${id}`)
    })
    expect(container.querySelector('.browser-loading')).toBeNull()
  }

  // ---- basic wiring ----

  it('opens a new browser tab when browser:new-tab is received', async () => {
    const onOpenBrowserTab = vi.fn()
    await renderView({ onOpenBrowserTab })
    await act(async () => {
      emitWailsEvent('browser:new-tab:session-1', 'https://new.example.com')
    })
    expect(onOpenBrowserTab).toHaveBeenCalledWith('https://new.example.com')
  })

  it('forwards the favicon from browser:page-info to onFaviconChange', async () => {
    const onFaviconChange = vi.fn()
    await renderView({ onFaviconChange })
    await act(async () => {
      emitWailsEvent('browser:page-info:session-1', {
        u: 'https://example.com',
        ic: 'data:image/png;base64,AAA',
        bg: '#ffffff',
      })
    })
    expect(onFaviconChange).toHaveBeenCalledWith('data:image/png;base64,AAA')
  })

  it('ignores page-info favicon for browser-internal URLs', async () => {
    const onFaviconChange = vi.fn()
    await renderView({ onFaviconChange })
    await act(async () => {
      emitWailsEvent('browser:page-info:session-1', {
        u: 'chrome-error://chromewebdata/',
        ic: 'data:image/png;base64,AAA',
      })
    })
    expect(onFaviconChange).not.toHaveBeenCalled()
  })

  it('opens the right browser window on mount with the initial URL', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    expect(bridge.openBrowserSession).toHaveBeenCalledWith(
      'session-1',
      'global',
      'https://example.com',
      expect.any(Number),
      expect.any(Number),
      expect.any(Number),
      expect.any(Number),
    )
  })

  // ---- address bar = confirmedURL (no optimistic write) ----

  it('updates the address bar from browser:state.confirmedURL', async () => {
    await renderView()
    expect(inputValue()).toBe('https://example.com')
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://sporemind.dev' })
    })
    expect(inputValue()).toBe('https://sporemind.dev')
  })

  it('ignores browser-internal URLs in state.confirmedURL', async () => {
    await renderView()
    expect(inputValue()).toBe('https://example.com')
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'chrome-error://chromewebdata/' })
    })
    expect(inputValue()).toBe('https://example.com')
  })

  it('updates the address bar for file:// URLs in state.confirmedURL', async () => {
    await renderView()
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'file:///D:/web/page.html' })
    })
    expect(inputValue()).toBe('file:///D:/web/page.html')
  })

  it('shows the attempted URL in the address bar when a navigation fails', async () => {
    await renderView()
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://example.com' })
    })
    // Failed navigation to a dead local URL: the webview sits on the error
    // page of the attempted URL, so the address bar must render it.
    await act(async () => {
      emitState({ status: 'failed', confirmedURL: 'https://example.com', attemptedURL: 'http://localhost:9999/', error: 'web_error_-2147012867' })
    })
    expect(inputValue()).toBe('http://localhost:9999/')
    // Error indicator is surfaced alongside.
    expect(container.querySelector('.browser-error-indicator')).not.toBeNull()
    // Recovery: a later successful navigation re-binds the address bar to
    // confirmedURL and clears the error.
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://sporemind.dev' })
    })
    expect(inputValue()).toBe('https://sporemind.dev')
    expect(container.querySelector('.browser-error-indicator')).toBeNull()
  })

  it('ignores browser-internal attempted URLs on failure', async () => {
    await renderView()
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://example.com' })
    })
    await act(async () => {
      emitState({ status: 'failed', confirmedURL: 'https://example.com', attemptedURL: 'chrome-error://chromewebdata/', error: 'navigation_failed' })
    })
    expect(inputValue()).toBe('https://example.com')
  })

  it('normalizes a hand-typed two-slash file URL on submit', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad()
    bridge.navigateBrowserSession.mockClear()

    const addr = container.querySelector('.browser-address-input') as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(addr, 'file://D:/web/page.html')
      addr.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      ;(container.querySelector('[title="Go"]') as HTMLButtonElement).click()
    })

    expect(bridge.navigateBrowserSession).toHaveBeenCalledWith('session-1', 'file:///D:/web/page.html')
  })

  it('converts a bare Windows drive path to a file URL on submit', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad()
    bridge.navigateBrowserSession.mockClear()

    const addr = container.querySelector('.browser-address-input') as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(addr, 'D:\\web\\page.html')
      addr.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      ;(container.querySelector('[title="Go"]') as HTMLButtonElement).click()
    })

    expect(bridge.navigateBrowserSession).toHaveBeenCalledWith('session-1', 'file:///D:/web/page.html')
  })

  it('reloads the last confirmed URL after receiving an internal URL', async () => {
    await renderView()
    // confirmedURL rejected (internal) → address bar keeps the initial URL.
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'chrome-error://chromewebdata/' })
    })
    const reloadBtn = container.querySelector('[title="Reload"]') as HTMLButtonElement
    await act(async () => {
      reloadBtn.click()
    })
    expect(bridge.navigateBrowserSession).toHaveBeenCalledWith('session-1', 'https://example.com')
  })

  // ---- history-state ----

  it('enables back/forward buttons from browser:history-state', async () => {
    await renderView()
    const backBtn = () => container.querySelector('[title="Back"]') as HTMLButtonElement | null
    const forwardBtn = () => container.querySelector('[title="Forward"]') as HTMLButtonElement | null

    expect(backBtn()?.disabled).toBe(true)
    expect(forwardBtn()?.disabled).toBe(true)

    await act(async () => {
      emitWailsEvent('browser:history-state:session-1', { canBack: true, canForward: true })
    })

    expect(backBtn()?.disabled).toBe(false)
    expect(forwardBtn()?.disabled).toBe(false)

    await act(async () => {
      backBtn()?.click()
    })
    expect(bridge.goBackBrowserWindow).toHaveBeenCalledWith('session-1')

    await act(async () => {
      forwardBtn()?.click()
    })
    expect(bridge.goForwardBrowserWindow).toHaveBeenCalledWith('session-1')
  })

  // ---- loading = (status == loading) ----

  it('shows the loading overlay when state status is loading, clears on loaded + shown', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()

    // Before any state event: loading is false but shown is false, so the
    // overlay covers the frame (overlay = loading || !shown).
    expect(container.querySelector('.browser-loading')).not.toBeNull()
    // Navigation must NOT toggle native-window visibility.
    expect(bridge.setBrowserWindowVisible).not.toHaveBeenCalledWith('session-1', false)

    // state{loading} from NavigationStarting.
    await act(async () => {
      emitState({ status: 'loading', confirmedURL: '' })
    })
    expect(container.querySelector('.browser-loading')).not.toBeNull()

    // state{loaded} clears loading…
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://example.com' })
    })
    // …but the overlay stays until the native window is shown (!shown).
    expect(container.querySelector('.browser-loading')).not.toBeNull()

    await act(async () => {
      emitWailsEvent('browser:shown:session-1')
    })
    expect(container.querySelector('.browser-loading')).toBeNull()
  })

  it('does not borrow the shown event to clear loading', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad()

    // Issue a navigation: the backend pushes state{loading}.
    await act(async () => {
      emitState({ status: 'loading', confirmedURL: 'https://example.com' })
    })
    expect(container.querySelector('.browser-loading')).not.toBeNull()

    // A shown event must NOT clear loading (shown is a visibility signal only).
    await act(async () => {
      emitWailsEvent('browser:shown:session-1')
    })
    expect(container.querySelector('.browser-loading')).not.toBeNull()

    // Only state{loaded} clears loading.
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://other.example.com' })
    })
    expect(container.querySelector('.browser-loading')).toBeNull()
  })

  it('clears loading on a navigation failure (state status failed) and surfaces the error', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad()

    await act(async () => {
      emitState({ status: 'loading', confirmedURL: 'https://example.com' })
    })
    expect(container.querySelector('.browser-loading')).not.toBeNull()

    await act(async () => {
      emitState({ status: 'failed', confirmedURL: 'https://example.com', error: 'net::ERR_FAILED' })
    })
    expect(container.querySelector('.browser-loading')).toBeNull()
    expect(container.querySelector('.browser-error-indicator')).not.toBeNull()
  })

  // ---- visibility lifecycle: owned by AIShellLayout state machine ----

  it('does not hide the native window on unmount', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    bridge.setBrowserWindowVisible.mockClear()

    await act(async () => {
      root.unmount()
    })

    expect(bridge.setBrowserWindowVisible).not.toHaveBeenCalled()
  })

  it('does not hide the native window when the frame has a zero rect', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad()
    bridge.setBrowserWindowVisible.mockClear()
    bridge.updateBrowserWindow.mockClear()

    // Simulate the frame becoming 0x0 (e.g. during a remount / layout transition).
    const frame = container.querySelector('.browser-frame') as HTMLDivElement | null
    if (frame) {
      frame.getBoundingClientRect = () => ({ x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, bottom: 0, right: 0, toJSON: () => ({}) } as DOMRect)
    }
    window.dispatchEvent(new Event('resize'))
    await flushMount()

    expect(bridge.setBrowserWindowVisible).not.toHaveBeenCalled()
    expect(bridge.updateBrowserWindow).not.toHaveBeenCalled()
  })

  // ---- in-page navigation enters loading via state ----

  it('enters loading on a page-initiated navigation (state without a host command)', async () => {
    // Contract §2: page-initiated navigations (links/forms/scripts) also bump
    // status to loading via NavigationStarting. The frontend must show the
    // loading indicator even though no host command was issued.
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad()
    expect(container.querySelector('.browser-loading')).toBeNull()

    // A link click on the page triggers NavigationStarting → state{loading}.
    bridge.navigateBrowserSession.mockClear()
    await act(async () => {
      emitState({ status: 'loading', confirmedURL: 'https://example.com' })
    })
    expect(container.querySelector('.browser-loading')).not.toBeNull()
    expect(bridge.navigateBrowserSession).not.toHaveBeenCalled()

    // The page finishes loading.
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://example.com/page2' })
    })
    expect(container.querySelector('.browser-loading')).toBeNull()
    // confirmedURL projected to the address bar.
    expect(inputValue()).toBe('https://example.com/page2')
  })

  // ---- navigation commands don't optimistically write state ----

  it('navigates from the address bar without optimistic state write', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad()
    bridge.setBrowserWindowVisible.mockClear()
    bridge.navigateBrowserSession.mockClear()

    const addr = container.querySelector('.browser-address-input') as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(addr, 'https://new.example.com')
      addr.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      ;(container.querySelector('[title="Go"]') as HTMLButtonElement).click()
    })

    expect(bridge.navigateBrowserSession).toHaveBeenCalledWith('session-1', 'https://new.example.com')
    // Regression: navigation must NOT flip wanted=false.
    expect(bridge.setBrowserWindowVisible).not.toHaveBeenCalled()
    // No optimistic loading: loading stays false until state{loading} arrives.
    // (shown is true → overlay hidden.)
    expect(container.querySelector('.browser-loading')).toBeNull()

    // Backend confirms the navigation is loading.
    await act(async () => {
      emitState({ status: 'loading', confirmedURL: 'https://example.com' })
    })
    expect(container.querySelector('.browser-loading')).not.toBeNull()

    // Backend confirms the navigation completed.
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://new.example.com' })
    })
    expect(container.querySelector('.browser-loading')).toBeNull()
    expect(inputValue()).toBe('https://new.example.com')
  })

  it('reload does not strand the window hidden', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad()
    bridge.setBrowserWindowVisible.mockClear()
    bridge.navigateBrowserSession.mockClear()

    await act(async () => {
      ;(container.querySelector('[title="Reload"]') as HTMLButtonElement).click()
    })

    expect(bridge.navigateBrowserSession).toHaveBeenCalledWith('session-1', 'https://example.com')
    // Regression: reload must not flip wanted=false.
    expect(bridge.setBrowserWindowVisible).not.toHaveBeenCalled()

    // Backend confirms loading then loaded.
    await act(async () => {
      emitState({ status: 'loading', confirmedURL: 'https://example.com' })
    })
    expect(container.querySelector('.browser-loading')).not.toBeNull()

    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://example.com' })
    })
    expect(container.querySelector('.browser-loading')).toBeNull()
  })

  it('reload uses the confirmed URL from state, not the initial URL', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad('https://changed.example.com')
    bridge.navigateBrowserSession.mockClear()

    await act(async () => {
      ;(container.querySelector('[title="Reload"]') as HTMLButtonElement).click()
    })
    expect(bridge.navigateBrowserSession).toHaveBeenCalledWith('session-1', 'https://changed.example.com')
  })

  // ---- global / independent routing distinction ----

  it('persists the confirmed URL for an independent tab', async () => {
    const { updateBrowserWindowUrl } = await import('../../../application/browser-manager')
    vi.mocked(updateBrowserWindowUrl).mockClear()
    await renderView({ kind: 'independent', sessionId: 'inst-1', initialUrl: 'https://example.com' })
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://changed.example.com' }, 'inst-1')
    })
    expect(updateBrowserWindowUrl).toHaveBeenCalledWith('inst-1', 'https://changed.example.com')
  })

  it('navigates to the settings page (Config.Url) from the Home button', async () => {
    const { listBrowserWindows } = await import('../../../application/browser-manager')
    vi.mocked(listBrowserWindows).mockResolvedValue([
      {
        Config: {
          Id: 'inst-1', Name: 'work', Url: 'https://home.example.com', Mode: 'tab',
          State: { Url: 'https://home.example.com', Title: 'work' },
        },
        Status: { Id: 'inst-1', Open: true, Url: 'https://home.example.com', Title: 'work' },
      },
    ] as never)
    bridge.navigateBrowserSession.mockClear()

    await renderView({ kind: 'independent', sessionId: 'inst-1', initialUrl: 'https://example.com' })
    await flushMount()

    const home = container.querySelector('[title="Back to settings page"]') as HTMLButtonElement | null
    expect(home).not.toBeNull()
    await act(async () => {
      home!.click()
    })
    expect(bridge.navigateBrowserSession).toHaveBeenCalledWith('inst-1', 'https://home.example.com')
  })

  it('does not render the Home button for global tabs', async () => {
    await renderView({ kind: 'global', initialUrl: 'https://example.com' })
    await flushMount()
    expect(container.querySelector('[title="Back to settings page"]')).toBeNull()
  })

  it('does not persist the URL for a global tab', async () => {
    const { updateBrowserWindowUrl } = await import('../../../application/browser-manager')
    vi.mocked(updateBrowserWindowUrl).mockClear()
    await renderView({ kind: 'global', initialUrl: 'https://example.com' })
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://changed.example.com' })
    })
    expect(updateBrowserWindowUrl).not.toHaveBeenCalled()
  })

  it('projects the title from state for global tabs', async () => {
    const onTitleChange = vi.fn()
    await renderView({ onTitleChange })
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://example.com', title: 'My Page' })
    })
    expect(onTitleChange).toHaveBeenCalledWith('My Page')
  })

  // ---- first address-bar submit from an empty new tab ----

  it('creates the native session on the first submit from an empty global new tab', async () => {
    await renderView({ kind: 'global', initialUrl: '' })
    await flushMount()
    // Mount-time open() must have early-returned on the empty URL.
    expect(bridge.openBrowserSession).not.toHaveBeenCalled()

    bridge.openBrowserSession.mockClear()
    bridge.navigateBrowserSession.mockClear()

    const addr = container.querySelector('.browser-address-input') as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(addr, 'example.com')
      addr.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      ;(container.querySelector('[title="Go"]') as HTMLButtonElement).click()
    })

    // open() must create the session + navigate in one step.
    expect(bridge.openBrowserSession).toHaveBeenCalledWith(
      'session-1',
      'global',
      'https://example.com',
      expect.any(Number),
      expect.any(Number),
      expect.any(Number),
      expect.any(Number),
    )
    // navigate() must NOT be called — the session does not exist yet, so it
    // would silently no-op.
    expect(bridge.navigateBrowserSession).not.toHaveBeenCalled()

    // Backend confirms the navigation.
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://example.com' })
    })
    expect(inputValue()).toBe('https://example.com')
  })

  it('creates the native session on the first submit from an empty independent new tab', async () => {
    await renderView({ kind: 'independent', sessionId: 'inst-1', initialUrl: '' })
    await flushMount()
    expect(bridge.openBrowserSession).not.toHaveBeenCalled()

    bridge.openBrowserSession.mockClear()
    bridge.navigateBrowserSession.mockClear()

    const addr = container.querySelector('.browser-address-input') as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(addr, 'https://new.example.com')
      addr.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      ;(container.querySelector('[title="Go"]') as HTMLButtonElement).click()
    })

    expect(bridge.openBrowserSession).toHaveBeenCalledWith(
      'inst-1',
      'independent',
      'https://new.example.com',
      expect.any(Number),
      expect.any(Number),
      expect.any(Number),
      expect.any(Number),
    )
    expect(bridge.navigateBrowserSession).not.toHaveBeenCalled()

    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://new.example.com' }, 'inst-1')
    })
    expect(inputValue()).toBe('https://new.example.com')
  })

  it('uses navigate() for subsequent submits after the session is created', async () => {
    await renderView({ kind: 'global', initialUrl: '' })
    await flushMount()

    // First submit creates the session.
    const addr = container.querySelector('.browser-address-input') as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(addr, 'https://first.example.com')
      addr.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      ;(container.querySelector('[title="Go"]') as HTMLButtonElement).click()
    })
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://first.example.com' })
    })

    // Second submit: session now exists, so navigate() is used.
    bridge.openBrowserSession.mockClear()
    bridge.navigateBrowserSession.mockClear()
    await act(async () => {
      setter.call(addr, 'https://second.example.com')
      addr.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      ;(container.querySelector('[title="Go"]') as HTMLButtonElement).click()
    })

    expect(bridge.navigateBrowserSession).toHaveBeenCalledWith('session-1', 'https://second.example.com')
    expect(bridge.openBrowserSession).not.toHaveBeenCalled()
  })

  // ---- redirect: the post-redirect final URL is projected ----

  it('projects the post-redirect final URL after a redirect', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad()

    // Backend reports the navigation started loading for the short URL.
    await act(async () => {
      emitState({ status: 'loading', confirmedURL: 'https://example.com' })
    })
    expect(container.querySelector('.browser-loading')).not.toBeNull()

    // The redirect resolves to a different final URL.
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://example.com/final' })
    })
    expect(container.querySelector('.browser-loading')).toBeNull()
    // The address bar must show the post-redirect URL, not the requested one.
    expect(inputValue()).toBe('https://example.com/final')
  })

  // ---- back/forward full flow ----

  it('drives back navigation through the state machine', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad()

    // History becomes available (can go back).
    await act(async () => {
      emitWailsEvent('browser:history-state:session-1', { canBack: true, canForward: false })
    })
    bridge.goBackBrowserWindow.mockClear()
    bridge.navigateBrowserSession.mockClear()

    // Click back: emits the host back command.
    await act(async () => {
      ;(container.querySelector('[title="Back"]') as HTMLButtonElement).click()
    })
    expect(bridge.goBackBrowserWindow).toHaveBeenCalledWith('session-1')
    expect(bridge.navigateBrowserSession).not.toHaveBeenCalled()

    // Backend reports the back navigation loading then loaded at the back target.
    await act(async () => {
      emitState({ status: 'loading', confirmedURL: 'https://example.com' })
    })
    expect(container.querySelector('.browser-loading')).not.toBeNull()

    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://example.com/back' })
    })
    expect(container.querySelector('.browser-loading')).toBeNull()
    // The address bar reflects the document we navigated back to.
    expect(inputValue()).toBe('https://example.com/back')
  })

  // ---- stale event race: a stale page-info must not corrupt the address bar ----

  it('does not let a stale page-info regress the address bar', async () => {
    await renderView({ initialUrl: 'https://example.com' })
    await flushMount()
    await completeInitialLoad('https://current.example.com')
    expect(inputValue()).toBe('https://current.example.com')

    // A delayed page-info from a PREVIOUS page (old URL) arrives after the new
    // document already committed. It must not change the address bar — the
    // address bar is driven exclusively by browser:state.confirmedURL.
    await act(async () => {
      emitWailsEvent('browser:page-info:session-1', { u: 'https://old.example.com', t: 'Stale Title' })
    })
    expect(inputValue()).toBe('https://current.example.com')

    // Only a state event can move the address bar.
    await act(async () => {
      emitState({ status: 'loaded', confirmedURL: 'https://newer.example.com' })
    })
    expect(inputValue()).toBe('https://newer.example.com')
  })

  // ---- proxy in create/edit modal ----

  // The proxy mode selector is the last .browser-newtab-mode button group in
  // the modal; its buttons are ordered system / none / custom.
  const proxyModeButtons = (): NodeListOf<HTMLButtonElement> => {
    const groups = container.querySelectorAll('.browser-newtab-mode')
    return groups[groups.length - 1]!.querySelectorAll('button')
  }

  it('passes proxy to openBrowserWindow when creating an independent instance', async () => {
    const { openBrowserWindow } = await import('../../../application/browser-manager')
    vi.mocked(openBrowserWindow).mockClear()
    ;(openBrowserWindow as any).mockResolvedValue({ Instance: null })

    await renderView({ kind: 'independent', initialUrl: '', defaultCreateScope: 'independent' })
    await flushMount()

    // Click the "+" add button to open the create modal.
    const addBtn = container.querySelector('.browser-newtab-add') as HTMLButtonElement
    await act(async () => { addBtn.click() })

    // Switch the proxy mode to custom so the proxy URL input appears.
    const customBtn = proxyModeButtons()[2] as HTMLButtonElement
    await act(async () => { customBtn.click() })

    // Set the name, URL, and proxy inputs.
    const inputs = container.querySelectorAll('.browser-modal-input')
    const nameInput = inputs[0] as HTMLInputElement
    const urlInput = inputs[1] as HTMLInputElement
    const proxyInput = inputs[2] as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(nameInput, 'My Proxy Instance')
      nameInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      setter.call(urlInput, 'https://example.com')
      urlInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      setter.call(proxyInput, 'http://myproxy:8080')
      proxyInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    // Click the save button.
    const saveBtn = container.querySelector('.browser-modal-btn.primary') as HTMLButtonElement
    await act(async () => { saveBtn.click() })

    expect(openBrowserWindow).toHaveBeenCalledWith('My Proxy Instance', 'https://example.com', 'http://myproxy:8080', 'custom')
  })

  it('creates an independent instance with no proxy when none mode is selected', async () => {
    const { openBrowserWindow } = await import('../../../application/browser-manager')
    vi.mocked(openBrowserWindow).mockClear()
    ;(openBrowserWindow as any).mockResolvedValue({ Instance: null })

    await renderView({ kind: 'independent', initialUrl: '', defaultCreateScope: 'independent' })
    await flushMount()

    const addBtn = container.querySelector('.browser-newtab-add') as HTMLButtonElement
    await act(async () => { addBtn.click() })

    const noneBtn = proxyModeButtons()[1] as HTMLButtonElement
    await act(async () => { noneBtn.click() })

    const inputs = container.querySelectorAll('.browser-modal-input')
    const nameInput = inputs[0] as HTMLInputElement
    const urlInput = inputs[1] as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(nameInput, 'Direct Instance')
      nameInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      setter.call(urlInput, 'https://example.com')
      urlInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const saveBtn = container.querySelector('.browser-modal-btn.primary') as HTMLButtonElement
    await act(async () => { saveBtn.click() })

    expect(openBrowserWindow).toHaveBeenCalledWith('Direct Instance', 'https://example.com', '', 'none')
  })

  it('preserves proxy when editing an existing instance', async () => {
    const { listBrowserWindows, updateBrowserWindow } = await import('../../../application/browser-manager')
    vi.mocked(listBrowserWindows).mockResolvedValue([
      {
        Config: { Id: 'inst-1', Name: 'Test', Url: 'https://example.com', Open: false, Proxy: 'http://oldproxy:8080', ProxyMode: '', Mode: 'tab', Hidden: false, State: { X: 0, Y: 0, Width: 0, Height: 0, Maximised: false, Url: '', Title: '', PageState: { ScrollX: 0, ScrollY: 0, Zoom: 1, Data: '' }, History: [] } },
        Status: { Id: 'inst-1', Open: false, Url: '', Title: '' },
      },
    ])
    vi.mocked(updateBrowserWindow).mockClear()

    await renderView({ kind: 'independent', initialUrl: '' })
    await flushMount()

    // Click the edit (pencil) button on the card.
    const cardTools = container.querySelectorAll('.browser-newtab-card-tool')
    const editBtn = cardTools[0] as HTMLButtonElement  // index 0 = edit (pencil) button
    await act(async () => { editBtn.click() })

    // The legacy Proxy value maps to custom mode, so the proxy input is
    // pre-filled with the existing value.
    const inputs = container.querySelectorAll('.browser-modal-input')
    const proxyInput = inputs[2] as HTMLInputElement
    expect(proxyInput.value).toBe('http://oldproxy:8080')

    // Change the proxy value.
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(proxyInput, 'http://newproxy:9090')
      proxyInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    // Click the save button.
    const saveBtn = container.querySelector('.browser-modal-btn.primary') as HTMLButtonElement
    await act(async () => { saveBtn.click() })

    expect(updateBrowserWindow).toHaveBeenCalledWith({
      Id: 'inst-1',
      Config: expect.objectContaining({ Proxy: 'http://newproxy:9090', ProxyMode: 'custom' }),
    })
  })

  it('shows an import button on the card and imports a dropped cookies.txt', async () => {
    const { listBrowserWindows, importBrowserCookies } = await import('../../../application/browser-manager')
    vi.mocked(listBrowserWindows).mockResolvedValue([
      {
        Config: { Id: 'inst-1', Name: 'Test', Url: 'https://example.com', Open: false, Proxy: '', ProxyMode: '', Mode: 'tab', Hidden: false, State: { X: 0, Y: 0, Width: 0, Height: 0, Maximised: false, Url: '', Title: '', PageState: { ScrollX: 0, ScrollY: 0, Zoom: 1, Data: '' }, History: [] } },
        Status: { Id: 'inst-1', Open: false, Url: '', Title: '' },
      },
    ])
    vi.mocked(importBrowserCookies).mockResolvedValue(1)

    await renderView({ kind: 'independent', initialUrl: '' })
    await flushMount()

    // Import button sits between edit and delete in the card tools row.
    const cardTools = container.querySelectorAll('.browser-newtab-card-tool')
    expect(cardTools.length).toBe(3)
    expect((cardTools[1] as HTMLButtonElement).title).toBe('Import cookies')

    const card = container.querySelector('.browser-newtab-card:not(.browser-newtab-add)') as HTMLElement
    const file = new File(
      ['# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t1893456000\tsid\tabc\n'],
      'cookies.txt',
      { type: 'text/plain' },
    )
    const drop = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(drop, 'dataTransfer', { value: { files: [file] } })
    await act(async () => { card.dispatchEvent(drop) })

    expect(importBrowserCookies).toHaveBeenCalledTimes(1)
    expect(importBrowserCookies).toHaveBeenCalledWith('inst-1', {
      '.example.com': [
        { Name: 'sid', Value: 'abc', Domain: '.example.com', Path: '/', Expires: 1893456000, HttpOnly: false, Secure: true, SameSite: '' },
      ],
    })
  })

  // ---- address-bar history omnibox (independent instances) ----

  const histInstance = (history: Array<{ Url: string; Title?: string; VisitedAt: string }>) => ({
    Config: {
      Id: 'inst-1',
      Name: 'Inst One',
      Url: '',
      Open: true,
      Proxy: '',
      ProxyMode: 'system',
      Mode: 'tab',
      Hidden: false,
      State: {
        X: 0, Y: 0, Width: 0, Height: 0, Maximised: false,
        Url: '', Title: '',
        PageState: { ScrollX: 0, ScrollY: 0, Zoom: 0, Data: '' },
        History: history,
      },
    },
    Status: { Id: 'inst-1', Open: true, Url: '', Title: '' },
  })

  const setListInstances = async (items: unknown[]) => {
    const { listBrowserWindows } = await import('../../../application/browser-manager')
    vi.mocked(listBrowserWindows).mockResolvedValue(items as never)
  }

  const focusAddress = async (typed = '') => {
    const addr = container.querySelector('.browser-address-input') as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      if (typed !== addr.value) {
        setter.call(addr, typed)
        addr.dispatchEvent(new Event('input', { bubbles: true }))
      }
      addr.focus()
    })
    return addr
  }

  const dropdownUrls = () =>
    [...container.querySelectorAll('.browser-history-item-url')].map((el) => el.textContent)

  it('shows persisted history on address focus (newest first) and navigates on click', async () => {
    await setListInstances([histInstance([
      { Url: 'https://a.example/one', Title: 'One', VisitedAt: '2026-09-10T10:00:00Z' },
      { Url: 'https://b.example/two', Title: 'Two', VisitedAt: '2026-09-10T11:00:00Z' },
    ])])
    await renderView({ kind: 'independent', sessionId: 'inst-1', initialUrl: 'https://example.com' })
    await flushMount()

    await focusAddress('')
    expect(container.querySelector('.browser-history-dropdown')).not.toBeNull()
    expect(dropdownUrls()).toEqual(['https://b.example/two', 'https://a.example/one'])

    bridge.navigateBrowserSession.mockClear()
    await act(async () => {
      ;(container.querySelectorAll('.browser-history-item')[1] as HTMLButtonElement).click()
    })
    expect(bridge.navigateBrowserSession).toHaveBeenCalledWith('inst-1', 'https://a.example/one')
    expect(inputValue()).toBe('https://a.example/one')
    expect(container.querySelector('.browser-history-dropdown')).toBeNull()
  })

  it('filters history suggestions by the typed input', async () => {
    await setListInstances([histInstance([
      { Url: 'https://a.example/one', Title: 'Alpha', VisitedAt: '2026-09-10T10:00:00Z' },
      { Url: 'https://b.example/two', Title: 'Beta', VisitedAt: '2026-09-10T11:00:00Z' },
    ])])
    await renderView({ kind: 'independent', sessionId: 'inst-1', initialUrl: 'https://example.com' })
    await flushMount()

    await focusAddress('two')
    expect(dropdownUrls()).toEqual(['https://b.example/two'])

    const addr = container.querySelector('.browser-address-input') as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(addr, 'nomatch-xyz')
      addr.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.querySelector('.browser-history-dropdown')).toBeNull()
  })

  it('Enter submits the keyboard-highlighted history entry', async () => {
    await setListInstances([histInstance([
      { Url: 'https://a.example/one', Title: 'One', VisitedAt: '2026-09-10T10:00:00Z' },
      { Url: 'https://b.example/two', Title: 'Two', VisitedAt: '2026-09-10T11:00:00Z' },
    ])])
    await renderView({ kind: 'independent', sessionId: 'inst-1', initialUrl: 'https://example.com' })
    await flushMount()

    const addr = await focusAddress('')
    await act(async () => {
      addr.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    })
    expect((container.querySelector('.browser-history-item') as HTMLElement).className).toContain('active')

    bridge.navigateBrowserSession.mockClear()
    await act(async () => {
      ;(container.querySelector('.browser-address-bar') as HTMLFormElement).requestSubmit()
    })
    expect(bridge.navigateBrowserSession).toHaveBeenCalledWith('inst-1', 'https://b.example/two')
    expect(inputValue()).toBe('https://b.example/two')
    expect(container.querySelector('.browser-history-dropdown')).toBeNull()
  })

  it('does not show the history omnibox for global tabs', async () => {
    await setListInstances([histInstance([
      { Url: 'https://a.example/one', Title: 'One', VisitedAt: '2026-09-10T10:00:00Z' },
    ])])
    await renderView({ kind: 'global', initialUrl: 'https://example.com' })
    await flushMount()

    await focusAddress('')
    expect(container.querySelector('.browser-history-dropdown')).toBeNull()
  })
})
