/**
 * Converter from schema-generated PuppetDocumentSnapshot to DrawListSnapshot.
 *
 * Walks the PuppetNode tree produced by the puppet schema (see
 * schemas/puppet._2962.spore) and builds the flat DrawList wire format that
 * the inochi2d WebGL viewer consumes (see web/src/ui/inochi2d/types.ts).
 *
 * Each enabled "part" node becomes either an explicit triangulated mesh (when
 * the node carries a Phase 2 PuppetMesh) or the implicit textured quad (two
 * triangles). Texture asset IDs are resolved to image data URLs via
 * collectNodeTextures() (backed by the generated puppet.asset.read client) or
 * a caller-supplied resolver; unresolved textures fall back to flat-color
 * rendering (sourceId = -1).
 *
 * Also provides summarizeMesh() for the Inspector's mesh summary and
 * extractWireframeSegments() for the canvas mesh-wireframe overlay.
 *
 * This module has no React or WebGL dependency — fully unit-testable.
 */

import type {
  PuppetDocumentSnapshot,
  PuppetNode,
  PuppetMesh,
  PuppetAssetReadResp,
} from '../../gen-types/puppet'
import type { GosporeClient } from '@qomos/gospore-client'
import * as puppetAsset from '../../gen-clients/puppet.asset/client'
import type {
  DrawListSnapshot,
  DrawListTexture,
  DrawCmd,
  DrawListAlloc,
  VtxData,
} from '../inochi2d/types'
import { DrawState, BlendMode, MaskingMode } from '../inochi2d/types'

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export interface ResolvedTexture {
  src: string
  width: number
  height: number
}

/** Maps a texture asset ID (string) to image data. Return undefined to skip. */
export type TextureResolver = (assetId: string) => ResolvedTexture | undefined

/**
 * Parsed, validated mesh geometry ready for DrawList emission.
 */
export interface MeshGeometry {
  /** Positions in model space (node-local vertices translated by x/y params). */
  positions: Array<[number, number]>
  /** Texture coordinates matching the vertex order (0..1). */
  uvs: Array<[number, number]>
  /** Triangle indices into `positions` (0-based, already remapped by caller). */
  indices: number[]
}

/** Summary of a node's explicit mesh, for Inspector display. */
export interface MeshSummary {
  vertexCount: number
  triangleCount: number
}

/** One wireframe edge in overlay space (Y already flipped if needed). */
export interface WireframeSegment {
  x1: number
  y1: number
  x2: number
  y2: number
}

export interface ConvertOptions {
  /** Resolve texture asset IDs to image data URLs. */
  textureResolver?: TextureResolver
  /** Default quad width/height (px) when Params lacks width/height. */
  defaultQuadSize?: number
  /** Bounds padding ratio (0.1 = 10 % margin around geometry). */
  boundsPadding?: number
}

// ---------------------------------------------------------------------------
// Internal types
// ---------------------------------------------------------------------------

interface RenderableNode {
  guid: string
  name: string
  params: Record<string, string> | undefined
  z: number | undefined
  textureAssetId: string | undefined
  resolvedTexture: ResolvedTexture | undefined
  mesh: PuppetMesh | undefined
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Default PartVars: white tint, full opacity, no emission.
 * Mirrors defaultPartVars() in sampleData.ts — 16 float32 values.
 */
function defaultPartVars(opacity = 1): number[] {
  return [
    1, 1, 1,       // tint (white)
    0, 0, 0,       // screenTint (none)
    0,             // padding
    opacity,       // opacity
    0,             // emission
    0, 0, 0, 0, 0, 0, 0, // unused
  ]
}

/** Safely parse a numeric param, returning the fallback on failure. */
function paramFloat(params: Record<string, string> | undefined, key: string, fallback: number): number {
  const raw = params?.[key]
  if (raw === undefined) return fallback
  const v = parseFloat(raw)
  return Number.isNaN(v) ? fallback : v
}

/**
 * Recursively collect enabled "part" nodes from the PuppetNode tree.
 * Only part-kind nodes carry renderable geometry; composite/mask/group/bone
 * are structural containers (rendered by the full Inochi2D pipeline).
 */
function collectRenderableNodes(
  node: PuppetNode,
  resolver: TextureResolver | undefined,
  out: RenderableNode[],
): void {
  if (!node.Enabled) return

  if (node.Kind === 'part') {
    const resolved = node.TextureAssetId && resolver
      ? resolver(node.TextureAssetId)
      : undefined
    out.push({
      guid: node.Guid,
      name: node.Name,
      params: node.Params,
      z: node.Z,
      textureAssetId: node.TextureAssetId,
      resolvedTexture: resolved,
      mesh: node.Mesh,
    })
  }

  for (const child of node.Children) {
    collectRenderableNodes(child, resolver, out)
  }
}

/** Compute tight bounds [minX, minY, maxX, maxY] from vertices. */
function computeBounds(
  vertices: VtxData[],
  padding: number,
): [number, number, number, number] {
  if (vertices.length === 0) {
    return [-128, -128, 128, 128]
  }
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  for (const v of vertices) {
    if (v.vtx[0] < minX) minX = v.vtx[0]
    if (v.vtx[1] < minY) minY = v.vtx[1]
    if (v.vtx[0] > maxX) maxX = v.vtx[0]
    if (v.vtx[1] > maxY) maxY = v.vtx[1]
  }
  const dx = (maxX - minX) * padding
  const dy = (maxY - minY) * padding
  return [minX - dx, minY - dy, maxX + dx, maxY + dy]
}

/**
 * Collect the unique texture asset IDs referenced by enabled "part" nodes,
 * in tree order.
 */
function collectTextureAssetIds(node: PuppetNode, out: string[] = []): string[] {
  if (node.Enabled && node.Kind === 'part' && node.TextureAssetId) {
    if (!out.includes(node.TextureAssetId)) out.push(node.TextureAssetId)
  }
  for (const child of node.Children) collectTextureAssetIds(child, out)
  return out
}

/**
 * Validate and normalize a node's explicit Mesh into DrawList-ready geometry.
 *
 * Vertices are node-local and get translated by the node's (x, y) params.
 * UVs, when present, must match the vertex count; when absent or mismatched
 * they are derived from the mesh bounds (v flipped to match the implicit-quad
 * convention: bottom edge v=1, top edge v=0).
 *
 * Returns undefined for absent or invalid meshes (odd vertex data, indices
 * that are not a triangle list, or out-of-range indices) — callers fall back
 * to the implicit quad so rendering never breaks on bad data.
 */
export function parseMeshGeometry(
  mesh: PuppetMesh | undefined,
  offsetX: number,
  offsetY: number,
): MeshGeometry | undefined {
  if (!mesh) return undefined
  const flatVertices = mesh.Vertices
  const flatIndices = mesh.Indices
  if (!flatVertices || !flatIndices) return undefined

  const vertexCount = Math.floor(flatVertices.length / 2)
  if (vertexCount < 3) return undefined
  if (flatIndices.length < 3 || flatIndices.length % 3 !== 0) return undefined
  for (const idx of flatIndices) {
    if (!Number.isInteger(idx) || idx < 0 || idx >= vertexCount) return undefined
  }

  const positions: Array<[number, number]> = []
  for (let i = 0; i < vertexCount; i++) {
    const x = flatVertices[i * 2] ?? 0
    const y = flatVertices[i * 2 + 1] ?? 0
    if (Number.isNaN(x) || Number.isNaN(y)) return undefined
    positions.push([x + offsetX, y + offsetY])
  }

  const flatUvs = mesh.UVs
  const uvs: Array<[number, number]> = []
  if (flatUvs && flatUvs.length >= flatVertices.length) {
    for (let i = 0; i < vertexCount; i++) {
      uvs.push([flatUvs[i * 2] ?? 0, flatUvs[i * 2 + 1] ?? 0])
    }
  } else {
    // Derive UVs from the mesh bounds, mirroring the implicit-quad mapping
    // (bottom edge → v=1, top edge → v=0).
    let minX = Infinity
    let minY = Infinity
    let maxX = -Infinity
    let maxY = -Infinity
    for (const [x, y] of positions) {
      if (x < minX) minX = x
      if (y < minY) minY = y
      if (x > maxX) maxX = x
      if (y > maxY) maxY = y
    }
    const w = maxX - minX || 1
    const h = maxY - minY || 1
    for (const [x, y] of positions) {
      uvs.push([(x - minX) / w, 1 - (y - minY) / h])
    }
  }

  return { positions, uvs, indices: flatIndices.slice() }
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/**
 * Convert a PuppetDocumentSnapshot (schema-generated tree) into a
 * DrawListSnapshot (flat wire format for the WebGL viewer).
 *
 * Each enabled part node produces a textured quad. Nodes are sorted by Z
 * (lower = further back) so the draw order matches Inochi2D's z-ordering.
 */
export function convertPuppetDocumentToDrawList(
  snapshot: PuppetDocumentSnapshot,
  options: ConvertOptions = {},
): DrawListSnapshot {
  const {
    textureResolver,
    defaultQuadSize = 128,
    boundsPadding = 0.1,
  } = options

  const renderableNodes: RenderableNode[] = []
  collectRenderableNodes(snapshot.Document.Root, textureResolver, renderableNodes)

  // Sort by Z (lower Z = further back = drawn first).
  renderableNodes.sort((a, b) => (a.z ?? 0) - (b.z ?? 0))

  const vertices: VtxData[] = []
  const indices: number[] = []
  const commands: DrawCmd[] = []
  const allocations: DrawListAlloc[] = []
  const textures: DrawListTexture[] = []

  // Track resolved textures by assetId to deduplicate.
  const texIdByAsset = new Map<string, number>()

  let vtxOffset = 0
  let idxOffset = 0

  for (let i = 0; i < renderableNodes.length; i++) {
    const node = renderableNodes[i]!
    const p = node.params

    const cx = paramFloat(p, 'x', 0)
    const cy = paramFloat(p, 'y', 0)
    const opacity = paramFloat(p, 'opacity', 1)

    const baseIdx = vertices.length
    let vtxCount: number
    let elemCount: number

    // Explicit mesh path: nodes carrying a valid Mesh render as a deformable
    // triangulated mesh instead of the implicit quad.
    const meshGeom = parseMeshGeometry(node.mesh, cx, cy)
    if (meshGeom) {
      for (let v = 0; v < meshGeom.positions.length; v++) {
        vertices.push({ vtx: meshGeom.positions[v]!, uv: meshGeom.uvs[v]! })
      }
      for (const idx of meshGeom.indices) {
        indices.push(baseIdx + idx)
      }
      vtxCount = meshGeom.positions.length
      elemCount = meshGeom.indices.length
    } else {
      // Implicit quad path: two triangles centered at (cx, cy).
      const w = paramFloat(p, 'width', defaultQuadSize)
      const h = paramFloat(p, 'height', defaultQuadSize)
      const hw = w / 2
      const hh = h / 2

      // Quad vertices (model-space, Y-down convention matching DrawList).
      vertices.push(
        { vtx: [cx - hw, cy - hh], uv: [0, 1] },
        { vtx: [cx + hw, cy - hh], uv: [1, 1] },
        { vtx: [cx + hw, cy + hh], uv: [1, 0] },
        { vtx: [cx - hw, cy + hh], uv: [0, 0] },
      )

      // Two triangles forming the quad.
      indices.push(
        baseIdx, baseIdx + 1, baseIdx + 2,
        baseIdx, baseIdx + 2, baseIdx + 3,
      )
      vtxCount = 4
      elemCount = 6
    }

    // Resolve texture slot.
    let texSlot = -1
    if (node.resolvedTexture && node.textureAssetId) {
      const existing = texIdByAsset.get(node.textureAssetId)
      if (existing !== undefined) {
        texSlot = existing
      } else {
        texSlot = textures.length
        texIdByAsset.set(node.textureAssetId, texSlot)
        textures.push({
          id: texSlot,
          width: node.resolvedTexture.width,
          height: node.resolvedTexture.height,
          src: node.resolvedTexture.src,
        })
      }
    }

    commands.push({
      sourceIds: [texSlot],
      state: DrawState.Normal,
      blendMode: BlendMode.Normal,
      maskMode: MaskingMode.Mask,
      allocId: i,
      vtxOffset,
      idxOffset,
      elemCount,
      typeId: 1,
      variables: defaultPartVars(opacity),
    })

    allocations.push({
      vtxOffset,
      idxOffset,
      idxCount: elemCount,
      vtxCount,
      allocId: i,
    })

    vtxOffset += vtxCount
    idxOffset += elemCount
  }

  return {
    vertices,
    indices,
    commands,
    allocations,
    textures,
    bounds: computeBounds(vertices, boundsPadding),
  }
}

/**
 * Count the total number of renderable part nodes in a document snapshot.
 * Useful for UI status display without performing a full conversion.
 */
export function countRenderableNodes(snapshot: PuppetDocumentSnapshot): number {
  let count = 0
  function walk(node: PuppetNode): void {
    if (node.Enabled && node.Kind === 'part') count++
    for (const child of node.Children) walk(child)
  }
  walk(snapshot.Document.Root)
  return count
}

// ---------------------------------------------------------------------------
// Texture loading via the generated puppet.asset.read client
// ---------------------------------------------------------------------------

/**
 * Load every texture referenced by enabled "part" nodes through the generated
 * `puppet.asset.read` client, producing a resolver map for
 * `convertPuppetDocumentToDrawList`.
 *
 * Each unique TextureAssetId is read exactly once. Individual read failures
 * (unknown asset, non-committed asset, backend error) are skipped so the
 * affected nodes simply fall back to flat-color rendering instead of failing
 * the whole conversion.
 */
export async function collectNodeTextures(
  snapshot: PuppetDocumentSnapshot,
  client: GosporeClient,
): Promise<Map<string, ResolvedTexture>> {
  const assetIds = collectTextureAssetIds(snapshot.Document.Root)
  const resolved = new Map<string, ResolvedTexture>()
  for (const assetId of assetIds) {
    try {
      const resp: PuppetAssetReadResp = await puppetAsset.read(client, { AssetId: assetId })
      if (!resp?.Uri) continue
      resolved.set(assetId, {
        src: resp.Uri,
        width: resp.Width ?? 0,
        height: resp.Height ?? 0,
      })
    } catch {
      // Unreadable asset → resolver misses the entry → flat-color fallback.
    }
  }
  return resolved
}

// ---------------------------------------------------------------------------
// Mesh summary & wireframe extraction
// ---------------------------------------------------------------------------

/**
 * Summarize a node's explicit mesh (vertex and triangle counts) for Inspector
 * display. Returns null when the node has no usable mesh arrays.
 */
export function summarizeMesh(mesh: PuppetMesh | undefined): MeshSummary | null {
  if (!mesh?.Vertices || !mesh.Indices) return null
  const vertexCount = Math.floor(mesh.Vertices.length / 2)
  const triangleCount = Math.floor(mesh.Indices.length / 3)
  if (vertexCount === 0 && triangleCount === 0) return null
  return { vertexCount, triangleCount }
}

/**
 * Extract the unique wireframe edges of a DrawListSnapshot's geometry as
 * segments in overlay space, for the canvas mesh-wireframe toggle.
 *
 * `flipY` must match the viewer's flip setting: when true (model-space Y-down
 * mapped to screen top), coordinates pass through unchanged; when false, Y is
 * mirrored around the snapshot bounds so the overlay matches the WebGL render.
 */
export function extractWireframeSegments(
  snapshot: DrawListSnapshot,
  flipY = true,
): WireframeSegment[] {
  const [, minY, , maxY] = snapshot.bounds
  const segments: WireframeSegment[] = []
  const seen = new Set<string>()

  for (const cmd of snapshot.commands) {
    for (let t = 0; t + 2 < cmd.elemCount; t += 3) {
      const a = snapshot.indices[cmd.idxOffset + t]
      const b = snapshot.indices[cmd.idxOffset + t + 1]
      const c = snapshot.indices[cmd.idxOffset + t + 2]
      if (a === undefined || b === undefined || c === undefined) continue
      const edges: Array<[number, number]> = [[a, b], [b, c], [c, a]]
      for (const [i, j] of edges) {
        const key = i < j ? `${i}:${j}` : `${j}:${i}`
        if (seen.has(key)) continue
        seen.add(key)
        const p1 = snapshot.vertices[i]
        const p2 = snapshot.vertices[j]
        if (!p1 || !p2) continue
        segments.push({
          x1: p1.vtx[0],
          y1: flipY ? p1.vtx[1] : minY + maxY - p1.vtx[1],
          x2: p2.vtx[0],
          y2: flipY ? p2.vtx[1] : minY + maxY - p2.vtx[1],
        })
      }
    }
  }
  return segments
}
