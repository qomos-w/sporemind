import React, { useLayoutEffect, useRef } from 'react'
import { MessageSquare, Pencil, Copy, Trash2, Eraser, Info, FileText, Brain, Unplug } from 'lucide-react'
import type { AgentInfo } from '../hooks/agentInfoStore'
import { agentDisplayName } from '../lib/agent-avatar'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import { useI18n } from '../../../i18n'
import './AgentContextMenu.css'

export interface AgentMenuTarget {
  agent: AgentInfo
  x: number
  y: number
  /** Where the menu was opened: 'topology' shows mount/unmount brain, default shows open brain. */
  context?: 'topology' | 'general'
}

interface AgentContextMenuProps {
  target: AgentMenuTarget | null
  onClose: () => void
  onOpenChat: (agent: AgentInfo) => void
  onEdit: (agent: AgentInfo) => void
  onClone: (agent: AgentInfo) => void
  onClearHistory?: (agent: AgentInfo) => void
  onDelete: (agent: AgentInfo) => void
  onInspectInfo?: (agent: AgentInfo) => void
  onOpenCard?: (agent: AgentInfo) => void
  brainMountAgentId?: string | null
  onToggleBrainMount?: (agent: AgentInfo) => void
  /** Opens the full-view brain mode (used outside topology). */
  onOpenBrain?: (agent: AgentInfo) => void
}

interface MenuItem {
  key: string
  hotkey: string
  label: string
  icon: React.ReactNode
  danger?: boolean
  run: () => void
}

export const AgentContextMenu: React.FC<AgentContextMenuProps> = ({
  target,
  onClose,
  onOpenChat,
  onEdit,
  onClone,
  onClearHistory,
  onDelete,
  onInspectInfo,
  onOpenCard,
  brainMountAgentId,
  onToggleBrainMount,
  onOpenBrain,
}) => {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)

  useMenuDismiss(menuRef, onClose, target)

  // Position the menu after its real size is known, flipping it left/up if it
  // would otherwise overflow the window edge.
  useLayoutEffect(() => {
    if (!target || !menuRef.current) return
    const menu = menuRef.current
    const rect = menu.getBoundingClientRect()
    const viewportW = document.documentElement.clientWidth
    const viewportH = document.documentElement.clientHeight
    const padding = 8

    let left = target.x
    let top = target.y

    // Flip to the left of the cursor when the right edge would be clipped.
    if (left + rect.width > viewportW - padding) {
      left = target.x - rect.width
    }
    // Keep a small margin from the left edge.
    if (left < padding) {
      left = padding
    }

    // Flip above the cursor when the bottom edge would be clipped.
    if (top + rect.height > viewportH - padding) {
      top = target.y - rect.height
    }
    // Keep a small margin from the top edge.
    if (top < padding) {
      top = padding
    }

    menu.style.left = `${left}px`
    menu.style.top = `${top}px`
  }, [target])

  if (!target) return null

  const { agent, x, y } = target

  const items: MenuItem[] = [
    { key: 'chat', hotkey: 'c', label: t('contextMenu.openChat'), icon: <MessageSquare size={13} />, run: () => onOpenChat(agent) },
    ...(onOpenCard ? [{ key: 'card', hotkey: 'o', label: t('contextMenu.openCard'), icon: <FileText size={13} />, run: () => onOpenCard(agent) }] : []),
    ...(onToggleBrainMount && target.context === 'topology' ? [{ key: 'brain', hotkey: 'b', label: brainMountAgentId === agent.Id ? t('contextMenu.unmountBrain') : t('contextMenu.mountBrain'), icon: brainMountAgentId === agent.Id ? <Unplug size={13} /> : <Brain size={13} />, run: () => onToggleBrainMount(agent) }] : []),
    ...(onOpenBrain && target.context !== 'topology' ? [{ key: 'brain', hotkey: 'b', label: t('contextMenu.openBrain'), icon: <Brain size={13} />, run: () => onOpenBrain(agent) }] : []),
    { key: 'edit', hotkey: 'e', label: t('contextMenu.edit'), icon: <Pencil size={13} />, run: () => onEdit(agent) },
    { key: 'clone', hotkey: 'f', label: t('contextMenu.clone'), icon: <Copy size={13} />, run: () => onClone(agent) },
    ...(onInspectInfo ? [{ key: 'info', hotkey: 'i', label: t('contextMenu.inspectInfo'), icon: <Info size={13} />, run: () => onInspectInfo(agent) }] : []),
    ...(onClearHistory ? [{ key: 'clear', hotkey: 'x', label: t('contextMenu.clearChat'), icon: <Eraser size={13} />, run: () => onClearHistory(agent) }] : []),
    // Top-level agents delete via workspace.delete_agent (CanDelete gate).
    // Child agents (fork children, workflow workers — spawned by another
    // agent) are system-managed kinds, so CanDelete is false, yet the user
    // must still be able to remove a stuck child: agent_terminate is the
    // sanctioned teardown path for children. Gate on ParentAgentId, not
    // LifecycleScope, so every spawned child is covered.
    ...(agent.CanDelete || agent.ParentAgentId ? [{ key: 'delete', hotkey: 'd', label: t('contextMenu.delete'), icon: <Trash2 size={13} />, danger: true, run: () => onDelete(agent) }] : []),
  ]

  const handleKey = (event: React.KeyboardEvent) => {
    const key = event.key.toLowerCase()
    const match = items.find(item => item.hotkey === key)
    if (match) {
      event.preventDefault()
      match.run()
      onClose()
    }
  }

  return (
    <div
      ref={menuRef}
      className="agent-context-menu"
      style={{ left: x, top: y }}
      role="menu"
      tabIndex={-1}
      onKeyDown={handleKey}
    >
      <div className="agent-context-menu-header">
        <span className="agent-context-menu-prompt">$</span>
        <span className="agent-context-menu-title">{agentDisplayName(agent.Title, agent.DisplayName)}</span>
      </div>
      <div className="agent-context-menu-list">
        {items.map(item => (
          <button
            key={item.key}
            type="button"
            className={`agent-context-menu-item${item.danger ? ' danger' : ''}`}
            onClick={() => { item.run(); onClose() }}
          >
            <span className="agent-context-menu-hotkey">[{item.hotkey}]</span>
            <span className="agent-context-menu-icon">{item.icon}</span>
            <span className="agent-context-menu-label">{item.label}</span>
          </button>
        ))}
      </div>
    </div>
  )
}
