import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { HexViewer } from './HexViewer'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../application/generated-client', () => ({ client: {} }))

const readChunkMock = vi.fn()

vi.mock('../../gen-clients/filesystem/client', () => ({
  readChunk: (...args: unknown[]) => readChunkMock(...args),
}))

const ROW_BYTES = 16
const ROW_HEIGHT = 20
const CHUNK_SIZE = 256 * 1024
const MAX_SCROLL = 16_000_000

function toBase64(data: Uint8Array): string {
  // Native path keeps the large eviction-test payload encoding fast.
  if (typeof Buffer !== 'undefined') {
    return Buffer.from(data.buffer, data.byteOffset, data.byteLength).toString('base64')
  }
  let binary = ''
  for (let i = 0; i < data.length; i++) binary += String.fromCharCode(data[i]!)
  return btoa(binary)
}

function chunkData(offset: number, length: number): Uint8Array {
  const arr = new Uint8Array(length)
  for (let i = 0; i < length; i++) arr[i] = (offset + i) & 0xff
  return arr
}

function readChunkResponse(offset: number, length: number, total: number, data?: string) {
  return Promise.resolve({
    Offset: offset,
    Length: length,
    Total: total,
    Data: data ?? toBase64(chunkData(offset, length)),
  })
}

const mountedRoots: { container: HTMLElement; root: ReturnType<typeof createRoot> }[] = []

function renderHexViewer(props: Partial<Parameters<typeof HexViewer>[0]> = {}) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  mountedRoots.push({ container, root })
  act(() => {
    root.render(<HexViewer filePath="/test.bin" {...props} />)
  })
  return { container, root }
}

async function flush() {
  await act(async () => {
    await new Promise(r => setTimeout(r, 0))
  })
}

beforeEach(() => {
  readChunkMock.mockReset()
  // happy-dom's atob is a slow JS decoder; for the multi-megabyte chunk
  // payloads used by the eviction test, route through Node's native base64.
  vi.stubGlobal('atob', (b64: string) => Buffer.from(b64, 'base64').toString('binary'))
})

afterEach(() => {
  vi.unstubAllGlobals()
  for (const { container, root } of mountedRoots.splice(0)) {
    act(() => root.unmount())
    container.remove()
  }
})

function getContent(container: HTMLElement) {
  const content = container.querySelector('.hv-content') as HTMLElement
  expect(content).toBeTruthy()
  return content
}

/** Scroll to a raw position (pixels) and let the component react. */
function scrollToRaw(content: HTMLElement, rawPx: number) {
  act(() => {
    content.scrollTop = rawPx
    content.dispatchEvent(new Event('scroll', { bubbles: true }))
  })
}

describe('HexViewer', () => {
  it('requests only the chunks covering the visible range on first render', async () => {
    const total = CHUNK_SIZE * 2
    readChunkMock.mockImplementation((_client: unknown, req: { Offset: number }) =>
      readChunkResponse(req.Offset, CHUNK_SIZE, total),
    )

    const { container } = renderHexViewer()
    await flush()

    expect(readChunkMock).toHaveBeenCalledTimes(1)
    expect(readChunkMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Path: '/test.bin', Offset: 0, Length: CHUNK_SIZE }),
    )

    // Scroll into the second chunk (byte offset CHUNK_SIZE). Small file so no
    // scroll mapping is active: raw position equals logical position.
    const content = getContent(container)
    scrollToRaw(content, (CHUNK_SIZE / ROW_BYTES) * ROW_HEIGHT)
    await flush()

    expect(readChunkMock).toHaveBeenCalledTimes(2)
    expect(readChunkMock).toHaveBeenLastCalledWith(
      expect.anything(),
      expect.objectContaining({ Path: '/test.bin', Offset: CHUNK_SIZE, Length: CHUNK_SIZE }),
    )
  })

  it('does not re-request a cached chunk when scrolling back to it', async () => {
    const total = CHUNK_SIZE * 4
    readChunkMock.mockImplementation((_client: unknown, req: { Offset: number }) =>
      readChunkResponse(req.Offset, CHUNK_SIZE, total),
    )

    const { container } = renderHexViewer()
    await flush()
    expect(readChunkMock).toHaveBeenCalledTimes(1)

    const content = getContent(container)
    scrollToRaw(content, (CHUNK_SIZE / ROW_BYTES) * ROW_HEIGHT)
    await flush()
    expect(readChunkMock).toHaveBeenCalledTimes(2)

    scrollToRaw(content, 0)
    await flush()
    expect(readChunkMock).toHaveBeenCalledTimes(2)
  })

  it(
    're-requests a chunk after it has been evicted by the cache limit',
    async () => {
      // Each mock chunk is 24MB (well above CHUNK_SIZE) so the 64MB cache limit
      // is exceeded after the third fetch. The base64 payload is generated once
      // and reused so the test does not spend seconds re-encoding each chunk.
      const huge = 24 * 1024 * 1024
      const total = huge * 3
      const hugeB64 = toBase64(new Uint8Array(huge))
      readChunkMock.mockImplementation((_client: unknown, req: { Offset: number }) =>
        readChunkResponse(req.Offset, huge, total, hugeB64),
      )

      const { container } = renderHexViewer()
      await flush()
      expect(readChunkMock).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ Offset: 0 }))

      const content = getContent(container)

      // happy-dom does not clamp scrollTop to the scroll range; convert
      // logical byte positions to raw mapped pixels like a real browser would.
      const contentHeight = (total / ROW_BYTES) * ROW_HEIGHT
      const ratio = contentHeight > MAX_SCROLL ? MAX_SCROLL / contentHeight : 1
      const scrollLogical = (byteOffset: number) =>
        scrollToRaw(content, Math.floor(byteOffset / ROW_BYTES) * ROW_HEIGHT * ratio)

      // Fetch chunk 1, then chunk 2: 3 chunks x 24MB = 72MB > 64MB limit, so
      // the farthest chunk (0) is evicted and scrolling back re-requests it.
      scrollLogical(huge * 1.5)
      await flush()
      scrollLogical(huge * 2.5)
      await flush()
      scrollLogical(0)
      await flush()

      const calls = readChunkMock.mock.calls as [unknown, { Offset: number }][]
      const chunk0Calls = calls.filter(([, req]) => req.Offset === 0)
      expect(chunk0Calls.length).toBeGreaterThanOrEqual(2)
    },
    30000,
  )

  it('keeps the virtual scroll height within the browser limit for multi-gigabyte files', async () => {
    const total = 4 * 1024 * 1024 * 1024 // 4 GB
    readChunkMock.mockImplementation((_client: unknown, req: { Offset: number }) =>
      readChunkResponse(req.Offset, CHUNK_SIZE, total),
    )

    const { container } = renderHexViewer()
    await flush()

    const spacer = container.querySelector('.hv-spacer') as HTMLElement
    expect(spacer).toBeTruthy()
    expect(spacer.style.height).toBe(`${MAX_SCROLL}px`)
  })

  it('positions the row container with scaled raw pixels in mapped mode', async () => {
    const total = 4 * 1024 * 1024 * 1024 // 4 GB
    readChunkMock.mockImplementation((_client: unknown, req: { Offset: number }) =>
      readChunkResponse(req.Offset, CHUNK_SIZE, total),
    )

    const { container } = renderHexViewer()
    await flush()

    const content = getContent(container)
    const spacer = container.querySelector('.hv-spacer') as HTMLElement
    expect(spacer).toBeTruthy()

    const contentHeight = (total / ROW_BYTES) * ROW_HEIGHT
    const ratio = MAX_SCROLL / contentHeight

    // Scroll to the middle of the clamped raw scroll range.
    const rawScroll = MAX_SCROLL / 2
    scrollToRaw(content, rawScroll)
    await flush()

    const rowContainer = container.querySelector('.hv-rows') as HTMLElement
    expect(rowContainer).toBeTruthy()
    const m = /translate3d\(0, ([\d.]+)px, 0\)/.exec(rowContainer.style.transform ?? '')
    expect(m).toBeTruthy()
    const translateY = parseFloat(m![1]!)

    // The logical position the raw scroll maps to, with the same overscan
    // the component applies when picking the first visible row.
    const logicalScrollTop = rawScroll / ratio
    const firstVisibleRow = Math.max(0, Math.floor(logicalScrollTop / ROW_HEIGHT) - 20)
    const unscaledTop = firstVisibleRow * ROW_HEIGHT

    // The old buggy value (unscaled logical pixels) would overflow MAX_SCROLL
    // and defeat the clamp; the fixed value stays at raw-pixel level.
    expect(unscaledTop).toBeGreaterThan(MAX_SCROLL)
    expect(translateY).toBeLessThanOrEqual(MAX_SCROLL)
    expect(translateY).toBeLessThan(unscaledTop)
    expect(Math.round(translateY)).toBe(Math.round(unscaledTop * ratio))
  })

  it('does not allocate a Uint8Array of the full file size', async () => {
    const total = 4 * 1024 * 1024 * 1024 // 4 GB
    const seenLengths: number[] = []
    const OriginalUint8Array = Uint8Array
    const spy = vi.spyOn(globalThis, 'Uint8Array').mockImplementation(function (this: unknown, ...args: unknown[]) {
      if (args.length === 1 && typeof args[0] === 'number') {
        seenLengths.push(args[0])
      }
      return new (OriginalUint8Array as any)(...(args as any))
    } as any)

    readChunkMock.mockImplementation((_client: unknown, req: { Offset: number }) =>
      readChunkResponse(req.Offset, CHUNK_SIZE, total),
    )

    try {
      renderHexViewer()
      await flush()
      expect(seenLengths).not.toContain(total)
    } finally {
      spy.mockRestore()
    }
  })

  it('caps rendered rows even when the viewport height explodes from a broken CSS chain', async () => {
    // Regression for the webview freeze: the scroller once grew to content
    // height (16M px), making clientHeight 16M and one render paint 800k rows.
    const total = 4 * 1024 * 1024 * 1024 // 4 GB
    readChunkMock.mockImplementation((_client: unknown, req: { Offset: number }) =>
      readChunkResponse(req.Offset, CHUNK_SIZE, total),
    )

    const { container } = renderHexViewer()
    await flush()

    const content = getContent(container)
    Object.defineProperty(content, 'clientHeight', { value: 16_000_000, configurable: true })
    scrollToRaw(content, 1000)
    await flush()

    const rows = container.querySelectorAll('.hv-row')
    expect(rows.length).toBeGreaterThan(0)
    expect(rows.length).toBeLessThanOrEqual(400)
  })

  it('keeps the bare prop rendering only the scrollable grid', async () => {
    const total = CHUNK_SIZE
    readChunkMock.mockImplementation((_client: unknown, req: { Offset: number }) =>
      readChunkResponse(req.Offset, CHUNK_SIZE, total),
    )

    const { container } = renderHexViewer({ bare: true })
    await flush()

    expect(container.querySelector('.hv-statusbar')).toBeFalsy()
    expect(container.querySelector('.hv-footer')).toBeFalsy()
    expect(container.querySelector('.hv-content')).toBeTruthy()
  })

  it('discards stale fetchChunk responses after filePath changes', async () => {
    const totalA = CHUNK_SIZE * 4
    const totalB = CHUNK_SIZE * 2

    // File A chunk 0 resolves immediately. A second in-flight request
    // (simulating a scroll-triggered fetch) stays pending.
    let resolveStaleA!: (v: unknown) => void
    const pendingStaleA = new Promise(r => { resolveStaleA = r })

    readChunkMock.mockImplementation((_client: unknown, req: { Path: string; Offset: number }) => {
      if (req.Path === '/test.bin' && req.Offset === 0) {
        return readChunkResponse(0, CHUNK_SIZE, totalA)
      }
      if (req.Path === '/test.bin' && req.Offset === CHUNK_SIZE) {
        return pendingStaleA
      }
      // File B
      return readChunkResponse(req.Offset, CHUNK_SIZE, totalB)
    })

    const { container, root } = renderHexViewer()
    await flush()

    // Trigger a scroll fetch for file A at offset CHUNK_SIZE (stays pending).
    const content = getContent(container)
    scrollToRaw(content, (CHUNK_SIZE / ROW_BYTES) * ROW_HEIGHT)
    await flush()

    // Switch to file B — clears the map, resets totalSize, fetches file B chunk 0.
    act(() => {
      root.render(<HexViewer filePath="/file-b.bin" />)
    })
    await flush()

    // Now resolve file A's stale chunk — must NOT corrupt file B's totalSize.
    resolveStaleA({ Offset: CHUNK_SIZE, Length: CHUNK_SIZE, Total: totalA, Data: toBase64(chunkData(CHUNK_SIZE, CHUNK_SIZE)) })
    await flush()

    const spacer = container.querySelector('.hv-spacer') as HTMLElement
    expect(spacer).toBeTruthy()
    const actualHeight = parseFloat(spacer.style.height ?? '0')
    const expectedHeightB = (totalB / ROW_BYTES) * ROW_HEIGHT
    expect(actualHeight).toBe(expectedHeightB)
  })
})