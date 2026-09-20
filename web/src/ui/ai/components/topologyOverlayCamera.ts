/** Pure helpers for positioning TopologyGraph overlays using a single-layer
 *  camera transform. These functions have no side effects and are kept in a
 *  dedicated module so they can be unit-tested without pulling in the full
 *  component / vis-network dependency graph. */

/** Build the single-layer camera transform applied to overlay containers.
 *  Overlays are positioned in world (canvas) coordinates; this transform maps
 *  them to screen space during pan/zoom. */
export function buildLayerTransform(origin: { x: number; y: number }, scale: number): string {
  return `translate(${origin.x}px, ${origin.y}px) scale(${scale})`
}

/** Compute world-space reticle dimensions from a node's bounding box and a fixed
 *  screen-space padding. The padding is converted to world units so that after
 *  the layer transform the on-screen gap stays constant. */
export function worldReticleSize(
  bbox: { left: number; top: number; right: number; bottom: number },
  screenGap: number,
  scale: number,
): { width: number; height: number } {
  const gap = screenGap / scale
  return {
    width: bbox.right - bbox.left + gap * 2,
    height: bbox.bottom - bbox.top + gap * 2,
  }
}