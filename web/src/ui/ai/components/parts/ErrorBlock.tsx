import React from 'react'
import { AlertTriangle } from 'lucide-react'
import type { Frame, ErrorFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { TimelineStep } from '../timeline'

interface ErrorBlockProps {
  frame: ErrorFrame
  connectorMode?: TimelineConnectorMode
  onFrameSelect?: (frame: Frame) => void
}

export const ErrorBlock: React.FC<ErrorBlockProps> = ({ frame, connectorMode = 'none', onFrameSelect }) => {
  const { t } = useI18n()
  const colorClass = frame.status === 'error' || frame.status === 'completed'
    ? 'ai-step-icon-error'
    : ''
  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={<AlertTriangle size={12} className={`ai-step-icon ${colorClass}`} />}
      label={
        <>
          <span className="ai-step-label">{frame.toolName ?? t('ai.step.error')}</span>
          <span className="ai-error-subtitle">
            <span className="ai-error-subtitle-text" title={frame.message}>{frame.message}</span>
          </span>
          {frame.code && <span className="ai-error-code">{frame.code}</span>}
        </>
      }
      connectorMode={connectorMode}
      expandable
      onMaximize={onFrameSelect ? () => onFrameSelect(frame) : undefined}
    >
      <div className="ai-error-message">{frame.message}</div>
    </TimelineStep>
  )
}
