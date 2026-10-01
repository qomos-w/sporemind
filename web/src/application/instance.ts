// Instance container — the single-window local↔remote swap.
//
// The desktop shell keeps ONE window. Switching a connection rebuilds the
// "instance": gateway resolution is repointed (setGatewayOverride), the live
// client is rebound to a fresh transport (rebindClient), the instance is
// authenticated, and subscribers are told to reset their module-level caches
// before React remounts the UI subtree on the new instance id. See
// [[instance-swap-refactor]] for the design contract.
//
// Auth orchestration is injected via registerAuthProviders() rather than
// imported from auth-store: auth-store imports generated-client, and importing
// it here would close the loop into a cycle the moment auth-store (or the app)
// registers these providers.

import { useSyncExternalStore } from 'react'
import { GosporeClient } from '@qomos/gospore-client'
import { Events } from '@wailsio/runtime'
import { createTransport, rebindClient } from './generated-client'
import { GATEWAY_DEFAULT_PORT, getGatewayOverride, setGatewayOverride } from './gateway'
import { isWails } from './runtime'
import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'

/** The local installation is always instance id "local". */
export const LOCAL_INSTANCE_ID = 'local'

/** Emitted (synchronously) after a completed swap: from → to. */
export interface InstanceSwapEvent {
  from: string
  to: string
}

/**
 * Auth orchestration for a swap. The real implementations live in auth-store
 * and are registered by the app (registerAuthProviders) to avoid an import
 * cycle; authenticateRemote receives the connection id.
 *
 * snapshotAuthState/restoreAuthState capture and restore token/account state
 * so a failed swap can roll back to the previous instance's credentials.
 */
export interface InstanceAuthProviders {
  authenticateLocal(): Promise<void>
  authenticateRemote(connId: string): Promise<void>
  snapshotAuthState?(): unknown
  restoreAuthState?(snapshot: unknown): void
}

let authProviders: InstanceAuthProviders | null = null

/** Register the auth implementations used by switchInstance (see above). */
export function registerAuthProviders(providers: InstanceAuthProviders): void {
  authProviders = providers
}

// ---------------------------------------------------------------------------
// Active instance id (external store for React)
// ---------------------------------------------------------------------------

/** Initial instance: a dedicated remote window carries ?conn=<id>; everything
 *  else (the single-window shell) starts on local. */
function readInstanceIdFromUrl(): string {
  if (typeof window === 'undefined') return LOCAL_INSTANCE_ID
  try {
    const conn = new URLSearchParams(window.location.search).get('conn')
    return conn && conn.trim() ? conn : LOCAL_INSTANCE_ID
  } catch {
    return LOCAL_INSTANCE_ID
  }
}

let activeInstanceId: string = readInstanceIdFromUrl()

// The instance this window booted as (?conn= for dedicated remote windows,
// local for the single-window shell). A swap AWAY from the boot instance
// makes this window a guest projection of another instance: guest sessions
// must not persist UI state into the visited instance's workspace model —
// that clobbers whatever the instance's own clients saved there.
const bootInstanceId = activeInstanceId

export function isActiveInstanceGuest(): boolean {
  return activeInstanceId !== bootInstanceId
}

const instanceListeners = new Set<() => void>()
const swapListeners = new Set<(e: InstanceSwapEvent) => void>()

export function getActiveInstanceId(): string {
  return activeInstanceId
}

function subscribeInstance(cb: () => void): () => void {
  instanceListeners.add(cb)
  return () => {
    instanceListeners.delete(cb)
  }
}

/** React hook: the active instance id ("local" | connection id). */
export function useActiveInstanceId(): string {
  return useSyncExternalStore(subscribeInstance, getActiveInstanceId, getActiveInstanceId)
}

/**
 * Subscribe to completed swaps. Fires synchronously after the client has been
 * rebound and the active id updated, but before React remounts the UI subtree.
 * This is where store-reset consumers clear their module-level caches.
 */
export function onInstanceSwap(cb: (e: InstanceSwapEvent) => void): () => void {
  swapListeners.add(cb)
  return () => {
    swapListeners.delete(cb)
  }
}

function notifyInstanceListeners(): void {
  for (const cb of Array.from(instanceListeners)) {
    try {
      cb()
    } catch (err) {
      console.error('[instance] instance listener threw:', err)
    }
  }
}

function notifySwap(event: InstanceSwapEvent): void {
  for (const cb of Array.from(swapListeners)) {
    try {
      cb(event)
    } catch (err) {
      console.error('[instance] swap listener threw:', err)
    }
  }
}

// ---------------------------------------------------------------------------
// Swap orchestration
// ---------------------------------------------------------------------------

function createClient(): GosporeClient {
  return new GosporeClient(createTransport())
}

/**
 * Resolve the gateway WS url a target should dial. Local returns null so
 * resolution falls back to the wails binding (sporemind.yaml honoured); a
 * remote connection builds ws://host:port/ws from its saved profile, which the
 * host resolves after SwitchConnection recorded it as active.
 */
async function resolveTargetWsUrl(target: string): Promise<string | null> {
  if (target === LOCAL_INSTANCE_ID) return null
  const active = await desktop.GetActiveConnection()
  const conn = active?.conn
  if (!conn || !conn.host) {
    throw new Error(`instance: no saved profile for connection "${target}"`)
  }
  const port = conn.port || GATEWAY_DEFAULT_PORT
  return `ws://${conn.host}:${port}/ws`
}

let swapInFlight = false

/**
 * Switch the whole frontend to another instance (design contract §switchInstance):
 *
 * 1. host SwitchConnection(target) — probe / anti-self-connect live host-side;
 *    a rejection throws before any local state changes (UI stays put).
 * 2. repoint gateway resolution at the new instance (setGatewayOverride).
 * 3. install a fresh client dialing it (rebindClient).
 * 4. authenticate the new instance (injected providers).
 * 5. commit: flip the active id, then notify subscribers.
 *
 * Any failure after step 1 rolls back completely — override, client, and auth
 * state — so the window stays on the previous instance.
 */
export function switchInstance(target: string): Promise<void> {
  return performSwap(target)
}

async function performSwap(target: string): Promise<void> {
  const normalized = target && target.trim() ? target : LOCAL_INSTANCE_ID
  if (normalized === activeInstanceId) return
  if (swapInFlight) {
    throw new Error('instance: a swap is already in progress')
  }
  if (!authProviders) {
    throw new Error('instance: auth providers not registered')
  }
  const providers = authProviders

  swapInFlight = true
  const from = activeInstanceId
  const previousOverride = getGatewayOverride()
  let clientRebound = false
  let authSnapshotTaken = false
  let previousAuth: unknown

  try {
    // 1. host-side switch — probe + anti-self-connect; throws on refusal.
    await desktop.SwitchConnection(normalized)

    // 2. repoint resolution at the new instance before the transport is built.
    const wsUrl = await resolveTargetWsUrl(normalized)
    setGatewayOverride(wsUrl)

    // 3. install a fresh client for the new instance.
    rebindClient(createClient())
    clientRebound = true

    // 4. authenticate against the freshly-bound client. Capture the previous
    //    auth state first so a failure here can restore it.
    if (providers.snapshotAuthState) {
      previousAuth = providers.snapshotAuthState()
      authSnapshotTaken = true
    }
    if (normalized === LOCAL_INSTANCE_ID) {
      await providers.authenticateLocal()
    } else {
      await providers.authenticateRemote(normalized)
    }

    // 5. commit — flip the id, then reset stores before React remounts.
    activeInstanceId = normalized
    console.log(`[instance] swap committed: ${from} -> ${normalized} (listeners=${instanceListeners.size}, swapSubs=${swapListeners.size})`)
    notifyInstanceListeners()
    notifySwap({ from, to: normalized })
  } catch (err) {
    setGatewayOverride(previousOverride)
    if (clientRebound) {
      // The forward rebind closed the previous transport, and the frame
      // transport's close() is terminal (state → destroyed, no reconnect), so
      // rebinding the previous client object would leave a dead socket. Rebuild
      // a client for the previous instance instead — resolution now points back
      // at the previous gateway via previousOverride.
      rebindClient(createClient())
    }
    if (authSnapshotTaken && providers.restoreAuthState) {
      providers.restoreAuthState(previousAuth)
    }
    throw err
  } finally {
    swapInFlight = false
  }
}

// ---------------------------------------------------------------------------
// Host event sync (switches initiated outside this webview / frame)
// ---------------------------------------------------------------------------

function eventTarget(evt: unknown): string | null {
  const data = (evt as { data?: unknown })?.data ?? evt
  const target = (data as { target?: unknown })?.target
  return typeof target === 'string' && target.trim() ? target : null
}

let cancelActiveListener: (() => void) | null = null
let tracking = false

/**
 * Follow host-side connection switches. After the host performs a switch it
 * emits `connections:active`; when the id differs from ours we run the same
 * swap flow (the host's same-target early-exit makes our re-issued
 * SwitchConnection call a no-op). Idempotent; no-op outside Wails.
 */
export function startInstanceTracking(): void {
  if (tracking || !isWails()) return
  tracking = true
  try {
    const cancel = Events.On('connections:active', ((evt: unknown) => {
      const target = eventTarget(evt)
      if (!target) return
      if (target === activeInstanceId || swapInFlight) return
      void performSwap(target).catch((err) => {
        console.error('[instance] following host switch failed:', err)
      })
    }) as (...args: unknown[]) => void)
    cancelActiveListener = typeof cancel === 'function' ? (cancel as () => void) : null
  } catch (err) {
    tracking = false
    console.warn('[instance] could not subscribe to connections:active:', err)
  }
}

// Track from module load — the desktop app imports this module (via the
// connection switcher) in Wails mode, so the listener is live before the first
// user switch. Guarded by isWails(); outside the desktop it is a no-op.
startInstanceTracking()

// ---------------------------------------------------------------------------
// Test hooks
// ---------------------------------------------------------------------------

/** Reset all module state (active id re-derived from the current URL, listeners
 *  dropped, providers cleared, gateway override cleared). Test-only. */
export function __resetInstanceForTests(): void {
  if (cancelActiveListener) {
    try {
      cancelActiveListener()
    } catch {
      /* ignore */
    }
  }
  cancelActiveListener = null
  tracking = false
  swapInFlight = false
  instanceListeners.clear()
  swapListeners.clear()
  authProviders = null
  activeInstanceId = readInstanceIdFromUrl()
  setGatewayOverride(null)
}
