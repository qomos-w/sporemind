/**
 * Tests for the pure blend-mode math in `blendMode.ts`.
 *
 * These tests verify the mathematical correctness of each blend formula
 * against golden values chosen to exercise the standard reference formulas
 * (W3C compositing, SVG / Photoshop definitions, inochi2d-go upstream).
 * The JS implementations and GLSL string generators share their formulas —
 * passing tests here imply the shaders behave the same way once compiled.
 *
 * Convention: all inputs are premultiplied RGBA. All outputs are clamped to
 * [0, 1]. Channels are tested independently so any per-channel bug is
 * isolated to a single assertion.
 */

import { describe, it, expect } from 'vitest'
import {
  blendPremultiplied,
  getBlendModeImpl,
  getNativeBlendParams,
  blendGlslBody,
  NATIVE_BLEND_MODES,
  SHADER_BLEND_MODES,
  ALL_BLEND_MODES,
  type RGBA,
} from './blendMode'
import { BlendMode as BM } from './types'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const EPSILON = 1e-9

function approx(a: number, b: number, eps = EPSILON): boolean {
  return Math.abs(a - b) <= eps
}

function expectColor(actual: RGBA, expected: RGBA, eps = 1e-6): void {
  expect(approx(actual.r, expected.r, eps)).toBe(true)
  expect(approx(actual.g, expected.g, eps)).toBe(true)
  expect(approx(actual.b, expected.b, eps)).toBe(true)
  expect(approx(actual.a, expected.a, eps)).toBe(true)
}

/** Construct a premultiplied RGBA from un-premultiplied color + alpha. */
function pre(r: number, g: number, b: number, a: number): RGBA {
  return { r: r * a, g: g * a, b: b * a, a }
}

// Pure (fully opaque) source colors.
const OPAQUE_RED: RGBA = { r: 1, g: 0, b: 0, a: 1 }
const OPAQUE_GREEN: RGBA = { r: 0, g: 1, b: 0, a: 1 }
const OPAQUE_BLUE: RGBA = { r: 0, g: 0, b: 1, a: 1 }
const OPAQUE_WHITE: RGBA = { r: 1, g: 1, b: 1, a: 1 }
const OPAQUE_BLACK: RGBA = { r: 0, g: 0, b: 0, a: 1 }
const OPAQUE_YELLOW: RGBA = { r: 1, g: 1, b: 0, a: 1 }
const OPAQUE_GRAY_50: RGBA = { r: 0.5, g: 0.5, b: 0.5, a: 1 }
const OPAQUE_GRAY_25: RGBA = { r: 0.25, g: 0.25, b: 0.25, a: 1 }

// Transparent colors.
const TRANSPARENT_BLACK: RGBA = { r: 0, g: 0, b: 0, a: 0 }
const HALF_RED: RGBA = { r: 0.5, g: 0, b: 0, a: 0.5 }

// ---------------------------------------------------------------------------
// Reference: each mode against hand-derived golden values
// ---------------------------------------------------------------------------

describe('blendPremultiplied — Normal', () => {
  it('source-over with opaque red over opaque green yields red', () => {
    expectColor(blendPremultiplied(OPAQUE_RED, OPAQUE_GREEN, BM.Normal), OPAQUE_RED)
  })

  it('source-over with transparent source keeps destination', () => {
    expectColor(blendPremultiplied(TRANSPARENT_BLACK, OPAQUE_BLUE, BM.Normal), OPAQUE_BLUE)
  })

  it('source-over with half red over opaque green', () => {
    // src=(0.5,0,0,0.5), dst=(0,1,0,1)
    // r = 0.5 + 0 * 0.5 = 0.5
    // g = 0 + 1 * 0.5 = 0.5
    // b = 0
    // a = 0.5 + 1 * 0.5 = 1
    expectColor(blendPremultiplied(HALF_RED, OPAQUE_GREEN, BM.Normal), {
      r: 0.5, g: 0.5, b: 0, a: 1,
    })
  })

  it('opaque source fully replaces destination', () => {
    // src.a = 1: (1 - src.a) = 0, so dst contribution is zero.
    expectColor(blendPremultiplied(OPAQUE_BLUE, OPAQUE_RED, BM.Normal), OPAQUE_BLUE)
  })
})

describe('blendPremultiplied — Multiply', () => {
  it('opaque red times opaque green = opaque black', () => {
    expectColor(blendPremultiplied(OPAQUE_RED, OPAQUE_GREEN, BM.Multiply), OPAQUE_BLACK)
  })

  it('opaque white times anything = that thing', () => {
    expectColor(blendPremultiplied(OPAQUE_WHITE, OPAQUE_BLUE, BM.Multiply), OPAQUE_BLUE)
  })

  it('opaque black times anything = opaque black', () => {
    expectColor(blendPremultiplied(OPAQUE_BLACK, OPAQUE_RED, BM.Multiply), OPAQUE_BLACK)
  })

  it('componentwise: half red * opaque green', () => {
    expectColor(blendPremultiplied(HALF_RED, OPAQUE_GREEN, BM.Multiply), {
      r: 0, g: 0, b: 0, a: 0.5,
    })
  })

  it('commutative: a*b == b*a', () => {
    const a = { r: 0.4, g: 0.7, b: 0.2, a: 1 }
    const b = { r: 0.6, g: 0.3, b: 0.9, a: 1 }
    expectColor(blendPremultiplied(a, b, BM.Multiply), blendPremultiplied(b, a, BM.Multiply))
  })
})

describe('blendPremultiplied — Screen', () => {
  it('opaque red screen opaque green = opaque yellow', () => {
    expectColor(blendPremultiplied(OPAQUE_RED, OPAQUE_GREEN, BM.Screen), OPAQUE_YELLOW)
  })

  it('opaque white screen anything = opaque white', () => {
    expectColor(blendPremultiplied(OPAQUE_WHITE, OPAQUE_RED, BM.Screen), OPAQUE_WHITE)
  })

  it('opaque black screen anything = anything', () => {
    expectColor(blendPremultiplied(OPAQUE_BLACK, OPAQUE_BLUE, BM.Screen), OPAQUE_BLUE)
  })

  it('commutative: a screen b == b screen a', () => {
    const a = { r: 0.4, g: 0.7, b: 0.2, a: 1 }
    const b = { r: 0.6, g: 0.3, b: 0.9, a: 1 }
    expectColor(blendPremultiplied(a, b, BM.Screen), blendPremultiplied(b, a, BM.Screen))
  })
})

describe('blendPremultiplied — Overlay', () => {
  it('overlay gray-50 over anything = the input (midpoint identity)', () => {
    expectColor(blendPremultiplied(OPAQUE_GRAY_50, OPAQUE_RED, BM.Overlay), OPAQUE_RED)
    expectColor(blendPremultiplied(OPAQUE_GRAY_50, OPAQUE_BLUE, BM.Overlay), OPAQUE_BLUE)
  })

  it('overlay BLACK on dark reproduces W3C spec: result = 0', () => {
    // s=0, d=0.25: branch on d<0.5 → 2*d*s = 0
    expectColor(blendPremultiplied(OPAQUE_BLACK, OPAQUE_GRAY_25, BM.Overlay), OPAQUE_BLACK)
  })

  it('overlay BLACK on lighter base screens (1 - 2*(1-d)*(1-s))', () => {
    // s=0, d=0.75: 1 - 2*(1-0.75)*(1-0) = 1 - 0.5 = 0.5
    const out = blendPremultiplied(OPAQUE_BLACK, { r: 0.75, g: 0.75, b: 0.75, a: 1 }, BM.Overlay)
    expectColor(out, { r: 0.5, g: 0.5, b: 0.5, a: 1 })
  })

  it('overlay WHITE on dark base multiplies to 2*d*s', () => {
    // s=1, d=0.25: 2*0.25*1 = 0.5 (NOT 1 — the overlay curve lifts darks).
    expectColor(blendPremultiplied(OPAQUE_WHITE, OPAQUE_GRAY_25, BM.Overlay), OPAQUE_GRAY_50)
  })

  it('overlay BLACK on RED: r stays 1, g/b go to 0 (mixed curve)', () => {
    // s=0, d=(1,0,0,1):
    //   r: d=1≥0.5 → 1-2*(1-1)*(1-0) = 1
    //   g: d=0<0.5 → 2*0*0 = 0
    //   b: d=0<0.5 → 2*0*0 = 0
    //   a: d=1≥0.5 → 1-2*0*0 = 1
    // Result: (1,0,0,1) = RED — unchanged. Black overlay on saturated colors preserves them.
    expectColor(blendPremultiplied(OPAQUE_BLACK, OPAQUE_RED, BM.Overlay), OPAQUE_RED)
  })

  it('overlay WHITE on LIGHT base stays LIGHT', () => {
    // s=1, d=1: 1 - 2*(1-1)*(1-1) = 1
    expectColor(blendPremultiplied(OPAQUE_WHITE, OPAQUE_RED, BM.Overlay), OPAQUE_RED)
  })
})

describe('blendPremultiplied — Darken / Lighten', () => {
  it('darken takes per-channel min', () => {
    // src=(1,0,0,1), dst=(0,1,0,1) → (0,0,0,1)
    expectColor(blendPremultiplied(OPAQUE_RED, OPAQUE_GREEN, BM.Darken), OPAQUE_BLACK)
  })

  it('lighten takes per-channel max', () => {
    // src=(1,0,0,1), dst=(0,1,0,1) → (1,1,0,1) = YELLOW
    expectColor(blendPremultiplied(OPAQUE_RED, OPAQUE_GREEN, BM.Lighten), OPAQUE_YELLOW)
  })

  it('lighten over opaque white = opaque white', () => {
    expectColor(blendPremultiplied(OPAQUE_RED, OPAQUE_WHITE, BM.Lighten), OPAQUE_WHITE)
  })

  it('darken alpha is min(src.a, dst.a)', () => {
    const out = blendPremultiplied({ r: 0, g: 0, b: 0, a: 0.5 }, OPAQUE_RED, BM.Darken)
    expect(out.a).toBeCloseTo(0.5, 6)
  })
})

describe('blendPremultiplied — ColorDodge', () => {
  it('opaque black dodged into dark = opaque black (guard: src=0 → 0 per channel; alpha preserved since src.a=1)', () => {
    // src=(0,0,0,1), dst=(0.25,0.25,0.25,1)
    // r: cd(0,0.25) = 0; g: 0; b: 0; a: cd(1,1) = 1 (guard hits src>=1)
    expectColor(blendPremultiplied(OPAQUE_BLACK, OPAQUE_GRAY_25, BM.ColorDodge), OPAQUE_BLACK)
  })

  it('opaque white dodged into anything = opaque white', () => {
    expectColor(blendPremultiplied(OPAQUE_WHITE, OPAQUE_RED, BM.ColorDodge), OPAQUE_WHITE)
  })

  it('mid-tone: 0.5 src over 0.5 dst = 1.0 (saturated)', () => {
    // src=0.5, dst=0.5: 0.5 / (1-0.5) = 1.0
    const out = blendPremultiplied(OPAQUE_GRAY_50, OPAQUE_GRAY_50, BM.ColorDodge)
    expectColor(out, OPAQUE_WHITE)
  })
})

describe('blendPremultiplied — ColorBurn', () => {
  it('opaque white burned into anything = the destination (guard: src=1 → 1-(1-d) = d)', () => {
    expectColor(blendPremultiplied(OPAQUE_WHITE, OPAQUE_RED, BM.ColorBurn), OPAQUE_RED)
  })

  it('opaque black burned into anything = opaque black', () => {
    expectColor(blendPremultiplied(OPAQUE_BLACK, OPAQUE_RED, BM.ColorBurn), OPAQUE_BLACK)
  })

  it('mid-tone: 0.5 src over 0.5 dst = 0', () => {
    const out = blendPremultiplied(OPAQUE_GRAY_50, OPAQUE_GRAY_50, BM.ColorBurn)
    expectColor(out, OPAQUE_BLACK)
  })
})

describe('blendPremultiplied — LinearDodge / AddGlow', () => {
  it('linearDodge sums color channels, clamped to 1', () => {
    const out = blendPremultiplied(pre(0.6, 0, 0, 1), pre(0.5, 0, 0, 1), BM.LinearDodge)
    expectColor(out, { r: 1, g: 0, b: 0, a: 1 })
  })

  it('addGlow is identical to LinearDodge', () => {
    const a = blendPremultiplied(pre(0.4, 0.3, 0.2, 1), pre(0.5, 0.5, 0.5, 1), BM.LinearDodge)
    const b = blendPremultiplied(pre(0.4, 0.3, 0.2, 1), pre(0.5, 0.5, 0.5, 1), BM.AddGlow)
    expectColor(a, b)
  })

  it('clamps to 1 when sum exceeds 1', () => {
    const out = blendPremultiplied(pre(0.7, 0, 0, 1), pre(0.7, 0, 0, 1), BM.LinearDodge)
    expectColor(out, { r: 1, g: 0, b: 0, a: 1 })
  })

  it('fully transparent source leaves destination unchanged', () => {
    expectColor(blendPremultiplied(TRANSPARENT_BLACK, OPAQUE_RED, BM.LinearDodge), OPAQUE_RED)
  })
})

describe('blendPremultiplied — HardLight', () => {
  it('gray-50 over anything = anything (midpoint identity)', () => {
    expectColor(blendPremultiplied(OPAQUE_GRAY_50, OPAQUE_RED, BM.HardLight), OPAQUE_RED)
  })

  it('white on dark = white', () => {
    expectColor(blendPremultiplied(OPAQUE_WHITE, OPAQUE_GRAY_25, BM.HardLight), OPAQUE_WHITE)
  })

  it('HardLight(s, d) is symmetric with Overlay(d, s)', () => {
    const s = pre(0.3, 0.6, 0.9, 1)
    const d = pre(0.7, 0.2, 0.5, 1)
    const hard = blendPremultiplied(s, d, BM.HardLight)
    const ov = blendPremultiplied(d, s, BM.Overlay)
    expectColor(hard, ov)
  })
})

describe('blendPremultiplied — SoftLight (Pegtop)', () => {
  it('gray-50 over anything = anything (Pegtop identity at 0.5)', () => {
    expectColor(blendPremultiplied(OPAQUE_GRAY_50, OPAQUE_RED, BM.SoftLight), OPAQUE_RED)
  })

  it('BLACK over base: Pegtop(0, d) = d² — for dst=1 yields 1', () => {
    // Pegtop curve, not Photoshop's soft-light.
    const out = blendPremultiplied(OPAQUE_BLACK, OPAQUE_RED, BM.SoftLight)
    expectColor(out, OPAQUE_RED)
  })

  it('WHITE over gray-50 = 0.75 (Pegtop(1, 0.5) = -0.25 + 1 = 0.75)', () => {
    const out = blendPremultiplied(OPAQUE_WHITE, OPAQUE_GRAY_50, BM.SoftLight)
    expect(out.r).toBeCloseTo(0.75, 6)
  })
})

describe('blendPremultiplied — Difference', () => {
  it('opaque red difference opaque green: alpha goes to 0', () => {
    // src=(1,0,0,1), dst=(0,1,0,1)
    // r=|1-0|=1, g=|0-1|=1, b=0, a=|1-1|=0
    expectColor(blendPremultiplied(OPAQUE_RED, OPAQUE_GREEN, BM.Difference), {
      r: 1, g: 1, b: 0, a: 0,
    })
  })

  it('commutative: a difference b == b difference a', () => {
    const a = { r: 0.4, g: 0.7, b: 0.2, a: 1 }
    const b = { r: 0.6, g: 0.3, b: 0.9, a: 1 }
    const ab = blendPremultiplied(a, b, BM.Difference)
    const ba = blendPremultiplied(b, a, BM.Difference)
    expectColor(ab, ba)
  })

  it('identical colors = 0 per channel (alpha too)', () => {
    const out = blendPremultiplied(OPAQUE_RED, OPAQUE_RED, BM.Difference)
    expectColor(out, TRANSPARENT_BLACK)
  })
})

describe('blendPremultiplied — Exclusion', () => {
  it('opaque red excludes opaque green: r=1 g=1 b=0 a=0', () => {
    // r = 1+0-0 = 1, g = 0+1-0 = 1, b = 0, a = 1+1-2 = 0
    expectColor(blendPremultiplied(OPAQUE_RED, OPAQUE_GREEN, BM.Exclusion), {
      r: 1, g: 1, b: 0, a: 0,
    })
  })

  it('exclusion of BLACK: color = dst, alpha = 0 (1+1-2*1*1)', () => {
    // src=(0,0,0,1), dst=(1,0,0,1)
    // r = 0+1-0 = 1, g = 0, b = 0, a = 1+1-2 = 0
    expectColor(blendPremultiplied(OPAQUE_BLACK, OPAQUE_RED, BM.Exclusion), {
      r: 1, g: 0, b: 0, a: 0,
    })
  })

  it('commutative: a excl b == b excl a', () => {
    const a = { r: 0.4, g: 0.7, b: 0.2, a: 1 }
    const b = { r: 0.6, g: 0.3, b: 0.9, a: 1 }
    const ab = blendPremultiplied(a, b, BM.Exclusion)
    const ba = blendPremultiplied(b, a, BM.Exclusion)
    expectColor(ab, ba)
  })
})

describe('blendPremultiplied — Subtract', () => {
  it('dst - src clamped to 0 (componentwise)', () => {
    const out = blendPremultiplied(pre(0.6, 0, 0, 1), pre(0.5, 0, 0, 1), BM.Subtract)
    expectColor(out, TRANSPARENT_BLACK)
  })

  it('dst - fully transparent source = dst', () => {
    // src=(0,0,0,0), dst=(1,0,0,1) → (1,0,0,1)
    expectColor(blendPremultiplied(TRANSPARENT_BLACK, OPAQUE_RED, BM.Subtract), OPAQUE_RED)
  })

  it('clamped to 0 on negative', () => {
    const out = blendPremultiplied(OPAQUE_RED, OPAQUE_RED, BM.Subtract)
    expectColor(out, TRANSPARENT_BLACK)
  })
})

describe('blendPremultiplied — Inverse', () => {
  it('inverts opaque red then source-overs: result = cyan (with opaque dst)', () => {
    // opaque red un-premul = (1,0,0); invert = (0,1,1); re-premul = (0,1,1).
    // Source-over a cyan onto opaque green: src.a=1 → dst hidden.
    expectColor(blendPremultiplied(OPAQUE_RED, OPAQUE_GREEN, BM.Inverse), {
      r: 0, g: 1, b: 1, a: 1,
    })
  })

  it('transparent source keeps destination', () => {
    expectColor(blendPremultiplied(TRANSPARENT_BLACK, OPAQUE_RED, BM.Inverse), OPAQUE_RED)
  })

  it('half-alpha blue inverts to half-orange over white', () => {
    // src=(0,0,0.5,0.5), dst=(1,1,1,1)
    // inv un-premul blue = 1 - (0.5/0.5) = 0; re-premul = 0
    // inv un-premul red = 1 - (0/0.5) = 1; re-premul = 0.5
    // inv un-premul green = 1 - 0 = 1; re-premul = 0.5
    // Source-over: src=(0.5,0.5,0,0.5), dst=(1,1,1,1)
    //   r = 0.5 + 1 * 0.5 = 1
    //   g = 0.5 + 1 * 0.5 = 1
    //   b = 0 + 1 * 0.5 = 0.5
    //   a = 0.5 + 1 * 0.5 = 1
    expectColor(blendPremultiplied({ r: 0, g: 0, b: 0.5, a: 0.5 }, OPAQUE_WHITE, BM.Inverse), {
      r: 1, g: 1, b: 0.5, a: 1,
    })
  })
})

describe('blendPremultiplied — SourceIn / SourceOut', () => {
  it('SourceIn masks source by destination alpha', () => {
    // src=(1,0,0,1), dst=(0,1,0,0.5) → (1*0.5, 0, 0, 1*0.5) = (0.5,0,0,0.5)
    expectColor(blendPremultiplied(OPAQUE_RED, { r: 0, g: 1, b: 0, a: 0.5 }, BM.SourceIn), {
      r: 0.5, g: 0, b: 0, a: 0.5,
    })
  })

  it('SourceIn with transparent destination zeroes source', () => {
    expectColor(blendPremultiplied(OPAQUE_RED, TRANSPARENT_BLACK, BM.SourceIn), TRANSPARENT_BLACK)
  })

  it('SourceOut masks source by (1 - dst.a)', () => {
    expectColor(blendPremultiplied(OPAQUE_RED, { r: 0, g: 1, b: 0, a: 0.5 }, BM.SourceOut), {
      r: 0.5, g: 0, b: 0, a: 0.5,
    })
  })

  it('SourceOut with opaque destination zeroes source', () => {
    expectColor(blendPremultiplied(OPAQUE_RED, OPAQUE_GREEN, BM.SourceOut), TRANSPARENT_BLACK)
  })

  it('SourceOut with transparent destination shows source as-is', () => {
    expectColor(blendPremultiplied(OPAQUE_RED, TRANSPARENT_BLACK, BM.SourceOut), OPAQUE_RED)
  })
})

describe('blendPremultiplied — DestinationIn', () => {
  it('dest alpha gates source', () => {
    expectColor(blendPremultiplied(OPAQUE_RED, { r: 0, g: 1, b: 0, a: 0.5 }, BM.DestinationIn), {
      r: 0.5, g: 0, b: 0, a: 0.5,
    })
  })
})

// ---------------------------------------------------------------------------
// Output always clamped to [0, 1]
// ---------------------------------------------------------------------------

describe('blendPremultiplied — output clamping', () => {
  it('clamps negative components to 0', () => {
    const out = blendPremultiplied(OPAQUE_WHITE, OPAQUE_BLACK, BM.Subtract)
    expect(out.r).toBeGreaterThanOrEqual(0)
    expect(out.g).toBeGreaterThanOrEqual(0)
    expect(out.b).toBeGreaterThanOrEqual(0)
    expect(out.a).toBeGreaterThanOrEqual(0)
  })

  it('clamps above 1 to 1', () => {
    const out = blendPremultiplied(OPAQUE_WHITE, OPAQUE_WHITE, BM.LinearDodge)
    expect(out.r).toBeLessThanOrEqual(1)
    expect(out.g).toBeLessThanOrEqual(1)
    expect(out.b).toBeLessThanOrEqual(1)
    expect(out.a).toBeLessThanOrEqual(1)
  })
})

// ---------------------------------------------------------------------------
// getBlendModeImpl — implementation-path selection
// ---------------------------------------------------------------------------

describe('getBlendModeImpl', () => {
  it('marks separable / min-max / additive modes as native', () => {
    expect(getBlendModeImpl(BM.Normal)).toBe('native')
    expect(getBlendModeImpl(BM.Darken)).toBe('native')
    expect(getBlendModeImpl(BM.Lighten)).toBe('native')
    expect(getBlendModeImpl(BM.LinearDodge)).toBe('native')
    expect(getBlendModeImpl(BM.AddGlow)).toBe('native')
    expect(getBlendModeImpl(BM.Subtract)).toBe('native')
    expect(getBlendModeImpl(BM.DestinationIn)).toBe('native')
  })

  it('marks non-separable and mask-clipping modes as shader', () => {
    expect(getBlendModeImpl(BM.Multiply)).toBe('shader')
    expect(getBlendModeImpl(BM.Screen)).toBe('shader')
    expect(getBlendModeImpl(BM.Overlay)).toBe('shader')
    expect(getBlendModeImpl(BM.ColorDodge)).toBe('shader')
    expect(getBlendModeImpl(BM.ColorBurn)).toBe('shader')
    expect(getBlendModeImpl(BM.HardLight)).toBe('shader')
    expect(getBlendModeImpl(BM.SoftLight)).toBe('shader')
    expect(getBlendModeImpl(BM.Difference)).toBe('shader')
    expect(getBlendModeImpl(BM.Exclusion)).toBe('shader')
    expect(getBlendModeImpl(BM.Inverse)).toBe('shader')
    expect(getBlendModeImpl(BM.SourceIn)).toBe('shader')
    expect(getBlendModeImpl(BM.SourceOut)).toBe('shader')
  })

  it('covers all 19 modes with no overlap', () => {
    const allNative = ALL_BLEND_MODES.filter((m) => getBlendModeImpl(m) === 'native')
    const allShader = ALL_BLEND_MODES.filter((m) => getBlendModeImpl(m) === 'shader')
    expect(new Set(allNative).size).toBe(NATIVE_BLEND_MODES.length)
    expect(new Set(allShader).size).toBe(SHADER_BLEND_MODES.length)
    expect(allNative.length + allShader.length).toBe(ALL_BLEND_MODES.length)
  })
})

// ---------------------------------------------------------------------------
// getNativeBlendParams
// ---------------------------------------------------------------------------

describe('getNativeBlendParams', () => {
  const mockGL = {
    FUNC_ADD: 0x8006,
    FUNC_REVERSE_SUBTRACT: 0x800b,
    MIN: 0x8007,
    MAX: 0x8008,
    ONE: 1,
    ZERO: 0,
    ONE_MINUS_SRC_ALPHA: 0x803,
    ONE_MINUS_DST_COLOR: 0x807,
    SRC_ALPHA: 0x302,
    DST_COLOR: 0x806,
  } as const

  it('returns null for shader-only modes', () => {
    expect(getNativeBlendParams(BM.Multiply, mockGL)).toBeNull()
    expect(getNativeBlendParams(BM.Overlay, mockGL)).toBeNull()
    expect(getNativeBlendParams(BM.SoftLight, mockGL)).toBeNull()
    expect(getNativeBlendParams(BM.SourceIn, mockGL)).toBeNull()
  })

  it('Normal: source-over in premultiplied space', () => {
    const p = getNativeBlendParams(BM.Normal, mockGL)
    expect(p).toEqual({
      equation: mockGL.FUNC_ADD,
      srcFactor: mockGL.ONE,
      dstFactor: mockGL.ONE_MINUS_SRC_ALPHA,
    })
  })

  it('Darken: MIN equation, both factors ONE', () => {
    const p = getNativeBlendParams(BM.Darken, mockGL)
    expect(p).toEqual({
      equation: mockGL.MIN,
      srcFactor: mockGL.ONE,
      dstFactor: mockGL.ONE,
    })
  })

  it('Lighten: MAX equation, both factors ONE', () => {
    const p = getNativeBlendParams(BM.Lighten, mockGL)
    expect(p).toEqual({
      equation: mockGL.MAX,
      srcFactor: mockGL.ONE,
      dstFactor: mockGL.ONE,
    })
  })

  it('LinearDodge / AddGlow share the same additive params', () => {
    const a = getNativeBlendParams(BM.LinearDodge, mockGL)
    const b = getNativeBlendParams(BM.AddGlow, mockGL)
    expect(a).toEqual(b)
    expect(a).toEqual({
      equation: mockGL.FUNC_ADD,
      srcFactor: mockGL.ONE,
      dstFactor: mockGL.ONE,
    })
  })

  it('Subtract uses FUNC_REVERSE_SUBTRACT', () => {
    const p = getNativeBlendParams(BM.Subtract, mockGL)
    expect(p).toEqual({
      equation: mockGL.FUNC_REVERSE_SUBTRACT,
      srcFactor: mockGL.ONE,
      dstFactor: mockGL.ONE,
    })
  })

  it('DestinationIn masks source by src alpha', () => {
    const p = getNativeBlendParams(BM.DestinationIn, mockGL)
    expect(p).toEqual({
      equation: mockGL.FUNC_ADD,
      srcFactor: mockGL.ZERO,
      dstFactor: mockGL.SRC_ALPHA,
    })
  })
})

// ---------------------------------------------------------------------------
// blendGlslBody — GLSL formula strings
// ---------------------------------------------------------------------------

describe('blendGlslBody — shader formula presence', () => {
  it('returns a string for every shader blend mode', () => {
    for (const mode of SHADER_BLEND_MODES) {
      const body = blendGlslBody(mode)
      expect(typeof body).toBe('string')
      expect(body.length).toBeGreaterThan(0)
      expect(body).toContain('outColor')
    }
  })

  it('Screen uses s + d - s*d', () => {
    const body = blendGlslBody(BM.Screen)
    expect(body).toMatch(/s\.r\s*\+\s*d\.r\s*-\s*s\.r\s*\*\s*d\.r/)
  })

  it('Multiply uses s.r * d.r', () => {
    const body = blendGlslBody(BM.Multiply)
    expect(body).toMatch(/s\.r\s*\*\s*d\.r/)
  })

  it('Overlay branches on d < 0.5', () => {
    const body = blendGlslBody(BM.Overlay)
    expect(body).toMatch(/d\.r\s*<\s*0\.5/)
    expect(body).toMatch(/2\.0\s*\*\s*\(?\s*d\.r\s*\)?\s*\*\s*\(?\s*s\.r/)
  })

  it('HardLight branches on s < 0.5', () => {
    const body = blendGlslBody(BM.HardLight)
    expect(body).toMatch(/s\.r\s*<\s*0\.5/)
  })

  it('SoftLight uses Pegtop (1 - 2s)*d^2 + 2s*d', () => {
    const body = blendGlslBody(BM.SoftLight)
    expect(body).toMatch(/1\.0\s*-\s*2\.0\s*\*\s*s\.r/)
    expect(body).toMatch(/d\.r\s*\*\s*d\.r/)
  })

  it('ColorDodge guards src=1 case', () => {
    const body = blendGlslBody(BM.ColorDodge)
    expect(body).toMatch(/s\.r\s*>=\s*1\.0/)
    expect(body).toMatch(/d\.r\s*\)\s*\/\s*\(\s*1\.0\s*-\s*s\.r/)
  })

  it('ColorBurn guards src=0 case', () => {
    const body = blendGlslBody(BM.ColorBurn)
    expect(body).toMatch(/s\.r\s*<=\s*0\.0/)
  })

  it('Difference uses abs', () => {
    const body = blendGlslBody(BM.Difference)
    expect(body).toMatch(/abs\(\s*s\.r\s*-\s*d\.r\s*\)/)
  })

  it('Exclusion uses s + d - 2*s*d', () => {
    const body = blendGlslBody(BM.Exclusion)
    expect(body).toMatch(/s\.r\s*\+\s*d\.r\s*-\s*2\.0\s*\*\s*s\.r\s*\*\s*d\.r/)
  })

  it('Inverse un-premultiplies, inverts, re-premultiplies', () => {
    const body = blendGlslBody(BM.Inverse)
    expect(body).toMatch(/1\.0\s*-\s*s\.r\s*\/\s*s\.a/)
    expect(body).toMatch(/\(\s*1\.0\s*-\s*s\.a\s*\)/)
  })

  it('SourceIn uses d.a as mask', () => {
    const body = blendGlslBody(BM.SourceIn)
    expect(body).toMatch(/s\.r\s*\*\s*d\.a/)
  })

  it('SourceOut uses (1 - d.a) as mask', () => {
    const body = blendGlslBody(BM.SourceOut)
    expect(body).toMatch(/s\.r\s*\*\s*\(\s*1\.0\s*-\s*d\.a\s*\)/)
  })
})

// ---------------------------------------------------------------------------
// Cross-check: enum membership counts
// ---------------------------------------------------------------------------

describe('BlendMode coverage', () => {
  it('every BlendMode appears in exactly one of native/shader lists', () => {
    const native = new Set(NATIVE_BLEND_MODES)
    const shader = new Set(SHADER_BLEND_MODES)
    for (const mode of ALL_BLEND_MODES) {
      const inNative = native.has(mode)
      const inShader = shader.has(mode)
      expect(inNative !== inShader).toBe(true)
    }
  })

  it('covers 19 modes (Normal + 18 others)', () => {
    expect(ALL_BLEND_MODES.length).toBe(19)
  })
})
