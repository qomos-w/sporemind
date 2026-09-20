import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { MoreHorizontal, Plus, HelpCircle, Lock, AlertCircle, FileText, Check, Trash2, Target, Waypoints, Brain, GitBranch } from 'lucide-react'
import type { AgentInfo } from '../hooks/agentInfoStore'
import { agentStatusClass, hasUnreadCompletion } from '../hooks/agentInfoStore'
import { avatarHue, agentDisplayName, sidebarAgentLabel } from '../lib/agent-avatar'
import { AgentAvatarContent } from './AgentAvatarContent'
import { buildAgentForest, flattenAgentForest, type AgentTreeNode } from '../lib/agent-tree'
import { useLongPress } from '../hooks/useLongPress'
import { useDropdownVerticalFit } from '../hooks/useDropdownVerticalFit'
import { useI18n } from '../../../i18n'
import { useNow, useRelativeTime } from '../../lib/format-time'
import { jumpToWorktreeGit } from '../hooks/worktreeGitJump'
import './AgentAvatarBar.css'

function agentAvatarContent(agent: { DisplayName: string; AgentKind: string; Status?: string }, size: number) {
  return <AgentAvatarContent agent={agent} size={size} mushroomStrokeWidth={64} />
}

interface AgentAvatarBarProps {
  agents: AgentInfo[]
  activeAgentId: string | null
  onAgentClick: (agent: AgentInfo) => void
  onAgentMenuOpen?: (agent: AgentInfo, x: number, y: number) => void
  onAdd?: () => void
  onCreateAgent?: () => void
  onDeleteAgent?: (agent: AgentInfo) => void
  showOverflow?: boolean
  overflowAgents?: AgentInfo[]
}

interface AgentGroup {
  projectId: string
  agents: AgentInfo[]
}

// Groups adjacent agents by project, then reorders the groups so the
// project-less (global) group comes first — exactly like the sidebar, which
// renders global agents (Coordinator, Oracle) in a dedicated top section before
// any project. The per-group agent order is preserved as-is; the caller runs
// buildAgentForest + flattenAgentForest per group to mirror the sidebar tree.
function groupAgentsByProject(agents: AgentInfo[]): AgentGroup[] {
  const groups: AgentGroup[] = []
  for (const agent of agents) {
    const last = groups[groups.length - 1]
    if (last && last.projectId === agent.ProjectId) {
      last.agents.push(agent)
    } else {
      groups.push({ projectId: agent.ProjectId, agents: [agent] })
    }
  }
  // Promote the global (project-less) group to the front so the avatar bar
  // matches the sidebar's global-agents-first section ordering.
  const globalIdx = groups.findIndex(g => !g.projectId)
  if (globalIdx > 0) {
    const [globalGroup] = groups.splice(globalIdx, 1)
    if (globalGroup) groups.unshift(globalGroup)
  }
  return groups
}

export const AgentAvatarBar: React.FC<AgentAvatarBarProps> = ({
  agents,
  activeAgentId,
  onAgentClick,
  onAgentMenuOpen,
  onAdd,
  onCreateAgent,
  onDeleteAgent,
  showOverflow = true,
  overflowAgents,
}) => {
  const dropdownRef = useRef<HTMLDivElement>(null)
  const [dropdownOpen, setDropdownOpen] = useState(false)
  useDropdownVerticalFit(dropdownOpen, dropdownRef, 'up')
  const { t } = useI18n()

  // Agents arrive pre-ordered by the parent (shared with the sidebar), so the
  // avatar bar renders that order directly; reordering happens in the sidebar.

  const dropdownAgents = useMemo(
    () => overflowAgents ?? agents,
    [overflowAgents, agents],
  )

  // Mirror the sidebar tree: group by project (global-first), then build +
  // flatten the parent-child forest so nested agents render with tree guides
  // and status icons exactly like the sidebar rows. We keep group boundaries so
  // the dropdown can render a divider between projects.
  const dropdownGroups = useMemo(
    () =>
      groupAgentsByProject(dropdownAgents).map(group => ({
        projectId: group.projectId,
        projectName: group.agents[0]?.ProjectName || group.projectId,
        nodes: flattenAgentForest(buildAgentForest(group.agents)),
      })),
    [dropdownAgents],
  )

  // Optimistic highlight: move the active indicator instantly on click, ahead
  // of the parent state update + timeline commit that normally gate it.
  const [optimisticId, setOptimisticId] = useState<string | null>(null)
  const displayActiveId = optimisticId ?? activeAgentId

  useEffect(() => {
    if (optimisticId === null) return
    if (activeAgentId === optimisticId) {
      setOptimisticId(null)
      return
    }
    // Safety: drop the optimistic highlight if the real selection never lands
    // (e.g. navigation failed) so it can't strand on a non-active agent.
    const timer = setTimeout(() => setOptimisticId(null), 3000)
    return () => clearTimeout(timer)
  }, [optimisticId, activeAgentId])

  const handleSelect = useCallback((agent: AgentInfo) => {
    setOptimisticId(agent.Id)
    onAgentClick(agent)
  }, [onAgentClick])

  const scrollRef = useRef<HTMLDivElement>(null)

  // Desktop mouse wheels scroll vertically; translate that into horizontal
  // scrolling on the avatar strip. Trackpad deltaX pans pass through untouched,
  // and the event is left alone when the strip cannot scroll further so the
  // page keeps scrolling naturally at the edges.
  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      if (e.deltaX !== 0) return
      if (el.scrollWidth <= el.clientWidth) return
      const maxScroll = el.scrollWidth - el.clientWidth
      if ((el.scrollLeft <= 0 && e.deltaY < 0) || (el.scrollLeft >= maxScroll - 1 && e.deltaY > 0)) return
      e.preventDefault()
      el.scrollLeft += e.deltaY
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [])

  // One-shot positioning when the bar is (re)shown: scroll the strip as a
  // whole so the active agent's avatar lands as close to the middle as the
  // existing 0..maxScroll limits allow — never past them just to center.
  const centeredRef = useRef(false)
  useEffect(() => {
    if (centeredRef.current || !displayActiveId) return
    const el = scrollRef.current
    if (!el) return
    const maxScroll = el.scrollWidth - el.clientWidth
    if (maxScroll <= 0) return
    const item = el.querySelector<HTMLElement>(`[data-agent-id="${CSS.escape(displayActiveId)}"]`)
    if (!item) return
    centeredRef.current = true
    const elRect = el.getBoundingClientRect()
    const itemRect = item.getBoundingClientRect()
    const itemCenter = itemRect.left - elRect.left + el.scrollLeft + itemRect.width / 2
    el.scrollLeft = Math.max(0, Math.min(maxScroll, itemCenter - el.clientWidth / 2))
  }, [displayActiveId, agents])

  // Mouse drag pans the strip horizontally, like a touch swipe on mobile.
  // Touch/pen pointers are left to the browser's native overflow scrolling;
  // only mouse dragging needs the manual 1:1 drag-to-scroll mapping.
  const [panning, setPanning] = useState(false)
  const panRef = useRef<{ pointerId: number; startX: number; startScrollLeft: number; engaged: boolean } | null>(null)
  const suppressClickRef = useRef(false)

  const handlePanPointerDown = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (e.pointerType !== 'mouse' || e.button !== 0) return
    const el = scrollRef.current
    if (!el || el.scrollWidth <= el.clientWidth) return
    panRef.current = { pointerId: e.pointerId, startX: e.clientX, startScrollLeft: el.scrollLeft, engaged: false }
  }, [])

  const handlePanPointerMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    const pan = panRef.current
    const el = scrollRef.current
    if (!pan || !el || pan.pointerId !== e.pointerId) return
    const dx = e.clientX - pan.startX
    if (!pan.engaged) {
      if (Math.abs(dx) < 6) return
      pan.engaged = true
      setPanning(true)
      el.setPointerCapture?.(e.pointerId)
    }
    e.preventDefault()
    el.scrollLeft = pan.startScrollLeft - dx
  }, [])

  const endPan = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    const pan = panRef.current
    if (!pan || pan.pointerId !== e.pointerId) return
    if (pan.engaged) {
      suppressClickRef.current = true
      setPanning(false)
      if (typeof scrollRef.current?.hasPointerCapture === 'function' && scrollRef.current.hasPointerCapture(e.pointerId)) {
        scrollRef.current.releasePointerCapture(e.pointerId)
      }
    }
    panRef.current = null
  }, [])

  // Swallow the click that the browser fires right after a drag-pan so the
  // avatar under the cursor is not selected.
  const handlePanClickCapture = useCallback((e: React.MouseEvent) => {
    if (!suppressClickRef.current) return
    suppressClickRef.current = false
    e.preventDefault()
    e.stopPropagation()
  }, [])

  // Mirror the sidebar: group by project (global-first), then build + flatten
  // the parent-child forest per group so the avatar bar shows the same
  // depth-first pre-order as the sidebar rows.
  const groups = useMemo(
    () =>
      groupAgentsByProject(agents).map(group => ({
        ...group,
        agents: flattenAgentForest(buildAgentForest(group.agents)).map(n => n.agent),
      })),
    [agents],
  )

  const hasAgents = agents.length > 0
  const showCreate = !hasAgents && onCreateAgent

  if (!hasAgents && !showCreate && !onAdd) {
    return null
  }

  return (
    <div className="agent-avatar-bar-root">
      <div className="agent-avatar-bar">
        {hasAgents && (
          <div
            className={`agent-avatar-scroll${panning ? ' panning' : ''}`}
            ref={scrollRef}
            onPointerDown={handlePanPointerDown}
            onPointerMove={handlePanPointerMove}
            onPointerUp={endPan}
            onPointerCancel={endPan}
            onClickCapture={handlePanClickCapture}
          >
            {groups.map(group => {
              const firstAgent = group.agents[0]
              if (!firstAgent) return null
              if (group.agents.length === 1) {
                return (
                  <AgentAvatarItem
                    key={firstAgent.Id}
                    agent={firstAgent}
                    active={firstAgent.Id === displayActiveId}
                    onClick={() => handleSelect(firstAgent)}
                    onMenuOpen={onAgentMenuOpen}
                  />
                )
              }
              return (
                <div key={firstAgent.Id} className="agent-avatar-group" title={firstAgent.ProjectName || firstAgent.ProjectId}>
                  {group.agents.map(agent => (
                    <AgentAvatarItem
                      key={agent.Id}
                      agent={agent}
                      active={agent.Id === displayActiveId}
                      onClick={() => handleSelect(agent)}
                      onMenuOpen={onAgentMenuOpen}
                    />
                  ))}
                </div>
              )
            })}
          </div>
        )}
        {hasAgents && showOverflow && (
          <button
            type="button"
            className="agent-avatar-more"
            title="All agents"
            onClick={() => setDropdownOpen(v => !v)}
          >
            <MoreHorizontal size={12} />
          </button>
        )}
        {showCreate ? (
          <button
            type="button"
            className="agent-avatar-add"
            title={t('agentAvatar.createAgent')}
            onClick={onCreateAgent}
          >
            <Plus size={14} />
          </button>
        ) : (
          onAdd && (
            <button
              type="button"
              className="agent-avatar-add"
              title="Add agent"
              onClick={onAdd}
            >
              <Plus size={14} />
            </button>
          )
        )}
      </div>
      {hasAgents && dropdownOpen && (
        <>
          <div className="agent-avatar-dropdown-backdrop" onClick={() => setDropdownOpen(false)} />
          <div className="agent-avatar-dropdown" ref={dropdownRef}>
            {dropdownAgents.length === 0 ? (
              <div className="agent-avatar-dropdown-empty">No agents</div>
            ) : (
              dropdownGroups
                .filter(group => group.nodes.length > 0)
                .map((group, gi) => (
                  <div className="agent-avatar-dropdown-group" key={group.projectId || '__global'}>
                    {gi > 0 && <div className="agent-avatar-dropdown-divider" />}
                    {group.projectName && (
                      <div className="agent-avatar-dropdown-group-label">{group.projectName}</div>
                    )}
                    {group.nodes.map(node => (
                      <DropdownItem
                        key={node.key}
                        agent={node.agent}
                        node={node}
                        active={node.agent.Id === displayActiveId}
                        onSelect={() => {
                          handleSelect(node.agent)
                          setDropdownOpen(false)
                        }}
                        onDelete={onDeleteAgent ? () => onDeleteAgent(node.agent) : undefined}
                      />
                    ))}
                  </div>
                ))
            )}
          </div>
        </>
      )}
    </div>
  )
}

interface DropdownItemProps {
  agent: AgentInfo
  node?: AgentTreeNode
  active: boolean
  onSelect: () => void
  onDelete?: () => void
}

/** Priority-ordered status badge overlaid on an avatar (shared by the avatar
 * bar items and the dropdown items; same chain as the sidebar session badge). */
function AgentStatusBadge({ agent }: { agent: AgentInfo }) {
  const now = useNow()
  if (agent.IsError) {
    return (
      <span className="agent-avatar-badge error" aria-label="error" title={agent.ErrorMessage || 'error'}>
        <AlertCircle size={7} strokeWidth={2.5} />
      </span>
    )
  }
  if (agent.IsPlanApproval) {
    return (
      <span className="agent-avatar-badge plan-approval" aria-label="plan approval">
        <FileText size={6} strokeWidth={2.5} />
      </span>
    )
  }
  if (agent.IsGoalSubmit) {
    return (
      <span className="agent-avatar-badge goal-submit" aria-label="goal submit">
        <Target size={6} strokeWidth={2.5} />
      </span>
    )
  }
  if (agent.IsAskUser) {
    return (
      <span className="agent-avatar-badge ask-user" aria-label="ask user">
        <HelpCircle size={7} strokeWidth={2.5} />
      </span>
    )
  }
  if (agent.IsAskPermission) {
    return (
      <span className="agent-avatar-badge ask-permission" aria-label="ask permission">
        <Lock size={6} strokeWidth={2.5} />
      </span>
    )
  }
  if (hasUnreadCompletion(agent, now)) {
    return (
      <span className="agent-avatar-badge completed" aria-label="completed">
        <Check size={7} strokeWidth={3} />
      </span>
    )
  }
  return null
}

function DropdownItem({ agent, node, active, onSelect, onDelete }: DropdownItemProps) {
  const { t } = useI18n()
  const relativeTime = useRelativeTime()
  const canDelete = onDelete != null
  const statusClass = agentStatusClass(agent.Status)
  const lastActivity = agent.LastTurnCompletedAt || agent.LastActivity

  // Worktree badge click jumps to git mode at the agent's HEAD commit without
  // selecting the agent. stopPropagation keeps the enclosing select button
  // from firing.
  const handleWorktreeJump = useCallback((e: React.MouseEvent | React.KeyboardEvent) => {
    if ('key' in e && e.key !== 'Enter' && e.key !== ' ') return
    e.preventDefault()
    e.stopPropagation()
    void jumpToWorktreeGit(agent.ProjectId, agent.WorktreeID)
  }, [agent])

  return (
    <div className="agent-avatar-dropdown-item">
      <button
        type="button"
        className={`agent-avatar-dropdown-content${active ? ' active' : ''}`}
        onClick={onSelect}
      >
        {node && node.depth > 0 ? (
          <span className="agent-avatar-dropdown-guides" aria-hidden="true">
            {node.guideLines.map((_, i) => <span key={i} className="agent-avatar-dropdown-guide" />)}
            <span className={`agent-avatar-dropdown-connector ${node.isLastChild ? 'last' : 'mid'}`} />
          </span>
        ) : null}
        <span
          className={`agent-avatar-dropdown-avatar${agent.IsWorking ? ' working' : ''}${(agent.AgentKind ?? '').toLowerCase() === 'coordinator' ? ' coordinator' : ''}${agent.LoadState !== 'loaded' ? ' unloaded' : ''}`}
          style={{ '--avatar-hue': avatarHue(agent.Id) } as React.CSSProperties}
        >
          {agentAvatarContent(agent, 18)}
          {agent.IsWorking && <span className="agent-avatar-ring" />}
          <AgentStatusBadge agent={agent} />
        </span>
        <span className="agent-avatar-dropdown-info">
          <span className="agent-avatar-dropdown-name-row">
            <span className="agent-avatar-dropdown-name">{sidebarAgentLabel(agent)}</span>
            {agent.ActiveWorkflowMapCardId ? (
              <span
                className="agent-avatar-dropdown-icon waypoints"
                title={agent.ActiveWorkflowMapCardId}
                aria-label="orchestrating workflow"
              >
                <Waypoints size={12} />
              </span>
            ) : null}
            {agent.MemoryMounted ? (
              <span
                className="agent-avatar-dropdown-icon memory"
                title={t('shell.sidebar.memoryMounted')}
                aria-label="memory mounted"
              >
                <Brain size={12} />
              </span>
            ) : null}
            {agent.WorktreeID ? (
              <span
                role="button"
                tabIndex={0}
                className="agent-avatar-dropdown-icon worktree"
                title={agent.WorktreeName || agent.WorktreeID}
                aria-label="worktree isolated"
                onClick={handleWorktreeJump}
                onKeyDown={handleWorktreeJump}
              >
                <GitBranch size={12} />
              </span>
            ) : null}
            {agent.BoundTaskCardId ? (
              <span
                className="agent-avatar-dropdown-icon target"
                title={agent.BoundTaskCardId}
                aria-label="executing task"
              >
                <Target size={12} />
              </span>
            ) : null}
            {agent.Degraded ? (
              <span
                className="agent-avatar-dropdown-degraded"
                title={agent.DegradedReason ? `agent degraded: ${agent.DegradedReason}` : 'agent degraded'}
              >!</span>
            ) : null}
          </span>
          <span className="agent-avatar-dropdown-meta">
            <span className="agent-avatar-dropdown-project">{agent.ProjectName}</span>
            <span className={`agent-avatar-dropdown-status ${statusClass}`}>{agent.StatusLabel}</span>
            {lastActivity ? (
              <span className="agent-avatar-dropdown-time" title={lastActivity}>
                {relativeTime(lastActivity)}
              </span>
            ) : null}
          </span>
        </span>
        {active && <Check size={14} />}
      </button>
      {canDelete && (
        <button
          type="button"
          className="agent-avatar-dropdown-delete"
          onClick={(e) => { e.stopPropagation(); onDelete?.() }}
          aria-label={t('common.delete')}
        >
          <Trash2 size={14} />
        </button>
      )}
    </div>
  )
}

export function AgentAvatarItem({
  agent,
  active,
  onClick,
  onMenuOpen,
}: {
  agent: AgentInfo
  active: boolean
  onClick: () => void
  onMenuOpen?: (agent: AgentInfo, x: number, y: number) => void
}) {
  const hue = avatarHue(agent.Id)

  const longPress = useLongPress({
    onLongPress: (e) => onMenuOpen?.(agent, e.clientX, e.clientY),
  })

  const itemClass = [
    'agent-avatar-item',
    active ? 'active' : '',
    agent.IsWorking ? 'working' : '',
    agent.Status === 'paused' ? 'paused' : '',
    agent.IsError ? 'error' : '',
    agent.IsPlanApproval ? 'plan-approval' : '',
    agent.IsGoalSubmit ? 'goal-submit' : '',
    agent.Status === 'ask_user' ? 'ask-user' : '',
    agent.Status === 'ask_permission' ? 'ask-permission' : '',
  ].filter(Boolean).join(' ')

  return (
    <button
      type="button"
      className={itemClass}
      data-agent-id={agent.Id}
      title={agentDisplayName(agent.Title, agent.DisplayName)}
      onClick={() => { if (!longPress.wasLongPress()) onClick() }}
      onMouseDown={(e) => { if (longPress.wasLongPress()) { e.preventDefault(); e.stopPropagation() } }}
      onContextMenu={(e) => {
        if (!onMenuOpen) return
        if (longPress.wasLongPress()) {
          e.preventDefault()
          return
        }
        e.preventDefault()
        e.stopPropagation()
        onMenuOpen(agent, e.clientX, e.clientY)
      }}
      onPointerDown={longPress.onPointerDown}
      onPointerMove={longPress.onPointerMove}
      onPointerUp={longPress.onPointerUp}
      onPointerLeave={longPress.onPointerLeave}
      onPointerCancel={longPress.onPointerCancel}
    >
      <span
        className={`agent-avatar-initials${(agent.AgentKind ?? '').toLowerCase() === 'coordinator' ? ' coordinator' : ''}${agent.LoadState !== 'loaded' ? ' unloaded' : ''}`}
        style={{ '--avatar-hue': hue } as React.CSSProperties}
      >
        {agentAvatarContent(agent, 16)}
        {agent.IsWorking && <span className="agent-avatar-ring" />}
        <AgentStatusBadge agent={agent} />
      </span>
    </button>
  )
}
