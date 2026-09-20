import React, { useCallback, useEffect, useLayoutEffect, useRef, useSyncExternalStore } from 'react'
import type { Frame, TurnEnvelope } from '../model/frame-types'
import type { ProjectSnapshot } from '../../../domain/types'
import { useSlotRegistry } from '../hooks/useSlotRegistry'
import { clearViewportHeightCache } from '../hooks/useViewportAware'
import { useScrollLock } from '../hooks/useScrollLock'
import MessageStream from './MessageStream'
import { gitStore } from '../../panels/git-store'
import { FileReferenceProvider } from './parts/file-reference.tsx'
import type { AgentInfo } from '../hooks/agentInfoStore'
import './timeline/timeline.css'
import './parts/parts.css'
import './AIConversationPage.css'

interface AIConversationPageProps {
  envelopes: TurnEnvelope[]
  emptyState?: React.ReactNode
  isStreaming: boolean
  smoothStream?: boolean
  timelineLoading?: boolean
  onFrameSelect?: (frame: Frame) => void
  onSaveToCard?: (content: string) => void
  activeAgent?: AgentInfo | null
  activeProjectId?: string | null
  projects?: ProjectSnapshot[]
  hasMoreHistory?: boolean
  summarizedBoundary?: boolean
  discardedBoundary?: boolean
  loadingMoreHistory?: boolean
  loadOlderTurns?: () => void
  onCancelPendingSubmit?: (clientId: string) => void
  /** Active conversation id — used for scroll lock context. */
  activeConversationId: string | null
}

export const AIConversationPage = React.memo<AIConversationPageProps>(({
  envelopes,
  emptyState,
  isStreaming,
  smoothStream,
  timelineLoading,
  onFrameSelect,
  onSaveToCard,
  activeAgent,
  activeProjectId,
  projects,
  hasMoreHistory,
  summarizedBoundary,
  discardedBoundary,
  loadingMoreHistory,
  loadOlderTurns,
  onCancelPendingSubmit,
  activeConversationId,
}) => {
  const resolvedEnvelopes = envelopes
  const activeProject = projects?.find(p => p.ProjectID === activeProjectId)

  // Git store subscription for branch info in composer.
  useSyncExternalStore(gitStore.subscribe.bind(gitStore), gitStore.getVersion)

  // Refresh git status when a turn completes (streaming transitions true → false).
  const wasStreamingRef = useRef(false)
  const prevAgentRef = useRef<string | null>(null)
  useEffect(() => {
    const agentChanged = prevAgentRef.current !== activeConversationId
    if (!agentChanged && wasStreamingRef.current && !isStreaming && activeProjectId) {
      gitStore.refresh()
    }
    wasStreamingRef.current = isStreaming
    prevAgentRef.current = activeConversationId
  }, [isStreaming, activeProjectId, activeConversationId])

  const streamRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const { canScrollDown, scrollToBottom, saveScrollAnchor, restoreScrollAnchor } = useScrollLock(streamRef, isStreaming, contentRef, activeConversationId, smoothStream)
  const { slots, registerFrames, clear: clearSlotRegistry } = useSlotRegistry()

  useEffect(() => {
    const allFrames = resolvedEnvelopes.flatMap(envelope => envelope.frames)
    registerFrames(allFrames)
  }, [registerFrames, resolvedEnvelopes])

  // Scroll anchor restoration after loading older turns.
  const loadingOlderRef = useRef(false)
  // Mirror of the loadingMoreHistory prop so the disarm timer below can read
  // the latest value without re-arming the click callback.
  const loadingMoreHistoryRef = useRef(loadingMoreHistory)
  loadingMoreHistoryRef.current = loadingMoreHistory
  useLayoutEffect(() => {
    if (loadingMoreHistory) return
    if (!loadingOlderRef.current) return
    loadingOlderRef.current = false
    restoreScrollAnchor()
  }, [loadingMoreHistory, restoreScrollAnchor])

  const handleLoadOlderTurns = useCallback(() => {
    if (!loadOlderTurns || loadingOlderRef.current) return
    saveScrollAnchor()
    loadingOlderRef.current = true
    loadOlderTurns()
    // TimelineManager.loadOlderTurns no-ops (no anchorable envelope, layer
    // already loading, hasMoreHistory raced false) without flipping
    // loadingMoreHistory, so the reset effect above never fires and the armed
    // ref would swallow every later click. Disarm unless the load actually
    // started: the manager notifies via requestAnimationFrame, so by this
    // timeout the prop has settled either way.
    setTimeout(() => {
      if (!loadingMoreHistoryRef.current) loadingOlderRef.current = false
    }, 120)
  }, [loadOlderTurns, saveScrollAnchor])

  useEffect(() => {
    if (resolvedEnvelopes.length === 0) {
      clearSlotRegistry()
      clearViewportHeightCache()
    }
  }, [clearSlotRegistry, resolvedEnvelopes.length])

  return (
    <div className="ai-conversation">
      <FileReferenceProvider projectId={activeProjectId ?? null} projectRoot={activeProject?.RootPath ?? null}>
        <MessageStream
          envelopes={resolvedEnvelopes}
          emptyState={emptyState}
          timelineLoading={timelineLoading}
          streamRef={streamRef}
          contentRef={contentRef}
          slots={slots}
          onFrameSelect={onFrameSelect}
          onSaveToCard={onSaveToCard}
          agentActorId={activeAgent?.ActorId}
          hasMoreHistory={hasMoreHistory}
          summarizedBoundary={summarizedBoundary}
          discardedBoundary={discardedBoundary}
          loadingMoreHistory={loadingMoreHistory}
          loadOlderTurns={handleLoadOlderTurns}
          onCancelPendingSubmit={onCancelPendingSubmit}
        />
      </FileReferenceProvider>

      <div className="ai-conversation-composer-area">
        {canScrollDown && (
          <button className="ai-scroll-down-btn" onClick={() => scrollToBottom()}>
            <div className="ai-scroll-down-btn-inner">
              <svg width="20" height="20" viewBox="0 0 20 20" fill="none">
                <path d="M5 7.5L10 12.5L15 7.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
              </svg>
            </div>
          </button>
        )}
      </div>
    </div>
  )
})
