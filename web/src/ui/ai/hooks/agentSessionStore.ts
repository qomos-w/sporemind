import { useEffect, useState } from 'react'

export interface AgentSessionSnapshot {
  activeAgentId: string | null
  activeProjectId: string | null
}

let snapshot: AgentSessionSnapshot = { activeAgentId: null, activeProjectId: null }
let listeners = new Set<() => void>()

function emit() {
  for (const cb of listeners) {
    cb()
  }
}

export function setActiveAgentId(id: string | null, projectId?: string | null): void {
  if (snapshot.activeAgentId === id && snapshot.activeProjectId === projectId) return
  snapshot = { activeAgentId: id, activeProjectId: projectId ?? null }
  emit()
}

export function subscribeAgentSessionStore(cb: () => void): () => void {
  listeners.add(cb)
  return () => {
    listeners.delete(cb)
  }
}

export function getAgentSessionSnapshot(): AgentSessionSnapshot {
  return snapshot
}

export function getActiveAgentId(): string | null {
  return snapshot.activeAgentId
}

export function getActiveProjectId(): string | null {
  return snapshot.activeProjectId
}

export function useActiveAgentId(): string | null {
  const [id, setId] = useState<string | null>(getActiveAgentId)
  useEffect(() => subscribeAgentSessionStore(() => setId(getActiveAgentId())), [])
  return id
}

export function useActiveProjectId(): string | null {
  const [pid, setPid] = useState<string | null>(getActiveProjectId)
  useEffect(() => subscribeAgentSessionStore(() => setPid(getActiveProjectId())), [])
  return pid
}
