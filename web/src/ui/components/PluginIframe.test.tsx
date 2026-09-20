import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { PluginIframe } from './PluginIframe'
import { appRegistry } from '../../application/app-registry'
import { getHttpUrl, resolveWailsGateway } from '../../application/gateway'
import { isCapacitor, isWails } from '../../application/runtime'
import { pluginLoad, sessionCreate } from '../../gen-clients/appmanager/client'

vi.mock('../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../application/gateway', () => ({
  getHttpUrl: vi.fn(() => 'http://127.0.0.1:18080'),
  resolveWailsGateway: vi.fn(async () => {}),
}))

vi.mock('../../application/runtime', () => ({
  isWails: vi.fn(() => false),
  isCapacitor: vi.fn(() => false),
}))

vi.mock('../../gen-clients/appmanager/client', () => ({
  pluginLoad: vi.fn(),
  sessionCreate: vi.fn(),
  sessionRevoke: vi.fn(),
}))

const mockedPluginLoad = vi.mocked(pluginLoad)
const mockedSessionCreate = vi.mocked(sessionCreate)
const mockedIsCapacitor = vi.mocked(isCapacitor)
const mockedIsWails = vi.mocked(isWails)
const mockedResolveWailsGateway = vi.mocked(resolveWailsGateway)

describe('PluginIframe', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedIsCapacitor.mockReturnValue(false)
    appRegistry.clear()
    document.documentElement.setAttribute('data-theme', 'light')
    document.documentElement.setAttribute('lang', 'en-US')
  })

  it('always resolves the gateway plugin asset URL, even without a registry entry', async () => {
    render(<PluginIframe pluginID="com.example.test" viewID="main" route="index.html" />)
    const iframe = await screen.findByTitle('main')
    expect(iframe.tagName).toBe('IFRAME')
    expect((iframe as HTMLIFrameElement).src).toBe('http://127.0.0.1:18080/plugin/com.example.test/index.html')
  })

  it('keeps the gateway asset route for running apps without an HTTP listener', async () => {
    appRegistry.upsert({
      id: 'com.example.legacy', runtime: 'native', state: 'running', version: '1',
      generation: 3, entrypoints: [],
    })
    render(<PluginIframe pluginID="com.example.legacy" viewID="main" route="index.html" />)
    const iframe = await screen.findByTitle('main')
    // Cache-busting generation param is still applied on the gateway route.
    expect((iframe as HTMLIFrameElement).src).toBe('http://127.0.0.1:18080/plugin/com.example.legacy/index.html?v=3')
  })

  it('routes through the gateway even when the app advertises a backendUrl', async () => {
    appRegistry.upsert({
      id: 'com.example.direct', runtime: 'native', state: 'running', version: '1',
      generation: 2, backendUrl: 'http://127.0.0.1:4100', entrypoints: [],
    })
    render(<PluginIframe pluginID="com.example.direct" viewID="main" route="index.html" />)
    const iframe = await screen.findByTitle('main')
    // backendUrl is an internal detail of the gateway proxy; the iframe
    // document always loads from the gateway /plugin/{id} route.
    expect((iframe as HTMLIFrameElement).src).toBe('http://127.0.0.1:18080/plugin/com.example.direct/index.html?v=2')
  })

  it('posts a gateway bootstrap message with appBase and no cookie fields', async () => {
    appRegistry.upsert({
      id: 'com.example.boot', runtime: 'native', state: 'running', version: '1',
      generation: 1, backendUrl: 'http://127.0.0.1:4100', entrypoints: [],
    })
    mockedSessionCreate.mockResolvedValue({
      SessionId: 'session-boot', AppId: 'com.example.boot', ViewId: 'main',
      AgentId: 'agent-1', ProjectId: 'proj-1', Origin: 'http://localhost:3000',
      ExpiresAt: 0, Nonce: 'n1', Generation: 1, Token: 'tok-1',
    } as any)
    render(<PluginIframe pluginID="com.example.boot" viewID="main" route="index.html" />)
    const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
    const win = iframe.contentWindow
    expect(win).not.toBeNull()
    const posted: unknown[] = []
    vi.spyOn(win!, 'postMessage').mockImplementation(((msg: unknown) => { posted.push(msg) }) as any)

    // Simulate the injected snippet asking for its bootstrap payload.
    window.dispatchEvent(new MessageEvent('message', {
      data: { type: 'sporemind:ready-for-bootstrap' },
      source: win,
    }))

    await waitFor(() => {
      expect(posted.some((m: any) => m?.type === 'sporemind:bootstrap')).toBe(true)
    })
    const boot = posted.find((m: any) => m?.type === 'sporemind:bootstrap') as any
    expect(boot.appBase).toBe('/plugin/com.example.boot')
    expect('cookieToken' in boot).toBe(false)
    expect('backendUrl' in boot).toBe(false)
    expect(boot.voiceNative).toBe(false)
    expect(boot.themeMode).toBe('light')
    expect(boot.locale).toBe('en-US')
  })

  it('logs the mount/bind/bootstrap chain under the [plugin-bridge] key', async () => {
    const infoSpy = vi.spyOn(console, 'info').mockImplementation(() => {})
    try {
      mockedSessionCreate.mockResolvedValue({
        SessionId: 'session-chain', AppId: 'com.example.chain', ViewId: 'main',
        AgentId: 'agent-1', ProjectId: 'proj-1', Origin: 'http://localhost:3000',
        ExpiresAt: 0, Nonce: 'n1', Generation: 1, Token: 'tok-chain',
      } as any)
      render(<PluginIframe pluginID="com.example.chain" viewID="main" route="index.html" />)
      const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
      await waitFor(() => {
        expect(infoSpy.mock.calls.some((c) => String(c[0]).includes('mount app=com.example.chain'))).toBe(true)
        expect(infoSpy.mock.calls.some((c) => String(c[0]).includes('src_ready app=com.example.chain'))).toBe(true)
      })
      fireEvent(iframe, new Event('load'))
      await waitFor(() => {
        expect(infoSpy.mock.calls.some((c) => String(c[0]).includes('frame_load app=com.example.chain'))).toBe(true)
        expect(infoSpy.mock.calls.some((c) => String(c[0]).includes('bind_ok app=com.example.chain'))).toBe(true)
      })
      fireEvent(window, new MessageEvent('message', {
        data: { type: 'sporemind:ready-for-bootstrap' },
        source: iframe.contentWindow,
      }))
      await waitFor(() => {
        expect(infoSpy.mock.calls.some((c) => String(c[0]).includes('ready_received app=com.example.chain'))).toBe(true)
        expect(infoSpy.mock.calls.some((c) => String(c[0]).includes('bootstrap_sent app=com.example.chain'))).toBe(true)
      })
    } finally {
      infoSpy.mockRestore()
    }
  })

  it('posts event-suspend/resume when panel activity flips (and once after load)', async () => {
    appRegistry.upsert({
      id: 'com.example.suspend', runtime: 'native', state: 'running', version: '1',
      generation: 1, backendUrl: 'http://127.0.0.1:4100', entrypoints: [],
    })
    const { rerender } = render(
      <PluginIframe pluginID="com.example.suspend" viewID="main" route="index.html" active={false} />,
    )
    const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
    const posted: unknown[] = []
    vi.spyOn(iframe.contentWindow!, 'postMessage').mockImplementation(((msg: unknown) => { posted.push(msg) }) as any)

    // The suspend lands only after the document loaded (the snippet's
    // listener must exist); before that, nothing is sent.
    expect(posted).toHaveLength(0)
    fireEvent(iframe, new Event('load'))
    await waitFor(() => {
      expect(posted.some((m: any) => m?.type === 'sporemind:event-suspend')).toBe(true)
    })

    rerender(
      <PluginIframe pluginID="com.example.suspend" viewID="main" route="index.html" active={true} />,
    )
    await waitFor(() => {
      expect(posted.some((m: any) => m?.type === 'sporemind:event-resume')).toBe(true)
    })
    // No duplicate posts on re-render without a flip.
    const suspends = posted.filter((m: any) => m?.type === 'sporemind:event-suspend')
    const resumes = posted.filter((m: any) => m?.type === 'sporemind:event-resume')
    expect(suspends).toHaveLength(1)
    expect(resumes).toHaveLength(1)
  })

  it('retries a wedged gateway resolution and surfaces a terminal error instead of an eternal spinner', async () => {
    vi.useFakeTimers()
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const infoSpy = vi.spyOn(console, 'info').mockImplementation(() => {})
    mockedIsWails.mockReturnValue(true)
    mockedResolveWailsGateway.mockImplementation(() => new Promise(() => {}))
    try {
      render(<PluginIframe pluginID="com.example.wedged" viewID="main" route="index.html" />)
      // 3 attempts × 8s timeout + 2 × 1s backoff = 26s of fake time.
      await act(async () => { await vi.advanceTimersByTimeAsync(27000) })
      expect(screen.getByTestId('plugin-iframe-src-error')).toBeTruthy()
      expect(screen.getByTestId('plugin-iframe-src-error-cause').textContent).toContain('resolveWailsGateway timed out')
      const warns = warnSpy.mock.calls.map((c) => String(c[0]))
      expect(warns.filter((l) => l.includes('src_resolve_err app=com.example.wedged'))).toHaveLength(3)
    } finally {
      vi.useRealTimers()
      warnSpy.mockRestore()
      infoSpy.mockRestore()
      mockedIsWails.mockReturnValue(false)
      mockedResolveWailsGateway.mockResolvedValue({} as never)
    }
  })

  it('auto-reloads a dead error-page frame once per tab activation', async () => {
    const infoSpy = vi.spyOn(console, 'info').mockImplementation(() => {})
    try {
      // Background tab (keepAlive): the panel document "loads" but is the
      // gateway 502 text or the webview error page — no snippet, no
      // ready-for-bootstrap. Grab the element under real timers, then
      // switch to fake timers for the grace periods.
      const { rerender } = render(
        <PluginIframe pluginID="com.example.deadframe" viewID="main" route="index.html" active={false} />,
      )
      const first = (await screen.findByTitle('main')) as HTMLIFrameElement
      vi.useFakeTimers()
      await act(async () => { fireEvent(first, new Event('load')) })
      await act(async () => { await vi.advanceTimersByTimeAsync(8000) })
      expect(infoSpy.mock.calls.some((c) => String(c[0]).includes('frame_dead app=com.example.deadframe'))).toBe(true)

      // Activating the tab remounts the dead document exactly once.
      rerender(<PluginIframe pluginID="com.example.deadframe" viewID="main" route="index.html" active={true} />)
      const second = screen.getByTitle('main') as HTMLIFrameElement
      expect(second).not.toBe(first)

      // The retried document is also dead — but the same active episode
      // never retries again (no reload loop on a broken backend).
      await act(async () => { fireEvent(second, new Event('load')) })
      await act(async () => { await vi.advanceTimersByTimeAsync(20000) })
      expect(screen.getByTitle('main')).toBe(second)

      // Going inactive and back re-arms exactly one more attempt.
      rerender(<PluginIframe pluginID="com.example.deadframe" viewID="main" route="index.html" active={false} />)
      rerender(<PluginIframe pluginID="com.example.deadframe" viewID="main" route="index.html" active={true} />)
      const third = screen.getByTitle('main') as HTMLIFrameElement
      expect(third).not.toBe(second)

      const retries = infoSpy.mock.calls.filter((c) => String(c[0]).includes('auto_retry_activate'))
      expect(retries).toHaveLength(2)
    } finally {
      vi.useRealTimers()
      infoSpy.mockRestore()
    }
  })

  it('never auto-reloads a live frame that signaled ready-for-bootstrap', async () => {
    const infoSpy = vi.spyOn(console, 'info').mockImplementation(() => {})
    try {
      mockedSessionCreate.mockResolvedValue({
        SessionId: 'session-live', AppId: 'com.example.live', ViewId: 'main',
        AgentId: 'agent-1', ProjectId: 'proj-1', Origin: 'http://localhost:3000',
        ExpiresAt: 0, Nonce: 'n1', Generation: 1, Token: 'tok-live',
      } as any)
      const { rerender } = render(
        <PluginIframe pluginID="com.example.live" viewID="main" route="index.html" active={false} />,
      )
      const first = (await screen.findByTitle('main')) as HTMLIFrameElement
      const win = first.contentWindow
      expect(win).not.toBeNull()
      vi.useFakeTimers()
      await act(async () => { fireEvent(first, new Event('load')) })
      await act(async () => {
        window.dispatchEvent(new MessageEvent('message', {
          data: { type: 'sporemind:ready-for-bootstrap' },
          source: win,
        }))
      })
      await act(async () => { await vi.advanceTimersByTimeAsync(20000) })

      rerender(<PluginIframe pluginID="com.example.live" viewID="main" route="index.html" active={true} />)
      await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
      expect(screen.getByTitle('main')).toBe(first)
      expect(infoSpy.mock.calls.some((c) => String(c[0]).includes('frame_dead app=com.example.live'))).toBe(false)
      expect(infoSpy.mock.calls.some((c) => String(c[0]).includes('auto_retry_activate'))).toBe(false)
    } finally {
      vi.useRealTimers()
      infoSpy.mockRestore()
    }
  })

  it('marks the bootstrap payload with the native voice capability inside the Capacitor shell', async () => {
    mockedIsCapacitor.mockReturnValue(true)
    appRegistry.upsert({
      id: 'com.example.voicemark', runtime: 'native', state: 'running', version: '1',
      generation: 1, entrypoints: [],
    })
    mockedSessionCreate.mockResolvedValue({
      SessionId: 'session-voicemark', AppId: 'com.example.voicemark', ViewId: 'main',
      AgentId: 'agent-1', ProjectId: 'proj-1', Origin: 'http://localhost:3000',
      ExpiresAt: 0, Nonce: 'n1', Generation: 1, Token: 'tok-3',
    } as any)
    render(<PluginIframe pluginID="com.example.voicemark" viewID="main" route="index.html" />)
    const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
    const win = iframe.contentWindow
    expect(win).not.toBeNull()
    const posted: unknown[] = []
    vi.spyOn(win!, 'postMessage').mockImplementation(((msg: unknown) => { posted.push(msg) }) as any)

    window.dispatchEvent(new MessageEvent('message', {
      data: { type: 'sporemind:ready-for-bootstrap' },
      source: win,
    }))

    await waitFor(() => {
      expect(posted.some((m: any) => m?.type === 'sporemind:bootstrap')).toBe(true)
    })
    const boot = posted.find((m: any) => m?.type === 'sporemind:bootstrap') as any
    expect(boot.voiceNative).toBe(true)
  })

  it('relays plugin voice:* frames up to the shell inside the Capacitor shell', async () => {
    mockedIsCapacitor.mockReturnValue(true)
    render(<PluginIframe pluginID="com.example.voice1" viewID="main" route="index.html" />)
    const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
    const win = iframe.contentWindow
    expect(win).not.toBeNull()
    const parentPost = vi.spyOn(window.parent, 'postMessage').mockImplementation((() => {}) as any)

    const start = { _sporemind: true, type: 'voice:start', data: { requestId: 7 } }
    window.dispatchEvent(new MessageEvent('message', { data: start, source: win }))
    expect(parentPost).toHaveBeenCalledWith(start, '*')

    // Strict source check: voice frames not from this iframe are dropped,
    // and non-voice frames never take the relay.
    parentPost.mockClear()
    window.dispatchEvent(new MessageEvent('message', { data: start, source: window }))
    window.dispatchEvent(new MessageEvent('message', { data: start }))
    window.dispatchEvent(new MessageEvent('message', {
      data: { _sporemind: true, type: 'debug:log', data: { msg: 'x' } },
      source: win,
    }))
    expect(parentPost).not.toHaveBeenCalled()
  })

  it('does not relay plugin voice frames outside the Capacitor shell', async () => {
    render(<PluginIframe pluginID="com.example.voice2" viewID="main" route="index.html" />)
    const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
    const win = iframe.contentWindow
    expect(win).not.toBeNull()
    const parentPost = vi.spyOn(window.parent, 'postMessage').mockImplementation((() => {}) as any)
    window.dispatchEvent(new MessageEvent('message', {
      data: { _sporemind: true, type: 'voice:start', data: { requestId: 1 } },
      source: win,
    }))
    expect(parentPost).not.toHaveBeenCalled()
  })

  it('forwards shell voice:* replies down into the plugin iframe inside the Capacitor shell', async () => {
    mockedIsCapacitor.mockReturnValue(true)
    render(<PluginIframe pluginID="com.example.voice3" viewID="main" route="index.html" />)
    const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
    const win = iframe.contentWindow
    expect(win).not.toBeNull()
    const framePost = vi.spyOn(win!, 'postMessage').mockImplementation((() => {}) as any)

    // jsdom: window.parent === window, so the shell leg is a message whose
    // source is the host window itself.
    const partial = { _sporemind: true, type: 'voice:partial', data: { requestId: 7, text: 'hi' } }
    window.dispatchEvent(new MessageEvent('message', { data: partial, source: window.parent }))
    expect(framePost).toHaveBeenCalledWith(partial, '*')

    // Strict source check: frames not from window.parent are dropped, and
    // non-voice frames from the shell never take the relay.
    framePost.mockClear()
    window.dispatchEvent(new MessageEvent('message', { data: partial, source: win }))
    window.dispatchEvent(new MessageEvent('message', {
      data: { type: 'sporemind:theme-update', themeMode: 'dark' },
      source: window.parent,
    }))
    expect(framePost).not.toHaveBeenCalled()
  })

  it('does not forward shell voice frames outside the Capacitor shell', async () => {
    render(<PluginIframe pluginID="com.example.voice4" viewID="main" route="index.html" />)
    const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
    const win = iframe.contentWindow
    expect(win).not.toBeNull()
    const framePost = vi.spyOn(win!, 'postMessage').mockImplementation((() => {}) as any)
    window.dispatchEvent(new MessageEvent('message', {
      data: { _sporemind: true, type: 'voice:volume', data: { requestId: 1, level: 0.5 } },
      source: window.parent,
    }))
    expect(framePost).not.toHaveBeenCalled()
  })

  it('proactively posts bootstrap on iframe load once the session binds (lost ready-for-bootstrap)', async () => {
    appRegistry.upsert({
      id: 'com.example.proactive', runtime: 'native', state: 'running', version: '1',
      generation: 1, entrypoints: [],
    })
    mockedSessionCreate.mockResolvedValue({
      SessionId: 'session-proactive', AppId: 'com.example.proactive', ViewId: 'main',
      AgentId: 'agent-1', ProjectId: 'proj-1', Origin: 'http://localhost:3000',
      ExpiresAt: 0, Nonce: 'n1', Generation: 1, Token: 'tok-2',
    } as any)
    render(<PluginIframe pluginID="com.example.proactive" viewID="main" route="index.html" />)
    const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
    const win = iframe.contentWindow
    expect(win).not.toBeNull()
    const posted: unknown[] = []
    vi.spyOn(win!, 'postMessage').mockImplementation(((msg: unknown) => { posted.push(msg) }) as any)

    // The lost-message race: the iframe's ready-for-bootstrap fired before
    // this component's listener mounted, so it is never seen here. The load
    // event alone must still deliver the bootstrap once the session binds.
    fireEvent(iframe, new Event('load'))

    await waitFor(() => {
      expect(posted.some((m: any) => m?.type === 'sporemind:bootstrap')).toBe(true)
    })
    const boot = posted.find((m: any) => m?.type === 'sporemind:bootstrap') as any
    expect(boot.appBase).toBe('/plugin/com.example.proactive')
    expect(boot.bridgeUrl).toContain('/src/plugin-bridge-client.ts')
  })

  it('does not proactively post bootstrap when the session bind fails', async () => {
    appRegistry.upsert({
      id: 'com.example.unbound', runtime: 'native', state: 'running', version: '1',
      generation: 1, entrypoints: [],
    })
    mockedSessionCreate.mockRejectedValue(new Error('session_create down'))
    render(<PluginIframe pluginID="com.example.unbound" viewID="main" route="index.html" />)
    const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
    const win = iframe.contentWindow
    expect(win).not.toBeNull()
    const posted: unknown[] = []
    vi.spyOn(win!, 'postMessage').mockImplementation(((msg: unknown) => { posted.push(msg) }) as any)

    fireEvent(iframe, new Event('load'))

    // Give the rejected bind promise a chance to (wrongly) send.
    await new Promise((r) => setTimeout(r, 50))
    expect(posted.some((m: any) => m?.type === 'sporemind:bootstrap')).toBe(false)
  })

  it('mirrors theme and locale attribute changes into the iframe after load', async () => {
    render(<PluginIframe pluginID="com.example.env" viewID="main" route="index.html" />)
    const iframe = (await screen.findByTitle('main')) as HTMLIFrameElement
    const win = iframe.contentWindow
    expect(win).not.toBeNull()
    const posted: unknown[] = []
    vi.spyOn(win!, 'postMessage').mockImplementation(((msg: unknown) => { posted.push(msg) }) as any)

    // The observer attaches on iframe load.
    fireEvent(iframe, new Event('load'))

    document.documentElement.setAttribute('lang', 'zh-CN')
    await waitFor(() => {
      expect(posted.some((m: any) => m?.type === 'sporemind:locale-update')).toBe(true)
    })
    expect((posted.find((m: any) => m?.type === 'sporemind:locale-update') as any).locale).toBe('zh-CN')

    document.documentElement.setAttribute('data-theme', 'dark')
    await waitFor(() => {
      expect(posted.some((m: any) => m?.type === 'sporemind:theme-update')).toBe(true)
    })
    expect((posted.find((m: any) => m?.type === 'sporemind:theme-update') as any).themeMode).toBe('dark')

    // A lang mutation must not emit a theme-update and vice versa.
    const localeUpdates = posted.filter((m: any) => m?.type === 'sporemind:locale-update')
    const themeUpdates = posted.filter((m: any) => m?.type === 'sporemind:theme-update')
    document.documentElement.setAttribute('lang', 'ja-JP')
    await waitFor(() => {
      expect(posted.filter((m: any) => m?.type === 'sporemind:locale-update').length).toBe(localeUpdates.length + 1)
    })
    expect(posted.filter((m: any) => m?.type === 'sporemind:theme-update').length).toBe(themeUpdates.length)
  })

  it('shows an error state with a restart action when the plugin is not running', async () => {
    appRegistry.upsert({
      id: 'com.example.crashed', runtime: 'native', state: 'crashed', version: '1', error: 'segfault',
      entrypoints: [],
    })
    mockedPluginLoad.mockResolvedValue({
      Status: { Id: 'com.example.crashed', State: 'running', Version: '1', Entrypoints: [] },
    } as any)
    render(<PluginIframe pluginID="com.example.crashed" viewID="main" route="index.html" />)

    expect(screen.getByTestId('plugin-iframe-error')).toBeDefined()
    expect(screen.getByTestId('plugin-iframe-error-cause').textContent).toContain('segfault')

    fireEvent.click(screen.getByTestId('plugin-iframe-restart'))
    await waitFor(() => {
      expect(mockedPluginLoad).toHaveBeenCalledWith(expect.anything(), { Id: 'com.example.crashed' })
    })
    // Registry upsert from the restart response swaps the shell back to the
    // loading/iframe path.
    await waitFor(() => {
      expect(appRegistry.get('com.example.crashed')?.state).toBe('running')
    })
  })

  it('surfaces restart failures', async () => {
    appRegistry.upsert({
      id: 'com.example.stopped', runtime: 'native', state: 'stopped', version: '1',
      entrypoints: [],
    })
    mockedPluginLoad.mockRejectedValue(new Error('load failed'))
    render(<PluginIframe pluginID="com.example.stopped" viewID="main" route="index.html" />)
    fireEvent.click(screen.getByTestId('plugin-iframe-restart'))
    await waitFor(() => {
      expect(screen.getByTestId('plugin-iframe-restart-error').textContent).toContain('load failed')
    })
  })
})

describe('getHttpUrl mock sanity', () => {
  it('uses the mocked gateway URL', () => {
    expect(getHttpUrl()).toBe('http://127.0.0.1:18080')
  })
})
