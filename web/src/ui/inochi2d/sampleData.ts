/**
 * Sample DrawListSnapshot data for testing and demo purposes.
 *
 * Each sample is a self-contained, JSON-serializable DrawListSnapshot that
 * exercises different aspects of the viewer:
 *   - sampleTexturedQuad: basic textured quad with Normal blend
 *   - sampleFlatTriangle: flat-color triangle (no texture)
 *   - sampleMultiCommand: multiple parts with different blend modes
 *   - sampleWithGaps: includes mask and composite commands to verify gap reporting
 */

import type { DrawListSnapshot } from './types'
import { DrawState, BlendMode, MaskingMode } from './types'

/** A 2×2 red RGBA texture as a data URL (PNG). */
function makeRedTextureDataUrl(): string {
  // Minimal 1×1 solid red PNG (base64).
  return 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=='
}

/** A 2×2 green RGBA texture as a data URL (PNG). */
function makeGreenTextureDataUrl(): string {
  return 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=='
}

/** Default PartVars: white tint, full opacity. */
function defaultPartVars(): number[] {
  return [
    1, 1, 1, // tint
    0, 0, 0, // screenTint
    0, // padding
    1, // opacity
    0, // emission
    0, 0, 0, 0, 0, 0, 0, // unused
  ]
}

/**
 * Sample: a single textured quad centered at origin.
 * 4 vertices, 2 triangles, one Normal-blend draw command.
 */
export const sampleTexturedQuad: DrawListSnapshot = {
  vertices: [
    { vtx: [-100, -100], uv: [0, 1] },
    { vtx: [100, -100], uv: [1, 1] },
    { vtx: [100, 100], uv: [1, 0] },
    { vtx: [-100, 100], uv: [0, 0] },
  ],
  indices: [0, 1, 2, 0, 2, 3],
  allocations: [
    { vtxOffset: 0, idxOffset: 0, idxCount: 6, vtxCount: 4, allocId: 0 },
  ],
  commands: [
    {
      sourceIds: [0],
      state: DrawState.Normal,
      blendMode: BlendMode.Normal,
      maskMode: MaskingMode.Mask,
      allocId: 0,
      vtxOffset: 0,
      idxOffset: 0,
      elemCount: 6,
      typeId: 1,
      variables: defaultPartVars(),
    },
  ],
  textures: [
    { id: 0, width: 1, height: 1, src: makeRedTextureDataUrl() },
  ],
  bounds: [-128, -128, 128, 128],
}

/**
 * Sample: a flat-color triangle (no texture).
 * 3 vertices, 1 triangle, Normal blend, red tint.
 */
export const sampleFlatTriangle: DrawListSnapshot = {
  vertices: [
    { vtx: [0, 80], uv: [0.5, 0] },
    { vtx: [-80, -60], uv: [0, 1] },
    { vtx: [80, -60], uv: [1, 1] },
  ],
  indices: [0, 1, 2],
  allocations: [
    { vtxOffset: 0, idxOffset: 0, idxCount: 3, vtxCount: 3, allocId: 0 },
  ],
  commands: [
    {
      sourceIds: [-1],
      state: DrawState.Normal,
      blendMode: BlendMode.Normal,
      maskMode: MaskingMode.Mask,
      allocId: 0,
      vtxOffset: 0,
      idxOffset: 0,
      elemCount: 3,
      typeId: 1,
      variables: [
        0.2, 0.8, 0.4, // tint (green-ish)
        0, 0, 0, // screenTint
        0, // padding
        0.7, // opacity
        0, // emission
        0, 0, 0, 0, 0, 0, 0, // unused
      ],
    },
  ],
  textures: [],
  bounds: [-100, -80, 100, 100],
}

/**
 * Sample: two overlapping parts with different blend modes.
 * The second part uses Multiply (unsupported by spike → fallback to Normal).
 */
export const sampleMultiCommand: DrawListSnapshot = {
  vertices: [
    // Part 0: blue quad
    { vtx: [-60, -60], uv: [0, 1] },
    { vtx: [60, -60], uv: [1, 1] },
    { vtx: [60, 60], uv: [1, 0] },
    { vtx: [-60, 60], uv: [0, 0] },
    // Part 1: red quad (overlapping, offset)
    { vtx: [-20, -20], uv: [0, 1] },
    { vtx: [100, -20], uv: [1, 1] },
    { vtx: [100, 100], uv: [1, 0] },
    { vtx: [-20, 100], uv: [0, 0] },
  ],
  indices: [
    0, 1, 2, 0, 2, 3, // Part 0
    4, 5, 6, 4, 6, 7, // Part 1
  ],
  allocations: [
    { vtxOffset: 0, idxOffset: 0, idxCount: 6, vtxCount: 4, allocId: 0 },
    { vtxOffset: 4, idxOffset: 6, idxCount: 6, vtxCount: 4, allocId: 1 },
  ],
  commands: [
    {
      sourceIds: [0],
      state: DrawState.Normal,
      blendMode: BlendMode.Normal,
      maskMode: MaskingMode.Mask,
      allocId: 0,
      vtxOffset: 0,
      idxOffset: 0,
      elemCount: 6,
      typeId: 1,
      variables: defaultPartVars(),
    },
    {
      sourceIds: [1],
      state: DrawState.Normal,
      blendMode: BlendMode.Multiply,
      maskMode: MaskingMode.Mask,
      allocId: 1,
      vtxOffset: 4,
      idxOffset: 6,
      elemCount: 6,
      typeId: 1,
      variables: defaultPartVars(),
    },
  ],
  textures: [
    { id: 0, width: 1, height: 1, src: makeRedTextureDataUrl() },
    { id: 1, width: 1, height: 1, src: makeGreenTextureDataUrl() },
  ],
  bounds: [-128, -128, 128, 128],
}

/**
 * Sample: includes mask and composite commands to verify gap reporting.
 * The mask/composite commands are structural — they carry no geometry but
 * must be counted and surfaced to the user.
 */
export const sampleWithGaps: DrawListSnapshot = {
  vertices: [
    { vtx: [-50, -50], uv: [0, 1] },
    { vtx: [50, -50], uv: [1, 1] },
    { vtx: [50, 50], uv: [1, 0] },
    { vtx: [-50, 50], uv: [0, 0] },
  ],
  indices: [0, 1, 2, 0, 2, 3],
  allocations: [
    { vtxOffset: 0, idxOffset: 0, idxCount: 6, vtxCount: 4, allocId: 0 },
  ],
  commands: [
    // Composite begin (gap)
    {
      sourceIds: [],
      state: DrawState.CompositeBegin,
      blendMode: BlendMode.Normal,
      maskMode: MaskingMode.Mask,
      allocId: 0,
      vtxOffset: 0,
      idxOffset: 0,
      elemCount: 0,
      typeId: 0,
      variables: [],
    },
    // PushMask (gap)
    {
      sourceIds: [],
      state: DrawState.PushMask,
      blendMode: BlendMode.Normal,
      maskMode: MaskingMode.Mask,
      allocId: 0,
      vtxOffset: 0,
      idxOffset: 0,
      elemCount: 0,
      typeId: 0,
      variables: [],
    },
    // DefineMask (gap)
    {
      sourceIds: [-1],
      state: DrawState.DefineMask,
      blendMode: BlendMode.Normal,
      maskMode: MaskingMode.Mask,
      allocId: 0,
      vtxOffset: 0,
      idxOffset: 0,
      elemCount: 6,
      typeId: 1,
      variables: defaultPartVars(),
    },
    // PopMask (gap)
    {
      sourceIds: [],
      state: DrawState.PopMask,
      blendMode: BlendMode.Normal,
      maskMode: MaskingMode.Mask,
      allocId: 0,
      vtxOffset: 0,
      idxOffset: 0,
      elemCount: 0,
      typeId: 0,
      variables: [],
    },
    // Normal geometry draw
    {
      sourceIds: [-1],
      state: DrawState.Normal,
      blendMode: BlendMode.AddGlow,
      maskMode: MaskingMode.Mask,
      allocId: 0,
      vtxOffset: 0,
      idxOffset: 0,
      elemCount: 6,
      typeId: 1,
      variables: defaultPartVars(),
    },
    // Composite blit (gap)
    {
      sourceIds: [],
      state: DrawState.CompositeBlit,
      blendMode: BlendMode.Normal,
      maskMode: MaskingMode.Mask,
      allocId: 0,
      vtxOffset: 0,
      idxOffset: 0,
      elemCount: 0,
      typeId: 0,
      variables: [],
    },
    // Composite end (gap)
    {
      sourceIds: [],
      state: DrawState.CompositeEnd,
      blendMode: BlendMode.Normal,
      maskMode: MaskingMode.Mask,
      allocId: 0,
      vtxOffset: 0,
      idxOffset: 0,
      elemCount: 0,
      typeId: 0,
      variables: [],
    },
  ],
  textures: [],
  bounds: [-80, -80, 80, 80],
}

export const ALL_SAMPLES: Array<{ label: string; data: DrawListSnapshot }> = [
  { label: 'Textured Quad', data: sampleTexturedQuad },
  { label: 'Flat Triangle', data: sampleFlatTriangle },
  { label: 'Multi Command', data: sampleMultiCommand },
  { label: 'With Gaps', data: sampleWithGaps },
]
