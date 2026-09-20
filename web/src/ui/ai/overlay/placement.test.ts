import { describe, expect, it } from 'vitest'
import { computePlacement, unionRect } from './placement'
import type { Rect, Size, Viewport } from './placement'

const viewport: Viewport = { width: 1000, height: 1000 }
const anchor: Rect = { x: 400, y: 400, width: 200, height: 100 }
const popup: Size = { width: 200, height: 100 }

describe('computePlacement', () => {
  describe('basic geometry for four sides', () => {
    it('places popup above the anchor for top', () => {
      const result = computePlacement(anchor, popup, { side: 'top', offset: 10 }, viewport)
      expect(result.x).toBe(400)
      expect(result.y).toBe(290)
      expect(result.actualSide).toBe('top')
    })

    it('places popup below the anchor for bottom', () => {
      const result = computePlacement(anchor, popup, { side: 'bottom', offset: 10 }, viewport)
      expect(result.x).toBe(400)
      expect(result.y).toBe(510)
      expect(result.actualSide).toBe('bottom')
    })

    it('places popup to the left of the anchor for left', () => {
      const result = computePlacement(anchor, popup, { side: 'left', offset: 10 }, viewport)
      expect(result.x).toBe(190)
      expect(result.y).toBe(400)
      expect(result.actualSide).toBe('left')
    })

    it('places popup to the right of the anchor for right', () => {
      const result = computePlacement(anchor, popup, { side: 'right', offset: 10 }, viewport)
      expect(result.x).toBe(610)
      expect(result.y).toBe(400)
      expect(result.actualSide).toBe('right')
    })
  })

  describe('three alignments', () => {
    const narrowPopup: Size = { width: 100, height: 50 }

    it('aligns to the start for top/bottom', () => {
      const top = computePlacement(anchor, narrowPopup, { side: 'top', align: 'start', offset: 10 }, viewport)
      expect(top.x).toBe(400)
      expect(top.y).toBe(340)

      const bottom = computePlacement(anchor, narrowPopup, { side: 'bottom', align: 'start', offset: 10 }, viewport)
      expect(bottom.x).toBe(400)
      expect(bottom.y).toBe(510)
    })

    it('aligns to the center for top/bottom', () => {
      const top = computePlacement(anchor, narrowPopup, { side: 'top', align: 'center', offset: 10 }, viewport)
      expect(top.x).toBe(450)
      expect(top.y).toBe(340)

      const bottom = computePlacement(anchor, narrowPopup, { side: 'bottom', align: 'center', offset: 10 }, viewport)
      expect(bottom.x).toBe(450)
      expect(bottom.y).toBe(510)
    })

    it('aligns to the end for top/bottom', () => {
      const top = computePlacement(anchor, narrowPopup, { side: 'top', align: 'end', offset: 10 }, viewport)
      expect(top.x).toBe(500)
      expect(top.y).toBe(340)

      const bottom = computePlacement(anchor, narrowPopup, { side: 'bottom', align: 'end', offset: 10 }, viewport)
      expect(bottom.x).toBe(500)
      expect(bottom.y).toBe(510)
    })

    it('aligns to the start for left/right', () => {
      const left = computePlacement(anchor, narrowPopup, { side: 'left', align: 'start', offset: 10 }, viewport)
      expect(left.x).toBe(290)
      expect(left.y).toBe(400)

      const right = computePlacement(anchor, narrowPopup, { side: 'right', align: 'start', offset: 10 }, viewport)
      expect(right.x).toBe(610)
      expect(right.y).toBe(400)
    })

    it('aligns to the center for left/right', () => {
      const left = computePlacement(anchor, narrowPopup, { side: 'left', align: 'center', offset: 10 }, viewport)
      expect(left.x).toBe(290)
      expect(left.y).toBe(425)

      const right = computePlacement(anchor, narrowPopup, { side: 'right', align: 'center', offset: 10 }, viewport)
      expect(right.x).toBe(610)
      expect(right.y).toBe(425)
    })

    it('aligns to the end for left/right', () => {
      const left = computePlacement(anchor, narrowPopup, { side: 'left', align: 'end', offset: 10 }, viewport)
      expect(left.x).toBe(290)
      expect(left.y).toBe(450)

      const right = computePlacement(anchor, narrowPopup, { side: 'right', align: 'end', offset: 10 }, viewport)
      expect(right.x).toBe(610)
      expect(right.y).toBe(450)
    })

    it('defaults to center alignment', () => {
      const explicit = computePlacement(anchor, popup, { side: 'top', align: 'center' }, viewport)
      const implicit = computePlacement(anchor, popup, { side: 'top' }, viewport)
      expect(implicit.x).toBe(explicit.x)
      expect(implicit.y).toBe(explicit.y)
    })
  })

  describe('flip when preferred side has no space', () => {
    it('flips top to bottom when space above is insufficient', () => {
      const nearTop: Rect = { x: 400, y: 50, width: 200, height: 100 }
      const result = computePlacement(nearTop, popup, { side: 'top', offset: 10, viewportPadding: 20 }, viewport)
      expect(result.actualSide).toBe('bottom')
      expect(result.y).toBe(160)
      expect(result.x).toBe(400)
    })

    it('flips bottom to top when space below is insufficient', () => {
      const nearBottom: Rect = { x: 400, y: 850, width: 200, height: 100 }
      const result = computePlacement(nearBottom, popup, { side: 'bottom', offset: 10, viewportPadding: 20 }, viewport)
      expect(result.actualSide).toBe('top')
      expect(result.y).toBe(740)
      expect(result.x).toBe(400)
    })

    it('flips left to right when space to the left is insufficient', () => {
      const nearLeft: Rect = { x: 50, y: 400, width: 100, height: 200 }
      const result = computePlacement(nearLeft, popup, { side: 'left', align: 'start', offset: 10, viewportPadding: 20 }, viewport)
      expect(result.actualSide).toBe('right')
      expect(result.x).toBe(160)
      expect(result.y).toBe(400)
    })

    it('flips right to left when space to the right is insufficient', () => {
      const nearRight: Rect = { x: 850, y: 400, width: 100, height: 200 }
      const result = computePlacement(nearRight, popup, { side: 'right', align: 'start', offset: 10, viewportPadding: 20 }, viewport)
      expect(result.actualSide).toBe('left')
      expect(result.x).toBe(640)
      expect(result.y).toBe(400)
    })
  })

  describe('clamp to viewport', () => {
    it('clamps popup that overflows on the right after flipping', () => {
      const nearTop: Rect = { x: 400, y: 50, width: 200, height: 100 }
      const widePopup: Size = { width: 900, height: 100 }
      const result = computePlacement(nearTop, widePopup, { side: 'top', align: 'start', offset: 10, viewportPadding: 20 }, viewport)
      expect(result.actualSide).toBe('bottom')
      expect(result.x).toBe(80)
      expect(result.y).toBe(160)
    })

    it('clamps popup that overflows on the bottom after flipping', () => {
      const nearTop: Rect = { x: 400, y: 50, width: 200, height: 100 }
      const tallPopup: Size = { width: 200, height: 900 }
      const result = computePlacement(nearTop, tallPopup, { side: 'top', offset: 10, viewportPadding: 20 }, viewport)
      expect(result.actualSide).toBe('bottom')
      expect(result.x).toBe(400)
      expect(result.y).toBe(80)
    })

    it('clamps popup to viewport padding when both sides are too small', () => {
      const smallViewport: Viewport = { width: 300, height: 300 }
      const nearTop: Rect = { x: 50, y: 10, width: 50, height: 50 }
      const oversizedPopup: Size = { width: 400, height: 400 }
      const result = computePlacement(nearTop, oversizedPopup, { side: 'top', offset: 10, viewportPadding: 10 }, smallViewport)
      expect(result.x).toBe(10)
      expect(result.y).toBe(10)
    })

    it('does not move a fitting popup', () => {
      const result = computePlacement(anchor, popup, { side: 'top', offset: 10 }, viewport)
      expect(result.x).toBe(400)
      expect(result.y).toBe(290)
    })
  })

  describe('offset and viewportPadding boundaries', () => {
    it('uses zero offset by default', () => {
      const withZero = computePlacement(anchor, popup, { side: 'top', offset: 0 }, viewport)
      const withDefault = computePlacement(anchor, popup, { side: 'top' }, viewport)
      expect(withDefault.x).toBe(withZero.x)
      expect(withDefault.y).toBe(withZero.y)
      expect(withDefault.y).toBe(300)
    })

    it('pushes popup away with a large offset', () => {
      const result = computePlacement(anchor, popup, { side: 'top', offset: 100 }, viewport)
      expect(result.y).toBe(200)
    })

    it('keeps popup inside viewportPadding', () => {
      const result = computePlacement(anchor, popup, { side: 'top', offset: 0, viewportPadding: 50 }, viewport)
      expect(result.x).toBeGreaterThanOrEqual(50)
      expect(result.y).toBeGreaterThanOrEqual(50)
      expect(result.x + popup.width).toBeLessThanOrEqual(950)
      expect(result.y + popup.height).toBeLessThanOrEqual(950)
    })

    it('triggers flip when padding consumes the preferred side', () => {
      const nearTop: Rect = { x: 400, y: 80, width: 200, height: 100 }
      const result = computePlacement(nearTop, popup, { side: 'top', offset: 10, viewportPadding: 80 }, viewport)
      expect(result.actualSide).toBe('bottom')
    })
  })

  describe('zero-size anchor', () => {
    const pointAnchor: Rect = { x: 500, y: 500, width: 0, height: 0 }

    it('places popup above a point anchor', () => {
      const result = computePlacement(pointAnchor, popup, { side: 'top', offset: 10 }, viewport)
      expect(result.x).toBe(400)
      expect(result.y).toBe(390)
      expect(result.actualSide).toBe('top')
    })

    it('places popup below a point anchor', () => {
      const result = computePlacement(pointAnchor, popup, { side: 'bottom', offset: 10 }, viewport)
      expect(result.x).toBe(400)
      expect(result.y).toBe(510)
      expect(result.actualSide).toBe('bottom')
    })

    it('places popup to the left of a point anchor', () => {
      const result = computePlacement(pointAnchor, popup, { side: 'left', offset: 10 }, viewport)
      expect(result.x).toBe(290)
      expect(result.y).toBe(450)
      expect(result.actualSide).toBe('left')
    })

    it('places popup to the right of a point anchor', () => {
      const result = computePlacement(pointAnchor, popup, { side: 'right', offset: 10 }, viewport)
      expect(result.x).toBe(510)
      expect(result.y).toBe(450)
      expect(result.actualSide).toBe('right')
    })

    it('flips a point anchor when space is insufficient', () => {
      const nearTopPoint: Rect = { x: 500, y: 20, width: 0, height: 0 }
      const result = computePlacement(nearTopPoint, popup, { side: 'top', offset: 10, viewportPadding: 20 }, viewport)
      expect(result.actualSide).toBe('bottom')
      expect(result.y).toBe(30)
    })
  })
})

describe('unionRect', () => {
  it('returns the bounding box of two rects', () => {
    const u = unionRect(
      { x: 100, y: 0, width: 120, height: 32 },
      { x: 100, y: 32, width: 180, height: 220 },
    )
    expect(u).toEqual({ x: 100, y: 0, width: 180, height: 252 })
  })

  it('unions a zero-size rect as its origin point (call sites guard zero-size avoid rects)', () => {
    expect(unionRect({ x: 5, y: 6, width: 10, height: 12 }, { x: 8, y: 10, width: 0, height: 0 }))
      .toEqual({ x: 5, y: 6, width: 10, height: 12 })
  })
})

describe('computePlacement with an avoided (union) rect', () => {
  const card = { width: 360, height: 200 }
  const viewport = { width: 1920, height: 1080 }

  it('places the card beside the target+menu union, clear of the menu', () => {
    const trigger = { x: 100, y: 0, width: 120, height: 32 }
    const menu = { x: 100, y: 32, width: 180, height: 220 }
    const result = computePlacement(unionRect(trigger, menu), card, {
      side: 'right',
      align: 'center',
      offset: 8,
      viewportPadding: 8,
    }, viewport)
    // Card's left edge sits right of the menu (union right = 280).
    expect(result.x).toBeGreaterThanOrEqual(280 + 8)
    // Card is vertically centered on the union, never inside it.
    expect(result.y).toBeGreaterThan(0)
    expect(result.y).toBeLessThan(252)
  })

  it('falls back to the opposite side when the right side does not fit', () => {
    const trigger = { x: 1700, y: 0, width: 120, height: 32 }
    const menu = { x: 1700, y: 32, width: 180, height: 220 }
    const result = computePlacement(unionRect(trigger, menu), card, {
      side: 'right',
      align: 'center',
      offset: 8,
      viewportPadding: 8,
    }, viewport)
    expect(result.actualSide).toBe('left')
    // Card's right edge sits left of the union (union left = 1700).
    expect(result.x + card.width).toBeLessThanOrEqual(1700 - 8)
  })
})
