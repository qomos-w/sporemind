import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { bindPluginBridgeSource, handlePluginBridgeHandshake, detachPluginBridgePort, type PluginBridgeBinding } from '../../application/plugin-bridge'
import { getHttpUrl, resolveWailsGateway } from '../../application/gateway'
import { isCapacitor, isWails } from '../../application/runtime'
import { appRegistry, normalizeApp } from '../../application/app-registry'
import { client } from '../../application/generated-client'
import { pluginLoad } from '../../gen-clients/appmanager/client'
import { defaultLocale } from '../../i18n'
import { PluginVoiceHost } from './plugin-voice-host'

// Voice relay message shape (see ui/ai/voice-api.ts CapacitorVoiceDriver):
// {_sporemind: true, type: 'voice:*', data: {requestId, ...}}. The relay is
// protocol-blind — it forwards voice:* verbatim and never inspects requestId
// semantics; correlation stays between the plugin driver and the shell.
function isVoiceRelayMessage(data: unknown): boolean {
  if (!data || typeof data !== 'object') return false
  const msg = data as { _sporemind?: unknown; type?: unknown }
  return msg._sporemind === true && typeof msg.type === 'string' && msg.type.startsWith('voice:')
}

// Panel-URL resolution retries: a wedged wails binding (GetGatewayAddr never
// answering or rejecting) must not wedge the panel on "Loading plugin…"
// forever — bound attempts, then a visible error state.
const SRC_RESOLVE_TIMEOUT_MS = 8000
const SRC_RESOLVE_ATTEMPTS = 3
const SRC_RESOLVE_RETRY_MS = 1000

// Frame-alive grace: every gateway-served panel page carries the injected
// bootstrap snippet, which posts ready-for-bootstrap during initial parse
// and retries until answered. A document whose load event fired but never
// signaled (the gateway's 502 plain text, or the webview's own navigation
// error page) is wedged on an error document.
const FRAME_ALIVE_TIMEOUT_MS = 8000

function withTimeout<T>(p: Promise<T>, ms: number, label: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${label} timed out after ${ms}ms`)), ms)
    p.then(
      (v) => { clearTimeout(timer); resolve(v) },
      (e) => { clearTimeout(timer); reject(e) },
    )
  })
}


export interface PluginIframeProps {
  pluginID: string
  viewID: string
  route: string
  title?: string
  className?: string
  // Bumped by the host toolbar to force a document reload (iframe remount).
  refreshNonce?: number
  // Fires when the embedded document finishes loading (initial + refresh).
  onFrameLoad?: () => void
  // Whether the panel is visible (active tab + panel open). Inactive panels
  // receive a sporemind:event-suspend post so the SDK closes its event
  // connections: a hidden panel must not hold a stream while other panels
  // need the pool. SDKs that predate the message ignore it.
  active?: boolean
}

export function PluginIframe({ pluginID, viewID, route, title, className, refreshNonce, onFrameLoad, active = true }: PluginIframeProps) {
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const [src, setSrc] = useState<string | null>(null)
  const onFrameLoadRef = useRef(onFrameLoad)
  useEffect(() => { onFrameLoadRef.current = onFrameLoad })

  // The app's session generation: 1 on register, bumped on every reload.
  // It feeds both the cache-busting ?v= URL param and the iframe remount
  // key, so a reload_project forces a fresh document fetch instead of the
  // browser reusing a cached plugin page.
  const generation = useSyncExternalStore(
    (listener) => appRegistry.subscribe(listener),
    () => appRegistry.get(pluginID)?.generation,
  )

  // Live registry entry: when the plugin process has died (or was stopped /
  // failed to load), mounting the iframe would sit on 'Loading plugin…'
  // forever — the asset server serves the page but the backend is gone.
  // Render an error state with a restart action instead. An absent entry
  // (registry not synced yet) keeps the normal loading path.
  const appEntry = useSyncExternalStore(
    (listener) => appRegistry.subscribe(listener),
    () => appRegistry.get(pluginID),
  )
  const [restarting, setRestarting] = useState(false)
  const [restartError, setRestartError] = useState<string | null>(null)
  // Terminal failure of the panel-URL resolution (all retries exhausted).
  const [srcError, setSrcError] = useState<string | null>(null)
  // Loaded-but-dead document: load fired, ready-for-bootstrap never arrived.
  // A keepAlive tab can sit on such an error page forever — the registry
  // says running, so the appDown UI never shows. Drives a bounded
  // auto-retry on the next tab activation.
  const [docFailed, setDocFailed] = useState(false)
  // Bumped by the activation auto-retry; part of the iframe remount key.
  const [autoReloadNonce, setAutoReloadNonce] = useState(0)
  // One auto-retry per active episode; re-armed only by going inactive.
  const autoTriedRef = useRef(false)
  // Whether the iframe document finished loading. The suspend/resume post
  // needs a listener inside the document (installed by the bootstrap
  // snippet), so activity messages wait for the load event; every reload
  // resets the document's channel state and re-sends.
  const [frameReady, setFrameReady] = useState(false)
  const lastSentActiveRef = useRef<boolean | null>(null)
  const appDown = appEntry !== undefined && appEntry.state !== 'running' && appEntry.state !== 'restart_pending'

  // Single grep key for the whole mount/bind/bootstrap chain. Info level so
  // the console patch forwards it to logs/console — postMessage traffic is
  // otherwise invisible server-side, which made the 2026-09-14 plan-usage
  // investigation pure elimination.
  const bridgeLog = (event: string) => {
    console.info(`[plugin-bridge] ${event} app=${pluginID} view=${viewID}`)
  }

  // Gateway data path: the iframe always loads through the host gateway's
  // /plugin/{id} route, which proxies to the plugin process's own SDK HTTP
  // listener. The advertised backendUrl stays an internal detail of that
  // proxy — the panel document never talks to the subprocess origin directly.
  const handleRestart = async () => {
    setRestarting(true)
    setRestartError(null)
    try {
      const resp = await pluginLoad(client, { Id: pluginID })
      appRegistry.upsert(normalizeApp(resp.Status))
    } catch (err) {
      setRestartError(err instanceof Error ? err.message : String(err))
    } finally {
      setRestarting(false)
    }
  }

  // Resolve the panel URL before rendering the iframe. In the desktop shell
  // the SPA runs on a wails origin, so we need the gateway HTTP address for
  // the fallback route and the bridge-client script URL; the wails resolver
  // is async, and falling back to the default 18080 would show "connection
  // refused" until the cache is populated. The resolver can also wedge (a
  // binding that never answers), so each attempt is time-boxed and retried;
  // exhaustion surfaces a visible error instead of an eternal spinner.
  useEffect(() => {
    let cancelled = false
    const resolve = async () => {
      bridgeLog('mount')
      setSrcError(null)
      setDocFailed(false)
      for (let attempt = 1; attempt <= SRC_RESOLVE_ATTEMPTS; attempt++) {
        try {
          if (isWails()) {
            await withTimeout(resolveWailsGateway(), SRC_RESOLVE_TIMEOUT_MS, 'resolveWailsGateway')
          }
          const path = route.startsWith('/') ? route : '/' + route
          const versioned = (base: string) => (generation ? `${base}?v=${generation}` : base)
          const base = getHttpUrl().replace(/\/$/, '')
          const resolved = versioned(`${base}/plugin/${pluginID}${path}`)
          if (!cancelled) setSrc(resolved)
          bridgeLog('src_ready')
          return
        } catch (err) {
          const detail = err instanceof Error ? err.message : String(err)
          console.warn(`[plugin-bridge] src_resolve_err app=${pluginID} attempt=${attempt}/${SRC_RESOLVE_ATTEMPTS} ${detail}`)
          if (attempt < SRC_RESOLVE_ATTEMPTS) {
            await new Promise((res) => setTimeout(res, SRC_RESOLVE_RETRY_MS))
            if (cancelled) return
          } else if (!cancelled) {
            setSrcError(detail)
          }
        }
      }
    }
    void resolve()
    return () => { cancelled = true }
  }, [pluginID, viewID, route, generation])

  useEffect(() => {
    const iframe = iframeRef.current
    if (!iframe) return

    let disposed = false
    let generation = 0
    let activePort: MessagePort | undefined
    let themeObserver: MutationObserver | undefined
    let revokeSession: (() => Promise<void>) | undefined
    let revokeKeepalive: (() => void) | undefined
    // Frame-alive tracking for the dead-document detector (see
    // FRAME_ALIVE_TIMEOUT_MS): set by the snippet's ready-for-bootstrap,
    // re-armed by every load event.
    let frameSignaled = false
    let aliveTimer: ReturnType<typeof setTimeout> | undefined
    // Inside the Capacitor mobile shell this SPA is an iframe of the native
    // app: relay the plugin's voice:* frames to it verbatim. On desktop
    // (wails) and flat web the SPA is the top-level window — it answers the
    // protocol itself with a PluginVoiceHost (getUserMedia + voice.recognize),
    // mirroring the native shell one level up.
    const voiceRelay = isCapacitor()
    const voiceHost = voiceRelay
      ? null
      : new PluginVoiceHost((msg) => { iframe.contentWindow?.postMessage(msg, '*') })

    // React's unmount cleanup never fires on page close/refresh; a pagehide
    // keepalive revoke closes the leak window where the host session lives
    // on until its 24h TTL.
    const onPageHide = () => {
      revokeKeepalive?.()
    }
    window.addEventListener('pagehide', onPageHide)

    // Session bind shared between the load handler and the bootstrap
    // request: the iframe snippet asks for bootstrap during initial parse,
    // which can race (or precede) the load event, so both paths funnel
    // through the same in-flight bind promise. The bind must complete
    // before the bootstrap message is posted so the later bridge handshake
    // (which resolves the session from this source) never races an unbound
    // iframe.
    let bindDone: Promise<PluginBridgeBinding | null> | null = null

    const doBind = async (): Promise<PluginBridgeBinding | null> => {
      const currentGeneration = ++generation
      if (revokeSession) {
        await revokeSession().catch(() => {})
        revokeSession = undefined
        revokeKeepalive = undefined
      }
      if (activePort) {
        detachPluginBridgePort(activePort)
        activePort = undefined
      }
      try {
        const win = iframe.contentWindow
        if (!win) return null
        const binding = await bindPluginBridgeSource(win, pluginID, viewID)
        if (disposed || currentGeneration !== generation) {
          await binding.revoke().catch(() => {})
          return null
        }
        revokeSession = binding.revoke
        revokeKeepalive = binding.revokeKeepalive
        bridgeLog('bind_ok')
        return binding
      } catch (err) {
        console.error(`[plugin-bridge] bind_err app=${pluginID} view=${viewID}`, err)
        return null
      }
    }

    const ensureBind = (): Promise<PluginBridgeBinding | null> => {
      if (!bindDone) bindDone = doBind()
      return bindDone
    }

    // Gateway-mode bootstrap payload: the iframe snippet's appBase, bridge
    // client URL, and current theme. Sent in reply to the snippet's
    // ready-for-bootstrap request and proactively after a successful session
    // bind on iframe load — the ready request can be lost (posted before this
    // component's listener mounted) or its reply delayed past the snippet's
    // 3s fallback by a cold-starting appmanager. Duplicate sends are safe:
    // the snippet's appBase resolve is done-guarded and bridge-script
    // injection dedups via the data-sporemind-bridge selector.
    const sendBootstrap = (win: Window) => {
      // Absolute bridge-client URL: the iframe loads from the gateway
      // /plugin/{id} route (a different origin than the host shell in
      // desktop mode), so a relative path would resolve against the
      // gateway 404. In dev the gateway proxies /src/* to Vite; in
      // production the client is a built asset under /assets/.
      const gatewayBase = getHttpUrl().replace(/\/$/, '')
      const bridgeUrl = import.meta.env.DEV
        ? `${gatewayBase}/src/plugin-bridge-client.ts`
        : `${gatewayBase}/assets/plugin-bridge-client.js`
      const themeMode = document.documentElement.getAttribute('data-theme') ?? 'light'
      // Host locale: I18nProvider mirrors the active locale onto <html lang>,
      // the same DOM-attribute pattern as data-theme.
      const locale = document.documentElement.lang || defaultLocale
      // Gateway mode: the bootstrap hands the iframe its gateway base path
      // (consumed by the generated client.gen.ts as window.__sporemindAppBase)
      // and no longer carries backendUrl/cookieToken — cookie bootstrap on
      // the subprocess origin is retired; the gateway authenticates by
      // session instead.
      win.postMessage({
        type: 'sporemind:bootstrap',
        pluginID,
        viewID,
        bridgeUrl,
        appBase: `/plugin/${pluginID}`,
        themeMode,
        locale,
        // Capability flag: inside the Capacitor shell the host relays voice:*
        // postMessages to the native shell; on wails desktop the host SPA
        // answers them itself (see PluginVoiceHost below). Either way the
        // panel should drive voice through the relay protocol instead of its
        // own getUserMedia. The injected snippet turns the flag into
        // window.__sporemindVoiceNative for the panel's voice driver choice.
        voiceNative: isCapacitor() || isWails(),
      }, '*')
      bridgeLog('bootstrap_sent')
    }

    const onMessage = async (e: MessageEvent) => {
      const win = iframe.contentWindow
      if (!win || e.source !== win) return
      // Native voice relay, upstream leg (Capacitor shell only): the host SPA
      // is itself an iframe of the mobile shell, so a plugin panel one level
      // deeper cannot reach it directly. Forward the plugin's voice:* frames
      // verbatim to the shell; strict source check above guarantees only this
      // iframe's traffic is relayed. On desktop/web the voice host answers
      // the frames itself instead of relaying.
      if (isVoiceRelayMessage(e.data)) {
        if (voiceRelay) window.parent.postMessage(e.data, '*')
        else voiceHost?.handle(e.data)
        return
      }
      if (e.data?.type === 'sporemind:handshake') {
        if (activePort) {
          try { activePort.postMessage({ type: 'sporemind:dispose' }) } catch {}
          detachPluginBridgePort(activePort)
          activePort = undefined
        }
        activePort = e.ports?.[0]
        if (activePort) {
          await handlePluginBridgeHandshake(win, activePort)
        }
        return
      }
      if (e.data?.type === 'sporemind:ready-for-bootstrap') {
        bridgeLog('ready_received')
        // Live document: cancel the dead-document timer before the async
        // bind so a slow session bind cannot race the grace period.
        frameSignaled = true
        if (aliveTimer) { clearTimeout(aliveTimer); aliveTimer = undefined }
        setDocFailed(false)
        await ensureBind()
        sendBootstrap(win)
        return
      }
    }

    const onLoad = () => {
      bridgeLog('frame_load')
      onFrameLoadRef.current?.()
      // New document: its channels start live, so re-post the current
      // activity state once the snippet's listener exists.
      setFrameReady(true)
      lastSentActiveRef.current = null
      // Re-arm the dead-document detector: error documents fire load too,
      // but only a gateway-served page carries the snippet that answers.
      frameSignaled = false
      setDocFailed(false)
      if (aliveTimer) clearTimeout(aliveTimer)
      aliveTimer = setTimeout(() => {
        aliveTimer = undefined
        if (frameSignaled || disposed) return
        setDocFailed(true)
        bridgeLog('frame_dead')
      }, FRAME_ALIVE_TIMEOUT_MS)
      const win = iframe.contentWindow
      // Start (or join) the session bind; theme mirroring attaches
      // independently of the bind outcome.
      void ensureBind().then((binding) => {
        // Proactive bootstrap once the session is bound: covers a
        // ready-for-bootstrap that was lost before this component's
        // listener mounted. Must wait for the bind or the later bridge
        // handshake would race an unbound iframe.
        if (binding && win && !disposed) sendBootstrap(win)
      })

      if (!win || disposed) return

      // Mirror theme and locale changes into the iframe via postMessage
      // (cross-origin safe). Both live as documentElement attributes written
      // by the host SPA (applyTheme → data-theme, I18nProvider → lang), so
      // attribute mutations are the canonical change signal; each mutation
      // dispatches only its own update kind.
      const sendThemeUpdate = () => {
        const mode = document.documentElement.getAttribute('data-theme') ?? 'light'
        win.postMessage({ type: 'sporemind:theme-update', themeMode: mode }, '*')
      }
      const sendLocaleUpdate = () => {
        const next = document.documentElement.lang
        if (next) win.postMessage({ type: 'sporemind:locale-update', locale: next }, '*')
      }
      themeObserver = new MutationObserver((records) => {
        for (const r of records) {
          if (r.attributeName === 'lang') sendLocaleUpdate()
          else sendThemeUpdate()
        }
      })
      themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'style', 'lang'] })
    }

    // Native voice relay, downstream leg: forward the shell's voice:* frames
    // (replies, partials, volume, audio buffers) back into the plugin iframe.
    // Strict source check: only the shell (window.parent) may feed this leg.
    const onShellMessage = (e: MessageEvent) => {
      if (!voiceRelay) return
      const win = iframe.contentWindow
      if (!win || e.source !== window.parent) return
      if (!isVoiceRelayMessage(e.data)) return
      win.postMessage(e.data, '*')
    }

    window.addEventListener('message', onMessage)
    window.addEventListener('message', onShellMessage)
    iframe.addEventListener('load', onLoad)

    return () => {
      disposed = true
      generation++
      window.removeEventListener('pagehide', onPageHide)
      window.removeEventListener('message', onMessage)
      window.removeEventListener('message', onShellMessage)
      iframe.removeEventListener('load', onLoad)
      voiceHost?.dispose()
      if (activePort) {
        try { activePort.postMessage({ type: 'sporemind:dispose' }) } catch {}
        detachPluginBridgePort(activePort)
      }
      if (revokeSession) void revokeSession().catch(() => {})
      if (aliveTimer) clearTimeout(aliveTimer)
      themeObserver?.disconnect()
    }
    // refreshNonce is in the deps because it remounts the <iframe> element
    // (it is part of the React key) without changing src: without re-running,
    // the load listener stays bound to the discarded element and the toolbar
    // spin (reset by onFrameLoad) never stops. autoReloadNonce for the same
    // reason — it is a key component too.
  }, [pluginID, viewID, route, src, refreshNonce, autoReloadNonce])

  // Activity control: post suspend/resume to the document whenever its
  // visibility flips (and once after every document load). Cross-origin
  // safe: same '*' postMessage channel as theme/locale updates.
  useEffect(() => {
    const win = iframeRef.current?.contentWindow
    if (!win || !frameReady) return
    if (lastSentActiveRef.current === active) return
    lastSentActiveRef.current = active
    win.postMessage({ type: active ? 'sporemind:event-resume' : 'sporemind:event-suspend' }, '*')
  }, [active, frameReady])

  // Auto-retry a dead document on tab activation: a hidden keepAlive tab can
  // have loaded the gateway's 502 text or the webview's error page while the
  // plugin was briefly unavailable, and nothing else will ever replace that
  // document. Switching back remounts the iframe once (panel HTML is served
  // no-store, so the remount refetches). Bounded: one retry per active
  // episode, re-armed only by going inactive again — a still-dead backend
  // cannot reload-loop.
  useEffect(() => {
    if (!active) {
      autoTriedRef.current = false
      return
    }
    if (!docFailed || src === null || appDown || autoTriedRef.current) return
    autoTriedRef.current = true
    setDocFailed(false)
    setAutoReloadNonce((n) => n + 1)
    bridgeLog('auto_retry_activate')
  }, [active, docFailed, src, appDown])

  if (appDown) {
    return (
      <div
        className={className ?? 'plugin-iframe-loading'}
        data-testid="plugin-iframe-error"
        title={title ?? viewID}
        style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: 8,
          padding: 16,
          textAlign: 'center',
          color: 'var(--text-tertiary)',
        }}
      >
        <div style={{ color: 'var(--text-secondary)' }}>
          Plugin is not running ({appEntry?.state})
        </div>
        {appEntry?.error && (
          <div data-testid="plugin-iframe-error-cause" style={{ fontSize: 12, maxWidth: '100%', overflowWrap: 'anywhere' }}>
            {appEntry.error}
          </div>
        )}
        <button
          type="button"
          data-testid="plugin-iframe-restart"
          disabled={restarting}
          onClick={() => void handleRestart()}
          style={{
            padding: '4px 14px',
            border: '1px solid var(--border-strong)',
            borderRadius: 'var(--radius-md)',
            background: 'transparent',
            color: 'var(--text-primary)',
            cursor: restarting ? 'wait' : 'pointer',
            font: 'inherit',
            fontSize: 13,
          }}
        >
          {restarting ? 'Restarting…' : 'Restart'}
        </button>
        {restartError && (
          <div data-testid="plugin-iframe-restart-error" style={{ fontSize: 12, color: 'var(--text-danger, #ef4444)' }}>
            {restartError}
          </div>
        )}
      </div>
    )
  }

  if (srcError && src === null) {
    return (
      <div
        className={className ?? 'plugin-iframe-loading'}
        data-testid="plugin-iframe-src-error"
        title={title ?? viewID}
        style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: 8,
          padding: 16,
          textAlign: 'center',
          color: 'var(--text-tertiary)',
        }}
      >
        <div style={{ color: 'var(--text-secondary)' }}>
          Panel failed to resolve its gateway URL
        </div>
        <div data-testid="plugin-iframe-src-error-cause" style={{ fontSize: 12, maxWidth: '100%', overflowWrap: 'anywhere' }}>
          {srcError}
        </div>
      </div>
    )
  }

  if (src === null) {
    return (
      <div
        className={className ?? 'plugin-iframe-loading'}
        title={title ?? viewID}
        style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--text-tertiary)' }}
      >
        Loading plugin…
      </div>
    )
  }

  return (
    <iframe
      key={`${generation ?? 0}:${refreshNonce ?? 0}:${autoReloadNonce}`}
      ref={iframeRef}
      src={src}
      title={title ?? viewID}
      className={className ?? 'plugin-iframe'}
      sandbox="allow-scripts allow-same-origin allow-forms"
      allow="clipboard-read; clipboard-write; microphone"
    />
  )
}
