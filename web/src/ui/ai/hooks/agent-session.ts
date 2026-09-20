import type { TurnEnvelope, LocalInteractionResponse, TaskEntry, Frame, FileChangeEntry, ContextBudget, SessionGoal, TurnMetadata } from '../model/frame-types'
import type { Step, StepEvent, TurnEvent, ModelUnit } from '../../../gen-types/aigen'
import type { TimelineState, LocalEvent } from './timeline-manager'
import { stepReducerBatch } from './step-reducer'
import { computeEnvelopes, isTurnStale, latestStepMs, mergeSteps, type ProjectionCacheRef, compareOptionalSeq, isTerminalTurnState, isValidTurnState, type TurnState } from './projection'
import { hasDiscardedSteps } from './steps-to-envelopes'
import { PendingMessageQueue, type PendingEntry } from './pending-message-queue'
import { iconFromPath } from './tool-parsers'
import { truncateHistoryEnvelopes } from '../model/payload-truncate'

/** Find the Step whose canonical RequestId matches the given value. */
function findStepByRequestId(steps: Step[], requestId: string): Step | undefined {
  const direct = steps.find(s => s.Id === requestId || s.RequestId === requestId)
  if (direct) return direct
  return steps.find(s =>
    s.Content?.some(b => b.Type === 'tool_use' && b.ToolUseId === requestId),
  )
}

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
  /** Truncated dispatch failure text from turn.dispatch_retry (why the
   *  engine is backing off, e.g. provider 429 quota message). */
  retryError?: string
}

const _emptyTurnStates: ReadonlyMap<string, TurnStateEntry> = new Map()
const _emptyTurnTasks: ReadonlyMap<string, TaskEntry[]> = new Map()
const _emptyTurnFileChanges: ReadonlyMap<string, FileChangeEntry[]> = new Map()
const _emptyTurnContextBudgets: ReadonlyMap<string, ContextBudget> = new Map()

/** Extract the canonical monotonic revision from a TurnEvent payload.
 *  Returns undefined when the payload carries no revision (legacy events). */
function payloadRevision(payload: Record<string, unknown> | undefined): number | undefined {
  if (!payload) return undefined
  const rev = payload.revision
  if (typeof rev === 'number' && !Number.isNaN(rev)) return rev
  return undefined
}

/** Quiet window after a turn's last step activity before the
 * missing-terminal-event sweep may request a reconcile. Must exceed the
 * normal ordering gap (terminal turn event arrives milliseconds after the
 * last step.closed) so healthy flows never fetch. */
const MISSING_TERMINAL_RECONCILE_DELAY_MS = 15_000

/** Returns true when an incoming lifecycle event revision is usable:
 *  apply if the event revision is strictly greater than the current active
 *  revision. Equal revisions are treated as idempotent duplicates and ignored;
 *  older revisions are discarded as stale. When either side lacks a revision
 *  (legacy), the event is always accepted. */
function shouldApplyLifecycleEvent(eventRevision: number | undefined, currentRevision: number | undefined): boolean {
  if (eventRevision == null || currentRevision == null) return true
  return eventRevision > currentRevision
}

function taskFromPayload(task: Record<string, unknown> | undefined): TaskEntry | undefined {
  if (!task) return undefined
  const id = typeof task.id === 'string' ? task.id : ''
  if (!id) return undefined
  const activeForm = typeof task.activeForm === 'string' && task.activeForm !== '' ? task.activeForm : undefined
  return {
    id,
    subject: typeof task.subject === 'string' ? task.subject : '',
    status: (task.status === 'in_progress' || task.status === 'completed' ? task.status : 'pending') as TaskEntry['status'],
    activeForm,
  }
}

// ── AgentSession ──

/** Single-owner per-agent state + caches. Replaces the module-level Maps
 *  (stepCache in steps-to-envelopes, lastEventSeqByStep in step-reducer,
 *  cacheByAgent in projection) with instance fields that are destroyed on
 *  release. */
export class AgentSession {
  readonly agentActorId: string

  // ── State fields (from AgentStateLayer) ──
  private _steps: Step[] = []
  private _historyEnvelopes: TurnEnvelope[] = []
  private _loading: boolean = false
  private _loadingMore: boolean = false
  private _error: string | null = null
  private _hasMoreHistory: boolean = false
  private _summarizedBoundary: boolean = false
  private _discardedBoundary: boolean = false
  private _agentState: string | undefined
  private _localInteractionResponses = new Map<string, LocalInteractionResponse>()
  private _activeTurnStates = new Map<string, TurnStateEntry>()
  /** TurnIds for which the missing-terminal-event reconcile sweep already
   *  requested a summary fetch (one-shot per turnId per session). */
  private _canonicalReconcileRequested = new Set<string>()
  private _pendingMessages = new PendingMessageQueue()
  private _turnTasks = new Map<string, TaskEntry[]>()
  private _turnFileChanges = new Map<string, FileChangeEntry[]>()
  private _turnContextBudgets = new Map<string, ContextBudget>()
  private _currentGoal: SessionGoal | undefined
  private _pendingPause: boolean = false
  /** Set by importHistory (initFromLoad). While true, the next server
   *  summary reconcile (applyInitSummary) preserves imported envelopes the
   *  backend doesn't know about as a visible prefix instead of wholesale
   *  replacing them — otherwise imported history vanishes on the first
   *  message. One-shot: cleared after the first reconcile. */
  private _importedSnapshot: boolean = false
  /** The model unit the aggregator actually resolved for the current dispatch
   *  (β feedback from turn.unit_changed). Undefined until the first dispatch
   *  of a turn reports it. */
  private _currentUnit: ModelUnit | undefined
  private _listeners = new Set<() => void>()
  private _cachedSnapshot: TimelineState | null = null
  /** Local events that arrived before their target step. AUDIT 1.6: previously
   *  dropped silently when findStepByRequestId missed; we now buffer and
   *  replay on applyStepEvents so fast UI interactions / reconnect doesn't
   *  lose the user's response. */
  private _pendingLocalEvents: LocalEvent[] = []

  // ── Per-agent caches (moved from module-level) ──
  /** Per-step frame cache. Formerly module-level `stepCache` in steps-to-envelopes.ts. */
  readonly stepCache = new Map<string, { sig: string; frames: Frame[] }>()
  /** Per-step EventSeq dedup. Formerly module-level `lastEventSeqByStep` in step-reducer.ts. */
  readonly seqTracking = new Map<string, number>()
  private readonly pendingStepEvents = new Map<string, StepEvent[]>()
  /** Projection compute cache. Formerly module-level `cacheByAgent` in projection.ts. */
  private _projectionCache: ProjectionCacheRef = { current: null }

  // ── Collaborators (set after construction) ──

  /** Summary reconciler for single-flight summary fetches. Set by
   *  timeline-manager after construction. */
  reconciler: import('./summary-reconciler').SummaryReconciler | null = null

  constructor(agentActorId: string) {
    this.agentActorId = agentActorId
  }

  // ── Read accessors ──

  get steps(): Step[] { return this._steps }

  get historyEnvelopes(): TurnEnvelope[] { return this._historyEnvelopes }

  get loading(): boolean { return this._loading }

  get loadingMore(): boolean { return this._loadingMore }

  get error(): string | null { return this._error }

  get hasMoreHistory(): boolean { return this._hasMoreHistory }

  get summarizedBoundary(): boolean { return this._summarizedBoundary }

  get discardedBoundary(): boolean { return this._discardedBoundary }

  get isStreaming(): boolean {
    // Authoritative source: live turn lifecycle events (turn.started /
    // turn.completed / turn.failed / turn.cancelled / turn.waiting). Step
    // Closed flags are no longer used for streaming state. Waiting is a
    // terminal state, so it must not count as streaming.
    for (const entry of this._activeTurnStates.values()) {
      if (entry.state === 'running') return true
    }
    if (this._activeTurnStates.size > 0) {
      // We have live turn state and none are running → not streaming.
      return false
    }
    // No live turn state yet (e.g. after summary load / reconnect). Fall back
    // to the history envelope metadata, which is the backend snapshot.
    return this._historyEnvelopes.some(e =>
      e.role === 'assistant' &&
      e.metadata?.turnState === 'running',
    )
  }

  get isPaused(): boolean {
    // User pause: authoritative turn.paused SSE event from the backend.
    // Running takes priority over paused — crash-recovery resume starts a
    // new running turn while the old paused envelope lingers in history.
    if (this.isStreaming) return false
    for (const entry of this._activeTurnStates.values()) {
      if (entry.state === 'paused') return true
    }
    if (this._agentState === 'paused') return true
    if (this._activeTurnStates.size > 0) return false
    // _agentState set but not paused is authoritative (post-restart recovery):
    // don't fall through to a stale history envelope.
    if (this._agentState !== undefined) return false
    // No live state at all (fresh load before any status arrived). Fall back
    // to the LAST assistant envelope — scanning all history would let one
    // stale paused envelope pin the composer paused forever.
    return this.lastAssistantTurnState() === 'paused'
  }

  get isWaiting(): boolean {
    // Workflow-level waiting: authoritative turn.waiting SSE event from the
    // backend. Running takes priority; terminal waiting follows the same
    // precedence rules as paused.
    if (this.isStreaming) return false
    for (const entry of this._activeTurnStates.values()) {
      if (entry.state === 'waiting') return true
    }
    if (this._agentState === 'waiting') return true
    if (this._activeTurnStates.size > 0) return false
    // _agentState set but not waiting is authoritative: after restart the
    // backend recovers a waiting turn to paused/recovery, and the stale
    // waiting envelope must not keep isWaiting true (it would hide the
    // resume button behind the pause-all button).
    if (this._agentState !== undefined) return false
    return this.lastAssistantTurnState() === 'waiting'
  }

  /** Turn state of the most recent assistant envelope, for the no-live-state
   *  fallback in isPaused/isWaiting. */
  private lastAssistantTurnState(): TurnMetadata['turnState'] | undefined {
    for (let i = this._historyEnvelopes.length - 1; i >= 0; i--) {
      const e = this._historyEnvelopes[i]
      if (e?.role === 'assistant') return e.metadata?.turnState
    }
    return undefined
  }

  get isPausing(): boolean {
    return this._pendingPause
  }

  getActiveTurnStates(): ReadonlyMap<string, TurnStateEntry> {
    return this._activeTurnStates.size > 0 ? this._activeTurnStates : _emptyTurnStates
  }

  getTurnTasks(): ReadonlyMap<string, TaskEntry[]> {
    return this._turnTasks.size > 0 ? this._turnTasks : _emptyTurnTasks
  }

  getTurnFileChanges(): ReadonlyMap<string, FileChangeEntry[]> {
    return this._turnFileChanges.size > 0 ? this._turnFileChanges : _emptyTurnFileChanges
  }

  getTurnContextBudgets(): ReadonlyMap<string, ContextBudget> {
    return this._turnContextBudgets.size > 0 ? this._turnContextBudgets : _emptyTurnContextBudgets
  }

  get currentGoal(): SessionGoal | undefined {
    return this._currentGoal
  }

  /** Resolved executing model (β). Undefined when no dispatch has reported it. */
  get currentUnit(): ModelUnit | undefined {
    return this._currentUnit
  }

  /** Seed the β channel from the agent's persisted executing unit
   *  (agent_status.CurrentUnit) so the composer shows the in-use unit before
   *  the first live turn.unit_changed event (page reload, agent switch, or
   *  same-model dispatches that emit no event). Only fills when unset — live
   *  events are newer and always win. */
  seedCurrentUnit(unit: ModelUnit): void {
    if (this._currentUnit || !unit.model) return
    this._currentUnit = { model: unit.model, provider: unit.provider ?? '' }
    this._notify()
  }

  setGoal(goal: SessionGoal | undefined): void {
    this._currentGoal = goal
  }

  getPendingUserMessages(): readonly PendingEntry[] {
    return this._pendingMessages.snapshot()
  }

  getLocalInteractionResponses(): ReadonlyMap<string, LocalInteractionResponse> {
    return this._localInteractionResponses
  }

  // ── Snapshot ──

  getSnapshot(): TimelineState {
    if (this._cachedSnapshot) return this._cachedSnapshot
    const envelopes = computeEnvelopes(
      this.agentActorId,
      this._steps,
      this._historyEnvelopes,
      this._localInteractionResponses,
      this.getActiveTurnStates(),
      this.getTurnTasks(),
      this.getPendingUserMessages(),
      this._projectionCache,
      this.stepCache,
      this.getTurnFileChanges(),
      this.getTurnContextBudgets(),
      this._currentGoal,
    )
    // Derive isStreaming/isPaused/isWaiting from the computed envelopes (the
    // same envelopes the UI renders) so the Composer buttons always match what
    // TurnTail displays.
    const computedStreaming = envelopes.some(e =>
      e.role === 'assistant' && e.metadata?.turnState === 'running',
    )
    // A turn is only paused if nothing is running — crash-recovery resume
    // starts a new turn (running) while the old paused envelope lingers in
    // history until reconcile prunes it. Running takes priority.
    // A pending interaction (goal_submit / plan_approval / ask_user / etc.)
    // is NOT a user-paused turn: the interaction card provides the
    // approve/reject UI, so the Composer must not offer a "Resume" button
    // that would bypass the confirmation.
    const hasPendingInteraction = this._steps.some(
      s => s.InteractionStatus === 'pending' && !s.Closed,
    )
    const computedPaused = !computedStreaming && !hasPendingInteraction && envelopes.some(e =>
      e.role === 'assistant' && e.metadata?.turnState === 'paused',
    )
    const computedWaiting = !computedStreaming && !hasPendingInteraction && envelopes.some(e =>
      e.role === 'assistant' && e.metadata?.turnState === 'waiting',
    )
    // AUDIT 5.6: freeze the snapshot so external consumers can't mutate the
    // cached envelopes array and pollute subsequent reads. The cost is
    // negligible (one shallow freeze) and the cache stays referentially
    // stable as long as inputs don't change.
    this._cachedSnapshot = Object.freeze({
      envelopes: Object.freeze(envelopes) as TurnEnvelope[],
      isStreaming: computedStreaming,
      isPaused: computedPaused,
      isWaiting: computedWaiting,
      isPausing: this._pendingPause,
      loading: this._loading,
      loadingMore: this._loadingMore,
      error: this._error,
      hasMoreHistory: this._hasMoreHistory,
      summarizedBoundary: this._summarizedBoundary,
      discardedBoundary: this._discardedBoundary || hasDiscardedSteps(this._steps),
      currentUnit: this._currentUnit,
    }) as TimelineState
    return this._cachedSnapshot
  }

  // ── Step / Turn event application ──

  applyStepEvents(events: StepEvent[]): void {
    const known = new Set(this._steps.map(step => step.Id))
    const ready: StepEvent[] = []
    for (const ev of events) {
      // step.opened and step.interaction_requested are both step-creation
      // events: they introduce a new StepId that doesn't exist yet. Both must
      // bypass the known-set filter so they reach the step reducer.
      // (Previously, step.interaction_requested was buffered in
      // pendingStepEvents — a dead-end with no subsequent step.opened to
      // flush it — so plan approval cards never rendered in real-time.)
      const isStepCreation = ev.Kind === 'step.opened' || ev.Kind === 'step.interaction_requested'
      if (isStepCreation || known.has(ev.StepId)) {
        ready.push(ev)
        if (isStepCreation) {
          known.add(ev.StepId)
          const pending = this.pendingStepEvents.get(ev.StepId)
          if (pending) {
            ready.push(...pending)
            this.pendingStepEvents.delete(ev.StepId)
          }
        }
      } else {
        const pending = this.pendingStepEvents.get(ev.StepId) ?? []
        pending.push(ev)
        this.pendingStepEvents.set(ev.StepId, pending)
      }
    }
    this._steps = stepReducerBatch(this._steps, ready, this.seqTracking)
    // NOTE: We deliberately do NOT delete _localInteractionResponses on
    //  step.closed. The renderer needs the user's allowed/decision value to
    //  render permission/plan_approval cards after the step has closed;
    //  deleting on close caused the card to flicker back to "pending"
    //  (AUDIT 4.3). The map is cleared on reset/release/applyInitSummary.
    for (const ev of events) {
      // Primary confirmation path: use OriginMessageId from step.opened events.
      if (ev.OriginMessageId) {
        this._pendingMessages.confirmByMessageId(ev.OriginMessageId)
      }
    }
    // Fallback: text-based confirmation for summary API steps (no OriginMessageId).
    this._confirmPendingUserMessagesFromSteps()
    this._applyTaskEvents(events)
    this._applyFileChangeEvents(events)
    this._replayPendingLocalEvents()
    this._notify()
  }

  private _confirmPendingUserMessagesFromSteps(): void {
    if (this._pendingMessages.size === 0) return
    for (const s of this._steps) {
      if (s.Id && this._pendingMessages.confirmByMessageId(s.Id)) continue
      const isUserStep = s.Role === 'user' && s.Type === 'text'
      if (s.Type !== 'user_inject' && !isUserStep) continue
      const text = s.Content.map(b => b.Text ?? '').join('')
      if (!text) continue
      this._pendingMessages.confirmByExactText(text)
    }
  }

  private _applyTaskEvents(events: StepEvent[]): void {
    let modified = false
    for (const ev of events) {
      if (ev.Kind !== 'step.task_created' && ev.Kind !== 'step.task_updated' && ev.Kind !== 'step.task_deleted') {
        continue
      }
      const turnId = ev.TurnId || ''
      if (!turnId) continue
      const task = taskFromPayload(ev.Task)
      if (!task) continue

      const current = this._turnTasks.get(turnId) ?? []
      if (ev.Kind === 'step.task_deleted') {
        const filtered = current.filter(t => t.id !== task.id)
        if (filtered.length !== current.length) {
          this._turnTasks.set(turnId, filtered)
          modified = true
        }
        continue
      }

      const idx = current.findIndex(t => t.id === task.id)
      let next: TaskEntry[]
      if (idx >= 0) {
        const existing = current[idx]!
        next = [...current]
        next[idx] = { ...existing, ...task, activeForm: task.activeForm || existing.activeForm }
      } else {
        next = [...current, task]
      }
      this._turnTasks.set(turnId, next)
      modified = true
    }
    if (modified) {
      this._turnTasks = new Map(this._turnTasks)
    }
  }

  /** 累积 step.file_changes 事件到 _turnFileChanges。按 (turnId, path) merge，
   *  同 path 后写覆盖前写 —— 与后端 appendFileChanges 语义一致。重连重放
   *  时按 path 幂等 merge。 */
  private _applyFileChangeEvents(events: StepEvent[]): void {
    let modified = false
    for (const ev of events) {
      if (ev.Kind !== 'step.file_changes') continue
      const turnId = ev.TurnId || ''
      if (!turnId) continue
      const incoming = ev.FileChanges
      if (!incoming || incoming.length === 0) continue

      const current = this._turnFileChanges.get(turnId) ?? []
      const byPath = new Map<string, FileChangeEntry>()
      for (const fc of current) {
        byPath.set(fc.filepath ?? fc.filename, fc)
      }
      for (const fc of incoming) {
        const path = String(fc.Path ?? '')
        if (!path) continue
        byPath.set(path, {
          filename: path.split('/').pop() ?? 'file',
          filepath: path,
          icon: iconFromPath(path),
          additions: Number(fc.Additions ?? 0),
          deletions: Number(fc.Deletions ?? 0),
          diffContent: String(fc.DiffContent ?? ''),
        })
      }
      this._turnFileChanges.set(turnId, Array.from(byPath.values()))
      modified = true
    }
    if (modified) {
      this._turnFileChanges = new Map(this._turnFileChanges)
    }
  }

  /** Apply a canonical lifecycle event to _activeTurnStates, enforcing monotonic
   *  revision. Old revisions are discarded, equal revisions are idempotent.
   *  Returns true if the state was actually updated. */
  private _applyCanonicalLifecycle(
    turnId: string,
    state: TurnState,
    eventRevision: number | undefined,
    build: (prev: TurnStateEntry | undefined) => Partial<TurnStateEntry>,
  ): boolean {
    const prev = this._activeTurnStates.get(turnId)
    if (!shouldApplyLifecycleEvent(eventRevision, prev?.revision)) return false
    const next = new Map(this._activeTurnStates)
    const built = build(prev)
    next.set(turnId, {
      state,
      revision: eventRevision ?? prev?.revision,
      startedAt: built.startedAt ?? prev?.startedAt,
      completedAt: built.completedAt ?? prev?.completedAt,
      error: built.error ?? prev?.error,
      turnOrder: built.turnOrder ?? prev?.turnOrder,
      retryCount: built.retryCount ?? prev?.retryCount,
      retryMax: built.retryMax ?? prev?.retryMax,
      retryError: built.retryError ?? prev?.retryError,
    })
    this._activeTurnStates = next
    // A canonical state arrived for this turn — re-arm the
    // missing-terminal-event sweep marker so a future activity cycle that
    // again loses the terminal event can request another reconcile.
    if (isTerminalTurnState(state)) {
      this._canonicalReconcileRequested.delete(turnId)
      this._pendingPause = false
      this._closeOpenStepsInTurn(turnId)
    } else if (state === 'running' || state === 'paused') {
      this._pendingPause = false
    }
    return true
  }

  applyTurnEvent(event: TurnEvent): void {
    const _isTerminal = event.Kind === 'turn.cancelled' || event.Kind === 'turn.completed' || event.Kind === 'turn.failed' || event.Kind === 'turn.abandoned' || event.Kind === 'turn.waiting'
    const _t0 = _isTerminal && typeof performance !== 'undefined' ? performance.now() : 0
    switch (event.Kind) {
      case 'turn.context_budget': {
        if (!event.ContextBudget) break
        const cb = event.ContextBudget
        const turnId = event.TurnId || ''
        const budget: ContextBudget = {
          estimatedTokens: cb.EstimatedTokens ?? 0,
          contextWindowSize: cb.ContextWindowSize ?? 0,
          tokenBudget: cb.TokenBudget ?? 0,
        }
        const livePreviousBudget = turnId ? this._turnContextBudgets.get(turnId) : undefined
        const historyIndex = this._historyEnvelopes.findIndex(e =>
          e.role === 'assistant' && (e.id === turnId || e.metadata?.turnId === turnId),
        )
        const historyPreviousBudget = historyIndex >= 0
          ? this._historyEnvelopes[historyIndex]!.metadata?.contextBudget
          : undefined
        const previousBudget = livePreviousBudget?.estimatedTokens
          ? livePreviousBudget
          : historyPreviousBudget
        // The backend emits a zero-token placeholder before the probe result.
        // Events can arrive out of order, so never let that placeholder erase
        // an already received real provider/probe value.
        const effectiveBudget = previousBudget && previousBudget.estimatedTokens > 0 && budget.estimatedTokens === 0
          ? previousBudget
          : budget
        // Cache the latest budget per turn so active turns that are not yet
        // in historyEnvelopes can still render the context budget bar.
        if (turnId) {
          this._turnContextBudgets = new Map(this._turnContextBudgets)
          this._turnContextBudgets.set(turnId, effectiveBudget)
        }
        if (historyIndex >= 0) {
          this._historyEnvelopes[historyIndex] = {
            ...this._historyEnvelopes[historyIndex]!,
            metadata: {
              ...this._historyEnvelopes[historyIndex]!.metadata,
              contextBudget: effectiveBudget,
            },
          }
        }
        this._notify()
        break
      }
      case 'turn.unit_changed': {
        // β feedback: the aggregator reported the unit it actually selected.
        const payload = (event.Payload ?? {}) as { model?: unknown; provider?: unknown }
        const model = typeof payload.model === 'string' ? payload.model : ''
        if (!model) break
        const provider = typeof payload.provider === 'string' ? payload.provider : ''
        const next: ModelUnit = { model, provider }
        const prev = this._currentUnit
        if (!prev || prev.model !== next.model || (prev.provider ?? '') !== provider) {
          this._currentUnit = next
          this._notify()
        }
        break
      }
      case 'turn.dispatch_retry': {
        const turnId = event.TurnId
        if (!turnId) break
        const cur = this._activeTurnStates.get(turnId)
        if (!cur) break
        const payload = (event.Payload ?? {}) as { retry?: unknown; maxRetries?: unknown; error?: unknown }
        const retry = typeof payload.retry === 'number' ? payload.retry : 0
        const maxRetries = typeof payload.maxRetries === 'number' ? payload.maxRetries : 0
        const retryError = typeof payload.error === 'string' ? payload.error : undefined
        const next = new Map(this._activeTurnStates)
        next.set(turnId, {
          ...cur,
          retryCount: retry > 0 ? retry : undefined,
          retryMax: retry > 0 ? maxRetries : undefined,
          retryError: retry > 0 ? retryError : undefined,
        })
        this._activeTurnStates = next
        this._notify()
        break
      }
      case 'turn.started': {
        const turnId = event.TurnId
        if (!turnId) break
        const payload = (event.Payload ?? {}) as { startedAt?: unknown; state?: unknown; turnOrder?: unknown }
        const startedAt = typeof payload.startedAt === 'string' ? payload.startedAt : new Date().toISOString()
        const rawOrder = payload.turnOrder
        const turnOrder = typeof rawOrder === 'number' && !Number.isNaN(rawOrder) ? rawOrder : undefined
        this._applyCanonicalLifecycle(turnId, 'running', payloadRevision(event.Payload), () => ({
          startedAt,
          completedAt: undefined,
          turnOrder,
        }))
        break
      }
      case 'turn.paused': {
        const turnId = event.TurnId
        if (!turnId) break
        this._applyCanonicalLifecycle(turnId, 'paused', payloadRevision(event.Payload), prev => ({
          // Preserve turnOrder through the paused transition so the step-derived
          // envelope keeps its correct sort position.
          startedAt: prev?.startedAt,
          completedAt: undefined,
          turnOrder: prev?.turnOrder,
        }))
        break
      }
      case 'turn.waiting': {
        const turnId = event.TurnId
        if (!turnId) break
        const payload = (event.Payload ?? {}) as { startedAt?: unknown; completedAt?: unknown; turnOrder?: unknown }
        const completedAt = typeof payload.completedAt === 'string' ? payload.completedAt : new Date().toISOString()
        const startedAt = typeof payload.startedAt === 'string' ? payload.startedAt : undefined
        const payloadTurnOrder = typeof payload.turnOrder === 'number' && !Number.isNaN(payload.turnOrder)
          ? payload.turnOrder
          : undefined
        this._applyCanonicalLifecycle(turnId, 'waiting', payloadRevision(event.Payload), prev => ({
          startedAt: startedAt ?? prev?.startedAt,
          completedAt,
          turnOrder: payloadTurnOrder ?? prev?.turnOrder,
        }))
        break
      }
      case 'turn.resumed': {
        const turnId = event.TurnId
        if (!turnId) break
        this._applyCanonicalLifecycle(turnId, 'running', payloadRevision(event.Payload), prev => ({
          // Backend turn.resumed is always non-terminal; there is no terminal 'resumed' state.
          startedAt: prev?.startedAt,
          completedAt: undefined,
          turnOrder: prev?.turnOrder,
        }))
        break
      }
      case 'turn.completed':
      case 'turn.failed':
      case 'turn.cancelled':
      case 'turn.abandoned': {
        const turnId = event.TurnId
        if (!turnId) break
        const payload = (event.Payload ?? {}) as { startedAt?: unknown; completedAt?: unknown; error?: unknown; turnOrder?: unknown }
        const state: TurnState = event.Kind === 'turn.completed' ? 'completed'
          : event.Kind === 'turn.failed' ? 'failed'
          : event.Kind === 'turn.cancelled' ? 'cancelled'
          : 'abandoned'
        const completedAt = typeof payload.completedAt === 'string' ? payload.completedAt : new Date().toISOString()
        const startedAt = typeof payload.startedAt === 'string' ? payload.startedAt : undefined
        const error = state === 'failed' && typeof payload.error === 'string' ? payload.error : undefined
        const payloadTurnOrder = typeof payload.turnOrder === 'number' && !Number.isNaN(payload.turnOrder)
          ? payload.turnOrder
          : undefined
        this._applyCanonicalLifecycle(turnId, state, payloadRevision(event.Payload), prev => ({
          // Keep the turn.started ordering key through the terminal transition.
          // Until the next summary supplies the committed Turn envelope, the
          // projection has no other source of TurnOrder for this assistant turn.
          startedAt: startedAt ?? prev?.startedAt,
          completedAt,
          error,
          turnOrder: payloadTurnOrder ?? prev?.turnOrder,
        }))
        // Terminal events don't carry contextBudget. Flush any live budget into
        // the assistant history envelope's metadata so it survives beyond the
        // active turn, then remove the per-turn cache entry so completed turns
        // don't stay in the map forever.
        const liveBudget = this._turnContextBudgets.get(turnId)
        if (liveBudget) {
          const historyIndex = this._historyEnvelopes.findIndex(e =>
            e.role === 'assistant' && (e.id === turnId || e.metadata?.turnId === turnId),
          )
          if (historyIndex >= 0) {
            const existingBudget = this._historyEnvelopes[historyIndex]!.metadata?.contextBudget
            if (!existingBudget || existingBudget.estimatedTokens === 0) {
              this._historyEnvelopes[historyIndex] = {
                ...this._historyEnvelopes[historyIndex]!,
                metadata: {
                  ...this._historyEnvelopes[historyIndex]!.metadata,
                  contextBudget: liveBudget,
                },
              }
            }
          }
          this._turnContextBudgets = new Map(this._turnContextBudgets)
          this._turnContextBudgets.delete(turnId)
        }
        break
      }
      default:
        break
    }
    this._notify()
    if (_isTerminal && _t0) {
      const _t1 = performance.now()
      if (_t1 - _t0 > 50) {
        console.warn(`[PERF applyTurnEvent] SLOW ${event.Kind}: ${(_t1-_t0).toFixed(1)}ms steps=${this._steps.length}`)
      }
    }
  }

  /** Close any remaining open steps in the given turn so isStreaming
   *  stays consistent with the envelope state. Also marks ContentStatus as
   *  'stable' — otherwise UI keeps showing "streaming" indefinitely (AUDIT 4.1).
   *  Pending interaction steps are left open so the approval/ask_user UI
   *  remains visible when a turn enters the 'waiting' terminal state. */
  private _closeOpenStepsInTurn(turnId: string): void {
    const hasOpen = this._steps.some(s => s.TurnId === turnId && !s.Closed)
    if (!hasOpen) return
    this._steps = this._steps.map(s =>
      s.TurnId === turnId && !s.Closed && s.InteractionStatus !== 'pending'
        ? { ...s, Closed: true, ContentStatus: 'stable' as const }
        : s,
    )
  }

  /** P2 defensive reap: when a turn's terminal event (turn.completed/failed/
   *  cancelled) is permanently lost, _closeOpenStepsInTurn never fires and the
   *  compaction step stays open + running forever. This method detects turns
   *  whose latest step is older than STALE_PENDING_TURN_MS (5 min) and closes
   *  their open steps as a last-resort frontend-only recovery. The next
   *  reconnect / reconcile overwrites with authoritative state.
   *
   *  Driven by a coarse React-level timer (useTimelineManager), NOT from
   *  getSnapshot (which must stay side-effect-free for useSyncExternalStore)
   *  and NOT from applyTurnEvent — normal turn.completed still flows through
   *  _closeOpenStepsInTurn without interference. */
  private _reapStaleOpenSteps(nowMs: number = Date.now()): void {
    const turnIds = new Set<string>()
    for (const s of this._steps) {
      if (!s.Closed && s.TurnId) turnIds.add(s.TurnId)
    }
    if (turnIds.size === 0) return
    // Group steps by TurnId so isTurnStale can inspect each turn's steps.
    const byTurn = new Map<string, Step[]>()
    for (const s of this._steps) {
      if (s.TurnId && turnIds.has(s.TurnId)) {
        let list = byTurn.get(s.TurnId)
        if (!list) { list = []; byTurn.set(s.TurnId, list) }
        list.push(s)
      }
    }
    let changed = false
    for (const [turnId, steps] of byTurn) {
      // A pending interaction (plan_approval / goal_submit / ask_user) is a
      // user-input gate that must never be reaped — the user may take
      // arbitrarily long to respond.
      if (steps.some(s => !s.Closed && s.InteractionStatus === 'pending')) {
        continue
      }
      if (isTurnStale(steps, nowMs)) {
        const hasOpen = steps.some(s => !s.Closed)
        if (hasOpen) {
          this._closeOpenStepsInTurn(turnId)
          changed = true
        }
      }
    }
    if (changed) this._notify()
  }

  /** Missing-terminal-event reconcile sweep. When a turn's terminal lifecycle
   *  event (turn.completed / turn.waiting / ...) is lost on the wire (the
   *  backend eventbus drops events with no subscribers), the turn keeps ALL
   *  steps closed but has no canonical state anywhere: no live
   *  _activeTurnStates entry and no revision-bearing history envelope. The
   *  projection then falls back to the step-derived heuristic ('completed'),
   *  which is wrong for workflow owners parked in 'waiting' — the tail shows
   *  "success" until a manual history reload.
   *
   *  This sweep, driven by the same coarse 15s timer as the stale-step reap,
   *  detects such turns once their last step activity has been quiet for
   *  MISSING_TERMINAL_RECONCILE_DELAY_MS (past the normal ordering window
   *  where the terminal turn event arrives milliseconds after the last
   *  step.closed) and requests a single summary reconcile so the authoritative
   *  record state (waiting/completed/failed) is recovered. One-shot per
   *  turnId; the marker clears when a canonical state for the turn arrives. */
  private _reconcileMissingTerminalTurns(nowMs: number): void {
    if (!this.reconciler) return
    const byTurn = new Map<string, Step[]>()
    for (const s of this._steps) {
      if (!s.TurnId) continue
      let list = byTurn.get(s.TurnId)
      if (!list) { list = []; byTurn.set(s.TurnId, list) }
      list.push(s)
    }
    let fetchNeeded = false
    for (const [turnId, steps] of byTurn) {
      if (this._activeTurnStates.has(turnId)) continue
      if (this._canonicalReconcileRequested.has(turnId)) continue
      // Live turns (any open step) are not terminal candidates.
      if (steps.some(s => !s.Closed)) continue
      const hist = this._historyEnvelopes.find(e =>
        e.role === 'assistant' && e.metadata?.turnId === turnId)
      const histState = hist?.metadata?.turnState
      const histRevision = hist?.metadata?.revision
      // A revision-bearing history envelope is already an authoritative
      // canonical source — the projection resolves from it; no fetch needed.
      if (histRevision != null && isValidTurnState(histState)) {
        this._canonicalReconcileRequested.add(turnId)
        continue
      }
      const latest = latestStepMs(steps)
      if (latest <= 0 || nowMs - latest < MISSING_TERMINAL_RECONCILE_DELAY_MS) continue
      // Mark EVERY pending turn in this pass and fetch at most once: the
      // summary is global, so one fetch serves all. Fetching one turn per
      // 15s tick (the old break) turned N un-reconciled turns into a train
      // of state applications at 15s cadence — periodic idle re-renders.
      this._canonicalReconcileRequested.add(turnId)
      fetchNeeded = true
    }
    if (fetchNeeded) void this.reconciler.fetch('reconcile')
  }

  /** P2 public entry for the React-level reap timer (useTimelineManager). Reaps
   *  stale open steps whose turn terminal event was permanently lost. */
  reapStaleOpenSteps(nowMs: number = Date.now()): void {
    this._reapStaleOpenSteps(nowMs)
    this._reconcileMissingTerminalTurns(nowMs)
  }

  // ── State mutations ──

  setPendingPause(v: boolean): void {
    if (this._pendingPause === v) return
    this._pendingPause = v
    this._notify()
  }

  setLoading(v: boolean): void {
    this._loading = v
    this._notify()
  }

  setLoadingMore(v: boolean): void {
    this._loadingMore = v
    this._notify()
  }

  setError(msg: string | null): void {
    this._error = msg
    this._notify()
  }

  setHasMoreHistory(v: boolean): void {
    this._hasMoreHistory = v
  }

  initFromLoad(steps: Step[], historyEnvs: TurnEnvelope[], hasMoreHistory: boolean, goal?: SessionGoal): void {
    this._steps = steps
    this._historyEnvelopes = truncateHistoryEnvelopes(historyEnvs)
    this._importedSnapshot = true
    this._localInteractionResponses = new Map()
    this._activeTurnStates.clear()
    this._pendingPause = false
    this._currentGoal = goal
    // Wholesale replace _turnTasks (importHistory semantics). _syncTurnTasksFromEnvelopes
    // merges by default (AUDIT 3.3) — clear first to preserve the discard behavior.
    this._turnTasks.clear()
    this._turnFileChanges.clear()
    this._syncTurnTasksFromEnvelopes(historyEnvs)
    this._hasMoreHistory = hasMoreHistory
    this._loading = false
    this._error = null
    this._notify()
  }

  /** Apply a fresh-load summary: refresh history envelopes + merge steps,
   *  preserving ephemeral live state (interaction responses, active turn
   *  states, file changes, context budgets) keyed by turnId/requestId so
   *  summary-in-flight user actions don't disappear.
   *
   *  AUDIT 2.1: prior impl did wholesale `_localInteractionResponses = new Map()`
   *  / `_activeTurnStates.clear()` / `_turnFileChanges.clear()` / `_turnContextBudgets.clear()`,
   *  wiping answers submitted while the summary was in flight.
   *
   *  Differs from initFromLoad (wholesale replace, used by importHistory) and
   *  applySummaryToState (reconnect path, similar merge semantics but operates
   *  on stale-close flags rather than fresh load). */
  /** Drop active-turn entries for turnIds the server now reports as terminal
   *  (completed/failed/cancelled/abandoned). The history envelope is authoritative;
   *  keep entries for turnIds the server doesn't know yet (live-only).
   *  Shared by applyInitSummary (init path) and applySummaryToState (reconnect
   *  path) — divergence here was the root cause of stuck `_activeTurnStates`
   *  when turn.completed was lost in flight. */
  pruneActiveTurnStates(terminalTurnIds: Set<string>): void {
    if (terminalTurnIds.size === 0) return
    const next = new Map<string, TurnStateEntry>()
    for (const [k, v] of this._activeTurnStates) {
      if (terminalTurnIds.has(k)) continue
      next.set(k, v)
    }
    this._activeTurnStates = next
  }

  /** Reconcile active turn states against authoritative history envelopes.
   *  A history envelope carrying a newer canonical Revision overwrites the
   *  local event cache; legacy envelopes without revision use prior precedence
   *  (terminal prunes, running/paused seeds/updates). */
  reconcileActiveTurnStatesFromHistory(historyEnvs: TurnEnvelope[]): void {
    if (historyEnvs.length === 0 && this._activeTurnStates.size === 0) return
    let changed = false
    const next = new Map(this._activeTurnStates)
    for (const env of historyEnvs) {
      if (env.role !== 'assistant') continue
      const turnId = env.metadata?.turnId
      if (!turnId) continue
      const active = next.get(turnId)
      const histRevision = env.metadata?.revision
      const activeRevision = active?.revision
      // If both sides have revisions, keep the newer source. This lets a
      // summary reconnect converge to the server's terminal state even when a
      // stale running event was buffered locally.
      if (histRevision != null && activeRevision != null && histRevision < activeRevision) continue
      const state = env.metadata?.turnState
      if (!state || !isValidTurnState(state)) continue
      if (isTerminalTurnState(state)) {
        next.delete(turnId)
        changed = true
      } else if (state === 'running' || state === 'paused') {
        next.set(turnId, {
          state,
          revision: histRevision ?? active?.revision,
          startedAt: env.metadata?.startedAt ?? active?.startedAt,
          completedAt: env.metadata?.completedAt ?? active?.completedAt,
          error: env.metadata?.error ?? active?.error,
          turnOrder: env.turnSeq ?? active?.turnOrder,
        })
        changed = true
      }
    }
    if (changed) this._activeTurnStates = next
  }

  /** Proactively seed a running turn in _activeTurnStates before the
   *  turn.started SSE event arrives (or in case it never arrives).
   *  Used after chat.submit returns TurnActorID so isStreaming returns
   *  true immediately and the assistant envelope renders without delay.
   *  Does nothing if an entry already exists for the given turnId
   *  (live turn.started or a prior seed takes precedence). */
  seedActiveTurn(turnId: string, startedAt?: string, turnOrder?: number, revision?: number): void {
    if (this._activeTurnStates.has(turnId)) return
    const next = new Map(this._activeTurnStates)
    next.set(turnId, {
      state: 'running',
      revision,
      startedAt: startedAt ?? new Date().toISOString(),
      completedAt: undefined,
      turnOrder,
    })
    this._activeTurnStates = next
    this._notify()
  }

  /** Highest step-event EventSeq observed locally for the given turn, derived
   *  from seqTracking (per-step last consumed block.appended/block.delta seq)
   *  across the turn's steps. Compared against the server's
   *  ActiveTurn.EventSeq watermark by applySummaryToState to decide whether the
   *  active turn's open steps are stale and must be authoritatively replaced. */
  activeTurnEventSeq(turnId: string): number {
    let max = 0
    for (const s of this._steps) {
      if (s.TurnId !== turnId) continue
      const seq = this.seqTracking.get(s.Id) ?? 0
      if (seq > max) max = seq
    }
    return max
  }

  /** Raise the per-step EventSeq watermark for a turn's steps to `watermark`.
   *  Called after an authoritative snapshot replacement so the OpenStepEvents
   *  replay (whose events are already reflected in the snapshot) is skipped by
   *  the reducer's dedup instead of double-appending deltas. */
  bumpTurnEventSeq(turnId: string, watermark: number): void {
    if (watermark <= 0) return
    for (const s of this._steps) {
      if (s.TurnId !== turnId) continue
      const cur = this.seqTracking.get(s.Id) ?? 0
      if (watermark > cur) this.seqTracking.set(s.Id, watermark)
    }
  }

  applyInitSummary(serverSteps: Step[], historyEnvs: TurnEnvelope[], hasMoreHistory: boolean, hasDiscardedSteps = false, agentState?: string, goal?: SessionGoal): void {
    console.info(`[jitter-diag] init-summary turns=${historyEnvs.length} steps=${serverSteps.length} prevSteps=${this._steps.length} prevHist=${this._historyEnvelopes.length}`)
    const activeEnvelope = historyEnvs.find(e =>
      e.role === 'assistant' && e.metadata?.turnState === 'running' && e.metadata.turnId,
    )
    // Reconcile first so seedActiveTurn only fills a missing running entry.
    this.reconcileActiveTurnStatesFromHistory(historyEnvs)
    if (activeEnvelope?.metadata?.turnId && !this._activeTurnStates.has(activeEnvelope.metadata.turnId)) {
      this.seedActiveTurn(
        activeEnvelope.metadata.turnId,
        activeEnvelope.metadata.startedAt,
        activeEnvelope.turnSeq,
        activeEnvelope.metadata?.revision,
      )
    }
    // AUDIT 2.1: keep _localInteractionResponses, _turnFileChanges,
    // _turnContextBudgets intact — summary is authoritative for the turn
    // list but not for in-flight local user actions.
    // Preserve imported envelopes the backend doesn't know about: when a
    // session was loaded from an exported JSON (importHistory), the backend's
    // real history diverges. Without this, the first reconcile replaces the
    // imported envelopes entirely, making the import vanish. We keep imported
    // turns the backend lacks as a visible prefix. One-shot flag.
    let baseEnvelopes = historyEnvs
    if (this._importedSnapshot) {
      const backendIds = new Set(historyEnvs.map(e => e.metadata?.turnId ?? e.id))
      const importedOnly = this._historyEnvelopes.filter(e => !backendIds.has(e.metadata?.turnId ?? e.id))
      baseEnvelopes = [...importedOnly, ...historyEnvs]
      this._importedSnapshot = false
    }
    this._historyEnvelopes = truncateHistoryEnvelopes(baseEnvelopes.filter((e, i, all) => {
      const id = e.metadata?.turnId ?? e.id
      return all.findIndex(x => (x.metadata?.turnId ?? x.id) === id) === i
    }))
    this._currentGoal = goal
    console.debug('[agent-session] applyInitSummary goal=', goal, 'currentGoal=', this._currentGoal)
    this.setAgentState(agentState)
    this._syncTurnTasksFromEnvelopes(historyEnvs)
    this._steps = serverSteps.length > 0 ? mergeSteps(this._steps, serverSteps) : this._steps
    this._confirmPendingUserMessagesFromSteps()
    this._hasMoreHistory = hasMoreHistory
    this._discardedBoundary = hasDiscardedSteps
    this._loading = false
    this._error = null
    this._notify()
  }

  setAgentState(agentState: string | undefined): void {
    if (agentState !== undefined) this._agentState = agentState
  }

  setHistoryEnvelopes(historyEnvs: TurnEnvelope[], notify = true): void {
    this._historyEnvelopes = truncateHistoryEnvelopes(historyEnvs)
    this._syncTurnTasksFromEnvelopes(historyEnvs)
    if (notify) this._notify()
  }

  setDiscardedBoundary(value: boolean, notify = true): void {
    this._discardedBoundary = value
    if (notify) this._notify()
  }

  appendOlderHistory(olderEnvelopes: TurnEnvelope[], hasMore: boolean, olderSteps?: Step[], summarizedBoundary?: boolean): void {
    const existingTurnIds = new Set(this._historyEnvelopes.map(e => e.metadata?.turnId ?? e.id))
    const uniqueOlder = olderEnvelopes.filter(e => !existingTurnIds.has(e.metadata?.turnId ?? e.id))
    this._historyEnvelopes = [...truncateHistoryEnvelopes(uniqueOlder), ...this._historyEnvelopes]
    if (olderSteps && olderSteps.length > 0) {
      const existingIds = new Set(this._steps.map(s => s.Id))
      const newSteps = olderSteps.filter(s => !existingIds.has(s.Id))
      if (newSteps.length > 0) {
        this._steps = [...newSteps, ...this._steps].sort((a, b) => {
          const cmp = compareOptionalSeq(a.Seq, b.Seq)
          if (cmp !== 0) return cmp
          return (a.Timestamp ?? '').localeCompare(b.Timestamp ?? '')
        })
        this._confirmPendingUserMessagesFromSteps()
      }
    }
    this._syncTurnTasksFromEnvelopes(this._historyEnvelopes)
    this._hasMoreHistory = hasMore
    if (summarizedBoundary !== undefined) this._summarizedBoundary = summarizedBoundary
    this._loadingMore = false
    this._notify()
  }

  /** Rebuild _turnTasks from history envelopes. Merges per-turnId: existing
   *  live entries (set by real-time task_updated events) are preserved, and
   *  history is only used as fallback for turnIds the live stream hasn't
   *  populated yet. AUDIT 3.3: prior wholesale replace wiped activeForm /
   *  status progress that the live event stream had accumulated. */
  private _syncTurnTasksFromEnvelopes(envelopes: TurnEnvelope[]): void {
    const next = new Map<string, TaskEntry[]>(this._turnTasks)
    for (const env of envelopes) {
      if (env.role !== 'assistant' || !env.tasks || env.tasks.length === 0) continue
      const turnId = env.metadata?.turnId || env.id
      if (!turnId) continue
      if (next.has(turnId)) continue
      next.set(turnId, env.tasks)
    }
    this._turnTasks = next
  }

  setSteps(steps: Step[], notify = true): void {
    this._steps = steps
    this._confirmPendingUserMessagesFromSteps()
    // AUDIT 1.6: also replay buffered local events — setSteps is called by
    // summary-load paths, which may bring in the step a buffered event was
    // waiting for.
    this._replayPendingLocalEvents()
    if (notify) this._notify()
  }

  dispatchLocalEvent(event: LocalEvent): void {
    const step = findStepByRequestId(this._steps, event.requestId)
    if (!step) {
      // AUDIT 1.6: target step doesn't exist yet (fast UI click, or the
      // step stream is mid-reconnect). Buffer the event and replay once
      // the step arrives. Cap at a small number to avoid unbounded growth
      // on a buggy caller that keeps dispatching against non-existent ids.
      if (this._pendingLocalEvents.length < 32) {
        this._pendingLocalEvents.push(event)
      }
      return
    }

    this._applyLocalEvent(event, step)
    this._notify()
  }

  private _applyLocalEvent(event: LocalEvent, _step: Step): void {
    const response: LocalInteractionResponse = (() => {
      switch (event.kind) {
        case 'ai.ask_answered':
          return { kind: 'ask_answered', answers: event.answers }
        case 'ai.permission_answered':
          return { kind: 'permission_answered', allowed: event.allowed }
        case 'ai.plan_approval_answered':
          return { kind: 'plan_approval_answered', decision: event.decision, editedPlan: event.editedPlan, feedback: event.feedback, selectedPolicy: event.selectedPolicy }
        case 'ai.goal_submit_answered':
          return { kind: 'goal_submit_answered', decision: event.decision, feedback: event.feedback }
        case 'ai.goal_card_submit_answered':
          return { kind: 'goal_card_submit_answered', decision: event.decision, feedback: event.feedback }
      }
    })()

    // Selector 化: only store the response in the Map. The step's
    // InteractionStatus stays as the backend last set it ('pending' until
    // step.interaction_resolved arrives). Selectors (steps-to-envelopes,
    // projection) derive the effective "resolved" state by consulting this
    // Map. This keeps a single source of truth and makes rollback a pure
    // Map delete — no step mutation to undo.
    this._localInteractionResponses = new Map(this._localInteractionResponses)
    this._localInteractionResponses.set(event.requestId, response)
  }

  /** Remove a previously-dispatched local interaction response. Used when
   *  the outbound RPC fails (useAIShellSource.realDispatch catch path) —
   *  since selectors treat _localInteractionResponses as authoritative,
   *  deleting the entry is enough to revert the optimistic UI. */
  rollbackLocalInteraction(requestId: string): void {
    if (!this._localInteractionResponses.has(requestId)) return
    this._localInteractionResponses = new Map(this._localInteractionResponses)
    this._localInteractionResponses.delete(requestId)
    this._notify()
  }

  /** Replay buffered local events whose target step has appeared since the
   *  last batch. Called from applyStepEvents. AUDIT 1.6. */
  private _replayPendingLocalEvents(): void {
    if (this._pendingLocalEvents.length === 0) return
    const remaining: LocalEvent[] = []
    let applied = false
    for (const event of this._pendingLocalEvents) {
      const step = findStepByRequestId(this._steps, event.requestId)
      if (step) {
        this._applyLocalEvent(event, step)
        applied = true
      } else {
        remaining.push(event)
      }
    }
    this._pendingLocalEvents = remaining
    if (applied) {
      // _notify is called by the caller (applyStepEvents); nothing to do here.
    }
  }

  // ── Pending user messages ──

  addPendingUserMessage(clientId: string, text: string): void {
    this._pendingMessages.enqueue(clientId, text)
    this._notify()
  }

  confirmUserMessage(clientId: string): void {
    this._pendingMessages.removeByClientId(clientId)
    this._notify()
  }

  /** Return the backend messageId for a pending entry, if bound. */
  getPendingMessageId(clientId: string): string | undefined {
    return this._pendingMessages.snapshot().find(e => e.clientId === clientId)?.messageId
  }

  /** Mark all currently-pending entries as failed. Called by timeline.stop()
   *  so a cancelled turn's queued messages don't linger in "sending" state
   *  for the full TTL window (AUDIT 1.8). */
  markPendingMessagesFailed(): void {
    this._pendingMessages.markAllFailed()
  }

  bindMessageId(clientId: string, messageId: string): void {
    this._pendingMessages.bindMessageId(clientId, messageId)
  }

  // ── Lifecycle ──

  /** Clear all state fields. Kept for backward compatibility with
   *  callers that used AgentStateLayer.reset(). */
  reset(): void {
    this._steps = []
    this.pendingStepEvents.clear()
    this._historyEnvelopes = []
    this._localInteractionResponses = new Map()
    this._activeTurnStates.clear()
    this._turnTasks.clear()
    this._turnFileChanges.clear()
    this._turnContextBudgets.clear()
    this._currentGoal = undefined
    this._currentUnit = undefined
    this._pendingMessages.clear()
    this._pendingLocalEvents = []
    this._pendingPause = false
    this._agentState = undefined
    this._loading = false
    this._loadingMore = false
    this._error = null
    this._hasMoreHistory = false
    this._summarizedBoundary = false
    this._listeners.clear()
    this._cachedSnapshot = null
  }

  /** Release all resources including per-agent caches. After calling this,
   *  the session is dead and should be removed from the registry. */
  release(): void {
    this.reset()
    this.stepCache.clear()
    this.seqTracking.clear()
    this._projectionCache.current = null
  }

  /** Reset all conversation state and caches while keeping listeners.
   *  Used when the user explicitly clears history for an active session. */
  clear(): void {
    this._steps = []
    this._historyEnvelopes = []
    this._localInteractionResponses = new Map()
    this._activeTurnStates.clear()
    this._turnTasks.clear()
    this._turnFileChanges.clear()
    this._turnContextBudgets.clear()
    this._currentGoal = undefined
    this._currentUnit = undefined
    this._pendingMessages.clear()
    this._pendingLocalEvents = []
    this._pendingPause = false
    this._agentState = undefined
    this._loading = false
    this._loadingMore = false
    this._error = null
    this._hasMoreHistory = false
    this._summarizedBoundary = false
    this.stepCache.clear()
    this.seqTracking.clear()
    this._projectionCache.current = null
    this._cachedSnapshot = null
    this._notify()
  }

  // ── Subscription ──

  subscribe(cb: () => void): () => void {
    this._listeners.add(cb)
    return () => { this._listeners.delete(cb) }
  }

  /** @internal — emit to listeners, invalidate snapshot cache. */
  _notify(): void {
    this._cachedSnapshot = null
    for (const l of this._listeners) l()
  }
}
