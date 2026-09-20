import { describe, it, expect } from 'vitest'
import { computeLineDiff, computeUnifiedDiff } from './card-diff'

describe('computeLineDiff', () => {
  it('returns all unchanged for identical text', () => {
    const diff = computeLineDiff('a\nb\nc', 'a\nb\nc')
    expect(diff).toEqual([
      { type: 'unchanged', value: 'a' },
      { type: 'unchanged', value: 'b' },
      { type: 'unchanged', value: 'c' },
    ])
  })

  it('marks added lines', () => {
    const diff = computeLineDiff('a\nb', 'a\nb\nc')
    expect(diff).toEqual([
      { type: 'unchanged', value: 'a' },
      { type: 'unchanged', value: 'b' },
      { type: 'added', value: 'c' },
    ])
  })

  it('marks removed lines', () => {
    const diff = computeLineDiff('a\nb\nc', 'a\nc')
    expect(diff).toEqual([
      { type: 'unchanged', value: 'a' },
      { type: 'removed', value: 'b' },
      { type: 'unchanged', value: 'c' },
    ])
  })

  it('handles replaced lines', () => {
    const diff = computeLineDiff('a\nb\nc', 'a\nB\nc')
    expect(diff).toEqual([
      { type: 'unchanged', value: 'a' },
      { type: 'removed', value: 'b' },
      { type: 'added', value: 'B' },
      { type: 'unchanged', value: 'c' },
    ])
  })

  it('handles empty old text', () => {
    const diff = computeLineDiff('', 'a\nb')
    expect(diff).toEqual([
      { type: 'added', value: 'a' },
      { type: 'added', value: 'b' },
    ])
  })
})

describe('computeUnifiedDiff', () => {
  it('produces a unified diff with hunks', () => {
    const patch = computeUnifiedDiff('a\nb\nc', 'a\nB\nc', 'card.md')
    expect(patch).toContain('--- a/card.md')
    expect(patch).toContain('+++ b/card.md')
    expect(patch).toContain('-b')
    expect(patch).toContain('+B')
    expect(patch).toContain(' a')
    expect(patch).toContain(' c')
  })

  it('includes hunk headers', () => {
    const patch = computeUnifiedDiff('line1\nline2\nline3\nline4', 'line1\nline2\nchanged\nline4', 'x')
    expect(patch).toMatch(/@@ -\d+,\d+ \+\d+,\d+ @@/)
  })
})
