import { describe, expect, it } from 'vitest'
import { formatRelativeTime } from './format-time'

const NOW = new Date('2026-08-13T12:00:00Z').getTime()

describe('formatRelativeTime', () => {
  it('formats hours and days ago in English', () => {
    expect(formatRelativeTime('2026-08-13T09:00:00Z', 'en-US', NOW)).toBe('3 hours ago')
    expect(formatRelativeTime('2026-08-11T12:00:00Z', 'en-US', NOW)).toBe('2 days ago')
  })

  it('formats relative time in Chinese', () => {
    expect(formatRelativeTime('2026-08-13T09:00:00Z', 'zh-CN', NOW)).toBe('3小时前')
    expect(formatRelativeTime('2026-08-11T12:00:00Z', 'zh-CN', NOW)).toBe('2天前')
  })

  it('returns empty string for missing input and the raw string for invalid dates', () => {
    expect(formatRelativeTime(undefined, 'en-US', NOW)).toBe('')
    expect(formatRelativeTime('not-a-date', 'en-US', NOW)).toBe('not-a-date')
  })

  it('falls back to an absolute string for future dates', () => {
    const future = '2026-08-14T12:00:00Z'
    expect(formatRelativeTime(future, 'en-US', NOW)).toBe(new Date(future).toLocaleString())
  })
})
