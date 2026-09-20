import { describe, it, expect } from 'vitest'
import {
  FILE_COLUMN_MIN,
  FILE_COLUMN_DEFAULT,
  FILE_COLUMN_ORDER,
  clampColumnWidth,
  buildFileGridCols,
  type FileColumnWidths,
} from './sshFileColumns'

describe('sshFileColumns', () => {
  describe('clampColumnWidth', () => {
    it('keeps values above the minimum, rounded to whole pixels', () => {
      expect(clampColumnWidth('size', 200)).toBe(200)
      expect(clampColumnWidth('size', 73.6)).toBe(74)
    })

    it('clamps to the per-column minimum and never below', () => {
      expect(clampColumnWidth('name', 10)).toBe(FILE_COLUMN_MIN.name)
      expect(clampColumnWidth('size', 0)).toBe(FILE_COLUMN_MIN.size)
      expect(clampColumnWidth('mode', -5)).toBe(FILE_COLUMN_MIN.mode)
      expect(clampColumnWidth('modified', 50)).toBe(FILE_COLUMN_MIN.modified)
    })

    it('respects each column having its own distinct minimum', () => {
      const mins = FILE_COLUMN_ORDER.map(k => clampColumnWidth(k, 1))
      // every column clamps to its own minimum, not a shared one
      expect(mins).toEqual(FILE_COLUMN_ORDER.map(k => FILE_COLUMN_MIN[k]))
      expect(new Set(mins).size).toBeGreaterThan(1)
    })
  })

  describe('buildFileGridCols', () => {
    it('emits explicit pixel widths for all four columns in fixed order', () => {
      const w: FileColumnWidths = { name: 240, size: 80, mode: 100, modified: 120 }
      expect(buildFileGridCols(w)).toBe('240px 80px 100px 120px 1fr')
    })

    it('ends with a flexible 1fr track so the grid always fills the container', () => {
      expect(buildFileGridCols(FILE_COLUMN_DEFAULT)).toMatch(/ 1fr$/)
      // no leading flexible track — every content column is a concrete px value
      expect(buildFileGridCols(FILE_COLUMN_DEFAULT)).not.toMatch(/^1fr/)
    })

    it('reflects resized widths so header and rows stay aligned', () => {
      const w: FileColumnWidths = { name: 300, size: 60, mode: 120, modified: 160 }
      expect(buildFileGridCols(w)).toBe('300px 60px 120px 160px 1fr')
    })
  })

  it('default widths all satisfy their own minimums', () => {
    for (const k of FILE_COLUMN_ORDER) {
      expect(FILE_COLUMN_DEFAULT[k]).toBeGreaterThanOrEqual(FILE_COLUMN_MIN[k])
    }
  })
})
