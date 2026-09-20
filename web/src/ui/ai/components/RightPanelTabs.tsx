import React, { useRef, useEffect, useState, useCallback, useLayoutEffect } from 'react'
import { Plus, X, Maximize2, Minimize2, Maximize, Minimize } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { useBrowserOverlay } from '../browserOverlay'
import './RightPanelTabs.css'

export interface RightTabItem {
  id: string
  label: string
  icon?: React.ReactNode
  closable?: boolean
  render: () => React.ReactNode
  /** When true, the tab content stays mounted (display:none) when inactive
   *  instead of being unmounted. Use for stateful views like SSH sessions. */
  keepAlive?: boolean
  /** When set, overrides `id` as the React key for the rendered tab content.
   *  Use to force remount when key changes (e.g. browser tab epoch). */
  renderKey?: string
}

export interface NewTabOption {
  id: string
  label: string
  icon?: React.ReactNode
  onClick: () => void
}

interface RightPanelTabsProps {
  tabs: RightTabItem[]
  activeTabId: string | null
  onSwitchTab: (id: string) => void
  onCloseTab: (id: string) => void
  onReorderTabs?: (fromId: string, toIndex: number) => void
  onDragStartReorder?: () => void
  onDragEndReorder?: () => void
  onNewTab?: () => void
  newTabOptions?: NewTabOption[]
  isMobile?: boolean
  style?: React.CSSProperties
  onToggleExpand?: () => void
  isExpanded?: boolean
  onClosePanel?: () => void
  bottomSlot?: React.ReactNode
}

interface ContextMenuState {
  x: number
  y: number
  tabId: string
}

interface AddTabButtonProps {
  newTabOptions?: NewTabOption[]
  onNewTab?: () => void
  containerRef: React.RefObject<HTMLDivElement | null>
}

function AddTabButton({ newTabOptions, onNewTab, containerRef }: AddTabButtonProps) {
  const [addMenuOpen, setAddMenuOpen] = useState(false)
  const [menuPos, setMenuPos] = useState<{ top: number; left: number } | null>(null)
  const addMenuRef = useRef<HTMLDivElement>(null)
  const hasMenu = newTabOptions && newTabOptions.length > 0
  if (!hasMenu && !onNewTab) return null

  useBrowserOverlay(addMenuOpen)

  // Close add menu on outside click or focus moving outside the add button/menu
  useEffect(() => {
    if (!addMenuOpen) return
    const handleClick = (e: MouseEvent) => {
      if (addMenuRef.current && !addMenuRef.current.contains(e.target as Node)) {
        setAddMenuOpen(false)
      }
    }
    const handleFocus = (e: FocusEvent) => {
      if (addMenuRef.current && !addMenuRef.current.contains(e.target as Node)) {
        setAddMenuOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    document.addEventListener('focusin', handleFocus)
    return () => {
      document.removeEventListener('mousedown', handleClick)
      document.removeEventListener('focusin', handleFocus)
    }
  }, [addMenuOpen])

  const handleClick = useCallback(() => {
    if (hasMenu) {
      const wrapper = addMenuRef.current
      const container = containerRef.current
      if (wrapper && container) {
        const wrapperRect = wrapper.getBoundingClientRect()
        const containerRect = container.getBoundingClientRect()
        const menuWidth = 140
        const left = Math.max(
          containerRect.left + 4,
          Math.min(wrapperRect.right - menuWidth, window.innerWidth - menuWidth - 4)
        )
        setMenuPos({ top: wrapperRect.bottom + 4, left })
      }
      setAddMenuOpen(v => !v)
    } else {
      onNewTab?.()
    }
  }, [hasMenu, onNewTab, containerRef])

  return (
    <div className="rpt-add-tab-wrapper" ref={addMenuRef}>
      <button
        className="rpt-add-tab"
        type="button"
        onClick={handleClick}
        aria-label="New tab"
        aria-expanded={hasMenu ? addMenuOpen : undefined}
      >
        <Plus size={14} />
      </button>
      {hasMenu && addMenuOpen && menuPos && (
        <div
          className="rpt-new-tab-menu"
          style={{ top: menuPos.top, left: menuPos.left }}
        >
          {newTabOptions!.map(option => (
            <button
              key={option.id}
              className="rpt-new-tab-menu-item"
              type="button"
              onClick={() => {
                option.onClick()
                setAddMenuOpen(false)
              }}
            >
              {option.icon && <span className="rpt-new-tab-menu-icon">{option.icon}</span>}
              <span>{option.label}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

export const RightPanelTabs = React.forwardRef<HTMLDivElement, RightPanelTabsProps>(function RightPanelTabs({
  tabs,
  activeTabId,
  onSwitchTab,
  onCloseTab,
  onReorderTabs,
  onDragStartReorder,
  onDragEndReorder,
  onNewTab,
  newTabOptions,
  isMobile,
  style,
  onToggleExpand,
  isExpanded,
  onClosePanel,
  bottomSlot,
}, ref) {
  const { t } = useI18n()
  const tabbarRef = useRef<HTMLDivElement>(null)
  const [hiddenIds, setHiddenIds] = useState<Set<string>>(new Set())
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const [isFullscreen, setIsFullscreen] = useState(false)
  // Cached tab rects captured at dragstart so the ~60/s dragover handler skips
  // per-frame layout reads. Tabs don't move during a drag (reorder is on drop).
  const tabRectsRef = useRef<Map<string, { left: number; right: number }> | null>(null)

  useBrowserOverlay(!!contextMenu)

  const [draggedId, setDraggedId] = useState<string | null>(null)
  const [dragOverIndex, setDragOverIndex] = useState<number | null>(null)
  const enableReorder = !!onReorderTabs

  const draggedIndex = draggedId ? tabs.findIndex((t) => t.id === draggedId) : -1

  const computeDragOverIndex = useCallback((e: React.DragEvent) => {
    const bar = tabbarRef.current
    if (!bar) return null
    const tabEls = Array.from(bar.querySelectorAll('.rpt-tab')) as HTMLElement[]
    if (tabEls.length === 0) return 0
    const clientX = e.clientX
    const cached = tabRectsRef.current
    let bestEdge: { idx: number; dist: number } | null = null
    for (let i = 0; i < tabEls.length; i++) {
      const el = tabEls[i]!
      const cachedRect = cached?.get(el.dataset.tabId!)
      const rect = cachedRect ?? { left: el.getBoundingClientRect().left, right: el.getBoundingClientRect().right }
      const dLeft = Math.abs(clientX - rect.left)
      const dRight = Math.abs(clientX - rect.right)
      if (!bestEdge || dLeft < bestEdge.dist) {
        bestEdge = { idx: i, dist: dLeft }
      }
      if (dRight < bestEdge.dist) {
        bestEdge = { idx: i + 1, dist: dRight }
      }
    }
    return bestEdge?.idx ?? tabEls.length
  }, [])

  const handleDragStart = useCallback((e: React.DragEvent, tabId: string) => {
    e.dataTransfer.setData('text/plain', tabId)
    e.dataTransfer.effectAllowed = 'move'
    setDraggedId(tabId)
    // Cache tab rects once; tabs do not move during a drag, so the per-frame
    // dragover can resolve the drop edge from cached values instead of layout.
    const bar = tabbarRef.current
    if (bar) {
      const rects = new Map<string, { left: number; right: number }>()
      bar.querySelectorAll('.rpt-tab').forEach((el) => {
        const tabEl = el as HTMLElement
        const r = tabEl.getBoundingClientRect()
        rects.set(tabEl.dataset.tabId!, { left: r.left, right: r.right })
      })
      tabRectsRef.current = rects
    }
    onDragStartReorder?.()
  }, [onDragStartReorder])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
    const idx = computeDragOverIndex(e)
    if (idx !== null) setDragOverIndex(idx)
  }, [computeDragOverIndex])

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    const fromId = e.dataTransfer.getData('text/plain')
    if (fromId && dragOverIndex !== null && onReorderTabs) {
      onReorderTabs(fromId, dragOverIndex)
    }
    setDraggedId(null)
    setDragOverIndex(null)
    tabRectsRef.current = null
  }, [dragOverIndex, onReorderTabs])

  const handleDragEnd = useCallback(() => {
    setDraggedId(null)
    setDragOverIndex(null)
    tabRectsRef.current = null
    onDragEndReorder?.()
  }, [onDragEndReorder])

  const setContainerRef = useCallback((node: HTMLDivElement | null) => {
    containerRef.current = node
    if (typeof ref === 'function') {
      ref(node)
    } else if (ref) {
      ref.current = node
    }
  }, [ref])

  // Track hidden tabs by overflow
  useEffect(() => {
    const bar = tabbarRef.current
    if (!bar) return
    const updateHidden = () => {
      const tabEls = Array.from(bar.querySelectorAll('.rpt-tab')) as HTMLElement[]
      const barRect = bar.getBoundingClientRect()
      const hidden = new Set<string>()
      tabEls.forEach((el) => {
        const rect = el.getBoundingClientRect()
        if (rect.left < barRect.left || rect.right > barRect.right) {
          hidden.add(el.dataset.tabId!)
        }
      })
      setHiddenIds(hidden)
    }
    const ro = new ResizeObserver(updateHidden)
    ro.observe(bar)
    updateHidden()
    return () => ro.disconnect()
  }, [tabs.length, activeTabId])

  // Track tabbar height for floating-mode body offset
  useLayoutEffect(() => {
    const bar = tabbarRef.current
    const container = containerRef.current
    if (!bar || !container) return
    const update = () => {
      container.style.setProperty('--rpt-tabbar-h', `${bar.offsetHeight}px`)
    }
    update()
    const ro = new ResizeObserver(update)
    ro.observe(bar)
    return () => ro.disconnect()
  }, [isExpanded, tabs.length, activeTabId])

  // Auto-scroll active tab into view
  useEffect(() => {
    const bar = tabbarRef.current
    if (!bar) return
    const active = bar.querySelector('.rpt-tab.active') as HTMLElement | null
    if (active) {
      active.scrollIntoView({ behavior: 'smooth', inline: 'nearest' })
    }
  }, [activeTabId])

  // Close context menu on outside click
  useEffect(() => {
    if (!contextMenu) return
    const handleClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setContextMenu(null)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [contextMenu])

  // Close context menu on Escape
  useEffect(() => {
    if (!contextMenu) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setContextMenu(null)
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [contextMenu])

  // Escape also exits mobile fullscreen
  useEffect(() => {
    if (!isFullscreen) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setIsFullscreen(false)
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [isFullscreen])

  // Reset fullscreen when the panel closes
  useEffect(() => {
    if (tabs.length === 0) setIsFullscreen(false)
  }, [tabs.length])

  const activeTab = tabs.find((t) => t.id === activeTabId)
  const renderTabBody = () => (
    <>
      {tabs
        .filter(tab => tab.keepAlive)
        // Stable DOM order independent of the tabbar order: reordering tabs
        // moves DOM nodes, and moving an <iframe> in the tree discards its
        // browsing context (state lost) even though it stays mounted. Only
        // one pane is visible at a time, so DOM order has no visual effect.
        .sort((a, b) => (a.renderKey ?? a.id).localeCompare(b.renderKey ?? b.id))
        .map(tab => {
        const isActive = tab.id === activeTabId
        const tabKey = tab.renderKey ?? tab.id
        return (
          <div
            key={tabKey}
            className={`rpt-tab-pane ${isActive ? 'active' : 'hidden'}`}
            aria-hidden={!isActive}
          >
            {tab.render()}
          </div>
        )
      })}
      {activeTab && !activeTab.keepAlive
        ? React.cloneElement(activeTab.render() as React.ReactElement, { key: activeTab.renderKey ?? activeTab.id })
        : !activeTab && <div className="rpt-empty">{t('rightPanel.selectTab')}</div>}
    </>
  )

  const handleContextMenu = useCallback((e: React.MouseEvent, tabId: string) => {
    e.preventDefault()
    setContextMenu({ x: e.clientX, y: e.clientY, tabId })
  }, [])

  if (tabs.length === 0) {
    if (isMobile) return null
    return (
      <div className="rpt-container" ref={setContainerRef} style={style}>
        <div className="rpt-tabbar">
          <AddTabButton newTabOptions={newTabOptions} onNewTab={onNewTab} containerRef={containerRef} />
        </div>
      </div>
    )
  }

  if (isMobile) {
    return (
      <div className="rpt-mobile-backdrop" onClick={() => onClosePanel?.()}>
        <div
          className={`rpt-mobile-container ${isFullscreen ? 'fullscreen' : ''}`}
          onClick={(e) => e.stopPropagation()}
        >
          <div className="rpt-mobile-header">
            <div className="rpt-mobile-tabbar">
              {tabs.map((tab) => {
                const isActive = tab.id === activeTabId
                return (
                  <div
                    key={tab.id}
                    className={`rpt-mobile-tab ${isActive ? 'active' : ''}`}
                    onClick={() => onSwitchTab(tab.id)}
                  >
                    {tab.icon && <span className="rpt-tab-icon">{tab.icon}</span>}
                    <span className="rpt-tab-label">{tab.label}</span>
                    {tab.closable !== false && (
                      <button
                        className="rpt-tab-close"
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation()
                          onCloseTab(tab.id)
                        }}
                        aria-label={t('rightPanel.closeTabAriaLabel')}
                      >
                        <X size={12} />
                      </button>
                    )}
                  </div>
                )
              })}
            </div>
            <button
              className="rpt-mobile-fullscreen-btn"
              type="button"
              onClick={() => setIsFullscreen((v) => !v)}
              aria-label={isFullscreen ? t('rightPanel.exitFullscreen') : t('rightPanel.fullscreen')}
              title={isFullscreen ? t('rightPanel.exitFullscreen') : t('rightPanel.fullscreen')}
            >
              {isFullscreen ? <Minimize size={18} /> : <Maximize size={18} />}
            </button>
            <button className="rpt-mobile-close-all" onClick={() => onClosePanel?.()}>
              <X size={18} />
            </button>
          </div>
            <div className="rpt-mobile-body">
              {renderTabBody()}
            </div>
        </div>
      </div>
    )
  }

  return (
    <div className={`rpt-container${isExpanded && (activeTabId === 'mode:conversation' || activeTabId === 'mode:topology' || activeTabId === 'mode:files' || activeTabId === 'mode:ssh' || activeTabId === 'mode:problems' || activeTabId === 'mode:notes' || activeTabId === 'mode:git' || activeTabId === 'mode:aistats') ? ' rpt-floating-mode' : ''}`} ref={setContainerRef} style={style}>
      <div className="rpt-tabbar" ref={tabbarRef}>
        <div
          className="rpt-tabbar-scroll"
          onDragOver={handleDragOver}
          onDrop={handleDrop}
          onDragLeave={(e) => {
            // Clear only when leaving the tabbar for real. Crossing from one
            // child tab to the next fires dragleave with relatedTarget still
            // inside the bar; clearing there blinks the drop indicator off for
            // a frame before the next dragover re-sets it.
            const bar = tabbarRef.current
            if (bar && !bar.contains(e.relatedTarget as Node)) setDragOverIndex(null)
          }}
        >
          {tabs.map((tab, index) => {
            const isActive = tab.id === activeTabId
            const isHidden = hiddenIds.has(tab.id)
            const isDragging = tab.id === draggedId
            const showDropBefore = enableReorder
              && dragOverIndex !== null
              && dragOverIndex === index
              && dragOverIndex !== draggedIndex
              && dragOverIndex !== draggedIndex + 1
            const showDropAfter = enableReorder
              && dragOverIndex !== null
              && dragOverIndex === index + 1
              && dragOverIndex !== draggedIndex
              && dragOverIndex !== draggedIndex + 1
            return (
              <div
                key={tab.id}
                data-tab-id={tab.id}
                draggable={enableReorder}
                className={`rpt-tab ${isActive ? 'active' : ''} ${isHidden ? 'hidden' : ''} ${isDragging ? 'dragging' : ''} ${showDropBefore ? 'drop-before' : ''} ${showDropAfter ? 'drop-after' : ''}`}
                onClick={() => onSwitchTab(tab.id)}
                onContextMenu={(e) => handleContextMenu(e, tab.id)}
                onDragStart={(e) => handleDragStart(e, tab.id)}
                onDragEnd={handleDragEnd}
                title={tab.label}
              >
                {tab.icon && <span className="rpt-tab-icon">{tab.icon}</span>}
                <span className="rpt-tab-label">{tab.label}</span>
                {tab.closable !== false && (
                  <button
                    className="rpt-tab-close"
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation()
                      onCloseTab(tab.id)
                    }}
                    aria-label={t('rightPanel.closeTabAriaLabel')}
                  >
                    <X size={12} />
                  </button>
                )}
              </div>
            )
          })}
          <AddTabButton newTabOptions={newTabOptions} onNewTab={onNewTab} containerRef={containerRef} />
        </div>
        <div className="rpt-tabbar-actions">
          {onToggleExpand && (
            <button
              className={`rpt-action-btn ${isExpanded ? 'active' : ''}`}
              type="button"
              onClick={onToggleExpand}
              title={isExpanded ? t('rightPanel.collapse') : t('rightPanel.expand')}
            >
              {isExpanded ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
            </button>
          )}
        </div>
      </div>
      <div className="rpt-body">
        {renderTabBody()}
      </div>
      {isExpanded && bottomSlot && (
        <div className="rpt-bottom-slot">
          {bottomSlot}
        </div>
      )}
      {contextMenu && (
        <div
          ref={menuRef}
          className="rpt-context-menu"
          style={{ left: contextMenu.x, top: contextMenu.y }}
        >
          {(() => {
            const tab = tabs.find((t) => t.id === contextMenu.tabId)
            return tab?.closable !== false ? (
              <button
                className="rpt-context-menu-item"
                onClick={() => {
                  onCloseTab(contextMenu.tabId)
                  setContextMenu(null)
                }}
              >
                <X size={14} />
                <span>{t('rightPanel.close')}</span>
              </button>
            ) : null
          })()}
        </div>
      )}
    </div>
  )
})
