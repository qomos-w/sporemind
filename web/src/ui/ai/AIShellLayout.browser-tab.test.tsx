import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AIShellLayout, resolveTabInsertIndex, computeAutoOpenPluginTabs } from './AIShellLayout'
import { appRegistry } from '../../application/app-registry'
import { I18nProvider } from '../../i18n'
import type { ViewMode } from './model/ai-types'
import * as browserManager from '../../application/browser-manager'
import * as workspaceClient from '../../gen-clients/workspace/client'
import { resetAgentInfoStore } from './hooks/agentInfoStore'
import { resetAgentListStore } from './hooks/agentListStore'
import { getAIShellLayoutState, saveAIShellLayoutState } from '../../application/workspace-ui-state'

const mockLayout = { viewMode: 'desktop' as ViewMode, containerRef: vi.fn() }

const isWailsMock = vi.hoisted(() => vi.fn(() => true))
const listBrowserWindowsMock = vi.hoisted(() => vi.fn(() => Promise.resolve<any[]>([])))
const wailsHandlers = vi.hoisted(() => new Map<string, Array<(ev: unknown) => void>>())

vi.mock('./hooks/useResponsiveLayout', () => ({
  useResponsiveLayout: () => mockLayout,
}))

vi.mock('../../application/runtime', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../application/runtime')>()),
  isWails: isWailsMock,
}))

vi.mock('../../application/browser-manager', () => ({
  listBrowserWindows: listBrowserWindowsMock,
  closeBrowserInstance: vi.fn(() => Promise.resolve()),
  openBrowserInstance: vi.fn(() => Promise.resolve()),
  onBrowserManagerEvent: vi.fn(() => () => {}),
  openBrowserWindow: vi.fn(() => Promise.resolve({ Instance: null })),
  closeBrowserWindow: vi.fn(() => Promise.resolve()),
  updateBrowserWindow: vi.fn(() => Promise.resolve()),
  updateBrowserWindowUrl: vi.fn(() => Promise.resolve()),
  proxySignature: (cfg: { Proxy: string; ProxyMode: string }) =>
    cfg.ProxyMode === 'none' ? 'none' : (cfg.Proxy ? `custom:${cfg.Proxy}` : 'system'),
  effectiveProxyMode: (cfg: { Proxy: string; ProxyMode: string }) =>
    cfg.ProxyMode === 'none' ? 'none'
      : cfg.ProxyMode === 'custom' ? (cfg.Proxy ? 'custom' : 'system')
        : (cfg.Proxy ? 'custom' : 'system'),
}))

vi.mock('../../application/wails-runtime', () => ({
  isWailsAvailable: isWailsMock,
  emitWailsEvent: vi.fn(),
  onWailsEvent: (name: string, cb: (ev: unknown) => void) => {
    const list = wailsHandlers.get(name) ?? []
    list.push(cb)
    wailsHandlers.set(name, list)
    return () => {}
  },
}))

const emitWails = async (name: string, data?: unknown) => {
  await act(async () => {
    wailsHandlers.get(name)?.forEach(cb => cb({ data }))
  })
}

vi.mock('../../application/wails-bridge', () => ({
  setBrowserWindowVisible: vi.fn(() => Promise.resolve()),
  closeBrowserSession: vi.fn(() => Promise.resolve()),
  hideAllBrowserWindows: vi.fn(() => Promise.resolve()),
  captureBrowserWindow: vi.fn(() => Promise.resolve('')),
  setBrowserColorScheme: vi.fn(() => Promise.resolve()),
  navigateBrowserSession: vi.fn(() => Promise.resolve()),
  openBrowserSession: vi.fn(() => Promise.resolve()),
  goBackBrowserWindow: vi.fn(() => Promise.resolve()),
  goForwardBrowserWindow: vi.fn(() => Promise.resolve()),
  updateBrowserWindow: vi.fn(() => Promise.resolve()),
  splashDone: vi.fn(() => Promise.resolve()),
}))

vi.mock('../../application/workspace-ui-state', () => ({
  defaultAIShellProjectUIState: { sidebarOrder: [], sidebarPinned: [] },
  getAIShellProjectUIState: vi.fn(async () => ({ sidebarOrder: [], sidebarPinned: [] })),
  saveAIShellProjectUIState: vi.fn(async () => undefined),
  getAIShellLayoutState: vi.fn(async () => ({ rightPanelOpen: true })),
  saveAIShellLayoutState: vi.fn(async () => undefined),
  getMultiConsoleGridConfig: vi.fn(async () => ({ cols: 1, rows: 1 })),
  getCategoryExpansion: vi.fn(async () => ({})),
  toggleCategoryExpansion: vi.fn(async () => undefined),
  getExpandedProjectIds: vi.fn(async () => new Set<string>()),
  toggleExpandedProjectId: vi.fn(async () => undefined),
  getKbNotesUIState: vi.fn(async () => ({ view: 'home', lane: null, expandedProjectIds: [] })),
  saveKbNotesUIState: vi.fn(async () => undefined),
  defaultKbNotesUIState: { view: 'home', lane: null, expandedProjectIds: [] },
  defaultAIShellSessionState: { selectedAgentId: null, previousAgentId: null },
  getAIShellSessionState: vi.fn(async () => ({ selectedAgentId: null, previousAgentId: null })),
  saveAIShellSessionState: vi.fn(async () => undefined),
  getComposerBadgesVisible: vi.fn(async () => false),
  saveComposerBadgesVisible: vi.fn(async () => undefined),
  getTurnTailMetricsVisible: vi.fn(async () => ({ budgetBar: false, tokenBadge: false, duration: false, throughput: false })),
  saveTurnTailMetricsVisible: vi.fn(async () => undefined),
  defaultTurnTailMetricsVisible: { budgetBar: false, tokenBadge: false, duration: false, throughput: false },
  getComposerExtrasVisible: vi.fn(async () => ({ quickGit: true, mobileContextAgent: true })),
  saveComposerExtrasVisible: vi.fn(async () => undefined),
  defaultComposerExtrasVisible: { quickGit: true, mobileContextAgent: true },
  getSmoothStream: vi.fn(async () => true),
  saveSmoothStream: vi.fn(async () => undefined),
  getDeveloperMode: vi.fn(async () => false),
  saveDeveloperMode: vi.fn(async () => undefined),
  DEFAULT_UI_EXPANSION_MODES: {},
  DEFAULT_UI_EXPAND_DURATION_MS: 200,
  getUiExpansionModes: vi.fn(async () => ({})),
  getUiExpandDurationMs: vi.fn(async () => 200),
  getPinnedModes: vi.fn(async () => []),
  getProviderUserAgentVisible: vi.fn(async () => false),
}))

vi.mock('../../gen-clients/sshmanager/client', () => ({
  hostList: vi.fn(),
  shellOpen: vi.fn(),
  shellClose: vi.fn(),
  OnSshManagerEvent: vi.fn(() => () => {}),
}))

vi.mock('../../application/generated-client', () => ({
  client: {
    events: { onService: vi.fn(() => () => {}), onInstance: vi.fn(() => () => {}) },
    getTransport: vi.fn(() => null),
    invoke: vi.fn(async () => ({})),
  },
}))

vi.mock('../../application/backend-ready', () => ({
  waitForBackendReady: vi.fn(async () => undefined),
}))

vi.mock('../../gen-clients/workspace/client', () => ({
  account: vi.fn(async () => ({ displayName: 'Tester', roles: ['admin'] })),
  session: vi.fn(async () => ({ sessionId: 'session-1', online: true })),
  listProject: vi.fn(async () => ({ Items: [] })),
  systemTree: vi.fn(async () => []),
  active: vi.fn(async () => undefined),
  listAgents: vi.fn(async () => ({ Items: [] })),
  agentListState: vi.fn(async () => ({ Version: 1, Full: true, Items: [] })),
  OnAgentListState: vi.fn(() => () => {}),
  listAgentKinds: vi.fn(async () => ({ Items: [] })),
  listAgentKindConfigs: vi.fn(async () => ({ Items: [] })),
  createAgent: vi.fn(async () => ({})),
  create: vi.fn(async () => ({})),
  mount: vi.fn(async () => undefined),
  updateProject: vi.fn(async () => ({})),
  OnMounts: vi.fn(() => () => {}),
  preferencesGet: vi.fn(async () => ({ Preferences: {} })),
  preferencesSave: vi.fn(async () => ({})),
  slashCommandsList: vi.fn(async () => ({ Commands: [] })),
  builtinModesList: vi.fn(async () => ({ Modes: [] })),
  wikiListCards: vi.fn(async () => ({ Cards: [] })),
  agentAccess: vi.fn(async () => ({ Version: 1, Full: true, Items: [] })),
}))

vi.mock('../../gen-clients/aimanager/client', () => ({
  providerList: vi.fn(async () => ({ Items: [] })),
  aggregatorList: vi.fn(async () => ({ Items: [] })),
}))

vi.mock('../../gen-clients/aiaggregator/client', () => ({
  status: vi.fn(async () => ({ UnitCount: 0, Units: [] })),
}))

vi.mock('../../gen-clients/local/client', () => ({
  chatSubmit: vi.fn(async () => ({})),
  listCallables: vi.fn(async () => ({ Items: [] })),
  componentList: vi.fn(async () => ({ Items: [] })),
  componentMount: vi.fn(async () => ({ Mount: {} })),
  componentUnmount: vi.fn(async () => ({ Title: '' })),
  componentSnapshot: vi.fn(async () => ({ Snapshot: { Tools: [] } })),
  thinkingGetRegistry: vi.fn(async () => ({ Entries: [] })),
  thinkingSetLevel: vi.fn(async () => ({})),
  permissionModeSet: vi.fn(async () => {}),
}))

vi.mock('../../application/window-controller', () => ({
  wailsWindowController: {
    isHostMode: () => false,
    isMaximised: async () => false,
  },
}))

vi.mock('./hooks/useAIShellSource', () => ({
  useAIShellSource: () => ({
    envelopes: [],
    emptyState: React.createElement('div', null, 'empty'),
    previewScenarios: [],
    activePreviewScenarioId: null,
    setPreviewScenarioId: vi.fn(),
    isStreaming: false,
    isPaused: false,
    isWaiting: false,
    isPausing: false,
    loading: false,
    loadingMore: false,
    stop: vi.fn(),
    pause: vi.fn(),
    pauseAll: vi.fn(),
    resume: vi.fn(),
    startPreview: vi.fn(),
    resetPreview: vi.fn(),
    dispatchEvent: vi.fn(),
    pushUserMessage: vi.fn(() => 'temp-id'),
    replaceUserMessageId: vi.fn(),
    updateUserMessageIdx: vi.fn(),
    hasMoreHistory: false,
    summarizedBoundary: false,
    discardedBoundary: false,
    loadOlderTurns: vi.fn(),
  }),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true
class ResizeObserverStub {
  observe() {}
  disconnect() {}
  unobserve() {}
}
;(globalThis as any).ResizeObserver = ResizeObserverStub

const flush = async () => {
  await act(async () => { await Promise.resolve() })
}

describe('AIShellLayout independent browser card on the new-tab page', () => {
  let container: HTMLDivElement
  let root: Root

  const renderLayout = async () => {
    root = createRoot(container)
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <AIShellLayout />
        </I18nProvider>,
      )
    })
    for (let i = 0; i < 6; i++) await flush()
  }

  const tabLabels = () => Array.from(container.querySelectorAll('.rpt-tab-label'))
    .map(el => el.textContent ?? '')

  const activateNewTabPage = async () => {
    const newtabTab = Array.from(container.querySelectorAll('.rpt-tab'))
      .find(el => el.textContent?.includes('新标签页'))
    expect(newtabTab).toBeTruthy()
    await act(async () => {
      newtabTab!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    for (let i = 0; i < 4; i++) await flush()
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    // One saved tab-mode instance that is NOT open: the restore effect seeds
    // the 'browser-newtab' page and BrowserView lists this instance as a card.
    listBrowserWindowsMock.mockResolvedValue([
      {
        Config: { Id: 'inst-1', Name: '独立实例', Url: 'https://example.com', Mode: 'tab', Proxy: '', ProxyMode: 'system' },
        Status: { Open: false, Url: '', Title: '' },
      },
    ])
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.clearAllMocks()
  })

  it('creates the independent browser tab when its card is clicked on the new-tab page', async () => {
    await renderLayout()

    expect(tabLabels().some(l => l.includes('新标签页'))).toBe(true)

    await activateNewTabPage()

    // The instance card (not the add-new-instance button) must be rendered.
    const card = Array.from(container.querySelectorAll('.browser-newtab-card'))
      .find(el => !el.classList.contains('browser-newtab-add') && el.textContent?.includes('独立实例'))
    expect(card).toBeTruthy()

    await act(async () => {
      card!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    for (let i = 0; i < 6; i++) await flush()

    // Regression: the self-close of the new-tab page must not clobber the
    // just-queued independent browser tab (stale-closure snapshot overwrite).
    expect(tabLabels().some(l => l.includes('独立实例'))).toBe(true)
    expect(tabLabels().some(l => l.includes('新标签页'))).toBe(false)
    expect(browserManager.openBrowserInstance).toHaveBeenCalledWith('inst-1')
  })
})

describe('AIShellLayout right-panel tab order persistence across restart', () => {
  // Regression for the pre-restore save-clobber: the 500ms debounced save
  // fires while listBrowserWindows() is still pending (rightTabs is empty),
  // and previously wrote rightTabOrder: [], destroying the user's saved drag
  // order. With an unreliable beforeunload (native webview teardown drops the
  // async write), this lost the order on every restart.
  let container: HTMLDivElement
  let root: Root
  let resolveList: (v: any) => void
  let layoutStore: any
  let savePayloads: any[]
  let pendingInstances: any[]

  const flush = async () => {
    await act(async () => { await Promise.resolve() })
  }

  beforeEach(() => {
    vi.useFakeTimers()
    container = document.createElement('div')
    document.body.appendChild(container)
    layoutStore = { rightPanelOpen: true, rightTabOrder: ['browser-inst-B', 'browser-inst-A', 'browser-inst-C'] }
    savePayloads = []
    vi.mocked(getAIShellLayoutState).mockImplementation(async () => ({ ...layoutStore }) as any)
    vi.mocked(saveAIShellLayoutState).mockImplementation(async (s: any) => {
      savePayloads.push(s)
      layoutStore = { ...layoutStore, ...s }
    })
    const mkInst = (id: string, name: string) => ({
      Config: { Id: id, Name: name, Url: `https://${id}.test`, Mode: 'tab', Proxy: '', ProxyMode: 'system' },
      Status: { Open: true, Url: `https://${id}.test`, Title: name },
    })
    pendingInstances = [mkInst('inst-A', '独立A'), mkInst('inst-B', '独立B'), mkInst('inst-C', '独立C')]
    listBrowserWindowsMock.mockReturnValue(new Promise<any[]>(r => { resolveList = (v: any) => r(v) }))
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  const labels = () => Array.from(container.querySelectorAll('.rpt-tab[data-tab-id^="browser-inst"] .rpt-tab-label'))
    .map(e => e.textContent ?? '')

  const render = async () => {
    root = createRoot(container)
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <AIShellLayout />
        </I18nProvider>,
      )
    })
    for (let i = 0; i < 8; i++) await flush()
  }

  it('preserves independent-browser tab order across restart (no pre-restore save clobber)', async () => {
    // --- restart #1: saved order B,A,C; list resolves slowly ---
    await render()
    // Fire the 500ms debounced save while listBrowserWindows is still pending
    // (rightTabs is empty here). This is the frame that used to write [].
    await act(async () => { vi.advanceTimersByTime(600) })
    for (let i = 0; i < 4; i++) await flush()
    expect(layoutStore.rightTabOrder).toEqual(['browser-inst-B', 'browser-inst-A', 'browser-inst-C'])
    // No save may ever persist an empty rightTabOrder.
    expect(savePayloads.every(s => !Array.isArray((s as any).rightTabOrder) || ((s as any).rightTabOrder as string[]).length > 0)).toBe(true)

    // List resolves; restored order follows the saved drag order.
    await act(async () => { resolveList(pendingInstances) })
    for (let i = 0; i < 10; i++) await flush()
    expect(labels()).toEqual(['独立B', '独立A', '独立C'])

    // Crash-close: no beforeunload flush, no further debounce — the persisted
    // order must already be safe from the pre-restore race.
    await act(async () => { root.unmount() })

    // --- restart #2 ---
    container = document.createElement('div')
    document.body.appendChild(container)
    await render()
    await act(async () => { vi.advanceTimersByTime(600) })
    for (let i = 0; i < 4; i++) await flush()
    await act(async () => { resolveList(pendingInstances) })
    for (let i = 0; i < 12; i++) await flush()
    expect(labels()).toEqual(['独立B', '独立A', '独立C'])
  })
})

describe('resolveTabInsertIndex (drag drop lands at the pointer gap)', () => {
  // toIndex is the gap index in the ORIGINAL array resolved from the pointer.
  // Helper translates it to an index in the array after the dragged tab is removed.
  it('dragging right inserts one slot earlier than the raw gap index', () => {
    // [A,B,C], drag A(0), pointer between B and C → gap 2 → insert at 1 in [B,C] → [B,A,C]
    expect(resolveTabInsertIndex(0, 2, 2)).toBe(1)
    // [A,B,C], drag A(0), pointer past C → gap 3 → insert at 2 in [B,C] → [B,C,A]
    expect(resolveTabInsertIndex(0, 3, 2)).toBe(2)
    // [A,B,C], drag B(1), pointer past C → gap 3 → insert at 2 in [A,C] → [A,C,B]
    expect(resolveTabInsertIndex(1, 3, 2)).toBe(2)
  })

  it('dragging left keeps the gap index unchanged', () => {
    // [A,B,C], drag C(2), pointer before A → gap 0 → insert at 0 in [A,B] → [C,A,B]
    expect(resolveTabInsertIndex(2, 0, 2)).toBe(0)
    // [A,B,C], drag C(2), pointer between A and B → gap 1 → insert at 1 in [A,B] → [A,C,B]
    expect(resolveTabInsertIndex(2, 1, 2)).toBe(1)
    // [A,B,C], drag B(1), pointer before A → gap 0 → insert at 0 in [A,C] → [B,A,C]
    expect(resolveTabInsertIndex(1, 0, 2)).toBe(0)
  })

  it('no-op drops resolve back to the original position', () => {
    // gap at the dragged tab itself, or immediately after it: no movement
    expect(resolveTabInsertIndex(0, 0, 2)).toBe(0)
    expect(resolveTabInsertIndex(0, 1, 2)).toBe(0)
    expect(resolveTabInsertIndex(2, 2, 2)).toBe(2)
    expect(resolveTabInsertIndex(2, 3, 2)).toBe(2)
  })

  it('clamps out-of-range indices into the post-removal array', () => {
    expect(resolveTabInsertIndex(0, 99, 2)).toBe(2)
    expect(resolveTabInsertIndex(2, 99, 2)).toBe(2)
    expect(resolveTabInsertIndex(0, -1, 2)).toBe(0)
  })
})

describe('AIShellLayout global browser tab favicon', () => {
  let container: HTMLDivElement
  let root: Root

  const flush = async () => {
    await act(async () => { await Promise.resolve() })
  }

  const renderLayout = async () => {
    root = createRoot(container)
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <AIShellLayout />
        </I18nProvider>,
      )
    })
    for (let i = 0; i < 8; i++) await flush()
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    wailsHandlers.clear()
    // Restore one saved global browser tab as the active right-panel tab so
    // the real BrowserView mounts for it and its wails event handlers run.
    vi.mocked(getAIShellLayoutState).mockImplementation(async () => ({
      rightPanelOpen: true,
      browserTabs: [{
        id: 'browser-global-1',
        kind: 'global',
        sessionId: 'global-abc',
        url: 'https://example.com',
        label: 'Example',
      }],
      activeBrowserTabId: 'browser-global-1',
    }) as any)
    listBrowserWindowsMock.mockResolvedValue([])
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.clearAllMocks()
    vi.mocked(getAIShellLayoutState).mockImplementation(async () => ({ rightPanelOpen: true }) as any)
  })

  it('shows the favicon from page-info and keeps it across a state title update', async () => {
    await renderLayout()

    await emitWails('browser:page-info:global-abc', {
      u: 'https://example.com',
      t: 'Example',
      ic: 'https://example.com/favicon.ico',
    })
    await flush()
    const img = container.querySelector('.rpt-tab-favicon') as HTMLImageElement
    expect(img).toBeTruthy()
    expect(img.getAttribute('src')).toBe('https://example.com/favicon.ico')

    // Regression: Go re-emits the normalized state (browser:state)
    // around page-info; the title projection must not wipe the favicon.
    await emitWails('browser:state:global-abc', {
      status: 'loaded',
      confirmedURL: 'https://example.com',
      title: 'Example',
    })
    await flush()
    expect(container.querySelector('.rpt-tab-favicon')).toBeTruthy()
  })

  it('falls back to the globe icon when the favicon URL fails to load', async () => {
    await renderLayout()

    await emitWails('browser:page-info:global-abc', {
      u: 'https://example.com',
      t: 'Example',
      ic: 'https://example.com/favicon.ico',
    })
    await flush()
    const img = container.querySelector('.rpt-tab-favicon') as HTMLImageElement
    expect(img).toBeTruthy()

    await act(async () => {
      img.dispatchEvent(new Event('error'))
    })
    expect(container.querySelector('.rpt-tab-favicon')).toBeNull()

    // A re-sent identical favicon (periodic backend re-send) must not
    // retry-flicker; only a new URL retries.
    await emitWails('browser:page-info:global-abc', {
      u: 'https://example.com',
      t: 'Example',
      ic: 'https://example.com/favicon.ico',
    })
    await flush()
    expect(container.querySelector('.rpt-tab-favicon')).toBeNull()

    await emitWails('browser:page-info:global-abc', {
      u: 'https://example.com/next',
      t: 'Next',
      ic: 'https://example.com/next-icon.png',
    })
    await flush()
    const retried = container.querySelector('.rpt-tab-favicon') as HTMLImageElement
    expect(retried).toBeTruthy()
    expect(retried.getAttribute('src')).toBe('https://example.com/next-icon.png')
  })
})

describe('AIShellLayout composer drawer mode on non-conversation tabs in the expanded right panel', () => {
  // Regression: with the panel expanded, the conversation stream is only
  // visible on the mode:conversation tab. Browser/app tabs kept the composer
  // in floating (no-drawer) mode because contentMode stayed 'conversation'
  // even though no conversation was visible behind the page.
  let container: HTMLDivElement
  let root: Root

  const flush = async () => {
    await act(async () => { await Promise.resolve() })
  }

  const renderLayout = async () => {
    root = createRoot(container)
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <AIShellLayout />
        </I18nProvider>,
      )
    })
    for (let i = 0; i < 8; i++) await flush()
  }

  const clickSelector = async (selector: string) => {
    const el = container.querySelector(selector) as HTMLElement
    expect(el).toBeTruthy()
    await act(async () => {
      el.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    for (let i = 0; i < 4; i++) await flush()
  }

  const clickText = async (selector: string, text: string) => {
    const el = Array.from(container.querySelectorAll(selector))
      .find(node => node.textContent?.includes(text)) as HTMLElement
    expect(el).toBeTruthy()
    await act(async () => {
      el.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    for (let i = 0; i < 4; i++) await flush()
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    wailsHandlers.clear()
    resetAgentInfoStore()
    resetAgentListStore()
    // One project + one agent so the global composer renders once the agent
    // is selected (otherwise the new-chat landing page takes over).
    const project = { ActorId: 'project-1', Name: 'Project One', Path: '/workspace/project-1', Root: true, Mounts: [] }
    const agent = {
      Id: 'agent-1', ActorId: 'actor-1', AggregatorActorId: 'agg-1', ProjectId: 'project-1',
      DisplayName: 'Existing Agent', AgentKind: 'coder', LoadState: 'loaded',
    }
    vi.mocked(workspaceClient.listProject).mockResolvedValue({ Items: [project] } as any)
    vi.mocked(workspaceClient.listAgents).mockResolvedValue({ Items: [agent] } as any)
    vi.mocked(workspaceClient.agentListState).mockResolvedValue({ Version: 1, Full: true, Items: [agent] } as any)
    // A restored global browser tab is the active right-panel tab.
    vi.mocked(getAIShellLayoutState).mockImplementation(async () => ({
      rightPanelOpen: true,
      browserTabs: [{
        id: 'browser-global-1', kind: 'global', sessionId: 'global-abc',
        url: 'https://example.com', label: 'Example',
      }],
      activeBrowserTabId: 'browser-global-1',
    }) as any)
    listBrowserWindowsMock.mockResolvedValue([])
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.clearAllMocks()
    vi.mocked(workspaceClient.listProject).mockResolvedValue({ Items: [] } as any)
    vi.mocked(workspaceClient.listAgents).mockResolvedValue({ Items: [] } as any)
    vi.mocked(workspaceClient.agentListState).mockResolvedValue({ Version: 1, Full: true, Items: [] } as any)
    vi.mocked(getAIShellLayoutState).mockImplementation(async () => ({ rightPanelOpen: true }) as any)
  })

  it('uses drawer mode on a browser tab but floats on the conversation tab', async () => {
    await renderLayout()
    await clickText('.ai-sidebar-project-item', 'Project One')
    await clickText('.ai-sidebar-session-item', 'Existing Agent')

    // Non-expanded: conversation mode in the content area → floating composer.
    const contentFrame = container.querySelector('.ai-shell-content .composer-frame')
    expect(contentFrame).toBeTruthy()
    expect(contentFrame!.classList.contains('no-drawer')).toBe(true)

    // Expand the right panel; the active tab stays the browser tab.
    await clickSelector('button[title="放大面板"]')

    // The composer moved into the panel bottom slot and must be in drawer
    // mode: the conversation stream is not visible behind a browser page.
    const slotFrame = container.querySelector('.rpt-bottom-slot .composer-frame')
    expect(slotFrame).toBeTruthy()
    expect(slotFrame!.classList.contains('has-drawer')).toBe(true)

    // Switching to the conversation tab restores the floating composer.
    await clickSelector('.rpt-tab[data-tab-id="mode:conversation"]')
    const convFrame = container.querySelector('.rpt-bottom-slot .composer-frame')
    expect(convFrame).toBeTruthy()
    expect(convFrame!.classList.contains('no-drawer')).toBe(true)
  })
})

describe('computeAutoOpenPluginTabs (pure)', () => {
  const app = {
    id: 'demo.app',
    state: 'running',
    icon: '🧩',
    entrypoints: [
      { id: 'home', kind: 'view', title: 'Home', route: '/' },
      { id: 'settings', kind: 'view', title: 'Settings' },
      { id: 'run-tool', kind: 'tool', title: 'Not a view' },
    ],
  }

  it('returns one tab per view entrypoint of a running app', () => {
    const out = computeAutoOpenPluginTabs([app], new Set(), new Set())
    expect(out.map(t => t.id)).toEqual(['plugin-view-demo.app-home', 'plugin-view-demo.app-settings'])
    expect(out[0]!.label).toBe('Home')
    expect(out[0]!.view.pluginID).toBe('demo.app')
    expect(out[0]!.view.route).toBe('/')
    // Missing route falls back to the plugin root.
    expect(out[1]!.view.route).toBe('/')
  })

  it('skips non-running apps, already-open tabs and session-closed tabs', () => {
    const stopped = { ...app, state: 'stopped' }
    expect(computeAutoOpenPluginTabs([stopped], new Set(), new Set())).toEqual([])

    const existing = new Set(['plugin-view-demo.app-home'])
    const closed = new Set(['plugin-view-demo.app-settings'])
    expect(computeAutoOpenPluginTabs([app], existing, closed)).toEqual([])
  })

  it('restricts to the allow-list when one is given (boot restore)', () => {
    const wanted = new Set(['plugin-view-demo.app-settings'])
    const out = computeAutoOpenPluginTabs([app], new Set(), new Set(), wanted)
    expect(out.map(t => t.id)).toEqual(['plugin-view-demo.app-settings'])

    // An empty allow-list restores nothing — not-open apps stay closed.
    expect(computeAutoOpenPluginTabs([app], new Set(), new Set(), new Set())).toEqual([])
  })

  it('carries the bundle color into the auto-opened view for the tab glyph', () => {
    const colored = { ...app, color: '#2563eb' }
    const out = computeAutoOpenPluginTabs([colored], new Set(), new Set())
    expect(out[0]!.view.color).toBe('#2563eb')
  })
})

describe('AIShellLayout registered plugins keep-open in the right panel', () => {
  let container: HTMLDivElement
  let root: Root

  const flush = async () => {
    await act(async () => { await Promise.resolve() })
  }

  const renderLayout = async () => {
    root = createRoot(container)
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <AIShellLayout />
        </I18nProvider>,
      )
    })
    for (let i = 0; i < 6; i++) await flush()
  }

  const upsertApp = async (state: string, generation = 1) => {
    await act(async () => {
      appRegistry.upsert({
        id: 'demo.app', name: 'Demo', runtime: 'spore', state, version: '1',
        generation, entrypoints: [{ id: 'home', kind: 'view', title: 'Demo Home', route: '/' }],
      })
    })
    for (let i = 0; i < 4; i++) await flush()
  }

  // Simulate the appmanager `list` snapshot landing (startAppRegistrySync's
  // initial load): marks the registry loaded and, for a restart scenario,
  // delivers the boot set of already-running apps in one batch.
  const loadRegistry = async (items: any[] = []) => {
    await act(async () => {
      await appRegistry.load({ invoke: async () => ({ Items: items }) } as any)
    })
    for (let i = 0; i < 4; i++) await flush()
  }

  const appItem = (state: string, generation = 1) => ({
    id: 'demo.app', name: 'Demo', runtime: 'spore', state, version: '1',
    generation,
    entrypoints: [
      { id: 'home', kind: 'view', title: 'Demo Home', route: '/' },
      { id: 'settings', kind: 'view', title: 'Demo Settings' },
    ],
  })

  const tabLabels = () => Array.from(container.querySelectorAll('.rpt-tab-label'))
    .map(el => el.textContent ?? '')

  const closePluginTab = async () => {
    const tab = Array.from(container.querySelectorAll('.rpt-tab'))
      .find(el => el.textContent?.includes('Demo Home'))
    expect(tab).toBeTruthy()
    await act(async () => {
      tab!.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }))
    })
    await flush()
    const closeItem = Array.from(container.querySelectorAll('.rpt-context-menu-item'))
      .find(el => el.textContent?.includes('关闭'))
    expect(closeItem).toBeTruthy()
    await act(async () => {
      closeItem!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    for (let i = 0; i < 4; i++) await flush()
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    resetAgentInfoStore()
    resetAgentListStore()
    vi.mocked(getAIShellLayoutState).mockImplementation(async () => ({ rightPanelOpen: true }) as any)
    listBrowserWindowsMock.mockResolvedValue([])
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    appRegistry.clear()
    vi.clearAllMocks()
    vi.mocked(getAIShellLayoutState).mockImplementation(async () => ({ rightPanelOpen: true }) as any)
  })

  it('auto-opens a tab for every view entrypoint of a running registered app', async () => {
    await renderLayout()
    await loadRegistry([])
    expect(tabLabels().some(l => l.includes('Demo Home'))).toBe(false)

    await upsertApp('running')

    expect(tabLabels().some(l => l.includes('Demo Home'))).toBe(true)
  })

  it('does not auto-open tabs for apps that are not running', async () => {
    await renderLayout()
    await loadRegistry([])

    await upsertApp('crashed')

    expect(tabLabels().some(l => l.includes('Demo Home'))).toBe(false)
  })

  it('keeps a session-closed plugin tab closed across later registry updates', async () => {
    await renderLayout()
    await loadRegistry([])
    await upsertApp('running')
    expect(tabLabels().some(l => l.includes('Demo Home'))).toBe(true)

    await closePluginTab()
    expect(tabLabels().some(l => l.includes('Demo Home'))).toBe(false)

    // A registry change (e.g. generation bump on reload) must not resurrect
    // the tab the user closed this session.
    await upsertApp('running', 2)
    expect(tabLabels().some(l => l.includes('Demo Home'))).toBe(false)
  })

  it('restart restores only the plugin tabs recorded as open at shutdown', async () => {
    vi.mocked(getAIShellLayoutState).mockImplementation(async () => ({
      rightPanelOpen: true,
      rightPluginTabs: [{ id: 'plugin-view-demo.app-home', pluginID: 'demo.app', viewID: 'home' }],
    }) as any)
    await renderLayout()

    // Backend re-spawned the app at boot; registry sync delivers it with the
    // initial snapshot. Only the persisted tab may come back.
    await loadRegistry([appItem('running')])

    const labels = tabLabels()
    expect(labels.some(l => l.includes('Demo Home'))).toBe(true)
    expect(labels.some(l => l.includes('Demo Settings'))).toBe(false)
  })

  it('restart does not open running apps whose tabs were not open at shutdown', async () => {
    vi.mocked(getAIShellLayoutState).mockImplementation(async () => ({
      rightPanelOpen: true,
      rightPluginTabs: [],
    }) as any)
    await renderLayout()

    await loadRegistry([appItem('running')])

    expect(tabLabels().some(l => l.includes('Demo'))).toBe(false)
  })

  it('legacy layouts restore plugin ids from the persisted tab order', async () => {
    vi.mocked(getAIShellLayoutState).mockImplementation(async () => ({
      rightPanelOpen: true,
      rightTabOrder: ['plugin-view-demo.app-home', 'browser-global-1'],
    }) as any)
    await renderLayout()

    await loadRegistry([appItem('running')])

    const labels = tabLabels()
    expect(labels.some(l => l.includes('Demo Home'))).toBe(true)
    expect(labels.some(l => l.includes('Demo Settings'))).toBe(false)
  })

  it('an app started mid-session (after the boot snapshot) auto-opens every view', async () => {
    await renderLayout()
    await loadRegistry([])

    await act(async () => {
      appRegistry.upsert(appItem('running'))
    })
    for (let i = 0; i < 4; i++) await flush()

    const labels = tabLabels()
    expect(labels.some(l => l.includes('Demo Home'))).toBe(true)
    expect(labels.some(l => l.includes('Demo Settings'))).toBe(true)
  })
})
