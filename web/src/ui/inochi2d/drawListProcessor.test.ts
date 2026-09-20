import { describe, it, expect } from 'vitest'
import {
  packVertexBuffer,
  packIndexBuffer,
  classifyCommand,
  classifyAllCommands,
  decodePartVars,
  orthoProjection,
  mapBlendFunc,
  blendModeName,
  isBlendSupported,
  maxIndex,
  MAX_UINT16_INDEX,
  FLOATS_PER_VERTEX,
  BYTES_PER_VERTEX,
  PARTVARS,
} from './drawListProcessor'
import { DrawState, BlendMode, MaskingMode } from './types'
import type { DrawCmd, DrawListSnapshot, VtxData } from './types'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeCmd(overrides: Partial<DrawCmd> = {}): DrawCmd {
  return {
    sourceIds: [0],
    state: DrawState.Normal,
    blendMode: BlendMode.Normal,
    maskMode: MaskingMode.Mask,
    allocId: 0,
    vtxOffset: 0,
    idxOffset: 0,
    elemCount: 6,
    typeId: 1,
    variables: [1, 1, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0],
    ...overrides,
  }
}

function makeSnapshot(
  vertices: VtxData[],
  commands: DrawCmd[],
  indices: number[] = [0, 1, 2],
): DrawListSnapshot {
  return {
    vertices,
    indices,
    commands,
    allocations: [],
    textures: [],
    bounds: [-100, -100, 100, 100],
  }
}

const MOCK_GL = {
  ONE: 1,
  ZERO: 0,
  ONE_MINUS_SRC_ALPHA: 0x803,
  SRC_ALPHA: 0x302,
} as const

// ---------------------------------------------------------------------------
// packVertexBuffer
// ---------------------------------------------------------------------------

describe('packVertexBuffer', () => {
  it('packs vertices into interleaved Float32Array', () => {
    const vertices: VtxData[] = [
      { vtx: [10, 20], uv: [0.5, 0.25] },
      { vtx: [30, 40], uv: [0.75, 0.5] },
    ]
    const buf = packVertexBuffer(vertices)
    expect(buf).toBeInstanceOf(Float32Array)
    expect(buf.length).toBe(2 * FLOATS_PER_VERTEX)
    expect([...buf]).toEqual([
      10, 20, 0.5, 0.25,
      30, 40, 0.75, 0.5,
    ])
  })

  it('returns empty array for zero vertices', () => {
    const buf = packVertexBuffer([])
    expect(buf.length).toBe(0)
  })

  it('produces correct byte stride', () => {
    expect(BYTES_PER_VERTEX).toBe(16)
    expect(FLOATS_PER_VERTEX).toBe(4)
  })
})

// ---------------------------------------------------------------------------
// packIndexBuffer
// ---------------------------------------------------------------------------

describe('packIndexBuffer', () => {
  it('packs indices into Uint32Array', () => {
    const buf = packIndexBuffer([0, 1, 2, 0, 2, 3])
    expect(buf).toBeInstanceOf(Uint32Array)
    expect([...buf]).toEqual([0, 1, 2, 0, 2, 3])
  })
})

// ---------------------------------------------------------------------------
// maxIndex
// ---------------------------------------------------------------------------

describe('maxIndex', () => {
  it('returns the maximum index value', () => {
    expect(maxIndex([0, 1, 2, 0, 2, 3])).toBe(3)
    expect(maxIndex([5, 10, 3, 7])).toBe(10)
  })

  it('returns 0 for empty array', () => {
    expect(maxIndex([])).toBe(0)
  })

  it('handles large indices exceeding uint16 range', () => {
    expect(maxIndex([0, 70000, 2])).toBe(70000)
  })

  it('MAX_UINT16_INDEX constant is correct', () => {
    expect(MAX_UINT16_INDEX).toBe(65535)
  })
})

// ---------------------------------------------------------------------------
// classifyCommand
// ---------------------------------------------------------------------------

describe('classifyCommand', () => {
  it('classifies Normal state as geometry', () => {
    const c = classifyCommand(makeCmd({ state: DrawState.Normal }))
    expect(c.kind).toBe('geometry')
    expect(c.blendSupported).toBe(true)
  })

  it('classifies DefineMask as mask-define', () => {
    const c = classifyCommand(makeCmd({ state: DrawState.DefineMask }))
    expect(c.kind).toBe('mask-define')
  })

  it('classifies PushMask as mask-push', () => {
    const c = classifyCommand(makeCmd({ state: DrawState.PushMask }))
    expect(c.kind).toBe('mask-push')
  })

  it('classifies PopMask as mask-pop', () => {
    const c = classifyCommand(makeCmd({ state: DrawState.PopMask }))
    expect(c.kind).toBe('mask-pop')
  })

  it('classifies CompositeBegin', () => {
    const c = classifyCommand(makeCmd({ state: DrawState.CompositeBegin }))
    expect(c.kind).toBe('composite-begin')
  })

  it('classifies CompositeEnd', () => {
    const c = classifyCommand(makeCmd({ state: DrawState.CompositeEnd }))
    expect(c.kind).toBe('composite-end')
  })

  it('classifies CompositeBlit', () => {
    const c = classifyCommand(makeCmd({ state: DrawState.CompositeBlit }))
    expect(c.kind).toBe('composite-blit')
  })

  it('marks non-Normal blend as supported (Multiply now implemented)', () => {
    const c = classifyCommand(makeCmd({ state: DrawState.Normal, blendMode: BlendMode.Multiply }))
    expect(c.kind).toBe('geometry')
    expect(c.blendSupported).toBe(true)
  })

  it('marks DestinationIn as supported (consistent with mapBlendFunc)', () => {
    const c = classifyCommand(makeCmd({ state: DrawState.Normal, blendMode: BlendMode.DestinationIn }))
    expect(c.blendSupported).toBe(true)
  })

  it('marks SourceIn (ClipToLower) as supported (shader path)', () => {
    const c = classifyCommand(makeCmd({ state: DrawState.Normal, blendMode: BlendMode.SourceIn }))
    expect(c.blendSupported).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// isBlendSupported (single source of truth)
// ---------------------------------------------------------------------------

describe('isBlendSupported', () => {
  it('returns true for Normal', () => {
    expect(isBlendSupported(BlendMode.Normal)).toBe(true)
  })

  it('returns true for DestinationIn', () => {
    expect(isBlendSupported(BlendMode.DestinationIn)).toBe(true)
  })

  it('returns true for all 19 modes (full blend-mode implementation)', () => {
    const allModes = Object.values(BlendMode).filter(
      (v): v is BlendMode => typeof v === 'number',
    )
    for (const mode of allModes) {
      expect(isBlendSupported(mode)).toBe(true)
    }
  })

  it('returns false for unrecognized enum values', () => {
    expect(isBlendSupported(0xff as BlendMode)).toBe(false)
  })

  it('mapBlendFunc reports supported=true only for factor-mappable modes', () => {
    // mapBlendFunc covers only the fixed-function factor pair subset
    // (Normal, DestinationIn); the other 17 modes are implemented via blend
    // equations (getNativeBlendParams) or shader path (blendGlslBody).
    const gl = MOCK_GL
    expect(mapBlendFunc(gl, BlendMode.Normal).supported).toBe(true)
    expect(mapBlendFunc(gl, BlendMode.DestinationIn).supported).toBe(true)
    expect(mapBlendFunc(gl, BlendMode.Multiply).supported).toBe(false)
    expect(mapBlendFunc(gl, BlendMode.Overlay).supported).toBe(false)
    expect(mapBlendFunc(gl, BlendMode.SourceIn).supported).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// classifyAllCommands
// ---------------------------------------------------------------------------

describe('classifyAllCommands', () => {
  it('counts geometry, mask, and composite commands', () => {
    const snapshot = makeSnapshot(
      [{ vtx: [0, 0], uv: [0, 0] }],
      [
        makeCmd({ state: DrawState.Normal }),
        makeCmd({ state: DrawState.PushMask }),
        makeCmd({ state: DrawState.PopMask }),
        makeCmd({ state: DrawState.CompositeBegin }),
        makeCmd({ state: DrawState.CompositeBlit }),
        makeCmd({ state: DrawState.CompositeEnd }),
        makeCmd({ state: DrawState.Normal, blendMode: BlendMode.Multiply }),
      ],
    )
    const result = classifyAllCommands(snapshot)
    expect(result.geometryCount).toBe(2)
    // All blend modes are now supported; no geometry counts as unsupported.
    expect(result.unsupportedBlendCount).toBe(0)
    expect(result.maskCount).toBe(2)
    expect(result.compositeCount).toBe(3)
    expect(result.commands).toHaveLength(7)
  })

  it('handles empty command list', () => {
    const snapshot = makeSnapshot([], [])
    const result = classifyAllCommands(snapshot)
    expect(result.geometryCount).toBe(0)
    expect(result.maskCount).toBe(0)
    expect(result.compositeCount).toBe(0)
  })

  it('does not count DestinationIn geometry as unsupported blend', () => {
    const snapshot = makeSnapshot(
      [{ vtx: [0, 0], uv: [0, 0] }],
      [
        makeCmd({ state: DrawState.Normal, blendMode: BlendMode.DestinationIn }),
      ],
    )
    const result = classifyAllCommands(snapshot)
    expect(result.geometryCount).toBe(1)
    expect(result.unsupportedBlendCount).toBe(0)
  })
})

// ---------------------------------------------------------------------------
// decodePartVars
// ---------------------------------------------------------------------------

describe('decodePartVars', () => {
  it('decodes tint, opacity, and emission from PartVars layout', () => {
    const vars = [
      0.5, 0.6, 0.7, // tint
      0.1, 0.2, 0.3, // screenTint
      0,             // padding
      0.85,          // opacity
      0.3,           // emission
      0, 0, 0, 0, 0, 0, 0,
    ]
    const decoded = decodePartVars(vars)
    expect(decoded.tint).toEqual([0.5, 0.6, 0.7])
    expect(decoded.screenTint).toEqual([0.1, 0.2, 0.3])
    expect(decoded.opacity).toBe(0.85)
    expect(decoded.emissionStrength).toBe(0.3)
  })

  it('handles short variables array by zero-padding', () => {
    const decoded = decodePartVars([0.9, 0.8, 0.7])
    expect(decoded.tint).toEqual([0.9, 0.8, 0.7])
    expect(decoded.opacity).toBe(0)
  })

  it('uses correct PartVars offsets', () => {
    expect(PARTVARS.tintOffset).toBe(0)
    expect(PARTVARS.screenTintOffset).toBe(3)
    expect(PARTVARS.opacityOffset).toBe(7)
    expect(PARTVARS.emissionOffset).toBe(8)
  })
})

// ---------------------------------------------------------------------------
// orthoProjection
// ---------------------------------------------------------------------------

describe('orthoProjection', () => {
  it('produces a 16-element column-major matrix', () => {
    const m = orthoProjection(-100, 100, -100, 100, false)
    expect(m).toBeInstanceOf(Float32Array)
    expect(m.length).toBe(16)
  })

  it('maps center of bounds to origin in clip space', () => {
    const m = orthoProjection(-100, 100, -100, 100, false)
    // Transform (0,0,0,1) → should give (tx, ty, 0, 1)
    // Column-major: m[12]=tx, m[13]=ty
    // With l=-100, r=100: -(r+l)/(r-l) = 0
    // With t=100, b=-100 (no flip): -(t+b)/(t-b) = 0
    expect(m[12]).toBeCloseTo(0, 5)
    expect(m[13]).toBeCloseTo(0, 5)
  })

  it('scales correctly for symmetric bounds', () => {
    const m = orthoProjection(-100, 100, -100, 100, false)
    // sx = 2/(r-l) = 2/200 = 0.01
    expect(m[0]).toBeCloseTo(0.01, 5)
    // Without flip: sy uses (maxY, minY) as (t, b): sy = 2/(100-(-100)) = 0.01
    expect(m[5]).toBeCloseTo(0.01, 5)
  })

  it('flips Y when flipY is true', () => {
    const m = orthoProjection(-100, 100, -100, 100, false)
    const mFlip = orthoProjection(-100, 100, -100, 100, true)
    // Y scale should be negated when flipped
    expect(mFlip[5]!).toBeCloseTo(-(m[5]!), 5)
  })

  it('maps corner of bounds to clip space edges', () => {
    const m = orthoProjection(0, 100, 0, 100, false)
    // sx = 2/100 = 0.02
    expect(m[0]).toBeCloseTo(0.02, 5)
    // Translation: -(r+l)/(r-l) = -100/100 = -1
    expect(m[12]).toBeCloseTo(-1, 5)
  })
})

// ---------------------------------------------------------------------------
// blendModeName
// ---------------------------------------------------------------------------

describe('blendModeName', () => {
  it('returns correct name for known modes', () => {
    expect(blendModeName(BlendMode.Normal)).toBe('Normal')
    expect(blendModeName(BlendMode.Multiply)).toBe('Multiply')
    expect(blendModeName(BlendMode.AddGlow)).toBe('AddGlow')
    expect(blendModeName(BlendMode.SourceIn)).toBe('SourceIn')
    expect(blendModeName(BlendMode.SourceOut)).toBe('SourceOut')
  })

  it('returns Unknown for unrecognized values', () => {
    expect(blendModeName(0xff as BlendMode)).toBe('Unknown')
  })
})

// ---------------------------------------------------------------------------
// mapBlendFunc
// ---------------------------------------------------------------------------

describe('mapBlendFunc', () => {
  it('maps Normal to premultiplied source-over', () => {
    const result = mapBlendFunc(MOCK_GL, BlendMode.Normal)
    expect(result.supported).toBe(true)
    expect(result.srcFactor).toBe(MOCK_GL.ONE)
    expect(result.dstFactor).toBe(MOCK_GL.ONE_MINUS_SRC_ALPHA)
  })

  it('maps DestinationIn correctly', () => {
    const result = mapBlendFunc(MOCK_GL, BlendMode.DestinationIn)
    expect(result.supported).toBe(true)
    expect(result.srcFactor).toBe(MOCK_GL.ZERO)
    expect(result.dstFactor).toBe(MOCK_GL.SRC_ALPHA)
  })

  it('falls back to Normal for unsupported modes', () => {
    const modes = [
      BlendMode.Multiply,
      BlendMode.Screen,
      BlendMode.Overlay,
      BlendMode.Darken,
      BlendMode.Lighten,
      BlendMode.ColorDodge,
      BlendMode.LinearDodge,
      BlendMode.AddGlow,
      BlendMode.ColorBurn,
      BlendMode.HardLight,
      BlendMode.SoftLight,
      BlendMode.Difference,
      BlendMode.Exclusion,
      BlendMode.Subtract,
      BlendMode.Inverse,
      BlendMode.SourceIn,
      BlendMode.SourceOut,
    ]
    for (const mode of modes) {
      const result = mapBlendFunc(MOCK_GL, mode)
      expect(result.supported).toBe(false)
      expect(result.srcFactor).toBe(MOCK_GL.ONE)
      expect(result.dstFactor).toBe(MOCK_GL.ONE_MINUS_SRC_ALPHA)
    }
  })
})
