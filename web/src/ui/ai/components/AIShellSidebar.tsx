import React, { useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { AlertCircle, ArrowDownAZ, ArrowUpAZ, BookOpen, Bot, Brain, Check, ChevronDown, ChevronRight, CircleHelp, Clock, Cloud, Code, Compass, ConciergeBell, File, FileCode, FileText, Folder, FolderOpen, FolderPlus, FolderTree, Funnel, GitBranch, Hexagon, Languages, Lock, LogIn, LogOut, MessageCircle, MessageSquare, Moon, Package, Pin, PinOff, Plus, Puzzle, RefreshCw, Search, Settings, Smartphone, SquarePen, Sun, Target, Unplug, UserRound, Wrench, Waypoints, X } from 'lucide-react'
import { useLongPress } from '../hooks/useLongPress'
import { useBrowserOverlay } from '../browserOverlay'
import { isWails } from '../../../application/runtime'
import * as cloudaccount from '../../../gen-clients/cloudaccount/client'
import { refreshInsiderAccess } from '../hooks/useInsiderAccess'
import type { CloudAccountStatus } from '../../../gen-clients/system/types'
import { monoStore } from '../../panels/mono-store'
import type { MonoCardListItem } from '../../../domain/mono-types'
import { resolveTaskMode } from './scheduledTasks'
import type {
  AccountSnapshot,
} from '../../../gen-types/workspace'
import type {
  ProjectSnapshot,
} from '../../../domain/types'
import { projectDisplayName } from '../../../application/project-adapter'
import type { MonoCardListItem as WireMonoCardListItem, SystemTreeNode } from '../../../gen-clients/system/types'
import type { AgentInfo } from '../hooks/agentInfoStore'
import { hasUnreadCompletion } from '../hooks/agentInfoStore'
import { avatarHue, agentDisplayName, sidebarAgentLabel } from '../lib/agent-avatar'
import { AgentAvatarContent } from './AgentAvatarContent'
import { useNow, useRelativeTime } from '../../lib/format-time'
import type { ThemeState } from '@qomos/sporemind-theme'
import { client } from '../../../application/generated-client'
import * as wikiClient from '../../../gen-clients/project/client'
import { useI18n, type Locale, supportedLocales, localeLabels } from '../../../i18n'
import { persistLocale } from '../../../application/locale-persist'
import { getCategoryExpansion, toggleCategoryExpansion, getExpandedProjectIds, toggleExpandedProjectId } from '../../../application/workspace-ui-state'
import { useMonoStore } from '../hooks/useMonoStore'
import type { I18nKey } from '../../../i18n/types'
import { overrideBuiltinTitle } from '../../../domain/builtin-cards'
import { useResolvedContentModes, type ResolvedMode } from '../content-modes'
import { buildAgentForest, flattenAgentForest, type AgentTreeNode } from '../lib/agent-tree'
import { jumpToWorktreeGit } from '../hooks/worktreeGitJump'
import { SidebarModeSwitch, type LauncherMode } from './SidebarModeSwitch'
import { CloudLoginOverlay } from './CloudLoginOverlay'
import './AIShellSidebar.css'

interface PinnedItem {
  id: string
  projectId: string
  type: 'project' | 'agent' | 'card' | 'card-builtin' | 'file'
  targetId: string
  label: string
  agentInfo?: AgentInfo
  isDir?: boolean
}

const PINNED_STORAGE_KEY = 'sporemind:sidebar:pinned'

const PinnedContext = React.createContext<{
  pinned: PinnedItem[]
  isPinned: (id: string) => boolean
  addPinned: (item: PinnedItem) => void
  removePinned: (id: string) => void
  togglePinned: (item: PinnedItem) => void
} | null>(null)

function PinnedProvider({ children }: { children: React.ReactNode }) {
  const [pinned, setPinned] = useState<PinnedItem[]>(() => {
    try {
      const raw = localStorage.getItem(PINNED_STORAGE_KEY)
      return raw ? JSON.parse(raw) : []
    } catch {
      return []
    }
  })

  useEffect(() => {
    localStorage.setItem(PINNED_STORAGE_KEY, JSON.stringify(pinned))
  }, [pinned])

  const isPinned = useCallback((id: string) => pinned.some(f => f.id === id), [pinned])

  const addPinned = useCallback((item: PinnedItem) => {
    setPinned(prev => {
      if (prev.some(f => f.id === item.id)) return prev
      return [...prev, item]
    })
  }, [])

  const removePinned = useCallback((id: string) => {
    setPinned(prev => prev.filter(f => f.id !== id))
  }, [])

  const togglePinned = useCallback((item: PinnedItem) => {
    if (isPinned(item.id)) {
      removePinned(item.id)
    } else {
      addPinned(item)
    }
  }, [isPinned, addPinned, removePinned])

  const value = useMemo(() => ({
    pinned,
    isPinned,
    addPinned,
    removePinned,
    togglePinned,
  }), [pinned, isPinned, addPinned, removePinned, togglePinned])

  return <PinnedContext.Provider value={value}>{children}</PinnedContext.Provider>
}

function usePinnedContext() {
  const ctx = useContext(PinnedContext)
  if (!ctx) throw new Error('usePinnedContext must be used within PinnedProvider')
  return ctx
}

function PinButton({
  item,
  className = '',
}: {
  item: PinnedItem
  className?: string
}) {
  const { t } = useI18n()
  const { isPinned, togglePinned } = usePinnedContext()
  const active = isPinned(item.id)
  return (
    <button
      type="button"
      className={`ai-sidebar-pin-button${active ? ' active' : ''} ${className}`.trim()}
      onClick={(e) => { e.stopPropagation(); togglePinned(item) }}
      title={active ? t('shell.sidebar.unpin') : t('shell.sidebar.pin')}
    >
      <Pin size={12} />
    </button>
  )
}

function SidebarPinnedSection({
  renderItem,
}: {
  renderItem: (item: PinnedItem) => React.ReactNode
}) {
  const { t } = useI18n()
  const { pinned } = usePinnedContext()

  return (
    <ProjectCategorySection
      label={t('shell.sidebar.pinned')}
      storageKey="pinned"
      defaultOpen
      rightIcon={<ChevronDown size={12} fill="currentColor" />}
    >
      {pinned.length === 0 ? (
        <div className="ai-sidebar-category-empty">{t('shell.sidebar.pinned.empty')}</div>
      ) : pinned
        .map(item => ({ item, node: renderItem(item) }))
        .filter(x => x.node !== null && x.node !== false)
        .map(({ item, node }) => (
          <div key={item.id} className="ai-sidebar-pinned-wrapper">{node}</div>
        ))}
    </ProjectCategorySection>
  )
}

function SidebarPinnedItem({
  item,
  onClick,
  active,
}: {
  item: PinnedItem
  onClick: () => void
  active?: boolean
}) {
  const icon = (() => {
    switch (item.type) {
      case 'project':
        return <Folder size={11} />
      case 'agent':
        if (item.agentInfo) {
          const hue = avatarHue(item.agentInfo.Id)
          return (
            <span
              className={`ai-sidebar-pinned-avatar${(item.agentInfo.AgentKind ?? '').toLowerCase() === 'coordinator' ? ' coordinator' : ''}${item.agentInfo.LoadState !== 'loaded' ? ' unloaded' : ''}`}
              style={{ '--avatar-hue': hue } as React.CSSProperties}
            >
              <AgentAvatarContent agent={item.agentInfo} size={15} />
            </span>
          )
        }
        return <UserRound size={11} />
      case 'card':
        return <MessageCircle size={11} />
      case 'card-builtin': {
        const builtin = BUILTIN_NODES.find(b => b.id === item.targetId)
        return builtin ? builtin.icon : <BookOpen size={11} />
      }
      case 'file':
        return item.isDir ? <Folder size={11} /> : <File size={11} />
      default:
        return null
    }
  })()

  return (
    <button type="button" className={`ai-sidebar-pinned-item${active ? ' active' : ''}`} onClick={onClick}>
      <span className="ai-sidebar-pinned-item-icon">{icon}</span>
      <span className="ai-sidebar-pinned-item-label">{item.label}</span>
      <PinButton item={item} />
    </button>
  )
}

function mapServerListItem(item: WireMonoCardListItem): MonoCardListItem {
  return {
    id: item.Id,
    type: item.Type || 'wiki',
    source: item.Source || 'project',
    storage: item.Storage || 'cardstore',
    visibility: item.Visibility || 'wiki',
    protected: item.Protected === true,
    editable: item.Editable !== false,
    deletable: item.Deletable !== false,
    tags: item.Tags || [],
    list: item.List || [],
    modified: item.Modified,
    created: item.Created,
    due: item.Due,
    priority: item.Priority as MonoCardListItem['priority'],
    status: item.Status as MonoCardListItem['status'],
    parent: item.Parent,
    standalone: item.Standalone,
    raw: item.Raw,
  }
}


export type ContentMode = 'conversation' | 'topology' | 'workflow' | 'scheduled' | 'multiconsole' | 'notes' | 'problems' | 'approvals' | 'ssh' | 'db' | 'files' | 'aistats' | 'git'

interface AIShellSidebarProps {
  projects: ProjectSnapshot[]
  systemTreeNodes: SystemTreeNode[]
  activeProject: ProjectSnapshot | null
  projectAgents: AgentInfo[]
  activeConversationId: string | null
  onOpenCreateProject: () => void
  onSelectProject: (id: string) => void
  onReorderProjects: (orderedIds: string[]) => void
  onReorderAgents: (orderedIds: string[]) => void
  onAgentClick: (agent: AgentInfo) => void
  globalAgents: AgentInfo[]
  // Virtual per-app directory projection for dedicated plugin agents: one
  // group per owning app (never empty — a group only exists while the app has
  // at least one bound agent). Rendered between the flat global agents and
  // the project folders.
  pluginAgentGroups?: { appId: string; label: string; agents: AgentInfo[] }[]
  onSelectCoordinator?: (agent: AgentInfo) => void
  onEditAgent?: (agent: AgentInfo) => void
  onOpenAgentMenu?: (agent: AgentInfo, x: number, y: number) => void
  onDeleteAgent?: (agent: AgentInfo) => void
  onCreateAgent?: (projectId: string) => void
  onNewChat?: () => void
  onOpenProjectMenu?: (project: ProjectSnapshot, x: number, y: number) => void
  onCloseSidebar?: () => void
  isMobile: boolean
  account: AccountSnapshot | null
  theme: ThemeState
  onThemeChange: (state: ThemeState) => void
  contentMode: ContentMode
  onContentModeChange: (mode: ContentMode, params?: { cardId?: string; label?: string; projectId?: string }) => void
  pinnedModes?: string[]
  onReorderPinnedModes?: (ordered: string[]) => void
  onTogglePinnedMode?: (mode: string) => void
  /** Active knowledge-base lane (notes mode): highlights the pinned card item
   *  whose project + card match the currently open swimlane. */
  activeKbLane?: { projectId: string; projectName: string; cardId?: string } | null
  onOpenSettings?: () => void
  onOpenMobileSync?: () => void
  onShellModeChange?: () => void
  onOpenFile?: (filePath: string) => void
  /** Open the omnibox command palette. */
  onOpenOmnibox?: () => void
  /** Launcher (home) pane variant; when set together with
   *  onLauncherModeChange, renders the launcher-mode switch atop the
   *  sidebar mode group, above the new-chat button. */
  launcherMode?: LauncherMode
  onLauncherModeChange?: (next: LauncherMode) => void
}

function accountLabel(account: AccountSnapshot | null): string {
  return account?.DisplayName || 'Guest'
}

function projectTypeIcon(open?: boolean): React.ReactNode {
  return open ? <FolderOpen size={16} /> : <Folder size={16} />
}

function ModeSwitchButtons({
  contentMode,
  onContentModeChange,
  onShellModeChange,
  pinnedModes,
  onReorderPinnedModes,
  onTogglePinnedMode,
  projects,
  activeProject,
  onCreateAgent,
  onNewChat,
  launcherMode,
  onLauncherModeChange,
  isMobile,
}: {
  contentMode: ContentMode
  onContentModeChange: (mode: ContentMode, params?: { cardId?: string; label?: string; projectId?: string }) => void
  onShellModeChange?: () => void
  pinnedModes?: string[]
  onReorderPinnedModes?: (ordered: string[]) => void
  onTogglePinnedMode?: (mode: string) => void
  projects: ProjectSnapshot[]
  activeProject: ProjectSnapshot | null
  onCreateAgent?: (projectId: string) => void
  onNewChat?: () => void
  launcherMode?: LauncherMode
  onLauncherModeChange?: (next: LauncherMode) => void
  isMobile: boolean
}) {
  const { t } = useI18n()
  const resolvedModes = useResolvedContentModes()
  const byId = useMemo(() => {
    const map: Record<string, ResolvedMode> = {}
    for (const m of resolvedModes) map[m.id] = m
    return map
  }, [resolvedModes])

  const pinnedList = pinnedModes ?? []
  const conversation = byId['conversation']!
  // Pinned modes display in pinnedModes array order so drag reorder is reflected.
  const pinnedEntries = pinnedList
    .map(id => byId[id])
    .filter((m): m is ResolvedMode => !!m && m.pinnable)
  const activeEntry = byId[contentMode]
  const showActiveFallback = activeEntry && activeEntry.id !== 'conversation'
    && !pinnedList.includes(activeEntry.id)

  const visibleEntries: ResolvedMode[] = [conversation, ...pinnedEntries]
  if (showActiveFallback && activeEntry) visibleEntries.push(activeEntry)
  // De-duplicate while preserving order.
  const deduped = visibleEntries.filter((m, i, arr) => arr.findIndex(x => x.id === m.id) === i)

  // Drag-to-reorder state. Only pinned modes are draggable / drop targets.
  const [dragId, setDragId] = useState<string | null>(null)
  const [overId, setOverId] = useState<string | null>(null)
  const [overPos, setOverPos] = useState<'before' | 'after'>('before')

  const canDrag = !!onReorderPinnedModes && pinnedEntries.length > 0

  const handleDragStart = (e: React.DragEvent, id: string) => {
    if (!onReorderPinnedModes) return
    setDragId(id)
    e.dataTransfer.effectAllowed = 'move'
    try { e.dataTransfer.setData('text/plain', id) } catch { /* ignore */ }
  }

  const handleDragOver = (e: React.DragEvent, id: string) => {
    if (!onReorderPinnedModes || !dragId) return
    if (id === 'conversation' || !pinnedList.includes(id)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
    setOverPos(e.clientY < rect.top + rect.height / 2 ? 'before' : 'after')
    setOverId(id)
  }

  const handleDrop = (e: React.DragEvent, id: string) => {
    if (!onReorderPinnedModes || !dragId || id === 'conversation' || !pinnedList.includes(id)) {
      setDragId(null)
      setOverId(null)
      return
    }
    e.preventDefault()
    if (dragId !== id) {
      const ordered: string[] = pinnedEntries.map(m => m.id)
      const fromIdx = ordered.indexOf(dragId)
      const toIdx = ordered.indexOf(id)
      if (fromIdx !== -1 && toIdx !== -1) {
        ordered.splice(fromIdx, 1)
        let insertAt = ordered.indexOf(id)
        if (overPos === 'after') insertAt += 1
        ordered.splice(insertAt, 0, dragId)
        onReorderPinnedModes(ordered)
      }
    }
    setDragId(null)
    setOverId(null)
  }

  const handleDragEnd = () => {
    setDragId(null)
    setOverId(null)
  }

  return (
    <div className="ai-sidebar-mode-group">
      {/* Mobile only — on desktop the switch parks in the shell topbar. */}
      {isMobile && launcherMode !== undefined && onLauncherModeChange && (
        <SidebarModeSwitch value={launcherMode} onChange={onLauncherModeChange} />
      )}
      {(onNewChat || onCreateAgent) && (
        <button
          type="button"
          className="ai-sidebar-mode-btn ai-sidebar-mode-new-chat"
          data-guide-id="sidebar.new-chat"
          title={t('shell.sidebar.newChat')}
          onClick={() => onNewChat ? onNewChat() : onCreateAgent!(activeProject?.ProjectID ?? projects.find(project => !project.System)?.ProjectID ?? '')}
        >
          <SquarePen size={16} strokeWidth={1.5} />
          <span>{t('shell.sidebar.newChat')}</span>
        </button>
      )}
      {deduped.map(m => {
        const draggable = canDrag && pinnedList.includes(m.id)
        const isDragging = dragId === m.id
        const isOver = draggable && overId === m.id
        const dragClass = isDragging ? ' dragging' : (isOver ? (overPos === 'before' ? ' drag-over-before' : ' drag-over-after') : '')
        return (
          <button
            key={m.id}
            type="button"
            className={`ai-sidebar-mode-btn${contentMode === m.id ? ' active' : ''}${draggable ? ' draggable' : ''}${dragClass}`}
            title={m.label}
            onClick={() => onContentModeChange(m.id)}
            draggable={draggable || undefined}
            onDragStart={draggable ? (e) => handleDragStart(e, m.id) : undefined}
            onDragOver={draggable ? (e) => handleDragOver(e, m.id) : undefined}
            onDrop={draggable ? (e) => handleDrop(e, m.id) : undefined}
            onDragEnd={draggable ? handleDragEnd : undefined}
          >
            <m.Icon size={16} />
            <span>{m.label}</span>
            {contentMode === m.id && <span className="ai-sidebar-mode-active-dot" aria-hidden="true" />}
            {m.pinnable && pinnedList.includes(m.id) && onTogglePinnedMode && (
              <span
                role="button"
                tabIndex={0}
                className="ai-sidebar-mode-unpin"
                title={t('shell.sidebar.unpin')}
                aria-label={t('shell.sidebar.unpin')}
                onClick={(e) => { e.stopPropagation(); onTogglePinnedMode(m.id) }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    e.stopPropagation()
                    onTogglePinnedMode(m.id)
                  }
                }}
              >
                <PinOff size={14} />
              </span>
            )}
          </button>
        )
      })}
      {onShellModeChange && (
        <button
          type="button"
          className="ai-sidebar-mode-btn"
          title={t('workbench.options.workbench')}
          onClick={() => onShellModeChange()}
        >
          <Hexagon size={16} />
          <span>{t('workbench.options.workbench')}</span>
        </button>
      )}
    </div>
  )
}

function ClickableMenuItem({
  icon,
  label,
  active,
  badge,
  role,
  disabled,
  onClick,
}: {
  icon?: React.ReactNode
  label: string
  active?: boolean
  badge?: string
  role?: string
  disabled?: boolean
  onClick?: () => void
}) {
  return (
    <button type="button" className={`ai-sidebar-menu-item${active ? ' active' : ''}`} role={role} onClick={onClick} disabled={disabled}>
      <span className="ai-sidebar-menu-icon">{icon}</span>
      <span className="ai-sidebar-menu-label">{label}</span>
      {badge ? <span className="ai-sidebar-menu-badge">{badge}</span> : null}
    </button>
  )
}

type ProjectSortMode = 'name-asc' | 'name-desc' | 'none'

export type AgentTimeFilter = 'all' | '5m' | '30m' | '5h' | '24h'

const AGENT_TIME_FILTER_MS: Record<Exclude<AgentTimeFilter, 'all'>, number> = {
  '5m': 5 * 60_000,
  '30m': 30 * 60_000,
  '5h': 5 * 60 * 60_000,
  '24h': 24 * 60 * 60_000,
}

// Pure predicate (unit-testable): an agent passes the sidebar time filter when
// its latest known timestamp (turn completion, else last activity) falls within
// the window. Agents without a parseable timestamp never match a window.
export function agentWithinTimeFilter(
  agent: Pick<AgentInfo, 'LastTurnCompletedAt' | 'LastActivity'>,
  filter: AgentTimeFilter,
  now: number,
): boolean {
  if (filter === 'all') return true
  const stamp = agent.LastTurnCompletedAt || agent.LastActivity
  if (!stamp) return false
  const ts = Date.parse(stamp)
  if (Number.isNaN(ts)) return false
  return now - ts <= AGENT_TIME_FILTER_MS[filter]
}

function ProjectSortDropdown({
  sortMode,
  onSortModeChange,
  showFolders,
  onToggleShowFolders,
  timeFilter,
  onTimeFilterChange,
}: {
  sortMode: ProjectSortMode
  onSortModeChange: (mode: ProjectSortMode) => void
  showFolders: boolean
  onToggleShowFolders: (next: boolean) => void
  timeFilter: AgentTimeFilter
  onTimeFilterChange: (filter: AgentTimeFilter) => void
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const options: Array<{ id: ProjectSortMode; label: string; icon: React.ReactNode }> = [
    { id: 'name-asc', label: t('shell.sidebar.sort.nameAsc'), icon: <ArrowDownAZ size={14} /> },
    { id: 'name-desc', label: t('shell.sidebar.sort.nameDesc'), icon: <ArrowUpAZ size={14} /> },
  ]

  const timeOptions: Array<{ id: AgentTimeFilter; label: string }> = [
    { id: 'all', label: t('shell.sidebar.filter.time.all') },
    { id: '5m', label: t('shell.sidebar.filter.time.m5') },
    { id: '30m', label: t('shell.sidebar.filter.time.m30') },
    { id: '5h', label: t('shell.sidebar.filter.time.h5') },
    { id: '24h', label: t('shell.sidebar.filter.time.h24') },
  ]

  const filterActive = !showFolders || timeFilter !== 'all'

  return (
    <div className="ai-sidebar-sort-dropdown" ref={ref}>
      <button
        type="button"
        className={`ai-sidebar-toolbar-button${filterActive ? ' filter-active' : ''}`}
        title={t('shell.sidebar.sort.title')}
        aria-label={t('shell.sidebar.sort.title')}
        aria-expanded={open}
        onClick={() => setOpen(v => !v)}
      >
        <Funnel size={13} />
      </button>
      {open ? (
        <div className="ai-sidebar-sort-dropdown-menu" role="menu">
          <div className="ai-sidebar-sort-dropdown-section">{t('shell.sidebar.sort.title')}</div>
          {options.map(option => (
            <button
              key={option.id}
              type="button"
              className={`ai-sidebar-sort-dropdown-item${sortMode === option.id ? ' active' : ''}`}
              role="menuitemradio"
              aria-checked={sortMode === option.id}
              onClick={() => {
                onSortModeChange(option.id)
                setOpen(false)
              }}
            >
              <span className="ai-sidebar-sort-dropdown-icon">{option.icon}</span>
              <span className="ai-sidebar-sort-dropdown-label">{option.label}</span>
              {sortMode === option.id ? <Check size={12} className="ai-sidebar-sort-dropdown-check" /> : null}
            </button>
          ))}
          <div className="ai-sidebar-sort-dropdown-divider" />
          <div className="ai-sidebar-sort-dropdown-section">{t('shell.sidebar.filter.section.display')}</div>
          <button
            type="button"
            className={`ai-sidebar-sort-dropdown-item${showFolders ? ' active' : ''}`}
            role="menuitemcheckbox"
            aria-checked={showFolders}
            onClick={() => onToggleShowFolders(!showFolders)}
          >
            <span className="ai-sidebar-sort-dropdown-icon">{showFolders ? <FolderOpen size={14} /> : <Folder size={14} />}</span>
            <span className="ai-sidebar-sort-dropdown-label">{t('shell.sidebar.filter.showFolders')}</span>
            {showFolders ? <Check size={12} className="ai-sidebar-sort-dropdown-check" /> : null}
          </button>
          <div className="ai-sidebar-sort-dropdown-divider" />
          <div className="ai-sidebar-sort-dropdown-section">{t('shell.sidebar.filter.section.time')}</div>
          {timeOptions.map(option => (
            <button
              key={option.id}
              type="button"
              className={`ai-sidebar-sort-dropdown-item${timeFilter === option.id ? ' active' : ''}`}
              role="menuitemradio"
              aria-checked={timeFilter === option.id}
              onClick={() => {
                onTimeFilterChange(option.id)
                setOpen(false)
              }}
            >
              <span className="ai-sidebar-sort-dropdown-icon"><Clock size={14} /></span>
              <span className="ai-sidebar-sort-dropdown-label">{option.label}</span>
              {timeFilter === option.id ? <Check size={12} className="ai-sidebar-sort-dropdown-check" /> : null}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  )
}

function recentProjects(projects: ProjectSnapshot[]): ProjectSnapshot[] {
  const safe = projects ?? []
  const system = safe.filter(project => project.System)
  const rest = safe.filter(project => !project.System)
  return [...system, ...rest]
}

/** Folder-less projection used when the sidebar "show folders" filter is off:
 * plugin-group agents plus per-project agents rendered as one flat list of
 * plain rows (no directories, no drag-and-drop — there is no container to
 * reorder within). Global agents are rendered by the sidebar itself above. */
function FlatAgentList({
  projects,
  projectAgents,
  pluginAgentGroups,
  agentPassesTimeFilter,
  activeConversationId,
  onSelect,
  onEdit,
  onOpenMenu,
  onDelete,
}: {
  projects: ProjectSnapshot[]
  projectAgents: AgentInfo[]
  pluginAgentGroups: { appId: string; label: string; agents: AgentInfo[] }[]
  agentPassesTimeFilter: (agent: AgentInfo) => boolean
  activeConversationId: string | null
  onSelect: (agent: AgentInfo) => void
  onEdit?: (agent: AgentInfo) => void
  onOpenMenu?: (agent: AgentInfo, x: number, y: number) => void
  onDelete?: (agent: AgentInfo) => void
}) {
  const agents: AgentInfo[] = []
  for (const group of pluginAgentGroups) {
    for (const agent of group.agents) {
      if (agentPassesTimeFilter(agent)) agents.push(agent)
    }
  }
  for (const project of projects) {
    for (const agent of projectAgents) {
      if (agent.ProjectId === project.ProjectID && agentPassesTimeFilter(agent)) agents.push(agent)
    }
  }
  if (agents.length === 0) return null
  return (
    <>
      {agents.map(agent => (
        <ProjectAgentItem
          key={agent.Id}
          active={activeConversationId === agent.Id}
          item={agent}
          dragPosition={null}
          isDragging={false}
          onSelect={onSelect}
          onEdit={onEdit}
          onOpenMenu={onOpenMenu}
          onDelete={onDelete}
          onDragStart={() => {}}
          onDragEnd={() => {}}
          onDragEnter={() => {}}
          onDragLeave={() => {}}
          onReorder={() => {}}
        />
      ))}
    </>
  )
}

/** Collapsible category section inside an expanded project. */
function ProjectCategorySection({
  icon,
  label,
  storageKey,
  defaultOpen = false,
  toolbar,
  rightIcon,
  className,
  children,
}: {
  icon?: React.ReactNode
  label: string
  storageKey: string
  defaultOpen?: boolean
  toolbar?: React.ReactNode
  rightIcon?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)

  useEffect(() => {
    let cancelled = false
    void getCategoryExpansion().then(data => {
      if (!cancelled && typeof data[storageKey] === 'boolean') setOpen(data[storageKey])
    })
    return () => { cancelled = true }
  }, [storageKey])

  const toggle = () => {
    const next = !open
    setOpen(next)
    void toggleCategoryExpansion(storageKey, defaultOpen)
  }
  return (
    <div className={`ai-sidebar-category${className ? ` ${className}` : ''}`}>
      <div className="ai-sidebar-category-header">
        <button type="button" className="ai-sidebar-category-toggle" onClick={toggle} aria-expanded={open}>
          {icon && (
            <span className="ai-sidebar-category-icon">
              <span className="ai-sidebar-category-icon-default">{icon}</span>
              <span className="ai-sidebar-category-icon-hover">
                <ChevronRight size={12} className={open ? 'open' : ''} />
              </span>
            </span>
          )}
          <span className="ai-sidebar-category-label">{label}</span>
          {rightIcon && (
            <span className="ai-sidebar-category-right-icon" aria-hidden="true">
              {rightIcon}
            </span>
          )}
        </button>
        {toolbar && <span className="ai-sidebar-category-toolbar">{toolbar}</span>}
      </div>
      {open && <div className="ai-sidebar-category-children">{children}</div>}
    </div>
  )
}

// PluginAgentGroup is the virtual per-app directory: a collapsible section
// that groups one plugin's dedicated agents (AgentKind "plugin", workspace-
// global, keyed by BoundAppId). It is a pure frontend projection — no backend
// entity; the group exists only while agents is non-empty, so deleting the
// last bound agent (or unregistering the plugin) removes the directory. Agent
// rows inside are plain, non-draggable (no project to reorder them within).
function PluginAgentGroup({
  appId,
  label,
  agents,
  activeAgentId,
  onSelect,
  onEdit,
  onOpenMenu,
  onDelete,
}: {
  appId: string
  label: string
  agents: AgentInfo[]
  activeAgentId: string | null
  onSelect: (agent: AgentInfo) => void
  onEdit?: (agent: AgentInfo) => void
  onOpenMenu?: (agent: AgentInfo, x: number, y: number) => void
  onDelete?: (agent: AgentInfo) => void
}) {
  if (agents.length === 0) return null
  return (
    <ProjectCategorySection
      className="ai-sidebar-category-plugin"
      icon={<Puzzle size={16} />}
      label={label}
      storageKey={`plugin-group:${appId}`}
      defaultOpen={false}
      rightIcon={<span className="ai-sidebar-project-count">{agents.length}</span>}
    >
      {agents.map(agent => (
        <ProjectAgentItem
          key={agent.Id}
          active={activeAgentId === agent.Id}
          item={agent}
          dragPosition={null}
          isDragging={false}
          noDrag
          onSelect={onSelect}
          onEdit={onEdit}
          onOpenMenu={onOpenMenu}
          onDelete={onDelete}
          onDragStart={() => {}}
          onDragEnd={() => {}}
          onDragEnter={() => {}}
          onDragLeave={() => {}}
          onReorder={() => {}}
        />
      ))}
    </ProjectCategorySection>
  )
}

/** Lazy-loaded file tree node. Directories load their children only when
 * expanded. Icons and titles are separately clickable: title opens the file
 * (or toggles a folder), icon toggles folder expansion. Supports right-click
 * context menu (copy/cut/paste/etc.) and drag-and-drop file moving. */

/** Built-in category cards and dynamic TOC tree nodes for the Cards sidebar. */
const BUILTIN_TOC_ID = 'toc'
const BUILTIN_SKILL_ID = '__builtin_skill__'
const BUILTIN_OPEN_ID = '__builtin_open__'
const BUILTIN_RECENT_ID = '__builtin_recent__'

type BuiltinType = 'toc' | 'project_info' | 'skill' | 'plugin' | 'agent' | 'open' | 'recent'

const BUILTIN_NODES: Array<{ id: string; type: BuiltinType; icon: React.ReactNode; i18nKey: I18nKey }> = [
  { id: BUILTIN_TOC_ID, type: 'toc', icon: <FolderTree size={11} />, i18nKey: 'shell.sidebar.cards.toc' },
  { id: '__builtin_plugin__', type: 'plugin', icon: <Wrench size={11} />, i18nKey: 'shell.sidebar.cards.plugin' },
  { id: '__builtin_agent__', type: 'agent', icon: <Bot size={11} />, i18nKey: 'shell.sidebar.cards.agent' },
  { id: BUILTIN_SKILL_ID, type: 'skill', icon: <Wrench size={11} />, i18nKey: 'shell.sidebar.cards.skill' },
  { id: BUILTIN_OPEN_ID, type: 'open', icon: <BookOpen size={11} />, i18nKey: 'shell.sidebar.cards.open' },
  { id: BUILTIN_RECENT_ID, type: 'recent', icon: <Clock size={11} />, i18nKey: 'shell.sidebar.cards.recent' },
]


function PluginFolderIcon({ open, size = 12 }: { open?: boolean; size?: number }) {
  return (
    <span className="ai-sidebar-plugin-folder">
      {open ? <FolderOpen size={size} /> : <Folder size={size} />}
      <Package size={Math.round(size * 0.55)} className="ai-sidebar-plugin-folder-badge" />
    </span>
  )
}

function SystemNodeIcon({ kind, open }: { kind: string; open?: boolean }) {
  switch (kind) {
    case 'app-dir': return <Package size={12} />
    case 'dev-app-dir': return <Wrench size={12} />
    case 'sporeapp-dir': return <Code size={12} />
    case 'installed-plugin': return <PluginFolderIcon open={open} />
    case 'dev-app': return <Folder size={12} />
    case 'sporeapp': return <FileCode size={12} />
    // Legacy compatibility
    case 'plugins': return <PluginFolderIcon open={open} />
    case 'dev-plugins': return <PluginFolderIcon open={open} />
    case 'dev-plugin': return <PluginFolderIcon open={open} />
    default: return <Folder size={12} />
  }
}

function SystemNodeItem({ node, depth = 0 }: { node: SystemTreeNode; depth?: number }) {
  const [expanded, setExpanded] = useState(depth < 1)
  const hasChildren = (node.Children?.length ?? 0) > 0
  const paddingLeft = 8 + depth * 16

  return (
    <div className="ai-sidebar-system-node">
      <div
        className={`ai-sidebar-system-node-row${hasChildren ? ' expandable' : ''}`}
        style={{ paddingLeft }}
        onClick={() => hasChildren ? setExpanded(v => !v) : undefined}
        role={hasChildren ? 'button' : undefined}
      >
        <span className="ai-sidebar-system-node-icon">
          <SystemNodeIcon kind={node.Kind} open={expanded} />
        </span>
        <span className="ai-sidebar-system-node-name">{node.Name}</span>
        {node.Status && (
          <span className={`ai-sidebar-system-node-status status-${node.Status}`}>{node.Status}</span>
        )}
        {node.Extra?.version && (
          <span className="ai-sidebar-system-node-version">v{node.Extra.version}</span>
        )}
      </div>
      {hasChildren && expanded && (
        <div className="ai-sidebar-system-node-children">
          {node.Children!.map(child => (
            <SystemNodeItem key={child.Id} node={child} depth={depth + 1} />
          ))}
        </div>
      )}
    </div>
  )
}

const DRAG_HYSTERESIS_RATIO = 0.3

function computeDragPosition(
  clientY: number,
  rect: DOMRect,
  currentPosition: 'before' | 'after' | null,
): 'before' | 'after' {
  const mid = rect.top + rect.height / 2
  if (!currentPosition) return clientY < mid ? 'before' : 'after'
  const threshold = rect.height * DRAG_HYSTERESIS_RATIO
  if (currentPosition === 'before' && clientY > rect.top + rect.height / 2 + threshold) {
    return 'after'
  }
  if (currentPosition === 'after' && clientY < rect.top + rect.height / 2 - threshold) {
    return 'before'
  }
  return currentPosition
}

function ProjectMenuItem({
  active,
  item,
  agents,
  activeAgentId,
  expanded,
  onToggleExpanded,
  dragPosition,
  isDragging,
  agentDragState,
  onSelect,
  onSelectAgent,
  onEditAgent,
  onOpenAgentMenu,
  onDeleteAgent,
  onCreateAgent,
  onOpenProjectMenu,
  onDragStart,
  onDragEnd,
  onDragEnter,
  onDragLeave,
  onReorder,
  onAgentDragStart,
  onAgentDragEnd,
  onAgentDragEnter,
  onAgentDragLeave,
  onAgentReorder,
  systemTreeNodes,
}: {
  active: boolean
  item: ProjectSnapshot
  agents: AgentInfo[]
  activeAgentId?: string | null
  expanded: boolean
  onToggleExpanded: (id: string) => void
  dragPosition: 'before' | 'after' | null
  isDragging: boolean
  agentDragState: { targetId: string | null; position: 'before' | 'after' | null; draggingId: string | null }
  onSelect: (id: string) => void
  onSelectAgent?: (agent: AgentInfo) => void
  onEditAgent?: (agent: AgentInfo) => void
  onOpenAgentMenu?: (agent: AgentInfo, x: number, y: number) => void
  onDeleteAgent?: (agent: AgentInfo) => void
  onCreateAgent?: (projectId: string) => void
  onOpenProjectMenu?: (project: ProjectSnapshot, x: number, y: number) => void
  onDragStart: (projectId: string) => void
  onDragEnd: () => void
  onDragEnter: (projectId: string, position: 'before' | 'after') => void
  onDragLeave: (projectId: string) => void
  onReorder: (orderedIds: string[]) => void
  onAgentDragStart: (agentId: string) => void
  onAgentDragEnd: () => void
  onAgentDragEnter: (agentId: string, position: 'before' | 'after') => void
  onAgentDragLeave: (agentId: string) => void
  onAgentReorder: (orderedIds: string[]) => void
  systemTreeNodes?: SystemTreeNode[]
}) {
  const { t } = useI18n()
  const displayName = projectDisplayName(item, t)
  const monoState = useMonoStore()
  const open = expanded
  const [projectCards, setProjectCards] = useState<MonoCardListItem[] | null>(null)

  useEffect(() => {
    if (!open) return
    let cancelled = false
    wikiClient.wikiListCards(client, { Flat: true, IncludeRaw: false, Limit: -1 }, { target: item.ProjectID })
      .then(resp => {
        if (!cancelled) setProjectCards(resp.Cards.map(mapServerListItem))
      })
      .catch(() => {
        if (!cancelled) setProjectCards([])
      })
    return () => { cancelled = true }
  }, [open, item.ProjectID])

  // Prefer live store data so tag changes (e.g. adding/removing toc) reflect immediately
  const storeHasProject = monoState.projectId === item.ProjectID
  const rawMonoCards = (storeHasProject && monoState.cards.length > 0) ? monoState.cards : (projectCards ?? [])
  const allCards = useMemo(
    () => rawMonoCards.map(c => overrideBuiltinTitle(c, t)),
    [rawMonoCards, t],
  )

  const agentForest = useMemo(() => buildAgentForest(agents), [agents])
  const flatTreeNodes = useMemo(() => flattenAgentForest(agentForest), [agentForest])

  const agentItems = agents.length === 0 ? (
    <div className="ai-sidebar-category-empty">No agents</div>
  ) : flatTreeNodes.map(node => {
    const isRoot = node.depth === 0
    const treeGuides = node.depth > 0 ? <AgentTreeGuides node={node} /> : null

    // Persistent agent in the list — render full ProjectAgentItem.
    // Only root-level agents participate in drag-and-drop reordering;
    // nested children have noDrag to avoid messing up tree structure.
    return (
      <div key={node.key} className={`ai-sidebar-agent-tree-row${isRoot ? '' : ' nested'}`}>
        {treeGuides}
        <div className="ai-sidebar-agent-tree-content">
          <ProjectAgentItem
            active={activeAgentId === node.agent.Id}
            item={node.agent}
            allCards={allCards}
            dragPosition={isRoot ? (agentDragState.targetId === node.agent.Id ? agentDragState.position : null) : null}
            isDragging={isRoot ? agentDragState.draggingId === node.agent.Id : false}
            noDrag={!isRoot}
            onSelect={onSelectAgent ?? (() => {})}
            onEdit={onEditAgent}
            onOpenMenu={onOpenAgentMenu}
            onDelete={onDeleteAgent}
            onDragStart={onAgentDragStart}
            onDragEnd={onAgentDragEnd}
            onDragEnter={onAgentDragEnter}
            onDragLeave={onAgentDragLeave}
            onReorder={onAgentReorder}
          />
        </div>
      </div>
    )
  })

  const showCreateAgentButton = !!onCreateAgent

  const createAgentButton = (
    <button
      type="button"
      className="ai-sidebar-create-agent-empty"
      data-guide-id="sidebar.new-agent"
      title={t('shell.sidebar.createAgent')}
      onClick={() => onCreateAgent!(item.ProjectID)}
    >
      <span className="ai-sidebar-create-agent-circle">
        <Plus size={10} />
      </span>
      <span className="ai-sidebar-create-agent-label">{t('shell.sidebar.createAgent')}</span>
    </button>
  )

  return (
    <div className={`ai-sidebar-menu-group${open ? ' expanded' : ''}`}>
      <div
        className={`ai-sidebar-project-item${active ? ' active' : ''}${isDragging ? ' dragging' : ''}${item.System ? ' system-meta-project' : ''}${dragPosition ? ` drag-${dragPosition}` : ''}`}
        draggable={!item.System}
        role="button"
        tabIndex={0}
        onDragStart={(event) => {
          if (item.System) {
            event.preventDefault()
            return
          }
          event.dataTransfer.setData('text/project-id', item.ProjectID)
          event.dataTransfer.effectAllowed = 'move'
          onDragStart(item.ProjectID)
        }}
        onDragEnd={() => onDragEnd()}
        onDragOver={(event) => {
          if (item.System) return
          if (!Array.from(event.dataTransfer.types).includes('text/project-id')) return
          event.preventDefault()
          const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
          const position = computeDragPosition(event.clientY, rect, dragPosition)
          onDragEnter(item.ProjectID, position)
        }}
        onDragLeave={(event) => {
          if (item.System) return
          if (!event.currentTarget.contains(event.relatedTarget as Node)) {
            onDragLeave(item.ProjectID)
          }
        }}
        onDrop={(event) => {
          if (item.System) return
          event.preventDefault()
          event.stopPropagation()
          const draggedId = event.dataTransfer.getData('text/project-id')
          const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
          const position = computeDragPosition(event.clientY, rect, dragPosition)
          if (draggedId && draggedId !== item.ProjectID) {
            onReorder([draggedId, item.ProjectID, position])
          }
          onDragLeave(item.ProjectID)
        }}
        onClick={() => {
          onSelect(item.ProjectID)
          if (!open) onToggleExpanded(item.ProjectID)
        }}
        onContextMenu={(e) => {
          e.preventDefault()
          e.stopPropagation()
          onOpenProjectMenu?.(item, e.clientX, e.clientY)
        }}
      >
        <span className="ai-sidebar-project-item-main">
          <button
            type="button"
            className="ai-sidebar-project-icon-toggle"
            aria-expanded={open}
            title={displayName}
            onClick={(e) => {
              e.stopPropagation()
              onToggleExpanded(item.ProjectID)
            }}
          >
            {projectTypeIcon(open)}
          </button>
          <span className="ai-sidebar-project-name">{displayName}</span>
        </span>
        <span className="ai-sidebar-project-actions">
          {!open && agents.length > 0 ? (
            <span className="ai-sidebar-project-count">{agents.length}</span>
          ) : null}
          <PinButton
            item={{
              id: `project:${item.ProjectID}`,
              projectId: item.ProjectID,
              type: 'project',
              targetId: item.ProjectID,
              label: displayName,
            }}
          />
        </span>
      </div>
      {open ? (
        <div className="ai-sidebar-menu-children ai-sidebar-project-menu">
          {agents.length === 0 && showCreateAgentButton ? createAgentButton : agentItems}
          {agents.length > 0 && showCreateAgentButton && createAgentButton}

          {item.System && systemTreeNodes && systemTreeNodes.length > 0 && (
            <>
              {systemTreeNodes.map(node => (
                <SystemNodeItem key={node.Id} node={node} />
              ))}
            </>
          )}

        </div>
      ) : null}
    </div>
  )
}
function agentKindBadge(kind: string): React.ReactNode {
  switch (kind.toLowerCase()) {
    case 'architect':
      return <span className="ai-sidebar-agent-kind-badge architect" title="Architect" aria-label="Architect"><Compass size={11} /></span>
    case 'coordinator':
      return <span className="ai-sidebar-agent-kind-badge coordinator" title="Coordinator" aria-label="Coordinator"><ConciergeBell size={11} /></span>
    case 'plugin':
      return <span className="ai-sidebar-agent-kind-badge plugin" title="Plugin Agent" aria-label="Plugin Agent"><Puzzle size={11} /></span>
    default:
      return null
  }
}

/** Task-mode badge for an agent bound to a scheduler (task) card. Shows the
 *  task's runtime mode (prompt / template), mirroring the scheduled-task list
 *  badge. Returns null for non-scheduler bound cards. */
function taskModeBadge(card: MonoCardListItem | undefined, t: ReturnType<typeof useI18n>['t']): React.ReactNode {
  if (!card || card.type !== 'scheduler') return null
  const data = (card.data ?? {}) as Record<string, unknown>
  const templateId = typeof data.workflow_template === 'string' ? data.workflow_template : ''
  const mode = resolveTaskMode(typeof data.task_mode === 'string' ? data.task_mode : undefined, templateId || undefined)
  const label = mode === 'template' ? t('scheduled.taskMode.template') : t('scheduled.taskMode.prompt')
  return (
    <span className={`ai-sidebar-task-mode-badge ${mode}`} title={label} aria-label={label}>
      {mode === 'template' ? <Waypoints size={11} /> : <MessageSquare size={11} />}
      <span className="ai-sidebar-task-mode-badge-label">{label}</span>
    </span>
  )
}

function cardTitleById(cards: MonoCardListItem[] | undefined, cardId: string | undefined): string | undefined {
  if (!cardId || !cards) return undefined
  const card = cards.find(c => c.id === cardId)
  if (!card) return undefined
  // Built-in cards get a localized title injected by overrideBuiltinTitle;
  // regular cards store their title in data.title.
  return (card as MonoCardListItem & { title?: string }).title || (card.data?.title as string | undefined) || card.id
}

/** Render L-shaped tree guide cells for a tree node at depth > 0. */
function AgentTreeGuides({ node }: { node: AgentTreeNode }) {
  return (
    <span className="ai-sidebar-agent-tree-guides" aria-hidden="true">
      {node.guideLines.map((_, i) => <span key={i} className="ai-sidebar-tree-guide" />)}
      <span className={`ai-sidebar-tree-connector ${node.isLastChild ? 'last' : 'mid'}`} />
    </span>
  )
}

function ProjectAgentItem({
  active,
  item,
  allCards,
  dragPosition,
  isDragging,
  noDrag,
  onSelect,
  onEdit,
  onOpenMenu,
  onDelete,
  onDragStart,
  onDragEnd,
  onDragEnter,
  onDragLeave,
  onReorder,
}: {
  active: boolean
  item: AgentInfo
  allCards?: MonoCardListItem[]
  dragPosition: 'before' | 'after' | null
  isDragging: boolean
  noDrag?: boolean
  onSelect: (agent: AgentInfo) => void
  onEdit?: (agent: AgentInfo) => void
  onOpenMenu?: (agent: AgentInfo, x: number, y: number) => void
  onDelete?: (agent: AgentInfo) => void
  onDragStart: (agentId: string) => void
  onDragEnd: () => void
  onDragEnter: (agentId: string, position: 'before' | 'after') => void
  onDragLeave: (agentId: string) => void
  onReorder: (orderedIds: string[]) => void
}) {
  const hue = avatarHue(item.Id)
  const working = item.IsWorking
  const relativeTime = useRelativeTime()
  const now = useNow()
  const { t } = useI18n()
  const activeMapName = useMemo(() => cardTitleById(allCards, item.ActiveWorkflowMapCardId), [allCards, item.ActiveWorkflowMapCardId])
  const boundTaskName = useMemo(() => cardTitleById(allCards, item.BoundTaskCardId), [allCards, item.BoundTaskCardId])
  const boundTaskCard = useMemo(() => allCards?.find(c => c.id === item.BoundTaskCardId), [allCards, item.BoundTaskCardId])

  // Worktree badge click jumps to git mode at the agent's HEAD commit without
  // selecting the agent. stopPropagation keeps the session row select handler
  // (click + keyboard) from firing.
  const handleWorktreeJump = useCallback((e: React.MouseEvent | React.KeyboardEvent) => {
    if ('key' in e && e.key !== 'Enter' && e.key !== ' ') return
    e.preventDefault()
    e.stopPropagation()
    void jumpToWorktreeGit(item.ProjectId, item.WorktreeID)
  }, [item])

  const badge = useMemo(() => {
    if (item.IsError) {
      return (
        <span className="ai-sidebar-session-badge error" aria-label="error" title={item.ErrorMessage || 'error'}>
          <AlertCircle size={6} strokeWidth={2.5} />
        </span>
      )
    }
    if (item.IsPlanApproval) {
      return (
        <span className="ai-sidebar-session-badge plan-approval" aria-label="plan approval">
          <FileText size={5} strokeWidth={2.5} />
        </span>
      )
    }
    if (item.IsGoalSubmit) {
      return (
        <span className="ai-sidebar-session-badge goal-submit" aria-label="goal submit">
          <Target size={5} strokeWidth={2.5} />
        </span>
      )
    }
    if (item.IsAskUser) {
      return (
        <span className="ai-sidebar-session-badge ask-user" aria-label="ask user">
          <CircleHelp size={6} strokeWidth={2.5} />
        </span>
      )
    }
    if (item.IsAskPermission) {
      return (
        <span className="ai-sidebar-session-badge ask-permission" aria-label="ask permission">
          <Lock size={5} strokeWidth={2.5} />
        </span>
      )
    }
    if (hasUnreadCompletion(item, now)) {
      return (
        <span className="ai-sidebar-session-badge completed" aria-label="completed">
          <Check size={6} strokeWidth={3} />
        </span>
      )
    }
    return null
  }, [item, now])

  const itemClass = [
    'ai-sidebar-session-item',
    active ? 'active' : '',
    item.IsWorking ? 'working' : '',
    item.Status === 'paused' ? 'paused' : '',
    item.IsError ? 'error' : '',
    item.IsPlanApproval ? 'plan-approval' : '',
    item.Status === 'goal_submit' ? 'goal-submit' : '',
    item.Status === 'ask_user' ? 'ask-user' : '',
    item.Status === 'ask_permission' ? 'ask-permission' : '',
    isDragging ? 'dragging' : '',
    dragPosition ? ` drag-${dragPosition}` : '',
  ].filter(Boolean).join(' ')

  const avatarClass = [
    'ai-sidebar-session-avatar',
    working ? 'working' : '',
    (item.AgentKind ?? '').toLowerCase() === 'coordinator' ? 'coordinator' : '',
    item.LoadState !== 'loaded' ? 'unloaded' : '',
  ].filter(Boolean).join(' ')

  const handleOpenMenu = (agent: AgentInfo, x: number, y: number) => {
    if (onOpenMenu) {
      onOpenMenu(agent, x, y)
    } else if (onEdit) {
      onEdit(agent)
    }
  }

  const longPress = useLongPress({
    onLongPress: (e) => handleOpenMenu(item, e.clientX, e.clientY),
  })

  return (
    <div className="ai-sidebar-agent-wrapper">
      <div
        role="button"
        tabIndex={0}
        draggable={!noDrag}
        className={itemClass}
        onClick={() => { if (!longPress.wasLongPress()) onSelect(item) }}
        onMouseDown={(e) => { if (longPress.wasLongPress()) { e.preventDefault(); e.stopPropagation() } }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            onSelect(item)
          } else if (e.key === 'Delete' && item.CanDelete) {
            e.preventDefault()
            onDelete?.(item)
          }
        }}
        onDragStart={(event) => {
          if (noDrag) return
          event.dataTransfer.setData('text/agent-id', item.Id)
          event.dataTransfer.effectAllowed = 'move'
          onDragStart(item.Id)
        }}
        onDragEnd={() => { if (!noDrag) onDragEnd() }}
        onDragOver={(event) => {
          if (noDrag) return
          if (!Array.from(event.dataTransfer.types).includes('text/agent-id')) return
          event.preventDefault()
          const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
          const position = computeDragPosition(event.clientY, rect, dragPosition)
          onDragEnter(item.Id, position)
        }}
        onDragLeave={(event) => {
          if (noDrag) return
          if (!event.currentTarget.contains(event.relatedTarget as Node)) {
            onDragLeave(item.Id)
          }
        }}
        onDrop={(event) => {
          if (noDrag) return
          event.preventDefault()
          event.stopPropagation()
          const draggedId = event.dataTransfer.getData('text/agent-id')
          const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
          const position = computeDragPosition(event.clientY, rect, dragPosition)
          if (draggedId && draggedId !== item.Id) {
            onReorder([draggedId, item.Id, position])
          }
          onDragLeave(item.Id)
        }}
        onContextMenu={(e) => {
          if (longPress.wasLongPress()) {
            e.preventDefault()
            return
          }
          e.preventDefault()
          e.stopPropagation()
          handleOpenMenu(item, e.clientX, e.clientY)
        }}
        onPointerDown={longPress.onPointerDown}
        onPointerMove={longPress.onPointerMove}
        onPointerUp={longPress.onPointerUp}
        onPointerLeave={longPress.onPointerLeave}
        onPointerCancel={longPress.onPointerCancel}
      >
        <span className={avatarClass} style={{ '--avatar-hue': hue } as React.CSSProperties}>
          <AgentAvatarContent agent={item} size={15} pauseSize={8} />
          {badge}
        </span>
        <span className="ai-sidebar-session-title">{sidebarAgentLabel(item)}</span>
        <span className="ai-sidebar-session-meta">
          {agentKindBadge(item.AgentKind)}
          {taskModeBadge(boundTaskCard, t)}
          {item.ActiveWorkflowMapCardId ? (
            <span
              className="ai-sidebar-workflow-icon waypoints"
              title={activeMapName || item.ActiveWorkflowMapCardId}
              aria-label="orchestrating workflow"
            >
              <Waypoints size={12} />
            </span>
          ) : null}
          {item.MemoryMounted ? (
            <span
              className="ai-sidebar-workflow-icon memory"
              title={t('shell.sidebar.memoryMounted')}
              aria-label="memory mounted"
            >
              <Brain size={12} />
            </span>
          ) : null}
          {item.WorktreeID ? (
            <button
              type="button"
              className="ai-sidebar-workflow-icon worktree"
              title={item.WorktreeName || item.WorktreeID}
              aria-label="worktree isolated"
              onClick={handleWorktreeJump}
              onKeyDown={handleWorktreeJump}
            >
              <GitBranch size={12} />
            </button>
          ) : null}
          {item.BoundTaskCardId ? (
            <span
              className="ai-sidebar-workflow-icon target"
              title={boundTaskName || item.BoundTaskCardId}
              aria-label="executing task"
            >
              <Target size={12} />
            </span>
          ) : null}
          {item.Degraded ? (
            <span
              className="ai-sidebar-session-degraded"
              title={item.DegradedReason ? `agent degraded: ${item.DegradedReason}` : 'agent degraded'}
            >!</span>
          ) : null}
          {item.LastTurnCompletedAt || item.LastActivity ? (
            <span className="ai-sidebar-session-time" title={item.LastTurnCompletedAt || item.LastActivity}>
              {relativeTime(item.LastTurnCompletedAt || item.LastActivity)}
            </span>
          ) : null}
          <PinButton
            className="ai-sidebar-session-star"
            item={{
              id: `agent:${item.ProjectId}:${item.Id}`,
              projectId: item.ProjectId,
              type: 'agent',
              targetId: item.Id,
              label: agentDisplayName(item.Title, item.DisplayName),
              agentInfo: item,
            }}
          />
        </span>
      </div>
    </div>
  )
}

function LanguageSwitch() {
  const { t, locale, setLocale } = useI18n()
  return (
    <select
      className="ai-sidebar-account-popover-select"
      aria-label={t('shell.sidebar.language')}
      value={locale}
      onChange={event => {
        const next = event.target.value as Locale
        if (supportedLocales.includes(next)) {
          setLocale(next)
          void persistLocale(next)
        }
      }}
    >
      {supportedLocales.map(loc => (
        <option key={loc} value={loc}>
          {localeLabels[loc]}
        </option>
      ))}
    </select>
  )
}

export function SidebarAccountMenu({
  account,
  onOpenSettings,
}: {
  account: AccountSnapshot | null
  onOpenSettings?: () => void
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const [popoverStyle, setPopoverStyle] = useState<{
    left: number
    bottom: number
    width: number
  } | null>(null)

  // The popover is a floating overlay above the embedded browser area, so it
  // must be registered with the BrowserOverlayManager (0→1 hides native panes).
  useBrowserOverlay(open)

  // The collapsed button reflects the cloud linkage: avatar + cloud display
  // name when linked, "not signed in" otherwise. The local account name
  // (e.g. admin) stays as the secondary line.
  const [cloud, setCloud] = useState<CloudAccountStatus | null>(null)
  const [loginOpen, setLoginOpen] = useState(false)
  const [cloudBusy, setCloudBusy] = useState(false)
  const [syncBusy, setSyncBusy] = useState(false)
  // Set when a manual resync either throws or comes back still degraded
  // (EntitlementsDegraded), so the menu can show an inline failure notice
  // instead of silently leaving the yellow text unchanged.
  const [syncFailed, setSyncFailed] = useState(false)

  const refreshCloud = useCallback(async () => {
    try {
      const status = await cloudaccount.status(client)
      setCloud(status)
      if (!status.EntitlementsDegraded) setSyncFailed(false)
    } catch {
      setCloud(null)
    }
    void refreshInsiderAccess()
  }, [])

  useEffect(() => {
    void refreshCloud()
  }, [refreshCloud])

  // Manual cloud resync: the collapsed button / popover may show degraded
  // (stale entitlements); cloudaccount.sync forces an immediate re-fetch so
  // the tier recovers without waiting for the actor's background tick.
  const handleCloudResync = useCallback(async () => {
    if (!cloud?.Linked) return
    setSyncBusy(true)
    setSyncFailed(false)
    try {
      const status = await cloudaccount.sync(client)
      setCloud(status)
      // The actor's sync never throws on a cloud error (it keeps the stale
      // degraded status), so a still-degraded response is the failure signal
      // the user must see — otherwise the yellow text just sits there.
      setSyncFailed(status.EntitlementsDegraded === true)
      void refreshInsiderAccess()
    } catch {
      setSyncFailed(true)
    } finally {
      setSyncBusy(false)
    }
  }, [cloud?.Linked])

  const handleCloudLogout = useCallback(async () => {
    setCloudBusy(true)
    try {
      await cloudaccount.unlink(client)
      await refreshCloud()
    } catch {
      // surfaced via the settings cloud-account section; keep the menu quiet
    } finally {
      setCloudBusy(false)
    }
  }, [refreshCloud])

  // Session recovery: when the refresh token is dead or revoked, resync keeps
  // failing and the account stays permanently degraded. Give the user a single
  // action that drops the stale session and lands directly in the login flow
  // (instead of hunting for "log out" and then "sign in" separately).
  const handleCloudReconnect = useCallback(async () => {
    if (!cloud?.Linked) return
    setCloudBusy(true)
    setSyncFailed(false)
    try {
      await cloudaccount.unlink(client)
      await refreshCloud()
    } catch {
      // Even if unlinking fails, still route the user to the sign-in surface;
      // a successful link overwrites the stale tokens.
    } finally {
      setCloudBusy(false)
    }
    setOpen(false)
    if (isWails()) setLoginOpen(true)
  }, [cloud?.Linked, refreshCloud])

  const updatePopoverPosition = useCallback(() => {
    if (!menuRef.current) return
    const rect = menuRef.current.getBoundingClientRect()
    const width = Math.min(260, window.innerWidth - 32)
    let left = rect.left
    const minLeft = 16
    const maxLeft = window.innerWidth - width - 16
    if (left < minLeft) left = minLeft
    if (left > maxLeft) left = maxLeft
    setPopoverStyle({
      left,
      bottom: window.innerHeight - rect.top + 8,
      width,
    })
  }, [])

  useLayoutEffect(() => {
    if (!open) return
    updatePopoverPosition()
  }, [open, updatePopoverPosition])

  useEffect(() => {
    if (!open) return
    const handleResize = () => updatePopoverPosition()
    const handleScroll = () => updatePopoverPosition()
    window.addEventListener('resize', handleResize)
    window.addEventListener('scroll', handleScroll, true)
    return () => {
      window.removeEventListener('resize', handleResize)
      window.removeEventListener('scroll', handleScroll, true)
    }
  }, [open, updatePopoverPosition])

  useEffect(() => {
    if (!open) return
    const handlePointerDown = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handlePointerDown)
    return () => document.removeEventListener('mousedown', handlePointerDown)
  }, [open])

  return (
    <div className="ai-sidebar-account-menu" ref={menuRef}>
      {open ? (
        <div
          className="ai-sidebar-account-popover"
          role="menu"
          style={
            popoverStyle
              ? {
                  position: 'fixed',
                  left: popoverStyle.left,
                  bottom: popoverStyle.bottom,
                  width: popoverStyle.width,
                }
              : undefined
          }
        >
          {cloud?.Linked ? (
            <>
              <div className="ai-sidebar-account-cloud">
                <SidebarCloudAvatar url={cloud.AvatarUrl} />
                <div className="ai-sidebar-account-cloud-text">
                  <div className="ai-sidebar-account-cloud-name">{cloud.DisplayName || t('settings.cloudAccount.unknownUser')}</div>
                  <div className={`ai-sidebar-account-cloud-sub${cloud.EntitlementsDegraded ? ' degraded' : ''}`}>
                    {cloud.EntitlementsDegraded ? t('shell.sidebar.cloudDegraded') : t('shell.sidebar.cloudSection')}
                  </div>
                </div>
              </div>
              {cloud.EntitlementsDegraded && (
                <ClickableMenuItem
                  icon={syncBusy ? undefined : <RefreshCw size={14} />}
                  label={t('shell.sidebar.cloudSync')}
                  role="menuitem"
                  disabled={syncBusy}
                  onClick={() => { if (!syncBusy) void handleCloudResync() }}
                />
              )}
              {syncFailed && (
                <div className="ai-sidebar-account-cloud-failed" role="alert">
                  <AlertCircle size={12} />
                  <span>{t('shell.sidebar.cloudSyncFailed')}</span>
                </div>
              )}
              {cloud.EntitlementsDegraded && (
                <ClickableMenuItem
                  icon={<LogIn size={14} />}
                  label={t('shell.sidebar.cloudReconnect')}
                  role="menuitem"
                  disabled={cloudBusy}
                  onClick={() => { if (!cloudBusy) void handleCloudReconnect() }}
                />
              )}
              <ClickableMenuItem
                icon={<LogOut size={14} />}
                label={t('shell.sidebar.logout')}
                role="menuitem"
                onClick={() => { if (!cloudBusy) void handleCloudLogout() }}
              />
            </>
          ) : (
            <ClickableMenuItem
              icon={<Cloud size={14} />}
              label={t('shell.sidebar.loginCloud')}
              role="menuitem"
              onClick={() => { if (isWails()) setLoginOpen(true) }}
            />
          )}
          <div className="ai-sidebar-account-popover-separator" />
          <ClickableMenuItem icon={<CircleHelp size={14} />} label={t('shell.sidebar.helpFeedback')} role="menuitem" />
          <div className="ai-sidebar-account-popover-separator" />
          <div className="ai-sidebar-account-popover-section-label"><Languages size={12} />{t('shell.sidebar.language')}</div>
          <div className="ai-sidebar-account-popover-switch">
            <LanguageSwitch />
          </div>
          <div className="ai-sidebar-account-popover-separator" />
          <ClickableMenuItem icon={<Settings size={14} />} label={t('shell.sidebar.settings')} role="menuitem" onClick={onOpenSettings} />
        </div>
      ) : null}
      {(() => {
        const degraded = !!cloud?.Linked && !!cloud.EntitlementsDegraded
        return (
          <div className={`ai-sidebar-account-button${degraded ? ' degraded' : ''}`}>
            <button
              type="button"
              className="ai-sidebar-account-main"
              aria-haspopup="menu"
              aria-expanded={open}
              aria-label={degraded ? t('shell.sidebar.cloudDegradedAction') : undefined}
              onClick={() => setOpen(value => !value)}
            >
              {cloud?.Linked ? (
                <SidebarCloudAvatar url={cloud.AvatarUrl} />
              ) : (
                <span className="ai-sidebar-account-icon"><UserRound size={16} /></span>
              )}
              <span className="ai-sidebar-account-copy">
                <span className="ai-sidebar-account-name">
                  {cloud?.Linked
                    ? cloud.DisplayName || t('settings.cloudAccount.unknownUser')
                    : t('shell.sidebar.notLoggedIn')}
                </span>
                <span
                  className={`ai-sidebar-account-status ${cloud?.Linked ? (cloud.EntitlementsDegraded ? 'degraded' : 'online') : 'offline'}`}
                  title={cloud?.Linked && cloud.EntitlementsDegraded ? t('shell.sidebar.cloudDegraded') : undefined}
                >
                  {accountLabel(account)}
                </span>
              </span>
              <ChevronDown size={14} className="ai-sidebar-account-action" />
            </button>
            {degraded && (
              <button
                type="button"
                className="ai-sidebar-account-reconnect"
                title={t('shell.sidebar.cloudReconnect')}
                aria-label={t('shell.sidebar.cloudReconnect')}
                disabled={cloudBusy}
                onClick={event => {
                  event.stopPropagation()
                  if (!cloudBusy) void handleCloudReconnect()
                }}
              >
                <Unplug size={13} />
              </button>
            )}
          </div>
        )
      })()}
      <CloudLoginOverlay
        open={loginOpen}
        onClose={() => setLoginOpen(false)}
        onLinked={st => setCloud(st)}
      />
    </div>
  )
}

function SidebarCloudAvatar({ url }: { url?: string }) {
  const [failed, setFailed] = useState(false)
  if (!url || failed) {
    return (
      <span className="ai-sidebar-account-cloud-avatar placeholder">
        <Cloud size={14} />
      </span>
    )
  }
  return <img className="ai-sidebar-account-cloud-avatar" src={url} alt="" onError={() => setFailed(true)} />
}

export const AIShellSidebar: React.FC<AIShellSidebarProps> = ({
  projects,
  systemTreeNodes,
  activeProject,
  projectAgents,
  activeConversationId,
  onOpenCreateProject,
  onSelectProject,
  onReorderProjects,
  onReorderAgents,
  onAgentClick,
  globalAgents,
  pluginAgentGroups,
  onSelectCoordinator,
  onEditAgent,
  onOpenAgentMenu,
  onDeleteAgent,
  onCreateAgent,
  onNewChat,
  onOpenProjectMenu,
  account,
  theme,
  onThemeChange,
  contentMode,
  onContentModeChange,
  pinnedModes,
  onReorderPinnedModes,
  onTogglePinnedMode,
  activeKbLane,
  onOpenSettings,
  onOpenMobileSync,
  onShellModeChange,
  onOpenFile,
  onOpenOmnibox,
  launcherMode,
  onLauncherModeChange,
  isMobile,
}) => {
  const { t } = useI18n()
  const [dragTarget, setDragTarget] = useState<{ projectId: string; position: 'before' | 'after' } | null>(null)
  const [draggingId, setDraggingId] = useState<string | null>(null)
  const [agentDragTarget, setAgentDragTarget] = useState<{ agentId: string; position: 'before' | 'after' } | null>(null)
  const [agentDraggingId, setAgentDraggingId] = useState<string | null>(null)
  const [expandedProjectIds, setExpandedProjectIds] = useState<Set<string>>(new Set())
  const [expandedProjectsLoaded, setExpandedProjectsLoaded] = useState(false)
  // The system meta project is an internal storage backend (skill/wiki
  // directories), not a user-facing project node. Exclude it from the sidebar
  // project list entirely.
  const visibleProjects = useMemo(
    () => recentProjects(projects).filter(p => !p.System),
    [projects],
  )
  useEffect(() => {
    let cancelled = false
    setExpandedProjectsLoaded(false)
    void getExpandedProjectIds().then(ids => {
      if (cancelled) return
      setExpandedProjectIds(ids)
      setExpandedProjectsLoaded(true)
    })
    return () => { cancelled = true }
  }, [])
  useEffect(() => {
    monoStore.setProjectId(activeProject?.ProjectID ?? null)
    if (activeProject?.ProjectID) {
      monoStore.load()
      monoStore.loadOpenCards()
    }
  }, [activeProject?.ProjectID])

  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const searchContainerRef = useRef<HTMLDivElement>(null)
  const searchInputRef = useRef<HTMLInputElement>(null)

  const filteredVisibleProjects = useMemo(() => {
    if (!searchQuery.trim()) return visibleProjects
    return visibleProjects.filter(p => projectDisplayName(p, t).toLowerCase().includes(searchQuery.toLowerCase()) || (p.Name ?? '').toLowerCase().includes(searchQuery.toLowerCase()))
  }, [visibleProjects, searchQuery, t])

  const [sortMode, setSortMode] = useState<ProjectSortMode>('none')
  const [showFolders, setShowFolders] = useState(true)
  const [timeFilter, setTimeFilter] = useState<AgentTimeFilter>('all')
  const now = useNow()
  const agentPassesTimeFilter = useCallback(
    (agent: AgentInfo) => agentWithinTimeFilter(agent, timeFilter, now),
    [timeFilter, now],
  )

  const toggleProjectExpanded = useCallback((projectId: string) => {
    setExpandedProjectIds(prev => {
      const next = new Set(prev)
      if (next.has(projectId)) {
        next.delete(projectId)
      } else {
        next.add(projectId)
      }
      return next
    })
    if (expandedProjectsLoaded) void toggleExpandedProjectId(projectId)
  }, [expandedProjectsLoaded])

  const sortedVisibleProjects = useMemo(() => {
    if (sortMode === 'none') {
      return filteredVisibleProjects
    }

    const list = [...filteredVisibleProjects]
    list.sort((a, b) => {
      const cmp = a.Name.localeCompare(b.Name)
      return sortMode === 'name-desc' ? -cmp : cmp
    })

    return list
  }, [filteredVisibleProjects, sortMode])

  const handleSelectProject = useCallback((projectId: string) => {
    onSelectProject(projectId)
  }, [onSelectProject])

  useEffect(() => {
    if (searchOpen && searchInputRef.current) {
      searchInputRef.current.focus()
    }
  }, [searchOpen])

  useEffect(() => {
    if (!searchOpen) return
    const handlePointerDown = (event: MouseEvent) => {
      if (searchContainerRef.current && !searchContainerRef.current.contains(event.target as Node)) {
        setSearchOpen(false)
      }
    }
    document.addEventListener('mousedown', handlePointerDown)
    return () => document.removeEventListener('mousedown', handlePointerDown)
  }, [searchOpen])

  useEffect(() => {
    if (!searchOpen) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setSearchOpen(false)
      }
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [searchOpen])

  const renderPinnedItem = (item: PinnedItem): React.ReactNode => {
    if (item.type === 'project') {
      const project = projects.find(p => p.ProjectID === item.targetId)
      // The system meta project must never render, even via a stale pinned
      // item left in localStorage from before it was hidden from the UI.
      if (!project || project.System) {
        return null
      }
      const agents = (projectAgents ?? []).filter(a => a.ProjectId === project.ProjectID)
      return (
        <ProjectMenuItem
          item={project}
          active={project.ProjectID === activeProject?.ProjectID && !activeConversationId}
          agents={agents}
          activeAgentId={activeConversationId}
          expanded={expandedProjectIds.has(project.ProjectID)}
          onToggleExpanded={toggleProjectExpanded}
          dragPosition={dragTarget?.projectId === project.ProjectID ? dragTarget.position : null}
          isDragging={draggingId === project.ProjectID}
          agentDragState={{ targetId: agentDragTarget?.agentId ?? null, position: agentDragTarget?.position ?? null, draggingId: agentDraggingId }}
          onDragStart={(projectId) => setDraggingId(projectId)}
          onDragEnd={() => {
            setDraggingId(null)
            setDragTarget(null)
          }}
          onDragEnter={(projectId, position) => {
            setDragTarget(current => {
              if (current?.projectId === projectId && current?.position === position) return current
              return { projectId, position }
            })
          }}
          onDragLeave={(projectId) => {
            setDragTarget(current => current?.projectId === projectId ? null : current)
          }}
          onReorder={(orderedIds) => {
            setSortMode('none')
            onReorderProjects(orderedIds)
          }}
          onSelect={handleSelectProject}
          onSelectAgent={onAgentClick}
          onEditAgent={onEditAgent}
          onOpenAgentMenu={onOpenAgentMenu}
          onDeleteAgent={onDeleteAgent}
          onCreateAgent={onCreateAgent}
          onOpenProjectMenu={onOpenProjectMenu}
          onAgentDragStart={(agentId) => setAgentDraggingId(agentId)}
          onAgentDragEnd={() => {
            setAgentDraggingId(null)
            setAgentDragTarget(null)
          }}
          onAgentDragEnter={(agentId, position) => {
            setAgentDragTarget(current => {
              if (current?.agentId === agentId && current?.position === position) return current
              return { agentId, position }
            })
          }}
          onAgentDragLeave={(agentId) => {
            setAgentDragTarget(current => current?.agentId === agentId ? null : current)
          }}
          onAgentReorder={(orderedIds) => {
            setAgentDragTarget(null)
            setAgentDraggingId(null)
            setSortMode('none')
            onReorderAgents(orderedIds)
          }}
          systemTreeNodes={project.System ? systemTreeNodes : undefined}
        />
      )
    }

    const isActive = (() => {
      switch (item.type) {
        case 'agent':
          return item.targetId === activeConversationId
        case 'card':
          return contentMode === 'notes' && activeKbLane?.projectId === item.projectId && activeKbLane?.cardId === item.targetId
        case 'card-builtin':
          return contentMode === 'notes' && activeKbLane?.projectId === item.projectId && activeKbLane?.cardId === item.targetId
        default:
          return false
      }
    })()

    return (
      <SidebarPinnedItem
        item={item}
        onClick={() => {
          if (item.type !== 'card' && item.type !== 'card-builtin') onSelectProject(item.projectId)
          switch (item.type) {
            case 'agent':
              if (item.agentInfo) onAgentClick(item.agentInfo)
              break
            case 'card':
              onContentModeChange('notes', { cardId: item.targetId, projectId: item.projectId })
              break
            case 'card-builtin':
              onContentModeChange('notes', { cardId: item.targetId, projectId: item.projectId })
              break
            case 'file':
              if (!item.isDir) onOpenFile?.(item.targetId)
              break
            default:
              break
          }
        }}
        active={isActive}
      />
    )
  }

  return (
    <PinnedProvider>
      <aside className="ai-sidebar">
      <div className="ai-sidebar-fixed">
        <div className="ai-sidebar-function-section">
          <ModeSwitchButtons
            contentMode={contentMode}
            onContentModeChange={onContentModeChange}
            onShellModeChange={onShellModeChange}
            pinnedModes={pinnedModes}
            onReorderPinnedModes={onReorderPinnedModes}
            onTogglePinnedMode={onTogglePinnedMode}
            projects={projects}
            activeProject={activeProject}
            onCreateAgent={onCreateAgent}
            onNewChat={onNewChat}
            launcherMode={launcherMode}
            onLauncherModeChange={onLauncherModeChange}
            isMobile={isMobile}
          />
        </div>

        <SidebarPinnedSection
          renderItem={renderPinnedItem}
        />

        <div className="ai-sidebar-project-toolbar" ref={searchContainerRef}>
          <span className="ai-sidebar-project-toolbar-label" title={t('shell.sidebar.projects')}>
            <span className="ai-sidebar-project-toolbar-name">{t('shell.sidebar.projects')}</span>
          </span>
          {searchOpen ? (
            <div className="ai-sidebar-project-search-popover">
              <Search size={12} />
              <input
                ref={searchInputRef}
                type="search"
                placeholder={t('shell.sidebar.searchProject')}
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
              />
              <button
                type="button"
                className="ai-sidebar-project-search-close"
                title={t('shell.sidebar.closeSearch')}
                aria-label={t('shell.sidebar.closeSearch')}
                onClick={() => {
                  setSearchOpen(false)
                  setSearchQuery('')
                }}
              >
                <X size={12} />
              </button>
            </div>
          ) : null}
          <button type="button" className="ai-sidebar-toolbar-button" title={t('shell.sidebar.addProject')} aria-label={t('shell.sidebar.addProject')} onClick={onOpenCreateProject}>
            <FolderPlus size={13} />
          </button>
          <ProjectSortDropdown
            sortMode={sortMode}
            onSortModeChange={setSortMode}
            showFolders={showFolders}
            onToggleShowFolders={setShowFolders}
            timeFilter={timeFilter}
            onTimeFilterChange={setTimeFilter}
          />
          <button type="button" className="ai-sidebar-toolbar-button" title={t('shell.sidebar.searchProject')} aria-label={t('shell.sidebar.searchProject')} onClick={onOpenOmnibox ?? (() => setSearchOpen(true))}>
            <Search size={13} />
          </button>
        </div>
      </div>
      <div className="ai-sidebar-scroll">
        <div className="ai-sidebar-project-stack">
          {globalAgents.filter(agentPassesTimeFilter).map(agent => (
            <ProjectAgentItem
              key={agent.Id}
              active={activeConversationId === agent.Id}
              item={agent}
              dragPosition={null}
              isDragging={false}
              onSelect={onSelectCoordinator ?? onAgentClick}
              onEdit={onEditAgent}
              onOpenMenu={onOpenAgentMenu}
              onDelete={onDeleteAgent}
              onDragStart={() => {}}
              onDragEnd={() => {}}
              onDragEnter={() => {}}
              onDragLeave={() => {}}
              onReorder={() => {}}
            />
          ))}
          {showFolders ? (
            <>
              {pluginAgentGroups?.map(group => (
                <PluginAgentGroup
                  key={group.appId}
                  appId={group.appId}
                  label={group.label}
                  agents={group.agents.filter(agentPassesTimeFilter)}
                  activeAgentId={activeConversationId}
                  onSelect={onSelectCoordinator ?? onAgentClick}
                  onEdit={onEditAgent}
                  onOpenMenu={onOpenAgentMenu}
                  onDelete={onDeleteAgent}
                />
              ))}
              {sortedVisibleProjects.map(project => {
                const agents = (projectAgents ?? []).filter(item => item.ProjectId === project.ProjectID && agentPassesTimeFilter(item))
                if (timeFilter !== 'all' && agents.length === 0) return null
                return (
                  <ProjectMenuItem
                    key={project.ProjectID}
                    item={project}
                    active={project.ProjectID === activeProject?.ProjectID && !activeConversationId}
                    agents={agents}
                    activeAgentId={activeConversationId}
                    expanded={expandedProjectIds.has(project.ProjectID)}
                    onToggleExpanded={toggleProjectExpanded}
                    dragPosition={dragTarget?.projectId === project.ProjectID ? dragTarget.position : null}
                    isDragging={draggingId === project.ProjectID}
                    agentDragState={{ targetId: agentDragTarget?.agentId ?? null, position: agentDragTarget?.position ?? null, draggingId: agentDraggingId }}
                    onDragStart={(projectId) => setDraggingId(projectId)}
                    onDragEnd={() => {
                      setDraggingId(null)
                      setDragTarget(null)
                    }}
                    onDragEnter={(projectId, position) => {
                      setDragTarget(current => {
                        if (current?.projectId === projectId && current?.position === position) return current
                        return { projectId, position }
                      })
                    }}
                    onDragLeave={(projectId) => {
                      setDragTarget(current => current?.projectId === projectId ? null : current)
                    }}
                    onReorder={(orderedIds) => {
                      setSortMode('none')
                      onReorderProjects(orderedIds)
                    }}
                    onSelect={handleSelectProject}
                    onSelectAgent={onAgentClick}
                    onEditAgent={onEditAgent}
                    onOpenAgentMenu={onOpenAgentMenu}
                    onDeleteAgent={onDeleteAgent}
                    onCreateAgent={onCreateAgent}
                    onOpenProjectMenu={onOpenProjectMenu}
                    onAgentDragStart={(agentId) => setAgentDraggingId(agentId)}
                    onAgentDragEnd={() => {
                      setAgentDraggingId(null)
                      setAgentDragTarget(null)
                    }}
                    onAgentDragEnter={(agentId, position) => {
                      setAgentDragTarget(current => {
                        if (current?.agentId === agentId && current?.position === position) return current
                        return { agentId, position }
                      })
                    }}
                    onAgentDragLeave={(agentId) => {
                      setAgentDragTarget(current => current?.agentId === agentId ? null : current)
                    }}
                    onAgentReorder={(orderedIds) => {
                      setAgentDragTarget(null)
                      setAgentDraggingId(null)
                      setSortMode('none')
                      onReorderAgents(orderedIds)
                    }}
                    systemTreeNodes={project.System ? systemTreeNodes : undefined}
                  />
                )
              })}
            </>
          ) : (
            <FlatAgentList
              projects={sortedVisibleProjects}
              projectAgents={projectAgents ?? []}
              pluginAgentGroups={pluginAgentGroups ?? []}
              agentPassesTimeFilter={agentPassesTimeFilter}
              activeConversationId={activeConversationId}
              onSelect={onSelectCoordinator ?? onAgentClick}
              onEdit={onEditAgent}
              onOpenMenu={onOpenAgentMenu}
              onDelete={onDeleteAgent}
            />
          )}
        </div>
        <div className="ai-sidebar-tail" />
      </div>

      <div className="ai-sidebar-footer">
        <div className="ai-sidebar-footer-row">
          <SidebarAccountMenu
            account={account}
            onOpenSettings={onOpenSettings}
          />
          <div className="ai-sidebar-footer-actions">
            <button
              type="button"
              className="ai-sidebar-footer-btn"
              title={t('shell.sidebar.themeToggle')}
              aria-label={t('shell.sidebar.themeToggle')}
              onClick={() => onThemeChange({ ...theme, mode: theme.mode === 'dark' ? 'light' : 'dark' })}
            >
              {theme.mode === 'dark' ? <Sun size={14} /> : <Moon size={14} />}
            </button>
            <button
              type="button"
              className="ai-sidebar-footer-btn"
              title={t('shell.sidebar.mobileSync')}
              aria-label={t('shell.sidebar.mobileSync')}
              onClick={onOpenMobileSync}
            >
              <Smartphone size={14} />
            </button>
            <button
              type="button"
              className="ai-sidebar-footer-btn"
              data-guide-id="topbar.settings"
              title={t('shell.sidebar.settings')}
              aria-label={t('shell.sidebar.settings')}
              onClick={onOpenSettings}
            >
              <Settings size={14} />
            </button>
          </div>
        </div>
      </div>
    </aside>
    </PinnedProvider>
  )
}
