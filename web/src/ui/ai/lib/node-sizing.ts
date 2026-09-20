/**
 * Scale a node's circle size based on how many children it parents. More
 * children → larger circle, so hubs are visually larger. Growth is sublinear
 * (sqrt) and capped so a single mega-parent can't swallow the whole canvas.
 *
 * Mass is intentionally NOT scaled by childCount. In the forceAtlas2Based
 * solver the repulsion/central-gravity formulas already multiply by node
 * degree (edge count), so a hub's structural pull is already captured there.
 * Adding a childCount-based mass bonus on top double-counts it: hub mass would
 * balloon and, since force ∝ mass, the hub would dominate and drag the whole
 * layout. Keep mass flat at its base value per node type.
 */
export function sizeAndMassForChildCount(
  childCount: number,
  base: { size: number; mass: number },
): { size: number; mass: number } {
  if (childCount <= 0) return { size: base.size, mass: base.mass }
  const growth = Math.sqrt(childCount)
  return {
    size: base.size + Math.min(growth * 3.5, 18),
    mass: base.mass,
  }
}
