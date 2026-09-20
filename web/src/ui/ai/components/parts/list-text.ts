/** Parse project.list text output (plain or detail mode) into entries.
 *
 * Line formats:
 *   plain:  `name` or `name/` (dirs keep the trailing slash)
 *   detail: `name[/] YYYY-MM-DD HH:MM:SS ±HH:MM [Size:N]`
 *
 * The detail tail is matched right-anchored (greedy name prefix), so file
 * names containing spaces — even date-like text — still parse. Marker lines
 * emitted by the project actor (`[truncated: …]`, `[worktree note] …`,
 * `(empty directory)`) start with `[` or `(` and are surfaced in extraLines.
 */

export interface ListTextEntry {
  Name: string
  IsDir: boolean
  Size: number
  ModTime: string
}

export interface ListTextResult {
  entries: ListTextEntry[]
  truncated: boolean
  extraLines: string[]
}

const DETAIL_LINE_RE = /^(.*) (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2})(?: Size:(\d+))?$/

function detailMtimeToIso(mtime: string): string {
  // "YYYY-MM-DD HH:MM:SS ±HH:MM" → "YYYY-MM-DDTHH:MM:SS±HH:MM" so Date
  // honors the explicit offset instead of falling back to local time.
  const iso = mtime.replace(' ', 'T').replace(/ ([+-]\d{2}:\d{2})$/, '$1')
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

export function parseListText(output: string): ListTextResult {
  const result: ListTextResult = { entries: [], truncated: false, extraLines: [] }
  for (const raw of output.split('\n')) {
    const line = raw.trim()
    if (line === '') continue
    if (line.startsWith('[') || line.startsWith('(')) {
      if (line.startsWith('[truncated')) result.truncated = true
      result.extraLines.push(line)
      continue
    }
    const m = DETAIL_LINE_RE.exec(line)
    if (m) {
      const rawName = m[1] ?? ''
      const mtime = m[2] ?? ''
      const isDir = rawName.endsWith('/')
      result.entries.push({
        Name: isDir ? rawName.slice(0, -1) : rawName,
        IsDir: isDir,
        Size: m[3] != null ? Number(m[3]) : 0,
        ModTime: mtime !== '' ? detailMtimeToIso(mtime) : '',
      })
      continue
    }
    const isDir = line.endsWith('/')
    result.entries.push({
      Name: isDir ? line.slice(0, -1) : line,
      IsDir: isDir,
      Size: 0,
      ModTime: '',
    })
  }
  return result
}
