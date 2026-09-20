import type { Step, StepEvent, ContentBlock } from '../../../gen-types/aigen'
import { stepDebugEnabled } from './debug-flag'

/** Symbol-keyed, own-enumerable accumulator maintained on live Step objects by
 *  the step.execution_progress handler. It holds the accumulated per-stream
 *  stdout/stderr text so each streaming chunk only pays an O(1) string append,
 *  instead of the legacy JSON.parse(previous full) + JSON.stringify(merged
 *  full) per chunk (O(N²) over the stream).
 *
 *  A symbol (not a string key) is used so the accumulator:
 *   - survives the reducer's `{...cur}` spread (own enumerable symbol copy),
 *   - stays invisible to JSON.stringify (Step objects are never re-serialized
 *     with it; the wire Progress field stays a plain string),
 *   - stays invisible to Object.keys / for-in (won't pollute generic Step walks).
 *
 *  It is dropped naturally when the step object is replaced wholesale by a
 *  server snapshot (mergeSteps / replaceActiveTurnSteps); the consumer then
 *  falls back to parsing step.Progress (the last delta), which is exactly the
 *  historical-replay / loaded-session behavior. */
const EXECUTION_PROGRESS_ACC = Symbol('executionProgressAcc')

/** Accumulated execution output keyed by stream name (stdout/stderr, plus any
 *  other stream name the backend emits). Mirrors the per-stream keys the
 *  legacy full-string merge produced under `existing[stream]`. */
export type ExecutionProgressAcc = Record<string, string>

/** Read the live accumulator (if any) carried on a Step object. */
export function getExecutionProgressAcc(step: Step): ExecutionProgressAcc | undefined {
  return (step as unknown as Partial<Record<typeof EXECUTION_PROGRESS_ACC, ExecutionProgressAcc>>)[EXECUTION_PROGRESS_ACC]
}

function setExecutionProgressAcc(step: Step, acc: ExecutionProgressAcc | undefined): void {
  const target = step as unknown as Record<typeof EXECUTION_PROGRESS_ACC, ExecutionProgressAcc | undefined>
  if (acc) target[EXECUTION_PROGRESS_ACC] = acc
  else delete target[EXECUTION_PROGRESS_ACC]
}

/** Compact invalidation token for the projection-cache signature. The
 *  accumulator only ever grows (append-only; the backend skips empty chunks),
 *  so per-stream text lengths fully capture mutations without re-hashing the
 *  full text — keeping signature construction O(streams) per chunk. */
export function executionProgressAccSig(step: Step): string {
  const acc = getExecutionProgressAcc(step)
  if (!acc) return ''
  return Object.keys(acc).sort().map(k => `${k}:${acc[k]!.length}`).join(',')
}

/** Parse an execution_progress delta payload. A "stream delta" carries
 *  `{stream: 'stdout'|'stderr'|..., text: '<chunk>'}`; any other shape (explore
 *  stats snapshots, goal_review stats, malformed JSON) returns undefined and
 *  is treated as a verbatim Progress replacement (legacy behavior). */
function parseStreamDelta(next: string | undefined): Record<string, unknown> | undefined {
  if (!next) return undefined
  let value: unknown
  try {
    value = JSON.parse(next)
  } catch {
    return undefined
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  const incoming = value as Record<string, unknown>
  if (typeof incoming.stream !== 'string' || typeof incoming.text !== 'string') return undefined
  return incoming
}

function shouldSkipEvent(stepId: string, eventSeq?: number, seqTracking?: Map<string, number>): boolean {
  if (eventSeq == null || eventSeq <= 0) return false
  if (!seqTracking) return false
  const last = seqTracking.get(stepId) ?? 0
  if (eventSeq <= last) {
    if (stepDebugEnabled()) console.debug(`[step.reducer] skipping duplicate: stepId=${stepId} eventSeq=${eventSeq} last=${last}`)
    return true
  }
  seqTracking.set(stepId, eventSeq)
  return false
}

/** stepReducer maintains a canonical Step[] from a stream of StepEvents.
 *  Each Step is identified by StepId; events are idempotent (duplicate
 *  step.opened for the same ID is ignored).
 *
 *  Semantics: unchanged steps keep their object references; a modified step
 *  is replaced by a new object; the array reference only changes when the
 *  batch actually mutated something (so the projection cache can short-circuit
 *  on no-op batches). */
export function stepReducer(steps: Step[], event: StepEvent, seqTracking?: Map<string, number>): Step[] {
  return stepReducerBatch(steps, [event], seqTracking)
}

/** Batch-reduce multiple StepEvents into a Step[].
 *
 *  Implementation note: this engine copies the steps array ONCE per batch and
 *  locates the target step via an id→index map, instead of running
 *  steps.map() per event. With multi-thousand-step conversations the old
 *  per-event array clone was the dominant allocation during streaming
 *  (batch × N clones per rAF flush). */
export function stepReducerBatch(steps: Step[], events: StepEvent[], seqTracking?: Map<string, number>): Step[] {
  if (events.length === 0) return steps
  const index = new Map<string, number>()
  for (let i = 0; i < steps.length; i++) index.set(steps[i]!.Id, i)
  let draft: Step[] | null = null
  const mutable = (): Step[] => {
    if (!draft) draft = steps.slice()
    return draft
  }
  const has = (id: string): boolean => index.has(id)
  const at = (id: string): Step => {
    const i = index.get(id)
    if (i === undefined) throw new Error(`step not found: ${id}`)
    return (draft ?? steps)[i]!
  }
  const replace = (id: string, step: Step): void => {
    mutable()[index.get(id)!] = step
  }
  const append = (step: Step): void => {
    const arr = mutable()
    index.set(step.Id, arr.length)
    arr.push(step)
  }

  for (const event of events) {
    // Only events that mutate state non-idempotently (appending) need seq-based
    // dedup: block.appended / block.delta (content append), step.reset (content
    // wipe), and step.execution_progress (stream-output append — without dedup an
    // SSE reconnect replay would double-apply already-seen chunks). step.* events
    // have their own idempotency via StepId / StepId+TurnId checks, so we must
    // NOT block out-of-order step.opened (e.g. block.delta seq=2 arriving before
    // step.opened seq=1 due to network reorder). AUDIT 2.3.
    if (event.Kind === 'block.appended' || event.Kind === 'block.delta' || event.Kind === 'step.reset' || event.Kind === 'step.execution_progress') {
      if (shouldSkipEvent(event.StepId, event.EventSeq, seqTracking)) continue
    }
    switch (event.Kind) {
      case 'step.opened': {
        // AUDIT 4.4: idempotency check uses StepId only. StepIds are server-
        // generated UUIDs, globally unique — the prior (StepId, TurnId) check
        // was inconsistent with the rest of the reducer / mergeSteps / cache
        // (all keyed by Id alone) and could split state when the same StepId
        // appeared under a stale TurnId (replayed event after turn lifecycle).
        if (has(event.StepId)) {
          if (stepDebugEnabled()) console.debug(`[step.reducer] step.opened IGNORED (duplicate): stepId=${event.StepId}`)
          continue
        }
        if (stepDebugEnabled()) console.debug(`[step.reducer] step.opened: stepId=${event.StepId} role=${event.Role} type=${event.StepType} turnId=${event.TurnId}`)
        append({
          Id: event.StepId,
          Role: event.Role ?? 'assistant',
          Type: event.StepType ?? 'text',
          Content: event.Block ? [{ ...event.Block }] : [],
          Closed: false,
          Timestamp: new Date().toISOString(),
          TurnId: event.TurnId,
          ContentStatus: 'appending',
          ExecutionStatus: 'idle',
          InteractionStatus: 'none',
          Seq: event.Seq,
          Meta: event.Meta,
          Model: event.Model,
          Provider: event.Provider,
        })
        continue
      }

      case 'block.appended': {
        if (!event.Block) {
          if (stepDebugEnabled()) console.debug(`[step.reducer] block.appended IGNORED (no block): stepId=${event.StepId}`)
          continue
        }
        if (!has(event.StepId)) continue
        if (stepDebugEnabled()) console.debug(`[step.reducer] block.appended: stepId=${event.StepId} blockType=${event.Block.Type}`)
        const cur = at(event.StepId)
        if (cur.Discarded) continue
        replace(event.StepId, { ...cur, Content: [...cur.Content, { ...event.Block! }], ContentStatus: 'appending' as const })
        continue
      }

      case 'block.delta': {
        const idx = event.BlockIndex ?? 0
        if (!has(event.StepId)) continue
        if (stepDebugEnabled()) console.debug(`[step.reducer] block.delta: stepId=${event.StepId} idx=${idx} deltaLen=${event.Delta?.length ?? 0}`)
        const cur = at(event.StepId)
        if (cur.Discarded) continue
        // Reasoning step: accumulate into ReasoningContent; Content stays empty.
        if (cur.Type === 'reasoning') {
          replace(event.StepId, {
            ...cur,
            ReasoningContent: (cur.ReasoningContent ?? '') + (event.Delta ?? ''),
            ContentStatus: 'appending' as const,
          })
          continue
        }
        const content = [...cur.Content]
        if (idx >= 0 && idx < content.length) {
          const block = content[idx]!
          content[idx] = { ...block, Text: (block.Text ?? '') + (event.Delta ?? '') }
        } else if (idx === content.length) {
          content.push({ Type: 'text', Text: event.Delta ?? '' })
        }
        replace(event.StepId, { ...cur, Content: content, ContentStatus: 'appending' as const })
        continue
      }

      case 'step.closed': {
        if (!has(event.StepId)) continue
        if (stepDebugEnabled()) console.debug(`[step.reducer] step.closed: stepId=${event.StepId}`)
        const cur = at(event.StepId)
        const discarded = event.Discarded ?? cur.Discarded
        replace(event.StepId, {
          ...cur,
          Closed: true,
          ContentStatus: 'stable' as const,
          Usage: event.Usage ?? cur.Usage,
          Discarded: discarded,
          Content: discarded ? [] : cur.Content,
        })
        continue
      }

      case 'step.reset': {
        // A retried dispatch (e.g. mid-stream cut) clears the streamed content
        // of the failed attempt so the regenerated stream replaces it. The step
        // stays open and keeps its ID.
        if (!has(event.StepId)) continue
        console.info(`[step.reducer] step.reset: stepId=${event.StepId}`)
        const cur = at(event.StepId)
        if (cur.Discarded) continue
        if (cur.Type === 'reasoning') {
          replace(event.StepId, { ...cur, ReasoningContent: '', ContentStatus: 'appending' as const })
          continue
        }
        const content = cur.Content.map(b => ({ ...b, Text: '' }))
        replace(event.StepId, { ...cur, Content: content, ContentStatus: 'appending' as const })
        continue
      }

      case 'step.error': {
        if (!has(event.StepId)) continue
        console.warn(`[step.reducer] step.error: stepId=${event.StepId} error=${event.Error ?? ''}`)
        const cur = at(event.StepId)
        replace(event.StepId, { ...cur, Error: event.Error ?? '', Closed: true })
        continue
      }

      // ── Phase 2: Interaction / Task / Execution events ──

      case 'step.interaction_requested': {
        // Idempotency by StepId only (consistent with step.opened, AUDIT 4.4).
        if (has(event.StepId)) {
          if (stepDebugEnabled()) console.debug(`[step.reducer] step.interaction_requested IGNORED (duplicate): stepId=${event.StepId}`)
          continue
        }
        if (stepDebugEnabled()) console.debug(`[step.reducer] step.interaction_requested: stepId=${event.StepId} type=${event.InteractionType}`)
        append({
          Id: event.StepId,
          Role: 'assistant',
          Type: event.InteractionType ?? 'interaction',
          Content: event.Task ? [{
            Type: 'text',
            Text: JSON.stringify(event.Task),
          }] : [],
          Closed: false,
          Timestamp: new Date().toISOString(),
          TurnId: event.TurnId,
          ContentStatus: 'stable',
          ExecutionStatus: 'idle',
          InteractionStatus: 'pending',
          Seq: event.Seq,
          RequestId: event.RequestId,
        })
        continue
      }

      case 'step.interaction_resolved': {
        if (!has(event.StepId)) continue
        if (stepDebugEnabled()) console.debug(`[step.reducer] step.interaction_resolved: stepId=${event.StepId}`)
        const cur = at(event.StepId)
        const content = [...cur.Content]
        if (event.Task && Object.keys(event.Task).length > 0) {
          content.push({ Type: 'text', Text: JSON.stringify(event.Task) } as ContentBlock)
        }
        replace(event.StepId, { ...cur, InteractionStatus: 'resolved' as const, Closed: true, ContentStatus: 'stable' as const, Content: content })
        continue
      }

      case 'step.execution_progress': {
        if (!has(event.StepId)) continue
        if (stepDebugEnabled()) console.debug(`[step.reducer] step.execution_progress: stepId=${event.StepId}`)
        const cur = at(event.StepId)
        // Empty payload: a legacy "status-only" progress tick — keep Progress and
        // the accumulator untouched (the old appendExecutionProgress returned
        // `previous` unchanged for empty deltas).
        if (!event.Progress) {
          replace(event.StepId, { ...cur, ExecutionStatus: 'in_progress' as const })
          continue
        }
        const delta = parseStreamDelta(event.Progress)
        if (!delta) {
          // Non-stream payload (explore/goal_review stats snapshot or malformed
          // JSON): Progress is replaced verbatim — legacy behavior — and the
          // accumulated stream text is dropped, exactly as the old full-string
          // merge lost accumulated stdout/stderr keys once a non-stream delta
          // arrived.
          const next = { ...cur, ExecutionStatus: 'in_progress' as const, Progress: event.Progress }
          setExecutionProgressAcc(next, undefined)
          replace(event.StepId, next)
          continue
        }
        // Stream delta: O(1) append into the per-stream accumulator.
        // step.Progress keeps the latest delta verbatim (the wire string), not a
        // re-stringified accumulation — the consumer reads accumulated text from
        // the acc symbol and falls back to parsing Progress for loaded/replayed
        // sessions. Protocol (Progress is a string) is unchanged.
        const stream = delta.stream as string
        const acc: ExecutionProgressAcc = { ...(getExecutionProgressAcc(cur) ?? {}) }
        acc[stream] = (acc[stream] ?? '') + (delta.text as string)
        const next = { ...cur, ExecutionStatus: 'in_progress' as const, Progress: event.Progress }
        setExecutionProgressAcc(next, acc)
        replace(event.StepId, next)
        continue
      }

      case 'step.execution_completed': {
        if (!has(event.StepId)) continue
        if (stepDebugEnabled()) console.debug(`[step.reducer] step.execution_completed: stepId=${event.StepId}`)
        const cur = at(event.StepId)
        replace(event.StepId, { ...cur, ExecutionStatus: 'completed' as const })
        continue
      }

      case 'step.task_created':
      case 'step.task_updated':
      case 'step.task_deleted': {
        // Task events are aggregated at the Turn level (Turn.tasks) — no step state change.
        if (stepDebugEnabled()) console.debug(`[step.reducer] ${event.Kind}: taskId=${event.TaskId}`)
        continue
      }

      case 'step.file_changes': {
        // Display-only event consumed by Turntail; no step state change.
        continue
      }

      default:
        console.warn(`[step.reducer] unknown event kind: ${event.Kind}`)
        continue
    }
  }

  return draft ?? steps
}
