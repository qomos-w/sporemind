/**
 * Auto-scroll follow for a running terminal-tail box (ToolCodeBlock's
 * `.running-scroll` `<pre>`).
 *
 * When smoothing is enabled the follow glides via the same damped-spring lerp
 * as the outer stream (`computeFollowStep` in smoothFollow.ts) instead of
 * snapping `scrollTop` to the bottom on every content change. When smoothing
 * is off, or the user prefers reduced motion, the original instant snap
 * applies.
 *
 * The spring is growth-triggered, like useScrollLock's: the box's border-box is
 * clamped by its max-height once the content overflows, so a ResizeObserver
 * cannot see further scrollHeight growth (line wraps) — the content prop
 * change is the growth signal. The RAF stops once the scroll converges to the
 * tail and restarts on the next growth.
 */

import { useEffect, useRef } from 'react'
import { advanceScrollToTail } from '../../hooks/smoothFollow'
import { usePrefersReducedMotion } from './usePrefersReducedMotion'

export function useRunningScrollFollow(
  scrollRef: React.RefObject<HTMLElement | null>,
  /** Current text payload; each change is treated as a growth tick. */
  text: string,
  running: boolean,
  smooth: boolean,
): void {
  const reduced = usePrefersReducedMotion()
  const rafRef = useRef(0)
  const lastTsRef = useRef(0)

  const stopSpring = () => {
    if (rafRef.current) cancelAnimationFrame(rafRef.current)
    rafRef.current = 0
    lastTsRef.current = 0
  }

  // Idempotent: starts the glide RAF only when there is a real gap to the tail
  // and no glide is already in flight. The running tick reads the live
  // scrollHeight each frame so it keeps up with content that grows between
  // growth signals while the spring is active.
  const ensureSpring = () => {
    if (rafRef.current !== 0) return
    const el = scrollRef.current
    if (!el) return
    if (el.scrollHeight - el.clientHeight - el.scrollTop <= 0.5) return

    const tick = () => {
      const el = scrollRef.current
      if (!el) {
        stopSpring()
        return
      }
      const now = performance.now()
      if (lastTsRef.current === 0) {
        lastTsRef.current = now
        rafRef.current = requestAnimationFrame(tick)
        return
      }
      const dt = Math.min(50, now - lastTsRef.current)
      lastTsRef.current = now
      const target = el.scrollHeight - el.clientHeight
      const next = advanceScrollToTail(dt, el.scrollTop, target)
      if (next !== el.scrollTop) el.scrollTop = next
      if (next >= target - 0.5) {
        stopSpring()
        return
      }
      rafRef.current = requestAnimationFrame(tick)
    }

    rafRef.current = requestAnimationFrame(tick)
  }

  useEffect(() => {
    if (!running) {
      stopSpring()
      return
    }
    const el = scrollRef.current
    if (!el) return
    if (!smooth || reduced) {
      // Instant snap: original behavior when smoothing is off or reduced
      // motion is preferred.
      stopSpring()
      el.scrollTop = el.scrollHeight
      return
    }
    ensureSpring()
  }, [text, running, smooth, reduced])

  // Cancel any in-flight glide on unmount.
  useEffect(() => () => stopSpring(), [])
}
