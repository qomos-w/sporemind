/**
 * Pure logic for processing Inochi2D DrawList data.
 *
 * These functions have no WebGL dependency and are fully unit-testable. They
 * handle vertex buffer packing, command classification, uniform decoding, and
 * projection matrix generation.
 */

import type {
  DrawCmd,
  DrawListSnapshot,
  BlendMode,
} from './types'
import { DrawState as DS, BlendMode as BM } from './types'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/** Floats per vertex in the interleaved VBO: [posX, posY, uvU, uvV]. */
export const FLOATS_PER_VERTEX = 4
/** Bytes per vertex in the interleaved VBO (4 × float32). */
export const BYTES_PER_VERTEX = FLOATS_PER_VERTEX * 4

/** PartVars float layout offsets (mirror part.go:225-234). */
export const PARTVARS = {
  tintOffset: 0, // vec3
  screenTintOffset: 3, // vec3
  paddingOffset: 6,
  opacityOffset: 7,
  emissionOffset: 8,
} as const

/** Expected number of float32 values in the variables buffer. */
export const VARIABLES_FLOAT_COUNT = 16

// ---------------------------------------------------------------------------
// Command classification
// ---------------------------------------------------------------------------

export interface ClassifiedCommand {
  cmd: DrawCmd
  kind: 'geometry' | 'mask-define' | 'mask-push' | 'mask-pop' | 'composite-begin' | 'composite-end' | 'composite-blit'
  /** True when this command's blend mode is supported by the spike renderer. */
  blendSupported: boolean
}

/**
 * Report whether a BlendMode is supported by the renderer.
 *
 * All 19 Inochi2D blend modes are now implemented, split across two paths:
 *  - Native path (fixed-function gl.blendFunc / gl.blendEquation): Normal,
 *    Darken, Lighten, LinearDodge, AddGlow, Subtract, DestinationIn.
 *  - Shader path (FBO render-to-texture + blend-shader): Multiply, Screen,
 *    Overlay, ColorDodge, ColorBurn, HardLight, SoftLight, Difference,
 *    Exclusion, Inverse, SourceIn, SourceOut.
 *
 * See blendMode.ts (getBlendModeImpl) for the authoritative path selection.
 * Unrecognized enum values remain unsupported.
 */
export function isBlendSupported(mode: BlendMode): boolean {
  return Object.values(BM).includes(mode)
}

/**
 * Classify a draw command by its state, determining whether it carries
 * renderable geometry or is a structural state change.
 */
export function classifyCommand(cmd: DrawCmd): ClassifiedCommand {
  let kind: ClassifiedCommand['kind']
  switch (cmd.state) {
    case DS.Normal:
      kind = 'geometry'
      break
    case DS.DefineMask:
      kind = 'mask-define'
      break
    case DS.PushMask:
      kind = 'mask-push'
      break
    case DS.PopMask:
      kind = 'mask-pop'
      break
    case DS.CompositeBegin:
      kind = 'composite-begin'
      break
    case DS.CompositeEnd:
      kind = 'composite-end'
      break
    case DS.CompositeBlit:
      kind = 'composite-blit'
      break
    default:
      kind = 'geometry'
  }
  return {
    cmd,
    kind,
    blendSupported: isBlendSupported(cmd.blendMode),
  }
}

/**
 * Classify all commands in a snapshot. Returns the list plus summary counts
 * for gap reporting.
 */
export function classifyAllCommands(snapshot: DrawListSnapshot): {
  commands: ClassifiedCommand[]
  geometryCount: number
  unsupportedBlendCount: number
  maskCount: number
  compositeCount: number
} {
  const commands = snapshot.commands.map(classifyCommand)
  let geometryCount = 0
  let unsupportedBlendCount = 0
  let maskCount = 0
  let compositeCount = 0
  for (const c of commands) {
    if (c.kind === 'geometry') {
      geometryCount++
      if (!c.blendSupported) unsupportedBlendCount++
    } else if (c.kind === 'mask-define' || c.kind === 'mask-push' || c.kind === 'mask-pop') {
      maskCount++
    } else {
      compositeCount++
    }
  }
  return { commands, geometryCount, unsupportedBlendCount, maskCount, compositeCount }
}

// ---------------------------------------------------------------------------
// Vertex buffer packing
// ---------------------------------------------------------------------------

/**
 * Pack vertex data into a flat Float32Array: [posX, posY, uvU, uvV, ...].
 * This matches the interleaved attribute layout consumed by the vertex shader.
 */
export function packVertexBuffer(vertices: DrawListSnapshot['vertices']): Float32Array {
  const buf = new Float32Array(vertices.length * FLOATS_PER_VERTEX)
  for (let i = 0; i < vertices.length; i++) {
    const v = vertices[i]!
    const off = i * FLOATS_PER_VERTEX
    buf[off + 0] = v.vtx[0]
    buf[off + 1] = v.vtx[1]
    buf[off + 2] = v.uv[0]
    buf[off + 3] = v.uv[1]
  }
  return buf
}

/** Pack index data into a Uint32Array. */
export function packIndexBuffer(indices: number[]): Uint32Array {
  return new Uint32Array(indices)
}

/**
 * Compute the maximum index value in an index buffer, or 0 if empty.
 * Used to decide whether a 16-bit fallback is safe.
 */
export function maxIndex(indices: number[]): number {
  let m = 0
  for (let i = 0; i < indices.length; i++) {
    const v = indices[i]!
    if (v > m) m = v
  }
  return m
}

/** Maximum index representable in a 16-bit (UNSIGNED_SHORT) index buffer. */
export const MAX_UINT16_INDEX = 65535

// ---------------------------------------------------------------------------
// Uniform (PartVars) decoding
// ---------------------------------------------------------------------------

export interface DecodedPartVars {
  tint: [number, number, number]
  screenTint: [number, number, number]
  opacity: number
  emissionStrength: number
}

/**
 * Decode the 64-byte Variables buffer of a PartNode command into typed fields.
 * The variables array is 16 float32 values; only the first 9 are meaningful
 * for PartVars.
 */
export function decodePartVars(variables: number[]): DecodedPartVars {
  const safe = variables.length >= VARIABLES_FLOAT_COUNT
    ? variables
    : [...variables, ...new Array(VARIABLES_FLOAT_COUNT - variables.length).fill(0)]
  return {
    tint: [safe[PARTVARS.tintOffset]!, safe[PARTVARS.tintOffset + 1]!, safe[PARTVARS.tintOffset + 2]!],
    screenTint: [safe[PARTVARS.screenTintOffset]!, safe[PARTVARS.screenTintOffset + 1]!, safe[PARTVARS.screenTintOffset + 2]!],
    opacity: safe[PARTVARS.opacityOffset]!,
    emissionStrength: safe[PARTVARS.emissionOffset]!,
  }
}

// ---------------------------------------------------------------------------
// Projection matrix
// ---------------------------------------------------------------------------

/**
 * Generate an orthographic projection matrix (column-major, WebGL convention)
 * that maps model-space bounds [minX, maxX] × [minY, maxY] to clip space
 * [-1, 1]².
 *
 * If flipY is true, the Y axis is flipped so that model-space Y-down maps to
 * clip-space Y-up (matching typical 2D art conventions where (0,0) is top-left).
 *
 * Returns a Float32Array of length 16.
 */
export function orthoProjection(
  minX: number,
  maxX: number,
  minY: number,
  maxY: number,
  flipY = false,
): Float32Array {
  const l = minX
  const r = maxX
  // Without flip: standard OpenGL Y-up (minY=bottom, maxY=top).
  // With flip: Y-down / screen-coordinate convention (maxY=bottom, minY=top).
  const b = flipY ? maxY : minY
  const t = flipY ? minY : maxY

  const sx = 2 / (r - l)
  const sy = 2 / (t - b)

  // Column-major 4×4 orthographic matrix (near=-1, far=1).
  // prettier-ignore
  return new Float32Array([
    sx,   0,    0, 0,
    0,    sy,   0, 0,
    0,    0,   -1, 0,
    -(r+l)/(r-l), -(t+b)/(t-b), 0, 1,
  ])
}

// ---------------------------------------------------------------------------
// Blend mode → WebGL blend function mapping
// ---------------------------------------------------------------------------

export interface BlendFunc {
  srcFactor: number
  dstFactor: number
}

/**
 * BlendMode names for logging and gap reporting.
 * Mirrors render.BlendModeName (state.go:127-170).
 */
export function blendModeName(mode: BlendMode): string {
  const entries = Object.entries(BM) as [string, BlendMode][]
  for (const [name, val] of entries) {
    if (val === mode) return name
  }
  return 'Unknown'
}

/**
 * Map an Inochi2D BlendMode to WebGL blend factors for the modes that map to
 * fixed-function blending in premultiplied-alpha space.
 *
 * This covers only the factor-mappable subset (Normal, DestinationIn). The
 * renderer's authoritative path selection lives in blendMode.ts:
 * `getNativeBlendParams` (equation + factors for all native modes) and
 * `getBlendModeImpl` (native vs shader dispatch). Modes that require a blend
 * equation (Darken/Lighten/Subtract/LinearDodge/AddGlow) or a shader
 * (Multiply/Screen/etc.) return the Normal factors here with
 * `supported: false` — callers should treat that as "use another path", not
 * "render incorrectly".
 *
 * @param gl the WebGL rendering context (for constant values)
 * @param mode the Inochi2D blend mode
 * @returns blend factors; `supported` indicates whether the mode maps to
 *          fixed-function factors directly.
 */
export function mapBlendFunc(
  gl: { ONE: number; ZERO: number; ONE_MINUS_SRC_ALPHA: number; SRC_ALPHA: number },
  mode: BlendMode,
): BlendFunc & { supported: boolean } {
  // Normal source-over in premultiplied-alpha space.
  const normal: BlendFunc = {
    srcFactor: gl.ONE,
    dstFactor: gl.ONE_MINUS_SRC_ALPHA,
  }

  if (mode === BM.Normal) {
    return { ...normal, supported: true }
  }

  // DestinationIn maps cleanly to WebGL blend factors.
  if (mode === BM.DestinationIn) {
    return {
      srcFactor: gl.ZERO,
      dstFactor: gl.SRC_ALPHA,
      supported: true,
    }
  }

  // Other modes are implemented via blend equations (getNativeBlendParams)
  // or the shader-FBO path (blendGlslBody) — not plain factors.
  return { ...normal, supported: false }
}
