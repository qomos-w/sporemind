import React from 'react'
import { Paperclip } from 'lucide-react'
import type { Frame, AttachmentsFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { TimelineStep } from '../timeline'

interface AttachmentsBlockProps {
  frame: AttachmentsFrame
  connectorMode?: TimelineConnectorMode
  onFrameSelect?: (frame: Frame) => void
}

export const AttachmentsBlock: React.FC<AttachmentsBlockProps> = ({ frame, connectorMode = 'none', onFrameSelect }) => {
  const { t } = useI18n()
  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={<Paperclip size={12} className="ai-step-icon" />}
      label={<span className="ai-step-label">{t('ai.step.attachments')} · {frame.files.length}</span>}
      connectorMode={connectorMode}
      expandable
      onMaximize={onFrameSelect ? () => onFrameSelect(frame) : undefined}
    >
      <div className="ai-attachments-list">
        {frame.files.map((file, i) => (
          <span key={i} className="ai-attachment-item">
            {file.name}
            {file.sizeBytes != null && <span className="ai-attachment-size">({(file.sizeBytes / 1024).toFixed(1)} KB)</span>}
          </span>
        ))}
      </div>
    </TimelineStep>
  )
}
