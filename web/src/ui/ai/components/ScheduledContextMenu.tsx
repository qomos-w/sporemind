import React, { useLayoutEffect, useRef } from 'react'
import { Play, Pause, ExternalLink, Pencil, Locate, Trash2 } from 'lucide-react'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import { useBrowserOverlay } from '../browserOverlay'
import { useI18n } from '../../../i18n'
import type { TimerRow } from './ScheduledView'
import { Button } from '../../settings/shadcn/ui'
import { cn } from '../../settings/shadcn/lib/utils'

export interface ScheduledMenuTarget {
  row: TimerRow
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

interface ScheduledContextMenuProps {
  target: ScheduledMenuTarget | null
  onClose: () => void
  onToggle: (row: TimerRow) => void
  onRunNow: (row: TimerRow) => void
  onOpenCard?: (row: TimerRow) => void
  onEditSchedule: (row: TimerRow) => void
  onLocateMap?: (row: TimerRow) => void
  onDelete: (row: TimerRow) => void
}

export const ScheduledContextMenu: React.FC<ScheduledContextMenuProps> = ({
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

  const { row } = target

  const items: MenuItem[] = [
    {
      key: 'toggle',
      hotkey: 't',
      label: row.timer.Enabled ? t('scheduled.status.paused') : t('scheduled.status.enabled'),
      icon: row.timer.Enabled ? <Pause size={13} /> : <Play size={13} />,
      run: () => onToggle(row),
    },
    {
      key: 'run',
      hotkey: 'r',
      label: t('scheduled.runNow'),
      icon: <Play size={13} />,
      run: () => onRunNow(row),
    },
    ...(onOpenCard ? [{
      key: 'card',
      hotkey: 'o',
      label: t('scheduled.openCard'),
      icon: <ExternalLink size={13} />,
      run: () => onOpenCard(row),
    }] : []),
    {
      key: 'edit',
      hotkey: 'e',
      label: t('scheduled.edit.button'),
      icon: <Pencil size={13} />,
      run: () => onEditSchedule(row),
    },
    ...(onLocateMap ? [{
      key: 'locate',
      hotkey: 'l',
      label: t('scheduled.locateTemplate'),
      icon: <Locate size={13} />,
      run: () => onLocateMap(row),
    }] : []),
    {
      key: 'delete',
      hotkey: 'd',
      label: t('common.delete'),
      icon: <Trash2 size={13} />,
      danger: true,
      run: () => onDelete(row),
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
      className="scheduled-context-menu fixed z-[1000] isolate min-w-[220px] rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-md outline-none"
      style={{ left: target.x, top: target.y }}
      role="menu"
      tabIndex={-1}
      onKeyDown={handleKey}
    >
      <div className="scheduled-context-menu-header flex items-center gap-2 border-b border-border px-2.5 py-1.5 text-xs text-muted-foreground">
        <span className="font-semibold text-primary">$</span>
        <span className="scheduled-context-menu-title max-w-[220px] truncate">{row.title}</span>
      </div>
      <div className="scheduled-context-menu-list flex flex-col py-1">
        {items.map(item => (
          <Button
            key={item.key}
            type="button"
            role="menuitem"
            variant="ghost"
            size="sm"
            className={cn(
              'scheduled-context-menu-item w-full justify-start gap-3 rounded-md px-2 py-1.5 text-xs font-normal',
              item.danger && 'scheduled-context-menu-item-danger text-destructive hover:bg-destructive/10 hover:text-destructive'
            )}
            onClick={() => { item.run(); onClose() }}
          >
            <span className="w-5 text-[10.5px] text-muted-foreground">[{item.hotkey}]</span>
            <span className="text-muted-foreground">{item.icon}</span>
            <span>{item.label}</span>
          </Button>
        ))}
      </div>
    </div>
  )
}
