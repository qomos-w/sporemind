import React, { useState } from 'react'
import { Maximize2 } from 'lucide-react'
import type { TimelineConnectorMode, ToolFrame } from '../../model/frame-types.ts'
import { useSmoothStreamContext, useUiExpansionMode } from '../../context/SmoothStreamContext.ts'
import { TimelineIconSlot } from '../timeline/TimelineIconSlot.tsx'
import { useTimelineExpansionController, useDeferredMount } from '../timeline/hooks.ts'
import { TOOL_TITLE_DELAY_MS } from '../timeline/types.ts'

interface ToolFoldCardProps {
  frame: ToolFrame
  /** Semantic UI kind driving the configured expansion action. */
  uiKind: string
  icon: React.ReactNode
  /** Title-row label content (tool title + subtitles + badges). */
  label: React.ReactNode
  /** Optional trailing content on the title row (right side). */
  headerExtras?: React.ReactNode
  connectorMode?: TimelineConnectorMode
  onMaximize?: () => void
  /** Class stamped on the outermost step div (kind-specific styling). */
  stepClassName?: string
  /** Card layout: wraps title row + body in a bordered card. */
  card?: boolean
  /** Extra wrapper className (combined with card base). */
  wrapperClassName?: string
  /** Per-envelope streaming state. When false, completed frames mount at
   *  their terminal state without replaying the expansion/icon animation. */
  turnStreaming?: boolean
  children?: React.ReactNode
}

/**
 * Shared Edit-card-pattern fold container for tool steps.
 *
 * The expansion state lives on THIS component (stable across status flips:
 * pending -> running -> completed), the collapsible region is ours, and the
 * title row is a plain hand-rolled row — no TimelineStep in between. This is
 * the single choreography entry point for every tool view: title-first delay,
 * four-state configurable unfold, auto-collapse, manual toggle, history
 * terminal-state mount.
 */
export const ToolFoldCard: React.FC<ToolFoldCardProps> = ({
  frame,
  uiKind,
  icon,
  label,
  headerExtras,
  connectorMode,
  onMaximize,
  stepClassName,
  card,
  wrapperClassName,
  turnStreaming,
  children,
}) => {
  const [isHoveringTrigger, setIsHoveringTrigger] = useState(false)
  const hasBody = frame.status !== 'pending'
  const { isStreaming: contextStreaming, smoothStream } = useSmoothStreamContext()
  const effectiveTurnStreaming = turnStreaming ?? contextStreaming
  const expansionMode = useUiExpansionMode(uiKind)

  const { isOpen: bodyOpen, toggle, iconPhase, isActive, titleFirst } = useTimelineExpansionController({
    status: frame.status,
    expandable: hasBody,
    expansionMode,
    turnStreaming: effectiveTurnStreaming,
    choreography: smoothStream,
  })
  const { mounted, renderedOpen } = useDeferredMount(bodyOpen)

  const inner = (
    <>
      <div
        className={`ai-step-row ${hasBody ? 'expandable' : ''}`}
        onClick={hasBody ? toggle : undefined}
        onMouseEnter={() => setIsHoveringTrigger(true)}
        onMouseLeave={() => setIsHoveringTrigger(false)}
      >
        <TimelineIconSlot
          status={frame.status}
          icon={icon}
          expandable={hasBody}
          expanded={bodyOpen}
          iconPhase={iconPhase}
          suppressArrow={isActive || iconPhase !== 'idle'}
          forceHovered={isHoveringTrigger}
        />
        {label}
        {headerExtras}
        {onMaximize && (
          <button
            type="button"
            className="ai-frame-maximize-btn"
            title="View full details"
            onClick={(e) => { e.stopPropagation(); onMaximize() }}
          >
            <Maximize2 size={12} />
          </button>
        )}
      </div>
      <div
        className={`ai-collapsible ${renderedOpen ? 'open' : ''} ${titleFirst ? 'title-first' : ''}`}
        style={{ '--ai-title-delay': `${TOOL_TITLE_DELAY_MS}ms` } as React.CSSProperties}
      >
        <div className="ai-collapsible-inner">
          {mounted && hasBody && children}
        </div>
      </div>
    </>
  )

  if (card) {
    return (
      <div className={`ai-step ${stepClassName ?? 'ai-step-card-step'} ai-card-step`} data-slot-id={frame.id} data-status={frame.status} data-icon-phase={iconPhase}>
        {connectorMode === 'to-next-icon' && <div className="ai-step-line" />}
        <div className="ai-step-card-row">
          <div className={`ai-step-card ${wrapperClassName ?? ''}`}>{inner}</div>
        </div>
      </div>
    )
  }

  if (wrapperClassName) {
    return (
      <div className={`ai-step ${stepClassName ?? ''}`} data-slot-id={frame.id} data-status={frame.status} data-icon-phase={iconPhase}>
        {connectorMode === 'to-next-icon' && <div className="ai-step-line" />}
        <div className={`ai-card-base ${wrapperClassName}`}>{inner}</div>
      </div>
    )
  }

  return (
    <div className={`ai-step ${stepClassName ?? 'ai-step-tool'} ${wrapperClassName ?? ''}`} data-slot-id={frame.id} data-status={frame.status} data-icon-phase={iconPhase}>
      {connectorMode === 'to-next-icon' && <div className="ai-step-line" />}
      {inner}
    </div>
  )
}
