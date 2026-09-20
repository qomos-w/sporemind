import React, { useContext } from 'react'
import { Copy, CornerDownRight } from 'lucide-react'
import type { Frame, TurnEnvelope } from '../model/frame-types'
import type { SlotEntry } from '../model/frame-types'
import { ViewportObserverProvider } from '../hooks/useViewportAware'
import { ViewportSlot } from './ViewportSlot'
import { FrameRenderer } from './parts/FrameRenderer'
import { ErrorBoundary } from './ErrorBoundary'
import { TurnTail } from './TurnTail'
import { isTerminalTurnState } from '../hooks/projection'
import { envelopeHasPendingInteraction } from '../model/pending-interaction'
import { TimelineStep, formatTimestamp, formatTimestampFull, connectorModeForFrame, AssistantTurnFold } from './timeline'
import { layoutAssistantFrames } from './MessageStream.util'
import { AIStepText } from './parts/AIStepText'
import { SystemContinuationBubble } from './parts/SystemContinuationBubble'
import { SenderAvatar } from './SenderAvatar'
import { AIShellContext } from '../context/AIShellContext'
import { setClipboardText } from '../../../application/wails-bridge'
import { useI18n } from '../../../i18n/provider'
import './AIConversationPage.css'

// ── Inline helpers ──

function isAssistantTurnComplete(envelope: TurnEnvelope): boolean {
  return envelope.completed === true
}

function isEnvelopeEffectivelyComplete(envelope: TurnEnvelope): boolean {
  return envelope.completed === true || isTerminalTurnState(envelope.metadata?.turnState)
}


// ── EnvelopeSlot: memoized per-envelope renderer ──

interface EnvelopeSlotProps {
  envelope: TurnEnvelope
  slots: Map<string, SlotEntry>
  onFrameSelect?: (frame: Frame) => void
  onSaveToCard?: (content: string) => void
  agentActorId?: string
  onCancelPendingSubmit?: (clientId: string) => void
  isLastAssistant?: boolean
}

/** Skip re-render when the envelope's rendering-relevant content is unchanged.
 *
 *  Envelope objects are rebuilt by the projection on every notify during
 *  streaming (computeEnvelopes re-spreads every envelope), so reference
 *  equality never holds and React.memo was effectively disabled — with a
 *  multi-thousand-step conversation the whole timeline re-rendered on every
 *  rAF flush and froze the renderer. Frame objects themselves ARE stable for
 *  closed turns (per-step frame cache in steps-to-envelopes), so comparing
 *  frame element references plus the metadata scalars that drive rendering
 *  gives a cheap, sound "nothing visual changed" check. */
export function sameEnvelopeRender(a: TurnEnvelope, b: TurnEnvelope): boolean {
  if (a === b) return true
  if (a.id !== b.id) return false
  if (a.clientKey !== b.clientKey) return false
  if (a.role !== b.role) return false
  if (a.completed !== b.completed) return false
  if (a.seq !== b.seq) return false
  if (a.turnSeq !== b.turnSeq) return false
  if (a.idx !== b.idx) return false
  if (a.isInject !== b.isInject) return false
  if (a.systemOrigin !== b.systemOrigin) return false
  if (a.timestamp !== b.timestamp) return false
  if (a.userContent !== b.userContent) return false
  if (a.goal !== b.goal) return false
  if (a.userImages?.length !== b.userImages?.length) return false
  if (a.userAttachments?.length !== b.userAttachments?.length) return false
  if (a.fileChanges !== b.fileChanges) return false
  if (a.tasks !== b.tasks) return false
  const am = a.metadata
  const bm = b.metadata
  if (am || bm) {
    if (!am || !bm) return false
    if (
      am.turnId !== bm.turnId ||
      am.turnState !== bm.turnState ||
      am.error !== bm.error ||
      am.retryCount !== bm.retryCount ||
      am.retryMax !== bm.retryMax ||
      am.retryError !== bm.retryError ||
      am.loopState !== bm.loopState ||
      am.model !== bm.model ||
      am.actionCount !== bm.actionCount ||
      am.elapsedSeconds !== bm.elapsedSeconds ||
      am.startedAt !== bm.startedAt ||
      am.completedAt !== bm.completedAt ||
      am.worktreeId !== bm.worktreeId
    ) return false
    if (am.usage !== bm.usage) {
      const au = am.usage
      const bu = bm.usage
      if (!au || !bu) return false
      if (
        au.inputTokens !== bu.inputTokens ||
        au.outputTokens !== bu.outputTokens ||
        au.totalTokens !== bu.totalTokens
      ) return false
    }
    if (am.contextBudget !== bm.contextBudget) return false
    if (am.pendingSubmits !== bm.pendingSubmits) return false
    if (am.requestStats !== bm.requestStats) return false
  }
  if (a.frames.length !== b.frames.length) return false
  for (let i = 0; i < a.frames.length; i++) {
    if (a.frames[i] !== b.frames[i]) return false
  }
  return true
}

function envelopeSlotPropsEqual(prev: EnvelopeSlotProps, next: EnvelopeSlotProps): boolean {
  if (!sameEnvelopeRender(prev.envelope, next.envelope)) {
    diagnoseEnvRerender(prev.envelope, next.envelope)
    return false
  }
  if (prev.onCancelPendingSubmit !== next.onCancelPendingSubmit) return false
  if (prev.isLastAssistant !== next.isLastAssistant) return false
  if (prev.slots === next.slots) return true
  const frames = next.envelope.frames
  for (let i = 0; i < frames.length; i++) {
    const fid = frames[i]!.id
    const previousSlot = prev.slots.get(fid)
    const nextSlot = next.slots.get(fid)
    // A new frame enters the registry at version 1. Treating a missing entry
    // as version 1 hides that append when the envelope is updated in place.
    if (!previousSlot || !nextSlot || previousSlot.version !== nextSlot.version) return false
  }
  return true
}

// [jitter-diag] temporary instrumentation: catch re-renders whose frames are
// all element-identical (no content streamed) — these are metadata/state flips
// that change layout (fold, TurnTail) and are the suspected occasional jump.
let envRerenderLogAt = 0
function diagnoseEnvRerender(a: TurnEnvelope, b: TurnEnvelope): void {
  if (a.frames.length !== b.frames.length) return
  for (let i = 0; i < a.frames.length; i++) {
    if (a.frames[i] !== b.frames[i]) return
  }
  const now = Date.now()
  if (now - envRerenderLogAt < 200) return
  envRerenderLogAt = now
  const am = a.metadata
  const bm = b.metadata
  const fields: string[] = []
  if (a.completed !== b.completed) fields.push('completed')
  if (a.userContent !== b.userContent) fields.push('userContent')
  if (a.fileChanges !== b.fileChanges) fields.push('fileChanges')
  if (a.tasks !== b.tasks) fields.push('tasks')
  if (am && bm) {
    if (am.turnState !== bm.turnState) fields.push(`turnState:${bm.turnState ?? ''}`)
    if (am.elapsedSeconds !== bm.elapsedSeconds) fields.push('elapsedSeconds')
    if (am.usage !== bm.usage) fields.push('usage')
    if (am.contextBudget !== bm.contextBudget) fields.push('contextBudget')
    if (am.pendingSubmits !== bm.pendingSubmits) fields.push('pendingSubmits')
    if (am.requestStats !== bm.requestStats) fields.push('requestStats')
  }
  console.info(`[jitter-diag] env-rerender envId=${b.id} fields=${fields.join(',') || 'other'}`)
}

const EnvelopeSlot: React.FC<EnvelopeSlotProps> = ({
  envelope,
  slots,
  onFrameSelect,
  onSaveToCard,
  agentActorId,
  onCancelPendingSubmit,
  isLastAssistant = true,
}) => {
  const { t } = useI18n()
  const shellCtx = useContext(AIShellContext)
  const isActiveAssistant = envelope.role === 'assistant' && !isEnvelopeEffectivelyComplete(envelope)
  // Per-envelope streaming: frames from a completed turn (e.g. when switching
  // agents) should mount at their terminal state, not replay animation. Only
  // frames belonging to a currently-running turn should animate.
  const envelopeTurnStreaming = isActiveAssistant
  // User messages are short, causally important anchors. Virtualizing them can
  // leave a cached-height blank after reload/layout changes, making the last
  // conversational pair appear to vanish even though its projection remains.
  // Envelopes with a pending interaction (plan approval, ask_user, permission)
  // hold unsaved typed input — a virtualization unmount mid-interaction
  // destroys it, so they stay mounted too.
  const alwaysVisible = envelope.role === 'user' || isActiveAssistant || envelopeHasPendingInteraction(envelope)

  const content = envelope.role === 'user' ? (() => {
    if (envelope.systemOrigin) {
      return (
        <div className="ai-system-continuation-row-wrap">
          <SystemContinuationBubble
            kind={envelope.systemOrigin}
            text={envelope.userContent ?? ''}
            meta={envelope.systemOrigin}
            goal={envelope.goal}
          />
        </div>
      )
    }
    if (envelope.isInject) {
      return (
        <div className="ai-inject-peer-row">
          {envelope.peerSender && <SenderAvatar sender={envelope.peerSender} size={20} />}
          <TimelineStep
            slotId={envelope.id}
            status="completed"
            icon={<CornerDownRight size={12} className="ai-step-icon" />}
            label={<span className="ai-step-label">{envelope.peerSender?.name || t('ai.turn.injected')}</span>}
            connectorMode="none"
            expandable
            expansionMode="open"
          >
            <AIStepText className="ai-exploration-text">{envelope.userContent ?? ''}</AIStepText>
          </TimelineStep>
        </div>
      )
    }
    const hasImages = (envelope.userImages?.length ?? 0) > 0
    const hasAttachments = (envelope.userAttachments?.length ?? 0) > 0
    return (
      <div className="ai-user-bubble-row">
        <div className="ai-user-turn">
          {envelope.peerSender && <SenderAvatar sender={envelope.peerSender} size={18} />}
          <div className={`ai-user-bubble ${(hasImages || hasAttachments) ? 'has-media' : ''}`}>
            <AIStepText>{envelope.userContent ?? ''}</AIStepText>
            {hasImages && (
              <div className="ai-user-images">
                {envelope.userImages!.map((img, i) => (
                  <img
                    key={i}
                    src={img.url}
                    alt={img.alt ?? ''}
                    className="ai-user-image"
                    title={t('ai.imageViewer.openInViewer')}
                    onClick={() => shellCtx?.onOpenImage?.(img.url, img.alt)}
                  />
                ))}
              </div>
            )}
            {hasAttachments && (
              <div className="ai-user-attachments">
                {envelope.userAttachments!.map((file, i) => (
                  <span key={i} className="ai-user-attachment-item">
                    {file.name}
                    {file.sizeBytes != null && <span className="ai-attachment-size"> ({(file.sizeBytes / 1024).toFixed(1)} KB)</span>}
                  </span>
                ))}
              </div>
            )}
          </div>
          <div className="ai-user-footer ai-message-footer">
            <div className="ai-message-footer-actions">
              <button
                className="ai-message-footer-icon-btn"
                title="Copy message"
                aria-label="Copy message"
                onClick={() => {
                  void setClipboardText(envelope.userContent ?? '').catch(() => { /* ignore */ })
                }}
              >
                <Copy size={12} />
              </button>
            </div>
            <span className="ai-message-footer-time" title={formatTimestampFull(envelope.timestamp)}>{formatTimestamp(envelope.timestamp)}</span>
          </div>
        </div>
      </div>
    )
  })() : (
    <div className="ai-assistant-turn">
      {(() => {
        const { visible, foldFrames, keptFrames, shouldFold } = layoutAssistantFrames(envelope.frames, isAssistantTurnComplete(envelope))
        return (
          <div className="ai-turn-frames">
            {shouldFold && (
              <AssistantTurnFold
                envelope={envelope}
                frames={foldFrames}
                slots={slots}
                onFrameSelect={onFrameSelect}
                onSaveToCard={onSaveToCard}
                agentActorId={agentActorId}
                turnStreaming={false}
              />
            )}
            {keptFrames.map((frame, i) => {
              const visibleIndex = shouldFold ? visible.length - keptFrames.length + i : i
              const isLastKept = i === keptFrames.length - 1
              const frameStreaming = envelopeTurnStreaming && (frame.status === 'running' || frame.status === 'pending' || isLastKept)
              return (
                <ErrorBoundary key={frame.id}>
                  <FrameRenderer
                    frame={frame}
                    version={slots.get(frame.id)?.version ?? 1}
                    connectorMode={connectorModeForFrame(visible, visibleIndex)}
                    onFrameSelect={onFrameSelect}
                    onSaveToCard={onSaveToCard}
                    agentActorId={agentActorId}
                    turnStreaming={frameStreaming}
                  />
                </ErrorBoundary>
              )
            })}
          </div>
        )
      })()}
      <TurnTail
        envelope={envelope}
        isComplete={isAssistantTurnComplete(envelope)}
        agentActorId={agentActorId}
        onCancelPendingSubmit={onCancelPendingSubmit}
        isLast={isLastAssistant}
      />
    </div>
  )

  return (
    <div data-envelope-id={envelope.id}>
      <ViewportSlot id={envelope.id} alwaysVisible={alwaysVisible}>
        {content}
      </ViewportSlot>
    </div>
  )
}

const MemoizedEnvelopeSlot = React.memo(EnvelopeSlot, envelopeSlotPropsEqual)

// ── Props ──

interface MessageStreamProps {
  envelopes: TurnEnvelope[]
  emptyState?: React.ReactNode
  timelineLoading?: boolean
  streamRef: React.RefObject<HTMLDivElement | null>
  contentRef: React.RefObject<HTMLDivElement | null>

  slots: Map<string, SlotEntry>
  onFrameSelect?: (frame: Frame) => void
  onSaveToCard?: (content: string) => void
  agentActorId?: string
  hasMoreHistory?: boolean
  summarizedBoundary?: boolean
  discardedBoundary?: boolean
  loadingMoreHistory?: boolean
  loadOlderTurns?: () => void
  onCancelPendingSubmit?: (clientId: string) => void
}

const MessageStream: React.FC<MessageStreamProps> = ({
  envelopes,
  emptyState,
  timelineLoading,
  streamRef,
  contentRef,
  slots,
  onFrameSelect,
  onSaveToCard,
  agentActorId,
  hasMoreHistory,
  summarizedBoundary,
  discardedBoundary,
  loadingMoreHistory,
  loadOlderTurns,
  onCancelPendingSubmit,
}) => {
  const { t } = useI18n()
  return (
    <>
      <ViewportObserverProvider scrollContainerRef={streamRef} scopeKey={agentActorId}>
        <div className="ai-message-stream" ref={streamRef}>
          <div ref={contentRef} className="ai-message-content">
            {!timelineLoading && envelopes.length === 0 && emptyState && (
              <div className="ai-conversation-empty-state">
                {emptyState}
              </div>
            )}
            {timelineLoading && (
              <div className="ai-assistant-turn">
                <div className="ai-turn-tail-wrap">
                  <div className="ai-turn-tail">
                    <div className="ai-step ai-step-text">
                      <div className="ai-timeline-loading-card">
                        <span className="ai-timeline-loading-line" style={{ width: '40%' }} />
                        <span className="ai-timeline-loading-line" style={{ width: '65%' }} />
                        <span className="ai-timeline-loading-line" style={{ width: '30%' }} />
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            )}
            {!timelineLoading && (hasMoreHistory || loadingMoreHistory) && loadOlderTurns && (
              <div className="ai-load-more-history">
                <button
                  className="ai-load-more-btn"
                  type="button"
                  onClick={loadOlderTurns}
                  disabled={loadingMoreHistory}
                >
                  {loadingMoreHistory ? t('ai.turn.loading') : t('ai.turn.loadMoreHistory')}
                </button>
              </div>
            )}
            {!timelineLoading && !hasMoreHistory && !loadingMoreHistory && discardedBoundary && (
              <div className="ai-discarded-boundary">
                <span className="ai-discarded-boundary-line" />
                <span className="ai-discarded-boundary-label">{t('ai.turn.discarded')}</span>
                <span className="ai-discarded-boundary-line" />
              </div>
            )}
            {!timelineLoading && !hasMoreHistory && !loadingMoreHistory && summarizedBoundary && (
              <div className="ai-summarized-boundary">
                <span className="ai-summarized-boundary-line" />
                <span className="ai-summarized-boundary-label">{t('ai.turn.summarized')}</span>
                <span className="ai-summarized-boundary-line" />
              </div>
            )}
            {!timelineLoading && (() => {
              let lastAssistantIdx = -1
              for (let i = envelopes.length - 1; i >= 0; i--) {
                if (envelopes[i]!.role === 'assistant') { lastAssistantIdx = i; break }
              }
              return envelopes.map((envelope, i) => (
                <MemoizedEnvelopeSlot
                  key={envelope.clientKey || envelope.id || `env-${i}`}
                  envelope={envelope}
                  slots={slots}
                  onFrameSelect={onFrameSelect}
                  onSaveToCard={onSaveToCard}
                  agentActorId={agentActorId}
                  onCancelPendingSubmit={onCancelPendingSubmit}
                  isLastAssistant={i === lastAssistantIdx}
                />
              ))
            })()}
          </div>
        </div>
      </ViewportObserverProvider>

    </>
  )
}

export default React.memo(MessageStream)
