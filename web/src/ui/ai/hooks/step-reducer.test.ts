import { describe, it, expect, vi } from 'vitest'
import { stepReducer, stepReducerBatch, getExecutionProgressAcc } from './step-reducer'
import type { Step, StepEvent } from '../../../gen-types/aigen'

function makeEvent(overrides: Partial<StepEvent>): StepEvent {
  return {
    Kind: 'step.opened',
    StepId: 's1',
    TurnId: 't1',
    ...overrides,
  } as StepEvent
}

describe('stepReducer', () => {
  it('handles step.opened', () => {
    const steps: Step[] = []
    const ev = makeEvent({
      Kind: 'step.opened',
      StepId: 's1',
      StepType: 'text',
      Role: 'assistant',
      Block: { Type: 'text', Text: 'hello' },
    })
    const result = stepReducer(steps, ev)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      Id: 's1',
      Role: 'assistant',
      Type: 'text',
      Closed: false,
      Content: [{ Type: 'text', Text: 'hello' }],
      TurnId: 't1',
    })
  })

  it('ignores duplicate step.opened for same StepId', () => {
    // AUDIT 4.4: idempotency is by StepId only. StepIds are server-generated
    // UUIDs and globally unique; the prior (StepId, TurnId) check was
    // inconsistent with mergeSteps / cache keying and could split state.
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({ Kind: 'step.opened', StepId: 's1', TurnId: 't1' })
    const result = stepReducer(steps, ev)
    expect(result).toHaveLength(1)
    expect(result[0]!.Content).toHaveLength(0)
  })

  it('ignores duplicate step.opened even when TurnId differs (AUDIT 4.4)', () => {
    // A replayed step.opened carrying a stale TurnId must NOT create a second
    // step — that would put two entries with the same Id into the cache and
    // cause state divergence between reducer / mergeSteps / projection.
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({ Kind: 'step.opened', StepId: 's1', TurnId: 't2' })
    const result = stepReducer(steps, ev)
    expect(result).toHaveLength(1)
    expect(result[0]!.TurnId).toBe('t1') // original TurnId preserved
  })

  it('handles block.appended', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'tool_call', Content: [], Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({
      Kind: 'block.appended',
      StepId: 's1',
      Block: { Type: 'tool_use', ToolName: 'project.read', Input: '{"path": "/tmp"}' },
    })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Content).toHaveLength(1)
    expect(result[0]!.Content[0]!).toMatchObject({
      Type: 'tool_use', ToolName: 'project.read', Input: '{"path": "/tmp"}',
    })
  })

  it('handles block.delta on correct block index', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text',
      Content: [{ Type: 'text', Text: 'hel' }],
      Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({
      Kind: 'block.delta',
      StepId: 's1',
      BlockIndex: 0,
      Delta: 'lo',
    })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Content[0]!.Text).toBe('hello')
  })

  it('ignores block.delta with out-of-range index', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text',
      Content: [{ Type: 'text', Text: 'hello' }],
      Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({
      Kind: 'block.delta',
      StepId: 's1',
      BlockIndex: 5,
      Delta: 'x',
    })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Content[0]!.Text).toBe('hello')
  })

  // step.reset: a retried dispatch (mid-stream cut) clears the partial output
  // so the regenerated stream replaces rather than concatenates.
  it('step.reset clears streamed text and reasoning content, keeping the step open', () => {
    const steps: Step[] = [
      {
        Id: 'llm-1', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'partial' }],
        Closed: false, TurnId: 't1',
      },
      {
        Id: 'rs-1', Role: 'assistant', Type: 'reasoning',
        Content: [], ReasoningContent: 'partial thinking',
        Closed: false, TurnId: 't1',
      },
    ]
    const resetLlm = makeEvent({ Kind: 'step.reset', StepId: 'llm-1', EventSeq: 10 })
    const resetRs = makeEvent({ Kind: 'step.reset', StepId: 'rs-1', EventSeq: 11 })
    let result = stepReducer(steps, resetLlm)
    result = stepReducer(result, resetRs)

    expect(result[0]!.Content[0]!.Text).toBe('')
    expect(result[0]!.Closed).toBe(false)
    expect(result[1]!.ReasoningContent).toBe('')
    expect(result[1]!.Closed).toBe(false)

    // The regenerated stream re-fills the same step from scratch.
    const regen = makeEvent({ Kind: 'block.delta', StepId: 'llm-1', BlockIndex: 0, Delta: 'full', EventSeq: 12 })
    result = stepReducer(result, regen)
    expect(result[0]!.Content[0]!.Text).toBe('full')
  })

  it('step.reset skips discarded steps', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text',
      Content: [{ Type: 'text', Text: 'kept' }],
      Closed: false, TurnId: 't1', Discarded: true,
    } as Step]
    const ev = makeEvent({ Kind: 'step.reset', StepId: 's1' })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Content[0]!.Text).toBe('kept')
  })

  it('step.reset is deduplicated by EventSeq (replayed reset cannot wipe regenerated content)', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text',
      Content: [{ Type: 'text', Text: 'regenerated' }],
      Closed: false, TurnId: 't1',
    }]
    const seqTracking = new Map<string, number>()
    const fresh = makeEvent({ Kind: 'block.delta', StepId: 's1', BlockIndex: 0, Delta: 'regenerated', EventSeq: 12 })
    stepReducer(steps, fresh, seqTracking)

    const stale = makeEvent({ Kind: 'step.reset', StepId: 's1', EventSeq: 10 })
    const result = stepReducer(steps, stale, seqTracking)
    expect(result[0]!.Content[0]!.Text).toBe('regenerated')
  })

  it('handles step.closed', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({ Kind: 'step.closed', StepId: 's1' })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Closed).toBe(true)
  })

  // AUDIT 4.2: step.closed without Usage in the event must not wipe an
  // existing Usage value (set by an earlier event or a summary snapshot).

  it('step.closed preserves existing Usage when event has none (AUDIT 4.2)', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
      Usage: { InputTokens: 100, OutputTokens: 50 },
    } as Step]
    const ev = makeEvent({ Kind: 'step.closed', StepId: 's1' })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Usage).toEqual({ InputTokens: 100, OutputTokens: 50 })
    expect(result[0]!.Closed).toBe(true)
  })

  it('step.closed overwrites Usage when event carries a newer value', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
      Usage: { InputTokens: 100, OutputTokens: 50 },
    } as Step]
    const ev = makeEvent({
      Kind: 'step.closed',
      StepId: 's1',
      Usage: { InputTokens: 200, OutputTokens: 80 } as any,
    })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Usage).toEqual({ InputTokens: 200, OutputTokens: 80 })
  })

  it('block.appended does not restore content to a discarded step', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: true,
      Discarded: true, TurnId: 't1',
    }]
    const ev = makeEvent({
      Kind: 'block.appended', StepId: 's1',
      Block: { Type: 'text', Text: 'should be ignored' },
    })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Discarded).toBe(true)
    expect(result[0]!.Content).toHaveLength(0)
  })

  it('block.delta does not restore content to a discarded step', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: true,
      Discarded: true, TurnId: 't1',
    }]
    const ev = makeEvent({
      Kind: 'block.delta', StepId: 's1', BlockIndex: 0, Delta: 'should be ignored',
    })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Discarded).toBe(true)
    expect(result[0]!.Content).toHaveLength(0)
  })

  it('handles step.error', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({ Kind: 'step.error', StepId: 's1', Error: 'timeout' })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Closed).toBe(true)
    expect(result[0]!.Error).toBe('timeout')
  })

  it('returns unchanged for unknown event kind', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({ Kind: 'unknown.kind' as any, StepId: 's1' })
    const result = stepReducer(steps, ev)
    expect(result).toEqual(steps)
  })

  it('returns unchanged for step.file_changes without warning', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
    }]
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const ev = makeEvent({ Kind: 'step.file_changes' as any, StepId: 's1' })
    const result = stepReducer(steps, ev)
    expect(result).toEqual(steps)
    expect(warnSpy).not.toHaveBeenCalled()
    warnSpy.mockRestore()
  })

  it('ignores block.appended without Block', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [{ Type: 'text', Text: 'a' }], Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({ Kind: 'block.appended', StepId: 's1' })
    const result = stepReducer(steps, ev)
    expect(result[0]!.Content).toHaveLength(1)
  })
})

describe('stepReducerBatch', () => {
  it('processes a full text-generation sequence', () => {
    const steps: Step[] = []
    const events: StepEvent[] = [
      makeEvent({ Kind: 'step.opened', StepId: 'llm-1', StepType: 'text', Role: 'assistant' }),
      makeEvent({ Kind: 'block.appended', StepId: 'llm-1', Block: { Type: 'text', Text: '' } }),
      makeEvent({ Kind: 'block.delta', StepId: 'llm-1', BlockIndex: 0, Delta: 'Hello' }),
      makeEvent({ Kind: 'block.delta', StepId: 'llm-1', BlockIndex: 0, Delta: ' world' }),
      makeEvent({ Kind: 'step.closed', StepId: 'llm-1' }),
    ]
    const result = stepReducerBatch(steps, events)
    expect(result).toHaveLength(1)
    expect(result[0]!).toMatchObject({
      Id: 'llm-1',
      Role: 'assistant',
      Type: 'text',
      Closed: true,
    })
    expect(result[0]!.Content[0]!.Text).toBe('Hello world')
  })

  it('processes a tool-call sequence', () => {
    const steps: Step[] = []
    const events: StepEvent[] = [
      makeEvent({ Kind: 'step.opened', StepId: 'tool-1', StepType: 'tool_call', Role: 'assistant' }),
      makeEvent({
        Kind: 'block.appended', StepId: 'tool-1',
        Block: { Type: 'tool_use', ToolName: 'project.list_json', Input: '{"path": "/"}' },
      }),
      makeEvent({
        Kind: 'block.appended', StepId: 'tool-1',
        Block: { Type: 'tool_result', Text: 'bin  etc  home' },
      }),
      makeEvent({ Kind: 'step.closed', StepId: 'tool-1' }),
    ]
    const result = stepReducerBatch(steps, events)
    expect(result).toHaveLength(1)
    expect(result[0]!.Type).toBe('tool_call')
    expect(result[0]!.Content).toHaveLength(2)
    expect(result[0]!.Content[0]!.Type).toBe('tool_use')
    expect(result[0]!.Content[1]!.Type).toBe('tool_result')
    expect(result[0]!.Content[1]!.Text).toBe('bin  etc  home')
    expect(result[0]!.Closed).toBe(true)
  })

  it('processes interleaved llm + tool steps', () => {
    const steps: Step[] = []
    const events: StepEvent[] = [
      makeEvent({ Kind: 'step.opened', StepId: 'llm-1', StepType: 'text', Role: 'assistant' }),
      makeEvent({ Kind: 'block.delta', StepId: 'llm-1', BlockIndex: 0, Delta: 'Let me check' }),
      makeEvent({ Kind: 'step.closed', StepId: 'llm-1' }),
      makeEvent({ Kind: 'step.opened', StepId: 'tool-1', StepType: 'tool_call', Role: 'assistant' }),
      makeEvent({ Kind: 'block.appended', StepId: 'tool-1', Block: { Type: 'tool_use', ToolName: 'project.shell_exec', Input: '{"command": "ls"}' } }),
      makeEvent({ Kind: 'step.closed', StepId: 'tool-1' }),
      makeEvent({ Kind: 'step.opened', StepId: 'llm-2', StepType: 'text', Role: 'assistant' }),
      makeEvent({ Kind: 'block.delta', StepId: 'llm-2', BlockIndex: 0, Delta: 'Done' }),
      makeEvent({ Kind: 'step.closed', StepId: 'llm-2' }),
    ]
    const result = stepReducerBatch(steps, events)
    expect(result).toHaveLength(3)
    expect(result[0]!.Id).toBe('llm-1')
    expect(result[1]!.Id).toBe('tool-1')
    expect(result[2]!.Id).toBe('llm-2')
    expect(result[2]!.Content[0]!.Text).toBe('Done')
  })

  it('handles reasoning step with deltas', () => {
    const steps: Step[] = []
    const events: StepEvent[] = [
      makeEvent({ Kind: 'step.opened', StepId: 'rs-1', StepType: 'reasoning', Role: 'assistant' }),
      makeEvent({ Kind: 'block.delta', StepId: 'rs-1', Delta: 'Thinking...' }),
      makeEvent({ Kind: 'step.closed', StepId: 'rs-1' }),
    ]
    const result = stepReducerBatch(steps, events)
    expect(result[0]!.Type).toBe('reasoning')
    expect(result[0]!.ReasoningContent).toBe('Thinking...')
    expect(result[0]!.Content).toHaveLength(0)
  })
})

// ── Parallel state dimensions ──

describe('step state machine — parallel dimensions', () => {
  it('content appending + execution in_progress can coexist', () => {
    const steps: Step[] = []
    const events: StepEvent[] = [
      makeEvent({ Kind: 'step.opened', StepId: 's1', StepType: 'text', Role: 'assistant' }),
      makeEvent({ Kind: 'block.delta', StepId: 's1', BlockIndex: 0, Delta: 'hello' }),
      // execution starts while content is still streaming
      makeEvent({ Kind: 'step.execution_progress', StepId: 's1' }),
    ]
    const result = stepReducerBatch(steps, events)
    expect(result[0]!.ContentStatus).toBe('appending')
    expect(result[0]!.ExecutionStatus).toBe('in_progress')
    expect(result[0]!.InteractionStatus).toBe('none')
  })

  it('execution completes before content stabilises', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'tool_call',
      Content: [{ Type: 'tool_use', ToolName: 'shell.exec', Input: '{}' }],
      Closed: false, TurnId: 't1',
      ContentStatus: 'appending', ExecutionStatus: 'idle', InteractionStatus: 'none',
    }]
    const events: StepEvent[] = [
      makeEvent({ Kind: 'step.execution_progress', StepId: 's1' }),
      makeEvent({ Kind: 'step.execution_completed', StepId: 's1' }),
      makeEvent({ Kind: 'block.appended', StepId: 's1', Block: { Type: 'tool_result', Text: 'done' } }),
      makeEvent({ Kind: 'step.closed', StepId: 's1' }),
    ]
    const result = stepReducerBatch(steps, events)
    expect(result[0]!.ExecutionStatus).toBe('completed')
    expect(result[0]!.ContentStatus).toBe('stable')
    expect(result[0]!.Closed).toBe(true)
  })

  it('interaction pending halts neither content nor execution', () => {
    const steps: Step[] = []
    const events: StepEvent[] = [
      makeEvent({ Kind: 'step.opened', StepId: 'plan-1', StepType: 'text', Role: 'assistant' }),
      makeEvent({ Kind: 'block.delta', StepId: 'plan-1', BlockIndex: 0, Delta: 'plan content...' }),
      makeEvent({
        Kind: 'step.interaction_requested', StepId: 'plan-interaction',
        InteractionType: 'plan_approval', TurnId: 't1',
        Task: { requestId: 'req-1', plan: 'plan content...' },
      }),
      // Content still appends on the original step during interaction
      makeEvent({ Kind: 'block.delta', StepId: 'plan-1', BlockIndex: 0, Delta: ' more' }),
      makeEvent({ Kind: 'step.closed', StepId: 'plan-1' }),
    ]
    const result = stepReducerBatch(steps, events)
    expect(result).toHaveLength(2)
    const interactionStep = result.find(s => s.Id === 'plan-interaction')!
    expect(interactionStep.InteractionStatus).toBe('pending')
    expect(interactionStep.ContentStatus).toBe('stable')
    const contentStep = result.find(s => s.Id === 'plan-1')!
    expect(contentStep.Content[0]!.Text).toBe('plan content... more')
    expect(contentStep.Closed).toBe(true)
  })
})

// ── Execution events ──

describe('step execution events', () => {
  it('step.execution_progress accumulates stdout and stderr chunks (off-string accumulator)', () => {
    const opened = stepReducer([], makeEvent({ Kind: 'step.opened', StepId: 's1', StepType: 'tool' }))
    const first = stepReducer(opened, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: JSON.stringify({ stream: 'stdout', text: 'one' }) }))
    const second = stepReducer(first, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: JSON.stringify({ stream: 'stdout', text: ' two' }) }))
    const third = stepReducer(second, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: JSON.stringify({ stream: 'stderr', text: 'bad' }) }))
    // Accumulated text lives in the symbol-keyed accumulator (O(1) per chunk),
    // identical to the legacy full-string merge result. Progress itself holds
    // only the latest delta — the wire string, unchanged protocol.
    expect(getExecutionProgressAcc(third[0]!)).toEqual({ stdout: 'one two', stderr: 'bad' })
    expect(third[0]!.Progress).toBe(JSON.stringify({ stream: 'stderr', text: 'bad' }))
  })

  it('step.execution_progress accumulates interleaved stdout/stderr in arrival order', () => {
    const opened = stepReducer([], makeEvent({ Kind: 'step.opened', StepId: 's1', StepType: 'tool' }))
    let steps = opened
    const chunks: Array<[string, string]> = [
      ['stdout', 'a'], ['stderr', 'X'], ['stdout', 'b'], ['stderr', 'Y'], ['stdout', 'c'],
    ]
    for (const [stream, text] of chunks) {
      steps = stepReducer(steps, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: JSON.stringify({ stream, text }) }))
    }
    expect(getExecutionProgressAcc(steps[0]!)).toEqual({ stdout: 'abc', stderr: 'XY' })
  })

  it('step.execution_progress non-stream delta replaces Progress and drops the accumulator', () => {
    const opened = stepReducer([], makeEvent({ Kind: 'step.opened', StepId: 's1', StepType: 'tool' }))
    const streamed = stepReducer(opened, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: JSON.stringify({ stream: 'stdout', text: 'hi' }) }))
    expect(getExecutionProgressAcc(streamed[0]!)).toEqual({ stdout: 'hi' })
    const stats = stepReducer(streamed, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: JSON.stringify({ phase: 'summarizing', searchCount: 3 }) }))
    expect(getExecutionProgressAcc(stats[0]!)).toBeUndefined()
    expect(stats[0]!.Progress).toBe(JSON.stringify({ phase: 'summarizing', searchCount: 3 }))
  })

  it('step.execution_progress with empty payload keeps Progress and the accumulator', () => {
    const opened = stepReducer([], makeEvent({ Kind: 'step.opened', StepId: 's1', StepType: 'tool' }))
    const streamed = stepReducer(opened, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: JSON.stringify({ stream: 'stdout', text: 'hi' }) }))
    const ticked = stepReducer(streamed, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: '' }))
    expect(getExecutionProgressAcc(ticked[0]!)).toEqual({ stdout: 'hi' })
    expect(ticked[0]!.Progress).toBe(JSON.stringify({ stream: 'stdout', text: 'hi' }))
    expect(ticked[0]!.ExecutionStatus).toBe('in_progress')
  })

  it('step.execution_progress is deduplicated by EventSeq (reconnect replay does not double-apply chunks)', () => {
    const seqTrack = new Map<string, number>()
    const opened = stepReducer([], makeEvent({ Kind: 'step.opened', StepId: 's1', StepType: 'tool', EventSeq: 1 }), seqTrack)
    const first = stepReducer(opened, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: JSON.stringify({ stream: 'stdout', text: 'one' }), EventSeq: 2 }), seqTrack)
    expect(getExecutionProgressAcc(first[0]!)).toEqual({ stdout: 'one' })
    // Replayed seq=2 (SSE reconnect) must be skipped — no double accumulation.
    const replayed = stepReducer(first, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: JSON.stringify({ stream: 'stdout', text: 'one' }), EventSeq: 2 }), seqTrack)
    expect(getExecutionProgressAcc(replayed[0]!)).toEqual({ stdout: 'one' })
    // A genuinely new chunk (seq=3) still applies.
    const next = stepReducer(replayed, makeEvent({ Kind: 'step.execution_progress', StepId: 's1', Progress: JSON.stringify({ stream: 'stdout', text: ' two' }), EventSeq: 3 }), seqTrack)
    expect(getExecutionProgressAcc(next[0]!)).toEqual({ stdout: 'one two' })
  })

  it('step.execution_progress sets in_progress', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'tool_call', Content: [],
      Closed: false, TurnId: 't1',
      ContentStatus: 'appending', ExecutionStatus: 'idle', InteractionStatus: 'none',
    }]
    const result = stepReducer(steps, makeEvent({ Kind: 'step.execution_progress', StepId: 's1' }))
    expect(result[0]!.ExecutionStatus).toBe('in_progress')
  })

  it('step.execution_completed sets completed', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'tool_call', Content: [],
      Closed: false, TurnId: 't1',
      ContentStatus: 'appending', ExecutionStatus: 'in_progress', InteractionStatus: 'none',
    }]
    const result = stepReducer(steps, makeEvent({ Kind: 'step.execution_completed', StepId: 's1' }))
    expect(result[0]!.ExecutionStatus).toBe('completed')
  })

  it('execution_progress on unknown stepId is no-op', () => {
    const steps: Step[] = []
    const result = stepReducer(steps, makeEvent({ Kind: 'step.execution_progress', StepId: 'unknown' }))
    expect(result).toEqual(steps)
  })
})

// ── Interaction lifecycle ──

describe('step interaction lifecycle', () => {
  it('step.interaction_requested creates pending interaction step', () => {
    const steps: Step[] = []
    const ev = makeEvent({
      Kind: 'step.interaction_requested',
      StepId: 'ask-1',
      TurnId: 't1',
      InteractionType: 'ask_user',
      Task: { question: 'Which file?' },
    })
    const result = stepReducer(steps, ev)
    expect(result).toHaveLength(1)
    expect(result[0]!).toMatchObject({
      Id: 'ask-1',
      Type: 'ask_user',
      InteractionStatus: 'pending',
      ContentStatus: 'stable',
      Closed: false,
    })
  })

  it('step.interaction_resolved resolves pending interaction', () => {
    const steps: Step[] = [{
      Id: 'ask-1', Role: 'assistant', Type: 'ask_user',
      Content: [{ Type: 'text', Text: '{"question":"Which file?"}' }],
      Closed: false, TurnId: 't1',
      ContentStatus: 'stable', ExecutionStatus: 'idle', InteractionStatus: 'pending',
    }]
    const ev = makeEvent({ Kind: 'step.interaction_resolved', StepId: 'ask-1' })
    const result = stepReducer(steps, ev)
    expect(result[0]!.InteractionStatus).toBe('resolved')
    expect(result[0]!.Closed).toBe(true)
    expect(result[0]!.ContentStatus).toBe('stable')
    expect(result[0]!.Content).toHaveLength(1)
  })

  it('step.interaction_resolved stores task payload in content for plan approval', () => {
    const steps: Step[] = [{
      Id: 'plan-1', Role: 'assistant', Type: 'plan_approval',
      Content: [{ Type: 'text', Text: JSON.stringify({ plan: '# Plan', editable: true, tasks: [] }) }],
      Closed: false, TurnId: 't1',
      ContentStatus: 'stable', ExecutionStatus: 'idle', InteractionStatus: 'pending',
    }]
    const ev = makeEvent({
      Kind: 'step.interaction_resolved',
      StepId: 'plan-1',
      Task: { decision: 'reject', answer: '{"decision":"reject"}' },
    })
    const result = stepReducer(steps, ev)
    expect(result[0]!.InteractionStatus).toBe('resolved')
    expect(result[0]!.Content).toHaveLength(2)
    expect(JSON.parse(result[0]!.Content[1]!.Text!)).toMatchObject({ decision: 'reject' })
  })

  it('duplicate interaction_requested is ignored', () => {
    const steps: Step[] = [{
      Id: 'plan-int', Role: 'assistant', Type: 'plan_approval',
      Content: [], Closed: false, TurnId: 't1',
      ContentStatus: 'stable', ExecutionStatus: 'idle', InteractionStatus: 'pending',
    }]
    const ev = makeEvent({
      Kind: 'step.interaction_requested',
      StepId: 'plan-int',
      TurnId: 't1',
      InteractionType: 'plan_approval',
    })
    const result = stepReducer(steps, ev)
    expect(result).toHaveLength(1)
    expect(result[0]!.InteractionStatus).toBe('pending')
  })
})

// ── Task events (Turn-level aggregation) ──

describe('step task events', () => {
  it('step.task_created is no-op at step level', () => {
    const steps: Step[] = []
    const ev = makeEvent({ Kind: 'step.task_created', StepId: 's1', TaskId: 'task-1' })
    const result = stepReducer(steps, ev)
    expect(result).toEqual(steps)
  })

  it('step.task_updated is no-op at step level', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({ Kind: 'step.task_updated', StepId: 's1', TaskId: 'task-1' })
    const result = stepReducer(steps, ev)
    expect(result).toEqual(steps)
  })

  it('step.task_deleted is no-op at step level', () => {
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text', Content: [], Closed: false, TurnId: 't1',
    }]
    const ev = makeEvent({ Kind: 'step.task_deleted', StepId: 's1', TaskId: 'task-1' })
    const result = stepReducer(steps, ev)
    expect(result).toEqual(steps)
  })
})

// ── EventSeq dedup ──

describe('EventSeq dedup', () => {
  it('skips events with already-consumed EventSeq', () => {
    const seqTrack = new Map<string, number>()
    const steps: Step[] = []
    const first = stepReducer(steps, makeEvent({
      Kind: 'step.opened', StepId: 's1', StepType: 'text', TurnId: 't1',
      Role: 'assistant', EventSeq: 1,
    }), seqTrack)
    expect(first).toHaveLength(1)

    // Same EventSeq replayed — should be skipped
    const second = stepReducer(first, makeEvent({
      Kind: 'step.opened', StepId: 's1', StepType: 'text', TurnId: 't1',
      Role: 'assistant', EventSeq: 1,
    }), seqTrack)
    // No duplicate step added; event was skipped entirely (including the handler body)
    expect(second).toHaveLength(1)
  })

  it('skips events with lower EventSeq than last consumed', () => {
    const seqTrack = new Map<string, number>()
    const steps: Step[] = [{
      Id: 's1', Role: 'assistant', Type: 'text',
      Content: [{ Type: 'text', Text: 'v1' }], Closed: false, TurnId: 't1',
    }]
    // Consume eventSeq 5
    stepReducer(steps, makeEvent({ Kind: 'block.delta', StepId: 's1', BlockIndex: 0, Delta: '!', EventSeq: 5 }), seqTrack)

    // Replay eventSeq 3 should be skipped
    const result = stepReducer(steps, makeEvent({ Kind: 'block.delta', StepId: 's1', BlockIndex: 0, Delta: 'x', EventSeq: 3 }), seqTrack)
    // Content unchanged — delta was skipped
    expect(result[0]!.Content[0]!.Text).toBe('v1')
  })

  it('processes events with EventSeq == 0 (legacy / no seq)', () => {
    const seqTrack = new Map<string, number>()
    const steps: Step[] = []
    const first = stepReducer(steps, makeEvent({
      Kind: 'step.opened', StepId: 's1', StepType: 'text', TurnId: 't1',
      Role: 'assistant', EventSeq: 0,
    }), seqTrack)
    // EventSeq 0 should still open the step
    expect(first).toHaveLength(1)

    // Another EventSeq 0 — should also process (0 means no tracking)
    const second = stepReducer(first, makeEvent({
      Kind: 'step.closed', StepId: 's1', EventSeq: 0,
    }), seqTrack)
    expect(second[0]!.Closed).toBe(true)
  })
})
