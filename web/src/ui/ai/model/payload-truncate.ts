import type { TurnEnvelope, Frame, ToolFrame } from './frame-types'

/** Maximum rendered length for a stored tool payload string.
 *  Long payloads are kept as head + tail with a truncation marker in between.
 *  The total (head + marker + tail) is approximately 2 KB. */
const MAX_PAYLOAD_CHARS = 2048
const HEAD_TAIL_KEEP_EACH = 1024

/** Truncate a single payload string while preserving total length metadata.
 *
 *  Strings up to MAX_PAYLOAD_CHARS are returned unchanged. Longer strings keep
 *  the first and last HEAD_TAIL_KEEP_EACH characters with a marker containing
 *  the original total length in between.
 *
 *  Example:
 *    "<first 1024 chars>\n...(truncated 12345 chars)...\n<last 1024 chars>" */
export function truncatePayloadString(value: string): string {
  if (value.length <= MAX_PAYLOAD_CHARS) return value
  const head = value.slice(0, HEAD_TAIL_KEEP_EACH)
  const tail = value.slice(value.length - HEAD_TAIL_KEEP_EACH)
  return `${head}\n...(truncated ${value.length} chars)...\n${tail}`
}

/** Truncate tool input/output payloads inside a single history envelope.
 *
 *  Live steps and streaming deltas are NOT passed through here; this is only
 *  for envelopes that enter the persistent history cache. Returns the same
 *  reference when no frame was modified. */
export function truncateHistoryEnvelope(envelope: TurnEnvelope): TurnEnvelope {
  if (!envelope.frames || envelope.frames.length === 0) return envelope

  let changed = false
  const newFrames: Frame[] = []
  for (const frame of envelope.frames) {
    if (frame.type !== 'tool') {
      newFrames.push(frame)
      continue
    }
    const toolFrame = frame as ToolFrame
    const newInput = truncatePayloadString(toolFrame.input)
    const newOutput = toolFrame.output === undefined
      ? undefined
      : truncatePayloadString(toolFrame.output)
    if (newInput === toolFrame.input && newOutput === toolFrame.output) {
      newFrames.push(frame)
      continue
    }
    changed = true
    newFrames.push({ ...toolFrame, input: newInput, output: newOutput })
  }

  if (!changed) return envelope
  return { ...envelope, frames: newFrames }
}

/** Truncate tool payloads across a list of history envelopes.
 *
 *  Returns the same array reference when no envelope was modified, which keeps
 *  referential-equality short-circuits (e.g. projection cache) intact. */
export function truncateHistoryEnvelopes(envelopes: TurnEnvelope[]): TurnEnvelope[] {
  let changed = false
  const result = envelopes.map(e => {
    const truncated = truncateHistoryEnvelope(e)
    if (truncated !== e) changed = true
    return truncated
  })
  return changed ? result : envelopes
}
