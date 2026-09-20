import React, { useCallback, useContext, useRef, useEffect, useLayoutEffect, useState, useMemo } from 'react'
import { createPortal } from 'react-dom'
import { ArrowUp, Bot, Plus, ChevronDown, Check, Square, Mic, Clock, Star, Trash2, X, Paperclip, File, FileText, Terminal, Crop, Loader2, Pause, Play, ShieldCheck, ShieldHalf, ShieldAlert, Zap, Search, Globe, MessageSquare, Users } from 'lucide-react'
import { Events } from '@wailsio/runtime'
import { voiceAPI } from '../voice-api'
import { ProviderIcon, iconKeyForModelRow, iconKeysForModels } from './providerIcons'
import type { ThinkingLevel, ModelUnit, DispatchActivity, ModelSlot } from '../../../gen-clients/system/types'
import type { AgentInfo } from '../hooks/agentInfoStore'
import type { AttachmentEntry, ImageEntry } from '../../../gen-types/agent.chat'
import { startScreenshot } from '../../../application/wails-bridge'
import { useViewportMode } from '../../../application/useViewportMode'
import { isCapacitor } from '../../../application/runtime'
import { useI18n } from '../../../i18n'
import { useBrowserOverlay } from '../browserOverlay'
import { emitInteraction } from '../../../application/telemetry'
import { GuideIds } from '../guide-ids'
import { useSlashCommands } from '../hooks/useSlashCommands'
import { useCardMention, useFileMention, useAgentMention, useBrowserMention, parseCardMention, parseFileMention, parseAgentMention, parseBrowserMention, type BrowserMentionItem } from '../hooks/useCardMention'
import { useInsiderAccess } from '../hooks/useInsiderAccess'
import type { MonoCardListItem } from '../../../gen-types/project.wiki.part1'
import type { AppOmniboxAction } from '../hooks/useAppOmnibox'
import { useDropdownVerticalFit } from '../hooks/useDropdownVerticalFit'
import { useLongPress } from '../hooks/useLongPress'
import { getComposerMedia, setComposerMedia } from '../hooks/composerDraftStore'
export { useDropdownVerticalFit }
import { AppOmniboxResults, filterOmniboxActions, orderOmniboxActions } from './AppOmniboxOverlay'
import { CardIcon } from './CardIcon'
import { skillLabel } from './omniboxLabels'
import type { SlotRoute } from '../hooks/modelSlot'
import { CooldownProviderOption, DispatchActivityBadge } from './CooldownProviderOption'
import { isCoolingDown, isDisabled, formatCountdown, healthReasonLabelKey } from '../hooks/healthStatus'
import { AIShellContext } from '../context/AIShellContext'
import { ProviderSlotMenu } from './ProviderSlotMenu'
import type { ProviderSlotMenuSlots, SlotName } from './ProviderSlotMenu'
import './AIComposer.css'

export interface ProviderOption {
  id: string
  label: string
  subtitle?: string
  icon?: string
  unit?: ModelUnit
  /** Provider endpoint (base URL) — authoritative input for brand icon resolution. */
  endpoint?: string
  /** Epoch seconds (from backend CooldownUntil projection). */
  cooldownUntil?: number
  healthState?: 'healthy' | 'cooling_down' | 'disabled'
  healthReason?: string
  lastFailureAt?: number
  recoveryMode?: string
  /** Aggregator-local dispatch state (trying/in_use), projected from the
   *  aggregator's status unit. Purely structural — health/cooldown never
   *  comes from here (global cooldown source is authoritative). */
  dispatchActivity?: DispatchActivity
}

/** A reference entry: a parent aggregator's status unit whose `aggregatorID`
 *  points to a child aggregator. These are NOT selectable options — they are
 *  info rows showing the child aggregator's name and nested health badge.
 *  Clicking a ref soft-pins the child's first available unit through the ROOT
 *  aggregator (same as clicking a nested unit); the chevron expands an inline
 *  tree. Internal aggregators are never promoted to the active route. */
export interface RefOption {
  /** The child aggregator's config ID (from `aggregatorID` on the status unit). */
  aggregatorId: string
  /** Display name resolved from the aggregator list (falls back to the ID). */
  label: string
  healthState?: 'healthy' | 'cooling_down' | 'disabled'
  healthReason?: string
  cooldownUntil?: number
  lastFailureAt?: number
  recoveryMode?: string
  /** Deepest active dispatch activity projected from the child aggregator
   *  (backend bubbles child-layer in-flight state up to this ref entry). */
  dispatchActivity?: DispatchActivity
}

/** Model labels for a route's icon stack: direct models first, then the leaf
 * models contributed by nested child aggregators (recursive, cycle-safe).
 * Every aggregator is a top-level group, so children are found by routeId. */
export function routeModelLabels(groups: ProviderGroup[], routeId: string, seen: Set<string> = new Set()): string[] {
  if (seen.has(routeId)) return []
  seen.add(routeId)
  const g = groups.find(x => x.routeId === routeId)
  if (!g) return []
  const out = g.models.map(m => m.label)
  for (const r of g.refs ?? []) out.push(...routeModelLabels(groups, r.aggregatorId, seen))
  return out
}

/** A single entry in a provider group's ordered list. Preserves the
 *  original aggregator pool order so concrete models and child-aggregator
 *  references interleave naturally instead of being grouped by type. */
export type ProviderItem =
  | { kind: 'model'; option: ProviderOption }
  | { kind: 'ref'; option: RefOption }

/** A route group in the two-level model selector: the system/auto pool or a
 *  custom aggregator. Clicking the header selects the route (strategy);
 *  clicking a model pins it with this route as failover. */
export interface ProviderGroup {
  /** 'system' for the auto/system pool, else a custom aggregator config ID. */
  routeId: string
  label: string
  isAuto: boolean
  /** Always true for custom aggregators: every aggregator is exposed as a
   *  top-level route so it can be selected independently, even when nested. */
  isRoot?: boolean
  models: ProviderOption[]
  /** Reference entries (child aggregator links). Only present on custom
   *  aggregator groups. */
  refs?: RefOption[]
  /** Ordered entries interleaving models and refs in original pool order.
   *  The dropdown renderer iterates this so display order matches the
   *  aggregator's configured pool, not a type-partitioned grouping. */
  items: ProviderItem[]
  /** Active dispatch activity bubbled up from all descendants (direct models
   *  and nested refs) so the route header can surface retry/in-use state without
   *  expanding the child tree. */
  bubbledActivity?: DispatchActivity
}

/** Right-click "assign models to agent slots" menu wiring for the provider
 *  button. Optional: when omitted the button keeps its left-click-only
 *  behavior and a right-click opens nothing. The shape mirrors the props
 *  ProviderSlotMenu consumes, minus the open/onClose the composer owns. */
export interface ComposerSlotMenuConfig {
  /** Current value of each of the five agent model slots. */
  slots: ProviderSlotMenuSlots
  /** Grouped provider options (composer dropdown source). */
  groups?: ProviderGroup[]
  /** Flat provider options (composer legacy source). */
  options?: ProviderOption[]
  /** Called with the slot name and new slot value on each pick. The parent
   *  owns persisting the change; ProviderSlotMenu never writes state itself. */
  onSelectSlot: (slot: SlotName, slotValue: ModelSlot) => void
}

export interface ComposerHistoryItem {
  id: string
  text: string
  timestamp: number
  isFavorite: boolean
}

export interface ComposerBundleTool {
  /** Callable id, e.g. "project.grep" */
  id: string
  /** Friendly display name derived from the callable id */
  name: string
  description?: string
  params?: ComposerBundleToolParam[]
}

export interface ComposerBundleToolParam {
  name: string
  type: string
  required?: boolean
  description?: string
}

export interface ComposerBadge {
  icon: string
  color?: string
  title: string
  /** Friendly display title (falls back to title when not set) */
  label?: string
  onClose?: () => void
  /** Config-mounted bundle: rendered icon-only (no title, no close) and
   *  collapsed together with all other config badges into a single capsule. */
  configGroup?: boolean
  /** Tools contributed by this bundle, surfaced in the capsule hover tooltip.
   *  Only populated for configGroup badges. */
  tools?: ComposerBundleTool[]
  /** Click action for the badge body (registered via modeClickRegistry, e.g.
   *  the workflow mode's "open workflow view and locate" action). The close /
   *  exit controls remain independent. */
  onClick?: () => void
  /** Right-click action for the badge body; receives the cursor position so
   *  the parent can open a context menu (e.g. mode/bundle details). */
  onContextMenu?: (x: number, y: number) => void
  /** Stale mount: the target agent is unloaded/removed. Renders red text so
   *  the dead channel stays visible and removable via the close X. */
  stale?: boolean
}

export type PermissionMode = 'permission' | 'yolo' | 'allow-all' | 'auto' | 'autopilot'

const PERMISSION_MODE_IDS = ['permission', 'yolo', 'allow-all', 'auto', 'autopilot'] as const

/** Switching to these modes requires an explicit confirmation click: they
 * auto-approve tool calls without any guard (yolo additionally blocks
 * ask_user and auto-approves plan/goal submits). */
const DANGEROUS_PERMISSION_MODES: ReadonlySet<PermissionMode> = new Set(['yolo', 'allow-all'])

/** Maps a wire/persisted permission mode to its canonical value; unknown or
 * empty input returns null so callers fall back to their own default. */
export function normalizePermissionMode(mode: string | null | undefined): PermissionMode | null {
  if (mode && (PERMISSION_MODE_IDS as readonly string[]).includes(mode)) return mode as PermissionMode
  return null
}

interface AIComposerProps {
  value: string
  onChange: (v: string) => void
  onSend: () => void
  onSubmit?: (text: string, attachments: AttachmentEntry[], images: ImageEntry[]) => void
  isStreaming?: boolean
  isPaused?: boolean
  isWaiting?: boolean
  isPausing?: boolean
  onStop?: () => void
  onPause?: () => void
  onResume?: () => void
  disabled?: boolean
  placeholder?: string
  providers?: ProviderOption[]
  activeProviderId?: string
  onProviderChange?: (id: string) => void
  /** Grouped two-level selector (conversation top bar). When present and
   *  non-empty, the dropdown renders routes + per-route models instead of the
   *  flat provider list. */
  groups?: ProviderGroup[]
  activeRoute?: SlotRoute
  currentUnit?: ModelUnit | null
  onSelectRoute?: (routeId: string) => void
  onSelectUnit?: (routeId: string, option: ProviderOption) => void
  activeThinkingLevel?: ThinkingLevel | null
  onThinkingLevelChange?: (level: ThinkingLevel) => void
  onVoiceResult?: (text: string) => void
  history?: ComposerHistoryItem[]
  onHistoryChange?: (items: ComposerHistoryItem[]) => void
  footer?: React.ReactNode
  leftSlot?: React.ReactNode
  middleSlot?: React.ReactNode
  rightSlot?: React.ReactNode
  dropdownSlot?: React.ReactNode
  aboveLeftSlot?: React.ReactNode
  aboveRightSlot?: React.ReactNode
  avatarBarSlot?: React.ReactNode
  badges?: ComposerBadge[]
  /** Whether config-group badge bundles (ai-composer-badge-group) are visible.
   *  Regular mounted-mode badges are always shown. Defaults to false. */
  configBadgesVisible?: boolean
  isMobile?: boolean
  permissionMode?: PermissionMode
  onPermissionModeChange?: (mode: PermissionMode) => void
  /** Account-level default permission mode. When provided alongside
   *  `onGlobalPermissionModeChange`, the dropdown shows a "Current / Global"
   *  tab bar. `permissionMode` is the active agent's actual mode; this is the
   *  account default. Omit both to hide the tab bar (backward compat). */
  globalPermissionMode?: PermissionMode
  onGlobalPermissionModeChange?: (mode: PermissionMode) => void
  componentActions?: AppOmniboxAction[]
  /** Open the global command/search omnibox. */
  onOpenOmnibox?: () => void
  /** Delete the current agent after an inline confirmation. */
  onDeleteCurrentAgent?: () => void
  /** Create a new agent (mobile-only entry in the add menu). */
  onCreateAgent?: () => void
  /** Invoked when the input matches a builtin mode token ("/<name>") after a
   *  short settle. The parent mounts the mode and clears the input — no submit. */
  onEnterMode?: (cardId: string) => void
  /** Invoked when the user selects a card from the #-mention dropdown. The
   *  composer removes the '#query' fragment from the input first; the parent
   *  receives the selected card's id (which is also its display title). */
  onMentionCard?: (cardId: string, cardTitle: string) => void
  /** Invoked when the user selects a file from the @-mention dropdown. The
   *  composer removes the '@query' fragment from the input first; the parent
   *  receives the project-relative file path. */
  onMentionFile?: (path: string) => void
  /** Invoked when the user selects an agent from the @-mention dropdown
   *  (loaded workspace agents from the agentListStore, self excluded, ranked
   *  above file results). The composer removes the '@query' fragment from the
   *  input first; the parent receives the agent's stable Id, display name,
   *  and ActorId and mounts the agent-chat:<agentId> conversable tag. No
   *  submit. */
  onMentionAgent?: (agentId: string, displayName: string, actorId: string) => void
  /** Invoked when the user selects an independent browser from the %-mention
   *  dropdown (open tab-mode browser-manager instances, ranked by name). The
   *  composer removes the '%query' fragment from the input first; the parent
   *  receives the instance ID, display name, and live URL and mounts the
   *  browser-chat:<instanceId> tag. No submit. The browser badge is a pure
   *  display reflection of the per-agent backend mount list. */
  onMentionBrowser?: (instanceId: string, name: string, url: string) => void
  /** The active agent's actor id. Scopes the slash-mode interception so a
   *  pending mode-enter (the 250ms settle) cannot land on a different agent
   *  after a switch. Badges remain a pure display reflection of the mounts. */
  agentActorId?: string | null
  projectId?: string | null
  /** When provided, the composer is in drawer mode (not conversation). A
   *  "return to conversation" icon is shown in the left tools group, right of
   *  the permission mode selector; clicking it invokes this callback. */
  onReturnToConversation?: () => void
  /** Right-click menu config for the provider button. When provided, a right
   *  click opens ProviderSlotMenu above the button (left click is unchanged). When
   *  omitted, right-click opens nothing (e.g. pages with no active agent). */
  slotMenu?: ComposerSlotMenuConfig
}

// Global default: dual-line mode is enabled by default
export const DEFAULT_DUAL_LINE_MODE = true

function genId(): string {
  return Math.random().toString(36).slice(2,9)
}

export function fmtTime(ts: number): string {
  const d = new Date(ts)
  const h = String(d.getHours()).padStart(2, '0')
  const m = String(d.getMinutes()).padStart(2, '0')
  return `${h}:${m}`
}

export const MAX_HISTORY_SIZE = 100

export function sortHistory(items: ComposerHistoryItem[]): ComposerHistoryItem[] {
  return [...items].sort((a, b) => {
    if (a.isFavorite !== b.isFavorite) return a.isFavorite ? -1 : 1
    return b.timestamp - a.timestamp
  })
}

export function limitHistory(items: ComposerHistoryItem[]): ComposerHistoryItem[] {
  return sortHistory(items).slice(0, MAX_HISTORY_SIZE)
}

export function addHistoryEntry(items: ComposerHistoryItem[], text: string): ComposerHistoryItem[] {
  const existing = items.find(h => h.text === text)
  const next: ComposerHistoryItem[] = existing
    ? items.map(h => h.text === text ? { ...h, timestamp: Date.now() } : h)
    : [...items, { id: genId(), text, timestamp: Date.now(), isFavorite: false }]
  return limitHistory(next)
}

export function toggleHistoryFavorite(items: ComposerHistoryItem[], id: string): ComposerHistoryItem[] {
  return limitHistory(items.map(h =>
    h.id === id ? { ...h, isFavorite: !h.isFavorite, timestamp: Date.now() } : h
  ))
}

export function removeHistoryItem(items: ComposerHistoryItem[], id: string): ComposerHistoryItem[] {
  return limitHistory(items.filter(h => h.id !== id))
}

// Lazy canvas for text measurement
let _canvas: HTMLCanvasElement | null = null
function measureTextWidth(text: string, font: string): number {
  if (!_canvas) _canvas = document.createElement('canvas')
  const ctx = _canvas.getContext('2d')!
  ctx.font = font
  return ctx.measureText(text).width
}

function getComputedFont(el: HTMLElement): string {
  const s = getComputedStyle(el)
  return `${s.fontWeight} ${s.fontSize} ${s.fontFamily}`
}

export const AIComposer: React.FC<AIComposerProps> = ({
  value,
  onChange,
  onSend,
  onSubmit,
  isStreaming = false,
  isPaused = false,
  isWaiting = false,
  isPausing = false,
  onStop,
  onPause,
  onResume,
  disabled = false,
  placeholder = '',
  providers = [],
  activeProviderId,
  onProviderChange,
  groups,
  activeRoute,
  currentUnit,
  onSelectRoute,
  onSelectUnit,
  activeThinkingLevel,
  onThinkingLevelChange,
  onVoiceResult,
  history: historyProp,
  onHistoryChange,
  footer,
  leftSlot,
  middleSlot,
  rightSlot,
  dropdownSlot,
  aboveLeftSlot,
  aboveRightSlot,
  avatarBarSlot,
  badges,
  configBadgesVisible = false,
  permissionMode = 'permission',
  onPermissionModeChange,
  globalPermissionMode,
  onGlobalPermissionModeChange,
  componentActions = [],
  onOpenOmnibox,
  onDeleteCurrentAgent,
  onCreateAgent,
  onEnterMode,
  onMentionCard,
  onMentionFile,
  onMentionAgent,
  onMentionBrowser,
  agentActorId,
  projectId,
  isMobile: isMobileProp,
  onReturnToConversation,
  slotMenu,
}) => {
  const { t } = useI18n()
  // Most dangerous first: unguarded auto-approve modes at the top, guarded
  // modes in the middle, confirm-per-call last.
  const permissionModes: { id: PermissionMode; label: string; desc: string; Icon: typeof ShieldCheck }[] = [
    { id: 'yolo', label: t('composer.permission.yolo'), desc: t('composer.permission.yoloDesc'), Icon: Zap },
    { id: 'allow-all', label: t('composer.permission.allowAll'), desc: t('composer.permission.allowAllDesc'), Icon: Bot },
    { id: 'autopilot', label: t('composer.permission.autopilot'), desc: t('composer.permission.autopilotDesc'), Icon: ShieldAlert },
    { id: 'auto', label: t('composer.permission.bypass'), desc: t('composer.permission.bypassDesc'), Icon: ShieldHalf },
    { id: 'permission', label: t('composer.permission.permission'), desc: t('composer.permission.permissionDesc'), Icon: ShieldCheck },
  ]
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const toolbarRef = useRef<HTMLDivElement>(null)
  const providerRef = useRef<HTMLDivElement>(null)
  const providerDropdownRef = useRef<HTMLDivElement>(null)
  const addDropdownRef = useRef<HTMLDivElement>(null)
  const permissionDropdownRef = useRef<HTMLDivElement>(null)
  const historyDropdownRef = useRef<HTMLDivElement>(null)
  const [providerOpen, setProviderOpen] = useState(false)
  // Whether the provider button's right-click slot menu is open. Owned here so
  // the overlay registration below can hide embedded native browser windows;
  // dismissal is delegated to ProviderSlotMenu's useMenuDismiss via onClose.
  const [slotMenuOpen, setSlotMenuOpen] = useState(false)
  // Per-second display tick — active while the provider dropdown is open OR
  // the selected unit is cooling_down (so the button countdown stays live).
  // Updates countdown/progress overlays without sending network requests.
  const [providerNowMs, setProviderNowMs] = useState(() => Date.now())

  // Compute the selected option's health early so the tick effect can decide
  // whether to run without depending on providerNowMs (which would restart the
  // interval every second).
  const useGroups = !!(groups && groups.length > 0)
  const activeProvider = providers.find(p => p.id === activeProviderId)
  // The authoritative active unit: pinned unit wins over the aggregator-resolved
  // β channel (currentUnit is not updated on pin, only on route change).
  const activeUnitForHealth = activeRoute?.kind === 'unit' ? activeRoute.unit : currentUnit
  const selectedHealthOption = (() => {
    if (useGroups && groups && activeUnitForHealth) {
      for (const g of groups) {
        for (const m of g.models) {
          if (m.unit?.model === activeUnitForHealth.model &&
              (m.unit?.provider ?? '') === (activeUnitForHealth.provider ?? '')) {
            return m
          }
        }
      }
    }
    return activeProvider ?? null
  })()
  const hasSelectedCooldown = selectedHealthOption?.healthState === 'cooling_down'

  useEffect(() => {
    if (!providerOpen && !hasSelectedCooldown) return
    if (providerOpen) {
      window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
    }
    setProviderNowMs(Date.now())
    const id = setInterval(() => setProviderNowMs(Date.now()), 1000)
    return () => clearInterval(id)
  }, [providerOpen, hasSelectedCooldown])
  // Re-fetch actor state when a visible cooldown deadline elapses.
  const cooldownExpiryFiredRef = useRef(false)
  useEffect(() => {
    if (!providerOpen || !groups) return
    const hasActiveCooldown = groups.some(g => g.models.some(m =>
      m.healthState === 'cooling_down' && m.cooldownUntil && m.cooldownUntil * 1000 > providerNowMs
    ))
    const hasExpiredCooldown = groups.some(g => g.models.some(m =>
      m.healthState === 'cooling_down' && m.cooldownUntil && m.cooldownUntil * 1000 <= providerNowMs
    ))
    if (hasActiveCooldown) {
      cooldownExpiryFiredRef.current = false
    }
    if (hasExpiredCooldown && !cooldownExpiryFiredRef.current) {
      cooldownExpiryFiredRef.current = true
      window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
    }
  }, [providerNowMs, providerOpen, groups])
  const [expandedRoutes, setExpandedRoutes] = useState<Set<string>>(new Set())
  const [expandedTreeRefs, setExpandedTreeRefs] = useState<Set<string>>(new Set())
  const [permissionOpen, setPermissionOpen] = useState(false)
  const [permissionTab, setPermissionTab] = useState<'current' | 'global'>('current')
  const [isMultiline, setIsMultiline] = useState(DEFAULT_DUAL_LINE_MODE)
  const [isRecording, setIsRecording] = useState(false)
  const isRecordingRef = useRef(false)
  const pttGestureRef = useRef<string | null>(null)
  const [isEditing, setIsEditing] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [groupTooltipOpen, setGroupTooltipOpen] = useState(false)
  const [groupTooltipRect, setGroupTooltipRect] = useState<DOMRect | null>(null)
  const groupTooltipRef = useRef<HTMLSpanElement>(null)
  const groupTooltipTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Shared hover handlers for both the capsule and the (portaled) tooltip:
  // entering either cancels the close timer; leaving either starts it. This
  // keeps the overlay open while the pointer travels from the capsule into it.
  const openGroupTooltip = useCallback(() => {
    if (groupTooltipTimer.current) clearTimeout(groupTooltipTimer.current)
    groupTooltipTimer.current = setTimeout(() => {
      if (groupTooltipRef.current) {
        setGroupTooltipRect(groupTooltipRef.current.getBoundingClientRect())
      }
      setGroupTooltipOpen(true)
    }, 250)
  }, [])
  const scheduleCloseGroupTooltip = useCallback(() => {
    if (groupTooltipTimer.current) clearTimeout(groupTooltipTimer.current)
    groupTooltipTimer.current = setTimeout(() => setGroupTooltipOpen(false), 120)
  }, [])
  useEffect(() => () => {
    if (groupTooltipTimer.current) clearTimeout(groupTooltipTimer.current)
  }, [])
  const [attachments, setAttachments] = useState<AttachmentEntry[]>([])
  const [images, setImages] = useState<ImageEntry[]>([])

  // Images/attachments belong to the composer (agent) they were inserted in,
  // not to this long-lived component instance: on an agent switch, stash the
  // outgoing agent's media and restore the incoming agent's draft media. The
  // cleanup also covers unmount/remount of the shared composerFrame across
  // layout modes. The workspace composer (no agentActorId) mirrors the text
  // draft behavior: its media is discarded on switch, never stashed.
  const imagesRef = useRef(images)
  imagesRef.current = images
  const attachmentsRef = useRef(attachments)
  attachmentsRef.current = attachments
  useEffect(() => {
    if (!agentActorId) {
      setImages([])
      setAttachments([])
      return
    }
    const media = getComposerMedia(agentActorId)
    setImages(media.images)
    setAttachments(media.attachments)
    return () => {
      setComposerMedia(agentActorId, { images: imagesRef.current, attachments: attachmentsRef.current })
    }
  }, [agentActorId])
  const shellCtx = useContext(AIShellContext)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [dragOver, setDragOver] = useState(false)
  const [isStopping, setIsStopping] = useState(false)
  const [internalHistory, setInternalHistory] = useState<ComposerHistoryItem[]>([])
  const [addMenuOpen, setAddMenuOpen] = useState(false)
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)
  const [permissionConfirm, setPermissionConfirm] = useState<{ mode: PermissionMode; apply: () => void } | null>(null)

  // Slash command menu: shown while the user is typing a '/'-prefixed input.
  // Spaces are tolerated so multi-word component names ("Web Search") stay
  // matchable — the menu only renders when the full query still matches an
  // action, so typing command args naturally empties it. Selecting a command
  // fills the composer with "/name " and the user submits via Enter, which
  // flows through the existing chat.submit -> slashcmd.Dispatch path.
  const slashActive = value.startsWith('/')
  const slashQuery = slashActive ? value.slice(1) : ''
  const { commands: slashCommands, skills: slashSkills, modes: builtinModes } = useSlashCommands(true, projectId)
  const slashCommandActions = slashCommands.map((command) => ({
    id: `command:${command.Name}`,
    section: 'command' as const,
    label: `/${command.Name}`,
    description: command.ShortHelp,
    keywords: [command.Name],
    score: 0,
    run: () => onChange(`/${command.Name} `),
  }))
  const slashSkillActions = slashSkills.map((skill) => ({
    id: `skill:${skill.Name}`,
    section: 'skill' as const,
    label: skillLabel(skill.Name),
    description: skill.ShortHelp,
    icon: skill.Name.startsWith('ext-skill:') ? <Globe size={16} /> : <Zap size={16} />,
    keywords: [skill.Name],
    score: 0,
    run: () => onChange(`/${skill.Name} `),
  }))
  const visibleSlashActions = slashActive
    ? orderOmniboxActions(filterOmniboxActions([...slashCommandActions, ...slashSkillActions, ...componentActions], slashQuery))
    : []
  const [slashSelected, setSlashSelected] = useState(0)
  const [slashDismissed, setSlashDismissed] = useState(false)
  useEffect(() => {
    setSlashSelected(0)
  }, [slashQuery])
  useEffect(() => {
    if (slashActive) setSlashDismissed(false)
  }, [slashActive])
  const slashMenuOpen = slashActive && !slashDismissed && visibleSlashActions.length > 0

  // Card mention (#) interception: when the input ends in an active #-token,
  // the text after '#' becomes a debounced query over the current project's
  // cards. Results render in the same dropdown style as the slash-command menu
  // (AppOmniboxResults). Selecting a card removes the '#query' fragment and
  // reports the card to the parent via onMentionCard — no submit.
  const mention = parseCardMention(value)
  const mentionActive = !!mention
  const mentionQuery = mention?.query ?? ''
  const { results: mentionResults } = useCardMention(mentionActive, mentionQuery, projectId)
  const onMentionCardRef = useRef(onMentionCard)
  onMentionCardRef.current = onMentionCard
  const selectCardMention = useCallback((card: MonoCardListItem) => {
    const m = parseCardMention(value)
    if (!m) return
    // Drop the trailing '#query' token (the '#' plus the query text).
    const removed = value.slice(0, m.start) + value.slice(m.start + 1 + m.query.length)
    onChange(removed.replace(/\s+$/, ''))
    onMentionCardRef.current?.(card.Id, card.Id)
  }, [value, onChange])
  const mentionActions = useMemo<AppOmniboxAction[]>(() => {
    if (!mentionActive) return []
    return mentionResults.map((card) => ({
      id: `card:${card.Id}`,
      section: 'card' as const,
      label: card.Id,
      icon: <FileText size={16} />,
      keywords: [card.Id],
      score: 0,
      run: () => selectCardMention(card),
    }))
  }, [mentionActive, mentionResults, selectCardMention])
  const [mentionSelected, setMentionSelected] = useState(0)
  const [mentionDismissed, setMentionDismissed] = useState(false)
  useEffect(() => {
    setMentionSelected(0)
  }, [mentionQuery])
  useEffect(() => {
    if (mentionActive) setMentionDismissed(false)
  }, [mentionActive])
  const mentionMenuOpen = mentionActive && !mentionDismissed && mentionActions.length > 0

  // File mention ($) interception: mirrors the card mention, but the query
  // searches project files (project.glob). Selecting a file reports its path
  // via onMentionFile — no auto-submit.
  const fileMention = parseFileMention(value)
  const fileMentionActive = !!fileMention
  const fileMentionQuery = fileMention?.query ?? ''
  const { results: fileMentionResults } = useFileMention(fileMentionActive, fileMentionQuery, projectId)
  const onMentionFileRef = useRef(onMentionFile)
  onMentionFileRef.current = onMentionFile
  const selectFileMention = useCallback((path: string) => {
    const m = parseFileMention(value)
    if (!m) return
    // Drop the trailing '$query' token (the '$' plus the query text).
    const removed = value.slice(0, m.start) + value.slice(m.start + 1 + m.query.length)
    onChange(removed.replace(/\s+$/, ''))
    onMentionFileRef.current?.(path)
  }, [value, onChange])
  const fileMentionActions = useMemo<AppOmniboxAction[]>(() => {
    if (!fileMentionActive) return []
    return fileMentionResults.map((path) => ({
      id: `file:${path}`,
      section: 'file' as const,
      label: path,
      icon: <File size={16} />,
      keywords: [path],
      score: 0,
      run: () => selectFileMention(path),
    }))
  }, [fileMentionActive, fileMentionResults, selectFileMention])
  const [fileMentionSelected, setFileMentionSelected] = useState(0)
  const [fileMentionDismissed, setFileMentionDismissed] = useState(false)
  useEffect(() => {
    setFileMentionSelected(0)
  }, [fileMentionQuery])
  useEffect(() => {
    if (fileMentionActive) setFileMentionDismissed(false)
  }, [fileMentionActive])
  const fileMentionMenuOpen = fileMentionActive && !fileMentionDismissed && fileMentionActions.length > 0

  // Agent mention (@) interception: the agent store is a synchronous
  // client-side snapshot, so there is no debounce; ranking comes from the
  // pure rankAgentMentionResults helper (loaded agents only, current agent
  // excluded via its ActorId). Selecting an agent fires onMentionAgent so the
  // parent mounts the agent-chat:<agentId> conversable tag. No auto-submit.
  const agentMention = parseAgentMention(value)
  const agentMentionActive = !!agentMention
  const agentMentionQuery = agentMention?.query ?? ''
  const agentMentionItems = useAgentMention(agentMentionActive, agentMentionQuery, agentActorId)
  const onMentionAgentRef = useRef(onMentionAgent)
  onMentionAgentRef.current = onMentionAgent
  const selectAgentMention = useCallback((agent: AgentInfo) => {
    const m = parseAgentMention(value)
    if (!m) return
    // Drop the trailing '@query' token exactly like the file token above.
    const removed = value.slice(0, m.start) + value.slice(m.start + 1 + m.query.length)
    onChange(removed.replace(/\s+$/, ''))
    onMentionAgentRef.current?.(agent.Id, agent.DisplayName || agent.Title || agent.Id, agent.ActorId)
  }, [value, onChange])
  const agentMentionActions = useMemo<AppOmniboxAction[]>(() => {
    if (!agentMentionActive) return []
    return agentMentionItems.map((agent) => {
      // Description carries the project NAME (the primary disambiguator when
      // agents share a display name across projects) and the agent's Title
      // when it has one (a real goal/card title distinct from its display
      // name — HasTitle is false when Title fell back to DisplayName), then
      // the agent kind.
      const desc = [
        agent.ProjectName,
        agent.HasTitle ? agent.Title : null,
        agent.AgentKind,
      ].filter(Boolean).join(' · ')
      return {
        id: `agent:${agent.Id || agent.ActorId}`,
        section: 'agent' as const,
        label: agent.DisplayName || agent.Title || agent.Id,
        icon: <Bot size={16} />,
        description: desc,
        keywords: [agent.DisplayName, agent.Title || '', agent.AgentKind, agent.ProjectName, agent.Id, 'agent'].filter(Boolean),
        score: 0,
        run: () => selectAgentMention(agent),
      }
    })
  }, [agentMentionActive, agentMentionItems, selectAgentMention])
  const [agentMentionSelected, setAgentMentionSelected] = useState(0)
  const [agentMentionDismissed, setAgentMentionDismissed] = useState(false)
  useEffect(() => {
    setAgentMentionSelected(0)
  }, [agentMentionQuery])
  useEffect(() => {
    if (agentMentionActive) setAgentMentionDismissed(false)
  }, [agentMentionActive])
  const agentMentionMenuOpen = agentMentionActive && !agentMentionDismissed && agentMentionActions.length > 0

  // Browser mention (%) interception: mirrors the agent mention, but the
  // candidates come from browser-manager — open independent (right-panel tab)
  // browser instances only. Selecting one fires onMentionBrowser so the
  // parent mounts the browser-chat:<instanceId> tag and renders a browser
  // badge. No auto-submit. Insider-and-above only: without access the '%'
  // trigger stays inert (no dropdown).
  const insiderAccess = useInsiderAccess()
  const browserMention = parseBrowserMention(value)
  const browserMentionActive = !!browserMention && insiderAccess
  const browserMentionQuery = browserMention?.query ?? ''
  const { results: browserMentionItems } = useBrowserMention(browserMentionActive, browserMentionQuery)
  const onMentionBrowserRef = useRef(onMentionBrowser)
  onMentionBrowserRef.current = onMentionBrowser
  const selectBrowserMention = useCallback((item: BrowserMentionItem) => {
    const m = parseBrowserMention(value)
    if (!m) return
    // Drop the trailing '%query' token (the '%' plus the query text).
    const removed = value.slice(0, m.start) + value.slice(m.start + 1 + m.query.length)
    onChange(removed.replace(/\s+$/, ''))
    onMentionBrowserRef.current?.(item.instanceId, item.name, item.url)
  }, [value, onChange])
  const browserMentionActions = useMemo<AppOmniboxAction[]>(() => {
    if (!browserMentionActive) return []
    return browserMentionItems.map((item) => ({
      id: `browser:${item.instanceId}`,
      section: 'browser' as const,
      label: item.name,
      icon: <Globe size={16} />,
      description: item.url,
      keywords: [item.name, item.url, item.instanceId, 'browser'].filter(Boolean),
      score: 0,
      run: () => selectBrowserMention(item),
    }))
  }, [browserMentionActive, browserMentionItems, selectBrowserMention])
  const [browserMentionSelected, setBrowserMentionSelected] = useState(0)
  const [browserMentionDismissed, setBrowserMentionDismissed] = useState(false)
  useEffect(() => {
    setBrowserMentionSelected(0)
  }, [browserMentionQuery])
  useEffect(() => {
    if (browserMentionActive) setBrowserMentionDismissed(false)
  }, [browserMentionActive])
  const browserMentionMenuOpen = browserMentionActive && !browserMentionDismissed && browserMentionActions.length > 0

  // Slash-mode interception: when the input is exactly "/<mode-name>" and
  // matches a builtin mode, settle briefly then fire onEnterMode so the parent
  // mounts the mode and clears the input — no submit, no slash dispatch.
  const matchedModeCardId = slashActive ? builtinModes.find((m) => m.Name === slashQuery)?.CardId ?? null : null
  const onEnterModeRef = useRef(onEnterMode)
  onEnterModeRef.current = onEnterMode
  // onEnterMode rebinds to whichever agent is currently active (it closes over
  // the active agent's mount). A deferred mode-enter must therefore be scoped to
  // the agent that was active when the mode token was typed: if the user
  // switches agents during the 250ms settle, the pending enter is dropped so it
  // never mounts the mode on the NEW agent's backend (which would leak
  // workflow/goal mode across agents). agentActorId only changes on a real
  // switch, so depending on it cancels the timer exactly then, not every render.
  const agentActorIdRef = useRef(agentActorId)
  agentActorIdRef.current = agentActorId
  const enterMatchedMode = useCallback(() => {
    if (!matchedModeCardId || !onEnterModeRef.current) return false
    onEnterModeRef.current(matchedModeCardId)
    return true
  }, [matchedModeCardId])
  useEffect(() => {
    if (!matchedModeCardId) return
    const startedAgent = agentActorId
    const timer = setTimeout(() => {
      // Guard the gap between a switch and the effect cleanup commit.
      if (agentActorIdRef.current !== startedAgent) return
      enterMatchedMode()
    }, 250)
    return () => clearTimeout(timer)
  }, [matchedModeCardId, enterMatchedMode, agentActorId])

  useBrowserOverlay(addMenuOpen || providerOpen || permissionOpen || historyOpen || slashMenuOpen || mentionMenuOpen || fileMentionMenuOpen || agentMentionMenuOpen || browserMentionMenuOpen || deleteConfirmOpen || permissionConfirm !== null || slotMenuOpen)
  const viewportMode = useViewportMode()
  const isMobile = isMobileProp ?? viewportMode === 'mobile'
  const [avatarBarVisible, setAvatarBarVisible] = useState(() => {
    try { return JSON.parse(localStorage.getItem('sporemind:composer:avatarBarVisible') || 'true') }
    catch { return true }
  })

  useEffect(() => {
    try { localStorage.setItem('sporemind:composer:avatarBarVisible', JSON.stringify(avatarBarVisible)) }
    catch { /* ignore */ }
  }, [avatarBarVisible])

  useEffect(() => {
    if (isMobile) {
      setAvatarBarVisible(true)
    }
  }, [isMobile])

  // listen for screenshot completion from screenshot window
  useEffect(() => {
    const unsub = Events.On('screenshot-completed', (event) => {
      const data = event.data as string
      if (data) {
        setImages(prev => [...prev, { Url: data, MimeType: 'image/png' }])
      }
    })
    return () => { unsub() }
  }, [])

  // Keep the provider dropdown inside the viewport when the composer is narrow.
  useLayoutEffect(() => {
    if (!providerOpen) return
    const provider = providerRef.current
    const dropdown = providerDropdownRef.current
    if (!provider || !dropdown) return

    const updatePosition = () => {
      const providerRect = provider.getBoundingClientRect()
      const dropdownRect = dropdown.getBoundingClientRect()
      const viewportWidth = window.innerWidth
      const leftEdge = providerRect.right - dropdownRect.width
      const maxLeft = viewportWidth - dropdownRect.width
      const clampedLeft = Math.max(0, Math.min(leftEdge, maxLeft))
      dropdown.style.setProperty('--provider-dropdown-offset-x', `${clampedLeft - leftEdge}px`)
    }

    updatePosition()
    window.addEventListener('resize', updatePosition)
    return () => { window.removeEventListener('resize', updatePosition) }
  }, [providerOpen])

  // Keep all composer dropdowns within the vertical viewport
  useDropdownVerticalFit(providerOpen, providerDropdownRef)
  useDropdownVerticalFit(addMenuOpen, addDropdownRef)
  useDropdownVerticalFit(permissionOpen, permissionDropdownRef)
  useDropdownVerticalFit(historyOpen, historyDropdownRef)

  const history = historyProp ?? internalHistory
  const setHistory = useCallback((items: ComposerHistoryItem[]) => {
    if (onHistoryChange) {
      onHistoryChange(items)
    } else {
      setInternalHistory(items)
    }
  }, [onHistoryChange])

  const valueRef = useRef(value)
  valueRef.current = value

  const syncLayout = useCallback(() => {
    const el = inputRef.current
    if (!el) return

    const v = valueRef.current

    if (!v) {
      el.style.height = ''
      if (!DEFAULT_DUAL_LINE_MODE) {
        setIsMultiline(false)
      }
      return
    }

    // In dual-line mode, always force multiline layout
    if (DEFAULT_DUAL_LINE_MODE || v.includes('\n')) {
      el.style.height = 'auto'
      const scrollH = el.scrollHeight
      const lineH = parseInt(getComputedStyle(el).lineHeight) || 20
      const maxH = Math.max(lineH * 8, 200)
      el.style.height = `${Math.min(scrollH, maxH)}px`
      setIsMultiline(true)
      return
    }

    // Measure available width for inline text (only when dual-line is off)
    const toolbar = toolbarRef.current
    if (!toolbar) return
    const toolbarPadding = 16 // 8px each side
    const gapTotal = 4 * 4 // 4 gaps between 5 items
    const sendWidth = 28
    let leftBtnsWidth = 0
    const leftItems = toolbar.querySelectorAll<HTMLElement>('.ai-composer-left > *')
    leftItems.forEach(item => {
      leftBtnsWidth += item.offsetWidth
    })
    if (leftItems.length > 1) leftBtnsWidth += 4 * (leftItems.length - 1)

    const availableWidth = toolbar.clientWidth - toolbarPadding - leftBtnsWidth - gapTotal - sendWidth

    const font = getComputedFont(el)
    const textWidth = measureTextWidth(v, font)
    const twoCharWidth = measureTextWidth('xx', font)

    const needsMultiline = textWidth > availableWidth - twoCharWidth

    if (needsMultiline) {
      el.style.height = 'auto'
      const scrollH = el.scrollHeight
      const lineH = parseInt(getComputedStyle(el).lineHeight) || 20
      const maxH = Math.max(lineH * 8, 200)
      el.style.height = `${Math.min(scrollH, maxH)}px`
    } else {
      el.style.height = ''
    }

    setIsMultiline(needsMultiline)
  }, [])

  // Debounced layout sync — runs 150ms after last keystroke, plus immediately on mount
  const syncLayoutTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const syncLayoutMountedRef = useRef(false)

  useEffect(() => {
    if (!syncLayoutMountedRef.current) {
      // First mount: sync immediately
      syncLayoutMountedRef.current = true
      syncLayout()
      return
    }

    // Subsequent changes: debounce
    if (syncLayoutTimerRef.current) clearTimeout(syncLayoutTimerRef.current)
    syncLayoutTimerRef.current = setTimeout(syncLayout, 150)

    return () => {
      if (syncLayoutTimerRef.current) clearTimeout(syncLayoutTimerRef.current)
    }
  }, [value, syncLayout])

  useEffect(() => {
    const el = inputRef.current
    if (!el) return
    const ro = new ResizeObserver(() => syncLayout())
    ro.observe(el)
    return () => ro.disconnect()
  }, [syncLayout])

  const handleSendWithHistory = useCallback(() => {
    const text = value.trim()
    if (!text && attachments.length === 0 && images.length === 0) return

    // Add to history (dedup by text, update timestamp)
    if (text) {
      setHistory(addHistoryEntry(history, text))

      // Local-only telemetry for tour gating: never sent to the backend.
      // Deliver only the first token so the payload stays minimal while still
      // matching `/workflow` / `/` gates.
      emitInteraction({
        kind: 'submit',
        guideId: GuideIds.composer_input,
        text: text.split(/\s+/)[0]?.slice(0, 80) ?? '',
      })
    }
    if (onSubmit) {
      onSubmit(text, attachments, images)
    } else {
      onSend()
    }
    setAttachments([])
    setImages([])
  }, [value, history, setHistory, onSend, onSubmit, attachments, images])

  // Emit a local-only `input` event when the composer text transitions to a
  // slash command (starts with `/`) — used by tour step T4 gating. Kept local
  // to telemetry listeners; user text never leaves the process.
  const slashInputEmittedRef = useRef(false)
  const slashInputCandidate = value.trim().startsWith('/') ? value.trim() : ''
  useEffect(() => {
    if (slashInputCandidate) {
      if (!slashInputEmittedRef.current) {
        slashInputEmittedRef.current = true
        emitInteraction({
          kind: 'input',
          guideId: GuideIds.composer_input,
          text: slashInputCandidate.split(/\s+/)[0]?.slice(0, 80) ?? '',
        })
      }
    } else {
      slashInputEmittedRef.current = false
    }
  }, [slashInputCandidate])

  const handleStop = useCallback(() => {
    if (!onStop) return
    setIsStopping(true)
    onStop()
  }, [onStop])

  // isStopping is the optimistic stop-request spinner. Its natural reset is
  // the isStreaming true→false edge, but stop is also clickable from
  // non-streaming states (crash-recovery paused turns) where that edge never
  // fires — without isStopping in the deps the spinner would never clear and
  // the composer stays stuck on a disabled button forever.
  useEffect(() => {
    if (!isStreaming) {
      setIsStopping(false)
    }
  }, [isStreaming, isStopping])

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    // Exact builtin-mode tokens take priority over the command menu. This lets
    // Enter mount `/goal` immediately instead of treating it as a command
    // submission while the 250ms interception timer is still pending.
    if (e.key === 'Enter' && !e.shiftKey && enterMatchedMode()) {
      e.preventDefault()
      return
    }
    if (mentionMenuOpen) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setMentionSelected((i) => (i + 1) % mentionActions.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setMentionSelected((i) => (i - 1 + mentionActions.length) % mentionActions.length)
        return
      }
      if ((e.key === 'Enter' && !e.shiftKey) || e.key === 'Tab') {
        e.preventDefault()
        const action = mentionActions[mentionSelected]
        if (action) {
          void action.run()
          setMentionDismissed(true)
        }
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setMentionDismissed(true)
        return
      }
    }
    if (fileMentionMenuOpen) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setFileMentionSelected((i) => (i + 1) % fileMentionActions.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setFileMentionSelected((i) => (i - 1 + fileMentionActions.length) % fileMentionActions.length)
        return
      }
      if ((e.key === 'Enter' && !e.shiftKey) || e.key === 'Tab') {
        e.preventDefault()
        const action = fileMentionActions[fileMentionSelected]
        if (action) {
          void action.run()
          setFileMentionDismissed(true)
        }
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setFileMentionDismissed(true)
        return
      }
    }
    if (agentMentionMenuOpen) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setAgentMentionSelected((i) => (i + 1) % agentMentionActions.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setAgentMentionSelected((i) => (i - 1 + agentMentionActions.length) % agentMentionActions.length)
        return
      }
      if ((e.key === 'Enter' && !e.shiftKey) || e.key === 'Tab') {
        e.preventDefault()
        const action = agentMentionActions[agentMentionSelected]
        if (action) {
          void action.run()
          setAgentMentionDismissed(true)
        }
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setAgentMentionDismissed(true)
        return
      }
    }
    if (browserMentionMenuOpen) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setBrowserMentionSelected((i) => (i + 1) % browserMentionActions.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setBrowserMentionSelected((i) => (i - 1 + browserMentionActions.length) % browserMentionActions.length)
        return
      }
      if ((e.key === 'Enter' && !e.shiftKey) || e.key === 'Tab') {
        e.preventDefault()
        const action = browserMentionActions[browserMentionSelected]
        if (action) {
          void action.run()
          setBrowserMentionDismissed(true)
        }
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setBrowserMentionDismissed(true)
        return
      }
    }
    if (slashMenuOpen) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSlashSelected((i) => (i + 1) % visibleSlashActions.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSlashSelected((i) => (i - 1 + visibleSlashActions.length) % visibleSlashActions.length)
        return
      }
      if ((e.key === 'Enter' && !e.shiftKey) || e.key === 'Tab') {
        e.preventDefault()
        const action = visibleSlashActions[slashSelected]
        if (action) {
          void action.run()
          setSlashDismissed(true)
        }
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setSlashDismissed(true)
        return
      }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      if (!disabled && (value.trim() || attachments.length > 0 || images.length > 0)) {
        handleSendWithHistory()
      }
    }
  }, [enterMatchedMode, mentionMenuOpen, mentionActions, mentionSelected, fileMentionMenuOpen, fileMentionActions, fileMentionSelected, agentMentionMenuOpen, agentMentionActions, agentMentionSelected, browserMentionMenuOpen, browserMentionActions, browserMentionSelected, slashMenuOpen, visibleSlashActions, slashSelected, onChange, disabled, value, attachments, images, handleSendWithHistory])

  const setVoiceActive = useCallback((active: boolean) => {
    if (isRecordingRef.current === active) return
    isRecordingRef.current = active
    setIsRecording(active)
  }, [])

  useEffect(() => {
    if (!onVoiceResult) return
    void voiceAPI.setActive(isRecording, isRecording ? {
      onPartial: () => {},
      onResult: onVoiceResult,
      onError: (err) => {
        console.error('[Voice]', err)
        setVoiceActive(false)
      },
    } : undefined).catch((err) => {
      console.error('[Voice] transition failed:', err)
      setVoiceActive(false)
    })
  }, [isRecording, onVoiceResult, setVoiceActive])

  useEffect(() => {
    return () => { void voiceAPI.setActive(false) }
  }, [])

  const handleMicButtonClick = useCallback(() => {
    setVoiceActive(!isRecordingRef.current)
  }, [setVoiceActive])

  const voiceLongPress = useLongPress({
    onLongPress: () => setVoiceActive(true),
    onRelease: () => setVoiceActive(false),
  })

  useEffect(() => {
    if (!isCapacitor()) return
    const handlePTTMessage = (event: MessageEvent) => {
      const msg = event.data || {}
      if (!msg._sporemind) return
      const data = msg.data || {}
      if (data.gestureId !== pttGestureRef.current) return
      if (msg.type === 'ptt:state') {
        setVoiceActive(!!data.active)
        if (!data.active) pttGestureRef.current = null
      } else if (msg.type === 'ptt:tap') {
        pttGestureRef.current = null
        setIsEditing(true)
      }
    }
    window.addEventListener('message', handlePTTMessage)
    return () => window.removeEventListener('message', handlePTTMessage)
  }, [setVoiceActive])

  const handleFacadePointerDown = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (!isCapacitor()) {
      voiceLongPress.onPointerDown(e)
      return
    }
    const gestureId = `${Date.now()}-${e.pointerId}`
    pttGestureRef.current = gestureId
    window.parent.postMessage({
      _sporemind: true,
      type: 'ptt:arm',
      data: { gestureId, thresholdMs: 420 },
    }, '*')
  }, [voiceLongPress])

  const handleFacadePointerUp = useCallback(() => {
    if (isCapacitor()) return
    const wasLongPress = voiceLongPress.wasLongPress()
    voiceLongPress.onPointerUp()
    if (!wasLongPress) setIsEditing(true)
  }, [voiceLongPress])

  const handleFacadeContextMenu = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    if (voiceLongPress.wasLongPress()) {
      e.preventDefault()
      e.stopPropagation()
    }
  }, [voiceLongPress])

  useLayoutEffect(() => {
    if (!isEditing || !isMobile) return
    const frame = requestAnimationFrame(() => {
      const input = inputRef.current
      if (!input) return
      input.focus()
      const end = input.value.length
      input.setSelectionRange(end, end)
      input.scrollTop = input.scrollHeight
    })
    return () => cancelAnimationFrame(frame)
  }, [isEditing, isMobile])

  const addImage = useCallback((file: File) => {
    const reader = new FileReader()
    reader.onload = () => {
      const url = reader.result as string
      setImages(prev => [...prev, { Url: url, Alt: file.name, MimeType: file.type }])
    }
    reader.readAsDataURL(file)
  }, [])

  const handleFileSelect = useCallback((files: FileList | null) => {
    if (!files) return
    Array.from(files).forEach(file => {
      if (file.type.startsWith('image/')) {
        addImage(file)
      } else {
        setAttachments(prev => [...prev, { Name: file.name, MimeType: file.type, SizeBytes: file.size }])
      }
    })
    if (fileInputRef.current) {
      fileInputRef.current.value = ''
    }
  }, [addImage])

  const handlePaste = useCallback((e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const clipboardImages = Array.from(e.clipboardData.items)
      .filter(item => item.kind === 'file' && item.type.startsWith('image/'))
      .map(item => item.getAsFile())
      .filter((file): file is File => file !== null)
    if (clipboardImages.length === 0) return
    e.preventDefault()
    clipboardImages.forEach(addImage)
  }, [addImage])

  const removeAttachment = useCallback((index: number) => {
    setAttachments(prev => prev.filter((_, i) => i !== index))
  }, [])

  const removeImage = useCallback((index: number) => {
    setImages(prev => prev.filter((_, i) => i !== index))
  }, [])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setDragOver(true)
  }, [])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setDragOver(false)
  }, [])

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setDragOver(false)
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      handleFileSelect(e.dataTransfer.files)
    }
  }, [handleFileSelect])

  const toggleFavorite = useCallback((id: string) => {
    setHistory(toggleHistoryFavorite(history, id))
  }, [history, setHistory])

  const deleteHistoryItem = useCallback((id: string) => {
    setHistory(removeHistoryItem(history, id))
  }, [history, setHistory])

  const selectHistoryItem = useCallback((text: string) => {
    onChange(text)
    setHistoryOpen(false)
    // Focus textarea after selection
    requestAnimationFrame(() => {
      inputRef.current?.focus()
    })
  }, [onChange])

  const providerDisplay = (() => {
    if (!useGroups || !activeRoute) return null
    const routeLabel = (routeId: string): string => {
      if (routeId === 'system') return t('dialog.newAgent.autoFirst') || 'Auto'
      return groups!.find(g => g.routeId === routeId)?.label ?? routeId
    }
    const resolvedUnit = currentUnit
      ? [currentUnit.provider, currentUnit.model].filter(Boolean).join(' · ')
      : ''
    if (activeRoute.kind === 'auto') {
      return { label: routeLabel('system'), subtitle: resolvedUnit || undefined }
    }
    if (activeRoute.kind === 'aggregator') {
      return { label: routeLabel(activeRoute.aggregatorId), subtitle: resolvedUnit || undefined }
    }
    // unit pinned — when served by a custom aggregator, badge the aggregator
    // name (not the raw provider); the system aggregator keeps the provider badge.
    const servingAgg = activeRoute.servingAggregatorId
    const subtitle = servingAgg && servingAgg !== 'system'
      ? routeLabel(servingAgg)
      : (activeRoute.unit.provider || routeLabel(activeRoute.failover))
    return { label: activeRoute.unit.model, subtitle }
  })()

  // unit (provider::model) → endpoint, built from the selector's model
  // options; lets the collapsed button resolve brands via the official
  // endpoint when the model name alone doesn't match.
  const unitEndpointMap = useMemo(() => {
    const m = new Map<string, string>()
    const add = (o: ProviderOption) => {
      if (o.endpoint && o.unit) m.set(`${o.unit.provider}::${o.unit.model}`, o.endpoint)
    }
    for (const g of groups ?? []) for (const o of g.models) add(o)
    for (const o of providers ?? []) add(o)
    return m
  }, [groups, providers])

  // Brand icon for the collapsed provider button, resolved by the current
  // MODEL name first, then its endpoint, then the provider/route label.
  const providerBtnIconKey = useGroups
    ? (activeRoute?.kind === 'unit'
        ? iconKeyForModelRow(
            activeRoute.unit.model,
            unitEndpointMap.get(`${activeRoute.unit.provider}::${activeRoute.unit.model}`),
            activeRoute.unit.provider,
          )
        : iconKeyForModelRow(
            currentUnit?.model,
            currentUnit ? unitEndpointMap.get(`${currentUnit.provider}::${currentUnit.model}`) : undefined,
            currentUnit?.provider ?? providerDisplay?.label,
          ))
    : iconKeyForModelRow(activeProvider?.label, activeProvider?.endpoint, activeProvider?.subtitle)

  // Cooldown display for the selected unit (selectedHealthOption computed early
  // above so the tick effect can gate on hasSelectedCooldown).
  const selectedCooling = selectedHealthOption
    ? isCoolingDown(selectedHealthOption, providerNowMs)
    : false
  const selectedCountdown = selectedHealthOption?.cooldownUntil && selectedCooling
    ? formatCountdown(selectedHealthOption.cooldownUntil, providerNowMs)
    : null

  const THINKING_OPTIONS: ThinkingLevel[] = [
    { Mode: 'none' },
    { Mode: 'effort', Effort: 'low' },
    { Mode: 'effort', Effort: 'medium' },
    { Mode: 'effort', Effort: 'high' },
    { Mode: 'effort', Effort: 'xhigh' },
    { Mode: 'effort', Effort: 'max' },
    { Mode: 'effort', Effort: 'ultra' },
  ]

  const isThinkingActive = (opt: ThinkingLevel): boolean => {
    if (!activeThinkingLevel) return opt.Mode === 'none'
    return activeThinkingLevel.Mode === opt.Mode && activeThinkingLevel.Effort === opt.Effort
  }
  const hasValue = value.trim().length > 0 || attachments.length > 0 || images.length > 0
  const visibleBadges = badges ?? []
  const configBadges = visibleBadges.filter(b => b.configGroup)
  const regularBadges = visibleBadges.filter(b => !b.configGroup)

  // Button state:
  // isRecording -> Mic (red pulse)
  // isStopping -> Spinner
  // isPausing   -> Spinner
  // isWaiting   -> Pause (pause all workflow children) + Send/History; paused takes priority (resume)
  // isPaused    -> Stop + Resume
  // isStreaming && hasValue -> Send + compact Stop
  // isStreaming && !hasValue -> Stop
  // hasValue    -> Send (green bg)
  // default     -> History (Clock, circular)

  const renderRightButton = () => {
    if (isRecording) {
      return (
        <button
          className="ai-composer-send recording"
          onClick={handleMicButtonClick}
          disabled={disabled}
          type="button"
          title="Stop recording"
        >
          <Mic size={16} />
        </button>
      )
    }
    if (isStopping) {
      return (
        <button
          className="ai-composer-stop ai-composer-stop-pending"
          disabled
          type="button"
          title="Stopping..."
        >
          <Loader2 size={14} className="ai-composer-stop-spinner" />
        </button>
      )
    }
    if (isPausing) {
      return (
        <div className="ai-composer-right-group">
          <button
            className="ai-composer-stop-compact ai-composer-stop-pending"
            disabled
            type="button"
            title="Pausing..."
          >
            <Loader2 size={12} className="ai-composer-stop-spinner" />
          </button>
        </div>
      )
    }
    if (isWaiting && !isPaused && onPause) {
      // Waiting means the LLM loop already finished; input stays usable.
      // Pause-all rides alongside the normal Send/History affordance.
      return (
        <div className="ai-composer-right-group">
          <button
            className="ai-composer-stop-compact"
            onClick={() => onPause?.()}
            type="button"
            title={t('ai.pauseAll') ?? 'Pause All'}
          >
            <Pause size={12} fill="currentColor" />
          </button>
          {hasValue ? (
            <button
              className="ai-composer-send visible"
              data-guide-id="composer.send"
              onClick={handleSendWithHistory}
              disabled={disabled}
              type="button"
              title="Send"
            >
              <ArrowUp size={16} />
            </button>
          ) : (
            <button
              className="ai-composer-history-btn"
              onClick={() => setHistoryOpen(v => !v)}
              disabled={disabled}
              type="button"
              title="History"
            >
              <Clock size={16} />
            </button>
          )}
        </div>
      )
    }
    if (isPaused) {
      return (
        <div className="ai-composer-right-group">
          <button
            className="ai-composer-stop-compact"
            onClick={handleStop}
            type="button"
            title="Stop"
          >
            <Square size={12} fill="currentColor" />
          </button>
          <button
            className="ai-composer-send visible"
            onClick={() => onResume?.()}
            disabled={!onResume}
            type="button"
            title="Resume"
          >
            <Play size={16} />
          </button>
        </div>
      )
    }
    if (isStreaming && hasValue) {
      return (
        <div className="ai-composer-right-group">
          <button
            className="ai-composer-stop-compact"
            onClick={handleStop}
            type="button"
            title="Stop"
          >
            <Square size={12} fill="currentColor" />
          </button>
          {onPause && (
            <button
              className="ai-composer-stop-compact"
              onClick={() => onPause?.()}
              type="button"
              title="Pause"
            >
              <Pause size={12} fill="currentColor" />
            </button>
          )}
          <button
            className="ai-composer-send visible"
            data-guide-id="composer.send"
            onClick={handleSendWithHistory}
            disabled={disabled}
            type="button"
            title="Send"
          >
            <ArrowUp size={16} />
          </button>
        </div>
      )
    }
    if (isStreaming) {
      return (
        <div className="ai-composer-right-group">
          <button
            className="ai-composer-stop"
            onClick={handleStop}
            type="button"
            title="Stop"
          >
            <Square size={14} fill="currentColor" />
          </button>
          {onPause && (
            <button
              className="ai-composer-stop-compact"
              onClick={() => onPause?.()}
              type="button"
              title="Pause"
            >
              <Pause size={14} fill="currentColor" />
            </button>
          )}
        </div>
      )
    }
    if (hasValue) {
      return (
        <button
          className="ai-composer-send visible"
          data-guide-id="composer.send"
          onClick={handleSendWithHistory}
          disabled={disabled || !hasValue}
          type="button"
          title="Send"
        >
          <ArrowUp size={16} />
        </button>
      )
    }
    return (
      <button
        className="ai-composer-history-btn"
        onClick={() => setHistoryOpen(v => !v)}
        disabled={disabled}
        type="button"
        title="History"
      >
        <Clock size={16} />
      </button>
    )
  }

  return (
    <>
      {(aboveLeftSlot || middleSlot || aboveRightSlot) && (
        <div className="ai-composer-above">
          <div className="ai-composer-above-left">
            {aboveLeftSlot}
          </div>
          <div className="ai-composer-above-middle">{middleSlot}</div>
          <div className="ai-composer-above-right">{aboveRightSlot}</div>
        </div>
      )}
      <div className={`ai-composer ${isMultiline ? 'multiline' : ''} ${isRecording ? 'recording' : ''}`}>
      {dropdownSlot}

      {/* History dropdown */}
      {historyOpen && (
        <>
          <div className="ai-composer-history-backdrop" onClick={() => setHistoryOpen(false)} />
          <div className="ai-composer-history-dropdown" ref={historyDropdownRef}>
            {history.length === 0 ? (
              <div className="ai-composer-history-empty">No history</div>
            ) : (
              history.map(item => (
                <div key={item.id} className="ai-composer-history-item">
                  <button
                    className="ai-composer-history-text"
                    type="button"
                    onClick={() => selectHistoryItem(item.text)}
                    title={item.text}
                  >
                    <span className="ai-composer-history-text-label">{item.text}</span>
                    <span className="ai-composer-history-text-time">{fmtTime(item.timestamp)}</span>
                  </button>
                  <button
                    className={`ai-composer-history-fav ${item.isFavorite ? 'active' : ''}`}
                    type="button"
                    title={item.isFavorite ? 'Unfavorite' : 'Favorite'}
                    onClick={() => toggleFavorite(item.id)}
                  >
                    <Star size={12} fill={item.isFavorite ? 'currentColor' : 'none'} />
                  </button>
                  <button
                    className="ai-composer-history-del"
                    type="button"
                    title="Delete"
                    onClick={() => deleteHistoryItem(item.id)}
                  >
                    <Trash2 size={12} />
                  </button>
                </div>
              ))
            )}
          </div>
        </>
      )}

      <div className={`ai-composer-toolbar ${dragOver ? 'drag-over' : ''}`} ref={toolbarRef}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        {/* Badge panel — mounted modes (e.g. Goal) shown inline, wrapping to multiple lines. */}
        {(regularBadges.length > 0 || (configBadgesVisible && configBadges.length > 0)) && (
          <div className="ai-composer-badge-drawer-panel">
            {/* Config-mounted bundles collapse into a single icon-only capsule */}
            {configBadgesVisible && configBadges.length > 0 && (
              <span
                ref={groupTooltipRef}
                className="ai-composer-badge ai-composer-badge-group"
                onMouseEnter={openGroupTooltip}
                onMouseLeave={scheduleCloseGroupTooltip}
              >
                {configBadges.map((badge, idx) => (
                  <span
                    key={idx}
                    className="ai-composer-badge-icon"
                    title={badge.title}
                    onContextMenu={badge.onContextMenu ? (e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      badge.onContextMenu!(e.clientX, e.clientY)
                    } : undefined}
                  >
                    <CardIcon name={badge.icon} size={14} color={badge.color} />
                  </span>
                ))}
              </span>
            )}
            {regularBadges.map((badge, idx) => (
              <span
                key={idx}
                className={`ai-composer-badge${badge.onClick ? ' ai-composer-badge--clickable' : ''}${badge.stale ? ' ai-composer-badge--stale' : ''}`}
                title={badge.title}
                onClick={badge.onClick}
                onContextMenu={badge.onContextMenu ? (e) => {
                  e.preventDefault()
                  e.stopPropagation()
                  badge.onContextMenu!(e.clientX, e.clientY)
                } : undefined}
                role={badge.onClick ? 'button' : undefined}
                tabIndex={badge.onClick ? 0 : undefined}
                onKeyDown={badge.onClick ? (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); badge.onClick!() } } : undefined}
              >
                <span className="ai-composer-badge-icon"><CardIcon name={badge.icon} size={14} color={badge.color} /></span>
                <span className="ai-composer-badge-title">{badge.title}</span>
                <button
                  type="button"
                  className="ai-composer-badge-close"
                  aria-label={`Remove ${badge.title}`}
                  onClick={(e) => { e.stopPropagation(); badge.onClose?.() }}
                >
                  <X size={12} />
                </button>
              </span>
            ))}
          </div>
        )}

        <input
          type="file"
          ref={fileInputRef}
          style={{ display: 'none' }}
          multiple
          accept="image/*,*/*"
          onChange={(e) => handleFileSelect(e.target.files)}
        />
        {/* Input area: textarea always gets pointer events (no shell capture) */}
        <div className="ai-composer-input-area">
          {slashMenuOpen && (
            <AppOmniboxResults
              actions={visibleSlashActions}
              selectedIndex={slashSelected}
              onSelectedIndexChange={setSlashSelected}
              onSelect={(action) => {
                void action.run()
                setSlashDismissed(true)
              }}
              dropdown
            />
          )}
          {mentionMenuOpen && (
            <AppOmniboxResults
              actions={mentionActions}
              selectedIndex={mentionSelected}
              onSelectedIndexChange={setMentionSelected}
              onSelect={(action) => {
                void action.run()
                setMentionDismissed(true)
              }}
              dropdown
            />
          )}
          {fileMentionMenuOpen && (
            <AppOmniboxResults
              actions={fileMentionActions}
              selectedIndex={fileMentionSelected}
              onSelectedIndexChange={setFileMentionSelected}
              onSelect={(action) => {
                void action.run()
                setFileMentionDismissed(true)
              }}
              dropdown
            />
          )}
          {agentMentionMenuOpen && (
            <AppOmniboxResults
              actions={agentMentionActions}
              selectedIndex={agentMentionSelected}
              onSelectedIndexChange={setAgentMentionSelected}
              onSelect={(action) => {
                void action.run()
                setAgentMentionDismissed(true)
              }}
              dropdown
            />
          )}
          {browserMentionMenuOpen && (
            <AppOmniboxResults
              actions={browserMentionActions}
              selectedIndex={browserMentionSelected}
              onSelectedIndexChange={setBrowserMentionSelected}
              onSelect={(action) => {
                void action.run()
                setBrowserMentionDismissed(true)
              }}
              dropdown
            />
          )}
          {isMobile && !isEditing ? (
            <div
              className="ai-composer-textarea ai-composer-textarea-facade"
              onPointerDown={handleFacadePointerDown}
              onPointerMove={voiceLongPress.onPointerMove}
              onPointerUp={handleFacadePointerUp}
              onPointerCancel={voiceLongPress.onPointerCancel}
              onPointerLeave={voiceLongPress.onPointerLeave}
              onContextMenu={handleFacadeContextMenu}
            >
              {value || <span style={{ opacity: 0.5 }}>{placeholder}</span>}
            </div>
          ) : (
            <textarea
              ref={inputRef}
              className="ai-composer-textarea"
              data-guide-id="composer.input"
              value={value}
              onChange={(e) => onChange(e.target.value)}
              onKeyDown={handleKeyDown}
              onPaste={handlePaste}
              placeholder={placeholder}
              disabled={disabled}
              onBlur={isMobile ? () => setIsEditing(false) : undefined}
            />
          )}

          {/* Voice waveform — pawpad terminal style, 5-bar scaleY wave */}
          <div className="ai-composer-voice-wave">
            <div className="ai-composer-voice-wave-bar" />
            <div className="ai-composer-voice-wave-bar" />
            <div className="ai-composer-voice-wave-bar" />
            <div className="ai-composer-voice-wave-bar" />
            <div className="ai-composer-voice-wave-bar" />
          </div>
        </div>

        {/* Attachments and images preview — directly below textarea */}
        {(images.length > 0 || attachments.length > 0) && (
          <div className="ai-composer-preview-bar">
            {images.map((img, i) => (
              <div key={`img-${i}`} className="ai-composer-preview-item">
                <img
                  src={img.Url}
                  alt={img.Alt}
                  className="ai-composer-preview-img"
                  onClick={() => shellCtx?.onOpenImage?.(img.Url, img.Alt)}
                />
                <button
                  type="button"
                  className="ai-composer-preview-remove"
                  onClick={() => removeImage(i)}
                  title="Remove"
                >
                  <X size={10} />
                </button>
              </div>
            ))}
            {attachments.map((att, i) => (
              <div key={`att-${i}`} className="ai-composer-preview-item">
                <Paperclip size={12} />
                <span className="ai-composer-preview-name">{att.Name}</span>
                <button
                  type="button"
                  className="ai-composer-preview-remove"
                  onClick={() => removeAttachment(i)}
                  title="Remove"
                >
                  <X size={10} />
                </button>
              </div>
            ))}
          </div>
        )}

        {/* Actions frame: all functional buttons */}
        <div className="ai-composer-actions-frame">
          {/* Left tools group */}
          <div className="ai-composer-left">
            {addMenuOpen && (
              <>
                <div className="ai-composer-add-backdrop" onClick={() => setAddMenuOpen(false)} />
                <div className="ai-composer-add-dropdown" ref={addDropdownRef}>
                  {isMobile && onCreateAgent && (
                    <button
                      className="ai-composer-add-item"
                      type="button"
                      onClick={() => {
                        onCreateAgent()
                        setAddMenuOpen(false)
                      }}
                    >
                      <Bot size={16} />
                      <span>{t('composer.newAgent')}</span>
                    </button>
                  )}
                  {onDeleteCurrentAgent && (
                    <button
                      className="ai-composer-add-item danger"
                      type="button"
                      onClick={() => {
                        setAddMenuOpen(false)
                        setDeleteConfirmOpen(true)
                      }}
                    >
                      <Trash2 size={16} />
                      <span>{t('composer.deleteCurrentAgent')}</span>
                    </button>
                  )}
                  <button
                    className="ai-composer-add-item"
                    type="button"
                    onClick={() => {
                      fileInputRef.current?.click()
                      setAddMenuOpen(false)
                    }}
                  >
                    <FileText size={16} />
                    <span>{t('composer.attachFile')}</span>
                  </button>
                  <button
                    className="ai-composer-add-item"
                    type="button"
                    onClick={() => {
                      onChange('/')
                      inputRef.current?.focus()
                      setAddMenuOpen(false)
                    }}
                  >
                    <Terminal size={16} />
                    <span>{t('composer.addCommand')}</span>
                  </button>
                  <button
                    className="ai-composer-add-item"
                    type="button"
                    onClick={() => {
                      onOpenOmnibox?.()
                      setAddMenuOpen(false)
                    }}
                  >
                    <Search size={16} />
                    <span>{t('composer.search')}</span>
                  </button>
                  <button
                    className="ai-composer-add-item"
                    type="button"
                    onClick={() => {
                      startScreenshot()
                      setAddMenuOpen(false)
                    }}
                  >
                    <Crop size={16} />
                    <span>{t('composer.screenshot')}</span>
                  </button>
                </div>
              </>
            )}
            {deleteConfirmOpen && (
              <>
                <div className="ai-composer-add-backdrop" onClick={() => setDeleteConfirmOpen(false)} />
                <div className="ai-composer-delete-confirm">
                  <p className="ai-composer-delete-confirm-title">{t('composer.deleteCurrentAgentConfirm')}</p>
                  <div className="ai-composer-delete-confirm-actions">
                    <button type="button" className="ai-composer-delete-confirm-cancel" onClick={() => setDeleteConfirmOpen(false)}>
                      {t('composer.cancel')}
                    </button>
                    <button
                      type="button"
                      className="ai-composer-delete-confirm-confirm"
                      onClick={() => {
                        setDeleteConfirmOpen(false)
                        onDeleteCurrentAgent?.()
                      }}
                    >
                      {t('composer.delete')}
                    </button>
                  </div>
                </div>
              </>
            )}
            {permissionConfirm && (
              <>
                <div className="ai-composer-add-backdrop" onClick={() => setPermissionConfirm(null)} />
                <div className="ai-composer-delete-confirm">
                  <p className="ai-composer-delete-confirm-title">
                    {t('composer.permission.confirmTitle', { mode: permissionModes.find(m => m.id === permissionConfirm.mode)?.label ?? permissionConfirm.mode })}
                  </p>
                  <p className="ai-composer-permission-confirm-body">{t('composer.permission.confirmBody')}</p>
                  <div className="ai-composer-delete-confirm-actions">
                    <button type="button" className="ai-composer-delete-confirm-cancel" onClick={() => setPermissionConfirm(null)}>
                      {t('composer.cancel')}
                    </button>
                    <button
                      type="button"
                      className="ai-composer-delete-confirm-confirm"
                      onClick={() => {
                        const pending = permissionConfirm
                        setPermissionConfirm(null)
                        pending.apply()
                      }}
                    >
                      {t('composer.permission.confirmOk')}
                    </button>
                  </div>
                </div>
              </>
            )}
            <button
              className="ai-composer-add-btn"
              type="button"
              title="Add"
              onClick={() => setAddMenuOpen(v => !v)}
            >
              <Plus size={16} />
            </button>
            {avatarBarSlot && (
              <button
                className={`ai-composer-avatar-toggle-btn ${avatarBarVisible ? 'active' : ''}`}
                type="button"
                title={avatarBarVisible ? 'Hide agents' : 'Show agents'}
                onClick={() => setAvatarBarVisible(!avatarBarVisible)}
              >
                <Users size={16} />
              </button>
            )}
            {onPermissionModeChange && (() => {
              const activeMode = (permissionModes.find(m => m.id === (normalizePermissionMode(permissionMode) ?? 'permission')) ?? permissionModes.find(m => m.id === 'permission'))!
              const hasGlobalPermission = globalPermissionMode !== undefined && !!onGlobalPermissionModeChange
              const tabMode: PermissionMode = normalizePermissionMode(permissionTab === 'current'
                ? permissionMode
                : globalPermissionMode) ?? 'permission'
              const tabChange = permissionTab === 'current'
                ? onPermissionModeChange
                : onGlobalPermissionModeChange
              return (
                <div className="ai-composer-permission" data-guide-id="composer.permission-mode">
                  {permissionOpen && (
                    <>
                      <div className="ai-composer-permission-backdrop" onClick={() => setPermissionOpen(false)} />
                      <div className="ai-composer-permission-dropdown" ref={permissionDropdownRef}>
                        {hasGlobalPermission && (
                          <div className="ai-composer-permission-source-switch" role="tablist">
                            <button
                              className={permissionTab === 'current' ? 'active' : ''}
                              type="button"
                              role="tab"
                              aria-selected={permissionTab === 'current'}
                              onClick={() => setPermissionTab('current')}
                            >
                              {t('composer.permission.tabCurrent')}
                            </button>
                            <button
                              className={permissionTab === 'global' ? 'active' : ''}
                              type="button"
                              role="tab"
                              aria-selected={permissionTab === 'global'}
                              onClick={() => setPermissionTab('global')}
                            >
                              {t('composer.permission.tabGlobal')}
                            </button>
                          </div>
                        )}
                        {hasGlobalPermission && (
                          <div className="ai-composer-permission-source-desc">
                            {permissionTab === 'current'
                              ? t('composer.permission.tabCurrentDesc')
                              : t('composer.permission.tabGlobalDesc')}
                          </div>
                        )}
                        {permissionModes.map(m => {
                          const ModeIcon = m.Icon
                          const isActive = m.id === tabMode
                          return (
                            <button
                              key={m.id}
                              className={`ai-composer-permission-item ${isActive ? 'active' : ''}`}
                              type="button"
                              onClick={() => {
                                if (DANGEROUS_PERMISSION_MODES.has(m.id)) {
                                  setPermissionConfirm({ mode: m.id, apply: () => tabChange?.(m.id) })
                                } else {
                                  tabChange?.(m.id)
                                }
                                setPermissionOpen(false)
                              }}
                            >
                              <ModeIcon size={14} />
                              <span className="ai-composer-permission-item-text">
                                <span className="ai-composer-permission-item-label">{m.label}</span>
                                <span className="ai-composer-permission-item-desc">{m.desc}</span>
                              </span>
                              {isActive && <Check size={14} />}
                            </button>
                          )
                        })}
                      </div>
                    </>
                  )}
                  <button
                    className={`ai-composer-permission-btn${permissionOpen ? ' open' : ''}`}
                    type="button"
                    title={activeMode.desc}
                    onClick={() => setPermissionOpen(v => !v)}
                  >
                    <activeMode.Icon size={14} />
                    <span className="ai-composer-permission-label">{activeMode.label}</span>
                    <ChevronDown className="ai-composer-permission-chevron" size={12} />
                  </button>
                </div>
              )
            })()}
            {onReturnToConversation && (
              <button
                type="button"
                className="ai-composer-return-to-conversation"
                title={t('composer.returnToConversation')}
                onClick={onReturnToConversation}
                aria-label={t('composer.returnToConversation')}
              >
                <MessageSquare size={14} />
              </button>
            )}

          </div>

          {/* Right: provider + git controls + send/history/stop */}
          <div className="ai-composer-right">
            {!isRecording && (providers.length > 0 || useGroups) && (
              <div className="ai-composer-provider" ref={providerRef}>
                <button
                  className={`ai-composer-provider-btn${providerOpen ? ' open' : ''}${selectedCooling ? ' is-cooling' : ''}`}
                  type="button"
                  onClick={() => setProviderOpen(v => !v)}
                  onContextMenu={slotMenu ? (e) => {
                    e.preventDefault()
                    setProviderOpen(false)
                    setSlotMenuOpen(true)
                  } : undefined}
                >
                  <span className="ai-composer-provider-text">
                    {providerBtnIconKey && (
                      <ProviderIcon id={providerBtnIconKey} size={14} className="ai-composer-provider-btn-icon" />
                    )}
                    <span className="ai-composer-provider-label">
                      {useGroups
                        ? (providerDisplay?.label ?? 'Select')
                        : (activeProvider?.label ?? 'Select')}
                    </span>
                    {(useGroups ? providerDisplay?.subtitle : activeProvider?.subtitle) && (
                      <span className="ai-composer-provider-subtitle">
                        {useGroups ? providerDisplay!.subtitle : activeProvider!.subtitle}
                      </span>
                    )}
                    {selectedCooling && selectedCountdown && (
                      <span className="ai-composer-provider-cooldown" aria-hidden="true">{selectedCountdown}</span>
                    )}
                    {activeThinkingLevel && activeThinkingLevel.Mode !== 'none' && (
                      <span className="ai-composer-provider-thinking">
                        {activeThinkingLevel.Effort ?? 'think'}
                      </span>
                    )}
                  </span>
                  <ChevronDown size={12} />
                </button>
                {providerOpen && (
                  <>
                    <div className="ai-composer-provider-backdrop" onClick={() => setProviderOpen(false)} />
                    <div className="ai-composer-provider-dropdown" ref={providerDropdownRef}>
                      {useGroups ? (() => {
                        const activeRouteId = !activeRoute ? null
                          : activeRoute.kind === 'aggregator' ? activeRoute.aggregatorId
                          : activeRoute.kind === 'unit'
                            ? activeRoute.servingAggregatorId
                            : 'system'
                        const pinnedUnit = activeRoute?.kind === 'unit' ? activeRoute.unit : null
                        const toggleExpand = (routeId: string) => setExpandedRoutes(prev => {
                          const next = new Set(prev)
                          if (next.has(routeId)) next.delete(routeId)
                          else next.add(routeId)
                          return next
                        })
                        // Clicking a child-aggregator ref soft-pins the child's
                        // first available (non-cooling, non-disabled) unit
                        // through the ROOT aggregator — identical to clicking
                        // a nested unit row. Internal aggregators are never
                        // promoted to the active route; the inline tree
                        // (chevron) remains the way to browse them.
                        const selectRefAggregator = (aggregatorId: string, rootRouteId: string) => {
                          const childGroup = groups!.find(g => g.routeId === aggregatorId && !g.isAuto)
                          if (!childGroup) return // stale ref — row is greyed
                          const firstAvailable = childGroup.items
                            .filter((it): it is { kind: 'model'; option: ProviderOption } => it.kind === 'model')
                            .map(it => it.option)
                            .find(m => !!m.unit && !isCoolingDown(m, providerNowMs) && !isDisabled(m))
                          if (!firstAvailable) return // child has no healthy unit to pin
                          onSelectUnit?.(rootRouteId, firstAvailable)
                          setProviderOpen(false)
                        }
                        // Expand/collapse an inline tree ref (path-keyed so the
                        // same child referenced from different parents keeps
                        // independent expansion state).
                        const toggleTreeRef = (pathKey: string) => setExpandedTreeRefs(prev => {
                          const next = new Set(prev)
                          if (next.has(pathKey)) next.delete(pathKey)
                          else next.add(pathKey)
                          return next
                        })
                        // Recursively render a group's models + refs as a
                        // tree branch under an expanded ref row. `depth` drives
                        // indentation; `ancestors` is the chain of aggregator
                        // route IDs on the current branch, used to detect
                        // cycles so lazy expansion can never loop forever.
                        // `pathKey` uniquely identifies this branch's expansion
                        // state in `expandedTreeRefs`.
                        const renderTreeBranch = (
                          group: ProviderGroup,
                          depth: number,
                          ancestors: Set<string>,
                          pathKey: string,
                          rootRouteId: string,
                        ): React.ReactNode => {
                          // Walk items in pool order; group consecutive ref items
                          // so they share a single refs-container div, matching
                          // the original layout when refs were rendered together.
                          const modelNodes: React.ReactNode[] = []
                          let pendingRefs: RefOption[] = []
                          let pendingKey = pathKey
                          const flushRefs = () => {
                            if (pendingRefs.length > 0) {
                              modelNodes.push(<React.Fragment key={`refs-${pendingKey}`}>{renderTreeRefs(pendingRefs, depth, ancestors, pendingKey, rootRouteId)}</React.Fragment>)
                              pendingRefs = []
                            }
                          }
                          group.items.forEach((item, idx) => {
                            if (item.kind === 'ref') {
                              if (pendingRefs.length === 0) pendingKey = `${pathKey}-${idx}`
                              pendingRefs.push(item.option)
                              return
                            }
                            flushRefs()
                            const m = item.option
                            // Nested units highlight against the active unit
                            // directly (the parent may be the serving route);
                            // top-level units keep the route-gated check.
                            const current = depth > 0 ? (pinnedUnit ?? currentUnit) : (routeActiveFor(group.routeId) ? (pinnedUnit ?? currentUnit) : null)
                            const unitActive = !!current
                              && !!m.unit
                              && m.unit.model === current.model
                              && (m.unit.provider ?? '') === (current.provider ?? '')
                            modelNodes.push(
                              <div key={`tree-model-${pathKey}-${idx}-${m.id}`} className="ai-composer-provider-tree-node">
                                <CooldownProviderOption
                                  option={m}
                                  active={unitActive}
                                  nowMs={providerNowMs}
                                  onSelect={() => { onSelectUnit?.(rootRouteId, m); setProviderOpen(false) }}
                                  t={t as any}
                                />
                              </div>,
                            )
                          })
                          flushRefs()
                          return <React.Fragment key={`tree-${pathKey}`}>{modelNodes}</React.Fragment>
                        }
                        // Render the ref rows of a group, each of which may be
                        // expanded into a deeper tree branch (lazy — content is
                        // only rendered on user expansion). A ref back into the
                        // current ancestor chain renders a cycle indicator
                        // instead of an expandable row.
                        const renderTreeRefs = (
                          refs: RefOption[],
                          depth: number,
                          ancestors: Set<string>,
                          parentPathKey: string,
                          rootRouteId: string,
                        ): React.ReactNode => (
                          <div className="ai-composer-provider-refs">
                            {refs.map(ref => {
                              const childPathKey = `${parentPathKey}::${ref.aggregatorId}`
                              const isCycle = ancestors.has(ref.aggregatorId)
                              const childGroup = groups!.find(gg => gg.routeId === ref.aggregatorId && !gg.isAuto)
                              const refStale = !childGroup
                              const childExpanded = !isCycle && !refStale && expandedTreeRefs.has(childPathKey)
                              const refProj = {
                                healthState: ref.healthState,
                                healthReason: ref.healthReason,
                                cooldownUntil: ref.cooldownUntil,
                                lastFailureAt: ref.lastFailureAt,
                                recoveryMode: ref.recoveryMode,
                              }
                              const refCooling = isCoolingDown(refProj, providerNowMs)
                              const refDisabled = isDisabled(refProj)
                              const refUnavailable = refCooling || refDisabled
                              // A child aggregator whose pool contains the active unit
                              // (pinned or auto-selected) is marked active even without
                              // runtime dispatch activity — the dispatch routes through it.
                              const activeUnit = pinnedUnit ?? currentUnit
                              const refServing = !isCycle && !refStale && !!activeUnit
                                && !!childGroup
                                && childGroup.models.some(m =>
                                  !!m.unit && m.unit.model === activeUnit.model
                                  && (m.unit.provider ?? '') === (activeUnit.provider ?? ''))
                              const refActive = !!ref.dispatchActivity || refServing
                              return (
                                <div key={`ref-${childPathKey}`} className="ai-composer-provider-tree-ref">
                                  <div
                                    role="button"
                                    tabIndex={refStale || isCycle ? -1 : 0}
                                    className={[
                                      'ai-composer-provider-ref-row',
                                      refStale ? 'is-stale' : '',
                                      refCooling ? 'is-cooling' : '',
                                      refDisabled ? 'is-disabled' : '',
                                      refUnavailable ? 'is-unavailable' : '',
                                      isCycle ? 'is-cycle' : '',
                                      refActive ? 'is-active' : '',
                                    ].filter(Boolean).join(' ')}
                                    onClick={() => {
                                      if (refStale || isCycle) return
                                      selectRefAggregator(ref.aggregatorId, rootRouteId)
                                    }}
                                    onKeyDown={(e) => {
                                      if (refStale || isCycle || (e.key !== 'Enter' && e.key !== ' ')) return
                                      e.preventDefault()
                                      selectRefAggregator(ref.aggregatorId, rootRouteId)
                                    }}
                                  >
                                    <span className="ai-composer-provider-ref-label" title={ref.label}>
                                      {ref.label}
                                    </span>
                                    {(() => {
                                      const keys = iconKeysForModels(routeModelLabels(groups ?? [], ref.aggregatorId), 3)
                                      return keys.length ? (
                                        <span className="ai-composer-icon-stack" aria-hidden="true">
                                          {keys.map(k => <ProviderIcon key={k} id={k} size={12} />)}
                                        </span>
                                      ) : null
                                    })()}
                                    {isCycle ? (
                                      <span className="ai-composer-provider-ref-cycle" title={t('composer.tree.cycle')}>
                                        {t('composer.tree.cycle')}
                                      </span>
                                    ) : refStale ? (
                                      <span className="ai-composer-provider-ref-stale">{t('composer.ref.stale')}</span>
                                    ) : (
                                      <>
                                        {refCooling && ref.cooldownUntil ? (
                                          <span className="ai-composer-provider-ref-badge is-cooling">
                                            {t('health.coolingDown', { countdown: formatCountdown(ref.cooldownUntil, providerNowMs) })}
                                          </span>
                                        ) : refDisabled ? (
                                          <span className="ai-composer-provider-ref-badge is-disabled">
                                            {t(`health.reason.${healthReasonLabelKey(ref.healthReason)}` as any)}
                                          </span>
                                        ) : null}
                                        {ref.dispatchActivity && (
                                          <DispatchActivityBadge activity={ref.dispatchActivity} t={t as any} />
                                        )}
                                      </>
                                    )}
                                    {refActive && !refStale && !isCycle && (
                                      <span className="ai-composer-provider-ref-check"><Check size={13} /></span>
                                    )}
                                    {!refStale && !isCycle && (
                                      <button
                                        className="ai-composer-provider-ref-toggle"
                                        type="button"
                                        aria-label={`toggle-tree-${ref.aggregatorId}`}
                                        onClick={(e) => { e.stopPropagation(); toggleTreeRef(childPathKey) }}
                                      >
                                        <ChevronDown size={12} className={childExpanded ? 'expanded' : ''} />
                                      </button>
                                    )}
                                  </div>
                                  {childExpanded && childGroup && (
                                    <div className="ai-composer-provider-tree-branch" style={{ marginLeft: `${18 * (depth + 1)}px` }}>
                                      {renderTreeBranch(childGroup, depth + 1, new Set(ancestors).add(ref.aggregatorId), childPathKey, rootRouteId)}
                                    </div>
                                  )}
                                </div>
                              )
                            })}
                          </div>
                        )
                        const routeActiveFor = (routeId: string) => activeRouteId === routeId
                        const customGroups = groups!.filter(g => !g.isAuto)
                        const autoGroup = groups!.find(g => g.isAuto)
                        return (
                          <div className="ai-composer-provider-col">
                            {customGroups.map(g => {
                              const expanded = expandedRoutes.has(g.routeId) || (activeRouteId === g.routeId && expandedRoutes.size === 0)
                              // The serving aggregator stays selected even when one of its units
                              // is pinned — the pinned unit is also highlighted below.
                              const routeActive = activeRouteId === g.routeId
                              return (
                                <React.Fragment key={g.routeId}>
                                  <div
                                    className="ai-composer-provider-route-row"
                                  >
                                    <button
                                      className={`ai-composer-provider-route ${routeActive ? 'active' : ''}`}
                                      type="button"
                                      onClick={() => { onSelectRoute?.(g.routeId); setProviderOpen(false) }}
                                    >
                                      <span className="ai-composer-provider-route-label is-badge">{g.label}</span>
                                    {(() => {
                                      const keys = iconKeysForModels(routeModelLabels(groups ?? [], g.routeId), 3)
                                      return keys.length ? (
                                        <span className="ai-composer-icon-stack" aria-hidden="true">
                                          {keys.map(k => <ProviderIcon key={k} id={k} size={12} />)}
                                        </span>
                                      ) : null
                                    })()}
                                    {g.bubbledActivity && (
                                        <DispatchActivityBadge activity={g.bubbledActivity} t={t as any} />
                                      )}
                                      {routeActive && <Check size={13} />}
                                    </button>
                                    {(g.items.length > 0) && (
                                      <button
                                        className="ai-composer-provider-route-toggle"
                                        type="button"
                                        onClick={() => toggleExpand(g.routeId)}
                                      >
                                        <ChevronDown size={12} className={expanded ? 'expanded' : ''} />
                                      </button>
                                    )}
                                  </div>
                                  {expanded && (() => {
                                    const nodes: React.ReactNode[] = []
                                    let pendingRefs: RefOption[] = []
                                    let pendingKey = g.routeId
                                    const flush = () => {
                                      if (pendingRefs.length > 0) {
                                        nodes.push(<React.Fragment key={`refs-${pendingKey}`}>{renderTreeRefs(pendingRefs, 0, new Set([g.routeId]), pendingKey, g.routeId)}</React.Fragment>)
                                        pendingRefs = []
                                      }
                                    }
                                    g.items.forEach((item, idx) => {
                                      if (item.kind === 'ref') {
                                        if (pendingRefs.length === 0) pendingKey = `${g.routeId}-${idx}`
                                        pendingRefs.push(item.option)
                                        return
                                      }
                                      flush()
                                      const m = item.option
                                      const current = routeActive ? (pinnedUnit ?? currentUnit) : null
                                      const unitActive = !!current
                                        && !!m.unit
                                        && m.unit.model === current.model
                                        && (m.unit.provider ?? '') === (current.provider ?? '')
                                      nodes.push(
                                        <CooldownProviderOption
                                          key={`${g.routeId}-${idx}-${m.id}`}
                                          option={m}
                                          active={unitActive}
                                          nowMs={providerNowMs}
                                          onSelect={() => { onSelectUnit?.(g.routeId, m); setProviderOpen(false) }}
                                          t={t as any}
                                        />,
                                      )
                                    })
                                    flush()
                                    return nodes
                                  })()}
                                </React.Fragment>
                              )
                            })}
                            {/* Auto's models are shown flat (no Auto header) at the bottom,
                                grouped alongside the custom aggregators. */}
                            {autoGroup && autoGroup.models.length > 0 && (
                              <div className="ai-composer-provider-auto-group">
                                {/* Auto header mirrors the custom route rows so
                                    [auto] routing gets the same route checkmark
                                    and an explicit way back to auto. */}
                                <div className="ai-composer-provider-route-row">
                                  <button
                                    className={`ai-composer-provider-route ${activeRouteId === autoGroup.routeId ? 'active' : ''}`}
                                    type="button"
                                    onClick={() => { onSelectRoute?.(autoGroup.routeId); setProviderOpen(false) }}
                                  >
                                    <span className="ai-composer-provider-route-label is-badge">{t('dialog.newAgent.autoFirst') || 'Auto'}</span>
                                    {(() => {
                                      const keys = iconKeysForModels(routeModelLabels(groups ?? [], autoGroup.routeId), 3)
                                      return keys.length ? (
                                        <span className="ai-composer-icon-stack" aria-hidden="true">
                                          {keys.map(k => <ProviderIcon key={k} id={k} size={12} />)}
                                        </span>
                                      ) : null
                                    })()}
                                    {activeRouteId === autoGroup.routeId && <Check size={13} />}
                                  </button>
                                </div>
                                {autoGroup.models.map(m => {
                                  const current = activeRouteId === autoGroup.routeId ? (pinnedUnit ?? currentUnit) : null
                                  const unitActive = !!current
                                    && !!m.unit
                                    && m.unit.model === current.model
                                    && (m.unit.provider ?? '') === (current.provider ?? '')
                                  return (
                                    <CooldownProviderOption
                                      key={m.id}
                                      option={m}
                                      active={unitActive}
                                      nowMs={providerNowMs}
                                      onSelect={() => { onSelectUnit?.(autoGroup.routeId, m); setProviderOpen(false) }}
                                      t={t as any}
                                    />
                                  )
                                })}
                              </div>
                            )}
                          </div>
                        )
                      })() : (
                        <div className="ai-composer-provider-col">
                          <div className="ai-composer-provider-col-header">Model</div>
                          {providers.map(p => (
                            <CooldownProviderOption
                              key={p.id}
                              option={p}
                              active={p.id === activeProviderId}
                              nowMs={providerNowMs}
                              onSelect={() => { onProviderChange?.(p.id); setProviderOpen(false) }}
                              t={t as any}
                            />
                          ))}
                        </div>
                      )}
                      <div className="ai-composer-thinking-col">
                        <div className="ai-composer-provider-col-header">Thinking</div>
                        {THINKING_OPTIONS.map(opt => (
                          <button
                            key={`${opt.Mode}-${opt.Effort ?? ''}`}
                            className={`ai-composer-thinking-item ${isThinkingActive(opt) ? 'active' : ''}`}
                            type="button"
                            onClick={() => onThinkingLevelChange?.(opt)}
                          >
                            <span>{opt.Mode === 'none' ? 'none' : opt.Effort ?? 'none'}</span>
                            {isThinkingActive(opt) && <Check size={14} />}
                          </button>
                        ))}
                      </div>
                    </div>
                  </>
                )}
                {slotMenuOpen && slotMenu && (
                  <ProviderSlotMenu
                    open
                    onClose={() => setSlotMenuOpen(false)}
                    slots={slotMenu.slots}
                    groups={slotMenu.groups}
                    options={slotMenu.options}
                    onSelectSlot={slotMenu.onSelectSlot}
                  />
                )}
              </div>
            )}
            {!isRecording && rightSlot}
            {!isRecording && leftSlot}
            {onVoiceResult && !isRecording && !isStopping && (
              <button
                className="ai-composer-send visible"
                onClick={handleMicButtonClick}
                type="button"
                title="Voice input"
              >
                <Mic size={16} />
              </button>
            )}
            {renderRightButton()}
          </div>
        </div>

        {/* Agent avatars belong to the composer surface, below the action row. */}
        {avatarBarVisible && avatarBarSlot && (
          <div className="ai-composer-below">
            {avatarBarSlot}
          </div>
        )}
      </div>

      {/* Footer slot for project switcher and git controls */}
      {footer && (
        <div className="ai-composer-footer">
          {footer}
        </div>
      )}
    </div>
    {/* Config-bundle capsule tooltip, portaled to body so it floats above the
        composer surface instead of being clipped by the badge panel's overflow. */}
    {configBadgesVisible && groupTooltipOpen && configBadges.some(b => b.tools?.length) && groupTooltipRect && createPortal(
      <div
        className="ai-composer-badge-group-tooltip"
        role="tooltip"
        onMouseEnter={openGroupTooltip}
        onMouseLeave={scheduleCloseGroupTooltip}
        style={{ position: 'fixed', left: groupTooltipRect.left, top: groupTooltipRect.top - 8, transform: 'translateY(-100%)' }}
      >
        {configBadges
          .filter(b => b.tools?.length)
          .map((bundle, bi) => (
            <span key={bi} className="ai-composer-badge-group-tooltip-bundle">
              <span className="ai-composer-badge-group-tooltip-bundle-head">
                <CardIcon name={bundle.icon} size={13} color={bundle.color} />
                <span className="ai-composer-badge-group-tooltip-bundle-title">
                  {bundle.label || bundle.title}
                </span>
                <span className="ai-composer-badge-group-tooltip-count">
                  {bundle.tools!.length}
                </span>
              </span>
              <span className="ai-composer-badge-group-tooltip-tools">
                {bundle.tools!.map((tool, ti) => (
                  <span key={ti} className="ai-composer-badge-group-tooltip-tool">
                    <span className="ai-composer-badge-group-tooltip-tool-row">
                      <span className="ai-composer-badge-group-tooltip-tool-name">
                        {tool.name}
                      </span>
                      {tool.params && tool.params.length > 0 && (
                        <span className="ai-composer-badge-group-tooltip-tool-sig">
                          ({tool.params.map(p =>
                            p.required ? p.name : `${p.name}?`
                          ).join(', ')})
                        </span>
                      )}
                    </span>
                    {tool.description && (
                      <span className="ai-composer-badge-group-tooltip-tool-desc">
                        {tool.description}
                      </span>
                    )}
                  </span>
                ))}
              </span>
            </span>
          ))}
      </div>,
      document.body
    )}
    </>
  )
}
