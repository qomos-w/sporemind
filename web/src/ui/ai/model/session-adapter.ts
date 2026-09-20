import type { TurnEnvelope, TurnUsage, ContextBudget, TurnMetadata } from './frame-types'
import type { UsageData, Turn, ExploreResult } from '../../../gen-types/aigen'
import type { TurnRequestStat } from '../../../gen-types/aistats'
import type { TurnStatus } from '../../../gen-clients/system/types'
import { iconFromPath } from '../hooks/tool-parsers'

function sessionUsage(u: UsageData | undefined): TurnUsage | undefined {
  if (!u) return undefined
  return {
    inputTokens: u.InputTokens ?? undefined,
    outputTokens: u.OutputTokens ?? undefined,
    totalTokens: u.TotalTokens ?? undefined,
  }
}

// ── Legacy Turn-to-Frame converters removed after Phase 1 flattening ──
// blockStatus, blockToFrame, entryToFrames, and stepToFrame were removed
// because Turn no longer carries Entries/Actions/Output. Frames now come
// from Steps via steps-to-envelopes.ts.

function turnCompleted(state: string | undefined): boolean {
  // Paused is a resumable live state, not a terminal state. Keeping this
  // false ensures the active envelope remains visible while the engine waits
  // for resume (and also keeps the pause controls/TurnTail active).
  // Waiting is terminal: the envelope closes and the UI renders as completed.
  return state === 'waiting' || state === 'completed' || state === 'failed' || state === 'cancelled' || state === 'abandoned'
}

/** Map backend Turn.State string to the frontend turnState union. Backend
 *  states not in the union (e.g. 'idle') fall through to 'running' so the
 *  timer keeps ticking until a terminal signal arrives. */
function mapTurnState(state: string | undefined): TurnMetadata['turnState'] {
  switch (state) {
    case 'completed': return 'completed'
    case 'failed': return 'failed'
    case 'cancelled': return 'cancelled'
    case 'abandoned': return 'abandoned'
    case 'paused': return 'paused'
    case 'waiting': return 'waiting'
    default: return 'running'
  }
}

function assistantTurnToEnvelope(turn: Turn): TurnEnvelope {
  // After Phase 1 flattening, Turn is metadata-only. Frames come from Steps
  // via stepsToEnvelopes. This function provides metadata (tasks, contextBudget,
  // fileChanges, usage) for history envelopes when no step-derived envelope exists.
  const tasks = turn.Tasks?.map(t => ({
    id: t.Id,
    subject: t.Subject,
    status: t.Status as 'pending' | 'in_progress' | 'completed',
    activeForm: t.ActiveForm,
  }))

  const contextBudget: ContextBudget | undefined = turn.ContextBudget
    ? {
        estimatedTokens: turn.ContextBudget.EstimatedTokens ?? 0,
        contextWindowSize: turn.ContextBudget.ContextWindowSize ?? 0,
        tokenBudget: turn.ContextBudget.TokenBudget ?? 0,
      }
    : undefined

  const fileChanges = turn.FileChanges?.map(fc => ({
    filename: fc.Path.split('/').pop() ?? 'file',
    filepath: fc.Path,
    icon: iconFromPath(fc.Path),
    additions: fc.Additions,
    deletions: fc.Deletions,
    diffContent: fc.DiffContent,
  }))

  // Use StartedAt for assistant turns when available; fallback to Timestamp.
  const timestamp = turn.StartedAt || turn.Timestamp || ''

  return {
    id: turn.Id,
    role: 'assistant',
    frames: [], // Frames from Steps (via stepsToEnvelopes), not from Turn
    tasks,
    fileChanges,
    timestamp,
    completed: turnCompleted(turn.State),
    seq: undefined,
    turnSeq: turn.TurnOrder ?? turn.Seq ?? undefined,
    metadata: {
      turnId: turn.Id,
      model: turn.Unit?.model || undefined,
      usage: sessionUsage(turn.Usage),
      requestStats: turn.RequestStats as TurnRequestStat[] | undefined,
      contextBudget,
      turnState: mapTurnState(turn.State),
      revision: turn.Revision ?? undefined,
      startedAt: turn.StartedAt || undefined,
      completedAt: turn.CompletedAt || undefined,
      error: turn.Error || undefined,
    },
  }
}

function userTurnToEnvelope(turn: Turn): TurnEnvelope {
  const content = turn.UserInput ?? ''
  // User turns are terminal by default. The compact/recompact turn is the one
  // user turn with a live lifecycle: it stays running until compaction ends,
  // so the persisted State must drive the envelope instead of a hardcoded
  // completed=true (which froze the turn as done while compaction ran).
  const stateKnown = turn.State != null && turn.State !== ''
  const metadata = stateKnown
    ? {
        turnId: turn.Id,
        turnState: mapTurnState(turn.State),
        revision: turn.Revision ?? undefined,
        startedAt: turn.StartedAt || undefined,
        completedAt: turn.CompletedAt || undefined,
        error: turn.Error || undefined,
      }
    : { turnId: turn.Id }
  return {
    id: turn.Id,
    role: 'user',
    userContent: content,
    userAttachments: turn.UserAttachments?.map(a => ({
      name: a.Name,
      mimeType: a.MimeType,
      url: a.Url,
      sizeBytes: a.SizeBytes,
    })),
    userImages: turn.UserImages?.map(img => ({ url: img.Url, alt: img.Alt })),
    frames: content
      ? [{ id: `${turn.Id}-text`, type: 'text', status: 'completed', content }]
      : [],
    timestamp: turn.Timestamp || '',
    completed: stateKnown ? turnCompleted(turn.State) : true,
    seq: undefined,
    turnSeq: turn.TurnOrder ?? turn.Seq ?? undefined,
    metadata,
  }
}

export function sessionTurnsToEnvelopes(turns: Turn[], exploreResults?: ExploreResult[]): TurnEnvelope[] {
  const result = turns.map(turn => {
    if (turn.Role === 'user') return userTurnToEnvelope(turn)
    return assistantTurnToEnvelope(turn)
  })

  if (!exploreResults || exploreResults.length === 0) return result

  // Patch explore progress from persisted ExploreResults into matching fork tool frames.
  // Key by StepId when available; fall back to TurnId for legacy data.
  const exploreByStepId = new Map<string, ExploreResult>()
  const exploreByTurnId = new Map<string, ExploreResult>()
  for (const er of exploreResults) {
    if (er.StepId) {
      exploreByStepId.set(er.StepId, er)
    } else {
      exploreByTurnId.set(er.TurnId, er)
    }
  }

  return result.map(env => {
    if (env.role !== 'assistant') return env
    let modified = false
    const newFrames = env.frames.map(f => {
      if (f.type === 'tool' && /^(explore|fork)/.test((f as any).toolName?.toLowerCase() ?? '')) {
        const er = exploreByStepId.get(f.id) ?? exploreByTurnId.get(env.id)
        if (er) {
          modified = true
          return {
            ...f,
            progress: {
              summaryText: er.Summary,
              searchCount: er.SearchCount,
              readCount: er.ReadCount,
            },
          }
        }
      }
      return f
    })
    return modified ? { ...env, frames: newFrames } : env
  })
}

export function turnStatusToEnvelope(status: TurnStatus): TurnEnvelope {
  const envelope = assistantTurnToEnvelope(status.Turn as Turn)
  envelope.completed = turnCompleted(status.Turn.State)
  // ActiveTurn carries the authoritative start timestamp on the status wrapper,
  // not always on the nested Turn itself.
  if (status.StartedAt && envelope.metadata) {
    envelope.metadata.startedAt = status.StartedAt
  }
  return envelope
}

