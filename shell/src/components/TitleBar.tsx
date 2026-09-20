import React, { useState, useEffect, useRef, useCallback } from 'react'
import { Minus, Square, X, Hexagon, Circle, ChevronRight } from 'lucide-react'
import { browserNoOpController, type WindowController } from '../adapters/window'
import './TitleBar.css'

export interface MenuItem {
  id: string
  label?: string
  submenu?: MenuItem[]
  disabled?: boolean
  separator?: boolean
  checked?: boolean
}

interface TitleBarProps {
  title?: string
  menuItems?: MenuItem[]
  shellMode?: 'workbench' | 'standalone-ai-shell' | 'standalone-preview-shell' | 'multiconsole'
  onShellModeChange?: (mode: 'workbench' | 'standalone-ai-shell' | 'standalone-preview-shell' | 'multiconsole') => void
  onMenuItemClick?: (itemId: string) => void
  /** Window controller for minimise/maximise/close. Defaults to browser no-op. */
  controller?: WindowController
  /** Show multiconsole button (desktop only). */
  showMulticonsole?: boolean
  /** When in multiconsole mode, render agent avatars in the title bar. */
  agentAvatars?: React.ReactNode
  /** Extra content rendered in the right section before window controls. */
  rightSlot?: React.ReactNode
}

interface MenuListProps {
  items: MenuItem[]
  onItemClick: (itemId: string) => void
  level?: number
}

const MenuList: React.FC<MenuListProps> = ({ items, onItemClick, level = 0 }) => {
  return (
    <div className={`tb-submenu tb-submenu-level-${level}`}>
      {items.map(sub => (
        sub.separator ? (
          <div key={sub.id} className="tb-submenu-separator" />
        ) : sub.submenu ? (
          <div key={sub.id} className="tb-submenu-item-wrapper has-children">
            <button
              className={`tb-submenu-item ${sub.disabled ? 'disabled' : ''}`}
              disabled={sub.disabled}
              type="button"
            >
              <span className="tb-submenu-check">{sub.checked ? '✓' : ''}</span>
              <span className="tb-submenu-label">{sub.label}</span>
              <span className="tb-submenu-chevron"><ChevronRight size={12} /></span>
            </button>
            <MenuList items={sub.submenu} onItemClick={onItemClick} level={level + 1} />
          </div>
        ) : (
          <button
            key={sub.id}
            className={`tb-submenu-item ${sub.disabled ? 'disabled' : ''}`}
            disabled={sub.disabled}
            onClick={() => onItemClick(sub.id)}
            type="button"
          >
            <span className="tb-submenu-check">{sub.checked ? '✓' : ''}</span>
            <span className="tb-submenu-label">{sub.label}</span>
          </button>
        )
      ))}
    </div>
  )
}

export const TitleBar: React.FC<TitleBarProps> = ({
  title = '',
  menuItems = [],
  shellMode = 'workbench',
  onShellModeChange,
  onMenuItemClick,
  controller = browserNoOpController,
  showMulticonsole = false,
  agentAvatars,
  rightSlot,
}) => {
  const [maximised, setMaximised] = useState(false)
  const [openMenuId, setOpenMenuId] = useState<string | null>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const [mcDropdownOpen, setMcDropdownOpen] = useState(false)
  const mcDropdownRef = useRef<HTMLDivElement>(null)

  const refreshMaximised = useCallback(() => {
    controller.isMaximised().then(setMaximised).catch(() => {})
  }, [controller])

  useEffect(() => {
    refreshMaximised()

    const handleWindowStateChange = () => {
      refreshMaximised()
    }
    window.addEventListener('resize', handleWindowStateChange)
    window.addEventListener('focus', handleWindowStateChange)
    return () => {
      window.removeEventListener('resize', handleWindowStateChange)
      window.removeEventListener('focus', handleWindowStateChange)
    }
  }, [refreshMaximised])

  useEffect(() => {
    if (!openMenuId) return
    const handleClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setOpenMenuId(null)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [openMenuId])

  useEffect(() => {
    if (!mcDropdownOpen) return
    const handleClick = (e: MouseEvent) => {
      if (mcDropdownRef.current && !mcDropdownRef.current.contains(e.target as Node)) {
        setMcDropdownOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [mcDropdownOpen])

  useEffect(() => {
    const onGrid = (e: Event) => {
      const detail = (e as CustomEvent<{ cols: number; rows: number }>).detail
      setMcCols(detail.cols)
      setMcRows(detail.rows)
    }
    window.addEventListener('sporemind:multiconsole-grid', onGrid)
    return () => {
      window.removeEventListener('sporemind:multiconsole-grid', onGrid)
    }
  }, [])

  const handleMenuToggle = useCallback((id: string) => {
    setOpenMenuId(prev => prev === id ? null : id)
  }, [])

  const handleSubmenuClick = useCallback((itemId: string) => {
    setOpenMenuId(null)
    onMenuItemClick?.(itemId)
  }, [onMenuItemClick])

  const handleMinimise = useCallback(() => {
    if (!controller.isHostMode()) return
    controller.minimise()
  }, [controller])

  const handleToggleMaximise = useCallback(() => {
    if (!controller.isHostMode()) return
    controller.toggleMaximise().finally(refreshMaximised)
  }, [controller, refreshMaximised])

  const handleClose = useCallback(() => {
    if (!controller.isHostMode()) return
    controller.quit()
  }, [controller])

  const handleDragDoubleClick = useCallback(() => {
    handleToggleMaximise()
  }, [handleToggleMaximise])

  const isWorkbench = shellMode === 'workbench'
  const isMulticonsole = shellMode === 'multiconsole'
  const showShellModeSwitch = isWorkbench || isMulticonsole
  const showMenuItems = isWorkbench && menuItems.length > 0
  const showMcButton = isMulticonsole || showMulticonsole

  const [mcCols, setMcCols] = useState(() => {
    const saved = localStorage.getItem('sporemind:multiconsole-grid-config')
    if (saved) {
      try {
        return JSON.parse(saved).cols
      } catch { /* ignore */ }
    }
    return 2
  })
  const [mcRows, setMcRows] = useState(() => {
    const saved = localStorage.getItem('sporemind:multiconsole-grid-config')
    if (saved) {
      try {
        return JSON.parse(saved).rows
      } catch { /* ignore */ }
    }
    return 2
  })

  return (
    <div className="titlebar">
      <div className="tb-menu-section" ref={menuRef}>
        {showShellModeSwitch && (
          <button
            className={`tb-shell-mode-switch ${isWorkbench ? 'ide' : 'shell'}`}
            type="button"
            onClick={() => onShellModeChange?.(isWorkbench ? 'standalone-ai-shell' : 'workbench')}
            title={isWorkbench ? 'Workbench' : 'Shell'}
            aria-label={isWorkbench ? 'Switch to shell mode' : 'Switch to workbench mode'}
          >
            {isWorkbench ? <Hexagon size={14} /> : <Circle size={14} />}
          </button>
        )}
        {showMenuItems && menuItems.map(item => (
          <div key={item.id} className="tb-menu-item-wrapper">
            <button
              className={`tb-menu-item ${openMenuId === item.id ? 'open' : ''}`}
              onClick={() => handleMenuToggle(item.id)}
              onPointerEnter={() => { if (openMenuId) setOpenMenuId(item.id) }}
              type="button"
            >
              {item.label}
            </button>
            {openMenuId === item.id && item.submenu && (
              <MenuList items={item.submenu} onItemClick={handleSubmenuClick} />
            )}
          </div>
        ))}
      </div>

      <div className="tb-drag-region" onDoubleClick={handleDragDoubleClick}>
        {isMulticonsole && agentAvatars ? (
          <div className="tb-multiconsole-avatars">
            {agentAvatars}
          </div>
        ) : (
          title ? <span className="tb-title">{title}</span> : null
        )}
      </div>

      <div className="tb-center-toggle" onDoubleClick={(event) => event.stopPropagation()}>
      </div>

      <div className="tb-right-section">
        {showMcButton && (
          <div className="tb-multiconsole-wrapper" ref={mcDropdownRef}>
            <button
              className={`tb-multiconsole-btn ${isMulticonsole ? 'active' : ''}`}
              type="button"
              onClick={() => {
                if (isMulticonsole) {
                  setMcDropdownOpen(prev => !prev)
                } else {
                  onShellModeChange?.('multiconsole')
                }
              }}
              title="Multiconsole"
            >
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.2">
                <rect x="1" y="1" width="5" height="5" rx="1" />
                <rect x="8" y="1" width="5" height="5" rx="1" />
                <rect x="1" y="8" width="5" height="5" rx="1" />
                <rect x="8" y="8" width="5" height="5" rx="1" />
              </svg>
            </button>
            {mcDropdownOpen && isMulticonsole && (
              <div className="tb-multiconsole-dropdown">
                <div className="tb-mc-selector">
                  <div className="tb-mc-slider-row">
                    <span className="tb-mc-slider-label">Width</span>
                    <div className="tb-mc-slider-track">
                      {[1,2,3,4,5].map(v => (
                        <button
                          key={v}
                          className={`tb-mc-slider-dot ${mcCols >= v ? 'active' : ''}`}
                          type="button"
                          onClick={() => {
                            setMcCols(v)
                            window.dispatchEvent(new CustomEvent('sporemind:multiconsole-grid', { detail: { cols: v, rows: mcRows } }))
                          }}
                          title={`${v}`}
                        />
                      ))}
                    </div>
                    <span className="tb-mc-slider-value">{mcCols}</span>
                  </div>
                  <div className="tb-mc-slider-row">
                    <span className="tb-mc-slider-label">Height</span>
                    <div className="tb-mc-slider-track">
                      {[1,2,3,4,5].map(v => (
                        <button
                          key={v}
                          className={`tb-mc-slider-dot ${mcRows >= v ? 'active' : ''}`}
                          type="button"
                          onClick={() => {
                            setMcRows(v)
                            window.dispatchEvent(new CustomEvent('sporemind:multiconsole-grid', { detail: { cols: mcCols, rows: v } }))
                          }}
                          title={`${v}`}
                        />
                      ))}
                    </div>
                    <span className="tb-mc-slider-value">{mcRows}</span>
                  </div>
                </div>
              </div>
            )}
          </div>
        )}
        {rightSlot}
        <div className="tb-window-controls">
          <button className="tb-win-btn" onClick={handleMinimise} title="Minimize" type="button">
            <Minus size={14} />
          </button>
          <button className="tb-win-btn" onClick={handleToggleMaximise} title={maximised ? 'Restore' : 'Maximize'} type="button">
            {maximised
              ? <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.2"><rect x="2.5" y="4" width="8" height="8" rx="1" /><path d="M4.5 4V2.5a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H10" /></svg>
              : <Square size={12} />
            }
          </button>
          <button className="tb-win-btn close" onClick={handleClose} title="Close" type="button">
            <X size={14} />
          </button>
        </div>
      </div>
    </div>
  )
}
