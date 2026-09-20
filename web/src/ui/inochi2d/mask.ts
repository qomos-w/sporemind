/**
 * Pure mask logic for Inochi2D mask operations.
 *
 * Inochi2D masking uses a three-command protocol in the DrawList stream:
 *
 *   1. **PushMask**  — begins a mask region. Subsequent DefineMask commands
 *      render mask geometry into a mask buffer (FBO), and subsequent Normal
 *      geometry is accumulated in a content buffer. Both buffers are
 *      canvas-sized and cleared to transparent at PushMask time.
 *   2. **DefineMask** — renders geometry into the mask buffer. The mask
 *      buffer's alpha channel defines the visible/invisible regions.
 *   3. **PopMask**   — ends the mask region. The accumulated content buffer
 *      is multiplied by the mask buffer's alpha (Mask mode) or its inverse
 *      (Dodge mode), then source-over composited onto the parent render
 *      target (the main canvas, or the parent mask's content buffer for
 *      nested masks).
 *
 * MaskingMode (mirror of `core/render/state.go:174-181`):
 *   - **Mask**  (0): content visible where mask alpha > 0  → content × mask.a
 *   - **Dodge** (1): content visible where mask alpha = 0  → content × (1 − mask.a)
 *
 * Masks can nest: an inner PushMask within an outer mask region creates a
 * sub-mask. The inner content is composited onto the outer content buffer
 * (not the main canvas) when the inner PopMask fires. The outer content is
 * then composited onto the main canvas when the outer PopMask fires.
 *
 * This module provides:
 *   - Pure pixel-level mask application functions (testable without GL).
 *   - A pure mask-stack state machine (testable without GL).
 *   - GLSL fragment shader source for the mask composite pass (used by the
 *     renderer's PopMask fullscreen quad).
 *
 * Source: `core/render/drawlist.go:225-235,267-270` (PushMask/PopMask/SetMasking),
 * `core/render/state.go:174-181` (MaskingMode enum),
 * `part.go:243-248` (OnDrawMask), `mask.go:114-143` (MaskNode.OnDraw).
 */

import { MaskingMode as MM, type MaskingMode } from './types'
import type { RGBA } from './blendMode'

// ---------------------------------------------------------------------------
// Pixel-level mask application (pure, no GL dependency)
// ---------------------------------------------------------------------------

/**
 * Apply a mask to a content pixel.
 *
 * For **Mask** mode: multiply content by mask alpha (content visible where
 * the mask is opaque).
 * For **Dodge** mode: multiply content by (1 − mask alpha) (content visible
 * where the mask is transparent — "dodging" the masked area).
 *
 * Both content and result are in premultiplied alpha space. The `maskAlpha`
 * parameter is the alpha channel of the mask buffer at this pixel (0..1).
 */
export function applyMaskPixel(
  content: RGBA,
  maskAlpha: number,
  mode: MaskingMode,
): RGBA {
  const factor = mode === MM.Dodge ? 1 - maskAlpha : maskAlpha
  return {
    r: content.r * factor,
    g: content.g * factor,
    b: content.b * factor,
    a: content.a * factor,
  }
}

/**
 * Composite a masked content pixel onto a destination pixel using
 * source-over (premultiplied alpha compositing).
 *
 * This is the second half of the PopMask pipeline: after the mask has been
 * applied to the content, the result is blended onto the parent render target.
 */
export function maskCompositePixel(
  maskedContent: RGBA,
  dst: RGBA,
): RGBA {
  return {
    r: maskedContent.r + dst.r * (1 - maskedContent.a),
    g: maskedContent.g + dst.g * (1 - maskedContent.a),
    b: maskedContent.b + dst.b * (1 - maskedContent.a),
    a: maskedContent.a + dst.a * (1 - maskedContent.a),
  }
}

/**
 * Full mask pipeline for a single pixel: apply mask to content, then
 * source-over composite onto destination.
 *
 * This is exactly what the PopMask composite shader computes per-pixel:
 *   1. maskedContent = content × maskFactor (maskFactor = mask.a or 1−mask.a)
 *   2. result = sourceOver(maskedContent, dst)
 */
export function maskPipelinePixel(
  content: RGBA,
  maskAlpha: number,
  mode: MaskingMode,
  dst: RGBA,
): RGBA {
  const masked = applyMaskPixel(content, maskAlpha, mode)
  return maskCompositePixel(masked, dst)
}

/**
 * Nested mask pipeline for a single pixel.
 *
 * Simulates two levels of masking:
 *   1. Inner content is masked by innerMask, then composited onto the outer
 *      content buffer.
 *   2. The outer content (now including the inner contribution) is masked by
 *      outerMask, then composited onto the final destination.
 *
 * This mirrors the renderer's nested PopMask sequence: inner PopMask composites
 * onto the outer content FBO, then outer PopMask composites onto the canvas.
 */
export function nestedMaskPipelinePixel(
  innerContent: RGBA,
  innerMaskAlpha: number,
  innerMode: MaskingMode,
  outerContent: RGBA,
  outerMaskAlpha: number,
  outerMode: MaskingMode,
  finalDst: RGBA,
): RGBA {
  // Inner: mask inner content and composite onto outer content buffer
  const innerMasked = applyMaskPixel(innerContent, innerMaskAlpha, innerMode)
  const outerWithInner = maskCompositePixel(innerMasked, outerContent)

  // Outer: mask the combined content and composite onto final destination
  const outerMasked = applyMaskPixel(outerWithInner, outerMaskAlpha, outerMode)
  return maskCompositePixel(outerMasked, finalDst)
}

// ---------------------------------------------------------------------------
// Mask stack state machine (pure, no GL dependency)
// ---------------------------------------------------------------------------

/**
 * A single entry in the mask stack. The renderer maintains a parallel stack
 * of GPU resources (FBOs, textures); this pure structure tracks only the
 * logical state for testing.
 */
export interface MaskStackEntry {
  maskMode: MaskingMode
  /** Whether at least one DefineMask has been processed for this level. */
  hasMaskGeometry: boolean
}

/** Mutable mask-stack state, testable without any GL context. */
export interface MaskStackState {
  stack: MaskStackEntry[]
}

/** Create a fresh, empty mask-stack state. */
export function createMaskStackState(): MaskStackState {
  return { stack: [] }
}

/** Push a new mask level onto the stack. */
export function maskStackPush(state: MaskStackState, mode: MaskingMode): void {
  state.stack.push({ maskMode: mode, hasMaskGeometry: false })
}

/** Mark that a DefineMask has been processed for the current (top) level. */
export function maskStackDefine(state: MaskStackState): void {
  const top = state.stack[state.stack.length - 1]
  if (top) top.hasMaskGeometry = true
}

/** Pop the top mask level. Returns the popped entry, or null if the stack was empty. */
export function maskStackPop(state: MaskStackState): MaskStackEntry | null {
  return state.stack.pop() ?? null
}

/** Current mask depth (0 = no mask active). */
export function maskStackDepth(state: MaskStackState): number {
  return state.stack.length
}

/** Whether a mask is currently active (stack is non-empty). */
export function maskStackActive(state: MaskStackState): boolean {
  return state.stack.length > 0
}

/** Get the MaskingMode of the current (top) level, or null if no mask is active. */
export function maskStackCurrentMode(state: MaskStackState): MaskingMode | null {
  const top = state.stack[state.stack.length - 1]
  return top ? top.maskMode : null
}

// ---------------------------------------------------------------------------
// GLSL shader for mask composite pass (used by the renderer)
// ---------------------------------------------------------------------------

/**
 * Fragment shader for the PopMask composite pass.
 *
 * Samples three textures and computes the composited result:
 *   - `u_content` — the content FBO (accumulated geometry within the mask region)
 *   - `u_mask`    — the mask FBO (alpha channel defines the mask shape)
 *   - `u_dst`     — a copy of the parent render target (for source-over)
 *   - `u_dodge`   — 0.0 for Mask mode, 1.0 for Dodge mode
 *
 * The shader:
 *   1. Reads the mask alpha from `u_mask`.
 *   2. If Dodge mode, inverts: maskAlpha = 1.0 − maskAlpha.
 *   3. Multiplies the content by maskAlpha (premultiplied space).
 *   4. Source-over composites the masked content onto the destination.
 *
 * Uses the same fullscreen-quad vertex shader (`VERT_SHADER_FULLSCREEN`) as
 * the blend path, reusing the existing `drawFullscreenQuad` infrastructure.
 */
export const MASK_COMPOSITE_FRAG_SHADER = `
precision mediump float;
varying vec2 v_uv;
uniform sampler2D u_content;
uniform sampler2D u_mask;
uniform sampler2D u_dst;
uniform float u_dodge;
void main() {
  vec4 content = texture2D(u_content, v_uv);
  float maskAlpha = texture2D(u_mask, v_uv).a;
  if (u_dodge > 0.5) maskAlpha = 1.0 - maskAlpha;
  vec4 masked = vec4(content.rgb * maskAlpha, content.a * maskAlpha);
  vec4 dst = texture2D(u_dst, v_uv);
  gl_FragColor = vec4(
    masked.rgb + dst.rgb * (1.0 - masked.a),
    masked.a + dst.a * (1.0 - masked.a)
  );
}
`