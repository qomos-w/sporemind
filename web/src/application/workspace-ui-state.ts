import type {
  WorkspaceExplorerState,
  WorkspaceProjectBrowserState,
} from '../gen-types/workspace'
import { loadWorkspaceUIModel, saveWorkspaceUIModel } from './workspace-ui-client'
// The knowledge-base view protocol is declared once in the quick-view logic
// module ([[kb-quick-views]]) and re-exported below under this file's
// historical names (`KbQuickViewId` / `isKbQuickViewId`).
import {
  isKbQuickView as isKbQuickViewId,
  type KbQuickView as KbQuickViewId,
} from '../ui/ai/components/kbQuickViews.logic'

export interface AIShellProjectUIState {
  sidebarOrder: string[]
  agentOrder: string[]
}

export const defaultAIShellProjectUIState: AIShellProjectUIState = {
  sidebarOrder: [],
  agentOrder: [],
}

export async function getAIShellProjectUIState(): Promise<AIShellProjectUIState> {
  const model = await loadWorkspaceUIModel()
  return {
    sidebarOrder: model.AiShell?.SidebarOrder ?? [],
    agentOrder: model.AiShell?.AgentOrder ?? [],
  }
}

export async function saveAIShellProjectUIState(state: AIShellProjectUIState): Promise<void> {
  await saveWorkspaceUIModel('AiShell', (model) => ({
    AiShell: {
      ...(model.AiShell ?? {}),
      SidebarOrder: state.sidebarOrder,
      AgentOrder: state.agentOrder,
    },
  }))
}

export interface AIShellSessionState {
  selectedAgentId: string | null
  previousAgentId: string | null
}

export const defaultAIShellSessionState: AIShellSessionState = {
  selectedAgentId: null,
  previousAgentId: null,
}

export async function getAIShellSessionState(): Promise<AIShellSessionState> {
  const model = await loadWorkspaceUIModel()
  return {
    selectedAgentId: model.AiShell?.SelectedAgentId || null,
    previousAgentId: model.AiShell?.PreviousAgentId || null,
  }
}

export async function saveAIShellSessionState(state: AIShellSessionState): Promise<void> {
  await saveWorkspaceUIModel('AiShell', (model) => ({
    AiShell: {
      ...(model.AiShell ?? {}),
      SelectedAgentId: state.selectedAgentId ?? '',
      PreviousAgentId: state.previousAgentId ?? '',
    },
  }))
}

export interface AIShellLayoutState {
  sidebarVisible?: boolean
  sidebarPinned?: boolean
  sidebarWidth?: number
  rightPanelWidth?: number
  rightPanelOpen?: boolean
  shellMode?: string
  contentMode?: string
  /** Launcher (home) pane variant: 'code' (default), 'app', or 'workbench'
   * (fullscreen board takeover). */
  launcherMode?: 'code' | 'app' | 'workbench'
  /** Launcher app display: 'grid' (default) square tiles or 'list' rows. */
  launcherView?: 'grid' | 'list'
  /** Launcher favorite app ids in display order (收藏 section). */
  launcherFavorites?: string[]
  /** Launcher recently used app ids, most recent first, capped at 6 (最近 section). */
  launcherRecent?: string[]
  /** Launcher all-section (列表) custom order; ids not listed are appended in default order. */
  launcherOrder?: string[]
  rightTabOrder?: string[]
  categoryExpansion?: Record<string, boolean>
  expandedProjectIds?: string[]
  browserTabs?: Array<{
    id: string
    sessionId: string
    kind: 'global' | 'independent' | 'app'
    url: string
    label: string
  }>
  activeBrowserTabId?: string
  /** Persisted SSH right-panel tabs: stable tabId (ssh-host-<hostId>, from
   *  sshTabId in ui/ai/tabIds.ts) + hostId + hostName only. The volatile
   *  sessionId is deliberately excluded — startup adopts the account's live
   *  session for the host (sessionList) or opens a fresh one via shellOpen. */
  rightSshTabs?: Array<{
    id: string
    hostId: string
    hostName: string
  }>
  /** Persisted database right-panel tabs: stable tabId (db-profile-<profileId>,
   *  from dbTabId in ui/ai/tabIds.ts) + profileId + profileName + backend only.
   *  Unlike SSH, dbclient manages a per-profile connection pool inside the
   *  actor; the connection is reopened lazily on first use, so no volatile
   *  handle id is persisted. */
  rightDbTabs?: Array<{
    id: string
    profileId: string
    profileName: string
    backend: string
  }>
  /** Persisted open plugin (app) right-panel tabs: tabId components only
   * (pluginID + viewID; the tab id is recomputed via pluginTabId in
   * ui/ai/tabIds.ts). The registry is the source of truth for
   * title/route/icon/color at restore, so those are deliberately excluded.
   * Restart restores exactly these tabs — running apps whose views were not
   * open at shutdown are NOT auto-opened. Saved as an explicit array (even
   * empty) so closing every plugin tab is durable, unlike ssh/db tabs which
   * omit the key to keep the last value. */
  rightPluginTabs?: Array<{
    id: string
    pluginID: string
    viewID: string
  }>
  browserCreateScope?: 'normal' | 'independent'
  multiconsoleGrid?: MultiConsoleGridConfig
  recentOmniboxCommands?: string[]
  /** Pinned content modes (excluding conversation) shown in the sidebar. */
  pinnedModes?: string[]
  /** Whether composer mode badges (ai-composer-badge / ai-composer-badge-group) are visible. */
  composerBadgesVisible?: boolean
  /** Whether the context-window budget progress bar is shown in the turn tail. */
  turnBudgetBarVisible?: boolean
  /** Whether the token usage badge (input/output tokens, cost, cache hit) is shown in the turn tail. */
  turnTokenBadgeVisible?: boolean
  /** Whether the wall-clock duration badge is shown in the turn tail. */
  turnDurationBadgeVisible?: boolean
  /** Whether the token/s throughput sparkline is shown in the turn tail (desktop). */
  turnThroughputVisible?: boolean
  /** Whether the quick git bar is shown above the composer. */
  quickGitVisible?: boolean
  /** Whether the agent name label is shown next to the composer project switcher. */
  mobileContextAgentVisible?: boolean
  /** Whether smooth streaming scroll-follow is enabled. */
  smoothStream?: boolean
  /** Developer mode: unlocks app-creation tooling (Create App in the
   * sidebar). Persisted so release builds can opt in from settings. */
  developerMode?: boolean
  /** Experimental: when on, provider add/edit forms expose the per-provider
   * User-Agent override. Gated behind developer mode in settings. */
  providerUserAgentVisible?: boolean
  /** Expansion choreography per expandable UI kind. Four states:
   *  none (never expand), fold-expand-collapse (title delay -> expand ->
   *  fold after done), fold-expand (title delay -> expand -> stay),
   *  always-open. */
  uiExpansionModes?: Record<string, UiExpandAction>
  /** Duration (ms) of the grid-rows expand/collapse CSS transition shared by
   *  all expandable blocks. */
  uiExpandDurationMs?: number
  /** Expanded composer drawer stream height in px (desktop only). */
  composerDrawerHeight?: number
  /** Whether the IDEA-style terminal panel is expanded. Restored on desktop
   *  only; mobile never auto-opens (mutual exclusion with right panel). */
  terminalPanelOpen?: boolean
  /** Terminal panel height in px (desktop). Clamped to [120, 60% viewport]. */
  terminalPanelHeight?: number
  /** Persisted terminal tabs: { id, type, title? }. `type` is a key in the
   *  terminal tab type registry (web/src/ui/ai/components/terminal/tabRegistry). */
  terminalTabs?: Array<{ id: string; type: string; title?: string }>
  /** Active terminal tab id. */
  terminalActiveTabId?: string
  /** Knowledge-base (notes mode) selected quick view: 'home' | 'search' | 'starred'.
   *  The knowledge base is NOT bound to the active project; this plus `kbLane`
   *  is the durable replacement for the old notesProjectId/notesCardId pair. */
  kbView?: string
  /** Knowledge-base active swimlane target (project + optional root card).
   *  `null` means a quick view is shown instead of a lane. */
  kbLane?: { projectId: string; projectName: string; cardId?: string } | null
  /** Knowledge-base left-outline expanded project ids (global projects). */
  kbExpandedProjectIds?: string[]
}

export type UiExpandAction = 'none' | 'fold-expand-collapse' | 'fold-expand' | 'always-open'

export const DEFAULT_UI_EXPAND_DURATION_MS = 200

/** Per-UI-kind expansion choreography. Keys are the semantic UI kinds
 *  (ToolKind values plus 'reasoning'); each maps to one of the four
 *  configurable actions. Reasoning defaults to 'none' (stay folded, manual
 *  toggle only); the rest mirror the pre-config behavior. */
export const DEFAULT_UI_EXPANSION_MODES: Record<string, UiExpandAction> = {
  reasoning: 'none',
  edit: 'fold-expand',
  bash: 'fold-expand-collapse',
  read: 'fold-expand-collapse',
  search: 'fold-expand-collapse',
  glob: 'fold-expand-collapse',
  list: 'fold-expand-collapse',
  explore: 'fold-expand-collapse',
  web: 'fold-expand-collapse',
  card: 'fold-expand-collapse',
  mcp: 'fold-expand-collapse',
  task: 'fold-expand-collapse',
  agent_review: 'fold-expand-collapse',
  page_preview: 'always-open',
  media_gen: 'always-open',
  generic: 'fold-expand-collapse',
}

/** Ordered kind list for the settings UI (labelled by i18n key suffix). */
export const UI_EXPANSION_KINDS: ReadonlyArray<{ kind: string; labelKey: string }> = [
  { kind: 'reasoning', labelKey: 'settings.general.expandKindReasoning' },
  { kind: 'edit', labelKey: 'settings.general.expandKindEdit' },
  { kind: 'bash', labelKey: 'settings.general.expandKindBash' },
  { kind: 'search', labelKey: 'settings.general.expandKindSearch' },
  { kind: 'read', labelKey: 'settings.general.expandKindRead' },
  { kind: 'glob', labelKey: 'settings.general.expandKindGlob' },
  { kind: 'list', labelKey: 'settings.general.expandKindList' },
  { kind: 'explore', labelKey: 'settings.general.expandKindExplore' },
  { kind: 'web', labelKey: 'settings.general.expandKindWeb' },
  { kind: 'card', labelKey: 'settings.general.expandKindCard' },
  { kind: 'mcp', labelKey: 'settings.general.expandKindMcp' },
  { kind: 'task', labelKey: 'settings.general.expandKindTask' },
  { kind: 'agent_review', labelKey: 'settings.general.expandKindAgentReview' },
  { kind: 'page_preview', labelKey: 'settings.general.expandKindPagePreview' },
  { kind: 'media_gen', labelKey: 'settings.general.expandKindMediaGen' },
  { kind: 'generic', labelKey: 'settings.general.expandKindGeneric' },
]

export interface MultiConsoleGridConfig {
  cols: number
  rows: number
  slots?: Array<string | null>
}

export const defaultMultiConsoleGridConfig: MultiConsoleGridConfig = {
  cols: 2,
  rows: 2,
}

export async function getAIShellLayoutState(): Promise<AIShellLayoutState | null> {
  const model = await loadWorkspaceUIModel()
  const layoutJson = model.AiShell?.LayoutJson
  if (!layoutJson) return null
  try {
    return JSON.parse(layoutJson) as AIShellLayoutState
  } catch { return null }
}

export async function saveAIShellLayoutState(state: AIShellLayoutState): Promise<void> {
  await saveWorkspaceUIModel('AiShell', (model) => {
    const existing = model.AiShell ?? {}
    let existingLayout: AIShellLayoutState = {}
    if (existing.LayoutJson) {
      try {
        existingLayout = JSON.parse(existing.LayoutJson) as AIShellLayoutState
      } catch {
        existingLayout = {}
      }
    }
    const merged: AIShellLayoutState = { ...existingLayout, ...state }
    return {
      AiShell: {
        ...existing,
        LayoutJson: JSON.stringify(merged),
      },
    }
  })
}

export const defaultCategoryExpansion: Record<string, boolean> = {}

export async function getCategoryExpansion(): Promise<Record<string, boolean>> {
  const layout = await getAIShellLayoutState()
  const value = layout?.categoryExpansion
  if (!value || typeof value !== 'object' || Array.isArray(value)) return defaultCategoryExpansion
  return Object.fromEntries(Object.entries(value).filter(([, open]) => typeof open === 'boolean'))
}

export async function saveCategoryExpansion(data: Record<string, boolean>): Promise<void> {
  await saveAIShellLayoutState({ categoryExpansion: data })
}

export async function toggleCategoryExpansion(key: string, defaultOpen = false): Promise<void> {
  await saveWorkspaceUIModel('AiShell', (model) => {
    const existing = model.AiShell ?? {}
    let layout: AIShellLayoutState = {}
    if (existing.LayoutJson) {
      try {
        layout = JSON.parse(existing.LayoutJson) as AIShellLayoutState
      } catch {
        layout = {}
      }
    }
    const categoryExpansion = layout.categoryExpansion && typeof layout.categoryExpansion === 'object' && !Array.isArray(layout.categoryExpansion)
      ? Object.fromEntries(Object.entries(layout.categoryExpansion).filter(([, open]) => typeof open === 'boolean'))
      : {}
    categoryExpansion[key] = !(categoryExpansion[key] ?? defaultOpen)
    return {
      AiShell: {
        ...existing,
        LayoutJson: JSON.stringify({ ...layout, categoryExpansion }),
      },
    }
  })
}

export async function getMultiConsoleGridConfig(): Promise<MultiConsoleGridConfig> {
  const layout = await getAIShellLayoutState()
  const grid = layout?.multiconsoleGrid
  if (!grid || !Number.isInteger(grid.cols) || !Number.isInteger(grid.rows) || grid.cols < 1 || grid.cols > 5 || grid.rows < 1 || grid.rows > 5) {
    return defaultMultiConsoleGridConfig
  }
  return grid
}

export async function saveMultiConsoleGridConfig(config: MultiConsoleGridConfig): Promise<void> {
  await saveAIShellLayoutState({ multiconsoleGrid: config })
}

export async function getPinnedModes(): Promise<string[]> {
  const layout = await getAIShellLayoutState()
  const modes = layout?.pinnedModes
  if (!Array.isArray(modes)) return []
  return modes.filter(id => typeof id === 'string' && id.length > 0)
}

export async function getComposerBadgesVisible(): Promise<boolean> {
  const layout = await getAIShellLayoutState()
  return layout?.composerBadgesVisible ?? false
}

export async function saveComposerBadgesVisible(visible: boolean): Promise<void> {
  await saveAIShellLayoutState({ composerBadgesVisible: visible })
}

export interface TurnTailMetricsVisible {
  budgetBar: boolean
  tokenBadge: boolean
  duration: boolean
  throughput: boolean
}

export const defaultTurnTailMetricsVisible: TurnTailMetricsVisible = {
  budgetBar: false,
  tokenBadge: false,
  duration: false,
  throughput: false,
}

export async function getTurnTailMetricsVisible(): Promise<TurnTailMetricsVisible> {
  const layout = await getAIShellLayoutState()
  return {
    budgetBar: layout?.turnBudgetBarVisible ?? defaultTurnTailMetricsVisible.budgetBar,
    tokenBadge: layout?.turnTokenBadgeVisible ?? defaultTurnTailMetricsVisible.tokenBadge,
    duration: layout?.turnDurationBadgeVisible ?? defaultTurnTailMetricsVisible.duration,
    throughput: layout?.turnThroughputVisible ?? defaultTurnTailMetricsVisible.throughput,
  }
}

export async function saveTurnTailMetricsVisible(visible: TurnTailMetricsVisible): Promise<void> {
  await saveAIShellLayoutState({
    turnBudgetBarVisible: visible.budgetBar,
    turnTokenBadgeVisible: visible.tokenBadge,
    turnDurationBadgeVisible: visible.duration,
    turnThroughputVisible: visible.throughput,
  })
}

export interface ComposerExtrasVisible {
  quickGit: boolean
  mobileContextAgent: boolean
}

export const defaultComposerExtrasVisible: ComposerExtrasVisible = {
  quickGit: true,
  mobileContextAgent: true,
}

export async function getComposerExtrasVisible(): Promise<ComposerExtrasVisible> {
  const layout = await getAIShellLayoutState()
  return {
    quickGit: layout?.quickGitVisible ?? defaultComposerExtrasVisible.quickGit,
    mobileContextAgent: layout?.mobileContextAgentVisible ?? defaultComposerExtrasVisible.mobileContextAgent,
  }
}

export async function saveComposerExtrasVisible(visible: ComposerExtrasVisible): Promise<void> {
  await saveAIShellLayoutState({
    quickGitVisible: visible.quickGit,
    mobileContextAgentVisible: visible.mobileContextAgent,
  })
}

export async function getSmoothStream(): Promise<boolean> {
  const layout = await getAIShellLayoutState()
  return layout?.smoothStream ?? true
}

export async function saveSmoothStream(enabled: boolean): Promise<void> {
  await saveAIShellLayoutState({ smoothStream: enabled })
}

export async function getDeveloperMode(): Promise<boolean> {
  const layout = await getAIShellLayoutState()
  return layout?.developerMode ?? false
}

export async function saveDeveloperMode(enabled: boolean): Promise<void> {
  await saveAIShellLayoutState({ developerMode: enabled })
}

export async function getProviderUserAgentVisible(): Promise<boolean> {
  const layout = await getAIShellLayoutState()
  return layout?.providerUserAgentVisible ?? false
}

export async function saveProviderUserAgentVisible(visible: boolean): Promise<void> {
  await saveAIShellLayoutState({ providerUserAgentVisible: visible })
}

export async function getUiExpansionModes(): Promise<Record<string, UiExpandAction>> {
  const layout = await getAIShellLayoutState()
  return { ...DEFAULT_UI_EXPANSION_MODES, ...(layout?.uiExpansionModes ?? {}) }
}

export async function saveUiExpansionMode(kind: string, action: UiExpandAction): Promise<void> {
  const layout = await getAIShellLayoutState()
  const modes = { ...DEFAULT_UI_EXPANSION_MODES, ...(layout?.uiExpansionModes ?? {}) }
  modes[kind] = action
  await saveAIShellLayoutState({ uiExpansionModes: modes })
}

export async function getUiExpandDurationMs(): Promise<number> {
  const layout = await getAIShellLayoutState()
  return layout?.uiExpandDurationMs ?? DEFAULT_UI_EXPAND_DURATION_MS
}

export async function saveUiExpandDurationMs(ms: number): Promise<void> {
  await saveAIShellLayoutState({ uiExpandDurationMs: ms })
}

export type ProjectCardBrowserSort = 'name-asc' | 'name-desc'

export const defaultProjectCardBrowserSort: ProjectCardBrowserSort = 'name-asc'

function encodeProjectCardBrowserSort(sortMode: ProjectCardBrowserSort): WorkspaceProjectBrowserState {
  return {
    SortMode: sortMode,
  }
}

export async function getProjectCardBrowserSort(): Promise<ProjectCardBrowserSort> {
  const model = await loadWorkspaceUIModel()
  return model.ProjectCardBrowser?.SortMode === 'name-desc' ? 'name-desc' : 'name-asc'
}

export async function saveProjectCardBrowserSort(sortMode: ProjectCardBrowserSort): Promise<void> {
  await saveWorkspaceUIModel('ProjectCardBrowser', () => ({
    ProjectCardBrowser: encodeProjectCardBrowserSort(sortMode),
  }))
}

export interface ExplorerUIState {
  activeProjectId: string | null
  selectedPath: string | null
  expandedPaths: string[]
}

export const defaultExplorerUIState: ExplorerUIState = {
  activeProjectId: null,
  selectedPath: null,
  expandedPaths: [],
}

function encodeExplorerUIState(state: ExplorerUIState): WorkspaceExplorerState {
  return {
    ActiveProjectId: state.activeProjectId ?? '',
    SelectedPath: state.selectedPath ?? '',
    ExpandedPaths: state.expandedPaths,
  }
}

export async function getExplorerUIState(): Promise<ExplorerUIState> {
  const model = await loadWorkspaceUIModel()
  const expandedPaths = model.Explorer?.ExpandedPaths
  return {
    activeProjectId: model.Explorer?.ActiveProjectId || null,
    selectedPath: model.Explorer?.SelectedPath || null,
    expandedPaths: Array.isArray(expandedPaths) ? expandedPaths.filter(path => typeof path === 'string') : [],
  }
}

export async function saveExplorerUIState(state: ExplorerUIState): Promise<void> {
  await saveWorkspaceUIModel('Explorer', () => ({
    Explorer: encodeExplorerUIState(state),
  }))
}
export async function getExpandedProjectIds(): Promise<Set<string>> {
  const layout = await getAIShellLayoutState()
  const ids = layout?.expandedProjectIds
  if (!Array.isArray(ids)) return new Set()
  return new Set(ids.filter(id => typeof id === 'string' && id.length > 0))
}

export async function toggleExpandedProjectId(projectId: string): Promise<void> {
  if (projectId.length === 0) return
  await saveWorkspaceUIModel('AiShell', (model) => {
    const existing = model.AiShell ?? {}
    let layout: AIShellLayoutState = {}
    if (existing.LayoutJson) {
      try {
        layout = JSON.parse(existing.LayoutJson) as AIShellLayoutState
      } catch {
        layout = {}
      }
    }
    const ids = Array.isArray(layout.expandedProjectIds)
      ? layout.expandedProjectIds.filter(id => typeof id === 'string')
      : []
    const next = new Set(ids)
    if (next.has(projectId)) next.delete(projectId)
    else next.add(projectId)
    return {
      AiShell: {
        ...existing,
        LayoutJson: JSON.stringify({ ...layout, expandedProjectIds: Array.from(next) }),
      },
    }
  })
}

/* ───────────────────── Knowledge-base (notes mode) ───────────────────── */

/**
 * The three knowledge-base quick views. The union and its guard are declared
 * once in the quick-view logic module ([[kb-quick-views]]) and re-exported here
 * under the durable accessor's historical names, so the wire/durable protocol
 * has a single source of truth (`kbQuickViews.logic`).
 */
export type { KbQuickViewId }
export { isKbQuickViewId }

/** Active knowledge-base lane: the project (and optional root card) shown in the
 *  right column. Replaces the old `notesProjectId ?? activeProject` binding. */
export interface KbNotesLane {
  projectId: string
  projectName: string
  /** Root card id; absent = the project's own root card (the `toc` container). */
  cardId?: string
}

export interface KbNotesUIState {
  view: KbQuickViewId
  lane: KbNotesLane | null
  expandedProjectIds: string[]
}

export const defaultKbNotesUIState: KbNotesUIState = {
  view: 'home',
  lane: null,
  expandedProjectIds: [],
}

function sanitizeKbLane(value: unknown): KbNotesLane | null {
  if (!value || typeof value !== 'object') return null
  const lane = value as { projectId?: unknown; projectName?: unknown; cardId?: unknown }
  if (typeof lane.projectId !== 'string' || lane.projectId.length === 0) return null
  const next: KbNotesLane = {
    projectId: lane.projectId,
    projectName: typeof lane.projectName === 'string' ? lane.projectName : lane.projectId,
  }
  if (typeof lane.cardId === 'string' && lane.cardId.length > 0) next.cardId = lane.cardId
  return next
}

/** Load the durable knowledge-base UI state from the workspace actor. */
export async function getKbNotesUIState(): Promise<KbNotesUIState> {
  const layout = await getAIShellLayoutState()
  const expanded = layout?.kbExpandedProjectIds
  return {
    view: isKbQuickViewId(layout?.kbView) ? layout!.kbView as KbQuickViewId : defaultKbNotesUIState.view,
    lane: sanitizeKbLane(layout?.kbLane),
    expandedProjectIds: Array.isArray(expanded)
      ? expanded.filter((id): id is string => typeof id === 'string' && id.length > 0)
      : [],
  }
}

/** Persist a partial knowledge-base UI state (merged into the shell layout JSON). */
export async function saveKbNotesUIState(patch: Partial<KbNotesUIState>): Promise<void> {
  const next: AIShellLayoutState = {}
  if (patch.view !== undefined) next.kbView = patch.view
  if (patch.lane !== undefined) next.kbLane = patch.lane
  if (patch.expandedProjectIds !== undefined) next.kbExpandedProjectIds = patch.expandedProjectIds
  await saveAIShellLayoutState(next)
}
