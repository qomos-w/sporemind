import React from 'react'
import { buildToolDisplay, normalizePath, getToolKind, parseJsonObject, firstString, NO_MATCHES_TEXT } from './tool-display.ts'
import type { Frame, ToolFrame, FileChangeEntry, SlotStatus, TimelineConnectorMode } from '../../model/frame-types.ts'
import { formatTokenCount, formatDurationBadge } from '../timeline'
import { ToolFoldCard } from './ToolFoldCard.tsx'
import { BashToolView } from './BashToolView.tsx'
import { EditToolView } from './EditToolView.tsx'
import { GenericToolView } from './GenericToolView.tsx'
import { ListToolView } from './ListToolView.tsx'
import { ReadToolView } from './ReadToolView.tsx'
import { GlobToolView } from './GlobToolView.tsx'
import { SearchToolView, parseGrepResult } from './SearchToolView.tsx'
import { WebToolView } from './WebToolView.tsx'
import { PagePreviewToolView } from './PagePreviewToolView.tsx'
import { MediaGenToolView } from './MediaGenToolView.tsx'
import { ExploreToolView, workItemLine } from './ExploreToolView.tsx'
import { AgentReviewToolView, parseAgentReview, resolveEffectiveApproved } from './AgentReviewToolView.tsx'
import { AgentMessageToolView } from './AgentMessageToolView.tsx'
import { CardToolView } from './CardToolView.tsx'
import { TaskToolView } from './TaskToolView.tsx'
import { McpToolView } from './McpToolView.tsx'
import { SshSessionToolView, SshRunToolView } from './SshSessionToolView.tsx'
import type { FileEntryListResp, FileSystemGlobResp, FileSystemGrepResp } from '../../../../gen-types/filesystem'
import { useI18n } from '../../../../i18n/provider'
import { useMonoStore } from '../../hooks/useMonoStore'
import { useAgentInfoList } from '../../hooks/agentInfoStore'
import { avatarHue, agentAvatarLabel, agentDisplayName } from '../../lib/agent-avatar'
import type { I18nKey } from '../../../../i18n/types'

type TFunction = (key: I18nKey, params?: Record<string, string | number>) => string

function toolIcon(status: SlotStatus, Icon: React.ComponentType<{ size?: number; className?: string }>): React.ReactNode {
  const colorClass = status === 'completed'
    ? 'ai-step-icon-accent'
    : status === 'error'
      ? 'ai-step-icon-error'
      : ''
  return <Icon size={12} className={`ai-step-icon ${colorClass}`} />
}

function countNonEmptyLines(text: string): number {
  return text.split(/\r?\n/).filter(line => line.trim().length > 0).length
}

function extractTrailingFileCount(text: string): { count: number; truncated: boolean } | null {
  const lines = text.split(/\r?\n/).filter(line => line.trim().length > 0)
  const lastLine = lines.at(-1)
  if (!lastLine) return null
  const match = lastLine.match(/\((\d+)(\+?)\s+files?\)$/)
  if (!match) return null
  return { count: parseInt(match[1]!, 10), truncated: match[2] === '+' }
}

function getToolCountLabel(kind: string, frame: ToolFrame, t: TFunction): string | null {
  if (kind !== 'search' && kind !== 'list' && kind !== 'glob') return null
  if (!frame.output || (frame.status !== 'completed' && frame.status !== 'error')) return null

  if (kind === 'search') {
    const parsedInput = parseJsonObject(frame.input)
    const parsedOutput = parseJsonObject(frame.output) as FileSystemGrepResp | null
    // Prefer the authoritative output mode returned by the backend.
    const outputMode = parsedOutput?.Output_mode || (parsedInput?.Output_mode as string) || 'content'
    const result = parseGrepResult(outputMode, frame.output)
    if (result) {
      if (result.mode === 'content') return t('ai.tool.fragments', { count: result.matches.length })
      if (result.mode === 'files') return t('ai.tool.files', { count: result.files.length })
      return t('ai.tool.files', { count: result.rows.length })
    }
    if (frame.output?.trim() === NO_MATCHES_TEXT) {
      if (outputMode === 'content') return t('ai.tool.fragments', { count: 0 })
      return t('ai.tool.files', { count: 0 })
    }
    // The LLM tool layer may return the result as plain text rather than JSON.
    // Fall back to line counting / trailing summary parsing.
    const trailing = extractTrailingFileCount(frame.output)
    if (trailing) {
      const suffix = trailing.truncated ? '+' : ''
      if (outputMode === 'content') return `${t('ai.tool.fragments', { count: trailing.count })}${suffix}`
      return `${t('ai.tool.files', { count: trailing.count })}${suffix}`
    }
    const count = countNonEmptyLines(frame.output)
    if (count === 0) return null
    if (outputMode === 'content') return t('ai.tool.fragments', { count })
    return t('ai.tool.files', { count })
  }

  if (kind === 'list') {
    const parsed = parseJsonObject(frame.output) as FileEntryListResp | null
    if (parsed && Array.isArray(parsed.Items)) {
      const entries = parsed.Items
      const files = entries.filter(e => !e.IsDir).length
      const dirs = entries.filter(e => e.IsDir).length
      if (dirs > 0) return `${t('ai.tool.files', { count: files })} · ${t('ai.tool.dirs', { count: dirs })}`
      return t('ai.tool.files', { count: files })
    }
    if (frame.output?.trim() === NO_MATCHES_TEXT) return t('ai.tool.files', { count: 0 })
    const count = countNonEmptyLines(frame.output)
    if (count === 0) return null
    return t('ai.tool.files', { count })
  }

  if (kind === 'glob') {
    const parsed = parseJsonObject(frame.output) as FileSystemGlobResp | null
    if (parsed && Array.isArray(parsed.Files)) {
      const count = parsed.Files.length
      if (parsed.Truncated) return `${t('ai.tool.files', { count })}+`
      return t(count === 1 ? 'ai.tool.file' : 'ai.tool.files', { count })
    }
    if (frame.output?.trim() === NO_MATCHES_TEXT) return t('ai.tool.files', { count: 0 })
    const trailing = extractTrailingFileCount(frame.output)
    if (trailing) {
      const suffix = trailing.truncated ? '+' : ''
      return `${t('ai.tool.files', { count: trailing.count })}${suffix}`
    }
    const count = countNonEmptyLines(frame.output)
    if (count === 0) return null
    return t(count === 1 ? 'ai.tool.file' : 'ai.tool.files', { count })
  }

  return null
}

function ToolStepSubtitle({ subtitle, countLabel }: { subtitle: string; countLabel: string | null }) {
  return (
    <span className="ai-tool-step-subtitle">
      <span className="ai-tool-step-subtitle-text" title={subtitle}>{subtitle}</span>
      {countLabel && <span className="ai-tool-step-subtitle-count"> · {countLabel}</span>}
    </span>
  )
}

// While a fork agent (explore/review/fork) is running, surface its latest
// active work item in the step title row instead of the static description.
function forkLiveSubtitle(kind: string, frame: ToolFrame): string | null {
  if (!isForkKind(kind)) return null
  if (frame.status !== 'running') return null
  const activeWork = frame.progress?.activeWork
  if (!activeWork || activeWork.length === 0) return null
  const latest = activeWork[activeWork.length - 1]!
  return workItemLine(latest.Type, latest.Target)
}

const isForkKind = (kind: string): boolean => kind === 'explore' || kind === 'review' || kind === 'fork'

// Agent avatar shown at the start of fork tool steps (fork_explore, fork_review,
// fork_general). Resolves the fork child agent from the parent's child list
// using the ToolUseId injected into the fork tool input. Renders the same
// circular hue background + initials as the sidebar agent avatar, without
// status indicators.
function ForkStepAvatar({ frame, agentActorId }: { frame: ToolFrame; agentActorId?: string }) {
  const snapshot = useAgentInfoList()
  const parent = agentActorId ? snapshot.byActorId.get(agentActorId) : undefined
  const parsed = parseJsonObject(frame.input)
  const toolUseId = parsed ? firstString(parsed, ['ToolUseId', 'toolUseId']) : undefined
  const childRef = parent?.Children?.find(c => (toolUseId && c.ToolUseId === toolUseId) || c.ToolUseId === frame.id)
  if (!childRef) return null
  const child = childRef.ActorId ? snapshot.byActorId.get(childRef.ActorId) : undefined
  const displayName = child?.DisplayName ?? childRef.DisplayName
  const agentKind = child?.AgentKind ?? childRef.AgentKind
  const id = child?.Id ?? childRef.Id
  const actorId = child?.ActorId ?? childRef.ActorId
  const initials = agentAvatarLabel(displayName, agentKind, (actorId || id).slice(0, 2).toUpperCase())
  const hue = avatarHue(id)
  const title = agentDisplayName(child?.Title, displayName ?? '', actorId || id)
  return (
    <span className="ai-fork-step-avatar" style={{ '--avatar-hue': hue } as React.CSSProperties} title={title}>
      {initials}
    </span>
  )
}

// Right-aligned extras for fork agent steps: search/read stats, token usage,
// and duration — same headerExtras slot other steps use (e.g. card +/- stats).
function ForkStepExtras({ frame }: { frame: ToolFrame }) {
  const progress = frame.progress
  const searchCount = progress?.searchCount ?? 0
  const readCount = progress?.readCount ?? 0
  const inputTokens = progress?.inputTokens ?? frame.inputTokens
  const outputTokens = progress?.outputTokens ?? frame.outputTokens

  const statsParts: string[] = []
  if (searchCount > 0) statsParts.push(`Searched ${searchCount} files`)
  if (readCount > 0) statsParts.push(`Read ${readCount} files`)

  const showTokens = (inputTokens ?? 0) > 0 || (outputTokens ?? 0) > 0
  const showDuration = frame.durationSeconds != null && frame.durationSeconds > 0
  if (statsParts.length === 0 && !showTokens && !showDuration) return null

  return (
    <>
      {statsParts.length > 0 && <span className="ai-tool-step-count">{statsParts.join(' · ')}</span>}
      {inputTokens != null && inputTokens > 0 && (
        <span className="ai-tool-step-count">{formatTokenCount(inputTokens)}↑</span>
      )}
      {outputTokens != null && outputTokens > 0 && (
        <span className="ai-tool-step-count">{formatTokenCount(outputTokens)}↓</span>
      )}
      {frame.durationSeconds != null && frame.durationSeconds > 0 && (
        <span className="ai-tool-step-count">{formatDurationBadge(frame.durationSeconds)}</span>
      )}
    </>
  )
}

const forkHeaderExtras = (kind: string, frame: ToolFrame): React.ReactNode | undefined =>
  isForkKind(kind) ? <ForkStepExtras frame={frame} /> : undefined

// For agent_review steps, show the resolved card title (truncated via CSS)
// instead of the raw id, plus an approve/reject status suffix. Subscribes to
// the mono store so only agent_review frames pay the lookup cost.
function AgentReviewStepSubtitle({ frame, t }: { frame: ToolFrame; t: TFunction }) {
  const cards = useMonoStore(s => s.cards)
  const data = parseAgentReview(frame.input, frame.output)
  const cardTitle = data.taskCardId
    ? (cards.find(c => c.id === data.taskCardId)?.data?.title as string | undefined) ?? data.taskCardId
    : undefined
  const subtitle = cardTitle ?? data.agentActorId ?? ''
  const approved = resolveEffectiveApproved(data, frame.status)
  let countLabel: string | null = null
  if (approved === true) countLabel = t('ai.tool.agentReview.approved')
  else if (approved === false) countLabel = t('ai.tool.agentReview.rejected')
  if (!subtitle && !countLabel) return null
  return <ToolStepSubtitle subtitle={subtitle} countLabel={countLabel} />
}

function ToolBody({ frame, agentActorId, t }: { frame: ToolFrame; agentActorId?: string; t: TFunction }) {
  const display = buildToolDisplay(frame.toolName, frame.input, t)

  switch (display.kind) {
    case 'bash':
      return <BashToolView frame={frame} />
    case 'edit':
      return <EditToolView frame={frame} />
    case 'list':
      return <ListToolView frame={frame} />
    case 'read':
      return <ReadToolView frame={frame} agentActorId={agentActorId} />
    case 'glob':
      return <GlobToolView frame={frame} />
    case 'search':
      return <SearchToolView frame={frame} />
    case 'web':
      return <WebToolView frame={frame} />
    case 'page_preview':
      return <PagePreviewToolView frame={frame} agentActorId={agentActorId} />
    case 'media_gen':
      return <MediaGenToolView frame={frame} />
    case 'explore':
    case 'review':
    case 'fork':
      return <ExploreToolView frame={frame} />
    case 'agent_review':
      return <AgentReviewToolView frame={frame} />
    case 'agent_message':
      return <AgentMessageToolView frame={frame} />
    case 'task':
      return <TaskToolView frame={frame} />
    case 'card':
      return <CardToolView frame={frame} />
    case 'mcp':
      return <McpToolView frame={frame} />
    case 'ssh_session':
      return <SshSessionToolView frame={frame} />
    case 'ssh_run':
      return <SshRunToolView frame={frame} />
    default:
      return <GenericToolView frame={frame} />
  }
}

function ToolCallInline({ frame, connectorMode, onFrameSelect, agentActorId, t, turnStreaming }: { frame: ToolFrame; connectorMode?: TimelineConnectorMode; onFrameSelect?: (frame: Frame) => void; agentActorId?: string; t: TFunction; turnStreaming?: boolean }) {
  const done = frame.status === 'completed' || frame.status === 'error'
  const display = buildToolDisplay(frame.toolName, frame.input, t)
  const countLabel = done ? getToolCountLabel(display.kind, frame, t) : null
  const subtitle = forkLiveSubtitle(display.kind, frame) ?? display.subtitle

  return (
    <ToolFoldCard
      frame={frame}
      uiKind={display.kind}
      icon={toolIcon(frame.status, display.icon)}
      connectorMode={connectorMode}
      headerExtras={forkHeaderExtras(display.kind, frame)}
      onMaximize={onFrameSelect ? () => onFrameSelect(frame) : undefined}
      turnStreaming={turnStreaming}
      label={
        <span className="ai-step-label ai-tool-step-label">
          {isForkKind(display.kind) && <ForkStepAvatar frame={frame} agentActorId={agentActorId} />}
          <span>{display.title}</span>
          {display.kind === 'agent_review' ? (
            <AgentReviewStepSubtitle frame={frame} t={t} />
          ) : (
            subtitle && <ToolStepSubtitle subtitle={subtitle} countLabel={countLabel} />
          )}
          {frame.targetService && (
            <span className="ai-tool-step-target" title={frame.callableId}>{normalizePath(frame.targetService)}</span>
          )}
          {done && frame.exitCode != null && (
            <span className={`ai-exit-code ${frame.exitCode === 0 ? 'success' : 'failed'}`}>
              exit {frame.exitCode}
            </span>
          )}
        </span>
      }
    >
      <ToolBody frame={frame} agentActorId={agentActorId} t={t} />
    </ToolFoldCard>
  )
}

function ToolCallCard({ frame, fileChanges, connectorMode, onFrameSelect, agentActorId, t, turnStreaming }: { frame: ToolFrame; fileChanges: FileChangeEntry[]; connectorMode?: TimelineConnectorMode; onFrameSelect?: (frame: Frame) => void; agentActorId?: string; t: TFunction; turnStreaming?: boolean }) {
  const totalAdd = fileChanges.reduce((s, f) => s + (f.additions ?? 0), 0)
  const totalDel = fileChanges.reduce((s, f) => s + (f.deletions ?? 0), 0)
  const display = buildToolDisplay(frame.toolName, frame.input, t)
  const countLabel = getToolCountLabel(display.kind, frame, t)
  const subtitle = forkLiveSubtitle(display.kind, frame) ?? display.subtitle

  return (
    <ToolFoldCard
      frame={frame}
      uiKind={display.kind}
      icon={toolIcon(frame.status, display.icon)}
      card
      connectorMode={connectorMode}
      onMaximize={onFrameSelect ? () => onFrameSelect(frame) : undefined}
      turnStreaming={turnStreaming}
      headerExtras={
        <>
          {totalAdd > 0 && <span className="ai-tool-card-stat add">+{totalAdd}</span>}
          {totalDel > 0 && <span className="ai-tool-card-stat del">-{totalDel}</span>}
          {forkHeaderExtras(display.kind, frame)}
        </>
      }
      label={
        <span className="ai-step-label ai-tool-step-label">
          {isForkKind(display.kind) && <ForkStepAvatar frame={frame} agentActorId={agentActorId} />}
          <span>{display.title}</span>
          {display.kind === 'agent_review' ? (
            <AgentReviewStepSubtitle frame={frame} t={t} />
          ) : (
            subtitle && <ToolStepSubtitle subtitle={subtitle} countLabel={countLabel} />
          )}
          {frame.targetService && (
            <span className="ai-tool-step-target" title={frame.callableId}>{normalizePath(frame.targetService)}</span>
          )}
        </span>
      }
    >
      <ToolBody frame={frame} agentActorId={agentActorId} t={t} />
      <div className="ai-file-changes">
        {fileChanges.map((fc, i) => (
          <div key={i} className="ai-file-change-row">
            <span className={`ai-file-dot ai-dot-${fc.icon ?? 'other'}`} />
            <span className="ai-file-name">{fc.filename}</span>
            {fc.filepath && <span className="ai-file-path">{fc.filepath}</span>}
            {fc.additions != null && <span className="ai-file-add">+{fc.additions}</span>}
            {fc.deletions != null && <span className="ai-file-del">-{fc.deletions}</span>}
          </div>
        ))}
      </div>
    </ToolFoldCard>
  )
}

interface ToolCallBlockProps {
  frame: ToolFrame
  connectorMode?: TimelineConnectorMode
  onFrameSelect?: (frame: Frame) => void
  agentActorId?: string
  turnStreaming?: boolean
}

export const ToolCallBlock: React.FC<ToolCallBlockProps> = ({ frame, connectorMode = 'none', onFrameSelect, agentActorId, turnStreaming }) => {
  const { t } = useI18n()
  const fileChanges = frame.fileChanges
  if (fileChanges && fileChanges.length > 0 && getToolKind(frame.toolName) !== 'edit') {
    return <ToolCallCard frame={frame} fileChanges={fileChanges} connectorMode={connectorMode} onFrameSelect={onFrameSelect} agentActorId={agentActorId} t={t} turnStreaming={turnStreaming} />
  }
  // Edit-kind tools (project.edit, project.write) have their own
  // expandable card UI (EditToolView) — render directly without TimelineStep wrapper.
  if (getToolKind(frame.toolName) === 'edit') {
    return (
      <div className="ai-step ai-edit-step" data-slot-id={frame.id} data-status={frame.status}>
        {connectorMode === 'to-next-icon' && <div className="ai-step-line" />}
        <EditToolView frame={frame} turnStreaming={turnStreaming} />
      </div>
    )
  }
  return <ToolCallInline frame={frame} connectorMode={connectorMode} onFrameSelect={onFrameSelect} agentActorId={agentActorId} t={t} turnStreaming={turnStreaming} />
}
