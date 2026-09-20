import { describe, it, expect } from 'vitest'
import {
  isPassActive,
  isPermanentExpiry,
  resolveAccountTierState,
  resolveContentAccess,
  resolveInstallGate,
} from './tier-badges'
import type { PassViewLike } from './tier-badges'

const now = new Date('2026-08-23T12:00:00Z')

describe('isPassActive', () => {
  it('returns false for no pass or empty Type', () => {
    expect(isPassActive(undefined, now)).toBe(false)
    expect(isPassActive({ Type: '', EndsAt: '9999-12-31T23:59:59Z' }, now)).toBe(false)
    expect(isPassActive({}, now)).toBe(false)
  })

  it('treats a missing boundary as open-ended', () => {
    expect(isPassActive({ Type: 'annual' }, now)).toBe(true)
    expect(isPassActive({ Type: 'annual', StartsAt: '2026-01-01T00:00:00Z' }, now)).toBe(true)
  })

  it('is active inside the window', () => {
    const pass: PassViewLike = { Type: 'annual', StartsAt: '2026-01-01T00:00:00Z', EndsAt: '2027-01-01T00:00:00Z' }
    expect(isPassActive(pass, now)).toBe(true)
  })

  it('is inactive before start and after end', () => {
    const pass: PassViewLike = { Type: 'annual', StartsAt: '2026-01-01T00:00:00Z', EndsAt: '2027-01-01T00:00:00Z' }
    expect(isPassActive(pass, new Date('2025-12-31T23:59:59Z'))).toBe(false)
    expect(isPassActive(pass, new Date('2027-01-01T00:00:01Z'))).toBe(false)
  })
})

describe('isPermanentExpiry', () => {
  it('treats year >= 9999 (UTC) as permanent', () => {
    expect(isPermanentExpiry('9999-12-31T23:59:59Z')).toBe(true)
    expect(isPermanentExpiry('9999-01-01T00:00:00Z')).toBe(true)
  })

  it('treats finite dates as non-permanent', () => {
    expect(isPermanentExpiry('2027-01-01T00:00:00Z')).toBe(false)
    expect(isPermanentExpiry(undefined)).toBe(false)
    expect(isPermanentExpiry('not-a-date')).toBe(false)
  })
})

describe('resolveAccountTierState', () => {
  it('ultimate wins regardless of pass', () => {
    const state = resolveAccountTierState('ultimate', { Type: 'annual', EndsAt: '2027-01-01T00:00:00Z' }, now)
    expect(state.kind).toBe('ultimate')
  })

  it('active pass maps to insider with permanent flag (9999 special case)', () => {
    const state = resolveAccountTierState('pro', { Type: 'experimental', EndsAt: '9999-12-31T23:59:59Z' }, now)
    expect(state).toEqual({ kind: 'insider', endsAt: '9999-12-31T23:59:59Z', permanent: true })
  })

  it('finite pass maps to insider with permanent=false and keeps endsAt', () => {
    const state = resolveAccountTierState('pro', { Type: 'experimental', EndsAt: '2027-01-01T00:00:00Z' }, now)
    expect(state).toEqual({ kind: 'insider', endsAt: '2027-01-01T00:00:00Z', permanent: false })
  })

  it('expired or not-yet-started pass falls back to free', () => {
    expect(resolveAccountTierState('pro', { Type: 'experimental', EndsAt: '2026-01-01T00:00:00Z' }, now).kind).toBe('free')
    expect(resolveAccountTierState('pro', { Type: 'experimental', StartsAt: '2026-09-01T00:00:00Z' }, now).kind).toBe('free')
  })

  it('no pass or no tier resolves to free', () => {
    expect(resolveAccountTierState('free', undefined, now).kind).toBe('free')
    expect(resolveAccountTierState(undefined, undefined, now).kind).toBe('free')
    expect(resolveAccountTierState('pro', { Type: '' }, now).kind).toBe('free')
  })
})

describe('resolveContentAccess', () => {
  it('ultimate has both experimental and paid access', () => {
    const access = resolveContentAccess('ultimate', undefined, now)
    expect(access).toEqual({ hasExperimentalAccess: true, hasPaidAccess: true })
  })

  it('active insider pass unlocks experimental only', () => {
    const access = resolveContentAccess('pro', { Type: 'experimental', EndsAt: '2027-01-01T00:00:00Z' }, now)
    expect(access).toEqual({ hasExperimentalAccess: true, hasPaidAccess: false })
  })

  it('free or expired pass has no access', () => {
    expect(resolveContentAccess('free', undefined, now)).toEqual({ hasExperimentalAccess: false, hasPaidAccess: false })
    expect(resolveContentAccess('pro', { Type: 'experimental', EndsAt: '2026-01-01T00:00:00Z' }, now)).toEqual({
      hasExperimentalAccess: false,
      hasPaidAccess: false,
    })
  })
})

describe('resolveInstallGate', () => {
  const noAccess = { hasExperimentalAccess: false, hasPaidAccess: false }
  const insider = { hasExperimentalAccess: true, hasPaidAccess: false }
  const ultimate = { hasExperimentalAccess: true, hasPaidAccess: true }

  it('stable/free items are installable for everyone', () => {
    expect(resolveInstallGate({ Visibility: 'stable', Pricing: 'free' }, noAccess)).toBe('installable')
    expect(resolveInstallGate({}, noAccess)).toBe('installable')
  })

  it('experimental items lock without Insider/Ultimate access', () => {
    expect(resolveInstallGate({ Visibility: 'experimental', Pricing: 'free' }, noAccess)).toBe('locked_experimental')
    expect(resolveInstallGate({ Visibility: 'experimental', Pricing: 'free' }, insider)).toBe('installable')
    expect(resolveInstallGate({ Visibility: 'experimental', Pricing: 'free' }, ultimate)).toBe('installable')
  })

  it('paid items lock below Ultimate even for Insider pass holders', () => {
    expect(resolveInstallGate({ Visibility: 'stable', Pricing: 'paid' }, insider)).toBe('locked_paid')
    expect(resolveInstallGate({ Visibility: 'stable', Pricing: 'paid' }, noAccess)).toBe('locked_paid')
    expect(resolveInstallGate({ Visibility: 'stable', Pricing: 'paid' }, ultimate)).toBe('installable')
  })

  it('experimental+paid item reports experimental lock first', () => {
    expect(resolveInstallGate({ Visibility: 'experimental', Pricing: 'paid' }, noAccess)).toBe('locked_experimental')
    expect(resolveInstallGate({ Visibility: 'experimental', Pricing: 'paid' }, insider)).toBe('locked_paid')
  })
})