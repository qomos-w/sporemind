import React from 'react'
import { Shield, CheckCircle, Ban } from 'lucide-react'
import type { PermissionRequestFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useStepInteraction } from '../../hooks/useStepInteraction'
import { useI18n } from '../../../../i18n'
import { TimelineStep } from '../timeline'

interface PermissionRequestBlockProps {
  frame: PermissionRequestFrame
  connectorMode?: TimelineConnectorMode
}

export const PermissionRequestBlock: React.FC<PermissionRequestBlockProps> = ({
  frame,
  connectorMode = 'none',
}) => {
  const { t } = useI18n()
  const { submitPermission } = useStepInteraction(frame.requestId ?? '')
  const isComplete = frame.status === 'completed' || frame.allowed != null
  const isDenied = isComplete && !frame.allowed

  const icon = isComplete
    ? isDenied
      ? <Ban size={12} className="ai-step-icon ai-step-icon-error" />
      : <CheckCircle size={12} className="ai-step-icon ai-step-icon-accent" />
    : <Shield size={12} className="ai-step-icon" />

  const label = (
    <span className="ai-step-label">
      {isComplete ? (isDenied ? t('ai.step.permissionDenied') : t('ai.step.permissionGranted')) : t('ai.step.permissionRequired')}
    </span>
  )

  const handle = (allowed: boolean, allowInProject?: boolean) => {
    if (frame.requestId) submitPermission(allowed, allowInProject)
  }

  return (
    <TimelineStep
      slotId={frame.id}
      status={isComplete ? (isDenied ? 'error' : 'completed') : frame.status}
      icon={icon}
      label={label}
      connectorMode={connectorMode}
      headerExtras={!isComplete ? (
        <div className="ai-perm-actions">
          <button className="ai-perm-deny" onClick={() => handle(false)}>{t('ai.permission.deny')}</button>
          <button className="ai-perm-allow-in-project" onClick={() => handle(true, true)}>{t('ai.permission.allowInProject')}</button>
          <button className="ai-perm-allow" onClick={() => handle(true)}>{t('ai.permission.allow')}</button>
        </div>
      ) : undefined}
    />
  )
}
