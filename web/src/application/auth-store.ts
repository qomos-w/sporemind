import { client, waitWailsReady, connectClient, waitForClientReady, AUTH_CONNECTION_TIMEOUT_MS } from './generated-client'
import * as userAuth from '../gen-clients/user/client'
import * as wailsApp from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import type { AccountView } from '../gen-clients/system/types'
import { isCapacitor } from './runtime'

let _token: string | null = null
let _refreshToken: string | null = null
let _account: AccountView | null = null
let _proactiveTimer: ReturnType<typeof setTimeout> | null = null

export function getToken(): string | null {
  return _token
}

export function setToken(token: string | null) {
  _token = token
}

export function getRefreshToken(): string | null {
  return _refreshToken
}

export function setRefreshToken(token: string | null) {
  _refreshToken = token
}

export function getAccount(): AccountView | null {
  return _account
}

export function setAccount(acc: AccountView | null) {
  _account = acc
}

/** Parse JWT payload without verification. Returns exp timestamp (seconds) or null. */
function parseTokenExp(token: string): number | null {
  try {
    const payload = token.split('.')[1]
    if (!payload) return null
    const json = atob(payload.replace(/-/g, '+').replace(/_/g, '/'))
    const obj = JSON.parse(json) as { exp?: number }
    return obj.exp ?? null
  } catch {
    return null
  }
}

export function isLoggedIn(): boolean {
  if (!_token) return false
  const exp = parseTokenExp(_token)
  if (exp && Date.now() >= exp * 1000) {
    clearAuth()
    return false
  }
  return true
}

export function isAdmin(): boolean {
  return _account?.Roles.includes('admin') ?? false
}

export function isDeveloper(): boolean {
  return _account?.Roles.includes('developer') ?? false
}

export function clearAuth() {
  _token = null
  _refreshToken = null
  _account = null
  if (_proactiveTimer) {
    clearTimeout(_proactiveTimer)
    _proactiveTimer = null
  }
}

export async function refreshMe(): Promise<boolean> {
  if (!_token) return false
  try {
    const acc = await userAuth.authMe(client)
    _account = acc
    return true
  } catch {
    clearAuth()
    return false
  }
}

/** Detect Capacitor iframe mode (mobile app wrapping web frontend).
 *  window.Capacitor is only injected in the native WebView shell — NOT in
 *  the cross-origin iframe that loads the remote web app.  So we detect
 *  the iframe by self !== top.  Wails WebView is top-level, so this
 *  returns false there.
 *
 *  Re-exported from runtime.ts — the canonical source now requires the
 *  sporemind mobile shell contract (?server= query param) so generic web
 *  iframes are not mistakenly treated as the mobile shell. */
export const isCapacitorMode = isCapacitor

/** Try login from URL query parameter ?token=xxx.
 *  Used for direct-link login (not Capacitor — that uses postMessage). */
export async function tryUrlTokenLogin(): Promise<boolean> {
  if (typeof window === 'undefined') return false
  const params = new URLSearchParams(window.location.search)
  const token = params.get('token')
  if (!token) {
    console.log('[Auth] No token in URL')
    return false
  }

  console.log('[Auth] Token found in URL, connecting...')
  _token = token
  await connectClient()
  try {
    await waitForClientReady(AUTH_CONNECTION_TIMEOUT_MS)
    console.log('[Auth] WebSocket ready')
  } catch (err) {
    console.error('[Auth] WebSocket connection/auth failed:', err)
    clearAuth()
    throw err
  }
  try {
    const acc = await userAuth.authMe(client)
    _account = acc
    console.log('[Auth] user.me OK, account:', acc?.Username)
  } catch (e) {
    console.warn('[Auth] user.me failed:', e)
    // token valid but me failed — keep token, account stays null
  }
  return true
}

/** Request token from Capacitor parent via postMessage.
 *  Replaces URL-based token passing to avoid leaking credentials into
 *  browser history / Referer and losing them on internal navigation.
 *  Returns {ok:true} on successful WebSocket auth, or {ok:false, reason}
 *  so the caller can surface a useful message. */
export async function requestCapacitorToken(): Promise<{ ok: boolean; reason?: string }> {
  if (!isCapacitorMode()) return { ok: false, reason: 'not-capacitor-mode' }
  return new Promise<{ ok: boolean; reason?: string }>((resolve) => {
    let resolved = false
    const done = (result: { ok: boolean; reason?: string }) => {
      if (resolved) return
      resolved = true
      window.removeEventListener('message', handler)
      resolve(result)
    }
    const handler = (event: MessageEvent) => {
      const msg = event.data || {}
      if (msg.type === 'token-response') {
        if (msg.token) {
          console.log('[Auth] Token received from Capacitor parent')
          _token = msg.token
          if (msg.refreshToken) {
            _refreshToken = msg.refreshToken
          }
          connectClient()
            .then(() => waitForClientReady(AUTH_CONNECTION_TIMEOUT_MS))
            .then(() => {
              return userAuth.authMe(client)
                .then((acc) => {
                  _account = acc
                  console.log('[Auth] user.me OK, account:', acc?.Username)
                })
                .catch((e) => {
                  console.warn('[Auth] user.me failed:', e)
                })
            })
            .then(() => done({ ok: true }))
            .catch((err) => {
              console.error('[Auth] WebSocket connection failed:', err)
              clearAuth()
              done({ ok: false, reason: 'websocket-failed' })
            })
        } else {
          console.log('[Auth] Capacitor parent returned no token')
          done({ ok: false, reason: 'no-saved-token' })
        }
      }
    }
    window.addEventListener('message', handler)
    window.parent.postMessage({ type: 'request-token' }, '*')
    // Mobile iframe reload + WebSocket handshake can take longer than 500ms on
    // slow networks; give it a generous window before treating it as a failure.
    setTimeout(() => {
      console.log('[Auth] Capacitor token request timed out')
      done({ ok: false, reason: 'token-request-timeout' })
    }, 1500)
  })
  .then((result) => {
    if (result.ok) {
      startProactiveRefreshTimer()
    }
    return result
  })
}

/** Set up postMessage listener for Capacitor parent token refresh. */
export function setupCapacitorTokenBridge(): void {
  if (!isCapacitorMode()) return
  window.addEventListener('message', async (event) => {
    const msg = event.data || {}
    if (msg.type === 'token-updated' && msg.token) {
      setToken(msg.token)
      if (msg.refreshToken) {
        setRefreshToken(msg.refreshToken)
      }
      startProactiveRefreshTimer()
      try {
        await connectClient()
        await waitForClientReady(AUTH_CONNECTION_TIMEOUT_MS)
      } catch (err) {
        console.warn('[Auth] token-updated reconnect failed:', err)
        return
      }
      try {
        await refreshMe()
      } catch (err) {
        console.warn('[Auth] refreshMe failed after token update:', err)
      }
    }
  })
}

/** Try auto-login via Wails desktop binding (admin token).
 *
 *  The desktop frontend now uses the same WebSocket transport as the web
 *  frontend. We still fetch the admin token through the Wails binding
 *  GetAdminToken (process-level trust), then authenticate the WebSocket
 *  with that token and call user.me over the socket.
 *
 *  If `waitWailsReady()` confirms we are inside a Wails window, the binding
 *  MUST succeed — there is no HTTP fallback. A failure here is a real bug
 *  (Go backend not ready, binding proxy broken, etc.) and is thrown so the
 *  caller can surface it instead of silently hiding it behind a login form. */
export async function tryWailsAutoLogin(): Promise<boolean> {
  const ready = await waitWailsReady()
  if (!ready) return false

  const token = await wailsApp.GetAdminToken()
  if (!token) {
    throw new Error('GetAdminToken returned empty token in Wails mode')
  }

  _token = token
  await connectClient()
  try {
    await waitForClientReady(AUTH_CONNECTION_TIMEOUT_MS)
  } catch (err) {
    console.error('[Auth] WebSocket connection/auth failed:', err)
    throw err
  }

  try {
    const acc = await userAuth.authMe(client)
    _account = acc
    console.log('[Auth] Wails auto-login OK, account:', acc?.Username)
  } catch (err) {
    console.warn('[Auth] Wails me failed, using fallback account:', err)
    _account = {
      Id: 'local-admin',
      Username: 'admin',
      DisplayName: 'Admin (Desktop)',
      Roles: ['admin'],
      Groups: [],
      Status: 'active',
      CreatedAt: new Date().toISOString(),
    }
  }
  startDesktopTokenRefreshTimer()
  return true
}

/** Desktop-only: re-fetches the admin token from GetAdminToken() before
 *  expiry. The desktop doesn't use refresh tokens — GetAdminToken issues
 *  a fresh JWT every call via process-level trust. */
function startDesktopTokenRefreshTimer(): void {
  if (_proactiveTimer) {
    clearTimeout(_proactiveTimer)
    _proactiveTimer = null
  }
  if (!_token) return

  const exp = parseTokenExp(_token)
  if (!exp) return

  const remainingMs = exp * 1000 - Date.now()
  if (remainingMs <= 0) return

  const refreshAt = Math.max(remainingMs * 0.8, 30_000)
  console.log(`[Auth] desktop token refresh scheduled in ${Math.round(refreshAt / 1000)}s`)
  _proactiveTimer = setTimeout(async () => {
    _proactiveTimer = null
    try {
      const freshToken = await wailsApp.GetAdminToken()
      if (freshToken) {
        _token = freshToken
        console.log('[Auth] desktop token refresh OK')
        startDesktopTokenRefreshTimer()
      }
    } catch (err) {
      console.warn('[Auth] desktop token refresh failed:', err)
    }
  }, refreshAt)
}

/** Start a timer that refreshes the access token before it expires.
 *  Fires at 80% of the remaining TTL. If refresh succeeds, schedules the
 *  next timer. If refresh fails, logs a warning (the reactive path takes over). */
export function startProactiveRefreshTimer(): void {
  if (_proactiveTimer) {
    clearTimeout(_proactiveTimer)
    _proactiveTimer = null
  }
  if (!_token || !_refreshToken) return

  const exp = parseTokenExp(_token)
  if (!exp) return

  const expiresAtMs = exp * 1000
  const now = Date.now()
  const remainingMs = expiresAtMs - now
  if (remainingMs <= 0) return

  const refreshAt = Math.max(remainingMs * 0.8, 30_000)
  console.log(`[Auth] proactive refresh scheduled in ${Math.round(refreshAt / 1000)}s`)
  _proactiveTimer = setTimeout(async () => {
    _proactiveTimer = null
    if (!_refreshToken) return
    try {
      const resp = await userAuth.authRefresh(client, { RefreshToken: _refreshToken })
      _token = resp.Token
      _refreshToken = resp.RefreshToken
      console.log('[Auth] proactive refresh OK')
      startProactiveRefreshTimer()
    } catch (err) {
      console.warn('[Auth] proactive refresh failed:', err)
    }
  }, refreshAt)
}
