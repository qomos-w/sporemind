// ── Pending interaction detection ──
// Derives the latest unresolved blocking interaction (ask_user / permission /
// goal submit / plan approval) from conversation envelopes. Used by the
// message drawer collapsed peek bar to surface interactions the user must
// answer while the drawer is not expanded.

import type {
  Frame,
  TurnEnvelope,
  AskUserQuestionFrame,
  PermissionRequestFrame,
  GoalSubmitFrame,
  GoalCardSubmitFrame,
  PlanFrame,
} from './frame-types'

export type PendingInteraction =
  | { kind: 'ask_user'; frame: AskUserQuestionFrame }
  | { kind: 'permission_request'; frame: PermissionRequestFrame }
  | { kind: 'goal_submit'; frame: GoalSubmitFrame }
  | { kind: 'goal_card_submit'; frame: GoalCardSubmitFrame }
  | { kind: 'plan'; frame: PlanFrame }

function isLiveStatus(status: string): boolean {
  return status === 'running' || status === 'pending'
}

/** Classify a single frame as an unresolved blocking interaction, or null. */
function framePendingInteraction(f: Frame): PendingInteraction | null {
  switch (f.type) {
    case 'ask_user':
      if ((f as AskUserQuestionFrame).answers == null && isLiveStatus(f.status)) {
        return { kind: 'ask_user', frame: f as AskUserQuestionFrame }
      }
      return null
    case 'permission_request':
      if ((f as PermissionRequestFrame).allowed == null && isLiveStatus(f.status)) {
        return { kind: 'permission_request', frame: f as PermissionRequestFrame }
      }
      return null
    case 'goal_submit':
      if ((f as GoalSubmitFrame).approvalStatus === 'pending') {
        return { kind: 'goal_submit', frame: f as GoalSubmitFrame }
      }
      return null
    case 'goal_card_submit':
      if ((f as GoalCardSubmitFrame).approvalStatus === 'pending') {
        return { kind: 'goal_card_submit', frame: f as GoalCardSubmitFrame }
      }
      return null
    case 'plan':
      if ((f as PlanFrame).approvalStatus === 'pending' && isLiveStatus(f.status)) {
        return { kind: 'plan', frame: f as PlanFrame }
      }
      return null
    default:
      return null
  }
}

/** Scan envelopes newest-first and return the latest unresolved blocking
 *  interaction, or null when nothing awaits the user. */
export function extractPendingInteraction(envelopes: TurnEnvelope[]): PendingInteraction | null {
  for (let i = envelopes.length - 1; i >= 0; i--) {
    const env = envelopes[i]!
    if (env.role !== 'assistant') continue
    for (let j = env.frames.length - 1; j >= 0; j--) {
      const found = framePendingInteraction(env.frames[j]!)
      if (found) return found
    }
  }
  return null
}

/** True when the envelope carries any unresolved blocking interaction frame.
 *  Such envelopes hold unsaved user input (plan drafts, answers, approvals):
 *  virtualizing them away unmounts the card mid-interaction and destroys the
 *  typed content, so message-stream virtualization must skip them. */
export function envelopeHasPendingInteraction(env: TurnEnvelope): boolean {
  if (env.role !== 'assistant') return false
  for (let j = env.frames.length - 1; j >= 0; j--) {
    if (framePendingInteraction(env.frames[j]!)) return true
  }
  return false
}
