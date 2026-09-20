import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { MouseEvent, ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { ExternalLink, ChevronDown, ChevronLeft, ChevronRight, Layers, Play, Pause, Check, X, Clock } from 'lucide-react'
import { Network } from 'vis-network/standalone'
import type { MonoCardListItem } from '../../../domain/mono-types'
import type { AgentInfo, AgentInfoSnapshot } from '../hooks/agentInfoStore'
import { MonoCardCompact } from './MonoCardCompact'
import { CardAgentAvatar } from './CardAgentAvatar'
import { requestOpenAgentChat } from '../pendingAgentChat'
import { STATUS_META } from './mono-card-meta'
import { buildEpicLayout, routeWorkflowEdge, workflowLastActivity, workflowOwnerAgentId, workflowTaskIds, classifyWorkflowCategory, locateMapIdForCard, EPIC_CATEGORY_PREFIX, EPIC_CATEGORY_ORDER } from './workflowLayout'
import type { EpicCategory } from './workflowLayout'
import type { WorkflowGroupOffset, WorkflowLayout, WorkflowDirection, WorkflowNodeLayout } from './workflowLayout'
import { applyWorkflowFilters, useWorkflowFilters } from './workflowFiltersStore'
import type { WorkflowFilterableNode } from './workflowFiltersStore'
import { useI18n, I18nContext } from '../../../i18n'
import { formatRelativeTime } from '../../lib/format-time'
import { loadPreference, savePreference } from '../../../application/theme-persist'
import { client } from '../../../application/generated-client'
import * as projectClient from '../../../gen-clients/project/client'
import * as agentTurn from '../../../gen-clients/local/client'
import { GuideIds } from '../guide-ids'
import './WorkflowGraph.css'

/** Backend preference key prefixes; each layout direction owns its own saved view. */
const VIEWPORT_PREF_KEY_PREFIX = 'workflowGraph.viewport.v1'
const GROUP_OFFSETS_PREF_KEY_PREFIX = 'workflowGraph.groupOffsets.v1'

/** Maximum wall-clock time (ms) to wait for a locate target to render before
 *  giving up. Cards load asynchronously from the backend; the old fixed 30-frame
 *  RAF cap (~500ms) silently abandoned locates on cold/slow loads. */
export const LOCATE_TIMEOUT_MS = 5000

/** Working-edge dash animation redraw interval (~15fps). The dash phase
 *  advances visibly at 60fps, but redrawing every edge + traversing every node
 *  at that rate is wasted work while the animation loop runs. */
export const DRAW_INTERVAL_MS = 66

/** Pure throttle predicate for the animation redraw loop: a frame is due when
 *  there is no previous frame timestamp (first frame after mount or after a
 *  visibility resume) or when at least `intervalMs` has elapsed since the last
 *  drawn frame. rAF keeps firing at ~60fps; this decides which ticks actually
 *  perform the full redraw. */
export function shouldDrawFrame(lastTs: number | null, now: number, intervalMs: number): boolean {
  if (lastTs == null) return true
  return now - lastTs >= intervalMs
}

/** A request to zoom the viewport so a workflow (or the whole graph) fits the
 *  screen. `nonce` makes repeated requests for the same target observable;
 *  `mapId` targets a specific workflow map — when absent the currently
 *  selected card's workflow is used, falling back to the entire graph.
 *  `selectStart` also selects the map's start node (reused by the badge/row
 *  locate entry points). */
export interface WorkflowLocateRequest {
  nonce: number
  mapId?: string
  selectStart?: boolean
}

function directionPreferenceKey(prefix: string, direction: WorkflowDirection): string {
  return `${prefix}.${direction.toLowerCase()}`
}

interface WorkflowGraphProps {
  cards: MonoCardListItem[]
  agentInfoSnapshot?: AgentInfoSnapshot
  onNodeClick?: (cardId: string, label: string) => void
  /** Card-menu request (right-click); coordinates are viewport client px. */
  onNodeMenu?: (cardId: string, x: number, y: number) => void
  /** Blank-area menu request (right-click on empty canvas); coordinates are viewport client px. */
  onBlankMenu?: (x: number, y: number) => void
  /** Layout flow direction; controlled by the parent toolbar. Defaults to left-to-right. */
  direction?: WorkflowDirection
  /** Workflow map card ids expanded to show full internals. */
  expandedWorkflows?: ReadonlySet<string>
  /** Toggle the expand state of a workflow (epic mode). */
  onToggleFold?: (mapId: string) => void
  /** Archived time-bucket ids that are expanded (buckets fold by default). */
  expandedBuckets?: ReadonlySet<string>
  /** Toggle the fold state of an archived time bucket (epic mode). */
  onToggleBucketFold?: (bucketId: string) => void
  /** Categories folded into their bare category node (categories expand by default). */
  foldedCategories?: ReadonlySet<string>
  /** Toggle the fold state of a category band (epic mode). */
  onToggleCategoryFold?: (category: EpicCategory) => void
  /** Category-node menu request (right-click on a virtual category band node);
   *  coordinates are viewport client px. */
  onCategoryMenu?: (category: EpicCategory, x: number, y: number) => void
  /** Idempotently unfold a workflow's full ancestor chain (category band →
   *  archived bucket → the workflow itself) so its nodes render. Called when a
   *  locate request resolves to a concrete map — the condition-driven locate
   *  then fits once the nodes appear. */
  onLocateWorkflow?: (mapId: string) => void
  /** Start a to-start workflow: invoked when the status button is clicked on a
   *  workflow with no active owner agent. The parent either starts the bound
   *  owner directly or opens the agent pick/create dialog. */
  onStartWorkflow?: (mapId: string) => void
  /** Open the schedule (cron) modal for a scheduler card: invoked when the
   *  clock button on a scheduler node is clicked. */
  onEditSchedule?: (cardId: string) => void
  /** Locate/zoom request from the parent toolbar: fit a workflow (mapId or the
   * selected card's workflow) or the whole graph when nothing is selected. */
  locateRequest?: WorkflowLocateRequest | null
  /** Workflow nodes currently covered by the open delete confirmation. */
  pendingDeleteNodeIds?: ReadonlySet<string>
}

/** Task card ids currently claimed by a working agent (Goal.BoundTaskCardID). */
function buildWorkingTaskAgents(snapshot: AgentInfoSnapshot | undefined): Map<string, string> {
  const map = new Map<string, string>()
  if (!snapshot) return map
  for (const agent of snapshot.items) {
    const cardId = agent.BoundTaskCardId || agent.Runtime?.BoundTaskCardId
    if (!cardId || !agent.IsWorking) continue
    map.set(cardId, agent.ActorId || agent.Id)
  }
  return map
}

/** Dot color per agent kind; unknown kinds fall back to neutral gray. */
const AGENT_KIND_DOT_COLORS: Record<string, string> = {
  coordinator: '#f59e0b',
  coder: '#3b82f6',
  worker: '#22c55e',
  reviewer: '#a855f7',
  researcher: '#06b6d4',
}

function agentKindDotColor(kind: string | undefined): string {
  return (kind && AGENT_KIND_DOT_COLORS[kind]) || '#94a3b8'
}

function workflowStatus(status: string | undefined): 'start' | 'doing' | 'paused' | 'done' | 'failed' {
  if (status === 'doing') return 'doing'
  if (status === 'paused') return 'paused'
  if (status === 'done') return 'done'
  if (status === 'failed' || status === 'blocked' || status === 'cancelled') return 'failed'
  return 'start'
}

function nextWorkflowStatus(status: ReturnType<typeof workflowStatus>): string {
  if (status === 'start' || status === 'paused') return 'doing'
  if (status === 'doing') return 'paused'
  return 'done'
}

function WorkflowStatusIcon({ status }: { status: ReturnType<typeof workflowStatus> }) {
  // doing → pause (click to pause the owner); paused → play (click to resume).
  // Media-player affordance: the color class (info vs warning) distinguishes
  // the two states even though both could read as "play/pause" toggles.
  if (status === 'doing') return <Pause size={15} />
  if (status === 'paused') return <Play size={15} />
  if (status === 'done') return <Check size={15} />
  if (status === 'failed') return <X size={15} />
  return <Play size={15} />
}

function getCssVar(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
}

/** Minimal interface for vis-network's internal DataSet (accessed via network.body.data). */
interface VisDataSet {
  getIds(): (string | number)[]
  update(data: Record<string, unknown> | Record<string, unknown>[]): (string | number)[]
  remove(id: string | number | (string | number)[]): (string | number)[]
}

interface StartDragState {
  startId: string
  mapId: string
  startPosition: { x: number; y: number }
  dx: number
  dy: number
  nodeIds: Set<string>
  positions: Map<string, { x: number; y: number }>
}

function getInternalDataSets(network: Network): { nodes?: VisDataSet; edges?: VisDataSet } {
  return (network as unknown as { body: { data: { nodes?: VisDataSet; edges?: VisDataSet } } }).body.data
}

/** Content signature of the vis DataSet payloads (nodes + edges). Two syncs
 *  producing byte-identical nodes/edges share a signature — the network's
 *  anchor boxes and edge set are already up to date — so syncDataAndOverlays
 *  can skip the full DataSet update + redraw. Cards-array reference churn with
 *  unchanged layout contents (a wfDedup bookkeeping write, an edit to an
 *  unrelated card, a status-event snapshot refresh) is the common case this
 *  guards: the memoized node/edge objects are rebuilt but carry the same data. */
function dataSetSignature(nodes: readonly unknown[], edges: readonly unknown[]): string {
  return JSON.stringify([nodes, edges])
}

/** Direct access to live node positions. moveNode() must not be used during a
 *  group drag: each call schedules a zero-delay startSimulation emit that runs
 *  a full synchronous _redraw, so N nodes × every mousemove floods the render
 *  loop and the drag stutters. */
function getBodyNodes(network: Network): Record<string, { x: number; y: number }> {
  return (network as unknown as { body: { nodes: Record<string, { x: number; y: number }> } }).body.nodes
}

/** Whether any rendered node currently falls inside the visible viewport.
 *  The workflow pane stays mounted but is hidden via `display:none` while
 *  another mode is active, which collapses its container to 0×0. When the
 *  pane is shown again (re-activation), this check decides whether to
 *  auto-fit: only refit when the viewport the user left behind points at
 *  empty space, so a tab switch that lands on a blank canvas re-centers the
 *  graph instead of showing nothing — and never refits while it stays open. */
export function anyNodeInViewport(network: Network, container: { clientWidth: number; clientHeight: number }): boolean {
  const scale = network.getScale()
  if (!(scale > 0)) return false
  const width = container.clientWidth
  const height = container.clientHeight
  if (width === 0 || height === 0) return false
  const center = network.getViewPosition()
  const halfW = width / (2 * scale)
  const halfH = height / (2 * scale)
  const minX = center.x - halfW
  const maxX = center.x + halfW
  const minY = center.y - halfH
  const maxY = center.y + halfH
  const nodes = getBodyNodes(network)
  for (const id in nodes) {
    const p = nodes[id]
    if (p && p.x >= minX && p.x <= maxX && p.y >= minY && p.y <= maxY) return true
  }
  return false
}

/** Viewport client coordinates of a vis-network pointer event. */
function pointerClientPosition(params: { pointer?: { DOM?: { x: number; y: number } }; event?: { srcEvent?: unknown } }): { x: number; y: number } {
  const direct = params.event as { clientX?: unknown; clientY?: unknown } | undefined
  if (typeof direct?.clientX === 'number' && typeof direct?.clientY === 'number') {
    return { x: direct.clientX, y: direct.clientY }
  }
  const srcEvent = params.event?.srcEvent as { clientX?: unknown; clientY?: unknown } | undefined
  if (typeof srcEvent?.clientX === 'number' && typeof srcEvent?.clientY === 'number') {
    return { x: srcEvent.clientX, y: srcEvent.clientY }
  }
  return { x: params.pointer?.DOM?.x ?? 0, y: params.pointer?.DOM?.y ?? 0 }
}

/** An agent counts as active for workflow classification when it is mounted
 *  and not paused — only then can it actually drive turns. A workflow whose
 *  owner/worker is absent, unloaded, or paused shows as 待开始, not 进行中.
 *  Exception: a waiting owner (its turn parked between workflow events) is
 *  alive by definition and the agent list may briefly report it unloaded
 *  during a refresh — treating that flicker as inactive made in-progress
 *  workflows blink away. Waiting always counts as active. */
function isAgentActive(agent: AgentInfo | undefined): boolean {
  if (agent?.Status === 'waiting') return true
  return !!agent && agent.LoadState === 'loaded' && agent.Status !== 'paused'
}

function isActiveTarget(cardId: string, workingTasks: Map<string, string>): boolean {
  // Only tasks with a live working agent animate; a card merely marked doing
  // (agent unloaded/paused/gone) is 待开始, not working.
  return workingTasks.has(cardId)
}

/**
 * Draw every structural workflow edge on the canvas before vis draws nodes, so
 * edges stay behind the DOM card overlays. Paths are routed orthogonally
 * between cards (with relaxed clearance) and rendered with generous rounded
 * corners. Edge style carries status: working edges are animated green energy
 * dashes without arrowheads, done edges solid green, edges in an active
 * workflow whose work has not started orange dashes, and idle edges neutral
 * dashes — all static edges land an arrowhead on the target card border.
 */
interface WorkflowEdgeDrawCache {
  layout?: WorkflowLayout
  byId?: Map<string, MonoCardListItem>
  workingTasks?: Map<string, string>
  positionKey: string
  nodesForRoute: WorkflowNodeLayout[]
  nodesById: Map<string, WorkflowNodeLayout>
  workflowByNode: Map<string, string>
  routeNodesByWorkflow: Map<string, WorkflowNodeLayout[]>
  routes: Map<number, ReturnType<typeof routeWorkflowEdge>>
}

function drawWorkflowEdges(
  ctx: CanvasRenderingContext2D,
  layout: WorkflowLayout,
  /** Nodes currently visible after filter application — routing and edge
   *  emission only consider these, so edges touching a filtered-out node are
   *  not drawn. */
  visibleNodes: WorkflowNodeLayout[],
  byId: Map<string, MonoCardListItem>,
  workingTasks: Map<string, string>,
  time: number,
  nodePositions: ReadonlyMap<string, { x: number; y: number }>,
  colors: { strong: string; subtle: string },
  cache: WorkflowEdgeDrawCache,
): boolean {
  // Positions are read every frame, but expensive obstacle routing is only done
  // when the layout or a node's position changes (animation only changes dash phase).
  // The position key is derived from the visible node set, so switching filters
  // invalidates the cache exactly when a routed node appears or disappears.
  let positionKey = ''
  const nodesForRoute: WorkflowNodeLayout[] = []
  const nodesById = new Map<string, WorkflowNodeLayout>()
  for (const node of visibleNodes) {
    const position = nodePositions.get(node.cardId)
    const x = position ? position.x - node.width / 2 : node.x
    const y = position ? position.y - node.height / 2 : node.y
    positionKey += `${node.cardId}:${x},${y};`
    const positioned = { ...node, x, y }
    nodesForRoute.push(positioned)
    nodesById.set(node.cardId, positioned)
  }
  if (cache.layout !== layout || cache.byId !== byId || cache.workingTasks !== workingTasks || cache.positionKey !== positionKey) {
    const cards = [...byId.values()]
    const workflowByNode = new Map<string, string>()
    for (const mapCard of cards) {
      if (mapCard.type !== 'workflow' || mapCard.standalone) continue
      workflowByNode.set(mapCard.id, mapCard.id)
      workflowByNode.set(`${mapCard.id}:start`, mapCard.id)
      for (const taskId of workflowTaskIds(mapCard, cards)) workflowByNode.set(taskId, mapCard.id)
    }
    cache.layout = layout
    cache.byId = byId
    cache.workingTasks = workingTasks
    cache.positionKey = positionKey
    cache.nodesForRoute = nodesForRoute
    cache.nodesById = nodesById
    cache.workflowByNode = workflowByNode
    cache.routeNodesByWorkflow = new Map()
    for (const node of nodesForRoute) {
      const workflow = workflowByNode.get(node.cardId)
      if (workflow) cache.routeNodesByWorkflow.set(workflow, [...(cache.routeNodesByWorkflow.get(workflow) ?? []), node])
    }
    cache.routes = new Map()
  }
  const byIdOf = (id: string) => cache.nodesById.get(id)
  const workflowByNode = cache.workflowByNode
  const edgeColor = colors.strong
  let hasActive = false
  const drawnPaths = new Set<string>()

  for (const [edgeIndex, edge] of layout.edges.entries()) {
    const source = byIdOf(edge.from)
    const target = byIdOf(edge.to)
    if (!source || !target) continue

    // Tree edges (epic root → category → workflow start) are simple neutral
    // connectors — never subject to workflow-activity styling. Route along the
    // layout's flow direction so LTR epic mode connects left→right.
    if (edge.kind === 'tree') {
      const route = cache.routes.get(edgeIndex) ?? routeWorkflowEdge(source, target, [source, target], layout.direction ?? 'TTB')
      cache.routes.set(edgeIndex, route)
      if (route.points.length < 2) continue
      const pts = route.points
      const dedupKey = `tree:${pts.map(p => `${Math.round(p.x)},${Math.round(p.y)}`).join(';')}`
      if (drawnPaths.has(dedupKey)) continue
      drawnPaths.add(dedupKey)
      ctx.save()
      ctx.strokeStyle = colors.subtle
      ctx.lineWidth = 1.4
      ctx.lineCap = 'round'
      ctx.lineJoin = 'round'
      ctx.setLineDash([])
      ctx.beginPath()
      ctx.moveTo(pts[0]!.x, pts[0]!.y)
      for (let i = 1; i < pts.length; i++) ctx.lineTo(pts[i]!.x, pts[i]!.y)
      ctx.stroke()
      ctx.restore()
      continue
    }

    const sourceWorkflow = workflowByNode.get(edge.from)
    const targetWorkflow = workflowByNode.get(edge.to)
    // Routing must only consider obstacles in the edge's own workflow. A rare
    // cross-workflow edge gets a direct route between its endpoints instead.
    const routeNodes = sourceWorkflow && sourceWorkflow === targetWorkflow
      ? (cache.routeNodesByWorkflow.get(sourceWorkflow) ?? [source, target])
      : [source, target]
    const route = cache.routes.get(edgeIndex) ?? routeWorkflowEdge(source, target, routeNodes, layout.direction)
    cache.routes.set(edgeIndex, route)
    if (route.points.length < 2) continue

    // Edge semantics (styled by the flow source / depends_on & start target):
    // working → animated green energy dashes (no arrow); done in an active
    // workflow → solid green; active workflow but not started → orange dashes;
    // idle workflow → neutral (done edges solid, the rest dashed).
    const styleTargetId = edge.kind === 'flow' ? edge.from : edge.to
    const animate = isActiveTarget(styleTargetId, workingTasks)
    if (animate) hasActive = true

    const targetStatus = byId.get(styleTargetId)?.status
    const styleWorkflow = workflowByNode.get(styleTargetId)
    const workflowActive = styleWorkflow ? byId.get(styleWorkflow)?.status === 'doing' : false
    // A card marked doing without a live working agent is 待开始, not working:
    // blue dashes instead of the misleading green animation.
    const staleDoing = !animate && targetStatus === 'doing'
    const color = animate || (targetStatus === 'done' && workflowActive)
      ? '#22c55e'
      : staleDoing
        ? '#3b82f6'
        : workflowActive && targetStatus !== 'done'
          ? '#f97316'
          : edgeColor
    const dash: number[] | null = animate ? [5, 7] : targetStatus === 'done' ? null : [6, 4]
    const pts = route.points
    const dedupKey = `${color}:${dash?.join(',') ?? ''}:${pts.map(point => `${Math.round(point.x)},${Math.round(point.y)}`).join(';')}`
    if (drawnPaths.has(dedupKey)) continue
    drawnPaths.add(dedupKey)

    ctx.save()
    ctx.strokeStyle = color
    ctx.lineWidth = animate ? 1.8 : 1.6
    ctx.lineCap = 'round'
    ctx.lineJoin = 'round'
    ctx.setLineDash(dash ?? [])
    if (animate) {
      // Match the landing orchestration flow: 1s over a 12px dash period.
      ctx.lineDashOffset = -((time % 1000) / 1000) * 12
    }
    ctx.beginPath()
    ctx.moveTo(pts[0]!.x, pts[0]!.y)
    for (let i = 1; i < pts.length; i++) {
      const point = pts[i]!
      const next = pts[i + 1]
      if (!next || i === pts.length - 1) {
        ctx.lineTo(point.x, point.y)
        continue
      }
      const previous = pts[i - 1]!
      // Generous corner radius keeps the orthogonal route smooth.
      const radius = Math.min(18, Math.hypot(point.x - previous.x, point.y - previous.y) / 2, Math.hypot(next.x - point.x, next.y - point.y) / 2)
      ctx.arcTo(point.x, point.y, next.x, next.y, radius)
    }
    ctx.stroke()

    // Arrowhead on static edges only; energy (animated) lines stay clean.
    if (!animate) {
      const last = pts[pts.length - 1]!
      const prev = pts[pts.length - 2] ?? pts[0]!
      const angle = Math.atan2(last.y - prev.y, last.x - prev.x)
      const headSize = 7
      ctx.translate(last.x, last.y)
      ctx.rotate(angle)
      ctx.beginPath()
      ctx.moveTo(0, 0)
      ctx.lineTo(-headSize, -headSize / 2)
      ctx.lineTo(-headSize, headSize / 2)
      ctx.closePath()
      ctx.fillStyle = color
      ctx.fill()
    }
    ctx.restore()
  }

  return hasActive
}

export const WorkflowGraph = memo(function WorkflowGraph({
  cards,
  agentInfoSnapshot,
  onNodeClick,
  onNodeMenu,
  onBlankMenu,
  direction = 'LTR',
  expandedWorkflows,
  onToggleFold,
  expandedBuckets,
  onToggleBucketFold,
  foldedCategories,
  onToggleCategoryFold,
  onCategoryMenu,
  onLocateWorkflow,
  onStartWorkflow,
  onEditSchedule,
  locateRequest,
  pendingDeleteNodeIds,
}: WorkflowGraphProps) {
  const i18n = useI18n()
  const i18nRef = useRef(i18n)
  const containerRef = useRef<HTMLDivElement>(null)
  const networkRef = useRef<Network | null>(null)
  const overlayLayerRef = useRef<HTMLDivElement | null>(null)
  const overlayRootsRef = useRef(new Map<string, { el: HTMLDivElement; root: ReturnType<typeof createRoot>; lastLayout?: string; lastPosition?: string; lastRenderSignature?: string }>())
  const rafIdRef = useRef<number | null>(null)
  const cardsRef = useRef(cards)
  const onNodeClickRef = useRef(onNodeClick)
  const onNodeMenuRef = useRef(onNodeMenu)
  const onBlankMenuRef = useRef(onBlankMenu)
  const syncDataAndOverlaysRef = useRef<() => void>(() => {})
  /** Signature of the last node/edge set pushed into the vis DataSets. When a
   *  sync produces an identical signature, the DataSet update + redraw are
   *  skipped — cards-array churn with unchanged contents is the common case. */
  const lastDataSetSignatureRef = useRef('')

  // Layout direction is controlled by the parent (TopologyModeView toolbar),
  // which also owns the persisted backend preference.

  const [groupOffsets, setGroupOffsets] = useState<Record<string, WorkflowGroupOffset>>({})
  const groupOffsetsRef = useRef(groupOffsets)
  /** Currently selected card id (null = none); selection shows the floating toolbar. */
  const [selectedCardId, setSelectedCardId] = useState<string | null>(null)
  const selectedCardIdRef = useRef(selectedCardId)
  /** Card id that the view is actively centering on. Set by selectStart locate
   *  requests and cleared when the target is deselected or the user drags/zooms
   *  the canvas. */
  const [centeringCardId, setCenteringCardId] = useState<string | null>(null)
  const centeringCardIdRef = useRef(centeringCardId)
  const toolbarOpenRef = useRef<() => void>(() => {})
  const toolbarResetRef = useRef<() => void>(() => {})
  const toolbarFoldRef = useRef<() => void>(() => {})
  const groupOffsetsVersionRef = useRef({ v: 0 })
  const layoutRef = useRef(buildEpicLayout(cards, { direction }))
  const byIdRef = useRef(new Map<string, MonoCardListItem>())
  const workingTasksRef = useRef(new Map<string, string>())
  const categoryCountsRef = useRef<ReadonlyMap<EpicCategory, number>>(new Map())
  const edgeColorsRef = useRef({ strong: '#94a3b8', subtle: '#94a3b8' })
  const edgeDrawCacheRef = useRef<WorkflowEdgeDrawCache>({
    positionKey: '', nodesForRoute: [], nodesById: new Map(), workflowByNode: new Map(), routeNodesByWorkflow: new Map(), routes: new Map(),
  })
  const agentSnapshotRef = useRef(agentInfoSnapshot)
  const directionRef = useRef(direction)
  const activeStartDragRef = useRef<StartDragState | null>(null)
  const expandedWorkflowsRef = useRef(expandedWorkflows)
  const onToggleFoldRef = useRef(onToggleFold)
  const expandedBucketsRef = useRef(expandedBuckets)
  const onToggleBucketFoldRef = useRef(onToggleBucketFold)
  const foldedCategoriesRef = useRef(foldedCategories)
  const onToggleCategoryFoldRef = useRef(onToggleCategoryFold)
  const onCategoryMenuRef = useRef(onCategoryMenu)
  const onLocateWorkflowRef = useRef(onLocateWorkflow)
  const onStartWorkflowRef = useRef(onStartWorkflow)
  const onEditScheduleRef = useRef(onEditSchedule)
  /** The locate request currently awaiting its readiness conditions (network
   *  up, container sized, target nodes in the live DataSet). Null when none is
   *  pending. Doubles as the mutex that suppresses preference-restore viewport
   *  moves while a locate is in flight — see the restore effects below. */
  const pendingLocateRef = useRef<WorkflowLocateRequest | null>(null)
  const locateRafRef = useRef<number | null>(null)
  const locateTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  /** Active centering poll. No safety timeout — the loop runs until the user
   *  breaks centering by selecting another card, deselecting, or
   *  dragging/zooming the canvas. */
  const centeringRafRef = useRef<number | null>(null)
  /** Ignore user zoom/drag events for a short window after a programmatic fit so
   *  the centering state does not cancel itself. */
  const programmaticViewUntilRef = useRef(0)

  // Reactively track theme (data-theme attribute) changes so edge colors can be
  // pushed into the live network instance without recreating it.
  const [themeKey, setThemeKey] = useState(0)
  useEffect(() => {
    const refreshEdgeColors = () => {
      edgeColorsRef.current = {
        strong: getCssVar('--border-strong', '#94a3b8'),
        subtle: getCssVar('--border-subtle', '#94a3b8'),
      }
    }
    refreshEdgeColors()
    const observer = new MutationObserver(() => {
      refreshEdgeColors()
      setThemeKey(k => k + 1)
    })
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    return () => observer.disconnect()
  }, [])

  const byId = useMemo(() => new Map(cards.map(c => [c.id, c])), [cards])
  // Agent snapshots are replaced for every status event. Keep the set identity
  // stable when its contents are unchanged so layout memoization is not invalidated.
  // Only active agents (mounted, not paused) count: a workflow whose owner is
  // unloaded/paused classifies as 待开始, not 进行中.
  const agentIdKeys = useMemo(() => (agentInfoSnapshot?.items ?? []).filter(isAgentActive).map(a => a.ActorId).sort(), [agentInfoSnapshot])
  const agentIdsRef = useRef<Set<string>>(new Set())
  const agentIdsKeyRef = useRef('')
  const agentIdsKey = agentIdKeys.join('\u0000')
  if (agentIdsKey !== agentIdsKeyRef.current) {
    agentIdsRef.current = new Set(agentIdKeys)
    agentIdsKeyRef.current = agentIdsKey
  }
  const agentIds = agentIdsRef.current
  // Layout only reads workflow maps, task cards, and scheduler bindings. Reuse
  // the input reference when other cards change so the layout memo stays valid.
  const layoutCards = useMemo(() => cards.filter(card =>
    card.type === 'workflow' || card.type === 'task' || card.type === 'scheduler'), [cards])
  // The updater's wfDedup bookkeeping (written into map-card data every few
  // seconds) is layout-invisible; keying on the whole data object would
  // recompute buildEpicLayout on every dedup write. Strip it.
  const layoutCardsKey = useMemo(() => JSON.stringify(layoutCards.map(card => [
    card.id, card.type, card.parent, card.standalone, card.status, card.modified, card.tags,
    card.data && typeof card.data === 'object'
      ? Object.fromEntries(Object.entries(card.data).filter(([k]) => k !== 'wfDedup'))
      : card.data,
  ])), [layoutCards])
  const layoutCardsRef = useRef(layoutCards)
  const layoutCardsKeyRef = useRef(layoutCardsKey)
  if (layoutCardsKey !== layoutCardsKeyRef.current) {
    layoutCardsRef.current = layoutCards
    layoutCardsKeyRef.current = layoutCardsKey
  }
  const stableLayoutCards = layoutCardsRef.current
  const layout = useMemo(() => buildEpicLayout(stableLayoutCards, { expandedWorkflows, direction, agentIds, expandedBuckets, foldedCategories }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [stableLayoutCards, direction, expandedWorkflows, agentIds, expandedBuckets, foldedCategories])
  const workingTasks = useMemo(() => buildWorkingTaskAgents(agentInfoSnapshot), [agentInfoSnapshot])

  // ── Filter state integration ────────────────────────────────────────
  // The filter store drives node visibility below: enriched nodes feed
  // applyWorkflowFilters, and the resulting visible set shapes the vis-network
  // DataSet, the DOM overlays, and the canvas edge pass. Fold/locate stay
  // untouched — they operate on the live DataSet, which only ever contains
  // visible nodes.
  const filterState = useWorkflowFilters()

  // Agent ref (actor id or agent id) → agent kind, mirroring the overlay's
  // kindOf resolution (byActorId first, agent.Id fallback).
  const agentKindByRef = useMemo(() => {
    const map = new Map<string, string>()
    for (const agent of agentInfoSnapshot?.items ?? []) {
      const kind = agent.AgentKind
      if (!kind) continue
      if (agent.ActorId) map.set(agent.ActorId, kind)
      if (agent.Id && agent.Id !== agent.ActorId) map.set(agent.Id, kind)
    }
    return map
  }, [agentInfoSnapshot])

  // Task card id → owning workflow map id (same index drawWorkflowEdges uses).
  const workflowOfCard = useMemo(() => {
    const map = new Map<string, string>()
    for (const card of cards) {
      if (card.type !== 'workflow' || card.standalone) continue
      for (const taskId of workflowTaskIds(card, cards)) map.set(taskId, card.id)
    }
    return map
  }, [cards])

  // Tree-edge parent chain (epic root → category band → archived bucket →
  // workflow start sentinel) used for ancestor preservation and band
  // subtree hiding.
  const treeParents = useMemo(() => {
    const map = new Map<string, string[]>()
    for (const edge of layout.edges) {
      if (edge.kind !== 'tree') continue
      const parents = map.get(edge.to) ?? []
      if (!parents.includes(edge.from)) parents.push(edge.from)
      map.set(edge.to, parents)
    }
    return map
  }, [layout])

  // Attach the filter metadata each node needs: display title, task status,
  // resolved owner agent kind, last activity, and structural ancestors.
  const filterableNodes = useMemo<WorkflowFilterableNode[]>(() => {
    const epicRootTitle = i18n.t('workflowGraph.epicRoot')
    const mapIdOf = (node: WorkflowNodeLayout): string =>
      node.kind === 'sentinel' ? node.cardId.slice(0, -':start'.length) : node.cardId
    return layout.nodes.map(node => {
      const isTask = node.kind === 'task' || node.kind === 'reality'
      const isMapNode = node.kind === 'root' || node.kind === 'sentinel'
      let title = node.cardId
      if (node.kind === 'epic') title = epicRootTitle
      else if (node.kind === 'category' || node.kind === 'bucket') title = node.label ?? ''
      else {
        const card = byId.get(mapIdOf(node))
        if (card) title = card.id
      }
      let status: string | undefined
      let ownerKind: string | undefined
      let lastActivity: string | undefined
      let parentIds: string[] = []
      if (isTask) {
        const card = byId.get(node.cardId)
        status = card?.status
        const data = card?.data as Record<string, unknown> | undefined
        const dataRef = data && (typeof data.agentId === 'string' ? data.agentId : typeof data.ownerAgentId === 'string' ? data.ownerAgentId : undefined)
        const ownerRef = workingTasks.get(node.cardId) ?? dataRef
        ownerKind = ownerRef ? agentKindByRef.get(ownerRef) : undefined
        lastActivity = card?.modified
        const mapId = workflowOfCard.get(node.cardId)
        if (mapId) {
          if (byId.has(mapId)) parentIds.push(mapId)
          if (byId.has(`${mapId}:start`)) parentIds.push(`${mapId}:start`)
        }
      } else if (isMapNode) {
        const mapId = mapIdOf(node)
        const mapCard = byId.get(mapId)
        lastActivity = mapCard ? workflowLastActivity(mapCard, cards) : undefined
        parentIds = (treeParents.get(`${mapId}:start`) ?? []).filter(p => byId.has(p))
      } else {
        parentIds = (treeParents.get(node.cardId) ?? []).filter(p => byId.has(p))
      }
      return { ...node, title, status, ownerKind, lastActivity, parentIds }
    })
  }, [layout, byId, cards, workingTasks, agentKindByRef, workflowOfCard, treeParents, i18n])

  const filteredNodes = useMemo(() => applyWorkflowFilters(filterableNodes, filterState), [filterableNodes, filterState])
  const visibleNodeIds = useMemo(() => {
    const ids = new Set<string>()
    for (const n of filteredNodes) if (n.visible) ids.add(n.cardId)
    return ids
  }, [filteredNodes])
  const visibleNodeIdsRef = useRef<Set<string>>(new Set())
  useEffect(() => {
    visibleNodeIdsRef.current = visibleNodeIds
  }, [visibleNodeIds])

  const { nodes, edges } = useMemo(() => {
    // vis-network owns only transparent anchor boxes at each card's center; the
    // cards themselves are DOM overlays and all edges are hand-drawn on canvas.
    const accentColor = getCssVar('--accent', 'hsl(210 90% 55%)')

    const nodes = layout.nodes.filter(node => visibleNodeIds.has(node.cardId)).map(node => ({
      id: node.cardId,
      x: node.x + node.width / 2,
      y: node.y + node.height / 2,
      shape: 'box' as const,
      label: '',
      widthConstraint: { minimum: node.width, maximum: node.width },
      heightConstraint: { minimum: node.height, maximum: node.height },
      borderWidth: 0,
      color: {
        background: 'transparent',
        border: 'transparent',
        highlight: { background: 'transparent', border: accentColor },
      },
      font: { size: 12, color: 'transparent', face: 'system-ui, -apple-system, sans-serif' },
      fixed: node.kind !== 'sentinel' ? { x: true, y: true } : { x: false, y: false },
    }))

    const edges = layout.edges.map((e, i) => ({
      id: `${e.from}->${e.to}-${i}`,
      from: e.from,
      to: e.to,
      kind: e.kind,
      hidden: true,
    }))

    return { nodes, edges }
  }, [layout, themeKey, visibleNodeIds])

  useEffect(() => {
    groupOffsetsRef.current = groupOffsets
  }, [groupOffsets])

  useEffect(() => {
    selectedCardIdRef.current = selectedCardId
  }, [selectedCardId])

  useEffect(() => {
    centeringCardIdRef.current = centeringCardId
  }, [centeringCardId])

  // When the user selects a different card (or deselects), clear centering.
  // This catches the case where the RAF loop already stopped after a
  // successful fit: without this guard the stale centeringCardId would
  // persist indefinitely, blocking viewport preference restore.
  useEffect(() => {
    if (centeringCardId && selectedCardId !== centeringCardId) {
      setCenteringCardId(null)
    }
  }, [selectedCardId, centeringCardId])

  useEffect(() => {
    i18nRef.current = i18n
    cardsRef.current = cards
    layoutRef.current = layout
    byIdRef.current = byId
    workingTasksRef.current = workingTasks
    const categoryCounts = new Map<EpicCategory, number>()
    const seenWorkflowIds = new Set<string>()
    for (const card of cards) {
      if (card.type !== 'workflow') continue
      if (seenWorkflowIds.has(card.id)) continue
      seenWorkflowIds.add(card.id)
      const category = classifyWorkflowCategory(card, agentIds, { cards })
      categoryCounts.set(category, (categoryCounts.get(category) ?? 0) + 1)
    }
    categoryCountsRef.current = categoryCounts
    agentSnapshotRef.current = agentInfoSnapshot
    directionRef.current = direction
    onNodeClickRef.current = onNodeClick
    onNodeMenuRef.current = onNodeMenu
    onBlankMenuRef.current = onBlankMenu
    expandedWorkflowsRef.current = expandedWorkflows
    onToggleFoldRef.current = onToggleFold
    expandedBucketsRef.current = expandedBuckets
    onToggleBucketFoldRef.current = onToggleBucketFold
    foldedCategoriesRef.current = foldedCategories
    onToggleCategoryFoldRef.current = onToggleCategoryFold
    onCategoryMenuRef.current = onCategoryMenu
    onLocateWorkflowRef.current = onLocateWorkflow
    onStartWorkflowRef.current = onStartWorkflow
    onEditScheduleRef.current = onEditSchedule
  }, [i18n, cards, layout, byId, workingTasks, agentInfoSnapshot, onNodeClick, onNodeMenu, onBlankMenu, expandedWorkflows, onToggleFold, expandedBuckets, onToggleBucketFold, foldedCategories, onToggleCategoryFold, onCategoryMenu, onLocateWorkflow, onStartWorkflow, onEditSchedule])

  // Restore the independent viewport and whole-workflow offset for the selected
  // direction. Initial mount is handled by the network setup effect below.
  useEffect(() => {
    const net = networkRef.current
    if (!net) return
    let cancelled = false

    void Promise.all([
      loadPreference(directionPreferenceKey(GROUP_OFFSETS_PREF_KEY_PREFIX, direction)),
      loadPreference(directionPreferenceKey(VIEWPORT_PREF_KEY_PREFIX, direction)),
    ]).then(([offsetsRaw, viewportRaw]) => {
      if (cancelled || networkRef.current !== net) return

      if (offsetsRaw) {
        try {
          const parsed = JSON.parse(offsetsRaw) as Record<string, WorkflowGroupOffset>
          const valid = Object.fromEntries(
            Object.entries(parsed).filter(([, offset]) => typeof offset?.x === 'number' && typeof offset?.y === 'number'),
          )
          groupOffsetsRef.current = valid
          setGroupOffsets(valid)
        } catch {}
      } else {
        groupOffsetsRef.current = {}
        setGroupOffsets({})
      }

      // A locate or centering may be in flight; restoring the saved viewport now
      // would override the fit, so defer it. Group offsets are restored
      // regardless — they shape the layout, not the view.
      if (pendingLocateRef.current || centeringCardIdRef.current) return
      try {
        const viewport = viewportRaw ? JSON.parse(viewportRaw) as { x?: unknown; y?: unknown; scale?: unknown } : null
        if (typeof viewport?.x === 'number' && typeof viewport.y === 'number' && typeof viewport.scale === 'number') {
          net.moveTo({ position: { x: viewport.x, y: viewport.y }, scale: viewport.scale, animation: false })
          return
        }
      } catch {}
      net.fit({ animation: { duration: 300, easingFunction: 'easeInOutQuad' } })
    })

    return () => { cancelled = true }
  }, [direction])

  const syncDataAndOverlays = useCallback(() => {
    const network = networkRef.current
    const overlayLayer = overlayLayerRef.current
    if (!network || !overlayLayer) return

    const layout = layoutRef.current
    const byId = byIdRef.current
    const workingTasks = workingTasksRef.current
    const i18nValue = i18nRef.current

    const { nodes: nodesDS, edges: edgesDS } = getInternalDataSets(network)

    // Short-circuit the full vis DataSet update + redraw when the node/edge
    // set is content-identical to the last sync. The common case is cards
    // array reference churn (a wfDedup bookkeeping write, an unrelated card
    // edit, a status-event snapshot refresh) that rebuilds the memoized node
    // and edge objects without changing any of their data. The overlay loop
    // below still runs in this case: per-overlay signatures guard React
    // renders, and the selected/centering class updates and the overlay
    // removal pass must stay live. Delete/add paths are unchanged and run
    // whenever the signature actually differs.
    const nextSignature = dataSetSignature(nodes, edges)
    const dataSetsChanged = nextSignature !== lastDataSetSignatureRef.current
    if (dataSetsChanged) {
      lastDataSetSignatureRef.current = nextSignature

      if (nodesDS) {
        const newNodeIds = new Set(nodes.map(n => n.id))
        const oldNodeIds = nodesDS.getIds() as string[]
        const toRemove = oldNodeIds.filter(id => !newNodeIds.has(id))
        if (toRemove.length > 0) nodesDS.remove(toRemove)
        nodesDS.update(nodes as unknown as Record<string, unknown>[])
      }

      if (edgesDS) {
        const newEdgeIds = new Set(edges.map(e => e.id))
        const oldEdgeIds = edgesDS.getIds() as string[]
        const toRemove = oldEdgeIds.filter(id => !newEdgeIds.has(id))
        if (toRemove.length > 0) edgesDS.remove(toRemove)
        edgesDS.update(edges as unknown as Record<string, unknown>[])
      }
    }

    // Re-render / create React card overlays for the current node set. Nodes
    // hidden by the active filters get no anchor node in the DataSet and no
    // overlay — the removal pass below unmounts them when filters change.
    for (const node of layout.nodes) {
      if (!visibleNodeIdsRef.current.has(node.cardId)) continue
      const card = byId.get(node.cardId)
      const isSentinel = node.kind === 'sentinel'
      const isSelected = selectedCardId === node.cardId
      const isCentering = centeringCardId === node.cardId

      let entry = overlayRootsRef.current.get(node.cardId)
      if (!entry) {
        const el = document.createElement('div')
        el.dataset.cardId = node.cardId
        el.className = 'workflow-node-overlay'
        el.setAttribute('aria-hidden', 'true')
        overlayLayer.appendChild(el)
        const root = createRoot(el)
        entry = { el, root }
        overlayRootsRef.current.set(node.cardId, entry)
      }

      // Update selected / centering classes on the overlay wrapper.
      if (isSelected) entry.el.classList.add('workflow-node-overlay--selected')
      else entry.el.classList.remove('workflow-node-overlay--selected')
      if (isCentering) entry.el.classList.add('workflow-node-overlay--centering')
      else entry.el.classList.remove('workflow-node-overlay--centering')

      const renderIfChanged = (signature: string, overlay: ReactNode) => {
        if (entry.lastRenderSignature === signature) return
        entry.root.render(overlay)
        entry.lastRenderSignature = signature
      }
      const signature = (parts: Record<string, unknown>) => JSON.stringify(parts)
      const kindOf = (actorId: string | undefined) =>
        actorId ? agentSnapshotRef.current?.byActorId.get(actorId)?.AgentKind : undefined

      // ── Epic tree structural nodes (render-only badges, never real cards) ──
      if (node.kind === 'epic') {
        renderIfChanged(signature({ kind: node.kind, locale: i18nValue.locale }),
          <div className="workflow-node-overlay-inner workflow-epic-root-badge">
            <Layers size={16} />
            <span>{i18nValue.t('workflowGraph.epicRoot')}</span>
          </div>,
        )
        continue
      }
      if (node.kind === 'category') {
        const cat = (node.label ?? '') as EpicCategory
        const count = categoryCountsRef.current.get(cat) ?? 0
        // Fold toggle mirrors the archived bucket's, on the right edge.
        const collapsed = foldedCategoriesRef.current?.has(cat) ?? false
        renderIfChanged(signature({ kind: node.kind, cat, count, collapsed, locale: i18nValue.locale }),
          <div className={`workflow-node-overlay-inner workflow-epic-category-badge workflow-epic-category-badge--${cat}${collapsed ? ' workflow-epic-category-badge--collapsed' : ''}`}>
            <span className="workflow-epic-category-label">{i18nValue.t(`workflowGraph.category.${cat}` as never, { defaultValue: cat })}</span>
            <span className="workflow-epic-category-count">{count}</span>
            <button
              className="workflow-fold-btn"
              title={collapsed ? i18nValue.t('workflowGraph.expand') : i18nValue.t('workflowGraph.collapse')}
              onClick={(e) => { e.stopPropagation(); onToggleCategoryFoldRef.current?.(cat) }}
              onMouseDown={(e) => e.stopPropagation()}
            >
              {collapsed
                ? <ChevronRight size={14} className="workflow-folded-chevron" />
                : <ChevronLeft size={14} className="workflow-folded-chevron" />}
            </button>
          </div>,
        )
        continue
      }
      if (node.kind === 'bucket') {
        // Virtual archived time bucket: fold toggle only, never a real card.
        // The fold button mirrors the start sentinel's, on the right edge.
        const collapsed = !(expandedBucketsRef.current?.has(node.cardId) ?? false)
        renderIfChanged(signature({ kind: node.kind, label: node.label, count: node.count, collapsed, locale: i18nValue.locale }),
          <div className={`workflow-node-overlay-inner workflow-epic-bucket-badge${collapsed ? ' workflow-epic-bucket-badge--collapsed' : ''}`}>
            <span className="workflow-epic-bucket-label">{node.label}</span>
            {typeof node.count === 'number' && <span className="workflow-epic-category-count">{node.count}</span>}
            <button
              className="workflow-fold-btn"
              title={collapsed ? i18nValue.t('workflowGraph.expand') : i18nValue.t('workflowGraph.collapse')}
              onClick={(e) => { e.stopPropagation(); onToggleBucketFoldRef.current?.(node.cardId) }}
              onMouseDown={(e) => e.stopPropagation()}
            >
              {collapsed
                ? <ChevronRight size={14} className="workflow-folded-chevron" />
                : <ChevronLeft size={14} className="workflow-folded-chevron" />}
            </button>
          </div>,
        )
        continue
      }
      // Detect a folded start sentinel (card-sized, not a circle).
      const mapIdOfSentinel = isSentinel ? node.cardId.slice(0, -':start'.length) : ''
      const isFoldedStart = isSentinel && !(expandedWorkflowsRef.current?.has(mapIdOfSentinel) ?? false)

      // Floating above-card toolbar for the selected node (left-click menu).
      const toolbar = isSelected ? (
        <div className="workflow-card-toolbar">
          <button
            className="workflow-card-toolbar__btn"
            title={i18nValue.t('workflowGraph.openCard')}
            onClick={toolbarOpenRef.current}
          >
            <ExternalLink size={14} />
          </button>
          {isSentinel && (
            <button
              className="workflow-card-toolbar__btn"
              title={isFoldedStart ? i18nValue.t('workflowGraph.expand') : i18nValue.t('workflowGraph.collapse')}
              onClick={toolbarFoldRef.current}
            >
              {isFoldedStart ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
            </button>
          )}
        </div>
      ) : null

      if (node.kind === 'ghost') {
        // Virtual preview of the workflow template a future instance will
        // mount under this scheduler. Dashed + dimmed so it reads as
        // simulated, not as a live workflow. Click opens the template.
        const tplCard = node.templateId ? byId.get(node.templateId) : undefined
        if (!tplCard) continue
        renderIfChanged(signature({ kind: node.kind, tplId: node.templateId, tplModified: tplCard.modified, locale: i18nValue.locale }),
          <I18nContext.Provider value={i18nValue}>
            <div
              className="workflow-node-overlay-inner workflow-node-overlay-inner--ghost"
              onMouseDown={(e) => e.stopPropagation()}
              onClick={(e) => { e.stopPropagation(); onNodeClickRef.current?.(node.templateId!, node.templateId!) }}
            >
              <div className="workflow-ghost-card workflow-node-card">
                <div className="workflow-ghost-tag">{i18nValue.t('workflowGraph.category.template')}</div>
                <div className="workflow-ghost-title">{tplCard.id}</div>
              </div>
            </div>
          </I18nContext.Provider>,
        )
        continue
      }

      if (node.kind === 'scheduler') {
        // Scheduler card node in the planned band: card-sized (same as task
        // nodes) with a clock button (opens the schedule modal) on the left
        // border and a fold toggle to show/hide its instance workflows.
        if (!card) continue
        const collapsed = !(expandedBucketsRef.current?.has(node.cardId) ?? false)
        const schedule = (card.data as Record<string, unknown> | undefined)?.schedule as Record<string, unknown> | undefined
        const cron = typeof schedule?.cron === 'string' ? schedule.cron : ''
        const enabled = schedule?.enabled !== false
        const titleOverride = card.id.replace(/^sched:/, '')
        renderIfChanged(signature({ kind: node.kind, cardModified: card.modified, count: node.count, collapsed, enabled, cron, locale: i18nValue.locale }),
          <I18nContext.Provider value={i18nValue}>
            <div className={`workflow-node-overlay-inner workflow-node-overlay-inner--scheduler${collapsed ? ' workflow-node-overlay-inner--collapsed' : ''}`}>
              {toolbar}
              <button
                className="workflow-status-btn workflow-status-btn--scheduler"
                title={cron || i18nValue.t('scheduled.edit.button')}
                aria-label="Edit schedule"
                onClick={(e) => { e.stopPropagation(); onEditScheduleRef.current?.(node.cardId) }}
                onMouseDown={(e) => e.stopPropagation()}
              >
                {enabled ? <Clock size={15} /> : <Pause size={15} />}
              </button>
              <MonoCardCompact
                card={card}
                compact
                className="workflow-node-card"
                titleOverride={titleOverride}
                variant="workflow"
              />
              <button
                className="workflow-fold-btn"
                title={collapsed ? i18nValue.t('workflowGraph.expand') : i18nValue.t('workflowGraph.collapse')}
                onClick={(e) => { e.stopPropagation(); onToggleBucketFoldRef.current?.(node.cardId) }}
                onMouseDown={(e) => e.stopPropagation()}
              >
                {collapsed
                  ? <ChevronRight size={14} className="workflow-folded-chevron" />
                  : <ChevronLeft size={14} className="workflow-folded-chevron" />}
              </button>
            </div>
          </I18nContext.Provider>,
        )
        continue
      }

      if (isSentinel) {
        // The start sentinel carries the orchestrator (map owner) kind as a dot.
        const mapCard = byId.get(mapIdOfSentinel)
        const ownerId = mapCard ? workflowOwnerAgentId(mapCard) : undefined
        const ownerAgent = ownerId ? agentSnapshotRef.current?.byActorId.get(ownerId) : undefined
        const ownerActive = isAgentActive(ownerAgent)
        const ownerKind = kindOf(ownerId)
        const rawWorkflowStatus = workflowStatus(mapCard?.status)
        // Follow the to-start classification: a map marked doing whose owner
        // agent is not active (unloaded / gone) is 待开始 everywhere — the
        // band, this status button, and the edge styling all agree. A
        // paused-but-loaded owner keeps the paused affordance instead:
        // clicking the triangle resumes it (same as the composer's resume),
        // not a fresh start.
        const ownerPaused = ownerAgent?.Status === 'paused'
        const currentWorkflowStatus = rawWorkflowStatus === 'doing' && !ownerActive ? (ownerPaused ? 'paused' : 'start') : rawWorkflowStatus
        const startActive = ownerActive && (rawWorkflowStatus === 'doing' || (!!mapCard && ownerAgent?.ActiveWorkflowMapCardId === mapCard.id && workflowTaskIds(mapCard, cards).some(taskId => isActiveTarget(taskId, workingTasks))))
        const startDone = currentWorkflowStatus === 'done'
        const handleWorkflowStatusClick = async (event: MouseEvent) => {
          event.stopPropagation()
          if (!mapCard) return
          // 待开始 → start the workflow: the parent starts the bound owner
          // agent directly, or opens the agent pick/create dialog when none
          // is bound.
          if (currentWorkflowStatus === 'start') {
            onStartWorkflowRef.current?.(mapCard.id)
            return
          }
          const ownerTarget = ownerAgent?.ActorId || ownerId
          // 进行中 → pause the owner. A waiting owner is supervising its
          // children, so workflow_pause_all cascades turn_pause to every
          // child; a running owner is mid-turn, so turn_pause halts just the
          // owner's engine. The map-card status mirrors the agent so the
          // button flips to a play (resume) affordance.
          if (currentWorkflowStatus === 'doing') {
            if (ownerTarget) {
              const pauseCall = ownerAgent?.Status === 'waiting'
                ? agentTurn.workflowPauseAll(client, {}, { target: ownerTarget })
                : agentTurn.turnPause(client, { target: ownerTarget })
              pauseCall.catch((err: unknown) => console.error('[workflow] pause failed:', err))
            }
            await projectClient.wikiSetStatus(client, { Id: mapCard.id, Status: 'paused', ExpectedStatus: mapCard.status })
            return
          }
          // 已暂停 → resume the owner. turn_resume transitions a paused-from-
          // waiting workflow owner back to waiting and fans turn_resume out to
          // every child (resumeChildAgents).
          if (currentWorkflowStatus === 'paused') {
            if (ownerTarget) {
              agentTurn.turnResume(client, { target: ownerTarget }).catch((err: unknown) => console.error('[workflow] resume failed:', err))
            }
            await projectClient.wikiSetStatus(client, { Id: mapCard.id, Status: 'doing', ExpectedStatus: mapCard.status })
            return
          }
          await projectClient.wikiSetStatus(client, { Id: mapCard.id, Status: nextWorkflowStatus(currentWorkflowStatus), ExpectedStatus: mapCard.status })
        }

        // Folded start in epic mode → render as a compact card with fold chevron.
        if (isFoldedStart && mapCard) {
          const taskCount = workflowTaskIds(mapCard, cards).length
          const lastActivity = workflowLastActivity(mapCard, cards)
          const isTemplateMap = mapCard.standalone === true || mapCard.data?.template === true
          const isInstanceMap = typeof mapCard.data?.instance_of === 'string'
          renderIfChanged(signature({
            kind: node.kind,
            mapModified: mapCard.modified,
            mapStatus: mapCard.status,
            taskCount,
            lastActivity,
            startActive,
            startDone,
            selected: isSelected,
            folded: isFoldedStart,
            ownerKind,
            ownerActive,
            pendingDelete: pendingDeleteNodeIds?.has(node.cardId) ?? false,
            isTemplate: isTemplateMap,
            isInstance: isInstanceMap,
            locale: i18nValue.locale,
          }),
            <I18nContext.Provider value={i18nValue}>
              <div
                className={`workflow-node-overlay-inner workflow-node-overlay-inner--sentinel-start workflow-folded-card${startActive ? ' workflow-node-overlay-inner--active' : ''}${startDone ? ' workflow-node-overlay-inner--sentinel-done' : ''}${isTemplateMap ? ' workflow-node-overlay-inner--template' : ''}${isInstanceMap ? ' workflow-node-overlay-inner--instance' : ''}${pendingDeleteNodeIds?.has(node.cardId) ? ' workflow-node-overlay-inner--pending-delete' : ''}`}
              >
                {toolbar}
                <div className="workflow-folded-card-body">
                  <button
                    className={`workflow-status-btn workflow-status-btn--${currentWorkflowStatus}`}
                    title={currentWorkflowStatus}
                    aria-label={`Workflow status: ${currentWorkflowStatus}`}
                    onClick={handleWorkflowStatusClick}
                    onMouseDown={(e) => e.stopPropagation()}
                  >
                    <WorkflowStatusIcon status={currentWorkflowStatus} />
                  </button>
                  <div className="workflow-folded-card-info">
                    <span className="workflow-folded-card-title">{mapCard.id}</span>
                    <span className="workflow-folded-card-meta">
                      {taskCount} {taskCount === 1 ? i18nValue.t('workflowGraph.taskSingular', { defaultValue: 'task' }) : i18nValue.t('workflowGraph.taskPlural', { defaultValue: 'tasks' })}{mapCard.status ? ` · ${mapCard.status}` : ''}
                      {lastActivity ? ` · ${formatRelativeTime(lastActivity, i18nValue.locale)}` : ''}
                    </span>
                  </div>
                  <button
                    className="workflow-fold-btn"
                    title={i18nValue.t('workflowGraph.expand')}
                    onClick={(e) => { e.stopPropagation(); onToggleFoldRef.current?.(mapIdOfSentinel) }}
                    onMouseDown={(e) => e.stopPropagation()}
                  >
                    <ChevronRight size={14} className="workflow-folded-chevron" />
                  </button>
                </div>
              </div>
            </I18nContext.Provider>,
          )
          continue
        }

        renderIfChanged(signature({
          kind: node.kind,
          mapModified: mapCard?.modified,
          mapStatus: mapCard?.status,
          startActive,
          startDone,
          selected: isSelected,
          folded: isFoldedStart,
          ownerKind,
          ownerActive,
          ownerId: ownerId ?? '',
          pendingDelete: pendingDeleteNodeIds?.has(node.cardId) ?? false,
          isTemplate: mapCard?.standalone === true || mapCard?.data?.template === true,
          isInstance: typeof mapCard?.data?.instance_of === 'string',
          locale: i18nValue.locale,
        }),
          <I18nContext.Provider value={i18nValue}>
          <div
            className={`workflow-node-overlay-inner workflow-node-overlay-inner--sentinel workflow-node-overlay-inner--sentinel-start${startActive ? ' workflow-node-overlay-inner--active' : startDone ? ' workflow-node-overlay-inner--sentinel-done' : ''}${mapCard?.standalone === true || mapCard?.data?.template === true ? ' workflow-node-overlay-inner--template' : ''}${typeof mapCard?.data?.instance_of === 'string' ? ' workflow-node-overlay-inner--instance' : ''}${pendingDeleteNodeIds?.has(node.cardId) ? ' workflow-node-overlay-inner--pending-delete' : ''}`}
            title={ownerKind}
            >
              {toolbar}
              <button
                className={`workflow-status-btn workflow-status-btn--${currentWorkflowStatus}`}
                title={currentWorkflowStatus}
                aria-label={`Workflow status: ${currentWorkflowStatus}`}
                onClick={handleWorkflowStatusClick}
                onMouseDown={(e) => e.stopPropagation()}
              >
                <WorkflowStatusIcon status={currentWorkflowStatus} />
              </button>
              <span className="workflow-sentinel-label workflow-sentinel-title">{mapIdOfSentinel}</span>
              <button
                className="workflow-fold-btn"
                title={i18nValue.t('workflowGraph.collapse')}
                onClick={(e) => { e.stopPropagation(); onToggleFoldRef.current?.(mapIdOfSentinel) }}
                onMouseDown={(e) => e.stopPropagation()}
              >
                <ChevronLeft size={14} />
              </button>
              {ownerId && (
                <div className="workflow-sentinel-agent-corner">
                  <CardAgentAvatar
                    agentRef={ownerId}
                    onClick={(agent) => {
                      if (!agent) return
                      requestOpenAgentChat(agent.ProjectId, agent.ActorId || agent.Id)
                    }}
                  />
                </div>
              )}
            </div>
          </I18nContext.Provider>,
        )
        continue
      }

      if (!card) continue
      const isRoot = node.kind === 'root'
      const ownerId = isRoot ? workflowOwnerAgentId(card) : undefined
      const boundAgentRef = workingTasks.get(node.cardId)
      const active = (node.kind === 'task' || node.kind === 'reality') && isActiveTarget(node.cardId, workingTasks)
      // Card border mirrors status (root only): done → green, doing → blue;
      // task/reality cards and unstarted maps keep the neutral base border.
      const statusBorderClass = isRoot && card.status === 'done'
        ? ' workflow-node-overlay-inner--card-done'
        : isRoot && card.status === 'doing'
          ? ' workflow-node-overlay-inner--root-doing'
          : ''

      // Leading dot: bound/owner agent kind color; unbound tasks fall back to status color.
      const agentId = boundAgentRef ?? ownerId
      const dotKind = isRoot ? kindOf(ownerId) : kindOf(boundAgentRef)
      const leadingDot = dotKind || isRoot
        ? { color: agentKindDotColor(dotKind), title: dotKind }
        : { color: STATUS_META[card.status ?? '']?.color ?? 'var(--text-tertiary)', title: card.status }
      const isTemplateCard = card.standalone === true || card.data?.template === true
      const isInstanceCard = typeof card.data?.instance_of === 'string'

      renderIfChanged(signature({
        kind: node.kind,
        cardModified: card.modified,
        cardStatus: card.status,
        active,
        selected: isSelected,
        agentId,
        dotKind,
        pendingDelete: pendingDeleteNodeIds?.has(node.cardId) ?? false,
        isTemplate: isTemplateCard,
        isInstance: isInstanceCard,
        locale: i18nValue.locale,
      }),
        <I18nContext.Provider value={i18nValue}>
          <div
            className={`workflow-node-overlay-inner workflow-node-overlay-inner--${node.kind}${active ? ' workflow-node-overlay-inner--active' : ''}${statusBorderClass}${isTemplateCard ? ' workflow-node-overlay-inner--template' : ''}${isInstanceCard ? ' workflow-node-overlay-inner--instance' : ''}${pendingDeleteNodeIds?.has(node.cardId) ? ' workflow-node-overlay-inner--pending-delete' : ''}`}
          >
            {toolbar}
            <MonoCardCompact
              card={card}
              compact
              agentRef={agentId}
              className="workflow-node-card"
              cornerStatus={node.kind === 'task' || node.kind === 'reality' || isRoot}
              leadingDot={leadingDot}
              leadingIcon={isRoot ? 'target' : undefined}
              cornerChip={isRoot && dotKind ? { label: dotKind, color: agentKindDotColor(dotKind) } : undefined}
              variant="workflow"
            />
          </div>
        </I18nContext.Provider>,
      )
    }

    // Remove overlays for nodes that no longer exist (layout changes or
    // filter-driven hiding).
    const liveNodeIds = new Set(visibleNodeIdsRef.current)
    for (const [id, { el, root }] of [...overlayRootsRef.current]) {
      if (!liveNodeIds.has(id)) {
        root.unmount()
        el.remove()
        overlayRootsRef.current.delete(id)
      }
    }

    if (dataSetsChanged) network.redraw()
    // pendingDeleteNodeIds must be a dep: dialog open/close doesn't change
    // nodes/edges/selection, yet the red pending-delete outlines must repaint
    // the moment the confirm dialog appears and disappears.
  }, [nodes, edges, selectedCardId, centeringCardId, pendingDeleteNodeIds])

  useEffect(() => {
    syncDataAndOverlaysRef.current = syncDataAndOverlays
    syncDataAndOverlaysRef.current()
  }, [syncDataAndOverlays])

  // ── Locate (zoom-to-fit) requests ──────────────────────────────────
  //
  // Locate is condition-driven rather than frame-counted: it waits until the
  // network has actually rendered the target workflow's nodes (they enter the
  // live DataSet only after the async card load + syncDataAndOverlays) and the
  // container has a real size (a panel mount animation can leave it 0, which
  // yields a degenerate fit scale), then fits. It retries on every animation
  // frame and whenever the rendered nodes change, giving up with a warning
  // (never silently) after LOCATE_TIMEOUT_MS. This survives cold/slow card
  // loads that blew past the old fixed 30-frame cap.
  const locateNonceRef = useRef(0)

  /** Apply the pending locate once its readiness conditions hold. Reads only
   *  refs (plus the stable setSelectedCardId) so it is safe to call from a RAF
   *  loop or an effect; clears the pending request on success. */
  const attemptLocate = useCallback(() => {
    const req = pendingLocateRef.current
    if (!req) return
    const network = networkRef.current
    const container = containerRef.current
    if (!network) return
    // A zero-size container produces a degenerate fit scale; wait for layout.
    if (!container || container.clientWidth === 0 || container.clientHeight === 0) return

    const mapId = req.mapId ?? locateMapIdForCard(selectedCardIdRef.current, cardsRef.current)
    const { nodes: nodesDS } = getInternalDataSets(network)
    const networkIds = nodesDS ? (nodesDS.getIds() as string[]) : []

    let ids: string[] | null = null
    if (mapId) {
      const mapCard = cardsRef.current.find(c => c.id === mapId && c.type === 'workflow')
      if (!mapCard) return // map card not loaded yet — keep waiting
      const member = new Set([mapId, `${mapId}:start`, ...workflowTaskIds(mapCard, cardsRef.current)])
      ids = networkIds.filter(id => member.has(id))
      if (ids.length === 0) return // workflow nodes not synced into the network yet
    } else if (networkIds.length === 0) {
      // Whole-graph fit: wait until the network has at least one node.
      return
    }

    const animation = { duration: 300, easingFunction: 'easeInOutQuad' as const }
    if (ids && ids.length > 0) network.fit({ nodes: ids, animation })
    else network.fit({ animation })

    // Success — clear the pending request and the safety timeout.
    pendingLocateRef.current = null
    if (locateTimerRef.current != null) { clearTimeout(locateTimerRef.current); locateTimerRef.current = null }
  }, [])

  useEffect(() => {
    const nonce = locateRequest?.nonce ?? 0
    if (nonce === 0 || nonce === locateNonceRef.current) return
    locateNonceRef.current = nonce

    // Unfold the target workflow's full ancestor chain (category band →
    // archived bucket → the workflow itself) so its nodes render and
    // attemptLocate can find them. Resolved here because only the graph knows
    // the fallback target (the selected card's workflow) when no explicit
    // mapId is given. Idempotent — a no-op when everything is visible.
    const targetMapId = locateRequest?.mapId ?? locateMapIdForCard(selectedCardIdRef.current, cardsRef.current)
    if (targetMapId) onLocateWorkflowRef.current?.(targetMapId)

    // selectStart requests enter persistent centering state instead of the
    // one-shot locate path so they survive slow async loads until the user
    // breaks the state by selecting a different card, deselecting, or
    // dragging/zooming the canvas.
    if (locateRequest?.selectStart && targetMapId) {
      setSelectedCardId(`${targetMapId}:start`)
      setCenteringCardId(`${targetMapId}:start`)
      return
    }

    pendingLocateRef.current = locateRequest ?? null

    // Safety timeout: give up (with a warning, never silently) if the target
    // never becomes ready — e.g. a stale mapId or a permanently hidden panel.
    // Fall back to fitting the whole graph so the viewport is never left blank.
    if (locateTimerRef.current != null) clearTimeout(locateTimerRef.current)
    locateTimerRef.current = setTimeout(() => {
      if (!pendingLocateRef.current) return
      const net = networkRef.current
      const { nodes: nodesDS } = net ? getInternalDataSets(net) : {}
      if (net && nodesDS && nodesDS.getIds().length > 0) {
        net.fit({ animation: { duration: 300, easingFunction: 'easeInOutQuad' } })
      }
      console.warn('[WorkflowGraph] locate timed out waiting for the target workflow to render')
      pendingLocateRef.current = null
    }, LOCATE_TIMEOUT_MS)

    // Retry loop driven by animation frames; attemptLocate clears the pending
    // request on success, which stops the loop.
    const loop = () => {
      attemptLocate()
      if (pendingLocateRef.current != null) locateRafRef.current = requestAnimationFrame(loop)
      else locateRafRef.current = null
    }
    // Immediate attempt in case everything is already ready (re-locate).
    attemptLocate()
    if (pendingLocateRef.current != null) locateRafRef.current = requestAnimationFrame(loop)

    return () => {
      if (locateRafRef.current != null) { cancelAnimationFrame(locateRafRef.current); locateRafRef.current = null }
      if (locateTimerRef.current != null) { clearTimeout(locateTimerRef.current); locateTimerRef.current = null }
    }
  }, [locateRequest, attemptLocate])

  // Re-attempt the pending locate whenever the rendered nodes change (cards
  // loaded asynchronously, a workflow expanded, a direction switch) so it does
  // not have to wait for the next animation frame.
  useEffect(() => {
    if (pendingLocateRef.current) attemptLocate()
  }, [nodes, attemptLocate])

  // ── Centering state ──────────────────────────────────────────────────
  //
  // selectStart locate requests put the view into a "centering" mode: the UI
  // polls until the selected card (usually a workflow start sentinel) has
  // entered the live network DataSet, then fits the whole workflow. The state
  // is broken by selecting another card, deselecting, or dragging/zooming the
  // canvas, so it never fights the user. A short window after a programmatic
  // fit ignores the zoom event the fit itself emits.
  const tryCenterOnCard = useCallback((cardId: string): boolean => {
    const network = networkRef.current
    const container = containerRef.current
    if (!network) return false
    if (!container || container.clientWidth === 0 || container.clientHeight === 0) return false

    const { nodes: nodesDS } = getInternalDataSets(network)
    const networkIds = nodesDS ? (nodesDS.getIds() as string[]) : []
    if (!networkIds.includes(cardId)) return false

    const mapId = locateMapIdForCard(cardId, cardsRef.current)
    const mapCard = mapId ? cardsRef.current.find(c => c.id === mapId && c.type === 'workflow') : undefined
    const targetIds = mapId && mapCard
      ? [mapId, `${mapId}:start`, ...workflowTaskIds(mapCard, cardsRef.current)].filter(id => networkIds.includes(id))
      : [cardId]
    if (targetIds.length === 0) return false

    programmaticViewUntilRef.current = Date.now() + 500
    network.fit({ nodes: targetIds, animation: { duration: 300, easingFunction: 'easeInOutQuad' } })
    return true
  }, [])

  useEffect(() => {
    if (!centeringCardId) return
    if (selectedCardIdRef.current !== centeringCardId) {
      setCenteringCardId(null)
      return
    }

    const center = () => {
      const ok = tryCenterOnCard(centeringCardId)
      if (ok) {
        if (centeringRafRef.current != null) {
          cancelAnimationFrame(centeringRafRef.current)
          centeringRafRef.current = null
        }
      }
      return ok
    }

    if (center()) return

    const loop = () => {
      if (!centeringCardIdRef.current || selectedCardIdRef.current !== centeringCardIdRef.current) {
        centeringRafRef.current = null
        return
      }
      if (center()) return
      centeringRafRef.current = requestAnimationFrame(loop)
    }
    centeringRafRef.current = requestAnimationFrame(loop)

    // No timeout: poll until the user breaks centering by selecting another
    // card, deselecting, or dragging/zooming the canvas. The RAF loop checks
    // selectedCardIdRef on every frame and self-terminates when it diverges.

    return () => {
      if (centeringRafRef.current != null) { cancelAnimationFrame(centeringRafRef.current); centeringRafRef.current = null }
    }
  }, [centeringCardId, tryCenterOnCard])

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const network = new Network(
      container,
      { nodes: [], edges: [] },
      {
        nodes: {
          shape: 'box',
          borderWidth: 0,
          color: {
            background: 'transparent',
            border: 'transparent',
            highlight: { background: 'transparent', border: getCssVar('--accent', 'hsl(210 90% 55%)') },
          },
          font: { size: 12, color: 'transparent', face: 'system-ui, -apple-system, sans-serif' },
        },
        edges: {
          hidden: true,
          width: 0,
          color: {
            color: 'transparent',
            highlight: 'transparent',
          },
          arrows: { to: { scaleFactor: 0.6 } },
          smooth: { enabled: true, type: 'cubicBezier', forceDirection: 'horizontal', roundness: 0.5 },
        },
        physics: { enabled: false },
        interaction: {
          dragNodes: true,
          dragView: true,
          zoomView: true,
          hover: true,
          multiselect: false,
          navigationButtons: false,
          keyboard: false,
        },
        layout: { randomSeed: 2 },
        autoResize: false,
      },
    )
    networkRef.current = network
    // A brand-new Network has an empty DataSet, so the cached signature must
    // reflect that — otherwise (e.g. StrictMode's create→destroy→recreate
    // double-invoke, which does not reset refs) the initial sync below would
    // see a stale matching signature and skip the DataSet push, leaving the
    // recreated graph with zero nodes and every overlay collapsed at the
    // layer origin.
    lastDataSetSignatureRef.current = ''

    const overlayLayer = document.createElement('div')
    overlayLayer.className = 'workflow-node-overlay-layer'
    container.appendChild(overlayLayer)
    overlayLayerRef.current = overlayLayer

    // Re-activation auto-fit: the workflow pane is cacheable — it stays
    // mounted but hidden via `display:none` while another shell mode is
    // active, collapsing this container to 0×0. Track the visible→hidden→
    // shown transition so that, only at the moment of switching back, when
    // no node is on screen the view re-centers the whole graph (a locate).
    // It never fires while the pane stays open (no 0×0 transition) and
    // yields to an in-flight locate or centering, which drive their own fit.
    let hadRealSize = false
    let wasHidden = false

    // Working-edge dash animation loop. The pane is cacheable: it stays
    // mounted but hidden via `display:none` while another shell mode is
    // active, collapsing this container to 0×0. The loop must not keep
    // redrawing a hidden canvas, so `schedule()` refuses to arm a frame while
    // the container has no size, and the resize observer below calls
    // `resumeDrawing()` on the 0×0 → non-zero transition. Redraws are
    // throttled to ~15fps; the dash phase advances by real elapsed time so
    // throttled gaps and a reset baseline cannot make the dashes jump.
    let time = 0
    let keepDrawing = false
    let drawingPaused = false
    let lastFrameTs: number | null = null
    // Pane visibility is cached from the ResizeObserver's size reads. Reading
    // clientWidth inside the rAF loop forces a synchronous layout recalc
    // whenever the previous frame's afterDrawing wrote styles (drag/pan),
    // turning the idle-throttle re-arms into a 60Hz forced-reflow loop.
    let paneHidden = container.clientWidth === 0 || container.clientHeight === 0
    const containerIsHidden = () => paneHidden
    const schedule = () => {
      if (rafIdRef.current != null) return
      if (containerIsHidden()) {
        drawingPaused = true
        return
      }
      rafIdRef.current = requestAnimationFrame((t) => {
        rafIdRef.current = null
        if (containerIsHidden()) {
          drawingPaused = true
          return
        }
        if (!shouldDrawFrame(lastFrameTs, t, DRAW_INTERVAL_MS)) {
          // Not due yet — stay armed for the next rAF tick instead of wasting
          // a full redraw (all edges + node traversal) at 60fps.
          schedule()
          return
        }
        if (lastFrameTs != null) {
          // Advance the animation phase by real elapsed time. Clamp to one
          // dash period (the 1000ms in drawWorkflowEdges) so a long gap (e.g.
          // rAF frozen while the tab is backgrounded) shifts the dashes by at
          // most one full cycle — visually identical, no jump.
          time += Math.min(t - lastFrameTs, 1000)
        }
        lastFrameTs = t
        network.redraw()
      })
    }
    const resumeDrawing = () => {
      drawingPaused = false
      // Reset the time baseline: the first resumed frame draws immediately
      // (shouldDrawFrame sees lastTs === null) and advances from a fresh
      // timestamp, so the stale pre-hide `time` cannot cause a dash jump on
      // the first visible frame.
      lastFrameTs = null
      if (keepDrawing) schedule()
    }

    const resizeObserver = new ResizeObserver(() => {
      network.redraw()
      const width = container.clientWidth
      const height = container.clientHeight
      paneHidden = width === 0 || height === 0
      if (width === 0 || height === 0) {
        if (hadRealSize) wasHidden = true
        return
      }
      // Pane regained a real size: restart the animation loop if a frame was
      // refused while hidden. This also covers mount-while-hidden, where
      // `wasHidden` is never set because the pane never had a real size.
      if (drawingPaused) resumeDrawing()
      if (hadRealSize && wasHidden) {
        wasHidden = false
        if (!pendingLocateRef.current && !centeringCardIdRef.current && !anyNodeInViewport(network, container)) {
          network.fit({ animation: { duration: 300, easingFunction: 'easeInOutQuad' } })
        }
      }
      hadRealSize = true
    })
    resizeObserver.observe(container)

    let lastGridKey = ''
    const syncGrid = () => {
      const scale = network.getScale()
      const origin = network.canvasToDOM({ x: 0, y: 0 })
      // Adaptive grid: double the world step as the canvas zooms out (halve it
      // when zooming in) so on-screen spacing stays near 22px. Below 0.25x the
      // dots would saturate into a moiré — hide them entirely.
      let gridStep = 22
      while (gridStep * scale < 22) gridStep *= 2
      while (gridStep > 22 && (gridStep / 2) * scale >= 22) gridStep /= 2
      const showGrid = gridStep <= 22 * 4
      // CSS var writes force a style recalc of the grid background; skip them
      // when the values have not actually changed.
      const gridKey = `${gridStep * scale}|${origin.x},${origin.y}|${showGrid}`
      if (gridKey === lastGridKey) return
      lastGridKey = gridKey
      container.style.setProperty('--workflow-grid-size', `${gridStep * scale}px`)
      container.style.setProperty('--workflow-grid-origin', `${origin.x}px ${origin.y}px`)
      container.style.setProperty('--workflow-grid-dot', showGrid ? '' : 'transparent')
    }
    syncGrid()

    // Left-click selects a node and shows its floating toolbar; a second click
    // on the selected node opens the card. The right-click menu stays on
    // 'oncontext' below. Clicking empty space clears the selection.
    network.on('click', (params) => {
      const id = params.nodes?.[0] ? String(params.nodes[0]) : undefined
      if (!id) {
        setSelectedCardId(null)
        return
      }
      if (id.startsWith('ghost:')) {
        // Ghost preview: open the underlying template card directly.
        const ghost = layoutRef.current.nodes.find(n => n.cardId === id)
        if (ghost?.templateId) onNodeClickRef.current?.(ghost.templateId, ghost.templateId)
        return
      }
      const mapId = id.endsWith(':start') ? id.slice(0, -':start'.length) : id
      if (!cardsRef.current.some(card => card.id === mapId)) {
        setSelectedCardId(null)
        return
      }
      if (selectedCardIdRef.current === id) {
        toolbarOpenRef.current()
      } else {
        setSelectedCardId(id)
      }
    })

    network.on('oncontext', (params) => {
      const direct = params.event as { preventDefault?: () => void } | undefined
      direct?.preventDefault?.()
      ;(params.event?.srcEvent as { preventDefault?: () => void } | undefined)?.preventDefault?.()
      // Hit-test the node under the cursor: params.nodes reflects the current
      // selection, so without this the menu would only open for an already
      // selected node (and for the wrong node when the selection is stale).
      const pointer = params.pointer?.DOM
      const hit = pointer ? network.getNodeAt(pointer) : undefined
      const id = hit != null ? String(hit) : undefined
      const { x, y } = pointerClientPosition(params)
      if (!id) {
        onBlankMenuRef.current?.(x, y)
        return
      }
      // Virtual category band nodes carry no real card — route them to the
      // category menu (expand all / collapse all).
      if (id.startsWith(EPIC_CATEGORY_PREFIX)) {
        const category = id.slice(EPIC_CATEGORY_PREFIX.length) as EpicCategory
        if (EPIC_CATEGORY_ORDER.includes(category)) onCategoryMenuRef.current?.(category, x, y)
        return
      }
      const mapId = id.endsWith(':start') ? id.slice(0, -':start'.length) : id
      if (!cardsRef.current.some(card => card.id === mapId)) return
      onNodeMenuRef.current?.(mapId, x, y)
    })

    network.on('beforeDrawing', (ctx) => {
      // Read positions from body.nodes instead of network.getPosition: during a
      // layout switch (e.g. toggling epic mode) the DataSet remove/update can
      // trigger a redraw before the new node ids (virtual epic/category nodes)
      // exist, and getPosition throws for unknown ids.
      const bodyNodes = getBodyNodes(network)
      const nodePositions = new Map<string, { x: number; y: number }>()
      // Only nodes that passed the active filters take part in edge routing —
      // a filtered-out node's edges simply are not drawn.
      const visibleNodes = layoutRef.current.nodes.filter(node => visibleNodeIdsRef.current.has(node.cardId))
      for (const node of visibleNodes) {
        const position = bodyNodes[node.cardId]
        if (position) nodePositions.set(node.cardId, { x: position.x, y: position.y })
      }
      const hadActive = drawWorkflowEdges(
        ctx,
        layoutRef.current,
        visibleNodes,
        byIdRef.current,
        workingTasksRef.current,
        time,
        nodePositions,
        edgeColorsRef.current,
        edgeDrawCacheRef.current,
      )
      keepDrawing = hadActive
      if (keepDrawing) schedule()
    })

    // The whole overlay layer moves with a single transform: overlays are
    // positioned in world coordinates, and pan/zoom only rewrites the layer's
    // translate+scale. Per-node style writes happen only when a node's world
    // position or size actually changes — during a canvas pan this function
    // touches exactly one style property instead of N card subtrees.
    let lastLayerTransform = ''
    const syncOverlays = () => {
      const net = networkRef.current
      const overlay = overlayLayerRef.current
      if (!net || !overlay) return
      const layout = layoutRef.current
      const scale = net.getScale()
      const origin = net.canvasToDOM({ x: 0, y: 0 })
      const layerTransform = `translate(${origin.x}px, ${origin.y}px) scale(${scale})`
      if (layerTransform !== lastLayerTransform) {
        overlay.style.transform = layerTransform
        lastLayerTransform = layerTransform
      }

      const bodyNodes = getBodyNodes(net)
      for (const node of layout.nodes) {
        const entry = overlayRootsRef.current.get(node.cardId)
        if (!entry) continue
        const position = bodyNodes[node.cardId]
        if (!position) continue
        const layoutKey = `${node.width}x${node.height}`
        const positionKey = `${position.x},${position.y}`
        if (entry.lastLayout !== layoutKey) {
          entry.el.style.width = `${node.width}px`
          entry.el.style.height = `${node.height}px`
          entry.lastLayout = layoutKey
        }
        if (entry.lastPosition !== positionKey) {
          entry.el.style.left = `${position.x}px`
          entry.el.style.top = `${position.y}px`
          entry.lastPosition = positionKey
        }
      }
    }

    network.on('afterDrawing', () => {
      syncGrid()
      syncOverlays()
    })

    // Persist the viewport (debounced) and restore the previous one on mount;
    // falls back to fitting the whole graph when nothing was saved yet.
    const viewportVersion = { v: 0 }
    let saveTimer: ReturnType<typeof setTimeout> | undefined
    const saveViewport = () => {
      if (saveTimer) clearTimeout(saveTimer)
      saveTimer = setTimeout(() => {
        const net = networkRef.current
        if (!net) return
        const pos = net.getViewPosition()
        void savePreference(
          directionPreferenceKey(VIEWPORT_PREF_KEY_PREFIX, directionRef.current),
          JSON.stringify({ x: pos.x, y: pos.y, scale: net.getScale() }),
          `workflow-graph-viewport-${directionRef.current.toLowerCase()}`, 
          viewportVersion,
        ).catch(() => {})
      }, 400)
    }
    const groupOffsetsVersion = groupOffsetsVersionRef.current
    network.on('zoom', () => {
      if (Date.now() < programmaticViewUntilRef.current) return
      setCenteringCardId(null)
      saveViewport()
    })
    // Apply the start-node drag delta to the rest of the workflow group by
    // writing node positions directly (never moveNode — see getBodyNodes) and
    // redrawing at most once per animation frame.
    let dragRafId: number | null = null
    const applyStartDrag = (drag: StartDragState) => {
      const bodyNodes = getBodyNodes(network)
      for (const [id, position] of drag.positions) {
        if (id === drag.startId) continue
        const node = bodyNodes[id]
        if (node) {
          node.x = position.x + drag.dx
          node.y = position.y + drag.dy
        }
      }
      network.redraw()
    }
    network.on('dragStart', (params) => {
      setCenteringCardId(null)
      const startId = params.nodes?.map((node: unknown) => String(node)).find((id: string) => id.endsWith(':start'))
      if (!startId) return
      const mapId = startId.slice(0, -':start'.length)
      const mapCard = cardsRef.current.find(card => card.id === mapId)
      if (!mapCard) return
      const ids = [startId, mapId, ...workflowTaskIds(mapCard, cardsRef.current)]
      // Skip ids not in the current layout (e.g. folded workflows in epic
      // mode hide their task nodes); getPosition would throw for them.
      const bodyNodes = getBodyNodes(network)
      const positions = new Map<string, { x: number; y: number }>()
      for (const id of ids) {
        const position = bodyNodes[id]
        if (position) positions.set(id, { x: position.x, y: position.y })
      }
      const startPosition = positions.get(startId)
      if (!startPosition) return
      activeStartDragRef.current = { startId, mapId, startPosition, dx: 0, dy: 0, nodeIds: new Set(ids), positions }
    })
    network.on('dragging', () => {
      const activeStartDrag = activeStartDragRef.current
      if (!activeStartDrag) return
      const current = network.getPosition(activeStartDrag.startId)
      activeStartDrag.dx = current.x - activeStartDrag.startPosition.x
      activeStartDrag.dy = current.y - activeStartDrag.startPosition.y
      if (dragRafId != null) return
      dragRafId = requestAnimationFrame(() => {
        dragRafId = null
        const drag = activeStartDragRef.current
        if (drag) applyStartDrag(drag)
      })
    })
    network.on('dragEnd', (params) => {
      if (dragRafId != null) {
        cancelAnimationFrame(dragRafId)
        dragRafId = null
      }
      const drag = activeStartDragRef.current
      if (drag) applyStartDrag(drag)
      saveViewport()
      const startId = params.nodes?.map((node: unknown) => String(node)).find((id: string) => id.endsWith(':start'))
      if (startId) {
        // Save group offset (existing behavior).
        const mapId = startId.slice(0, -':start'.length)
        const startNode = layoutRef.current.nodes.find(node => node.cardId === startId)
        if (!startNode) {
          activeStartDragRef.current = null
          return
        }
        const position = network.getPosition(startId)
        const offset = {
          x: position.x - (startNode.x + startNode.width / 2),
          y: position.y - (startNode.y + startNode.height / 2),
        }
        if (offset.x === 0 && offset.y === 0) {
          activeStartDragRef.current = null
          return
        }
        const nextOffsets = {
          ...groupOffsetsRef.current,
          [mapId]: {
            x: (groupOffsetsRef.current[mapId]?.x ?? 0) + offset.x,
            y: (groupOffsetsRef.current[mapId]?.y ?? 0) + offset.y,
          },
        }
        groupOffsetsRef.current = nextOffsets
        setGroupOffsets(nextOffsets)
        activeStartDragRef.current = null
        void savePreference(
          directionPreferenceKey(GROUP_OFFSETS_PREF_KEY_PREFIX, directionRef.current),
          JSON.stringify(nextOffsets),
          `workflow-graph-group-offsets-${directionRef.current.toLowerCase()}`,
          groupOffsetsVersion,
        ).catch(() => {})
      }
    })

    void loadPreference(directionPreferenceKey(GROUP_OFFSETS_PREF_KEY_PREFIX, directionRef.current)).then(raw => {
      if (!raw) return
      try {
        const parsed = JSON.parse(raw) as Record<string, WorkflowGroupOffset>
        const valid = Object.fromEntries(
          Object.entries(parsed).filter(([, offset]) => typeof offset?.x === 'number' && typeof offset?.y === 'number'),
        )
        groupOffsetsRef.current = valid
        setGroupOffsets(valid)
      } catch {}
    })

    void loadPreference(directionPreferenceKey(VIEWPORT_PREF_KEY_PREFIX, directionRef.current)).then(raw => {
      const net = networkRef.current
      if (!net) return
      // A locate or centering is pending — let it win instead of restoring.
      if (pendingLocateRef.current || centeringCardIdRef.current) return
      if (raw) {
        try {
          const v = JSON.parse(raw) as { x?: unknown; y?: unknown; scale?: unknown }
          if (typeof v.x === 'number' && typeof v.y === 'number' && typeof v.scale === 'number') {
            net.moveTo({ position: { x: v.x, y: v.y }, scale: v.scale, animation: false })
            return
          }
        } catch { /* fall through to fit */ }
      }
      net.fit({ animation: false })
    })

    // Initial data sync: this creates overlays and triggers a redraw that will
    // position them in the afterDrawing handler.
    syncDataAndOverlaysRef.current()

    return () => {
      if (saveTimer) clearTimeout(saveTimer)
      if (dragRafId != null) cancelAnimationFrame(dragRafId)
      resizeObserver.disconnect()
      if (rafIdRef.current != null) {
        cancelAnimationFrame(rafIdRef.current)
        rafIdRef.current = null
      }
      for (const { root } of overlayRootsRef.current.values()) root.unmount()
      overlayRootsRef.current.clear()
      overlayLayer.remove()
      overlayLayerRef.current = null
      network.destroy()
      networkRef.current = null
    }
  }, [])

  if (layout.nodes.length === 0) {
    return (
      <div className="workflow-graph workflow-graph--empty">
        <span>No workflows yet — create a map card with task cards to chart work.</span>
      </div>
    )
  }

  const handleToolbarOpen = () => {
    const id = selectedCardIdRef.current
    if (!id) return
    const mapId = id.endsWith(':start') ? id.slice(0, -':start'.length) : id
    if (cardsRef.current.some(c => c.id === mapId)) onNodeClickRef.current?.(mapId, mapId)
  }

  // Reset clears the whole-workflow drag offset for the selected start node,
  // snapping the group back to its computed layout position.
  const handleToolbarReset = () => {
    const id = selectedCardIdRef.current
    if (!id || !id.endsWith(':start')) return
    const mapId = id.slice(0, -':start'.length)
    if (!(mapId in groupOffsetsRef.current)) return
    const nextOffsets = { ...groupOffsetsRef.current }
    delete nextOffsets[mapId]
    groupOffsetsRef.current = nextOffsets
    setGroupOffsets(nextOffsets)
    void savePreference(
      directionPreferenceKey(GROUP_OFFSETS_PREF_KEY_PREFIX, directionRef.current),
      JSON.stringify(nextOffsets),
      `workflow-graph-group-offsets-${directionRef.current.toLowerCase()}`,
      groupOffsetsVersionRef.current,
    ).catch(() => {})
    setSelectedCardId(null)
  }

  toolbarOpenRef.current = handleToolbarOpen
  toolbarResetRef.current = handleToolbarReset

  // Toggle the fold state of the selected start node's workflow (epic mode).
  const handleToolbarFold = () => {
    const id = selectedCardIdRef.current
    if (!id || !id.endsWith(':start')) return
    const mapId = id.slice(0, -':start'.length)
    onToggleFoldRef.current?.(mapId)
    setSelectedCardId(null)
  }
  toolbarFoldRef.current = handleToolbarFold

  return (
    <div className={`workflow-graph workflow-graph--${direction.toLowerCase()}`} ref={containerRef} data-guide-id={GuideIds.workflow_canvas} />
  )
})
