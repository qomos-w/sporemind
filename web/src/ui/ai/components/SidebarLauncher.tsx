import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Download, FileSearch, Globe, Grid2x2, Layers, List, MonitorSmartphone, Settings, Smartphone } from 'lucide-react'
import type { AppEntry } from '../../../application/app-registry'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n/types'
import type {
  AccountSnapshot,
} from '../../../gen-types/workspace'
import { useBrowserOverlay } from '../browserOverlay'
import { GuideIds } from '../guide-ids'
import { SidebarModeSwitch, type LauncherMode } from './SidebarModeSwitch'
import { SidebarAccountMenu } from './AIShellSidebar'
import { resolveAppIcon } from './appIconResolver'
import './SidebarLauncher.css'

export type LauncherBuiltin = 'review' | 'glass' | 'puppet' | 'install-local' | 'browser'

/**
 * Discriminated launch intent of a launcher tile. The shell injects the actual
 * handler wiring (built-in right-panel tabs vs. plugin views) at render time,
 * keeping the data construction pure and unit-testable.
 */
export type LauncherAppAction =
  | { kind: 'builtin'; builtin: LauncherBuiltin }
  | { kind: 'plugin'; pluginID: string; entryID: string; title: string; route: string }

export interface LauncherApp {
  id: string
  label: string
  icon: React.ReactNode
  description?: string
  action: LauncherAppAction
  /** Plugin transport/trust badge; undefined for built-ins and spore apps. */
  badge?: { transport: 'subprocess' | 'inprocess'; thirdParty: boolean }
  /** Tile rendered non-interactive (e.g. update ready, restart pending). */
  disabled?: boolean
  /** Show the small "restart pending" pill on the tile. */
  restartPending?: boolean
  /** Show the red "crashed" badge; tile stays clickable to open the error panel. */
  crashed?: boolean
  /** Show the "failed to load" badge; same clickable error-panel behavior as crashed. */
  failed?: boolean
  /** Verbatim failure cause shown in the crash/failed badge tooltip. */
  failureCause?: string
}

type Translator = (key: I18nKey, params?: Record<string, string | number>) => string

/**
 * Build the launcher tile list: built-in apps (review / glass / puppet, plus
 * browser in wails) followed by one tile per `view` entrypoint of every
 * running registered app. install-local is NOT a tile — it lives on the
 * launcher toolbar.
 */
export function buildLauncherApps(registeredApps: readonly AppEntry[], t: Translator, isWails: boolean, showDevTools = true): LauncherApp[] {
  const apps: LauncherApp[] = [
    { id: 'review', label: t('launcher.codeReview'), icon: <FileSearch size={20} />, action: { kind: 'builtin', builtin: 'review' } },
  ]
  // Glass debug panel and the inochi2d puppet editor are internal tooling:
  // only surfaced on non-production (dev/build) shells.
  if (showDevTools) {
    apps.push(
      { id: 'glass', label: t('launcher.glass'), icon: <MonitorSmartphone size={20} />, action: { kind: 'builtin', builtin: 'glass' } },
      { id: 'puppet', label: t('launcher.puppetEditor'), icon: <Layers size={20} />, action: { kind: 'builtin', builtin: 'puppet' } },
    )
  }
  if (isWails) {
    apps.unshift({ id: 'browser', label: t('launcher.browser'), icon: <Globe size={20} />, action: { kind: 'builtin', builtin: 'browser' } })
  }
  // Registered apps surface directly as tiles: one tile per view entrypoint
  // of every running app (native or spore). Three non-running states stay
  // visible: restart_pending — the update is already staged and the tile is
  // shown disabled with a badge until the host restart activates it —
  // crashed — the plugin process died, so the tile is greyed out with a red
  // badge but stays clickable (the opened panel shows the error state with a
  // restart action) — and failed — the artifact could not be restored (e.g.
  // the recorded artifact path vanished between host restarts), rendered
  // like crashed so the app does not silently disappear from the launcher.
  // Stopped/unloaded apps are managed from Settings → Plugins and stay
  // hidden here.
  for (const app of registeredApps) {
    if (app.state !== 'running' && app.state !== 'restart_pending' && app.state !== 'crashed' && app.state !== 'failed') continue
    const updatePending = app.state === 'restart_pending'
    const crashed = app.state === 'crashed'
    const failed = app.state === 'failed'
    for (const entry of app.entrypoints) {
      if (entry.kind !== 'view') continue
      apps.push({
        id: `${app.id}:${entry.id}`,
        label: entry.title || app.id,
        icon: resolveAppIcon(app.icon, app.id, 20, app.color),
        description: app.namespace || app.id,
        action: { kind: 'plugin', pluginID: app.id, entryID: entry.id, title: entry.title, route: entry.route ?? '' },
        badge: app.runtime === 'native'
          ? { transport: app.isolation === 'inprocess' ? 'inprocess' : 'subprocess', thirdParty: app.trustClass === 'third_party' }
          : undefined,
        disabled: updatePending,
        restartPending: updatePending,
        crashed,
        failed,
        failureCause: crashed || failed ? app.error : undefined,
      })
    }
  }
  return apps
}

/**
 * True when at least one registered app currently surfaces a launcher tile.
 * Built-in tiles (review etc.) are excluded — this answers "does the user
 * have installed Spore Apps", which gates the tutorial's app-panel steps.
 */
export function hasLaunchableApps(registeredApps: readonly AppEntry[]): boolean {
  for (const app of registeredApps) {
    if (app.state !== 'running' && app.state !== 'restart_pending' && app.state !== 'crashed' && app.state !== 'failed') continue
    if (app.entrypoints.some(e => e.kind === 'view')) return true
  }
  return false
}

/** The three launcher sections. `all` is always rendered; the other two hide when empty. */
export type LauncherSection = 'favorites' | 'recent' | 'all'

/** Launcher app display: square-cell grid (default) or one-per-row list. */
export type LauncherView = 'grid' | 'list'

export interface SidebarLauncherProps {
  apps: LauncherApp[]
  mode: LauncherMode
  onModeChange: (next: LauncherMode) => void
  onLaunch: (app: LauncherApp) => void
  /**
   * Whether the shell runs in mobile layout; defaults to true. The mode
   * switch header is mobile-only — on desktop it parks in the shell topbar.
   */
  isMobile?: boolean
  /** Signed-in account snapshot; when provided, the login-state footer renders. */
  account?: AccountSnapshot | null
  onOpenSettings?: () => void
  onOpenMobileSync?: () => void
  /** App display variant shown by the toolbar toggle; defaults to 'grid'. */
  view?: LauncherView
  onViewChange?: (next: LauncherView) => void
  /** Favorite app ids in display order; ids missing from `apps` are skipped. */
  favorites?: string[]
  /** Recently used app ids in display order. */
  recent?: string[]
  /** Manual ordering of the "all" section; ids not listed fall back to `apps` order. */
  order?: string[]
  /**
   * Reorder inside a section via drag & drop. `fromId` is the dragged tile,
   * `toIndex` is the 0-based insertion position within the section list after
   * `fromId` has been removed (parent consumes it as filter + splice).
   */
  onReorder?: (fromId: string, toIndex: number, section: LauncherSection) => void
  /** Favorite toggle: favorite=true adds, favorite=false removes. Dragging onto the favorites section calls it with true. */
  onToggleFavorite?: (id: string, favorite: boolean) => void
  /** Context-menu "move to top". */
  onMoveToTop?: (id: string) => void
  /** Context-menu "remove from recent" (shown only for tiles in the recent section). */
  onRemoveFromRecent?: (id: string) => void
  /** Context-menu "manage / uninstall". */
  onManageApps?: () => void
  /**
   * Drop a .zip app package onto the panel to start the local install flow.
   * Receives the dropped File; the parent parses it and shows the install
   * preview. Files with other extensions are ignored.
   */
  onInstallZipDrop?: (file: File) => void
  /**
   * Experiment-subscription gate for developer tooling on the toolbar
   * (Install from local). Hidden entirely when false.
   */
  showDeveloperActions?: boolean
}

export interface LauncherDropTile {
  appId: string
  rect: Pick<DOMRect, 'top' | 'bottom' | 'left' | 'right'>
}

/**
 * Compute the insertion index for a dragged tile inside a grid section.
 * Iterates the visible tiles in DOM (row-major) order, skips the dragged tile
 * itself, and counts how many remaining tiles sit before the pointer: above a
 * tile's vertical midline, or in its lower band but left of its horizontal
 * midline. The result is the 0-based index at which `fromId` should be
 * inserted after being removed from the section list.
 */
export function computeLauncherDropIndex(
  clientX: number,
  clientY: number,
  tiles: readonly LauncherDropTile[],
  draggingId: string | null,
): number {
  let index = 0
  for (const tile of tiles) {
    if (tile.appId === draggingId) continue
    const { top, bottom, left, right } = tile.rect
    const midY = top + (bottom - top) / 2
    const midX = left + (right - left) / 2
    const before = clientY < midY || (clientY >= midY && clientY < bottom && clientX < midX)
    if (before) break
    index++
  }
  return index
}

/**
 * Merge a persisted custom order with the current full id list of the "all"
 * (列表) section. Ids present in `order` keep their stored sequence (stale ids
 * that no longer exist are dropped); ids not listed are appended in
 * `defaultIds` order.
 */
export function mergeOrder(order: string[] | undefined, defaultIds: string[]): string[] {
  if (!order || order.length === 0) return defaultIds
  const listed = order.filter(id => defaultIds.includes(id))
  const listedIds = new Set(listed)
  return [...listed, ...defaultIds.filter(id => !listedIds.has(id))]
}

/**
 * Record a launch in the recent list: unshift the id, drop the previous
 * occurrence, and cap the length at `cap` (the 最近 section cap is 6).
 */
export function pushRecent(recent: string[], id: string, cap = 6): string[] {
  return [id, ...recent.filter(r => r !== id)].slice(0, cap)
}

/**
 * Reorder a persisted id list after a drag & drop: remove `fromId`, then
 * insert it at `toIndex` (0-based, clamped). `toIndex` is the insertion
 * position within the list after `fromId` has been removed.
 */
export function reorderIdList(list: string[], fromId: string, toIndex: number): string[] {
  const idx = list.indexOf(fromId)
  if (idx < 0) return list
  const next = [...list]
  next.splice(idx, 1)
  next.splice(Math.max(0, Math.min(toIndex, next.length)), 0, fromId)
  return next
}

interface ContextMenuState {
  appId: string
  section: LauncherSection
  x: number
  y: number
}

const sectionTitleKeys: Record<LauncherSection, I18nKey> = {
  favorites: 'launcher.sections.favorites',
  recent: 'launcher.sections.recent',
  all: 'launcher.sections.all',
}

/**
 * App-mode sidebar home: a desktop launcher rendered as three vertically
 * stacked sections (收藏 / 最近 / 列表) inside a vertically scrolling body,
 * with native HTML5 drag-and-drop reordering and a per-tile context menu.
 * All tiles keep the pawpad visual language (44px icon block, dual shadow,
 * single-line label) defined in SidebarLauncher.css.
 */
export function SidebarLauncher({
  apps,
  mode,
  onModeChange,
  onLaunch,
  isMobile = true,
  view = 'grid',
  onViewChange,
  favorites = [],
  recent = [],
  order = [],
  onReorder,
  onToggleFavorite,
  onMoveToTop,
  onRemoveFromRecent,
  onManageApps,
  onInstallZipDrop,
  showDeveloperActions = false,
  account,
  onOpenSettings,
  onOpenMobileSync,
}: SidebarLauncherProps) {
  const { t } = useI18n()
  const [draggedId, setDraggedId] = useState<string | null>(null)
  const [menu, setMenu] = useState<ContextMenuState | null>(null)
  const [zipDragOver, setZipDragOver] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  // Right-click overlay must register with useBrowserOverlay (hard constraint,
  // same as the AddTabButton in RightPanelTabs).
  useBrowserOverlay(!!menu)

  const byId = useMemo(() => new Map(apps.map(app => [app.id, app])), [apps])
  const favoritesSet = useMemo(() => new Set(favorites), [favorites])
  const recentSet = useMemo(() => new Set(recent), [recent])

  const favoriteList = useMemo(
    () => favorites.map(id => byId.get(id)).filter((app): app is LauncherApp => app !== undefined),
    [favorites, byId],
  )
  const recentList = useMemo(
    () => recent.map(id => byId.get(id)).filter((app): app is LauncherApp => app !== undefined),
    [recent, byId],
  )
  const allList = useMemo(() => {
    const orderedIds = mergeOrder(order, apps.map(app => app.id))
    return orderedIds
      .map(id => byId.get(id))
      .filter((app): app is LauncherApp => app !== undefined)
  }, [order, apps, byId])

  // ── Native HTML5 drag & drop ──
  const handleTileDragStart = useCallback((e: React.DragEvent, appId: string) => {
    e.dataTransfer?.setData('text/plain', appId)
    if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
    setDraggedId(appId)
  }, [])

  const handleTileDragEnd = useCallback(() => {
    setDraggedId(null)
  }, [])

  // ── Zip package drag-and-drop install ──
  // A file drag (OS drag over the webview) has 'Files' in dataTransfer.types;
  // a tile reorder drag only carries the 'text/plain' payload. File names are
  // not readable during dragover, so the .zip check runs on drop only.
  const isFileDrag = (dt: DataTransfer | null): boolean => {
    if (!dt) return false
    const types = Array.from(dt.types ?? [])
    return types.includes('Files')
  }

  const handleZipDragOver = useCallback((e: React.DragEvent) => {
    if (!onInstallZipDrop) return
    if (!isFileDrag(e.dataTransfer)) return
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'
    setZipDragOver(true)
  }, [onInstallZipDrop])

  const handleZipDragLeave = useCallback((e: React.DragEvent) => {
    if (e.currentTarget.contains(e.relatedTarget as Node)) return
    setZipDragOver(false)
  }, [])

  const handleZipDrop = useCallback((e: React.DragEvent) => {
    setZipDragOver(false)
    if (!onInstallZipDrop) return
    const dt = e.dataTransfer
    if (!dt || !Array.from(dt.types ?? []).includes('Files')) return
    const file = Array.from(dt.files ?? []).find(f => /\.zip$/i.test(f.name))
    if (!file) return
    e.preventDefault()
    e.stopPropagation()
    onInstallZipDrop(file)
  }, [onInstallZipDrop])

  const handleGridDragOver = useCallback((e: React.DragEvent) => {
    // Allow the drop: without preventDefault the browser refuses to drop.
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  }, [])

  const handleGridDrop = useCallback(
    (e: React.DragEvent<HTMLDivElement>, section: LauncherSection) => {
      e.preventDefault()
      const dt = e.dataTransfer
      const transferred = dt && typeof dt.getData === 'function' ? dt.getData('text/plain') : ''
      const fromId = transferred || draggedId
      setDraggedId(null)
      if (!fromId) return

      if (section === 'favorites' && !favoritesSet.has(fromId)) {
        // Dropped onto the favorites section: a non-favorite tile gets added
        // (the parent decides the insertion position).
        onToggleFavorite?.(fromId, true)
        return
      }
      if (section === 'recent' && !recentSet.has(fromId)) {
        // The recent section only accepts in-section reordering; ignore others.
        return
      }
      const tiles: LauncherDropTile[] = Array.from(
        e.currentTarget.querySelectorAll<HTMLElement>('.ai-sidebar-launcher-tile'),
      ).map(el => ({ appId: el.dataset.appId ?? '', rect: el.getBoundingClientRect() }))
      const index = computeLauncherDropIndex(e.clientX, e.clientY, tiles, fromId)
      onReorder?.(fromId, index, section)
    },
    [draggedId, favoritesSet, recentSet, onReorder, onToggleFavorite],
  )

  // ── Context menu ──
  const handleTileContextMenu = useCallback((e: React.MouseEvent, appId: string, section: LauncherSection) => {
    e.preventDefault()
    setMenu({ appId, section, x: e.clientX, y: e.clientY })
  }, [])

  // Close on outside click / Escape.
  useEffect(() => {
    if (!menu) return
    const handleMouseDown = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenu(null)
      }
    }
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMenu(null)
    }
    document.addEventListener('mousedown', handleMouseDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('mousedown', handleMouseDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [menu])

  const handleMenuToggleFavorite = () => {
    if (!menu) return
    onToggleFavorite?.(menu.appId, !favoritesSet.has(menu.appId))
    setMenu(null)
  }
  const handleMenuMoveToTop = () => {
    if (!menu) return
    onMoveToTop?.(menu.appId)
    setMenu(null)
  }
  const handleMenuRemoveRecent = () => {
    if (!menu) return
    onRemoveFromRecent?.(menu.appId)
    setMenu(null)
  }
  const handleMenuManage = () => {
    onManageApps?.()
    setMenu(null)
  }

  // install-local is a toolbar action, not a grid tile; reuse onLaunch routing.
  const installLocalTile = useMemo<LauncherApp>(
    () => ({
      id: 'install-local',
      label: t('launcher.installLocal'),
      icon: <Download size={20} />,
      action: { kind: 'builtin', builtin: 'install-local' },
    }),
    [t],
  )

  const renderSection = (section: LauncherSection, list: LauncherApp[]) => (
    <section
      className="ai-sidebar-launcher-section"
      data-testid={`launcher-section-${section}`}
      data-section={section}
    >
      <h2 className="ai-sidebar-launcher-section-title">{t(sectionTitleKeys[section])}</h2>
      <div
        className={view === 'list' ? 'ai-sidebar-launcher-list' : 'ai-sidebar-launcher-grid'}
        data-testid={`launcher-grid-${section}`}
        data-guide-id={GuideIds.launcher_app_grid}
        onDragOver={handleGridDragOver}
        onDrop={e => handleGridDrop(e, section)}
      >
        {list.map(app => (
          <button
            key={app.id}
            type="button"
            draggable={!app.disabled}
            disabled={app.disabled}
            data-app-id={app.id}
            className={`ai-sidebar-launcher-tile${view === 'list' ? ' row' : ''}${app.disabled ? ' disabled' : ''}${app.crashed || app.failed ? ' crashed' : ''}${draggedId === app.id ? ' is-dragging' : ''}`}
            onClick={() => onLaunch(app)}
            onDragStart={e => handleTileDragStart(e, app.id)}
            onDragEnd={handleTileDragEnd}
            onContextMenu={e => handleTileContextMenu(e, app.id, section)}
          >
            <span className="ai-sidebar-launcher-tile-icon">
              {app.icon}
              {app.badge && (
                <span
                  className={`ai-sidebar-launcher-tile-badge ${app.badge.transport}${app.badge.thirdParty ? ' third-party' : ''}`}
                  title={
                    app.badge.thirdParty
                      ? `${app.badge.transport === 'subprocess' ? 'subprocess (crash-isolated)' : 'in-process (shares host)'} · third-party (untrusted)`
                      : app.badge.transport === 'subprocess'
                        ? 'subprocess · crash-isolated'
                        : 'in-process · shares host'
                  }
                />
              )}
              {app.restartPending && (
                <span className="ai-sidebar-launcher-tile-pending" title={t('settings.plugins.state.restart_pending')} />
              )}
              {(app.crashed || app.failed) && (
                <span
                  className="ai-sidebar-launcher-tile-crash"
                  title={`${t(app.failed ? 'launcher.failed' : 'launcher.crashed')}${app.failureCause ? `: ${app.failureCause}` : ''}`}
                  data-testid="launcher-tile-crash"
                />
              )}
            </span>
            <span className="ai-sidebar-launcher-tile-label">{app.label}</span>
            {view === 'list' && app.description && (
              <span className="ai-sidebar-launcher-tile-desc">{app.description}</span>
            )}
            {view === 'list' && app.restartPending && (
              <span className="ai-sidebar-launcher-tile-pending-text">{t('launcher.restartPending')}</span>
            )}
            {view === 'list' && (app.crashed || app.failed) && (
              <span className="ai-sidebar-launcher-tile-crash-text" data-testid="launcher-tile-crash-text">{t(app.failed ? 'launcher.failed' : 'launcher.crashed')}</span>
            )}
          </button>
        ))}
      </div>
    </section>
  )

  return (
    <div
      className={`ai-sidebar-launcher${zipDragOver ? ' zip-drag-over' : ''}`}
      onDragOver={handleZipDragOver}
      onDragLeave={handleZipDragLeave}
      onDrop={handleZipDrop}
    >
      {zipDragOver && (
        <div className="ai-sidebar-launcher-zip-overlay" data-testid="launcher-zip-drop-overlay">
          <Download size={28} />
          <span>{t('launcher.dropToInstall')}</span>
        </div>
      )}
      {/* Mobile only — on desktop the mode switch parks in the shell topbar. */}
      {isMobile && (
        <div className="ai-sidebar-launcher-header">
          <SidebarModeSwitch value={mode} onChange={onModeChange} />
        </div>
      )}
      <div className="ai-sidebar-launcher-toolbar" data-testid="launcher-toolbar">
        <button
          type="button"
          className="ai-sidebar-launcher-view-btn"
          data-testid="launcher-view-toggle"
          title={view === 'grid' ? t('launcher.view.list') : t('launcher.view.grid')}
          aria-label={view === 'grid' ? t('launcher.view.list') : t('launcher.view.grid')}
          onClick={() => onViewChange?.(view === 'grid' ? 'list' : 'grid')}
        >
          {view === 'grid' ? <List size={14} /> : <Grid2x2 size={14} />}
          <span>{view === 'grid' ? t('launcher.view.list') : t('launcher.view.grid')}</span>
        </button>
        <div className="ai-sidebar-launcher-dev-actions">
          {showDeveloperActions && (
            <button
              type="button"
              className="ai-sidebar-launcher-install"
              data-testid="launcher-install-local"
              title={t('launcher.installLocal')}
              onClick={() => onLaunch(installLocalTile)}
            >
              <Download size={14} />
              <span>{t('launcher.installLocal')}</span>
            </button>
          )}
        </div>
      </div>
      <div className="ai-sidebar-launcher-body" data-testid="sidebar-launcher-scroll">
        {favoriteList.length > 0 && renderSection('favorites', favoriteList)}
        {recentList.length > 0 && renderSection('recent', recentList)}
        {renderSection('all', allList)}
      </div>
      {/* Same login-state footer as the code-mode sidebar bottom. */}
      {account !== undefined && (
        <div className="ai-sidebar-launcher-footer">
          <div className="ai-sidebar-footer-row">
            <SidebarAccountMenu
              account={account}
              onOpenSettings={onOpenSettings}
            />
            {(onOpenMobileSync || onOpenSettings) && (
              <div className="ai-sidebar-footer-actions">
                {onOpenMobileSync && (
                  <button
                    type="button"
                    className="ai-sidebar-footer-btn"
                    title={t('shell.sidebar.mobileSync')}
                    aria-label={t('shell.sidebar.mobileSync')}
                    onClick={onOpenMobileSync}
                  >
                    <Smartphone size={14} />
                  </button>
                )}
                {onOpenSettings && (
                  <button
                    type="button"
                    className="ai-sidebar-footer-btn"
                    data-guide-id="topbar.settings"
                    title={t('shell.sidebar.settings')}
                    aria-label={t('shell.sidebar.settings')}
                    onClick={onOpenSettings}
                  >
                    <Settings size={14} />
                  </button>
                )}
              </div>
            )}
          </div>
        </div>
      )}
      {menu && (
        <div
          ref={menuRef}
          role="menu"
          className="ai-sidebar-launcher-menu"
          style={{ top: menu.y, left: menu.x }}
          data-testid="launcher-context-menu"
        >
          <button
            type="button"
            role="menuitem"
            className="ai-sidebar-launcher-menu-item"
            data-testid="launcher-menu-favorite"
            onClick={handleMenuToggleFavorite}
          >
            {favoritesSet.has(menu.appId) ? t('launcher.menu.unfavorite') : t('launcher.menu.favorite')}
          </button>
          <button
            type="button"
            role="menuitem"
            className="ai-sidebar-launcher-menu-item"
            data-testid="launcher-menu-move-top"
            onClick={handleMenuMoveToTop}
          >
            {t('launcher.menu.moveToTop')}
          </button>
          {menu.section === 'recent' && (
            <button
              type="button"
              role="menuitem"
              className="ai-sidebar-launcher-menu-item"
              data-testid="launcher-menu-remove-recent"
              onClick={handleMenuRemoveRecent}
            >
              {t('launcher.menu.removeFromRecent')}
            </button>
          )}
          <button
            type="button"
            role="menuitem"
            className="ai-sidebar-launcher-menu-item"
            data-testid="launcher-menu-manage"
            onClick={handleMenuManage}
          >
            {t('launcher.menu.manageApps')}
          </button>
        </div>
      )}
    </div>
  )
}