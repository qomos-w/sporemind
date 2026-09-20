import React from 'react'
import { ChevronDown, ChevronUp, Loader2, CheckCircle2, AlertCircle, MessageSquare, X } from 'lucide-react'
import type { TurnEnvelope, Frame, ToolFrame, TextFrame } from '../model/frame-types'
import { buildToolDisplay } from './parts/tool-display'
import { useI18n } from '../../../i18n'
import { CompactMessageStream } from './CompactMessageStream'
import { PeekInteractionBar } from './PeekInteractionBar'
import { extractPendingInteraction } from '../model/pending-interaction'
import { ResizeHandle } from '../../components/ResizeHandle'

// NOTE: This component does NOT render its own card (border/background/border-radius).
// The parent ComposerFrame provides the drawer card styling.
// This keeps peek bar + expanded content as pure content.

export interface MessageDrawerProps {
  envelopes: TurnEnvelope[]
  isStreaming?: boolean
  expanded: boolean
  onToggle: () => void
  onFrameSelect?: (frame: Frame) => void
  onCancelPendingSubmit?: (clientId: string) => void
  /** Desktop-only: allow dragging the top edge of the expanded drawer to
   *  resize the message stream height. */
  resizable?: boolean
  /** Persisted stream height (px). When unset, the CSS default (50vh) applies. */
  streamHeight?: number
  onStreamHeightChange?: (height: number) => void
}

// ── Peek content extraction ──

interface PeekInfo {
  icon: React.ComponentType<{ size?: number; className?: string }>
  title: string
  subtitle?: string
  running: boolean
}

function extractPeekInfo(envelopes: TurnEnvelope[], isStreaming: boolean, t: (key: any) => string): PeekInfo | null {
  if (envelopes.length === 0) return null

  // Walk backwards from the most recent envelope; pick the latest meaningful activity
  // (user message, assistant tool call, assistant text, or error).
  for (let i = envelopes.length - 1; i >= 0; i--) {
    const env = envelopes[i]!

    if (env.role === 'user') {
      if (env.userContent) {
        return {
          icon: MessageSquare,
          title: env.userContent.length > 60 ? env.userContent.slice(0, 60) + '…' : env.userContent,
          running: false,
        }
      }
      continue
    }

    if (env.role === 'assistant' && env.frames.length > 0) {
      const visibleFrames = env.frames.filter(f => {
        if (f.type === 'text') return !!(f as TextFrame).content?.trim()
        if (f.type === 'permission_request') return (f as any).allowed !== true
        return ['text', 'tool', 'error'].includes(f.type)
      })
      if (visibleFrames.length === 0) continue

      const lastFrame = visibleFrames[visibleFrames.length - 1]!

      if (lastFrame.type === 'tool') {
        const tf = lastFrame as ToolFrame
        const display = buildToolDisplay(tf.toolName, tf.input, t)
        return {
          icon: display.icon,
          title: display.title,
          subtitle: display.subtitle,
          running: tf.status === 'running' || tf.status === 'pending',
        }
      }
      if (lastFrame.type === 'text') {
        const text = (lastFrame as TextFrame).content
        return {
          icon: CheckCircle2,
          title: text.length > 60 ? text.slice(0, 60) + '…' : text,
          running: isStreaming && env.completed !== true,
        }
      }
      if (lastFrame.type === 'error') {
        return {
          icon: AlertCircle,
          title: (lastFrame as any).message ?? 'Error',
          running: false,
        }
      }
    }
  }

  return null
}

// ── Component ──

export const MessageDrawer: React.FC<MessageDrawerProps> = ({
  envelopes,
  isStreaming = false,
  expanded,
  onToggle,
  onFrameSelect,
  onCancelPendingSubmit,
  resizable = false,
  streamHeight,
  onStreamHeightChange,
}) => {
  const { t } = useI18n()
  const streamWrapRef = React.useRef<HTMLDivElement>(null)
  const peek = extractPeekInfo(envelopes, isStreaming, t)
  const PeekIcon = peek?.icon ?? CheckCircle2
  const pendingInteraction = React.useMemo(() => extractPendingInteraction(envelopes), [envelopes])
  const pendingSubmits = envelopes.flatMap(envelope => envelope.metadata?.pendingSubmits ?? [])

  const showEmpty = envelopes.length === 0
  const latestEnvelope = envelopes[envelopes.length - 1]
  const turnState = latestEnvelope?.metadata?.turnState
  const retryCount = latestEnvelope?.metadata?.retryCount
  const retryMax = latestEnvelope?.metadata?.retryMax ?? retryCount
  const retryError = latestEnvelope?.metadata?.retryError
  const statusText = turnState === 'failed'
    ? t('ai.step.error')
    : retryCount && retryCount > 0
      ? retryError
        ? `${t('ai.step.retrying', { retry: retryCount, max: retryMax! })} — ${retryError}`
        : t('ai.step.retrying', { retry: retryCount, max: retryMax! })
      : isStreaming
        ? t('ai.step.thinking')
        : t('ai.step.outcome.success')

  return (
    <div className={`message-drawer ${expanded ? 'expanded' : 'collapsed'} ${isStreaming ? 'streaming' : ''}`}>
      {!expanded && pendingInteraction && !showEmpty && (
        <PeekInteractionBar interaction={pendingInteraction} onExpand={onToggle} />
      )}
      {!expanded && !(pendingInteraction && !showEmpty) && (
        <div
          className="message-drawer-peek"
          onClick={showEmpty ? undefined : onToggle}
          role={showEmpty ? undefined : 'button'}
          aria-label={showEmpty ? undefined : 'Expand drawer'}
        >
        <div className="message-drawer-peek-content">
          {showEmpty ? (
            <>
              <CheckCircle2 size={13} className="message-drawer-peek-icon" />
              <span className="message-drawer-peek-title">No messages</span>
            </>
          ) : (
            <>
              {isStreaming && !showEmpty && (
                <span className="message-drawer-peek-running-dot" aria-label="running" />
              )}
              {peek?.running ? (
                <Loader2 size={13} className="message-drawer-peek-spinner" />
              ) : (
                <PeekIcon size={13} className="message-drawer-peek-icon" />
              )}
              <span className="message-drawer-peek-title">{peek?.title ?? 'No activity'}</span>
              {peek?.subtitle && (
                <span className="message-drawer-peek-subtitle">{peek.subtitle}</span>
              )}
            </>
          )}
        </div>
        {!showEmpty && (
          <div className="message-drawer-peek-chevron">
            <ChevronUp size={14} />
          </div>
        )}
        </div>
      )}
      {expanded && (
        <>
          {resizable && (
            <ResizeHandle
              direction="vertical"
              className="message-drawer-resize-handle"
              targetRef={streamWrapRef}
              minSize={120}
              negateDelta
              onResize={(size) => onStreamHeightChange?.(size)}
              onDragStateChange={(dragging) => {
                // While dragging, lift the max-height cap (persisted px or the
                // 50vh CSS default) so the wrapper can grow past its previous cap.
                const el = streamWrapRef.current
                if (!el) return
                el.style.maxHeight = dragging ? 'none' : (streamHeight ? `${streamHeight}px` : '')
              }}
            />
          )}
          <button
            type="button"
            className="message-drawer-collapse"
            onClick={onToggle}
            aria-label="Collapse drawer"
          >
            <ChevronDown size={14} />
          </button>
          <div
            ref={streamWrapRef}
            className={`message-drawer-stream-wrap${resizable ? ' resizable' : ''}`}
            style={resizable && streamHeight ? { maxHeight: `${streamHeight}px` } : undefined}
          >
            <CompactMessageStream
              envelopes={envelopes}
              isStreaming={isStreaming}
              onFrameSelect={onFrameSelect}
              expanded={expanded}
            />
          </div>
        </>
      )}
      {pendingSubmits.length > 0 && (
        <div className="ai-pending-submits message-drawer-pending-submits">
          {pendingSubmits.map(pendingSubmit => (
            <div key={pendingSubmit.id} className="ai-pending-submit-item">
              {onCancelPendingSubmit && (
                <button
                  type="button"
                  className="ai-pending-submit-cancel"
                  title={t('ai.turn.cancelPending')}
                  onClick={event => {
                    event.stopPropagation()
                    onCancelPendingSubmit(pendingSubmit.id)
                  }}
                >
                  <X size={12} />
                </button>
              )}
              <span className="ai-pending-submit-text">{pendingSubmit.text}</span>
            </div>
          ))}
        </div>
      )}
      {expanded && (
        <div className={`message-drawer-status${turnState === 'failed' ? ' error' : ''}`} role="status">
          {isStreaming && turnState !== 'failed' && !(retryCount && retryCount > 0) ? (
            <span className="message-drawer-thinking-dots" aria-label={statusText}>
              {[0, 1, 2, 3, 4].map(i => (
                <span key={i} className="message-drawer-thinking-dot" />
              ))}
            </span>
          ) : statusText}
        </div>
      )}
    </div>
  )
}
