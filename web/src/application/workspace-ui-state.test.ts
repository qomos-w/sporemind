import { describe, expect, it, vi, beforeEach } from 'vitest'
import {
  getAIShellLayoutState,
  saveAIShellLayoutState,
  getMultiConsoleGridConfig,
  getCategoryExpansion,
  saveCategoryExpansion,
  toggleCategoryExpansion,
  getExpandedProjectIds,
  toggleExpandedProjectId,
  getKbNotesUIState,
  saveKbNotesUIState,
  defaultKbNotesUIState,
} from './workspace-ui-state'
import { mergeOrder } from '../ui/ai/components/SidebarLauncher'
import type { WorkspaceUIModel } from '../gen-types/workspace'

const emptyModel: WorkspaceUIModel = {
  WorkspaceId: 'default',
  Version: 1,
  SchemaVersion: 1,
  Layout: { Version: 1, LayoutJson: '', ActiveViewId: '' },
  Panels: { Version: 2, Panels: {}, ZoneTabOrder: {} },
  Dock: { LeftWidth: 260, RightWidth: 260, BottomHeight: 260, LeftTopPct: 0.5, RightTopPct: 0.5, BottomLeftPct: 0.5, WindowW: 1400, WindowH: 900 },
  AiShell: { SidebarOrder: [], SidebarPinned: [], LayoutJson: '' },
  ProjectCardBrowser: { SortMode: 'name-asc' },
  Explorer: { ActiveProjectId: '', SelectedPath: '', ExpandedPaths: [] },
}

let model = { ...emptyModel }

vi.mock('./workspace-ui-client', () => ({
  loadWorkspaceUIModel: vi.fn(async () => model),
  saveWorkspaceUIModel: vi.fn(async (_kind: string, buildPayload: (m: WorkspaceUIModel) => unknown) => {
    const payload = buildPayload(model) as { AiShell?: typeof model.AiShell; Explorer?: typeof model.Explorer }
    if (payload.AiShell) model = { ...model, AiShell: payload.AiShell }
    if (payload.Explorer) model = { ...model, Explorer: payload.Explorer }
    return model
  }),
}))

describe('workspace-ui-state AI shell layout', () => {
  beforeEach(() => {
    model = { ...emptyModel }
  })

  it('round-trips sidebar open state', async () => {
    await saveAIShellLayoutState({ sidebarVisible: true })
    const restored = await getAIShellLayoutState()
    expect(restored).not.toBeNull()
    expect(restored!.sidebarVisible).toBe(true)
  })

  it('round-trips launcherMode', async () => {
    await saveAIShellLayoutState({ launcherMode: 'app' })
    const restored = await getAIShellLayoutState()
    expect(restored!.launcherMode).toBe('app')
    await saveAIShellLayoutState({ launcherMode: 'code' })
    expect((await getAIShellLayoutState())!.launcherMode).toBe('code')
  })

  it('round-trips launcherView', async () => {
    await saveAIShellLayoutState({ launcherView: 'list' })
    expect((await getAIShellLayoutState())!.launcherView).toBe('list')
    await saveAIShellLayoutState({ launcherView: 'grid' })
    expect((await getAIShellLayoutState())!.launcherView).toBe('grid')
  })

  it('round-trips launcher favorites/recent/order lists', async () => {
    await saveAIShellLayoutState({
      launcherFavorites: ['app-a:main', 'review'],
      launcherRecent: ['app-b:view', 'app-a:main'],
      launcherOrder: ['glass', 'app-b:view', 'review'],
    })
    const restored = await getAIShellLayoutState()
    expect(restored?.launcherFavorites).toEqual(['app-a:main', 'review'])
    expect(restored?.launcherRecent).toEqual(['app-b:view', 'app-a:main'])
    expect(restored?.launcherOrder).toEqual(['glass', 'app-b:view', 'review'])
    // each field persists independently (merged against the existing layout)
    await saveAIShellLayoutState({ launcherRecent: ['app-c:main'] })
    const after = await getAIShellLayoutState()
    expect(after?.launcherFavorites).toEqual(['app-a:main', 'review'])
    expect(after?.launcherRecent).toEqual(['app-c:main'])
    expect(after?.launcherOrder).toEqual(['glass', 'app-b:view', 'review'])
  })

  it('merges layout fields without overwriting others', async () => {
    await saveAIShellLayoutState({ sidebarVisible: true, sidebarWidth: 300 })
    await saveAIShellLayoutState({ rightPanelOpen: true })
    const restored = await getAIShellLayoutState()
    expect(restored!.sidebarVisible).toBe(true)
    expect(restored!.sidebarWidth).toBe(300)
    expect(restored!.rightPanelOpen).toBe(true)
  })

  it('round-trips multiconsole grid state', async () => {
    await saveAIShellLayoutState({ multiconsoleGrid: { cols: 4, rows: 3 } })
    await expect(getMultiConsoleGridConfig()).resolves.toEqual({ cols: 4, rows: 3 })
  })

  it('falls back for invalid multiconsole grid state', async () => {
    model = { ...emptyModel, AiShell: { ...emptyModel.AiShell, LayoutJson: JSON.stringify({ multiconsoleGrid: { cols: 0, rows: 9 } }) } }
    await expect(getMultiConsoleGridConfig()).resolves.toEqual({ cols: 2, rows: 2 })
  })

  it('round-trips recent omnibox commands', async () => {
    await saveAIShellLayoutState({ recentOmniboxCommands: ['mode:files', 'agent:abc123'] })
    const restored = await getAIShellLayoutState()
    expect(restored?.recentOmniboxCommands).toEqual(['mode:files', 'agent:abc123'])
  })

  it('round-trips rightSshTabs (tabId + hostId + hostName, no sessionId)', async () => {
    await saveAIShellLayoutState({
      rightSshTabs: [
        { id: 'ssh-host-1', hostId: 'host-1', hostName: 'Host One' },
        { id: 'ssh-host-2', hostId: 'host-2', hostName: 'Host Two' },
      ],
    })
    const restored = await getAIShellLayoutState()
    expect(restored?.rightSshTabs).toEqual([
      { id: 'ssh-host-1', hostId: 'host-1', hostName: 'Host One' },
      { id: 'ssh-host-2', hostId: 'host-2', hostName: 'Host Two' },
    ])
  })

  it('clears rightSshTabs when saved with undefined', async () => {
    await saveAIShellLayoutState({ rightSshTabs: [{ id: 'ssh-host-1', hostId: 'host-1', hostName: 'Host One' }] })
    await saveAIShellLayoutState({ rightSshTabs: undefined })
    const restored = await getAIShellLayoutState()
    expect(restored?.rightSshTabs).toBeUndefined()
  })

  it('falls back when category layout JSON is malformed', async () => {
    model = { ...emptyModel, AiShell: { ...emptyModel.AiShell, LayoutJson: '{not-json' } }
    await expect(getCategoryExpansion()).resolves.toEqual({})
  })

  it('round-trips category expansion and filters invalid values', async () => {
    await saveCategoryExpansion({ pinned: true, projects: false })
    await expect(getCategoryExpansion()).resolves.toEqual({ pinned: true, projects: false })
    model = { ...model, AiShell: { ...model.AiShell, LayoutJson: JSON.stringify({ categoryExpansion: { pinned: true, invalid: 'yes' } }) } }
    await expect(getCategoryExpansion()).resolves.toEqual({ pinned: true })
  })

  it('serializes category toggles against the latest layout state', async () => {
    await toggleCategoryExpansion('pinned', true)
    await toggleCategoryExpansion('projects', false)
    await toggleCategoryExpansion('pinned', true)
    await expect(getCategoryExpansion()).resolves.toEqual({ pinned: true, projects: true })
  })

  it('round-trips expanded project ids and falls back for invalid values', async () => {
    await saveAIShellLayoutState({ expandedProjectIds: ['project-a', 'project-b'] })
    await expect(getExpandedProjectIds()).resolves.toEqual(new Set(['project-a', 'project-b']))
    model = { ...model, AiShell: { ...model.AiShell, LayoutJson: JSON.stringify({ expandedProjectIds: ['', 'project-a', 42 as unknown as string, null as unknown as string] }) } }
    await expect(getExpandedProjectIds()).resolves.toEqual(new Set(['project-a']))
  })

  it('ignores empty project ids when toggling expansion', async () => {
    await toggleExpandedProjectId('')
    await expect(getExpandedProjectIds()).resolves.toEqual(new Set())
  })

  it('serializes project toggles against the latest layout state', async () => {
    await toggleExpandedProjectId('project-a')
    await toggleExpandedProjectId('project-b')
    await toggleExpandedProjectId('project-a')
    await expect(getExpandedProjectIds()).resolves.toEqual(new Set(['project-b']))
  })
})

describe('workspace-ui-state knowledge-base (notes mode)', () => {
  beforeEach(() => {
    model = { ...emptyModel }
  })

  it('defaults to the home view with no lane when nothing is persisted', async () => {
    await expect(getKbNotesUIState()).resolves.toEqual(defaultKbNotesUIState)
  })

  it('round-trips the quick view, lane and expanded project ids', async () => {
    await saveKbNotesUIState({ view: 'starred' })
    await saveKbNotesUIState({ lane: { projectId: 'p1', projectName: 'Alpha', cardId: 'design' } })
    await saveKbNotesUIState({ expandedProjectIds: ['p1', 'p2'] })
    await expect(getKbNotesUIState()).resolves.toEqual({
      view: 'starred',
      lane: { projectId: 'p1', projectName: 'Alpha', cardId: 'design' },
      expandedProjectIds: ['p1', 'p2'],
    })
  })

  it('persists a null lane (quick view shown instead of a swimlane)', async () => {
    await saveKbNotesUIState({ lane: { projectId: 'p1', projectName: 'Alpha', cardId: 'design' } })
    await saveKbNotesUIState({ lane: null })
    await expect(getKbNotesUIState()).resolves.toEqual(defaultKbNotesUIState)
  })

  it('keeps the lane unbound to the active project: a project-only lane drops cardId', async () => {
    await saveKbNotesUIState({ lane: { projectId: 'p2', projectName: 'Beta' } })
    const state = await getKbNotesUIState()
    expect(state.lane).toEqual({ projectId: 'p2', projectName: 'Beta' })
  })

  it('falls back for an unknown view / malformed lane / invalid expanded ids', async () => {
    model = {
      ...emptyModel,
      AiShell: {
        ...emptyModel.AiShell,
        LayoutJson: JSON.stringify({
          kbView: 'nope',
          kbLane: { projectId: '', projectName: 'x' },
          kbExpandedProjectIds: ['p1', '', 42, null],
        }),
      },
    }
    await expect(getKbNotesUIState()).resolves.toEqual({
      view: 'home',
      lane: null,
      expandedProjectIds: ['p1'],
    })
  })
})

describe('mergeOrder (launcher all-section custom order)', () => {
  const defaults = ['a0', 'a1', 'a2', 'a3']

  it('returns the default order when no custom order is persisted', () => {
    expect(mergeOrder(undefined, defaults)).toEqual(defaults)
    expect(mergeOrder([], defaults)).toEqual(defaults)
  })

  it('keeps stored ids in custom order first, appends unlisted ids in default order', () => {
    expect(mergeOrder(['a3', 'a1'], defaults)).toEqual(['a3', 'a1', 'a0', 'a2'])
  })

  it('drops stale ids that no longer exist', () => {
    expect(mergeOrder(['gone', 'a2'], defaults)).toEqual(['a2', 'a0', 'a1', 'a3'])
    expect(mergeOrder(['gone-a', 'gone-b'], defaults)).toEqual(defaults)
  })
})
