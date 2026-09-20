import React from 'react'
import { Target, CheckCircle, AlertCircle, Ban } from 'lucide-react'
import type { GoalReviewFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { TimelineStep } from '../timeline'
import { ExploreWorkItem } from './ExploreToolView.tsx'
import { useSmoothText } from './useSmoothStreamContent.ts'
import { useSmoothStreamContext } from '../../context/SmoothStreamContext'

interface GoalReviewViewProps {
  frame: GoalReviewFrame
  connectorMode?: TimelineConnectorMode
}

export const GoalReviewView: React.FC<GoalReviewViewProps> = ({ frame, connectorMode = 'none' }) => {
  const { t } = useI18n()
  const isRunning = frame.status === 'running'
  const achieved = frame.achieved === true
  const aborted = frame.aborted === true

  let icon: React.ReactNode
  let labelText: string
  let statusClass = ''

  if (isRunning) {
    icon = <Target size={12} className="ai-step-icon" />
    labelText = t('ai.goalReview.reviewing', { condition: frame.condition })
  } else if (achieved) {
    icon = <CheckCircle size={12} className="ai-step-icon ai-step-icon-success" />
    labelText = t('ai.goalReview.achieved')
    statusClass = 'ai-goal-review-achieved'
  } else if (aborted) {
    icon = <Ban size={12} className="ai-step-icon ai-step-icon-error" />
    labelText = t('ai.goalReview.aborted')
    statusClass = 'ai-goal-review-aborted'
  } else {
    icon = <AlertCircle size={12} className="ai-step-icon ai-step-icon-warning" />
    labelText = t('ai.goalReview.notAchieved')
    statusClass = 'ai-goal-review-not-achieved'
  }

  const label = (
    <span className={`ai-step-label ${statusClass}`}>{labelText}</span>
  )

  const progress = frame.progress
  const phase = progress?.phase
  const activeWork = progress?.activeWork ?? []
  const searchCount = progress?.searchCount ?? 0
  const readCount = progress?.readCount ?? 0
  const summaryText = progress?.summaryText
  const { smoothStream } = useSmoothStreamContext()
  const { displayed: smoothedSummary, guardRef: smoothGuardRef } = useSmoothText(summaryText ?? '', isRunning && smoothStream)

  const statsParts: string[] = []
  if (searchCount > 0) statsParts.push(`Searched ${searchCount} files`)
  if (readCount > 0) statsParts.push(`Read ${readCount} files`)
  const statsText = statsParts.length > 0 ? statsParts.join(', ') : undefined

  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={icon}
      label={label}
      connectorMode={connectorMode}
      expandable
      expansionMode="auto-while-active"
    >
      <div className="ai-goal-review-detail">
        {isRunning && (
          <div className="ai-explore-running">
            {phase && <span className="ai-explore-stats">{phase}</span>}
            {statsText && <span className="ai-explore-stats">{statsText}</span>}
          </div>
        )}

        {activeWork.length > 0 && (
          <div className="ai-explore-work-list">
            {activeWork.slice(-5).map((item, idx) => (
              <ExploreWorkItem
                key={idx}
                type={item.Type}
                target={item.Target}
                countLabel={item.Type === 'search' && searchCount > 0 ? `${searchCount} searches` : undefined}
              />
            ))}
            {activeWork.length > 5 && (
              <div className="ai-explore-work-item ai-explore-work-more">
                ... and {activeWork.length - 5} more
              </div>
            )}
          </div>
        )}

        {smoothedSummary && isRunning && (
          <div ref={smoothGuardRef} className="ai-goal-review-summary ai-text-content">
            {smoothedSummary}
          </div>
        )}

        {frame.condition && (
          <div className="ai-goal-review-row">
            <span className="ai-goal-review-label">{t('ai.goalReview.condition')}</span>
            <span className="ai-goal-review-value">{frame.condition}</span>
          </div>
        )}
        {frame.reason && (
          <div className="ai-goal-review-row">
            <span className="ai-goal-review-label">{t('ai.goalReview.reason')}</span>
            <span className="ai-goal-review-value">{frame.reason}</span>
          </div>
        )}
        {(frame.turnCount != null || frame.maxTurns != null) && (
          <div className="ai-goal-review-row">
            <span className="ai-goal-review-label">{t('ai.goalReview.turns')}</span>
            <span className="ai-goal-review-value">
              {frame.turnCount ?? '?'} / {frame.maxTurns ?? '?'}
            </span>
          </div>
        )}
        {frame.planCardCount != null && frame.planCardCount > 0 && (
          <div className="ai-goal-review-row">
            <span className="ai-goal-review-label">{t('ai.goalReview.plans')}</span>
            <span className="ai-goal-review-value">{frame.planCardCount}</span>
          </div>
        )}
      </div>
    </TimelineStep>
  )
}
