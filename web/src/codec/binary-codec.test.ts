import { describe, expect, it } from 'vitest'
import {
  decodeAppPayload,
  decodePayloadBytes,
  encodeAppPayload,
  encodePayloadBytes,
  EncodeError,
} from './binary-codec'

describe('binary codec', () => {
  it('encodes and decodes a known schema payload', () => {
    const value = { Username: 'alice', Password: 'secret' }
    const bytes = encodeAppPayload('AuthLoginReq', value)
    expect(bytes.length).toBeGreaterThan(4)
    expect(bytes[0]).toBe(0x54) // 'T'
    expect(bytes[1]).toBe(0x42) // 'B'
    expect(bytes[2]).toBe(0x43) // 'C'
    expect(bytes[3]).toBe(0x02) // version
    const decoded = decodeAppPayload('AuthLoginReq', bytes)
    expect(decoded).toEqual(value)
  })

  it('throws EncodeError for an illegal payload', () => {
    expect(() => encodeAppPayload('AuthLoginReq', { Username: 123, Password: 'secret' })).toThrow(EncodeError)
    expect(() => encodeAppPayload('AuthLoginReq', { Username: 'alice', Password: ['not', 'a', 'string'] })).toThrow(EncodeError)
  })

  it('throws EncodeError for an unknown schema', () => {
    expect(() => encodeAppPayload('NotARealSchema', { x: 1 })).toThrow(EncodeError)
    expect(() => decodeAppPayload('NotARealSchema', new Uint8Array([1, 2, 3]))).toThrow()
  })

  it('throws EncodeError for nested struct type mismatches', () => {
    // AuthLoginResp.Account must be an AccountView struct, not a scalar.
    expect(() =>
      encodeAppPayload('AuthLoginResp', { Token: 't', ExpiresAt: 'x', Account: 'not-a-struct' }),
    ).toThrow(EncodeError)
  })

  it('tolerates absent struct fields on encode (wire omits them)', () => {
    // The TBC wire format is self-describing; missing fields are simply not
    // emitted. Validation rejects wrong types, not absence.
    const bytes = encodeAppPayload('AuthLoginReq', { Username: 'alice' })
    const decoded = decodeAppPayload('AuthLoginReq', bytes) as Record<string, unknown>
    expect(decoded.Username).toBe('alice')
  })

  it('encodes and decodes a nested struct payload', () => {
    const value = {
      Token: 'tok',
      ExpiresAt: '2030-01-01T00:00:00Z',
      Account: { Id: 'acc-1', Username: 'alice' },
    }
    const bytes = encodeAppPayload('AuthLoginResp', value)
    const decoded = decodeAppPayload('AuthLoginResp', bytes) as typeof value
    expect(decoded.Token).toBe('tok')
    expect(decoded.Account.Id).toBe('acc-1')
  })

  it('falls back to JSON text when no schema is provided', () => {
    const value = { answer: 42 }
    const bytes = encodePayloadBytes(value)
    expect(new TextDecoder().decode(bytes)).toBe('{"answer":42}')
    expect(decodePayloadBytes(bytes)).toEqual(value)
  })

  it('falls back to JSON for unregistered schema names', () => {
    const value = { answer: 42 }
    const bytes = encodePayloadBytes(value, 'DefinitelyMissingSchema')
    expect(new TextDecoder().decode(bytes)).toBe('{"answer":42}')
  })

  it('round-trips null through JSON fallback', () => {
    const bytes = encodePayloadBytes(null)
    expect(new TextDecoder().decode(bytes)).toBe('null')
    expect(decodePayloadBytes(bytes)).toBeNull()
  })

  it('returns undefined for empty payload bytes', () => {
    expect(decodePayloadBytes(new Uint8Array())).toBeUndefined()
  })
})
