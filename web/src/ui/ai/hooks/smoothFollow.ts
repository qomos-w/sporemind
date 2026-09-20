/**
 * Damped-spring lerp for the smooth scroll-follow.
 *
 * Ported from dsh-smooth-stream's teleprompterGlide `computeFollowStep`.
 * The scroll container pins `scrollTop` at the floor so the TurnTail
 * (turn-status chrome) stays visible. The visual lag from content growth
 * rides as a `translate3d` on the frames container and converges to zero
 * via this spring, so new lines glide into place instead of snapping.
 */

/** Damped-spring time constant in ms: `1 - exp(-dt / 10)`. */
export const FOLLOW_LERP_DT_MS = 10

/** Floor / ceiling of the per-frame lerp fraction before the dt term.
 * Tuned against the CSS expansion (--ai-expand-ms, 1s default): the RO
 * fires every frame WHILE the grid-rows transition animates, so the lag
 * reaches a steady state of rate/k. The minimum is raised so that
 * steady-state lag for typical streaming (~1-2px/frame) stays below one
 * line height, preventing the content from visibly trailing the pinned tail. */
export const FOLLOW_LERP_MIN = 0.20
export const FOLLOW_LERP_MAX = 0.30

/** Lag (px) at which the lag term of the lerp saturates. Raised from 160:
 * big tool-body expansions (bash output, edit diff) must not crawl. */
export const FOLLOW_LERP_LAG_REF_PX = 320

/** Reveal cps at which the speed factor is 1. */
export const FOLLOW_SPEED_REF_CPS = 35

export const FOLLOW_SPEED_FACTOR_MIN = 0.7
export const FOLLOW_SPEED_FACTOR_MAX = 2.2

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

export interface FollowStepInput {
  /** How far the interpolated position trails the target, in px. */
  readonly lag: number
  /** Observed reveal rate in chars/s; 0 uses the reference speed. */
  readonly speedEma: number
}

export interface FollowStep {
  /** Pixels to advance this frame (fractional). */
  readonly advancePx: number
  /** Applied lerp fraction, for tests. */
  readonly lerpStep: number
}

/**
 * One spring-lerp frame.
 *
 * @param dtMs - Frame delta in ms.
 * @param input - Current lag and reveal-speed EMA.
 * @returns The fractional advance and the lerp fraction.
 */
export function computeFollowStep(dtMs: number, input: FollowStepInput): FollowStep {
  if (input.lag <= 0.1 || dtMs <= 0) return { advancePx: 0, lerpStep: 0 }
  const speed = input.speedEma > 0 ? input.speedEma : FOLLOW_SPEED_REF_CPS
  const speedFactor = clamp(speed / FOLLOW_SPEED_REF_CPS, FOLLOW_SPEED_FACTOR_MIN, FOLLOW_SPEED_FACTOR_MAX)
  const baseLerp = clamp((input.lag / FOLLOW_LERP_LAG_REF_PX) * speedFactor, FOLLOW_LERP_MIN, FOLLOW_LERP_MAX)
  const lerpStep = baseLerp * (1 - Math.exp(-dtMs / FOLLOW_LERP_DT_MS))
  return { advancePx: input.lag * lerpStep, lerpStep }
}

/**
 * One damped-spring frame advancing a scroll container's `scrollTop` toward a
 * growing tail target (`scrollHeight - clientHeight`). This mirrors the outer
 * stream's smooth-follow (`computeFollowStep`) so a running terminal-tail box
 * (ToolCodeBlock's `.running-scroll`) glides instead of snapping per token.
 *
 * Returns the next scrollTop, never exceeding `target`. When the gap is
 * negligible (< 0.5px) it snaps to `target`; when `target` is at or behind the
 * current position (no growth, or content shrank) it holds.
 */
export function advanceScrollToTail(dtMs: number, scrollTop: number, target: number): number {
  const lag = target - scrollTop
  if (lag <= 0.5) return Math.max(scrollTop, target)
  const { advancePx } = computeFollowStep(dtMs, { lag, speedEma: 0 })
  return Math.min(target, scrollTop + advancePx)
}

/** Buffer past the CSS expansion duration before resuming smooth auto-scroll,
 *  so the TurnTail's grid-rows transition fully settles. */
export const SMOOTH_SUPPRESS_BUFFER_MS = 150

/**
 * Resolves the auto-scroll behavior for a ResizeObserver growth event.
 *
 * `'auto'` (instant pin to the floor) is used while streaming — the
 * smooth-follow spring already provides the visual glide — AND for a
 * suppression window after streaming stops: the TurnTail's grid-rows
 * expansion (`--ai-expand-ms`) fires the ResizeObserver per-frame, and a
 * `scrollTo({behavior:'smooth'})` on each fire re-targets the native smooth
 * scroll engine continuously, jittering the text. After the window, `'smooth'`
 * resumes so discrete non-streaming growth (e.g. a user envelope appearing
 * after the turn ends) glides instead of snapping.
 *
 * `bulkFillUntil` scopes the same instant-pin rule to the bulk history fill
 * window after a scope change (agent switch / initial load): growth inside it
 * must land at the terminal position directly, never as a native smooth
 * scroll across a multi-thousand-px batch.
 */
export function resolveAutoScrollBehavior(opts: {
  isStreaming: boolean
  now: number
  smoothSuppressUntil: number
  bulkFillUntil?: number
  /** scrollHeight delta of the tick triggering the scroll, px. Micro growth
   *  pins instantly: a ≤8px glide reads as motion for no informational value,
   *  and any periodic size source oscillating by a pixel (composer padding
   *  var churn, subpixel drift) then animates the idle stream every cycle. */
  growthPx?: number
}): 'auto' | 'smooth' {
  if (opts.isStreaming) return 'auto'
  if (opts.now < opts.smoothSuppressUntil) return 'auto'
  if (opts.bulkFillUntil !== undefined && opts.now < opts.bulkFillUntil) return 'auto'
  if (opts.growthPx !== undefined && opts.growthPx <= MICRO_GROWTH_SMOOTH_MAX_PX) return 'auto'
  return 'smooth'
}

/** Growth at or below this pins instantly instead of animating a glide. */
export const MICRO_GROWTH_SMOOTH_MAX_PX = 8

/** Whether a tail-region growth tick happens while the stream is idle: not
 *  streaming, outside the post-turn expansion window and outside the bulk
 *  history fill window. Idle tail growth (duration badge wrap, late async
 *  settle, virtualization swaps) anchors the viewport by exactly the growth
 * instead of re-pinning to the scroll floor, so a user reading above the
 * bottom is never dragged down. */
export function isIdleTailGrowth(opts: {
  isStreaming: boolean
  now: number
  smoothSuppressUntil: number
  bulkFillUntil: number
}): boolean {
  if (opts.isStreaming) return false
  return opts.now >= opts.smoothSuppressUntil && opts.now >= opts.bulkFillUntil
}

/** Minimum |delta| (px) for the mid-view anchor compensation to write
 *  scrollTop. Below this the drift is treated as measurement noise and only
 *  refreshes the baseline. */
export const MIDVIEW_COMPENSATE_MIN_PX = 1

/**
 * Whether the viewport was following the stream tail BEFORE a growth tick,
 * derived from geometry rather than the userScrolledUp flag.
 *
 * Content above the viewport can shrink or grow (grid-rows fold animations,
 * turn-completion folding, history prepends, virtualization placeholders)
 * WITHOUT firing a scroll event — scrollTop is unchanged, so the
 * userScrolledUp flag stays stale-false while the viewport is actually far
 * from the bottom. Deciding from real distances prevents the next growth
 * tick from launching a native smooth scroll across the whole stream.
 */
export function wasFollowingTail(opts: {
  prevScrollHeight: number
  clientHeight: number
  scrollTop: number
  tolerancePx: number
}): boolean {
  const prevMaxScroll = Math.max(0, opts.prevScrollHeight - opts.clientHeight)
  return prevMaxScroll - opts.scrollTop <= opts.tolerancePx
}

/** Floor for the smooth-glide distance cap, so tiny viewports still allow a
 *  small glide. */
export const SMOOTH_GROWTH_DISTANCE_CAP_MIN_PX = 160
/** A smooth glide only masks small per-token growth; beyond ~1.5 viewports a
 *  native smooth scroll visibly replays the whole stream ("整体重新滚动"). */
export const SMOOTH_GROWTH_DISTANCE_CAP_RATIO = 1.5

/**
 * Downgrades a `'smooth'` auto-scroll to `'auto'` when the distance to cover
 * exceeds the cap. The smooth glide is meant to mask small per-token growth;
 * animating across most of the stream (a folded section expanding above the
 * viewport, an image landing, a history prepend) replays the whole stream
 * once. Such growth lands instantly instead.
 */
export function capSmoothToAuto(
  behavior: 'auto' | 'smooth',
  distancePx: number,
  capPx: number,
): 'auto' | 'smooth' {
  if (behavior === 'smooth' && distancePx > capPx) return 'auto'
  return behavior
}

/** Tolerance (px) when matching the tail's downward shift against the growth. */
export const TAIL_SHIFT_MATCH_PX = 1

/**
 * Whether the scroll position sits at the exact scroll floor (bottom), within
 * a sub-layout epsilon.
 *
 * While pinned, mid-view growth compensation must be a no-op: a shrink above
 * the tail (e.g. turn-completion folding a tall frame away) lets the browser
 * clamp scrollTop to the new floor on its own, and the fold-straddling anchor
 * jumps to an earlier child — diffing that against the pre-shrink baseline
 * would adjust scrollTop UP by the whole gap (the "turn end scrolls up" bug).
 */
export function isAtScrollFloor(opts: {
  scrollHeight: number
  clientHeight: number
  scrollTop: number
  tolerancePx?: number
}): boolean {
  const maxScroll = Math.max(0, opts.scrollHeight - opts.clientHeight)
  return maxScroll - opts.scrollTop <= (opts.tolerancePx ?? TAIL_SHIFT_MATCH_PX)
}

/** Where a ResizeObserver growth tick happened relative to the stream tail. */
export type StreamGrowthKind = 'tail' | 'above'

/**
 * Classifies a ResizeObserver growth tick as occurring at the tail or entirely
 * above it.
 *
 * Idle (non-streaming) growth that lands ABOVE the viewport — a late image
 * load, a code-highlighter settling its measured height, or a virtualization
 * placeholder swapping in with a stale cached height — must keep the visible
 * content put. Panning to the (new) bottom in that case slides the text the
 * user is reading: the idle "sudden scroll". Growth that extends the tail
 * (new content appended, the last envelope growing at its bottom edge)
 * follows the bottom as before.
 *
 * The signal is geometric: an above-the-tail growth shifts EVERY downstream
 * element — including the content's last child — downward by the full
 * growth, so the last child's top moves by the growth. A tail growth leaves
 * the last child's top unmoved (or, for an append, adds a brand-new child).
 */
export function classifyStreamGrowth(opts: {
  /** scrollHeight delta this tick, px. */
  readonly growth: number
  /** Top delta of the content's last child this tick, px; null on the first
   *  sample (no previous value to diff). */
  readonly tailShift: number | null
  /** Whether a new child was appended to the content column this tick. */
  readonly appended: boolean
}): StreamGrowthKind {
  if (opts.appended) return 'tail'
  if (opts.tailShift === null || opts.growth <= 0) return 'tail'
  return opts.tailShift >= opts.growth - TAIL_SHIFT_MATCH_PX ? 'above' : 'tail'
}

export interface AnchorChildGeometry {
  /** Content-relative top of a direct content child, px. */
  readonly top: number
  /** Content-relative bottom of the same child, px. */
  readonly bottom: number
}

/**
 * Mid-view scroll anchor: the content-relative top of the first direct child
 * whose bottom reaches the fold (the viewport top of the scroll container) —
 * i.e. the element straddling or just above the fold.
 *
 * The stream disables native scroll anchoring (`overflow-anchor: none`, so the
 * programmatic follow scrolls stay deterministic), so growth above the fold
 * while the user is NOT pinned to the bottom — a mermaid card popping in after
 * a history load, a late image, a virtualization placeholder swap — shifts the
 * visible content with nothing to compensate. Diffing this anchor's top across
 * a growth tick measures exactly how much the content above the fold moved, so
 * the caller can re-adjust scrollTop and keep the visible picture stable.
 *
 * Returns null when no child reaches the fold (empty content).
 */
export function pickMidViewAnchorTop(
  children: Iterable<AnchorChildGeometry>,
  /** Content-relative Y of the scroll container's viewport top. */
  foldTop: number,
): number | null {
  for (const c of children) {
    if (c.bottom >= foldTop) return c.top
  }
  return null
}
