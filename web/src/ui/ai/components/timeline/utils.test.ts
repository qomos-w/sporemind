import { describe, it, expect } from 'vitest'
import { truncateLabel } from './utils'

describe('truncateLabel', () => {
  it('returns short text unchanged', () => {
    expect(truncateLabel('reading file')).toBe('reading file')
  })

  it('returns text at exactly the limit unchanged', () => {
    const text = 'a'.repeat(80)
    expect(truncateLabel(text)).toBe(text)
  })

  it('truncates long text with an ellipsis', () => {
    const out = truncateLabel('a'.repeat(120))
    expect(out.length).toBe(81)
    expect(out.endsWith('…')).toBe(true)
  })

  it('honors a custom limit', () => {
    const out = truncateLabel('abcdef', 4)
    expect(out).toBe('abcd…')
  })

  it('counts by code point, not UTF-16 units', () => {
    const emoji = '😀'.repeat(10)
    const out = truncateLabel(emoji, 4)
    expect(Array.from(out)).toEqual(['😀', '😀', '😀', '😀', '…'])
  })

  it('truncates CJK text at the character limit', () => {
    const out = truncateLabel('很长的中文标签内容测试', 6)
    expect(out).toBe('很长的中文标…')
  })

  it('returns text unchanged for non-positive limit', () => {
    expect(truncateLabel('abc', 0)).toBe('abc')
  })

  it('handles empty string', () => {
    expect(truncateLabel('')).toBe('')
  })
})
