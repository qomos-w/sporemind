import { describe, expect, it } from 'vitest'
import { buildLayerTransform, worldReticleSize } from './topologyOverlayCamera'

describe('topology overlay camera transform', () => {
  it('builds the layer transform from the canvas origin and scale', () => {
    expect(buildLayerTransform({ x: 0, y: 0 }, 1)).toBe('translate(0px, 0px) scale(1)')
    expect(buildLayerTransform({ x: 120.5, y: -40 }, 1.5)).toBe('translate(120.5px, -40px) scale(1.5)')
  })

  it('keeps the on-screen reticle gap constant by converting it to world units', () => {
    const bbox = { left: 10, top: 10, right: 30, bottom: 30 }
    // At scale 2 the 12px screen gap becomes 6 world units on each side.
    expect(worldReticleSize(bbox, 12, 2)).toEqual({ width: 20 + 6 * 2, height: 20 + 6 * 2 })
    // At scale 0.5 the same gap becomes 24 world units on each side.
    expect(worldReticleSize(bbox, 12, 0.5)).toEqual({ width: 20 + 24 * 2, height: 20 + 24 * 2 })
  })
})