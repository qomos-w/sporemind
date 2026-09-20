import React from 'react'
import { BookOpen, AlertTriangle } from 'lucide-react'
import type { SkillUseFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { TimelineStep, truncateLabel } from '../timeline'

interface SkillUseBlockProps {
  frame: SkillUseFrame
  connectorMode?: TimelineConnectorMode
}

export const SkillUseBlock: React.FC<SkillUseBlockProps> = ({ frame, connectorMode = 'none' }) => {
  const { t } = useI18n()
  const isError = frame.isError === true || frame.status === 'error'
  const icon = isError
    ? <AlertTriangle size={12} className="ai-step-icon ai-step-icon-error" />
    : <BookOpen size={12} className="ai-step-icon" />
  const labelText = isError
    ? t('ai.step.skillUseFailed', { skillName: frame.skillName })
    : t('ai.step.skillUse', { skillName: frame.skillName })
  const label = (
    <span className="ai-step-label" title={labelText}>{truncateLabel(labelText)}</span>
  )
  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={icon}
      label={label}
      connectorMode={connectorMode}
    />
  )
}
