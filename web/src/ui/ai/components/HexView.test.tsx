import { describe, it, expect } from 'vitest'
import {
  BYTES_PER_ROW,
  DEFAULT_PAGE_SIZE,
  toHexByte,
  toHexNibble,
  toHexOffset,
  toAsciiChar,
  parseHexNibble,
  buildHexRows,
  totalPagesFor,
  pageRangeFor,
  isByteEdited,
} from './HexView'

describe('toHexByte', () => {
  it('formats 0..255 as two lowercase hex digits', () => {
    expect(toHexByte(0)).toBe('00')
    expect(toHexByte(10)).toBe('0a')
    expect(toHexByte(0x41)).toBe('41')
    expect(toHexByte(255)).toBe('ff')
  })

  it('masks out-of-range values to a single byte', () => {
    expect(toHexByte(256)).toBe('00')
    expect(toHexByte(0x1ff)).toBe('ff')
    expect(toHexByte(-1)).toBe('ff')
  })
})

describe('toHexNibble', () => {
  it('formats 0..15 as one lowercase hex digit', () => {
    expect(toHexNibble(0)).toBe('0')
    expect(toHexNibble(10)).toBe('a')
    expect(toHexNibble(15)).toBe('f')
  })

  it('masks to a single nibble', () => {
    expect(toHexNibble(16)).toBe('0')
    expect(toHexNibble(0x1f)).toBe('f')
  })
})

describe('toHexOffset', () => {
  it('pads small offsets to eight hex digits', () => {
    expect(toHexOffset(0)).toBe('00000000')
    expect(toHexOffset(16)).toBe('00000010')
    expect(toHexOffset(255)).toBe('000000ff')
    expect(toHexOffset(4096)).toBe('00001000')
  })

  it('never truncates offsets larger than 32 bits', () => {
    expect(toHexOffset(0x100000000)).toBe('100000000')
  })

  it('clamps negative input', () => {
    expect(toHexOffset(-5)).toBe('00000000')
  })
})

describe('toAsciiChar', () => {
  it('returns the printable char for 0x21..0x7e', () => {
    expect(toAsciiChar(0x41)).toBe('A')
    expect(toAsciiChar(0x61)).toBe('a')
    expect(toAsciiChar(0x30)).toBe('0')
    expect(toAsciiChar(0x7e)).toBe('~')
  })

  it('returns "." for non-printable / control bytes', () => {
    expect(toAsciiChar(0)).toBe('.')
    expect(toAsciiChar(0x0a)).toBe('.')
    expect(toAsciiChar(0x7f)).toBe('.')
    expect(toAsciiChar(0xff)).toBe('.')
  })

  it('returns "." for the space byte', () => {
    expect(toAsciiChar(0x20)).toBe('.')
  })
})

describe('parseHexNibble', () => {
  it('parses digits and letters in both cases', () => {
    expect(parseHexNibble('0')).toBe(0)
    expect(parseHexNibble('9')).toBe(9)
    expect(parseHexNibble('a')).toBe(10)
    expect(parseHexNibble('f')).toBe(15)
    expect(parseHexNibble('A')).toBe(10)
    expect(parseHexNibble('F')).toBe(15)
  })

  it('returns -1 for non-hex or multi-char input', () => {
    expect(parseHexNibble('g')).toBe(-1)
    expect(parseHexNibble('G')).toBe(-1)
    expect(parseHexNibble('!')).toBe(-1)
    expect(parseHexNibble('')).toBe(-1)
    expect(parseHexNibble('1f')).toBe(-1)
  })
})

describe('buildHexRows', () => {
  it('returns no rows for empty input', () => {
    expect(buildHexRows(new Uint8Array(0), 0)).toEqual([])
  })

  it('builds a single partial row for fewer than 16 bytes', () => {
    const rows = buildHexRows(new Uint8Array([0x48, 0x65, 0x6c, 0x6c, 0x6f]), 0)
    expect(rows).toHaveLength(1)
    expect(rows[0]).toEqual({ offset: 0, bytes: [0x48, 0x65, 0x6c, 0x6c, 0x6f] })
  })

  it('packs exactly 16 bytes per row', () => {
    const data = new Uint8Array(BYTES_PER_ROW * 2)
    for (let i = 0; i < data.length; i++) data[i] = i & 0xff
    const rows = buildHexRows(data, 0)
    expect(rows).toHaveLength(2)
    expect(rows[0]!.bytes).toHaveLength(16)
    expect(rows[1]!.bytes).toHaveLength(16)
    expect(rows[0]!.offset).toBe(0)
    expect(rows[1]!.offset).toBe(16)
    expect(rows[0]!.bytes[0]).toBe(0)
    expect(rows[1]!.bytes[0]).toBe(16)
  })

  it('starts rows at the given byte offset and sets absolute offsets', () => {
    const data = new Uint8Array(40)
    data[16] = 0xab
    const rows = buildHexRows(data, 16, 2)
    expect(rows).toHaveLength(2)
    expect(rows[0]!.offset).toBe(16)
    expect(rows[1]!.offset).toBe(32)
    expect(rows[0]!.bytes[0]).toBe(0xab)
  })

  it('clamps an offset beyond the data to empty', () => {
    expect(buildHexRows(new Uint8Array(8), 100)).toEqual([])
  })

  it('clamps rowCount to the available rows', () => {
    const rows = buildHexRows(new Uint8Array(20), 0, 99)
    // 20 bytes -> 2 rows
    expect(rows).toHaveLength(2)
  })

  it('truncates the final row of a partial page', () => {
    const rows = buildHexRows(new Uint8Array(20), 0)
    expect(rows[1]!.bytes).toHaveLength(4)
  })

  it('respects an explicit rowCount shorter than the data', () => {
    const data = new Uint8Array(BYTES_PER_ROW * 3)
    const rows = buildHexRows(data, 0, 1)
    expect(rows).toHaveLength(1)
    expect(rows[0]!.offset).toBe(0)
  })
})

describe('totalPagesFor', () => {
  it('is at least 1 even for empty data', () => {
    expect(totalPagesFor(0, 4096)).toBe(1)
  })

  it('fits exactly full pages', () => {
    expect(totalPagesFor(DEFAULT_PAGE_SIZE, DEFAULT_PAGE_SIZE)).toBe(1)
  })

  it('spills a single extra byte into a new page', () => {
    expect(totalPagesFor(DEFAULT_PAGE_SIZE + 1, DEFAULT_PAGE_SIZE)).toBe(2)
  })

  it('honours a custom page size', () => {
    expect(totalPagesFor(64, 16)).toBe(4)
    expect(totalPagesFor(65, 16)).toBe(5)
  })
})

describe('pageRangeFor', () => {
  it('returns the half-open range for the first page', () => {
    expect(pageRangeFor(0, 4096, 5000)).toEqual({ start: 0, end: 4096 })
  })

  it('clamps the last page to the byte length', () => {
    expect(pageRangeFor(1, 4096, 5000)).toEqual({ start: 4096, end: 5000 })
  })

  it('returns an empty range for empty data', () => {
    expect(pageRangeFor(0, 4096, 0)).toEqual({ start: 0, end: 0 })
  })
})

describe('isByteEdited', () => {
  const bytes = new Uint8Array([1, 2, 3, 4])
  const original = new Uint8Array([1, 9, 3, 4])

  it('is false when originalBytes is undefined', () => {
    expect(isByteEdited(bytes, undefined, 1)).toBe(false)
  })

  it('is false when the byte matches the original', () => {
    expect(isByteEdited(bytes, original, 0)).toBe(false)
    expect(isByteEdited(bytes, original, 2)).toBe(false)
  })

  it('is true when the byte differs from the original', () => {
    expect(isByteEdited(bytes, original, 1)).toBe(true)
  })

  it('is true for bytes present now but absent from the original snapshot', () => {
    const grew = new Uint8Array([1, 2, 3, 4, 5])
    const shortOriginal = new Uint8Array([1, 2, 3, 4])
    expect(isByteEdited(grew, shortOriginal, 4)).toBe(true)
    expect(isByteEdited(grew, shortOriginal, 0)).toBe(false)
  })

  it('is false for out-of-range indices', () => {
    expect(isByteEdited(bytes, original, -1)).toBe(false)
    expect(isByteEdited(bytes, original, 99)).toBe(false)
  })
})
