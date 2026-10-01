import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest'
import {
  createTransport,
  connectClient,
  waitForClientReady,
  forceReconnectClient,
  getLocalGatewayClient,
  rebindClient,
  client,
} from './generated-client'
import { WebSocketTransport, type GosporeClient } from '@qomos/gospore-client'
import { WailsRawTransport } from './wails-raw-transport'
import { recomputeRuntime } from './runtime'
import { setDesktopConfigForTest } from './desktop-config'
import { __resetGatewayCacheForTests } from './gateway'

vi.mock('../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  GetGatewayAddr: vi.fn(async () => '127.0.0.1:18080'),
  WaitGatewayReady: vi.fn(async () => true),
  // wails-bridge module-init (wails mode) exposes this on window.
  EvalJSResult: vi.fn(),
}))

import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'

import * as generatedClients from '../gen-clients/index.js'
import { SchemaIDs } from '../gen-types/registry.js'

describe('createTransport', () => {
  beforeEach(() => {
    // Ensure a clean runtime cache for each test.
    recomputeRuntime()
  })

  afterEach(() => {
    delete (window as any).chrome
    delete (window as any).webkit
    delete (window as any).wails
    delete (window as any)._wails
    delete (window as any).go
    delete (window as any).runtime
    setDesktopConfigForTest(null)
    recomputeRuntime()
  })

  it('creates WebSocketTransport in web mode', () => {
    const t = createTransport()
    expect(t).toBeInstanceOf(WebSocketTransport)
    t.close()
  })

  it('creates WailsRawTransport in Wails mode', () => {
    ;(window as any).chrome = { webview: { postMessage: vi.fn() } }
    recomputeRuntime()

    const t = createTransport()
    expect(t).toBeInstanceOf(WailsRawTransport)
    t.close()
  })

  it('creates WebSocketTransport in Wails mode when configured to ws', () => {
    ;(window as any).chrome = { webview: { postMessage: vi.fn() } }
    recomputeRuntime()
    setDesktopConfigForTest({ transport: 'ws', gatewayAddr: ':18081', gatewayBindAddrs: [] })

    const t = createTransport()
    expect(t).toBeInstanceOf(WebSocketTransport)
    t.close()

    setDesktopConfigForTest(null)
  })
})

describe('client', () => {
  it('registers project.card generated clients and schemas', () => {
    expect(generatedClients.project.cardMount).toBeTypeOf('function')
    expect(generatedClients.project.cardUnmount).toBeTypeOf('function')
    expect(generatedClients.project.cardList).toBeTypeOf('function')
    expect(SchemaIDs.ProjectCardMountReq).toBe(3440)
    expect(SchemaIDs.ProjectCardListResp).toBe(3445)
  })

})

describe('forceReconnectClient', () => {
  const originalGetTransport = client.getTransport.bind(client)

  afterEach(() => {
    ;(client as any).getTransport = originalGetTransport
    vi.useRealTimers()
  })

  it('collapses burst resume signals into a single reconnect', () => {
    vi.useFakeTimers()
    const reconnect = vi.fn()
    ;(client as any).getTransport = () => ({ reconnect })

    // app-resumed postMessage + visibilitychange + online arrive together.
    forceReconnectClient()
    forceReconnectClient()
    forceReconnectClient()
    expect(reconnect).toHaveBeenCalledTimes(1)

    vi.advanceTimersByTime(1100)
    forceReconnectClient()
    expect(reconnect).toHaveBeenCalledTimes(2)
  })
})

describe('connectClient', () => {
  const originalGetTransport = client.getTransport.bind(client)
  const waitReadyMock = desktop.WaitGatewayReady as unknown as ReturnType<typeof vi.fn>
  let created = 0

  function stubTransport(): WebSocketTransport {
    return new WebSocketTransport({
      url: 'ws://127.0.0.1:18080/ws',
      lazyConnect: true,
      createSocket: () => {
        created++
        return {
          readyState: 0,
          binaryType: 'arraybuffer',
          send: () => {},
          close: () => {},
          onopen: null,
          onmessage: null,
          onclose: null,
          onerror: null,
        } as unknown as WebSocket
      },
    })
  }

  beforeEach(() => {
    created = 0
    waitReadyMock.mockReset()
    waitReadyMock.mockResolvedValue(true)
    ;(client as any).getTransport = () => stubTransport()
  })

  afterEach(() => {
    ;(client as any).getTransport = originalGetTransport
    delete (window as any).chrome
    __resetGatewayCacheForTests()
    recomputeRuntime()
  })

  it('waits for gateway readiness before dialing in wails ws mode', async () => {
    ;(window as any).chrome = { webview: { postMessage: vi.fn() } }
    recomputeRuntime()
    __resetGatewayCacheForTests()

    waitReadyMock.mockReset()
    waitReadyMock.mockReturnValue(new Promise<boolean>((resolve) => {
      setTimeout(() => resolve(true), 10_000)
    }))

    let connectErr: unknown
    const connectP = connectClient().catch((e) => { connectErr = e })
    await new Promise((r) => setTimeout(r, 50))

    if (connectErr) throw new Error(`connectClient failed early: ${connectErr}`)
    expect(waitReadyMock).toHaveBeenCalledTimes(1)
    expect(waitReadyMock).toHaveBeenCalledWith(12_000)
    expect(created).toBe(0)
    // Silence the unresolved promise (10s timer) for vitest cleanup.
    connectP.catch(() => {})
  })

  it('still dials when WaitGatewayReady rejects (older backend)', async () => {
    ;(window as any).chrome = { webview: { postMessage: vi.fn() } }
    recomputeRuntime()
    __resetGatewayCacheForTests()
    waitReadyMock.mockRejectedValue(new Error('no such binding'))

    await connectClient()
    expect(created).toBe(1)
  })

  it('does not wait for the wails binding in web mode', async () => {
    recomputeRuntime()
    __resetGatewayCacheForTests()

    await connectClient()
    expect(waitReadyMock).not.toHaveBeenCalled()
    expect(created).toBe(1)
  })
})

describe('waitForClientReady', () => {
  const originalGetTransport = client.getTransport.bind(client)

  afterEach(() => {
    ;(client as any).getTransport = originalGetTransport
  })

  it('rejects with a timeout when the transport never becomes ready', async () => {
    const fakeTransport = {
      waitUntilReady: () => new Promise<void>(() => {}),
    } as any
    ;(client as any).getTransport = () => fakeTransport

    await expect(waitForClientReady(50)).rejects.toThrow('WebSocket ready timeout after 50ms')
  })

  it('resolves when the transport becomes ready before the timeout', async () => {
    const fakeTransport = {
      waitUntilReady: () => Promise.resolve(),
    } as any
    ;(client as any).getTransport = () => fakeTransport

    await expect(waitForClientReady(50)).resolves.toBeUndefined()
  })
})

describe('getLocalGatewayClient (single wails-raw session per window)', () => {
  // Regression: the browser-manager pin used to construct its own
  // WailsRawTransport, and the backend keeps ONE raw session per window — the
  // pin's `connect` superseded (silently killed) the main session, so every
  // gateway call hung after boot. The local pin must REUSE the boot client.

  it('non-wails: returns the live shared client and follows rebinds', () => {
    const originalClient = client
    const originalGetTransport = client.getTransport.bind(client)
    try {
      const closeFn = vi.fn()
      ;(client as any).getTransport = () => ({ close: closeFn })

      expect(getLocalGatewayClient()).toBe(client)

      const next = { getTransport: () => ({ close: vi.fn() }) } as unknown as GosporeClient
      rebindClient(next)
      expect(client).toBe(next)
      // The previous non-local client is closed on rebind (swap semantics).
      expect(closeFn).toHaveBeenCalledTimes(1)
      expect(getLocalGatewayClient()).toBe(next)
    } finally {
      rebindClient(originalClient)
      ;(client as any).getTransport = originalGetTransport
    }
  })

  it('wails main window: pins to the boot client, never builds a second wails-raw session, and survives swaps', async () => {
    ;(window as any).chrome = { webview: { postMessage: vi.fn() } }
    try {
      vi.resetModules()
      const { WailsRawTransport: FreshWailsRawTransport } = await import('./wails-raw-transport')
      const fresh = await import('./generated-client')

      const bootClientRef = fresh.client
      const bootTransport = bootClientRef.getTransport()
      expect(bootTransport).toBeInstanceOf(FreshWailsRawTransport)
      expect(fresh.getLocalGatewayClient()).toBe(bootClientRef)

      const bootClose = vi.spyOn(bootTransport, 'close')
      const remoteClose = vi.fn()
      const remote = { getTransport: () => ({ close: remoteClose }) } as unknown as GosporeClient

      // Swap away to a remote instance: the local session must be preserved.
      fresh.rebindClient(remote)
      expect(fresh.client).toBe(remote)
      expect(bootClose).not.toHaveBeenCalled()
      expect(fresh.getLocalGatewayClient()).toBe(bootClientRef)

      // Swap back to local: reuses the preserved session, closes the remote.
      fresh.rebindClient(fresh.getLocalGatewayClient())
      expect(fresh.client).toBe(bootClientRef)
      expect(remoteClose).toHaveBeenCalledTimes(1)
      expect(bootClose).not.toHaveBeenCalled()
    } finally {
      delete (window as any).chrome
      recomputeRuntime()
      vi.resetModules()
    }
  })
})
