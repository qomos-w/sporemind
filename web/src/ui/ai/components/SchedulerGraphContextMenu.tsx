import React, { useLayoutEffect, useRef } from 'react'
import { Play, Pause, ExternalLink, Pencil, Locate, Trash2 } from 'lucide-react'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import { useBrowserOverlay } from '../browserOverlay'
import { useI18n } from '../../../i18n'
import type { MonoCardListItem } from '../../../domain/mono-types'

export interface SchedulerGraphMenuTarget {
  card: MonoCardListItem
  x: number
  y: number
}

interface MenuItem {
  key: string
  hotkey: string
  label: string
  icon: React.ReactNode
  danger?: boolean
  run: () => void
}

interface SchedulerGraphContextMenuProps {
  target: SchedulerGraphMenuTarget | null
  onClose: () => void
  onToggle: (card: MonoCardListItem) => void
  onRunNow: (card: MonoCardListItem) => void
  onOpenCard?: (card: MonoCardListItem) => void
  onEditSchedule: (card: MonoCardListItem) => void
  onLocateMap?: (card: MonoCardListItem) => void
  onDelete: (card: MonoCardListItem) => void
}

export const SchedulerGraphContextMenu: React.FC<SchedulerGraphContextMenuProps> = ({
  target,
  onClose,
  onToggle,
  onRunNow,
  onOpenCard,
  onEditSchedule,
  onLocateMap,
  onDelete,
}) => {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)

  useMenuDismiss(menuRef, onClose, target)
  useBrowserOverlay(!!target)

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

  const { card } = target
  const schedule = (card.data as Record<string, unknown> | undefined)?.schedule as Record<string, unknown> | undefined
  const isEnabled = schedule?.enabled !== false
  const cardData = (card.data as Record<string, unknown> | undefined) ?? {}
  const hasTemplate = typeof cardData.workflow_template === 'string' && cardData.workflow_template !== ''

  const items: MenuItem[] = [
    {
      key: 'toggle',
      hotkey: 't',
      label: isEnabled ? t('scheduled.status.paused') : t('scheduled.status.enabled'),
      icon: isEnabled ? <Pause size={13} /> : <Play size={13} />,
      run: () => onToggle(card),
    },
    {
      key: 'run',
      hotkey: 'r',
      label: t('scheduled.runNow'),
      icon: <Play size={13} />,
      run: () => onRunNow(card),
    },
    ...(onOpenCard ? [{
      key: 'card',
      hotkey: 'o',
      label: t('scheduled.openCard'),
      icon: <ExternalLink size={13} />,
      run: () => onOpenCard(card),
    }] : []),
    {
      key: 'edit',
      hotkey: 'e',
      label: t('scheduled.edit.button'),
      icon: <Pencil size={13} />,
      run: () => onEditSchedule(card),
    },
    ...(hasTemplate && onLocateMap ? [{
      key: 'locate',
      hotkey: 'l',
      label: t('scheduled.locateTemplate'),
      icon: <Locate size={13} />,
      run: () => onLocateMap(card),
    }] : []),
    {
      key: 'delete',
      hotkey: 'd',
      label: t('common.delete'),
      icon: <Trash2 size={13} />,
      danger: true,
      run: () => onDelete(card),
    },
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
      style={{ left: target.x, top: target.y }}
      role="menu"
      tabIndex={-1}
      onKeyDown={handleKey}
    >
      <div className="agent-context-menu-header">
        <span className="agent-context-menu-prompt">$</span>
        <span className="agent-context-menu-title">{card.id}</span>
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