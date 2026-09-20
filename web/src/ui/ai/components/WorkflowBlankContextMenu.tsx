import React, { useEffect, useLayoutEffect, useRef } from 'react'
import { Plus, Maximize2, MoveHorizontal } from 'lucide-react'
import { useI18n } from '../../../i18n'
import './TopologyCardContextMenu.css'

export interface WorkflowBlankMenuTarget {
  x: number
  y: number
}

interface WorkflowBlankContextMenuProps {
  target: WorkflowBlankMenuTarget | null
  onClose: () => void
  onCreateCard: () => void
  onFitView: () => void
  onDirectionChange: () => void
}

interface MenuItem {
  key: string
  label: string
  icon: React.ReactNode
  danger?: boolean
  disabled?: boolean
  active?: boolean
  run: () => void
}

export const WorkflowBlankContextMenu: React.FC<WorkflowBlankContextMenuProps> = ({
  target,
  onClose,
  onCreateCard,
  onFitView,
  onDirectionChange,
}) => {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!target) return
    const handleMouseDown = (event: MouseEvent) => {
      if (event.button !== 0) return
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        onClose()
      }
    }
    const handleContextMenu = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        onClose()
      }
    }
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    document.addEventListener('mousedown', handleMouseDown)
    document.addEventListener('contextmenu', handleContextMenu, true)
    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('mousedown', handleMouseDown)
      document.removeEventListener('contextmenu', handleContextMenu, true)
      document.removeEventListener('keydown', handleKey)
    }
  }, [target, onClose])

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
    { key: 'createCard', label: t('workflowBlankContextMenu.createCard'), icon: <Plus size={13} />, run: onCreateCard },
    { key: 'fitView', label: t('workflowBlankContextMenu.fitView'), icon: <Maximize2 size={13} />, run: onFitView },
    { key: 'direction', label: t('workflowBlankContextMenu.toggleDirection'), icon: <MoveHorizontal size={13} />, run: onDirectionChange },
  ]

  return (
    <div
      ref={menuRef}
      className="topology-card-context-menu"
      style={{ left: target.x, top: target.y }}
      role="menu"
      tabIndex={-1}
    >
      <div className="topology-card-context-menu-list">
        {items.map(item => (
          <button
            key={item.key}
            type="button"
            className={[
              'topology-card-context-menu-item',
              item.danger ? 'danger' : '',
              item.disabled ? 'disabled' : '',
            ].join(' ')}
            role="menuitem"
            disabled={item.disabled}
            onClick={() => {
              if (!item.disabled) {
                item.run()
                onClose()
              }
            }}
          >
            <span className="topology-card-context-menu-icon">{item.icon}</span>
            <span className="topology-card-context-menu-label">{item.label}</span>
          </button>
        ))}
      </div>
    </div>
  )
}
