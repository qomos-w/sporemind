import { describe, it, expect, vi, beforeEach } from 'vitest'

// The bridge client is a module with top-level side effects: it installs
// console capture, opens a MessageChannel, and posts the handshake (with
// port2 transferred) to the parent window. Each test resets the module
// registry and the globals it reads (bridge context, EventSource, fetch),
// captures the transferred port as the "host" side, and re-imports a fresh
// instance.

const REAL_CONSOLE = {
  log: console.log,
  info: console.info,
  warn: console.warn,
  error: console.error,
  debug: console.debug,
}

/** Minimal EventSource mock capturing the URL and per-kind listeners. */
class MockEventSource {
  static instances: MockEventSource[] = []
  readonly url: string
  readonly withCredentials: boolean
  closed = false
  private listeners = new Map<string, Set<(ev: MessageEvent) => void>>()
  constructor(url: string, opts?: { withCredentials?: boolean }) {
    this.url = url
    this.withCredentials = Boolean(opts?.withCredentials)
    MockEventSource.instances.push(this)
  }
  addEventListener(kind: string, cb: (ev: MessageEvent) => void) {
    let set = this.listeners.get(kind)
    if (!set) {
      set = new Set()
      this.listeners.set(kind, set)
    }
    set.add(cb)
  }
  removeEventListener(kind: string, cb: (ev: MessageEvent) => void) {
    this.listeners.get(kind)?.delete(cb)
  }
  close() {
    this.closed = true
  }
  emit(kind: string, data: string) {
    for (const cb of this.listeners.get(kind) ?? []) {
      cb({ data } as MessageEvent)
    }
  }
}

/** Minimal WebSocket mock: constructor captures the URL; tests deliver
 * envelopes via deliver() and drive fallback via failHandshake(). */
class MockWebSocket {
  static instances: MockWebSocket[] = []
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  readonly url: string
  closed = false
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: unknown }) => void) | null = null
  onclose: (() => void) | null = null
  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
  }
  close() {
    this.closed = true
  }
  open() {
    this.onopen?.()
  }
  deliver(event: string, data: unknown) {
    this.onmessage?.({ data: JSON.stringify({ event, data }) })
  }
  /** Simulate a refused upgrade (older SDK answers 200): close before open. */
  failHandshake() {
    this.onclose?.()
  }
}

/** Capture the port the client transfers to its "parent" at handshake. */
let hostPort: MessagePort | undefined

function freshBridgeContext(overrides: Record<string, unknown> = {}) {
  ;(globalThis as any).__sporemindBridgeContext = {
    pluginID: 'test.app',
    viewID: 'main',
    ...overrides,
  }
}

/** Import a fresh client instance and return its exposed surface + host port. */
async function importClient(ctx: Record<string, unknown> = {}) {
  freshBridgeContext(ctx)
  await import('./plugin-bridge-client')
  const sporemind = (window as any).sporemind
  const port = hostPort as MessagePort
  const frames: any[] = []
  port.onmessage = (e: MessageEvent) => {
    frames.push(e.data)
  }
  return { sporemind, port, frames }
}

describe('plugin-bridge-client (management plane)', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.restoreAllMocks()
    // Undo console patching from a previous import before re-importing.
    console.log = REAL_CONSOLE.log
    console.info = REAL_CONSOLE.info
    console.warn = REAL_CONSOLE.warn
    console.error = REAL_CONSOLE.error
    console.debug = REAL_CONSOLE.debug
    MockEventSource.instances = []
    MockWebSocket.instances = []
    vi.stubGlobal('EventSource', MockEventSource)
    vi.stubGlobal('WebSocket', MockWebSocket)
    delete (window as any).__sporemindEventChannels
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true }))
    delete (window as any).sporemind
    hostPort = undefined
    vi.spyOn(window.parent, 'postMessage').mockImplementation(((_msg: unknown, _target: unknown, transfer?: MessagePort[]) => {
      if (Array.isArray(transfer) && transfer[0]) {
        hostPort = transfer[0]
      }
    }) as any)
  })

  it('exposes the management surface and handshakes the port to the parent', async () => {
    const { sporemind } = await importClient()
    expect(sporemind).toBeDefined()
    expect(sporemind.pluginID).toBe('test.app')
    expect(sporemind.viewID).toBe('main')
    expect(typeof sporemind.subscribe).toBe('function')
    expect(typeof sporemind.agentAction).toBe('function')
    expect(typeof sporemind.openView).toBe('function')
    expect(typeof sporemind.dispose).toBe('function')
    expect(sporemind.context.pluginID).toBe('test.app')
    // Data-plane surface is gone from the management bridge.
    expect(sporemind.invoke).toBeUndefined()
    expect(sporemind.invokeStream).toBeUndefined()
    expect(sporemind.cast).toBeUndefined()
    expect(sporemind.emit).toBeUndefined()
    // The handshake transferred the host-side port.
    expect(hostPort).toBeDefined()
  })

  it('replies to dom-snapshot-request with a bounded document outline', async () => {
    document.body.innerHTML = `
      <div id="root" class="app main" data-test="hook">
        <section>
          <p>hello</p>
          <script>var x = 1;</script>
          <style>.a{color:red}</style>
        </section>
      </div>`
    const { port, frames } = await importClient()
    port.postMessage({ type: 'sporemind:dom-snapshot-request' })
    await vi.waitFor(() => {
      expect(frames.some((f) => f?.type === 'sporemind:dom-snapshot')).toBe(true)
    })
    const reply = frames.find((f) => f?.type === 'sporemind:dom-snapshot')
    expect(typeof reply.snapshot).toBe('string')
    expect(reply.snapshot).toContain('div#root.app.main')
    expect(reply.snapshot).toContain('[data-test=hook]')
    expect(reply.snapshot).toContain('p "hello"')
    // Script/style are listed as tags only — no text content.
    expect(reply.snapshot).not.toContain('var x = 1')
    expect(reply.snapshot).not.toContain('color:red')
  })

  it('truncates huge documents within the snapshot size cap', async () => {
    document.body.innerHTML = `<div><p>${'x'.repeat(5000)}</p></div>`
    const { port, frames } = await importClient()
    port.postMessage({ type: 'sporemind:dom-snapshot-request' })
    await vi.waitFor(() => {
      expect(frames.some((f) => f?.type === 'sporemind:dom-snapshot')).toBe(true)
    })
    const reply = frames.find((f) => f?.type === 'sporemind:dom-snapshot')
    expect(reply.snapshot.length).toBeLessThanOrEqual(33 * 1024)
    expect(reply.snapshot).toContain('...(truncated)')
  })

  it('sends ui-action frames on the port for the host to dispatch', async () => {
    const { sporemind, port, frames } = await importClient()
    sporemind.openView('settings')
    sporemind.setTitle('New Title')
    sporemind.setBadge('5')
    sporemind.notify('info', 'Hi', 'Body')
    await vi.waitFor(() => {
      expect(frames.filter((f) => f?.Type === 'sporemind.ui-action').length).toBeGreaterThanOrEqual(4)
    })
    const actions = frames.filter((f) => f?.Type === 'sporemind.ui-action').map((f) => f.Action)
    expect(actions).toEqual(['open-view', 'set-title', 'set-badge', 'notify'])
    const open = frames.find((f) => f?.Action === 'open-view')
    expect(open).toMatchObject({ PluginId: 'test.app', ViewId: 'settings' })
    expect(open.RequestId).toBeTruthy()
    void port
  })

  it('forwards console output as fire-and-forget console-log frames', async () => {
    const { frames } = await importClient()
    console.log('hello', { a: 1 })
    console.error('boom')
    await vi.waitFor(() => {
      expect(frames.filter((f) => f?.Type === 'sporemind.console-log').length).toBeGreaterThanOrEqual(2)
    })
    const logs = frames.filter((f) => f?.Type === 'sporemind.console-log')
    expect(logs.some((f) => f.Level === 'info' && f.Message.includes('hello'))).toBe(true)
    expect(logs.some((f) => f.Level === 'error' && f.Message.includes('boom'))).toBe(true)
    expect(logs[0]).toMatchObject({ PluginId: 'test.app', ViewId: 'main' })
    // Fire-and-forget: no response frame for console-log.
    expect(frames.some((f) => f?.Type === 'sporemind.console-log-response')).toBe(false)
  })

  it('subscribes to events over the shared WebSocket channel and unsubscribes cleanly', async () => {
    const { sporemind } = await importClient()
    const handler = vi.fn()
    const unsubscribe = sporemind.subscribe('loginHappened', handler)

    const ws = MockWebSocket.instances[0]!
    expect(ws).toBeDefined()
    // Root-relative /events resolved against the panel origin, ws scheme.
    expect(ws.url).toBe('ws://localhost:3000/events')

    ws.open()
    ws.deliver('loginHappened', { user: 'ada' })
    expect(handler).toHaveBeenCalledTimes(1)
    expect(handler).toHaveBeenCalledWith({ user: 'ada' })

    // A second subscriber on the same kind shares the single connection.
    const handler2 = vi.fn()
    const unsubscribe2 = sporemind.subscribe('loginHappened', handler2)
    expect(MockWebSocket.instances).toHaveLength(1)
    ws.deliver('loginHappened', { user: 'bob' })
    expect(handler).toHaveBeenCalledTimes(2)
    expect(handler2).toHaveBeenCalledTimes(1)

    unsubscribe()
    ws.deliver('loginHappened', { user: 'c' })
    expect(handler).toHaveBeenCalledTimes(2)
    expect(handler2).toHaveBeenCalledTimes(2)

    unsubscribe2()
    // Last subscriber out: the channel tears down its connection.
    expect(ws.closed).toBe(true)
    expect((window as any).__sporemindEventChannels.get('/events')).toBeUndefined()
  })

  it('reuses a window-level channel created by the generated client (one connection per URL)', async () => {
    const shared: any[] = []
    ;(window as any).__sporemindEventChannels = new Map([
      [
        '/events',
        {
          subscribe(kind: string, cb: (kind: string, payload: unknown) => void) {
            shared.push({ kind, cb })
            return () => {}
          },
        },
      ],
    ])
    const { sporemind } = await importClient()
    sporemind.subscribe('evt', () => {})
    // No new WebSocket: the pre-existing channel was joined.
    expect(MockWebSocket.instances).toHaveLength(0)
    expect(shared).toHaveLength(1)
    expect(shared[0].kind).toBe('evt')
  })

  it('suspend closes the live connection; resume re-dials it', async () => {
    const { sporemind } = await importClient()
    const handler = vi.fn()
    sporemind.subscribe('evt', handler)
    const ws = MockWebSocket.instances[0]!
    ws.open()
    expect(ws.closed).toBe(false)

    const channel = (window as any).__sporemindEventChannels.get('/events')
    channel.suspend()
    expect(ws.closed).toBe(true)
    // Deliveries from the old socket are dead; no reconnect storm either.
    ws.deliver('evt', { n: 1 })
    expect(handler).toHaveBeenCalledTimes(0)
    expect(MockWebSocket.instances).toHaveLength(1)

    channel.resume()
    expect(MockWebSocket.instances).toHaveLength(2)
    const ws2 = MockWebSocket.instances[1]!
    ws2.open()
    ws2.deliver('evt', { n: 2 })
    expect(handler).toHaveBeenCalledTimes(1)
    expect(handler).toHaveBeenCalledWith({ n: 2 })
  })

  it('falls back to SSE when the WS handshake is refused (older SDK)', async () => {
    const { sporemind } = await importClient()
    const handler = vi.fn()
    sporemind.subscribe('evt', handler)

    const ws = MockWebSocket.instances[0]!
    // Refused upgrade: close fires before onopen ever did.
    ws.failHandshake()

    const es = MockEventSource.instances[0]!
    expect(es).toBeDefined()
    expect(es.url).toBe('/events')
    expect(es.withCredentials).toBe(true)
    es.emit('evt', '{"n":1}')
    expect(handler).toHaveBeenCalledWith({ n: 1 })
    // And no reconnect storm: exactly one WS dial happened.
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  it('dispose releases the shared channel and closes its connection', async () => {
    const { sporemind } = await importClient()
    sporemind.subscribe('evt', () => {})
    const ws = MockWebSocket.instances[0]!
    expect(ws.closed).toBe(false)

    sporemind.dispose('test teardown')
    expect(ws.closed).toBe(true)

    // A later subscription opens a fresh connection.
    sporemind.subscribe('evt2', () => {})
    const ws2 = MockWebSocket.instances[1]!
    expect(ws2).toBeDefined()
    expect(ws2.closed).toBe(false)
  })

  // Regression: unsubscribing the last handler of a kind and re-subscribing
  // must not double-dispatch. Without the generated client's pinned "*"
  // subscription the channel refcounts to zero and closes; the re-subscribe
  // dials a fresh connection that sees only the new handler.
  it('does not double-dispatch when a kind is unsubscribed and re-subscribed', async () => {
    const { sporemind } = await importClient()
    const first = vi.fn()
    const unsubscribeFirst = sporemind.subscribe('evtResub', first)
    const ws = MockWebSocket.instances[0]!
    ws.open()

    unsubscribeFirst()
    const second = vi.fn()
    sporemind.subscribe('evtResub', second)
    expect(MockWebSocket.instances).toHaveLength(2)

    ws.deliver('evtResub', { n: 1 })
    const ws2 = MockWebSocket.instances[1]!
    ws2.open()
    ws2.deliver('evtResub', { n: 2 })
    expect(first).toHaveBeenCalledTimes(0)
    expect(second).toHaveBeenCalledTimes(1)
    expect(second).toHaveBeenCalledWith({ n: 2 })
  })

  it('exposes appBase from the injected bridge context for the generated client', async () => {
    const { sporemind } = await importClient({ appBase: '/plugin/test.app' })
    expect(sporemind.context.appBase).toBe('/plugin/test.app')
  })

  it('exposes the host locale from <html lang>, falling back to the browser language', async () => {
    document.documentElement.setAttribute('lang', 'zh-CN')
    const { sporemind } = await importClient()
    expect(sporemind.locale).toBe('zh-CN')

    // Live update path: the snippet rewrites lang on sporemind:locale-update,
    // so the getter must read the attribute at call time.
    document.documentElement.setAttribute('lang', 'ja-JP')
    expect(sporemind.locale).toBe('ja-JP')

    // Standalone dev (no host handshake): lang is never applied, fall back
    // to navigator.language.
    document.documentElement.removeAttribute('lang')
    expect(sporemind.locale).toBe(navigator.language || 'en-US')
    document.documentElement.setAttribute('lang', 'en-US')
  })

  it('never bootstraps a session cookie, even with legacy context fields', async () => {
    // The direct-HTTP cookie bootstrap is retired: the panel talks through
    // the gateway (appBase) and session auth, so importing the client with
    // leftover backendUrl/cookieToken context must not fire any fetch.
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    vi.stubGlobal('fetch', fetchMock)
    await importClient({ backendUrl: 'http://127.0.0.1:4100/', cookieToken: 'sess.tok' })
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
