import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ProjectNewChatPage } from './ProjectNewChatPage'
import type { AgentInfo } from '../hooks/agentInfoStore'
import type { ProviderGroup } from './AIComposer'
import type { AgentKindConfig } from '../../../gen-clients/system/types'
import { getComposerDraft, clearComposerDraft } from '../hooks/composerDraftStore'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  useViewportMode: vi.fn(() => 'desktop'),
  useBrowserOverlay: vi.fn(() => {}),
  voiceAPI: { stop: vi.fn(() => Promise.resolve()), setActive: vi.fn(() => Promise.resolve()) },
  dbgLog: vi.fn(),
  startScreenshot: vi.fn(),
  eventsOn: vi.fn(() => () => {}),
  slashCommands: [{ Name: 'clear', ShortHelp: 'Clear session' }],
  slashSkills: [{ Name: 'review', ShortHelp: 'Review current changes' }],
  builtinModes: [{ CardId: 'builtin:mode:goal', Name: 'goal', Title: 'Goal Mode', Icon: 'target' }],
  mentionResults: [] as unknown[],
  fileResults: [] as string[],
  agentMentionResults: [] as any[],
  createAgent: vi.fn(),
  mount: vi.fn(),
  fetchRouteGroups: vi.fn(async (): Promise<ProviderGroup[]> => []),
  listAgentKinds: vi.fn(async (): Promise<{ Items: { Kind: string }[] }> => ({ Items: [] })),
  listAgentKindConfigs: vi.fn(async (): Promise<{ Items: AgentKindConfig[] }> => ({ Items: [] })),
  aggregatorList: vi.fn(async () => ({ Items: [] })),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/useViewportMode', () => ({
  useViewportMode: hoisted.useViewportMode,
}))

vi.mock('../browserOverlay', () => ({
  useBrowserOverlay: hoisted.useBrowserOverlay,
}))

vi.mock('../hooks/useSlashCommands', () => ({
  useSlashCommands: () => ({ commands: hoisted.slashCommands, skills: hoisted.slashSkills, modes: hoisted.builtinModes, loading: false, error: null }),
}))

vi.mock('../hooks/useCardMention', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../hooks/useCardMention')>()
  return {
    ...actual,
    useCardMention: () => ({ results: hoisted.mentionResults, loading: false }),
    useFileMention: () => ({ results: hoisted.fileResults, loading: false }),
    useAgentMention: () => hoisted.agentMentionResults,
  }
})

vi.mock('../voice-api', () => ({
  voiceAPI: hoisted.voiceAPI,
  dbgLog: hoisted.dbgLog,
}))

vi.mock('../../../application/wails-bridge', () => ({
  startScreenshot: hoisted.startScreenshot,
}))

vi.mock('@wailsio/runtime', async (importOriginal) => {
  const runtime = await importOriginal<typeof import('@wailsio/runtime')>()
  return {
    ...runtime,
    Events: { ...runtime.Events, On: hoisted.eventsOn },
  }
})

vi.mock('../hooks/useAgentOperations', () => ({
  useAgentOperations: () => ({
    state: { creating: false, error: null },
    createAgent: hoisted.createAgent,
    importHistory: vi.fn(),
    clearError: vi.fn(),
  }),
}))

vi.mock('../hooks/useAgentComponentMounts', () => ({
  useAgentComponentMounts: () => ({ mount: hoisted.mount }),
}))

vi.mock('../hooks/useComposerHistory', () => ({
  COMPOSER_HISTORY_SCOPE: 'global',
  useComposerHistory: () => ({ history: [], setHistory: vi.fn() }),
}))

vi.mock('../hooks/useAIShellProviders', () => ({
  fetchRouteGroups: hoisted.fetchRouteGroups,
}))

vi.mock('../../../gen-clients/workspace/client', () => ({
  listAgentKinds: hoisted.listAgentKinds,
  listAgentKindConfigs: hoisted.listAgentKindConfigs,
}))

vi.mock('../../../gen-clients/aimanager/client', () => ({
  aggregatorList: hoisted.aggregatorList,
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

function makeCoordinator(): AgentInfo {
  return {
    Id: 'coord-1',
    ActorId: 'actor-1',
    DisplayName: 'Momo',
    Title: 'Coordinator',
    HasTitle: false,
    AgentKind: 'coordinator',
    ProjectId: '',
    ProjectName: '',
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
    CompactionPolicyLoading: false,
    CanDelete: true,
  }
}

describe('ProjectNewChatPage permission-mode anchor', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderPage = async (extra: Record<string, unknown> = {}) => {
    await act(async () => {
      root.render(
        <ProjectNewChatPage
          activeProject={null}
          projects={[]}
          onProjectChange={vi.fn()}
          agents={[]}
          onAgentClick={vi.fn()}
          onAgentCreated={vi.fn()}
          coordinator={makeCoordinator()}
          onCoordinatorSend={vi.fn()}
          {...extra}
        />,
      )
    })
  }

  it('renders the composer.permission-mode anchor in home mode when permission props are wired', async () => {
    await renderPage({
      permissionMode: 'allow-all',
      onPermissionModeChange: vi.fn(),
      globalPermissionMode: 'permission',
      onGlobalPermissionModeChange: vi.fn(),
    })
    expect(container.querySelector('[data-guide-id="composer.permission-mode"]')).not.toBeNull()
  })

  it('omits the permission selector when onPermissionModeChange is not provided', async () => {
    await renderPage()
    expect(container.querySelector('[data-guide-id="composer.permission-mode"]')).toBeNull()
  })
})

describe('ProjectNewChatPage quick actions', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderPage = async (extra: Record<string, unknown> = {}) => {
    await act(async () => {
      root.render(
        <ProjectNewChatPage
          activeProject={null}
          projects={[]}
          onProjectChange={vi.fn()}
          agents={[]}
          onAgentClick={vi.fn()}
          onAgentCreated={vi.fn()}
          coordinator={makeCoordinator()}
          onCoordinatorSend={vi.fn()}
          onCreateProject={vi.fn()}
          onOpenProject={vi.fn()}
          {...extra}
        />,
      )
    })
  }

  const actionButtons = () => Array.from(container.querySelectorAll<HTMLButtonElement>('.project-new-chat-quick-action'))

  it('home mode shows create/open project cards, never workflow/goal/plugin cards', async () => {
    await renderPage()
    const buttons = actionButtons()
    expect(buttons.some(b => b.textContent?.includes('shell.newChat.quickAction.createProject'))).toBe(true)
    expect(buttons.some(b => b.textContent?.includes('shell.newChat.quickAction.openProject'))).toBe(true)
    expect(buttons.some(b => b.textContent?.includes('quickStart.workflow.label'))).toBe(false)
    expect(buttons.some(b => b.textContent?.includes('quickStart.goal.label'))).toBe(false)
    expect(buttons.some(b => b.textContent?.includes('quickStart.plugin.label'))).toBe(false)
  })

  it('project mode shows workflow/goal/plugin quick-start cards', async () => {
    await renderPage({
      coordinator: null,
      activeProject: { ProjectID: 'p1', Name: 'Project One', RootPath: '/p', IsOpen: true },
      onSelectHome: vi.fn(),
    })
    const buttons = actionButtons()
    expect(buttons.some(b => b.textContent?.includes('quickStart.workflow.label'))).toBe(true)
    expect(buttons.some(b => b.textContent?.includes('quickStart.goal.label'))).toBe(true)
    expect(buttons.some(b => b.textContent?.includes('quickStart.plugin.label'))).toBe(true)
  })

  it('no-project mode keeps create/open project cards without workflow/goal/plugin cards', async () => {
    await renderPage({ coordinator: null })
    const buttons = actionButtons()
    expect(buttons.some(b => b.textContent?.includes('shell.newChat.quickAction.createProject'))).toBe(true)
    expect(buttons.some(b => b.textContent?.includes('shell.newChat.quickAction.openProject'))).toBe(true)
    expect(buttons.some(b => b.textContent?.includes('quickStart.workflow.label'))).toBe(false)
    expect(buttons.some(b => b.textContent?.includes('quickStart.goal.label'))).toBe(false)
    expect(buttons.some(b => b.textContent?.includes('quickStart.plugin.label'))).toBe(false)
  })

  it('renders the quick actions above the composer', async () => {
    await renderPage({
      activeProject: { ProjectID: 'p1', Name: 'Project One', RootPath: '/p', IsOpen: true },
    })
    const actions = container.querySelector('.project-new-chat-quick-actions')
    const composer = container.querySelector('.project-new-chat-composer')
    expect(actions).not.toBeNull()
    expect(composer).not.toBeNull()
    expect((actions as Element).compareDocumentPosition(composer as Node) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })
})

describe('ProjectNewChatPage quick action interactions', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderPage = async (extra: Record<string, unknown> = {}) => {
    await act(async () => {
      root.render(
        <ProjectNewChatPage
          activeProject={null}
          projects={[]}
          onProjectChange={vi.fn()}
          agents={[]}
          onAgentClick={vi.fn()}
          onAgentCreated={vi.fn()}
          coordinator={makeCoordinator()}
          onCoordinatorSend={vi.fn()}
          {...extra}
        />,
      )
    })
  }

  const findCard = (label: string) => {
    const btn = Array.from(container.querySelectorAll<HTMLButtonElement>('.project-new-chat-quick-action'))
      .find(b => b.textContent?.includes(label))
    if (!btn) throw new Error(`${label} card not found`)
    return btn
  }

  it('clicking create/open project cards invokes the callbacks', async () => {
    const onCreateProject = vi.fn()
    const onOpenProject = vi.fn()
    await renderPage({ onCreateProject, onOpenProject })
    await act(async () => {
      findCard('shell.newChat.quickAction.createProject').click()
    })
    expect(onCreateProject).toHaveBeenCalledTimes(1)
    await act(async () => {
      findCard('shell.newChat.quickAction.openProject').click()
    })
    expect(onOpenProject).toHaveBeenCalledTimes(1)
  })

  it('clicking workflow/goal/plugin quick-start cards prefills the page composer', async () => {
    await renderPage({
      coordinator: null,
      activeProject: { ProjectID: 'p1', Name: 'Project One', RootPath: '/p', IsOpen: true },
      onSelectHome: vi.fn(),
    })
    await act(async () => {
      findCard('quickStart.workflow.label').click()
    })
    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement | null
    expect(textarea?.value).toBe('quickStart.workflow.prompt')
    await act(async () => {
      findCard('quickStart.goal.label').click()
    })
    expect((container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement)?.value).toBe('quickStart.goal.prompt')
    await act(async () => {
      findCard('quickStart.plugin.label').click()
    })
    expect((container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement)?.value).toBe('quickStart.plugin.prompt')
  })

  it('quick-start card with a typed draft creates the agent and carries the draft over', async () => {
    hoisted.fetchRouteGroups.mockResolvedValueOnce([{
      routeId: 'system',
      label: 'Auto',
      isAuto: true,
      models: [{ id: 'm1', label: 'Model One', unit: { model: 'gpt-x', provider: 'p1' } }],
      items: [],
    }])
    hoisted.listAgentKinds.mockResolvedValueOnce({ Items: [{ Kind: 'coder' }] })
    hoisted.listAgentKindConfigs.mockResolvedValueOnce({ Items: [{ Kind: 'coder', UserCreatable: true, DisplayName: 'Coder', SystemManaged: false, RolePromptRef: { Kind: '', Key: '' } }] })
    hoisted.createAgent.mockResolvedValueOnce({ Id: 'g1', ActorId: 'a1' })
    const onAgentCreated = vi.fn()
    await renderPage({
      coordinator: null,
      activeProject: { ProjectID: 'p1', Name: 'Project One', RootPath: '/p', IsOpen: true },
      onSelectHome: vi.fn(),
      onAgentCreated,
    })
    // Flush the mount-time loads (agent kinds, route groups) before clicking.
    await act(async () => {})
    await act(async () => {})
    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    const nativeSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
    await act(async () => {
      nativeSetter.call(textarea, 'develop a plugin')
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      findCard('quickStart.goal.label').click()
    })
    expect(onAgentCreated).toHaveBeenCalledWith('g1', '', expect.anything(), 'builtin:mode:goal')
    expect(getComposerDraft('g1')).toBe('develop a plugin')
    clearComposerDraft('g1')
  })
})
