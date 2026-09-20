import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { fireEvent } from '@testing-library/react'
import { KnowledgeModeView } from './KnowledgeModeView'
import type { MonoCard } from '../../../domain/mono-types'

const kbData = vi.hoisted(() => ({
  listKbProjects: vi.fn(),
  loadProjectToc: vi.fn(),
  loadProjectLaneCards: vi.fn(),
  listKbStarred: vi.fn(),
  setCardStarred: vi.fn(),
  createKbCard: vi.fn(),
}))

const uiState = vi.hoisted(() => ({
  getKbNotesUIState: vi.fn(),
  saveKbNotesUIState: vi.fn(),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: 'en-US' }),
}))

vi.mock('../kb/kbData', () => kbData)

// Cut out the quick views' production data channel / generated-client graph;
// the mode only needs the components themselves. Individual tests decide what
// the source returns (e.g. a starred row to un-star).
const quickData = vi.hoisted(() => ({
  listProjects: vi.fn(),
  listStarred: vi.fn(),
  listRecent: vi.fn(),
  search: vi.fn(),
  setStarred: vi.fn(),
}))

vi.mock('./kbQuickViews.data', () => ({
  defaultKbQuickViewsDataSource: quickData,
}))

vi.mock('../../../application/workspace-ui-state', () => ({
  getKbNotesUIState: uiState.getKbNotesUIState,
  saveKbNotesUIState: uiState.saveKbNotesUIState,
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

function card(id: string, list: string[] = [], body = '', extra: Partial<MonoCard> = {}): MonoCard {
  return { id, tags: [], list, created: '', modified: '', body, raw: '', type: 'wiki', ...extra }
}

/** One `WikiCardTreeNode` as the TOC load returns it. */
function tocNode(id: string, children: unknown[] = []) {
  return { Id: id, Title: id, Type: 'wiki', Status: '', Modified: '', Children: children }
}

function mount(ui: React.ReactElement): { container: HTMLDivElement; root: Root } {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => { root.render(ui) })
  return { container, root }
}

const stackIds = (container: HTMLElement) =>
  Array.from(container.querySelectorAll('.kb-swimlane-card')).map(el => el.getAttribute('data-card-id'))
const isCollapsed = (container: HTMLElement, id: string) =>
  container.querySelector(`[data-card-id="${id}"]`)?.classList.contains('kb-swimlane-card--collapsed') ?? false

describe('KnowledgeModeView', () => {
  let mounted: { container: HTMLDivElement; root: Root } | null = null

  beforeEach(() => {
    vi.clearAllMocks()
    kbData.listKbProjects.mockResolvedValue([
      { Name: 'Alpha', Path: '/alpha', Root: false, ActorId: 'p1' },
      { Name: 'Beta', Path: '/beta', Root: false, ActorId: 'p2' },
    ])
    kbData.loadProjectToc.mockResolvedValue([])
    kbData.loadProjectLaneCards.mockResolvedValue([
      card('toc', ['design']),
      card('design', [], 'Design body'),
      card('project_info', [], 'Project overview'),
    ])
    kbData.listKbStarred.mockResolvedValue([])
    kbData.setCardStarred.mockResolvedValue([])
    kbData.createKbCard.mockResolvedValue('fresh-note')
    quickData.listProjects.mockResolvedValue([])
    quickData.listStarred.mockResolvedValue([])
    quickData.listRecent.mockResolvedValue([])
    quickData.search.mockResolvedValue([])
    quickData.setStarred.mockResolvedValue(undefined)
    uiState.getKbNotesUIState.mockResolvedValue({ view: 'home', lane: null, expandedProjectIds: [] })
    uiState.saveKbNotesUIState.mockResolvedValue(undefined)
  })

  afterEach(() => {
    if (mounted) { act(() => mounted!.root.unmount()); mounted.container.remove(); mounted = null }
  })

  it('renders the two columns: left outline + a quick view', async () => {
    mounted = mount(<KnowledgeModeView />)
    await act(async () => {})
    const { container } = mounted
    expect(container.querySelector('.kb-mode-outline .kb-outline')).toBeTruthy()
    expect(container.querySelector('.kb-mode-main .kb-quick-view--home')).toBeTruthy()
    // Project list loaded from the shared kbData channel.
    expect(kbData.listKbProjects).toHaveBeenCalled()
  })

  it('is not bound to the active project: a pending open request opens that project lane', async () => {
    const onActiveLaneChange = vi.fn()
    mounted = mount(<KnowledgeModeView openRequest={{ key: 1, projectId: 'p1', cardId: 'design' }} onActiveLaneChange={onActiveLaneChange} />)
    await act(async () => {})
    const { container } = mounted
    expect(kbData.loadProjectLaneCards).toHaveBeenCalledWith('p1')
    // The lane shows the project's stack — the toc container is NOT a card,
    // the requested card is present and expanded (focus), siblings visible.
    expect(container.querySelector('.kb-swimlane')).toBeTruthy()
    expect(stackIds(container!)).toEqual(['design', 'project_info'])
    expect(isCollapsed(container!, 'design')).toBe(false)
    expect(isCollapsed(container!, 'project_info')).toBe(true)
    expect(container.querySelector('[data-card-id="toc"]')).toBeNull()
    // The display name is backfilled from the project list (never the actor id).
    expect(onActiveLaneChange).toHaveBeenCalledWith({ projectId: 'p1', projectName: 'Alpha', cardId: 'design' })
    expect(uiState.saveKbNotesUIState).toHaveBeenCalledWith({ lane: { projectId: 'p1', projectName: 'Alpha', cardId: 'design' } })
  })

  it('applies an open request once, ahead of the durable lane, and reports it consumed', async () => {
    const onOpenRequestConsumed = vi.fn()
    // Durable state points at another project: without serialization the late
    // durable restore would overwrite the request's lane (two writers).
    uiState.getKbNotesUIState.mockResolvedValue({
      view: 'home',
      lane: { projectId: 'p2', projectName: 'Beta' },
      expandedProjectIds: [],
    })
    mounted = mount(
      <KnowledgeModeView
        openRequest={{ key: 7, projectId: 'p1', cardId: 'design' }}
        onOpenRequestConsumed={onOpenRequestConsumed}
      />,
    )
    await act(async () => {})
    const { container } = mounted
    expect(stackIds(container)).toContain('design')
    expect(onOpenRequestConsumed).toHaveBeenCalledTimes(1)
    expect(onOpenRequestConsumed).toHaveBeenCalledWith(7)
    // The request wins, so the durable lane's project is never even loaded.
    expect(kbData.loadProjectLaneCards).toHaveBeenCalledTimes(1)
    expect(kbData.loadProjectLaneCards).toHaveBeenCalledWith('p1')
  })

  it('does not replay an already-consumed request when the mode remounts', async () => {
    const onOpenRequestConsumed = vi.fn()
    uiState.getKbNotesUIState.mockResolvedValue({
      view: 'home',
      lane: { projectId: 'p2', projectName: 'Beta' },
      expandedProjectIds: [],
    })
    mounted = mount(
      <KnowledgeModeView
        openRequest={{ key: 7, projectId: 'p1', cardId: 'design' }}
        onOpenRequestConsumed={onOpenRequestConsumed}
      />,
    )
    await act(async () => {})
    expect(stackIds(mounted.container)).toContain('design')

    // The parent clears the request once consumed (kbOpenRequest = null). A mode
    // tab switch remounts the view; the abandoned lane must not come back.
    act(() => mounted!.root.unmount())
    mounted.container.remove()
    mounted = mount(<KnowledgeModeView openRequest={null} onOpenRequestConsumed={onOpenRequestConsumed} />)
    await act(async () => {})
    // Durable restore falls back to the stored lane (p2 has cards loaded in the
    // shared mock — every project resolves the same list).
    expect(mounted.container.querySelector('.kb-swimlane')).toBeTruthy()
    expect(onOpenRequestConsumed).toHaveBeenCalledTimes(1)
  })

  it('does not bind the request-consumed callback identity into the initial load', async () => {
    // The callback is read through a ref: an inline (unstable) prop must not
    // re-run the initial load and re-open the lane.
    uiState.getKbNotesUIState.mockResolvedValue({
      view: 'starred',
      lane: null,
      expandedProjectIds: [],
    })
    mounted = mount(
      <KnowledgeModeView openRequest={{ key: 3, projectId: 'p1' }} onOpenRequestConsumed={() => {}} />,
    )
    await act(async () => {})
    act(() => { mounted!.root.render(<KnowledgeModeView openRequest={null} onOpenRequestConsumed={() => {}} />) })
    await act(async () => {})
    expect(uiState.getKbNotesUIState).toHaveBeenCalledTimes(1)
    expect(kbData.listKbProjects).toHaveBeenCalledTimes(1)
  })

  it('restores a durable lane (focused card expanded) and marks the outline active', async () => {
    uiState.getKbNotesUIState.mockResolvedValue({
      view: 'starred',
      lane: { projectId: 'p2', projectName: 'Beta', cardId: 'design' },
      expandedProjectIds: ['p2'],
    })
    mounted = mount(<KnowledgeModeView />)
    await act(async () => {})
    const { container } = mounted
    // Lane restored (project stack) → swimlane, not the quick view.
    expect(container.querySelector('.kb-swimlane')).toBeTruthy()
    expect(isCollapsed(container, 'design')).toBe(false)
    // Expanded project triggers its TOC lazy load.
    expect(kbData.loadProjectToc).toHaveBeenCalledWith('p2')
  })

  it('switching the top view persists the view and clears the lane', async () => {
    mounted = mount(<KnowledgeModeView />)
    await act(async () => {})
    const { container } = mounted
    const views = container.querySelectorAll('.kb-outline-view')
    expect(views).toHaveLength(3)
    act(() => { fireEvent.click(views[1]!) })
    expect(uiState.saveKbNotesUIState).toHaveBeenCalledWith({ view: 'search', lane: null })
    expect(container.querySelector('.kb-mode-main .kb-quick-view--search')).toBeTruthy()
  })

  it('expanding a project requests its TOC and persists the expansion set', async () => {
    mounted = mount(<KnowledgeModeView />)
    await act(async () => {})
    const { container } = mounted
    const caret = container.querySelector('.kb-outline-caret')
    expect(caret).toBeTruthy()
    act(() => { fireEvent.click(caret!) })
    await act(async () => {})
    expect(kbData.loadProjectToc).toHaveBeenCalledWith('p1')
    expect(uiState.saveKbNotesUIState).toHaveBeenCalledWith({ expandedProjectIds: ['p1'] })
  })

  it('un-starring from the starred view clears the left outline star index', async () => {
    uiState.getKbNotesUIState.mockResolvedValue({ view: 'starred', lane: null, expandedProjectIds: ['p1'] })
    kbData.loadProjectToc.mockResolvedValue([tocNode('toc', [tocNode('card-a')])])
    kbData.listKbStarred.mockResolvedValue([{ ProjectID: 'p1', CardID: 'card-a', ProjectName: 'Alpha' }])
    quickData.listStarred.mockResolvedValue([{ projectId: 'p1', projectName: 'Alpha', cardId: 'card-a' }])
    mounted = mount(<KnowledgeModeView />)
    await act(async () => {})
    const { container } = mounted
    expect(container.querySelector('.kb-outline-starred')).toBeTruthy()

    const unstar = container.querySelector('.kb-quick-item-tail')
    expect(unstar).toBeTruthy()
    act(() => { fireEvent.click(unstar!) })
    await act(async () => {})

    expect(quickData.setStarred).toHaveBeenCalledWith('p1', 'card-a', false)
    expect(container.querySelector('.kb-outline-starred')).toBeNull()
  })

  it('retries a failed TOC load when the project is expanded again', async () => {
    kbData.loadProjectToc
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValue([tocNode('toc', [tocNode('card-a')])])
    mounted = mount(<KnowledgeModeView />)
    await act(async () => {})
    const { container } = mounted
    const caret = () => container.querySelector('.kb-outline-row--project .kb-outline-caret')
    expect(caret()).toBeTruthy()

    act(() => { fireEvent.click(caret()!) })
    await act(async () => {})
    expect(container.querySelector('.kb-outline-note--error')).toBeTruthy()

    act(() => { fireEvent.click(caret()!) }) // collapse
    act(() => { fireEvent.click(caret()!) }) // expand again → retry
    await act(async () => {})
    expect(kbData.loadProjectToc).toHaveBeenCalledTimes(2)
    expect(container.querySelector('.kb-outline-note--error')).toBeNull()
    expect(container.querySelector('.kb-outline-row--card')).toBeTruthy()
  })

  it('retries a failed lane load when the lane is re-entered', async () => {
    kbData.loadProjectLaneCards
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValue([card('toc', ['design']), card('design', [], 'Design body')])
    mounted = mount(<KnowledgeModeView />)
    await act(async () => {})
    const { container } = mounted
    const label = () => container.querySelector('.kb-outline-row--project .kb-outline-label')

    act(() => { fireEvent.click(label()!) })
    await act(async () => {})
    expect(container.querySelector('.kb-mode-placeholder--error')).toBeTruthy()

    act(() => { fireEvent.click(label()!) })
    await act(async () => {})
    expect(kbData.loadProjectLaneCards).toHaveBeenCalledTimes(2)
    expect(container.querySelector('.kb-swimlane')).toBeTruthy()
  })

  it('clicking a card in the left tree shows the project stack with that card focused, not re-rooted', async () => {
    kbData.loadProjectToc.mockResolvedValue([tocNode('toc', [tocNode('design'), tocNode('project_info')])])
    kbData.loadProjectLaneCards.mockResolvedValue([
      card('toc', ['design']),
      card('design', [], 'Design body'),
      card('project_info', [], 'Project overview'),
    ])
    mounted = mount(<KnowledgeModeView />)
    await act(async () => {})

    // Expand the project to reveal the tree, then click the project_info node.
    const { container } = mounted
    act(() => { fireEvent.click(container.querySelector('.kb-outline-row--project .kb-outline-caret')!) })
    await act(async () => {})

    const label = Array.from(container.querySelectorAll('.kb-outline-row--card .kb-outline-label'))
      .find(el => el.textContent?.includes('project_info'))!
    act(() => { fireEvent.click(label) })
    await act(async () => {})

    // project_info must be visible in the right column — mounted under toc via
    // the well-known rule even though toc.list omits it — and expanded.
    expect(stackIds(container)).toEqual(['design', 'project_info'])
    expect(isCollapsed(container, 'project_info')).toBe(false)
    expect(container.querySelector('[data-card-id="project_info"]')?.classList.contains('kb-swimlane-card--focused')).toBe(true)
    expect(uiState.saveKbNotesUIState).toHaveBeenCalledWith({ lane: { projectId: 'p1', projectName: 'Alpha', cardId: 'project_info' } })
  })

  it('creating a card from the outline menu mounts it under toc, refreshes and focuses it', async () => {
    kbData.loadProjectToc
      .mockResolvedValueOnce([tocNode('toc', [])])
      .mockResolvedValue([tocNode('toc', [tocNode('design'), tocNode('fresh-note')])])
    kbData.loadProjectLaneCards
      .mockResolvedValueOnce([card('toc', []), card('design', [], 'Design body')])
      .mockResolvedValue([card('toc', ['design']), card('design', [], 'Design body'), card('fresh-note', [], '')])
    mounted = mount(<KnowledgeModeView />)
    await act(async () => {})
    const { container } = mounted

    // Open the project ⋯ menu and click 新建卡片.
    act(() => { fireEvent.click(container.querySelector('.kb-outline-row--project .kb-outline-more')!) })
    const menuItem = Array.from(container.querySelectorAll('.kb-outline-menu-item'))
      .find(el => el.textContent?.includes('knowledgeOutline.menu.newCard'))!
    act(() => { fireEvent.click(menuItem) })
    expect(container.querySelector('.kb-outline-newcard-input')).toBeTruthy()

    // Type a name and submit with Enter.
    const input = container.querySelector('.kb-outline-newcard-input') as HTMLInputElement
    act(() => { fireEvent.change(input, { target: { value: 'Fresh note' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })
    await act(async () => {})

    expect(kbData.createKbCard).toHaveBeenCalledWith('p1', 'Fresh note', ['toc', 'design'])
    // Both caches refreshed (the lane list was also pre-loaded for slug
    // de-duplication) and the new card focused in the stack.
    expect(kbData.loadProjectToc).toHaveBeenCalledTimes(1)
    expect(kbData.loadProjectLaneCards).toHaveBeenCalledTimes(2)
    expect(stackIds(container)).toContain('fresh-note')
    expect(isCollapsed(container, 'fresh-note')).toBe(false)
  })
})
