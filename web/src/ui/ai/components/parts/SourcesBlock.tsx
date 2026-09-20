import React from 'react'
import { Search } from 'lucide-react'
import type { Frame, SourcesFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { TimelineStep } from '../timeline'

interface SourcesBlockProps {
  frame: SourcesFrame
  connectorMode?: TimelineConnectorMode
  onFrameSelect?: (frame: Frame) => void
}

export const SourcesBlock: React.FC<SourcesBlockProps> = ({ frame, connectorMode = 'none', onFrameSelect }) => {
  const { t } = useI18n()
  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={<Search size={12} className="ai-step-icon" />}
      label={<span className="ai-step-label">{t('ai.step.sources')} · {frame.entries.length}</span>}
      connectorMode={connectorMode}
      expandable
      onMaximize={onFrameSelect ? () => onFrameSelect(frame) : undefined}
    >
      <div className="ai-sources-list">
        {frame.entries.map((entry, i) => (
          <div key={i} className="ai-source-item">
            <div className="ai-source-title">
              {entry.url
                ? <a href={entry.url} target="_blank" rel="noopener noreferrer">{entry.title}</a>
                : entry.title}
            </div>
            {entry.snippet && <div className="ai-source-snippet">{entry.snippet}</div>}
          </div>
        ))}
      </div>
    </TimelineStep>
  )
}
