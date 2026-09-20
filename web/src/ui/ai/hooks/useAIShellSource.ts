import React, { useCallback, useMemo } from 'react'
import type { TurnEnvelope } from '../model/frame-types'
import type { ModelUnit } from '../../../gen-types/aigen'
import { useAIPreviewEnvelopeStream } from './useAIPreviewEnvelopeStream'
import { useTimelineManager } from './useTimelineManager'
import { client } from '../../../application/generated-client'
import * as agentTurnClient from '../../../gen-clients/local/client'
import type { AILocalEvent } from '../model/local-events'

export interface AIShellSource {
  envelopes: TurnEnvelope[]
  emptyState?: React.ReactNode
  isStreaming: boolean
  isPaused: boolean
  isWaiting: boolean
  isPausing: boolean
  loading: boolean
  loadingMore: boolean
  stop: () => void
  pause: () => void
  pauseAll: () => void
  resume: () => void
  previewScenarios: import('../model/ai-preview-envelopes').AIPreviewScenario[]
  activePreviewScenarioId: string
  setPreviewScenarioId: (id: string) => void
  startPreview?: () => void
  resetPreview?: () => void
  /** Dispatch a UI-originated event (e.g. user answered ask_user / permission).
   *  Fans out to a local reducer update and an outbound callable to the turn. */
  dispatchEvent: (event: AILocalEvent) => Promise<void>
  /** Push a user-message envelope into the real stream. No-op in preview mode.
   *  Returns the temporary ID assigned to the user envelope. */
  pushUserMessage: (text: string) => string
  /** Replace a temporary user-message ID with the real one from the backend. */
  replaceUserMessageId: (tempId: string, realId: string) => void
  /** Backfill the Idx assigned by the turn into the optimistic envelope. */
  updateUserMessageIdx: (messageId: string, idx: number) => void
  /** Cancel (revoke) a pending user message by clientId. */
  cancelPendingSubmit: (clientId: string) => void
  /** Whether older turns exist beyond the currently loaded window */
  hasMoreHistory: boolean
  /** Older summarized turns were collapsed; show a summarized boundary */
  summarizedBoundary: boolean
  /** Older summarized turns were discarded to free storage/memory */
  discardedBoundary: boolean
  /** Load older turns when scrolling to the top of the conversation */
  loadOlderTurns: () => void
  /** The model unit the aggregator actually resolved for the current dispatch
   *  (β feedback). Undefined in preview mode or before first dispatch. */
  currentUnit?: ModelUnit | undefined
}

export function useAIShellSource(
  shellVariant: 'ai' | 'preview',
  realEmptyState?: React.ReactNode,
  _turnActorId?: string | null,
  agentActorId?: string,
  _onStaleActor?: () => void,
): AIShellSource {
  const previewStream = useAIPreviewEnvelopeStream()
  const timeline = useTimelineManager(agentActorId ?? null)

  const previewDispatch = useCallback(async (event: AILocalEvent) => {
    previewStream.dispatchEvent(event)
  }, [previewStream])

  const realDispatch = useCallback(async (event: AILocalEvent) => {
    // 1) Local frame update — always run, so the UI reflects the user's
    //    choice immediately, even if the backend send fails.
    timeline.dispatchLocalEvent(event)

    // 2) Outbound callable
    if (!agentActorId) {
      console.warn('[useAIShellSource] local event dispatched but agentActorId is empty', event.kind)
      return
    }
    let answersJson: string
    switch (event.kind) {
      case 'ai.ask_answered':
        answersJson = JSON.stringify(event.answers)
        break
      case 'ai.permission_answered':
        answersJson = JSON.stringify({ allowed: event.allowed, allowInProject: event.allowInProject })
        break
      case 'ai.plan_approval_answered':
        answersJson = JSON.stringify({
          decision: event.decision,
          editedPlan: event.editedPlan,
          feedback: event.feedback,
          selectedPolicy: event.selectedPolicy,
        })
        break
      case 'ai.goal_submit_answered':
        answersJson = JSON.stringify({
          decision: event.decision,
          feedback: event.feedback,
        })
        break
      case 'ai.goal_card_submit_answered':
        answersJson = JSON.stringify({
          decision: event.decision,
          feedback: event.feedback,
        })
        break
    }
    try {
      await agentTurnClient.turnAnswer(client, { AnswersJson: answersJson, RequestId: event.requestId }, { target: agentActorId })
    } catch (err: unknown) {
      // RPC failed (turn cancelled, network drop, validation error, ...).
      // Revert the optimistic local response so the UI shows the prior
      // pending state and the user can retry. Selectors treat the Map as
      // authoritative, so a delete is enough — no step mutation to undo.
      console.error('[useAIShellSource] agent.turn.answer failed:', event.kind, err)
      timeline.rollbackLocalInteraction(event.requestId)
    }
  }, [agentActorId, timeline])

  const dispatchEvent = shellVariant === 'preview' ? previewDispatch : realDispatch
  const pushUserMessage = shellVariant === 'ai' ? timeline.pushUserMessage : useCallback((_text: string) => '', [])
  const replaceUserMessageId = shellVariant === 'ai' ? timeline.replaceUserMessageId : useCallback((_tempId: string, _realId: string) => {}, [])
  const updateUserMessageIdx = shellVariant === 'ai' ? timeline.updateUserMessageIdx : useCallback((_messageId: string, _idx: number) => {}, [])
  const cancelPendingSubmit = shellVariant === 'ai' ? timeline.cancelPendingUserMessage : useCallback((_clientId: string) => {}, [])

  return useMemo(() => {
    if (shellVariant === 'ai') {
      return {
        envelopes: timeline.envelopes,
        emptyState: realEmptyState,
        isStreaming: timeline.isStreaming,
        isPaused: timeline.isPaused,
        isWaiting: timeline.isWaiting,
        isPausing: timeline.isPausing,
        loading: timeline.loading,
        loadingMore: timeline.loadingMore,
        stop: timeline.stop,
        pause: timeline.pause,
        pauseAll: timeline.pauseAll,
        resume: timeline.resume,
        previewScenarios: previewStream.scenarios,
        activePreviewScenarioId: previewStream.scenarioId,
        setPreviewScenarioId: previewStream.setScenarioId,
        startPreview: undefined,
        resetPreview: undefined,
        dispatchEvent,
        pushUserMessage,
        replaceUserMessageId,
        updateUserMessageIdx,
        cancelPendingSubmit,
        hasMoreHistory: timeline.hasMoreHistory,
        summarizedBoundary: timeline.summarizedBoundary,
        discardedBoundary: timeline.discardedBoundary,
        loadOlderTurns: timeline.loadOlderTurns,
        currentUnit: timeline.currentUnit,
      }
    }

    return {
      envelopes: previewStream.envelopes,
      emptyState: undefined,
      isStreaming: previewStream.isStreaming,
      isPaused: false,
      isWaiting: false,
      isPausing: false,
      loading: false,
      loadingMore: false,
      stop: timeline.stop,
      pause: timeline.pause,
      pauseAll: timeline.pauseAll,
      resume: timeline.resume,
      previewScenarios: previewStream.scenarios,
      activePreviewScenarioId: previewStream.scenarioId,
      setPreviewScenarioId: previewStream.setScenarioId,
      startPreview: previewStream.startPreviewStream,
      resetPreview: previewStream.resetPreviewStream,
      dispatchEvent,
      pushUserMessage,
      replaceUserMessageId,
      updateUserMessageIdx,
      cancelPendingSubmit,
      hasMoreHistory: false,
      summarizedBoundary: false,
      discardedBoundary: false,
      loadOlderTurns: () => {},
    }
  }, [previewStream, timeline, realEmptyState, shellVariant, dispatchEvent, pushUserMessage, replaceUserMessageId, updateUserMessageIdx, cancelPendingSubmit])
}
