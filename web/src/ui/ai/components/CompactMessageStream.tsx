import React from 'react'
import { CheckCircle2, AlertCircle, Loader2, XCircle } from 'lucide-react'
import type { TurnEnvelope, Frame, TextFrame, ToolFrame, PlanFrame, AskUserQuestionFrame, PermissionRequestFrame, GoalSubmitFrame, GoalCardSubmitFrame } from '../model/frame-types'
import { buildToolDisplay } from './parts/tool-display'
import { parseEditOutput } from '../hooks/tool-parsers'
import type { SlotStatus } from '../model/frame-types'
import { useI18n } from '../../../i18n'
import { AIStepText } from './parts/AIStepText'
import { AskUserQuestionBlock } from './parts/AskUserQuestionBlock'
import { PermissionRequestBlock } from './parts/PermissionRequestBlock'
import { GoalSubmitView } from './parts/GoalSubmitView'
import { GoalCardSubmitView } from './parts/GoalCardSubmitView'
import { SenderAvatar } from './SenderAvatar'
import { PlanBlock } from './parts/PlanBlock'
import { SystemContinuationBubble } from './parts/SystemContinuationBubble'
import './CompactMessageStream.css'

// ── Types ──

export interface CompactMessageStreamProps {
  envelopes: TurnEnvelope[]
  isStreaming?: boolean
  onFrameSelect?: (frame: Frame) => void
  /** Maximum number of envelopes to render (most recent). Default: 50. */
  maxEnvelopes?: number
  /** Scroll to bottom when the drawer becomes expanded. */
  expanded?: boolean
}

// ── Status icon helper ──

function StatusIcon({ status, size = 12 }: { status: SlotStatus; size?: number }) {
  switch (status) {
    case 'running':
    case 'pending':
      return <Loader2 size={size} className="compact-tool-spinner" />
    case 'completed':
      return <CheckCircle2 size={size} className="compact-tool-check" />
    case 'error':
      return <AlertCircle size={size} className="compact-tool-error" />
    case 'cancelled':
      return <XCircle size={size} className="compact-tool-cancelled" />
    default:
      return null
  }
}

/** Edit-kind frames: derive +/− stats from the structured edit output, falling
 * back to fileChanges. parseEditOutput coerces missing counts to 0, so only
 * nonzero output stats win; zero/zero falls through (no badges shown). */
function getEditStats(frame: ToolFrame): { additions: number; deletions: number } | null {
  const editOut = frame.output ? parseEditOutput(frame.output) : null
  if (editOut && (editOut.Additions > 0 || editOut.Deletions > 0)) {
    return { additions: editOut.Additions, deletions: editOut.Deletions }
  }
  if (frame.fileChanges && frame.fileChanges.length > 0) {
    const additions = frame.fileChanges.reduce((sum, c) => sum + (c.additions ?? 0), 0)
    const deletions = frame.fileChanges.reduce((sum, c) => sum + (c.deletions ?? 0), 0)
    if (additions > 0 || deletions > 0) return { additions, deletions }
  }
  return null
}

// ── Tool call bubble (title-only) ──

interface CompactToolBubbleProps {
  frame: ToolFrame
  onFrameSelect?: (frame: Frame) => void
}

const CompactToolBubble: React.FC<CompactToolBubbleProps> = ({ frame, onFrameSelect }) => {
  const { t } = useI18n()
  const display = buildToolDisplay(frame.toolName, frame.input, t)
  const Icon = display.icon
  const editStats = display.kind === 'edit' ? getEditStats(frame) : null

  return (
    <div
      className={`compact-tool-bubble compact-tool-${display.kind} compact-tool-${frame.status}`}
      onClick={onFrameSelect ? () => onFrameSelect(frame) : undefined}
      role={onFrameSelect ? 'button' : undefined}
    >
      <div className="compact-tool-row">
        <Icon size={13} className="compact-tool-icon" />
        <span className="compact-tool-title">{display.title}</span>
        {display.subtitle && (
          <span className="compact-tool-subtitle">{display.subtitle}</span>
        )}
        {editStats && (
          <span className="compact-tool-stats">
            {editStats.additions > 0 && (
              <span className="compact-tool-stat add">+{editStats.additions}</span>
            )}
            {editStats.deletions > 0 && (
              <span className="compact-tool-stat del">−{editStats.deletions}</span>
            )}
          </span>
        )}
        {frame.status === 'completed' && frame.durationSeconds != null && (
          <span className="compact-tool-duration">{frame.durationSeconds}s</span>
        )}
        <StatusIcon status={frame.status} />
      </div>
    </div>
  )
}

// ── Assistant text bubble ──

const CompactTextBubble: React.FC<{ content: string }> = ({ content }) => (
  <div className="compact-text-bubble">
    <AIStepText>{content}</AIStepText>
  </div>
)

// ── Main component ──

const CompactMessageStreamInner: React.FC<CompactMessageStreamProps> = ({
  envelopes,
  isStreaming,
  onFrameSelect,
  maxEnvelopes = 50,
  expanded,
}) => {
  const scrollRef = React.useRef<HTMLDivElement>(null)

  // Auto-scroll to bottom on new content. Envelope objects are rebuilt by the
  // projection on every session notify (identical content, new references —
  // e.g. each transport reconnect's summary reconcile), so reference identity
  // must not trigger a scroll: gate the idle path on the envelope list
  // actually changing, and keep the unconditional follow while streaming.
  const idleSigRef = React.useRef<string | null>(null)
  const wasStreamingRef = React.useRef(false)
  React.useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const last = envelopes[envelopes.length - 1]
    const idleSig = `${envelopes.length}|${last?.clientKey ?? last?.id ?? ''}|${last?.frames.length ?? 0}`
    const streamingStarted = !!isStreaming && !wasStreamingRef.current
    wasStreamingRef.current = !!isStreaming
    if (!isStreaming && !streamingStarted && idleSigRef.current === idleSig) return
    idleSigRef.current = idleSig
    el.scrollTop = el.scrollHeight
  }, [envelopes, isStreaming])

  // When the drawer is expanded, scroll to the bottom so the latest messages are visible.
  // Use useLayoutEffect to set the scroll position before paint, and temporarily disable
  // smooth scrolling so the jump is immediate rather than animated.
  React.useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el || !expanded) return
    const original = el.style.scrollBehavior
    el.style.scrollBehavior = 'auto'
    el.scrollTop = el.scrollHeight
    el.style.scrollBehavior = original
  }, [expanded])

  // Trim to most recent N envelopes
  const visible = envelopes.length > maxEnvelopes
    ? envelopes.slice(-maxEnvelopes)
    : envelopes

  return (
    <div className="compact-message-stream" ref={scrollRef}>
      {visible.length === 0 && (
        <div className="compact-message-empty">No messages yet</div>
      )}
      {visible.map((env, i) => {
        const key = env.clientKey ?? env.id ?? `env-${i}`

        if (env.role === 'user') {
          if (env.systemOrigin) {
            return (
              <div className="compact-bubble-row compact-system-continuation-row" key={key}>
                <SystemContinuationBubble
                  kind={env.systemOrigin}
                  text={env.userContent ?? ''}
                  meta={env.systemOrigin}
                  goal={env.goal}
                />
              </div>
            )
          }
          return (
            <div className="compact-bubble-row compact-user-row" key={key}>
              <div className="compact-user-turn">
                {env.peerSender && <SenderAvatar sender={env.peerSender} size={16} />}
                <div className="compact-user-bubble">
                  <AIStepText>{env.userContent ?? ''}</AIStepText>
                </div>
              </div>
            </div>
          )
        }

        // Assistant: iterate frames, render each as a bubble
        return (
          <div className="compact-assistant-group" key={key}>
            {env.frames
              .filter((f): f is Frame => {
                // Skip empty text frames
                if (f.type === 'text') {
                  return !!(f as TextFrame).content?.trim()
                }
                // Skip auto-approved permission frames
                if (f.type === 'permission_request') {
                  return (f as any).allowed !== true
                }
                // Skip reasoning, compaction, attachments, sources, ui, image, skill_use
                // Show text, tool, error, plan + blocking interactions
                return ['text', 'tool', 'error', 'plan', 'ask_user', 'permission_request', 'goal_submit', 'goal_card_submit'].includes(f.type)
              })
              .map((frame) => {
                if (frame.type === 'tool') {
                  return (
                    <CompactToolBubble
                      key={frame.id}
                      frame={frame as ToolFrame}
                      onFrameSelect={onFrameSelect}
                    />
                  )
                }
                if (frame.type === 'text') {
                  return (
                    <CompactTextBubble
                      key={frame.id}
                      content={(frame as TextFrame).content}
                    />
                  )
                }
                if (frame.type === 'error') {
                  return (
                    <CompactTextBubble
                      key={frame.id}
                      content={`⚠️ ${(frame as any).message}`}
                    />
                  )
                }
                if (frame.type === 'plan') {
                  const pf = frame as PlanFrame
                  if (pf.approvalStatus === 'pending') {
                    return (
                      <PlanBlock
                        key={frame.id}
                        frame={pf}
                      />
                    )
                  }
                  return (
                    <CompactTextBubble
                      key={frame.id}
                      content={`📋 ${pf.content?.slice(0, 200) ?? ''}`}
                    />
                  )
                }
                if (frame.type === 'ask_user') {
                  return (
                    <AskUserQuestionBlock
                      key={frame.id}
                      frame={frame as AskUserQuestionFrame}
                      onFrameSelect={onFrameSelect}
                    />
                  )
                }
                if (frame.type === 'permission_request') {
                  return (
                    <PermissionRequestBlock
                      key={frame.id}
                      frame={frame as PermissionRequestFrame}
                    />
                  )
                }
                if (frame.type === 'goal_submit') {
                  return (
                    <GoalSubmitView
                      key={frame.id}
                      frame={frame as GoalSubmitFrame}
                    />
                  )
                }
                if (frame.type === 'goal_card_submit') {
                  return (
                    <GoalCardSubmitView
                      key={frame.id}
                      frame={frame as GoalCardSubmitFrame}
                    />
                  )
                }
                return null
              })}
          </div>
        )
      })}
    </div>
  )
}

export const CompactMessageStream = React.memo(CompactMessageStreamInner)
