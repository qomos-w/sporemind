import React from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Search, FileText, Wrench } from 'lucide-react'
import type { ToolFrame } from '../../model/frame-types.ts'
import { normalizePath } from './tool-display.ts'
import { ToolBodyFrame, ToolCodeBlock } from './ToolViewPrimitives.tsx'
import { useSmoothText } from './useSmoothStreamContent.ts'
import { useSmoothStreamContext } from '../../context/SmoothStreamContext'

interface ExploreToolViewProps {
  frame: ToolFrame
}

export const workItemLine = (type: string, target: string): string => {
  switch (type) {
    case 'search':
      return `Searching ${target}`
    case 'read':
      return `Reading ${target}`
    case 'tool':
      return `Running ${target}`
    case 'write':
      return `Writing ${target}`
    default:
      return `${type} ${target}`
  }
}

const workItemTitle = (type: string): string => {
  switch (type) {
    case 'search':
      return 'Searching'
    case 'read':
      return 'Reading'
    case 'tool':
      return 'Running'
    case 'write':
      return 'Writing'
    default:
      return type
  }
}

const workItemTarget = (type: string, target: string): string => {
  if (type !== 'read' && type !== 'write') return target
  const normalized = normalizePath(target) ?? target
  return normalized.split('/').at(-1) ?? normalized
}

const workItemIcon = (type: string): React.ComponentType<{ size?: number; className?: string }> => {
  switch (type) {
    case 'search':
      return Search
    case 'read':
      return FileText
    default:
      return Wrench
  }
}

// Same structure as an ai-step tool title row: icon + title + subtitle.
export const ExploreWorkItem: React.FC<{ type: string; target: string; countLabel?: string }> = ({ type, target, countLabel }) => {
  const Icon = workItemIcon(type)
  const displayTarget = workItemTarget(type, target)
  return (
    <div className="ai-explore-work-item">
      <Icon size={12} className="ai-explore-work-item-icon" />
      <span className="ai-step-label ai-tool-step-label">
        <span>{workItemTitle(type)}</span>
        <span className="ai-tool-step-subtitle">
          <span className="ai-tool-step-subtitle-text" title={target}>{displayTarget}</span>
          {countLabel && <span className="ai-tool-step-subtitle-count"> · {countLabel}</span>}
        </span>
      </span>
    </div>
  )
}

export const ExploreToolView: React.FC<ExploreToolViewProps> = ({ frame }) => {
  const progress = frame.progress
  const activeWork = progress?.activeWork ?? []
  const phase = progress?.phase
  const isRunning = frame.status === 'running'
  const isSummarizing = isRunning && phase === 'summarizing'
  const done = frame.status === 'completed'
  const [expanded, setExpanded] = React.useState(false)

  // Completed frames use the authoritative tool_result output (final summary).
  // Running frames stream the partial summary from progress events.
  const summaryText = done ? (frame.output ?? progress?.summaryText) : progress?.summaryText
  const { smoothStream } = useSmoothStreamContext()
  const { displayed: smoothedSummary, guardRef: smoothGuardRef } = useSmoothText(summaryText ?? '', isSummarizing && smoothStream)

  const searchCount = progress?.searchCount ?? 0
  const readCount = progress?.readCount ?? 0
  const statsParts: string[] = []
  if (searchCount > 0) statsParts.push(`Searched ${searchCount} files`)
  if (readCount > 0) statsParts.push(`Read ${readCount} files`)
  const statsText = statsParts.length > 0 ? statsParts.join(', ') : undefined

  return (
    <ToolBodyFrame frame={frame}>
      {isRunning && !isSummarizing && (
        <div className="ai-explore-running">
          {statsText && <span className="ai-explore-stats">{statsText}</span>}
        </div>
      )}

      {isSummarizing && (
        <div className="ai-explore-running">
          <span className="ai-explore-stats">Summarizing</span>
        </div>
      )}

      {activeWork.length > 0 && (
        <div className="ai-explore-work-list">
          {activeWork.slice(-5).map((item, idx) => (
            <ExploreWorkItem
              key={idx}
              type={item.Type}
              target={item.Target}
              countLabel={item.Type === 'search' && searchCount > 0 ? `${searchCount} searches` : undefined}
            />
          ))}
          {activeWork.length > 5 && (
            <div className="ai-explore-work-item ai-explore-work-more">
              ... and {activeWork.length - 5} more
            </div>
          )}
        </div>
      )}

      {smoothedSummary && isRunning && (
        <div ref={smoothGuardRef}>
          <ToolCodeBlock maxLines={5} running={true} smoothScroll={smoothStream}>{smoothedSummary}</ToolCodeBlock>
        </div>
      )}

      {summaryText && !isRunning && (
        <div className="ai-explore-summary-wrapper">
          <div className={`ai-text-content ${expanded ? '' : 'ai-explore-summary-collapsed'}`}>
            <Markdown remarkPlugins={[remarkGfm]}>{summaryText}</Markdown>
          </div>
          <button
            type="button"
            className="ai-tool-code-expander"
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? 'collapse' : '… expand'}
          </button>
        </div>
      )}
    </ToolBodyFrame>
  )
}
