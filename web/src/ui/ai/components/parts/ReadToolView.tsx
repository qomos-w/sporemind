import React, { useLayoutEffect, useRef, useState } from 'react'
import type { ToolFrame } from '../../model/frame-types.ts'
import type { FileSystemReadReq } from '../../../../gen-types/filesystem'
import { parseJsonObject, unescapeOutput } from './tool-display.ts'
import { ToolBodyFrame, ToolSection, ToolSummaryRow, ToolRunning } from './ToolViewPrimitives.tsx'
import * as agentClient from '../../../../gen-clients/local/client'
import { client } from '../../../../application/generated-client'

const MAX_RENDER_LINES = 15

function FilePreview({ output, isError }: { output: string; isError: boolean }) {
  const [expanded, setExpanded] = useState(false)
  const preRef = useRef<HTMLPreElement>(null)
  const [needsClamp, setNeedsClamp] = useState(false)

  useLayoutEffect(() => {
    const el = preRef.current
    if (!el) return
    const lineHeightPx = parseFloat(getComputedStyle(el).lineHeight) || 20
    const maxPx = MAX_RENDER_LINES * lineHeightPx
    setNeedsClamp(el.scrollHeight > maxPx + 0.5)
  }, [output])

  const baseCls = ['ai-tool-code', isError ? 'error' : ''].filter(Boolean).join(' ')

  if (!needsClamp) {
    return <pre className={baseCls} ref={preRef}>{output}</pre>
  }

  return (
    <div className="ai-tool-code-wrapper">
      <pre
        ref={preRef}
        className={baseCls}
        style={expanded ? undefined : { maxHeight: `${MAX_RENDER_LINES}lh`, overflow: 'hidden' }}
      >
        {output}
      </pre>
      <button
        type="button"
        className="ai-tool-code-expander"
        onClick={() => setExpanded(!expanded)}
      >
        {expanded ? 'collapse' : `… +more lines (click to expand)`}
      </button>
    </div>
  )
}

interface ReadToolViewProps {
  frame: ToolFrame
  agentActorId?: string
}

function parseReadOutput(output: string): string {
  const parsed = parseJsonObject(output) as { Content?: string; content?: string } | null
  if (parsed) {
    const content = typeof parsed.Content === 'string' ? parsed.Content : typeof parsed.content === 'string' ? parsed.content : undefined
    if (content != null) return unescapeOutput(content)
  }
  return unescapeOutput(output)
}

export const ReadToolView: React.FC<ReadToolViewProps> = ({ frame, agentActorId }) => {
  const parsed = parseJsonObject(frame.input) as FileSystemReadReq | null
  const filePath = frame.snapshotRef?.path ?? parsed?.Path
  const offset = frame.snapshotRef?.offset ?? parsed?.Offset
  const limit = frame.snapshotRef?.limit ?? parsed?.Limit
  const tail = parsed?.Tail
  const startLine = frame.snapshotRef?.startLine
  const numLines = frame.snapshotRef?.numLines
  const totalLines = frame.snapshotRef?.totalLines

  // Prefer the real read span (1-based, from the backend response). Fall back to
  // the raw offset/limit only when meaningful (limit > 0); an omitted limit means
  // "whole file" and must never render a bogus range like "0–-1".
  let range: string | undefined
  if (tail != null && tail > 0) {
    range = `last ${tail} lines`
  } else if (numLines != null && numLines > 0 && startLine != null) {
    const end = startLine + numLines - 1
    range = totalLines != null && totalLines > 0 ? `${startLine}–${end} of ${totalLines}` : `${startLine}–${end}`
  } else if (offset != null && limit != null && limit > 0) {
    range = `${offset}–${offset + limit - 1}`
  } else if (offset != null && offset > 0) {
    range = `from ${offset}`
  } else if (limit != null && limit > 0) {
    range = `limit ${limit}`
  }

  const isRunning = frame.status === 'running'
  const isError = frame.status === 'error'
  const hasInlineOutput = frame.output != null && frame.output !== ''
  const hasSnapshot = frame.snapshotRef != null

  const [snapshotContent, setSnapshotContent] = useState('')
  const [snapshotLoading, setSnapshotLoading] = useState(false)
  const [snapshotError, setSnapshotError] = useState('')

  const handleLoadSnapshot = async () => {
    if (!agentActorId || !frame.snapshotRef) return
    setSnapshotLoading(true)
    setSnapshotError('')
    try {
      const resp = await agentClient.readSnapshot(client, { snapshotId: frame.snapshotRef.id }, { target: agentActorId })
      const content = resp.content
      setSnapshotContent(content)
    } catch (err) {
      setSnapshotError(err instanceof Error ? err.message : 'Failed to load snapshot')
    } finally {
      setSnapshotLoading(false)
    }
  }

  const output = hasInlineOutput ? parseReadOutput(frame.output!) : snapshotContent

  return (
    <ToolBodyFrame frame={frame}>
      {filePath && <ToolSummaryRow label="File" value={filePath} />}
      {range && <ToolSummaryRow label="Range" value={range} />}

      {isRunning && <ToolRunning label="Reading…" />}

      {hasSnapshot && !hasInlineOutput && snapshotContent === '' && !snapshotLoading && (
        <div className="ai-tool-snapshot">
 <button type="button" className="ai-tool-snapshot-load" onClick={handleLoadSnapshot}>
            Load file preview ({(frame.snapshotRef!.size / 1024).toFixed(1)} KB)
          </button>
        </div>
      )}

      {hasSnapshot && snapshotLoading && <ToolRunning label="Loading…" />}

      {snapshotError && (
        <ToolSection label="Error">
          <div className="ai-tool-error">{snapshotError}</div>
        </ToolSection>
      )}

      {(output !== '' || (hasSnapshot && snapshotContent !== '')) && (
        <ToolSection label={isError ? 'Error' : 'File preview'}>
          <FilePreview output={output} isError={isError} />
        </ToolSection>
      )}
    </ToolBodyFrame>
  )
}
