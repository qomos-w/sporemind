import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { trimBufferHead, takeFlushBatch, applySummaryToState, isTransientError, shouldFlushStepEventImmediately, hasLegitimateSilence, AgentEventLayer, requestReconnectLadder, resetReconnectThrottleForTest, RECONNECT_SUMMARY_CONCURRENCY, STREAM_SILENCE_TIMEOUT_MS } from './event-layer'
import { AgentSession } from './agent-session'
import type { AgentSessionSummaryResp, Turn, Step } from '../../../gen-types/aigen'

const mockSubscribe = vi.fn()
const mockAgentStatus = vi.fn()

vi.mock('../../../application/generated-client', () => ({
  client: {
    getTransport: vi.fn(),
    ['transport']: {
      subscribe: (...args: unknown[]) => mockSubscribe(...args),
    },
  },
  waitForClientReady: vi.fn(async () => undefined),
}))

vi.mock('../../../gen-clients/local/client', () => ({
  agentStatus: (...args: unknown[]) => mockAgentStatus(...args),
}))

function makeTurn(overrides?: Partial<Turn>): Turn {
  return {
    Id: 't1',
    Role: 'assistant',
    State: 'completed',
    ...overrides,
  }
}

function makeSummary(overrides?: Partial<AgentSessionSummaryResp>): AgentSessionSummaryResp {
  return {
    Turns: [],
    Steps: [],
    ActiveTurn: {} as any,
    ActiveTurnEvents: [],
    TotalTurns: 0,
    HasMoreHistory: false,
    ...overrides,
  }
}

describe('trimBufferHead (AUDIT 2.1)', () => {
  it('keeps newest events when buffer exceeds cap', () => {
    const buffer = Array.from({ length: 201 }, (_, i) => i) // 0..200
    const dropped = trimBufferHead(buffer, 200)

    expect(dropped).toBe(81) // 201 - 120 (60% of 200)
    expect(buffer.length).toBe(120)
    // Newest 120 items preserved (indices 81..200)
    expect(buffer[0]).toBe(81)
    expect(buffer[buffer.length - 1]).toBe(200)
  })

  it('drops oldest items when growing well past cap', () => {
    const buffer = Array.from({ length: 500 }, (_, i) => i)
    const dropped = trimBufferHead(buffer, 200)

    expect(dropped).toBe(380)
    expect(buffer.length).toBe(120)
    // Newest 120 items: 380..499
    expect(buffer[0]).toBe(380)
    expect(buffer[buffer.length - 1]).toBe(499)
  })

  it('is a no-op when buffer is at or below cap', () => {
    const at = Array.from({ length: 200 }, (_, i) => i)
    expect(trimBufferHead(at, 200)).toBe(0)
    expect(at.length).toBe(200)
    expect(at[0]).toBe(0)
    expect(at[199]).toBe(199)

    const below = Array.from({ length: 50 }, (_, i) => i)
    expect(trimBufferHead(below, 200)).toBe(0)
    expect(below.length).toBe(50)
  })

  it('preserves the most recently pushed event (regression: AUDIT 2.1)', () => {
    // The bug: splice(keep, len-keep) deleted indices [keep..len-1] which
    // included the just-pushed newest event. Verify the newest survives.
    const buffer: number[] = []
    for (let i = 0; i < 250; i++) {
      buffer.push(i)
      trimBufferHead(buffer, 200)
    }
    expect(buffer[buffer.length - 1]).toBe(249)
    expect(buffer.length).toBeLessThanOrEqual(200)
  })
})

describe('takeFlushBatch', () => {
  it('leaves overflow for the next animation frame', () => {
    const buffer = Array.from({ length: 120 }, (_, i) => i)

    expect(takeFlushBatch(buffer, 50)).toEqual(Array.from({ length: 50 }, (_, i) => i))
    expect(buffer).toEqual(Array.from({ length: 70 }, (_, i) => i + 50))
  })
})

describe('interaction request flush boundary', () => {
  it('flushes step.interaction_requested immediately', () => {
    expect(shouldFlushStepEventImmediately({ Kind: 'step.interaction_requested' })).toBe(true)
  })

  it('keeps streaming and ordinary lifecycle events batched', () => {
    expect(shouldFlushStepEventImmediately({ Kind: 'block.delta' })).toBe(false)
    expect(shouldFlushStepEventImmediately({ Kind: 'step.opened' })).toBe(false)
    expect(shouldFlushStepEventImmediately({ Kind: 'step.closed' })).toBe(false)
  })
})

describe('isTransientError — cold-start race coverage', () => {
  // The cold-start race: gospore.events.subscribe_instance returns
  // "event X.Y not found" if the agent cell hasn't finished OnStart
  // (RegisterEventKind hasn't run yet). session.summary returns
  // "snapshot not ready" if takeSnapshot() hasn't completed. Both resolve
  // within milliseconds of agent startup; classifying them as persistent
  // caused permanent load failures on fast agent creation.

  it('classifies "not found" as transient (subscribe before agent OnStart)', () => {
    expect(isTransientError(
      'gospore.events.subscribe_instance: event step.step not found (actorId=abc)',
    )).toBe(true)
  })

  it('classifies "snapshot not ready" as transient (summary before first takeSnapshot)', () => {
    expect(isTransientError('agent: session snapshot not ready')).toBe(true)
  })

  it('still classifies transport errors as transient', () => {
    expect(isTransientError('websocket closed')).toBe(true)
    expect(isTransientError('websocket not connected')).toBe(true)
    expect(isTransientError('unexpected EOF')).toBe(true)
  })

  it('classifies request timeout as transient', () => {
    expect(isTransientError('invoke agent.session.summary timed out')).toBe(true)
  })

  it('classifies genuine persistent errors as non-transient', () => {
    expect(isTransientError('permission denied')).toBe(false)
    expect(isTransientError('policy denied: actor not authorized')).toBe(false)
    expect(isTransientError('malformed request payload')).toBe(false)
  })
})

describe('applySummaryToState — reconnect pruning', () => {
  // The bug: when turn.started arrived but turn.completed was lost in flight,
  // _activeTurnStates kept the entry as 'running' forever and isStreaming
  // never returned false. applySummaryToState now mirrors applyInitSummary's
  // cleanup pattern via pruneActiveTurnStates.

  it('prunes terminal turns from _activeTurnStates', async () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't-done' } as any)
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't-run' } as any)
    expect(session.isStreaming).toBe(true)

    await applySummaryToState(session, makeSummary({
      Turns: [
        makeTurn({ Id: 't-done', State: 'completed' }),
        makeTurn({ Id: 't-run', State: 'running' }),
      ],
    }))

    // t-done pruned (server says completed); t-run preserved (still running)
    const active = session.getActiveTurnStates()
    expect(active.has('t-done')).toBe(false)
    expect(active.has('t-run')).toBe(true)
    expect(session.isStreaming).toBe(true)
    session.release()
  })

  it('clears isStreaming when all locally-tracked turns are now terminal in summary', async () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1' } as any)
    expect(session.isStreaming).toBe(true)

    await applySummaryToState(session, makeSummary({
      Turns: [makeTurn({ Id: 't1', State: 'completed' })],
    }))

    expect(session.getActiveTurnStates().has('t1')).toBe(false)
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('seeds a missing running turn from ActiveTurn summary', async () => {
    const session = new AgentSession('a1')
    await applySummaryToState(session, makeSummary({
      ActiveTurn: {
        Turn: makeTurn({ Id: 't-active', State: 'running' }),
        StartedAt: '2026-01-01T00:00:00Z',
      } as any,
    }))
    expect(session.getActiveTurnStates().get('t-active')).toMatchObject({
      state: 'running',
      startedAt: '2026-01-01T00:00:00Z',
    })
    session.release()
  })

  it('no-ops _activeTurnStates when summary has no terminal turns', async () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1' } as any)

    await applySummaryToState(session, makeSummary({
      Turns: [makeTurn({ Id: 't1', State: 'running' })],
    }))

    expect(session.getActiveTurnStates().has('t1')).toBe(true)
    expect(session.getActiveTurnStates().get('t1')?.state).toBe('running')
    session.release()
  })

  it('prune runs before stale-close-steps (order matters for isStreaming recovery)', async () => {
    // A turn marked terminal in summary with a still-open step locally:
    // both prune AND stale-close-steps must fire. Verify both effects.
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1' } as any)
    session.setSteps([{
      Id: 's1',
      Role: 'assistant',
      Type: 'text',
      Content: [],
      Closed: false,
      Timestamp: new Date().toISOString(),
      TurnId: 't1',
      ContentStatus: 'appending',
      ExecutionStatus: 'idle',
      InteractionStatus: 'none',
    }])

    await applySummaryToState(session, makeSummary({
      Turns: [makeTurn({ Id: 't1', State: 'failed' })],
    }))

    expect(session.getActiveTurnStates().has('t1')).toBe(false)
    expect(session.isStreaming).toBe(false)
    expect(session.steps.every(s => s.Closed)).toBe(true)
    session.release()
  })
})

describe('applySummaryToState — no-op rung (same-content skip)', () => {
  it('keeps the old references and does not notify when summary content is unchanged', async () => {
    // The reconnect summary ladder re-fetches while the active turn is
    // genuinely still running; each rung previously rebuilt history
    // envelopes + steps arrays and notified unconditionally, invalidating
    // the projection cache and re-rendering the whole timeline — the
    // "stream refreshes itself every few minutes" symptom. A rung whose
    // content matches current state must be a true no-op.
    const session = new AgentSession('a1')

    const first = makeSummary({
      Turns: [
        makeTurn({ Id: 't1', State: 'completed' }),
        makeTurn({ Id: 't2', State: 'running' }),
      ],
      ActiveTurn: {
        Turn: makeTurn({ Id: 't2', State: 'running' }),
      } as any,
    })
    await applySummaryToState(session, first)
    const historyRef = session.historyEnvelopes
    const stepsRef = session.steps
    expect(historyRef.length).toBeGreaterThan(0)

    const notified = vi.fn()
    const unsub = session.subscribe(notified)
    notified.mockClear()

    // Second rung: same content (fresh objects, same values).
    await applySummaryToState(session, makeSummary({
      Turns: [
        makeTurn({ Id: 't1', State: 'completed' }),
        makeTurn({ Id: 't2', State: 'running' }),
      ],
      ActiveTurn: {
        Turn: makeTurn({ Id: 't2', State: 'running' }),
      } as any,
    }))

    expect(notified).not.toHaveBeenCalled()
    expect(session.historyEnvelopes).toBe(historyRef)
    expect(session.steps).toBe(stepsRef)

    // Third rung: the turn completed — must notify and refresh.
    await applySummaryToState(session, makeSummary({
      Turns: [
        makeTurn({ Id: 't1', State: 'completed' }),
        makeTurn({ Id: 't2', State: 'completed' }),
      ],
      ActiveTurn: {} as any,
    }))
    expect(notified).toHaveBeenCalled()
    expect(session.historyEnvelopes).not.toBe(historyRef)
    unsub()
    session.release()
  })
})

describe('applySummaryToState — revision convergence', () => {
  it('overwrites a stale running local event with a newer terminal summary record', async () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 1 } } as any)
    expect(session.isStreaming).toBe(true)

    await applySummaryToState(session, makeSummary({
      Turns: [makeTurn({ Id: 't1', State: 'completed', Revision: 5 })],
    }))

    expect(session.getActiveTurnStates().has('t1')).toBe(false)
    expect(session.isStreaming).toBe(false)
    session.release()
  })

  it('does not overwrite a newer local running event with an older summary record', async () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 5 } } as any)

    await applySummaryToState(session, makeSummary({
      Turns: [makeTurn({ Id: 't1', State: 'running', Revision: 1 })],
    }))

    expect(session.getActiveTurnStates().get('t1')?.state).toBe('running')
    expect(session.getActiveTurnStates().get('t1')?.revision).toBe(5)
    expect(session.isStreaming).toBe(true)
    session.release()
  })

  it('lets a newer paused summary record overwrite a running local event', async () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 1 } } as any)

    await applySummaryToState(session, makeSummary({
      Turns: [makeTurn({ Id: 't1', State: 'paused', Revision: 3, StartedAt: '2026-01-01T00:00:00Z' })],
    }))

    expect(session.getActiveTurnStates().get('t1')?.state).toBe('paused')
    expect(session.getActiveTurnStates().get('t1')?.revision).toBe(3)
    expect(session.isPaused).toBe(true)
    session.release()
  })

  it('retains legacy precedence when summary has no revision', async () => {
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1', Payload: { revision: 9 } } as any)

    // Summary without revision: legacy records always take precedence over live events.
    await applySummaryToState(session, makeSummary({
      Turns: [makeTurn({ Id: 't1', State: 'completed' })],
    }))

    expect(session.getActiveTurnStates().has('t1')).toBe(false)
    expect(session.isStreaming).toBe(false)
    session.release()
  })
})

describe('AgentEventLayer reconnect sinceSeqNo', () => {
  beforeEach(() => {
    mockSubscribe.mockReset()
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('first subscribe omits sinceSeqNo; reconnect resumes from last chunk seqNo', async () => {
    const session = new AgentSession('a1')
    const layer = new AgentEventLayer(session, () => {}, () => {})

    // First subscription: no prior seqNo.
    let firstReq: Record<string, unknown> | null = null
    mockSubscribe.mockImplementationOnce((_, req) => {
      firstReq = req as Record<string, unknown>
      return {
        async *[Symbol.asyncIterator]() {
          yield { seqNo: 1, payload: { Kind: 'step.opened', StepId: 's1', TurnId: 't1' } }
          yield { seqNo: 2, payload: { Kind: 'step.closed', StepId: 's1', TurnId: 't1' } }
        },
      }
    })

    const signal = new AbortController().signal
    const resetBackoff = vi.fn()
    const backoff = vi.fn(async () => undefined)
    const waitForClient = vi.fn(async () => undefined)

    await (layer as any)._step.onReconnect(waitForClient, signal, resetBackoff, backoff, session)

    expect(firstReq).not.toBeNull()
    expect(firstReq!.sinceSeqNo).toBeUndefined()

    // Simulate the createStreamLoop consuming events: lastSeqNo advances after
    // processing chunks (line 330). Manually set it to 2 as if seqNo 1 & 2
    // were received before the disconnect.
    ;(layer as any)._step.lastSeqNo = 2
    // Reset iter to null as createStreamLoop does before calling onReconnect.
    ;(layer as any)._step.iter = null
    let secondReq: Record<string, unknown> | null = null
    mockSubscribe.mockImplementationOnce((_, req) => {
      secondReq = req as Record<string, unknown>
      return {
        async *[Symbol.asyncIterator]() {
          // no further events before test ends
        },
      }
    })

    await (layer as any)._step.onReconnect(waitForClient, signal, resetBackoff, backoff, session)

    expect(secondReq).not.toBeNull()
    expect(secondReq!.sinceSeqNo).toBe(2)

    session.release()
  })

  it('toggles loading state around reconnect handshake', async () => {
    const session = new AgentSession('a1')
    const setLoadingSpy = vi.spyOn(session, 'setLoading')
    const layer = new AgentEventLayer(session, () => {}, () => {})

    mockSubscribe.mockImplementation(() => ({
      async *[Symbol.asyncIterator]() {
        // no events
      },
    }))

    const signal = new AbortController().signal
    const resetBackoff = vi.fn()
    const backoff = vi.fn(async () => undefined)
    const waitForClient = vi.fn(async () => undefined)

    await (layer as any)._step.onReconnect(waitForClient, signal, resetBackoff, backoff, session)

    expect(setLoadingSpy).toHaveBeenCalledWith(true)
    expect(setLoadingSpy).toHaveBeenLastCalledWith(false)

    session.release()
  })

  it('keeps a populated timeline mounted during reconnect (no loading swap)', async () => {
    // Regression: the reconnect handshake used to setLoading(true) uncondit-
    // ionally, which unmounts every envelope slot — scroll position and the
    // open ask-user form's local state — every time the silence watchdog or a
    // transport blip forced a reconnect ("stream flickers, selection resets").
    const session = new AgentSession('a1')
    session.setHistoryEnvelopes([{ id: 'e1', role: 'assistant', frames: [] } as any], false)
    const setLoadingSpy = vi.spyOn(session, 'setLoading')
    const layer = new AgentEventLayer(session, () => {}, () => {})

    mockSubscribe.mockImplementation(() => ({
      async *[Symbol.asyncIterator]() {
        // no events
      },
    }))

    const signal = new AbortController().signal
    const resetBackoff = vi.fn()
    const backoff = vi.fn(async () => undefined)
    const waitForClient = vi.fn(async () => undefined)

    await (layer as any)._step.onReconnect(waitForClient, signal, resetBackoff, backoff, session)

    expect(setLoadingSpy).not.toHaveBeenCalled()
    expect(session.getSnapshot().loading).toBe(false)

    session.release()
  })

  it('step-slot reconnect delegates to the bounded reconcile ladder (B2)', async () => {
    const session = new AgentSession('a1')
    const ladderSpy = vi.fn(async () => undefined)
    session.reconciler = { reconcileLadder: ladderSpy } as any
    const layer = new AgentEventLayer(session, () => {}, () => {})

    mockSubscribe.mockImplementation(() => ({
      async *[Symbol.asyncIterator]() {
        // no events
      },
    }))

    const signal = new AbortController().signal
    const resetBackoff = vi.fn()
    const backoff = vi.fn(async () => undefined)
    const waitForClient = vi.fn(async () => undefined)

    await (layer as any)._step.onReconnect(waitForClient, signal, resetBackoff, backoff, session)

    expect(ladderSpy).toHaveBeenCalledTimes(1)
    expect(ladderSpy).toHaveBeenCalledWith('reconnect')
    session.release()
  })
})

describe('AgentEventLayer — watchdog re-arm on session notify', () => {
  // The deaf-stream incident: a subscription dies silently while the agent is
  // idle (iter.next() never settles, no terminal frame). The consume loop is
  // parked in an un-timed race. When the user then submits, chat.submit seeds
  // a running turn (session notify) but the parked race was constructed with
  // the watchdog disabled — nothing ever re-armed it and the timeline froze
  // until an unrelated action resubscribed.
  beforeEach(() => {
    mockSubscribe.mockReset()
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  /** Iterator whose next() never settles — a silently-dead subscription. */
  const deadIterable = () => ({
    [Symbol.asyncIterator]: () => ({
      next: () => new Promise<IteratorResult<unknown>>(() => {}),
      return: async () => ({ value: undefined, done: true as const }),
    }),
  })

  it('arms the silence watchdog after a turn is seeded while parked idle', async () => {
    mockSubscribe.mockImplementation(() => deadIterable())

    const session = new AgentSession('a1')
    const layer = new AgentEventLayer(session, () => {}, () => {})
    layer.start()

    // Both slots subscribed once; the step loop parks in the un-timed race.
    await vi.advanceTimersByTimeAsync(10)
    expect(mockSubscribe).toHaveBeenCalledTimes(2)

    // User submits: chat.submit seeds a running turn → session notify →
    // the parked race reconstructs with the watchdog armed.
    session.seedActiveTurn('turn-1')
    await vi.advanceTimersByTimeAsync(0)

    // 45s of silence while streaming now forces a reconnect.
    await vi.advanceTimersByTimeAsync(STREAM_SILENCE_TIMEOUT_MS)
    await vi.advanceTimersByTimeAsync(0)
    expect(mockSubscribe.mock.calls.length).toBeGreaterThanOrEqual(3)

    layer.abort()
    session.release()
  })

  it('does not reconnect an idle silent stream (bounded-ladder regression guard)', async () => {
    mockSubscribe.mockImplementation(() => deadIterable())

    const session = new AgentSession('a2')
    const layer = new AgentEventLayer(session, () => {}, () => {})
    layer.start()

    // Idle silence across several watchdog budgets: no notify, no turn —
    // the stream must NOT be force-reconnected.
    await vi.advanceTimersByTimeAsync(3 * STREAM_SILENCE_TIMEOUT_MS)
    expect(mockSubscribe).toHaveBeenCalledTimes(2)

    layer.abort()
    session.release()
  })
})

describe('AgentEventLayer — watchdog legitimate-silence probe', () => {
  /** Iterator whose next() never settles — a silently-dead subscription. */
  const deadIterable = () => ({
    [Symbol.asyncIterator]: () => ({
      next: () => new Promise<IteratorResult<unknown>>(() => {}),
      return: async () => ({ value: undefined, done: true as const }),
    }),
  })

  beforeEach(() => {
    mockSubscribe.mockReset()
    mockAgentStatus.mockReset()
    mockSubscribe.mockImplementation(() => deadIterable())
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('hasLegitimateSilence: pending interaction, in-flight tool and execution count; closed/resolved do not', () => {
    const mk = (over: Partial<Step>) => ({
      Id: 's', Role: 'assistant', Type: 'text', Content: [], Closed: false,
      Timestamp: new Date().toISOString(), TurnId: 't1',
      ContentStatus: 'stable', ExecutionStatus: 'idle', InteractionStatus: 'none', Seq: 1, ...over,
    })
    const session = new AgentSession('a1')
    expect(hasLegitimateSilence({ steps: [mk({ InteractionStatus: 'pending' })] } as any)).toBe(true)
    expect(hasLegitimateSilence({ steps: [mk({ Type: 'tool_call' })] } as any)).toBe(true)
    expect(hasLegitimateSilence({ steps: [mk({ ExecutionStatus: 'in_progress' })] } as any)).toBe(true)
    expect(hasLegitimateSilence({ steps: [mk({})] } as any)).toBe(false)
    // Closed steps never count, whatever their status fields say.
    expect(hasLegitimateSilence({ steps: [mk({ Type: 'tool_call', Closed: true })] } as any)).toBe(false)
    expect(hasLegitimateSilence({ steps: [mk({ InteractionStatus: 'pending', Closed: true })] } as any)).toBe(false)
    expect(hasLegitimateSilence({ steps: [] } as any)).toBe(false)
    session.release()
  })

  it('pending ask_user silence probes the backend and keeps the stream when it answers', async () => {
    mockAgentStatus.mockResolvedValue({})
    const session = new AgentSession('a1')
    const layer = new AgentEventLayer(session, () => {}, () => {})
    layer.start()
    await vi.advanceTimersByTimeAsync(10)

    // Turn running + a pending interaction step: backend is parked waiting
    // for the user's answer — silence is legitimate.
    session.seedActiveTurn('turn-1')
    session.applyStepEvents([{ Kind: 'step.interaction_requested', StepId: 'ask-1', TurnId: 'turn-1', InteractionType: 'ask_user' } as any])
    await vi.advanceTimersByTimeAsync(0)

    // Several watchdog budgets of silence: probe answers each time, the
    // stream is never torn down (no resubscribe beyond the initial two).
    await vi.advanceTimersByTimeAsync(3 * STREAM_SILENCE_TIMEOUT_MS)
    expect(mockAgentStatus.mock.calls.length).toBeGreaterThanOrEqual(3)
    expect(mockAgentStatus.mock.calls[0]![1]).toMatchObject({ target: 'a1' })
    expect(mockSubscribe).toHaveBeenCalledTimes(2)

    layer.abort()
    session.release()
  })

  it('in-flight tool_call silence is probed too, and a failed probe still forces reconnect', async () => {
    mockAgentStatus.mockRejectedValue(new Error('backend gone'))
    const session = new AgentSession('a1')
    const layer = new AgentEventLayer(session, () => {}, () => {})
    layer.start()
    await vi.advanceTimersByTimeAsync(10)

    session.seedActiveTurn('turn-1')
    session.applyStepEvents([{ Kind: 'step.opened', StepId: 'tool-1', TurnId: 'turn-1', StepType: 'tool_call', Role: 'assistant' } as any])
    await vi.advanceTimersByTimeAsync(0)

    // Watchdog fires into the probe path; the probe rejects → forced
    // reconnect (step slot resubscribes).
    await vi.advanceTimersByTimeAsync(STREAM_SILENCE_TIMEOUT_MS)
    await vi.advanceTimersByTimeAsync(0)
    expect(mockAgentStatus).toHaveBeenCalled()
    expect(mockSubscribe.mock.calls.length).toBeGreaterThanOrEqual(3)

    layer.abort()
    session.release()
  })

  it('streaming silence with no legitimate state still reconnects without probing', async () => {
    const session = new AgentSession('a1')
    const layer = new AgentEventLayer(session, () => {}, () => {})
    layer.start()
    await vi.advanceTimersByTimeAsync(10)

    session.seedActiveTurn('turn-1')
    await vi.advanceTimersByTimeAsync(0)

    await vi.advanceTimersByTimeAsync(STREAM_SILENCE_TIMEOUT_MS)
    await vi.advanceTimersByTimeAsync(0)
    // No pending interaction / open tool: plain watchdog path, no probe.
    expect(mockAgentStatus).not.toHaveBeenCalled()
    expect(mockSubscribe.mock.calls.length).toBeGreaterThanOrEqual(3)

    layer.abort()
    session.release()
  })
})

describe('AgentEventLayer — turn.history_imported triggers reconcile', () => {
  it('re-fetches summary when a history_imported turn event arrives', () => {
    const session = new AgentSession('a1')
    const fetchSpy = vi.fn()
    session.reconciler = { fetch: fetchSpy } as any
    const layer = new AgentEventLayer(session, () => {}, () => {})

    const applyTurn = (layer as any)._turn.applyEvents as (batch: unknown[]) => void
    // A clone import emits only the control event — no live turn lifecycle.
    applyTurn([{ Kind: 'turn.history_imported' }])

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    expect(fetchSpy).toHaveBeenCalledWith('reconcile')
    session.release()
  })

  it('does not reconcile for ordinary turn lifecycle events', () => {
    const session = new AgentSession('a1')
    const fetchSpy = vi.fn()
    session.reconciler = { fetch: fetchSpy } as any
    const layer = new AgentEventLayer(session, () => {}, () => {})

    const applyTurn = (layer as any)._turn.applyEvents as (batch: unknown[]) => void
    applyTurn([{ Kind: 'turn.started', TurnId: 't1' }, { Kind: 'turn.completed', TurnId: 't1' }])

    expect(fetchSpy).not.toHaveBeenCalled()
    session.release()
  })

  it('does not reconcile once history is already displayed (avoids wiping scroll-loaded turns)', () => {
    const session = new AgentSession('a1')
    const fetchSpy = vi.fn()
    session.reconciler = { fetch: fetchSpy } as any
    // Simulate the recent turns already rendered (first chunk landed earlier).
    session.setHistoryEnvelopes([{ id: 'e1', role: 'assistant', frames: [] } as any])
    const layer = new AgentEventLayer(session, () => {}, () => {})

    const applyTurn = (layer as any)._turn.applyEvents as (batch: unknown[]) => void
    applyTurn([{ Kind: 'turn.history_imported' }])

    expect(fetchSpy).not.toHaveBeenCalled()
    session.release()
  })
})

describe('applySummaryToState — active-turn authoritative replacement', () => {
  function makeOpenStep(overrides?: Partial<Step>): Step {
    return {
      Id: 's1',
      Role: 'assistant',
      Type: 'text',
      Content: [],
      Closed: false,
      Timestamp: new Date().toISOString(),
      TurnId: 't-active',
      ContentStatus: 'appending',
      ExecutionStatus: 'idle',
      InteractionStatus: 'none',
      Seq: 1,
      ...overrides,
    }
  }

  it('replaces stale active-turn steps when the server EventSeq watermark is ahead', async () => {
    // Scenario: background→foreground / reconnect. The active turn's open step
    // accumulated deltas on the backend that the frontend never received; the
    // local EventSeq watermark (2) is behind the server's (10). The summary
    // snapshot carries the full content, which must replace the local partial.
    const session = new AgentSession('a1')
    session.setSteps([makeOpenStep({ Content: [{ Type: 'text', Text: 'partial' }] })])
    session.seqTracking.set('s1', 2)

    await applySummaryToState(session, makeSummary({
      ActiveTurn: {
        Turn: makeTurn({ Id: 't-active', State: 'running' }),
        EventSeq: 10,
      } as any,
      Steps: [makeOpenStep({ Content: [{ Type: 'text', Text: 'full accumulated content' }] })],
    }))

    const step = session.steps.find(s => s.Id === 's1')!
    expect(step.Content[0]!.Text).toBe('full accumulated content')
    // Watermark bumped so a subsequent OpenStepEvents replay skips events
    // already reflected in the snapshot (would otherwise double-append).
    expect(session.seqTracking.get('s1')).toBe(10)
    session.release()
  })

  it('keeps the local step when the local EventSeq watermark is not behind', async () => {
    // The frontend already saw newer deltas than the snapshot (live stream
    // raced ahead). Replacing would lose them; mergeSteps keeps the local copy.
    const session = new AgentSession('a1')
    session.setSteps([makeOpenStep({ Content: [{ Type: 'text', Text: 'local-newer' }] })])
    session.seqTracking.set('s1', 20)

    await applySummaryToState(session, makeSummary({
      ActiveTurn: {
        Turn: makeTurn({ Id: 't-active', State: 'running' }),
        EventSeq: 10,
      } as any,
      Steps: [makeOpenStep({ Content: [{ Type: 'text', Text: 'server-snapshot' }] })],
    }))

    const step = session.steps.find(s => s.Id === 's1')!
    expect(step.Content[0]!.Text).toBe('local-newer')
    session.release()
  })

  it('does not re-seed a running ActiveTurn the summary also reports terminal', async () => {
    // Snapshot race: ActiveTurn.State is stale 'running' but the turn already
    // landed in Turns as 'completed'. Without the guard, prune removes the
    // entry and seedActiveTurn immediately re-adds it running — the active
    // turn gets stuck after completing during a reconnect window.
    const session = new AgentSession('a1')
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't-race' } as any)
    expect(session.getActiveTurnStates().has('t-race')).toBe(true)

    await applySummaryToState(session, makeSummary({
      Turns: [makeTurn({ Id: 't-race', State: 'completed' })],
      ActiveTurn: { Turn: makeTurn({ Id: 't-race', State: 'running' }) } as any,
    }))

    expect(session.getActiveTurnStates().has('t-race')).toBe(false)
    expect(session.isStreaming).toBe(false)
    session.release()
  })
})

describe('reconnect summary ladder throttle', () => {
  beforeEach(() => {
    resetReconnectThrottleForTest()
  })
  afterEach(() => {
    resetReconnectThrottleForTest()
  })

  it('runs at most RECONNECT_SUMMARY_CONCURRENCY ladders concurrently and drains FIFO', async () => {
    const started: string[] = []
    const finished: string[] = []
    let maxInFlight = 0
    let inFlight = 0
    const gates: Array<() => void> = []
    const hold = async (id: string) => {
      started.push(id)
      inFlight++
      maxInFlight = Math.max(maxInFlight, inFlight)
      await new Promise<void>(r => gates.push(r))
      inFlight--
      finished.push(id)
    }

    const ids = ['a', 'b', 'c', 'd', 'e']
    const promises = ids.map(id => requestReconnectLadder(id, () => hold(id)))

    // First three run immediately (slots available); the rest queue.
    await Promise.resolve()
    expect(started).toEqual(['a', 'b', 'c'])
    expect(maxInFlight).toBeLessThanOrEqual(RECONNECT_SUMMARY_CONCURRENCY)

    // Free one slot → the next queued agent starts; cap never exceeded.
    gates[0]!()
    await vi.waitFor(() => expect(started).toEqual(['a', 'b', 'c', 'd']))
    expect(maxInFlight).toBeLessThanOrEqual(RECONNECT_SUMMARY_CONCURRENCY)

    gates[1]!(); gates[2]!(); gates[3]!()
    await vi.waitFor(() => expect(started).toEqual(['a', 'b', 'c', 'd', 'e']))
    gates[4]!()
    await Promise.all(promises)

    expect(finished.sort()).toEqual(ids)
    expect(maxInFlight).toBeLessThanOrEqual(RECONNECT_SUMMARY_CONCURRENCY)
  })

  it('merges a duplicate request for an already-queued agent into one slot (latest runner wins)', async () => {
    const ran: string[] = []
    const gates: Array<() => void> = []
    const hold = async (id: string) => {
      ran.push(id)
      await new Promise<void>(r => gates.push(r))
    }

    // Occupy all slots with blockers so 'a' can only queue.
    const blockers = ['x', 'y', 'z'].map(id => requestReconnectLadder(id, () => hold(id)))
    let gateA!: () => void
    const gateAPromise = new Promise<void>(r => { gateA = r })
    const p1 = requestReconnectLadder('a', async () => { ran.push('a-v1'); await gateAPromise })
    const p2 = requestReconnectLadder('a', async () => { ran.push('a-v2'); await gateAPromise })

    // Let the three blockers start (their gate resolvers land in `gates` as
    // microtasks), then release all slots so the merged 'a' request runs.
    await Promise.resolve()
    expect(gates).toHaveLength(3)
    gates.forEach(g => g())
    await vi.waitFor(() => expect(ran.length).toBe(4))
    // Exactly one runner fired for 'a' — the merge consumed a single slot.
    expect(ran.filter(r => r.startsWith('a')).length).toBe(1)
    expect(ran[3]).toBe('a-v2')

    gateA()
    await Promise.all([...blockers, p1, p2])
    expect(ran.filter(r => r.startsWith('a'))).toEqual(['a-v2'])
  })

  it('resolves immediately when the agent already has a ladder running', async () => {
    let release!: () => void
    const gate = new Promise<void>(r => { release = r })
    let runs = 0
    const p1 = requestReconnectLadder('a', async () => { runs++; await gate })
    // Second request while 'a' is running: covered by the in-flight ladder,
    // so it resolves immediately and never runs again.
    const p2 = requestReconnectLadder('a', () => { runs++; return Promise.resolve() })
    await Promise.resolve()
    expect(runs).toBe(1)
    release()
    await Promise.all([p1, p2])
    expect(runs).toBe(1)
  })
})

describe('createStreamLoop — silence watchdog', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mockSubscribe.mockReset()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('forces a reconnect (resubscribe with sinceSeqNo + reconcile) when the stream goes silent', async () => {
    const session = new AgentSession('a1')
    const ladderSpy = vi.fn(async () => undefined)
    session.reconciler = { reconcileLadder: ladderSpy } as any
    const layer = new AgentEventLayer(session, () => {}, () => {})

    // layer.start() spawns BOTH step and turn loops; mockSubscribe is shared,
    // so implement it by kind: turn streams park silently from the start, the
    // step stream delivers seqNo 42 once then dies without a terminal frame,
    // and the step reconnect subscription captures its request.
    const stepReqs: Record<string, unknown>[] = []
    const turnDead = () => ({
      [Symbol.asyncIterator]() {
        return {
          next: () => new Promise(() => {}),
          return: async () => ({ done: true as const, value: undefined }),
        }
      },
    })
    mockSubscribe.mockImplementation((_, req: Record<string, unknown>) => {
      if (req.kind === 'turn') return turnDead()
      if (stepReqs.length === 0) {
        stepReqs.push(req)
        return {
          [Symbol.asyncIterator]() {
            let first = true
            return {
              next: () => {
                if (first) {
                  first = false
                  return Promise.resolve({ done: false as const, value: { seqNo: 42, payload: { Kind: 'step.opened', StepId: 's1', TurnId: 't1' } } })
                }
                return new Promise(() => {})
              },
              return: async () => ({ done: true as const, value: undefined }),
            }
          },
        }
      }
      stepReqs.push(req)
      return turnDead()
    })

    // The watchdog only arms while a turn is streaming — mark t1 running so
    // the step slot's silence counts as a dead stream, not a healthy idle one.
    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1' } as any)

    layer.start()

    // Consume the first event (rAF flush needs a frame tick).
    await vi.advanceTimersByTimeAsync(0)

    // Watchdog fires after the silence budget elapses with no chunks. The
    // budget counts from the last observed activity — an applied event
    // batch notifies the session, which re-arms the race and restarts the
    // budget — so advance strictly past it.
    await vi.advanceTimersByTimeAsync(STREAM_SILENCE_TIMEOUT_MS + 5_000)

    // The forced reconnect resubscribed with the last seen seqNo and ran
    // the reconcile ladder to recover anything missed.
    expect(stepReqs.length).toBe(2)
    expect(stepReqs[1]!.sinceSeqNo).toBe(42)
    expect(ladderSpy).toHaveBeenCalledTimes(1)
    expect(ladderSpy).toHaveBeenCalledWith('reconnect')

    session.release()
  })

  it('does not arm the watchdog on an idle (non-streaming) silent stream', async () => {
    const session = new AgentSession('a1')
    const ladderSpy = vi.fn(async () => undefined)
    session.reconciler = { reconcileLadder: ladderSpy } as any
    const layer = new AgentEventLayer(session, () => {}, () => {})

    // Idle agent: a silent live stream is normal and must stay subscribed
    // indefinitely — far past the silence budget.
    mockSubscribe.mockImplementation(() => ({
      [Symbol.asyncIterator]() {
        return {
          next: () => new Promise(() => {}),
          return: async () => ({ done: true as const, value: undefined }),
        }
      },
    }))

    layer.start()
    await vi.advanceTimersByTimeAsync(300_000)

    expect(ladderSpy).not.toHaveBeenCalled()
    const stepSubscribes = mockSubscribe.mock.calls.filter(c => (c[1] as Record<string, unknown>).kind === 'step')
    expect(stepSubscribes.length).toBe(1)

    session.release()
  })

  it('does not trip the watchdog while a streaming turn keeps emitting heartbeats', async () => {
    const session = new AgentSession('a1')
    const ladderSpy = vi.fn(async () => undefined)
    session.reconciler = { reconcileLadder: ladderSpy } as any
    const layer = new AgentEventLayer(session, () => {}, () => {})

    // Streaming turn with a heartbeat every 10s — each arrival resets the
    // per-await silence race, so 45s must never elapse between awaits.
    // Bounded to 12 chunks so the fake clock's 120s advance ends the loop
    // deterministically without OOM.
    mockSubscribe.mockImplementation(() => ({
      [Symbol.asyncIterator]() {
        let seq = 1
        return {
          next: async () => {
            if (seq > 12) return new Promise(() => {})
            await new Promise(r => { setTimeout(r, 10_000) })
            const value = { done: false as const, value: { seqNo: seq, payload: { Kind: 'step.closed', StepId: `s${seq}`, TurnId: 't1' } } }
            seq++
            return value
          },
          return: async () => ({ done: true as const, value: undefined }),
        }
      },
    }))

    session.applyTurnEvent({ Kind: 'turn.started', TurnId: 't1' } as any)
    layer.start()
    await vi.advanceTimersByTimeAsync(120_000)

    expect(ladderSpy).not.toHaveBeenCalled()
    const stepSubscribes = mockSubscribe.mock.calls.filter(c => (c[1] as Record<string, unknown>).kind === 'step')
    expect(stepSubscribes.length).toBe(1)

    session.release()
  })
})
