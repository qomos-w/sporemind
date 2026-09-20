import { describe, expect, it } from 'vitest'
import { sessionTurnsToEnvelopes } from './session-adapter'
import type { Turn } from '../../../gen-types/aigen'

describe('sessionTurnsToEnvelopes', () => {
  it('generates metadata-only assistant envelope (frames come from Steps)', () => {
    const turn: Turn = {
      Id: 'turn-1',
      Role: 'assistant',
      State: 'completed',
      Timestamp: '2026-05-26T10:00:00Z',
      StartedAt: '2026-05-26T10:00:00Z',
      CompletedAt: '2026-05-26T10:00:05Z',
      Seq: 1,
      Tasks: [
        { Id: 'task-1', Subject: 'Do X', Status: 'completed', ActiveForm: 'Doing X' },
        { Id: 'task-2', Subject: 'Do Y', Status: 'pending' },
      ],
      FileChanges: [
        {
          Path: 'src/foo.ts',
          Additions: 10,
          Deletions: 3,
          DiffContent: 'unified diff',
        },
      ],
      Usage: {
        InputTokens: 100,
        OutputTokens: 200,
        TotalTokens: 300,
      },
      ContextBudget: {
        EstimatedTokens: 500,
        ContextWindowSize: 8000,
        TokenBudget: 4000,
      },
      Unit: { model: 'gpt-4', provider: 'openai' },
    }

    const result = sessionTurnsToEnvelopes([turn])
    expect(result).toHaveLength(1)
    const envelope = result[0]!

    // After Phase 1 flattening, frames come from Steps (via stepsToEnvelopes).
    // The Turn adapter only provides metadata.
    expect(envelope.id).toBe('turn-1')
    expect(envelope.role).toBe('assistant')
    expect(envelope.frames).toHaveLength(0)
    expect(envelope.completed).toBe(true)
    expect(envelope.turnSeq).toBe(1)

    // Tasks should be preserved.
    expect(envelope.tasks).toHaveLength(2)
    expect(envelope.tasks![0]).toMatchObject({
      id: 'task-1',
      subject: 'Do X',
      status: 'completed',
      activeForm: 'Doing X',
    })
    expect(envelope.tasks![1]).toMatchObject({
      id: 'task-2',
      subject: 'Do Y',
      status: 'pending',
    })

    // FileChanges should be preserved.
    expect(envelope.fileChanges).toHaveLength(1)
    expect(envelope.fileChanges![0]).toMatchObject({
      filepath: 'src/foo.ts',
      additions: 10,
      deletions: 3,
      diffContent: 'unified diff',
    })

    // Usage should be preserved.
    expect(envelope.metadata?.usage).toEqual({
      inputTokens: 100,
      outputTokens: 200,
      totalTokens: 300,
    })

    // ContextBudget should be preserved.
    expect(envelope.metadata?.contextBudget).toEqual({
      estimatedTokens: 500,
      contextWindowSize: 8000,
      tokenBudget: 4000,
    })

    expect(envelope.metadata?.model).toBe('gpt-4')
    expect(envelope.metadata?.turnId).toBe('turn-1')
    expect(envelope.metadata?.startedAt).toBe('2026-05-26T10:00:00Z')
    expect(envelope.metadata?.completedAt).toBe('2026-05-26T10:00:05Z')
  })

  it('generates user envelope with UserInput', () => {
    const userTurn: Turn = {
      Id: 'turn-user',
      Role: 'user',
      State: 'completed',
      Timestamp: '2026-05-26T10:00:00Z',
      UserInput: 'hello',
    }

    const result = sessionTurnsToEnvelopes([userTurn])
    expect(result).toHaveLength(1)
    const envelope = result[0]!
    expect(envelope.id).toBe('turn-user')
    expect(envelope.role).toBe('user')
    expect(envelope.frames).toHaveLength(1)
    expect(envelope.frames[0]).toMatchObject({ type: 'text', content: 'hello', status: 'completed' })
  })

  it('keeps a running compact user turn live (compaction in progress)', () => {
    const turn: Turn = {
      Id: 'turn-compact',
      Role: 'user',
      State: 'running',
      Timestamp: '2026-05-26T10:00:00Z',
      StartedAt: '2026-05-26T10:00:00Z',
      Revision: 1,
      TurnOrder: 4,
      UserInput: '/compact',
    }
    const result = sessionTurnsToEnvelopes([turn])
    expect(result[0]!.completed).toBe(false)
    expect(result[0]!.metadata?.turnState).toBe('running')
    expect(result[0]!.metadata?.revision).toBe(1)
  })

  it('carries a failed compact user turn terminal state and error', () => {
    const turn: Turn = {
      Id: 'turn-compact',
      Role: 'user',
      State: 'failed',
      Timestamp: '2026-05-26T10:00:00Z',
      StartedAt: '2026-05-26T10:00:00Z',
      CompletedAt: '2026-05-26T10:00:09Z',
      Revision: 2,
      UserInput: '/compact',
      Error: 'round 1 summarize failed: boom',
    }
    const result = sessionTurnsToEnvelopes([turn])
    expect(result[0]!.completed).toBe(true)
    expect(result[0]!.metadata?.turnState).toBe('failed')
    expect(result[0]!.metadata?.error).toBe('round 1 summarize failed: boom')
  })

  it('carries Turn.Seq through to envelope seq', () => {
    const userTurn: Turn = {
      Id: 'turn-user',
      Role: 'user',
      State: 'completed',
      Timestamp: '2026-05-26T10:00:00Z',
      Seq: 7,
      UserInput: 'hello',
    }
    const assistantTurn: Turn = {
      Id: 'turn-assistant',
      Role: 'assistant',
      State: 'completed',
      Timestamp: '2026-05-26T10:00:01Z',
      Seq: 8,
    }
    const result = sessionTurnsToEnvelopes([userTurn, assistantTurn])
    expect(result).toHaveLength(2)
    expect(result.find(e => e.role === 'user')?.turnSeq).toBe(7)
    expect(result.find(e => e.role === 'assistant')?.turnSeq).toBe(8)
  })

  it('keeps paused turns active and resumable', () => {
    const turn: Turn = {
      Id: 'turn-paused',
      Role: 'assistant',
      State: 'paused',
      Timestamp: '2026-05-26T10:00:00Z',
    }
    const result = sessionTurnsToEnvelopes([turn])
    expect(result[0]!.completed).toBe(false)
    expect(result[0]!.metadata?.turnState).toBe('paused')
  })

  it('marks cancelled turns as completed', () => {
    const turn: Turn = {
      Id: 'turn-cancelled',
      Role: 'assistant',
      State: 'cancelled',
      Timestamp: '2026-05-26T10:00:00Z',
      Cancelled: true,
    }
    const result = sessionTurnsToEnvelopes([turn])
    expect(result).toHaveLength(1)
    expect(result[0]!.completed).toBe(true)
  })

  it('marks failed turns as completed (closed)', () => {
    const turn: Turn = {
      Id: 'turn-failed',
      Role: 'assistant',
      State: 'failed',
      Timestamp: '2026-05-26T10:00:00Z',
      Error: 'something went wrong',
    }
    const result = sessionTurnsToEnvelopes([turn])
    expect(result).toHaveLength(1)
    expect(result[0]!.completed).toBe(true)
    // The error must survive into the history envelope's metadata so TurnTail
    // keeps showing it after pruneActiveTurnStates drops the live entry.
    expect(result[0]!.metadata?.turnState).toBe('failed')
    expect(result[0]!.metadata?.error).toBe('something went wrong')
  })

  it('marks waiting turns as completed (terminal, not live)', () => {
    const turn: Turn = {
      Id: 'turn-waiting',
      Role: 'assistant',
      State: 'waiting',
      Timestamp: '2026-05-26T10:00:00Z',
    }
    const result = sessionTurnsToEnvelopes([turn])
    expect(result).toHaveLength(1)
    expect(result[0]!.completed).toBe(true)
    expect(result[0]!.metadata?.turnState).toBe('waiting')
  })
})
