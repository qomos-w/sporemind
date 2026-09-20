import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { Network } from 'vis-network/standalone'
import { agentCardId, deriveMountSpecs, isVirtualMountNode, mountTargetForCard, splitFrontmatter, type MonoCardListItem } from '../../../domain/mono-types'
import { isBuiltinCard, overrideBuiltinTitle } from '../../../domain/builtin-cards'
import { resolveStatusAliases } from '../../../domain/task-status'
import { buildChildMap, tagParentIds } from './topologyRelations'
import { workflowOwnerAgentId } from './workflowLayout'
import { sizeAndMassForChildCount } from '../lib/node-sizing'
import { extractWikiWordTargets } from './parts/wikiword'
import { useI18n } from '../../../i18n'
import { avatarHue, agentAvatarLabel } from '../lib/agent-avatar'
import { getAgentInfoSnapshot, type AgentInfo, type AgentInfoSnapshot, type AgentStatus } from '../hooks/agentInfoStore'
import type { MemoryNode, MemorySnapshotResp } from '../../../gen-clients/system/types'

import './TopologyGraph.css'
import { buildLayerTransform, worldReticleSize } from './topologyOverlayCamera'
import { CARD_ICON_LIBRARY } from './cardIconLibrary'
import { cardVisual, getCardVisualStyle } from './cardVisual'

/** Minimal interface for vis-network's internal DataSet (accessed via network.body.data). */
interface VisDataSet {
  getIds(): (string | number)[]
  update(data: Record<string, unknown> | Record<string, unknown>[]): (string | number)[]
  remove(id: string | number | (string | number)[]): (string | number)[]
}

interface VisNetworkNode {
  x: number
  y: number
  options: { fixed?: boolean | { x?: boolean; y?: boolean } }
}

/** Type cast helper for accessing vis-network's internal node/edge DataSets. */
function getInternalDataSets(network: Network): { nodes?: VisDataSet; edges?: VisDataSet } {
  return (network as unknown as { body: { data: { nodes?: VisDataSet; edges?: VisDataSet } } }).body.data
}

/** vis-network keeps drag and physics positions on its rendered node instances. */
function getInternalNodes(network: Network): Record<string, VisNetworkNode> {
  return (network as unknown as { body: { nodes: Record<string, VisNetworkNode> } }).body.nodes
}

function agentIdFromCardId(cardId: string): string {
  return cardId.slice('agent:'.length)
}

const agentAvatarRoots = new Map<HTMLDivElement, ReturnType<typeof createRoot>>()
const cardIconInner = new Map<string, string>()
const cardIconSvgCache = new Map<string, string>()

// Pre-extract each icon's SVG inner markup once at module load. Rendering
// icons during a component's render phase (createRoot + flushSync + unmount)
// triggers a "synchronously unmount a root while React was already rendering"
// race, so we build a static map up front and let cardIconSvg() collapse to
// pure string concatenation.
for (const [name, Icon] of Object.entries(CARD_ICON_LIBRARY)) {
  if (!Icon) continue
  const markup = renderToStaticMarkup(<Icon size={24} color="currentColor" strokeWidth={2} />)
  const match = markup.match(/<svg[^>]*>([\s\S]*)<\/svg>/i)
  cardIconInner.set(name, match ? match[1] ?? '' : '')
}

function cardIconSvg(name: string, color: string, background: string): string | undefined {
  const key = `${name}:${color}:${background}`
  const cached = cardIconSvgCache.get(key)
  if (cached) return cached
  const inner = cardIconInner.get(name)
  if (!inner) return undefined
  const colored = inner.replace(/currentColor/gi, color)
  const wrapped = `<svg xmlns="http://www.w3.org/2000/svg" width="28" height="28" viewBox="0 0 28 28"><circle cx="14" cy="14" r="14" fill="${background}"/><g transform="translate(4.25 4.25) scale(0.8125)" fill="none" stroke="${color}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${colored}</g></svg>`
  const url = `data:image/svg+xml,${encodeURIComponent(wrapped)}`
  cardIconSvgCache.set(key, url)
  return url
}

function escapeSvgText(text: string): string {
  return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

function agentAvatarSvg(id: string, displayName: string | undefined, agentKind: string | undefined, status?: string, selected?: boolean, loaded = true): string {
  const hue = avatarHue(id)
  const isDark = document.documentElement.getAttribute('data-theme') === 'dark'
  // Unloaded (lazy) agents render in grayscale — same neutral scheme as the
  // coordinator/unloaded CSS avatars — until LoadState flips to "loaded".
  // Selection stays carried by the node border, so the avatar body itself
  // stays grayscale even for the active agent.
  const bg = !loaded
    ? (isDark ? '#17181d' : '#f2f2f5')
    : selected ? getCssVar('--accent-primary', '#3b82f6') : (isDark ? `hsl(${hue} 45% 22%)` : `hsl(${hue} 70% 92%)`)
  const fg = !loaded
    ? getCssVar('--text-tertiary', isDark ? '#a1a1aa' : '#8b8f9a')
    : selected ? '#ffffff' : (isDark ? `hsl(${hue} 75% 88%)` : `hsl(${hue} 64% 36%)`)

  // The working animation is rendered by the DOM overlay; keep the SVG node
  // clean so the two don't fight on size or layering.
  const ring = ''

  const center = status === 'paused'
    ? `<path d="M11.5 10 v8 M16.5 10 v8" stroke="${fg}" stroke-width="2" stroke-linecap="round" fill="none"/>`
    : `<text x="14" y="14" text-anchor="middle" dominant-baseline="central" font-family="system-ui, -apple-system, BlinkMacSystemFont, sans-serif" font-size="11" font-weight="600" fill="${fg}">${escapeSvgText(agentAvatarLabel(displayName, agentKind))}</text>`

  let badge = ''
  if (status === 'error') {
    badge = `<circle cx="20" cy="20" r="5" fill="hsl(0 75% 55%)"/><text x="20" y="20" text-anchor="middle" dominant-baseline="central" font-family="system-ui, -apple-system, sans-serif" font-size="8" font-weight="700" fill="#fff">!</text>`
  } else if (status === 'ask_user') {
    badge = `<circle cx="20" cy="20" r="5" fill="hsl(30 90% 55%)"/><text x="20" y="20" text-anchor="middle" dominant-baseline="central" font-family="system-ui, -apple-system, sans-serif" font-size="8" font-weight="700" fill="#fff">?</text>`
  } else if (status === 'ask_permission') {
    badge = `<circle cx="20" cy="20" r="5" fill="hsl(45 95% 45%)"/><text x="20" y="20" text-anchor="middle" dominant-baseline="central" font-family="system-ui, -apple-system, sans-serif" font-size="7" font-weight="700" fill="hsl(0 0% 10%)">L</text>`
  } else if (status === 'plan_approval') {
    badge = `<circle cx="20" cy="20" r="5" fill="${isDark ? 'hsl(190 75% 55%)' : 'hsl(190 80% 45%)'}"/><text x="20" y="20" text-anchor="middle" dominant-baseline="central" font-family="system-ui, -apple-system, sans-serif" font-size="7" font-weight="700" fill="#fff">F</text>`
  } else if (status === 'goal_submit') {
    badge = `<circle cx="20" cy="20" r="5" fill="${isDark ? 'hsl(270 75% 68%)' : 'hsl(270 70% 60%)'}"/><text x="20" y="20" text-anchor="middle" dominant-baseline="central" font-family="system-ui, -apple-system, sans-serif" font-size="7" font-weight="700" fill="#fff">G</text>`
  }

  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="28" height="28"><circle cx="14" cy="14" r="14" fill="${bg}"/>${ring}${center}${badge}</svg>`
  return `data:image/svg+xml,${encodeURIComponent(svg)}`
}

interface TopologyGraphProps {
  cards: MonoCardListItem[]
  agentInfoSnapshot?: AgentInfoSnapshot
  activeAgentId?: string
  onNodeClick?: (cardId: string, label: string) => void
  onConnectNodes?: (sourceCardId: string, targetCardId: string) => void | Promise<void>
  onContextMenu?: (event: { clientX: number; clientY: number; canvasX: number; canvasY: number; nodeId?: string; edge?: TopologyEdgeRef }) => void
  pendingPlacement?: { id: string; x: number; y: number } | null
  onPlacementDone?: () => void
  /** Per-kind edge visibility. Defaults to all visible except wikiword. */
  edgeVisibility?: { parent?: boolean; tag?: boolean; type?: boolean; wikiword?: boolean; depends_on?: boolean; agent_binding?: boolean }
  nodeGraphMode?: NodeGraphMode
  /** Task-card dependency edges ({from: taskId that depends, to: prerequisite}),
   * fetched from project.wiki.list_dependencies. */
  depEdges?: { from: string; to: string }[]
  /** Agent whose brain memory graph should be injected as a subtree. */
  brainMountAgentId?: string | null
  /** Fetched memory snapshot for the mounted agent. */
  brainSnapshot?: MemorySnapshotResp | null
  /** Click handler for memory nodes. */
  onMemoryNodeClick?: (node: MemoryNode) => void
}

export type NodeGraphMode = 'default' | 'created' | 'modified' | 'connections' | 'size'

export function actorIdTimestampMs(actorId: string): number | null {
  if (!/^[0-9a-f]{32}$/i.test(actorId)) return null
  const timestamp = Number.parseInt(actorId.slice(0, 12), 16)
  return Number.isSafeInteger(timestamp) ? timestamp : null
}

export function timeHeatRatio(value: number, minMetric: number, maxMetric: number): number {
  if (maxMetric <= minMetric) return value >= maxMetric ? 1 : 0
  const ratio = Math.max(0, Math.min(1, (value - minMetric) / (maxMetric - minMetric)))
  const curveStrength = 9
  return 1 - Math.log1p(curveStrength * (1 - ratio)) / Math.log1p(curveStrength)
}

export type TopologyEdgeKind = 'parent' | 'tag' | 'type' | 'wikiword' | 'depends_on' | 'agent_binding'

/** Lightweight reference to a rendered edge, passed up with the context-menu
 * event so the shell can offer edge-level actions without re-deriving the
 * relationship from the cards. */
export interface TopologyEdgeRef {
  from: string
  to: string
  kind: TopologyEdgeKind
}

const TOPOLOGY_EDGE_LABELS: Record<TopologyEdgeKind, string> = {
  parent: 'hierarchy',
  tag: 'tag relation',
  type: 'type relation',
  wikiword: 'wiki link',
  depends_on: 'depends on',
  agent_binding: 'agent binding',
}

export interface TopologyEdge {
  from: string
  to: string
  arrows: string
  id?: string
  length?: number
  width?: number
  color?: { color: string; highlight: string }
  /** vis-network dash pattern: false=solid (parent), [6,4]=dashed (tag),
   * [1,4]=dotted (type/system). */
  dashes?: boolean | number[]
  /** Hide the edge from vis-network and draw it manually (used for animated
   * depends_on energy-flow edges while a task is doing). */
  hidden?: boolean
  kind: TopologyEdgeKind
}

/** Build all explicit, tag-derived, and type-driven card relationships for
 * vis-network. Edge styles encode the relationship kind:
 *   - parent: solid (default color)
 *   - tag:    dashed, blue
 *   - type:   dotted, gray/system (card → its __builtin_<type>__ virtual node) */
export function buildTopologyEdges(
  cards: MonoCardListItem[],
  depEdges: { from: string; to: string }[] = [],
  snapshot?: AgentInfoSnapshot,
): TopologyEdge[] {
  const connectedCards = cards.filter(card => !card.standalone)
  const connectedIds = new Set(connectedCards.map(card => card.id))
  const agentIds = new Set(connectedCards.filter(card => card.type === 'agent' || card.data?.componentKind === 'agent').map(card => card.id))
  const edgeSet = new Set<string>()
  const edges: TopologyEdge[] = []
  const addEdge = (
    from: string,
    to: string,
    kind: TopologyEdgeKind,
    opts?: { color?: { color: string; highlight: string }; dashes?: boolean | number[] },
  ) => {
    if (from === to || !connectedIds.has(from) || !connectedIds.has(to)) return
    const id = `${from}→${to}`
    if (edgeSet.has(id)) return
    edgeSet.add(id)
    // Give edges touching an agent card a longer spring length so agent nodes
    // sit farther away from the rest of the topology.
    const touchesAgent = agentIds.has(from) || agentIds.has(to)
    edges.push({ from, to, arrows: 'to', length: touchesAgent ? 180 : undefined, color: opts?.color, dashes: opts?.dashes, kind })
  }

  // Parent (explicit parent field + List) — solid.
  for (const card of connectedCards) {
    if (card.parent) addEdge(card.parent, card.id, 'parent')
    for (const childId of card.list ?? []) addEdge(card.id, childId, 'parent')
  }

  // Tags are card references too. Resolve them via the shared `tagParentIds`
  // helper (same `tagMatchesCard` rules as the knowledge base and the
  // subtree/lineage filters) so the rendered edges and the filters agree on
  // card relationships. Builtin virtual mount nodes are intentionally excluded
  // here because type-driven mounting already creates those edges, keeping the
  // two mechanisms in one paradigm.
  for (const card of connectedCards) {
    for (const parentId of tagParentIds(card, connectedCards)) {
      if (isVirtualMountNode(parentId) || isBuiltinCard(parentId)) continue
      const color = { color: '#3b82f6', highlight: '#60a5fa' }
      addEdge(parentId, card.id, 'tag', { color, dashes: [6, 4] })
    }
  }

  // Type-driven virtual mount: a card with no explicit parent attaches to its
  // __builtin_<type>__ virtual node. Task nodes are per-status. The routing
  // rules are derived from the builtin mount cards (single source of truth =
  // the backend registry) and task-status aliases from the status-map card, so
  // there is no hardcoded mirror to drift. Dotted, gray. System/builtin cards
  // are the mount targets themselves, not mount sources.
  const typeColor = { color: '#9ca3af', highlight: '#6b7280' }
  const mountSpecs = deriveMountSpecs(connectedCards)
  const statusAliases = resolveStatusAliases(connectedCards)
  for (const card of connectedCards) {
    if (isVirtualMountNode(card.id)) continue
    const target = mountTargetForCard(card.type, card.status, mountSpecs, statusAliases)
    if (target) addEdge(target, card.id, 'type', { color: typeColor, dashes: [1, 4] })
  }

  // Wikiword weak connections: [[CardId]] / CamelCase links in a card body
  // create faint edges to the referenced cards. Hidden by default.
  const wikiColor = { color: '#cbd5e1', highlight: '#94a3b8' }
  for (const card of connectedCards) {
    if (!card.raw) continue
    const { body } = splitFrontmatter(card.raw)
    for (const target of extractWikiWordTargets(body)) {
      addEdge(card.id, target, 'wikiword', { color: wikiColor, dashes: [2, 3] })
    }
  }

  // Task-card dependency edges: From depends_on To (To is the prerequisite).
  // Render as orange arrows pointing from prerequisite → dependent.
  // When either endpoint is doing we hide the edge from vis-network and draw an
  // animated energy-flow line ourselves (matching WorkflowGraph's
  // workflow-edge-flow). When both endpoints are done we render a static bright
  // line. This is a pure visual enhancement; the underlying depEdges data model
  // is unchanged.
  const depColor = { color: '#f97316', highlight: '#fb923c' }
  const accentColor = { color: getCssVar('--accent', 'hsl(210 90% 55%)'), highlight: getCssVar('--accent', 'hsl(210 90% 55%)') }
  const normalizeStatus = (status: string | undefined): string =>
    status?.toLowerCase().replace(/[-_]/g, '') ?? ''
  for (const dep of depEdges) {
    // Arrow: prerequisite (To) → dependent (From) = "enables" direction
    const from = dep.to
    const to = dep.from
    if (from === to || !connectedIds.has(from) || !connectedIds.has(to)) continue
    const id = `${from}→${to}:dep`
    if (edgeSet.has(id)) continue
    edgeSet.add(id)

    const fromCard = connectedCards.find(c => c.id === from)
    const toCard = connectedCards.find(c => c.id === to)
    const fromStatus = normalizeStatus(fromCard ? resolveCardStatus(fromCard) : undefined)
    const toStatus = normalizeStatus(toCard ? resolveCardStatus(toCard) : undefined)
    const fromDoing = fromStatus === 'doing'
    const toDoing = toStatus === 'doing'
    const fromDone = fromStatus === 'done' || fromStatus === 'completed'
    const toDone = toStatus === 'done' || toStatus === 'completed'

    if (fromDoing || toDoing) {
      // Active energy-flow: rendered manually in beforeDrawing.
      edges.push({ id, from, to, arrows: 'to', color: accentColor, dashes: [8, 6], hidden: true, kind: 'depends_on' })
    } else if (fromDone && toDone) {
      // Completed: static bright line.
      edges.push({ id, from, to, arrows: 'to', width: 2, color: accentColor, dashes: false, kind: 'depends_on' })
    } else {
      edges.push({ id, from, to, arrows: 'to', color: depColor, dashes: [5, 5], kind: 'depends_on' })
    }
  }

  // Agent bindings: frontmatter data.agentId, runtime BoundTaskCardId, and
  // workflow map ownerAgentId all produce typed cyan edges. Owner-orchestrator
  // bindings use a directional arrow (owner -> root) and a parent-like spring
  // length so the owner agent sits at a hierarchical level consistent with
  // workflow child/task parent-child connections; the other binding kinds use
  // the short undirected spring that pulls agent and bound card together.
  const bindingColor = { color: '#06b6d4', highlight: '#22d3ee' }
  const agentBoundPairs = buildAgentBoundPairs(connectedCards, snapshot)
  for (const pair of agentBoundPairs) {
    const id = `${pair.agentCardId}→${pair.cardId}:binding`
    if (edgeSet.has(id)) continue
    edgeSet.add(id)
    const isOwner = pair.bindingKind === 'owner'
    edges.push({
      id,
      from: pair.agentCardId,
      to: pair.cardId,
      arrows: isOwner ? 'to' : '',
      // Owner edges: default spring length (parent-like). Other bindings:
      // short spring (50) to cluster agent with its bound card.
      length: isOwner ? 180 : 50,
      width: 1,
      color: bindingColor,
      dashes: false,
      kind: 'agent_binding',
    })
  }

  // Agent spawn hierarchy: a spawned worker's workspace ParentAgentId records
  // its logical parent agent (stored as the parent's actor ID). Translate it
  // into a solid hierarchy edge between the two agents' cards, mirroring the
  // runtime agent_child edge shown in the actor topology.
  //
  // A cardless live agent (freshly spawned worker whose mono card hasn't been
  // loaded yet) may enter the graph only through an *anchored* parent: an
  // agent that already has a card in this project or a binding pair to one of
  // its cards. The fixpoint extends anchoring down the spawn chain (owner →
  // worker → sub-worker) while keeping unrelated projects' agents out.
  if (snapshot) {
    const actorToAgentId = new Map<string, string>()
    const snapshotAgentCardIds = new Set<string>()
    for (const a of snapshot.items) {
      if (a.Id) snapshotAgentCardIds.add(agentCardId(a.Id))
      if (a.ActorId && a.Id) actorToAgentId.set(a.ActorId, a.Id)
    }
    const childByParent = new Map<string, string[]>()
    for (const a of snapshot.items) {
      if (!a.Id || !a.ParentAgentId) continue
      const parentAgentId = actorToAgentId.get(a.ParentAgentId) ?? a.ParentAgentId
      if (parentAgentId === a.Id) continue
      const parentCardId = agentCardId(parentAgentId)
      const list = childByParent.get(parentCardId) ?? []
      list.push(agentCardId(a.Id))
      childByParent.set(parentCardId, list)
    }
    const anchored = new Set<string>(connectedIds)
    for (const pair of agentBoundPairs) anchored.add(pair.agentCardId)
    let grew = true
    while (grew) {
      grew = false
      for (const [parentCardId, childIds] of childByParent) {
        if (!anchored.has(parentCardId)) continue
        for (const childId of childIds) {
          if (!connectedIds.has(childId) && !snapshotAgentCardIds.has(childId)) continue
          if (!anchored.has(childId)) {
            anchored.add(childId)
            grew = true
          }
        }
      }
    }
    for (const [parentCardId, childIds] of childByParent) {
      if (!anchored.has(parentCardId)) continue
      for (const childId of childIds) {
        if (!anchored.has(childId)) continue
        const id = `${parentCardId}→${childId}`
        if (edgeSet.has(id)) continue
        edgeSet.add(id)
        edges.push({ from: parentCardId, to: childId, arrows: 'to', length: 180, kind: 'parent' })
      }
    }
  }

  return edges
}

function findStoreAgent(card: MonoCardListItem, snapshot: AgentInfoSnapshot): AgentInfo | undefined {
  const explicitAgentId = typeof card.data?.agentId === 'string' ? card.data.agentId : undefined
  return (explicitAgentId
    ? (snapshot.byId.get(explicitAgentId) ?? snapshot.byActorId.get(explicitAgentId))
    : undefined)
    ?? snapshot.items.find(a => agentCardId(a.Id) === card.id)
    ?? snapshot.byId.get(agentIdFromCardId(card.id))
    ?? snapshot.byActorId.get(agentIdFromCardId(card.id))
}

const CARD_TYPE_HUES: Record<string, number> = {
  note: 210,
  agent: 0,
  skill: 45,
  prompt: 270,
  reminder: 340,
  comment: 200,
}

function cardTypeColor(type: string | undefined, isDark: boolean): string {
  const hue = CARD_TYPE_HUES[type ?? 'note'] ?? CARD_TYPE_HUES.note
  return isDark ? `hsl(${hue} 45% 22%)` : `hsl(${hue} 70% 92%)`
}

function statusColor(status: string | undefined, _isDark: boolean): string | undefined {
  switch (status?.toLowerCase().replace(/[-_]/g, '')) {
    case 'error': return 'hsl(0 75% 55%)'
    case 'running':
    case 'inprogress': return 'hsl(35 90% 55%)'
    case 'waiting': return 'hsl(38 92% 50%)'
    case 'backlog': return 'hsl(215 20% 55%)'
    case 'todo': return 'hsl(210 80% 55%)'
    case 'doing': return 'hsl(38 90% 55%)'
    case 'done':
    case 'completed': return 'hsl(145 70% 45%)'
    case 'blocked': return 'hsl(0 70% 55%)'
    case 'cancelled': return 'hsl(270 60% 60%)'
    case 'askuser': return 'hsl(270 70% 55%)'
    case 'askpermission': return 'hsl(300 70% 55%)'
    case 'planapproval': return 'hsl(190 80% 45%)'
    case 'paused': return 'hsl(220 10% 55%)'
    default: return undefined
  }
}

export interface AgentBoundPair {
  cardId: string
  agentCardId: string
  agentId?: string
  /** Binding source: 'frontmatter' (data.agentId), 'runtime' (BoundTaskCardId),
   * or 'owner' (workflow data.ownerAgentId). Owner bindings get a directional
   * arrow and parent-like spring length to convey hierarchy. */
  bindingKind?: 'frontmatter' | 'runtime' | 'owner'
}

// linkedPartnerForSelection returns the other side of an agent↔task-card
// binding when the selected node participates in one, so selection can
// highlight both ends of the execution pair.
export function linkedPartnerForSelection(selectedId: string, pairs: AgentBoundPair[]): string | undefined {
  for (const p of pairs) {
    if (p.cardId === selectedId) return p.agentCardId
    if (p.agentCardId === selectedId) return p.cardId
  }
  return undefined
}

const agentBoundPairsCache = new WeakMap<MonoCardListItem[], WeakMap<object, AgentBoundPair[]>>()
const agentBoundPairsWithoutSnapshotCache = new WeakMap<MonoCardListItem[], AgentBoundPair[]>()

export function buildAgentBoundPairs(cards: MonoCardListItem[], snapshot?: AgentInfoSnapshot): AgentBoundPair[] {
  if (snapshot) {
    let bySnapshot = agentBoundPairsCache.get(cards)
    if (!bySnapshot) {
      bySnapshot = new WeakMap<object, AgentBoundPair[]>()
      agentBoundPairsCache.set(cards, bySnapshot)
    }
    const cached = bySnapshot.get(snapshot)
    if (cached) return cached
    const pairs = buildAgentBoundPairsUncached(cards, snapshot)
    bySnapshot.set(snapshot, pairs)
    return pairs
  }
  const cached = agentBoundPairsWithoutSnapshotCache.get(cards)
  if (cached) return cached
  const pairs = buildAgentBoundPairsUncached(cards)
  agentBoundPairsWithoutSnapshotCache.set(cards, pairs)
  return pairs
}

function buildAgentBoundPairsUncached(cards: MonoCardListItem[], snapshot?: AgentInfoSnapshot): AgentBoundPair[] {
  const connectedIds = new Set(cards.filter(c => !c.standalone).map(c => c.id))
  // Agents visible through the snapshot (live agents) — they may not have an
  // entity mono card but are still renderable as virtual agent nodes. This set
  // lets push() accept pairs whose agent endpoint is only known via the live
  // store, preventing the silent edge drop described in the workflow-root-owner
  // topology bug.
  const snapshotAgentCardIds = new Set<string>()
  // ActorID → agent.Id resolution map. The workflow data.ownerAgentId stores
  // an actor ID, but the agent external card is keyed by agent.Id. When they
  // differ we must resolve to the correct card id.
  const actorIdToAgentId = new Map<string, string>()
  if (snapshot) {
    for (const agent of snapshot.items) {
      if (agent.Id) snapshotAgentCardIds.add(agentCardId(agent.Id))
      if (agent.ActorId && agent.Id) actorIdToAgentId.set(agent.ActorId, agent.Id)
    }
  }
  const pairs: AgentBoundPair[] = []
  const seen = new Set<string>()
  const push = (
    cardId: string,
    agentCardIdValue: string,
    agentId?: string,
    bindingKind?: 'frontmatter' | 'runtime' | 'owner',
  ) => {
    if (!connectedIds.has(cardId)) return
    // The agent endpoint is visible if it has a mono card (connectedIds) OR
    // exists as a live agent in the snapshot (snapshotAgentCardIds). This
    // prevents silent drops (owner with no card) while still avoiding dangling
    // edges (stale/missing owner not in snapshot or cards).
    if (!connectedIds.has(agentCardIdValue) && !snapshotAgentCardIds.has(agentCardIdValue)) return
    const key = `${cardId}→${agentCardIdValue}`
    if (seen.has(key)) return
    seen.add(key)
    pairs.push({ cardId, agentCardId: agentCardIdValue, agentId, bindingKind })
  }
  for (const card of cards) {
    if (card.standalone) continue
    if (card.type === 'agent' || card.data?.componentKind === 'agent') continue
    const rawAgentId = typeof card.data?.agentId === 'string' ? card.data.agentId : undefined
    if (!rawAgentId) continue
    push(card.id, agentCardId(rawAgentId), rawAgentId, 'frontmatter')
  }
  // Runtime bindings: an agent bound to a task card (goal_card_submit or
  // workflow spawn_assign) carries BoundTaskCardId on the workspace agent
  // projection, sourced from runtime state or the spawn-time mode. The task
  // card itself never gets a data.agentId stamp, so without this pass no
  // execution line would ever be drawn for runtime bindings.
  if (snapshot) {
    for (const card of cards) {
      if (card.standalone) continue
      if (card.type !== 'agent' && card.data?.componentKind !== 'agent') continue
      const agent = findStoreAgent(card, snapshot)
      const boundCardId = agent?.BoundTaskCardId || agent?.Runtime?.BoundTaskCardId
      if (!boundCardId) continue
      push(boundCardId, card.id, agent.Id, 'runtime')
    }
    // Cardless live agents: a spawned workflow worker has no entity mono card
    // until the card list is reloaded (agent spawn emits no card event), so
    // the card-backed pass above never sees it. Scan the snapshot directly so
    // the worker's runtime binding still produces a pair — and, through
    // virtualAgentNodeIds, a virtual node — for the duration of its run.
    for (const agent of snapshot.items) {
      if (!agent.Id) continue
      const agentCard = agentCardId(agent.Id)
      if (connectedIds.has(agentCard)) continue
      const boundCardId = agent.Runtime?.BoundTaskCardId || agent.BoundTaskCardId
      if (!boundCardId) continue
      push(boundCardId, agentCard, agent.Id, 'runtime')
    }
  }
  // Owner-orchestrator bindings: workflow (map) cards declare their owner
  // agent via data.ownerAgentId (an actor ID). Resolve the actor ID to the
  // agent's Id-based card id via the snapshot so the mapping is correct even
  // when the owner has no entity mono card. When the snapshot is unavailable
  // or the owner is stale/missing, push()'s visibility guard prevents a
  // dangling edge.
  for (const card of cards) {
    if (card.standalone) continue
    if (card.type !== 'workflow') continue
    const ownerAgentId = workflowOwnerAgentId(card)
    if (!ownerAgentId) continue
    const resolvedAgentId = actorIdToAgentId.get(ownerAgentId) ?? ownerAgentId
    push(card.id, agentCardId(resolvedAgentId), resolvedAgentId, 'owner')
  }

  return pairs
}

/** Determines which agent-bound pairs should produce virtual agent nodes.
 *
 * Virtual agent nodes are injected for owner agents that have a binding pair
 * but no entity mono card. This function respects the edge visibility toggle:
 * when `agent_binding` edges are hidden, no virtual nodes are produced,
 * preventing orphan dots without connecting lines.
 *
 * Exported so the toggle-respecting behavior can be unit-tested independently
 * of the component's rendering pipeline.
 */
export function virtualAgentNodePairs(
  pairs: AgentBoundPair[],
  cardNodeIds: Set<string>,
  edgeVisibility?: Record<string, boolean>,
): AgentBoundPair[] {
  if (edgeVisibility?.agent_binding === false) return []
  return pairs.filter(pair => !cardNodeIds.has(pair.agentCardId))
}

/** Full set of agent card ids that need virtual nodes: binding-pair agents
 * (owner/worker bindings to project cards) plus snapshot agents referenced by
 * spawn-hierarchy parent edges. Both families respect their edge visibility
 * toggle so no orphan dots appear when the connecting lines are hidden.
 *
 * Exported so the toggle-respecting behavior can be unit-tested independently
 * of the component's rendering pipeline.
 */
export function virtualAgentNodeIds(
  pairs: AgentBoundPair[],
  topologyEdges: TopologyEdge[],
  cardNodeIds: Set<string>,
  snapshotAgentCardIds: Set<string>,
  edgeVisibility?: Record<string, boolean>,
): string[] {
  const out = new Set<string>()
  for (const pair of virtualAgentNodePairs(pairs, cardNodeIds, edgeVisibility)) out.add(pair.agentCardId)
  if (edgeVisibility?.parent !== false) {
    for (const e of topologyEdges) {
      if (e.kind !== 'parent') continue
      if (!cardNodeIds.has(e.from) && snapshotAgentCardIds.has(e.from)) out.add(e.from)
      if (!cardNodeIds.has(e.to) && snapshotAgentCardIds.has(e.to)) out.add(e.to)
    }
  }
  return [...out]
}

function resolveCardStatus(card: MonoCardListItem): string | undefined {
  return card.status ?? (typeof card.data?.status === 'string' ? card.data.status : undefined)
}

function agentBoundColor(card: MonoCardListItem, agent: AgentInfo | undefined, isActive: boolean, isDark: boolean): string {
  const status = resolveCardStatus(card)
  if (status?.toLowerCase() === 'error' || agent?.IsError) return statusColor('error', isDark)!
  if (isActive || agent?.IsWorking) return isDark ? 'hsl(210 80% 55%)' : 'hsl(210 90% 55%)'
  if (status && ['running', 'in-progress', 'in_progress'].includes(status.toLowerCase())) return statusColor('running', isDark)!
  if (status && ['completed', 'done'].includes(status.toLowerCase())) return statusColor('completed', isDark)!
  return isDark ? 'hsl(220 10% 45%)' : 'hsl(220 10% 65%)'
}

const cardByIdCache = new WeakMap<readonly MonoCardListItem[], Map<string, MonoCardListItem>>()

/** Reuse the status lookup index throughout animation frames until card data changes. */
export function cardByIdForCards(cards: readonly MonoCardListItem[]): Map<string, MonoCardListItem> {
  let cardById = cardByIdCache.get(cards)
  if (!cardById) {
    cardById = new Map(cards.map(card => [card.id, card]))
    cardByIdCache.set(cards, cardById)
  }
  return cardById
}

/** Keep curves smooth without oversampling short active edges every animation frame. */
export function dependsOnEdgeSampleCount(length: number): number {
  return Math.max(8, Math.min(40, Math.round(length / 10)))
}

interface DependsOnEdgeVisuals {
  accent: string
  isDark: boolean
  canGlow: boolean
}

function resolveDependsOnEdgeVisuals(): DependsOnEdgeVisuals {
  return {
    accent: getCssVar('--accent', 'hsl(210 90% 55%)'),
    isDark: document.documentElement.getAttribute('data-theme') === 'dark',
    canGlow: !window.matchMedia?.('(pointer: coarse)').matches,
  }
}

/** Draw animated energy-flow depends_on edges that vis-network is asked to hide.
 * Mirrors WorkflowGraph's workflow-edge-flow (WorkflowGraph.css:56) on the
 * canvas: accent-colored dashed line whose dash offset animates toward the
 * target. Returns true if any active (doing) edge was drawn so the caller can
 * keep the render loop alive. */
function drawDependsOnEdges(
  ctx: CanvasRenderingContext2D,
  network: Network,
  cards: readonly MonoCardListItem[],
  visuals: DependsOnEdgeVisuals,
  time: number,
): boolean {
  const { accent, isDark } = visuals
  const cardById = cardByIdForCards(cards)
  const body = (network as unknown as { body: { edges: Record<string, any> } }).body
  let hasActive = false

  for (const edgeId in body.edges) {
    const edge = body.edges[edgeId]
    if (edge.options?.kind !== 'depends_on') continue
    if (edge.options?.hidden !== true) continue

    const fromId = String(edge.from?.id ?? '')
    const toId = String(edge.to?.id ?? '')
    const fromCard = cardById.get(fromId)
    const toCard = cardById.get(toId)
    if (!fromCard || !toCard) continue

    const fromStatus = resolveCardStatus(fromCard)?.toLowerCase().replace(/[-_]/g, '')
    const toStatus = resolveCardStatus(toCard)?.toLowerCase().replace(/[-_]/g, '')
    const isDoing = fromStatus === 'doing' || toStatus === 'doing'
    if (!isDoing) continue
    hasActive = true

    const { from, to } = edge.findBorderPositions(ctx)

    ctx.save()
    // Glow is expensive on mobile GPUs and unreadable at low zoom, so retain it
    // only for precise-pointer views where the current scale can show it.
    if (visuals.canGlow && network.getScale() >= 0.8) {
      ctx.shadowColor = accent
      ctx.shadowBlur = isDark ? 10 : 8
    }
    ctx.strokeStyle = accent
    ctx.lineWidth = 2
    ctx.lineCap = 'round'
    ctx.setLineDash([8, 6])
    // Match CSS workflow-edge-flow: 0.8s linear infinite over a 14px dash period.
    ctx.lineDashOffset = -((time % 800) / 800) * 14

    ctx.beginPath()
    ctx.moveTo(from.x, from.y)
    const samples = dependsOnEdgeSampleCount(Math.hypot(to.x - from.x, to.y - from.y))
    for (let i = 1; i < samples; i++) {
      const p = edge.getPoint(i / samples)
      ctx.lineTo(p.x, p.y)
    }
    ctx.lineTo(to.x, to.y)
    ctx.stroke()

    // Arrow head at the target border, aligned with the curve tangent.
    const nearEnd = edge.getPoint(0.95)
    const angle = Math.atan2(to.y - nearEnd.y, to.x - nearEnd.x)
    const headSize = 7
    ctx.translate(to.x, to.y)
    ctx.rotate(angle)
    ctx.beginPath()
    ctx.moveTo(0, 0)
    ctx.lineTo(-headSize, -headSize / 2)
    ctx.lineTo(-headSize, headSize / 2)
    ctx.closePath()
    ctx.fillStyle = accent
    ctx.fill()
    ctx.restore()
  }

  return hasActive
}

function getCssVar(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
}

// Complementary (hue+180) of a CSS color string. Used to recolor the working
// ring so it reads against the accent-colored avatar background regardless of
// theme. Accepts #rgb / #rrggbb hex or rgb()/rgba() functional forms.
function complementaryColor(color: string): string {
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(color.trim())
  const rgb = /^rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)/i.exec(color.trim())
  let r = 0, g = 0, b = 0
  if (hex) {
    const hh = hex[1]
    if (!hh) return '#ff7a00'
    let h = hh
    if (h.length === 3) h = h.split('').map(c => c + c).join('')
    const n = parseInt(h, 16)
    r = (n >> 16) & 255; g = (n >> 8) & 255; b = n & 255
  } else if (rgb) {
    const rr = rgb[1], gg = rgb[2], bb = rgb[3]
    if (rr == null || gg == null || bb == null) return '#ff7a00'
    r = parseFloat(rr); g = parseFloat(gg); b = parseFloat(bb)
  } else {
    return '#ff7a00'
  }
  const rn = r / 255, gn = g / 255, bn = b / 255
  const max = Math.max(rn, gn, bn), min = Math.min(rn, gn, bn)
  let h = 0
  if (max !== min) {
    const d = max - min
    if (max === rn) h = ((gn - bn) / d + (gn < bn ? 6 : 0))
    else if (max === gn) h = ((bn - rn) / d + 2)
    else h = ((rn - gn) / d + 4)
    h *= 60
  }
  h = (h + 180) % 360
  // Soften: cap saturation and lift lightness so the warm contrast against the
  // blue avatar reads without the neon glare of a full-saturation complement.
  return `hsl(${Math.round(h)} 50% 73%)`
}

function isWorkingStatus(status: string): status is AgentStatus {
  return status === 'running' || status === 'ask_user' || status === 'ask_permission' || status === 'plan_approval' || status === 'goal_submit'
}

function fallbackAgentFromCard(card: MonoCardListItem): AgentInfo {
  const status = (typeof card.data?.agentStatus === 'string' ? card.data.agentStatus : 'idle') as AgentStatus
  const id = agentIdFromCardId(card.id)
  const displayName = (typeof card.data?.agentDisplayName === 'string' && card.data.agentDisplayName)
    ? card.data.agentDisplayName
    : (typeof card.data?.displayName === 'string' && card.data.displayName)
    ? card.data.displayName
    : card.id
  const title = (typeof card.data?.agentTitle === 'string' && card.data.agentTitle)
    ? card.data.agentTitle
    : (typeof card.data?.title === 'string' && card.data.title)
    ? card.data.title
    : card.id
  return {
    Id: id,
    ActorId: '',
    DisplayName: displayName,
    Title: title,
    HasTitle: true,
    AgentKind: 'worker',
    ProjectId: '',
    ProjectName: '',
    Primary: undefined,
    Fast: undefined,
    ThinkLevel: undefined,
    Status: status,
    StatusLabel: status,
    IsWorking: isWorkingStatus(status),
    IsError: status === 'error',
    IsCompleted: status === 'completed',
    IsAskUserPermission: false,
    IsAskUser: status === 'ask_user',
    IsAskPermission: status === 'ask_permission',
    IsPlanApproval: status === 'plan_approval',
    IsGoalSubmit: status === 'goal_submit',
    ErrorMessage: undefined,
    ActiveTurnRef: undefined,
    LastActivity: undefined,
    LastTurnCompletedAt: undefined,
    Degraded: false,
    DegradedReason: undefined,
    Runtime: undefined,
    CompactionPolicy: undefined,
    CompactionPolicyLoading: false,
    CompactionPolicyError: undefined,
    CompactionPolicySource: undefined,
    CanDelete: false,
  }
}

function displayAgentForCard(card: MonoCardListItem, storeAgent: AgentInfo | undefined): AgentInfo {
  // Always prefer the live agent info store (which reacts to turn/role events)
  // over the possibly stale card.data.agentStatus.
  return storeAgent ?? fallbackAgentFromCard(card)
}

export const TopologyGraph = memo(function TopologyGraph({ cards, agentInfoSnapshot, activeAgentId, onNodeClick, onConnectNodes, onContextMenu, pendingPlacement, onPlacementDone, edgeVisibility, depEdges, brainMountAgentId, brainSnapshot, onMemoryNodeClick, nodeGraphMode = 'default' }: TopologyGraphProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const networkRef = useRef<Network | null>(null)
  const connectDragRef = useRef<{ sourceId: string; pointer: { x: number; y: number }; initialPointer: { x: number; y: number } } | null>(null)
  const connectDragOccurredRef = useRef(false)
  const suppressClickRef = useRef(false)
  const { t } = useI18n()
  const onNodeClickRef = useRef(onNodeClick)
  onNodeClickRef.current = onNodeClick
  const onConnectNodesRef = useRef(onConnectNodes)
  onConnectNodesRef.current = onConnectNodes
  const onContextMenuRef = useRef(onContextMenu)
  onContextMenuRef.current = onContextMenu
  const onMemoryNodeClickRef = useRef(onMemoryNodeClick)
  onMemoryNodeClickRef.current = onMemoryNodeClick
  const pendingPlacementRef = useRef(pendingPlacement)
  pendingPlacementRef.current = pendingPlacement
  const onPlacementDoneRef = useRef(onPlacementDone)
  onPlacementDoneRef.current = onPlacementDone
  const cardsRef = useRef(cards)
  cardsRef.current = cards
  const agentInfoSnapshotRef = useRef(agentInfoSnapshot)
  agentInfoSnapshotRef.current = agentInfoSnapshot
  const activeAgentIdRef = useRef(activeAgentId)
  activeAgentIdRef.current = activeAgentId
  const brainSnapshotRef = useRef(brainSnapshot)
  brainSnapshotRef.current = brainSnapshot

  // Reactively track theme (data-theme attribute) changes so node/edge/avatar
  // colors can be pushed into the live network instance without recreating it.
  const [themeKey, setThemeKey] = useState(0)
  const dependsOnEdgeVisualsRef = useRef<DependsOnEdgeVisuals>({
    accent: 'hsl(210 90% 55%)',
    isDark: false,
    canGlow: false,
  })
  useEffect(() => {
    const syncTheme = () => {
      dependsOnEdgeVisualsRef.current = resolveDependsOnEdgeVisuals()
      setThemeKey(k => k + 1)
    }
    syncTheme()
    const observer = new MutationObserver(syncTheme)
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    return () => observer.disconnect()
  }, [])

  const { nodes, edges, agentBoundPairs, labelColor } = useMemo(() => {
    const labelColor = getCssVar('--text-primary', '#e2e8f0')
    const connectedCards = cards.filter(card => !card.standalone)
    // Guard against duplicate card ids. monoState.cards can hold two entries
    // sharing one id when the card-changed and file-changed watchers both fire
    // for a write whose file slug differs from the card's frontmatter `id:`
    // title (the slug-keyed dedup in the store misses, both watchers prepend).
    // vis DataSet throws on duplicate node ids, so keep the first per id.
    const seenIds = new Set<string>()
    const dedupedCards = connectedCards.filter(card => {
      if (seenIds.has(card.id)) return false
      seenIds.add(card.id)
      return true
    })
    const isDark = document.documentElement.getAttribute('data-theme') === 'dark'
    const borderColor = getCssVar('--border-default', '#475569')
    const accentPrimaryColor = getCssVar('--accent-primary', '#3b82f6')
    const snapshot = agentInfoSnapshot ?? getAgentInfoSnapshot()
    const activeAgentCardId = activeAgentId ? agentCardId(activeAgentId) : undefined
    const agentBoundPairs = buildAgentBoundPairs(dedupedCards, snapshot)
    const boundCardToAgent = new Map(agentBoundPairs.map(p => [p.cardId, p]))
    const agentInfoByCardId = new Map<string, AgentInfo | undefined>()
    for (const pair of agentBoundPairs) {
      if (!agentInfoByCardId.has(pair.agentCardId)) {
        agentInfoByCardId.set(
          pair.agentCardId,
          snapshot.items.find(a => agentCardId(a.Id) === pair.agentCardId) ??
            snapshot.byId.get(agentIdFromCardId(pair.agentCardId)) ??
            snapshot.byActorId.get(agentIdFromCardId(pair.agentCardId)),
        )
      }
    }

    // Nodes that parent more children render larger and carry more mass so
    // hub nodes anchor their subtrees in the force layout.
    const childMap = buildChildMap(connectedCards)
    const childCountOf = (id: string): number => childMap.get(id)?.size ?? 0
    const topologyEdges = buildTopologyEdges(dedupedCards, depEdges ?? [], snapshot)
    const degreeMap = new Map<string, number>()
    for (const edge of topologyEdges) {
      degreeMap.set(edge.from, (degreeMap.get(edge.from) ?? 0) + 1)
      degreeMap.set(edge.to, (degreeMap.get(edge.to) ?? 0) + 1)
    }
    const parseCardDate = (value: string | undefined): number | null => {
      if (!value) return null
      const parsed = Date.parse(value)
      return Number.isFinite(parsed) ? parsed : null
    }
    const metricFor = (card: MonoCardListItem) => {
      if (nodeGraphMode === 'connections') return degreeMap.get(card.id) ?? 0
      if (nodeGraphMode === 'size') return cardVisual(card).size ?? 24
      const isAgent = card.type === 'agent' || card.data?.componentKind === 'agent'
      const agentCreatedAt = nodeGraphMode === 'created' && isAgent
        ? actorIdTimestampMs(findStoreAgent(card, snapshot)?.ActorId ?? '')
        : null
      const primary = nodeGraphMode === 'created' ? card.created : card.modified
      return agentCreatedAt ?? parseCardDate(primary) ?? parseCardDate(nodeGraphMode === 'created' ? card.modified : card.created)
    }

    const metrics = dedupedCards
      .map(metricFor)
      .filter((metric): metric is number => metric != null)
    const isTimeHeatmap = nodeGraphMode === 'created' || nodeGraphMode === 'modified'
    const maxMetric = isTimeHeatmap ? Date.now() : (metrics.length ? Math.max(...metrics) : 1)
    const minMetric = isTimeHeatmap
      ? maxMetric - 30 * 24 * 60 * 60 * 1000
      : (metrics.length ? Math.min(...metrics) : 0)
    const heatColor = (value: number) => {
      const ratio = timeHeatRatio(value, minMetric, maxMetric)
      return `hsl(${Math.round(220 - ratio * 220)} 82% ${Math.round(48 + ratio * 8)}%)`
    }
    const heatColorFor = (card: MonoCardListItem) => {
      const metric = metricFor(card)
      return metric == null ? undefined : heatColor(metric)
    }
    const isHeatmap = nodeGraphMode !== 'default'

    const nodes = dedupedCards.map(card => {
      const localized = overrideBuiltinTitle(card, t)
      const isAgent = localized.type === 'agent' || localized.data?.componentKind === 'agent'
      const status = resolveCardStatus(card)
      const statusBorder = statusColor(status, isDark)
      const nodeHeatColor = isHeatmap ? heatColorFor(card) : undefined
      if (isAgent) {
        // Look up live agent info from the store so avatars reflect real-time
        // status changes (running, paused, error, etc.) even if the card data
        // hasn't been refreshed yet.
        // Match the live agent using the same rules as the sidebar context menu.
        // Card IDs are sanitized slugs, so we also try reverse lookup via agentCardId.
        const storeAgent = findStoreAgent(card, snapshot)
        const liveStatus = storeAgent?.Status ?? (typeof localized.data?.agentStatus === 'string' ? localized.data.agentStatus : undefined)
        const displayName = storeAgent?.DisplayName
          ?? (typeof localized.data?.agentDisplayName === 'string' ? localized.data.agentDisplayName : undefined)
          ?? (typeof localized.data?.displayName === 'string' ? localized.data.displayName : undefined)
          ?? localized.id
        const agentTitle = storeAgent?.Title ?? localized.id
        const isActive = activeAgentCardId === card.id
        const agentBorder = isActive ? (isDark ? 'hsl(210 80% 55%)' : 'hsl(210 90% 55%)') : (statusBorder ?? borderColor)
        const { size, mass } = sizeAndMassForChildCount(childCountOf(card.id), { size: 28, mass: 3 })
        return {
          id: card.id,
          label: agentTitle,
          title: agentTitle,
          shape: isHeatmap ? 'dot' as const : 'circularImage' as const,
          image: isHeatmap ? undefined : agentAvatarSvg(card.id, displayName, storeAgent?.AgentKind, liveStatus, isActive, storeAgent?.LoadState === 'loaded'),
          size,
          mass,
          borderWidth: isActive ? 0 : (statusBorder ? 1.5 : 1),
          color: {
            background: nodeHeatColor ?? 'transparent',
            border: nodeHeatColor ?? (isActive ? 'transparent' : agentBorder),
            highlight: { background: nodeHeatColor ?? 'transparent', border: nodeHeatColor ?? (isActive ? (isDark ? 'hsl(210 80% 55%)' : 'hsl(210 90% 55%)') : borderColor) },
          },
          font: { size: 12, color: nodeHeatColor ? labelColor : (isActive ? accentPrimaryColor : labelColor), face: 'system-ui, -apple-system, sans-serif' },
        }
      }
      const bound = boundCardToAgent.get(card.id)
      const isActiveBound = bound && activeAgentCardId === bound.agentCardId
      const lineColor = isActiveBound && bound
        ? agentBoundColor(card, agentInfoByCardId.get(bound.agentCardId), true, isDark)
        : statusBorder
      const visual = cardVisual(card)
      const visualStyle = getCardVisualStyle(visual, isDark ? 'dark' : 'light')
      // Apply the visual theme whenever the card carries any visual metadata
      // (icon, accent, emphasis, or explicit colors). Previously this only
      // checked accent/background/border, so a card with just an icon fell
      // back to the default type color and rendered the icon on the wrong
      // background after a reload.
      const hasVisual = !!visual.icon || !!visual.accent || !!visual.emphasis || !!visual.background || !!visual.border || !!visual.color
      const defaultBgColor = hasVisual ? visualStyle.background : cardTypeColor(localized.type, isDark)
      const bgColor = nodeHeatColor ?? defaultBgColor
      const nodeBorder = nodeHeatColor ?? (hasVisual ? visualStyle.border : (lineColor ?? borderColor))
      const image = visual.icon && visual.iconType !== 'emoji' ? cardIconSvg(visual.icon, visualStyle.icon, bgColor) : undefined
      // Builtin mount nodes are structural hubs; their size is fixed at 1.5x
      // the base (scaled down from 2x to 0.75 of that) instead of scaling with
      // childCount, so a heavily-connected hub doesn't balloon and visually
      // dominate the canvas.
      const baseSize = visual.size ?? 24
      const isMount = isVirtualMountNode(card.id)
      const defaultSize = isMount
        ? baseSize * 2 * 0.75
        : sizeAndMassForChildCount(childCountOf(card.id), { size: baseSize, mass: 1 }).size
      const size = nodeGraphMode === 'connections'
        ? baseSize + Math.min(40, (degreeMap.get(card.id) ?? 0) * 4)
        : defaultSize
      const mass = isMount ? 1 : 1
      return {
        id: card.id,
        label: visual.iconType === 'emoji' && visual.icon ? visual.icon : (localized.title ?? localized.id),
        title: localized.title ?? localized.id,
        shape: image ? 'circularImage' as const : 'dot' as const,
        image,
        size,
        mass,
        color: {
          background: bgColor,
          border: nodeBorder,
          highlight: { background: bgColor, border: nodeBorder },
        },
        borderWidth: visualStyle.borderWidth,
        font: { size: 12, color: visualStyle.title, face: 'system-ui, -apple-system, sans-serif' },
      }
    })

    // Suppress generic tag edges that duplicate explicit agent-bound connections.
    const boundSet = new Set(agentBoundPairs.flatMap(p => [`${p.cardId}→${p.agentCardId}`, `${p.agentCardId}→${p.cardId}`]))
    const vis = edgeVisibility ?? {}
    const edges = buildTopologyEdges(dedupedCards, depEdges ?? [], snapshot)
      // Preserve explicit ids (e.g. depends_on :dep suffix) so custom renderers
      // can identify edge kind reliably. Falls back to from→to for edges
      // without an explicit id.
      .map(e => ({ ...e, id: e.id ?? `${e.from}→${e.to}` }))
      .filter(e => !boundSet.has(e.id))
      .filter(e => vis[e.kind] !== false)
      // The kind text rides on the hover tooltip (title); the visible label is
      // only applied while the edge is selected (see syncEdgeSelectionLabels).
      .map(e => ({ ...e, title: TOPOLOGY_EDGE_LABELS[e.kind] }))

    // Inject virtual agent nodes for live agents that have a binding pair or
    // a spawn-hierarchy edge but no entity mono card in the cards list (e.g. a
    // freshly spawned workflow worker — agent spawn emits no card event, so
    // its card only appears on the next full list reload). Without this, the
    // edges would reference a non-existent node id and vis-network would
    // silently drop both the node and the edge. The virtual node reuses the
    // same avatar/styling as regular agent nodes and disappears automatically
    // when the agent leaves the snapshot (teardown).
    //
    // This injection must respect the edge visibility toggles: when the user
    // hides agent_binding or parent edges, the corresponding virtual nodes
    // must also disappear, otherwise orphan dots without connecting lines
    // appear.
    const cardNodeIds = new Set(nodes.map(n => n.id))
    const snapshotAgentCardIds = new Set<string>()
    for (const a of snapshot.items) {
      if (a.Id) snapshotAgentCardIds.add(agentCardId(a.Id))
    }
    const virtualAgentNodes = virtualAgentNodeIds(agentBoundPairs, topologyEdges, cardNodeIds, snapshotAgentCardIds, vis)
      .map(agentNodeId => {
        const agent =
          snapshot.items.find(a => agentCardId(a.Id) === agentNodeId) ??
          snapshot.byId.get(agentIdFromCardId(agentNodeId)) ??
          snapshot.byActorId.get(agentIdFromCardId(agentNodeId))
        const displayName = agent?.DisplayName ?? agentNodeId
        const agentTitle = agent?.Title ?? agentNodeId
        const isActive = activeAgentCardId === agentNodeId
        const { size, mass } = sizeAndMassForChildCount(0, { size: 28, mass: 3 })
        return {
          id: agentNodeId,
          label: agentTitle,
          title: agentTitle,
          shape: 'circularImage' as const,
          image: agentAvatarSvg(agentNodeId, displayName, agent?.AgentKind, agent?.Status, isActive, agent?.LoadState === 'loaded'),
          size,
          mass,
          borderWidth: isActive ? 0 : 1,
          color: {
            background: 'transparent',
            border: isActive ? 'transparent' : borderColor,
            highlight: { background: 'transparent', border: borderColor },
          },
          font: { size: 12, color: isActive ? accentPrimaryColor : labelColor, face: 'system-ui, -apple-system, sans-serif' },
        }
      })

    // Inject brain memory nodes as a subtree of the mounted agent node.
    const allNodes = [...nodes, ...virtualAgentNodes]
    const allEdges = [...edges]
    if (brainMountAgentId && brainSnapshot && brainSnapshot.Nodes.length > 0) {
      const rootCardId = agentCardId(brainMountAgentId)
      const prefix = `brain:${brainMountAgentId}:`
      const brainNodeColor: Record<string, string> = {
        session: '#38bdf8',
        experience: '#a78bfa',
        ontology: '#f59e0b',
      }
      const brainEdgeColor: Record<string, string> = {
        peer: '#60a5fa',
        cross: '#a78bfa',
        antagonist: '#ef4444',
      }
      for (const mn of brainSnapshot.Nodes) {
        const nid = prefix + mn.Id
        const label = (mn.Head || mn.Content || mn.Id)
        allNodes.push({
          id: nid,
          label: label.length > 40 ? label.slice(0, 37) + '…' : label,
          title: `${label}\nLayer: ${mn.Layer} · Energy: ${mn.Energy.toFixed(1)} · Tokens: ${mn.Tokens}`,
          shape: 'dot' as const,
          image: undefined,
          size: 8 + Math.min(20, Math.max(0, mn.Energy) * 6),
          mass: 0.5,
          color: {
            background: mn.MarkedForDeath ? '#64748b' : (brainNodeColor[mn.Layer] ?? '#94a3b8'),
            border: mn.MarkedForDeath ? '#ef4444' : (brainNodeColor[mn.Layer] ?? '#94a3b8'),
            highlight: { background: brainNodeColor[mn.Layer] ?? '#94a3b8', border: brainNodeColor[mn.Layer] ?? '#94a3b8' },
          },
          borderWidth: mn.MarkedForDeath ? 3 : 1,
          font: { size: 10, color: labelColor, face: 'system-ui, -apple-system, sans-serif' },
        })
        // Edge from agent root → brain node
        allEdges.push({
          id: `${rootCardId}→${nid}`,
          from: rootCardId,
          to: nid,
          arrows: '',
          length: 60,
          color: { color: brainNodeColor[mn.Layer] ?? '#64748b', highlight: brainNodeColor[mn.Layer] ?? '#64748b' },
          dashes: [2, 4],
          width: 0.5,
          kind: 'parent',
          title: 'brain memory',
        })
      }
      // Inter-brain-node edges (peer/cross/antagonist)
      for (const e of brainSnapshot.Edges) {
        const fromId = prefix + e.From
        const toId = prefix + e.To
        allEdges.push({
          id: `${fromId}→${toId}:brain:${e.From}:${e.To}:${e.Type}`,
          from: fromId,
          to: toId,
          arrows: e.Type === 'antagonist' ? 'to, from' : '',
          color: { color: brainEdgeColor[e.Type] ?? '#64748b', highlight: brainEdgeColor[e.Type] ?? '#64748b' },
          dashes: e.Type === 'antagonist' ? [6, 4] : false,
          width: 1,
          kind: 'parent',
          title: `brain ${e.Type}`,
        })
      }
    }

    return { nodes: allNodes, edges: allEdges, agentBoundPairs, labelColor }
  }, [cards, t, themeKey, agentInfoSnapshot, activeAgentId, edgeVisibility, nodeGraphMode, depEdges, brainMountAgentId, brainSnapshot])

  const nodesRef = useRef(nodes)
  nodesRef.current = nodes
  const edgesRef = useRef(edges)
  edgesRef.current = edges
  const agentBoundPairsRef = useRef(agentBoundPairs)
  agentBoundPairsRef.current = agentBoundPairs
  const syncEdgeLabelsRef = useRef(() => {})

  const [selectedCardId, setSelectedCardId] = useState<string | null>(null)
  const selectedCardIdRef = useRef(selectedCardId)
  selectedCardIdRef.current = selectedCardId
  const selectionOverlayRef = useRef<HTMLDivElement | null>(null)
  const linkedOverlayRef = useRef<HTMLDivElement | null>(null)
  const selectionOverlayLayerTransformRef = useRef('')
  const selectionOverlaySignaturesRef = useRef(new WeakMap<HTMLDivElement, string>())

  // Create the network once and wire up event listeners. The network is only
  // destroyed on unmount, so scrolling or other parent re-renders don't cause
  // the graph to reset and flicker.
  useEffect(() => {
    if (!containerRef.current) return

    const network = new Network(
      containerRef.current,
      { nodes, edges },
      {
        nodes: {
          shape: 'dot',
          size: 12,
          borderWidth: 1,
          font: { size: 12, color: labelColor, face: 'system-ui, -apple-system, sans-serif' },
          shadow: false,
        },
        edges: {
          width: 1,
          color: { color: getCssVar('--border-default', '#475569'), highlight: getCssVar('--accent-primary', '#3b82f6') },
          arrows: { to: { scaleFactor: 0.6 } },
          smooth: { enabled: true, type: 'continuous', roundness: 0.5 },
        },
        physics: {
          enabled: true,
          solver: 'forceAtlas2Based',
          // forceAtlas2Based never converges on its own: with the default
          // damping (0.4) and minVelocity (0.5) the system's kinetic energy
          // rarely drops below the stop threshold on multi-node graphs, so it
          // keeps oscillating. Higher damping dissipates energy fast, avoidOverlap
          // stops mutual node-pushing, and a higher minVelocity lets the engine
          // declare "stable" sooner and halt the live simulation.
          forceAtlas2Based: {
            gravitationalConstant: -50,
            centralGravity: 0.005,
            springLength: 100,
            springConstant: 0.08,
            damping: 0.9,
            avoidOverlap: 0.5,
          },
          maxVelocity: 30,
          minVelocity: 1.0,
          timestep: 0.35,
          stabilization: { enabled: true, iterations: 1000 },
        },
        layout: { randomSeed: 2 },
        interaction: { hover: true, tooltipDelay: 200, zoomView: true, dragView: true },
        // Disable vis-network's built-in autoResize. Its ResizeObserver calls
        // setSize() (which clears the canvas) then _requestRedraw() (deferred to
        // the NEXT animation frame), leaving a one-frame blank gap that causes
        // visible flicker during continuous resize (e.g. sidebar drag). We
        // handle resize manually below with a synchronous setSize + redraw.
        autoResize: false,
      },
    )

    networkRef.current = network

    // Vis-network clears the container on create, so the overlay layer must be
    // appended dynamically after initialization.
    const overlay = document.createElement('div')
    overlay.className = 'topology-agent-overlay-layer'
    containerRef.current.appendChild(overlay)
    agentOverlayRef.current = overlay

    // Selection reticle overlay: four rounded corner brackets marking the
    // selected node. Opening a node is done by clicking it again.
    const selectionOverlay = document.createElement('div')
    selectionOverlay.className = 'topology-selection-overlay'
    selectionOverlay.innerHTML = `
      <svg class="topology-selection-frame" viewBox="0 0 100 100" aria-hidden="true">
        <path d="M 22 2 L 10 2 Q 2 2 2 10 L 2 22" />
        <path d="M 78 2 L 90 2 Q 98 2 98 10 L 98 22" />
        <path d="M 2 78 L 2 90 Q 2 98 10 98 L 22 98" />
        <path d="M 78 98 L 90 98 Q 98 98 98 90 L 98 78" />
      </svg>
    `
    containerRef.current.appendChild(selectionOverlay)
    selectionOverlayRef.current = selectionOverlay

    // Linked-partner reticle: dashed brackets marking the bound partner node
    // (agent ↔ task card) of the current selection.
    const linkedOverlay = document.createElement('div')
    linkedOverlay.className = 'topology-selection-overlay topology-linked-overlay'
    linkedOverlay.innerHTML = selectionOverlay.innerHTML
    containerRef.current.appendChild(linkedOverlay)
    linkedOverlayRef.current = linkedOverlay

    // Manual resize handling: network.redraw() calls setSize() (clears canvas)
    // and _redraw() synchronously in the same call, so the browser never paints
    // a blank frame between clear and redraw.
    const resizeObserver = new ResizeObserver(() => {
      network.redraw()
    })
    resizeObserver.observe(containerRef.current)

    const openSelectedNode = (id: string) => {
      // Brain memory node click — route to onMemoryNodeClick.
      if (id.startsWith('brain:')) {
        const parts = id.split(':')
        const nodeId = parts.slice(2).join(':')
        const snap = brainSnapshotRef.current
        const mn = snap?.Nodes.find(n => n.Id === nodeId)
        if (mn) onMemoryNodeClickRef.current?.(mn)
        return
      }
      const node = nodesRef.current.find(n => n.id === id)
      if (cardsRef.current.some(card => card.id === id && (card.type === 'agent' || card.data?.componentKind === 'agent'))) {
        const card = cardsRef.current.find(c => c.id === id)
        const realAgentId = typeof card?.data?.agentId === 'string'
          ? card.data.agentId
          : agentIdFromCardId(id)
        onNodeClickRef.current?.(`agent:${realAgentId}`, node?.label ?? id)
      } else {
        onNodeClickRef.current?.(id, node?.label ?? id)
      }
    }

    network.on('click', (params) => {
      if (suppressClickRef.current) {
        suppressClickRef.current = false
        return
      }
      if (params.nodes && params.nodes.length > 0) {
        const id = String(params.nodes[0])
        // Click an already-selected node to open it; otherwise just select it.
        // (Agent nodes follow the same rule; opening routes via openSelectedNode.)
        if (selectedCardIdRef.current === id) {
          openSelectedNode(id)
        } else {
          setSelectedCardId(id)
        }
      } else {
        setSelectedCardId(null)
      }
    })

    network.on('doubleClick', (params) => {
      if (params.nodes && params.nodes.length > 0) {
        const id = String(params.nodes[0])
        setSelectedCardId(id)
        openSelectedNode(id)
      }
    })

    // Edge labels are selection-gated: the kind text lives on the hover
    // tooltip (title) and is promoted to a visible label only while the edge
    // is selected. Runs on every selection change and after each data sync
    // (an edge removed from and re-added to the DataSet loses its promoted
    // label, so the sync effect re-invokes this to reconcile).
    const edgeLabelState = new Map<string, string>()
    const syncEdgeSelectionLabels = () => {
      const { edges: edgesDS } = getInternalDataSets(network)
      if (!edgesDS) return
      const selected = new Set(network.getSelectedEdges().map(String))
      const updates: Record<string, unknown>[] = []
      const seen = new Set<string>()
      for (const e of edgesRef.current) {
        const id = String(e.id)
        seen.add(id)
        const want = selected.has(id) && e.title ? e.title : ''
        if ((edgeLabelState.get(id) ?? '') !== want) {
          edgeLabelState.set(id, want)
          updates.push({ id, label: want })
        }
      }
      for (const id of [...edgeLabelState.keys()]) {
        if (!seen.has(id)) edgeLabelState.delete(id)
      }
      if (updates.length > 0) edgesDS.update(updates)
    }
    syncEdgeLabelsRef.current = syncEdgeSelectionLabels
    network.on('select', syncEdgeSelectionLabels)

    // Right-click context menu on the card topology canvas. This fires for both
    // node and blank-area clicks so the user can create new items at the pointer
    // position. We still suppress the menu after a real connect-drag gesture.
    network.on('oncontext', (params) => {
      if (connectDragOccurredRef.current) {
        connectDragOccurredRef.current = false
        return
      }
      const evt = params.event as MouseEvent
      evt.preventDefault()
      const canvasPos = (params.pointer as unknown as { canvas?: { x: number; y: number } })?.canvas ?? { x: 0, y: 0 }
      const nodeId = network.getNodeAt(params.pointer.DOM)
      // No node under the cursor: check for an edge so the shell can show the
      // edge-level context menu (open connected cards / delete connection).
      let edge: TopologyEdgeRef | undefined
      if (nodeId == null) {
        const edgeId = network.getEdgeAt(params.pointer.DOM)
        if (edgeId != null) {
          const hit = edgesRef.current.find(e => String(e.id) === String(edgeId))
          if (hit) edge = { from: hit.from, to: hit.to, kind: hit.kind }
        }
      }
      onContextMenuRef.current?.({
        clientX: evt.clientX,
        clientY: evt.clientY,
        canvasX: canvasPos.x,
        canvasY: canvasPos.y,
        nodeId: nodeId != null ? String(nodeId) : undefined,
        edge,
      })
    })

    // Keep energy-flow animation running at display refresh while any doing
    // depends_on edge is visible. The loop self-schedules only when the last
    // beforeDrawing pass actually drew active edges, so it idles when idle.
    let energyFlowRafId: number | null = null
    const scheduleEnergyFlowRedraw = () => {
      if (energyFlowRafId != null) return
      energyFlowRafId = requestAnimationFrame(() => {
        energyFlowRafId = null
        if (networkRef.current === network) {
          network.redraw()
        }
      })
    }

    network.on('beforeDrawing', (ctx) => {
      const hasActive = drawDependsOnEdges(
        ctx,
        network,
        cardsRef.current,
        dependsOnEdgeVisualsRef.current,
        performance.now(),
      )
      if (hasActive) {
        scheduleEnergyFlowRedraw()
      }
    })

    network.on('afterDrawing', (ctx) => {
      const drag = connectDragRef.current
      if (drag) {
        const source = network.getPosition(drag.sourceId)
        const pointer = network.DOMtoCanvas(drag.pointer)
        ctx.save()
        ctx.beginPath()
        ctx.moveTo(source.x, source.y)
        ctx.lineTo(pointer.x, pointer.y)
        ctx.strokeStyle = getCssVar('--accent-primary', '#3b82f6')
        ctx.lineWidth = 2
        ctx.setLineDash([6, 4])
        ctx.stroke()
        ctx.restore()
      }
      syncAgentOverlaysRef.current()
      syncSelectionOverlayRef.current()
    })

    const container = containerRef.current
    const domPoint = (event: { clientX: number; clientY: number }) => {
      const rect = container.getBoundingClientRect()
      return { x: event.clientX - rect.left, y: event.clientY - rect.top }
    }
    const handlePointerDown = (event: PointerEvent) => {
      if (event.button !== 2) return
      const pointer = domPoint(event)
      const sourceId = network.getNodeAt(pointer)
      if (sourceId == null) return
      event.preventDefault()
      suppressClickRef.current = true
      connectDragRef.current = { sourceId: String(sourceId), pointer, initialPointer: pointer }
      network.redraw()
    }
    const handlePointerMove = (event: PointerEvent) => {
      if (!connectDragRef.current) return
      event.preventDefault()
      connectDragRef.current.pointer = domPoint(event)
      network.redraw()
    }
    const finishConnectDrag = (event: PointerEvent) => {
      const drag = connectDragRef.current
      if (!drag) return
      event.preventDefault()
      const releasePointer = domPoint(event)
      connectDragRef.current = null
      network.redraw()

      const dx = releasePointer.x - drag.initialPointer.x
      const dy = releasePointer.y - drag.initialPointer.y
      const movedDistance = Math.sqrt(dx * dx + dy * dy)
      if (movedDistance >= 5) {
        // Real drag — attempt connection
        const targetId = network.getNodeAt(releasePointer)
        if (targetId != null && String(targetId) !== drag.sourceId) {
          void onConnectNodesRef.current?.(drag.sourceId, String(targetId))
        }
        connectDragOccurredRef.current = true
      }
      // If moved < 5px, it was a click, not a drag — leave connectDragOccurredRef as false
      // so the contextmenu handler shows the agent menu.
      window.setTimeout(() => { suppressClickRef.current = false }, 0)
    }
    const cancelConnectDrag = () => {
      if (!connectDragRef.current) return
      connectDragRef.current = null
      suppressClickRef.current = false
      network.redraw()
    }
    const preventContextMenu = (event: MouseEvent) => {
      event.preventDefault()
    }

    container.addEventListener('pointerdown', handlePointerDown)
    container.addEventListener('pointermove', handlePointerMove)
    container.addEventListener('pointerup', finishConnectDrag)
    container.addEventListener('pointercancel', cancelConnectDrag)
    container.addEventListener('contextmenu', preventContextMenu)

    // Without setPointerCapture, pointerup may fire outside the container
    // (e.g. user releases the right button over the sidebar). This window-level
    // listener ensures the drag is always completed or cleaned up.
    const onWindowPointerUp = (event: PointerEvent) => {
      if (connectDragRef.current) finishConnectDrag(event)
    }
    window.addEventListener('pointerup', onWindowPointerUp)

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setSelectedCardId(null)
      } else if (e.key === 'Enter' && selectedCardIdRef.current) {
        openSelectedNode(selectedCardIdRef.current)
      }
    }
    window.addEventListener('keydown', handleKeyDown)

    return () => {
      if (energyFlowRafId != null) {
        cancelAnimationFrame(energyFlowRafId)
      }
      resizeObserver.disconnect()
      window.removeEventListener('keydown', handleKeyDown)
      window.removeEventListener('pointerup', onWindowPointerUp)
      container.removeEventListener('pointerdown', handlePointerDown)
      container.removeEventListener('pointermove', handlePointerMove)
      container.removeEventListener('pointerup', finishConnectDrag)
      container.removeEventListener('pointercancel', cancelConnectDrag)
      container.removeEventListener('contextmenu', preventContextMenu)
      agentAvatarRoots.forEach(root => root.unmount())
      agentAvatarRoots.clear()
      agentOverlayRef.current?.remove()
      agentOverlayRef.current = null
      selectionOverlayRef.current?.remove()
      selectionOverlayRef.current = null
      linkedOverlayRef.current?.remove()
      linkedOverlayRef.current = null
      network.destroy()
      networkRef.current = null
    }
  }, [])

  // Theme colors are global vis-network options, so update them only when the
  // data-theme observer changes `themeKey`, rather than on every data sync.
  useEffect(() => {
    const network = networkRef.current
    if (!network) return

    const lc = getCssVar('--text-primary', '#e2e8f0')
    network.setOptions({
      nodes: { font: { size: 12, color: lc, face: 'system-ui, -apple-system, sans-serif' } },
      edges: {
        color: { color: getCssVar('--border-default', '#475569'), highlight: getCssVar('--accent-primary', '#3b82f6') },
        font: { size: 10, color: lc, face: 'system-ui, -apple-system, sans-serif', strokeWidth: 2, strokeColor: getCssVar('--bg-surface', '#ffffff') },
        labelHighlightBold: false,
      },
    })
  }, [themeKey])

  // Sync data (node/edge add/remove/update) without replacing the positions
  // owned by vis-network. Status and avatar updates retain both drag positions
  // and fixed state; only node additions/removals resume stabilization.
  useEffect(() => {
    const network = networkRef.current
    if (!network) return

    const { nodes: nodesDS, edges: edgesDS } = getInternalDataSets(network)

    // --- Nodes: remove deleted, add/update the rest ---
    if (nodesDS) {
      const newNodeIds = new Set(nodes.map(n => String(n.id)))
      const oldNodeIds = nodesDS.getIds().map(String)
      const oldNodeIdSet = new Set(oldNodeIds)
      const toRemove = oldNodeIds.filter(id => !newNodeIds.has(id))
      const hasStructuralChange = toRemove.length > 0 || nodes.some(node => !oldNodeIdSet.has(String(node.id)))
      const internalNodes = getInternalNodes(network)
      const nodesWithPositions = nodes.map(node => {
        const current = internalNodes[String(node.id)]
        if (!current) return node
        return { ...node, x: current.x, y: current.y, fixed: current.options.fixed }
      })

      if (toRemove.length > 0) nodesDS.remove(toRemove)
      // update() handles both add (missing id) and update (existing id).
      nodesDS.update(nodesWithPositions as unknown as Record<string, unknown>[])

      if (hasStructuralChange) {
        network.stabilize()
      }
    }

    // --- Edges: remove deleted, add/update the rest ---
    if (edgesDS) {
      const newEdgeIds = new Set(edges.map(e => e.id))
      const oldEdgeIds = edgesDS.getIds() as string[]
      const toRemove = oldEdgeIds.filter(id => !newEdgeIds.has(id))
      if (toRemove.length > 0) edgesDS.remove(toRemove)
      edgesDS.update(edges as unknown as Record<string, unknown>[])
      syncEdgeLabelsRef.current()
    }

    // --- Place a newly created node at the position where the user opened the
    // context menu. This runs when the node first appears in the data set. ---
    const placement = pendingPlacementRef.current
    if (placement && nodesDS) {
      const ids = nodesDS.getIds() as string[]
      if (ids.includes(placement.id)) {
        network.moveNode(placement.id, placement.x, placement.y)
        onPlacementDoneRef.current?.()
      }
    }
    // --- Sync working-agent ring overlays after nodes/edges change. ---
    syncAgentOverlaysRef.current()
  }, [nodes, edges])

  // Keep agent avatar overlays positioned over the vis-network canvas for agent
  // nodes. This lets us reuse CSS animations (working ring) while vis-network
  // handles layout, edges and dragging. The SVG image underneath is always
  // visible as a fallback while the overlay is mounting or when agent info is
  // not yet loaded.
  const agentOverlayRef = useRef<HTMLDivElement | null>(null)
  const agentOverlayLayerTransformRef = useRef('')
  const syncAgentOverlaysRef = useRef(() => {})

  const syncAgentOverlays = useCallback(() => {
    const network = networkRef.current
    const overlay = agentOverlayRef.current
    const container = containerRef.current
    if (!network || !overlay || !container) return

    // Single-layer camera transform: the overlay container handles pan/zoom
    // so ring elements are positioned in world coordinates only.
    const scale = network.getScale()
    const origin = network.canvasToDOM({ x: 0, y: 0 })
    const layerTransform = buildLayerTransform(origin, scale)
    const transformRef = agentOverlayLayerTransformRef
    if (layerTransform !== transformRef.current) {
      overlay.style.transform = layerTransform
      transformRef.current = layerTransform
    }

    const existing = new Map<string, HTMLDivElement>()
    overlay.childNodes.forEach(child => {
      const el = child as HTMLDivElement
      const id = el.dataset.agentCardId
      if (id) existing.set(id, el)
    })

    const snapshot = agentInfoSnapshotRef.current ?? getAgentInfoSnapshot()
    const presentIds = new Set<string>()
    const cards = cardsRef.current
    const workingCardIds = new Set<string>()
    const cardIds = new Set(cards.map(card => card.id))
    for (const card of cards) {
      if (card.standalone) continue
      const isAgent = card.type === 'agent' || card.data?.componentKind === 'agent'
      if (!isAgent) continue
      const storeAgent = findStoreAgent(card, snapshot)
      const agent = displayAgentForCard(card, storeAgent)
      if (!agent.IsWorking) continue
      workingCardIds.add(card.id)
    }
    const agentBoundPairs = buildAgentBoundPairs(cards, snapshot)
    // Virtual agent nodes (owner agents without entity mono cards) are not in
    // the cards list, so the loop above misses them. Scan the snapshot directly
    // for working agents whose card id has a binding pair and no mono card,
    // then add their card id to the working set so the spinning ring renders.
    for (const pair of agentBoundPairs) {
      if (workingCardIds.has(pair.agentCardId) || cardIds.has(pair.agentCardId)) continue
      const agent =
        snapshot.items.find(a => agentCardId(a.Id) === pair.agentCardId) ??
        snapshot.byId.get(pair.agentId ?? '')
      if (agent?.IsWorking) workingCardIds.add(pair.agentCardId)
    }
    // Bound task cards mirror the working agent's spinning ring so the pair
    // reads as one executing unit.
    const boundRingCardIds = new Set<string>()
    for (const pair of agentBoundPairs) {
      if (workingCardIds.has(pair.agentCardId) && !workingCardIds.has(pair.cardId)) {
        boundRingCardIds.add(pair.cardId)
      }
    }

    const upsertRing = (cardId: string, isBoundCard: boolean) => {
      const position = network.getPosition(cardId)

      presentIds.add(cardId)

      let el = existing.get(cardId)
      if (!el) {
        el = document.createElement('div')
        el.dataset.agentCardId = cardId
        el.className = 'topology-agent-avatar-overlay'
        el.setAttribute('aria-hidden', 'true')
        overlay.appendChild(el)
      }
      const positionSignature = `${position.x},${position.y}`
      if (el.dataset.positionSignature !== positionSignature) {
        el.style.left = `${position.x}px`
        el.style.top = `${position.y}px`
        el.dataset.positionSignature = positionSignature
      }

      // When this working agent is also the two-click-selected (active) one,
      // recolor the working ring to the complement of the avatar background
      // color so it stays readable in both light and dark themes.
      const activeCardId = activeAgentIdRef.current ? agentCardId(activeAgentIdRef.current) : undefined
      const isActiveAgent = !isBoundCard && activeCardId === cardId
      const stateSignature = `${isBoundCard}:${isActiveAgent}:${document.documentElement.getAttribute('data-theme') ?? ''}`
      if (el.dataset.stateSignature !== stateSignature) {
        el.classList.toggle('is-bound-card', isBoundCard)
        el.classList.toggle('is-active', isActiveAgent)
        if (isActiveAgent) {
          el.style.setProperty('--agent-ring-active-color', complementaryColor(getCssVar('--accent-primary', '#3b82f6')))
        } else {
          el.style.removeProperty('--agent-ring-active-color')
        }
        el.dataset.stateSignature = stateSignature
      }

      if (!agentAvatarRoots.has(el)) {
        const root = createRoot(el)
        agentAvatarRoots.set(el, root)
        root.render(<div className="topology-agent-ring" aria-hidden="true" />)
      }
    }

    for (const cardId of workingCardIds) upsertRing(cardId, false)
    for (const cardId of boundRingCardIds) upsertRing(cardId, true)

    for (const [id, el] of existing) {
      if (!presentIds.has(id)) {
        const root = agentAvatarRoots.get(el)
        if (root) {
          root.unmount()
          agentAvatarRoots.delete(el)
        }
        el.remove()
      }
    }
  }, [])

  useEffect(() => {
    syncAgentOverlaysRef.current = syncAgentOverlays
  }, [syncAgentOverlays])

  const syncSelectionOverlay = useCallback(() => {
    const network = networkRef.current
    const overlay = selectionOverlayRef.current
    if (!network || !overlay) return
    const linked = linkedOverlayRef.current
    const signatures = selectionOverlaySignaturesRef.current

    // Single-layer camera transform: the overlay container handles pan/zoom
    // so reticles are positioned in world coordinates only.
    const scale = network.getScale()
    const origin = network.canvasToDOM({ x: 0, y: 0 })
    const layerTransform = buildLayerTransform(origin, scale)
    const transformRef = selectionOverlayLayerTransformRef
    if (layerTransform !== transformRef.current) {
      overlay.style.transform = layerTransform
      if (linked) linked.style.transform = layerTransform
      transformRef.current = layerTransform
    }

    const hide = (el: HTMLDivElement) => {
      if (signatures.get(el) === 'hidden') return
      el.style.display = 'none'
      signatures.set(el, 'hidden')
    }
    const positionOn = (el: HTMLDivElement, nodeId: string) => {
      const pos = network.getPosition(nodeId)
      const bbox = (network as unknown as { getBoundingBox: (id: string) => { left: number; top: number; right: number; bottom: number } }).getBoundingBox(nodeId)
      const { width: w, height: h } = worldReticleSize(bbox, 12, scale)
      const signature = `${nodeId}:${pos.x},${pos.y},${w},${h}`
      if (signatures.get(el) === signature) return
      el.style.display = 'block'
      el.style.left = `${pos.x}px`
      el.style.top = `${pos.y}px`
      el.style.width = `${w}px`
      el.style.height = `${h}px`
      el.style.transform = 'translate(-50%, -50%)'
      signatures.set(el, signature)
    }
    const id = selectedCardIdRef.current
    if (!id || !cardsRef.current.some(c => c.id === id)) {
      hide(overlay)
      if (linked) hide(linked)
      return
    }
    positionOn(overlay, id)
    // Selecting either side of an agent↔task-card binding highlights the
    // bound partner node as well.
    if (linked) {
      const partnerId = linkedPartnerForSelection(id, agentBoundPairsRef.current)
      if (partnerId && cardsRef.current.some(c => c.id === partnerId)) {
        positionOn(linked, partnerId)
      } else {
        hide(linked)
      }
    }
  }, [])
  const syncSelectionOverlayRef = useRef(syncSelectionOverlay)
  useEffect(() => {
    syncSelectionOverlayRef.current = syncSelectionOverlay
  }, [syncSelectionOverlay])

  return <div className="topology-graph" ref={containerRef} />
})
