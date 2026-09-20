import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act, StrictMode } from 'react'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// ── Hoisted mocks ──────────────────────────────────────────────────

const { savePreferenceMock, loadPreferenceMock, agentSnapshotMock, overlayRootRenderMock, wikiSetStatusMock, workflowPauseAllMock, turnPauseMock, turnResumeMock } = vi.hoisted(() => ({
  savePreferenceMock: vi.fn<(...a: unknown[]) => Promise<void>>(),
  loadPreferenceMock: vi.fn<(k: string) => Promise<string | undefined>>(),
  agentSnapshotMock: vi.fn<() => unknown>(),
  overlayRootRenderMock: vi.fn(),
  wikiSetStatusMock: vi.fn<(...a: unknown[]) => Promise<unknown>>().mockResolvedValue({}),
  workflowPauseAllMock: vi.fn<(...a: unknown[]) => Promise<unknown>>().mockResolvedValue({ PausedCount: 0, SkippedCount: 0 }),
  turnPauseMock: vi.fn<(...a: unknown[]) => Promise<void>>().mockResolvedValue(undefined),
  turnResumeMock: vi.fn<(...a: unknown[]) => Promise<void>>().mockResolvedValue(undefined),
}))

vi.mock('../../../application/theme-persist', () => ({
  savePreference: savePreferenceMock,
  loadPreference: loadPreferenceMock,
}))

// The status-cycle path calls wikiSetStatus; the graph uses nothing else from
// the project client.
vi.mock('../../../gen-clients/project/client', () => ({
  wikiSetStatus: wikiSetStatusMock,
}))

// The doing-button pause path and the paused-button resume path dispatch
// through the local agent client (workflow_pause_all / turn_pause / turn_resume).
vi.mock('../../../gen-clients/local/client', () => ({
  workflowPauseAll: workflowPauseAllMock,
  turnPause: turnPauseMock,
  turnResume: turnResumeMock,
}))

vi.mock('react-dom/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-dom/client')>()
  return {
    ...actual,
    createRoot: (...args: Parameters<typeof actual.createRoot>) => {
      const root = actual.createRoot(...args)
      return {
        render: (node: Parameters<typeof root.render>[0]) => {
          overlayRootRenderMock()
          root.render(node)
        },
        unmount: () => root.unmount(),
      }
    },
  }
})

vi.mock('../../../i18n', async () => {
  const { createContext } = await import('react')
  const ctx = createContext<any>(null)
  const value = {
    t: (key: string) => key,
    locale: 'en-US' as const,
    setLocale: () => {},
    supportedLocales: ['en-US'] as const,
  }
  return {
    useI18n: () => value,
    I18nContext: ctx,
  }
})

// Mock agentInfoStore so CardAgentAvatar can resolve agent data in tests.
vi.mock('../hooks/agentInfoStore', () => ({
  useAgentInfoList: () => agentSnapshotMock(),
}))


// Lightweight vis-network stub — the real Network needs a canvas.
vi.mock('vis-network/standalone', () => {
  /** Minimal vis DataSet stub. Class shape (instead of a closure factory)
   *  lets tests spy on prototype methods such as update() to assert DataSet
   *  call counts during data synchronization. */
  class MockDataSet {
    // Track ids so getIds() reflects what syncDataAndOverlays pushes — the
    // locate readiness check reads the live DataSet, not just the layout memo.
    ids = new Set<string | number>()
    getIds() {
      return [...this.ids]
    }
    update(data: Record<string, unknown> | Record<string, unknown>[]) {
      const arr = Array.isArray(data) ? data : [data]
      for (const d of arr) {
        if (d && typeof (d as { id?: unknown }).id !== 'undefined') this.ids.add((d as { id: string | number }).id)
      }
      return []
    }
    remove(id: string | number | (string | number)[]) {
      const arr = Array.isArray(id) ? id : [id]
      for (const i of arr) this.ids.delete(i)
      return []
    }
  }
  return {
    Network: class MockNetwork {
      /** Exposed so tests can reach the DataSet prototype for call counting. */
      static DataSet = MockDataSet
      body = { data: { nodes: new MockDataSet(), edges: new MockDataSet() } }
      on() {}
      off() {}
      setData() {}
      setOptions() {}
      redraw() {}
      fit() {}
      moveTo() {}
      getScale() { return 1 }
      getPosition() { return { x: 0, y: 0 } }
      getNodeAt(): string | undefined { return undefined }
      canvasToDOM(p: { x: number; y: number }) { return p }
      getViewPosition() { return { x: 0, y: 0 } }
      destroy() {}
    },
  }
})

// Import component AFTER mocks are set up.
import { WorkflowGraph, LOCATE_TIMEOUT_MS, DRAW_INTERVAL_MS, shouldDrawFrame, anyNodeInViewport } from './WorkflowGraph'
import { Network } from 'vis-network/standalone'
import type { MonoCardListItem } from '../../../domain/mono-types'
import type { AgentInfo, AgentInfoSnapshot } from '../hooks/agentInfoStore'

// ── Test helpers ───────────────────────────────────────────────────

function makeCard(id: string, over: Partial<MonoCardListItem> = {}): MonoCardListItem {
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

function makeMap(id: string, include: string[]): MonoCardListItem {
  return makeCard(id, { type: 'workflow', data: { scope: { include } } })
}

/** Minimal card set: one workflow map with a single task. */
function minimalCards(): MonoCardListItem[] {
  return [
    makeMap('root', ['taskA']),
    makeCard('taskA', { type: 'task', status: 'todo' }),
  ]
}

/** Epic mode is always on and workflows render folded by default; tests that
 *  assert on task-card internals must expand the workflow explicitly. */
const EXPANDED_ROOT = new Set(['root'])

// ── Tests: controlled layout direction ─────────────────────────────

describe('WorkflowGraph layout direction (controlled prop)', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined) // no saved viewport by default
    savePreferenceMock.mockResolvedValue(undefined)

    // Default: empty agent store (no agents).
    agentSnapshotMock.mockReturnValue({
      version: 1,
      loading: false,
      items: [],
      byActorId: new Map(),
      byId: new Map(),
    })

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('defaults to LTR and applies the workflow-graph--ltr container class', () => {
    act(() => {
      root.render(<WorkflowGraph cards={minimalCards()} />)
    })

    const graphContainer = container.querySelector('.workflow-graph')
    expect(graphContainer).not.toBeNull()
    expect(graphContainer!.classList.contains('workflow-graph--ltr')).toBe(true)
  })

  it('applies workflow-graph--ttb when direction="TTB" is passed', () => {
    act(() => {
      root.render(<WorkflowGraph cards={minimalCards()} direction="TTB" />)
    })

    const graphContainer = container.querySelector('.workflow-graph')!
    expect(graphContainer.classList.contains('workflow-graph--ttb')).toBe(true)
    expect(graphContainer.classList.contains('workflow-graph--ltr')).toBe(false)
  })

  it('does not render an internal layout toolbar (moved to the parent)', () => {
    act(() => {
      root.render(<WorkflowGraph cards={minimalCards()} />)
    })

    expect(container.querySelector('.workflow-layout-toolbar')).toBeNull()
    expect(container.querySelector('.workflow-layout-btn')).toBeNull()
  })

  it('skips unchanged overlay root renders during data synchronization', () => {
    const cards = minimalCards()
    act(() => {
      root.render(<WorkflowGraph cards={cards} expandedWorkflows={EXPANDED_ROOT} />)
    })
    overlayRootRenderMock.mockClear()

    act(() => {
      root.render(<WorkflowGraph cards={[...cards]} expandedWorkflows={EXPANDED_ROOT} />)
    })

    // The graph component is re-rendered with a new card-array reference, but
    // each overlay retains the same visual signature and avoids root.render().
    // The only render belongs to the outer test root.
    expect(overlayRootRenderMock).toHaveBeenCalledTimes(1)
  })
})

// ── Tests: DataSet update / redraw short-circuit ────────────────────

describe('WorkflowGraph data synchronization short-circuit', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined) // no saved viewport by default
    savePreferenceMock.mockResolvedValue(undefined)

    agentSnapshotMock.mockReturnValue({
      version: 1,
      loading: false,
      items: [],
      byActorId: new Map(),
      byId: new Map(),
    })

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('skips DataSet updates and network redraws when an unrelated cards change leaves the node set identical', async () => {
    const MockNetwork = Network as unknown as { prototype: { redraw: () => void } }
    const MockDataSet = (Network as unknown as { DataSet: { prototype: { update: (data: unknown) => unknown } } }).DataSet
    const redrawSpy = vi.spyOn(MockNetwork.prototype, 'redraw').mockImplementation(() => {})
    const updateSpy = vi.spyOn(MockDataSet.prototype, 'update').mockImplementation(() => [])
    try {
      const cards = [
        makeMap('root', ['taskA']),
        makeCard('taskA', { type: 'task', status: 'todo' }),
        makeMap('other', ['otherTask']),
        makeCard('otherTask', { type: 'task', status: 'todo' }),
      ]
      await act(async () => {
        root.render(<WorkflowGraph cards={cards} expandedWorkflows={EXPANDED_ROOT} />)
      })

      // The initial sync pushes both DataSet payloads (nodes + edges) and
      // repaints the canvas exactly once — no other redraw path (animation
      // loop, resize observer, drag) is active in this harness.
      expect(redrawSpy).toHaveBeenCalledTimes(1)
      expect(updateSpy).toHaveBeenCalledTimes(2)

      redrawSpy.mockClear()
      updateSpy.mockClear()

      // Unrelated change: another workflow card's title is edited. The cards
      // array is brand new and the layout memo rebuilds, but the rendered
      // node set is identical — the full DataSet update and network redraw
      // must not run again (only the guarded overlay pass does).
      const nextCards = cards.map(c =>
        c.id === 'other' ? { ...c, data: { ...c.data, title: 'Renamed workflow' } } : c,
      )
      await act(async () => {
        root.render(<WorkflowGraph cards={nextCards} expandedWorkflows={EXPANDED_ROOT} />)
      })

      expect(redrawSpy).not.toHaveBeenCalled()
      expect(updateSpy).not.toHaveBeenCalled()
    } finally {
      redrawSpy.mockRestore()
      updateSpy.mockRestore()
    }
  })

  it('still updates DataSets and redraws when the node set actually changes', async () => {
    const MockNetwork = Network as unknown as { prototype: { redraw: () => void } }
    const MockDataSet = (Network as unknown as { DataSet: { prototype: { update: (data: unknown) => unknown } } }).DataSet
    const redrawSpy = vi.spyOn(MockNetwork.prototype, 'redraw').mockImplementation(() => {})
    const updateSpy = vi.spyOn(MockDataSet.prototype, 'update').mockImplementation(() => [])
    try {
      await act(async () => {
        root.render(<WorkflowGraph cards={minimalCards()} expandedWorkflows={EXPANDED_ROOT} />)
      })
      redrawSpy.mockClear()
      updateSpy.mockClear()

      // A genuinely different node set (a second task in the expanded map)
      // must still take the full update + redraw path.
      const changedCards = [
        makeMap('root', ['taskA', 'taskB']),
        makeCard('taskA', { type: 'task', status: 'todo' }),
        makeCard('taskB', { type: 'task', status: 'todo' }),
      ]
      await act(async () => {
        root.render(<WorkflowGraph cards={changedCards} expandedWorkflows={EXPANDED_ROOT} />)
      })

      expect(redrawSpy).toHaveBeenCalledTimes(1)
      expect(updateSpy).toHaveBeenCalledTimes(2)
    } finally {
      redrawSpy.mockRestore()
      updateSpy.mockRestore()
    }
  })

  it('re-pushes nodes to the recreated network under React StrictMode', async () => {
    const MockDataSet = (Network as unknown as { DataSet: { prototype: { update: (data: unknown) => unknown } } }).DataSet
    const updateSpy = vi.spyOn(MockDataSet.prototype, 'update').mockImplementation(() => [])
    try {
      await act(async () => {
        root.render(
          <StrictMode>
            <WorkflowGraph cards={minimalCards()} expandedWorkflows={EXPANDED_ROOT} />
          </StrictMode>,
        )
      })
      // StrictMode double-invokes the network setup effect (create → destroy
      // → recreate). The recreated Network starts with an empty DataSet, so its
      // initial sync must push nodes again. Without the signature reset the
      // second push is skipped and the recreated graph stays empty — every
      // overlay collapses to the layer origin (all cards at one point). Each
      // mount pushes nodes + edges, so two mounts make four DataSet updates.
      expect(updateSpy.mock.calls.length).toBe(4)
    } finally {
      updateSpy.mockRestore()
    }
  })
})

// ── Tests: DOM style regression ────────────────────────────────────

describe('WorkflowGraph status bar DOM positioning', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)

    agentSnapshotMock.mockReturnValue({
      version: 1,
      loading: false,
      items: [],
      byActorId: new Map(),
      byId: new Map(),
    })

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('renders a status bar element inside workflow node overlays (compact mode)', async () => {
    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} expandedWorkflows={EXPANDED_ROOT} />)
    })

    // The overlay layer should contain MonoCardCompact instances with the
    // status-bar element (compact + status = todo).
    const statusBars = container.querySelectorAll('.mono-card-compact-status-bar')
    expect(statusBars.length).toBeGreaterThanOrEqual(1)
  })

  it('uses workflow-graph--ltr class by default, matching left status bar CSS rule', () => {
    act(() => {
      root.render(<WorkflowGraph cards={minimalCards()} />)
    })

    const graphContainer = container.querySelector('.workflow-graph')!
    // The CSS selector `.workflow-graph--ltr .mono-card-compact-status-bar`
    // positions the bar on the LEFT edge. Verify the class is present.
    expect(graphContainer.classList.contains('workflow-graph--ltr')).toBe(true)
  })

  it('uses workflow-graph--ttb class when direction="TTB", matching top status bar CSS rule', () => {
    act(() => {
      root.render(<WorkflowGraph cards={minimalCards()} direction="TTB" />)
    })

    const graphContainer = container.querySelector('.workflow-graph')!
    // The CSS selector `.workflow-graph--ttb .mono-card-compact-status-bar`
    // positions the bar on the TOP edge. Verify the class is present.
    expect(graphContainer.classList.contains('workflow-graph--ttb')).toBe(true)
  })
})

// ── Tests: workflow variant agent avatar & tags ─────────────────────

/** Build a minimal AgentInfoSnapshot with one working agent bound to a task card. */
function makeAgentSnapshot(boundCardId: string, actorId = 'actor-1'): AgentInfoSnapshot {
  const agent: AgentInfo = {
    Id: 'agent-1',
    ActorId: actorId,
    Title: 'Test',
    DisplayName: 'Test Agent',
    ProjectId: 'proj-1',
    ProjectName: 'Test Project',
    Status: 'running',
    StatusLabel: 'running',
    IsWorking: true,
    IsError: false,
    IsCompleted: false,
    IsAskUserPermission: false,
    IsAskUser: false,
    IsAskPermission: false,
    IsPlanApproval: false, IsGoalSubmit: false,
    CanDelete: true,
    CompactionPolicyLoading: false,
    BoundTaskCardId: boundCardId,
    Runtime: { BoundTaskCardId: boundCardId, State: 'running' },
  } as unknown as AgentInfo

  const byActorId = new Map<string, AgentInfo>([[actorId, agent]])
  const byId = new Map<string, AgentInfo>([['agent-1', agent]])
  return {
    version: 1,
    loading: false,
    items: [agent],
    byActorId,
    byId,
  }
}

describe('WorkflowGraph agent avatar & tags layout (workflow variant)', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('renders agent avatar in bottom-right corner (not header) for task with bound agent', async () => {
    const cards = minimalCards()
    const snap = makeAgentSnapshot('taskA')
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={cards} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    // Agent avatar should be in the agent-corner container, not in the header.
    const cornerAvatar = container.querySelector('.mono-card-compact-agent-corner .card-agent-avatar')
    expect(cornerAvatar).not.toBeNull()

    const headerAvatars = container.querySelectorAll('.mono-card-compact-header .card-agent-avatar')
    expect(headerAvatars).toHaveLength(0)
  })

  it('uses the spawn-time binding before runtime state reports it', async () => {
    const cards = minimalCards()
    const snap = makeAgentSnapshot('taskA')
    const agent = snap.items[0]!
    agent.Runtime = undefined
    agent.BoundTaskCardId = 'taskA'
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={cards} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    const cornerAvatar = container.querySelector('.mono-card-compact-agent-corner .card-agent-avatar')
    expect(cornerAvatar).not.toBeNull()
  })

  it('does not render agent avatar when task has no bound agent', async () => {
    const cards = minimalCards()
    // Empty agent store — no agents working.
    agentSnapshotMock.mockReturnValue({
      version: 1, loading: false, items: [], byActorId: new Map(), byId: new Map(),
    })

    await act(async () => {
      root.render(<WorkflowGraph cards={cards} expandedWorkflows={EXPANDED_ROOT} />)
    })

    const cornerAvatar = container.querySelector('.mono-card-compact-agent-corner')
    expect(cornerAvatar).toBeNull()

    const allAvatars = container.querySelectorAll('.card-agent-avatar')
    expect(allAvatars).toHaveLength(0)
  })

  it('hides tags row in workflow variant (card.tags data preserved)', async () => {
    const cards = [
      makeMap('root', ['taskA']),
      makeCard('taskA', { type: 'task', status: 'todo', tags: ['frontend', 'bug'] }),
    ]
    agentSnapshotMock.mockReturnValue({
      version: 1, loading: false, items: [], byActorId: new Map(), byId: new Map(),
    })

    await act(async () => {
      root.render(<WorkflowGraph cards={cards} expandedWorkflows={EXPANDED_ROOT} />)
    })

    // Tags row must not be rendered in workflow variant.
    const tagsRows = container.querySelectorAll('.mono-card-compact-tags-row')
    expect(tagsRows).toHaveLength(0)

    // Verify the card data itself still has tags (not mutated).
    expect(cards[1]!.tags).toEqual(['frontend', 'bug'])
  })

  it('renders agent avatars for multiple task cards with bound agents', async () => {
    const cards = [
      makeMap('root', ['taskA', 'taskB']),
      makeCard('taskA', { type: 'task', status: 'doing' }),
      makeCard('taskB', { type: 'task', status: 'todo' }),
    ]
    // Two agents: one bound to taskA, one bound to taskB.
    const agentA: AgentInfo = {
      Id: 'agent-a', ActorId: 'actor-a', Title: 'A', DisplayName: 'Agent A',
      ProjectId: 'p', ProjectName: 'P', Status: 'running', StatusLabel: 'running',
      IsWorking: true, IsError: false, IsCompleted: false, IsAskUserPermission: false,
      IsAskUser: false, IsAskPermission: false, IsPlanApproval: false, IsGoalSubmit: false,
      CanDelete: true, CompactionPolicyLoading: false,
      Runtime: { BoundTaskCardId: 'taskA', State: 'running' },
    } as unknown as AgentInfo
    const agentB: AgentInfo = {
      Id: 'agent-b', ActorId: 'actor-b', Title: 'B', DisplayName: 'Agent B',
      ProjectId: 'p', ProjectName: 'P', Status: 'running', StatusLabel: 'running',
      IsWorking: true, IsError: false, IsCompleted: false, IsAskUserPermission: false,
      IsAskUser: false, IsAskPermission: false, IsPlanApproval: false, IsGoalSubmit: false,
      CanDelete: true, CompactionPolicyLoading: false,
      Runtime: { BoundTaskCardId: 'taskB', State: 'running' },
    } as unknown as AgentInfo
    const snap: AgentInfoSnapshot = {
      version: 1, loading: false, items: [agentA, agentB],
      byActorId: new Map([['actor-a', agentA], ['actor-b', agentB]]),
      byId: new Map([['agent-a', agentA], ['agent-b', agentB]]),
    }
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={cards} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    // Both task cards should have agent-corner avatars.
    const cornerAvatars = container.querySelectorAll('.mono-card-compact-agent-corner .card-agent-avatar')
    expect(cornerAvatars.length).toBe(2)
  })

  it('renders agent avatar in bottom-right corner in TTB (vertical) layout', async () => {
    const cards = minimalCards()
    const snap = makeAgentSnapshot('taskA')
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={cards} agentInfoSnapshot={snap} direction="TTB" expandedWorkflows={EXPANDED_ROOT} />)
    })

    // Agent corner avatar should still be present.
    const cornerAvatar = container.querySelector('.mono-card-compact-agent-corner .card-agent-avatar')
    expect(cornerAvatar).not.toBeNull()
  })

  it('shows a completed start sentinel for a completed workflow', async () => {
    const cards = [
      { ...makeMap('root', ['taskA']), status: 'done' },
      makeCard('taskA', { type: 'task', status: 'done' }),
    ]

    await act(async () => {
      root.render(<WorkflowGraph cards={cards} expandedWorkflows={EXPANDED_ROOT} />)
    })

    const sentinel = container.querySelector('[data-card-id="root:start"] .workflow-node-overlay-inner--sentinel-start')
    expect(sentinel?.classList.contains('workflow-node-overlay-inner--sentinel-done')).toBe(true)
    // The expanded start sentinel shows the workflow title, never a status text.
    expect(sentinel?.textContent).toContain('root')
    expect(sentinel?.textContent).not.toContain('workflowGraph.completed')
  })

  it('does not render agent corner for sentinel nodes', async () => {
    const cards = minimalCards()
    const snap = makeAgentSnapshot('taskA')
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={cards} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    // Sentinel nodes should not have agent corner avatars.
    const sentinelOverlays = container.querySelectorAll('.workflow-node-overlay-inner--sentinel')
    expect(sentinelOverlays.length).toBeGreaterThanOrEqual(1)
    for (const sentinel of sentinelOverlays) {
      expect(sentinel.querySelector('.mono-card-compact-agent-corner')).toBeNull()
    }
  })

  it('renders corner status badge and agent avatar without overlap (both present)', async () => {
    const cards = [
      makeMap('root', ['taskA']),
      makeCard('taskA', { type: 'task', status: 'doing' }),
    ]
    const snap = makeAgentSnapshot('taskA')
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={cards} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    // Both corner status and agent corner should be present on the same card.
    const cornerStatus = container.querySelector('.mono-card-compact-corner-status')
    expect(cornerStatus).not.toBeNull()

    const agentCorner = container.querySelector('.mono-card-compact-agent-corner')
    expect(agentCorner).not.toBeNull()

    // Corner status is top-right; agent corner is bottom-right — verify they
    // are distinct elements (no DOM overlap).
    expect(cornerStatus).not.toBe(agentCorner)
  })
})

// ── Tests: card opening ───────────────────────────────────────────────

describe('WorkflowGraph card opening', () => {
  let container: HTMLDivElement
  let root: Root
  const mockNetwork = Network as unknown as { prototype: { on: (event: string, handler: (...args: any[]) => void) => void } }
  const defaultOn = mockNetwork.prototype.on

  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)

    agentSnapshotMock.mockReturnValue({
      version: 1,
      loading: false,
      items: [],
      byActorId: new Map(),
      byId: new Map(),
    })

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    mockNetwork.prototype.on = defaultOn
  })

  it('ignores clicks on empty space', async () => {
    const onClick = vi.fn()
    // Access the hoisted mock and wrap its prototype.on to capture click handlers.
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void } }
    const origOn = MockNetwork.prototype.on
    const clickHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'click') { clickHandlers.push(handler) }
      origOn.call(this, event, handler)
    }

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} onNodeClick={onClick} />)
    })

    // Simulate click on empty space (no nodes).
    await act(async () => {
      clickHandlers.forEach(h => h({ nodes: [], edges: [] }))
    })

    expect(onClick).not.toHaveBeenCalled()
  })

  it('selects a task card on first click and opens it on second click', async () => {
    const onClick = vi.fn()
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void } }
    const origOn = MockNetwork.prototype.on
    const clickHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'click') { clickHandlers.push(handler) }
      origOn.call(this, event, handler)
    }

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} onNodeClick={onClick} expandedWorkflows={EXPANDED_ROOT} />)
    })

    // First click selects the card and shows the floating toolbar, no open.
    await act(async () => {
      clickHandlers.forEach(h => h({ nodes: ['taskA'], edges: [] }))
    })
    expect(onClick).not.toHaveBeenCalled()
    const overlay = container.querySelector('[data-card-id="taskA"]')
    expect(overlay?.classList.contains('workflow-node-overlay--selected')).toBe(true)
    expect(overlay?.querySelector('.workflow-card-toolbar')).not.toBeNull()

    // Second click on the selected card opens it.
    await act(async () => {
      clickHandlers.forEach(h => h({ nodes: ['taskA'], edges: [] }))
    })
    expect(onClick).toHaveBeenCalledOnce()
    expect(onClick).toHaveBeenCalledWith('taskA', 'taskA')
  })

  it('clears the selection when empty space is clicked', async () => {
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void } }
    const origOn = MockNetwork.prototype.on
    const clickHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'click') { clickHandlers.push(handler) }
      origOn.call(this, event, handler)
    }

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} expandedWorkflows={EXPANDED_ROOT} />)
    })

    await act(async () => {
      clickHandlers.forEach(h => h({ nodes: ['taskA'], edges: [] }))
    })
    expect(container.querySelector('[data-card-id="taskA"]')?.classList.contains('workflow-node-overlay--selected')).toBe(true)

    await act(async () => {
      clickHandlers.forEach(h => h({ nodes: [], edges: [] }))
    })
    const overlay = container.querySelector('[data-card-id="taskA"]')
    expect(overlay?.classList.contains('workflow-node-overlay--selected')).toBe(false)
    expect(overlay?.querySelector('.workflow-card-toolbar')).toBeNull()
  })

  it('selects the start sentinel on first click and opens the workflow map on second click', async () => {
    const onClick = vi.fn()
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void } }
    const origOn = MockNetwork.prototype.on
    const clickHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'click') clickHandlers.push(handler)
      origOn.call(this, event, handler)
    }

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} onNodeClick={onClick} />)
    })

    await act(async () => {
      clickHandlers.forEach(h => h({ nodes: ['root:start'], edges: [] }))
    })
    expect(onClick).not.toHaveBeenCalled()
    expect(container.querySelector('[data-card-id="root:start"]')?.classList.contains('workflow-node-overlay--selected')).toBe(true)

    await act(async () => {
      clickHandlers.forEach(h => h({ nodes: ['root:start'], edges: [] }))
    })
    expect(onClick).toHaveBeenCalledOnce()
    expect(onClick).toHaveBeenCalledWith('root', 'root')
  })

  it('does not request the card menu on left-click', async () => {
    const onMenu = vi.fn()
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void } }
    const origOn = MockNetwork.prototype.on
    const clickHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'click') clickHandlers.push(handler)
      origOn.call(this, event, handler)
    }

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} onNodeMenu={onMenu} />)
    })
    await act(async () => {
      clickHandlers.forEach(h => h({
        nodes: ['taskA'],
        edges: [],
        pointer: { DOM: { x: 10, y: 20 }, canvas: { x: 0, y: 0 } },
        event: { srcEvent: { clientX: 123, clientY: 456 } },
      }))
    })

    expect(onMenu).not.toHaveBeenCalled()
  })

  it('maps the start sentinel to its map card for the right-click menu', async () => {
    const onMenu = vi.fn()
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void; getNodeAt: (p: unknown) => string | undefined } }
    const origOn = MockNetwork.prototype.on
    const origGetNodeAt = MockNetwork.prototype.getNodeAt
    const contextHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'oncontext') contextHandlers.push(handler)
      origOn.call(this, event, handler)
    }
    MockNetwork.prototype.getNodeAt = () => 'root:start'

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} onNodeMenu={onMenu} />)
    })
    await act(async () => {
      contextHandlers.forEach(h => h({ nodes: [], edges: [], pointer: { DOM: { x: 7, y: 9 } } }))
    })

    expect(onMenu).toHaveBeenCalledOnce()
    expect(onMenu).toHaveBeenCalledWith('root', 7, 9)
    MockNetwork.prototype.getNodeAt = origGetNodeAt
  })

  it('requests the card menu on right-click (oncontext)', async () => {
    const onMenu = vi.fn()
    const onClick = vi.fn()
    const preventDefault = vi.fn()
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void; getNodeAt: (p: unknown) => string | undefined } }
    const origOn = MockNetwork.prototype.on
    const origGetNodeAt = MockNetwork.prototype.getNodeAt
    const contextHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'oncontext') contextHandlers.push(handler)
      origOn.call(this, event, handler)
    }
    // The node under the cursor, regardless of the current selection.
    MockNetwork.prototype.getNodeAt = () => 'taskA'

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} onNodeClick={onClick} onNodeMenu={onMenu} />)
    })
    await act(async () => {
      contextHandlers.forEach(h => h({
        nodes: [],
        edges: [],
        pointer: { DOM: { x: 1, y: 2 } },
        event: { srcEvent: { clientX: 50, clientY: 60, preventDefault } },
      }))
    })

    expect(preventDefault).toHaveBeenCalledOnce()
    expect(onClick).not.toHaveBeenCalled()
    expect(onMenu).toHaveBeenCalledOnce()
    expect(onMenu).toHaveBeenCalledWith('taskA', 50, 60)
    MockNetwork.prototype.getNodeAt = origGetNodeAt
  })

  it('opens the menu for an unselected node under the cursor', async () => {
    const onMenu = vi.fn()
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void; getNodeAt: (p: unknown) => string | undefined } }
    const origOn = MockNetwork.prototype.on
    const origGetNodeAt = MockNetwork.prototype.getNodeAt
    const contextHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'oncontext') contextHandlers.push(handler)
      origOn.call(this, event, handler)
    }
    MockNetwork.prototype.getNodeAt = () => 'taskA'

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} onNodeMenu={onMenu} />)
    })
    await act(async () => {
      // No selection (params.nodes empty), but the pointer is over taskA.
      contextHandlers.forEach(h => h({
        nodes: [],
        edges: [],
        pointer: { DOM: { x: 30, y: 40 } },
        event: { clientX: 300, clientY: 400, preventDefault: () => {} },
      }))
    })

    expect(onMenu).toHaveBeenCalledOnce()
    expect(onMenu).toHaveBeenCalledWith('taskA', 300, 400)
    MockNetwork.prototype.getNodeAt = origGetNodeAt
  })

  it('ignores right-click on empty canvas even when a node is selected', async () => {
    const onMenu = vi.fn()
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void; getNodeAt: (p: unknown) => string | undefined } }
    const origOn = MockNetwork.prototype.on
    const contextHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'oncontext') contextHandlers.push(handler)
      origOn.call(this, event, handler)
    }

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} onNodeMenu={onMenu} />)
    })
    await act(async () => {
      // taskA is selected but the pointer is over empty canvas (getNodeAt → undefined).
      contextHandlers.forEach(h => h({ nodes: ['taskA'], edges: [], pointer: { DOM: { x: 1, y: 2 } } }))
    })

    expect(onMenu).not.toHaveBeenCalled()
    MockNetwork.prototype.on = origOn
  })

  it('routes right-click on a virtual category node to onCategoryMenu', async () => {
    const onMenu = vi.fn()
    const onCategoryMenu = vi.fn()
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void; getNodeAt: (p: unknown) => string | undefined } }
    const origOn = MockNetwork.prototype.on
    const origGetNodeAt = MockNetwork.prototype.getNodeAt
    const contextHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'oncontext') contextHandlers.push(handler)
      origOn.call(this, event, handler)
    }
    MockNetwork.prototype.getNodeAt = () => '__epic_cat__:completed'

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} onNodeMenu={onMenu} onCategoryMenu={onCategoryMenu} />)
    })
    await act(async () => {
      contextHandlers.forEach(h => h({
        nodes: [],
        edges: [],
        pointer: { DOM: { x: 3, y: 4 } },
        event: { clientX: 33, clientY: 44, preventDefault: () => {} },
      }))
    })

    expect(onMenu).not.toHaveBeenCalled()
    expect(onCategoryMenu).toHaveBeenCalledOnce()
    expect(onCategoryMenu).toHaveBeenCalledWith('completed', 33, 44)
    MockNetwork.prototype.on = origOn
    MockNetwork.prototype.getNodeAt = origGetNodeAt
  })
})

// ── Tests: locate request (zoom-to-fit) ──────────────────────────────

describe('WorkflowGraph locate request', () => {
  let container: HTMLDivElement
  let root: Root
  const MockNetwork = Network as unknown as {
    prototype: { fit: (...a: unknown[]) => void; moveTo: (...a: unknown[]) => void }
  }
  let fitSpy: ReturnType<typeof vi.spyOn>
  let moveToSpy: ReturnType<typeof vi.spyOn>

  /** Give the rendered .workflow-graph element a non-zero size so the locate's
   *  container-size readiness check passes (jsdom reports 0 by default). */
  function stubGraphSize(w = 800, h = 600) {
    const el = container.querySelector('.workflow-graph') as HTMLDivElement | null
    if (!el) return
    Object.defineProperty(el, 'clientWidth', { value: w, configurable: true })
    Object.defineProperty(el, 'clientHeight', { value: h, configurable: true })
  }

  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)

    agentSnapshotMock.mockReturnValue({
      version: 1,
      loading: false,
      items: [],
      byActorId: new Map(),
      byId: new Map(),
    })

    fitSpy = vi.spyOn(MockNetwork.prototype, 'fit').mockImplementation(() => {})
    moveToSpy = vi.spyOn(MockNetwork.prototype, 'moveTo').mockImplementation(() => {})

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    fitSpy.mockRestore()
    moveToSpy.mockRestore()
  })

  it('selects the map start node when a selectStart locate request is applied', async () => {
    // Mount the network first, give it a real size, then trigger the locate so
    // the locate's immediate attempt finds a ready network + sized container.
    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} expandedWorkflows={EXPANDED_ROOT} />)
    })
    stubGraphSize()
    await act(async () => {
      root.render(
        <WorkflowGraph
          cards={minimalCards()}
          expandedWorkflows={EXPANDED_ROOT}
          locateRequest={{ nonce: 1, mapId: 'root', selectStart: true }}
        />,
      )
    })

    // The locate effect selects the start node — the same effect as clicking it.
    await vi.waitFor(() => {
      const overlay = container.querySelector('[data-card-id="root:start"]')
      expect(overlay?.classList.contains('workflow-node-overlay--selected')).toBe(true)
    })

    // A selected node shows its floating toolbar.
    expect(container.querySelector('[data-card-id="root:start"] .workflow-card-toolbar')).not.toBeNull()
  })

  it('keeps centering alive after the old timeout when the target never renders', async () => {
    vi.useFakeTimers()
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      await act(async () => {
        root.render(
          <WorkflowGraph
            cards={[makeMap('other', ['t1']), makeCard('t1', { type: 'task', status: 'todo' })]}
            expandedWorkflows={new Set(['other'])}
            locateRequest={{ nonce: 1, mapId: 'root', selectStart: true }}
          />,
        )
      })
      stubGraphSize()

      // Advance well past the old LOCATE_TIMEOUT_MS. Centering must NOT
      // give up — it polls until the user breaks it (select/drag/zoom).
      act(() => {
        vi.advanceTimersByTime(LOCATE_TIMEOUT_MS * 3)
      })

      // The old centering timeout would have logged this warning. It must
      // never appear now — the state persists until user interruption.
      const centeringWarning = warnSpy.mock.calls.find(
        ([msg]) => typeof msg === 'string' && msg.includes('centering timed out'),
      )
      expect(centeringWarning).toBeUndefined()
    } finally {
      warnSpy.mockRestore()
      vi.useRealTimers()
    }
  })

  it('clears centering when the user clicks a different card after selectStart', async () => {
    const { Network } = await import('vis-network/standalone')
    const MockNetwork = Network as unknown as { prototype: { on: (e: string, fn: (...a: unknown[]) => void) => void } }
    const origOn = MockNetwork.prototype.on
    const clickHandlers: ((...a: unknown[]) => void)[] = []
    MockNetwork.prototype.on = function(this: any, event: string, handler: (...a: unknown[]) => void) {
      if (event === 'click') clickHandlers.push(handler)
      origOn.call(this, event, handler)
    }

    try {
      await act(async () => {
        root.render(<WorkflowGraph cards={minimalCards()} expandedWorkflows={EXPANDED_ROOT} />)
      })
      stubGraphSize()
      await act(async () => {
        root.render(
          <WorkflowGraph
            cards={minimalCards()}
            expandedWorkflows={EXPANDED_ROOT}
            locateRequest={{ nonce: 1, mapId: 'root', selectStart: true }}
          />,
        )
      })

      await vi.waitFor(() => {
        const sentinel = container.querySelector('[data-card-id="root:start"]')
        expect(sentinel?.classList.contains('workflow-node-overlay--centering')).toBe(true)
      })

      // User clicks a different card → centering state must clear.
      await act(async () => {
        clickHandlers.forEach(h => h({ nodes: ['taskA'], edges: [] }))
      })

      await vi.waitFor(() => {
        const sentinel = container.querySelector('[data-card-id="root:start"]')
        expect(sentinel?.classList.contains('workflow-node-overlay--centering')).toBe(false)
      })
    } finally {
      MockNetwork.prototype.on = origOn
    }
  })

  it('does not select the start node when selectStart is absent', async () => {
    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} expandedWorkflows={EXPANDED_ROOT} />)
    })
    stubGraphSize()
    await act(async () => {
      root.render(
        <WorkflowGraph
          cards={minimalCards()}
          expandedWorkflows={EXPANDED_ROOT}
          locateRequest={{ nonce: 1, mapId: 'root' }}
        />,
      )
    })

    // The locate fits the workflow but must not select the start node.
    await vi.waitFor(() => {
      expect(fitSpy).toHaveBeenCalled()
    })

    const overlay = container.querySelector('[data-card-id="root:start"]')
    expect(overlay?.classList.contains('workflow-node-overlay--selected')).toBe(false)
  })

  it('waits until the target workflow renders before fitting (condition-driven retry)', async () => {
    // A different workflow is present; the locate target 'root' is not loaded.
    await act(async () => {
      root.render(
        <WorkflowGraph
          cards={[makeMap('other', ['t1']), makeCard('t1', { type: 'task', status: 'todo' })]}
          expandedWorkflows={new Set(['other'])}
          locateRequest={{ nonce: 1, mapId: 'root' }}
        />,
      )
    })
    stubGraphSize()

    // The target is absent — locate keeps waiting and must not fit yet.
    await act(async () => { await new Promise(r => setTimeout(r, 60)) })
    expect(fitSpy).not.toHaveBeenCalled()

    // Now load the target workflow. The nodes change → locate re-attempts and
    // fits the now-present target (without a new nonce).
    await act(async () => {
      root.render(
        <WorkflowGraph
          cards={[
            makeMap('other', ['t1']),
            makeCard('t1', { type: 'task', status: 'todo' }),
            makeMap('root', ['taskA']),
            makeCard('taskA', { type: 'task', status: 'todo' }),
          ]}
          expandedWorkflows={new Set(['other', 'root'])}
          locateRequest={{ nonce: 1, mapId: 'root' }}
        />,
      )
    })

    await vi.waitFor(() => {
      expect(fitSpy).toHaveBeenCalled()
    })
  })

  it('waits for a non-zero container size before fitting, then succeeds', async () => {
    // Cards are present and expanded, but the container is 0-sized (jsdom
    // default) — the locate's container readiness check must defer the fit.
    await act(async () => {
      root.render(
        <WorkflowGraph
          cards={minimalCards()}
          expandedWorkflows={EXPANDED_ROOT}
          locateRequest={{ nonce: 1, mapId: 'root' }}
        />,
      )
    })
    // Do NOT stub the size yet — container is still 0×0.
    await act(async () => { await new Promise(r => setTimeout(r, 60)) })
    expect(fitSpy).not.toHaveBeenCalled()

    // Now give the container a real size. The RAF retry loop picks it up and
    // fits the target workflow.
    stubGraphSize()
    await vi.waitFor(() => {
      expect(fitSpy).toHaveBeenCalled()
    })
  })

  it('does not restore a saved viewport while a locate is pending (mutex)', async () => {
    // Provide a saved viewport for the current direction.
    const savedViewport = JSON.stringify({ x: 10, y: 20, scale: 0.5 })
    loadPreferenceMock.mockImplementation((key: string) =>
      key.includes('viewport') ? Promise.resolve(savedViewport) : Promise.resolve(undefined),
    )

    // Render with the locate already pending: the locate effect arms the mutex
    // before either restore path's loadPreference resolves, so both skip their
    // saved-viewport moveTo and let the locate's fit win.
    await act(async () => {
      root.render(
        <WorkflowGraph
          cards={minimalCards()}
          expandedWorkflows={EXPANDED_ROOT}
          locateRequest={{ nonce: 1, mapId: 'root' }}
        />,
      )
    })
    stubGraphSize()

    await vi.waitFor(() => {
      expect(fitSpy).toHaveBeenCalled()
    })

    expect(moveToSpy).not.toHaveBeenCalled()
  })

  it('restores a saved viewport when no locate is pending', async () => {
    const savedViewport = JSON.stringify({ x: 10, y: 20, scale: 0.5 })
    loadPreferenceMock.mockImplementation((key: string) =>
      key.includes('viewport') ? Promise.resolve(savedViewport) : Promise.resolve(undefined),
    )

    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} expandedWorkflows={EXPANDED_ROOT} />)
    })
    // Let the async restore promises resolve.
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    expect(moveToSpy).toHaveBeenCalled()
  })

  it('gives up with a warning after the timeout when the target never renders', () => {
    vi.useFakeTimers()
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      act(() => {
        root.render(
          <WorkflowGraph
            cards={[makeMap('other', ['t1']), makeCard('t1', { type: 'task', status: 'todo' })]}
            expandedWorkflows={new Set(['other'])}
            locateRequest={{ nonce: 1, mapId: 'root' }}
          />,
        )
      })
      stubGraphSize()

      // Target 'root' is absent; advance past the locate timeout.
      act(() => {
        vi.advanceTimersByTime(LOCATE_TIMEOUT_MS + 100)
      })

      expect(warnSpy).toHaveBeenCalledWith('[WorkflowGraph] locate timed out waiting for the target workflow to render')
      // The timeout falls back to fitting the whole graph (the 'other' workflow)
      // rather than leaving the viewport blank.
      expect(fitSpy).toHaveBeenCalled()
    } finally {
      warnSpy.mockRestore()
      vi.useRealTimers()
    }
  })
})

// ── Tests: category band fold ────────────────────────────────────────

describe('WorkflowGraph category band fold', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)

    agentSnapshotMock.mockReturnValue({
      version: 1,
      loading: false,
      items: [],
      byActorId: new Map(),
      byId: new Map(),
    })

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('category badge shows a fold button that toggles the category', async () => {
    const onToggleCategoryFold = vi.fn()
    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} onToggleCategoryFold={onToggleCategoryFold} />)
    })

    // minimalCards' 'root' map has no status → classified as 'to-start'.
    const badge = container.querySelector('[data-card-id="__epic_cat__:to-start"] .workflow-epic-category-badge')
    expect(badge).not.toBeNull()
    const btn = badge!.querySelector('.workflow-fold-btn') as HTMLButtonElement
    expect(btn).not.toBeNull()

    await act(async () => { btn.click() })
    expect(onToggleCategoryFold).toHaveBeenCalledWith('to-start')
  })

  it('folded category hides its workflows and shows the collapsed state', async () => {
    await act(async () => {
      root.render(<WorkflowGraph cards={minimalCards()} foldedCategories={new Set(['to-start'])} />)
    })

    // The folded band's workflow nodes are gone; the category node remains.
    expect(container.querySelector('[data-card-id="root:start"]')).toBeNull()
    expect(container.querySelector('[data-card-id="taskA"]')).toBeNull()
    const badge = container.querySelector('[data-card-id="__epic_cat__:to-start"] .workflow-epic-category-badge')
    expect(badge).not.toBeNull()
    expect(badge!.classList.contains('workflow-epic-category-badge--collapsed')).toBe(true)
  })

  it('locate request invokes onLocateWorkflow with the target map id', async () => {
    const onLocateWorkflow = vi.fn()
    await act(async () => {
      root.render(
        <WorkflowGraph
          cards={minimalCards()}
          expandedWorkflows={EXPANDED_ROOT}
          onLocateWorkflow={onLocateWorkflow}
          locateRequest={{ nonce: 1, mapId: 'root' }}
        />,
      )
    })
    expect(onLocateWorkflow).toHaveBeenCalledWith('root')
  })

  it('pending-delete outlines follow the confirm dialog open/close without other state changes', async () => {
    // Same cards identity across renders: opening the dialog must repaint the
    // overlays even though nodes/edges/selection are all unchanged.
    const cards = minimalCards()
    await act(async () => {
      root.render(<WorkflowGraph cards={cards} expandedWorkflows={EXPANDED_ROOT} />)
    })
    const overlays = () => ({
      start: container.querySelector('[data-card-id="root:start"] .workflow-node-overlay-inner'),
      map: container.querySelector('[data-card-id="root"] .workflow-node-overlay-inner'),
      task: container.querySelector('[data-card-id="taskA"] .workflow-node-overlay-inner'),
    })
    expect(Object.values(overlays()).every(el => el !== null)).toBe(true)
    expect(Object.values(overlays()).every(el => !el!.classList.contains('workflow-node-overlay-inner--pending-delete'))).toBe(true)

    // Dialog opens → red outlines appear immediately.
    await act(async () => {
      root.render(<WorkflowGraph cards={cards} expandedWorkflows={EXPANDED_ROOT} pendingDeleteNodeIds={new Set(['root', 'root:start', 'taskA'])} />)
    })
    expect(Object.values(overlays()).every(el => el!.classList.contains('workflow-node-overlay-inner--pending-delete'))).toBe(true)

    // Dialog closes → red outlines disappear immediately.
    await act(async () => {
      root.render(<WorkflowGraph cards={cards} expandedWorkflows={EXPANDED_ROOT} />)
    })
    expect(Object.values(overlays()).every(el => !el!.classList.contains('workflow-node-overlay-inner--pending-delete'))).toBe(true)
  })

  it('template maps render with the template modifier; instance maps with the instance modifier', async () => {
    const cards = [
      makeMap('tpl', ['tpl::t1']),
      makeCard('tpl::t1', { type: 'task', status: 'todo' }),
      makeMap('inst', ['inst::t1']),
      makeCard('inst::t1', { type: 'task', status: 'todo', data: {} }),
      makeCard('taskB', { type: 'task', status: 'todo' }),
    ]
    cards[0]!.data!.template = true
    cards[0]!.status = 'doing'
    cards[2]!.data!.instance_of = 'tpl'
    cards[2]!.status = 'doing'
    cards[3]!.data!.instance_of = 'tpl::t1'
    cards[4]!.parent = 'inst'

    await act(async () => {
      root.render(<WorkflowGraph cards={cards} expandedWorkflows={new Set(['tpl', 'inst'])} />)
    })

    const cls = (id: string) =>
      (container.querySelector(`[data-card-id="${id}"] .workflow-node-overlay-inner`) as HTMLElement | null)?.classList

    // Template map: folded start sentinel carries the template modifier.
    expect(cls('tpl:start')?.contains('workflow-node-overlay-inner--template')).toBe(true)
    expect(cls('tpl:start')?.contains('workflow-node-overlay-inner--instance')).toBe(false)
    // Template task card (data.template not set, but map is the template).
    expect(cls('tpl')?.contains('workflow-node-overlay-inner--template')).toBe(true)
    // Template's task: copied by template_save without instance_of → no modifiers.
    expect(cls('tpl::t1')?.contains('workflow-node-overlay-inner--instance')).toBe(false)

    // Instance map: dashed modifier on start sentinel and map card.
    expect(cls('inst:start')?.contains('workflow-node-overlay-inner--instance')).toBe(true)
    expect(cls('inst:start')?.contains('workflow-node-overlay-inner--template')).toBe(false)
    expect(cls('inst')?.contains('workflow-node-overlay-inner--instance')).toBe(true)
    // Instance task card carries data.instance_of → dashed too.
    expect(cls('inst::t1')?.contains('workflow-node-overlay-inner--instance')).toBe(true)

    // A plain workflow's cards carry neither modifier.
    expect(cls('root:start')).toBeUndefined()
  })
})

// ── Tests: to-start classification & start button ────────────────────

/** Build a snapshot with a single agent in a controllable load/status state. */
function makeOwnerSnapshot(actorId: string, over: Partial<AgentInfo> = {}): AgentInfoSnapshot {
  const agent = {
    Id: `agent-${actorId}`,
    ActorId: actorId,
    Title: 'Owner',
    DisplayName: 'Owner Agent',
    ProjectId: 'proj-1',
    ProjectName: 'Test Project',
    Status: 'idle',
    StatusLabel: 'idle',
    LoadState: 'loaded',
    IsWorking: false,
    IsError: false,
    IsCompleted: false,
    IsAskUserPermission: false,
    IsAskUser: false,
    IsAskPermission: false,
    IsPlanApproval: false, IsGoalSubmit: false,
    CanDelete: true,
    CompactionPolicyLoading: false,
    ...over,
  } as unknown as AgentInfo
  return {
    version: 1, loading: false, items: [agent],
    byActorId: new Map([[actorId, agent]]),
    byId: new Map([[agent.Id, agent]]),
  }
}

/** A doing workflow map with an owner binding. */
function doingOwnerCards(ownerActorId?: string): MonoCardListItem[] {
  const map = makeMap('root', ['taskA'])
  map.status = 'doing'
  if (ownerActorId) map.data = { ...map.data, ownerAgentId: ownerActorId }
  return [map, makeCard('taskA', { type: 'task', status: 'todo' })]
}

describe('WorkflowGraph to-start classification & start button', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)
    workflowPauseAllMock.mockReset()
    workflowPauseAllMock.mockResolvedValue({ PausedCount: 0, SkippedCount: 0 })
    turnPauseMock.mockReset()
    turnPauseMock.mockResolvedValue(undefined)
    turnResumeMock.mockReset()
    turnResumeMock.mockResolvedValue(undefined)

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('doing map with an active owner stays in-progress and shows the doing button', async () => {
    const snap = makeOwnerSnapshot('actor-owner')
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={doingOwnerCards('actor-owner')} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    expect(container.querySelector('[data-card-id="__epic_cat__:in-progress"]')).not.toBeNull()
    expect(container.querySelector('[data-card-id="__epic_cat__:to-start"]')).toBeNull()
    const btn = container.querySelector('[data-card-id="root:start"] .workflow-status-btn')
    expect(btn?.classList.contains('workflow-status-btn--doing')).toBe(true)
  })

  it('doing map with an unloaded owner classifies as to-start', async () => {
    const snap = makeOwnerSnapshot('actor-owner', { LoadState: 'unloaded' })
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={doingOwnerCards('actor-owner')} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    expect(container.querySelector('[data-card-id="__epic_cat__:to-start"]')).not.toBeNull()
    expect(container.querySelector('[data-card-id="__epic_cat__:in-progress"]')).toBeNull()
    const btn = container.querySelector('[data-card-id="root:start"] .workflow-status-btn')
    expect(btn?.classList.contains('workflow-status-btn--start')).toBe(true)
  })

  it('doing map with a waiting owner stays in-progress even when briefly unloaded', async () => {
    // A workflow owner's turn parks in waiting between events; the agent list
    // may report it unloaded during a refresh. The workflow must not blink
    // away from in-progress.
    const snap = makeOwnerSnapshot('actor-owner', { Status: 'waiting', LoadState: 'unloaded' })
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={doingOwnerCards('actor-owner')} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    expect(container.querySelector('[data-card-id="__epic_cat__:in-progress"]')).not.toBeNull()
    expect(container.querySelector('[data-card-id="__epic_cat__:to-start"]')).toBeNull()
  })

  it('doing map with a paused owner keeps the paused affordance (triangle = resume)', async () => {
    wikiSetStatusMock.mockClear()
    turnResumeMock.mockClear()
    const snap = makeOwnerSnapshot('actor-owner', { Status: 'paused' })
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={doingOwnerCards('actor-owner')} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    expect(container.querySelector('[data-card-id="__epic_cat__:to-start"]')).not.toBeNull()
    const btn = container.querySelector('[data-card-id="root:start"] .workflow-status-btn') as HTMLButtonElement
    expect(btn?.classList.contains('workflow-status-btn--paused')).toBe(true)
    // Clicking the triangle resumes the paused owner — equivalent to the
    // composer's resume (turn_resume) — rather than re-starting the workflow.
    await act(async () => { btn.click() })
    await vi.waitFor(() => {
      expect(turnResumeMock).toHaveBeenCalledWith(expect.anything(), { target: 'actor-owner' })
      expect(wikiSetStatusMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({ Id: 'root', Status: 'doing', ExpectedStatus: 'doing' }),
      )
    })
  })

  it('clicking the to-start status button fires onStartWorkflow instead of cycling status', async () => {
    const snap = makeOwnerSnapshot('actor-owner', { LoadState: 'unloaded' })
    agentSnapshotMock.mockReturnValue(snap)
    const onStartWorkflow = vi.fn()

    await act(async () => {
      root.render(<WorkflowGraph cards={doingOwnerCards('actor-owner')} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} onStartWorkflow={onStartWorkflow} />)
    })

    const btn = container.querySelector('[data-card-id="root:start"] .workflow-status-btn') as HTMLButtonElement
    expect(btn).not.toBeNull()
    await act(async () => { btn.click() })
    expect(onStartWorkflow).toHaveBeenCalledWith('root')
  })

  it('clicking the doing status button pauses the owner (cascade) and sets the map to paused', async () => {
    wikiSetStatusMock.mockClear()
    workflowPauseAllMock.mockClear()
    turnPauseMock.mockClear()
    // A waiting owner supervises its children → workflow_pause_all cascades.
    const snap = makeOwnerSnapshot('actor-owner', { Status: 'waiting' })
    agentSnapshotMock.mockReturnValue(snap)
    const onStartWorkflow = vi.fn()

    await act(async () => {
      root.render(<WorkflowGraph cards={doingOwnerCards('actor-owner')} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} onStartWorkflow={onStartWorkflow} />)
    })

    const btn = container.querySelector('[data-card-id="root:start"] .workflow-status-btn') as HTMLButtonElement
    // The doing button shows a pause affordance, not a spinner.
    expect(btn?.classList.contains('workflow-status-btn--doing')).toBe(true)
    await act(async () => { btn.click() })
    expect(onStartWorkflow).not.toHaveBeenCalled()
    await vi.waitFor(() => {
      expect(workflowPauseAllMock).toHaveBeenCalledWith(expect.anything(), {}, { target: 'actor-owner' })
      expect(turnPauseMock).not.toHaveBeenCalled()
      expect(wikiSetStatusMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({ Id: 'root', Status: 'paused', ExpectedStatus: 'doing' }),
      )
    })
  })

  it('clicking the doing status button with a running owner uses turn_pause (no cascade)', async () => {
    wikiSetStatusMock.mockClear()
    workflowPauseAllMock.mockClear()
    turnPauseMock.mockClear()
    const snap = makeOwnerSnapshot('actor-owner', { Status: 'running' })
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={doingOwnerCards('actor-owner')} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    const btn = container.querySelector('[data-card-id="root:start"] .workflow-status-btn') as HTMLButtonElement
    await act(async () => { btn.click() })
    await vi.waitFor(() => {
      expect(turnPauseMock).toHaveBeenCalledWith(expect.anything(), { target: 'actor-owner' })
      expect(workflowPauseAllMock).not.toHaveBeenCalled()
      expect(wikiSetStatusMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({ Id: 'root', Status: 'paused', ExpectedStatus: 'doing' }),
      )
    })
  })

  it('clicking the paused status button resumes the owner and sets the map back to doing', async () => {
    wikiSetStatusMock.mockClear()
    turnResumeMock.mockClear()
    const snap = makeOwnerSnapshot('actor-owner', { Status: 'paused' })
    agentSnapshotMock.mockReturnValue(snap)
    const mapCards = doingOwnerCards('actor-owner')
    mapCards[0]!.status = 'paused'

    await act(async () => {
      root.render(<WorkflowGraph cards={mapCards} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    // A paused owner makes a doing map classify as to-start; set the map to
    // paused explicitly so the button shows the paused (resume) affordance.
    expect(mapCards[0]!.status).toBe('paused')
    const btn = container.querySelector('[data-card-id="root:start"] .workflow-status-btn') as HTMLButtonElement
    expect(btn?.classList.contains('workflow-status-btn--paused')).toBe(true)
    await act(async () => { btn.click() })
    await vi.waitFor(() => {
      expect(turnResumeMock).toHaveBeenCalledWith(expect.anything(), { target: 'actor-owner' })
      expect(wikiSetStatusMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({ Id: 'root', Status: 'doing', ExpectedStatus: 'paused' }),
      )
    })
  })

  it('expanded start sentinel shows the title and owner avatar, never status text', async () => {
    const snap = makeOwnerSnapshot('actor-owner')
    agentSnapshotMock.mockReturnValue(snap)

    await act(async () => {
      root.render(<WorkflowGraph cards={doingOwnerCards('actor-owner')} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} />)
    })

    const sentinel = container.querySelector('[data-card-id="root:start"] .workflow-node-overlay-inner--sentinel-start')!
    expect(sentinel).not.toBeNull()
    // Title, not the raw status string.
    expect(sentinel.querySelector('.workflow-sentinel-title')?.textContent).toBe('root')
    expect(sentinel.textContent).not.toContain('doing')
    // Owner avatar in the bottom-right corner.
    const avatar = sentinel.querySelector('.workflow-sentinel-agent-corner .card-agent-avatar')
    expect(avatar).not.toBeNull()
  })

  it('no avatar corner renders when the workflow has no owner', async () => {
    agentSnapshotMock.mockReturnValue({ version: 1, loading: false, items: [], byActorId: new Map(), byId: new Map() })

    await act(async () => {
      root.render(<WorkflowGraph cards={doingOwnerCards()} expandedWorkflows={EXPANDED_ROOT} />)
    })

    const sentinel = container.querySelector('[data-card-id="root:start"] .workflow-node-overlay-inner--sentinel-start')!
    expect(sentinel.querySelector('.workflow-sentinel-agent-corner')).toBeNull()
  })
})

// ── Tests: avatar click → open-agent-chat ────────────────────────────

describe('WorkflowGraph avatar click dispatches open-agent-chat', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('workflow variant corner avatar click dispatches sporemind:open-agent-chat with correct detail', async () => {
    const cards = minimalCards()
    const snap = makeAgentSnapshot('taskA')
    agentSnapshotMock.mockReturnValue(snap)

    const onNodeClick = vi.fn()
    await act(async () => {
      root.render(<WorkflowGraph cards={cards} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} onNodeClick={onNodeClick} />)
    })

    const events: { projectId: string; agentActorId: string }[] = []
    const handler = (e: Event) => {
      events.push((e as CustomEvent).detail)
    }
    window.addEventListener('sporemind:open-agent-chat', handler)

    const avatar = container.querySelector('.mono-card-compact-agent-corner .card-agent-avatar') as HTMLElement
    expect(avatar).not.toBeNull()
    await act(async () => { avatar.click() })

    window.removeEventListener('sporemind:open-agent-chat', handler)

    // Event dispatched with the agent's ProjectId and ActorId.
    expect(events).toHaveLength(1)
    expect(events[0]!.projectId).toBe('proj-1')
    expect(events[0]!.agentActorId).toBe('actor-1')
    // Card onClick must NOT fire — stopPropagation prevents it.
    expect(onNodeClick).not.toHaveBeenCalled()
  })

  it('sentinel owner avatar click dispatches sporemind:open-agent-chat with correct detail', async () => {
    const snap = makeOwnerSnapshot('actor-owner')
    agentSnapshotMock.mockReturnValue(snap)

    const onNodeClick = vi.fn()
    await act(async () => {
      root.render(<WorkflowGraph cards={doingOwnerCards('actor-owner')} agentInfoSnapshot={snap} expandedWorkflows={EXPANDED_ROOT} onNodeClick={onNodeClick} />)
    })

    const events: { projectId: string; agentActorId: string }[] = []
    const handler = (e: Event) => {
      events.push((e as CustomEvent).detail)
    }
    window.addEventListener('sporemind:open-agent-chat', handler)

    const avatar = container.querySelector('.workflow-sentinel-agent-corner .card-agent-avatar') as HTMLElement
    expect(avatar).not.toBeNull()
    await act(async () => { avatar.click() })

    window.removeEventListener('sporemind:open-agent-chat', handler)

    expect(events).toHaveLength(1)
    expect(events[0]!.projectId).toBe('proj-1')
    expect(events[0]!.agentActorId).toBe('actor-owner')
    // Sentinel onClick must NOT fire — stopPropagation prevents it.
    expect(onNodeClick).not.toHaveBeenCalled()
  })

  it('default variant header avatar is not interactive (no clickable affordance)', async () => {
    // Render a MonoCardCompact in default variant (no variant="workflow").
    const snap = makeAgentSnapshot('taskA')
    agentSnapshotMock.mockReturnValue(snap)

    const { MonoCardCompact } = await import('./MonoCardCompact')
    await act(async () => {
      root.render(
        <MonoCardCompact
          card={makeCard('test', { type: 'task', status: 'todo' })}
          agentRef="actor-1"
        />
      )
    })

    const avatar = container.querySelector('.card-agent-avatar') as HTMLElement
    expect(avatar).not.toBeNull()
    // No clickable class, no role=button — default behavior preserved.
    expect(avatar.classList.contains('card-agent-avatar--clickable')).toBe(false)
    expect(avatar.getAttribute('role')).toBeNull()
  })
})

// ── Tests: scheduler node rendering ──────────────────────────────────

describe('WorkflowGraph scheduler node rendering', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    document.body.removeChild(container)
  })

  function schedulerCards(): MonoCardListItem[] {
    return [
      makeMap('root', ['taskA']),
      makeCard('taskA', { type: 'task', status: 'todo' }),
      // Scheduler card with a cron schedule and template reference.
      makeCard('sched:my-scheduler', {
        type: 'scheduler',
        tags: ['scheduler'],
        data: { schedule: { cron: '0 9 * * *', enabled: true }, workflow_template: 'template-map', executor: 'default' },
      }),
      // Instance workflow referencing the scheduler. Needs doing + instance_of
      // so it classifies as planned (ownerless template instance) under the
      // scheduler node; agentIds defaults to an empty Set in WorkflowGraph.
      makeCard('instance-1', {
        type: 'workflow',
        status: 'doing',
        data: { scheduler_card_id: 'sched:my-scheduler', instance_of: 'template-map' },
      }),
    ]
  }

  function schedulerCardsZero(): MonoCardListItem[] {
    return [
      makeMap('root', ['taskA']),
      makeCard('taskA', { type: 'task', status: 'todo' }),
      makeCard('sched:my-scheduler', {
        type: 'scheduler',
        tags: ['scheduler'],
        data: { schedule: { cron: '0 9 * * *', enabled: true }, workflow_template: 'template-map', executor: 'default' },
      }),
      makeMap('template-map', ['taskT']),
      makeCard('taskT', { type: 'task', status: 'todo' }),
    ]
  }

  it('renders a ghost template preview under an expanded zero-instance scheduler', async () => {
    await act(async () => {
      root.render(<WorkflowGraph cards={schedulerCardsZero()} expandedBuckets={new Set(['sched:my-scheduler'])} />)
    })

    const ghost = container.querySelector('.workflow-node-overlay-inner--ghost') as HTMLElement
    expect(ghost).not.toBeNull()
    expect(ghost.querySelector('.workflow-ghost-title')?.textContent).toBe('template-map')
    // The ghost tag reuses the template category label.
    expect(ghost.querySelector('.workflow-ghost-tag')?.textContent).not.toBe('')
  })

  it('hides the ghost preview when the zero-instance scheduler is collapsed', async () => {
    await act(async () => {
      root.render(<WorkflowGraph cards={schedulerCardsZero()} />)
    })

    expect(container.querySelector('.workflow-node-overlay-inner--ghost')).toBeNull()
  })

  it('opens the template card when the ghost preview is clicked', async () => {
    const onNodeClick = vi.fn()
    await act(async () => {
      root.render(<WorkflowGraph cards={schedulerCardsZero()} expandedBuckets={new Set(['sched:my-scheduler'])} onNodeClick={onNodeClick} />)
    })

    const ghost = container.querySelector('.workflow-node-overlay-inner--ghost') as HTMLElement
    await act(async () => { ghost.click() })
    expect(onNodeClick).toHaveBeenCalledWith('template-map', 'template-map')
  })

  it('renders a card-sized scheduler node with the scheduler id', async () => {
    await act(async () => {
      root.render(<WorkflowGraph cards={schedulerCards()} />)
    })

    // Scheduler nodes are card-sized (same as task nodes), not compact badges.
    const node = container.querySelector('.workflow-node-overlay-inner--scheduler') as HTMLElement
    expect(node).not.toBeNull()
    // The card title strips the "sched:" prefix (full id kept as tooltip).
    const title = node.querySelector('.mono-card-compact-title') as HTMLElement
    expect(title.textContent).toBe('my-scheduler')
    expect(title.getAttribute('title')).toBe('sched:my-scheduler')
  })

  it('renders a clock button that calls onEditSchedule', async () => {
    const onEdit = vi.fn()
    await act(async () => {
      root.render(<WorkflowGraph cards={schedulerCards()} onEditSchedule={onEdit} />)
    })

    const clockBtn = container.querySelector('.workflow-node-overlay-inner--scheduler .workflow-status-btn--scheduler') as HTMLElement
    expect(clockBtn).not.toBeNull()
    await act(async () => { clockBtn.click() })
    expect(onEdit).toHaveBeenCalledWith('sched:my-scheduler')
  })

  it('renders a fold button on the scheduler node', async () => {
    await act(async () => {
      root.render(<WorkflowGraph cards={schedulerCards()} />)
    })

    const foldBtn = container.querySelector('.workflow-node-overlay-inner--scheduler .workflow-fold-btn') as HTMLElement
    expect(foldBtn).not.toBeNull()
  })

  it('calls onToggleBucketFold when the scheduler fold button is clicked', async () => {
    const onToggle = vi.fn()
    await act(async () => {
      root.render(<WorkflowGraph cards={schedulerCards()} onToggleBucketFold={onToggle} />)
    })

    const foldBtn = container.querySelector('.workflow-node-overlay-inner--scheduler .workflow-fold-btn') as HTMLElement
    expect(foldBtn).not.toBeNull()

    await act(async () => { foldBtn.click() })
    expect(onToggle).toHaveBeenCalledWith('sched:my-scheduler')
  })

  it('applies collapsed class when scheduler is not in expandedBuckets', async () => {
    await act(async () => {
      root.render(<WorkflowGraph cards={schedulerCards()} />)
    })

    // Default: not expanded, so collapsed class is present.
    const collapsed = container.querySelector('.workflow-node-overlay-inner--scheduler.workflow-node-overlay-inner--collapsed') as HTMLElement
    expect(collapsed).not.toBeNull()
  })

  it('does not apply collapsed class when scheduler is in expandedBuckets', async () => {
    const expanded = new Set<string>(['sched:my-scheduler'])
    await act(async () => {
      root.render(<WorkflowGraph cards={schedulerCards()} expandedBuckets={expanded} />)
    })

    const collapsed = container.querySelector('.workflow-node-overlay-inner--scheduler.workflow-node-overlay-inner--collapsed')
    expect(collapsed).toBeNull()
  })
})

// ── Tests: re-activation viewport visibility check ─────────────────

/** `anyNodeInViewport` is the pure predicate that decides whether the
 *  re-activation auto-fit fires when the (cacheable) workflow pane is shown
 *  again: fit only when the viewport the user left behind points at empty
 *  space. The ResizeObserver orchestration (display:none→flex 0×0→size
 *  transition) is browser-glue and not unit-testable here, so the pure
 *  predicate is extracted and covered directly. */
describe('anyNodeInViewport (re-activation auto-fit predicate)', () => {
  function makeNetwork(
    scale: number,
    center: { x: number; y: number },
    nodes: Record<string, { x: number; y: number }>,
  ): Network {
    return {
      getScale: () => scale,
      getViewPosition: () => center,
      body: { nodes },
    } as unknown as Network
  }

  const viewport = { clientWidth: 1000, clientHeight: 800 }

  it('returns true when a node is within the viewport bounds', () => {
    const net = makeNetwork(1, { x: 0, y: 0 }, { n1: { x: 0, y: 0 } })
    expect(anyNodeInViewport(net, viewport)).toBe(true)
  })

  it('returns false when all nodes are far outside the viewport', () => {
    const net = makeNetwork(1, { x: 0, y: 0 }, { n1: { x: 100000, y: 100000 } })
    expect(anyNodeInViewport(net, viewport)).toBe(false)
  })

  it('treats a node on the boundary edge as visible', () => {
    // scale 1, width 1000 → halfW 500, halfH 400 → bounds [±500, ±400].
    const net = makeNetwork(1, { x: 0, y: 0 }, { edge: { x: 500, y: 400 } })
    expect(anyNodeInViewport(net, viewport)).toBe(true)
  })

  it('returns false when there are no nodes', () => {
    const net = makeNetwork(1, { x: 0, y: 0 }, {})
    expect(anyNodeInViewport(net, viewport)).toBe(false)
  })

  it('returns false for a non-positive scale', () => {
    const net = makeNetwork(0, { x: 0, y: 0 }, { n1: { x: 0, y: 0 } })
    expect(anyNodeInViewport(net, viewport)).toBe(false)
  })

  it('returns false for a zero-size container', () => {
    const net = makeNetwork(1, { x: 0, y: 0 }, { n1: { x: 0, y: 0 } })
    expect(anyNodeInViewport(net, { clientWidth: 0, clientHeight: 0 })).toBe(false)
  })

  it('honors zoom: a far node becomes visible when zoomed out', () => {
    // At scale 1 the node at x=100000 is off-screen (halfW=500).
    expect(anyNodeInViewport(makeNetwork(1, { x: 0, y: 0 }, { n1: { x: 100000, y: 0 } }), viewport)).toBe(false)
    // Zoomed out to scale 0.001 → halfW = 1000/(2*0.001) = 500000 → on-screen.
    expect(anyNodeInViewport(makeNetwork(0.001, { x: 0, y: 0 }, { n1: { x: 100000, y: 0 } }), viewport)).toBe(true)
  })
})

/** `shouldDrawFrame` is the pure throttle predicate of the working-edge
 *  animation loop (see `schedule` in WorkflowGraph.tsx): rAF keeps firing at
 *  ~60fps, but the full edge+node redraw only happens every `DRAW_INTERVAL_MS`
 *  (~15fps). The visibility pause (container 0×0 while a cacheable pane is
 *  `display:none`) is browser glue — happy-dom reports clientWidth/clientHeight
 *  as 0 regardless of layout, so the pause/resume orchestration is not
 *  unit-testable here; the throttle decision is extracted and covered instead. */
describe('shouldDrawFrame (animation redraw throttle)', () => {
  it('draws the first frame immediately — no previous timestamp (mount/resume)', () => {
    expect(shouldDrawFrame(null, 1000, DRAW_INTERVAL_MS)).toBe(true)
  })

  it('draws once the interval has elapsed since the last drawn frame', () => {
    expect(shouldDrawFrame(0, DRAW_INTERVAL_MS, DRAW_INTERVAL_MS)).toBe(true)
    expect(shouldDrawFrame(1000, 1000 + DRAW_INTERVAL_MS, DRAW_INTERVAL_MS)).toBe(true)
  })

  it('skips frames that arrive inside the interval', () => {
    expect(shouldDrawFrame(0, DRAW_INTERVAL_MS - 1, DRAW_INTERVAL_MS)).toBe(false)
    expect(shouldDrawFrame(1000, 1030, 66)).toBe(false)
  })

  it('is false for zero or negative elapsed time', () => {
    expect(shouldDrawFrame(1000, 1000, 66)).toBe(false)
    expect(shouldDrawFrame(1000, 900, 66)).toBe(false)
  })

  it('honors custom intervals (e.g. 120Hz displays)', () => {
    // ~8.3ms rAF ticks still gated by the same predicate.
    expect(shouldDrawFrame(0, 60, 100)).toBe(false)
    expect(shouldDrawFrame(0, 100, 100)).toBe(true)
  })
})
