/**
 * Pure helpers for the SSH FTP file-list column widths.
 *
 * Extracted as side-effect-free functions so the resize logic (clamping +
 * grid-template composition) can be unit-tested without mounting the heavy
 * xterm-backed {@link SshSessionView}. See the project testing constraint:
 * pure logic fragments are covered directly rather than through full assembly.
 */

export type FileColumnKey = 'name' | 'size' | 'mode' | 'modified'

export interface FileColumnWidths {
  name: number
  size: number
  mode: number
  modified: number
}

/** Hard floor per column — a column can never be dragged below this. */
export const FILE_COLUMN_MIN: Record<FileColumnKey, number> = {
  name: 80,
  size: 48,
  mode: 80,
  modified: 104,
}

/** Initial widths, chosen to match the previous fixed layout as closely as
 *  possible while leaving room for typical content (permissions string,
 *  formatted date). */
export const FILE_COLUMN_DEFAULT: FileColumnWidths = {
  name: 240,
  size: 80,
  mode: 100,
  modified: 120,
}

export const FILE_COLUMN_ORDER: FileColumnKey[] = ['name', 'size', 'mode', 'modified']

export const FILE_COLUMN_LABELS: Record<FileColumnKey, string> = {
  name: 'Name',
  size: 'Size',
  mode: 'Mode',
  modified: 'Modified',
}

/** Clamp a proposed width to the column's minimum, rounding to whole pixels. */
export function clampColumnWidth(key: FileColumnKey, width: number): number {
  return Math.max(FILE_COLUMN_MIN[key], Math.round(width))
}

/**
 * Compose the CSS `grid-template-columns` value shared by the file-list header
 * and every row so the columns stay pixel-aligned.
 *
 * All four content columns carry explicit pixel widths (so each is independently
 * draggable). A trailing `1fr` track absorbs any leftover horizontal space so
 * the header/rows always span the full container width without a gap or
 * spurious horizontal scrollbar; it shrinks to zero once the four columns
 * exceed the available width (at which point the list scrolls horizontally).
 */
export function buildFileGridCols(w: FileColumnWidths): string {
  return `${w.name}px ${w.size}px ${w.mode}px ${w.modified}px 1fr`
}
