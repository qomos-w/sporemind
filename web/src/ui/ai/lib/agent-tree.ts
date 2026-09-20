/**
 * Cycle-safe agent parent-child tree builder for the sidebar.
 *
 * Given a flat list of agents (each carrying `ParentAgentId` and `Children`),
 * `buildAgentForest` produces a depth-ordered tree suitable for rendering with
 * L-shaped connectors.
 *
 * Two node categories:
 *
 * 1. **Persistent agent** — an `AgentInfo` that lives in the agent list. Its
 *    children are resolved from its own `Children` projection, but only those
 *    that also appear as a persistent agent in the list are mounted (workflow
 *    children). Transient fork children are not rendered in the sidebar.
 *
 * 2. **Orphan root** — a persistent agent that has a `ParentAgentId` but was
 *    never mounted as a child (its parent is absent from the list, or the
 *    parent's `Children` projection is stale/missing). These are promoted to
 *    root level so they remain visible in the sidebar.
 *
 * Cycle prevention: each node's `ActorId` is tracked in a per-path visited
 * set. If a child's ActorId already appears on the path from root, the branch
 * is pruned (returns `null`), preventing infinite loops and duplicate nodes.
 */

import type { AgentInfo } from '../hooks/agentInfoStore'

/** A single node in the rendered agent tree. */
export interface AgentTreeNode {
  /** The persistent AgentInfo. */
  agent: AgentInfo
  /** Stable React key. */
  key: string
  /** Agent ID. */
  id: string
  /** Actor ID. */
  actorId: string
  /** Human-readable label. */
  displayName: string
  /** Agent kind string (coder, worker, …). */
  agentKind: string
  /** Lifecycle scope: "workflow" | "turn". */
  lifecycleScope: string
  /** Whether this node has visible children. */
  hasChildren: boolean
  /** Child nodes. */
  children: AgentTreeNode[]
  /** Depth from the root (0 = top-level). */
  depth: number
  /** Whether this node is the last child of its parent. */
  isLastChild: boolean
  /** For each ancestor depth level (0 .. depth-1): `true` if a vertical
   *  guide line should be drawn (the ancestor had later siblings), `false`
   *  if the space should be blank. Length === depth. */
  guideLines: boolean[]
}

/**
 * Compute a stable React key. Falls back to a structural path so that
 * multiple nodes with empty Id/ActorId at the same depth don't collide.
 */
function nodeKey(id: string, actorId: string, pathKey: string): string {
  return id || actorId || `node-${pathKey}`
}

function buildNode(
  agent: AgentInfo,
  byActorId: Map<string, AgentInfo>,
  byId: Map<string, AgentInfo>,
  depth: number,
  isLastChild: boolean,
  guideLines: boolean[],
  pathVisited: Set<string>,
  pathKey: string,
  mounted: Set<string>,
): AgentTreeNode | null {
  const actorId = agent.ActorId
  const id = agent.Id

  // Cycle detection: if this actorId is already on the path, prune.
  if (actorId && pathVisited.has(actorId)) {
    return null
  }

  // Track mounted persistent agents so orphan recovery can detect
  // agents that were never attached to any tree branch.
  mounted.add(agent.Id)

  const childPathVisited = new Set(pathVisited)
  if (actorId) childPathVisited.add(actorId)

  // Resolve children from the agent's Children projection. Only children
  // that resolve to an agent in the list are mounted. Fork children are
  // registered in the workspace agent list (LifecycleScope='fork', with the
  // parent's ProjectId) while alive, so they render like workflow workers
  // and disappear once they self-terminate.
  const childRefs = agent.Children ?? []
  const children: AgentTreeNode[] = []
  for (let i = 0; i < childRefs.length; i++) {
    const cr = childRefs[i]
    if (!cr) continue
    const childAgent = cr.ActorId ? byActorId.get(cr.ActorId) : byId.get(cr.Id)
    if (!childAgent) continue
    const childGuideLines = [...guideLines, !isLastChild]
    const childNode = buildNode(
      childAgent,
      byActorId,
      byId,
      depth + 1,
      i === childRefs.length - 1,
      childGuideLines,
      childPathVisited,
      `${pathKey}.${i}`,
      mounted,
    )
    if (childNode) children.push(childNode)
  }

  return {
    agent,
    key: nodeKey(id, actorId, pathKey),
    id,
    actorId,
    displayName: agent.DisplayName,
    agentKind: agent.AgentKind,
    lifecycleScope: agent.LifecycleScope ?? 'workflow',
    hasChildren: children.length > 0,
    children,
    depth,
    isLastChild,
    guideLines,
  }
}

/**
 * Recompute `depth`, `isLastChild`, and `guideLines` for every node in a
 * forest. This is necessary after orphan roots are appended because the
 * original root-level `isLastChild` values (and their children's guide
 * lines) were computed before the final forest shape was known.
 */
function recomputeRootMetadata(forest: AgentTreeNode[]): void {
  for (let i = 0; i < forest.length; i++) {
    fixupGuideLines(forest[i]!, 0, i === forest.length - 1, [])
  }
}

function fixupGuideLines(
  node: AgentTreeNode,
  depth: number,
  isLastChild: boolean,
  guideLines: boolean[],
): void {
  node.depth = depth
  node.isLastChild = isLastChild
  node.guideLines = guideLines
  const childGuideLines = [...guideLines, !isLastChild]
  for (let i = 0; i < node.children.length; i++) {
    fixupGuideLines(node.children[i]!, depth + 1, i === node.children.length - 1, childGuideLines)
  }
}

/**
 * Build a forest of agent trees from a flat agent list.
 *
 * Roots are agents without a `ParentAgentId`. Children are resolved from
 * each agent's `Children` projection, mounting only those that are also
 * persistent agents in the list (workflow children).
 *
 * **Orphan recovery**: any persistent agent that has a `ParentAgentId` but
 * was never mounted as a child (because its parent is absent or the parent's
 * `Children` projection is stale/missing) is promoted to a root-level node.
 * Its `ParentAgentId` is preserved on the underlying `AgentInfo` for
 * observability.
 *
 * Cycle-safe: if a child's ActorId already appears on the path from root
 * to the current node, that branch is pruned.
 */
export function buildAgentForest(agents: AgentInfo[]): AgentTreeNode[] {
  if (agents.length === 0) return []

  const byActorId = new Map<string, AgentInfo>()
  const byId = new Map<string, AgentInfo>()
  for (const a of agents) {
    if (a.ActorId) byActorId.set(a.ActorId, a)
    byId.set(a.Id, a)
  }

  const mounted = new Set<string>()

  // Phase 1: build from natural roots (agents with no ParentAgentId).
  const roots = agents.filter(a => !a.ParentAgentId)
  const rootVisited = new Set<string>()

  const rootTrees = roots
    .map((agent, i) =>
      buildNode(
        agent,
        byActorId,
        byId,
        0,
        i === roots.length - 1,
        [],
        rootVisited,
        `r${i}`,
        mounted,
      ),
    )
    .filter((n): n is AgentTreeNode => n !== null)

  // Phase 2: orphan recovery — agents with a ParentAgentId that were never
  // mounted during Phase 1 (parent missing, or parent's Children projection
  // stale/missing). Promoted to root level so they remain visible in the
  // sidebar.
  const orphans = agents.filter(a => !!a.ParentAgentId && !mounted.has(a.Id))
  const orphanTrees = orphans
    .map((agent, i) =>
      buildNode(
        agent,
        byActorId,
        byId,
        0,
        i === orphans.length - 1,
        [],
        new Set<string>(),
        `o${i}`,
        mounted,
      ),
    )
    .filter((n): n is AgentTreeNode => n !== null)

  const forest = [...rootTrees, ...orphanTrees]

  // Fix up rendering metadata now that the final forest shape is known.
  recomputeRootMetadata(forest)

  return forest
}

/** Flatten a tree forest into a depth-first ordered list for rendering. */
export function flattenAgentForest(nodes: AgentTreeNode[]): AgentTreeNode[] {
  const result: AgentTreeNode[] = []
  function walk(node: AgentTreeNode) {
    result.push(node)
    for (const child of node.children) walk(child)
  }
  for (const node of nodes) walk(node)
  return result
}
