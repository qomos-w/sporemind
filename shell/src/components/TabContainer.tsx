import React, { useState, useCallback, useRef, useEffect, useSyncExternalStore } from 'react'
import { X, MoreVertical } from 'lucide-react'
import type { LeafNode, ViewType, TabItem } from '../types/layout'
import { noBadgeProvider, type TabBadgeProvider } from '../adapters/tab-badge'
import { getTabContextMenuLabels, type TabContextMenuLocale } from '../i18n/tab-context-menu'
import './TabContainer.css'

const TAB_DND_TYPE = 'application/sporemind-tab'
const TAB_BAR_HEIGHT = 28 // matches --tab-bar-height

// Module-level fallback for WebView2/Chromium environments where
// getData() returns empty string for custom MIME types during drop.
let _activeDrag: { leafId: string; tabId: string } | null = null

interface TabContainerProps {
  leaf: LeafNode
  renderTabContent: (tab: TabItem) => React.ReactNode
  renderViewToolbar?: (viewType: ViewType) => React.ReactNode
  /** Optional resolver to render a small icon before the tab label. */
  getTabIcon?: (tab: TabItem) => React.ReactNode
  /** Optional provider that drives the per-tab dirty-dot indicator. */
  badgeProvider?: TabBadgeProvider
  /** Locale for the tab context menu labels. */
  locale?: TabContextMenuLocale
  onCloseTab?: (leafId: string, tabId: string) => void
  onSetActiveTab?: (leafId: string, index: number) => void
  onMoveTab?: (fromLeafId: string, tabId: string, toLeafId: string, insertIndex?: number) => void
  onMoveTabToEdge?: (fromLeafId: string, tabId: string, targetLeafId: string, edge: 'top' | 'bottom' | 'left' | 'right') => void
}

type DropEdge = 'top' | 'bottom' | 'left' | 'right' | 'center' | null

export const TabContainer: React.FC<TabContainerProps> = ({
  leaf,
  renderTabContent,
  renderViewToolbar,
  getTabIcon,
  badgeProvider = noBadgeProvider,
  locale,
  onCloseTab,
  onSetActiveTab,
  onMoveTab,
  onMoveTabToEdge,
}) => {
  const [dropEdge, setDropEdge] = useState<DropEdge>(null)
  const [dropTabIndex, setDropTabIndex] = useState<number | null>(null)
  const [isDragActive, setIsDragActive] = useState(false)
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; tabIndex: number } | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const tabbarRef = useRef<HTMLDivElement>(null)
  const overflowBtnRef = useRef<HTMLButtonElement>(null)
  const [overflowOpen, setOverflowOpen] = useState(false)
  const [hiddenIndices, setHiddenIndices] = useState<number[]>([])

  const labels = getTabContextMenuLabels(locale)

  // Subscribe to badge provider for per-tab dirty/badge indicators.
  useSyncExternalStore(badgeProvider.subscribe, badgeProvider.getVersion)

  const activeTab = leaf.tabs[leaf.activeTabIndex] || null

  // Close context menu when clicking outside
  useEffect(() => {
    if (!contextMenu) return
    const handleClick = () => setContextMenu(null)
    document.addEventListener('click', handleClick)
    return () => document.removeEventListener('click', handleClick)
  }, [contextMenu])

  // Track which tabs are hidden by overflow
  useEffect(() => {
    const bar = tabbarRef.current
    if (!bar) return
    const updateHidden = () => {
      const tabs = Array.from(bar.querySelectorAll('.tc-tab')) as HTMLElement[]
      const barRect = bar.getBoundingClientRect()
      const barLeft = barRect.left
      const barRight = barRect.right - 28 // reserve overflow button width
      const hidden: number[] = []
      tabs.forEach((tab, i) => {
        const rect = tab.getBoundingClientRect()
        if (rect.left < barLeft || rect.right > barRight) {
          hidden.push(i)
        }
      })
      setHiddenIndices(hidden)
    }
    const ro = new ResizeObserver(updateHidden)
    ro.observe(bar)
    bar.addEventListener('scroll', updateHidden)
    updateHidden()
    return () => {
      ro.disconnect()
      bar.removeEventListener('scroll', updateHidden)
    }
  }, [leaf.tabs.length, leaf.activeTabIndex])

  // Auto-scroll active tab into view
  useEffect(() => {
    const bar = tabbarRef.current
    if (!bar) return
    const activeTab = bar.querySelector('.tc-tab.active') as HTMLElement | null
    if (activeTab) {
      activeTab.scrollIntoView({ behavior: 'smooth', inline: 'nearest' })
    }
  }, [leaf.activeTabIndex])

  // Close overflow menu on outside click
  useEffect(() => {
    if (!overflowOpen) return
    const handleClick = () => setOverflowOpen(false)
    document.addEventListener('click', handleClick)
    return () => document.removeEventListener('click', handleClick)
  }, [overflowOpen])

  const handleTabContextMenu = useCallback((e: React.MouseEvent, tabIndex: number) => {
    e.preventDefault()
    setOverflowOpen(false)
    setContextMenu({ x: e.clientX, y: e.clientY, tabIndex })
  }, [])

  const handleCloseTab = useCallback((tabIndex: number) => {
    const tab = leaf.tabs[tabIndex]
    if (tab) onCloseTab?.(leaf.id, tab.id)
    setContextMenu(null)
  }, [leaf.id, leaf.tabs, onCloseTab])

  const handleCloseOthers = useCallback((tabIndex: number) => {
    leaf.tabs.forEach((tab, i) => {
      if (i !== tabIndex && tab.closable) {
        onCloseTab?.(leaf.id, tab.id)
      }
    })
    setContextMenu(null)
  }, [leaf.id, leaf.tabs, onCloseTab])

  const handleCloseRight = useCallback((tabIndex: number) => {
    for (let i = tabIndex + 1; i < leaf.tabs.length; i++) {
      const tab = leaf.tabs[i]!
      if (tab.closable) onCloseTab?.(leaf.id, tab.id)
    }
    setContextMenu(null)
  }, [leaf.id, leaf.tabs, onCloseTab])

  // Track global drag start/end so every TabContainer knows when to show
  // its capture overlay, even if the drag started in a different container.
  useEffect(() => {
    const onStart = () => setIsDragActive(true)
    const onEnd   = () => { setIsDragActive(false); setDropEdge(null); setDropTabIndex(null) }
    document.addEventListener('dragstart', onStart)
    document.addEventListener('dragend',   onEnd)
    return () => {
      document.removeEventListener('dragstart', onStart)
      document.removeEventListener('dragend',   onEnd)
    }
  }, [])

  // ── Drag start ──
  const handleTabDragStart = useCallback((e: React.DragEvent, tab: TabItem) => {
    _activeDrag = { leafId: leaf.id, tabId: tab.id }
    e.dataTransfer.setData(TAB_DND_TYPE, JSON.stringify(_activeDrag))
    e.dataTransfer.effectAllowed = 'move'
  }, [leaf.id])

  // ── Tab bar: calculate insert index from cursor X ──
  const getTabInsertIndex = useCallback((clientX: number): number => {
    const bar = tabbarRef.current
    if (!bar) return leaf.tabs.length
    const tabs = Array.from(bar.querySelectorAll('.tc-tab')) as HTMLElement[]
    for (let i = 0; i < tabs.length; i++) {
      const rect = tabs[i]!.getBoundingClientRect()
      if (clientX < rect.left + rect.width / 2) return i
    }
    return tabs.length
  }, [leaf.tabs.length])

  // ── Tab bar drag handlers (reorder / merge) ──
  const handleTabBarDragOver = useCallback((e: React.DragEvent) => {
    if (!e.dataTransfer.types.includes(TAB_DND_TYPE)) return
    e.preventDefault()
    e.stopPropagation()
    e.dataTransfer.dropEffect = 'move'
    setDropTabIndex(getTabInsertIndex(e.clientX))
    setDropEdge(null)
  }, [getTabInsertIndex])

  const handleTabBarDragLeave = useCallback((e: React.DragEvent) => {
    if (!tabbarRef.current?.contains(e.relatedTarget as Node)) {
      setDropTabIndex(null)
    }
  }, [])

  const handleTabBarDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    const insertIdx = getTabInsertIndex(e.clientX)
    setDropTabIndex(null)

    const raw = e.dataTransfer.getData(TAB_DND_TYPE)
    const parsed = raw ? JSON.parse(raw) as { leafId: string; tabId: string } : _activeDrag
    _activeDrag = null
    if (!parsed) return
    const { leafId: fromLeafId, tabId } = parsed

    if (fromLeafId === leaf.id) {
      const fromIndex = leaf.tabs.findIndex(t => t.id === tabId)
      if (fromIndex < 0) return
      const adjustedIdx = insertIdx > fromIndex ? insertIdx - 1 : insertIdx
      if (adjustedIdx === fromIndex) return
      onMoveTab?.(leaf.id, tabId, leaf.id, adjustedIdx)
    } else {
      onMoveTab?.(fromLeafId, tabId, leaf.id, insertIdx)
    }
  }, [leaf.id, leaf.tabs, getTabInsertIndex, onMoveTab])

  // ── Content area edge detection for splitting ──
  const detectEdge = useCallback((e: React.DragEvent): DropEdge => {
    const rect = containerRef.current?.getBoundingClientRect()
    if (!rect) return null
    const x = e.clientX - rect.left
    const y = e.clientY - rect.top
    const w = rect.width
    const h = rect.height
    const contentH = h - TAB_BAR_HEIGHT
    const margin = Math.min(w, contentH) * 0.25
    const cy = y - TAB_BAR_HEIGHT

    if (x < margin) return 'left'
    if (x > w - margin) return 'right'
    if (cy < margin) return 'top'
    if (cy > contentH - margin) return 'bottom'
    return 'center'
  }, [])

  // ── Drag-capture overlay handlers (splitting / moving to center) ──
  const handleOverlayDragOver = useCallback((e: React.DragEvent) => {
    if (!e.dataTransfer.types.includes(TAB_DND_TYPE)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
    setDropEdge(detectEdge(e))
    setDropTabIndex(null)
  }, [detectEdge])

  const handleOverlayDragLeave = useCallback((e: React.DragEvent) => {
    if (!containerRef.current?.contains(e.relatedTarget as Node)) {
      setDropEdge(null)
    }
  }, [])

  const handleOverlayDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    setDropEdge(null)
    const raw = e.dataTransfer.getData(TAB_DND_TYPE)
    const parsed = raw ? JSON.parse(raw) as { leafId: string; tabId: string } : _activeDrag
    _activeDrag = null
    if (!parsed) return
    const { leafId: fromLeafId, tabId } = parsed
    const edge = detectEdge(e)

    if (edge === 'center' || edge === null) {
      if (fromLeafId === leaf.id) return
      onMoveTab?.(fromLeafId, tabId, leaf.id)
    } else {
      onMoveTabToEdge?.(fromLeafId, tabId, leaf.id, edge)
    }
  }, [leaf.id, detectEdge, onMoveTab, onMoveTabToEdge])

  return (
    <div
      ref={containerRef}
      className="tc-container"
    >
      {/* Tab bar handles reorder/merge drops */}
      <div className="tc-tabbar-wrapper">
        <div
          className="tc-tabbar"
          ref={tabbarRef}
          onDragOver={handleTabBarDragOver}
          onDragLeave={handleTabBarDragLeave}
          onDrop={handleTabBarDrop}
        >
          {leaf.tabs.map((tab, i) => (
            <React.Fragment key={tab.id}>
              {dropTabIndex === i && <div className="tc-insert-indicator" />}
              <div
                className={`tc-tab ${i === leaf.activeTabIndex ? 'active' : ''}`}
                draggable
                onDragStart={(e) => handleTabDragStart(e, tab)}
                onClick={() => onSetActiveTab?.(leaf.id, i)}
                onContextMenu={(e) => handleTabContextMenu(e, i)}
              >
                {getTabIcon && (
                  <span className="tc-tab-icon">{getTabIcon(tab)}</span>
                )}
                <span className="tc-tab-label">{tab.label}</span>
                {badgeProvider.getBadge(tab) === 'dirty' && (
                  <span className="tc-tab-dirty" />
                )}
                {tab.closable && (
                  <button
                    className="tc-tab-close"
                    onClick={(e) => {
                      e.stopPropagation()
                      onCloseTab?.(leaf.id, tab.id)
                    }}
                  >
                    <X size={10} />
                  </button>
                )}
              </div>
            </React.Fragment>
          ))}
          {dropTabIndex === leaf.tabs.length && <div className="tc-insert-indicator" />}
        </div>

        {hiddenIndices.length > 0 && (
          <>
            <button
              className="tc-overflow-btn"
              ref={overflowBtnRef}
              type="button"
              onClick={(e) => {
                e.stopPropagation()
                setContextMenu(null)
                setOverflowOpen((prev) => !prev)
              }}
            >
              <MoreVertical size={14} />
            </button>
            {overflowOpen && (
              <div
                className="tc-overflow-menu"
                style={{
                  left: overflowBtnRef.current?.getBoundingClientRect().left ?? 0,
                  top: (overflowBtnRef.current?.getBoundingClientRect().bottom ?? 0) + 4,
                }}
                onClick={(e) => e.stopPropagation()}
              >
                {hiddenIndices.map((i) => {
                  const tab = leaf.tabs[i]
                  if (!tab) return null
                  return (
                    <div
                      key={tab.id}
                      className="tc-overflow-item"
                      onClick={() => {
                        onSetActiveTab?.(leaf.id, i)
                        setOverflowOpen(false)
                      }}
                    >
                      {getTabIcon && (
                        <span className="tc-overflow-item-icon">{getTabIcon(tab)}</span>
                      )}
                      <span className="tc-overflow-item-label">{tab.label}</span>
                      {badgeProvider.getBadge(tab) === 'dirty' && (
                        <span className="tc-tab-dirty" />
                      )}
                    </div>
                  )
                })}
              </div>
            )}
          </>
        )}
      </div>

      {/* Content area */}
      <div className="tc-content">
        {activeTab ? renderTabContent(activeTab) : (
          <div className="tc-empty">No tab selected</div>
        )}
        {renderViewToolbar && activeTab && renderViewToolbar(activeTab.viewType)}

        {/* Drag-capture overlay: transparent, activates on any tab drag.
            Sits above all content (Monaco, DAG canvas, etc.) so they cannot
            swallow drag events. All pane types are handled uniformly here. */}
        {isDragActive && (
          <div
            className="tc-drag-capture"
            onDragOver={handleOverlayDragOver}
            onDragLeave={handleOverlayDragLeave}
            onDrop={handleOverlayDrop}
          />
        )}
      </div>

      {/* Split drop zone overlays */}
      {dropEdge && dropEdge !== 'center' && (
        <div className={`tc-drop-zone ${dropEdge}`} />
      )}
      {dropEdge === 'center' && (
        <div className="tc-drop-zone center" />
      )}

      {/* Context menu */}
      {contextMenu && (
        <div
          className="tc-context-menu"
          style={{ left: contextMenu.x, top: contextMenu.y }}
          onClick={(e) => e.stopPropagation()}
        >
          <div className="tc-menu-item" onClick={() => handleCloseTab(contextMenu.tabIndex)}>
            {labels.close}
          </div>
          <div className="tc-menu-item" onClick={() => handleCloseOthers(contextMenu.tabIndex)}>
            {labels.closeOthers}
          </div>
          <div className="tc-menu-item" onClick={() => handleCloseRight(contextMenu.tabIndex)}>
            {labels.closeRight}
          </div>
        </div>
      )}
    </div>
  )
}
