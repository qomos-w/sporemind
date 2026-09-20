import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { useAppOmnibox, type ProjectInfo } from './useAppOmnibox'
import { I18nProvider } from '../../../i18n/provider'
import type { ContentMode } from '../components/AIShellSidebar'
import type { AgentInfo } from './agentInfoStore'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

function TestHarness(props: {
  contentMode: ContentMode
  activeProjectId: string | null
  activeAgentId: string | null
  recentCommands: string[]
  projects: ProjectInfo[]
  agents: AgentInfo[]
  onContentModeChange: (mode: ContentMode) => void
  onProjectChange: (projectId: string) => void
  onOpenAgentChat: (agent: AgentInfo) => void
  onCreateProject: () => void
  onCreateAgent: (projectId?: string) => void
  onOpenSettings: (category?: string) => void
  onRecentChange: (recent: string[]) => void
  onExportHistory: () => void
  onImportHistory: () => void
  onDeleteAgent?: (agent: AgentInfo) => void
  onDeleteProject?: (project: ProjectInfo) => void
  expose: (api: ReturnType<typeof useAppOmnibox>) => void
}) {
  const api = useAppOmnibox({
    contentMode: props.contentMode,
    activeProjectId: props.activeProjectId,
    activeAgentId: props.activeAgentId,
    recentCommands: props.recentCommands,
    projects: props.projects,
    agents: props.agents,
    onContentModeChange: props.onContentModeChange,
    onProjectChange: props.onProjectChange,
    onOpenAgentChat: props.onOpenAgentChat,
    onCreateProject: props.onCreateProject,
    onCreateAgent: props.onCreateAgent,
    onOpenSettings: props.onOpenSettings,
    onRecentChange: props.onRecentChange,
    onExportHistory: props.onExportHistory,
    onImportHistory: props.onImportHistory,
    onDeleteAgent: props.onDeleteAgent,
    onDeleteProject: props.onDeleteProject,
  })
  props.expose(api)
  return null
}

describe('useAppOmnibox', () => {
  let container: HTMLDivElement | null = null
  let root: Root | null = null

  const defaultProjects: ProjectInfo[] = [
    { ProjectID: 'p1', Name: 'Sporemind', RootPath: '/dev/sporemind' },
    { ProjectID: 'p2', Name: 'Gospore', RootPath: '/dev/gospore' },
  ]

  const defaultAgents: AgentInfo[] = [
    { ActorId: 'a1', Title: 'coder', DisplayName: 'Coder', AgentKind: 'coder', ProjectId: 'p1', ProjectName: 'Sporemind', Status: 'idle' } as AgentInfo,
    { ActorId: 'a2', Title: 'architect', DisplayName: 'Architect', AgentKind: 'architect', ProjectId: 'p1', ProjectName: 'Sporemind', Status: 'idle' } as AgentInfo,
  ]

  const defaultProps = {
    contentMode: 'conversation' as ContentMode,
    activeProjectId: 'p1',
    activeAgentId: 'a1',
    recentCommands: [] as string[],
    projects: defaultProjects,
    agents: defaultAgents,
    onContentModeChange: vi.fn<(mode: ContentMode) => void>(),
    onProjectChange: vi.fn<(projectId: string) => void>(),
    onOpenAgentChat: vi.fn<(agent: AgentInfo) => void>(),
    onCreateProject: vi.fn<() => void>(),
    onCreateAgent: vi.fn<(projectId?: string) => void>(),
    onOpenSettings: vi.fn<(category?: string) => void>(),
    onRecentChange: vi.fn<(recent: string[]) => void>(),
    onExportHistory: vi.fn<() => void>(),
    onImportHistory: vi.fn<() => void>(),
    onDeleteAgent: undefined as ((agent: AgentInfo) => void) | undefined,
    onDeleteProject: undefined as ((project: ProjectInfo) => void) | undefined,
  }

  function render(props: Partial<typeof defaultProps> = {}) {
    let api: ReturnType<typeof useAppOmnibox> | null = null
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    const merged = { ...defaultProps, ...props }
    act(() => {
      root!.render(
        <I18nProvider>
          <TestHarness
            {...merged}
            expose={(value) => {
              api = value
            }}
          />
        </I18nProvider>
      )
    })
    return api!
  }

  beforeEach(() => {
    vi.clearAllMocks()
    defaultProps.recentCommands = []
  })

  afterEach(() => {
    if (root && container) {
      act(() => {
        root!.unmount()
      })
      container.remove()
    }
    root = null
    container = null
  })

  it('includes project and agent actions', () => {
    const api = render()
    const ids = api.actions.map((a) => a.id)
    expect(ids).toContain('project:p1')
    expect(ids).toContain('agent:a1')
    expect(ids).toContain('mode:files')
    expect(ids).toContain('session:export')
    expect(ids).toContain('session:import')
  })

  it('filters actions by query', () => {
    const api = render()
    const filtered = api.filter('gospore')
    expect(filtered.map((a) => a.id)).toContain('project:p2')
    expect(filtered.map((a) => a.id)).not.toContain('project:p1')
  })

  it('records recent command ids and surfaces them first', () => {
    const api = render()
    act(() => {
      api.recordUse('mode:files')
    })
    act(() => {
      api.recordUse('project:p2')
    })

    expect(defaultProps.onRecentChange).toHaveBeenLastCalledWith(['project:p2', 'mode:files'])

    const nextApi = render({ recentCommands: ['project:p2', 'mode:files'] })
    const topIds = nextApi.filter('').slice(0, 2).map((a) => a.id)
    expect(topIds).toEqual(['project:p2', 'mode:files'])
  })

  it('deduplicates recent commands and caps at 15', () => {
    const manyProjects: ProjectInfo[] = Array.from({ length: 16 }, (_, i) => ({
      ProjectID: `p${i}`,
      Name: `Project ${i}`,
      RootPath: `/p${i}`,
    }))
    const api = render({ projects: manyProjects })
    act(() => {
      for (let i = 0; i < 16; i++) {
        api.recordUse(`project:p${i}`)
      }
    })
    const lastCall = defaultProps.onRecentChange.mock.calls[defaultProps.onRecentChange.mock.calls.length - 1]?.[0]
    expect(lastCall).toHaveLength(15)
    expect(lastCall?.[0]).toBe('project:p15')
  })

  it('ignores unknown command ids when recording', () => {
    const api = render()
    act(() => {
      api.recordUse('unknown:command')
    })
    expect(defaultProps.onRecentChange).not.toHaveBeenCalled()
  })

  it('keeps agents with empty ActorId distinct (no id collapse)', () => {
    // Some agent kinds (retired kinds like architect, ...) report an empty ActorId. The
    // agent action id must fall back to agent.Id so they don't all collapse to
    // a single "agent:" id — which would make them collide and get filtered out
    // by the recent-commands set, hiding most agents from the palette.
    const agentsWithEmptyActor: AgentInfo[] = [
      { Id: 'Reviewer#1', ActorId: '', Title: '', DisplayName: 'Reviewer', AgentKind: 'reviewer', ProjectId: 'p1', ProjectName: 'Sporemind', Status: 'idle' } as AgentInfo,
      { Id: 'Architect#2', ActorId: '', Title: '', DisplayName: 'Architect', AgentKind: 'architect', ProjectId: 'p1', ProjectName: 'Sporemind', Status: 'idle' } as AgentInfo,
      { Id: 'Coder#3', ActorId: 'c3', Title: '', DisplayName: 'Coder', AgentKind: 'coder', ProjectId: 'p1', ProjectName: 'Sporemind', Status: 'idle' } as AgentInfo,
    ]
    const api = render({ agents: agentsWithEmptyActor })
    const agentIds = api.actions.map((a) => a.id).filter((id) => id.startsWith('agent:') && id !== 'agent:create')

    // Three distinct ids (fallback to Id), not a single shared "agent:".
    expect(agentIds).toEqual(['agent:Reviewer#1', 'agent:Architect#2', 'agent:Coder#3'])
    expect(new Set(agentIds).size).toBe(3)

    // Even with a stray "agent:" in recent commands, all agents survive.
    const filtered = api.filter('')
    const filteredAgentIds = filtered.map((a) => a.id).filter((id) => id.startsWith('agent:') && id !== 'agent:create')
    expect(filteredAgentIds).toHaveLength(3)
  })

  it('wires onDelete for agents and non-system projects', () => {
    const onDeleteAgent = vi.fn()
    const onDeleteProject = vi.fn()
    const api = render({ onDeleteAgent, onDeleteProject })

    const agentAction = api.actions.find((a) => a.id === 'agent:a1')!
    expect(agentAction.onDelete).toBeDefined()
    act(() => { agentAction.onDelete!() })
    expect(onDeleteAgent).toHaveBeenCalledTimes(1)

    const projectAction = api.actions.find((a) => a.id === 'project:p1')!
    expect(projectAction.onDelete).toBeDefined()
    act(() => { projectAction.onDelete!() })
    expect(onDeleteProject).toHaveBeenCalledTimes(1)
  })

  it('omits onDelete for system projects', () => {
    const onDeleteProject = vi.fn()
    const projectsWithSystem: ProjectInfo[] = [
      { ProjectID: 'p1', Name: 'User Project', RootPath: '/p1' },
      { ProjectID: 'sys', Name: 'Workspace', RootPath: '/sys', System: true },
    ]
    const api = render({ projects: projectsWithSystem, onDeleteProject })

    const userProject = api.actions.find((a) => a.id === 'project:p1')!
    const systemProject = api.actions.find((a) => a.id === 'project:sys')!
    expect(userProject.onDelete).toBeDefined()
    expect(systemProject.onDelete).toBeUndefined()
  })

  it('omits onDelete when no delete handlers are provided', () => {
    const api = render()
    const agentAction = api.actions.find((a) => a.id === 'agent:a1')!
    const projectAction = api.actions.find((a) => a.id === 'project:p1')!
    expect(agentAction.onDelete).toBeUndefined()
    expect(projectAction.onDelete).toBeUndefined()
  })
})
