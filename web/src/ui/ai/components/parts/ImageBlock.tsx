import React, { useContext } from 'react'
import { Image as ImageIcon, Maximize2 } from 'lucide-react'
import type { Frame, ImageFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useI18n } from '../../../../i18n'
import { AIShellContext } from '../../context/AIShellContext'
import { TimelineStep } from '../timeline'
import { ImageViewer } from '../ImageViewer'

interface ImageBlockProps {
  frame: ImageFrame
  connectorMode?: TimelineConnectorMode
  onFrameSelect?: (frame: Frame) => void
}

export const ImageBlock: React.FC<ImageBlockProps> = ({ frame, connectorMode = 'none', onFrameSelect }) => {
  const { t } = useI18n()
  const shellCtx = useContext(AIShellContext)
  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={<ImageIcon size={12} className="ai-step-icon" />}
      label={<span className="ai-step-label">{t('ai.step.image')}</span>}
      connectorMode={connectorMode}
      alwaysVisible={
        <span className="ai-image-wrap">
          <ImageViewer src={frame.url} alt={frame.alt} inline />
          {shellCtx?.onOpenImage && (
            <button
              type="button"
              className="ai-image-open-btn"
              title={t('ai.imageViewer.openInViewer')}
              onClick={e => {
                e.stopPropagation()
                shellCtx.onOpenImage?.(frame.url, frame.alt)
              }}
            >
              <Maximize2 size={12} />
            </button>
          )}
        </span>
      }
      onMaximize={onFrameSelect ? () => onFrameSelect(frame) : undefined}
    />
  )
}
