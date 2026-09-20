import React, { useEffect, useLayoutEffect, useRef } from 'react'
import { UnfoldVertical, FoldVertical } from 'lucide-react'
import { useI18n } from '../../../i18n'
import type { EpicCategory } from './workflowLayout'
import './TopologyCardContextMenu.css'

export interface WorkflowCategoryMenuTarget {
  category: EpicCategory
  x: number
  y: number
}

interface WorkflowCategoryContextMenuProps {
  target: WorkflowCategoryMenuTarget | null
  onClose: () => void
  onExpandAll: (category: EpicCategory) => void
  onCollapseAll: (category: EpicCategory) => void
}

interface MenuItem {
  key: string
  label: string
  icon: React.ReactNode
  run: (category: EpicCategory) => void
}

export const WorkflowCategoryContextMenu: React.FC<WorkflowCategoryContextMenuProps> = ({
  target,
  onClose,
  onExpandAll,
  onCollapseAll,
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

  const { category } = target

  const items: MenuItem[] = [
    { key: 'expandAll', label: t('workflowCategoryContextMenu.expandAll'), icon: <UnfoldVertical size={13} />, run: onExpandAll },
    { key: 'collapseAll', label: t('workflowCategoryContextMenu.collapseAll'), icon: <FoldVertical size={13} />, run: onCollapseAll },
  ]

  return (
    <div
      ref={menuRef}
      className="topology-card-context-menu"
      style={{ left: target.x, top: target.y }}
      role="menu"
      tabIndex={-1}
    >
      <div className="topology-card-context-menu-header">
        <span className="topology-card-context-menu-title">
          {t(`workflowGraph.category.${category}` as never, { defaultValue: category })}
        </span>
      </div>
      <div className="topology-card-context-menu-list">
        {items.map(item => (
          <button
            key={item.key}
            type="button"
            className="topology-card-context-menu-item"
            role="menuitem"
            onClick={() => {
              item.run(category)
              onClose()
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
