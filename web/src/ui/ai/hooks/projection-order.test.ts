import { describe, expect, it } from 'vitest'
import { computeEnvelopes, sortEnvelopesBySeq } from './projection'
import type { Step } from '../../../gen-types/aigen'

const env = (id: string, turnSeq: number, timestamp: string, seq?: number, turnId = id) => ({
  id,
  role: id.startsWith('u') ? 'user' : 'assistant',
  frames: [],
  timestamp,
  completed: true,
  seq,
  turnSeq,
  metadata: { turnId },
} as any)

describe('timeline turn ordering', () => {
  it('sorts active steps by positive Seq and puts Seq=0 after them', () => {
    const legacy = env('a-legacy', 2, '2026-01-01T00:00:01Z', 0)
    const active = env('a-active', 2, '2026-01-01T00:00:02Z', 2)
    expect(sortEnvelopesBySeq(legacy, active)).toBeLessThan(0)
  })

  it('uses step Seq before timestamps within an active turn', () => {
    const earlier = env('a-earlier', 0, '2026-01-01T00:00:10Z', 3, 'active')
    const later = env('a-later', 0, '2026-01-01T00:00:01Z', 4, 'active')
    expect(sortEnvelopesBySeq(earlier, later)).toBeLessThan(0)
  })

  it('uses TurnOrder before timestamps', () => {
    const a = env('u1', 1, '2026-01-01T00:00:10Z')
    const b = env('a1', 2, '2026-01-01T00:00:01Z')
    expect(sortEnvelopesBySeq(a, b)).toBeLessThan(0)
  })

  it('puts envelopes with a known TurnOrder after unknown legacy envelopes', () => {
    const legacyUser = env('u-legacy', 0, '2026-01-01T00:00:00Z')
    const currentAssistant = env('a-current', 4, '2026-01-01T00:00:01Z')
    expect(sortEnvelopesBySeq(legacyUser, currentAssistant)).toBeLessThan(0)
    expect(sortEnvelopesBySeq(currentAssistant, legacyUser)).toBeGreaterThan(0)
  })

  it('creates a placeholder envelope from a seeded running turn', () => {
    const result = computeEnvelopes(
      'agent-1',
      [],
      [],
      undefined,
      new Map([['turn-1', { state: 'running', startedAt: '2026-01-01T00:00:00Z', turnOrder: 3 }]]),
    )
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      id: 'turn-1',
      role: 'assistant',
      completed: false,
      turnSeq: 3,
      metadata: { turnId: 'turn-1', turnState: 'running' },
    })
  })

  it('injects currentGoal into the active running turn envelope', () => {
    const goal = { Condition: 'implement goal display', MaxTurns: 10, TurnCount: 3 }
    const result = computeEnvelopes(
      'agent-1',
      [],
      [],
      undefined,
      new Map([['turn-1', { state: 'running', startedAt: '2026-01-01T00:00:00Z', turnOrder: 3 }]]),
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      goal,
    )
    expect(result).toHaveLength(1)
    expect(result[0]!.goal).toEqual(goal)
  })

  it('does not inject currentGoal into a completed turn envelope', () => {
    const goal = { Condition: 'implement goal display', MaxTurns: 10, TurnCount: 3 }
    const result = computeEnvelopes(
      'agent-1',
      [],
      [],
      undefined,
      new Map([['turn-1', {
        state: 'completed',
        startedAt: '2026-01-01T00:00:00Z',
        completedAt: '2026-01-01T00:00:03Z',
        turnOrder: 3,
      }]]),
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      goal,
    )
    expect(result).toHaveLength(1)
    expect(result[0]!.goal).toBeUndefined()
  })

  it('keeps a terminal placeholder until summary pruning removes the state', () => {
    const result = computeEnvelopes(
      'agent-1',
      [],
      [],
      undefined,
      new Map([['turn-1', {
        state: 'completed',
        startedAt: '2026-01-01T00:00:00Z',
        completedAt: '2026-01-01T00:00:03Z',
        turnOrder: 3,
      }]]),
    )
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      id: 'turn-1',
      role: 'assistant',
      completed: true,
      turnSeq: 3,
      metadata: { turnId: 'turn-1', turnState: 'completed' },
    })
  })

  it('removes the synthetic pending queue envelope after pending messages are confirmed', () => {
    const cacheRef = { current: null }
    const completedTurn = new Map([['turn-1', {
      state: 'completed' as const,
      startedAt: '2026-01-01T00:00:00Z',
      completedAt: '2026-01-01T00:00:03Z',
    }]])
    const queued = [{ clientId: 'pending-1', text: 'queued message', timestamp: '2026-01-01T00:00:02Z', state: 'pending' as const }]

    const withPending = computeEnvelopes('agent-1', [], [], undefined, completedTurn, undefined, queued, cacheRef)
    expect(withPending.find(envelope => envelope.id === '__pending_queue__')).toBeTruthy()

    const confirmed = computeEnvelopes('agent-1', [], [], undefined, completedTurn, undefined, [], cacheRef)
    expect(confirmed.find(envelope => envelope.id === '__pending_queue__')).toBeUndefined()
  })

  it('infers turnSeq for user envelopes from live step events (promotion path)', () => {
    // Scenario: a pending submit promoted to a new user turn via
    // handleTurnComplete. The user step arrives via live step events (no
    // history envelope yet), so its step-derived envelope has no turnSeq.
    // Without inference, sortEnvelopesBySeq pushes it before ALL envelopes
    // with turnSeq — it would appear at the TOP of the conversation instead
    // of between the preceding assistant turn and the new running turn.
    const steps: Step[] = [
      { Id: 'user-turn-3', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hello' } as any], Closed: true, Timestamp: '2026-01-01T00:00:05Z', TurnId: 'user-turn-3', Seq: 6 } as Step,
      { Id: 'turn-4-start-001', Role: 'assistant', Type: 'turn_start', Content: [], Closed: true, Timestamp: '2026-01-01T00:00:06Z', TurnId: 'turn-4', Seq: 7 } as Step,
    ]
    const historyEnvelopes = [
      env('u1', 1, '2026-01-01T00:00:00Z'),
      env('a1', 2, '2026-01-01T00:00:01Z'),
      env('u2', 3, '2026-01-01T00:00:02Z'),
      env('a2', 4, '2026-01-01T00:00:03Z'),
    ]
    const ats = new Map([['turn-4', { state: 'running' as const, startedAt: '2026-01-01T00:00:06Z', turnOrder: 5 }]])

    const result = computeEnvelopes('agent-1', steps, historyEnvelopes, undefined, ats)

    const userIdx = result.findIndex(e => e.id === 'user-turn-3')
    const asst2Idx = result.findIndex(e => e.id === 'a2')
    const turn4Idx = result.findIndex(e => e.id === 'turn-4' || e.metadata?.turnId === 'turn-4')

    // User envelope must exist and be positioned between a2 and turn-4
    expect(userIdx).toBeGreaterThan(-1)
    expect(userIdx).toBeGreaterThan(asst2Idx)
    expect(userIdx).toBeLessThan(turn4Idx)
  })

  it('places new live turns after history even without turnSeq', () => {
    const historyEnvelopes = [
      env('u1', 1, '2026-01-01T00:00:00Z'),
      env('a1', 2, '2026-01-01T00:00:01Z'),
      env('u2', 3, '2026-01-01T00:00:02Z'),
      env('a2', 4, '2026-01-01T00:00:03Z'),
    ]
    const steps: Step[] = [
      { Id: 'user-turn-3', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'third user' } as any], Closed: true, Timestamp: '2026-01-01T00:00:04Z', TurnId: 'turn-3', Seq: 7 } as Step,
      { Id: 'asst-turn-3-text', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'third answer' } as any], Closed: true, Timestamp: '2026-01-01T00:00:05Z', TurnId: 'turn-3', Seq: 8 } as Step,
    ]
    const result = computeEnvelopes('agent-1', steps, historyEnvelopes)
    const ids = result.map(e => e.id)
    expect(ids).toEqual(['u1', 'a1', 'u2', 'a2', 'user-turn-3', 'turn-3'])
  })
})
