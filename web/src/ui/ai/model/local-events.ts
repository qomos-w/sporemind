// AILocalEvent: events the UI dispatches in response to user interaction
// (answering a permission prompt, resolving an ask_user question, etc.).
// Distinct from TurnEvent — these never come from the backend stream, and
// each one fans out to (a) a local frame update via the reducer and
// (b) an outbound callable to the agent (agent.turn.answer).
//
// `requestId` is the 128-bit canonical ID issued by the agent on the
// pause event (permission_requested / ask_user). The reducer uses it to
// match the answer back to the exact paused frame, and the backend
// validates it on agent.turn.answer to reject stale responses.

import type { PlanFrame } from './frame-types'

export type AILocalEvent =
  | { kind: 'ai.ask_answered'; requestId: string; answers: Record<number, string> }
  | { kind: 'ai.permission_answered'; requestId: string; allowed: boolean; allowInProject?: boolean }
  | { kind: 'ai.plan_approval_answered'; requestId: string; decision: 'approve' | 'reject' | 'edit' | 'confirm_goal' | 'start_workflow'; editedPlan?: string; feedback?: string; selectedPolicy?: PlanFrame['policy'] }
  | { kind: 'ai.goal_submit_answered'; requestId: string; decision: 'approve' | 'reject'; feedback?: string }
  | { kind: 'ai.goal_card_submit_answered'; requestId: string; decision: 'approve' | 'reject'; feedback?: string }

export function isAILocalEvent(value: unknown): value is AILocalEvent {
  if (!value || typeof value !== 'object') return false
  const k = (value as { kind?: unknown }).kind
  return k === 'ai.ask_answered' || k === 'ai.permission_answered' || k === 'ai.plan_approval_answered' || k === 'ai.goal_submit_answered' || k === 'ai.goal_card_submit_answered'
}
