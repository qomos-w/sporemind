// Gateway URL resolution — single source of truth.
//
// Precedence (first match wins, evaluation short-circuits):
//   1. import.meta.env.VITE_WS_URL       dev-time override, beats everything
//   2. ?server= URL query param           set by the mobile shell on iframe src
//   3. Mode-specific async resolver:
//        wails             → GetGatewayAddr() binding (sporemind.yaml honoured)
//        capacitor-iframe  → already has ?server=, no async work
//        web-iframe        → requestServerFromParent() handshake, 300ms timeout
//   4. window.location.host/ws            only for web / web-iframe
//   5. GATEWAY_DEFAULT_WS                 last-resort constant, never silent
//
// Wails binding failure throws GatewayResolutionError — never silently falls
// back to localhost. The desktop frontend depends on the binding for
// sporemind.yaml overrides, so a broken binding is a real bug.

import type { RuntimeSignals } from './runtime'
import { getRuntime, isWails, isCapacitor, isWeb } from './runtime'
import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import type { AuthLoginReq, AuthLoginResp } from '../gen-clients/system/types'

// Default gateway address/port — single source of truth for the TS side.
// Must mirror pkg/config.DefaultGatewayAddr / DefaultGatewayPort on the Go
// side. Every module that needs a default or fallback must reference these
// instead of re-hardcoding a port.
export const GATEWAY_DEFAULT_PORT = 18080 as const
export const GATEWAY_DEFAULT_ADDR = '127.0.0.1:18080' as const
export const GATEWAY_DEFAULT_WS = `ws://${GATEWAY_DEFAULT_ADDR}/ws` as const

export type GatewaySource =
  | 'env'
  | 'query-param'
  | 'wails-binding'
  | 'parent-handshake'
  | 'location-host'
  | 'dev-fallback'
  | 'default'

export interface GatewayUrls {
  ws: string
  http: string
  source: GatewaySource
  resolvedAt: number
}

export class GatewayResolutionError extends Error {
  constructor(
    public readonly reason: string,
    public readonly signals: RuntimeSignals,
  ) {
    super(`gateway resolution failed: ${reason}`)
    this.name = 'GatewayResolutionError'
  }
}

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

export function wsToHttp(wsUrl: string): string {
  return wsUrl.replace(/^ws/, 'http').replace(/\/ws$/, '')
}

export function validateGatewayUrl(url: string): void {
  if (!url) throw new GatewayResolutionError('empty-url', getRuntime().signals)
  if (!/^wss?:\/\/[^/]+/.test(url)) {
    throw new GatewayResolutionError(`malformed-url: ${url}`, getRuntime().signals)
  }
}

function withSlashWs(origin: string): string {
  const cleaned = origin.replace(/\/$/, '')
  return cleaned.endsWith('/ws') ? cleaned : `${cleaned}/ws`
}

function httpToWs(httpUrl: string): string {
  return httpUrl.replace(/^http/, 'ws').replace(/\/$/, '') + '/ws'
}

// ---------------------------------------------------------------------------
// Sync URL resolution (no mode-specific async work)
// ---------------------------------------------------------------------------

function resolveSync(): GatewayUrls {
  // 0. test override (never set in production)
  if (testOverride) return testOverride

  const snap = getRuntime()
  const s = snap.signals

  // 1. env override
  if (s.viteWsUrl) {
    return finalize(s.viteWsUrl, 'env')
  }

  // 2. ?server= query
  if (s.serverParam) {
    const ws = s.serverParam.startsWith('ws')
      ? withSlashWs(s.serverParam)
      : httpToWs(s.serverParam)
    return finalize(ws, 'query-param')
  }

  // 3. Async-resolved cache (wails binding, capacitor parent handshake).
  //    connectClient() awaits these before transport.connect(), so by the
  //    time the transport's getUrl callback fires, the cache is populated.
  //    Without this check, the transport would fall through to the default
  //    URL even after a successful async resolution.
  if (snap.mode === 'wails' && wailsResolved) {
    return wailsResolved
  }
  if ((snap.mode === 'capacitor-iframe' || snap.mode === 'web-iframe') && capacitorResolved) {
    return capacitorResolved
  }

  // 4. location-host (web / web-iframe only)
  if ((snap.mode === 'web' || snap.mode === 'web-iframe') && s.locationHost) {
    const proto = s.locationHost && window?.location?.protocol === 'https:' ? 'wss:' : 'ws:'
    return finalize(`${proto}//${s.locationHost}/ws`, 'location-host')
  }

  // 5. default
  return finalize(GATEWAY_DEFAULT_WS, 'default')
}

function finalize(ws: string, source: GatewaySource): GatewayUrls {
  validateGatewayUrl(ws)
  const urls: GatewayUrls = { ws, http: wsToHttp(ws), source, resolvedAt: Date.now() }
  if (typeof console !== 'undefined') {
    console.debug('[gateway]', `ws=${urls.ws} http=${urls.http} source=${urls.source}`)
  }
  return urls
}

export function getGatewayUrls(): GatewayUrls {
  return resolveSync()
}

export function getWsUrl(): string {
  return getGatewayUrls().ws
}

export function getHttpUrl(): string {
  return getGatewayUrls().http
}

// Test hook: forces the resolved URLs ahead of every other source so sync
// consumers see a known gateway without a wails binding / capacitor
// handshake. No-op in production (never set outside tests).
let testOverride: GatewayUrls | null = null
export function __setGatewayUrlsForTest(urls: GatewayUrls | null): void {
  testOverride = urls
}

// ---------------------------------------------------------------------------
// Async resolution (wails binding, capacitor handshake)
// ---------------------------------------------------------------------------

let wailsResolved: GatewayUrls | null = null

export async function resolveWailsGateway(): Promise<GatewayUrls> {
  if (!isWails()) {
    throw new GatewayResolutionError('not-wails-mode', getRuntime().signals)
  }
  if (wailsResolved) return wailsResolved

  const snap = getRuntime()
  // env / query still win inside wails — only fall through to binding if neither set.
  if (snap.signals.viteWsUrl || snap.signals.serverParam) {
    wailsResolved = resolveSync()
    return wailsResolved
  }

  let addr: string
  try {
    addr = await desktop.GetGatewayAddr()
  } catch (err) {
    // Dev-only escape hatch: when the Wails binding misroutes (we have seen
    // /wails/runtime 404 in `make dev-desktop` setups), the in-process gateway
    // is still reachable through the Vite dev-server proxy. Strip the
    // wails.localhost prefix because WebView2 intercepts that host and the
    // assetserver rejects WebSocket upgrades. localhost:<port> goes through
    // the normal network stack and hits Vite's /ws proxy.
    // In production this is a real bug — the binding is the only way to learn
    // a sporemind.yaml override, so we still throw there.
    if (import.meta.env.DEV && snap.signals.locationHost) {
      const host = snap.signals.locationHost.replace(/^wails\./, '')
      console.warn(
        `[gateway] wails binding failed in dev; falling back to ws://${host}/ws via Vite proxy`,
        err,
      )
      wailsResolved = finalize(`ws://${host}/ws`, 'dev-fallback')
      return wailsResolved
    }
    throw new GatewayResolutionError(
      `wails-binding-threw: ${err instanceof Error ? err.message : String(err)}`,
      snap.signals,
    )
  }
  if (!addr) {
    throw new GatewayResolutionError('wails-binding-empty', snap.signals)
  }
  wailsResolved = finalize(`ws://${addr}/ws`, 'wails-binding')
  return wailsResolved
}

const PARENT_HANDSHAKE_TIMEOUT_MS = 300

export interface ParentHandshakeResult {
  ok: boolean
  url?: string
  reason?: string
}

export function requestServerFromParent(timeoutMs = PARENT_HANDSHAKE_TIMEOUT_MS): Promise<ParentHandshakeResult> {
  return new Promise((resolve) => {
    if (typeof window === 'undefined' || window.self === window.top) {
      resolve({ ok: false, reason: 'not-iframe' })
      return
    }
    let done = false
    const finish = (r: ParentHandshakeResult) => {
      if (done) return
      done = true
      window.removeEventListener('message', handler)
      clearTimeout(timer)
      resolve(r)
    }
    const handler = (event: MessageEvent) => {
      const msg = event.data || {}
      if (msg.type === 'server-response' && typeof msg.url === 'string') {
        finish({ ok: true, url: msg.url })
      }
    }
    window.addEventListener('message', handler)
    const timer = setTimeout(() => finish({ ok: false, reason: 'timeout' }), timeoutMs)
    window.parent.postMessage({ type: 'request-server' }, '*')
  })
}

let capacitorResolved: GatewayUrls | null = null

export async function resolveCapacitorGatewayFromParent(): Promise<GatewayUrls> {
  if (!isCapacitor() && !isWeb()) {
    // web-iframe can also try the handshake (shell may have omitted ?server=)
    throw new GatewayResolutionError('not-iframe-mode', getRuntime().signals)
  }
  if (capacitorResolved) return capacitorResolved

  const snap = getRuntime()
  // env / query always win — no need to ask the parent.
  if (snap.signals.viteWsUrl || snap.signals.serverParam) {
    capacitorResolved = resolveSync()
    return capacitorResolved
  }

  const result = await requestServerFromParent()
  if (result.ok && result.url) {
    const ws = result.url.startsWith('ws') ? withSlashWs(result.url) : httpToWs(result.url)
    capacitorResolved = finalize(ws, 'parent-handshake')
    return capacitorResolved
  }

  // Handshake failed. Fall back to location-host if available, else throw — we
  // are inside an iframe with no contract and no signal, that's a misconfig.
  if (snap.signals.locationHost) {
    capacitorResolved = resolveSync() // location-host branch
    return capacitorResolved
  }
  throw new GatewayResolutionError(
    `parent-handshake-${result.reason ?? 'failed'}`,
    snap.signals,
  )
}

// ---------------------------------------------------------------------------
// HTTP login (uses gateway HTTP URL)
// ---------------------------------------------------------------------------

export async function httpLogin(username: string, password: string): Promise<AuthLoginResp> {
  const httpUrl = getHttpUrl()
  const resp = await fetch(`${httpUrl}/api/user.auth_login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ Username: username, Password: password } as AuthLoginReq),
  })
  if (!resp.ok) {
    const text = await resp.text()
    throw new Error(text || `login failed: ${resp.status}`)
  }
  return resp.json() as Promise<AuthLoginResp>
}

// ---------------------------------------------------------------------------
// Test-only escape hatches
// ---------------------------------------------------------------------------

export function __resetGatewayCacheForTests(): void {
  wailsResolved = null
  capacitorResolved = null
}
