import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { client } from '../../application/generated-client'
import * as agent_chat from '../../gen-clients/local/client'
import * as workspace from '../../gen-clients/workspace/client'
import { useBackgroundTimeline } from '../ai/hooks/useTimelineManager'
import type { TurnEnvelope } from '../ai/model/frame-types'
import { useWorkbenchAgents, type WorkbenchAgentSession } from './useWorkbenchAgents'

/** Rows the dock conversation renders (user bubble / agent reply / status hint). */
export interface CoordinatorChatRow {
  id: string
  who: 'u' | 'a'
  text: string
}

/** Dock wiring status — surfaced so a half-connected board is never silent. */
export type CoordinatorChatStatus =
  /** No workspace-global coordinator exists; the dock runs local-only rows. */
  | 'none'
  /** Coordinator resolved but its agent is not materialized (or the timeline
   *  is still loading); submits are held back and a hint row shows. */
  | 'resolving'
  /** Wired: dock input submits to the coordinator and rows track its timeline. */
  | 'live'
  /** Last submit failed; the optimistic row stays and an error hint shows. */
  | 'error'

/** Rows kept in the dock conversation (the board is a glance, not a transcript). */
const MAX_ROWS = 40
/** Retry cadence for materializing the coordinator agent (desktop-start race:
 * the projection lists the agent before the workspace spawns its actor). */
const LOAD_RETRY_MS = 2500
/** Give up retrying after this many attempts per agent id (the surface can
 * always remount to restart). */
const MAX_LOAD_ATTEMPTS = 24

/**
 * The coordinator conversation behind the workbench dock.
 *
 * The workspace-global coordinator agent (AgentKind === 'coordinator', no
 * project) is the workbench controller: dock input submits to its
 * `chat_submit`, and the conversation history / streaming replies come from
 * the shared timeline manager in background mode (same machinery as the card
 * MiniComposer) — never a second SSE stream of our own.
 *
 * The timeline is wired only once the coordinator's agent is materialized
 * (`LoadState === 'loaded'`): subscribing to an unloaded agent's actor id
 * fails the stream and would leave a dead timeline that pushUserMessage
 * can't land on — the dock would go permanently silent.
 */
export function useCoordinatorChat() {
  const sessions = useWorkbenchAgents()
  const coordinator = useMemo<WorkbenchAgentSession | null>(
    () => Array.from(sessions.values()).find(s => s.kind === 'coordinator' && !s.projectId) ?? null,
    [sessions],
  )
  const wired = coordinator?.loadState === 'loaded'
  const actorId = coordinator && wired ? coordinator.actorId : null

  // Materialize the coordinator agent with a bounded retry: at desktop start
  // the projection may list the agent before the workspace has spawned its
  // actor; a single one-shot loadAgent can lose that race and strand the dock.
  const [submitError, setSubmitError] = useState<string | null>(null)
  const loadAttemptsRef = useRef(0)
  useEffect(() => {
    if (!coordinator || coordinator.loadState === 'loaded') return
    if (loadAttemptsRef.current >= MAX_LOAD_ATTEMPTS) return
    let cancelled = false
    const attempt = () => {
      if (cancelled) return
      loadAttemptsRef.current += 1
      workspace.loadAgent(client, { AgentId: coordinator.id }).catch(() => {
        // The projection flips LoadState on success; failures just wait for
        // the next tick.
      })
    }
    attempt()
    const timer = setInterval(() => {
      if (loadAttemptsRef.current >= MAX_LOAD_ATTEMPTS) {
        clearInterval(timer)
        return
      }
      attempt()
    }, LOAD_RETRY_MS)
    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [coordinator?.id, coordinator?.loadState])

  const timeline = useBackgroundTimeline(actorId)

  const rows = useMemo<CoordinatorChatRow[]>(() => envelopeRows(timeline.envelopes), [timeline.envelopes])

  const status: CoordinatorChatStatus = !coordinator
    ? 'none'
    : !wired || timeline.loading
      ? 'resolving'
      : submitError
        ? 'error'
        : 'live'

  const submit = useCallback(async (text: string): Promise<boolean> => {
    const trimmed = text.trim()
    if (!actorId || !trimmed) return false
    const tempId = timeline.pushUserMessage(trimmed)
    try {
      const resp = await agent_chat.chatSubmit(client, { Text: trimmed }, { target: actorId })
      setSubmitError(null)
      if (resp.TurnActorId) timeline.seedActiveTurn(resp.TurnActorId)
      if (tempId && resp.MessageId) timeline.replaceUserMessageId(tempId, resp.MessageId)
      if (resp.Idx != null && resp.Idx > 0 && resp.MessageId) {
        timeline.updateUserMessageIdx(resp.MessageId, resp.Idx)
      }
      timeline.reconcile()
      return true
    } catch (err) {
      // The optimistic user envelope stays; surface the failure instead of
      // failing silently (the dock has no other error channel).
      setSubmitError(err instanceof Error ? err.message : String(err))
      // Orphaned-turn recovery: a client-side timeout does not mean the server
      // rejected the submit. Reconcile fetches the summary — a created turn's
      // user step confirms the pending message instead of the user resending
      // and duplicating the turn.
      timeline.reconcile()
      return false
    }
  }, [actorId, timeline])

  return { coordinator, rows, submit, streaming: timeline.isStreaming, status, error: submitError }
}

/** Flatten timeline envelopes into dock rows: user text bubbles and assistant
 * text frames. Non-text frames (tools, reasoning, plans) are skipped — the
 * board's conversation is the dialogue, not the execution trace. */
function envelopeRows(envelopes: readonly TurnEnvelope[]): CoordinatorChatRow[] {
  const rows: CoordinatorChatRow[] = []
  for (const env of envelopes) {
    if (env.role === 'user') {
      const text = (env.userContent ?? '').trim()
      if (text) rows.push({ id: `u:${env.id}`, who: 'u', text })
      continue
    }
    const parts: string[] = []
    for (const frame of env.frames) {
      if (frame.type === 'text' && frame.content.trim()) parts.push(frame.content.trim())
    }
    if (parts.length > 0) rows.push({ id: `a:${env.id}`, who: 'a', text: parts.join('\n') })
  }
  return rows.length > MAX_ROWS ? rows.slice(rows.length - MAX_ROWS) : rows
}
