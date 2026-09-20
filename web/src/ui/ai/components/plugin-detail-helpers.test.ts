import { describe, expect, it } from 'vitest'
import { formatHashShort, groupCapabilities, isCapabilityGranted } from './plugin-detail-helpers'

describe('formatHashShort', () => {
  it('returns empty display for undefined hash', () => {
    expect(formatHashShort(undefined)).toEqual({ short: '', full: '' })
  })

  it('keeps short hashes as-is', () => {
    expect(formatHashShort('abcd1234')).toEqual({ short: 'abcd1234', full: 'abcd1234' })
  })

  it('truncates long hashes to the first 8 characters', () => {
    const hash = 'aabbccddeeff00112233445566778899'
    expect(formatHashShort(hash)).toEqual({ short: 'aabbccdd…', full: hash })
  })
})

describe('groupCapabilities', () => {
  it('splits permissions into granted and denied sets', () => {
    expect(groupCapabilities(['fs.read', 'fs.write', 'llm.invoke'], ['fs.read', 'llm.invoke']))
      .toEqual({ granted: ['fs.read', 'llm.invoke'], denied: ['fs.write'] })
  })

  it('treats all permissions as denied when granted list is empty', () => {
    expect(groupCapabilities(['fs.read', 'fs.write'], []))
      .toEqual({ granted: [], denied: ['fs.read', 'fs.write'] })
  })

  it('returns empty groups when no permissions are declared', () => {
    expect(groupCapabilities(undefined, ['fs.read'])).toEqual({ granted: [], denied: [] })
  })
})

describe('isCapabilityGranted', () => {
  it('returns true when the capability is in the granted list', () => {
    expect(isCapabilityGranted('fs.read', ['fs.read', 'llm.invoke'])).toBe(true)
  })

  it('returns false when the capability is not granted', () => {
    expect(isCapabilityGranted('fs.write', ['fs.read'])).toBe(false)
  })

  it('returns false when the granted list is undefined', () => {
    expect(isCapabilityGranted('fs.read', undefined)).toBe(false)
  })
})
