import type { AgentSessionSummaryResp, Step } from '../../../gen-types/aigen'
import type { AgentSession } from './agent-session'
import { client, waitForClientReady } from '../../../application/generated-client'
import { waitForBackendReady } from '../../../application/backend-ready'
import * as agentSession from '../../../gen-clients/local/client'

export type ReconcileMode = 'init' | 'reconnect' | 'reconcile'

/** Backoff between the *extra* summary fetches of a reconnect reconcile
 *  ladder. The first fetch runs immediately on the caller's critical path;
 *  each subsequent rung fires only while the server summary still reports a
 *  running active turn ("not converged"). Total ladder span ≈ 6s, hard-bounded
 *  at LADDER_DELAYS_MS.length extra fetches regardless of how long the turn
 *  actually runs — a genuinely long-running turn is left to the live stream,
 *  only the short resubscribe-window completion race gets the extra coverage. */
export const LADDER_DELAYS_MS: readonly number[] = [500, 1500, 4000]

/** Convergence check for the reconnect reconcile ladder. The ladder exists to
 *  cover the "turn completed inside the resubscribe window" race: the one-shot
 *  reconnect summary can land while the turn is still 'running', and the
 *  terminal event emitted during the gap had no subscriber and was lost,
 *  leaving isStreaming pinned true forever. Once the server summary no longer
 *  reports an active running turn, the applied state is authoritative and the
 *  ladder stops. 'paused'/'waiting'/terminal/absent all mean "not streaming",
 *  so only 'running' keeps the ladder alive. */
export function isSummaryConverged(summary: AgentSessionSummaryResp | null): boolean {
  if (!summary) return true
  const active = summary.ActiveTurn?.Turn
  if (!active?.Id) return true
  return active.State !== 'running'
}

/** Hard cap on the number of steps sent via knownStepEventSeqs. Per the
 *  research conclusion ([[锁定 KnownStepEventSeqs 裁剪语义]]): the backend only
 *  consumes the map's max Seq as the reconnect watermark
 *  (computeReconnectTurnWindow) and looks up MissingSteps inside its ≤50-turn
 *  response window; tail-K = 2500 covers the 50 turn × 50 step/turn stress
 *  baseline and always contains the newest step, so the watermark never
 *  regresses. */
export const KNOWN_STEPS_CAP = 2500

/** Build the minimal knownStepEventSeqs (stepId → last known step.Seq) for the
 *  reconnect handshake. Instead of sending every session step (payload grows
 *  linearly with history), only the newest KNOWN_STEPS_CAP steps by Seq are
 *  sent, with the global max-Seq step explicitly retained as the watermark.
 *  The backend compares each recent step's Seq against this map and returns
 *  the step as Missing when unseen or newer. The legacy field name is retained
 *  for wire compatibility. Bound: |map| ≤ KNOWN_STEPS_CAP + 1. */
export function buildKnownStepEventSeqs(session: AgentSession): Record<string, number> {
  const steps = session.steps
  if (steps.length === 0) return {}

  // `_steps` is maintained in Seq-ascending order (appendOlderHistory /
  // mergeSteps), but sort defensively so the newest-K selection by Seq never
  // depends on it. Steps without a finite Seq rank first and are skipped below.
  const seqRank = (s: Step): number => (s.Seq != null && !Number.isNaN(s.Seq) ? s.Seq : -1)
  const bySeq = [...steps].sort((a, b) => seqRank(a) - seqRank(b))

  const out: Record<string, number> = {}
  const tail = bySeq.slice(Math.max(0, bySeq.length - KNOWN_STEPS_CAP))
  for (const s of tail) {
    if (s.Id && s.Seq != null && !Number.isNaN(s.Seq)) out[s.Id] = s.Seq
  }

  // Explicit global max-Seq watermark: pin the entry with the largest finite
  // Seq so the backend reconnect window never regresses. A no-op in the
  // normal sorted case (the newest step is already inside the tail).
  for (let i = bySeq.length - 1; i >= 0; i--) {
    const s = bySeq[i]!
    if (s.Id && s.Seq != null && !Number.isNaN(s.Seq)) {
      out[s.Id] = s.Seq
      break
    }
  }
  return out
}

interface InFlight {
  ctrl: AbortController
  seq: number
}

/** Result of a single runFetch. Never rejects — every exit path is classified
 *  so the reconnect ladder can decide whether to keep fetching. */
type FetchOutcome =
  | { applied: true; summary: AgentSessionSummaryResp; mode: ReconcileMode }
  | { applied: false; reason: 'aborted' | 'superseded' | 'error'; mode: ReconcileMode }

/** Single-flight summary fetch with AbortController + request sequencing.
 *  All agentSession.sessionSummary calls are routed through this class so that
 *  concurrent requests (e.g. reconcileNow + onReconnect) are serialized
 *  and only the latest result is applied. */
export class SummaryReconciler {
  private inFlight: InFlight | null = null
  private counter = 0
  /** Monotonic generation for reconnect ladders. Any background ladder step
   *  that wakes from its sleep is invalidated by a newer generation, so a
   *  fresh reconnect signal takes over instead of two ladders interleaving
   *  summary RPCs. */
  private ladderGeneration = 0
  /** Abort controller for the current ladder's inter-step sleeps. Aborted by
   *  a newer reconcileLadder (new signal) and by abort() (teardown). */
  private ladderSleepCtrl: AbortController | null = null

  constructor(
    private target: string,
    private session: AgentSession,
    private onApply: (summary: AgentSessionSummaryResp | null, mode: ReconcileMode) => void,
    private onFatal?: (err: unknown) => void,
    /** AUDIT 1.2: invoked when a fetch settles (success, fatal, retry-exhausted)
     *  but NOT on abort/supersede. Lets callers clear realLoading flags without
     *  duplicating logic at every exit branch. */
    private onSettle?: (mode: ReconcileMode) => void,
  ) {}

  /** Fetch agent summary with mode-aware retry/backoff.
   *
   *  'init' does not poll OnStart: the backend pure session.summary handler
   *  waits off the owner loop until OnStart seeds its atomic snapshot. This
   *  avoids the workspace→project→agent deadlock caused by waiting inside a
   *  spawn handler. Keep one retry for request timeout/network jitter only.
   *
   *  'reconnect'/'reconcile' keep the short window — they run against an
   *  already-running agent, so long waits are usually network hiccups. */
  private retrySchedule(mode: ReconcileMode): { maxAttempts: number; delays: number[] } {
    if (mode === 'init') {
      return {
        maxAttempts: 2,
        delays: [300],
      }
    }
    return { maxAttempts: 3, delays: [200, 800, 2000] }
  }

  /** AUDIT 11.1: abortable sleep — a plain setTimeout would ignore the abort
   *  signal, so an aborted old request kept the inFlight slot occupied while
   *  its timer ran out. Throws when aborted so callers can bail out. */
  private async sleepAbortable(ms: number, signal: AbortSignal): Promise<void> {
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(resolve, ms)
      signal.addEventListener('abort', () => {
        clearTimeout(timer)
        reject(new Error('aborted'))
      }, { once: true })
    })
  }

  private async fetchWithRetry(
    ctrl: AbortController,
    seq: number,
    mode: ReconcileMode,
  ): Promise<AgentSessionSummaryResp> {
    const { maxAttempts, delays } = this.retrySchedule(mode)

    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      try {
        return await agentSession.sessionSummary(
          client,
          { MaxTurns: 5, knownStepEventSeqs: buildKnownStepEventSeqs(this.session) },
          { target: this.target, timeoutMs: 60_000 },
        )
      } catch (err) {
        if (ctrl.signal.aborted) throw err
        if (seq !== this.counter) throw err

        const msg = err instanceof Error ? err.message : String(err)
        if (msg.includes('mailbox: closed')) throw err // fatal

        // The pure session.summary handler waits off the owner loop for OnStart
        // to seed its snapshot. A not-ready response now means that wait hit its
        // request deadline; treat it as transient for the single init retry.
        const isTransient =
          msg.includes('service not found') ||
          msg.includes('not registered') ||
          msg.includes('session snapshot not ready') ||
          msg.includes('timed out') ||
          msg.includes('timeout')
        if (!isTransient || attempt >= maxAttempts - 1) throw err

        console.warn(`[summary-reconciler] retrying (attempt ${attempt + 1}/${maxAttempts}, mode=${mode}):`, err)
        await this.sleepAbortable(delays[attempt]!, ctrl.signal)
      }
    }
    throw new Error('fetchWithRetry: exhausted attempts')
  }

  /** One summary fetch through the full guard stack (abort in-flight, client
   *  ready, backend ready, abort/supersede sequence checks, fatal handling).
   *  Shared by fetch() and the reconnect reconcile ladder; never rejects. */
  private async runFetch(mode: ReconcileMode): Promise<FetchOutcome> {
    if (this.inFlight) {
      this.inFlight.ctrl.abort()
      this.inFlight = null
    }

    const ctrl = new AbortController()
    const seq = ++this.counter
    this.inFlight = { ctrl, seq }

    try {
      await waitForClientReady()
      if (ctrl.signal.aborted) return { applied: false, reason: 'aborted', mode }
      await waitForBackendReady()
      if (ctrl.signal.aborted) return { applied: false, reason: 'aborted', mode }

      const summary = await this.fetchWithRetry(ctrl, seq, mode)

      if (ctrl.signal.aborted) return { applied: false, reason: 'aborted', mode }
      if (seq !== this.counter) return { applied: false, reason: 'superseded', mode }

      this.inFlight = null
      this.onApply(summary, mode)
      this.onSettle?.(mode)
      return { applied: true, summary, mode }
    } catch (err) {
      if (ctrl.signal.aborted) return { applied: false, reason: 'aborted', mode }
      if (seq !== this.counter) return { applied: false, reason: 'superseded', mode }
      this.inFlight = null
      const msg = err instanceof Error ? err.message : String(err)
      if (msg.includes('mailbox: closed')) {
        this.onFatal?.(err)
        this.onSettle?.(mode)
        return { applied: false, reason: 'error', mode }
      }
      // AUDIT 7.2: 重试耗尽/不可重试错误时清 loading 设 error，
      // 否则 timeline 永久停在 loading 直到下一次 WS 重连。
      console.warn(`[summary-reconciler] fetch failed (mode=${mode}):`, err)
      if (mode === 'init') {
        this.session.setLoading(false)
        this.session.setError(msg)
      }
      this.onSettle?.(mode)
      return { applied: false, reason: 'error', mode }
    }
  }

  /** Initiate a summary fetch. If a request is already in flight it is
   *  aborted and replaced. The caller is responsible for ensuring
   *  waitForClientReady resolves before calling fetch.
   *
   *  Returns a promise that settles when the fetch completes, retries are
   *  exhausted, or the request is superseded. Rejections are handled internally;
   *  callers can use `.finally()` to clear transient loading states. */
  fetch(mode: ReconcileMode): Promise<void> {
    return this.runFetch(mode).then(() => undefined)
  }

  /** Bounded reconnect reconcile ladder (B2: 重连 reconcile 有界重试阶梯).
   *
   *  After a transport reconnect the one-shot summary fetch can land while a
   *  turn that completed inside the resubscribe window is still reported as
   *  'running' — the terminal event emitted during the gap had no subscriber
   *  and is permanently lost, pinning isStreaming=true forever. This method
   *  runs the first fetch on the caller's critical path (the returned promise
   *  settles when it settles) and, when the summary still reports a running
   *  active turn, continues with a *bounded* ladder of extra fetches at
   *  LADDER_DELAYS_MS intervals until the summary converges (no running
   *  active turn), the request is superseded/aborted/errors, or the bound is
   *  reached. The continuation runs detached so reconnect handshakes /
   *  loading toggles are never blocked by the retries.
   *
   *  The ladder is safe for genuinely long-running turns: if the server really
   *  is still executing, the extra fetches all report 'running' and the ladder
   *  exhausts its bound and gives up, leaving completion to the live stream. */
  reconcileLadder(mode: ReconcileMode): Promise<void> {
    const gen = ++this.ladderGeneration
    this.ladderSleepCtrl?.abort()
    const sleepCtrl = new AbortController()
    this.ladderSleepCtrl = sleepCtrl

    return this.runFetch(mode).then((first) => {
      if (!first.applied) return
      if (isSummaryConverged(first.summary)) return
      void this.ladderSteps(gen, sleepCtrl, mode)
    })
  }

  private async ladderSteps(gen: number, sleepCtrl: AbortController, mode: ReconcileMode): Promise<void> {
    try {
      for (let i = 0; i < LADDER_DELAYS_MS.length; i++) {
        if (gen !== this.ladderGeneration || sleepCtrl.signal.aborted) return
        try {
          await this.sleepAbortable(LADDER_DELAYS_MS[i]!, sleepCtrl.signal)
        } catch {
          return // aborted by a newer ladder or abort()
        }
        if (gen !== this.ladderGeneration || sleepCtrl.signal.aborted) return
        const res = await this.runFetch(mode)
        if (!res.applied) return // superseded / aborted / error → stop laddering
        if (isSummaryConverged(res.summary)) return
      }
    } finally {
      if (gen === this.ladderGeneration && this.ladderSleepCtrl === sleepCtrl) {
        this.ladderSleepCtrl = null
      }
    }
  }

  /** Cancel any in-flight request and stop any pending ladder steps. Safe to
   *  call multiple times. */
  abort(): void {
    this.ladderGeneration++
    this.ladderSleepCtrl?.abort()
    this.ladderSleepCtrl = null
    if (this.inFlight) {
      this.inFlight.ctrl.abort()
      this.inFlight = null
    }
  }
}
