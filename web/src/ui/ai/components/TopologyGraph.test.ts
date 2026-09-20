import { describe, expect, it } from 'vitest'
import type { MonoCardListItem } from '../../../domain/mono-types'
import type { AgentRuntimeState } from '../../../gen-clients/system/types'
import type { AgentInfo, AgentInfoSnapshot } from '../hooks/agentInfoStore'
import type { CustomFilter } from './topoFiltersStore'
import { actorIdTimestampMs, buildAgentBoundPairs, buildTopologyEdges, cardByIdForCards, dependsOnEdgeSampleCount, linkedPartnerForSelection, timeHeatRatio, virtualAgentNodeIds, virtualAgentNodePairs, type AgentBoundPair } from './TopologyGraph'
import { filterCardsByTime, matchesField, collectValues, matchTitle, computeTimeBounds, buildChildMap, buildParentMap, collectDescendants, lineageSet, filterLabel, isStructuralField, applyCustomFilters } from './TopologyModeView'

function card(id: string, overrides: Partial<MonoCardListItem> = {}): MonoCardListItem {
  return {
    id: id,
    tags: [],
    list: [],
    modified: '',
    ...overrides,
  }
}

describe('depends_on edge animation helpers', () => {
  it('reuses its card index while the cards reference is unchanged', () => {
    const cards = [card('first'), card('second')]
    const firstIndex = cardByIdForCards(cards)

    expect(cardByIdForCards(cards)).toBe(firstIndex)
    expect(firstIndex.get('second')).toBe(cards[1])
    expect(cardByIdForCards([...cards])).not.toBe(firstIndex)
  })

  it('adapts Bezier sampling to the visible edge length within bounds', () => {
    expect(dependsOnEdgeSampleCount(0)).toBe(8)
    expect(dependsOnEdgeSampleCount(85)).toBe(9)
    expect(dependsOnEdgeSampleCount(400)).toBe(40)
    expect(dependsOnEdgeSampleCount(1000)).toBe(40)
  })
})

describe('topology time heat', () => {
  it('reads the creation timestamp from a canonical actor ID', () => {
    expect(actorIdTimestampMs('019f30e43afd00000000000000000004')).toBe(Number.parseInt('019f30e43afd', 16))
    expect(actorIdTimestampMs('not-an-actor-id')).toBeNull()
  })

  it('uses the same curve with stronger separation near the present', () => {
    expect(timeHeatRatio(0, 0, 100)).toBe(0)
    expect(timeHeatRatio(50, 0, 100)).toBeCloseTo(1 - Math.log1p(4.5) / Math.log1p(9))
    expect(timeHeatRatio(90, 0, 100) - timeHeatRatio(80, 0, 100)).toBeGreaterThan(
      timeHeatRatio(20, 0, 100) - timeHeatRatio(10, 0, 100),
    )
    expect(timeHeatRatio(100, 0, 100)).toBe(1)
  })
})

describe('buildTopologyEdges', () => {
  it('connects agent cards to the builtin agent card by type', () => {
    const edges = buildTopologyEdges([
      card('__builtin_agent__', { data: { builtinRole: 'mount', mountType: 'agent', autoMount: true } }),
      card('agent:syntax-turtle', { type: 'agent', tags: ['agent'] }),
    ])

    expect(edges).toContainEqual({
      from: '__builtin_agent__',
      to: 'agent:syntax-turtle',
      arrows: 'to',
      length: 180,
      color: { color: '#9ca3af', highlight: '#6b7280' },
      dashes: [1, 4],
      kind: 'type',
    })
    expect(edges).toHaveLength(1)
  })

  it('mounts namespaced skill cards by type', () => {
    const edges = buildTopologyEdges([
      card('__builtin_skill__', { data: { builtinRole: 'mount', mountType: 'skill', autoMount: true } }),
      card('skill:plan-module', { type: 'skill' }),
    ])

    expect(edges).toContainEqual({
      from: '__builtin_skill__',
      to: 'skill:plan-module',
      arrows: 'to',
      length: undefined,
      color: { color: '#9ca3af', highlight: '#6b7280' },
      dashes: [1, 4],
      kind: 'type',
    })
  })

  it('keeps parent and type mount edges as separate relationships', () => {
    const edges = buildTopologyEdges([
      card('__builtin_done__', { data: { builtinRole: 'mount', mountType: 'task', mountStatus: 'done', autoMount: true } }),
      card('plan'),
      card('task', { type: 'task', status: 'done', parent: 'plan' }),
    ])

    expect(edges).toContainEqual({ from: 'plan', to: 'task', arrows: 'to', length: undefined, color: undefined, dashes: undefined, kind: 'parent' })
    expect(edges).toContainEqual({ from: '__builtin_done__', to: 'task', arrows: 'to', length: undefined, color: { color: '#9ca3af', highlight: '#6b7280' }, dashes: [1, 4], kind: 'type' })
  })

  it('keeps parent and list relationships while deduplicating them', () => {
    const edges = buildTopologyEdges([
      card('parent', { list: ['child'] }),
      card('child', { parent: 'parent' }),
    ])

    expect(edges).toEqual([{ from: 'parent', to: 'child', arrows: 'to', length: undefined, color: undefined, dashes: undefined, kind: 'parent' }])
  })

  it('does not connect standalone cards', () => {
    const edges = buildTopologyEdges([
      card('__builtin_agent__'),
      card('agent:hidden', { tags: ['__builtin_agent__'], standalone: true }),
    ])

    expect(edges).toEqual([])
  })

  it('skips wikiword edges when card.raw is empty (lightweight list mode)', () => {
    // In lightweight list mode, list items have no raw. Wikiword edges must
    // be skipped — they are hidden by default and can be supplemented via
    // getCard when the user enables them. Parent/type/tag edges still work.
    const edges = buildTopologyEdges([
      card('parent', { list: ['child'] }),
      card('child', { parent: 'parent' }),
    ])
    expect(edges.some(e => e.kind === 'wikiword')).toBe(false)
    expect(edges).toHaveLength(1) // only the parent edge
  })

  it('creates wikiword edges when card.raw contains [[links]]', () => {
    const edges = buildTopologyEdges([
      card('a', { raw: '---\nid: a\n---\nSee [[b]] for details.' }),
      card('b'),
    ])
    const wikiEdges = edges.filter(e => e.kind === 'wikiword')
    expect(wikiEdges).toHaveLength(1)
    expect(wikiEdges[0]).toMatchObject({ from: 'a', to: 'b' })
  })

  describe('agent spawn hierarchy', () => {
    it('connects a spawned worker to its parent agent card via ParentAgentId', () => {
      const edges = buildTopologyEdges(
        [
          card('agent:main-agent', { type: 'agent', tags: ['agent'] }),
          card('agent:worker-1', { type: 'agent', tags: ['agent'] }),
        ],
        [],
        snapshotWith(
          agentInfo('main-agent', { ActorId: 'actor-main' }),
          agentInfo('worker-1', { ActorId: 'actor-worker', ParentAgentId: 'actor-main' }),
        ),
      )

      expect(edges).toContainEqual({
        from: 'agent:main-agent',
        to: 'agent:worker-1',
        arrows: 'to',
        length: 180,
        color: undefined,
        dashes: undefined,
        kind: 'parent',
      })
    })

    it('falls back to treating ParentAgentId as an agent id when no actor mapping exists', () => {
      const edges = buildTopologyEdges(
        [
          card('agent:main-agent', { type: 'agent', tags: ['agent'] }),
          card('agent:worker-1', { type: 'agent', tags: ['agent'] }),
        ],
        [],
        snapshotWith(
          agentInfo('main-agent'),
          agentInfo('worker-1', { ParentAgentId: 'main-agent' }),
        ),
      )

      expect(edges.some(e => e.kind === 'parent' && e.from === 'agent:main-agent' && e.to === 'agent:worker-1')).toBe(true)
    })

    it('draws no hierarchy edge without a snapshot or with a missing parent', () => {
      const cards = [
        card('agent:main-agent', { type: 'agent', tags: ['agent'] }),
        card('agent:worker-1', { type: 'agent', tags: ['agent'] }),
      ]
      expect(buildTopologyEdges(cards).filter(e => e.kind === 'parent')).toEqual([])
      const ghost = buildTopologyEdges(
        cards,
        [],
        snapshotWith(agentInfo('worker-1', { ActorId: 'actor-worker', ParentAgentId: 'actor-ghost' })),
      )
      expect(ghost.filter(e => e.kind === 'parent')).toEqual([])
    })

    it('ignores a self-referencing ParentAgentId', () => {
      const edges = buildTopologyEdges(
        [card('agent:worker-1', { type: 'agent', tags: ['agent'] })],
        [],
        snapshotWith(agentInfo('worker-1', { ActorId: 'actor-worker', ParentAgentId: 'actor-worker' })),
      )
      expect(edges.filter(e => e.kind === 'parent')).toEqual([])
    })
  })


  describe('filterCardsByTime', () => {
    it('returns all cards when both bounds are open', () => {
      const cards = [card('a', { modified: '2020-01-01T00:00:00Z' })]
      expect(filterCardsByTime(cards, 0, 0)).toBe(cards)
    })

    it('filters disconnected virtual mount nodes outside the time range', () => {
      const cutoff = Date.now() - 7 * 24 * 60 * 60 * 1000
      const cards = [
        card('__builtin_done__', { modified: '1970-01-01T00:00:00Z' }),
        card('stale', { modified: '1970-01-01T00:00:00Z' }),
      ]
      expect(filterCardsByTime(cards, cutoff, 0).map(c => c.id)).toEqual([])
    })

    it('keeps a virtual mount node connected to a matching card', () => {
      const cutoff = Date.now() - 7 * 24 * 60 * 60 * 1000
      const cards = [
        card('__builtin_done__', { modified: '1970-01-01T00:00:00Z', list: ['recent'] }),
        card('recent', { modified: new Date().toISOString() }),
      ]
      const childMap = buildChildMap(cards)
      const parentMap = buildParentMap(cards)
      expect(filterCardsByTime(cards, cutoff, 0, childMap, parentMap).map(c => c.id)).toEqual(['__builtin_done__', 'recent'])
    })

    it('filters cards older than the lower bound', () => {
      const now = new Date().toISOString()
      const old = new Date(Date.now() - 10 * 24 * 60 * 60 * 1000).toISOString()
      const cutoff = Date.now() - 7 * 24 * 60 * 60 * 1000
      const cards = [
        card('recent', { modified: now }),
        card('stale', { modified: old }),
      ]
      const filtered = filterCardsByTime(cards, cutoff, 0)
      expect(filtered.map(c => c.id)).toEqual(['recent'])
    })

    it('filters cards newer than the upper bound (range)', () => {
      const old = '2020-01-01T00:00:00Z'
      const future = '2050-01-01T00:00:00Z'
      const upper = new Date('2030-01-01T00:00:00Z').getTime()
      const cards = [card('old', { modified: old }), card('future', { modified: future })]
      const filtered = filterCardsByTime(cards, 0, upper)
      expect(filtered.map(c => c.id)).toEqual(['old'])
    })

    it('excludes cards with unparseable modified timestamps', () => {
      const cards = [
        card('bad', { modified: '' }),
        card('bad2', { modified: 'not-a-date' }),
      ]
      expect(filterCardsByTime(cards, Date.now() - 24 * 60 * 60 * 1000, 0)).toEqual([])
    })
  })

  describe('computeTimeBounds (ago direction)', () => {
    const base = { mode: 'ago' as const, agoValue: '7', agoUnit: 'days' as const, rangeFrom: '', rangeTo: '' }
    const DAY = 24 * 60 * 60 * 1000

    it('after direction sets a lower bound (within last N)', () => {
      const { lower, upper } = computeTimeBounds({ ...base, agoDirection: 'after' })
      expect(upper).toBe(0)
      expect(lower).toBeGreaterThan(Date.now() - 8 * DAY)
      expect(lower).toBeLessThan(Date.now() - 6 * DAY)
    })

    it('before direction sets an upper bound (older than N ago)', () => {
      const { lower, upper } = computeTimeBounds({ ...base, agoDirection: 'before' })
      expect(lower).toBe(0)
      expect(upper).toBeGreaterThan(Date.now() - 8 * DAY)
      expect(upper).toBeLessThan(Date.now() - 6 * DAY)
    })

    it('returns open bounds for a non-positive value', () => {
      expect(computeTimeBounds({ ...base, agoValue: '0', agoDirection: 'after' })).toEqual({ lower: 0, upper: 0 })
      expect(computeTimeBounds({ ...base, agoValue: 'abc', agoDirection: 'before' })).toEqual({ lower: 0, upper: 0 })
    })
  })

  describe('matchesField', () => {
    it('matches title by substring (case-insensitive)', () => {
      expect(matchesField(card('a', { id: 'Sprint Review' }), 'title', 'sprint')).toBe(true)
      expect(matchesField(card('a', { id: 'Sprint Review' }), 'title', 'retro')).toBe(false)
    })

    it('matches tag by membership', () => {
      expect(matchesField(card('a', { tags: ['kanban', 'todo'] }), 'tag', 'todo')).toBe(true)
      expect(matchesField(card('a', { tags: ['kanban'] }), 'tag', 'done')).toBe(false)
    })

    it('matches status / type / source by exact value', () => {
      expect(matchesField(card('a', { status: 'in-progress' }), 'status', 'in-progress')).toBe(true)
      expect(matchesField(card('a', { status: 'done' }), 'status', 'in-progress')).toBe(false)
      expect(matchesField(card('a', { type: 'agent' }), 'type', 'agent')).toBe(true)
      expect(matchesField(card('a', { source: 'project' }), 'source', 'project')).toBe(true)
    })

    it('treats an empty value as a match-all', () => {
      expect(matchesField(card('a', { id: 'x' }), 'title', '')).toBe(true)
    })
  })

  describe('matchTitle', () => {
    it('matches substring case-insensitively without delimiters', () => {
      expect(matchTitle('Sprint Review', 'sprint')).toBe(true)
      expect(matchTitle('Sprint Review', 'retro')).toBe(false)
    })

    it('matches via /pattern/ regexp with flags', () => {
      expect(matchTitle('Sprint 42 Review', '/^sprint\\s+\\d+/i')).toBe(true)
      expect(matchTitle('Retro', '/^sprint/i')).toBe(false)
    })

    it('treats an invalid regexp inside delimiters as no match', () => {
      expect(matchTitle('anything', '/(/')).toBe(false)
    })
  })

  describe('collectValues', () => {
    it('collects unique sorted values for a field', () => {
      const cards = [
        card('a', { status: 'done', tags: ['x', 'y'] }),
        card('b', { status: 'todo', tags: ['y'] }),
      ]
      expect(collectValues(cards, 'status')).toEqual(['done', 'todo'])
      expect(collectValues(cards, 'tag')).toEqual(['x', 'y'])
    })
  })

  it('unifies type-driven connections across all auto-mount types', () => {
    const edges = buildTopologyEdges([
      card('__builtin_task__', { data: { builtinRole: 'aggregator', mountType: 'task', autoMount: true } }),
      card('__builtin_todo__', { data: { builtinRole: 'mount', mountType: 'task', mountStatus: 'todo', autoMount: true } }),
      card('__builtin_skill__', { data: { builtinRole: 'mount', mountType: 'skill', autoMount: true } }),
      card('__builtin_prompt__', { data: { builtinRole: 'mount', mountType: 'prompt', autoMount: true } }),
      card('__builtin_agent__', { data: { builtinRole: 'mount', mountType: 'agent', autoMount: true } }),
      card('task-1', { type: 'task', status: 'todo' }),
      card('skill:plan-module', { type: 'skill' }),
      card('prompt:profile:project.coder', { type: 'prompt' }),
      card('agent:syntax-turtle', { type: 'agent' }),
    ])

    const typeColor = { color: '#9ca3af', highlight: '#6b7280' }
    expect(edges).toContainEqual({ from: '__builtin_todo__', to: 'task-1', arrows: 'to', length: undefined, color: typeColor, dashes: [1, 4], kind: 'type' })
    expect(edges).toContainEqual({ from: '__builtin_skill__', to: 'skill:plan-module', arrows: 'to', length: undefined, color: typeColor, dashes: [1, 4], kind: 'type' })
    expect(edges).toContainEqual({ from: '__builtin_prompt__', to: 'prompt:profile:project.coder', arrows: 'to', length: undefined, color: typeColor, dashes: [1, 4], kind: 'type' })
    expect(edges).toContainEqual({ from: '__builtin_agent__', to: 'agent:syntax-turtle', arrows: 'to', length: 180, color: typeColor, dashes: [1, 4], kind: 'type' })
  })

  it('adds short cyan agent_binding edges from runtime BoundTaskCardId', () => {
    const edges = buildTopologyEdges(
      [
        card('agent:a1', { type: 'agent', tags: ['agent'], data: { agentId: 'a1' } }),
        card('task-1', { type: 'task' }),
      ],
      [],
      snapshotWith(agentInfo('a1', { Runtime: { BoundTaskCardId: 'task-1' } as AgentRuntimeState })),
    )

    expect(edges).toContainEqual({
      id: 'agent:a1→task-1:binding',
      from: 'agent:a1',
      to: 'task-1',
      arrows: '',
      length: 50,
      width: 1,
      color: { color: '#06b6d4', highlight: '#22d3ee' },
      dashes: false,
      kind: 'agent_binding',
    })
  })

  it('uses the spawn-time binding when an agent card has no runtime state', () => {
    const edges = buildTopologyEdges(
      [
        card('agent:a1', { type: 'agent', tags: ['agent'], data: { agentId: 'a1' } }),
        card('task-1', { type: 'task' }),
      ],
      [],
      snapshotWith(agentInfo('a1', { BoundTaskCardId: 'task-1' })),
    )

    expect(edges).toContainEqual({
      id: 'agent:a1→task-1:binding',
      from: 'agent:a1',
      to: 'task-1',
      arrows: '',
      length: 50,
      width: 1,
      color: { color: '#06b6d4', highlight: '#22d3ee' },
      dashes: false,
      kind: 'agent_binding',
    })
  })

  it('adds cyan owner→map binding edges from workflow ownerAgentId', () => {
    const edges = buildTopologyEdges([
      card('agent:a1', { type: 'agent', tags: ['agent'], data: { agentId: 'a1' } }),
      card('map-1', { type: 'workflow', data: { ownerAgentId: 'a1' } }),
    ])

    expect(edges).toContainEqual({
      id: 'agent:a1→map-1:binding',
      from: 'agent:a1',
      to: 'map-1',
      arrows: 'to',
      length: 180,
      width: 1,
      color: { color: '#06b6d4', highlight: '#22d3ee' },
      dashes: false,
      kind: 'agent_binding',
    })
  })

  it('generates owner→root binding edge when owner has no mono card but is in snapshot', () => {
    // The owner agent has no entity mono card, but exists as a live agent.
    // The agent_binding edge must still be generated.
    const edges = buildTopologyEdges(
      [card('map-1', { type: 'workflow', data: { ownerAgentId: 'arch-1' } })],
      [],
      snapshotWith(agentInfo('arch-1')),
    )

    expect(edges).toContainEqual({
      id: 'agent:arch-1→map-1:binding',
      from: 'agent:arch-1',
      to: 'map-1',
      arrows: 'to',
      length: 180,
      width: 1,
      color: { color: '#06b6d4', highlight: '#22d3ee' },
      dashes: false,
      kind: 'agent_binding',
    })
  })

  it('does not generate a dangling owner→root edge when owner is stale', () => {
    const edges = buildTopologyEdges(
      [card('map-1', { type: 'workflow', data: { ownerAgentId: 'ghost' } })],
      [],
      snapshotWith(agentInfo('real')),
    )

    expect(edges.filter(e => e.kind === 'agent_binding')).toEqual([])
  })

  it('resolves owner actorId to agent id card id in edge generation', () => {
    const edges = buildTopologyEdges(
      [card('map-1', { type: 'workflow', data: { ownerAgentId: 'actor-xyz' } })],
      [],
      snapshotWith(agentInfo('uuid-789', { ActorId: 'actor-xyz' })),
    )

    expect(edges).toContainEqual({
      id: 'agent:uuid-789→map-1:binding',
      from: 'agent:uuid-789',
      to: 'map-1',
      arrows: 'to',
      length: 180,
      width: 1,
      color: { color: '#06b6d4', highlight: '#22d3ee' },
      dashes: false,
      kind: 'agent_binding',
    })
  })
})

describe('subtree / lineage filters', () => {
  // Hierarchy:
  //   root
  //   ├── child1
  //   │   └── grandchild
  //   └── child2
  const cards = [
    card('root', { list: ['child1', 'child2'] }),
    card('child1', { parent: 'root', list: ['grandchild'] }),
    card('child2', { parent: 'root' }),
    card('grandchild', { parent: 'child1' }),
  ]
  const childMap = buildChildMap(cards)
  const parentMap = buildParentMap(cards)

  it('buildChildMap derives parent -> children from both list and parent', () => {
    expect([...childMap.get('root')!].sort()).toEqual(['child1', 'child2'])
    expect([...childMap.get('child1')!]).toEqual(['grandchild'])
    expect([...childMap.get('child2')!]).toEqual([])
  })

  it('collectDescendants walks the full subtree (excluding the root)', () => {
    expect([...collectDescendants('root', childMap)].sort()).toEqual(['child1', 'child2', 'grandchild'])
    expect([...collectDescendants('child1', childMap)].sort()).toEqual(['grandchild'])
    // A leaf has no descendants.
    expect([...collectDescendants('child2', childMap)]).toEqual([])
  })

  it('subtree filter keeps the root plus all descendants', () => {
    const subtree = new Set(['root', ...collectDescendants('root', childMap)])
    expect([...subtree].sort()).toEqual(['child1', 'child2', 'grandchild', 'root'])
  })

  it('lineageSet keeps the node plus descendants and ancestors', () => {
    // child1 lineage = {child1, grandchild (down), root (up)}
    expect([...lineageSet('child1', childMap, parentMap)].sort()).toEqual(['child1', 'grandchild', 'root'])
    // grandchild lineage = {grandchild, child1, root} (full chain to root)
    expect([...lineageSet('grandchild', childMap, parentMap)].sort()).toEqual(['child1', 'grandchild', 'root'])
    // root lineage = whole subtree (no ancestors)
    expect([...lineageSet('root', childMap, parentMap)].sort()).toEqual(['child1', 'child2', 'grandchild', 'root'])
  })
})

describe('filterLabel', () => {
  const titleMap = new Map<string, string>([
    ['root', 'Root Card'],
    ['child1', 'Child One'],
  ])

  it('renders a subtree filter with the field label and resolved card title', () => {
    expect(filterLabel('subtree', 'root', titleMap)).toBe('Subtree: Root Card')
  })

  it('renders a lineage filter with the field label and resolved card title', () => {
    expect(filterLabel('lineage', 'child1', titleMap)).toBe('Lineage: Child One')
  })

  it('falls back to the raw value when the card id is unknown', () => {
    expect(filterLabel('subtree', 'missing', titleMap)).toBe('Subtree: missing')
  })

  it('renders a free-text field verbatim (no title lookup)', () => {
    expect(filterLabel('title', 'sprint', titleMap)).toBe('Title: sprint')
  })

  it('uses the supplied label map (i18n)', () => {
    const zh = { title: '标题', status: '状态', tag: '标签', type: '类型', source: '来源', subtree: '子树', lineage: '关联树' }
    expect(filterLabel('subtree', 'root', titleMap, zh)).toBe('子树: Root Card')
    expect(filterLabel('lineage', 'child1', titleMap, zh)).toBe('关联树: Child One')
  })
})

describe('isStructuralField', () => {
  it('true for subtree and lineage, false for free-text fields', () => {
    expect(isStructuralField('subtree')).toBe(true)
    expect(isStructuralField('lineage')).toBe(true)
    expect(isStructuralField('title')).toBe(false)
    expect(isStructuralField('tag')).toBe(false)
  })
})

// Regression: builtin grouping nodes (e.g. __builtin_agent__) reach their
// children only via tags, not via parent/list. The subtree and lineage filters
// must walk those tag-derived relationships, otherwise filtering a builtin node
// buildChildMap / buildParentMap must also derive type-driven virtual mount
// relationships, otherwise filtering a builtin node returns nothing even though
// the graph clearly shows its children.
describe('builtin node subtree / lineage via type mounts', () => {
  const cards = [
    card('__builtin_agent__', { data: { builtinRole: 'mount', mountType: 'agent', autoMount: true } }),
    card('__builtin_skill__', { data: { builtinRole: 'mount', mountType: 'skill', autoMount: true } }),
    card('agent:syntax-turtle', { type: 'agent', tags: ['agent'] }),
    card('agent:plan-beaver', { type: 'agent' }),
    card('skill:plan-module', { type: 'skill' }),
  ]

  it("buildChildMap links a builtin node to every card mounted by type", () => {
    const childMap = buildChildMap(cards)
    expect([...childMap.get('__builtin_agent__')!].sort())
      .toEqual(['agent:plan-beaver', 'agent:syntax-turtle'])
    expect([...childMap.get('__builtin_skill__')!]).toEqual(['skill:plan-module'])
  })

  it("subtree filter on a builtin keeps the builtin plus all type-mounted children", () => {
    const childMap = buildChildMap(cards)
    const subtree = new Set(['__builtin_agent__', ...collectDescendants('__builtin_agent__', childMap)])
    expect([...subtree].sort()).toEqual(['__builtin_agent__', 'agent:plan-beaver', 'agent:syntax-turtle'])
  })

  it("lineage filter on a type-mounted child reaches the builtin ancestor", () => {
    const childMap = buildChildMap(cards)
    const parentMap = buildParentMap(cards)
    expect([...lineageSet('agent:plan-beaver', childMap, parentMap)].sort())
      .toEqual(['__builtin_agent__', 'agent:plan-beaver'])
  })

  it("edges and filters share the same type resolution (no duplicates)", () => {
    const edges = buildTopologyEdges(cards).filter(e => e.from === '__builtin_agent__')
    const targets = edges.map(e => e.to).sort()
    expect(targets).toEqual(['agent:plan-beaver', 'agent:syntax-turtle'])
    expect(edges.filter(e => e.to === 'agent:syntax-turtle')).toHaveLength(1)
  })
})

// Regression (buildParentMap): a parent declaring a child only via its `list`
// field (the child has no `parent` of its own) used to break the lineage
// filter's upward walk, because buildParentMap only read `parent` and skipped
// the `list` reverse. It now mirrors buildChildMap / backend buildCardIndex.
describe('lineage via list-only relationship', () => {
  const cards = [
    card('root', { list: ['child'] }),
    card('child'),
  ]
  const childMap = buildChildMap(cards)
  const parentMap = buildParentMap(cards)

  it('buildParentMap derives the parent from the list reverse', () => {
    expect([...(parentMap.get('child') ?? [])]).toContain('root')
  })

  it('lineage reaches the ancestor expressed only via the parent list field', () => {
    expect([...lineageSet('child', childMap, parentMap)].sort()).toEqual(['child', 'root'])
  })
})

// Regression (virtual node retention): under a structural (subtree/lineage)
// filter, virtual mount nodes unrelated to the selected branch used to be kept
// unconditionally, leaving isolated builtin nodes in the graph. They must now
// survive only when they lie on the branch (e.g. the virtual ancestor reached
// by lineage). Property/time filters still exempt every virtual node so the
// skeleton does not collapse.
describe('applyCustomFilters — virtual mount node retention', () => {
  const cards = [
    card('__builtin_skill__', { data: { builtinRole: 'mount', mountType: 'skill', autoMount: true } }),
    card('__builtin_task__', { data: { builtinRole: 'mount', mountType: 'task', mountStatus: 'todo', autoMount: true } }),
    card('skillX', { type: 'skill' }),
    card('taskA', { type: 'task', status: 'todo' }),
  ]
  const childMap = buildChildMap(cards)
  const parentMap = buildParentMap(cards)
  const cf = (field: CustomFilter['field'], value: string, operator?: CustomFilter['operator']): CustomFilter =>
    ({ id: 'cf', field, value, operator })

  it('subtree filter keeps connected virtual mount nodes', () => {
    const out = applyCustomFilters(cards, [cf('subtree', 'skillX')], childMap, parentMap)
    expect(out.map(c => c.id).sort()).toEqual(['__builtin_skill__', 'skillX'])
  })

  it('lineage filter keeps only the virtual ancestor on the branch', () => {
    const out = applyCustomFilters(cards, [cf('lineage', 'skillX')], childMap, parentMap)
    expect(out.map(c => c.id).sort()).toEqual(['__builtin_skill__', 'skillX'])
  })

  it('lineage filter on taskA keeps its own virtual ancestor, drops the skill node', () => {
    const out = applyCustomFilters(cards, [cf('lineage', 'taskA')], childMap, parentMap)
    expect(out.map(c => c.id).sort()).toEqual(['__builtin_task__', 'taskA'])
  })

  it('property filter keeps only connected virtual mount nodes', () => {
    const out = applyCustomFilters(cards, [cf('type', 'skill')], childMap, parentMap)
    expect(out.map(c => c.id).sort()).toEqual(['__builtin_skill__', 'skillX'])
  })
})

function agentInfo(id: string, overrides: Partial<AgentInfo> = {}): AgentInfo {
  return {
    Id: id,
    ActorId: '',
    DisplayName: id,
    Title: id,
    HasTitle: true,
    AgentKind: 'worker',
    ProjectId: '',
    ProjectName: '',
    Status: 'running',
    StatusLabel: 'running',
    IsWorking: true,
    IsError: false,
    IsCompleted: false,
    IsAskUserPermission: false,
    IsAskUser: false,
    IsAskPermission: false,
    IsPlanApproval: false, IsGoalSubmit: false,
    Degraded: false,
    Runtime: undefined,
    CompactionPolicyLoading: false,
    CompactionPolicyError: undefined,
    CanDelete: false,
    ...overrides,
  }
}

function snapshotWith(...agents: AgentInfo[]): AgentInfoSnapshot {
  return {
    version: 1,
    loading: false,
    items: agents,
    byId: new Map(agents.map(a => [a.Id, a])),
    byActorId: new Map(agents.filter(a => a.ActorId).map(a => [a.ActorId, a])),
  }
}

describe('buildAgentBoundPairs', () => {
  it('reuses pairs for unchanged cards and snapshot references', () => {
    const cards = [
      card('agent:a1', { type: 'agent', tags: ['agent'] }),
      card('task-1', { type: 'task', data: { agentId: 'a1' } }),
    ]
    const snapshot = snapshotWith(agentInfo('a1'))
    const pairs = buildAgentBoundPairs(cards, snapshot)

    expect(buildAgentBoundPairs(cards, snapshot)).toBe(pairs)
    expect(buildAgentBoundPairs(cards, snapshotWith(agentInfo('a1')))).not.toBe(pairs)
    expect(buildAgentBoundPairs(cards)).toBe(buildAgentBoundPairs(cards))
    expect(buildAgentBoundPairs([...cards], snapshot)).not.toBe(pairs)
  })

  it('pairs a non-agent card carrying data.agentId with its agent card', () => {
    const pairs = buildAgentBoundPairs([
      card('agent:a1', { type: 'agent', tags: ['agent'] }),
      card('task-1', { type: 'task', data: { agentId: 'a1' } }),
    ])
    expect(pairs).toEqual([{ cardId: 'task-1', agentCardId: 'agent:a1', agentId: 'a1', bindingKind: 'frontmatter' }])
  })

  it('pairs workflow map cards with their owner agent', () => {
    const pairs = buildAgentBoundPairs([
      card('agent:a1', { type: 'agent', tags: ['agent'] }),
      card('map-1', { type: 'workflow', data: { ownerAgentId: 'a1' } }),
    ])
    expect(pairs).toEqual([{ cardId: 'map-1', agentCardId: 'agent:a1', agentId: 'a1', bindingKind: 'owner' }])
  })

  it('pairs owner agent without entity mono card via snapshot', () => {
    // The owner agent has no mono card in the list, but is a live agent in the
    // snapshot. The pair must still be generated so the binding edge is drawn.
    const pairs = buildAgentBoundPairs(
      [card('map-1', { type: 'workflow', data: { ownerAgentId: 'arch-1' } })],
      snapshotWith(agentInfo('arch-1')),
    )
    expect(pairs).toEqual([{ cardId: 'map-1', agentCardId: 'agent:arch-1', agentId: 'arch-1', bindingKind: 'owner' }])
  })

  it('resolves owner actorId to agent id via snapshot', () => {
    // ownerAgentId is an actor ID that differs from the agent's Id.
    // The pair must resolve to the Id-based card id, not the raw actor id.
    const pairs = buildAgentBoundPairs(
      [card('map-1', { type: 'workflow', data: { ownerAgentId: 'actor-abc' } })],
      snapshotWith(agentInfo('uuid-123', { ActorId: 'actor-abc' })),
    )
    expect(pairs).toEqual([{ cardId: 'map-1', agentCardId: 'agent:uuid-123', agentId: 'uuid-123', bindingKind: 'owner' }])
  })

  it('does not generate a dangling edge when owner is missing or stale', () => {
    // Owner references an agent that is neither in the cards list nor in the
    // snapshot — no edge should be generated.
    const pairs = buildAgentBoundPairs(
      [card('map-1', { type: 'workflow', data: { ownerAgentId: 'ghost-agent' } })],
      snapshotWith(agentInfo('real-agent')),
    )
    expect(pairs).toEqual([])
  })

  it('does not generate a dangling edge when no snapshot is available', () => {
    // Without a snapshot, a virtual agent node cannot be rendered, so no edge.
    const pairs = buildAgentBoundPairs([
      card('map-1', { type: 'workflow', data: { ownerAgentId: 'ghost-agent' } }),
    ])
    expect(pairs).toEqual([])
  })

  it('switches owner edge when ownerAgentId changes', () => {
    const pairsOld = buildAgentBoundPairs(
      [card('map-1', { type: 'workflow', data: { ownerAgentId: 'a1' } })],
      snapshotWith(agentInfo('a1'), agentInfo('a2')),
    )
    const pairsNew = buildAgentBoundPairs(
      [card('map-1', { type: 'workflow', data: { ownerAgentId: 'a2' } })],
      snapshotWith(agentInfo('a1'), agentInfo('a2')),
    )
    expect(pairsOld).toEqual([{ cardId: 'map-1', agentCardId: 'agent:a1', agentId: 'a1', bindingKind: 'owner' }])
    expect(pairsNew).toEqual([{ cardId: 'map-1', agentCardId: 'agent:a2', agentId: 'a2', bindingKind: 'owner' }])
  })

  it('derives pairs from runtime BoundTaskCardId regardless of card frontmatter', () => {
    const pairs = buildAgentBoundPairs(
      [
        card('agent:a1', { type: 'agent', tags: ['agent'], data: { agentId: 'a1' } }),
        card('task-1', { type: 'task' }),
      ],
      snapshotWith(agentInfo('a1', { Runtime: { BoundTaskCardId: 'task-1' } as AgentRuntimeState })),
    )
    expect(pairs).toEqual([{ cardId: 'task-1', agentCardId: 'agent:a1', agentId: 'a1', bindingKind: 'runtime' }])
  })

  it('skips runtime pairs whose bound task card is not in the graph', () => {
    const pairs = buildAgentBoundPairs(
      [card('agent:a1', { type: 'agent', tags: ['agent'], data: { agentId: 'a1' } })],
      snapshotWith(agentInfo('a1', { Runtime: { BoundTaskCardId: 'task-missing' } as AgentRuntimeState })),
    )
    expect(pairs).toEqual([])
  })

  it('skips runtime pairs for standalone cards', () => {
    const pairs = buildAgentBoundPairs(
      [
        card('agent:a1', { type: 'agent', tags: ['agent'], data: { agentId: 'a1' } }),
        card('task-1', { type: 'task', standalone: true }),
      ],
      snapshotWith(agentInfo('a1', { Runtime: { BoundTaskCardId: 'task-1' } as AgentRuntimeState })),
    )
    expect(pairs).toEqual([])
  })

  it('deduplicates pairs surfaced by both frontmatter and runtime', () => {
    const pairs = buildAgentBoundPairs(
      [
        card('agent:a1', { type: 'agent', tags: ['agent'], data: { agentId: 'a1' } }),
        card('task-1', { type: 'task', data: { agentId: 'a1' } }),
      ],
      snapshotWith(agentInfo('a1', { Runtime: { BoundTaskCardId: 'task-1' } as AgentRuntimeState })),
    )
    expect(pairs).toEqual([{ cardId: 'task-1', agentCardId: 'agent:a1', agentId: 'a1', bindingKind: 'frontmatter' }])
  })
})

describe('linkedPartnerForSelection', () => {
  const pairs: AgentBoundPair[] = [{ cardId: 'task-1', agentCardId: 'agent:a1', agentId: 'a1' }]

  it('returns the agent card when the task card is selected', () => {
    expect(linkedPartnerForSelection('task-1', pairs)).toBe('agent:a1')
  })

  it('returns the task card when the agent card is selected', () => {
    expect(linkedPartnerForSelection('agent:a1', pairs)).toBe('task-1')
  })

  it('returns undefined for unbound nodes', () => {
    expect(linkedPartnerForSelection('other', pairs)).toBeUndefined()
  })

  it('returns the workflow root when the owner agent (virtual node) is selected', () => {
    const ownerPairs: AgentBoundPair[] = [
      { cardId: 'map-1', agentCardId: 'agent:arch-1', agentId: 'arch-1', bindingKind: 'owner' },
    ]
    expect(linkedPartnerForSelection('agent:arch-1', ownerPairs)).toBe('map-1')
  })

  it('returns the owner agent when the workflow root is selected', () => {
    const ownerPairs: AgentBoundPair[] = [
      { cardId: 'map-1', agentCardId: 'agent:arch-1', agentId: 'arch-1', bindingKind: 'owner' },
    ]
    expect(linkedPartnerForSelection('map-1', ownerPairs)).toBe('agent:arch-1')
  })
})

describe('virtualAgentNodePairs', () => {
  const ownerPair: AgentBoundPair = {
    cardId: 'map-1', agentCardId: 'agent:arch-1', agentId: 'arch-1', bindingKind: 'owner',
  }
  const frontmatterPair: AgentBoundPair = {
    cardId: 'task-1', agentCardId: 'agent:coder', agentId: 'coder', bindingKind: 'frontmatter',
  }

  it('returns pairs for agents without mono cards when agent_binding is visible', () => {
    const pairs = virtualAgentNodePairs(
      [ownerPair],
      new Set(['map-1']),
    )
    expect(pairs).toEqual([ownerPair])
  })

  it('returns pairs for agents without mono cards when edgeVisibility is undefined', () => {
    const pairs = virtualAgentNodePairs(
      [ownerPair],
      new Set(['map-1']),
      undefined,
    )
    expect(pairs).toEqual([ownerPair])
  })

  it('returns pairs for agents without mono cards when edgeVisibility.agent_binding is true', () => {
    const pairs = virtualAgentNodePairs(
      [ownerPair],
      new Set(['map-1']),
      { agent_binding: true },
    )
    expect(pairs).toEqual([ownerPair])
  })

  it('returns NO pairs when edgeVisibility.agent_binding is false (orphan dot prevention)', () => {
    // The toggle is off — edges are hidden, so virtual nodes must also vanish.
    const pairs = virtualAgentNodePairs(
      [ownerPair],
      new Set(['map-1']),
      { agent_binding: false },
    )
    expect(pairs).toEqual([])
  })

  it('re-creates virtual nodes when the toggle is switched back on', () => {
    // Toggle off → empty
    expect(virtualAgentNodePairs([ownerPair], new Set(['map-1']), { agent_binding: false })).toEqual([])
    // Toggle back on → pair restored
    expect(virtualAgentNodePairs([ownerPair], new Set(['map-1']), { agent_binding: true })).toEqual([ownerPair])
  })

  it('excludes pairs whose agentCardId already has a mono card node', () => {
    // agent:coder has a card node — should not produce a virtual node.
    const pairs = virtualAgentNodePairs(
      [ownerPair, frontmatterPair],
      new Set(['map-1', 'agent:coder']),
    )
    expect(pairs).toEqual([ownerPair])
  })

  it('handles multiple owner agents with no mono cards', () => {
    const secondOwner: AgentBoundPair = {
      cardId: 'map-2', agentCardId: 'agent:arch-2', agentId: 'arch-2', bindingKind: 'owner',
    }
    const pairs = virtualAgentNodePairs(
      [ownerPair, secondOwner],
      new Set(['map-1', 'map-2']),
    )
    expect(pairs).toEqual([ownerPair, secondOwner])
  })

  it('returns empty for no pairs', () => {
    expect(virtualAgentNodePairs([], new Set())).toEqual([])
  })
})

describe('buildAgentBoundPairs — cardless live worker', () => {
  it('pairs a snapshot worker with no mono card via its runtime BoundTaskCardId', () => {
    const pairs = buildAgentBoundPairs(
      [card('task-1', { type: 'task' })],
      snapshotWith(agentInfo('worker-1', { Runtime: { BoundTaskCardId: 'task-1' } as AgentRuntimeState })),
    )
    expect(pairs).toEqual([{ cardId: 'task-1', agentCardId: 'agent:worker-1', agentId: 'worker-1', bindingKind: 'runtime' }])
  })

  it('falls back to the top-level BoundTaskCardId when runtime state is absent', () => {
    const pairs = buildAgentBoundPairs(
      [card('task-1', { type: 'task' })],
      snapshotWith(agentInfo('worker-1', { BoundTaskCardId: 'task-1' })),
    )
    expect(pairs).toEqual([{ cardId: 'task-1', agentCardId: 'agent:worker-1', agentId: 'worker-1', bindingKind: 'runtime' }])
  })

  it('does not duplicate the pair when the worker already has a mono card', () => {
    const pairs = buildAgentBoundPairs(
      [
        card('agent:worker-1', { type: 'agent', tags: ['agent'] }),
        card('task-1', { type: 'task' }),
      ],
      snapshotWith(agentInfo('worker-1', { Runtime: { BoundTaskCardId: 'task-1' } as AgentRuntimeState })),
    )
    expect(pairs).toEqual([{ cardId: 'task-1', agentCardId: 'agent:worker-1', agentId: 'worker-1', bindingKind: 'runtime' }])
  })

  it('ignores a cardless worker bound to a card outside the list', () => {
    const pairs = buildAgentBoundPairs(
      [card('task-1', { type: 'task' })],
      snapshotWith(agentInfo('worker-1', { Runtime: { BoundTaskCardId: 'other-project-task' } as AgentRuntimeState })),
    )
    expect(pairs).toEqual([])
  })
})

describe('agent spawn hierarchy — cardless worker anchoring', () => {
  it('connects a cardless worker to its parent agent card', () => {
    const edges = buildTopologyEdges(
      [card('agent:main-agent', { type: 'agent', tags: ['agent'] })],
      [],
      snapshotWith(
        agentInfo('main-agent', { ActorId: 'actor-main' }),
        agentInfo('worker-1', { ActorId: 'actor-worker', ParentAgentId: 'actor-main' }),
      ),
    )
    expect(edges).toContainEqual({
      from: 'agent:main-agent',
      to: 'agent:worker-1',
      arrows: 'to',
      length: 180,
      color: undefined,
      dashes: undefined,
      kind: 'parent',
    })
  })

  it('extends anchoring down the spawn chain via a bound owner with no card', () => {
    // Owner has no mono card but is anchored by its workflow ownerAgentId
    // binding; the grandchild worker hangs off the cardless owner.
    const edges = buildTopologyEdges(
      [card('map-1', { type: 'workflow', data: { ownerAgentId: 'actor-owner' } })],
      [],
      snapshotWith(
        agentInfo('owner-1', { ActorId: 'actor-owner' }),
        agentInfo('worker-1', { ActorId: 'actor-worker', ParentAgentId: 'actor-owner' }),
      ),
    )
    expect(edges.some(e => e.kind === 'parent' && e.from === 'agent:owner-1' && e.to === 'agent:worker-1')).toBe(true)
  })

  it('keeps workers of unrelated agents out of the graph', () => {
    // Neither the worker nor its parent has a card or binding in this
    // project — no anchor, no edge.
    const edges = buildTopologyEdges(
      [card('task-1', { type: 'task' })],
      [],
      snapshotWith(
        agentInfo('other-owner', { ActorId: 'actor-other' }),
        agentInfo('other-worker', { ActorId: 'actor-other-worker', ParentAgentId: 'actor-other' }),
      ),
    )
    expect(edges.filter(e => e.kind === 'parent')).toEqual([])
  })

  it('drops the edge and node reference once the worker leaves the snapshot', () => {
    const cards = [card('agent:main-agent', { type: 'agent', tags: ['agent'] })]
    const live = buildTopologyEdges(
      cards,
      [],
      snapshotWith(
        agentInfo('main-agent', { ActorId: 'actor-main' }),
        agentInfo('worker-1', { ActorId: 'actor-worker', ParentAgentId: 'actor-main' }),
      ),
    )
    expect(live.some(e => e.kind === 'parent' && e.to === 'agent:worker-1')).toBe(true)
    const tornDown = buildTopologyEdges(cards, [], snapshotWith(agentInfo('main-agent', { ActorId: 'actor-main' })))
    expect(tornDown.filter(e => e.kind === 'parent')).toEqual([])
  })
})

describe('virtualAgentNodeIds', () => {
  const ownerPair: AgentBoundPair = {
    cardId: 'map-1', agentCardId: 'agent:arch-1', agentId: 'arch-1', bindingKind: 'owner',
  }
  const parentEdge = { from: 'agent:arch-1', to: 'agent:worker-1', arrows: 'to', kind: 'parent' as const }
  const snapshotIds = new Set(['agent:arch-1', 'agent:worker-1'])

  it('injects pair-driven and parent-edge-driven agents without cards', () => {
    const ids = virtualAgentNodeIds([ownerPair], [parentEdge], new Set(['map-1']), snapshotIds)
    expect(ids.sort()).toEqual(['agent:arch-1', 'agent:worker-1'])
  })

  it('skips agents that already have a card node', () => {
    const ids = virtualAgentNodeIds(
      [ownerPair],
      [parentEdge],
      new Set(['map-1', 'agent:arch-1', 'agent:worker-1']),
      snapshotIds,
    )
    expect(ids).toEqual([])
  })

  it('omits parent-edge agents when parent edges are hidden', () => {
    const ids = virtualAgentNodeIds([ownerPair], [parentEdge], new Set(['map-1']), snapshotIds, { parent: false })
    expect(ids).toEqual(['agent:arch-1'])
  })

  it('omits binding-pair agents when agent_binding edges are hidden', () => {
    const ids = virtualAgentNodeIds([ownerPair], [], new Set(['map-1']), snapshotIds, { agent_binding: false })
    expect(ids).toEqual([])
  })

  it('ignores parent-edge endpoints that are not live snapshot agents', () => {
    const ids = virtualAgentNodeIds([], [parentEdge], new Set(['map-1']), new Set())
    expect(ids).toEqual([])
  })
})
