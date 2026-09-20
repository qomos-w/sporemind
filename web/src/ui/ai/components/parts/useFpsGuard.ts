import { useCallback, useEffect, useRef } from 'react'

const FPS_THRESHOLD = 30
const FPS_ALPHA = 0.12
const RECOVER_FRAMES = 6
const MAX_FRAME_MS = 100

interface MutableFps {
  emaMs: number
  lastMs: number
  healthyRun: number
  degraded: boolean
}

/**
 * Monitors frame rate and element visibility while streaming. Returns a
 * `shouldHoldBack` predicate that is true only when FPS is below 30 AND the
 * reply is offscreen — the spec's "skip offscreen DOM updates when FPS < 30"
 * rule. The smoother consumes it as a commit veto.
 */
export function useFpsGuard(active: boolean): {
  ref: (element: HTMLElement | null) => void
  shouldHoldBack: () => boolean
} {
  const fpsRef = useRef<MutableFps>({ emaMs: 0, lastMs: 0, healthyRun: 0, degraded: false })
  const visibleRef = useRef(true)
  const elementRef = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!active) return
    let rafId = 0
    const frame = (now: number) => {
      rafId = requestAnimationFrame(frame)
      const fps = fpsRef.current
      if (fps.lastMs === 0) {
        fps.lastMs = now
        return
      }
      const delta = Math.min(MAX_FRAME_MS, Math.max(1, now - fps.lastMs))
      fps.lastMs = now
      fps.emaMs = fps.emaMs === 0 ? delta : fps.emaMs + FPS_ALPHA * (delta - fps.emaMs)
      const currentFps = 1000 / fps.emaMs
      if (currentFps < FPS_THRESHOLD) {
        fps.healthyRun = 0
        fps.degraded = true
      } else if (fps.degraded) {
        fps.healthyRun += 1
        if (fps.healthyRun >= RECOVER_FRAMES) fps.degraded = false
      }
    }
    rafId = requestAnimationFrame(frame)
    return () => {
      cancelAnimationFrame(rafId)
      fpsRef.current = { emaMs: 0, lastMs: 0, healthyRun: 0, degraded: false }
    }
  }, [active])

  const ref = useCallback((element: HTMLElement | null) => {
    elementRef.current = element
  }, [])

  useEffect(() => {
    const element = elementRef.current
    if (element === null || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) visibleRef.current = entry.isIntersecting
      },
      { rootMargin: '120px 0px' },
    )
    observer.observe(element)
    return () => observer.disconnect()
  })

  const shouldHoldBack = useCallback(() => {
    return active && fpsRef.current.degraded && !visibleRef.current
  }, [active])

  return { ref, shouldHoldBack }
}
