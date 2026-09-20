import { useEffect, useMemo, useSyncExternalStore, useCallback } from 'react'
import { createTimelineManager, type TimelineManager, type TimelineState, type LocalEvent } from './timeline-manager'

// ── Module-level singleton manager ──

let _manager: TimelineManager | null = null

/** Get or create the singleton TimelineManager.  Exported so non-React code
 *  (e.g. AIShellLayout cache cleanup) can call release/select directly. */
export function getTimelineManager(): TimelineManager {
  if (!_manager) _manager = createTimelineManager()
  return _manager
}

const getManager = getTimelineManager

// Stable placeholder shown the instant the user switches to an agent whose
// timeline isn't tracked yet. select() runs in an effect (after render), so the
// first render would otherwise flash the empty state / welcome card before the
// skeleton appears. Returning this makes the skeleton show up immediately.
const SWITCHING_PLACEHOLDER = Object.freeze({
  envelopes: Object.freeze<TimelineState['envelopes']>([]),
  isStreaming: false,
  isPaused: false,
  isWaiting: false,
  isPausing: false,
  loading: true,
  loadingMore: false,
  error: null,
  hasMoreHistory: false,
  summarizedBoundary: false,
  discardedBoundary: false,
}) as TimelineState

// ── Public interface ──

export interface TimelineView extends TimelineState {
  pushUserMessage: (text: string) => string
  replaceUserMessageId: (tempId: string, realId: string) => void
  updateUserMessageIdx: (messageId: string, idx: number) => void
  cancelPendingUserMessage: (clientId: string) => void
  dispatchLocalEvent: (event: LocalEvent) => void
  rollbackLocalInteraction: (requestId: string) => void
  stop: () => void
  pause: () => void
  pauseAll: () => void
  resume: () => void
  loadOlderTurns: () => void
}

export function useTimelineManager(agentActorId: string | null): TimelineView {
  const manager = useMemo(() => getManager(), [])

  useEffect(() => {
    if (agentActorId) {
      manager.select(agentActorId)
    }
    // No release on unmount — agent stays loaded in background memory.
    // Release is only called on cache eviction (agent deleted from workspace).
  }, [agentActorId, manager])

  // P2: coarse reap timer. When a turn's terminal event (completed/failed/
  // cancelled) is permanently lost, the open compaction step would spin
  // forever. reaps stale open steps (latest activity > 5min) so the UI
  // recovers without waiting for a reconnect. 15s is coarse vs the 5min
  // threshold; the timer follows this component's lifecycle (cleared on unmount).
  useEffect(() => {
    if (!agentActorId) return
    const id = setInterval(() => manager.reapStale(agentActorId), 15_000)
    return () => clearInterval(id)
  }, [agentActorId, manager])

  const state = useSyncExternalStore<TimelineState>(
    useCallback((cb: () => void) => manager.subscribe(cb, agentActorId ?? undefined), [manager, agentActorId]),
    useCallback((): TimelineState => {
      if (!agentActorId) return manager.getSnapshot(null)
      const snap = manager.getSnapshot(agentActorId)
      // First render after switching to an untracked agent: the select()
      // effect hasn't fired yet, so getSnapshot returns the empty state.
      // Return the loading placeholder so the skeleton appears immediately.
      if (
        snap.envelopes.length === 0 &&
        !snap.isStreaming &&
        manager.getSelectedId() !== agentActorId &&
        !manager.hasTimeline(agentActorId)
      ) {
        return SWITCHING_PLACEHOLDER
      }
      return snap
    }, [manager, agentActorId]),
  )

  return useMemo(() => ({
    ...state,
    pushUserMessage: manager.pushUserMessage,
    replaceUserMessageId: manager.replaceUserMessageId,
    updateUserMessageIdx: manager.updateUserMessageIdx,
    cancelPendingUserMessage: manager.cancelPendingUserMessage,
    dispatchLocalEvent: manager.dispatchLocalEvent,
    rollbackLocalInteraction: manager.rollbackLocalInteraction,
    stop: manager.stop,
    pause: manager.pause,
    pauseAll: manager.pauseAll,
    resume: manager.resume,
    loadOlderTurns: manager.loadOlderTurns,
  }), [state, manager])
}

export interface BackgroundTimelineView extends TimelineState {
  pushUserMessage: (text: string) => string
  replaceUserMessageId: (tempId: string, realId: string) => void
  updateUserMessageIdx: (messageId: string, idx: number) => void
  cancelPendingUserMessage: (clientId: string) => void
  dispatchLocalEvent: (event: LocalEvent) => void
  stop: () => void
  reconcile: () => void
  seedActiveTurn: (turnActorId: string) => void
}

/** Subscribe to an agent's timeline WITHOUT making it the globally-selected
 *  agent. Loads the timeline on mount via `manager.load` (which restores the
 *  previous selection synchronously), then reads its snapshot directly. All
 *  mutating actions are bound to the explicit actorId so they never affect
 *  the active agent. Used by the card MiniComposer to talk to the project's coder agent
 *  in the background while the main timeline stays on the active agent. */
export function useBackgroundTimeline(agentActorId: string | null): BackgroundTimelineView {
  const manager = useMemo(() => getManager(), [])

  useEffect(() => {
    if (agentActorId) manager.load(agentActorId)
  }, [agentActorId, manager])

  // P2: same coarse reap timer as the main timeline so a stuck background
  // conversation's compaction step also recovers.
  useEffect(() => {
    if (!agentActorId) return
    const id = setInterval(() => manager.reapStale(agentActorId), 15_000)
    return () => clearInterval(id)
  }, [agentActorId, manager])

  const state = useSyncExternalStore<TimelineState>(
    useCallback((cb: () => void) => manager.subscribe(cb, agentActorId ?? undefined), [manager, agentActorId]),
    useCallback((): TimelineState => manager.getSnapshot(agentActorId), [manager, agentActorId]),
  )

  return useMemo(() => ({
    ...state,
    pushUserMessage: (text: string) => manager.pushUserMessage(text, agentActorId ?? undefined),
    replaceUserMessageId: (tempId: string, realId: string) => manager.replaceUserMessageId(tempId, realId, agentActorId ?? undefined),
    updateUserMessageIdx: (messageId: string, idx: number) => manager.updateUserMessageIdx(messageId, idx, agentActorId ?? undefined),
    cancelPendingUserMessage: (clientId: string) => manager.cancelPendingUserMessage(clientId, agentActorId ?? undefined),
    dispatchLocalEvent: (event: LocalEvent) => manager.dispatchLocalEvent(event, agentActorId ?? undefined),
    stop: () => manager.stop(agentActorId ?? undefined),
    reconcile: () => manager.reconcile(agentActorId ?? undefined),
    seedActiveTurn: (turnActorId: string) => { if (agentActorId) manager.seedActiveTurn(agentActorId, turnActorId) },
  }), [state, manager, agentActorId])
}
