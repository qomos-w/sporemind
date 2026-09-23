import type { TurnEnvelope, ToolFrame, Frame, PlanFrame, LocalInteractionResponse, TaskEntry, FileChangeEntry, PendingSubmitEntry, ContextBudget, SessionGoal } from '../model/frame-types'
import type { Step } from '../../../gen-types/aigen'
import type { PendingEntry } from './pending-message-queue'
import { stepsToEnvelopes, dedupeEnvelopes } from './steps-to-envelopes'

/** Frontend turn lifecycle state vocabulary. 'resumed' is not a backend
 *  terminal state; turn.resumed events always transition back to 'running'.
 *  'abandoned' is a backend terminal state written by restart-recovery when a
 *  turn's live engine is gone. */
export type TurnState = 'running' | 'paused' | 'waiting' | 'completed' | 'failed' | 'cancelled' | 'abandoned'
export type TerminalTurnState = Extract<TurnState, 'waiting' | 'completed' | 'failed' | 'cancelled' | 'abandoned'>

/** True for any value in the frontend TurnState vocabulary. Used to decide
 *  whether a history envelope's metadata.turnState can be trusted as the
 *  authoritative state. Unknown / legacy values (e.g. 'resumed') fall through
 *  to step-derived heuristics. */
export function isValidTurnState(state: string | undefined): state is TurnState {
  return state === 'running' || state === 'paused' || state === 'waiting' || state === 'completed' || state === 'failed' || state === 'cancelled' || state === 'abandoned'
}

/** True for terminal states only — never treats 'resumed' as terminal. */
export function isTerminalTurnState(state: string | undefined): state is TerminalTurnState {
  return state === 'waiting' || state === 'completed' || state === 'failed' || state === 'cancelled' || state === 'abandoned'
}

/** Authoritative turn-level state keyed by turnId. Same shape as
 *  AgentStateLayer.TurnStateEntry — duplicated here to keep the projection
 *  layer decoupled from the state-layer class. */
export interface TurnStateEntry {
  state: TurnState
  /** Canonical monotonic revision observed for this turn record. Undefined for
   *  entries created before revision tracking or seeded without a revision. */
  revision?: number
  startedAt?: string
  completedAt?: string
  error?: string
  turnOrder?: number
  retryCount?: number
  retryMax?: number
  retryError?: string
}

/** Compare optional monotonic Seq values. When only one side has Seq, the
 *  side with Seq is considered newer (greater). Callers fall back to
 *  timestamp tiebreakers only when both sides lack Seq. */
export function compareOptionalSeq(a: number | undefined, b: number | undefined): number {
  const hasA = a != null && a > 0 && !Number.isNaN(a)
  const hasB = b != null && b > 0 && !Number.isNaN(b)
  if (hasA && hasB) return a! - b!
  if (hasA) return 1   // a has Seq, b doesn't → a is newer
  if (hasB) return -1  // b has Seq, a doesn't → b is newer
  return 0
}

/** Sort steps by Seq (when both sides have it), falling back to timestamp. */
function stepSortCompare(a: Step, b: Step): number {
  const cmp = compareOptionalSeq(a.Seq, b.Seq)
  if (cmp !== 0) return cmp
  return (a.Timestamp || '').localeCompare(b.Timestamp || '')
}

/** Sort steps by Seq (timestamp as legacy tiebreaker). Fast path: during
 *  streaming the reducer already keeps the array in order, and this runs on
 *  every projection pass — copying + sorting a multi-thousand-step array per
 *  rAF flush was pure overhead. Returns the input reference when already
 *  sorted (callers treat the result as read-only). */
function sortStepsBySeq(steps: Step[]): Step[] {
  for (let i = 1; i < steps.length; i++) {
    if (stepSortCompare(steps[i - 1]!, steps[i]!) > 0) {
      return [...steps].sort(stepSortCompare)
    }
  }
  return steps
}

/** Merge incoming steps into an existing step list, keyed by StepId. Used by
 *  applySummaryToState (reconnect/reconcile) and AgentSession.applyInitSummary
 *  (initial load) so live-stream steps are preserved when a summary lands.
 *
 *  Precedence (high → low):
 *  1. Closed beats open (a closed snapshot is always more complete).
 *  2. Higher Seq wins when both sides carry Seq.
 *  3. Side-with-Seq wins over side-without-Seq.
 *  4. Otherwise keep existing — NEVER fall back to a client-clock timestamp
 *     compare. AUDIT 4.5: the prior `(current.Timestamp <= s.Timestamp)`
 *     tiebreaker fired when both sides lacked Seq, and local-clock skew
 *     caused fully-rendered server steps to be rejected in favor of partial
 *     local ones. Step ordering is still deterministic because
 *     sortStepsBySeq uses the same Seq-first compare with timestamp as a
 *     final tiebreaker — but only for SORT, not for content selection.
 *
 *  AUDIT 2.3 / 7.3: when incoming is fully covered by existing (every entry
 *  is a duplicate that loses the precedence check), returns `existing`
 *  unchanged. The reconciler's `knownStepEventSeqs` is snapshotted at fetch
 *  entry, so by the time the response lands the live stream has often
 *  already delivered the same steps the backend returns as MissingSteps.
 *  Returning a new array here would assign a fresh reference to
 *  session._steps and ripple into projection + React re-renders even though
 *  nothing actually changed. Preserving referential equality on no-op
 *  merges lets computeEnvelopes' cache short-circuit. */
export function mergeSteps(existing: Step[], incoming: Step[]): Step[] {
  const byId = new Map<string, Step>()
  for (const s of existing) byId.set(s.Id, s)
  let changed = false
  for (const s of incoming) {
    const current = byId.get(s.Id)
    if (!current) { byId.set(s.Id, s); changed = true; continue }
    // A locally-discarded step must never be overwritten by an incoming
    // non-discarded snapshot (e.g. a stale MissingSteps response captured
    // before the backend rewrote the per-turn JSONL). This keeps the
    // Discarded=true + Content=[] clearing authoritative.
    if (current.Discarded && !s.Discarded) { continue }
    if (s.Closed && !current.Closed) { byId.set(s.Id, s); changed = true; continue }
    if (!s.Closed && current.Closed) { continue }
    const sSeq = s.Seq; const cSeq = current.Seq
    if (sSeq != null && cSeq != null && sSeq > cSeq) { byId.set(s.Id, s); changed = true; continue }
    if (sSeq != null && cSeq == null) { byId.set(s.Id, s); changed = true; continue }
    // Both sides lack Seq (legacy / racing paths) — keep existing to avoid
    // clock-skew content loss (AUDIT 4.5).
  }
  if (!changed) return existing
  return sortStepsBySeq(Array.from(byId.values()))
}

/** Authoritatively replace every step belonging to `turnId` with the matching
 *  entry from `serverSteps`. Unlike mergeSteps (which keeps the local version
 *  when both sides are open with equal Seq), this unconditionally takes the
 *  server snapshot. Used by applySummaryToState when the server's
 *  ActiveTurn.EventSeq watermark proves the local active turn is stale
 *  (background→foreground / reconnect), so deltas lost during the disconnect are
 *  recovered from the snapshot instead of relying on incomplete OpenStepEvents
 *  replay. Steps from other turns are preserved; server steps for the turn not
 *  present locally are appended. Returns the input unchanged (referential
 *  equality preserved for the projection cache) when no replacement occurred. */
export function replaceActiveTurnSteps(current: Step[], turnId: string, serverSteps: Step[]): Step[] {
  const server = new Map<string, Step>()
  for (const s of serverSteps) {
    if (s.TurnId === turnId) server.set(s.Id, s)
  }
  if (server.size === 0) return current
  const out: Step[] = []
  let changed = false
  for (const s of current) {
    const replacement = server.get(s.Id)
    if (replacement) {
      out.push(replacement)
      server.delete(s.Id)
      changed = true
    } else {
      out.push(s)
    }
  }
  for (const s of server.values()) {
    out.push(s)
    changed = true
  }
  return changed ? sortStepsBySeq(out) : current
}

/** Sort envelopes by turn sequence, then by intra-turn sequence/index, then
 *  by timestamp. Missing turnSeq/stepSeq is no longer treated as "older" than
 *  known values; live step envelopes that have not yet been assigned a turnSeq
 *  are ordered by their monotonic Seq or timestamp relative to history. */
export function sortEnvelopesBySeq(a: TurnEnvelope, b: TurnEnvelope): number {
  const turnSeqA = a.turnSeq
  const turnSeqB = b.turnSeq
  const hasTurnSeqA = turnSeqA != null && turnSeqA > 0 && !Number.isNaN(turnSeqA)
  const hasTurnSeqB = turnSeqB != null && turnSeqB > 0 && !Number.isNaN(turnSeqB)
  if (hasTurnSeqA && hasTurnSeqB && turnSeqA !== turnSeqB) return turnSeqA - turnSeqB

  const sameTurn = a.metadata?.turnId != null && a.metadata.turnId === b.metadata?.turnId
  if (sameTurn && a.idx != null && b.idx != null && a.idx !== b.idx) return a.idx - b.idx
  const stepSeqA = a.seq
  const stepSeqB = b.seq
  const hasStepSeqA = stepSeqA != null && stepSeqA > 0 && !Number.isNaN(stepSeqA)
  const hasStepSeqB = stepSeqB != null && stepSeqB > 0 && !Number.isNaN(stepSeqB)
  if (hasStepSeqA && hasStepSeqB && stepSeqA !== stepSeqB) return stepSeqA - stepSeqB

  const ta = new Date(a.timestamp).getTime()
  const tb = new Date(b.timestamp).getTime()
  const validA = !Number.isNaN(ta)
  const validB = !Number.isNaN(tb)
  if (validA && validB && ta !== tb) return ta - tb
  if (!validA && validB) return 1
  if (validA && !validB) return -1

  if (a.role !== b.role) return a.role === 'user' ? -1 : 1
  return 0
}

// Frame types that only come from historical / non-step sources.
// When a turn has both step-derived and history-derived frames, these are preserved.
const TURN_ONLY_FRAME_TYPES = new Set(['compaction', 'sources', 'attachments', 'ui', 'plan'])

// ── Projection cache ──

interface ComputeCache {
  steps: Step[]
  historyEnvelopes: TurnEnvelope[]
  localInteractionResponses: ReadonlyMap<string, LocalInteractionResponse>
  activeTurnStates: ReadonlyMap<string, TurnStateEntry>
  turnTasks: ReadonlyMap<string, TaskEntry[]>
  pendingUserMessages: readonly PendingEntry[]
  turnFileChanges: ReadonlyMap<string, FileChangeEntry[]>
  turnContextBudgets: ReadonlyMap<string, ContextBudget>
  currentGoal: SessionGoal | undefined
  result: TurnEnvelope[]
}

/** Mutable ref for per-session projection cache. */
export interface ProjectionCacheRef {
  current: ComputeCache | null
}
const _emptyLocalResponses: ReadonlyMap<string, LocalInteractionResponse> = new Map()
const _emptyTurnStates: ReadonlyMap<string, TurnStateEntry> = new Map()
const _emptyTurnTasks: ReadonlyMap<string, TaskEntry[]> = new Map()
const _emptyPendingUserMessages: readonly PendingEntry[] = []
const _emptyTurnFileChanges: ReadonlyMap<string, FileChangeEntry[]> = new Map()
const _emptyTurnContextBudgets: ReadonlyMap<string, ContextBudget> = new Map()

/** Stale threshold for "pending interaction" turns. If the latest step in a
 *  turn is older than this and the turn is still open with a pending
 *  interaction, we assume the turn lifecycle event was permanently lost and
 *  the pending UI is stale. Returns 'completed' so the UI doesn't get stuck.
 *  5 minutes is conservative — legitimate user approvals rarely take this
 *  long, and a reconnect / reconcile will replace this heuristic with the
 *  authoritative turn state.
 *
 *  Exported for reuse by agent-session.ts reap logic (P2 defensive fix). */
export const STALE_PENDING_TURN_MS = 5 * 60 * 1000

/** Latest step timestamp in ms (0 when none parseable). Exported for the
 * missing-terminal-event reconcile sweep in agent-session.ts. */
export function latestStepMs(steps: Step[]): number {
  let max = 0
  for (const s of steps) {
    if (!s.Timestamp) continue
    const t = Date.parse(s.Timestamp)
    if (Number.isFinite(t) && t > max) max = t
  }
  return max
}

/** Check if a turn's steps have been inactive long enough to be considered
 *  stale. Uses the latest step Timestamp + thresholdMs (default
 *  STALE_PENDING_TURN_MS). Semantically identical to the stale check inside
 *  deriveTurnState. Exported so agent-session.ts can reap open steps for stale
 *  turns without duplicating the threshold or timestamp logic. The thresholdMs
 *  parameter lets the event-layer watchdog reap with a shorter budget when
 *  the SSE silence probe budget is exhausted. */
export function isTurnStale(steps: Step[], nowMs: number, thresholdMs: number = STALE_PENDING_TURN_MS): boolean {
  const latest = latestStepMs(steps)
  return latest > 0 && (nowMs - latest) > thresholdMs
}

/** Derive Turn.State from the steps belonging to a single turn.
 *  State-machine rules (priority order):
 *  - 'paused':  any open step has a pending interaction (waiting for user).
 *               A pending interaction is an explicit user-input gate
 *               (plan_approval / goal_submit / ask_user / ask_permission) — the
 *               user may take arbitrarily long to respond, so it is never
 *               treated as stale regardless of how much time has passed.
 *  - 'failed':  any open step has an Error
 *  - 'completed': all steps are Closed (or no steps)
 *  - 'running': at least one step is open, no interaction pending, no error
 *
 *  Stale fallback (running only, NOT pending): if the latest step in the turn
 *  has been inactive for STALE_PENDING_TURN_MS and there is no pending
 *  interaction, assume the turn terminal event was permanently lost and return
 *  'completed'. Surfaces a neutral done state rather than a misleading error
 *  icon (TurnTail maps 'failed' → error). The next reconnect/reconcile will
 *  overwrite with the authoritative state. */
export function deriveTurnState(steps: Step[], nowMs: number = Date.now()): TurnState {
  if (steps.length === 0) return 'completed'
  const openSteps = steps.filter(s => !s.Closed)
  if (openSteps.length === 0) return 'completed'
  // A pending interaction is a user-input gate that must never be reaped.
  if (openSteps.some(s => s.InteractionStatus === 'pending')) {
    return 'paused'
  }
  const latest = latestStepMs(steps)
  const isStale = latest > 0 && (nowMs - latest) > STALE_PENDING_TURN_MS
  if (isStale) return 'completed'
  if (openSteps.some(s => s.Error != null)) return 'failed'
  return 'running'
}

/** Choose the canonical turn state across the active event cache and the history
 *  envelope, using Revision as the tie-breaker. Active events carry the live
 *  stream revision; history envelopes carry the persisted record revision. When
 *  both sides have revisions the newer one wins. When only one side has a
 *  revision it wins. When neither has a revision (legacy data), active events
 *  take precedence over history, and step-derived heuristics are the final
 *  fallback. */
function chooseCanonicalTurnState(
  entry: TurnStateEntry | undefined,
  hist: TurnEnvelope | undefined,
  stepDerived: TurnState,
): { state: TurnState; source: 'active' | 'history' | 'derived'; revision?: number; startedAt?: string; completedAt?: string; error?: string; turnOrder?: number; retryCount?: number; retryMax?: number; retryError?: string } {
  const entryRev = entry?.revision
  const histMetadata = hist?.metadata
  const histRev = histMetadata?.revision

  if (hist && histMetadata && histRev != null) {
    if (entryRev == null || histRev > entryRev) {
      return {
        state: isValidTurnState(histMetadata.turnState) ? histMetadata.turnState : stepDerived,
        source: 'history',
        revision: histRev,
        startedAt: histMetadata.startedAt,
        completedAt: histMetadata.completedAt,
        error: histMetadata.error,
        turnOrder: hist.turnSeq,
      }
    }
  }

  if (entry) {
    return {
      state: entry.state,
      source: 'active',
      revision: entryRev,
      startedAt: entry.startedAt,
      completedAt: entry.completedAt,
      error: entry.error,
      turnOrder: entry.turnOrder,
      retryCount: entry.retryCount,
      retryMax: entry.retryMax,
      retryError: entry.retryError,
    }
  }

  if (hist && histMetadata && isValidTurnState(histMetadata.turnState)) {
    return {
      state: histMetadata.turnState,
      source: 'history',
      revision: histRev,
      startedAt: histMetadata.startedAt,
      completedAt: histMetadata.completedAt,
      error: histMetadata.error,
      turnOrder: hist.turnSeq,
    }
  }

  return { state: stepDerived, source: 'derived' }
}

// ── Projection ──

/** Derive the live envelope list from steps + historical envelopes.
 *  Pure function: same inputs produce same output. Cache is an internal
 *  performance optimisation keyed by agentActorId.
 *
 *  activeTurnStates carries authoritative turn-level signals from
 *  turn.started / turn.completed / turn.failed / turn.cancelled events.
 *  When an entry exists for a turn, it overrides the step-derived
 *  envelope.completed / metadata.turnState (which flicker between
 *  dispatch iterations). */
// [jitter-diag] temporary instrumentation: throttle per-turn stale-flip logs.
const staleFlipLogAt = new Map<string, number>()

export function computeEnvelopes(
  _agentActorId: string,
  steps: Step[],
  historyEnvelopes: TurnEnvelope[],
  localInteractionResponses?: ReadonlyMap<string, LocalInteractionResponse>,
  activeTurnStates?: ReadonlyMap<string, TurnStateEntry>,
  turnTasks?: ReadonlyMap<string, TaskEntry[]>,
  pendingUserMessages?: readonly PendingEntry[],
  cacheRef?: ProjectionCacheRef,
  stepCache?: Map<string, { sig: string; frames: Frame[] }>,
  turnFileChanges?: ReadonlyMap<string, FileChangeEntry[]>,
  turnContextBudgets?: ReadonlyMap<string, ContextBudget>,
  currentGoal?: SessionGoal,
): TurnEnvelope[] {
  const lr = localInteractionResponses ?? _emptyLocalResponses
  const ats = activeTurnStates ?? _emptyTurnStates
  const tt = turnTasks ?? _emptyTurnTasks
  const pum = pendingUserMessages ?? _emptyPendingUserMessages
  const tfc = turnFileChanges ?? _emptyTurnFileChanges
  const tcb = turnContextBudgets ?? _emptyTurnContextBudgets
  const cache = cacheRef?.current ?? null
  if (
    cache &&
    cache.steps === steps &&
    cache.historyEnvelopes === historyEnvelopes &&
    cache.localInteractionResponses === lr &&
    cache.activeTurnStates === ats &&
    cache.turnTasks === tt &&
    cache.pendingUserMessages === pum &&
    cache.turnFileChanges === tfc &&
    cache.turnContextBudgets === tcb &&
    cache.currentGoal === currentGoal
  ) {
    return cache.result
  }

  // Derive turn-id sets and reserved user IDs directly from the steps array
  // (O(n)) instead of from a full stepsToEnvelopes pass (O(n) with per-block
  // JSON parse + frame construction).
  const stepUserTurnIds = new Set<string>()
  const stepAsstTurnIds = new Set<string>()
  const reservedUserIds = new Set<string>()
  const discardedTurnIds = new Set<string>()
  for (const s of steps) {
    const tid = s.TurnId || s.Id
    if (s.Discarded) {
      discardedTurnIds.add(tid)
      continue
    }
    if (s.Role === 'user') {
      stepUserTurnIds.add(tid)
      reservedUserIds.add(s.Id)
    } else {
      stepAsstTurnIds.add(tid)
    }
  }
  for (const env of historyEnvelopes) {
    if (env.role === 'user') reservedUserIds.add(env.id)
  }

  // History envelopes for turns that have no step-derived envelope of the same
  // role, OR that have been fully discarded (their steps carry Discarded=true).
  // Without the discardedTurnIds check, a discarded turn's stale history
  // envelope would reappear because the turn id is absent from the
  // step-derived sets above.
  let filteredHistory = historyEnvelopes.filter(e => {
    const turnId = e.metadata?.turnId
    if (!turnId) return true
    if (discardedTurnIds.has(turnId)) return false
    if (e.role === 'user') return !stepUserTurnIds.has(turnId)
    if (e.role === 'assistant') return !stepAsstTurnIds.has(turnId)
    return true
  })

  const dedupedStepEnvelopes = stepsToEnvelopes(steps, lr, reservedUserIds, stepCache)

  // Preserve turn-only frames from history onto active step envelopes.
  const historyByTurnId = new Map<string, TurnEnvelope>()
  for (const env of historyEnvelopes) {
    if (env.metadata?.turnId) {
      historyByTurnId.set(env.metadata.turnId, env)
    }
  }

  // Pre-compute turn state per turn from steps.
  const stepsByTurnId = new Map<string, Step[]>()
  for (const s of steps) {
    if (s.Discarded) continue
    if (!s.TurnId) continue
    const list = stepsByTurnId.get(s.TurnId)
    if (list) list.push(s)
    else stepsByTurnId.set(s.TurnId, [s])
  }

  let enrichedStepEnvelopes = dedupedStepEnvelopes.map(env => {
    const turnId = env.metadata?.turnId
    if (!turnId) return env
    const hist = historyByTurnId.get(turnId)
    const entry = ats.get(turnId)
    const turnSteps = stepsByTurnId.get(turnId) ?? []
    const stepDerivedTurnState = deriveTurnState(turnSteps, Date.now())
    // [jitter-diag] temporary instrumentation: a stale-flip derives
    // 'completed' while steps are still open — the layout jump suspect.
    if (stepDerivedTurnState === 'completed' && turnSteps.some(s => !s.Closed)) {
      const now = Date.now()
      const last = staleFlipLogAt.get(turnId) ?? 0
      if (now - last > 5000) {
        staleFlipLogAt.set(turnId, now)
        console.info(`[jitter-diag] stale-turn-flip turnId=${turnId} idleMs=${now - latestStepMs(turnSteps)}`)
      }
    }
    // Canonical turn-level state is selected by Revision across the live event
    // cache and the persisted history record. Step-derived heuristics are only
    // a fallback when no canonical state/revision is available.
    const canonical = chooseCanonicalTurnState(entry, hist, stepDerivedTurnState)
    const turnState = canonical.state
    const turnStartedAt = canonical.startedAt
    const turnCompletedAt = canonical.completedAt
    const turnError = canonical.error
    const turnOrder = canonical.turnOrder
    const retryCount = canonical.retryCount
    const retryMax = canonical.retryMax
    const retryError = canonical.retryError
    // Merge metadata from history (actionCount, usage, model, contextBudget, etc.)
    // into the step-derived envelope so TurnTail shows correct stats.
    const stepMeta = { ...env.metadata, turnState, startedAt: turnStartedAt, completedAt: turnCompletedAt, error: turnError, retryCount, retryMax, retryError }
    // A history envelope can carry the turn-start placeholder (0 tokens),
    // which must not overwrite a later real value received in live
    // turn.context_budget events when the turn transitions to completed.
    const historyBudget = hist?.metadata?.contextBudget
    const liveBudget = tcb.get(turnId)
    const contextBudget = historyBudget && historyBudget.estimatedTokens > 0
      ? historyBudget
      : liveBudget ?? historyBudget
    const mergedMetadata = hist
      ? { ...hist.metadata, ...stepMeta, contextBudget }
      : { ...stepMeta, contextBudget }
    const preserved = hist ? hist.frames.filter(f => TURN_ONLY_FRAME_TYPES.has(f.type)) : []
    // Completion is driven by the canonical turn state when we have an
    // authoritative active or history source. A terminal canonical state
    // (completed/failed/cancelled/abandoned) always means the turn is complete;
    // 'paused' and 'running' are live. When we fall back to step-derived
    // heuristics (no canonical source), preserve the original completed flag
    // precedence: an explicit completed=false from history keeps the turn live,
    // otherwise a completed=true from either source marks it terminal. This
    // covers both active-turn placeholders (history completed=false) and legacy
    // completed turns that contain unclosed steps (history completed=true).
    const completed = canonical.source !== 'derived'
      ? isTerminalTurnState(turnState)
      : (hist?.completed === false
          ? false
          : (env.completed || (hist?.completed ?? false)))
    const base: TurnEnvelope = {
      ...env,
      metadata: mergedMetadata,
      idx: env.idx ?? hist?.idx,
      seq: env.seq,
      turnSeq: env.turnSeq ?? hist?.turnSeq ?? turnOrder,
      clientKey: env.clientKey || hist?.clientKey,
      completed,
    }
    // If the step-derived envelope has no renderable frames, fall back to the
    // history envelope's frames so reconnect doesn't wipe an active turn's content.
    if (env.frames.length === 0 && hist && hist.frames.length > 0) {
      return { ...base, frames: hist.frames, fileChanges: tfc.get(turnId) ?? hist.fileChanges }
    }
    const mergedFrames = preserved.length === 0 ? env.frames : [...env.frames, ...preserved]
    // Precedence: tfc (live step.file_changes events) wins over hist (snapshot).
    // Step events have higher EventSeq than the snapshot time, so they're strictly
    // newer. On reconnect, summary fills hist, then step events replay and merge
    // into tfc by-path (idempotent).
    const mergedFileChanges = tfc.get(turnId) ?? hist?.fileChanges
    if (mergedFileChanges) {
      return { ...base, frames: mergedFrames, fileChanges: mergedFileChanges }
    }
    if (preserved.length === 0) return base
    return { ...base, frames: mergedFrames }
  })

  // plan_submit / goal_submit / goal_card_submit stamp content already carried
  // by their interaction cards; the raw tool calls should never render as
  // frames. Also strip any plan frame lacking a requestId — those can slip in
  // from older history envelopes before the approval event arrived. Match both
  // "plan_submit" (tool name and callable ID) form. Finally, dedupe plan frames by
  // requestId so a step-derived plan + a preserved history plan for the same
  // approval don't both render (AUDIT 5.2).
  const stripSubmitToolFrames = (frames: Frame[]): Frame[] => {
    const seenPlanRequestIds = new Set<string>()
    return frames.filter(f => {
      if (f.type === 'tool' && /^(plan|goal)[._][a-z_]*submit$/.test((f as ToolFrame).toolName ?? '')) return false
      if (f.type === 'plan') {
        const pf = f as PlanFrame
        if (!pf.requestId) return false
        if (seenPlanRequestIds.has(pf.requestId)) return false
        seenPlanRequestIds.add(pf.requestId)
        return true
      }
      return true
    })
  }
  enrichedStepEnvelopes = enrichedStepEnvelopes.map(env => {
    if (env.role !== 'assistant') return env
    const frames = stripSubmitToolFrames(env.frames)
    return { ...env, frames }
  })
  filteredHistory = filteredHistory.map(env => {
    if (env.role !== 'assistant') return env
    const frames = stripSubmitToolFrames(env.frames)
    return { ...env, frames }
  })

  // Overlay authoritative live turn state onto history-only assistant
  // envelopes (no step-derived twin). Needed for crash-recovery: the old
  // paused turn often lives only in history until summary refreshes, and
  // turn.resumed with state='running' re-activates the existing envelope.
  // When the history envelope carries a newer canonical revision, it wins.
  filteredHistory = filteredHistory.map(env => {
    if (env.role !== 'assistant') return env
    const turnId = env.metadata?.turnId
    if (!turnId) return env
    const entry = ats.get(turnId)
    if (!entry) return env
    const histRevision = env.metadata?.revision
    const entryRevision = entry.revision
    const historyWins = histRevision != null && (entryRevision == null || histRevision > entryRevision)
    if (historyWins) {
      return {
        ...env,
        completed: isTerminalTurnState(env.metadata?.turnState),
      }
    }
    const historyBudget = env.metadata?.contextBudget
    const liveBudget = tcb.get(turnId)
    const contextBudget = historyBudget && historyBudget.estimatedTokens > 0
      ? historyBudget
      : liveBudget ?? historyBudget
    return {
      ...env,
      completed: isTerminalTurnState(entry.state),
      turnSeq: env.turnSeq ?? entry.turnOrder,
      metadata: {
        ...env.metadata,
        turnState: entry.state,
        startedAt: entry.startedAt ?? env.metadata?.startedAt,
        completedAt: entry.completedAt ?? env.metadata?.completedAt,
        error: entry.error ?? env.metadata?.error,
        retryCount: entry.retryCount,
        retryMax: entry.retryMax,
        retryError: entry.retryError,
        contextBudget,
      },
    }
  })

  // Create a placeholder for a live turn whose step and history events have
  // not arrived yet. The submit response gives the frontend the turnId before
  // the backend emits its first turn event, so the running state alone is not
  // enough to make the assistant envelope render.
  const representedTurnIds = new Set<string>()
  for (const env of [...filteredHistory, ...enrichedStepEnvelopes]) {
    const turnId = env.metadata?.turnId
    if (turnId) representedTurnIds.add(turnId)
  }
  for (const [turnId, entry] of ats) {
    if (representedTurnIds.has(turnId)) continue
    const live = entry.state === 'running' || entry.state === 'paused'
    enrichedStepEnvelopes.push({
      id: turnId,
      role: 'assistant',
      frames: [],
      timestamp: entry.startedAt ?? entry.completedAt ?? new Date().toISOString(),
      completed: !live,
      turnSeq: entry.turnOrder,
      metadata: {
        turnId,
        turnState: entry.state,
        revision: entry.revision,
        startedAt: entry.startedAt,
        completedAt: entry.completedAt,
        error: entry.error,
      },
    })
  }

  // Merge real-time task snapshots into assistant envelopes. History envelopes
  // that already carry tasks keep them (committed turn snapshot); step-derived
  // envelopes for running turns get tasks from the state-layer materialized view.
  const injectTurnTasks = (env: TurnEnvelope): TurnEnvelope => {
    if (env.role !== 'assistant') return env
    if (env.tasks && env.tasks.length > 0) return env
    const turnId = env.metadata?.turnId
    if (!turnId) return env
    const tasks = tt.get(turnId)
    if (!tasks || tasks.length === 0) return env
    return { ...env, tasks }
  }
  enrichedStepEnvelopes = enrichedStepEnvelopes.map(injectTurnTasks)
  filteredHistory = filteredHistory.map(injectTurnTasks)

  // Infer turnSeq for user envelopes that lack one. When a user turn arrives
  // via live step events (e.g. pending-submit promotion in handleTurnComplete),
  // its envelope has no turnSeq: userStepToEnvelope doesn't set it, no history
  // envelope exists yet, and activeTurnStates has no entry for user turns.
  // Without a turnSeq, a user envelope could drift away from the assistant turn
  // it belongs to when sorting by timestamp/Seq alone. Infer it from the next
  // assistant turn's turnSeq by comparing step Seq values (monotonically
  // allocated across all turns) so the user bubble stays immediately before its
  // assistant turn.
  const asstTurnSeqByMinSeq: Array<{ minSeq: number; turnSeq: number }> = []
  {
    const seen = new Set<number>()
    for (const env of enrichedStepEnvelopes) {
      if (env.role !== 'assistant' || env.turnSeq == null || env.turnSeq <= 0) continue
      if (env.seq == null || env.seq <= 0) continue
      if (seen.has(env.turnSeq)) continue
      seen.add(env.turnSeq)
      asstTurnSeqByMinSeq.push({ minSeq: env.seq, turnSeq: env.turnSeq })
    }
    asstTurnSeqByMinSeq.sort((a, b) => a.minSeq - b.minSeq)
  }
  if (asstTurnSeqByMinSeq.length > 0) {
    enrichedStepEnvelopes = enrichedStepEnvelopes.map(env => {
      if (env.role !== 'user' || env.turnSeq != null) return env
      if (env.seq == null || env.seq <= 0) return env
      const next = asstTurnSeqByMinSeq.find(a => a.minSeq > env.seq!)
      if (next) return { ...env, turnSeq: next.turnSeq - 0.5 }
      const last = asstTurnSeqByMinSeq[asstTurnSeqByMinSeq.length - 1]!
      return { ...env, turnSeq: last.turnSeq + 0.5 }
    })
  }

  const combined = [...filteredHistory, ...enrichedStepEnvelopes]
  combined.sort(sortEnvelopesBySeq)

  let result = dedupeEnvelopes(combined).filter(env => env.id !== '__pending_queue__')

  // Tasks and fileChanges belong to a turn, not to each envelope of that turn.
  // A single turn can produce multiple assistant envelopes (planning half +
  // execution half split by the user's approval message). Without this guard
  // the same list is rendered in every TurnTail of that turn → visible
  // duplication. Keep them only on the chronologically last assistant envelope
  // of each turn.
  const lastAsstIdxByTurn = new Map<string, number>()
  result.forEach((env, idx) => {
    if (env.role !== 'assistant') return
    const tid = env.metadata?.turnId
    if (!tid) return
    lastAsstIdxByTurn.set(tid, idx)
  })
  result = result.map((env, idx) => {
    if (env.role !== 'assistant') return env
    const tid = env.metadata?.turnId
    if (!tid) return env
    const isLast = lastAsstIdxByTurn.get(tid) === idx
    const hasTasks = env.tasks && env.tasks.length > 0
    const hasFileChanges = env.fileChanges && env.fileChanges.length > 0
    if ((hasTasks || hasFileChanges) && !isLast) {
      const { tasks: _dropTasks, fileChanges: _dropFileChanges, ...rest } = env
      return rest
    }
    return env
  })

  // Inject the session-level active goal onto the last assistant envelope of
  // the currently active turn (running or paused). Goal is session-level data,
  // but it should render inside the active turn's TurnTail, not on every turn.
  // Goal-continuation system bubbles also get the goal so they can show its name.
  if (currentGoal) {
    let activeTurnId: string | undefined
    for (const [turnId, entry] of ats) {
      if (entry.state === 'running' || entry.state === 'paused') {
        activeTurnId = turnId
        break
      }
    }
    const activeGoalIdx = activeTurnId != null ? lastAsstIdxByTurn.get(activeTurnId) : undefined
    console.debug('[projection] injectGoal currentGoal=', currentGoal, 'activeTurnId=', activeTurnId, 'activeGoalIdx=', activeGoalIdx, 'lastAsstIdxByTurn=', Object.fromEntries(lastAsstIdxByTurn))
    result = result.map((env, idx) => {
      if (env.systemOrigin === 'goal') return { ...env, goal: currentGoal }
      if (activeGoalIdx != null && idx === activeGoalIdx) return { ...env, goal: currentGoal }
      return env
    })
  }

  // Inject pending user messages as pendingSubmits on the currently running
  // assistant envelope so TurnTail can render optimistic "queued message" UI.
  if (pum.length > 0) {
    const pendingOnly = pum.filter(e => e.state === 'pending')
    const pendingSubmits: PendingSubmitEntry[] = pendingOnly.map(entry => ({
      id: entry.clientId,
      text: entry.text,
      timestamp: entry.timestamp,
    }))
    let runningAsstIdx = -1
    for (let i = result.length - 1; i >= 0; i--) {
      const env = result[i]
      if (env?.role === 'assistant' && env.completed === false) {
        runningAsstIdx = i
        break
      }
    }
    if (runningAsstIdx >= 0) {
      result = result.map((env, idx) => {
        if (idx !== runningAsstIdx) return env
        return {
          ...env,
          metadata: {
            ...env.metadata,
            pendingSubmits,
          },
        }
      })
    } else if (pendingSubmits.length > 0) {
      // AUDIT 1.9: no running assistant envelope — we're in the gap between
      // the user's send and the next turn starting. Append a synthetic
      // "queued" placeholder so TurnTail still renders the optimistic UI
      // instead of leaving the user with no feedback during the window.
      result = [...result, {
        id: '__pending_queue__',
        role: 'assistant',
        frames: [],
        timestamp: pendingSubmits[0]!.timestamp,
        completed: false,
        metadata: { pendingSubmits },
      }]
    }
  }

  // Ensure every envelope has a boolean completed flag. History-only envelopes
  // that never went through step enrichment may carry undefined; derive from
  // turnState when possible so paused/failed/completed render consistently.
  result = result.map(env => {
    if (env.completed != null) return env
    const ts = env.metadata?.turnState
    const terminal = isTerminalTurnState(ts)
    return { ...env, completed: terminal }
  })

  const newCache: ComputeCache = { steps, historyEnvelopes, localInteractionResponses: lr, activeTurnStates: ats, turnTasks: tt, pendingUserMessages: pum, turnFileChanges: tfc, turnContextBudgets: tcb, currentGoal, result }
  if (cacheRef) {
    cacheRef.current = newCache
  }
  return result
}
