import { GosporeClient, WebSocketTransport, type Transport, type ManagedTransport } from '@qomos/gospore-client'
import { SchemaRegistry, BinaryCodec } from '@qomos/spore-ts'
import { getToken, clearAuth, getRefreshToken, setToken, setRefreshToken } from './auth-store'
import {
  getWsUrl,
  resolveWailsGateway,
  resolveCapacitorGatewayFromParent,
} from './gateway'
import { isWails, isCapacitor, isWeb, isIframe } from './runtime'
import { WailsRawTransport } from './wails-raw-transport'
import { getDesktopConfig } from './desktop-config'
import { reportDiagnostic } from '../gen-clients/oracle/client'
import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import * as clients from '../gen-clients/index.js'
import * as userAuth from '../gen-clients/user/client'

function buildSchemaRegistry(): SchemaRegistry {
  const reg = new SchemaRegistry()
  const modules = Object.values(clients) as Array<{ schemaEntries?: import('@qomos/spore-ts/registry').SchemaEntry[] }>
  for (const mod of modules) {
    if (mod.schemaEntries) {
      for (const entry of mod.schemaEntries) {
        reg.register(entry)
      }
    }
  }
  return reg
}

const schemaRegistry = buildSchemaRegistry()
const binaryCodec = new BinaryCodec()

function tokenSuffix(): string {
  const token = getToken()
  return token ? `?token=${encodeURIComponent(token)}` : ''
}

// ── Reconnect state subscription ──
export type ReconnectState = 'connected' | 'reconnecting' | 'error'

const reconnectListeners = new Set<(state: ReconnectState) => void>()
let reconnectState: ReconnectState = 'connected'

// ── Auth retry state ──
const MAX_AUTH_RETRIES = 3
const MAX_TOTAL_FAILURES = 8
let authFailCount = 0
let isRefreshing = false

function notifyReconnectState(state: ReconnectState): void {
  if (state === reconnectState) return
  reconnectState = state
  for (const l of reconnectListeners) {
    try {
      l(state)
    } catch (err) {
      console.error('[generated-client] reconnect listener threw:', err)
    }
  }
}

export function onReconnectStateChange(cb: (state: ReconnectState) => void): () => void {
  reconnectListeners.add(cb)
  cb(reconnectState)
  return () => reconnectListeners.delete(cb)
}

function handleAuthFailure(): void {
  if (isRefreshing) return
  authFailCount++
  console.warn(`[auth] failure #${authFailCount}`)

  if (authFailCount >= MAX_AUTH_RETRIES && getRefreshToken()) {
    isRefreshing = true
    performReactiveRefresh()
      .then((ok) => {
        isRefreshing = false
        if (ok) {
          authFailCount = 0
          console.log('[auth] reactive refresh succeeded, transport will reconnect with new token')
        } else {
          doRedirect()
        }
      })
      .catch(() => {
        isRefreshing = false
        doRedirect()
      })
    return
  }

  if (authFailCount >= MAX_TOTAL_FAILURES) {
    doRedirect()
  }
}

function doRedirect(): void {
  clearAuth()
  if (isIframe()) {
    window.parent.postMessage({ type: 'token-invalid', data: { reason: 'Authentication failed' } }, '*')
  } else {
    window.location.reload()
  }
}

function createWebSocketTransport(): WebSocketTransport {
  const transport = new WebSocketTransport({
    url: getWsUrl(),
    getUrl: () => getWsUrl() + tokenSuffix(),
    lazyConnect: true,
    getAuthToken: () => getToken(),
    onAuthFailure: handleAuthFailure,
    onReconnecting: () => notifyReconnectState('reconnecting'),
    onError: () => notifyReconnectState('error'),
    useBinary: true,
    binaryOnly: true,
    schemaRegistry,
    binaryCodec,
    invokeTimeoutMs: 120000,
    reconnectBaseMs: 500,
    reconnectMaxMs: 15000,
    reconnectJitterMs: 1000,
    heartbeatIntervalMs: 15000,
    heartbeatTimeoutMs: 10000,
  })
  transport.onConnected(() => {
    authFailCount = 0
    notifyReconnectState('connected')
  })
  return transport
}

function createWailsRawTransport(): WailsRawTransport {
  // Channel-loss observability: every transId gap is rate-limited-reported
  // into the oracle diagnostics ring (ProblemsPanel) with the cumulative
  // gapStats attached, so multi-agent frame loss on the Wails channel is
  // quantifiable and attributable (Go-side fan-out drops surface separately
  // as events-store diagnostics from the oracle's events-stats tick).
  let lastGapReportAt = 0
  const transport = new WailsRawTransport({
    url: getWsUrl(),
    lazyConnect: true,
    getAuthToken: () => getToken(),
    onAuthFailure: handleAuthFailure,
    onReconnecting: () => notifyReconnectState('reconnecting'),
    onError: () => notifyReconnectState('error'),
    useBinary: true,
    binaryOnly: true,
    schemaRegistry,
    binaryCodec,
    invokeTimeoutMs: 120000,
    // Mirror the WebSocket transport's liveness knobs (see
    // createWebSocketTransport): a 15s ping / 10s timeout pair detects a dead
    // Wails session faster than the ManagedFrameTransport defaults (30s), so
    // a Go-side force-close surfaces as a reconnect instead of a silent
    // 30s-long dead stream.
    reconnectBaseMs: 500,
    heartbeatIntervalMs: 15000,
    heartbeatTimeoutMs: 10000,
    onTransIdGap: (info) => {
      const now = Date.now()
      if (now - lastGapReportAt < 30_000) return
      lastGapReportAt = now
      void reportDiagnostic(client, {
        Severity: 'warning',
        Source: 'wails-raw',
        Message: `transId ${info.kind}: got=${info.got} expected=${info.expected} lost=${info.lost} (rate-limited 30s; cumulative in Output)`,
        Output: JSON.stringify(transport.gapStats()),
      }).catch(() => { /* diagnostics are best-effort */ })
    },
  })
  transport.onConnected(() => {
    authFailCount = 0
    notifyReconnectState('connected')
  })
  return transport
}

export function createTransport(): Transport {
  if (isWails()) {
    const cfg = getDesktopConfig()
    if (cfg?.transport === 'ws') {
      return createWebSocketTransport()
    }
    return createWailsRawTransport()
  }
  return createWebSocketTransport()
}

export let client: GosporeClient = new GosporeClient(createTransport())

let lastForceReconnectAt = 0

export function forceReconnectClient(): void {
  // App resume surfaces through several near-simultaneous signals
  // (postMessage 'app-resumed', visibilitychange, online). Collapse them
  // into a single reconnect so we don't tear down a socket that is still
  // being established by the signal that arrived milliseconds earlier.
  const now = Date.now()
  if (now - lastForceReconnectAt < 1000) return
  lastForceReconnectAt = now
  const transport = client.getTransport() as ManagedTransport
  notifyReconnectState('reconnecting')
  transport.reconnect()
}

export function reconnectIfDisconnected(): void {
  if (reconnectState === 'connected') return
  forceReconnectClient()
}

export async function connectClient(): Promise<void> {
  const transport = client.getTransport()
  if (transport instanceof WebSocketTransport) {
    if (isWails()) {
      const gwResolved = await resolveWailsGateway()
      // The in-process gateway binds its HTTP listener only after every actor
      // finished OnStart; dialing earlier gets connection-refused and lands in
      // the 0.5–1.5s reconnect backoff — the dominant ws-vs-wails startup gap.
      // Wails-raw mode blocks on GatewayReady in the backend; mirror that here.
      if (gwResolved) {
        try {
          const ok = await desktop.WaitGatewayReady(12_000)
          if (!ok) console.warn('[gateway] WaitGatewayReady timed out; dialing anyway')
        } catch (err) {
          console.warn('[gateway] WaitGatewayReady unavailable, dialing anyway', err)
        }
      }
    } else if (isCapacitor() || isWeb()) {
      await resolveCapacitorGatewayFromParent().catch((err) => {
        // web-iframe without parent handshake is fine — location-host is already
        // captured by getWsUrl. Only propagate if location-host is also missing.
        const s = (err as Error)?.message ?? ''
        if (!/parent-handshake/.test(s)) throw err
      })
    }
  }
  const mt = transport as ManagedTransport
  mt.connect()
}

export const AUTH_CONNECTION_TIMEOUT_MS = 10_000

export function waitForClientReady(timeoutMs?: number): Promise<void> {
  const transport = client.getTransport() as ManagedTransport
  const ready = transport.waitUntilReady()
  if (typeof timeoutMs !== 'number' || timeoutMs <= 0) return ready

  return new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error(`WebSocket ready timeout after ${timeoutMs}ms`)),
      timeoutMs,
    )
    ready.then(
      () => {
        clearTimeout(timer)
        resolve()
      },
      (err) => {
        clearTimeout(timer)
        reject(err)
      },
    )
  })
}

export { waitWailsReady } from './wails-bridge'

/** Reactive refresh: create a temporary anonymous WebSocket connection,
 *  call user.auth_refresh to get a fresh token pair, then close the temp
 *  connection. Used when the main transport is down and the access token
 *  has expired. Returns true on success (tokens updated via auth-store),
 *  false on failure. */
export async function performReactiveRefresh(): Promise<boolean> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) return false

  console.log('[Auth] reactive refresh: creating temporary connection')
  let tempClient: GosporeClient | null = null
  try {
    const tempTransport = new WebSocketTransport({
      url: getWsUrl(),
      lazyConnect: true,
      useBinary: true,
      binaryOnly: true,
      schemaRegistry,
      binaryCodec,
      invokeTimeoutMs: 15000,
      reconnectBaseMs: 500,
      reconnectMaxMs: 3000,
      maxReconnectAttempts: 1,
    })
    tempClient = new GosporeClient(tempTransport)
    tempTransport.connect()
    await (tempTransport as ManagedTransport).waitUntilReady()

    const resp = await userAuth.authRefresh(tempClient, { RefreshToken: refreshToken })
    setToken(resp.Token)
    setRefreshToken(resp.RefreshToken)
    console.log('[Auth] reactive refresh OK')
    return true
  } catch (err) {
    console.error('[Auth] reactive refresh failed:', err)
    return false
  } finally {
    if (tempClient) {
      try { (tempClient.getTransport() as ManagedTransport).close?.(); } catch {}
    }
  }
}
