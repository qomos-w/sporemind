import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  GATEWAY_DEFAULT_WS,
  GatewayResolutionError,
  __resetGatewayCacheForTests,
  getGatewayUrls,
  getWsUrl,
  getHttpUrl,
  wsToHttp,
  validateGatewayUrl,
  resolveWailsGateway,
  resolveCapacitorGatewayFromParent,
  requestServerFromParent,
} from './gateway'
import { recomputeRuntime } from './runtime'

// Mock the Wails desktop binding — the auto-generated module hits @wailsio/runtime
// which is a real WebSocket-ish bridge we cannot exercise under happy-dom.
vi.mock('../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  GetGatewayAddr: vi.fn(),
}))

import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
const GetGatewayAddrMock = desktop.GetGatewayAddr as unknown as ReturnType<typeof vi.fn>

describe('gateway pure helpers', () => {
  it('wsToHttp strips /ws suffix and rewrites scheme', () => {
    expect(wsToHttp('ws://h:1/ws')).toBe('http://h:1')
    expect(wsToHttp('wss://h:1/ws')).toBe('https://h:1')
    expect(wsToHttp('ws://h:1')).toBe('http://h:1')
  })

  it('validateGatewayUrl accepts ws/wss with host', () => {
    expect(() => validateGatewayUrl('ws://localhost:18080/ws')).not.toThrow()
    expect(() => validateGatewayUrl('wss://example.com/ws')).not.toThrow()
  })

  it('validateGatewayUrl rejects malformed URLs', () => {
    expect(() => validateGatewayUrl('')).toThrow(GatewayResolutionError)
    expect(() => validateGatewayUrl('http://localhost:18080/ws')).toThrow(GatewayResolutionError)
    expect(() => validateGatewayUrl('ws://')).toThrow(GatewayResolutionError)
    expect(() => validateGatewayUrl('garbage')).toThrow(GatewayResolutionError)
  })
})

describe('gateway sync precedence', () => {
  const origWindow = globalThis.window

  beforeEach(() => {
    globalThis.window = {
      location: {
        href: 'http://127.0.0.1:18080/',
        host: '127.0.0.1:18080',
        origin: 'http://127.0.0.1:18080',
        protocol: 'http:',
        search: '',
      },
      self: globalThis.window as unknown as Window,
      top: globalThis.window as unknown as Window,
    } as unknown as Window & typeof globalThis
    __resetGatewayCacheForTests()
    vi.spyOn(console, 'debug').mockImplementation(() => {})
    recomputeRuntime()
    GetGatewayAddrMock.mockReset()
  })

  afterEach(() => {
    globalThis.window = origWindow
    vi.restoreAllMocks()
    vi.unstubAllEnvs()
  })

  it('uses location-host in plain web mode', () => {
    const urls = getGatewayUrls()
    expect(urls.source).toBe('location-host')
    expect(urls.ws).toBe('ws://127.0.0.1:18080/ws')
    expect(urls.http).toBe('http://127.0.0.1:18080')
  })

  it('VITE_WS_URL overrides location-host', () => {
    vi.stubEnv('VITE_WS_URL', 'ws://override:9999/ws')
    recomputeRuntime()
    const urls = getGatewayUrls()
    expect(urls.source).toBe('env')
    expect(urls.ws).toBe('ws://override:9999/ws')
  })

  it('?server= query param wins over location-host', () => {
    ;(globalThis.window as any).location.search = '?server=http%3A%2F%2F192.168.0.107%3A18080'
    recomputeRuntime()
    const urls = getGatewayUrls()
    expect(urls.source).toBe('query-param')
    expect(urls.ws).toBe('ws://192.168.0.107:18080/ws')
  })

  it('VITE_WS_URL wins over ?server=', () => {
    vi.stubEnv('VITE_WS_URL', 'ws://env-wins:1/ws')
    ;(globalThis.window as any).location.search = '?server=http%3A%2F%2Fquery%3A18080'
    recomputeRuntime()
    expect(getGatewayUrls().source).toBe('env')
  })

  it('falls back to default when no signals (no window.location.host)', () => {
    delete (globalThis.window as any).location
    globalThis.window = { location: { protocol: 'http:' } } as unknown as Window & typeof globalThis
    // Force top-level so we don't hit iframe branches
    ;(globalThis.window as any).self = globalThis.window
    ;(globalThis.window as any).top = globalThis.window
    recomputeRuntime()
    const urls = getGatewayUrls()
    expect(urls.source).toBe('default')
    expect(urls.ws).toBe(GATEWAY_DEFAULT_WS)
  })

  it('wss scheme when page is https', () => {
    ;(globalThis.window as any).location.protocol = 'https:'
    ;(globalThis.window as any).location.host = 'secure.example.com:443'
    recomputeRuntime()
    expect(getWsUrl()).toBe('wss://secure.example.com:443/ws')
  })

  it('getHttpUrl mirrors getWsUrl scheme', () => {
    vi.stubEnv('VITE_WS_URL', 'wss://secure:9999/ws')
    recomputeRuntime()
    expect(getHttpUrl()).toBe('https://secure:9999')
  })
})

describe('resolveWailsGateway', () => {
  const origWindow = globalThis.window

  beforeEach(() => {
    globalThis.window = {
      location: { href: '', host: '', origin: '', protocol: 'http:', search: '' },
      self: globalThis.window as unknown as Window,
      top: globalThis.window as unknown as Window,
      // Native bridge present = real Wails WebView. We can't rely on
      // window._wails.invoke alone since @wailsio/runtime sets it in any
      // browser that imports the package.
      chrome: { webview: { postMessage: () => {} } },
    } as unknown as Window & typeof globalThis
    __resetGatewayCacheForTests()
    vi.spyOn(console, 'debug').mockImplementation(() => {})
    recomputeRuntime()
    GetGatewayAddrMock.mockReset()
  })

  afterEach(() => {
    globalThis.window = origWindow
    vi.restoreAllMocks()
    vi.unstubAllEnvs()
  })

  it('returns ws URL from GetGatewayAddr binding', async () => {
    GetGatewayAddrMock.mockResolvedValue('192.168.1.5:18080')
    const urls = await resolveWailsGateway()
    expect(urls.source).toBe('wails-binding')
    expect(urls.ws).toBe('ws://192.168.1.5:18080/ws')
  })

  it('throws GatewayResolutionError when binding rejects', async () => {
    GetGatewayAddrMock.mockRejectedValue(new Error('binding gone'))
    await expect(resolveWailsGateway()).rejects.toBeInstanceOf(GatewayResolutionError)
  })

  it('in DEV mode with locationHost, falls back via Vite proxy when binding rejects', async () => {
    vi.stubEnv('DEV', true)
    ;(globalThis.window as any).location.host = 'wails.localhost:5558'
    recomputeRuntime()
    GetGatewayAddrMock.mockRejectedValue(new Error('binding gone'))
    const urls = await resolveWailsGateway()
    expect(urls.source).toBe('dev-fallback')
    // wails. prefix stripped so the request goes through the normal network
    // stack and hits Vite's /ws proxy rather than WebView2's intercepted host.
    expect(urls.ws).toBe('ws://localhost:5558/ws')
  })

  it('in PROD mode with locationHost, still throws when binding rejects', async () => {
    vi.stubEnv('DEV', false)
    ;(globalThis.window as any).location.host = 'wails.localhost:5558'
    recomputeRuntime()
    GetGatewayAddrMock.mockRejectedValue(new Error('binding gone'))
    await expect(resolveWailsGateway()).rejects.toBeInstanceOf(GatewayResolutionError)
  })

  it('throws when binding returns empty string', async () => {
    GetGatewayAddrMock.mockResolvedValue('')
    await expect(resolveWailsGateway()).rejects.toBeInstanceOf(GatewayResolutionError)
  })

  it('caches the result so subsequent calls do not re-invoke binding', async () => {
    GetGatewayAddrMock.mockResolvedValue('cached:18080')
    await resolveWailsGateway()
    await resolveWailsGateway()
    expect(GetGatewayAddrMock).toHaveBeenCalledTimes(1)
  })

  it('skips binding when VITE_WS_URL is set', async () => {
    vi.stubEnv('VITE_WS_URL', 'ws://env-override:1/ws')
    recomputeRuntime()
    const urls = await resolveWailsGateway()
    expect(urls.source).toBe('env')
    expect(GetGatewayAddrMock).not.toHaveBeenCalled()
  })

  it('throws when called outside wails mode', async () => {
    delete (globalThis.window as any).chrome
    recomputeRuntime()
    await expect(resolveWailsGateway()).rejects.toBeInstanceOf(GatewayResolutionError)
  })
})

describe('resolveCapacitorGatewayFromParent', () => {
  const origWindow = globalThis.window
  let listeners: Record<string, ((e: MessageEvent) => void)[]>
  let parentPostMessage: ReturnType<typeof vi.fn>

  beforeEach(() => {
    listeners = {}
    parentPostMessage = vi.fn()
    globalThis.window = {
      location: {
        href: 'http://iframe.host/',
        host: 'iframe.host',
        origin: 'http://iframe.host',
        protocol: 'http:',
        search: '',
      },
      // iframe shape — self !== top
      self: {} as Window,
      top: globalThis.window as unknown as Window,
      parent: {
        postMessage: parentPostMessage,
      } as unknown as Window,
      addEventListener: (_: string, fn: (e: MessageEvent) => void) => {
        listeners['message'] = listeners['message'] || []
        listeners['message'].push(fn)
      },
      removeEventListener: (_: string, fn: (e: MessageEvent) => void) => {
        const arr = listeners['message'] || []
        const idx = arr.indexOf(fn)
        if (idx >= 0) arr.splice(idx, 1)
      },
    } as unknown as Window & typeof globalThis
    __resetGatewayCacheForTests()
    vi.spyOn(console, 'debug').mockImplementation(() => {})
    recomputeRuntime()
  })

  afterEach(() => {
    globalThis.window = origWindow
    vi.restoreAllMocks()
    vi.unstubAllEnvs()
  })

  it('uses parent-handshake when parent responds with url', async () => {
    parentPostMessage.mockImplementation(() => {
      // Simulate parent replying asynchronously
      setTimeout(() => {
        for (const fn of listeners['message'] || []) {
          fn({ data: { type: 'server-response', url: 'http://192.168.0.107:18080' } } as MessageEvent)
        }
      }, 0)
    })
    const urls = await resolveCapacitorGatewayFromParent()
    expect(urls.source).toBe('parent-handshake')
    expect(urls.ws).toBe('ws://192.168.0.107:18080/ws')
  })

  it('falls back to location-host when parent times out', async () => {
    // parentPostMessage is a no-op (no reply) — wait for timeout by mocking it short
    const result = await Promise.race([
      resolveCapacitorGatewayFromParent(),
      new Promise((_, rej) => setTimeout(() => rej(new Error('test took too long')), 4000)),
    ])
    expect((result as any).source).toBe('location-host')
  }, 5000)

  it('skips handshake when ?server= is already in URL', async () => {
    ;(globalThis.window as any).location.search = '?server=http%3A%2F%2Fvia-param%3A18080'
    recomputeRuntime()
    const urls = await resolveCapacitorGatewayFromParent()
    expect(urls.source).toBe('query-param')
    expect(parentPostMessage).not.toHaveBeenCalled()
  })
})

describe('requestServerFromParent', () => {
  const origWindow = globalThis.window

  beforeEach(() => {
    vi.spyOn(console, 'debug').mockImplementation(() => {})
  })

  afterEach(() => {
    globalThis.window = origWindow
    vi.restoreAllMocks()
  })

  it('rejects immediately when not in an iframe', async () => {
    globalThis.window = {
      self: globalThis.window as unknown as Window,
      top: globalThis.window as unknown as Window,
    } as unknown as Window & typeof globalThis
    const result = await requestServerFromParent()
    expect(result.ok).toBe(false)
    expect(result.reason).toBe('not-iframe')
  })
})
