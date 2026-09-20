import { describe, it, expect } from 'vitest'
import {
  base64ToBytes,
  bytesToBase64,
  bytesEqual,
  parseDataUrl,
} from './ImageViewer'

describe('base64ToBytes', () => {
  it('decodes a simple base64 string to bytes', () => {
    expect(Array.from(base64ToBytes('QQ=='))).toEqual([0x41]) // 'A'
    expect(Array.from(base64ToBytes('SGVsbG8='))).toEqual([
      0x48, 0x65, 0x6c, 0x6c, 0x6f, // "Hello"
    ])
  })

  it('handles full byte range 0..255', () => {
    const all = Array.from({ length: 256 }, (_, i) => i)
    const b64 = bytesToBase64(new Uint8Array(all))
    expect(Array.from(base64ToBytes(b64))).toEqual(all)
  })

  it('ignores whitespace in the payload', () => {
    expect(Array.from(base64ToBytes('SGVs\n bG8= '))).toEqual([
      0x48, 0x65, 0x6c, 0x6c, 0x6f,
    ])
  })

  it('returns an empty array for empty input', () => {
    expect(base64ToBytes('').length).toBe(0)
  })
})

describe('bytesToBase64', () => {
  it('encodes bytes to base64', () => {
    expect(bytesToBase64(new Uint8Array([0x41]))).toBe('QQ==')
    expect(bytesToBase64(new Uint8Array([0x48, 0x65, 0x6c, 0x6c, 0x6f]))).toBe('SGVsbG8=')
  })

  it('handles the empty array', () => {
    expect(bytesToBase64(new Uint8Array(0))).toBe('')
  })
})

describe('base64 <-> bytes roundtrip', () => {
  it('is lossless across varied sizes (incl. >B64_CHUNK boundary)', () => {
    const sizes = [0, 1, 2, 3, 64, 255, 256, 4096, 0x8000 + 17]
    for (const size of sizes) {
      const original = new Uint8Array(size)
      for (let i = 0; i < size; i++) original[i] = (i * 37 + 7) & 0xff
      const roundtrip = base64ToBytes(bytesToBase64(original))
      expect(roundtrip.length).toBe(size)
      expect(Array.from(roundtrip)).toEqual(Array.from(original))
    }
  })
})

describe('bytesEqual', () => {
  it('treats identical references as equal', () => {
    const a = new Uint8Array([1, 2, 3])
    expect(bytesEqual(a, a)).toBe(true)
  })

  it('treats same content (different refs) as equal', () => {
    expect(bytesEqual(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2, 3]))).toBe(true)
  })

  it('detects different content', () => {
    expect(bytesEqual(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2, 4]))).toBe(false)
  })

  it('detects different length', () => {
    expect(bytesEqual(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2]))).toBe(false)
  })

  it('handles null operands', () => {
    expect(bytesEqual(null, null)).toBe(true)
    expect(bytesEqual(new Uint8Array(), null)).toBe(false)
    expect(bytesEqual(null, new Uint8Array())).toBe(false)
  })
})

describe('parseDataUrl', () => {
  it('parses a base64 data URL with mime', () => {
    const parsed = parseDataUrl('data:image/png;base64,SGVsbG8=')
    expect(parsed).not.toBeNull()
    expect(parsed!.mime).toBe('image/png')
    expect(Array.from(parsed!.bytes)).toEqual([0x48, 0x65, 0x6c, 0x6c, 0x6f])
  })

  it('lowercases the mime type', () => {
    const parsed = parseDataUrl('data:IMAGE/PNG;base64,QQ==')
    expect(parsed!.mime).toBe('image/png')
  })

  it('defaults mime when omitted', () => {
    const parsed = parseDataUrl('data:;base64,QQ==')
    expect(parsed!.mime).toBe('application/octet-stream')
  })

  it('returns null for a non-data URL', () => {
    expect(parseDataUrl('https://example.com/image.png')).toBeNull()
    expect(parseDataUrl('/local/path.png')).toBeNull()
  })

  it('handles a percent-encoded (non-base64) payload', () => {
    const parsed = parseDataUrl('data:text/plain,Hello%20World')
    expect(parsed).not.toBeNull()
    expect(parsed!.bytes.length).toBeGreaterThan(0)
  })
})

describe('dirty detection logic', () => {
  // Mirrors the component's `dirty` derivation:
  //   dirty = bytes != null && !bytesEqual(bytes, originalBytes)
  const isDirty = (bytes: Uint8Array | null, original: Uint8Array | null) =>
    bytes != null && !bytesEqual(bytes, original)

  it('is clean right after load (bytes === original)', () => {
    const b = new Uint8Array([1, 2, 3])
    expect(isDirty(b, b)).toBe(false)
  })

  it('becomes dirty after an edit', () => {
    const original = new Uint8Array([1, 2, 3])
    const edited = new Uint8Array([1, 2, 9])
    expect(isDirty(edited, original)).toBe(true)
  })

  it('is clean again after original is reset to current bytes (save)', () => {
    const current = new Uint8Array([1, 2, 9])
    expect(isDirty(current, current)).toBe(false)
  })

  it('is never dirty when there are no bytes loaded', () => {
    expect(isDirty(null, new Uint8Array([1]))).toBe(false)
  })
})
