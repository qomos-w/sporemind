import React from 'react'
import { MessageSquare } from 'lucide-react'
import type { UserInjectFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { TimelineStep } from '../timeline'

interface UserInjectBlockProps {
  frame: UserInjectFrame
  connectorMode?: TimelineConnectorMode
}

export const UserInjectBlock: React.FC<UserInjectBlockProps> = ({ frame, connectorMode = 'none' }) => {
  const { t } = useI18n()
  const isCompleted = frame.status === 'completed'
  const count = frame.items.length
  const label = isCompleted
    ? (count === 1 ? t('ai.step.injectedMessage') : t('ai.step.injectedMessages', { count }))
    : (count === 1 ? t('ai.step.queuedMessage') : t('ai.step.queuedMessages', { count }))
  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={<MessageSquare size={12} className="ai-step-icon" />}
      label={<span className={`ai-step-label ${isCompleted ? '' : 'ai-step-label-queued'}`}>{label}</span>}
      connectorMode={connectorMode}
      expandable={true}
      expansionMode="open"
    >
      <div className="ai-pending-user-input-list">
        {frame.items.map((item) => (
          <div key={item.id} className="ai-pending-user-input-item">{item.text}</div>
        ))}
      </div>
    </TimelineStep>
  )
}
