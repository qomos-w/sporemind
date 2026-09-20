import React from 'react'
import type { Frame, UIFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { TimelineStep, TransitionHost } from '../timeline'

interface UIFrameBlockProps {
  frame: UIFrame
  version: number
  connectorMode?: TimelineConnectorMode
  onFrameSelect?: (frame: Frame) => void
}

export const UIFrameBlock: React.FC<UIFrameBlockProps> = ({ frame, version, connectorMode = 'none', onFrameSelect }) => {
  const { t } = useI18n()
  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={<span className="ai-step-icon">◆</span>}
      label={<span className="ai-step-label">{t('ai.step.component')}</span>}
      connectorMode={connectorMode}
      onMaximize={onFrameSelect ? () => onFrameSelect(frame) : undefined}
    >
      <TransitionHost animation={frame.animation ?? 'fade'} version={version}>
        <div
          className="ai-ui-frame-mount"
          dangerouslySetInnerHTML={{ __html: frame.html }}
        />
      </TransitionHost>
    </TimelineStep>
  )
}
