import React from 'react'
import type { ToolFrame } from '../../model/frame-types.ts'
import type { FileSystemGrepReq, FileSystemGrepResp } from '../../../../gen-types/filesystem'
import { parseJsonObject, splitPatternPath } from './tool-display.ts'
import { ToolBodyFrame, ToolCodeBlock, ToolSection, ToolSummaryRow, ToolRunning, ToolFallbackOutput } from './ToolViewPrimitives.tsx'

interface SearchToolViewProps {
  frame: ToolFrame
}

export interface ContentMatch { file: string; line: number; content: string }
export interface CountRow { file: string; count: number }

export type ParsedResult =
  | { mode: 'content'; matches: ContentMatch[] }
  | { mode: 'files'; files: string[] }
  | { mode: 'count'; rows: CountRow[] }

const ROW_LIMIT = 15

export function parseGrepResult(outputMode: string, output?: string): ParsedResult | null {
  // Backend uses "files" while some callers/schema use "files_with_matches".
  if (outputMode === 'files_with_matches' || outputMode === 'files') {
    const resp = parseJsonObject(output || '') as FileSystemGrepResp | null
    if (resp && Array.isArray(resp.Files)) return { mode: 'files', files: resp.Files }
    return null
  }

  if (outputMode === 'count') {
    const resp = parseJsonObject(output || '') as FileSystemGrepResp | null
    if (resp && Array.isArray(resp.Counts)) {
      const rows: CountRow[] = resp.Counts.map(c => ({ file: c.File, count: c.Count }))
      return { mode: 'count', rows }
    }
    return null
  }

  const resp = parseJsonObject(output || '') as FileSystemGrepResp | null
  if (resp && Array.isArray(resp.Matches)) {
    const out: ContentMatch[] = resp.Matches.map(m => ({ file: m.File, line: m.Line, content: m.Content }))
    return { mode: 'content', matches: out }
  }
  return null
}

export const SearchToolView: React.FC<SearchToolViewProps> = ({ frame }) => {
  const [expanded, setExpanded] = React.useState(false)
  const parsed = parseJsonObject(frame.input) as FileSystemGrepReq | null
  const pattern = parsed?.Pattern
  let path = parsed?.Path
  let glob = parsed?.Glob
  if (!path && glob) {
    const split = splitPatternPath(glob)
    if (split.path) {
      path = split.path
      glob = split.pattern
    }
  }
  const scope = [path, glob].filter(Boolean).join(' · ')
  const parsedOutputMode = parsed?.Output_mode
  const respOutput = parseJsonObject(frame.output || '') as FileSystemGrepResp | null
  // The backend is authoritative for which shape it returned; fall back to the
  // request's output_mode for legacy calls that did not echo it back.
  const outputMode = respOutput?.Output_mode || parsedOutputMode || 'content'
  const caseInsensitive = parsed?.Ignore_case
  const grepContext = parsed?.Context
  const flagBits: string[] = []
  if (caseInsensitive) flagBits.push('case-insensitive')
  if (grepContext != null && grepContext > 0) flagBits.push(`±${grepContext} lines`)
  const flags = flagBits.join(' · ')
  const isRunning = frame.status === 'running'

  const result = parseGrepResult(outputMode, frame.output)

  const renderRowsWithFold = <T,>(rows: T[], renderRow: (row: T, i: number) => React.ReactNode): React.ReactNode => {
    const overLimit = rows.length > ROW_LIMIT
    const visibleRows = expanded || !overLimit ? rows : rows.slice(0, ROW_LIMIT)
    const hidden = rows.length - visibleRows.length
    return (
      <>
        {visibleRows.map(renderRow)}
        {overLimit && (
          <button
            type="button"
            className="ai-tool-code-expander"
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? 'collapse' : `… +${hidden} more (click to expand)`}
          </button>
        )}
      </>
    )
  }

  let resultSection: React.ReactNode = null
  if (result) {
    if (result.mode === 'content') {
      resultSection = (
        <ToolSection label={`Matches (${result.matches.length})`}>
          <div className="ai-tool-match-list">
            {renderRowsWithFold(result.matches, (m, i) => (
              <div key={`m-${i}`} className="ai-tool-match-item">
                <span className="ai-tool-match-loc">{m.file}:{m.line}</span>
                {': '}
                <span>{m.content}</span>
              </div>
            ))}
          </div>
        </ToolSection>
      )
    } else if (result.mode === 'files') {
      resultSection = (
        <ToolSection label={`Files (${result.files.length})`}>
          <div className="ai-tool-match-list">
            {renderRowsWithFold(result.files, (f, i) => (
              <div key={`f-${i}`} className="ai-tool-match-item">{f}</div>
            ))}
          </div>
        </ToolSection>
      )
    } else {
      resultSection = (
        <ToolSection label={`Counts (${result.rows.length})`}>
          <div className="ai-tool-count-table">
            {renderRowsWithFold(result.rows, (r, i) => (
              <div key={`c-${i}`} className="ai-tool-count-row">
                <span className="ai-tool-count-file">{r.file}</span>
                <span className="ai-tool-count-num">{r.count}</span>
              </div>
            ))}
          </div>
        </ToolSection>
      )
    }
  }

  return (
    <ToolBodyFrame frame={frame}>
      {pattern && (
        <ToolSection label="Pattern">
          <ToolCodeBlock>{scope ? `${pattern}  (${scope})` : pattern}</ToolCodeBlock>
        </ToolSection>
      )}
      {!pattern && scope && <ToolSummaryRow label="Scope" value={scope} />}
      {outputMode !== 'content' && <ToolSummaryRow label="Mode" value={outputMode} />}
      {flags && <ToolSummaryRow label="Flags" value={flags} />}

      {isRunning && <ToolRunning label="Searching…" />}

      {resultSection}

      <ToolFallbackOutput frame={frame} parsed={result} />
    </ToolBodyFrame>
  )
}
