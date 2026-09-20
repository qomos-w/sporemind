import { useCallback, useSyncExternalStore } from 'react'
import type { TimeFilterState } from './TopologyModeView'
import type { WorkflowNodeLayout } from './workflowLayout'

// Re-export the topology time-filter state shape so workflow consumers have a
// single import site — the type itself is defined once in TopologyModeView and
// never duplicated here.
export type { TimeFilterState } from './TopologyModeView'

/**
 * Multi-select filter state for the workflow graph. Each dimension is a
 * module-level store slice mirroring `topoFiltersStore.ts`; empty sets mean
 * "no filter" for that dimension. Setters replace slices immutably (sets are
 * copied) so every mutation triggers subscribers exactly once.
 */
export interface WorkflowFilterState {
  /** Task statuses (backlog, todo, doing, pending_review, done, failed, …).
   *  Non-empty: task nodes whose status is not in the set are hidden. */
  statusFilter: Set<string>
  /** Epic categories (in-progress, to-start, template, planned, completed,
   *  archived). Non-empty: category band nodes not in the set — and their
   *  whole subtree — are hidden. */
  categoryFilter: Set<string>
  /** Owner agent kinds (worker, reviewer, …; `(none)` for unowned tasks).
   *  Non-empty: task nodes whose owner agent kind is not in the set are
   *  hidden; tasks without an owner are included only when `(none)` is
   *  selected. */
  ownerKindFilter: Set<string>
  /** Free-text search: substring or `/regexp/` match on node titles. */
  textFilter: string
  /** Reuses the topology time-filter state; applies to node `lastActivity`. */
  timeFilter: TimeFilterState
}

/** Sentinel owner-kind value selecting tasks that have no assigned agent. */
export const NONE_OWNER_KIND = '(none)'

const DEFAULT_TIME_FILTER: TimeFilterState = {
  mode: 'all',
  agoValue: '7',
  agoUnit: 'days',
  agoDirection: 'after',
  rangeFrom: '',
  rangeTo: '',
}

function defaultState(): WorkflowFilterState {
  return {
    statusFilter: new Set(),
    categoryFilter: new Set(),
    ownerKindFilter: new Set(),
    textFilter: '',
    timeFilter: { ...DEFAULT_TIME_FILTER },
  }
}

let state: WorkflowFilterState = defaultState()
const listeners = new Set<() => void>()

function emit() {
  for (const l of listeners) l()
}

export const workflowFiltersStore = {
  getState: (): WorkflowFilterState => state,
  subscribe(cb: () => void) {
    listeners.add(cb)
    return () => {
      listeners.delete(cb)
    }
  },
  setStatusFilter(next: Set<string>) {
    state = { ...state, statusFilter: new Set(next) }
    emit()
  },
  setCategoryFilter(next: Set<string>) {
    state = { ...state, categoryFilter: new Set(next) }
    emit()
  },
  setOwnerKindFilter(next: Set<string>) {
    state = { ...state, ownerKindFilter: new Set(next) }
    emit()
  },
  setTextFilter(text: string) {
    state = { ...state, textFilter: text }
    emit()
  },
  setTimeFilter(tf: TimeFilterState) {
    state = { ...state, timeFilter: { ...tf } }
    emit()
  },
  clearAll() {
    state = defaultState()
    emit()
  },
  /** Number of filter dimensions currently constraining the graph (0..5).
   *  A time filter only counts when it actually produces bounds, so the
   *  toolbar badge never disagrees with what `applyWorkflowFilters` does. */
  activeCount(): number {
    let n = 0
    if (state.statusFilter.size > 0) n++
    if (state.categoryFilter.size > 0) n++
    if (state.ownerKindFilter.size > 0) n++
    if (state.textFilter.trim() !== '') n++
    const { lower, upper } = workflowTimeBounds(state.timeFilter)
    if (lower > 0 || upper > 0) n++
    return n
  },
}

/** React binding for the store — mirrors `useCustomFilters`. */
export function useWorkflowFilters(): WorkflowFilterState {
  const subscribe = useCallback((cb: () => void) => workflowFiltersStore.subscribe(cb), [])
  const getSnapshot = useCallback(() => workflowFiltersStore.getState(), [])
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}

// ── Pure filter application logic ─────────────────────────────────────

const MS_PER_HOUR = 60 * 60 * 1000
const MS_PER_DAY = 24 * MS_PER_HOUR

/**
 * Resolve a time filter into a half-open [lower, upper] window in epoch ms.
 * A bound of 0 means open on that side; both 0 means the filter is inactive.
 *
 * Mirrors `TopologyModeView.computeTimeBounds`. It is reimplemented here
 * (rather than imported) to avoid a runtime import cycle: TopologyModeView
 * imports WorkflowGraph, which imports this module. The type shape stays
 * shared — only the 12-line bound math is mirrored, and both functions must
 * agree so the toolbar badge and the graph filter never diverge.
 */
export function workflowTimeBounds(tf: TimeFilterState): { lower: number; upper: number } {
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
    return { lower: Number.isNaN(lower) ? 0 : lower, upper: Number.isNaN(upper) ? 0 : upper }
  }
  return { lower: 0, upper: 0 }
}

/**
 * Match a node title against a free-text filter value. Supports the
 * `/pattern/flags` regexp convention; otherwise falls back to a
 * case-insensitive substring match. An invalid regexp pattern matches nothing.
 * Mirrors topology's `matchTitle` (see `TopologyModeView.matchesField`).
 */
export function matchWorkflowTitle(title: string, value: string): boolean {
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

/**
 * A built workflow graph node enriched with the attributes filters act on.
 * Enrichment happens where the card/agent data lives (WorkflowGraph), keeping
 * the filter function itself pure and unit-testable.
 */
export interface WorkflowFilterableNode extends WorkflowNodeLayout {
  /** Display title used by the text filter (card title, or label for virtual
   *  nodes). */
  title: string
  /** Card status of task/reality nodes. */
  status?: string
  /** Resolved owner agent kind of task/reality nodes; undefined = unowned. */
  ownerKind?: string
  /** ISO timestamp of the node's last activity (task `modified`, or the
   *  workflow's `workflowLastActivity` for map/sentinel nodes). */
  lastActivity?: string
  /** Immediate structural ancestors (by cardId): task → its workflow's start
   *  sentinel + map root; sentinel/map root → category band (via archived
   *  bucket where present); band → epic root. Derived from the layout's
   *  tree/start/flow edges plus the map-of-task index. */
  parentIds: string[]
}

/** A filterable node with the `visible` flag applied. */
export type FilteredWorkflowNode = WorkflowFilterableNode & { visible: boolean }

/**
 * Collect all descendants of a node (exclusive — does not include the node
 * itself). Walks the reverse-parent map: children are nodes whose parentIds
 * contain this node's id. Uses a visited set for cycle safety.
 */
function collectDescendantsByParentMap(
  nodeId: string,
  nodes: WorkflowFilterableNode[],
): Set<string> {
  const result = new Set<string>()
  const childMap = new Map<string, Set<string>>()
  for (const n of nodes) {
    for (const pid of n.parentIds) {
      if (!childMap.has(pid)) childMap.set(pid, new Set())
      childMap.get(pid)!.add(n.cardId)
    }
  }
  const stack = [...(childMap.get(nodeId) ?? [])]
  while (stack.length > 0) {
    const id = stack.pop()!
    if (result.has(id)) continue
    result.add(id)
    stack.push(...(childMap.get(id) ?? []))
  }
  return result
}

/**
 * Apply workflow filters to the built node tree.
 *
 * Filtering is workflow-root-granular: a workflow (map root + sentinel) is
 * visible if at least one of its task nodes matches all active filter
 * dimensions. When a workflow root is visible, its entire subtree (sentinel,
 * all task/reality nodes) is visible — individual tasks are NOT filtered out.
 * When a workflow root is hidden, its entire subtree disappears.
 *
 * Semantics per dimension (all optional — empty/`all` filters are no-ops):
 *  - Status: a workflow is visible if at least one task has a status in
 *    `statusFilter`.
 *  - Category: category band nodes not in `categoryFilter` — and their whole
 *    subtree — are hidden (applied before workflow matching).
 *  - Owner kind: a workflow is visible if at least one task's owner agent
 *    kind is in `ownerKindFilter` (or is unowned and `(none)` is selected).
 *  - Text: a workflow is visible if any node in its subtree matches the text
 *    filter (substring or `/regexp/`). Non-workflow nodes (category, bucket,
 *    epic) match on their own title.
 *  - Time: a workflow is visible if any node in its subtree has a
 *    `lastActivity` within the window. Non-workflow nodes without a timestamp
 *    are always kept (conservative).
 *
 * Pure and returning a fresh node list — the caller drives the vis-network
 * DataSet and DOM overlays from the `visible` flags.
 */
export function applyWorkflowFilters(
  nodes: WorkflowFilterableNode[],
  filterState: WorkflowFilterState,
): FilteredWorkflowNode[] {
  const byId = new Map(nodes.map(n => [n.cardId, n]))
  const visible = new Set<string>()
  const textValue = filterState.textFilter.trim()
  const { lower, upper } = workflowTimeBounds(filterState.timeFilter)
  const timeActive = lower > 0 || upper > 0

  const hasActiveFilters =
    filterState.statusFilter.size > 0 ||
    filterState.ownerKindFilter.size > 0 ||
    textValue !== '' ||
    timeActive ||
    filterState.categoryFilter.size > 0

  // If no filters are active, everything is visible.
  if (!hasActiveFilters) {
    return nodes.map(node => ({ ...node, visible: true }))
  }

  // Helper: does a single task node pass all non-structural filter dimensions?
  function taskMatches(node: WorkflowFilterableNode): boolean {
    if (filterState.statusFilter.size > 0 && !(node.status && filterState.statusFilter.has(node.status))) return false
    if (filterState.ownerKindFilter.size > 0) {
      const selected = node.ownerKind
        ? filterState.ownerKindFilter.has(node.ownerKind)
        : filterState.ownerKindFilter.has(NONE_OWNER_KIND)
      if (!selected) return false
    }
    if (textValue !== '' && !matchWorkflowTitle(node.title, textValue)) return false
    if (timeActive && node.lastActivity) {
      const ms = Date.parse(node.lastActivity)
      if (Number.isFinite(ms) && ((lower > 0 && ms < lower) || (upper > 0 && ms > upper))) return false
    }
    return true
  }

  // Helper: does any node in a subtree match text/time (for non-status/non-owner
  // dimensions, which can match any node kind, not just tasks)?
  function subtreeMatchesTextTime(subtreeIds: Set<string>): boolean {
    if (textValue === '' && !timeActive) return true
    for (const id of subtreeIds) {
      const n = byId.get(id)
      if (!n) continue
      if (textValue !== '' && matchWorkflowTitle(n.title, textValue)) return true
      if (timeActive && n.lastActivity) {
        const ms = Date.parse(n.lastActivity)
        if (Number.isFinite(ms) && (lower === 0 || ms >= lower) && (upper === 0 || ms <= upper)) return true
      }
    }
    // No timestamp → conservative keep when only time is active
    if (textValue === '' && timeActive) {
      for (const id of subtreeIds) {
        const n = byId.get(id)
        if (n && !n.lastActivity) return true
      }
    }
    return false
  }

  // Pass 1 — excluded category bands. Computed early so workflows under
  // excluded bands are filtered out.
  let excludedBandIds: Set<string> | null = null
  if (filterState.categoryFilter.size > 0) {
    excludedBandIds = new Set<string>()
    for (const node of nodes) {
      if (node.kind === 'category' && !filterState.categoryFilter.has(node.label ?? '')) excludedBandIds.add(node.cardId)
    }
  }

  // Check whether a node has an excluded category band ancestor.
  function hasExcludedBandAncestor(node: WorkflowFilterableNode): boolean {
    if (!excludedBandIds || excludedBandIds.size === 0) return false
    const seen = new Set<string>()
    const stack = [node.cardId]
    while (stack.length > 0) {
      const id = stack.pop()!
      if (seen.has(id)) continue
      seen.add(id)
      if (excludedBandIds!.has(id)) return true
      const parent = byId.get(id)
      if (parent) stack.push(...parent.parentIds)
    }
    return false
  }

  // Pass 2 — workflow-root-granular filtering.
  // For each workflow root node, collect its entire subtree (sentinel + tasks).
  // The workflow is visible if:
  //   - it is not under an excluded category band, AND
  //   - at least one task in its subtree matches all active filter dimensions.
  // When a workflow is visible, its entire subtree is visible.
  const hasTaskFilters = filterState.statusFilter.size > 0 || filterState.ownerKindFilter.size > 0

  for (const node of nodes) {
    if (node.kind !== 'root') continue
    if (hasExcludedBandAncestor(node)) continue

    // Collect this workflow's subtree: sentinel + all task/reality descendants.
    const subtreeIds = collectDescendantsByParentMap(node.cardId, nodes)
    subtreeIds.add(node.cardId)

    // Check if any task in the subtree matches the task-level filters.
    if (hasTaskFilters) {
      let anyTaskMatches = false
      for (const id of subtreeIds) {
        const n = byId.get(id)
        if (n && (n.kind === 'task' || n.kind === 'reality') && taskMatches(n)) {
          anyTaskMatches = true
          break
        }
      }
      if (!anyTaskMatches) continue
    }

    // Check text/time: any node in the subtree (including root/sentinel) can
    // match. When only text/time is active (no status/owner), we still need
    // at least one node to match.
    if (textValue !== '' || timeActive) {
      if (!subtreeMatchesTextTime(subtreeIds)) continue
    }

    // Workflow root passed all filters — make its entire subtree visible.
    visible.add(node.cardId)
    for (const id of subtreeIds) visible.add(id)
  }

  // Pass 3 — non-workflow structural nodes (epic root, category bands, buckets).
  // These are visible if not excluded by category filter and (when text/time
  // is active) they match the text filter themselves. Category bands and epic
  // root have no timestamps, so time filter conservatively keeps them.
  for (const node of nodes) {
    if (node.kind === 'root' || node.kind === 'sentinel' || node.kind === 'task' || node.kind === 'reality') continue
    if (hasExcludedBandAncestor(node)) continue
    if (node.kind === 'category' && excludedBandIds?.has(node.cardId)) continue
    if (textValue !== '' && !matchWorkflowTitle(node.title, textValue)) continue
    if (timeActive && node.lastActivity) {
      const ms = Date.parse(node.lastActivity)
      if (Number.isFinite(ms) && ((lower > 0 && ms < lower) || (upper > 0 && ms > upper))) continue
    }
    visible.add(node.cardId)
  }

  // Pass 4 — ancestor preservation (bottom-up): every visible node keeps its
  // ancestors visible so a visible workflow renders under its category band
  // and epic root.
  for (const id of [...visible]) {
    const stack = [...(byId.get(id)?.parentIds ?? [])]
    while (stack.length > 0) {
      const pid = stack.pop()!
      if (visible.has(pid)) continue
      visible.add(pid)
      const parent = byId.get(pid)
      if (parent) stack.push(...parent.parentIds)
    }
  }

  return nodes.map(node => ({ ...node, visible: visible.has(node.cardId) }))
}