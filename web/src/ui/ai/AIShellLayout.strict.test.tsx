import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AIShellLayout } from './AIShellLayout'
import { I18nProvider } from '../../i18n'
import type { ViewMode } from './model/ai-types'

const mockLayout = { viewMode: 'desktop' as ViewMode, containerRef: vi.fn() }

vi.mock('./hooks/useResponsiveLayout', () => ({
  useResponsiveLayout: () => mockLayout,
}))

vi.mock('../../application/workspace-ui-state', () => ({
  defaultAIShellProjectUIState: { sidebarOrder: [], sidebarPinned: [] },
  getAIShellProjectUIState: vi.fn(async () => ({ sidebarOrder: [], sidebarPinned: [] })),
  saveAIShellProjectUIState: vi.fn(async () => undefined),
  getAIShellLayoutState: vi.fn(async () => ({ rightPanelOpen: true, browserTabs: [{ id: 'browser-1', sessionId: 'global-1', kind: 'global', url: 'https://example.com', label: 'Example' }] })),
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
  getProviderUserAgentVisible: vi.fn(async () => false),
}))

vi.mock('../../application/generated-client', () => ({
  client: {
    events: { onService: vi.fn(() => () => {}) },
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

describe('AIShellLayout strict mode restore', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('sets sessionRestored and restores rightPanelOpen under React StrictMode', async () => {
    root = createRoot(container)
    await act(async () => {
      root.render(
        <React.StrictMode>
          <I18nProvider initialLocale="zh-CN">
            <AIShellLayout />
          </I18nProvider>
        </React.StrictMode>
      )
    })
    await act(async () => { await Promise.resolve() })
    await act(async () => { await Promise.resolve() })
    await act(async () => { await Promise.resolve() })

    const panel = container.querySelector('.rpt-container') as HTMLElement
    expect(panel).toBeTruthy()
    const style = panel.getAttribute('style')
    expect(style).not.toContain('width: 0')
  })
})
