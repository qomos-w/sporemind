import { createContext, useContext, useState, useCallback } from 'react'
import type { PlanFrame } from '../model/frame-types'
import type { AILocalEvent } from '../model/local-events'

export type InteractionDispatchFn = (event: AILocalEvent) => Promise<void>

export const InteractionDispatchContext = createContext<InteractionDispatchFn | null>(null)

export function useStepInteraction(requestId: string) {
  const dispatch = useContext(InteractionDispatchContext)
  const [submitting, setSubmitting] = useState(false)

  const wrap = useCallback(async (event: AILocalEvent) => {
    if (!dispatch) {
      console.warn('[useStepInteraction] No InteractionDispatchContext provider found')
      return
    }
    setSubmitting(true)
    try {
      await dispatch(event)
    } finally {
      setSubmitting(false)
    }
  }, [dispatch])

  const submitAskAnswer = useCallback((answers: Record<number, string>) => {
    void wrap({ kind: 'ai.ask_answered', requestId, answers })
  }, [wrap, requestId])

  const submitPermission = useCallback((allowed: boolean, allowInProject?: boolean) => {
    void wrap({ kind: 'ai.permission_answered', requestId, allowed, allowInProject })
  }, [wrap, requestId])

  const submitPlanApproval = useCallback((
    decision: 'approve' | 'reject' | 'edit' | 'confirm_goal' | 'start_workflow',
    editedPlan?: string,
    feedback?: string,
    selectedPolicy?: PlanFrame['policy'],
  ) => {
    void wrap({ kind: 'ai.plan_approval_answered', requestId, decision, editedPlan, feedback, selectedPolicy })
  }, [wrap, requestId])

  const submitGoalApproval = useCallback((
    decision: 'approve' | 'reject',
    feedback?: string,
  ) => {
    void wrap({ kind: 'ai.goal_submit_answered', requestId, decision, feedback })
  }, [wrap, requestId])

  const submitGoalCardApproval = useCallback((
    decision: 'approve' | 'reject',
    feedback?: string,
  ) => {
    void wrap({ kind: 'ai.goal_card_submit_answered', requestId, decision, feedback })
  }, [wrap, requestId])

  return { submitAskAnswer, submitPermission, submitPlanApproval, submitGoalApproval, submitGoalCardApproval, submitting }
}
