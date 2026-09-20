import React from 'react'
import { Check, X, CircleAlert } from 'lucide-react'
import type { ToolFrame, SlotStatus } from '../../model/frame-types.ts'
import { ToolBodyFrame, ToolCodeBlock } from './ToolViewPrimitives.tsx'
import { parseJsonObject, firstString } from './tool-display.ts'
import { useI18n } from '../../../../i18n/provider'
import type { I18nKey } from '../../../../i18n/types'

// ── Pure parsing / resolution helpers (unit-testable) ──

export interface AgentReviewData {
  /** Target agent actor id (from tool input). */
  agentActorId?: string
  /** Target task card id (from tool input, optional). */
  taskCardId?: string
  /** Requested decision from input: 'approve' | 'reject'. */
  decision?: string
  /** Rejection feedback text from input (may be long). */
  feedback?: string
  /** Authoritative approval flag from the tool result. */
  approved?: boolean
  /** Resulting card status from the tool result ('done' | 'doing'). */
  cardStatus?: string
}

/**
 * Parse the agent_review tool input + output into a structured shape.
 * Reads AgentActorId / TaskCardId / Decision / Feedback from input and
 * Approved / CardStatus from the result as the final fact.
 */
export function parseAgentReview(input: string, output?: string): AgentReviewData {
  const inObj = parseJsonObject(input)
  const outObj = parseJsonObject(output ?? '')
  return {
    agentActorId: inObj ? firstString(inObj, ['AgentActorId', 'agentActorId']) : undefined,
    taskCardId: inObj ? firstString(inObj, ['TaskCardId', 'taskCardId']) : undefined,
    decision: inObj ? firstString(inObj, ['Decision', 'decision']) : undefined,
    feedback: inObj ? firstString(inObj, ['Feedback', 'feedback']) : undefined,
    approved: outObj && typeof outObj.Approved === 'boolean' ? outObj.Approved : undefined,
    cardStatus: outObj ? firstString(outObj, ['CardStatus', 'cardStatus']) : undefined,
  }
}

/**
 * Resolve the effective approval boolean. The tool result's `Approved` field is
 * the authoritative final fact when present; otherwise we infer from the
 * requested `Decision` (covers running, tool-failure, and old-history frames
 * that lack a structured result).
 */
export function resolveEffectiveApproved(data: AgentReviewData, status: SlotStatus): boolean | undefined {
  if (typeof data.approved === 'boolean') return data.approved
  if (status === 'completed') {
    if (data.decision === 'approve') return true
    if (data.decision === 'reject') return false
  }
  return undefined
}

// ── Component ──

interface AgentReviewToolViewProps {
  frame: ToolFrame
}

/**
 * Feedback overflow detection. CSS `-webkit-line-clamp` clips both by line
 * count and by visual height, so a single very long line is also truncated.
 * We therefore check both the line count and a character threshold, so the
 * expand toggle appears for multi-line AND long single-line feedback alike.
 */
const FEEDBACK_MAX_LINES = 4
const FEEDBACK_MAX_CHARS = 200

export function isFeedbackOverflow(feedback: string | undefined): boolean {
  if (!feedback) return false
  if (feedback.split('\n').length > FEEDBACK_MAX_LINES) return true
  if (feedback.length > FEEDBACK_MAX_CHARS) return true
  return false
}

function statusIcon(approved: boolean | undefined): React.ReactNode {
  if (approved === true) return <Check size={14} className="ai-agent-review-icon" aria-hidden="true" />
  if (approved === false) return <X size={14} className="ai-agent-review-icon" aria-hidden="true" />
  return null
}

export const AgentReviewToolView: React.FC<AgentReviewToolViewProps> = ({ frame }) => {
  const { t } = useI18n()
  // Hooks MUST be called unconditionally before any early return, otherwise a
  // status transition (running → completed → error) changes the number of hooks
  // between renders and violates the Rules of Hooks.
  const [feedbackExpanded, setFeedbackExpanded] = React.useState(false)

  const isRunning = frame.status === 'running'
  const isError = frame.status === 'error'

  const data = parseAgentReview(frame.input, frame.output)
  const approved = resolveEffectiveApproved(data, frame.status)

  // Running: show the reviewing indicator plus the review target.
  if (isRunning) {
    return (
      <ToolBodyFrame frame={frame}>
        <div
          className="ai-agent-review ai-agent-review--running"
          role="status"
          aria-live="polite"
          aria-label={t('ai.tool.agentReview.reviewing')}
        >
          <span className="ai-agent-review-running-label">{t('ai.tool.agentReview.reviewing')}</span>
          {data.agentActorId && (
            <span className="ai-agent-review-target" title={data.agentActorId}>{data.agentActorId}</span>
          )}
        </div>
      </ToolBodyFrame>
    )
  }

  // Error: clear failure indicator + raw error output.
  if (isError) {
    return (
      <ToolBodyFrame frame={frame}>
        <div
          className="ai-agent-review ai-agent-review--failed"
          role="status"
          aria-label={t('ai.tool.agentReview.failed')}
        >
          <CircleAlert size={14} className="ai-agent-review-icon ai-agent-review-icon--error" aria-hidden="true" />
          <span>{t('ai.tool.agentReview.failed')}</span>
        </div>
        {frame.output && <ToolCodeBlock error>{frame.output}</ToolCodeBlock>}
      </ToolBodyFrame>
    )
  }

  // Completed (or non-running terminal): structured review result.
  const statusClass = approved === true
    ? 'approved'
    : approved === false
      ? 'rejected'
      : 'unknown'
  const statusLabel: I18nKey | undefined = approved === true
    ? 'ai.tool.agentReview.approved'
    : approved === false
      ? 'ai.tool.agentReview.rejected'
      : undefined

  const showFeedback = approved === false && !!data.feedback
  const feedbackOverflows = isFeedbackOverflow(data.feedback)

  return (
    <ToolBodyFrame frame={frame}>
      <div
        className={`ai-agent-review ai-agent-review--${statusClass}`}
        role="status"
        aria-label={statusLabel ? t(statusLabel) : undefined}
      >
        <div className="ai-agent-review-header">
          <span className={`ai-agent-review-badge ai-agent-review-badge--${statusClass}`}>
            {statusIcon(approved)}
            {statusLabel && <span>{t(statusLabel)}</span>}
          </span>
          {data.cardStatus && (
            <span className="ai-agent-review-cardstatus" title={data.cardStatus}>{data.cardStatus}</span>
          )}
        </div>

        {(data.agentActorId || data.taskCardId) && (
          <div className="ai-agent-review-details">
            {data.agentActorId && (
              <div className="ai-agent-review-row">
                <span className="ai-agent-review-row-label">{t('ai.tool.agentReview.targetAgent')}</span>
                <span className="ai-agent-review-row-value" title={data.agentActorId}>{data.agentActorId}</span>
              </div>
            )}
            {data.taskCardId && (
              <div className="ai-agent-review-row">
                <span className="ai-agent-review-row-label">{t('ai.tool.agentReview.taskCard')}</span>
                <span className="ai-agent-review-row-value" title={data.taskCardId}>{data.taskCardId}</span>
              </div>
            )}
          </div>
        )}

        {showFeedback && (
          <div className="ai-agent-review-feedback">
            <div className="ai-agent-review-feedback-label">{t('ai.tool.agentReview.feedback')}</div>
            <div className={`ai-agent-review-feedback-text ${feedbackExpanded ? '' : 'ai-agent-review-feedback-collapsed'}`}>
              {data.feedback}
            </div>
            {feedbackOverflows && (
              <button
                type="button"
                className="ai-tool-code-expander"
                aria-expanded={feedbackExpanded}
                onClick={() => setFeedbackExpanded(v => !v)}
              >
                {feedbackExpanded ? t('ai.tool.agentReview.collapse') : t('ai.tool.agentReview.expand')}
              </button>
            )}
          </div>
        )}
      </div>
    </ToolBodyFrame>
  )
}
