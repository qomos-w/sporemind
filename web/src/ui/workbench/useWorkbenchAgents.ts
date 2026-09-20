import { useEffect, useState } from 'react'

import { client } from '../../application/generated-client'
import { getAgents, watchAgents } from '../../gen-clients/workspace/projection-client'
import type { AgentRef } from '../../gen-clients/system/types'
import { pascalize } from './wireShape'

/**
 * Workbench agent session metadata, resolved from the workspace `Agents`
 * projection.
 *
 * The workbench actor only sees the emitting agent's actor id on `step` / `turn`
 * events, so it cannot name a card or summarise a session on its own. The board
 * resolves both here: the display name is fed back through
 * `workbench.upsert_card` (D8), and the recent turn status / title / last
 * activity drives the chat card's session summary.
 *
 * Best-effort: a missing workspace actor simply leaves the map empty.
 */
export interface WorkbenchAgentSession {
  /** Stable agent id (`AgentRef.Id`). */
  id: string
  actorId: string
  /** Display name (falls back to the stable id). */
  name: string
  projectId: string
  /** Agent kind (`AgentRef.AgentKind`), e.g. coordinator / coder. */
  kind: string
  /** Load state (`AgentRef.LoadState`), '' when unknown. */
  loadState: string
  /** Recent turn status (`AgentRef.Status`), '' when unknown. */
  status: string
  /** Session title (`AgentRef.Title`), '' when unknown. */
  title: string
  /** ISO timestamp of the last turn/activity, '' when unknown. */
  lastActivity: string
}

export function useWorkbenchAgents(): ReadonlyMap<string, WorkbenchAgentSession> {
  const [sessions, setSessions] = useState<ReadonlyMap<string, WorkbenchAgentSession>>(() => new Map())

  useEffect(() => {
    let cancelled = false
    const apply = (agents: AgentRef[]) => {
      if (cancelled) return
      const next = new Map<string, WorkbenchAgentSession>()
      for (const raw of agents) {
        // The Agents projection arrives with lowerFirst wire keys (see pascalize).
        const agent = pascalize(raw)
        const actorId = agent.ActorId
        if (!actorId) continue
        const name = agent.DisplayName?.trim() || agent.Id
        if (!name) continue
        next.set(actorId, {
          id: agent.Id,
          actorId,
          name,
          projectId: agent.ProjectId ?? '',
          kind: agent.AgentKind ?? '',
          loadState: agent.LoadState ?? '',
          status: agent.Status ?? '',
          title: agent.Title ?? '',
          lastActivity: agent.LastActivity ?? '',
        })
      }
      setSessions(next)
    }
    void (async () => {
      try {
        apply(await getAgents(client))
      } catch {
        // workspace actor unavailable — leave the map empty.
      }
      if (cancelled) return
      try {
        for await (const agents of watchAgents(client)) apply(agents)
      } catch {
        // stream closed.
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  return sessions
}
