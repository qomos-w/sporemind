import type { TurnEnvelope, PlanFrame } from '../model/frame-types'
import { client, waitForClientReady } from '../../../application/generated-client'
import { waitForBackendReady } from '../../../application/backend-ready'
import { sessionTurnsToEnvelopes, turnStatusToEnvelope } from '../model/session-adapter'

import { AgentSession } from './agent-session'
import { AgentEventLayer, mergeSteps, applySummaryToState, isTransientError, requestReconnectLadder } from './event-layer'
import { SummaryReconciler } from './summary-reconciler'
import type { PendingEntry } from './pending-message-queue'
import * as agentTurnsClient from '../../../gen-clients/local/client'
import * as agentTurn from '../../../gen-clients/local/client'
import * as agentChat from '../../../gen-clients/local/client'

// ── Helpers ──

function genTempUserId(): string {
  return `pending-user-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
}

export { mergeSteps }

// ── Types ──

export interface TimelineState {
  envelopes: TurnEnvelope[]
  isStreaming: boolean
  isPaused: boolean
  isWaiting: boolean
  isPausing: boolean
  loading: boolean
  loadingMore: boolean
  error: string | null
  hasMoreHistory: boolean
  summarizedBoundary: boolean
  discardedBoundary: boolean
  /** The model unit the aggregator actually resolved for the current dispatch
   *  (β feedback). Undefined until reported. */
  currentUnit?: import('../../../gen-types/aigen').ModelUnit | undefined
}

/** Local events dispatched by UI interaction (answer ask_user, resolve permission, plan approval, goal submit, goal card submit). */
export type LocalEvent =
  | { kind: 'ai.ask_answered'; requestId: string; answers: Record<number, string> }
  | { kind: 'ai.permission_answered'; requestId: string; allowed: boolean; allowInProject?: boolean }
  | { kind: 'ai.plan_approval_answered'; requestId: string; decision: 'approve' | 'reject' | 'edit' | 'confirm_goal' | 'start_workflow'; editedPlan?: string; feedback?: string; selectedPolicy?: PlanFrame['policy'] }
  | { kind: 'ai.goal_submit_answered'; requestId: string; decision: 'approve' | 'reject'; feedback?: string }
  | { kind: 'ai.goal_card_submit_answered'; requestId: string; decision: 'approve' | 'reject'; feedback?: string }

/** @internal exported for testing only */
export interface AgentTimeline {
  agentActorId: string
  layer: AgentSession
  eventLayer: AgentEventLayer | null
  cancelLoad: (() => void) | null
  /** Monotonic counter bumped whenever a real load starts. The placeholder
   *  rAF in select() captures the generation before scheduling and bails
   *  out if it changed by the time the rAF fires, so it can't clobber a
   *  real loading state set in the meantime (AUDIT 5.8). */
  loadingGeneration: number
  /** AUDIT 1.2: independent flag for "real" loading (loadOlderTurns, init
   *  fetch, etc.). select()'s placeholder rAF checks this flag and refuses
   *  to flip loading back to false while it's set — sharing loadingGeneration
   *  with loadOlderTurns meant rapid switching could truncate real loads. */
  realLoading: boolean
  /** Safety timeout handle armed whenever realLoading is set to true. Forces
   *  realLoading=false + loading=false after LOADING_SAFETY_TIMEOUT_MS even
   *  if SummaryReconciler.onSettle never fires (e.g. backend-ready infinite
   *  loop, ws stuck in connecting). Cleared on legitimate settle / abort /
   *  eviction. */
  loadingTimeoutId: ReturnType<typeof setTimeout> | null
}

// ── Manager state (module singleton) ──

/** Upper bound on tracked agent timelines. Each timeline owns two SSE/WS
 *  streams + per-step caches + history envelopes, so unbounded growth is a
 *  real resource leak (not just memory). When select() would exceed the cap,
 *  the least-recently-used timeline is evicted via clearAgentTimeline so its
 *  streams abort and caches release.
 *
 *  Cap is 8. The real fix for eviction cost is a snapshot cache that survives
 *  eviction so re-selecting an evicted agent renders immediately from cached
 *  envelopes while a fresh load reconciles in the background. */
const MAX_TRACKED_AGENTS = 8

const timelines = new Map<string, AgentTimeline>()
const globalListeners = new Set<() => void>()
const agentListeners = new Map<string, Set<() => void>>()
let selectedId: string | null = null

let notifyQueued = false
const pendingAgentIds = new Set<string>()

/** ClientIds that were cancelled before submit returned their real messageId.
 *  When replaceUserMessageId is called later, we detect the late cancel and
 *  fire the backend cancel_pending with the now-known message ID. */
const cancelledClientIds = new Set<string>()

/** Look up a timeline and promote it to most-recently-used. Map preserves
 *  insertion order in JS, so delete + set moves the entry to the tail. */
function getTimelineLRU(id: string): AgentTimeline | undefined {
  const tl = timelines.get(id)
  if (!tl) return undefined
  timelines.delete(id)
  timelines.set(id, tl)
  return tl
}

/** Evict the oldest timeline if at cap. Called before inserting a new entry.
 *  Uses clearAgentTimeline so streams abort + caches release. */
function evictIfAtCap(): void {
  if (timelines.size < MAX_TRACKED_AGENTS) return
  const oldestId = timelines.keys().next().value
  if (oldestId == null) return
  console.debug(`[timeline] LRU evict: actorId=${oldestId} (cap=${MAX_TRACKED_AGENTS})`)
  clearAgentTimeline(oldestId)
}

function scheduleNotifyFor(agentActorId?: string) {
  if (agentActorId) pendingAgentIds.add(agentActorId)
  if (notifyQueued) return
  notifyQueued = true
  requestAnimationFrame(() => {
    notifyQueued = false
    const agentIds = new Set(pendingAgentIds)
    pendingAgentIds.clear()
    // Event-driven completion of a deferred history import (clone/fork): the
    // turn.history_imported reconcile fills envelopes off the poll cadence, so
    // clear the pending state + skeleton here instead of waiting for the next
    // fallback tick.
    for (const id of pendingHistoryAgents) {
      const tl = timelines.get(id)
      if (tl && tl.layer.getSnapshot().envelopes.length > 0) {
        pendingHistoryAgents.delete(id)
        tl.layer.setLoading(false)
        agentIds.add(id)
      }
    }
    for (const l of globalListeners) l()
    for (const id of agentIds) {
      const set = agentListeners.get(id)
      if (set) {
        for (const l of set) l()
      }
    }
  })
}

const EMPTY_STATE: TimelineState = Object.freeze({
  envelopes: Object.freeze([] as TurnEnvelope[]),
  isStreaming: false,
  isPaused: false,
  isPausing: false,
  loading: false,
  loadingMore: false,
  error: null,
  hasMoreHistory: false,
  summarizedBoundary: false,
  discardedBoundary: false,
}) as TimelineState

function getEmptyState(): TimelineState {
  return EMPTY_STATE as TimelineState
}

function getSelectedOrThrow(): AgentTimeline {
  const id = selectedId
  if (!id) throw new Error('TimelineManager: no agent selected')
  const tl = getTimelineLRU(id)
  if (!tl) throw new Error(`TimelineManager: selected agent ${id} not found`)
  return tl
}


// ── Agent timeline lifecycle ──

/** Safety ceiling on realLoading. SummaryReconciler.onSettle has known early-
 *  return paths (aborted, superseded) that skip the callback; if the reconciler
 *  also never gets to retry (e.g. backend-ready infinite loop, ws stuck), this
 *  timer is the only escape from an infinite spinner. 30s is well above normal
 *  summary latency (<1s) and below user attention span. */
const LOADING_SAFETY_TIMEOUT_MS = 15_000

function armLoadingTimeout(tl: AgentTimeline): void {
  disarmLoadingTimeout(tl)
  tl.loadingTimeoutId = setTimeout(() => {
    tl.loadingTimeoutId = null
    if (!tl.realLoading) return
    console.warn(`[timeline] realLoading safety timeout (${LOADING_SAFETY_TIMEOUT_MS}ms) — forcing clear: actorId=${tl.agentActorId}`)
    tl.realLoading = false
    if (tl.layer.getSnapshot().loading) tl.layer.setLoading(false)
    scheduleNotifyFor(tl.agentActorId)
  }, LOADING_SAFETY_TIMEOUT_MS)
}

function disarmLoadingTimeout(tl: AgentTimeline): void {
  if (tl.loadingTimeoutId) {
    clearTimeout(tl.loadingTimeoutId)
    tl.loadingTimeoutId = null
  }
}

function clearAgentTimeline(agentActorId: string): void {
  const tl = timelines.get(agentActorId)
  if (tl) {
    // Disarm safety timeout FIRST — the timer captures `tl` and would fire
    // into a released session otherwise. Modern JS GC handles the leak but
    // the warn log would be noise.
    disarmLoadingTimeout(tl)
    // Abort in-flight summary fetch FIRST so its callback can't reach the
    // session after release (AUDIT 3.4). Order matters: reconciler.abort →
    // eventLayer.abort → release.
    tl.layer.reconciler?.abort()
    tl.eventLayer?.abort()
    tl.cancelLoad?.()
    // release() clears all state + per-agent caches (stepCache, seqTracking,
    // _projectionCache). Was reset() which left caches alive, slowly leaking
    // across many select/release cycles.
    tl.layer.release()
  }
  timelines.delete(agentActorId)
  if (selectedId === agentActorId) {
    selectedId = null
  }
  scheduleNotifyFor(agentActorId)
}

// This space intentionally empty — consume loops moved to event-layer.ts

// ── Public API ──

export interface TimelineManager {
  select: (agentActorId: string) => void
  /** Ensure an agent's timeline is loaded/streaming without making it the
   *  globally-selected agent. Restores the previous selection synchronously
   *  (the async rAF notify never observes the switch), so background
   *  consumers like the card MiniComposer can read a non-active agent's
   *  envelopes without hijacking the main timeline. */
  load: (agentActorId: string) => void
  release: (agentActorId: string) => void
  pushUserMessage: (text: string, actorId?: string) => string
  replaceUserMessageId: (tempId: string, realId: string, actorId?: string) => void
  updateUserMessageIdx: (messageId: string, idx: number, actorId?: string) => void
  dispatchLocalEvent: (event: LocalEvent, actorId?: string) => void
  rollbackLocalInteraction: (requestId: string, actorId?: string) => void
  stop: (actorId?: string) => void
  pause: (actorId?: string) => void
  pauseAll: (actorId?: string) => void
  resume: (actorId?: string) => void
  loadOlderTurns: (actorId?: string) => void
  /** Proactively backfill missing steps/history for an agent (e.g. after send). */
  reconcile: (actorId?: string) => void
  /** Seed a running turn in active state before turn.started SSE arrives,
   *  so isStreaming returns true and the assistant envelope renders immediately. */
  seedActiveTurn: (agentActorId: string, turnActorId: string) => void
  getSnapshot: (agentActorId: string | null) => TimelineState
  subscribe: (cb: () => void, agentActorId?: string) => () => void
  getSelectedId: () => string | null
  /** Whether a timeline for the given agent is currently tracked (loaded or loading). */
  hasTimeline: (agentActorId: string) => boolean
  /** P2: reap stale open steps whose turn terminal event was lost. */
  reapStale: (agentActorId?: string) => void
  /** Return all agent actor IDs currently tracked by the manager. */
  getTrackedAgents: () => string[]
  /** Replace the current session history with imported envelopes. Clears live steps. */
  importHistory: (envelopes: TurnEnvelope[], actorId?: string) => void
  /** Clear the local conversation state for an agent (e.g. after /clear). */
  clearHistory: (actorId?: string) => void
  /** @internal — unconfirmed pending user messages keyed by clientId, for reconnect re-submit. */
  getPendingUserMessages: (actorId: string) => readonly PendingEntry[]
  /** Remove a pending (unconfirmed) user message by clientId so it disappears
   *  from the optimistic "queued message" UI. The backend may still process it,
   *  but the confirmation path is a no-op when the entry is already gone. */
  cancelPendingUserMessage: (clientId: string, actorId?: string) => void
  /** Mark an agent as expecting deferred session history (clone/fork). The
   *  backend returns the AgentRef before a detached goroutine finishes copying
   *  the source session. The event bus has no replay, so the import event is
   *  frequently dropped. The initial empty summary is polled until history
   *  arrives or a timeout elapses. */
  expectPendingHistory: (agentActorId: string) => void
}

async function loadTimeline(
  agentActorId: string,
  eventLayer: AgentEventLayer,
  tl: AgentTimeline,
  cancelled: { v: boolean },
  expectPending?: boolean,
): Promise<void> {
  try {
    await waitForClientReady()
    if (cancelled.v) return
    await waitForBackendReady()
    if (cancelled.v) return

    const stepIterable = client['transport'].subscribe('gospore.events.subscribe_instance', {
      actorId: agentActorId,
      kind: 'step',
    }, { withSeqNo: true })
    const stepIter = stepIterable[Symbol.asyncIterator]()
    console.debug(`[timeline] subscribed step: actorId=${agentActorId}`)

    const turnIterable = client['transport'].subscribe('gospore.events.subscribe_instance', {
      actorId: agentActorId,
      kind: 'turn',
    }, { withSeqNo: true })
    const turnIter = turnIterable[Symbol.asyncIterator]()
    console.debug(`[timeline] subscribed turn: actorId=${agentActorId}`)

    eventLayer.setStreamIters(stepIter, turnIter)

    // Seed the β channel (current executing unit) from the agent's persisted
    // status. turn.unit_changed only fires when the unit changes, so a reload,
    // agent switch, or same-model dispatch would otherwise leave the composer
    // without the in-use unit until the next model change. seedCurrentUnit
    // never clobbers a live-updated value.
    agentTurnsClient.agentStatus(client, { target: agentActorId })
      .then(resp => {
        if (cancelled.v) return
        if (resp.CurrentUnit?.model) tl.layer.seedCurrentUnit(resp.CurrentUnit)
      })
      .catch(() => {})

    // Delegate the initial summary fetch to the reconciler (single-flight).
    // start() is called immediately so the consume loop buffers events while
    // the summary is in flight — the onApply callback merges them correctly.
    tl.layer.reconciler?.fetch('init')
    eventLayer.start()

    // Clone/fork: the backend copies session history in a detached goroutine
    // that starts before the clone RPC returns. The initial fetch often lands
    // empty, and the import event is frequently dropped (forward-only bus).
    // Poll the summary until history appears or a timeout elapses.
    if (expectPending) {
      pollPendingHistory(agentActorId, tl, cancelled)
    }
  } catch (err) {
    if (cancelled.v) return
    const msg = err instanceof Error ? err.message : String(err)
    console.error(`[timeline] load error: ${msg}`, err)
    pendingHistoryAgents.delete(agentActorId)
    if (msg.includes('mailbox: closed')) {
      clearAgentTimeline(agentActorId)
      return
    }
    tl.layer.setLoading(false)
    tl.layer.setError(msg)
    // AUDIT 10.1: realLoading was set to true in select(); the reconciler's
    // onSettle would normally clear it, but on a persistent load error fetch
    // never runs and onSettle never fires. Clear it here so a subsequent
    // select() for the same agent isn't stuck on the placeholder rAF that
    // refuses to flip loading back to false while realLoading is true.
    disarmLoadingTimeout(tl)
    tl.realLoading = false
    scheduleNotifyFor(agentActorId)
    // AUDIT 10.1: only kick off the event-layer retry loop for transient
    // transport errors. Starting it on a persistent error (actor not found,
    // permission denied, malformed request) would spin forever between
    // subscribe-fail and onReconnect, hammering the backend. The user can
    // retry by re-selecting the agent.
    if (isTransientError(msg)) {
      eventLayer.start()
    }
  }
}

// Agents whose session history is being imported asynchronously by the backend
// (clone/fork). Cleared when history arrives or the poll times out.
const pendingHistoryAgents = new Set<string>()

/** Fallback polling for an agent that expects deferred history (clone/fork).
 *  The fast path is event-driven: turn.history_imported triggers an immediate
 *  reconcile (event-layer.ts) and scheduleNotifyFor clears the pending state
 *  as soon as envelopes arrive. These few long-interval ticks only cover the
 *  case where the import event fired before the stream subscription was
 *  established (forward-only bus) and was dropped. */
function pollPendingHistory(
  agentActorId: string,
  tl: AgentTimeline,
  cancelled: { v: boolean },
): void {
  const fallbackDelays = [1000, 4000, 10000]
  let attempt = 0
  const tick = () => {
    if (cancelled.v || attempt >= fallbackDelays.length || tl.layer.getSnapshot().envelopes.length > 0) {
      pendingHistoryAgents.delete(agentActorId)
      tl.layer.setLoading(false)
      scheduleNotifyFor(agentActorId)
      return
    }
    // Keep the loading skeleton visible while we wait for the deferred copy.
    tl.layer.setLoading(true)
    tl.layer.reconciler?.fetch('reconcile')
    setTimeout(tick, fallbackDelays[attempt++])
  }
  // Let the initial fetch settle before the first re-fetch.
  setTimeout(tick, 600)
}

/** Create a singleton TimelineManager.  Call once at module init; consumers
 *  reference the returned interface directly. */
export function createTimelineManager(): TimelineManager {
  return {
    select(agentActorId: string) {
      console.debug(`[timeline] select: actorId=${agentActorId}`)
      const existing = getTimelineLRU(agentActorId)
      if (existing) {
        selectedId = agentActorId
        const snap = existing.layer.getSnapshot()

        // Retry path: prior load left an error. Re-selecting the agent should
        // re-attempt, not show stale error forever. Tear down streams + abort
        // any in-flight reconcile, clear error, kick off a fresh loadTimeline.
        // AUDIT 10.1's comment claimed "user can retry by re-selecting" —
        // without this branch that was false; existing-timeline selects never
        // re-loaded.
        if (snap.error) {
          console.debug(`[timeline]   → retrying after error: actorId=${agentActorId}`)
          existing.layer.reconciler?.abort()
          existing.eventLayer?.abort()
          existing.cancelLoad?.()
          existing.layer.setError(null)
          existing.layer.setLoading(true)
          existing.realLoading = true
          armLoadingTimeout(existing)
          existing.loadingGeneration++
          const cancelled = { v: false }
          existing.cancelLoad = () => { cancelled.v = true }
          if (existing.eventLayer) {
            void loadTimeline(agentActorId, existing.eventLayer, existing, cancelled)
          }
          scheduleNotifyFor(agentActorId)
          return
        }

        // Converge to authoritative state on reselect: a stream that died
        // silently leaves the cached timeline frozen mid-turn, and nothing
        // else re-fetches until the next send or transport reconnect. The
        // reconciler is single-flight and the apply only notifies when the
        // summary actually differs, so a healthy timeline re-renders nothing.
        void existing.layer.reconciler?.fetch('reconcile')

        if (!snap.envelopes.length) {
          existing.layer.setLoading(true)
          scheduleNotifyFor(agentActorId)
          // AUDIT 5.8: capture the loading generation before the rAF fires.
          // If something else (loadOlderTurns) sets loading=true for a real
          // reason during the wait, we must not clobber it back to false.
          const loadingGen = ++existing.loadingGeneration
          requestAnimationFrame(() => {
            if (existing.loadingGeneration !== loadingGen) return
            // AUDIT 1.2: realLoading is the authoritative signal — if a real
            // load is in progress, the placeholder rAF must not reset it.
            if (existing.realLoading) return
            existing.layer.setLoading(false)
            scheduleNotifyFor(agentActorId)
          })
        } else {
          scheduleNotifyFor(agentActorId)
        }
        console.debug(`[timeline]   → switched to existing timeline (loading placeholder)`)
        return
      }

      // Create a fresh timeline with loading state.
      const layer = new AgentSession(agentActorId)
      layer.subscribe(() => scheduleNotifyFor(agentActorId))
      layer.setLoading(true)
      const tl: AgentTimeline = {
        agentActorId,
        layer,
        cancelLoad: null,
        eventLayer: null,
        loadingGeneration: 0,
        realLoading: true,
        loadingTimeoutId: null,
      }
      const eventLayer = new AgentEventLayer(layer, () => scheduleNotifyFor(agentActorId), () => clearAgentTimeline(agentActorId))
      tl.eventLayer = eventLayer
      armLoadingTimeout(tl)

      // Create the reconciler on the session so event-layer can delegate
      // reconnect / reconcile calls through it.
      layer.reconciler = new SummaryReconciler(
        agentActorId,
        layer,
        (summary, mode) => {
          if (mode === 'init') {
            // Merge server steps with whatever the live stream populated
            // between start() and this summary landing — preserves any
            // step.opened / block.delta events that would otherwise be
            // wiped by a wholesale replace (AUDIT 3.1).
            const turns = summary?.Turns ?? []
            const historyEnvs = sessionTurnsToEnvelopes(turns, summary?.ExploreResults)
            if (summary?.ActiveTurn?.Turn?.Id) {
              const activeTurnId = summary.ActiveTurn.Turn.Id
              const activeEnv = turnStatusToEnvelope(summary.ActiveTurn)
              const existing = historyEnvs.findIndex(e => e.metadata?.turnId === activeTurnId)
              if (existing >= 0) historyEnvs[existing] = activeEnv
              else historyEnvs.push(activeEnv)
            }
            layer.applyInitSummary(
              summary?.Steps ?? [],
              historyEnvs,
              summary?.HasMoreHistory ?? false,
              summary?.HasDiscardedSteps ?? false,
              summary?.AgentState,
              summary?.Goal,
            )
          } else {
            void applySummaryToState(layer, summary).then(hasGap => {
              if (hasGap && summary?.HasMoreHistory) {
                console.debug(`[timeline] step seq gap detected (local max < ${summary.NextSeq! - 1}), loading older turns`)
                this.loadOlderTurns(agentActorId)
              }
            })
          }
        },
        (err) => {
          console.error(`[timeline] reconciler fatal:`, err)
          clearAgentTimeline(agentActorId)
        },
        // AUDIT 1.2: realLoading clears when the authoritative fetch settles,
        // so select()'s placeholder rAF knows whether to flip loading off.
        // Also disarms the safety timeout — if onSettle fires legitimately
        // the timer is no longer needed.
        () => { disarmLoadingTimeout(tl); tl.realLoading = false },
      )

      // LRU: evict the oldest timeline before inserting the new one so we
      // never hold more than MAX_TRACKED_AGENTS simultaneous stream pairs.
      evictIfAtCap()
      timelines.set(agentActorId, tl)
      selectedId = agentActorId
      scheduleNotifyFor(agentActorId) // UI shows loading placeholder

      const cancelled = { v: false }
      tl.cancelLoad = () => { cancelled.v = true }

      void loadTimeline(agentActorId, eventLayer, tl, cancelled, pendingHistoryAgents.has(agentActorId))
    },

    load(agentActorId: string) {
      const existing = getTimelineLRU(agentActorId)
      if (existing && !existing.layer.getSnapshot().error) return
      const prev = selectedId
      this.select(agentActorId)
      // select() set selectedId and queued an async rAF notify. Restore the
      // previous selection synchronously so the main timeline never observes
      // a switch — subscribers only re-render on the next rAF, by which time
      // selectedId is already restored. The loaded timeline keeps streaming
      // in the background, keyed by its own actor id.
      if (prev && prev !== agentActorId) {
        selectedId = prev
        scheduleNotifyFor(prev)
      }
    },

    release(agentActorId: string) {
      clearAgentTimeline(agentActorId)
    },

    pushUserMessage(text: string, actorId?: string): string {
      const tl = actorId ? getTimelineLRU(actorId) : (selectedId ? getTimelineLRU(selectedId) : undefined)
      const clientId = genTempUserId()
      if (!tl) {
        if (actorId) {
          console.warn(`[timeline] pushUserMessage: no timeline for actorId=${actorId}`)
        }
        return clientId
      }
      // Always store the pending message. If the backend step stream drops the
      // user_inject event (or the client reconnects before it arrives), the
      // pending entry lets the UI either show a queued indicator or re-submit.
      tl.layer.addPendingUserMessage(clientId, text)
      scheduleNotifyFor(tl.agentActorId)
      return clientId
    },

    replaceUserMessageId(tempId: string, realId: string, actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : (selectedId ? getTimelineLRU(selectedId) : undefined)
      if (!tl) return
      // Bind the real messageId so confirmByMessageId can match when the
      // step.opened event arrives with OriginMessageId === realId.
      tl.layer.bindMessageId(tempId, realId)
      // Late cancel: the user clicked X before submit returned. The local
      // entry is already gone (bindMessageId is a no-op), but the backend
      // still has the PendingSubmit. Cancel it now with the real ID.
      if (cancelledClientIds.delete(tempId)) {
        agentChat.chatCancelPending(client, { MessageId: realId }, { target: tl.agentActorId }).catch((err: unknown) => {
          console.warn('[timeline] late cancel_pending failed:', err)
        })
      }
    },

    updateUserMessageIdx(_messageId: string, _idx: number, _actorId?: string) {
      // No-op: ordering is driven by step stream order, not client-side idx.
    },

    dispatchLocalEvent(event: LocalEvent, actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : getSelectedOrThrow()
      if (!tl) {
        if (actorId) {
          console.warn(`[timeline] dispatchLocalEvent: no timeline for actorId=${actorId}`)
          return
        }
        throw new Error('TimelineManager: no agent selected')
      }
      console.debug(`[timeline] dispatchLocalEvent: kind=${event.kind} requestId=${event.requestId}`)

      tl.layer.dispatchLocalEvent(event)
      scheduleNotifyFor(tl.agentActorId)
    },

    rollbackLocalInteraction(requestId: string, actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : getSelectedOrThrow()
      if (!tl) {
        if (actorId) return
        throw new Error('TimelineManager: no agent selected')
      }
      console.debug(`[timeline] rollbackLocalInteraction: requestId=${requestId}`)
      tl.layer.rollbackLocalInteraction(requestId)
      scheduleNotifyFor(tl.agentActorId)
    },

    stop(actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : getSelectedOrThrow()
      if (!tl) {
        if (actorId) return
        throw new Error('TimelineManager: no agent selected')
      }
      console.debug(`[timeline] stop: actorId=${tl.agentActorId}`)
      const { agentActorId } = tl
      if (agentActorId) {
        agentTurn.turnCancel(client, { target: agentActorId }).catch((err: unknown) => {
          const msg = err instanceof Error ? err.message : String(err)
          if (!msg.includes('EOF')) {
            console.error('[timeline] cancel failed:', err)
          }
        })
      }
      // AUDIT 1.8: cancelling the turn means any queued pending submits
      // will never be consumed by this turn. Mark them failed so the UI
      // reflects the cancellation immediately instead of waiting 30s for TTL.
      tl.layer.markPendingMessagesFailed()
      scheduleNotifyFor(agentActorId)
    },

    pause(actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : getSelectedOrThrow()
      if (!tl) {
        if (actorId) return
        throw new Error('TimelineManager: no agent selected')
      }
      const { agentActorId } = tl
      if (agentActorId) {
        // Optimistically mark the agent as pausing so the composer button keeps
        // the spinner even if the user switches agents before the backend ack.
        tl.layer.setPendingPause(true)
        agentTurn.turnPause(client, { target: agentActorId }).catch((err: unknown) => {
          console.error('[timeline] pause failed:', err)
          // If the backend rejects the pause, clear the optimistic spinner so
          // the UI doesn't stay stuck in "pausing".
          tl.layer.setPendingPause(false)
        })
      }
    },

    pauseAll(actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : getSelectedOrThrow()
      if (!tl) {
        if (actorId) return
        throw new Error('TimelineManager: no agent selected')
      }
      const { agentActorId } = tl
      if (agentActorId) {
        tl.layer.setPendingPause(true)
        agentTurn.workflowPauseAll(client, {}, { target: agentActorId }).catch((err: unknown) => {
          console.error('[timeline] pause-all failed:', err)
          tl.layer.setPendingPause(false)
        })
      }
    },

    resume(actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : getSelectedOrThrow()
      if (!tl) {
        if (actorId) return
        throw new Error('TimelineManager: no agent selected')
      }
      const { agentActorId } = tl
      if (agentActorId) {
        agentTurn.turnResume(client, { target: agentActorId }).catch((err: unknown) => {
          console.error('[timeline] resume failed:', err)
        })
      }
    },

    reconcile(actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : (selectedId ? getTimelineLRU(selectedId) : undefined)
      if (!tl) return
      tl.eventLayer?.reconcileNow()
    },

    seedActiveTurn(agentActorId: string, turnActorId: string) {
      const tl = getTimelineLRU(agentActorId)
      if (!tl) return
      tl.layer.seedActiveTurn(turnActorId)
    },

    loadOlderTurns(actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : getSelectedOrThrow()
      if (!tl) {
        if (actorId) return
        throw new Error('TimelineManager: no agent selected')
      }
      const { agentActorId } = tl
      if (!agentActorId || !tl.layer.hasMoreHistory || tl.layer.loadingMore) return
      const envelopes = tl.layer.getSnapshot().envelopes
      // Avoid anchoring on envelopes whose seq is missing (e.g. legacy compaction
      // steps with Seq=0). Fall back to the plain oldest assistant if none match.
      const oldest = envelopes.find(e => e.role === 'assistant' && e.metadata?.turnId && e.turnSeq != null)
        ?? envelopes.find(e => e.role === 'assistant' && e.metadata?.turnId)
      const beforeTurnId = oldest?.metadata?.turnId
      if (!beforeTurnId) return

      console.debug(`[timeline] loadOlderTurns: beforeTurnId=${beforeTurnId}`)
      tl.layer.setLoadingMore(true)

      void agentTurnsClient.turnsList(client, { BeforeTurnId: beforeTurnId, Limit: 20 }, { target: agentActorId, timeoutMs: 60_000 }).then(resp => {
        const userTurnIds = new Set(
          (resp.Steps ?? [])
            .filter(s => s.Role === 'user' && s.TurnId)
            .map(s => s.TurnId!),
        )
        const olderTurns = resp.Turns.filter(turn =>
          !(turn.Role !== 'user' && turn.State === 'paused' && userTurnIds.has(turn.Id)),
        )
        const olderEnvelopes = sessionTurnsToEnvelopes(olderTurns)
        console.debug(`[timeline]   → loaded ${olderEnvelopes.length} older turns, rawTurns=${resp.Turns.length}, userTurnIds=${userTurnIds.size}, steps=${resp.Steps?.length ?? 0}, hasMore=${resp.HasMore}`)
        // Trust the backend's HasMore flag. During async clone background copy
        // the backend may return 0 turns with HasMore=true because more history
        // is still being copied. Hiding the button would prevent the user from
        // loading it later.
        if (olderEnvelopes.length === 0 && resp.HasMore) {
          tl.layer.setLoadingMore(false)
          tl.layer.setError('History anchor is not available yet')
        } else {
          tl.layer.appendOlderHistory(olderEnvelopes, resp.HasMore, resp.Steps, false)
        }
        scheduleNotifyFor(agentActorId)
      }).catch((err: unknown) => {
        const msg = err instanceof Error ? err.message : String(err)
        console.error(`[timeline]   → loadOlderTurns failed: ${msg}`)
        tl.layer.setLoadingMore(false)
        tl.layer.setError(msg)
        scheduleNotifyFor(agentActorId)
      })
    },

    getSnapshot(agentActorId: string | null): TimelineState {
      if (!agentActorId) return getEmptyState()
      return timelines.get(agentActorId)?.layer.getSnapshot() ?? getEmptyState()
    },

    hasTimeline(agentActorId: string): boolean {
      return timelines.has(agentActorId)
    },

    /** P2: reap stale open steps on the given agent's session. No-op if the
     *  agent isn't tracked or has no session. Driven by useTimelineManager's
     *  coarse timer so a stuck turn (lost terminal event) never leaves a
     *  compaction step spinning forever. */
    reapStale(agentActorId?: string): void {
      if (!agentActorId) return
      timelines.get(agentActorId)?.layer.reapStaleOpenSteps()
    },

    subscribe(cb: () => void, agentActorId?: string): () => void {
      if (agentActorId) {
        let set = agentListeners.get(agentActorId)
        if (!set) {
          set = new Set()
          agentListeners.set(agentActorId, set)
        }
        set.add(cb)
        return () => { set!.delete(cb) }
      }
      globalListeners.add(cb)
      return () => { globalListeners.delete(cb) }
    },

    getSelectedId(): string | null {
      return selectedId
    },

    getTrackedAgents(): string[] {
      return Array.from(timelines.keys())
    },

    importHistory(envelopes: TurnEnvelope[], actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : getSelectedOrThrow()
      if (!tl) {
        if (actorId) {
          console.warn(`[timeline] importHistory: no timeline for actorId=${actorId}`)
          return
        }
        throw new Error('TimelineManager: no agent selected')
      }
      console.debug(`[timeline] importHistory: envelopes=${envelopes.length} actorId=${tl.agentActorId}`)
      tl.layer.initFromLoad([], envelopes, tl.layer.hasMoreHistory)
      scheduleNotifyFor(tl.agentActorId)
    },

    clearHistory(actorId?: string) {
      const tl = actorId ? getTimelineLRU(actorId) : (selectedId ? getTimelineLRU(selectedId) : undefined)
      if (!tl) {
        if (actorId) {
          console.warn(`[timeline] clearHistory: no timeline for actorId=${actorId}`)
        }
        return
      }
      console.debug(`[timeline] clearHistory: actorId=${tl.agentActorId}`)
      tl.layer.reconciler?.abort()
      tl.layer.clear()
      scheduleNotifyFor(tl.agentActorId)
    },

    getPendingUserMessages(actorId: string) {
      const tl = timelines.get(actorId)
      if (!tl) return []
      return tl.layer.getPendingUserMessages()
    },

    cancelPendingUserMessage(clientId: string, actorId?: string): void {
      const tl = actorId ? getTimelineLRU(actorId) : (selectedId ? getTimelineLRU(selectedId) : undefined)
      if (!tl) return
      // Remove from local pending queue immediately for instant UI feedback.
      const messageId = tl.layer.getPendingMessageId(clientId)
      tl.layer.confirmUserMessage(clientId)
      scheduleNotifyFor(tl.agentActorId)
      if (messageId) {
        // MessageId already bound — cancel on backend right now.
        agentChat.chatCancelPending(client, { MessageId: messageId }, { target: tl.agentActorId }).catch((err: unknown) => {
          console.warn('[timeline] cancel_pending failed:', err)
        })
      } else {
        // MessageId not bound yet (submit still in flight). Track the clientId
        // so replaceUserMessageId can fire the backend cancel when the real
        // messageId arrives.
        cancelledClientIds.add(clientId)
      }
    },

    expectPendingHistory(agentActorId: string): void {
      pendingHistoryAgents.add(agentActorId)
    },
  }
}

// Reset the realLoading safety timeout on reconnect: the new connection may
// still complete the in-flight load, so give it a fresh window instead of
// letting a timeout armed before the disconnect fire immediately.
const transport = typeof (client as any).getTransport === 'function' ? (client as any).getTransport() as any : null
if (transport && typeof transport.onConnected === 'function') {
  transport.onConnected(({ isReconnect }: { isReconnect: boolean }) => {
    if (!isReconnect) return
    for (const tl of timelines.values()) {
      if (tl.realLoading) {
        armLoadingTimeout(tl)
      }
    }
    // Transport reconnect is transparent to subscribe iterators: they never
    // return done and are auto-resumed via resumeSubscriptions, so the
    // stream-loop onReconnect path (which re-fetches a summary to correct
    // stale active-turn state) is NOT entered when forceReconnectClient
    // closes and reopens the socket (e.g. Capacitor appStateChange resume,
    // visibilitychange, online). Re-fetch every tracked timeline so a turn
    // that completed while the app was backgrounded is pruned from the
    // active-turn map via applySummaryToState → pruneActiveTurnStates,
    // instead of staying stuck in 'running' with isStreaming pinned true.
    // AUDIT 12.1: the single fetch can still race a turn completing inside
    // the resubscribe window — if the snapshot lands while the turn is
    // still 'running', the terminal event emitted during the gap was lost
    // (forward-only event bus) and nothing would ever unpin isStreaming.
    // reconcileLadder re-fetches with bounded backoff until the summary
    // stops reporting a running active turn (or the bound is exhausted).
    //
    // Reconnect storm throttling: the fan-out above would run every tracked
    // agent's 1+3-rung ladder simultaneously (worst case 8 × 4 = 32
    // concurrent session_summary RPCs). requestReconnectLadder gates the
    // ladder starts to ≤RECONNECT_SUMMARY_CONCURRENCY in flight. Priority is
    // decided here by enqueue order: the selected (active) agent first, the
    // rest by LRU recency — `timelines` is a Map, so iteration order is
    // insertion order and getTimelineLRU moves a touched entry to the tail,
    // making the tail the most-recently-used.
    const ids = Array.from(timelines.keys())
    const ordered: string[] = []
    if (selectedId && timelines.has(selectedId)) {
      ordered.push(selectedId)
    }
    for (let i = ids.length - 1; i >= 0; i--) {
      if (ids[i] !== selectedId) {
        ordered.push(ids[i]!)
      }
    }
    for (const id of ordered) {
      const tl = timelines.get(id)
      if (!tl?.layer.reconciler) continue
      requestReconnectLadder(id, async () => {
        // The timeline may have been cleared / re-selected while the request
        // waited for a concurrency slot — only run against the same instance.
        if (timelines.get(id) !== tl) return
        await tl.layer.reconciler?.reconcileLadder('reconnect')
      })
    }
  })
}
