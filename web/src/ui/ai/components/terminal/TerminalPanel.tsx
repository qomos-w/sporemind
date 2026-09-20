import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Plus, X, ChevronDown } from 'lucide-react'
import { useI18n } from '../../../../i18n'
import { ResizeHandle } from '../../../components/ResizeHandle'
import { useBrowserOverlay } from '../../browserOverlay'
import { terminalTabRegistry, terminalTabTypeOrder } from './tabRegistry'
import './TerminalPanel.css'

/**
 * IDEA-style terminal panel.
 *
 * Desktop: renders inline below the main card inside `.ai-shell-main-col`;
 * expands from the bottom, top edge is vertically draggable (ResizeHandle,
 * min height). Mobile: renders a fixed bottom-sheet overlay that slides up
 * (mirrors RightPanelTabs mobile interaction) and is mutually exclusive with
 * the right panel (enforced by the parent, not here).
 *
 * The panel consumes ONLY the terminal tab type registry: tab bar labels,
 * icons, "+" dropdown order and tab bodies all resolve through
 * `terminalTabRegistry`. Unknown tab types (e.g. a registry entry removed
 * after a persisted tab) fall back to the empty-state guide.
 */

export interface TerminalTab {
  id: string
  type: string
  title?: string
}

export interface TerminalPanelProps {
  tabs: TerminalTab[]
  activeTabId: string | null
  /** Whether the panel is expanded (desktop inline / mobile sheet). */
  open: boolean
  /** Desktop panel height in px. Ignored on mobile (fixed 85% sheet). */
  height: number
  isMobile: boolean
  /** Collapse/expand the whole panel (tab bar collapse button / sheet close). */
  onToggle: () => void
  onSelectTab: (id: string) => void
  onCloseTab: (id: string) => void
  /** Create a new tab of the given registry type. */
  onAddTab: (type: string) => void
  /** Desktop drag-resize final height (px). */
  onResize: (height: number) => void
  /**
   * Update a tab's title (used by real tab bodies, e.g. the shell console
   * appends an "exited" suffix). Optional so simple hosts can ignore it.
   */
  onSetTabTitle?: (tabId: string, title: string | undefined) => void
  /** Open a file at a line in the right-panel file viewer (e.g. log caller jump). */
  onOpenFile?: (filePath: string, line?: number) => void
  /** Project root path passed to shell tabs for the initial working directory. */
  projectRoot?: string
}

export function TerminalPanel({
  tabs,
  activeTabId,
  open,
  height,
  isMobile,
  onToggle,
  onSelectTab,
  onCloseTab,
  onAddTab,
  onResize,
  onSetTabTitle,
  onOpenFile,
  projectRoot,
}: TerminalPanelProps) {
  const { t } = useI18n()
  const [addMenuOpen, setAddMenuOpen] = useState(false)
  const addMenuRef = useRef<HTMLDivElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)

  useBrowserOverlay(addMenuOpen)

  // Close the "+" dropdown on outside click.
  useEffect(() => {
    if (!addMenuOpen) return
    const handleClick = (e: MouseEvent) => {
      if (addMenuRef.current && !addMenuRef.current.contains(e.target as Node)) {
        setAddMenuOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [addMenuOpen])

  const activeTab = useMemo(
    () => tabs.find((tab) => tab.id === activeTabId) ?? tabs[0] ?? null,
    [tabs, activeTabId],
  )

  const handleAdd = useCallback((type: string) => {
    onAddTab(type)
    setAddMenuOpen(false)
  }, [onAddTab])

  useEffect(() => {
    if (open && tabs.length === 0) onAddTab('shell')
  }, [open, onAddTab, tabs.length])

  const activeDef = activeTab ? terminalTabRegistry[activeTab.type] : undefined

  const tabBar = (
    <div className="terminal-tabbar">
      {/* "+" add button leads the tab strip (IDE convention). Its dropdown
          anchors left since the button now sits at the strip's left edge. */}
      <div className="terminal-add-wrapper" ref={addMenuRef}>
        <button
          className="terminal-add-btn"
          type="button"
          onClick={() => setAddMenuOpen((v) => !v)}
          aria-label={t('terminalPanel.newTab')}
          aria-expanded={addMenuOpen}
        >
          <Plus size={14} />
        </button>
        {addMenuOpen && (
          <div className="terminal-new-tab-menu">
            {terminalTabTypeOrder.map((type) => {
              const def = terminalTabRegistry[type]
              if (!def) return null
              return (
                <button
                  key={type}
                  className="terminal-new-tab-menu-item"
                  type="button"
                  onClick={() => handleAdd(type)}
                >
                  {def.icon && <span className="terminal-new-tab-menu-icon">{def.icon}</span>}
                  <span>{t(def.labelKey)}</span>
                </button>
              )
            })}
          </div>
        )}
      </div>
      <div className="terminal-tabbar-tabs" role="tablist">
        {tabs.length === 0 && (
          <span className="terminal-tabbar-empty-hint">{t('terminalPanel.empty.title')}</span>
        )}
        {tabs.map((tab) => {
          const def = terminalTabRegistry[tab.type]
          const isActive = tab.id === activeTabId
          const label = tab.title || (def ? t(def.labelKey) : tab.type)
          return (
            <div
              key={tab.id}
              role="tab"
              aria-selected={isActive}
              className={`terminal-tab${isActive ? ' active' : ''}`}
              onClick={() => onSelectTab(tab.id)}
            >
              {def && <span className="terminal-tab-icon">{def.icon}</span>}
              <span className="terminal-tab-label">{label}</span>
              <button
                className="terminal-tab-close"
                type="button"
                onClick={(e) => {
                  e.stopPropagation()
                  onCloseTab(tab.id)
                }}
                aria-label={t('terminalPanel.closeTab')}
              >
                <X size={12} />
              </button>
            </div>
          )
        })}
      </div>
      <div className="terminal-tabbar-actions">
        <button
          className="terminal-collapse-btn"
          type="button"
          onClick={onToggle}
          aria-label={t('terminalPanel.collapse')}
          title={t('terminalPanel.collapse')}
        >
          <ChevronDown size={14} />
        </button>
      </div>
    </div>
  )

  const body = (
    <div className="terminal-body">
      {tabs.length === 0 ? (
        <div className="terminal-empty">
          <div className="terminal-empty-title">{t('terminalPanel.empty.title')}</div>
          <div className="terminal-empty-hint">{t('terminalPanel.empty.hint')}</div>
        </div>
      ) : (
        <>
          {tabs.map((tab) => {
            const def = terminalTabRegistry[tab.type]
            if (!def) return null
            const isActive = tab.id === activeTab?.id
            const TabBody = def.component
            return (
              <div key={tab.id} className={`terminal-tab-pane${isActive ? ' active' : ' hidden'}`}>
                <TabBody tab={tab} onSetTabTitle={onSetTabTitle} onOpenFile={onOpenFile} projectRoot={projectRoot} />
              </div>
            )
          })}
          {activeTab && !activeDef && (
            <div className="terminal-tab-pane active">
              <div className="terminal-empty">
                <div className="terminal-empty-title">{t('terminalPanel.empty.title')}</div>
                <div className="terminal-empty-hint">{t('terminalPanel.empty.hint')}</div>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )

  if (isMobile) {
    return (
      <div
        className={`terminal-mobile-backdrop${open ? '' : ' terminal-mobile-collapsed'}`}
        onClick={open ? onToggle : undefined}
      >
        <div className="terminal-mobile-container" onClick={(e) => e.stopPropagation()}>
          <div className="terminal-mobile-header">
            {tabBar}
            <button
              className="terminal-mobile-close"
              type="button"
              onClick={onToggle}
              aria-label={t('terminalPanel.close')}
            >
              <X size={18} />
            </button>
          </div>
          <div className="terminal-mobile-body">{body}</div>
        </div>
      </div>
    )
  }

  return (
    <div
      className={`terminal-panel${open ? '' : ' collapsed'}`}
      ref={panelRef}
      style={open ? { height: `${height}px` } : undefined}
    >
      <ResizeHandle
        direction="vertical"
        className="terminal-resize-handle"
        targetRef={panelRef}
        onResize={onResize}
        minSize={120}
        negateDelta
      />
      {tabBar}
      {body}
    </div>
  )
}
