import { describe, it, expect } from 'vitest'
import { stepReducer, stepReducerBatch } from './step-reducer'
import { stepsToEnvelopes } from './steps-to-envelopes'
import type { StepEvent, Step } from '../../../gen-types/aigen'
import type { TextFrame, ReasoningFrame } from '../model/frame-types'

function asText(f: unknown): TextFrame {
  return f as TextFrame
}

/** Simulates a complete backend event sequence and verifies the resulting
 *  frontend envelope state. This is the end-to-end validation of the
 *  StepEvent → Step[] → TurnEnvelope[] pipeline. */
describe('step event pipeline', () => {
  function runPipeline(events: StepEvent[]) {
    const steps = stepReducerBatch([], events)
    return stepsToEnvelopes(steps)
  }

  it('batch reducer equals sequential single-event application and preserves untouched step refs', () => {
    const events: StepEvent[] = [
      { Kind: 'step.opened', StepId: 'user-1', TurnId: 'turn-1', StepType: 'text', Role: 'user' },
      { Kind: 'block.appended', StepId: 'user-1', TurnId: 'turn-1', Block: { Type: 'text', Text: 'Hello' } },
      { Kind: 'step.closed', StepId: 'user-1', TurnId: 'turn-1' },
      { Kind: 'step.opened', StepId: 'llm-1', TurnId: 'turn-1', StepType: 'text', Role: 'assistant' },
      { Kind: 'block.delta', StepId: 'llm-1', TurnId: 'turn-1', BlockIndex: 0, Delta: 'a' },
      { Kind: 'block.delta', StepId: 'llm-1', TurnId: 'turn-1', BlockIndex: 0, Delta: 'b' },
      { Kind: 'step.opened', StepId: 'tool-1', TurnId: 'turn-1', StepType: 'tool_call', Role: 'assistant' },
      { Kind: 'step.execution_progress', StepId: 'tool-1', TurnId: 'turn-1', Progress: JSON.stringify({ stream: 'out', text: 'x' }) },
      { Kind: 'step.execution_completed', StepId: 'tool-1', TurnId: 'turn-1' },
      { Kind: 'step.closed', StepId: 'tool-1', TurnId: 'turn-1' },
      { Kind: 'step.closed', StepId: 'llm-1', TurnId: 'turn-1' },
      { Kind: 'step.error', StepId: 'ghost-1', TurnId: 'turn-1', Error: 'no such step' },
    ]
    const seqTracking = new Map<string, number>()
    let sequential: Step[] = []
    for (const ev of events) sequential = stepReducer(sequential, ev, seqTracking)

    const batched = stepReducerBatch([], events, new Map<string, number>())
    const stripTs = (steps: Step[]) => steps.map(s => ({ ...s, Timestamp: undefined }))
    expect(stripTs(batched)).toEqual(stripTs(sequential))
    // Untouched steps keep their object references through a mutating batch:
    // the projection cache and React.memo rely on this.
    const beforeRef = batched[0]
    const more = stepReducerBatch(batched, [
      { Kind: 'block.delta', StepId: 'llm-1', TurnId: 'turn-1', BlockIndex: 0, Delta: 'c' },
      { Kind: 'step.closed', StepId: 'llm-1', TurnId: 'turn-1' },
    ], new Map<string, number>())
    expect(more).not.toBe(batched)
    expect(more[0]).toBe(beforeRef)
    expect(more[1]).not.toBe(batched[1])
  })

  it('no-op batch returns the input array reference (projection cache short-circuit)', () => {
    const opened: StepEvent[] = [
      { Kind: 'step.opened', StepId: 's1', TurnId: 't1', StepType: 'text', Role: 'assistant' },
    ]
    const steps = stepReducerBatch([], opened)
    // Unknown step id + task/file events are all no-ops.
    const noop: StepEvent[] = [
      { Kind: 'step.closed', StepId: 'missing', TurnId: 't1' },
      { Kind: 'step.task_created', StepId: 's1', TurnId: 't1', TaskId: 'k1' },
      { Kind: 'step.file_changes', StepId: 's1', TurnId: 't1' },
    ]
    expect(stepReducerBatch(steps, noop)).toBe(steps)
  })

  it('full text response turn', () => {
    const events: StepEvent[] = [
      { Kind: 'step.opened', StepId: 'user-1', TurnId: 'turn-1', StepType: 'text', Role: 'user' },
      { Kind: 'block.appended', StepId: 'user-1', TurnId: 'turn-1', Block: { Type: 'text', Text: 'Hello' } },
      { Kind: 'step.closed', StepId: 'user-1', TurnId: 'turn-1' },
      { Kind: 'step.opened', StepId: 'llm-1', TurnId: 'turn-1', StepType: 'text', Role: 'assistant' },
      { Kind: 'block.delta', StepId: 'llm-1', TurnId: 'turn-1', BlockIndex: 0, Delta: 'Hello' },
      { Kind: 'block.delta', StepId: 'llm-1', TurnId: 'turn-1', BlockIndex: 0, Delta: ' world' },
      { Kind: 'step.closed', StepId: 'llm-1', TurnId: 'turn-1' },
    ]
    const envelopes = runPipeline(events)

    expect(envelopes).toHaveLength(2)
    expect(envelopes[0]).toMatchObject({ role: 'user', userContent: 'Hello', completed: true })
    expect(envelopes[1]).toMatchObject({ role: 'assistant', completed: true, metadata: { turnId: 'turn-1' } })
    expect(asText(envelopes[1]!.frames[0]).content).toBe('Hello world')
    expect(envelopes[1]!.frames[0]!.status).toBe('completed')
    expect(envelopes[1]!.frames[0]!.type).toBe('text')
  })

  it('streaming text response (incomplete)', () => {
    const events: StepEvent[] = [
      { Kind: 'step.opened', StepId: 'llm-1', TurnId: 'turn-1', StepType: 'text', Role: 'assistant' },
      { Kind: 'block.delta', StepId: 'llm-1', TurnId: 'turn-1', BlockIndex: 0, Delta: 'Partial' },
    ]
    const envelopes = runPipeline(events)

    expect(envelopes).toHaveLength(1)
    expect(envelopes[0]!.completed).toBe(false)
    expect(envelopes[0]!.frames[0]!.status).toBe('running')
    expect(asText(envelopes[0]!.frames[0]).content).toBe('Partial')
  })

  it('tool call turn: text → tool → text', () => {
    const events: StepEvent[] = [
      { Kind: 'step.opened', StepId: 'user-1', TurnId: 'turn-1', StepType: 'text', Role: 'user' },
      { Kind: 'block.appended', StepId: 'user-1', TurnId: 'turn-1', Block: { Type: 'text', Text: 'List /' } },
      { Kind: 'step.closed', StepId: 'user-1', TurnId: 'turn-1' },

      { Kind: 'step.opened', StepId: 'llm-1', TurnId: 'turn-1', StepType: 'text', Role: 'assistant' },
      { Kind: 'block.delta', StepId: 'llm-1', TurnId: 'turn-1', BlockIndex: 0, Delta: 'Let me check' },
      { Kind: 'step.closed', StepId: 'llm-1', TurnId: 'turn-1' },

      { Kind: 'step.opened', StepId: 'tool-1', TurnId: 'turn-1', StepType: 'tool_call', Role: 'assistant' },
      { Kind: 'block.appended', StepId: 'tool-1', TurnId: 'turn-1', Block: { Type: 'tool_use', ToolName: 'project.list_json', Input: '{"path":"/"}' } },
      { Kind: 'block.appended', StepId: 'tool-1', TurnId: 'turn-1', Block: { Type: 'tool_result', Text: 'bin etc home' } },
      { Kind: 'step.closed', StepId: 'tool-1', TurnId: 'turn-1' },

      { Kind: 'step.opened', StepId: 'llm-2', TurnId: 'turn-1', StepType: 'text', Role: 'assistant' },
      { Kind: 'block.delta', StepId: 'llm-2', TurnId: 'turn-1', BlockIndex: 0, Delta: 'Here are the files' },
      { Kind: 'step.closed', StepId: 'llm-2', TurnId: 'turn-1' },
    ]
    const envelopes = runPipeline(events)

    expect(envelopes).toHaveLength(2)
    const asst = envelopes.find(e => e.role === 'assistant')
    expect(asst!.frames).toHaveLength(3)
    expect(asText(asst!.frames[0]).content).toBe('Let me check')
    expect(asst!.frames[1]!.type).toBe('tool')
    expect(asText(asst!.frames[2]).content).toBe('Here are the files')
  })

  it('multiple independent turns', () => {
    const events: StepEvent[] = [
      // Turn 1
      { Kind: 'step.opened', StepId: 'u1', TurnId: 't1', StepType: 'text', Role: 'user' },
      { Kind: 'block.appended', StepId: 'u1', TurnId: 't1', Block: { Type: 'text', Text: 'Q1' } },
      { Kind: 'step.closed', StepId: 'u1', TurnId: 't1' },
      { Kind: 'step.opened', StepId: 'a1', TurnId: 't1', StepType: 'text', Role: 'assistant' },
      { Kind: 'block.delta', StepId: 'a1', TurnId: 't1', BlockIndex: 0, Delta: 'A1' },
      { Kind: 'step.closed', StepId: 'a1', TurnId: 't1' },

      // Turn 2
      { Kind: 'step.opened', StepId: 'u2', TurnId: 't2', StepType: 'text', Role: 'user' },
      { Kind: 'block.appended', StepId: 'u2', TurnId: 't2', Block: { Type: 'text', Text: 'Q2' } },
      { Kind: 'step.closed', StepId: 'u2', TurnId: 't2' },
      { Kind: 'step.opened', StepId: 'a2', TurnId: 't2', StepType: 'text', Role: 'assistant' },
      { Kind: 'block.delta', StepId: 'a2', TurnId: 't2', BlockIndex: 0, Delta: 'A2' },
      { Kind: 'step.closed', StepId: 'a2', TurnId: 't2' },
    ]
    const envelopes = runPipeline(events)

    expect(envelopes).toHaveLength(4)
    expect(envelopes.map(e => e.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
    expect(asText(envelopes[1]!.frames[0]).content).toBe('A1')
    expect(asText(envelopes[3]!.frames[0]).content).toBe('A2')
  })

  it('error step produces error frame', () => {
    const events: StepEvent[] = [
      { Kind: 'step.opened', StepId: 'llm-1', TurnId: 'turn-1', StepType: 'text', Role: 'assistant' },
      { Kind: 'step.error', StepId: 'llm-1', TurnId: 'turn-1', Error: 'LLM timeout' },
    ]
    const envelopes = runPipeline(events)

    expect(envelopes).toHaveLength(1)
    expect(envelopes[0]!.frames[0]!).toMatchObject({ type: 'error', message: 'LLM timeout' })
    expect(envelopes[0]!.completed).toBe(true)
  })

  it('step.error on a tool_call step carries the tool name', () => {
    const events: StepEvent[] = [
      { Kind: 'step.opened', StepId: 'tool-1', TurnId: 'turn-1', StepType: 'tool_call', Role: 'assistant' },
      { Kind: 'block.appended', StepId: 'tool-1', TurnId: 'turn-1', Block: { Type: 'tool_use', ToolName: 'project.list', ToolUseId: 'tu-1', Input: '{"path":"/"}' } },
      { Kind: 'step.error', StepId: 'tool-1', TurnId: 'turn-1', Error: 'permission denied' },
    ]
    const envelopes = runPipeline(events)

    expect(envelopes).toHaveLength(1)
    expect(envelopes[0]!.frames[0]!).toMatchObject({
      type: 'error',
      message: 'permission denied',
      toolName: 'project.list',
    })
    expect(envelopes[0]!.completed).toBe(true)
  })

  it('step.error on a tool_call step without a tool_use block stays unnamed', () => {
    const events: StepEvent[] = [
      { Kind: 'step.opened', StepId: 'tool-2', TurnId: 'turn-1', StepType: 'tool_call', Role: 'assistant' },
      { Kind: 'step.error', StepId: 'tool-2', TurnId: 'turn-1', Error: 'dispatch failed' },
    ]
    const envelopes = runPipeline(events)

    expect(envelopes).toHaveLength(1)
    expect(envelopes[0]!.frames[0]!).toMatchObject({
      type: 'error',
      message: 'dispatch failed',
      toolName: undefined,
    })
  })

  it('reasoning step produces reasoning frame', () => {
    const events: StepEvent[] = [
      { Kind: 'step.opened', StepId: 'rs-1', TurnId: 'turn-1', StepType: 'reasoning', Role: 'assistant' },
      { Kind: 'block.appended', StepId: 'rs-1', TurnId: 'turn-1', Block: { Type: 'text', Text: '' } },
      { Kind: 'block.delta', StepId: 'rs-1', TurnId: 'turn-1', BlockIndex: 0, Delta: 'Analyzing codebase...' },
      { Kind: 'step.closed', StepId: 'rs-1', TurnId: 'turn-1' },
    ]
    const envelopes = runPipeline(events)

    expect(envelopes[0]!.frames[0]!).toMatchObject({
      type: 'reasoning',
      content: 'Analyzing codebase...',
      status: 'completed',
    } as ReasoningFrame)
  })

  it('idempotent: duplicate step.opened is ignored', () => {
    const events: StepEvent[] = [
      { Kind: 'step.opened', StepId: 's1', TurnId: 'turn-1', StepType: 'text', Role: 'assistant' },
      { Kind: 'step.opened', StepId: 's1', TurnId: 'turn-1', StepType: 'text', Role: 'assistant' },
      { Kind: 'block.delta', StepId: 's1', TurnId: 'turn-1', BlockIndex: 0, Delta: 'X' },
    ]
    const envelopes = runPipeline(events)

    expect(envelopes).toHaveLength(1)
    expect(asText(envelopes[0]!.frames[0]).content).toBe('X')
  })

  it('streaming stdout/stderr accumulates into tool runningOutput (interleaved, off-string)', () => {
    const events: StepEvent[] = [
      { Kind: 'step.opened', StepId: 'tool-1', TurnId: 'turn-1', StepType: 'tool_call', Role: 'assistant' },
      { Kind: 'block.appended', StepId: 'tool-1', TurnId: 'turn-1', Block: { Type: 'tool_use', ToolName: 'shell.exec', Input: '{}' } },
      { Kind: 'step.execution_progress', StepId: 'tool-1', TurnId: 'turn-1', Progress: JSON.stringify({ stream: 'stdout', text: 'line1\n' }) },
      { Kind: 'step.execution_progress', StepId: 'tool-1', TurnId: 'turn-1', Progress: JSON.stringify({ stream: 'stderr', text: 'warn\n' }) },
      { Kind: 'step.execution_progress', StepId: 'tool-1', TurnId: 'turn-1', Progress: JSON.stringify({ stream: 'stdout', text: 'line2\n' }) },
    ]
    const envelopes = runPipeline(events)

    const toolFrame = envelopes[0]!.frames.find(f => f!.type === 'tool') as { runningOutput?: { stdout: string; stderr: string } }
    expect(toolFrame.runningOutput).toEqual({ stdout: 'line1\nline2\n', stderr: 'warn\n' })
  })

  it('replayed execution_progress chunks (same EventSeq) are not double-applied', () => {
    const seqTracking = new Map<string, number>()
    const base: StepEvent[] = [
      { Kind: 'step.opened', StepId: 'tool-1', TurnId: 'turn-1', StepType: 'tool_call', Role: 'assistant', EventSeq: 1 },
      { Kind: 'block.appended', StepId: 'tool-1', TurnId: 'turn-1', Block: { Type: 'tool_use', ToolName: 'shell.exec', Input: '{}' } },
      { Kind: 'step.execution_progress', StepId: 'tool-1', TurnId: 'turn-1', Progress: JSON.stringify({ stream: 'stdout', text: 'ab' }), EventSeq: 2 },
    ]
    let steps = stepReducerBatch([], base, seqTracking)
    // Simulate an SSE reconnect replaying the same seq=2 chunk.
    steps = stepReducerBatch(steps, [
      { Kind: 'step.execution_progress', StepId: 'tool-1', TurnId: 'turn-1', Progress: JSON.stringify({ stream: 'stdout', text: 'ab' }), EventSeq: 2 },
      // …followed by genuinely new chunks.
      { Kind: 'step.execution_progress', StepId: 'tool-1', TurnId: 'turn-1', Progress: JSON.stringify({ stream: 'stdout', text: 'cd' }), EventSeq: 3 },
    ], seqTracking)

    const envelopes = stepsToEnvelopes(steps)
    const toolFrame = envelopes[0]!.frames.find(f => f!.type === 'tool') as { runningOutput?: { stdout: string; stderr: string } }
    expect(toolFrame.runningOutput).toEqual({ stdout: 'abcd', stderr: '' })
  })

  it('loaded session (no accumulator) falls back to parsing step.Progress', () => {
    // A step materialized from a backend snapshot carries only the last delta
    // in Progress and has no live accumulator; the consumer must still parse
    // Progress for the accumulated output a legacy frontend may have left there.
    const steps: Step[] = [{
      Id: 'tool-1', Role: 'assistant', Type: 'tool_call', TurnId: 'turn-1', Closed: false,
      Content: [{ Type: 'tool_use', ToolName: 'shell.exec', Input: '{}' }],
      ContentStatus: 'appending', ExecutionStatus: 'in_progress', InteractionStatus: 'none',
      Progress: JSON.stringify({ stream: 'stdout', text: 'loaded', stdout: 'loaded' }),
    }]
    const envelopes = stepsToEnvelopes(steps)
    const toolFrame = envelopes[0]!.frames.find(f => f!.type === 'tool') as { runningOutput?: { stdout: string; stderr: string } }
    expect(toolFrame.runningOutput).toEqual({ stdout: 'loaded', stderr: '' })
  })
})
