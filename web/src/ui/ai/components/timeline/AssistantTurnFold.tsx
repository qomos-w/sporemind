import React from 'react'
import { Layers } from 'lucide-react'
import type { Frame, TurnEnvelope, SlotEntry } from '../../model/frame-types'
import { TimelineStep, connectorModeForFrame, formatDurationBadge } from './'
import { FrameRenderer } from '../parts/FrameRenderer'
import { ErrorBoundary } from '../ErrorBoundary'
import { buildAssistantTurnSummary } from '../TurnTail'
import { useI18n } from '../../../../i18n/provider'

interface AssistantTurnFoldProps {
  envelope: TurnEnvelope
  frames: Frame[]
  slots: Map<string, SlotEntry>
  onFrameSelect?: (frame: Frame) => void
  onSaveToCard?: (content: string) => void
  agentActorId?: string
  turnStreaming?: boolean
}

export const AssistantTurnFold: React.FC<AssistantTurnFoldProps> = React.memo(({
  envelope,
  frames,
  slots,
  onFrameSelect,
  onSaveToCard,
  agentActorId,
  turnStreaming,
}) => {
  const { t } = useI18n()
  const turnState = envelope.metadata?.turnState ?? 'completed'
  const summary = buildAssistantTurnSummary(envelope, true, turnState, t)

  const parts: string[] = []
  if (summary.actionCount > 0) {
    parts.push(`${summary.actionCount} ${t(summary.actionCount === 1 ? 'ai.turn.action' : 'ai.turn.actions')}`)
  }
  if (summary.failActionCount > 0) {
    parts.push(`${summary.failActionCount} ${t('ai.turn.failed')}`)
  }
  if (summary.elapsedSeconds != null) {
    parts.push(formatDurationBadge(summary.elapsedSeconds))
  }
  const labelText = parts.length > 0 ? parts.join(' · ') : t('ai.turn.details')

  return (
    <div className="ai-turn-fold" data-envelope-id={envelope.id}>
      <TimelineStep
        slotId={`${envelope.id}-fold`}
        status="completed"
        icon={<Layers size={12} className="ai-step-icon" />}
        label={<span className="ai-step-label ai-turn-fold-label">{labelText}</span>}
        connectorMode="none"
        expandable
        expansionMode="manual"
      >
        <div className="ai-assistant-turn">
          {frames.map((frame, i) => (
            <ErrorBoundary key={frame.id}>
              <FrameRenderer
                frame={frame}
                version={slots.get(frame.id)?.version ?? 1}
                connectorMode={connectorModeForFrame(frames, i)}
                onFrameSelect={onFrameSelect}
                onSaveToCard={onSaveToCard}
                agentActorId={agentActorId}
                turnStreaming={turnStreaming}
              />
            </ErrorBoundary>
          ))}
        </div>
      </TimelineStep>
    </div>
  )
})
