import React, { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { wailsWindowController } from '../../../application/window-controller'
import { ChevronDown, ChevronLeft, ChevronRight, Circle, Hexagon, Bell, BellOff, PanelBottom, PanelBottomClose, PanelLeft, PanelRight, PanelRightClose, Pin, Search } from 'lucide-react'
import type { ContentMode } from './AIShellSidebar'
import { GuideIds } from '../guide-ids'
import { AIShellModeSwitch } from './AIShellModeSwitch'
import { AIShellPreviewControls } from './AIShellPreviewControls'
import { AIShellWindowControls } from './AIShellWindowControls'
import { AppUpdateButton } from './AppUpdateButton'
import { AppUpdateDialog } from './AppUpdateDialog'
import { useAppUpdate } from './useAppUpdate'
import type { AIPreviewScenario } from '../model/ai-preview-envelopes'
import { useBrowserOverlay } from '../browserOverlay'
import { useI18n } from '../../../i18n'
import { getMultiConsoleGridConfig, saveMultiConsoleGridConfig } from '../../../application/workspace-ui-state'
import { useResolvedContentModes } from '../content-modes'
import { useDynamicTutorials } from '../../../application/tutorial-registry'
import { SidebarModeSwitch, type LauncherMode } from './SidebarModeSwitch'
import {
  ensureCompanionVisibleLoaded,
  getCompanionVisible,
  setCompanionVisible,
  subscribeCompanionVisible,
} from '../../../application/companion-visibility'
import './AIShellTopbar.css'
import './AppUpdateButton.css'

function ModeOptionItem({
  icon,
  label,
  active,
  onClick,
  pinned,
  pinnable,
  onTogglePin,
  guideId,
}: {
  icon: React.ReactNode
  label: string
  active?: boolean
  onClick?: () => void
  pinned?: boolean
  pinnable?: boolean
  onTogglePin?: () => void
  guideId?: string
}) {
  const { t } = useI18n()
  return (
    <div className={`ai-shell-mc-dropdown-item-row${active ? ' active' : ''}`}>
      <button
        type="button"
        className="ai-shell-mc-dropdown-item"
        data-guide-id={guideId}
        onClick={onClick}
      >
        <span className="ai-shell-mc-dropdown-item-icon">{icon}</span>
        <span className="ai-shell-mc-dropdown-item-label">{label}</span>
      </button>
      {pinnable && onTogglePin && (
        <button
          type="button"
          className={`ai-shell-mc-dropdown-pin${pinned ? ' active' : ''}`}
          onClick={(e) => { e.stopPropagation(); onTogglePin() }}
          title={pinned ? t('shell.sidebar.unpin') : t('shell.sidebar.pin')}
        >
          <Pin size={12} />
        </button>
      )}
    </div>
  )
}

type TopbarMenuId = 'file' | 'edit' | 'view' | 'about'

type TopbarMenuEntry = {
  id: string
  label: string
  shortcut?: string
  separator?: boolean
  disabled?: boolean
  pinnable?: boolean
  pinned?: boolean
  onTogglePin?: () => void
  /** Small trailing badge (e.g. "AI" for agent-created dynamic tutorials). */
  badge?: string
}

type TopbarMenuGroup = {
  id: TopbarMenuId
  label: string
  entries: TopbarMenuEntry[]
}

function TopbarMenuBar({
  menus,
  activeMenu,
  onActivate,
  onClose,
  onAction,
}: {
  menus: TopbarMenuGroup[]
  activeMenu: TopbarMenuId | null
  onActivate: (id: TopbarMenuId | null) => void
  onClose: () => void
  onAction: (menuId: TopbarMenuId, entryId: string) => void
}) {
  const { t } = useI18n()
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!activeMenu) return
    const handleClick = (e: MouseEvent) => {
      if (!containerRef.current?.contains(e.target as Node)) {
        onClose()
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [activeMenu, onClose])

  return (
    <div className="ai-shell-menu-bar" ref={containerRef}>
      {menus.map((menu) => {
        const isOpen = activeMenu === menu.id
        return (
          <div key={menu.id} className="ai-shell-menu-bar-item">
            <button
              type="button"
              className={`ai-shell-menu-bar-trigger${isOpen ? ' active' : ''}`}
              onClick={() => onActivate(isOpen ? null : menu.id)}
              onMouseEnter={() => {
                if (activeMenu && activeMenu !== menu.id) {
                  onActivate(menu.id)
                }
              }}
              aria-expanded={isOpen}
            >
              {menu.label}
            </button>
            {isOpen && (
              <div className="ai-shell-menu-bar-dropdown" role="menu">
                {menu.entries.map((entry, index) =>
                  entry.separator ? (
                    <div key={`sep-${index}`} className="ai-shell-menu-bar-sep" />
                  ) : entry.pinnable && entry.onTogglePin ? (
                    <div key={entry.id} className="ai-shell-mc-dropdown-item-row">
                      <button
                        type="button"
                        className={`ai-shell-menu-bar-dropdown-item${entry.disabled ? ' disabled' : ''}`}
                        disabled={entry.disabled}
                        onClick={() => onAction(menu.id, entry.id)}
                        role="menuitem"
                      >
                        <span className="ai-shell-menu-bar-dropdown-label">{entry.label}</span>
                        {entry.shortcut && (
                          <span className="ai-shell-menu-bar-dropdown-shortcut">{entry.shortcut}</span>
                        )}
                      </button>
                      <button
                        type="button"
                        className={`ai-shell-mc-dropdown-pin${entry.pinned ? ' active' : ''}`}
                        onClick={(e) => { e.stopPropagation(); entry.onTogglePin?.() }}
                        title={entry.pinned ? t('shell.sidebar.unpin') : t('shell.sidebar.pin')}
                      >
                        <Pin size={12} />
                      </button>
                    </div>
                  ) : (
                    <button
                      key={entry.id}
                      type="button"
                      className={`ai-shell-menu-bar-dropdown-item${entry.disabled ? ' disabled' : ''}`}
                      disabled={entry.disabled}
                      onClick={() => onAction(menu.id, entry.id)}
                      role="menuitem"
                    >
                      <span className="ai-shell-menu-bar-dropdown-label">{entry.label}</span>
                      {entry.badge && (
                        <span className="ai-shell-menu-bar-dropdown-badge">{entry.badge}</span>
                      )}
                      {entry.shortcut && (
                        <span className="ai-shell-menu-bar-dropdown-shortcut">{entry.shortcut}</span>
                      )}
                    </button>
                  ),
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

interface AIShellTopbarProps {
  maximised: boolean
  onMaximisedChange: (v: boolean) => void
  sidebarVisible?: boolean
  onToggleSidebar?: () => void
  rightPanelOpen?: boolean
  onToggleRightPanel?: () => void
  terminalPanelOpen?: boolean
  onToggleTerminalPanel?: () => void
  shellVariant?: 'ai' | 'preview'
  isMobile?: boolean
  previewScenarios?: AIPreviewScenario[]
  activePreviewScenarioId?: string
  onPreviewScenarioChange?: (id: string) => void
  isPreviewStreaming?: boolean
  onStartPreview?: () => void
  contentMode?: ContentMode
  onContentModeChange?: (mode: ContentMode, params?: { cardId?: string }) => void
  onShellModeChange?: () => void
  pinnedModes?: string[]
  onTogglePinnedMode?: (mode: string) => void
  onResetPreview?: () => void
  /** Open the omnibox command palette. */
  onOpenOmnibox?: () => void
  /** Whether the navigation history can go back. */
  canGoBack?: boolean
  /** Whether the navigation history can go forward. */
  canGoForward?: boolean
  /** Navigate back in history. */
  onGoBack?: () => void
  /** Navigate forward in history. */
  onGoForward?: () => void
  /** Menu action callbacks keyed by menu id and entry id. */
  onFileMenuAction?: (action: string) => void
  onEditMenuAction?: (action: string) => void
  onViewMenuAction?: (action: string) => void
  onHelpMenuAction?: (action: string) => void
  /** Open the About page. */
  onAboutAction?: () => void
  /** Launcher (home) pane variant; present on desktop only — the switch parks here. */
  launcherMode?: LauncherMode
  onLauncherModeChange?: (next: LauncherMode) => void
}

export const AIShellTopbar: React.FC<AIShellTopbarProps> = ({
  maximised,
  onMaximisedChange,
  sidebarVisible,
  onToggleSidebar,
  rightPanelOpen,
  onToggleRightPanel,
  terminalPanelOpen,
  onToggleTerminalPanel,
  shellVariant = 'ai',
  isMobile = false,
  previewScenarios = [],
  activePreviewScenarioId = '',
  onPreviewScenarioChange,
  isPreviewStreaming = false,
  onStartPreview,
  onResetPreview,
  onContentModeChange,
  onShellModeChange,
  contentMode,
  pinnedModes = [],
  onTogglePinnedMode,
  onOpenOmnibox,
  canGoBack = false,
  canGoForward = false,
  onGoBack,
  onGoForward,
  onFileMenuAction,
  onEditMenuAction,
  onViewMenuAction,
  onHelpMenuAction,
  onAboutAction,
  launcherMode,
  onLauncherModeChange,
}) => {
  const { t } = useI18n()
  const wails = wailsWindowController.isHostMode()
  const mcDropdownRef = useRef<HTMLDivElement>(null)
  const [mcDropdownOpen, setMcDropdownOpen] = useState(false)
  const [showMcGrid, setShowMcGrid] = useState(false)
  const [activeMenu, setActiveMenu] = useState<TopbarMenuId | null>(null)
  const contentModes = useResolvedContentModes()
  // Agent-created dynamic tutorials, live-subscribed from the in-memory
  // projection populated by AIShellLayout at startup + via create/delete
  // interface events.
  const dynamicTutorials = useDynamicTutorials()

  // Auto-update (desktop-only): button + dialog driven by the backend
  // "sporemind:update" event via useAppUpdate.
  const appUpdate = useAppUpdate()
  const [updateDialogOpen, setUpdateDialogOpen] = useState(false)

  // Toast overlay mute toggle: flips whether notification cards may pop
  // over the main UI. State lives in the companion-visibility store — shared
  // with the overlay itself and persisted through workspace preferences, so
  // the toggle survives restarts and both views always agree.
  const companionVisible = useSyncExternalStore(subscribeCompanionVisible, getCompanionVisible)
  useEffect(() => {
    void ensureCompanionVisibleLoaded()
  }, [])
  const handleToggleCompanion = useCallback(() => {
    setCompanionVisible(!companionVisible)
  }, [companionVisible])
  const handleOpenUpdateDialog = useCallback(() => setUpdateDialogOpen(true), [])
  const handleCloseUpdateDialog = useCallback(() => setUpdateDialogOpen(false), [])

  const menuGroups: TopbarMenuGroup[] = useMemo(() => {
    const viewEntries: TopbarMenuEntry[] = [
      { id: 'toggle-sidebar', label: t('shell.topbar.menu.view.toggleSidebar') },
      { id: 'toggle-right-panel', label: t('shell.topbar.menu.view.toggleRightPanel') },
      { id: 'toggle-mc', label: t('shell.topbar.menu.view.toggleMultiConsole') },
      { id: 'sep-view', label: '', separator: true },
      // Content modes in onboarding-aligned order (matches right-side dropdown)
      ...contentModes.map((m) => ({
        id: m.id,
        label: m.label,
        pinnable: m.pinnable,
        pinned: pinnedModes.includes(m.id),
        onTogglePin: onTogglePinnedMode ? () => onTogglePinnedMode(m.id) : undefined,
      })),
    ]
    return [
      {
        id: 'file',
        label: t('shell.topbar.menu.file'),
        entries: [
          { id: 'new-project', label: t('shell.topbar.menu.file.newProject') },
          { id: 'open-omnibox', label: t('shell.topbar.menu.file.openCommandPalette') },
          { id: 'sep-file', label: '', separator: true },
          { id: 'settings', label: t('shell.topbar.menu.file.settings') },
        ],
      },
      {
        id: 'edit',
        label: t('shell.topbar.menu.edit'),
        entries: [
          { id: 'cut', label: t('shell.topbar.menu.edit.cut') },
          { id: 'copy', label: t('shell.topbar.menu.edit.copy') },
          { id: 'paste', label: t('shell.topbar.menu.edit.paste') },
          { id: 'select-all', label: t('shell.topbar.menu.edit.selectAll') },
        ],
      },
      {
        id: 'view',
        label: t('shell.topbar.menu.view'),
        entries: viewEntries,
      },
      {
        id: 'about',
        label: t('shell.topbar.menu.about'),
        entries: [
          { id: 'tutorial-basics', label: t('onboarding.tutorial.category.basics.label') },
          { id: 'tutorial-workflow', label: t('onboarding.tutorial.category.workflow.label') },
          { id: 'tutorial-tools', label: t('onboarding.tutorial.category.tools.label') },
          { id: 'tutorial-settings', label: t('onboarding.tutorial.category.settings.label') },
          // Agent-created dynamic tutorials — id encodes the store lookup key
          // handled by AIShellLayout's onHelpMenuAction.
          ...dynamicTutorials.map((spec) => ({
            id: `tutorial-dynamic:${spec.TutorialId}`,
            label: spec.Title,
            badge: 'AI',
          })),
          { id: 'sep-help', label: '', separator: true },
          { id: 'tool-guide', label: t('shell.topbar.menu.help.toolGuide') },
          { id: 'sep-about', label: '', separator: true },
          { id: 'about-open', label: t('shell.topbar.menu.about.software') },
        ],
      },
    ]
  }, [contentModes, t, pinnedModes, onTogglePinnedMode, dynamicTutorials])

  const handleMenuAction = useCallback((menuId: TopbarMenuId, entryId: string) => {
    setActiveMenu(null)
    if (menuId === 'file' && onFileMenuAction) {
      onFileMenuAction(entryId)
      return
    }
    if (menuId === 'edit' && onEditMenuAction) {
      onEditMenuAction(entryId)
      return
    }
    if (menuId === 'view' && onViewMenuAction) {
      onViewMenuAction(entryId)
      return
    }
    if (menuId === 'about') {
      if (entryId === 'about-open') {
        onAboutAction?.()
      } else {
        onHelpMenuAction?.(entryId)
      }
    }
  }, [onFileMenuAction, onEditMenuAction, onViewMenuAction, onHelpMenuAction, onAboutAction])

  const mobileViewOptionsRef = useRef<HTMLDivElement>(null)
  const [mobileViewOptionsOpen, setMobileViewOptionsOpen] = useState(false)

  const anyDropdownOpen = mcDropdownOpen || mobileViewOptionsOpen || activeMenu !== null
  useBrowserOverlay(anyDropdownOpen)

  const closeMcDropdown = () => {
    setMcDropdownOpen(false)
    setShowMcGrid(false)
  }

  const closeMobileViewOptions = () => {
    setMobileViewOptionsOpen(false)
    setShowMcGrid(false)
  }

  const [mcCols, setMcCols] = useState(2)
  const [mcRows, setMcRows] = useState(2)

  useEffect(() => {
    let cancelled = false
    void getMultiConsoleGridConfig().then(config => {
      if (cancelled) return
      setMcCols(config.cols)
      setMcRows(config.rows)
    })
    return () => {
      cancelled = true
    }
  }, [])

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

  useEffect(() => {
    if (!anyDropdownOpen) {
      setShowMcGrid(false)
      return
    }
    if (contentMode === 'multiconsole') {
      setShowMcGrid(true)
    }
    const handleClick = (e: MouseEvent) => {
      const target = e.target as Node
      const insideMc = mcDropdownRef.current?.contains(target) ?? false
      const insideMobile = mobileViewOptionsRef.current?.contains(target) ?? false
      if (!insideMc && !insideMobile) {
        closeMcDropdown()
        closeMobileViewOptions()
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [anyDropdownOpen, contentMode])

  return (
    <div className={`ai-shell-topbar${sidebarVisible === false ? ' sidebar-collapsed' : ''}`}>
      <div className="ai-shell-topbar-left">
        {isMobile ? (
          <AIShellModeSwitch onToggleSidebar={onToggleSidebar} />
        ) : (
          <>
            <button
              className="ai-shell-sidebar-toggle"
              data-guide-id={GuideIds.topbar_sidebar_toggle}
              type="button"
              onClick={onToggleSidebar}
              title={sidebarVisible ? t('shell.topbar.sidebar.close') : t('shell.topbar.sidebar.open')}
              aria-label={sidebarVisible ? t('shell.topbar.sidebar.close') : t('shell.topbar.sidebar.open')}
            >
              {sidebarVisible ? <PanelLeft size={14} /> : <Circle size={14} />}
            </button>
            {launcherMode !== undefined && onLauncherModeChange && (
              <SidebarModeSwitch value={launcherMode} onChange={onLauncherModeChange} />
            )}
            <button
              className={`ai-shell-history-btn ${canGoBack ? '' : 'disabled'}`}
              type="button"
              onClick={onGoBack}
              disabled={!canGoBack}
              title={t('shell.topbar.nav.back')}
              aria-label={t('shell.topbar.nav.back')}
            >
              <ChevronLeft size={14} />
            </button>
            <button
              className={`ai-shell-history-btn ${canGoForward ? '' : 'disabled'}`}
              type="button"
              onClick={onGoForward}
              disabled={!canGoForward}
              title={t('shell.topbar.nav.forward')}
              aria-label={t('shell.topbar.nav.forward')}
            >
              <ChevronRight size={14} />
            </button>
            <TopbarMenuBar
              menus={menuGroups}
              activeMenu={activeMenu}
              onActivate={setActiveMenu}
              onClose={() => setActiveMenu(null)}
              onAction={handleMenuAction}
            />
            {wails && appUpdate.featureEnabled && (
              <AppUpdateButton status={appUpdate.status} onClick={handleOpenUpdateDialog} />
            )}
          </>
        )}
      </div>
      <div className="ai-shell-topbar-center">
        {isMobile ? (
          <div className="ai-shell-topbar-center-mobile">
            {onOpenOmnibox && (
              <button
                className="ai-shell-omnibox-trigger-mobile"
                data-guide-id="topbar.omnibox-trigger"
                type="button"
                onClick={onOpenOmnibox}
                aria-label={t('omnibox.searchPlaceholder')}
              >
                <span className="ai-shell-omnibox-trigger-mobile-label">{t('omnibox.searchPlaceholder')}</span>
                <Search size={14} />
              </button>
            )}
            <div className="ai-shell-mc-wrapper mobile" data-guide-id={GuideIds.mode_cluster} ref={mobileViewOptionsRef}>
              <button
                className={`ai-shell-mc-btn ${contentMode === 'multiconsole' ? 'active' : ''}`}
                type="button"
                onClick={() => setMobileViewOptionsOpen(prev => !prev)}
                title={t('shell.topbar.viewOptions')}
                aria-label={t('shell.topbar.viewOptions')}
              >
                <ChevronDown size={14} />
              </button>
              {mobileViewOptionsOpen && (
                <div className="ai-shell-mc-dropdown">
                  {contentModes.map(m => (
                    <ModeOptionItem
                      key={m.id}
                      icon={<m.Icon size={14} />}
                      label={m.label}
                      active={contentMode === m.id}
                      pinnable={m.pinnable}
                      pinned={pinnedModes.includes(m.id)}
                      onTogglePin={onTogglePinnedMode ? () => onTogglePinnedMode(m.id) : undefined}
                      guideId={m.id === 'workflow' ? GuideIds.mode_workflow : undefined}
                      onClick={() => {
                        onContentModeChange?.(m.id)
                        closeMobileViewOptions()
                      }}
                    />
                  ))}
                  {onShellModeChange ? (
                    <ModeOptionItem
                      icon={<Hexagon size={14} />}
                      label={t('workbench.options.workbench')}
                      onClick={() => {
                        onShellModeChange()
                        closeMobileViewOptions()
                      }}
                    />
                  ) : null}
                  {showMcGrid ? (
                    <>
                      <div className="ai-shell-mc-sep" />
                      <div className="ai-shell-mc-selector">
                        <div className="ai-shell-mc-slider-row">
                          <span className="ai-shell-mc-slider-label">{t('shell.topbar.multiConsole.width')}</span>
                          <div className="ai-shell-mc-slider-track">
                            {[1,2,3,4,5].map(v => (
                              <button
                                key={v}
                                className={`ai-shell-mc-slider-dot ${mcCols >= v ? 'active' : ''}`}
                                type="button"
                                onClick={() => {
                                  setMcCols(v)
                                  const config = { cols: v, rows: mcRows }
                                  void saveMultiConsoleGridConfig(config)
                                  window.dispatchEvent(new CustomEvent('sporemind:multiconsole-grid', { detail: config }))
                                }}
                                title={`${v}`}
                              />
                            ))}
                          </div>
                          <span className="ai-shell-mc-slider-value">{mcCols}</span>
                        </div>
                        <div className="ai-shell-mc-slider-row">
                          <span className="ai-shell-mc-slider-label">{t('shell.topbar.multiConsole.height')}</span>
                          <div className="ai-shell-mc-slider-track">
                            {[1,2,3,4,5].map(v => (
                              <button
                                key={v}
                                className={`ai-shell-mc-slider-dot ${mcRows >= v ? 'active' : ''}`}
                                type="button"
                                onClick={() => {
                                  setMcRows(v)
                                  const config = { cols: mcCols, rows: v }
                                  void saveMultiConsoleGridConfig(config)
                                  window.dispatchEvent(new CustomEvent('sporemind:multiconsole-grid', { detail: config }))
                                }}
                                title={`${v}`}
                              />
                            ))}
                          </div>
                          <span className="ai-shell-mc-slider-value">{mcRows}</span>
                        </div>
                      </div>
                    </>
                  ) : null}
                </div>
              )}
            </div>
            {onToggleRightPanel && (
              <button
                className={`ai-shell-mobile-panel-toggle ${rightPanelOpen ? 'active' : ''}`}
                type="button"
                onClick={onToggleRightPanel}
                title={rightPanelOpen ? t('shell.topbar.rightPanel.close') : t('shell.topbar.rightPanel.open')}
                aria-label={rightPanelOpen ? t('shell.topbar.rightPanel.close') : t('shell.topbar.rightPanel.open')}
              >
                <PanelRight size={16} />
              </button>
            )}
            {onToggleTerminalPanel && (
              <button
                className={`ai-shell-mobile-panel-toggle ${terminalPanelOpen ? 'active' : ''}`}
                type="button"
                onClick={onToggleTerminalPanel}
                title={terminalPanelOpen ? t('terminalPanel.close') : t('terminalPanel.open')}
                aria-label={terminalPanelOpen ? t('terminalPanel.close') : t('terminalPanel.open')}
              >
                {terminalPanelOpen ? <PanelBottomClose size={16} /> : <PanelBottom size={16} />}
              </button>
            )}
          </div>
        ) : (
          <div className="ai-shell-mode-cluster" />
        )}
      </div>
      <div className="ai-shell-topbar-right">
        {shellVariant === 'preview' && (
          <AIShellPreviewControls
            scenarios={previewScenarios}
            activeScenarioId={activePreviewScenarioId}
            onScenarioChange={onPreviewScenarioChange ?? (() => {})}
            isPreviewStreaming={isPreviewStreaming}
            onStartPreview={onStartPreview}
            onResetPreview={onResetPreview}
          />
        )}
        {!isMobile && (
          <div className="ai-shell-mc-wrapper" data-guide-id={GuideIds.mode_cluster} ref={mcDropdownRef}>
            <button
              className={`ai-shell-mc-btn ${contentMode === 'multiconsole' ? 'active' : ''}`}
              type="button"
              onClick={() => setMcDropdownOpen(prev => !prev)}
              title={t('shell.topbar.viewOptions')}
            >
              <ChevronDown size={14} />
            </button>
            {mcDropdownOpen && (
              <div className="ai-shell-mc-dropdown">
                {contentModes.map(m => (
                  <ModeOptionItem
                    key={m.id}
                    icon={<m.Icon size={14} />}
                    label={m.label}
                    active={contentMode === m.id}
                    pinnable={m.pinnable}
                    pinned={pinnedModes.includes(m.id)}
                    onTogglePin={onTogglePinnedMode ? () => onTogglePinnedMode(m.id) : undefined}
                    guideId={m.id === 'workflow' ? GuideIds.mode_workflow : undefined}
                    onClick={() => {
                      onContentModeChange?.(m.id)
                      closeMcDropdown()
                    }}
                  />
                ))}
                {onShellModeChange ? (
                  <ModeOptionItem
                    icon={<Hexagon size={14} />}
                    label={t('workbench.options.workbench')}
                    onClick={() => {
                      onShellModeChange()
                      closeMcDropdown()
                    }}
                  />
                ) : null}
                {showMcGrid ? (
                  <>
                    <div className="ai-shell-mc-sep" />
                    <div className="ai-shell-mc-selector">
                      <div className="ai-shell-mc-slider-row">
                        <span className="ai-shell-mc-slider-label">{t('shell.topbar.multiConsole.width')}</span>
                        <div className="ai-shell-mc-slider-track">
                          {[1,2,3,4,5].map(v => (
                            <button
                              key={v}
                              className={`ai-shell-mc-slider-dot ${mcCols >= v ? 'active' : ''}`}
                              type="button"
                              onClick={() => {
                                setMcCols(v)
                                const config = { cols: v, rows: mcRows }
                                void saveMultiConsoleGridConfig(config)
                                window.dispatchEvent(new CustomEvent('sporemind:multiconsole-grid', { detail: config }))
                              }}
                              title={`${v}`}
                            />
                          ))}
                        </div>
                        <span className="ai-shell-mc-slider-value">{mcCols}</span>
                      </div>
                      <div className="ai-shell-mc-slider-row">
                        <span className="ai-shell-mc-slider-label">{t('shell.topbar.multiConsole.height')}</span>
                        <div className="ai-shell-mc-slider-track">
                          {[1,2,3,4,5].map(v => (
                            <button
                              key={v}
                              className={`ai-shell-mc-slider-dot ${mcRows >= v ? 'active' : ''}`}
                              type="button"
                              onClick={() => {
                                setMcRows(v)
                                const config = { cols: mcCols, rows: v }
                                void saveMultiConsoleGridConfig(config)
                                window.dispatchEvent(new CustomEvent('sporemind:multiconsole-grid', { detail: config }))
                              }}
                              title={`${v}`}
                            />
                          ))}
                        </div>
                        <span className="ai-shell-mc-slider-value">{mcRows}</span>
                      </div>
                    </div>
                  </>
                ) : null}
              </div>
            )}
          </div>
        )}
        {!isMobile && onToggleTerminalPanel && (
          <button
            className={`ai-shell-terminal-toggle ${terminalPanelOpen ? 'active' : ''}`}
            type="button"
            onClick={onToggleTerminalPanel}
            title={terminalPanelOpen ? t('terminalPanel.close') : t('terminalPanel.open')}
            aria-label={terminalPanelOpen ? t('terminalPanel.close') : t('terminalPanel.open')}
          >
            {terminalPanelOpen ? <PanelBottomClose size={14} /> : <PanelBottom size={14} />}
          </button>
        )}
        {!isMobile && (
          <button
            className={`ai-shell-right-panel-toggle ${rightPanelOpen ? 'active' : ''}`}
            type="button"
            onClick={onToggleRightPanel}
            title={rightPanelOpen ? t('shell.topbar.rightPanel.close') : t('shell.topbar.rightPanel.open')}
            aria-label={rightPanelOpen ? t('shell.topbar.rightPanel.close') : t('shell.topbar.rightPanel.open')}
          >
            {rightPanelOpen ? <PanelRightClose size={14} /> : <PanelRight size={14} />}
          </button>
        )}
        {!isMobile && (
          <button
            className={`ai-shell-right-panel-toggle ${companionVisible ? 'active' : ''}`}
            type="button"
            onClick={handleToggleCompanion}
            title={companionVisible ? t('shell.topbar.companion.hide') : t('shell.topbar.companion.show')}
            aria-label={companionVisible ? t('shell.topbar.companion.hide') : t('shell.topbar.companion.show')}
            aria-pressed={companionVisible ?? undefined}
          >
            {companionVisible ? <Bell size={14} /> : <BellOff size={14} />}
          </button>
        )}
        {wails && <AIShellWindowControls maximised={maximised} onMaximisedChange={onMaximisedChange} />}
      </div>
      {wails && appUpdate.featureEnabled && (
        <AppUpdateDialog
          open={updateDialogOpen}
          status={appUpdate.status}
          onClose={handleCloseUpdateDialog}
          onCheck={() => void appUpdate.check()}
          onDownload={() => void appUpdate.download()}
          onInstall={() => void appUpdate.install()}
          onReveal={() => void appUpdate.reveal()}
          onDismiss={() => { void appUpdate.dismiss(); handleCloseUpdateDialog() }}
        />
      )}
    </div>
  )
}
