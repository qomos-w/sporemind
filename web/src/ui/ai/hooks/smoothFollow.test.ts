import { describe, it, expect } from 'vitest'
import { computeFollowStep, resolveAutoScrollBehavior, wasFollowingTail, capSmoothToAuto, classifyStreamGrowth, advanceScrollToTail, pickMidViewAnchorTop, isAtScrollFloor, isIdleTailGrowth, MIDVIEW_COMPENSATE_MIN_PX } from './smoothFollow'

describe('computeFollowStep', () => {
  it('returns zero for zero lag', () => {
    const step = computeFollowStep(16, { lag: 0, speedEma: 0 })
    expect(step.advancePx).toBe(0)
    expect(step.lerpStep).toBe(0)
  })

  it('returns zero for zero dt', () => {
    const step = computeFollowStep(0, { lag: 100, speedEma: 0 })
    expect(step.advancePx).toBe(0)
  })

  it('returns zero for negligible lag', () => {
    const step = computeFollowStep(16, { lag: 0.05, speedEma: 0 })
    expect(step.advancePx).toBe(0)
  })

  it('advances proportional to lag', () => {
    const small = computeFollowStep(16, { lag: 20, speedEma: 0 })
    const large = computeFollowStep(16, { lag: 200, speedEma: 0 })
    expect(large.advancePx).toBeGreaterThan(small.advancePx)
  })

  it('never advances more than the lag', () => {
    const step = computeFollowStep(1000, { lag: 50, speedEma: 0 })
    expect(step.advancePx).toBeLessThanOrEqual(50)
  })

  it('uses reference speed when speedEma is 0', () => {
    const step = computeFollowStep(16, { lag: 100, speedEma: 0 })
    expect(step.advancePx).toBeGreaterThan(0)
    expect(step.lerpStep).toBeGreaterThan(0)
  })

  it('scales with frame time', () => {
    const short = computeFollowStep(8, { lag: 100, speedEma: 0 })
    const normal = computeFollowStep(16, { lag: 100, speedEma: 0 })
    expect(short.advancePx).toBeLessThan(normal.advancePx)
  })

  it('lerp fraction is always between 0 and 1', () => {
    for (const lag of [1, 10, 100, 500, 1000]) {
      for (const dt of [1, 8, 16, 33, 50]) {
        const step = computeFollowStep(dt, { lag, speedEma: 0 })
        expect(step.lerpStep).toBeGreaterThanOrEqual(0)
        expect(step.lerpStep).toBeLessThanOrEqual(1)
      }
    }
  })
})

describe('resolveAutoScrollBehavior', () => {
  it('returns auto while streaming', () => {
    expect(resolveAutoScrollBehavior({ isStreaming: true, now: 5000, smoothSuppressUntil: 3000 })).toBe('auto')
  })

  it('returns auto during the post-stream suppression window', () => {
    expect(resolveAutoScrollBehavior({ isStreaming: false, now: 3000, smoothSuppressUntil: 5000 })).toBe('auto')
  })

  it('returns smooth after the suppression window expires', () => {
    expect(resolveAutoScrollBehavior({ isStreaming: false, now: 6000, smoothSuppressUntil: 5000 })).toBe('smooth')
  })

  it('returns smooth when not streaming and no suppression set', () => {
    expect(resolveAutoScrollBehavior({ isStreaming: false, now: 1000, smoothSuppressUntil: 0 })).toBe('smooth')
  })

  it('returns auto during the bulk-fill window', () => {
    expect(resolveAutoScrollBehavior({ isStreaming: false, now: 1000, smoothSuppressUntil: 0, bulkFillUntil: 2000 })).toBe('auto')
  })

  it('returns smooth after the bulk-fill window expires', () => {
    expect(resolveAutoScrollBehavior({ isStreaming: false, now: 3000, smoothSuppressUntil: 0, bulkFillUntil: 2000 })).toBe('smooth')
  })

  it('treats a zero bulk-fill deadline as unset', () => {
    expect(resolveAutoScrollBehavior({ isStreaming: false, now: 1000, smoothSuppressUntil: 0, bulkFillUntil: 0 })).toBe('smooth')
  })

  it('pins instantly for micro growth instead of animating a glide', () => {
    expect(resolveAutoScrollBehavior({ isStreaming: false, now: 6000, smoothSuppressUntil: 5000, growthPx: 1 })).toBe('auto')
    expect(resolveAutoScrollBehavior({ isStreaming: false, now: 6000, smoothSuppressUntil: 5000, growthPx: 8 })).toBe('auto')
  })

  it('still glides for real growth beyond the micro threshold', () => {
    expect(resolveAutoScrollBehavior({ isStreaming: false, now: 6000, smoothSuppressUntil: 5000, growthPx: 9 })).toBe('smooth')
    expect(resolveAutoScrollBehavior({ isStreaming: false, now: 6000, smoothSuppressUntil: 5000, growthPx: 400 })).toBe('smooth')
  })
})

describe('wasFollowingTail', () => {
  const tolerancePx = 80

  it('follows when pinned to the bottom', () => {
    expect(wasFollowingTail({ prevScrollHeight: 2000, clientHeight: 600, scrollTop: 1400, tolerancePx })).toBe(true)
  })

  it('follows within the bottom tolerance', () => {
    expect(wasFollowingTail({ prevScrollHeight: 2000, clientHeight: 600, scrollTop: 1340, tolerancePx })).toBe(true)
  })

  it('does not follow when far from the bottom', () => {
    expect(wasFollowingTail({ prevScrollHeight: 2000, clientHeight: 600, scrollTop: 0, tolerancePx })).toBe(false)
  })

  it('does not follow when scrollTop trails the previous floor beyond tolerance (stale userScrolledUp)', () => {
    // The scroll listener missed the drift (no scroll event fired), so the
    // userScrolledUp flag is stale-false — geometry still reports the truth.
    expect(wasFollowingTail({ prevScrollHeight: 2000, clientHeight: 600, scrollTop: 600, tolerancePx })).toBe(false)
  })

  it('follows when there is nothing to scroll', () => {
    expect(wasFollowingTail({ prevScrollHeight: 400, clientHeight: 600, scrollTop: 0, tolerancePx })).toBe(true)
  })
})

describe('isAtScrollFloor', () => {
  it('is pinned at the exact floor', () => {
    expect(isAtScrollFloor({ scrollHeight: 2000, clientHeight: 600, scrollTop: 1400 })).toBe(true)
  })

  it('is pinned within the sub-layout epsilon', () => {
    expect(isAtScrollFloor({ scrollHeight: 2000, clientHeight: 600, scrollTop: 1399.6 })).toBe(true)
  })

  it('is not pinned one viewport-anchor gap above the floor', () => {
    // The turn-end fold case: the browser clamped scrollTop to the new floor,
    // but a stale check computed against the taller pre-shrink content would
    // report not-pinned — the exact distinction the fix relies on.
    expect(isAtScrollFloor({ scrollHeight: 2000, clientHeight: 600, scrollTop: 800 })).toBe(false)
  })

  it('is pinned when there is nothing to scroll', () => {
    expect(isAtScrollFloor({ scrollHeight: 400, clientHeight: 600, scrollTop: 0 })).toBe(true)
  })

  it('honors a wider explicit tolerance', () => {
    expect(isAtScrollFloor({ scrollHeight: 2000, clientHeight: 600, scrollTop: 1360, tolerancePx: 80 })).toBe(true)
    expect(isAtScrollFloor({ scrollHeight: 2000, clientHeight: 600, scrollTop: 1300, tolerancePx: 80 })).toBe(false)
  })
})

describe('capSmoothToAuto', () => {
  it('keeps smooth for distances within the cap', () => {
    expect(capSmoothToAuto('smooth', 300, 600)).toBe('smooth')
  })

  it('downgrades smooth to auto beyond the cap', () => {
    expect(capSmoothToAuto('smooth', 4000, 600)).toBe('auto')
  })

  it('leaves auto untouched regardless of distance', () => {
    expect(capSmoothToAuto('auto', 4000, 600)).toBe('auto')
    expect(capSmoothToAuto('auto', 10, 600)).toBe('auto')
  })
})

describe('classifyStreamGrowth', () => {
  it('classifies growth above the tail when the last child shifts down by the full growth', () => {
    // Late image load / highlight settle above the viewport: everything
    // downstream — including the last child's top — moves down by the growth.
    expect(classifyStreamGrowth({ growth: 24, tailShift: 24, appended: false })).toBe('above')
  })

  it('matches within the 1px rounding tolerance', () => {
    expect(classifyStreamGrowth({ growth: 24, tailShift: 23.4, appended: false })).toBe('above')
  })

  it('classifies tail growth when the last child top stays put', () => {
    // Last envelope growing at its bottom edge (TaskList, tail image).
    expect(classifyStreamGrowth({ growth: 40, tailShift: 0, appended: false })).toBe('tail')
  })

  it('classifies partial above-shift inside the tail as tail growth', () => {
    expect(classifyStreamGrowth({ growth: 100, tailShift: 20, appended: false })).toBe('tail')
  })

  it('classifies an appended child as tail growth regardless of shift', () => {
    // New envelope appended after a tall previous one: the new last child's
    // top is far below the old last child's top — still tail growth.
    expect(classifyStreamGrowth({ growth: 20, tailShift: 500, appended: true })).toBe('tail')
  })

  it('falls back to tail growth on the first sample', () => {
    expect(classifyStreamGrowth({ growth: 30, tailShift: null, appended: false })).toBe('tail')
  })

  it('falls back to tail growth for non-positive growth', () => {
    expect(classifyStreamGrowth({ growth: 0, tailShift: 0, appended: false })).toBe('tail')
    expect(classifyStreamGrowth({ growth: -5, tailShift: -5, appended: false })).toBe('tail')
  })
})

describe('advanceScrollToTail', () => {
  it('snaps to the target when the gap is negligible', () => {
    expect(advanceScrollToTail(16, 100, 100.3)).toBe(100.3)
  })

  it('holds position when the target is at or behind the current scroll', () => {
    expect(advanceScrollToTail(16, 120, 100)).toBe(120)
    expect(advanceScrollToTail(16, 100, 100)).toBe(100)
  })

  it('advances part of the gap without exceeding the target', () => {
    const next = advanceScrollToTail(16, 0, 200)
    expect(next).toBeGreaterThan(0)
    expect(next).toBeLessThan(200)
  })

  it('holds position for zero frame time', () => {
    expect(advanceScrollToTail(0, 40, 200)).toBe(40)
  })

  it('converges monotonically to the target', () => {
    let top = 0
    const target = 300
    for (let i = 0; i < 200 && top < target; i++) {
      const next = advanceScrollToTail(16, top, target)
      expect(next).toBeGreaterThanOrEqual(top)
      top = next
    }
    expect(top).toBe(target)
  })
})

describe('pickMidViewAnchorTop', () => {
  it('picks the first child whose bottom reaches the fold', () => {
    const children = [
      { top: 0, bottom: 100 },
      { top: 100, bottom: 220 },
      { top: 220, bottom: 400 },
    ]
    // fold at 150: first child bottom (100) < 150; second bottom (220) >= 150
    expect(pickMidViewAnchorTop(children, 150)).toBe(100)
  })

  it('returns the first child when the fold is at the very top', () => {
    const children = [{ top: 0, bottom: 80 }, { top: 80, bottom: 200 }]
    expect(pickMidViewAnchorTop(children, 0)).toBe(0)
  })

  it('returns null when no child reaches the fold (empty/short content)', () => {
    expect(pickMidViewAnchorTop([{ top: 0, bottom: 50 }], 200)).toBeNull()
    expect(pickMidViewAnchorTop([], 0)).toBeNull()
  })
})

describe('isIdleTailGrowth', () => {
  it('is idle when not streaming and both windows have expired', () => {
    expect(isIdleTailGrowth({ isStreaming: false, now: 5000, smoothSuppressUntil: 4000, bulkFillUntil: 4000 })).toBe(true)
  })

  it('is never idle while streaming', () => {
    expect(isIdleTailGrowth({ isStreaming: true, now: 5000, smoothSuppressUntil: 0, bulkFillUntil: 0 })).toBe(false)
  })

  it('is not idle inside the post-turn expansion window', () => {
    expect(isIdleTailGrowth({ isStreaming: false, now: 3999, smoothSuppressUntil: 4000, bulkFillUntil: 0 })).toBe(false)
  })

  it('is not idle inside the bulk-fill window', () => {
    expect(isIdleTailGrowth({ isStreaming: false, now: 3999, smoothSuppressUntil: 0, bulkFillUntil: 4000 })).toBe(false)
  })
})

describe('MIDVIEW_COMPENSATE_MIN_PX', () => {
  it('filters sub-pixel anchor drift', () => {
    expect(Math.abs(0.4) < MIDVIEW_COMPENSATE_MIN_PX).toBe(true)
    expect(Math.abs(1.0) >= MIDVIEW_COMPENSATE_MIN_PX).toBe(true)
  })
})
