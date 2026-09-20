/**
 * Pure blend-mode math for Inochi2D.
 *
 * The renderer has two paths for blending source fragments onto the canvas:
 *
 *  1. **Native** — drive the fixed-function blender via `gl.blendFunc`/
 *     `gl.blendEquation`. Works for separable / min-max / additive modes where
 *     the formula maps cleanly to a single blend equation + factor pair in
 *     premultiplied-alpha space.
 *
 *  2. **Shader** — render the source to a transient FBO, then composite via a
 *     fragment shader that reads both the source FBO and the current framebuffer
 *     texture and emits the per-pixel blend result. Required for non-separable
 *     formulas (Multiply, Screen, Overlay, ColorDodge, ColorBurn, HardLight,
 *     SoftLight, Difference, Exclusion, Inverse) and for the mask-clipping
 *     modes (SourceIn / SourceOut).
 *
 * Every formula below is implemented twice:
 *  - As a pure JS function operating on premultiplied RGBA tuples, so that
 *    correctness can be unit-tested without a GL context.
 *  - As a GLSL string generator for the WebGL renderer.
 *
 * Premultiplied-alpha convention (matches the existing spike renderer):
 *   - Fragment shader outputs `(rgb * a * opacity, a * opacity)` — i.e. RGB are
 *     already multiplied by alpha.
 *   - The destination framebuffer therefore stores premultiplied colors.
 *   - Blend formulas are written accordingly (e.g. Multiply is `s.r * d.r`,
 *     not `s.r/s.a * d.r/d.a`).
 *
 * Source: Inochi2D's `core/render/state.d` (BlendMode enum) and the upstream
 * inochi2d-gl blend-mode shaders, which spell out the same mix() / screen() /
 * overlay() / pegtop() functions adapted for premultiplied inputs.
 */

import { BlendMode as BM, type BlendMode } from './types'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Premultiplied RGBA color, channels in [0, 1]. */
export interface RGBA {
  r: number
  g: number
  b: number
  a: number
}

/** Categorization of a blend mode by which rendering path it uses. */
export type BlendModeImpl = 'native' | 'shader'

/** Native-blend parameters: equation + factor pair. */
export interface NativeBlendParams {
  /** WebGL blend equation constant (e.g. gl.FUNC_ADD, gl.MIN, gl.MAX). */
  equation: number
  /** Source factor (e.g. gl.ONE). */
  srcFactor: number
  /** Destination factor (e.g. gl.ONE_MINUS_SRC_ALPHA). */
  dstFactor: number
}

/** Minimal GL knob surface used by `getNativeBlendParams`. */
export interface GLBlendKnobs {
  FUNC_ADD: number
  FUNC_REVERSE_SUBTRACT: number
  MIN: number
  MAX: number
  ONE: number
  ZERO: number
  ONE_MINUS_SRC_ALPHA: number
  ONE_MINUS_DST_COLOR: number
  SRC_ALPHA: number
  DST_COLOR: number
}

// ---------------------------------------------------------------------------
// Premultiplied helpers
// ---------------------------------------------------------------------------

/** Clamp a value to [0, 1]. Used by the blend formulas to keep output sane. */
function clamp01(x: number): number {
  if (x < 0) return 0
  if (x > 1) return 1
  return x
}

/** Construct a fully-zero (transparent) color. */
export function rgbaZero(): RGBA {
  return { r: 0, g: 0, b: 0, a: 0 }
}

// ---------------------------------------------------------------------------
// Per-mode blend formulas (premultiplied)
// ---------------------------------------------------------------------------
//
// Each formula takes the candidate source pixel (s) and destination pixel (d)
// — both in premultiplied space — and returns the result color, also in
// premultiplied space. The convention is:
//   applyOpacity(s, srcOpacity) at the call site, then plug into the formula.
//
// Alpha is blended with the same formula as the color channels, EXCEPT for
// the few modes that explicitly specify a different alpha treatment
// (Normal uses standard source-over alpha; Subtract / Difference / etc. use
// the same componentwise formula for both color and alpha).
//
// All output is clamped to [0, 1] to avoid artifacts from rounding.

/** Normal: standard source-over in premultiplied space. */
function blendNormal(s: RGBA, d: RGBA): RGBA {
  return {
    r: s.r + d.r * (1 - s.a),
    g: s.g + d.g * (1 - s.a),
    b: s.b + d.b * (1 - s.a),
    a: s.a + d.a * (1 - s.a),
  }
}

/** Multiply: src * dst (premultiplied componentwise). */
function blendMultiply(s: RGBA, d: RGBA): RGBA {
  return {
    r: s.r * d.r,
    g: s.g * d.g,
    b: s.b * d.b,
    a: s.a * d.a,
  }
}

/** Screen: 1 - (1 - s)(1 - d). In premultiplied: s + d - s*d. */
function blendScreen(s: RGBA, d: RGBA): RGBA {
  return {
    r: s.r + d.r - s.r * d.r,
    g: s.g + d.g - s.g * d.g,
    b: s.b + d.b - s.b * d.b,
    a: s.a + d.a - s.a * d.a,
  }
}

/** Dst-split Overlay: HardLight on dst below 0.5, Screen otherwise. */
function blendOverlay(s: RGBA, d: RGBA): RGBA {
  return {
    r: overlayComponent(s.r, d.r),
    g: overlayComponent(s.g, d.g),
    b: overlayComponent(s.b, d.b),
    a: overlayComponent(s.a, d.a),
  }
}

function overlayComponent(s: number, d: number): number {
  // Standard overlay: 2*d*s if d < 0.5, else 1 - 2*(1-d)*(1-s).
  // In premultiplied space this maps directly to the same formula.
  if (d < 0.5) return 2 * d * s
  return 1 - 2 * (1 - d) * (1 - s)
}

/** Darken: min(src, dst). */
function blendDarken(s: RGBA, d: RGBA): RGBA {
  return {
    r: Math.min(s.r, d.r),
    g: Math.min(s.g, d.g),
    b: Math.min(s.b, d.b),
    a: Math.min(s.a, d.a),
  }
}

/** Lighten: max(src, dst). */
function blendLighten(s: RGBA, d: RGBA): RGBA {
  return {
    r: Math.max(s.r, d.r),
    g: Math.max(s.g, d.g),
    b: Math.max(s.b, d.b),
    a: Math.max(s.a, d.a),
  }
}

/** ColorDodge: dst / (1 - src). Guard src>=1 and src<=0 for stability. */
function blendColorDodge(s: RGBA, d: RGBA): RGBA {
  return {
    r: colorDodgeComponent(s.r, d.r),
    g: colorDodgeComponent(s.g, d.g),
    b: colorDodgeComponent(s.b, d.b),
    a: colorDodgeComponent(s.a, d.a),
  }
}

function colorDodgeComponent(s: number, d: number): number {
  if (s >= 1) return 1 // saturate
  if (s <= 0) return 0 // no contribution
  return d / (1 - s)
}

/** LinearDodge / AddGlow: src + dst, clamped. */
function blendAdd(s: RGBA, d: RGBA): RGBA {
  return {
    r: clamp01(s.r + d.r),
    g: clamp01(s.g + d.g),
    b: clamp01(s.b + d.b),
    a: clamp01(s.a + d.a),
  }
}

/** ColorBurn: 1 - (1 - dst) / src. */
function blendColorBurn(s: RGBA, d: RGBA): RGBA {
  return {
    r: colorBurnComponent(s.r, d.r),
    g: colorBurnComponent(s.g, d.g),
    b: colorBurnComponent(s.b, d.b),
    a: colorBurnComponent(s.a, d.a),
  }
}

function colorBurnComponent(s: number, d: number): number {
  if (s <= 0) return 0
  if (s >= 1) return 1 - (1 - d) // = 1 - 0 = 1 when src=1
  return 1 - (1 - d) / s
}

/** HardLight: Overlay with src/dst swapped. */
function blendHardLight(s: RGBA, d: RGBA): RGBA {
  return {
    r: overlayComponent(d.r, s.r),
    g: overlayComponent(d.g, s.g),
    b: overlayComponent(d.b, s.b),
    a: overlayComponent(d.a, s.a),
  }
}

/**
 * SoftLight (Pegtop variant): 1 - 2*(1-s)*... linear blend between the two
 * reference points. The Pegtop formula is:
 *
 *   b(d, s) = (1 - 2s) * d² + 2 * s * d
 *
 * applied independently to each channel. Stable at endpoints.
 */
function blendSoftLight(s: RGBA, d: RGBA): RGBA {
  return {
    r: pegtopComponent(s.r, d.r),
    g: pegtopComponent(s.g, d.g),
    b: pegtopComponent(s.b, d.b),
    a: pegtopComponent(s.a, d.a),
  }
}

function pegtopComponent(s: number, d: number): number {
  return (1 - 2 * s) * d * d + 2 * s * d
}

/** Difference: |src - dst|. */
function blendDifference(s: RGBA, d: RGBA): RGBA {
  return {
    r: Math.abs(s.r - d.r),
    g: Math.abs(s.g - d.g),
    b: Math.abs(s.b - d.b),
    a: Math.abs(s.a - d.a),
  }
}

/** Exclusion: src + dst - 2*src*dst. */
function blendExclusion(s: RGBA, d: RGBA): RGBA {
  return {
    r: s.r + d.r - 2 * s.r * d.r,
    g: s.g + d.g - 2 * s.g * d.g,
    b: s.b + d.b - 2 * s.b * d.b,
    a: s.a + d.a - 2 * s.a * d.a,
  }
}

/** Subtract: dst - src, clamped. */
function blendSubtract(s: RGBA, d: RGBA): RGBA {
  return {
    r: clamp01(d.r - s.r),
    g: clamp01(d.g - s.g),
    b: clamp01(d.b - s.b),
    a: clamp01(d.a - s.a),
  }
}

/**
 * Inverse: the source is visually inverted, then composited as Normal.
 * In premultiplied space:
 *   - The "un-premultiplied" color is s.rgb / s.a (if s.a > 0).
 *   - Invert it: 1 - (s.rgb / s.a).
 *   - Re-premultiply with the original alpha s.a.
 *   - Composite the result over dst using Normal source-over.
 *
 * When the source is fully transparent, the inverse is empty and the
 * destination is preserved.
 */
function blendInverse(s: RGBA, d: RGBA): RGBA {
  if (s.a <= 0) {
    return d
  }
  const invR = (1 - s.r / s.a) * s.a
  const invG = (1 - s.g / s.a) * s.a
  const invB = (1 - s.b / s.a) * s.a
  return {
    r: invR + d.r * (1 - s.a),
    g: invG + d.g * (1 - s.a),
    b: invB + d.b * (1 - s.a),
    a: s.a + d.a * (1 - s.a),
  }
}

/** DestinationIn: src * dst.a. Keep destination alpha, multiply source by it. */
function blendDestinationIn(s: RGBA, d: RGBA): RGBA {
  return {
    r: s.r * d.a,
    g: s.g * d.a,
    b: s.b * d.a,
    a: s.a * d.a,
  }
}

/**
 * SourceIn (ClipToLower): show source only where destination exists.
 * The destination acts as the mask — output = src * dst.a.
 */
function blendSourceIn(s: RGBA, d: RGBA): RGBA {
  return {
    r: s.r * d.a,
    g: s.g * d.a,
    b: s.b * d.a,
    a: s.a * d.a,
  }
}

/**
 * SourceOut (SliceFromLower): show source only where destination does NOT exist.
 * Output = src * (1 - dst.a). Alpha is the same gating.
 */
function blendSourceOut(s: RGBA, d: RGBA): RGBA {
  const mask = 1 - d.a
  return {
    r: s.r * mask,
    g: s.g * mask,
    b: s.b * mask,
    a: s.a * mask,
  }
}

// ---------------------------------------------------------------------------
// Dispatcher
// ---------------------------------------------------------------------------

/**
 * Apply an Inochi2D blend mode to a (src, dst) pair in premultiplied space.
 * Returns the resulting premultiplied color. All outputs are clamped to [0, 1].
 */
export function blendPremultiplied(src: RGBA, dst: RGBA, mode: BlendMode): RGBA {
  let result: RGBA
  switch (mode) {
    case BM.Normal:
      result = blendNormal(src, dst)
      break
    case BM.Multiply:
      result = blendMultiply(src, dst)
      break
    case BM.Screen:
      result = blendScreen(src, dst)
      break
    case BM.Overlay:
      result = blendOverlay(src, dst)
      break
    case BM.Darken:
      result = blendDarken(src, dst)
      break
    case BM.Lighten:
      result = blendLighten(src, dst)
      break
    case BM.ColorDodge:
      result = blendColorDodge(src, dst)
      break
    case BM.LinearDodge:
    case BM.AddGlow:
      result = blendAdd(src, dst)
      break
    case BM.ColorBurn:
      result = blendColorBurn(src, dst)
      break
    case BM.HardLight:
      result = blendHardLight(src, dst)
      break
    case BM.SoftLight:
      result = blendSoftLight(src, dst)
      break
    case BM.Difference:
      result = blendDifference(src, dst)
      break
    case BM.Exclusion:
      result = blendExclusion(src, dst)
      break
    case BM.Subtract:
      result = blendSubtract(src, dst)
      break
    case BM.Inverse:
      result = blendInverse(src, dst)
      break
    case BM.DestinationIn:
      result = blendDestinationIn(src, dst)
      break
    case BM.SourceIn:
      result = blendSourceIn(src, dst)
      break
    case BM.SourceOut:
      result = blendSourceOut(src, dst)
      break
    default:
      // Defensive fallback — should not occur with a valid BlendMode.
      result = blendNormal(src, dst)
  }
  return {
    r: clamp01(result.r),
    g: clamp01(result.g),
    b: clamp01(result.b),
    a: clamp01(result.a),
  }
}

// ---------------------------------------------------------------------------
// Implementation-path selection
// ---------------------------------------------------------------------------

/**
 * Decide whether a blend mode should be implemented via fixed-function blending
 * (gl.blendFunc + gl.blendEquation) or via a render-to-texture + blend shader.
 *
 * Native modes are exactly the ones that map cleanly onto a single
 * blendEquation + factor pair in premultiplied alpha space:
 *   - Normal         (FUNC_ADD, ONE / ONE_MINUS_SRC_ALPHA)
 *   - Darken         (MIN, ONE / ONE)
 *   - Lighten        (MAX, ONE / ONE)
 *   - LinearDodge    (FUNC_ADD, ONE / ONE)
 *   - AddGlow        (FUNC_ADD, ONE / ONE)
 *   - Subtract       (FUNC_REVERSE_SUBTRACT, ONE / ONE)
 *   - DestinationIn  (FUNC_ADD, ZERO / SRC_ALPHA)
 *
 * Everything else falls through to the shader-FBO path.
 */
export function getBlendModeImpl(mode: BlendMode): BlendModeImpl {
  switch (mode) {
    case BM.Normal:
    case BM.Darken:
    case BM.Lighten:
    case BM.LinearDodge:
    case BM.AddGlow:
    case BM.Subtract:
    case BM.DestinationIn:
      return 'native'
    default:
      return 'shader'
  }
}

/** Set of all 19 blend modes. */
export const ALL_BLEND_MODES: BlendMode[] = [
  BM.Normal,
  BM.Multiply,
  BM.Screen,
  BM.Overlay,
  BM.Darken,
  BM.Lighten,
  BM.ColorDodge,
  BM.LinearDodge,
  BM.AddGlow,
  BM.ColorBurn,
  BM.HardLight,
  BM.SoftLight,
  BM.Difference,
  BM.Exclusion,
  BM.Subtract,
  BM.Inverse,
  BM.DestinationIn,
  BM.SourceIn,
  BM.SourceOut,
]

// ---------------------------------------------------------------------------
// Native blend parameters
// ---------------------------------------------------------------------------

/**
 * Return the fixed-function blend parameters for a native-implementation mode.
 * If the mode is shader-only, returns null (the renderer must use the FBO path).
 */
export function getNativeBlendParams(
  mode: BlendMode,
  gl: GLBlendKnobs,
): NativeBlendParams | null {
  switch (mode) {
    case BM.Normal:
      return {
        equation: gl.FUNC_ADD,
        srcFactor: gl.ONE,
        dstFactor: gl.ONE_MINUS_SRC_ALPHA,
      }
    case BM.Darken:
      return {
        equation: gl.MIN,
        srcFactor: gl.ONE,
        dstFactor: gl.ONE,
      }
    case BM.Lighten:
      return {
        equation: gl.MAX,
        srcFactor: gl.ONE,
        dstFactor: gl.ONE,
      }
    case BM.LinearDodge:
    case BM.AddGlow:
      return {
        equation: gl.FUNC_ADD,
        srcFactor: gl.ONE,
        dstFactor: gl.ONE,
      }
    case BM.Subtract:
      // out = dst - src (premultiplied), clamped implicitly by 0 ≤ d ≤ 1.
      return {
        equation: gl.FUNC_REVERSE_SUBTRACT,
        srcFactor: gl.ONE,
        dstFactor: gl.ONE,
      }
    case BM.DestinationIn:
      return {
        equation: gl.FUNC_ADD,
        srcFactor: gl.ZERO,
        dstFactor: gl.SRC_ALPHA,
      }
    default:
      return null
  }
}

// ---------------------------------------------------------------------------
// GLSL formula string (for the shader-based blend path)
// ---------------------------------------------------------------------------

/**
 * Return a GLSL expression body that computes the result color given two
 * premultiplied RGBA inputs `s` (source) and `d` (destination). Used as the
 * body of the blend-shader fragment program.
 *
 * The returned string is a single GLSL expression assigned to a `vec4` named
 * `outColor`. The renderer assembles the full shader.
 *
 * The formulas mirror the JS implementations above, translated to GLSL with
 * standard componentwise functions. Stable at endpoints: clamp / saturate
 * are applied to all channels at the end.
 */
export function blendGlslBody(mode: BlendMode): string {
  // s = source (premultiplied), d = destination (premultiplied).
  // Each component formula is written in [0, 1] channel-wise.
  // Final output is clamped to [0, 1] via the assembler's clamp().
  switch (mode) {
    case BM.Multiply:
      return 'outColor = vec4(s.r * d.r, s.g * d.g, s.b * d.b, s.a * d.a);'
    case BM.Screen:
      return 'outColor = vec4(s.r + d.r - s.r * d.r, s.g + d.g - s.g * d.g, s.b + d.b - s.b * d.b, s.a + d.a - s.a * d.a);'
    case BM.Overlay: {
      const ov = (sCol: string, dCol: string) =>
        `(${dCol} < 0.5 ? 2.0 * (${dCol}) * (${sCol}) : 1.0 - 2.0 * (1.0 - ${dCol}) * (1.0 - ${sCol}))`
      return (
        'outColor = vec4(' +
        ov('s.r', 'd.r') + ', ' +
        ov('s.g', 'd.g') + ', ' +
        ov('s.b', 'd.b') + ', ' +
        ov('s.a', 'd.a') +
        ');'
      )
    }
    case BM.ColorDodge: {
      // safeColorDodge: saturate at 1 when src>=1, 0 when src<=0.
      const cd = (sCol: string, dCol: string) =>
        `(${sCol} >= 1.0 ? 1.0 : (${sCol} <= 0.0 ? 0.0 : (${dCol}) / (1.0 - ${sCol})))`
      return (
        'outColor = vec4(' +
        cd('s.r', 'd.r') + ', ' +
        cd('s.g', 'd.g') + ', ' +
        cd('s.b', 'd.b') + ', ' +
        cd('s.a', 'd.a') +
        ');'
      )
    }
    case BM.ColorBurn: {
      const cb = (sCol: string, dCol: string) =>
        `(${sCol} <= 0.0 ? 0.0 : (${sCol} >= 1.0 ? 1.0 : 1.0 - (1.0 - ${dCol}) / ${sCol}))`
      return (
        'outColor = vec4(' +
        cb('s.r', 'd.r') + ', ' +
        cb('s.g', 'd.g') + ', ' +
        cb('s.b', 'd.b') + ', ' +
        cb('s.a', 'd.a') +
        ');'
      )
    }
    case BM.HardLight: {
      // Overlay with src and dst swapped (i.e. s acts as the "base").
      const hl = (sCol: string, dCol: string) =>
        `(${sCol} < 0.5 ? 2.0 * (${sCol}) * (${dCol}) : 1.0 - 2.0 * (1.0 - ${sCol}) * (1.0 - ${dCol}))`
      return (
        'outColor = vec4(' +
        hl('s.r', 'd.r') + ', ' +
        hl('s.g', 'd.g') + ', ' +
        hl('s.b', 'd.b') + ', ' +
        hl('s.a', 'd.a') +
        ');'
      )
    }
    case BM.SoftLight: {
      // Pegtop soft-light: (1 - 2s) * d^2 + 2s * d.
      const sl = (sCol: string, dCol: string) =>
        `(1.0 - 2.0 * ${sCol}) * ${dCol} * ${dCol} + 2.0 * ${sCol} * ${dCol}`
      return (
        'outColor = vec4(' +
        sl('s.r', 'd.r') + ', ' +
        sl('s.g', 'd.g') + ', ' +
        sl('s.b', 'd.b') + ', ' +
        sl('s.a', 'd.a') +
        ');'
      )
    }
    case BM.Difference:
      return 'outColor = vec4(abs(s.r - d.r), abs(s.g - d.g), abs(s.b - d.b), abs(s.a - d.a));'
    case BM.Exclusion:
      return 'outColor = vec4(s.r + d.r - 2.0 * s.r * d.r, s.g + d.g - 2.0 * s.g * d.g, s.b + d.b - 2.0 * s.b * d.b, s.a + d.a - 2.0 * s.a * d.a);'
    case BM.Inverse: {
      // Invert the source (still premultiplied), then source-over.
      const inv = (sCol: string) =>
        `(s.a > 0.0 ? (1.0 - ${sCol} / s.a) * s.a : 0.0)`
      return (
        'outColor = vec4(' +
        // r
        `${inv('s.r')} + d.r * (1.0 - s.a), ` +
        // g
        `${inv('s.g')} + d.g * (1.0 - s.a), ` +
        // b
        `${inv('s.b')} + d.b * (1.0 - s.a), ` +
        // a: standard source-over alpha
        `s.a + d.a * (1.0 - s.a)` +
        ');'
      )
    }
    case BM.SourceIn:
      // Show source only where destination exists. Use destination alpha as mask.
      return 'outColor = vec4(s.r * d.a, s.g * d.a, s.b * d.a, s.a * d.a);'
    case BM.SourceOut:
      // Show source only where destination does NOT exist.
      return 'outColor = vec4(s.r * (1.0 - d.a), s.g * (1.0 - d.a), s.b * (1.0 - d.a), s.a * (1.0 - d.a));'
    default:
      // Should not be reached for shader modes; renderer's defensive default.
      return 'outColor = s;'
  }
}

/**
 * Set of blend modes that must be implemented via the shader-FBO path.
 * Exposed for the renderer's gap-reporting logic and tests.
 */
export const SHADER_BLEND_MODES: BlendMode[] = [
  BM.Multiply,
  BM.Screen,
  BM.Overlay,
  BM.ColorDodge,
  BM.ColorBurn,
  BM.HardLight,
  BM.SoftLight,
  BM.Difference,
  BM.Exclusion,
  BM.Inverse,
  BM.SourceIn,
  BM.SourceOut,
]

/**
 * Set of blend modes that use the native (fixed-function) blend path.
 * Exposed for the renderer's gap-reporting logic and tests.
 */
export const NATIVE_BLEND_MODES: BlendMode[] = [
  BM.Normal,
  BM.Darken,
  BM.Lighten,
  BM.LinearDodge,
  BM.AddGlow,
  BM.Subtract,
  BM.DestinationIn,
]
