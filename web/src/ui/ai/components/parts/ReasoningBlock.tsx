import React from 'react'
import { Atom } from 'lucide-react'
import type { Frame, ReasoningFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { TimelineStep } from '../timeline'
import { useI18n } from '../../../../i18n'
import { useSmoothText } from './useSmoothStreamContent.ts'
import { useSmoothStreamContext, useUiExpansionMode } from '../../context/SmoothStreamContext'

/** Cap the boundary scan so a long single-sentence reasoning block doesn't
 *  make the preview search walk the whole (growing) string every tick. The
 *  preview is truncated to 40 chars anyway, so anything past the cap can
 *  never change the output. */
const PREVIEW_SCAN_LIMIT = 512

function extractReasoningPreview(content: string): string {
  const trimmed = content.trimStart().slice(0, PREVIEW_SCAN_LIMIT)
  if (!trimmed) return ''
  const lineBreak = trimmed.indexOf('\n')
  const cnPeriod = trimmed.indexOf('。')
  let enPeriod = -1
  for (let i = 1; i < trimmed.length; i++) {
    if (trimmed[i] === '.' && /[a-zA-Z\u4e00-\u9fa5]/.test(trimmed.charAt(i - 1))) {
      enPeriod = i
      break
    }
  }
  let end = lineBreak
  if (cnPeriod >= 0 && (end < 0 || cnPeriod < end)) {
    end = cnPeriod + 1
  }
  if (enPeriod >= 0 && (end < 0 || enPeriod < end)) {
    end = enPeriod + 1
  }
  const preview = end >= 0 ? trimmed.slice(0, end) : trimmed
  if (preview.length > 40) {
    return preview.slice(0, 40) + '...'
  }
  return preview
}

interface ReasoningBlockProps {
  frame: ReasoningFrame
  connectorMode?: TimelineConnectorMode
  onFrameSelect?: (frame: Frame) => void
  turnStreaming?: boolean
}

export const ReasoningBlock: React.FC<ReasoningBlockProps> = ({ frame, connectorMode = 'none', onFrameSelect, turnStreaming }) => {
  const { t } = useI18n()
  const isStreaming = frame.status === 'running'
  const { smoothStream, isStreaming: contextStreaming } = useSmoothStreamContext()
  const expansionMode = useUiExpansionMode('reasoning')

  // Smooth reveal: buffer streaming reasoning text and reveal at a rate that
  // tracks arrival. When not streaming, returns full content unchanged.
  const { displayed: smoothedContent, guardRef: smoothGuardRef } = useSmoothText(frame.content, isStreaming && smoothStream)

  const preview = extractReasoningPreview(smoothedContent)

  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={<Atom size={12} className="ai-step-icon" />}
      label={<span className="ai-step-label">{t('ai.step.reasoning')}{preview && <span className="ai-reasoning-label-preview">{preview}</span>}</span>}
      connectorMode={connectorMode}
      expandable
      expansionMode={expansionMode}
      turnStreaming={turnStreaming ?? contextStreaming}
      onMaximize={onFrameSelect ? () => onFrameSelect(frame) : undefined}
    >
      <div ref={smoothGuardRef} className={`ai-reasoning-content ${isStreaming ? 'streaming' : ''}`}>
        {smoothedContent}
      </div>
    </TimelineStep>
  )
}
