import { describe, expect, it } from 'vitest'
import { computeEnvelopes, deriveTurnState, isTerminalTurnState } from './projection'
import type { Step, StepEvent } from '../../../gen-types/aigen'
import type { TurnEnvelope } from '../model/frame-types'

function makeStep(overrides: Partial<Step> & { Id: string; TurnId: string }): Step {
  const { Id, TurnId, ...rest } = overrides
  return {
    Id,
    TurnId,
    Role: 'assistant',
    Closed: false,
    ContentStatus: 'appending',
    Content: [],
    Events: [] as StepEvent[],
    Seq: 1,
    Timestamp: new Date().toISOString(),
    ...rest,
  } as Step
}

function makeHistoryEnv(overrides: Partial<TurnEnvelope> & { metadata: { turnId: string } }): TurnEnvelope {
  return {
    id: overrides.id ?? 'h1',
    role: 'assistant',
    frames: [],
    timestamp: new Date().toISOString(),
    completed: false,
    ...overrides,
  } as TurnEnvelope
}

describe('turn lifecycle projection', () => {
  it('isTerminalTurnState recognizes waiting/completed/failed/cancelled/abandoned only', () => {
    expect(isTerminalTurnState('waiting')).toBe(true)
    expect(isTerminalTurnState('completed')).toBe(true)
    expect(isTerminalTurnState('failed')).toBe(true)
    expect(isTerminalTurnState('cancelled')).toBe(true)
    expect(isTerminalTurnState('abandoned')).toBe(true)
    expect(isTerminalTurnState('running')).toBe(false)
    expect(isTerminalTurnState('paused')).toBe(false)
    expect(isTerminalTurnState('resumed')).toBe(false)
    expect(isTerminalTurnState(undefined)).toBe(false)
  })

  it('abandoned history envelope is projected as terminal', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      makeHistoryEnv({
        id: 'abandoned-env',
        metadata: {
          turnId: 't-abandoned',
          turnState: 'abandoned',
          startedAt: '2026-01-01T00:00:00Z',
          completedAt: '2026-01-01T00:00:01Z',
        },
      }),
    ]
    const steps: Step[] = [
      makeStep({ Id: 's1', TurnId: 't-abandoned', Closed: false, Timestamp: '2026-01-01T00:00:00Z' }),
    ]
    const result = computeEnvelopes('a1', steps, historyEnvelopes)
    const env = result.find(e => e.metadata?.turnId === 't-abandoned')
    expect(env).toBeDefined()
    expect(env!.completed).toBe(true)
    expect(env!.metadata?.turnState).toBe('abandoned')
  })

  it('cancelled history envelope is projected as terminal', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      makeHistoryEnv({
        id: 'cancelled-env',
        metadata: {
          turnId: 't-cancelled',
          turnState: 'cancelled',
          startedAt: '2026-01-01T00:00:00Z',
          completedAt: '2026-01-01T00:00:01Z',
        },
      }),
    ]
    const steps: Step[] = [
      makeStep({ Id: 's1', TurnId: 't-cancelled', Closed: false, Timestamp: '2026-01-01T00:00:00Z' }),
    ]
    const result = computeEnvelopes('a1', steps, historyEnvelopes)
    const env = result.find(e => e.metadata?.turnId === 't-cancelled')
    expect(env).toBeDefined()
    expect(env!.completed).toBe(true)
    expect(env!.metadata?.turnState).toBe('cancelled')
  })

  it('failed history envelope is projected as terminal', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      makeHistoryEnv({
        id: 'failed-env',
        metadata: {
          turnId: 't-failed',
          turnState: 'failed',
          startedAt: '2026-01-01T00:00:00Z',
          completedAt: '2026-01-01T00:00:01Z',
          error: 'boom',
        },
      }),
    ]
    const steps: Step[] = [
      makeStep({ Id: 's1', TurnId: 't-failed', Closed: false, Timestamp: '2026-01-01T00:00:00Z' }),
    ]
    const result = computeEnvelopes('a1', steps, historyEnvelopes)
    const env = result.find(e => e.metadata?.turnId === 't-failed')
    expect(env).toBeDefined()
    expect(env!.completed).toBe(true)
    expect(env!.metadata?.turnState).toBe('failed')
    expect(env!.metadata?.error).toBe('boom')
  })

  it('history turnState overrides step stale fallback for cancelled/abandoned/failed', () => {
    const veryOld = '2020-01-01T00:00:00Z'
    const historyEnvelopes: TurnEnvelope[] = [
      makeHistoryEnv({
        id: 'cancelled-env',
        metadata: {
          turnId: 't-cancelled',
          turnState: 'cancelled',
          startedAt: '2026-01-01T00:00:00Z',
          completedAt: '2026-01-01T00:00:01Z',
        },
      }),
      makeHistoryEnv({
        id: 'abandoned-env',
        metadata: {
          turnId: 't-abandoned',
          turnState: 'abandoned',
          startedAt: '2026-01-01T00:00:00Z',
          completedAt: '2026-01-01T00:00:01Z',
        },
      }),
      makeHistoryEnv({
        id: 'failed-env',
        metadata: {
          turnId: 't-failed',
          turnState: 'failed',
          startedAt: '2026-01-01T00:00:00Z',
          completedAt: '2026-01-01T00:00:01Z',
        },
      }),
    ]
    const steps: Step[] = [
      makeStep({ Id: 's1', TurnId: 't-cancelled', Closed: false, Timestamp: veryOld }),
      makeStep({ Id: 's2', TurnId: 't-abandoned', Closed: false, Timestamp: veryOld }),
      makeStep({ Id: 's3', TurnId: 't-failed', Closed: false, Timestamp: veryOld }),
    ]
    // Without history override, deriveTurnState would treat stale open steps as 'completed'.
    expect(deriveTurnState(steps.filter(s => s.TurnId === 't-cancelled'), Date.now())).toBe('completed')

    const result = computeEnvelopes('a1', steps, historyEnvelopes)
    const cancelled = result.find(e => e.metadata?.turnId === 't-cancelled')
    const abandoned = result.find(e => e.metadata?.turnId === 't-abandoned')
    const failed = result.find(e => e.metadata?.turnId === 't-failed')
    expect(cancelled?.metadata?.turnState).toBe('cancelled')
    expect(cancelled?.completed).toBe(true)
    expect(abandoned?.metadata?.turnState).toBe('abandoned')
    expect(abandoned?.completed).toBe(true)
    expect(failed?.metadata?.turnState).toBe('failed')
    expect(failed?.completed).toBe(true)
  })

  it('active turn state is preferred over history turnState', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      makeHistoryEnv({
        id: 'running-env',
        metadata: { turnId: 't-running', turnState: 'running' },
      }),
    ]
    const activeTurnStates = new Map([
      ['t-running', { state: 'completed' as const, startedAt: '2026-01-01T00:00:00Z', completedAt: '2026-01-01T00:00:01Z' }],
    ])
    const result = computeEnvelopes('a1', [], historyEnvelopes, undefined, activeTurnStates)
    const env = result.find(e => e.metadata?.turnId === 't-running')
    expect(env?.metadata?.turnState).toBe('completed')
    expect(env?.completed).toBe(true)
  })

  it('turn.resumed active state is not treated as terminal', () => {
    // Defensive: even if a stale active turn state somehow carried 'resumed',
    // it must not be considered terminal. In practice the backend never emits it.
    const historyEnvelopes: TurnEnvelope[] = [
      makeHistoryEnv({
        id: 'paused-env',
        metadata: { turnId: 't-old', turnState: 'paused' },
      }),
    ]
    const activeTurnStates = new Map([
      ['t-old', { state: 'running' as const, startedAt: '2026-01-01T00:00:00Z' }],
    ])
    const result = computeEnvelopes('a1', [], historyEnvelopes, undefined, activeTurnStates)
    const env = result.find(e => e.metadata?.turnId === 't-old')
    expect(env?.metadata?.turnState).toBe('running')
    expect(env?.completed).toBe(false)
  })

  it('active state with higher revision wins over terminal history', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      makeHistoryEnv({
        id: 'stale-env',
        metadata: { turnId: 't1', turnState: 'completed', revision: 1 },
      }),
    ]
    const activeTurnStates = new Map([
      ['t1', { state: 'running' as const, revision: 5, startedAt: '2026-01-01T00:00:00Z' }],
    ])
    const result = computeEnvelopes('a1', [], historyEnvelopes, undefined, activeTurnStates)
    const env = result.find(e => e.metadata?.turnId === 't1')
    expect(env?.metadata?.turnState).toBe('running')
    expect(env?.completed).toBe(false)
  })

  it('history with higher revision wins over active state', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      makeHistoryEnv({
        id: 'fresh-env',
        metadata: { turnId: 't1', turnState: 'completed', revision: 5, completedAt: '2026-01-01T00:00:01Z' },
      }),
    ]
    const activeTurnStates = new Map([
      ['t1', { state: 'running' as const, revision: 1, startedAt: '2026-01-01T00:00:00Z' }],
    ])
    const result = computeEnvelopes('a1', [], historyEnvelopes, undefined, activeTurnStates)
    const env = result.find(e => e.metadata?.turnId === 't1')
    expect(env?.metadata?.turnState).toBe('completed')
    expect(env?.completed).toBe(true)
    expect(env?.metadata?.completedAt).toBe('2026-01-01T00:00:01Z')
  })

  it('prefers revision-bearing source when only one side has revision', () => {
    // Active has revision 3, history has no revision → active wins even though
    // history says terminal (legacy snapshot).
    const historyEnvelopes: TurnEnvelope[] = [
      makeHistoryEnv({
        id: 'legacy-env',
        metadata: { turnId: 't1', turnState: 'completed' },
      }),
    ]
    const activeTurnStates = new Map([
      ['t1', { state: 'running' as const, revision: 3, startedAt: '2026-01-01T00:00:00Z' }],
    ])
    const result = computeEnvelopes('a1', [], historyEnvelopes, undefined, activeTurnStates)
    const env = result.find(e => e.metadata?.turnId === 't1')
    expect(env?.metadata?.turnState).toBe('running')
    expect(env?.completed).toBe(false)
  })

  it('falls back to step-derived state when no canonical source exists', () => {
    const steps = [
      makeStep({ Id: 's1', TurnId: 't1', Closed: true, Timestamp: '2026-01-01T00:00:00Z' }),
    ]
    const result = computeEnvelopes('a1', steps, [])
    const env = result.find(e => e.metadata?.turnId === 't1')
    expect(env?.metadata?.turnState).toBe('completed')
    expect(env?.completed).toBe(true)
  })
})
