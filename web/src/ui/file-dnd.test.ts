import { describe, it, expect } from 'vitest'
import {
  LOCAL_FILE_DND_MIME,
  REMOTE_FILE_DND_MIME,
  setFileDragPayload,
  hasForeignFileDrag,
  getForeignFileDragPayload,
} from './file-dnd'

/** Minimal DataTransfer stand-in that tracks types. */
function makeDataTransfer() {
  const store: Record<string, string> = {}
  return {
    setData: (k: string, v: string) => { store[k] = v },
    getData: (k: string) => store[k] ?? '',
    get types() { return Object.keys(store) },
    effectAllowed: 'none' as string,
    dropEffect: 'none' as string,
  } as unknown as DataTransfer
}

describe('file-dnd', () => {
  it('uses distinct MIME types per origin', () => {
    expect(LOCAL_FILE_DND_MIME).not.toBe(REMOTE_FILE_DND_MIME)
    expect(LOCAL_FILE_DND_MIME).toContain('local')
    expect(REMOTE_FILE_DND_MIME).toContain('remote')
  })

  it('serializes the payload under the origin-specific MIME', () => {
    const dt = makeDataTransfer()
    setFileDragPayload(dt, {
      origin: 'local', name: 'a.txt', path: 'src/a.txt', isDir: false, projectId: 'p1',
    })
    expect(dt.getData(LOCAL_FILE_DND_MIME)).toContain('"origin":"local"')
    expect(dt.getData(LOCAL_FILE_DND_MIME)).toContain('"projectId":"p1"')
    expect(dt.getData(REMOTE_FILE_DND_MIME)).toBe('')
  })

  it('detects foreign drags via types (dragover-safe)', () => {
    const localDt = makeDataTransfer()
    setFileDragPayload(localDt, { origin: 'local', name: 'x', path: 'x', isDir: false })

    // A remote panel should see a local drag as foreign
    expect(hasForeignFileDrag(localDt, 'remote')).toBe(true)
    // A local panel should not see its own drag as foreign
    expect(hasForeignFileDrag(localDt, 'local')).toBe(false)

    const remoteDt = makeDataTransfer()
    setFileDragPayload(remoteDt, { origin: 'remote', name: 'y', path: '/y', isDir: true, sessionId: 's1' })

    expect(hasForeignFileDrag(remoteDt, 'local')).toBe(true)
    expect(hasForeignFileDrag(remoteDt, 'remote')).toBe(false)
  })

  it('reads the foreign payload during drop', () => {
    const dt = makeDataTransfer()
    setFileDragPayload(dt, {
      origin: 'remote', name: 'config.yaml', path: '/etc/config.yaml', isDir: false, sessionId: 'sess-42',
    })

    const payload = getForeignFileDragPayload(dt, 'local')
    expect(payload).not.toBeNull()
    expect(payload!.origin).toBe('remote')
    expect(payload!.sessionId).toBe('sess-42')
    expect(payload!.name).toBe('config.yaml')
    expect(payload!.path).toBe('/etc/config.yaml')
  })

  it('returns null when no foreign payload is present', () => {
    const dt = makeDataTransfer()
    setFileDragPayload(dt, { origin: 'local', name: 'z', path: 'z', isDir: false })
    // Same-origin panel gets null
    expect(getForeignFileDragPayload(dt, 'local')).toBeNull()
  })

  it('returns null for empty DataTransfer', () => {
    const dt = makeDataTransfer()
    expect(getForeignFileDragPayload(dt, 'local')).toBeNull()
    expect(getForeignFileDragPayload(dt, 'remote')).toBeNull()
  })

  it('round-trips a directory payload across panels preserving isDir and identity', () => {
    // Local side: a directory is dragged (e.g. for local → remote archive transfer).
    const dt = makeDataTransfer()
    setFileDragPayload(dt, {
      origin: 'local', name: 'src', path: 'src', isDir: true, projectId: 'proj-1',
    })

    // Remote panel reads it as a foreign payload during drop.
    const payload = getForeignFileDragPayload(dt, 'remote')
    expect(payload).not.toBeNull()
    expect(payload!.isDir).toBe(true)
    expect(payload!.origin).toBe('local')
    expect(payload!.projectId).toBe('proj-1')
    expect(payload!.name).toBe('src')

    // The remote panel must not see its own drag as foreign.
    expect(hasForeignFileDrag(dt, 'local')).toBe(false)
  })
})
