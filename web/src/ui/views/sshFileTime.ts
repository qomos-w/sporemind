/**
 * Pure helper for the SSH FTP file-list "Modified" cell.
 *
 * The SCP backend reports RFC3339 (`2026-08-14T12:34:56+08:00`), which is too
 * long for the column and wraps onto a second line; the exec backend reports
 * the raw `ls` date (`2026-08-14 12:34` or `Aug 14 12:34`). RFC3339 values are
 * normalized to compact local time; anything else passes through unchanged.
 */

const RFC3339_RE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/

export function formatSshModified(value: string | undefined): string {
  if (!value) return ''
  if (!RFC3339_RE.test(value)) return value
  const d = new Date(value)
  if (isNaN(d.getTime())) return value
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
