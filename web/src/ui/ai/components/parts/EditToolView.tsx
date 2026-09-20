import React, { useState, useCallback } from 'react'
import { Undo2, Pencil } from 'lucide-react'
import type { ToolFrame, FileChangeEntry } from '../../model/frame-types.ts'
import type { FileSystemEditReq, FileSystemWriteReq } from '../../../../gen-types/filesystem'
import { normalizePath, parseJsonObject } from './tool-display.ts'
import { parseEditOutput, hunksToUnifiedDiff } from '../../hooks/tool-parsers'
import { DiffBlock } from './DiffBlock.tsx'
import { ToolRunning } from './ToolViewPrimitives.tsx'
import { setReviewState } from './review-store'
import { useAIShellContext } from '../../context/AIShellContext'
import { ToolFoldCard } from './ToolFoldCard.tsx'

const MAX_SYNTHETIC_DIFF_LINES = 100

function buildSyntheticDiff(oldStr: string, newStr: string, filePath?: string): string {
  const oldLines = oldStr.split('\n')
  const newLines = newStr.split('\n')
  if (oldLines.length > MAX_SYNTHETIC_DIFF_LINES || newLines.length > MAX_SYNTHETIC_DIFF_LINES) {
    return `--- a/${filePath ?? 'file'}\n+++ b/${filePath ?? 'file'}\n@@ File too large to display inline (${Math.max(oldLines.length, newLines.length)} lines) @@`
  }
  const header = filePath ?? 'file'
  return [
    `--- a/${header}`,
    `+++ b/${header}`,
    `@@ -1,${oldLines.length} +1,${newLines.length} @@`,
    ...oldLines.map(l => `-${l}`),
    ...newLines.map(l => `+${l}`),
  ].join('\n')
}

// ── Inline diff line limit ──
const INLINE_DIFF_MAX_LINES = 12

// ── Main component ──

interface EditToolViewProps {
  frame: ToolFrame
  turnStreaming?: boolean
}

export const EditToolView: React.FC<EditToolViewProps> = ({ frame, turnStreaming }) => {
  const shell = useAIShellContext()
  const parsed = parseJsonObject(frame.input) as (FileSystemEditReq & FileSystemWriteReq) | null
  const filePath = normalizePath(parsed?.Path)
  const oldString = parsed?.Old_string
  const newString = parsed?.New_string
  const replaceAll = parsed?.Replace_all
  const writeContent = (parsed as FileSystemWriteReq | null)?.Content

  const isActive = frame.status === 'running' || frame.status === 'pending'
  const isCompleted = frame.status === 'completed'
  const isWrite = writeContent != null

  // Build diff content
  const hasFileChangesDiff = frame.fileChanges?.some(c => c.diffContent) ?? false
  const editOut = frame.output ? parseEditOutput(frame.output) : null
  const hasStructuredHunks = editOut?.Hunks && editOut.Hunks.length > 0

  let diffContent: string | undefined
  if (hasFileChangesDiff) {
    diffContent = frame.fileChanges!.find(c => c.diffContent)!.diffContent
  } else if (hasStructuredHunks) {
    diffContent = hunksToUnifiedDiff(editOut!.Hunks)
  } else if (oldString != null && newString != null) {
    diffContent = buildSyntheticDiff(oldString, newString, filePath)
  } else if (isWrite && writeContent != null && filePath != null) {
    diffContent = buildSyntheticDiff('', writeContent, filePath)
  }

  const replacements = editOut?.Replacements
  const showReplaceCount = replacements != null && replaceAll === true && replacements > 1
  const writeBytes = isWrite && writeContent ? new TextEncoder().encode(writeContent).length : 0

  const stats = editOut?.Additions != null || editOut?.Deletions != null
    ? { add: editOut.Additions ?? 0, del: editOut.Deletions ?? 0 }
    : undefined

  const hasDiff = diffContent != null

  const [reverted, setReverted] = useState(false)

  const handleRevert = useCallback(() => {
    setReverted(true)
  }, [])

  const handleMaximize = useCallback(() => {
    if (filePath && shell.onOpenFile) {
      const startLine = hasStructuredHunks && editOut?.Hunks?.length
        ? editOut.Hunks[0]!.New_start || editOut.Hunks[0]!.Old_start || undefined
        : undefined
      shell.onOpenFile(filePath, diffContent, startLine)
      return
    }
    const changes: FileChangeEntry[] = frame.fileChanges && frame.fileChanges.length > 0
      ? frame.fileChanges
      : diffContent
        ? [{
            filename: filePath?.split('/').pop() ?? 'file',
            filepath: filePath,
            additions: stats?.add ?? 0,
            deletions: stats?.del ?? 0,
            diffContent,
          }]
        : []
    if (changes.length > 0) {
      setReviewState({ fileChanges: changes, turnId: frame.id })
      window.dispatchEvent(new CustomEvent('sporemind:open-view', { detail: 'turn-review' }))
    }
  }, [frame.fileChanges, frame.id, diffContent, filePath, stats, shell, hasStructuredHunks, editOut])

  // Clicking the diff/write text area opens the file in the right panel tab,
  // same as the maximize button. Clicks on the diff expander and drag text
  // selections are left alone; the fold toggle is always suppressed.
  const handleDiffAreaClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation()
    if ((e.target as HTMLElement).closest('.ai-tool-code-expander')) return
    const selection = window.getSelection()
    if (selection && !selection.isCollapsed) return
    if (isCompleted && !isActive) handleMaximize()
  }, [isCompleted, isActive, handleMaximize])

  // No diff and no write content and no identifiable file path — fallback to raw display
  if (!hasDiff && !writeContent && !isActive && filePath == null) {
    return (
      <div className={`ai-tool-body ${frame.status === 'error' ? 'error' : ''}`}>
        <pre className="ai-tool-code">{frame.input}</pre>
        {frame.status === 'error' && frame.output && (
          <pre className="ai-tool-code error">{frame.output}</pre>
        )}
      </div>
    )
  }

  return (
    <ToolFoldCard
      frame={frame}
      uiKind="edit"
      icon={<Pencil size={12} className={`ai-edit-card-icon ${frame.status === 'error' ? 'ai-step-icon-error' : ''}`} />}
      stepClassName="ai-edit-step"
      wrapperClassName={`ai-edit-card ${reverted ? 'reverted' : ''}`}
      onMaximize={isCompleted && !isActive ? handleMaximize : undefined}
      turnStreaming={turnStreaming}
      label={
        <>
          {filePath && <span className="ai-edit-card-path" title={filePath}>{filePath}</span>}
          {showReplaceCount && <span className="ai-edit-card-replace-count">x{replacements}</span>}
        </>
      }
      headerExtras={
        <>
          <span className="ai-edit-card-spacer" />
          {isCompleted && !isActive && (
            <span className="ai-edit-card-header-actions">
              {!reverted && (
                <button
                  type="button"
                  className="ai-edit-card-action revert"
                  title="Revert"
                  onClick={(e) => { e.stopPropagation(); handleRevert() }}
                >
                  <Undo2 size={12} />
                </button>
              )}
              {reverted && <span className="ai-edit-card-reverted-badge">Reverted</span>}
            </span>
          )}
          {stats && (
            <span className="ai-edit-card-stats">
              <span className="ai-edit-card-stat add">+{stats.add}</span>
              <span className="ai-edit-card-stat del">-{stats.del}</span>
            </span>
          )}
          {frame.status === 'error' && <span className="ai-edit-card-error-badge">Failed</span>}
        </>
      }
    >
      <div
        className={`ai-edit-card-diff${isCompleted && !isActive ? ' ai-edit-card-diff--openable' : ''}`}
        onClick={handleDiffAreaClick}
      >
        {isActive ? (
          <ToolRunning label={isWrite ? 'Writing...' : 'Editing...'} />
        ) : hasDiff ? (
          <DiffBlock diffContent={diffContent!} maxLines={INLINE_DIFF_MAX_LINES} filePath={filePath} />
        ) : writeContent != null ? (
          <pre className="ai-tool-code">{writeContent}</pre>
        ) : null}
      </div>

      {isWrite && isCompleted && !reverted && (
        <div className="ai-tool-success">
          Wrote {writeBytes} bytes{filePath ? ` to ${filePath}` : ''}
        </div>
      )}

      {frame.status === 'error' && frame.output && (
        <pre className="ai-tool-code error">{frame.output}</pre>
      )}
    </ToolFoldCard>
  )
}
