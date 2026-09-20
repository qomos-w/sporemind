/**
 * Stream-smoothing reveal hook.
 *
 * Buffers the model's chunked text and reveals it at a cadence that tracks
 * the observed arrival rate, so a long reply never dumps whole paragraphs at
 * once and a fast stream never stutters. Port of dsh-smooth-stream's
 * smoother: queue-based reveal (backlog / 8 per frame), backlog pressure, and
 * a flush-speed settle drain once the input idles.
 *
 * `shouldHoldBack` is the performance guard's veto: while it returns true the
 * loop keeps measuring but skips the DOM commit, so an offscreen reply never
 * competes with visible frames when the frame rate is degraded.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import { useFpsGuard } from './useFpsGuard'
import { usePrefersReducedMotion } from './usePrefersReducedMotion'

const MAX_COMMIT_INTERVAL_MS = 16
/** Each frame reveals about `backlog / 8` characters. */
export const QUEUE_REVEAL_DIVISOR = 8
const QUEUE_FRAME_MS = 16.67

export const clamp = (value: number, min: number, max: number): number =>
  Math.min(max, Math.max(min, value))

/** Counts user-perceived characters (code points), not UTF-16 units. */
export const countChars = (text: string): number => {
  let count = 0
  for (const _ of text) count += 1
  return count
}

/**
 * Per-frame reveal: drain `backlog / 8` characters, at least 1, scaled by
 * frame time. A small queue types one glyph per frame; a burst raises the
 * step so the display does not fall behind.
 */
export function computeQueueReveal(backlog: number, dtMs: number): number {
  if (backlog <= 0 || dtMs <= 0) return 0
  return Math.min(backlog, Math.max(1, Math.ceil((backlog / QUEUE_REVEAL_DIVISOR) * (dtMs / QUEUE_FRAME_MS))))
}

export interface UseSmoothStreamContentOptions {
  enabled?: boolean
  /** Performance guard veto: while true, reveal commits are held back. */
  shouldHoldBack?: (() => boolean) | undefined
  /** Written each commit with the live reveal-rate EMA for scroll-follow. */
  speedCpsRef?: { current: number } | undefined
}

/**
 * Smooth a chunked content stream into a reveal-paced display string.
 *
 * @param content - The full accumulated input so far.
 * @returns The displayed content, revealed at the smoothed cadence.
 */
export function useSmoothStreamContent(
  content: string,
  { enabled = true, shouldHoldBack, speedCpsRef }: UseSmoothStreamContentOptions = {},
): string {
  const [displayedContent, setDisplayedContent] = useState(content)

  const displayedContentRef = useRef(content)
  const displayedCountRef = useRef(countChars(content))
  const targetContentRef = useRef(content)
  const targetCharsRef = useRef([...content])
  const targetCountRef = useRef(countChars(content))

  const rafRef = useRef<number | null>(null)
  const lastFrameTsRef = useRef<number | null>(null)
  const holdBackRef = useRef(shouldHoldBack)
  const wasHoldingRef = useRef(false)
  const speedOutRef = useRef(speedCpsRef)
  speedOutRef.current = speedCpsRef

  useEffect(() => {
    holdBackRef.current = shouldHoldBack
  }, [shouldHoldBack])

  const stopFrameLoop = useCallback(() => {
    if (rafRef.current !== null) {
      cancelAnimationFrame(rafRef.current)
      rafRef.current = null
    }
    lastFrameTsRef.current = null
  }, [])

  const syncImmediate = useCallback((nextContent: string) => {
    stopFrameLoop()
    const chars = [...nextContent]
    targetContentRef.current = nextContent
    targetCharsRef.current = chars
    targetCountRef.current = chars.length
    displayedContentRef.current = nextContent
    displayedCountRef.current = chars.length
    setDisplayedContent(nextContent)
  }, [stopFrameLoop])

  const startFrameLoop = useCallback(() => {
    if (rafRef.current !== null) return

    const tick = () => {
      const targetCount = targetCountRef.current
      const displayedCount = displayedCountRef.current
      const backlog = targetCount - displayedCount

      if (backlog <= 0) {
        stopFrameLoop()
        return
      }

      const now = performance.now()
      if (lastFrameTsRef.current === null) {
        lastFrameTsRef.current = now
        rafRef.current = requestAnimationFrame(tick)
        return
      }

      const frameIntervalMs = Math.max(0, now - lastFrameTsRef.current)
      if (frameIntervalMs < MAX_COMMIT_INTERVAL_MS) {
        rafRef.current = requestAnimationFrame(tick)
        return
      }

      lastFrameTsRef.current = now
      const dtMs = frameIntervalMs

      let revealChars = computeQueueReveal(backlog, dtMs)

      // Performance guard: while degraded and the reply is offscreen, skip
      // the DOM commit — the backlog keeps accumulating and flushes when the
      // guard clears or the reply scrolls into view.
      const holding = holdBackRef.current?.() === true
      if (holding !== wasHoldingRef.current) {
        wasHoldingRef.current = holding
        if (!holding && backlog > 200) {
          console.info(`[jitter-diag] holdback-release backlog=${backlog}`)
        }
      }
      if (holding) {
        rafRef.current = requestAnimationFrame(tick)
        return
      }

      const speedOut = speedOutRef.current
      if (speedOut !== undefined && dtMs > 0) {
        const instantCps = (revealChars * 1000) / dtMs
        speedOut.current = speedOut.current * 0.92 + instantCps * 0.08
      }

      const nextCount = displayedCount + revealChars
      const segment = targetCharsRef.current.slice(displayedCount, nextCount).join('')
      if (segment) {
        const nextDisplayed = displayedContentRef.current + segment
        displayedContentRef.current = nextDisplayed
        displayedCountRef.current = nextCount
        setDisplayedContent(nextDisplayed)
      } else {
        displayedContentRef.current = targetContentRef.current
        displayedCountRef.current = targetCount
        setDisplayedContent(targetContentRef.current)
      }

      rafRef.current = requestAnimationFrame(tick)
    }

    rafRef.current = requestAnimationFrame(tick)
  }, [stopFrameLoop])

  useEffect(() => {
    if (!enabled) {
      syncImmediate(content)
      return
    }

    const prevTargetContent = targetContentRef.current
    if (content === prevTargetContent) return

    const appendOnly = content.startsWith(prevTargetContent)
    if (!appendOnly) {
      let common = 0
      const cap = Math.min(prevTargetContent.length, content.length, 4096)
      while (common < cap && prevTargetContent[common] === content[common]) common += 1
      console.info(`[jitter-diag] content-replace prevLen=${prevTargetContent.length} newLen=${content.length} common=${common}`)
      syncImmediate(content)
      return
    }

    const appended = content.slice(prevTargetContent.length)
    const appendedChars = [...appended]
    targetContentRef.current = content
    targetCharsRef.current.push(...appendedChars)
    targetCountRef.current += appendedChars.length

    startFrameLoop()
  }, [content, enabled, startFrameLoop, syncImmediate])

  useEffect(() => {
    return () => stopFrameLoop()
  }, [stopFrameLoop])

  return displayedContent
}

/**
 * One-call convenience: wires the FPS guard, reduced-motion, and smoother
 * together for a streaming text source. Returns `{ displayed, guardRef }`.
 *
 * Settle/drain: when `streaming` turns false, the smoother keeps running
 * until the displayed content catches up to the full text, then settles.
 * This prevents the remaining buffered text from dumping all at once when
 * the stream closes. Ported from dsh-smooth-stream's AnimatedMarkdownText.
 */
export function useSmoothText(
  content: string,
  streaming: boolean,
): { displayed: string; guardRef: (el: HTMLElement | null) => void } {
  const reduced = usePrefersReducedMotion()
  const [typing, setTyping] = useState(streaming)
  const { ref: guardRef, shouldHoldBack } = useFpsGuard(streaming)
  const displayed = useSmoothStreamContent(content, {
    enabled: typing && !reduced,
    shouldHoldBack,
  })
  const shown = reduced ? content : displayed
  const live = typing && !reduced

  useEffect(() => {
    if (typing && !streaming && shown.length >= content.length) setTyping(false)
  }, [shown, streaming, content, typing])

  return { displayed: live ? shown : content, guardRef }
}
