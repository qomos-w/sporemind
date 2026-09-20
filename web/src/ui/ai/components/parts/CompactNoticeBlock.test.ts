import { describe, it, expect } from 'vitest'
import { roundLabel } from './CompactNoticeBlock.tsx'
import type { CompactionRound } from '../../model/frame-types.ts'

function makeRound(partial: Partial<CompactionRound>): CompactionRound {
  return {
    round: 1,
    level: 1,
    sourceStartIndex: 0,
    sourceEndIndex: 0,
    compactedMessageCount: 0,
    compactedRanges: [],
    afterLayout: [],
    ...partial,
  }
}

describe('roundLabel', () => {
  it('renders summarize range for level 1', () => {
    const label = roundLabel(makeRound({
      round: 1,
      level: 1,
      sourceStartIndex: 0,
      sourceEndIndex: 199,
      compactedMessageCount: 50,
    }))
    expect(label).toBe('Round 1: summarize 0-199 (L1)')
  })

  it('renders merge label for level > 1', () => {
    const label = roundLabel(makeRound({
      round: 2,
      level: 2,
      sourceStartIndex: 0,
      sourceEndIndex: 399,
    }))
    expect(label).toBe('Round 2: merge L1 → L2')
  })
})
