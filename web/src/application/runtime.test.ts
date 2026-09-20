import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  getRuntime,
  recomputeRuntime,
  isWails,
  isCapacitor,
  isIframe,
  isWeb,
} from './runtime'

describe('runtime detection', () => {
  const origWindow = globalThis.window

  beforeEach(() => {
    // Reset to a clean top-level "web" window between tests.
    globalThis.window = {
      location: {
        href: 'http://127.0.0.1:18080/',
        host: '127.0.0.1:18080',
        origin: 'http://127.0.0.1:18080',
        search: '',
      },
      self: globalThis.window as unknown as Window,
      top: globalThis.window as unknown as Window,
    } as unknown as Window & typeof globalThis
    // Suppress console.debug noise during tests.
    vi.spyOn(console, 'debug').mockImplementation(() => {})
    // Memo must be re-read against the fresh window object.
    recomputeRuntime()
  })

  afterEach(() => {
    globalThis.window = origWindow
    vi.restoreAllMocks()
  })

  it('detects wails mode when Windows native bridge is present', () => {
    ;(globalThis.window as any).chrome = { webview: { postMessage: () => {} } }
    const snap = recomputeRuntime()
    expect(snap.mode).toBe('wails')
    expect(snap.signals.hasWailsNativeBridge).toBe(true)
    expect(isWails()).toBe(true)
    expect(isCapacitor()).toBe(false)
    expect(isWeb()).toBe(false)
  })

  it('detects wails mode when macOS native bridge is present', () => {
    ;(globalThis.window as any).webkit = {
      messageHandlers: { external: { postMessage: () => {} } },
    }
    expect(recomputeRuntime().mode).toBe('wails')
  })

  it('detects wails mode when _wails.environment is set by native code', () => {
    ;(globalThis.window as any)._wails = { environment: { OS: 'windows' } }
    expect(recomputeRuntime().mode).toBe('wails')
  })

  it('does NOT detect wails mode when only window._wails.invoke is set (browser that imported @wailsio/runtime)', () => {
    // @wailsio/runtime sets window._wails.invoke = System.invoke on import
    // whenever a DOM exists. This is what tripped the old detection in a
    // regular browser. The native bridge must be the real signal.
    ;(globalThis.window as any)._wails = { invoke: () => {} }
    const snap = recomputeRuntime()
    expect(snap.mode).toBe('web')
    expect(snap.signals.hasWailsNativeBridge).toBe(false)
    expect(isWails()).toBe(false)
    expect(isWeb()).toBe(true)
  })

  it('detects web mode at top-level without any iframe/wails signals', () => {
    const snap = recomputeRuntime()
    expect(snap.mode).toBe('web')
    expect(isWeb()).toBe(true)
    expect(isIframe()).toBe(false)
    expect(isWails()).toBe(false)
  })

  it('does NOT misdetect a Capacitor-style WebView as wails when iframe', () => {
    // Capacitor on Android uses System WebView; some configurations expose
    // Chromium-style bridges that look superficially like Wails native bridges.
    // An iframe with such a bridge must still be classified as an iframe,
    // otherwise the gateway URL resolver will try /wails/runtime and 404.
    const iframeSelf = {} as Window
    ;(globalThis.window as any).self = iframeSelf
    ;(globalThis.window as any).top = globalThis.window as unknown as Window
    ;(globalThis.window as any).chrome = { webview: { postMessage: () => {} } }
    ;(globalThis.window as any).location.search = '?server=http%3A%2F%2F192.168.0.107%3A18080'
    const snap = recomputeRuntime()
    expect(snap.mode).toBe('capacitor-iframe')
    expect(snap.signals.hasWailsNativeBridge).toBe(true) // signal is still recorded
    expect(isWails()).toBe(false)
    expect(isCapacitor()).toBe(true)
  })

  it('detects capacitor-iframe when iframe + ?server= param present', () => {
    const iframeSelf = {} as Window
    ;(globalThis.window as any).self = iframeSelf
    ;(globalThis.window as any).top = globalThis.window as unknown as Window
    ;(globalThis.window as any).location.search = '?server=http%3A%2F%2F192.168.0.107%3A18080'
    const snap = recomputeRuntime()
    expect(snap.mode).toBe('capacitor-iframe')
    expect(snap.signals.serverParam).toBe('http://192.168.0.107:18080')
    expect(isCapacitor()).toBe(true)
    expect(isIframe()).toBe(true)
    expect(isWeb()).toBe(false)
  })

  it('detects web-iframe when iframe without ?server=', () => {
    const iframeSelf = {} as Window
    ;(globalThis.window as any).self = iframeSelf
    ;(globalThis.window as any).top = globalThis.window as unknown as Window
    const snap = recomputeRuntime()
    expect(snap.mode).toBe('web-iframe')
    expect(isIframe()).toBe(true)
    expect(isWeb()).toBe(true)
    expect(isCapacitor()).toBe(false)
  })

  it('parentOrigin stays null on cross-origin iframe without throwing', () => {
    const iframeSelf = {} as Window
    ;(globalThis.window as any).self = iframeSelf
    ;(globalThis.window as any).top = globalThis.window as unknown as Window
    // Simulate cross-origin access throwing
    Object.defineProperty(globalThis.window, 'parent', {
      get() {
        throw new Error('blocked a frame with origin')
      },
      configurable: true,
    })
    const snap = recomputeRuntime()
    expect(snap.signals.parentOrigin).toBeNull()
    expect(snap.mode).toBe('web-iframe')
  })

  it('recomputeRuntime invalidates the memo so getRuntime sees new state', () => {
    expect(getRuntime().mode).toBe('web')
    // Promote to iframe
    const iframeSelf = {} as Window
    ;(globalThis.window as any).self = iframeSelf
    ;(globalThis.window as any).top = globalThis.window as unknown as Window
    ;(globalThis.window as any).location.search = '?server=http%3A%2F%2Fexample.com'
    expect(getRuntime().mode).toBe('web') // still cached
    recomputeRuntime()
    expect(getRuntime().mode).toBe('capacitor-iframe')
  })

  it('iframe detection beats wails native bridge when both signals present', () => {
    // Sporemind's Wails desktop is always top-level, so an iframe with
    // Chromium-style bridges (e.g. Capacitor WebView) must classify as iframe.
    const iframeSelf = {} as Window
    ;(globalThis.window as any).self = iframeSelf
    ;(globalThis.window as any).top = globalThis.window as unknown as Window
    ;(globalThis.window as any).chrome = { webview: { postMessage: () => {} } }
    expect(recomputeRuntime().mode).toBe('web-iframe')
  })

  it('captures viteWsUrl from import.meta.env when set', () => {
    vi.stubEnv('VITE_WS_URL', 'ws://override:9999/ws')
    const snap = recomputeRuntime()
    expect(snap.signals.viteWsUrl).toBe('ws://override:9999/ws')
    vi.unstubAllEnvs()
  })
})
