// Runtime environment detection — single source of truth.
//
// Modes (mutually exclusive):
//   wails             — desktop webview, native bridge present
//   capacitor-iframe  — iframe inside the sporemind mobile shell (signalled
//                       by ?server= query param that the shell always sets)
//   web-iframe        — iframe without the sporemind shell contract (generic
//                       embed, third-party host)
//   web               — top-level browser tab
//
// IMPORTANT: `window._wails.invoke` is NOT a reliable Wails signal. The
// @wailsio/runtime package sets `window._wails.invoke = System.invoke` on
// import whenever a DOM exists — so any browser that imports our auto-
// generated bindings (which transitively import @wailsio/runtime) ends up
// with `window._wails.invoke` populated, even though there is no native
// bridge. The reliable signals are the native bridges themselves:
//   - Windows:  window.chrome.webview.postMessage
//   - macOS/iOS: window.webkit.messageHandlers.external.postMessage
//   - Android:  window.wails.invoke (lowercase)
// Plus `window._wails.environment`, which is populated by the native side
// before the JS loads (not by the runtime package).
//
// Iframe detection takes PRECEDENCE over Wails detection because:
//   1. In sporemind the Wails desktop is always a top-level window, never
//      embedded as an iframe.
//   2. Capacitor / Cordova / generic WebView shells may expose Chromium-style
//      bridges that superficially resemble Wails native bridges; treating
//      them as Wails would route calls to /wails/runtime and fail with 404.
//   3. The Capacitor mobile contract (?server=) is unambiguous.
//
// Sync detection is best-effort. The handshake to confirm the parent really
// is the sporemind shell happens in gateway.ts (resolveCapacitorGatewayFromParent)
// — mode discrimination here only needs the URL-contract signal (?server=).

export type RuntimeMode = 'wails' | 'capacitor-iframe' | 'web-iframe' | 'web'

export interface RuntimeSignals {
  hasWindow: boolean
  hasWailsNativeBridge: boolean
  hasCapacitorBridge: boolean
  isIframe: boolean
  parentOrigin: string | null
  locationHref: string | null
  locationHost: string | null
  serverParam: string | null
  viteWsUrl: string | null
}

export interface RuntimeSnapshot {
  mode: RuntimeMode
  signals: RuntimeSignals
  detectedAt: number
}

function readSignals(): RuntimeSignals {
  const hasWindow = typeof window !== 'undefined'
  if (!hasWindow) {
    return {
      hasWindow: false,
      hasWailsNativeBridge: false,
      hasCapacitorBridge: false,
      isIframe: false,
      parentOrigin: null,
      locationHref: null,
      locationHost: null,
      serverParam: null,
      viteWsUrl: null,
    }
  }

  const w = window as unknown as {
    chrome?: { webview?: { postMessage?: unknown } }
    webkit?: { messageHandlers?: { external?: { postMessage?: unknown } } }
    wails?: { invoke?: unknown } // Android (lowercase)
    _wails?: { environment?: unknown; invoke?: unknown }
    Capacitor?: unknown
    self?: Window
    top?: Window
    parent?: Window
    location?: Location
  }

  // Real Wails WebView presence — see file header for why we can't trust
  // window._wails.invoke alone.
  const hasWailsNativeBridge =
    typeof w.chrome?.webview?.postMessage === 'function' ||
    typeof w.webkit?.messageHandlers?.external?.postMessage === 'function' ||
    typeof w.wails?.invoke === 'function' ||
    typeof w._wails?.environment === 'object'

  const hasCapacitorBridge = typeof w.Capacitor !== 'undefined'
  const isIframe = w.self !== w.top
  let parentOrigin: string | null = null
  if (isIframe) {
    try {
      parentOrigin = w.parent?.location?.origin ?? null
    } catch {
      // cross-origin — keep null, that's a valid signal too
    }
  }

  const locationHref = w.location?.href ?? null
  const locationHost = w.location?.host ?? null
  const search = w.location?.search ?? ''
  let serverParam: string | null = null
  if (search) {
    try {
      serverParam = new URLSearchParams(search).get('server')
    } catch {
      serverParam = null
    }
  }

  const viteWsUrl = (import.meta.env.VITE_WS_URL as string | undefined) ?? null

  return {
    hasWindow,
    hasWailsNativeBridge,
    hasCapacitorBridge,
    isIframe,
    parentOrigin,
    locationHref,
    locationHost,
    serverParam,
    viteWsUrl,
  }
}

function detectMode(s: RuntimeSignals): RuntimeMode {
  if (!s.hasWindow) return 'web'
  // Iframe detection beats Wails detection — see file header.
  if (s.isIframe) {
    // Discriminate by ?server= contract — the mobile shell always appends it
    // to the iframe src (see mobile/www/index.html loadWebView).
    return s.serverParam ? 'capacitor-iframe' : 'web-iframe'
  }
  if (s.hasWailsNativeBridge) return 'wails'
  return 'web'
}

let cached: RuntimeSnapshot | null = null

export function recomputeRuntime(): RuntimeSnapshot {
  const signals = readSignals()
  const mode = detectMode(signals)
  const snapshot: RuntimeSnapshot = { mode, signals, detectedAt: Date.now() }
  cached = snapshot
  if (typeof console !== 'undefined') {
    console.debug('[runtime]', RuntimeSnapshotToString(snapshot))
  }
  return snapshot
}

function RuntimeSnapshotToString(s: RuntimeSnapshot): string {
  const s2 = s.signals
  return `mode=${s.mode} wails=${s2.hasWailsNativeBridge} iframe=${s2.isIframe} server=${s2.serverParam ?? '-'} host=${s2.locationHost ?? '-'}`
}

export function getRuntime(): RuntimeSnapshot {
  if (cached) return cached
  return recomputeRuntime()
}

export function isWails(): boolean {
  return getRuntime().mode === 'wails'
}

export function isCapacitor(): boolean {
  return getRuntime().mode === 'capacitor-iframe'
}

export function isIframe(): boolean {
  const m = getRuntime().mode
  return m === 'capacitor-iframe' || m === 'web-iframe'
}

export function isWeb(): boolean {
  const m = getRuntime().mode
  return m === 'web' || m === 'web-iframe'
}
