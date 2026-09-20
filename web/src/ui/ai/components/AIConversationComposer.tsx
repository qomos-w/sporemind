import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Check, Plus, ChevronDown, Trash2, Package } from 'lucide-react'
import type { ProjectSnapshot } from '../../../domain/types'
import type { AttachmentEntry, ImageEntry } from '../../../gen-types/agent.chat'
import { useAIShellContext } from '../context/AIShellContext'
import { AIComposer, type PermissionMode, type ComposerBundleTool, type ComposerBadge } from './AIComposer'
import { useComposerHistory, COMPOSER_HISTORY_SCOPE } from '../hooks/useComposerHistory'
import { AgentAvatarBar } from './AgentAvatarBar'
import { GitQuickbar } from './GitQuickbar'
import { aggregateProjectStatus, type AgentInfo } from '../hooks/agentInfoStore'
import { avatarHue, agentDisplayName } from '../lib/agent-avatar'
import { AgentAvatarContent } from './AgentAvatarContent'
import { projectDisplayName } from '../../../application/project-adapter'
import { useI18n } from '../../../i18n'
import { client } from '../../../application/generated-client'
import { buildType } from '../../../config/buildConfig'
import * as agentComponentClient from '../../../gen-clients/local/client'
import { useAgentComponentMounts, isDeveloperOnlyBundleCard, isInsiderOnlyBundleCard, isDevBuildOnlyBundleCard } from '../hooks/useAgentComponentMounts'
import { useInsiderAccess } from '../hooks/useInsiderAccess'
import { getModeClickAction } from '../modeClickRegistry'
import { formatCardLinks, formatFileRefs } from '../hooks/useCardMention'
import { listBrowserWindows, onBrowserManagerEvent } from '../../../application/browser-manager'
import { ComponentBadgeContextMenu, type ComponentBadgeMenuTarget } from './ComponentBadgeContextMenu'
import { ComponentDetailOverlay } from './ComponentDetailOverlay'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { componentLabel, componentIcon, componentSlug, compactToken, localizedComponentLabel, localizedComponentDescription, localizedCardTitle } from './omniboxLabels'
import { subscribeModeMount, consumePendingModeMount } from './modeMountStore'
import { truncateLabel } from './timeline'
import './AIConversationPage.css'

export interface ConversationOption {
  id: string
  label: string
  isWorking?: boolean
  isError?: boolean
  statusLabel?: string
}

function renderQuickSwitchLabel(options: ConversationOption[], activeId: string | null, fallback: string): string {
  if (!activeId) return fallback
  return options.find(option => option.id === activeId)?.label ?? fallback
}

export interface QuickSwitchMenuProps {
  id: string
  value: string | null
  options: ConversationOption[]
  disabled?: boolean
  onChange: (id: string) => void
  action?: { label: string; onClick: () => void }
  emptyLabel?: string
  projectAgentsMap?: Map<string, { AgentId: string; DisplayName: string; AgentKind: string; Title?: string }[]>
  header?: string
  onDeleteOption?: (id: string) => void
  secondaryColumn?: {
    header: string
    options: ConversationOption[]
    value: string | null
    onChange: (id: string) => void
    action?: { label: string; onClick: () => void }
    emptyLabel?: string
  }
}

export function QuickSwitchMenu({
  id,
  value,
  options,
  disabled,
  onChange,
  action,
  emptyLabel = 'no agent',
  projectAgentsMap,
  header,
  onDeleteOption,
  secondaryColumn,
}: QuickSwitchMenuProps) {
  const [open, setOpen] = useState(false)
  const wrapperRef = useRef<HTMLDivElement | null>(null)
  const dropdownRef = useRef<HTMLDivElement | null>(null)
  const selectedLabel = renderQuickSwitchLabel(options, value, value == null ? emptyLabel : '')
  const { t } = useI18n()

  // Clamp the upward-popping dropdown to the space actually available above
  // the trigger, so tall lists never spill past the viewport top.
  useEffect(() => {
    if (!open) return
    const dropdown = dropdownRef.current
    const wrapper = wrapperRef.current
    if (!dropdown || !wrapper) return
    const spaceAbove = wrapper.getBoundingClientRect().top - 12
    const capped = Math.min(360, spaceAbove)
    dropdown.style.maxHeight = `${Math.max(120, capped)}px`
  }, [open])

  React.useEffect(() => {
    if (disabled) setOpen(false)
  }, [disabled])

  const renderOptions = (
    opts: ConversationOption[],
    activeValue: string | null,
    handleChange: (id: string) => void,
    showAgents?: boolean,
    colEmptyLabel?: string,
    colDeleteOption?: (id: string) => void,
  ) => (
    <>
      {opts.length === 0 ? (
        <div className="ai-conversation-quickbar-empty">{colEmptyLabel ?? emptyLabel}</div>
      ) : (
        opts.map(option => (
          <div
            key={option.id}
            className={`ai-conversation-quickbar-item ${option.id === activeValue ? 'active' : ''}`}
          >
            <button
              type="button"
              className="ai-conversation-quickbar-item-btn"
              onClick={() => {
                handleChange(option.id)
                setOpen(false)
              }}
            >
              <span className="ai-conversation-quickbar-item-main">
                {(option.isWorking || option.isError) && (
                  <span
                    className={`ai-conversation-quickbar-item-status${option.isWorking ? ' working' : ' error'}`}
                    title={option.statusLabel}
                  />
                )}
                <span className="ai-conversation-quickbar-item-label">{option.label}</span>
              </span>
              {showAgents && projectAgentsMap && (
                <span className="ai-conversation-quickbar-item-avatars">
                  {(projectAgentsMap.get(option.id) ?? []).slice(0, 3).map(a => (
                    <span
                      key={a.AgentId}
                      className={`ai-conversation-quickbar-item-avatar${(a.AgentKind ?? '').toLowerCase() === 'coordinator' ? ' coordinator' : ''}`}
                      style={{ '--avatar-hue': avatarHue(a.AgentId) } as React.CSSProperties}
                      title={agentDisplayName(a.Title, a.DisplayName)}
                    >
                      <AgentAvatarContent agent={a} size={12} mushroomScale={1} mushroomStemEnd={578} />
                    </span>
                  ))}
                  {(projectAgentsMap.get(option.id) ?? []).length > 3 && (
                    <span className="ai-conversation-quickbar-item-avatar-more">+</span>
                  )}
                </span>
              )}
              {option.id === activeValue && <Check size={14} />}
            </button>
            {colDeleteOption && (
              <button
                type="button"
                className="ai-conversation-quickbar-item-delete"
                onClick={(e) => { e.stopPropagation(); colDeleteOption(option.id) }}
                aria-label={t('common.delete')}
              >
                <Trash2 size={13} />
              </button>
            )}
          </div>
        ))
      )}
    </>
  )

  const renderAction = (act?: { label: string; onClick: () => void }) =>
    act ? (
      <>
        <div className="ai-conversation-quickbar-separator" />
        <button
          type="button"
          className="ai-conversation-quickbar-action-btn"
          onClick={() => {
            act.onClick()
            setOpen(false)
          }}
        >
          <Plus size={12} />
          <span>{act.label}</span>
        </button>
      </>
    ) : null

  return (
    <div className="ai-conversation-quickbar-menu" ref={wrapperRef}>
      <button
        id={id}
        type="button"
        className="ai-conversation-quickbar-trigger"
        aria-expanded={open}
        disabled={disabled}
        onClick={() => setOpen(current => !current)}
      >
        <span className="ai-conversation-quickbar-select-value">{selectedLabel}</span>
        <ChevronDown size={12} className="ai-conversation-quickbar-arrow" />
      </button>
      {open && (
        <>
          <div className="ai-conversation-quickbar-backdrop" onClick={() => setOpen(false)} />
          <div ref={dropdownRef} className={`ai-conversation-quickbar-dropdown ${secondaryColumn ? 'dual-col' : ''}`}>
            {secondaryColumn ? (
              <>
                <div className="ai-conversation-quickbar-col">
                  {header && (
                    <div className="ai-conversation-quickbar-col-header">{header}</div>
                  )}
                  {renderOptions(options, value, onChange, false)}
                  {renderAction(action)}
                </div>
                <div className="ai-conversation-quickbar-secondary-col">
                  <div className="ai-conversation-quickbar-col-header">{secondaryColumn.header}</div>
                  {renderOptions(secondaryColumn.options, secondaryColumn.value, secondaryColumn.onChange, false, secondaryColumn.emptyLabel, onDeleteOption)}
                  {renderAction(secondaryColumn.action)}
                </div>
              </>
            ) : (
              <>
                {renderOptions(options, value, onChange, false)}
                {renderAction(action)}
              </>
            )}
          </div>
        </>
      )}
    </div>
  )
}

export interface AIConversationComposerProps {
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
  projects: ProjectSnapshot[]
  activeProjectId: string | null
  onProjectChange: (id: string) => void
  projectSwitchingId: string | null
  contextLoading: boolean
  contextError: string | null
  conversations: ConversationOption[]
  activeConversationId: string | null
  onConversationChange: (id: string) => void
  onCreateProject?: () => void
  onCreateAgent?: () => void
  allAgents?: AgentInfo[]
  activeAgent?: AgentInfo | null
  onAgentAvatarClick?: (agent: AgentInfo) => void
  onAgentMenuOpen?: (agent: AgentInfo, x: number, y: number) => void
  onDeleteAgent?: (agent: AgentInfo) => void
  onOpenDiff?: (filePath: string, diffContent: string) => void
  permissionMode?: PermissionMode
  onPermissionModeChange?: (mode: PermissionMode) => void
  globalPermissionMode?: PermissionMode
  onGlobalPermissionModeChange?: (mode: PermissionMode) => void
  isMobile?: boolean
  hideAboveSlots?: boolean
  /** Whether config-group badge bundles are visible. */
  configBadgesVisible?: boolean
  /** Whether the quick git bar is shown in the composer above-right slot. Defaults to true. */
  quickGitVisible?: boolean
  /** Whether the agent name label is shown next to the project switcher. Defaults to true. */
  mobileContextAgentVisible?: boolean
  /** Open the global command/search omnibox. */
  onOpenOmnibox?: () => void
  /** Open a wiki card in the right panel (used by @-mention card badges). */
  onOpenCard?: (cardId: string, label: string) => void
  /** Open an independent browser instance's tab in the right panel (used by
   *  %-mention browser badges). */
  onOpenBrowserTab?: (instanceId: string, name: string, url: string) => void
  /** Open the full git mode panel. */
  onOpenGitMode?: () => void
  /** In drawer mode: clicking the "return to conversation" icon invokes this. */
  onReturnToConversation?: () => void
  /** Task-mode badge (prompt/template) for the active agent session when it is
   *  bound to a scheduler task card. Computed by the parent and injected into
   *  the composer badge row. */
  taskModeBadge?: ComposerBadge | null
  /** Called when the set of mounted mode/bundle card IDs changes so the parent
   *  can resolve a contextual composer placeholder. */
  onMountedModeCardIdsChange?: (cardIds: string[]) => void
  /** Developer mode surfaces the debug and plugin bundles in the slash
   *  panel; they stay hidden when false. */
  developerMode?: boolean
}

export const AIConversationComposer: React.FC<AIConversationComposerProps> = ({
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
  placeholder,
  projects,
  activeProjectId,
  onProjectChange,
  projectSwitchingId,
  contextLoading,
  contextError,
  conversations,
  activeConversationId,
  onConversationChange,
  onCreateProject,
  onCreateAgent,
  allAgents,
  activeAgent,
  onAgentAvatarClick,
  onAgentMenuOpen,
  onDeleteAgent,
  onOpenDiff,
  permissionMode,
  onPermissionModeChange,
  globalPermissionMode,
  onGlobalPermissionModeChange,
  isMobile: isMobileProp,
  hideAboveSlots = false,
  configBadgesVisible,
  quickGitVisible = true,
  mobileContextAgentVisible = true,
  onOpenOmnibox,
  onOpenCard,
  onOpenBrowserTab,
  onOpenGitMode,
  onReturnToConversation,
  taskModeBadge,
  onMountedModeCardIdsChange,
  developerMode = false,
}) => {
  const { providers, groups, activeRoute, currentUnit, activeProviderId, onProviderChange, onSelectRoute, onSelectUnit, activeThinkingLevel, onThinkingLevelChange, slotMenu } = useAIShellContext()
  const { history, setHistory } = useComposerHistory(COMPOSER_HISTORY_SCOPE)
  const activeProject = projects.find(p => p.ProjectID === activeProjectId)
  const { t } = useI18n()
  const { mounts, componentCards, componentTools, callablesById, mount, unmount, mcpStatusById } = useAgentComponentMounts(activeAgent?.ActorId, activeProjectId)
  const mountedCardIds = useMemo(() => new Set((mounts ?? []).map((m) => m.CardId)), [mounts])
  const mountedModeCardIds = useMemo(() =>
    (mounts ?? [])
      .filter(m => m.Enabled && m.Scope !== 'dependency' && m.Title && m.Icon)
      .map(m => m.CardId),
    [mounts],
  )
  useEffect(() => {
    onMountedModeCardIdsChange?.(mountedModeCardIds)
  }, [mountedModeCardIds, onMountedModeCardIdsChange])
  // Group the component-snapshot tools by their source bundle CardId and
  // enrich each with its callable schema (description + params) so the capsule
  // hover tooltip can show a user-friendly tool summary.
  const toolsByCard = useMemo(() => {
    const pretty = (id: string) =>
      id.replace(/^.*[:.]/, '').replace(/[-_]/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
    const map = new Map<string, ComposerBundleTool[]>()
    for (const tool of componentTools ?? []) {
      if (!tool.CardId) continue
      const ci = callablesById.get(tool.CallableId)
      const enriched: ComposerBundleTool = {
        id: tool.CallableId,
        name: ci?.Name ? pretty(ci.Name) : pretty(tool.CallableId),
        description: ci?.Description,
        params: ci?.Params?.map(p => ({
          name: p.Name,
          type: p.Type,
          required: p.Required,
          description: p.Description,
        })),
      }
      const list = map.get(tool.CardId)
      if (list) list.push(enriched)
      else map.set(tool.CardId, [enriched])
    }
    return map
  }, [componentTools, callablesById])
  const [memoryUnloadOpen, setMemoryUnloadOpen] = useState(false)
  const [memoryUnloadLoading, setMemoryUnloadLoading] = useState(false)
  const [worktreeDiscardOpen, setWorktreeDiscardOpen] = useState(false)
  const [worktreeDiscardLoading, setWorktreeDiscardLoading] = useState(false)
  // Right-click target of a mode/bundle badge; its "details" entry opens the
  // capability overlay for the card.
  const [badgeMenu, setBadgeMenu] = useState<ComponentBadgeMenuTarget | null>(null)
  const [detailCard, setDetailCard] = useState<{ cardId: string; label: string; icon?: string; color?: string } | null>(null)

  // Cards selected via #-mention that will be appended as [[CardId]] wiki links
  // to the outgoing message and rendered as closable badges in the composer.
  const [mentionedCards, setMentionedCards] = useState<{ id: string; title: string }[]>([])
  const handleMentionCard = useCallback((cardId: string, cardTitle: string) => {
    setMentionedCards(prev =>
      prev.some(c => c.id === cardId) ? prev : [...prev, { id: cardId, title: cardTitle || cardId }],
    )
  }, [])
  const removeMentionedCard = useCallback((cardId: string) => {
    setMentionedCards(prev => prev.filter(c => c.id !== cardId))
  }, [])
  // Files selected via $-mention: project-relative paths appended to the
  // outgoing message (backticked) and rendered as closable badges, mirroring
  // the card badges above.
  const [mentionedFiles, setMentionedFiles] = useState<string[]>([])
  const handleMentionFile = useCallback((path: string) => {
    setMentionedFiles(prev => prev.includes(path) ? prev : [...prev, path])
  }, [])
  const removeMentionedFile = useCallback((path: string) => {
    setMentionedFiles(prev => prev.filter(p => p !== path))
  }, [])
  // Agents selected via @-mention: mount the conversable tag card
  // (agent-chat:<agentId>) so the target becomes reachable as a persistent
  // mount chip in the mounts row above (already unmountable via the badge
  // close). Deliberately NOT part of the ephemeral mentionedCards /
  // mentionedFiles badge state, and skipped when the tag is already mounted.
  // The mounted tag is a one-way link (current agent → target): after a NEW
  // mount, offer the reverse link on the target via a confirm dialog.
  const [reverseLinkPending, setReverseLinkPending] = useState<{
    targetActorId: string
    targetName: string
    reverseCardId: string
  } | null>(null)
  const [reverseLinkLoading, setReverseLinkLoading] = useState(false)
  const handleMentionAgent = useCallback((agentId: string, displayName: string, targetActorId?: string) => {
    const cardId = `agent-chat:${agentId}`
    if (mountedCardIds.has(cardId)) return
    void mount(cardId)
    // Reverse card suffix is the current agent's ref Id; the backend resolver
    // also accepts the ActorId when the ref Id is absent.
    const selfId = activeAgent?.Id || activeAgent?.ActorId
    if (!targetActorId || !selfId) return
    const reverseCardId = `agent-chat:${selfId}`
    void (async () => {
      // Suppress the prompt when the target already carries the reverse
      // link. Best-effort: on lookup failure still ask — a duplicate
      // confirm-mount is idempotent server-side.
      try {
        const resp = await agentComponentClient.componentList(client, {}, { target: targetActorId })
        if ((resp.Items ?? []).some((m) => m.CardId === reverseCardId)) return
      } catch { /* ask anyway */ }
      setReverseLinkPending({ targetActorId, targetName: displayName, reverseCardId })
    })()
  }, [mountedCardIds, mount, activeAgent])
  // Browsers selected via %-mention: mount the conversable tag card
  // (browser-chat:<instanceId>) like the agent-chat flow above. The badge is a
  // pure reflection of the backend mount list (per-agent), so it can never
  // leak onto another agent after a switch — name/url for the badge click come
  // from the live browser-manager instance map below.
  const [browserMetaById, setBrowserMetaById] = useState<Record<string, { name: string; url: string }>>({})
  useEffect(() => {
    let active = true
    const load = () => {
      listBrowserWindows()
        .then((instances) => {
          if (!active) return
          const map: Record<string, { name: string; url: string }> = {}
          for (const inst of instances) {
            map[inst.Config.Id] = {
              name: inst.Config.Name || inst.Config.Url || inst.Config.Id,
              url: inst.Status.Url || inst.Config.Url || '',
            }
          }
          setBrowserMetaById(map)
        })
        .catch(() => { /* badge falls back to the mount title */ })
    }
    load()
    const unsubscribe = onBrowserManagerEvent(load)
    return () => {
      active = false
      unsubscribe()
    }
  }, [])
  const handleMentionBrowser = useCallback((instanceId: string, _name: string, _url: string) => {
    const cardId = `browser-chat:${instanceId}`
    if (!mountedCardIds.has(cardId)) {
      void mount(cardId)
    }
  }, [mountedCardIds, mount])
  const confirmReverseLink = useCallback(async () => {
    if (!reverseLinkPending) return
    setReverseLinkLoading(true)
    try {
      await agentComponentClient.componentMount(
        client,
        { CardId: reverseLinkPending.reverseCardId, Scope: 'user' },
        { target: reverseLinkPending.targetActorId },
      )
      setReverseLinkPending(null)
    } finally {
      setReverseLinkLoading(false)
    }
  }, [reverseLinkPending])
  const onOpenCardRef = useRef(onOpenCard)
  onOpenCardRef.current = onOpenCard

  const handleEnterMode = useCallback((cardId: string) => {
    mount(cardId)
    onChange('')
  }, [mount, onChange])

  // Quick-start cards (QuickStartActions, ProjectNewChatPage) request a mode
  // mount through the module-level modeMountStore; consume the request here
  // when it is addressed to the agent this composer owns. Unlike the slash
  // path above, a card click must not consume the draft the user typed.
  const mountAgentActorId = activeAgent?.ActorId
  const handleQuickStartMount = useCallback((cardId: string) => {
    mount(cardId)
  }, [mount])
  useEffect(() => {
    return subscribeModeMount(() => {
      const request = consumePendingModeMount()
      if (request && request.agentActorId === mountAgentActorId) {
        handleQuickStartMount(request.cardId)
      }
    })
  }, [mountAgentActorId, handleQuickStartMount])
  const projectStatusMap = useMemo(() => aggregateProjectStatus(allAgents ?? []), [allAgents])
  // Insider-or-above account gate for %-browser-mention / browser-crawl.
  const insiderAccess = useInsiderAccess()
  // Dev-build-only bundles (data.devOnly, e.g. coordinator-wearable) are
  // offered only when the compile-time build type is dev.
  const isDevBuild = buildType === 'dev'
  const componentActions = useMemo(() => componentCards
    .filter(card => !mountedCardIds.has(card.Id))
    .filter(card => developerMode || !isDeveloperOnlyBundleCard(card))
    .filter(card => insiderAccess || !isInsiderOnlyBundleCard(card))
    .filter(card => isDevBuild || !isDevBuildOnlyBundleCard(card))
    .map(card => {
    const kind = card.Data?.componentKind
    // Localized display name; non-builtin cards (mcp:*, app-bundle:*) fall
    // back to their own title.
    const label = localizedComponentLabel(card, t)
    // Bundle titles carry spaces ("Web Search") which slash input cannot
    // express as one token; also match the hyphenated slug and its compact
    // form so "/web-search" and "/websearch" resolve in the slash menu. The
    // original English title stays matchable so both spellings work.
    const slug = componentSlug(card)
    const mcpServerId = card.Data?.mcpServerId
    const mcpStatus = typeof mcpServerId === 'string' ? mcpStatusById[mcpServerId] : undefined
    const statusHint = mcpStatus !== undefined
      ? (mcpStatus.Connected ? t('settings.mcp.status.connected') : t('settings.mcp.status.disconnected'))
      : ''
    // Localized intro when the card is a builtin bundle/mode; the generic
    // mount hint stays as the fallback for non-builtin cards. MCP bundles
    // append their live connection state.
    const intro = localizedComponentDescription(card, t) ?? t('omnibox.mountHint')
    const description = statusHint ? `${intro} — ${statusHint}` : intro
    return {
      id: `component:${card.Id}`,
      section: kind === 'bundle' ? 'bundle' as const : 'componentMode' as const,
      label,
      // Slash spelling shown as the trailing chip: typing "/<slug>" filters
      // straight to this entry. Only builtin cards get a chip — namespaced
      // third-party ids (mcp:*, app-bundle:*) have no clean slash spelling.
      shortcut: card.Id.startsWith('builtin:') ? `/${slug}` : undefined,
      description,
      icon: componentIcon(card, <Package size={16} />),
      keywords: [label, componentLabel(card), slug, compactToken(slug)],
      score: 0,
      run: () => { mount(card.Id); onChange('') },
    }
  }), [componentCards, mount, onChange, t, mountedCardIds, mcpStatusById, developerMode, insiderAccess, isDevBuild])
  const mountBadges = useMemo(() => {
    return (mounts ?? [])
      .filter(m => m.Enabled && m.Scope !== 'dependency' && (m.Title && m.Icon || m.CardId.startsWith('agent-chat:') || m.CardId.startsWith('browser-chat:')))
      .map(m => {
        // %-mention browser mounts: icon + name label + close. Clicking opens
        // the instance's right-panel browser tab; closing unmounts the
        // browser-chat:<instanceId> tag. Rendered from the backend mount list
        // (per-agent) — NOT local mention state — so a stale mention can never
        // project a badge onto another agent. Name/url come from the live
        // browser-manager instance map; a mount whose window no longer exists
        // still shows (title fallback) so its close X can unmount it.
        if (m.CardId.startsWith('browser-chat:')) {
          const instanceId = m.CardId.slice('browser-chat:'.length)
          const meta = browserMetaById[instanceId]
          return {
            icon: 'globe',
            title: meta?.name || m.Title || instanceId,
            label: meta?.name || m.Title || instanceId,
            onClick: meta ? () => onOpenBrowserTab?.(instanceId, meta.name, meta.url) : undefined,
            onClose: () => unmount(m.CardId),
          } as ComposerBadge
        }
        // Stale agent-chat mount: the target agent is unloaded/removed. After
        // a source-agent restart the descriptor cache is gone (empty Title/
        // Icon); in-memory the cached visuals survive, so also consult the
        // live workspace agent snapshot (LoadState / absence). Either way keep
        // the badge visible with red text so the user can unmount the dead
        // channel via its close X instead of the badge silently vanishing.
        if (m.CardId.startsWith('agent-chat:')) {
          const targetId = m.CardId.slice('agent-chat:'.length)
          const target = allAgents?.find(a => a.Id === targetId || a.ActorId === targetId)
          const agentGone = allAgents != null && allAgents.length > 0 && target == null
          const unloaded = target != null && target.LoadState !== 'loaded'
          if (!m.Title || !m.Icon || agentGone || unloaded) {
            const fallbackTitle = target?.DisplayName || target?.Title || m.Title || targetId
            return {
              icon: 'alert-circle',
              color: 'var(--text-danger, #ff5252)',
              title: fallbackTitle,
              stale: true,
              // Clicking a stale badge whose target still exists (merely
              // unloaded) switches to that agent — the parent's
              // navigateToAgent lazy-loads it via workspace.loadAgent before
              // selecting the session. A gone target stays inert; only the
              // close X applies.
              onClick: target ? () => onAgentAvatarClick?.(target) : undefined,
              onClose: () => unmount(m.CardId),
            } as ComposerBadge
          }
        }
        // Config-mounted bundles (Scope == "builtin" — seeded from the agent
        // kind config's DefaultBundleIDs plus builtin protected cards) are
        // ambient: they render icon-only and collapse into a single capsule.
        // User-mounted bundles ("user") and interactive modes ("system") keep
        // their title and close control.
        const configGroup = m.Scope === 'builtin'
        // Friendly label derived from the CardId (e.g. "builtin:bundle:file-tools"
        // → "File Tools") for the capsule tooltip; builtin bundle/mode cards use
        // their localized catalog name.
        const label = localizedCardTitle(
          m.CardId,
          m.Title && /[:.]/.test(m.Title)
            ? m.Title.replace(/^.*[:.]/, '').replace(/[-_]/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
            : (m.Title ?? ''),
          t,
        )
        // Icon color comes from the mount's card-library visual (data.visual.color);
        // CardIcon renders a colored lucide glyph, falling back to emoji/neutral.
        const color = m.Visual?.Color
        // Badge clicks carry the composer agent's ActorId so actions can target
        // "the current agent" (e.g. the workflow mode locates the map this
        // agent orchestrates, not any active workflow in the project).
        const agentActorId = activeAgent?.ActorId
        const modeAction = getModeClickAction(m.CardId)
        // Agent-chat mounts (from @-mention) switch to the linked agent on
        // click. The CardId suffix is the agent's stable ref Id; ActorId is
        // accepted as fallback, matching the backend reverse-link resolver.
        let onClick = modeAction ? () => modeAction({ agentActorId }) : undefined
        if (!onClick && m.CardId.startsWith('agent-chat:')) {
          const targetId = m.CardId.slice('agent-chat:'.length)
          const target = allAgents?.find(a => a.Id === targetId || a.ActorId === targetId)
          if (target) onClick = () => onAgentAvatarClick?.(target)
        }
        // Mode/bundle badges open a shared right-click menu whose "details"
        // entry opens the capability overlay for the card. Other kinds
        // (agent-chat tags, plain prompt mounts) keep the default menu.
        const onContextMenu = m.Kind === 'bundle' || m.Kind === 'mode'
          ? (x: number, y: number) => setBadgeMenu({ cardId: m.CardId, label, x, y })
          : undefined
        // The worktree mode badge's close control is a guarded discard:
        // unmounting the card discards the bound worktree (destructive), so
        // the X opens a warning dialog first; only a confirmed discard
        // reaches the backend unmount path.
        if (m.CardId === 'builtin:mode:worktree') {
          return {
            icon: m.Icon!,
            color,
            title: m.Title!,
            onClick,
            onContextMenu,
            onClose: () => setWorktreeDiscardOpen(true),
          }
        }
        return {
          icon: m.Icon!,
          color,
          title: m.Title!,
          label,
          configGroup,
          tools: configGroup ? toolsByCard.get(m.CardId) : undefined,
          onClick,
          onContextMenu,
          onClose: m.CardId === 'builtin:mode:memory'
            ? () => setMemoryUnloadOpen(true)
            : () => unmount(m.CardId),
        }
      })
  }, [mounts, unmount, toolsByCard, activeAgent?.ActorId, t, allAgents, onAgentAvatarClick, browserMetaById, onOpenBrowserTab])

  // @-mention card badges: icon + label + close. Clicking opens the card in the
  // right panel (reusing the parent's onOpenCard); closing removes it from the
  // mentioned set so it won't be appended as a [[CardId]] link on send.
  const mentionBadges = useMemo<ComposerBadge[]>(() =>
    mentionedCards.map(c => ({
      icon: 'file-text',
      title: c.id,
      label: c.title,
      onClick: () => onOpenCardRef.current?.(c.id, c.title),
      onClose: () => removeMentionedCard(c.id),
    })),
  [mentionedCards, removeMentionedCard])

  // $-mention file badges: icon + basename label + close, mirroring the card
  // badges. Closing removes the path so it won't be appended on send.
  const fileBadges = useMemo<ComposerBadge[]>(() =>
    mentionedFiles.map(p => ({
      icon: 'files',
      title: p,
      label: p.split(/[\\/]/).pop() ?? p,
      onClose: () => removeMentionedFile(p),
    })),
  [mentionedFiles, removeMentionedFile])

  // %-mention browser badges render from the backend mount list inside
  // mountBadges above (click → right-panel tab, close → unmount).

  // Parent badge: when the active agent is a child (has a ParentAgentId),
  // surface a clickable badge showing the parent's name. Clicking switches to
  // the parent agent via onAgentAvatarClick.
  const parentBadge = useMemo<ComposerBadge[]>(() => {
    const parentActorId = activeAgent?.ParentAgentId
    if (!parentActorId || !allAgents) return []
    const parent = allAgents.find(a => a.ActorId === parentActorId)
    if (!parent) return []
    return [{
      icon: 'corner-up-left',
      title: parent.DisplayName || parent.Title || parent.AgentKind || 'Parent',
      onClick: () => onAgentAvatarClick?.(parent),
    }]
  }, [activeAgent?.ParentAgentId, allAgents, onAgentAvatarClick])

  const badges = useMemo(() => [...(taskModeBadge ? [taskModeBadge] : []), ...parentBadge, ...mountBadges, ...mentionBadges, ...fileBadges], [taskModeBadge, parentBadge, mountBadges, mentionBadges, fileBadges])

  const projectAgentsMap = useMemo(() => {
    const map = new Map<string, { AgentId: string; DisplayName: string; AgentKind: string; Title?: string }[]>()
    if (!allAgents) return map
    for (const agent of allAgents) {
      const existing = map.get(agent.ProjectId)
      const entry = { AgentId: agent.Id, DisplayName: agent.DisplayName, AgentKind: agent.AgentKind, Title: agent.Title }
      if (existing) {
        existing.push(entry)
      } else {
        map.set(agent.ProjectId, [entry])
      }
    }
    return map
  }, [allAgents])

  const confirmMemoryUnload = async () => {
    setMemoryUnloadLoading(true)
    try {
      await unmount('builtin:mode:memory')
      setMemoryUnloadOpen(false)
    } finally {
      setMemoryUnloadLoading(false)
    }
  }

  // Confirmed worktree discard: the backend unmount path calls
  // project.worktree_exit(mode=discard, force) before removing the card, so
  // the worktree including uncommitted changes is deleted.
  const confirmWorktreeDiscard = async () => {
    setWorktreeDiscardLoading(true)
    try {
      await unmount('builtin:mode:worktree', true)
      setWorktreeDiscardOpen(false)
    } finally {
      setWorktreeDiscardLoading(false)
    }
  }

  // Wrap onSubmit so that any mentioned cards/files are appended to the
  // outgoing text ([[CardId]] wiki links / backticked file paths), then clear
  // the badges. The enriched text is passed through to the parent's original
  // handler.
  const handleSubmitWithCards = useCallback((text: string, attachments: AttachmentEntry[], images: ImageEntry[]) => {
    const ids = mentionedCards.map(c => c.id)
    const enriched = formatFileRefs(formatCardLinks(text, ids), mentionedFiles)
    if (ids.length > 0) setMentionedCards([])
    if (mentionedFiles.length > 0) setMentionedFiles([])
    if (onSubmit) {
      onSubmit(enriched, attachments, images)
    } else {
      onSend()
    }
  }, [onSubmit, onSend, mentionedCards, mentionedFiles])

  return (
    <>
    <AIComposer
      value={value}
      onChange={onChange}
      onSend={onSend}
      onSubmit={handleSubmitWithCards}
      onMentionCard={handleMentionCard}
      onMentionFile={handleMentionFile}
      onMentionAgent={handleMentionAgent}
      onMentionBrowser={handleMentionBrowser}
      isStreaming={isStreaming}
      isPaused={isPaused}
      isWaiting={isWaiting}
      isPausing={isPausing}
      onStop={onStop}
      onPause={onPause}
      onResume={onResume}
      disabled={disabled}
      placeholder={placeholder}
      providers={providers}
      activeProviderId={activeProviderId}
      onProviderChange={onProviderChange}
      groups={groups}
      activeRoute={activeRoute}
      currentUnit={currentUnit}
      onSelectRoute={onSelectRoute}
      onSelectUnit={onSelectUnit}
      activeThinkingLevel={activeThinkingLevel}
      onThinkingLevelChange={onThinkingLevelChange}
      slotMenu={slotMenu}
      onVoiceResult={(text) => onChange(value ? value + ' ' + text : text)}
      history={history}
      onHistoryChange={setHistory}
      onEnterMode={handleEnterMode}
      agentActorId={activeAgent?.ActorId ?? null}
      projectId={activeProjectId}
      badges={badges}
      configBadgesVisible={configBadgesVisible}
      aboveLeftSlot={hideAboveSlots ? undefined : (
        <>
          <QuickSwitchMenu
            id="ai-project-switcher"
            value={activeProjectId}
            options={(projects ?? []).map(project => {
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
            onChange={onProjectChange}
            action={onCreateProject ? { label: 'New Project', onClick: onCreateProject } : undefined}
            emptyLabel="no project"
            header="Project"
            projectAgentsMap={projectAgentsMap}
            onDeleteOption={(agentId) => {
              const a = allAgents?.find(x => x.Id === agentId)
              if (a) onDeleteAgent?.(a)
            }}
            secondaryColumn={{
              header: 'Agent',
              options: conversations,
              value: activeConversationId,
              onChange: onConversationChange,
              action: onCreateAgent ? { label: 'New Agent', onClick: onCreateAgent } : undefined,
              emptyLabel: 'no agent',
            }}
          />
          {mobileContextAgentVisible && activeProject && activeAgent && (
            <div className="ai-conversation-mobile-context">
              <span
                className="ai-conversation-mobile-context-agent"
                title={agentDisplayName(activeAgent.Title, activeAgent.DisplayName)}
              >
                {truncateLabel(agentDisplayName(activeAgent.Title, activeAgent.DisplayName), 40)}
              </span>
            </div>
          )}
        </>
      )}
      middleSlot={hideAboveSlots ? undefined : (
        <>
          {(contextLoading || contextError || projectSwitchingId) && (
            <div className="ai-conversation-quickbar-status">
              {projectSwitchingId ? 'Switching project…' : contextError ?? 'Loading context…'}
            </div>
          )}
        </>
      )}
      avatarBarSlot={
        allAgents ? (
          <AgentAvatarBar
            agents={allAgents}
            activeAgentId={activeConversationId}
            onAgentClick={onAgentAvatarClick ?? (() => {})}
            onAgentMenuOpen={onAgentMenuOpen}
            onDeleteAgent={onDeleteAgent}
            onCreateAgent={onCreateAgent}
          />
        ) : null
      }
      aboveRightSlot={hideAboveSlots ? undefined : (
        quickGitVisible ? (
          <GitQuickbar
            projectId={activeProjectId}
            onOpenDiff={onOpenDiff}
            onOpenGitMode={onOpenGitMode}
          />
        ) : null
      )}
      permissionMode={permissionMode}
      onPermissionModeChange={onPermissionModeChange}
      globalPermissionMode={globalPermissionMode}
      onGlobalPermissionModeChange={onGlobalPermissionModeChange}
      isMobile={isMobileProp}
      componentActions={componentActions}
      onOpenOmnibox={onOpenOmnibox}
      onDeleteCurrentAgent={activeAgent ? () => onDeleteAgent?.(activeAgent) : undefined}
      onCreateAgent={onCreateAgent}
      onReturnToConversation={onReturnToConversation}
    />
    <ConfirmDialog
      open={memoryUnloadOpen}
      title={t('dialog.memoryUnload.title')}
      description={t('dialog.memoryUnload.description')}
      confirmLabel={t('dialog.memoryUnload.confirm')}
      cancelLabel={t('common.cancel')}
      danger
      loading={memoryUnloadLoading}
      onConfirm={confirmMemoryUnload}
      onCancel={() => setMemoryUnloadOpen(false)}
    />
    <ConfirmDialog
      open={worktreeDiscardOpen}
      title={t('dialog.worktreeDiscard.title')}
      description={t('dialog.worktreeDiscard.description')}
      confirmLabel={t('dialog.worktreeDiscard.confirm')}
      cancelLabel={t('common.cancel')}
      danger
      loading={worktreeDiscardLoading}
      onConfirm={confirmWorktreeDiscard}
      onCancel={() => setWorktreeDiscardOpen(false)}
    />
    <ConfirmDialog
      open={!!reverseLinkPending}
      title={t('dialog.agentReverseLink.title')}
      description={t('dialog.agentReverseLink.description', { name: reverseLinkPending?.targetName ?? '' })}
      confirmLabel={t('dialog.agentReverseLink.confirm')}
      cancelLabel={t('dialog.agentReverseLink.cancel')}
      loading={reverseLinkLoading}
      onConfirm={confirmReverseLink}
      onCancel={() => setReverseLinkPending(null)}
    />
    <ComponentBadgeContextMenu
      target={badgeMenu}
      onClose={() => setBadgeMenu(null)}
      onOpenDetails={(cardId) => {
        const mount = (mounts ?? []).find(m => m.CardId === cardId)
        setDetailCard({
          cardId,
          label: badgeMenu?.label ?? cardId,
          icon: mount?.Icon,
          color: mount?.Visual?.Color,
        })
      }}
    />
    <ComponentDetailOverlay
      open={!!detailCard}
      cardId={detailCard?.cardId ?? null}
      projectId={activeProjectId}
      titleHint={detailCard?.label}
      iconHint={detailCard?.icon}
      colorHint={detailCard?.color}
      onClose={() => setDetailCard(null)}
    />
    </>
  )
}
