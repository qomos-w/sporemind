import { describe, it, expect } from 'vitest'
import { computeQueueReveal, countChars, clamp } from './useSmoothStreamContent'

describe('clamp', () => {
  it('clamps within range', () => {
    expect(clamp(5, 0, 10)).toBe(5)
    expect(clamp(-1, 0, 10)).toBe(0)
    expect(clamp(15, 0, 10)).toBe(10)
  })
})

describe('countChars', () => {
  it('counts ASCII characters', () => {
    expect(countChars('hello')).toBe(5)
  })

  it('counts code points, not UTF-16 units', () => {
    expect(countChars('你好')).toBe(2)
    expect(countChars('🎉')).toBe(1)
  })

  it('empty string is zero', () => {
    expect(countChars('')).toBe(0)
  })
})

describe('computeQueueReveal', () => {
  it('returns 0 for zero backlog', () => {
    expect(computeQueueReveal(0, 16)).toBe(0)
  })

  it('returns 0 for zero dt', () => {
    expect(computeQueueReveal(100, 0)).toBe(0)
  })

  it('reveals at least 1 character for small backlog', () => {
    expect(computeQueueReveal(1, 16)).toBe(1)
    expect(computeQueueReveal(3, 16)).toBe(1)
  })

  it('reveals proportional to backlog for burst', () => {
    const reveal = computeQueueReveal(160, 16.67)
    // backlog / 8 * (16.67/16.67) = 160/8 = 20
    expect(reveal).toBe(20)
    expect(reveal).toBeLessThan(160)
  })

  it('never reveals more than backlog', () => {
    // computeQueueReveal(5, 100) = ceil(5/8 * 100/16.67) = ceil(3.75) = 4, min(5, 4) = 4
    expect(computeQueueReveal(5, 100)).toBe(4)
    // Large backlog with large dt still capped to backlog
    expect(computeQueueReveal(2, 200)).toBe(2)
  })

  it('scales with frame time', () => {
    const shortFrame = computeQueueReveal(160, 8)
    const normalFrame = computeQueueReveal(160, 16.67)
    // Half the frame time → roughly half the reveal
    expect(shortFrame).toBeLessThan(normalFrame)
  })
})
