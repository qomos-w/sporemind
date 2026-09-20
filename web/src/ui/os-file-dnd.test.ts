import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  attachOsFileDrop,
  collectOsDroppedEntries,
  startOsFileDragOut,
  bytesToBase64,
  joinOsPath,
  baseNameOf,
  type OsDragOutRequest,
} from './os-file-dnd'

/* -------------------------------------------------------------------------- */
/* Module mocks                                                               */
/* -------------------------------------------------------------------------- */

const mocks = vi.hoisted(() => ({
  isWails: vi.fn<() => boolean>(),
  projectInfo: vi.fn(),
  sshArchiveExport: vi.fn(),
  startFileDragOut: vi.fn(),
}))

vi.mock('../application/runtime', () => ({ isWails: mocks.isWails }))
vi.mock('../application/generated-client', () => ({ client: {} }))
vi.mock('../gen-clients/project/client', () => ({ info: mocks.projectInfo }))
vi.mock('../gen-clients/sshmanager/client', () => ({
  archiveExport: mocks.sshArchiveExport,
}))
vi.mock('../application/wails-runtime', () => ({
  StartFileDragOut: mocks.startFileDragOut,
}))

/* -------------------------------------------------------------------------- */
/* Fake DOM drag plumbing                                                     */
/* -------------------------------------------------------------------------- */

/** File stand-in — avoids depending on happy-dom's Blob internals. */
function fakeFile(name: string, bytes: Uint8Array): File {
  const buffer = bytes.slice().buffer
  return {
    name,
    size: bytes.length,
    arrayBuffer: async () => buffer,
  } as unknown as File
}

function fakeFileEntry(name: string, bytes: Uint8Array): FileSystemEntry {
  const file = fakeFile(name, bytes)
  return {
    name,
    isFile: true,
    isDirectory: false,
    file: (cb: (f: File) => void) => cb(file),
  } as unknown as FileSystemEntry
}

function fakeBrokenFileEntry(name: string, err: Error): FileSystemEntry {
  return {
    name,
    isFile: true,
    isDirectory: false,
    file: (_cb: (f: File) => void, eb: (e: Error) => void) => eb(err),
  } as unknown as FileSystemEntry
}

/**
 * Directory entry whose reader hands out children in fixed batches — the
 * webkit readEntries contract allows at most 100 per call and only an empty
 * batch signals the end, so expansions must loop.
 */
function fakeDirEntry(name: string, batches: FileSystemEntry[][]): FileSystemEntry {
  const queue = batches.slice()
  return {
    name,
    isFile: false,
    isDirectory: true,
    createReader: () => ({
      readEntries: (cb: (entries: FileSystemEntry[]) => void) => {
        const next = queue.shift() ?? []
        cb(next)
      },
    }),
  } as unknown as FileSystemEntry
}

interface FakeItem {
  entry?: FileSystemEntry | null
  file?: File | null
  kind?: string
}

/** DataTransfer whose items expose (or omit) webkit entries. */
function fakeDataTransfer(items: FakeItem[]): DataTransfer {
  const list = items.map((it) => ({
    kind: it.kind ?? 'file',
    webkitGetAsEntry: () => it.entry ?? null,
    getAsFile: () => it.file ?? null,
  }))
  return { items: list as unknown as DataTransferItemList } as DataTransfer
}

function dispatch(el: HTMLElement, type: 'dragover' | 'drop', dt?: DataTransfer): Event {
  const ev = new Event(type, { cancelable: true })
  if (dt !== undefined) {
    Object.defineProperty(ev, 'dataTransfer', { value: dt })
  }
  el.dispatchEvent(ev)
  return ev
}

/* -------------------------------------------------------------------------- */
/* Utilities                                                                  */
/* -------------------------------------------------------------------------- */

describe('bytesToBase64', () => {
  it('encodes small buffers', () => {
    expect(bytesToBase64(new Uint8Array([104, 105]))).toBe('aGk=')
    expect(bytesToBase64(new Uint8Array([]))).toBe('')
  })

  it('encodes buffers larger than one chunk without stack overflow', () => {
    const big = new Uint8Array(0x8000 * 2 + 7).fill(65) // 'A' * 32775
    expect(bytesToBase64(big)).toBe(btoa('A'.repeat(big.length)))
  })
})

describe('joinOsPath', () => {
  it('joins windows roots with backslashes', () => {
    expect(joinOsPath('D:\\proj', 'src/a.ts')).toBe('D:\\proj\\src\\a.ts')
    expect(joinOsPath('D:\\proj\\', 'a.txt')).toBe('D:\\proj\\a.txt')
  })

  it('joins posix roots with forward slashes', () => {
    expect(joinOsPath('/home/u/proj', 'src/a.ts')).toBe('/home/u/proj/src/a.ts')
  })

  it('normalizes redundant rel segments', () => {
    expect(joinOsPath('/r', './a//b/')).toBe('/r/a/b')
    expect(joinOsPath('/r', '.')).toBe('/r')
    expect(joinOsPath('/r', '')).toBe('/r')
  })
})

describe('baseNameOf', () => {
  it('returns the last segment', () => {
    expect(baseNameOf('/home/u/notes')).toBe('notes')
    expect(baseNameOf('notes')).toBe('notes')
  })
  it('falls back to the whole path when no segments exist', () => {
    expect(baseNameOf('/')).toBe('/')
    expect(baseNameOf('')).toBe('')
  })
})

/* -------------------------------------------------------------------------- */
/* OS → app: drop expansion                                                   */
/* -------------------------------------------------------------------------- */

const enc = new TextEncoder()

function sampleTree() {
  const aTxt = fakeFileEntry('a.txt', enc.encode('hello'))
  const bMd = fakeFileEntry('b.md', enc.encode('# doc'))
  const cPng = fakeFileEntry('c.png', new Uint8Array([0x89, 0x50, 0x4e, 0x47]))
  const img = fakeDirEntry('img', [[cPng]])
  const docs = fakeDirEntry('docs', [[bMd, img], []]) // two batches: [b.md, img], then empty
  return { aTxt, docs, bMd, cPng }
}

describe('collectOsDroppedEntries', () => {
  it('flattens nested directory trees depth-first, dirs before children', async () => {
    const { aTxt, docs } = sampleTree()
    const entries = await collectOsDroppedEntries(fakeDataTransfer([{ entry: docs }, { entry: aTxt }]))
    expect(entries.map((e) => [e.relPath, e.isDir, e.size])).toEqual([
      ['docs', true, 0],
      ['docs/b.md', false, 5],
      ['docs/img', true, 0],
      ['docs/img/c.png', false, 4],
      ['a.txt', false, 5],
    ])
  })

  it('readBytes returns file bytes and rejects for directories', async () => {
    const { aTxt, docs } = sampleTree()
    const entries = await collectOsDroppedEntries(fakeDataTransfer([{ entry: docs }, { entry: aTxt }]))
    const file = entries.find((e) => e.relPath === 'a.txt')!
    expect(new TextDecoder().decode(await file.readBytes())).toBe('hello')
    const dir = entries.find((e) => e.relPath === 'docs')!
    await expect(dir.readBytes()).rejects.toThrow('is a directory')
  })

  it('falls back to bare File items when webkitGetAsEntry is unavailable', async () => {
    const entries = await collectOsDroppedEntries(
      fakeDataTransfer([{ entry: null, file: fakeFile('plain.bin', enc.encode('BIN')) }]),
    )
    expect(entries).toHaveLength(1)
    expect(entries[0]!.relPath).toBe('plain.bin')
    expect(entries[0]!.isDir).toBe(false)
    expect(new TextDecoder().decode(await entries[0]!.readBytes())).toBe('BIN')
  })

  it('skips non-file items', async () => {
    const entries = await collectOsDroppedEntries(
      fakeDataTransfer([{ kind: 'string', entry: null, file: null }]),
    )
    expect(entries).toHaveLength(0)
  })

  it('throws when a subtree fails to expand', async () => {
    const bad = fakeBrokenFileEntry('bad.txt', new Error('read failed'))
    const good = fakeFileEntry('ok.txt', enc.encode('ok'))
    await expect(
      collectOsDroppedEntries(fakeDataTransfer([{ entry: bad }, { entry: good }])),
    ).rejects.toThrow('read failed')
  })

  it('handles null dataTransfer', async () => {
    await expect(collectOsDroppedEntries(null)).resolves.toEqual([])
  })
})

describe('attachOsFileDrop', () => {
  it('prevents default on dragover (no navigation)', () => {
    const el = document.createElement('div')
    attachOsFileDrop(el, { onDrop: () => {} })
    const ev = dispatch(el, 'dragover')
    expect(ev.defaultPrevented).toBe(true)
  })

  it('delivers the flattened tree to onDrop and prevents drop default', async () => {
    const el = document.createElement('div')
    const onDrop = vi.fn()
    const detach = attachOsFileDrop(el, { onDrop })
    const { aTxt } = sampleTree()
    const ev = dispatch(el, 'drop', fakeDataTransfer([{ entry: aTxt }]))
    expect(ev.defaultPrevented).toBe(true)
    await vi.waitFor(() => expect(onDrop).toHaveBeenCalled())
    const entries = onDrop.mock.calls[0]![0] as Array<{ relPath: string }>
    expect(entries.map((e) => e.relPath)).toEqual(['a.txt'])
    detach()
  })

  it('stop listening after detach', async () => {
    const el = document.createElement('div')
    const onDrop = vi.fn()
    const detach = attachOsFileDrop(el, { onDrop })
    detach()
    const { aTxt } = sampleTree()
    dispatch(el, 'drop', fakeDataTransfer([{ entry: aTxt }]))
    await new Promise((r) => setTimeout(r, 0))
    expect(onDrop).not.toHaveBeenCalled()
    // dragover no longer prevented either
    expect(dispatch(el, 'dragover').defaultPrevented).toBe(false)
  })

  it('routes expansion errors to onError', async () => {
    const el = document.createElement('div')
    const onError = vi.fn()
    attachOsFileDrop(el, { onDrop: () => {}, onError })
    const bad = fakeBrokenFileEntry('bad.txt', new Error('boom'))
    dispatch(el, 'drop', fakeDataTransfer([{ entry: bad }]))
    await vi.waitFor(() => expect(onError).toHaveBeenCalled())
    expect(String(onError.mock.calls[0]![0])).toContain('boom')
  })
})

/* -------------------------------------------------------------------------- */
/* app → OS: drag-out                                                         */
/* -------------------------------------------------------------------------- */

const desktopDeps = (invoke: (req: OsDragOutRequest) => Promise<unknown>) => ({
  isDesktop: () => true,
  invokeDragOut: invoke,
})

describe('startOsFileDragOut', () => {
  beforeEach(() => {
    mocks.isWails.mockReset().mockReturnValue(false)
    mocks.projectInfo.mockReset()
    mocks.sshArchiveExport.mockReset()
    mocks.startFileDragOut.mockReset()
  })

  it('returns not-desktop when isWails is unavailable (default deps)', async () => {
    const res = await startOsFileDragOut([{ kind: 'local', projectId: 'p1', relPath: 'a.txt' }])
    expect(res).toEqual({ ok: false, reason: 'not-desktop' })
    expect(mocks.projectInfo).not.toHaveBeenCalled()
  })

  it('resolves local items to absolute paths via project.info (first root)', async () => {
    mocks.projectInfo.mockResolvedValue({ Roots: [{ Name: 'default', Path: 'D:\\proj' }] })
    const invoke = vi.fn().mockResolvedValue(undefined)
    const res = await startOsFileDragOut(
      [
        { kind: 'local', projectId: 'p1', relPath: 'src/a.ts' },
        { kind: 'local', projectId: 'p1', relPath: 'README.md' },
      ],
      desktopDeps(invoke),
    )
    expect(res).toEqual({ ok: true })
    expect(mocks.projectInfo).toHaveBeenCalledWith(expect.anything(), { target: 'p1' })
    expect(invoke).toHaveBeenCalledWith({
      localPaths: ['D:\\proj\\src\\a.ts', 'D:\\proj\\README.md'],
      archiveTarGzBase64: '',
      archiveRootName: '',
    })
  })

  it('exports a single remote item as tar.gz base64 with root name', async () => {
    mocks.sshArchiveExport.mockResolvedValue({ Content: 'QkFTRTY0', Size: 5, NumEntries: 1 })
    const invoke = vi.fn().mockResolvedValue(undefined)
    const res = await startOsFileDragOut(
      [{ kind: 'remote', sessionId: 's1', remotePath: '/home/u/notes' }],
      desktopDeps(invoke),
    )
    expect(res).toEqual({ ok: true })
    expect(mocks.sshArchiveExport).toHaveBeenCalledWith(expect.anything(), {
      SessionId: 's1',
      Path: '/home/u/notes',
    })
    expect(invoke).toHaveBeenCalledWith({
      localPaths: [],
      archiveTarGzBase64: 'QkFTRTY0',
      archiveRootName: 'notes',
    })
  })

  it('supports mixing any number of local items with one remote item', async () => {
    mocks.projectInfo.mockResolvedValue({ Roots: [{ Name: 'default', Path: '/w/p' }] })
    mocks.sshArchiveExport.mockResolvedValue({ Content: 'QQ==', Size: 1, NumEntries: 1 })
    const invoke = vi.fn().mockResolvedValue(undefined)
    const res = await startOsFileDragOut(
      [
        { kind: 'local', projectId: 'p1', relPath: 'x.txt' },
        { kind: 'remote', sessionId: 's1', remotePath: '/home/u/dir' },
      ],
      desktopDeps(invoke),
    )
    expect(res).toEqual({ ok: true })
    expect(invoke).toHaveBeenCalledWith({
      localPaths: ['/w/p/x.txt'],
      archiveTarGzBase64: 'QQ==',
      archiveRootName: 'dir',
    })
  })

  it('rejects more than one remote item (single archive contract)', async () => {
    mocks.sshArchiveExport.mockResolvedValue({ Content: 'QQ==', Size: 1, NumEntries: 1 })
    const invoke = vi.fn()
    const res = await startOsFileDragOut(
      [
        { kind: 'remote', sessionId: 's1', remotePath: '/a' },
        { kind: 'remote', sessionId: 's1', remotePath: '/b' },
      ],
      desktopDeps(invoke),
    )
    expect(res.ok).toBe(false)
    expect(res.reason).toBe('unsupported')
    expect(res.error).toContain('one remote item')
    expect(invoke).not.toHaveBeenCalled()
  })

  it('rejects empty item lists', async () => {
    const invoke = vi.fn()
    const res = await startOsFileDragOut([], desktopDeps(invoke))
    expect(res).toEqual({ ok: false, reason: 'unsupported', error: 'no items to drag out' })
    expect(invoke).not.toHaveBeenCalled()
  })

  it('surfaces project.info failures without invoking the binding', async () => {
    mocks.projectInfo.mockRejectedValue(new Error('no such project'))
    const invoke = vi.fn()
    const res = await startOsFileDragOut(
      [{ kind: 'local', projectId: 'pX', relPath: 'a' }],
      desktopDeps(invoke),
    )
    expect(res).toEqual({ ok: false, reason: 'error', error: 'no such project' })
    expect(invoke).not.toHaveBeenCalled()
  })

  it('errors when project.info reports no roots', async () => {
    mocks.projectInfo.mockResolvedValue({ Roots: [] })
    const invoke = vi.fn()
    const res = await startOsFileDragOut(
      [{ kind: 'local', projectId: 'p1', relPath: 'a' }],
      desktopDeps(invoke),
    )
    expect(res.ok).toBe(false)
    expect(res.reason).toBe('error')
    expect(res.error).toContain('no roots')
    expect(invoke).not.toHaveBeenCalled()
  })

  it('surfaces desktop binding failures', async () => {
    mocks.projectInfo.mockResolvedValue({ Roots: [{ Name: 'default', Path: '/w/p' }] })
    const invoke = vi.fn().mockRejectedValue(new Error('DoDragDrop failed'))
    const res = await startOsFileDragOut(
      [{ kind: 'local', projectId: 'p1', relPath: 'a' }],
      desktopDeps(invoke),
    )
    expect(res).toEqual({ ok: false, reason: 'error', error: 'DoDragDrop failed' })
  })

  it('routes the default desktop invoke through the generated wails-runtime binding', async () => {
    mocks.projectInfo.mockResolvedValue({ Roots: [{ Name: 'default', Path: '/w/p' }] })
    mocks.startFileDragOut.mockResolvedValue(true)
    const res = await startOsFileDragOut(
      [{ kind: 'local', projectId: 'p1', relPath: 'a' }],
      { isDesktop: () => true },
    )
    expect(res).toEqual({ ok: true })
    expect(mocks.startFileDragOut).toHaveBeenCalledWith({
      localPaths: ['/w/p/a'],
      archiveTarGzBase64: '',
      archiveRootName: '',
    })
  })

  it('treats a false return from the wails-runtime wrapper as an error', async () => {
    mocks.projectInfo.mockResolvedValue({ Roots: [{ Name: 'default', Path: '/w/p' }] })
    mocks.startFileDragOut.mockResolvedValue(false)
    const res = await startOsFileDragOut(
      [{ kind: 'local', projectId: 'p1', relPath: 'a' }],
      { isDesktop: () => true },
    )
    expect(res.ok).toBe(false)
    expect(res.reason).toBe('error')
  })
})
