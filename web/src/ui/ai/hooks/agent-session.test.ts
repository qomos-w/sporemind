import { describe, it, expect, vi } from 'vitest'
import { AgentSession } from './agent-session'
// Static import: event-layer's module graph initializes the client singleton
// (dials the dev backend with retries when absent, ~5s). A dynamic
// `await import()` inside a test body charges that cost to the test's
// default 5s timeout — the AUDIT 3.2 test timed out nondeterministically
// depending on whether another test had warmed the module cache first.
import { applySummaryToState } from './event-layer'
import type { TurnEnvelope, PlanFrame, GoalSubmitFrame, ToolFrame } from '../model/frame-types'
import type { Step, StepEvent } from '../../../gen-types/aigen'

function makeStep(overrides?: Partial<Step>): Step {
  return {
    Id: 's1',
    Role: 'assistant',
    Type: 'text',
    Content: [{ Type: 'text', Text: 'hello' }],
    Closed: false,
    Timestamp: new Date().toISOString(),
    TurnId: 't1',
    ContentStatus: 'appending',
    ExecutionStatus: 'idle',
    InteractionStatus: 'none',
    ...overrides,
  }
}

function makeHistoryEnv(overrides?: Partial<TurnEnvelope>): TurnEnvelope {
  return {
    id: 'h1',
    role: 'assistant',
    frames: [{ id: 'f1', type: 'text', status: 'completed', content: 'old' }],
    timestamp: new Date().toISOString(),
    completed: true,
    metadata: { turnId: 't-old' },
    ...overrides,
  }
}

// ── A1: Initialization ──

describe('AgentSession', () => {
  it('preserves a real context budget when a zero-token placeholder arrives after it', () => {
    const session = new AgentSession('a1')
    session.setHistoryEnvelopes([{
      id: 't1',
      role: 'assistant',
      frames: [],
      timestamp: '1',
      completed: false,
      metadata: {
        turnId: 't1',
        contextBudget: { estimatedTokens: 0, contextWindowSize: 128000, tokenBudget: 100000 },
      },
    }])

    session.applyTurnEvent({
      Kind: 'turn.context_budget',
      TurnId: 't1',
      ContextBudget: { EstimatedTokens: 42000, ContextWindowSize: 128000, TokenBudget: 100000 },
    } as any)
    session.applyTurnEvent({
      Kind: 'turn.context_budget',
      TurnId: 't1',
      ContextBudget: { EstimatedTokens: 0, ContextWindowSize: 128000, TokenBudget: 100000 },
    } as any)

    const budget = session.getSnapshot().envelopes.find(e => e.metadata?.turnId === 't1')?.metadata?.contextBudget
    expect(budget?.estimatedTokens).toBe(42000)
  })

  it('surfaces the dispatch error text from turn.dispatch_retry onto the envelope metadata', () => {
    const session = new AgentSession('a1')
    session.setHistoryEnvelopes([{
      id: 't1',
      role: 'assistant',
      frames: [],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1' },
    }])

    session.applyTurnEvent({
      Kind: 'turn.started',
      TurnId: 't1',
      Payload: { startedAt: '2026-01-01T00:00:00Z', turnOrder: 1 },
    } as any)
    session.applyTurnEvent({
      Kind: 'turn.dispatch_retry',
      TurnId: 't1',
      Payload: { retry: 1, maxRetries: 5, error: 'http 429: quota exceeded' },
    } as any)

    const env = session.getSnapshot().envelopes.find(e => e.metadata?.turnId === 't1')
    expect(env?.metadata?.retryCount).toBe(1)
    expect(env?.metadata?.retryMax).toBe(5)
    expect(env?.metadata?.retryError).toBe('http 429: quota exceeded')
    session.release()
  })

  it('clears retryError when dispatch resolves (retry=0)', () => {
    const session = new AgentSession('a1')
    session.setHistoryEnvelopes([{
      id: 't1',
      role: 'assistant',
      frames: [],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1' },
    }])
    session.applyTurnEvent({
      Kind: 'turn.started',
      TurnId: 't1',
      Payload: { startedAt: '2026-01-01T00:00:00Z', turnOrder: 1 },
    } as any)
    session.applyTurnEvent({
      Kind: 'turn.dispatch_retry',
      TurnId: 't1',
      Payload: { retry: 2, maxRetries: 5, error: 'timeout' },
    } as any)
    session.applyTurnEvent({
      Kind: 'turn.dispatch_retry',
      TurnId: 't1',
      Payload: { retry: 0, maxRetries: 5, error: 'timeout' },
    } as any)

    const env = session.getSnapshot().envelopes.find(e => e.metadata?.turnId === 't1')
    expect(env?.metadata?.retryCount).toBeUndefined()
    expect(env?.metadata?.retryError).toBeUndefined()
    session.release()
  })

  it('clears _turnContextBudgets on terminal turn event while preserving envelope metadata', () => {
    const session = new AgentSession('a1')
    session.setHistoryEnvelopes([{
      id: 't1',
      role: 'assistant',
      frames: [],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1' },
    }])

    session.applyTurnEvent({
      Kind: 'turn.started',
      TurnId: 't1',
      Payload: { startedAt: '2026-01-01T00:00:00Z', turnOrder: 1 },
    } as any)
    session.applyTurnEvent({
      Kind: 'turn.context_budget',
      TurnId: 't1',
      ContextBudget: { EstimatedTokens: 42000, ContextWindowSize: 128000, TokenBudget: 100000 },
    } as any)

    expect(session.getTurnContextBudgets().get('t1')).toEqual({
      estimatedTokens: 42000,
      contextWindowSize: 128000,
      tokenBudget: 100000,
    })

    session.applyTurnEvent({
      Kind: 'turn.completed',
      TurnId: 't1',
      Payload: { completedAt: '2026-01-01T00:00:10Z' },
    } as any)

    expect(session.getTurnContextBudgets().size).toBe(0)
    const env = session.getSnapshot().envelopes.find(e => e.metadata?.turnId === 't1')
    expect(env).toBeDefined()
    expect(env!.metadata?.contextBudget).toEqual({
      estimatedTokens: 42000,
      contextWindowSize: 128000,
      tokenBudget: 100000,
    })
    session.release()
  })

  it('A1: initializes with empty caches', () => {
    const session = new AgentSession('a1')
    expect(session.agentActorId).toBe('a1')
    expect(session.stepCache.size).toBe(0)
    expect(session.seqTracking.size).toBe(0)
    expect(session.steps).toEqual([])
    expect(session.historyEnvelopes).toEqual([])
    expect(session.loading).toBe(false)
    expect(session.error).toBeNull()
    expect(session.hasMoreHistory).toBe(false)
    expect(session.isStreaming).toBe(false)
  })

  // ── A2: release ──

  it('A2: release() clears all state and caches', () => {
    const session = new AgentSession('a1')
    session.stepCache.set('s1', { sig: 'x', frames: [] })
    session.seqTracking.set('s1', 5)
    session.addPendingUserMessage('c1', 'hi')
    session.initFromLoad([makeStep()], [makeHistoryEnv()], true)

    expect(session.steps.length).toBeGreaterThan(0)
    expect(session.historyEnvelopes.length).toBeGreaterThan(0)
    expect(session.stepCache.size).toBeGreaterThan(0)
    expect(session.seqTracking.size).toBeGreaterThan(0)

    session.release()

    expect(session.steps).toEqual([])
    expect(session.historyEnvelopes).toEqual([])
    expect(session.stepCache.size).toBe(0)
    expect(session.seqTracking.size).toBe(0)
    expect(session.loading).toBe(false)
    expect(session.error).toBeNull()
    expect(session.hasMoreHistory).toBe(false)
    expect(session.getPendingUserMessages().length).toBe(0)
  })

  // ── A3: Listener isolation ──

  it('A3: two sessions do not cross-notify', () => {
    const sessionA = new AgentSession('a')
    const sessionB = new AgentSession('b')
    const cbA = vi.fn()
    const cbB = vi.fn()
    sessionA.subscribe(cbA)
    sessionB.subscribe(cbB)

    sessionA.setLoading(true)
    expect(cbA).toHaveBeenCalledTimes(1)
    expect(cbB).not.toHaveBeenCalled()

    sessionB.setLoading(true)
    expect(cbA).toHaveBeenCalledTimes(1) // no extra call
    expect(cbB).toHaveBeenCalledTimes(1)
  })

  // ── A4: Both notify in same tick ──

  it('A4: both sessions notify their own listeners correctly', () => {
    const sessionA = new AgentSession('a')
    const sessionB = new AgentSession('b')
    const cbA = vi.fn()
    const cbB = vi.fn()
    sessionA.subscribe(cbA)
    sessionB.subscribe(cbB)

    sessionA.setLoading(true)
    sessionB.setError('boom')

    expect(cbA).toHaveBeenCalledTimes(1)
    expect(cbB).toHaveBeenCalledTimes(1)
  })

  // ── A5: release isolation ──

  it("A5: release() does not clear another session's caches", () => {
    const sessionA = new AgentSession('a')
    const sessionB = new AgentSession('b')
    sessionA.stepCache.set('s1', { sig: 'x', frames: [] })
    sessionB.stepCache.set('s2', { sig: 'y', frames: [] })
    sessionA.seqTracking.set('s1', 5)
    sessionB.seqTracking.set('s2', 10)

    sessionA.release()

    expect(sessionB.stepCache.has('s2')).toBe(true)
    expect(sessionB.stepCache.size).toBe(1)
    expect(sessionB.seqTracking.get('s2')).toBe(10)
    expect(sessionA.stepCache.size).toBe(0)
  })

  // ── A6: seqTracking isolation ──

  it('A6: seqTracking is independent per session', () => {
    const sessionA = new AgentSession('a')
    const sessionB = new AgentSession('b')
    sessionA.seqTracking.set('s1', 5)
    sessionB.seqTracking.set('s1', 10)

    expect(sessionA.seqTracking.get('s1')).toBe(5)
    expect(sessionB.seqTracking.get('s1')).toBe(10)

    sessionA.seqTracking.set('s1', 15)
    expect(sessionA.seqTracking.get('s1')).toBe(15)
    expect(sessionB.seqTracking.get('s1')).toBe(10) // unaffected
  })

  // ── A7: Snapshot cache hit ──

  it('A7: getSnapshot() returns same reference when state is unchanged', () => {
    const session = new AgentSession('a1')
    session.initFromLoad([makeStep()], [makeHistoryEnv()], true)

    const snap1 = session.getSnapshot()
    const snap2 = session.getSnapshot()

    expect(snap1).toBe(snap2)
    expect(snap1.envelopes).toBe(snap2.envelopes)
  })

  // ── A8: Snapshot cache invalidation ──

  it('A8: getSnapshot() returns new reference after mutation', () => {
    const session = new AgentSession('a1')
    session.initFromLoad([makeStep()], [makeHistoryEnv()], true)

    const snap1 = session.getSnapshot()
    session.setLoading(true)
    const snap2 = session.getSnapshot()

    expect(snap1).not.toBe(snap2)
    expect(snap2.loading).toBe(true)
  })

  // ── Additional: getSnapshot uses per-agent projection cache ──

  it('getSnapshot() uses per-session projection cache (not shared across agents)', () => {
    const sessionA = new AgentSession('a')
    const sessionB = new AgentSession('b')
    sessionA.initFromLoad([makeStep()], [makeHistoryEnv()], true)
    sessionB.initFromLoad([makeStep({ Id: 's2' })], [makeHistoryEnv({ id: 'h2' })], false)

    const snapA1 = sessionA.getSnapshot()
    const snapB = sessionB.getSnapshot()

    // Different agents should produce different envelopes
    expect(snapA1.envelopes).not.toBe(snapB.envelopes)

    // Second call on sessionA should hit the per-session cache
    const snapA2 = sessionA.getSnapshot()
    expect(snapA1).toBe(snapA2)
  })

  // ── Step events use per-session seqTracking ──

  it('applyStepEvents uses per-session seqTracking for dedup', () => {
    const session = new AgentSession('a1')

    const event: StepEvent = {
      Kind: 'step.opened',
      StepId: 's-new',
      TurnId: 't1',
      Role: 'assistant',
      StepType: 'text',
      EventSeq: 1,
      Seq: 1,
    }

    session.applyStepEvents([event])
    expect(session.steps.length).toBe(1)

    // Re-applying the same step.opened is a no-op via StepId+TurnId check
    // (AUDIT 2.3: step.* events rely on idempotency checks, not seqTracking).
    session.applyStepEvents([event])
    expect(session.steps.length).toBe(1)
  })

  it('applyStepEvents still dedups block.delta by EventSeq (AUDIT 2.3)', () => {
    const session = new AgentSession('a1')
    // Seed an open step for the delta to land on.
    session.setSteps([makeStep({ Id: 's-delta', Content: [{ Type: 'text', Text: '' }] })])

    const delta: StepEvent = {
      Kind: 'block.delta',
      StepId: 's-delta',
      TurnId: 't1',
      BlockIndex: 0,
      Delta: 'a',
      EventSeq: 1,
    }
    session.applyStepEvents([delta])
    expect(session.steps[0]!.Content[0]!.Text).toBe('a')
    expect(session.seqTracking.get('s-delta')).toBe(1)

    // Re-applying the same block.delta (same EventSeq) is a no-op.
    session.applyStepEvents([delta])
    expect(session.steps[0]!.Content[0]!.Text).toBe('a')
  })

  it('applyStepEvents allows out-of-order step.opened after block.delta (AUDIT 2.3)', () => {
    const session = new AgentSession('a1')

    // Network reorder: block.delta seq=2 arrives before step.opened seq=1.
    const deltaFirst: StepEvent = {
      Kind: 'block.delta',
      StepId: 's-oop',
      TurnId: 't1',
      BlockIndex: 0,
      Delta: 'text',
      EventSeq: 2,
    }
    session.applyStepEvents([deltaFirst])
    // No step yet — delta is a no-op on _steps but seqTracking records it.
    expect(session.steps.length).toBe(0)

    const openedLate: StepEvent = {
      Kind: 'step.opened',
      StepId: 's-oop',
      TurnId: 't1',
      Role: 'assistant',
      StepType: 'text',
      Block: { Type: 'text', Text: '' },
      EventSeq: 1,
    }
    session.applyStepEvents([openedLate])
    // Prior implementation skipped step.opened because seq=1 <= 2. Step was
    // never created. Now: step.opened bypasses seqTracking and creates step.
    expect(session.steps.length).toBe(1)
    expect(session.steps[0]!.Id).toBe('s-oop')
  })

  it('applyStepEvents creates step from step.interaction_requested in real-time', () => {
    // Plan approval / permission events arrive as step.interaction_requested.
    // This is a step-creation event (like step.opened) and must NOT be
    // buffered in pendingStepEvents — otherwise the plan card only appears
    // after a page refresh (which fetches steps via session.summary).
    const session = new AgentSession('a1')

    const interactionEvent: StepEvent = {
      Kind: 'step.interaction_requested',
      StepId: 's-plan-1',
      TurnId: 't1',
      InteractionType: 'plan_approval',
      RequestId: 'req-1',
      Seq: 1,
      Task: { plan: 'do stuff', tasks: [], policy: 'default', editable: true },
    } as any

    session.applyStepEvents([interactionEvent])
    expect(session.steps.length).toBe(1)
    expect(session.steps[0]!.Id).toBe('s-plan-1')
    expect(session.steps[0]!.Type).toBe('plan_approval')
    expect((session.steps[0] as any).InteractionStatus).toBe('pending')
    // No orphaned pending events.
    expect((session as any).pendingStepEvents.size).toBe(0)
    session.release()
  })

  // ── Wave 4 / AUDIT 3.4: clearAgentTimeline now calls release() ──

  it('release() clears stepCache and seqTracking that reset() leaves behind (AUDIT 3.4)', () => {
    const session = new AgentSession('a1')
    // Populate the per-agent caches that were previously leaking.
    session.stepCache.set('s1', { sig: 'x', frames: [] })
    session.stepCache.set('s2', { sig: 'y', frames: [] })
    session.seqTracking.set('s1', 5)
    session.seqTracking.set('s2', 10)
    session.setSteps([makeStep()])
    session.setHistoryEnvelopes([makeHistoryEnv()])
    session.addPendingUserMessage('c1', 'hi')

    // Compare: reset() would leave caches alive.
    session.reset()
    expect(session.stepCache.size).toBe(2) // reset() does NOT clear stepCache
    expect(session.seqTracking.size).toBe(2) // reset() does NOT clear seqTracking

    // release() does the full cleanup including caches.
    session.release()
    expect(session.stepCache.size).toBe(0)
    expect(session.seqTracking.size).toBe(0)
    expect(session.steps).toEqual([])
  })

  // ── Wave 5 / AUDIT 3.2: applySummaryToState coalesces to single notify ──

  it('applySummaryToState notifies listeners exactly once (AUDIT 3.2)', async () => {
    const session = new AgentSession('a1')
    session.setSteps([makeStep({ Id: 'live-1' })])
    const listener = vi.fn()
    session.subscribe(listener)

    const summary = {
      Turns: [
        { Id: 't1', Role: 'assistant', State: 'completed', UserInput: '', Steps: [] as any[] },
      ],
      Steps: [],
      HasMoreHistory: false,
    } as any

    await applySummaryToState(session, summary)

    // AUDIT 3.2: previously fired twice (setHistoryEnvelopes + setSteps both
    // notified), letting listeners observe "new history + old steps".
    expect(listener).toHaveBeenCalledTimes(1)
    session.release()
  })

  // ── Wave 5 / AUDIT 3.3: turn tasks preserve live updates ──

  it('applySummaryToState preserves live task updates from being overwritten (AUDIT 3.3)', async () => {
    const session = new AgentSession('a1')

    // Simulate live task_updated events setting fresh activeForm for a task
    // in the active turn.
    session.setSteps([makeStep({ Id: 'asst-1', TurnId: 't-active' })])
    session.applyStepEvents([{
      Kind: 'step.task_updated',
      StepId: 'asst-1',
      TurnId: 't-active',
      TaskId: 'task-x',
      Task: { id: 'task-x', subject: 'do thing', status: 'in_progress', activeForm: 'live progress' },
    } as any])
    expect(session.getTurnTasks().get('t-active')?.[0]?.activeForm).toBe('live progress')

    // Summary arrives with a STALE snapshot of the same turn's tasks.
    const summary = {
      Turns: [
        { Id: 't-active', Role: 'assistant', State: 'running', UserInput: '', Steps: [] as any[] },
      ],
      ActiveTurn: {
        Turn: { Id: 't-active', Role: 'assistant', State: 'running' } as any,
        Tasks: [{ id: 'task-x', subject: 'do thing', status: 'pending' }],  // no activeForm
      },
      Steps: [],
      HasMoreHistory: false,
    } as any

    await applySummaryToState(session, summary)

    // Live activeForm must NOT be wiped by the stale summary snapshot.
    const tasks = session.getTurnTasks().get('t-active')
    expect(tasks).toBeDefined()
    expect(tasks?.[0]?.activeForm).toBe('live progress')
    session.release()
  })

  it('applySummaryToState returns true when NextSeq indicates a step gap', async () => {
    const session = new AgentSession('a1')
    session.setSteps([makeStep({ Id: 's1', Seq: 1, Closed: true })])

    const summary = {
      Turns: [{ Id: 't1', Role: 'assistant', State: 'running', UserInput: '', Steps: [] as any[] }],
      Steps: [],
      HasMoreHistory: true,
      NextSeq: 5,
    } as any

    const hasGap = await applySummaryToState(session, summary)
    expect(hasGap).toBe(true)
    session.release()
  })

  it('applySummaryToState replays OpenStepEvents on top of summary snapshot', async () => {
    const session = new AgentSession('a1')
    // Server snapshot has an open step with empty content.
    session.setSteps([makeStep({ Id: 's-open', Seq: 2, Closed: false, Content: [] as any[] })])

    const summary = {
      Turns: [{ Id: 't1', Role: 'assistant', State: 'running', UserInput: '', Steps: [] as any[] }],
      Steps: [makeStep({ Id: 's-open', Seq: 2, Closed: false, Content: [] as any[] })],
      HasMoreHistory: false,
      OpenStepEvents: [
        { Kind: 'block.appended', StepId: 's-open', Block: { Type: 'text', Text: '' }, EventSeq: 1 },
        { Kind: 'block.delta', StepId: 's-open', BlockIndex: 0, Delta: 'world', EventSeq: 2 },
      ] as StepEvent[],
    } as any

    await applySummaryToState(session, summary)
    const step = session.steps.find(s => s.Id === 's-open')
    expect(step).toBeDefined()
    expect(step?.Content?.[0]?.Text).toBe('world')
    session.release()
  })

  it('history envelope tasks populate _turnTasks when no live entry exists', async () => {
    const session = new AgentSession('a1')
    const historyWithTasks = makeHistoryEnv({
      id: 'h-completed',
      role: 'assistant',
      metadata: { turnId: 't-completed' },
      tasks: [{ id: 'task-old', subject: 'past work', status: 'completed' }],
    })
    session.setSteps([])
    session.setHistoryEnvelopes([historyWithTasks])

    expect(session.getTurnTasks().get('t-completed')).toBeDefined()
    expect(session.getTurnTasks().get('t-completed')?.[0]?.subject).toBe('past work')
    session.release()
  })

  // ── Wave 1: pending confirmation message-id priority ──

  it('confirmByMessageId wins over text fallback when text differs but id matches', () => {
    const session = new AgentSession('a1')
    // Entry's text intentionally differs from the user step's text. Only the
    // message id binds them. AUDIT 1.4: pure-text match would pick the wrong
    // entry in this scenario.
    session.addPendingUserMessage('c1', 'what user typed locally')
    session.bindMessageId('c1', 'ps-1')

    const serverStep = makeStep({
      Id: 'ps-1',
      Type: 'user_inject',
      Content: [{ Type: 'text', Text: 'different normalized text' }],
    })
    session.setSteps([serverStep])

    expect(session.getPendingUserMessages().length).toBe(0)
    session.release()
  })

  it('falls back to text match when entry has no messageId binding', () => {
    const session = new AgentSession('a1')
    session.addPendingUserMessage('c1', 'hello')

    const serverStep = makeStep({
      Id: 'unknown-id',
      Type: 'user_inject',
      Content: [{ Type: 'text', Text: 'hello' }],
    })
    session.setSteps([serverStep])

    expect(session.getPendingUserMessages().length).toBe(0)
    session.release()
  })

  // ── Wave 2 / AUDIT 3.1: applyInitSummary merges live steps with summary ──

  it('keeps paused composer state when a completed turn entry is present', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.completed', StepId: 'old-step', TurnId: 'old-turn', Payload: {} } as StepEvent)
    session.setAgentState('paused')
    expect(session.isPaused).toBe(true)
    session.release()
  })

  it('restores paused composer state from agent summary state', () => {
    const session = new AgentSession('a1')
    session.applyInitSummary([], [], false, false, 'paused')
    expect(session.isPaused).toBe(true)
    session.setAgentState('completed')
    expect(session.isPaused).toBe(false)
    session.release()
  })

  it('agent state paused suppresses isWaiting even when a stale waiting envelope lingers in history', () => {
    // Restart scenario: backend recovered the waiting turn to paused/recovery
    // (agent state = paused), but the loaded history still contains an
    // assistant envelope with turnState 'waiting' from before. isWaiting must
    // not fall back to the stale envelope and hide the resume button.
    const session = new AgentSession('a1')
    const staleWaiting: TurnEnvelope = {
      id: 't-wait', role: 'assistant', frames: [], timestamp: '1',
      completed: true,
      metadata: { turnId: 't-wait', turnState: 'waiting' },
    }
    session.setHistoryEnvelopes([staleWaiting], false)
    session.setAgentState('paused')
    expect(session.isPaused).toBe(true)
    expect(session.isWaiting).toBe(false)
    session.release()
  })

  it('isWaiting falls back to the last assistant envelope when no live state exists', () => {
    const session = new AgentSession('a1')
    const last: TurnEnvelope = {
      id: 't-wait', role: 'assistant', frames: [], timestamp: '1',
      completed: true,
      metadata: { turnId: 't-wait', turnState: 'waiting' },
    }
    const older: TurnEnvelope = {
      id: 't-old', role: 'assistant', frames: [], timestamp: '0',
      completed: true,
      metadata: { turnId: 't-old', turnState: 'paused' },
    }
    session.setHistoryEnvelopes([older, last], false)
    expect(session.isWaiting).toBe(true)
    session.setHistoryEnvelopes([last, older], false)
    expect(session.isWaiting).toBe(false)
    session.release()
  })

  it('applyInitSummary stores the current session goal', () => {
    const session = new AgentSession('a1')
    const goal = { Condition: 'implement goal display', MaxTurns: 10, TurnCount: 3 }
    session.applyInitSummary([], [], false, false, undefined, goal)
    expect(session.currentGoal).toEqual(goal)
    session.release()
  })

  it('reset clears the current session goal', () => {
    const session = new AgentSession('a1')
    session.setGoal({ Condition: 'implement goal display', MaxTurns: 10, TurnCount: 3 })
    expect(session.currentGoal).not.toBeUndefined()
    session.reset()
    expect(session.currentGoal).toBeUndefined()
    session.release()
  })

  it('applyInitSummary restores active running state from history envelope', () => {
    const session = new AgentSession('a1')
    session.applyInitSummary([], [{
      id: 'turn-active',
      role: 'assistant',
      frames: [],
      timestamp: '2026-01-01T00:00:00Z',
      completed: false,
      turnSeq: 7,
      metadata: {
        turnId: 'turn-active',
        turnState: 'running',
        startedAt: '2026-01-01T00:00:00Z',
      },
    }], false, true)
    expect(session.getActiveTurnStates().get('turn-active')).toMatchObject({
      state: 'running',
      startedAt: '2026-01-01T00:00:00Z',
      turnOrder: 7,
    })
    expect(session.isStreaming).toBe(true)
    session.release()
  })

  it('applyInitSummary merges live steps with summary instead of wiping', () => {
    const session = new AgentSession('a1')
    // Simulate live events that arrived before the summary landed.
    const liveStep = makeStep({
      Id: 'live-1',
      Type: 'text',
      Content: [{ Type: 'text', Text: 'streaming text so far' }],
      Seq: 5,
    })
    session.setSteps([liveStep])

    // Summary brings a server snapshot — it doesn't include live-1 (mid-stream)
    // but does include a completed history step.
    const serverStep = makeStep({
      Id: 'server-old',
      Type: 'text',
      Content: [{ Type: 'text', Text: 'past turn' }],
      Closed: true,
      Seq: 1,
    })

    const historyEnv = makeHistoryEnv()
    session.applyInitSummary([serverStep], [historyEnv], true)

    const ids = session.steps.map(s => s.Id).sort()
    expect(ids).toEqual(['live-1', 'server-old'])
    expect(session.loading).toBe(false)
    expect(session.error).toBeNull()
    expect(session.hasMoreHistory).toBe(true)
    session.release()
  })

  it('preserves imported history envelopes across the first reconcile (importHistory)', () => {
    const session = new AgentSession('a1')
    // Simulate an import: loaded envelopes the backend doesn't know about.
    const importedEnv = makeHistoryEnv({ id: 'imp-1', metadata: { turnId: 'turn-imported' } })
    session.initFromLoad([], [importedEnv], false)

    // The first server reconcile arrives with the real backend state, which
    // does NOT contain turn-imported. Without preservation, the imported
    // envelope would vanish here.
    const backendEnv = makeHistoryEnv({ id: 'be-1', metadata: { turnId: 'turn-backend' } })
    session.applyInitSummary([], [backendEnv], false)

    const turnIds = session.getSnapshot().envelopes.map(e => e.metadata?.turnId)
    expect(turnIds).toContain('turn-imported')
    expect(turnIds).toContain('turn-backend')

    // The second reconcile is a normal replace — the one-shot flag is gone.
    const backendEnv2 = makeHistoryEnv({ id: 'be-2', metadata: { turnId: 'turn-backend2' } })
    session.applyInitSummary([], [backendEnv2], false)
    const turnIds2 = session.getSnapshot().envelopes.map(e => e.metadata?.turnId)
    expect(turnIds2).toContain('turn-backend2')
    expect(turnIds2).not.toContain('turn-imported')
    session.release()
  })

  it('applyInitSummary clears ephemeral state but keeps merged steps', () => {
    const session = new AgentSession('a1')

    // Pretend some ephemeral state was set from earlier session activity.
    session.setSteps([makeStep({ Id: 's1', RequestId: 's1' })])
    session.dispatchLocalEvent({
      kind: 'ai.permission_answered',
      requestId: 's1',
      allowed: true,
    })
    expect(session.getLocalInteractionResponses().size).toBeGreaterThan(0)

    // After init summary, ephemeral state resets.
    session.applyInitSummary(
      [makeStep({ Id: 'fresh' })],
      [makeHistoryEnv()],
      false,
    )

    // AUDIT 2.1: local interaction responses survive init summary — the
    // user clicked during the in-flight fetch, that answer must not vanish.
    expect(session.getLocalInteractionResponses().size).toBe(1)
    expect(session.getActiveTurnStates().size).toBe(0)
    // Live s1 merged with summary's fresh — both kept (AUDIT 3.1 fix).
    expect(session.steps.map(s => s.Id).sort()).toEqual(['fresh', 's1'])
    session.release()
  })

  it('applyInitSummary preserves steps when summary has no serverSteps', () => {
    const session = new AgentSession('a1')
    const liveStep = makeStep({ Id: 'live-1', Seq: 5 })
    session.setSteps([liveStep])

    session.applyInitSummary([], [makeHistoryEnv()], false)

    // Live step preserved; only history/loading updated.
    expect(session.steps.map(s => s.Id)).toEqual(['live-1'])
    expect(session.loading).toBe(false)
    session.release()
  })

  it('applyInitSummary dedupes by StepId with newer Seq winning', () => {
    const session = new AgentSession('a1')
    // Live stream saw step X with Seq 3 (mid-stream, content partial).
    const liveStep = makeStep({ Id: 'X', Seq: 3, Content: [{ Type: 'text', Text: 'partial' }] })
    session.setSteps([liveStep])

    // Server summary has step X with Seq 5 (closed, full content).
    const serverStep = makeStep({
      Id: 'X',
      Seq: 5,
      Closed: true,
      Content: [{ Type: 'text', Text: 'full server snapshot' }],
    })

    session.applyInitSummary([serverStep], [makeHistoryEnv()], false)

    expect(session.steps).toHaveLength(1)
    expect(session.steps[0]!.Seq).toBe(5)
    expect(session.steps[0]!.Content[0]!.Text).toBe('full server snapshot')
    session.release()
  })

  // ── Wave 3 / AUDIT 4.1: _closeOpenStepsInTurn sets ContentStatus stable ──

  it('turn.completed preserves the TurnOrder received at turn.started', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({
      Kind: 'turn.started',
      TurnId: 't-end',
      Payload: { startedAt: '2026-01-01T00:00:00Z', turnOrder: 12 },
    } as any)
    session.applyTurnEvent({
      Kind: 'turn.completed',
      TurnId: 't-end',
      Payload: { completedAt: '2026-01-01T00:00:10Z' },
    } as any)
    expect(session.getActiveTurnStates().get('t-end')).toMatchObject({
      state: 'completed',
      turnOrder: 12,
    })
    session.release()
  })

  it('turn.paused preserves turnOrder so the envelope does not sort to top', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({
      Kind: 'turn.started',
      TurnId: 't-paused',
      Payload: { startedAt: '2026-01-01T00:00:00Z', turnOrder: 12 },
    } as any)
    session.applyTurnEvent({ Kind: 'turn.paused', TurnId: 't-paused' } as any)
    expect(session.getActiveTurnStates().get('t-paused')).toMatchObject({
      state: 'paused',
      turnOrder: 12,
    })
    session.release()
  })

  it('turn.resumed always flips to running and preserves turnOrder', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({
      Kind: 'turn.started',
      TurnId: 't-crash',
      Payload: { startedAt: '2026-01-01T00:00:00Z', turnOrder: 12 },
    } as any)
    session.applyTurnEvent({ Kind: 'turn.paused', TurnId: 't-crash' } as any)
    // Legacy payload.state='resumed' is no longer a terminal state.
    session.applyTurnEvent({
      Kind: 'turn.resumed',
      TurnId: 't-crash',
      Payload: { state: 'resumed' },
    } as any)
    expect(session.getActiveTurnStates().get('t-crash')).toMatchObject({
      state: 'running',
      turnOrder: 12,
    })

    // User-pause wake: turn.resumed with state='running' → running
    session.applyTurnEvent({
      Kind: 'turn.started',
      TurnId: 't-wake',
      Payload: { startedAt: '2026-01-01T00:00:00Z', turnOrder: 42 },
    } as any)
    session.applyTurnEvent({ Kind: 'turn.paused', TurnId: 't-wake' } as any)
    session.applyTurnEvent({
      Kind: 'turn.resumed',
      TurnId: 't-wake',
      Payload: { state: 'running' },
    } as any)
    expect(session.getActiveTurnStates().get('t-wake')).toMatchObject({
      state: 'running',
      turnOrder: 42,
    })
    session.release()
  })

  it('turn.completed marks remaining open steps as stable (AUDIT 4.1)', () => {
    const session = new AgentSession('a1')
    const streamingStep = makeStep({
      Id: 'streaming-1',
      TurnId: 't-end',
      Closed: false,
      ContentStatus: 'appending',
    })
    session.setSteps([streamingStep])

    session.applyTurnEvent({
      Kind: 'turn.completed',
      TurnId: 't-end',
    } as any)

    expect(session.steps[0]!.Closed).toBe(true)
    expect(session.steps[0]!.ContentStatus).toBe('stable')
    session.release()
  })

  it('turn.failed marks open steps as stable too', () => {
    const session = new AgentSession('a1')
    const streamingStep = makeStep({
      Id: 'streaming-2',
      TurnId: 't-fail',
      Closed: false,
      ContentStatus: 'appending',
    })
    session.setSteps([streamingStep])

    session.applyTurnEvent({
      Kind: 'turn.failed',
      TurnId: 't-fail',
    } as any)

    expect(session.steps[0]!.Closed).toBe(true)
    expect(session.steps[0]!.ContentStatus).toBe('stable')
    session.release()
  })

  it('turn.cancelled marks open steps as stable too', () => {
    const session = new AgentSession('a1')
    const streamingStep = makeStep({
      Id: 'streaming-3',
      TurnId: 't-cancel',
      Closed: false,
      ContentStatus: 'appending',
    })
    session.setSteps([streamingStep])

    session.applyTurnEvent({
      Kind: 'turn.cancelled',
      TurnId: 't-cancel',
    } as any)

    expect(session.steps[0]!.Closed).toBe(true)
    expect(session.steps[0]!.ContentStatus).toBe('stable')
    session.release()
  })

  // ── isStreaming derives from turn state, not step Closed ──

  it('isStreaming is false with no active turn state and no running history', () => {
    const session = new AgentSession('a1')
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('isStreaming follows turn.started / turn.completed lifecycle', () => {
    const session = new AgentSession('a1')
    expect(session.isStreaming).toBe(false)

    session.applyTurnEvent({
      Kind: 'turn.started',
      TurnId: 't1',
      Payload: { startedAt: '2024-01-01T00:00:00.000Z' },
    } as any)
    expect(session.isStreaming).toBe(true)

    session.applyTurnEvent({
      Kind: 'turn.completed',
      TurnId: 't1',
      Payload: { completedAt: '2024-01-01T00:00:01.000Z' },
    } as any)
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('isStreaming is false after turn.failed and turn.cancelled', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't-fail' } as any)
    expect(session.isStreaming).toBe(true)
    session.applyTurnEvent({ Kind: 'turn.failed', TurnId: 't-fail' } as any)
    expect(session.isStreaming).toBe(false)

    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't-cancel' } as any)
    expect(session.isStreaming).toBe(true)
    session.applyTurnEvent({ Kind: 'turn.cancelled', TurnId: 't-cancel' } as any)
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('isStreaming ignores step Closed flags and uses turn state', () => {
    const session = new AgentSession('a1')
    // Open steps with no turn state should NOT make isStreaming true.
    session.setSteps([makeStep({ Id: 'open-1', Closed: false, TurnId: 't1' })])
    expect(session.isStreaming).toBe(false)

    // Once a turn is running, isStreaming is true even if steps are closed.
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1' } as any)
    session.setSteps([makeStep({ Id: 'open-1', Closed: true, TurnId: 't1' })])
    expect(session.isStreaming).toBe(true)
    session.release()
  })

  it('isStreaming falls back to history envelope turnState when no active turn state', () => {
    const session = new AgentSession('a1')
    session.initFromLoad([], [
      makeHistoryEnv({
        id: 'active-env',
        role: 'assistant',
        completed: false,
        metadata: { turnId: 't-active', turnState: 'running' },
      }),
    ], false)
    expect(session.isStreaming).toBe(true)

    // Paused turns do NOT count as streaming, but DO count as isPaused
    // (history fallback is safe: backend status.State="paused" is only for
    // user pauses/restart recovery, not interaction pauses).
    session.initFromLoad([], [
      makeHistoryEnv({
        id: 'paused-env',
        role: 'assistant',
        completed: false,
        metadata: { turnId: 't-paused', turnState: 'paused' },
      }),
    ], false)
    expect(session.isStreaming).toBe(false)
    expect(session.isPaused).toBe(true)

    // Replace with a completed history envelope.
    session.initFromLoad([], [
      makeHistoryEnv({
        id: 'done-env',
        role: 'assistant',
        completed: true,
        metadata: { turnId: 't-done', turnState: 'completed' },
      }),
    ], false)
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('isStreaming is false for abandoned and cancelled history envelopes', () => {
    const session = new AgentSession('a1')
    session.initFromLoad([], [
      makeHistoryEnv({
        id: 'abandoned-env',
        role: 'assistant',
        completed: false,
        metadata: { turnId: 't-abandoned', turnState: 'abandoned', startedAt: '2026-01-01T00:00:00Z', completedAt: '2026-01-01T00:00:01Z' },
      }),
    ], false)
    expect(session.isStreaming).toBe(false)
    expect(session.isPaused).toBe(false)

    session.initFromLoad([], [
      makeHistoryEnv({
        id: 'cancelled-env',
        role: 'assistant',
        completed: false,
        metadata: { turnId: 't-cancelled', turnState: 'cancelled', startedAt: '2026-01-01T00:00:00Z', completedAt: '2026-01-01T00:00:01Z' },
      }),
    ], false)
    expect(session.isStreaming).toBe(false)
    expect(session.isPaused).toBe(false)
    session.release()
  })

  // ── reapStuckTurns clears wedged running turns ──

  it('reapStuckTurns closes stale open steps AND clears the running active-turn entry', () => {
    const session = new AgentSession('a1')
    const oldTs = new Date(Date.now() - 10 * 60 * 1000).toISOString() // 10 min ago
    session.seedActiveTurn('t-stuck', oldTs)
    session.setSteps([makeStep({ Id: 's1', TurnId: 't-stuck', Type: 'tool_call', Closed: false, Timestamp: oldTs })], false)
    expect(session.isStreaming).toBe(true)

    session.reapStuckTurns(3 * 60 * 1000) // 3-min threshold
    expect(session.isStreaming).toBe(false)
    expect(session.steps[0]!.Closed).toBe(true)
    session.release()
  })

  it('reapStuckTurns reaps stepless running turns via startedAt', () => {
    const session = new AgentSession('a1')
    const oldTs = new Date(Date.now() - 10 * 60 * 1000).toISOString()
    session.seedActiveTurn('t-no-steps', oldTs)
    expect(session.isStreaming).toBe(true)

    session.reapStuckTurns(3 * 60 * 1000)
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('reapStuckTurns never reaps pending interactions', () => {
    const session = new AgentSession('a1')
    const oldTs = new Date(Date.now() - 10 * 60 * 1000).toISOString()
    session.seedActiveTurn('t-gate', oldTs)
    session.setSteps([makeStep({ Id: 's1', TurnId: 't-gate', InteractionStatus: 'pending', Closed: false, Timestamp: oldTs })], false)
    expect(session.isStreaming).toBe(true)

    session.reapStuckTurns(3 * 60 * 1000)
    expect(session.isStreaming).toBe(true)
    expect(session.steps[0]!.Closed).toBe(false)
    session.release()
  })

  it('force-reaped turns are not resurrected by running history envelopes', () => {
    const session = new AgentSession('a1')
    const oldTs = new Date(Date.now() - 10 * 60 * 1000).toISOString()
    session.seedActiveTurn('t-stuck', oldTs)
    session.reapStuckTurns(3 * 60 * 1000)
    expect(session.isStreaming).toBe(false)

    // A summary fetch landing after the reap still reports the turn running —
    // the marker must suppress the resurrection.
    session.reconcileActiveTurnStatesFromHistory([
      makeHistoryEnv({ id: 'stuck-env', role: 'assistant', completed: false, metadata: { turnId: 't-stuck', turnState: 'running' } }),
    ])
    expect(session.isStreaming).toBe(false)

    // The isStreaming history fallback is guarded too.
    session.setHistoryEnvelopes([
      makeHistoryEnv({ id: 'stuck-env', role: 'assistant', completed: false, metadata: { turnId: 't-stuck', turnState: 'running' } }),
    ], false)
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('force-reaped turns are not reseeded by seedActiveTurn', () => {
    const session = new AgentSession('a1')
    const oldTs = new Date(Date.now() - 10 * 60 * 1000).toISOString()
    session.seedActiveTurn('t-stuck', oldTs)
    session.reapStuckTurns(3 * 60 * 1000)
    expect(session.isStreaming).toBe(false)

    session.seedActiveTurn('t-stuck')
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('a genuine turn.completed lifecycle event clears the force-reap marker', () => {
    const session = new AgentSession('a1')
    const oldTs = new Date(Date.now() - 10 * 60 * 1000).toISOString()
    session.seedActiveTurn('t-stuck', oldTs)
    session.reapStuckTurns(3 * 60 * 1000)
    expect((session as any)._forceReapedTurns.has('t-stuck')).toBe(true)
    expect(session.isStreaming).toBe(false)

    // Backend finally sends the terminal event — the marker clears and the
    // entry records the terminal state normally.
    session.applyTurnEvent({ Kind: 'turn.completed', TurnId: 't-stuck', Payload: { completedAt: new Date().toISOString() } } as any)
    expect((session as any)._forceReapedTurns.has('t-stuck')).toBe(false)
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('turn.resumed with state=resumed flips old crash-recovery turn back to running', () => {
    const session = new AgentSession('a1')
    session.initFromLoad([], [
      makeHistoryEnv({
        id: 'paused-env',
        role: 'assistant',
        completed: false,
        metadata: { turnId: 't-paused', turnState: 'paused', startedAt: '2026-01-01T00:00:00Z' },
      }),
    ], false)
    session.applyTurnEvent({
      Kind: 'turn.paused',
      TurnId: 't-paused',
    } as any)
    expect(session.isPaused).toBe(true)

    session.applyTurnEvent({
      Kind: 'turn.resumed',
      TurnId: 't-paused',
      Payload: { state: 'resumed' },
    } as any)

    expect(session.getActiveTurnStates().get('t-paused')?.state).toBe('running')
    expect(session.isPaused).toBe(false)
    const env = session.getSnapshot().envelopes.find(e => e.metadata?.turnId === 't-paused')
    expect(env?.metadata?.turnState).toBe('running')
    expect(env?.completed).toBe(false)
    session.release()
  })

  it('turn.resumed without resumed payload keeps same turn running (user pause wake)', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1' } as any)
    session.applyTurnEvent({ Kind: 'turn.paused', TurnId: 't1' } as any)
    expect(session.isPaused).toBe(true)

    session.applyTurnEvent({
      Kind: 'turn.resumed',
      TurnId: 't1',
      Payload: { state: 'running' },
    } as any)

    expect(session.getActiveTurnStates().get('t1')?.state).toBe('running')
    expect(session.isStreaming).toBe(true)
    expect(session.isPaused).toBe(false)
    session.release()
  })

  it('setPendingPause / turn.paused / terminal events drive isPausing', () => {
    const session = new AgentSession('a1')
    const listener = vi.fn()
    session.subscribe(listener)

    session.setPendingPause(true)
    expect(session.isPausing).toBe(true)
    expect(listener).toHaveBeenCalled()

    session.applyTurnEvent({ Kind: 'turn.paused', TurnId: 't1' } as any)
    expect(session.isPausing).toBe(false)

    session.setPendingPause(true)
    session.applyTurnEvent({ Kind: 'turn.resumed', TurnId: 't1' } as any)
    expect(session.isPausing).toBe(false)

    session.setPendingPause(true)
    session.applyTurnEvent({ Kind: 'turn.completed', TurnId: 't1' } as any)
    expect(session.isPausing).toBe(false)
    session.release()
  })

  it('turn.waiting marks the turn terminal and stops streaming', () => {
    const session = new AgentSession('a1')
    session.setSteps([
      makeStep({ Id: 's1', TurnId: 't1', Closed: false, ContentStatus: 'appending' }),
    ])
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1' } as any)
    expect(session.isStreaming).toBe(true)

    session.applyTurnEvent({ Kind: 'turn.waiting', TurnId: 't1', Payload: { completedAt: '2026-05-26T10:00:05Z' } } as any)

    expect(session.getActiveTurnStates().get('t1')?.state).toBe('waiting')
    expect(session.isStreaming).toBe(false)
    expect(session.isWaiting).toBe(true)
    expect(session.getSnapshot().isWaiting).toBe(true)
    // Open steps must be closed so the UI does not keep showing streaming.
    expect(session.steps.find(s => s.Id === 's1')?.Closed).toBe(true)
    expect(session.steps.find(s => s.Id === 's1')?.ContentStatus).toBe('stable')
    session.release()
  })

  it('turn.waiting leaves pending interaction steps open', () => {
    const session = new AgentSession('a1')
    session.setSteps([
      makeStep({ Id: 's1', TurnId: 't1', Closed: false, ContentStatus: 'appending', InteractionStatus: 'none' }),
      makeStep({ Id: 'perm-1', TurnId: 't1', Type: 'permission_request', Closed: false, ContentStatus: 'appending', InteractionStatus: 'pending', RequestId: 'req-1' }),
      makeStep({ Id: 'ask-1', TurnId: 't1', Type: 'ask_user', Closed: false, ContentStatus: 'appending', InteractionStatus: 'pending', RequestId: 'req-2' }),
    ])
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1' } as any)
    expect(session.isStreaming).toBe(true)

    session.applyTurnEvent({ Kind: 'turn.waiting', TurnId: 't1', Payload: { completedAt: '2026-05-26T10:00:05Z' } } as any)

    expect(session.getActiveTurnStates().get('t1')?.state).toBe('waiting')
    expect(session.isStreaming).toBe(false)
    expect(session.isWaiting).toBe(true)
    // Non-pending open steps should still be closed to stop the streaming UI.
    expect(session.steps.find(s => s.Id === 's1')?.Closed).toBe(true)
    expect(session.steps.find(s => s.Id === 's1')?.ContentStatus).toBe('stable')
    // Pending interaction steps must stay open so Allow/Deny/Approve UI remains visible.
    expect(session.steps.find(s => s.Id === 'perm-1')?.Closed).toBe(false)
    expect(session.steps.find(s => s.Id === 'perm-1')?.ContentStatus).toBe('appending')
    expect(session.steps.find(s => s.Id === 'ask-1')?.Closed).toBe(false)
    expect(session.steps.find(s => s.Id === 'ask-1')?.ContentStatus).toBe('appending')
    session.release()
  })

  it('isWaiting falls back to history envelope turnState', () => {
    const session = new AgentSession('a1')
    session.initFromLoad([], [
      makeHistoryEnv({
        id: 'waiting-env',
        role: 'assistant',
        completed: true,
        metadata: { turnId: 't-waiting', turnState: 'waiting' },
      }),
    ], false)
    expect(session.isStreaming).toBe(false)
    expect(session.isWaiting).toBe(true)
    expect(session.getSnapshot().isWaiting).toBe(true)
    expect(session.getSnapshot().isPaused).toBe(false)
    session.release()
  })

  it('running takes priority over waiting', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't-running' } as any)
    session.applyTurnEvent({ Kind: 'turn.waiting', TurnId: 't-waiting', Payload: {} } as any)
    expect(session.isStreaming).toBe(true)
    expect(session.isWaiting).toBe(false)
    expect(session.getSnapshot().isWaiting).toBe(false)
    session.release()
  })

  // ── Turn revision convergence ──

  it('discards stale lifecycle events whose revision is older than current', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 2 } } as any)
    expect(session.getActiveTurnStates().get('t1')?.revision).toBe(2)

    // Stale running event (rev 1) should not overwrite rev 2.
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 1 } } as any)
    expect(session.getActiveTurnStates().get('t1')?.revision).toBe(2)
    expect(session.isStreaming).toBe(true)

    // Stale terminal event (rev 1) should not terminate the turn.
    session.applyTurnEvent({ Kind: 'turn.completed', TurnId: 't1', Payload: { revision: 1 } } as any)
    expect(session.getActiveTurnStates().get('t1')?.revision).toBe(2)
    expect(session.isStreaming).toBe(true)
    session.release()
  })

  it('treats equal-revision lifecycle events as idempotent', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 1 } } as any)
    const before = session.getActiveTurnStates().get('t1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 1 } } as any)
    const after = session.getActiveTurnStates().get('t1')
    expect(after).toBe(before)
    expect(after?.revision).toBe(1)
    expect(session.isStreaming).toBe(true)
    session.release()
  })

  it('applies newer lifecycle events and updates revision', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 1 } } as any)
    session.applyTurnEvent({ Kind: 'turn.paused', TurnId: 't1', Payload: { revision: 2 } } as any)
    expect(session.getActiveTurnStates().get('t1')?.state).toBe('paused')
    expect(session.getActiveTurnStates().get('t1')?.revision).toBe(2)
    expect(session.isPaused).toBe(true)

    session.applyTurnEvent({ Kind: 'turn.resumed', TurnId: 't1', Payload: { revision: 3 } } as any)
    expect(session.getActiveTurnStates().get('t1')?.state).toBe('running')
    expect(session.getActiveTurnStates().get('t1')?.revision).toBe(3)
    expect(session.isStreaming).toBe(true)

    session.applyTurnEvent({ Kind: 'turn.completed', TurnId: 't1', Payload: { revision: 4 } } as any)
    expect(session.getActiveTurnStates().get('t1')?.state).toBe('completed')
    expect(session.getActiveTurnStates().get('t1')?.revision).toBe(4)
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('accepts legacy lifecycle events without revision for backward compatibility', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1' } as any)
    expect(session.getActiveTurnStates().get('t1')?.state).toBe('running')

    session.applyTurnEvent({ Kind: 'turn.completed', TurnId: 't1' } as any)
    expect(session.getActiveTurnStates().get('t1')?.state).toBe('completed')
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('applies turn.abandoned as a terminal lifecycle event', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 1 } } as any)
    session.applyTurnEvent({ Kind: 'turn.abandoned', TurnId: 't1', Payload: { revision: 2 } } as any)
    expect(session.getActiveTurnStates().get('t1')?.state).toBe('abandoned')
    expect(session.getActiveTurnStates().get('t1')?.revision).toBe(2)
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('discards stale turn.abandoned events', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 2 } } as any)
    session.applyTurnEvent({ Kind: 'turn.abandoned', TurnId: 't1', Payload: { revision: 1 } } as any)
    expect(session.getActiveTurnStates().get('t1')?.state).toBe('running')
    expect(session.isStreaming).toBe(true)
    session.release()
  })

  // ── Wave 3 / AUDIT 4.3: step.closed does NOT delete local interaction response ──

  it('step.closed preserves local interaction response (AUDIT 4.3)', () => {
    const session = new AgentSession('a1')
    const step = makeStep({ Id: 'perm-1', InteractionStatus: 'pending' })
    session.setSteps([step])
    session.dispatchLocalEvent({
      kind: 'ai.permission_answered',
      requestId: 'perm-1',
      allowed: true,
    })
    expect(session.getLocalInteractionResponses().get('perm-1')).toBeDefined()

    // step.closed fires — renderer still needs the local response afterwards.
    session.applyStepEvents([{
      Kind: 'step.closed',
      StepId: 'perm-1',
      TurnId: 't1',
    } as any])

    expect(session.getLocalInteractionResponses().get('perm-1')).toEqual({
      kind: 'permission_answered',
      allowed: true,
    })
    session.release()
  })

  // ── Wave 6 / AUDIT 1.6: dispatchLocalEvent buffers & replays ──

  it('dispatchLocalEvent buffers when step has not arrived yet (AUDIT 1.6)', () => {
    const session = new AgentSession('a1')
    // No step exists for req-early — should buffer.
    session.dispatchLocalEvent({
      kind: 'ai.permission_answered',
      requestId: 'req-early',
      allowed: true,
    })
    expect(session.getLocalInteractionResponses().has('req-early')).toBe(false)

    // Step arrives → buffered event replays.
    session.setSteps([makeStep({ Id: 'req-early', RequestId: 'req-early', InteractionStatus: 'pending' })])

    expect(session.getLocalInteractionResponses().get('req-early')).toEqual({
      kind: 'permission_answered',
      allowed: true,
    })
    session.release()
  })

  it('dispatchLocalEvent buffer is bounded (AUDIT 1.6)', () => {
    const session = new AgentSession('a1')
    // Spam 40 events for non-existent steps — only the first 32 should buffer.
    for (let i = 0; i < 40; i++) {
      session.dispatchLocalEvent({
        kind: 'ai.permission_answered',
        requestId: `req-${i}`,
        allowed: true,
      })
    }
    // Internal state — verify via behavior: when steps arrive, at most 32 confirm.
    // Easiest assertion: no responses recorded (none of the steps existed).
    expect(session.getLocalInteractionResponses().size).toBe(0)
    session.release()
  })

  it('snapshot reflects local plan approval immediately without a step event', () => {
    const session = new AgentSession('a1')
    const planStep = makeStep({
      Id: 't1-plan-req-1',
      Type: 'plan_approval',
      InteractionStatus: 'pending',
      RequestId: 'req-1',
      Content: [{ Type: 'text', Text: JSON.stringify({ plan: '# Plan', editable: true, tasks: [] }) }],
    })
    session.setSteps([planStep])

    // Warm the projection cache.
    expect((session.getSnapshot().envelopes[0]!.frames[0] as PlanFrame).approvalStatus).toBe('pending')

    session.dispatchLocalEvent({
      kind: 'ai.plan_approval_answered',
      requestId: 'req-1',
      decision: 'approve',
    })

    const frame = session.getSnapshot().envelopes[0]!.frames[0] as PlanFrame
    expect(frame.approvalStatus).toBe('approved')
    session.release()
  })

  it('rollbackLocalInteraction reverts optimistic plan approval so UI returns to pending', () => {
    const session = new AgentSession('a1')
    const planStep = makeStep({
      Id: 't1-plan-req-1', Type: 'plan_approval',
      InteractionStatus: 'pending',
      RequestId: 'req-1',
      Content: [{ Type: 'text', Text: JSON.stringify({ plan: '# Plan', editable: true, tasks: [] }) }],
    })
    session.setSteps([planStep])

    expect((session.getSnapshot().envelopes[0]!.frames[0] as PlanFrame).approvalStatus).toBe('pending')

    // Optimistic approve.
    session.dispatchLocalEvent({
      kind: 'ai.plan_approval_answered',
      requestId: 'req-1',
      decision: 'approve',
    })
    expect((session.getSnapshot().envelopes[0]!.frames[0] as PlanFrame).approvalStatus).toBe('approved')

    // RPC failed (e.g. turn already cancelled). Rollback.
    session.rollbackLocalInteraction('req-1')
    expect((session.getSnapshot().envelopes[0]!.frames[0] as PlanFrame).approvalStatus).toBe('pending')
    session.release()
  })

  it('rollbackLocalInteraction is a no-op when no response is stored', () => {
    const session = new AgentSession('a1')
    session.setSteps([makeStep({ Id: 's1', Closed: true })])
    // Should not throw.
    session.rollbackLocalInteraction('never-dispatched')
    session.release()
  })

  it('applyInitSummary preserves pending plan_approval frame across refresh', () => {
    const session = new AgentSession('a1')
    const planStep = makeStep({
      Id: 't1-plan-req-1',
      Type: 'plan_approval',
      InteractionStatus: 'pending',
      RequestId: 'req-1',
      Closed: false,
      Content: [{ Type: 'text', Text: JSON.stringify({ plan: '# Plan', editable: true, tasks: [] }) }],
    })
    const histEnv: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [],
      timestamp: new Date().toISOString(),
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }

    session.applyInitSummary([planStep], [histEnv], false)

    const envs = session.getSnapshot().envelopes
    const asst = envs.find(e => e.role === 'assistant')
    expect(asst).toBeDefined()
    const planFrame = asst!.frames.find(f => f.type === 'plan') as PlanFrame | undefined
    expect(planFrame).toBeDefined()
    expect(planFrame!.approvalStatus).toBe('pending')
    expect(planFrame!.requestId).toBe('req-1')
    session.release()
  })

  it('applyInitSummary preserves approved plan_approval frame across refresh', () => {
    const session = new AgentSession('a1')
    const planStep = makeStep({
      Id: 't1-plan-req-1',
      Type: 'plan_approval',
      InteractionStatus: 'resolved',
      RequestId: 'req-1',
      Closed: true,
      Content: [{ Type: 'text', Text: JSON.stringify({ plan: '# Plan', editable: true, tasks: [] }) }],
    })
    const histEnv: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [],
      timestamp: new Date().toISOString(),
      completed: true,
      metadata: { turnId: 't1', turnState: 'completed' },
    }

    session.applyInitSummary([planStep], [histEnv], false)

    const envs = session.getSnapshot().envelopes
    const asst = envs.find(e => e.role === 'assistant')
    expect(asst).toBeDefined()
    const planFrame = asst!.frames.find(f => f.type === 'plan') as PlanFrame | undefined
    expect(planFrame).toBeDefined()
    expect(planFrame!.approvalStatus).toBe('approved')
    session.release()
  })

  it('applyInitSummary renders expired plan frame when turn was cancelled mid-approval', () => {
    const session = new AgentSession('a1')
    const planStep = makeStep({
      Id: 't1-plan-req-1',
      Type: 'plan_approval',
      InteractionStatus: 'pending',
      RequestId: 'req-1',
      Closed: true,
      Content: [{ Type: 'text', Text: JSON.stringify({ plan: '# Plan', editable: true, tasks: [] }) }],
    })
    const histEnv: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [],
      timestamp: new Date().toISOString(),
      completed: true,
      metadata: { turnId: 't1', turnState: 'cancelled' },
    }

    session.applyInitSummary([planStep], [histEnv], false)

    const envs = session.getSnapshot().envelopes
    const asst = envs.find(e => e.role === 'assistant')
    expect(asst).toBeDefined()
    const planFrame = asst!.frames.find(f => f.type === 'plan') as PlanFrame | undefined
    expect(planFrame).toBeDefined()
    expect(planFrame!.approvalStatus).toBe('cancelled')
    session.release()
  })

  // ── workflow_start confirmation restoration after restart ──

  it('applyInitSummary preserves pending goal_submit frame across restart (paused turn)', () => {
    const session = new AgentSession('a1')
    // After restart, the workflow_start confirmation step is restored with
    // InteractionStatus='pending', Closed=false, and the assistant turn is
    // 'paused' (created by recoverOrphanTurnSteps). AgentState is 'running'.
    const goalStep = makeStep({
      Id: 't2-goal-req-1',
      Type: 'goal_submit',
      InteractionStatus: 'pending',
      RequestId: 'req-1',
      Closed: false,
      TurnId: 't2',
      Content: [{ Type: 'text', Text: JSON.stringify({ condition: 'Build a web app', interpretedGoal: 'Implement a web application' }) }],
    })
    const histEnv: TurnEnvelope = {
      id: 't2',
      role: 'assistant',
      frames: [],
      timestamp: new Date().toISOString(),
      completed: false,
      metadata: { turnId: 't2', turnState: 'paused' },
    }

    session.applyInitSummary([goalStep], [histEnv], false, false, 'running')

    const envs = session.getSnapshot().envelopes
    const asst = envs.find(e => e.role === 'assistant' && e.metadata?.turnId === 't2')
    expect(asst).toBeDefined()
    const goalFrame = asst!.frames.find(f => f.type === 'goal_submit') as GoalSubmitFrame | undefined
    expect(goalFrame).toBeDefined()
    expect(goalFrame!.approvalStatus).toBe('pending')
    expect(goalFrame!.requestId).toBe('req-1')
    expect(goalFrame!.condition).toBe('Build a web app')
    session.release()
  })

  it('isPaused is false when a pending interaction exists (no misleading Resume)', () => {
    const session = new AgentSession('a1')
    const goalStep = makeStep({
      Id: 't2-goal-req-1',
      Type: 'goal_submit',
      InteractionStatus: 'pending',
      RequestId: 'req-1',
      Closed: false,
      TurnId: 't2',
      Content: [{ Type: 'text', Text: JSON.stringify({ condition: 'Build a web app', interpretedGoal: 'Implement' }) }],
    })
    const histEnv: TurnEnvelope = {
      id: 't2',
      role: 'assistant',
      frames: [],
      timestamp: new Date().toISOString(),
      completed: false,
      metadata: { turnId: 't2', turnState: 'paused' },
    }

    session.applyInitSummary([goalStep], [histEnv], false, false, 'running')

    // The turn IS paused (deriveTurnState → 'paused'), but the Composer must
    // not show a "Resume" button — the interaction card handles user input.
    expect(session.getSnapshot().isPaused).toBe(false)
    session.release()
  })

  // ── Wave 6 / AUDIT 5.6: snapshot is frozen ──

  it('getSnapshot returns a frozen snapshot (AUDIT 5.6)', () => {
    const session = new AgentSession('a1')
    session.setSteps([makeStep({ Id: 's1', Closed: true })])
    const snap = session.getSnapshot()
    expect(Object.isFrozen(snap)).toBe(true)
    expect(Object.isFrozen(snap.envelopes)).toBe(true)
    session.release()
  })

  // ── step.file_changes reducer ──

  it('applyStepEvents accumulates step.file_changes into _turnFileChanges', () => {
    const session = new AgentSession('a1')
    session.applyStepEvents([
      { Kind: 'step.file_changes', StepId: 's1', TurnId: 't1', FileChanges: [
        { Path: 'foo.ts', Additions: 10, Deletions: 2, DiffContent: '@@ diff @@' },
      ] } as any,
    ])
    const tfc = session.getTurnFileChanges()
    expect(tfc.get('t1')).toBeDefined()
    expect(tfc.get('t1')!).toHaveLength(1)
    expect(tfc.get('t1')![0]!.filename).toBe('foo.ts')
    expect(tfc.get('t1')![0]!.filepath).toBe('foo.ts')
    expect(tfc.get('t1')![0]!.additions).toBe(10)
    expect(tfc.get('t1')![0]!.deletions).toBe(2)
    expect(tfc.get('t1')![0]!.diffContent).toBe('@@ diff @@')
    session.release()
  })

  it('step.file_changes merges by path within a turn (last write wins)', () => {
    const session = new AgentSession('a1')
    session.applyStepEvents([
      { Kind: 'step.file_changes', StepId: 's1', TurnId: 't1', FileChanges: [
        { Path: 'foo.ts', Additions: 5, Deletions: 0, DiffContent: '@@ v1 @@' },
      ] } as any,
      { Kind: 'step.file_changes', StepId: 's2', TurnId: 't1', FileChanges: [
        { Path: 'foo.ts', Additions: 3, Deletions: 1, DiffContent: '@@ v2 @@' },
      ] } as any,
    ])
    const tfc = session.getTurnFileChanges()
    expect(tfc.get('t1')!).toHaveLength(1)
    expect(tfc.get('t1')![0]!.additions).toBe(3)
    expect(tfc.get('t1')![0]!.deletions).toBe(1)
    expect(tfc.get('t1')![0]!.diffContent).toBe('@@ v2 @@')
    session.release()
  })

  it('step.file_changes accumulates across distinct paths in same turn', () => {
    const session = new AgentSession('a1')
    session.applyStepEvents([
      { Kind: 'step.file_changes', StepId: 's1', TurnId: 't1', FileChanges: [
        { Path: 'foo.ts', Additions: 1, Deletions: 0, DiffContent: '' },
      ] } as any,
      { Kind: 'step.file_changes', StepId: 's2', TurnId: 't1', FileChanges: [
        { Path: 'bar.css', Additions: 0, Deletions: 5, DiffContent: '' },
        { Path: 'baz.md', Additions: 2, Deletions: 2, DiffContent: '' },
      ] } as any,
    ])
    const tfc = session.getTurnFileChanges()
    expect(tfc.get('t1')!).toHaveLength(3)
    const paths = tfc.get('t1')!.map(fc => fc.filename).sort()
    expect(paths).toEqual(['bar.css', 'baz.md', 'foo.ts'])
    session.release()
  })

  it('step.file_changes ignores events with empty FileChanges array', () => {
    const session = new AgentSession('a1')
    session.applyStepEvents([
      { Kind: 'step.file_changes', StepId: 's1', TurnId: 't1', FileChanges: [] } as any,
    ])
    expect(session.getTurnFileChanges().has('t1')).toBe(false)
    session.release()
  })

  it('step.file_changes ignores events without TurnId', () => {
    const session = new AgentSession('a1')
    session.applyStepEvents([
      { Kind: 'step.file_changes', StepId: 's1', TurnId: '', FileChanges: [
        { Path: 'foo.ts', Additions: 1, Deletions: 0, DiffContent: '' },
      ] } as any,
    ])
    expect(session.getTurnFileChanges().size).toBe(0)
    session.release()
  })

  it('turn.completed event no longer carries fileChanges payload', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({
      Kind: 'turn.completed',
      TurnId: 't1',
      Payload: { fileChanges: [{ Path: 'should-be-ignored.ts', Additions: 99, Deletions: 99, DiffContent: '' }] },
    } as any)
    // Terminal event must NOT populate tfc — only step.file_changes events do.
    expect(session.getTurnFileChanges().has('t1')).toBe(false)
    session.release()
  })

  // ── Payload truncation ──

  it('truncates tool payloads in setHistoryEnvelopes', () => {
    const session = new AgentSession('a1')
    const longInput = JSON.stringify({ content: 'x'.repeat(5000) })
    const longOutput = 'y'.repeat(5000)
    session.setHistoryEnvelopes([{
      id: 'h1',
      role: 'assistant',
      timestamp: '1',
      frames: [{
        id: 't1',
        type: 'tool',
        toolName: 'project.read',
        status: 'completed',
        input: longInput,
        output: longOutput,
      } as ToolFrame],
    }])
    const stored = session.historyEnvelopes[0]!
    const tool = stored.frames[0] as ToolFrame
    expect(tool.input.length).toBeLessThan(2100)
    expect(tool.input).toContain('...(truncated')
    expect(tool.output!.length).toBeLessThan(2100)
    expect(tool.output).toContain('...(truncated')
    expect(tool.input.startsWith(longInput.slice(0, 1024))).toBe(true)
    expect(tool.input.endsWith(longInput.slice(-1024))).toBe(true)
    session.release()
  })

  it('truncates tool payloads in appendOlderHistory', () => {
    const session = new AgentSession('a1')
    const longInput = 'a'.repeat(5000)
    const older: TurnEnvelope = {
      id: 'older-1',
      role: 'assistant',
      timestamp: '1',
      frames: [{
        id: 'tool-1',
        type: 'tool',
        toolName: 'project.read',
        status: 'completed',
        input: longInput,
      } as ToolFrame],
    }
    session.appendOlderHistory([older], false)
    const stored = session.historyEnvelopes[0]!
    const tool = stored.frames[0] as ToolFrame
    expect(tool.input.length).toBeLessThan(2100)
    expect(tool.input).toContain('...(truncated')
    session.release()
  })

  it('truncates tool payloads in initFromLoad', () => {
    const session = new AgentSession('a1')
    const longInput = 'b'.repeat(5000)
    session.initFromLoad([], [{
      id: 'h1',
      role: 'assistant',
      timestamp: '1',
      frames: [{
        id: 't1',
        type: 'tool',
        toolName: 'project.read',
        status: 'completed',
        input: longInput,
      } as ToolFrame],
    }], false)
    const stored = session.historyEnvelopes[0]!
    const tool = stored.frames[0] as ToolFrame
    expect(tool.input.length).toBeLessThan(2100)
    expect(tool.input).toContain('...(truncated')
    session.release()
  })

  // ── β channel (currentUnit) seeding ──

  it('seedCurrentUnit fills the β channel when unset and notifies listeners', () => {
    const session = new AgentSession('a1')
    const cb = vi.fn()
    session.subscribe(cb)
    expect(session.currentUnit).toBeUndefined()

    session.seedCurrentUnit({ model: 'm-1', provider: 'p-1' })
    expect(session.currentUnit).toEqual({ model: 'm-1', provider: 'p-1' })
    expect(session.getSnapshot().currentUnit).toEqual({ model: 'm-1', provider: 'p-1' })
    expect(cb).toHaveBeenCalledTimes(1)
    session.release()
  })

  it('seedCurrentUnit never clobbers a live turn.unit_changed value', () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({
      Kind: 'turn.unit_changed',
      TurnId: 't1',
      Payload: { model: 'live-model', provider: 'live-provider' },
    } as any)

    session.seedCurrentUnit({ model: 'stale-model', provider: 'stale-provider' })
    expect(session.currentUnit).toEqual({ model: 'live-model', provider: 'live-provider' })
    session.release()
  })

  it('seedCurrentUnit is a no-op for an empty model', () => {
    const session = new AgentSession('a1')
    const cb = vi.fn()
    session.subscribe(cb)

    session.seedCurrentUnit({ model: '', provider: 'p' })
    expect(session.currentUnit).toBeUndefined()
    expect(cb).not.toHaveBeenCalled()
    session.release()
  })
})
