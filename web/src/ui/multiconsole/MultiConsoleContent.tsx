import React, { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { useAgentInfoList, type AgentInfo } from '../ai/hooks/agentInfoStore'
import { DeleteConfirmModal } from '../ai/components/parts/DeleteConfirmModal'
import { getTimelineManager, useTimelineManager } from '../ai/hooks/useTimelineManager'
import { updateAgentState } from '../ai/hooks/updateAgentState'
import { unitToSlot } from '../ai/hooks/modelSlot'
import { InteractionDispatchContext, type InteractionDispatchFn } from '../ai/hooks/useStepInteraction'
import * as agentTurnClient from '../../gen-clients/local/client'
import { getComposerDraft, setComposerDraft, clearComposerDraft } from '../ai/hooks/composerDraftStore'
import { useComposerHistory, COMPOSER_HISTORY_SCOPE } from '../ai/hooks/useComposerHistory'
import { addHistoryEntry, toggleHistoryFavorite, removeHistoryItem, fmtTime } from '../ai/components/AIComposer'
import { useBrowserOverlay } from '../ai/browserOverlay'
import { AgentAvatarItem } from '../ai/components/AgentAvatarBar'
import { AgentContextMenu } from '../ai/components/AgentContextMenu'
import type { AgentMenuTarget } from '../ai/components/AgentContextMenu'

import MessageStream from '../ai/components/MessageStream'
import { NewAgentDialog, type AgentDialogInput } from '../ai/components/NewAgentDialog'
import type { AgentChatSubmitReq } from '../../gen-types/agent.chat'
import { client } from '../../application/generated-client'
import { useSlotRegistry } from '../ai/hooks/useSlotRegistry'
import { FileReferenceProvider } from '../ai/components/parts/file-reference.tsx'
import { useAIShellProviders } from '../ai/hooks/useAIShellProviders'
import { ProviderIcon, iconKeyForModelRow } from '../ai/components/providerIcons'
import * as agent_chat from '../../gen-clients/local/client'
import * as workspace from '../../gen-clients/workspace/client'
import * as aimanager_aggregator from '../../gen-clients/aimanager/client'
import { agentDisplayName } from '../ai/lib/agent-avatar'
import { useRelativeTime } from '../lib/format-time'
import type { ProjectRef, RandomNameConfig } from '../../gen-clients/system/types'
import type { AggregatorDescriptor } from '../../gen-types/aigen'
import type { ProjectSnapshot } from '../../domain/types'
import { gitStore } from '../panels/git-store'
import { Check, ChevronDown, ChevronLeft, ChevronRight, GitBranch, ArrowUp, Square, Clock, Maximize, X, Download, Upload, Mic, LayoutGrid, Star, Trash2 } from 'lucide-react'
import { useScrollLock } from '../ai/hooks/useScrollLock'
import { useAgentOperations } from '../ai/hooks/useAgentOperations'
import { useI18n } from '../../i18n'
import { voiceAPI } from '../ai/voice-api'
import { getMultiConsoleGridConfig, saveMultiConsoleGridConfig } from '../../application/workspace-ui-state'
import '../ai/components/AIConversationPage.css'
import './MultiConsoleShell.css'
import { computeGridDimensions } from './gridLayout'
import { selectAutoArrangeAgents, selectConsoleAgents } from './consoleAgents'

type GridConfig = { cols: number; rows: number }

/**
 * Fixed vertical chrome each grid cell always consumes regardless of grid size:
 * the two-row header (~56px: padding 12 + 2 rows + gap + border) plus the
 * bottom composer (~40px: min-height 36 + padding). Drives the layout to favour
 * columns, since rows are "expensive" in height.
 */
const CELL_VERTICAL_CHROME = 96

function useTimelineState(agentActorId: string) {
  return useTimelineManager(agentActorId)
}

function CellComposer({ agent, active, isStreaming, onStop }: { agent: AgentInfo; active: boolean; isStreaming?: boolean; onStop?: () => void }) {
  const { t } = useI18n()
  const [text, setText] = useState(() => getComposerDraft(agent.Id))
  const [historyOpen, setHistoryOpen] = useState(false)
  const [isRecording, setIsRecording] = useState(false)
  const isRecordingRef = useRef(false)
  const manager = getTimelineManager()
  const providerCtx = useAIShellProviders(agent)
  const inputRef = useRef<HTMLInputElement>(null)
  const { history, setHistory } = useComposerHistory(COMPOSER_HISTORY_SCOPE)
  useBrowserOverlay(historyOpen)

  // Voice input — mirrors AIComposer: a ref-guarded setter drives the shared
  // voiceAPI state machine via setActive, never raw start/stop calls.
  const setVoiceActive = useCallback((active: boolean) => {
    if (isRecordingRef.current === active) return
    isRecordingRef.current = active
    setIsRecording(active)
  }, [])

  useEffect(() => {
    void voiceAPI.setActive(isRecording, isRecording ? {
      onResult: (voiceText) => {
        setText(voiceText)
        setComposerDraft(agent.Id, voiceText)
      },
      onError: (err) => {
        console.error('[Voice]', err)
        setVoiceActive(false)
      },
      onStop: () => setVoiceActive(false),
    } : undefined).catch((err) => {
      console.error('[Voice] transition failed:', err)
      setVoiceActive(false)
    })
  }, [isRecording, agent.Id, setVoiceActive])

  useEffect(() => {
    return () => { void voiceAPI.setActive(false) }
  }, [])

  const handleMicButtonClick = useCallback(() => {
    setVoiceActive(!isRecordingRef.current)
  }, [setVoiceActive])

  const send = async () => {
    const trimmed = text.trim()
    if (!trimmed || !active) return
    setHistory(addHistoryEntry(history, trimmed))
    const activeUnit = providerCtx.activeUnit
    manager.select(agent.ActorId)
    const tempId = manager.pushUserMessage(trimmed, agent.ActorId)
    setText('')
    clearComposerDraft(agent.Id)
    try {
      const request: AgentChatSubmitReq = {
        Text: trimmed,
        Unit: activeUnit ?? undefined,
        Title: '',
      }
      const resp = await agent_chat.chatSubmit(
        client,
        request,
        { target: agent.ActorId },
      )
      if (tempId && resp.MessageId) {
        manager.replaceUserMessageId(tempId, resp.MessageId, agent.ActorId)
      }
      if (resp.Idx != null && resp.Idx > 0 && resp.MessageId) {
        manager.updateUserMessageIdx(resp.MessageId, resp.Idx, agent.ActorId)
      }
    } catch (err) {
      console.error('multiconsole send failed:', err)
    }
  }

  const hasValue = text.trim().length > 0

  const toggleFavorite = useCallback((id: string) => {
    setHistory(toggleHistoryFavorite(history, id))
  }, [history, setHistory])

  const deleteHistoryItem = useCallback((id: string) => {
    setHistory(removeHistoryItem(history, id))
  }, [history, setHistory])

  const selectHistoryItem = useCallback((itemText: string) => {
    setText(itemText)
    setComposerDraft(agent.Id, itemText)
    setHistoryOpen(false)
    requestAnimationFrame(() => { inputRef.current?.focus() })
  }, [agent.Id])

  const renderRightButton = () => {
    if (isStreaming && hasValue) {
      return (
        <div className="multiconsole-cell-composer-right-group">
          <button
            className="multiconsole-cell-stop-compact"
            type="button"
            onClick={() => onStop?.()}
            onMouseDown={e => e.stopPropagation()}
            title="Stop"
          >
            <Square size={12} fill="currentColor" />
          </button>
          <button
            className="multiconsole-cell-send-icon visible"
            type="button"
            onClick={send}
            onMouseDown={e => e.stopPropagation()}
            title="Send"
          >
            <ArrowUp size={16} />
          </button>
        </div>
      )
    }
    if (isStreaming) {
      return (
        <button
          className="multiconsole-cell-stop-icon"
          type="button"
          onClick={() => onStop?.()}
          onMouseDown={e => e.stopPropagation()}
          title="Stop"
        >
          <Square size={14} fill="currentColor" />
        </button>
      )
    }
    if (hasValue) {
      return (
        <button
          className="multiconsole-cell-send-icon visible"
          type="button"
          onClick={send}
          onMouseDown={e => e.stopPropagation()}
          disabled={!hasValue}
          title="Send"
        >
          <ArrowUp size={16} />
        </button>
      )
    }
    return (
      <button
        className="multiconsole-cell-history-btn"
        type="button"
        onClick={() => setHistoryOpen(v => !v)}
        onMouseDown={e => e.stopPropagation()}
        title="History"
      >
        <Clock size={16} />
      </button>
    )
  }

  return (
    <div className="multiconsole-cell-composer">
      {active ? (
        <>
          <button
            className={`multiconsole-cell-voice-btn ${isRecording ? 'recording' : ''}`}
            type="button"
            onClick={handleMicButtonClick}
            onMouseDown={e => e.stopPropagation()}
            title={isRecording ? 'Stop recording' : 'Voice input'}
          >
            <Mic size={14} />
          </button>
          <div className={`multiconsole-cell-input-wrap ${isRecording ? 'recording' : ''}`}>
            <input
              ref={inputRef}
              className="multiconsole-cell-input"
              type="text"
              value={text}
              onChange={e => {
                setText(e.target.value)
                setComposerDraft(agent.Id, e.target.value)
              }}
              onMouseDown={e => e.stopPropagation()}
              onKeyDown={e => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault()
                  if (text.trim()) {
                    send()
                  } else if (isStreaming) {
                    onStop?.()
                  }
                }
              }}
              placeholder={isStreaming ? t('multiconsole.placeholder.interrupt') : t('multiconsole.placeholder.sendMessage')}
              readOnly={isRecording}
            />
            <div
              className="multiconsole-cell-voice-wave"
              onClick={() => setVoiceActive(false)}
              onMouseDown={e => e.stopPropagation()}
              title="Stop recording"
            >
              <div className="multiconsole-cell-voice-wave-bar" />
              <div className="multiconsole-cell-voice-wave-bar" />
              <div className="multiconsole-cell-voice-wave-bar" />
              <div className="multiconsole-cell-voice-wave-bar" />
              <div className="multiconsole-cell-voice-wave-bar" />
            </div>
          </div>
          {renderRightButton()}
          {historyOpen && (
            <>
              <div className="multiconsole-cell-history-backdrop" onClick={() => setHistoryOpen(false)} onMouseDown={e => e.stopPropagation()} />
              <div className="multiconsole-cell-history-dropdown">
                {history.length === 0 ? (
                  <div className="multiconsole-cell-history-empty">No history</div>
                ) : (
                  history.map(item => (
                    <div key={item.id} className="multiconsole-cell-history-item">
                      <button
                        className="multiconsole-cell-history-text"
                        type="button"
                        onClick={() => selectHistoryItem(item.text)}
                        onMouseDown={e => e.stopPropagation()}
                        title={item.text}
                      >
                        <span className="multiconsole-cell-history-text-label">{item.text}</span>
                        <span className="multiconsole-cell-history-text-time">{fmtTime(item.timestamp)}</span>
                      </button>
                      <button
                        className={`multiconsole-cell-history-fav ${item.isFavorite ? 'active' : ''}`}
                        type="button"
                        title={item.isFavorite ? 'Unfavorite' : 'Favorite'}
                        onClick={() => toggleFavorite(item.id)}
                        onMouseDown={e => e.stopPropagation()}
                      >
                        <Star size={12} fill={item.isFavorite ? 'currentColor' : 'none'} />
                      </button>
                      <button
                        className="multiconsole-cell-history-del"
                        type="button"
                        title="Delete"
                        onClick={() => deleteHistoryItem(item.id)}
                        onMouseDown={e => e.stopPropagation()}
                      >
                        <Trash2 size={12} />
                      </button>
                    </div>
                  ))
                )}
              </div>
            </>
          )}
        </>
      ) : (
        <div className="multiconsole-cell-composer-placeholder" />
      )}
    </div>
  )
}

function CellTopBar({ agent, isStreaming, onDelete, onMaximize, onExportHistory, onImportHistory, onDragStart, onContextMenu }: { agent: AgentInfo; isStreaming: boolean; onDelete: () => void; onMaximize?: () => void; onExportHistory?: () => void; onImportHistory?: () => void; onDragStart?: (event: React.DragEvent) => void; onContextMenu?: (event: React.MouseEvent) => void }) {
  const providerCtx = useAIShellProviders(agent)
  const [providerOpen, setProviderOpen] = useState(false)
  const [gitMenuOpen, setGitMenuOpen] = useState(false)
  const relativeTime = useRelativeTime()

  useSyncExternalStore(
    useCallback((cb: () => void) => gitStore.subscribe(cb, agent.ProjectId), [agent.ProjectId]),
    gitStore.getVersion,
  )

  const projectName = useMemo(() => {
    const project = gitStore.state.projects.find((p: ProjectSnapshot) => p.ProjectID === agent.ProjectId)
    return project?.Name ?? agent.ProjectId
  }, [agent.ProjectId])

  const activeProvider = providerCtx.providers.find(p => p.id === providerCtx.activeProviderId)

  const handleProviderChange = (id: string) => {
    const unit = providerCtx.providers.find(p => p.id === id)
    updateAgentState(agent.Id, {
      Primary: unit ? unitToSlot({ model: unit.label, provider: unit.subtitle ?? '' }) : null,
    }).catch(() => {})
    providerCtx.onProviderChange(id)
    setProviderOpen(false)
  }

  const handleGitClick = () => {
    gitStore.setContext({ projectId: agent.ProjectId, worktreeId: agent.WorktreeID })
    setGitMenuOpen(v => !v)
  }

  const gitBranch = gitStore.state.branch || null
  const gitChangeCount = (gitStore.state.projectId === agent.ProjectId && gitStore.state.worktreeId === (agent.WorktreeID || null)) ? gitStore.state.files.length : 0

  return (
    <div className="multiconsole-cell-header" draggable={!!onDragStart} onDragStart={onDragStart} onContextMenu={(event) => { event.preventDefault(); event.stopPropagation(); onContextMenu?.(event) }}>
      <div className="multiconsole-cell-header-row">
        <div className="multiconsole-cell-header-left">
          <span className="multiconsole-cell-title">
            {agentDisplayName(agent.Title, agent.DisplayName)}
            {agent.LastActivity ? (
              <span className="multiconsole-cell-title-time"> · {relativeTime(agent.LastActivity)}</span>
            ) : null}
          </span>
        </div>
        <div className="multiconsole-cell-header-right">
          {onExportHistory && (
            <button
              className="multiconsole-cell-maximize-btn"
              type="button"
              onClick={(e) => { e.stopPropagation(); onExportHistory() }}
              title="Export session history"
            >
              <Download size={12} />
            </button>
          )}
          {onImportHistory && (
            <button
              className="multiconsole-cell-maximize-btn"
              type="button"
              onClick={(e) => { e.stopPropagation(); onImportHistory() }}
              title="Import session history"
            >
              <Upload size={12} />
            </button>
          )}
          {onMaximize && (
            <button
              className="multiconsole-cell-maximize-btn"
              type="button"
              onClick={(e) => { e.stopPropagation(); onMaximize() }}
              title="Open in conversation"
            >
              <Maximize size={12} />
            </button>
          )}
          <button
            className="multiconsole-cell-close-btn"
            type="button"
            onClick={(e) => { e.stopPropagation(); onDelete() }}
            title="Close agent"
          >
            <X size={12} />
          </button>
        </div>
      </div>
      <div className="multiconsole-cell-header-row">
        <div className="multiconsole-cell-header-left">
          {isStreaming && <span className="multiconsole-cell-status">streaming</span>}
          <span className="multiconsole-cell-project">{projectName}</span>
        </div>
        <div className="multiconsole-cell-header-right">
          {providerCtx.providers.length > 0 && (
            <div className="multiconsole-cell-provider-wrap">
              <button
                className="multiconsole-cell-provider-btn"
                type="button"
                onClick={() => setProviderOpen(v => !v)}
              >
                {(() => { const k = iconKeyForModelRow(activeProvider?.label, activeProvider?.endpoint, activeProvider?.subtitle); return k ? <ProviderIcon id={k} size={13} /> : null })()}
                <span>{activeProvider?.label ?? 'Select'}</span>
                {activeProvider?.subtitle && (
                  <span className="multiconsole-cell-provider-btn-subtitle">{activeProvider.subtitle}</span>
                )}
                <ChevronDown size={12} />
              </button>
              {providerOpen && (
                <>
                  <div className="multiconsole-cell-provider-backdrop" onClick={() => setProviderOpen(false)} onMouseDown={e => e.stopPropagation()} />
                  <div className="multiconsole-cell-provider-dropdown">
                    {providerCtx.providers.map(p => (
                      <button
                        key={p.id}
                        className={`multiconsole-cell-provider-item ${p.id === providerCtx.activeProviderId ? 'active' : ''}`}
                        type="button"
                        onClick={() => handleProviderChange(p.id)}
                      >
                        {(() => { const k = iconKeyForModelRow(p.label, p.endpoint, p.subtitle); return k ? <ProviderIcon id={k} size={13} /> : null })()}
                        <span className="multiconsole-cell-provider-item-label">{p.label}</span>
                        {p.subtitle && <span className="multiconsole-cell-provider-item-subtitle">{p.subtitle}</span>}
                        {p.id === providerCtx.activeProviderId && <Check size={14} />}
                      </button>
                    ))}
                  </div>
                </>
              )}
            </div>
          )}

          <button className="multiconsole-cell-git-btn" type="button" onClick={handleGitClick}>
            <GitBranch size={10} />
            {gitBranch && <span className="multiconsole-cell-git-branch">{gitBranch}</span>}
            {gitChangeCount > 0 && (
              <span className="multiconsole-cell-git-badge">{gitChangeCount}</span>
            )}
          </button>
          {gitMenuOpen && (
            <>
              <div className="multiconsole-cell-git-backdrop" onClick={() => setGitMenuOpen(false)} onMouseDown={e => e.stopPropagation()} />
              <div className="multiconsole-cell-git-dropdown">
                <button className="multiconsole-cell-git-item" type="button" onClick={() => { gitStore.refresh(); setGitMenuOpen(false) }}>
                  Refresh Status
                </button>
                <button className="multiconsole-cell-git-item" type="button" onClick={() => { gitStore.stageAll(); setGitMenuOpen(false) }}>
                  Stage All
                </button>
                <button className="multiconsole-cell-git-item" type="button" onClick={() => { gitStore.push(); setGitMenuOpen(false) }}>
                  Push
                </button>
                <button className="multiconsole-cell-git-item" type="button" onClick={() => { gitStore.pull(); setGitMenuOpen(false) }}>
                  Pull
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

interface CellProps {
  agent: AgentInfo
  selected: boolean
  onSelect: () => void
  onDelete: () => void
  onMaximize?: () => void
  onExportHistory?: () => void
  onImportHistory?: () => void
  onDrop?: (event: React.DragEvent) => void
  onDragStart?: (event: React.DragEvent) => void
  onContextMenu?: (event: React.MouseEvent) => void
}

const Cell = React.memo(function Cell({ agent, selected, onSelect, onDelete, onMaximize, onExportHistory, onImportHistory, onDrop, onDragStart, onContextMenu }: CellProps) {
  const state = useTimelineState(agent.ActorId)
  const streamRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)

  const { canScrollDown, scrollToBottom } = useScrollLock(streamRef, state.isStreaming, contentRef, agent.ActorId)
  const { slots, registerFrames } = useSlotRegistry()

  useEffect(() => {
    registerFrames(state.envelopes.flatMap(envelope => envelope.frames))
  }, [registerFrames, state.envelopes])

  const handleStop = useCallback(() => {
    getTimelineManager().stop(agent.ActorId)
  }, [agent.ActorId])

  const dispatchInteraction = useCallback<InteractionDispatchFn>(async (event) => {
    getTimelineManager().dispatchLocalEvent(event, agent.ActorId)
    let answersJson: string
    switch (event.kind) {
      case 'ai.ask_answered':
        answersJson = JSON.stringify(event.answers)
        break
      case 'ai.permission_answered':
        answersJson = JSON.stringify({ allowed: event.allowed, allowInProject: event.allowInProject })
        break
      case 'ai.plan_approval_answered':
        answersJson = JSON.stringify({ decision: event.decision, editedPlan: event.editedPlan, feedback: event.feedback, selectedPolicy: event.selectedPolicy })
        break
      case 'ai.goal_submit_answered':
        answersJson = JSON.stringify({ decision: event.decision, feedback: event.feedback })
        break
      case 'ai.goal_card_submit_answered':
        answersJson = JSON.stringify({ decision: event.decision, feedback: event.feedback })
        break
    }
    try {
      await agentTurnClient.turnAnswer(client, { AnswersJson: answersJson, RequestId: event.requestId }, { target: agent.ActorId })
    } catch (err: unknown) {
      console.error('[MultiConsole] agent.turn.answer failed:', event.kind, err)
    }
  }, [agent.ActorId])

  return (
    <div
      className={`multiconsole-cell ${selected ? 'selected' : ''}`}
      onMouseDown={onSelect}
      onDragOver={(event) => {
        const types = Array.from(event.dataTransfer.types)
        if (types.includes('text/agent-id') || types.includes('text/multiconsole-slot')) event.preventDefault()
      }}
      onDrop={onDrop}
    >
      <CellTopBar agent={agent} isStreaming={state.isStreaming} onDelete={onDelete} onMaximize={onMaximize} onExportHistory={onExportHistory} onImportHistory={onImportHistory} onDragStart={onDragStart} onContextMenu={onContextMenu} />
      <div className="ai-conversation">
        <InteractionDispatchContext.Provider value={dispatchInteraction}>
          <FileReferenceProvider projectId={agent.ProjectId ?? null} projectRoot={null}>
            <MessageStream
              envelopes={state.envelopes}
              timelineLoading={state.loading}
              streamRef={streamRef}
              contentRef={contentRef}
              slots={slots}
              agentActorId={agent.ActorId}
              hasMoreHistory={state.hasMoreHistory}
              summarizedBoundary={state.summarizedBoundary}
              discardedBoundary={state.discardedBoundary}
              loadingMoreHistory={state.loadingMore}
              loadOlderTurns={() => getTimelineManager().loadOlderTurns(agent.ActorId)}
            />
          </FileReferenceProvider>
        </InteractionDispatchContext.Provider>
        <div className="ai-conversation-composer-area" onClick={(e) => e.stopPropagation()}>
          {canScrollDown && (
            <button className="ai-scroll-down-btn" type="button" onClick={() => scrollToBottom()}>
              <div className="ai-scroll-down-btn-inner">
                <ChevronDown size={14} />
              </div>
            </button>
          )}
          <CellComposer agent={agent} active={true} isStreaming={state.isStreaming} onStop={handleStop} />
        </div>
      </div>
    </div>
  )
}, (prev, next) => prev.agent.Id === next.agent.Id && prev.selected === next.selected && prev.onExportHistory === next.onExportHistory && prev.onImportHistory === next.onImportHistory)

interface AgentKindOption {
  kind: string
  displayName: string
  randomName: RandomNameConfig | undefined
  namePool?: string[]
}

interface MultiConsoleContentProps {
  activeSessionId?: string | null
  activeProjectId?: string | null
  onSwitchToAgent?: (agentId: string) => void
  onRequestProjectChange?: (projectId: string) => void
  onClearHistory?: (agent: AgentInfo) => void
  onInspectInfo?: (agent: AgentInfo) => void
}

export const MultiConsoleContent: React.FC<MultiConsoleContentProps> = ({ activeSessionId, activeProjectId, onSwitchToAgent, onRequestProjectChange, onClearHistory, onInspectInfo }) => {
  const { t } = useI18n()
  const agentInfoSnapshot = useAgentInfoList()
  const allAgents = agentInfoSnapshot.items
  const [gridConfig, setGridConfig] = useState<GridConfig>({ cols: 2, rows: 2 })
  const [gridConfigLoaded, setGridConfigLoaded] = useState(false)
  const [displayedAgentIds, setDisplayedAgentIds] = useState<Array<string | null>>([])
  const [dragOverSlot, setDragOverSlot] = useState<number | null>(null)
  const [dragActive, setDragActive] = useState(false)
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(null)
  const gridRef = useRef<HTMLDivElement>(null)
  const [gridContainerSize, setGridContainerSize] = useState<{ width: number; height: number } | null>(null)
  const [pendingGridAgent, setPendingGridAgent] = useState<{ id: string; index?: number } | null>(null)
  const [pendingCellIndex, setPendingCellIndex] = useState<number | null>(null)
  const filledSessionRef = useRef<string | null>(null)

  useEffect(() => {
    let cancelled = false
    void getMultiConsoleGridConfig().then(config => {
      if (cancelled) return
      setGridConfig(config)
      setDisplayedAgentIds(config.slots ? [...config.slots] : [])
      setGridConfigLoaded(true)
    })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    const el = gridRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const update = () => setGridContainerSize({ width: el.clientWidth, height: el.clientHeight })
    update()
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // New agent dialog state
  const [showDialog, setShowDialog] = useState(false)
  const [projects, setProjects] = useState<ProjectRef[]>([])
  const [aggregators, setAggregators] = useState<AggregatorDescriptor[]>([])
  const [agentKindOptions, setAgentKindOptions] = useState<AgentKindOption[]>([])
  const [dialogDataLoaded, setDialogDataLoaded] = useState(false)
  const [agentMenu, setAgentMenu] = useState<AgentMenuTarget | null>(null)
  const [dialogMode, setDialogMode] = useState<'create' | 'edit' | 'clone'>('create')
  const [editAgentInfo, setEditAgentInfo] = useState<AgentInfo | null>(null)
  const [cloneAgentInfo, setCloneAgentInfo] = useState<AgentInfo | null>(null)
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)
  const [agentToDelete, setAgentToDelete] = useState<AgentInfo | null>(null)
  const agentOps = useAgentOperations()

  const loadDialogData = useCallback(async () => {
    try {
      const [projectsResp, aggregatorsResp, kindsResp, configsResp] = await Promise.all([
        workspace.listProject(client),
        aimanager_aggregator.aggregatorList(client),
        workspace.listAgentKinds(client),
        workspace.listAgentKindConfigs(client),
      ])
      setProjects(projectsResp.Items ?? [])
      setAggregators(aggregatorsResp.Items)

      const configByKind = new Map(configsResp.Items?.map(config => [config.Kind, config]) ?? [])
      const options = (kindsResp.Items ?? [])
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
        .filter((option): option is AgentKindOption => option !== null)
      setAgentKindOptions(options)
      setDialogDataLoaded(true)
    } catch (err) {
      console.error('[MultiConsoleContent] load dialog data failed:', err)
    }
  }, [])

  const handleOpenDialog = useCallback((cellIndex?: number) => {
    setPendingCellIndex(cellIndex ?? null)
    if (!dialogDataLoaded) {
      void loadDialogData()
      setDialogMode('create')
      setEditAgentInfo(null)
      setCloneAgentInfo(null)
      setShowDialog(true)
      agentOps.clearError()
      return
    }
    const userProjects = projects.filter(project => !project.System)
    const currentProjectId = activeProjectId ?? projects.find(project => project.System)?.ActorId ?? ''
    const currentProjectAgents = allAgents.filter(agent => agent.ProjectId === currentProjectId)
    const creatableKinds = agentKindOptions
    const currentProjectFull = projects.find(project => project.ActorId === currentProjectId)?.System ||
      (creatableKinds.length > 0 && creatableKinds.every(option => currentProjectAgents.some(agent => agent.AgentKind === option.kind)))
    if (currentProjectFull && userProjects.length > 0) {
      const nextProject = userProjects.find(project => project.ActorId !== currentProjectId) ?? userProjects[0]
      if (nextProject?.ActorId) onRequestProjectChange?.(nextProject.ActorId)
    }
    setDialogMode('create')
    setEditAgentInfo(null)
    setCloneAgentInfo(null)
    setShowDialog(true)
    agentOps.clearError()
    void loadDialogData()
  }, [activeProjectId, agentOps, agentKindOptions, allAgents, dialogDataLoaded, loadDialogData, onRequestProjectChange, projects])

  const handleCloseDialog = useCallback(() => {
    if (agentOps.state.creating) return
    setShowDialog(false)
    agentOps.clearError()
    setEditAgentInfo(null)
    setCloneAgentInfo(null)
    setPendingCellIndex(null)
  }, [agentOps])

  const handleOpenAgentMenu = useCallback((agent: AgentInfo, x: number, y: number) => {
    setAgentMenu({ agent, x, y })
  }, [])

  const handleOpenAgentChat = useCallback((agent: AgentInfo) => {
    onSwitchToAgent?.(agent.Id)
  }, [onSwitchToAgent])

  const handleEditAgent = useCallback((agent: AgentInfo) => {
    setDialogMode('edit')
    setEditAgentInfo(agent)
    setCloneAgentInfo(null)
    setShowDialog(true)
    agentOps.clearError()
    void loadDialogData()
  }, [activeProjectId, agentOps, agentKindOptions, allAgents, dialogDataLoaded, loadDialogData, onRequestProjectChange, projects])

  const handleCloneAgent = useCallback((agent: AgentInfo) => {
    setDialogMode('clone')
    setCloneAgentInfo(agent)
    setEditAgentInfo(null)
    setShowDialog(true)
    agentOps.clearError()
    void loadDialogData()
  }, [activeProjectId, agentOps, agentKindOptions, allAgents, dialogDataLoaded, loadDialogData, onRequestProjectChange, projects])

  const handleDeleteAgent = useCallback((agent: AgentInfo) => {
    setAgentToDelete(agent)
    setDeleteConfirmOpen(true)
  }, [])

  const handleConfirmDelete = useCallback(async () => {
    if (!agentToDelete) return
    try {
      await agentOps.deleteAgent(agentToDelete)
      setDeleteConfirmOpen(false)
      setAgentToDelete(null)
    } catch (error) {
      console.error('[MultiConsoleContent] delete agent failed:', error)
    }
  }, [agentToDelete, agentOps])

  const defaultAgentKind = useMemo(() => agentKindOptions[0]?.kind || '', [agentKindOptions])

  useEffect(() => {
    if (!gridConfigLoaded) return
    void saveMultiConsoleGridConfig(gridConfig)
  }, [gridConfig, gridConfigLoaded])

  useEffect(() => {
    const onGrid = (e: Event) => {
      const detail = (e as CustomEvent<GridConfig>).detail
      setGridConfig(detail)
    }
    window.addEventListener('sporemind:multiconsole-grid', onGrid)
    return () => {
      window.removeEventListener('sporemind:multiconsole-grid', onGrid)
    }
  }, [])

  const [currentPage, setCurrentPage] = useState(0)
  const cellsPerPage = gridConfig.cols * gridConfig.rows
  const consoleAgents = useMemo(() => selectConsoleAgents(allAgents), [allAgents])
  const totalPages = Math.max(1, Math.ceil((Math.max(displayedAgentIds.length, cellsPerPage) + (displayedAgentIds.length > 0 && displayedAgentIds.length % cellsPerPage === 0 ? 1 : 0)) / cellsPerPage))
  const pageStart = currentPage * cellsPerPage
  const slots = useMemo(() => Array.from({ length: cellsPerPage }, (_, index) => displayedAgentIds[pageStart + index] ?? null), [displayedAgentIds, pageStart, cellsPerPage])
  const displayedAgents = useMemo(() => slots.map(id => id ? consoleAgents.find(agent => agent.Id === id) : null).filter((agent): agent is AgentInfo => !!agent), [slots, consoleAgents])

  useEffect(() => {
    setCurrentPage(page => Math.min(page, totalPages - 1))
  }, [totalPages])

  const autoArrangeRunningAgents = useCallback(() => {
    const arrangedAgents = selectAutoArrangeAgents(consoleAgents)
    const { cols, rows } = computeGridDimensions(
      arrangedAgents.length,
      gridContainerSize?.width ?? 0,
      gridContainerSize?.height ?? 0,
      CELL_VERTICAL_CHROME,
    )
    const slots = arrangedAgents.map(agent => agent.Id)
    const nextConfig = { cols, rows, slots }
    setGridConfig(nextConfig)
    setDisplayedAgentIds(slots)
    setCurrentPage(0)
    setSelectedAgentId(arrangedAgents[0]?.Id ?? null)
    void saveMultiConsoleGridConfig(nextConfig)
  }, [consoleAgents, gridContainerSize])

  const persistSlots = useCallback((next: Array<string | null>) => {
    setDisplayedAgentIds(previous => {
      const merged = [...previous]
      next.forEach((id, index) => { merged[pageStart + index] = id })
      void saveMultiConsoleGridConfig({ ...gridConfig, slots: merged })
      return merged
    })
  }, [gridConfig, pageStart])

  useEffect(() => {
    if (!gridConfigLoaded || agentInfoSnapshot.loading) return
    const normalized = displayedAgentIds.map(id => id && (agentInfoSnapshot.loading || consoleAgents.some(agent => agent.Id === id)) ? id : null)
    if (JSON.stringify(normalized) !== JSON.stringify(displayedAgentIds)) {
      setDisplayedAgentIds(normalized)
      void saveMultiConsoleGridConfig({ ...gridConfig, slots: normalized })
    }
  }, [gridConfigLoaded, agentInfoSnapshot.loading, consoleAgents, displayedAgentIds, gridConfig])

  useEffect(() => {
    if (!gridConfigLoaded || selectedAgentId) return
    const firstAgentId = slots.find(id => id !== null)
    if (firstAgentId) setSelectedAgentId(firstAgentId)
  }, [gridConfigLoaded, selectedAgentId, slots])

  useEffect(() => {
    if (!gridConfigLoaded || !activeSessionId || !consoleAgents.some(agent => agent.Id === activeSessionId)) return
    const sessionChanged = filledSessionRef.current !== activeSessionId
    filledSessionRef.current = activeSessionId
    if (displayedAgentIds.includes(activeSessionId)) {
      if (sessionChanged) setSelectedAgentId(activeSessionId)
      return
    }
    if (!sessionChanged) return
    const next = [...displayedAgentIds]
    const target = next.findIndex(id => id === null)
    const targetIndex = target >= 0 ? target : next.length
    next[targetIndex] = activeSessionId
    setDisplayedAgentIds(next)
    void saveMultiConsoleGridConfig({ ...gridConfig, slots: next })
    setSelectedAgentId(activeSessionId)
  }, [activeSessionId, consoleAgents, displayedAgentIds, gridConfigLoaded, gridConfig])

  const addAgentToGrid = useCallback(async (agentId: string, index?: number, replace = false) => {
    const agent = consoleAgents.find(item => item.Id === agentId)
    if (!agent) return
    if (agent.LoadState !== 'loaded') {
      await workspace.loadAgent(client, { AgentId: agent.Id })
    }
    const next = [...slots]
    if (index == null && next.includes(agentId)) return
    const existing = next.indexOf(agentId)
    if (existing >= 0) next[existing] = null
    const target = index ?? next.findIndex(id => id === null)
    if (target >= 0) {
      if (next[target] !== null && !replace) return
      next[target] = agentId
      persistSlots(next)
    } else if (index == null) {
      const fullSlots = [...displayedAgentIds, agentId]
      setDisplayedAgentIds(fullSlots)
      void saveMultiConsoleGridConfig({ ...gridConfig, slots: fullSlots })
      setCurrentPage(Math.floor((fullSlots.length - 1) / cellsPerPage))
    } else {
      return
    }
    setSelectedAgentId(agentId)
  }, [consoleAgents, slots, cellsPerPage, persistSlots, displayedAgentIds, gridConfig])

  useEffect(() => {
    if (!pendingGridAgent || !consoleAgents.some(agent => agent.Id === pendingGridAgent.id)) return
    setPendingGridAgent(null)
    void addAgentToGrid(pendingGridAgent.id, pendingGridAgent.index, pendingGridAgent.index != null)
  }, [pendingGridAgent, consoleAgents, addAgentToGrid])

  const addCreatedAgentToGrid = useCallback((agentId: string) => {
    const cellIndex = pendingCellIndex
    let target: number | undefined
    if (cellIndex != null && cellIndex >= 0) {
      target = cellIndex
    } else {
      const index = selectedAgentId ? slots.indexOf(selectedAgentId) : -1
      target = index >= 0 ? index : undefined
    }
    setPendingCellIndex(null)
    setPendingGridAgent({ id: agentId, index: target })
    void addAgentToGrid(agentId, target, target != null)
  }, [pendingCellIndex, selectedAgentId, slots, addAgentToGrid])

  const handleDialogSubmit = useCallback(async (input: AgentDialogInput) => {
    if (dialogMode === 'create') {
      const agent = await agentOps.createAgent(input, { agentKinds: agentKindOptions, aggregators })
      if (agent) {
        addCreatedAgentToGrid(agent.Id)
        setShowDialog(false)
        setEditAgentInfo(null)
        setCloneAgentInfo(null)
      }
    } else if (dialogMode === 'edit' && editAgentInfo) {
      await agentOps.updateAgent(input, { editAgent: editAgentInfo, aggregators })
      setShowDialog(false)
      setEditAgentInfo(null)
      setCloneAgentInfo(null)
    } else if (dialogMode === 'clone' && cloneAgentInfo) {
      const created = await agentOps.cloneAgent(input, { sourceAgent: cloneAgentInfo, aggregators })
      if (created) {
        addCreatedAgentToGrid(created.Id)
        setShowDialog(false)
        setEditAgentInfo(null)
        setCloneAgentInfo(null)
      }
    }
  }, [dialogMode, editAgentInfo, cloneAgentInfo, agentKindOptions, aggregators, agentOps, addCreatedAgentToGrid])

  const removeAgentFromGrid = useCallback((agentId: string) => {
    const next = slots.map(id => id === agentId ? null : id)
    persistSlots(next)
    setSelectedAgentId(id => id === agentId ? null : id)
  }, [slots, persistSlots])

  const handleCellDrop = useCallback((event: React.DragEvent, index: number) => {
    const sourceSlot = event.dataTransfer.getData('text/multiconsole-slot')
    const sourceIndex = Number(sourceSlot)
    if (sourceSlot !== '' && Number.isInteger(sourceIndex) && sourceIndex >= 0) {
      event.preventDefault()
      event.stopPropagation()
      const next: Array<string | null> = [...slots]
      const sourceAgentId = next[sourceIndex] ?? null
      const targetAgentId = next[index] ?? null
      if (sourceIndex !== index && next[index] === null) {
        next[index] = sourceAgentId
        next[sourceIndex] = null
        persistSlots(next)
      } else if (sourceIndex !== index) {
        ;[next[sourceIndex], next[index]] = [targetAgentId, sourceAgentId]
        persistSlots(next)
      }
      setDragOverSlot(null)
      return
    }
    const agentId = event.dataTransfer.getData('text/agent-id')
    if (!agentId) return
    event.preventDefault()
    event.stopPropagation()
    void addAgentToGrid(agentId, index)
  }, [addAgentToGrid, persistSlots, slots])

  const handleCellDragStart = useCallback((event: React.DragEvent, index: number) => {
    event.stopPropagation()
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/multiconsole-slot', String(index))
    setDragActive(true)
  }, [])

  useEffect(() => {
    const onAdd = (event: Event) => {
      const agentId = (event as CustomEvent<{ agentId: string }>).detail.agentId
      void addAgentToGrid(agentId)
    }
    window.addEventListener('sporemind:multiconsole-add-agent', onAdd)
    return () => window.removeEventListener('sporemind:multiconsole-add-agent', onAdd)
  }, [addAgentToGrid])

  const handleCellSelect = useCallback((agentId: string) => {
    setSelectedAgentId(agentId)
  }, [])

  return (
    <div
      className="multiconsole-content"
      onDragEnter={(event) => {
        const types = Array.from(event.dataTransfer.types)
        if (types.includes('text/agent-id') || types.includes('text/multiconsole-slot')) setDragActive(true)
      }}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node)) {
          setDragActive(false)
          setDragOverSlot(null)
        }
      }}
      onDrop={() => {
        setDragActive(false)
        setDragOverSlot(null)
      }}
    >
      <div
        ref={gridRef}
        className="multiconsole-grid"
        style={{
          gridTemplateColumns: `repeat(${gridConfig.cols}, 1fr)`,
          gridTemplateRows: `repeat(${gridConfig.rows}, 1fr)`,
        }}
      >
        {slots.map((agentId, index) => {
          const agent = agentId ? consoleAgents.find(item => item.Id === agentId) : null
          return agent ? (
          <Cell
            key={agent.Id}
            agent={agent}
            selected={selectedAgentId === agent.Id}
            onSelect={() => handleCellSelect(agent.Id)}
            onDelete={() => removeAgentFromGrid(agent.Id)}
            onDrop={(event) => handleCellDrop(event, index)}
            onDragStart={(event) => handleCellDragStart(event, index)}
            onContextMenu={(event) => { event.preventDefault(); event.stopPropagation(); handleOpenAgentMenu(agent, event.clientX, event.clientY) }}
            onMaximize={() => onSwitchToAgent?.(agent.Id)}
            onExportHistory={() => agentOps.exportHistory(
              getTimelineManager().getSnapshot(agent.ActorId).envelopes,
              agentDisplayName(agent.Title, agent.DisplayName)
            )}
            onImportHistory={() => {
              const input = document.createElement('input')
              input.type = 'file'
              input.accept = '.json'
              input.onchange = async (e) => {
                const file = (e.target as HTMLInputElement).files?.[0]
                if (file) {
                  await agentOps.importHistory(file, agent.ActorId)
                }
              }
              input.click()
            }}
          />
          ) : (
            <div
              key={`empty-${index}`}
              className={`multiconsole-cell multiconsole-cell-empty ${dragActive ? 'drag-active' : ''} ${dragOverSlot === index ? 'drag-over' : ''}`}
              onDragEnter={(event) => {
                if (Array.from(event.dataTransfer.types).includes('text/agent-id')) setDragOverSlot(index)
              }}
              onDragOver={(event) => {
                const types = Array.from(event.dataTransfer.types)
        if (types.includes('text/agent-id') || types.includes('text/multiconsole-slot')) event.preventDefault()
              }}
              onDragLeave={() => setDragOverSlot(null)}
              onDrop={(event) => { setDragOverSlot(null); handleCellDrop(event, index) }}
              onClick={() => handleOpenDialog(index)}
            >
              <div className="multiconsole-cell-add-inner">
                <span className="multiconsole-cell-add-icon">+</span>
                <span className="multiconsole-cell-add-label">{t('multiconsole.dragOrNew')}</span>
              </div>
            </div>
          )
        })}
        {false && (
          <div
            className="multiconsole-cell multiconsole-cell-add"
            onClick={() => handleOpenDialog()}
            onDragOver={(event) => {
              const types = Array.from(event.dataTransfer.types)
        if (types.includes('text/agent-id') || types.includes('text/multiconsole-slot')) event.preventDefault()
            }}
            onDrop={(event) => handleCellDrop(event, displayedAgents.length)}
          >
            <div className="multiconsole-cell-add-inner">
              <span className="multiconsole-cell-add-icon">+</span>
              <span className="multiconsole-cell-add-label">Add agent</span>
            </div>
          </div>
        )}
      </div>
      <div className="multiconsole-footer">
        <div className="multiconsole-footer-left">
          {selectedAgentId && (
            <span className="multiconsole-selection-info">{displayedAgents.length} displayed</span>
          )}
        </div>
        <div className="multiconsole-footer-center">
          <button type="button" className="multiconsole-page-button" disabled={currentPage === 0} onClick={() => setCurrentPage(page => page - 1)} aria-label="Previous page">
            <ChevronLeft size={16} />
          </button>
          <div className="multiconsole-footer-avatars">
            {slots.map((agentId, index) => {
              const agent = agentId ? consoleAgents.find(item => item.Id === agentId) : null
              return agent ? (
                <div key={`avatar-${agent.Id}`} className="multiconsole-slot-avatar" title={agentDisplayName(agent.Title, agent.DisplayName)} onContextMenu={(event) => { event.preventDefault(); event.stopPropagation(); handleOpenAgentMenu(agent, event.clientX, event.clientY) }}>
                  <AgentAvatarItem agent={agent} active={selectedAgentId === agent.Id} onClick={() => handleCellSelect(agent.Id)} onMenuOpen={(item, x, y) => handleOpenAgentMenu(item, x, y)} />
                </div>
              ) : <div key={`avatar-empty-${index}`} className="multiconsole-slot-avatar-wrap">
                <button type="button" className="agent-avatar-add" title="Add agent" aria-label="Add agent" onClick={() => handleOpenDialog()}>
                  <span>+</span>
                </button>
              </div>
            })}
          </div>
          <button type="button" className="multiconsole-page-button" disabled={currentPage >= totalPages - 1} onClick={() => setCurrentPage(page => page + 1)} aria-label="Next page">
            <ChevronRight size={16} />
          </button>
          <span className="multiconsole-page-indicator">{currentPage + 1}/{totalPages}</span>
        </div>
        <div className="multiconsole-footer-right">
          <button type="button" className="multiconsole-page-button" onClick={autoArrangeRunningAgents} aria-label="Arrange active agents" title="Arrange running agents and sessions active in the last 10 minutes">
            <LayoutGrid size={16} />
          </button>
        </div>
      </div>
      <NewAgentDialog
        open={showDialog}
        mode={dialogMode}
        editAgent={editAgentInfo ?? undefined}
        cloneSource={cloneAgentInfo ? {
          displayName: cloneAgentInfo.DisplayName,
          agentKind: cloneAgentInfo.AgentKind,
          projectId: cloneAgentInfo.ProjectId,
        } : undefined}
        projects={projects}
        aggregators={aggregators}
        agentKinds={agentKindOptions}
        agents={allAgents}
        defaultAgentKind={defaultAgentKind}
        defaultProjectId={activeProjectId ?? projects.filter(p => !p.System)[0]?.ActorId ?? ''}
        creating={agentOps.state.creating}
        error={agentOps.state.error || ''}
        onCancel={handleCloseDialog}
        onSubmit={handleDialogSubmit}
      />
      <AgentContextMenu
        target={agentMenu}
        onClose={() => setAgentMenu(null)}
        onOpenChat={handleOpenAgentChat}
        onEdit={handleEditAgent}
        onClone={handleCloneAgent}
        onClearHistory={onClearHistory}
        onInspectInfo={onInspectInfo}
        onDelete={handleDeleteAgent}
      />
      {deleteConfirmOpen && agentToDelete && (
        <DeleteConfirmModal
          open={deleteConfirmOpen}
          agentName={agentToDelete.Title || agentToDelete.DisplayName || 'Unknown'}
          requireTypeName={agentToDelete.MemoryMounted === true}
          hasWorktree={!!agentToDelete.WorktreeID}
          onClose={() => { setDeleteConfirmOpen(false); setAgentToDelete(null) }}
          onConfirm={handleConfirmDelete}
        />
      )}
    </div>
  )
}
