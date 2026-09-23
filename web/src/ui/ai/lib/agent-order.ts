/**
 * Flat-list ordering that respects agent parent-child hierarchy.
 *
 * The avatar bar renders a horizontal run of agents in the same order as the
 * sidebar: a depth-first pre-order walk of the parent-child tree. Each agent
 * is emitted, then its entire subtree is expanded, before the next sibling
 * root — children never trail at the end of the list.
 */

import type { AgentChildRef } from '../../../gen-clients/system/types'

/**
 * Fields the ordering helpers need — satisfied by both `AgentInfo`
 * (agentInfoStore) and `AgentListItem` (agentListStore), so any agent
 * picker can mirror the sidebar order.
 */
export interface SidebarOrderableAgent {
  Id: string
  ActorId: string
  ProjectId: string
  ParentAgentId?: string | undefined
  Children?: AgentChildRef[] | undefined
}

/**
 * Reorder `agents` into depth-first pre-order over the parent-child tree:
 * roots keep their input order, and each root is followed by its full
 * subtree (recursively) before the next root.
 *
 * `ParentAgentId` references the parent's ActorId; only agents whose parent
 * is present in the set are attached. Children are ordered like the sidebar:
 * by the parent's `Children` projection when available, falling back to
 * input order. Agents whose parent is absent are treated as roots, and any
 * nodes unreachable due to cycles are appended in input order.
 */
export function applyParentChildOrder<T extends SidebarOrderableAgent>(agents: T[]): T[] {
  if (agents.length <= 1) return agents

  const byActorId = new Map<string, T>()
  const byId = new Map<string, T>()
  const inputIndex = new Map<string, number>()
  agents.forEach((a, i) => {
    if (a.ActorId) byActorId.set(a.ActorId, a)
    byId.set(a.Id, a)
    inputIndex.set(a.Id, i)
  })

  // Map each agent to its parent, but only when the parent is present in
  // this set. Agents with an absent parent are treated as roots.
  const parentOf = new Map<string, T>()
  for (const a of agents) {
    if (!a.ParentAgentId) continue
    const parent = byActorId.get(a.ParentAgentId)
    if (!parent || parent.Id === a.Id) continue
    parentOf.set(a.Id, parent)
  }

  if (parentOf.size === 0) return agents

  // Group children by parent, ordered like the sidebar: the parent's
  // Children projection first, then input order as fallback.
  const childrenOf = new Map<string, T[]>()
  for (const a of agents) {
    const parent = parentOf.get(a.Id)
    if (!parent) continue
    const list = childrenOf.get(parent.Id) ?? []
    list.push(a)
    childrenOf.set(parent.Id, list)
  }
  for (const [parentId, list] of childrenOf) {
    const parent = byId.get(parentId)!
    const projectionRank = new Map<string, number>()
    ;(parent.Children ?? []).forEach((ref, i) => {
      const child = ref.ActorId ? byActorId.get(ref.ActorId) : byId.get(ref.Id)
      if (child) projectionRank.set(child.Id, i)
    })
    list.sort((x, y) => {
      const rx = projectionRank.get(x.Id)
      const ry = projectionRank.get(y.Id)
      if (rx != null && ry != null) return rx - ry
      if (rx != null) return -1
      if (ry != null) return 1
      return (inputIndex.get(x.Id) ?? 0) - (inputIndex.get(y.Id) ?? 0)
    })
  }

  // Depth-first pre-order with per-path cycle protection.
  const ordered: T[] = []
  const visited = new Set<string>()
  const walk = (agent: T, path: Set<string>) => {
    if (visited.has(agent.Id)) return
    if (agent.ActorId && path.has(agent.ActorId)) return
    visited.add(agent.Id)
    ordered.push(agent)
    const childPath = new Set(path)
    if (agent.ActorId) childPath.add(agent.ActorId)
    for (const child of childrenOf.get(agent.Id) ?? []) walk(child, childPath)
  }

  // Roots in input order; each fully expands its subtree before the next.
  for (const a of agents) {
    if (!parentOf.has(a.Id)) walk(a, new Set())
  }
  // Leftovers (cycle members with no reachable root): append in input order.
  for (const a of agents) {
    if (!visited.has(a.Id)) walk(a, new Set())
  }

  return ordered
}

/**
 * Sidebar agent ordering: projects in `projectOrder` (cross-project
 * sidebarOrder), agents within each project by `agentOrder` (per-project
 * drag order), unseen agents appended at the end. Returns the ordered IDs.
 */
export function orderedAgentIds<T extends SidebarOrderableAgent>(
  agents: T[],
  agentOrder: string[] | undefined,
  projectOrder: string[] | undefined,
): string[] {
  const safeAgentOrder = agentOrder ?? []
  const safeProjectOrder = projectOrder ?? []

  const byProject = new Map<string, T[]>()
  for (const agent of agents) {
    const list = byProject.get(agent.ProjectId) ?? []
    list.push(agent)
    byProject.set(agent.ProjectId, list)
  }

  const sortedByProject = new Map<string, string[]>()
  for (const [projectId, projectAgents] of byProject) {
    const ids = new Set(projectAgents.map(agent => agent.Id))
    const known = safeAgentOrder.filter(id => ids.has(id))
    const unseen = projectAgents.map(agent => agent.Id).filter(id => !known.includes(id))
    sortedByProject.set(projectId, [...known, ...unseen])
  }

  const knownProjectIds = new Set(agents.map(agent => agent.ProjectId))
  const orderedProjects = [
    ...safeProjectOrder.filter(id => knownProjectIds.has(id)),
    ...[...knownProjectIds].filter(id => !safeProjectOrder.includes(id)),
  ]

  return orderedProjects.flatMap(projectId => sortedByProject.get(projectId) ?? [])
}

/**
 * Full sidebar ordering for agent pickers: `orderedAgentIds` per-project +
 * cross-project grouping, then `applyParentChildOrder` depth-first
 * flattening so the result matches the sidebar exactly.
 */
export function applyAgentOrder<T extends SidebarOrderableAgent>(
  agents: T[],
  agentOrder: string[] | undefined,
  projectOrder: string[] | undefined,
): T[] {
  const byId = new Map(agents.map(agent => [agent.Id, agent]))
  const order = orderedAgentIds(agents, agentOrder, projectOrder)
  const ordered = order.map(id => byId.get(id)).filter((agent): agent is T => Boolean(agent))
  return applyParentChildOrder(ordered)
}
