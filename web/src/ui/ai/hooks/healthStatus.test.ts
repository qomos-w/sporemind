import { describe, it, expect } from 'vitest'
import {
  isCoolingDown,
  isDisabled,
  isUnavailable,
  formatCountdown,
  cooldownRatio,
  healthReasonLabelKey,
  recoveryModeLabelKey,
  type HealthProjection,
} from './healthStatus'

const NOW_MS = 1_700_000_000_000 // fixed reference time

describe('isCoolingDown', () => {
  it('returns true when healthState is cooling_down and deadline is in the future', () => {
    const p: HealthProjection = {
      healthState: 'cooling_down',
      cooldownUntil: NOW_MS / 1000 + 30,
    }
    expect(isCoolingDown(p, NOW_MS)).toBe(true)
  })

  it('returns false when deadline has passed', () => {
    const p: HealthProjection = {
      healthState: 'cooling_down',
      cooldownUntil: NOW_MS / 1000 - 5,
    }
    expect(isCoolingDown(p, NOW_MS)).toBe(false)
  })

  it('returns false when healthState is healthy', () => {
    const p: HealthProjection = { healthState: 'healthy' }
    expect(isCoolingDown(p, NOW_MS)).toBe(false)
  })

  it('returns false when healthState is disabled', () => {
    const p: HealthProjection = { healthState: 'disabled' }
    expect(isCoolingDown(p, NOW_MS)).toBe(false)
  })

  it('returns false when cooldownUntil is missing', () => {
    const p: HealthProjection = { healthState: 'cooling_down' }
    expect(isCoolingDown(p, NOW_MS)).toBe(false)
  })
})

describe('isDisabled', () => {
  it('returns true when healthState is disabled', () => {
    expect(isDisabled({ healthState: 'disabled' })).toBe(true)
  })

  it('returns false for other states', () => {
    expect(isDisabled({ healthState: 'cooling_down' })).toBe(false)
    expect(isDisabled({ healthState: 'healthy' })).toBe(false)
    expect(isDisabled({})).toBe(false)
  })
})

describe('isUnavailable', () => {
  it('returns true for cooling_down with future deadline', () => {
    expect(isUnavailable({ healthState: 'cooling_down', cooldownUntil: NOW_MS / 1000 + 10 }, NOW_MS)).toBe(true)
  })

  it('returns true for disabled', () => {
    expect(isUnavailable({ healthState: 'disabled' }, NOW_MS)).toBe(true)
  })

  it('returns false for healthy', () => {
    expect(isUnavailable({ healthState: 'healthy' }, NOW_MS)).toBe(false)
  })
})

describe('formatCountdown', () => {
  it('formats sub-minute as mm:ss', () => {
    const deadline = NOW_MS / 1000 + 42
    expect(formatCountdown(deadline, NOW_MS)).toBe('00:42')
  })

  it('formats minutes as mm:ss', () => {
    const deadline = NOW_MS / 1000 + 84 // 1:24
    expect(formatCountdown(deadline, NOW_MS)).toBe('01:24')
  })

  it('formats hours as h:mm:ss', () => {
    const deadline = NOW_MS / 1000 + 3738 // 1:02:18
    expect(formatCountdown(deadline, NOW_MS)).toBe('1:02:18')
  })

  it('clamps to zero when deadline passed', () => {
    const deadline = NOW_MS / 1000 - 10
    expect(formatCountdown(deadline, NOW_MS)).toBe('00:00')
  })
})

describe('cooldownRatio', () => {
  it('returns 0 when startEpochSec is undefined', () => {
    expect(cooldownRatio(undefined, NOW_MS / 1000 + 30, NOW_MS)).toBe(0)
  })

  it('returns 0 when total duration is zero or negative', () => {
    const start = NOW_MS / 1000 + 30
    const deadline = NOW_MS / 1000 + 30
    expect(cooldownRatio(start, deadline, NOW_MS)).toBe(0)
  })

  it('returns 0.5 at midpoint', () => {
    const start = NOW_MS / 1000
    const deadline = NOW_MS / 1000 + 60
    const midNow = NOW_MS + 30_000
    expect(cooldownRatio(start, deadline, midNow)).toBeCloseTo(0.5, 2)
  })

  it('clamps to 1 when elapsed exceeds total', () => {
    const start = NOW_MS / 1000
    const deadline = NOW_MS / 1000 + 30
    expect(cooldownRatio(start, deadline, NOW_MS + 60_000)).toBe(1)
  })
})

describe('label keys', () => {
  it('returns the reason as-is', () => {
    expect(healthReasonLabelKey('quota_exhausted')).toBe('quota_exhausted')
  })

  it('returns unknown for undefined', () => {
    expect(healthReasonLabelKey(undefined)).toBe('unknown')
  })

  it('returns unknown for empty string (agg-ref rows without a reason)', () => {
    expect(healthReasonLabelKey('')).toBe('unknown')
  })

  it('returns the recovery mode as-is', () => {
    expect(recoveryModeLabelKey('probe')).toBe('probe')
  })

  it('returns cooldown for undefined recovery mode', () => {
    expect(recoveryModeLabelKey(undefined)).toBe('cooldown')
  })
})
