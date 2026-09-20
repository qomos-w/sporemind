import { useState, useCallback } from 'react'
import type { Frame, SlotEntry } from '../model/frame-types'

function frameSignature(frame: Frame): string {
  switch (frame.type) {
    case 'text':
      return `text:${frame.status}:${frame.content}`
    case 'plan':
      return `plan:${frame.status}:${frame.content}`
    case 'reasoning':
      return `reasoning:${frame.status}:${frame.content}`
    case 'tool':
      return `tool:${frame.status}:${frame.toolName}:${frame.input}:${frame.output ?? ''}:${frame.callableId ?? ''}:${frame.targetService ?? ''}:${JSON.stringify(frame.progress ?? null)}:${JSON.stringify(frame.fileChanges ?? null)}`
    case 'error':
      return `error:${frame.status}:${frame.message}:${frame.code ?? ''}`
    case 'image':
      return `image:${frame.status}:${frame.url}:${frame.alt ?? ''}`
    case 'sources':
      return `sources:${frame.status}:${JSON.stringify(frame.entries)}`
    case 'attachments':
      return `attachments:${frame.status}:${JSON.stringify(frame.files)}`
    case 'ask_user':
      return `ask_user:${frame.status}:${frame.requestId ?? ''}:${JSON.stringify(frame.questions)}:${JSON.stringify(frame.answers ?? null)}`
    case 'permission_request':
      return `permission_request:${frame.status}:${frame.requestId ?? ''}:${JSON.stringify(frame.toolCalls)}:${frame.reason ?? ''}:${String(frame.allowed ?? '')}`
    case 'ui':
      return `ui:${frame.status}:${frame.html}`
    case 'compaction':
      return `compaction:${frame.status}:${frame.trigger}:${frame.beforeTokens}:${frame.afterTokens}:${frame.rounds.length}`
    case 'goal_review':
      return `goal_review:${frame.status}:${frame.condition}:${String(frame.achieved ?? '')}:${frame.reason ?? ''}:${frame.turnCount ?? ''}:${frame.maxTurns ?? ''}:${String(frame.aborted ?? '')}:${JSON.stringify(frame.progress ?? null)}`
    case 'goal_submit':
      return `goal_submit:${frame.status}:${frame.condition}:${frame.interpretedGoal}:${frame.requestId}:${frame.approvalStatus}`
    case 'goal_card_submit':
      return `goal_card_submit:${frame.status}:${frame.cardId}:${frame.interpretedGoal ?? ''}:${frame.requestId}:${frame.approvalStatus}`
    default:
      return `${(frame as any).type}:${(frame as any).status}`
  }
}

/**
 * Tracks slot versions for in-place frame replacement.
 * When a frame with the same ID arrives and its rendered content changed,
 * the version increments, triggering TransitionHost animation.
 */
export function useSlotRegistry() {
  const [slots, setSlots] = useState<Map<string, SlotEntry>>(new Map())

  const registerFrames = useCallback((frames: Frame[]) => {
    if (frames.length === 0) return
    setSlots(prev => {
      let changed = false
      const next = new Map(prev)
      for (const frame of frames) {
        const existing = next.get(frame.id)
        if (existing?.frame === frame) {
          // Same reference: guaranteed same content, skip signature work.
          continue
        }
        const sameContent = existing ? frameSignature(existing.frame) === frameSignature(frame) : false
        const version = sameContent ? (existing?.version ?? 1) : (existing ? existing.version + 1 : 1)
        if (!existing || existing.version !== version) {
          changed = true
          next.set(frame.id, { id: frame.id, frame, version })
        }
      }
      return changed ? next : prev
    })
  }, [])

  const getVersion = useCallback((id: string): number => {
    return slots.get(id)?.version ?? 1
  }, [slots])

  const clear = useCallback(() => {
    setSlots(new Map())
  }, [])

  return { slots, registerFrames, getVersion, clear }
}
