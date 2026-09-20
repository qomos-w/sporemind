import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { Search, Play, Pause, RefreshCw, Clock, ExternalLink, Locate, MapPin, CalendarPlus, ChevronLeft, ChevronDown, Trash2, Waypoints, MessageSquare, UserCog, Sparkles, Plus, Folder, Globe, Check } from 'lucide-react'
import { useBrowserOverlay } from '../browserOverlay'
import { client } from '../../../application/generated-client'
import * as projectClient from '../../../gen-clients/project/client'
import * as workspaceClient from '../../../gen-clients/workspace/client'
import type { InvokeOptions } from '@qomos/gospore-client'
import { useI18n } from '../../../i18n'
import { splitFrontmatter, type MonoCardListItem } from '../../../domain/mono-types'
import { mapServerListItem } from '../../panels/mono-store'
import { loadPreference, savePreference } from '../../../application/theme-persist'
import type { WikiTimerListItem } from '../../../gen-types/project.wiki.part1'
import type { TemplateRunRecord } from '../../../gen-types/project.wiki.part5'
import type { AgentListItem, AgentKindInfo } from '../../../gen-clients/system/types'
import { subscribeAgentListStore, getAgentListItems } from '../hooks/agentListStore'
import {
  describeCron, filterTimersByStatus, searchTimers, relativeTime,
  cronToDraft, draftToCron, resolveScheduleType, resolveAgentStateAction, resolveTaskMode,
  findAgentByRef, agentRefId, agentActionsOf, resolveScheduledScope,
  type CronSummary, type TimerStatusFilter, type ScheduleDraft, type ScheduleType, type TaskMode, type AgentStateAction,
  type AgentActionItem,
} from './scheduledTasks'
import './ScheduledView.css'
import { ScheduleCronEditor } from './ScheduleCronEditor'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { useClickOutside } from '../hooks/useClickOutside'
import { ScheduledContextMenu } from './ScheduledContextMenu'
import type { ScheduledMenuTarget } from './ScheduledContextMenu'
import { consumePendingScheduledLocate, subscribeScheduledLocate } from './scheduledLocateStore'
import { cn } from '../../settings/shadcn/lib/utils'
import {
  Badge,
  Button,
  Input,
  Textarea,
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  SelectItemText,
  SelectSeparator,
} from '../../settings/shadcn/ui'

interface ScheduledViewProps {
  cards: MonoCardListItem[]
  /** Active project actor ID — project.* callables route to the project actor's
   *  cell via this target. Without it the call lands on the root cell and the
   *  gospore routing layer reports "call ID not registered". */
  projectId?: string
  /** Locate a workflow/instance map in the workflow view (open + zoom-to-fit).
   *  Only offered for rows of the active project. */
  onLocateMap?: (mapId: string) => void
  /** Open a card in the right-panel tab (used for the timer card, edit mode).
   *  The optional projectId routes foreign-project rows to a project-bound
   *  tab; omitted for active-project rows. */
  onOpenCard?: (cardId: string, projectId?: string) => void
  /** Create a scheduler card (null = prompt-type with the body as the prompt,
   *  tpl::<mapId> = workflow-bound, opts.type = agent_action creates a
   *  pause/resume timer card). Provided by AIShellLayout via
   *  monoStore.createSchedulerCard; returns the created card id (caller opens
   *  it for editing) or null on failure. Always targets the active project —
   *  the "new task" UI is only offered in the active scope. */
  onCreateScheduler?: (
    templateCardId: string | null,
    opts?: { type: 'agent_action'; agentAction: string; targetAgent: string },
  ) => Promise<string | null>
  /** Rewrite a scheduler card's cron. Returns true on success; the view then
   *  reloads timers (the backend re-arms them from the edited card). */
  onUpdateScheduleCron?: (cardId: string, cron: string, projectId?: string) => Promise<boolean>
  /** Rewrite the prompt body of a scheduler card (prompt-type only).
   *  Returns true on success; the view reloads timers. */
  onUpdateCardBody?: (cardId: string, body: string, projectId?: string) => Promise<boolean>
  /** Mobile shell mode: master-detail collapses to one panel at a time with a
   *  back button instead of the side-by-side desktop split. */
  isMobile?: boolean
  /** Delete a scheduler card (cascade-deletes its timer and instances). */
  onDeleteTimer?: (cardId: string, projectId?: string) => Promise<boolean>
  /** Rewrite the executor agent reference in a scheduler card's data block.
   *  Returns true on success. Used by the executor picker dropdown. */
  onUpdateExecutor?: (cardId: string, executor: string, projectId?: string) => Promise<boolean>
  /** Rewrite the agent kind template (data.agent_kind) a prompt-mode task
   *  creates its per-fire agent from. Returns true on success. */
  onUpdateAgentKind?: (cardId: string, kind: string, projectId?: string) => Promise<boolean>
  /** Rewrite the bound-agent contract in a scheduler card's data block
   *  (data.bind_mode=bound + data.bound_agent). An empty ref unbinds back to
   *  the fresh-agent-per-fire default. Returns true on success. */
  onUpdateBoundAgent?: (cardId: string, agentRef: string, projectId?: string) => Promise<boolean>
  /** Rewrite the agent-state actions list on a unified scheduler card.
   *  Replaces any legacy single-action fields (agent_action/target_agent). */
  onUpdateAgentActions?: (cardId: string, actions: AgentActionItem[], projectId?: string) => Promise<boolean>
  /** Bind or unbind a workflow template on a unified scheduler card.
   *  Passing null removes the binding and leaves the task in prompt mode. */
  onUpdateTemplateBinding?: (cardId: string, templateId: string | null, projectId?: string) => Promise<boolean>
  /** Rename a scheduled task in place. Persists data.title on the scheduler
   *  card; the card id / timer id is immutable. Returns true on success. */
  onUpdateTitle?: (cardId: string, title: string, projectId?: string) => Promise<boolean>
  /** All known projects — used to resolve ProjectId → display name in agent
   *  dropdowns (the agent list spans multiple projects) and to power the
   *  scope picker (全局 / per-project browsing). */
  projects?: { ProjectID: string; Name: string; System?: boolean }[]
}

const PREFIX = 'scheduler:'

export interface TimerRow {
  timer: WikiTimerListItem
  title: string
  summary: CronSummary
  cron: string
  expression: string
  timezone: string
  templateId: string
  scheduleType: ScheduleType
  /** Unified tasks: the runtime mode ("prompt" | "template") of the task. */
  taskMode: TaskMode
  /** Agent-state action cards: "pause" | "resume" (undefined for ordinary tasks). */
  agentStateAction?: AgentStateAction
  /** agent_action cards: target agent reference ("agent:<name>" or raw ActorID). */
  targetAgent: string
  /** Executor agent reference for prompt/workflow types ("agent:<name>" or raw ActorID).
   *  Only used by template-mode task cards (the workflow instance owner). */
  executor: string
  /** Agent kind template a prompt-mode task creates its per-fire agent from
   *  (data.agent_kind; empty means the backend default coder). */
  agentKind: string
  /** Explicit dual-mode opt-in on the card data block (data.bind_mode). */
  bindMode: string
  /** Stable agent the card is bound to (data.bound_agent, "agent:<id>"). */
  boundAgent: string
  body: string
  /** Parsed agent-state actions (legacy single-action cards are normalised). */
  agentActions: AgentActionItem[]
  /** Origin project of the timer ('' = root/unknown). Foreign-project rows are
   *  view + edit routed per-project; creation/locate stay active-project. */
  projectId: string
}

/** Stable React key / selection id for a timer row. Timer card ids are only
 *  unique within a project, so global lists key on project + id. */
function timerRowKey(row: Pick<TimerRow, 'projectId' | 'timer'>): string {
  return row.projectId ? `${row.projectId} ${row.timer.Id}` : row.timer.Id
}

/** Spread helper: foreign rows append their projectId to update callbacks,
 *  active-project rows keep the legacy call shape (no trailing undefined). */
function projectArgs(projectId?: string): [] | [string] {
  return projectId ? [projectId] : []
}

function scheduleOf(card: MonoCardListItem): Record<string, unknown> {
  const d = card.data?.schedule
  return d && typeof d === 'object' ? (d as Record<string, unknown>) : {}
}
function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}
function stripTitle(id: string): string {
  return id.startsWith(PREFIX) ? id.slice(PREFIX.length) : id
}
/** List/detail type + task-mode badges for a timer row. agent_action rows show
 *  the pause/resume badge; unified task rows show a "Task" type badge plus a
 *  task-mode (prompt/template) badge. */
function ScheduledTypeBadges({ row, t, size = 11 }: { row: TimerRow; t: ReturnType<typeof useI18n>['t']; size?: number }) {
  if (row.agentStateAction) {
    return (
      <Badge variant="outline" className="scheduled-type-badge agent_action" title={t('scheduled.type.agentAction')}>
        <Pause size={size} />
        <span className="scheduled-type-badge-label">
          {row.agentStateAction === 'resume' ? t('scheduled.type.agentResume') : t('scheduled.type.agentPause')}
        </span>
      </Badge>
    )
  }
  const modeLabel = row.taskMode === 'template' ? t('scheduled.taskMode.template') : t('scheduled.taskMode.prompt')
  return (
    <>
      <Badge variant="outline" className="scheduled-type-badge task" title={t('scheduled.type.task')}>
        <Sparkles size={size} />
        <span className="scheduled-type-badge-label">{t('scheduled.type.task')}</span>
      </Badge>
      <Badge variant="outline" className={`scheduled-task-mode-badge ${row.taskMode}`} title={modeLabel}>
        {row.taskMode === 'template' ? <Waypoints size={size} /> : <MessageSquare size={size} />}
        <span className="scheduled-task-mode-badge-label">{modeLabel}</span>
      </Badge>
    </>
  )
}

/** project.* callables route to the project actor's cell via `target`. */
function targetOpts(projectId?: string): InvokeOptions | undefined {
  return projectId ? { target: projectId } : undefined
}

function agentDisplay(ag: AgentListItem | null | undefined, fallback: string): string {
  return ag ? (ag.DisplayName || ag.Id) : fallback
}

export function ScheduledView({
  cards,
  projectId,
  onLocateMap,
  onOpenCard,
  onCreateScheduler,
  onUpdateScheduleCron,
  onUpdateCardBody,
  isMobile,
  onDeleteTimer,
  onUpdateExecutor,
  onUpdateAgentKind,
  onUpdateBoundAgent,
  onUpdateAgentActions,
  onUpdateTemplateBinding,
  onUpdateTitle,
  projects,
}: ScheduledViewProps) {
  const { t } = useI18n()
  const [timers, setTimers] = useState<{ timer: WikiTimerListItem; projectId: string }[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string>('')
  const [statusFilter, setStatusFilter] = useState<TimerStatusFilter>('all')
  const [query, setQuery] = useState('')

  // ── Scope picker (global / pinned project / follow active) ──
  // '' follows the active project, 'global' spans all user projects, anything
  // else pins that project. Persisted as an actor-owned preference.
  const SCOPE_PREF = 'scheduled.scope.v1'
  const [scopeValue, setScopeValue] = useState('')
  const [scopeMenuOpen, setScopeMenuOpen] = useState(false)
  const scopeMenuRef = useRef<HTMLDivElement>(null)
  useClickOutside(scopeMenuRef, () => setScopeMenuOpen(false), scopeMenuOpen)
  const scopeSaveVersion = useRef({ v: 0 })

  useEffect(() => {
    let cancelled = false
    void loadPreference(SCOPE_PREF).then(v => {
      if (!cancelled && v) setScopeValue(v)
    })
    return () => { cancelled = true }
  }, [])

  // Memoized: loadTimers depends on this object, so it must be referentially
  // stable across renders or the load effect re-fires forever.
  const scope = useMemo(() => resolveScheduledScope(scopeValue, projectId), [scopeValue, projectId])
  const scopeIsForeign = scope.kind !== 'active'
  const userProjects = useMemo(
    () => (projects ?? []).filter(p => !p.System),
    [projects],
  )
  // Drop a persisted pin that no longer exists (deleted project).
  useEffect(() => {
    if (scope.kind === 'project' && !userProjects.some(p => p.ProjectID === scope.projectId)) {
      setScopeValue('')
    }
  }, [scope, userProjects])

  const selectScope = useCallback((value: string) => {
    setScopeValue(value)
    setScopeMenuOpen(false)
    setSelectedId(null)
    void savePreference(SCOPE_PREF, value, 'scheduled-scope', scopeSaveVersion.current).catch(() => {})
  }, [])

  // Agent list subscription for the executor picker dropdown.
  useSyncExternalStore(subscribeAgentListStore, getAgentListItems)
  const agentItems = getAgentListItems()

  // Project ID → display name lookup for agent dropdowns.
  const projectNameOf = useMemo(() => {
    const m = new Map<string, string>()
    for (const p of projects ?? []) m.set(p.ProjectID, p.System ? t('project.systemProject') : p.Name)
    return (pid: string) => m.get(pid) ?? ''
  }, [projects, t])

  const scopeLabel = scope.kind === 'global' ? t('scheduled.scope.global')
    : scope.kind === 'project' ? (projectNameOf(scope.projectId ?? '') || t('scheduled.scope.project'))
    : (projectId ? (projectNameOf(projectId) || t('scheduled.scope.current')) : t('scheduled.scope.current'))

  // Coder agents of one project, suitable as executors.
  const coderAgentsOf = useCallback(
    (pid?: string) => agentItems.filter(a => a.AgentKind === 'coder' && (!pid || a.ProjectId === pid)),
    [agentItems],
  )

  // Agent kind templates (workspace registry, full list — no hardcoded subset)
  // power the prompt-mode task's kind picker: each fire creates a fresh agent
  // of the selected kind.
  const [agentKinds, setAgentKinds] = useState<AgentKindInfo[]>([])
  useEffect(() => {
    let cancelled = false
    workspaceClient.listAgentKinds(client)
      .then(resp => { if (!cancelled) setAgentKinds(resp.Items ?? []) })
      .catch(() => { if (!cancelled) setAgentKinds([]) })
    return () => { cancelled = true }
  }, [])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [runs, setRuns] = useState<TemplateRunRecord[]>([])
  const [runsLoading, setRunsLoading] = useState(false)
  const [busy, setBusy] = useState<Set<string>>(() => new Set())
  const [creating, setCreating] = useState(false)
  const [templatesOpen, setTemplatesOpen] = useState(false)
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)
  const [menuTarget, setMenuTarget] = useState<ScheduledMenuTarget | null>(null)
  const newTaskRef = useRef<HTMLDivElement>(null)
  useClickOutside(newTaskRef, () => setTemplatesOpen(false), templatesOpen)

  // Template binding / executor / agent-action selects each need overlay registration.
  const [templateBindOpen, setTemplateBindOpen] = useState(false)
  const [executorOpen, setExecutorOpen] = useState(false)
  const [agentKindOpen, setAgentKindOpen] = useState(false)
  const [agentActionOverlayOpen, setAgentActionOverlayOpen] = useState(false)

  useBrowserOverlay(templatesOpen || deleteConfirmOpen || !!menuTarget || templateBindOpen || executorOpen || agentKindOpen || agentActionOverlayOpen || scopeMenuOpen)

  // Template cards power the "New scheduled task" picker and template binding select.
  const templateCards = useMemo(
    () => cards.filter(c => c.type === 'workflow' && (c.data as Record<string, unknown> | undefined)?.template === true),
    [cards],
  )

  // Foreign-project scheduler / template cards fetched by the scope fan-out
  // (active-project cards come from the live `cards` prop).
  const [foreignSchedulerCards, setForeignSchedulerCards] = useState<Record<string, MonoCardListItem[]>>({})
  const [foreignTemplateCards, setForeignTemplateCards] = useState<Record<string, MonoCardListItem[]>>({})

  const schedulerCardsByProject = useMemo(() => {
    const m = new Map<string, Map<string, MonoCardListItem>>()
    const put = (pid: string, card: MonoCardListItem) => {
      let byId = m.get(pid)
      if (!byId) { byId = new Map(); m.set(pid, byId) }
      byId.set(card.id, card)
    }
    const activeKey = projectId ?? ''
    for (const c of cards) if (c.type === 'scheduler') put(activeKey, c)
    for (const [pid, list] of Object.entries(foreignSchedulerCards)) for (const c of list) put(pid, c)
    return m
  }, [cards, foreignSchedulerCards, projectId])

  const templateCardsOf = useCallback((pid: string): MonoCardListItem[] =>
    (projectId ?? '') === pid ? templateCards : (foreignTemplateCards[pid] ?? []),
    [projectId, templateCards, foreignTemplateCards])

  const rows = useMemo<TimerRow[]>(() => {
    return timers.map(({ timer, projectId: timerProject }) => {
      const card = schedulerCardsByProject.get(timerProject)?.get(timer.Id)
      const sched = card ? scheduleOf(card) : {}
      const cron = str(sched.cron)
      const expression = str(sched.expression)
      const cardData = (card?.data ?? {}) as Record<string, unknown>
      const templateId = str(cardData.workflow_template)
      const agentActions = agentActionsOf(cardData)
      return {
        timer,
        projectId: timerProject,
        // data.title is the in-place renamable display name; the stripped card
        // id is the fallback for cards never renamed.
        title: str(cardData.title) || stripTitle(timer.Id),
        summary: describeCron(cron),
        cron,
        expression,
        timezone: str(sched.timezone),
        templateId,
        scheduleType: resolveScheduleType(
          (timer as { ScheduleType?: string }).ScheduleType ?? str(cardData.schedule_type),
        ),
        taskMode: resolveTaskMode(
          (timer as { TaskMode?: string }).TaskMode ?? str(cardData.task_mode),
          templateId || undefined,
        ),
        agentStateAction: agentActions[0]?.action ?? resolveAgentStateAction(
          str(cardData.agent_action) || undefined,
          str(cardData.schedule_type) || undefined,
        ),
        targetAgent: str(cardData.target_agent),
        executor: str(cardData.executor),
        agentKind: str((timer as { AgentKind?: string }).AgentKind ?? cardData.agent_kind),
        bindMode: str(cardData.bind_mode),
        boundAgent: str(cardData.bound_agent),
        body: card?.raw ? splitFrontmatter(card.raw).body : '',
        agentActions,
      }
    })
  }, [timers, schedulerCardsByProject])

  const filtered = useMemo(() => {
    const byStatusSet = new Set(
      filterTimersByStatus(timers.map(e => e.timer), statusFilter).map(t => t.Id),
    )
    const scoped = rows.filter(r => byStatusSet.has(r.timer.Id))
    const searched = searchTimers(scoped.map(r => ({ timer: r.timer, title: r.title })), query)
    const searchedSet = new Set(searched.map(s => s.timer.Id))
    return rows.filter(r => searchedSet.has(r.timer.Id))
  }, [rows, timers, statusFilter, query])

  const selected = useMemo(
    () => rows.find(r => timerRowKey(r) === selectedId) ?? null,
    [rows, selectedId],
  )
  const selectedKey = selected ? timerRowKey(selected) : null
  // Active-project rows keep every locate affordance; foreign rows are
  // view-only. Compare normalized — projectId may be undefined for the active
  // workspace while row keys always use ''.
  const selectedIsActive = !!selected && selected.projectId === (projectId ?? '')
  // Executor/bound-agent selects follow the selected row's project.
  const selectedProjectId = selected?.projectId || projectId
  const projectAgents = useMemo(
    () => coderAgentsOf(selectedProjectId),
    [coderAgentsOf, selectedProjectId],
  )
  const selectedProjectOpts = selectedProjectId && selectedProjectId !== projectId ? selectedProjectId : undefined

  const loadTimers = useCallback(async () => {
    setLoading(true)
    setError('')
    const targets: (string | undefined)[] =
      scope.kind === 'global'
        ? userProjects.map(p => p.ProjectID)
        : scope.kind === 'project'
          ? [scope.projectId]
          : [projectId]
    const foreignIds = targets.filter((id): id is string => !!id && id !== projectId)
    const timerResults = await Promise.allSettled(targets.map(async id => {
      const resp = await projectClient.wikiListTimers(client, targetOpts(id))
      return (resp.Timers ?? []).map(timer => ({ timer, projectId: id ?? '' }))
    }))
    const cardResults = foreignIds.length === 0 ? [] : await Promise.allSettled(foreignIds.map(async id => {
      const [sched, wfs] = await Promise.all([
        projectClient.wikiListCards(client, { Flat: true, IncludeRaw: true, Limit: -1, Type: 'scheduler' }, targetOpts(id)),
        projectClient.wikiListCards(client, { Flat: true, Limit: -1, Type: 'workflow' }, targetOpts(id)),
      ])
      return {
        id,
        sched: (sched.Cards ?? []).map(mapServerListItem),
        tpl: (wfs.Cards ?? []).map(mapServerListItem).filter(
          c => (c.data as Record<string, unknown> | undefined)?.template === true,
        ),
      }
    }))
    const nextTimers = timerResults.flatMap(r => (r.status === 'fulfilled' ? r.value : []))
    const nextSched: Record<string, MonoCardListItem[]> = {}
    const nextTpl: Record<string, MonoCardListItem[]> = {}
    for (const r of cardResults) {
      if (r.status !== 'fulfilled') continue
      nextSched[r.value.id] = r.value.sched
      nextTpl[r.value.id] = r.value.tpl
    }
    setTimers(nextTimers)
    setForeignSchedulerCards(nextSched)
    setForeignTemplateCards(nextTpl)
    // AllSettled: one unreachable project must not blank the others — only a
    // total failure surfaces as an error.
    if (timerResults.length > 0 && timerResults.every(r => r.status === 'rejected')) {
      const firstRejected = timerResults.find(r => r.status === 'rejected') as PromiseRejectedResult | undefined
      const e = firstRejected?.reason
      setError(e instanceof Error ? e.message : String(e ?? 'load failed'))
    }
    setLoading(false)
  }, [scope, userProjects, projectId])

  useEffect(() => { void loadTimers() }, [loadTimers])

  // ── Locate requests from the scheduler mode badge ──
  const listRef = useRef<HTMLDivElement>(null)
  const locateRef = useRef<{ cardId: string; done: boolean } | null>(null)
  const applyLocate = useCallback(() => {
    const intent = consumePendingScheduledLocate()
    if (!intent) return
    locateRef.current = { cardId: intent.cardId, done: false }
    setStatusFilter('all')
    setQuery('')
    // Locate intents target the active project's timers; in a foreign scope
    // there is nothing to select — the pending intent simply no-ops.
    setSelectedId(projectId ? `${projectId} ${intent.cardId}` : intent.cardId)
    // Selection keys use `<projectId> <timerId>` with '' for the active
    // project, matching timerRowKey.
    setTemplatesOpen(false)
  }, [projectId])

  useEffect(() => {
    applyLocate()
    return subscribeScheduledLocate(applyLocate)
  }, [applyLocate])

  useEffect(() => {
    const loc = locateRef.current
    if (!loc || loc.done) return
    if (filtered.some(r => r.timer.Id === loc.cardId && (!projectId || r.projectId === projectId))) {
      loc.done = true
      listRef.current?.querySelector(`[data-timer-id="${loc.cardId}"]`)?.scrollIntoView({ block: 'nearest' })
    }
  }, [filtered, selectedId, projectId])

  const handleCreateFromTemplate = useCallback(async (
    templateCardId: string | null,
    opts?: { type: 'agent_action'; agentAction: string; targetAgent: string },
  ) => {
    if (!onCreateScheduler) return
    setCreating(true)
    setTemplatesOpen(false)
    try {
      const created = await onCreateScheduler(templateCardId, opts)
      if (created) {
        await loadTimers()
        onOpenCard?.(created)
      }
    } finally {
      setCreating(false)
    }
  }, [onCreateScheduler, onOpenCard, loadTimers])

  const loadRuns = useCallback(async (templateId: string, runProjectId?: string) => {
    if (!templateId) { setRuns([]); return }
    setRunsLoading(true)
    try {
      const resp = await projectClient.wikiListTemplateRuns(client, { TemplateMapId: templateId, Limit: 10 }, targetOpts(runProjectId ?? projectId))
      setRuns(resp.Runs ?? [])
    } catch {
      setRuns([])
    } finally {
      setRunsLoading(false)
    }
  }, [projectId])

  useEffect(() => {
    if (selected?.templateId) {
      void loadRuns(selected.templateId, selected.projectId)
    } else {
      // No bound template: clear both the rows and the loading flag, otherwise
      // the spinner left over from the previous selection hangs forever.
      setRuns([])
      setRunsLoading(false)
    }
  }, [selectedKey, selected?.templateId, selected?.projectId, loadRuns])

  useEffect(() => {
    if (!isMobile && !selectedId && filtered.length > 0) setSelectedId(timerRowKey(filtered[0]!))
  }, [isMobile, selectedId, filtered])

  const toggleBusy = useCallback((id: string, on: boolean) => {
    setBusy(prev => {
      const next = new Set(prev)
      if (on) next.add(id); else next.delete(id)
      return next
    })
  }, [])

  const handleToggle = useCallback(async (row: TimerRow) => {
    toggleBusy(timerRowKey(row), true)
    setError('')
    try {
      await projectClient.wikiToggleTimer(client, { Id: row.timer.Id, Enabled: !row.timer.Enabled }, targetOpts(row.projectId || projectId))
      await loadTimers()
    } catch (e) {
      // Without this the rejection escapes and the user gets no feedback while
      // busy silently clears; mirror handleRunNow's error surface.
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      toggleBusy(timerRowKey(row), false)
    }
  }, [projectId, loadTimers, toggleBusy])

  const handleRunNow = useCallback(async (row: TimerRow) => {
    toggleBusy(timerRowKey(row), true)
    setError('')
    const rowProjectId = row.projectId || projectId
    const rowAgents = coderAgentsOf(rowProjectId)
    try {
      // Prompt-mode task fires spawn their own fresh agent (data.agent_kind);
      // only template-mode tasks need a pre-registered executor agent.
      if (row.scheduleType === 'task' && row.taskMode === 'template' && row.executor && rowProjectId) {
        const ref = row.executor.startsWith('agent:') ? row.executor.slice(6) : row.executor
        const exists = rowAgents.some(a => a.Id === ref)
        if (!exists) {
          const agent = await workspaceClient.createAgent(client, {
            ProjectId: rowProjectId,
            DisplayName: '',
            AgentKind: 'coder',
          })
          if (onUpdateExecutor) {
            await onUpdateExecutor(row.timer.Id, `agent:${agent.Id}`, ...(rowProjectId !== projectId ? [rowProjectId!] : []))
          }
        } else {
          const agent = rowAgents.find(a => a.Id === ref)!
          if (agent.LoadState !== 'loaded') {
            await workspaceClient.loadAgent(client, { AgentId: agent.Id })
          }
        }
      }
      await projectClient.wikiTriggerTimerCard(client, { Id: row.timer.Id }, targetOpts(rowProjectId))
      await loadTimers()
      if (row.templateId) void loadRuns(row.templateId, row.projectId)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      toggleBusy(timerRowKey(row), false)
    }
  }, [projectId, loadTimers, loadRuns, toggleBusy, coderAgentsOf, onUpdateExecutor])

  const repeatLabel = useCallback((row: TimerRow) => {
    const s = row.summary
    if (s.kind === 'everyDay') return t('scheduled.repeat.everyDay')
    if (s.kind === 'weekdays') return t('scheduled.repeat.weekdays')
    if (s.kind === 'weekly') return t('scheduled.repeat.weekly', { day: t(`scheduled.dow.${s.dow}` as never, { defaultValue: '' }) })
    if (s.kind === 'monthly') return t('scheduled.repeat.monthly', { dom: s.dom ?? 0 })
    return row.expression || row.cron || t('scheduled.repeat.custom')
  }, [t])

  const summaryText = useCallback((row: TimerRow) => {
    const label = repeatLabel(row)
    return row.summary.time ? `${label} ${row.summary.time}` : label
  }, [repeatLabel])

  // ── Inline schedule editor (always visible when editable) ──
  const [draft, setDraft] = useState<ScheduleDraft | null>(null)
  const [savingSchedule, setSavingSchedule] = useState(false)
  const [scheduleError, setScheduleError] = useState('')

  useEffect(() => {
    if (selected) {
      setDraft(cronToDraft(selected.cron, selected.expression))
      setScheduleError('')
      setSavingSchedule(false)
    } else {
      setDraft(null)
    }
  }, [selectedKey])

  const saveSchedule = useCallback(async () => {
    if (!draft || !selected || !onUpdateScheduleCron) return
    const cron = draftToCron(draft)
    if (!cron) {
      setScheduleError(t('scheduled.edit.invalid'))
      return
    }
    setSavingSchedule(true)
    try {
      const ok = await onUpdateScheduleCron(selected.timer.Id, cron, ...projectArgs(selectedProjectOpts))
      if (ok) {
        await loadTimers()
      } else {
        setScheduleError(t('scheduled.edit.saveFailed'))
      }
    } finally {
      setSavingSchedule(false)
    }
  }, [draft, selected, selectedProjectOpts, onUpdateScheduleCron, loadTimers, t])

  // ── Inline prompt body editor (always visible for prompt-mode tasks) ──
  const [promptDraft, setPromptDraft] = useState('')
  const [savingPrompt, setSavingPrompt] = useState(false)
  const [promptError, setPromptError] = useState('')

  useEffect(() => {
    if (selected) {
      setPromptDraft(selected.body)
      setPromptError('')
      setSavingPrompt(false)
    }
  }, [selectedKey])

  const savePrompt = useCallback(async () => {
    if (!selected || !onUpdateCardBody) return
    setSavingPrompt(true)
    try {
      const ok = await onUpdateCardBody(selected.timer.Id, promptDraft, ...projectArgs(selectedProjectOpts))
      if (ok) {
        await loadTimers()
      } else {
        setPromptError(t('scheduled.edit.saveFailed'))
      }
    } finally {
      setSavingPrompt(false)
    }
  }, [selected, promptDraft, selectedProjectOpts, onUpdateCardBody, loadTimers, t])

  // ── Inline title rename (click the detail heading to edit in place) ──
  const [titleEditing, setTitleEditing] = useState(false)
  const [titleDraft, setTitleDraft] = useState('')
  const [savingTitle, setSavingTitle] = useState(false)
  const titleCommittingRef = useRef(false)

  useEffect(() => { setTitleEditing(false) }, [selectedKey])

  const commitTitle = useCallback(async () => {
    // Enter and blur can both land here; the ref keeps the save single-flight.
    if (titleCommittingRef.current || !selected || !onUpdateTitle) return
    const next = titleDraft.trim()
    if (!next || next === selected.title) {
      setTitleEditing(false)
      return
    }
    titleCommittingRef.current = true
    setSavingTitle(true)
    try {
      const ok = await onUpdateTitle(selected.timer.Id, next, ...projectArgs(selectedProjectOpts))
      if (!ok) setError(t('scheduled.edit.titleFailed'))
    } finally {
      titleCommittingRef.current = false
      setSavingTitle(false)
      setTitleEditing(false)
    }
  }, [selected, selectedProjectOpts, onUpdateTitle, titleDraft, t])

  const startTitleEdit = useCallback(() => {
    if (!selected) return
    setTitleDraft(selected.title)
    setTitleEditing(true)
  }, [selected])

  // ── Executor picker ──
  const [executorError, setExecutorError] = useState('')
  const [executorBusy, setExecutorBusy] = useState(false)

  const handleExecutorChange = useCallback(async (executor: string) => {
    if (!selected || !onUpdateExecutor || executor === '__create__') return
    setExecutorBusy(true)
    setExecutorError('')
    setExecutorOpen(false)
    try {
      const ok = await onUpdateExecutor(selected.timer.Id, executor, ...projectArgs(selectedProjectOpts))
      if (ok) {
        await loadTimers()
      } else {
        setExecutorError(t('scheduled.edit.saveFailed'))
      }
    } catch (e) {
      setExecutorError(e instanceof Error ? e.message : String(e))
    } finally {
      setExecutorBusy(false)
    }
  }, [selected, selectedProjectOpts, onUpdateExecutor, loadTimers, t])

  // ── Agent kind picker (prompt-mode tasks: each fire spawns a fresh agent
  // of the selected kind template; data.agent_kind drives the backend spawn).
  const [agentKindBusy, setAgentKindBusy] = useState(false)
  const [agentKindError, setAgentKindError] = useState('')

  const handleAgentKindChange = useCallback(async (kind: string) => {
    if (!selected || !onUpdateAgentKind) return
    setAgentKindBusy(true)
    setAgentKindError('')
    try {
      const ok = await onUpdateAgentKind(selected.timer.Id, kind, ...projectArgs(selectedProjectOpts))
      if (!ok) setAgentKindError(t('scheduled.edit.saveFailed'))
    } catch (e) {
      setAgentKindError(e instanceof Error ? e.message : String(e))
    } finally {
      setAgentKindBusy(false)
    }
  }, [selected, selectedProjectOpts, onUpdateAgentKind, t])

  const selectedAgentKind = selected?.agentKind || 'coder'
  const agentKindDisplayName = agentKinds.find(k => k.Kind === selectedAgentKind)?.DisplayName || selectedAgentKind

  // ── Bound-agent picker (prompt-mode tasks may bind a stable existing agent
  // instead of spawning per fire: data.bind_mode=bound + data.bound_agent).
  const [boundAgentBusy, setBoundAgentBusy] = useState(false)
  const [boundAgentError, setBoundAgentError] = useState('')

  const handleBoundAgentChange = useCallback(async (agentRef: string) => {
    if (!selected || !onUpdateBoundAgent) return
    setBoundAgentBusy(true)
    setBoundAgentError('')
    try {
      const ok = await onUpdateBoundAgent(selected.timer.Id, agentRef, ...projectArgs(selectedProjectOpts))
      if (ok) {
        await loadTimers()
      } else {
        setBoundAgentError(t('scheduled.edit.saveFailed'))
      }
    } catch (e) {
      setBoundAgentError(e instanceof Error ? e.message : String(e))
    } finally {
      setBoundAgentBusy(false)
    }
  }, [selected, selectedProjectOpts, onUpdateBoundAgent, loadTimers, t])

  /** The bound contract is active when the card explicitly declares it AND
   *  names an agent — a bind_mode with an empty bound_agent falls back to the
   *  kind display (the backend rebinds at fire time). */
  const boundActive = !!selected && selected.bindMode === 'bound' && !!selected.boundAgent
  const boundAgentInfo = useMemo(() => {
    if (!selected?.boundAgent) return null
    const ref = selected.boundAgent.startsWith('agent:') ? selected.boundAgent.slice(6) : selected.boundAgent
    return agentItems.find(a => a.Id === ref) ?? null
  }, [selected, agentItems])
  const boundAgentDisplayName = boundAgentInfo
    ? boundAgentInfo.DisplayName || boundAgentInfo.Id
    : selected?.boundAgent ?? ''

  const handleCreateAgent = useCallback(async () => {
    if (!selectedProjectId || !selected) return
    setExecutorBusy(true)
    setExecutorError('')
    setExecutorOpen(false)
    try {
      const agent = await workspaceClient.createAgent(client, {
        ProjectId: selectedProjectId,
        DisplayName: '',
        AgentKind: 'coder',
      })
      const ref = `agent:${agent.Id}`
      if (onUpdateExecutor) {
        await onUpdateExecutor(selected.timer.Id, ref, ...projectArgs(selectedProjectOpts))
        await loadTimers()
      }
    } catch (e) {
      setExecutorError(e instanceof Error ? e.message : String(e))
    } finally {
      setExecutorBusy(false)
    }
  }, [selectedProjectId, selected, selectedProjectOpts, onUpdateExecutor, loadTimers])

  const executorAgent = useMemo(() => {
    if (!selected?.executor) return null
    const ref = selected.executor.startsWith('agent:') ? selected.executor.slice(6) : selected.executor
    return projectAgents.find(a => a.Id === ref) ?? null
  }, [selected, projectAgents])

  const executorMissing = !!selected?.executor && !executorAgent

  // ── Template binding ──
  const handleTemplateBindingChange = useCallback(async (templateId: string) => {
    if (!selected || !onUpdateTemplateBinding) return
    const value = templateId || null
    try {
      const ok = await onUpdateTemplateBinding(selected.timer.Id, value, ...projectArgs(selectedProjectOpts))
      if (ok) await loadTimers()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [selected, selectedProjectOpts, onUpdateTemplateBinding, loadTimers])

  // Reset per-selection editor state.
  useEffect(() => {
    setExecutorError('')
    setAgentKindError('')
    setBoundAgentError('')
    setError('')
    setDeleteConfirmOpen(false)
  }, [selectedId])

  const instanceCount = useMemo(() => {
    if (!selected) return 0
    return runs.filter(r => r.SchedulerCardId === selected.timer.Id).length
  }, [runs, selected])

  const handleDelete = useCallback(async () => {
    if (!selected || !onDeleteTimer) return
    const cardId = selected.timer.Id
    setError('')
    try {
      const ok = await onDeleteTimer(cardId, ...projectArgs(selectedProjectOpts))
      if (ok) {
        setSelectedId(null)
        setDeleteConfirmOpen(false)
        await loadTimers()
      } else {
        // Keep the dialog open so the user can retry; surface the failure
        // instead of silently skipping the reload.
        setError(t('scheduled.edit.saveFailed'))
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [selected, selectedProjectOpts, onDeleteTimer, loadTimers, t])

  const tabs: { key: TimerStatusFilter; label: string }[] = [
    { key: 'all', label: t('scheduled.tab.all') },
    { key: 'enabled', label: t('scheduled.tab.enabled') },
    { key: 'paused', label: t('scheduled.tab.paused') },
  ]

  return (
    <div className={cn('shadcn-scope scheduled-view', isMobile && 'mobile', isMobile && selectedId && 'selected')}>
      <section className="scheduled-list-panel">
        <div className="scheduled-list-header">
          <div className="scheduled-scope" ref={scopeMenuRef}>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="scheduled-scope-btn"
              title={t('scheduled.scope.title')}
              aria-label={t('scheduled.scope.title')}
              aria-haspopup="menu"
              aria-expanded={scopeMenuOpen}
              onClick={() => setScopeMenuOpen(v => !v)}
            >
              {scope.kind === 'global' ? <Globe size={13} /> : <Folder size={13} />}
              <span className="scheduled-scope-label">{scopeLabel}</span>
              <ChevronDown size={12} />
            </Button>
            {scopeMenuOpen && (
              <div className="scheduled-scope-menu" role="menu">
                <Button
                  type="button"
                  role="menuitem"
                  variant="ghost"
                  size="sm"
                  className="scheduled-scope-item"
                  aria-checked={scope.kind === 'active'}
                  onClick={() => selectScope(projectId ?? '')}
                >
                  <Folder size={13} />
                  <span className="scheduled-scope-item-label">
                    {projectId ? (projectNameOf(projectId) || t('scheduled.scope.current')) : t('scheduled.scope.current')}
                  </span>
                  {scope.kind === 'active' && <Check size={12} />}
                </Button>
                <Button
                  type="button"
                  role="menuitem"
                  variant="ghost"
                  size="sm"
                  className="scheduled-scope-item"
                  aria-checked={scope.kind === 'global'}
                  onClick={() => selectScope('global')}
                >
                  <Globe size={13} />
                  <span className="scheduled-scope-item-label">{t('scheduled.scope.global')}</span>
                  {scope.kind === 'global' && <Check size={12} />}
                </Button>
                {userProjects.map(p => (
                  <Button
                    key={p.ProjectID}
                    type="button"
                    role="menuitem"
                    variant="ghost"
                    size="sm"
                    className="scheduled-scope-item"
                    aria-checked={scope.kind === 'project' && scope.projectId === p.ProjectID}
                    onClick={() => selectScope(p.ProjectID)}
                  >
                    <Folder size={13} />
                    <span className="scheduled-scope-item-label" title={p.Name}>{p.Name}</span>
                    {p.ProjectID === projectId && <span className="scheduled-scope-current-tag">{t('scheduled.scope.current')}</span>}
                    {scope.kind === 'project' && scope.projectId === p.ProjectID && <Check size={12} />}
                  </Button>
                ))}
              </div>
            )}
          </div>
          <div className="scheduled-list-header-row">
            <div className="scheduled-tabs" role="tablist">
              {tabs.map(tab => (
                <Button
                  key={tab.key}
                  role="tab"
                  type="button"
                  variant={statusFilter === tab.key ? 'secondary' : 'ghost'}
                  size="sm"
                  aria-selected={statusFilter === tab.key}
                  onClick={() => setStatusFilter(tab.key)}
                >
                  {tab.label}
                </Button>
              ))}
            </div>
            {/* Creation always targets the active project — only offered in the
                active scope so a foreign view never writes to a surprising place. */}
            {onCreateScheduler && !scopeIsForeign && (
              <div className="scheduled-new-task" ref={newTaskRef}>
                {templatesOpen && (
                  <div className="scheduled-new-task-menu" role="menu">
                    <Button
                      type="button"
                      role="menuitem"
                      variant="ghost"
                      size="sm"
                      className="scheduled-new-task-item scheduled-new-task-prompt"
                      onClick={() => void handleCreateFromTemplate(null)}
                    >
                      <MessageSquare size={13} />
                      <span>{t('scheduled.newTask.promptEntry')}</span>
                    </Button>
                    <Button
                      type="button"
                      role="menuitem"
                      variant="ghost"
                      size="sm"
                      className="scheduled-new-task-item"
                      onClick={() => void handleCreateFromTemplate(null, { type: 'agent_action', agentAction: 'pause', targetAgent: 'agent:coder' })}
                    >
                      <Pause size={13} />
                      <span>{t('scheduled.newTask.agentActionEntry')}</span>
                    </Button>
                    <div className="scheduled-new-task-label">{t('scheduled.newTask.templatesLabel')}</div>
                    {templateCards.length === 0 ? (
                      <div className="scheduled-new-task-empty">{t('scheduled.newTask.noTemplates')}</div>
                    ) : templateCards.map(tpl => (
                      <Button
                        key={tpl.id}
                        type="button"
                        role="menuitem"
                        variant="ghost"
                        size="sm"
                        className="scheduled-new-task-item"
                        onClick={() => void handleCreateFromTemplate(tpl.id)}
                      >
                        <Clock size={13} />
                        <span title={tpl.id}>{tpl.id}</span>
                      </Button>
                    ))}
                  </div>
                )}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="scheduled-icon-btn"
                  title={t('scheduled.newTask.title')}
                  aria-label={t('scheduled.newTask.title')}
                  aria-expanded={templatesOpen}
                  disabled={creating}
                  onClick={() => setTemplatesOpen(v => !v)}
                >
                  <CalendarPlus size={15} />
                </Button>
              </div>
            )}
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="scheduled-icon-btn"
              title={t('scheduled.refresh')}
              aria-label={t('scheduled.refresh')}
              onClick={() => void loadTimers()}
            >
              <RefreshCw size={15} />
            </Button>
          </div>
        </div>
        <div className="scheduled-search">
          <Search size={14} className="text-muted-foreground" />
          <Input
            type="text"
            placeholder={t('scheduled.search')}
            value={query}
            onChange={e => setQuery(e.target.value)}
          />
        </div>
        <div className="scheduled-list" ref={listRef}>
          {loading ? (
            <div className="scheduled-list-empty">{t('scheduled.loading')}</div>
          ) : error ? (
            <div className="scheduled-list-empty scheduled-error">{error}</div>
          ) : filtered.length === 0 ? (
            <div className="scheduled-list-empty">
              <div className="scheduled-empty-title">{t('scheduled.empty.title')}</div>
              <div className="scheduled-empty-hint">{t('scheduled.empty.hint')}</div>
            </div>
          ) : (
            filtered.map(row => (
              <button
                key={timerRowKey(row)}
                type="button"
                data-timer-id={row.timer.Id}
                className={cn('scheduled-item', selectedId === timerRowKey(row) && 'selected')}
                onClick={() => setSelectedId(timerRowKey(row))}
                onContextMenu={e => {
                  e.preventDefault()
                  setSelectedId(timerRowKey(row))
                  setMenuTarget({ row, x: e.clientX, y: e.clientY })
                }}
              >
                <span className={cn('scheduled-status-dot', row.timer.Enabled ? 'on' : 'off')} />
                <span className="scheduled-item-main">
                  <span className="scheduled-item-title-row">
                    <span className="scheduled-item-title">{row.title}</span>
                    <ScheduledTypeBadges row={row} t={t} />
                    {scope.kind === 'global' && row.projectId && (
                      <span className="scheduled-item-project" title={projectNameOf(row.projectId)}>
                        <MapPin size={10} />
                        <span>{projectNameOf(row.projectId) || row.projectId}</span>
                      </span>
                    )}
                  </span>
                  <span className="scheduled-item-summary">{summaryText(row)}</span>
                </span>
                <span className="scheduled-item-toggle" aria-hidden>
                  {row.timer.Enabled ? <Pause size={14} /> : <Play size={14} />}
                </span>
              </button>
            ))
          )}
        </div>
      </section>

      <section className="scheduled-detail-panel">
        {!selected ? (
          <div className="scheduled-detail-empty">{t('scheduled.selectPrompt')}</div>
        ) : (
          <div className="scheduled-detail">
            <header className="scheduled-detail-header">
              {isMobile && (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="scheduled-icon-btn scheduled-back"
                  title={t('scheduled.back')}
                  aria-label={t('scheduled.back')}
                  onClick={() => setSelectedId(null)}
                >
                  <ChevronLeft size={16} />
                </Button>
              )}
              <Badge variant={selected.timer.Enabled ? 'default' : 'secondary'} className="scheduled-status-badge">
                {selected.timer.Enabled ? t('scheduled.status.enabled') : t('scheduled.status.paused')}
              </Badge>
              <ScheduledTypeBadges row={selected} t={t} size={12} />
              {selected.projectId && selected.projectId !== projectId && (
                <span className="scheduled-detail-project" title={projectNameOf(selected.projectId)}>
                  <MapPin size={11} />
                  <span>{projectNameOf(selected.projectId) || selected.projectId}</span>
                </span>
              )}
              <div className="scheduled-detail-actions">
                <Button
                  type="button"
                  variant="outline"
                  size="icon-sm"
                  className="scheduled-icon-btn"
                  title={selected.timer.Enabled ? t('scheduled.status.paused') : t('scheduled.status.enabled')}
                  disabled={busy.has(selectedKey ?? '')}
                  onClick={() => void handleToggle(selected)}
                >
                  {selected.timer.Enabled ? <Pause size={15} /> : <Play size={15} />}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="icon-sm"
                  className="scheduled-icon-btn"
                  title={selected.timer.LastStatus === 'running' && selected.timer.CurrentInstance ? t('scheduled.instanceRunning') : t('scheduled.runNow')}
                  aria-label={t('scheduled.runNow')}
                  disabled={busy.has(selectedKey ?? '') || (selected.timer.LastStatus === 'running' && !!selected.timer.CurrentInstance)}
                  onClick={() => void handleRunNow(selected)}
                >
                  <Play size={15} />
                </Button>
                {onOpenCard && (
                  <Button
                    type="button"
                    variant="outline"
                    size="icon-sm"
                    className="scheduled-icon-btn scheduled-open-card"
                    title={t('scheduled.openCard')}
                    aria-label={t('scheduled.openCard')}
                    onClick={() => onOpenCard(selected.timer.Id, ...projectArgs(selectedProjectOpts))}
                  >
                    <ExternalLink size={15} />
                  </Button>
                )}
                {onDeleteTimer && (
                  <Button
                    type="button"
                    variant="outline"
                    size="icon-sm"
                    className="scheduled-icon-btn scheduled-delete-timer"
                    title={t('scheduled.delete.button')}
                    aria-label={t('scheduled.delete.button')}
                    disabled={busy.has(selectedKey ?? '')}
                    onClick={() => setDeleteConfirmOpen(true)}
                  >
                    <Trash2 size={15} />
                  </Button>
                )}
              </div>
            </header>

            {onUpdateTitle && titleEditing ? (
              <input
                className="scheduled-detail-title-input"
                value={titleDraft}
                autoFocus
                disabled={savingTitle}
                aria-label={t('scheduled.edit.title')}
                onChange={e => setTitleDraft(e.target.value)}
                onKeyDown={e => {
                  if (e.key === 'Enter') void commitTitle()
                  else if (e.key === 'Escape') setTitleEditing(false)
                }}
                onBlur={() => void commitTitle()}
              />
            ) : (
              <h2
                className={cn('scheduled-detail-title', onUpdateTitle && 'editable')}
                title={onUpdateTitle ? t('scheduled.edit.title') : undefined}
                onClick={onUpdateTitle ? startTitleEdit : undefined}
              >
                {selected.title}
              </h2>
            )}

            {onUpdateScheduleCron && draft && (
              <section className="scheduled-detail-section">
                <h3 className="scheduled-section-heading scheduled-frequency-heading">
                  <span className="scheduled-frequency-heading-text">
                    <Clock size={14} /> {t('scheduled.section.frequency')}
                  </span>
                </h3>
                <ScheduleCronEditor
                  draft={draft}
                  onDraftChange={setDraft}
                  onApply={() => void saveSchedule()}
                  saving={savingSchedule}
                  error={scheduleError}
                  originalCron={selected.cron}
                  originalExpression={selected.expression}
                />
              </section>
            )}

            {selected.scheduleType === 'task' && !selected.agentStateAction && (
              <section className="scheduled-detail-section">
                <h3 className="scheduled-section-heading">
                  <UserCog size={14} /> {t('scheduled.section.executor')}
                </h3>
                {selected.taskMode === 'prompt' ? (
                  // Prompt-mode tasks run either on a fresh agent spawned per
                  // fire from the selected kind template (data.agent_kind), or
                  // — when an existing agent is picked from the second group —
                  // on that stable agent (data.bind_mode=bound +
                  // data.bound_agent, the dual-mode scheduler contract).
                  <div className="scheduled-executor-row">
                    {onUpdateAgentKind || onUpdateBoundAgent ? (
                      <>
                        <SelectRoot
                          open={agentKindOpen}
                          onOpenChange={setAgentKindOpen}
                          value={boundActive ? selected.boundAgent : selectedAgentKind}
                          onValueChange={(v) => {
                            const value = (v as string) ?? ''
                            if (!value) return
                            if (value.startsWith('agent:')) void handleBoundAgentChange(value)
                            else void handleAgentKindChange(value)
                          }}
                        >
                          <SelectTrigger size="sm" disabled={agentKindBusy || boundAgentBusy} className="w-full max-w-xs" data-guide-id="scheduled-agent-kind-trigger">
                            <SelectValue>
                              <span className="scheduled-executor-name">
                                {boundActive ? boundAgentDisplayName : agentKindDisplayName}
                              </span>
                            </SelectValue>
                          </SelectTrigger>
                          <SelectContent>
                            {agentKinds.length === 0 && projectAgents.length === 0 && (
                              <SelectItem value="__empty__" disabled>
                                <SelectItemText>{t('scheduled.agent.noKinds')}</SelectItemText>
                              </SelectItem>
                            )}
                            {agentKinds.map(k => (
                              <SelectItem key={k.Kind} value={k.Kind} data-guide-id={`scheduled-agent-kind-${k.Kind}`}>
                                <SelectItemText>{k.DisplayName || k.Kind}</SelectItemText>
                              </SelectItem>
                            ))}
                            {projectAgents.length > 0 && (
                              <>
                                <SelectSeparator />
                                <SelectItem value="__existing_header__" disabled>
                                  <SelectItemText>{t('scheduled.agent.existingGroup')}</SelectItemText>
                                </SelectItem>
                                {projectAgents.map(ag => (
                                  <SelectItem key={ag.Id} value={`agent:${ag.Id}`} data-guide-id={`scheduled-agent-existing-${ag.Id}`}>
                                    <SelectItemText>
                                      <span className={cn('flex flex-col', ag.LoadState !== 'loaded' && 'text-muted-foreground')}>
                                        <span className="flex items-center gap-1">
                                          {ag.DisplayName || ag.Id}
                                          {ag.Title && <span className="text-xs text-muted-foreground">{ag.Title}</span>}
                                        </span>
                                        {ag.LoadState !== 'loaded' && (
                                          <span className="text-xs text-muted-foreground">{t('scheduled.mode.agentUnloaded')}</span>
                                        )}
                                      </span>
                                    </SelectItemText>
                                  </SelectItem>
                                ))}
                              </>
                            )}
                          </SelectContent>
                        </SelectRoot>
                        <div className="text-xs text-muted-foreground mt-1">
                          {boundActive ? t('scheduled.agent.boundHint') : t('scheduled.agent.kindHint')}
                        </div>
                      </>
                    ) : (
                      <span className="scheduled-executor-name">
                        {boundActive ? boundAgentDisplayName : agentKindDisplayName}
                      </span>
                    )}
                  </div>
                ) : (
                  // Template-mode tasks need an existing agent as the workflow
                  // instance owner (data.executor).
                  <div className="scheduled-executor-row">
                    {onUpdateExecutor ? (
                      <SelectRoot
                        open={executorOpen}
                        onOpenChange={setExecutorOpen}
                        value={selected.executor || ''}
                        onValueChange={(v) => {
                          const value = v as string
                          if (value === '__create__') void handleCreateAgent()
                          else void handleExecutorChange(value)
                        }}
                      >
                        <SelectTrigger size="sm" disabled={executorBusy} className="w-full max-w-xs">
                          <SelectValue placeholder={t('scheduled.executor.none')}>
                            <span className={cn('scheduled-executor-name', executorMissing && 'missing')}>
                              {executorAgent
                                ? executorAgent.DisplayName || executorAgent.Id
                                : selected.executor || t('scheduled.executor.none')}
                            </span>
                          </SelectValue>
                        </SelectTrigger>
                        <SelectContent>
                          {projectAgents.map(ag => (
                            <SelectItem key={ag.Id} value={`agent:${ag.Id}`}>
                              <SelectItemText>
                                <span className={cn('flex flex-col', ag.LoadState !== 'loaded' && 'text-muted-foreground')}>
                                  <span className="flex items-center gap-1">
                                    {ag.DisplayName || ag.Id}
                                    {ag.Title && <span className="text-xs text-muted-foreground">{ag.Title}</span>}
                                  </span>
                                  {ag.LoadState !== 'loaded' && (
                                    <span className="text-xs text-muted-foreground">{t('scheduled.mode.agentUnloaded')}</span>
                                  )}
                                </span>
                              </SelectItemText>
                            </SelectItem>
                          ))}
                          <SelectSeparator />
                          <SelectItem value="__create__">
                            <SelectItemText>{t('scheduled.executor.create')}</SelectItemText>
                          </SelectItem>
                        </SelectContent>
                      </SelectRoot>
                    ) : (
                      <span className="scheduled-executor-name">
                        {executorAgent
                          ? executorAgent.DisplayName || executorAgent.Id
                          : selected.executor || t('scheduled.executor.none')}
                      </span>
                    )}
                  </div>
                )}
                {(selected.taskMode === 'prompt' ? (agentKindError || boundAgentError) : executorError) && (
                  <div className="text-sm text-destructive mt-2">
                    {selected.taskMode === 'prompt' ? (agentKindError || boundAgentError) : executorError}
                  </div>
                )}
              </section>
            )}

            {selected.scheduleType === 'task' && !selected.agentStateAction && (
              <section className="scheduled-detail-section">
                <h3 className="scheduled-section-heading">
                  <MapPin size={14} /> {t('scheduled.section.template')}
                </h3>
                <div className="scheduled-template-row">
                  {onUpdateTemplateBinding ? (
                    <SelectRoot
                      open={templateBindOpen}
                      onOpenChange={setTemplateBindOpen}
                      value={selected.templateId || ''}
                      onValueChange={(v) => void handleTemplateBindingChange(v as string)}
                    >
                      <SelectTrigger size="sm" className="w-full max-w-xs">
                        <SelectValue placeholder={t('scheduled.template.unbind', { defaultValue: '未绑定' })}>
                          {selected.templateId || t('scheduled.template.unbind', { defaultValue: '未绑定' })}
                        </SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="">
                          <SelectItemText>{t('scheduled.template.unbind', { defaultValue: '未绑定' })}</SelectItemText>
                        </SelectItem>
                        {templateCardsOf(selected.projectId || (projectId ?? '')).map(tpl => (
                          <SelectItem key={tpl.id} value={tpl.id}>
                            <SelectItemText>{tpl.id}</SelectItemText>
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </SelectRoot>
                  ) : (
                    <code className="scheduled-template-id">{selected.templateId || '—'}</code>
                  )}
                  {selected.templateId && onLocateMap && selectedIsActive && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="scheduled-template-locate"
                      onClick={() => onLocateMap(selected.templateId)}
                    >
                      <Locate size={13} /> {t('scheduled.locateTemplate')}
                    </Button>
                  )}
                </div>
              </section>
            )}

            {selected.scheduleType === 'task' && selected.taskMode === 'prompt' && selected.agentActions.length === 0 && (
              <section className="scheduled-detail-section">
                <h3 className="scheduled-section-heading scheduled-frequency-heading">
                  <span className="scheduled-frequency-heading-text">
                    <MessageSquare size={14} /> {t('scheduled.section.prompt')}
                  </span>
                </h3>
                <div className="scheduled-prompt-editor">
                  <Textarea
                    className="scheduled-prompt-textarea min-h-[120px]"
                    value={promptDraft}
                    onChange={e => setPromptDraft(e.target.value)}
                    rows={6}
                  />
                  {promptError && <div className="scheduled-editor-error">{promptError}</div>}
                  {onUpdateCardBody && (
                    <div className="scheduled-editor-actions">
                      <Button
                        type="button"
                        size="sm"
                        className="scheduled-editor-btn primary"
                        disabled={savingPrompt || promptDraft === selected.body}
                        onClick={() => void savePrompt()}
                      >
                        {t('scheduled.edit.save')}
                      </Button>
                    </div>
                  )}
                </div>
              </section>
            )}

            {selected.agentActions.length > 0 && onUpdateAgentActions && (
              <AgentActionsEditor
                key={selectedKey ?? undefined}
                selected={selected}
                agentItems={agentItems}
                projectNameOf={projectNameOf}
                projectOpts={selectedProjectOpts}
                onUpdateAgentActions={onUpdateAgentActions}
                loadTimers={loadTimers}
                onOverlayChange={setAgentActionOverlayOpen}
                t={t}
              />
            )}

            {selected.timer.CurrentInstance && (
              <section className="scheduled-detail-section">
                <h3 className="scheduled-section-heading">
                  <Clock size={14} /> {t('scheduled.currentInstance')}
                </h3>
                <div className="scheduled-template-row">
                  <code className="scheduled-template-id">{selected.timer.CurrentInstance}</code>
                  {onLocateMap && selectedIsActive && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="scheduled-template-locate"
                      onClick={() => onLocateMap(selected.timer.CurrentInstance!)}
                    >
                      <Locate size={13} /> {t('scheduled.locateInstance')}
                    </Button>
                  )}
                </div>
              </section>
            )}

            <section className="scheduled-detail-section">
              <h3 className="scheduled-section-heading">
                <Clock size={14} /> {t('scheduled.section.runHistory')}
              </h3>
              {!selected.templateId ? (
                <div className="scheduled-history-empty">{t('scheduled.runHistory.noTemplate')}</div>
              ) : runsLoading ? (
                <div className="scheduled-history-empty">{t('scheduled.loading')}</div>
              ) : runs.length === 0 ? (
                <div className="scheduled-history-empty">{t('scheduled.runHistory.empty')}</div>
              ) : (
                <ul className="scheduled-run-list">
                  {runs.map((run, i) => (
                    <li key={run.InstanceMapId + i} className="scheduled-run-item">
                      <span className="scheduled-run-time">{relativeTime(run.StartedAt)}</span>
                      {onLocateMap && selectedIsActive ? (
                        <button
                          type="button"
                          className="scheduled-run-btn"
                          title={t('scheduled.runHistory.locate')}
                          aria-label={t('scheduled.runHistory.locate')}
                          onClick={() => onLocateMap(run.InstanceMapId)}
                        >
                          {run.InstanceMapId}
                        </button>
                      ) : (
                        <span className="scheduled-run-id">{run.InstanceMapId}</span>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </div>
        )}
      </section>
      {selected && onDeleteTimer && (
        <ConfirmDialog
          open={deleteConfirmOpen}
          title={t('scheduled.delete.title')}
          description={instanceCount > 0
            ? t('scheduled.delete.confirmCount', { name: selected.title, count: instanceCount })
            : t('scheduled.delete.confirm', { name: selected.title })}
          danger
          confirmLabel={t('common.delete')}
          cancelLabel={t('common.cancel')}
          onCancel={() => setDeleteConfirmOpen(false)}
          onConfirm={() => void handleDelete()}
        />
      )}
      <ScheduledContextMenu
        target={menuTarget}
        onClose={() => setMenuTarget(null)}
        onToggle={handleToggle}
        onRunNow={handleRunNow}
        onOpenCard={onOpenCard ? (row) => onOpenCard(row.timer.Id, row.projectId && row.projectId !== projectId ? row.projectId : undefined) : undefined}
        onEditSchedule={(row) => {
          setSelectedId(timerRowKey(row))
          setDraft(cronToDraft(row.cron, row.expression))
          setScheduleError('')
        }}
        onLocateMap={onLocateMap && menuTarget?.row.projectId === projectId ? (row) => onLocateMap(row.templateId) : undefined}
        onDelete={(row) => {
          setSelectedId(timerRowKey(row))
          setDeleteConfirmOpen(true)
        }}
      />
    </div>
  )
}

interface AgentActionsEditorProps {
  selected: TimerRow
  agentItems: AgentListItem[]
  projectNameOf: (pid: string) => string
  /** Foreign-project id for the selected row (undefined = active project). */
  projectOpts?: string
  onUpdateAgentActions: (cardId: string, actions: AgentActionItem[], projectId?: string) => Promise<boolean>
  loadTimers: () => Promise<void>
  onOverlayChange: (open: boolean) => void
  t: ReturnType<typeof useI18n>['t']
}

function AgentActionsEditor({ selected, agentItems, projectNameOf, projectOpts, onUpdateAgentActions, loadTimers, onOverlayChange, t }: AgentActionsEditorProps) {
  const [actions, setActions] = useState<AgentActionItem[]>(selected.agentActions)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')

  useEffect(() => {
    setActions(selected.agentActions)
    setSaveError('')
  }, [selected.timer.Id, selected.agentActions])

  const updateAction = useCallback((index: number, patch: Partial<AgentActionItem>) => {
    setActions(prev => prev.map((a, i) => i === index ? { ...a, ...patch } : a))
  }, [])

  const removeAction = useCallback((index: number) => {
    setActions(prev => prev.filter((_, i) => i !== index))
  }, [])

  const addAction = useCallback(() => {
    // Default to an empty target: 'agent:coder' is a *kind* template, not a
    // concrete agent instance, so persisting it produced a target the backend
    // cannot resolve. An empty target stays unset until the user picks an agent
    // (save filters empty targets out rather than persisting an invalid ref).
    setActions(prev => [...prev, { action: 'pause', targetAgent: '' }])
  }, [])

  const save = useCallback(async () => {
    setSaving(true)
    setSaveError('')
    try {
      const valid = actions.filter(a => a.targetAgent)
      const ok = await onUpdateAgentActions(selected.timer.Id, valid, ...projectArgs(projectOpts))
      if (ok) {
        await loadTimers()
      } else {
        setSaveError(t('scheduled.edit.saveFailed'))
      }
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }, [actions, selected, onUpdateAgentActions, loadTimers, t])

  const targetOptions = useCallback((current: string) => {
    const existing = new Set(agentItems.map(a => `agent:${a.Id}`))
    const opts = agentItems.map(a => ({
      value: `agent:${a.Id}`,
      label: a.DisplayName || a.Id,
      title: a.Title,
      projectName: projectNameOf(a.ProjectId),
      loaded: a.LoadState === 'loaded',
    }))
    if (current && !existing.has(current) && current !== `agent:${current}`) {
      opts.unshift({ value: current, label: agentRefId(current), title: undefined, projectName: '', loaded: false })
    }
    return opts
  }, [agentItems, projectNameOf])

  return (
    <section className="scheduled-detail-section">
      <h3 className="scheduled-section-heading">
        <Pause size={14} /> {t('scheduled.section.agentAction')}
      </h3>
      <div className="scheduled-agent-actions-list">
        {actions.map((action, index) => (
          <div key={index} className="scheduled-agent-action-row">
            <SelectRoot
              value={action.action}
              onValueChange={(v) => { if (v) updateAction(index, { action: v as AgentStateAction }) }}
              onOpenChange={onOverlayChange}
            >
              <SelectTrigger size="sm" className="w-28">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="pause">
                  <SelectItemText>{t('scheduled.type.agentPause')}</SelectItemText>
                </SelectItem>
                <SelectItem value="resume">
                  <SelectItemText>{t('scheduled.type.agentResume')}</SelectItemText>
                </SelectItem>
              </SelectContent>
            </SelectRoot>
            <SelectRoot
              value={action.targetAgent || undefined}
              onValueChange={(v) => { if (v) updateAction(index, { targetAgent: v as string }) }}
              onOpenChange={onOverlayChange}
            >
              <SelectTrigger size="sm" className="flex-1 min-w-0">
                <SelectValue placeholder={t('scheduled.targetAgent')}>
                  {agentDisplay(findAgentByRef(agentItems, action.targetAgent), action.targetAgent)}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                {targetOptions(action.targetAgent).map(opt => (
                  <SelectItem key={opt.value} value={opt.value}>
                    <SelectItemText>
                      <span className={cn('flex items-center gap-2', !opt.loaded && 'text-muted-foreground')}>
                        <span className="flex flex-col">
                          <span className="flex items-center gap-1">
                            {opt.label}
                            {opt.title && <span className="text-xs text-muted-foreground">{opt.title}</span>}
                          </span>
                          {opt.projectName && <span className="text-xs text-muted-foreground">{opt.projectName}</span>}
                        </span>
                        {!opt.loaded && <span className="text-xs text-muted-foreground">{t('scheduled.mode.agentUnloaded')}</span>}
                      </span>
                    </SelectItemText>
                  </SelectItem>
                ))}
              </SelectContent>
            </SelectRoot>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              title={t('common.delete')}
              aria-label={t('common.delete')}
              onClick={() => removeAction(index)}
            >
              <Trash2 size={14} />
            </Button>
          </div>
        ))}
      </div>
      <div className="scheduled-agent-actions-footer">
        <Button type="button" variant="outline" size="sm" onClick={addAction}>
          <Plus size={14} /> {t('scheduled.agentAction.add', { defaultValue: '添加动作' })}
        </Button>
        <Button type="button" size="sm" disabled={saving || actions === selected.agentActions} onClick={() => void save()}>
          {t('scheduled.edit.save')}
        </Button>
      </div>
      {saveError && <div className="text-sm text-destructive mt-2">{saveError}</div>}
    </section>
  )
}
