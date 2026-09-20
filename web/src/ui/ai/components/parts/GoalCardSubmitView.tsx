import React, { useCallback, useState } from 'react'
import { FileText, CheckCircle, XCircle } from 'lucide-react'
import type { GoalCardSubmitFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { useViewportMode } from '../../../../application/useViewportMode'
import { TimelineStep } from '../timeline'
import { useStepInteraction } from '../../hooks/useStepInteraction'
import { AIStepText } from './AIStepText.tsx'
import { MobileCardComposer } from '../MobileCardComposer'

interface GoalCardSubmitViewProps {
  frame: GoalCardSubmitFrame
  connectorMode?: TimelineConnectorMode
}

export const GoalCardSubmitView: React.FC<GoalCardSubmitViewProps> = ({
  frame,
  connectorMode = 'none',
}) => {
  const { t } = useI18n()
  const mode = useViewportMode()
  const isMobile = mode === 'mobile'
  const { submitGoalCardApproval, submitting } = useStepInteraction(frame.requestId ?? '')
  const [feedback, setFeedback] = useState('')
  const [showFeedbackInput, setShowFeedbackInput] = useState(false)
  const [composerOpen, setComposerOpen] = useState(false)

  const isPending = frame.approvalStatus === 'pending'
  const isApproved = frame.approvalStatus === 'approved'
  const isRejected = frame.approvalStatus === 'rejected'
  const isCancelled = frame.approvalStatus === 'cancelled'

  const handleApprove = useCallback(() => {
    if (!frame.requestId || submitting) return
    submitGoalCardApproval('approve')
  }, [submitGoalCardApproval, frame.requestId, submitting])

  const handleReject = useCallback(() => {
    if (!frame.requestId || submitting) return
    submitGoalCardApproval('reject', feedback.trim() || undefined)
    setShowFeedbackInput(false)
    setComposerOpen(false)
    setFeedback('')
  }, [submitGoalCardApproval, frame.requestId, feedback, submitting])

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
    labelText = t('ai.goalCardSubmit.confirmed')
    statusClass = 'ai-goal-submit-confirmed'
  } else if (isRejected) {
    icon = <XCircle size={12} className="ai-step-icon ai-step-icon-error" />
    labelText = t('ai.goalCardSubmit.rejected')
    statusClass = 'ai-goal-submit-rejected'
  } else if (isCancelled) {
    icon = <XCircle size={12} className="ai-step-icon ai-step-icon-warning" />
    labelText = t('ai.goalCardSubmit.cancelled')
    statusClass = 'ai-goal-submit-cancelled'
  } else {
    icon = <FileText size={12} className="ai-step-icon" />
    labelText = t('ai.goalCardSubmit.confirmCard')
    statusClass = 'ai-goal-submit-pending'
  }

  const label = (
    <span className={`ai-step-label ${statusClass}`}>{labelText}</span>
  )

  const detail = (
    <div className="ai-goal-submit-detail">
      {frame.cardId && (
        <div className="ai-goal-submit-row">
          <span className="ai-goal-submit-label">{t('ai.goalCardSubmit.cardId')}</span>
          <AIStepText className="ai-goal-submit-value ai-goal-submit-original">{frame.cardId}</AIStepText>
        </div>
      )}
      {frame.interpretedGoal && (
        <div className="ai-goal-submit-row">
          <span className="ai-goal-submit-label">{t('ai.goalCardSubmit.interpreted')}</span>
          <AIStepText className="ai-goal-submit-value ai-goal-submit-interpreted">{frame.interpretedGoal}</AIStepText>
        </div>
      )}
      {isPending && (
        <div className="ai-goal-submit-actions">
          <button
            className="ai-goal-submit-btn ai-goal-submit-btn-approve"
            onClick={handleApprove}
          >
            {t('ai.goalCardSubmit.confirm')}
          </button>
          <button
            className="ai-goal-submit-btn ai-goal-submit-btn-reject"
            onClick={openRejectComposer}
          >
            {t('ai.goalCardSubmit.reject')}
          </button>
        </div>
      )}
      {showFeedbackInput && !isMobile && (
        <div className="ai-goal-submit-feedback">
          <textarea
            className="ai-goal-submit-feedback-input"
            value={feedback}
            onChange={e => setFeedback(e.target.value)}
            placeholder={t('ai.goalCardSubmit.feedbackPlaceholder')}
            rows={2}
            autoFocus
          />
          <div className="ai-goal-submit-feedback-actions">
            <button
              className="ai-goal-submit-btn ai-goal-submit-btn-reject"
              onClick={handleReject}
            >
              {t('ai.goalCardSubmit.submitRejection')}
            </button>
            <button
              className="ai-goal-submit-btn ai-goal-submit-btn-cancel"
              onClick={() => { setShowFeedbackInput(false); setFeedback('') }}
            >
              {t('ai.goalCardSubmit.cancel')}
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
        title={t('ai.goalCardSubmit.reject')}
        placeholder={t('ai.goalCardSubmit.feedbackPlaceholder')}
        submitLabel={t('ai.goalCardSubmit.submitRejection')}
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
