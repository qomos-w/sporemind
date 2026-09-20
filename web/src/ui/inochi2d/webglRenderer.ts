/**
 * WebGL renderer for Inochi2D DrawList data.
 *
 * Consumes a processed DrawListSnapshot and renders it on a transparent
 * canvas with full Inochi2D blend-mode support:
 *
 *   - Textured triangles (premultiplied alpha)
 *   - Flat-color triangles (no texture)
 *   - Opacity and tint from PartVars
 *   - All 19 Inochi2D blend modes, via two paths:
 *     * **Native**: fixed-function `gl.blendFunc` / `gl.blendEquation` for
 *       separable / min-max / additive modes (Normal, Darken, Lighten,
 *       LinearDodge, AddGlow, Subtract, DestinationIn). Requires WebGL2, or
 *       WebGL1 with `EXT_blend_minmax` for MIN/MAX.
 *     * **Shader**: render-to-texture (FBO) + per-mode blend shader for
 *       non-separable formulas (Multiply, Screen, Overlay, ColorDodge,
 *       ColorBurn, HardLight, SoftLight, Difference, Exclusion, Inverse,
 *       SourceIn, SourceOut).
 *
 * Unsupported features (composites) are still classified and surfaced
 * via gap reporting — see RENDERER_GAPS.md. Mask operations (DefineMask,
 * PushMask, PopMask) are fully implemented via FBO-based mask buffers
 * with nested mask support — see mask.ts and RENDERER_GAPS.md Gap 2.
 */

import type { DrawListSnapshot, DrawListTexture, BlendMode, MaskingMode } from './types'
import {
  packVertexBuffer,
  packIndexBuffer,
  classifyAllCommands,
  decodePartVars,
  orthoProjection,
  blendModeName,
  maxIndex,
  MAX_UINT16_INDEX,
  BYTES_PER_VERTEX,
  type ClassifiedCommand,
} from './drawListProcessor'
import {
  getBlendModeImpl,
  getNativeBlendParams,
  blendGlslBody,
  type NativeBlendParams,
  type GLBlendKnobs,
} from './blendMode'
import { MASK_COMPOSITE_FRAG_SHADER } from './mask'

// ---------------------------------------------------------------------------
// Shader sources
// ---------------------------------------------------------------------------

const VERT_SHADER = `
attribute vec2 a_pos;
attribute vec2 a_uv;
uniform mat4 u_projection;
varying vec2 v_uv;
void main() {
  v_uv = a_uv;
  gl_Position = u_projection * vec4(a_pos, 0.0, 1.0);
}
`

const FRAG_SHADER_TEXTURED = `
precision mediump float;
varying vec2 v_uv;
uniform sampler2D u_tex;
uniform vec3 u_tint;
uniform float u_opacity;
void main() {
  vec4 tex = texture2D(u_tex, v_uv);
  vec3 rgb = tex.rgb * u_tint;
  float a = tex.a;
  // Premultiply for source-over blending.
  gl_FragColor = vec4(rgb * a * u_opacity, a * u_opacity);
}
`

const FRAG_SHADER_FLAT = `
precision mediump float;
varying vec2 v_uv;
uniform vec3 u_tint;
uniform float u_opacity;
void main() {
  // Flat color with premultiplied alpha.
  gl_FragColor = vec4(u_tint * u_opacity, u_opacity);
}
`

/**
 * Full-screen composite vertex shader for the blend path. Bypasses the
 * projection matrix — the quad covers NDC [-1, 1]² directly.
 */
const VERT_SHADER_FULLSCREEN = `
attribute vec2 a_pos;
varying vec2 v_uv;
void main() {
  v_uv = a_pos * 0.5 + 0.5;
  gl_Position = vec4(a_pos, 0.0, 1.0);
}
`

/**
 * Assemble the blend-shader fragment program for a given mode.
 *
 * Samples the premultiplied source (rendered to an FBO) and the premultiplied
 * destination (a copy of the current framebuffer), applies the mode's formula
 * from `blendGlslBody`, and alpha-gates the result by the source alpha so the
 * destination is preserved wherever the source geometry does not cover.
 */
function buildBlendFragShader(mode: BlendMode): string {
  return `
precision mediump float;
varying vec2 v_uv;
uniform sampler2D u_src;
uniform sampler2D u_dst;
void main() {
  vec4 s = texture2D(u_src, v_uv);
  vec4 d = texture2D(u_dst, v_uv);
  vec4 outColor;
  ${blendGlslBody(mode)}
  // Alpha-gate: preserve destination where the source is transparent.
  // At s.a=0 the result is exactly d; at s.a=1 it is the pure blend result.
  gl_FragColor = vec4(
    mix(d.rgb, clamp(outColor.rgb, 0.0, 1.0), s.a),
    mix(d.a, clamp(outColor.a, 0.0, 1.0), s.a)
  );
}
`
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface RenderGaps {
  unsupportedBlendModes: string[]
  maskCommands: number
  compositeCommands: number
  totalCommands: number
}

export interface RenderResult {
  drawnCommands: number
  skippedCommands: number
  gaps: RenderGaps
}

type GL = WebGLRenderingContext
type GL2 = WebGL2RenderingContext

/** A lazily-created FBO used for the shader blend path. */
interface BlendTargets {
  /** Framebuffer the source geometry is rendered into. */
  fbo: WebGLFramebuffer
  /** Color attachment of `fbo`; sampled as u_src. */
  srcTexture: WebGLTexture
  /** Copy of the canvas framebuffer; sampled as u_dst. */
  dstTexture: WebGLTexture
  /** Width/height the targets were allocated at. */
  width: number
  height: number
}

/**
 * A render target pair (FBO + color texture) used for mask operations.
 * Each mask level needs two targets: one for the mask buffer (accumulated
 * DefineMask geometry alpha) and one for the content buffer (accumulated
 * Normal geometry within the masked region).
 */
interface MaskTargets {
  /** FBO for the mask buffer — DefineMask geometry is rendered here. */
  maskFbo: WebGLFramebuffer
  /** Color attachment of `maskFbo`; its alpha channel is the mask shape. */
  maskTexture: WebGLTexture
  /** FBO for the content buffer — Normal geometry within the mask region. */
  contentFbo: WebGLFramebuffer
  /** Color attachment of `contentFbo`. */
  contentTexture: WebGLTexture
  width: number
  height: number
}

/** A stack entry for nested mask rendering. */
interface MaskLevel {
  targets: MaskTargets
  maskMode: MaskingMode
  hasMaskGeometry: boolean
}

// ---------------------------------------------------------------------------
// Shader helpers
// ---------------------------------------------------------------------------

function compileShader(gl: GL, type: number, source: string): WebGLShader {
  const shader = gl.createShader(type)
  if (!shader) throw new Error('Failed to create shader')
  gl.shaderSource(shader, source)
  gl.compileShader(shader)
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const info = gl.getShaderInfoLog(shader)
    gl.deleteShader(shader)
    throw new Error(`Shader compile error: ${info}`)
  }
  return shader
}

function linkProgram(gl: GL, vertSrc: string, fragSrc: string): WebGLProgram {
  const vert = compileShader(gl, gl.VERTEX_SHADER, vertSrc)
  const frag = compileShader(gl, gl.FRAGMENT_SHADER, fragSrc)
  const program = gl.createProgram()
  if (!program) throw new Error('Failed to create program')
  gl.attachShader(program, vert)
  gl.attachShader(program, frag)
  gl.linkProgram(program)
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    const info = gl.getProgramInfoLog(program)
    gl.deleteProgram(program)
    throw new Error(`Program link error: ${info}`)
  }
  gl.deleteShader(vert)
  gl.deleteShader(frag)
  return program
}

// ---------------------------------------------------------------------------
// Texture loading
// ---------------------------------------------------------------------------

/**
 * Load a DrawListTexture (data URL) into a WebGL texture.
 * Returns a promise because image decoding is async.
 */
export function loadTexture(gl: GL, tex: DrawListTexture): Promise<WebGLTexture> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => {
      const glTex = gl.createTexture()
      if (!glTex) {
        reject(new Error('Failed to create texture'))
        return
      }
      gl.bindTexture(gl.TEXTURE_2D, glTex)
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, img)
      resolve(glTex)
    }
    img.onerror = () => reject(new Error(`Failed to load texture ${tex.id}: ${tex.src.slice(0, 50)}`))
    img.src = tex.src
  })
}

// ---------------------------------------------------------------------------
// Index packing strategy (pure, testable)
// ---------------------------------------------------------------------------

/**
 * Decide how to pack the index buffer given extension availability.
 *
 * When `OES_element_index_uint` is present, a Uint32Array is returned. When it
 * is absent, indices are downgraded to Uint16Array — but only if every index
 * fits in 16 bits. If any index exceeds `MAX_UINT16_INDEX` the function throws
 * rather than silently producing a corrupt buffer.
 *
 * This is exported so the no-extension path can be unit-tested without a WebGL
 * context.
 */
export function packIndexBufferForType(
  indices: number[],
  useUint32: boolean,
): Uint32Array | Uint16Array {
  if (useUint32) {
    return packIndexBuffer(indices)
  }
  const maxIdx = maxIndex(indices)
  if (maxIdx > MAX_UINT16_INDEX) {
    throw new Error(
      `OES_element_index_uint not available and max index ${maxIdx} exceeds ${MAX_UINT16_INDEX}; ` +
      'cannot safely downgrade to 16-bit indices.',
    )
  }
  return new Uint16Array(indices)
}

// ---------------------------------------------------------------------------
// Renderer
// ---------------------------------------------------------------------------

export class DrawListWebGLRenderer {
  private gl: GL
  private texProgram: WebGLProgram
  private flatProgram: WebGLProgram
  private vbo: WebGLBuffer | null = null
  private ibo: WebGLBuffer | null = null
  /** Fullscreen quad VBO for the blend pass (NDC positions only). */
  private quadVbo: WebGLBuffer | null = null
  private textures: Map<number, WebGLTexture> = new Map()
  private canvas: HTMLCanvasElement
  /** WebGL index type for the current frame: UNSIGNED_INT or UNSIGNED_SHORT. */
  private indexType: number = 0

  /** True when the context supports MIN/MAX blend equations (WebGL2 or EXT_blend_minmax). */
  private hasMinMaxBlend: boolean

  /** Cached per-mode blend programs for the shader path. */
  private blendPrograms: Map<BlendMode, WebGLProgram> = new Map()
  private blendProgramUniforms: Map<
    BlendMode,
    { src: WebGLUniformLocation | null; dst: WebGLUniformLocation | null }
  > = new Map()

  /** Lazily-allocated render targets for the shader blend path. */
  private blendTargets: BlendTargets | null = null

  /** Stack of mask render targets for nested mask support. */
  private maskStack: MaskLevel[] = []
  /** Compiled mask-composite program (lazily linked on first use). */
  private maskCompositeProgram: WebGLProgram | null = null
  private maskCompositeUniforms: {
    content: WebGLUniformLocation | null
    mask: WebGLUniformLocation | null
    dst: WebGLUniformLocation | null
    dodge: WebGLUniformLocation | null
  } | null = null

  // Attribute/uniform locations (reused across programs since layout is identical)
  private texUniforms: { proj: WebGLUniformLocation | null; tex: WebGLUniformLocation | null; tint: WebGLUniformLocation | null; opacity: WebGLUniformLocation | null }
  private flatUniforms: { proj: WebGLUniformLocation | null; tint: WebGLUniformLocation | null; opacity: WebGLUniformLocation | null }

  constructor(canvas: HTMLCanvasElement) {
    this.canvas = canvas
    // Prefer WebGL2 (native MIN/MAX/FUNC_REVERSE_SUBTRACT blend equations);
    // fall back to WebGL1 + extensions.
    const attrs: WebGLContextAttributes = {
      premultipliedAlpha: true,
      alpha: true,
      antialias: true,
    }
    const gl2 = canvas.getContext('webgl2', attrs) as GL2 | null
    const gl = (gl2 ?? canvas.getContext('webgl', attrs)) as GL | null
    if (!gl) throw new Error('WebGL not supported')
    this.gl = gl

    // MIN/MAX equations: core in WebGL2; EXT_blend_minmax extension in WebGL1.
    if (gl2) {
      this.hasMinMaxBlend = true
    } else {
      const ext = gl.getExtension('EXT_blend_minmax')
      this.hasMinMaxBlend = ext !== null
    }

    this.texProgram = linkProgram(gl, VERT_SHADER, FRAG_SHADER_TEXTURED)
    this.flatProgram = linkProgram(gl, VERT_SHADER, FRAG_SHADER_FLAT)

    this.texUniforms = {
      proj: gl.getUniformLocation(this.texProgram, 'u_projection'),
      tex: gl.getUniformLocation(this.texProgram, 'u_tex'),
      tint: gl.getUniformLocation(this.texProgram, 'u_tint'),
      opacity: gl.getUniformLocation(this.texProgram, 'u_opacity'),
    }
    this.flatUniforms = {
      proj: gl.getUniformLocation(this.flatProgram, 'u_projection'),
      tint: gl.getUniformLocation(this.flatProgram, 'u_tint'),
      opacity: gl.getUniformLocation(this.flatProgram, 'u_opacity'),
    }
  }

  /**
   * Load textures from a snapshot into GPU memory. Must be called before render.
   */
  async loadTextures(snapshot: DrawListSnapshot): Promise<void> {
    const gl = this.gl
    for (const tex of snapshot.textures) {
      const glTex = await loadTexture(gl, tex)
      this.textures.set(tex.id, glTex)
    }
  }

  /**
   * Upload vertex and index buffers to the GPU.
   *
   * If `OES_element_index_uint` is available, a Uint32Array index buffer is
   * uploaded with UNSIGNED_INT. If the extension is missing, the indices are
   * re-packed as Uint16Array (UNSIGNED_SHORT) — but only when every index fits
   * in 16 bits (max ≤ 65535). Otherwise an error is thrown because reinterpreting
   * a 32-bit buffer as 16-bit would silently corrupt the geometry.
   */
  private uploadBuffers(snapshot: DrawListSnapshot, useUint32: boolean): void {
    const gl = this.gl
    const vtxData = packVertexBuffer(snapshot.vertices)
    const idxData = packIndexBufferForType(snapshot.indices, useUint32)

    if (!this.vbo) this.vbo = gl.createBuffer()
    if (!this.ibo) this.ibo = gl.createBuffer()

    gl.bindBuffer(gl.ARRAY_BUFFER, this.vbo)
    gl.bufferData(gl.ARRAY_BUFFER, vtxData, gl.STATIC_DRAW)

    gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, this.ibo)
    gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, idxData, gl.STATIC_DRAW)
  }

  /**
   * Render a DrawListSnapshot to the canvas.
   *
   * Processes commands sequentially: geometry commands are drawn, structural
   * commands (masks, composites) are counted as gaps.
   *
   * @param snapshot the DrawList data to render
   * @param flipY if true, flip Y axis (top-left origin → clip-space)
   * @returns render statistics including gap information
   */
  render(snapshot: DrawListSnapshot, flipY = false): RenderResult {
    const gl = this.gl
    const w = this.canvas.width
    const h = this.canvas.height

    // Determine index type for this frame. Prefer uint32 (OES_element_index_uint);
    // fall back to uint16 when the extension is absent.
    const useUint32 = gl2OrUint32(gl)
    this.indexType = useUint32 ? gl.UNSIGNED_INT : gl.UNSIGNED_SHORT

    // Clear to transparent.
    gl.viewport(0, 0, w, h)
    gl.clearColor(0, 0, 0, 0)
    gl.clear(gl.COLOR_BUFFER_BIT)
    gl.enable(gl.BLEND)

    // Upload buffers (re-packs to Uint16 when uint32 is unavailable).
    this.uploadBuffers(snapshot, useUint32)

    // Projection: use snapshot bounds, or canvas dimensions if degenerate.
    const [minX, minY, maxX, maxY] = snapshot.bounds
    const proj = orthoProjection(minX, maxX, minY, maxY, flipY)

    // Classify commands.
    const { commands } = classifyAllCommands(snapshot)

    const unsupportedBlendSet = new Set<string>()
    let maskCount = 0
    let compositeCount = 0
    let drawnCommands = 0
    let skippedCommands = 0

    for (const classified of commands) {
      const result = this.renderCommand(classified, proj)
      if (result.drawn) {
        drawnCommands++
      } else {
        skippedCommands++
      }
      if (result.unsupportedBlend) {
        unsupportedBlendSet.add(blendModeName(classified.cmd.blendMode))
      }
      if (classified.kind === 'mask-define' || classified.kind === 'mask-push' || classified.kind === 'mask-pop') {
        maskCount++
      }
      if (classified.kind === 'composite-begin' || classified.kind === 'composite-end' || classified.kind === 'composite-blit') {
        compositeCount++
      }
    }

    return {
      drawnCommands,
      skippedCommands,
      gaps: {
        unsupportedBlendModes: [...unsupportedBlendSet],
        maskCommands: maskCount,
        compositeCommands: compositeCount,
        totalCommands: commands.length,
      },
    }
  }

  /**
   * Render a single classified command. Returns whether it was drawn and
   * whether its blend mode was unsupported.
   *
   * Mask structural commands (PushMask, DefineMask, PopMask) are now fully
   * executed:
   *   - PushMask: allocates a mask FBO + content FBO, clears both, pushes onto
   *     the mask stack.
   *   - DefineMask: renders the command's geometry into the mask buffer (the
   *     alpha channel defines the mask shape).
   *   - PopMask: runs the mask-composite shader (content × maskAlpha, then
   *     source-over onto the parent target), then pops and frees the level.
   *
   * Normal geometry within a mask region is rendered into the content FBO
   * instead of the main canvas. When no mask is active, geometry goes to the
   * canvas as before.
   */
  private renderCommand(
    classified: ClassifiedCommand,
    proj: Float32Array,
  ): { drawn: boolean; unsupportedBlend: boolean } {
    const cmd = classified.cmd

    // Handle mask structural commands.
    if (classified.kind === 'mask-push') {
      this.handlePushMask(cmd.maskMode)
      return { drawn: false, unsupportedBlend: false }
    }
    if (classified.kind === 'mask-pop') {
      this.handlePopMask()
      return { drawn: false, unsupportedBlend: false }
    }
    if (classified.kind === 'mask-define') {
      // DefineMask renders geometry into the mask buffer.
      if (cmd.elemCount > 0) {
        this.drawMaskDefineGeometry(cmd, proj)
      }
      const top = this.maskStack[this.maskStack.length - 1]
      if (top) top.hasMaskGeometry = true
      return { drawn: cmd.elemCount > 0, unsupportedBlend: false }
    }

    // Composite structural commands are still not implemented.
    if (classified.kind !== 'geometry') {
      return { drawn: false, unsupportedBlend: false }
    }

    // Empty commands carry no geometry.
    if (cmd.elemCount === 0) {
      return { drawn: false, unsupportedBlend: false }
    }

    // If a mask is active, redirect geometry to the content FBO.
    const maskLevel = this.maskStack[this.maskStack.length - 1]
    if (maskLevel) {
      const gl = this.gl
      gl.bindFramebuffer(gl.FRAMEBUFFER, maskLevel.targets.contentFbo)
      gl.viewport(0, 0, maskLevel.targets.width, maskLevel.targets.height)
    }

    const impl = getBlendModeImpl(cmd.blendMode)
    if (impl === 'native') {
      const params = this.resolveNativeParams(cmd.blendMode)
      if (params) {
        this.drawGeometryNative(cmd, proj, params)
        if (maskLevel) this.restoreCanvasTarget()
        return { drawn: true, unsupportedBlend: false }
      }
      // MIN/MAX unsupported on this context — fall through to shader path.
    }
    if (maskLevel) {
      this.drawGeometryShaderBlended(
        cmd, proj, cmd.blendMode,
        maskLevel.targets.contentFbo,
        maskLevel.targets.width,
        maskLevel.targets.height,
      )
    } else {
      this.drawGeometryShaderBlended(cmd, proj, cmd.blendMode)
    }
    if (maskLevel) this.restoreCanvasTarget()
    return { drawn: true, unsupportedBlend: false }
  }

  /**
   * Resolve native blend params for a mode, accounting for context support.
   * Returns null when the required equation is unavailable (caller falls
   * back to the shader path).
   */
  private resolveNativeParams(mode: BlendMode): NativeBlendParams | null {
    const gl = this.gl
    // Build a GLBlendKnobs view of the context. In WebGL2 MIN/MAX are core;
    // in WebGL1 they live on the EXT_blend_minmax extension object. We pass
    // the equation constants directly so the renderer never dereferences an
    // extension at draw time.
    const knobs: GLBlendKnobs = {
      FUNC_ADD: gl.FUNC_ADD,
      FUNC_REVERSE_SUBTRACT: gl.FUNC_REVERSE_SUBTRACT,
      MIN: (gl as unknown as GL2).MIN ?? 0x8007,
      MAX: (gl as unknown as GL2).MAX ?? 0x8008,
      ONE: gl.ONE,
      ZERO: gl.ZERO,
      ONE_MINUS_SRC_ALPHA: gl.ONE_MINUS_SRC_ALPHA,
      ONE_MINUS_DST_COLOR: gl.ONE_MINUS_DST_COLOR,
      SRC_ALPHA: gl.SRC_ALPHA,
      DST_COLOR: gl.DST_COLOR,
    }
    const params = getNativeBlendParams(mode, knobs)
    if (!params) return null
    // Darken/Lighten require MIN/MAX equations.
    if (
      (params.equation === knobs.MIN || params.equation === knobs.MAX) &&
      !this.hasMinMaxBlend
    ) {
      return null
    }
    return params
  }

  /** Draw geometry with a fixed-function blend configuration. */
  private drawGeometryNative(
    cmd: ClassifiedCommand['cmd'],
    proj: Float32Array,
    params: NativeBlendParams,
  ): void {
    const gl = this.gl
    gl.blendEquation(params.equation)
    gl.blendFunc(params.srcFactor, params.dstFactor)
    this.drawGeometry(cmd, proj)
    // Restore the default equation so subsequent commands are unaffected.
    gl.blendEquation(gl.FUNC_ADD)
  }

  /**
   * Draw geometry using the FBO render-to-texture + blend-shader path.
   *
   * 1. Copy the current render target content into `dstTexture`.
   * 2. Render the source geometry into the source FBO.
   * 3. Composite source over destination with the mode's blend shader via a
   *    full-screen pass (blending disabled — the shader computes the final
   *    composite).
   *
   * When `targetFbo` and `targetW`/`targetH` are provided, the copy and
   * composite operate on that FBO instead of the default canvas (null).
   * This is used when rendering geometry inside a mask content buffer.
   */
  private drawGeometryShaderBlended(
    cmd: ClassifiedCommand['cmd'],
    proj: Float32Array,
    mode: BlendMode,
    targetFbo: WebGLFramebuffer | null = null,
    targetW?: number,
    targetH?: number,
  ): void {
    const gl = this.gl
    const w = targetW ?? this.canvas.width
    const h = targetH ?? this.canvas.height
    const targets = this.ensureBlendTargets(w, h)

    // 1. Snapshot the current target into dstTexture.
    gl.activeTexture(gl.TEXTURE0)
    gl.bindTexture(gl.TEXTURE_2D, targets.dstTexture)
    gl.copyTexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, 0, 0, w, h)

    // 2. Render the source geometry into the FBO, cleared to transparent.
    gl.bindFramebuffer(gl.FRAMEBUFFER, targets.fbo)
    gl.viewport(0, 0, w, h)
    gl.clearColor(0, 0, 0, 0)
    gl.clear(gl.COLOR_BUFFER_BIT)
    // Source-over accumulation within the source buffer (self-overlap).
    gl.blendEquation(gl.FUNC_ADD)
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
    this.drawGeometry(cmd, proj)

    // 3. Composite onto the target with the blend shader.
    gl.bindFramebuffer(gl.FRAMEBUFFER, targetFbo)
    gl.viewport(0, 0, w, h)
    gl.disable(gl.BLEND)

    const program = this.getBlendProgram(mode)
    gl.useProgram(program)
    const uniforms = this.blendProgramUniforms.get(mode)!
    gl.activeTexture(gl.TEXTURE0)
    gl.bindTexture(gl.TEXTURE_2D, targets.srcTexture)
    gl.uniform1i(uniforms.src, 0)
    gl.activeTexture(gl.TEXTURE1)
    gl.bindTexture(gl.TEXTURE_2D, targets.dstTexture)
    gl.uniform1i(uniforms.dst, 1)

    this.drawFullscreenQuad(program)

    // Restore state for subsequent commands.
    gl.enable(gl.BLEND)
    gl.blendEquation(gl.FUNC_ADD)
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
    gl.activeTexture(gl.TEXTURE0)
  }

  /** Get (or lazily compile) the blend program for a mode. */
  private getBlendProgram(mode: BlendMode): WebGLProgram {
    let program = this.blendPrograms.get(mode)
    if (!program) {
      program = linkProgram(this.gl, VERT_SHADER_FULLSCREEN, buildBlendFragShader(mode))
      this.blendPrograms.set(mode, program)
      this.blendProgramUniforms.set(mode, {
        src: this.gl.getUniformLocation(program, 'u_src'),
        dst: this.gl.getUniformLocation(program, 'u_dst'),
      })
    }
    return program
  }

  /** Allocate (or resize) the FBO + textures used by the shader blend path. */
  private ensureBlendTargets(w: number, h: number): BlendTargets {
    const gl = this.gl
    const existing = this.blendTargets
    if (existing && existing.width === w && existing.height === h) {
      return existing
    }
    if (existing) {
      gl.deleteFramebuffer(existing.fbo)
      gl.deleteTexture(existing.srcTexture)
      gl.deleteTexture(existing.dstTexture)
    }
    const fbo = gl.createFramebuffer()
    const srcTexture = gl.createTexture()
    const dstTexture = gl.createTexture()
    if (!fbo || !srcTexture || !dstTexture) {
      throw new Error('Failed to allocate blend render targets')
    }
    gl.bindTexture(gl.TEXTURE_2D, srcTexture)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, null)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)

    gl.bindTexture(gl.TEXTURE_2D, dstTexture)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, null)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)

    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo)
    gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, srcTexture, 0)
    gl.bindFramebuffer(gl.FRAMEBUFFER, null)

    const targets: BlendTargets = { fbo, srcTexture, dstTexture, width: w, height: h }
    this.blendTargets = targets
    return targets
  }

  /** Bind the shared fullscreen quad and draw it with `program`. */
  private drawFullscreenQuad(program: WebGLProgram): void {
    const gl = this.gl
    if (!this.quadVbo) {
      this.quadVbo = gl.createBuffer()
      gl.bindBuffer(gl.ARRAY_BUFFER, this.quadVbo)
      // NDC quad: two triangles covering [-1,1]².
      gl.bufferData(
        gl.ARRAY_BUFFER,
        new Float32Array([-1, -1, 1, -1, 1, 1, -1, -1, 1, 1, -1, 1]),
        gl.STATIC_DRAW,
      )
    } else {
      gl.bindBuffer(gl.ARRAY_BUFFER, this.quadVbo)
    }
    const posLoc = gl.getAttribLocation(program, 'a_pos')
    gl.enableVertexAttribArray(posLoc)
    gl.vertexAttribPointer(posLoc, 2, gl.FLOAT, false, 0, 0)
    gl.drawArrays(gl.TRIANGLES, 0, 6)
  }

  // ---------------------------------------------------------------------------
  // Mask rendering
  // ---------------------------------------------------------------------------

  /**
   * PushMask: allocate a mask FBO + content FBO at canvas dimensions, clear
   * both to transparent, and push onto the mask stack.
   */
  private handlePushMask(maskMode: MaskingMode): void {
    const gl = this.gl
    const w = this.canvas.width
    const h = this.canvas.height
    const targets = this.ensureMaskTargets(w, h)

    // Clear the mask buffer.
    gl.bindFramebuffer(gl.FRAMEBUFFER, targets.maskFbo)
    gl.viewport(0, 0, w, h)
    gl.clearColor(0, 0, 0, 0)
    gl.clear(gl.COLOR_BUFFER_BIT)

    // Clear the content buffer.
    gl.bindFramebuffer(gl.FRAMEBUFFER, targets.contentFbo)
    gl.viewport(0, 0, w, h)
    gl.clearColor(0, 0, 0, 0)
    gl.clear(gl.COLOR_BUFFER_BIT)

    this.maskStack.push({ targets, maskMode, hasMaskGeometry: false })
  }

  /**
   * DefineMask: render the command's geometry into the current mask level's
   * mask FBO. The alpha channel of the rendered geometry defines the mask
   * shape. Uses source-over accumulation for self-overlapping mask geometry.
   */
  private drawMaskDefineGeometry(
    cmd: ClassifiedCommand['cmd'],
    proj: Float32Array,
  ): void {
    const level = this.maskStack[this.maskStack.length - 1]
    if (!level) return

    const gl = this.gl
    gl.bindFramebuffer(gl.FRAMEBUFFER, level.targets.maskFbo)
    gl.viewport(0, 0, level.targets.width, level.targets.height)
    gl.enable(gl.BLEND)
    gl.blendEquation(gl.FUNC_ADD)
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA)

    // DefineMask commands carry geometry like Normal commands; render with
    // Normal blend (source-over) to accumulate mask alpha.
    this.drawGeometry(cmd, proj)
  }

  /**
   * PopMask: run the mask-composite shader to multiply the content buffer by
   * the mask buffer's alpha (or 1−alpha for Dodge mode), then source-over
   * composite the result onto the parent render target (the main canvas, or
   * the parent mask level's content FBO for nested masks).
   *
   * After compositing, the mask level is popped and its GPU resources freed.
   */
  private handlePopMask(): void {
    const level = this.maskStack.pop()
    if (!level) return

    const gl = this.gl
    const w = level.targets.width
    const h = level.targets.height

    // Determine the parent render target.
    const parentLevel = this.maskStack[this.maskStack.length - 1]
    const parentFbo = parentLevel ? parentLevel.targets.contentFbo : null

    // Copy the parent target into the blend dst texture for the composite shader.
    gl.bindFramebuffer(gl.FRAMEBUFFER, parentFbo)
    gl.viewport(0, 0, w, h)
    const blendTargets = this.ensureBlendTargets(w, h)
    gl.activeTexture(gl.TEXTURE0)
    gl.bindTexture(gl.TEXTURE_2D, blendTargets.dstTexture)
    gl.copyTexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, 0, 0, w, h)

    // Run the mask-composite shader onto the parent target.
    gl.bindFramebuffer(gl.FRAMEBUFFER, parentFbo)
    gl.viewport(0, 0, w, h)
    gl.disable(gl.BLEND)

    const program = this.ensureMaskCompositeProgram()
    gl.useProgram(program)
    const u = this.maskCompositeUniforms!

    gl.activeTexture(gl.TEXTURE0)
    gl.bindTexture(gl.TEXTURE_2D, level.targets.contentTexture)
    gl.uniform1i(u.content, 0)
    gl.activeTexture(gl.TEXTURE1)
    gl.bindTexture(gl.TEXTURE_2D, level.targets.maskTexture)
    gl.uniform1i(u.mask, 1)
    gl.activeTexture(gl.TEXTURE2)
    gl.bindTexture(gl.TEXTURE_2D, blendTargets.dstTexture)
    gl.uniform1i(u.dst, 2)
    gl.uniform1f(u.dodge, level.maskMode === 1 /* MaskingMode.Dodge */ ? 1.0 : 0.0)

    this.drawFullscreenQuad(program)

    // Restore state.
    gl.enable(gl.BLEND)
    gl.blendEquation(gl.FUNC_ADD)
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
    gl.activeTexture(gl.TEXTURE0)

    // Free the popped level's GPU resources.
    this.deleteMaskTargets(level.targets)
  }

  /** Restore the render target to the canvas (or the parent mask content FBO). */
  private restoreCanvasTarget(): void {
    const gl = this.gl
    const parentLevel = this.maskStack[this.maskStack.length - 1]
    if (parentLevel) {
      gl.bindFramebuffer(gl.FRAMEBUFFER, parentLevel.targets.contentFbo)
      gl.viewport(0, 0, parentLevel.targets.width, parentLevel.targets.height)
    } else {
      gl.bindFramebuffer(gl.FRAMEBUFFER, null)
      gl.viewport(0, 0, this.canvas.width, this.canvas.height)
    }
  }

  /** Lazily compile the mask-composite program. */
  private ensureMaskCompositeProgram(): WebGLProgram {
    if (this.maskCompositeProgram) return this.maskCompositeProgram
    const program = linkProgram(this.gl, VERT_SHADER_FULLSCREEN, MASK_COMPOSITE_FRAG_SHADER)
    this.maskCompositeProgram = program
    this.maskCompositeUniforms = {
      content: this.gl.getUniformLocation(program, 'u_content'),
      mask: this.gl.getUniformLocation(program, 'u_mask'),
      dst: this.gl.getUniformLocation(program, 'u_dst'),
      dodge: this.gl.getUniformLocation(program, 'u_dodge'),
    }
    return program
  }

  /** Allocate (or resize) the mask + content FBOs. */
  private ensureMaskTargets(w: number, h: number): MaskTargets {
    const gl = this.gl
    const maskFbo = gl.createFramebuffer()
    const maskTexture = gl.createTexture()
    const contentFbo = gl.createFramebuffer()
    const contentTexture = gl.createTexture()
    if (!maskFbo || !maskTexture || !contentFbo || !contentTexture) {
      throw new Error('Failed to allocate mask render targets')
    }

    // Mask texture.
    gl.bindTexture(gl.TEXTURE_2D, maskTexture)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, null)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)

    // Mask FBO.
    gl.bindFramebuffer(gl.FRAMEBUFFER, maskFbo)
    gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, maskTexture, 0)

    // Content texture.
    gl.bindTexture(gl.TEXTURE_2D, contentTexture)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, null)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)

    // Content FBO.
    gl.bindFramebuffer(gl.FRAMEBUFFER, contentFbo)
    gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, contentTexture, 0)

    gl.bindFramebuffer(gl.FRAMEBUFFER, null)

    return { maskFbo, maskTexture, contentFbo, contentTexture, width: w, height: h }
  }

  /** Free mask render target GPU resources. */
  private deleteMaskTargets(targets: MaskTargets): void {
    const gl = this.gl
    gl.deleteFramebuffer(targets.maskFbo)
    gl.deleteTexture(targets.maskTexture)
    gl.deleteFramebuffer(targets.contentFbo)
    gl.deleteTexture(targets.contentTexture)
  }

  /** Issue the geometry draw call for a command (no blend state changes). */
  private drawGeometry(cmd: ClassifiedCommand['cmd'], proj: Float32Array): void {
    const gl = this.gl

    // Decode PartVars.
    const vars = decodePartVars(cmd.variables)

    // Determine if we have a valid texture.
    const texSlot = cmd.sourceIds.length > 0 ? (cmd.sourceIds[0] ?? -1) : -1
    const glTex = texSlot >= 0 ? this.textures.get(texSlot) : undefined

    if (glTex) {
      // Textured draw.
      gl.useProgram(this.texProgram)
      gl.uniformMatrix4fv(this.texUniforms.proj, false, proj)
      gl.uniform1i(this.texUniforms.tex, 0)
      gl.uniform3f(this.texUniforms.tint, vars.tint[0], vars.tint[1], vars.tint[2])
      gl.uniform1f(this.texUniforms.opacity, vars.opacity)

      gl.activeTexture(gl.TEXTURE0)
      gl.bindTexture(gl.TEXTURE_2D, glTex)
    } else {
      // Flat-color draw (no texture).
      gl.useProgram(this.flatProgram)
      gl.uniformMatrix4fv(this.flatUniforms.proj, false, proj)
      gl.uniform3f(this.flatUniforms.tint, vars.tint[0], vars.tint[1], vars.tint[2])
      gl.uniform1f(this.flatUniforms.opacity, vars.opacity)
    }

    // Bind attributes.
    const program = glTex ? this.texProgram : this.flatProgram
    const posLoc = gl.getAttribLocation(program, 'a_pos')
    const uvLoc = gl.getAttribLocation(program, 'a_uv')

    gl.bindBuffer(gl.ARRAY_BUFFER, this.vbo)
    gl.enableVertexAttribArray(posLoc)
    gl.vertexAttribPointer(posLoc, 2, gl.FLOAT, false, BYTES_PER_VERTEX, 0)
    if (uvLoc >= 0) {
      gl.enableVertexAttribArray(uvLoc)
      gl.vertexAttribPointer(uvLoc, 2, gl.FLOAT, false, BYTES_PER_VERTEX, 2 * 4)
    }

    // Draw elements using the index type determined for this frame.
    gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, this.ibo)
    if (this.indexType === gl.UNSIGNED_INT) {
      gl.drawElements(gl.TRIANGLES, cmd.elemCount, gl.UNSIGNED_INT, cmd.idxOffset * 4)
    } else {
      // 16-bit fallback: indices were re-packed in uploadBuffers.
      gl.drawElements(gl.TRIANGLES, cmd.elemCount, gl.UNSIGNED_SHORT, cmd.idxOffset * 2)
    }
  }

  /** Release all GPU resources. */
  dispose(): void {
    const gl = this.gl
    if (this.vbo) gl.deleteBuffer(this.vbo)
    if (this.ibo) gl.deleteBuffer(this.ibo)
    if (this.quadVbo) gl.deleteBuffer(this.quadVbo)
    for (const tex of this.textures.values()) {
      gl.deleteTexture(tex)
    }
    this.textures.clear()
    if (this.blendTargets) {
      gl.deleteFramebuffer(this.blendTargets.fbo)
      gl.deleteTexture(this.blendTargets.srcTexture)
      gl.deleteTexture(this.blendTargets.dstTexture)
      this.blendTargets = null
    }
    // Clean up any remaining mask stack entries.
    for (const level of this.maskStack) {
      this.deleteMaskTargets(level.targets)
    }
    this.maskStack = []
    if (this.maskCompositeProgram) {
      gl.deleteProgram(this.maskCompositeProgram)
      this.maskCompositeProgram = null
      this.maskCompositeUniforms = null
    }
    for (const program of this.blendPrograms.values()) {
      gl.deleteProgram(program)
    }
    this.blendPrograms.clear()
    this.blendProgramUniforms.clear()
    gl.deleteProgram(this.texProgram)
    gl.deleteProgram(this.flatProgram)
  }
}

/**
 * Whether the index buffer should be uint32. In WebGL2 the extension is
 * built-in; in WebGL1 it requires `OES_element_index_uint`.
 */
function gl2OrUint32(gl: GL): boolean {
  // WebGL2 exposes OES_element_index_uint as core; getExtension still returns
  // an object for compat. In WebGL1 the extension must be present.
  return gl.getExtension('OES_element_index_uint') !== null
}
