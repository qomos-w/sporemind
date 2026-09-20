import React from 'react'
import type { ToolFrame } from '../../model/frame-types.ts'
import type { FileSystemGlobReq, FileSystemGlobResp } from '../../../../gen-types/filesystem'
import { parseJsonObject, normalizePath, splitPatternPath } from './tool-display.ts'
import { ToolBodyFrame, ToolSection, ToolSummaryRow, ToolRunning, ToolFallbackOutput } from './ToolViewPrimitives.tsx'

interface GlobToolViewProps {
  frame: ToolFrame
}

const ROW_LIMIT = 15

export const GlobToolView: React.FC<GlobToolViewProps> = ({ frame }) => {
  const [expanded, setExpanded] = React.useState(true)
  const isRunning = frame.status === 'running'

  const resp = parseJsonObject(frame.output || '') as FileSystemGlobResp | null
  const files: string[] | null = Array.isArray(resp?.Files) ? resp!.Files : null

  const parsedInput = parseJsonObject(frame.input) as FileSystemGlobReq | null
  let pattern = parsedInput?.Pattern
  let path = parsedInput?.Path
  if (!path && pattern) {
    const split = splitPatternPath(pattern)
    if (split.path) {
      path = split.path
      pattern = split.pattern
    }
  }
  const maxDepth = parsedInput?.Maxdepth

  const overLimit = files != null && files.length > ROW_LIMIT
  const visibleFiles = files != null && (expanded || !overLimit) ? files : files?.slice(0, ROW_LIMIT) ?? []
  const hiddenCount = files != null ? files.length - visibleFiles.length : 0

  return (
    <ToolBodyFrame frame={frame}>
      {pattern && <ToolSummaryRow label="Pattern" value={pattern} />}
      {path && <ToolSummaryRow label="Path" value={normalizePath(path) ?? path} />}
      {maxDepth != null && <ToolSummaryRow label="Max depth" value={String(maxDepth)} />}

      {isRunning && <ToolRunning label="Searching…" />}

      {files != null && files.length > 0 && (
        <ToolSection label={`Files (${files.length})`}>
          <div className="ai-tool-match-list">
            {visibleFiles.map((f, i) => (
              <div key={`f-${i}`} className="ai-tool-match-item">{f}</div>
            ))}
            {overLimit && (
              <button
                type="button"
                className="ai-tool-code-expander"
                onClick={() => setExpanded(!expanded)}
              >
                {expanded ? 'collapse' : `… +${hiddenCount} more (click to expand)`}
              </button>
            )}
          </div>
        </ToolSection>
      )}

      {!isRunning && files != null && files.length === 0 && (
        <ToolSummaryRow label="Result" value="No files matched" />
      )}

      <ToolFallbackOutput frame={frame} parsed={files} />
    </ToolBodyFrame>
  )
}
