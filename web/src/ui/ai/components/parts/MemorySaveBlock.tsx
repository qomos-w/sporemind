import React from 'react'
import { Brain, AlertTriangle } from 'lucide-react'
import type { MemorySaveFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { TimelineStep } from '../timeline'

interface MemorySaveBlockProps {
  frame: MemorySaveFrame
  connectorMode?: TimelineConnectorMode
}

export const MemorySaveBlock: React.FC<MemorySaveBlockProps> = ({ frame, connectorMode = 'none' }) => {
  const { t } = useI18n()
  const isError = frame.isError === true || frame.status === 'error'
  const icon = isError
    ? <AlertTriangle size={12} className="ai-step-icon ai-step-icon-error" />
    : <Brain size={12} className="ai-step-icon" />
  const label = isError
    ? <span className="ai-step-label">{t('ai.step.memorySaveFailed')}</span>
    : <span className="ai-step-label">{t('ai.step.memorySave')}</span>
  // Show a short preview of what was remembered, right-aligned on the step row.
  const preview = frame.content ? (frame.content.length > 60 ? frame.content.slice(0, 57) + '…' : frame.content) : ''
  const headerExtras = preview
    ? <span className="ai-step-memory-preview">{preview}</span>
    : undefined
  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={icon}
      label={label}
      headerExtras={headerExtras}
      connectorMode={connectorMode}
    />
  )
}
