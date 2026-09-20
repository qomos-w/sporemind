import type { GosporeClient } from '@qomos/gospore-client'
import { onReconnectStateChange, type ReconnectState } from './generated-client'
import { appRegistry } from './app-registry'

export interface AppRegistrySyncOptions {
  /** Optional callback invoked whenever the underlying transport changes state. */
  onStateChange?: (state: ReconnectState) => void
}

/**
 * Start a resilient sync between the backend AppManager and the frontend
 * {@link appRegistry}. The sync:
 *
 *   1. Loads the current app snapshot on first connect.
 *   2. Watches both the projection stream and the lifecycle event stream.
 *   3. Aborts the watchers while disconnected and automatically reloads +
 *      re-watches after the transport reconnects.
 *
 * This keeps the app's entrypoints (views, panels, commands) consistent with
 * the server even across WebSocket reconnects, without relying on any browser
 * storage.
 */
export function startAppRegistrySync(
  client: GosporeClient,
  opts?: AppRegistrySyncOptions,
): () => void {
  let abortController: AbortController | null = null
  let active = true

  async function runLoop(signal: AbortSignal): Promise<void> {
    try {
      // Subscribe BEFORE loading the snapshot. appmanager lifecycle events
      // have no replay: a "reloaded" carrying a bumped generation that fires
      // between the snapshot fetch and the subscription would be lost
      // forever, leaving a restored plugin panel mounted against whatever it
      // loaded before the live proxy attach — the manual-refresh bug. With
      // the subscription live first, the snapshot reconciles anything missed
      // while the subscription was being established.
      const lifecycleWatch = appRegistry.watchLifecycle(client, signal, () => {
        // No extra bookkeeping needed: the registry upsert for any
        // non-removal lifecycle event (reload, re-register) carries the
        // bumped generation, which remounts the plugin iframe — the new
        // document rebinds a fresh session through the normal handshake.
      })
      await appRegistry.load(client)
      await Promise.all([
        appRegistry.watch(client, signal),
        lifecycleWatch,
      ])
    } catch (err) {
      if (!signal.aborted && active) {
        console.error('[AppRegistrySync] sync loop failed:', err)
      }
    }
  }

  function start(): void {
    abortController?.abort()
    abortController = new AbortController()
    void runLoop(abortController.signal)
  }

  function stop(): void {
    active = false
    abortController?.abort()
    abortController = null
  }

  const unsubscribe = onReconnectStateChange((state) => {
    opts?.onStateChange?.(state)
    if (state === 'connected' && active) {
      start()
    } else if ((state === 'reconnecting' || state === 'error') && active) {
      abortController?.abort()
    }
  })

  return () => {
    stop()
    unsubscribe()
  }
}
