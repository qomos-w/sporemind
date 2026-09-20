import React, { useReducer, useCallback, useMemo, useState, useEffect, useRef, useSyncExternalStore } from 'react'
import { wailsProduction, buildType } from '../../config/buildConfig'
import type {
  AccountSnapshot,
  SessionSnapshot,
  ProjectRef,
} from '../../gen-types/workspace'
import type {
  ModelUnit,
  ProjectSnapshot,
} from '../../domain/types'
import { client } from '../../application/generated-client'
import * as projectClient from '../../gen-clients/project/client'
import * as appmanagerClient from '../../gen-clients/appmanager/client'
import { mapProjectRef, projectDisplayName } from '../../application/project-adapter'
import { projectCardStore } from '../../application/project-card-client'
import { ProjectCardsIndicator } from './components/ProjectCardsIndicator'
import { WorkbenchSurface } from '../workbench/WorkbenchSurface'
import type { LauncherMode } from './components/SidebarModeSwitch'
import { consumePrefetchedShellContext, getPrefetchError } from '../../application/shell-context-prefetch'
import { savePreference } from '../../application/theme-persist'
import * as workspace_preferences from '../../gen-clients/workspace/client'
import type { AgentChatSubmitReq } from '../../gen-types/agent.chat'
import * as agent_chat from '../../gen-clients/local/client'
import * as agent_permission_mode from '../../gen-clients/local/client'
import * as aimanager_aggregator from '../../gen-clients/aimanager/client'
import * as workspace from '../../gen-clients/workspace/client'
import type { SystemTreeNode, SystemTreeResp, AgentRef, ModelSlot } from '../../gen-clients/system/types'
import { gitStore } from '../panels/git-store'
import { updateAgentState } from './hooks/updateAgentState'
import { unitToSlot, autoSlot, aggregatorSlot, pinnedUnitSlot, SYSTEM_AGGREGATOR_ID, selectionToSlot } from './hooks/modelSlot'
import type { AttachmentEntry, ImageEntry } from '../../gen-types/agent.chat'
import type { AggregatorDescriptor } from '../../gen-types/aigen'
import type { RandomNameConfig } from '../../gen-types/workspace'
import type { AIShellState, AIShellAction, ViewMode } from './model/ai-types'
import type { Frame, SessionGoal } from './model/frame-types'
import { useResponsiveLayout } from './hooks/useResponsiveLayout'
import { useMonoStore } from './hooks/useMonoStore'
import { monoStore } from '../panels/mono-store'
import {
  defaultAIShellProjectUIState,
  getAIShellProjectUIState,
  saveAIShellProjectUIState,
  getAIShellLayoutState,
  saveAIShellLayoutState,
  getAIShellSessionState,
  saveAIShellSessionState,
  getPinnedModes,
  getComposerBadgesVisible,
  saveComposerBadgesVisible,
  getDeveloperMode,
  saveDeveloperMode,
  getProviderUserAgentVisible,
  saveProviderUserAgentVisible,
  getTurnTailMetricsVisible,
  saveTurnTailMetricsVisible,
  defaultTurnTailMetricsVisible,
  type TurnTailMetricsVisible,
  getComposerExtrasVisible,
  saveComposerExtrasVisible,
  defaultComposerExtrasVisible,
  type ComposerExtrasVisible,
  getSmoothStream,
  saveSmoothStream,
  getUiExpansionModes,
  saveUiExpansionMode,
  getUiExpandDurationMs,
  saveUiExpandDurationMs,
  DEFAULT_UI_EXPANSION_MODES,
  DEFAULT_UI_EXPAND_DURATION_MS,
  type UiExpandAction,
} from '../../application/workspace-ui-state'
import { wailsWindowController } from '../../application/window-controller'
import { useSidebarInteraction } from './hooks/useSidebarInteraction'
import { getTimelineManager } from './hooks/useTimelineManager'
import { useAIShellSource } from './hooks/useAIShellSource'
import { useTurnCompleteNotifier } from './hooks/useTurnCompleteNotifier'
import { useToastActionEvents } from '../toast/useToastActionEvents'
import { ToastOverlay } from '../toast/ToastOverlay'
import { SchemaOverlayProvider, useSchemaOverlay } from '../schema-overlay/useSchemaOverlay'
import { jsonSchemaFromSchemaId } from '../schema-overlay/spore-converter'
import type { JSONSchema } from '../schema-overlay/json-schema'
import { loadNotifyConfig, playInteractionSound } from './notify-sound'
import { useAgentInfoList, useAgentInfoWithCompaction, type AgentInfo, aggregateProjectStatus } from './hooks/agentInfoStore'
import { setActiveAgentId } from './hooks/agentSessionStore'
import { getComposerDraft, setComposerDraft, clearComposerDraft } from './hooks/composerDraftStore'
import { useAIShellTheme } from './hooks/useAIShellTheme'
import { useAIShellProviders } from './hooks/useAIShellProviders'
import { useAgentOperations } from './hooks/useAgentOperations'
import { useLocalAppInstall } from './hooks/useLocalAppInstall'
import { useNavHistory } from './hooks/useNavHistory'
import { consumePendingAgentChat } from './pendingAgentChat'
import { AIShellContext } from './context/AIShellContext'
import { SmoothStreamContext } from './context/SmoothStreamContext'
import { InteractionDispatchContext } from './hooks/useStepInteraction'
import { AIShellSidebar, type ContentMode } from './components/AIShellSidebar'
import { AIConversationPage } from './components/AIConversationPage'
import { AIConversationComposer, QuickSwitchMenu } from './components/AIConversationComposer'
import { resolveTaskMode } from './components/scheduledTasks'
import type { ComposerBadge, ComposerSlotMenuConfig } from './components/AIComposer'
import type { SlotName } from './components/ProviderSlotMenu'
import { resolveComposerPlaceholderKey, type ComposerPlaceholderContext } from './composerPlaceholderRegistry'
import { GitQuickbar } from './components/GitQuickbar'
import { ComposerFrame } from './components/ComposerFrame'
import { AIShellTopbar } from './components/AIShellTopbar'
import { NewAgentDialog, type AgentDialogInput } from './components/NewAgentDialog'
import { NewProjectDialog } from '../panels/NewProjectDialog'
import { OnboardingFlow } from './components/OnboardingFlow'
import { AgentContextMenu, type AgentMenuTarget } from './components/AgentContextMenu'
import { AgentInspectorPanel } from './components/AgentInspectorPanel'
import { ProjectContextMenu, type ProjectMenuTarget } from './components/ProjectContextMenu'
import { ProjectPropertiesOverlay } from './components/ProjectPropertiesOverlay'
import { RenameProjectDialog } from './components/RenameProjectDialog'
import { openInSystem } from './components/file-ops'
import { agentDisplayName } from './lib/agent-avatar'
import { randomWelcome } from './lib/greetingPhrases'
import { truncateLabel } from './components/timeline'
import { applyParentChildOrder } from './lib/agent-order'
import { DeleteConfirmModal } from './components/parts/DeleteConfirmModal'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { InstallPreviewDialog } from '../components/InstallPreviewDialog'
import { MultiConsoleContent } from '../multiconsole/MultiConsoleContent'
import { ShellSshContent } from './components/ShellSshContent'
import { KnowledgeModeView, type KnowledgeModeOpenRequest } from './components/KnowledgeModeView'
import { FileReferenceProvider } from './components/parts/file-reference.tsx'
import { topoFiltersStore } from './components/topoFiltersStore'
import './registerModeClickActions'
import { TopologyModeView, type TopoMode } from './components/TopologyModeView'
import { ScheduledView } from './components/ScheduledView'
import { archivedBucketIdOf, workflowOwnerAgentId, workflowTaskIds } from './components/workflowLayout'
import { openWorkflowAndLocate, requestWorkflowLocate, requestWorkflowDirectionToggle } from './components/workflowLocateStore'
import { setTurnTailMetricsVisible as setTurnTailMetricsStore } from './components/turnTailMetricsStore'
import { workflowFoldStore } from './components/workflowFoldStore'
import { TopologyContextMenu, type TopologyMenuTarget } from './components/TopologyContextMenu'
import { TopologyCardContextMenu, type CardMenuTarget } from './components/TopologyCardContextMenu'
import { WorkflowBlankContextMenu, type WorkflowBlankMenuTarget } from './components/WorkflowBlankContextMenu'
import { SchedulerGraphContextMenu, type SchedulerGraphMenuTarget } from './components/SchedulerGraphContextMenu'
import { ScheduleModal, type SchedulerAgentSelection } from './components/ScheduleModal'
import { cronToDraft, draftToCron, type ScheduleDraft } from './components/scheduledTasks'
import { TopologyEdgeContextMenu, type EdgeMenuTarget } from './components/TopologyEdgeContextMenu'
import type { TopologyEdgeRef } from './components/TopologyGraph'
import { CardVisualDialog } from './components/CardVisualDialog'
import { MiniComposer, type MiniComposerAnchor } from './components/MiniComposer'
import { NameInputDialog } from '../components/NameInputDialog'
import type { UnifiedGraphNode } from '../../gen-types/observation'
import { MonoCardPanel, MonoCardDraftPanel } from './components/MonoCardPanel'
import { AssignGoalDialog, type AssignGoalInput } from './components/AssignGoalDialog'
import { MemoryNodePanel } from './components/MemoryNodePanel'
import type { MemoryNode } from '../../gen-clients/system/types'
import { agentCardId, type MonoCardListItem } from '../../domain/mono-types'
import { resolveCardId, tagMatchesCard } from '../../domain/builtin-cards'
import { normalizePermissionMode, sortHistory, type PermissionMode, type ProviderOption } from './components/AIComposer'
import { RightPanelTabs, type RightTabItem, type NewTabOption } from './components/RightPanelTabs'
import { ResizeHandle } from '../components/ResizeHandle'
import { SettingsSidebar } from './components/SettingsSidebar'
import { SettingsContent } from './components/SettingsContent'
import { MobileSyncPanel } from './components/MobileSyncPanel'
import { TerminalPanel } from './components/terminal/TerminalPanel'
import { AppOmniboxOverlay } from './components/AppOmniboxOverlay'
import { GlobalFindReplaceOverlay } from './components/GlobalFindReplaceOverlay'
import { GuideOverlay } from './components/GuideOverlay'
import { DelegatedTooltip } from './overlay/DelegatedTooltip'
import { ToolGuideOverlay } from './components/ToolGuideOverlay'
import { AboutOverlay } from './components/AboutOverlay'
import { RegistrationPermissionOverlay } from './components/RegistrationPermissionOverlay'
import { toolGuideManager } from './components/tool-guide-manager'
import { getTutorialCategory, dynamicTutorialStore, type TutorialContext } from '../../application/tutorial-registry'
import { useAppOmnibox, type ProjectInfo } from './hooks/useAppOmnibox'
import { useBrowserOverlay, useBrowserOverlayOpen, useBrowserPreHideCapture, decodeSnapshotImage, afterNextPaint } from './browserOverlay'
import { categories } from './components/settings-data'
import { FrameRenderer } from './components/parts/FrameRenderer'
import { FileDiffViewer } from './components/FileDiffViewer'
import FileBrowser from './components/FileBrowser'
import { FileClipboardProvider } from './components/file-clipboard-context'
import { EditorKeymapProvider } from '../editor/EditorKeymapContext'
import { ImageViewer, base64ToBytes } from './components/ImageViewer'
import { CodeMirrorViewer } from './components/CodeMirrorViewer'
import { isImageExt } from './components/parts/image-utils'
import { parseListText } from './components/parts/list-text'
import { collectOsDroppedEntries, OS_DROP_TARGET_ATTR } from '../os-file-dnd'
import { bytesLookBinary, dropIsUnclaimed, imageDataUrl, osDropOpenKind } from '../os-drop-open'
import { BrowserView, type BrowserKind } from './components/BrowserView'
import { ReviewPanel } from './components/ReviewPanel'
import { AppListPanel } from './components/AppListPanel'
import { GitPanel } from './components/GitPanel'
import { SidebarLauncher, buildLauncherApps, hasLaunchableApps, pushRecent, reorderIdList, type LauncherApp, type LauncherSection } from './components/SidebarLauncher'
import { PluginTabView } from '../components/PluginToolbar'
import { appRegistry, EMPTY_APP_ENTRIES, type AppEntry } from '../../application/app-registry'
import type { PluginView } from '../../application/plugin-registry'
import { Globe, FileSearch, MessageCircle, Network, LayoutGrid, Folder, FolderPlus, FolderOpen, Terminal, AlertTriangle, Gauge, Settings, MonitorSmartphone, Waypoints, Layers, GitBranch, FileCode, ShieldCheck, Database, Cloud } from 'lucide-react'
import { resolveAppIcon } from './components/appIconResolver'
import { ProblemsPanel } from '../panels/ProblemsPanel'
import { ApprovalAuditPanel } from '../panels/ApprovalAuditPanel'
import { SshSessionView } from '../views/SshSessionView'
import { ShellDbClientView } from './components/ShellDbClientView'
import { ShellDbContent } from './components/ShellDbContent'
import { ObjectStorageSessionView } from '../views/ObjectStorageSessionView'
import { SshRemoteFileEditor } from './components/SshRemoteFileEditor'
import * as sshmanagerClient from '../../gen-clients/sshmanager/client'
import * as dbclientClient from '../../gen-clients/dbclient/client'
import { sshTabId, dbTabId, objectStorageTabId, pluginTabId, APP_LIST_TAB_ID } from './tabIds'
import { AIStatsDashboard } from './components/AIStatsDashboard'
import { GlassDebugPanel } from '../glass/GlassDebugPanel'
import { PuppetEditorPanel } from '../puppet/PuppetEditorPanel'
import { QuickStartActions } from './components/QuickStartActions'
import { requestModeMount } from './components/modeMountStore'
import { SuggestionChips } from './components/SuggestionChips'
import { useHasGitRepo } from './hooks/useHasGitRepo'
import { useComposerHistory, COMPOSER_HISTORY_SCOPE } from './hooks/useComposerHistory'
import { ProjectNewChatPage } from './components/ProjectNewChatPage'
import type { ThemeState } from '@qomos/sporemind-theme'
import { useI18n } from '../../i18n'
import { isWails } from '../../application/runtime'
import { emitWailsEvent } from '../../application/wails-runtime'
import { setBrowserWindowVisible, closeBrowserSession, captureBrowserWindow, setBrowserColorScheme, navigateBrowserSession } from '../../application/wails-bridge'
import type { BrowserManagerEvent } from '../../gen-types/browser'
import { listBrowserWindows, closeBrowserInstance, onBrowserManagerEvent, proxySignature } from '../../application/browser-manager'
import { onInterfaceManagerEvent, fetchTutorialCatalog } from '../../application/interface-manager'
import { requestPluginDomSnapshot } from '../../application/plugin-bridge'
import { guideManager } from '../../application/guide-manager'
import { initTelemetry, setTelemetryView, reportInteraction } from '../../application/telemetry'
import { executeInteraction } from '../../application/ui-interactor'
import type { InteractionPrimitive } from '../../application/ui-interactor'
import './AIShellLayout.css'

const LAST_SETTINGS_CATEGORY_KEY = 'sporemind:shell:settings:last-category'

const validSettingCategoryIds = new Set(categories.map(cat => cat.id))

/** Guard for persisted id-array layout fields: must be an array of strings. */
function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every(id => typeof id === 'string')
}

// Content view modes drivable via interfacemanager.control (action="set_view").
const INTERFACE_VIEW_MODES: ReadonlySet<string> = new Set([
  'conversation', 'topology', 'workflow', 'scheduled', 'files', 'problems', 'approvals', 'notes', 'ssh', 'multiconsole', 'git',
])

/** Content modes that benefit from staying mounted (cached) across switches.
 *  Heavy graph views (topology, workflow) and stateful panels (files, notes,
 *  multiconsole, ssh) are kept alive with display:none so switching back is
 *  instant — no vis-network recreation, no async preference flash. */
const CACHEABLE_MODES: ReadonlySet<string> = new Set([
  'topology', 'workflow', 'scheduled', 'multiconsole', 'files', 'notes', 'ssh', 'problems', 'approvals', 'aistats', 'git',
])

let pendingContentMode: ContentMode | null = null

export function setPendingShellContentMode(mode: ContentMode) {
  pendingContentMode = mode
}

export function getPendingShellContentMode(): ContentMode | null {
  return pendingContentMode
}

function loadLastSettingsCategory(): string {
  try {
    const raw = localStorage.getItem(LAST_SETTINGS_CATEGORY_KEY)
    if (raw && validSettingCategoryIds.has(raw)) return raw
  } catch {
    // ignore storage errors
  }
  return 'general'
}

function reorderTabs<T extends { id: string }>(tabs: T[], order: string[] | undefined): T[] {
  if (!order || order.length === 0) return tabs
  const orderMap = new Map(order.map((id, idx) => [id, idx]))
  const sorted = [...tabs]
  sorted.sort((a, b) => {
    const ai = orderMap.get(a.id)
    const bi = orderMap.get(b.id)
    if (ai !== undefined && bi !== undefined) return ai - bi
    if (ai !== undefined) return -1
    if (bi !== undefined) return 1
    return 0
  })
  return sorted
}

/**
 * toIndex is the drop-gap index in the ORIGINAL tab array (0..N, resolved from
 * the mouse pointer against tab edges by RightPanelTabs). Once the dragged tab
 * is spliced out, every gap to its right shifts left by one, so a rightward
 * drop must insert at toIndex - 1 — otherwise the tab lands one slot right of
 * where the pointer indicated. Pure; unit-tested in AIShellLayout.browser-tab.test.tsx.
 */
export function resolveTabInsertIndex(fromIdx: number, toIndex: number, lengthAfterRemoval: number): number {
  const adjusted = fromIdx < toIndex ? toIndex - 1 : toIndex
  return Math.max(0, Math.min(adjusted, lengthAfterRemoval))
}

/** Extract SSH right-panel tabs for layout persistence: stable tabId + hostId
 *  + hostName only. The volatile sessionId is excluded so a restart reopens the
 *  tab and calls shellOpen to obtain a fresh session. */
function extractSshTabsForPersist(
  tabs: Array<{ id: string; type: string; payload: unknown }>,
): Array<{ id: string; hostId: string; hostName: string }> {
  return tabs
    .filter((t) => t.type === 'ssh-session')
    .map((t) => {
      const p = t.payload as { hostId: string; hostName: string }
      return { id: t.id, hostId: p.hostId, hostName: p.hostName }
    })
}

/** Extract DB right-panel tabs for layout persistence: stable tabId +
 *  profileId + profileName + backend. dbclient re-dials lazily on first use
 *  (no volatile handle id to persist). */
function extractDbTabsForPersist(
  tabs: Array<{ id: string; type: string; payload: unknown }>,
): Array<{ id: string; profileId: string; profileName: string; backend: string }> {
  return tabs
    .filter((t) => t.type === 'db-session')
    .map((t) => {
      const p = t.payload as { profileId: string; profileName: string; backend: string }
      return { id: t.id, profileId: p.profileId, profileName: p.profileName, backend: p.backend }
    })
}

/** Plugin tabs the keep-open rule wants present in the right panel: one tab
 * per `view` entrypoint of every RUNNING app (dead/crashed apps must not
 * launch — their iframe would sit on the restart-error state). Ids already
 * open and ids the user closed this session are skipped. When `onlyTabIds`
 * is given the result is additionally restricted to that allow-list — the
 * boot restore uses it to reopen exactly the plugin tabs that were open at
 * shutdown instead of every running app's views. Pure; unit-tested in
 * AIShellLayout.browser-tab.test.tsx. */
export function computeAutoOpenPluginTabs(
  apps: ReadonlyArray<Pick<AppEntry, 'id' | 'state' | 'icon' | 'color' | 'entrypoints'>>,
  existingTabIds: ReadonlySet<string>,
  sessionClosedIds: ReadonlySet<string>,
  onlyTabIds?: ReadonlySet<string>,
): Array<{ id: string; label: string; view: PluginView }> {
  const out: Array<{ id: string; label: string; view: PluginView }> = []
  for (const app of apps) {
    if (app.state !== 'running') continue
    for (const entry of app.entrypoints) {
      if (entry.kind !== 'view') continue
      const id = pluginTabId(app.id, entry.id)
      if (existingTabIds.has(id) || sessionClosedIds.has(id)) continue
      if (onlyTabIds && !onlyTabIds.has(id)) continue
      out.push({
        id,
        label: entry.title,
        view: { id: entry.id, pluginID: app.id, title: entry.title, route: entry.route ?? '/', icon: app.icon, color: app.color },
      })
    }
  }
  return out
}

/** Extract plugin right-panel tabs for layout persistence: the tabId
 * components only. The registry is the source of truth for
 * title/route/icon/color, so the restore path re-resolves them from the
 * (running) app entrypoints instead of persisting volatile display data. */
function extractPluginTabsForPersist(
  tabs: Array<{ id: string; type: string; payload: unknown }>,
): Array<{ id: string; pluginID: string; viewID: string }> {
  return tabs
    .filter((t) => t.type === 'plugin')
    .map((t) => {
      const view = (t.payload as { view?: PluginView }).view
      return { id: t.id, pluginID: view?.pluginID ?? '', viewID: view?.id ?? '' }
    })
    .filter((t) => t.pluginID !== '' && t.viewID !== '')
}

function saveLastSettingsCategory(id: string) {
  try {
    if (validSettingCategoryIds.has(id)) {
      localStorage.setItem(LAST_SETTINGS_CATEGORY_KEY, id)
    }
  } catch {
    // ignore storage errors
  }
}

interface ProjectUIState {
  sidebarOrder: string[]
  agentOrder: string[]
}

interface AgentKindOption {
  kind: string
  displayName: string
  randomName: RandomNameConfig | undefined
  namePool?: string[]
}

const defaultProjectUIState: ProjectUIState = defaultAIShellProjectUIState

function orderedProjectIds(projects: ProjectSnapshot[], state: ProjectUIState): string[] {
  const safe = projects ?? []
  const systemIds = safe.filter(project => project.System).map(project => project.ProjectID)
  const systemSet = new Set(systemIds)
  const userProjects = safe.filter(project => !project.System)
  const ids = new Set(userProjects.map(project => project.ProjectID))
  const knownOrder = state.sidebarOrder.filter(id => ids.has(id) && !systemSet.has(id))
  const unseen = userProjects.map(project => project.ProjectID).filter(id => !knownOrder.includes(id))
  return [...systemIds, ...knownOrder, ...unseen]
}

function orderedAgentIds(
  agents: AgentInfo[],
  agentOrder: string[] | undefined,
  projectOrder: string[] | undefined,
): string[] {
  const safeAgentOrder = agentOrder ?? []
  const safeProjectOrder = projectOrder ?? []

  const byProject = new Map<string, AgentInfo[]>()
  for (const agent of agents) {
    const list = byProject.get(agent.ProjectId) ?? []
    list.push(agent)
    byProject.set(agent.ProjectId, list)
  }

  const sortedByProject = new Map<string, string[]>()
  for (const [projectId, projectAgents] of byProject) {
    const ids = new Set(projectAgents.map(agent => agent.Id))
    const known = safeAgentOrder.filter(id => ids.has(id))
    const unseen = projectAgents.map(agent => agent.Id).filter(id => !known.includes(id))
    sortedByProject.set(projectId, [...known, ...unseen])
  }

  const knownProjectIds = new Set(agents.map(agent => agent.ProjectId))
  const orderedProjectIds = [
    ...safeProjectOrder.filter(id => knownProjectIds.has(id)),
    ...[...knownProjectIds].filter(id => !safeProjectOrder.includes(id)),
  ]

  return orderedProjectIds.flatMap(projectId => sortedByProject.get(projectId) ?? [])
}

function applyAgentOrder(
  agents: AgentInfo[],
  agentOrder: string[] | undefined,
  projectOrder: string[] | undefined,
): AgentInfo[] {
  const byId = new Map(agents.map(agent => [agent.Id, agent]))
  const order = orderedAgentIds(agents, agentOrder, projectOrder)
  const ordered = order.map(id => byId.get(id)).filter((agent): agent is AgentInfo => Boolean(agent))
  // Flatten the parent-child tree depth-first so the avatar bar matches the
  // sidebar: each agent's whole subtree follows it before the next root.
  return applyParentChildOrder(ordered)
}

function orderedSidebarProjects(projects: ProjectSnapshot[], state: ProjectUIState): ProjectSnapshot[] {
  const safe = projects ?? []
  const byId = new Map(safe.map(project => [project.ProjectID, project]))
  const order = orderedProjectIds(projects, state)
  return order
    .map(id => byId.get(id))
    .filter((project): project is ProjectSnapshot => Boolean(project))
}

function promoteProject(projectId: string, current: ProjectUIState): ProjectUIState {
  // System meta projects are always forced to the front by orderedProjectIds; keep
  // user-driven order for non-system meta projects only.
  const order = current.sidebarOrder.filter(id => id !== projectId)
  return { ...current, sidebarOrder: [projectId, ...order] }
}

function dependsOnIds(card: MonoCardListItem): string[] {
  const deps = card.data?.depends_on
  if (Array.isArray(deps)) return deps.map(String)
  if (typeof deps === 'string') return deps.replace(/^\[|\]$/g, '').split(',').map(id => id.trim()).filter(Boolean)
  return []
}

function frameTypeLabel(frame: { type: string; toolName?: string }, t: ReturnType<typeof useI18n>['t']): string {
  switch (frame.type) {
    case 'text': return t('ai.frameType.text')
    case 'reasoning': return t('ai.frameType.reasoning')
    case 'tool': return frame.toolName || t('ai.frameType.tool')
    case 'sources': return t('ai.frameType.sources')
    case 'attachments': return t('ai.frameType.attachments')
    case 'image': return t('ai.frameType.image')
    case 'error': return t('ai.frameType.error')
    case 'ui': return t('ai.frameType.ui')
    case 'ask_user': return t('ai.frameType.askUser')
    case 'goal_review': return t('ai.frameType.goalReview')
    case 'goal_submit': return t('ai.frameType.goalSubmit')
    case 'goal_card_submit': return t('ai.frameType.goalCardSubmit')
    default: return t('ai.frameType.frame')
  }
}

function initialState(): AIShellState {
  return {
    activeSessionId: null,
    previousSessionId: null,
    sidebarVisible: false,
    composerValue: '',
    selectedFrame: null,
  }
}

function reducer(state: AIShellState, action: AIShellAction): AIShellState {
  switch (action.type) {
    case 'SELECT_SESSION': {
      if (state.activeSessionId) {
        setComposerDraft(state.activeSessionId, state.composerValue)
      }
      const nextDraft = action.id ? getComposerDraft(action.id) : ''
      const previousSessionId = action.id === state.activeSessionId ? state.previousSessionId : state.activeSessionId
      return { ...state, activeSessionId: action.id, previousSessionId, composerValue: nextDraft }
    }
    case 'RESTORE_SESSION': {
      // Start with workspace selected; do not restore the last agent so that
      // lazy-loaded agents are not spawned on startup.
      return {
        ...state,
        activeSessionId: null,
        previousSessionId: null,
        composerValue: '',
      }
    }
    case 'TOGGLE_SIDEBAR':
      return { ...state, sidebarVisible: !state.sidebarVisible }
    case 'SET_SIDEBAR_VISIBLE':
      return { ...state, sidebarVisible: action.value }
    case 'SET_COMPOSER_VALUE': {
      if (state.activeSessionId) {
        setComposerDraft(state.activeSessionId, action.value)
      }
      return { ...state, composerValue: action.value }
    }
    case 'SEND_MESSAGE': {
      if (state.activeSessionId) {
        clearComposerDraft(state.activeSessionId)
      }
      return { ...state, composerValue: '' }
    }
    case 'SELECT_FRAME':
      return { ...state, selectedFrame: action.frame }
    default:
      return state
  }
}

interface AIShellLayoutProps {
  embedded?: boolean
  shellVariant?: 'ai' | 'preview'
  onShellModeChange?: () => void
  theme?: ThemeState
  onThemeChange?: (theme: ThemeState) => void
}

export const AIShellLayout: React.FC<AIShellLayoutProps> = ({
  embedded = false,
  shellVariant = 'ai',
  onShellModeChange,
  theme: externalTheme,
  onThemeChange: externalOnThemeChange,
}) => {
  const { t } = useI18n()
  const inWails = isWails()
  const [state, dispatch] = useReducer(reducer, initialState())
  const monoState = useMonoStore()
  const schemaOverlay = useSchemaOverlay()
  const layout = useResponsiveLayout()
  const [maximised, setMaximised] = useState(false)
  const [rightPanelExpanded, setRightPanelExpanded] = useState(false)
  const [guideState, setGuideState] = useState({ steps: [] as ReturnType<typeof guideManager.getState>['steps'], currentIndex: 0, visible: false, gateMet: true, categoryLabel: undefined as string | undefined })
  const [account, setAccount] = useState<AccountSnapshot | null>(null)
  const [, setSession] = useState<SessionSnapshot | null>(null)
  const [projects, setProjects] = useState<ProjectSnapshot[]>([])
  const [systemTreeNodes, setSystemTreeNodes] = useState<SystemTreeNode[]>([])
  const [activeProject, setActiveProject] = useState<ProjectSnapshot | null>(null)
  const [contextError, setContextError] = useState<string | null>(null)
  const [contextSuccess, setContextSuccess] = useState<string | null>(null)
  const [contextLoading, setContextLoading] = useState(true)
  const [projectSwitchingId, setProjectSwitchingId] = useState<string | null>(null)
  const [projectUIState, setProjectUIState] = useState<ProjectUIState>(defaultProjectUIState)
  const [projectUIHydrated, setProjectUIHydrated] = useState(false)
  const [createAgentProjectId, setCreateAgentProjectId] = useState<string | null>(null)
  const [editAgent, setEditAgent] = useState<AgentInfo | null>(null)
  const editAgentInfo = useAgentInfoWithCompaction(editAgent?.ActorId ?? null)
  const [cloneAgent, setCloneAgent] = useState<AgentInfo | null>(null)
  const [forkTurn, setForkTurn] = useState<{ agent: AgentInfo; turnId: string } | null>(null)
  const [agentMenu, setAgentMenu] = useState<AgentMenuTarget | null>(null)
  const [projectMenu, setProjectMenu] = useState<ProjectMenuTarget | null>(null)
  const [renameProject, setRenameProject] = useState<ProjectMenuTarget['project'] | null>(null)
  const [propertiesProject, setPropertiesProject] = useState<ProjectMenuTarget['project'] | null>(null)
  const [closeConfirmProject, setCloseConfirmProject] = useState<ProjectMenuTarget['project'] | null>(null)
  const [closeConfirmLoading, setCloseConfirmLoading] = useState(false)
  const [deleteConfirmAgent, setDeleteConfirmAgent] = useState<AgentInfo | null>(null)
  // Deleting a workflow card (the graph's start/end nodes map to it) deletes
  // the entire workflow: the map card plus every scoped task card.
  const [deleteConfirmCard, setDeleteConfirmCard] = useState<{ card: MonoCardListItem; taskIds: string[] } | null>(null)
  // Deleting a scheduler card (cascade-deletes its timer and instances).
  const [deleteConfirmScheduler, setDeleteConfirmScheduler] = useState<{ card: MonoCardListItem; instanceCount: number } | null>(null)
  // Schedule (cron) editor modal for scheduler cards, opened from the graph
  // node's clock button or the context menu.
  const [scheduleEdit, setScheduleEdit] = useState<{ card: MonoCardListItem; draft: ScheduleDraft; saving: boolean; error: string } | null>(null)
  const pendingDeleteNodeIds = useMemo(() => {
    if (!deleteConfirmCard || deleteConfirmCard.card.type !== 'workflow') return undefined
    return new Set([deleteConfirmCard.card.id, `${deleteConfirmCard.card.id}:start`, ...deleteConfirmCard.taskIds])
  }, [deleteConfirmCard])
  const [clearConfirmAgent, setClearConfirmAgent] = useState<AgentInfo | null>(null)
  const [brainMemoryConfirmAgent, setBrainMemoryConfirmAgent] = useState<AgentInfo | null>(null)
  const [backlogIntercept, setBacklogIntercept] = useState<string | null>(null)
  const [brainMemoryMountLoading, setBrainMemoryMountLoading] = useState(false)
  const [renameCard, setRenameCard] = useState<MonoCardListItem | null>(null)
  const [renameCardValue, setRenameCardValue] = useState('')
  const [tagCard, setTagCard] = useState<MonoCardListItem | null>(null)
  const [tagCardValue, setTagCardValue] = useState('')
  const [childCard, setChildCard] = useState<MonoCardListItem | null>(null)
  const [childCardValue, setChildCardValue] = useState('')
  const [visualCard, setVisualCard] = useState<MonoCardListItem | null>(null)
  const [assignGoalCard, setAssignGoalCard] = useState<MonoCardListItem | null>(null)
  const [assignGoalSubmitting, setAssignGoalSubmitting] = useState(false)
  const [assignGoalError, setAssignGoalError] = useState<string | null>(null)
  const [startWorkflowMapId, setStartWorkflowMapId] = useState<string | null>(null)
  const [startWorkflowSubmitting, setStartWorkflowSubmitting] = useState(false)
  const [startWorkflowError, setStartWorkflowError] = useState<string | null>(null)
  const [clearConfirmLoading, setClearConfirmLoading] = useState(false)
  const [topologyMenu, setTopologyMenu] = useState<TopologyMenuTarget | null>(null)
  const [cardMenu, setCardMenu] = useState<CardMenuTarget | null>(null)
  const [cardMenuContext, setCardMenuContext] = useState<'topology' | 'workflow'>('topology')
  const [cardMenuRuns, setCardMenuRuns] = useState<string[] | undefined>(undefined)
  // Workflow fold snapshot (shared store): drives the cards-topology card menu
  // fold/unfold item. Toggling re-fetches monoStore via watchFoldChanges, which
  // applies the server-side fold filter (hidden maps' task cards drop out).
  const [foldSnapshot, setFoldSnapshot] = useState(workflowFoldStore.getSnapshot())
  useEffect(() => {
    void workflowFoldStore.ensureLoaded()
    return workflowFoldStore.subscribe(setFoldSnapshot)
  }, [])
  const handleCardToggleFold = useCallback((card: MonoCardListItem) => {
    const next = new Set(workflowFoldStore.getSnapshot().foldedWorkflows)
    const unfolding = next.has(card.id)
    if (unfolding) next.delete(card.id)
    else next.add(card.id)
    workflowFoldStore.setFoldedWorkflows(next)
    if (unfolding) {
      // The archived-bucket rule hides archived workflows' tasks even when the
      // workflow itself is unfolded; un-folding must also expand its bucket or
      // the tasks never come back and the toggle looks broken.
      const agentIds = new Set(allAgentsRef.current.map(a => a.ActorId))
      const bucket = archivedBucketIdOf(monoStore.getState().cards, card.id, { agentIds })
      if (bucket) {
        const buckets = new Set(workflowFoldStore.getSnapshot().expandedBuckets)
        buckets.add(bucket)
        workflowFoldStore.setExpandedBuckets(buckets)
      }
    }
  }, [])
  const [workflowBlankMenu, setWorkflowBlankMenu] = useState<WorkflowBlankMenuTarget | null>(null)
  const [schedulerGraphMenu, setSchedulerGraphMenu] = useState<SchedulerGraphMenuTarget | null>(null)
  const [edgeMenu, setEdgeMenu] = useState<EdgeMenuTarget | null>(null)
  const [miniComposer, setMiniComposer] = useState<{ open: boolean; anchor: MiniComposerAnchor | null; initialValue: string }>({ open: false, anchor: null, initialValue: '' })
  const [activeTopologyFilterIds, setActiveTopologyFilterIds] = useState<Set<string>>(new Set())
  const [pendingPlacement, setPendingPlacement] = useState<{ id: string; x: number; y: number } | null>(null)
  const [createNameDialog, setCreateNameDialog] = useState<{ kind: 'card' | 'backlog' | 'skill' | 'prompt'; open: boolean } | null>(null)
  const [createNameValue, setCreateNameValue] = useState('')
  const topologyPlacementRef = useRef<{ x: number; y: number } | null>(null)
  const creatingAgentFromTopologyRef = useRef(false)
  const creatingProjectFromTopologyRef = useRef(false)
  const [showNewProjectDialog, setShowNewProjectDialog] = useState(false)
  const [newProjectDialogMode, setNewProjectDialogMode] = useState<'create' | 'open'>('create')
  const [agentKinds, setAgentKinds] = useState<AgentKindOption[]>(() => {
    try {
      const raw = localStorage.getItem('sporemind:agent-kinds')
      if (raw) return JSON.parse(raw) as AgentKindOption[]
    } catch { /* ignore */ }
    return []
  })
  const [aggregators, setAggregators] = useState<AggregatorDescriptor[]>([])
  // ---- Right-panel browser tab routing model ----
  // A `type:'browser'` tab carries `payload:{ sessionId, kind, url }`.
  //   kind 'global':  workspace-shared browsing. Each user "new tab" mints a
  //                   fresh `global-<ts>` session; the agent open_global route
  //                   reuses the stable `global-agent` session. Global tabs are
  //                   NOT backed by a BrowserManager instance — they are
  //                   persisted only as ephemeral layout state (kind==='global'
  //                   filter) and closing them destroys the native window
  //                   without touching any instance config.
  //   kind 'independent': backed by a BrowserManager instance whose Id == the
  //                   tab sessionId (`browser-<instanceId>`). Restored on startup
  //                   from the instance list; closing sets the instance Open=false
  //                   (it survives and can be reopened) and destroys the native
  //                   window. Profile isolation is per-instance (backend).
  //   kind 'app':     application-managed browser (not user-creatable here).
  // Switching tabs only toggles native-window visibility (only the active
  // browser tab mounts, via key remount); it never navigates or destroys a
  // background tab's window. See [[收敛前端加载与显隐状态]].
  const [rightTabs, setRightTabs] = useState<Array<{ id: string; type: 'frame' | 'diff' | 'file' | 'image' | 'browser' | 'review' | 'agent-inspector' | 'mono-card' | 'mono-card-draft' | 'memory-node' | 'plugin' | 'glass' | 'puppet' | 'goal-text' | 'source' | 'ssh-session' | 'ssh-file' | 'db-session' | 'object-storage' | 'app-list'; label: string; payload: unknown }>>([])
  const visibleRightTabs = useMemo(() => {
    if (inWails) return rightTabs
    return rightTabs.filter((t) => t.type !== 'browser')
  }, [inWails, rightTabs])
  const [activeBrowserTabId, setActiveBrowserTabId] = useState<string | null>(null)
  const rightTabOrderRef = useRef<string[]>([])
  // True once the browser-tab restore effect has applied the saved order to
  // rightTabs. The debounced/beforeunload saves omit rightTabOrder until then
  // so the 500ms pre-restore debounce (which sees an empty/partial tab list)
  // cannot clobber the persisted user order with [].
  const browserRestoreDoneRef = useRef(false)
  // Plugin tab ids the user closed THIS session. The keep-open effect skips
  // them so closing stays meaningful mid-session; the closed set is also
  // persisted through rightPluginTabs, so a restart no longer resurrects
  // tabs the user had closed before shutdown.
  const sessionClosedPluginTabsRef = useRef<Set<string>>(new Set())
  // Plugin tabs recorded as open in the persisted layout, captured before
  // any effect mutates rightTabs. `null` marks a legacy layout that never
  // persisted the key — the boot restore then falls back to the plugin ids
  // found in the persisted rightTabOrder (which is the open-tab set, in
  // order).
  const persistedPluginTabsRef = useRef<Array<{ id: string; pluginID: string; viewID: string }> | null>(null)
  // True once the boot restore consumed the first registry snapshot. Plugin
  // tab persistence is gated on it (like rightTabOrder on
  // browserRestoreDoneRef) so the pre-restore debounce cannot clobber the
  // persisted set with [].
  const pluginBootRestoreDoneRef = useRef(false)
  // App ids observed running at least once this session. A later transition
  // to running (app started/installed mid-session) still auto-opens the
  // app's views per the keep-open rule.
  const seenRunningAppsRef = useRef<Set<string>>(new Set())
  // Latest right-panel tab ids for the keep-open effect (avoids re-running it
  // on every tab change; it only re-runs when the app registry changes).
  const rightTabIdsRef = useRef<Array<{ id: string }>>([])
  rightTabIdsRef.current = rightTabs
  const [sidebarMode, setSidebarMode] = useState<'normal' | 'settings'>('normal')
  const sidebarModeRef = useRef(sidebarMode)
  sidebarModeRef.current = sidebarMode
  const [rightPanelOpen, setRightPanelOpen] = useState(false)
  const rightPanelOpenRef = useRef(rightPanelOpen)
  rightPanelOpenRef.current = rightPanelOpen
  const embeddedRef = useRef(embedded)
  embeddedRef.current = embedded
  const [createScope, setCreateScope] = useState<'normal' | 'independent'>('normal')
  const [settingsCategories, setSettingsCategories] = useState<string[]>(() => [loadLastSettingsCategory()])
  const [mobileSyncOpen, setMobileSyncOpen] = useState(false)
  const [pendingFirstMessage, setPendingFirstMessage] = useState<{
    agentId: string
    text: string
    unit: ModelUnit | null
    modeCardId?: string
  } | null>(null)
  const agentOps = useAgentOperations()
  const localInstall = useLocalAppInstall()
  const [contentMode, setContentMode] = useState<ContentMode>('conversation')
  // Launcher (home) pane variant. 'workbench' is the fullscreen board takeover
  // (WorkbenchSurface overlay): it hides the topbar and the whole shell body by
  // CSS only — pane state (sidebar / right panel) is never collapsed, so exit
  // restores exactly what was there. Restored from the persisted layout on
  // startup, persisted on change by the debounced layout save below.
  const [launcherMode, setLauncherMode] = useState<LauncherMode>('code')
  // Last non-workbench launcher pane (code / app) — what "exit workbench"
  // restores.
  const lastPaneLauncherModeRef = useRef<Exclude<LauncherMode, 'workbench'>>('code')
  // Clicking the launcher-mode switch (构建 / 应用 / 工作台) from the settings
  // view exits settings and reveals the matching sidebar. The workbench pane is
  // a pure visual takeover: `ai-shell-layout--workbench` hides the shell body
  // (sidebar + main pane + right panel) in CSS while the board is up — the pane
  // state is never touched, so exiting restores the exact layout the user had
  // and the persisted layout is never polluted by takeover chrome.
  const handleLauncherModeChange = useCallback((mode: LauncherMode) => {
    if (mode !== 'workbench') lastPaneLauncherModeRef.current = mode
    setLauncherMode(mode)
    if (sidebarModeRef.current === 'settings') {
      setSidebarMode('normal')
      dispatch({ type: 'SET_SIDEBAR_VISIBLE', value: true })
    }
  }, [])
  // The board is an HTML layer raised over the embedded native browser child
  // windows: like every such overlay it must register with the
  // BrowserOverlayManager (ref-counted hide/restore of the native panes).
  // Without this the takeover keeps rightPanelOpen true, the per-tab
  // visibility effect below leaves the native windows shown, and they float
  // above the board (CSS cannot cover a native child window).
  useBrowserOverlay(!embedded && launcherMode === 'workbench')
  // Launcher section state: favorite ids (收藏), recently used ids (最近, most
  // recent first, capped at 6) and the all-section custom order (列表). All
  // three persist through the debounced layout save / beforeunload below.
  const [launcherFavorites, setLauncherFavorites] = useState<string[]>([])
  // App display variant of the launcher body: square grid (default) or list rows.
  const [launcherView, setLauncherView] = useState<'grid' | 'list'>('grid')
  const [launcherRecent, setLauncherRecent] = useState<string[]>([])
  const [launcherOrder, setLauncherOrder] = useState<string[]>([])
  const navHistory = useNavHistory()
  const historyNavigationRef = useRef(false)
  // navigateToAgent is defined later in the component; restoreEntry (defined
  // earlier) reaches it through this ref to avoid TDZ ordering issues.
  const navigateToAgentRef = useRef<(projectId: string, agentId: string, opts?: { switchToConversation?: boolean }) => Promise<void>>(async () => {})
  // Navigation-history state for components that are not otherwise represented in layout state.
  const [fileBrowserPath, setFileBrowserPath] = useState<string | null>(null)
  const [fileBrowserSelection, setFileBrowserSelection] = useState<string | null>(null)
  const [cursorPos, setCursorPos] = useState<number | null>(null)
  const [selectionFrom, setSelectionFrom] = useState<number | null>(null)
  const [selectionTo, setSelectionTo] = useState<number | null>(null)
  const [cursorRestoreKey, setCursorRestoreKey] = useState(0)
  const [topologyMode, setTopologyMode] = useState<TopoMode>('cards')
  /** Track cacheable modes the user has visited; each is kept mounted in a
   *  hidden pane so switching back preserves internal state (vis-network
   *  viewport, filters, scroll position, etc.). */
  const [visitedModes, setVisitedModes] = useState<ContentMode[]>([])
  const [brainMountAgentId, setBrainMountAgentId] = useState<string | null>(null)
  const [pinnedModes, setPinnedModes] = useState<string[]>([])
  const [omniboxOpen, setOmniboxOpen] = useState(false)
  const [omniboxPosition, setOmniboxPosition] = useState<'top' | 'bottom'>('top')
  const [findReplace, setFindReplace] = useState<{ open: boolean; mode: 'find' | 'replace' }>({ open: false, mode: 'find' })
  const [recentOmniboxCommands, setRecentOmniboxCommands] = useState<string[]>([])
  const [configBadgesVisible, setConfigBadgesVisible] = useState(false)
  const [turnTailMetrics, setTurnTailMetrics] = useState<TurnTailMetricsVisible>(defaultTurnTailMetricsVisible)
  const turnTailMetricsLoaded = useRef(false)
  const [composerExtras, setComposerExtras] = useState<ComposerExtrasVisible>(defaultComposerExtrasVisible)
  const composerExtrasLoaded = useRef(false)
  const [smoothStream, setSmoothStream] = useState(true)
  const [developerMode, setDeveloperMode] = useState(false)
  const developerModeLoaded = useRef(false)
  // Experimental option under developer settings; gates the per-provider
  // User-Agent input in provider add/edit forms.
  const [providerUserAgentVisible, setProviderUserAgentVisible] = useState(false)
  const providerUserAgentVisibleLoaded = useRef(false)
  // Experiment subscription (Ultimate / active Insider pass) gates every
  // developer-tooling entry (Create App, install-local, debug/native bundles).
  const [devModePromptOpen, setDevModePromptOpen] = useState(false)
  const [expansionModes, setExpansionModes] = useState<Record<string, UiExpandAction>>(DEFAULT_UI_EXPANSION_MODES)
  const [expandDurationMs, setExpandDurationMs] = useState(DEFAULT_UI_EXPAND_DURATION_MS)
  const importFileInputRef = useRef<HTMLInputElement>(null)

  const { state: installState, reset: resetInstall } = localInstall
  useEffect(() => {
    if (installState.success) {
      setContextSuccess(installState.success)
      resetInstall()
    }
    if (installState.error) {
      setContextError(installState.error)
      resetInstall()
    }
  }, [installState.success, installState.error, resetInstall])

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey
      const k = e.key.toLowerCase()
      if (mod && k === 'k') {
        e.preventDefault()
        setOmniboxOpen((open) => {
          if (!open) setOmniboxPosition('top')
          return !open
        })
      } else if (mod && e.shiftKey && k === 'f') {
        e.preventDefault()
        setFindReplace({ open: true, mode: 'find' })
      } else if (mod && e.shiftKey && k === 'r') {
        e.preventDefault()
        setFindReplace({ open: true, mode: 'replace' })
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [])

  // Initialize telemetry on mount
  useEffect(() => {
    initTelemetry()
  }, [])

  // Track navigate events on content mode changes
  useEffect(() => {
    setTelemetryView(contentMode)
    reportInteraction('navigate', '', { view: contentMode, detail: contentMode })
  }, [contentMode])

  // Register cacheable modes as visited so their pane stays mounted.
  useEffect(() => {
    if (CACHEABLE_MODES.has(contentMode)) {
      setVisitedModes(prev => prev.includes(contentMode) ? prev : [...prev, contentMode])
    }
  }, [contentMode])
  useEffect(() => {
    let cancelled = false
    void getComposerBadgesVisible().then(visible => {
      if (cancelled) return
      setConfigBadgesVisible(visible)
    })
    return () => { cancelled = true }
  }, [])
  useEffect(() => {
    void saveComposerBadgesVisible(configBadgesVisible)
  }, [configBadgesVisible])
  useEffect(() => {
    let cancelled = false
    void getTurnTailMetricsVisible().then(metrics => {
      if (cancelled) return
      turnTailMetricsLoaded.current = true
      setTurnTailMetrics(metrics)
      setTurnTailMetricsStore(metrics)
    })
    return () => { cancelled = true }
  }, [])
  useEffect(() => {
    if (!turnTailMetricsLoaded.current) return
    setTurnTailMetricsStore(turnTailMetrics)
    void saveTurnTailMetricsVisible(turnTailMetrics)
  }, [turnTailMetrics])
  useEffect(() => {
    let cancelled = false
    void getComposerExtrasVisible().then(extras => {
      if (cancelled) return
      composerExtrasLoaded.current = true
      setComposerExtras(extras)
    })
    return () => { cancelled = true }
  }, [])
  useEffect(() => {
    if (!composerExtrasLoaded.current) return
    void saveComposerExtrasVisible(composerExtras)
  }, [composerExtras])
  useEffect(() => {
    let cancelled = false
    void getSmoothStream().then(v => {
      if (!cancelled) setSmoothStream(v)
    })
    return () => { cancelled = true }
  }, [])
  useEffect(() => {
    void saveSmoothStream(smoothStream)
  }, [smoothStream])
  useEffect(() => {
    let cancelled = false
    void getDeveloperMode().then(v => {
      if (cancelled) return
      developerModeLoaded.current = true
      setDeveloperMode(v)
    })
    return () => { cancelled = true }
  }, [])
  useEffect(() => {
    if (!developerModeLoaded.current) return
    void saveDeveloperMode(developerMode)
  }, [developerMode])
  useEffect(() => {
    let cancelled = false
    void getProviderUserAgentVisible().then(v => {
      if (cancelled) return
      providerUserAgentVisibleLoaded.current = true
      setProviderUserAgentVisible(v)
    })
    return () => { cancelled = true }
  }, [])
  useEffect(() => {
    if (!providerUserAgentVisibleLoaded.current) return
    void saveProviderUserAgentVisible(providerUserAgentVisible)
  }, [providerUserAgentVisible])
  useEffect(() => {
    let cancelled = false
    void Promise.all([getUiExpansionModes(), getUiExpandDurationMs()]).then(([modes, ms]) => {
      if (cancelled) return
      setExpansionModes(modes)
      setExpandDurationMs(ms)
    })
    return () => { cancelled = true }
  }, [])
  const handleExpansionModeChange = useCallback((kind: string, action: UiExpandAction) => {
    setExpansionModes(prev => ({ ...prev, [kind]: action }))
    void saveUiExpansionMode(kind, action)
  }, [])
  const handleExpandDurationChange = useCallback((ms: number) => {
    const clamped = Math.max(0, Math.min(10000, Math.round(ms)))
    setExpandDurationMs(clamped)
    void saveUiExpandDurationMs(clamped)
  }, [])
  // ComposerFrame is rendered inside the right panel when expanded;
  // in normal mode it lives at the bottom of ai-shell-content.
  // Knowledge base (notes mode) is a global two-column view. Navigating into a
  // project/card lane is expressed as a monotonic open request consumed by
  // KnowledgeModeView (cleared once consumed); its durable lane / view /
  // expansion state lives in the workspace actor (no `notesProjectId ??
  // activeProject` binding, no localStorage).
  const [kbOpenRequest, setKbOpenRequest] = useState<KnowledgeModeOpenRequest | null>(null)
  const kbOpenRequestKeyRef = useRef(0)
  const [kbActiveLane, setKbActiveLane] = useState<{ projectId: string; projectName: string; cardId?: string } | null>(null)
  const [sidebarWidth, setSidebarWidth] = useState(280)
  const [rightPanelWidth, setRightPanelWidth] = useState(400)

  // ── IDEA-style terminal panel (default collapsed; persisted via
  //    AIShellLayoutState so no localStorage). Mobile never auto-opens
  //    (mutual exclusion with the right panel — enforced below).
  const [terminalPanelOpen, setTerminalPanelOpen] = useState(false)
  const [terminalPanelHeight, setTerminalPanelHeight] = useState(240)
  const [terminalTabs, setTerminalTabs] = useState<Array<{ id: string; type: string; title?: string }>>([])
  const [terminalActiveTabId, setTerminalActiveTabId] = useState<string | null>(null)
  const sidebarRef = useRef<HTMLDivElement>(null)
  const rightPanelRef = useRef<HTMLDivElement>(null)
  const [composerFrameH, setComposerFrameH] = useState(0)
  const [composerCardTop, setComposerCardTop] = useState(0)
  // Persisted expanded drawer stream height (desktop only; null = CSS default 50vh).
  const [composerDrawerHeight, setComposerDrawerHeight] = useState<number | null>(null)

  const handleComposerDrawerResize = useCallback((size: number) => {
    setComposerDrawerHeight(Math.round(Math.max(160, Math.min(size, window.innerHeight * 0.75))))
  }, [])

  const handleRightPanelResize = useCallback((size: number) => {
    setRightPanelWidth(Math.max(240, size))
  }, [])

  const handleSidebarResize = useCallback((size: number) => {
    setSidebarWidth(Math.max(180, Math.min(size, 480)))
  }, [])

  const handleTerminalPanelResize = useCallback((size: number) => {
    setTerminalPanelHeight(Math.max(120, Math.min(size, Math.floor(window.innerHeight * 0.6))))
  }, [])

  const handleAddTerminalTab = useCallback((type: string) => {
    const id = `term-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
    setTerminalTabs(prev => [...prev, { id, type, title: undefined }])
    setTerminalActiveTabId(id)
    setTerminalPanelOpen(true)
  }, [])

  const handleCloseTerminalTab = useCallback((id: string) => {
    setTerminalTabs(prev => {
      const idx = prev.findIndex(t => t.id === id)
      const next = prev.filter(t => t.id !== id)
      if (next.length === 0) setTerminalPanelOpen(false)
      setTerminalActiveTabId(active => {
        if (active !== id) return active
        if (next.length === 0) return null
        // Prefer the tab that took the closed tab's slot, then its left
        // neighbour, then the last remaining tab.
        const activeIdx = idx >= 0 ? Math.min(idx, next.length - 1) : next.length - 1
        return next[activeIdx]!.id
      })
      return next
    })
  }, [])

  // Terminal tab bodies (e.g. the shell console) can annotate their own tab
  // title — the shell console appends an "exited" suffix when the session
  // dies and clears it again when a fresh session starts on reopen.
  const handleSetTerminalTabTitle = useCallback((id: string, title: string | undefined) => {
    setTerminalTabs(prev => prev.map(t => (t.id === id ? { ...t, title } : t)))
  }, [])

  // Right-panel open state is managed solely by the tab-count sync
  // below (and by explicit user toggles); files mode no longer forces it.

  // Sync panel open state with tab count, but skip the first run so the
  // persisted layout state (rightPanelOpen restored from storage) survives
  // restart even when rightTabs is still empty.
  const skipPanelSyncRef = useRef(true)
  useEffect(() => {
    if (skipPanelSyncRef.current) {
      skipPanelSyncRef.current = false
      if (inWails) return
    }
    setRightPanelOpen(visibleRightTabs.length > 0)
  }, [visibleRightTabs.length, inWails])

  // Collapsing the right panel also clears its expanded state so it never
  // reopens maximized.
  useEffect(() => {
    if (!rightPanelOpen) setRightPanelExpanded(false)
  }, [rightPanelOpen])

  // Ensure the active right-panel tab remains valid when browser tabs are
  // hidden outside Wails (mobile / web).
  useEffect(() => {
    if (inWails) return
    const activeStillVisible = visibleRightTabs.some((t) => t.id === activeBrowserTabId)
    if (activeStillVisible) return
    setActiveBrowserTabId(visibleRightTabs.length > 0 ? visibleRightTabs[0]!.id : null)
  }, [inWails, visibleRightTabs, activeBrowserTabId])

  useEffect(() => {
    const handler = (e: Event) => {
      const mode = (e as CustomEvent<ContentMode>).detail
      if (mode === 'conversation' || mode === 'topology' || mode === 'workflow' || mode === 'scheduled' || mode === 'multiconsole' || mode === 'notes' || mode === 'problems' || mode === 'approvals' || mode === 'ssh' || mode === 'db' || mode === 'files' || mode === 'aistats' || mode === 'git') {
        setContentMode(mode)
        pendingContentMode = null
        if (mode === 'topology' || mode === 'workflow') {
          monoStore.load().catch(() => {})
        }
      }
    }
    window.addEventListener('sporemind:set-shell-content-mode', handler)
    return () => window.removeEventListener('sporemind:set-shell-content-mode', handler)
  }, [])

  useEffect(() => {
    if (embedded && (contentMode === 'multiconsole' || contentMode === 'topology' || contentMode === 'workflow' || contentMode === 'ssh')) {
      setContentMode('conversation')
    }
  }, [embedded, contentMode])

  // Registered apps from the shared app registry (synced by
  // startAppRegistrySync in App.tsx). The app panel, revoked-tab checks, and
  // plugin-agent sidebar group labels all derive from this snapshot.
  const registeredApps = useSyncExternalStore(
    (listener) => appRegistry.subscribe(listener),
    () => appRegistry.getAll(),
    () => EMPTY_APP_ENTRIES,
  )
  // True once the registry's initial `list` snapshot has landed. The plugin
  // boot restore must not run against a half-synced registry — apps that
  // arrive with the snapshot are boot apps (restore-only); apps that become
  // running later are mid-session starts (auto-open).
  const registryLoaded = useSyncExternalStore(
    (listener) => appRegistry.subscribe(listener),
    () => appRegistry.isLoaded(),
    () => false,
  )

  const agentInfoSnapshot = useAgentInfoList()
  const allAgents = agentInfoSnapshot.items
  const allAgentsRef = useRef(allAgents)
  allAgentsRef.current = allAgents

  const sidebarAgents = useMemo(() => {
    return applyAgentOrder(allAgents, projectUIState.agentOrder, projectUIState.sidebarOrder)
  }, [allAgents, contentMode, projectUIState.agentOrder])

  // Project-level subscription: keep monoStore bound to the active project and
  // listening for card/file-change events regardless of the current view mode.
  // Previously this only ran inside MonoCard (notes mode), so topology and
  // other modes missed live card updates from agents or the file system.
  useEffect(() => {
    const projectId = activeProject?.ProjectID
    if (!projectId) return
    const controller = new AbortController()
    monoStore.setProjectId(projectId)
    monoStore.load().catch(() => {})
    monoStore.watchCardChanges(projectId, controller.signal)
    monoStore.watchFileChanges(projectId, controller.signal)
    monoStore.watchGraphChanges(projectId, controller.signal)
    monoStore.watchFoldChanges(controller.signal)
    return () => controller.abort()
  }, [activeProject?.ProjectID])

  const activeProjectAgents = useMemo(() => {
    if (!activeProject) return []
    return applyAgentOrder(allAgents.filter(a => a.ProjectId === activeProject.ProjectID), projectUIState.agentOrder, projectUIState.sidebarOrder)
  }, [allAgents, activeProject, projectUIState.agentOrder])

  // The first global coordinator agent is the app's default "home" assistant:
  // a project-less agent the user lands on at startup and after onboarding.
  const globalCoordinator = useMemo(
    () => allAgents.find(a => a.AgentKind === 'coordinator' && !a.ProjectId) ?? null,
    [allAgents],
  )

  // Project-less system agents (Coordinator, Oracle) are workspace-global.
  // Plugin agents (BoundAppId set) are workspace-global too but group under a
  // virtual per-app directory in the sidebar; they split out here so the
  // sidebar renders them separately from the flat global section.
  const globalAgents = useMemo(
    () => sidebarAgents.filter(a => !a.ProjectId && !a.BoundAppId),
    [sidebarAgents],
  )

  // Virtual per-app directory projection: plugin agents grouped by their
  // owning app id, labeled by the app registry's display name. A group with
  // no agents never appears (the "directory" exists only as this grouping),
  // so unregistering a plugin or deleting its last bound agent removes the
  // directory automatically.
  const pluginAgentGroups = useMemo(() => {
    const byApp = new Map<string, AgentInfo[]>()
    for (const agent of sidebarAgents) {
      if (!agent.BoundAppId) continue
      const list = byApp.get(agent.BoundAppId)
      if (list) list.push(agent)
      else byApp.set(agent.BoundAppId, [agent])
    }
    return [...byApp.entries()]
      .map(([appId, agents]) => ({
        appId,
        label: appRegistry.get(appId)?.name || appId,
        agents: [...agents].sort((a, b) => a.DisplayName.localeCompare(b.DisplayName)),
      }))
      .sort((a, b) => a.appId.localeCompare(b.appId))
  }, [sidebarAgents, registeredApps])

  // The card MiniComposer holds its own conversation with the project's
  // coder agent (user-creatable "project assistant" role). It is
  // independent of the active timeline agent.
  const composerTargetActorId = useMemo(() => {
    const a = activeProjectAgents.find(a => a.AgentKind === 'coder')
    return a?.ActorId ?? null
  }, [activeProjectAgents])

  const projectAgentsMap = useMemo(() => {
    const map = new Map<string, { AgentId: string; DisplayName: string; AgentKind: string; Title?: string }[]>()
    for (const agent of sidebarAgents) {
      const existing = map.get(agent.ProjectId)
      const entry = { AgentId: agent.Id, DisplayName: agent.DisplayName, AgentKind: agent.AgentKind, Title: agent.Title }
      if (existing) {
        existing.push(entry)
      } else {
        map.set(agent.ProjectId, [entry])
      }
    }
    return map
  }, [sidebarAgents])
  const projectStatusMap = useMemo(() => aggregateProjectStatus(allAgents), [allAgents])
  const projectsRef = useRef(projects)
  projectsRef.current = projects
  const activeProjectRef = useRef(activeProject)
  activeProjectRef.current = activeProject
  const activeSessionIdRef = useRef(state.activeSessionId)
  activeSessionIdRef.current = state.activeSessionId
  const activeAgentIdRef = useRef<string | null>(null)
  const pendingNavRef = useRef<{ projectId: string; agentActorId: string } | null>(null)
  const navigateGenRef = useRef(0)
  const { theme: aiTheme, handleThemeChange } = useAIShellTheme({
    embedded,
    theme: externalTheme,
    onThemeChange: externalOnThemeChange,
  })

  useEffect(() => {
    void setBrowserColorScheme(aiTheme.mode)
  }, [aiTheme.mode])

  const viewMode: ViewMode = layout.viewMode
  const isMobile = viewMode === 'mobile'
  // Restore effect runs once with `[]` deps, so its async `.then` closure would
  // capture a stale `isMobile` (viewMode starts as 'desktop' until the
  // ResizeObserver fires). Use this ref to read the current value inside that
  // closure, preventing mobile overlays (sidebar + right panel) from being
  // restored open on a phone before layout measurement completes.
  const isMobileRef = useRef(isMobile)
  isMobileRef.current = isMobile

  // Mobile only: the terminal panel and the right panel are mutually
  // exclusive — opening one collapses the other. Desktop can co-exist.
  useEffect(() => {
    if (!isMobile) return
    if (terminalPanelOpen) setRightPanelOpen(false)
  }, [isMobile, terminalPanelOpen])

  useEffect(() => {
    if (!isMobile) return
    if (rightPanelOpen) setTerminalPanelOpen(false)
  }, [isMobile, rightPanelOpen])

  const sidebar = useSidebarInteraction({
    sidebarVisible: state.sidebarVisible,
    dispatchToggleSidebar: useCallback(() => dispatch({ type: 'TOGGLE_SIDEBAR' }), []),
  })

  const layoutLoadedRef = useRef(false)
  const restorePromiseRef = useRef<Promise<void> | null>(null)
  const [sessionRestored, setSessionRestored] = useState(false)

  useEffect(() => {
    if (restorePromiseRef.current) return
    restorePromiseRef.current = Promise.all([getAIShellLayoutState(), getAIShellSessionState()]).then(([layout, session]) => {
      layoutLoadedRef.current = true
      setSessionRestored(true)
      dispatch({ type: 'RESTORE_SESSION', selectedAgentId: session.selectedAgentId, previousAgentId: session.previousAgentId })
      if (!layout) return
      rightTabOrderRef.current = layout.rightTabOrder ?? []
      persistedPluginTabsRef.current = Array.isArray(layout.rightPluginTabs)
        ? layout.rightPluginTabs.filter((t) => t
          && typeof t.id === 'string' && t.id.length > 0
          && typeof t.pluginID === 'string' && t.pluginID.length > 0
          && typeof t.viewID === 'string' && t.viewID.length > 0)
        : null
      if (layout.sidebarVisible !== undefined && !isMobileRef.current) {
        dispatch({ type: 'SET_SIDEBAR_VISIBLE', value: layout.sidebarVisible })
      }
      if (layout.sidebarWidth !== undefined) {
        setSidebarWidth(Math.max(180, layout.sidebarWidth))
      }
      if (layout.rightPanelWidth !== undefined) {
        setRightPanelWidth(Math.max(240, layout.rightPanelWidth))
      }
      if (layout.composerDrawerHeight !== undefined) {
        setComposerDrawerHeight(Math.max(160, layout.composerDrawerHeight))
      }
      if (layout.rightPanelOpen !== undefined && !isMobileRef.current) {
        setRightPanelOpen(layout.rightPanelOpen)
      }
      if (layout.terminalPanelOpen !== undefined && !isMobileRef.current) {
        setTerminalPanelOpen(layout.terminalPanelOpen)
      }
      if (layout.terminalPanelHeight !== undefined) {
        setTerminalPanelHeight(Math.max(120, Math.min(layout.terminalPanelHeight, Math.floor(window.innerHeight * 0.6))))
      }
      if (Array.isArray(layout.terminalTabs)) {
        setTerminalTabs(layout.terminalTabs.filter(t => t && typeof t.id === 'string' && typeof t.type === 'string'))
      }
      if (layout.terminalActiveTabId && layout.terminalTabs?.some(t => t.id === layout.terminalActiveTabId)) {
        setTerminalActiveTabId(layout.terminalActiveTabId)
      }
      if (layout.browserCreateScope) {
        setCreateScope(layout.browserCreateScope)
      }
      if (layout.browserTabs?.length && inWails) {
        const globalTabs = layout.browserTabs
          .filter((t) => t.kind === 'global' && t.sessionId.startsWith('global-') && t.id !== 'browser-newtab')
          .map((t) => ({
            id: t.id,
            type: 'browser' as const,
            label: t.label,
            payload: { sessionId: t.sessionId, kind: t.kind, url: t.url },
          }))
        if (globalTabs.length > 0) {
          setRightTabs(globalTabs)
          setRightPanelOpen(true)
          if (layout.activeBrowserTabId && globalTabs.some((t) => t.id === layout.activeBrowserTabId)) {
            setActiveBrowserTabId(layout.activeBrowserTabId)
          } else {
            setActiveBrowserTabId(globalTabs[0]!.id)
          }
        }
      }
      // SSH tab restore: re-establish persisted SSH right-panel tabs by hostId.
      // Alignment-first: adopt the account's LIVE sessions (sessionList) so a
      // reloading client reattaches to the same session other clients already
      // view, instead of opening a second one that would kill and replace
      // their terminal mid-use. Only persisted hosts with no live session get
      // a fresh shellOpen. The volatile sessionId is NOT persisted; hosts that
      // were deleted, or whose reconnect fails, are silently skipped.
      if (layout.rightSshTabs?.length) {
        const sshTabsToRestore = layout.rightSshTabs
        void (async () => {
          let firstRestoredTabId: string | null = null
          const ensureTab = (sessionId: string, hostId: string, hostName: string) => {
            const newTabId = sshTabId(hostId)
            setRightTabs(prev => {
              if (prev.some(t => t.id === newTabId)) return prev
              return [...prev, { id: newTabId, type: 'ssh-session' as const, label: hostName, payload: { sessionId, hostId, hostName } }]
            })
            if (firstRestoredTabId === null) firstRestoredTabId = newTabId
          }
          try {
            const hostResp = await sshmanagerClient.hostList(client, {})
            const validHostIds = new Set((hostResp.Items ?? []).map(h => h.Id))
            const liveByHost = new Map<string, { sessionId: string; hostName: string }>()
            try {
              const sessResp = await sshmanagerClient.sessionList(client, {})
              for (const s of sessResp.Items ?? []) {
                if (s.Connected && s.SessionId && s.HostId && validHostIds.has(s.HostId) && !liveByHost.has(s.HostId)) {
                  liveByHost.set(s.HostId, { sessionId: s.SessionId, hostName: s.HostName })
                }
              }
            } catch {
              // sessionList failed — fall back to opening fresh sessions below.
            }
            // Adopt every live account session (opened by any client or by the
            // agent): all clients converge on the same session per host.
            for (const [hostId, live] of liveByHost) ensureTab(live.sessionId, hostId, live.hostName)
            for (const tab of sshTabsToRestore) {
              if (!tab.hostId || !validHostIds.has(tab.hostId)) continue // host deleted
              if (liveByHost.has(tab.hostId)) continue // already adopted above
              try {
                const resp = await sshmanagerClient.shellOpen(client, { HostId: tab.hostId, InitialCols: 80, InitialRows: 24 })
                if (!resp.SessionId || !resp.Connected) continue // reconnect failed
                // Converge on the same hostId key used by the ssh_manager_event
                // path (handleOpenSshSession) so restore and live events can
                // never open two tabs for one host.
                const newTabId = sshTabId(tab.hostId)
                setRightTabs(prev => {
                  // duplicate-recovery guard: never create the same tab twice.
                  // The tab may already exist because the event path opened it
                  // first; keep that tab and best-effort close the redundant
                  // session opened here so it does not leak.
                  if (prev.some(t => t.id === newTabId)) {
                    void sshmanagerClient.shellClose(client, { SessionId: resp.SessionId }).catch(() => {})
                    return prev
                  }
                  return [...prev, { id: newTabId, type: 'ssh-session' as const, label: tab.hostName, payload: { sessionId: resp.SessionId, hostId: tab.hostId, hostName: tab.hostName } }]
                })
                if (firstRestoredTabId === null) firstRestoredTabId = newTabId
              } catch {
                // reconnect failed — skip this tab
              }
            }
          } catch {
            // hostList failed — cannot determine which hosts still exist
          }
          if (firstRestoredTabId) {
            setRightPanelOpen(true)
            // Only grab focus when no other restored tab (e.g. browser) already has it.
            setActiveBrowserTabId(active => active ?? firstRestoredTabId)
          }
        })()
      }
      // DB tab restore: re-establish persisted DB right-panel tabs by profileId.
      // The dbclient actor re-dials lazily on first tree/read; no connection
      // handle has to be opened here. Profiles that have since been deleted
      // are silently skipped.
      if (layout.rightDbTabs?.length) {
        const dbTabsToRestore = layout.rightDbTabs
        let firstDbTabId: string | null = null
        const seen = new Set<string>()
        for (const tab of dbTabsToRestore) {
          if (!tab.profileId || !tab.backend) continue
          if (seen.has(tab.id)) continue
          seen.add(tab.id)
          setRightTabs(prev => {
            if (prev.some(t => t.id === tab.id)) return prev
            return [...prev, { id: tab.id, type: 'db-session' as const, label: tab.profileName, payload: { profileId: tab.profileId, profileName: tab.profileName, backend: tab.backend } }]
          })
          if (firstDbTabId === null) firstDbTabId = tab.id
        }
        if (firstDbTabId) {
          setRightPanelOpen(true)
          setActiveBrowserTabId(active => active ?? firstDbTabId)
        }
      }
      if (pendingContentMode === 'conversation' || pendingContentMode === 'topology' || pendingContentMode === 'workflow' || pendingContentMode === 'scheduled' || pendingContentMode === 'multiconsole' || pendingContentMode === 'notes' || pendingContentMode === 'problems' || pendingContentMode === 'approvals' || pendingContentMode === 'ssh' || pendingContentMode === 'db' || pendingContentMode === 'files' || pendingContentMode === 'aistats' || pendingContentMode === 'git') {
        setContentMode(pendingContentMode)
        pendingContentMode = null
      } else if (layout.contentMode === 'conversation' || layout.contentMode === 'topology' || layout.contentMode === 'workflow' || layout.contentMode === 'scheduled' || layout.contentMode === 'multiconsole' || layout.contentMode === 'notes' || layout.contentMode === 'problems' || layout.contentMode === 'approvals' || layout.contentMode === 'ssh' || layout.contentMode === 'db' || layout.contentMode === 'files' || layout.contentMode === 'aistats' || layout.contentMode === 'git') {
        setContentMode(layout.contentMode)
      }
      if (layout.recentOmniboxCommands) {
        setRecentOmniboxCommands(layout.recentOmniboxCommands)
      }
      if (layout.launcherMode === 'code' || layout.launcherMode === 'app' || layout.launcherMode === 'workbench') {
        const isDevBuild = buildType === 'dev'
        if (layout.launcherMode === 'workbench' && !isDevBuild) {
          // Workbench was persisted by a dev build; release builds don't
          // offer it — fall back to the last code/app pane instead of
          // entering an unreachable takeover.
          setLauncherMode('code')
          lastPaneLauncherModeRef.current = 'code'
        } else {
          setLauncherMode(layout.launcherMode)
          if (layout.launcherMode !== 'workbench') lastPaneLauncherModeRef.current = layout.launcherMode
        }
      }
      if (isStringArray(layout.launcherFavorites)) setLauncherFavorites(layout.launcherFavorites)
      if (isStringArray(layout.launcherRecent)) setLauncherRecent(layout.launcherRecent)
      if (isStringArray(layout.launcherOrder)) setLauncherOrder(layout.launcherOrder)
      if (layout.launcherView === 'grid' || layout.launcherView === 'list') setLauncherView(layout.launcherView)
      void getPinnedModes().then(setPinnedModes)
    }).catch(() => {
      layoutLoadedRef.current = true
      setSessionRestored(true)
    })
  }, [])

  useEffect(() => {
    if (!layoutLoadedRef.current) return
    const timer = setTimeout(() => {
      const independentUrls = new Set(rightTabs
        .filter((t) => t.type === 'browser' && (t.payload as { kind?: BrowserKind }).kind === 'independent')
        .map((t) => (t.payload as { url?: string }).url)
        .filter((url): url is string => !!url))
      const globalTabs = rightTabs
        .filter((t) => t.type === 'browser' && (t.payload as { kind?: BrowserKind }).kind === 'global' && t.id !== 'browser-newtab')
        .filter((t) => !independentUrls.has((t.payload as { url?: string }).url || ''))
        .map((t) => ({
          id: t.id,
          sessionId: (t.payload as { sessionId: string }).sessionId,
          kind: (t.payload as { kind: BrowserKind }).kind,
          url: (t.payload as { url: string }).url,
          label: t.label,
        }))
      const sshTabs = extractSshTabsForPersist(rightTabs)
      const dbTabs = extractDbTabsForPersist(rightTabs)
      const pluginTabs = extractPluginTabsForPersist(rightTabs)
      saveAIShellLayoutState({
        sidebarVisible: isMobile ? false : state.sidebarVisible,
        sidebarWidth,
        rightPanelWidth,
        rightPanelOpen,
        contentMode,
        launcherMode,
        launcherView,
        launcherFavorites,
        launcherRecent,
        launcherOrder,
        ...(browserRestoreDoneRef.current ? { rightTabOrder: rightTabs.map((t) => t.id) } : {}),
        // Explicit array (even empty) so closing every plugin tab is durable;
        // gated on both restores so the pre-restore debounce cannot clobber
        // the persisted set with [].
        ...(browserRestoreDoneRef.current && pluginBootRestoreDoneRef.current ? { rightPluginTabs: pluginTabs } : {}),
        browserTabs: globalTabs.length > 0 ? globalTabs : undefined,
        activeBrowserTabId: rightTabs.some(
          (t) => t.id === activeBrowserTabId && t.type === 'browser' && (t.payload as { kind?: BrowserKind }).kind === 'global'
        )
          ? (activeBrowserTabId ?? undefined)
          : undefined,
        rightSshTabs: sshTabs.length > 0 ? sshTabs : undefined,
        rightDbTabs: dbTabs.length > 0 ? dbTabs : undefined,
        browserCreateScope: createScope,
        recentOmniboxCommands,
        pinnedModes,
        ...(composerDrawerHeight != null ? { composerDrawerHeight } : {}),
        terminalPanelOpen: isMobile ? false : terminalPanelOpen,
        terminalPanelHeight,
        terminalTabs: terminalTabs.length > 0 ? terminalTabs : undefined,
        terminalActiveTabId: terminalActiveTabId ?? undefined,
      }).catch(() => {})
    }, 500)
    return () => clearTimeout(timer)
  }, [state.sidebarVisible, sidebarWidth, rightPanelWidth, rightPanelOpen, contentMode, launcherMode, launcherView, launcherFavorites, launcherRecent, launcherOrder, rightTabs, activeBrowserTabId, createScope, recentOmniboxCommands, pinnedModes, composerDrawerHeight, terminalPanelOpen, terminalPanelHeight, terminalTabs, terminalActiveTabId])

  useEffect(() => {
    const saveBeforeUnload = () => {
      const independentUrls = new Set(rightTabs
        .filter((t) => t.type === 'browser' && (t.payload as { kind?: BrowserKind }).kind === 'independent')
        .map((t) => (t.payload as { url?: string }).url)
        .filter((url): url is string => !!url))
      const globalTabs = rightTabs
        .filter((t) => t.type === 'browser' && (t.payload as { kind?: BrowserKind }).kind === 'global' && t.id !== 'browser-newtab')
        .filter((t) => !independentUrls.has((t.payload as { url?: string }).url || ''))
        .map((t) => ({
          id: t.id,
          sessionId: (t.payload as { sessionId: string }).sessionId,
          kind: (t.payload as { kind: BrowserKind }).kind,
          url: (t.payload as { url: string }).url,
          label: t.label,
        }))
      const sshTabs = extractSshTabsForPersist(rightTabs)
      const dbTabs = extractDbTabsForPersist(rightTabs)
      const pluginTabs = extractPluginTabsForPersist(rightTabs)
      void saveAIShellLayoutState({
        sidebarVisible: isMobile ? false : state.sidebarVisible,
        sidebarWidth,
        rightPanelWidth,
        rightPanelOpen,
        contentMode,
        launcherMode,
        launcherView,
        launcherFavorites,
        launcherRecent,
        launcherOrder,
        ...(browserRestoreDoneRef.current ? { rightTabOrder: rightTabs.map((t) => t.id) } : {}),
        // Explicit array (even empty) so closing every plugin tab is durable;
        // gated on both restores so the pre-restore debounce cannot clobber
        // the persisted set with [].
        ...(browserRestoreDoneRef.current && pluginBootRestoreDoneRef.current ? { rightPluginTabs: pluginTabs } : {}),
        browserTabs: globalTabs.length > 0 ? globalTabs : undefined,
        activeBrowserTabId: rightTabs.some(
          (t) => t.id === activeBrowserTabId && t.type === 'browser' && (t.payload as { kind?: BrowserKind }).kind === 'global'
        )
          ? (activeBrowserTabId ?? undefined)
          : undefined,
        rightSshTabs: sshTabs.length > 0 ? sshTabs : undefined,
        rightDbTabs: dbTabs.length > 0 ? dbTabs : undefined,
        browserCreateScope: createScope,
        recentOmniboxCommands,
        ...(composerDrawerHeight != null ? { composerDrawerHeight } : {}),
        terminalPanelOpen: isMobile ? false : terminalPanelOpen,
        terminalPanelHeight,
        terminalTabs: terminalTabs.length > 0 ? terminalTabs : undefined,
        terminalActiveTabId: terminalActiveTabId ?? undefined,
      })
    }
    window.addEventListener('beforeunload', saveBeforeUnload)
    return () => window.removeEventListener('beforeunload', saveBeforeUnload)
  }, [state.sidebarVisible, sidebarWidth, rightPanelWidth, rightPanelOpen, contentMode, launcherMode, launcherView, launcherFavorites, launcherRecent, launcherOrder, rightTabs, activeBrowserTabId, createScope, recentOmniboxCommands, composerDrawerHeight, terminalPanelOpen, terminalPanelHeight, terminalTabs, terminalActiveTabId])

  useEffect(() => {
    if (!sessionRestored) return
    saveAIShellSessionState({
      selectedAgentId: state.activeSessionId,
      previousAgentId: state.previousSessionId,
    }).catch(() => {})
  }, [state.activeSessionId, state.previousSessionId, sessionRestored])

  useEffect(() => {
    if (!sessionRestored) return
    let cancelled = false
    const restoreBrowserTabs = async () => {
      if (!inWails) {
        // No independent browser tabs to restore; saves may persist order now.
        browserRestoreDoneRef.current = true
        return
      }
      try {
        const instances = await listBrowserWindows()
        if (cancelled) return
        const browserTabs = instances
          .filter((inst) => inst.Config.Mode === 'tab' && inst.Status.Open)
          .map((inst) => ({
            id: `browser-${inst.Config.Id}`,
            type: 'browser' as const,
            label: inst.Config.Name,
            payload: {
              sessionId: inst.Config.Id,
              kind: 'independent' as BrowserKind,
              url: inst.Status.Url || inst.Config.Url,
              sessionEpoch: 0,
              appliedProxy: proxySignature(inst.Config),
            },
          }))
        setRightTabs((prev) => {
          const independentUrls = new Set(browserTabs
            .map((t) => (t.payload as { url: string }).url)
            .filter(Boolean))
          const cleaned = prev.filter((t) => {
            if (t.type !== 'browser') return true
            if (t.id === 'browser-newtab') return false
            const p = t.payload as { kind?: BrowserKind; url?: string }
            if (p.kind === 'global' && p.url && independentUrls.has(p.url)) return false
            return true
          })
          const existingIds = new Set(cleaned.map((t) => t.id))
          const tabsToAdd = [...browserTabs]
          if (instances.length > 0 && browserTabs.length === 0 && !cleaned.some((t) => t.type === 'browser')) {
            tabsToAdd.unshift({
              id: 'browser-newtab',
              type: 'browser' as const,
              label: t('rightPanel.newTab'),
              payload: { sessionId: `global-${Date.now()}`, kind: 'global' as BrowserKind, url: '', sessionEpoch: 0, appliedProxy: '' },
            })
          }
          const newTabs = tabsToAdd.filter((t) => !existingIds.has(t.id))
          const merged = newTabs.length > 0 ? [...cleaned, ...newTabs] : cleaned
          return reorderTabs(merged, rightTabOrderRef.current)
        })
        // rightTabs now reflects the restored tab set in the saved order; from
        // here the saves may persist rightTabOrder (until then it is omitted so
        // the pre-restore debounce race cannot overwrite the saved order).
        browserRestoreDoneRef.current = true
        if (browserTabs.length > 0) {
          setRightPanelOpen(true)
          setActiveBrowserTabId((prev) => prev || browserTabs[0]!.id)
        }
      } catch {
        // ignore
      }
    }
    void restoreBrowserTabs()
    return () => { cancelled = true }
  }, [sessionRestored])

  // Keep independent browser tab labels in sync when their saved config
  // (e.g. name) is updated. For tab-mode independent instances, a proxy
  // change also bumps the tab's sessionEpoch so the BrowserView remounts
  // and desktop recreates the WebView2 session with the new proxy.
  useEffect(() => {
    return onBrowserManagerEvent((event: BrowserManagerEvent) => {
      if (event.Kind !== 'updated') return
      const inst = event.Instance
      if (!inst?.Config?.Id) return
      const tabId = `browser-${inst.Config.Id}`
      setRightTabs(prev => {
        const idx = prev.findIndex(t => t.id === tabId)
        if (idx < 0) return prev
        const tab = prev[idx]!
        if (tab.type !== 'browser') return prev
        const nextLabel = inst.Config.Name || tab.label
        const payload = tab.payload as Record<string, unknown>
        const isIndependent = payload.kind === 'independent'
        const proxyChanged = isIndependent
          && inst.Config.Mode === 'tab'
          && proxySignature(inst.Config) !== payload.appliedProxy
        if (tab.label === nextLabel && !proxyChanged) return prev
        const nextPayload = { ...payload }
        if (proxyChanged) {
          nextPayload.appliedProxy = proxySignature(inst.Config)
          nextPayload.sessionEpoch = ((payload.sessionEpoch as number | undefined) ?? 0) + 1
        }
        const next = [...prev]
        next[idx] = { ...tab, label: nextLabel, payload: nextPayload }
        return next
      })
    })
  }, [])

  // Open or navigate the shared global browser tab when an agent triggers
  // browsermanager.open_global.
  useEffect(() => {
    return onBrowserManagerEvent((event: BrowserManagerEvent) => {
      if (event.Kind !== 'open_global') return
      const inst = event.Instance
      const url = inst?.Config?.Url
      if (!url) return
      const sessionId = 'global-agent'
      const tabId = `browser-${sessionId}`
      setRightPanelOpen(true)
      let existed = false
      setRightTabs(prev => {
        const idx = prev.findIndex(tab => tab.id === tabId)
        if (idx >= 0) {
          existed = true
          const next = [...prev]
          next[idx] = { ...prev[idx]!, payload: { ...(prev[idx]!.payload as Record<string, unknown>), url } }
          return next
        }
        existed = false
        return [...prev, {
          id: tabId,
          type: 'browser' as const,
          label: t('rightPanel.newTab'),
          payload: { sessionId, kind: 'global' as BrowserKind, url },
        }]
      })
      setActiveBrowserTabId(tabId)
      // If the tab already existed, the BrowserView may not remount, so we
      // explicitly navigate the native window after React has applied the state.
      if (existed) {
        requestAnimationFrame(() => {
          void navigateBrowserSession(sessionId, url)
        })
      }
    })
  }, [t])

  useEffect(() => {
    let cancelled = false

    const loadContext = async () => {
      // Fast path: if the prefetch cache has data (started from App.tsx),
      // consume it synchronously — no network round-trip, no duplicate
      // requests. This also means contextLoading flips to false instantly.
      const prefetched = consumePrefetchedShellContext()
      if (prefetched) {
        setAccount(prefetched.account)
        setSession(prefetched.session)
        setProjects(prefetched.projects)
        setSystemTreeNodes(prefetched.systemTreeNodes)
        setActiveProject(prefetched.activeProject)
        setContextLoading(false)
        return
      }

      // Slow path: prefetch wasn't available (e.g. non-Wails web mode where
      // the splash gate doesn't trigger prefetch). Fall back to fetching.
      setContextLoading(true)
      setContextError(getPrefetchError())

      try {
        const [nextAccount, nextSession, listResp, treeResp] = await Promise.all([
          workspace.account(client),
          workspace.session(client),
          workspace.listProject(client),
          workspace.systemTree(client).catch(() => ({ Nodes: [] }) as SystemTreeResp),
        ])

        if (cancelled) return

        const nextProjects = (listResp.Items ?? []).map(mapProjectRef)
        // The system meta project is an internal storage node (always first);
        // never auto-select it as the landing project.
        const firstRaw = (listResp.Items ?? []).find(p => !p.System)
        const activeRef = firstRaw?.ActorId ? mapProjectRef(firstRaw) : null

        setAccount(nextAccount)
        setSession(nextSession)
        setProjects(nextProjects)
        setSystemTreeNodes(treeResp?.Nodes ?? [])
        setActiveProject(activeRef)
      } catch (error) {
        if (!cancelled) {
          setContextError(error instanceof Error ? error.message : 'Failed to load shell context')
        }
      } finally {
        if (!cancelled) setContextLoading(false)
      }
    }

    loadContext()

    return () => {
      cancelled = true
    }
  }, [])


  useEffect(() => {
    return workspace.OnMounts(client, (event) => {
      setProjects(prev => {
        const openId = prev.find(p => p.IsOpen)?.ProjectID
        return event.Mounts?.map(ref => {
          const snap = mapProjectRef(ref)
          return openId && snap.ProjectID === openId ? { ...snap, IsOpen: true } : snap
        })
      })
    })
  }, [])

  useEffect(() => {
    let cancelled = false
    getAIShellProjectUIState().then(loaded => {
      if (!cancelled) {
        setProjectUIState(loaded)
        setProjectUIHydrated(true)
      }
    }).catch(() => {
      if (!cancelled) setProjectUIHydrated(true)
    })
    return () => { cancelled = true }
  }, [])

  useEffect(() => {
    if (!projectUIHydrated) return
    void saveAIShellProjectUIState(projectUIState).catch(() => {})
  }, [projectUIHydrated, projectUIState])

  useEffect(() => {
    if (!wailsWindowController.isHostMode()) return
    const refresh = () => { wailsWindowController.isMaximised().then(setMaximised).catch(() => {}) }
    refresh()
    window.addEventListener('resize', refresh)
    return () => { window.removeEventListener('resize', refresh) }
  }, [])

  // Fetch aggregators lazily when the New/Edit/Clone/Fork Agent dialog opens so that
  // late-spawned aggregators (e.g. default after config.import) are visible.
  useEffect(() => {
    const dialogOpen = !!createAgentProjectId || !!editAgent || !!cloneAgent || !!forkTurn
    if (!dialogOpen) return
    let cancelled = false
    aimanager_aggregator.aggregatorList(client).then(resp => {
      if (!cancelled) setAggregators(resp.Items)
    }).catch(() => {
      if (!cancelled) setAggregators([])
    })
    return () => { cancelled = true }
  }, [createAgentProjectId, editAgent, cloneAgent, forkTurn])

  // Re-fetch aggregators when another panel mutates them.
  useEffect(() => {
    const handler = () => {
      aimanager_aggregator.aggregatorList(client).then(resp => {
        setAggregators(resp.Items)
      }).catch(() => {
        setAggregators([])
      })
    }
    window.addEventListener('sporemind:aggregators-changed', handler)
    return () => window.removeEventListener('sporemind:aggregators-changed', handler)
  }, [])

  // Fetch agent kinds lazily when the New/Edit/Clone/Fork Agent dialog opens.
  // Initial state comes from the localStorage cache (see useState initializer
  // above) so the dialog shows options immediately, then refreshes on open.
  useEffect(() => {
    const dialogOpen = !!createAgentProjectId || !!editAgent || !!cloneAgent || !!forkTurn || !!startWorkflowMapId
    if (!dialogOpen) return
    let cancelled = false
    ;(async () => {
      try {
        const [kindsResp, configsResp] = await Promise.all([
          workspace.listAgentKinds(client),
          workspace.listAgentKindConfigs(client),
        ])
        if (cancelled) return

        const configByKind = new Map(configsResp.Items?.map(config => [config.Kind, config]) ?? [])
        const kinds = (kindsResp.Items ?? [])
          .map((kindInfo): AgentKindOption | null => {
            const config = configByKind.get(kindInfo.Kind)
            if (!config || !config.UserCreatable) return null
            return {
              kind: config.Kind,
              displayName: config.DisplayName || config.Kind,
              randomName: config.RandomName,
              namePool: config.NamePool,
            }
          })
          .filter((config): config is AgentKindOption => config !== null)

        setAgentKinds(kinds)
        try {
          localStorage.setItem('sporemind:agent-kinds', JSON.stringify(kinds))
        } catch { /* ignore */ }
      } catch {
        if (!cancelled) {
          setAgentKinds([])
          try {
            localStorage.removeItem('sporemind:agent-kinds')
          } catch { /* ignore */ }
        }
      }
    })()
    return () => { cancelled = true }
  }, [createAgentProjectId, editAgent, cloneAgent, forkTurn, startWorkflowMapId])

  const activeAgent = useMemo(() => {
    if (!state.activeSessionId) return null
    // Project agents resolve within the active project; a global coordinator
    // (project-less) resolves from the full agent list so it stays selectable
    // as the active agent even when a project is open.
    return activeProjectAgents.find(a => a.Id === state.activeSessionId)
      ?? allAgents.find(a => a.Id === state.activeSessionId && !a.ProjectId)
      ?? null
  }, [state.activeSessionId, activeProjectAgents, allAgents])

  // Task-mode composer badge for a session bound to a scheduler (task) card:
  // surfaces the task's runtime mode (prompt / template) in the composer badge
  // row, mirroring the scheduled-task list badge.
  const activeTaskModeBadge = useMemo<ComposerBadge | null>(() => {
    if (!activeAgent?.BoundTaskCardId) return null
    const card = monoState.cards.find(c => c.id === activeAgent.BoundTaskCardId)
    if (!card || card.type !== 'scheduler') return null
    const data = (card.data ?? {}) as Record<string, unknown>
    const templateId = typeof data.workflow_template === 'string' ? data.workflow_template : ''
    const mode = resolveTaskMode(typeof data.task_mode === 'string' ? data.task_mode : undefined, templateId || undefined)
    const label = mode === 'template' ? t('scheduled.taskMode.template') : t('scheduled.taskMode.prompt')
    return {
      icon: mode === 'template' ? 'layout-grid' : 'file-text',
      color: mode === 'template' ? '#7c3aed' : undefined,
      title: label,
      label,
    }
  }, [activeAgent?.BoundTaskCardId, monoState.cards, t])

  // Sync active agent actor ID with the global session store for panels.
  useEffect(() => {
    setActiveAgentId(activeAgent?.ActorId ?? null, activeAgent?.ProjectId || null)
  }, [activeAgent])

  // Sync git store context to the active agent's project+worktree.
  useEffect(() => {
    if (activeAgent?.ProjectId) {
      gitStore.setContext({ projectId: activeAgent.ProjectId, worktreeId: activeAgent.WorktreeID })
    }
  }, [activeAgent?.ProjectId, activeAgent?.WorktreeID])

  // Play a notification sound when the active agent's turn completes.
  useTurnCompleteNotifier(activeAgent?.ActorId ?? null)

  // Toast overlay action channel: opens the schema overlay modal on a
  // triggered action (see [[schema-overlay-input-modal]] for the three
  // entry-point contract).
  useToastActionEvents()

  // Validate the active session after restore and whenever the agent list or
  // project changes. Only fall back when a non-null selection is missing.
  // Workspace selection (null) is preserved so agents stay lazy-loaded.
  useEffect(() => {
    if (!sessionRestored) return
    if (!activeProject) return
    if (activeProjectAgents.length === 0) return
    // Don't override a selection that is waiting for a newly-created agent to
    // appear in the agent list; the pending-first-message effect will handle it.
    if (pendingFirstMessage) return
    const activeId = state.activeSessionId
    if (!activeId) return
    if (activeProjectAgents.some(a => a.Id === activeId)) return
    // A global (project-less) agent selection is valid regardless of project;
    // don't let the project-scoped fallback steal focus from it.
    if (allAgents.some(a => a.Id === activeId && !a.ProjectId)) return
    const previousId = state.previousSessionId
    const fallbackId = previousId && previousId !== activeId && activeProjectAgents.some(a => a.Id === previousId)
      ? previousId
      : (activeProjectAgents.find(a => a.ActiveTurnRef) ?? activeProjectAgents[0]!).Id
    dispatch({ type: 'SELECT_SESSION', id: fallbackId })
  }, [activeProject, activeProjectAgents, allAgents, sessionRestored, pendingFirstMessage])

  // Default landing: at startup, if a global coordinator exists and nothing is
  // explicitly selected, land on the coordinator chat. Runs once per mount.
  const landingBootstrappedRef = useRef(false)
  useEffect(() => {
    if (landingBootstrappedRef.current) return
    if (!sessionRestored) return
    if (!globalCoordinator) return
    landingBootstrappedRef.current = true
    if (!state.activeSessionId) {
      dispatch({ type: 'SELECT_SESSION', id: globalCoordinator.Id })
    }
  }, [globalCoordinator, sessionRestored, state.activeSessionId])

  // Derived synchronously so turnActorId is never one render behind activeAgent.
  const turnActorId = activeAgent?.ActiveTurnRef ?? null
  activeAgentIdRef.current = activeAgent?.ActorId ?? null

  // Stamp the agent's LastAccessedAt on the workspace whenever its
  // conversation becomes active, so unread-completion badges clear once the
  // agent has actually been viewed.
  const activeAgentActorId = activeAgent?.ActorId ?? null
  useEffect(() => {
    if (!activeAgentActorId) return
    workspace.agentAccess(client, { AgentActorId: activeAgentActorId }).catch(() => {})
  }, [activeAgentActorId])
  const source = useAIShellSource(shellVariant, undefined, turnActorId, activeAgent?.ActorId)
  // Session goal of the active timeline — the same object the turn-tail goal
  // row renders (projection injects the current goal into the active envelope).
  // Scanned from the last envelope backwards and kept in a ref so the
  // goal-mode badge click handler reads the latest value without effect churn.
  const activeGoalRef = useRef<SessionGoal | null>(null)
  let activeGoal: SessionGoal | undefined
  for (let i = source.envelopes.length - 1; i >= 0; i--) {
    const g = source.envelopes[i]?.goal
    if (g) { activeGoal = g; break }
  }
  activeGoalRef.current = activeGoal ?? null
  const [mountedModeCardIds, setMountedModeCardIds] = useState<string[]>([])
  const composerPlaceholder = useMemo(() => {
    const ctx: ComposerPlaceholderContext = {
      agentStatus: activeAgent?.Status,
      boundTaskCardId: activeAgent?.BoundTaskCardId,
      hasConversation: source.envelopes.length > 0,
      mountedModeCardIds,
    }
    return t(resolveComposerPlaceholderKey(ctx) as Parameters<typeof t>[0])
  }, [activeAgent?.Status, activeAgent?.BoundTaskCardId, source.envelopes.length, mountedModeCardIds, t])

  const handlePause = useCallback(() => {
    if (source.isWaiting) {
      source.pauseAll()
    } else {
      source.pause()
    }
  }, [source.isWaiting, source.pauseAll, source.pause])

  const providerContext = useAIShellProviders(activeAgent, source.currentUnit)

  const [globalPermissionMode, setGlobalPermissionMode] = useState<PermissionMode>('permission')

  const handleSelectRoute = useCallback(async (routeId: string) => {
    if (!activeAgent?.Id) return
    const slot = routeId === SYSTEM_AGGREGATOR_ID ? autoSlot() : aggregatorSlot(routeId)
    try {
      await updateAgentState(activeAgent.Id, { Primary: slot })
    } catch (err) {
      setContextError(err instanceof Error ? err.message : 'Failed to update model route')
    }
  }, [activeAgent])

  const handleSelectUnit = useCallback(async (routeId: string, option: ProviderOption) => {
    if (!activeAgent?.Id || !option.unit) return
    // routeId is the aggregator whose pool the unit was picked from. Pass it as
    // the serving aggregator so dispatch routes the unit THROUGH it (pinnedUnitSlot
    // normalizes SYSTEM_AGGREGATOR_ID to a system-served unit).
    const slot = pinnedUnitSlot(option.unit, routeId)
    try {
      await updateAgentState(activeAgent.Id, { Primary: slot })
    } catch (err) {
      setContextError(err instanceof Error ? err.message : 'Failed to update model')
    }
  }, [activeAgent])

  useEffect(() => {
    workspace_preferences.preferencesGet(client).then(snap => {
      const saved = normalizePermissionMode(snap.Preferences?.['permissionMode'])
      if (saved) {
        setGlobalPermissionMode(saved)
      }
    }).catch(() => {})
  }, [])

  // The agent's actual permission mode is read from the backend snapshot
  // (AgentInfo.PermissionMode, surfaced via AgentRuntimeState). This is the
  // authoritative live value: it reflects the mode the agent was spawned with
  // and any subsequent permission_mode_set changes. When the snapshot has no
  // value (e.g. agent unloaded, no status push yet), fall back to the global
  // preference so the UI shows a sensible default.
  const effectivePermissionMode: PermissionMode = useMemo(() => {
    return normalizePermissionMode(activeAgent?.PermissionMode) ?? globalPermissionMode
  }, [activeAgent?.PermissionMode, globalPermissionMode])

  const handlePermissionModeChange = useCallback((mode: PermissionMode) => {
    // Push to the currently active agent. The agent is already loaded and
    // running at this point (the user is interacting with it), so the RPC
    // should succeed. The agent's status push will update the snapshot, and
    // effectivePermissionMode will re-derive from the new snapshot value.
    const actorId = activeAgent?.ActorId
    if (actorId) {
      agent_permission_mode
        .permissionModeSet(client, { Mode: mode }, { target: actorId })
        .catch(() => {})
    }
  }, [activeAgent?.ActorId])

  const handleGlobalPermissionModeChange = useCallback((mode: PermissionMode) => {
    setGlobalPermissionMode(mode)
    savePreference('permissionMode', mode, 'permission-mode', { v: 0 }).catch(() => {})
  }, [])

  const handleProviderChange = useCallback(async (unitId: string) => {
    // Optimistic local update
    providerContext.onProviderChange(unitId)
    // Persist to backend
    if (!activeAgent?.Id) return
    const unit = providerContext.providers.find(p => p.id === unitId)
    if (!unit) return
    try {
      await updateAgentState(activeAgent.Id, {
        Primary: unitToSlot({ model: unit.label, provider: unit.subtitle ?? '' }),
      })
    } catch (err) {
      setContextError(err instanceof Error ? err.message : 'Failed to update agent model')
    }
  }, [activeAgent, providerContext])

  // Slot-menu pick: persist a single agent model slot. `slotName` is one of the
  // five AgentStatusResp slot fields, so it doubles as the AgentStatePatch key.
  // updateAgentState merges the patch over the store snapshot before sending, so
  // the other four slots are never clobbered.
  const handleSelectSlot = useCallback(async (slotName: SlotName, slotValue: ModelSlot) => {
    if (!activeAgent?.Id) return
    const patch: Partial<Record<SlotName, ModelSlot>> = { [slotName]: slotValue }
    try {
      await updateAgentState(activeAgent.Id, patch)
    } catch (err) {
      setContextError(err instanceof Error ? err.message : 'Failed to update agent model slot')
    }
  }, [activeAgent])

  const handleOpenCardTab = useCallback((cardId: string, label: string, options?: { edit?: boolean; projectId?: string | null }) => {
    const tabId = `card-${cardId}`
    // Tabs carry the project the card was opened from so they keep resolving
    // after the shell switches to another project.
    const projectId = options?.projectId !== undefined ? options.projectId : (activeProject?.ProjectID ?? null)
    setRightTabs(prev => {
      if (!prev.some(t => t.id === tabId)) {
        return [...prev, { id: tabId, type: 'mono-card' as const, label, payload: { cardId, projectId, edit: options?.edit } }]
      }
      // Tab already open: refresh its payload (e.g. edit: true) and re-activate.
      // The origin project is sticky — reopening from another project must not
      // silently retarget an existing tab.
      return prev.map(t => (t.id === tabId ? { ...t, payload: { ...((t.payload ?? {}) as { cardId: string; edit?: boolean }), edit: options?.edit } } : t))
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [activeProject?.ProjectID])

  const handleOpenMemoryNodeTab = useCallback((node: MemoryNode) => {
    const tabId = `memory-${node.Id}`
    const label = node.Head || node.Id
    setRightTabs(prev => {
      if (prev.some(t => t.id === tabId)) return prev
      return [...prev, { id: tabId, type: 'memory-node' as const, label, payload: { node } }]
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [])

  const handleOpenCardForEdit = useCallback((cardId: string) => {
    handleOpenCardTab(cardId, cardId, { edit: true })
  }, [handleOpenCardTab])

  // Temporary read-only text tab (e.g. a plain-text goal). Singleton: clicking
  // another goal replaces the tab content instead of stacking throwaway tabs.
  const handleOpenTextTab = useCallback((label: string, text: string) => {
    const tabId = 'goal-text'
    setRightTabs(prev => {
      const next = { id: tabId, type: 'goal-text' as const, label, payload: { text } }
      if (prev.some(t => t.id === tabId)) return prev.map(t => (t.id === tabId ? next : t))
      return [...prev, next]
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [])

  // Open an SSH session as a right-panel tab. The tab id is keyed on hostId
  // (not sessionId) so it stays stable across reconnects. If a tab for this host
  // already exists, its sessionId is refreshed. The session stays alive while
  // its tab exists; closing the tab closes the backend session
  // (handleCloseRightTab).
  const handleOpenSshSession = useCallback((sessionId: string, hostId: string, hostName: string) => {
    const tabId = sshTabId(hostId)
    setRightTabs(prev => {
      const existing = prev.find(t => t.id === tabId && t.type === 'ssh-session')
      if (existing) {
        // Same host already has a tab: refresh its sessionId instead of
        // opening a second tab. Best-effort close the old session; the view
        // closes stale sessions on reconnect anyway (SshSessionView), so a
        // double close is harmless.
        const old = (existing.payload as { sessionId?: string }).sessionId
        if (old && old !== sessionId) {
          void sshmanagerClient.shellClose(client, { SessionId: old }).catch(() => {})
        }
        return prev.map(t =>
          t.id === tabId && t.type === 'ssh-session'
            ? { ...t, label: hostName, payload: { sessionId, hostId, hostName } }
            : t,
        )
      }
      return [...prev, { id: tabId, type: 'ssh-session' as const, label: hostName, payload: { sessionId, hostId, hostName } }]
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [])

  // Open a database session as a right-panel tab. Mirrors handleOpenSshSession:
  // tab id is keyed on profileId (from dbTabId) so the tab stays stable across
  // re-opens. Unlike SSH, dbclient manages connection handles internally; the
  // view dials lazily on the first tree/read call.
  const handleOpenDbSession = useCallback((profileId: string, profileName: string, backend: string) => {
    const tabId = dbTabId(profileId)
    setRightTabs(prev => {
      const existing = prev.find(t => t.id === tabId && t.type === 'db-session')
      if (existing) {
        // Same profile already has a tab: refresh its label/backend (the user
        // may have edited the profile in the meantime).
        return prev.map(t =>
          t.id === tabId && t.type === 'db-session'
            ? { ...t, label: profileName, payload: { profileId, profileName, backend } }
            : t,
        )
      }
      return [...prev, { id: tabId, type: 'db-session' as const, label: profileName, payload: { profileId, profileName, backend } }]
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [])

  // Open an object-storage session (oss | webdav) as a right-panel tab.
  // Separate from handleOpenDbSession so the dedicated ObjectStorageSessionView
  // can ship its own tree + transfer panel without inheriting the SQL/JSON
  // query editor baked into DbSessionView.
  const handleOpenObjectStorage = useCallback((profileId: string, profileName: string, backend: string) => {
    const tabId = objectStorageTabId(profileId)
    setRightTabs(prev => {
      const existing = prev.find(t => t.id === tabId && t.type === 'object-storage')
      if (existing) {
        return prev.map(t =>
          t.id === tabId && t.type === 'object-storage'
            ? { ...t, label: profileName, payload: { profileId, profileName, backend } }
            : t,
        )
      }
      return [...prev, { id: tabId, type: 'object-storage' as const, label: profileName, payload: { profileId, profileName, backend } }]
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [])

  // Shared profile → right-tab routing used by both the settings panel and
  // the "Databases" content mode: object-storage profiles (oss / webdav) go
  // through the dedicated ObjectStorageSessionView; everything else falls
  // through to the SQL/JSON DbSessionView.
  const handleOpenDbClient = useCallback((p: { Id: string; Name: string; Backend: string }) => {
    if (p.Backend === 'oss' || p.Backend === 'webdav') {
      handleOpenObjectStorage(p.Id, p.Name, p.Backend)
    } else {
      handleOpenDbSession(p.Id, p.Name, p.Backend)
    }
  }, [handleOpenDbSession, handleOpenObjectStorage])

  // Open a remote file from an SSH session's FTP browser in a right-panel
  // editor tab. Deduped by (session, path) so re-opening the same file just
  // activates its existing tab.
  const handleOpenSshFile = useCallback((sessionId: string, path: string) => {
    const name = path.split('/').pop() || path
    const tabId = `ssh-file-${sessionId}-${path}`
    setRightTabs(prev => {
      if (prev.some(t => t.id === tabId)) return prev
      return [...prev, { id: tabId, type: 'ssh-file' as const, label: name, payload: { sessionId, path } }]
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [])

  // Open an ssh-session right-panel tab when the sshmanager emits an
  // open_session event (triggered by an agent calling open_ssh_session or
  // by the SSH manager panel opening a session).
  useEffect(() => {
    return sshmanagerClient.OnSshManagerEvent(client, (event) => {
      if (event.Kind === 'close_session') {
        const sessionId = event.SessionId
        if (!sessionId) return
        // Remove only the tab still pointing at the closed session: a tab
        // that has already moved on (reconnect/refresh) keeps its newer id.
        setRightTabs(prev => {
          const removed = prev.filter(t => t.type === 'ssh-session' && (t.payload as { sessionId?: string }).sessionId === sessionId)
          if (!removed.length) return prev
          removed.forEach(t => navHistory.removeTab(t.id))
          const next = prev.filter(t => !removed.includes(t))
          setActiveBrowserTabId(active => next.some(t => t.id === active) ? active : (next[next.length - 1]?.id ?? null))
          return next
        })
        return
      }
      if (event.Kind !== 'open_session') return
      const sessionId = event.SessionId
      const hostId = event.HostId
      const hostName = event.HostName
      if (!sessionId || !hostId) return
      handleOpenSshSession(sessionId, hostId, hostName)
    })
  }, [handleOpenSshSession, navHistory])

  // Propagate a reconnected session id back into the tab payload so the close
  // handler (handleCloseRightTab) closes the live session, not a stale one.
  // KeepAlive-safe: this is a state-change notification driven by the child,
  // never an unmount-close side effect.
  const handleSshSessionIdChange = useCallback((tabId: string, sessionId: string) => {
    setRightTabs(prev => prev.map(t =>
      t.id === tabId && t.type === 'ssh-session'
        ? { ...t, payload: { ...(t.payload as { sessionId: string; hostId: string; hostName: string }), sessionId } }
        : t
    ))
  }, [])

  // Bump-and-set helper for the knowledge-base open request: the monotonic key
  // makes repeated navigation to the same target re-fire inside KnowledgeModeView.
  // The request is cleared once the mode reports it consumed, so a remount (mode
  // tabs are not keep-alive) can never replay an abandoned navigation.
  const requestKbLane = useCallback((projectId: string | null, cardId?: string | null) => {
    kbOpenRequestKeyRef.current += 1
    setKbOpenRequest({ key: kbOpenRequestKeyRef.current, projectId, cardId: cardId ?? null })
  }, [])

  const handleKbOpenRequestConsumed = useCallback((key: number) => {
    setKbOpenRequest(prev => (prev && prev.key === key ? null : prev))
  }, [])

  const handleContentModeChange = useCallback((mode: ContentMode, params?: { cardId?: string; label?: string; projectId?: string }) => {
    // Notes mode: sidebar card click. The knowledge base is global, so the
    // click carries its target project/card into the open request consumed by
    // KnowledgeModeView (rather than binding the mode to one project).
    if (mode === 'notes' && params?.cardId) {
      requestKbLane(params.projectId ?? null, params.cardId)
      if (rightPanelExpandedRef.current) {
        // Fullscreen: also open as a right panel tab.
        handleOpenCardTab(params.cardId, params.label ?? params.cardId, { projectId: params.projectId ?? undefined })
      } else {
        // Non-fullscreen: show the card's swimlane in the knowledge base view.
        setContentMode('notes')
      }
      return
    }
    // Switching to a regular content mode always exits settings sidebar mode,
    // mirroring how switching tabs leaves the settings view.
    setSidebarMode('normal')
    // When the right panel is expanded, switch to the content-mode tab instead of collapsing.
    if (rightPanelExpandedRef.current) {
      setContentMode(mode)
      setActiveBrowserTabId(`mode:${mode}`)
      if (mode === 'topology' || mode === 'workflow') {
        monoStore.load().catch(() => {})
      }
      return
    }
    setRightPanelExpanded(false)
    setContentMode(mode)
    // Reload wiki cards when entering topology or workflow mode so the graph
    // reflects agents/cards created since the last load.
    if (mode === 'topology' || mode === 'workflow') {
      monoStore.load().catch(() => {})
    }
  }, [handleOpenCardTab, requestKbLane])

  const restoreEntry = useCallback(
    (entry: {
      contentMode: ContentMode
      rightTabs: Array<{ id: string; type: string; label: string; payload: unknown }>
      activeBrowserTabId: string | null
      rightPanelOpen: boolean
      filePath: string | null
      folderPath: string | null
      selectedFilePath: string | null
      cursorPos: number | null
      selectionFrom: number | null
      selectionTo: number | null
      activeAgentId: string | null
    }) => {
      skipPanelSyncRef.current = true
      setContentMode(entry.contentMode)
      setRightTabs(entry.rightTabs as any)
      setActiveBrowserTabId(entry.activeBrowserTabId)
      setRightPanelOpen(entry.rightPanelOpen)
      setFileBrowserPath(entry.folderPath)
      setFileBrowserSelection(entry.selectedFilePath)
      setCursorPos(entry.cursorPos)
      setSelectionFrom(entry.selectionFrom)
      setSelectionTo(entry.selectionTo)
      // Restore the agent via navigateToAgent (handles cross-project switch
      // and lazy load); switchToConversation=false because contentMode is
      // already restored explicitly above.
      const agent = entry.activeAgentId
        ? allAgentsRef.current.find(a => a.Id === entry.activeAgentId || a.ActorId === entry.activeAgentId)
        : null
      if (agent) {
        void navigateToAgentRef.current(agent.ProjectId, agent.Id, { switchToConversation: false })
      } else {
        dispatch({ type: 'SELECT_SESSION', id: entry.activeAgentId ?? '' })
      }
    },
    [],
  )

  const goBackContentMode = useCallback(() => {
    const prev = navHistory.goBack()
    if (!prev) return
    historyNavigationRef.current = true
    setCursorRestoreKey(k => k + 1)
    restoreEntry(prev)
  }, [navHistory, restoreEntry])

  const goForwardContentMode = useCallback(() => {
    const next = navHistory.goForward()
    if (!next) return
    historyNavigationRef.current = true
    setCursorRestoreKey(k => k + 1)
    restoreEntry(next)
  }, [navHistory, restoreEntry])

  // Active file path is derived from the currently-active right-panel file tab.
  const activeFilePath = useMemo(() => {
    const active = rightTabs.find(t => t.id === activeBrowserTabId)
    if (active?.type === 'file') {
      return (active.payload as { filePath?: string }).filePath ?? null
    }
    return null
  }, [rightTabs, activeBrowserTabId])

  // Build the current navigation-history entry from layout state.
  const currentEntry = useMemo(
    () => ({
      contentMode,
      rightTabs: rightTabs.map(t => ({ id: t.id, type: t.type, label: t.label, payload: t.payload })),
      activeBrowserTabId,
      rightPanelOpen,
      filePath: activeFilePath,
      cursorPos,
      selectionFrom,
      selectionTo,
      folderPath: fileBrowserPath,
      selectedFilePath: fileBrowserSelection,
      activeAgentId: state.activeSessionId ?? null,
    }),
    [contentMode, rightTabs, activeBrowserTabId, rightPanelOpen, activeFilePath, cursorPos, selectionFrom, selectionTo, fileBrowserPath, fileBrowserSelection, state.activeSessionId],
  )

  // Push changed layout state into the navigation history (guarded during back/forward restores).
  // The guard is reset via setTimeout(0) so it survives cascading effects (e.g.
  // rightPanelOpen sync) that fire in subsequent render cycles after a restore.
  // Pushes are suppressed until the persisted layout has been restored so the
  // first history entry is the true initial frame (restored tabs included),
  // not the pre-restore empty state.
  useEffect(() => {
    if (!sessionRestored) return
    if (historyNavigationRef.current) {
      const id = setTimeout(() => { historyNavigationRef.current = false }, 0)
      return () => clearTimeout(id)
    }
    navHistory.push(currentEntry)
  }, [sessionRestored, currentEntry, navHistory])

  const handleCursorChange = useCallback(
    (pos: number, selFrom: number, selTo: number) => {
      setCursorPos(pos)
      setSelectionFrom(selFrom)
      setSelectionTo(selTo)
      navHistory.updateCursor({ pos, from: selFrom, to: selTo })
    },
    [navHistory],
  )

  const handleFileBrowserPathChange = useCallback(
    (path: string) => {
      setFileBrowserPath(path)
      navHistory.push({
        ...currentEntry,
        folderPath: path,
        selectedFilePath: null,
      })
    },
    [currentEntry, navHistory],
  )

  const handleFileBrowserSelectionChange = useCallback(
    (path: string | null) => {
      setFileBrowserSelection(path)
      navHistory.push({
        ...currentEntry,
        selectedFilePath: path,
      })
    },
    [currentEntry, navHistory],
  )

  // File mode owns its project target (picked in the browser toolbar), so a
  // project switch there must drop the restored path/selection of the previous
  // project and let the browser reload its per-project persisted state.
  const handleFileBrowserProjectChange = useCallback(() => {
    setFileBrowserPath(null)
    setFileBrowserSelection(null)
  }, [])

  const handleTogglePinnedMode = useCallback((mode: string) => {
    setPinnedModes(prev => prev.includes(mode) ? prev.filter(m => m !== mode) : [...prev, mode])
  }, [])

  const handleReorderPinnedModes = useCallback((ordered: string[]) => {
    setPinnedModes(ordered)
  }, [])

  const handleSend = useCallback(async (textOverride?: string, attachments?: AttachmentEntry[], images?: ImageEntry[]) => {
    if (shellVariant === 'preview') {
      dispatch({ type: 'SEND_MESSAGE' })
      return
    }
    // No usable model pool (no provider configured) — re-open the provider
    // onboarding instead of letting the send fail downstream.
    if (providerContext.providers.length === 0) {
      window.dispatchEvent(new CustomEvent('sporemind:show-provider-onboarding'))
      return
    }
    if (!activeAgent) {
      setContextError('Please select an agent first')
      return
    }
    const text = textOverride ?? state.composerValue.trim()
    if (!text && (!attachments || attachments.length === 0) && (!images || images.length === 0)) {
      return
    }

    // Intercept "! text" at the start: offer to save as a backlog card instead of sending.
    if (/^!\s*.+/.test(text)) {
      setBacklogIntercept(text.replace(/^!\s*/, ''))
      return
    }

    dispatch({ type: 'SEND_MESSAGE' })

    if (/^\/clear(\s|$)/.test(text)) {
      try {
        await agent_chat.chatSubmit(client, { Text: text }, { target: activeAgent.ActorId })
        getTimelineManager().clearHistory(activeAgent.ActorId)
      } catch (err) {
        setContextError(err instanceof Error ? err.message : 'Failed to clear history')
      }
      return
    }

    // Criterion: send must respect the slot, not the resolved display unit.
    // Pin a unit only when the slot pins one; for aggregator/auto routing pass
    // undefined so the backend picks per the slot's fallback chain.
    const sendUnit = providerContext.activeRoute?.kind === 'unit' ? providerContext.activeRoute.unit : undefined
    const thinkingLevel = providerContext.activeThinkingLevel
    console.log(`[ui.send] text="${text.slice(0, 60)}" model=${sendUnit?.model ?? ''} provider=${sendUnit?.provider ?? ''}`)
    try {
      const tempId = source.pushUserMessage(text)
      const request: AgentChatSubmitReq = {
        Text: text,
        Unit: sendUnit,
        Title: '',
        ThinkingBudget: thinkingLevel?.Mode === 'budget' ? thinkingLevel.Budget : undefined,
        ReasoningEffort: thinkingLevel?.Mode === 'effort' ? thinkingLevel.Effort : undefined,
        Attachments: attachments,
        Images: images,
        Meta: account ? `user|${account.AccountId}|${account.DisplayName}` : 'user',
      }
      const targetAgentId = activeAgent.ActorId
      const resp = await agent_chat.chatSubmit(
        client,
        request,
        { target: targetAgentId },
      )
      // Guard against undefined response (can happen if the backend returns
      // an empty payload — e.g. the agent's default loop is blocked and the
      // reply frame carries zero bytes, which decodePayload treats as
      // undefined). Without this guard, resp.TurnActorId throws
      // "Cannot read properties of undefined (reading 'TurnActorId')".
      if (!resp) {
        console.warn('[ui.send] chatSubmit returned undefined — agent may be blocked')
        setContextError('Agent is busy (previous turn still stopping). Please try again.')
        return
      }
      console.log(`[ui.send] submit resp: turnActorId=${resp.TurnActorId} msgId=${resp.MessageId} idx=${resp.Idx}`)
      // Guard against agent switch while this submit was in-flight.
      if (targetAgentId !== activeAgentIdRef.current) return
      // Proactively seed the running turn so isStreaming returns true
      // immediately, even before turn.started SSE arrives.
      if (resp.TurnActorId) {
        getTimelineManager().seedActiveTurn(targetAgentId, resp.TurnActorId)
      }
      if (tempId && resp.MessageId) {
        source.replaceUserMessageId(tempId, resp.MessageId)
      }
      if (resp.Idx != null && resp.Idx > 0 && resp.MessageId) {
        source.updateUserMessageIdx(resp.MessageId, resp.Idx)
      }
      // Backfill the assistant turn in case the live step stream dropped the
      // first events of the new turn (intermittent subscription race).
      getTimelineManager().reconcile(targetAgentId)
    } catch (err) {
      setContextError(err instanceof Error ? err.message : 'Failed to send message')
    }
  }, [shellVariant, activeAgent, state.composerValue, providerContext.providers, providerContext.activeRoute, providerContext.activeThinkingLevel])

  // Cross-component navigation: DAG "打开对话" or sporemind:open-agent-chat event.
  // Uses the shared agent list store so no extra fetch is needed.
  // The second argument may be either an agent actor ID or a stable agent ID.
  const navigateToAgent = useCallback(async (projectId: string, agentActorIdOrId: string, opts?: { switchToConversation?: boolean }) => {
    const gen = ++navigateGenRef.current
    const stale = () => gen !== navigateGenRef.current
    const currentActive = activeProjectRef.current
    const currentProjects = projectsRef.current

    if (currentActive?.ProjectID !== projectId) {
      const targetProject = currentProjects.find(p => p.ProjectID === projectId)
      if (!targetProject) {
        // 项目列表还没加载完成，保存 pending 等后续处理
        pendingNavRef.current = { projectId, agentActorId: agentActorIdOrId }
        return
      }
      setActiveProject(targetProject)
    }

    pendingNavRef.current = null
    const agents = allAgentsRef.current
    if (stale()) return

    // Diagnostic: log what we're looking for and what we have. Without this
    // it's impossible to tell whether navigation silently failed because the
    // agent isn't in the list, or because multiple agents share an empty
    // ActorId and the find returns the wrong one (AUDIT 8.3 keeps agents with
    // empty ActorID in the list — clicking any of them resolves to the first).
    console.info('[navigateToAgent]', {
      projectId,
      agentActorIdOrId,
      agentsCount: agents.length,
      agentsPreview: agents.map(a => ({ Id: a.Id, ActorId: a.ActorId, ProjectId: a.ProjectId, IsError: a.IsError })),
    })

    if (!agentActorIdOrId) {
      console.warn('[navigateToAgent] agentActorIdOrId is empty — aborting navigation.')
      return
    }

    const match = agents.find(a => a.ActorId === agentActorIdOrId || a.Id === agentActorIdOrId)
    if (!match) {
      console.warn('[navigateToAgent] no match — aborting navigation')
      return
    }

    if (opts?.switchToConversation !== false) {
      setContentMode('conversation')
    }

    if (match.LoadState !== 'loaded') {
      // Lazy-loaded agents retain their stable ActorId; LoadState determines
      // whether the actor must be started before switching sessions.
      try {
        await workspace.loadAgent(client, { AgentId: match.Id })
        dispatch({ type: 'SELECT_SESSION', id: match.Id })
      } catch (err) {
        setContextError(err instanceof Error ? err.message : 'Failed to load agent')
      }
      return
    }

    dispatch({ type: 'SELECT_SESSION', id: match.Id })
  }, [])
  navigateToAgentRef.current = navigateToAgent

  const handleSelectProjectAgent = useCallback((id: string) => {
    const agent = allAgentsRef.current.find(a => a.Id === id)
    // Route project agents through navigateToAgent so unloaded actors start
    // before their session becomes usable; global agents use their own path.
    if (agent && agent.ProjectId) {
      void navigateToAgent(agent.ProjectId, agent.Id, { switchToConversation: false })
      return
    }
    dispatch({ type: 'SELECT_SESSION', id })
  }, [navigateToAgent])

  const handleSwitchToAgent = useCallback((agentId: string) => {
    const agent = allAgents.find(item => item.Id === agentId)
    if (!agent) return
    void navigateToAgent(agent.ProjectId, agent.Id, { switchToConversation: true })
  }, [allAgents, navigateToAgent])

  // Resolve any pending navigation once the target project and store list are available.
  useEffect(() => {
    const pending = pendingNavRef.current
    if (!pending || activeProject?.ProjectID !== pending.projectId) return
    const match = allAgents.find(
      a => (a.ActorId === pending.agentActorId || a.Id === pending.agentActorId) && a.ProjectId === pending.projectId,
    )
    if (!match) return
    void navigateToAgent(pending.projectId, pending.agentActorId)
    pendingNavRef.current = null
  }, [activeProject, allAgents, navigateToAgent])

  // Purge timeline manager entries for agents that no longer exist in the store.
  useEffect(() => {
    const validActorIds = new Set(allAgents.map(a => a.ActorId))
    const manager = getTimelineManager()
    for (const actorId of manager.getTrackedAgents()) {
      if (!validActorIds.has(actorId)) {
        manager.release(actorId)
      }
    }
  }, [allAgents])

  // Handle agent avatar click: switch project if needed, then select agent.
  const handleAgentAvatarClick = useCallback(async (agent: AgentInfo) => {
    if (contentMode === 'multiconsole') {
      window.dispatchEvent(new CustomEvent('sporemind:multiconsole-add-agent', { detail: { agentId: agent.Id } }))
      return
    }
    await navigateToAgent(agent.ProjectId, agent.Id, { switchToConversation: false })
  }, [contentMode, navigateToAgent])

  const handleStartAgentChat = useCallback(async (agent: AgentInfo) => {
    await navigateToAgent(agent.ProjectId, agent.Id, { switchToConversation: true })
  }, [navigateToAgent])

  useEffect(() => {
    // On mount: consume any pending navigation queued before the panel was visible.
    // 即使 projects 还没加载导致 navigateToAgent 提前返回，pending 也会保留在
    // pendingNavRef 中，等 activeProject effect 的 listAgents 完成后继续处理。
    const pending = consumePendingAgentChat()
    if (pending) {
      pendingNavRef.current = pending
      navigateToAgent(pending.projectId, pending.agentActorId)
    }

    const handler = (e: Event) => {
      const { projectId, agentActorId } = (e as CustomEvent<{ projectId: string; agentActorId: string }>).detail
      navigateToAgent(projectId, agentActorId)
    }
    window.addEventListener('sporemind:open-agent-chat', handler)
    return () => window.removeEventListener('sporemind:open-agent-chat', handler)
  }, [navigateToAgent])

  // Cross-component clone trigger: the sidebar agent list dispatches the full
  // agent ref so we don't need allAgents to be loaded yet (the panel may have just mounted).
  useEffect(() => {
    const handler = (e: Event) => {
      const agent = (e as CustomEvent<AgentInfo>).detail
      if (!agent?.ActorId) return
      setCloneAgent(agent)
      agentOps.clearError()
    }
    window.addEventListener('sporemind:clone-agent', handler)
    return () => window.removeEventListener('sporemind:clone-agent', handler)
  }, [agentOps])

  // Fork-from-turn trigger: TurnTail dispatches agentActorId + turnId.
  // We resolve the AgentInfo from the currently active project's agent list.
  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent<{ agentActorId: string; turnId: string }>).detail
      if (!detail?.agentActorId || !detail.turnId) return
      const agent = activeProjectAgents.find(a => a.ActorId === detail.agentActorId)
      if (!agent) return
      setForkTurn({ agent, turnId: detail.turnId })
      agentOps.clearError()
    }
    window.addEventListener('sporemind:fork-from-turn', handler)
    return () => window.removeEventListener('sporemind:fork-from-turn', handler)
  }, [activeProjectAgents, agentOps])

  const defaultAgentKind = useMemo(() => agentKinds[0]?.kind || '', [agentKinds])

  const createAgentProject = useMemo(
    () => (projects ?? []).find(project => project.ProjectID === createAgentProjectId) ?? null,
    [createAgentProjectId, projects],
  )

  const handleCloseCreateAgent = useCallback(() => {
    if (agentOps.state.creating) return
    setCreateAgentProjectId(null)
    setEditAgent(null)
    setCloneAgent(null)
    setForkTurn(null)
    agentOps.clearError()
    creatingAgentFromTopologyRef.current = false
    topologyPlacementRef.current = null
  }, [agentOps])

  const openEditAgent = useCallback(async (agent: AgentInfo) => {
    setEditAgent(agent)
    agentOps.clearError()
  }, [agentOps])

  const handleCreateAgent = useCallback(async (input: AgentDialogInput) => {
    if (!account) return
    setContextError(null)
    const agent = await agentOps.createAgent(input, { agentKinds, aggregators })
    if (agent) {
      // New agents may be created for a project other than the currently active
      // one (e.g. from the New Agent dialog). Switch the shell to the agent's
      // project so the conversation stream can resolve activeAgent correctly
      // instead of getting stuck on the "Creating agent" placeholder.
      if (agent.ProjectId) {
        const targetProject = projectsRef.current.find(p => p.ProjectID === agent.ProjectId)
        if (targetProject && activeProjectRef.current?.ProjectID !== targetProject.ProjectID) {
          setActiveProject(targetProject)
          setProjects(prev => {
            const arr = Array.isArray(prev) ? prev : []
            return arr.map(p => ({ ...p, IsOpen: p.ProjectID === targetProject.ProjectID }))
          })
        }
      }
      // Refresh wiki cards so the topology graph picks up the new agent node.
      monoStore.load().catch(() => {})
      const pos = topologyPlacementRef.current
      if (pos && creatingAgentFromTopologyRef.current) {
        setPendingPlacement({ id: agentCardId(agent.Id), x: pos.x, y: pos.y })
        topologyPlacementRef.current = null
        creatingAgentFromTopologyRef.current = false
      }
      dispatch({ type: 'SELECT_SESSION', id: agent.Id })
      setPendingFirstMessage({ agentId: agent.Id, text: '', unit: null })
      setCreateAgentProjectId(null)
    }
  }, [account, agentKinds, aggregators, agentOps, setActiveProject, setProjects])

  // Onboarding step 2 completes here: land the user on the newly created
  // global coordinator's chat. The agent arrives in the store via SSE, which
  // flips OnboardingFlow's hasCoordinator and unmounts the setup modal.
  const handleCoordinatorCreated = useCallback((agent: AgentRef) => {
    monoStore.load().catch(() => {})
    dispatch({ type: 'SELECT_SESSION', id: agent.Id })
    // Guard the selection until the coordinator arrives via SSE: without this
    // the project-scoped validation effect would steal focus back to a project
    // agent before the global coordinator resolves as activeAgent.
    setPendingFirstMessage({ agentId: agent.Id, text: '', unit: null })
  }, [])

  // Selecting a workspace-global agent bypasses project-scoped navigation.
  const handleSelectCoordinator = useCallback(async (agent: AgentInfo) => {
    setContentMode('conversation')
    if (agent.LoadState !== 'loaded') {
      try {
        await workspace.loadAgent(client, { AgentId: agent.Id })
      } catch (err) {
        setContextError(err instanceof Error ? err.message : 'Failed to load agent')
        return
      }
    }
    dispatch({ type: 'SELECT_SESSION', id: agent.Id })
  }, [setContentMode, client])

  const handleNewProjectCreated = useCallback(async (ref: ProjectRef) => {
    setShowNewProjectDialog(false)
    const createdRef = mapProjectRef(ref)
    const createdId = createdRef.ProjectID
    try {
      const listResp = await workspace.listProject(client)
      const nextProjects = (listResp.Items ?? []).map(mapProjectRef)
      const createdProject = nextProjects.find(p => p.ProjectID === createdId) ?? createdRef
      setProjects(nextProjects.map(p => ({ ...p, IsOpen: p.ProjectID === createdId })))
      setActiveProject(createdProject)
      setProjectUIState(current => promoteProject(createdId, current))
    } catch {
      setActiveProject(createdRef)
      setProjectUIState(current => promoteProject(createdId, current))
    }
    const pos = topologyPlacementRef.current
    if (pos && creatingProjectFromTopologyRef.current) {
      setPendingPlacement({ id: createdId, x: pos.x, y: pos.y })
      topologyPlacementRef.current = null
      creatingProjectFromTopologyRef.current = false
    }
  }, [setActiveProject, setProjects, setProjectUIState])

  const handleNewProjectCancel = useCallback(() => {
    setShowNewProjectDialog(false)
    creatingProjectFromTopologyRef.current = false
    topologyPlacementRef.current = null
  }, [])

  const handleProjectNewChatAgentCreated = useCallback((agentId: string, text: string, unit: ModelUnit, modeCardId?: string) => {
    // Refresh wiki cards so the topology graph picks up the new agent node.
    monoStore.load().catch(() => {})
    dispatch({ type: 'SELECT_SESSION', id: agentId })
    setPendingFirstMessage({ agentId, text, unit, modeCardId })
  }, [])

  const handleUpdateAgent = useCallback(async (input: AgentDialogInput) => {
    if (!editAgent) return
    await agentOps.updateAgent(input, { editAgent, aggregators })
    setEditAgent(null)
  }, [editAgent, aggregators, agentOps])

  const handleDeleteAgent = useCallback((agent: AgentInfo) => {
    setDeleteConfirmAgent(agent)
  }, [])

  const confirmDeleteAgent = useCallback(async () => {
    if (!deleteConfirmAgent) return
    const agent = deleteConfirmAgent
    setDeleteConfirmAgent(null)
    try {
      await agentOps.deleteAgent(agent)
      // Refresh wiki cards so the topology graph drops the deleted agent node.
      monoStore.load().catch(() => {})
      if (state.activeSessionId === agent.Id) {
        const fallbackId = state.previousSessionId && state.previousSessionId !== agent.Id && activeProjectAgents.some(a => a.Id === state.previousSessionId)
          ? state.previousSessionId
          : ''
        dispatch({ type: 'SELECT_SESSION', id: fallbackId })
      }
    } catch (error) {
      setContextError(error instanceof Error ? error.message : 'Failed to delete agent')
    }
  }, [deleteConfirmAgent, state.activeSessionId, state.previousSessionId, activeProjectAgents, agentOps])

  const handleClearAgentHistory = useCallback((agent: AgentInfo) => {
    setClearConfirmAgent(agent)
  }, [])

  const confirmClearAgentHistory = useCallback(async () => {
    if (!clearConfirmAgent || clearConfirmLoading) return
    const agent = clearConfirmAgent
    setClearConfirmLoading(true)
    try {
      await agent_chat.chatSubmit(client, { Text: '/clear' }, { target: agent.ActorId })
      getTimelineManager().clearHistory(agent.ActorId)
      setClearConfirmAgent(null)
    } catch (error) {
      setContextError(error instanceof Error ? error.message : 'Failed to clear history')
    } finally {
      setClearConfirmLoading(false)
    }
  }, [clearConfirmAgent, clearConfirmLoading])

  const handleOpenAgentChat = useCallback(async (agent: AgentInfo) => {
    // Opening an agent chat with no provider configured re-opens the provider
    // onboarding so the chat is usable right after configuration. Navigation
    // still proceeds underneath — the modal is a guide, not a blocker.
    if (providerContext.providers.length === 0) {
      window.dispatchEvent(new CustomEvent('sporemind:show-provider-onboarding'))
    }
    await navigateToAgent(agent.ProjectId, agent.Id)
  }, [navigateToAgent, providerContext.providers])

  const openBrainView = useCallback((agent: AgentInfo) => {
    void navigateToAgent(agent.ProjectId, agent.Id, { switchToConversation: false })
    setContentMode('topology')
    setTopologyMode('brain')
  }, [navigateToAgent])

  // Opening the brain requires the memory mode to be mounted on the agent;
  // ask first when it is not, mounting only after the user confirms.
  const handleOpenBrain = useCallback(async (agent: AgentInfo) => {
    if (agent.LoadState !== 'loaded') {
      try {
        await workspace.loadAgent(client, { AgentId: agent.Id })
      } catch (err) {
        setContextError(err instanceof Error ? err.message : 'Failed to load agent')
        return
      }
    }
    try {
      const snap = await agent_chat.memorySnapshot(client, {}, { target: agent.ActorId })
      if (snap.Mounted) {
        openBrainView(agent)
        return
      }
    } catch {
      // Snapshot unreadable: fall through and let the user decide.
    }
    setBrainMemoryConfirmAgent(agent)
  }, [openBrainView])

  const confirmMountMemoryAndOpenBrain = useCallback(async () => {
    if (!brainMemoryConfirmAgent || brainMemoryMountLoading) return
    const agent = brainMemoryConfirmAgent
    setBrainMemoryMountLoading(true)
    try {
      await agent_chat.componentMount(client, { CardId: 'builtin:mode:memory', Scope: 'user' }, { target: agent.ActorId })
      setBrainMemoryConfirmAgent(null)
      openBrainView(agent)
    } catch (error) {
      setContextError(error instanceof Error ? error.message : 'Failed to mount memory mode')
    } finally {
      setBrainMemoryMountLoading(false)
    }
  }, [brainMemoryConfirmAgent, brainMemoryMountLoading, openBrainView])

  // Memory-mode badge click → open the brain view for the composer agent. The
  // badge dispatches 'sporemind:open-brain' with the agent's ActorId (see
  // registerModeClickActions); handleOpenBrain performs the memory-mount
  // check and opens the brain topology view.
  useEffect(() => {
    const handler = (e: Event) => {
      const actorId = (e as CustomEvent<string | undefined>).detail
      const agent = actorId ? agentInfoSnapshot.byActorId.get(actorId) : undefined
      if (agent) void handleOpenBrain(agent)
    }
    window.addEventListener('sporemind:open-brain', handler)
    return () => window.removeEventListener('sporemind:open-brain', handler)
  }, [agentInfoSnapshot, handleOpenBrain])

  const handleOpenAgentMenu = useCallback((agent: AgentInfo, x: number, y: number) => {
    setAgentMenu({ agent, x, y })
  }, [])

  const handleCloseAgentMenu = useCallback(() => setAgentMenu(null), [])
  const handleCloseProjectMenu = useCallback(() => setProjectMenu(null), [])

  const handleOpenAgentInspector = useCallback(async (agent: AgentInfo) => {
    let actorId = agent.ActorId
    if (agent.LoadState !== 'loaded') {
      try {
        await workspace.loadAgent(client, { AgentId: agent.Id })
      } catch (err) {
        setContextError(err instanceof Error ? err.message : 'Failed to load agent')
        return
      }
    }
    const tabId = `agent-inspector-${agent.Id}`
    setRightTabs(prev => {
      if (prev.some(t => t.id === tabId)) return prev
      return [...prev, {
        id: tabId,
        type: 'agent-inspector' as const,
        label: agentDisplayName(agent.Title, agent.DisplayName),
        payload: { agentId: agent.Id, actorId },
      }]
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [])

  const handleOpenProjectMenu = useCallback((project: ProjectSnapshot, x: number, y: number) => {
    setProjectMenu({ project, x, y })
  }, [])

  const handleRenameProject = useCallback(async (project: ProjectMenuTarget['project'], name: string) => {
    try {
      await workspace.updateProject(client, {
        ProjectId: project.ProjectID,
        Name: name,
      })
    } catch (error) {
      setContextError(error instanceof Error ? error.message : 'Failed to rename project')
    }
  }, [])

  const handleOpenProjectInSystem = useCallback(async (project: ProjectMenuTarget['project']) => {
    const ok = await openInSystem(project.ProjectID, '.')
    if (!ok) setContextError('Failed to open project folder in system file manager')
  }, [])

  const handleCloseProject = useCallback((project: ProjectMenuTarget['project']) => {
    setCloseConfirmProject(project)
  }, [])

  const handleRegisterApp = useCallback(async (project: ProjectMenuTarget['project']) => {
    try {
      setContextError(null)
      setContextSuccess(null)
      const resp = await appmanagerClient.registerProject(client, { ProjectId: project.ProjectID })
      // Refresh the app registry with the full AppStatus returned by the backend
      const appRegistryModule = await import('../../application/app-registry')
      appRegistryModule.appRegistry.upsert(appRegistryModule.normalizeApp(resp.Status))
      setContextError(null)
      setContextSuccess(null)
      setContextSuccess(`${resp.Status.Id} registered successfully`)
    } catch (error) {
      setContextError(error instanceof Error ? error.message : 'Failed to register app')
      setContextSuccess(null)
    }
  }, [])

  const handleReloadApp = useCallback(async (project: ProjectMenuTarget['project']) => {
    try {
      setContextError(null)
      setContextSuccess(null)
      const app = await appmanagerClient.list(client, {})
      const existing = (app.Items ?? []).find(item => item.Id === project.ProjectID || item.Id === project.Name || item.Id === `app.${project.Name}`)
      if (!existing) {
        throw new Error('App is not registered; register the package before reloading')
      }
      const resp = await appmanagerClient.reloadProject(client, { ProjectId: project.ProjectID, AppId: existing.Id })
      const appRegistryModule = await import('../../application/app-registry')
      appRegistryModule.appRegistry.upsert(appRegistryModule.normalizeApp(resp.Status))
      setContextError(null)
      setContextSuccess(null)
      setContextSuccess(`${resp.Status.Id} reloaded successfully`)
    } catch (error) {
      setContextError(error instanceof Error ? error.message : 'Failed to reload app')
      setContextSuccess(null)
    }
  }, [])

  const confirmCloseProject = useCallback(async () => {
    if (!closeConfirmProject || closeConfirmLoading) return
    const project = closeConfirmProject
    setCloseConfirmLoading(true)
    try {
      await workspace.unmount(client, { ProjectId: project.ProjectID })
      if (activeProject?.ProjectID === project.ProjectID) {
        setActiveProject(null)
        setActiveAgentId(null)
      }
      const nextProjects = projects.filter(p => p.ProjectID !== project.ProjectID)
      setProjects(nextProjects)
    } catch (error) {
      setContextError(error instanceof Error ? error.message : 'Failed to close project')
    } finally {
      setCloseConfirmLoading(false)
      setCloseConfirmProject(null)
    }
  }, [activeProject, closeConfirmLoading, closeConfirmProject, projects])

  const handleCloneAgent = useCallback(async (input: AgentDialogInput) => {
    if (!cloneAgent) return
    const created = await agentOps.cloneAgent(input, { sourceAgent: cloneAgent, aggregators })
    if (created) {
      // Refresh wiki cards so the topology graph picks up the new agent node.
      monoStore.load().catch(() => {})
      // The backend copies session history asynchronously after the clone RPC
      // returns. Mark the timeline to poll for the deferred history.
      getTimelineManager().expectPendingHistory(created.ActorId)
      dispatch({ type: 'SELECT_SESSION', id: created.Id })
      setCloneAgent(null)
    }
  }, [cloneAgent, aggregators, agentOps])

  const handleForkFromTurn = useCallback(async (input: AgentDialogInput) => {
    if (!forkTurn) return
    const created = await agentOps.forkFromTurn(input, {
      sourceAgent: forkTurn.agent,
      turnId: forkTurn.turnId,
      aggregators,
    })
    if (created) {
      // Refresh wiki cards so the topology graph picks up the new agent node.
      monoStore.load().catch(() => {})
      // The backend copies session history asynchronously after the fork RPC
      // returns. Mark the timeline to poll for the deferred history.
      getTimelineManager().expectPendingHistory(created.ActorId)
      dispatch({ type: 'SELECT_SESSION', id: created.Id })
      // Guard against the session-validation effect overriding our selection
      // before the new agent arrives via SSE — same pattern as handleCreateAgent.
      setPendingFirstMessage({ agentId: created.Id, text: '', unit: null })
      setForkTurn(null)
    }
  }, [forkTurn, aggregators, agentOps])

  const handleProjectChange = useCallback(async (projectId: string) => {
    const targetProject = projects.find(project => project.ProjectID === projectId)
    if (!targetProject) return

    setProjectSwitchingId(projectId)
    setContextError(null)

    try {
      projectCardStore.setProjectId(targetProject.ProjectID)
      void projectCardStore.load()
      setActiveProject(targetProject)
      setProjects((currentProjects: any) => {
        const arr = Array.isArray(currentProjects) ? currentProjects : []
        return arr.map((project: any) => ({
          ...project,
          IsOpen: project.ProjectID === targetProject.ProjectID,
        }))
      })
      await workspace.updateProject(client, {
        ProjectId: targetProject.ProjectID,
        LastOpenedAt: new Date().toISOString(),
      })
      // Selecting a project makes it the sole sidebar selection; clear any
      // active agent session so the new-chat empty state can render.
      dispatch({ type: 'SELECT_SESSION', id: '' })
    } catch (error) {
      setContextError(error instanceof Error ? error.message : 'Failed to switch project')
    } finally {
      setProjectSwitchingId(null)
    }
  }, [projects])

  const handleOpenCreateAgent = useCallback((projectId?: string) => {
    if (!account) return

    let effectiveProjectId = projectId || activeProject?.ProjectID
    if (!effectiveProjectId) return

    const targetProject = projects.find(project => project.ProjectID === effectiveProjectId)
    const creatableKinds = agentKinds
    const projectAgents = allAgentsRef.current.filter(agent => agent.ProjectId === effectiveProjectId)
    const hasAllAgentKinds = targetProject && (
      targetProject.System || (creatableKinds.length > 0 && creatableKinds.every(option =>
        projectAgents.some(agent => agent.AgentKind === option.kind),
      ))
    )
    if (hasAllAgentKinds) {
      const projectOrder = orderedProjectIds(projects, projectUIState)
      const userProjectIds = projectOrder.filter(id =>
        projects.some(project => project.ProjectID === id && !project.System),
      )
      const currentIndex = projectOrder.indexOf(effectiveProjectId)
      if (currentIndex >= 0 && userProjectIds.length > 0) {
        const nextProjectId = [...projectOrder.slice(currentIndex + 1), ...projectOrder.slice(0, currentIndex)]
          .find(id => userProjectIds.includes(id))
        if (nextProjectId) {
          void handleProjectChange(nextProjectId)
          projectId = nextProjectId
          effectiveProjectId = nextProjectId
        }
      }
    }

    setCreateAgentProjectId(effectiveProjectId)
    agentOps.clearError()
    setContextError(null)
  }, [account, activeProject?.ProjectID, agentKinds, agentOps, handleProjectChange, projectUIState, projects])

  const handleReorderProjects = useCallback((orderedIds: string[]) => {
    if (orderedIds.length < 3) return
    const [draggedId, targetId, position] = orderedIds
    const dragged = (projects ?? []).find(project => project.ProjectID === draggedId)
    const target = (projects ?? []).find(project => project.ProjectID === targetId)
    // System meta projects stay pinned; ignore drags that would move them.
    if (dragged?.System || target?.System) return
    setProjectUIState(current => {
      if (!draggedId || !targetId || (position !== 'before' && position !== 'after')) return current
      const order = orderedProjectIds(projects, current).filter(id => id !== draggedId)
      const targetIndex = order.indexOf(targetId)
      if (targetIndex < 0) return current
      const insertIndex = position === 'before' ? targetIndex : targetIndex + 1
      return {
        ...current,
        // Persist only user order; system ids are re-prefixed by orderedProjectIds.
        sidebarOrder: [...order.slice(0, insertIndex), draggedId, ...order.slice(insertIndex)].filter(id => {
          const project = (projects ?? []).find(item => item.ProjectID === id)
          return !project?.System
        }),
      }
    })
  }, [projects])

  const handleReorderAgents = useCallback((orderedIds: string[]) => {
    if (orderedIds.length < 3) return
    const [draggedId, targetId, position] = orderedIds
    setProjectUIState(current => {
      if (!draggedId || !targetId || (position !== 'before' && position !== 'after')) return current
      const order = orderedAgentIds(allAgents, current.agentOrder ?? [], current.sidebarOrder).filter(id => id !== draggedId)
      const targetIndex = order.indexOf(targetId)
      if (targetIndex < 0) return current
      const insertIndex = position === 'before' ? targetIndex : targetIndex + 1
      return {
        ...current,
        agentOrder: [...order.slice(0, insertIndex), draggedId, ...order.slice(insertIndex)],
      }
    })
  }, [allAgents])

  const conversationOptions = useMemo(() => {
    if (!activeProjectAgents) return []
    return activeProjectAgents.map(agent => ({
      id: agent.Id,
      label: agentDisplayName(agent.Title, agent.DisplayName),
      isWorking: agent.IsWorking,
      isError: agent.IsError,
      statusLabel: agent.StatusLabel,
    }))
  }, [activeProjectAgents])

  const activeConversationId = state.activeSessionId ?? null
  const resolvedActiveProject = activeProject ?? projects.find(project => project.IsOpen) ?? null
  const sidebarProjects = useMemo(() => orderedSidebarProjects(projects ?? [], projectUIState), [projects, projectUIState])
  // The system meta project is internal storage; it must never appear in any
  // user-facing project selector or be auto-selected as a fallback.
  const userProjects = useMemo(() => (projects ?? []).filter(p => !p.System), [projects])

  // Send the first user message after a project-new-chat agent is created and selected.
  useEffect(() => {
    if (!pendingFirstMessage) return
    if (!activeAgent || activeAgent.Id !== pendingFirstMessage.agentId) return
    // Guard against selecting an agent whose actor hasn't been spawned yet
    // (e.g. post-restart lazy agents with empty ActorId). Without this, the
    // submit targets an empty string and silently fails.
    if (!activeAgent.ActorId) return
    const text = pendingFirstMessage.text
    const unit = pendingFirstMessage.unit
    const targetAgentId = activeAgent.ActorId
    setPendingFirstMessage(null)

    // Dialog-created agents have no first message; just clear the pending guard.
    // Quick-mode cards create the agent with an empty text and a modeCardId —
    // mount the mode here so the badge is up before the user types. The
    // conversation composer subscribed to modeMountStore at mount (child
    // effects run before this parent effect), so the request is consumed
    // immediately.
    if (text === '') {
      if (pendingFirstMessage.modeCardId) {
        requestModeMount(pendingFirstMessage.modeCardId, targetAgentId)
      }
      return
    }

    dispatch({ type: 'SEND_MESSAGE' })

    if (/^\/clear(\s|$)/.test(text)) {
      agent_chat.chatSubmit(client, { Text: text }, { target: targetAgentId })
        .then(() => {
          getTimelineManager().clearHistory(targetAgentId)
        })
        .catch(err => {
          setContextError(err instanceof Error ? err.message : 'Failed to clear history')
        })
      return
    }

    const tempId = source.pushUserMessage(text)
    const request: AgentChatSubmitReq = {
      Text: text,
      Unit: unit ?? undefined,
      Title: '',
      Meta: account ? `user|${account.AccountId}|${account.DisplayName}` : 'user',
    }
    agent_chat.chatSubmit(client, request, { target: targetAgentId })
      .then(resp => {
        if (!resp) return
        // Proactively seed the running turn so isStreaming returns true
        // immediately, even before turn.started SSE arrives.  This is critical
        // for freshly created agents whose timeline SSE subscription may still
        // be establishing — without it, missed turn.started leaves the UI stuck
        // in a non-streaming state with no assistant envelope.
        if (resp.TurnActorId) {
          getTimelineManager().seedActiveTurn(targetAgentId, resp.TurnActorId)
        }
        if (tempId && resp.MessageId) {
          source.replaceUserMessageId(tempId, resp.MessageId)
        }
        if (resp.Idx != null && resp.Idx > 0 && resp.MessageId) {
          source.updateUserMessageIdx(resp.MessageId, resp.Idx)
        }
        // Backfill the assistant turn in case the live step stream dropped the
        // first events of the new turn (subscription race on fresh agent).
        getTimelineManager().reconcile(targetAgentId)
      })
      .catch(err => {
        setContextError(err instanceof Error ? err.message : 'Failed to send message')
      })
  }, [pendingFirstMessage, activeAgent, source])

  // Auto-close detail panel when selected frame is no longer in envelopes
  // or when switching conversations.
  useEffect(() => {
    if (!state.selectedFrame) return
    const frameStillVisible = source.envelopes.some(env =>
      env.frames.some(f => f.id === state.selectedFrame!.id)
    )
    if (!frameStillVisible) {
      dispatch({ type: 'SELECT_FRAME', frame: null })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source.envelopes, state.activeSessionId])

  // Sync right tabs with selectedFrame
  useEffect(() => {
    if (state.selectedFrame) {
      const tabId = `frame-${state.selectedFrame.id}`
      setRightTabs(prev => {
        if (prev.some(t => t.id === tabId)) return prev
        return [...prev, { id: tabId, type: 'frame' as const, label: frameTypeLabel(state.selectedFrame!, t), payload: state.selectedFrame }]
      })
      setActiveBrowserTabId(tabId)
    } else {
      setRightTabs(prev => {
        const next = prev.filter(t => t.type !== 'frame')
        if (next.length !== prev.length) {
          setActiveBrowserTabId(active => next.find(t => t.id === active) ? active : (next[next.length - 1]?.id ?? null))
        }
        return next
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.selectedFrame?.id])

  const handleOpenSettings = useCallback((category?: string) => {
    setSidebarMode('settings')
    setSettingsCategories(() => {
      if (category && validSettingCategoryIds.has(category)) {
        saveLastSettingsCategory(category)
        return [category]
      }
      const last = loadLastSettingsCategory()
      return [last]
    })
    // When the right panel is expanded (fullscreen), switch to the settings tab
    // instead of collapsing — same routing as other content modes.
    if (rightPanelExpandedRef.current) {
      setActiveBrowserTabId('mode:settings')
    }
  }, [])

  const [aboutOverlayOpen, setAboutOverlayOpen] = useState(false)
  const handleAboutAction = useCallback(() => setAboutOverlayOpen(true), [])
  const handleCloseAboutOverlay = useCallback(() => setAboutOverlayOpen(false), [])

  const handleEditMenuAction = useCallback((action: string) => {
    if (action === 'select-all') {
      if (document.activeElement && 'select' in document.activeElement) {
        (document.activeElement as HTMLInputElement).select?.()
      } else {
        window.getSelection()?.selectAllChildren(document.body)
      }
      return
    }
    if (['cut', 'copy', 'paste'].includes(action)) {
      try {
        document.execCommand(action)
      } catch {
        // ignore unsupported commands
      }
    }
  }, [])

  const handleViewMenuAction = useCallback((action: string) => {
    switch (action) {
      case 'toggle-sidebar': {
        sidebar.handleToggleSidebar()
        break
      }
      case 'toggle-right-panel': {
        setRightPanelOpen(prev => !prev)
        break
      }
      case 'toggle-mc': {
        handleContentModeChange('multiconsole')
        break
      }
      default: {
        if (INTERFACE_VIEW_MODES.has(action)) {
          handleContentModeChange(action as ContentMode)
        }
        break
      }
    }
  }, [sidebar.handleToggleSidebar, setRightPanelOpen, handleContentModeChange])

  const tutorialContext = useMemo<TutorialContext>(() => ({
    hasProjects: projects.filter(p => !p.System).length > 0,
    hasAgents: allAgents.filter(a => a.AgentKind !== 'coordinator').length > 0,
    // Installed-app gate for the basics tutorial's launcher steps. Derived
    // straight from the registry snapshot because `launcherApps` is declared
    // below this memo.
    hasApps: hasLaunchableApps(appRegistry.getAll()),
  }), [projects, allAgents])

  const handleHelpMenuAction = useCallback((action: string) => {
    if (action === 'tool-guide') {
      toolGuideManager.showToolGuide()
      return
    }
    // Agent-created dynamic tutorials carry the `tutorial-dynamic:` prefix;
    // resolve their steps from the store projection at click time.
    if (action.startsWith('tutorial-dynamic:')) {
      const spec = dynamicTutorialStore.get(action.slice('tutorial-dynamic:'.length))
      if (spec && spec.Steps.length > 0) {
        guideManager.replayGuide(spec.Steps, spec.Title)
      }
      return
    }
    if (action.startsWith('tutorial-')) {
      const categoryId = action.slice('tutorial-'.length)
      const cat = getTutorialCategory(categoryId)
      if (cat) {
        guideManager.replayGuide(cat.build(t, tutorialContext), t(cat.labelKey))
      }
    }
  }, [t, tutorialContext])

  const handleOpenMobileSync = useCallback(() => setMobileSyncOpen(true), [])
  const handleCloseMobileSync = useCallback(() => setMobileSyncOpen(false), [])

  // Subscribe to guide manager state.
  useEffect(() => {
    return guideManager.subscribe((s) => {
      setGuideState({ steps: s.steps, currentIndex: s.currentIndex, visible: s.visible, gateMet: s.gateMet, categoryLabel: s.categoryLabel })
    })
  }, [])

  // Onboarding tour completion/skip -> auto-open the full-screen tool guide.
  useEffect(() => {
    return guideManager.onTourFinished(() => {
      toolGuideManager.showToolGuide()
    })
  }, [])

  // Startup re-sync: load agent-created dynamic tutorials from the
  // interfacemanager actor into the in-memory projection. create/delete
  // events keep it current afterwards.
  useEffect(() => {
    let cancelled = false
    void fetchTutorialCatalog().then((tutorials) => {
      if (cancelled) return
      dynamicTutorialStore.clear()
      for (const spec of tutorials) dynamicTutorialStore.upsert(spec)
    })
    return () => { cancelled = true }
  }, [])

  // Agent-driven interface control: dispatch interface_manager_event to the
  // existing omnibox handlers so an agent invoking interfacemanager.control
  // produces the same UI effect as a user picking the option. (open_app_view
  // is handled separately below handleOpenPluginView.)
  useEffect(() => {
    return onInterfaceManagerEvent((event) => {
      switch (event.Action) {
        case 'set_view': {
          if (event.Mode && INTERFACE_VIEW_MODES.has(event.Mode)) {
            handleContentModeChange(event.Mode as ContentMode)
          }
          break
        }
        case 'open_settings': {
          handleOpenSettings(event.Category)
          break
        }
        case 'switch_project': {
          if (event.ProjectId) void handleProjectChange(event.ProjectId)
          break
        }
        case 'focus_agent': {
          if (!event.AgentId) break
          const agent = allAgentsRef.current.find(a => a.Id === event.AgentId || a.ActorId === event.AgentId)
          if (agent) void handleOpenAgentChat(agent)
          break
        }
        // Guide actions
        case 'show_guide': {
          if (event.Steps && event.Steps.length > 0) {
            // Agent-driven guide: flag it as external so progress is reported
            // into the interaction ring buffer for the coordinator.
            guideManager.showGuide(event.Steps, false, undefined, { source: 'external' })
          }
          break
        }
        case 'create_tutorial': {
          // Agent-created dynamic tutorial: upsert into the in-memory
          // projection (durable state lives in the interfacemanager actor).
          if (event.Tutorial) {
            dynamicTutorialStore.upsert(event.Tutorial)
            if (event.AutoPlay && event.Tutorial.Steps.length > 0) {
              guideManager.showGuide(event.Tutorial.Steps, false, event.Tutorial.Title, { source: 'external' })
            }
          }
          break
        }
        case 'delete_tutorial': {
          if (event.TutorialId) {
            dynamicTutorialStore.remove(event.TutorialId)
          }
          break
        }
        case 'hide_guide': {
          guideManager.hideGuide()
          break
        }
        case 'interact': {
          if (event.GuideId && event.Interaction) {
            // Interaction is validated server-side against the primitive set.
            executeInteraction(event.GuideId, event.Interaction as InteractionPrimitive, event.Text)
          }
          break
        }
        default:
          break
      }
    })
  }, [handleContentModeChange, handleOpenSettings, handleProjectChange, handleOpenAgentChat])

  const projectInfos: ProjectInfo[] = useMemo(() => {
    return projects.map((p) => ({ ProjectID: p.ProjectID, Name: p.Name, RootPath: p.RootPath, System: p.System }))
  }, [projects])

  const { actions: omniboxActions, recordUse: recordOmniboxUse } = useAppOmnibox({
    contentMode,
    activeProjectId: activeProject?.ProjectID ?? null,
    activeAgentId: activeAgent?.ActorId ?? null,
    recentCommands: recentOmniboxCommands,
    projects: projectInfos,
    agents: sidebarAgents,
    onContentModeChange: handleContentModeChange,
    onProjectChange: handleProjectChange,
    onOpenAgentChat: handleOpenAgentChat,
    onCreateProject: () => { setNewProjectDialogMode('create'); setShowNewProjectDialog(true) },
    onCreateAgent: handleOpenCreateAgent,
    onOpenSettings: handleOpenSettings,
    onRecentChange: setRecentOmniboxCommands,
    onExportHistory: () => {
      agentOps.exportHistory(source.envelopes, activeAgent?.Title || activeAgent?.DisplayName)
    },
    onImportHistory: () => {
      importFileInputRef.current?.click()
    },
    onDeleteAgent: handleDeleteAgent,
    onDeleteProject: (p) => handleCloseProject({ ProjectID: p.ProjectID, Name: p.Name, RootPath: p.RootPath ?? '', System: p.System }),
  })

  // Set when a right-panel browser tab was just opened programmatically.
  // BrowserView launches an independent instance (onOpenRightTab queues the
  // new tab + active change) and then self-closes the new-tab page in the
  // same synchronous block. That close must not be treated as an
  // active-tab close retreating into the trajectory (restoreEntry would
  // clobber the just-queued tab). The flag is consumed by the next close
  // and self-clears after the current tick.
  const browserOpenThenCloseRef = useRef(false)

  const handleCloseRightTab = useCallback((tabId: string) => {
    const suppressRetreat = browserOpenThenCloseRef.current
    browserOpenThenCloseRef.current = false
    const tab = rightTabs.find(t => t.id === tabId)
    if (!tab) return
    // Closing a plugin tab is recorded for this session (keep-open effect
    // must not re-open it on the next registry change) and persisted —
    // rightPluginTabs no longer contains it, so a restart keeps it closed.
    if (tab.type === 'plugin') sessionClosedPluginTabsRef.current.add(tabId)
    if (tab.type === 'frame') {
      dispatch({ type: 'SELECT_FRAME', frame: null })
    } else if (tab.type === 'browser') {
      const p = tab.payload as { sessionId?: string; kind?: BrowserKind }
      if (p?.sessionId) {
        void closeBrowserSession(p.sessionId)
        if (p.kind === 'independent') {
          void closeBrowserInstance(p.sessionId)
        }
      }
    } else if (tab.type === 'ssh-session') {
      const p = tab.payload as { sessionId?: string }
      if (p?.sessionId) {
        void sshmanagerClient.shellClose(client, { SessionId: p.sessionId }).catch(() => {})
      }
    } else if (tab.type === 'db-session') {
      const p = tab.payload as { profileId?: string }
      if (p?.profileId) {
        // Best-effort release of pooled connection handles for this profile.
        // Failure is non-fatal: the actor reaps idle handles after TTL anyway.
        void dbclientClient.close(client, { ProfileId: p.profileId }).catch(() => {})
      }
    }
    if (activeBrowserTabId === tabId && !suppressRetreat) {
      // Closing the active tab retreats into the back/forward trajectory:
      // restore the last recorded entry that still selects a live tab. The
      // closed tab is purged from the whole trajectory inside backPastTab.
      const entry = navHistory.backPastTab(tabId)
      if (entry) {
        historyNavigationRef.current = true
        setCursorRestoreKey(k => k + 1)
        restoreEntry(entry)
        return
      }
    } else {
      // Background close: purge the tab from the trajectory so
      // back/forward cannot resurrect its dead session.
      navHistory.removeTab(tabId)
    }
    // Functional updaters are required: a tab opened in the same tick
    // (queued before this close) must survive the removal instead of being
    // clobbered by a stale-closure snapshot.
    setRightTabs(prev => {
      const next = prev.filter(t => t.id !== tabId)
      setActiveBrowserTabId(active => next.find(t => t.id === active) ? active : (next[next.length - 1]?.id ?? null))
      return next
    })
  }, [rightTabs, activeBrowserTabId, navHistory, restoreEntry, dispatch])

  const handleOpenFile = useCallback((filePath: string, diffContent?: string, line?: number, lineEnd?: number, mode?: 'source' | 'diff', customTabId?: string, column?: number, columnEnd?: number, sourceProjectId?: string) => {
    const normalizedPath = filePath.replace(/\\/g, '/')
    const rootPath = activeProject?.RootPath?.replace(/\\/g, '/').replace(/\/$/, '')
    const relativePath = rootPath && normalizedPath.toLowerCase().startsWith(`${rootPath.toLowerCase()}/`)
      ? normalizedPath.slice(rootPath.length + 1)
      : normalizedPath
    const displayMode = mode ?? (diffContent ? 'diff' : 'source')
    if (!diffContent && isImageExt(normalizedPath)) {
      const tabId = `image-${relativePath}`
      setRightTabs(prev => {
        if (prev.some(t => t.id === tabId)) return prev
        return [...prev, {
          id: tabId,
          type: 'image' as const,
          label: relativePath,
          payload: { filePath: relativePath, projectId: sourceProjectId },
        }]
      })
      setActiveBrowserTabId(tabId)
      return
    }
    const tabId = customTabId ?? `file-${relativePath}`
    setRightTabs(prev => {
      const idx = prev.findIndex(t => t.id === tabId || (t.type === 'diff' && (t.payload as { filePath?: string })?.filePath === relativePath))
      const existing = idx >= 0 ? prev[idx] : null
      if (existing) {
        const prevPayload = (existing.payload ?? {}) as { filePath: string; diffContent?: string; line?: number; lineEnd?: number; mode?: 'source' | 'diff'; column?: number; columnEnd?: number; projectId?: string }
        const updated = {
          ...existing,
          id: tabId,
          type: 'file' as const,
          label: relativePath,
          // Bump line via a fresh object so consumers watching payload identity re-render.
          payload: { ...prevPayload, filePath: relativePath, diffContent: diffContent ?? prevPayload.diffContent, line, lineEnd, mode: displayMode, column, columnEnd, projectId: sourceProjectId ?? prevPayload.projectId },
        }
        const next = [...prev]
        next[idx] = updated
        return next
      }
      return [...prev, {
        id: tabId,
        type: 'file' as const,
        label: relativePath,
        payload: { filePath: relativePath, diffContent, line, lineEnd, mode: displayMode, column, columnEnd, projectId: sourceProjectId },
      }]
    })
    setActiveBrowserTabId(tabId)
    // Open the panel inline rather than waiting for the tab-count effect, so
    // first-click file opening feels instant.
    setRightPanelOpen(true)
  }, [activeProject])

  // Open an in-memory image (data URL, e.g. an ai-step screenshot) in the
  // right-panel image viewer. Tabs dedupe by a content hash of the source.
  const handleOpenImage = useCallback((src: string, label?: string) => {
    let hash = 0
    for (let i = 0; i < src.length; i++) hash = ((hash << 5) - hash + src.charCodeAt(i)) | 0
    const tabId = `image-view-${(hash >>> 0).toString(36)}`
    setRightTabs(prev => {
      if (prev.some(t => t.id === tabId)) return prev
      return [...prev, {
        id: tabId,
        type: 'image' as const,
        label: label || t('ai.step.image'),
        payload: { src },
      }]
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [t])

  // OS files dropped on unclaimed shell areas (message stream, git / topology
  // panes, tab chrome…) open read-only in the right panel. Claimed targets keep
  // their own semantics: the composer toolbar attaches to the message,
  // FileBrowser imports into the project, SshSessionView uploads.
  const handleOsDropOpenFiles = useCallback((e: React.DragEvent) => {
    if (!Array.from(e.dataTransfer.types).includes('Files')) return
    if (!dropIsUnclaimed(e.target, e.currentTarget)) return
    e.preventDefault()
    e.stopPropagation()
    // collectOsDroppedEntries captures the DataTransfer entries synchronously
    // before any await — they are invalid after event dispatch.
    void collectOsDroppedEntries(e.dataTransfer).then(async entries => {
      for (const entry of entries) {
        const kind = osDropOpenKind(entry.name, entry.size)
        if (kind === 'skip') continue
        let bytes: Uint8Array
        try {
          bytes = await entry.readBytes()
        } catch {
          continue
        }
        if (kind === 'image') {
          handleOpenImage(imageDataUrl(entry.name, bytes), entry.name)
          continue
        }
        if (bytesLookBinary(bytes)) continue
        const content = new TextDecoder().decode(bytes)
        const tabId = `osfile-${entry.name}`
        setRightTabs(prev => {
          if (prev.some(t => t.id === tabId)) return prev
          return [...prev, { id: tabId, type: 'source' as const, label: entry.name, payload: { name: entry.name, content } }]
        })
        setActiveBrowserTabId(tabId)
        setRightPanelOpen(true)
      }
    }).catch(() => {})
  }, [handleOpenImage])

  // Files-only dragover: internal tab / agent drags are untouched. preventDefault
  // keeps plain-browser dev mode from navigating to the dropped file (the Wails
  // runtime already does the same at document level on desktop).
  const handleOsDropDragOver = useCallback((e: React.DragEvent) => {
    if (!Array.from(e.dataTransfer.types).includes('Files')) return
    if (!dropIsUnclaimed(e.target, e.currentTarget)) return
    e.preventDefault()
  }, [])

  /* --- FileBrowser items dropped on a plugin iframe open read-only in the
   * right panel, with the exact same classification as OS files dropped on
   * unclaimed shell areas (image tab for images, source tab for text, skip
   * binary / oversized). The overlay in PluginTabView forwards the drag
   * payload as a window event because the drop target lives far from the
   * drag source. */
  const openProjectFilePreviewTab = useCallback(async (target: string, path: string, name: string, size: number) => {
    const kind = osDropOpenKind(name, size)
    if (kind === 'skip') return
    const resp = await projectClient.readBase64(client, { Path: path }, { target }).catch(() => null)
    if (!resp?.Content) return
    const bytes = base64ToBytes(resp.Content)
    if (kind === 'image') {
      handleOpenImage(imageDataUrl(name, bytes), name)
      return
    }
    if (bytesLookBinary(bytes)) return
    const content = new TextDecoder().decode(bytes)
    const tabId = `osfile-${target}-${path}`
    setRightTabs(prev => {
      if (prev.some(t => t.id === tabId)) return prev
      return [...prev, { id: tabId, type: 'source' as const, label: name, payload: { name, content } }]
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [handleOpenImage])

  const handleFilePreviewOpen = useCallback((detail: unknown) => {
    const payload = detail as { projectId?: string; path?: string; name?: string; isDir?: boolean } | null
    if (!payload || typeof payload.path !== 'string') return
    const target = payload.projectId || activeProject?.ProjectID
    if (!target) return
    const path = payload.path
    if (payload.isDir) {
      // Consistent with an OS folder drop: every contained file is opened.
      projectClient.list(client, { Path: path, Depth: -1, Detail: true }, { target })
        .then(async text => {
          const { entries } = parseListText(text)
          for (const ent of entries) {
            if (ent.IsDir) continue
            const rel = path === '.' ? ent.Name : `${path}/${ent.Name}`
            const name = ent.Name.includes('/') ? ent.Name.slice(ent.Name.lastIndexOf('/') + 1) : ent.Name
            await openProjectFilePreviewTab(target, rel, name, ent.Size)
          }
        })
        .catch(() => {})
      return
    }
    void openProjectFilePreviewTab(target, path, payload.name || path, 0)
  }, [activeProject, openProjectFilePreviewTab])

  useEffect(() => {
    const handler = (e: Event) => { handleFilePreviewOpen((e as CustomEvent).detail) }
    window.addEventListener('sporemind:file-preview-open', handler)
    return () => window.removeEventListener('sporemind:file-preview-open', handler)
  }, [handleFilePreviewOpen])

  const handleOpenBrowserTab = useCallback((url?: string) => {
    if (!inWails) return
    const sessionId = `global-${Date.now()}`
    const tabId = `browser-${sessionId}`
    const initialUrl = url || ''
    setRightTabs(prev => [...prev, {
      id: tabId,
      type: 'browser' as const,
      label: t('rightPanel.newTab'),
      payload: { sessionId, kind: 'global' as BrowserKind, url: initialUrl },
    }])
    setActiveBrowserTabId(tabId)
  }, [inWails])

  const handleSaveCreateDefaults = useCallback((scope: 'normal' | 'independent') => {
    setCreateScope(scope)
    const globalTabs = rightTabs
      .filter((t) => t.type === 'browser' && (t.payload as { kind?: BrowserKind }).kind === 'global')
      .map((t) => ({
        id: t.id,
        sessionId: (t.payload as { sessionId: string }).sessionId,
        kind: (t.payload as { kind: BrowserKind }).kind,
        url: (t.payload as { url: string }).url,
        label: t.label,
      }))
    saveAIShellLayoutState({
      sidebarVisible: isMobile ? false : state.sidebarVisible,
      sidebarWidth,
      rightPanelWidth,
      rightPanelOpen,
      contentMode,
      browserTabs: globalTabs.length > 0 ? globalTabs : undefined,
      activeBrowserTabId: rightTabs.some(
        (t) => t.id === activeBrowserTabId && t.type === 'browser' && (t.payload as { kind?: BrowserKind }).kind === 'global'
      )
        ? (activeBrowserTabId ?? undefined)
        : undefined,
      browserCreateScope: scope,
    }).catch(() => {})
  }, [state.sidebarVisible, sidebarWidth, rightPanelWidth, rightPanelOpen, contentMode, rightTabs, activeBrowserTabId])

  const handleOpenBrowserRightTab = useCallback((sessionId: string, kind: BrowserKind, url: string, label: string) => {
    if (!inWails) return
    const tabId = `browser-${sessionId}`
    setRightTabs(prev => {
      if (prev.some(t => t.id === tabId)) return prev
      const payload: Record<string, unknown> = { sessionId, kind, url }
      if (kind === 'independent') {
        payload.sessionEpoch = 0
        payload.appliedProxy = ''
      }
      return [...prev, {
        id: tabId,
        type: 'browser' as const,
        label: label || t('rightPanel.browser'),
        payload,
      }]
    })
    setActiveBrowserTabId(tabId)
    // BrowserView may self-close the new-tab page right after this in the
    // same tick; mark the open so that close takes the background path.
    browserOpenThenCloseRef.current = true
    queueMicrotask(() => { browserOpenThenCloseRef.current = false })
  }, [inWails])

  const handleOpenReviewTab = useCallback(() => {
    setRightTabs(prev => {
      if (prev.some(t => t.id === 'review')) return prev
      return [...prev, { id: 'review', type: 'review' as const, label: t('rightPanel.review'), payload: null }]
    })
    setActiveBrowserTabId('review')
  }, [])

  const handleOpenPluginView = useCallback((view: PluginView) => {
    const tabId = pluginTabId(view.pluginID, view.id)
    setRightTabs(prev => {
      if (prev.some(t => t.id === tabId)) return prev
      return [...prev, { id: tabId, type: 'plugin' as const, label: view.title, payload: { view } }]
    })
    setActiveBrowserTabId(tabId)
    setRightPanelOpen(true)
  }, [])

  // Settings → Plugins "open" button: resolve the app's primary view entrypoint
  // and open it in the right panel, mirroring the launcher tile (and the
  // agent-driven open_app_view path below).
  const handleOpenAppView = useCallback((app: AppEntry) => {
    const target = app.entrypoints.find(entry => entry.kind === 'view')
    if (!target) return
    handleOpenPluginView({ id: target.id, pluginID: app.id, title: target.title, route: target.route ?? '/', icon: app.icon, color: app.color })
  }, [handleOpenPluginView])

  // "+" menu → Apps: open the right-panel app picker (singleton tab). Lives
  // in the right panel so the user can launch an app without leaving the
  // conversation surface.
  const handleOpenAppListTab = useCallback(() => {
    setRightTabs(prev => {
      if (prev.some(t => t.id === APP_LIST_TAB_ID)) return prev
      return [...prev, { id: APP_LIST_TAB_ID, type: 'app-list' as const, label: t('rightPanel.apps'), payload: null }]
    })
    setActiveBrowserTabId(APP_LIST_TAB_ID)
    setRightPanelOpen(true)
  }, [t])

  // Picking an app from the picker: open its primary view (which activates the
  // new plugin tab) and retire the picker tab in the same batch, so the panel
  // auto-closes.
  const handleSelectAppFromList = useCallback((app: AppEntry) => {
    handleOpenAppView(app)
    setRightTabs(prev => prev.filter(t => t.id !== APP_LIST_TAB_ID))
  }, [handleOpenAppView])

  // Plugin tabs persist across restarts like SSH/DB tabs. Boot phase (first
  // registry snapshot after session restore): reopen exactly the plugin
  // tabs the layout recorded as open at shutdown — running apps whose views
  // were not open stay closed; a restart restores, it does not open every
  // registered plugin. Transition phase (rest of the session): an app that
  // becomes running — user/agent launch, install, crash recovery — keeps
  // the keep-open rule: one tab per `view` entrypoint, appended and
  // re-sorted against the persisted rightTabOrder so a late-arriving app
  // cannot scramble the user's drag order. Tabs the user closed this
  // session stay closed (sessionClosedPluginTabsRef). No focus steal —
  // active is only set when nothing else holds it.
  const appendPluginTabs = useCallback((additions: ReadonlyArray<{ id: string; label: string; view: PluginView }>) => {
    if (additions.length === 0) return
    setRightTabs(prev => {
      const prevIds = new Set(prev.map(t => t.id))
      const add = additions.filter(m => !prevIds.has(m.id))
      if (add.length === 0) return prev
      return reorderTabs(
        [...prev, ...add.map(m => ({ id: m.id, type: 'plugin' as const, label: m.label, payload: { view: m.view } }))],
        rightTabOrderRef.current,
      )
    })
    if (!isMobileRef.current) setRightPanelOpen(true)
    setActiveBrowserTabId(active => active ?? additions[0]!.id)
  }, [])

  useEffect(() => {
    if (!sessionRestored || !registryLoaded) return
    const existing = new Set(rightTabIdsRef.current.map(t => t.id))
    if (!pluginBootRestoreDoneRef.current) {
      pluginBootRestoreDoneRef.current = true
      seenRunningAppsRef.current = new Set(registeredApps.filter(a => a.state === 'running').map(a => a.id))
      // Legacy layouts never persisted rightPluginTabs; the persisted tab
      // ORDER has always been the open-tab id set, so fall back to its
      // plugin ids for a zero-disruption migration.
      const persisted = persistedPluginTabsRef.current
      const wanted = persisted !== null
        ? new Set(persisted.map(t => t.id))
        : new Set(rightTabOrderRef.current.filter(id => id.startsWith('plugin-view-')))
      appendPluginTabs(computeAutoOpenPluginTabs(registeredApps, existing, sessionClosedPluginTabsRef.current, wanted))
      return
    }
    const newlyRunning = registeredApps.filter(a => a.state === 'running' && !seenRunningAppsRef.current.has(a.id))
    seenRunningAppsRef.current = new Set(registeredApps.filter(a => a.state === 'running').map(a => a.id))
    appendPluginTabs(computeAutoOpenPluginTabs(newlyRunning, existing, sessionClosedPluginTabsRef.current))
  }, [registeredApps, sessionRestored, registryLoaded, appendPluginTabs])

  // Agent-driven open_app_view: resolve the app in the registry snapshot and
  // open its view entrypoint tab, mirroring the app panel tile click.
  useEffect(() => {
    return onInterfaceManagerEvent((event) => {
      if (event.Action !== 'open_app_view') return
      const appId = event.AppId
      if (!appId) return
      const app = registeredApps.find(a => a.id === appId)
      if (!app) return
      const views = app.entrypoints.filter(e => e.kind === 'view')
      const target = (event.ViewId && views.find(v => v.id === event.ViewId)) || views[0]
      if (!target) return
      handleOpenPluginView({ id: target.id, pluginID: appId, title: target.title, route: target.route ?? '/', icon: app.icon, color: app.color })
    })
  }, [registeredApps, handleOpenPluginView])

  // Agent-driven request_plugin_dom_snapshot: fire a snapshot request at the
  // mounted plugin iframe; the bridge port routes the reply to
  // pluginhost.plugin_dom_put. Fire-and-forget — the backend polls ~3s for
  // the result.
  useEffect(() => {
    return onInterfaceManagerEvent((event) => {
      if (event.Action !== 'request_plugin_dom_snapshot') return
      if (!event.AppId) return
      requestPluginDomSnapshot(event.AppId)
    })
  }, [])

  const handleOpenGlassTab = useCallback(() => {
    setRightTabs(prev => {
      if (prev.some(t => t.id === 'glass')) return prev
      return [...prev, { id: 'glass', type: 'glass' as const, label: 'Glass', payload: null }]
    })
    setActiveBrowserTabId('glass')
    setRightPanelOpen(true)
  }, [])

  const handleOpenPuppetTab = useCallback(() => {
    setRightTabs(prev => {
      if (prev.some(t => t.id === 'puppet')) return prev
      return [...prev, { id: 'puppet', type: 'puppet' as const, label: t('launcher.puppetEditor'), payload: null }]
    })
    setActiveBrowserTabId('puppet')
    setRightPanelOpen(true)
  }, [t])

  const handleOpenMonoCardInRightPanel = useCallback((cardId: string, label: string, projectId?: string | null) => {
    handleOpenCardTab(cardId, label, projectId !== undefined ? { projectId } : undefined)
  }, [handleOpenCardTab])

  // Resolve a wikiword click in the right panel: open the matching card tab
  // if the card exists, otherwise open (or replace) a single draft tab so the
  // user can create the card inline. Mirrors the story river's resolve-or-draft
  // behavior but routes into right-panel tabs instead of the story river.
  // Draft tabs use a `draft-` prefix (matching isDraftId in mono-store); the
  // singleton invariant is enforced by stripping all `draft-` tabs before
  // adding a fresh one. A timestamped id makes the React key change per draft
  // so the editor remounts with fresh state for the new wikiword.
  const handleOpenCardOrDraftInRightPanel = useCallback(async (word: string, originProjectId?: string | null) => {
    // Wikiwords inside a foreign-project card tab resolve in that card's own
    // project first; only unresolved words fall through to the draft path.
    if (originProjectId && originProjectId !== (activeProject?.ProjectID ?? null)) {
      const fetched = await monoStore.getCard(word, { projectId: originProjectId })
      if (fetched) {
        handleOpenCardTab(fetched.id, word, { projectId: originProjectId })
        return
      }
    }
    let cards = monoState.cards
    if (cards.length === 0 && activeProject?.ProjectID) {
      monoStore.setProjectId(activeProject.ProjectID)
      await monoStore.load()
      cards = monoStore.getState().cards
    }
    let resolvedId = resolveCardId(word, cards, t)
    if (!resolvedId) {
      // Fold-hidden cards are absent from the list; check the backend before
      // falling back to a draft tab.
      const fetched = await monoStore.getCard(word)
      if (fetched) resolvedId = fetched.id
    }
    if (resolvedId) {
      handleOpenCardTab(resolvedId, word)
      return
    }
    const draftId = `draft-${Date.now()}`
    setRightTabs(prev => {
      const withoutDraft = prev.filter(t => !t.id.startsWith('draft-'))
      return [...withoutDraft, { id: draftId, type: 'mono-card-draft' as const, label: `draft · ${word}`, payload: { word } }]
    })
    setActiveBrowserTabId(draftId)
    setRightPanelOpen(true)
  }, [monoState.cards, t, handleOpenCardTab, activeProject?.ProjectID])

  // Goal-mode badge click → open the same target as the turn-tail goal row:
  // the goal's bound task card in the right panel, or a read-only goal text
  // tab. The badge dispatches 'sporemind:open-goal' with the composer agent's
  // ActorId (see registerModeClickActions); only the active agent's composer
  // can have goal mode mounted, so foreign actor IDs are ignored.
  useEffect(() => {
    const handler = (e: Event) => {
      const actorId = (e as CustomEvent<string | undefined>).detail
      if (actorId && actorId !== activeAgentActorId) return
      const goal = activeGoalRef.current
      if (!goal) return
      const cardId = goal.BoundTaskCardId?.trim()
      if (cardId) {
        void handleOpenCardOrDraftInRightPanel(cardId)
      } else {
        handleOpenTextTab(t('ai.turn.goal'), goal.InterpretedGoal?.trim() || goal.Condition)
      }
    }
    window.addEventListener('sporemind:open-goal', handler)
    return () => window.removeEventListener('sporemind:open-goal', handler)
  }, [activeAgentActorId, handleOpenCardOrDraftInRightPanel, handleOpenTextTab, t])

  // Topology node click: agent nodes use the SAME navigation as the sidebar
  // avatar bar — call navigateToAgent directly with switchToConversation:false
  // so we stay in topology mode while the floating composer picks up the new
  // session. Non-agent nodes open the card in the right panel — except for
  // "gate" / approval-pending cards, which open the shared schema input
  // modal so the user can supply the args the workflow is blocked on
  // (see [[schema-overlay-input-modal]] for the three-entry-point contract).
  const handleTopologyNodeClick = useCallback((cardId: string, label: string) => {
    if (cardId.startsWith('agent:')) {
      const rawId = cardId.slice('agent:'.length)
      // The raw id from card.data.agentId (= backend AgentRef.ID) may not
      // directly match AgentListItem.Id or ActorId. Resolve the real agent
      // via the snapshot using the same multi-fallback strategy as
      // TopologyGraph.findStoreAgent before calling navigateToAgent.
      const snap = agentInfoSnapshot
      const agent = snap.byId.get(rawId)
        ?? snap.byActorId.get(rawId)
        ?? snap.items.find(a => agentCardId(a.Id) === cardId)
        ?? snap.byId.get(rawId.replace(/-/g, '#'))
        ?? snap.byActorId.get(rawId.replace(/-/g, '#'))
      if (agent) {
        void navigateToAgent(agent.ProjectId, agent.Id, { switchToConversation: false })
        return
      }
      // Last resort: pass the raw id directly and let navigateToAgent try.
      const projectId = activeProjectRef.current?.ProjectID
      if (projectId) {
        void navigateToAgent(projectId, rawId, { switchToConversation: false })
      }
    } else {
      const card = monoState.cards.find(c => c.id === cardId)
      if (card && tryOpenSchemaOverlayForCard(card)) return
      handleOpenMonoCardInRightPanel(cardId, label)
    }
  }, [navigateToAgent, handleOpenMonoCardInRightPanel, agentInfoSnapshot, monoState.cards])

  // Actor topology node click: navigate to the agent and open its conversation.
  // Non-agent nodes are ignored.
  const handleActorNodeClick = useCallback((_nodeId: string, _label: string, node: UnifiedGraphNode) => {
    if (node.ActorType === 'agent' && node.ActorId) {
      const projectId = activeProjectRef.current?.ProjectID
      if (projectId) {
        void navigateToAgent(projectId, node.ActorId, { switchToConversation: true })
      }
    }
  }, [navigateToAgent])

  // Actor topology right-click: open context menu for agent nodes
  const handleActorNodeContext = useCallback((_nodeId: string, _label: string, node: UnifiedGraphNode, x: number, y: number) => {
    if (node.ActorType === 'agent' && node.ActorId) {
      const agent = agentInfoSnapshot.byId.get(node.ActorId) ?? agentInfoSnapshot.byActorId.get(node.ActorId)
      if (agent) {
        setAgentMenu({ agent, x, y, context: 'topology' })
      }
    }
  }, [agentInfoSnapshot])

  // Card topology right-click:
  // - agent node -> existing agent context menu
  // - other card node -> card-specific context menu
  // - edge -> edge menu (open connected cards / delete connection)
  // - blank area / tag node -> create menu
  const handleTopologyContextMenu = useCallback((event: { clientX: number; clientY: number; canvasX: number; canvasY: number; nodeId?: string; edge?: TopologyEdgeRef }) => {
    const { clientX, clientY, canvasX, canvasY, nodeId, edge } = event
    if (nodeId) {
      const card = monoState.cards.find(c => c.id === nodeId)
      if (card) {
        if (card.type === 'agent' || card.data?.componentKind === 'agent') {
          const rawAgentId = typeof card.data?.agentId === 'string' ? card.data.agentId : undefined
          const agent = rawAgentId
            ? (agentInfoSnapshot.byId.get(rawAgentId) ?? agentInfoSnapshot.byActorId.get(rawAgentId))
            : agentInfoSnapshot.items.find(a => agentCardId(a.Id) === card.id)
          if (agent) {
            setAgentMenu({ agent, x: clientX, y: clientY, context: 'topology' })
            return
          }
        } else {
          setCardMenuContext('topology')
          setCardMenu({ card, x: clientX, y: clientY })
          return
        }
      }
    }
    if (edge) {
      setEdgeMenu({ edge, x: clientX, y: clientY })
      return
    }
    setTopologyMenu({ x: clientX, y: clientY, canvasX, canvasY })
    topologyPlacementRef.current = { x: canvasX, y: canvasY }
  }, [monoState.cards, agentInfoSnapshot])

  // Workflow graph card right-click: show the floating card menu (workflow
  // context hides topology-specific items) and, when the card is bound to an
  // agent (map owner / task worker), switch the composer's session to that agent.
  const handleWorkflowCardMenu = useCallback((cardId: string, x: number, y: number) => {
    const card = monoState.cards.find(c => c.id === cardId)
    if (!card) return
    // Scheduler cards get their own context menu (toggle, run now, edit, delete, locate).
    if (card.type === 'scheduler') {
      setSchedulerGraphMenu({ card, x, y })
      return
    }
    setCardMenuContext('workflow')
    setCardMenu({ card, x, y })
    const ownerAgentId = card.type === 'workflow' ? workflowOwnerAgentId(card) : undefined
    const agent = ownerAgentId
      ? (agentInfoSnapshot.byActorId.get(ownerAgentId) ?? agentInfoSnapshot.byId.get(ownerAgentId))
      : agentInfoSnapshot.items.find(a => (a.BoundTaskCardId ?? a.Runtime?.BoundTaskCardId) === cardId)
    if (agent) handleSelectProjectAgent(agent.Id)
  }, [monoState.cards, agentInfoSnapshot, handleSelectProjectAgent])

  const handleWorkflowBlankMenu = useCallback((x: number, y: number) => {
    setWorkflowBlankMenu({ x, y })
  }, [])

  /** Mirror of `jsonSchemaForValue` in useToastActionEvents — kept local
   *  to avoid a cross-file import. Walks a value to derive a JSONSchema
   *  that mirrors its runtime shape (e.g. arrays become typed arrays). */
  function jsonSchemaForValue(value: unknown): JSONSchema {
    if (value === null || value === undefined) return {}
    if (typeof value === 'string') return { type: 'string' }
    if (typeof value === 'boolean') return { type: 'boolean' }
    if (typeof value === 'number') {
      return Number.isInteger(value) ? { type: 'integer' } : { type: 'number' }
    }
    if (Array.isArray(value)) {
      return { type: 'array', items: jsonSchemaForValue(value[0]) }
    }
    if (typeof value === 'object') {
      return { type: 'object', additionalProperties: true }
    }
    return {}
  }

  // ── Schema overlay entry points (workflow card + AI step) ─────────────
  //
  // Three entry points share the schema input modal (see
  // [[schema-overlay-input-modal]]):
  //
  //   1. toast action (handled inside useToastActionEvents).
  //   2. workflow card click (this file: handleTopologyNodeClick).
  //   3. AI step click (this file: handleFrameSelect / ToolCallBlock).
  //
  // The helpers below pick which cards / steps are "schema-modal worthy"
  // and build the JSONSchema the modal renders. Today the only schema-bearing
  // workflow cards are toolcall tasks whose data.exec.callable points at a
  // known callable; the JSONSchema is derived from CallableInterface.Params
  // (shallow) or from ActionSchemaID (deep, when present). Future gate cards
  // can plug in by adding a new branch here.
  const tryOpenSchemaOverlayForCard = useCallback((card: MonoCardListItem): boolean => {
    const data = (card.data ?? {}) as Record<string, unknown>
    const exec = data.exec as Record<string, unknown> | undefined
    const callable = typeof exec?.callable === 'string' ? exec.callable.trim() : ''
    const status = (card.status ?? '').toLowerCase()
    const isApproval = status === 'pending_review' || status === 'awaiting_approval' || status === 'blocked' || status === 'gate'
    const isGateKind = exec?.kind === 'gate' || data.requiresApproval === true
    if (!callable) return false
    if (!isApproval && !isGateKind) return false
    // Prefer the deep schema if ActionSchemaID is present in the card data;
    // otherwise derive a flat schema from the live CallableInterface (looked
    // up via list_callables) or, in the worst case, from the card's static
    // args shape.
    const deepSchema = typeof exec?.schemaId === 'number'
      ? jsonSchemaFromSchemaId(exec.schemaId)
      : undefined
    const args = (exec?.args as Record<string, unknown> | undefined) ?? {}
    const draft = Object.keys(args).length > 0 ? args : undefined
    const fallback: JSONSchema = Object.keys(args).length > 0
      ? {
          type: 'object',
          properties: Object.fromEntries(
            Object.entries(args).map(([k, v]) => [k, jsonSchemaForValue(v)]),
          ),
          additionalProperties: true,
        }
      : { type: 'object', additionalProperties: true }
    const cardTitle = (card as MonoCardListItem & { title?: string }).title
      ?? (card.data?.title as string | undefined)
      ?? card.id
    schemaOverlay.open({
      callableId: callable,
      jsonSchema: deepSchema ?? fallback,
      ...(draft ? { draft } : {}),
      title: cardTitle,
      description: t('schemaOverlay.approvalCardTitle'),
      draftScope: `card:${card.id}:${callable}`,
    })
    return true
  }, [schemaOverlay, t])

  /** Open the schema input modal for an AI step that requires the user to
   *  supply or confirm the call's args. Today the only step type the
   *  schema modal handles is a ToolFrame with a `forcedApproval` payload
   *  (set by the turn engine's forced-dispatch branch — see
   *  [[schema-overlay-input-modal]] for the entry-point contract). The
   *  existing inline-approval frames (PermissionRequest, AskUserQuestion,
   *  Plan/Goal Review) keep their own UI; this helper is purely additive. */
  const tryOpenSchemaOverlayForFrame = useCallback((frame: Frame): boolean => {
    if (frame.type !== 'tool') return false
    const forced = frame.forcedApproval
    if (!forced) return false
    const targetCallable = forced.targetCallable || frame.callableId || frame.toolName
    if (!targetCallable) return false
    // Schema resolution priority: (1) explicit schemaId, (2) the frame's
    // parsed input as a draft, (3) a free-form object schema.
    const deepSchema = typeof forced.schemaId === 'number'
      ? jsonSchemaFromSchemaId(forced.schemaId)
      : undefined
    let draft: Record<string, unknown> | undefined
    if (forced.draftArgs && Object.keys(forced.draftArgs).length > 0) {
      draft = forced.draftArgs
    } else if (frame.input) {
      try {
        const parsed = JSON.parse(frame.input) as unknown
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
          draft = parsed as Record<string, unknown>
        }
      } catch {
        // Frame input isn't valid JSON; fall through to the free-form schema.
      }
    }
    const fallback: JSONSchema = { type: 'object', additionalProperties: true }
    schemaOverlay.open({
      callableId: targetCallable,
      jsonSchema: deepSchema ?? fallback,
      ...(draft ? { draft } : {}),
      title: frame.toolName,
      ...(forced.note ? { description: forced.note } : {}),
      draftScope: `frame:${frame.id}:${targetCallable}`,
    })
    return true
  }, [schemaOverlay])

  // ── Scheduler graph context menu handlers ──────────────────────────────
  const handleOpenScheduleModal = useCallback(async (cardId: string) => {
    const card = monoState.cards.find(c => c.id === cardId) ?? await monoStore.getCard(cardId)
    if (!card) return
    const schedule = (card.data as Record<string, unknown> | undefined)?.schedule as Record<string, unknown> | undefined
    const cron = typeof schedule?.cron === 'string' ? schedule.cron : ''
    const expression = typeof schedule?.expression === 'string' ? schedule.expression : ''
    setScheduleEdit({ card, draft: cronToDraft(cron, expression), saving: false, error: '' })
  }, [monoState.cards])

  const handleApplySchedule = useCallback(async (agent?: SchedulerAgentSelection) => {
    if (!scheduleEdit) return
    const cron = draftToCron(scheduleEdit.draft)
    if (!cron) {
      setScheduleEdit(prev => prev ? { ...prev, error: t('scheduled.edit.invalid') } : prev)
      return
    }
    setScheduleEdit(prev => prev ? { ...prev, saving: true, error: '' } : prev)
    try {
      const card = await monoStore.getCard(scheduleEdit.card.id)
      if (!card) {
        setScheduleEdit(prev => prev ? { ...prev, saving: false, error: t('scheduled.edit.saveFailed') } : prev)
        return
      }
      const schedule = { ...((card.data as Record<string, unknown> | undefined)?.schedule as Record<string, unknown> | undefined ?? {}) }
      schedule.cron = cron
      // expression and cron are mutually exclusive per card validation.
      delete schedule.expression
      const data = { ...(card.data ?? {}) } as Record<string, unknown>
      data.schedule = schedule
      // Agent selection: create-new writes agent_kind + model_slots (JSON
      // object with a primary key) and clears any stale bound-agent
      // reference; unset leaves the card's executor fields untouched.
      if (agent?.mode === 'create') {
        data.agent_kind = agent.kind
        const slot = agent.selection ? selectionToSlot(agent.selection) : undefined
        if (slot) data.model_slots = JSON.stringify({ primary: slot })
        else delete data.model_slots
        delete data.bound_agent
      }
      const updated = await monoStore.updateCard(card.id, { data })
      if (updated) {
        setScheduleEdit(null)
      } else {
        setScheduleEdit(prev => prev ? { ...prev, saving: false, error: t('scheduled.edit.saveFailed') } : prev)
      }
    } catch {
      setScheduleEdit(prev => prev ? { ...prev, saving: false, error: t('scheduled.edit.saveFailed') } : prev)
    }
  }, [scheduleEdit, t])

  const handleCloseSchedulerGraphMenu = useCallback(() => {
    setSchedulerGraphMenu(null)
  }, [])

  const handleSchedulerToggle = useCallback(async (card: MonoCardListItem) => {
    setSchedulerGraphMenu(null)
    const projectId = activeProject?.ProjectID
    if (!projectId) return
    const schedule = (card.data as Record<string, unknown> | undefined)?.schedule as Record<string, unknown> | undefined
    const currentEnabled = schedule?.enabled !== false
    try {
      // The backend wikiToggleTimer callable routes to the project actor cell.
      await projectClient.wikiToggleTimer(client, { Id: card.id, Enabled: !currentEnabled }, { target: projectId })
    } catch {
      // ignore
    }
  }, [activeProject?.ProjectID])

  const handleSchedulerRunNow = useCallback(async (card: MonoCardListItem) => {
    setSchedulerGraphMenu(null)
    try {
      await monoStore.triggerTimerCard(card.id)
    } catch {
      // ignore
    }
  }, [])

  const handleSchedulerEditSchedule = useCallback((card: MonoCardListItem) => {
    setSchedulerGraphMenu(null)
    void handleOpenScheduleModal(card.id)
  }, [handleOpenScheduleModal])

  const handleSchedulerLocateTemplate = useCallback((card: MonoCardListItem) => {
    setSchedulerGraphMenu(null)
    const templateId = (card.data as Record<string, unknown> | undefined)?.workflow_template
    if (typeof templateId === 'string' && templateId) {
      openWorkflowAndLocate(templateId)
    }
  }, [])

  const handleSchedulerDelete = useCallback(async (card: MonoCardListItem) => {
    setSchedulerGraphMenu(null)
    // Count instance workflows that reference this scheduler card.
    const instanceCount = monoState.cards.filter(
      c => c.type === 'workflow' && (c.data as Record<string, unknown> | undefined)?.scheduler_card_id === card.id
    ).length
    setDeleteConfirmScheduler({ card, instanceCount })
  }, [monoState.cards])

  const handleConfirmDeleteScheduler = useCallback(async () => {
    if (!deleteConfirmScheduler) return
    try {
      await monoStore.deleteCard(deleteConfirmScheduler.card.id)
      setDeleteConfirmScheduler(null)
    } catch (err) {
      setContextError(err instanceof Error ? err.message : 'Failed to delete scheduler card')
      setDeleteConfirmScheduler(null)
    }
  }, [deleteConfirmScheduler])

  const handleCloseCardMenu = useCallback(() => {
    setCardMenu(null)
  }, [])

  const handleCloseEdgeMenu = useCallback(() => {
    setEdgeMenu(null)
  }, [])

  const handleEdgeOpenCards = useCallback((edge: TopologyEdgeRef) => {
    const openable = [edge.from, edge.to].filter(id => monoState.cards.some(c => c.id === id))
    for (const id of openable) {
      handleOpenMonoCardInRightPanel(id, id)
    }
    const last = openable[openable.length - 1]
    if (last) {
      setActiveBrowserTabId(`card-${last}`)
      setRightPanelOpen(true)
    }
  }, [monoState.cards, handleOpenMonoCardInRightPanel])

  const handleEdgeDelete = useCallback(async (edge: TopologyEdgeRef) => {
    const fromCard = monoState.cards.find(c => c.id === edge.from)
    const toCard = monoState.cards.find(c => c.id === edge.to)
    try {
      if (edge.kind === 'parent') {
        // Parent edges can be declared from either side: the child's `parent`
        // field and/or the parent's `list`. Clear whichever side declares it.
        if (toCard?.parent === edge.from) {
          await monoStore.updateCard(toCard.id, { parent: '' })
        }
        if (fromCard?.list?.includes(edge.to)) {
          await monoStore.updateCard(fromCard.id, { list: fromCard.list.filter(id => id !== edge.to) })
        }
      } else if (edge.kind === 'tag') {
        // Tag edges run from the referenced card to the card carrying the tag.
        if (fromCard && toCard) {
          const tag = toCard.tags.find(cardTag => tagMatchesCard(cardTag, fromCard))
          if (tag) await monoStore.updateCard(toCard.id, { tags: toCard.tags.filter(cardTag => cardTag !== tag) })
        }
      } else if (edge.kind === 'depends_on') {
        // Rendered as prerequisite -> dependent; remove the prerequisite from
        // the dependent card's data.depends_on list.
        if (toCard) {
          const deps = dependsOnIds(toCard)
          const next = deps.filter(id => id !== edge.from)
          if (next.length !== deps.length) {
            await monoStore.updateCard(toCard.id, { data: { ...(toCard.data ?? {}), depends_on: next } })
          }
        }
      }
    } catch {
      // ignore
    }
  }, [monoState.cards])

  // --- Mini Composer (floating chat anchored to a card / menu position) ---
  // The MiniComposer owns its own conversation with the project's coder agent;
  // the shell only controls open/anchor/initial-seed.
  const handleCloseMiniComposer = useCallback(() => {
    setMiniComposer(prev => ({ ...prev, open: false }))
  }, [])

  // Anchor the mini composer to the context-menu target rect, seeded with a
  // reference to the card so the user can ask about it inline.
  const handleCardMenuChat = useCallback((card: MonoCardListItem) => {
    const a: MiniComposerAnchor | null = cardMenu
      ? { x: cardMenu.x, y: cardMenu.y, width: 0, height: 0 }
      : null
    setMiniComposer({ open: true, anchor: a, initialValue: `[[${card.id}]] ` })
  }, [cardMenu])

  // Open the mini composer from a card tab, anchoring to the right panel.
  const handleOpenMonoCardChat = useCallback((_cardId: string) => {
    const el = document.querySelector('.ai-right-panel') as HTMLElement | null
    const anchor: MiniComposerAnchor | null = el
      ? (() => { const r = el.getBoundingClientRect(); return { x: r.right - 380, y: r.bottom - 120, width: 360, height: 0 } })()
      : null
    setMiniComposer({ open: true, anchor, initialValue: '' })
  }, [])

  const handleCardOpen = useCallback((card: MonoCardListItem) => {
    handleOpenMonoCardInRightPanel(card.id, card.id)
    setActiveBrowserTabId(`card-${card.id}`)
    setRightPanelOpen(true)
  }, [handleOpenMonoCardInRightPanel])

  const handleCardRename = useCallback((card: MonoCardListItem) => {
    setRenameCard(card)
    setRenameCardValue(card.id)
  }, [])

  const handleCardAddTag = useCallback((card: MonoCardListItem) => {
    setTagCard(card)
    setTagCardValue('')
  }, [])

  const handleCardAddChild = useCallback((card: MonoCardListItem) => {
    setChildCard(card)
    setChildCardValue(t('topologyContextMenu.newCard'))
  }, [t])

  const handleConfirmAddChild = useCallback(async () => {
    if (!childCard || !childCardValue.trim()) return
    try {
      await monoStore.createCard({
        name: childCardValue.trim(),
        body: '',
        tags: [...(childCard.tags ?? []), childCard.id],
        parent: childCard.id,
      })
    } finally {
      setChildCard(null)
      setChildCardValue('')
    }
  }, [childCard, childCardValue])

  const handleCardSetVisual = useCallback((card: MonoCardListItem) => setVisualCard(card), [])
  const handleSaveVisual = useCallback(async (data: Record<string, unknown>) => {
    if (!visualCard) return
    await monoStore.updateCard(visualCard.id, { data: { ...(visualCard.data ?? {}), ...(data.visual ? { visual: data.visual } : {}) } })
    setVisualCard(null)
  }, [visualCard])

  const handleCardDuplicate = useCallback(async (card: MonoCardListItem) => {
    try {
      const full = await monoStore.getCard(card.id)
      if (!full) return
      await monoStore.createCard({
        name: `${card.id} (copy)`,
        body: full.body,
        tags: [...card.tags],
        list: [...card.list],
        data: full.data ? { ...full.data } : undefined,
      })
    } catch {
      // ignore
    }
  }, [])

  const handleCardCopyPath = useCallback((card: MonoCardListItem) => {
    void navigator.clipboard.writeText(card.id)
  }, [])

  const handleCardSaveAsTemplate = useCallback(async (card: MonoCardListItem) => {
    try {
      await monoStore.saveWorkflowTemplate(card.id)
    } catch {
      // ignore — consistent with other card menu handlers
    }
  }, [])

  const handleCardLocateTemplate = useCallback((card: MonoCardListItem) => {
    const tplId = card.data?.instance_of
    if (typeof tplId !== 'string' || !tplId) return
    requestWorkflowLocate({ mapId: tplId, selectStart: true })
  }, [])

  // P1/U1 — manually instantiate a workflow template into a new runnable
  // instance map, then locate/zoom to it (same mechanism as
  // handleCardLocateTemplate) and refresh the card list.
  const handleCardInstantiateFromTemplate = useCallback(async (card: MonoCardListItem) => {
    setCardMenuRuns(undefined)
    handleCloseCardMenu()
    try {
      const instance = await monoStore.instantiateWorkflow(card.id)
      if (!instance) return
      requestWorkflowLocate({ mapId: instance.id, selectStart: true })
      await monoStore.load()
    } catch {
      // ignore — consistent with other card menu handlers
    }
  }, [handleCloseCardMenu])

  // P1 follow-up — create a scheduler card bound to this template (daily
  // 09:00 default), then open it for editing so the user can set cron and
  // executor before it first fires. The Scheduled view picks it up via the
  // scheduler service (backend auto-registers on card create).
  const handleCardCreateScheduledTask = useCallback(async (card: MonoCardListItem) => {
    handleCloseCardMenu()
    try {
      const created = await monoStore.createSchedulerCard(card.id)
      if (created) handleOpenCardForEdit(created.id)
    } catch {
      // ignore — consistent with other card menu handlers
    }
  }, [handleCloseCardMenu, handleOpenCardForEdit])

  // N1 — template → instance list: fetch the runs and show them as
  // sub-items of the context menu (menu stays open while loading).
  const handleCardViewInstances = useCallback((card: MonoCardListItem) => {
    setCardMenuRuns(undefined)
    monoStore.listTemplateRuns(card.id, 50)
      .then(runs => setCardMenuRuns(runs.map(r => r.InstanceMapId)))
      .catch(() => setCardMenuRuns([]))
  }, [])

  const handleOpenInstanceMap = useCallback((instanceMapId: string) => {
    requestWorkflowLocate({ mapId: instanceMapId, selectStart: true })
  }, [])

  // N5 — instance → scheduler: locate the scheduler card that spawned this
  // instance map (data.scheduler_card_id is landed by the backend line; the
  // menu item hides itself until the field is present).
  const handleCardLocateScheduler = useCallback((card: MonoCardListItem) => {
    const schedulerId = card.data?.scheduler_card_id
    if (typeof schedulerId !== 'string' || !schedulerId) return
    requestWorkflowLocate({ mapId: schedulerId, selectStart: true })
  }, [])

  // U2 — run a scheduler card immediately from the topology context menu.
  const handleCardRunNow = useCallback(async (card: MonoCardListItem) => {
    try {
      await monoStore.triggerTimerCard(card.id)
    } catch {
      // ignore — consistent with other card menu handlers
    }
  }, [])

  // N3 — scheduler → current instance: zoom-to-fit a workflow map id from
  // the scheduler card detail panel.
  const handleNavigateToMap = useCallback((mapId: string) => {
    requestWorkflowLocate({ mapId, selectStart: true })
  }, [])

  const handleCardMenuClose = useCallback(() => {
    setCardMenuRuns(undefined)
    handleCloseCardMenu()
  }, [handleCloseCardMenu])

  const handleCardDelete = useCallback((card: MonoCardListItem) => {
    const taskIds = card.type === 'workflow' ? workflowTaskIds(card, monoState.cards) : []
    setDeleteConfirmCard({ card, taskIds })
  }, [monoState.cards])

  const handleCardAssignGoal = useCallback((card: MonoCardListItem) => {
    // The assigned agent derives its goal from the task card body itself; the
    // dialog only picks the target agent.
    setAssignGoalError(null)
    setAssignGoalSubmitting(false)
    setAssignGoalCard(card)
  }, [])

  const handleCloseAssignGoal = useCallback(() => {
    if (assignGoalSubmitting) return
    setAssignGoalCard(null)
    setAssignGoalError(null)
  }, [assignGoalSubmitting])

  const handleAssignGoal = useCallback(async (input: AssignGoalInput) => {
    if (!assignGoalCard) return
    const projectId = activeProject?.ProjectID ?? undefined
    setAssignGoalSubmitting(true)
    setAssignGoalError(null)
    try {
      if (input.mode === 'new') {
        await workspace.agentSpawnAssign(client, {
          To: input.displayName,
          AgentKind: input.agentKind,
          BoundTaskCardId: assignGoalCard.id,
          ProjectId: projectId,
        })
      } else {
        await workspace.agentAssign(client, {
          AgentActorId: input.agentActorId,
          BoundTaskCardId: assignGoalCard.id,
          ProjectId: projectId,
        })
      }
      // The backend flips the card to "doing" (and, for spawn, emits an
      // agent_list_state event); reload the card list so the topology and the
      // detail panel reflect the new state.
      void monoStore.load()
      setAssignGoalCard(null)
    } catch (err) {
      setAssignGoalError(err instanceof Error ? err.message : String(err))
    } finally {
      setAssignGoalSubmitting(false)
    }
  }, [assignGoalCard, activeProject?.ProjectID])

  // ── Workflow start (to-start status button on the graph) ─────────────
  // Bound owner agent → mount + activate + kick off directly. No owner →
  // open the agent pick/create dialog; on confirm the chosen/created agent
  // is mounted and started on the map.

  const startWorkflowOnAgent = useCallback(async (agentActorId: string, mapId: string) => {
    await workspace.workflowStart(client, {
      AgentActorId: agentActorId,
      MapCardId: mapId,
      ProjectId: activeProject?.ProjectID ?? undefined,
    })
    // Starting a workflow on a background agent gives no immediate feedback
    // otherwise: the owner's first turn is on another conversation and the
    // background watcher only fires on error/interaction/completion. Play the
    // attention cue so the kick-off is audible (respects the notify config;
    // skipped when the user stayed on the owner — the timeline shows it).
    if (agentActorId !== activeAgentIdRef.current) {
      loadNotifyConfig().then(cfg => playInteractionSound(cfg)).catch(() => {})
    }
    // Activation flips the map owner; reload cards so the graph reflects the
    // new state immediately (agent list updates arrive via events).
    void monoStore.load()
  }, [activeProject?.ProjectID])

  const handleStartWorkflow = useCallback((mapId: string) => {
    const mapCard = monoState.cards.find(c => c.id === mapId)
    if (!mapCard) return
    const ownerId = workflowOwnerAgentId(mapCard)
    const owner = ownerId
      ? agentInfoSnapshot.byActorId.get(ownerId) ?? agentInfoSnapshot.byId.get(ownerId)
      : undefined
    if (ownerId && owner) {
      startWorkflowOnAgent(owner.ActorId || owner.Id, mapId)
        .catch(err => setContextError(err instanceof Error ? err.message : String(err)))
      return
    }
    setStartWorkflowError(null)
    setStartWorkflowSubmitting(false)
    setStartWorkflowMapId(mapId)
  }, [monoState.cards, agentInfoSnapshot, startWorkflowOnAgent])

  const handleCloseStartWorkflow = useCallback(() => {
    if (startWorkflowSubmitting) return
    setStartWorkflowMapId(null)
    setStartWorkflowError(null)
  }, [startWorkflowSubmitting])

  const handleStartWorkflowAssign = useCallback(async (input: AssignGoalInput) => {
    if (!startWorkflowMapId) return
    const projectId = activeProject?.ProjectID
    setStartWorkflowSubmitting(true)
    setStartWorkflowError(null)
    try {
      let agentActorId = input.agentActorId
      if (input.mode === 'new') {
        const agent = await agentOps.createAgent(
          { displayName: input.displayName, agentKind: input.agentKind, projectId },
          { agentKinds, aggregators },
        )
        if (!agent) throw new Error(agentOps.state.error ?? 'Failed to create agent')
        agentActorId = agent.ActorId || agent.Id
      }
      if (!agentActorId) throw new Error('Select an agent')
      await startWorkflowOnAgent(agentActorId, startWorkflowMapId)
      setStartWorkflowMapId(null)
    } catch (err) {
      setStartWorkflowError(err instanceof Error ? err.message : String(err))
    } finally {
      setStartWorkflowSubmitting(false)
    }
  }, [startWorkflowMapId, activeProject?.ProjectID, agentOps, agentKinds, aggregators, startWorkflowOnAgent])

  const handleCardFilterSubtree = useCallback((card: MonoCardListItem) => {
    const id = topoFiltersStore.add('subtree', card.id)
    setActiveTopologyFilterIds(prev => new Set(prev).add(id))
  }, [])

  const handleCardFilterLineage = useCallback((card: MonoCardListItem) => {
    const id = topoFiltersStore.add('lineage', card.id)
    setActiveTopologyFilterIds(prev => new Set(prev).add(id))
  }, [])

  const handleConfirmRenameCard = useCallback(async () => {
    if (!renameCard) return
    const name = renameCardValue.trim()
    if (!name) return
    try {
      await monoStore.updateCard(renameCard.id, { id: name })
      setRenameCard(null)
      setRenameCardValue('')
    } catch {
      // ignore
    }
  }, [renameCard, renameCardValue])

  const handleCancelRenameCard = useCallback(() => {
    setRenameCard(null)
    setRenameCardValue('')
  }, [])

  const handleConfirmAddTagCard = useCallback(async () => {
    if (!tagCard) return
    const tag = tagCardValue.trim()
    if (!tag) return
    try {
      await monoStore.addCardTag(tagCard.id, tag)
      setTagCard(null)
      setTagCardValue('')
    } catch {
      // ignore
    }
  }, [tagCard, tagCardValue])

  const handleCancelAddTagCard = useCallback(() => {
    setTagCard(null)
    setTagCardValue('')
  }, [])

  const handleConfirmDeleteCard = useCallback(async () => {
    if (!deleteConfirmCard) return
    try {
      for (const taskId of deleteConfirmCard.taskIds) {
        await monoStore.deleteCard(taskId)
      }
      await monoStore.deleteCard(deleteConfirmCard.card.id)
      setDeleteConfirmCard(null)
    } catch (err) {
      setContextError(err instanceof Error ? err.message : 'Failed to delete card')
      setDeleteConfirmCard(null)
    }
  }, [deleteConfirmCard])

  const handleConfirmBacklogIntercept = useCallback(async () => {
    const name = backlogIntercept
    if (!name) return
    try {
      const card = await monoStore.createCard({ name, body: '', type: 'task', tags: [], status: 'backlog' })
      handleOpenCardTab(card.id, card.id)
      dispatch({ type: 'SEND_MESSAGE' })
    } catch (err) {
      setContextError(err instanceof Error ? err.message : t('topologyContextMenu.createFailed'))
    } finally {
      setBacklogIntercept(null)
    }
  }, [backlogIntercept, handleOpenCardTab, t])

  const handleCancelBacklogIntercept = useCallback(() => {
    setBacklogIntercept(null)
  }, [])

  const handleCloseTopologyMenu = useCallback(() => {
    setTopologyMenu(null)
  }, [])

  const handleTopologyCreateAgent = useCallback((target: TopologyMenuTarget) => {
    const projectId = activeProject?.ProjectID ?? projects.find(p => p.IsOpen && !p.System)?.ProjectID ?? projects.find(p => !p.System)?.ProjectID
    if (!projectId) return
    creatingAgentFromTopologyRef.current = true
    topologyPlacementRef.current = { x: target.canvasX, y: target.canvasY }
    handleOpenCreateAgent(projectId)
    setTopologyMenu(null)
  }, [activeProject, projects, handleOpenCreateAgent])

  const handleTopologyCreateProject = useCallback((target: TopologyMenuTarget) => {
    creatingProjectFromTopologyRef.current = true
    topologyPlacementRef.current = { x: target.canvasX, y: target.canvasY }
    setNewProjectDialogMode('create')
    setShowNewProjectDialog(true)
    setTopologyMenu(null)
  }, [])

  const openNameDialog = useCallback((kind: 'card' | 'backlog' | 'skill' | 'prompt', target: TopologyMenuTarget) => {
    topologyPlacementRef.current = { x: target.canvasX, y: target.canvasY }
    setCreateNameValue('')
    setCreateNameDialog({ kind, open: true })
    setTopologyMenu(null)
  }, [])

  const handleTopologyCreateCard = useCallback((target: TopologyMenuTarget) => {
    openNameDialog('card', target)
  }, [openNameDialog])

  const handleTopologyCreateBacklog = useCallback((target: TopologyMenuTarget) => {
    openNameDialog('backlog', target)
  }, [openNameDialog])

  const handleTopologyCreateSkill = useCallback((target: TopologyMenuTarget) => {
    openNameDialog('skill', target)
  }, [openNameDialog])

  const handleTopologyCreatePrompt = useCallback((target: TopologyMenuTarget) => {
    openNameDialog('prompt', target)
  }, [openNameDialog])

  const handlePlacementDone = useCallback(() => {
    setPendingPlacement(null)
  }, [])

  const handleConfirmCreateName = useCallback(async () => {
    const dialog = createNameDialog
    if (!dialog?.open) return
    const name = createNameValue.trim()
    if (!name) return
    const projectId = activeProject?.ProjectID
    if (!projectId) {
      setContextError(t('topologyContextMenu.noProject'))
      setCreateNameDialog(null)
      return
    }
    const pos = topologyPlacementRef.current
    try {
      let cardId: string | undefined
      if (dialog.kind === 'card') {
        const card = await monoStore.createCard({ name, body: '', tags: [] })
        cardId = card.id
      } else if (dialog.kind === 'backlog') {
        const card = await monoStore.createCard({ name, body: '', tags: [], status: 'backlog' })
        cardId = card.id
      } else if (dialog.kind === 'skill') {
        const card = await monoStore.createCard({ name, body: '', tags: ['component', 'skill'], data: { componentKind: 'skill' } })
        cardId = card.id
      } else if (dialog.kind === 'prompt') {
        const card = await monoStore.createCard({ name, body: '', tags: ['component', 'prompt'], data: { componentKind: 'prompt' } })
        cardId = card.id
      }
      if (cardId && pos) {
        setPendingPlacement({ id: cardId, x: pos.x, y: pos.y })
      }
    } catch (err) {
      setContextError(err instanceof Error ? err.message : t('topologyContextMenu.createFailed'))
    } finally {
      setCreateNameDialog(null)
      setCreateNameValue('')
    }
  }, [createNameDialog, createNameValue, activeProject, t])

  const handleCancelCreateName = useCallback(() => {
    setCreateNameDialog(null)
    setCreateNameValue('')
  }, [])

  const handleConnectTopologyNodes = useCallback(async (sourceCardId: string, targetCardId: string) => {
    await monoStore.addCardTag(sourceCardId, targetCardId)
  }, [])

  // "Open in knowledge base" from a right-panel card tab: switch to notes mode
  // and open the card's swimlane lane (carrying the owning project).
  const handleOpenMonoCardInNotes = useCallback((cardId: string, projectId?: string | null) => {
    setContentMode('notes')
    requestKbLane(projectId ?? null, cardId)
  }, [requestKbLane])

  // Save composer text to a new card: the knowledge base no longer hosts a
  // draft editor, so this opens the right-panel draft tab pre-filled with the
  // selection (same route as the resolve-or-draft wikiword path).
  const handleSaveTextToCard = useCallback((content: string) => {
    const draftId = `draft-${Date.now()}`
    setRightTabs(prev => [
      ...prev.filter(t => !t.id.startsWith('draft-')),
      { id: draftId, type: 'mono-card-draft' as const, label: `draft · ${t('storyRiver.placeholder.untitled')}`, payload: { word: t('storyRiver.placeholder.untitled'), initialBody: content } },
    ])
    setActiveBrowserTabId(draftId)
    setRightPanelOpen(true)
  }, [t])

  // Right-click provider-button slot menu. The composer renders ProviderSlotMenu
  // and owns its target/close state; we supply the five agent model slots, reuse
  // the composer's provider options (grouped + flat), and hand back the persist
  // callback. Omitted when no agent is active, so the provider button stays
  // left-click-only there.
  const slotMenu = useMemo<ComposerSlotMenuConfig | undefined>(() => {
    if (!activeAgent?.Id) return undefined
    return {
      slots: {
        Primary: activeAgent.Primary ?? autoSlot(),
        Fast: activeAgent.Fast ?? autoSlot(),
        Execution: activeAgent.Execution ?? autoSlot(),
        Review: activeAgent.Review ?? autoSlot(),
        Summary: activeAgent.Summary ?? autoSlot(),
      },
      groups: providerContext.groups,
      options: providerContext.providers,
      onSelectSlot: handleSelectSlot,
    }
  }, [activeAgent, providerContext.groups, providerContext.providers, handleSelectSlot])

  const wrappedProviderContext = useMemo(() => ({
    ...providerContext,
    slotMenu,
    onProviderChange: handleProviderChange,
    onSelectRoute: handleSelectRoute,
    onSelectUnit: handleSelectUnit,
    onOpenFile: handleOpenFile,
    onOpenImage: handleOpenImage,
    onOpenCard: (cardId: string) => { void handleOpenCardOrDraftInRightPanel(cardId) },
    onOpenCardForEdit: handleOpenCardForEdit,
    onOpenText: handleOpenTextTab,
  }), [providerContext, slotMenu, handleProviderChange, handleSelectRoute, handleSelectUnit, handleOpenFile, handleOpenImage, handleOpenCardOrDraftInRightPanel, handleOpenCardForEdit, handleOpenTextTab])

  const handleSwitchRightTab = useCallback((tabId: string) => {
    setActiveBrowserTabId(tabId)
    if (tabId === 'mode:settings') {
      // Settings is routed via sidebarMode, not contentMode.
      setSidebarMode('settings')
    } else if (tabId.startsWith('mode:')) {
      const mode = tabId.slice(5) as ContentMode
      setContentMode(mode)
      setSidebarMode('normal')
    }
    // Reset browser bg color when switching away from a browser tab
    setBrowserBgColor('')
  }, [])

  const handleReorderTabs = useCallback((fromId: string, toIndex: number) => {
    setRightTabs(prev => {
      const fromIdx = prev.findIndex(t => t.id === fromId)
      if (fromIdx < 0) return prev
      const next = [...prev]
      const moved = next.splice(fromIdx, 1)[0]
      if (!moved) return prev
      next.splice(resolveTabInsertIndex(fromIdx, toIndex, next.length), 0, moved)
      return next
    })
  }, [])

  const handleToggleExpand = useCallback(() => {
    if (rightPanelExpanded) {
      // Shrinking: switch away from content-mode tab to first regular tab
      if (activeBrowserTabId?.startsWith('mode:')) {
        const firstRegular = rightTabsRef.current[0]
        setActiveBrowserTabId(firstRegular?.id ?? null)
      }
    } else {
      // Expanding: keep the currently selected regular tab if any;
      // only auto-select the content-mode tab when there's no active regular tab.
      const hasRegularTab = activeBrowserTabId && !activeBrowserTabId.startsWith('mode:')
      if (!hasRegularTab) {
        const currentModeTab = sidebarModeRef.current === 'settings' ? 'mode:settings' : `mode:${contentMode}`
        setActiveBrowserTabId(currentModeTab)
      }
    }
    setRightPanelExpanded(prev => !prev)
  }, [rightPanelExpanded, contentMode, activeBrowserTabId])

  // App-mode sidebar data: built in SidebarLauncher.buildLauncherApps (shared
  // with the removed right-panel app panel).
  const launcherApps = useMemo(() => buildLauncherApps(registeredApps, t, inWails, !wailsProduction), [registeredApps, t, inWails])

  const handleLauncherToggleFavorite = useCallback((id: string, favorite: boolean) => {
    setLauncherFavorites(prev => (favorite ? (prev.includes(id) ? prev : [...prev, id]) : prev.filter(fid => fid !== id)))
  }, [])

  const handleLauncherMoveToTop = useCallback((id: string) => {
    // Move the tile to the front of every section it currently appears in
    // (收藏 / 最近 / 列表), mirroring the per-section "move to top" menu item.
    setLauncherFavorites(prev => (prev.includes(id) ? [id, ...prev.filter(fid => fid !== id)] : prev))
    setLauncherRecent(prev => (prev.includes(id) ? [id, ...prev.filter(rid => rid !== id)] : prev))
    setLauncherOrder(prev => (prev.includes(id) ? [id, ...prev.filter(oid => oid !== id)] : prev))
  }, [])

  const handleLauncherRemoveRecent = useCallback((id: string) => {
    setLauncherRecent(prev => prev.filter(rid => rid !== id))
  }, [])

  const handleLauncherReorder = useCallback((fromId: string, toIndex: number, section: LauncherSection) => {
    if (section === 'favorites') {
      setLauncherFavorites(prev => reorderIdList(prev, fromId, toIndex))
    } else if (section === 'all') {
      setLauncherOrder(prev => reorderIdList(prev, fromId, toIndex))
    }
    // section 'recent' is launch-ordered (most recent first); manual drag
    // reordering inside it is ignored.
  }, [])

  const handleLauncherManageApps = useCallback(() => {
    handleOpenSettings('plugins')
  }, [handleOpenSettings])

  // Developer-tooling entries are visible for experiment subscribers; using
  // them requires developer mode, otherwise a prompt offers to enable it in
  // the developer settings section.
  const requireDeveloperMode = useCallback((action: () => void) => {
    if (developerMode) {
      action()
      return
    }
    setDevModePromptOpen(true)
  }, [developerMode])

  const handleFileMenuAction = useCallback((action: string) => {
    switch (action) {
      case 'new-project': {
        setNewProjectDialogMode('create')
        setShowNewProjectDialog(true)
        break
      }
      case 'open-omnibox': {
        setOmniboxPosition('top')
        setOmniboxOpen(true)
        break
      }
      case 'settings': {
        handleOpenSettings()
        break
      }
      default:
        break
    }
  }, [handleOpenSettings])

  const handleLauncherLaunch = useCallback((app: LauncherApp) => {
    // Track recency: unshift the launched id (deduped), capped at 6.
    setLauncherRecent(prev => pushRecent(prev, app.id, 6))
    switch (app.action.kind) {
      case 'builtin':
        switch (app.action.builtin) {
          case 'review': handleOpenReviewTab(); break
          case 'glass': handleOpenGlassTab(); break
          case 'puppet': handleOpenPuppetTab(); break
          case 'install-local':
            requireDeveloperMode(() => { void localInstall.selectAndPreviewZip(client) })
            break
          case 'browser': handleOpenBrowserTab(); break
        }
        break
      case 'plugin': {
        const action = app.action
        handleOpenPluginView({ id: action.entryID, pluginID: action.pluginID, title: action.title, route: action.route, icon: registeredApps.find(a => a.id === action.pluginID)?.icon, color: registeredApps.find(a => a.id === action.pluginID)?.color })
        break
      }
    }
  }, [handleOpenReviewTab, handleOpenGlassTab, handleOpenPuppetTab, handleOpenBrowserTab, handleOpenPluginView, localInstall.selectAndPreviewZip, requireDeveloperMode, registeredApps])

  // Zip drop on the launcher panel: same local-install preview flow as the
  // install-local toolbar action, gated on developer mode the same way.
  const handleLauncherZipDrop = useCallback(
    (file: File) => {
      requireDeveloperMode(() => { void localInstall.handleDroppedZip(file) })
    },
    [localInstall.handleDroppedZip, requireDeveloperMode],
  )

  const [browserSnapshot, setBrowserSnapshot] = useState<string | null>(null)
  const [browserBgColor, setBrowserBgColor] = useState<string>('')

  // ── Content-mode tab definitions (shown in the right panel tab bar when expanded) ──
  const contentModeTabs = useMemo<Array<{ id: ContentMode; label: string; icon: React.ReactNode }>>(() => [
    { id: 'conversation', label: t('shell.sidebar.conversationMode'), icon: <MessageCircle size={14} /> },
    { id: 'topology', label: t('shell.sidebar.topologyMode'), icon: <Network size={14} /> },
    { id: 'workflow', label: t('shell.sidebar.workflowMode'), icon: <Waypoints size={14} /> },
    { id: 'multiconsole', label: t('shell.sidebar.multiConsoleMode'), icon: <LayoutGrid size={14} /> },
    { id: 'files', label: t('shell.sidebar.filesMode'), icon: <Folder size={14} /> },
    { id: 'ssh', label: t('shell.sidebar.sshMode', { defaultValue: 'SSH' }), icon: <Terminal size={14} /> },
    { id: 'problems', label: t('shell.contentMode.problems'), icon: <AlertTriangle size={14} /> },
    { id: 'approvals', label: t('shell.sidebar.approvalsMode', { defaultValue: 'Approvals' }), icon: <ShieldCheck size={14} /> },
    { id: 'aistats', label: t('shell.contentMode.modelStats'), icon: <Gauge size={14} /> },
    { id: 'git', label: t('shell.sidebar.gitMode', { defaultValue: 'Git' }), icon: <GitBranch size={14} /> },
  ], [t])

  // Ref-based content body renderer to avoid stale closures in rightTabItems useMemo.
  // The ref is updated every render, so tab render() calls always use fresh values.
  const renderContentBodyRef = useRef<(mode: ContentMode) => React.ReactNode>(() => null)

  // Keep a ref of rightPanelExpanded for handlers that must not be invalidated by it
  const rightPanelExpandedRef = useRef(rightPanelExpanded)
  rightPanelExpandedRef.current = rightPanelExpanded

  const rightTabItems: RightTabItem[] = useMemo(() => {
    // Prepend content-mode tabs when the right panel is expanded
    const modeTabs: RightTabItem[] = rightPanelExpanded
      ? [
          ...contentModeTabs.map(m => ({
            id: `mode:${m.id}`,
            label: m.label,
            icon: m.icon,
            closable: false,
            render: () => (
              <div className="rpt-content-mode">
                {renderContentBodyRef.current(m.id)}
              </div>
            ),
          })),
          // Settings follows the same routing as other content modes: in
          // fullscreen it lives behind its own mode tab.
          {
            id: 'mode:settings',
            label: t('shell.sidebar.settings'),
            icon: <Settings size={14} />,
            closable: false,
            render: () => (
              <div className="rpt-content-mode">
                {renderContentBodyRef.current('settings' as ContentMode)}
              </div>
            ),
          },
        ]
      : []
    // When two open file/image tabs share a relative path but belong to
    // different projects, prefix the project name so each tab is visually
    // distinct (the stored label is project-relative only).
    const disambigLabel = (() => {
      const counts = new Map<string, Set<string>>()
      for (const t of visibleRightTabs) {
        if (t.type !== 'file' && t.type !== 'diff' && t.type !== 'image') continue
        const pl = (t.payload ?? {}) as { filePath?: string; projectId?: string | null }
        if (!pl.filePath) continue
        let s = counts.get(pl.filePath)
        if (!s) { s = new Set<string>(); counts.set(pl.filePath, s) }
        if (pl.projectId) s.add(pl.projectId)
      }
      return (tab: typeof visibleRightTabs[number]) => {
        const pl = (tab.payload ?? {}) as { filePath?: string; projectId?: string | null }
        const rp = pl.filePath
        if (!rp) return tab.label
        const owners = counts.get(rp)
        if (owners && owners.size > 1) {
          const name = projects.find(p => p.ProjectID === pl.projectId)?.Name
          if (name) return `${name} · ${rp}`
        }
        return tab.label
      }
    })()
    return [
      ...modeTabs,
      ...visibleRightTabs.map((tab): RightTabItem => {
      if (tab.type === 'frame') {
        const frame = tab.payload as typeof state.selectedFrame
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          render: () => (
            <div className="ai-right-panel-content">
              <div className="ai-right-panel-scroll">
                {frame ? <FrameRenderer frame={frame} version={1} turnStreaming={false} /> : null}
              </div>
            </div>
          ),
        }
      }
      if (tab.type === 'browser') {
        const p = tab.payload as {
          sessionId: string
          kind: BrowserKind
          url: string
          favicon?: string
          faviconFailed?: boolean
          sessionEpoch?: number
        }
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          icon: p.kind === 'independent'
            ? <Globe size={14} className="rpt-tab-icon-independent" />
            : p.favicon && !p.faviconFailed
              ? <img
                  src={p.favicon}
                  alt=""
                  className="rpt-tab-favicon"
                  onError={() => setRightTabs(prev => prev.map(t =>
                    t.id === tab.id ? { ...t, payload: { ...(t.payload as Record<string, unknown>), faviconFailed: true } } : t
                  ))}
                />
              : <Globe size={14} />,
          renderKey: p.sessionEpoch != null ? `${tab.id}-epoch-${p.sessionEpoch}` : tab.id,
          render: () => (
            <div className="ai-right-panel-content">
              <BrowserView
                sessionId={p.sessionId}
                kind={p.kind}
                initialUrl={p.url}
                snapshot={tab.id === activeBrowserTabId ? browserSnapshot : null}
                onUrlChange={(url) => setRightTabs(prev => prev.map(t =>
                  t.id === tab.id ? { ...t, payload: { ...(t.payload as Record<string, unknown>), url } } : t
                ))}
                onTitleChange={(title) => setRightTabs(prev => prev.map(t =>
                  t.id === tab.id ? { ...t, label: title || t.label, payload: { ...(t.payload as Record<string, unknown>), title } } : t
                ))}
                onFaviconChange={(favicon) => setRightTabs(prev => prev.map(t => {
                  if (t.id !== tab.id) return t
                  const p = t.payload as Record<string, unknown>
                  // Only retry a failed favicon when its value actually changed
                  // (new page); the backend periodically re-sends the same URL.
                  const faviconFailed = p.favicon !== favicon ? false : p.faviconFailed
                  return { ...t, payload: { ...p, favicon, faviconFailed } }
                }))}
                onBgColorChange={tab.id === activeBrowserTabId ? setBrowserBgColor : undefined}
                onOpenRightTab={handleOpenBrowserRightTab}
                onOpenBrowserTab={handleOpenBrowserTab}
                onSaveCreateDefaults={handleSaveCreateDefaults}
                defaultCreateScope={createScope}
                onCloseSelf={() => handleCloseRightTab(tab.id)}
              />
            </div>
          ),
        }
      }
      if (tab.type === 'review') {
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          render: () => (
            <div className="ai-right-panel-content">
              <ReviewPanel />
            </div>
          ),
        }
      }
      if (tab.type === 'glass') {
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          icon: <MonitorSmartphone size={14} />,
          render: () => (
            <div className="ai-right-panel-content">
              <GlassDebugPanel />
            </div>
          ),
        }
      }
      if (tab.type === 'puppet') {
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          icon: <Layers size={14} />,
          render: () => (
            <div className="ai-right-panel-content">
              <PuppetEditorPanel />
            </div>
          ),
        }
      }
      if (tab.type === 'plugin') {
        const payload = tab.payload as { view?: PluginView } | null
        if (payload?.view) {
          const view = payload.view
          return {
            id: tab.id,
            label: tab.label,
            closable: true,
            // Keep the iframe mounted (hidden) across tab switches: the app
            // frontend holds its own state and may have requests in flight —
            // unmounting would destroy the document and reload on return.
            keepAlive: true,
            icon: resolveAppIcon(view.icon, view.pluginID, 14, view.color),
            render: () => (
              <div className="ai-right-panel-content">
                <PluginTabView
                  pluginID={view.pluginID}
                  viewID={view.id}
                  route={view.route}
                  title={view.title}
                  active={tab.id === activeBrowserTabId && rightPanelOpen}
                />
              </div>
            ),
          }
        }
        // Legacy null-payload plugin tabs (the removed management panel) have
        // nothing to render; the tab can simply be closed by the user.
        return { id: tab.id, label: tab.label, closable: true, render: () => null }
      }
      if (tab.type === 'app-list') {
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          icon: <LayoutGrid size={14} />,
          render: () => (
            <div className="ai-right-panel-content">
              <AppListPanel apps={registeredApps} onSelect={handleSelectAppFromList} />
            </div>
          ),
        }
      }
      if (tab.type === 'agent-inspector') {
        const p = tab.payload as { agentId: string; actorId: string }
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          render: () => (
            <div className="ai-right-panel-content">
              <AgentInspectorPanel
                agentId={p.agentId}
                actorId={p.actorId}
                agentTitle={tab.label}
              />
            </div>
          ),
        }
      }
      if (tab.type === 'mono-card') {
        const p = tab.payload as { cardId: string; edit?: boolean; projectId?: string | null }
        // Resolve the tab's own project (the project the card was opened
        // from); legacy tabs without one fall back to the active project.
        const tabProjectId = p.projectId ?? activeProject?.ProjectID ?? null
        const tabProject = tabProjectId ? (projects ?? []).find(pr => pr.ProjectID === tabProjectId) : undefined
        const tabProjectRoot = tabProject?.RootPath ?? (tabProjectId === activeProject?.ProjectID ? activeProject?.RootPath : null) ?? null
        const tabLabel = tabProjectId && tabProjectId !== activeProject?.ProjectID && tabProject?.Name
          ? `${tabProject.Name} · ${tab.label}`
          : tab.label
        return {
          id: tab.id,
          label: tabLabel,
          closable: true,
          render: () => (
            <div className="ai-right-panel-content">
              <FileReferenceProvider projectId={tabProjectId} projectRoot={tabProjectRoot}>
                <MonoCardPanel cardId={p.cardId} projectId={tabProjectId} onOpenInNotes={handleOpenMonoCardInNotes} onCloseTab={(cardId) => handleCloseRightTab(`card-${cardId}`)} onChat={handleOpenMonoCardChat} onWikiWord={(word) => { void handleOpenCardOrDraftInRightPanel(word, tabProjectId) }} initialEditMode={p.edit} onAssignGoal={handleCardAssignGoal} onNavigateToMap={handleNavigateToMap} />
              </FileReferenceProvider>
            </div>
          ),
        }
      }
      if (tab.type === 'mono-card-draft') {
        const p = tab.payload as { word: string; initialBody?: string }
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          render: () => (
            <div className="ai-right-panel-content">
              <MonoCardDraftPanel
                draftTitle={p.word}
                initialBody={p.initialBody}
                projectId={activeProject?.ProjectID ?? null}
                onSaved={(cardId) => {
                  setRightTabs(prev => {
                    const withoutDraft = prev.filter(t => !t.id.startsWith('draft-'))
                    if (withoutDraft.some(t => t.id === `card-${cardId}`)) return withoutDraft
                    return [...withoutDraft, { id: `card-${cardId}`, type: 'mono-card' as const, label: cardId, payload: { cardId, projectId: activeProject?.ProjectID ?? null } }]
                  })
                  setActiveBrowserTabId(`card-${cardId}`)
                }}
                onCancel={() => handleCloseRightTab(tab.id)}
                onWikiWord={(word) => { void handleOpenCardOrDraftInRightPanel(word) }}
              />
            </div>
          ),
        }
      }
      if (tab.type === 'memory-node') {
        const p = tab.payload as { node: MemoryNode }
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          render: () => (
            <div className="ai-right-panel-content">
              <MemoryNodePanel node={p.node} onClose={() => handleCloseRightTab(tab.id)} />
            </div>
          ),
        }
      }
      if (tab.type === 'goal-text') {
        const p = tab.payload as { text: string }
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          render: () => (
            <div className="ai-right-panel-content">
              <div className="ai-goal-text-view">{p.text}</div>
            </div>
          ),
        }
      }
      if (tab.type === 'image') {
        const p = tab.payload as { projectId?: string | null; filePath?: string; src?: string }
        const tabProjectId = p.projectId ?? activeProject?.ProjectID
        return {
          id: tab.id,
          label: disambigLabel(tab),
          closable: true,
          render: () => (
            <div className="ai-right-panel-content">
              <ImageViewer
                filePath={p.filePath}
                src={p.src}
                projectRootPath={projects.find(pr => pr.ProjectID === tabProjectId)?.RootPath ?? activeProject?.RootPath}
              />
            </div>
          ),
        }
      }
      if (tab.type === 'source') {
        const p = tab.payload as { name: string; content: string }
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          icon: <FileCode size={14} />,
          render: () => (
            <div className="ai-right-panel-content">
              <CodeMirrorViewer content={p.content} filePath={p.name} />
            </div>
          ),
        }
      }
      if (tab.type === 'ssh-file') {
        const p = tab.payload as { sessionId: string; path: string }
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          keepAlive: true,
          icon: <FileCode size={14} />,
          render: () => (
            <div className="ai-right-panel-content">
              <SshRemoteFileEditor sessionId={p.sessionId} path={p.path} />
            </div>
          ),
        }
      }
      if (tab.type === 'ssh-session') {
        const p = tab.payload as { sessionId: string; hostId: string; hostName: string }
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          keepAlive: true,
          icon: <Terminal size={14} />,
          render: () => (
            <div className="ai-right-panel-content">
              <SshSessionView
                sessionId={p.sessionId}
                hostId={p.hostId}
                hostName={p.hostName}
                onSessionIdChange={(id) => handleSshSessionIdChange(tab.id, id)}
                onOpenRemoteFile={(entry) => handleOpenSshFile(p.sessionId, entry.FullPath)}
                isActive={tab.id === activeBrowserTabId && rightPanelOpen}
              />
            </div>
          ),
        }
      }
      if (tab.type === 'db-session') {
        const p = tab.payload as { profileId: string; profileName: string; backend: string }
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          keepAlive: true,
          icon: <Database size={14} />,
          render: () => (
            <div className="ai-right-panel-content">
              <ShellDbClientView
                profileId={p.profileId}
                profileName={p.profileName}
                backend={p.backend}
                isActive={tab.id === activeBrowserTabId && rightPanelOpen}
              />
            </div>
          ),
        }
      }
      if (tab.type === 'object-storage') {
        const p = tab.payload as { profileId: string; profileName: string; backend: string }
        return {
          id: tab.id,
          label: tab.label,
          closable: true,
          keepAlive: true,
          icon: <Cloud size={14} />,
          render: () => (
            <div className="ai-right-panel-content">
              <ObjectStorageSessionView
                profileId={p.profileId}
                profileName={p.profileName}
                backend={p.backend}
                isActive={tab.id === activeBrowserTabId && rightPanelOpen}
              />
            </div>
          ),
        }
      }
      const file = tab.payload as { projectId?: string | null; filePath: string; diffContent?: string; line?: number; lineEnd?: number; mode?: 'source' | 'diff'; viewMode?: string; column?: number; columnEnd?: number }
      const isFileActive = tab.id === activeBrowserTabId
      const tabProjectId = file.projectId ?? activeProject?.ProjectID
      const tabProjectRoot = projects.find(p => p.ProjectID === tabProjectId)?.RootPath ?? activeProject?.RootPath
      return {
        id: tab.id,
        label: disambigLabel(tab),
        closable: true,
        render: () => (
          <div className="ai-right-panel-content">
            <FileDiffViewer
              filePath={file.filePath}
              projectId={tabProjectId}
              diffContent={file.diffContent}
              defaultMode={(file.viewMode as 'source' | 'diff' | undefined) ?? file.mode ?? (file.diffContent ? 'diff' : 'source')}
              initialLine={file.line}
              initialLineEnd={file.lineEnd}
              initialColumn={file.column}
              initialColumnEnd={file.columnEnd}
              onCursorChange={handleCursorChange}
              onViewModeChange={(mode) => {
                setRightTabs(prev => prev.map(t =>
                  t.id === tab.id ? { ...t, payload: { ...(t.payload as Record<string, unknown>), viewMode: mode } } : t
                ))
              }}
              restoreCursorPos={isFileActive ? cursorPos : null}
              restoreSelectionFrom={isFileActive ? selectionFrom : null}
              restoreSelectionTo={isFileActive ? selectionTo : null}
              cursorRestoreKey={isFileActive ? cursorRestoreKey : 0}
              projectRootPath={tabProjectRoot}
              onGotoDefinition={(targetPath, targetLine) => handleOpenFile(targetPath, undefined, targetLine + 1)}
              workspaceProjectRoots={projects.filter(p => p.IsOpen && p.RootPath).map(p => p.RootPath)}
            />
          </div>
        ),
      }
    })
    ]
  }, [visibleRightTabs, rightPanelExpanded, contentModeTabs, activeBrowserTabId, browserSnapshot, handleOpenPluginView, handleOpenMonoCardInRightPanel, handleOpenCardOrDraftInRightPanel, handleOpenMonoCardInNotes, handleOpenBrowserRightTab, handleOpenBrowserTab, handleSaveCreateDefaults, createScope, handleCloseRightTab, handleOpenMemoryNodeTab, handleOpenFile, activeProject, projects, isMobile, t, handleCursorChange, cursorRestoreKey, registeredApps, handleSelectAppFromList])

  // Keep latest tab state in a ref so the single browser visibility effect
  // below does not need to depend on the rightTabs array reference.
  const rightTabsRef = useRef(rightTabs)
  rightTabsRef.current = rightTabs

  // All modals/dialogs/menus driven by AIShellLayout state register with the
  // unified browser overlay manager so native child windows are hidden while
  // they are open.
  const localOverlayOpen = !!(
    createAgentProjectId || editAgent || cloneAgent ||
    agentMenu || projectMenu || topologyMenu || cardMenu || renameProject ||
    deleteConfirmAgent || deleteConfirmCard || deleteConfirmScheduler || scheduleEdit || closeConfirmProject || clearConfirmAgent || childCard ||
    showNewProjectDialog || createNameDialog?.open || renameCard || tagCard || mobileSyncOpen ||
    assignGoalCard || backlogIntercept
  )
  useBrowserOverlay(localOverlayOpen)

  // Drag-reordering the right panel tabs counts as an overlay too.
  const [dragOverlayOpen, setDragOverlayOpen] = useState(false)
  const handleDragStartReorder = useCallback(() => setDragOverlayOpen(true), [])
  const handleDragEndReorder = useCallback(() => setDragOverlayOpen(false), [])
  useBrowserOverlay(dragOverlayOpen)

  const overlayOpen = useBrowserOverlayOpen()

  // Screenshot placeholder: when the first overlay opens, capture the active
  // browser session's content while its window is still visible; the frozen
  // image stands in until the last overlay closes and the window is restored.
  const activeBrowserTabIdRef = useRef(activeBrowserTabId)
  activeBrowserTabIdRef.current = activeBrowserTabId
  useBrowserPreHideCapture(async () => {
    if (!isWails()) return
    const active = rightTabsRef.current.find(t => t.id === activeBrowserTabIdRef.current && t.type === 'browser')
    if (!active) return
    const p = active.payload as { sessionId?: string }
    if (!p.sessionId) return
    try {
      const img = await captureBrowserWindow(p.sessionId)
      if (img) {
        // Decode + commit + paint the snapshot BEFORE returning: the manager
        // hides the native window only after this settles, so the swap
        // happens in one frame instead of flashing blank during decode.
        await decodeSnapshotImage(img)
        setBrowserSnapshot(img)
        await afterNextPaint()
      }
    } catch {
      // Capture failed: the window hides without a placeholder.
    }
  })
  useEffect(() => {
    if (overlayOpen) return
    // Keep the frozen image until the native window has actually been
    // re-shown (show is async IPC); clearing it in the same commit as the
    // overlay close would flash the empty frame beneath.
    const timer = setTimeout(() => setBrowserSnapshot(null), 250)
    return () => clearTimeout(timer)
  }, [overlayOpen])

  // Track when the right panel geometry is animating so the single browser
  // visibility effect can keep child windows hidden during the transition.
  const [resizing, setResizing] = useState(false)

  // Hide browser child windows during expand/collapse animation and emit the
  // legacy layout-changing/settled events that BrowserView still consumes.
  useEffect(() => {
    if (!isWails() || !rightPanelOpen) return
    setResizing(true)
    emitWailsEvent('browser:layout-changing')
    const timer = setTimeout(() => {
      setResizing(false)
      emitWailsEvent('browser:layout-settled')
    }, 240)
    return () => {
      clearTimeout(timer)
      setResizing(false)
      emitWailsEvent('browser:layout-settled')
    }
  }, [rightPanelExpanded, rightPanelOpen])

  // Single source of truth for right-panel browser child window visibility.
  // Computes shouldShow from the panel state, embedded mode, active tab, and
  // panel resize animation. While an overlay is open this effect stands down
  // completely: the BrowserOverlayManager owns visibility then and hides all
  // windows only AFTER the pre-hide snapshot is captured and painted. Writing
  // wanted=false here would win that race and blank the browser area for the
  // ~100-200ms the capture takes; writing wanted=true would un-hide a window
  // beneath the overlay.
  useEffect(() => {
    if (!isWails() || overlayOpen) return
    const activeTab = rightTabsRef.current.find(t => t.id === activeBrowserTabId)
    const activeSessionId = activeTab?.type === 'browser'
      ? (activeTab.payload as { sessionId?: string }).sessionId
      : undefined
    const shouldShow = rightPanelOpen && !embedded && !resizing
    rightTabsRef.current.forEach(t => {
      if (t.type !== 'browser') return
      const p = t.payload as { sessionId?: string }
      if (!p.sessionId) return
      const isActive = p.sessionId === activeSessionId
      void setBrowserWindowVisible(p.sessionId, shouldShow && isActive)
    })
  }, [rightPanelOpen, embedded, activeBrowserTabId, overlayOpen, resizing])

  const newTabOptions = useMemo<NewTabOption[]>(() => {
    const options: NewTabOption[] = [
      { id: 'apps', label: t('rightPanel.apps'), icon: <LayoutGrid size={14} />, onClick: handleOpenAppListTab },
      { id: 'review', label: t('rightPanel.review'), icon: <FileSearch size={14} />, onClick: handleOpenReviewTab },
    ]
    if (inWails) {
      options.unshift({ id: 'browser', label: t('rightPanel.browser'), icon: <Globe size={14} />, onClick: () => handleOpenBrowserTab() })
    }
    return options
  }, [handleOpenBrowserTab, handleOpenReviewTab, handleOpenAppListTab, inWails, t])

  // Mobile: auto-collapse sidebar when right panel opens
  const previousRightPanelOpenRef = useRef(rightPanelOpen)
  useEffect(() => {
    const wasOpen = previousRightPanelOpenRef.current
    previousRightPanelOpenRef.current = rightPanelOpen
    if (isMobile && rightPanelOpen && !wasOpen && sidebar.showSidebar) {
      dispatch({ type: 'SET_SIDEBAR_VISIBLE', value: false })
    }
  }, [isMobile, rightPanelOpen, sidebar.showSidebar])

  // Whether to show the message drawer above the composer.
  // Conversation mode: composer floats (drawer=false) in all layouts.
  // Other modes (notes, topology, files, ssh, problems): show drawer.
  // In the expanded right panel the conversation stream is only visible on the
  // mode:conversation tab; every other active tab (browser, app, settings, …)
  // hides it, so the composer must fall back to drawer mode there.
  const showDrawer = rightPanelExpanded
    ? activeBrowserTabId !== 'mode:conversation'
    : contentMode !== 'conversation'

  const showNewChatPage = !activeAgent && !pendingFirstMessage && (activeProject || (!contextLoading && userProjects.length === 0))

  // Coordinator landing page takes over the content area (with its own composer)
  // when the global coordinator is active and has no conversation history yet.
  const showCoordinatorHome = !!activeAgent
    && activeAgent.AgentKind === 'coordinator'
    && !activeAgent.ProjectId
    && !pendingFirstMessage
    && source.envelopes.length === 0

  // Quick-start cards (workflow/goal) float directly above the composer while
  // the active conversation has no history yet; they leave once the first
  // turn lands (mirrors the stream empty-state visibility conditions).
  // Coordinators are excluded: workflow/goal are project-agent quick starts —
  // the coordinator landing keeps project management cards only.
  const showQuickStartRow = !showDrawer
    && !!activeAgent
    && activeAgent.AgentKind !== 'coordinator'
    && !pendingFirstMessage
    && source.envelopes.length === 0
    && !source.loading
    && !agentInfoSnapshot.loading
    && sessionRestored

  // Random welcome line for the new-agent empty conversation; re-rolled when
  // switching agents or locale so each fresh chat feels varied.
  const newChatWelcome = useMemo(
    () => randomWelcome(t),
    [t, activeAgent?.Id],
  )

  const focusComposer = useCallback(() => {
    setTimeout(() => {
      const composer = document.querySelector('.ai-composer-textarea') as HTMLTextAreaElement | null
      composer?.focus()
    }, 0)
  }, [])

  // Git-repo detection for the welcome-row suggestion chips (shared fetch:
  // useHasGitRepo dedups per project at module level, so the project new-chat
  // page and this row never double-fetch no_git_mode_get).
  const { hasGitRepo: activeProjectHasGitRepo } = useHasGitRepo(activeProject?.ProjectID)

  // Favorited composer texts (most recent first) for the welcome-row chips.
  // Read-only here: writes stay inside the composers; the shared history store
  // propagates favorite toggles to this copy live.
  const { history: composerHistory } = useComposerHistory(COMPOSER_HISTORY_SCOPE)
  const favoritePrompts = useMemo(
    () => sortHistory(composerHistory).filter(h => h.isFavorite).map(h => h.text),
    [composerHistory],
  )

  // Suggestion chips fill the composer then focus it, mirroring
  // ProjectNewChatPage's handleSuggestionPick.
  const handleSuggestionPick = useCallback((text: string) => {
    dispatch({ type: 'SET_COMPOSER_VALUE', value: text })
    focusComposer()
  }, [dispatch, focusComposer])

  // Assign the content body renderer to the ref (updated every render for fresh closures).
  renderContentBodyRef.current = (mode: ContentMode): React.ReactNode => {
    if (sidebarMode === 'settings') {
      return (
        <SettingsContent
          activeIds={settingsCategories}
          theme={aiTheme}
          onThemeChange={handleThemeChange}
          configBadgesVisible={configBadgesVisible}
          onConfigBadgesVisibleChange={setConfigBadgesVisible}
          turnTailMetrics={turnTailMetrics}
          onTurnTailMetricsChange={setTurnTailMetrics}
          composerExtras={composerExtras}
          onComposerExtrasChange={setComposerExtras}
          smoothStream={smoothStream}
          onSmoothStreamChange={setSmoothStream}
          developerMode={developerMode}
          onDeveloperModeChange={setDeveloperMode}
          providerUserAgentVisible={providerUserAgentVisible}
          onProviderUserAgentVisibleChange={setProviderUserAgentVisible}
          expansionModes={expansionModes}
          onExpansionModeChange={handleExpansionModeChange}
          expandDurationMs={expandDurationMs}
          onExpandDurationChange={handleExpandDurationChange}
          onOpenDbClient={handleOpenDbClient}
          onOpenAppView={handleOpenAppView}
        />
      )
    }
    if (mode === 'topology') {
      return (
        <TopologyModeView
          cards={monoState.cards}
          agentInfoSnapshot={agentInfoSnapshot}
          activeAgentId={state.activeSessionId ?? undefined}
          onNodeClick={handleTopologyNodeClick}
          onWorkflowCardMenu={handleWorkflowCardMenu}
          onConnectNodes={handleConnectTopologyNodes}
          onActorNodeClick={handleActorNodeClick}
          onActorNodeContext={handleActorNodeContext}
          onMemoryNodeClick={handleOpenMemoryNodeTab}
          onContextMenu={handleTopologyContextMenu}
          pendingDeleteNodeIds={pendingDeleteNodeIds}
          pendingPlacement={pendingPlacement}
          onPlacementDone={handlePlacementDone}
          activeFilterIds={activeTopologyFilterIds}
          setActiveFilterIds={setActiveTopologyFilterIds}
          mode={topologyMode}
          onModeChange={setTopologyMode}
          brainMountAgentId={brainMountAgentId}
        />
      )
    }
    if (mode === 'workflow') {
      return (
        <TopologyModeView
          cards={monoState.cards}
          agentInfoSnapshot={agentInfoSnapshot}
          activeAgentId={state.activeSessionId ?? undefined}
          onNodeClick={handleTopologyNodeClick}
          onWorkflowCardMenu={handleWorkflowCardMenu}
          onWorkflowBlankMenu={handleWorkflowBlankMenu}
          onConnectNodes={handleConnectTopologyNodes}
          onStartWorkflow={handleStartWorkflow}
          onEditSchedule={(cardId) => { void handleOpenScheduleModal(cardId) }}
          onContextMenu={handleTopologyContextMenu}
          pendingDeleteNodeIds={pendingDeleteNodeIds}
          pendingPlacement={pendingPlacement}
          onPlacementDone={handlePlacementDone}
          activeFilterIds={activeTopologyFilterIds}
          setActiveFilterIds={setActiveTopologyFilterIds}
          mode="workflow"
        />
      )
    }
    if (mode === 'scheduled') {
      // Foreign-scope rows carry their origin projectId so monoStore reads and
      // writes target that project; undefined keeps the active-project path.
      const scheduledOpts = (pid?: string) =>
        pid && pid !== activeProject?.ProjectID ? { projectId: pid } : undefined
      return (
        <ScheduledView
          cards={monoState.cards}
          projectId={activeProject?.ProjectID}
          projects={projects}
          onLocateMap={openWorkflowAndLocate}
          onOpenCard={(cardId, pid) => {
            if (scheduledOpts(pid)) handleOpenCardTab(cardId, cardId, { edit: true, projectId: pid })
            else handleOpenCardForEdit(cardId)
          }}
          onCreateScheduler={async (templateCardId, opts) => {
            const created = await monoStore.createSchedulerCard(templateCardId, opts)
            return created?.id ?? null
          }}
          onUpdateScheduleCron={async (cardId, cron, pid) => {
            const card = await monoStore.getCard(cardId, scheduledOpts(pid))
            if (!card) return false
            const schedule = { ...(card.data?.schedule as Record<string, unknown> | undefined ?? {}) }
            schedule.cron = cron
            // expression and cron are mutually exclusive per card validation.
            delete schedule.expression
            const data = { ...(card.data ?? {}), schedule }
            const updated = await monoStore.updateCard(cardId, { data }, scheduledOpts(pid))
            return !!updated
          }}
          onUpdateCardBody={async (cardId, body, pid) => {
            const card = await monoStore.getCard(cardId, scheduledOpts(pid))
            if (!card) return false
            const updated = await monoStore.updateCard(cardId, { body }, scheduledOpts(pid))
            return !!updated
          }}
          onUpdateTitle={async (cardId, title, pid) => {
            const card = await monoStore.getCard(cardId, scheduledOpts(pid))
            if (!card) return false
            const data = { ...(card.data ?? {}), title }
            const updated = await monoStore.updateCard(cardId, { data }, scheduledOpts(pid))
            return !!updated
          }}
          onDeleteTimer={async (cardId, pid) => {
            await monoStore.deleteCard(cardId, scheduledOpts(pid))
            return true
          }}
          onUpdateExecutor={async (cardId, executor, pid) => {
            const card = await monoStore.getCard(cardId, scheduledOpts(pid))
            if (!card) return false
            const data = { ...(card.data ?? {}), executor }
            const updated = await monoStore.updateCard(cardId, { data }, scheduledOpts(pid))
            return !!updated
          }}
          onUpdateAgentKind={async (cardId, kind, pid) => {
            const card = await monoStore.getCard(cardId, scheduledOpts(pid))
            if (!card) return false
            const data = { ...(card.data ?? {}) } as Record<string, unknown>
            if (kind) data.agent_kind = kind
            else delete data.agent_kind
            // Picking a kind means fresh-agent-per-fire: drop any bound-agent
            // contract so the fire branch stops routing to the stale binding.
            delete data.bind_mode
            delete data.bound_agent
            const updated = await monoStore.updateCard(cardId, { data }, scheduledOpts(pid))
            return !!updated
          }}
          onUpdateBoundAgent={async (cardId, agentRef, pid) => {
            const card = await monoStore.getCard(cardId, scheduledOpts(pid))
            if (!card) return false
            const data = { ...(card.data ?? {}) } as Record<string, unknown>
            if (agentRef) {
              // Dual-mode scheduler contract: bind_mode=bound makes the fire
              // reuse this stable agent instead of spawning per fire.
              data.bind_mode = 'bound'
              data.bound_agent = agentRef
              delete data.agent_kind
              delete data.model_slots
            } else {
              delete data.bind_mode
              delete data.bound_agent
            }
            const updated = await monoStore.updateCard(cardId, { data }, scheduledOpts(pid))
            return !!updated
          }}
          onUpdateAgentActions={async (cardId, actions, pid) => {
            const card = await monoStore.getCard(cardId, scheduledOpts(pid))
            if (!card) return false
            const data = { ...(card.data ?? {}) }
            data.agent_actions = JSON.stringify(
              actions.map(a => ({ action: a.action, target: a.targetAgent })),
            )
            delete data.agent_action
            delete data.target_agent
            data.schedule_type = 'task'
            const updated = await monoStore.updateCard(cardId, { data }, scheduledOpts(pid))
            return !!updated
          }}
          onUpdateTemplateBinding={async (cardId, templateId, pid) => {
            const card = await monoStore.getCard(cardId, scheduledOpts(pid))
            if (!card) return false
            const data = { ...(card.data ?? {}) }
            if (templateId) {
              data.workflow_template = templateId
            } else {
              delete data.workflow_template
            }
            delete data.task_mode
            data.schedule_type = 'task'
            const updated = await monoStore.updateCard(cardId, { data }, scheduledOpts(pid))
            return !!updated
          }}
          isMobile={isMobile}
        />
      )
    }
    if (mode === 'multiconsole') {
      return (
        <MultiConsoleContent
          activeSessionId={state.activeSessionId}
          activeProjectId={activeProject?.ProjectID ?? null}
          onSwitchToAgent={handleSwitchToAgent}
          onRequestProjectChange={handleProjectChange}
          onClearHistory={(agent) => { void handleClearAgentHistory(agent) }}
          onInspectInfo={handleOpenAgentInspector}
        />
      )
    }
    if (mode === 'problems') return <ProblemsPanel />
    if (mode === 'approvals') return <ApprovalAuditPanel />
    if (mode === 'aistats') return <AIStatsDashboard />
    if (mode === 'ssh') return <ShellSshContent onOpenSession={handleOpenSshSession} isActive={contentMode === 'ssh'} />
    if (mode === 'db') return <ShellDbContent onOpenClient={handleOpenDbClient} isActive={contentMode === 'db'} />
    if (mode === 'git') return (
      <GitPanel
        projectId={activeProject?.ProjectID ?? null}
        onOpenFile={handleOpenFile}
        providers={providerContext.providers}
        activeProviderId={providerContext.activeProviderId}
        agentActorId={activeAgentActorId}
        agentName={activeAgent?.Title || activeAgent?.DisplayName}
      />
    )
    if (mode === 'notes') {
      return (
        <div className="ai-right-panel-content ai-wiki-tab">
          <KnowledgeModeView
            openRequest={kbOpenRequest}
            onOpenRequestConsumed={handleKbOpenRequestConsumed}
            onActiveLaneChange={setKbActiveLane}
            onOpenCardInRightPanel={(cardId, projectId) => handleOpenMonoCardInRightPanel(cardId, cardId, projectId)}
            isMobile={isMobile}
          />
        </div>
      )
    }
    // File mode is decoupled from the agent's active project: the browser
    // manages its own target (persisted, switchable from its toolbar picker).
    // It mounts unconditionally — while the projects list is still loading it
    // shows its own loading hint, and only a settled empty list shows the
    // no-project placeholder, so opening it at any time always has content.
    if (mode === 'files') return (
      <FileBrowser
        projects={userProjects}
        projectsLoading={contextLoading}
        onProjectChange={handleFileBrowserProjectChange}
        onOpenFile={(filePath, projectId) => { handleOpenFile(filePath, undefined, undefined, undefined, undefined, undefined, undefined, undefined, projectId) }}
        path={fileBrowserPath ?? undefined}
        onPathChange={handleFileBrowserPathChange}
        selectedPath={fileBrowserSelection}
        onSelectionChange={handleFileBrowserSelectionChange}
      />
    )
    if (showNewChatPage) {
      return (
        <ProjectNewChatPage
          activeProject={activeProject}
          projects={userProjects}
          onProjectChange={handleProjectChange}
          projectSwitchingId={projectSwitchingId}
          contextLoading={contextLoading}
          agents={activeProjectAgents}
          onAgentClick={handleStartAgentChat}
          onAgentMenuOpen={handleOpenAgentMenu}
          onAgentCreated={handleProjectNewChatAgentCreated}
          onCreateAgent={activeProject ? () => handleOpenCreateAgent(activeProject.ProjectID) : undefined}
          onDeleteAgent={handleDeleteAgent}
          onCreateProject={() => { setNewProjectDialogMode('create'); setShowNewProjectDialog(true) }}
          onOpenProject={() => { setNewProjectDialogMode('open'); setShowNewProjectDialog(true) }}
        />
      )
    }
    // Coordinator landing page: shown when the active agent is the global
    // coordinator and it has no conversation history yet. Renders the unified
    // ProjectNewChatPage in "home" mode — the project selector shows a Home
    // option at the top; typing sends into the coordinator's session via the
    // pending-first-message mechanism and swaps to the conversation page.
    if (showCoordinatorHome && activeAgent) {
      const fallbackProject = activeProject ?? userProjects.find(p => p.IsOpen) ?? userProjects[0] ?? null
      return (
        <ProjectNewChatPage
          activeProject={fallbackProject}
          projects={userProjects}
          onProjectChange={handleProjectChange}
          projectSwitchingId={projectSwitchingId}
          contextLoading={contextLoading}
          agents={sidebarAgents}
          onAgentClick={(agent) => {
            if (!agent.ProjectId) {
              handleSelectCoordinator(agent)
            } else {
              void handleStartAgentChat(agent)
            }
          }}
          onAgentMenuOpen={handleOpenAgentMenu}
          onAgentCreated={handleProjectNewChatAgentCreated}
          onCreateAgent={() => handleOpenCreateAgent(fallbackProject?.ProjectID)}
          onDeleteAgent={handleDeleteAgent}
          onCreateProject={() => { setNewProjectDialogMode('create'); setShowNewProjectDialog(true) }}
          onOpenProject={() => { setNewProjectDialogMode('open'); setShowNewProjectDialog(true) }}
          coordinator={activeAgent}
          onCoordinatorSend={(text, unit) => {
            setPendingFirstMessage({ agentId: activeAgent.Id, text, unit })
            dispatch({ type: 'SELECT_SESSION', id: activeAgent.Id })
          }}
          permissionMode={effectivePermissionMode}
          onPermissionModeChange={handlePermissionModeChange}
          globalPermissionMode={globalPermissionMode}
          onGlobalPermissionModeChange={handleGlobalPermissionModeChange}
        />
      )
    }
    return (
      <InteractionDispatchContext.Provider value={source.dispatchEvent}>
        <AIConversationPage
          envelopes={source.envelopes}
          emptyState={activeAgent ? null : pendingFirstMessage ? (
            <div className="project-new-chat-creating">Creating agent…</div>
          ) : (
            <div className="ai-empty-quick-card">
              <div className="ai-empty-quick-card-icon"><Folder size={24} strokeWidth={1.5} /></div>
              <div className="ai-empty-quick-card-title">{t('shell.newChat.noProject.title')}</div>
              <div className="ai-empty-quick-card-subtitle">{t('shell.newChat.noProject.subtitle')}</div>
              <div className="project-new-chat-quick-actions">
                <button
                  type="button"
                  className="project-new-chat-quick-action"
                  style={{ '--qa-accent': '#2b9be6' } as React.CSSProperties}
                  onClick={() => { setNewProjectDialogMode('create'); setShowNewProjectDialog(true) }}
                >
                  <span className="project-new-chat-quick-action-icon"><FolderPlus size={16} /></span>
                  <span className="project-new-chat-quick-action-label">{t('shell.newChat.quickAction.createProject')}</span>
                </button>
                <button
                  type="button"
                  className="project-new-chat-quick-action"
                  style={{ '--qa-accent': '#22a06b' } as React.CSSProperties}
                  onClick={() => { setNewProjectDialogMode('open'); setShowNewProjectDialog(true) }}
                >
                  <span className="project-new-chat-quick-action-icon"><FolderOpen size={16} /></span>
                  <span className="project-new-chat-quick-action-label">{t('shell.newChat.quickAction.openProject')}</span>
                </button>
              </div>
            </div>
          )}
          isStreaming={source.isStreaming}
          smoothStream={smoothStream}
          timelineLoading={source.loading || agentInfoSnapshot.loading || !sessionRestored}
          onFrameSelect={(frame) => {
            if (tryOpenSchemaOverlayForFrame(frame)) return
            dispatch({ type: 'SELECT_FRAME', frame })
          }}
          onSaveToCard={handleSaveTextToCard}
          activeAgent={activeAgent}
          activeProjectId={activeProject?.ProjectID ?? null}
          projects={userProjects}
          hasMoreHistory={source.hasMoreHistory}
          summarizedBoundary={source.summarizedBoundary}
          discardedBoundary={source.discardedBoundary}
          loadingMoreHistory={source.loadingMore}
          loadOlderTurns={source.loadOlderTurns}
          onCancelPendingSubmit={source.cancelPendingSubmit}
          activeConversationId={activeConversationId}
        />
      </InteractionDispatchContext.Provider>
    )
  }

  // Global unique composer: rendered in the main layout normally, and moved
  // into the expanded right-panel bottom slot in fullscreen panel mode.

  const renderBacklogInterceptDialog = (inContentColumn: boolean) => (
    <ConfirmDialog
      open={backlogIntercept !== null}
      title={t('composer.backlogIntercept.title')}
      description={t('composer.backlogIntercept.message', { content: backlogIntercept ?? '' })}
      confirmLabel={t('common.confirm')}
      cancelLabel={t('common.cancel')}
      confirmOnEnter={false}
      overlayClassName={inContentColumn ? 'absolute' : undefined}
      onCancel={handleCancelBacklogIntercept}
      onConfirm={() => { void handleConfirmBacklogIntercept() }}
    />
  )

  const composerFrame = (
    <InteractionDispatchContext.Provider value={source.dispatchEvent}>
    <ComposerFrame
      showDrawer={showDrawer}
      envelopes={source.envelopes}
      isStreaming={source.isStreaming}
      conversationId={activeConversationId}
      onFrameSelect={(frame) => {
        if (tryOpenSchemaOverlayForFrame(frame)) return
        dispatch({ type: 'SELECT_FRAME', frame })
      }}
      onCancelPendingSubmit={source.cancelPendingSubmit}
      onHeightChange={setComposerFrameH}
      onCardTopChange={setComposerCardTop}
      drawerResizable={!isMobile}
      drawerHeight={composerDrawerHeight ?? undefined}
      onDrawerHeightChange={handleComposerDrawerResize}
      aboveBarSlot={
        showDrawer ? (
          <>
            <div className="ai-composer-above-left">
              <QuickSwitchMenu
                id="ai-project-switcher-drawer"
                value={activeProject?.ProjectID ?? null}
                options={userProjects.map(project => {
                  const st = projectStatusMap.get(project.ProjectID)
                  return {
                    id: project.ProjectID,
                    label: projectDisplayName(project, t),
                    isWorking: st?.isWorking,
                    isError: st?.isError,
                    statusLabel: st?.statusLabel,
                  }
                })}
                disabled={contextLoading || projectSwitchingId !== null}
                onChange={handleProjectChange}
                action={activeProject ? { label: 'New Project', onClick: () => { setNewProjectDialogMode('create'); setShowNewProjectDialog(true) } } : undefined}
                emptyLabel="no project"
                header="Project"
                projectAgentsMap={projectAgentsMap}
                onDeleteOption={(agentId) => {
                  const a = allAgents.find(x => x.Id === agentId)
                  if (a) handleDeleteAgent(a)
                }}
                secondaryColumn={{
                  header: 'Agent',
                  options: conversationOptions,
                  value: activeConversationId,
                  onChange: handleSelectProjectAgent,
                  action: activeProject ? { label: 'New Agent', onClick: () => handleOpenCreateAgent(activeProject.ProjectID) } : undefined,
                  emptyLabel: 'no agent',
                }}
              />
              {composerExtras.mobileContextAgent && activeProject && activeAgent && (
                <div className="ai-conversation-mobile-context">
                  <span
                    className="ai-conversation-mobile-context-agent"
                    title={agentDisplayName(activeAgent.Title, activeAgent.DisplayName)}
                  >
                    {truncateLabel(agentDisplayName(activeAgent.Title, activeAgent.DisplayName), 40)}
                  </span>
                </div>
              )}
            </div>
            <div className="ai-composer-above-middle">
              {(contextLoading || contextError || contextSuccess || projectSwitchingId) && (
                <div className="ai-conversation-quickbar-status">
                  {projectSwitchingId
                    ? 'Switching project…'
                    : contextError
                      ? contextError
                      : contextSuccess
                        ? contextSuccess
                        : 'Loading context…'}
                  <ProjectCardsIndicator />
                </div>
              )}
            </div>
            <div className="ai-composer-above-right">
              {composerExtras.quickGit && (
                <GitQuickbar
                  projectId={activeProject?.ProjectID ?? null}
                  onOpenDiff={(filePath, diffContent) => handleOpenFile(filePath, diffContent)}
                  onOpenGitMode={() => window.dispatchEvent(new CustomEvent('sporemind:set-shell-content-mode', { detail: 'git' }))}
                />
              )}
            </div>
          </>
        ) : undefined
      }
    >
      {showQuickStartRow && activeAgent && (
        <>
          <div className="project-new-chat-header">
            <h2 className="project-new-chat-title">{newChatWelcome}</h2>
          </div>
          <QuickStartActions
            actorId={activeAgent.ActorId}
            onMounted={focusComposer}
          />
          <SuggestionChips
            context={{
              hasProject: !!activeProject?.ProjectID,
              isHomeMode: false,
              hasGitRepo: activeProjectHasGitRepo,
              agentKind: activeAgent.AgentKind,
              permissionMode: effectivePermissionMode,
              favoritePrompts,
            }}
            onPick={handleSuggestionPick}
          />
        </>
      )}
      <AIConversationComposer
        value={state.composerValue}
        onChange={(v) => dispatch({ type: 'SET_COMPOSER_VALUE', value: v })}
        onSend={handleSend}
        onSubmit={handleSend}
        placeholder={composerPlaceholder}
        onMountedModeCardIdsChange={setMountedModeCardIds}
        isStreaming={source.isStreaming}
        isPaused={source.isPaused}
        isWaiting={source.isWaiting}
        isPausing={source.isPausing}
        onStop={source.stop}
        onPause={handlePause}
        onResume={source.resume}
        projects={userProjects}
        activeProjectId={activeProject?.ProjectID ?? null}
        onProjectChange={handleProjectChange}
        projectSwitchingId={projectSwitchingId}
        contextLoading={contextLoading}
        contextError={contextError}
        conversations={conversationOptions}
        activeConversationId={activeConversationId}
        onConversationChange={handleSelectProjectAgent}
        onCreateProject={() => { setNewProjectDialogMode('create'); setShowNewProjectDialog(true) }}
        onCreateAgent={() => handleOpenCreateAgent(activeProject?.ProjectID ?? userProjects[0]?.ProjectID ?? '')}
        allAgents={sidebarAgents}
        activeAgent={activeAgent}
        taskModeBadge={activeTaskModeBadge}
        onAgentAvatarClick={handleAgentAvatarClick}
        onAgentMenuOpen={handleOpenAgentMenu}
        onDeleteAgent={handleDeleteAgent}
        onOpenDiff={(filePath, diffContent) => handleOpenFile(filePath, diffContent)}
        onOpenGitMode={() => window.dispatchEvent(new CustomEvent('sporemind:set-shell-content-mode', { detail: 'git' }))}
        permissionMode={effectivePermissionMode}
        onPermissionModeChange={handlePermissionModeChange}
        globalPermissionMode={globalPermissionMode}
        onGlobalPermissionModeChange={handleGlobalPermissionModeChange}
        isMobile={isMobile}
        hideAboveSlots={showDrawer}
        onReturnToConversation={showDrawer ? () => handleContentModeChange('conversation') : undefined}
        configBadgesVisible={configBadgesVisible}
        quickGitVisible={composerExtras.quickGit}
        mobileContextAgentVisible={composerExtras.mobileContextAgent}
        onOpenOmnibox={() => {
          setOmniboxPosition(isMobile ? 'top' : 'bottom')
          setOmniboxOpen(true)
        }}
        onOpenCard={handleOpenMonoCardInRightPanel}
        onOpenBrowserTab={(instanceId, name, url) => {
          setRightPanelOpen(true)
          handleOpenBrowserRightTab(instanceId, 'independent', url, name)
        }}
        developerMode={developerMode}
      />
    </ComposerFrame>
    </InteractionDispatchContext.Provider>
  )

  return (
    <EditorKeymapProvider>
    <FileClipboardProvider>
    <SchemaOverlayProvider>
    <AIShellContext.Provider value={wrappedProviderContext}>
    <SmoothStreamContext.Provider value={{ smoothStream, isStreaming: source.isStreaming, expansionModes, expandDurationMs }}>
      <div
        className={`ai-shell-layout ai-shell-${viewMode} ${embedded ? 'embedded' : 'standalone'}${!embedded && launcherMode === 'workbench' ? ' ai-shell-layout--workbench' : ''}`}
        ref={layout.containerRef}
        {...{ [OS_DROP_TARGET_ATTR]: '' }}
        onDragOver={handleOsDropDragOver}
        onDrop={handleOsDropOpenFiles}
        style={{ ['--sidebar-width' as any]: `${sidebarWidth}px`, ['--composer-frame-h' as any]: `${composerFrameH}px`, ['--composer-card-top' as any]: `${composerCardTop}px`, ['--ai-expand-ms' as any]: smoothStream ? `${expandDurationMs}ms` : '0ms' }}
      >
        {/* Workbench takeover hides the topbar entirely (the board's own
            .wb-drag-region keeps the titlebar strip draggable and its corner
            carries the window controls). */}
        {!embedded && launcherMode !== 'workbench' && (
          <AIShellTopbar
            maximised={maximised}
            onMaximisedChange={setMaximised}
            sidebarVisible={sidebar.showSidebar}
            onToggleSidebar={sidebar.handleToggleSidebar}
            rightPanelOpen={rightPanelOpen}
            onToggleRightPanel={() => setRightPanelOpen(prev => !prev)}
            terminalPanelOpen={terminalPanelOpen}
            onToggleTerminalPanel={() => setTerminalPanelOpen(prev => !prev)}
            shellVariant={shellVariant}
            isMobile={isMobile}
            previewScenarios={source.previewScenarios}
            activePreviewScenarioId={source.activePreviewScenarioId}
            onPreviewScenarioChange={source.setPreviewScenarioId}
            isPreviewStreaming={shellVariant === 'preview' ? source.isStreaming : false}
            onStartPreview={shellVariant === 'preview' ? source.startPreview : undefined}
            onResetPreview={shellVariant === 'preview' ? source.resetPreview : undefined}
            contentMode={contentMode}
            onContentModeChange={handleContentModeChange}
            onShellModeChange={onShellModeChange}
            pinnedModes={pinnedModes}
            onTogglePinnedMode={handleTogglePinnedMode}
            onOpenOmnibox={() => {
              setOmniboxPosition('top')
              setOmniboxOpen(true)
            }}
            canGoBack={navHistory.canGoBack}
            canGoForward={navHistory.canGoForward}
            onGoBack={goBackContentMode}
            onGoForward={goForwardContentMode}
            onFileMenuAction={handleFileMenuAction}
            onEditMenuAction={handleEditMenuAction}
            onViewMenuAction={handleViewMenuAction}
            onHelpMenuAction={handleHelpMenuAction}
            onAboutAction={handleAboutAction}
            launcherMode={launcherMode}
            onLauncherModeChange={handleLauncherModeChange}
          />
        )}

        {/* Toast cards pop over the main window from this overlay — hidden
            by default (no live cards → nothing rendered). */}
        <ToastOverlay />

        <div className="ai-shell-body">
          {!embedded && isMobile && sidebar.showSidebar && (
            <div className="ai-sidebar-backdrop" onClick={() => dispatch({ type: 'SET_SIDEBAR_VISIBLE', value: false })} />
          )}

          {/* 左侧项目/agent sidebar 属于独立 AI Shell；当 AI Shell 作为 Workbench 标签页嵌入时，必须隐藏 sidebar。 */}
          {!embedded && (
            <div
              ref={sidebarRef}
              className={`ai-sidebar-container ${sidebar.showSidebar ? 'open' : ''} ${isMobile ? 'overlay' : 'pinned'} ${isMobile ? 'mobile' : 'desktop'}`}
              style={!isMobile && sidebar.showSidebar ? { width: sidebarWidth } : undefined}
            >
              {sidebarMode === 'normal' ? (
                launcherMode === 'app' ? (
                  <SidebarLauncher
                    apps={launcherApps}
                    mode={launcherMode}
                    onModeChange={setLauncherMode}
                    isMobile={isMobile}
                    account={account}
                    onOpenSettings={handleOpenSettings}
                    onOpenMobileSync={handleOpenMobileSync}
                    view={launcherView}
                    onViewChange={setLauncherView}
                    onLaunch={handleLauncherLaunch}
                    favorites={launcherFavorites}
                    recent={launcherRecent}
                    order={launcherOrder}
                    onToggleFavorite={handleLauncherToggleFavorite}
                    onMoveToTop={handleLauncherMoveToTop}
                    onRemoveFromRecent={handleLauncherRemoveRecent}
                    onReorder={handleLauncherReorder}
                    onManageApps={handleLauncherManageApps}
                    onInstallZipDrop={handleLauncherZipDrop}
                    showDeveloperActions
                  />
                ) : (
                <AIShellSidebar
                  projects={sidebarProjects}
                  systemTreeNodes={systemTreeNodes}
                  activeProject={resolvedActiveProject}
                  projectAgents={sidebarAgents}
                  activeConversationId={activeConversationId}
                  onOpenCreateProject={() => { setNewProjectDialogMode('create'); setShowNewProjectDialog(true) }}
                  onSelectProject={handleProjectChange}
                  onReorderProjects={handleReorderProjects}
                  onReorderAgents={handleReorderAgents}
                  onAgentClick={handleAgentAvatarClick}
                  globalAgents={globalAgents}
                  pluginAgentGroups={pluginAgentGroups}
                  onSelectCoordinator={handleSelectCoordinator}
                  onEditAgent={agent => { void openEditAgent(agent) }}
                  onOpenAgentMenu={handleOpenAgentMenu}
                  onDeleteAgent={handleDeleteAgent}
                  onCreateAgent={handleOpenCreateAgent}
                  onNewChat={() => {
                    dispatch({ type: 'SELECT_SESSION', id: '' })
                    setContentMode('conversation')
                  }}
                  onOpenProjectMenu={handleOpenProjectMenu}
                  onCloseSidebar={() => dispatch({ type: 'SET_SIDEBAR_VISIBLE', value: false })}
                  isMobile={isMobile}
                  account={account}
                  theme={aiTheme}
                  onThemeChange={handleThemeChange}
                  contentMode={contentMode}
                  onContentModeChange={handleContentModeChange}
                  pinnedModes={pinnedModes}
                  onReorderPinnedModes={handleReorderPinnedModes}
                  onTogglePinnedMode={handleTogglePinnedMode}
                  activeKbLane={kbActiveLane}
                  onOpenSettings={handleOpenSettings}
                  onOpenMobileSync={handleOpenMobileSync}
                  onShellModeChange={onShellModeChange}
                  onOpenFile={(filePath) => handleOpenFile(filePath)}
                  launcherMode={launcherMode}
                  onLauncherModeChange={setLauncherMode}
                  onOpenOmnibox={() => {
                    setOmniboxPosition('top')
                    setOmniboxOpen(true)
                  }}
                />
                )
              ) : (
                <SettingsSidebar
                  activeIds={settingsCategories}
                  onSelect={(id) => {
                    saveLastSettingsCategory(id)
                    setSettingsCategories([id])
                  }}
                  onToggle={(id) => {
                    setSettingsCategories(prev => {
                      let next: string[]
                      if (prev.includes(id)) {
                        if (prev.length === 1) return prev
                        next = prev.filter(c => c !== id)
                      } else {
                        next = [...prev, id]
                      }
                      const first = next[0]
                      if (first) saveLastSettingsCategory(first)
                      return next
                    })
                  }}
                  onBack={() => setSidebarMode('normal')}
                />
              )}
            </div>
          )}
          {/* Main work area card: content + right panel merged; terminal panel hangs below */}
          <div className={`ai-shell-main-col ${terminalPanelOpen ? 'with-terminal' : ''}`}>
          {/* Sidebar resize handle lives on the column (not the card) so its drag
              edge spans the main card AND the terminal panel hanging below. */}
          {!embedded && !isMobile && sidebar.showSidebar && (
            <ResizeHandle
              direction="horizontal"
              targetRef={sidebarRef}
              onResize={handleSidebarResize}
              minSize={180}
              className="ai-shell-sidebar-handle"
            />
          )}
          <div
            className={`ai-shell-main-card ${embedded ? 'embedded' : ''} ${!embedded && !isMobile && sidebar.showSidebar ? 'with-sidebar' : ''} ${rightPanelExpanded ? 'expanded' : ''} ${!rightPanelOpen && !rightPanelExpanded ? 'right-panel-closed' : ''} ${contentMode === 'files' ? 'files-mode' : ''} ${contentMode === 'git' ? 'git-mode' : ''} ${launcherMode === 'app' ? 'app-mode' : ''}`}
          >
            <div className={`ai-shell-content ${contentMode === 'topology' ? 'topology-mode' : ''} ${rightPanelExpanded ? 'collapsed' : ''}`}>
              <div className="ai-shell-content-body">
                {/* When the right panel is expanded, content renders inside the panel tabs */}
                {!rightPanelExpanded && (
                  sidebarMode === 'settings' || !CACHEABLE_MODES.has(contentMode)
                    ? renderContentBodyRef.current(contentMode)
                    : visitedModes.map(mode => (
                        <div
                          key={mode}
                          className={`ai-shell-mode-pane${mode === contentMode ? ' ai-shell-mode-pane--active' : ''}`}
                        >
                          {renderContentBodyRef.current(mode)}
                        </div>
                      ))
                )}
              </div>

              {sidebarMode === 'normal' && !rightPanelExpanded && (contentMode === 'notes' || (!showNewChatPage && !showCoordinatorHome)) && contentMode !== 'multiconsole' && composerFrame}
              {!rightPanelExpanded && renderBacklogInterceptDialog(true)}
            </div>

            {/* 右侧边栏属于独立 AI Shell；当 AI Shell 作为 Workbench 标签页嵌入时，必须隐藏右侧边栏。 */}
            {!embedded && (
              <>
                {rightPanelOpen && !isMobile && !rightPanelExpanded && (
                  <ResizeHandle direction="horizontal" targetRef={rightPanelRef} onResize={handleRightPanelResize} minSize={240} negateDelta={launcherMode !== 'app'} />
                )}
                {!isMobile && (
                  <RightPanelTabs
                    ref={rightPanelRef}
                    tabs={rightTabItems}
                    activeTabId={activeBrowserTabId}
                    onSwitchTab={handleSwitchRightTab}
                    onCloseTab={handleCloseRightTab}
                    onReorderTabs={handleReorderTabs}
                    onDragStartReorder={handleDragStartReorder}
                    onDragEndReorder={handleDragEndReorder}
                    newTabOptions={newTabOptions}
                    isMobile={false}
                    onToggleExpand={handleToggleExpand}
                    isExpanded={rightPanelExpanded}
                    bottomSlot={rightPanelExpanded && launcherMode !== 'app' && !showNewChatPage && !showCoordinatorHome && contentMode !== 'multiconsole' ? composerFrame : undefined}
                    style={rightPanelOpen
                      ? rightPanelExpanded
                        ? { flex: 1, width: 'auto', overflow: 'hidden', ['--browser-bg' as any]: browserBgColor || undefined }
                        : { width: rightPanelWidth, overflow: 'hidden' }
                      : { width: 0, overflow: 'hidden' }
                    }
                  />
                )}
              </>
            )}
            </div>

            {/* IDEA-style terminal panel: desktop inline below the main card;
                mobile renders a fixed bottom-sheet overlay (mutually exclusive
                with the right panel, enforced above). Hidden in embedded mode. */}
            {!embedded && (
              <TerminalPanel
                tabs={terminalTabs}
                activeTabId={terminalActiveTabId}
                open={terminalPanelOpen}
                height={terminalPanelHeight}
                isMobile={isMobile}
                onToggle={() => setTerminalPanelOpen(prev => !prev)}
                onSelectTab={setTerminalActiveTabId}
                onCloseTab={handleCloseTerminalTab}
                onAddTab={handleAddTerminalTab}
                onResize={handleTerminalPanelResize}
                onSetTabTitle={handleSetTerminalTabTitle}
                onOpenFile={(filePath, line) => handleOpenFile(filePath, undefined, line)}
                projectRoot={activeProject?.RootPath}
              />
            )}
          </div>

          {/* Mobile overlay right panel stays outside the main card */}
          {!embedded && isMobile && rightPanelOpen && rightTabItems.length > 0 && (
            <RightPanelTabs
              ref={rightPanelRef}
              tabs={rightTabItems}
              activeTabId={activeBrowserTabId}
              onSwitchTab={handleSwitchRightTab}
              onCloseTab={handleCloseRightTab}
              onReorderTabs={handleReorderTabs}
              onDragStartReorder={handleDragStartReorder}
              onDragEndReorder={handleDragEndReorder}
              onClosePanel={() => setRightPanelOpen(false)}
              newTabOptions={newTabOptions}
              isMobile={true}
              style={{}}
            />
          )}

        </div>

        {/* 工作台全屏接管层（launcher-mode 第三档）：fixed inset 0 盖住 topbar、
            sidebar 与右栏，顶部保留窗口拖拽区。退出经板面左上角按钮，回到
            之前的构建/应用面板。嵌入模式（AI Shell 作为 Workbench 标签页）不接管。 */}
        {!embedded && launcherMode === 'workbench' && (
          <WorkbenchSurface
            projectRoot={activeProject?.RootPath}
            isDark={aiTheme.mode === 'dark'}
            onExit={() => handleLauncherModeChange(lastPaneLauncherModeRef.current)}
            onToggleTheme={() =>
              handleThemeChange({ ...aiTheme, mode: aiTheme.mode === 'dark' ? 'light' : 'dark' })
            }
          />
        )}

        {rightPanelExpanded && renderBacklogInterceptDialog(false)}
        <NewAgentDialog
          open={!!createAgentProject || !!editAgent || !!cloneAgent || !!forkTurn}
          mode={editAgent ? 'edit' : (cloneAgent || forkTurn) ? 'clone' : 'create'}
          editAgent={editAgent ?? undefined}
          forkTurnId={forkTurn?.turnId}
          cloneSource={(cloneAgent ?? forkTurn?.agent) ? {
            displayName: (cloneAgent ?? forkTurn!.agent).DisplayName,
            agentKind: (cloneAgent ?? forkTurn!.agent).AgentKind,
            projectId: (cloneAgent ?? forkTurn!.agent).ProjectId,
          } : undefined}
          projects={(projects ?? []).map(p => ({ ActorId: p.ProjectID, Name: p.Name, Path: p.RootPath, Root: false, System: p.System }))}
          aggregators={aggregators}
          agentKinds={agentKinds}
          agents={allAgents}
          defaultAgentKind={defaultAgentKind}
          defaultProjectId={createAgentProject?.ProjectID ?? cloneAgent?.ProjectId ?? forkTurn?.agent.ProjectId}
          creating={agentOps.state.creating}
          error={agentOps.state.error || ''}
          compactionPolicy={editAgentInfo?.CompactionPolicy}
          compactionSource={editAgentInfo?.CompactionPolicySource}
          onCancel={handleCloseCreateAgent}
          onSubmit={editAgent
            ? handleUpdateAgent
            : cloneAgent
              ? handleCloneAgent
              : forkTurn
                ? handleForkFromTurn
                : handleCreateAgent}
        />
        <AgentContextMenu
          target={agentMenu}
          onClose={handleCloseAgentMenu}
          onOpenChat={(agent) => { void handleOpenAgentChat(agent) }}
          onOpenCard={(agent) => { handleOpenMonoCardInRightPanel(agentCardId(agent.Id), agentDisplayName(agent.Title, agent.DisplayName)) }}
          brainMountAgentId={brainMountAgentId}
          onToggleBrainMount={(agent) => {
            void navigateToAgent(agent.ProjectId, agent.Id, { switchToConversation: false })
            setBrainMountAgentId(prev => prev === agent.Id ? null : agent.Id)
            setContentMode('topology')
            if (topologyMode !== 'cards') setTopologyMode('cards')
          }}
          onOpenBrain={(agent) => { void handleOpenBrain(agent) }}
          onEdit={(agent) => { void openEditAgent(agent) }}
          onClone={(agent) => { setCloneAgent(agent); agentOps.clearError() }}
          onClearHistory={(agent) => { void handleClearAgentHistory(agent) }}
          onDelete={(agent) => { void handleDeleteAgent(agent) }}
          onInspectInfo={handleOpenAgentInspector}
        />
        <ProjectContextMenu
          target={projectMenu}
          onClose={handleCloseProjectMenu}
          onOpen={(project) => { void handleProjectChange(project.ProjectID) }}
          onCreateAgent={(project) => { handleOpenCreateAgent(project.ProjectID) }}
          onRename={(project) => { setRenameProject(project) }}
          onCopyPath={(project) => { void navigator.clipboard.writeText(project.RootPath) }}
          onOpenInSystem={(project) => { void handleOpenProjectInSystem(project) }}
          onCloseProject={(project) => { void handleCloseProject(project) }}
          onRegisterApp={(project) => { void handleRegisterApp(project) }}
          onReloadApp={(project) => { void handleReloadApp(project) }}
          onProperties={(project) => { setPropertiesProject(project) }}
        />
        <ProjectPropertiesOverlay
          project={propertiesProject}
          onClose={() => { setPropertiesProject(null) }}
        />
        <TopologyContextMenu
          target={topologyMenu}
          onClose={handleCloseTopologyMenu}
          onCreateAgent={handleTopologyCreateAgent}
          onCreateProject={handleTopologyCreateProject}
          onCreateCard={handleTopologyCreateCard}
          onCreateBacklog={handleTopologyCreateBacklog}
          onCreateSkill={handleTopologyCreateSkill}
          onCreatePrompt={handleTopologyCreatePrompt}
        />
        <TopologyCardContextMenu
          target={cardMenu}
          context={cardMenuContext}
          onClose={handleCardMenuClose}
          onOpenCard={handleCardOpen}
          onRename={handleCardRename}
          onAddTag={handleCardAddTag}
          onAddChild={handleCardAddChild}
          onSetVisual={handleCardSetVisual}
          onDuplicate={handleCardDuplicate}
          onCopyPath={handleCardCopyPath}
          onDelete={handleCardDelete}
          onFilterSubtree={handleCardFilterSubtree}
          onFilterLineage={handleCardFilterLineage}
          onChat={handleCardMenuChat}
          onAssignGoal={handleCardAssignGoal}
          onSaveAsTemplate={handleCardSaveAsTemplate}
          onLocateTemplate={handleCardLocateTemplate}
          onInstantiateFromTemplate={handleCardInstantiateFromTemplate}
          onCreateScheduledTask={handleCardCreateScheduledTask}
          onViewInstances={handleCardViewInstances}
          instanceRuns={cardMenuRuns}
          onOpenInstanceMap={handleOpenInstanceMap}
          onRunNow={handleCardRunNow}
          onLocateScheduler={handleCardLocateScheduler}
          onToggleFold={handleCardToggleFold}
          foldedWorkflows={foldSnapshot.foldedWorkflows}
        />
        <TopologyEdgeContextMenu
          target={edgeMenu}
          onClose={handleCloseEdgeMenu}
          onOpenCards={handleEdgeOpenCards}
          onDelete={handleEdgeDelete}
        />
        <WorkflowBlankContextMenu
          target={workflowBlankMenu}
          onClose={() => setWorkflowBlankMenu(null)}
          onCreateCard={() => {
            setWorkflowBlankMenu(null)
            // Reuse the topology create-card flow: place the card at the menu position.
            const fakeTarget: TopologyMenuTarget = { x: 0, y: 0, canvasX: 0, canvasY: 0 }
            handleTopologyCreateCard(fakeTarget)
          }}
          onFitView={() => {
            setWorkflowBlankMenu(null)
            requestWorkflowLocate({ mapId: '*' })
          }}
          onDirectionChange={() => {
            setWorkflowBlankMenu(null)
            requestWorkflowDirectionToggle()
          }}
        />
        <ScheduleModal
          card={scheduleEdit?.card ?? null}
          draft={scheduleEdit?.draft ?? null}
          onDraftChange={(d) => setScheduleEdit(prev => prev ? { ...prev, draft: d } : prev)}
          onApply={(agent) => { void handleApplySchedule(agent) }}
          onClose={() => setScheduleEdit(null)}
          saving={scheduleEdit?.saving}
          error={scheduleEdit?.error}
          aggregators={aggregators}
        />
        <SchedulerGraphContextMenu
          target={schedulerGraphMenu}
          onClose={handleCloseSchedulerGraphMenu}
          onToggle={handleSchedulerToggle}
          onRunNow={handleSchedulerRunNow}
          onOpenCard={(card) => { setSchedulerGraphMenu(null); handleOpenCardForEdit(card.id) }}
          onEditSchedule={handleSchedulerEditSchedule}
          onLocateMap={handleSchedulerLocateTemplate}
          onDelete={handleSchedulerDelete}
        />
        <ConfirmDialog
          open={deleteConfirmScheduler !== null}
          title={t('scheduled.delete.title')}
          description={deleteConfirmScheduler && deleteConfirmScheduler.instanceCount > 0
            ? t('scheduled.delete.confirmCount', { name: deleteConfirmScheduler.card.id, count: deleteConfirmScheduler.instanceCount })
            : t('scheduled.delete.confirm', { name: deleteConfirmScheduler?.card.id ?? '' })}
          danger
          confirmLabel={t('common.delete')}
          cancelLabel={t('common.cancel')}
          onCancel={() => setDeleteConfirmScheduler(null)}
          onConfirm={() => { void handleConfirmDeleteScheduler() }}
        />
        <NameInputDialog
          open={createNameDialog?.open ?? false}
          title={createNameDialog ? t(`topologyContextMenu.${createNameDialog.kind}NameTitle`) : ''}
          placeholder={createNameDialog ? t(`topologyContextMenu.${createNameDialog.kind}NamePlaceholder`) : ''}
          value={createNameValue}
          onChange={setCreateNameValue}
          onConfirm={handleConfirmCreateName}
          onCancel={handleCancelCreateName}
          confirmLabel={t('common.create')}
          cancelLabel={t('common.cancel')}
        />
        <CardVisualDialog card={visualCard} onClose={() => setVisualCard(null)} onSave={handleSaveVisual} />
        <AssignGoalDialog
          open={assignGoalCard !== null}
          card={assignGoalCard}
          agents={activeProjectAgents}
          submitting={assignGoalSubmitting}
          error={assignGoalError}
          onClose={handleCloseAssignGoal}
          onAssign={handleAssignGoal}
        />
        <AssignGoalDialog
          open={startWorkflowMapId !== null}
          card={startWorkflowMapId ? monoState.cards.find(c => c.id === startWorkflowMapId) ?? null : null}
          agents={activeProjectAgents}
          submitting={startWorkflowSubmitting}
          error={startWorkflowError}
          onClose={handleCloseStartWorkflow}
          onAssign={handleStartWorkflowAssign}
          kindOptions={agentKinds.map(k => ({ kind: k.kind, label: k.displayName }))}
          title={t('workflowGraph.startDialog.title')}
          confirmLabel={t('workflowGraph.startDialog.confirm')}
          submittingLabel={t('workflowGraph.startDialog.submitting')}
        />
        <NameInputDialog
          open={childCard !== null}
          title={t('topologyCardContextMenu.addChild')}
          placeholder={t('topologyContextMenu.cardNamePlaceholder')}
          value={childCardValue}
          onChange={setChildCardValue}
          onConfirm={handleConfirmAddChild}
          onCancel={() => { setChildCard(null); setChildCardValue('') }}
          confirmLabel={t('common.create')}
          cancelLabel={t('common.cancel')}
        />
        <NameInputDialog
          open={renameCard !== null}
          title={t('topologyCardContextMenu.renameTitle')}
          placeholder={t('topologyCardContextMenu.renamePlaceholder')}
          value={renameCardValue}
          onChange={setRenameCardValue}
          onConfirm={handleConfirmRenameCard}
          onCancel={handleCancelRenameCard}
          confirmLabel={t('common.save')}
          cancelLabel={t('common.cancel')}
        />
        <NameInputDialog
          open={tagCard !== null}
          title={t('topologyCardContextMenu.addTagTitle')}
          placeholder={t('topologyCardContextMenu.addTagPlaceholder')}
          value={tagCardValue}
          onChange={setTagCardValue}
          onConfirm={handleConfirmAddTagCard}
          onCancel={handleCancelAddTagCard}
          confirmLabel={t('common.add')}
          cancelLabel={t('common.cancel')}
        />
        <RenameProjectDialog
          open={renameProject !== null}
          currentName={renameProject?.Name ?? ''}
          onClose={() => setRenameProject(null)}
          onSubmit={(name) => {
            if (renameProject) {
              void handleRenameProject(renameProject, name)
            }
            setRenameProject(null)
          }}
        />
        <DeleteConfirmModal
          open={deleteConfirmAgent !== null}
          agentName={deleteConfirmAgent?.Title || deleteConfirmAgent?.DisplayName || 'Unknown'}
          requireTypeName={deleteConfirmAgent?.MemoryMounted === true}
          hasWorktree={!!deleteConfirmAgent?.WorktreeID}
          onClose={() => setDeleteConfirmAgent(null)}
          onConfirm={() => { void confirmDeleteAgent() }}
        />
        <ConfirmDialog
          open={closeConfirmProject !== null}
          title={t('shell.dialog.closeProject.title')}
          description={t('shell.dialog.closeProject.message', { name: closeConfirmProject?.Name ?? '' })}
          confirmLabel={t('shell.dialog.closeProject.confirm')}
          cancelLabel={t('shell.dialog.closeProject.cancel')}
          danger
          loading={closeConfirmLoading}
          onCancel={() => setCloseConfirmProject(null)}
          onConfirm={() => { void confirmCloseProject() }}
        />
        <InstallPreviewDialog
          open={installState.previewOpen}
          manifest={installState.previewManifest}
          loading={installState.loading}
          error={installState.error}
          onConfirm={() => { void localInstall.confirmInstall(client) }}
          onCancel={localInstall.closePreview}
        />
        <ConfirmDialog
          open={clearConfirmAgent !== null}
          title={t('shell.dialog.clearChat.title')}
          description={t('shell.dialog.clearChat.message', { name: agentDisplayName(clearConfirmAgent?.Title, clearConfirmAgent?.DisplayName ?? '') })}
          confirmLabel={t('shell.dialog.clearChat.confirm')}
          cancelLabel={t('shell.dialog.clearChat.cancel')}
          danger
          loading={clearConfirmLoading}
          onCancel={() => setClearConfirmAgent(null)}
          onConfirm={() => { void confirmClearAgentHistory() }}
        />
        <ConfirmDialog
          open={brainMemoryConfirmAgent !== null}
          title={t('shell.dialog.mountMemory.title')}
          description={t('shell.dialog.mountMemory.message', { name: agentDisplayName(brainMemoryConfirmAgent?.Title, brainMemoryConfirmAgent?.DisplayName ?? '') })}
          confirmLabel={t('shell.dialog.mountMemory.confirm')}
          cancelLabel={t('shell.dialog.mountMemory.cancel')}
          loading={brainMemoryMountLoading}
          onCancel={() => setBrainMemoryConfirmAgent(null)}
          onConfirm={() => { void confirmMountMemoryAndOpenBrain() }}
        />
        <ConfirmDialog
          open={deleteConfirmCard !== null}
          title={deleteConfirmCard?.card.type === 'workflow'
            ? t('topologyCardContextMenu.deleteWorkflowTitle')
            : deleteConfirmCard?.card.type === 'task'
              ? t('topologyCardContextMenu.deleteTaskTitle')
              : t('topologyCardContextMenu.deleteTitle')}
          description={deleteConfirmCard?.card.type === 'workflow'
            ? t('topologyCardContextMenu.deleteWorkflowMessage', {
                name: deleteConfirmCard.card.id,
                count: deleteConfirmCard.taskIds.length,
              })
            : deleteConfirmCard?.card.type === 'task'
              ? t('topologyCardContextMenu.deleteTaskMessage', { name: deleteConfirmCard.card.id })
              : t('topologyCardContextMenu.deleteMessage', { name: deleteConfirmCard?.card.id ?? '' })}
          confirmLabel={t('common.delete')}
          cancelLabel={t('common.cancel')}
          danger
          onCancel={() => setDeleteConfirmCard(null)}
          onConfirm={() => { void handleConfirmDeleteCard() }}
        />
        <ConfirmDialog
          open={devModePromptOpen}
          title={t('shell.devModePrompt.title')}
          description={t('shell.devModePrompt.desc')}
          confirmLabel={t('shell.devModePrompt.confirm')}
          onConfirm={() => { setDevModePromptOpen(false); handleOpenSettings('developer') }}
          onCancel={() => setDevModePromptOpen(false)}
        />
        <NewProjectDialog
          key={newProjectDialogMode}
          open={showNewProjectDialog}
          initialMode={newProjectDialogMode}
          onCreated={handleNewProjectCreated}
          onCancel={handleNewProjectCancel}
        />
        <MobileSyncPanel
          open={mobileSyncOpen}
          onClose={handleCloseMobileSync}
          onOpenFrpSettings={() => handleOpenSettings('frp')}
        />
        <input
          ref={importFileInputRef}
          type="file"
          accept=".json"
          style={{ display: 'none' }}
          onChange={async (e) => {
            const file = e.target.files?.[0]
            if (file && activeAgent?.ActorId) {
              await agentOps.importHistory(file, activeAgent.ActorId)
            }
            if (importFileInputRef.current) {
              importFileInputRef.current.value = ''
            }
          }}
        />
        <input
          ref={localInstall.fileInputRef}
          type="file"
          accept=".zip"
          style={{ display: 'none' }}
          onChange={async (e) => {
            const file = e.target.files?.[0]
            if (file) {
              await localInstall.handleBrowserFileSelect(file)
            }
            if (localInstall.fileInputRef.current) {
              localInstall.fileInputRef.current.value = ''
            }
          }}
        />
        <AppOmniboxOverlay
          open={omniboxOpen}
          onClose={() => setOmniboxOpen(false)}
          actions={omniboxActions}
          isMobile={isMobile}
          position={omniboxPosition}
          onSelect={(action) => {
            recordOmniboxUse(action.id)
            action.run()
          }}
        />
        <GlobalFindReplaceOverlay
          open={findReplace.open}
          mode={findReplace.mode}
          onModeChange={(mode) => setFindReplace((prev) => ({ ...prev, mode }))}
          onClose={() => setFindReplace((prev) => ({ ...prev, open: false }))}
          projectId={activeProject?.ProjectID ?? null}
          onOpenFile={(filePath, line, lineEnd, column, columnEnd) =>
            handleOpenFile(filePath, undefined, line, lineEnd, 'source', undefined, column, columnEnd)
          }
        />
        <GuideOverlay
          steps={guideState.steps}
          currentIndex={guideState.currentIndex}
          visible={guideState.visible}
          gateMet={guideState.gateMet}
          categoryLabel={guideState.categoryLabel}
          onNext={() => guideManager.nextStep()}
          onPrev={() => guideManager.prevStep()}
          onComplete={() => guideManager.completeGuide()}
          onSkip={() => guideManager.skipGuide()}
        />
        <DelegatedTooltip />
        <ToolGuideOverlay />
        <AboutOverlay open={aboutOverlayOpen} onClose={handleCloseAboutOverlay} />
        {/* Forced app-registration interception: confirm/cancel overlay for the
            pending registration permission of the active conversation. Own
            provider so it answers regardless of the current content mode. */}
        <InteractionDispatchContext.Provider value={source.dispatchEvent}>
          <RegistrationPermissionOverlay envelopes={source.envelopes} />
        </InteractionDispatchContext.Provider>
        <MiniComposer
          open={miniComposer.open}
          anchor={miniComposer.anchor}
          initialValue={miniComposer.initialValue}
          onClose={handleCloseMiniComposer}
          targetActorId={composerTargetActorId}
        />
        <OnboardingFlow
          hasCoordinator={!!globalCoordinator}
          coordinatorAgentId={globalCoordinator?.Id}
          agentsLoading={agentInfoSnapshot.loading}
          hasProjects={projects.filter(p => !p.System).length > 0}
          hasAgents={allAgents.filter(a => a.AgentKind !== 'coordinator').length > 0}
          hasApps={hasLaunchableApps(registeredApps)}
          onCoordinatorCreated={handleCoordinatorCreated}
        />
      </div>
    </SmoothStreamContext.Provider>
    </AIShellContext.Provider>
    </SchemaOverlayProvider>
    </FileClipboardProvider>
    </EditorKeymapProvider>
  )
}
