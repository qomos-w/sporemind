import React, { type ReactNode } from 'react'
import type { ToolFrame } from '../../model/frame-types.ts'
import { DiffBlock } from './DiffBlock.tsx'
import { useRunningScrollFollow } from './useRunningScrollFollow'

/** Shared running-state indicator used by tool views while waiting for output. */
export const ToolRunning: React.FC<{ label?: ReactNode }> = ({ label }) => (
  <div className="ai-tool-running">
    {label ?? 'Running...'}
  </div>
)

interface ToolSummaryRowProps {
  label: string
  value: string
  title?: string
}

export const ToolSummaryRow: React.FC<ToolSummaryRowProps> = ({ label, value, title }) => {
  return (
    <div className="ai-tool-summary-row">
      <span className="ai-tool-summary-label">{label}</span>
      <span className="ai-tool-summary-value" title={title ?? value}>{value}</span>
    </div>
  )
}

interface ToolSectionProps {
  label: React.ReactNode
  children: React.ReactNode
}

export const ToolSection: React.FC<ToolSectionProps> = ({ label, children }) => {
  return (
    <div className="ai-tool-section">
      <div className="ai-tool-section-label">{label}</div>
      {children}
    </div>
  )
}

interface ToolCodeBlockProps {
  terminal?: boolean
  error?: boolean
  maxLines?: number
  summary?: string
  running?: boolean
  /** When true and `running`, the tail follows new content with the
   *  damped-spring easing shared with the outer stream instead of snapping.
   *  Defaults to false (the original instant-snap behavior). */
  smoothScroll?: boolean
  children: React.ReactNode
}

// Pull a plain-string payload out of children when children is either a string
// or a list whose non-falsy items are all strings. Returns null otherwise so
// the fold path is skipped for mixed React content (e.g. terminal cursor span).
function extractStringChildren(children: React.ReactNode): string | null {
  if (typeof children === 'string') return children
  if (Array.isArray(children)) {
    const parts: string[] = []
    for (const c of children) {
      if (typeof c === 'string') parts.push(c)
      else if (c === null || c === undefined || c === false || c === true) continue
      else return null
    }
    return parts.join('')
  }
  return null
}

export const ToolCodeBlock: React.FC<ToolCodeBlockProps> = ({
  terminal = false,
  error = false,
  maxLines = 15,
  summary,
  running = false,
  smoothScroll = false,
  children,
}) => {
  const [expanded, setExpanded] = React.useState(false)
  const scrollRef = React.useRef<HTMLPreElement>(null)
  const baseCls = ['ai-tool-code', terminal ? 'terminal' : '', error ? 'error' : ''].filter(Boolean).join(' ')

  const text = extractStringChildren(children)

  // Suppress the wrapper entirely when children is a string-like payload that
  // contains only whitespace (spaces, tabs, newlines). Mixed React children
  // keep their existing behavior because we cannot reliably trim them.
  const textTrimmed = text == null ? null : text.trim()
  const hasContent = textTrimmed === null || textTrimmed.length > 0

  // Running mode: fixed height, auto-scroll to bottom (like a terminal tail).
  // With smoothing on the follow glides via the damped-spring shared with the
  // outer stream; otherwise it snaps (the original instant behavior).
  useRunningScrollFollow(scrollRef, text ?? '', running, smoothScroll)

  if (running) {
    if (!hasContent) return null
    return (
      <div className="ai-tool-code-wrapper">
        <pre ref={scrollRef} className={`${baseCls} running-scroll`}>{children}</pre>
      </div>
    )
  }

  if (text == null) {
    return <pre className={baseCls}>{children}</pre>
  }

  if (textTrimmed === '') {
    return null
  }

  const lines = text.split('\n')
  const total = lines.length
  if (total <= maxLines) {
    return <pre className={baseCls}>{text}</pre>
  }

  if (!expanded) {
    const visible = lines.slice(0, maxLines).join('\n')
    const hidden = total - maxLines
    const label = summary ?? `+${hidden} more lines`
    return (
      <div className="ai-tool-code-wrapper">
        <pre className={`${baseCls} collapsed`}>{visible}</pre>
        <button
          type="button"
          className="ai-tool-code-expander"
          onClick={() => setExpanded(true)}
        >
          … {label} (click to expand)
        </button>
      </div>
    )
  }

  return (
    <div className="ai-tool-code-wrapper">
      <pre className={baseCls}>{text}</pre>
      <button
        type="button"
        className="ai-tool-code-expander"
        onClick={() => setExpanded(false)}
      >
        collapse
      </button>
    </div>
  )
}

interface ToolBodyFrameProps {
  frame: ToolFrame
  children: React.ReactNode
}

export const ToolBodyFrame: React.FC<ToolBodyFrameProps> = ({ frame, children }) => {
  return <div className={`ai-tool-body ${frame.status === 'error' ? 'error' : ''}`}>{children}</div>
}

export function renderDiffSection(frame: ToolFrame): React.ReactNode {
  const diffContent = frame.fileChanges?.find(change => change.diffContent)?.diffContent
  if (!diffContent) return null

  return (
    <ToolSection label="Diff">
      <DiffBlock diffContent={diffContent} />
    </ToolSection>
  )
}

/** Fallback: render raw output when parsing fails and the tool is not running. */
export const ToolFallbackOutput: React.FC<{ frame: ToolFrame; parsed: unknown }> = ({ frame, parsed }) => {
  if (frame.status === 'running' || !frame.output || parsed != null) return null
  return (
    <ToolSection label={frame.status === 'error' ? 'Error' : 'Output'}>
      <ToolCodeBlock error={frame.status === 'error'}>{frame.output}</ToolCodeBlock>
    </ToolSection>
  )
}
