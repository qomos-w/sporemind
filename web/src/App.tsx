import { useEffect, useRef, useState, useCallback } from 'react'
import { installConsolePatch } from '@qomos/sporemind-shell'
import { applyTheme } from '@qomos/sporemind-theme'
import type { ThemeState } from '@qomos/sporemind-theme'

function notifyShellTheme(state: ThemeState) {
  if (window.parent !== window) {
    window.parent.postMessage({ type: 'theme-change', mode: state.mode }, '*')
  }
}
import '@qomos/sporemind-theme/tokens.css'
import './ui/settings/shadcn/globals.css'
import './App.css'
import './ui/lib/keyboard.css'
import { AIShell } from './ui/ai/AIShell'
import { BrowserOverlayManager } from './ui/ai/BrowserOverlayManager'
import ScreenshotOverlay from './ui/screenshot/ScreenshotOverlay'
import { initialTheme, loadTheme } from './application/theme-persist'
import { applyAppBackground, loadAppBackground } from './application/app-background'
import './ui/ai/app-background.css'
import { loadLocale } from './application/locale-persist'
import { enableErrorCapture } from './ui/lib/error-capture'
import { CrashOverlay } from './ui/lib/CrashOverlay'
import { client, onReconnectStateChange, forceReconnectClient, reconnectIfDisconnected, type ReconnectState } from './application/generated-client'
import { isWails } from './application/runtime'
import { installConsoleLogPersist } from './application/console-log-persist'
import { destroyAllBrowserWindows, notifySplashDone } from './application/wails-bridge'
import * as workspace from './gen-clients/workspace/client'
import { LoginPage } from './ui/auth/LoginPage'
import { LoadingOverlay } from './ui/auth/LoadingOverlay'
import { SplashScreen } from './ui/auth/SplashScreen'
import { prefetchAgentList, isAgentListFetched } from './ui/ai/hooks/agentListStore'
import { prefetchShellContext, isShellContextFetched } from './application/shell-context-prefetch'
import type { OverlayState } from './ui/auth/LoadingOverlay'
import { tryWailsAutoLogin, isLoggedIn, clearAuth, tryUrlTokenLogin, setupCapacitorTokenBridge, isCapacitorMode, requestCapacitorToken } from './application/auth-store'
import { connectClient, waitForClientReady, AUTH_CONNECTION_TIMEOUT_MS } from './application/generated-client'
import { appRegistry } from './application/app-registry'
import { startAppRegistrySync } from './application/app-registry-sync'
import './ui/inspector/agentPromptInspectorRegistration'
import { useKeyboardHeight } from './application/useKeyboardHeight'
import { useI18n } from './i18n'

(window as any).__consolePatchInstalled = installConsolePatch()
installConsoleLogPersist()

console.log('[frontend] App mount — console patch installed, shell starting up')

if (isWails()) {
  void destroyAllBrowserWindows()
}

// Pre-paint with the boot-script theme so the splash never flashes the
// default palette before the persisted theme round-trips.
applyTheme(initialTheme())

export function App() {
  const { setLocale } = useI18n()

  // Load saved locale from backend preferences once on mount.
  // The splash screen waits for localeLoaded so its text can switch
  // to the saved language before the splash is dismissed.
  const [localeLoaded, setLocaleLoaded] = useState(false)
  useEffect(() => {
    loadLocale()
      .then((locale) => {
        if (locale) setLocale(locale)
      })
      .catch(() => {
        // ignore
      })
      .finally(() => setLocaleLoaded(true))
  }, [setLocale])

  // Screenshot overlay window — bypass all auth and shell UI
  if (window.location.hash === '#screenshot') {
    return <ScreenshotOverlay />
  }

  const [aiTheme, setAiTheme] = useState<ThemeState>(initialTheme)
  const [themeLoaded, setThemeLoaded] = useState(false)
  useKeyboardHeight()

  // Capacitor iframe mode: let the web app know it should reserve the bottom
  // system gesture area via env(safe-area-inset-bottom).
  useEffect(() => {
    if (isCapacitorMode()) {
      document.body.classList.add('capacitor-iframe')
    }
    return () => {
      document.body.classList.remove('capacitor-iframe')
    }
  }, [])

  // Auth state
  const [authReady, setAuthReady] = useState(false)
  const [loggedIn, setLoggedIn] = useState(false)
  const [overlayState, setOverlayState] = useState<OverlayState>('loading')
  const [overlayMessage, setOverlayMessage] = useState('')

  // Agent list readiness gate for the splash screen. If the initial fetch
  // already completed (e.g. from a prior session in the same page load),
  // skip the wait entirely.
  const [agentsLoaded, setAgentsLoaded] = useState(() => isAgentListFetched())
  // Shell context (account/session/projects/systemTree) readiness gate.
  const [contextLoaded, setContextLoaded] = useState(() => isShellContextFetched())

  // Splash screen on Wails desktop startup. Hide it once the frontend init
  // is complete (authReady) instead of using a fixed timer, with a minimum
  // display time so it doesn't flicker and a maximum cap so it never stays.
  const [showSplash, setShowSplash] = useState(() => isWails())
  const splashStartRef = useRef(Date.now())

  useEffect(() => {
    if (!showSplash) return
    splashStartRef.current = Date.now()
    // Remove native child windows left over from the previous frontend instance
    // before the refreshed shell can render over the splash screen.
    void destroyAllBrowserWindows()
    // Safety net: if the agent list / shell context fetch takes too long,
    // force-dismiss after 20s so a broken backend doesn't hold the splash
    // forever.
    const maxTimer = setTimeout(() => {
      setShowSplash(false)
      notifySplashDone()
    }, 20_000)
    return () => clearTimeout(maxTimer)
  }, [showSplash])

  useEffect(() => {
    if (!showSplash || !authReady) return
    // Wait for both the agent list AND shell context to finish loading
    // before dismissing the splash. This ensures the AI shell has all data
    // ready the moment the splash disappears. The 20s max timer is the
    // safety net.
    if (!agentsLoaded || !contextLoaded || !localeLoaded) return
    let cancelled = false
    const elapsed = Date.now() - splashStartRef.current
    const remaining = Math.max(0, 3000 - elapsed)
    const timer = setTimeout(() => {
      if (!cancelled) {
        setShowSplash(false)
        notifySplashDone()
      }
    }, remaining)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [showSplash, authReady, agentsLoaded, contextLoaded, localeLoaded])

  // On web/capacitor the login UI is shown first; once login succeeds, show
  // the splash until the agent list + shell context are ready so the shell
  // never appears half-loaded. On Wails the splash already covers startup.
  useEffect(() => {
    if (!loggedIn || isWails()) return
    setShowSplash(true)
  }, [loggedIn])

  const handleLoggedIn = useCallback(() => {
    setLoggedIn(true)
    setOverlayState('idle')
  }, [])

  const handleLogout = useCallback(() => {
    setLoggedIn(false)
  }, [])

  const applyCapacitorAuthResult = useCallback(
    async (result: { ok: boolean; reason?: string }, cancelled: boolean) => {
      if (cancelled) return
      if (result.ok) {
        console.log('[App] Capacitor token login OK')
        setLoggedIn(true)
        setOverlayState('idle')
        setAuthReady(true)
        return
      }
      const reason = result.reason ?? 'unknown'
      console.log('[App] Capacitor token login failed, reason=' + reason)
      let userMessage = 'Please sign in in the app login screen'
      if (reason === 'websocket-failed') {
        userMessage = 'Could not connect to server. Please check the server address.'
      } else if (reason === 'token-request-timeout') {
        userMessage = 'Login timed out. Please try again.'
      } else if (reason === 'no-saved-token') {
        userMessage = 'No saved token. Please sign in in the app login screen.'
      }
      window.parent.postMessage({ type: 'auth-required', data: { reason } }, '*')
      setOverlayState('error')
      setOverlayMessage(userMessage)
      setAuthReady(true)
    },
    [],
  )

  const handleRetry = useCallback(() => {
    setOverlayState('loading')
    setOverlayMessage('Reconnecting...')
    if (!loggedIn && isCapacitorMode()) {
      requestCapacitorToken().then((result) => {
        void applyCapacitorAuthResult(result, false)
      })
    } else {
      forceReconnectClient()
    }
  }, [loggedIn, applyCapacitorAuthResult])

  // Initial auth: Wails auto-login or check existing session
  useEffect(() => {
    let cancelled = false

    async function initAuth() {
      setOverlayState('loading')
      setOverlayMessage('Authenticating...')
      console.log('[App] initAuth start, capacitorMode=', isCapacitorMode(), 'search=', window.location.search)

      // Capacitor iframe: set up token bridge first, then request token via postMessage.
      setupCapacitorTokenBridge()

      try {
        if (isCapacitorMode()) {
          const capacitorResult = await requestCapacitorToken()
          if (cancelled) return
          await applyCapacitorAuthResult(capacitorResult, cancelled)
          return
        }

        const urlOk = await tryUrlTokenLogin()
        if (cancelled) return
        if (urlOk) {
          console.log('[App] URL token login OK')
          setLoggedIn(true)
          setOverlayState('idle')
          setAuthReady(true)
          return
        }
        console.log('[App] No token in URL')
      } catch (err) {
        console.error('[App] Token login failed:', err)
        // Token invalid — fall through to normal auth flow.
      }

      try {
        // Try Wails desktop auto-login
        const wailsOk = await tryWailsAutoLogin()
        if (cancelled) return

        if (wailsOk) {
          setLoggedIn(true)
          setOverlayState('idle')
          setAuthReady(true)
          return
        }
      } catch (err) {
        // Wails mode confirmed but binding failed — fatal, do not fallback.
        const msg = err instanceof Error ? err.message : String(err)
        setOverlayState('error')
        setOverlayMessage(`Wails auto-login failed: ${msg}`)
        // Keep authReady false so the login page is never shown in Wails mode.
        return
      }

      // Check if there's already a valid token (e.g. from a previous session).
      // If the server is unreachable after a disconnect, we must not leave the
      // auth splash spinning forever — time out and fall back to the login UI.
      if (isLoggedIn()) {
        try {
          await connectClient()
          await waitForClientReady(AUTH_CONNECTION_TIMEOUT_MS)
          setLoggedIn(true)
        } catch (err) {
          console.error('[App] Existing session connection failed:', err)
          clearAuth()
        }
      }

      setOverlayState('idle')
      setAuthReady(true)
    }

    void initAuth()
    return () => { cancelled = true }
  }, [])

  // Theme loading
  useEffect(() => {
    let cancelled = false
    loadTheme('ai-shell')
      .then(theme => { if (!cancelled) { setAiTheme(theme); setThemeLoaded(true) } })
      .catch(e => { if (!cancelled) { console.warn('[App] loadTheme(ai-shell) failed', e); setThemeLoaded(true) } })
    return () => { cancelled = true }
  }, [])

  // Global background image loading
  useEffect(() => {
    loadAppBackground()
      .then(bg => applyAppBackground(bg))
      .catch(e => console.warn('[App] loadAppBackground failed', e))
  }, [])

  // Reconnect state → overlay feedback. Only switch to 'reconnecting' when
  // already logged in; during initial auth the loading overlay owns the UI.
  useEffect(() => {
    const unsub = onReconnectStateChange((state: ReconnectState) => {
      if (!loggedIn || authReady === false) return
      if (state === 'reconnecting') {
        setOverlayState('reconnecting')
      } else if (state === 'error') {
        setOverlayState('error')
        setOverlayMessage('Connection lost. Please check your network and reload.')
      } else if (state === 'connected') {
        setOverlayState('idle')
      }
    })

    const forceReconnect = () => {
      forceReconnectClient()
    }

    const onLifecycleMessage = (event: MessageEvent) => {
      if (event.data?.type === 'app-resumed' && isCapacitorMode()) {
        forceReconnect()
      }
    }
    const onVisibilityChange = () => {
      if (!document.hidden) reconnectIfDisconnected()
    }
    window.addEventListener('message', onLifecycleMessage)
    window.addEventListener('online', forceReconnect)
    document.addEventListener('visibilitychange', onVisibilityChange)

    return () => {
      unsub()
      window.removeEventListener('message', onLifecycleMessage)
      window.removeEventListener('online', forceReconnect)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [loggedIn, authReady])

  // Apply theme
  useEffect(() => {
    applyTheme(aiTheme)
    if (themeLoaded) notifyShellTheme(aiTheme)
  }, [aiTheme, themeLoaded])

  // Listen for logout events from any component
  useEffect(() => {
    const onLogout = () => {
      if (isCapacitorMode()) {
        window.parent.postMessage({ type: 'auth-required', data: { reason: 'User logged out' } }, '*')
      }
      handleLogout()
    }
    window.addEventListener('sporemind:logout', onLogout)
    return () => window.removeEventListener('sporemind:logout', onLogout)
  }, [handleLogout])

  // Warmup workspace session (after auth)
  useEffect(() => {
    if (!loggedIn) return
    workspace.session(client).then(session => {
      if (session.SessionId) enableErrorCapture(session.SessionId)
    }).catch(e => console.warn('[App] workspace.session warmup failed', e))
  }, [loggedIn])

  // Bootstrap unified App Registry (after auth) with reconnect resilience.
  useEffect(() => {
    if (!loggedIn) return
    appRegistry.clear()
    const stop = startAppRegistrySync(client)
    return () => { stop(); appRegistry.clear() }
  }, [loggedIn])

  // Prefetch agent list + shell context as early as possible (right after
  // auth succeeds) so the splash screen can dismiss as soon as data is
  // ready. Both run in parallel — no extra wait vs. just one.
  useEffect(() => {
    if (!loggedIn) return

    // Agent list
    if (isAgentListFetched()) {
      setAgentsLoaded(true)
    } else {
      let cancelled1 = false
      prefetchAgentList()
        .then(() => { if (!cancelled1) setAgentsLoaded(true) })
        .catch(() => { if (!cancelled1) setAgentsLoaded(true) })
    }

    // Shell context (account / session / projects / systemTree)
    if (isShellContextFetched()) {
      setContextLoaded(true)
    } else {
      let cancelled2 = false
      prefetchShellContext()
        .then(() => { if (!cancelled2) setContextLoaded(true) })
        .catch(() => { if (!cancelled2) setContextLoaded(true) })
    }
  }, [loggedIn])

  // SplashScreen is rendered once, outside the per-state branches, so the
  // branch transitions below never remount it and restart its animation.
  //
  // CrashOverlay is mounted in every branch: a boot crash report must render
  // even before login. Outside BrowserOverlayManager its useBrowserOverlay is a
  // no-op (no native browser child windows exist pre-login anyway); inside the
  // logged-in branch it drives the browser overlay suppression.
  let content
  if (!authReady) {
    content = (
      <>
        <LoadingOverlay state={overlayState} message={overlayMessage} />
        <CrashOverlay />
      </>
    )
  } else if (!loggedIn) {
    // In Capacitor mode the native shell owns the login UI; the iframe waits
    // for a token via postMessage. Showing the web login form here would create
    // a duplicate login screen inside the app.
    content = isCapacitorMode() ? (
      <>
        <LoadingOverlay state={overlayState} message={overlayMessage} onRetry={handleRetry} />
        <CrashOverlay />
      </>
    ) : (
      <>
        <LoginPage onLoggedIn={handleLoggedIn} />
        <LoadingOverlay state={overlayState} message={overlayMessage} />
        <CrashOverlay />
      </>
    )
  } else {
    content = (
      <BrowserOverlayManager>
        <AIShell theme={aiTheme} onThemeChange={setAiTheme} />
        <LoadingOverlay state={overlayState} message={overlayMessage} onRetry={handleRetry} />
        <CrashOverlay />
      </BrowserOverlayManager>
    )
  }

  return (
    <>
      {content}
      {showSplash && <SplashScreen />}
    </>
  )
}
