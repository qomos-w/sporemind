import { client } from './generated-client'
import * as unifiedGraph from '../gen-clients/unified_graph/client'

const CHECK_INTERVAL_MS = 150
/** Hard cap on waitForBackendReady. Without it, a backend that opens the WS
 *  but never reports topology epoch > 0 (broken topology actor, partial
 *  startup) leaves every caller hung forever — loadTimeline awaits this
 *  before subscribing, so the timeline shows infinite loading. After the cap
 *  we resolve anyway; callers proceed and hit the real error (subscribe
 *  fails, summary returns an error) which surfaces to the user instead of an
 *  infinite spinner. 6s is above normal backend startup (<5s). */
const BACKEND_READY_TIMEOUT_MS = 6_000
/** Per-epoch check must resolve quickly; if a single sync call hangs (e.g.
 *  backend topology actor deadlocked or slow), the overall deadline above would
 *  be missed and the browser tab keeps spinning. Cap each check at 1s so the
 *  loop can retry and the hard cap stays meaningful. */
const CHECK_TIMEOUT_MS = 1_000

let ready = false
let readyPromise: Promise<void> | null = null
let cancelCurrent: (() => void) | null = null

function resetReady(): void {
  ready = false
  readyPromise = null
  cancelCurrent?.()
  cancelCurrent = null
}

async function checkReadyOnce(): Promise<boolean> {
  try {
    const resp = await unifiedGraph.sync(client, { ClientEpoch: 0 }, { timeoutMs: CHECK_TIMEOUT_MS })
    return resp.CurrentEpoch > 0
  } catch (err) {
    return false
  }
}

function createReadyPromise(): Promise<void> {
  let cancelled = false
  cancelCurrent = () => { cancelled = true }

  const promise = new Promise<void>((resolve) => {
    const loop = async () => {
      const deadline = Date.now() + BACKEND_READY_TIMEOUT_MS
      while (!cancelled) {
        const transport = client.getTransport() as any
        if (transport && transport.state === 'open' && await checkReadyOnce()) {
          ready = true
          resolve()
          return
        }
        if (Date.now() >= deadline) {
          console.warn(`[backend-ready] timeout after ${BACKEND_READY_TIMEOUT_MS}ms — proceeding anyway; callers will hit real errors`)
          resolve()
          return
        }
        await new Promise(r => setTimeout(r, CHECK_INTERVAL_MS))
      }
    }

    loop()

    // Warn once if the backend is taking a while to become ready.
    setTimeout(() => {
      if (!ready && !cancelled) {
        console.warn('[backend-ready] still waiting for backend ready signal')
      }
    }, 1000)
  })

  readyPromise = promise
  return promise
}

let reconnectListenerRegistered = false
function ensureReconnectListener(): void {
  if (reconnectListenerRegistered) return
  const transport = client.getTransport() as any
  if (transport && typeof transport.onConnected === 'function') {
    transport.onConnected(({ isReconnect }: { isReconnect: boolean }) => {
      if (isReconnect) resetReady()
    })
    reconnectListenerRegistered = true
  }
}

/** Resolves once the backend has finished its initial actor-tree build
 *  (topology epoch > 0). Safe to call multiple times; reconnects reset the
 *  gate so callers re-wait after the backend restarts.
 *
 *  After BACKEND_READY_TIMEOUT_MS, resolves anyway so callers don't hang
 *  forever on a partially-started backend; they'll hit real errors from
 *  downstream subscribe/summary calls instead. */
export function waitForBackendReady(): Promise<void> {
  ensureReconnectListener()
  if (ready) return Promise.resolve()
  if (!readyPromise) readyPromise = createReadyPromise()
  return readyPromise
}
