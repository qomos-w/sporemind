import React, { useState } from 'react'
import { RefreshCw, Waypoints, ChevronDown, ChevronRight } from 'lucide-react'
import { useI18n } from '../../../../i18n'
import { AIStepText } from './AIStepText'
import { summarizeSystemPrompt } from '../../model/system-prompt-summary'
import type { SessionGoal } from '../../model/frame-types'

/** Collapsed-by-default bubble for system-originated continuation
 *  messages (Meta=goal / Meta=workflow). Visually much lighter than a
 *  user bubble: small icon + kind label + one-line summary; click to
 *  expand the full original text. */
export const SystemContinuationBubble: React.FC<{
  /** Classification from envelope.systemOrigin ('goal' | 'workflow'). */
  kind: 'goal' | 'workflow'
  /** Full original message text. */
  text: string
  /** Raw meta value, used for summary extraction (same vocabulary as kind). */
  meta?: string
  /** Session goal, injected by projection; goal bubbles show its name. */
  goal?: SessionGoal
}> = ({ kind, text, meta, goal }) => {
  const { t } = useI18n()
  const [expanded, setExpanded] = useState(false)
  const { summary } = summarizeSystemPrompt(meta, text)

  const Icon = kind === 'goal' ? RefreshCw : Waypoints
  const label = kind === 'goal' ? t('ai.system.goalContinue') : t('ai.system.workflowUpdate')
  const goalName = goal?.InterpretedGoal?.trim() || goal?.Condition?.trim() || ''

  return (
    <div className={`ai-system-continuation ai-system-continuation-${kind}`} data-expanded={expanded}>
      <button
        type="button"
        className="ai-system-continuation-row"
        onClick={() => setExpanded(v => !v)}
        aria-expanded={expanded}
        aria-label={t('ai.system.expand')}
        title={t('ai.system.expand')}
      >
        <span className="ai-system-continuation-icon">
          <Icon size={12} />
        </span>
        <span className="ai-system-continuation-label">{label}</span>
        {kind === 'workflow' && (
          <span className="ai-system-continuation-summary">{summary}</span>
        )}
        {kind === 'goal' && goalName && (
          <span className="ai-system-continuation-summary">{goalName}</span>
        )}
        <span className="ai-system-continuation-toggle">
          {expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </span>
      </button>
      {expanded && (
        <div className="ai-system-continuation-body">
          <AIStepText className="ai-system-continuation-text">{text}</AIStepText>
        </div>
      )}
    </div>
  )
}

export default SystemContinuationBubble