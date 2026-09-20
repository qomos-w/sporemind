import { useMemo, useCallback, useRef, useEffect } from 'react'
import { useI18n } from '../../../i18n'
import {
  Folder,
  FolderPlus,
  Bot,
  Plus,
  MessageSquare,
  Network,
  LayoutGrid,
  FileText,
  AlertTriangle,
  Terminal,
  Monitor,
  Settings,
  UserCog,
  Mic,
  Wrench,
  FileCode,
  Server,
  Layers,
  SlidersHorizontal,
  Puzzle,
  Database,
  Code,
  Download,
  Upload,
  ShieldCheck,
} from 'lucide-react'
import type { ContentMode } from '../components/AIShellSidebar'
import type { AgentInfo } from '../hooks/agentInfoStore'
import { categories } from '../components/settings-data'
import { avatarHue, agentAvatarLabel } from '../lib/agent-avatar'

export type AppOmniboxSection = 'command' | 'skill' | 'bundle' | 'componentMode' | 'recent' | 'project' | 'agent' | 'browser' | 'mode' | 'settings' | 'session' | 'card' | 'file'

export interface ProjectInfo {
  ProjectID: string
  Name: string
  RootPath?: string
  System?: boolean
}

export interface AppOmniboxAction {
  id: string
  section: AppOmniboxSection
  label: string
  description?: string
  icon?: React.ReactNode
  shortcut?: string
  keywords: string[]
  score: number
  run: () => void | Promise<void>
  /** Optional secondary delete/remove action. When present, the omnibox item
   * shows a delete button (hover on desktop, always on touch devices). */
  onDelete?: () => void
}

export interface UseAppOmniboxOptions {
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
}

const MAX_RECENT = 15

const MODES: Array<{ id: ContentMode; label: string; descKey: string; icon: React.ReactNode }> = [
  { id: 'conversation', label: 'Conversation', descKey: 'omnibox.desc.conversation', icon: <MessageSquare size={16} /> },
  { id: 'topology', label: 'Topology', descKey: 'omnibox.desc.topology', icon: <Network size={16} /> },
  { id: 'files', label: 'Files', descKey: 'omnibox.desc.files', icon: <FileText size={16} /> },
  { id: 'problems', label: 'Problems', descKey: 'omnibox.desc.problems', icon: <AlertTriangle size={16} /> },
  { id: 'approvals', label: 'Approvals', descKey: 'omnibox.desc.approvals', icon: <ShieldCheck size={16} /> },
  { id: 'notes', label: 'Notes', descKey: 'omnibox.desc.notes', icon: <LayoutGrid size={16} /> },
  { id: 'ssh', label: 'SSH', descKey: 'omnibox.desc.ssh', icon: <Terminal size={16} /> },
  { id: 'multiconsole', label: 'Multiconsole', descKey: 'omnibox.desc.multiconsole', icon: <Monitor size={16} /> },
]

const ICONS_BY_SETTINGS_ID: Record<string, React.ReactNode> = {
  general: <Settings size={16} />,
  account: <UserCog size={16} />,
  voice: <Mic size={16} />,
  'model-providers': <Server size={16} />,
  'model-aggregators': <Layers size={16} />,
  'model-defaults': <SlidersHorizontal size={16} />,
  agent: <Bot size={16} />,
  skills: <Wrench size={16} />,
  prompts: <FileCode size={16} />,
  frp: <Network size={16} />,
  mcp: <Server size={16} />,
  plugins: <Puzzle size={16} />,
  commands: <Terminal size={16} />,
  index: <Database size={16} />,
  developer: <Code size={16} />,
}

function normalize(text: string): string {
  return text
    .toLowerCase()
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/[^a-z0-9\u4e00-\u9fa5]+/g, ' ')
    .trim()
}

function scoreMatch(query: string, action: AppOmniboxAction): number {
  if (!query) return action.score
  const q = normalize(query)
  const haystack = normalize([action.label, action.description, ...action.keywords].filter(Boolean).join(' '))
  if (haystack === q) return 1000 + action.score
  if (haystack.startsWith(q)) return 500 + action.score
  if (haystack.includes(' ' + q)) return 300 + action.score
  if (haystack.includes(q)) return 100 + action.score
  return 0
}

export function useAppOmnibox(options: UseAppOmniboxOptions): {
  actions: AppOmniboxAction[]
  filter: (query: string) => AppOmniboxAction[]
  recordUse: (id: string) => void
} {
  const { t } = useI18n()
  const {
    contentMode,
    activeProjectId,
    activeAgentId,
    recentCommands,
    projects,
    agents,
    onContentModeChange,
    onProjectChange,
    onOpenAgentChat,
    onCreateProject,
    onCreateAgent,
    onOpenSettings,
    onRecentChange,
    onExportHistory,
    onImportHistory,
    onDeleteAgent,
    onDeleteProject,
  } = options

  const allActions = useMemo<AppOmniboxAction[]>(() => {
    const actions: AppOmniboxAction[] = []

    // Session history actions
    actions.push({
      id: 'session:export',
      section: 'session',
      label: t('omnibox.action.exportChat'),
      description: t('omnibox.action.exportChatDesc'),
      icon: <Download size={16} />,
      keywords: ['export', 'history', 'download', 'json', 'daochu', 'duihua'],
      score: 0,
      run: onExportHistory,
    })
    actions.push({
      id: 'session:import',
      section: 'session',
      label: t('omnibox.action.importChat'),
      description: t('omnibox.action.importChatDesc'),
      icon: <Upload size={16} />,
      keywords: ['import', 'history', 'upload', 'json', 'daoru', 'duihua'],
      score: 0,
      run: onImportHistory,
    })

    // Project actions
    actions.push({
      id: 'project:create',
      section: 'project',
      label: t('omnibox.action.newProject'),
      description: t('omnibox.action.newProjectDesc'),
      icon: <FolderPlus size={16} />,
      keywords: ['new', 'project', 'create', 'xinjian', 'xiangmu'],
      score: 0,
      run: onCreateProject,
    })

    for (const project of projects) {
      const isActive = project.ProjectID === activeProjectId
      actions.push({
        id: `project:${project.ProjectID}`,
        section: 'project',
        label: project.Name,
        description: project.RootPath || undefined,
        icon: <Folder size={16} />,
        shortcut: isActive ? t('omnibox.shortcut.current') : undefined,
        keywords: [project.Name, project.RootPath || '', project.ProjectID, 'project', 'xiangmu'],
        score: isActive ? 50 : 0,
        run: () => onProjectChange(project.ProjectID),
        onDelete: onDeleteProject && !project.System ? () => onDeleteProject(project) : undefined,
      })
    }

    // Agent actions
    actions.push({
      id: 'agent:create',
      section: 'agent',
      label: t('omnibox.action.newAgent'),
      description: t('omnibox.action.newAgentDesc'),
      icon: <Plus size={16} />,
      keywords: ['new', 'agent', 'create', 'xinjian'],
      score: 0,
      run: () => onCreateAgent(activeProjectId ?? undefined),
    })

    for (const agent of agents) {
      const isActive = agent.ActorId === activeAgentId
      const label = agent.Title || agent.DisplayName || 'Agent'
      actions.push({
        // Prefer agent.Id (always unique in real data, e.g. "Oracle#64c6") over
        // ActorId, which can be empty for some agent kinds. Empty ActorId would
        // collapse many agents into a single "agent:" id and get deduped/filtered
        // out by the recent-commands set, hiding most agents from the palette.
        id: `agent:${agent.Id || agent.ActorId}`,
        section: 'agent',
        label,
        description: agent.ProjectName || agent.AgentKind,
        icon: (
          <span
            className={`app-omnibox-agent-avatar${agent.IsWorking ? ' working' : ''}`}
            style={{ '--avatar-hue': avatarHue(agent.ActorId || agent.Id) } as React.CSSProperties}
          >
            {agentAvatarLabel(agent.DisplayName, agent.AgentKind)}
          </span>
        ),
        shortcut: isActive ? t('omnibox.shortcut.current') : undefined,
        keywords: [label, agent.DisplayName, agent.AgentKind, agent.ProjectName, 'agent'],
        score: isActive ? 50 : 0,
        run: () => onOpenAgentChat(agent),
        onDelete: onDeleteAgent ? () => onDeleteAgent(agent) : undefined,
      })
    }

    // Mode actions
    for (const mode of MODES) {
      const isActive = mode.id === contentMode
      actions.push({
        id: `mode:${mode.id}`,
        section: 'mode',
        label: mode.label,
        description: t(mode.descKey as Parameters<typeof t>[0]),
        icon: mode.icon,
        shortcut: isActive ? t('omnibox.shortcut.current') : undefined,
        keywords: [mode.label, mode.id, mode.descKey, 'mode', 'moshi'],
        score: isActive ? 50 : 0,
        run: () => onContentModeChange(mode.id),
      })
    }

    // Settings / panel actions
    for (const category of categories) {
      actions.push({
        id: `settings:${category.id}`,
        section: 'settings',
        label: category.id,
        description: t('omnibox.desc.settings'),
        icon: ICONS_BY_SETTINGS_ID[category.id] ?? <Settings size={16} />,
        keywords: [category.id, 'settings', 'shezhi', 'panel'],
        score: 0,
        run: () => onOpenSettings(category.id),
      })
    }

    return actions
  }, [
    t,
    contentMode,
    activeProjectId,
    activeAgentId,
    projects,
    agents,
    onContentModeChange,
    onProjectChange,
    onOpenAgentChat,
    onCreateProject,
    onCreateAgent,
    onOpenSettings,
    onExportHistory,
    onImportHistory,
    onDeleteAgent,
    onDeleteProject,
  ])

  const recentRef = useRef(recentCommands)
  useEffect(() => {
    recentRef.current = recentCommands
  }, [recentCommands])

  const recordUse = useCallback(
    (id: string) => {
      if (!allActions.some((a) => a.id === id)) return
      const next = [id, ...recentRef.current.filter((cmd) => cmd !== id)].slice(0, MAX_RECENT)
      recentRef.current = next
      onRecentChange(next)
    },
    [allActions, onRecentChange]
  )

  const actionsWithRecent = useMemo<AppOmniboxAction[]>(() => {
    const recentSet = new Set(recentCommands)
    const existingIds = new Set(allActions.map((a) => a.id))
    const validRecentIds = recentCommands.filter((id) => existingIds.has(id))

    const recentActions: AppOmniboxAction[] = validRecentIds
      .map((id) => allActions.find((a) => a.id === id)!)
      .map((action) => ({ ...action, section: 'recent' as const, score: 100 }))

    const nonRecent = allActions.filter((a) => !recentSet.has(a.id))

    return [...recentActions, ...nonRecent]
  }, [allActions, recentCommands])

  const filter = useCallback(
    (query: string): AppOmniboxAction[] => {
      // Empty query: return the full action list (recent + base order) so the
      // palette shows all agents/commands on open — not just the few with a
      // positive base score. Filtering by score only applies to typed queries.
      if (!query.trim()) return actionsWithRecent
      const scored = actionsWithRecent
        .map((action) => ({ action, score: scoreMatch(query, action) }))
        .filter((item) => item.score > 0)
        .sort((a, b) => b.score - a.score)
        .map((item) => item.action)
      return scored
    },
    [actionsWithRecent]
  )

  return { actions: actionsWithRecent, recordUse, filter }
}
