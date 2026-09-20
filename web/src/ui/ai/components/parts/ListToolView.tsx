import React from 'react'
import { File as FileIcon, Folder } from 'lucide-react'
import type { ToolFrame } from '../../model/frame-types.ts'
import type { FileSystemListReq, FileEntryListResp, FileEntry } from '../../../../gen-types/filesystem'
import { parseJsonObject, normalizePath } from './tool-display.ts'
import { parseListText } from './list-text.ts'
import { ToolBodyFrame, ToolSection, ToolSummaryRow, ToolRunning, ToolFallbackOutput } from './ToolViewPrimitives.tsx'

interface ListToolViewProps {
  frame: ToolFrame
}

const ROW_LIMIT = 10

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function formatMtime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime())
    ? ''
    : d.toLocaleString(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

export const ListToolView: React.FC<ListToolViewProps> = ({ frame }) => {
  const [expanded, setExpanded] = React.useState(false)
  const isRunning = frame.status === 'running'

  // Legacy sessions persisted project.list_json output as JSON; current
  // project.list output is line-based text (plain or detail).
  const resp = parseJsonObject(frame.output || '') as FileEntryListResp | null
  let entries: FileEntry[] | null = resp?.Items ?? null
  let truncated = resp?.Truncated ?? false
  let extra: string[] = []
  if (!entries && frame.output) {
    const parsed = parseListText(frame.output)
    truncated = parsed.truncated
    extra = parsed.extraLines
    if (parsed.entries.length > 0) entries = parsed.entries
  }

  const parsedInput = parseJsonObject(frame.input) as FileSystemListReq | null
  const path = parsedInput?.Path
  const recursive = (parsedInput?.Depth ?? 0) < 0
  const exclude = parsedInput?.Exclude

  const byName = (a: FileEntry, b: FileEntry) => (a.Name ?? '').localeCompare(b.Name ?? '')
  const dirs = entries?.filter(e => e.IsDir).slice().sort(byName) ?? []
  const files = entries?.filter(e => !e.IsDir).slice().sort(byName) ?? []
  const summary = entries
    ? `${entries.length} entries · ${dirs.length} dirs · ${files.length} files${truncated ? ' · truncated' : ''}`
    : undefined

  type Row = FileEntry & { kind: 'dir' | 'file' }
  const allRows: Row[] = [
    ...dirs.map((e): Row => ({ ...e, kind: 'dir' })),
    ...files.map((e): Row => ({ ...e, kind: 'file' })),
  ]
  const overLimit = allRows.length > ROW_LIMIT
  const visibleRows = expanded || !overLimit ? allRows : allRows.slice(0, ROW_LIMIT)
  const hiddenCount = allRows.length - visibleRows.length

  return (
    <ToolBodyFrame frame={frame}>
      {path && <ToolSummaryRow label="Path" value={normalizePath(path) ?? path} />}
      {recursive && <ToolSummaryRow label="Mode" value="Recursive" />}
      {exclude && <ToolSummaryRow label="Exclude" value={exclude} />}
      {summary && <ToolSummaryRow label="Summary" value={summary} />}

      {isRunning && <ToolRunning label="Listing…" />}

      {entries && entries.length > 0 && (
        <ToolSection label="Entries">
          <div className="ai-tool-list">
            {visibleRows.map((e, i) => (
              <div key={`${e.kind}-${i}`} className="ai-tool-list-row">
                {e.kind === 'dir'
                  ? <Folder size={12} className="ai-tool-list-icon" />
                  : <FileIcon size={12} className="ai-tool-list-icon" />}
                <span className="ai-tool-list-name" title={e.kind === 'dir' ? `${e.Name}/` : e.Name}>{e.kind === 'dir' ? `${e.Name}/` : e.Name}</span>
                {e.ModTime && (
                  <span className="ai-tool-list-size">{formatMtime(e.ModTime)}</span>
                )}
                {e.kind === 'file' && e.Size > 0 && (
                  <span className="ai-tool-list-size">{formatBytes(e.Size)}</span>
                )}
              </div>
            ))}
            {overLimit && (
              <button
                type="button"
                className="ai-tool-code-expander"
                onClick={() => setExpanded(!expanded)}
              >
                {expanded ? 'collapse' : `… +${hiddenCount} more entries (click to expand)`}
              </button>
            )}
          </div>
        </ToolSection>
      )}
      {extra.length > 0 && (
        <ToolSection label="Notes">
          <div className="ai-tool-note">{extra.join('\n')}</div>
        </ToolSection>
      )}

      <ToolFallbackOutput frame={frame} parsed={entries} />
    </ToolBodyFrame>
  )
}
