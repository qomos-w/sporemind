import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { AlertTriangle, Activity, Ban, Check, SquareCheck, Square, MoreHorizontal, FileCode, Pause, Target, GitBranch, Waypoints, Database, ExternalLink, FolderOpen, Copy, SquareArrowOutUpRight, TrendingUp, TrendingDown } from 'lucide-react'
import type { Frame, TurnEnvelope, TaskEntry, FileChangeEntry, ContextBudget, SessionGoal, ToolFrame, PendingSubmitEntry } from '../model/frame-types'
import { TimelineStep, useTurnTokenSpeed, useTokenThroughput, useTurnLiveDuration, formatTokenCount, formatDurationBadge, formatTimestamp, formatTimestampFull, splitTextToWaveChars, truncateLabel } from './timeline'
import { getToolKind } from './parts/tool-display'
import { useAnimatedNumber } from '../hooks/use-animated-number'
import { useAIShellContext } from '../context/AIShellContext'
import { useI18n } from '../../../i18n/provider'
import { useAgentInfo } from '../hooks/agentInfoStore'
import { jumpToWorktreeGit } from '../hooks/worktreeGitJump'
import { useMonoStore } from '../hooks/useMonoStore'
import { openWorkflowAndLocate } from './workflowLocateStore'
import { workflowTaskIds } from './workflowLayout'
import { STATUS_META } from './mono-card-meta'
import type { MonoCardListItem } from '../../../domain/mono-types'
import { useTurnTailMetricsVisible } from './turnTailMetricsStore'
import { isWails } from '../../../application/runtime'
import { useBrowserOverlay } from '../browserOverlay'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import { useFileReference } from './parts/file-reference'
import { openInSystem, revealInSystem } from './file-ops'
import type { I18nKey } from '../../../i18n/types'

function taskLabel(task: TaskEntry): string {
  return task.activeForm || task.subject
}

type TFunction = (key: I18nKey, params?: Record<string, string | number>) => string

function inferRunningTitle(envelope: TurnEnvelope, t: TFunction): string {
  const turnState = envelope.metadata?.turnState
  if (turnState === 'failed') return t('ai.step.error')

  const frames = envelope.frames

  const askUserFrame = frames.find((f): f is Extract<Frame, { type: 'ask_user' }> => f.type === 'ask_user' && f.status === 'running')
  if (askUserFrame) return t('ai.step.waitingForInput')

  const permFrame = frames.find((f): f is Extract<Frame, { type: 'permission_request' }> => f.type === 'permission_request' && f.status === 'running')
  if (permFrame) return t('ai.step.waitingForApproval')

  // A pending plan approval / goal confirmation also blocks the turn waiting
  // for the user. After a reload these frames stay "running", so recognize
  // them here instead of falling through to "thinking" (which would imply the
  // agent is actively working rather than waiting for input).
  const planFrame = frames.find((f): f is Extract<Frame, { type: 'plan' }> => f.type === 'plan' && f.status === 'running' && f.approvalStatus === 'pending')
  if (planFrame) return t('ai.step.waitingForApproval')

  const goalSubmitFrame = frames.find((f): f is Extract<Frame, { type: 'goal_submit' }> => f.type === 'goal_submit' && f.status === 'running' && f.approvalStatus === 'pending')
  if (goalSubmitFrame) return t('ai.step.waitingForApproval')

  const goalCardSubmitFrame = frames.find((f): f is Extract<Frame, { type: 'goal_card_submit' }> => f.type === 'goal_card_submit' && f.status === 'running' && f.approvalStatus === 'pending')
  if (goalCardSubmitFrame) return t('ai.step.waitingForApproval')

  // Check running goal_review frame for live progress stats.
  const goalReviewFrame = frames.find((f): f is Extract<Frame, { type: 'goal_review' }> => f.type === 'goal_review' && f.status === 'running')
  if (goalReviewFrame?.progress) {
    const p = goalReviewFrame.progress
    if (p.readCount != null && p.readCount > 0) {
      return t('ai.step.reviewingWithCount', { readCount: p.readCount, searchCount: p.searchCount ?? 0 })
    }
    if (p.phase) return t('ai.step.reviewingPhase', { phase: p.phase })
    return t('ai.step.reviewing')
  }

  // Check running fork tool frame. Distinguish by tool kind so each fork
  // variant shows its own title instead of a unified "exploring":
  // fork_explore → exploring, fork_review → reviewing, fork_general → parallel.
  const forkTool = frames.find((f): f is Extract<Frame, { type: 'tool' }> =>
    f.type === 'tool' && f.status === 'running' && ['explore', 'review', 'fork'].includes(getToolKind(f.toolName)))
  if (forkTool) {
    const kind = getToolKind(forkTool.toolName)
    const p = forkTool.progress
    if (kind === 'review') {
      if (p?.readCount != null && p.readCount > 0) return t('ai.step.reviewingWithCount', { readCount: p.readCount, searchCount: p.searchCount ?? 0 })
      if (p?.phase) return t('ai.step.reviewingPhase', { phase: p.phase })
      return t('ai.step.reviewing')
    }
    if (kind === 'fork') {
      return t('ai.step.parallelExecution')
    }
    if (p?.readCount != null && p.readCount > 0) return t('ai.step.exploringWithCount', { readCount: p.readCount, searchCount: p.searchCount ?? 0 })
    if (p?.phase) return t('ai.step.exploringPhase', { phase: p.phase })
    return t('ai.step.exploring')
  }

  const runningTool = frames.find((f): f is Extract<Frame, { type: 'tool' }> => f.type === 'tool' && f.status === 'running')
  if (runningTool) {
    const name = runningTool.toolName
    switch (name) {
      case 'project.read':
      case 'project.read_base64':
      case 'project.read_chunk': return t('ai.step.readingFile')
      case 'project.write': return t('ai.step.writingFile')
      case 'project.edit': return t('ai.step.editingFile')
      case 'project.rm': return t('ai.step.deletingFile')
      case 'project.grep': return t('ai.step.searchingCode')
      case 'project.glob': return t('ai.step.findingFiles')
      case 'project.list':
      case 'project.list_json': return t('ai.step.listingFiles')
      case 'project.shell_exec': return t('ai.step.runningCommand')
      case 'web_search': return t('ai.step.searching')
      case 'plan_submit':
      case 'plan.submit': return t('ai.step.submittingPlan')
      case 'create_task': return t('ai.step.creatingTask')
      case 'update_task': return t('ai.step.updatingTask')
      case 'bash': return t('ai.step.runningCommand')
      default: {
        const cid = runningTool.callableId ?? ''
        if (cid.includes('file') && cid.includes('read')) return t('ai.step.readingFile')
        if (cid.includes('file') && cid.includes('write')) return t('ai.step.writingFile')
        if (cid.includes('file') && cid.includes('edit')) return t('ai.step.editingFile')
        if (cid.includes('search')) return t('ai.step.searching')
        if (cid.includes('bash') || cid.includes('shell')) return t('ai.step.runningCommand')
        return name.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
      }
    }
  }

  const reasoningFrame = frames.find((f): f is Extract<Frame, { type: 'reasoning' }> => f.type === 'reasoning' && f.status === 'running')
  if (reasoningFrame) return t('ai.step.reasoning')

  // Dispatch-retry backoff: the turn engine is waiting between failed LLM
  // dispatch attempts. Surfacing the count here replaces the generic
  // "thinking" label with a live "retrying (n/m)" indicator.
  const retryCount = envelope.metadata?.retryCount
  if (retryCount && retryCount > 0) {
    const base = t('ai.step.retrying', { retry: retryCount, max: envelope.metadata?.retryMax ?? retryCount })
    const retryError = envelope.metadata?.retryError
    return retryError ? `${base} — ${retryError}` : base
  }

  const loopState = envelope.metadata?.loopState
  switch (loopState) {
    case 'WaitChild': return t('ai.step.exploring')
    case 'WaitUser': return t('ai.step.waitingForInput')
    case 'WaitPermission': return t('ai.step.waitingForApproval')
    case 'Audit': return t('ai.step.reviewing')
    case 'Dispatch': return t('ai.step.thinking')
    case 'Execute': return t('ai.step.executing')
    default: return t('ai.step.thinking')
  }
}

type TurnOutcome = 'success' | 'error' | 'cancelled' | 'waiting'

interface AssistantTurnSummary {
  actionCount: number
  failActionCount: number
  elapsedSeconds?: number
  model?: string
  icon: React.ReactNode
  title: string
  outcome: TurnOutcome | null
  isUserPaused: boolean
}

export function buildAssistantTurnSummary(envelope: TurnEnvelope, isComplete: boolean, turnState: string | undefined, t: TFunction): AssistantTurnSummary {
  const toolFrames = envelope.frames.filter((frame): frame is Extract<Frame, { type: 'tool' }> => frame.type === 'tool')
  const reasoningFrames = envelope.frames.filter((frame): frame is Extract<Frame, { type: 'reasoning' }> => frame.type === 'reasoning')
  const errorFrames = envelope.frames.filter(frame => frame.type === 'error')
  const lastFrame = envelope.frames[envelope.frames.length - 1]

  // User pause: turnState is 'paused' but no interaction frame is pending.
  // Interaction waits (ask_user / permission / plan / goal_submit) block the
  // turn waiting for the user and carry a running interaction frame — those
  // should keep the spinner rather than read as a user-initiated pause.
  const isUserPaused = turnState === 'paused' && !envelope.frames.some(f =>
    (f.type === 'ask_user' ||
      f.type === 'permission_request' ||
      f.type === 'plan' ||
      f.type === 'goal_submit' ||
      f.type === 'goal_card_submit') &&
    f.status === 'running'
  )

  const derivedActionCount = toolFrames.length
  const derivedElapsedSeconds = [...toolFrames, ...reasoningFrames].reduce((sum, frame) => {
    return sum + (frame.durationSeconds ?? 0)
  }, 0)

  const actionCount = envelope.metadata?.actionCount ?? derivedActionCount
  const elapsedSeconds = envelope.metadata?.elapsedSeconds
    ?? (envelope.metadata?.startedAt && envelope.metadata?.completedAt
      ? Math.round((new Date(envelope.metadata.completedAt).getTime() - new Date(envelope.metadata.startedAt).getTime()) / 1000)
      : undefined)
    ?? (derivedElapsedSeconds > 0 ? derivedElapsedSeconds : undefined)

  const lastFrameIsError = lastFrame?.type === 'error'

  // The authoritative turn state (from turn.failed / turn.cancelled / turn.completed
  // events) must drive the outcome. Using only the last frame produced mismatches
  // where the badge showed Success with a red error background.
  // Waiting: the LLM loop finished but the workflow owner is parked until the
  // updater wakes it — show "waiting for workflow" instead of success.
  const outcome: TurnOutcome | null = isComplete
    ? (turnState === 'waiting'
        ? 'waiting'
        : turnState === 'cancelled' || turnState === 'abandoned'
        ? 'cancelled'
        : turnState === 'failed'
          ? 'error'
          : lastFrameIsError
            ? (errorFrames[errorFrames.length - 1]?.code === 'cancelled' ? 'cancelled' : 'error')
            : 'success')
    : null

  const activeTask = !isComplete ? envelope.tasks?.find(t => t.status === 'in_progress') : undefined
  const completedTasks = envelope.tasks?.filter(t => t.status === 'completed').length ?? 0
  const totalTasks = envelope.tasks?.length ?? 0

  const title = !isComplete
    ? (isUserPaused ? t('ai.step.paused')
      : activeTask ? taskLabel(activeTask) : (totalTasks > 0 && completedTasks === totalTasks
        ? t('ai.step.tasks', { completed: completedTasks, total: totalTasks })
        : inferRunningTitle(envelope, t)))
    : (outcome === 'error' && envelope.metadata?.error
        ? envelope.metadata.error
        : '')

  const icon = outcome === 'error' || outcome === 'cancelled'
    ? <AlertTriangle size={12} className="ai-step-icon ai-step-icon-error" />
    : isUserPaused
      ? <Pause size={12} className="ai-step-icon ai-step-icon-paused" />
      : <Activity size={12} className="ai-step-icon" />

  return {
    actionCount,
    failActionCount: errorFrames.length,
    elapsedSeconds,
    model: envelope.metadata?.model,
    icon,
    title,
    outcome,
    isUserPaused,
  }
}

function outcomeIcon(outcome: TurnOutcome): React.ReactNode {
  switch (outcome) {
    case 'success':
      return <Check size={12} className="ai-turn-detail-status-icon ai-turn-detail-status-icon-success" />
    case 'waiting':
      return <Waypoints size={12} className="ai-turn-detail-status-icon ai-turn-detail-status-icon-waiting" />
    case 'error':
      return <AlertTriangle size={12} className="ai-turn-detail-status-icon ai-turn-detail-status-icon-error" />
    case 'cancelled':
      return <Ban size={12} className="ai-turn-detail-status-icon ai-turn-detail-status-icon-cancelled" />
  }
}

function outcomeLabel(outcome: TurnOutcome, t: TFunction): string {
  switch (outcome) {
    case 'success': return t('ai.step.outcome.success')
    case 'waiting': return t('ai.step.outcome.waiting')
    case 'error': return t('ai.step.outcome.error')
    case 'cancelled': return t('ai.step.outcome.cancelled')
  }
}

const UsageBadge: React.FC<{
  inputTokens?: number
  outputTokens?: number
  contextBudget?: ContextBudget
  sync?: boolean
  costTotal?: number
  cacheHitRate?: number
  provider?: string
  model?: string
  showBudgetBar?: boolean
  showTokens?: boolean
}> = ({ inputTokens, outputTokens, contextBudget, sync, costTotal, cacheHitRate, provider, model, showBudgetBar = false, showTokens = true }) => {
  const inDisplay = useAnimatedNumber(inputTokens ?? 0, { sync })
  const outDisplay = useAnimatedNumber(outputTokens ?? 0, { sync })

  const hasBudget = contextBudget != null
  const usageRatio = hasBudget
    ? Math.min(1, contextBudget.estimatedTokens / contextBudget.contextWindowSize)
    : 0
  const budgetRatio = hasBudget && contextBudget.tokenBudget > 0
    ? Math.min(1, contextBudget.tokenBudget / contextBudget.contextWindowSize)
    : 0
  const isOverBudget = hasBudget && contextBudget.tokenBudget > 0
    && contextBudget.estimatedTokens >= contextBudget.tokenBudget

  const fmtCost = (n: number): string => {
    if (n === 0) return '0'
    if (n < 0.001) return n.toFixed(4)
    return n.toFixed(2)
  }

  const title = [
    provider && model && `${provider} / ${model}`,
    inputTokens != null && `${formatTokenCount(inputTokens)} in`,
    outputTokens != null && `${formatTokenCount(outputTokens)} out`,
    costTotal != null && `$${fmtCost(costTotal)}`,
    cacheHitRate != null && `${Math.round(cacheHitRate * 100)}% cache hit`,
    hasBudget && `${formatTokenCount(contextBudget.estimatedTokens)}/${formatTokenCount(contextBudget.contextWindowSize)} window`,
    hasBudget && contextBudget.tokenBudget > 0 && `compaction at ${formatTokenCount(contextBudget.tokenBudget)}`,
  ].filter(Boolean).join(' · ')

  const fillWidth = `${usageRatio * 100}%`

  return (
    <span className={`ai-turn-usage-badge${isOverBudget ? ' over-budget' : ''}`} title={title}>
      {hasBudget && showBudgetBar && budgetRatio > 0 && (
        <span className="ai-turn-usage-budget-mark" style={{ left: `${budgetRatio * 100}%` }} />
      )}
      {hasBudget && showBudgetBar && (
        <span className="ai-turn-usage-track">
          <span className="ai-turn-usage-fill" style={{ width: fillWidth }} />
        </span>
      )}
      {showTokens && (
        <span className="ai-turn-usage-text-row">
          {inputTokens != null && <span className="ai-turn-usage-in">↑{formatTokenCount(inDisplay)}</span>}
          {outputTokens != null && <span className="ai-turn-usage-out" style={{ marginLeft: '2px' }}>↓{formatTokenCount(outDisplay)}</span>}
          {costTotal != null && costTotal > 0 && <span className="ai-turn-usage-cost">{fmtCost(costTotal)}$</span>}
          {cacheHitRate != null && <span className="ai-turn-usage-cache" style={{ marginLeft: '3px' }}><Database size={10} style={{ display: 'inline-block', verticalAlign: '-1px', marginRight: '1px' }} />{Math.round(cacheHitRate * 100)}%</span>}
        </span>
      )}
      {hasBudget && showBudgetBar && showTokens && (
        <span className="ai-turn-usage-text-clip" style={{ width: fillWidth }}>
          <span className="ai-turn-usage-text-row invert">
            {inputTokens != null && <span className="ai-turn-usage-in">↑{formatTokenCount(inDisplay)}</span>}
            {outputTokens != null && <span className="ai-turn-usage-out" style={{ marginLeft: '2px' }}>↓{formatTokenCount(outDisplay)}</span>}
            {costTotal != null && costTotal > 0 && <span className="ai-turn-usage-cost">{fmtCost(costTotal)}$</span>}
            {cacheHitRate != null && <span className="ai-turn-usage-cache" style={{ marginLeft: '3px' }}><Database size={10} style={{ display: 'inline-block', verticalAlign: '-1px', marginRight: '1px' }} />{Math.round(cacheHitRate * 100)}%</span>}
          </span>
        </span>
      )}
    </span>
  )
}

// Sparkline viewport dimensions.
const THROUGHPUT_CHART_W = 72
const THROUGHPUT_CHART_H = 16

/** Desktop-only LLM throughput timeline: a 30s rolling sparkline of output
 *  token/s while streaming; once the turn ends the label switches to the turn's
 *  real average = outputTokens / elapsedSeconds (output tokens ÷ task total
 *  time), with the timeline frozen on its last window. */
const TokenThroughputChart: React.FC<{
  envelope: TurnEnvelope
  isStreaming: boolean
  paused: boolean
}> = ({ envelope, isStreaming, paused }) => {
  const { history, rate } = useTokenThroughput(envelope, isStreaming, paused)
  const outTokens = envelope.metadata?.usage?.outputTokens
  const elapsed = envelope.metadata?.elapsedSeconds
  // Exclude tool-call time from the denominator: the LLM is not generating
  // while a tool runs, so wall-clock elapsed over-counts. netElapsed is the
  // time the model actually spent producing tokens.
  const toolTimeSec = envelope.frames
    .filter((f): f is ToolFrame => f.type === 'tool')
    .reduce((s, f) => s + (f.durationSeconds ?? 0), 0)
  const netElapsed = elapsed != null ? Math.max(0, elapsed - toolTimeSec) : undefined
  const hasSummary = !isStreaming && outTokens != null && netElapsed != null && netElapsed > 0
  const target = hasSummary ? (outTokens as number) / (netElapsed as number) : rate
  const displayRate = useAnimatedNumber(target, { tauSec: hasSummary ? 0.5 : 0.12 })

  // Sparkline collapsed by default; click the badge to reveal the timeline.
  const [expanded, setExpanded] = useState(false)

  const maxRaw = history.length > 0 ? Math.max(...history) : 0
  const max = Math.max(10, maxRaw) * 1.2
  const n = history.length
  const pts: string[] = n === 0 ? [] : history.map((v, i) => {
    const x = n > 1 ? (i / (n - 1)) * THROUGHPUT_CHART_W : THROUGHPUT_CHART_W
    const y = THROUGHPUT_CHART_H - Math.min(1, v / max) * THROUGHPUT_CHART_H
    return `${x.toFixed(2)},${y.toFixed(2)}`
  })
  const areaD = pts.length > 0
    ? `M0,${THROUGHPUT_CHART_H} ${pts.map(p => `L${p}`).join(' ')} L${THROUGHPUT_CHART_W},${THROUGHPUT_CHART_H} Z`
    : ''

  const summaryRate = hasSummary ? Math.round((outTokens as number) / (netElapsed as number)) : 0
  const titleText = hasSummary
    ? `${outTokens} output tokens / ${netElapsed}s (excl. ${toolTimeSec}s tools) = ${summaryRate} tok/s`
    : `~${Math.round(rate)} tokens/sec (estimated)`

  return (
    <button
      type="button"
      className={`ai-token-rate-bar${isStreaming ? ' active' : ''}${expanded ? ' expanded' : ''}`}
      title={titleText}
      aria-label={titleText}
      aria-expanded={expanded}
      onClick={() => setExpanded(v => !v)}
    >
      {expanded && pts.length > 0 && (
        <svg
          className="ai-token-rate-spark"
          width={THROUGHPUT_CHART_W}
          height={THROUGHPUT_CHART_H}
          viewBox={`0 0 ${THROUGHPUT_CHART_W} ${THROUGHPUT_CHART_H}`}
          preserveAspectRatio="none"
          aria-hidden="true"
        >
          {areaD && <path d={areaD} className="ai-token-rate-spark-area" />}
          <polyline points={pts.join(' ')} className="ai-token-rate-spark-line" fill="none" />
        </svg>
      )}
      <span className="ai-token-rate-label">
        {Math.round(displayRate)}<span className="ai-token-rate-unit"> tok/s</span>
      </span>
    </button>
  )
}

function firstChangedLine(diffContent?: string): number | undefined {
  const match = diffContent?.match(/^@@ .* \+(\d+)/m)
  return match ? Number(match[1]) : undefined
}

function iconForFileChange(fc: FileChangeEntry): 'added' | 'removed' | 'modified' {
  if ((fc.deletions ?? 0) > 0 && (fc.additions ?? 0) === 0) return 'removed'
  if ((fc.additions ?? 0) > 0 && (fc.deletions ?? 0) === 0 && fc.diffContent?.trim().startsWith('@@')) {
    // Heuristic: additions-only with a diff header is likely a new file shown as all additions.
    return 'added'
  }
  return 'modified'
}

interface PendingSubmitListProps {
  items: PendingSubmitEntry[]
  onCancel?: (clientId: string) => void
}

/** Queued user messages, anchored INSIDE .ai-turn-tail like TaskList: the
 *  tail is counter-shifted by the scroll-lock pin, so the list rides with
 *  the tail and is immune to the smooth-follow glide transform. Fresh
 *  component (own ai-pending-anchor-* classes); visual language mirrors
 *  the timeline steps but shares no CSS coupling with them. */
const PendingSubmitList: React.FC<PendingSubmitListProps> = ({ items, onCancel }) => {
  const { t } = useI18n()
  return (
    <div className="ai-pending-anchor">
      {items.map(ps => (
        <div key={ps.id} className="ai-pending-anchor-item">
          <span className="ai-pending-anchor-dot" />
          <span className="ai-pending-anchor-text">{ps.text}</span>
          {onCancel && (
            <button
              type="button"
              className="ai-pending-anchor-cancel"
              title={t('ai.turn.cancelPending')}
              onClick={() => onCancel(ps.id)}
            >
              <span className="ai-pending-anchor-cancel-icon" aria-hidden="true">×</span>
            </button>
          )}
        </div>
      ))}
    </div>
  )
}

interface FileChangeMenuTarget {
  name: string
  path: string
  x: number
  y: number
}

// Right-click context menu for a changed file row inside a turn tail. Portaled
// to document.body because the message stream applies a smooth-follow glide
// transform (position: fixed would otherwise anchor to that transformed
// ancestor). Registered with useBrowserOverlay so native browser windows hide
// while it is open, per the overlay discipline.
const FileChangeMenu: React.FC<{
  target: FileChangeMenuTarget
  projectId: string | null
  worktreeId?: string
  onClose: () => void
  onOpen: () => void
}> = ({ target, projectId, worktreeId, onClose, onOpen }) => {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)
  useBrowserOverlay(true)
  useMenuDismiss(menuRef, onClose, target)

  useLayoutEffect(() => {
    if (!menuRef.current) return
    const menu = menuRef.current
    const rect = menu.getBoundingClientRect()
    const vw = document.documentElement.clientWidth
    const vh = document.documentElement.clientHeight
    const pad = 8
    let left = target.x
    let top = target.y
    if (left + rect.width > vw - pad) left = target.x - rect.width
    if (left < pad) left = pad
    if (top + rect.height > vh - pad) top = target.y - rect.height
    if (top < pad) top = pad
    menu.style.left = `${left}px`
    menu.style.top = `${top}px`
  }, [target.x, target.y])

  const run = (fn: () => void) => () => { fn(); onClose() }

  const canOpenExternally = !!projectId
  const handleCopyPath = () => {
    try { void navigator.clipboard?.writeText(target.path) } catch { /* clipboard unavailable */ }
  }

  return createPortal(
    <div ref={menuRef} className="ai-turn-file-menu" role="menu" tabIndex={-1}>
      <div className="ai-turn-file-menu-header">
        <FileCode size={12} className="ai-turn-file-menu-header-icon" />
        <span className="ai-turn-file-menu-title" title={target.path}>{target.name}</span>
      </div>
      <div className="ai-turn-file-menu-list">
        <button type="button" className="ai-turn-file-menu-item" onClick={run(onOpen)}>
          <span className="ai-turn-file-menu-icon"><ExternalLink size={13} /></span>
          {t('fileBrowserContextMenu.open')}
        </button>
        <button type="button" className="ai-turn-file-menu-item" disabled={!canOpenExternally} onClick={run(() => canOpenExternally && void openInSystem(projectId!, target.path, worktreeId))}>
          <span className="ai-turn-file-menu-icon"><SquareArrowOutUpRight size={13} /></span>
          {t('fileBrowserContextMenu.openInSystem')}
        </button>
        <button type="button" className="ai-turn-file-menu-item" disabled={!canOpenExternally} onClick={run(() => canOpenExternally && void revealInSystem(projectId!, target.path, worktreeId))}>
          <span className="ai-turn-file-menu-icon"><FolderOpen size={13} /></span>
          {t('fileBrowserContextMenu.revealInSystem')}
        </button>
        <button type="button" className="ai-turn-file-menu-item" onClick={run(handleCopyPath)}>
          <span className="ai-turn-file-menu-icon"><Copy size={13} /></span>
          {t('fileBrowserContextMenu.copyPath')}
        </button>
      </div>
    </div>,
    document.body,
  )
}

interface FileChangeListProps {
  changes: FileChangeEntry[]
  onOpenFile?: (filePath: string, diffContent?: string, line?: number, lineEnd?: number, mode?: 'source' | 'diff') => void
  worktreeId?: string
}

const MAX_FILE_CHANGES_VISIBLE = 6

const FileChangeList: React.FC<FileChangeListProps> = ({ changes, onOpenFile, worktreeId }) => {
  const { t } = useI18n()
  const { projectId } = useFileReference() ?? {}
  const [expanded, setExpanded] = useState(false)
  const [menuTarget, setMenuTarget] = useState<FileChangeMenuTarget | null>(null)

  const hiddenCount = changes.length - MAX_FILE_CHANGES_VISIBLE
  const showToggle = hiddenCount > 0
  const visible = showToggle && !expanded ? changes.slice(-MAX_FILE_CHANGES_VISIBLE) : changes

  return (
    <div className="ai-turn-file-changes">
      <div className="ai-turn-file-changes-list">
        {visible.map((fc, i) => {
          const path = fc.filepath ?? fc.filename
          const status = iconForFileChange(fc)
          return (
            <button
              key={`${path}-${i}`}
              type="button"
              className={`ai-turn-file-change-item ai-turn-file-change-${status}`}
              onClick={() => onOpenFile?.(path, fc.diffContent, firstChangedLine(fc.diffContent), undefined, 'diff')}
              onContextMenu={(e) => {
                e.preventDefault()
                e.stopPropagation()
                setMenuTarget({ name: fc.filename, path, x: e.clientX, y: e.clientY })
              }}
              title={path}
            >
              <FileCode size={12} className="ai-turn-file-change-icon" />
              <span className="ai-turn-file-change-name">{fc.filename}</span>
              <span className="ai-turn-file-stats">
                {(fc.additions ?? 0) > 0 && <span className="ai-turn-file-stat add">+{fc.additions}</span>}
                {(fc.deletions ?? 0) > 0 && <span className="ai-turn-file-stat del">-{fc.deletions}</span>}
              </span>
            </button>
          )
        })}
      </div>
      {showToggle && (
        <button
          type="button"
          className="ai-turn-truncate-toggle"
          onClick={() => setExpanded(v => !v)}
        >
          {expanded ? t('ai.turn.showLess') : t('ai.turn.showMore', { count: hiddenCount })}
        </button>
      )}
      {menuTarget && (
        <FileChangeMenu
          target={menuTarget}
          projectId={projectId ?? null}
          worktreeId={worktreeId}
          onClose={() => setMenuTarget(null)}
          onOpen={() => {
            const fc = visible.find(f => (f.filepath ?? f.filename) === menuTarget.path)
            onOpenFile?.(menuTarget.path, fc?.diffContent, firstChangedLine(fc?.diffContent), undefined, 'diff')
          }}
        />
      )}
    </div>
  )
}

interface GoalRowProps {
  goal: SessionGoal
}

// WorktreeRow shows the isolated worktree this agent is bound to. Clicking
// jumps to git mode on that worktree with the agent's HEAD commit selected —
// the same jump the worktree badges run (jumpToWorktreeGit).
const WorktreeRow: React.FC<{ agentActorId?: string }> = ({ agentActorId }) => {
  const agent = useAgentInfo(agentActorId)
  const runtime = agent?.Runtime
  if (!runtime?.WorktreeStatus || runtime.WorktreeStatus === 'none') return null

  const handleClick = () => {
    if (agent?.ProjectId) void jumpToWorktreeGit(agent.ProjectId, runtime.WorktreeID)
  }

  return (
    <div className="ai-turn-goal-row ai-turn-worktree-row">
      <button
        type="button"
        className="ai-turn-goal-header"
        title={runtime.WorktreeName || runtime.WorktreeID || ''}
        onClick={handleClick}
      >
        <GitBranch size={14} className="ai-turn-goal-icon" />
        <span className="ai-turn-goal-label">Worktree</span>
        <span className="ai-turn-goal-text" title={runtime.WorktreeID || runtime.WorktreeName || ''}>
          {runtime.WorktreeName || 'unnamed'}
        </span>
        <span className="ai-turn-goal-progress">{runtime.WorktreeStatus}</span>
      </button>
    </div>
  )
}

const GoalRow: React.FC<GoalRowProps> = ({ goal }) => {
  const { t } = useI18n()
  const { onOpenCard, onOpenText } = useAIShellContext()
  const displayText = goal.InterpretedGoal?.trim() || goal.Condition
  const cardId = goal.BoundTaskCardId?.trim()
  const nearLimit = goal.MaxTurns > 0 && goal.TurnCount >= goal.MaxTurns * 0.9
  const progress = nearLimit ? `${goal.TurnCount}/${goal.MaxTurns}` : null
  const handleClick = () => {
    if (cardId) onOpenCard?.(cardId)
    else onOpenText?.(t('ai.turn.goal'), displayText)
  }
  return (
    <div className="ai-turn-goal-row">
      <button
        type="button"
        className="ai-turn-goal-header"
        onClick={handleClick}
      >
        <Target size={14} className="ai-turn-goal-icon" />
        <span className="ai-turn-goal-label">{t('ai.turn.goal')}</span>
        <span className="ai-turn-goal-text" title={displayText}>{displayText}</span>
        {progress !== null && <span className="ai-turn-goal-progress">{progress}</span>}
      </button>
    </div>
  )
}

// WorkflowRow shows the workflow map this agent owns (map owner agents get
// ActiveWorkflowMapCardId stamped on their mode state). Clicking opens the
// workflow mode and zooms to fit the map — the same action the workflow
// composer badge runs (openWorkflowAndLocate).
//
// When the map's task cards are loaded in the mono store, the row appends a
// mini progress readout: done/total count, a "+N" delta since the row last
// saw the map, and a per-task status bar strip.

// Last-seen done-task count per workflow map. Module-level so the delta badge
// survives TurnTail remounts across turns; transient in-memory UI state only.
const workflowDoneBaseline = new Map<string, number>()

function workflowBarStatusKey(status: string | undefined): string {
  return status && STATUS_META[status] ? status : 'backlog'
}

// Bar strip sort order: most-complete statuses first so the mini chart reads
// as left-to-right progress fill. Array.sort is stable, so tasks sharing a
// status keep the map's include-list order.
const WORKFLOW_BAR_STATUS_RANK: Record<string, number> = {
  done: 0,
  pending_review: 1,
  doing: 2,
  todo: 3,
  backlog: 4,
}

function workflowBarRank(task: MonoCardListItem): number {
  return WORKFLOW_BAR_STATUS_RANK[workflowBarStatusKey(task.status)] ?? WORKFLOW_BAR_STATUS_RANK.backlog!
}

export function sortWorkflowProgressBars(tasks: MonoCardListItem[]): MonoCardListItem[] {
  return [...tasks].sort((a, b) => workflowBarRank(a) - workflowBarRank(b))
}

const WorkflowRow: React.FC<{ agentActorId?: string }> = ({ agentActorId }) => {
  const { t } = useI18n()
  const agent = useAgentInfo(agentActorId)
  const mapId = agent?.ActiveWorkflowMapCardId?.trim()
  const cards = useMonoStore(s => s.cards)
  const tasks = useMemo<MonoCardListItem[]>(() => {
    if (!mapId) return []
    const mapCard = cards.find(c => c.id === mapId)
    if (!mapCard) return []
    const byId = new Map(cards.map(c => [c.id, c]))
    return workflowTaskIds(mapCard, cards)
      .map(id => byId.get(id))
      .filter((c): c is MonoCardListItem => !!c)
  }, [cards, mapId])
  const total = tasks.length
  const done = useMemo(() => tasks.filter(c => c.status === 'done').length, [tasks])
  const [delta, setDelta] = useState(0)
  useEffect(() => {
    if (!mapId || total === 0) return
    const prev = workflowDoneBaseline.get(mapId)
    workflowDoneBaseline.set(mapId, done)
    setDelta(prev === undefined || done === prev ? 0 : done - prev)
  }, [mapId, done, total])
  if (!mapId) return null
  return (
    <div className="ai-turn-goal-row ai-turn-workflow-row">
      <button
        type="button"
        className="ai-turn-goal-header"
        onClick={() => openWorkflowAndLocate(mapId)}
      >
        <Waypoints size={14} className="ai-turn-goal-icon" />
        <span className="ai-turn-goal-label">{t('shell.sidebar.workflowMode')}</span>
        <span className="ai-turn-goal-text" title={mapId}>{mapId}</span>
        {total > 0 && (
          <span className="ai-turn-workflow-progress">
            <span className="ai-turn-workflow-bars" aria-hidden="true">
              {sortWorkflowProgressBars(tasks).map(task => {
                const key = workflowBarStatusKey(task.status)
                return (
                  <span
                    key={task.id}
                    className={`ai-turn-workflow-bar ai-turn-workflow-bar--${key}`}
                    style={{ background: STATUS_META[key]?.color }}
                  />
                )
              })}
            </span>
            <span className="ai-turn-workflow-progress-count">{done}/{total}</span>
            {delta !== 0 && (
              <span className={`ai-turn-workflow-progress-delta ai-turn-workflow-progress-delta--${delta > 0 ? 'up' : 'down'}`}>
                {delta > 0 ? <TrendingUp size={11} /> : <TrendingDown size={11} />}
                {delta > 0 ? `+${delta}` : delta}
              </span>
            )}
          </span>
        )}
      </button>
    </div>
  )
}

// ── TurnTail component ──

const MAX_TASKS_VISIBLE = 5

function selectVisibleTasks(tasks: TaskEntry[]): { visible: TaskEntry[]; hiddenCount: number } {
  const inProgress = tasks.filter(task => task.status === 'in_progress')
  const others = tasks.filter(task => task.status !== 'in_progress')
  const remaining = Math.max(0, MAX_TASKS_VISIBLE - inProgress.length)
  const visibleOthers = others.slice(-remaining)
  const visibleIds = new Set([...inProgress, ...visibleOthers].map(task => task.id))
  const visible = tasks.filter(task => visibleIds.has(task.id))
  return { visible, hiddenCount: tasks.length - visible.length }
}

const TaskList: React.FC<{ tasks: TaskEntry[] }> = ({ tasks }) => {
  const { t } = useI18n()
  const [expanded, setExpanded] = useState(false)

  const { visible: truncated, hiddenCount } = selectVisibleTasks(tasks)
  const showToggle = hiddenCount > 0
  const visible = showToggle && !expanded ? truncated : tasks

  return (
    <div className="ai-turn-tasks-section">
      {visible.map(task => (
        <div key={task.id} className={`ai-turn-task-row ai-turn-task-${task.status}`}>
          <span className="ai-turn-task-icon">
            {task.status === 'completed'
              ? <SquareCheck size={14} />
              : task.status === 'in_progress'
                ? <span className="ai-turn-task-spinner" aria-hidden="true" />
                : <Square size={14} />}
          </span>
          <span className="ai-turn-task-subject">{taskLabel(task)}</span>
        </div>
      ))}
      {showToggle && (
        <button
          type="button"
          className="ai-turn-truncate-toggle"
          onClick={() => setExpanded(v => !v)}
        >
          {expanded ? t('ai.turn.showLess') : t('ai.turn.showMore', { count: hiddenCount })}
        </button>
      )}
    </div>
  )
}

interface TurnTailProps {
  envelope: TurnEnvelope
  isComplete: boolean
  agentActorId?: string
  onCancelPendingSubmit?: (clientId: string) => void
  /** When false (a previous completed turn), the tail starts collapsed. */
  isLast?: boolean
}

export const TurnTail = React.memo(function TurnTail({ envelope, isComplete, agentActorId, onCancelPendingSubmit, isLast = true }: TurnTailProps) {
  const { onOpenFile } = useAIShellContext()
  const { t } = useI18n()
  const stableEnvelopeId = envelope.id
  // turnState is authoritative now (driven by turn.started / turn.completed /
  // turn.failed / turn.cancelled events). Falls back to envelope.completed for
  // legacy sessions that haven't received a turn event yet.
  const turnState = envelope.metadata?.turnState
    ?? (envelope.completed === true ? 'completed' : 'running')
  const summary = buildAssistantTurnSummary(envelope, isComplete, turnState, t)
  const isUserPaused = summary.isUserPaused
  const isStreaming = (turnState === 'running' || turnState === 'paused') && !isUserPaused
  const isAwaitingUser = envelope.frames.some(f => f.type === 'ask_user' && f.status === 'running')
  const spinnerSpeed = useTurnTokenSpeed(envelope, isStreaming, isAwaitingUser)
  const metricsVisible = useTurnTailMetricsVisible()
  // The token-rate badge is a live-streaming affordance: it only makes sense on
  // the current (last) turn. Historical tails keep a static summary badge
  // around forever, which is noise — hide it there.
  const showTokenMonitor = isLast && isWails() && metricsVisible.throughput
  const [openActions, setOpenActions] = useState(false)
  const actionsAnchorRef = useRef<HTMLDivElement>(null)
  const agentInfo = useAgentInfo(agentActorId)
  const hasWorkflow = !!agentInfo?.ActiveWorkflowMapCardId?.trim()
  const hasWorktree = !!agentInfo?.Runtime?.WorktreeID?.trim()
  const worktreeId = agentInfo?.WorktreeID || undefined

  useEffect(() => {
    setOpenActions(false)
  }, [stableEnvelopeId])

  // Close the actions dropdown when clicking outside.
  useEffect(() => {
    if (!openActions) return
    const handler = (e: MouseEvent) => {
      if (actionsAnchorRef.current && !actionsAnchorRef.current.contains(e.target as Node)) {
        setOpenActions(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [openActions])

  const usage = envelope.metadata?.usage
  const inputTokens = usage?.inputTokens
  const outputTokens = usage?.outputTokens

  // Wall-clock duration since turn start, computed from the turn's startedAt
  // timestamp (not a client-side counter). Ticks live while running; pins to
  // (completedAt - startedAt) on terminal states and freezes while paused.
  const liveDuration = useTurnLiveDuration({
    startedAt: envelope.metadata?.startedAt,
    completedAt: envelope.metadata?.completedAt,
    turnState: turnState === 'running' || turnState === 'paused' ? 'running' : turnState,
    paused: isUserPaused,
  })

  const requestStats = envelope.metadata?.requestStats
  const requestAgg = useMemo(() => {
    if (!requestStats || requestStats.length === 0) return null
    let cost = 0
    let cacheRead = 0
    let cacheWrite = 0
    let inputNoCache = 0
    let provider = ''
    let model = ''
    for (const s of requestStats) {
      const u = s.Usage
      if (!u) continue
      cost += u.CostTotal ?? 0
      cacheRead += u.CacheReadInputTokens ?? 0
      cacheWrite += u.CacheCreationInputTokens ?? 0
      inputNoCache += u.InputTokens ?? 0
      if (!provider) provider = s.Provider
      if (!model) model = s.ResponseModel || s.Model
    }
    const denom = inputNoCache + cacheRead + cacheWrite
    return {
      costTotal: cost,
      cacheHitRate: denom > 0 ? cacheRead / denom : 0,
      provider,
      model,
    }
  }, [requestStats])

  const headerExtras = (
    <div className={`ai-turn-header-metrics${isStreaming ? ' streaming' : ''}${isAwaitingUser ? ' ai-wave-paused' : ''}`}>
      {metricsVisible.duration && liveDuration > 0 && (
        <span className="ai-turn-duration-badge" title={envelope.metadata?.startedAt ? formatTimestampFull(envelope.metadata.startedAt) : undefined}>
          {formatDurationBadge(liveDuration)}
        </span>
      )}
      {metricsVisible.tokenBadge && (inputTokens != null || outputTokens != null || envelope.metadata?.contextBudget != null || requestAgg != null) && (
        <UsageBadge
          inputTokens={inputTokens}
          outputTokens={outputTokens}
          contextBudget={envelope.metadata?.contextBudget}
          sync={isComplete}
          costTotal={requestAgg?.costTotal}
          cacheHitRate={requestAgg?.cacheHitRate}
          provider={requestAgg?.provider}
          model={requestAgg?.model}
          showBudgetBar={metricsVisible.budgetBar}
        />
      )}
      {metricsVisible.budgetBar && !metricsVisible.tokenBadge && (inputTokens != null || outputTokens != null || envelope.metadata?.contextBudget != null || requestAgg != null) && (
        <UsageBadge
          inputTokens={inputTokens}
          outputTokens={outputTokens}
          contextBudget={envelope.metadata?.contextBudget}
          sync={isComplete}
          costTotal={requestAgg?.costTotal}
          cacheHitRate={requestAgg?.cacheHitRate}
          provider={requestAgg?.provider}
          model={requestAgg?.model}
          showBudgetBar
          showTokens={false}
        />
      )}
      {showTokenMonitor && (
        <TokenThroughputChart envelope={envelope} isStreaming={isStreaming} paused={isAwaitingUser} />
      )}
    </div>
  )

  const effectivelyComplete = isComplete

  const stepStatus: 'running' | 'completed' | 'error' =
    turnState === 'failed' || turnState === 'cancelled' || turnState === 'abandoned' ? 'error'
    : turnState === 'completed' || turnState === 'waiting' || isUserPaused ? 'completed'
    : 'running'

  return (
    <div className="ai-turn-tail-wrap" data-envelope-id={envelope.id}>
      <div className="ai-turn-tail">
        {!effectivelyComplete && (envelope.metadata?.pendingSubmits?.length ?? 0) > 0 && (
          <PendingSubmitList
            items={envelope.metadata!.pendingSubmits!}
            onCancel={onCancelPendingSubmit}
          />
        )}
        <TimelineStep
          slotId={`${envelope.id}-summary`}
          status={stepStatus}
          icon={summary.icon}
          spinnerVariant="petal"
          spinnerSpeed={spinnerSpeed}
          label={
            <span
              className={`ai-step-label ai-turn-summary-title${isAwaitingUser ? ' ai-wave-paused' : ''}`}
              title={summary.title}
            >
              {summary.outcome && (
                <span className={`ai-turn-summary-status ai-turn-summary-status-${summary.outcome}`}>
                  <span className="ai-turn-summary-status-text">
                    {outcomeIcon(summary.outcome)}
                    {outcomeLabel(summary.outcome, t)}
                  </span>
                </span>
              )}
              {isStreaming
                ? <span className="ai-step-label-wave">{splitTextToWaveChars(truncateLabel(summary.title))}</span>
                : truncateLabel(summary.title)
              }
            </span>
          }
          headerExtras={headerExtras}
          expandable={effectivelyComplete || ((envelope.tasks?.length ?? 0) > 0) || ((envelope.fileChanges?.length ?? 0) > 0) || !!envelope.goal || hasWorkflow || hasWorktree}
          expansionMode={effectivelyComplete ? (isLast ? 'open' : 'manual') : (((envelope.tasks?.length ?? 0) > 0) || ((envelope.fileChanges?.length ?? 0) > 0) || !!envelope.goal || hasWorkflow || hasWorktree) ? 'auto-while-active' : 'manual'}
        >
          <div className="ai-turn-details-section">
            {turnState === 'failed' && envelope.metadata?.error && (
              <div className="ai-turn-error-section">
                <div className="ai-error-message">{envelope.metadata.error}</div>
              </div>
            )}
            {envelope.fileChanges && envelope.fileChanges.length > 0 && (
              <FileChangeList changes={envelope.fileChanges} onOpenFile={onOpenFile} worktreeId={worktreeId} />
            )}
            {envelope.goal && (
              <div className="ai-turn-goal-section">
                <GoalRow goal={envelope.goal} />
              </div>
            )}
            <WorkflowRow agentActorId={agentActorId} />
            <WorktreeRow agentActorId={agentActorId} />
            {(envelope.tasks?.length ?? 0) > 0 && (
              <TaskList tasks={envelope.tasks!} />
            )}

          </div>
        </TimelineStep>
      </div>
      {isComplete && (
        <div className="ai-turn-footer ai-message-footer">
          {envelope.timestamp && (
            <span className="ai-message-footer-time" title={formatTimestampFull(envelope.timestamp)}>{formatTimestamp(envelope.timestamp)}</span>
          )}
          <div className="ai-turn-actions-anchor" ref={actionsAnchorRef}>
            <button
              className="ai-message-footer-icon-btn"
              title="More actions"
              aria-label="More actions"
              aria-expanded={openActions}
              onClick={() => setOpenActions(v => !v)}
            >
              <MoreHorizontal size={12} />
            </button>
            {openActions && (
              <div className="ai-turn-actions-menu">
                <button
                  className="ai-turn-actions-item"
                  onClick={() => {
                    if (agentActorId && envelope.id) {
                      window.dispatchEvent(new CustomEvent('sporemind:fork-from-turn', {
                        detail: { agentActorId, turnId: envelope.id },
                      }))
                    }
                    setOpenActions(false)
                  }}
                >
                  {t('ai.turn.forkFromHere')}
                </button>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
})
