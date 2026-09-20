import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { fireEvent } from '@testing-library/react'
import type { MonoCardListItem } from '../../../domain/mono-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// ── Hoisted mocks ──────────────────────────────────────────────────

const { savePreferenceMock, loadPreferenceMock, wfGraphProps, monoStateMock } = vi.hoisted(() => ({
  savePreferenceMock: vi.fn<(...a: unknown[]) => Promise<void>>(),
  loadPreferenceMock: vi.fn<(k: string) => Promise<string | undefined>>(),
  /** Captured props from the latest WorkflowGraph mock render. */
  wfGraphProps: { current: null as Record<string, unknown> | null },
  /** Drives the TopologyModeView loading overlay without touching the real
   *  monoStore (which would otherwise attempt network calls). */
  monoStateMock: { current: { loading: false, error: null as string | null } },
}))

vi.mock('../../../application/theme-persist', () => ({
  savePreference: savePreferenceMock,
  loadPreference: loadPreferenceMock,
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

// Stub the sibling graph components — only the workflow path is exercised.
vi.mock('./TopologyGraph', () => ({ TopologyGraph: () => null }))
import { workflowFoldStore } from './workflowFoldStore'
vi.mock('./ActorTopology', () => ({ ActorTopology: () => null }))
vi.mock('./BrainMemoryGraph', () => ({ BrainMemoryGraph: () => null }))

// WorkflowGraph is mocked as a spy so we can assert on the props
// TopologyModeView forwards (expandedWorkflows, locateRequest, …).
vi.mock('./WorkflowGraph', () => ({
  WorkflowGraph: (props: Record<string, unknown>) => {
    wfGraphProps.current = props
    return null
  },
}))

// External clients and hooks that need no real behaviour in these tests.
vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/local/client', () => ({ memorySnapshot: vi.fn() }))
vi.mock('../../../application/useViewportMode', () => ({ useViewportMode: () => 'desktop' }))
vi.mock('../hooks/useClickOutside', () => ({ useClickOutside: () => {} }))
vi.mock('../hooks/useTimelineManager', () => ({
  getTimelineManager: () => ({ subscribe: () => () => {} }),
}))

vi.mock('../hooks/useMonoStore', () => ({
  useMonoStore: (selector?: (s: { loading: boolean; error: string | null }) => unknown) =>
    selector ? selector(monoStateMock.current) : monoStateMock.current,
}))

// Import AFTER mocks are set up.
import { TopologyModeView } from './TopologyModeView'
import { requestWorkflowLocate, consumePendingWorkflowLocate } from './workflowLocateStore'
import { epicWorkflowPlacement } from './workflowLayout'

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
    modified: '2026-08-01T00:00:00Z',
    ...over,
  }
}

function makeMap(id: string, include: string[]): MonoCardListItem {
  return makeCard(id, { type: 'workflow', data: { scope: { include } } })
}

/** One workflow map with a single task. */
function minimalCards(): MonoCardListItem[] {
  return [
    makeMap('root', ['taskA']),
    makeCard('taskA', { type: 'task', status: 'todo' }),
  ]
}

function renderView(cards: MonoCardListItem[], mode: 'workflow' | 'cards' = 'workflow') {
  return act(async () => {
    root.render(
      <TopologyModeView
        cards={cards}
        mode={mode}
        activeFilterIds={new Set<string>()}
        setActiveFilterIds={() => {}}
      />,
    )
  })
}

// Shared across tests so afterEach can clean up.
let container: HTMLDivElement
let root: Root

// ── Tests: locate consume → expand + forward ───────────────────────

describe('TopologyModeView workflow locate consume', () => {
  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)
    wfGraphProps.current = null
    workflowFoldStore.reset()

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    // Clean up any unconsumed store state between tests.
    consumePendingWorkflowLocate()
  })

  it('forwards selectStart; onLocateWorkflow unfolds the folded workflow', async () => {
    // Persist a folded set containing 'root' so that, after the async load,
    // the workflow is collapsed.
    loadPreferenceMock.mockImplementation((key: string) =>
      key === 'workflowGraph.foldedWorkflows.v1' ? Promise.resolve(JSON.stringify(['root'])) : Promise.resolve(undefined),
    )

    await renderView(minimalCards())
    // Flush the async preference load so the collapse takes effect.
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    // Sanity: 'root' is collapsed after the empty preference loaded.
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('root')).toBe(false)

    // Simulate openWorkflowAndLocate — a selectStart locate via the store.
    await act(async () => {
      requestWorkflowLocate({ mapId: 'root', selectStart: true })
    })

    // The subscribe handler consumed the request and forwarded a locateRequest
    // carrying selectStart. Unfolding is WorkflowGraph's job: it calls
    // onLocateWorkflow when the locate resolves a concrete map.
    await vi.waitFor(() => {
      const locateReq = wfGraphProps.current!.locateRequest as { nonce: number; mapId?: string; selectStart?: boolean }
      expect(locateReq).not.toBeNull()
      expect(locateReq.mapId).toBe('root')
      expect(locateReq.selectStart).toBe(true)
    })

    // The request was consumed from the store.
    expect(consumePendingWorkflowLocate()).toBeNull()

    // Simulate WorkflowGraph resolving the target: the workflow unfolds and
    // the fold removal is persisted.
    await act(async () => {
      ;(wfGraphProps.current!.onLocateWorkflow as (id: string) => void)('root')
    })
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('root')).toBe(true)
    expect(savePreferenceMock).toHaveBeenCalledWith(
      'workflowGraph.foldedWorkflows.v1',
      '[]',
      'workflow-graph-expanded',
      expect.anything(),
    )
  })

  it('onLocateWorkflow unfolds the category band and the archived bucket', async () => {
    // An archived workflow: done, last activity far older than ARCHIVE_AFTER_MS
    // (both the map and its task — last activity takes the newest).
    const archivedCards = [
      makeMap('old-wf', ['t1']),
      makeCard('t1', { type: 'task', status: 'done', modified: '2020-01-01T00:00:00Z' }),
    ]
    archivedCards[0]!.status = 'done'
    archivedCards[0]!.modified = '2020-01-01T00:00:00Z'

    await renderView(archivedCards)
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    // Fold the archived category band.
    await act(async () => {
      ;(wfGraphProps.current!.onToggleCategoryFold as (c: string) => void)('archived')
    })
    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('archived')).toBe(true)

    await act(async () => {
      ;(wfGraphProps.current!.onLocateWorkflow as (id: string) => void)('old-wf')
    })

    // Category band unfolded and persisted…
    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('archived')).toBe(false)
    expect(savePreferenceMock).toHaveBeenCalledWith(
      'workflowGraph.foldedCategories.v1',
      '[]',
      'workflow-graph-folded-categories',
      expect.anything(),
    )
    // …and the containing time bucket expanded and persisted.
    const expanded = wfGraphProps.current!.expandedBuckets as Set<string>
    expect(expanded.size).toBe(1)
    expect([...expanded][0]).toMatch(/^__epic_bucket__:year:2020$/)
    expect(savePreferenceMock).toHaveBeenCalledWith(
      'workflowGraph.archivedExpanded.v1',
      JSON.stringify([...expanded]),
      'workflow-graph-archived-buckets',
      expect.anything(),
    )
  })

  it('consumes a pre-armed locate on mount and forwards it with selectStart', async () => {
    // Pre-arm before mount (the typical openWorkflowAndLocate → mount flow).
    requestWorkflowLocate({ mapId: 'root', selectStart: true })

    await renderView(minimalCards())

    // The consume effect fires synchronously on mount in workflow mode.
    await vi.waitFor(() => {
      const locateReq = wfGraphProps.current!.locateRequest as { nonce: number; mapId?: string; selectStart?: boolean }
      expect(locateReq).not.toBeNull()
      expect(locateReq.mapId).toBe('root')
      expect(locateReq.selectStart).toBe(true)
    })

    // The pending request has been consumed.
    expect(consumePendingWorkflowLocate()).toBeNull()
  })

  it('does not consume locates outside workflow mode', async () => {
    requestWorkflowLocate({ mapId: 'root', selectStart: true })

    await renderView(minimalCards(), 'cards')
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    // The locate is still pending — the consume effect is gated on workflow mode.
    expect(consumePendingWorkflowLocate()).toEqual({ mapId: 'root', selectStart: true })
  })
})

// ── Tests: archived bucket fold persistence ──────────────────────────

describe('TopologyModeView archived bucket fold', () => {
  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)
    wfGraphProps.current = null
    workflowFoldStore.reset()

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    consumePendingWorkflowLocate()
  })

  const BUCKET = '__epic_bucket__:date:2026-08-01'

  it('restores expanded bucket ids from the backend preference', async () => {
    loadPreferenceMock.mockImplementation((key: string) =>
      key === 'workflowGraph.archivedExpanded.v1' ? Promise.resolve(JSON.stringify([BUCKET])) : Promise.resolve(undefined),
    )

    await renderView(minimalCards())
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    expect((wfGraphProps.current!.expandedBuckets as Set<string>).has(BUCKET)).toBe(true)
  })

  it('buckets default to folded; toggling expands and persists the id set', async () => {
    await renderView(minimalCards())
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    expect((wfGraphProps.current!.expandedBuckets as Set<string>).has(BUCKET)).toBe(false)

    const toggle = wfGraphProps.current!.onToggleBucketFold as (id: string) => void
    await act(async () => { toggle(BUCKET) })

    expect((wfGraphProps.current!.expandedBuckets as Set<string>).has(BUCKET)).toBe(true)
    expect(savePreferenceMock).toHaveBeenCalledWith(
      'workflowGraph.archivedExpanded.v1',
      JSON.stringify([BUCKET]),
      'workflow-graph-archived-buckets',
      expect.anything(),
    )

    // Toggling again folds and persists an empty set.
    await act(async () => { (wfGraphProps.current!.onToggleBucketFold as (id: string) => void)(BUCKET) })
    expect((wfGraphProps.current!.expandedBuckets as Set<string>).has(BUCKET)).toBe(false)
    expect(savePreferenceMock).toHaveBeenLastCalledWith(
      'workflowGraph.archivedExpanded.v1',
      '[]',
      'workflow-graph-archived-buckets',
      expect.anything(),
    )
  })
})

// ── Tests: category band fold persistence ────────────────────────────

describe('TopologyModeView category fold', () => {
  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)
    wfGraphProps.current = null
    workflowFoldStore.reset()

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    consumePendingWorkflowLocate()
  })

  it('categories default expanded; toggling folds and persists the set', async () => {
    await renderView(minimalCards())
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    // minimalCards' 'root' map has no status → classified as 'to-start'.
    expect((wfGraphProps.current!.foldedCategories as Set<string>).size).toBe(0)

    const toggle = wfGraphProps.current!.onToggleCategoryFold as (c: string) => void
    await act(async () => { toggle('to-start') })

    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('to-start')).toBe(true)
    expect(savePreferenceMock).toHaveBeenCalledWith(
      'workflowGraph.foldedCategories.v1',
      JSON.stringify(['to-start']),
      'workflow-graph-folded-categories',
      expect.anything(),
    )

    // Toggling again expands and persists an empty set.
    await act(async () => { (wfGraphProps.current!.onToggleCategoryFold as (c: string) => void)('to-start') })
    expect((wfGraphProps.current!.foldedCategories as Set<string>).size).toBe(0)
    expect(savePreferenceMock).toHaveBeenLastCalledWith(
      'workflowGraph.foldedCategories.v1',
      '[]',
      'workflow-graph-folded-categories',
      expect.anything(),
    )
  })

  it('restores persisted folded categories after mount', async () => {
    loadPreferenceMock.mockImplementation((key: string) =>
      key === 'workflowGraph.foldedCategories.v1' ? Promise.resolve(JSON.stringify(['to-start'])) : Promise.resolve(undefined),
    )

    await renderView(minimalCards())
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('to-start')).toBe(true)
  })
})

// ── Tests: workflow fold persistence ─────────────────────────────────

describe('TopologyModeView workflow fold persistence', () => {
  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)
    wfGraphProps.current = null
    workflowFoldStore.reset()

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    consumePendingWorkflowLocate()
  })

  it('defaults all workflows to expanded and persists folds when toggled', async () => {
    await renderView(minimalCards())
    expect(wfGraphProps.current).not.toBeNull()
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('root')).toBe(true)

    const toggle = wfGraphProps.current!.onToggleFold as (id: string) => void
    await act(async () => { toggle('root') })

    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('root')).toBe(false)
    expect(savePreferenceMock).toHaveBeenCalledWith(
      'workflowGraph.foldedWorkflows.v1',
      JSON.stringify(['root']),
      'workflow-graph-expanded',
      expect.anything(),
    )
  })

  it('restores the persisted folded set after mount', async () => {
    loadPreferenceMock.mockImplementation((key: string) =>
      key === 'workflowGraph.foldedWorkflows.v1' ? Promise.resolve(JSON.stringify(['root'])) : Promise.resolve(undefined),
    )

    await renderView(minimalCards())
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('root')).toBe(false)
  })

  it('keeps a user fold when the cards list refreshes', async () => {
    await renderView(minimalCards())
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    const toggle = wfGraphProps.current!.onToggleFold as (id: string) => void
    await act(async () => { toggle('root') })
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('root')).toBe(false)

    // A cards-list refresh (new workflow appears) must not resurrect the fold:
    // the folded set is the persisted source of truth, so only genuinely new
    // ids start out expanded.
    const withNew = [...minimalCards(), makeMap('new-wf', ['taskB']), makeCard('taskB', { type: 'task' })]
    await renderView(withNew)
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('root')).toBe(false)
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('new-wf')).toBe(true)
  })
})

// ── Tests: parent fold cascades to the subtree ───────────────────────

describe('TopologyModeView cascade fold', () => {
  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)
    wfGraphProps.current = null
    workflowFoldStore.reset()

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    consumePendingWorkflowLocate()
  })

  it('folding a category band also folds the workflows inside it', async () => {
    await renderView(minimalCards())
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('root')).toBe(true)

    const toggle = wfGraphProps.current!.onToggleCategoryFold as (c: string) => void
    await act(async () => { toggle('to-start') })

    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('to-start')).toBe(true)
    // The subtree collapsed with the parent.
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('root')).toBe(false)

    // Re-expanding the band keeps the children folded.
    await act(async () => { (wfGraphProps.current!.onToggleCategoryFold as (c: string) => void)('to-start') })
    expect((wfGraphProps.current!.foldedCategories as Set<string>).size).toBe(0)
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('root')).toBe(false)
  })

  it('collapsing an archived bucket also folds the workflows inside it', async () => {
    const old = '2026-07-20T12:00:00Z'
    const map = makeCard('old-wf', { type: 'workflow', status: 'done', modified: old, data: { scope: { include: [] } } })
    const cards = [map]
    const placement = epicWorkflowPlacement(map, cards)
    expect(placement.category).toBe('archived')
    const bucketId = placement.bucketId!

    await renderView(cards)
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    const toggle = wfGraphProps.current!.onToggleBucketFold as (id: string) => void
    // Expand the bucket, then collapse it — the workflow folds with it.
    await act(async () => { toggle(bucketId) })
    expect((wfGraphProps.current!.expandedBuckets as Set<string>).has(bucketId)).toBe(true)
    await act(async () => { (wfGraphProps.current!.onToggleBucketFold as (id: string) => void)(bucketId) })
    expect((wfGraphProps.current!.expandedBuckets as Set<string>).has(bucketId)).toBe(false)
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('old-wf')).toBe(false)
  })
})

// ── Tests: unfolding a workflow cascades to its ancestor band/bucket ──

describe('TopologyModeView unfold cascade', () => {
  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)
    wfGraphProps.current = null
    workflowFoldStore.reset()

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    consumePendingWorkflowLocate()
  })

  it('unfolding an archived workflow expands its time bucket and category band', async () => {
    const old = '2020-01-01T00:00:00Z'
    const map = makeCard('old-wf', { type: 'workflow', status: 'done', modified: old, data: { scope: { include: [] } } })
    const cards = [map]
    const placement = epicWorkflowPlacement(map, cards)
    expect(placement.category).toBe('archived')
    const bucketId = placement.bucketId!

    // Start fully collapsed: band folded + workflow folded (as a bucket
    // collapse would have left it).
    loadPreferenceMock.mockImplementation((key: string) => {
      if (key === 'workflowGraph.foldedCategories.v1') return Promise.resolve(JSON.stringify(['archived']))
      if (key === 'workflowGraph.foldedWorkflows.v1') return Promise.resolve(JSON.stringify(['old-wf']))
      return Promise.resolve(undefined)
    })

    await renderView(cards)
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('archived')).toBe(true)
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('old-wf')).toBe(false)

    // Unfold the single workflow: the bucket and the band must open with it,
    // or the server-side fold filter would keep its tasks hidden forever.
    await act(async () => { (wfGraphProps.current!.onToggleFold as (id: string) => void)('old-wf') })

    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('old-wf')).toBe(true)
    expect((wfGraphProps.current!.expandedBuckets as Set<string>).has(bucketId)).toBe(true)
    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('archived')).toBe(false)
  })

  it('unfolding a planned workflow expands its scheduler bucket and category band', async () => {
    const scheduler = makeCard('sched-1', { type: 'scheduler' })
    const map = makeCard('planned-wf', {
      type: 'workflow',
      status: 'doing',
      data: { scope: { include: [] }, instance_of: 'tpl::base', scheduler_card_id: 'sched-1' },
    })
    const cards = [map, scheduler]
    const placement = epicWorkflowPlacement(map, cards, { agentIds: new Set<string>() })
    expect(placement.category).toBe('planned')
    const bucketId = placement.bucketId!

    loadPreferenceMock.mockImplementation((key: string) => {
      if (key === 'workflowGraph.foldedCategories.v1') return Promise.resolve(JSON.stringify(['planned']))
      if (key === 'workflowGraph.foldedWorkflows.v1') return Promise.resolve(JSON.stringify(['planned-wf']))
      return Promise.resolve(undefined)
    })

    await renderView(cards)
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('planned')).toBe(true)
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('planned-wf')).toBe(false)

    await act(async () => { (wfGraphProps.current!.onToggleFold as (id: string) => void)('planned-wf') })

    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('planned-wf')).toBe(true)
    expect((wfGraphProps.current!.expandedBuckets as Set<string>).has(bucketId)).toBe(true)
    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('planned')).toBe(false)
  })
})

// ── Tests: category right-click menu (expand all / collapse all) ─────

describe('TopologyModeView category context menu', () => {
  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)
    wfGraphProps.current = null
    workflowFoldStore.reset()

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    consumePendingWorkflowLocate()
  })

  const menuButtons = () =>
    [...container.querySelectorAll<HTMLButtonElement>('.topology-card-context-menu-item')]
  const clickItem = async (label: string) => {
    const button = menuButtons().find(b => b.textContent?.includes(label))
    expect(button, `menu item ${label}`).toBeTruthy()
    await act(async () => { button!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
  }

  it('right-clicking a category node opens a menu with expand/collapse all', async () => {
    const cards = [makeMap('m1', []), makeMap('m2', [])]
    await renderView(cards)
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    // Fold m1 so expand-all has something to unfold.
    await act(async () => { (wfGraphProps.current!.onToggleFold as (id: string) => void)('m1') })
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('m1')).toBe(false)

    // Open the category menu (i18n mock echoes keys as labels).
    await act(async () => {
      (wfGraphProps.current!.onCategoryMenu as (c: string, x: number, y: number) => void)('to-start', 100, 100)
    })
    expect(menuButtons().map(b => b.textContent)).toEqual([
      expect.stringContaining('workflowCategoryContextMenu.expandAll'),
      expect.stringContaining('workflowCategoryContextMenu.collapseAll'),
    ])

    // Expand all unfolds every workflow in the band and closes the menu.
    await clickItem('workflowCategoryContextMenu.expandAll')
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('m1')).toBe(true)
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('m2')).toBe(true)
    expect(menuButtons()).toHaveLength(0)

    // Collapse all folds every workflow in the band; the band stays open.
    await act(async () => {
      (wfGraphProps.current!.onCategoryMenu as (c: string, x: number, y: number) => void)('to-start', 100, 100)
    })
    await clickItem('workflowCategoryContextMenu.collapseAll')
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('m1')).toBe(false)
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('m2')).toBe(false)
    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('to-start')).toBe(false)
  })

  it('expand all also unfolds the band itself and archived buckets', async () => {
    const old = '2026-07-20T12:00:00Z'
    const map = makeCard('old-wf', { type: 'workflow', status: 'done', modified: old, data: { scope: { include: [] } } })
    const cards = [map]
    const placement = epicWorkflowPlacement(map, cards)
    const bucketId = placement.bucketId!

    // Start from a fully collapsed state: band folded, workflow folded.
    loadPreferenceMock.mockImplementation((key: string) => {
      if (key === 'workflowGraph.foldedCategories.v1') return Promise.resolve(JSON.stringify(['archived']))
      if (key === 'workflowGraph.foldedWorkflows.v1') return Promise.resolve(JSON.stringify(['old-wf']))
      return Promise.resolve(undefined)
    })

    await renderView(cards)
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('archived')).toBe(true)

    await act(async () => {
      (wfGraphProps.current!.onCategoryMenu as (c: string, x: number, y: number) => void)('archived', 100, 100)
    })
    await clickItem('workflowCategoryContextMenu.expandAll')

    expect((wfGraphProps.current!.foldedCategories as Set<string>).has('archived')).toBe(false)
    expect((wfGraphProps.current!.expandedWorkflows as Set<string>).has('old-wf')).toBe(true)
    expect((wfGraphProps.current!.expandedBuckets as Set<string>).has(bucketId)).toBe(true)
  })
})

// ── Tests: workflow view-mode switch + persistence ───────────────

describe('TopologyModeView workflow filter', () => {
  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)
    wfGraphProps.current = null
    workflowFoldStore.reset()

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    consumePendingWorkflowLocate()
  })

  it('filter status options include the canonical 8-value schema', async () => {
    await renderView(minimalCards())
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    // Open the workflow filter popover.
    await act(async () => { fireEvent.click(container.querySelector<HTMLButtonElement>('button[title="workflowFilter.filters"]')!) })

    const statusChips = [...container.querySelectorAll<HTMLButtonElement>('.workflow-filter-section[aria-label="workflowFilter.status"] .workflow-filter-chips button')]
    const labels = statusChips.map(b => b.textContent)

    expect(labels).toEqual([
      'workflowFilter.status.backlog',
      'workflowFilter.status.todo',
      'workflowFilter.status.doing',
      'workflowFilter.status.pending_review',
      'workflowFilter.status.done',
      'workflowFilter.status.blocked',
      'workflowFilter.status.cancelled',
      'workflowFilter.status.failed',
    ])
  })
})

// ── Tests: loading status indicator ─────────────────────────────────

describe('TopologyModeView loading status indicator', () => {
  beforeEach(() => {
    savePreferenceMock.mockReset()
    loadPreferenceMock.mockReset()
    loadPreferenceMock.mockResolvedValue(undefined)
    savePreferenceMock.mockResolvedValue(undefined)
    wfGraphProps.current = null
    workflowFoldStore.reset()
    monoStateMock.current = { loading: false, error: null }

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    consumePendingWorkflowLocate()
    monoStateMock.current = { loading: false, error: null }
  })

  it('shows a status overlay while the card list is loading and no cards exist', async () => {
    monoStateMock.current = { loading: true, error: null }
    await renderView([], 'cards')

    const overlay = container.querySelector<HTMLElement>('.topo-loading')
    expect(overlay).not.toBeNull()
    expect(overlay!.getAttribute('role')).toBe('status')
    expect(overlay!.textContent).toContain('topology.loading')
  })

  it('keeps the graph clean once cards have arrived, even mid-reload', async () => {
    monoStateMock.current = { loading: true, error: null }
    await renderView(minimalCards(), 'cards')

    expect(container.querySelector('.topo-loading')).toBeNull()
  })

  it('shows the error text when the initial load failed', async () => {
    monoStateMock.current = { loading: false, error: 'boom' }
    await renderView([], 'cards')

    const overlay = container.querySelector<HTMLElement>('.topo-loading')
    expect(overlay).not.toBeNull()
    expect(overlay!.textContent).toContain('topology.loadError')
  })

  it('renders no overlay in the idle empty state (loaded, no cards)', async () => {
    await renderView([], 'cards')
    // Let the fold-store ensureLoaded() settle so `!foldPrefsLoaded` clears.
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })

    expect(container.querySelector('.topo-loading')).toBeNull()
  })
})
