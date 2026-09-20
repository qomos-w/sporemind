/**
 * Tests for the index-packing strategy used when OES_element_index_uint is
 * unavailable. These exercise the pure function exported from the renderer
 * module, avoiding the need for a WebGL context.
 */
import { describe, it, expect } from 'vitest'
import { packIndexBufferForType } from './webglRenderer'

describe('packIndexBufferForType', () => {
  it('returns Uint32Array when uint32 is available', () => {
    const buf = packIndexBufferForType([0, 1, 2, 70000], true)
    expect(buf).toBeInstanceOf(Uint32Array)
    expect([...buf]).toEqual([0, 1, 2, 70000])
  })

  it('downgrades to Uint16Array when uint32 is unavailable and all indices fit', () => {
    const buf = packIndexBufferForType([0, 1, 2, 3, 1000], false)
    expect(buf).toBeInstanceOf(Uint16Array)
    expect([...buf]).toEqual([0, 1, 2, 3, 1000])
  })

  it('throws when uint32 is unavailable and an index exceeds 65535', () => {
    expect(() => packIndexBufferForType([0, 1, 70000], false)).toThrow(
      /OES_element_index_uint not available/,
    )
    expect(() => packIndexBufferForType([0, 1, 70000], false)).toThrow(
      /max index 70000 exceeds 65535/,
    )
  })

  it('accepts an index of exactly 65535 in uint16 mode', () => {
    const buf = packIndexBufferForType([65535, 0, 1], false)
    expect(buf).toBeInstanceOf(Uint16Array)
    expect([...buf]).toEqual([65535, 0, 1])
  })

  it('throws for index 65536 in uint16 mode (off-by-one boundary)', () => {
    expect(() => packIndexBufferForType([65536, 0, 1], false)).toThrow(
      /max index 65536 exceeds 65535/,
    )
  })

  it('does not throw for large indices when uint32 is available', () => {
    const buf = packIndexBufferForType([0, 1, 2, 1000000], true)
    expect(buf).toBeInstanceOf(Uint32Array)
    expect([...buf]).toEqual([0, 1, 2, 1000000])
  })

  it('preserves Uint16Array values exactly (no reinterpretation corruption)', () => {
    const indices = [0, 1, 2, 0, 2, 3]
    const buf32 = packIndexBufferForType(indices, true)
    const buf16 = packIndexBufferForType(indices, false)
    // Both buffers should contain the same values — the 16-bit version is a
    // genuine re-pack, not a reinterpretation of the 32-bit bytes.
    expect([...buf32]).toEqual(indices)
    expect([...buf16]).toEqual(indices)
  })
})
