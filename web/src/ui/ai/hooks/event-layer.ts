import type { StepEvent, TurnEvent, AgentSessionSummaryResp } from '../../../gen-types/aigen'
import { client, waitForClientReady } from '../../../application/generated-client'
import { sessionTurnsToEnvelopes, turnStatusToEnvelope } from '../model/session-adapter'
import type { TurnEnvelope } from '../model/frame-types'
import { sameEnvelopeRender } from '../components/MessageStream'
import type { AgentSession } from './agent-session'
import { mergeSteps, replaceActiveTurnSteps, isTerminalTurnState } from './projection'
import { stepDebugEnabled } from './debug-flag'

// Re-export for downstream callers (timeline-manager, tests).
export { mergeSteps } from './projection'

/** AUDIT 10.1: classify transport-level errors as transient (loop should
 *  retry with backoff) vs persistent (loop should give up). Exported so
 *  timeline-manager's loadTimeline catch block can use the same definition
 *  to decide whether to start the event-layer retry loop — kicking the loop
 *  off on a persistent error (actor not found, permission denied) would
 *  spin forever between subscribe-fail and reconnect.
 *
 *  Transient categories:
 *   - transport: websocket closed / not connected / EOF
 *   - cold-start race: agent cell OnStart hasn't finished registering the
 *     event kind yet → "event ... not found". Agent cell OnStart hasn't
 *     finished takeSnapshot() yet → "session snapshot not ready". Both
 *     resolve within milliseconds of agent startup. */
export function isTransientError(msg: string): boolean {
  return msg.includes('websocket closed')
    || msg.includes('websocket not connected')
    || msg.includes('EOF')
    || msg.includes('not found')
    || msg.includes('snapshot not ready')
    || msg.includes('timed out')
    || msg.includes('timeout')
}

/** Trim a buffer that has exceeded the cap. Drops from the HEAD (oldest)
 *  so the most recent events — which carry live tail state like the latest
 *  block.delta, step.opened, step.closed — are preserved. Mutates the input
 *  array in place. Returns the number of dropped items (0 if not over cap).
 *  See AUDIT 2.1 — prior implementation spliced from the middle and lost the
 *  newest event entirely, causing permanent `appending` state. */
export function trimBufferHead<T>(buffer: T[], maxLen: number): number {
  if (buffer.length <= maxLen) return 0
  const keep = Math.floor(maxLen * 0.6)
  const drop = buffer.length - keep
  buffer.splice(0, drop)
  return drop
}

export function takeFlushBatch<T>(buffer: T[], maxLen: number): T[] {
  return buffer.splice(0, maxLen)
}

/** Interaction requests are terminal events for the current backend execution
 * slice: after emitting one, the turn blocks waiting for user input and may
 * produce no subsequent event. Flush them synchronously so they cannot remain
 * stranded behind a requestAnimationFrame that never gets another event to
 * reschedule it. */
export function shouldFlushStepEventImmediately(event: { Kind?: string }): boolean {
  return event.Kind === 'step.interaction_requested'
}

/** Content-level equality for history envelope lists. The summary ladder
 *  rebuilds envelope objects on every fetch (sessionTurnsToEnvelopes), so
 *  reference equality never holds and every rung used to replace
 *  state._historyEnvelopes with a fresh array — invalidating the projection
 *  cache and re-rendering the whole timeline even when nothing changed.
 *  sameEnvelopeRender (MessageStream's render-relevant comparator) defines
 *  "nothing visual changed" per envelope; equal lists let applySummaryToState
 *  skip the write and keep the old reference. */
function sameHistoryEnvelopes(a: TurnEnvelope[], b: TurnEnvelope[]): boolean {
  if (a === b) return true
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) {
    if (!sameEnvelopeRender(a[i]!, b[i]!)) return false
  }
  return true
}

/** Merge a fresh summary into state: refresh history envelopes, pull in any
 *  steps the frontend is missing (including brand-new turns the live event
 *  stream never delivered), and stale-close steps whose turns have ended.
 *  Shared by the onReconnect path and the post-submit reconcile.
 *  Returns true when the server's NextSeq indicates there are steps/events
 *  the frontend has not yet seen, signalling that the caller should try to
 *  load older history. */
export async function applySummaryToState(
  state: AgentSession,
  summary: AgentSessionSummaryResp | null,
): Promise<boolean> {
  if (!summary) return false
  const completedTurnIds = new Set(
    (summary.Turns ?? [])
      .filter(t => isTerminalTurnState(t.State))
      .map(t => t.Id),
  )

  state.setAgentState(summary.AgentState)
  console.debug('[event-layer] applySummaryToState Goal=', summary.Goal)
  state.setGoal(summary.Goal)
  const historyEnvs = sessionTurnsToEnvelopes(summary.Turns ?? [], summary.ExploreResults)
  if (summary.ActiveTurn?.Turn?.Id) {
    const activeTurnId = summary.ActiveTurn.Turn.Id
    // Skip when the turn is already terminal in the Turns list (snapshot race
    // where the stale running ActiveTurn lags behind the completed entry).
    // Allowing it would overwrite the correct completed envelope and leave
    // isStreaming stuck true after the turn completed during a reconnect.
    if (!completedTurnIds.has(activeTurnId)) {
      const existing = historyEnvs.findIndex(e => e.metadata?.turnId === activeTurnId)
      const activeEnv = turnStatusToEnvelope(summary.ActiveTurn)
      if (existing >= 0) historyEnvs[existing] = activeEnv
      else historyEnvs.push(activeEnv)
    }
  }
  // Same-content skip: when the fresh summary produces envelope-for-envelope
  // equal history, keep the existing array reference. _syncTurnTasksFromEnvelopes
  // only merges new turnIds (existing entries preserved), so the state after
  // a skipped write is identical to the state after a performed one.
  const historyChanged = !sameHistoryEnvelopes(state.historyEnvelopes, historyEnvs)
  // [jitter-diag] temporary instrumentation: reconcile-driven wholesale state
  // replacement is the suspected cause of the "whole stream refreshed" flash.
  console.info(`[jitter-diag] summary-apply histChanged=${historyChanged} serverSteps=${summary.Steps?.length ?? 0} missingSteps=${summary.MissingSteps?.length ?? 0} turns=${summary.Turns?.length ?? 0} activeTurn=${summary.ActiveTurn?.Turn?.Id ?? ''}`)
  if (historyChanged) {
    state.setHistoryEnvelopes(historyEnvs, false)
    // Reconcile _activeTurnStates against the authoritative summary records by
    // canonical Revision. A summary terminal record with a newer revision
    // overwrites stale locally-buffered lifecycle events; legacy records without
    // revision retain prior precedence.
    state.reconcileActiveTurnStatesFromHistory(historyEnvs)
  }

  const serverSteps = summary.Steps?.length ? summary.Steps : summary.MissingSteps
  let steps = state.steps

  // Authoritative active-turn replacement: when the server's
  // ActiveTurn.EventSeq watermark is ahead of the local turn's step-event
  // watermark, the local open steps are stale (deltas produced during a
  // background/reconnect window were lost). mergeSteps would keep the local
  // version (equal Seq, both open), so replace the turn's steps from the
  // server snapshot first, then bump the per-step dedup watermark so the
  // subsequent OpenStepEvents replay (already reflected in the snapshot) is
  // skipped instead of double-appending deltas.
  const activeTurn = summary.ActiveTurn
  const serverActiveTurnId = activeTurn?.Turn?.Id
  const serverActiveEventSeq = activeTurn?.EventSeq ?? 0
  if (serverActiveTurnId && serverActiveEventSeq > 0 && serverSteps && serverSteps.length > 0) {
    if (serverActiveEventSeq > state.activeTurnEventSeq(serverActiveTurnId)) {
      steps = replaceActiveTurnSteps(steps, serverActiveTurnId, serverSteps)
      state.bumpTurnEventSeq(serverActiveTurnId, serverActiveEventSeq)
    }
  }

  if (serverSteps && serverSteps.length > 0) {
    steps = mergeSteps(steps, serverSteps)
  }

  const hasStaleOpen = steps.some(s => !s.Closed && s.TurnId != null && completedTurnIds.has(s.TurnId))
  if (completedTurnIds.size > 0 && hasStaleOpen) {
    steps = steps.map(s =>
      s.Closed || s.TurnId == null || !completedTurnIds.has(s.TurnId)
        ? s
        : { ...s, Closed: true },
    )
  }

  // Always call setSteps — when steps is unchanged (no serverSteps came in),
  // this still triggers _confirmPendingUserMessagesFromSteps so any pending
  // entries whose steps never arrived get a chance to be confirmed via the
  // existing _steps. (See AUDIT.md 1.5.)
  const stepsChanged = steps !== state.steps
  state.setSteps(steps, false)

  if (summary.HasDiscardedSteps) {
    state.setDiscardedBoundary(true, false)
  }

  // Replay events for still-open steps so deltas that arrived before/during
  // the disconnect are applied on top of the summary snapshot. The step
  // reducer dedups by EventSeq, so events already seen are ignored.
  if (summary.OpenStepEvents && summary.OpenStepEvents.length > 0) {
    state.applyStepEvents(summary.OpenStepEvents)
  }

  // Notify only when something actually changed: history write, steps write,
  // or step-event replay. A rung of the reconnect summary ladder that lands
  // while the active turn is genuinely still running produces identical
  // content — the notify here would invalidate the projection cache and
  // re-render the whole timeline for nothing (the "stream refreshes itself"
  // symptom). applyStepEvents and setDiscardedBoundary(true) notify
  // internally when they change state, so their effects are not lost here.
  if (historyChanged || stepsChanged || (summary.OpenStepEvents?.length ?? 0) > 0) {
    state._notify()
  }

  // Gap detection: NextSeq is the server's next UI step sequence number.
  // If the largest Seq among local steps is behind NextSeq-1, there are
  // missing steps or events that may live in older turns.
  if (summary.NextSeq == null || summary.NextSeq <= 0 || !summary.HasMoreHistory) {
    return false
  }
  const maxLocalSeq = steps.reduce((max, s) => Math.max(max, Number(s.Seq ?? 0)), 0)
  return maxLocalSeq < summary.NextSeq - 1
}

// ── Generic stream consume loop ──

/** Per-await silence budget on a live subscription. When iter.next() stays
 *  unsettled this long — backend stream died without delivering its terminal
 *  frame — the consume loop forces a reconnect (resubscribe with sinceSeqNo
 *  + summary reconcile). 45s: well above any legitimate first-token /
 *  reasoning-gap silence on a healthy stream, short enough that a frozen
 *  timeline heals within a minute instead of staying stuck until the user
 *  sends another message. */
export const STREAM_SILENCE_TIMEOUT_MS = 45_000

interface StreamSlot<TEvent> {
  tag: string
  kind: string
  applyEvents: (events: TEvent[]) => void
  onInit?: (signal: AbortSignal, agentActorId: string) => void
  /** Reconnect body. Called after clearing iter/buffer, before re-entering the loop. */
  onReconnect: (
    waitForClient: () => Promise<void>,
    signal: AbortSignal,
    resetBackoff: () => void,
    backoff: () => Promise<void>,
    state: AgentSession,
  ) => Promise<void>
  // Mutable refs — mutated by the generic loop
  iter: AsyncIterator<unknown> | null
  abort: AbortController | null
  buffer: TEvent[]
  /** Events that were still buffered at disconnect time but not yet applied.
   *  They are replayed after the reconnect handshake (subscribe + summary)
   *  so a flush crash or scheduling race doesn't lose in-flight deltas. */
  pendingReconnectBuffer: TEvent[]
  /** Last subscription chunk seqNo received before disconnect. Used to resume
   *  the stream from the correct position on reconnect. */
  lastSeqNo: number
  rafId: number
  /** AUDIT 2.6: which API scheduled rafId — 'raf' (cancelAnimationFrame) or
   *  'timeout' (clearTimeout). Mixed cancellation was a silent no-op when
   *  the tab was hidden (rAF scheduled via setTimeout, cancelled via cAF). */
  rafKind: 'raf' | 'timeout' | undefined
}

function makeAbortedPromise(controller: AbortController) {
  const { signal } = controller
  return new Promise<never>((_, reject) => {
    if (signal.aborted) reject(new Error('aborted'))
    else signal.addEventListener('abort', () => reject(new Error('aborted')), { once: true })
  })
}

/** AUDIT 9.1: subscribe to a gospore.events stream for the given slot. Shared
 *  between the generic loop body (initial subscribe) and onReconnect
 *  (re-establish subscription BEFORE triggering reconcile fetch). The previous
 *  order — fetch first, resubscribe after — meant any event emitted between
 *  the summary snapshot and the new subscription landing had nowhere to land
 *  and was permanently lost. Subscribing first means the live stream is
 *  already listening by the time the fetch fires; the summary fills in the
 *  gap from before the snapshot, and the subscription catches everything
 *  after. If the fetch fails the event stream is still restored. */
function subscribeStream(slot: { kind: string; iter: AsyncIterator<unknown> | null; lastSeqNo: number }, agentActorId: string): void {
  const req: Record<string, unknown> = {
    actorId: agentActorId,
    kind: slot.kind,
  }
  // Resume from the last received chunk seqNo so the gateway can keep the
  // subscription sequence continuous across reconnects.
  if (slot.lastSeqNo > 0) {
    req.sinceSeqNo = slot.lastSeqNo
  }
  const iterable = client['transport'].subscribe('gospore.events.subscribe_instance', req, { withSeqNo: true })
  slot.iter = iterable[Symbol.asyncIterator]()
}

function createStreamLoop<TEvent>(
  slot: StreamSlot<TEvent>,
  state: AgentSession,
  notify: () => void,
  onFatal: () => void,
): void {
  const agentActorId = state.agentActorId
  const controller = new AbortController()
  const { signal } = controller
  const aborted = makeAbortedPromise(controller)
  const waitForClientOrCancel = () => Promise.race([waitForClientReady(), aborted])
  slot.abort = controller

  let backoffMs = 1000
  const backoff = async () => {
    await new Promise<void>(r => setTimeout(r, backoffMs))
    backoffMs = Math.min(backoffMs * 2, 30000)
  }
  const resetBackoff = () => { backoffMs = 1000 }

  let flushCrashCount = 0

  // Deaf-stream guard: the silence watchdog below is only armed while a turn
  // is streaming, so a race parked while idle must be reconstructed when the
  // session seeds an active turn (chat.submit → seedActiveTurn → notify).
  // Without this the parked race kept the watchdog disabled forever and the
  // timeline froze until an unrelated action resubscribed.
  let streamingWakeResolve: (() => void) | null = null
  let wasStreaming = state.isStreaming
  const unsubSession = state.subscribe(() => {
    const now = state.isStreaming
    if (now && !wasStreaming) {
      const r = streamingWakeResolve
      streamingWakeResolve = null
      r?.()
    }
    wasStreaming = now
  })

  const MAX_BUFFER = 200
  const MAX_FLUSH_BATCH = 50

  // scheduleFlush is hoisted out of the inner loop so flushBuffer's crash-
  // retry path (AUDIT 2.5) can reschedule. Uses slot.rafId as the
  // "pending flush" marker. Tracks the timer kind (rAF vs setTimeout) on
  // slot.rafKind so cancel uses the right API (AUDIT 2.6).
  const scheduleFlush = () => {
    if (slot.rafId) return
    const useTimeout = typeof document !== 'undefined' && document.visibilityState === 'hidden'
    if (useTimeout) {
      slot.rafId = setTimeout(() => { flushBuffer() }) as unknown as number
      slot.rafKind = 'timeout'
    } else {
      slot.rafId = requestAnimationFrame(() => { flushBuffer() }) as unknown as number
      slot.rafKind = 'raf'
    }
  }

  const cancelScheduledFlush = () => {
    if (!slot.rafId) return
    if (slot.rafKind === 'timeout') {
      clearTimeout(slot.rafId as unknown as ReturnType<typeof setTimeout>)
    } else {
      cancelAnimationFrame(slot.rafId)
    }
    slot.rafId = 0
    slot.rafKind = undefined
  }

  const flushBuffer = (all = false) => {
    slot.rafId = 0
    slot.rafKind = undefined
    const batch = takeFlushBatch(slot.buffer, all ? slot.buffer.length : MAX_FLUSH_BATCH)
    if (batch.length === 0) return
    const _t0 = typeof performance !== 'undefined' ? performance.now() : 0
    if (stepDebugEnabled()) console.debug(`[${slot.tag}.flush] flushing ${batch.length} events`)
    try {
      slot.applyEvents(batch)
      const _t1 = typeof performance !== 'undefined' ? performance.now() : 0
      if (stepDebugEnabled()) console.debug(`[${slot.tag}.flush] done`)
      flushCrashCount = 0
      notify()
      const _t2 = typeof performance !== 'undefined' ? performance.now() : 0
      if (_t2 - _t0 > 50) {
        console.warn(`[PERF ${slot.tag}.flush] SLOW: ${(_t2-_t0).toFixed(1)}ms (apply=${(_t1-_t0).toFixed(1)}ms notify=${(_t2-_t1).toFixed(1)}ms) batch=${batch.length}`)
      }
      if (slot.buffer.length > 0) scheduleFlush()
    } catch (err) {
      flushCrashCount++
      console.error(`[${slot.tag}.flush] CRASH (attempt ${flushCrashCount}):`, err)
      if (flushCrashCount >= 3) {
        console.error(`[${slot.tag}.flush] dropping ${batch.length} events after ${flushCrashCount} crashes; triggering reconcile`)
        flushCrashCount = 0
        notify()
        // AUDIT 2.2: previously just dropped silently with no recovery.
        // Trigger a summary reconcile so any state lost in the dropped
        // batch is recovered from the server snapshot.
        state.reconciler?.fetch('reconcile')
        return
      }
      // AUDIT 2.5: re-queue the batch and immediately schedule another flush.
      // Previously we unshifted but never rescheduled, so the batch sat in
      // the buffer until the NEXT inbound event kicked the scheduler — could
      // be never if the user was idle.
      slot.buffer.unshift(...batch)
      scheduleFlush()
    }
  }

  slot.onInit?.(signal, agentActorId)

  void (async () => {
    try {
    while (!signal.aborted) {
      let shouldRetry = false

      try { await waitForClientOrCancel(); resetBackoff() }
      catch { if (signal.aborted) break; await backoff(); continue }
      if (signal.aborted) break

      if (!slot.iter) {
        console.debug(`[${slot.tag}] creating new subscription: actorId=${agentActorId}`)
        subscribeStream(slot, agentActorId)
      }
      const iter = slot.iter

      slot.buffer = []
      slot.rafId = 0
      slot.rafKind = undefined

      try {
        while (iter) {
          // Watchdog: a backend stream can die without its terminal frame
          // ever reaching the client (pending-table eviction mid-dispatch,
          // transport hiccup). iter.next() then never settles and the loop
          // above never exits — the agent's timeline silently freezes until
          // some unrelated user action triggers a resubscribe.
          //
          // Only arm it while a turn is streaming: a running turn emits
          // heartbeat events (context_budget, block deltas) at least every
          // few tens of seconds, so 45s of silence there means the stream
          // is dead. An idle agent's silent stream is perfectly normal —
          // arming the watchdog there would force a reconnect every 45s on
          // every open timeline (observed as a regression in
          // timeline-manager's bounded-ladder test).
          const streaming = slot.tag === 'step' && !signal.aborted && state.isStreaming
          const racers: Promise<unknown>[] = [iter.next(), aborted]
          if (!streaming && slot.tag === 'step' && !signal.aborted) {
            // Parked idle: arm the streaming wake so the race reconstructs
            // with the watchdog once a turn is seeded.
            racers.push(new Promise<'streaming-wake'>(r => { streamingWakeResolve = () => r('streaming-wake') }))
          }
          if (streaming) {
            // Re-check isStreaming at fire time: the turn may have converged
            // while this timer was pending (the race was armed by a wake just
            // before the summary completed). Firing a reconnect on a now-idle
            // silent stream violates the idle-no-reconnect rule.
            racers.push(new Promise<'watchdog' | 'idle-again'>(r => {
              setTimeout(() => { r(state.isStreaming ? 'watchdog' : 'idle-again') }, STREAM_SILENCE_TIMEOUT_MS)
            }))
          }
          const result = await Promise.race(racers).catch(() => undefined)
          if (result === 'streaming-wake' || result === 'idle-again') continue
          if (result === undefined) break
          if (result === 'watchdog') {
            console.warn(`[${slot.tag}] stream silent for ${STREAM_SILENCE_TIMEOUT_MS}ms — forcing reconnect (lastSeqNo=${slot.lastSeqNo})`)
            break
          }
          if ((result as IteratorResult<unknown>).done || signal.aborted) break
          const raw = (result as IteratorResult<unknown>).value
          // With withSeqNo: true the transport yields { seqNo, payload } objects.
          // Fall back to treating the raw value as the event itself for callers
          // that pass a plain iterator (e.g. timeline-manager's initial load).
          const ev = (raw && typeof raw === 'object' && 'payload' in raw)
            ? (raw as { payload: TEvent }).payload
            : (raw as TEvent)
          const seqNo = (raw && typeof raw === 'object' && 'seqNo' in raw)
            ? (raw as { seqNo: number }).seqNo
            : 0
          if (seqNo > 0) {
            slot.lastSeqNo = seqNo
          }
          if (stepDebugEnabled()) console.debug(`[${slot.tag}.recv] ${(ev as any).Kind} stepId=${(ev as any).StepId} turnId=${(ev as any).TurnId} seqNo=${seqNo}`)
          slot.buffer.push(ev)
          const dropped = trimBufferHead(slot.buffer, MAX_BUFFER)
          if (dropped > 0) {
            console.debug(`[${slot.tag}] buffer cap ${MAX_BUFFER}: dropped ${dropped} oldest events`)
          }
          if (slot.tag === 'step' && shouldFlushStepEventImmediately(ev as { Kind?: string })) {
            // The backend blocks immediately after interaction_requested. Do
            // not leave this final event waiting for a future animation frame:
            // cancel the pending batch callback and apply all preceding step
            // events plus the interaction atomically now.
            cancelScheduledFlush()
            flushBuffer(true)
          } else {
            scheduleFlush()
          }
        }
      } catch (err) {
        if (!signal.aborted) {
          const msg = err instanceof Error ? err.message : String(err)
          if (msg.includes('mailbox: closed')) {
            cancelScheduledFlush()
            onFatal()
            return
          }
          if (isTransientError(msg)) { shouldRetry = true }
          else { console.error(`[${slot.tag}] stream error:`, err) }
        }
      }

      cancelScheduledFlush()
      flushBuffer(true)
      if (signal.aborted) {
        void iter?.return?.()
        break
      }

      // Both transient-error retry and normal stream end go through
      // onReconnect. The normal-end path used to only re-subscribe without
      // a summary fallback, which meant any events dropped during the brief
      // disconnect were permanently lost (AUDIT 2.4). onReconnect fetches
      // a summary to recover.
      // Dispose the old iterator before discarding the reference so the
      // transport sends an unsubscribe frame and the backend tears down its
      // subscription goroutine. Without this the old stream leaks a backend
      // goroutine per reconnect cycle.
      void iter?.return?.()
      slot.iter = null
      // Preserve any events that survived flushBuffer (e.g. re-queued after a
      // flush crash) so they can be replayed after the reconnect handshake.
      if (slot.buffer.length > 0) {
        slot.pendingReconnectBuffer.push(...slot.buffer)
        slot.buffer = []
      }
      if (!shouldRetry) {
        // Normal end: notify listeners that streaming paused, then reconnect.
        notify()
      }
      await slot.onReconnect(waitForClientOrCancel, signal, resetBackoff, backoff, state)
      if (signal.aborted) break

      // Replay events buffered at disconnect time after the summary has been
      // applied, so late-arriving deltas are not overwritten by the snapshot.
      if (slot.pendingReconnectBuffer.length > 0) {
        const batch = slot.pendingReconnectBuffer.splice(0)
        console.debug(`[${slot.tag}.reconnect] replaying ${batch.length} buffered events`)
        slot.applyEvents(batch)
        notify()
      }
      continue
    }
    } finally {
      unsubSession()
    }
  })()
}

// ── Reconnect summary ladder throttle ──

/** Max concurrent session_summary reconcile ladders across all tracked agents
 *  after a transport reconnect. A reconnect storms every tracked timeline
 *  (up to MAX_TRACKED_AGENTS=8 in timeline-manager), each running a bounded
 *  1+3-rung ladder — an unthrottled fan-out would fire up to 32 simultaneous
 *  session_summary RPCs at the backend. Cap is 3: the active agent is granted
 *  a slot first, the rest queue by LRU recency (the caller enqueues in that
 *  order; the pump is strictly FIFO). */
export const RECONNECT_SUMMARY_CONCURRENCY = 3

interface ReconnectRequest {
  agentActorId: string
  runner: () => Promise<void>
  resolve: () => void
  promise: Promise<void>
}

const reconnectQueue: ReconnectRequest[] = []
const reconnectRunning = new Set<string>()

/** Enqueue a reconnect reconcile ladder behind the concurrency gate. Returns a
 *  promise that resolves when the ladder's critical-path fetch settles (or
 *  immediately when the request is deduped against an already-running ladder
 *  for the same agent — the work is already covered).
 *
 *  The per-agent ladder itself is untouched: same retry schedule, same
 *  LADDER_DELAYS_MS bound, same single-flight guards inside
 *  SummaryReconciler.reconcileLadder. Only the START of the ladder is gated.
 *
 *  Ordering policy is the caller's business: enqueue the active agent first,
 *  then the rest most-recently-used first, to get "active agent first, others
 *  in LRU recency" (timeline-manager's onConnected handler does exactly that). */
export function requestReconnectLadder(agentActorId: string, runner: () => Promise<void>): Promise<void> {
  // Already running: the in-flight ladder covers this request.
  if (reconnectRunning.has(agentActorId)) return Promise.resolve()
  // Already queued: merge — the latest runner wins, both awaiters share one slot.
  const existing = reconnectQueue.find(q => q.agentActorId === agentActorId)
  if (existing) {
    existing.runner = runner
    return existing.promise
  }
  let resolve!: () => void
  const promise = new Promise<void>(r => { resolve = r })
  reconnectQueue.push({ agentActorId, runner, resolve, promise })
  pumpReconnect()
  return promise
}

function pumpReconnect(): void {
  while (reconnectRunning.size < RECONNECT_SUMMARY_CONCURRENCY && reconnectQueue.length > 0) {
    const req = reconnectQueue.shift()!
    // Guard against a stale duplicate (request() already dedupes, but keep the
    // invariant cheap) — resolve so no awaiter is left hanging.
    if (reconnectRunning.has(req.agentActorId)) {
      req.resolve()
      continue
    }
    reconnectRunning.add(req.agentActorId)
    Promise.resolve()
      .then(req.runner)
      // Runners (reconcileLadder wrappers) never reject by contract, but a
      // stray rejection must not become an unhandled rejection. The slot is
      // released in finally regardless.
      .catch(() => {})
      .finally(() => {
        reconnectRunning.delete(req.agentActorId)
        req.resolve()
        pumpReconnect()
      })
  }
}

/** Reset the throttle (queue + in-flight set). Test-only: vitest keeps one
 *  module instance per file, so a storm test must not leak occupied slots
 *  into the next test. */
export function resetReconnectThrottleForTest(): void {
  reconnectQueue.length = 0
  reconnectRunning.clear()
}

// ── Event layer ──

export class AgentEventLayer {
  private _state: AgentSession
  private _notify: () => void
  private _onFatal: () => void

  private _step: StreamSlot<StepEvent> = { tag: 'step', kind: 'step', applyEvents: () => {}, onReconnect: async () => {}, iter: null, abort: null, buffer: [], pendingReconnectBuffer: [], lastSeqNo: 0, rafId: 0, rafKind: undefined }
  private _turn: StreamSlot<TurnEvent> = { tag: 'turn', kind: 'turn', applyEvents: () => {}, onReconnect: async () => {}, iter: null, abort: null, buffer: [], pendingReconnectBuffer: [], lastSeqNo: 0, rafId: 0, rafKind: undefined }

  constructor(state: AgentSession, notify: () => void, onFatal: () => void) {
    this._state = state
    this._notify = notify
    this._onFatal = onFatal

    // ── Step slot config ──
    this._step.applyEvents = (batch) => state.applyStepEvents(batch)
    // AUDIT 9.2: only the step slot triggers reconnect fetch. Previously
    // both step and turn fired fetch('reconnect') on every disconnect — the
    // reconciler is single-flight so the second aborted the first, doubling
    // latency for no benefit. The fetch summary covers both streams anyway.
    // AUDIT 9.1: re-subscribe BEFORE fetch so events emitted between the
    // summary snapshot and the new subscription landing are not lost.
    this._step.onReconnect = async (waitForClient, signal, resetBackoff, backoff, _state) => {
      try {
        await waitForClient()
        if (signal.aborted) return
        // Show a transient loading state while the reconnect handshake runs so
        // the user does not stare at stale history while the summary catches up.
        state.setLoading(true)
        if (!this._step.iter) {
          subscribeStream(this._step, state.agentActorId)
        }
        resetBackoff()
        // AUDIT 12.1: the reconnect handshake summary is the first rung of a
        // bounded retry ladder (SummaryReconciler.reconcileLadder). The
        // immediate fetch stays on this critical path; extra fetches continue
        // in the background until the summary stops reporting a running active
        // turn, covering the "turn completed inside the resubscribe window"
        // race that used to pin isStreaming=true forever when the single
        // snapshot landed while the turn was still running.
        //
        // Reconnect storm throttling: a transport reconnect can hit every
        // tracked agent (≤8) at once — an unthrottled fan-out would fire up to
        // 32 concurrent session_summary RPCs. The ladder's start is gated by
        // requestReconnectLadder to ≤RECONNECT_SUMMARY_CONCURRENCY in flight;
        // the ladder itself is unchanged (same retry schedule, same
        // LADDER_DELAYS_MS bound, same single-flight guards).
        await requestReconnectLadder(state.agentActorId, async () => {
          // The stream may have been aborted while this request waited for a
          // concurrency slot (timeline cleared) — do not fire a summary RPC
          // for a released session.
          if (signal.aborted) return
          await state.reconciler?.reconcileLadder('reconnect')
        })
      } catch {
        if (signal.aborted) return
        await backoff()
      } finally {
        if (!signal.aborted) {
          state.setLoading(false)
        }
      }
    }

    // ── Turn slot config ──
    this._turn.applyEvents = (batch) => {
      let historyImported = false
      for (const ev of batch) {
        if (ev.Kind === 'turn.history_imported') historyImported = true
        state.applyTurnEvent(ev)
      }
      // A clone/fork received session history off the live turn loop. That
      // import emits no step/turn lifecycle events, so the timeline — which
      // fetched an empty summary at clone time — never shows the imported
      // turns. Re-fetch the authoritative summary, but only while the timeline
      // is still in its initial empty state: once history is displayed, older
      // background-chunk imports are surfaced via scroll-load (loadOlderTurns),
      // and a wholesale reconcile here would wipe scroll-loaded envelopes.
      if (historyImported && state.getSnapshot().envelopes.length === 0) {
        this._state.reconciler?.fetch('reconcile')
      }
    }
    // AUDIT 9.1: mirror the step slot's order — re-subscribe BEFORE
    // resetBackoff so turn events during the fetch window are captured.
    this._turn.onReconnect = async (waitForClient, signal, resetBackoff, backoff, _state) => {
      try {
        await waitForClient()
        if (signal.aborted) return
        if (!this._turn.iter) {
          subscribeStream(this._turn, state.agentActorId)
        }
        resetBackoff()
      } catch {
        if (signal.aborted) return
        await backoff()
      }
    }
  }

  /** Set the initial stream iterators (called before start()). */
  setStreamIters(stepIter: AsyncIterator<unknown>, turnIter: AsyncIterator<unknown>): void {
    this._step.iter = stepIter
    this._turn.iter = turnIter
  }

  start(): void {
    createStreamLoop(this._step, this._state, this._notify, this._onFatal)
    createStreamLoop(this._turn, this._state, this._notify, this._onFatal)
  }

  /** Apply any buffered step/turn events immediately so a subsequent
   *  reconcile works against the most recent live state instead of replacing
   *  it with a stale summary snapshot. */
  flushPendingEvents(): void {
    // AUDIT 2.6: use the right cancel API for the scheduled timer kind.
    for (const slot of [this._step, this._turn]) {
      if (!slot.rafId) continue
      if (slot.rafKind === 'timeout') {
        clearTimeout(slot.rafId as unknown as ReturnType<typeof setTimeout>)
      } else {
        cancelAnimationFrame(slot.rafId)
      }
      slot.rafId = 0
      slot.rafKind = undefined
    }
    const stepBatch = this._step.buffer.splice(0)
    if (stepBatch.length > 0) {
      this._state.applyStepEvents(stepBatch)
    }
    const turnBatch = this._turn.buffer.splice(0)
    for (const ev of turnBatch) {
      this._state.applyTurnEvent(ev)
    }
  }

  /** One-shot proactive reconcile: flush buffered events, then delegate to
   *  the reconciler for a single-flight summary fetch. Used after the user
   *  sends a message so the assistant turn appears even if the live step
   *  stream dropped the first events. */
  reconcileNow(): void {
    this.flushPendingEvents()
    this._state.reconciler?.fetch('reconcile')
  }

  abort(): void {
    this._step.abort?.abort()
    this._turn.abort?.abort()
    // AUDIT 2.6: cancel via the right API for the scheduled timer kind.
    for (const slot of [this._step, this._turn]) {
      if (!slot.rafId) continue
      if (slot.rafKind === 'timeout') {
        clearTimeout(slot.rafId as unknown as ReturnType<typeof setTimeout>)
      } else {
        cancelAnimationFrame(slot.rafId)
      }
      slot.rafId = 0
      slot.rafKind = undefined
    }
    this._step.buffer = []
    this._step.pendingReconnectBuffer = []
    this._turn.buffer = []
    this._turn.pendingReconnectBuffer = []
    void this._step.iter?.return?.()
    void this._turn.iter?.return?.()
  }
}
