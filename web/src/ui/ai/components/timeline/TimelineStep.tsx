import React, { useState } from 'react'
import { Maximize2 } from 'lucide-react'
import type { SlotStatus, TimelineConnectorMode } from '../../model/frame-types'
import { TimelineIconSlot, type SpinnerVariant } from './TimelineIconSlot'
import { useTimelineExpansionController, useDeferredMount } from './hooks'
import { useSmoothStreamContext } from '../../context/SmoothStreamContext'
import type { TimelineExpansionMode } from './types'

interface TimelineStepProps {
  slotId: string
  status: SlotStatus
  icon: React.ReactNode
  label: React.ReactNode
  /** Trailing content on the step row, right-aligned next to the label */
  headerExtras?: React.ReactNode
  /** If set, renders a Maximize2 button calling this callback. Mutually exclusive with headerExtras containing a maximize button. */
  onMaximize?: () => void
  /** Card mode: wraps content in ai-step-card layout (replaces TimelineCardStep). */
  card?: boolean
  connectorMode?: TimelineConnectorMode
  /** Bare mode: no step row, no icon/label, just content block */
  bare?: boolean
  /** Whether content area is expandable/collapsible */
  expandable?: boolean
  /** Expansion lifecycle policy */
  expansionMode?: TimelineExpansionMode
  /** Turn-level streaming seed for title-first unfold of fast tools */
  turnStreaming?: boolean
  durationSeconds?: number
  inputTokens?: number
  outputTokens?: number
  spinnerVariant?: SpinnerVariant
  /** 0..1 — drives petal spinner rotation/sweep rate and elastic extension */
  spinnerSpeed?: number
  children?: React.ReactNode
  /** Always-visible content below the step row, auto-wrapped with content-offset */
  alwaysVisible?: React.ReactNode
}

export const TimelineStep = React.memo<TimelineStepProps>(({
  slotId,
  status,
  icon,
  label,
  headerExtras,
  onMaximize,
  card = false,
  connectorMode = 'none',
  bare = false,
  expandable = false,
  expansionMode = 'manual',
  turnStreaming,
  spinnerVariant,
  spinnerSpeed,
  children,
  alwaysVisible,
}) => {
  const { isStreaming: contextStreaming, smoothStream } = useSmoothStreamContext()
  const effectiveTurnStreaming = turnStreaming ?? contextStreaming
  const [isHoveringTrigger, setIsHoveringTrigger] = useState(false)
  const { isOpen, iconPhase, toggle, isActive } = useTimelineExpansionController({
    status,
    expandable,
    expansionMode,
    turnStreaming: effectiveTurnStreaming,
    choreography: smoothStream,
  })
  const { mounted, renderedOpen } = useDeferredMount(isOpen)
  const childMounted = expandable ? mounted : true
  const cssOpen = expandable ? renderedOpen : isOpen

  const maximizeBtn = onMaximize ? (
    <button type="button" className="ai-frame-maximize-btn" title="View full details" onClick={onMaximize}>
      <Maximize2 size={12} />
    </button>
  ) : null

  const extras = maximizeBtn
    ? headerExtras
      ? <><div className="ai-step-header-extras" onClick={(e) => e.stopPropagation()}>{headerExtras}</div>{maximizeBtn}</>
      : maximizeBtn
    : headerExtras
      ? <div className="ai-step-header-extras" onClick={(e) => e.stopPropagation()}>{headerExtras}</div>
      : null

  if (bare) {
    return (
      <div className="ai-step ai-step-text-block" data-slot-id={slotId} data-status={status} data-icon-phase={iconPhase}>
        {alwaysVisible}
        {expandable && (
          <div className={`ai-collapsible ${cssOpen ? 'open' : ''}`}>
            <div className="ai-collapsible-inner">
              {childMounted && children}
            </div>
          </div>
        )}
      </div>
    )
  }

  if (card) {
    return (
      <div className="ai-step ai-card-step" data-slot-id={slotId} data-status={status} data-icon-phase={iconPhase}>
        {connectorMode === 'to-next-icon' && <div className="ai-step-line" />}

        <div
          className="ai-step-card-row"
          onMouseEnter={() => setIsHoveringTrigger(true)}
          onMouseLeave={() => setIsHoveringTrigger(false)}
        >
          <TimelineIconSlot
            status={status}
            icon={icon}
            expandable
            expanded={isOpen}
            iconPhase={iconPhase}
            suppressArrow={isActive || iconPhase !== 'idle'}
            forceHovered={isHoveringTrigger}
            onClick={toggle}
          />

          <div className="ai-step-card">
            <div className="ai-step-card-header" onClick={toggle}>
              {label}
              {extras}
            </div>
            <div className={`ai-collapsible ${cssOpen ? 'open' : ''}`}>
              <div className="ai-collapsible-inner">
                {childMounted && children}
              </div>
            </div>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="ai-step" data-slot-id={slotId} data-status={status} data-icon-phase={iconPhase}>
      {connectorMode === 'to-next-icon' && <div className="ai-step-line" />}

      <div
        className={`ai-step-row ${expandable ? 'expandable' : ''}`}
        onClick={toggle}
        onMouseEnter={() => setIsHoveringTrigger(true)}
        onMouseLeave={() => setIsHoveringTrigger(false)}
      >
        <TimelineIconSlot
          status={status}
          icon={icon}
          expandable={expandable}
          expanded={isOpen}
          iconPhase={iconPhase}
          suppressArrow={isActive || iconPhase !== 'idle'}
          forceHovered={isHoveringTrigger}
          spinnerVariant={spinnerVariant}
          spinnerSpeed={spinnerSpeed}
        />
        {label}
        {extras}
      </div>

      {alwaysVisible && (
        <div className="ai-step-content-offset">{alwaysVisible}</div>
      )}

      {expandable && (
        <div className={`ai-collapsible ${cssOpen ? 'open' : ''}`}>
          <div className="ai-collapsible-inner">
            {childMounted && children}
          </div>
        </div>
      )}
    </div>
  )
})
