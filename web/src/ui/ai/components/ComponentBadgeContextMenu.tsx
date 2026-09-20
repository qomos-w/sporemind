import React, { useLayoutEffect, useRef } from 'react'
import { Info } from 'lucide-react'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import { useBrowserOverlay } from '../browserOverlay'
import { useI18n } from '../../../i18n'
import './ComponentBadgeContextMenu.css'

export interface ComponentBadgeMenuTarget {
  /** CardId of the right-clicked mode/bundle badge. */
  cardId: string
  /** Display label shown in the menu header. */
  label: string
  x: number
  y: number
}

/**
 * Right-click menu shared by all composer mode/bundle badges. Currently one
 * entry: "details", which opens the capability overlay for the card.
 *
 * Registers with `useBrowserOverlay` so the native right-panel browser window
 * is hidden while the menu is on screen (it may overlap the window area).
 */
export const ComponentBadgeContextMenu: React.FC<{
  target: ComponentBadgeMenuTarget | null
  onClose: () => void
  onOpenDetails: (cardId: string) => void
}> = ({ target, onClose, onOpenDetails }) => {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)

  useBrowserOverlay(!!target)
  useMenuDismiss(menuRef, onClose, target)

  // Position the menu after its real size is known, flipping left/up at the
  // window edges (same strategy as AgentContextMenu).
  useLayoutEffect(() => {
    if (!target || !menuRef.current) return
    const menu = menuRef.current
    const rect = menu.getBoundingClientRect()
    const viewportW = document.documentElement.clientWidth
    const viewportH = document.documentElement.clientHeight
    const padding = 8

    let left = target.x
    let top = target.y
    if (left + rect.width > viewportW - padding) left = target.x - rect.width
    if (left < padding) left = padding
    if (top + rect.height > viewportH - padding) top = target.y - rect.height
    if (top < padding) top = padding

    menu.style.left = `${left}px`
    menu.style.top = `${top}px`
  }, [target])

  if (!target) return null

  return (
    <div
      ref={menuRef}
      className="component-badge-context-menu"
      style={{ left: target.x, top: target.y }}
      role="menu"
      tabIndex={-1}
    >
      <div className="component-badge-context-menu-header">
        <span className="component-badge-context-menu-title">{target.label}</span>
      </div>
      <div className="component-badge-context-menu-list">
        <button
          type="button"
          className="component-badge-context-menu-item"
          onClick={() => { onOpenDetails(target.cardId); onClose() }}
        >
          <span className="component-badge-context-menu-icon"><Info size={13} /></span>
          <span className="component-badge-context-menu-label">{t('composer.badge.contextMenu.details')}</span>
        </button>
      </div>
    </div>
  )
}
