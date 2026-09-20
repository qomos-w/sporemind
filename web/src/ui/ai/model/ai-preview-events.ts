import type { Frame, TurnEnvelope } from './frame-types'
import type { StepEvent } from '../../../gen-types/aigen'
import type { LocalEvent } from '../hooks/timeline-manager'

export interface PreviewEvent {
  delayMs: number
  event: StepEvent | LocalEvent
}

const CHAR_DELTA_MS = 80
const TOOL_START_MS = 800
const TOOL_LINE_MS = 500
const FRAME_GAP_MS = 700
function mkStepEvent(kind: string, overrides: Record<string, unknown>): StepEvent {
  return { Kind: kind, ...overrides } as StepEvent
}

function textEvents(content: string, stepId: string, turnId: string): PreviewEvent[] {
  const events: PreviewEvent[] = []
  const chunkSize = 3

  events.push({
    delayMs: 0,
    event: mkStepEvent('step.opened', { StepId: stepId, TurnId: turnId, StepType: 'text', Role: 'assistant' }),
  })
  events.push({
    delayMs: 0,
    event: mkStepEvent('block.appended', { StepId: stepId, TurnId: turnId, Block: { Type: 'text', Text: '' } }),
  })

  for (let i = 0; i < content.length; i += chunkSize) {
    events.push({
      delayMs: i === 0 ? 0 : CHAR_DELTA_MS,
      event: mkStepEvent('block.delta', {
        StepId: stepId,
        TurnId: turnId,
        BlockIndex: 0,
        Delta: content.slice(i, i + chunkSize),
      }),
    })
  }

  events.push({
    delayMs: 0,
    event: mkStepEvent('step.closed', { StepId: stepId, TurnId: turnId }),
  })
  return events
}

function reasoningEvents(content: string, stepId: string, turnId: string): PreviewEvent[] {
  const events: PreviewEvent[] = []
  const chunkSize = 3

  events.push({
    delayMs: 0,
    event: mkStepEvent('step.opened', { StepId: stepId, TurnId: turnId, StepType: 'reasoning', Role: 'assistant' }),
  })

  for (let i = 0; i < content.length; i += chunkSize) {
    events.push({
      delayMs: i === 0 ? 0 : CHAR_DELTA_MS,
      event: mkStepEvent('block.delta', {
        StepId: stepId,
        TurnId: turnId,
        Delta: content.slice(i, i + chunkSize),
      }),
    })
  }

  events.push({
    delayMs: 0,
    event: mkStepEvent('step.closed', { StepId: stepId, TurnId: turnId }),
  })
  return events
}

function toolEvents(frame: Extract<Frame, { type: 'tool' }>, stepId: string, turnId: string): PreviewEvent[] {
  const events: PreviewEvent[] = []

  events.push({
    delayMs: 0,
    event: mkStepEvent('step.opened', { StepId: stepId, TurnId: turnId, StepType: 'tool_call', Role: 'assistant' }),
  })

  events.push({
    delayMs: 0,
    event: mkStepEvent('block.appended', {
      StepId: stepId,
      TurnId: turnId,
      Block: { Type: 'tool_use', ToolName: frame.toolName, Input: frame.input ?? '' },
    }),
  })

  if (frame.output) {
    events.push({
      delayMs: TOOL_START_MS,
      event: mkStepEvent('block.appended', {
        StepId: stepId,
        TurnId: turnId,
        Block: { Type: 'tool_result', Text: frame.output },
      }),
    })
  }

  events.push({
    delayMs: frame.output ? TOOL_LINE_MS : 0,
    event: mkStepEvent('step.closed', { StepId: stepId, TurnId: turnId }),
  })

  return events
}

function askUserEvents(frame: Extract<Frame, { type: 'ask_user' }>, stepId: string, turnId: string): PreviewEvent[] {
  const events: PreviewEvent[] = []
  const requestId = frame.requestId ?? `preview-req-${stepId}`

  events.push({
    delayMs: 0,
    event: mkStepEvent('step.opened', { StepId: stepId, TurnId: turnId, StepType: 'ask_user', Role: 'assistant' }),
  })

  events.push({
    delayMs: 0,
    event: mkStepEvent('block.appended', {
      StepId: stepId,
      TurnId: turnId,
      Block: { Type: 'text', Text: JSON.stringify(frame.questions) },
    }),
  })

  events.push({
    delayMs: 0,
    event: mkStepEvent('step.closed', { StepId: stepId, TurnId: turnId }),
  })

  if (frame.answers) {
    events.push({
      delayMs: TOOL_LINE_MS,
      event: { kind: 'ai.ask_answered', requestId, answers: frame.answers } as LocalEvent,
    })
  }

  return events
}

function errorEvents(message: string, stepId: string, turnId: string): PreviewEvent[] {
  return [
    {
      delayMs: 0,
      event: mkStepEvent('step.opened', { StepId: stepId, TurnId: turnId, StepType: 'text', Role: 'assistant' }),
    },
    {
      delayMs: 0,
      event: mkStepEvent('step.error', { StepId: stepId, TurnId: turnId, Error: message }),
    },
  ]
}

function frameEvents(frame: Frame, turnId: string): PreviewEvent[] {
  const stepId = frame.id

  switch (frame.type) {
    case 'text':
      return textEvents(frame.content, stepId, turnId)
    case 'reasoning':
      return reasoningEvents(frame.content, stepId, turnId)
    case 'tool':
      return toolEvents(frame, stepId, turnId)
    case 'ask_user':
      return askUserEvents(frame, stepId, turnId)
    case 'error':
      return errorEvents(frame.message, stepId, turnId)
    default:
      // sources, attachments, terminal, image, ui, compaction, goal_review — render as text
      return textEvents('', stepId, turnId)
  }
}

/**
 * Convert a static TurnEnvelope snapshot into a StepEvent sequence
 * that mirrors the backend step stream.
 */
export function buildEventSequence(envelopes: TurnEnvelope[]): PreviewEvent[] {
  let seq = 0
  const result: PreviewEvent[] = []

  for (const envelope of envelopes) {
    if (envelope.role !== 'assistant') continue

    const turnId = `preview-${++seq}`

    for (let fi = 0; fi < envelope.frames.length; fi++) {
      const frame = envelope.frames[fi]!
      const feList = frameEvents(frame, turnId)
      const withGap = feList.map((fe, ei) => ({
        ...fe,
        delayMs: fe.delayMs + (fi > 0 && ei === 0 ? FRAME_GAP_MS : 0),
      }))

      for (const fe of withGap) {
        result.push(fe)
      }

      if (frame.type === 'ask_user') break
    }

    // If the envelope is completed, no extra event needed — step.closed handles it.
  }

  return result
}
