import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mergeSteps, createTimelineManager, type AgentTimeline } from './timeline-manager'
import { computeEnvelopes, deriveTurnState, isTurnStale } from './projection'
import type { TurnEnvelope, LocalInteractionResponse } from '../model/frame-types'
import type { Step, StepEvent, TurnEvent, AgentSessionSummaryResp } from '../../../gen-types/aigen'
import { AgentSession } from './agent-session'
import { resetReconnectThrottleForTest, RECONNECT_SUMMARY_CONCURRENCY } from './event-layer'
import { client } from '../../../application/generated-client'
import * as agentSessionClient from '../../../gen-clients/local/client'

const { makeFakeTransport } = vi.hoisted(() => {
  const onConnectedHandlers: Array<(info: { isReconnect: boolean }) => void> = []
  function makeFakeTransport() {
    return {
      // Exposed so tests can fire the module-level reconnect handler that
      // timeline-manager registers on transport.onConnected at import time.
      _onConnectedHandlers: onConnectedHandlers,
      onConnected(h: (info: { isReconnect: boolean }) => void) {
        onConnectedHandlers.push(h)
        return () => {}
      },
    }
  }
  return { makeFakeTransport }
})

vi.mock('../../../application/generated-client', () => ({
  client: {
    transport: {
      subscribe: vi.fn(() => ({
        [Symbol.asyncIterator]() {
          return {
            // Never resolve: a real subscription stays open. Returning done
            // immediately would make the consume loop spin forever.
            next: async () => new Promise(() => {}),
            return: async () => ({ done: true as const, value: undefined }),
          }
        },
      })),
    },
    getTransport: () => makeFakeTransport(),
  },
  waitForClientReady: vi.fn(async () => undefined),
}))

vi.mock('../../../application/backend-ready', () => ({
  waitForBackendReady: vi.fn(async () => undefined),
}))

vi.mock('../../../gen-clients/local/client', () => ({
  sessionSummary: vi.fn(async () => ({
    Turns: [],
    Steps: [],
    HasMoreHistory: false,
    ActiveTurn: null,
  })),
  turnsList: vi.fn(async () => ({ Turns: [], HasMore: false })),
  turnCancel: vi.fn(async () => undefined),
  // loadTimeline seeds the β channel (current executing unit) from
  // agentStatus; without it select() throws before the initial summary fetch.
  agentStatus: vi.fn(async () => ({})),
}))

type MakeTimelineOverrides = Partial<AgentTimeline> & { steps?: Step[]; historyEnvelopes?: TurnEnvelope[] }

function makeTimeline(overrides?: MakeTimelineOverrides): AgentTimeline {
  const layer = new AgentSession('test-agent')
  if (overrides?.steps) layer.setSteps(overrides.steps)
  if (overrides?.historyEnvelopes) layer.setHistoryEnvelopes(overrides.historyEnvelopes)

  const { steps: _s, historyEnvelopes: _h, ...rest } = overrides ?? {}

  return {
    agentActorId: 'test-agent',
    layer,
    cancelLoad: null,
    eventLayer: null,
    loadingGeneration: 0,
    realLoading: false,
    loadingTimeoutId: null,
    ...rest,
  }
}

describe('computeEnvelopes', () => {
  it('returns empty list for empty timeline', () => {
    const tl = makeTimeline()
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)
    expect(result).toHaveLength(0)
  })
  it('does not mark a paused historical turn as completed', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      {
        id: 'paused-turn',
        role: 'assistant',
        frames: [],
        timestamp: '2026-01-01T10:00:00Z',
        metadata: { turnId: 'paused-turn', turnState: 'paused' },
      },
    ]
    const result = computeEnvelopes('agent', [], historyEnvelopes)
    expect(result).toHaveLength(1)
    expect(result[0]!.completed).toBe(false)
  })

  it('hides historical turns whose steps have been discarded', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'user-turn', role: 'user', frames: [], timestamp: '2026-01-01T10:00:00Z', metadata: { turnId: 'user-turn' } },
    ]
    const steps: Step[] = [
      { Id: 'discarded-step', Role: 'user', Type: 'text', TurnId: 'user-turn', Discarded: true, Closed: true, Content: [] },
    ]
    const result = computeEnvelopes('agent', steps, historyEnvelopes)
    expect(result.some(e => e.id === 'user-turn' && e.role === 'user')).toBe(false)
  })

  it('hides both user and assistant history envelopes for a fully discarded turn group', () => {
    // Mirrors the real discard flow: discardTurnGroup removes a user+assistant
    // pair, marking all their steps Discarded=true. The stale history envelopes
    // loaded before discard must not reappear.
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u-old', role: 'user', frames: [], timestamp: '2026-01-01T09:00:00Z', metadata: { turnId: 'u-old' } },
      { id: 'a-old', role: 'assistant', frames: [], timestamp: '2026-01-01T09:01:00Z', metadata: { turnId: 'a-old' } },
      { id: 'u-live', role: 'user', frames: [], timestamp: '2026-01-01T10:00:00Z', metadata: { turnId: 'u-live' } },
    ]
    const steps: Step[] = [
      { Id: 's-u-old', Role: 'user', Type: 'text', TurnId: 'u-old', Discarded: true, Closed: true, Content: [] },
      { Id: 's-a-old', Role: 'assistant', Type: 'text', TurnId: 'a-old', Discarded: true, Closed: true, Content: [] },
      { Id: 's-u-live', Role: 'user', Type: 'text', TurnId: 'u-live', Closed: true, Content: [{ Type: 'text', Text: 'hi' }] },
    ]
    const result = computeEnvelopes('agent', steps, historyEnvelopes)
    const ids = result.map(e => e.id)
    expect(ids).not.toContain('u-old')
    expect(ids).not.toContain('a-old')
    expect(ids).toContain('s-u-live')
  })
  it('merges history and steps without overlap', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u1', role: 'user', frames: [], timestamp: '1' },
      { id: 'a1', role: 'assistant', frames: [], timestamp: '2', completed: true, metadata: { turnId: 't1' } },
    ]
    const steps: Step[] = [
      { Id: 'u2', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q2' }], Closed: true, Timestamp: '3', TurnId: 't2' },
      { Id: 'a2', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'A2' }], Closed: true, Timestamp: '4', TurnId: 't2' },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    expect(result).toHaveLength(4)
    // stepsToEnvelopes uses TurnId as assistant envelope id (unless it collides with a user id)
    expect(result.map(e => e.id)).toEqual(['u1', 'a1', 'u2', 't2'])
  })

  it('keeps the user envelope before the assistant envelope within one turn', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'assistant', role: 'assistant', frames: [], timestamp: '', turnSeq: 4, metadata: { turnId: 't1' } },
      { id: 'user', role: 'user', frames: [], timestamp: '', turnSeq: 4, metadata: { turnId: 't1' } },
    ]
    const result = computeEnvelopes('agent', [], historyEnvelopes)
    expect(result.map(e => e.role)).toEqual(['user', 'assistant'])
  })

  it('orders historical turns by Turn.Seq instead of Step.Seq', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'old', role: 'assistant', frames: [], timestamp: '2026-01-01T11:00:00Z', turnSeq: 1, metadata: { turnId: 'old' } },
      { id: 'new', role: 'assistant', frames: [], timestamp: '2026-01-01T10:00:00Z', turnSeq: 2, metadata: { turnId: 'new' } },
    ]
    const result = computeEnvelopes('agent', [], historyEnvelopes)
    expect(result.map(e => e.metadata?.turnId)).toEqual(['old', 'new'])
  })

  it('keeps a live envelope without Seq after historical envelopes with Seq', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u1', role: 'user', frames: [], timestamp: '2026-01-01T10:00:00Z', seq: 1, metadata: { turnId: 't1' } },
      { id: 'a1', role: 'assistant', frames: [], timestamp: '2026-01-01T10:00:01Z', seq: 2, completed: true, metadata: { turnId: 't1' } },
    ]
    const steps: Step[] = [
      { Id: 'u2', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'current question' }], Closed: true, Timestamp: '2026-01-01T10:01:00Z', TurnId: 't2' },
      { Id: 'a2', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'current response' }], Closed: false, Timestamp: '2026-01-01T10:01:01Z', TurnId: 't2' },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)
    expect(result.map(e => e.metadata?.turnId)).toEqual(['t1', 't1', 't2', 't2'])
  })

  it('filters out history envelopes for active turns (steps take precedence)', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u1', role: 'user', frames: [], timestamp: '1' },
      { id: 'a1', role: 'assistant', frames: [{ id: 'f-old', type: 'text', status: 'completed', content: 'old' }], timestamp: '2', completed: true, metadata: { turnId: 't1' } },
    ]
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q1' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'a1-new', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'new' }], Closed: false, Timestamp: '2', TurnId: 't1' },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    // Only one user + one assistant for turn t1 (history filtered, steps used)
    expect(result).toHaveLength(2)
    const asst = result.find(e => e.role === 'assistant')
    expect(asst).toBeDefined()
    expect(asst!.frames).toHaveLength(1)
    expect((asst!.frames[0] as import('../model/frame-types').TextFrame).content).toBe('new')
  })

  it('preserves TURN_ONLY frames from history onto active step envelopes', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      {
        id: 'a1', role: 'assistant',
        frames: [
          { id: 'f1', type: 'text', status: 'completed', content: 'step text' },
          { id: 'src1', type: 'sources', status: 'completed', entries: [{ title: 'ref' }] },
        ],
        timestamp: '1', completed: true, metadata: { turnId: 't1' },
      },
    ]
    const steps: Step[] = [
      { Id: 'a1-step', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'new text' }], Closed: false, Timestamp: '2', TurnId: 't1' },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    const asst = result.find(e => e.role === 'assistant')
    expect(asst).toBeDefined()
    // Step-derived frame first, then preserved sources frame
    expect(asst!.frames).toHaveLength(2)
    expect(asst!.frames[0]).toMatchObject({ type: 'text', content: 'new text' })
    expect(asst!.frames[1]).toMatchObject({ type: 'sources' })
  })

  it('preserves completed=true from history when steps have unclosed steps', () => {
    // Scenario: a completed turn persisted with an unclosed reasoning step
    // (backend bug). History says completed, but step-derived says not completed.
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'a1', role: 'assistant', frames: [], timestamp: '2', completed: true, metadata: { turnId: 't1' } },
    ]
    const steps: Step[] = [
      { Id: 'r1', Role: 'assistant', Type: 'reasoning', Content: [], ReasoningContent: 'thinking...', Closed: false, Timestamp: '2', TurnId: 't1' },
      { Id: 'llm1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'response' }], Closed: true, Timestamp: '3', TurnId: 't1' },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    const asst = result.find(e => e.role === 'assistant')
    expect(asst).toBeDefined()
    // History says completed: true, so the enriched envelope must be completed
    // even though one step has Closed: false.
    expect(asst!.completed).toBe(true)
  })

  it('dedupes duplicate envelopes by id', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u1', role: 'user', frames: [], timestamp: '1' },
      { id: 'a1', role: 'assistant', frames: [{ id: 'f1', type: 'text', status: 'completed', content: 'old' }], timestamp: '2', metadata: { turnId: 't1' } },
    ]
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q1' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'new' }], Closed: false, Timestamp: '2', TurnId: 't1' },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    // Without dedupe we'd have duplicate u1 and a1.
    // With dedupe, the last occurrence wins.
    const ids = result.map(e => e.id)
    expect(new Set(ids).size).toBe(ids.length) // no duplicates
    expect(result).toHaveLength(2)
    const asst = result.find(e => e.role === 'assistant')
    expect((asst!.frames[0] as import('../model/frame-types').TextFrame).content).toBe('new')
  })

  it('preserves causal order when MsgIdx is inconsistent (user idx > assistant idx)', () => {
    // Simulates real-world data corruption where user StartMsgIdx was assigned
    // AFTER the assistant turn's range due to backend NextIdx desync.
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u1', role: 'user', frames: [], timestamp: '2026-01-01T10:00:00Z', metadata: { turnId: 'u1' } },
      { id: 'a1', role: 'assistant', frames: [], timestamp: '2026-01-01T10:00:00Z', completed: true, idx: 1, metadata: { turnId: 'a1' } },
      { id: 'u2', role: 'user', frames: [], timestamp: '2026-01-01T10:05:00Z', idx: 193, metadata: { turnId: 'u2' } },
      { id: 'a2', role: 'assistant', frames: [], timestamp: '2026-01-01T10:05:00Z', completed: true, idx: 162, metadata: { turnId: 'a2' } },
    ]
    const tl = makeTimeline({ historyEnvelopes })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    // u2 (idx=193) must still appear before a2 (idx=162) despite idx inversion.
    const roles = result.map(e => e.role)
    expect(roles).toEqual(['user', 'assistant', 'user', 'assistant'])
  })

  it('keeps compaction frame inside its owning turn even when timestamp would sort it later', () => {
    // Backend now anchors compaction steps to their turn's end timestamp so
    // frontend timestamp sorting cannot float them to the timeline end.
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q1' }], Closed: true, Timestamp: '2026-06-14T09:59:00Z', TurnId: 'turn-1' },
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'A1' }], Closed: true, Timestamp: '2026-06-14T09:59:30Z', TurnId: 'turn-1' },
      { Id: 'c1', Role: 'system', Type: 'text', Content: [{ Type: 'compaction', Text: '{"type":"compaction","status":"completed","beforeTokens":1000,"afterTokens":500,"contextWindowSize":8000}' }], Closed: true, Timestamp: '2026-06-14T09:59:35Z', TurnId: 'turn-1' },
      { Id: 'u2', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q2' }], Closed: true, Timestamp: '2026-06-14T09:59:45Z', TurnId: 'turn-2' },
      { Id: 'a2', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'A2' }], Closed: true, Timestamp: '2026-06-14T09:59:50Z', TurnId: 'turn-2' },
    ]
    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    expect(result).toHaveLength(4)
    expect(result.map(e => e.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
    expect(result[1]!.metadata?.turnId).toBe('turn-1')
    expect(result[1]!.frames).toHaveLength(2)
    expect(result[1]!.frames[0]).toMatchObject({ type: 'text', content: 'A1' })
    expect(result[1]!.frames[1]).toMatchObject({ type: 'compaction' })
  })

  it('does not float compaction frame to the end when its raw timestamp is after the next turn', () => {
    // Simulates a legacy compaction event whose CreatedAt drifted after the
    // next turn. Without the backend timestamp anchor this would sort last.
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q1' }], Closed: true, Timestamp: '2026-06-14T09:59:00Z', TurnId: 'turn-1' },
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'A1' }], Closed: true, Timestamp: '2026-06-14T09:59:30Z', TurnId: 'turn-1' },
      { Id: 'u2', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q2' }], Closed: true, Timestamp: '2026-06-14T09:59:45Z', TurnId: 'turn-2' },
      { Id: 'a2', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'A2' }], Closed: true, Timestamp: '2026-06-14T09:59:50Z', TurnId: 'turn-2' },
      { Id: 'c1', Role: 'system', Type: 'text', Content: [{ Type: 'compaction', Text: '{"type":"compaction","status":"completed","beforeTokens":1000,"afterTokens":500,"contextWindowSize":8000}' }], Closed: true, Timestamp: '2026-06-14T10:05:00Z', TurnId: 'turn-1' },
    ]
    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    // With only timestamp sorting this would place c1 last. The test documents
    // current behavior; the backend fix prevents this data shape from reaching
    // the frontend.
    const lastAsst = result.filter(e => e.role === 'assistant').pop()
    expect(lastAsst?.metadata?.turnId).toBe('turn-2')
  })

  it('returns the same array reference when inputs are unchanged', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q1' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'A1' }], Closed: true, Timestamp: '2', TurnId: 't1' },
    ]
    const tl = makeTimeline({ steps })

    const first = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)
    const second = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    expect(first).toStrictEqual(second)
  })

  it('returns a different array reference when steps change', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q1' }], Closed: true, Timestamp: '1', TurnId: 't1' },
    ]
    const tl = makeTimeline({ steps })

    const first = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)
    tl.layer.setSteps([...steps, { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'A1' }], Closed: true, Timestamp: '2', TurnId: 't1' }])
    const second = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    expect(first).not.toBe(second)
  })

  it('has independent caches per timeline', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q1' }], Closed: true, Timestamp: '1', TurnId: 't1' },
    ]
    const tlA = makeTimeline({ agentActorId: 'agent-a', steps })
    const tlB = makeTimeline({ agentActorId: 'agent-b', steps })


    const resultA = computeEnvelopes(tlA.agentActorId, tlA.layer.steps, tlA.layer.historyEnvelopes)
    const resultB = computeEnvelopes(tlB.agentActorId, tlB.layer.steps, tlB.layer.historyEnvelopes)

    expect(resultA).not.toBe(resultB)
  })

  it('keeps active turn running when step-derived envelope would be completed', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'active-turn', role: 'assistant', frames: [{ id: 'hist-text', type: 'text', status: 'completed', content: 'history text' }], timestamp: '2026-01-01T00:00:00Z', completed: false, metadata: { turnId: 'active-turn', actionCount: 2, usage: { totalTokens: 100 } } },
    ]
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 'active-turn' },
      { Id: 'llm1', Role: 'assistant', Type: 'llm_call', Content: [{ Type: 'text', Text: 'hello' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 'active-turn', Seq: 1 },
      { Id: 'tool1', Role: 'assistant', Type: 'tool_call', Content: [{ Type: 'tool_use', ToolUseId: 't1', ToolName: 'fs.read', Input: '{}' }, { Type: 'tool_result', ToolUseId: 't1', Text: 'ok' }], Closed: true, Timestamp: '2026-01-01T00:00:02Z', TurnId: 'active-turn', Seq: 2 },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 'active-turn')
    expect(asst).toBeDefined()
    expect(asst!.completed).toBe(false)
    expect(asst!.frames.some(f => f.type === 'text' && (f as any).content === 'hello')).toBe(true)
    expect(asst!.metadata?.actionCount).toBe(2)
  })

  it('uses turn.started event to keep envelope running even when all steps are closed', () => {
    // Bug scenario: between dispatch iterations all known steps are closed,
    // but turn is still active. Without turn.started event, the envelope
    // would briefly flip to completed — causing TurnTail to flicker.
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1' },
      { Id: 'llm1', Role: 'assistant', Type: 'llm_call', Content: [{ Type: 'text', Text: 'hello' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1', Seq: 1 },
    ]
    const tl = makeTimeline({ steps })
    const activeTurnStates = new Map([
      ['t1', { state: 'running' as const, startedAt: '2026-01-01T00:00:00Z' }],
    ])
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      activeTurnStates,
    )
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 't1')
    expect(asst).toBeDefined()
    expect(asst!.completed).toBe(false)
    expect(asst!.metadata?.turnState).toBe('running')
    expect(asst!.metadata?.startedAt).toBe('2026-01-01T00:00:00Z')
  })

  it('backfills turnSeq from activeTurnState so active step envelope does not sort to top', () => {
    // Bug scenario: step events for the active turn arrive before turn.started
    // (which carries TurnOrder). The step-derived envelope has no turnSeq and
    // would fall back to timestamp sorting. With an older timestamp it floats to
    // the top of history; the fix backfills turnSeq from activeTurnState.
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u1', role: 'user', frames: [], timestamp: '2026-01-01T00:00:00Z', turnSeq: 1, metadata: { turnId: 'u1' } },
      { id: 'a1', role: 'assistant', frames: [], timestamp: '2026-01-01T00:00:01Z', turnSeq: 2, completed: true, metadata: { turnId: 'a1' } },
      { id: 'u2', role: 'user', frames: [], timestamp: '2026-01-01T00:00:02Z', turnSeq: 3, metadata: { turnId: 'u2' } },
    ]
    const steps: Step[] = [
      { Id: 'active-step', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'responding' }], Closed: false, Timestamp: '2026-01-01T00:00:00Z', TurnId: 'active-turn' },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const activeTurnStates = new Map([
      ['active-turn', { state: 'running' as const, startedAt: '2026-01-01T00:00:03Z', turnOrder: 4 }],
    ])
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      activeTurnStates,
    )
    const idxU2 = result.findIndex(e => e.metadata?.turnId === 'u2')
    const idxActive = result.findIndex(e => e.metadata?.turnId === 'active-turn')
    expect(idxU2).toBeGreaterThan(-1)
    expect(idxActive).toBeGreaterThan(-1)
    expect(idxActive).toBeGreaterThan(idxU2)
    const active = result[idxActive]!
    expect(active.turnSeq).toBe(4)
    expect(active.metadata?.turnState).toBe('running')
  })

  it('uses turn.completed event to mark envelope complete with timestamps', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1' },
      { Id: 'llm1', Role: 'assistant', Type: 'llm_call', Content: [{ Type: 'text', Text: 'hello' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1', Seq: 1 },
    ]
    const tl = makeTimeline({ steps })
    const activeTurnStates = new Map([
      ['t1', {
        state: 'completed' as const,
        startedAt: '2026-01-01T00:00:00Z',
        completedAt: '2026-01-01T00:00:05Z',
      }],
    ])
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      activeTurnStates,
    )
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 't1')
    expect(asst).toBeDefined()
    expect(asst!.completed).toBe(true)
    expect(asst!.metadata?.turnState).toBe('completed')
    expect(asst!.metadata?.startedAt).toBe('2026-01-01T00:00:00Z')
    expect(asst!.metadata?.completedAt).toBe('2026-01-01T00:00:05Z')
  })

  it('uses turn.cancelled event to mark envelope with cancelled state', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1' },
      { Id: 'llm1', Role: 'assistant', Type: 'llm_call', Content: [{ Type: 'text', Text: 'partial' }], Closed: false, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1', Seq: 1 },
    ]
    const tl = makeTimeline({ steps })
    const activeTurnStates = new Map([
      ['t1', {
        state: 'cancelled' as const,
        startedAt: '2026-01-01T00:00:00Z',
        completedAt: '2026-01-01T00:00:03Z',
      }],
    ])
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      activeTurnStates,
    )
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 't1')
    expect(asst).toBeDefined()
    expect(asst!.completed).toBe(true)
    expect(asst!.metadata?.turnState).toBe('cancelled')
  })

  it('overlays crash-recovery running state onto history-only paused envelope', () => {
    // After crash-recovery resume the old turn often has no live steps and only
    // exists as a history envelope. activeTurnStates must flip it back to
    // 'running' so TurnTail re-activates the envelope immediately.
    const historyEnvelopes: TurnEnvelope[] = [
      {
        id: 'old-paused',
        role: 'assistant',
        frames: [{ id: 'hist-text', type: 'text', status: 'completed', content: 'partial before crash' }],
        timestamp: '2026-01-01T00:00:00Z',
        completed: false,
        metadata: {
          turnId: 'old-paused',
          turnState: 'paused',
          startedAt: '2026-01-01T00:00:00Z',
        },
      },
    ]
    const activeTurnStates = new Map([
      ['old-paused', {
        state: 'running' as const,
        startedAt: '2026-01-01T00:00:00Z',
      }],
    ])
    const result = computeEnvelopes(
      'a1',
      [],
      historyEnvelopes,
      undefined,
      activeTurnStates,
    )
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 'old-paused')
    expect(asst).toBeDefined()
    expect(asst!.completed).toBe(false)
    expect(asst!.metadata?.turnState).toBe('running')
    expect(asst!.metadata?.startedAt).toBe('2026-01-01T00:00:00Z')
  })

  it('does not let old paused turn tasks leak into new turn after crash recovery', () => {
    // Regression: after pause -> app restart -> new message, the backend
    // cancels the paused turn and snapshots its tasks there. The new running
    // turn must not inherit those tasks.
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'turn-1', role: 'user', frames: [], timestamp: '2026-01-01T00:00:00Z', turnSeq: 1, metadata: { turnId: 'turn-1' } },
      {
        id: 'turn-2',
        role: 'assistant',
        frames: [],
        timestamp: '2026-01-01T00:00:01Z',
        turnSeq: 2,
        completed: true,
        metadata: { turnId: 'turn-2', turnState: 'cancelled' },
        tasks: [{ id: 'task-1', subject: 'old task', status: 'completed' }],
      },
      {
        id: 'turn-3',
        role: 'assistant',
        frames: [],
        timestamp: '2026-01-01T00:00:02Z',
        turnSeq: 3,
        completed: false,
        metadata: { turnId: 'turn-3', turnState: 'running' },
      },
    ]
    const steps: Step[] = [
      { Id: 's3', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'new turn' }], Closed: false, Timestamp: '2026-01-01T00:00:02Z', TurnId: 'turn-3', Seq: 1 },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const activeTurnStates = new Map([
      ['turn-3', { state: 'running' as const, startedAt: '2026-01-01T00:00:02Z', turnOrder: 3 }],
    ])
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      activeTurnStates,
    )
    const oldTurn = result.find(e => e.metadata?.turnId === 'turn-2')
    const newTurn = result.find(e => e.metadata?.turnId === 'turn-3')
    expect(oldTurn?.tasks).toHaveLength(1)
    expect(newTurn?.tasks).toBeUndefined()
    expect(newTurn?.turnSeq).toBe(3)
  })

  it('does not treat an active paused turn as completed', () => {
    // A user-paused turn is still live; it should keep completed=false so
    // TurnTail renders the Paused title instead of a terminal Success badge.
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1' },
      { Id: 'llm1', Role: 'assistant', Type: 'llm_call', Content: [{ Type: 'text', Text: 'hello' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1', Seq: 1 },
    ]
    const activeTurnStates = new Map([
      ['t1', { state: 'paused' as const, startedAt: '2026-01-01T00:00:00Z' }],
    ])
    const result = computeEnvelopes(
      'a1',
      steps,
      [],
      undefined,
      activeTurnStates,
    )
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 't1')
    expect(asst).toBeDefined()
    expect(asst!.completed).toBe(false)
    expect(asst!.metadata?.turnState).toBe('paused')
  })

  it('falls back to step-derived state when no turn event has arrived', () => {
    // Legacy / pre-event race scenario. deriveTurnState rules apply.
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1' },
      { Id: 'llm1', Role: 'assistant', Type: 'llm_call', Content: [{ Type: 'text', Text: 'hello' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1', Seq: 1 },
    ]
    const tl = makeTimeline({ steps })
    // Empty activeTurnStates — same as a turn that started before this code shipped.
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      new Map(),
    )
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 't1')
    expect(asst).toBeDefined()
    // All steps closed → deriveTurnState says 'completed' → envelope completed.
    expect(asst!.completed).toBe(true)
    expect(asst!.metadata?.turnState).toBe('completed')
  })

  it('reconnect: history metadata.turnState=running overrides step-derived flicker', () => {
    // Reconnect scenario: session summary returns an active turn whose steps
    // all happen to be closed at the snapshot instant (between dispatch
    // iterations). session-adapter populates metadata.turnState from Turn.State
    // so projection keeps the envelope running without waiting for turn.started.
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1' },
      { Id: 'llm1', Role: 'assistant', Type: 'llm_call', Content: [{ Type: 'text', Text: 'hello' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1', Seq: 1 },
    ]
    const historyEnvelopes: TurnEnvelope[] = [
      {
        id: 't1', role: 'assistant', frames: [], timestamp: '2026-01-01T00:00:00Z',
        completed: false,
        metadata: {
          turnId: 't1',
          turnState: 'running',
          startedAt: '2026-01-01T00:00:00Z',
        },
      },
    ]
    const tl = makeTimeline({ steps, historyEnvelopes })
    // No activeTurnStates entry — simulates reconnect before any turn event arrives.
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      new Map(),
    )
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 't1')
    expect(asst).toBeDefined()
    expect(asst!.completed).toBe(false)
    expect(asst!.metadata?.turnState).toBe('running')
    expect(asst!.metadata?.startedAt).toBe('2026-01-01T00:00:00Z')
  })

  it('avoids duplicate user messages when history and step ids differ', () => {
    // Regression: the user message for turn 2 may appear both as a persisted
    // history envelope (id = turn id) and as a user_inject step (id = step id).
    // The projection must keep only the step-derived user envelope and must not
    // let the assistant envelope reuse the history user id.
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u1', role: 'user', frames: [], timestamp: '1', metadata: { turnId: 'u1' } },
      { id: 'a1', role: 'assistant', frames: [{ id: 'f1', type: 'text', status: 'completed', content: 'A1' }], timestamp: '2', completed: true, metadata: { turnId: 'a1' } },
      { id: 'u2', role: 'user', frames: [], timestamp: '3', metadata: { turnId: 'u2' } },
      { id: 'a2', role: 'assistant', frames: [], timestamp: '4', completed: false, metadata: { turnId: 'a2', turnState: 'running' } },
    ]
    const steps: Step[] = [
      { Id: 'u2-step', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q2' }], Closed: true, Timestamp: '3', TurnId: 'u2' },
      { Id: 'a2-step', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'A2 streaming' }], Closed: false, Timestamp: '4', TurnId: 'a2' },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    const userIds = result.filter(e => e.role === 'user').map(e => e.id)
    expect(userIds).toEqual(['u1', 'u2-step'])
    expect(result).toHaveLength(4)
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 'a2')
    expect(asst).toBeDefined()
    expect(asst!.id).not.toBe('u2')
    expect((asst!.frames[0] as import('../model/frame-types').TextFrame).content).toBe('A2 streaming')
  })
})

// ── computeEnvelopes: turnFileChanges from step.file_changes events ──

describe('computeEnvelopes — turnFileChanges', () => {
  it('uses turnFileChanges from step.file_changes when no history fileChanges', () => {
    const steps: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'done' }], Closed: true, Timestamp: '1', TurnId: 't1' },
    ]
    const tl = makeTimeline({ steps })
    const turnFileChanges = new Map([
      ['t1', [
        { filename: 'foo.ts', filepath: 'foo.ts', icon: 'ts' as const, additions: 10, deletions: 2, diffContent: '@@ diff @@' },
        { filename: 'bar.css', filepath: 'bar.css', icon: 'css' as const, additions: 5, deletions: 0, diffContent: '' },
      ]],
    ])
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      turnFileChanges,
    )
    const asst = result.find(e => e.role === 'assistant')!
    expect(asst.fileChanges).toBeDefined()
    expect(asst.fileChanges!).toHaveLength(2)
    expect(asst.fileChanges![0]!.filename).toBe('foo.ts')
    expect(asst.fileChanges![1]!.filename).toBe('bar.css')
  })

  it('turnFileChanges take precedence over history fileChanges (live data is newer)', () => {
    const steps: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'done' }], Closed: true, Timestamp: '1', TurnId: 't1' },
    ]
    const historyEnvelopes: TurnEnvelope[] = [
      {
        id: 't1', role: 'assistant', frames: [], timestamp: '1', completed: true,
        metadata: { turnId: 't1' },
        fileChanges: [{ filename: 'from-history.ts', filepath: 'from-history.ts', icon: 'ts' as const, additions: 1, deletions: 1, diffContent: '' }],
      },
    ]
    const tl = makeTimeline({ steps, historyEnvelopes })
    const turnFileChanges = new Map([
      ['t1', [
        { filename: 'from-event.ts', filepath: 'from-event.ts', icon: 'ts' as const, additions: 99, deletions: 99, diffContent: '' },
      ]],
    ])
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      turnFileChanges,
    )
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 't1')!
    expect(asst.fileChanges).toBeDefined()
    expect(asst.fileChanges!).toHaveLength(1)
    expect(asst.fileChanges![0]!.filename).toBe('from-event.ts')
  })

  it('history fileChanges is fallback when turnFileChanges is empty', () => {
    const steps: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'done' }], Closed: true, Timestamp: '1', TurnId: 't1' },
    ]
    const historyEnvelopes: TurnEnvelope[] = [
      {
        id: 't1', role: 'assistant', frames: [], timestamp: '1', completed: true,
        metadata: { turnId: 't1' },
        fileChanges: [{ filename: 'from-history.ts', filepath: 'from-history.ts', icon: 'ts' as const, additions: 1, deletions: 1, diffContent: '' }],
      },
    ]
    const tl = makeTimeline({ steps, historyEnvelopes })
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      new Map(), // empty tfc — fall back to hist
    )
    const asst = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 't1')!
    expect(asst.fileChanges).toBeDefined()
    expect(asst.fileChanges!).toHaveLength(1)
    expect(asst.fileChanges![0]!.filename).toBe('from-history.ts')
  })
})

describe('mergeSteps', () => {
  it('appends missing steps', () => {
    const existing: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1' },
    ]
    const incoming: Step[] = [
      { Id: 's2', Role: 'assistant', Type: 'tool_call', Content: [], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1' },
    ]
    const result = mergeSteps(existing, incoming)
    expect(result.map(s => s.Id)).toEqual(['s1', 's2'])
  })

  it('replaces older steps with newer timestamps', () => {
    const existing: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'old' }], Closed: false, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1' },
    ]
    const incoming: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'new' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1' },
    ]
    const result = mergeSteps(existing, incoming)
    expect(result).toHaveLength(1)
    expect(result[0]!.Closed).toBe(true)
    expect(result[0]!.Content[0]!.Text).toBe('new')
  })

  it('replaces open local step with closed server step even when timestamp is older', () => {
    const existing: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'reasoning', Content: [], Closed: false, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1' },
    ]
    const incoming: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'reasoning', Content: [], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1' },
    ]
    const result = mergeSteps(existing, incoming)
    expect(result).toHaveLength(1)
    expect(result[0]!.Closed).toBe(true)
  })

  it('prefers higher Seq over newer timestamp when both closed', () => {
    const existing: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'old' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1', Seq: 1 },
    ]
    const incoming: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'new' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1', Seq: 2 },
    ]
    const result = mergeSteps(existing, incoming)
    expect(result).toHaveLength(1)
    expect(result[0]!.Content[0]!.Text).toBe('new')
  })

  it('prefers server step with Seq over local legacy step without Seq', () => {
    const existing: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'local' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1' },
    ]
    const incoming: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'server' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1', Seq: 5 },
    ]
    const result = mergeSteps(existing, incoming)
    expect(result).toHaveLength(1)
    expect(result[0]!.Content[0]!.Text).toBe('server')
  })

  it('sorts merged steps by Seq, falling back to timestamp', () => {
    const existing: Step[] = [
      { Id: 's2', Role: 'assistant', Type: 'text', Content: [], Closed: true, Timestamp: '2026-01-01T00:00:02Z', TurnId: 't1', Seq: 2 },
    ]
    const incoming: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1', Seq: 1 },
      { Id: 's3', Role: 'assistant', Type: 'text', Content: [], Closed: true, Timestamp: '2026-01-01T00:00:03Z', TurnId: 't1', Seq: 3 },
    ]
    const result = mergeSteps(existing, incoming)
    expect(result.map(s => s.Id)).toEqual(['s1', 's2', 's3'])
  })

  it('preserves discarded step over non-discarded incoming snapshot', () => {
    // A stale MissingSteps response (captured before the backend rewrote the
    // per-turn JSONL) may return content without Discarded=true. The local
    // discarded state must win so the cleared content does not reappear.
    const existing: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: true, Discarded: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1', Seq: 1 },
    ]
    const incoming: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'resurrected' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1', Seq: 99 },
    ]
    const result = mergeSteps(existing, incoming)
    expect(result[0]!.Discarded).toBe(true)
    expect(result[0]!.Content).toHaveLength(0)
  })

  it('sorts legacy steps without Seq by timestamp', () => {
    const existing: Step[] = [
      { Id: 's2', Role: 'assistant', Type: 'text', Content: [], Closed: true, Timestamp: '2026-01-01T00:00:02Z', TurnId: 't1' },
    ]
    const incoming: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1' },
    ]
    const result = mergeSteps(existing, incoming)
    expect(result.map(s => s.Id)).toEqual(['s1', 's2'])
  })

  // AUDIT 2.3 / 7.3: when incoming is fully covered by existing (every entry
  // is a duplicate that loses the precedence check), mergeSteps must return
  // the existing array unchanged so downstream cache checks (computeEnvelopes)
  // short-circuit and React doesn't re-render. The reconciler's
  // knownStepEventSeqs is snapshotted at fetch entry, so by the time the
  // response lands the live stream has often already delivered the same
  // steps the backend returns as MissingSteps.
  it('returns existing reference when incoming is all duplicates (AUDIT 2.3/7.3)', () => {
    const existing: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'a' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1', Seq: 1 },
      { Id: 's2', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'b' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1', Seq: 2 },
    ]
    // Same Ids, same Seqs, same Closed — every entry loses the precedence check.
    const incoming: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'a' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1', Seq: 1 },
      { Id: 's2', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'b' }], Closed: true, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1', Seq: 2 },
    ]
    const result = mergeSteps(existing, incoming)
    expect(result).toBe(existing) // referential equality
  })

  it('returns new array when at least one incoming entry supersedes existing', () => {
    const existing: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'old' }], Closed: false, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1', Seq: 1 },
    ]
    // s1 closes — supersedes existing open step.
    const incoming: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'old' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1', Seq: 1 },
    ]
    const result = mergeSteps(existing, incoming)
    expect(result).not.toBe(existing)
    expect(result[0]!.Closed).toBe(true)
  })

  it('sorts envelopes by Seq ignoring out-of-order timestamps', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'a2', role: 'assistant', frames: [], timestamp: '2026-01-01T10:00:00Z', seq: 4, completed: true, metadata: { turnId: 'a2' } },
      { id: 'u2', role: 'user', frames: [], timestamp: '2026-01-01T09:59:00Z', seq: 3, metadata: { turnId: 'u2' } },
      { id: 'a1', role: 'assistant', frames: [], timestamp: '2026-01-01T09:58:00Z', seq: 2, completed: true, metadata: { turnId: 'a1' } },
      { id: 'u1', role: 'user', frames: [], timestamp: '2026-01-01T11:00:00Z', seq: 1, metadata: { turnId: 'u1' } },
    ]
    const tl = makeTimeline({ historyEnvelopes })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)
    expect(result.map(e => e.id)).toEqual(['u1', 'a1', 'u2', 'a2'])
  })

  it('keeps compaction frame inside owning turn when Seq order disagrees with timestamp', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q1' }], Closed: true, Timestamp: '2026-06-14T10:00:00Z', TurnId: 'turn-1', Seq: 1 },
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'A1' }], Closed: true, Timestamp: '2026-06-14T09:00:00Z', TurnId: 'turn-1', Seq: 2 },
      { Id: 'c1', Role: 'system', Type: 'text', Content: [{ Type: 'compaction', Text: '{"type":"compaction","status":"completed","beforeTokens":1000,"afterTokens":500,"contextWindowSize":8000}' }], Closed: true, Timestamp: '2026-06-14T11:00:00Z', TurnId: 'turn-1', Seq: 3 },
      { Id: 'u2', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'Q2' }], Closed: true, Timestamp: '2026-06-14T08:00:00Z', TurnId: 'turn-2', Seq: 4 },
      { Id: 'a2', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'A2' }], Closed: true, Timestamp: '2026-06-14T07:00:00Z', TurnId: 'turn-2', Seq: 5 },
    ]
    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    expect(result.map(e => e.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
    expect(result[1]!.metadata?.turnId).toBe('turn-1')
    expect(result[1]!.frames).toHaveLength(2)
    expect(result[1]!.frames[1]).toMatchObject({ type: 'compaction' })
  })
})

// ── State-layer: pending user messages ──

describe('AgentSession — pending user messages', () => {
  it('addPendingUserMessage stores message keyed by clientId', () => {
    const layer = new AgentSession('test-agent')
    layer.addPendingUserMessage('client-1', 'hello world')
    const pending = layer.getPendingUserMessages()
    expect(pending.length).toBe(1)
    expect(pending[0]!.clientId).toBe('client-1')
    expect(pending[0]!.text).toBe('hello world')
    expect(pending[0]!.timestamp).toBeTruthy()
  })

  it('confirmUserMessage removes the pending entry', () => {
    const layer = new AgentSession('test-agent')
    layer.addPendingUserMessage('client-1', 'hello')
    layer.addPendingUserMessage('client-2', 'world')
    layer.confirmUserMessage('client-1')
    const pending = layer.getPendingUserMessages()
    expect(pending.length).toBe(1)
    expect(pending[0]!.clientId).toBe('client-2')
    expect(pending[0]!.text).toBe('world')
  })

  it('confirmUserMessage is no-op for unknown clientId', () => {
    const layer = new AgentSession('test-agent')
    layer.addPendingUserMessage('client-1', 'hello')
    layer.confirmUserMessage('unknown')
    expect(layer.getPendingUserMessages().length).toBe(1)
  })

  it('reset clears pending user messages', () => {
    const layer = new AgentSession('test-agent')
    layer.addPendingUserMessage('client-1', 'hello')
    layer.reset()
    expect(layer.getPendingUserMessages().length).toBe(0)
  })

  it('getPendingUserMessages reflects live state', () => {
    const layer = new AgentSession('test-agent')
    layer.addPendingUserMessage('client-1', 'hello')
    expect(layer.getPendingUserMessages().length).toBe(1)
    layer.addPendingUserMessage('client-2', 'world')
    // snapshot() returns a new array each call — reflects current state.
    expect(layer.getPendingUserMessages().length).toBe(2)
  })

  it('confirms pending user messages when a matching user_inject step arrives', () => {
    const layer = new AgentSession('test-agent')
    layer.addPendingUserMessage('client-1', 'hello')
    layer.applyStepEvents([{
      Kind: 'step.opened',
      StepId: 'ui1',
      TurnId: 't1',
      StepType: 'user_inject',
      Role: 'assistant',
      Block: { Type: 'text', Text: 'hello' },
    }])
    expect(layer.getPendingUserMessages().length).toBe(0)
  })

  it('keeps pending user messages when user_inject text does not match', () => {
    const layer = new AgentSession('test-agent')
    layer.addPendingUserMessage('client-1', 'hello')
    layer.applyStepEvents([{
      Kind: 'step.opened',
      StepId: 'ui1',
      TurnId: 't1',
      StepType: 'user_inject',
      Role: 'assistant',
      Block: { Type: 'text', Text: 'world' },
    }])
    expect(layer.getPendingUserMessages().length).toBe(1)
  })

  it('confirms pending user messages when a matching normal user step arrives', () => {
    const layer = new AgentSession('test-agent')
    layer.addPendingUserMessage('client-1', 'hello')
    layer.applyStepEvents([{
      Kind: 'step.opened',
      StepId: 'u1',
      TurnId: 't1',
      StepType: 'text',
      Role: 'user',
      Block: { Type: 'text', Text: 'hello' },
    }])
    expect(layer.getPendingUserMessages().length).toBe(0)
  })

  it('keeps pending user messages when normal user step text does not match', () => {
    const layer = new AgentSession('test-agent')
    layer.addPendingUserMessage('client-1', 'hello')
    layer.applyStepEvents([{
      Kind: 'step.opened',
      StepId: 'u1',
      TurnId: 't1',
      StepType: 'text',
      Role: 'user',
      Block: { Type: 'text', Text: 'world' },
    }])
    expect(layer.getPendingUserMessages().length).toBe(1)
  })

  it('notifies listeners when a pending user message is added', () => {
    const layer = new AgentSession('test-agent')
    const listener = vi.fn()
    layer.subscribe(listener)
    layer.addPendingUserMessage('client-1', 'hello')
    expect(listener).toHaveBeenCalledTimes(1)
  })
})

// ── TimelineManager.pushUserMessage ──

describe('TimelineManager.pushUserMessage', () => {
  beforeEach(() => {
    // createTimelineManager uses module-level state; ensure each test starts
    // with a fresh agent-a timeline.
    createTimelineManager().release('agent-a')
  })

  it('adds pending user message even when no turn is running', async () => {
    const manager = createTimelineManager()
    manager.select('agent-a')
    await vi.waitFor(() => expect(manager.getSnapshot('agent-a').loading).toBe(false))

    const id = manager.pushUserMessage('hello', 'agent-a')
    expect(id).toBeTruthy()
    expect(manager.getPendingUserMessages('agent-a').length).toBe(1)
    expect(manager.getPendingUserMessages('agent-a')[0]?.text).toBe('hello')
  })

  it('adds pending user message when a turn is running', async () => {
    const manager = createTimelineManager()
    manager.select('agent-a')
    await vi.waitFor(() => expect(manager.getSnapshot('agent-a').loading).toBe(false))

    manager.importHistory([{
      id: 'a1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', status: 'running', content: '...' }],
      timestamp: '2026-01-01T00:00:00Z',
      completed: false,
      metadata: { turnId: 't1' },
    }], 'agent-a')

    const id = manager.pushUserMessage('hello', 'agent-a')
    expect(id).toBeTruthy()
    expect(manager.getPendingUserMessages('agent-a').length).toBe(1)
    expect(manager.getPendingUserMessages('agent-a')[0]?.text).toBe('hello')
  })
})

// ── State-layer: applyTurnEvent ──

describe('AgentSession — applyTurnEvent', () => {
  it('applies context_budget to matching history envelope by turnId', () => {
    const layer = new AgentSession('test-agent')
    const historyEnvs: TurnEnvelope[] = [
      { id: 'a1', role: 'assistant', frames: [], timestamp: '1', metadata: { turnId: 't1' } },
    ]
    layer.setHistoryEnvelopes(historyEnvs)

    const ev: TurnEvent = {
      Kind: 'turn.context_budget',
      TurnId: 't1',
      ContextBudget: { EstimatedTokens: 500, ContextWindowSize: 8000, TokenBudget: 7000 },
    }
    layer.applyTurnEvent(ev)

    const snap = layer.getSnapshot()
    const env = snap.envelopes.find(e => e.id === 'a1')!
    expect(env.metadata?.contextBudget).toEqual({
      estimatedTokens: 500,
      contextWindowSize: 8000,
      tokenBudget: 7000,
    })
  })

  it('context_budget no-ops when TurnId does not match any envelope', () => {
    const layer = new AgentSession('test-agent')
    layer.setHistoryEnvelopes([
      { id: 'a1', role: 'assistant', frames: [], timestamp: '1', metadata: { turnId: 't1' } },
    ])

    layer.applyTurnEvent({
      Kind: 'turn.context_budget',
      TurnId: 'unknown-turn',
      ContextBudget: { EstimatedTokens: 100, ContextWindowSize: 4000, TokenBudget: 3000 },
    })

    const env = layer.getSnapshot().envelopes.find(e => e.id === 'a1')!
    expect(env.metadata?.contextBudget).toBeUndefined()
  })

  it('context_budget merges into existing metadata', () => {
    const layer = new AgentSession('test-agent')
    layer.setHistoryEnvelopes([
      { id: 'a1', role: 'assistant', frames: [], timestamp: '1', completed: true, metadata: { turnId: 't1', actionCount: 3 } },
    ])

    layer.applyTurnEvent({
      Kind: 'turn.context_budget',
      TurnId: 't1',
      ContextBudget: { EstimatedTokens: 200, ContextWindowSize: 4000, TokenBudget: 3000 },
    })

    const env = layer.getSnapshot().envelopes.find(e => e.id === 'a1')!
    expect(env.metadata?.actionCount).toBe(3)
    expect(env.metadata?.contextBudget?.estimatedTokens).toBe(200)
  })

  it('context_budget is preserved for active turns not yet in history', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([
      { Id: 's1', Role: 'assistant', Type: 'turn_start', Content: [], Closed: false, Timestamp: '1', TurnId: 't1' },
      { Id: 's2', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: false, Timestamp: '2', TurnId: 't1' },
    ])

    layer.applyTurnEvent({
      Kind: 'turn.context_budget',
      TurnId: 't1',
      ContextBudget: { EstimatedTokens: 100, ContextWindowSize: 8000, TokenBudget: 6000 },
    })

    const env = layer.getSnapshot().envelopes.find(e => e.metadata?.turnId === 't1')!
    expect(env.metadata?.contextBudget).toEqual({
      estimatedTokens: 100,
      contextWindowSize: 8000,
      tokenBudget: 6000,
    })
  })
})

// ── State-layer: dispatchLocalEvent ──

describe('AgentSession — dispatchLocalEvent', () => {
  function makeStep(overrides: Partial<Step> = {}): Step {
    return {
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
      ...overrides,
    } as Step
  }

  it('stores ask_answered response keyed by ToolUseId', () => {
    const layer = new AgentSession('test-agent')
    const step = makeStep({
      Id: 'tool-ask', Type: 'tool_call',
      Content: [{ Type: 'tool_use', ToolUseId: 'ask-req-1', ToolName: 'ask_user', Input: '{}' }],
    })
    layer.setSteps([step])

    layer.dispatchLocalEvent({ kind: 'ai.ask_answered', requestId: 'ask-req-1', answers: { 0: 'yes' } })

    const responses = layer['_localInteractionResponses']
    expect(responses.get('ask-req-1')).toEqual({ kind: 'ask_answered', answers: { 0: 'yes' } })
  })

  it('stores permission_answered response keyed by step.Id', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([makeStep({ Id: 'perm-1' })])

    layer.dispatchLocalEvent({ kind: 'ai.permission_answered', requestId: 'perm-1', allowed: true })

    const responses = layer['_localInteractionResponses']
    expect(responses.get('perm-1')).toEqual({ kind: 'permission_answered', allowed: true })
  })

  it('stores plan_approval_answered response without mutating step (selector-driven)', () => {
    const layer = new AgentSession('test-agent')
    const step = makeStep({ Id: 'plan-1', Type: 'plan_approval', InteractionStatus: 'pending' })
    layer.setSteps([step])

    layer.dispatchLocalEvent({
      kind: 'ai.plan_approval_answered',
      requestId: 'plan-1',
      decision: 'approve',
    })

    const responses = layer['_localInteractionResponses']
    expect(responses.get('plan-1')).toEqual({
      kind: 'plan_approval_answered',
      decision: 'approve',
      editedPlan: undefined,
      selectedPolicy: undefined,
    })
    // Selector 化: step.InteractionStatus stays as backend last set it.
    // Effective "resolved" is derived in selectors by consulting the Map.
    expect(layer.steps[0]!.InteractionStatus).toBe('pending')
  })

  it('no-ops when requestId does not match any step', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([makeStep({ Id: 's1' })])

    layer.dispatchLocalEvent({ kind: 'ai.ask_answered', requestId: 'nonexistent', answers: {} })

    expect(layer['_localInteractionResponses'].size).toBe(0)
  })

  it('keeps already-resolved interaction status untouched on second dispatch', () => {
    const layer = new AgentSession('test-agent')
    const step = makeStep({ Id: 'plan-1', Type: 'plan_approval', InteractionStatus: 'resolved' })
    layer.setSteps([step])

    // Second dispatch should not revert resolved status
    layer.dispatchLocalEvent({ kind: 'ai.plan_approval_answered', requestId: 'plan-1', decision: 'reject' })

    expect(layer.steps[0]!.InteractionStatus).toBe('resolved')
  })
})

// ── State-layer: reset ──

describe('AgentSession — reset', () => {
  it('clears all state fields', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([{ Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1' } as Step])
    layer.setHistoryEnvelopes([{ id: 'h1', role: 'user', frames: [], timestamp: '1' }])
    layer.addPendingUserMessage('c1', 'hello')
    layer.setLoading(true)
    layer.setError('boom')

    layer.reset()

    expect(layer.steps).toEqual([])
    expect(layer.historyEnvelopes).toEqual([])
    expect(layer.loading).toBe(false)
    expect(layer.error).toBeNull()
    expect(layer.hasMoreHistory).toBe(false)
    expect(layer.getPendingUserMessages().length).toBe(0)
    expect(layer['_localInteractionResponses'].size).toBe(0)
  })
})

// ── Projection: deriveTurnState ──

describe('deriveTurnState', () => {
  const mkStep = (overrides: Partial<Step> = {}): Step =>
    ({ Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1', ...overrides }) as Step

  it('returns completed for empty steps', () => {
    expect(deriveTurnState([])).toBe('completed')
  })

  it('returns completed when all steps are closed', () => {
    expect(deriveTurnState([mkStep({ Closed: true }), mkStep({ Id: 's2', Closed: true })])).toBe('completed')
  })

  it('returns paused when an open step has pending interaction', () => {
    expect(deriveTurnState([
      mkStep({ Closed: true }),
      mkStep({ Id: 's2', InteractionStatus: 'pending' }),
    ])).toBe('paused')
  })

  it('returns failed when an open step has an Error', () => {
    expect(deriveTurnState([
      mkStep({ Closed: true }),
      mkStep({ Id: 's2', Error: 'timeout' }),
    ])).toBe('failed')
  })

  it('returns running when an open step has no interaction or error', () => {
    expect(deriveTurnState([mkStep()])).toBe('running')
  })

  it('paused takes priority over failed', () => {
    expect(deriveTurnState([
      mkStep({ Id: 's1', InteractionStatus: 'pending' }),
      mkStep({ Id: 's2', Error: 'timeout' }),
    ])).toBe('paused')
  })

  // ── Pending interactions are never stale: the user may take arbitrarily
  //  long to approve/reject a plan or goal. A pending interaction is an
  //  explicit user-input gate, not a lost terminal event.

  it('returns paused when pending interaction is stale (never reaped)', () => {
    // Step has been pending for > 5 minutes — but pending interactions are
    // user-input gates and must never be treated as stale.
    const oldTs = new Date(Date.now() - 10 * 60 * 1000).toISOString()
    expect(deriveTurnState([
      mkStep({ Id: 's1', InteractionStatus: 'pending', Timestamp: oldTs }),
    ])).toBe('paused')
  })

  it('returns paused when pending interaction is fresh', () => {
    const freshTs = new Date(Date.now() - 30 * 1000).toISOString()
    expect(deriveTurnState([
      mkStep({ Id: 's1', InteractionStatus: 'pending', Timestamp: freshTs }),
    ])).toBe('paused')
  })

  // ── Stale running fallback: open step with no pending interaction but
  //  no activity for >5min — assume turn terminal event was lost. Returns
  //  'completed' (NOT 'failed') to mirror pending-stale behavior; surfacing
  //  a false error icon for a turn that may have succeeded is worse than
  //  a neutral done.

  it('returns completed when a running turn is stale (no activity >5min)', () => {
    const oldTs = new Date(Date.now() - 10 * 60 * 1000).toISOString()
    expect(deriveTurnState([
      mkStep({ Id: 's1', InteractionStatus: 'none', Timestamp: oldTs }),
    ])).toBe('completed')
  })

  it('returns running when a running turn is fresh (<5min activity)', () => {
    const freshTs = new Date(Date.now() - 30 * 1000).toISOString()
    expect(deriveTurnState([
      mkStep({ Id: 's1', InteractionStatus: 'none', Timestamp: freshTs }),
    ])).toBe('running')
  })

  it('stale running does NOT mask a real error (Error takes priority when fresh)', () => {
    const freshTs = new Date(Date.now() - 30 * 1000).toISOString()
    expect(deriveTurnState([
      mkStep({ Id: 's1', Error: 'oops', Timestamp: freshTs }),
    ])).toBe('failed')
  })
})

// ── isTurnStale / _reapStaleOpenSteps (P2 defensive reap) ──

describe('isTurnStale', () => {
  it('returns false for no steps', () => {
    expect(isTurnStale([], Date.now())).toBe(false)
  })
  it('returns false for steps without Timestamp', () => {
    expect(isTurnStale(
      [{ Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1' }],
      Date.now(),
    )).toBe(false)
  })
  it('returns true when latest step is older than 5 min', () => {
    const oldTs = new Date(Date.now() - 6 * 60 * 1000).toISOString()
    expect(isTurnStale(
      [{ Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false,
         Timestamp: oldTs, TurnId: 't1' }],
      Date.now(),
    )).toBe(true)
  })
  it('returns false when latest step is recent (10s ago)', () => {
    const freshTs = new Date(Date.now() - 10 * 1000).toISOString()
    expect(isTurnStale(
      [{ Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false,
         Timestamp: freshTs, TurnId: 't1' }],
      Date.now(),
    )).toBe(false)
  })
})

describe('AgentSession — _reapStaleOpenSteps', () => {
  const STALE_TS = new Date(Date.now() - 6 * 60 * 1000).toISOString()
  const FRESH_TS = new Date(Date.now() - 10 * 1000).toISOString()

  it('closes open steps in a stale turn', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [],
        Closed: false, Timestamp: STALE_TS, TurnId: 't1' },
    ])
    // Trigger reap via the private method.
    // @ts-expect-error — private, tested via side effect
    layer._reapStaleOpenSteps(Date.now())
    const after = layer.steps
    expect(after[0]!.Closed).toBe(true)
    expect(after[0]!.ContentStatus).toBe('stable')
    layer.release()
  })

  it('does not close steps in a fresh turn', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [],
        Closed: false, Timestamp: FRESH_TS, TurnId: 't1' },
    ])
    // @ts-expect-error — private, tested via side effect
    layer._reapStaleOpenSteps(Date.now())
    expect(layer.steps[0]!.Closed).toBe(false)
    layer.release()
  })

  it('does not close steps already closed (no-op)', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [],
        Closed: true, ContentStatus: 'stable', Timestamp: STALE_TS, TurnId: 't1' },
    ])
    // @ts-expect-error — private, tested via side effect
    layer._reapStaleOpenSteps(Date.now())
    expect(layer.steps[0]!.Closed).toBe(true) // still closed
    layer.release()
  })

  it('does not reap stale steps with a pending interaction', () => {
    // A pending interaction (plan_approval / goal_submit / ask_user) is a
    // user-input gate — the user may take arbitrarily long to respond.
    const layer = new AgentSession('test-agent')
    layer.setSteps([
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [],
        Closed: false, Timestamp: STALE_TS, TurnId: 't1',
        InteractionStatus: 'pending' },
    ])
    // @ts-expect-error — private, tested via side effect
    layer._reapStaleOpenSteps(Date.now())
    expect(layer.steps[0]!.Closed).toBe(false)
    layer.release()
  })

  it('getSnapshot is side-effect-free and does not reap stale steps', () => {
    // useSyncExternalStore requires getSnapshot to be a pure read. Reap must
    // run only from the session timer, never from getSnapshot.
    const layer = new AgentSession('test-agent')
    layer.setSteps([
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [],
        Closed: false, Timestamp: STALE_TS, TurnId: 't1' },
    ])
    layer.getSnapshot()
    expect(layer.steps[0]!.Closed).toBe(false)
    layer.release()
  })
})

describe('AgentSession — _reconcileMissingTerminalTurns', () => {
  const QUIET_TS = new Date(Date.now() - 30 * 1000).toISOString()
  const FRESH_TS = new Date(Date.now() - 3 * 1000).toISOString()

  function makeLayer(steps: Step[], historyEnvelopes: TurnEnvelope[] = [], hasActiveEntry = false) {
    const layer = new AgentSession('test-agent')
    layer.setSteps(steps)
    layer.setHistoryEnvelopes(historyEnvelopes, false)
    if (hasActiveEntry) {
      layer.applyTurnEvent({ Kind: 'turn.completed', TurnId: steps[0]!.TurnId!, Payload: { state: 'completed', revision: 5 } } as never)
    }
    const fetch = vi.fn()
    layer.reconciler = { fetch } as never
    return { layer, fetch }
  }

  const closedTurn = (): Step[] => [
    { Id: 's1', Role: 'assistant', Type: 'text', Content: [],
      Closed: true, ContentStatus: 'stable', Timestamp: QUIET_TS, TurnId: 't1' },
  ]

  it('requests a reconcile when a closed turn has no canonical state (lost terminal event)', () => {
    const { layer, fetch } = makeLayer(closedTurn())
    layer.reapStaleOpenSteps(Date.now())
    expect(fetch).toHaveBeenCalledTimes(1)
    // One-shot: the same turn never triggers a second fetch.
    layer.reapStaleOpenSteps(Date.now())
    expect(fetch).toHaveBeenCalledTimes(1)
    layer.release()
  })

  it('does not fetch while the terminal event may still arrive (fresh steps)', () => {
    const steps = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [],
        Closed: true, ContentStatus: 'stable', Timestamp: FRESH_TS, TurnId: 't1' },
    ]
    const { layer, fetch } = makeLayer(steps)
    layer.reapStaleOpenSteps(Date.now())
    expect(fetch).not.toHaveBeenCalled()
    layer.release()
  })

  it('does not fetch when a live canonical entry exists', () => {
    const { layer, fetch } = makeLayer(closedTurn(), [], true)
    layer.reapStaleOpenSteps(Date.now())
    expect(fetch).not.toHaveBeenCalled()
    layer.release()
  })

  it('does not fetch when the history envelope carries a revision-bearing turnState', () => {
    const hist: TurnEnvelope[] = [{
      id: 't1', role: 'assistant', frames: [], completed: true, timestamp: '1',
      metadata: { turnId: 't1', turnState: 'waiting', revision: 2 },
    }]
    const { layer, fetch } = makeLayer(closedTurn(), hist)
    layer.reapStaleOpenSteps(Date.now())
    expect(fetch).not.toHaveBeenCalled()
    layer.release()
  })

  it('does not fetch while the turn still has open steps', () => {
    const steps = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [],
        Closed: false, Timestamp: QUIET_TS, TurnId: 't1' },
    ]
    const { layer, fetch } = makeLayer(steps)
    layer.reapStaleOpenSteps(Date.now())
    expect(fetch).not.toHaveBeenCalled()
    layer.release()
  })

  it('re-arms the marker when a canonical terminal state arrives', () => {
    const { layer, fetch } = makeLayer(closedTurn())
    layer.reapStaleOpenSteps(Date.now())
    expect(fetch).toHaveBeenCalledTimes(1)
    // The lost event arrives late; the marker must clear.
    layer.applyTurnEvent({ Kind: 'turn.waiting', TurnId: 't1', Payload: { state: 'waiting', revision: 9 } } as never)
    // Next cycle does not fetch again (entry exists now).
    layer.reapStaleOpenSteps(Date.now())
    expect(fetch).toHaveBeenCalledTimes(1)
    layer.release()
  })

  it('marks every pending turn in one pass and fetches only once (no 15s train)', () => {
    const twoTurns: Step[] = [
      ...closedTurn(),
      { Id: 's2', Role: 'assistant', Type: 'text', Content: [],
        Closed: true, ContentStatus: 'stable', Timestamp: QUIET_TS, TurnId: 't2' },
      { Id: 's3', Role: 'assistant', Type: 'text', Content: [],
        Closed: true, ContentStatus: 'stable', Timestamp: QUIET_TS, TurnId: 't3' },
    ]
    const { layer, fetch } = makeLayer(twoTurns)
    layer.reapStaleOpenSteps(Date.now())
    // One fetch serves all pending turns — the summary is global.
    expect(fetch).toHaveBeenCalledTimes(1)
    // And the next tick marks nothing new: no second fetch in the train.
    layer.reapStaleOpenSteps(Date.now())
    expect(fetch).toHaveBeenCalledTimes(1)
    layer.release()
  })
})

// ── computeEnvelopes: plan_submit stripping & requestId filtering ──

describe('computeEnvelopes — plan_submit stripping', () => {
  it('removes frames with plan_submit toolName', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'plan X' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'llm1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'here is the plan...' }], Closed: true, Timestamp: '2', TurnId: 't1' },
      { Id: 'tool1', Role: 'assistant', Type: 'tool_call', Content: [
        { Type: 'tool_use', ToolUseId: 'tu1', ToolName: 'plan_submit', Input: '{}' },
        { Type: 'tool_result', ToolUseId: 'tu1', Text: 'ok' },
      ], Closed: true, Timestamp: '3', TurnId: 't1' },
    ]
    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    const asst = result.find(e => e.role === 'assistant')!
    // plan_submit tool frame should be stripped; only the text frame remains
    expect(asst.frames.every(f => f.type !== 'tool')).toBe(true)
    expect(asst.frames.some(f => f.type === 'text')).toBe(true)
  })

  it('removes frames with plan_submit callable ID', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'plan Y' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'tool1', Role: 'assistant', Type: 'tool_call', Content: [
        { Type: 'tool_use', ToolUseId: 'tu1', ToolName: 'plan_submit', Input: '{}' },
        { Type: 'tool_result', ToolUseId: 'tu1', Text: 'ok' },
      ], Closed: true, Timestamp: '2', TurnId: 't1' },
    ]
    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    const asst = result.find(e => e.role === 'assistant')!
    expect(asst.frames.every(f => f.type !== 'tool')).toBe(true)
  })

  it('removes plan frames without requestId from history envelopes', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      {
        id: 'a1', role: 'assistant',
        frames: [{ id: 'f1', type: 'plan', status: 'completed', content: 'old plan' } as any],
        timestamp: '1', completed: true, metadata: { turnId: 't1' },
      },
    ]
    const tl = makeTimeline({ historyEnvelopes })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    const asst = result.find(e => e.role === 'assistant')!
    expect(asst.frames.filter((f: any) => f.type === 'plan').length).toBe(0)
  })

  // AUDIT 5.2: a step-derived plan + a preserved history plan with the SAME
  // requestId must render as ONE card. Previously both survived stripPlanSubmit.

  it('dedupes plan frames with the same requestId (AUDIT 5.2)', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      {
        id: 'a1', role: 'assistant',
        frames: [
          { id: 'hist-plan', type: 'plan', status: 'completed', content: 'plan body', requestId: 'req-1' } as any,
        ],
        timestamp: '1', completed: true, metadata: { turnId: 't1' },
      },
    ]
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'approve this' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      {
        Id: 'plan-step', Role: 'assistant', Type: 'plan_approval', Closed: false, Timestamp: '2', TurnId: 't1',
        Content: [{ Type: 'text', Text: JSON.stringify({ plan: 'plan body', tasks: [] }) }],
        RequestId: 'req-1', InteractionStatus: 'pending',
      } as any,
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    const asst = result.find(e => e.role === 'assistant')!
    const planFrames = asst.frames.filter((f: any) => f.type === 'plan')
    expect(planFrames.length).toBe(1)
  })
})

// ── computeEnvelopes: history frame fallback ──

describe('computeEnvelopes — history frame fallback', () => {
  it('falls back to history frames when step-derived envelope has no renderable frames', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      {
        id: 'a1', role: 'assistant',
        frames: [{ id: 'f1', type: 'text', status: 'completed', content: 'history text' }],
        timestamp: '2026-01-01T00:00:00Z', completed: false, metadata: { turnId: 't1' },
      },
    ]
    // An unrecognized step type produces zero frames, triggering the fallback.
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '2026-01-01T00:00:00Z', TurnId: 't1' },
      { Id: 's1', Role: 'assistant', Type: 'unknown_kind' as any, Content: [], Closed: false, Timestamp: '2026-01-01T00:00:01Z', TurnId: 't1' },
    ]
    const tl = makeTimeline({ historyEnvelopes, steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    const asst = result.find(e => e.role === 'assistant')!
    expect(asst.frames).toHaveLength(1)
    expect(asst.frames[0]).toMatchObject({ type: 'text', content: 'history text' })
  })
})

// ── computeEnvelopes: task dedup across turn envelopes ──

describe('computeEnvelopes — task dedup', () => {
  it('keeps tasks only on the last assistant envelope of each turn', () => {

    // Two history envelopes for the same turn (e.g. overlapping loads).
    // Last one keeps tasks; earlier ones have them stripped.
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u1', role: 'user', frames: [], timestamp: '1', metadata: { turnId: 'u1' } },
      { id: 'a1', role: 'assistant', frames: [{ id: 'f1', type: 'text', status: 'completed', content: 'plan' }], timestamp: '2', completed: true, metadata: { turnId: 't1' }, tasks: [{ id: 'task-1', subject: 'step 1', status: 'pending' as const }] },
      { id: 'a2', role: 'assistant', frames: [{ id: 'f2', type: 'text', status: 'completed', content: 'exec' }], timestamp: '3', completed: true, metadata: { turnId: 't1' }, tasks: [{ id: 'task-1', subject: 'step 1', status: 'pending' as const }] },
    ]
    const tl = makeTimeline({ historyEnvelopes })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes)

    const asstEnvs = result.filter(e => e.role === 'assistant')
    expect(asstEnvs.length).toBe(2)
    // First assistant envelope — tasks stripped (not the last for t1)
    expect(asstEnvs[0]!.tasks).toBeUndefined()
    // Last assistant envelope for t1 — tasks preserved
    expect(asstEnvs[1]!.tasks).toBeDefined()
    expect(asstEnvs[1]!.tasks![0]!.subject).toBe('step 1')
  })
})

// ── computeEnvelopes: localInteractionResponses threading ──

describe('computeEnvelopes — localInteractionResponses', () => {
  it('threads ask_answered local response into ask_user frame', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'which file?' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'ask1', Role: 'assistant', Type: 'tool_call', Content: [
        { Type: 'tool_use', ToolUseId: 'ask-req-1', ToolName: 'ask_user', Input: '{"questions":[{"header":"File","question":"Which file?","options":["a.ts","b.ts"],"multiSelect":false}]}' },
      ], Closed: false, Timestamp: '2', TurnId: 't1' },
    ]
    const localResponses = new Map<string, LocalInteractionResponse>()
    localResponses.set('ask-req-1', { kind: 'ask_answered', answers: { 0: 'a.ts' } })

    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes, localResponses)

    const asst = result.find(e => e.role === 'assistant')!
    const askFrame = asst.frames.find(f => f.type === 'ask_user') as any
    expect(askFrame).toBeDefined()
    expect(askFrame.answers).toEqual({ 0: 'a.ts' })
  })

  it('threads permission_answered local response into permission_request frame', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'run cmd' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'perm1', Role: 'assistant', Type: 'permission', Content: [{ Type: 'text', Text: 'allow shell.exec?' }], Closed: false, Timestamp: '2', TurnId: 't1' },
    ]
    const localResponses = new Map<string, LocalInteractionResponse>()
    localResponses.set('perm1', { kind: 'permission_answered', allowed: true })

    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes, localResponses)

    const asst = result.find(e => e.role === 'assistant')!
    const permFrame = asst.frames.find(f => f.type === 'permission_request') as any
    expect(permFrame).toBeDefined()
    expect(permFrame.allowed).toBe(true)
  })

  it('threads plan_approval_answered local response into plan frame', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'do X' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'plan1', Role: 'assistant', Type: 'plan_approval', Content: [{ Type: 'text', Text: '{"plan":"do X","tasks":[]}' }], Closed: false, Timestamp: '2', TurnId: 't1' },
    ]
    const localResponses = new Map<string, LocalInteractionResponse>()
    localResponses.set('plan1', { kind: 'plan_approval_answered', decision: 'approve' })

    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(tl.agentActorId, tl.layer.steps, tl.layer.historyEnvelopes, localResponses)

    const asst = result.find(e => e.role === 'assistant')!
    const planFrame = asst.frames.find(f => f.type === 'plan') as any
    expect(planFrame).toBeDefined()
    expect(planFrame.approvalStatus).toBe('approved')
  })
})

// ── AgentSession: real-time task snapshots ──

describe('AgentSession — task snapshots', () => {
  it('applies task_created events to the running turn', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'ok' }], Closed: false, Timestamp: '2', TurnId: 't1' },
    ])
    const event: StepEvent = {
      Kind: 'step.task_created',
      StepId: 'task-1',
      TurnId: 't1',
      TaskId: 'task-1',
      Task: { id: 'task-1', subject: 'Fix auth', status: 'pending', activeForm: 'Fixing auth' },
    }
    layer.applyStepEvents([event])

    const snapshot = layer.getSnapshot()
    const asst = snapshot.envelopes.find(e => e.role === 'assistant')
    expect(asst).toBeDefined()
    expect(asst!.tasks).toHaveLength(1)
    expect(asst!.tasks![0]).toMatchObject({ id: 'task-1', subject: 'Fix auth', status: 'pending', activeForm: 'Fixing auth' })
  })

  it('updates existing tasks via task_updated events', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'ok' }], Closed: false, Timestamp: '1', TurnId: 't1' },
    ])
    layer.applyStepEvents([
      {
        Kind: 'step.task_created',
        StepId: 'task-1',
        TurnId: 't1',
        TaskId: 'task-1',
        Task: { id: 'task-1', subject: 'Fix auth', status: 'pending' },
      },
    ])
    layer.applyStepEvents([
      {
        Kind: 'step.task_updated',
        StepId: 'task-1',
        TurnId: 't1',
        TaskId: 'task-1',
        Task: { id: 'task-1', subject: 'Fix auth', status: 'in_progress', activeForm: 'Fixing auth' },
      },
    ])

    const asst = layer.getSnapshot().envelopes.find(e => e.role === 'assistant')
    expect(asst!.tasks![0]!.status).toBe('in_progress')
    expect(asst!.tasks![0]!.activeForm).toBe('Fixing auth')
  })

  it('preserves activeForm and subject when task_updated omits them', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'ok' }], Closed: false, Timestamp: '1', TurnId: 't1' },
    ])
    layer.applyStepEvents([
      {
        Kind: 'step.task_created',
        StepId: 'task-1',
        TurnId: 't1',
        TaskId: 'task-1',
        Task: { id: 'task-1', subject: 'Fix auth', status: 'pending', activeForm: 'Fixing auth' },
      },
    ])
    layer.applyStepEvents([
      {
        Kind: 'step.task_updated',
        StepId: 'task-1',
        TurnId: 't1',
        TaskId: 'task-1',
        Task: { id: 'task-1', subject: 'Fix auth', status: 'in_progress', activeForm: '' },
      },
    ])

    const asst = layer.getSnapshot().envelopes.find(e => e.role === 'assistant')
    expect(asst!.tasks![0]!.status).toBe('in_progress')
    expect(asst!.tasks![0]!.activeForm).toBe('Fixing auth')
    expect(asst!.tasks![0]!.subject).toBe('Fix auth')
  })

  it('removes tasks via task_deleted events', () => {
    const layer = new AgentSession('test-agent')
    layer.setSteps([
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'ok' }], Closed: false, Timestamp: '1', TurnId: 't1' },
    ])
    layer.applyStepEvents([
      {
        Kind: 'step.task_created',
        StepId: 'task-1',
        TurnId: 't1',
        TaskId: 'task-1',
        Task: { id: 'task-1', subject: 'Fix auth', status: 'pending' },
      },
    ])
    layer.applyStepEvents([
      {
        Kind: 'step.task_deleted',
        StepId: 'task-1',
        TurnId: 't1',
        TaskId: 'task-1',
        Task: { id: 'task-1' },
      },
    ])

    const asst = layer.getSnapshot().envelopes.find(e => e.role === 'assistant')
    expect(asst!.tasks == null || asst!.tasks.length === 0).toBe(true)
  })

  it('seeds task snapshots from history envelopes on load', () => {
    const layer = new AgentSession('test-agent')
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u1', role: 'user', frames: [], timestamp: '1', metadata: { turnId: 't1' } },
      { id: 'a1', role: 'assistant', frames: [], timestamp: '2', completed: true, metadata: { turnId: 't1' }, tasks: [{ id: 'task-1', subject: 'Fix auth', status: 'completed' }] },
    ]
    layer.initFromLoad([], historyEnvelopes, false)

    const asst = layer.getSnapshot().envelopes.find(e => e.role === 'assistant')
    expect(asst!.tasks).toHaveLength(1)
    expect(asst!.tasks![0]!.status).toBe('completed')
  })
})

// ── State-layer: appendOlderHistory ──

describe('AgentSession — appendOlderHistory', () => {
  it('merges older steps so history assistant turns render frames', () => {
    const layer = new AgentSession('test-agent')
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'u1', role: 'user', frames: [], timestamp: '1', metadata: { turnId: 't1' } },
      { id: 'a1', role: 'assistant', frames: [], timestamp: '2', completed: true, metadata: { turnId: 't1' } },
    ]
    layer.initFromLoad([], historyEnvelopes, true)

    const olderSteps: Step[] = [
      { Id: 'u1-step', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hello' }], Closed: true, TurnId: 't1' },
      { Id: 'a1-step', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'world' }], Closed: true, TurnId: 't1' },
    ]
    layer.appendOlderHistory([], true, olderSteps)

    const asst = layer.getSnapshot().envelopes.find(e => e.role === 'assistant' && e.metadata?.turnId === 't1')
    expect(asst).toBeDefined()
    expect(asst!.frames).toHaveLength(1)
    expect(asst!.frames[0]).toMatchObject({ type: 'text', content: 'world' })
  })

  it('ignores duplicate step IDs when merging older steps', () => {
    const layer = new AgentSession('test-agent')
    const existingStep: Step = { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'first' }], Closed: true, TurnId: 't1' }
    layer.initFromLoad([existingStep], [], true)

    const olderSteps: Step[] = [
      { Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'duplicate' }], Closed: true, TurnId: 't1' },
      { Id: 's2', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'second' }], Closed: true, TurnId: 't1' },
    ]
    layer.appendOlderHistory([], true, olderSteps)

    expect(layer.steps).toHaveLength(2)
    const ids = layer.steps.map(s => s.Id)
    expect(ids.filter(id => id === 's1')).toHaveLength(1)
  })

  it('updates hasMoreHistory and prepends older envelopes', () => {
    const layer = new AgentSession('test-agent')
    layer.initFromLoad([], [{ id: 'new', role: 'user', frames: [], timestamp: '2', metadata: { turnId: 't2' } }], true)

    layer.appendOlderHistory([{ id: 'old', role: 'user', frames: [], timestamp: '1', metadata: { turnId: 't1' } }], false)

    expect(layer.hasMoreHistory).toBe(false)
    const ids = layer.getSnapshot().envelopes.map(e => e.id)
    expect(ids[0]).toBe('old')
    expect(ids[1]).toBe('new')
  })
})

// ── computeEnvelopes: turn task snapshot injection ──

describe('computeEnvelopes — turn task snapshots', () => {
  beforeEach(() => {
  })

  it('injects real-time tasks into the running assistant envelope', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'ok' }], Closed: false, Timestamp: '2', TurnId: 't1' },
    ]
    const turnTasks = new Map([['t1', [{ id: 'task-1', subject: 'Fix auth', status: 'in_progress' as const, activeForm: 'Fixing auth' }]]])

    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      undefined,
      turnTasks,
    )

    const asst = result.find(e => e.role === 'assistant')
    expect(asst).toBeDefined()
    expect(asst!.tasks).toHaveLength(1)
    expect(asst!.tasks![0]).toMatchObject({ id: 'task-1', status: 'in_progress', activeForm: 'Fixing auth' })
  })

  it('does not overwrite existing history envelope tasks', () => {
    const historyEnvelopes: TurnEnvelope[] = [
      { id: 'a1', role: 'assistant', frames: [], timestamp: '1', completed: true, metadata: { turnId: 't1' }, tasks: [{ id: 'task-1', subject: 'History task', status: 'completed' as const }] },
    ]
    const turnTasks = new Map([['t1', [{ id: 'task-2', subject: 'Realtime task', status: 'in_progress' as const }]]])

    const tl = makeTimeline({ historyEnvelopes })
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      undefined,
      turnTasks,
    )

    const asst = result.find(e => e.role === 'assistant')
    expect(asst!.tasks).toHaveLength(1)
    expect(asst!.tasks![0]!.id).toBe('task-1')
  })
})

// ── computeEnvelopes: pending user message injection ──

describe('computeEnvelopes — pending user messages', () => {
  beforeEach(() => {
  })

  it('injects pending user messages as pendingSubmits into the running assistant envelope', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'ok' }], Closed: false, Timestamp: '2', TurnId: 't1' },
    ]
    const pendingUserMessages = [
      { clientId: 'client-1', text: 'hello', state: 'pending' as const, timestamp: '2026-01-01T00:00:00Z' },
    ]

    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      undefined,
      undefined,
      pendingUserMessages,
    )

    const asst = result.find(e => e.role === 'assistant')
    expect(asst).toBeDefined()
    expect(asst!.metadata?.pendingSubmits).toHaveLength(1)
    expect(asst!.metadata?.pendingSubmits![0]).toMatchObject({ id: 'client-1', text: 'hello' })
  })

  it('does not inject pendingSubmits into a completed assistant envelope', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'ok' }], Closed: true, Timestamp: '2', TurnId: 't1' },
    ]
    const pendingUserMessages = [
      { clientId: 'client-1', text: 'hello', state: 'pending' as const, timestamp: '2026-01-01T00:00:00Z' },
    ]

    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      undefined,
      undefined,
      pendingUserMessages,
    )

    // The original assistant envelope (id = turnId 't1') should NOT carry
    // pendingSubmits — they go onto the synthetic placeholder instead.
    const real = result.find(e => e.role === 'assistant' && e.id !== '__pending_queue__')
    expect(real).toBeDefined()
    expect(real!.metadata?.pendingSubmits).toBeUndefined()
  })

  // AUDIT 1.9: between user send and turn start there's no running assistant
  // envelope. Inject a synthetic placeholder so the optimistic UI still shows.

  it('appends a synthetic placeholder when no running assistant envelope exists (AUDIT 1.9)', () => {
    const steps: Step[] = [
      { Id: 'u1', Role: 'user', Type: 'text', Content: [{ Type: 'text', Text: 'hi' }], Closed: true, Timestamp: '1', TurnId: 't1' },
      { Id: 'a1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'ok' }], Closed: true, Timestamp: '2', TurnId: 't1' },
    ]
    const pendingUserMessages = [
      { clientId: 'client-1', text: 'next message', state: 'pending' as const, timestamp: '2026-01-01T00:00:03Z' },
    ]

    const tl = makeTimeline({ steps })
    const result = computeEnvelopes(
      tl.agentActorId,
      tl.layer.steps,
      tl.layer.historyEnvelopes,
      undefined,
      undefined,
      undefined,
      pendingUserMessages,
    )

    const placeholder = result.find(e => e.id === '__pending_queue__')
    expect(placeholder).toBeDefined()
    expect(placeholder!.role).toBe('assistant')
    expect(placeholder!.completed).toBe(false)
    expect(placeholder!.metadata?.pendingSubmits).toHaveLength(1)
    expect(placeholder!.metadata?.pendingSubmits![0]).toMatchObject({ id: 'client-1', text: 'next message' })
  })
})

describe('TimelineManager subscription isolation', () => {
  it('notifies only listeners for the agent that changes', async () => {
    const manager = createTimelineManager()

    manager.select('agent-a')
    manager.select('agent-b')
    await vi.waitFor(() => expect(manager.getTrackedAgents()).toContain('agent-b'))

    // Flush initial RAF batch from timeline creation before subscribing.
    await new Promise(r => setTimeout(r, 20))

    const callsA = vi.fn()
    const callsB = vi.fn()
    const callsGlobal = vi.fn()

    manager.subscribe(callsA, 'agent-a')
    manager.subscribe(callsB, 'agent-b')
    manager.subscribe(callsGlobal)

    // Re-select agent-a to trigger a state change + notification.
    manager.select('agent-a')

    // Wait for the RAF-batched notification to fire.
    await vi.waitFor(() => expect(callsA).toHaveBeenCalled())

    expect(callsB).not.toHaveBeenCalled()
    expect(callsGlobal).toHaveBeenCalled()
  })

  it('notifies all agents that changed in the same RAF tick', async () => {
    const manager = createTimelineManager()

    manager.select('agent-a')
    manager.select('agent-b')
    await vi.waitFor(() => expect(manager.getTrackedAgents()).toContain('agent-b'))

    const callsA = vi.fn()
    const callsB = vi.fn()
    manager.subscribe(callsA, 'agent-a')
    manager.subscribe(callsB, 'agent-b')

    // Agitate both agents synchronously in the same tick.
    const idA = manager.pushUserMessage('msg-a', 'agent-a')
    const idB = manager.pushUserMessage('msg-b', 'agent-b')
    expect(idA).toBeTruthy()
    expect(idB).toBeTruthy()

    callsA.mockClear()
    callsB.mockClear()

    // Both agents changed — both listeners must fire.
    manager.pushUserMessage('x', 'agent-a')
    manager.pushUserMessage('y', 'agent-b')

    await vi.waitFor(() => expect(callsA).toHaveBeenCalled())
    expect(callsB).toHaveBeenCalled()
  })
})

describe('TimelineManager LRU eviction', () => {
  beforeEach(() => {
    const manager = createTimelineManager()
    for (const id of manager.getTrackedAgents()) {
      manager.release(id)
    }
  })

  it('evicts the oldest timeline when selecting beyond cap (MAX_TRACKED_AGENTS=8)', () => {
    const manager = createTimelineManager()

    for (let i = 0; i < 8; i++) {
      manager.select(`agent-${i}`)
    }
    expect(manager.getTrackedAgents()).toHaveLength(8)

    manager.select('agent-new')

    const tracked = manager.getTrackedAgents()
    expect(tracked).not.toContain('agent-0')
    expect(tracked).toContain('agent-new')
    expect(tracked).toHaveLength(8)
  })

  it('re-selecting an existing timeline does not grow the tracked set', () => {
    const manager = createTimelineManager()

    manager.select('agent-a')
    manager.select('agent-b')
    expect(manager.getTrackedAgents()).toHaveLength(2)

    manager.select('agent-a')

    expect(manager.getTrackedAgents()).toHaveLength(2)
    expect(manager.getSelectedId()).toBe('agent-a')
  })

  it('user interaction promotes timeline to most-recently-used', () => {
    const manager = createTimelineManager()

    for (let i = 0; i < 8; i++) {
      manager.select(`agent-${i}`)
    }

    // Touch agent-0 via pushUserMessage — promotes it to MRU.
    manager.pushUserMessage('hello', 'agent-0')

    // Select a 9th — should evict agent-1 (now the oldest), not agent-0.
    manager.select('agent-new')

    const tracked = manager.getTrackedAgents()
    expect(tracked).toContain('agent-0')
    expect(tracked).not.toContain('agent-1')
    expect(tracked).toContain('agent-new')
  })

  it('release removes the timeline and clears its snapshot', () => {
    const manager = createTimelineManager()

    manager.select('agent-a')
    expect(manager.getTrackedAgents()).toContain('agent-a')

    manager.release('agent-a')

    expect(manager.getTrackedAgents()).not.toContain('agent-a')
    const snap = manager.getSnapshot('agent-a')
    expect(snap.envelopes).toEqual([])
    expect(snap.loading).toBe(false)
  })
})

describe('TimelineManager select — retry on prior error', () => {
  beforeEach(() => {
    const manager = createTimelineManager()
    for (const id of manager.getTrackedAgents()) {
      manager.release(id)
    }
    vi.mocked(client['transport'].subscribe).mockClear()
  })

  it('re-selecting a timeline that previously errored clears error and re-loads', async () => {
    const manager = createTimelineManager()

    // First load: make the step-subscribe throw so loadTimeline's catch sets
    // a persistent error on the layer. "connection refused" is non-transient
    // per isTransientError, so event-layer retry loop does NOT start — the
    // only way to recover is re-selecting the agent.
    vi.mocked(client['transport'].subscribe).mockImplementationOnce(() => {
      throw new Error('connection refused')
    })

    manager.select('agent-a')
    await vi.waitFor(() => {
      expect(manager.getSnapshot('agent-a').error).toBeTruthy()
    })
    expect(manager.getSnapshot('agent-a').error).toContain('connection refused')
    expect(manager.getSnapshot('agent-a').loading).toBe(false)

    // Re-select should fire the retry branch: clear error, set loading true,
    // and kick off a fresh loadTimeline (which calls subscribe again).
    const callsBefore = vi.mocked(client['transport'].subscribe).mock.calls.length
    manager.select('agent-a')

    const snap = manager.getSnapshot('agent-a')
    expect(snap.error).toBeNull()
    expect(snap.loading).toBe(true)

    // A fresh loadTimeline subscribes twice (step + turn). The original load
    // aborted after the first throw, so the delta is exactly 2.
    await vi.waitFor(() => {
      expect(vi.mocked(client['transport'].subscribe).mock.calls.length).toBe(callsBefore + 2)
    })
  })
})

// ── L3: realLoading safety timeout ──
// SummaryReconciler.onSettle has known early-return paths (aborted,
// superseded) that skip the callback; the safety timeout is the only escape
// from an infinite spinner if the reconciler also never retries.

describe('TimelineManager — realLoading safety timeout', () => {
  beforeEach(() => {
    const manager = createTimelineManager()
    for (const id of manager.getTrackedAgents()) {
      manager.release(id)
    }
    vi.mocked(client['transport'].subscribe).mockClear()
  })

  it('force-clears realLoading after 30s when onSettle never fires', () => {
    vi.useFakeTimers()
    try {
      const manager = createTimelineManager()
      // Make the summary RPC throw a transient error so fetchWithRetry
      // exhausts 3 attempts without onSettle firing — only the safety timeout
      // can then clear realLoading.
      vi.mocked(agentSessionClient.sessionSummary).mockImplementation(async () => {
        throw new Error('service not found')
      })

      manager.select('agent-stuck')
      // select() synchronously sets realLoading=true + arms the safety timeout.
      expect(manager.getSnapshot('agent-stuck').loading).toBe(true)

      // Advance through the 3 summary retries (~7s of backoff) + the 30s
      // safety timeout.
      vi.advanceTimersByTime(60_000)

      const snap = manager.getSnapshot('agent-stuck')
      expect(snap.loading).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })

  it('disarms timer on clearAgentTimeline (no leak)', () => {
    vi.useFakeTimers()
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      const manager = createTimelineManager()
      manager.select('agent-evicted')
      expect(manager.getTrackedAgents()).toContain('agent-evicted')

      // Evict the timeline — should disarm the safety timeout.
      manager.release('agent-evicted')
      expect(manager.getTrackedAgents()).not.toContain('agent-evicted')

      // Advance well past the safety threshold — no warn should fire because
      // the timer was cleared on release.
      vi.advanceTimersByTime(60_000)
      const safetyWarns = warnSpy.mock.calls.filter(
        c => typeof c[0] === 'string' && c[0].includes('realLoading safety timeout'),
      )
      expect(safetyWarns).toHaveLength(0)
    } finally {
      warnSpy.mockRestore()
      vi.useRealTimers()
    }
  })
})

// ── Transport reconnect reconcile ──
// The transport-level reconnect triggered by forceReconnectClient on
// Capacitor foreground resume (appStateChange / visibilitychange) is
// transparent to subscribe iterators — they never return done and are
// auto-resumed via resumeSubscriptions — so the stream-loop onReconnect path
// is never entered. timeline-manager registers a module-level
// transport.onConnected listener that must re-fetch every tracked timeline's
// summary so a turn that completed while the app was backgrounded is pruned
// from the active-turn map instead of staying stuck in 'running'.

describe('TimelineManager — transport reconnect reconcile', () => {
  const emptySummary: AgentSessionSummaryResp = {
    Turns: [],
    Steps: [],
    ActiveTurn: {} as any,
    ActiveTurnEvents: [],
    TotalTurns: 0,
    HasMoreHistory: false,
  }

  beforeEach(() => {
    resetReconnectThrottleForTest()
    const manager = createTimelineManager()
    for (const id of manager.getTrackedAgents()) {
      manager.release(id)
    }
    vi.mocked(client['transport'].subscribe).mockClear()
    vi.mocked(agentSessionClient.sessionSummary).mockReset()
  })

  function fireReconnect(isReconnect: boolean): void {
    const transport = client.getTransport() as unknown as {
      _onConnectedHandlers: Array<(i: { isReconnect: boolean }) => void>
    }
    for (const h of transport._onConnectedHandlers) {
      h({ isReconnect })
    }
  }

  it('transport onConnected(isReconnect=true) triggers a summary reconcile for tracked timelines', async () => {
    vi.mocked(agentSessionClient.sessionSummary).mockResolvedValue(emptySummary)
    const manager = createTimelineManager()
    manager.select('agent-a')

    await vi.waitFor(() => {
      expect(vi.mocked(agentSessionClient.sessionSummary).mock.calls.length).toBeGreaterThanOrEqual(1)
    })

    // Simulate the transport reconnecting after a Capacitor foreground resume.
    vi.mocked(agentSessionClient.sessionSummary).mockClear()
    fireReconnect(true)

    // The reconnect listener must fire a fresh summary fetch for the tracked
    // timeline (mode='reconnect'), which prunes any stale active-turn state.
    await vi.waitFor(() => {
      expect(vi.mocked(agentSessionClient.sessionSummary).mock.calls.length).toBeGreaterThanOrEqual(1)
    })
  })

  it('does NOT reconcile on the initial connect (isReconnect=false)', async () => {
    vi.mocked(agentSessionClient.sessionSummary).mockResolvedValue(emptySummary)
    const manager = createTimelineManager()
    manager.select('agent-a')

    await vi.waitFor(() => {
      expect(vi.mocked(agentSessionClient.sessionSummary).mock.calls.length).toBeGreaterThanOrEqual(1)
    })

    vi.mocked(agentSessionClient.sessionSummary).mockClear()
    fireReconnect(false)

    // Flush any pending microtasks / timers.
    await new Promise((r) => setTimeout(r, 10))
    expect(vi.mocked(agentSessionClient.sessionSummary)).not.toHaveBeenCalled()
  })

  it('transport onConnected(reconnect) runs a bounded ladder until the active turn converges', async () => {
    vi.useFakeTimers()
    try {
      const runningSummary: AgentSessionSummaryResp = {
        ...emptySummary,
        ActiveTurn: { Turn: { Id: 't1', Role: 'assistant', State: 'running' } } as any,
      }
      const doneSummary: AgentSessionSummaryResp = {
        ...emptySummary,
        ActiveTurn: { Turn: { Id: 't1', Role: 'assistant', State: 'completed' } } as any,
      }
      // select() consumes the init fetch; the reconnect ladder then gets a
      // first rung that still reports the active turn running (snapshot taken
      // before the turn completed inside the resubscribe window) and a second
      // rung that has converged. Anything past the ladder bound must not fire.
      vi.mocked(agentSessionClient.sessionSummary)
        .mockResolvedValueOnce(runningSummary)
        .mockResolvedValueOnce(runningSummary)
        .mockResolvedValueOnce(doneSummary)
        .mockResolvedValue(doneSummary)

      const manager = createTimelineManager()
      manager.select('agent-a')
      await vi.advanceTimersByTimeAsync(0)
      expect(vi.mocked(agentSessionClient.sessionSummary)).toHaveBeenCalledTimes(1)

      fireReconnect(true)
      await vi.advanceTimersByTimeAsync(0)
      expect(vi.mocked(agentSessionClient.sessionSummary)).toHaveBeenCalledTimes(2)

      // First ladder backoff (500ms) elapses → second rung → converged.
      await vi.advanceTimersByTimeAsync(600)
      expect(vi.mocked(agentSessionClient.sessionSummary)).toHaveBeenCalledTimes(3)

      // Converged early — nothing more fires, even far past the full bound.
      await vi.advanceTimersByTimeAsync(60_000)
      expect(vi.mocked(agentSessionClient.sessionSummary)).toHaveBeenCalledTimes(3)

      manager.release('agent-a')
    } finally {
      vi.useRealTimers()
    }
  })

  it('reconnect storm: ≤cap ladders in flight, active agent first, LRU order, all agents refreshed', async () => {
    // Deferred-promise sessionSummary mock so the test can observe exactly how
    // many ladders are concurrently in flight and in what order they fired.
    const callTargets: string[] = []
    const pending: Array<{ target: string; resolve: (v: AgentSessionSummaryResp) => void }> = []
    let maxInFlight = 0
    const inFlight = new Set<string>()
    vi.mocked(agentSessionClient.sessionSummary).mockImplementation((_client, _params, opts) => {
      const target = (opts as { target: string }).target
      callTargets.push(target)
      inFlight.add(target)
      maxInFlight = Math.max(maxInFlight, inFlight.size)
      let resolve!: (v: AgentSessionSummaryResp) => void
      const p = new Promise<AgentSessionSummaryResp>(r => { resolve = r })
      pending.push({ target, resolve })
      void p.finally(() => { inFlight.delete(target) })
      return p
    })

    const manager = createTimelineManager()
    const agents = ['agent-a', 'agent-b', 'agent-c', 'agent-d', 'agent-e', 'agent-f', 'agent-g', 'agent-h']
    for (const id of agents) manager.select(id)
    // agent-h was selected last → it is the active agent.

    // Drain the 8 init fetches from select() so the reconnect round starts
    // from a clean call counter.
    await vi.waitFor(() => expect(callTargets).toHaveLength(8))
    for (const d of pending.splice(0)) d.resolve(emptySummary)
    await vi.waitFor(() => expect(inFlight.size).toBe(0))
    callTargets.length = 0
    maxInFlight = 0

    // One reconnect storms all 8 tracked agents. The scheduler admits the
    // active agent first, then LRU recency, capped at RECONNECT_SUMMARY_CONCURRENCY.
    fireReconnect(true)
    await vi.waitFor(() => expect(callTargets.length).toBe(3))
    expect(maxInFlight).toBeLessThanOrEqual(RECONNECT_SUMMARY_CONCURRENCY)
    // Active agent's fetch fired first; the next two are the most-recently-used.
    expect(callTargets[0]).toBe('agent-h')
    expect(callTargets).toEqual(['agent-h', 'agent-g', 'agent-f'])

    // Free one slot at a time: each freed slot admits the next queued agent
    // (LRU order) and in-flight never exceeds the cap.
    let resolved = 0
    while (callTargets.length < agents.length) {
      const expectedAfter = callTargets.length + 1
      pending[resolved]!.resolve(emptySummary)
      resolved++
      await vi.waitFor(() => expect(callTargets.length).toBe(expectedAfter))
      expect(maxInFlight).toBeLessThanOrEqual(RECONNECT_SUMMARY_CONCURRENCY)
    }
    // Resolve the remaining in-flight fetches so no reconciler is left dangling.
    for (; resolved < pending.length; resolved++) {
      pending[resolved]!.resolve(emptySummary)
    }
    await vi.waitFor(() => expect(inFlight.size).toBe(0))

    // Every tracked agent was eventually refreshed, in strict priority order.
    expect(callTargets).toEqual([...agents].reverse())
    expect(new Set(callTargets).size).toBe(agents.length)
    expect(maxInFlight).toBeLessThanOrEqual(RECONNECT_SUMMARY_CONCURRENCY)
  })

  it('interior seq hole after a long-disconnect reconnect fills from the hole cursor, not the oldest envelope', async () => {
    const mkStep = (seq: number, turnId: string): Step => ({
      Id: `s-${seq}`,
      Role: 'assistant',
      Type: 'text',
      Content: [{ Type: 'text', Text: `body-${seq}` }],
      Closed: true,
      Timestamp: new Date(Date.now() + seq).toISOString(),
      TurnId: turnId,
      Seq: seq,
    } as any)
    const mkTurn = (id: string) => ({ Id: id, Role: 'assistant', State: 'completed' })

    // Before the disconnect the client streamed turns t1-t2 (seqs 1-3).
    const oldSummary: AgentSessionSummaryResp = {
      Turns: [mkTurn('t1'), mkTurn('t2')],
      Steps: [mkStep(1, 't1'), mkStep(2, 't2'), mkStep(3, 't2')],
      ActiveTurn: {} as any,
      ActiveTurnEvents: [],
      TotalTurns: 2,
      HasMoreHistory: true,
    }
    // A disconnect longer than the backend's 50-turn reconnect window: the
    // reconnect summary carries only the newest turns (seqs 11-12). The
    // global watermark check cannot see the middle hole (NextSeq-1 == 12 ==
    // merged maxLocalSeq) — only findInteriorHoleCursor catches it.
    const newSummary: AgentSessionSummaryResp = {
      Turns: [mkTurn('t11'), mkTurn('t12')],
      Steps: [mkStep(11, 't11'), mkStep(12, 't12')],
      ActiveTurn: {} as any,
      ActiveTurnEvents: [],
      TotalTurns: 12,
      HasMoreHistory: true,
      NextSeq: 13,
    }

    vi.mocked(agentSessionClient.sessionSummary)
      .mockResolvedValueOnce(oldSummary) // init fetch
      .mockResolvedValue(newSummary)     // reconnect rungs
    // Round 1 (anchored on the hole's newer side t11): partial fill, seqs
    // 8-10 — the hole shrinks to 3→8. Round 2 (anchored on t9, the new
    // hole's newer side): seqs 4-7 — the hole closes and the loop stops.
    vi.mocked(agentSessionClient.turnsList).mockReset()
    vi.mocked(agentSessionClient.turnsList)
      .mockResolvedValueOnce({
        Turns: [mkTurn('t9'), mkTurn('t10')],
        Steps: [mkStep(8, 't9'), mkStep(9, 't10'), mkStep(10, 't10')],
        HasMore: true,
      })
      .mockResolvedValue({
        Turns: [mkTurn('t4'), mkTurn('t5')],
        Steps: [mkStep(4, 't4'), mkStep(5, 't5'), mkStep(6, 't5'), mkStep(7, 't5')],
        HasMore: true,
      })

    const manager = createTimelineManager()
    manager.select('agent-a')
    await vi.waitFor(() => {
      expect(vi.mocked(agentSessionClient.sessionSummary).mock.calls.length).toBeGreaterThanOrEqual(1)
    })

    fireReconnect(true)

    // Both gap-fill rounds must anchor on the hole's newer side (t11, then
    // t9) — never on the oldest envelope (t1), which would page before t1
    // forever and never fill the middle hole.
    await vi.waitFor(() => {
      expect(vi.mocked(agentSessionClient.turnsList)).toHaveBeenCalledTimes(2)
    })
    expect(vi.mocked(agentSessionClient.turnsList).mock.calls[0]![1]).toMatchObject({ BeforeTurnId: 't11' })
    expect(vi.mocked(agentSessionClient.turnsList).mock.calls[1]![1]).toMatchObject({ BeforeTurnId: 't9' })

    // Hole closed (seqs 1-12 contiguous) — no further rounds even after
    // settling.
    await new Promise((r) => setTimeout(r, 50))
    expect(vi.mocked(agentSessionClient.turnsList)).toHaveBeenCalledTimes(2)

    manager.release('agent-a')
  })
})


