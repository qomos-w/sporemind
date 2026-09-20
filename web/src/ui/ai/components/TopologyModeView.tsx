import { useEffect, useMemo, useRef, useState } from 'react'
import { Network, GitBranch, Brain, Calendar, Plus, Pencil, Trash2, Search, Settings, MoveHorizontal, MoveVertical, Crosshair, Filter, Loader2 } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as memoryClient from '../../../gen-clients/local/client'
import { useMonoStore } from '../hooks/useMonoStore'
import { TopologyGraph, type NodeGraphMode, type TopologyEdgeRef } from './TopologyGraph'
import { ActorTopology } from './ActorTopology'
import { BrainMemoryGraph } from './BrainMemoryGraph'
import { WorkflowGraph } from './WorkflowGraph'
import type { WorkflowLocateRequest } from './WorkflowGraph'
import { consumePendingWorkflowLocate, subscribeWorkflowLocate, subscribeWorkflowDirectionToggle } from './workflowLocateStore'
import { useWorkflowTopoDeps, applyWorkflowTopoDeps } from './workflowTopoGraphs'
import type { EpicCategory, WorkflowDirection } from './workflowLayout'
import { epicWorkflowPlacement, categoryContents, bucketWorkflowMapIds, visibleWorkflowMapIds } from './workflowLayout'
import { workflowFoldStore } from './workflowFoldStore'
import { WorkflowCategoryContextMenu, type WorkflowCategoryMenuTarget } from './WorkflowCategoryContextMenu'
import { loadPreference, savePreference } from '../../../application/theme-persist'
import { isVirtualMountNode, type MonoCardListItem } from '../../../domain/mono-types'
import { isBuiltinCard } from '../../../domain/builtin-cards'
import type { AgentInfoSnapshot } from '../hooks/agentInfoStore'
import { getTimelineManager } from '../hooks/useTimelineManager'
import type { UnifiedGraphNode } from '../../../gen-types/observation'
import type { MemoryNode, MemorySnapshotResp } from '../../../gen-clients/system/types'
import { useClickOutside } from '../hooks/useClickOutside'
import { useViewportMode } from '../../../application/useViewportMode'
import { useI18n } from '../../../i18n'
import { topoFiltersStore, useCustomFilters, type FilterField, type CustomFilter } from './topoFiltersStore'
import { workflowFiltersStore, useWorkflowFilters } from './workflowFiltersStore'
import { NONE_OWNER_KIND } from './workflowFiltersStore'
import {
  buildChildMap,
  buildParentMap,
  collectDescendants,
  collectAncestors,
  lineageSet,
} from './topologyRelations'
import './TopologyModeView.css'

// Re-export the shared relationship helpers so existing imports (incl. tests)
// keep resolving. The implementations live in topologyRelations and are shared
// with buildTopologyEdges so the graph and the filters agree on card relations.
export { buildChildMap, buildParentMap, collectDescendants, collectAncestors, lineageSet } from './topologyRelations'

export type TopoMode = 'cards' | 'actors' | 'brain' | 'workflow'

type TimeMode = 'all' | 'ago' | 'range'
type AgoUnit = 'hours' | 'days'
type AgoDirection = 'after' | 'before'

export interface TimeFilterState {
  mode: TimeMode
  agoValue: string
  agoUnit: AgoUnit
  agoDirection: AgoDirection
  rangeFrom: string
  rangeTo: string
}

interface TopologyModeViewProps {
  cards: MonoCardListItem[]
  agentInfoSnapshot?: AgentInfoSnapshot
  activeAgentId?: string
  onNodeClick?: (cardId: string, label: string) => void
  /** Workflow-mode card menu request (right-click on a card node). */
  onWorkflowCardMenu?: (cardId: string, x: number, y: number) => void
  /** Workflow-mode blank-area menu request (right-click on empty canvas). */
  onWorkflowBlankMenu?: (x: number, y: number) => void
  /** Workflow nodes currently covered by the open delete confirmation. */
  pendingDeleteNodeIds?: ReadonlySet<string>
  onConnectNodes?: (sourceCardId: string, targetCardId: string) => void | Promise<void>
  onActorNodeClick?: (nodeId: string, label: string, node: UnifiedGraphNode) => void
  onActorNodeContext?: (nodeId: string, label: string, node: UnifiedGraphNode, x: number, y: number) => void
  onMemoryNodeClick?: (node: MemoryNode) => void
  /** Start a to-start workflow (status button on its start node): the parent
   *  starts the bound owner agent directly or opens the agent pick/create
   *  dialog when the map has no owner. */
  onStartWorkflow?: (mapId: string) => void
  /** Open the schedule (cron) modal for a scheduler card (clock button on
   *  scheduler nodes). */
  onEditSchedule?: (cardId: string) => void
  onContextMenu?: (event: { clientX: number; clientY: number; canvasX: number; canvasY: number; nodeId?: string; edge?: TopologyEdgeRef }) => void
  pendingPlacement?: { id: string; x: number; y: number } | null
  onPlacementDone?: () => void
  activeFilterIds: Set<string>
  setActiveFilterIds: React.Dispatch<React.SetStateAction<Set<string>>>
  mode?: TopoMode
  onModeChange?: (mode: TopoMode) => void
  brainMountAgentId?: string | null
}

/** Free-text filter fields offered in the toolbar "add filter" popover. Subtree
 *  and lineage are structural (card-reference) filters added from the card
 *  context menu, so they are intentionally excluded from manual free-text entry. */
const FIELD_OPTIONS: FilterField[] = ['title', 'status', 'tag', 'type', 'source']

/** i18n keys for each filter field's display label. */
const FIELD_LABEL_KEYS: Record<FilterField, string> = {
  title: 'topologyFilter.fieldTitle',
  status: 'topologyFilter.fieldStatus',
  tag: 'topologyFilter.fieldTag',
  type: 'topologyFilter.fieldType',
  source: 'topologyFilter.fieldSource',
  subtree: 'topologyFilter.fieldSubtree',
  lineage: 'topologyFilter.fieldLineage',
}

/** English fallback labels used when no translator is supplied (e.g. unit tests). */
export const DEFAULT_FIELD_LABELS: Record<FilterField, string> = {
  title: 'Title',
  status: 'Status',
  tag: 'Tag',
  type: 'Type',
  source: 'Source',
  subtree: 'Subtree',
  lineage: 'Lineage',
}

/** Structural filters reference a card by id (added via the card context menu)
 *  rather than a free-text value, so their field/value cannot be edited inline. */
export function isStructuralField(field: FilterField): boolean {
  return field === 'subtree' || field === 'lineage'
}

/** Resolve the card set selected by the active custom filters against a
 *  pre-filtered `base` list (already narrowed by the time filter). Pure (no
 *  React state) so the filter pipeline is unit-testable. */
export function applyCustomFilters(
  base: MonoCardListItem[],
  activeCustom: CustomFilter[],
  childMap: Map<string, Set<string>>,
  parentMap: Map<string, Set<string>>,
): MonoCardListItem[] {
  function matchingIds(cf: CustomFilter): Set<string> {
    if (cf.field === 'subtree') {
      const set = new Set<string>([cf.value])
      for (const id of collectDescendants(cf.value, childMap)) set.add(id)
      return set
    }
    if (cf.field === 'lineage') {
      return lineageSet(cf.value, childMap, parentMap)
    }
    return new Set(base.filter(c => matchesField(c, cf.field, cf.value)).map(c => c.id))
  }

  let resultIds = new Set<string>()
  let first = true
  for (const cf of activeCustom) {
    const ids = matchingIds(cf)
    if (first) {
      resultIds = new Set(ids)
      first = false
      continue
    }
    const op = cf.operator ?? 'and'
    if (op === 'and') {
      resultIds = new Set([...resultIds].filter(id => ids.has(id)))
    } else if (op === 'or') {
      for (const id of ids) resultIds.add(id)
    } else if (op === 'xor') {
      for (const id of ids) {
        if (resultIds.has(id)) resultIds.delete(id)
        else resultIds.add(id)
      }
    }
  }
  const connectedBuiltinIds = new Set<string>()
  for (const id of resultIds) {
    for (const ancestor of collectAncestors(id, parentMap)) {
      if (isBuiltinCard(ancestor) || isVirtualMountNode(ancestor)) connectedBuiltinIds.add(ancestor)
    }
    for (const descendant of collectDescendants(id, childMap)) {
      if (isBuiltinCard(descendant) || isVirtualMountNode(descendant)) connectedBuiltinIds.add(descendant)
    }
  }
  return base.filter(c => resultIds.has(c.id) || connectedBuiltinIds.has(c.id))
}

const OPERATOR_LABELS: Record<import('./topoFiltersStore').FilterOperator, string> = {
  and: 'AND',
  or: 'OR',
  xor: 'XOR',
}

export function filterLabel(
  field: FilterField,
  value: string,
  titleMap: Map<string, string>,
  labels: Record<FilterField, string> = DEFAULT_FIELD_LABELS,
): string {
  const displayValue = isStructuralField(field) ? (titleMap.get(value) ?? value) : value
  return `${labels[field]}: ${displayValue}`
}

const QUICK_PRESETS: { label: string; value: number; unit: AgoUnit }[] = [
  { label: '1h', value: 1, unit: 'hours' },
  { label: '24h', value: 24, unit: 'hours' },
  { label: '7d', value: 7, unit: 'days' },
  { label: '30d', value: 30, unit: 'days' },
]

/** Workflow filter option lists. Values are the raw card/agent values stored
 *  in `workflowFiltersStore`; display labels come from i18n
 *  (`workflowFilter.*` keys). */
/** Canonical task status values from project.wiki.part3 schema: backlog/todo/doing/pending_review/done/blocked/cancelled/failed. */
const WORKFLOW_STATUS_OPTIONS = ['backlog', 'todo', 'doing', 'pending_review', 'done', 'blocked', 'cancelled', 'failed'] as const
const WORKFLOW_CATEGORY_OPTIONS = ['in-progress', 'to-start', 'template', 'planned', 'completed', 'archived'] as const
const WORKFLOW_OWNER_KIND_OPTIONS = ['worker', 'reviewer', NONE_OWNER_KIND] as const

const MS_PER_HOUR = 60 * 60 * 1000
const MS_PER_DAY = 24 * MS_PER_HOUR

export function computeTimeBounds(tf: TimeFilterState): { lower: number; upper: number } {
  if (tf.mode === 'ago') {
    const n = Number(tf.agoValue)
    if (!Number.isFinite(n) || n <= 0) return { lower: 0, upper: 0 }
    const unitMs = tf.agoUnit === 'hours' ? MS_PER_HOUR : MS_PER_DAY
    const cutoff = Date.now() - n * unitMs
    return tf.agoDirection === 'before'
      ? { lower: 0, upper: cutoff }
      : { lower: cutoff, upper: 0 }
  }
  if (tf.mode === 'range') {
    const lower = tf.rangeFrom ? new Date(`${tf.rangeFrom}T00:00:00`).getTime() : 0
    const upper = tf.rangeTo ? new Date(`${tf.rangeTo}T23:59:59`).getTime() : 0
    return { lower: isNaN(lower) ? 0 : lower, upper: isNaN(upper) ? 0 : upper }
  }
  return { lower: 0, upper: 0 }
}

/** Keep cards whose `modified` time falls within [lower, upper]. A bound of 0 means open. */
export function filterCardsByTime(
  cards: MonoCardListItem[],
  lower: number,
  upper: number,
  childMap?: Map<string, Set<string>>,
  parentMap?: Map<string, Set<string>>,
): MonoCardListItem[] {
  if (lower <= 0 && upper <= 0) return cards
  const matchingIds = new Set(cards.filter(c => {
    const ms = Date.parse(c.modified)
    if (isNaN(ms)) return false
    if (lower > 0 && ms < lower) return false
    if (upper > 0 && ms > upper) return false
    return true
  }).map(c => c.id))
  const connectedBuiltinIds = new Set<string>()
  if (childMap && parentMap) {
    for (const id of matchingIds) {
      for (const ancestor of collectAncestors(id, parentMap)) {
        if (isBuiltinCard(ancestor) || isVirtualMountNode(ancestor)) connectedBuiltinIds.add(ancestor)
      }
      for (const descendant of collectDescendants(id, childMap)) {
        if (isBuiltinCard(descendant) || isVirtualMountNode(descendant)) connectedBuiltinIds.add(descendant)
      }
    }
  }
  return cards.filter(c => matchingIds.has(c.id) || connectedBuiltinIds.has(c.id))
}

/** Match a free-text title against a filter value. Supports regexp via the
 * `/pattern/flags` convention; otherwise falls back to case-insensitive
 * substring match. An invalid regexp pattern matches nothing. */
export function matchTitle(title: string, value: string): boolean {
  const re = value.match(/^\/(.+)\/([gimsuy]*)$/)
  if (re) {
    try {
      return new RegExp(re[1]!, re[2]).test(title)
    } catch {
      return false
    }
  }
  return title.toLowerCase().includes(value.toLowerCase())
}

export function matchesField(card: MonoCardListItem, field: FilterField, value: string): boolean {
  const v = value.trim()
  if (!v) return true
  switch (field) {
    case 'title': return matchTitle(card.id ?? '', v)
    case 'tag': return (card.tags ?? []).some(t => t.toLowerCase() === v.toLowerCase())
    case 'status': return (card.status ?? '').toLowerCase() === v.toLowerCase()
    case 'type': return (card.type ?? '').toLowerCase() === v.toLowerCase()
    case 'source': return (card.source ?? '').toLowerCase() === v.toLowerCase()
    default: return true
  }
}

export function collectValues(cards: MonoCardListItem[], field: FilterField): string[] {
  const set = new Set<string>()
  for (const c of cards) {
    if (field === 'tag') (c.tags ?? []).forEach(t => set.add(t))
    else if (field === 'status') { if (c.status) set.add(c.status) }
    else if (field === 'type') { if (c.type) set.add(c.type) }
    else if (field === 'source') { if (c.source) set.add(c.source) }
  }
  return [...set].sort()
}

function timeSummary(tf: TimeFilterState): string {
  if (tf.mode === 'all') return 'All time'
  if (tf.mode === 'ago') {
    const n = Number(tf.agoValue)
    if (!Number.isFinite(n) || n <= 0) return 'All time'
    const unit = tf.agoUnit === 'hours' ? (n === 1 ? 'hour' : 'hours') : (n === 1 ? 'day' : 'days')
    return tf.agoDirection === 'before'
      ? `Before ${n} ${unit} ago`
      : `Last ${n} ${unit}`
  }
  return `${tf.rangeFrom || '…'} – ${tf.rangeTo || '…'}`
}

/**
 * Unified topology container with a floating toolbar that switches between:
 *  - **cards**: wiki-card force-directed graph (vis-network)
 *  - **actors**: runtime Actor DAG
 *
 * In **cards** mode two filter families narrow the visible cards:
 *  - a precise **time filter** (quick presets, a "N days/hours ago" input, or a
 *    calendar from–to range);
 *  - user-defined **property filters** (title / status / tag / type / source),
 *    rendered as toggleable chips to the left of an add button. Chips are
 *    editable and deletable. All popovers dismiss on outside click.
 */
export function TopologyModeView({ cards, agentInfoSnapshot, activeAgentId, onNodeClick, onWorkflowCardMenu, onWorkflowBlankMenu, pendingDeleteNodeIds, onConnectNodes, onActorNodeClick, onActorNodeContext, onMemoryNodeClick, onStartWorkflow, onEditSchedule, onContextMenu, pendingPlacement, onPlacementDone, activeFilterIds, setActiveFilterIds, mode, onModeChange, brainMountAgentId }: TopologyModeViewProps) {
  const { t } = useI18n()
  const [internalMode, setInternalMode] = useState<TopoMode>('cards')
  const topoMode = mode ?? internalMode
  const setTopoMode = onModeChange ?? setInternalMode
  const [timeFilter, setTimeFilter] = useState<TimeFilterState>({ mode: 'all', agoValue: '7', agoUnit: 'days', agoDirection: 'after', rangeFrom: '', rangeTo: '' })
  const customFilters = useCustomFilters()

  // Workflow graph layout direction (LTR/TTB). Owned here so the toggle lives in
  // the floating toolbar; persisted to actor-owned backend preference.
  // Defaults to LTR immediately so the graph renders without waiting for the
  // preference round-trip (which can take up to 10s on a cold WebSocket); the
  // saved value updates it when it arrives. A brief flash on direction flip is
  // preferable to a blank panel while loadPreference is in flight.
  const [workflowDirection, setWorkflowDirection] = useState<WorkflowDirection>('LTR')
  const workflowDirVersion = useRef({ v: 0 })
  useEffect(() => {
    void loadPreference('workflowGraph.layoutDirection.v1').then(raw => {
      setWorkflowDirection(raw === 'TTB' ? 'TTB' : 'LTR')
    })
  }, [])
  const handleWorkflowDirectionChange = (dir: WorkflowDirection) => {
    setWorkflowDirection(dir)
    void savePreference('workflowGraph.layoutDirection.v1', dir, 'workflow-graph-layout', workflowDirVersion.current).catch(() => {})
  }

  // Epic tree mode is always on for workflow view. Fold preferences live in
  // the shared workflowFoldStore (persisted to actor-owned backend
  // preference, never localStorage): monoStore.load builds the server-side
  // WorkflowFilter from the same state, so folded maps and their task cards
  // are filtered out of the global card list itself. Storing folds (rather
  // than the expanded set) keeps "new workflows default to expanded" true
  // while user folds survive cards-list refreshes and remounts.
  const [foldSnapshot, setFoldSnapshot] = useState(workflowFoldStore.getSnapshot())
  useEffect(() => {
    void workflowFoldStore.ensureLoaded()
    return workflowFoldStore.subscribe(setFoldSnapshot)
  }, [])
  // Graph fetching is gated on the fold preferences settling, so a cold open
  // renders the virtual skeleton (category bands / buckets) first and only
  // then requests the workflow_topo graphs of maps that survive the persisted
  // fold state — never the all-expanded burst.
  const foldPrefsLoaded = foldSnapshot.loaded
  const foldedWorkflows = foldSnapshot.foldedWorkflows
  const foldedCategories = foldSnapshot.foldedCategories
  const expandedBuckets = foldSnapshot.expandedBuckets

  // Card-list + fold-preference fetch both go through monoStore.load (which
  // awaits the fold prefs) and can take 6-10s on a cold WebSocket; with no
  // signal the panel is a blank canvas (or a misleading "no workflows" empty
  // state in workflow mode) the whole time. Surfacing a loading status until
  // the first payload settles closes that gap.
  const monoLoading = useMonoStore(s => s.loading)
  const monoError = useMonoStore(s => s.error)
  const topoLoading = (monoLoading || !foldPrefsLoaded || !!monoError) && cards.length === 0
  /** Mutate the persisted folded-workflows set (reads the live store
   *  snapshot, so cascades from stale closures stay correct). */
  const mutateFoldedWorkflows = (mutate: (next: Set<string>) => void) => {
    const next = new Set(workflowFoldStore.getSnapshot().foldedWorkflows)
    mutate(next)
    workflowFoldStore.setFoldedWorkflows(next)
  }
  const mutateFoldedCategories = (mutate: (next: Set<EpicCategory>) => void) => {
    const next = new Set(workflowFoldStore.getSnapshot().foldedCategories)
    mutate(next)
    workflowFoldStore.setFoldedCategories(next)
  }
  const mutateExpandedBuckets = (mutate: (next: Set<string>) => void) => {
    const next = new Set(workflowFoldStore.getSnapshot().expandedBuckets)
    mutate(next)
    workflowFoldStore.setExpandedBuckets(next)
  }
  const handleToggleFold = (mapId: string) => {
    // Read the live store snapshot to decide direction before mutating, so a
    // stale component closure can't flip the cascade the wrong way.
    const unfolding = workflowFoldStore.getSnapshot().foldedWorkflows.has(mapId)
    mutateFoldedWorkflows(next => {
      if (next.has(mapId)) next.delete(mapId)
      else next.add(mapId)
    })
    if (!unfolding) return
    // Cascade: unfolding a single workflow whose ancestors (category band, and
    // for archived/planned workflows the time/scheduler bucket) are still
    // folded must also expand them — otherwise the server-side fold filter
    // keeps the workflow's task cards out of the global list and the toggle
    // looks broken. Mirrors AIShellLayout.handleCardToggleFold (edf90b6fa),
    // extended to planned scheduler buckets via epicWorkflowPlacement.
    const mapCard = cards.find(c => c.id === mapId && c.type === 'workflow')
    if (!mapCard) return
    const placement = epicWorkflowPlacement(mapCard, cards, { agentIds: currentAgentIds() })
    mutateFoldedCategories(next => {
      next.delete(placement.category)
    })
    if (placement.bucketId) {
      mutateExpandedBuckets(next => {
        next.add(placement.bucketId!)
      })
    }
  }

  /** Live agent actor ids for category classification (in-progress gating). */
  const currentAgentIds = () => new Set(agentInfoSnapshot?.byActorId.keys() ?? [])
  /** Fold the given workflow maps (no-op for an empty list). */
  const foldWorkflowsById = (mapIds: string[]) => {
    if (mapIds.length === 0) return
    mutateFoldedWorkflows(next => {
      for (const id of mapIds) next.add(id)
    })
  }
  /** Unfold the given workflow maps (no-op for an empty list). */
  const unfoldWorkflowsById = (mapIds: string[]) => {
    if (mapIds.length === 0) return
    mutateFoldedWorkflows(next => {
      for (const id of mapIds) next.delete(id)
    })
  }

  const handleToggleBucketFold = (bucketId: string) => {
    // Cascade: folding a bucket also folds the workflows inside it, so
    // re-expanding the bucket shows folded children rather than restoring
    // their previous expansion state.
    const collapsing = expandedBuckets.has(bucketId)
    mutateExpandedBuckets(next => {
      if (next.has(bucketId)) next.delete(bucketId)
      else next.add(bucketId)
    })
    if (collapsing) foldWorkflowsById(bucketWorkflowMapIds(cards, bucketId, { agentIds: currentAgentIds() }))
  }

  // Maps whose task subgraph is on screen: not individually folded, not under
  // a folded category band, and (archived only) inside an expanded bucket.
  // Empty until the persisted fold states settle — fetching graphs for maps
  // hidden by virtual-node folds would defeat the lazy loading.
  const expandedWorkflows = useMemo(
    () => foldPrefsLoaded
      ? visibleWorkflowMapIds(cards, { foldedWorkflows, foldedCategories, expandedBuckets }, { agentIds: currentAgentIds() })
      : new Set<string>(),
    [cards, foldPrefsLoaded, foldedWorkflows, foldedCategories, expandedBuckets, agentInfoSnapshot],
  )
  /** Collapse the given archived time buckets (no-op for an empty list). */
  const collapseBucketsById = (bucketIds: string[]) => {
    if (bucketIds.length === 0) return
    mutateExpandedBuckets(next => {
      for (const id of bucketIds) next.delete(id)
    })
  }
  /** Expand the given archived time buckets (no-op for an empty list). */
  const expandBucketsById = (bucketIds: string[]) => {
    if (bucketIds.length === 0) return
    mutateExpandedBuckets(next => {
      for (const id of bucketIds) next.add(id)
    })
  }
  const handleToggleCategoryFold = (category: EpicCategory) => {
    // Cascade: folding a band collapses its whole subtree (workflows, and for
    // the archived band its time buckets) so re-expanding shows folded
    // children rather than the previous expansion state.
    const folding = !foldedCategories.has(category)
    mutateFoldedCategories(next => {
      if (next.has(category)) next.delete(category)
      else next.add(category)
    })
    if (folding) {
      const { mapIds, bucketIds } = categoryContents(cards, category, { agentIds: currentAgentIds() })
      foldWorkflowsById(mapIds)
      if (bucketIds.length > 0) collapseBucketsById(bucketIds)
    }
  }

  // Right-click menu on a virtual category band node: expand/collapse the
  // band's whole subtree in one action.
  const [categoryMenu, setCategoryMenu] = useState<WorkflowCategoryMenuTarget | null>(null)
  const handleCategoryMenu = (category: EpicCategory, x: number, y: number) => {
    setCategoryMenu({ category, x, y })
  }
  /** Expand all: unfold the band itself plus every workflow (and archived
   *  time bucket) inside it. */
  const handleExpandAllInCategory = (category: EpicCategory) => {
    const { mapIds, bucketIds } = categoryContents(cards, category, { agentIds: currentAgentIds() })
    mutateFoldedCategories(next => {
      next.delete(category)
    })
    unfoldWorkflowsById(mapIds)
    expandBucketsById(bucketIds)
  }
  /** Collapse all: fold every workflow (and archived time bucket) in the
   *  band; the band itself stays open so the folded cards remain visible. */
  const handleCollapseAllInCategory = (category: EpicCategory) => {
    const { mapIds, bucketIds } = categoryContents(cards, category, { agentIds: currentAgentIds() })
    foldWorkflowsById(mapIds)
    collapseBucketsById(bucketIds)
  }

  // Idempotently unfold a workflow's full ancestor chain (category band →
  // archived bucket → the workflow itself) so a locate target's nodes render.
  // Passed to WorkflowGraph as onLocateWorkflow; refs keep it safe to call
  // from the locate effect with fresh cards/snapshot regardless of when the
  // effect closure was created.
  const cardsRef = useRef(cards)
  cardsRef.current = cards
  const agentSnapshotRef = useRef(agentInfoSnapshot)
  agentSnapshotRef.current = agentInfoSnapshot
  const ensureWorkflowVisible = (mapId: string) => {
    const currentCards = cardsRef.current
    const mapCard = currentCards.find(c => c.id === mapId && c.type === 'workflow')
    if (!mapCard) return
    const agentIds = new Set(agentSnapshotRef.current?.byActorId.keys() ?? [])
    const placement = epicWorkflowPlacement(mapCard, currentCards, { agentIds })
    mutateFoldedCategories(next => {
      next.delete(placement.category)
    })
    if (placement.bucketId) {
      mutateExpandedBuckets(next => {
        next.add(placement.bucketId!)
      })
    }
    mutateFoldedWorkflows(next => {
      next.delete(mapId)
    })
  }

  const [timeOpen, setTimeOpen] = useState(false)
  const [edgeSettingsOpen, setEdgeSettingsOpen] = useState(false)
  const [wfSettingsOpen, setWfSettingsOpen] = useState(false)
  const [wfFilterOpen, setWfFilterOpen] = useState(false)
  // Locate/zoom requests forwarded to WorkflowGraph (toolbar button or the
  // workflow mode's registered click action via the locate store).
  const [locateRequest, setLocateRequest] = useState<WorkflowLocateRequest | null>(null)
  // Consume pending locate requests (e.g. issued by the workflow mode's
  // registered click action before this view mounted).
  useEffect(() => {
    if (topoMode !== 'workflow') return
    const apply = () => {
      const intent = consumePendingWorkflowLocate()
      if (intent === null) return
      // '*' is the sentinel for "fit the whole graph" (no specific map).
      const mapId = intent.mapId === '*' ? undefined : intent.mapId
      // Unfolding the target's ancestor chain (category band → bucket →
      // workflow) happens in WorkflowGraph via onLocateWorkflow, which covers
      // both this store path and in-graph locate triggers.
      setLocateRequest(prev => ({ nonce: (prev?.nonce ?? 0) + 1, mapId, selectStart: intent.selectStart }))
    }
    apply()
    return subscribeWorkflowLocate(apply)
  }, [topoMode])

  // Subscribe to blank-menu direction toggle requests (workflow mode only).
  const workflowDirectionRef = useRef(workflowDirection)
  workflowDirectionRef.current = workflowDirection
  useEffect(() => {
    if (topoMode !== 'workflow') return
    return subscribeWorkflowDirectionToggle(() => {
      handleWorkflowDirectionChange(workflowDirectionRef.current === 'LTR' ? 'TTB' : 'LTR')
    })
  }, [topoMode])

  const [editingId, setEditingId] = useState<string | null>(null)
  const [addOpen, setAddOpen] = useState(false)
  const [formField, setFormField] = useState<FilterField>('title')
  const [formValue, setFormValue] = useState('')

  // Brain memory snapshot for the mounted agent, fetched + auto-refreshed on
  // turn progression. Passed to TopologyGraph which injects the nodes/edges
  // directly into the cards topology as a subtree of the agent node.
  const [brainSnapshot, setBrainSnapshot] = useState<MemorySnapshotResp | null>(null)
  const [brainRefresh, setBrainRefresh] = useState(0)

  const brainAgent = brainMountAgentId
    ? (agentInfoSnapshot?.byId.get(brainMountAgentId) ?? agentInfoSnapshot?.byActorId.get(brainMountAgentId))
    : undefined
  const brainTarget = brainAgent?.ActorId ?? brainMountAgentId ?? undefined

  useEffect(() => {
    if (!brainTarget) { setBrainSnapshot(null); return }
    const manager = getTimelineManager()
    const bump = () => setBrainRefresh(n => n + 1)
    return manager.subscribe(bump, brainTarget)
  }, [brainTarget])

  useEffect(() => {
    if (!brainTarget) { setBrainSnapshot(null); return }
    let cancelled = false
    void memoryClient.memorySnapshot(client, {}, { target: brainTarget }).then(result => {
      if (!cancelled) setBrainSnapshot(result)
    }).catch(() => { if (!cancelled) setBrainSnapshot(null) })
    return () => { cancelled = true }
  }, [brainTarget, brainRefresh])

  const timeRef = useRef<HTMLDivElement>(null)
  const edgeSettingsRef = useRef<HTMLDivElement>(null)
  const wfSettingsRef = useRef<HTMLDivElement>(null)
  const wfFilterRef = useRef<HTMLDivElement>(null)
  const editRef = useRef<HTMLDivElement>(null)
  const addRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<HTMLDivElement>(null)
  const toolbarRef = useRef<HTMLDivElement>(null)

  const isMobile = useViewportMode() === 'mobile'
  const [overflowCompact, setOverflowCompact] = useState(false)
  const showCompact = isMobile || overflowCompact

  // Edge-visibility toggles for the card graph: Parent (solid), Tag (dashed),
  // Type (dotted/system), Wikiword (weak, hidden by default), Depends_on
  // (task-card dependency arrows, orange dashed).
  const [edgeVisibility, setEdgeVisibility] = useState({ parent: true, tag: true, type: true, wikiword: false, depends_on: true, agent_binding: true })
  const [nodeGraphMode, setNodeGraphMode] = useState<NodeGraphMode>('default')
  const toggleEdge = (kind: 'parent' | 'tag' | 'type' | 'wikiword' | 'depends_on' | 'agent_binding') => {
    setEdgeVisibility(prev => ({ ...prev, [kind]: !prev[kind] }))
  }
  // Authoritative per-task dependencies from the maps' workflow_topo graphs
  // (monoStore graph cache). Refetched automatically when graph_changed fires
  // (e.g. another client saved the graph), so both the cards-mode dependency
  // arrows and the workflow map layout pick up remote edits without a manual
  // reload.
  const topoDeps = useWorkflowTopoDeps(cards, expandedWorkflows)
  const cardsWithGraphDeps = useMemo(() => applyWorkflowTopoDeps(cards, topoDeps), [cards, topoDeps])

  const depEdges = useMemo(() => cardsWithGraphDeps.flatMap(card => {
    const deps = card.type === 'task' ? (card.data?.depends_on ?? []) : []
    const ids = Array.isArray(deps) ? deps.map(String) : typeof deps === 'string' ? deps.replace(/^\[|\]$/g, '').split(',').map(id => id.trim()).filter(Boolean) : []
    return ids.map(to => ({ from: card.id, to }))
  }), [cardsWithGraphDeps])

  useClickOutside(timeRef, () => setTimeOpen(false), timeOpen)
  useClickOutside(edgeSettingsRef, () => setEdgeSettingsOpen(false), edgeSettingsOpen)
  useClickOutside(wfSettingsRef, () => setWfSettingsOpen(false), wfSettingsOpen)
  useClickOutside(wfFilterRef, () => setWfFilterOpen(false), wfFilterOpen)
  useClickOutside(editRef, () => setEditingId(null), editingId !== null)
  useClickOutside(addRef, () => setAddOpen(false), addOpen)

  // Collapse the inline custom-filter bar into a "+" trigger whenever the
  // floating toolbar can't fit without clipping its container (narrow desktop
  // windows, or many chips). Actual left-edge overflow forces compact; we only
  // expand back once the view is clearly roomy (hysteresis to avoid flicker).
  useEffect(() => {
    const view = viewRef.current
    const toolbar = toolbarRef.current
    if (!view || !toolbar || typeof ResizeObserver === 'undefined') return
    const EXPAND_ABOVE = 540
    const check = () => {
      const vRect = view.getBoundingClientRect()
      const tRect = toolbar.getBoundingClientRect()
      const overflowing = tRect.left < vRect.left + 2
      const width = view.clientWidth
      setOverflowCompact(prev => {
        if (overflowing) return true
        if (!prev) return false
        if (width > EXPAND_ABOVE) return false
        return prev
      })
    }
    check()
    const ro = new ResizeObserver(check)
    ro.observe(view)
    ro.observe(toolbar)
    return () => ro.disconnect()
  }, [])

  const { lower, upper } = useMemo(() => computeTimeBounds(timeFilter), [timeFilter])
  const activeCustom = customFilters.filter(f => activeFilterIds.has(f.id))
  const liveCustom = useMemo(() => {
    const value = formValue.trim()
    return value ? [{ id: '__topo-live-search__', field: formField, value }] : []
  }, [formField, formValue])

  const titleMap = useMemo(() => {
    const map = new Map<string, string>()
    for (const c of cards) map.set(c.id, c.id)
    return map
  }, [cards])

  const fieldLabels = useMemo(() => {
    const labels = {} as Record<FilterField, string>
    for (const f of Object.keys(FIELD_LABEL_KEYS) as FilterField[]) {
      labels[f] = t(FIELD_LABEL_KEYS[f] as import('../../../i18n/types').I18nKey)
    }
    return labels
  }, [t])

  const childMap = useMemo(() => buildChildMap(cards), [cards])
  const parentMap = useMemo(() => buildParentMap(cards), [cards])

  const filteredCards = useMemo(() => {
    const base = filterCardsByTime(cards, lower, upper, childMap, parentMap)
    const filters = [...activeCustom, ...liveCustom]
    if (filters.length === 0) return base
    return applyCustomFilters(base, filters, childMap, parentMap)
  }, [cards, lower, upper, activeCustom, liveCustom, childMap, parentMap])

  const suggestions = useMemo(() => collectValues(cards, formField), [cards, formField])

  // Workflow-mode filter palette — binds directly to the shared
  // workflowFiltersStore (module-scoped, session memory; no localStorage).
  // Re-renders on every store mutation via useWorkflowFilters.
  const wfFilters = useWorkflowFilters()
  const wfFilterActiveCount = workflowFiltersStore.activeCount()

  const clearFilterInput = () => setFormValue('')

  function toggleFilter(id: string) {
    setActiveFilterIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  }

  function applyQuick(value: number, unit: AgoUnit) {
    setTimeFilter(tf => ({ ...tf, mode: 'ago', agoValue: String(value), agoUnit: unit, agoDirection: 'after' }))
  }

  function commitAdd() {
    const value = formValue.trim()
    if (!value) return
    const id = topoFiltersStore.add(formField, value)
    setActiveFilterIds(prev => new Set(prev).add(id))
    clearFilterInput()
  }

  function openEdit(id: string) {
    const f = customFilters.find(x => x.id === id)
    if (!f) return
    setFormField(f.field)
    setFormValue(f.value)
    setEditingId(id)
  }

  function commitEdit() {
    if (!editingId) return
    const value = formValue.trim()
    if (!value) return
    topoFiltersStore.update(editingId, formField, value)
    setEditingId(null)
  }

  function handleDelete(id: string) {
    topoFiltersStore.remove(id)
    setActiveFilterIds(prev => { const n = new Set(prev); n.delete(id); return n })
    setEditingId(null)
  }

  /** Toggle a single value in a workflow filter set (immutable copy). */
  function toggleSetValue(set: Set<string>, value: string): Set<string> {
    const next = new Set(set)
    if (next.has(value)) next.delete(value); else next.add(value)
    return next
  }
  const toggleStatus = (s: string) => workflowFiltersStore.setStatusFilter(toggleSetValue(wfFilters.statusFilter, s))
  const toggleCategory = (c: string) => workflowFiltersStore.setCategoryFilter(toggleSetValue(wfFilters.categoryFilter, c))
  const toggleOwnerKind = (k: string) => workflowFiltersStore.setOwnerKindFilter(toggleSetValue(wfFilters.ownerKindFilter, k))

  return (
    <div className="topology-mode-view" ref={viewRef}>
      <div className="topo-floating-toolbar" role="toolbar" aria-label="Topology mode switch" ref={toolbarRef}>
        {topoMode === 'workflow' ? (
        <div className="topo-toolbar-settings">
          <button
            type="button"
            className="topo-mode-btn"
            onClick={() => setLocateRequest(prev => ({ nonce: (prev?.nonce ?? 0) + 1 }))}
            title={t('workflowGraph.locate')}
            aria-label={t('workflowGraph.locate')}
          >
            <Crosshair size={16} />
          </button>
          <div className="topo-toolbar-separator" />
          <div className="topo-settings-wrapper" ref={wfSettingsRef}>
            <button type="button" className={`topo-mode-btn${wfSettingsOpen ? ' active' : ''}`} onClick={() => setWfSettingsOpen(open => !open)} title={t('workflowGraph.settings')} aria-label={t('workflowGraph.settings')} aria-expanded={wfSettingsOpen}>
              <Settings size={16} />
            </button>
            {wfSettingsOpen && (
              <div className="topo-settings-dropdown" role="dialog" aria-label={t('workflowGraph.settings')}>
                <div className="topo-settings-title">{t('workflowGraph.layoutToolbar')}</div>
                <div className="topo-edge-toggles" role="group" aria-label={t('workflowGraph.layoutToolbar')}>
                  <button type="button" className={`topo-edge-toggle${workflowDirection === 'LTR' ? ' active' : ''}`} onClick={() => handleWorkflowDirectionChange('LTR')} title={t('workflowGraph.layoutHorizontal')} aria-pressed={workflowDirection === 'LTR'}>
                    <MoveHorizontal size={14} /><span className="topo-edge-label">{t('workflowGraph.layoutHorizontalShort')}</span>
                  </button>
                  <button type="button" className={`topo-edge-toggle${workflowDirection === 'TTB' ? ' active' : ''}`} onClick={() => handleWorkflowDirectionChange('TTB')} title={t('workflowGraph.layoutVertical')} aria-pressed={workflowDirection === 'TTB'}>
                    <MoveVertical size={14} /><span className="topo-edge-label">{t('workflowGraph.layoutVerticalShort')}</span>
                  </button>
                </div>
              </div>
            )}
          </div>
          <div className="topo-toolbar-separator" />
          <div className="topo-settings-wrapper" ref={wfFilterRef}>
            <button
              type="button"
              className={`topo-mode-btn${wfFilterOpen ? ' active' : ''}`}
              onClick={() => setWfFilterOpen(open => !open)}
              title={t('workflowFilter.filters')}
              aria-label={t('workflowFilter.filters')}
              aria-expanded={wfFilterOpen}
            >
              <Filter size={16} />
              {wfFilterActiveCount > 0 && <span className="topo-filter-badge">{wfFilterActiveCount}</span>}
            </button>
            {wfFilterOpen && (
              <div className="workflow-filter-popover" role="dialog" aria-label={t('workflowFilter.filters')}>
                {/* Status multi-select */}
                <div className="workflow-filter-section" role="group" aria-label={t('workflowFilter.status')}>
                  <span className="workflow-filter-section-title">{t('workflowFilter.status')}</span>
                  <div className="workflow-filter-chips">
                    {WORKFLOW_STATUS_OPTIONS.map(s => (
                      <button
                        key={s}
                        type="button"
                        className={`topo-chip-mini${wfFilters.statusFilter.has(s) ? ' active' : ''}`}
                        onClick={() => toggleStatus(s)}
                        aria-pressed={wfFilters.statusFilter.has(s)}
                      >
                        {t(`workflowFilter.status.${s}`)}
                      </button>
                    ))}
                  </div>
                </div>

                {/* Category multi-select */}
                <div className="workflow-filter-section" role="group" aria-label={t('workflowFilter.category')}>
                  <span className="workflow-filter-section-title">{t('workflowFilter.category')}</span>
                  <div className="workflow-filter-chips">
                    {WORKFLOW_CATEGORY_OPTIONS.map(c => (
                      <button
                        key={c}
                        type="button"
                        className={`topo-chip-mini${wfFilters.categoryFilter.has(c) ? ' active' : ''}`}
                        onClick={() => toggleCategory(c)}
                        aria-pressed={wfFilters.categoryFilter.has(c)}
                      >
                        {t(`workflowFilter.category.${c}`)}
                      </button>
                    ))}
                  </div>
                </div>

                {/* Owner agent kind multi-select */}
                <div className="workflow-filter-section" role="group" aria-label={t('workflowFilter.owner')}>
                  <span className="workflow-filter-section-title">{t('workflowFilter.owner')}</span>
                  <div className="workflow-filter-chips">
                    {WORKFLOW_OWNER_KIND_OPTIONS.map(k => (
                      <button
                        key={k}
                        type="button"
                        className={`topo-chip-mini${wfFilters.ownerKindFilter.has(k) ? ' active' : ''}`}
                        onClick={() => toggleOwnerKind(k)}
                        aria-pressed={wfFilters.ownerKindFilter.has(k)}
                      >
                        {k === NONE_OWNER_KIND ? t('workflowFilter.ownerKind.none') : t(`workflowFilter.ownerKind.${k}` as import('../../../i18n/types').I18nKey)}
                      </button>
                    ))}
                  </div>
                </div>

                {/* Text search (substring or /regexp/, live-apply) */}
                <div className="workflow-filter-section">
                  <span className="workflow-filter-section-title">{t('workflowFilter.text')}</span>
                  <input
                    type="text"
                    value={wfFilters.textFilter}
                    onChange={e => workflowFiltersStore.setTextFilter(e.target.value)}
                    className="topo-popover-input"
                    placeholder="text, or /regexp/"
                    aria-label={t('workflowFilter.text')}
                  />
                </div>

                {/* Time filter — reuses the topology control pattern */}
                <div className="workflow-filter-section">
                  <span className="workflow-filter-section-title">{t('workflowFilter.lastActivity')}</span>
                  <div className="topo-time-presets">
                    <button
                      type="button"
                      className={`topo-chip-mini${wfFilters.timeFilter.mode === 'all' ? ' active' : ''}`}
                      onClick={() => workflowFiltersStore.setTimeFilter({ ...wfFilters.timeFilter, mode: 'all' })}
                    >
                      All
                    </button>
                    {QUICK_PRESETS.map(p => (
                      <button
                        key={p.label}
                        type="button"
                        className={`topo-chip-mini${wfFilters.timeFilter.mode === 'ago' && String(p.value) === wfFilters.timeFilter.agoValue && wfFilters.timeFilter.agoUnit === p.unit ? ' active' : ''}`}
                        onClick={() => workflowFiltersStore.setTimeFilter({ ...wfFilters.timeFilter, mode: 'ago', agoValue: String(p.value), agoUnit: p.unit, agoDirection: 'after' })}
                      >
                        {p.label}
                      </button>
                    ))}
                  </div>
                  <div className="topo-popover-section-head">
                    <span className="topo-popover-section-title">{wfFilters.timeFilter.agoDirection === 'before' ? 'Older than' : 'Within last'}</span>
                    <div className="topo-ago-switch" role="group" aria-label="Ago direction">
                      <button
                        type="button"
                        className={`topo-ago-switch-btn${wfFilters.timeFilter.agoDirection === 'after' ? ' active' : ''}`}
                        onClick={() => workflowFiltersStore.setTimeFilter({ ...wfFilters.timeFilter, mode: 'ago', agoDirection: 'after' })}
                        title="Modified within the last N (after the cutoff)"
                      >
                        Within
                      </button>
                      <button
                        type="button"
                        className={`topo-ago-switch-btn${wfFilters.timeFilter.agoDirection === 'before' ? ' active' : ''}`}
                        onClick={() => workflowFiltersStore.setTimeFilter({ ...wfFilters.timeFilter, mode: 'ago', agoDirection: 'before' })}
                        title="Modified before N ago (older than the cutoff)"
                      >
                        Before
                      </button>
                    </div>
                  </div>
                  <div className="topo-ago-row">
                    <input
                      type="number"
                      min={1}
                      value={wfFilters.timeFilter.agoValue}
                      onChange={e => workflowFiltersStore.setTimeFilter({ ...wfFilters.timeFilter, mode: 'ago', agoValue: e.target.value })}
                      className="topo-popover-input"
                    />
                    <select
                      value={wfFilters.timeFilter.agoUnit}
                      onChange={e => workflowFiltersStore.setTimeFilter({ ...wfFilters.timeFilter, mode: 'ago', agoUnit: e.target.value as AgoUnit })}
                      className="topo-popover-input"
                    >
                      <option value="hours">hours</option>
                      <option value="days">days</option>
                    </select>
                    <span className="topo-ago-suffix">ago</span>
                  </div>
                  <div className="topo-popover-section-head">
                    <span className="topo-popover-section-title">Date range</span>
                  </div>
                  <div className="topo-range-row">
                    <input
                      type="date"
                      value={wfFilters.timeFilter.rangeFrom}
                      onChange={e => workflowFiltersStore.setTimeFilter({ ...wfFilters.timeFilter, mode: 'range', rangeFrom: e.target.value })}
                      className="topo-popover-input"
                      aria-label="From date"
                    />
                    <span className="topo-range-sep">–</span>
                    <input
                      type="date"
                      value={wfFilters.timeFilter.rangeTo}
                      onChange={e => workflowFiltersStore.setTimeFilter({ ...wfFilters.timeFilter, mode: 'range', rangeTo: e.target.value })}
                      className="topo-popover-input"
                      aria-label="To date"
                    />
                  </div>
                </div>

                {/* Clear all — only shown while at least one dimension is active */}
                {wfFilterActiveCount > 0 && (
                  <div className="workflow-filter-actions">
                    <button
                      type="button"
                      className="topo-popover-btn"
                      onClick={() => workflowFiltersStore.clearAll()}
                    >
                      <Trash2 size={13} /> {t('workflowFilter.clearAll')}
                    </button>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
        ) : (
        <>
        <div className="topo-mode-selection">
        <button
          type="button"
          className={`topo-mode-btn${topoMode === 'cards' ? ' active' : ''}`}
          onClick={() => setTopoMode('cards')}
          title="Cards Topology"
          aria-pressed={topoMode === 'cards'}
        >
          <Network size={16} />
        </button>
            <button
              type="button"
              className={`topo-mode-btn${topoMode === 'actors' ? ' active' : ''}`}
              onClick={() => setTopoMode('actors')}
              title="Actor DAG"
              aria-pressed={topoMode === 'actors'}
            >
              <GitBranch size={16} />
            </button>
            <button
              type="button"
              className={`topo-mode-btn${topoMode === 'brain' ? ' active' : ''}`}
              onClick={() => setTopoMode('brain')}
              title="Agent Brain"
              aria-pressed={topoMode === 'brain'}
            >
              <Brain size={16} />
            </button>
        </div>
        <div className="topo-toolbar-settings">
        {topoMode === 'cards' && <div className="topo-toolbar-separator" />}
        {topoMode === 'cards' && (
          <div className="topo-settings-wrapper" ref={edgeSettingsRef}>
            <button
              type="button"
              className={`topo-mode-btn${edgeSettingsOpen ? ' active' : ''}`}
              onClick={() => setEdgeSettingsOpen(open => !open)}
              title="Topology settings"
              aria-label="Topology settings"
              aria-expanded={edgeSettingsOpen}
            >
              <Settings size={16} />
            </button>
            {edgeSettingsOpen && (
              <div className="topo-settings-dropdown" role="dialog" aria-label="Topology settings">
                <div className="topo-settings-title">Node heatmap</div>
                <div className="topo-edge-toggles" role="group" aria-label="Node heatmap">
                  {([['default', 'Default'], ['created', 'Created'], ['modified', 'Modified'], ['connections', 'Connections'], ['size', 'Size']] as const).map(([mode, label]) => (
                    <button type="button" key={mode} className={`topo-edge-toggle${nodeGraphMode === mode ? ' active' : ''}`} onClick={() => setNodeGraphMode(mode)} aria-pressed={nodeGraphMode === mode}>{label}</button>
                  ))}
                </div>
                <div className="topo-settings-title">Edge visibility</div>
                <div className="topo-edge-toggles" role="group" aria-label="Edge visibility">
                  <button type="button" className={`topo-edge-toggle${edgeVisibility.parent ? ' active' : ''}`} onClick={() => toggleEdge('parent')} title="Parent edges (solid)" aria-pressed={edgeVisibility.parent}>
                    <span className="topo-edge-glyph topo-edge-glyph--parent" /><span className="topo-edge-label">Parent</span>
                  </button>
                  <button type="button" className={`topo-edge-toggle${edgeVisibility.tag ? ' active' : ''}`} onClick={() => toggleEdge('tag')} title="Tag edges (dashed)" aria-pressed={edgeVisibility.tag}>
                    <span className="topo-edge-glyph topo-edge-glyph--tag" /><span className="topo-edge-label">Tag</span>
                  </button>
                  <button type="button" className={`topo-edge-toggle${edgeVisibility.type ? ' active' : ''}`} onClick={() => toggleEdge('type')} title="Type edges (dotted/system)" aria-pressed={edgeVisibility.type}>
                    <span className="topo-edge-glyph topo-edge-glyph--type" /><span className="topo-edge-label">Type</span>
                  </button>
                  <button type="button" className={`topo-edge-toggle${edgeVisibility.wikiword ? ' active' : ''}`} onClick={() => toggleEdge('wikiword')} title="Wikiword weak links (hidden by default)" aria-pressed={edgeVisibility.wikiword}>
                    <span className="topo-edge-glyph topo-edge-glyph--wikiword" /><span className="topo-edge-label">Wiki</span>
                  </button>
                  <button type="button" className={`topo-edge-toggle${edgeVisibility.depends_on ? ' active' : ''}`} onClick={() => toggleEdge('depends_on')} title="Task dependency edges (orange, dashed)" aria-pressed={edgeVisibility.depends_on}>
                    <span className="topo-edge-glyph topo-edge-glyph--depends_on" /><span className="topo-edge-label">Depends</span>
                  </button>
                  <button type="button" className={`topo-edge-toggle${edgeVisibility.agent_binding ? ' active' : ''}`} onClick={() => toggleEdge('agent_binding')} title="Agent binding edges (cyan, short)" aria-pressed={edgeVisibility.agent_binding}>
                    <span className="topo-edge-glyph topo-edge-glyph--agent_binding" /><span className="topo-edge-label">Binding</span>
                  </button>
                </div>
              </div>
            )}
          </div>
        )}

        {topoMode === 'cards' && (<>
          {/* Precise time filter */}
          <div className="topo-time-filter-wrapper" ref={timeRef}>
            <button
              type="button"
              className={`topo-mode-btn topo-time-filter-btn${timeOpen ? ' active' : ''}`}
              onClick={() => setTimeOpen(o => !o)}
              title={`Time filter: ${timeSummary(timeFilter)}`}
              aria-label="Time filter"
            >
              <Calendar size={14} />
              <span className="topo-time-filter-label">{timeSummary(timeFilter)}</span>
            </button>
            {timeOpen && (
              <div className="topo-time-popover" role="dialog" aria-label="Time filter">
                <div className="topo-time-presets">
                  <button type="button" className={`topo-chip-mini${timeFilter.mode === 'all' ? ' active' : ''}`} onClick={() => setTimeFilter(tf => ({ ...tf, mode: 'all' }))}>All</button>
                  {QUICK_PRESETS.map(p => (
                    <button
                      key={p.label}
                      type="button"
                      className={`topo-chip-mini${timeFilter.mode === 'ago' && String(p.value) === timeFilter.agoValue && timeFilter.agoUnit === p.unit ? ' active' : ''}`}
                      onClick={() => applyQuick(p.value, p.unit)}
                    >
                      {p.label}
                    </button>
                  ))}
                </div>

                <div className="topo-popover-section">
                  <div className="topo-popover-section-head">
                    <span className="topo-popover-section-title">{timeFilter.agoDirection === 'before' ? 'Older than' : 'Within last'}</span>
                    <div className="topo-ago-switch" role="group" aria-label="Ago direction">
                      <button
                        type="button"
                        className={`topo-ago-switch-btn${timeFilter.agoDirection === 'after' ? ' active' : ''}`}
                        onClick={() => setTimeFilter(tf => ({ ...tf, mode: 'ago', agoDirection: 'after' }))}
                        title="Modified within the last N (after the cutoff)"
                      >
                        Within
                      </button>
                      <button
                        type="button"
                        className={`topo-ago-switch-btn${timeFilter.agoDirection === 'before' ? ' active' : ''}`}
                        onClick={() => setTimeFilter(tf => ({ ...tf, mode: 'ago', agoDirection: 'before' }))}
                        title="Modified before N ago (older than the cutoff)"
                      >
                        Before
                      </button>
                    </div>
                  </div>
                  <div className="topo-ago-row">
                    <input
                      type="number"
                      min={1}
                      value={timeFilter.agoValue}
                      onChange={e => setTimeFilter(tf => ({ ...tf, mode: 'ago', agoValue: e.target.value }))}
                      className="topo-popover-input"
                    />
                    <select
                      value={timeFilter.agoUnit}
                      onChange={e => setTimeFilter(tf => ({ ...tf, mode: 'ago', agoUnit: e.target.value as AgoUnit }))}
                      className="topo-popover-input"
                    >
                      <option value="hours">hours</option>
                      <option value="days">days</option>
                    </select>
                    <span className="topo-ago-suffix">ago</span>
                  </div>
                </div>

                <div className="topo-popover-section">
                  <span className="topo-popover-section-title">Date range</span>
                  <div className="topo-range-row">
                    <input
                      type="date"
                      value={timeFilter.rangeFrom}
                      onChange={e => setTimeFilter(tf => ({ ...tf, mode: 'range', rangeFrom: e.target.value }))}
                      className="topo-popover-input"
                      aria-label="From date"
                    />
                    <span className="topo-range-sep">–</span>
                    <input
                      type="date"
                      value={timeFilter.rangeTo}
                      onChange={e => setTimeFilter(tf => ({ ...tf, mode: 'range', rangeTo: e.target.value }))}
                      className="topo-popover-input"
                      aria-label="To date"
                    />
                  </div>
                </div>
              </div>
            )}
          </div>

          {/* Custom property-filter chips (left of the add button) */}
          {customFilters.map((filter, index) => {
            const isActive = activeFilterIds.has(filter.id)
            const isEditing = editingId === filter.id
            const label = filterLabel(filter.field, filter.value, titleMap, fieldLabels)
            const canChangeOperator = index > 0
            return (
              <div className="topo-custom-filter-wrapper" key={filter.id} ref={isEditing ? editRef : undefined}>
                <button
                  type="button"
                  className={`topo-custom-chip${isActive ? ' active' : ''}`}
                  onClick={() => toggleFilter(filter.id)}
                  title={label}
                  aria-pressed={isActive}
                >
                  <span className="topo-custom-chip-label">{label}</span>
                </button>
                {canChangeOperator && (
                  <button
                    type="button"
                    className="topo-custom-chip-operator"
                    onClick={(e) => { e.stopPropagation(); const next = filter.operator === 'and' ? 'or' : filter.operator === 'or' ? 'xor' : 'and'; topoFiltersStore.setOperator(filter.id, next) }}
                    title={`Combine with previous: ${OPERATOR_LABELS[filter.operator ?? 'and']}`}
                    aria-label={`Operator ${OPERATOR_LABELS[filter.operator ?? 'and']}`}
                  >
                    {OPERATOR_LABELS[filter.operator ?? 'and']}
                  </button>
                )}
                <button
                  type="button"
                  className="topo-custom-chip-edit"
                  onClick={(e) => { e.stopPropagation(); isEditing ? setEditingId(null) : openEdit(filter.id) }}
                  title="Edit / delete filter"
                  aria-label={`Edit ${label}`}
                >
                  <Pencil size={12} />
                </button>
                {isEditing && (
                  <div className="topo-filter-popover" role="dialog" aria-label="Edit custom filter">
                    {index > 0 && (
                      <label className="topo-popover-row">
                        <span>Operator</span>
                        <select value={filter.operator ?? 'and'} onChange={e => topoFiltersStore.setOperator(filter.id, e.target.value as import('./topoFiltersStore').FilterOperator)}>
                          <option value="and">AND</option>
                          <option value="or">OR</option>
                          <option value="xor">XOR</option>
                        </select>
                      </label>
                    )}
                    <label className="topo-popover-row">
                      <span>Field</span>
                      <select
                        value={isStructuralField(filter.field) ? filter.field : formField}
                        onChange={e => setFormField(e.target.value as FilterField)}
                        disabled={isStructuralField(filter.field)}
                      >
                        {(isStructuralField(filter.field) ? [...FIELD_OPTIONS, filter.field] : FIELD_OPTIONS).map(f => <option key={f} value={f}>{fieldLabels[f]}</option>)}
                      </select>
                    </label>
                    <label className="topo-popover-row">
                      <span>Value</span>
                      <input
                        value={isStructuralField(filter.field) ? (titleMap.get(filter.value) ?? filter.value) : formValue}
                        onChange={e => setFormValue(e.target.value)}
                        disabled={isStructuralField(filter.field)}
                        list="topo-filter-suggestions"
                        placeholder={formField === 'title' ? 'text, or /regexp/' : 'exact value'}
                        autoFocus
                      />
                    </label>
                    <div className="topo-popover-actions">
                      <button type="button" className="topo-popover-btn danger" onClick={() => handleDelete(filter.id)}>
                        <Trash2 size={13} /> Delete
                      </button>
                      {!isStructuralField(filter.field) && (
                        <button type="button" className="topo-popover-btn primary" onClick={commitEdit}>Save</button>
                      )}
                    </div>
                  </div>
                )}
              </div>
            )
          })}

          {/* Add custom property filter — inline bar on desktop, "+" popover when compact (mobile / narrow width) */}
          {showCompact ? (
            <div className="topo-custom-add-wrapper" ref={addRef}>
              <button
                type="button"
                className={`topo-mode-btn${addOpen ? ' active' : ''}`}
                onClick={() => setAddOpen(o => !o)}
                title="Add custom filter"
                aria-label="Add custom filter"
                aria-expanded={addOpen}
              >
                <Plus size={16} />
              </button>
              {addOpen && (
                <div className="topo-filter-popover" role="dialog" aria-label="Add custom filter">
                  <label className="topo-popover-row">
                    <span>Field</span>
                    <select
                      value={formField}
                      onChange={e => setFormField(e.target.value as FilterField)}
                      aria-label="Filter type"
                    >
                      {FIELD_OPTIONS.map(f => <option key={f} value={f}>{fieldLabels[f]}</option>)}
                    </select>
                  </label>
                  <label className="topo-popover-row">
                    <span>Value</span>
                    <input
                      value={formValue}
                      onChange={e => setFormValue(e.target.value)}
                      onKeyDown={e => { if (e.key === 'Enter') commitAdd() }}
                      list="topo-filter-suggestions"
                      placeholder={formField === 'title' ? 'text, or /regexp/' : 'exact value'}
                      aria-label="Filter value"
                      autoFocus
                    />
                  </label>
                  <div className="topo-popover-actions">
                    <button
                      type="button"
                      className="topo-popover-btn primary"
                      onClick={commitAdd}
                      disabled={!formValue.trim()}
                    >
                      <Plus size={13} /> Add
                    </button>
                  </div>
                </div>
              )}
            </div>
          ) : (
          <div className="topo-custom-search" role="search" aria-label="Add custom filter">
            <select
              className="topo-custom-search-field"
              value={formField}
              onChange={e => setFormField(e.target.value as FilterField)}
              aria-label="Filter type"
            >
              {FIELD_OPTIONS.map(f => <option key={f} value={f}>{fieldLabels[f]}</option>)}
            </select>
            <input
              className="topo-custom-search-input"
              value={formValue}
              onChange={e => setFormValue(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') commitAdd() }}
              list="topo-filter-suggestions"
              placeholder={formField === 'title' ? 'Search title or /regexp/' : `Search ${fieldLabels[formField].toLowerCase()}`}
              aria-label="Filter value"
            />
            <button
              type="button"
              className={`topo-custom-search-add${formValue.trim() ? '' : ' disabled'}`}
              onClick={commitAdd}
              disabled={!formValue.trim()}
              title={formValue.trim() ? 'Add custom filter' : 'Type a value to add a filter'}
              aria-label={formValue.trim() ? 'Add custom filter' : 'Search'}
            >
              {formValue.trim() ? <Plus size={15} /> : <Search size={15} />}
            </button>
          </div>
          )}

          <datalist id="topo-filter-suggestions">
            {suggestions.map(v => <option key={v} value={v} />)}
          </datalist>
        </>)}
        </div>
        </>
        )}
      </div>

      {topoLoading && (
        <div className="topo-loading" role="status" aria-live="polite">
          {monoError ? (
            <span className="topo-loading-text">{t('topology.loadError')}</span>
          ) : (
            <>
              <Loader2 size={16} className="topo-loading-spinner" aria-hidden="true" />
              <span className="topo-loading-text">{t('topology.loading')}</span>
            </>
          )}
        </div>
      )}

      {topoMode === 'cards' ? (
        <TopologyGraph
          cards={filteredCards}
          agentInfoSnapshot={agentInfoSnapshot}
          activeAgentId={activeAgentId}
          onNodeClick={onNodeClick}
          onConnectNodes={onConnectNodes}
          onContextMenu={onContextMenu}
          pendingPlacement={pendingPlacement}
          onPlacementDone={onPlacementDone}
          edgeVisibility={edgeVisibility}
          nodeGraphMode={nodeGraphMode}
          depEdges={depEdges}
          brainMountAgentId={brainMountAgentId}
          brainSnapshot={brainSnapshot}
          onMemoryNodeClick={onMemoryNodeClick}
        />
      ) : topoMode === 'actors' ? (
        <ActorTopology
          agentInfoSnapshot={agentInfoSnapshot}
          onNodeClick={onActorNodeClick}
          onNodeContext={onActorNodeContext}
        />
      ) : topoMode === 'workflow' ? (
        <WorkflowGraph
          cards={cardsWithGraphDeps}
          agentInfoSnapshot={agentInfoSnapshot}
          onNodeClick={onNodeClick}
          onNodeMenu={onWorkflowCardMenu}
          onBlankMenu={onWorkflowBlankMenu}
          direction={workflowDirection}
          expandedWorkflows={expandedWorkflows}
          onToggleFold={handleToggleFold}
          expandedBuckets={expandedBuckets}
          onToggleBucketFold={handleToggleBucketFold}
          foldedCategories={foldedCategories}
          onToggleCategoryFold={handleToggleCategoryFold}
          onCategoryMenu={handleCategoryMenu}
          onLocateWorkflow={ensureWorkflowVisible}
          onStartWorkflow={onStartWorkflow}
          onEditSchedule={onEditSchedule}
          locateRequest={locateRequest}
          pendingDeleteNodeIds={pendingDeleteNodeIds}
        />
      ) : (
        <BrainMemoryGraph activeAgentId={activeAgentId} agentInfoSnapshot={agentInfoSnapshot} onMemoryNodeClick={onMemoryNodeClick} />
      )}
      <WorkflowCategoryContextMenu
        target={categoryMenu}
        onClose={() => setCategoryMenu(null)}
        onExpandAll={handleExpandAllInCategory}
        onCollapseAll={handleCollapseAllInCategory}
      />
    </div>
  )
}
