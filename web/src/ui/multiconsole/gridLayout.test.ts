import { describe, it, expect } from 'vitest'
import { computeGridDimensions } from './gridLayout'

// Reproduce the legacy chrome-free heuristic for an equivalence check.
const legacy = (n: number, aspect: number) => {
  const cols = Math.max(2, Math.min(5, Math.ceil(Math.sqrt(n * aspect))))
  return { cols, rows: Math.max(2, Math.ceil(n / cols)) }
}

describe('computeGridDimensions', () => {
  it('matches the legacy sqrt+ceil heuristic exactly when chrome is 0', () => {
    for (const [W, H] of [[1600, 900], [900, 1600], [1000, 1000], [2400, 600]] as const) {
      const aspect = W / H
      for (const n of [0, 1, 2, 3, 4, 6, 8, 9, 12, 20]) {
        expect(computeGridDimensions(n, W, H, 0)).toEqual(legacy(n, aspect))
      }
    }
  })

  it('never reduces cols as chrome grows (width-first bias)', () => {
    for (const [W, H] of [[1600, 900], [900, 1600], [600, 1200]] as const) {
      for (const n of [4, 6, 8, 12]) {
        let prevCols = computeGridDimensions(n, W, H, 0).cols
        for (const chrome of [40, 96, 160, 240]) {
          const cols = computeGridDimensions(n, W, H, chrome).cols
          expect(cols).toBeGreaterThanOrEqual(prevCols)
          prevCols = cols
        }
      }
    }
  })

  it('clamps columns to the upper bound', () => {
    // Ultra-wide, many cells, large chrome → cols pinned at 5.
    const { cols } = computeGridDimensions(20, 2400, 600, 96)
    expect(cols).toBe(5)
  })

  it('respects the minimum 2x2 floor for tiny counts', () => {
    expect(computeGridDimensions(0, 1600, 900, 96)).toEqual({ cols: 2, rows: 2 })
    expect(computeGridDimensions(1, 1600, 900, 96)).toEqual({ cols: 2, rows: 2 })
  })

  it('falls back to the plain square-root heuristic when dimensions are unknown', () => {
    // Unknown size → sqrt(n) with aspect 1, i.e. legacy(n, 1).
    expect(computeGridDimensions(9, 0, 0, 96)).toEqual(legacy(9, 1))
    expect(computeGridDimensions(9, NaN, NaN, 96)).toEqual(legacy(9, 1))
  })

  it('treats a non-positive chrome as zero', () => {
    expect(computeGridDimensions(8, 1600, 900, -10)).toEqual(computeGridDimensions(8, 1600, 900, 0))
    expect(computeGridDimensions(8, 1600, 900, NaN)).toEqual(computeGridDimensions(8, 1600, 900, 0))
  })
})
