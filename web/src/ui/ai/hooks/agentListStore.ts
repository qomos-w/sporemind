import { client } from '../../../application/generated-client'
import { waitForBackendReady } from '../../../application/backend-ready'
import * as workspace from '../../../gen-clients/workspace/client'
import type { AgentListItem, WorkspaceAgentListState, AgentRef } from '../../../gen-clients/system/types'

export interface AgentListSnapshot {
  version: number
  full: boolean
  items: AgentListItem[]
}

export function toAgentRef(item: AgentListItem): AgentRef {
  const rt = item.Runtime
  return {
    Id: item.Id,
    ActorId: item.ActorId,
    ProjectId: item.ProjectId,
    DisplayName: item.DisplayName,
    AgentKind: item.AgentKind,
    Status: rt?.State,
    LastActivity: rt?.LastActivity,
    ActiveTurnRef: rt?.ActiveTurnRef,
    Primary: item.Primary,
    Fast: item.Fast,
    Execution: item.Execution,
    Review: item.Review,
    Summary: item.Summary,
    Title: item.Title,
    Degraded: item.Degraded,
    DegradedReason: item.DegradedReason,
  }
}

let snapshot: AgentListSnapshot = { version: 0, full: true, items: [] }
let listeners = new Set<() => void>()
let started = false
let offEvent: (() => void) | null = null
let offReconnect: (() => void) | null = null
let offVisibility: (() => void) | null = null

// --- Splash coordination ---
// Tracks whether the initial agent list fetch has completed (success or
// failure). The splash screen gates its dismissal on this so the user never
// sees the AI shell before agents are available. Once resolved the gate is
// permanent — subsequent refetches (reconnect, visibility) do not re-block.
let initialFetchDone = false
let initialFetchResolve: (() => void) | null = null
let initialFetchPromise: Promise<void> = new Promise<void>(resolve => {
  initialFetchResolve = resolve
})

function emit() {
  for (const cb of listeners) {
    cb()
  }
}

export function mergeState(state: WorkspaceAgentListState) {
  // A Full snapshot is the authoritative current state, so accept it
  // unconditionally — even if Version rolled backwards. The workspace actor's
  // version counter (agentListVersion) is not persisted and resets to 1 on
  // restart; rejecting the post-restart Full snapshot by the stale old version
  // would freeze the frontend on pre-restart state forever. No reconnect or
  // visibilitychange fallback fires when only the actor restarted (not the
  // transport), so the Full snapshot is the only recovery path here.
  if (state.Full) {
    snapshot = { version: state.Version, full: true, items: state.Items ?? [] }
    return
  }

  // The workspace backend rebuilds every agent_list_state event from the
  // complete agent set, so a diff-patch always carries the full Items list at
  // its version. The same non-persisted counter means a mid-session backend
  // restart resets versions to single digits while this store may hold a
  // much higher one — dropping those events froze the list on pre-restart
  // state forever (an agent that completed after the restart never flipped
  // to completed, so its unread badge never appeared). Accept a
  // lower-version event when its Items differ; identical Items at a lower
  // version is a stale replay and stays a no-op.
  if (state.Version < snapshot.version) {
    const staleReplay =
      snapshot.items.length === (state.Items ?? []).length &&
      JSON.stringify(snapshot.items) === JSON.stringify(state.Items ?? [])
    if (staleReplay) return
    snapshot = { version: state.Version, full: false, items: state.Items ?? [] }
    return
  }

  // The backend version always advances within a session, so a same-or-higher
  // diff replaces directly. Replacing also keeps deletes correct: the backend
  // omits removed agents from Items, so a full replace drops them client-side.
  snapshot = { version: state.Version, full: false, items: state.Items ?? [] }
}

// Deduplicate concurrent fetchFull calls. Without this guard, every diff-patch
// event with Version > snapshot.version during the initial burst of agent state
// transitions fires its own fetchFull — with N agents that meant N+ racing
// fetches on first load. The request flood delayed per-agent timeline
// subscriptions from stabilizing (the symptom: "can't load conversation stream
// when many agents exist"). The backend's agentListState callable returns the
// latest snapshot at call time, so a single in-flight fetch is sufficient to
// catch up regardless of how many events triggered it.
let fetchFullPromise: Promise<void> | null = null

function fetchFull(): Promise<void> {
  if (fetchFullPromise) return fetchFullPromise
  fetchFullPromise = (async () => {
    try {
      await waitForBackendReady()
      const state = await workspace.agentListState(client)
      mergeState(state)
      emit()
    } catch (err) {
      // eslint-disable-next-line no-console
      console.error('agentListStore: failed to fetch full state', err)
    } finally {
      fetchFullPromise = null
      // Signal splash gate: initial fetch attempt completed (success or
      // failure). Resolving on failure too so a broken backend doesn't
      // hold the splash until the 8s max timer.
      if (!initialFetchDone) {
        initialFetchDone = true
        initialFetchResolve?.()
      }
    }
  })()
  return fetchFullPromise
}

function start() {
  if (started) return
  started = true

  // AUDIT 8.4: await fetchFull *before* subscribing so the backend's first
  // agent_list_state event can't race ahead of the subscription and get
  // dropped. If the very first event arrives between fetchFull and
  // OnAgentListState the subscribe would miss it; without a fetchFull
  // backstop the list could stay empty forever.
  void (async () => {
    await fetchFull()
    if (!started) return

    offEvent = workspace.OnAgentListState(client, (ev) => {
      mergeState(ev.State)
      emit()
    })

    const transport = client.getTransport() as any
    if (transport && typeof transport.onConnected === 'function') {
      offReconnect = transport.onConnected(({ isReconnect }: { isReconnect: boolean }) => {
        if (isReconnect) {
          void fetchFull()
        }
      })
    }

    const onVisibilityChange = () => {
      if (!document.hidden) {
        void fetchFull()
      }
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    offVisibility = () => document.removeEventListener('visibilitychange', onVisibilityChange)
  })()
}

function stop() {
  if (!started) return
  started = false
  offEvent?.()
  offEvent = null
  offReconnect?.()
  offReconnect = null
  offVisibility?.()
  offVisibility = null
}

export function resetAgentListStore(): void {
  stop()
  snapshot = { version: 0, full: true, items: [] }
  fetchFullPromise = null
  initialFetchDone = false
  initialFetchPromise = new Promise<void>(resolve => { initialFetchResolve = resolve })
}

export function subscribeAgentListStore(cb: () => void): () => void {
  start()
  listeners.add(cb)
  return () => {
    listeners.delete(cb)
    if (listeners.size === 0) {
      stop()
    }
  }
}

export function getAgentListSnapshot(): AgentListSnapshot {
  return snapshot
}

export function getAgentListItems(): AgentListItem[] {
  return snapshot.items
}

// --- Splash coordination API ---

/** Returns true once the initial agent list fetch has completed (success or
 *  failure). Used by the splash screen to decide whether to wait. */
export function isAgentListFetched(): boolean {
  return initialFetchDone
}

/** Resolves when the initial agent list fetch completes. If already done,
 *  resolves immediately — callers never block unnecessarily. */
export function agentListReady(): Promise<void> {
  return initialFetchPromise
}

/** Kick off the agent list fetch before any component subscribes to the
 *  store. Safe to call multiple times — the internal dedup guard prevents
 *  duplicate requests. Returns a promise that resolves on completion. */
export function prefetchAgentList(): Promise<void> {
  start()
  return fetchFull()
}
