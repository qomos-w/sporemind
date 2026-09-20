import { describe, expect, it } from 'vitest'
import type { MonoCardListItem } from '../../../domain/mono-types'
import {
  archivedBucketIdOf,
  buildWorkflowLayout,
  buildEpicLayout,
  buildArchivedBuckets,
  buildPlannedSchedulerGroups,
  classifyWorkflowCategory,
  schedulerOfInstance,
  workflowTaskIds,
  workflowParentIndexBuildCount,
  workflowOwnerAgentId,
  workflowMapDestination,
  workflowLastActivity,
  routeWorkflowEdge,
  dependsOnEdgeColor,
  epicWorkflowPlacement,
  categoryContents,
  bucketWorkflowMapIds,
  visibleWorkflowMapIds,
  DEPENDS_ON_DONE_COLOR,
  DEPENDS_ON_PENDING_COLOR,
  WORKFLOW_CARD_W,
  WORKFLOW_TASK_H,
  WORKFLOW_ROOT_H,
  EPIC_ROOT_ID,
  EPIC_CATEGORY_ORDER,
  EPIC_BUCKET_PREFIX,
  epicCategoryId,
  locateMapIdForCard,
} from './workflowLayout'
import type { WorkflowNodeLayout, WorkflowRoutePoint, EpicCategory } from './workflowLayout'

function card(id: string, over: Partial<MonoCardListItem> = {}): MonoCardListItem {
  return {
    id,
    type: 'wiki',
    source: 'project',
    storage: 'cardstore',
    visibility: 'wiki',
    protected: false,
    editable: true,
    deletable: true,
    tags: [],
    list: [],
    // Default to "now" so completed workflows are never auto-archived
    // (>7d inactivity) unless a test sets an explicit timestamp.
    modified: new Date().toISOString(),
    ...over,
  }
}

function mapCard(id: string, include: string[], ownerAgentId?: string, destination?: string, status?: string): MonoCardListItem {
  const data: Record<string, unknown> = { scope: { include } }
  if (ownerAgentId) data.ownerAgentId = ownerAgentId
  if (destination !== undefined) data.Destination = destination
  return card(id, { type: 'workflow', data, status })
}

function taskNode(id: string, x: number, y: number): WorkflowNodeLayout {
  return { cardId: id, kind: 'task', x, y, width: WORKFLOW_CARD_W, height: WORKFLOW_TASK_H }
}

function isOrthogonal(points: WorkflowRoutePoint[]): boolean {
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1]!
    const b = points[i]!
    if (a.x !== b.x && a.y !== b.y) return false
  }
  return true
}

/** True when the axis-aligned segment a->b intersects the rect (no inflation). */
function segmentHitsRect(a: WorkflowRoutePoint, b: WorkflowRoutePoint, r: { left: number; top: number; right: number; bottom: number }): boolean {
  if (a.x === b.x) {
    return a.x >= r.left && a.x <= r.right && Math.min(a.y, b.y) <= r.bottom && Math.max(a.y, b.y) >= r.top
  }
  if (a.y === b.y) {
    return a.y >= r.top && a.y <= r.bottom && Math.min(a.x, b.x) <= r.right && Math.max(a.x, b.x) >= r.left
  }
  return false
}

describe('workflowTaskIds', () => {
  it('reads data.scope.include and falls back to parent linkage', () => {
    const m = mapCard('root', ['a', 'b'])
    const orphan = card('c', { type: 'task', parent: 'root' })
    expect(workflowTaskIds(m, [m, orphan])).toEqual(['a', 'b', 'c'])
  })

  it('archivedBucketIdOf returns the containing bucket for an archived workflow', () => {
    const now = Date.parse('2026-08-25T00:00:00Z')
    const old = mapCard('old-wf', ['t1'], undefined, undefined, 'done')
    old.modified = '2026-08-14T12:00:00Z'
    const oldTask = card('t1', { type: 'task', modified: '2026-08-14T12:00:00Z' })
    const fresh = mapCard('fresh-wf', ['t2'])
    fresh.modified = '2026-08-24T00:00:00Z'
    const cards = [old, oldTask, fresh]
    expect(archivedBucketIdOf(cards, 'old-wf', { now })).toBe('__epic_bucket__:date:2026-08-14')
    expect(archivedBucketIdOf(cards, 'fresh-wf', { now })).toBeNull()
    expect(archivedBucketIdOf(cards, 'missing', { now })).toBeNull()
  })

  it('reads owner agent id from the data block', () => {
    expect(workflowOwnerAgentId(mapCard('root', [], 'agent-1'))).toBe('agent-1')
    expect(workflowOwnerAgentId(mapCard('root', []))).toBeUndefined()
  })

  it('workflowLastActivity picks the newest modified across map and tasks', () => {
    const m = mapCard('root', ['a', 'b'])
    m.modified = '2026-08-10T00:00:00Z'
    const a = card('a', { type: 'task', modified: '2026-08-12T08:00:00Z' })
    const b = card('b', { type: 'task', modified: '2026-08-11T00:00:00Z' })
    expect(workflowLastActivity(m, [m, a, b])).toBe('2026-08-12T08:00:00Z')
  })

  it('workflowLastActivity falls back to the map modified when tasks are older or missing', () => {
    const m = mapCard('root', ['a'])
    m.modified = '2026-08-10T00:00:00Z'
    const a = card('a', { type: 'task', modified: '2026-08-09T00:00:00Z' })
    expect(workflowLastActivity(m, [m, a])).toBe('2026-08-10T00:00:00Z')
    expect(workflowLastActivity(m, [m])).toBe('2026-08-10T00:00:00Z')
  })
})

describe('buildWorkflowLayout', () => {
  it('returns an empty layout when there are no map cards', () => {
    const layout = buildWorkflowLayout([card('n1')])
    expect(layout.nodes).toEqual([])
    expect(layout.edges).toEqual([])
    expect(layout.width).toBe(0)
    expect(layout.direction).toBe('LTR')
  })

  it('connects the virtual start directly to an otherwise empty workflow map', () => {
    const layout = buildWorkflowLayout([mapCard('root', [])])

    expect(layout.nodes).toHaveLength(2)
    expect(layout.edges).toEqual([{ from: 'root:start', to: 'root', kind: 'start' }])
  })

  it('uses a virtual start and the real workflow map target with task-card dependencies', () => {
    const cards = [
      mapCard('root', ['a', 'b', 'c'], 'agent-1', 'Reach goal'),
      card('a', { type: 'task', status: 'doing', data: { depends_on: ['b'] } }),
      card('b', { type: 'task', data: { depends_on: ['c'] } }),
      card('c', { type: 'task' }),
    ]
    const layout = buildWorkflowLayout(cards)
    const xOf = (id: string) => layout.nodes.find(n => n.cardId === id)!.x
    const startNode = layout.nodes.find(n => n.cardId === 'root:start')!
    const rootNode = layout.nodes.find(n => n.cardId === 'root')!

    expect(startNode.kind).toBe('sentinel')
    expect(startNode.sentinelRole).toBe('start')
    expect(startNode.label).toBeUndefined()
    expect(rootNode.kind).toBe('root')
    expect(xOf('c')).toBeGreaterThan(xOf('root:start'))
    expect(xOf('b')).toBeGreaterThan(xOf('c'))
    expect(xOf('a')).toBeGreaterThan(xOf('b'))
    expect(xOf('root')).toBeGreaterThan(xOf('a'))
    expect(layout.edges).toContainEqual({ from: 'root:start', to: 'c', kind: 'start' })
    expect(layout.edges).toContainEqual({ from: 'c', to: 'b', kind: 'depends_on' })
    expect(layout.edges).toContainEqual({ from: 'b', to: 'a', kind: 'depends_on' })
    expect(layout.edges).toContainEqual({ from: 'a', to: 'root', kind: 'flow' })
  })

  it('ignores task-card dependencies that cross map boundaries or reference unknown cards', () => {
    const cards = [
      mapCard('r1', ['t1']),
      mapCard('r2', ['t2']),
      card('t1', { type: 'task', data: { depends_on: ['t2', 'ghost'] } }),
      card('t2', { type: 'task' }),
    ]
    const layout = buildWorkflowLayout(cards)
    expect(layout.edges.filter(e => e.kind === 'depends_on')).toEqual([])
    expect(layout.edges).not.toContainEqual({ from: 'r1:start', to: 't1', kind: 'start' })
    expect(layout.edges).toContainEqual({ from: 'r2:start', to: 't2', kind: 'start' })
  })

  it('survives dependency cycles without hanging', () => {
    const cards = [
      mapCard('root', ['a', 'b']),
      card('a', { type: 'task', data: { depends_on: ['b'] } }),
      card('b', { type: 'task', data: { depends_on: ['a'] } }),
    ]
    const layout = buildWorkflowLayout(cards)
    // 2 real tasks + root map + virtual start.
    expect(layout.nodes).toHaveLength(4)
    expect(Number.isFinite(layout.height)).toBe(true)
  })

  it('stacks multiple workflows vertically without overlap', () => {
    const cards = [
      mapCard('r1', ['a']),
      mapCard('r2', ['b', 'c']),
      card('a', { type: 'task' }),
      card('b', { type: 'task' }),
      card('c', { type: 'task' }),
    ]
    const layout = buildWorkflowLayout(cards)
    const yOf = (id: string) => layout.nodes.find(n => n.cardId === id)!.y
    expect(yOf('r2:start')).toBeGreaterThan(yOf('a'))
    expect(layout.width).toBeGreaterThan(WORKFLOW_CARD_W)
  })

  it('renders a tagged reality task as a real terminal node', () => {
    const cards = [
      mapCard('root', ['a', 'reality'], undefined, 'Reach goal'),
      card('a', { type: 'task' }),
      card('reality', { type: 'task', tags: ['root', 'reality'], data: { depends_on: ['a'] } }),
    ]
    const layout = buildWorkflowLayout(cards)
    expect(layout.nodes.find(n => n.cardId === 'reality')?.kind).toBe('reality')
    expect(layout.edges).toContainEqual({ from: 'a', to: 'reality', kind: 'depends_on' })
    expect(layout.edges).toContainEqual({ from: 'reality', to: 'root', kind: 'flow' })
    expect(workflowMapDestination(cards[0]!)).toBe('Reach goal')
  })

  it('connects terminal tasks to the real workflow map regardless of map status', () => {
    const cards = [
      mapCard('root', ['a', 'b', 'c', 'd'], undefined, undefined, 'doing'),
      card('a', { type: 'task', data: { depends_on: ['b'] } }),
      card('b', { type: 'task', data: { depends_on: ['c'] } }),
      card('c', { type: 'task' }),
      card('d', { type: 'task' }),
    ]
    const layout = buildWorkflowLayout(cards)
    expect(layout.edges).toContainEqual({ from: 'a', to: 'root', kind: 'flow' })
    expect(layout.edges).not.toContainEqual({ from: 'b', to: 'root', kind: 'flow' })
    expect(layout.edges).not.toContainEqual({ from: 'c', to: 'root', kind: 'flow' })
    expect(layout.edges).not.toContainEqual({ from: 'd', to: 'root', kind: 'flow' })
  })

  it('uses compact size for tasks and start, with root height reserved for the map card', () => {
    const cards = [mapCard('root', ['a']), card('a', { type: 'task' })]
    const layout = buildWorkflowLayout(cards)
    for (const node of layout.nodes) {
      if (node.sentinelRole === 'start') {
        // Start node is card-sized, same as task cards.
        expect(node.width).toBe(WORKFLOW_CARD_W)
        expect(node.height).toBe(WORKFLOW_TASK_H)
      } else {
        expect(node.width).toBe(WORKFLOW_CARD_W)
        expect(node.height).toBe(node.kind === 'root' ? 72 : WORKFLOW_TASK_H)
      }
    }
  })

  it('lays out top-to-bottom when direction TTB is requested', () => {
    const cards = [
      mapCard('root', ['a', 'b', 'c'], undefined, 'Reach goal'),
      card('a', { type: 'task' }),
      card('b', { type: 'task' }),
      card('c', { type: 'task' }),
    ]
    cards[1]!.data = { depends_on: ['b'] }
    cards[2]!.data = { depends_on: ['c'] }
    const layout = buildWorkflowLayout(cards, { direction: 'TTB' })
    const yOf = (id: string) => layout.nodes.find(n => n.cardId === id)!.y

    expect(layout.direction).toBe('TTB')
    // depth increases downward: start above c above b above a above the map.
    expect(yOf('root:start')).toBeLessThan(yOf('c'))
    expect(yOf('c')).toBeLessThan(yOf('b'))
    expect(yOf('b')).toBeLessThan(yOf('a'))
    expect(yOf('a')).toBeLessThan(yOf('root'))
    // rows are one card-height + gap apart
    expect(yOf('c') - yOf('root:start')).toBe(yOf('b') - yOf('c'))

    // same edge semantics as LTR
    expect(layout.edges).toContainEqual({ from: 'root:start', to: 'c', kind: 'start' })
    expect(layout.edges).not.toContainEqual({ from: 'root:start', to: 'a', kind: 'start' })
    expect(layout.edges).toContainEqual({ from: 'c', to: 'b', kind: 'depends_on' })
    expect(layout.edges).toContainEqual({ from: 'b', to: 'a', kind: 'depends_on' })
  })

  it('stacks multiple workflow maps without overlap in TTB (vertical depth axis)', () => {
    const cards = [
      mapCard('r1', ['a', 'b']),
      mapCard('r2', ['c', 'd', 'e']),
      card('a', { type: 'task' }),
      card('b', { type: 'task', data: { depends_on: ['a'] } }),
      card('c', { type: 'task' }),
      card('d', { type: 'task', data: { depends_on: ['c'] } }),
      card('e', { type: 'task', data: { depends_on: ['d'] } }),
    ]
    const layout = buildWorkflowLayout(cards, { direction: 'TTB' })
    const rects = layout.nodes.map(n => ({ id: n.cardId, x: n.x, y: n.y, w: n.width, h: n.height }))
    const overlaps: string[] = []
    for (let i = 0; i < rects.length; i++) {
      for (let j = i + 1; j < rects.length; j++) {
        const a = rects[i]!, b = rects[j]!
        if (a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y) {
          overlaps.push(`${a.id}<->${b.id}`)
        }
      }
    }
    expect(overlaps).toEqual([])
    // r2 sits entirely to the right of r1's group.
    const r1Right = Math.max(...layout.nodes.filter(n => n.cardId.startsWith('r1') || n.cardId === 'a' || n.cardId === 'b').map(n => n.x + n.width))
    const r2Left = Math.min(...layout.nodes.filter(n => n.cardId.startsWith('r2') || n.cardId === 'c' || n.cardId === 'd' || n.cardId === 'e').map(n => n.x))
    expect(r2Left).toBeGreaterThan(r1Right)
  })

  it('keeps exactly one grid cell between consecutive maps and never lets a root spill into the gap (LTR + TTB)', () => {
    // Map r1 has a single-task lane (the smallest footprint) so its root card,
    // the tallest on the cross axis, is the worst case for spilling into the gap.
    const cards = [
      mapCard('r1', ['a']),
      mapCard('r2', ['b', 'c', 'd']),
      card('a', { type: 'task' }),
      card('b', { type: 'task' }),
      card('c', { type: 'task' }),
      card('d', { type: 'task' }),
    ]
    const expectedGap = (d: string) => d === 'LTR' ? WORKFLOW_TASK_H + 36 : WORKFLOW_TASK_H + 96 // two grid cells (extra for routing corridors)
    for (const direction of ['LTR', 'TTB'] as const) {
      const layout = buildWorkflowLayout(cards, { direction })
      // A node belongs to a map if it is the map card, its start sentinel, or one of its tasks.
      const belongTo = (mapId: string) => {
        const tasks = workflowTaskIds(cards.find(c => c.id === mapId)!, cards)
        return layout.nodes.filter(n => n.cardId === mapId || n.cardId === `${mapId}:start` || tasks.includes(n.cardId))
      }
      const r1 = belongTo('r1')
      const r2 = belongTo('r2')
      const r1End = direction === 'LTR'
        ? Math.max(...r1.map(n => n.y + n.height))
        : Math.max(...r1.map(n => n.x + n.width))
      const r2Start = direction === 'LTR'
        ? Math.min(...r2.map(n => n.y))
        : Math.min(...r2.map(n => n.x))
      // Exactly one grid cell of clearance between the two maps' footprints.
      expect(r2Start - r1End).toBe(expectedGap(direction))
      for (const n of r1) expect(direction === 'LTR' ? n.y + n.height : n.x + n.width).toBeLessThanOrEqual(r1End)
      for (const n of r2) expect(direction === 'LTR' ? n.y : n.x).toBeGreaterThanOrEqual(r2Start)
    }
  })

  it('applies a persisted offset to every node in a workflow group', () => {
    const cards = [mapCard('root', ['a']), card('a', { type: 'task' })]
    const base = buildWorkflowLayout(cards)
    const moved = buildWorkflowLayout(cards, { groupOffsets: { root: { x: 120, y: -40 } } })
    for (const node of base.nodes) {
      const shifted = moved.nodes.find(candidate => candidate.cardId === node.cardId)!
      expect(shifted.x).toBe(node.x + 120)
      expect(shifted.y).toBe(node.y - 40)
    }
  })

  it('centers TTB rows horizontally and shares one row per depth', () => {
    const cards = [
      mapCard('root', ['a', 'b']),
      card('a', { type: 'task' }),
      card('b', { type: 'task' }),
    ]
    const layout = buildWorkflowLayout(cards, { direction: 'TTB' })
    const node = (id: string) => layout.nodes.find(n => n.cardId === id)!
    const a = node('a')
    const b = node('b')
    const start = node('root:start')
    expect(a.y).toBe(b.y) // same depth row
    expect(a.x).toBeLessThan(b.x)
    // start sentinel is centered on the row's visual span
    expect(start.x + start.width / 2).toBe((a.x + a.width / 2 + b.x + b.width / 2) / 2)
  })
})

describe('routeWorkflowEdge', () => {
  it('routes LTR edges orthogonally from the right edge to the left edge', () => {
    const route = routeWorkflowEdge(taskNode('src', 0, 0), taskNode('tgt', 310, 200), [])
    expect(route.exitSide).toBe('right')
    expect(route.enterSide).toBe('left')
    expect(isOrthogonal(route.points)).toBe(true)
    // last point sits exactly on the target card border
    expect(route.points[route.points.length - 1]).toEqual({ x: 310, y: 232 })
    expect(route.points).toEqual([
      { x: 220, y: 32 },
      { x: 265, y: 32 },
      { x: 265, y: 232 },
      { x: 310, y: 232 },
    ])
  })

  it('routes TTB edges orthogonally from the bottom edge to the top edge', () => {
    const route = routeWorkflowEdge(taskNode('src', 0, 0), taskNode('tgt', 100, 200), [], 'TTB')
    expect(route.exitSide).toBe('bottom')
    expect(route.enterSide).toBe('top')
    expect(isOrthogonal(route.points)).toBe(true)
    expect(route.points[route.points.length - 1]).toEqual({ x: 210, y: 200 })
    expect(route.points).toEqual([
      { x: 110, y: 64 },
      { x: 110, y: 132 },
      { x: 210, y: 132 },
      { x: 210, y: 200 },
    ])
  })

  it('pushes the LTR crossing run around intermediate nodes so it never crosses a card', () => {
    const src = taskNode('src', 0, 0)
    const tgt = taskNode('tgt', 620, 200)
    // Two cards in the intermediate column block both preferred crossing rows
    // (source row and target row), forcing the run to the gap above them.
    const blockerA = taskNode('A', 310, 20)
    const blockerB = taskNode('B', 310, 200)
    const route = routeWorkflowEdge(src, tgt, [src, tgt, blockerA, blockerB])
    expect(isOrthogonal(route.points)).toBe(true)
    expect(route.points).toEqual([
      { x: 220, y: 32 },
      { x: 265, y: 32 },
      { x: 265, y: 7 },
      { x: 575, y: 7 },
      { x: 575, y: 232 },
      { x: 620, y: 232 },
    ])
    const rect = (n: WorkflowNodeLayout) => ({ left: n.x, top: n.y, right: n.x + n.width, bottom: n.y + n.height })
    for (const n of [blockerA, blockerB]) {
      for (let i = 1; i < route.points.length; i++) {
        expect(segmentHitsRect(route.points[i - 1]!, route.points[i]!, rect(n))).toBe(false)
      }
    }
  })

  it('routes backward cycle-residue edges around the left lane in LTR', () => {
    const route = routeWorkflowEdge(taskNode('src', 310, 200), taskNode('tgt', 0, 0), [])
    expect(route.exitSide).toBe('left')
    expect(route.enterSide).toBe('right')
    expect(isOrthogonal(route.points)).toBe(true)
    expect(route.points[0]).toEqual({ x: 310, y: 232 })
    expect(route.points[route.points.length - 1]).toEqual({ x: 220, y: 32 })
  })

  it('routes backward cycle-residue edges around the top lane in TTB', () => {
    const route = routeWorkflowEdge(taskNode('src', 0, 200), taskNode('tgt', 0, 0), [], 'TTB')
    expect(route.exitSide).toBe('top')
    expect(route.enterSide).toBe('bottom')
    expect(isOrthogonal(route.points)).toBe(true)
  })

  it('uses a side-bypass for multi-hop TTB edges with 1/2 cross-axis gap offset (matching LTR)', () => {
    // C (top) → A (bottom) with B sandwiched between them in the same column.
    // A depends on B and C; B depends on C. The C→A edge must skip B's layer.
    const c = taskNode('C', 24, 106)
    const b = taskNode('B', 24, 188)
    const a = taskNode('A', 24, 270)
    const route = routeWorkflowEdge(c, a, [c, b, a], 'TTB')
    expect(isOrthogonal(route.points)).toBe(true)
    // The bypass offset is COL_GAP/2 (45px), matching pickClearAxis so the
    // outermost line sits at 1/2 cross-axis spacing — consistent with LTR.
    const xs = route.points.map(p => p.x)
    const maxX = Math.max(...xs)
    const minX = Math.min(...xs)
    expect(maxX - minX).toBeLessThanOrEqual(2 * 45 + 1) // 2 × (COL_GAP/2), within rounding
    // No segment crosses B's rectangle.
    const rect = { left: b.x, top: b.y, right: b.x + b.width, bottom: b.y + b.height }
    for (let i = 1; i < route.points.length; i++) {
      expect(segmentHitsRect(route.points[i - 1]!, route.points[i]!, rect)).toBe(false)
    }
  })

  it('keeps the standard Z-route for multi-hop LTR edges (bypass is longer)', () => {
    // Same diamond but laid out horizontally; the standard route is shorter.
    const c = taskNode('C', 0, 0)
    const b = taskNode('B', 310, 0)
    const a = taskNode('A', 620, 0)
    const route = routeWorkflowEdge(c, a, [c, b, a])
    expect(route.exitSide).toBe('right')
    expect(route.enterSide).toBe('left')
    expect(isOrthogonal(route.points)).toBe(true)
    // No segment crosses B's rectangle.
    const rect = { left: b.x, top: b.y, right: b.x + b.width, bottom: b.y + b.height }
    for (let i = 1; i < route.points.length; i++) {
      expect(segmentHitsRect(route.points[i - 1]!, route.points[i]!, rect)).toBe(false)
    }
  })
})

describe('dependsOnEdgeColor', () => {
  it('returns green for a done target', () => {
    expect(dependsOnEdgeColor('done')).toBe(DEPENDS_ON_DONE_COLOR)
  })

  it('returns yellow for a not-started target (todo)', () => {
    expect(dependsOnEdgeColor('todo')).toBe(DEPENDS_ON_PENDING_COLOR)
  })

  it('returns yellow for a backlog target', () => {
    expect(dependsOnEdgeColor('backlog')).toBe(DEPENDS_ON_PENDING_COLOR)
  })

  it('returns yellow for an empty/undefined status (not started)', () => {
    expect(dependsOnEdgeColor(undefined)).toBe(DEPENDS_ON_PENDING_COLOR)
    expect(dependsOnEdgeColor('')).toBe(DEPENDS_ON_PENDING_COLOR)
  })

  // --- statuses that must NOT be recolored (keep existing default style) ---

  it('returns null for a blocked target (not "not started")', () => {
    expect(dependsOnEdgeColor('blocked')).toBeNull()
  })

  it('returns null for a cancelled target', () => {
    expect(dependsOnEdgeColor('cancelled')).toBeNull()
  })

  it('returns null for a pending_review target', () => {
    expect(dependsOnEdgeColor('pending_review')).toBeNull()
  })

  it('returns null for a doing target (handled by animated path)', () => {
    expect(dependsOnEdgeColor('doing')).toBeNull()
  })

  it('uses distinct, recognizable hex values for the two status colors', () => {
    expect(DEPENDS_ON_DONE_COLOR).not.toBe(DEPENDS_ON_PENDING_COLOR)
    expect(DEPENDS_ON_DONE_COLOR).toBe('#64748b')
    expect(DEPENDS_ON_PENDING_COLOR).toBe('#eab308')
  })
})

describe('depends_on edge status in layout', () => {
  it('preserves depends_on edges for tasks with mixed statuses', () => {
    // a (done) depends on b (todo): the edge is drawn regardless of status;
    // the renderer applies the color via dependsOnEdgeColor at draw time.
    const cards = [
      mapCard('root', ['a', 'b']),
      card('a', { type: 'task', status: 'done', data: { depends_on: ['b'] } }),
      card('b', { type: 'task', status: 'todo' }),
    ]
    const layout = buildWorkflowLayout(cards)
    const byId = new Map(cards.map(c => [c.id, c]))
    // prereq b → dependent a
    expect(layout.edges).toContainEqual({ from: 'b', to: 'a', kind: 'depends_on' })
    // Color of the edge to the dependent 'a' (done) → low-key slate gray
    expect(dependsOnEdgeColor(byId.get('a')?.status)).toBe(DEPENDS_ON_DONE_COLOR)
    // Color of the edge to 'b' (todo, but b is the source not the target) —
    // the depends_on edge 'b → a' targets 'a', so its color reflects 'a'.
  })

  it('status-based color does not affect edge topology', () => {
    // Layout structure is identical regardless of task statuses.
    const layoutTodo = buildWorkflowLayout([
      mapCard('root', ['a', 'b']),
      card('a', { type: 'task', status: 'todo', data: { depends_on: ['b'] } }),
      card('b', { type: 'task', status: 'todo' }),
    ])
    const layoutDone = buildWorkflowLayout([
      mapCard('root', ['a', 'b']),
      card('a', { type: 'task', status: 'done', data: { depends_on: ['b'] } }),
      card('b', { type: 'task', status: 'done' }),
    ])
    expect(layoutTodo.edges).toEqual(layoutDone.edges)
    expect(layoutTodo.nodes.map(n => ({ x: n.x, y: n.y })))
      .toEqual(layoutDone.nodes.map(n => ({ x: n.x, y: n.y })))
  })
})

describe('locateMapIdForCard', () => {
  const cards = [
    mapCard('map-a', ['t1', 't2']),
    mapCard('map-b', ['t3']),
    card('t1', { type: 'task' }),
    card('t2', { type: 'task', parent: 'map-a' }),
    card('t3', { type: 'task' }),
    card('t4', { type: 'task', parent: 'map-b' }), // parent-linked, not in scope.include
  ]

  it('resolves the map itself, its start sentinel, and scoped tasks', () => {
    expect(locateMapIdForCard('map-a', cards)).toBe('map-a')
    expect(locateMapIdForCard('map-a:start', cards)).toBe('map-a')
    expect(locateMapIdForCard('t1', cards)).toBe('map-a')
    expect(locateMapIdForCard('t3', cards)).toBe('map-b')
  })

  it('falls back to parent linkage for tasks outside scope.include', () => {
    expect(locateMapIdForCard('t4', cards)).toBe('map-b')
  })

  it('returns undefined for null and unknown cards', () => {
    expect(locateMapIdForCard(null, cards)).toBeUndefined()
    expect(locateMapIdForCard('nope', cards)).toBeUndefined()
  })
})

// ── Epic tree layout tests ─────────────────────────────────────────────

describe('classifyWorkflowCategory', () => {
  it('classifies scheduler cards as planned', () => {
    expect(classifyWorkflowCategory(card('s1', { type: 'scheduler' }))).toBe('planned')
  })

  it('classifies standalone workflows as template', () => {
    expect(classifyWorkflowCategory(card('t', { type: 'workflow', standalone: true }))).toBe('template')
  })

  it('classifies saved template maps (data.template:true) as template', () => {
    // wikiTemplateSave stamps data.template:true with status:doing and no owner.
    expect(classifyWorkflowCategory(card('tpl::w', { type: 'workflow', status: 'doing', data: { template: true } }))).toBe('template')
  })

  it('scheduler card takes priority over template', () => {
    // Scheduler cards are always planned, even if they have data.template.
    expect(classifyWorkflowCategory(card('s1', { type: 'scheduler', data: { template: true } }))).toBe('planned')
  })

  it('classifies doing with a bound owner as in-progress', () => {
    expect(classifyWorkflowCategory(mapCard('w', [], 'agent-1', undefined, 'doing'))).toBe('in-progress')
  })

  it('classifies ownerless doing/pending_review/blocked as to-start (in-progress requires a live owner)', () => {
    expect(classifyWorkflowCategory(mapCard('w', [], undefined, undefined, 'doing'))).toBe('to-start')
    expect(classifyWorkflowCategory(mapCard('w', [], undefined, undefined, 'pending_review'))).toBe('to-start')
    expect(classifyWorkflowCategory(mapCard('w', [], undefined, undefined, 'blocked'))).toBe('to-start')
  })

  it('classifies done as completed', () => {
    expect(classifyWorkflowCategory(mapCard('w', [], undefined, undefined, 'done'))).toBe('completed')
  })

  it('classifies done as completed even while an owner id is still stamped', () => {
    // workflow_stop marks the card done; a stale ownerAgentId must not drag
    // it back to in-progress. done is the single completion signal.
    const wf = mapCard('w', [], 'agent-1', undefined, 'done')
    expect(classifyWorkflowCategory(wf, new Set(['agent-1']))).toBe('completed')
  })

  it('classifies todo, backlog, and no-status as to-start', () => {
    expect(classifyWorkflowCategory(mapCard('w', [], undefined, undefined, 'todo'))).toBe('to-start')
    expect(classifyWorkflowCategory(mapCard('w', [], undefined, undefined, 'backlog'))).toBe('to-start')
    expect(classifyWorkflowCategory(mapCard('w', []))).toBe('to-start')
  })

  it('classifies a live owner as in-progress regardless of map status (Go parity)', () => {
    // workflow_start/activateWorkflow binds ownerAgentId without stamping
    // status:doing, so a running map may legitimately carry todo/empty status.
    // The server-side fold filter (workflow_fold.go) must classify the same
    // way, or it drops such a map's task cards on reload whenever the
    // to-start band is folded and the expanded graph collapses.
    expect(classifyWorkflowCategory(mapCard('w', [], 'agent-1', undefined, 'todo'), new Set(['agent-1']))).toBe('in-progress')
    expect(classifyWorkflowCategory(mapCard('w', [], 'agent-1', undefined, 'backlog'), new Set(['agent-1']))).toBe('in-progress')
    expect(classifyWorkflowCategory(mapCard('w', [], 'agent-1', undefined, undefined), new Set(['agent-1']))).toBe('in-progress')
    expect(classifyWorkflowCategory(mapCard('w', [], 'agent-1', undefined, 'todo'))).toBe('in-progress')
  })

  it('standalone takes priority over status', () => {
    expect(classifyWorkflowCategory(card('t', { type: 'workflow', standalone: true, status: 'done' }))).toBe('template')
  })

  it('classifies doing with existing owner agent as in-progress', () => {
    const wf = mapCard('w', [], 'agent-1', undefined, 'doing')
    expect(classifyWorkflowCategory(wf, new Set(['agent-1']))).toBe('in-progress')
  })

  it('classifies doing with missing owner agent as to-start', () => {
    const wf = mapCard('w', [], 'agent-1', undefined, 'doing')
    expect(classifyWorkflowCategory(wf, new Set(['other-agent']))).toBe('to-start')
  })

  it('classifies doing without owner agent as to-start when agentIds provided', () => {
    const wf = mapCard('w', [], undefined, undefined, 'doing')
    expect(classifyWorkflowCategory(wf, new Set(['agent-1']))).toBe('to-start')
  })

  it('classifies pending_review with existing owner agent as in-progress', () => {
    const wf = mapCard('w', [], 'agent-1', undefined, 'pending_review')
    expect(classifyWorkflowCategory(wf, new Set(['agent-1']))).toBe('in-progress')
  })

  it('classifies blocked with missing owner agent as to-start', () => {
    const wf = mapCard('w', [], 'agent-1', undefined, 'blocked')
    expect(classifyWorkflowCategory(wf, new Set())).toBe('to-start')
  })

  it('ignores agentIds when not provided (backward compatible)', () => {
    const wf = mapCard('w', [], 'agent-1', undefined, 'doing')
    expect(classifyWorkflowCategory(wf)).toBe('in-progress')
  })

  it('standalone takes priority over agent check', () => {
    const wf = card('t', { type: 'workflow', standalone: true, status: 'doing', data: { ownerAgentId: 'agent-1' } })
    expect(classifyWorkflowCategory(wf, new Set())).toBe('template')
  })

  it('classifies a template instance without owner as planned', () => {
    const inst = card('inst::1::tpl::w', { type: 'workflow', status: 'doing', data: { instance_of: 'tpl::w' } })
    expect(classifyWorkflowCategory(inst, new Set(['agent-1']))).toBe('planned')
  })

  it('classifies a template instance with a live owner as in-progress', () => {
    const inst = card('inst::1::tpl::w', { type: 'workflow', status: 'doing', data: { instance_of: 'tpl::w', ownerAgentId: 'agent-1' } })
    expect(classifyWorkflowCategory(inst, new Set(['agent-1']))).toBe('in-progress')
  })

  it('classifies a template instance whose owner is not live as planned', () => {
    const inst = card('inst::1::tpl::w', { type: 'workflow', status: 'doing', data: { instance_of: 'tpl::w', ownerAgentId: 'agent-1' } })
    expect(classifyWorkflowCategory(inst, new Set(['other-agent']))).toBe('planned')
  })

  it('keeps an ordinary doing map without instance_of or owner in to-start (regression)', () => {
    const wf = mapCard('w', [], undefined, undefined, 'doing')
    expect(classifyWorkflowCategory(wf, new Set(['agent-1']))).toBe('to-start')
  })
})

describe('buildEpicLayout', () => {
  it('returns an empty layout when there are no workflows', () => {
    const layout = buildEpicLayout([card('n1')])
    expect(layout.nodes).toEqual([])
    expect(layout.edges).toEqual([])
    expect(layout.direction).toBe('TTB')
  })

  it('produces a single epic root, one category node, and one start node for a single folded workflow', () => {
    const cards = [
      mapCard('w1', ['t1'], 'agent-1', undefined, 'doing'),
      card('t1', { type: 'task' }),
    ]
    const layout = buildEpicLayout(cards) // default: all folded
    const epicNode = layout.nodes.find(n => n.kind === 'epic')
    const catNodes = layout.nodes.filter(n => n.kind === 'category')
    const startNodes = layout.nodes.filter(n => n.kind === 'sentinel')

    expect(epicNode?.cardId).toBe(EPIC_ROOT_ID)
    expect(catNodes).toHaveLength(1)
    expect(catNodes[0]!.label).toBe('in-progress')
    // Folded: only the start sentinel, no tasks or root.
    expect(startNodes).toHaveLength(1)
    expect(startNodes[0]!.cardId).toBe('w1:start')
    expect(layout.nodes.some(n => n.cardId === 'w1')).toBe(false)
    expect(layout.nodes.some(n => n.cardId === 't1')).toBe(false)
  })

  it('connects epic root → category → start with tree edges', () => {
    const cards = [
      mapCard('w1', [], 'agent-1', undefined, 'doing'),
    ]
    const layout = buildEpicLayout(cards)
    const catId = epicCategoryId('in-progress')
    expect(layout.edges).toContainEqual({ from: EPIC_ROOT_ID, to: catId, kind: 'tree' })
    expect(layout.edges).toContainEqual({ from: catId, to: 'w1:start', kind: 'tree' })
  })

  it('groups workflows into the correct categories', () => {
    const cards = [
      mapCard('doing-wf', ['t'], 'agent-1', undefined, 'doing'),
      mapCard('done-wf', [], undefined, undefined, 'done'),
      card('t', { type: 'task' }),
    ]
    const layout = buildEpicLayout(cards)
    const cats = layout.nodes.filter(n => n.kind === 'category').map(n => n.label)
    expect(cats).toContain('in-progress')
    expect(cats).toContain('completed')
  })

  it('orders categories by EPIC_CATEGORY_ORDER', () => {
    const cards = [
      mapCard('w-done', [], undefined, undefined, 'done'),
      mapCard('w-doing', [], undefined, undefined, 'doing'),
      mapCard('w-todo', [], undefined, undefined, 'todo'),
    ]
    const layout = buildEpicLayout(cards)
    const catLabels = layout.nodes.filter(n => n.kind === 'category').map(n => n.label as EpicCategory)
    const orderIndices = catLabels.map(c => EPIC_CATEGORY_ORDER.indexOf(c))
    expect(orderIndices).toEqual([...orderIndices].sort())
  })

  it('folds all workflows by default (only start nodes, no internals)', () => {
    const cards = [
      mapCard('w1', ['t1', 't2']),
      card('t1', { type: 'task' }),
      card('t2', { type: 'task' }),
    ]
    const layout = buildEpicLayout(cards)
    expect(layout.nodes.some(n => n.cardId === 't1')).toBe(false)
    expect(layout.nodes.some(n => n.cardId === 't2')).toBe(false)
    expect(layout.nodes.some(n => n.cardId === 'w1')).toBe(false)
    // The folded start node is card-sized.
    const start = layout.nodes.find(n => n.cardId === 'w1:start')!
    expect(start.width).toBe(WORKFLOW_CARD_W)
    expect(start.height).toBe(WORKFLOW_TASK_H)
  })

  it('expands a workflow to reveal its full internal TTB layout', () => {
    const cards = [
      mapCard('w1', ['t1']),
      card('t1', { type: 'task' }),
    ]
    const layout = buildEpicLayout(cards, { expandedWorkflows: new Set(['w1']) })
    // Expanded: root map + start + task are all present.
    expect(layout.nodes.some(n => n.cardId === 'w1:start')).toBe(true)
    expect(layout.nodes.some(n => n.cardId === 'w1')).toBe(true)
    expect(layout.nodes.some(n => n.cardId === 't1')).toBe(true)
    // Internal edges: start → t1, t1 → root.
    expect(layout.edges).toContainEqual({ from: 'w1:start', to: 't1', kind: 'start' })
    expect(layout.edges.some(e => e.from === 't1' && e.to === 'w1' && e.kind === 'flow')).toBe(true)
    // Expanded start node is card-sized (same footprint as task cards).
    const start = layout.nodes.find(n => n.cardId === 'w1:start')!
    expect(start.width).toBe(WORKFLOW_CARD_W)
    expect(start.height).toBe(WORKFLOW_TASK_H)
  })

  it('includes standalone (template) workflows in the template category', () => {
    const cards = [
      card('tmpl', { type: 'workflow', standalone: true, data: { scope: { include: ['x'] } } }),
      card('x', { type: 'task' }),
    ]
    const layout = buildEpicLayout(cards)
    const catNodes = layout.nodes.filter(n => n.kind === 'category')
    expect(catNodes.some(n => n.label === 'template')).toBe(true)
    expect(layout.nodes.some(n => n.cardId === 'tmpl:start')).toBe(true)
  })

  it('stacks multiple workflows in the same category without overlap', () => {
    const cards = [
      mapCard('w1', ['a']),
      mapCard('w2', ['b']),
      card('a', { type: 'task' }),
      card('b', { type: 'task' }),
    ]
    const layout = buildEpicLayout(cards)
    const s1 = layout.nodes.find(n => n.cardId === 'w1:start')!
    const s2 = layout.nodes.find(n => n.cardId === 'w2:start')!
    // Both in the to-start category; they must not overlap.
    const rect1 = { left: s1.x, top: s1.y, right: s1.x + s1.width, bottom: s1.y + s1.height }
    const rect2 = { left: s2.x, top: s2.y, right: s2.x + s2.width, bottom: s2.y + s2.height }
    const overlapX = rect1.left < rect2.right && rect1.right > rect2.left
    const overlapY = rect1.top < rect2.bottom && rect1.bottom > rect2.top
    expect(overlapX && overlapY).toBe(false)
  })

  it('arranges workflows of a category horizontally in TTB mode', () => {
    const cards = [
      mapCard('w1', []),
      mapCard('w2', []),
    ]
    const layout = buildEpicLayout(cards, { direction: 'TTB' })
    const s1 = layout.nodes.find(n => n.cardId === 'w1:start')!
    const s2 = layout.nodes.find(n => n.cardId === 'w2:start')!
    // Same row (y), different x — side by side under the category.
    expect(s1.y).toBe(s2.y)
    expect(s1.x).not.toBe(s2.x)
    expect(layout.direction).toBe('TTB')
  })

  it('arranges categories in a column and workflows vertically in LTR mode', () => {
    const cards = [
      mapCard('w-doing', [], 'agent-1', undefined, 'doing'),
      mapCard('w-done', [], undefined, undefined, 'done'),
      mapCard('w-doing-2', [], 'agent-1', undefined, 'doing'),
    ]
    const layout = buildEpicLayout(cards, { direction: 'LTR' })
    expect(layout.direction).toBe('LTR')

    const epic = layout.nodes.find(n => n.kind === 'epic')!
    const cats = layout.nodes.filter(n => n.kind === 'category')
    // Categories are right of the epic root and stacked vertically (same x).
    for (const c of cats) {
      expect(c.x).toBeGreaterThan(epic.x)
    }
    const xs = new Set(cats.map(c => c.x))
    expect(xs.size).toBe(1)

    // The two in-progress workflows stack vertically (same x, different y).
    const s1 = layout.nodes.find(n => n.cardId === 'w-doing:start')!
    const s2 = layout.nodes.find(n => n.cardId === 'w-doing-2:start')!
    expect(s1.x).toBe(s2.x)
    expect(s1.y).not.toBe(s2.y)
    // Workflows are right of their category node.
    const cat = cats.find(c => c.label === 'in-progress')!
    expect(s1.x).toBeGreaterThan(cat.x)
  })

  it('lays expanded workflow internals right of the start node in LTR mode', () => {
    const cards = [
      mapCard('w1', ['t1']),
      card('t1', { type: 'task' }),
    ]
    const layout = buildEpicLayout(cards, { direction: 'LTR', expandedWorkflows: new Set(['w1']) })
    const start = layout.nodes.find(n => n.cardId === 'w1:start')!
    const task = layout.nodes.find(n => n.cardId === 't1')!
    expect(task.x).toBeGreaterThan(start.x)
  })

  it('places category nodes in a horizontal row below the epic root', () => {
    const cards = [
      mapCard('w-doing', [], undefined, undefined, 'doing'),
      mapCard('w-done', [], undefined, undefined, 'done'),
    ]
    const layout = buildEpicLayout(cards)
    const epic = layout.nodes.find(n => n.kind === 'epic')!
    const cats = layout.nodes.filter(n => n.kind === 'category')
    // All categories are below the epic root.
    for (const c of cats) {
      expect(c.y).toBeGreaterThan(epic.y)
    }
    // Categories are arranged left-to-right (sorted by x).
    const xs = cats.map(c => c.x)
    expect(xs).toEqual([...xs].sort((a, b) => a - b))
  })

  it('omits categories that have no workflows', () => {
    const cards = [
      mapCard('w1', [], 'agent-1', undefined, 'doing'),
    ]
    const layout = buildEpicLayout(cards)
    const catLabels = layout.nodes.filter(n => n.kind === 'category').map(n => n.label)
    expect(catLabels).toEqual(['in-progress'])
  })

  it('expanded workflow internals are positioned below the start node', () => {
    const cards = [
      mapCard('w1', ['t1']),
      card('t1', { type: 'task' }),
    ]
    const layout = buildEpicLayout(cards, { expandedWorkflows: new Set(['w1']) })
    const start = layout.nodes.find(n => n.cardId === 'w1:start')!
    const task = layout.nodes.find(n => n.cardId === 't1')!
    expect(task.y).toBeGreaterThan(start.y)
  })

  it('classifies a scheduler card as planned; source map goes by status', () => {
    const cards = [
      mapCard('w1', []),
      card('s1', {
        type: 'scheduler',
        data: { workflow_template: 'tpl::w1' },
        raw: '',
      }),
    ]
    const layout = buildEpicLayout(cards)
    const catLabels = layout.nodes.filter(n => n.kind === 'category').map(n => n.label)
    // The scheduler card is a real node in the planned category.
    expect(catLabels).toContain('planned')
    // The source map w1 has no status → to-start (not planned).
    expect(catLabels).toContain('to-start')
  })
})

// ── Planned scheduler grouping ─────────────────────────────────────────

describe('schedulerOfInstance', () => {
  it('returns the scheduler_card_id when present', () => {
    const instance = card('inst::ts::w1', { type: 'workflow', data: { scheduler_card_id: 'sched-1' } })
    expect(schedulerOfInstance(instance)).toBe('sched-1')
  })

  it('returns undefined when scheduler_card_id is missing', () => {
    const instance = card('w1', { type: 'workflow' })
    expect(schedulerOfInstance(instance)).toBeUndefined()
  })

  it('returns undefined when scheduler_card_id is empty', () => {
    const instance = card('inst::ts::w1', { type: 'workflow', data: { scheduler_card_id: '' } })
    expect(schedulerOfInstance(instance)).toBeUndefined()
  })
})

describe('buildPlannedSchedulerGroups', () => {
  it('groups workflows by scheduler_card_id', () => {
    const scheduler = card('sched-1', { type: 'scheduler' })
    const w1 = card('inst::ts::w1', { type: 'workflow', data: { scheduler_card_id: 'sched-1' } })
    const w2 = card('inst::ts::w2', { type: 'workflow', data: { scheduler_card_id: 'sched-1' } })
    const groups = buildPlannedSchedulerGroups([w1, w2], [scheduler])
    expect(groups).toHaveLength(1)
    expect(groups[0]!.id).toBe('sched-1')
    expect(groups[0]!.label).toBe('sched-1')
    expect(groups[0]!.workflows.map(w => w.id)).toEqual(['inst::ts::w1', 'inst::ts::w2'])
  })

  it('skips workflows without a matching scheduler card', () => {
    const scheduler = card('sched-1', { type: 'scheduler' })
    const w1 = card('inst::ts::w1', { type: 'workflow', data: { scheduler_card_id: 'sched-1' } })
    const w2 = card('inst::ts::w2', { type: 'workflow', data: { scheduler_card_id: 'ghost' } })
    const groups = buildPlannedSchedulerGroups([w1, w2], [scheduler])
    expect(groups).toHaveLength(1)
    expect(groups[0]!.workflows.map(w => w.id)).toEqual(['inst::ts::w1'])
  })

  it('returns empty array when no workflows have scheduler_card_id', () => {
    const groups = buildPlannedSchedulerGroups([card('w1', { type: 'workflow' })], [card('sched-1', { type: 'scheduler' })])
    expect(groups).toEqual([])
  })
})

describe('epic layout — schedule planner grouping', () => {
  function schedulerCard(id: string): MonoCardListItem {
    return card(id, { type: 'scheduler', raw: '' })
  }

  function instanceCard(id: string, schedulerId: string): MonoCardListItem {
    // Instance workflows are classified as planned via doing status + instance_of.
    return card(id, { type: 'workflow', status: 'doing', data: { scheduler_card_id: schedulerId, instance_of: `tpl::${id}`, scope: { include: [] } } })
  }

  it('places scheduler-bound instances under a scheduler group node in planned', () => {
    const cards = [
      schedulerCard('sched-1'),
      instanceCard('inst::t1::w1', 'sched-1'),
      instanceCard('inst::t2::w2', 'sched-1'),
      mapCard('manual-wf', [], undefined, undefined, 'doing'), // no scheduler_card_id
    ]
    // Make the manual workflow a planned instance (classify as planned).
    cards[3]!.data = { ...cards[3]!.data, instance_of: 'tpl::manual' }

    const layout = buildEpicLayout(cards, { agentIds: new Set(), expandedBuckets: new Set(['sched-1']) })
    const catLabels = layout.nodes.filter(n => n.kind === 'category').map(n => n.label)
    expect(catLabels).toEqual(expect.arrayContaining(['planned']))
    // Ensure we don't have unexpected categories like 'in-progress' (no owner agent).
    expect(catLabels).not.toContain('in-progress')

    // Scheduler group node exists.
    const schedNode = layout.nodes.find(n => n.cardId === 'sched-1')
    expect(schedNode).toBeDefined()
    expect(schedNode!.kind).toBe('scheduler')
    expect(schedNode!.label).toBe('sched-1')
    expect(schedNode!.count).toBe(2)

    // Two instance workflows under the scheduler.
    expect(layout.nodes.some(n => n.cardId === 'inst::t1::w1:start')).toBe(true)
    expect(layout.nodes.some(n => n.cardId === 'inst::t2::w2:start')).toBe(true)

    // Manual workflow (no scheduler_card_id) is directly under planned.
    expect(layout.nodes.some(n => n.cardId === 'manual-wf:start')).toBe(true)

    // Scheduler group is connected to the planned category.
    expect(layout.edges).toContainEqual({ from: epicCategoryId('planned'), to: 'sched-1', kind: 'tree' })
    // Instance workflows are connected to the scheduler group.
    expect(layout.edges).toContainEqual({ from: 'sched-1', to: 'inst::t1::w1:start', kind: 'tree' })
    expect(layout.edges).toContainEqual({ from: 'sched-1', to: 'inst::t2::w2:start', kind: 'tree' })
    // Manual workflow is connected directly to planned.
    expect(layout.edges).toContainEqual({ from: epicCategoryId('planned'), to: 'manual-wf:start', kind: 'tree' })
  })

  it('folds scheduler group: instances hidden when bucket not expanded', () => {
    const cards = [
      schedulerCard('sched-1'),
      instanceCard('inst::t1::w1', 'sched-1'),
    ]
    // Pass empty agentIds so doing+instance_of is classified as planned (not in-progress).
    const layout = buildEpicLayout(cards, { agentIds: new Set() })
    const schedNode = layout.nodes.find(n => n.cardId === 'sched-1')
    expect(schedNode).toBeDefined()
    expect(schedNode!.count).toBe(1) // count is always visible
    // Instance start block is NOT in the nodes (hidden by fold).
    expect(layout.nodes.some(n => n.cardId === 'inst::t1::w1:start')).toBe(false)
  })

  it('renders a zero-instance scheduler as a real planned node (count=0)', () => {
    // A scheduler with no current planned instances must still appear as a
    // planned node so the planned task does not vanish from the graph.
    const cards = [
      schedulerCard('sched-empty'),
    ]
    const layout = buildEpicLayout(cards, { agentIds: new Set() })
    const catLabels = layout.nodes.filter(n => n.kind === 'category').map(n => n.label)
    expect(catLabels).toContain('planned')
    const schedNode = layout.nodes.find(n => n.cardId === 'sched-empty')
    expect(schedNode).toBeDefined()
    expect(schedNode!.kind).toBe('scheduler')
    expect(schedNode!.label).toBe('sched-empty')
    expect(schedNode!.count).toBe(0)
    // Scheduler node is connected to the planned category.
    expect(layout.edges).toContainEqual({ from: epicCategoryId('planned'), to: 'sched-empty', kind: 'tree' })
  })

  it('mounts a ghost template preview under an expanded zero-instance scheduler', () => {
    const sched = schedulerCard('sched-empty')
    sched.data = { workflow_template: 'tpl::Weekly' }
    const cards = [sched, mapCard('tpl::Weekly', [], undefined, undefined, 'todo')]
    const layout = buildEpicLayout(cards, { agentIds: new Set(), expandedBuckets: new Set(['sched-empty']) })
    const ghost = layout.nodes.find(n => n.kind === 'ghost')
    expect(ghost).toBeDefined()
    expect(ghost!.cardId).toBe('ghost:sched-empty')
    expect(ghost!.templateId).toBe('tpl::Weekly')
    // Ghost is card-sized like a folded start.
    expect(ghost!.width).toBe(220)
    expect(ghost!.height).toBe(64)
    // Scheduler → ghost tree edge.
    expect(layout.edges).toContainEqual({ from: 'sched-empty', to: 'ghost:sched-empty', kind: 'tree' })
  })

  it('hides the ghost preview when the zero-instance scheduler is collapsed', () => {
    const sched = schedulerCard('sched-empty')
    sched.data = { workflow_template: 'tpl::Weekly' }
    const cards = [sched, mapCard('tpl::Weekly', [], undefined, undefined, 'todo')]
    const layout = buildEpicLayout(cards, { agentIds: new Set() })
    expect(layout.nodes.find(n => n.kind === 'ghost')).toBeUndefined()
  })

  it('omits the ghost preview when the template reference is dangling', () => {
    const sched = schedulerCard('sched-empty')
    sched.data = { workflow_template: 'tpl::Missing' }
    const layout = buildEpicLayout([sched], { agentIds: new Set(), expandedBuckets: new Set(['sched-empty']) })
    expect(layout.nodes.find(n => n.kind === 'ghost')).toBeUndefined()
  })

  it('no node overlaps when a zero-instance scheduler with ghost preview is expanded', () => {
    const sched = schedulerCard('sched-empty')
    sched.data = { workflow_template: 'tpl::Weekly' }
    const cards = [sched, mapCard('tpl::Weekly', [], undefined, undefined, 'todo')]
    const layout = buildEpicLayout(cards, { agentIds: new Set(), expandedBuckets: new Set(['sched-empty']) })
    for (let i = 0; i < layout.nodes.length; i++) {
      for (let j = i + 1; j < layout.nodes.length; j++) {
        const a = layout.nodes[i]!, b = layout.nodes[j]!
        const overlap = a.x < b.x + b.width && a.x + a.width > b.x &&
          a.y < b.y + b.height && a.y + a.height > b.y
        expect(overlap).toBe(false)
      }
    }
  })

  it('no node overlaps when scheduler group is expanded', () => {
    const cards = [
      mapCard('doing-wf', [], undefined, undefined, 'doing'),
      schedulerCard('sched-1'),
      instanceCard('inst::t1::w1', 'sched-1'),
      instanceCard('inst::t2::w2', 'sched-1'),
    ]
    const layout = buildEpicLayout(cards, { agentIds: new Set(), expandedBuckets: new Set(['sched-1']) })
    for (let i = 0; i < layout.nodes.length; i++) {
      for (let j = i + 1; j < layout.nodes.length; j++) {
        const a = layout.nodes[i]!, b = layout.nodes[j]!
        const overlap = a.x < b.x + b.width && a.x + a.width > b.x &&
          a.y < b.y + b.height && a.y + a.height > b.y
        expect(overlap).toBe(false)
      }
    }
  })
})

// ── Category band node spacing ───────────────────────────────────────

describe('epic band node spacing', () => {
  /** Assert no two layout nodes overlap (axis-aligned rect intersection). */
  function assertNoOverlaps(nodes: WorkflowNodeLayout[]) {
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i]!, b = nodes[j]!
        const overlap = a.x < b.x + b.width && a.x + a.width > b.x &&
          a.y < b.y + b.height && a.y + a.height > b.y
        expect(overlap).toBe(false)
      }
    }
  }

  /** All start nodes sharing the same main-axis coordinate must have uniform
   *  edge-to-edge gaps on the cross axis. Returns the gap value or throws. */
  function assertUniformBandGaps(
    nodes: WorkflowNodeLayout[],
    isLtr: boolean,
  ): number {
    // Collect start nodes (sentinels with role 'start' — their cardId ends ':start').
    const starts = nodes.filter(n => n.cardId.endsWith(':start'))
    // Group by main-axis position (y for TTB, x for LTR).
    const groups = new Map<number, WorkflowNodeLayout[]>()
    for (const s of starts) {
      const key = isLtr ? s.x : s.y
      const k = [...groups.keys()].find(kk => Math.abs(kk - key) < 1)
      const g = groups.get(k ?? key) ?? []
      g.push(s)
      groups.set(k ?? key, g)
    }
    let gapValue: number | undefined
    for (const [, group] of groups) {
      if (group.length < 2) continue
      // Sort by cross-axis position.
      group.sort((a, b) => (isLtr ? a.y - b.y : a.x - b.x))
      for (let i = 0; i < group.length - 1; i++) {
        const a = group[i]!, b = group[i + 1]!
        const aEnd = isLtr ? a.y + a.height : a.x + a.width
        const bStart = isLtr ? b.y : b.x
        const gap = bStart - aEnd
        if (gapValue === undefined) gapValue = gap
        expect(gap).toBe(gapValue)
        expect(gap).toBeGreaterThanOrEqual(0)
      }
    }
    return gapValue ?? -1
  }

  const multiBandCards = [
    mapCard('w1', ['a', 'b'], 'doing'),
    mapCard('w2', ['c'], 'doing'),
    mapCard('w3', ['d', 'e', 'f'], 'todo'),
    card('a', { type: 'task' }),
    card('b', { type: 'task', data: { depends_on: ['a'] } }),
    card('c', { type: 'task' }),
    card('d', { type: 'task' }),
    card('e', { type: 'task', data: { depends_on: ['d'] } }),
    card('f', { type: 'task', data: { depends_on: ['e'] } }),
  ]

  const parallelTaskCards = [
    mapCard('w1', ['a', 'b']),
    mapCard('w2', ['c']),
    card('a', { type: 'task' }),
    card('b', { type: 'task' }),
    card('c', { type: 'task' }),
  ]

  for (const dir of ['TTB', 'LTR'] as const) {
    const isLtr = dir === 'LTR'

    it(`no overlaps — folded workflows (${dir})`, () => {
      const layout = buildEpicLayout(multiBandCards, { direction: dir })
      assertNoOverlaps(layout.nodes)
    })

    it(`no overlaps — expanded workflows (${dir})`, () => {
      const layout = buildEpicLayout(multiBandCards, {
        direction: dir,
        expandedWorkflows: new Set(['w1', 'w3']),
      })
      assertNoOverlaps(layout.nodes)
    })

    it(`no overlaps — mixed folded+expanded (${dir})`, () => {
      const layout = buildEpicLayout(multiBandCards, {
        direction: dir,
        expandedWorkflows: new Set(['w1']),
      })
      assertNoOverlaps(layout.nodes)
    })

    it(`no overlaps — expanded parallel tasks (${dir})`, () => {
      const layout = buildEpicLayout(parallelTaskCards, {
        direction: dir,
        expandedWorkflows: new Set(['w1']),
      })
      assertNoOverlaps(layout.nodes)
    })

    it(`uniform intra-band start gaps — folded (${dir})`, () => {
      const layout = buildEpicLayout(multiBandCards, { direction: dir })
      assertUniformBandGaps(layout.nodes, isLtr)
    })

    it(`uniform intra-band start gaps — expanded (${dir})`, () => {
      const layout = buildEpicLayout(multiBandCards, {
        direction: dir,
        expandedWorkflows: new Set(['w1', 'w3']),
      })
      assertUniformBandGaps(layout.nodes, isLtr)
    })

    it(`uniform intra-band start gaps — mixed (${dir})`, () => {
      const layout = buildEpicLayout(multiBandCards, {
        direction: dir,
        expandedWorkflows: new Set(['w1']),
      })
      assertUniformBandGaps(layout.nodes, isLtr)
    })

    it(`expanded block reserves its tallest column centered on the start node (${dir})`, () => {
      // w1 has two parallel tasks (same depth) → the tallest column / widest
      // row spans two cards; its reserved space must split evenly around the
      // start node's cross-axis center.
      const layout = buildEpicLayout(parallelTaskCards, {
        direction: dir,
        expandedWorkflows: new Set(['w1']),
      })
      const start = layout.nodes.find(n => n.cardId === 'w1:start')!
      const tasks = ['a', 'b'].map(id => layout.nodes.find(n => n.cardId === id)!)
      const startCenter = isLtr ? start.y + start.height / 2 : start.x + start.width / 2
      const laneMin = Math.min(...tasks.map(t => (isLtr ? t.y : t.x)))
      const laneMax = Math.max(...tasks.map(t => (isLtr ? t.y + t.height : t.x + t.width)))
      expect((laneMin + laneMax) / 2).toBeCloseTo(startCenter, 6)
    })

    it(`inter-band gap equals EPIC_COL_GAP between block edges (${dir})`, () => {
      // Two categories, each with one folded workflow.
      const cards = [
        mapCard('w1', ['a'], 'agent-1', undefined, 'doing'),
        mapCard('w2', ['b'], undefined, undefined, 'todo'),
        card('a', { type: 'task' }),
        card('b', { type: 'task' }),
      ]
      const layout = buildEpicLayout(cards, { direction: dir })
      const s1 = layout.nodes.find(n => n.cardId === 'w1:start')!
      const s2 = layout.nodes.find(n => n.cardId === 'w2:start')!
      // Gap between BLOCK edges on the cross axis. The folded block's cross
      // extent is max(TASK_H, ROOT_H) in LTR (height) or CARD_W in TTB
      // (width), with the card centered inside the reserved extent.
      const blockCrossExtent = isLtr
        ? Math.max(WORKFLOW_TASK_H, WORKFLOW_ROOT_H)
        : WORKFLOW_CARD_W
      const cardCross = isLtr ? WORKFLOW_TASK_H : WORKFLOW_CARD_W
      const margin = (blockCrossExtent - cardCross) / 2
      const s1BlockEnd = (isLtr ? s1.y : s1.x) - margin + blockCrossExtent
      const s2BlockStart = (isLtr ? s2.y : s2.x) - margin
      const gap = s2BlockStart - s1BlockEnd
      expect(gap).toBe(72) // EPIC_COL_GAP
    })
  }
})

// ── Archived category & time buckets ─────────────────────────────────

describe('epic archived category', () => {
  const NOW = Date.parse('2026-08-14T12:00:00Z')
  const DAY = 86_400_000

  /** A done workflow whose last activity is `ageDays` before NOW (mid-day UTC
   *  so local-day bucket keys are stable across timezones). */
  function doneMap(id: string, ageDays: number): MonoCardListItem {
    return { ...mapCard(id, [], undefined, undefined, 'done'), modified: new Date(NOW - ageDays * DAY).toISOString() }
  }

  /** Mirror of the layout's local-time bucket id derivation. */
  function bucketId(kind: 'date' | 'month' | 'year', ageDays: number): string {
    const d = new Date(NOW - ageDays * DAY)
    const y = d.getFullYear()
    const m = String(d.getMonth() + 1).padStart(2, '0')
    const day = String(d.getDate()).padStart(2, '0')
    const key = kind === 'date' ? `${y}-${m}-${day}` : kind === 'month' ? `${y}-${m}` : `${y}`
    return `${EPIC_BUCKET_PREFIX}${kind}:${key}`
  }

  it('classifies completed workflows by last activity against the 7-day threshold', () => {
    const recent = doneMap('w-recent', 1)
    const boundary = doneMap('w-boundary', 7)
    const old = doneMap('w-old', 8)
    const cards = [recent, boundary, old]
    expect(classifyWorkflowCategory(recent, undefined, { cards, now: NOW })).toBe('completed')
    expect(classifyWorkflowCategory(boundary, undefined, { cards, now: NOW })).toBe('completed')
    expect(classifyWorkflowCategory(old, undefined, { cards, now: NOW })).toBe('archived')
    // Without the archive input the classification is unchanged.
    expect(classifyWorkflowCategory(old)).toBe('completed')
  })

  it('never archives non-completed workflows regardless of age', () => {
    const doing = { ...mapCard('w-doing', [], 'agent-1', undefined, 'doing'), modified: new Date(NOW - 30 * DAY).toISOString() }
    const tmpl = { ...card('w-tmpl', { type: 'workflow', standalone: true, status: 'done' }), modified: new Date(NOW - 30 * DAY).toISOString() }
    const cards = [doing, tmpl]
    expect(classifyWorkflowCategory(doing, undefined, { cards, now: NOW })).toBe('in-progress')
    expect(classifyWorkflowCategory(tmpl, undefined, { cards, now: NOW })).toBe('template')
  })

  it('buildArchivedBuckets groups by recency and creates buckets on demand', () => {
    const wA = doneMap('w-a', 8)
    const wB = doneMap('w-b', 10)
    const wC = doneMap('w-c', 40)
    const wD = doneMap('w-d', 400)
    const cards = [wA, wB, wC, wD]
    const buckets = buildArchivedBuckets([wA, wB, wC, wD], cards, NOW)
    expect(buckets.map(b => b.kind)).toEqual(['date', 'date', 'month', 'year'])
    expect(buckets.map(b => b.id)).toEqual([
      bucketId('date', 8),
      bucketId('date', 10),
      bucketId('month', 40),
      bucketId('year', 400),
    ])
    expect(buckets[0]!.workflows.map(w => w.id)).toEqual(['w-a'])
    // A workflow older than a year gets its own year bucket even though no
    // such bucket existed before.
    expect(buckets[3]!.label).toBe(String(new Date(NOW - 400 * DAY).getFullYear()))
  })

  it('keeps workflows sorted by last activity inside a bucket', () => {
    const wNew = doneMap('w-new', 8 + 1 / 24)
    const wOld = doneMap('w-old', 8 + 3 / 24)
    const cards = [wNew, wOld]
    const buckets = buildArchivedBuckets([wNew, wOld], cards, NOW)
    expect(buckets).toHaveLength(1)
    expect(buckets[0]!.workflows.map(w => w.id)).toEqual(['w-new', 'w-old'])
  })

  for (const dir of ['TTB', 'LTR'] as const) {
    const isLtr = dir === 'LTR'
    const crossOf = (n: WorkflowNodeLayout) => (isLtr ? n.y : n.x)

    it(`splits old completed workflows into the archived category (${dir})`, () => {
      const recent = doneMap('w-recent', 1)
      const old = doneMap('w-old', 10)
      const bucket = bucketId('date', 10)
      const layout = buildEpicLayout([recent, old], { direction: dir, now: NOW, expandedBuckets: new Set([bucket]) })
      const cats = layout.nodes.filter(n => n.kind === 'category').map(n => n.label)
      expect(cats).toEqual(['completed', 'archived'])
      const archivedCat = epicCategoryId('archived')
      // Archived category → bucket → workflow start; the recent workflow stays
      // directly under completed.
      expect(layout.edges).toContainEqual({ from: epicCategoryId('completed'), to: 'w-recent:start', kind: 'tree' })
      expect(layout.edges).toContainEqual({ from: archivedCat, to: bucket, kind: 'tree' })
      expect(layout.edges).toContainEqual({ from: bucket, to: 'w-old:start', kind: 'tree' })
      const bucketNode = layout.nodes.find(n => n.cardId === bucket)!
      expect(bucketNode.kind).toBe('bucket')
      expect(bucketNode.count).toBe(1)
    })

    it(`orders date buckets above months above years, newest first (${dir})`, () => {
      const cards = [doneMap('w-d1', 8), doneMap('w-d2', 10), doneMap('w-m', 40), doneMap('w-y', 400)]
      const layout = buildEpicLayout(cards, { direction: dir, now: NOW })
      const bucketNodes = layout.nodes.filter(n => n.kind === 'bucket')
      expect(bucketNodes.map(n => n.cardId)).toEqual([
        bucketId('date', 8),
        bucketId('date', 10),
        bucketId('month', 40),
        bucketId('year', 400),
      ])
      const crosses = bucketNodes.map(crossOf)
      expect(crosses).toEqual([...crosses].sort((a, b) => a - b))
    })

    it(`sorts completed and archived workflows by last activity, newest first (${dir})`, () => {
      const cards = [doneMap('w-c1', 1), doneMap('w-c3', 3), doneMap('w-c2', 2), doneMap('w-a2', 8 + 3 / 24), doneMap('w-a1', 8 + 1 / 24)]
      const layout = buildEpicLayout(cards, { direction: dir, now: NOW, expandedBuckets: new Set([bucketId('date', 8 + 1 / 24)]) })
      const cross = (id: string) => crossOf(layout.nodes.find(n => n.cardId === `${id}:start`)!)
      // Completed band: newest on top/left.
      expect(cross('w-c1')).toBeLessThan(cross('w-c2'))
      expect(cross('w-c2')).toBeLessThan(cross('w-c3'))
      // Archived band: same date bucket, newest first.
      expect(cross('w-a1')).toBeLessThan(cross('w-a2'))
    })

    it(`lays bucket nodes between the category and the workflow starts (${dir})`, () => {
      const cards = [doneMap('w-old', 10)]
      const layout = buildEpicLayout(cards, { direction: dir, now: NOW, expandedBuckets: new Set([bucketId('date', 10)]) })
      const cat = layout.nodes.find(n => n.cardId === epicCategoryId('archived'))!
      const bucket = layout.nodes.find(n => n.kind === 'bucket')!
      const start = layout.nodes.find(n => n.cardId === 'w-old:start')!
      const mainOf = (n: WorkflowNodeLayout) => (isLtr ? n.x : n.y)
      expect(mainOf(bucket)).toBeGreaterThan(mainOf(cat))
      expect(mainOf(start)).toBeGreaterThan(mainOf(bucket))
    })

    it(`buckets fold by default; expanding reveals their workflows (${dir})`, () => {
      const cards = [doneMap('w-old', 10), doneMap('w-month', 40)]
      const dateBucket = bucketId('date', 10)
      // Default: folded — no start nodes, no bucket→start edges, counts kept.
      const folded = buildEpicLayout(cards, { direction: dir, now: NOW })
      expect(folded.nodes.some(n => n.cardId === 'w-old:start')).toBe(false)
      expect(folded.nodes.some(n => n.cardId === 'w-month:start')).toBe(false)
      expect(folded.edges.some(e => e.from === dateBucket)).toBe(false)
      expect(folded.nodes.find(n => n.cardId === dateBucket)!.count).toBe(1)
      // Expanded: the bucket's workflows appear, other buckets stay folded.
      const expanded = buildEpicLayout(cards, { direction: dir, now: NOW, expandedBuckets: new Set([dateBucket]) })
      expect(expanded.nodes.some(n => n.cardId === 'w-old:start')).toBe(true)
      expect(expanded.edges).toContainEqual({ from: dateBucket, to: 'w-old:start', kind: 'tree' })
      expect(expanded.nodes.some(n => n.cardId === 'w-month:start')).toBe(false)
    })

    it(`archived band has no node overlaps (${dir})`, () => {
      const cards = [
        doneMap('w-d1', 8), doneMap('w-d2', 10), doneMap('w-m', 40), doneMap('w-y', 400),
        mapCard('w-doing', [], undefined, undefined, 'doing'),
      ]
      const allBuckets = [
        bucketId('date', 8), bucketId('date', 10), bucketId('month', 40), bucketId('year', 400),
      ]
      const layout = buildEpicLayout(cards, { direction: dir, now: NOW, expandedBuckets: new Set(allBuckets) })
      for (let i = 0; i < layout.nodes.length; i++) {
        for (let j = i + 1; j < layout.nodes.length; j++) {
          const a = layout.nodes[i]!, b = layout.nodes[j]!
          const overlap = a.x < b.x + b.width && a.x + a.width > b.x &&
            a.y < b.y + b.height && a.y + a.height > b.y
          expect(overlap).toBe(false)
        }
      }
    })
  }
})

// ── Category band folding ────────────────────────────────────────────

describe('buildEpicLayout foldedCategories', () => {
  const twoBands = [
    mapCard('doing-wf', ['a'], 'agent-1', undefined, 'doing'),
    mapCard('todo-wf', ['b']),
    card('a', { type: 'task' }),
    card('b', { type: 'task' }),
  ]

  for (const dir of ['TTB', 'LTR'] as const) {
    it(`folds a category to its bare category node (${dir})`, () => {
      const layout = buildEpicLayout(twoBands, { direction: dir, foldedCategories: new Set(['to-start']) })
      const ids = layout.nodes.map(n => n.cardId)
      // The folded band keeps only its category node; its workflow nodes vanish.
      expect(ids).toContain(epicCategoryId('to-start'))
      expect(ids).not.toContain('todo-wf')
      expect(ids).not.toContain('todo-wf:start')
      expect(ids).not.toContain('b')
      // The unfolded band is untouched (epic tree folds workflows by default,
      // so only the start sentinel renders for it).
      expect(ids).toContain('doing-wf:start')
      // No tree edge points at a node that does not exist.
      for (const e of layout.edges) {
        expect(ids).toContain(e.from)
        expect(ids).toContain(e.to)
      }
    })
  }

  it('folding the archived category hides its buckets and workflows', () => {
    const old = '2020-01-01T00:00:00Z'
    const cards = [
      mapCard('old-wf', ['t'], undefined, undefined, 'done'),
      card('t', { type: 'task', status: 'done', modified: old }),
    ]
    cards[0]!.modified = old
    const expanded = buildEpicLayout(cards, { direction: 'TTB', expandedBuckets: new Set(['__epic_bucket__:year:2020']) })
    expect(expanded.nodes.some(n => n.kind === 'bucket')).toBe(true)

    const folded = buildEpicLayout(cards, { direction: 'TTB', foldedCategories: new Set(['archived']) })
    const ids = folded.nodes.map(n => n.cardId)
    expect(ids).toContain(epicCategoryId('archived'))
    expect(folded.nodes.some(n => n.kind === 'bucket')).toBe(false)
    expect(ids).not.toContain('old-wf')
    expect(ids).not.toContain('old-wf:start')
    for (const e of folded.edges) {
      expect(ids).toContain(e.from)
      expect(ids).toContain(e.to)
    }
  })
})

describe('epicWorkflowPlacement', () => {
  it('returns the category for a non-archived workflow', () => {
    const cards = [mapCard('w', ['a']), card('a', { type: 'task' })]
    expect(epicWorkflowPlacement(cards[0]!, cards)).toEqual({ category: 'to-start' })
  })

  it('returns the containing time bucket for an archived workflow', () => {
    const old = '2020-06-15T00:00:00Z'
    const cards = [mapCard('old-wf', ['t'], undefined, undefined, 'done'), card('t', { type: 'task', status: 'done', modified: old })]
    cards[0]!.modified = old
    const placement = epicWorkflowPlacement(cards[0]!, cards)
    expect(placement.category).toBe('archived')
    expect(placement.bucketId).toBe(`${EPIC_BUCKET_PREFIX}year:2020`)
  })

  it('honours agentIds gating for in-progress classification', () => {
    const cards = [mapCard('w', [], 'agent-1', undefined, 'doing')]
    expect(epicWorkflowPlacement(cards[0]!, cards, { agentIds: new Set(['agent-1']) }).category).toBe('in-progress')
    expect(epicWorkflowPlacement(cards[0]!, cards, { agentIds: new Set() }).category).toBe('to-start')
  })

  it('returns the scheduler node id for a scheduler-bound planned workflow', () => {
    const cards = [
      card('sched-1', { type: 'scheduler', raw: '' }),
      mapCard('inst::ts::w1', [], undefined, undefined, 'doing'),
    ]
    cards[1]!.data = { ...cards[1]!.data, instance_of: 'tpl::w1', scheduler_card_id: 'sched-1' }
    const placement = epicWorkflowPlacement(cards[1]!, cards, { agentIds: new Set() })
    expect(placement.category).toBe('planned')
    expect(placement.bucketId).toBe('sched-1')
  })
})

describe('categoryContents', () => {
  const NOW = Date.parse('2026-08-14T12:00:00Z')
  const DAY = 86_400_000

  it('returns the map ids of a non-archived category and no buckets', () => {
    const cards = [
      mapCard('w-todo', [], undefined, undefined, 'todo'),
      mapCard('w-done', [], undefined, undefined, 'done'),
      card('t', { type: 'task' }),
    ]
    const contents = categoryContents(cards, 'to-start', { now: NOW })
    expect(contents).toEqual({ mapIds: ['w-todo'], bucketIds: [] })
  })

  it('returns bucket ids only for the archived category', () => {
    const old = new Date(NOW - 10 * DAY).toISOString()
    const cards = [
      { ...mapCard('w-old', [], undefined, undefined, 'done'), modified: old },
      mapCard('w-recent', [], undefined, undefined, 'done'),
    ]
    const archived = categoryContents(cards, 'archived', { now: NOW })
    expect(archived.mapIds).toEqual(['w-old'])
    expect(archived.bucketIds).toEqual([`${EPIC_BUCKET_PREFIX}date:${old.slice(0, 10)}`])
    // The completed category never carries buckets.
    expect(categoryContents(cards, 'completed', { now: NOW }).bucketIds).toEqual([])
  })

  it('returns scheduler node ids for the planned category', () => {
    const cards = [
      card('sched-1', { type: 'scheduler', raw: '' }),
      mapCard('inst::ts::w1', [], undefined, undefined, 'doing'),
    ]
    cards[1]!.data = { ...cards[1]!.data, instance_of: 'tpl::w1', scheduler_card_id: 'sched-1' }
    const planned = categoryContents(cards, 'planned', { now: NOW, agentIds: new Set() })
    expect(planned.mapIds).toEqual(['inst::ts::w1'])
    expect(planned.bucketIds).toEqual(['sched-1'])
  })
})

describe('bucketWorkflowMapIds', () => {
  const NOW = Date.parse('2026-08-14T12:00:00Z')
  const DAY = 86_400_000

  it('returns the workflow ids inside the bucket', () => {
    const oldA = new Date(NOW - 10 * DAY).toISOString()
    const oldB = new Date(NOW - 40 * DAY).toISOString()
    const cards = [
      { ...mapCard('w-a', [], undefined, undefined, 'done'), modified: oldA },
      { ...mapCard('w-b', [], undefined, undefined, 'done'), modified: oldB },
    ]
    expect(bucketWorkflowMapIds(cards, `${EPIC_BUCKET_PREFIX}date:${oldA.slice(0, 10)}`, { now: NOW })).toEqual(['w-a'])
    expect(bucketWorkflowMapIds(cards, `${EPIC_BUCKET_PREFIX}month:${oldB.slice(0, 7)}`, { now: NOW })).toEqual(['w-b'])
  })

  it('returns empty for an unknown bucket', () => {
    expect(bucketWorkflowMapIds([], `${EPIC_BUCKET_PREFIX}year:1999`, { now: NOW })).toEqual([])
  })
})

describe('visibleWorkflowMapIds', () => {
  const NOW = Date.parse('2026-08-14T12:00:00Z')
  const DAY = 86_400_000
  const noFold = { foldedWorkflows: new Set<string>(), foldedCategories: new Set<string>(), expandedBuckets: new Set<string>() }

  it('includes every unfolded workflow when nothing is folded', () => {
    const cards = [
      mapCard('w-doing', [], 'agent-1', undefined, 'doing'),
      mapCard('w-todo', [], undefined, undefined, 'todo'),
    ]
    const visible = visibleWorkflowMapIds(cards, noFold, { now: NOW, agentIds: new Set(['agent-1']) })
    expect([...visible].sort()).toEqual(['w-doing', 'w-todo'])
  })

  it('excludes individually folded workflows', () => {
    const cards = [mapCard('w-todo', [], undefined, undefined, 'todo')]
    const visible = visibleWorkflowMapIds(cards, { ...noFold, foldedWorkflows: new Set(['w-todo']) }, { now: NOW })
    expect(visible.size).toBe(0)
  })

  it('excludes workflows under a folded category band even when not individually folded', () => {
    const cards = [
      mapCard('w-todo', [], undefined, undefined, 'todo'),
      mapCard('w-tpl', [], undefined, undefined, 'todo'), // standalone? no — template category is standalone only
    ]
    const visible = visibleWorkflowMapIds(cards, { ...noFold, foldedCategories: new Set(['to-start']) }, { now: NOW })
    expect(visible.size).toBe(0)
  })

  it('excludes archived workflows whose time bucket is not expanded', () => {
    const old = new Date(NOW - 10 * DAY).toISOString()
    const cards = [{ ...mapCard('w-old', [], undefined, undefined, 'done'), modified: old }]
    // Bucket folded (default): hidden.
    expect(visibleWorkflowMapIds(cards, noFold, { now: NOW }).size).toBe(0)
    // Bucket expanded: visible.
    const bucketId = `${EPIC_BUCKET_PREFIX}date:${old.slice(0, 10)}`
    const visible = visibleWorkflowMapIds(cards, { ...noFold, expandedBuckets: new Set([bucketId]) }, { now: NOW })
    expect(visible.has('w-old')).toBe(true)
  })

  it('folded archived band hides workflows even when their bucket is expanded', () => {
    const old = new Date(NOW - 10 * DAY).toISOString()
    const bucketId = `${EPIC_BUCKET_PREFIX}date:${old.slice(0, 10)}`
    const cards = [{ ...mapCard('w-old', [], undefined, undefined, 'done'), modified: old }]
    const visible = visibleWorkflowMapIds(cards, {
      foldedWorkflows: new Set<string>(),
      foldedCategories: new Set(['archived']),
      expandedBuckets: new Set([bucketId]),
    }, { now: NOW })
    expect(visible.size).toBe(0)
  })

  it('keeps standalone template maps subject to their own category fold', () => {
    const cards = [{ ...mapCard('w-tpl', [], undefined, undefined, 'todo'), standalone: true }]
    expect(visibleWorkflowMapIds(cards, noFold, { now: NOW }).has('w-tpl')).toBe(true)
    const folded = visibleWorkflowMapIds(cards, { ...noFold, foldedCategories: new Set(['template']) }, { now: NOW })
    expect(folded.size).toBe(0)
  })

  it('excludes planned workflows whose scheduler group is not expanded', () => {
    const cards = [
      card('sched-1', { type: 'scheduler', raw: '' }),
      mapCard('inst::ts::w1', [], undefined, undefined, 'doing'),
    ]
    cards[1]!.data = { ...cards[1]!.data, instance_of: 'tpl::w1', scheduler_card_id: 'sched-1' }
    // Scheduler folded (default): hidden.
    expect(visibleWorkflowMapIds(cards, noFold, { now: NOW, agentIds: new Set() }).size).toBe(0)
    // Scheduler expanded: visible.
    const visible = visibleWorkflowMapIds(cards, { ...noFold, expandedBuckets: new Set(['sched-1']) }, { now: NOW, agentIds: new Set() })
    expect(visible.has('inst::ts::w1')).toBe(true)
  })
})

describe('workflow parent index (hot path)', () => {
  it('builds layouts for many workflows with a bounded number of full cards scans', () => {
    // 50 workflow maps × 4 parented tasks = 250 cards. Tasks carry no
    // scope/include entry, so every workflowTaskIds resolution must go through
    // the parent index — the exact path that used to rescan all 250 cards per
    // workflow (O(workflows × cards)).
    const cards: MonoCardListItem[] = []
    for (let w = 0; w < 50; w++) {
      const mapId = `wf-${w}`
      cards.push(mapCard(mapId, []))
      for (let t = 0; t < 4; t++) {
        cards.push(card(`${mapId}-t${t}`, { type: 'task', parent: mapId }))
      }
    }

    const before = workflowParentIndexBuildCount()
    const layout = buildEpicLayout(cards, { expandedWorkflows: new Set(['wf-0']) })
    const after = workflowParentIndexBuildCount()

    // One full scan builds the shared index — not 50 (one per workflow map).
    expect(after - before).toBeLessThanOrEqual(1)
    // Sanity: the layout actually resolved the parented tasks.
    expect(layout.nodes.some(n => n.cardId === 'wf-0-t0')).toBe(true)

    // Same cards array again: the WeakMap cache must absorb all scans.
    const beforeAgain = workflowParentIndexBuildCount()
    buildEpicLayout(cards, { direction: 'LTR' })
    buildWorkflowLayout(cards, { direction: 'TTB' })
    expect(workflowParentIndexBuildCount() - beforeAgain).toBe(0)
  })
})
