import { describe, expect, it } from 'vitest'
import {
  NONE_OWNER_KIND,
  applyWorkflowFilters,
  matchWorkflowTitle,
  workflowFiltersStore,
  workflowTimeBounds,
} from './workflowFiltersStore'
import type { TimeFilterState } from './TopologyModeView'
import type { WorkflowFilterableNode, WorkflowFilterState } from './workflowFiltersStore'

function node(
  cardId: string,
  kind: WorkflowFilterableNode['kind'],
  extra: Partial<WorkflowFilterableNode> = {},
): WorkflowFilterableNode {
  return {
    cardId,
    kind,
    title: cardId,
    x: 0,
    y: 0,
    width: 100,
    height: 60,
    parentIds: [],
    ...extra,
  }
}

/**
 * A minimal epic tree:
 *   epic root ── in-progress band ── wf-1 (sentinel + map) ── t-1, t-2
 *            └─ completed band ───── wf-2 (sentinel + map) ── t-3
 */
function tree(overrides: Record<string, Partial<WorkflowFilterableNode>> = {}): WorkflowFilterableNode[] {
  const epic = '__epic_root__'
  const inProg = '__epic_cat__:in-progress'
  const completed = '__epic_cat__:completed'
  const nodes: WorkflowFilterableNode[] = [
    node(epic, 'epic', { title: 'Epic' }),
    node(inProg, 'category', { label: 'in-progress', parentIds: [epic] }),
    node(completed, 'category', { label: 'completed', parentIds: [epic] }),
    node('wf-1:start', 'sentinel', { title: 'wf-1', parentIds: [inProg], lastActivity: '2026-08-15T10:00:00Z' }),
    node('wf-1', 'root', { title: 'wf-1', parentIds: [inProg], lastActivity: '2026-08-15T10:00:00Z' }),
    node('t-1', 'task', { parentIds: ['wf-1', 'wf-1:start'], status: 'doing', ownerKind: 'worker', lastActivity: '2026-08-15T09:00:00Z' }),
    node('t-2', 'task', { parentIds: ['wf-1', 'wf-1:start'], status: 'todo', lastActivity: '2026-08-02T09:00:00Z' }),
    node('wf-2:start', 'sentinel', { title: 'wf-2', parentIds: [completed], lastActivity: '2026-07-01T10:00:00Z' }),
    node('wf-2', 'root', { title: 'wf-2', parentIds: [completed], lastActivity: '2026-07-01T10:00:00Z' }),
    node('t-3', 'task', { parentIds: ['wf-2', 'wf-2:start'], status: 'done', ownerKind: 'reviewer', lastActivity: '2026-07-01T09:00:00Z' }),
  ]
  return nodes.map(n => ({ ...n, ...(overrides[n.cardId] ?? {}) }))
}

function stateOf(partial: Partial<WorkflowFilterState>): WorkflowFilterState {
  return {
    statusFilter: new Set(),
    categoryFilter: new Set(),
    ownerKindFilter: new Set(),
    textFilter: '',
    timeFilter: { mode: 'all', agoValue: '7', agoUnit: 'days', agoDirection: 'after', rangeFrom: '', rangeTo: '' },
    ...partial,
  }
}

function visibleIds(nodes: ReturnType<typeof applyWorkflowFilters>): string[] {
  return nodes.filter(n => n.visible).map(n => n.cardId)
}

describe('workflowFiltersStore', () => {
  it('starts fully inactive', () => {
    expect(workflowFiltersStore.activeCount()).toBe(0)
    expect(workflowFiltersStore.getState().statusFilter.size).toBe(0)
  })

  it('setters replace slices immutably and notify subscribers once each', () => {
    let calls = 0
    const unsub = workflowFiltersStore.subscribe(() => { calls++ })
    workflowFiltersStore.setStatusFilter(new Set(['doing']))
    workflowFiltersStore.setCategoryFilter(new Set(['in-progress']))
    workflowFiltersStore.setOwnerKindFilter(new Set([NONE_OWNER_KIND]))
    workflowFiltersStore.setTextFilter('wf')
    workflowFiltersStore.setTimeFilter({ mode: 'range', agoValue: '7', agoUnit: 'days', agoDirection: 'after', rangeFrom: '2026-08-01', rangeTo: '2026-08-31' })
    expect(calls).toBe(5)
    const s = workflowFiltersStore.getState()
    expect([...s.statusFilter]).toEqual(['doing'])
    expect([...s.categoryFilter]).toEqual(['in-progress'])
    expect([...s.ownerKindFilter]).toEqual([NONE_OWNER_KIND])
    expect(s.textFilter).toBe('wf')
    expect(s.timeFilter.mode).toBe('range')
    unsub()

    workflowFiltersStore.clearAll()
    const cleared = workflowFiltersStore.getState()
    expect(cleared.statusFilter.size).toBe(0)
    expect(cleared.categoryFilter.size).toBe(0)
    expect(cleared.ownerKindFilter.size).toBe(0)
    expect(cleared.textFilter).toBe('')
    expect(cleared.timeFilter.mode).toBe('all')
  })

  it('copies sets so late caller mutation cannot leak into the store', () => {
    const set = new Set(['doing'])
    workflowFiltersStore.setStatusFilter(set)
    set.add('stooge')
    expect([...workflowFiltersStore.getState().statusFilter]).toEqual(['doing'])
    workflowFiltersStore.clearAll()
  })

  it('activeCount counts each active dimension once', () => {
    workflowFiltersStore.clearAll()
    expect(workflowFiltersStore.activeCount()).toBe(0)
    workflowFiltersStore.setStatusFilter(new Set(['doing']))
    expect(workflowFiltersStore.activeCount()).toBe(1)
    workflowFiltersStore.setTextFilter('  ')
    expect(workflowFiltersStore.activeCount()).toBe(1)
    workflowFiltersStore.setTextFilter('wf')
    expect(workflowFiltersStore.activeCount()).toBe(2)
    // A time filter with a mode but no usable bound is not active.
    workflowFiltersStore.setTimeFilter({ mode: 'ago', agoValue: '', agoUnit: 'days', agoDirection: 'after', rangeFrom: '', rangeTo: '' })
    expect(workflowFiltersStore.activeCount()).toBe(2)
    workflowFiltersStore.setTimeFilter({ mode: 'range', agoValue: '7', agoUnit: 'days', agoDirection: 'after', rangeFrom: '2026-08-01', rangeTo: '' })
    expect(workflowFiltersStore.activeCount()).toBe(3)
    workflowFiltersStore.clearAll()
  })
})

describe('workflowTimeBounds', () => {
  it('returns open bounds for mode all / invalid values', () => {
    expect(workflowTimeBounds({ mode: 'all', agoValue: '', agoUnit: 'days', agoDirection: 'after', rangeFrom: '', rangeTo: '' })).toEqual({ lower: 0, upper: 0 })
    expect(workflowTimeBounds({ mode: 'ago', agoValue: 'abc', agoUnit: 'days', agoDirection: 'after', rangeFrom: '', rangeTo: '' })).toEqual({ lower: 0, upper: 0 })
  })

  it('resolves ago direction into lower or upper cutoff', () => {
    const after = workflowTimeBounds({ mode: 'ago', agoValue: '1', agoUnit: 'days', agoDirection: 'after', rangeFrom: '', rangeTo: '' })
    expect(after.lower).toBeGreaterThan(0)
    expect(after.upper).toBe(0)
    const before = workflowTimeBounds({ mode: 'ago', agoValue: '1', agoUnit: 'hours', agoDirection: 'before', rangeFrom: '', rangeTo: '' })
    expect(before.lower).toBe(0)
    expect(before.upper).toBeGreaterThan(0)
  })

  it('resolves a date range into a closed window', () => {
    const { lower, upper } = workflowTimeBounds({ mode: 'range', agoValue: '7', agoUnit: 'days', agoDirection: 'after', rangeFrom: '2026-08-01', rangeTo: '2026-08-31' })
    expect(lower).toBe(new Date('2026-08-01T00:00:00').getTime())
    expect(upper).toBe(new Date('2026-08-31T23:59:59').getTime())
  })
})

describe('matchWorkflowTitle', () => {
  it('matches substrings case-insensitively', () => {
    expect(matchWorkflowTitle('Build the thing', 'THE')).toBe(true)
    expect(matchWorkflowTitle('Build the thing', 'missing')).toBe(false)
  })

  it('supports /regexp/ with flags', () => {
    expect(matchWorkflowTitle('wf-1 publish', '/^wf-\\d+$/')).toBe(false)
    expect(matchWorkflowTitle('wf-1', '/^wf-\\d+$/')).toBe(true)
    expect(matchWorkflowTitle('WF-1', '/^wf-\\d+$/i')).toBe(true)
  })

  it('never matches on an invalid regexp', () => {
    expect(matchWorkflowTitle('anything', '/[/')).toBe(false)
  })
})

describe('applyWorkflowFilters', () => {
  it('keeps every node visible when no filter is active', () => {
    const filtered = applyWorkflowFilters(tree(), stateOf({}))
    expect(filtered.every(n => n.visible)).toBe(true)
    expect(filtered.length).toBe(10)
  })

  it('status filter keeps a workflow whose any task matches and shows its whole subtree', () => {
    const filtered = applyWorkflowFilters(tree(), stateOf({ statusFilter: new Set(['doing']) }))
    const ids = visibleIds(filtered)
    // wf-1 has t-1 (doing) — entire subtree visible.
    expect(ids).toContain('t-1')
    expect(ids).toContain('t-2') // entire subtree visible, even non-matching tasks
    expect(ids).toContain('wf-1')
    expect(ids).toContain('wf-1:start')
    // wf-2 has only t-3 (done) — not visible.
    expect(ids).not.toContain('t-3')
    expect(ids).not.toContain('wf-2')
    expect(ids).not.toContain('wf-2:start')
    // Ancestor chain of the surviving workflow stays visible.
    expect(ids).toContain('__epic_cat__:in-progress')
    expect(ids).toContain('__epic_root__')
  })

  it('status filter hides a workflow with no matching task', () => {
    const filtered = applyWorkflowFilters(tree(), stateOf({ statusFilter: new Set(['todo']) }))
    const ids = visibleIds(filtered)
    // wf-1 has t-2 (todo) — entire subtree visible.
    expect(ids).toContain('wf-1')
    expect(ids).toContain('t-1')
    expect(ids).toContain('t-2')
    // wf-2 has only t-3 (done) — not visible.
    expect(ids).not.toContain('wf-2')
    expect(ids).not.toContain('t-3')
  })

  it('status filter hides a workflow when its only matching task has no status', () => {
    const override = { 't-1': { status: undefined } }
    const filtered = applyWorkflowFilters(tree(override), stateOf({ statusFilter: new Set(['doing']) }))
    const ids = visibleIds(filtered)
    // wf-1 has t-1 (no status) + t-2 (todo) — neither matches 'doing'.
    expect(ids).not.toContain('t-1')
    expect(ids).not.toContain('wf-1')
  })

  it('category filter hides non-selected bands and their whole subtree', () => {
    const filtered = applyWorkflowFilters(tree(), stateOf({ categoryFilter: new Set(['in-progress']) }))
    const ids = visibleIds(filtered)
    expect(ids).toContain('__epic_cat__:in-progress')
    expect(ids).toContain('t-1')
    expect(ids).not.toContain('__epic_cat__:completed')
    expect(ids).not.toContain('wf-2')
    expect(ids).not.toContain('wf-2:start')
    expect(ids).not.toContain('t-3')
  })

  it('an excluded band cannot be resurrected by a matching descendant', () => {
    // t-3 matches the status filter but sits under the excluded completed band.
    const filtered = applyWorkflowFilters(tree(), stateOf({ categoryFilter: new Set(['in-progress']), statusFilter: new Set(['done']) }))
    const ids = visibleIds(filtered)
    expect(ids).not.toContain('t-3')
    expect(ids).not.toContain('__epic_cat__:completed')
  })

  it('owner-kind filter keeps a workflow whose any task has a matching kind', () => {
    const filtered = applyWorkflowFilters(tree(), stateOf({ ownerKindFilter: new Set(['worker']) }))
    const ids = visibleIds(filtered)
    // wf-1 has t-1 (worker) — entire subtree visible, including t-2 (unowned).
    expect(ids).toContain('t-1')
    expect(ids).toContain('t-2')
    expect(ids).toContain('wf-1')
    // wf-2 has t-3 (reviewer) — not visible.
    expect(ids).not.toContain('t-3')
    expect(ids).not.toContain('wf-2')
  })

  it('owner-kind filter with (none) selected keeps a workflow with unowned tasks', () => {
    const filtered = applyWorkflowFilters(tree(), stateOf({ ownerKindFilter: new Set([NONE_OWNER_KIND]) }))
    const ids = visibleIds(filtered)
    // wf-1 has t-2 (unowned) — entire subtree visible.
    expect(ids).toContain('t-2')
    expect(ids).toContain('t-1')
    expect(ids).toContain('wf-1')
    // wf-2 has only t-3 (reviewer) — not visible.
    expect(ids).not.toContain('t-3')
    expect(ids).not.toContain('wf-2')
  })

  it('text filter keeps a workflow when any node in its subtree matches', () => {
    const sub = applyWorkflowFilters(tree(), stateOf({ textFilter: 'WF-1' }))
    const subIds = visibleIds(sub)
    // wf-1 root matches — entire subtree visible.
    expect(subIds).toContain('wf-1')
    expect(subIds).toContain('wf-1:start')
    expect(subIds).toContain('t-1')
    expect(subIds).toContain('t-2')
    expect(subIds).not.toContain('wf-2')
    expect(subIds).not.toContain('t-3')

    const re = applyWorkflowFilters(tree(), stateOf({ textFilter: '/^wf-2$/' }))
    const reIds = visibleIds(re)
    expect(reIds).toContain('wf-2')
    expect(reIds).toContain('t-3')
    expect(reIds).not.toContain('wf-1')
  })

  it('time filter keeps a workflow whose any task is in range, showing its whole subtree', () => {
    const range: TimeFilterState = { mode: 'range', agoValue: '7', agoUnit: 'days', agoDirection: 'after', rangeFrom: '2026-08-01', rangeTo: '2026-08-31' }
    const filtered = applyWorkflowFilters(tree(), stateOf({ timeFilter: range }))
    const ids = visibleIds(filtered)
    // wf-1 has t-1 and t-2 in range — entire subtree visible.
    expect(ids).toContain('t-1')
    expect(ids).toContain('t-2')
    expect(ids).toContain('wf-1')
    expect(ids).toContain('wf-1:start')
    // wf-2 is out of range — not visible.
    expect(ids).not.toContain('t-3')
    expect(ids).not.toContain('wf-2')
    expect(ids).not.toContain('wf-2:start')
    // Untimestamped nodes (epic root, bands) are always visible.
    expect(ids).toContain('__epic_root__')
    expect(ids).toContain('__epic_cat__:in-progress')
    expect(ids).toContain('__epic_cat__:completed')
  })

  it('a workflow root with out-of-range sentinel but in-range task stays visible', () => {
    // The sentinel's own lastActivity is out of range, but its visible task
    // (in range) keeps the whole workflow visible.
    const overrides = { 'wf-1:start': { lastActivity: '2020-01-01T00:00:00Z' } }
    const range: TimeFilterState = { mode: 'range', agoValue: '7', agoUnit: 'days', agoDirection: 'after', rangeFrom: '2026-08-01', rangeTo: '2026-08-31' }
    const filtered = applyWorkflowFilters(tree(overrides), stateOf({ timeFilter: range }))
    const ids = visibleIds(filtered)
    expect(ids).toContain('t-1')
    expect(ids).toContain('wf-1:start')
    expect(ids).toContain('wf-1')
    expect(ids).toContain('__epic_cat__:in-progress')
    expect(ids).toContain('__epic_root__')
  })

  it('tolerates parent cycles without hanging', () => {
    const cyclic = tree({ 't-1': { parentIds: ['wf-1', 't-1'] } })
    const filtered = applyWorkflowFilters(cyclic, stateOf({ statusFilter: new Set(['doing']) }))
    expect(visibleIds(filtered)).toContain('t-1')
  })
})