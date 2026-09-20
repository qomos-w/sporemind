import React, { useEffect, useMemo, useRef, useState } from 'react'
import './HexView.css'

/** Bytes rendered per hex row (offset | hex | ASCII). */
export const BYTES_PER_ROW = 16
/** Default bytes rendered per page before pagination kicks in. */
export const DEFAULT_PAGE_SIZE = 4096

/** A single rendered hex row. */
export interface HexRow {
  /** Absolute byte offset of the first byte in the row. */
  offset: number
  /** Raw byte values for this row (1..16 entries). */
  bytes: number[]
}

/** All user-facing strings. Injected by the parent (e.g. via t()) so the
 *  component itself stays i18n-free. Every field is optional; English
 *  fallbacks are applied when a field is missing. */
export interface HexViewLabels {
  /** Offset column header (e.g. "Offset"). */
  offset?: string
  /** ASCII column header (e.g. "ASCII" / "文本"). */
  ascii?: string
  /** Paginator indicator; `{page}` / `{total}` are substituted. */
  pageOf?: string
  /** Paginator button titles (a11y). */
  prevPage?: string
  nextPage?: string
  firstPage?: string
  lastPage?: string
  /** Empty-data placeholder. */
  empty?: string
  /** Read-only hint shown in the toolbar. */
  readOnly?: string
}

export interface HexViewProps {
  /** Single source of truth (controlled). */
  bytes: Uint8Array
  /** Receives a fresh Uint8Array on every committed byte edit. */
  onChange: (next: Uint8Array) => void
  /** When true the grid is non-interactive. */
  readOnly?: boolean
  /** Original snapshot; bytes differing from this are highlighted as edited. */
  originalBytes?: Uint8Array
  /** Bytes per page (defaults to {@link DEFAULT_PAGE_SIZE}). */
  pageSize?: number
  /** UI strings; defaults are applied for any missing field. */
  labels?: HexViewLabels
}

// ---------------------------------------------------------------------------
// Pure helpers (exported for unit testing)
// ---------------------------------------------------------------------------

/** Format a byte value as two lowercase hex digits (masks to 0..255). */
export function toHexByte(n: number): string {
  return (n & 0xff).toString(16).padStart(2, '0')
}

/** Format a single nibble (0..15) as one lowercase hex digit. */
export function toHexNibble(n: number): string {
  return (n & 0xf).toString(16)
}

/** Format a byte offset as an at-least-8-digit lowercase hex string. */
export function toHexOffset(offset: number): string {
  const o = Math.max(0, Math.floor(offset))
  return o.toString(16).padStart(8, '0')
}

/** Printable-ASCII char for a byte, or '.' for everything else (incl. space). */
export function toAsciiChar(n: number): string {
  const b = n & 0xff
  return b >= 0x21 && b <= 0x7e ? String.fromCharCode(b) : '.'
}

/** Parse a single hex char ('0'..'9', 'a'..'f', 'A'..'F') to 0..15, else -1. */
export function parseHexNibble(ch: string): number {
  if (ch.length !== 1) return -1
  const c = ch.charCodeAt(0)
  if (c >= 48 && c <= 57) return c - 48 // 0-9
  if (c >= 65 && c <= 70) return c - 55 // A-F
  if (c >= 97 && c <= 102) return c - 87 // a-f
  return -1
}

/** Build up to `rowCount` hex rows starting at byte `offset` (clamped to data). */
export function buildHexRows(bytes: Uint8Array, offset: number, rowCount?: number): HexRow[] {
  const start = Math.max(0, Math.min(Math.floor(offset), bytes.length))
  const remaining = bytes.length - start
  const maxRows = Math.ceil(remaining / BYTES_PER_ROW)
  const count = rowCount == null ? maxRows : Math.max(0, Math.min(rowCount, maxRows))
  const rows: HexRow[] = []
  for (let r = 0; r < count; r++) {
    const rowOffset = start + r * BYTES_PER_ROW
    const end = Math.min(rowOffset + BYTES_PER_ROW, bytes.length)
    const bs: number[] = []
    for (let i = rowOffset; i < end; i++) {
      const v = bytes[i]
      if (v !== undefined) bs.push(v)
    }
    rows.push({ offset: rowOffset, bytes: bs })
  }
  return rows
}

/** Number of pages for a given byte length / page size (min 1). */
export function totalPagesFor(byteLength: number, pageSize: number): number {
  if (byteLength <= 0) return 1
  return Math.ceil(byteLength / pageSize)
}

/** Half-open byte range [start, end) for a page. */
export function pageRangeFor(
  page: number,
  pageSize: number,
  byteLength: number,
): { start: number; end: number } {
  const start = Math.max(0, Math.floor(page)) * pageSize
  const end = Math.min(start + pageSize, byteLength)
  return { start, end }
}

/** True when `bytes[index]` differs from the original snapshot. */
export function isByteEdited(
  bytes: Uint8Array,
  originalBytes: Uint8Array | undefined,
  index: number,
): boolean {
  if (!originalBytes) return false
  if (index < 0 || index >= bytes.length) return false
  if (index >= originalBytes.length) return true // byte added beyond original
  return bytes[index] !== originalBytes[index]
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

const DEFAULT_LABELS: Required<HexViewLabels> = {
  offset: 'Offset',
  ascii: 'ASCII',
  pageOf: '{page} / {total}',
  prevPage: 'Previous page',
  nextPage: 'Next page',
  firstPage: 'First page',
  lastPage: 'Last page',
  empty: 'No data',
  readOnly: 'Read only',
}

export const HexView: React.FC<HexViewProps> = ({
  bytes,
  onChange,
  readOnly = false,
  originalBytes,
  pageSize = DEFAULT_PAGE_SIZE,
  labels,
}) => {
  const L = { ...DEFAULT_LABELS, ...labels }

  const totalPages = totalPagesFor(bytes.length, pageSize)
  const [page, setPage] = useState(0)
  const [editingIndex, setEditingIndex] = useState<number | null>(null)
  /** High nibble already typed for the byte under edit (null = none yet). */
  const [firstNibble, setFirstNibble] = useState<number | null>(null)

  const gridRef = useRef<HTMLDivElement>(null)

  // Clamp the page when the data shrinks below the current page.
  useEffect(() => {
    if (page > totalPages - 1) setPage(Math.max(0, totalPages - 1))
  }, [page, totalPages])

  // Move keyboard focus onto the grid whenever a byte enters edit mode.
  useEffect(() => {
    if (editingIndex != null) gridRef.current?.focus({ preventScroll: true })
  }, [editingIndex])

  // Drop the edit handle if the data shrinks beneath the edited byte.
  useEffect(() => {
    if (editingIndex != null && editingIndex >= bytes.length) cancelEdit()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editingIndex, bytes.length])

  const range = useMemo(() => pageRangeFor(page, pageSize, bytes.length), [page, pageSize, bytes.length])
  const rowCount = Math.ceil((range.end - range.start) / BYTES_PER_ROW)
  const rows = useMemo(
    () => buildHexRows(bytes, range.start, rowCount),
    [bytes, range.start, rowCount],
  )

  // Offset column width (min 8 hex digits) keeps header + rows aligned across
  // pages regardless of file size.
  const offsetDigits = bytes.length > 0 ? Math.max(8, bytes.length.toString(16).length) : 8

  const beginEdit = (absIndex: number) => {
    if (readOnly) return
    setEditingIndex(absIndex)
    setFirstNibble(null)
  }

  const cancelEdit = () => {
    setEditingIndex(null)
    setFirstNibble(null)
  }

  const commitByte = (index: number, value: number) => {
    if (index < 0 || index >= bytes.length) return
    if (bytes[index] === value) return
    const next = bytes.slice()
    next[index] = value
    onChange(next)
  }

  const advanceTo = (nextIndex: number) => {
    setFirstNibble(null)
    if (nextIndex < 0 || nextIndex >= bytes.length) {
      setEditingIndex(null)
      return
    }
    setEditingIndex(nextIndex)
    const targetPage = Math.floor(nextIndex / pageSize)
    if (targetPage !== page) setPage(targetPage)
  }

  const goToPage = (p: number) => {
    cancelEdit()
    setPage(Math.max(0, Math.min(totalPages - 1, p)))
  }

  const handleEditKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (editingIndex == null) return
    switch (e.key) {
      case 'Escape':
        e.preventDefault()
        cancelEdit()
        return
      case 'Backspace':
        e.preventDefault()
        setFirstNibble(null)
        return
      case 'ArrowRight':
        e.preventDefault()
        advanceTo(editingIndex + 1)
        return
      case 'ArrowLeft':
        e.preventDefault()
        advanceTo(editingIndex - 1)
        return
      default: {
        const nibble = parseHexNibble(e.key)
        if (nibble < 0) {
          e.preventDefault()
          return
        }
        e.preventDefault()
        if (firstNibble == null) {
          setFirstNibble(nibble)
        } else {
          commitByte(editingIndex, (firstNibble << 4) | nibble)
          advanceTo(editingIndex + 1)
        }
      }
    }
  }

  const renderByteCell = (absIndex: number) => {
    if (absIndex >= bytes.length) {
      return <span className="hex-byte spacer" key={absIndex} aria-hidden="true" />
    }
    const editing = editingIndex === absIndex
    const edited = !editing && isByteEdited(bytes, originalBytes, absIndex)
    const cls = ['hex-byte']
    if (editing) cls.push('editing')
    else if (edited) cls.push('edited')
    if (!readOnly) cls.push('clickable')
    const value = bytes[absIndex]!
    const text = editing
      ? firstNibble == null
        ? toHexByte(value)
        : toHexNibble(firstNibble)
      : toHexByte(value)
    return (
      <span
        className={cls.join(' ')}
        key={absIndex}
        onClick={!readOnly ? () => beginEdit(absIndex) : undefined}
      >
        {text}
      </span>
    )
  }

  const renderAsciiCell = (absIndex: number) => {
    if (absIndex >= bytes.length) {
      return <span className="hex-ascii-char spacer" key={absIndex} aria-hidden="true" />
    }
    const edited = isByteEdited(bytes, originalBytes, absIndex)
    return (
      <span className={`hex-ascii-char${edited ? ' edited' : ''}`} key={absIndex}>
        {toAsciiChar(bytes[absIndex]!)}
      </span>
    )
  }

  const pagerLabel = L.pageOf
    .replace('{page}', String(page + 1))
    .replace('{total}', String(totalPages))

  return (
    <div className="hex-view">
      {totalPages > 1 && (
        <div className="hex-view-toolbar">
          <div className="hex-view-pager">
            <button
              type="button"
              className="hex-view-pager-btn"
              title={L.firstPage}
              disabled={page <= 0}
              onClick={() => goToPage(0)}
            >
              «
            </button>
            <button
              type="button"
              className="hex-view-pager-btn"
              title={L.prevPage}
              disabled={page <= 0}
              onClick={() => goToPage(page - 1)}
            >
              ‹
            </button>
            <span className="hex-view-pager-info">{pagerLabel}</span>
            <button
              type="button"
              className="hex-view-pager-btn"
              title={L.nextPage}
              disabled={page >= totalPages - 1}
              onClick={() => goToPage(page + 1)}
            >
              ›
            </button>
            <button
              type="button"
              className="hex-view-pager-btn"
              title={L.lastPage}
              disabled={page >= totalPages - 1}
              onClick={() => goToPage(totalPages - 1)}
            >
              »
            </button>
          </div>
          {readOnly && <span className="hex-view-ro-hint">{L.readOnly}</span>}
        </div>
      )}
      <div className="hex-view-body">
        {bytes.length === 0 ? (
          <div className="hex-view-empty">{L.empty}</div>
        ) : (
          <div
            className="hex-view-grid"
            ref={gridRef}
            tabIndex={0}
            style={{ '--hex-offset-width': `${offsetDigits + 1}ch` } as React.CSSProperties}
            onKeyDown={handleEditKey}
            onBlur={cancelEdit}
          >
            <div className="hex-row hex-row-header">
              <span className="hex-cell-offset">{L.offset}</span>
              <span className="hex-cell-group">
                {Array.from({ length: 8 }, (_, c) => (
                  <span className="hex-byte hex-col" key={c}>
                    {toHexByte(c)}
                  </span>
                ))}
              </span>
              <span className="hex-cell-group">
                {Array.from({ length: 8 }, (_, c) => (
                  <span className="hex-byte hex-col" key={c + 8}>
                    {toHexByte(c + 8)}
                  </span>
                ))}
              </span>
              <span className="hex-cell-ascii-group hex-col-ascii">{L.ascii}</span>
            </div>

            {rows.map(row => (
              <div className="hex-row" key={row.offset}>
                <span className="hex-cell-offset">{toHexOffset(row.offset)}</span>
                <span className="hex-cell-group">
                  {Array.from({ length: 8 }, (_, col) => renderByteCell(row.offset + col))}
                </span>
                <span className="hex-cell-group">
                  {Array.from({ length: 8 }, (_, col) => renderByteCell(row.offset + col + 8))}
                </span>
                <span className="hex-cell-ascii-group">
                  {Array.from({ length: 16 }, (_, col) => renderAsciiCell(row.offset + col))}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
