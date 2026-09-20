/**
 * Pixel-level unit tests for mask operations.
 *
 * These tests exercise the pure mask logic in `mask.ts` without any WebGL
 * context, covering both non-nested and nested mask scenarios at the
 * individual-pixel level — exactly the acceptance criteria from the task card.
 */

import { describe, it, expect } from 'vitest'
import {
  applyMaskPixel,
  maskCompositePixel,
  maskPipelinePixel,
  nestedMaskPipelinePixel,
  createMaskStackState,
  maskStackPush,
  maskStackDefine,
  maskStackPop,
  maskStackDepth,
  maskStackActive,
  maskStackCurrentMode,
} from './mask'
import { MaskingMode as MM } from './types'
import type { RGBA } from './blendMode'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Fully opaque red in premultiplied space (r=1, a=1). */
const RED: RGBA = { r: 1, g: 0, b: 0, a: 1 }
/** Fully opaque green in premultiplied space. */
const GREEN: RGBA = { r: 0, g: 1, b: 0, a: 1 }
/** Fully transparent (empty). */
const TRANSPARENT: RGBA = { r: 0, g: 0, b: 0, a: 0 }
/** 50% opacity red in premultiplied space (r=0.5, a=0.5). */
const HALF_RED: RGBA = { r: 0.5, g: 0, b: 0, a: 0.5 }
/** Fully opaque blue. */
const BLUE: RGBA = { r: 0, g: 0, b: 1, a: 1 }
/** Half-opacity blue premultiplied. */
const HALF_BLUE: RGBA = { r: 0, g: 0, b: 0.5, a: 0.5 }

function approxEqual(actual: RGBA, expected: RGBA): void {
  expect(actual.r).toBeCloseTo(expected.r, 5)
  expect(actual.g).toBeCloseTo(expected.g, 5)
  expect(actual.b).toBeCloseTo(expected.b, 5)
  expect(actual.a).toBeCloseTo(expected.a, 5)
}

// ---------------------------------------------------------------------------
// applyMaskPixel — single-pixel mask application
// ---------------------------------------------------------------------------

describe('applyMaskPixel', () => {
  it('Mask mode: opaque mask preserves content', () => {
    const result = applyMaskPixel(RED, 1.0, MM.Mask)
    expect(result).toEqual(RED)
  })

  it('Mask mode: transparent mask zeroes content', () => {
    const result = applyMaskPixel(RED, 0.0, MM.Mask)
    expect(result).toEqual(TRANSPARENT)
  })

  it('Mask mode: 50% mask halves content', () => {
    const result = applyMaskPixel(RED, 0.5, MM.Mask)
    approxEqual(result, { r: 0.5, g: 0, b: 0, a: 0.5 })
  })

  it('Mask mode: halves already-premultiplied content', () => {
    const result = applyMaskPixel(HALF_RED, 0.5, MM.Mask)
    // 0.5 * 0.5 = 0.25 for both r and a
    approxEqual(result, { r: 0.25, g: 0, b: 0, a: 0.25 })
  })

  it('Dodge mode: opaque mask zeroes content (inverted)', () => {
    const result = applyMaskPixel(RED, 1.0, MM.Dodge)
    expect(result).toEqual(TRANSPARENT)
  })

  it('Dodge mode: transparent mask preserves content (inverted)', () => {
    const result = applyMaskPixel(RED, 0.0, MM.Dodge)
    expect(result).toEqual(RED)
  })

  it('Dodge mode: 50% mask halves content (inverted)', () => {
    const result = applyMaskPixel(RED, 0.5, MM.Dodge)
    // factor = 1 - 0.5 = 0.5
    approxEqual(result, { r: 0.5, g: 0, b: 0, a: 0.5 })
  })

  it('transparent content is unaffected by mask', () => {
    const result = applyMaskPixel(TRANSPARENT, 0.7, MM.Mask)
    expect(result).toEqual(TRANSPARENT)
  })
})

// ---------------------------------------------------------------------------
// maskCompositePixel — source-over compositing for masked content
// ---------------------------------------------------------------------------

describe('maskCompositePixel', () => {
  it('opaque masked content over transparent dst = masked content', () => {
    const result = maskCompositePixel(RED, TRANSPARENT)
    expect(result).toEqual(RED)
  })

  it('transparent masked content over dst = dst', () => {
    const result = maskCompositePixel(TRANSPARENT, GREEN)
    expect(result).toEqual(GREEN)
  })

  it('half-opacity content over opaque dst blends correctly', () => {
    const result = maskCompositePixel(HALF_RED, GREEN)
    // source-over: s + d*(1-s.a) = 0.5 + 0*(0.5) for r; 0 + 1*(0.5) for g
    approxEqual(result, { r: 0.5, g: 0.5, b: 0, a: 1 })
  })
})

// ---------------------------------------------------------------------------
// maskPipelinePixel — full non-nested mask pipeline
// ---------------------------------------------------------------------------

describe('maskPipelinePixel — non-nested mask', () => {
  it('Mask mode: opaque mask, content onto transparent canvas = content', () => {
    const result = maskPipelinePixel(RED, 1.0, MM.Mask, TRANSPARENT)
    expect(result).toEqual(RED)
  })

  it('Mask mode: transparent mask, content onto canvas = canvas only', () => {
    const result = maskPipelinePixel(RED, 0.0, MM.Mask, GREEN)
    expect(result).toEqual(GREEN)
  })

  it('Mask mode: 50% mask, opaque content onto transparent canvas', () => {
    const result = maskPipelinePixel(RED, 0.5, MM.Mask, TRANSPARENT)
    // masked = {0.5, 0, 0, 0.5}; composite over transparent = masked
    approxEqual(result, { r: 0.5, g: 0, b: 0, a: 0.5 })
  })

  it('Mask mode: 50% mask, opaque content onto opaque canvas', () => {
    const result = maskPipelinePixel(RED, 0.5, MM.Mask, GREEN)
    // masked = {0.5, 0, 0, 0.5}
    // composite: r = 0.5 + 0*0.5 = 0.5; g = 0 + 1*0.5 = 0.5; a = 0.5 + 1*0.5 = 1
    approxEqual(result, { r: 0.5, g: 0.5, b: 0, a: 1 })
  })

  it('Dodge mode: opaque mask, content onto canvas = canvas only (content dodged away)', () => {
    const result = maskPipelinePixel(RED, 1.0, MM.Dodge, GREEN)
    // factor = 1 - 1 = 0; masked = transparent; result = GREEN
    expect(result).toEqual(GREEN)
  })

  it('Dodge mode: transparent mask, content onto canvas = content composited', () => {
    const result = maskPipelinePixel(RED, 0.0, MM.Dodge, TRANSPARENT)
    // factor = 1 - 0 = 1; masked = RED; composite over transparent = RED
    expect(result).toEqual(RED)
  })

  it('Dodge mode: 50% mask, opaque content onto opaque canvas', () => {
    const result = maskPipelinePixel(RED, 0.5, MM.Dodge, GREEN)
    // factor = 1 - 0.5 = 0.5; masked = {0.5, 0, 0, 0.5}
    // composite: r = 0.5 + 0*0.5 = 0.5; g = 0 + 1*0.5 = 0.5; a = 1
    approxEqual(result, { r: 0.5, g: 0.5, b: 0, a: 1 })
  })

  it('transparent content with any mask = dst unchanged', () => {
    const result = maskPipelinePixel(TRANSPARENT, 0.5, MM.Mask, GREEN)
    expect(result).toEqual(GREEN)
  })
})

// ---------------------------------------------------------------------------
// nestedMaskPipelinePixel — nested mask scenarios
// ---------------------------------------------------------------------------

describe('nestedMaskPipelinePixel — nested mask', () => {
  it('both masks opaque: inner content appears on canvas', () => {
    // inner content = RED, inner mask = 1 (visible), outer content = transparent,
    // outer mask = 1 (visible), dst = transparent
    const result = nestedMaskPipelinePixel(
      RED, 1.0, MM.Mask,
      TRANSPARENT, 1.0, MM.Mask,
      TRANSPARENT,
    )
    // Inner: masked(RED, 1) = RED; composite over transparent = RED
    // Outer: masked(RED, 1) = RED; composite over transparent = RED
    approxEqual(result, RED)
  })

  it('outer mask opaque, inner mask transparent: inner content is masked away, outer content remains', () => {
    // inner content = RED, inner mask = 0 (hidden), outer content = BLUE,
    // outer mask = 1 (visible), dst = transparent
    const result = nestedMaskPipelinePixel(
      RED, 0.0, MM.Mask,
      BLUE, 1.0, MM.Mask,
      TRANSPARENT,
    )
    // Inner: masked(RED, 0) = transparent; composite over BLUE = BLUE
    // Outer: masked(BLUE, 1) = BLUE; composite over transparent = BLUE
    approxEqual(result, BLUE)
  })

  it('outer mask transparent, inner mask opaque: everything is masked away at outer level', () => {
    // inner content = RED, inner mask = 1, outer content = BLUE,
    // outer mask = 0 (hidden), dst = transparent
    const result = nestedMaskPipelinePixel(
      RED, 1.0, MM.Mask,
      BLUE, 0.0, MM.Mask,
      TRANSPARENT,
    )
    // Inner: masked(RED, 1) = RED; composite over BLUE
    //   = {0+0*0, 0+0*0, 0+1*0, 1+1*0} ... wait RED is opaque so:
    //   r = 1 + 0*(1-1) = 1; g = 0 + 0*(1-1) = 0; b = 0 + 1*(1-1) = 0; a = 1 + 1*0 = 1 → RED
    // Outer: masked(RED, 0) = transparent; composite over transparent = transparent
    approxEqual(result, TRANSPARENT)
  })

  it('both masks 50%: content is quartered (0.5 × 0.5)', () => {
    // inner content = RED (opaque), inner mask = 0.5, outer content = transparent,
    // outer mask = 0.5, dst = transparent
    const result = nestedMaskPipelinePixel(
      RED, 0.5, MM.Mask,
      TRANSPARENT, 0.5, MM.Mask,
      TRANSPARENT,
    )
    // Inner: masked(RED, 0.5) = {0.5, 0, 0, 0.5}; composite over transparent = {0.5, 0, 0, 0.5}
    // Outer: masked({0.5, 0, 0, 0.5}, 0.5) = {0.25, 0, 0, 0.25}; composite over transparent = same
    approxEqual(result, { r: 0.25, g: 0, b: 0, a: 0.25 })
  })

  it('inner Mask + outer Dodge: inner content visible where outer mask is transparent', () => {
    // inner content = RED, inner mask = 1 (visible via Mask), outer content = transparent,
    // outer mask = 1 (Dodge → factor 0 → everything dodged), dst = transparent
    const result = nestedMaskPipelinePixel(
      RED, 1.0, MM.Mask,
      TRANSPARENT, 1.0, MM.Dodge,
      TRANSPARENT,
    )
    // Inner: masked(RED, 1, Mask) = RED; composite over transparent = RED
    // Outer: masked(RED, 1, Dodge) = transparent; composite over transparent = transparent
    approxEqual(result, TRANSPARENT)
  })

  it('inner Dodge + outer Mask: inner visible only where inner mask transparent, outer clips', () => {
    // inner content = RED, inner mask = 1 (Dodge → 0 → hidden), outer content = BLUE,
    // outer mask = 1 (Mask → visible), dst = transparent
    const result = nestedMaskPipelinePixel(
      RED, 1.0, MM.Dodge,
      BLUE, 1.0, MM.Mask,
      TRANSPARENT,
    )
    // Inner: masked(RED, 1, Dodge) = transparent; composite over BLUE = BLUE
    // Outer: masked(BLUE, 1, Mask) = BLUE; composite over transparent = BLUE
    approxEqual(result, BLUE)
  })

  it('inner content adds to outer content, then both are masked', () => {
    // inner content = HALF_RED, inner mask = 1, outer content = HALF_BLUE,
    // outer mask = 1, dst = transparent
    const result = nestedMaskPipelinePixel(
      HALF_RED, 1.0, MM.Mask,
      HALF_BLUE, 1.0, MM.Mask,
      TRANSPARENT,
    )
    // Inner: masked(HALF_RED, 1) = HALF_RED; composite over HALF_BLUE:
    //   r = 0.5 + 0*(1-0.5) = 0.5
    //   g = 0 + 0*0.5 = 0
    //   b = 0 + 0.5*0.5 = 0.25
    //   a = 0.5 + 0.5*0.5 = 0.75
    // Outer: masked({0.5, 0, 0.25, 0.75}, 1) = same; composite over transparent = same
    approxEqual(result, { r: 0.5, g: 0, b: 0.25, a: 0.75 })
  })
})

// ---------------------------------------------------------------------------
// Mask stack state machine
// ---------------------------------------------------------------------------

describe('mask stack state machine', () => {
  it('starts empty', () => {
    const state = createMaskStackState()
    expect(maskStackDepth(state)).toBe(0)
    expect(maskStackActive(state)).toBe(false)
    expect(maskStackCurrentMode(state)).toBe(null)
  })

  it('push increases depth and sets mode', () => {
    const state = createMaskStackState()
    maskStackPush(state, MM.Mask)
    expect(maskStackDepth(state)).toBe(1)
    expect(maskStackActive(state)).toBe(true)
    expect(maskStackCurrentMode(state)).toBe(MM.Mask)
  })

  it('pop decreases depth and returns the entry', () => {
    const state = createMaskStackState()
    maskStackPush(state, MM.Dodge)
    const popped = maskStackPop(state)
    expect(popped).not.toBeNull()
    expect(popped!.maskMode).toBe(MM.Dodge)
    expect(maskStackDepth(state)).toBe(0)
  })

  it('pop on empty returns null', () => {
    const state = createMaskStackState()
    const popped = maskStackPop(state)
    expect(popped).toBeNull()
  })

  it('define marks the top level as having mask geometry', () => {
    const state = createMaskStackState()
    maskStackPush(state, MM.Mask)
    expect(state.stack[0]!.hasMaskGeometry).toBe(false)
    maskStackDefine(state)
    expect(state.stack[0]!.hasMaskGeometry).toBe(true)
  })

  it('supports nested push/pop (LIFO)', () => {
    const state = createMaskStackState()
    maskStackPush(state, MM.Mask)   // depth 1
    maskStackPush(state, MM.Dodge)  // depth 2
    expect(maskStackDepth(state)).toBe(2)
    expect(maskStackCurrentMode(state)).toBe(MM.Dodge)

    const inner = maskStackPop(state)
    expect(inner!.maskMode).toBe(MM.Dodge)
    expect(maskStackDepth(state)).toBe(1)
    expect(maskStackCurrentMode(state)).toBe(MM.Mask)

    const outer = maskStackPop(state)
    expect(outer!.maskMode).toBe(MM.Mask)
    expect(maskStackDepth(state)).toBe(0)
  })

  it('define only affects the top level in a nested stack', () => {
    const state = createMaskStackState()
    maskStackPush(state, MM.Mask)   // level 0
    maskStackPush(state, MM.Dodge)  // level 1
    maskStackDefine(state)
    expect(state.stack[1]!.hasMaskGeometry).toBe(true)
    expect(state.stack[0]!.hasMaskGeometry).toBe(false)
  })
})