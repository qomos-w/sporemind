import React, { useState, useRef, useEffect, useCallback } from 'react'
import type { SlotStatus } from '../../model/frame-types'
import { ICON_ARROW_ANIMATION_MS, type IconPhase, type ArrowVisualState } from './types'
import { PetalSpectrumSpinner } from './PetalSpectrumSpinner'

export type SpinnerVariant = 'circle' | 'petal'

interface TimelineIconSlotProps {
  status: SlotStatus
  icon: React.ReactNode
  expandable?: boolean
  expanded?: boolean
  iconPhase?: IconPhase
  suppressArrow?: boolean
  forceHovered?: boolean
  spinnerVariant?: SpinnerVariant
  spinnerSpeed?: number
  onClick?: React.MouseEventHandler<HTMLDivElement>
}

export const TimelineIconSlot: React.FC<TimelineIconSlotProps> = ({
  status,
  icon,
  expandable = false,
  expanded = false,
  iconPhase = 'idle',
  suppressArrow = false,
  forceHovered = false,
  spinnerVariant = 'circle',
  spinnerSpeed = 0,
  onClick,
}) => {
  const [hovered, setHovered] = useState(false)
  const [arrowState, setArrowState] = useState<ArrowVisualState>('icon')
  const hoveredRef = useRef(false)
  const prevExpandedRef = useRef(expanded)
  const arrowTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const arrowEnabled = expandable && !suppressArrow
  const effectiveHovered = hovered || forceHovered
  const isBusy = status === 'running' || status === 'pending' || iconPhase === 'exiting' || iconPhase === 'spinning'

  useEffect(() => {
    if (arrowTimerRef.current) {
      clearTimeout(arrowTimerRef.current)
      arrowTimerRef.current = null
    }

    return () => {
      if (arrowTimerRef.current) {
        clearTimeout(arrowTimerRef.current)
        arrowTimerRef.current = null
      }
    }
  }, [])

  useEffect(() => {
    if (!arrowEnabled) return

    const wasExpanded = prevExpandedRef.current
    prevExpandedRef.current = expanded

    if (isBusy) {
      if (arrowTimerRef.current) {
        clearTimeout(arrowTimerRef.current)
        arrowTimerRef.current = null
      }
      setArrowState('icon')
      return
    }

    // Resting state keeps the tool icon visible; the chevron is a hover-only
    // affordance (right when collapsed, down when expanded). An expanded step
    // must not keep the icon hidden behind the chevron.
    const resting = effectiveHovered ? (expanded ? 'down' : 'right') : 'icon'

    if (wasExpanded !== expanded && effectiveHovered) {
      if (arrowTimerRef.current) {
        clearTimeout(arrowTimerRef.current)
      }
      setArrowState(expanded ? 'to-down' : 'to-right')
      arrowTimerRef.current = setTimeout(() => {
        setArrowState(expanded ? 'down' : 'right')
        arrowTimerRef.current = null
      }, ICON_ARROW_ANIMATION_MS)
      return
    }

    setArrowState(resting)
  }, [arrowEnabled, expanded, isBusy, effectiveHovered])

  const handleMouseEnter = useCallback(() => {
    hoveredRef.current = true
    setHovered(true)
    if (!arrowEnabled || isBusy) return
    if (arrowTimerRef.current) return
    setArrowState(expanded ? 'down' : 'right')
  }, [arrowEnabled, expanded, isBusy])

  const handleMouseLeave = useCallback(() => {
    hoveredRef.current = false
    setHovered(false)
    if (!arrowEnabled || isBusy || forceHovered) return
    if (arrowTimerRef.current) return
    setArrowState('icon')
  }, [arrowEnabled, isBusy, forceHovered])

  useEffect(() => {
    hoveredRef.current = effectiveHovered
  }, [effectiveHovered])

  const showSpinner = status === 'running' || status === 'pending' || iconPhase === 'spinning' || iconPhase === 'exiting'
  const showCurrentIcon = iconPhase !== 'spinning' && iconPhase !== 'exiting' && iconPhase !== 'arrow' && status !== 'running' && status !== 'pending'
  const showForcedArrow = iconPhase === 'arrow'
  const showInteractiveArrow = arrowEnabled && iconPhase === 'idle'

  return (
    <div
      className="ai-step-icon-slot"
      data-expandable={arrowEnabled || undefined}
      data-expanded={expanded || undefined}
      data-hovered={hovered || undefined}
      data-arrow-state={arrowEnabled ? arrowState : undefined}
      onClick={onClick}
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
    >
      <span className="ai-step-icon-current" aria-hidden={arrowEnabled || undefined}>
        {showSpinner && spinnerVariant === 'petal' && (
          <span className={`ai-step-icon ai-step-icon-petal ${status === 'pending' ? 'ai-step-icon-petal-pending' : ''} ${iconPhase === 'exiting' ? 'ai-step-icon-exit' : ''}`}>
            <PetalSpectrumSpinner speed={spinnerSpeed} />
          </span>
        )}
        {showSpinner && spinnerVariant !== 'petal' && (
          <span className={`ai-step-icon ai-step-icon-spin ${status === 'pending' ? 'ai-step-icon-spin-pending' : ''} ${iconPhase === 'exiting' ? 'ai-step-icon-exit' : ''}`} />
        )}
        {showCurrentIcon && (
          <span className={`ai-step-icon ${iconPhase === 'entering' ? 'ai-step-icon-enter' : ''}`}>{icon}</span>
        )}
      </span>
      {(showInteractiveArrow || showForcedArrow) && (
        <>
          <span className="ai-step-icon-arrow ai-step-icon-arrow-right" aria-hidden>
            <span className="ai-step-icon-arrow-shape" />
          </span>
          <span className={`ai-step-icon-arrow ai-step-icon-arrow-down ${showForcedArrow ? 'ai-step-icon-arrow-forced' : ''}`} aria-hidden>
            <span className="ai-step-icon-arrow-shape" />
          </span>
        </>
      )}
    </div>
  )
}
