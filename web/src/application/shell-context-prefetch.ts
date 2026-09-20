/**
 * Shell context prefetch cache.
 *
 * The 4 parallel context fetches (account, session, listProject, systemTree)
 * that AIShellLayout needs on mount are performed here as early as possible
 * (right after auth), so the splash screen can gate its dismissal on their
 * completion. AIShellLayout then consumes the cached result instead of
 * re-fetching.
 *
 * The cache is one-shot: once consumed it is cleared, so a subsequent mount
 * (e.g. HMR, re-render) triggers a normal fetch.
 */

import { client } from './generated-client'
import * as workspace from '../gen-clients/workspace/client'
import { mapProjectRef } from './project-adapter'
import type { AccountSnapshot, SessionSnapshot } from '../gen-types/workspace'
import type { ProjectSnapshot } from '../domain/types'
import type { SystemTreeNode, SystemTreeResp } from '../gen-clients/system/types'

export interface PrefetchedShellContext {
  account: AccountSnapshot
  session: SessionSnapshot
  projects: ProjectSnapshot[]
  systemTreeNodes: SystemTreeNode[]
  activeProject: ProjectSnapshot | null
}

// --- Module-level state (prefetch cache, not business state) ---

let cached: PrefetchedShellContext | null = null
let fetchPromise: Promise<void> | null = null
let done = false
let error: string | null = null

/**
 * Kick off the 4 parallel context fetches. Safe to call multiple times —
 * the internal dedup guard prevents duplicate requests.
 * Resolves on completion (success or failure).
 */
export function prefetchShellContext(): Promise<void> {
  if (fetchPromise) return fetchPromise
  fetchPromise = (async () => {
    try {
      const [account, session, listResp, treeResp] = await Promise.all([
        workspace.account(client),
        workspace.session(client),
        workspace.listProject(client),
        workspace.systemTree(client).catch(() => ({ Nodes: [] }) as SystemTreeResp),
      ])

      const projects = (listResp.Items ?? []).map(mapProjectRef)
      // The system meta project is an internal storage node (always first);
      // never auto-select it as the landing project.
      const firstRaw = (listResp.Items ?? []).find(p => !p.System)
      const activeProject = firstRaw?.ActorId ? mapProjectRef(firstRaw) : null

      cached = {
        account,
        session,
        projects,
        systemTreeNodes: treeResp?.Nodes ?? [],
        activeProject,
      }
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to load shell context'
    } finally {
      done = true
    }
  })()
  return fetchPromise
}

/** True once the initial context fetch has completed (success or failure). */
export function isShellContextFetched(): boolean {
  return done
}

/**
 * Returns and clears the cached context. After consumption the caller owns
 * the data and all subsequent state mutations. Returns null if the prefetch
 * hasn't run yet, has already been consumed, or failed (check
 * `getPrefetchError()` in the latter case).
 */
export function consumePrefetchedShellContext(): PrefetchedShellContext | null {
  const result = cached
  cached = null
  return result
}

/** Returns the error message if the prefetch failed, null otherwise. */
export function getPrefetchError(): string | null {
  return error
}

/** Reset the cache (for tests or re-login scenarios). */
export function resetShellContextPrefetch(): void {
  cached = null
  fetchPromise = null
  done = false
  error = null
}
