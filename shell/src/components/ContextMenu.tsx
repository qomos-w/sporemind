import React, { useEffect, useRef, useCallback } from 'react'
import './ContextMenu.css'

export interface ContextMenuItem {
  id: string
  label: string
  checked?: boolean
  separator?: boolean
  onClick?: () => void
}

interface ContextMenuProps {
  items: ContextMenuItem[]
  x: number
  y: number
  onClose: () => void
}

export const ContextMenu: React.FC<ContextMenuProps> = ({ items, x, y, onClose }) => {
  const ref = useRef<HTMLDivElement>(null)

  const handleClickOutside = useCallback((e: MouseEvent) => {
    if (ref.current && !ref.current.contains(e.target as Node)) {
      onClose()
    }
  }, [onClose])

  useEffect(() => {
    document.addEventListener('mousedown', handleClickOutside, true)
    return () => document.removeEventListener('mousedown', handleClickOutside, true)
  }, [handleClickOutside])

  // Clamp to viewport
  const style: React.CSSProperties = {
    left: Math.min(x, window.innerWidth - 200),
    top: Math.min(y, window.innerHeight - items.length * 28 - 8),
  }

  return (
    <div className="ctx-menu" ref={ref} style={style}>
      {items.map(item => {
        if (item.separator) {
          return <div key={item.id} className="ctx-menu-sep" />
        }
        return (
          <button
            key={item.id}
            className="ctx-menu-item"
            onClick={() => {
              item.onClick?.()
              onClose()
            }}
          >
            <span className="ctx-menu-check">{item.checked !== undefined ? (item.checked ? '✓' : '') : ''}</span>
            <span className="ctx-menu-label">{item.label}</span>
          </button>
        )
      })}
    </div>
  )
}
