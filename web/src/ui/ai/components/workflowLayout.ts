import type { MonoCardListItem } from '../../../domain/mono-types'

export type WorkflowDirection = 'LTR' | 'TTB'

export interface WorkflowNodeLayout {
  cardId: string
  kind: 'root' | 'task' | 'reality' | 'sentinel' | 'epic' | 'category' | 'bucket' | 'scheduler' | 'ghost'
  sentinelRole?: 'start'
  /** Category id for 'category' nodes (e.g. 'in-progress'); display label source.
   *  For 'scheduler' nodes: the scheduler card's title (falls back to card id). */
  label?: string
  /** Item count for 'bucket' nodes (kept accurate even when collapsed).
   *  For 'scheduler' nodes: the number of instance workflows under this scheduler. */
  count?: number
  /** For 'ghost' nodes: the workflow-template map id a future instance will
   *  materialize from (the virtual preview under an expanded zero-instance
   *  scheduler). */
  templateId?: string
  x: number
  y: number
  width: number
  height: number
}

export interface WorkflowEdgeLayout {
  from: string
  to: string
  kind: 'flow' | 'depends_on' | 'start' | 'tree'
}

export interface WorkflowLayout {
  nodes: WorkflowNodeLayout[]
  edges: WorkflowEdgeLayout[]
  width: number
  height: number
  direction: WorkflowDirection
}

export interface WorkflowGroupOffset {
  x: number
  y: number
}

export interface WorkflowLayoutOptions {
  /** Flow direction of the DAG. Defaults to 'LTR'. */
  direction?: WorkflowDirection
  /** Persisted canvas offsets keyed by workflow root card id. */
  groupOffsets?: Readonly<Record<string, WorkflowGroupOffset>>
}

export function taskDependsOn(card: MonoCardListItem): string[] {
  const value = (card.data as Record<string, unknown> | undefined)?.depends_on
  if (Array.isArray(value)) return value.map(String).filter(Boolean)
  if (typeof value !== 'string') return []
  return value.replace(/^\[|\]$/g, '').split(',').map(v => v.trim()).filter(Boolean)
}

export const WORKFLOW_CARD_W = 220
export const WORKFLOW_ROOT_H = 72
export const WORKFLOW_TASK_H = 64
export const WORKFLOW_SENTINEL_H = 48

const COL_GAP = 90
const ROW_GAP = 18
/** Vertical gap between depth layers in TTB (vertical) layout — wider than the
 *  LTR lane gap so stacked cards breathe vertically. */
const TTB_DEPTH_GAP = 48
/** Gap between consecutive workflow maps along the stacking axis = exactly one
 *  grid cell (one card + its gap), so each map keeps a uniform one-cell margin
 *  regardless of its footprint or direction. */
const GROUP_GAP_LTR = WORKFLOW_TASK_H + ROW_GAP + ROW_GAP
const GROUP_GAP_TTB = WORKFLOW_TASK_H + TTB_DEPTH_GAP + TTB_DEPTH_GAP
const PAD = 24

// ── Parent index (hot-path accelerator) ────────────────────────────────

/**
 * Per-cards-array derived data for workflow hot paths, built once per distinct
 * cards array (O(C)) and cached by array reference. Eliminates the old
 * O(workflows × cards) behavior where workflowTaskIds / workflowLastActivity
 * rescanned the whole cards array for every workflow map.
 */
export interface WorkflowCardIndex {
  /** card id → card, for every card in the input array (later entries win). */
  byId: ReadonlyMap<string, MonoCardListItem>
  /** workflow map id → task card ids parented to it, in cards-array order. */
  taskIdsByParent: ReadonlyMap<string, readonly string[]>
  /** task card id → owning workflow map id (card.parent). */
  owningMapId: ReadonlyMap<string, string>
}

/** Full cards scans performed to (re)build a WorkflowCardIndex. Exported as a
 *  regression canary: laying out W workflows must add at most 1 (the initial
 *  index build), not W. */
let parentIndexBuildCountValue = 0
export function workflowParentIndexBuildCount(): number {
  return parentIndexBuildCountValue
}

/** Build the per-cards index from scratch: one O(C) pass over the array. */
export function buildWorkflowParentIndex(cards: MonoCardListItem[]): WorkflowCardIndex {
  parentIndexBuildCountValue++
  const byId = new Map<string, MonoCardListItem>()
  const taskIdsByParent = new Map<string, string[]>()
  const owningMapId = new Map<string, string>()
  for (const c of cards) {
    byId.set(c.id, c)
    if (c.type === 'task' && c.parent) {
      owningMapId.set(c.id, c.parent)
      let ids = taskIdsByParent.get(c.parent)
      if (!ids) {
        ids = []
        taskIdsByParent.set(c.parent, ids)
      }
      ids.push(c.id)
    }
  }
  return { byId, taskIdsByParent, owningMapId }
}

/** Cached-per-cards-array index: rebuilt only when a new array reference is
 *  seen (store updates replace the array), never for identical arrays. */
const parentIndexCache = new WeakMap<MonoCardListItem[], WorkflowCardIndex>()
function getWorkflowCardIndex(cards: MonoCardListItem[]): WorkflowCardIndex {
  let index = parentIndexCache.get(cards)
  if (!index) {
    index = buildWorkflowParentIndex(cards)
    parentIndexCache.set(cards, index)
  }
  return index
}

/** Task ids a root map card scopes over: data.scope.include (primary) or
 *  data.include (fallback), plus any task card whose parent is the map.
 *  Parent-linked tasks resolve through the shared per-cards index instead of
 *  rescanning all cards per call. The optional `index` skips the lazy
 *  WeakMap lookup on hot paths; when omitted, the index is built/cached on
 *  first use, keyed by the cards array reference. */
export function workflowTaskIds(
  mapCard: MonoCardListItem,
  cards: MonoCardListItem[],
  index?: WorkflowCardIndex,
): string[] {
  const ids: string[] = []
  const seen = new Set<string>()
  const data = mapCard.data as Record<string, unknown> | undefined
  const scope = data?.scope as Record<string, unknown> | undefined
  const push = (v: unknown) => {
    if (Array.isArray(v)) {
      for (const item of v) {
        const id = String(item)
        if (id && !seen.has(id)) {
          seen.add(id)
          ids.push(id)
        }
      }
    }
  }
  push(scope?.include)
  push(data?.include)
  // Parented task ids come from the index in cards-array order — identical to
  // the old `for (const c of cards) if (c.type === 'task' && c.parent === mapCard.id)`
  // scan, but O(parented) instead of O(cards) per map.
  const parented = (index ?? getWorkflowCardIndex(cards)).taskIdsByParent.get(mapCard.id)
  if (parented) {
    for (const id of parented) {
      if (!seen.has(id)) {
        seen.add(id)
        ids.push(id)
      }
    }
  }
  return ids
}

/** Owner orchestrator agent actor id bound to a root map card. */
export function workflowOwnerAgentId(mapCard: MonoCardListItem): string | undefined {
  const v = (mapCard.data as Record<string, unknown> | undefined)?.ownerAgentId
  return typeof v === 'string' && v ? v : undefined
}

/** Destination text stored on a workflow map card. */
export function workflowMapDestination(mapCard: MonoCardListItem): string {
  const v = (mapCard.data as Record<string, unknown> | undefined)?.Destination
  return typeof v === 'string' && v ? v : mapCard.id
}

/** Latest modified timestamp across a workflow map and its task cards — the
 *  workflow's "last activity" shown on the folded start node. Resolves tasks
 *  through the shared per-cards index (optional `index`, lazily cached
 *  otherwise) so loop-internal calls neither rescan all cards nor rebuild a
 *  card map per workflow. */
export function workflowLastActivity(
  mapCard: MonoCardListItem,
  cards: MonoCardListItem[],
  index?: WorkflowCardIndex,
): string | undefined {
  const idx = index ?? getWorkflowCardIndex(cards)
  let latest = mapCard.modified || ''
  for (const taskId of workflowTaskIds(mapCard, cards, idx)) {
    const modified = idx.byId.get(taskId)?.modified
    if (modified && modified > latest) latest = modified
  }
  return latest || undefined
}

// ── Epic tree categories ──────────────────────────────────────────────

/** The six epic-tree categories, in canonical display order. */
export type EpicCategory = 'in-progress' | 'to-start' | 'template' | 'planned' | 'completed' | 'archived'

export const EPIC_CATEGORY_ORDER: EpicCategory[] = ['in-progress', 'to-start', 'template', 'planned', 'completed', 'archived']

/** A completed workflow whose last activity is older than this is archived. */
export const ARCHIVE_AFTER_MS = 7 * 24 * 60 * 60 * 1000

/** Age boundaries for archived time buckets: <30d per-day, <365d per-month,
 *  otherwise per-year. */
const ARCHIVE_DATE_BUCKET_DAYS = 30
const ARCHIVE_MONTH_BUCKET_DAYS = 365

/** Virtual node id prefix for archived time-bucket nodes. */
export const EPIC_BUCKET_PREFIX = '__epic_bucket__:'

export type EpicBucketKind = 'date' | 'month' | 'year' | 'scheduler'

/** A virtual time bucket under the archived category. Pure layout element —
 *  only its fold state is persisted; no real card is ever created. */
export interface EpicBucket {
  id: string
  kind: EpicBucketKind
  label: string
  /** Workflows in this bucket, sorted by last activity descending. */
  workflows: MonoCardListItem[]
}

/** True when a completed workflow's last activity is older than the archive
 *  threshold. Workflows without a parseable timestamp are never archived. */
export function isArchivedWorkflow(
  mapCard: MonoCardListItem,
  cards: MonoCardListItem[],
  now: number = Date.now(),
  index?: WorkflowCardIndex,
): boolean {
  const last = workflowLastActivity(mapCard, cards, index)
  const ts = last ? Date.parse(last) : NaN
  return Number.isFinite(ts) && now - ts > ARCHIVE_AFTER_MS
}

/**
 * Group archived workflows (already sorted by last activity descending) into
 * time buckets: per-day for the last 30 days, per-month up to a year, per-year
 * beyond that. Buckets are created on demand from the data — a workflow older
 * than any existing bucket range simply gets its own bucket. Bucket order:
 * date buckets (newest first), then month buckets, then year buckets.
 */
export function buildArchivedBuckets(
  workflows: MonoCardListItem[],
  cards: MonoCardListItem[],
  now: number,
  index?: WorkflowCardIndex,
): EpicBucket[] {
  const buckets: EpicBucket[] = []
  const byId = new Map<string, EpicBucket>()
  for (const wf of workflows) {
    const last = workflowLastActivity(wf, cards, index)
    const ts = last ? Date.parse(last) : NaN
    if (!Number.isFinite(ts)) continue
    const ageDays = (now - ts) / 86_400_000
    // Bucket boundaries follow the display layer: local calendar day/month/year.
    const d = new Date(ts)
    const y = d.getFullYear()
    const m = String(d.getMonth() + 1).padStart(2, '0')
    const day = String(d.getDate()).padStart(2, '0')
    let kind: EpicBucketKind
    let key: string
    if (ageDays < ARCHIVE_DATE_BUCKET_DAYS) {
      kind = 'date'
      key = `${y}-${m}-${day}`
    } else if (ageDays < ARCHIVE_MONTH_BUCKET_DAYS) {
      kind = 'month'
      key = `${y}-${m}`
    } else {
      kind = 'year'
      key = `${y}`
    }
    const id = `${EPIC_BUCKET_PREFIX}${kind}:${key}`
    let bucket = byId.get(id)
    if (!bucket) {
      bucket = { id, kind, label: key, workflows: [] }
      byId.set(id, bucket)
      buckets.push(bucket)
    }
    bucket.workflows.push(wf)
  }
  const kindRank: Record<EpicBucketKind, number> = { date: 0, month: 1, year: 2, scheduler: 3 }
  // Zero-padded keys sort lexicographically == chronologically; newest first.
  buckets.sort((a, b) => kindRank[a.kind] - kindRank[b.kind] || (a.id < b.id ? 1 : a.id > b.id ? -1 : 0))
  return buckets
}

/** A virtual scheduler grouping node under the planned category. */
export interface EpicSchedulerGroup {
  id: string
  schedulerCardId: string
  label: string
  /** Instance workflows belonging to this scheduler. */
  workflows: MonoCardListItem[]
}

/**
 * Group planned instance workflows by their owning scheduler card. Only
 * workflows whose `data.scheduler_card_id` matches an existing scheduler card
 * in the input are grouped; others remain unaffiliated and render directly
 * under the planned category band.
 */
export function buildPlannedSchedulerGroups(
  workflows: MonoCardListItem[],
  schedulerCards: MonoCardListItem[],
): EpicSchedulerGroup[] {
  const schedulerById = new Map(schedulerCards.map(c => [c.id, c]))
  const groups = new Map<string, EpicSchedulerGroup>()
  for (const wf of workflows) {
    const sid = schedulerOfInstance(wf)
    if (!sid || !schedulerById.has(sid)) continue
    let group = groups.get(sid)
    if (!group) {
      group = {
        id: sid,
        schedulerCardId: sid,
        label: sid,
        workflows: [],
      }
      groups.set(sid, group)
    }
    group.workflows.push(wf)
  }
  return [...groups.values()]
}

/** Virtual node id for the single epic-tree root. */
export const EPIC_ROOT_ID = '__epic_root__'
/** Virtual node id prefix for category nodes. */
export const EPIC_CATEGORY_PREFIX = '__epic_cat__:'

export function epicCategoryId(cat: EpicCategory): string {
  return `${EPIC_CATEGORY_PREFIX}${cat}`
}

/** Resolve the workflow map a card belongs to: the map itself, its start
 *  sentinel, a task in its scope, or a task parented to it. Used by the
 *  locate (zoom-to-fit) feature to find the workflow to frame. */
export function locateMapIdForCard(
  cardId: string | null,
  cards: MonoCardListItem[],
): string | undefined {
  if (!cardId) return undefined
  const maps = cards.filter(c => c.type === 'workflow')
  if (maps.some(m => m.id === cardId)) return cardId
  if (cardId.endsWith(':start')) {
    const mapId = cardId.slice(0, -':start'.length)
    if (maps.some(m => m.id === mapId)) return mapId
  }
  for (const m of maps) {
    if (workflowTaskIds(m, cards).includes(cardId)) return m.id
  }
  const card = cards.find(c => c.id === cardId)
  if (card?.parent && maps.some(m => m.id === card.parent)) return card.parent
  return undefined
}

/**
 * Return the scheduler card id that owns a planned workflow instance, if any.
 * Instance workflows carry `data.scheduler_card_id` pointing back to their
 * scheduler card; scheduler-bound template maps do not.
 */
export function schedulerOfInstance(instance: MonoCardListItem): string | undefined {
  const v = (instance.data as Record<string, unknown> | undefined)?.scheduler_card_id
  return typeof v === 'string' && v ? v : undefined
}

/**
 * Classify a workflow map card into one epic-tree category. Pure, unit-testable.
 *
 * Unified completion rule: a workflow is complete iff its card status is
 * "done"; while the card has a live owner it is in-progress regardless of task
 * statuses (the owner finishes via workflow_stop, which marks the card done).
 *
 * Precedence:
 *   1. scheduler card → 'planned'
 *   2. standalone OR data.template:true → 'template'
 *   3. done → 'completed'
 *   4. live owner agent → 'in-progress'
 *   5. template instance (data.instance_of) without a live owner → 'planned'
 *   6. everything else → 'to-start'
 *
 * The optional `agentIds` set contains live agent actor ids. When provided, a
 * workflow is only in-progress if its ownerAgentId exists in the set (a stale
 * owner binding must not keep a dead workflow in-progress); when omitted the
 * presence of ownerAgentId alone counts. An ownerless map is never in-progress:
 * to-start (or 'planned' when it carries data.instance_of — a template
 * instance spawned but not yet picked up).
 * The optional `archive` input splits the completed category by time: a
 * completed workflow whose last activity is older than ARCHIVE_AFTER_MS is
 * classified as 'archived' instead. `now` defaults to Date.now().
 */
export function classifyWorkflowCategory(
  card: MonoCardListItem,
  agentIds?: ReadonlySet<string>,
  archive?: { cards: MonoCardListItem[]; now?: number },
): EpicCategory {
  let base: EpicCategory
  // Scheduler cards are always planned (they are real nodes, not virtual).
  if (card.type === 'scheduler') base = 'planned'
  // data.template:true is what project.wiki.template_save stamps on saved
  // template maps (id tpl::<mapId>, status doing); without this check they
  // would land in to-start/in-progress instead of the template band.
  else if (card.standalone === true || card.data?.template === true) base = 'template'
  else if (card.status === 'done') base = 'completed'
  else {
    // In-progress requires a live owner: workflow_start binds ownerAgentId
    // when the workflow actually starts, and only workflow_stop (which marks
    // the card done) ends it. Ownerless maps are not running.
    const ownerId = workflowOwnerAgentId(card)
    const ownerLive = ownerId !== undefined && (agentIds === undefined || agentIds.has(ownerId))
    if (ownerLive) base = 'in-progress'
    // A template instance (data.instance_of) that no live owner has picked
    // up is planned: workflow_start binds ownerAgentId when the instance
    // actually starts, so an ownerless instance is queued, not to-start.
    else base = typeof card.data?.instance_of === 'string' ? 'planned' : 'to-start'
  }
  if (base === 'completed' && archive && isArchivedWorkflow(card, archive.cards, archive.now)) return 'archived'
  return base
}

/**
 * The epic-tree ancestors a workflow renders under: its category, and — for
 * archived workflows — the time bucket containing it. Used by the locate
 * (zoom-to-fit) flow to unfold the full ancestor chain (category → bucket →
 * workflow) before fitting, so a hidden target's nodes actually render.
 *
 * Pure, unit-testable. Bucket membership is order-independent, so the
 * archived list is not pre-sorted here (unlike the display path).
 */
export function epicWorkflowPlacement(
  mapCard: MonoCardListItem,
  cards: MonoCardListItem[],
  opts: { agentIds?: ReadonlySet<string>; now?: number } = {},
): { category: EpicCategory; bucketId?: string } {
  const now = opts.now ?? Date.now()
  const category = classifyWorkflowCategory(mapCard, opts.agentIds, { cards, now })
  if (category === 'planned') {
    const sid = schedulerOfInstance(mapCard)
    const schedulerCards = cards.filter(c => c.type === 'scheduler')
    if (sid && schedulerCards.some(c => c.id === sid)) {
      return { category, bucketId: sid }
    }
    return { category }
  }
  if (category !== 'archived') return { category }
  const archived = cards.filter(c =>
    c.type === 'workflow' && classifyWorkflowCategory(c, opts.agentIds, { cards, now }) === 'archived')
  const bucket = buildArchivedBuckets(archived, cards, now).find(b => b.workflows.some(w => w.id === mapCard.id))
  return { category, bucketId: bucket?.id }
}

// ── Per-workflow layout (shared by normal + epic modes) ────────────────

/** Fold scope of a category band: the workflow maps directly inside it, plus
 *  the archived band's time-bucket ids. Used by cascade-collapse (folding a
 *  band folds its whole subtree) and the category right-click "expand all /
 *  collapse all" menu. Pure, unit-testable. */
export function categoryContents(
  cards: MonoCardListItem[],
  category: EpicCategory,
  opts: { agentIds?: ReadonlySet<string>; now?: number } = {},
): { mapIds: string[]; bucketIds: string[] } {
  const now = opts.now ?? Date.now()
  const maps = cards.filter(c =>
    c.type === 'workflow' && classifyWorkflowCategory(c, opts.agentIds, { cards, now }) === category)
  const schedulerCards = cards.filter(c => c.type === 'scheduler')
  const bucketIds = category === 'archived'
    ? buildArchivedBuckets(maps, cards, now).map(b => b.id)
    : category === 'planned'
      ? buildPlannedSchedulerGroups(maps, schedulerCards).map(g => g.id)
      : []
  return { mapIds: maps.map(m => m.id), bucketIds }
}

/** The archived time-bucket id containing a given workflow map, or null when
 *  the workflow is not archived (or lacks a parseable activity timestamp).
 *  Used by fold toggles that target a single workflow: un-folding an archived
 *  workflow must also expand its bucket, otherwise the archived-bucket rule
 *  keeps the tasks hidden and the unfold appears to do nothing. */
export function archivedBucketIdOf(
  cards: MonoCardListItem[],
  mapId: string,
  opts: { agentIds?: ReadonlySet<string>; now?: number } = {},
): string | null {
  const now = opts.now ?? Date.now()
  const wf = cards.find(c => c.id === mapId && c.type === 'workflow')
  if (!wf) return null
  if (classifyWorkflowCategory(wf, opts.agentIds, { cards, now }) !== 'archived') return null
  const archived = cards.filter(c =>
    c.type === 'workflow' && classifyWorkflowCategory(c, opts.agentIds, { cards, now }) === 'archived')
  for (const b of buildArchivedBuckets(archived, cards, now)) {
    if (b.workflows.some(w => w.id === mapId)) return b.id
  }
  return null
}

/** Workflow map ids inside a single archived time bucket. Empty when the
 *  bucket does not exist for the current cards. */
export function bucketWorkflowMapIds(
  cards: MonoCardListItem[],
  bucketId: string,
  opts: { agentIds?: ReadonlySet<string>; now?: number } = {},
): string[] {
  const now = opts.now ?? Date.now()
  const archived = cards.filter(c =>
    c.type === 'workflow' && classifyWorkflowCategory(c, opts.agentIds, { cards, now }) === 'archived')
  const bucket = buildArchivedBuckets(archived, cards, now).find(b => b.id === bucketId)
  return bucket ? bucket.workflows.map(w => w.id) : []
}

/** Workflow maps whose task subgraph is actually on screen: not folded
 *  themselves, not under a folded category band, and — for archived maps —
 *  inside an expanded time bucket. This is the set worth fetching
 *  workflow_topo graphs for; folded virtual nodes (category bands, time
 *  buckets) hide their whole subtree, so mounting workflows must not be
 *  requested. Pure, unit-testable. */
export function visibleWorkflowMapIds(
  cards: MonoCardListItem[],
  fold: {
    foldedWorkflows: ReadonlySet<string>
    foldedCategories: ReadonlySet<string>
    expandedBuckets: ReadonlySet<string>
  },
  opts: { agentIds?: ReadonlySet<string>; now?: number } = {},
): Set<string> {
  const now = opts.now ?? Date.now()
  const workflows = cards.filter(c => c.type === 'workflow')
  const bucketOf = new Map<string, string>()
  const archived = workflows.filter(c =>
    classifyWorkflowCategory(c, opts.agentIds, { cards, now }) === 'archived')
  if (archived.length > 0) {
    for (const b of buildArchivedBuckets(archived, cards, now)) {
      for (const w of b.workflows) bucketOf.set(w.id, b.id)
    }
  }
  const schedulerCards = cards.filter(c => c.type === 'scheduler')
  const planned = workflows.filter(c =>
    classifyWorkflowCategory(c, opts.agentIds, { cards, now }) === 'planned')
  const schedulerOf = new Map<string, string>()
  if (planned.length > 0) {
    for (const g of buildPlannedSchedulerGroups(planned, schedulerCards)) {
      for (const w of g.workflows) schedulerOf.set(w.id, g.id)
    }
  }
  const visible = new Set<string>()
  for (const c of workflows) {
    if (fold.foldedWorkflows.has(c.id)) continue
    const category = classifyWorkflowCategory(c, opts.agentIds, { cards, now })
    if (fold.foldedCategories.has(category)) continue
    const bucketId = bucketOf.get(c.id) ?? schedulerOf.get(c.id)
    if (bucketId && !fold.expandedBuckets.has(bucketId)) continue
    visible.add(c.id)
  }
  return visible
}

interface SingleWorkflowLayout {
  nodes: WorkflowNodeLayout[]
  edges: WorkflowEdgeLayout[]
  width: number
  height: number
}

/**
 * Lay out a single workflow as a directional flowchart positioned at
 * (originX, originY). The map card is the real target node, the start sentinel
 * is render-only, and a tagged reality task is a real terminal node. Tasks are
 * layered by their data.depends_on depth.
 *
 * In LTR mode layers are columns flowing left-to-right; in TTB mode they are
 * rows flowing top-to-bottom. Dependency arrows run from prerequisite to
 * dependent. Extracted from buildWorkflowLayout so the epic tree can embed
 * expanded workflows at arbitrary positions.
 */
function layoutSingleWorkflow(
  mapCard: MonoCardListItem,
  cards: MonoCardListItem[],
  byId: ReadonlyMap<string, MonoCardListItem>,
  isLtr: boolean,
  originX: number,
  originY: number,
  index?: WorkflowCardIndex,
): SingleWorkflowLayout {
  const nodes: WorkflowNodeLayout[] = []
  const edges: WorkflowEdgeLayout[] = []
  const taskIds = workflowTaskIds(mapCard, cards, index).filter(id => byId.has(id))
  const taskSet = new Set(taskIds)

  // Prerequisite edges inside this map: prereq(to) -> dependent(from).
  const depsOf = new Map<string, string[]>()
  for (const taskId of taskIds) {
    const task = byId.get(taskId)
    if (!task) continue
    const deps = taskDependsOn(task).filter(dep => taskSet.has(dep) && dep !== taskId)
    if (deps.length > 0) depsOf.set(taskId, [...new Set(deps)])
  }

  // Dependency depth with cycle guard.
  const depthCache = new Map<string, number>()
  const visiting = new Set<string>()
  const depthOf = (id: string): number => {
    const cached = depthCache.get(id)
    if (cached !== undefined) return cached
    if (visiting.has(id)) return 0
    visiting.add(id)
    let d = 0
    for (const dep of depsOf.get(id) ?? []) {
      d = Math.max(d, depthOf(dep) + 1)
    }
    visiting.delete(id)
    depthCache.set(id, d)
    return d
  }

  // Group tasks into layers by depth; sort for stable output.
  const layers = new Map<number, string[]>()
  let maxDepth = 0
  for (const id of [...taskIds].sort()) {
    const d = depthOf(id)
    maxDepth = Math.max(maxDepth, d)
    const layer = layers.get(d) ?? []
    layer.push(id)
    layers.set(d, layer)
  }

  // Geometry depends on direction: LTR layers are columns (depth axis = x),
  // TTB layers are rows (depth axis = y).
  const laneLenOf = (ids: string[]) =>
    ids.length * (isLtr ? WORKFLOW_TASK_H : WORKFLOW_CARD_W) + (ids.length - 1) * (isLtr ? ROW_GAP : COL_GAP)
  const depthStep = isLtr ? WORKFLOW_CARD_W + COL_GAP : WORKFLOW_TASK_H + TTB_DEPTH_GAP
  const laneStep = isLtr ? WORKFLOW_TASK_H + ROW_GAP : WORKFLOW_CARD_W + COL_GAP

  // Stack each layer along the cross axis; track the longest to size the group.
  let groupLaneLength = isLtr ? Math.max(WORKFLOW_ROOT_H, WORKFLOW_TASK_H) : WORKFLOW_CARD_W
  const laneLength = new Map<number, number>()
  const laneOrigin = new Map<number, number>()
  for (const [depth, ids] of layers) {
    const len = laneLenOf(ids)
    laneLength.set(depth, len)
    groupLaneLength = Math.max(groupLaneLength, len)
  }
  const groupOriginX = originX
  const groupOriginY = originY
  const laneBase = isLtr ? groupOriginY : groupOriginX
  for (const [depth] of layers) {
    laneOrigin.set(depth, laneBase + (groupLaneLength - laneLength.get(depth)!) / 2)
  }

  // Group footprint along the depth axis. The root map occupies the final
  // depth slot after the task layers.
  const groupDepthSpan = isLtr
    ? (maxDepth + 3) * WORKFLOW_CARD_W + (maxDepth + 2) * COL_GAP
    : (maxDepth + 2) * WORKFLOW_TASK_H + WORKFLOW_ROOT_H + (maxDepth + 2) * TTB_DEPTH_GAP
  const groupWidth = isLtr ? groupDepthSpan : groupLaneLength
  const groupHeight = isLtr ? groupLaneLength : groupDepthSpan

  const startId = `${mapCard.id}:start`

  // The start node is render-only; the workflow map itself is the real target.
  // Card-sized like the task nodes (the fold button lives on its right edge).
  nodes.push({
    cardId: startId,
    kind: 'sentinel',
    sentinelRole: 'start',
    x: isLtr ? groupOriginX : groupOriginX + (groupWidth - WORKFLOW_CARD_W) / 2,
    y: isLtr ? groupOriginY + (groupHeight - WORKFLOW_TASK_H) / 2 : groupOriginY,
    width: WORKFLOW_CARD_W,
    height: WORKFLOW_TASK_H,
  })

  nodes.push({
    cardId: mapCard.id,
    kind: 'root',
    x: isLtr ? groupOriginX + (maxDepth + 2) * depthStep : groupOriginX + (groupWidth - WORKFLOW_CARD_W) / 2,
    y: isLtr ? groupOriginY + (groupHeight - WORKFLOW_ROOT_H) / 2 : groupOriginY + (maxDepth + 2) * depthStep,
    width: WORKFLOW_CARD_W,
    height: WORKFLOW_ROOT_H,
  })

  for (const [depth, ids] of layers) {
    const origin = laneOrigin.get(depth)!
    ids.forEach((id, i) => {
      nodes.push({
        cardId: id,
        kind: byId.get(id)?.tags?.includes('reality') ? 'reality' : 'task',
        x: isLtr ? groupOriginX + (depth + 1) * depthStep : origin + i * laneStep,
        y: isLtr ? origin + i * laneStep : groupOriginY + (depth + 1) * depthStep,
        width: WORKFLOW_CARD_W,
        height: WORKFLOW_TASK_H,
      })
    })
  }

  // Start -> entry tasks only. Any declared dependency excludes a task from start.
  for (const id of taskIds) {
    if (taskDependsOn(byId.get(id)!).length === 0) {
      edges.push({ from: startId, to: id, kind: 'start' })
    }
  }
  if (taskIds.length === 0) {
    edges.push({ from: startId, to: mapCard.id, kind: 'start' })
  }
  // Prerequisite -> dependent.
  for (const [from, deps] of depsOf) {
    for (const dep of deps) {
      edges.push({ from: dep, to: from, kind: 'depends_on' })
    }
  }
  const dependedOn = new Set<string>()
  for (const deps of depsOf.values()) for (const dep of deps) dependedOn.add(dep)
  for (const id of taskIds) {
    if (!dependedOn.has(id) && depthOf(id) === maxDepth) {
      edges.push({ from: id, to: mapCard.id, kind: 'flow' })
    }
  }

  return { nodes, edges, width: groupWidth, height: groupHeight }
}

/**
 * Lay out every workflow as a directional flowchart. Workflows are stacked
 * along the cross axis; each map card's group offset may be persisted.
 */
export function buildWorkflowLayout(
  cards: MonoCardListItem[],
  options: WorkflowLayoutOptions = {},
): WorkflowLayout {
  const direction = options.direction ?? 'LTR'
  const isLtr = direction === 'LTR'
  const groupOffsets = options.groupOffsets ?? {}
  const groupGap = isLtr ? GROUP_GAP_LTR : GROUP_GAP_TTB
  const index = getWorkflowCardIndex(cards)
  const byId = index.byId
  const maps = cards.filter(c => c.type === 'workflow' && !c.standalone)
  const nodes: WorkflowNodeLayout[] = []
  const edges: WorkflowEdgeLayout[] = []

  let groupX = PAD
  let groupY = PAD
  let maxWidth = 0
  let maxHeight = 0

  for (const mapCard of maps) {
    const offset = groupOffsets[mapCard.id] ?? { x: 0, y: 0 }
    const originX = (isLtr ? PAD : groupX) + offset.x
    const originY = (isLtr ? groupY : PAD) + offset.y
    const wl = layoutSingleWorkflow(mapCard, cards, byId, isLtr, originX, originY, index)
    nodes.push(...wl.nodes)
    edges.push(...wl.edges)
    maxWidth = Math.max(maxWidth, originX + wl.width - PAD)
    maxHeight = Math.max(maxHeight, originY + wl.height - PAD)
    if (isLtr) {
      groupY += wl.height + groupGap
    } else {
      groupX += wl.width + groupGap
    }
  }

  const height = maps.length > 0 ? maxHeight + PAD * 2 : 0
  const width = maps.length > 0 ? maxWidth + PAD * 2 : 0
  return { nodes, edges, width, height, direction }
}

// ── Epic tree layout options ──────────────────────────────────────────

export interface EpicLayoutOptions {
  /** Workflow map card ids that are expanded to reveal their full internal
   *  flowchart. All other workflows are folded into a single compact card.
   *  Default: empty set (everything folded — the overview state). */
  expandedWorkflows?: ReadonlySet<string>
  /** Tree flow direction. 'TTB' (default): epic root at top, categories in a
   *  row, workflows of a category arranged horizontally below their category.
   *  'LTR': epic root at left, categories in a column, workflows of a category
   *  stacked vertically to the right of their category. */
  direction?: WorkflowDirection
  /** Live agent actor ids. When provided, 'in-progress' classification requires
   *  the workflow's ownerAgentId to exist in this set. */
  agentIds?: ReadonlySet<string>
  /** Reference timestamp for archive classification and bucket boundaries.
   *  Default: Date.now(). */
  now?: number
  /** Archived time-bucket ids that are expanded to show their workflows.
   *  Default: empty set (all buckets folded). */
  expandedBuckets?: ReadonlySet<string>
  /** Categories folded into their bare category node (workflows — and for the
   *  archived band its time buckets — are hidden). Default: empty set (all
   *  categories expanded). */
  foldedCategories?: ReadonlySet<string>
}

/** A single workflow's content block inside the epic tree. */
interface EpicWorkflowBlock {
  startId: string
  mapId: string
  nodes: WorkflowNodeLayout[]
  edges: WorkflowEdgeLayout[]
  width: number
  height: number
}

/** Compute one workflow's block for the epic tree. Folded → a single compact
 *  card; expanded → the full internal flowchart from layoutSingleWorkflow,
 *  which already includes the start sentinel, map root, tasks, and all edges. */
function epicWorkflowBlock(
  mapCard: MonoCardListItem,
  cards: MonoCardListItem[],
  byId: ReadonlyMap<string, MonoCardListItem>,
  folded: boolean,
  ltr: boolean,
  index?: WorkflowCardIndex,
): EpicWorkflowBlock {
  const startId = `${mapCard.id}:start`
  if (folded) {
    // The folded block reserves the same cross-axis extent as the expanded
    // block's minimum (max(ROOT_H, TASK_H) in LTR, CARD_W in TTB) and centers
    // the card inside it — the same anchor the expanded branch uses — so
    // folded and expanded starts in a band stay evenly spaced.
    const crossExtent = ltr ? Math.max(WORKFLOW_TASK_H, WORKFLOW_ROOT_H) : WORKFLOW_CARD_W
    const cardCross = ltr ? WORKFLOW_TASK_H : WORKFLOW_CARD_W
    const nodeCross = (crossExtent - cardCross) / 2
    return {
      startId,
      mapId: mapCard.id,
      nodes: [{
        cardId: startId,
        kind: 'sentinel',
        sentinelRole: 'start',
        x: ltr ? 0 : nodeCross,
        y: ltr ? nodeCross : 0,
        width: WORKFLOW_CARD_W,
        height: WORKFLOW_TASK_H,
      }],
      edges: [],
      width: ltr ? WORKFLOW_CARD_W : crossExtent,
      height: ltr ? crossExtent : Math.max(WORKFLOW_TASK_H, WORKFLOW_ROOT_H),
    }
  }
  // Expanded: layoutSingleWorkflow already centers the start sentinel on the
  // group's cross-axis center (vertical center in LTR, horizontal in TTB) and
  // centers every task lane around the same anchor — i.e. the tallest
  // column's height is reserved split evenly above and below the start node's
  // center. Keep that geometry as-is so adjacent blocks space their content
  // evenly instead of crowding against one edge.
  const internal = layoutSingleWorkflow(mapCard, cards, byId, ltr, 0, 0, index)
  return {
    startId,
    mapId: mapCard.id,
    nodes: internal.nodes,
    edges: internal.edges,
    width: internal.width,
    height: internal.height,
  }
}

// Epic tree geometry constants.
const EPIC_ROOT_W = 168
const EPIC_ROOT_H = 52
const EPIC_CAT_W = 152
const EPIC_CAT_H = 44
const EPIC_PAD = 28
const EPIC_ROOT_CAT_GAP = 80
const EPIC_CAT_START_GAP = 56
const EPIC_WF_GAP = 22
const EPIC_COL_GAP = 72
/** Virtual archived time-bucket node dimensions and gaps. */
const EPIC_BUCKET_W = 184
const EPIC_BUCKET_H = 40
/** Cross-axis gap between adjacent bucket segments. */
const EPIC_BUCKET_GAP = 44
/** Main-axis gap between a bucket node and its workflow blocks. */
const EPIC_BUCKET_WF_GAP = 48

/**
 * Lay out every workflow as a category tree: a single epic root → category
 * nodes → each workflow's start node. Folded workflows collapse into a single
 * compact card; expanded workflows reveal their full internal flowchart.
 *
 * Direction-aware:
 * - 'TTB' (vertical): epic root at top, categories in a horizontal row, each
 *   category's workflows arranged horizontally in a row below the category.
 * - 'LTR' (horizontal): epic root at left, categories in a vertical column,
 *   each category's workflows stacked vertically to the right of the category.
 *
 * Internally computed on (main, cross) axes — main = tree flow axis, cross =
 * the axis bands tile along — then mapped back to (x, y). Virtual epic/category
 * nodes are never real cards — they are render-only structural scaffolding
 * connected by 'tree' edges.
 */
export function buildEpicLayout(
  cards: MonoCardListItem[],
  options: EpicLayoutOptions = {},
): WorkflowLayout {
  const direction = options.direction ?? 'TTB'
  const ltr = direction === 'LTR'
  const expanded = options.expandedWorkflows ?? new Set<string>()
  const expandedBuckets = options.expandedBuckets ?? new Set<string>()
  const foldedCategories = options.foldedCategories ?? new Set<string>()
  const now = options.now ?? Date.now()
  const index = getWorkflowCardIndex(cards)
  const byId = index.byId
  // Include standalone (template) workflows — they appear in the template category.
  // Deduplicate by id so a stale store with two list items sharing one id
  // renders one node (and the category count matches) instead of crashing
  // vis-network's id-keyed DataSet.
  const maps = cards.filter(c => c.type === 'workflow').filter((c, i, arr) =>
    arr.findIndex(o => o.id === c.id) === i)
  const agentIds = options.agentIds
  // Precompute workflow activity once. Classification and ordering both need
  // this value; recomputing it used to rebuild a card index for every workflow.
  const activityByMapId = new Map<string, string>()
  for (const m of maps) activityByMapId.set(m.id, workflowLastActivity(m, cards, index) ?? '')
  const byCategory = new Map<EpicCategory, MonoCardListItem[]>()
  for (const m of maps) {
    let cat = classifyWorkflowCategory(m, agentIds)
    const activity = activityByMapId.get(m.id) ?? ''
    if (cat === 'completed' && activity && Number.isFinite(Date.parse(activity)) && now - Date.parse(activity) > ARCHIVE_AFTER_MS) cat = 'archived'
    if (!byCategory.has(cat)) byCategory.set(cat, [])
    byCategory.get(cat)!.push(m)
  }
  // Add scheduler cards to the planned category so the band appears even
  // without instance workflows. Scheduler cards are real nodes (not virtual).
  const schedulerCards = cards.filter(c => c.type === 'scheduler')
  if (schedulerCards.length > 0) {
    if (!byCategory.has('planned')) byCategory.set('planned', [])
    byCategory.get('planned')!.push(...schedulerCards)
  }

  // Completed and archived workflows are ordered by last activity, newest
  // first ("近的排上面"). ISO timestamps compare lexicographically.
  const lastActivityDesc = (a: MonoCardListItem, b: MonoCardListItem) => {
    const ta = activityByMapId.get(a.id) ?? ''
    const tb = activityByMapId.get(b.id) ?? ''
    if (ta === tb) return a.id < b.id ? -1 : 1
    return ta > tb ? -1 : 1
  }
  byCategory.get('completed')?.sort(lastActivityDesc)
  byCategory.get('archived')?.sort(lastActivityDesc)

  const activeCategories = EPIC_CATEGORY_ORDER.filter(c => byCategory.has(c) && byCategory.get(c)!.length > 0)
  if (activeCategories.length === 0) {
    return { nodes: [], edges: [], width: 0, height: 0, direction }
  }

  // Axis helpers: main = tree flow axis, cross = band tiling axis.
  const crossOf = (w: number, h: number) => (ltr ? h : w)
  const mainOf = (w: number, h: number) => (ltr ? w : h)
  const place = (main: number, cross: number) => (ltr ? { x: main, y: cross } : { x: cross, y: main })

  // Phase 1: compute each category band's blocks and extents.
  interface BandBucket {
    bucket: EpicBucket
    /** Workflow blocks; empty when the bucket is collapsed. */
    blocks: EpicWorkflowBlock[]
    /** Segment extent along the cross axis (blocks tiling, at least the bucket node's cross size). */
    crossSize: number
    /** Blocks' tiling extent along the cross axis (before centering inside the segment). */
    tilingCross: number
    /** Deepest block extent along the main axis (0 when collapsed). */
    mainSize: number
  }
  interface Band {
    category: EpicCategory
    catId: string
    workflows: MonoCardListItem[]
    blocks: EpicWorkflowBlock[]
    /** Archived band only: time-bucket segments in display order. */
    buckets?: BandBucket[]
    /** Band extent along the cross axis (tiling of blocks, at least the category node's cross size). */
    crossSize: number
    /** Blocks' tiling extent along the cross axis (before centering inside the band). */
    tilingCross: number
    /** Deepest block extent along the main axis. */
    mainSize: number
  }

  const catCrossSize = crossOf(EPIC_CAT_W, EPIC_CAT_H)
  const catMainSize = mainOf(EPIC_CAT_W, EPIC_CAT_H)
  const bucketCrossSize = crossOf(EPIC_BUCKET_W, EPIC_BUCKET_H)
  const bucketMainSize = mainOf(EPIC_BUCKET_W, EPIC_BUCKET_H)
  // Scheduler nodes render as real card-sized nodes (same size as task nodes),
  // not compact bucket badges.
  const schedCardW = WORKFLOW_CARD_W
  const schedCardH = WORKFLOW_TASK_H
  const schedCrossSize = crossOf(schedCardW, schedCardH)
  const schedMainSize = mainOf(schedCardW, schedCardH)

  const bands: Band[] = []
  for (const cat of activeCategories) {
    const workflows = byCategory.get(cat)!
    const bandFolded = foldedCategories.has(cat)

    if (cat === 'archived') {
      // The archived band inserts a virtual time-bucket level between the
      // category node and the workflow start nodes. A folded band collapses to
      // its bare category node — buckets and workflows are all hidden.
      const bandBuckets: BandBucket[] = bandFolded ? [] : buildArchivedBuckets(workflows, cards, now, index).map(bucket => {
        const bucketExpanded = expandedBuckets.has(bucket.id)
        const blocks: EpicWorkflowBlock[] = bucketExpanded
          ? bucket.workflows.map(wf => epicWorkflowBlock(wf, cards, byId, !expanded.has(wf.id), ltr, index))
          : []
        let tilingCross = 0
        let mainSize = 0
        for (let i = 0; i < blocks.length; i++) {
          if (i > 0) tilingCross += EPIC_WF_GAP
          tilingCross += crossOf(blocks[i]!.width, blocks[i]!.height)
          mainSize = Math.max(mainSize, mainOf(blocks[i]!.width, blocks[i]!.height))
        }
        return { bucket, blocks, crossSize: Math.max(bucketCrossSize, tilingCross), tilingCross, mainSize }
      })
      let tilingCross = 0
      let mainSize = bandFolded ? 0 : bucketMainSize
      for (let i = 0; i < bandBuckets.length; i++) {
        if (i > 0) tilingCross += EPIC_BUCKET_GAP
        tilingCross += bandBuckets[i]!.crossSize
        if (bandBuckets[i]!.blocks.length > 0) {
          mainSize = Math.max(mainSize, bucketMainSize + EPIC_BUCKET_WF_GAP + bandBuckets[i]!.mainSize)
        }
      }
      bands.push({
        category: cat,
        catId: epicCategoryId(cat),
        workflows,
        blocks: [],
        buckets: bandBuckets,
        crossSize: Math.max(catCrossSize, tilingCross),
        tilingCross,
        mainSize,
      })
      continue
    }

    if (cat === 'planned') {
      // The planned band groups instance workflows under their scheduler cards.
      // Workflows without a scheduler_card_id (manual instances / legacy data)
      // render directly under the category band. Scheduler cards are real nodes
      // in the band (not workflow maps), handled separately below.
      const schedulerCards = cards.filter(c => c.type === 'scheduler')
      const wfWorkflows = workflows.filter(w => w.type !== 'scheduler')
      const schedulerGroups = bandFolded ? [] : buildPlannedSchedulerGroups(wfWorkflows, schedulerCards)
      const affiliatedIds = new Set<string>()
      for (const g of schedulerGroups) for (const w of g.workflows) affiliatedIds.add(w.id)
      const ungrouped = wfWorkflows.filter(wf => !affiliatedIds.has(wf.id))

      // Scheduler cards with no current planned instances still render as
      // real nodes (count=0) so a planned task never vanishes from the graph
      // just because its instances moved to in-progress/completed.
      const groupedSchedulerIds = new Set(schedulerGroups.map(g => g.schedulerCardId))
      const soloSchedulerBuckets: EpicBucket[] = (bandFolded ? [] : schedulerCards)
        .filter(s => !groupedSchedulerIds.has(s.id))
        .map(s => ({ id: s.id, kind: 'scheduler', label: s.id, workflows: [] }) as EpicBucket)

      const bandBuckets: BandBucket[] = schedulerGroups.map(group => {
        const bucketExpanded = expandedBuckets.has(group.id)
        const blocks: EpicWorkflowBlock[] = bucketExpanded
          ? group.workflows.map(wf => epicWorkflowBlock(wf, cards, byId, !expanded.has(wf.id), ltr, index))
          : []
        let tilingCross = 0
        let mainSize = 0
        for (let i = 0; i < blocks.length; i++) {
          if (i > 0) tilingCross += EPIC_WF_GAP
          tilingCross += crossOf(blocks[i]!.width, blocks[i]!.height)
          mainSize = Math.max(mainSize, mainOf(blocks[i]!.width, blocks[i]!.height))
        }
        const bucket: EpicBucket = { id: group.id, kind: 'scheduler', label: group.label, workflows: group.workflows }
        return { bucket, blocks, crossSize: Math.max(schedCrossSize, tilingCross), tilingCross, mainSize }
      })
      // Append zero-instance scheduler buckets. When such a scheduler is
      // expanded, mount a dashed ghost block previewing the template a future
      // instance will materialize from (skipped when the template reference
      // is dangling — the preview would show nothing actionable).
      for (const bucket of soloSchedulerBuckets) {
        const blocks: EpicWorkflowBlock[] = []
        if (expandedBuckets.has(bucket.id)) {
          const schedData = byId.get(bucket.id)?.data as Record<string, unknown> | undefined
          const tplId = typeof schedData?.workflow_template === 'string' ? schedData.workflow_template : ''
          const tplCard = tplId ? byId.get(tplId) : undefined
          if (tplCard) {
            // Same geometry as a folded start block, so the ghost previews
            // exactly where the first instance will mount.
            const crossExtent = ltr ? Math.max(WORKFLOW_TASK_H, WORKFLOW_ROOT_H) : WORKFLOW_CARD_W
            const cardCross = ltr ? WORKFLOW_TASK_H : WORKFLOW_CARD_W
            const nodeCross = (crossExtent - cardCross) / 2
            const ghostId = `ghost:${bucket.id}`
            const ghostBlock: EpicWorkflowBlock = {
              startId: ghostId,
              mapId: tplCard.id,
              nodes: [{
                cardId: ghostId,
                kind: 'ghost',
                templateId: tplCard.id,
                x: ltr ? 0 : nodeCross,
                y: ltr ? nodeCross : 0,
                width: WORKFLOW_CARD_W,
                height: WORKFLOW_TASK_H,
              }],
              edges: [],
              width: ltr ? WORKFLOW_CARD_W : crossExtent,
              height: ltr ? crossExtent : Math.max(WORKFLOW_TASK_H, WORKFLOW_ROOT_H),
            }
            blocks.push(ghostBlock)
          }
        }
        let tilingCross = 0
        let mainSize = 0
        for (let i = 0; i < blocks.length; i++) {
          tilingCross += crossOf(blocks[i]!.width, blocks[i]!.height)
          mainSize = Math.max(mainSize, mainOf(blocks[i]!.width, blocks[i]!.height))
        }
        bandBuckets.push({ bucket, blocks, crossSize: Math.max(schedCrossSize, tilingCross), tilingCross, mainSize })
      }

      const ungroupedBlocks: EpicWorkflowBlock[] = bandFolded ? [] : ungrouped.map(wf =>
        epicWorkflowBlock(wf, cards, byId, !expanded.has(wf.id), ltr, index),
      )

      let tilingCross = 0
      let mainSize = bandFolded ? 0 : schedMainSize
      for (let i = 0; i < bandBuckets.length; i++) {
        if (i > 0) tilingCross += EPIC_BUCKET_GAP
        tilingCross += bandBuckets[i]!.crossSize
        if (bandBuckets[i]!.blocks.length > 0) {
          mainSize = Math.max(mainSize, schedMainSize + EPIC_BUCKET_WF_GAP + bandBuckets[i]!.mainSize)
        }
      }
      if (bandBuckets.length > 0 && ungroupedBlocks.length > 0) tilingCross += EPIC_BUCKET_GAP
      for (let i = 0; i < ungroupedBlocks.length; i++) {
        if (i > 0) tilingCross += EPIC_WF_GAP
        tilingCross += crossOf(ungroupedBlocks[i]!.width, ungroupedBlocks[i]!.height)
        mainSize = Math.max(mainSize, mainOf(ungroupedBlocks[i]!.width, ungroupedBlocks[i]!.height))
      }

      bands.push({
        category: cat,
        catId: epicCategoryId(cat),
        workflows,
        blocks: ungroupedBlocks,
        buckets: bandBuckets,
        crossSize: Math.max(catCrossSize, tilingCross),
        tilingCross,
        mainSize,
      })
      continue
    }

    // A folded band emits no workflow blocks — it collapses to its bare
    // category node.
    const blocks: EpicWorkflowBlock[] = bandFolded ? [] : workflows.map(wf =>
      epicWorkflowBlock(wf, cards, byId, !expanded.has(wf.id), ltr, index),
    )
    let tilingCross = 0
    let mainSize = 0
    for (let i = 0; i < blocks.length; i++) {
      if (i > 0) tilingCross += EPIC_WF_GAP
      tilingCross += crossOf(blocks[i]!.width, blocks[i]!.height)
      mainSize = Math.max(mainSize, mainOf(blocks[i]!.width, blocks[i]!.height))
    }
    bands.push({
      category: cat,
      catId: epicCategoryId(cat),
      workflows,
      blocks,
      crossSize: Math.max(catCrossSize, tilingCross),
      tilingCross,
      mainSize,
    })
  }

  // Phase 2: tile bands along the cross axis.
  const rootCrossSize = crossOf(EPIC_ROOT_W, EPIC_ROOT_H)
  const rootMainSize = mainOf(EPIC_ROOT_W, EPIC_ROOT_H)
  const catMainPos = EPIC_PAD + rootMainSize + EPIC_ROOT_CAT_GAP
  const contentMainPos = catMainPos + catMainSize + EPIC_CAT_START_GAP

  let bandCross = EPIC_PAD
  const bandOrigins: number[] = []
  for (const band of bands) {
    bandOrigins.push(bandCross)
    bandCross += band.crossSize + EPIC_COL_GAP
  }
  const totalCross = bandCross - EPIC_COL_GAP
  const rootCross = EPIC_PAD + (totalCross - EPIC_PAD - rootCrossSize) / 2

  // Phase 3: emit nodes and edges.
  const nodes: WorkflowNodeLayout[] = []
  const edges: WorkflowEdgeLayout[] = []
  let maxMain = catMainPos + catMainSize

  // Epic root node (centered on the cross axis).
  nodes.push({ cardId: EPIC_ROOT_ID, kind: 'epic', ...place(EPIC_PAD, rootCross), width: EPIC_ROOT_W, height: EPIC_ROOT_H })

  for (let bi = 0; bi < bands.length; bi++) {
    const band = bands[bi]!
    const origin = bandOrigins[bi]!

    // Category node (centered within its band along the cross axis).
    nodes.push({
      cardId: band.catId,
      kind: 'category',
      label: band.category,
      ...place(catMainPos, origin + (band.crossSize - catCrossSize) / 2),
      width: EPIC_CAT_W,
      height: EPIC_CAT_H,
    })
    // Epic root → category.
    edges.push({ from: EPIC_ROOT_ID, to: band.catId, kind: 'tree' })

    if (band.buckets) {
      // Archived or planned band: category → bucket → workflow start.
      // Planned also has ungrouped blocks that connect directly to the category.
      // A band's buckets are homogeneous: planned = scheduler (card-sized),
      // archived = time bucket (compact badge). Pick sizes accordingly.
      const isSchedBand = band.buckets[0]?.bucket.kind === 'scheduler'
      const bbCross = isSchedBand ? schedCrossSize : bucketCrossSize
      const bbMain = isSchedBand ? schedMainSize : bucketMainSize
      const bbCardW = isSchedBand ? schedCardW : EPIC_BUCKET_W
      const bbCardH = isSchedBand ? schedCardH : EPIC_BUCKET_H
      const wfMainPos = contentMainPos + bbMain + EPIC_BUCKET_WF_GAP
      let segCross = origin + (band.crossSize - band.tilingCross) / 2
      for (const bb of band.buckets) {
        // Scheduler groups (kind='scheduler') render as real scheduler nodes;
        // archived time buckets render as virtual bucket nodes.
        const nodeKind = bb.bucket.kind === 'scheduler' ? 'scheduler' : 'bucket'
        nodes.push({
          cardId: bb.bucket.id,
          kind: nodeKind,
          label: bb.bucket.label,
          count: bb.bucket.workflows.length,
          ...place(contentMainPos, segCross + (bb.crossSize - bbCross) / 2),
          width: bbCardW,
          height: bbCardH,
        })
        edges.push({ from: band.catId, to: bb.bucket.id, kind: 'tree' })

        let blockCross = segCross + (bb.crossSize - bb.tilingCross) / 2
        for (const block of bb.blocks) {
          edges.push({ from: bb.bucket.id, to: block.startId, kind: 'tree' })
          for (const node of block.nodes) {
            const offset = place(wfMainPos, blockCross)
            nodes.push({ ...node, x: offset.x + node.x, y: offset.y + node.y })
          }
          edges.push(...block.edges)
          blockCross += crossOf(block.width, block.height) + EPIC_WF_GAP
        }
        segCross += bb.crossSize + EPIC_BUCKET_GAP
      }
      // For planned: render ungrouped blocks directly under the category,
      // at the same main-axis position as the bucket nodes.
      for (const block of band.blocks) {
        edges.push({ from: band.catId, to: block.startId, kind: 'tree' })
        for (const node of block.nodes) {
          const offset = place(contentMainPos, segCross)
          nodes.push({ ...node, x: offset.x + node.x, y: offset.y + node.y })
        }
        edges.push(...block.edges)
        segCross += crossOf(block.width, block.height) + EPIC_WF_GAP
      }
      maxMain = Math.max(maxMain, contentMainPos + band.mainSize)
      continue
    }

    // Category → each workflow start node. Folded bands emit no blocks, so no
    // edges either (their start nodes do not exist).
    for (const block of band.blocks) {
      edges.push({ from: band.catId, to: block.startId, kind: 'tree' })
    }

    // Tile blocks along the cross axis, centered within the band.
    let blockCross = origin + (band.crossSize - band.tilingCross) / 2
    for (const block of band.blocks) {
      for (const node of block.nodes) {
        const offset = place(contentMainPos, blockCross)
        nodes.push({ ...node, x: offset.x + node.x, y: offset.y + node.y })
      }
      edges.push(...block.edges)
      blockCross += crossOf(block.width, block.height) + EPIC_WF_GAP
    }
    maxMain = Math.max(maxMain, contentMainPos + band.mainSize)
  }

  const totalMain = maxMain + EPIC_PAD
  const width = (ltr ? totalMain : totalCross + EPIC_PAD)
  const height = (ltr ? totalCross + EPIC_PAD : totalMain)
  return { nodes, edges, width, height, direction }
}

/** Color of a depends_on edge when the target task is **done**: low-key slate
 *  gray solid — completed work recedes visually so active edges stand out. */
export const DEPENDS_ON_DONE_COLOR = '#64748b'
/** Color of a depends_on edge when the target task is **not started**: yellow dashed. */
export const DEPENDS_ON_PENDING_COLOR = '#eab308'

/** Statuses that mean "not started" — the dependency is waiting and not yet
 *  actionable.  Maps to a yellow dashed line. */
const NOT_STARTED_STATUSES = new Set(['backlog', 'todo'])

/**
 * Status-based color override for a depends_on edge whose target task has
 * `targetStatus`.
 *
 * - `done` → green
 * - `backlog` / `todo` / empty → yellow ("not started")
 * - all other statuses (`doing`, `blocked`, `cancelled`, `pending_review`, …)
 *   → **null** = the caller should keep the existing default style.  The
 *   in-progress / `doing` case never reaches this function in practice (it is
 *   handled by the animated accent path in the canvas renderer), but the
 *   terminal / error states (`blocked`, `cancelled`, `pending_review`) are
 *   explicitly **not** "not started" and must not be recolored.
 *
 * Extracted as a pure function so the status→color mapping is unit-testable
 * without mounting the canvas renderer.
 */
export function dependsOnEdgeColor(targetStatus: string | undefined): string | null {
  const s = targetStatus ?? ''
  if (s === 'done') return DEPENDS_ON_DONE_COLOR
  // Empty status means "no status set" → treat as not-started.
  if (s === '' || NOT_STARTED_STATUSES.has(s)) return DEPENDS_ON_PENDING_COLOR
  return null
}

export type WorkflowRouteSide = 'left' | 'right' | 'top' | 'bottom'

export interface WorkflowRoutePoint {
  x: number
  y: number
}

export interface WorkflowRoute {
  /** Orthogonal polyline from the source card border to the target card border. */
  points: WorkflowRoutePoint[]
  /** Side of the source card the route leaves from. */
  exitSide: WorkflowRouteSide
  /** Side of the target card the route enters; the arrow head sits here. */
  enterSide: WorkflowRouteSide
}

/** Extra clearance around node rectangles the router must not touch. */
const ROUTE_CLEAR = 4

function dedupePoints(pts: WorkflowRoutePoint[]): WorkflowRoutePoint[] {
  const out: WorkflowRoutePoint[] = []
  for (const p of pts) {
    const last = out[out.length - 1]
    if (!last || Math.abs(last.x - p.x) > 0.5 || Math.abs(last.y - p.y) > 0.5) out.push(p)
  }
  return out
}

/** Sum of axis-aligned segment lengths in a polyline. */
function manhattanLength(pts: WorkflowRoutePoint[]): number {
  let len = 0
  for (let i = 1; i < pts.length; i++) len += Math.abs(pts[i]!.x - pts[i - 1]!.x) + Math.abs(pts[i]!.y - pts[i - 1]!.y)
  return len
}

/** Pick an axis value for a cross run. Preferred centerlines win when clear;
 *  otherwise the run is centered in a free corridor: the midpoint between two
 *  adjacent occupied intervals, or half a lane gap outside the outermost
 *  interval — never hugging a card edge at ROUTE_CLEAR. Corridors nearest the
 *  preferred centerlines are tried first. */
function pickClearAxis(
  preferred: number[],
  intervals: Array<readonly [number, number]>,
  laneGap: number,
  blocked: (v: number) => boolean,
): number {
  for (const v of preferred) {
    if (!blocked(v)) return v
  }
  const sorted = [...intervals].sort((a, b) => a[0] - b[0])
  const candidates: number[] = []
  if (sorted.length > 0) {
    candidates.push(sorted[0]![0] - laneGap / 2)
    for (let i = 0; i < sorted.length - 1; i++) {
      candidates.push((sorted[i]![1] + sorted[i + 1]![0]) / 2)
    }
    candidates.push(sorted[sorted.length - 1]![1] + laneGap / 2)
  }
  const dist = (v: number) => Math.min(...preferred.map(p => Math.abs(v - p)))
  candidates.sort((a, b) => dist(a) - dist(b))
  for (const v of candidates) {
    if (!blocked(v)) return v
  }
  return preferred[0] ?? 0
}

/**
 * Orthogonal (axis-aligned) route between two workflow nodes that stays in the
 * lanes between depth layers, so the visible path never crosses a card.
 *
 * LTR: exit the source's right edge, branch through the midpoint of the
 * inter-layer gap, then enter the target's left edge. TTB is the transposed
 * version. The cross-axis run is pushed to a clear position when an
 * intermediate card blocks that midpoint.
 */
export function routeWorkflowEdge(
  source: WorkflowNodeLayout,
  target: WorkflowNodeLayout,
  nodes: WorkflowNodeLayout[],
  direction: WorkflowDirection = 'LTR',
): WorkflowRoute {
  const obstacles = nodes.filter(n => n.cardId !== source.cardId && n.cardId !== target.cardId)

  if (direction === 'TTB') {
    const srcBottom = source.y + source.height
    const srcCx = source.x + source.width / 2
    const tgtTop = target.y
    const tgtCx = target.x + target.width / 2

    if (tgtTop <= srcBottom) {
      // Backward edge (cycle residue): go around via a lane above both cards.
      const laneY = Math.min(source.y, target.y) - ROW_GAP / 2
      return {
        points: dedupePoints([
          { x: srcCx, y: source.y },
          { x: srcCx, y: laneY },
          { x: tgtCx, y: laneY },
          { x: tgtCx, y: target.y + target.height },
        ]),
        exitSide: 'top',
        enterSide: 'bottom',
      }
    }

    // Branch in the midpoint of the next layer gap, then merge in the midpoint
    // of the previous layer gap. For adjacent cards these are the same point.
    const nextTop = Math.min(tgtTop, ...nodes.filter(n => n.cardId !== source.cardId && n.y >= srcBottom).map(n => n.y))
    const prevBottom = Math.max(srcBottom, ...nodes.filter(n => n.cardId !== target.cardId && n.y + n.height <= tgtTop).map(n => n.y + n.height))
    const srcGapY = (srcBottom + nextTop) / 2
    const tgtGapY = (prevBottom + tgtTop) / 2
    const xStar = pickClearAxis(
      [srcCx, tgtCx],
      obstacles.map(o => [o.x - ROUTE_CLEAR, o.x + o.width + ROUTE_CLEAR] as const),
      COL_GAP,
      x =>
        obstacles.some(
          o => x > o.x - ROUTE_CLEAR && x < o.x + o.width + ROUTE_CLEAR && o.y + o.height > srcGapY && o.y < tgtGapY,
        ),
    )
    const standard: WorkflowRoute = {
      points: dedupePoints([
        { x: srcCx, y: srcBottom },
        { x: srcCx, y: srcGapY },
        { x: xStar, y: srcGapY },
        { x: xStar, y: tgtGapY },
        { x: tgtCx, y: tgtGapY },
        { x: tgtCx, y: tgtTop },
      ]),
      exitSide: 'bottom',
      enterSide: 'top',
    }

    // Side-bypass: for edges that span multiple layers (an obstacle sits directly
    // between source and target), the standard Z-detour swings from card-center
    // out to the obstacle edge — half a card width when cards are wide. A bypass
    // that exits from the card's side edge and runs parallel to the flow only
    // needs ROUTE_CLEAR of clearance, so it is shorter when the detour is large.
    return pickShorter(standard, sideBypassesTTB(source, target, obstacles))
  }

  const srcRight = source.x + source.width
  const srcCy = source.y + source.height / 2
  const tgtLeft = target.x
  const tgtCy = target.y + target.height / 2

  if (tgtLeft <= srcRight) {
    // Backward edge (cycle residue): go around via a lane left of both cards.
    const laneX = Math.min(source.x, target.x) - COL_GAP / 2
    return {
      points: dedupePoints([
        { x: source.x, y: srcCy },
        { x: laneX, y: srcCy },
        { x: laneX, y: tgtCy },
        { x: target.x + target.width, y: tgtCy },
      ]),
      exitSide: 'left',
      enterSide: 'right',
    }
  }

  // Branch in the midpoint of the next layer gap, then merge in the midpoint
  // of the previous layer gap. For adjacent cards these are the same point.
  const nextLeft = Math.min(tgtLeft, ...nodes.filter(n => n.cardId !== source.cardId && n.x >= srcRight).map(n => n.x))
  const prevRight = Math.max(srcRight, ...nodes.filter(n => n.cardId !== target.cardId && n.x + n.width <= tgtLeft).map(n => n.x + n.width))
  const srcGapX = (srcRight + nextLeft) / 2
  const tgtGapX = (prevRight + tgtLeft) / 2
  const yStar = pickClearAxis(
    [srcCy, tgtCy],
    obstacles.map(o => [o.y - ROUTE_CLEAR, o.y + o.height + ROUTE_CLEAR] as const),
    ROW_GAP,
    y =>
      obstacles.some(
        o => y > o.y - ROUTE_CLEAR && y < o.y + o.height + ROUTE_CLEAR && o.x + o.width > srcGapX && o.x < tgtGapX,
      ),
  )
  const standard: WorkflowRoute = {
    points: dedupePoints([
      { x: srcRight, y: srcCy },
      { x: srcGapX, y: srcCy },
      { x: srcGapX, y: yStar },
      { x: tgtGapX, y: yStar },
      { x: tgtGapX, y: tgtCy },
      { x: tgtLeft, y: tgtCy },
    ]),
    exitSide: 'right',
    enterSide: 'left',
  }

  return pickShorter(standard, sideBypassesLTR(source, target, obstacles))
}

/** Return the route with the shortest Manhattan path among a standard route
 *  and zero or more alternatives. */
function pickShorter(standard: WorkflowRoute, alternatives: WorkflowRoute[]): WorkflowRoute {
  let best = standard
  for (const alt of alternatives) {
    if (manhattanLength(alt.points) < manhattanLength(best.points)) best = alt
  }
  return best
}

/** Build side-bypass routes for TTB (vertical flow). A bypass exits from the
 *  source's left/right edge, runs vertically past all obstacles in the band,
 *  and enters the target from the same side. Only returned when there is at
 *  least one obstacle between source and target. */
function sideBypassesTTB(
  source: WorkflowNodeLayout,
  target: WorkflowNodeLayout,
  obstacles: WorkflowNodeLayout[],
): WorkflowRoute[] {
  const srcCy = source.y + source.height / 2
  const tgtCy = target.y + target.height / 2
  const crossMin = Math.min(srcCy, tgtCy) - ROUTE_CLEAR
  const crossMax = Math.max(srcCy, tgtCy) + ROUTE_CLEAR
  const inBand = (o: WorkflowNodeLayout) => o.y + o.height > crossMin && o.y < crossMax
  const bandObs = obstacles.filter(inBand)
  if (!bandObs.length) return []

  const bypassGap = COL_GAP / 2
  const isClear = (x: number) => !obstacles.some(o => inBand(o) && x > o.x - ROUTE_CLEAR && x < o.x + o.width + ROUTE_CLEAR)
  const routes: WorkflowRoute[] = []

  // Right bypass: past every obstacle's right edge, offset by half the
  //  cross-axis lane gap (matching pickClearAxis) so the outermost line sits
  //  at 1/2 spacing — consistent with LTR behaviour.
  const xR = Math.max(...bandObs.map(o => o.x + o.width)) + bypassGap
  if (isClear(xR)) {
    routes.push({
      points: dedupePoints([
        { x: source.x + source.width, y: srcCy },
        { x: xR, y: srcCy },
        { x: xR, y: tgtCy },
        { x: target.x + target.width, y: tgtCy },
      ]),
      exitSide: 'right',
      enterSide: 'right',
    })
  }

  // Left bypass: before every obstacle's left edge.
  const xL = Math.min(...bandObs.map(o => o.x)) - bypassGap
  if (isClear(xL)) {
    routes.push({
      points: dedupePoints([
        { x: source.x, y: srcCy },
        { x: xL, y: srcCy },
        { x: xL, y: tgtCy },
        { x: target.x, y: tgtCy },
      ]),
      exitSide: 'left',
      enterSide: 'left',
    })
  }

  return routes
}

/** Build side-bypass routes for LTR (horizontal flow). A bypass exits from the
 *  source's top/bottom edge, runs horizontally past all obstacles in the band,
 *  and enters the target from the same side. */
function sideBypassesLTR(
  source: WorkflowNodeLayout,
  target: WorkflowNodeLayout,
  obstacles: WorkflowNodeLayout[],
): WorkflowRoute[] {
  const srcCx = source.x + source.width / 2
  const tgtCx = target.x + target.width / 2
  const crossMin = Math.min(srcCx, tgtCx) - ROUTE_CLEAR
  const crossMax = Math.max(srcCx, tgtCx) + ROUTE_CLEAR
  const inBand = (o: WorkflowNodeLayout) => o.x + o.width > crossMin && o.x < crossMax
  const bandObs = obstacles.filter(inBand)
  if (!bandObs.length) return []

  const isClear = (y: number) => !obstacles.some(o => inBand(o) && y > o.y - ROUTE_CLEAR && y < o.y + o.height + ROUTE_CLEAR)
  const routes: WorkflowRoute[] = []

  // Bottom bypass: below every obstacle's bottom edge.
  const yB = Math.max(...bandObs.map(o => o.y + o.height)) + ROUTE_CLEAR
  if (isClear(yB)) {
    routes.push({
      points: dedupePoints([
        { x: srcCx, y: source.y + source.height },
        { x: srcCx, y: yB },
        { x: tgtCx, y: yB },
        { x: tgtCx, y: target.y + target.height },
      ]),
      exitSide: 'bottom',
      enterSide: 'bottom',
    })
  }

  // Top bypass: above every obstacle's top edge.
  const yT = Math.min(...bandObs.map(o => o.y)) - ROUTE_CLEAR
  if (isClear(yT)) {
    routes.push({
      points: dedupePoints([
        { x: srcCx, y: source.y },
        { x: srcCx, y: yT },
        { x: tgtCx, y: yT },
        { x: tgtCx, y: target.y },
      ]),
      exitSide: 'top',
      enterSide: 'top',
    })
  }

  return routes
}
