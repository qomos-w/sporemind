const MIN_DIM = 2
const MAX_COLS = 5

/**
 * Compute a near-square grid layout for `count` cells that best fits a
 * container of `width` × `height` pixels, accounting for the fixed vertical
 * chrome (cell header + composer) each cell consumes.
 *
 * The target is a square *content area* per cell, not a square cell: every row
 * loses `cellChromeHeight` pixels to the header + composer, so vertical space
 * is effectively reduced and the layout favours columns on wide containers.
 *
 * cols is the positive root of  H·cols² − chrome·n·cols − W·n = 0, derived from
 *   W/cols = H/rows − chrome   with   rows ≈ n/cols.
 * chrome = 0 collapses to cols = sqrt(n · W/H), the plain aspect heuristic.
 */
export function computeGridDimensions(
  count: number,
  width: number,
  height: number,
  cellChromeHeight: number,
): { cols: number; rows: number } {
  const n = Math.max(0, Math.floor(count))
  const W = width > 0 && Number.isFinite(width) ? width : 0
  const H = height > 0 && Number.isFinite(height) ? height : 0
  const chrome = cellChromeHeight > 0 && Number.isFinite(cellChromeHeight) ? cellChromeHeight : 0

  let colsRaw: number
  if (W > 0 && H > 0 && n > 0) {
    colsRaw = (chrome * n + Math.sqrt((chrome * n) ** 2 + 4 * H * W * n)) / (2 * H)
  } else {
    colsRaw = Math.sqrt(n)
  }
  const cols = Math.max(MIN_DIM, Math.min(MAX_COLS, Math.ceil(colsRaw)))
  const rows = Math.max(MIN_DIM, Math.ceil(n / cols))
  return { cols, rows }
}
