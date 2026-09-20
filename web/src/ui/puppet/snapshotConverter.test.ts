import { describe, it, expect, vi } from 'vitest'
import type { GosporeClient } from '@qomos/gospore-client'
import {
  convertPuppetDocumentToDrawList,
  collectNodeTextures,
  countRenderableNodes,
  summarizeMesh,
  extractWireframeSegments,
  parseMeshGeometry,
  type TextureResolver,
} from './snapshotConverter'
import type { PuppetDocumentSnapshot, PuppetMesh } from '../../gen-types/puppet'
import { DrawState, BlendMode, MaskingMode } from '../inochi2d/types'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeNode(overrides: {
  guid: string
  name: string
  kind?: string
  enabled?: boolean
  z?: number
  textureAssetId?: string
  mesh?: PuppetMesh
  params?: Record<string, string>
  children?: PuppetNodeLike[]
}): PuppetNodeLike {
  return {
    Guid: overrides.guid,
    Name: overrides.name,
    Kind: overrides.kind ?? 'part',
    Enabled: overrides.enabled ?? true,
    Z: overrides.z,
    TextureAssetId: overrides.textureAssetId,
    Mesh: overrides.mesh,
    Params: overrides.params,
    Children: overrides.children ?? [],
  }
}

interface PuppetNodeLike {
  Guid: string
  Name: string
  Kind: string
  Enabled: boolean
  Z?: number | undefined
  TextureAssetId?: string | undefined
  Mesh?: PuppetMesh | undefined
  Params?: Record<string, string> | undefined
  Children: PuppetNodeLike[]
}

function makeSnapshot(root: PuppetNodeLike): PuppetDocumentSnapshot {
  return {
    Document: {
      Id: 'doc-1',
      Revision: 1,
      Name: 'Test Puppet',
      Root: root,
      CreatedAt: '2026-01-01T00:00:00Z',
      UpdatedAt: '2026-01-01T00:00:00Z',
    },
    StagedAssetCount: 0,
    RevisionCount: 1,
  }
}

const MOCK_TEXTURE_SRC = 'data:image/png;base64,iVBORw0KGgo='
const MOCK_RESOLVER: TextureResolver = () => ({
  src: MOCK_TEXTURE_SRC,
  width: 64,
  height: 64,
})

// ---------------------------------------------------------------------------
// convertPuppetDocumentToDrawList
// ---------------------------------------------------------------------------

describe('convertPuppetDocumentToDrawList', () => {
  it('converts a single part node to a textured quad snapshot', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'Body',
      textureAssetId: 'tex-body',
      params: { x: '10', y: '20', width: '100', height: '50', opacity: '0.8' },
    }))

    const result = convertPuppetDocumentToDrawList(snapshot, { textureResolver: MOCK_RESOLVER })

    expect(result.vertices).toHaveLength(4)
    expect(result.indices).toEqual([0, 1, 2, 0, 2, 3])
    expect(result.commands).toHaveLength(1)
    expect(result.allocations).toHaveLength(1)
    expect(result.textures).toHaveLength(1)

    // Texture resolved with correct metadata.
    expect(result.textures[0]!.src).toBe(MOCK_TEXTURE_SRC)
    expect(result.textures[0]!.width).toBe(64)
    expect(result.textures[0]!.height).toBe(64)

    // Command references texture slot 0.
    expect(result.commands[0]!.sourceIds).toEqual([0])
    expect(result.commands[0]!.state).toBe(DrawState.Normal)
    expect(result.commands[0]!.blendMode).toBe(BlendMode.Normal)
    expect(result.commands[0]!.maskMode).toBe(MaskingMode.Mask)
    expect(result.commands[0]!.elemCount).toBe(6)

    // Opacity from params is applied in PartVars.
    expect(result.commands[0]!.variables[7]).toBe(0.8)
  })

  it('reads position and size from params', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'Part',
      params: { x: '-50', y: '30', width: '200', height: '100' },
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    // cx=-50, cy=30, w=200, h=100 → half extents hw=100, hh=50
    expect(result.vertices[0]!.vtx).toEqual([-150, -20]) // bottom-left
    expect(result.vertices[1]!.vtx).toEqual([50, -20])   // bottom-right
    expect(result.vertices[2]!.vtx).toEqual([50, 80])    // top-right
    expect(result.vertices[3]!.vtx).toEqual([-150, 80])  // top-left
  })

  it('uses default quad size when params lack width/height', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'Part',
    }))

    const result = convertPuppetDocumentToDrawList(snapshot, { defaultQuadSize: 64 })

    // cx=0, cy=0, w=64, h=64 → half extents 32
    expect(result.vertices[0]!.vtx).toEqual([-32, -32])
    expect(result.vertices[1]!.vtx).toEqual([32, -32])
    expect(result.vertices[2]!.vtx).toEqual([32, 32])
    expect(result.vertices[3]!.vtx).toEqual([-32, 32])
  })

  it('sorts nodes by Z (lower Z drawn first)', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'a', name: 'A', z: 10 }),
        makeNode({ guid: 'b', name: 'B', z: 1 }),
        makeNode({ guid: 'c', name: 'C', z: 5 }),
      ],
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    // Sorted by Z: B(1), C(5), A(10)
    expect(result.commands[0]!.allocId).toBe(0)
    expect(result.commands[1]!.allocId).toBe(1)
    expect(result.commands[2]!.allocId).toBe(2)

    // Vertex offsets are sequential.
    expect(result.allocations[0]!.vtxOffset).toBe(0)
    expect(result.allocations[1]!.vtxOffset).toBe(4)
    expect(result.allocations[2]!.vtxOffset).toBe(8)
  })

  it('skips disabled nodes', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'a', name: 'A', enabled: true }),
        makeNode({ guid: 'b', name: 'B', enabled: false }),
      ],
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    expect(result.commands).toHaveLength(1)
    expect(result.vertices).toHaveLength(4)
  })

  it('only renders part-kind nodes, not group/composite/mask', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'part1', name: 'Part1', kind: 'part' }),
        makeNode({ guid: 'comp1', name: 'Comp1', kind: 'composite' }),
        makeNode({ guid: 'mask1', name: 'Mask1', kind: 'mask' }),
        makeNode({ guid: 'part2', name: 'Part2', kind: 'part' }),
      ],
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    expect(result.commands).toHaveLength(2) // only part1 and part2
  })

  it('produces flat-color (sourceId -1) for parts without resolved texture', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'Part',
      textureAssetId: 'tex-missing',
    }))

    // No resolver → texture unresolved.
    const result = convertPuppetDocumentToDrawList(snapshot)

    expect(result.textures).toHaveLength(0)
    expect(result.commands[0]!.sourceIds).toEqual([-1])
  })

  it('deduplicates textures referenced by multiple nodes', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'a', name: 'A', textureAssetId: 'shared-tex' }),
        makeNode({ guid: 'b', name: 'B', textureAssetId: 'shared-tex' }),
      ],
    }))

    const result = convertPuppetDocumentToDrawList(snapshot, { textureResolver: MOCK_RESOLVER })

    expect(result.textures).toHaveLength(1)
    expect(result.commands[0]!.sourceIds).toEqual([0])
    expect(result.commands[1]!.sourceIds).toEqual([0])
  })

  it('computes bounds with padding around geometry', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'Part',
      params: { x: '0', y: '0', width: '100', height: '100' },
    }))

    const result = convertPuppetDocumentToDrawList(snapshot, { boundsPadding: 0.1 })

    // Vertices span [-50,-50] to [50,50]. With 10% padding: dx=dy=10.
    const [minX, minY, maxX, maxY] = result.bounds
    expect(minX).toBeCloseTo(-60, 5)
    expect(minY).toBeCloseTo(-60, 5)
    expect(maxX).toBeCloseTo(60, 5)
    expect(maxY).toBeCloseTo(60, 5)
  })

  it('returns default bounds for an empty tree', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    expect(result.vertices).toHaveLength(0)
    expect(result.commands).toHaveLength(0)
    expect(result.bounds).toEqual([-128, -128, 128, 128])
  })

  it('handles NaN param values gracefully', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'Part',
      params: { x: 'not-a-number', opacity: 'bad' },
    }))

    const result = convertPuppetDocumentToDrawList(snapshot, { defaultQuadSize: 100 })

    // x falls back to 0, opacity falls back to 1.
    expect(result.commands[0]!.variables[7]).toBe(1)
    expect(result.vertices[0]!.vtx).toEqual([-50, -50])
  })

  it('walks nested children recursively', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({
          guid: 'child-group',
          name: 'SubGroup',
          kind: 'group',
          children: [
            makeNode({ guid: 'deep-part', name: 'Deep', kind: 'part' }),
          ],
        }),
      ],
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    expect(result.commands).toHaveLength(1)
  })
})

// ---------------------------------------------------------------------------
// countRenderableNodes
// ---------------------------------------------------------------------------

describe('countRenderableNodes', () => {
  it('counts enabled part nodes', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'a', name: 'A', kind: 'part' }),
        makeNode({ guid: 'b', name: 'B', kind: 'composite' }),
        makeNode({ guid: 'c', name: 'C', kind: 'part' }),
      ],
    }))

    expect(countRenderableNodes(snapshot)).toBe(2)
  })

  it('excludes disabled parts', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'a', name: 'A', kind: 'part', enabled: true }),
        makeNode({ guid: 'b', name: 'B', kind: 'part', enabled: false }),
      ],
    }))

    expect(countRenderableNodes(snapshot)).toBe(1)
  })

  it('returns 0 for an empty group root', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
    }))

    expect(countRenderableNodes(snapshot)).toBe(0)
  })
})

// ---------------------------------------------------------------------------
// Mesh path (explicit PuppetMesh replaces the implicit quad)
// ---------------------------------------------------------------------------

const QUAD_MESH: PuppetMesh = {
  Vertices: [-50, -50, 50, -50, 50, 50, -50, 50],
  Indices: [0, 1, 2, 0, 2, 3],
  UVs: [0, 1, 1, 1, 1, 0, 0, 0],
}

describe('convertPuppetDocumentToDrawList mesh path', () => {
  it('renders an explicit mesh instead of the implicit quad', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'MeshPart',
      mesh: { Vertices: [0, 0, 40, 0, 0, 30], Indices: [0, 1, 2] },
      params: { x: '100', y: '50' },
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    // 3 mesh vertices (not the 4-vertex implicit quad), 1 triangle.
    expect(result.vertices).toHaveLength(3)
    expect(result.indices).toEqual([0, 1, 2])
    expect(result.commands[0]!.elemCount).toBe(3)
    expect(result.allocations[0]!.vtxCount).toBe(3)
    expect(result.allocations[0]!.idxCount).toBe(3)

    // Vertices are translated by the node's x/y params.
    expect(result.vertices[0]!.vtx).toEqual([100, 50])
    expect(result.vertices[1]!.vtx).toEqual([140, 50])
    expect(result.vertices[2]!.vtx).toEqual([100, 80])
  })

  it('preserves explicit UVs on mesh vertices', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'MeshPart',
      mesh: QUAD_MESH,
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    expect(result.vertices[0]!.uv).toEqual([0, 1])
    expect(result.vertices[1]!.uv).toEqual([1, 1])
    expect(result.vertices[2]!.uv).toEqual([1, 0])
    expect(result.vertices[3]!.uv).toEqual([0, 0])
  })

  it('derives UVs from mesh bounds when UVs are absent', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'MeshPart',
      mesh: { Vertices: [-50, -50, 50, -50, 50, 50, -50, 50], Indices: [0, 1, 2, 0, 2, 3] },
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    // Bottom edge v=1, top edge v=0 — matches the implicit-quad convention.
    expect(result.vertices[0]!.uv).toEqual([0, 1])
    expect(result.vertices[3]!.uv).toEqual([0, 0])
    expect(result.vertices[2]!.uv).toEqual([1, 0])
  })

  it('remaps mesh indices across multiple mesh nodes', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'a', name: 'A', mesh: { Vertices: [0, 0, 10, 0, 0, 10], Indices: [0, 1, 2] }, z: 1 }),
        makeNode({ guid: 'b', name: 'B', mesh: { Vertices: [0, 0, 20, 0, 0, 20], Indices: [0, 2, 1] }, z: 2 }),
      ],
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    // 6 vertices total; second node's indices offset by its vertex base (3).
    expect(result.vertices).toHaveLength(6)
    expect(result.indices).toEqual([0, 1, 2, 3, 5, 4])
    expect(result.allocations[1]!.vtxOffset).toBe(3)
  })

  it('falls back to the implicit quad for invalid meshes', () => {
    const outOfRange: PuppetMesh = { Vertices: [0, 0, 10, 0, 0, 10], Indices: [0, 1, 5] }
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'BadMeshPart',
      mesh: outOfRange,
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    expect(result.vertices).toHaveLength(4)
    expect(result.commands[0]!.elemCount).toBe(6)
  })

  it('keeps mesh and quad nodes side by side with correct offsets', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'quad', name: 'Quad', params: { width: '10', height: '10' }, z: 1 }),
        makeNode({ guid: 'mesh', name: 'Mesh', mesh: QUAD_MESH, z: 2 }),
      ],
    }))

    const result = convertPuppetDocumentToDrawList(snapshot)

    expect(result.vertices).toHaveLength(8) // 4 quad + 4 mesh
    expect(result.allocations[1]!.vtxOffset).toBe(4)
    expect(result.allocations[1]!.idxOffset).toBe(6)
  })
})

describe('parseMeshGeometry', () => {
  it('returns undefined for missing arrays', () => {
    expect(parseMeshGeometry(undefined, 0, 0)).toBeUndefined()
    expect(parseMeshGeometry({ Vertices: undefined, Indices: undefined }, 0, 0)).toBeUndefined()
  })

  it('rejects non-triangle-list indices', () => {
    expect(parseMeshGeometry({ Vertices: [0, 0, 10, 0, 0, 10], Indices: [0, 1] }, 0, 0)).toBeUndefined()
    expect(parseMeshGeometry({ Vertices: [0, 0, 10, 0, 0, 10], Indices: [0, 1, 2, 0] }, 0, 0)).toBeUndefined()
  })

  it('rejects fewer than 3 vertices', () => {
    expect(parseMeshGeometry({ Vertices: [0, 0, 10, 0], Indices: [0, 0, 0] }, 0, 0)).toBeUndefined()
  })
})

// ---------------------------------------------------------------------------
// collectNodeTextures (generated puppet.asset.read client)
// ---------------------------------------------------------------------------

function makeMockClient(
  handler: (callID: string, req: { AssetId: string }) => unknown,
): { client: GosporeClient; invoke: ReturnType<typeof vi.fn> } {
  const invoke = vi.fn(async (callID: string, req: { AssetId: string }) => handler(callID, req))
  return { client: { invoke } as unknown as GosporeClient, invoke }
}

describe('collectNodeTextures', () => {
  it('loads each unique asset once via puppet.asset.read', async () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'a', name: 'A', textureAssetId: 'tex-a' }),
        makeNode({ guid: 'b', name: 'B', textureAssetId: 'tex-a' }),
        makeNode({ guid: 'c', name: 'C', textureAssetId: 'tex-b' }),
      ],
    }))

    const { client, invoke } = makeMockClient((_callID, req) => ({
      AssetId: req.AssetId,
      Uri: `data:image/png;base64,${req.AssetId}`,
      MimeType: 'image/png',
      Width: 64,
      Height: 32,
    }))

    const map = await collectNodeTextures(snapshot, client)

    // Deduplicated: two unique assets, not three reads.
    expect(invoke).toHaveBeenCalledTimes(2)
    expect(invoke).toHaveBeenNthCalledWith(1, 'puppet.asset.read', { AssetId: 'tex-a' }, expect.anything())
    expect(invoke).toHaveBeenNthCalledWith(2, 'puppet.asset.read', { AssetId: 'tex-b' }, expect.anything())

    expect(map.get('tex-a')).toEqual({ src: 'data:image/png;base64,tex-a', width: 64, height: 32 })
    expect(map.get('tex-b')).toEqual({ src: 'data:image/png;base64,tex-b', width: 64, height: 32 })
  })

  it('ignores disabled and non-part nodes', async () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'off', name: 'Off', enabled: false, textureAssetId: 'tex-off' }),
        makeNode({ guid: 'group2', name: 'Group', kind: 'group', textureAssetId: 'tex-group' }),
        makeNode({ guid: 'on', name: 'On', textureAssetId: 'tex-on' }),
      ],
    }))

    const { client, invoke } = makeMockClient((_c, req) => ({ AssetId: req.AssetId, Uri: 'x' }))
    const map = await collectNodeTextures(snapshot, client)

    expect(invoke).toHaveBeenCalledTimes(1)
    expect(invoke).toHaveBeenCalledWith('puppet.asset.read', { AssetId: 'tex-on' }, expect.anything())
    expect(map.has('tex-off')).toBe(false)
  })

  it('skips failed reads so those nodes fall back to flat color', async () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'root',
      name: 'Root',
      kind: 'group',
      children: [
        makeNode({ guid: 'a', name: 'A', textureAssetId: 'tex-bad' }),
        makeNode({ guid: 'b', name: 'B', textureAssetId: 'tex-good' }),
      ],
    }))

    const { client } = makeMockClient((_c, req) => {
      if (req.AssetId === 'tex-bad') throw new Error('asset not committed')
      return { AssetId: req.AssetId, Uri: 'data:image/png;base64,good', Width: 8, Height: 8 }
    })

    const map = await collectNodeTextures(snapshot, client)

    expect(map.has('tex-bad')).toBe(false)
    expect(map.get('tex-good')?.src).toBe('data:image/png;base64,good')
  })

  it('skips responses without a Uri', async () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'a',
      name: 'A',
      textureAssetId: 'tex-x',
    }))
    const { client } = makeMockClient(() => ({ AssetId: 'tex-x', Uri: '' }))

    const map = await collectNodeTextures(snapshot, client)
    expect(map.size).toBe(0)
  })
})

// ---------------------------------------------------------------------------
// summarizeMesh
// ---------------------------------------------------------------------------

describe('summarizeMesh', () => {
  it('counts vertices and triangles', () => {
    expect(summarizeMesh(QUAD_MESH)).toEqual({ vertexCount: 4, triangleCount: 2 })
  })

  it('returns null for absent or empty meshes', () => {
    expect(summarizeMesh(undefined)).toBeNull()
    expect(summarizeMesh({})).toBeNull()
    expect(summarizeMesh({ Vertices: [], Indices: [] })).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// extractWireframeSegments
// ---------------------------------------------------------------------------

describe('extractWireframeSegments', () => {
  it('returns the unique edges of the rendered triangles', () => {
    // A single implicit quad centered at origin: 2 triangles → 5 unique edges
    // (4 outline + 1 diagonal 0→2).
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'Quad',
      params: { width: '100', height: '100' },
    }))
    const drawList = convertPuppetDocumentToDrawList(snapshot)

    const segments = extractWireframeSegments(drawList, true)

    expect(segments).toHaveLength(5)
    const edges = segments
      .map(s => [[s.x1, s.y1], [s.x2, s.y2]] as Array<[number, number]>)
      .map(([a, b]) => {
        const p = a!
        const q = b!
        const key = `${p[0]},${p[1]};${q[0]},${q[1]}`
        const reverse = `${q[0]},${q[1]};${p[0]},${p[1]}`
        return key < reverse ? key : reverse
      })
      .sort()
    expect(edges).toEqual([
      '-50,-50;-50,50',  // left edge
      '-50,-50;50,-50',  // bottom edge
      '-50,-50;50,50',   // diagonal
      '-50,50;50,50',    // top edge
      '50,-50;50,50',    // right edge
    ])
  })

  it('mirrors Y around the bounds when flipY is false', () => {
    const snapshot = makeSnapshot(makeNode({
      guid: 'n1',
      name: 'Quad',
      params: { width: '100', height: '100' },
    }))
    const drawList = convertPuppetDocumentToDrawList(snapshot)
    const [minY, maxY] = [drawList.bounds[1], drawList.bounds[3]]

    const flipped = extractWireframeSegments(drawList, false)
    const straight = extractWireframeSegments(drawList, true)

    for (let i = 0; i < flipped.length; i++) {
      const f = flipped[i]!
      const s = straight[i]!
      expect(f.x1).toBe(s.x1)
      expect(f.x2).toBe(s.x2)
      expect(f.y1).toBeCloseTo(minY + maxY - s.y1, 6)
      expect(f.y2).toBeCloseTo(minY + maxY - s.y2, 6)
    }
  })

  it('returns no segments for an empty document', () => {
    const snapshot = makeSnapshot(makeNode({ guid: 'root', name: 'Root', kind: 'group' }))
    const drawList = convertPuppetDocumentToDrawList(snapshot)
    expect(extractWireframeSegments(drawList, true)).toEqual([])
  })
})
