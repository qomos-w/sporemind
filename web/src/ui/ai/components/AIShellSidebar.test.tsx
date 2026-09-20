import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AIShellSidebar } from './AIShellSidebar'
import { I18nProvider } from '../../../i18n/provider'
import type { AgentInfo } from '../hooks/agentInfoStore'
import type { ProjectSnapshot } from '../../../domain/types'
import type { ThemeState } from '@qomos/sporemind-theme'
import { SHELL_CONTENT_MODE_EVENT } from '../hooks/worktreeGitJump'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// ── Mocks ────────────────────────────────────────────────────────────────────

const hoisted = vi.hoisted(() => ({
  getExpandedProjectIds: vi.fn(async () => new Set<string>()),
  toggleExpandedProjectId: vi.fn(async () => {}),
  getCategoryExpansion: vi.fn(async () => ({})),
  toggleCategoryExpansion: vi.fn(async () => {}),
  wikiListCards: vi.fn(async () => ({ Cards: [] })),
  list: vi.fn(async () => ''),
  monoStore: {
    subscribe: vi.fn(() => () => {}),
    getState: vi.fn(() => ({ projectId: null, cards: [] })),
    setProjectId: vi.fn(),
    load: vi.fn(),
    loadOpenCards: vi.fn(),
  },
  emptyMonoState: { projectId: null, cards: [] },
  persistLocale: vi.fn(async () => {}),
}))

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/project/client', () => ({
  wikiListCards: hoisted.wikiListCards,
  list: hoisted.list,
}))
vi.mock('../../../application/workspace-ui-state', () => ({
  getExpandedProjectIds: hoisted.getExpandedProjectIds,
  toggleExpandedProjectId: hoisted.toggleExpandedProjectId,
  getCategoryExpansion: hoisted.getCategoryExpansion,
  toggleCategoryExpansion: hoisted.toggleCategoryExpansion,
}))
vi.mock('../../../application/locale-persist', () => ({
  persistLocale: hoisted.persistLocale,
}))

// ── buildConfig mock ─────────────────────────────────────────────────────────
// Vitest defines mirror vite: __BUILD_TYPE__ defaults to 'release' and
// __WAILS_PRODUCTION__ to false (the `make build-desktop` scenario).
const buildCfg = vi.hoisted(() => ({
  buildType: 'release' as 'release' | 'beta' | 'dev',
  wailsProduction: false,
}))
vi.mock('../../../config/buildConfig', () => ({
  get buildType() { return buildCfg.buildType },
  get wailsProduction() { return buildCfg.wailsProduction },
  buildFlavor: 'default',
  buildVersion: 'test',
}))
vi.mock('../../panels/mono-store', () => ({ monoStore: hoisted.monoStore }))
vi.mock('../hooks/useMonoStore', () => ({
  useMonoStore: () => hoisted.emptyMonoState,
}))

// ── git-store mock (worktree badge jump) ────────────────────────────────────
const gitMock = vi.hoisted(() => {
  const gitState = {
    projectId: '',
    worktreeId: null as string | null,
    commits: [{ Hash: 'head-1', Short: 'head1', Message: 'head commit' }],
    loading: false,
  }
  const gitListeners = new Set<() => void>()
  function gitEmit() {
    for (const fn of gitListeners) fn()
  }
  const gitSetContext = vi.fn()
  const gitSelectCommit = vi.fn(async () => [])
  return { gitState, gitListeners, gitEmit, gitSetContext, gitSelectCommit }
})

vi.mock('../../panels/git-store', () => ({
  gitStore: {
    get state() { return gitMock.gitState },
    subscribe(fn: () => void) {
      gitMock.gitListeners.add(fn)
      return () => gitMock.gitListeners.delete(fn)
    },
    setContext: gitMock.gitSetContext,
    selectCommit: gitMock.gitSelectCommit,
  },
}))

// ── Helpers ──────────────────────────────────────────────────────────────────

const theme: ThemeState = { mode: 'dark', fontSize: 14 }
const project: ProjectSnapshot = {
  ProjectID: 'p1',
  Name: 'Demo',
  RootPath: '/demo',
  IsOpen: true,
}

function makeAgent(over: Partial<AgentInfo>): AgentInfo {
  return {
    Id: 'a',
    ActorId: 'act',
    DisplayName: 'Agent',
    Title: 'Agent',
    HasTitle: true,
    AgentKind: 'coder',
    ProjectId: 'p1',
    ProjectName: 'Demo',
    Status: 'idle',
    StatusLabel: 'idle',
    IsWorking: false,
    IsError: false,
    IsCompleted: false,
    IsAskUserPermission: false,
    IsAskUser: false,
    IsAskPermission: false,
    IsPlanApproval: false,
    IsGoalSubmit: false,
    Degraded: false,
    CompactionPolicyLoading: false,
    CanDelete: true,
    ...over,
  } as AgentInfo
}

// ── Test scaffold ────────────────────────────────────────────────────────────

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  hoisted.getExpandedProjectIds.mockResolvedValue(new Set())
  hoisted.getCategoryExpansion.mockResolvedValue({})
  hoisted.wikiListCards.mockResolvedValue({ Cards: [] })
})

afterEach(() => {
  act(() => root?.unmount())
  container.remove()
  vi.restoreAllMocks()
})

async function renderSidebar(projectAgents: AgentInfo[], overrides: Partial<React.ComponentProps<typeof AIShellSidebar>> = {}) {
  await act(async () => {
    root = createRoot(container)
    root.render(
      <I18nProvider initialLocale="en-US">
        <AIShellSidebar
          projects={[project]}
          systemTreeNodes={[]}
          activeProject={project}
          projectAgents={projectAgents}
          activeConversationId={null}
          onOpenCreateProject={() => {}}
          onSelectProject={() => {}}
          onReorderProjects={() => {}}
          onReorderAgents={() => {}}
          onAgentClick={() => {}}
          globalAgents={[]}
          isMobile={false}
          account={null}
          theme={theme}
          onThemeChange={() => {}}
          contentMode="conversation"
          onContentModeChange={() => {}}
          {...overrides}
        />
      </I18nProvider>,
    )
  })
  // Flush async effects: getExpandedProjectIds, getCategoryExpansion,
  // wikiListCards, and their setState calls.
  await act(async () => {})
  await act(async () => {})
}

function sessionRows(): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>('.ai-sidebar-session-item'))
}

function rowByLabel(label: string): HTMLElement | undefined {
  return sessionRows().find(row =>
    row.querySelector('.ai-sidebar-session-title')?.textContent === label,
  )
}

// ── Tests ────────────────────────────────────────────────────────────────────

describe('AIShellSidebar worktree icon', () => {
  it('renders a GitBranch icon on the agent row when WorktreeID is present', async () => {
    hoisted.getExpandedProjectIds.mockResolvedValue(new Set(['p1']))
    await renderSidebar([
      makeAgent({
        Id: 'a-wt',
        ActorId: 'act-wt',
        DisplayName: 'Worktree Agent',
        Title: 'Worktree Agent',
        WorktreeID: 'wt-123',
        WorktreeName: 'my-branch',
      }),
    ])

    const row = rowByLabel('Worktree Agent')
    expect(row).toBeTruthy()

    const icon = row!.querySelector('[aria-label="worktree isolated"]')
    expect(icon).toBeTruthy()
    expect(icon!.getAttribute('title')).toBe('my-branch')
    // The icon is a GitBranch svg drawn by lucide-react
    expect(icon!.querySelector('svg')).toBeTruthy()
  })

  it('does not render the worktree icon when the agent has no WorktreeID', async () => {
    hoisted.getExpandedProjectIds.mockResolvedValue(new Set(['p1']))
    await renderSidebar([
      makeAgent({
        Id: 'a-plain',
        ActorId: 'act-plain',
        DisplayName: 'Plain Agent',
        Title: 'Plain Agent',
      }),
    ])

    const row = rowByLabel('Plain Agent')
    expect(row).toBeTruthy()
    expect(row!.querySelector('[aria-label="worktree isolated"]')).toBeNull()
  })

  it('renders the icon only for the agent that has a worktree in a mixed list', async () => {
    hoisted.getExpandedProjectIds.mockResolvedValue(new Set(['p1']))
    await renderSidebar([
      makeAgent({
        Id: 'a-wt',
        ActorId: 'act-wt',
        DisplayName: 'Worktree Agent',
        Title: 'Worktree Agent',
        WorktreeID: 'wt-456',
      }),
      makeAgent({
        Id: 'a-plain',
        ActorId: 'act-plain',
        DisplayName: 'Plain Agent',
        Title: 'Plain Agent',
      }),
    ])

    const rows = sessionRows()
    expect(rows.length).toBe(2)

    const wtRow = rowByLabel('Worktree Agent')
    expect(wtRow?.querySelector('[aria-label="worktree isolated"]')).toBeTruthy()

    const plainRow = rowByLabel('Plain Agent')
    expect(plainRow?.querySelector('[aria-label="worktree isolated"]')).toBeNull()
  })

  it('places the worktree icon inside the session-meta (right side of the row)', async () => {
    hoisted.getExpandedProjectIds.mockResolvedValue(new Set(['p1']))
    await renderSidebar([
      makeAgent({
        Id: 'a-wt',
        ActorId: 'act-wt',
        DisplayName: 'Meta Agent',
        Title: 'Meta Agent',
        WorktreeID: 'wt-789',
      }),
    ])

    const row = rowByLabel('Meta Agent')
    expect(row).toBeTruthy()

    const icon = row!.querySelector('.ai-sidebar-session-meta [aria-label="worktree isolated"]')
    expect(icon).toBeTruthy()
  })

  it('shows the worktree ID as fallback title when WorktreeName is empty', async () => {
    hoisted.getExpandedProjectIds.mockResolvedValue(new Set(['p1']))
    await renderSidebar([
      makeAgent({
        Id: 'a-wt',
        ActorId: 'act-wt',
        DisplayName: 'Fallback Agent',
        Title: 'Fallback Agent',
        WorktreeID: 'wt-no-name',
        WorktreeName: '',
      }),
    ])

    const icon = rowByLabel('Fallback Agent')?.querySelector('[aria-label="worktree isolated"]')
    expect(icon?.getAttribute('title')).toBe('wt-no-name')
  })
})

describe('AIShellSidebar worktree badge jump', () => {
  beforeEach(() => {
    gitMock.gitListeners.clear()
    gitMock.gitSetContext.mockReset()
    gitMock.gitSelectCommit.mockReset()
    gitMock.gitState.projectId = ''
    gitMock.gitState.worktreeId = null
    gitMock.gitState.commits = [{ Hash: 'head-1', Short: 'head1', Message: 'head commit' }]
    gitMock.gitState.loading = false
  })

  it('clicking the sidebar worktree badge jumps to git mode without selecting the agent', async () => {
    hoisted.getExpandedProjectIds.mockResolvedValue(new Set(['p1']))

    const dispatchSpy = vi.spyOn(window, 'dispatchEvent')
    const onAgentClick = vi.fn()
    gitMock.gitSetContext.mockImplementation(({ projectId, worktreeId }: { projectId: string; worktreeId: string }) => {
      gitMock.gitState.projectId = projectId
      gitMock.gitState.worktreeId = worktreeId
      gitMock.gitState.loading = true
      setTimeout(() => {
        gitMock.gitState.loading = false
        gitMock.gitEmit()
      }, 0)
    })

    await renderSidebar([
      makeAgent({
        Id: 'a-wt',
        ActorId: 'act-wt',
        DisplayName: 'Worktree Agent',
        Title: 'Worktree Agent',
        ProjectId: 'p1',
        WorktreeID: 'wt-123',
        WorktreeName: 'my-branch',
      }),
    ], { onAgentClick })

    const row = rowByLabel('Worktree Agent')
    expect(row).toBeTruthy()

    const badge = row!.querySelector('[aria-label="worktree isolated"]') as HTMLElement
    expect(badge).not.toBeNull()

    await act(async () => {
      badge.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })

    expect(dispatchSpy).toHaveBeenCalledWith(
      expect.objectContaining({ type: SHELL_CONTENT_MODE_EVENT }),
    )
    expect(gitMock.gitSetContext).toHaveBeenCalledWith({ projectId: 'p1', worktreeId: 'wt-123' })
    expect(gitMock.gitSelectCommit).toHaveBeenCalledWith(gitMock.gitState.commits[0])
    expect(onAgentClick).not.toHaveBeenCalled()
  })
})

describe('AIShellSidebar launcher-mode switch (mobile only)', () => {
  it('renders the switch at the top of the mode group on mobile, above the new-chat button', async () => {
    const onLauncherModeChange = vi.fn()
    await renderSidebar([], { launcherMode: 'code', onLauncherModeChange, onNewChat: () => {}, isMobile: true })

    const modeGroup = container.querySelector('.ai-sidebar-mode-group')!
    const modeSwitch = modeGroup.querySelector('.ai-sidebar-mode-switch')!
    const newChat = modeGroup.querySelector('.ai-sidebar-mode-new-chat')!
    expect(modeSwitch).toBeTruthy()
    expect(newChat).toBeTruthy()
    const groupChildren = Array.from(modeGroup.children)
    expect(groupChildren.indexOf(modeSwitch as Element)).toBeLessThan(groupChildren.indexOf(newChat as Element))
  })

  it('renders no switch on desktop — it parks in the shell topbar', async () => {
    await renderSidebar([], { launcherMode: 'code', onLauncherModeChange: vi.fn() })
    expect(container.querySelector('.ai-sidebar-mode-switch')).toBeNull()
  })

  it('switches launcherMode when the inactive segment is clicked', async () => {
    const onLauncherModeChange = vi.fn()
    await renderSidebar([], { launcherMode: 'code', onLauncherModeChange, isMobile: true })

    const segments = Array.from(container.querySelectorAll<HTMLButtonElement>('.ai-sidebar-mode-switch-segment'))
    const appSegment = segments.find(s => s.textContent?.includes('App'))!
    act(() => {
      appSegment.click()
    })
    expect(onLauncherModeChange).toHaveBeenCalledWith('app')
  })

  it('renders no switch when launcherMode props are absent', async () => {
    await renderSidebar([])
    expect(container.querySelector('.ai-sidebar-mode-switch')).toBeNull()
  })
})

// ── Plugin agent virtual directory ──────────────────────────────────────────

describe('AIShellSidebar plugin-agent virtual directory', () => {
  function pluginAgent(over: Partial<AgentInfo>): AgentInfo {
    return makeAgent({
      Id: 'pa',
      ActorId: 'act-pa',
      DisplayName: 'Plugin Agent',
      Title: 'Plugin Agent',
      AgentKind: 'plugin',
      ProjectId: '',
      ProjectName: '',
      BoundAppId: 'app.demo',
      BoundAppSlot: 'default',
      ...over,
    })
  }

  it('renders a collapsible per-app directory grouping the plugin agents', async () => {
    await renderSidebar([], {
      pluginAgentGroups: [
        { appId: 'app.demo', label: 'Demo Plugin', agents: [pluginAgent({})] },
      ],
    })

    const labels = Array.from(container.querySelectorAll('.ai-sidebar-category-label')).map(el => el.textContent)
    expect(labels).toContain('Demo Plugin')

    // Expanded: the bound agent renders inside the group.
    const toggles = container.querySelectorAll<HTMLButtonElement>('.ai-sidebar-category-toggle')
    const groupToggle = Array.from(toggles).find(el => el.textContent?.includes('Demo Plugin'))!
    await act(async () => { groupToggle.click() })
    expect(rowByLabel('Plugin Agent')).toBeTruthy()

    // The agent does not appear in the flat global section — it lives only
    // inside its app directory.
    expect(hoisted.getCategoryExpansion).toHaveBeenCalled()
  })

  it('renders no directory when the group has no agents', async () => {
    await renderSidebar([], {
      pluginAgentGroups: [{ appId: 'app.demo', label: 'Demo Plugin', agents: [] }],
    })
    const labels = Array.from(container.querySelectorAll('.ai-sidebar-category-label')).map(el => el.textContent)
    expect(labels).not.toContain('Demo Plugin')
  })

  it('renders no directory when pluginAgentGroups is absent (no plugin agents)', async () => {
    await renderSidebar([])
    const labels = Array.from(container.querySelectorAll('.ai-sidebar-category-label')).map(el => el.textContent)
    expect(labels).not.toContain('Demo Plugin')
  })
})

// ── Funnel dropdown: show-folders toggle + agent time filter ────────────────

describe('AIShellSidebar funnel dropdown filters', () => {
  async function openFilterMenu() {
    const btn = container.querySelector<HTMLButtonElement>('.ai-sidebar-sort-dropdown .ai-sidebar-toolbar-button')!
    await act(async () => { btn.click() })
  }

  async function clickDropdownItem(label: string) {
    const items = Array.from(container.querySelectorAll<HTMLButtonElement>('.ai-sidebar-sort-dropdown-item'))
    const item = items.find(el => el.textContent?.includes(label))
    expect(item).toBeTruthy()
    await act(async () => { item!.click() })
  }

  it('hides project folders and shows agents flat when Show Folders is unchecked', async () => {
    hoisted.getExpandedProjectIds.mockResolvedValue(new Set(['p1']))
    await renderSidebar([
      makeAgent({ Id: 'a-folder', ActorId: 'act-a', DisplayName: 'Folder Agent', Title: 'Folder Agent' }),
    ])

    // Folders visible by default.
    expect(container.querySelector('.ai-sidebar-project-item')).toBeTruthy()
    expect(rowByLabel('Folder Agent')).toBeTruthy()

    await openFilterMenu()
    const checkbox = Array.from(container.querySelectorAll<HTMLButtonElement>('.ai-sidebar-sort-dropdown-item'))
      .find(el => el.textContent?.includes('Show Folders'))!
    expect(checkbox.getAttribute('aria-checked')).toBe('true')
    await act(async () => { checkbox.click() })

    // Project directory row is gone, but the agent remains as a flat row.
    expect(container.querySelector('.ai-sidebar-project-item')).toBeNull()
    expect(rowByLabel('Folder Agent')).toBeTruthy()
  })

  it('marks the funnel button active while a filter is engaged', async () => {
    await renderSidebar([])
    const btn = container.querySelector<HTMLButtonElement>('.ai-sidebar-sort-dropdown .ai-sidebar-toolbar-button')!
    expect(btn.classList.contains('filter-active')).toBe(false)

    await openFilterMenu()
    await clickDropdownItem('Show Folders')
    expect(btn.classList.contains('filter-active')).toBe(true)
  })

  it('hides agents whose last activity is outside the selected time window', async () => {
    hoisted.getExpandedProjectIds.mockResolvedValue(new Set(['p1']))
    const recent = new Date(Date.now() - 60_000).toISOString()
    const stale = new Date(Date.now() - 48 * 60 * 60_000).toISOString()
    await renderSidebar([
      makeAgent({ Id: 'a-recent', ActorId: 'act-r', DisplayName: 'Recent Agent', Title: 'Recent Agent', LastTurnCompletedAt: recent }),
      makeAgent({ Id: 'a-stale', ActorId: 'act-s', DisplayName: 'Stale Agent', Title: 'Stale Agent', LastActivity: stale }),
    ])

    await openFilterMenu()
    await clickDropdownItem('Last 5 Minutes')

    expect(rowByLabel('Recent Agent')).toBeTruthy()
    expect(rowByLabel('Stale Agent')).toBeFalsy()
  })

  it('hides projects with no agents inside the time window', async () => {
    hoisted.getExpandedProjectIds.mockResolvedValue(new Set(['p1']))
    const stale = new Date(Date.now() - 48 * 60 * 60_000).toISOString()
    await renderSidebar([
      makeAgent({ Id: 'a-stale', ActorId: 'act-s', DisplayName: 'Stale Agent', Title: 'Stale Agent', LastActivity: stale }),
    ])

    expect(container.querySelector('.ai-sidebar-project-item')).toBeTruthy()

    await openFilterMenu()
    await clickDropdownItem('Last 24 Hours')

    expect(container.querySelector('.ai-sidebar-project-item')).toBeNull()
  })

  it('applies the time filter to global agents', async () => {
    const recent = new Date(Date.now() - 60_000).toISOString()
    const stale = new Date(Date.now() - 48 * 60 * 60_000).toISOString()
    await renderSidebar([], {
      globalAgents: [
        makeAgent({ Id: 'g-recent', ActorId: 'act-gr', DisplayName: 'Global Recent', Title: 'Global Recent', ProjectId: '', ProjectName: '', LastTurnCompletedAt: recent }),
        makeAgent({ Id: 'g-stale', ActorId: 'act-gs', DisplayName: 'Global Stale', Title: 'Global Stale', ProjectId: '', ProjectName: '', LastActivity: stale }),
      ],
    })

    await openFilterMenu()
    await clickDropdownItem('Last 5 Minutes')

    expect(rowByLabel('Global Recent')).toBeTruthy()
    expect(rowByLabel('Global Stale')).toBeFalsy()
  })

  it('shows every agent again when the filter is reset to All', async () => {
    hoisted.getExpandedProjectIds.mockResolvedValue(new Set(['p1']))
    const stale = new Date(Date.now() - 48 * 60 * 60_000).toISOString()
    await renderSidebar([
      makeAgent({ Id: 'a-stale', ActorId: 'act-s', DisplayName: 'Stale Agent', Title: 'Stale Agent', LastActivity: stale }),
    ])

    await openFilterMenu()
    await clickDropdownItem('Last 5 Minutes')
    expect(rowByLabel('Stale Agent')).toBeFalsy()

    await openFilterMenu()
    await clickDropdownItem('All')
    expect(rowByLabel('Stale Agent')).toBeTruthy()
  })
})