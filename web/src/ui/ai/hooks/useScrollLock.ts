import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { computeFollowStep, resolveAutoScrollBehavior, wasFollowingTail, capSmoothToAuto, classifyStreamGrowth, pickMidViewAnchorTop, isAtScrollFloor, isIdleTailGrowth, MIDVIEW_COMPENSATE_MIN_PX, SMOOTH_GROWTH_DISTANCE_CAP_MIN_PX, SMOOTH_GROWTH_DISTANCE_CAP_RATIO, SMOOTH_SUPPRESS_BUFFER_MS, type AnchorChildGeometry } from './smoothFollow'

/** After a scope change (agent switch / initial load), growth inside this
 *  window is treated as bulk history fill: snap to the final position with
 *  no smooth-follow glide. */
const BULK_FILL_SMOOTH_SUPPRESS_MS = 800

const BOTTOM_TOLERANCE_PX = 80
const MAX_BOTTOM_TOLERANCE_PX = 220
const MOBILE_BOTTOM_TOLERANCE_RATIO = 0.25
const PROGRAMMATIC_SCROLL_GUARD_MS = 100
const MOBILE_PROGRAMMATIC_SCROLL_GUARD_MS = 450
const SMOOTH_SCROLL_GUARD_MS = 500

function isMobileLike(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false
  try {
    return window.matchMedia('(pointer: coarse)').matches
  } catch {
    return false
  }
}

function getBottomTolerance(el: HTMLElement): number {
  if (!isMobileLike()) return BOTTOM_TOLERANCE_PX
  const proportional = Math.floor(el.clientHeight * MOBILE_BOTTOM_TOLERANCE_RATIO)
  return Math.max(BOTTOM_TOLERANCE_PX, Math.min(MAX_BOTTOM_TOLERANCE_PX, proportional))
}

function getProgrammaticScrollGuardMs(behavior: ScrollBehavior): number {
  if (behavior === 'smooth') return SMOOTH_SCROLL_GUARD_MS
  return isMobileLike() ? MOBILE_PROGRAMMATIC_SCROLL_GUARD_MS : PROGRAMMATIC_SCROLL_GUARD_MS
}

/** Reads the --ai-expand-ms CSS custom property (set on the layout wrapper)
 *  to learn how long the TurnTail grid-rows expansion animates. Returns 0 when
 *  smooth-stream is off (the variable is '0ms') or unavailable. */
function readExpandMs(el: HTMLElement | null): number {
  if (!el || typeof window === 'undefined' || typeof getComputedStyle !== 'function') return 0
  const raw = getComputedStyle(el).getPropertyValue('--ai-expand-ms').trim()
  const ms = parseFloat(raw)
  return Number.isFinite(ms) && ms > 0 ? ms : 0
}

interface ScrollAnchor {
  elementId: string
  /** Pixels from the element's offsetTop to the stream's scrollTop. */
  delta: number
}

export interface UseScrollLockResult {
  /** Whether the stream is scrolled away from the bottom. */
  canScrollDown: boolean
  /** Scroll to the bottom of the stream. Also clears userScrolledUp. */
  scrollToBottom: (behavior?: ScrollBehavior) => void
  /** Save the first visible envelope element as a scroll anchor for later
   *  restoration (e.g. before inserting content above the viewport). */
  saveScrollAnchor: () => void
  /** Restore scroll position to the saved anchor. No-op if no anchor saved. */
  restoreScrollAnchor: () => void
}

/**
 * Manages auto-scroll/lock behavior for the AI chat message stream.
 *
 * The core model is based on user intent: if the user has manually scrolled
 * away from the bottom, we stop forcing the stream down while streaming.
 * Auto-scroll resumes when the user returns to the bottom or clicks the
 * scroll-to-bottom button.
 */
export function useScrollLock(
  streamRef: React.RefObject<HTMLElement | null>,
  isStreaming: boolean,
  contentRef?: React.RefObject<HTMLElement | null>,
  scopeKey?: string | null,
  smoothStream: boolean = true,
): UseScrollLockResult {
  const [canScrollDown, setCanScrollDown] = useState(false)
  const [userScrolledUp, setUserScrolledUp] = useState(false)

  // Mutable refs are used inside event listeners/observers so we never read
  // stale closures. State is mirrored for React renders.
  const userScrolledUpRef = useRef(userScrolledUp)
  const isSelectingRef = useRef(false)
  const isPointerDownRef = useRef(false)
  const isTouchingRef = useRef(false)
  const programmaticScrollUntilRef = useRef(0)
  const lastScrollHeightRef = useRef(0)
  /** Content-relative top of the content column's last child, plus its child
   * count, tracked across ResizeObserver ticks to classify where growth
   * happened (tail vs above-tail) — see classifyStreamGrowth. */
  const lastTailTopRef = useRef<number | null>(null)
  const lastChildCountRef = useRef<number | null>(null)
  /** Mid-view scroll anchor (content-relative top of the element at the fold,
   * see pickMidViewAnchorTop), tracked so growth above the fold can be
   * compensated with a matching scrollTop adjustment while the user is not
   * pinned to the bottom. Null = no baseline yet (first sample / scope swap). */
  const midViewAnchorTopRef = useRef<number | null>(null)
  const pendingScrollRafRef = useRef(0)
  const prevScopeKeyRef = useRef(scopeKey)
  // Sticky "anchor at bottom" intent. Set on initial mount, scope change, and
  // visibility regains. Consumed by the ResizeObserver once the stream has
  // real scrollable content — handles the common case where envelopes arrive
  // after the scope/visibility event fired (e.g., initial app load).
  const pendingInitialScrollRef = useRef(true)

  /** Bulk history fill (agent switch / initial load): growth must land at
   *  its terminal scroll position directly. The smooth-follow spring must
   *  not ride these multi-thousand-px batches — that's the "page pulls up
   *  then pulls down" glitch on agent switch. Cleared by a quiet window
   *  after the scope settles. */
  const bulkFillUntilRef = useRef(0)

  /** Deadline (ms timestamp) until which non-streaming ResizeObserver growth
   *  pins to the floor with 'auto' instead of smooth-scrolling. Armed when
   *  streaming stops, for the duration of the TurnTail's grid-rows expansion
   *  (--ai-expand-ms), to avoid re-targeting the native smooth-scroll engine
   *  every frame (text jitter). See resolveAutoScrollBehavior. */
  const smoothSuppressUntilRef = useRef(0)
  const prevStreamingRef = useRef(isStreaming)
  /** Live mirror of isStreaming for the ResizeObserver callback. Keeping the
   *  observer's closure off the isStreaming prop avoids tearing the observer
   *  down and recreating it on every streaming transition — that recreation
   *  nulled pinnedTailRef/contentElRef and re-seeded lastScrollHeightRef,
   *  dropping the growth delta of the turn's first content append. */
  const isStreamingRef = useRef(isStreaming)
  isStreamingRef.current = isStreaming

  // ── Smooth scroll-follow ──
  // scrollTop pins at the floor (last TurnTail stays visible). The visual lag
  // from content growth rides as a translate3d on the whole content column —
  // user envelopes, completed turns and their tails all glide together as one
  // body — while the LAST turn's tail is counter-shifted so it stays pinned.
  // The lag converges to zero via a damped spring; when the user scrolls
  // away, the transforms clear.
  const smoothLagRef = useRef(0)
  const smoothRafRef = useRef(0)
  const smoothLastTsRef = useRef(0)
  const contentElRef = useRef<HTMLElement | null>(null)
  const pinnedTailRef = useRef<HTMLElement | null>(null)
  const reducedMotionRef = useRef(false)
  const smoothStreamRef = useRef(smoothStream)

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    const update = () => { reducedMotionRef.current = mq.matches }
    update()
    mq.addEventListener('change', update)
    return () => mq.removeEventListener('change', update)
  }, [])

  useEffect(() => {
    userScrolledUpRef.current = userScrolledUp
  }, [userScrolledUp])

  useEffect(() => {
    smoothStreamRef.current = smoothStream
  }, [smoothStream])

  const isNearBottom = useCallback((): boolean => {
    const el = streamRef.current
    if (!el) return true
    const maxScroll = el.scrollHeight - el.clientHeight
    if (maxScroll <= 0) return true
    return maxScroll - el.scrollTop < getBottomTolerance(el)
  }, [streamRef])

  // Synchronous scroll to bottom. Used on the streaming hot path (the
  // ResizeObserver callback): it fires after layout but before paint, so a
  // synchronous scrollTo here lands in the SAME frame's paint. Deferring to
  // requestAnimationFrame instead would paint the frame with the old scrollTop
  // first, producing a 1-frame snap on every streamed token — the jitter.
  const performScrollToBottom = useCallback((behavior: ScrollBehavior = 'auto') => {
    const el = streamRef.current
    if (!el) return
    const guardMs = getProgrammaticScrollGuardMs(behavior)
    programmaticScrollUntilRef.current = Date.now() + guardMs
    const targetTop = el.scrollHeight - el.clientHeight
    el.scrollTo({ top: Math.max(0, targetTop), behavior })
  }, [streamRef])

  // RAF-deferred variant for one-shot / user-initiated scrolls (scope change,
  // visibility regain, scroll-to-bottom button) where a 1-frame delay is
  // imperceptible. NOT used for per-token streaming growth.
  const scheduleScrollToBottom = useCallback((behavior: ScrollBehavior = 'auto') => {
    cancelAnimationFrame(pendingScrollRafRef.current)
    pendingScrollRafRef.current = requestAnimationFrame(() => performScrollToBottom(behavior))
  }, [performScrollToBottom])

  // ── Mid-view growth compensation ──
  // Native scroll anchoring is disabled on the stream (it fought the
  // programmatic follow scrolls), so growth above the fold while the user is
  // NOT pinned to the bottom shifts the visible content with nothing to
  // compensate: a mermaid card popping in after a history load, a late image,
  // a virtualization placeholder swap with a stale cached height. Diffing the
  // fold-straddling child's content-relative top (transform-cancelled rect
  // math, same as the tail top above) measures how much the content above the
  // fold moved; re-adjusting scrollTop by that delta keeps the visible picture
  // stable in the same frame (the ResizeObserver fires after layout, before
  // paint). No-op when pinned to the bottom — there the re-pin path handles it.
  const measureMidViewAnchorTop = useCallback((): number | null => {
    const el = streamRef.current
    if (!el) return null
    const content = contentRef?.current ?? el
    const contentTop = content.getBoundingClientRect().top
    const foldTop = el.getBoundingClientRect().top - contentTop
    // Lazy generator: rects are read one child at a time so the cost is
    // bounded by the index of the fold child (small when scrolled up), not
    // the whole conversation — this runs on every scroll event while the
    // stream is not at the bottom.
    function* childGeometry(): Generator<AnchorChildGeometry> {
      for (let i = 0; i < content.children.length; i++) {
        const r = (content.children[i] as HTMLElement).getBoundingClientRect()
        yield { top: r.top - contentTop, bottom: r.bottom - contentTop }
      }
    }
    return pickMidViewAnchorTop(childGeometry(), foldTop)
  }, [streamRef, contentRef])

  /** Keep the visible content put when growth lands above the fold while the
   * user is not following the bottom. Adjusts scrollTop by the anchor delta.
   *
   * No-op (baseline refresh only) while pinned at the scroll floor: a shrink
   * above the tail (turn-completion folding a tall frame — e.g. a mermaid
   * diagram — away) makes the browser clamp scrollTop down to the new floor,
   * moving the fold-straddling anchor to an earlier child. Diffing against the
   * pre-shrink baseline would then scroll the viewport UP by the whole gap,
   * and the next growth tick would latch userScrolledUp. The clamp alone
   * already keeps the bottom pinned, so no scroll write is needed. */
  const compensateMidViewGrowth = useCallback(() => {
    const el = streamRef.current
    if (!el) return
    if (isAtScrollFloor({
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
      scrollTop: el.scrollTop,
    })) {
      midViewAnchorTopRef.current = measureMidViewAnchorTop()
      return
    }
    const cur = measureMidViewAnchorTop()
    const prev = midViewAnchorTopRef.current
    // Sub-pixel rounding (fractional line boxes, re-raster, zoom levels) makes
    // consecutive anchor measurements differ by fractions of a pixel; writing
    // those back micro-scrolls the idle stream. Only compensate real shifts
    // (>= 1px) — smaller drift just refreshes the baseline.
    if (cur !== null && prev !== null && Math.abs(cur - prev) >= MIDVIEW_COMPENSATE_MIN_PX) {
      programmaticScrollUntilRef.current = Date.now() + getProgrammaticScrollGuardMs('auto')
      el.scrollTop += cur - prev
      console.info(`[jitter-diag] midview-compensate delta=${(cur - prev).toFixed(1)}`)
    }
    midViewAnchorTopRef.current = cur
  }, [streamRef, measureMidViewAnchorTop])

  // ── Smooth follow engine ──

  const findLagTargets = useCallback((): boolean => {
    const content = contentRef?.current
    if (!content) return false
    // The lag transform rides directly on the content column; the pinned
    // tail (with its anchored pending-submits list) counter-shifts inside.
    contentElRef.current = content
    const tails = content.querySelectorAll<HTMLElement>('.ai-turn-tail')
    const next = tails.length > 0 ? tails[tails.length - 1]! : null
    // A new turn arrived: the previously pinned tail rejoins the gliding
    // body — clear its stale counter-shift so it doesn't hang mid-air.
    if (pinnedTailRef.current && pinnedTailRef.current !== next) {
      pinnedTailRef.current.style.transform = ''
    }
    pinnedTailRef.current = next
    // No tail to pin: the content transform alone would push the last
    // element into the composer. Refuse so the caller skips the shift.
    return next !== null
  }, [contentRef])

  /** Hard cap on the visual lag, relative to the pinned tail's height:
   * the counter-shifted tail must never be pushed further up than ~2/3 of
   * its own height, and the lag can never exceed half the viewport. A final
   * cap of ~1.5 line heights keeps the spring gap visually tight — the
   * content should never trail the tail by more than a line. */
  const clampLag = useCallback((lag: number): number => {
    if (lag <= 0) return 0
    const viewportCap = Math.max(120, (streamRef.current?.clientHeight ?? 0) * 0.5)
    const tail = pinnedTailRef.current
    const tailCap = tail ? tail.offsetHeight * 0.66 + 60 : viewportCap
    const lineCap = 24
    return Math.min(lag, viewportCap, tailCap, lineCap)
  }, [streamRef])

  const applyFramesTransform = useCallback((lag: number) => {
    const content = contentElRef.current
    const tail = pinnedTailRef.current
    // Round to whole device pixels: sub-pixel translate3d values force the
    // text into fractional rasterization on the compositor layer, which
    // renders visibly blurry during the glide and only sharpens when the
    // transform is removed.
    const clampedLag = lag > 0.5 ? Math.round(clampLag(lag)) : 0
    // A lag without a counter-shifted tail pushes the bottom element into
    // the composer overlay. Refuse the shift entirely in that case.
    if (clampedLag > 0 && !tail) return
    if (content) {
      content.style.transform = clampedLag > 0 ? `translate3d(0, ${clampedLag}px, 0)` : ''
    }
    if (tail) {
      tail.style.transform = clampedLag > 0 ? `translate3d(0, ${-clampedLag}px, 0)` : ''
    }
  }, [clampLag])

  const clearSmoothTransforms = useCallback(() => {
    const content = contentElRef.current
    const tail = pinnedTailRef.current
    if (content) {
      content.style.transform = ''
    }
    if (tail) {
      tail.style.transform = ''
    }
  }, [])

  const startSmoothFollow = useCallback(() => {
    if (smoothRafRef.current !== 0) return

    const tick = (now: number) => {
      if (smoothLastTsRef.current === 0) {
        smoothLastTsRef.current = now
        smoothRafRef.current = requestAnimationFrame(tick)
        return
      }
      const dt = Math.min(50, now - smoothLastTsRef.current)
      smoothLastTsRef.current = now

      // User scrolled away: clear and stop
      if (userScrolledUpRef.current) {
        smoothLagRef.current = 0
        // Re-resolve the pinned tail BEFORE clearing: a turn boundary can
        // swap in a new .ai-turn-tail node while the spring still targets
        // the detached old one (React reuses the wrapper). Clearing only
        // the stale node leaves the live tail with a foreign transform —
        // the "tail jumps to the top then slides down" glitch.
        findLagTargets()
        applyFramesTransform(0)
        smoothRafRef.current = 0
        smoothLastTsRef.current = 0
        return
      }

      const lag = smoothLagRef.current
      if (lag <= 0.1) {
        smoothLagRef.current = 0
        findLagTargets()
        applyFramesTransform(0)
        smoothRafRef.current = 0
        smoothLastTsRef.current = 0
        return
      }

      // Re-resolve the pinned tail on every tick: React can swap the
      // .ai-turn-tail node mid-spring (envelope remount, virtualization
      // restore). Targeting a detached node leaves the live tail riding
      // the content transform — the "tail slides into the composer" bug.
      if (!findLagTargets()) {
        smoothLagRef.current = 0
        applyFramesTransform(0)
        smoothRafRef.current = 0
        smoothLastTsRef.current = 0
        return
      }
      const step = computeFollowStep(dt, { lag, speedEma: 0 })
      const newLag = Math.max(0, lag - step.advancePx)
      smoothLagRef.current = newLag
      applyFramesTransform(newLag)

      smoothRafRef.current = requestAnimationFrame(tick)
    }

    smoothRafRef.current = requestAnimationFrame(tick)
  }, [applyFramesTransform, findLagTargets])

  const scrollToBottom = useCallback((behavior: ScrollBehavior = 'smooth') => {
    userScrolledUpRef.current = false
    setUserScrolledUp(false)
    scheduleScrollToBottom(behavior)
  }, [scheduleScrollToBottom])

  // Reset lock state when the conversation/agent changes so the new stream
  // starts anchored at the bottom instead of inheriting the previous scroll
  // intent.
  useEffect(() => {
    if (prevScopeKeyRef.current === scopeKey) return
    prevScopeKeyRef.current = scopeKey
    userScrolledUpRef.current = false
    setUserScrolledUp(false)
    setCanScrollDown(false)
    lastScrollHeightRef.current = streamRef.current?.scrollHeight ?? 0
    // Content column swaps wholesale on a scope change: drop the tail
    // diff baseline so the next ResizeObserver tick starts fresh.
    lastTailTopRef.current = null
    lastChildCountRef.current = null
    midViewAnchorTopRef.current = null
    pendingInitialScrollRef.current = true
    bulkFillUntilRef.current = Date.now() + BULK_FILL_SMOOTH_SUPPRESS_MS
    scheduleScrollToBottom('auto')
  }, [scopeKey, streamRef, scheduleScrollToBottom])

  // When the stream becomes visible after being hidden (e.g., switching from
  // standalone shell back to the workbench, or opening an AI conversation tab),
  // scroll to the bottom so the latest messages are in view.
  useEffect(() => {
    const el = streamRef.current
    if (!el) return

    let wasVisible = false
    const io = new IntersectionObserver(([entry]) => {
      const isVisible = !!entry?.isIntersecting
      if (!wasVisible && isVisible) {
        userScrolledUpRef.current = false
        setUserScrolledUp(false)
        pendingInitialScrollRef.current = true
        scheduleScrollToBottom('auto')
      }
      wasVisible = isVisible
    })

    io.observe(el)
    return () => { io.disconnect() }
  }, [streamRef, scheduleScrollToBottom])

  // When streaming stops, kill any residual spring so the terminal state lands
  // exactly. This prevents the next user envelope (or history load) from being
  // briefly drawn with the previous turn's lag transform and then gliding up.
  // Also arm the smooth-auto-scroll suppression window for the TurnTail's
  // grid-rows expansion (--ai-expand-ms): the ResizeObserver fires per-frame
  // during that transition, and a smooth scrollTo on each fire re-targets the
  // native scroll engine continuously, jittering the text.
  useLayoutEffect(() => {
    if (isStreaming) {
      prevStreamingRef.current = true
      return
    }
    if (prevStreamingRef.current) {
      smoothSuppressUntilRef.current = Date.now() + readExpandMs(streamRef.current) + SMOOTH_SUPPRESS_BUFFER_MS
    }
    prevStreamingRef.current = false
    if (smoothRafRef.current) {
      cancelAnimationFrame(smoothRafRef.current)
      smoothRafRef.current = 0
    }
    smoothLagRef.current = 0
    smoothLastTsRef.current = 0
    clearSmoothTransforms()
  }, [isStreaming, clearSmoothTransforms, streamRef])

  // Scroll listener: distinguishes manual scroll from programmatic scroll.
  useEffect(() => {
    const el = streamRef.current
    if (!el) return

    const handleScroll = () => {
      const atBottom = isNearBottom()
      setCanScrollDown(!atBottom)

      // Re-pick the mid-view anchor at the new fold (only meaningful away
      // from the bottom — while pinned, the re-pin path handles growth, and
      // skipping it keeps the per-token streaming scroll cheap).
      if (!atBottom) {
        midViewAnchorTopRef.current = measureMidViewAnchorTop()
      }

      const now = Date.now()
      if (now < programmaticScrollUntilRef.current && !isTouchingRef.current) {
        // Programmatic scroll event. If it landed at the bottom, clear the
        // user-scrolled-up flag.
        if (atBottom) {
          userScrolledUpRef.current = false
          setUserScrolledUp(false)
        }
        return
      }

      if (!atBottom) {
        if (!userScrolledUpRef.current) {
          userScrolledUpRef.current = true
          setUserScrolledUp(true)
        }
      } else if (userScrolledUpRef.current) {
        userScrolledUpRef.current = false
        setUserScrolledUp(false)
      }
    }

    // Initialize button visibility in case the stream starts already overflowed.
    setCanScrollDown(!isNearBottom())

    el.addEventListener('scroll', handleScroll, { passive: true })
    return () => el.removeEventListener('scroll', handleScroll)
  }, [streamRef, isNearBottom, measureMidViewAnchorTop])

  // User-interaction guards: selection, pointer down, and active touch gestures.
  useEffect(() => {
    const handlePointerDown = (e: PointerEvent) => {
      const stream = streamRef.current
      if (stream && stream.contains(e.target as Node)) {
        isPointerDownRef.current = true
      }
    }
    const handlePointerUp = () => {
      isPointerDownRef.current = false
    }
    const handleSelectionChange = () => {
      const sel = document.getSelection()
      const stream = streamRef.current
      if (!stream || !sel || sel.isCollapsed) {
        isSelectingRef.current = false
        return
      }
      const anchorNode = sel.anchorNode
      if (anchorNode && stream.contains(anchorNode)) {
        isSelectingRef.current = true
      }
    }
    const handleTouchStart = () => {
      isTouchingRef.current = true
    }
    const handleTouchEnd = () => {
      isTouchingRef.current = false
    }
    const handleTouchCancel = () => {
      isTouchingRef.current = false
    }

    window.addEventListener('pointerdown', handlePointerDown)
    window.addEventListener('pointerup', handlePointerUp)
    document.addEventListener('selectionchange', handleSelectionChange)
    window.addEventListener('touchstart', handleTouchStart, { passive: true })
    window.addEventListener('touchend', handleTouchEnd, { passive: true })
    window.addEventListener('touchcancel', handleTouchCancel, { passive: true })
    return () => {
      window.removeEventListener('pointerdown', handlePointerDown)
      window.removeEventListener('pointerup', handlePointerUp)
      document.removeEventListener('selectionchange', handleSelectionChange)
      window.removeEventListener('touchstart', handleTouchStart)
      window.removeEventListener('touchend', handleTouchEnd)
      window.removeEventListener('touchcancel', handleTouchCancel)
    }
  }, [streamRef])

  // Auto-scroll when content grows or the scroll container itself is resized
  // (e.g., composer expands). We observe both the stream and its content so we
  // catch both kinds of size changes.
  useEffect(() => {
    const el = streamRef.current
    const content = contentRef?.current
    if (!el) return

    lastScrollHeightRef.current = el.scrollHeight

    const ro = new ResizeObserver(() => {
      const atBottom = isNearBottom()
      setCanScrollDown(!atBottom)

      // Growth classification inputs, measured before any scroll correction
      // mutates geometry. The tail top is taken from viewport rects differenced
      // against the content column: that cancels both the scroll offset and
      // the smooth-follow translate3d riding on the content.
      const newScrollHeight = el.scrollHeight
      const growth = newScrollHeight - lastScrollHeightRef.current
      const content = contentRef?.current ?? el
      const tailEl = content.lastElementChild as HTMLElement | null
      const tailTop = tailEl
        ? tailEl.getBoundingClientRect().top - content.getBoundingClientRect().top
        : null
      const tailShift = tailTop !== null && lastTailTopRef.current !== null
        ? tailTop - lastTailTopRef.current
        : null
      const appended = lastChildCountRef.current !== null &&
        content.childElementCount === lastChildCountRef.current + 1
      // Was the viewport at the tail BEFORE this growth tick? Derived from
      // geometry: content above the viewport can shrink without a scroll
      // event, leaving userScrolledUpRef stale-false while the viewport is
      // actually far from the bottom — auto-scrolling from there makes the
      // whole stream visibly re-scroll once (native smooth across the gap).
      const followedBefore = wasFollowingTail({
        prevScrollHeight: lastScrollHeightRef.current,
        clientHeight: el.clientHeight,
        scrollTop: el.scrollTop,
        tolerancePx: getBottomTolerance(el),
      })
      // Exact-bottom check: native scroll anchoring only engages AWAY from
      // the scroll extremes, so growth while pinned needs explicit handling.
      const wasPinnedToBottom = isAtScrollFloor({
        scrollHeight: lastScrollHeightRef.current,
        clientHeight: el.clientHeight,
        scrollTop: el.scrollTop,
      })
      lastScrollHeightRef.current = newScrollHeight
      lastTailTopRef.current = tailTop
      lastChildCountRef.current = content.childElementCount

      // Consume pending initial scroll once the stream actually has scrollable
      // content. This catches the case where scopeKey or visibility fired the
      // scroll before envelopes arrived (typical on app load).
      if (pendingInitialScrollRef.current) {
        if (el.scrollHeight > el.clientHeight + 1) {
          pendingInitialScrollRef.current = false
          userScrolledUpRef.current = false
          setUserScrolledUp(false)
          // History just materialized: kill any in-flight spring so the
          // terminal position is exact, not a glide target.
          if (smoothRafRef.current) {
            cancelAnimationFrame(smoothRafRef.current)
            smoothRafRef.current = 0
          }
          smoothLagRef.current = 0
          smoothLastTsRef.current = 0
          clearSmoothTransforms()
          performScrollToBottom('auto')
          console.info('[jitter-diag] initial-scroll-consume')
        }
        return
      }

      if (userScrolledUpRef.current) { compensateMidViewGrowth(); return }
      if (isSelectingRef.current) { compensateMidViewGrowth(); return }
      if (isPointerDownRef.current) { compensateMidViewGrowth(); return }

      if (growth <= 0) { compensateMidViewGrowth(); return }

      if (!followedBefore) {
        // Viewport drifted away from the bottom without a scroll event.
        // Treat it as scrolled up: show the scroll-down button, never
        // launch a full-stream scroll on the user's behalf.
        setCanScrollDown(true)
        if (!userScrolledUpRef.current) {
          userScrolledUpRef.current = true
          setUserScrolledUp(true)
        }
        console.info(`[jitter-diag] drift-away growth=${growth.toFixed(0)}`)
        compensateMidViewGrowth()
        return
      }

      // Growth entirely ABOVE the tail (late image load above, highlighter
      // settling its height, virtualization placeholder swapping with a stale
      // cached height): the visible content must stay put. Panning to the new
      // bottom — smooth or capped-instant — slides the text the user is
      // reading: the idle "sudden scroll". While pinned to the exact bottom,
      // scroll anchoring is disabled, but this callback runs after layout and
      // before paint, so an instant re-pin restores the exact previous
      // picture in the same frame. Away from the bottom the (disabled) native
      // scroll anchoring can no longer stabilize the view — compensate via
      // the mid-view anchor instead.
      if (classifyStreamGrowth({ growth, tailShift, appended }) === 'above') {
        console.info(`[jitter-diag] above-growth growth=${growth.toFixed(0)} tailShift=${tailShift === null ? 'na' : tailShift.toFixed(0)} pinned=${wasPinnedToBottom}`)
        if (wasPinnedToBottom) performScrollToBottom('auto')
        else compensateMidViewGrowth()
        return
      }

      // Streaming pins to the floor per-token ('auto'); the smooth-follow
      // spring provides the visual glide. For non-streaming growth, glide
      // smoothly — EXCEPT right after a turn ends: the TurnTail's grid-rows
      // expansion (--ai-expand-ms) fires this observer per-frame, and a smooth
      // scrollTo on each fire re-targets the native scroll engine
      // continuously, jittering the text. resolveAutoScrollBehavior suppresses
      // 'smooth' for that expansion window, pinning to the floor per-frame
      // instead (visually smooth like streaming). Any remaining smooth scroll
      // is capped: a large distance (section expanding above the viewport,
      // image landing, history prepend) must not animate across the whole
      // stream — that is the "整体重新滚动" glitch.
      let behavior = resolveAutoScrollBehavior({
        isStreaming: isStreamingRef.current,
        now: Date.now(),
        smoothSuppressUntil: smoothSuppressUntilRef.current,
        bulkFillUntil: bulkFillUntilRef.current,
        growthPx: growth,
      })
      if (behavior === 'smooth') {
        const distancePx = Math.max(0, (newScrollHeight - el.clientHeight) - el.scrollTop)
        const capPx = Math.max(SMOOTH_GROWTH_DISTANCE_CAP_MIN_PX, el.clientHeight * SMOOTH_GROWTH_DISTANCE_CAP_RATIO)
        const capped = capSmoothToAuto(behavior, distancePx, capPx)
        if (capped !== behavior) {
          console.info(`[jitter-diag] smooth-capped-auto distance=${distancePx.toFixed(0)} cap=${capPx.toFixed(0)}`)
        }
        behavior = capped
      }
      // Streaming, the post-turn expansion window and bulk fills keep pinning
      // to the floor as before. Idle tail growth (a duration badge wrapping to
      // a new line, a late settle, virtualization swaps) must not drag the
      // viewport anywhere: anchoring scrollTop by exactly the growth keeps the
      // visible picture identical — at the exact floor it equals the re-pin
      // (floor_new = floor_old + growth), and above the floor the user's gap
      // to the bottom is preserved instead of being snapped away.
      if (isIdleTailGrowth({
        isStreaming: isStreamingRef.current,
        now: Date.now(),
        smoothSuppressUntil: smoothSuppressUntilRef.current,
        bulkFillUntil: bulkFillUntilRef.current,
      })) {
        programmaticScrollUntilRef.current = Date.now() + getProgrammaticScrollGuardMs('auto')
        el.scrollTop += growth
        console.info(`[jitter-diag] idle-tail-anchor growth=${growth.toFixed(0)}`)
      } else {
        performScrollToBottom(behavior)
      }

      // Bulk history fill right after a scope change lands at the terminal
      // position — no spring ride, no translate3d.
      if (Date.now() < bulkFillUntilRef.current) {
        if (smoothRafRef.current) {
          cancelAnimationFrame(smoothRafRef.current)
          smoothRafRef.current = 0
        }
        smoothLagRef.current = 0
        smoothLastTsRef.current = 0
        clearSmoothTransforms()
        return
      }

      // Smooth follow: accumulate ALL growth as riding lag on the content
      // column. The spring's advance is lag-proportional, so a large
      // expansion (tool call unfolding) converges faster in lockstep with
      // the CSS expansion itself — the push-up speed matches the expand
      // speed. scrollTop is pinned to the floor and the lag transform is
      // applied SYNCHRONOUSLY here (this callback runs after layout but
      // before paint), so the new content never flashes fully in place
      // before the glide starts.
      if (isStreamingRef.current && !reducedMotionRef.current && smoothStreamRef.current) {
        if (findLagTargets()) {
          // Accumulate, then clamp to the visual cap: the spring target must
          // stay inside what the tail cap allows, so convergence is measured
          // from the clamped value.
          const rawLag = smoothLagRef.current + growth
          smoothLagRef.current = clampLag(rawLag)
          if (rawLag > 24 && rawLag - smoothLagRef.current > 8) {
            console.info(`[jitter-diag] lag-capped raw=${rawLag.toFixed(0)} clamped=${smoothLagRef.current.toFixed(0)}`)
          }
          applyFramesTransform(smoothLagRef.current)
          startSmoothFollow()
        }
      }
    })

    ro.observe(el)
    if (content) ro.observe(content)
    return () => {
      ro.disconnect()
      cancelAnimationFrame(pendingScrollRafRef.current)
      cancelAnimationFrame(smoothRafRef.current)
      smoothRafRef.current = 0
      smoothLagRef.current = 0
      smoothLastTsRef.current = 0
      clearSmoothTransforms()
      contentElRef.current = null
      pinnedTailRef.current = null
    }
  }, [streamRef, contentRef, isNearBottom, performScrollToBottom, compensateMidViewGrowth, findLagTargets, applyFramesTransform, clearSmoothTransforms, startSmoothFollow, clampLag])

  // When streaming starts and the user is already near the bottom, resume
  // auto-scroll immediately. Use the synchronous scroll so the first token
  // of a resumed stream does not produce a 1-frame snap.
  // Also reset the smooth-follow engine: clear residual transforms/lag from
  // the previous turn so the new turn's tail isn't visually displaced, and
  // re-resolve the pinned tail to the new turn's .ai-turn-tail element.
  useEffect(() => {
    if (!isStreaming) return
    if (smoothRafRef.current) {
      cancelAnimationFrame(smoothRafRef.current)
      smoothRafRef.current = 0
    }
    smoothLagRef.current = 0
    smoothLastTsRef.current = 0
    clearSmoothTransforms()
    findLagTargets()
    if (userScrolledUpRef.current) {
      if (!isNearBottom()) return
      userScrolledUpRef.current = false
      setUserScrolledUp(false)
    }
    performScrollToBottom('auto')
  }, [isStreaming, isNearBottom, performScrollToBottom, clearSmoothTransforms, findLagTargets])

  const scrollAnchorRef = useRef<ScrollAnchor | null>(null)

  const saveScrollAnchor = useCallback(() => {
    const el = streamRef.current
    if (!el) return
    const content = contentRef?.current ?? el
    const first = content.querySelector('[data-envelope-id]') as HTMLElement | null
    if (!first || !first.dataset.envelopeId) return
    scrollAnchorRef.current = {
      elementId: first.dataset.envelopeId,
      delta: first.offsetTop - el.scrollTop,
    }
  }, [streamRef, contentRef])

  const restoreScrollAnchor = useCallback(() => {
    const anchor = scrollAnchorRef.current
    if (!anchor) return
    scrollAnchorRef.current = null
    const el = streamRef.current
    if (!el) return
    const content = contentRef?.current ?? el
    const target = content.querySelector(`[data-envelope-id="${anchor.elementId}"]`) as HTMLElement | null
    if (target && el) {
      el.scrollTop = target.offsetTop - anchor.delta
    }
  }, [streamRef, contentRef])

  return { canScrollDown, scrollToBottom, saveScrollAnchor, restoreScrollAnchor }
}
