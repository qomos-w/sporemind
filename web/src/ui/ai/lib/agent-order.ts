/**
 * Flat-list ordering that respects agent parent-child hierarchy.
 *
 * The avatar bar renders a horizontal run of agents in the same order as the
 * sidebar: a depth-first pre-order walk of the parent-child tree. Each agent
 * is emitted, then its entire subtree is expanded, before the next sibling
 * root — children never trail at the end of the list.
 */

import type { AgentInfo } from '../hooks/agentInfoStore'

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
export function applyParentChildOrder(agents: AgentInfo[]): AgentInfo[] {
  if (agents.length <= 1) return agents

  const byActorId = new Map<string, AgentInfo>()
  const byId = new Map<string, AgentInfo>()
  const inputIndex = new Map<string, number>()
  agents.forEach((a, i) => {
    if (a.ActorId) byActorId.set(a.ActorId, a)
    byId.set(a.Id, a)
    inputIndex.set(a.Id, i)
  })

  // Map each agent to its parent, but only when the parent is present in
  // this set. Agents with an absent parent are treated as roots.
  const parentOf = new Map<string, AgentInfo>()
  for (const a of agents) {
    if (!a.ParentAgentId) continue
    const parent = byActorId.get(a.ParentAgentId)
    if (!parent || parent.Id === a.Id) continue
    parentOf.set(a.Id, parent)
  }

  if (parentOf.size === 0) return agents

  // Group children by parent, ordered like the sidebar: the parent's
  // Children projection first, then input order as fallback.
  const childrenOf = new Map<string, AgentInfo[]>()
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
  const ordered: AgentInfo[] = []
  const visited = new Set<string>()
  const walk = (agent: AgentInfo, path: Set<string>) => {
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
