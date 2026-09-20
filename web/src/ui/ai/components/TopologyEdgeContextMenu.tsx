import React, { useLayoutEffect, useRef } from 'react'
import { FileText, Trash2 } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import type { TopologyEdgeKind, TopologyEdgeRef } from './TopologyGraph'
import './TopologyCardContextMenu.css'

export interface EdgeMenuTarget {
  edge: TopologyEdgeRef
  x: number
  y: number
}

/** Edge kinds backed by an explicit, user-removable relationship. Type mounts,
 * wikiword links, and agent bindings are derived from card structure/runtime
 * state, so deleting the rendered line is not meaningful for them. */
const DELETABLE_KINDS: ReadonlySet<TopologyEdgeKind> = new Set(['parent', 'tag', 'depends_on'])

interface TopologyEdgeContextMenuProps {
  target: EdgeMenuTarget | null
  onClose: () => void
  onOpenCards: (edge: TopologyEdgeRef) => void
  onDelete: (edge: TopologyEdgeRef) => void
}

export const TopologyEdgeContextMenu: React.FC<TopologyEdgeContextMenuProps> = ({
  target,
  onClose,
  onOpenCards,
  onDelete,
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

  const { edge } = target
  const deletable = DELETABLE_KINDS.has(edge.kind)

  return (
    <div
      ref={menuRef}
      className="topology-card-context-menu"
      style={{ left: target.x, top: target.y }}
      role="menu"
      tabIndex={-1}
    >
      <div className="topology-card-context-menu-header">
        <span className="topology-card-context-menu-title">{edge.from} → {edge.to}</span>
      </div>
      <div className="topology-card-context-menu-list">
        <button
          type="button"
          className="topology-card-context-menu-item"
          role="menuitem"
          onClick={() => {
            onOpenCards(edge)
            onClose()
          }}
        >
          <span className="topology-card-context-menu-icon"><FileText size={13} /></span>
          <span className="topology-card-context-menu-label">{t('topologyEdgeContextMenu.openCards')}</span>
        </button>
        <button
          type="button"
          className={`topology-card-context-menu-item danger${deletable ? '' : ' disabled'}`}
          role="menuitem"
          disabled={!deletable}
          onClick={() => {
            if (!deletable) return
            onDelete(edge)
            onClose()
          }}
        >
          <span className="topology-card-context-menu-icon"><Trash2 size={13} /></span>
          <span className="topology-card-context-menu-label">{t('topologyEdgeContextMenu.deleteConnection')}</span>
        </button>
      </div>
    </div>
  )
}
