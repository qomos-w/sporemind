import React, { useCallback, useState } from 'react'
import { Target, CheckCircle, XCircle } from 'lucide-react'
import type { GoalSubmitFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { useViewportMode } from '../../../../application/useViewportMode'
import { TimelineStep } from '../timeline'
import { useStepInteraction } from '../../hooks/useStepInteraction'
import { AIStepText } from './AIStepText.tsx'
import { MobileCardComposer } from '../MobileCardComposer'

interface GoalSubmitViewProps {
  frame: GoalSubmitFrame
  connectorMode?: TimelineConnectorMode
}

export const GoalSubmitView: React.FC<GoalSubmitViewProps> = ({
  frame,
  connectorMode = 'none',
}) => {
  const { t } = useI18n()
  const mode = useViewportMode()
  const isMobile = mode === 'mobile'
  const { submitGoalApproval, submitting } = useStepInteraction(frame.requestId ?? '')
  const [feedback, setFeedback] = useState('')
  const [showFeedbackInput, setShowFeedbackInput] = useState(false)
  const [composerOpen, setComposerOpen] = useState(false)

  const isPending = frame.approvalStatus === 'pending'
  const isApproved = frame.approvalStatus === 'approved'
  const isRejected = frame.approvalStatus === 'rejected'
  const isCancelled = frame.approvalStatus === 'cancelled'

  const handleApprove = useCallback(() => {
    if (!frame.requestId || submitting) return
    submitGoalApproval('approve')
  }, [submitGoalApproval, frame.requestId, submitting])

  const handleReject = useCallback(() => {
    if (!frame.requestId || submitting) return
    submitGoalApproval('reject', feedback.trim() || undefined)
    setShowFeedbackInput(false)
    setComposerOpen(false)
    setFeedback('')
  }, [submitGoalApproval, frame.requestId, feedback, submitting])

  const openRejectComposer = useCallback(() => {
    setShowFeedbackInput(true)
    if (isMobile) setComposerOpen(true)
  }, [isMobile])

  const closeRejectComposer = useCallback(() => {
    setShowFeedbackInput(false)
    setComposerOpen(false)
    setFeedback('')
  }, [])

  let icon: React.ReactNode
  let labelText: string
  let statusClass = ''

  if (isApproved) {
    icon = <CheckCircle size={12} className="ai-step-icon ai-step-icon-success" />
    labelText = t('ai.goalSubmit.confirmed')
    statusClass = 'ai-goal-submit-confirmed'
  } else if (isRejected) {
    icon = <XCircle size={12} className="ai-step-icon ai-step-icon-error" />
    labelText = t('ai.goalSubmit.rejected')
    statusClass = 'ai-goal-submit-rejected'
  } else if (isCancelled) {
    icon = <XCircle size={12} className="ai-step-icon ai-step-icon-warning" />
    labelText = t('ai.goalSubmit.cancelled')
    statusClass = 'ai-goal-submit-cancelled'
  } else {
    icon = <Target size={12} className="ai-step-icon" />
    labelText = t('ai.goalSubmit.confirmGoal')
    statusClass = 'ai-goal-submit-pending'
  }

  const label = (
    <span className={`ai-step-label ${statusClass}`}>{labelText}</span>
  )

  const detail = (
    <div className="ai-goal-submit-detail">
      {frame.condition && (
        <div className="ai-goal-submit-row">
          <span className="ai-goal-submit-label">{t('ai.goalSubmit.youSaid')}</span>
          <AIStepText className="ai-goal-submit-value ai-goal-submit-original">{frame.condition}</AIStepText>
        </div>
      )}
      {frame.interpretedGoal && (
        <div className="ai-goal-submit-row">
          <span className="ai-goal-submit-label">{t('ai.goalSubmit.interpreted')}</span>
          <AIStepText className="ai-goal-submit-value ai-goal-submit-interpreted">{frame.interpretedGoal}</AIStepText>
        </div>
      )}
      {isPending && (
        <div className="ai-goal-submit-actions">
          <button
            className="ai-goal-submit-btn ai-goal-submit-btn-approve"
            onClick={handleApprove}
          >
            {t('ai.goalSubmit.confirm')}
          </button>
          <button
            className="ai-goal-submit-btn ai-goal-submit-btn-reject"
            onClick={openRejectComposer}
          >
            {t('ai.goalSubmit.reject')}
          </button>
        </div>
      )}
      {showFeedbackInput && !isMobile && (
        <div className="ai-goal-submit-feedback">
          <textarea
            className="ai-goal-submit-feedback-input"
            value={feedback}
            onChange={e => setFeedback(e.target.value)}
            placeholder={t('ai.goalSubmit.feedbackPlaceholder')}
            rows={2}
            autoFocus
          />
          <div className="ai-goal-submit-feedback-actions">
            <button
              className="ai-goal-submit-btn ai-goal-submit-btn-reject"
              onClick={handleReject}
            >
              {t('ai.goalSubmit.submitRejection')}
            </button>
            <button
              className="ai-goal-submit-btn ai-goal-submit-btn-cancel"
              onClick={() => { setShowFeedbackInput(false); setFeedback('') }}
            >
              {t('ai.goalSubmit.cancel')}
            </button>
          </div>
        </div>
      )}
      <MobileCardComposer
        open={composerOpen}
        value={feedback}
        onChange={setFeedback}
        onSubmit={handleReject}
        onClose={closeRejectComposer}
        title={t('ai.goalSubmit.reject')}
        placeholder={t('ai.goalSubmit.feedbackPlaceholder')}
        submitLabel={t('ai.goalSubmit.submitRejection')}
        multiline={true}
      />
    </div>
  )

  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={icon}
      label={label}
      connectorMode={connectorMode}
      expandable
      expansionMode={isPending ? 'open' : 'manual'}
    >
      {detail}
    </TimelineStep>
  )
}
