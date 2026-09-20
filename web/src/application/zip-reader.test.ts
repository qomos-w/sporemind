import { describe, it, expect } from 'vitest'
import { extractZipText } from './zip-reader'

function u32le(n: number): Uint8Array {
  const buf = new Uint8Array(4)
  new DataView(buf.buffer).setUint32(0, n, true)
  return buf
}

function u16le(n: number): Uint8Array {
  const buf = new Uint8Array(2)
  new DataView(buf.buffer).setUint16(0, n, true)
  return buf
}

function strBytes(s: string): Uint8Array {
  return new TextEncoder().encode(s)
}

function concat(...parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((sum, p) => sum + p.length, 0)
  const out = new Uint8Array(total)
  let offset = 0
  for (const p of parts) {
    out.set(p, offset)
    offset += p.length
  }
  return out
}

/**
 * Build a minimal stored-only zip containing the given entries.
 */
function buildStoredZip(entries: Record<string, string>): ArrayBuffer {
  const centralHeaders: Uint8Array[] = []
  const parts: Uint8Array[] = []

  let offset = 0
  for (const [name, content] of Object.entries(entries)) {
    const nameBytes = strBytes(name)
    const contentBytes = strBytes(content)
    const crc = 0

    const localHeader = concat(
      u32le(0x04034b50),
      u16le(20),
      u16le(0),
      u16le(0),
      u16le(0),
      u16le(0),
      u32le(crc),
      u32le(contentBytes.length),
      u32le(contentBytes.length),
      u16le(nameBytes.length),
      u16le(0),
      nameBytes,
    )

    parts.push(localHeader, contentBytes)

    const centralHeader = concat(
      u32le(0x02014b50),
      u16le(20),
      u16le(20),
      u16le(0),
      u16le(0),
      u16le(0),
      u16le(0),
      u32le(crc),
      u32le(contentBytes.length),
      u32le(contentBytes.length),
      u16le(nameBytes.length),
      u16le(0),
      u16le(0),
      u16le(0),
      u16le(0),
      u32le(0),
      u32le(offset),
      nameBytes,
    )

    centralHeaders.push(centralHeader)
    offset += localHeader.length + contentBytes.length
  }

  const centralDir = concat(...centralHeaders)
  const centralOffset = offset

  const eocd = concat(
    u32le(0x06054b50),
    u16le(0),
    u16le(0),
    u16le(Object.keys(entries).length),
    u16le(Object.keys(entries).length),
    u32le(centralDir.length),
    u32le(centralOffset),
    u16le(0),
  )

  return concat(...parts, centralDir, eocd).buffer
}

describe('extractZipText', () => {
  it('extracts a stored text entry', async () => {
    const zip = buildStoredZip({
      'app.manifest.json': '{"Id":"test.app","Version":"1.0.0"}',
      'main.spore': 'export default {}',
    })
    const text = await extractZipText(zip, 'app.manifest.json')
    expect(JSON.parse(text).Id).toBe('test.app')
  })

  it('throws when entry is missing', async () => {
    const zip = buildStoredZip({ 'main.spore': 'x' })
    await expect(extractZipText(zip, 'app.manifest.json')).rejects.toThrow('not found')
  })

  it('parses a deflate-compressed entry', async () => {
    const { deflateRawSync } = require('zlib')
    const manifestJson = JSON.stringify({ Id: 'test.local.app', Version: '1.2.3' })
    const compressed = deflateRawSync(Buffer.from(manifestJson))
    const nameBytes = strBytes('app.manifest.json')
    const crc = 0

    const localHeader = concat(
      u32le(0x04034b50), u16le(20), u16le(0), u16le(8), u16le(0), u16le(0),
      u32le(crc), u32le(compressed.length), u32le(manifestJson.length),
      u16le(nameBytes.length), u16le(0), nameBytes,
    )
    const centralHeader = concat(
      u32le(0x02014b50), u16le(20), u16le(20), u16le(0), u16le(8), u16le(0), u16le(0),
      u32le(crc), u32le(compressed.length), u32le(manifestJson.length),
      u16le(nameBytes.length), u16le(0), u16le(0), u16le(0), u16le(0),
      u32le(0), u32le(0), nameBytes,
    )
    const eocd = concat(
      u32le(0x06054b50), u16le(0), u16le(0), u16le(1), u16le(1),
      u32le(centralHeader.length), u32le(localHeader.length + compressed.length), u16le(0),
    )
    const zip = concat(localHeader, new Uint8Array(compressed), centralHeader, eocd).buffer
    const text = await extractZipText(zip, 'app.manifest.json')
    const manifest = JSON.parse(text)
    expect(manifest.Id).toBe('test.local.app')
    expect(manifest.Version).toBe('1.2.3')
  })
})
