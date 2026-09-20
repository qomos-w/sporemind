import { useMemo } from 'react'
import { useBrowserOverlay } from '../browserOverlay'
import type { Rect } from './placement'
import './overlay.css'

export interface SpotlightProps {
  /** Whether the spotlight is visible. */
  open: boolean
  /** Rect describing the highlighted target in viewport coordinates. */
  target: Rect
  /** Padding added around the target rect to form the ring. */
  holePadding?: number
  /** Corner radius of the ring. */
  holeRadius?: number
}

/**
 * A non-blocking highlight ring drawn around the target. The guide never
 * intercepts UI interaction: the ring is paint-only (pointer-events: none
 * everywhere), so the user can click the highlighted element — and anything
 * else — freely. The guide observes completion via interaction telemetry
 * (ExpectedInteraction gating), not by trapping clicks.
 *
 * The earlier full-screen dim backdrop was removed because (a) Chromium's
 * hit testing ignores SVG masks, so the masked backdrop swallowed clicks
 * even inside the visual hole, and (b) blocking the surrounding UI is the
 * wrong model for a guide that must drive real, unobstructed interaction.
 */
export function Spotlight({
  open,
  target,
  holePadding = 4,
  holeRadius = 6,
}: SpotlightProps) {
  useBrowserOverlay(open)
  const ring = useMemo(
    () => {
      const pad = Math.max(0, holePadding)
      const radius = Math.max(0, holeRadius)
      return {
        left: target.x - pad,
        top: target.y - pad,
        width: target.width + pad * 2,
        height: target.height + pad * 2,
        radius,
      }
    },
    [target, holePadding, holeRadius],
  )

  if (!open) {
    return null
  }

  return (
    <div
      className="spotlight-ring"
      aria-hidden="true"
      style={{
        position: 'fixed',
        left: `${ring.left}px`,
        top: `${ring.top}px`,
        width: `${ring.width}px`,
        height: `${ring.height}px`,
        borderRadius: `${ring.radius}px`,
        pointerEvents: 'none',
      }}
    />
  )
}
