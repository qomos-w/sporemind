import { useSyncExternalStore } from 'react'
import { buildType } from '../../../config/buildConfig'
import { client } from '../../../application/generated-client'
import * as cloudaccount from '../../../gen-clients/cloudaccount/client'
import { resolveContentAccess, type PassViewLike } from '../components/tier-badges'

/**
 * Shared Insider-or-above gate (Ultimate tier or an active Insider pass —
 * resolveContentAccess().hasExperimentalAccess). One store backs every
 * consumer (the %-browser-mention trigger, the browser-crawl bundle in the
 * composer mount picker) so the tier is fetched once and refreshed on a
 * slow interval instead of per composer render.
 *
 * Fail-closed: any status/entitlements fetch error resolves to false.
 *
 * Dev builds (BUILD_TYPE=dev, e.g. `make dev-release` / `make dev-desktop`)
 * open the gate unconditionally: the experimental surface must be reachable
 * for development without a cloud entitlement, so the hook returns true and
 * skips the account polling entirely. Release/beta builds keep the
 * account-tier check.
 */

const REFRESH_INTERVAL_MS = 60_000

const devBuild = buildType === 'dev'

let snapshot = { hasInsiderAccess: false }
const listeners = new Set<() => void>()
let timer: ReturnType<typeof setInterval> | null = null
let inFlight: Promise<void> | null = null

function publish(next: boolean) {
  if (next === snapshot.hasInsiderAccess) return
  snapshot = { hasInsiderAccess: next }
  for (const listener of listeners) listener()
}

/** Recompute the gate from cloudaccount.status + entitlements. */
export async function refreshInsiderAccess(): Promise<void> {
  if (inFlight) return inFlight
  inFlight = (async () => {
    let pass: PassViewLike | undefined
    try {
      const status = await cloudaccount.status(client)
      if (status.Linked) {
        try {
          pass = (await cloudaccount.getEntitlements(client)).Entitlements?.Pass
        } catch {
          // Entitlements are best-effort here; a degraded fetch falls back to
          // the tier alone (ultimate still passes, an insider pass is lost).
        }
      }
      publish(resolveContentAccess(status.Tier, pass).hasExperimentalAccess)
    } catch {
      publish(false)
    } finally {
      inFlight = null
    }
  })()
  return inFlight
}

export function useInsiderAccess(): boolean {
  const access = useSyncExternalStore(
    (onChange) => {
      if (devBuild) return () => {}
      listeners.add(onChange)
      void refreshInsiderAccess()
      if (!timer) timer = setInterval(() => { void refreshInsiderAccess() }, REFRESH_INTERVAL_MS)
      return () => {
        listeners.delete(onChange)
        if (listeners.size === 0 && timer) {
          clearInterval(timer)
          timer = null
        }
      }
    },
    () => snapshot,
  )
  return devBuild || access.hasInsiderAccess
}
