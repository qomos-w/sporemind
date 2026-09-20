/**
 * Types for the serialized Inochi2D DrawList wire format.
 *
 * These TypeScript interfaces mirror the Go structs in inochi2d-go
 * (core/render/drawlist.go, core/render/state.go, core/mesh.go) and represent
 * the JSON-serializable snapshot consumed by the WebGL viewer.
 *
 * The viewer is a spike: it renders basic textured triangles with Normal blend
 * and transparent canvas. Advanced features (blend modes, masks) are now
 * implemented; composites are classified and logged as gaps — see
 * RENDERER_GAPS.md.
 */

// ---------------------------------------------------------------------------
// Enums (mirror inochi2d-go core/render/state.go)
// ---------------------------------------------------------------------------

/** DrawState — mirror of render.DrawState (drawlist.go:25-46). */
export const DrawState = {
  Normal: 0,
  DefineMask: 1,
  PushMask: 2,
  PopMask: 3,
  CompositeBegin: 4,
  CompositeEnd: 5,
  CompositeBlit: 6,
} as const
export type DrawState = (typeof DrawState)[keyof typeof DrawState]

/** BlendMode — mirror of render.BlendMode (state.go:31-72). */
export const BlendMode = {
  Normal: 0x00,
  Multiply: 0x01,
  Screen: 0x02,
  Overlay: 0x03,
  Darken: 0x04,
  Lighten: 0x05,
  ColorDodge: 0x06,
  LinearDodge: 0x07,
  AddGlow: 0x08,
  ColorBurn: 0x09,
  HardLight: 0x0a,
  SoftLight: 0x0b,
  Difference: 0x0c,
  Exclusion: 0x0d,
  Subtract: 0x0e,
  Inverse: 0x0f,
  DestinationIn: 0x10,
  SourceIn: 0x11, // ClipToLower
  SourceOut: 0x12, // SliceFromLower
} as const
export type BlendMode = (typeof BlendMode)[keyof typeof BlendMode]

/** MaskingMode — mirror of render.MaskingMode (state.go:174-181). */
export const MaskingMode = {
  Mask: 0,
  Dodge: 1,
} as const
export type MaskingMode = (typeof MaskingMode)[keyof typeof MaskingMode]

// ---------------------------------------------------------------------------
// Vertex data (mirror of core.VtxData, mesh.go:26-29)
// ---------------------------------------------------------------------------

/** Per-vertex GPU data: 2D position + texture coordinate. */
export interface VtxData {
  /** Vertex position [x, y] in model space (pixels). */
  vtx: [number, number]
  /** Texture coordinate [u, v], range 0..1. */
  uv: [number, number]
}

// ---------------------------------------------------------------------------
// DrawList allocations (mirror of render.DrawListAlloc, drawlist.go:51-63)
// ---------------------------------------------------------------------------

export interface DrawListAlloc {
  vtxOffset: number
  idxOffset: number
  idxCount: number
  vtxCount: number
  allocId: number
}

// ---------------------------------------------------------------------------
// Draw commands (mirror of render.DrawCmd, drawlist.go:67-90)
// ---------------------------------------------------------------------------

/**
 * A single drawing command. Sources reference texture-slot indices into the
 * DrawListSnapshot.textures array; -1 means "no texture" (flat color).
 *
 * Variables is a 16-element float32 array (64 bytes) whose layout depends on
 * typeId. For PartNode (the most common), the layout is PartVars:
 *   [0..2] tint.rgb, [3..5] screenTint.rgb, [6] padding, [7] opacity,
 *   [8] emissionStrength.
 */
export interface DrawCmd {
  /** Texture-slot indices for each attachment slot (-1 = unbound). */
  sourceIds: number[]
  state: DrawState
  blendMode: BlendMode
  maskMode: MaskingMode
  allocId: number
  vtxOffset: number
  idxOffset: number
  elemCount: number
  typeId: number
  /** 16 float32 values = 64 bytes, node-specific uniform data. */
  variables: number[]
}

// ---------------------------------------------------------------------------
// Textures
// ---------------------------------------------------------------------------

/**
 * A texture in the snapshot. For the spike, textures are provided as data URLs
 * (PNG/JPEG). A real integration would supply raw RGBA pixel data or GPU
 * texture handles from the backend.
 */
export interface DrawListTexture {
  id: number
  width: number
  height: number
  /** Data URL (image/png, image/jpeg) or raw RGBA base64. */
  src: string
}

// ---------------------------------------------------------------------------
// Snapshot — the full payload consumed by the viewer
// ---------------------------------------------------------------------------

export interface DrawListSnapshot {
  vertices: VtxData[]
  indices: number[]
  commands: DrawCmd[]
  allocations: DrawListAlloc[]
  textures: DrawListTexture[]
  /** Model-space bounds [minX, minY, maxX, maxY] for projection. */
  bounds: [number, number, number, number]
}
