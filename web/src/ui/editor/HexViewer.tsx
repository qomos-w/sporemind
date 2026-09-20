import { useEffect, useRef, useState, useCallback, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import { FileCode, Loader2 } from 'lucide-react'
import * as filesystem from '../../gen-clients/filesystem/client'
import * as projectClient from '../../gen-clients/project/client'
import { client } from '../../application/generated-client'
import './HexViewer.css'

interface HexViewerProps {
  filePath: string
  /** Owning project; when set, chunks are read through the project actor so a
   *  relative path resolves against the project root, not the primary mount. */
  projectId?: string
  /** When true, renders only the scrollable hex grid (no statusbar/footer). */
  bare?: boolean
}

const ROW_BYTES = 16
const ROW_HEIGHT = 20
const OVERSCAN = 20
const CHUNK_SIZE = 256 * 1024 // 256 KB per request
const MAX_CACHE = 64 * 1024 * 1024 // 64 MB
const MAX_SCROLL = 16_000_000 // Chromium safe scroll height limit
/** Hard cap on rendered rows regardless of any measurement pathology. A
 * viewport taller than this in rows is not a real hex use case. */
const MAX_RENDER_ROWS = 400

function base64ToUint8Array(b64: string): Uint8Array {
  const bin = atob(b64)
  const arr = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i)
  return arr
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function toHex(n: number): string {
  return n.toString(16).padStart(2, '0').toUpperCase()
}

function getCachedBytes(map: Map<number, Uint8Array>): number {
  let total = 0
  for (const chunk of map.values()) total += chunk.length
  return total
}

/** Parse a hex offset ("1A2F", "0x1a2f") to a byte index, or null. */
export function parseHexOffset(input: string): number | null {
  const s = input.trim().replace(/^0x/i, '')
  if (!/^[0-9a-f]+$/i.test(s)) return null
  const v = parseInt(s, 16)
  return Number.isSafeInteger(v) ? v : null
}

function HexRow({
  offset,
  chunkMap,
  totalSize,
  selIndex,
  onSelect,
}: {
  offset: number
  chunkMap: Map<number, Uint8Array>
  totalSize: number
  selIndex: number | null
  onSelect: (index: number) => void
}) {
  const hexCells: ReactNode[] = []
  const asciiCells: ReactNode[] = []

  for (let i = 0; i < ROW_BYTES; i++) {
    const idx = offset + i
    const selected = selIndex === idx
    if (idx >= totalSize) {
      hexCells.push(<span className="hv-b spacer" key={i} aria-hidden />)
      asciiCells.push(<span className="hv-a spacer" key={i} aria-hidden />)
      continue
    }
    const chunkOffset = Math.floor(idx / CHUNK_SIZE) * CHUNK_SIZE
    const chunk = chunkMap.get(chunkOffset)
    const b = chunk && chunk.length > 0 ? chunk[idx - chunkOffset] : undefined
    if (b === undefined) {
      hexCells.push(<span className="hv-b miss" key={i}>..</span>)
      asciiCells.push(<span className="hv-a miss" key={i}>.</span>)
      continue
    }
    hexCells.push(
      <span className={`hv-b${selected ? ' sel' : ''}`} key={i} onClick={() => onSelect(idx)}>
        {toHex(b)}
      </span>,
    )
    asciiCells.push(
      <span className={`hv-a${selected ? ' sel' : ''}`} key={i} onClick={() => onSelect(idx)}>
        {b >= 0x20 && b <= 0x7e ? String.fromCharCode(b) : '.'}
      </span>,
    )
  }

  return (
    <div className="hv-row">
      <span className="hv-offset">{offset.toString(16).padStart(8, '0').toUpperCase()}</span>
      <span className="hv-hex">{hexCells.slice(0, 8)}<span className="hv-gap" />{hexCells.slice(8)}</span>
      <span className="hv-ascii">{asciiCells}</span>
    </div>
  )
}

export function HexViewer({ filePath, projectId, bare = false }: HexViewerProps) {
  const chunkMapRef = useRef(new Map<number, Uint8Array>())
  const [totalSize, setTotalSize] = useState(0)
  const [, forceUpdate] = useState(0)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [cachedBytes, setCachedBytes] = useState(0)
  const scrollRef = useRef<HTMLDivElement>(null)
  const [scrollTop, setScrollTop] = useState(0) // logical px
  const scrollTopRef = useRef(0)
  const totalSizeRef = useRef(0)
  const filePathRef = useRef(filePath)
  filePathRef.current = filePath
  const projectIdRef = useRef(projectId)
  projectIdRef.current = projectId
  const [viewportH, setViewportH] = useState(0)
  const viewportHRef = useRef(0)
  const [selIndex, setSelIndex] = useState<number | null>(null)
  const [gotoValue, setGotoValue] = useState('')
  const [gotoError, setGotoError] = useState(false)

  scrollTopRef.current = scrollTop

  const evictIfNeeded = useCallback(() => {
    const map = chunkMapRef.current
    let total = getCachedBytes(map)
    if (total <= MAX_CACHE) return

    const scrolledPx = scrollTopRef.current
    const viewportPx = viewportHRef.current || 600
    const firstRow = Math.floor(scrolledPx / ROW_HEIGHT)
    const visibleStartByte = firstRow * ROW_BYTES
    const visibleEndByte = (firstRow + Math.ceil(viewportPx / ROW_HEIGHT) + 1) * ROW_BYTES

    const entries = Array.from(map.entries())
      .filter(([, chunk]) => chunk.length > 0)
      .map(([offset, chunk]) => ({
        offset,
        chunk,
        distance: Math.max(visibleStartByte - offset, offset - visibleEndByte, 0),
      }))
    entries.sort((a, b) => b.distance - a.distance)

    let removed = 0
    for (const { offset, chunk } of entries) {
      if (total - removed <= MAX_CACHE * 0.75) break
      removed += chunk.length
      map.delete(offset)
    }

    setCachedBytes(getCachedBytes(map))
  }, [viewportH])

  const fetchChunk = useCallback(
    async (chunkOffset: number) => {
      const aligned = Math.floor(chunkOffset / CHUNK_SIZE) * CHUNK_SIZE
      const map = chunkMapRef.current
      if (map.has(aligned)) return

      const currentPath = filePathRef.current
      map.set(aligned, new Uint8Array(0))

      try {
        const pid = projectIdRef.current
        const resp = pid
          ? await projectClient.readChunk(client, {
              Path: currentPath,
              Offset: aligned,
              Length: CHUNK_SIZE,
            }, { target: pid })
          : await filesystem.readChunk(client, {
              Path: currentPath,
              Offset: aligned,
              Length: CHUNK_SIZE,
            })
        if (filePathRef.current !== currentPath) return
        const chunk = base64ToUint8Array(resp.Data)
        map.set(aligned, chunk)
        if (!totalSizeRef.current) {
          totalSizeRef.current = resp.Total
          setTotalSize(resp.Total)
        }
        evictIfNeeded()
        forceUpdate((k) => k + 1)
      } catch (err) {
        map.delete(aligned)
        setError(String((err as { message?: string })?.message ?? err))
      }
    },
    [evictIfNeeded],
  )

  // Initial load: fetch first chunk
  useEffect(() => {
    chunkMapRef.current.clear()
    totalSizeRef.current = 0
    setTotalSize(0)
    setCachedBytes(0)
    setError('')
    setLoading(true)
    setScrollTop(0)
    scrollTopRef.current = 0
    setSelIndex(null)
    forceUpdate(0)

    fetchChunk(0).finally(() => setLoading(false))
  }, [filePath, fetchChunk])

  // Track the scroller's real height. Never trust clientHeight during render:
  // a broken CSS chain once let the container grow to content height (16M px)
  // and one render painted 800k rows, freezing the whole webview. The rendered
  // row count is hard-capped in the render math regardless of what we measure.
  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const update = () => {
      const h = el.clientHeight
      viewportHRef.current = h
      setViewportH(h)
    }
    update()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // Visible range → determine which chunks are needed
  const totalRows = totalSize > 0 ? Math.ceil(totalSize / ROW_BYTES) : 0
  const contentHeight = totalRows * ROW_HEIGHT
  const needsScrollMapping = contentHeight > MAX_SCROLL
  const scrollRatio = needsScrollMapping ? MAX_SCROLL / contentHeight : 1
  const scrollRatioRef = useRef(1)
  scrollRatioRef.current = scrollRatio
  const scrollHeight = needsScrollMapping ? MAX_SCROLL : contentHeight

  const viewportRows = Math.max(1, Math.ceil((viewportH || 0) / ROW_HEIGHT))
  const firstVisibleRow = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN)
  const lastVisibleRow = Math.min(
    totalRows,
    Math.min(firstVisibleRow + MAX_RENDER_ROWS, firstVisibleRow + viewportRows + OVERSCAN * 2 + 1),
  )

  const firstByte = firstVisibleRow * ROW_BYTES
  const lastByte = lastVisibleRow * ROW_BYTES

  // Trigger chunk fetches for visible range
  useEffect(() => {
    if (!totalSize) return
    const firstAligned = Math.floor(firstByte / CHUNK_SIZE) * CHUNK_SIZE
    for (let off = firstAligned; off < lastByte; off += CHUNK_SIZE) {
      void fetchChunk(off)
    }
  }, [firstByte, lastByte, totalSize, fetchChunk])

  // Scroll sampling: synchronous, but a re-render only happens when the
  // visible row actually changes (row-boundary dedupe). The row layer is
  // absolutely positioned and moved via transform only, and overflow-anchor
  // is disabled in CSS, so Chromium's scroll anchoring cannot feed back into
  // the row layer's layout and start a scroll loop.
  const lastRowRef = useRef(0)
  const onScroll = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    const h = el.clientHeight
    if (h !== viewportHRef.current) {
      viewportHRef.current = h
      setViewportH(h)
    }
    const raw = el.scrollTop
    const logical = scrollRatioRef.current < 1 ? raw / scrollRatioRef.current : raw
    const row = Math.floor(logical / ROW_HEIGHT)
    if (row === lastRowRef.current) return
    lastRowRef.current = row
    setScrollTop(logical)
  }, [])

  /** Scroll so the byte at `index` is visible (used by goto + keyboard nav). */
  const scrollToByte = useCallback((index: number) => {
    const el = scrollRef.current
    if (!el || !totalSizeRef.current) return
    const row = Math.floor(index / ROW_BYTES)
    const logical = row * ROW_HEIGHT
    const physical = scrollRatioRef.current < 1 ? logical * scrollRatioRef.current : logical
    const vh = el.clientHeight
    if (physical < el.scrollTop) el.scrollTop = physical
    else if (physical + ROW_HEIGHT > el.scrollTop + vh) el.scrollTop = physical + ROW_HEIGHT - vh
  }, [])

  const selectByte = useCallback(
    (index: number) => {
      setSelIndex(index)
      scrollToByte(index)
    },
    [scrollToByte],
  )

  const handleKeyDown = useCallback(
    (e: ReactKeyboardEvent) => {
      if (selIndex == null) return
      const max = totalSizeRef.current - 1
      let next: number | null = null
      switch (e.key) {
        case 'ArrowLeft': next = Math.max(0, selIndex - 1); break
        case 'ArrowRight': next = Math.min(max, selIndex + 1); break
        case 'ArrowUp': next = Math.max(0, selIndex - ROW_BYTES); break
        case 'ArrowDown': next = Math.min(max, selIndex + ROW_BYTES); break
        case 'Home': next = 0; break
        case 'End': next = Math.max(0, max); break
        case 'PageUp': next = Math.max(0, selIndex - ROW_BYTES * 20); break
        case 'PageDown': next = Math.min(max, selIndex + ROW_BYTES * 20); break
        default: return
      }
      e.preventDefault()
      if (next != null) {
        setSelIndex(next)
        scrollToByte(next)
      }
    },
    [selIndex, scrollToByte],
  )

  const handleGoto = useCallback(() => {
    const off = parseHexOffset(gotoValue)
    if (off == null || off < 0 || (totalSize > 0 && off >= totalSize)) {
      setGotoError(true)
      return
    }
    setGotoError(false)
    setSelIndex(off)
    scrollToByte(off)
    // Ensure the row lands in the fetch window too.
    const aligned = Math.floor(off / CHUNK_SIZE) * CHUNK_SIZE
    void fetchChunk(aligned)
  }, [gotoValue, totalSize, scrollToByte, fetchChunk])

  const fileName = filePath.replace(/\\/g, '/').split('/').pop() ?? filePath

  // In mapped mode the visible rows are positioned with raw pixels
  // (logicalTop * scrollRatio <= MAX_SCROLL) via transform so the inner
  // container stays clamped under the viewport window instead of restoring
  // the unbounded logical scroll range.
  const rawTop = firstVisibleRow * ROW_HEIGHT
  const rowContainerTop = needsScrollMapping ? rawTop * scrollRatio : rawTop

  // Byte inspector values at the current selection.
  let inspect: string | null = null
  if (selIndex != null && selIndex < totalSize) {
    const vals: number[] = []
    for (let i = 0; i < 4; i++) {
      const idx = selIndex + i
      if (idx >= totalSize) break
      const chunk = chunkMapRef.current.get(Math.floor(idx / CHUNK_SIZE) * CHUNK_SIZE)
      const b = chunk && chunk.length > 0 ? chunk[idx % CHUNK_SIZE] : undefined
      if (b === undefined) break
      vals.push(b)
    }
    if (vals.length > 0) {
      const parts = [`0x${selIndex.toString(16).toUpperCase().padStart(8, '0')}`, `hex ${vals.map(toHex).join(' ')}`, `u8 ${vals[0]}`]
      if (vals.length >= 2) parts.push(`u16le ${vals[0]! | (vals[1]! << 8)}`)
      if (vals.length >= 4) parts.push(`u32le ${((vals[0]! | (vals[1]! << 8) | (vals[2]! << 16) | (vals[3]! << 24)) >>> 0)}`)
      inspect = parts.join('  ·  ')
    } else {
      inspect = `0x${selIndex.toString(16).toUpperCase().padStart(8, '0')}`
    }
  }

  const header = (
    <div className="hv-header">
      <span className="hv-offset">Offset</span>
      <span className="hv-hex">
        {Array.from({ length: 8 }, (_, i) => <span className="hv-b" key={i}>{toHex(i)}</span>)}
        <span className="hv-gap" />
        {Array.from({ length: 8 }, (_, i) => <span className="hv-b" key={i + 8}>{toHex(i + 8)}</span>)}
      </span>
      <span className="hv-ascii">ASCII</span>
    </div>
  )

  const grid = (
    <div className={`hv-scroll-outer${bare ? ' hv-bare' : ''}`}>
      {header}
      <div
        className="hv-content"
        ref={scrollRef}
        onScroll={onScroll}
        onKeyDown={handleKeyDown}
        tabIndex={0}
      >
        {error ? (
          <div className="hv-error">{error}</div>
        ) : loading && totalSize === 0 ? (
          <div className="hv-loading">
            <Loader2 size={24} className="hv-spin" />
            <span>Loading binary...</span>
          </div>
        ) : totalSize > 0 ? (
          <>
            <div className="hv-spacer" style={{ height: scrollHeight }} />
            <div
              className="hv-rows"
              style={{ transform: `translate3d(0, ${rowContainerTop}px, 0)` }}
            >
              {Array.from({ length: lastVisibleRow - firstVisibleRow }, (_, i) => {
                const row = firstVisibleRow + i
                return (
                  <HexRow
                    key={row}
                    offset={row * ROW_BYTES}
                    chunkMap={chunkMapRef.current}
                    totalSize={totalSize}
                    selIndex={selIndex}
                    onSelect={selectByte}
                  />
                )
              })}
            </div>
          </>
        ) : null}
      </div>
    </div>
  )

  if (bare) return grid

  return (
    <div className="hv-container">
      <div className="hv-statusbar">
        <FileCode size={14} />
        <span className="hv-filename">{fileName}</span>
        <span className="hv-type">HEX</span>
        {totalSize > 0 && (
          <span className="hv-progress">cached {formatSize(cachedBytes)} / {formatSize(totalSize)}</span>
        )}
        <span className="hv-goto">
          <input
            className={`hv-goto-input${gotoError ? ' err' : ''}`}
            value={gotoValue}
            placeholder="Go to offset (hex)"
            spellCheck={false}
            onChange={(e) => { setGotoValue(e.target.value); setGotoError(false) }}
            onKeyDown={(e) => { if (e.key === 'Enter') handleGoto() }}
          />
        </span>
      </div>
      {grid}
      <div className="hv-footer">
        <span>{inspect ?? (totalSize > 0 ? `${formatSize(totalSize)} · click a byte to inspect` : '')}</span>
        <span>{filePath}</span>
      </div>
    </div>
  )
}
