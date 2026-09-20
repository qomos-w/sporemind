import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { FolderOpen, FolderPlus, ChevronDown, Loader2, Home, Megaphone, Waypoints, Target, Puzzle } from 'lucide-react'
import { MushroomIcon } from './MushroomIcon'
import { AIComposer, useDropdownVerticalFit, sortHistory } from './AIComposer'
import type { PermissionMode, ProviderOption, ProviderGroup } from './AIComposer'
import { useAgentOperations } from '../hooks/useAgentOperations'
import { useAgentComponentMounts } from '../hooks/useAgentComponentMounts'
import { useComposerHistory, COMPOSER_HISTORY_SCOPE } from '../hooks/useComposerHistory'
import { setComposerDraft } from '../hooks/composerDraftStore'
import { fetchRouteGroups } from '../hooks/useAIShellProviders'
import { SYSTEM_AGGREGATOR_ID } from '../hooks/modelSlot'
import type { SlotRoute } from '../hooks/modelSlot'
import { autoSlot, aggregatorSlot, pinnedUnitSlot, slotToRoute } from '../hooks/modelSlot'
import { client } from '../../../application/generated-client'
import * as workspace from '../../../gen-clients/workspace/client'
import * as aimanager_aggregator from '../../../gen-clients/aimanager/client'
import type { AggregatorDescriptor } from '../../../gen-types/aigen'
import type { ProjectSnapshot } from '../../../domain/types'
import type { ModelUnit } from '../../../domain/types'
import type { ModelSlot } from '../../../gen-clients/system/types'
import type { AgentKindOption } from './NewAgentDialog'
import type { AgentKindConfig } from '../../../gen-clients/system/types'
import { projectDisplayName } from '../../../application/project-adapter'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n/types'
import { AgentAvatarBar } from './AgentAvatarBar'
import { SuggestionChips } from './SuggestionChips'
import { useHasGitRepo } from '../hooks/useHasGitRepo'
import type { SuggestionContext } from './suggestions/suggestionRegistry'
import { greetingOnce } from '../lib/greetingPhrases'
import { agentDisplayName } from '../lib/agent-avatar'
import type { AgentInfo } from '../hooks/agentInfoStore'
import './ProjectNewChatPage.css'

const HOME_ID = '__home__'

/** Fallback composer prefill for each quick-start card when agent creation
 *  inputs (selected unit, coder kind) are missing. */
const QUICK_START_PROMPTS: Record<string, I18nKey> = {
  'builtin:mode:workflow': 'quickStart.workflow.prompt',
  'builtin:mode:goal': 'quickStart.goal.prompt',
  'builtin:bundle:plugin-dev': 'quickStart.plugin.prompt',
}

export interface ProjectNewChatPageProps {
  activeProject: ProjectSnapshot | null
  projects: ProjectSnapshot[]
  onProjectChange: (projectId: string) => void
  projectSwitchingId?: string | null
  contextLoading?: boolean
  agents: AgentInfo[]
  onAgentClick: (agent: AgentInfo) => void
  onAgentMenuOpen?: (agent: AgentInfo, x: number, y: number) => void
  onAgentCreated: (agentId: string, text: string, unit: ModelUnit, modeCardId?: string) => void
  onCreateAgent?: () => void
  onDeleteAgent?: (agent: AgentInfo) => void
  /** When set, the page shows a Home option in the selector and can switch to it. */
  coordinator?: AgentInfo | null
  /** Callback used in home mode to send a message to the coordinator. */
  onCoordinatorSend?: (text: string, unit: ModelUnit) => void
  /** Called when the user picks the Home option from the selector. */
  onSelectHome?: () => void
  /** Quick action shown when no project exists: create a new project. */
  onCreateProject?: () => void
  /** Quick action shown when no project exists: open an existing project folder. */
  onOpenProject?: () => void
  /** Active permission mode (home mode: the coordinator's mode). Providing
   *  `onPermissionModeChange` renders the permission selector, whose
   *  `data-guide-id="composer.permission-mode"` anchor is the onboarding
   *  tour's first target. */
  permissionMode?: PermissionMode
  onPermissionModeChange?: (mode: PermissionMode) => void
  globalPermissionMode?: PermissionMode
  onGlobalPermissionModeChange?: (mode: PermissionMode) => void
}

function buildAgentKindOption(config: AgentKindConfig): AgentKindOption {
  return {
    kind: config.Kind,
    displayName: config.DisplayName || config.Kind,
    randomName: config.RandomName,
    namePool: config.NamePool,
  }
}

interface ProjectOption {
  id: string
  label: string
  isHome?: boolean
}

interface QuickAction {
  key: string
  icon: React.ReactNode
  label: string
  color: string
  onClick: () => void
}

function ProjectSelector({
  value,
  options,
  disabled,
  loading,
  onChange,
}: {
  value: string
  options: ProjectOption[]
  disabled?: boolean
  loading?: boolean
  onChange: (id: string) => void
}) {
  const [open, setOpen] = useState(false)
  const selected = options.find(o => o.id === value)
  const containerRef = useRef<HTMLDivElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)

  useDropdownVerticalFit(open, dropdownRef, 'down')

  useEffect(() => {
    if (!open) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', handleKey)
    return () => window.removeEventListener('keydown', handleKey)
  }, [open])

  return (
    <div ref={containerRef} className="project-new-chat-project-selector">
      <button
        type="button"
        className="project-new-chat-project-selector-trigger"
        disabled={disabled || loading}
        onClick={() => setOpen(v => !v)}
      >
        {loading ? <Loader2 size={14} className="project-new-chat-project-selector-spin" /> : selected?.isHome ? <Home size={14} /> : <FolderOpen size={14} />}
        <span className="project-new-chat-project-selector-value">{selected?.label ?? 'Select'}</span>
        <ChevronDown size={12} />
      </button>
      {open && (
        <>
          <div className="project-new-chat-project-selector-backdrop" onClick={() => setOpen(false)} />
          <div className="project-new-chat-project-selector-dropdown" ref={dropdownRef}>
            {options.map(option => (
              <React.Fragment key={option.id}>
                {option.isHome && options.length > 1 && (
                  <div className="project-new-chat-project-selector-divider" />
                )}
                <button
                  type="button"
                  className={`project-new-chat-project-selector-item ${option.isHome ? 'home-item' : ''} ${option.id === value ? 'active' : ''}`}
                  onClick={() => {
                    onChange(option.id)
                    setOpen(false)
                  }}
                >
                  {option.isHome && <Home size={14} />}
                  <span>{option.label}</span>
                  {option.id === value && <ChevronDown size={14} style={{ transform: 'rotate(-90deg)' }} />}
                </button>
              </React.Fragment>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

export const ProjectNewChatPage: React.FC<ProjectNewChatPageProps> = ({
  activeProject,
  projects,
  onProjectChange,
  projectSwitchingId,
  contextLoading,
  agents,
  onAgentClick,
  onAgentMenuOpen,
  onAgentCreated,
  onCreateAgent,
  onDeleteAgent,
  coordinator,
  onCoordinatorSend,
  onSelectHome,
  onCreateProject,
  onOpenProject,
  permissionMode,
  onPermissionModeChange,
  globalPermissionMode,
  onGlobalPermissionModeChange,
}) => {
  const { t } = useI18n()
  const isHomeMode = !!coordinator
  const [value, setValue] = useState('')
  const [sending, setSending] = useState(false)
  const [groups, setGroups] = useState<ProviderGroup[]>([])
  const [loadingUnits, setLoadingUnits] = useState(false)
  const [agentKinds, setAgentKinds] = useState<AgentKindOption[]>([])
  const [aggregators, setAggregators] = useState<AggregatorDescriptor[]>([])
  // Local model selection state: the user picks a route (aggregator strategy)
  // or pins a concrete unit.  No agent exists yet to persist to, so this stays
  // in component state until the agent is created on send.
  const [slot, setSlot] = useState<ModelSlot | undefined>(undefined)
  // The aggregator-resolved current unit (β channel) — for the auto route we
  // show whatever the system aggregator is currently serving.
  const [currentUnit, setCurrentUnit] = useState<ModelUnit | null>(null)
  const agentOps = useAgentOperations()
  const { history, setHistory } = useComposerHistory(COMPOSER_HISTORY_SCOPE)
  // Favorited composer texts (most recent first) for the suggestion chips.
  const favoritePrompts = useMemo(
    () => sortHistory(history).filter(h => h.isFavorite).map(h => h.text),
    [history],
  )

  // Mode quick-start: home mode targets the coordinator; in project mode no
  // agent exists yet (one is created on send), so the composer's slash-mode
  // entry degrades to prefill-only. The page composer receives onEnterMode
  // directly and shares this component's scope.
  const modeAgentActorId = isHomeMode && coordinator ? coordinator.ActorId : null
  const { mount } = useAgentComponentMounts(modeAgentActorId)
  const handleEnterMode = useCallback((cardId: string) => {
    void mount(cardId)
    setValue('')
  }, [mount])

  // The coordinator is a long-lived global assistant: its DisplayName (the
  // nickname chosen at creation) is its stable identity. Its Title may carry
  // a stale per-conversation inference and must never become the greeting
  // name, so prefer DisplayName here.
  const coordinatorName = coordinator ? coordinator.DisplayName || coordinator.Title || 'Coordinator' : ''
  const greeting = useMemo(() => {
    if (!coordinator || !coordinatorName) return ''
    return greetingOnce(coordinatorName, t)
  }, [coordinator, coordinatorName, t])

  // Load aggregators and agent kinds once when the page mounts.
  useEffect(() => {
    let cancelled = false
    const load = async () => {
      try {
        const [kindsResp, configsResp, aggsResp] = await Promise.all([
          workspace.listAgentKinds(client),
          workspace.listAgentKindConfigs(client),
          aimanager_aggregator.aggregatorList(client),
        ])
        if (cancelled) return
        const configs = configsResp.Items ?? []
        const configsByKind = new Map(configs.map(c => [c.Kind, c]))
        const kinds = (kindsResp.Items ?? [])
          .map((info): AgentKindOption | null => {
            const cfg = configsByKind.get(info.Kind)
            if (!cfg || !cfg.UserCreatable) return null
            return buildAgentKindOption(cfg)
          })
          .filter((k): k is AgentKindOption => k !== null)
        setAgentKinds(kinds)
        setAggregators(aggsResp.Items)
      } catch {
        if (!cancelled) {
          setAgentKinds([])
          setAggregators([])
        }
      }
    }
    void load()
    return () => { cancelled = true }
  }, [])

  // Fetch all route groups (system/auto pool + every custom aggregator) so
  // the dropdown matches the conversation composer's two-level selector.
  useEffect(() => {
    let cancelled = false
    setLoadingUnits(true)
    fetchRouteGroups()
      .then(next => {
        if (cancelled) return
        setGroups(next)
        // Seed currentUnit from the first model in the auto group.
        const autoGroup = next.find(g => g.isAuto)
        setCurrentUnit(autoGroup?.models[0]?.unit ?? null)
      })
      .catch(() => { if (!cancelled) setGroups([]) })
      .finally(() => { if (!cancelled) setLoadingUnits(false) })
    return () => { cancelled = true }
  }, [])

  // Flat provider list for legacy AIComposer consumers (kept for parity, but
  // the grouped path is the primary display when groups are present).
  const providers = useMemo(() => {
    const autoGroup = groups.find(g => g.isAuto)
    return autoGroup?.models ?? []
  }, [groups])

  const activeRoute = useMemo<SlotRoute>(() => slotToRoute(slot), [slot])

  const activeUnit = useMemo<ModelUnit | null>(() => {
    if (activeRoute.kind === 'unit') return activeRoute.unit
    if (currentUnit && currentUnit.model) return currentUnit
    return null
  }, [activeRoute, currentUnit])

  const handleSelectRoute = useCallback((routeId: string) => {
    setSlot(routeId === SYSTEM_AGGREGATOR_ID ? autoSlot() : aggregatorSlot(routeId))
    // Reflect the selected route's first model as the β channel subtitle.
    const group = groups.find(g => g.routeId === routeId)
    setCurrentUnit(group?.models[0]?.unit ?? null)
  }, [groups])

  const handleSelectUnit = useCallback((routeId: string, option: ProviderOption) => {
    if (!option.unit) return
    setSlot(pinnedUnitSlot(option.unit, routeId))
  }, [])

  const primarySelection = useMemo(() => {
    if (activeRoute.kind === 'auto') return undefined
    if (activeRoute.kind === 'aggregator') return { type: 'aggregator' as const, aggregatorId: activeRoute.aggregatorId }
    const sel: { type: 'unit'; unit: ModelUnit; aggregatorId?: string } = {
      type: 'unit',
      unit: activeRoute.unit,
    }
    if (activeRoute.servingAggregatorId && activeRoute.servingAggregatorId !== SYSTEM_AGGREGATOR_ID) {
      sel.aggregatorId = activeRoute.servingAggregatorId
    }
    return sel
  }, [activeRoute])

  const handleSend = useCallback(async () => {
    const text = value.trim()
    if (!text || !activeUnit) return

    if (isHomeMode && onCoordinatorSend) {
      setSending(true)
      onCoordinatorSend(text, activeUnit)
      setValue('')
      return
    }

    if (!activeProject) return
    const coderKind = agentKinds.find(k => k.kind === 'coder')
    if (!coderKind) return

    setSending(true)
    try {
      const agent = await agentOps.createAgent({
        displayName: '', // server generates a random name
        agentKind: 'coder',
        projectId: activeProject.ProjectID,
        primarySelection,
      }, { agentKinds, aggregators })
      if (agent) {
        onAgentCreated(agent.Id, text, activeUnit)
      }
    } finally {
      setSending(false)
    }
  }, [value, activeUnit, primarySelection, agentKinds, aggregators, activeProject, agentOps, onAgentCreated, isHomeMode, onCoordinatorSend])

  const projectOptions = useMemo<ProjectOption[]>(() => {
    const items: ProjectOption[] = []
    if (coordinator) {
      items.push({ id: HOME_ID, label: t('onboarding.coordinator.home.selectorLabel'), isHome: true })
    }
    items.push(...projects.map(p => ({ id: p.ProjectID, label: projectDisplayName(p, t) })))
    return items
  }, [projects, t, coordinator])

  const handleSelectorChange = useCallback((id: string) => {
    if (id === HOME_ID) {
      onSelectHome?.()
    } else {
      onProjectChange(id)
    }
  }, [onProjectChange, onSelectHome])

  const selectorValue = isHomeMode ? HOME_ID : (activeProject?.ProjectID ?? '')

  // No-project mode: no active project to chat against — the page becomes a
  // landing surface with quick actions instead of a composer.
  const noProjectMode = !isHomeMode && !activeProject

  const { hasGitRepo } = useHasGitRepo(activeProject?.ProjectID)

  const ctx: SuggestionContext = {
    hasProject: !!activeProject,
    isHomeMode,
    hasGitRepo,
    permissionMode,
    favoritePrompts,
  }

  const title = isHomeMode
    ? (greeting || agentDisplayName(coordinator?.Title ?? '', coordinator?.DisplayName ?? ''))
    : noProjectMode
      ? t('shell.newChat.noProject.title')
      : projectDisplayName(activeProject!, t)
  const subtitle = isHomeMode
    ? t('onboarding.coordinator.home.subtitle').replace('{name}', coordinatorName)
    : noProjectMode
      ? t('shell.newChat.noProject.subtitle')
      : t('shell.newChat.subtitle')

  const handleSuggestionPick = useCallback((text: string) => {
    setValue(text)
    setTimeout(() => {
      const composer = document.querySelector('.ai-composer-textarea') as HTMLTextAreaElement | null
      composer?.focus()
    }, 0)
  }, [])

  // Quick-mode cards (workflow/goal/plugin-dev): create the agent immediately
  // and mount the mode badge or plugin-dev tool bundle on it — same result as a
  // fresh agent's welcome row, where the actor already exists. Falls back to
  // prompt pre-fill when the inputs needed to create an agent (selected unit,
  // coder kind) are missing.
  const handleQuickModeStart = useCallback(async (cardId: string) => {
    const coderKind = agentKinds.find(k => k.kind === 'coder')
    if (!activeUnit || !activeProject || !coderKind || sending) {
      handleSuggestionPick(t(QUICK_START_PROMPTS[cardId] ?? 'quickStart.goal.prompt'))
      return
    }
    setSending(true)
    try {
      const agent = await agentOps.createAgent({
        displayName: '', // server generates a random name
        agentKind: 'coder',
        projectId: activeProject.ProjectID,
        primarySelection,
      }, { agentKinds, aggregators })
      if (agent) {
        // Keep whatever the user already typed: the page composer is about to
        // unmount, so seed the new agent's per-agent draft — SELECT_SESSION
        // restores it into the conversation composer.
        if (value.trim()) setComposerDraft(agent.Id, value)
        onAgentCreated(agent.Id, '', activeUnit, cardId)
      }
    } finally {
      setSending(false)
    }
  }, [activeUnit, activeProject, agentKinds, aggregators, primarySelection, agentOps, onAgentCreated, sending, handleSuggestionPick, t, value])

  // Quick actions live above the composer. Project management cards serve the
  // landing page and the coordinator home. The project new-chat mode swaps in
  // workflow/goal/plugin-dev quick-start cards, which create the agent on click
  // and mount the mode badge or plugin-dev bundle on it (the conversation
  // composer takes over from there).
  const quickActions: QuickAction[] = []
  if (onCreateProject) {
    quickActions.push({
      key: 'create-project',
      icon: <FolderPlus size={16} />,
      label: t('shell.newChat.quickAction.createProject'),
      color: '#2b9be6',
      onClick: onCreateProject,
    })
  }
  if (onOpenProject) {
    quickActions.push({
      key: 'open-project',
      icon: <FolderOpen size={16} />,
      label: t('shell.newChat.quickAction.openProject'),
      color: '#22a06b',
      onClick: onOpenProject,
    })
  }
  if (coordinator && !isHomeMode && onSelectHome) {
    quickActions.push({
      key: 'talk-coordinator',
      icon: <Megaphone size={16} />,
      label: t('shell.newChat.quickAction.talkCoordinator'),
      color: '#a371f7',
      onClick: onSelectHome,
    })
  }
  if (!isHomeMode && !noProjectMode) {
    quickActions.push(
      {
        key: 'quick-start-workflow',
        icon: <Waypoints size={16} />,
        label: t('quickStart.workflow.label'),
        color: '#2b9be6',
        onClick: () => handleQuickModeStart('builtin:mode:workflow'),
      },
      {
        key: 'quick-start-goal',
        icon: <Target size={16} />,
        label: t('quickStart.goal.label'),
        color: '#a371f7',
        onClick: () => handleQuickModeStart('builtin:mode:goal'),
      },
      {
        key: 'quick-start-plugin',
        icon: <Puzzle size={16} />,
        label: t('quickStart.plugin.label'),
        color: '#9333ea',
        onClick: () => handleQuickModeStart('builtin:bundle:plugin-dev'),
      },
    )
  }
  const showQuickActions = quickActions.length > 0

  const disabled = !activeUnit || loadingUnits || sending || (!isHomeMode && !agentKinds.some(k => k.kind === 'coder'))
  const placeholder = activeUnit
    ? (isHomeMode ? t('onboarding.coordinator.home.placeholder') : t('composer.placeholder.default'))
    : t('onboarding.coordinator.home.noModel')

  return (
    <div className="project-new-chat-page">
      <div className="project-new-chat-center">
        <div className="project-new-chat-header">
          {isHomeMode ? (
            <>
              <div className="project-new-chat-icon coordinator-home-icon">
                <MushroomIcon size={32} strokeWidth={72} />
              </div>
              <h2 className="project-new-chat-title">{title}</h2>
              <p className="project-new-chat-subtitle">{subtitle}</p>
            </>
          ) : noProjectMode ? (
            <>
              <div className="project-new-chat-icon">
                <FolderOpen size={32} />
              </div>
              <h2 className="project-new-chat-title">{title}</h2>
              <p className="project-new-chat-subtitle">{subtitle}</p>
            </>
          ) : (
            <>
              <div className="project-new-chat-title-row">
                <FolderOpen size={20} className="project-new-chat-title-icon" />
                <h2 className="project-new-chat-title">{title}</h2>
              </div>
              <p className="project-new-chat-subtitle">{subtitle}</p>
            </>
          )}
        </div>
        {showQuickActions && (
          <div className="project-new-chat-quick-actions">
            {quickActions.map(action => (
              <button
                key={action.key}
                type="button"
                className="project-new-chat-quick-action"
                data-guide-id={action.key === 'create-project' ? 'quick-action.create-project' : undefined}
                style={{ '--qa-accent': action.color } as React.CSSProperties}
                onClick={action.onClick}
              >
                <span className="project-new-chat-quick-action-icon">{action.icon}</span>
                <span className="project-new-chat-quick-action-label">{action.label}</span>
              </button>
            ))}
          </div>
        )}
        {!noProjectMode && (
          <div className="project-new-chat-composer">
          <AIComposer
            value={value}
            onChange={setValue}
            onVoiceResult={(text) => setValue(prev => prev ? prev + ' ' + text : text)}
            onSend={handleSend}
            isStreaming={sending}
            disabled={disabled}
            placeholder={placeholder}
            projectId={activeProject?.ProjectID ?? null}
            providers={providers}
            groups={groups}
            activeRoute={activeRoute}
            currentUnit={currentUnit}
            onSelectRoute={handleSelectRoute}
            onSelectUnit={handleSelectUnit}
            history={history}
            onHistoryChange={setHistory}
            leftSlot={
              <ProjectSelector
                value={selectorValue}
                options={projectOptions}
                disabled={contextLoading || projectSwitchingId !== null}
                loading={projectSwitchingId === activeProject?.ProjectID}
                onChange={handleSelectorChange}
              />
            }
            avatarBarSlot={
              <AgentAvatarBar
                agents={agents}
                activeAgentId={isHomeMode ? coordinator!.Id : null}
                onAgentClick={onAgentClick}
                onAgentMenuOpen={onAgentMenuOpen}
                onDeleteAgent={onDeleteAgent}
                onCreateAgent={onCreateAgent}
              />
            }
            permissionMode={permissionMode}
            onPermissionModeChange={onPermissionModeChange}
            globalPermissionMode={globalPermissionMode}
            onGlobalPermissionModeChange={onGlobalPermissionModeChange}
            agentActorId={modeAgentActorId}
            onEnterMode={handleEnterMode}
          />
          </div>
        )}
        {!noProjectMode && <SuggestionChips context={ctx} onPick={handleSuggestionPick} />}
        {!noProjectMode && loadingUnits && (
          <div className="project-new-chat-loading">
            <Loader2 size={14} className="project-new-chat-selector-spin" />
            <span>{t('onboarding.coordinator.home.loadingModels')}</span>
          </div>
        )}
      </div>
    </div>
  )
}