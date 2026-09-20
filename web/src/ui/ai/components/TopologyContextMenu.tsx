import React, { useLayoutEffect, useRef } from 'react'
import { UserPlus, Folder, FileText, Zap, MessageSquare, Inbox } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import './TopologyContextMenu.css'

export interface TopologyMenuTarget {
  x: number
  y: number
  canvasX: number
  canvasY: number
}

interface TopologyContextMenuProps {
  target: TopologyMenuTarget | null
  onClose: () => void
  onCreateAgent: (target: TopologyMenuTarget) => void
  onCreateProject: (target: TopologyMenuTarget) => void
  onCreateCard: (target: TopologyMenuTarget) => void
  onCreateBacklog: (target: TopologyMenuTarget) => void
  onCreateSkill: (target: TopologyMenuTarget) => void
  onCreatePrompt: (target: TopologyMenuTarget) => void
}

interface MenuItem {
  key: string
  hotkey: string
  label: string
  icon: React.ReactNode
  run: (target: TopologyMenuTarget) => void
}

export const TopologyContextMenu: React.FC<TopologyContextMenuProps> = ({
  target,
  onClose,
  onCreateAgent,
  onCreateProject,
  onCreateCard,
  onCreateBacklog,
  onCreateSkill,
  onCreatePrompt,
}) => {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)

  useMenuDismiss(menuRef, onClose, target)

  useLayoutEffect(() => {
    if (!target || !menuRef.current) return
    const menu = menuRef.current
    const rect = menu.getBoundingClientRect()
    const viewportW = document.documentElement.clientWidth
    const viewportH = document.documentElement.clientHeight
    const padding = 8

    let left = target.x
    let top = target.y

    if (left + rect.width > viewportW - padding) {
      left = target.x - rect.width
    }
    if (left < padding) {
      left = padding
    }

    if (top + rect.height > viewportH - padding) {
      top = target.y - rect.height
    }
    if (top < padding) {
      top = padding
    }

    menu.style.left = `${left}px`
    menu.style.top = `${top}px`
  }, [target])

  if (!target) return null

  const items: MenuItem[] = [
    { key: 'agent', hotkey: 'a', label: t('topologyContextMenu.createAgent'), icon: <UserPlus size={13} />, run: onCreateAgent },
    { key: 'project', hotkey: 'p', label: t('topologyContextMenu.createProject'), icon: <Folder size={13} />, run: onCreateProject },
    { key: 'card', hotkey: 'c', label: t('topologyContextMenu.createCard'), icon: <FileText size={13} />, run: onCreateCard },
    { key: 'backlog', hotkey: 'b', label: t('topologyContextMenu.createBacklog'), icon: <Inbox size={13} />, run: onCreateBacklog },
    { key: 'skill', hotkey: 's', label: t('topologyContextMenu.createSkill'), icon: <Zap size={13} />, run: onCreateSkill },
    { key: 'prompt', hotkey: 'r', label: t('topologyContextMenu.createPrompt'), icon: <MessageSquare size={13} />, run: onCreatePrompt },
  ]

  const handleKey = (event: React.KeyboardEvent) => {
    const key = event.key.toLowerCase()
    const match = items.find(item => item.hotkey === key)
    if (match) {
      event.preventDefault()
      match.run(target)
      onClose()
    }
  }

  return (
    <div
      ref={menuRef}
      className="topology-context-menu"
      style={{ left: target.x, top: target.y }}
      role="menu"
      tabIndex={-1}
      onKeyDown={handleKey}
    >
      <div className="topology-context-menu-header">
        <span className="topology-context-menu-prompt">$</span>
        <span className="topology-context-menu-title">{t('topologyContextMenu.title')}</span>
      </div>
      <div className="topology-context-menu-list">
        {items.map(item => (
          <button
            key={item.key}
            type="button"
            className="topology-context-menu-item"
            onClick={() => { item.run(target); onClose() }}
          >
            <span className="topology-context-menu-hotkey">[{item.hotkey}]</span>
            <span className="topology-context-menu-icon">{item.icon}</span>
            <span className="topology-context-menu-label">{item.label}</span>
          </button>
        ))}
      </div>
    </div>
  )
}
