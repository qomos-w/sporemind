import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { fireEvent } from '@testing-library/react'
import { I18nProvider } from '../../../i18n'
import { BrowserOverlayProvider, type BrowserOverlayApi } from '../browserOverlay'
import type { AppEntry } from '../../../application/app-registry'
import {
  SidebarLauncher,
  buildLauncherApps,
  computeLauncherDropIndex,
  pushRecent,
  reorderIdList,
  type LauncherApp,
  type LauncherSection,
  type SidebarLauncherProps,
} from './SidebarLauncher'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const t = (key: string) => key

function makeApp(id: string): LauncherApp {
  return { id, label: `app-${id}`, icon: null, action: { kind: 'builtin', builtin: 'review' } }
}

function makeApps(count: number): LauncherApp[] {
  return Array.from({ length: count }, (_, i) => makeApp(`a${i}`))
}

describe('computeLauncherDropIndex', () => {
  const rect = (top: number, left: number, size: number) => ({ top, bottom: top + size, left, right: left + size })
  // 2×2 grid: a0 (row0,col0) a1 (row0,col1) a2 (row1,col0) a3 (row1,col1)
  const grid = [
    { appId: 'a0', rect: rect(0, 0, 100) },
    { appId: 'a1', rect: rect(0, 100, 100) },
    { appId: 'a2', rect: rect(100, 0, 100) },
    { appId: 'a3', rect: rect(100, 100, 100) },
  ]

  it('returns 0 when the pointer is above every tile', () => {
    expect(computeLauncherDropIndex(50, -5, grid, null)).toBe(0)
  })

  it('counts full rows and the column edge inside a row band', () => {
    // right of a1's midline, in a1's row band → after a0 and a1
    expect(computeLauncherDropIndex(150, 50, grid, null)).toBe(2)
    // right of a0 but left of a1, in a1's row band → before a1
    expect(computeLauncherDropIndex(70, 50, grid, null)).toBe(1)
    // in a2's row band, left of its midline → before a2 (row-major flow)
    expect(computeLauncherDropIndex(30, 150, grid, null)).toBe(2)
    // below every tile → end of the list
    expect(computeLauncherDropIndex(150, 400, grid, null)).toBe(4)
  })

  it('skips the dragged tile when computing the insertion position', () => {
    // dragging a0: only a1/a2/a3 count; pointer far below → 3
    expect(computeLauncherDropIndex(150, 400, grid, 'a0')).toBe(3)
    // dragging a3, pointer above the grid → 0
    expect(computeLauncherDropIndex(50, -5, grid, 'a3')).toBe(0)
  })

  it('counts every tile when nothing is being dragged', () => {
    expect(computeLauncherDropIndex(150, 400, grid, null)).toBe(4)
  })
})

describe('buildLauncherApps', () => {
  const runningApp = (id: string, entrypoints: AppEntry['entrypoints'] = [], state = 'running'): AppEntry => ({
    id,
    runtime: 'spore',
    namespace: `ns-${id}`,
    version: '1.0.0',
    state,
    entrypoints,
  })

  it('includes the three built-in apps (review/glass/puppet); install-local is NOT a tile', () => {
    const apps = buildLauncherApps([], t, false)
    const builtins = apps.filter(a => a.action.kind === 'builtin')
    expect(builtins.map(a => a.action)).toEqual([
      { kind: 'builtin', builtin: 'review' },
      { kind: 'builtin', builtin: 'glass' },
      { kind: 'builtin', builtin: 'puppet' },
    ])
  })

  it('drops the glass/puppet dev-tool tiles on production shells', () => {
    const apps = buildLauncherApps([], t, false, false)
    expect(apps.map(a => a.id)).toEqual(['review'])
  })

  it('prepends a browser tile only in wails', () => {
    const web = buildLauncherApps([], t, false)
    expect(web[0]!.id).toBe('review')
    const wails = buildLauncherApps([], t, true)
    expect(wails[0]!.id).toBe('browser')
    expect(wails[0]!.action).toEqual({ kind: 'builtin', builtin: 'browser' })
    expect(wails).toHaveLength(4)
  })

  it('adds a Puzzle tile per view entrypoint of running apps, and skips stopped ones', () => {
    const apps = buildLauncherApps([
      runningApp('spore-app', [
        { id: 'main', title: 'Main View', kind: 'view', route: '/main' },
        { id: 'panel', title: 'Side Panel', kind: 'panel', zone: 'right', route: '/panel' },
      ]),
      runningApp('stopped-app', [{ id: 'main', title: 'Stopped', kind: 'view', route: '/' }], 'stopped'),
    ], t, false)
    const pluginTiles = apps.filter(a => a.action.kind === 'plugin')
    // panel entrypoints are not view tiles; the stopped app is excluded entirely.
    expect(pluginTiles).toHaveLength(1)
    expect(pluginTiles[0]!.id).toBe('spore-app:main')
    expect(pluginTiles[0]!.label).toBe('Main View')
    expect(pluginTiles[0]!.description).toBe('ns-spore-app')
    expect(pluginTiles[0]!.action).toEqual({
      kind: 'plugin',
      pluginID: 'spore-app',
      entryID: 'main',
      title: 'Main View',
      route: '/main',
    })
  })

  it('falls back to the app id when an entry has no title or route', () => {
    const apps = buildLauncherApps([runningApp('bare', [{ id: 'x', kind: 'view', title: '' }])], t, false)
    const tile = apps.find(a => a.action.kind === 'plugin')!
    expect(tile.label).toBe('bare')
    expect((tile.action as { route: string }).route).toBe('')
  })

  it('tints the tile icon with the app bundle color', () => {
    const [tile] = buildLauncherApps([
      {
        ...runningApp('colorful', [{ id: 'main', title: 'Main', kind: 'view', route: '/' }]),
        icon: 'server',
        color: '#2563eb',
      },
    ], t, false).filter(a => a.action.kind === 'plugin')
    expect(tile!.id).toBe('colorful:main')
    // resolveAppIcon returns a lucide element carrying the tint + marker class.
    expect((tile!.icon as any).props.style).toEqual({ color: '#2563eb' })
    expect((tile!.icon as any).props.className).toBe('app-icon-colored')
  })

  it('surfaces restart_pending apps as disabled tiles; stopped/unloading stay hidden', () => {
    const apps = buildLauncherApps([
      runningApp('update-app', [{ id: 'main', title: 'Update', kind: 'view', route: '/' }], 'restart_pending'),
      runningApp('stopped-app', [{ id: 'main', title: 'Stopped', kind: 'view', route: '/' }], 'stopped'),
      runningApp('unloading-app', [{ id: 'main', title: 'Unloading', kind: 'view', route: '/' }], 'unloading'),
    ], t, false)
    const pluginTiles = apps.filter(a => a.action.kind === 'plugin')
    expect(pluginTiles).toHaveLength(1)
    const tile = pluginTiles[0]!
    expect(tile.id).toBe('update-app:main')
    expect(tile.disabled).toBe(true)
    expect(tile.restartPending).toBe(true)
  })

  it('surfaces crashed apps as clickable tiles flagged with the crash badge', () => {
    const apps = buildLauncherApps([
      runningApp('crash-app', [{ id: 'main', title: 'Crash', kind: 'view', route: '/' }], 'crashed'),
      runningApp('stopped-app', [{ id: 'main', title: 'Stopped', kind: 'view', route: '/' }], 'stopped'),
    ], t, false)
    const pluginTiles = apps.filter(a => a.action.kind === 'plugin')
    expect(pluginTiles).toHaveLength(1)
    const tile = pluginTiles[0]!
    expect(tile.id).toBe('crash-app:main')
    expect(tile.crashed).toBe(true)
    // Not disabled: clicking opens the panel, which renders the error state.
    expect(tile.disabled).toBe(false)
    expect(tile.restartPending).toBe(false)
  })

  it('surfaces failed apps as clickable tiles with the failed badge and cause', () => {
    const apps = buildLauncherApps([
      {
        ...runningApp('failed-app', [{ id: 'main', title: 'Failed', kind: 'view', route: '/' }], 'failed'),
        error: 'open F:\\gone\\plugin.exe: no such file',
      },
      runningApp('stopped-app', [{ id: 'main', title: 'Stopped', kind: 'view', route: '/' }], 'stopped'),
    ], t, false)
    const pluginTiles = apps.filter(a => a.action.kind === 'plugin')
    expect(pluginTiles).toHaveLength(1)
    const tile = pluginTiles[0]!
    expect(tile.id).toBe('failed-app:main')
    expect(tile.failed).toBe(true)
    expect(tile.crashed).toBe(false)
    expect(tile.disabled).toBe(false)
    expect(tile.failureCause).toContain('no such file')
  })
})

describe('SidebarLauncher', () => {
  let container: HTMLDivElement
  let root: Root | null

  interface SetupOptions {
    favorites?: string[]
    recent?: string[]
    order?: string[]
    view?: 'grid' | 'list'
    showDeveloperActions?: boolean
    onInstallZipDrop?: (file: File) => void
    account?: SidebarLauncherProps['account']
    onOpenSettings?: SidebarLauncherProps['onOpenSettings']
    onOpenMobileSync?: SidebarLauncherProps['onOpenMobileSync']
  }

  const setup = async (apps: LauncherApp[], props: SetupOptions = {}) => {
    const onLaunch = vi.fn()
    const onModeChange = vi.fn()
    const onViewChange = vi.fn()
    const onReorder = vi.fn()
    const onToggleFavorite = vi.fn()
    const onMoveToTop = vi.fn()
    const onRemoveFromRecent = vi.fn()
    const onManageApps = vi.fn()
    const pushOverlay = vi.fn()
    const popOverlay = vi.fn()
    const overlayApi: BrowserOverlayApi = { pushOverlay, popOverlay }
    await act(async () => {
      root = createRoot(container)
      root.render(
        <I18nProvider initialLocale="en-US">
          <BrowserOverlayProvider value={overlayApi}>
            <SidebarLauncher
              apps={apps}
              mode="app"
              onModeChange={onModeChange}
              onLaunch={onLaunch}
              view={props.view}
              onViewChange={onViewChange}
              favorites={props.favorites}
              recent={props.recent}
              order={props.order}
              onReorder={onReorder}
              onToggleFavorite={onToggleFavorite}
              onMoveToTop={onMoveToTop}
              onRemoveFromRecent={onRemoveFromRecent}
              onManageApps={onManageApps}
              onInstallZipDrop={props.onInstallZipDrop}
              showDeveloperActions={props.showDeveloperActions}
              account={props.account}
              onOpenSettings={props.onOpenSettings}
              onOpenMobileSync={props.onOpenMobileSync}
            />
          </BrowserOverlayProvider>
        </I18nProvider>,
      )
    })
    return { onLaunch, onModeChange, onViewChange, onReorder, onToggleFavorite, onMoveToTop, onRemoveFromRecent, onManageApps, pushOverlay, popOverlay }
  }

  const tileIn = (section: string, appId: string) =>
    container.querySelector(`[data-section="${section}"] [data-app-id="${appId}"]`) as HTMLElement
  const gridOf = (section: string) =>
    container.querySelector(`[data-testid="launcher-grid-${section}"]`) as HTMLElement
  const sectionIds = (section: string) =>
    Array.from(container.querySelectorAll(`[data-section="${section}"] .ai-sidebar-launcher-tile`))
      .map(el => (el as HTMLElement).dataset.appId)

  const makeDataTransfer = (value: string) => ({
    setData: vi.fn(),
    getData: vi.fn(() => value),
    effectAllowed: 'move',
    dropEffect: 'none',
  })

  // A file drag from the OS: types contains 'Files' and dt.files carries the
  // dragged files. jsdom has no real DataTransfer so we build one by hand.
  const makeFileDataTransfer = (files: File[]) => ({
    setData: vi.fn(),
    getData: vi.fn(() => ''),
    effectAllowed: 'copy',
    dropEffect: 'none',
    types: ['Files'],
    items: files.map(f => ({ kind: 'file', type: f.type })),
    files,
  })

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(() => {
    act(() => root?.unmount())
    root = null
    if (container.parentNode) document.body.removeChild(container)
    vi.restoreAllMocks()
  })

  it('renders the mode switch and all three sections in order, hiding empty ones', async () => {
    await setup(makeApps(3), { favorites: ['a0'], recent: ['a1'] })
    expect(container.querySelector('.ai-sidebar-mode-switch')).toBeTruthy()
    const sections = Array.from(container.querySelectorAll('.ai-sidebar-launcher-section'))
    expect(sections.map(s => s.getAttribute('data-section'))).toEqual(['favorites', 'recent', 'all'])
    const titles = Array.from(container.querySelectorAll('.ai-sidebar-launcher-section-title')).map(el => el.textContent)
    expect(titles).toEqual(['Favorites', 'Recent', 'All'])
    // pawpad-style tiles render name only: no description line in the tile structure.
    expect(container.querySelector('.ai-sidebar-launcher-tile-desc')).toBeNull()
  })

  it('renders a toolbar: single view toggle (grid mode shows the list icon), install-local button, and no install tile', async () => {
    const apps = makeApps(2)
    await setup(apps, { showDeveloperActions: true })
    const toolbar = container.querySelector('[data-testid="launcher-toolbar"]')!
    expect(toolbar).toBeTruthy()
    const buttons = Array.from(toolbar.querySelectorAll('.ai-sidebar-launcher-view-btn'))
    expect(buttons).toHaveLength(1)
    // Grid mode: the toggle advertises the target view (list).
    expect(buttons[0]!.querySelector('svg.lucide-list')).toBeTruthy()
    expect(toolbar.querySelector('[data-testid="launcher-install-local"]')).toBeTruthy()
    expect(gridOf('all').querySelector('[data-app-id="install-local"]')).toBeNull()
  })

  it('hides the developer toolbar actions (install local) by default', async () => {
    await setup(makeApps(2))
    expect(container.querySelector('[data-testid="launcher-install-local"]')).toBeNull()
  })

  it('install-local toolbar button launches the install action', async () => {
    const { onLaunch } = await setup(makeApps(2), { showDeveloperActions: true })
    const install = container.querySelector<HTMLButtonElement>('[data-testid="launcher-install-local"]')!
    act(() => { install.click() })
    expect(onLaunch).toHaveBeenCalledTimes(1)
    const arg = onLaunch.mock.calls[0]![0] as { id: string; action: { kind: string; builtin: string } }
    expect(arg.id).toBe('install-local')
    expect(arg.action).toEqual({ kind: 'builtin', builtin: 'install-local' })
  })

  it('clicking the single view toggle reports onViewChange(list)', async () => {
    const { onViewChange } = await setup(makeApps(2))
    const toggle = container.querySelector<HTMLButtonElement>('[data-testid="launcher-view-toggle"]')!
    act(() => { toggle.click() })
    expect(onViewChange).toHaveBeenCalledWith('list')
  })

  it('renders the login-state footer with right-side action buttons when handlers are provided', async () => {
    const onOpenSettings = vi.fn()
    const onOpenMobileSync = vi.fn()
    await setup(makeApps(2), {
      account: { Username: 'admin', DisplayName: 'Admin' } as any,
      onOpenSettings,
      onOpenMobileSync,
    })
    const footer = container.querySelector('.ai-sidebar-launcher-footer')
    expect(footer).toBeTruthy()
    expect(footer!.querySelector('.ai-sidebar-account-button')).toBeTruthy()
    const actions = footer!.querySelector('.ai-sidebar-footer-actions')
    expect(actions).toBeTruthy()
    const btns = Array.from(actions!.querySelectorAll<HTMLButtonElement>('.ai-sidebar-footer-btn'))
    expect(btns).toHaveLength(2)
    expect(btns[0]!.querySelector('svg.lucide-smartphone')).toBeTruthy()
    expect(btns[1]!.querySelector('svg.lucide-settings')).toBeTruthy()
    act(() => { btns[1]!.click() })
    expect(onOpenSettings).toHaveBeenCalledTimes(1)
  })

  it('renders no login-state footer without account props', async () => {
    await setup(makeApps(2))
    expect(container.querySelector('.ai-sidebar-launcher-footer')).toBeNull()
  })

  it('list view renders one row per app with description, grid view does not', async () => {
    const withDesc: LauncherApp[] = makeApps(2).map((app, i) => ({ ...app, description: `ns-${i}` }))
    await setup(withDesc, { view: 'list' })
    expect(gridOf('all').classList.contains('ai-sidebar-launcher-list')).toBe(true)
    const rows = Array.from(gridOf('all').querySelectorAll('.ai-sidebar-launcher-tile'))
    expect(rows).toHaveLength(2)
    rows.forEach(row => expect(row.classList.contains('row')).toBe(true))
    expect(rows[0]!.querySelector('.ai-sidebar-launcher-tile-desc')!.textContent).toBe('ns-0')
  })

  it('renders restart_pending tiles disabled with an update badge, and never launches them', async () => {
    const pending: LauncherApp = {
      id: 'upd:main',
      label: 'Update App',
      icon: null,
      disabled: true,
      restartPending: true,
      action: { kind: 'plugin', pluginID: 'upd', entryID: 'main', title: 'Main', route: '/' },
    }
    const { onLaunch } = await setup([pending])
    const tile = tileIn('all', 'upd:main')
    expect(tile).toBeTruthy()
    expect((tile as HTMLButtonElement).disabled).toBe(true)
    expect(tile.classList.contains('disabled')).toBe(true)
    expect(tile.querySelector('.ai-sidebar-launcher-tile-pending')).toBeTruthy()
    expect(tile.getAttribute('draggable')).toBe('false')
    act(() => { tile.click() })
    expect(onLaunch).not.toHaveBeenCalled()
  })

  it('renders crashed tiles greyed with a red crash badge, and still launches them', async () => {
    const crashedApp: LauncherApp = {
      id: 'dead:main',
      label: 'Dead App',
      icon: null,
      crashed: true,
      action: { kind: 'plugin', pluginID: 'dead', entryID: 'main', title: 'Main', route: '/' },
    }
    const { onLaunch } = await setup([crashedApp])
    const tile = tileIn('all', 'dead:main')
    expect(tile).toBeTruthy()
    // Clickable: the opened panel shows the crash error state + restart action.
    expect((tile as HTMLButtonElement).disabled).toBe(false)
    expect(tile.classList.contains('crashed')).toBe(true)
    expect(tile.querySelector('[data-testid="launcher-tile-crash"]')).toBeTruthy()
    act(() => { tile.click() })
    expect(onLaunch).toHaveBeenCalledTimes(1)
  })

  it('renders a crash text pill on crashed rows in list view', async () => {
    const crashedApp: LauncherApp = {
      id: 'dead:main',
      label: 'Dead App',
      icon: null,
      crashed: true,
      action: { kind: 'plugin', pluginID: 'dead', entryID: 'main', title: 'Main', route: '/' },
    }
    await setup([crashedApp], { view: 'list' })
    const pill = tileIn('all', 'dead:main').querySelector('[data-testid="launcher-tile-crash-text"]')
    expect(pill).toBeTruthy()
    expect(pill!.textContent).toBe('Crashed')
  })

  it('hides favorites/recent when empty but always renders the all section', async () => {
    await setup(makeApps(2))
    expect(container.querySelectorAll('.ai-sidebar-launcher-section')).toHaveLength(1)
    expect(container.querySelector('[data-section="all"]')).toBeTruthy()
    expect(container.querySelector('[data-section="favorites"]')).toBeNull()
    expect(container.querySelector('[data-section="recent"]')).toBeNull()
  })

  it('partitions apps into sections by favorites/recent/order', async () => {
    const apps = [
      makeApp('review'),
      makeApp('glass'),
      makeApp('a0'),
      makeApp('a1'),
      makeApp('a2'),
      makeApp('a3'),
      makeApp('a4'),
      makeApp('a5'),
    ]
    await setup(apps, {
      favorites: ['a3', 'a0'],
      recent: ['a1', 'a3'],
      order: ['glass', 'a5', 'review'],
    })
    expect(sectionIds('favorites')).toEqual(['a3', 'a0'])
    expect(sectionIds('recent')).toEqual(['a1', 'a3'])
    // order-listed ids first, unknown ids fall back to apps order afterwards
    expect(sectionIds('all')).toEqual(['glass', 'a5', 'review', 'a0', 'a1', 'a2', 'a3', 'a4'])
  })

  it('calls onLaunch with the clicked app', async () => {
    const { onLaunch } = await setup(makeApps(2))
    fireEvent.click(tileIn('all', 'a0'))
    expect(onLaunch).toHaveBeenCalledTimes(1)
    expect(onLaunch.mock.calls[0]![0]!.id).toBe('a0')
  })

  describe('context menu', () => {
    it('opens on right-click with favorite/move-to-top/manage items and no remove-from-recent in the all section', async () => {
      const { onMoveToTop, onManageApps, pushOverlay } = await setup(makeApps(2))
      fireEvent.contextMenu(tileIn('all', 'a0'))
      const menu = container.querySelector('[data-testid="launcher-context-menu"]')
      expect(menu).toBeTruthy()
      expect(container.querySelectorAll('.ai-sidebar-launcher-menu-item')).toHaveLength(3)
      expect(container.querySelector('[data-testid="launcher-menu-remove-recent"]')).toBeNull()
      // useBrowserOverlay: opening the menu pushes an overlay.
      expect(pushOverlay).toHaveBeenCalledTimes(1)

      fireEvent.click(container.querySelector('[data-testid="launcher-menu-move-top"]')!)
      expect(onMoveToTop).toHaveBeenCalledWith('a0')
      expect(container.querySelector('[data-testid="launcher-context-menu"]')).toBeNull()

      fireEvent.contextMenu(tileIn('all', 'a1'))
      fireEvent.click(container.querySelector('[data-testid="launcher-menu-manage"]')!)
      expect(onManageApps).toHaveBeenCalledTimes(1)
    })

    it('labels the first item Favorite for non-favorites and calls onToggleFavorite(id, true)', async () => {
      const { onToggleFavorite } = await setup(makeApps(2))
      fireEvent.contextMenu(tileIn('all', 'a1'))
      expect(container.querySelector('[data-testid="launcher-menu-favorite"]')!.textContent).toBe('Favorite')
      fireEvent.click(container.querySelector('[data-testid="launcher-menu-favorite"]')!)
      expect(onToggleFavorite).toHaveBeenCalledWith('a1', true)
    })

    it('labels the first item Unfavorite for favorites and calls onToggleFavorite(id, false)', async () => {
      const { onToggleFavorite } = await setup(makeApps(2), { favorites: ['a0'] })
      fireEvent.contextMenu(tileIn('favorites', 'a0'))
      expect(container.querySelector('[data-testid="launcher-menu-favorite"]')!.textContent).toBe('Unfavorite')
      fireEvent.click(container.querySelector('[data-testid="launcher-menu-favorite"]')!)
      expect(onToggleFavorite).toHaveBeenCalledWith('a0', false)
    })

    it('shows remove-from-recent only for tiles displayed in the recent section', async () => {
      const { onRemoveFromRecent } = await setup(makeApps(3), { recent: ['a1'] })
      fireEvent.contextMenu(tileIn('all', 'a0'))
      expect(container.querySelector('[data-testid="launcher-menu-remove-recent"]')).toBeNull()
      fireEvent.keyDown(document, { key: 'Escape' })
      expect(container.querySelector('[data-testid="launcher-context-menu"]')).toBeNull()

      fireEvent.contextMenu(tileIn('recent', 'a1'))
      expect(container.querySelector('[data-testid="launcher-menu-remove-recent"]')).toBeTruthy()
      fireEvent.click(container.querySelector('[data-testid="launcher-menu-remove-recent"]')!)
      expect(onRemoveFromRecent).toHaveBeenCalledWith('a1')
    })

    it('closes the menu on outside mousedown', async () => {
      await setup(makeApps(2))
      fireEvent.contextMenu(tileIn('all', 'a0'))
      expect(container.querySelector('[data-testid="launcher-context-menu"]')).toBeTruthy()
      fireEvent.mouseDown(document.body)
      expect(container.querySelector('[data-testid="launcher-context-menu"]')).toBeNull()
    })

    it('tracks the overlay ref-count: push on open, pop on close, pop on unmount-while-open', async () => {
      const { pushOverlay, popOverlay } = await setup(makeApps(2))
      // closed initially → nothing pushed
      expect(pushOverlay).not.toHaveBeenCalled()
      expect(popOverlay).not.toHaveBeenCalled()

      fireEvent.contextMenu(tileIn('all', 'a0'))
      expect(pushOverlay).toHaveBeenCalledTimes(1)
      expect(popOverlay).not.toHaveBeenCalled()

      fireEvent.mouseDown(document.body)
      expect(popOverlay).toHaveBeenCalledTimes(1)

      // reopen, then unmount while the menu is open → cleanup pops
      fireEvent.contextMenu(tileIn('all', 'a0'))
      expect(pushOverlay).toHaveBeenCalledTimes(2)
      act(() => root?.unmount())
      root = null
      expect(popOverlay).toHaveBeenCalledTimes(2)
    })
  })

  describe('drag and drop', () => {
    it('reorders within the all section via dragStart/dragOver/drop', async () => {
      const { onReorder } = await setup(makeApps(4))
      const dt = makeDataTransfer('a0')
      fireEvent.dragStart(tileIn('all', 'a0'), { dataTransfer: dt })
      fireEvent.dragOver(gridOf('all'), { dataTransfer: dt })
      fireEvent.drop(gridOf('all'), { dataTransfer: dt })
      expect(onReorder).toHaveBeenCalledTimes(1)
      // zero-rect tiles in jsdom: every non-dragged tile counts before the pointer → end
      expect(onReorder.mock.calls[0]).toEqual(['a0', 3, 'all' as LauncherSection])
    })

    it('drops a non-favorite tile onto the favorites section to favorite it', async () => {
      const { onReorder, onToggleFavorite } = await setup(makeApps(4), { favorites: ['a3'] })
      const dt = makeDataTransfer('a0')
      fireEvent.dragStart(tileIn('all', 'a0'), { dataTransfer: dt })
      fireEvent.drop(gridOf('favorites'), { dataTransfer: dt })
      expect(onToggleFavorite).toHaveBeenCalledWith('a0', true)
      expect(onReorder).not.toHaveBeenCalled()
    })

    it('reorders within the favorites section when a favorite tile is dropped there', async () => {
      const { onReorder, onToggleFavorite } = await setup(makeApps(4), { favorites: ['a3', 'a0'] })
      const dt = makeDataTransfer('a3')
      fireEvent.dragStart(tileIn('favorites', 'a3'), { dataTransfer: dt })
      fireEvent.drop(gridOf('favorites'), { dataTransfer: dt })
      expect(onToggleFavorite).not.toHaveBeenCalled()
      expect(onReorder).toHaveBeenCalledTimes(1)
      // non-dragged favorite tile [a0] counts → insertion index 1
      expect(onReorder.mock.calls[0]).toEqual(['a3', 1, 'favorites' as LauncherSection])
    })

    it('reorders within the recent section when a recent tile is dropped there', async () => {
      const { onReorder, onToggleFavorite } = await setup(makeApps(4), { recent: ['a0', 'a1'] })
      const dt = makeDataTransfer('a0')
      fireEvent.dragStart(tileIn('recent', 'a0'), { dataTransfer: dt })
      fireEvent.drop(gridOf('recent'), { dataTransfer: dt })
      expect(onReorder).toHaveBeenCalledTimes(1)
      expect(onReorder.mock.calls[0]).toEqual(['a0', 1, 'recent' as LauncherSection])
      expect(onToggleFavorite).not.toHaveBeenCalled()
    })
  })

  describe('zip drop-to-install', () => {
    it('shows the drop overlay while a file drag hovers and installs on drop', async () => {
      const onInstallZipDrop = vi.fn()
      await setup(makeApps(2), { onInstallZipDrop })
      const panel = container.querySelector('.ai-sidebar-launcher') as HTMLElement
      const zip = new File([new Uint8Array([0])], 'demo.zip', { type: 'application/zip' })
      const dt = makeFileDataTransfer([zip])

      expect(container.querySelector('[data-testid="launcher-zip-drop-overlay"]')).toBeNull()
      fireEvent.dragOver(panel, { dataTransfer: dt })
      expect(panel.classList.contains('zip-drag-over')).toBe(true)
      expect(container.querySelector('[data-testid="launcher-zip-drop-overlay"]')).toBeTruthy()

      fireEvent.drop(panel, { dataTransfer: dt })
      expect(panel.classList.contains('zip-drag-over')).toBe(false)
      expect(onInstallZipDrop).toHaveBeenCalledTimes(1)
      expect(onInstallZipDrop.mock.calls[0]![0]).toBeInstanceOf(File)
      expect((onInstallZipDrop.mock.calls[0]![0] as File).name).toBe('demo.zip')
    })

    it('clears the overlay on drag leave', async () => {
      const onInstallZipDrop = vi.fn()
      await setup(makeApps(2), { onInstallZipDrop })
      const panel = container.querySelector('.ai-sidebar-launcher') as HTMLElement
      const dt = makeFileDataTransfer([new File([new Uint8Array([0])], 'demo.zip')])
      fireEvent.dragOver(panel, { dataTransfer: dt })
      expect(panel.classList.contains('zip-drag-over')).toBe(true)
      fireEvent.dragLeave(panel, { dataTransfer: dt, relatedTarget: null })
      expect(panel.classList.contains('zip-drag-over')).toBe(false)
    })

    it('ignores drops without a .zip file and tile-reorder drags', async () => {
      const onInstallZipDrop = vi.fn()
      await setup(makeApps(2), { onInstallZipDrop })
      const panel = container.querySelector('.ai-sidebar-launcher') as HTMLElement

      // Non-zip file: overlay lights (extension unreadable during dragover)
      // but the drop is ignored.
      const txt = new File([new Uint8Array([0])], 'notes.txt', { type: 'text/plain' })
      const dt = makeFileDataTransfer([txt])
      fireEvent.dragOver(panel, { dataTransfer: dt })
      expect(panel.classList.contains('zip-drag-over')).toBe(true)
      fireEvent.drop(panel, { dataTransfer: dt })
      expect(onInstallZipDrop).not.toHaveBeenCalled()

      // Tile reorder drag (text/plain only) never lights the overlay.
      const tileDt = makeDataTransfer('a0')
      fireEvent.dragOver(panel, { dataTransfer: tileDt })
      expect(panel.classList.contains('zip-drag-over')).toBe(false)
    })

    it('does nothing when no onInstallZipDrop handler is wired', async () => {
      await setup(makeApps(2))
      const panel = container.querySelector('.ai-sidebar-launcher') as HTMLElement
      const dt = makeFileDataTransfer([new File([new Uint8Array([0])], 'demo.zip')])
      fireEvent.dragOver(panel, { dataTransfer: dt })
      expect(panel.classList.contains('zip-drag-over')).toBe(false)
    })
  })
})

describe('pushRecent (recent tracking: unshift, dedupe, cap 6)', () => {
  it('unshifts a new id and caps at 6', () => {
    const recent = ['a1', 'a2', 'a3', 'a4', 'a5', 'a6']
    expect(pushRecent(recent, 'a0')).toEqual(['a0', 'a1', 'a2', 'a3', 'a4', 'a5'])
  })

  it('moves an existing id to the front without duplicating it', () => {
    expect(pushRecent(['a0', 'b1', 'c2'], 'b1')).toEqual(['b1', 'a0', 'c2'])
  })

  it('accepts a custom cap', () => {
    expect(pushRecent(['a0', 'a1'], 'b0', 2)).toEqual(['b0', 'a0'])
  })
})

describe('reorderIdList (drag & drop removal + insertion)', () => {
  it('removes fromId then inserts at toIndex', () => {
    expect(reorderIdList(['a0', 'a1', 'a2', 'a3'], 'a1', 3)).toEqual(['a0', 'a2', 'a3', 'a1'])
    expect(reorderIdList(['a0', 'a1', 'a2', 'a3'], 'a3', 0)).toEqual(['a3', 'a0', 'a1', 'a2'])
  })

  it('clamps out-of-range indices and ignores unknown ids', () => {
    expect(reorderIdList(['a0', 'a1'], 'a1', 99)).toEqual(['a0', 'a1'])
    expect(reorderIdList(['a0', 'a1'], 'a1', 0)).toEqual(['a1', 'a0'])
    expect(reorderIdList(['a0', 'a1'], 'missing', 1)).toEqual(['a0', 'a1'])
  })
})
