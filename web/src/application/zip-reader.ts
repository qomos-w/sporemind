/**
 * Minimal ZIP reader for the local app install flow.
 *
 * Only supports extracting a single named entry by reading the central
 * directory. Handles stored (0) and deflate (8) compression. Zip64 and
 * encryption are not supported because local install packages are small,
 * plain zip files produced by the toolchain.
 */

const EOCD_SIG = 0x06054b50
const CDFH_SIG = 0x02014b50
const LFH_SIG = 0x04034b50

const COMPRESSION_STORED = 0
const COMPRESSION_DEFLATE = 8

function readUInt16LE(view: DataView, offset: number): number {
  return view.getUint16(offset, true)
}

function readUInt32LE(view: DataView, offset: number): number {
  return view.getUint32(offset, true)
}

function readString(view: DataView, offset: number, length: number): string {
  const bytes = new Uint8Array(view.buffer, offset, length)
  let s = ''
  for (let i = 0; i < bytes.length; i++) {
    s += String.fromCharCode(bytes[i]!)
  }
  return s
}

function findEocdOffset(view: DataView): number {
  // Search backwards for the EOCD signature, allowing for a 64 KiB comment.
  const maxCommentLen = 0xffff
  const start = Math.max(0, view.byteLength - 22 - maxCommentLen)
  for (let i = view.byteLength - 22; i >= start; i--) {
    if (readUInt32LE(view, i) === EOCD_SIG) {
      return i
    }
  }
  throw new Error('zip-reader: End of Central Directory not found')
}

interface CentralDirEntry {
  name: string
  compressionMethod: number
  compressedSize: number
  uncompressedSize: number
  localHeaderOffset: number
}

function readCentralDirectory(view: DataView, eocdOffset: number): CentralDirEntry[] {
  const cdSize = readUInt32LE(view, eocdOffset + 12)
  const cdOffset = readUInt32LE(view, eocdOffset + 16)
  const entries: CentralDirEntry[] = []

  let p = cdOffset
  const end = cdOffset + cdSize
  while (p < end) {
    const sig = readUInt32LE(view, p)
    if (sig !== CDFH_SIG) {
      throw new Error(`zip-reader: unexpected central directory signature 0x${sig.toString(16)}`)
    }
    const nameLen = readUInt16LE(view, p + 28)
    const extraLen = readUInt16LE(view, p + 30)
    const commentLen = readUInt16LE(view, p + 32)
    const compressionMethod = readUInt16LE(view, p + 10)
    const compressedSize = readUInt32LE(view, p + 20)
    const uncompressedSize = readUInt32LE(view, p + 24)
    const localHeaderOffset = readUInt32LE(view, p + 42)
    const name = readString(view, p + 46, nameLen)

    entries.push({
      name,
      compressionMethod,
      compressedSize,
      uncompressedSize,
      localHeaderOffset,
    })

    p += 46 + nameLen + extraLen + commentLen
  }

  return entries
}

async function inflateRaw(data: Uint8Array): Promise<Uint8Array> {
  const ds = new DecompressionStream('deflate-raw')
  const writer = ds.writable.getWriter()
  void writer.write(data)
  void writer.close()
  const reader = ds.readable.getReader()
  const chunks: Uint8Array[] = []
  let total = 0
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    chunks.push(value)
    total += value.length
  }
  const out = new Uint8Array(total)
  let offset = 0
  for (const chunk of chunks) {
    out.set(chunk, offset)
    offset += chunk.length
  }
  return out
}

async function readLocalEntry(
  view: DataView,
  entry: CentralDirEntry,
): Promise<Uint8Array> {
  const p = entry.localHeaderOffset
  const sig = readUInt32LE(view, p)
  if (sig !== LFH_SIG) {
    throw new Error(`zip-reader: unexpected local file header signature 0x${sig.toString(16)}`)
  }
  const nameLen = readUInt16LE(view, p + 26)
  const extraLen = readUInt16LE(view, p + 28)
  const dataOffset = p + 30 + nameLen + extraLen
  const compressed = new Uint8Array(
    view.buffer,
    dataOffset,
    entry.compressedSize,
  )

  if (entry.compressionMethod === COMPRESSION_STORED) {
    return compressed
  }
  if (entry.compressionMethod === COMPRESSION_DEFLATE) {
    return inflateRaw(compressed)
  }
  throw new Error(`zip-reader: unsupported compression method ${entry.compressionMethod}`)
}

/**
 * Extract the named entry from a zip archive as a UTF-8 decoded string.
 * Throws if the entry is missing or the archive is malformed.
 */
export async function extractZipText(
  data: ArrayBuffer,
  entryName: string,
): Promise<string> {
  const view = new DataView(data)
  const eocdOffset = findEocdOffset(view)
  const entries = readCentralDirectory(view, eocdOffset)
  const entry = entries.find(e => e.name === entryName)
  if (!entry) {
    throw new Error(`zip-reader: entry ${entryName} not found`)
  }
  const bytes = await readLocalEntry(view, entry)
  return new TextDecoder().decode(bytes)
}
