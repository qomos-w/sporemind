import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AIShellLayout } from './AIShellLayout'
import { I18nProvider } from '../../i18n'
import type { ViewMode } from './model/ai-types'
import * as sshmanagerClient from '../../gen-clients/sshmanager/client'

const mockLayout = { viewMode: 'desktop' as ViewMode, containerRef: vi.fn() }

vi.mock('./hooks/useResponsiveLayout', () => ({
  useResponsiveLayout: () => mockLayout,
}))

vi.mock('../../application/workspace-ui-state', () => ({
  defaultAIShellProjectUIState: { sidebarOrder: [], sidebarPinned: [] },
  getAIShellProjectUIState: vi.fn(async () => ({ sidebarOrder: [], sidebarPinned: [] })),
  saveAIShellProjectUIState: vi.fn(async () => undefined),
  getAIShellLayoutState: vi.fn(async () => ({
    rightPanelOpen: true,
    rightSshTabs: [
      { id: 'ssh-host-1', hostId: 'host-1', hostName: 'Host One' },
      { id: 'ssh-host-deleted', hostId: 'host-deleted', hostName: 'Deleted Host' },
    ],
  })),
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
  sessionList: vi.fn(),
  shellOpen: vi.fn(),
  shellClose: vi.fn(),
  OnSshManagerEvent: vi.fn(() => () => {}),
}))

// Stub SshSessionView so xterm.js / canvas are not required under jsdom.
vi.mock('../views/SshSessionView', () => ({
  SshSessionView: () => <div data-testid="ssh-session-view-stub" />,
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

const flush = async () => {
  await act(async () => { await Promise.resolve() })
}

describe('AIShellLayout SSH tab restore', () => {
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
    // Flush enough microtasks for the SSH restore IIFE (hostList → shellOpen →
    // setRightTabs) to settle and for React to re-render.
    await flush()
    await flush()
    await flush()
    await flush()
    await flush()
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    vi.mocked(sshmanagerClient.hostList).mockResolvedValue({
      Items: [
        { Id: 'host-1', Name: 'Host One', Host: '1.1.1.1', Port: 22, User: 'root', AuthMethod: 'password', Group: '', HasCredential: true, AgentInvisible: false },
      ],
      Groups: [],
    })
    vi.mocked(sshmanagerClient.shellOpen).mockResolvedValue({
      SessionId: 'fresh-session-1',
      Connected: true,
    })
    // Default: no live sessions — restore falls back to shellOpen.
    vi.mocked(sshmanagerClient.sessionList).mockResolvedValue({ Items: [] })
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.clearAllMocks()
  })

  it('reconnects persisted SSH tabs via shellOpen for existing hosts', async () => {
    await renderLayout()

    expect(sshmanagerClient.hostList).toHaveBeenCalledTimes(1)
    // Only host-1 still exists; host-deleted must be skipped.
    expect(sshmanagerClient.shellOpen).toHaveBeenCalledTimes(1)
    expect(sshmanagerClient.shellOpen).toHaveBeenCalledWith(
      expect.anything(),
      { HostId: 'host-1', InitialCols: 80, InitialRows: 24 },
    )

    // The restored tab should appear in the right-panel tab bar.
    const tab = Array.from(container.querySelectorAll('.rpt-tab-label'))
      .find(el => el.textContent?.includes('Host One'))
    expect(tab).toBeTruthy()
  })

  it('skips SSH tabs whose host was deleted', async () => {
    vi.mocked(sshmanagerClient.hostList).mockResolvedValue({ Items: [], Groups: [] })

    await renderLayout()

    expect(sshmanagerClient.shellOpen).not.toHaveBeenCalled()
    const tab = Array.from(container.querySelectorAll('.rpt-tab-label'))
      .find(el => el.textContent?.includes('Deleted Host'))
    expect(tab).toBeFalsy()
  })

  it('skips SSH tabs whose reconnect fails', async () => {
    vi.mocked(sshmanagerClient.shellOpen).mockResolvedValue({
      SessionId: '',
      Connected: false,
    })

    await renderLayout()

    expect(sshmanagerClient.shellOpen).toHaveBeenCalledTimes(1)
    const tab = Array.from(container.querySelectorAll('.rpt-tab-label'))
      .find(el => el.textContent?.includes('Host One'))
    expect(tab).toBeFalsy()
  })

  it('skips SSH tabs whose shellOpen throws', async () => {
    vi.mocked(sshmanagerClient.shellOpen).mockRejectedValue(new Error('network unreachable'))

    await renderLayout()

    expect(sshmanagerClient.shellOpen).toHaveBeenCalledTimes(1)
    const tab = Array.from(container.querySelectorAll('.rpt-tab-label'))
      .find(el => el.textContent?.includes('Host One'))
    expect(tab).toBeFalsy()
  })

  it('adopts the live account session instead of opening a duplicate', async () => {
    // Another client (or the agent) already holds a connected session for
    // host-1: reload must reattach to it, not shellOpen a second session that
    // would kill and replace the other client's terminal.
    vi.mocked(sshmanagerClient.sessionList).mockResolvedValue({
      Items: [
        { SessionId: 'live-s1', HostId: 'host-1', HostName: 'Host One', HostAddr: '1.1.1.1', User: 'root', Connected: true },
      ],
    })

    await renderLayout()

    expect(sshmanagerClient.shellOpen).not.toHaveBeenCalled()
    const tab = Array.from(container.querySelectorAll('.rpt-tab-label'))
      .find(el => el.textContent?.includes('Host One'))
    expect(tab).toBeTruthy()
  })

  it('opens tabs for live sessions of hosts not in persisted tabs (account-level alignment)', async () => {
    // host-2 has a live session but was never a persisted tab on this client:
    // strict alignment shows every account session on every client.
    vi.mocked(sshmanagerClient.hostList).mockResolvedValue({
      Items: [
        { Id: 'host-1', Name: 'Host One', Host: '1.1.1.1', Port: 22, User: 'root', AuthMethod: 'password', Group: '', HasCredential: true, AgentInvisible: false },
        { Id: 'host-2', Name: 'Host Two', Host: '2.2.2.2', Port: 22, User: 'root', AuthMethod: 'password', Group: '', HasCredential: true, AgentInvisible: false },
      ],
      Groups: [],
    })
    vi.mocked(sshmanagerClient.sessionList).mockResolvedValue({
      Items: [
        { SessionId: 'live-s2', HostId: 'host-2', HostName: 'Host Two', HostAddr: '2.2.2.2', User: 'root', Connected: true },
      ],
    })

    await renderLayout()

    // host-1 (persisted, no live session) opens fresh; host-2 adopts live-s2.
    expect(sshmanagerClient.shellOpen).toHaveBeenCalledTimes(1)
    expect(sshmanagerClient.shellOpen).toHaveBeenCalledWith(
      expect.anything(),
      { HostId: 'host-1', InitialCols: 80, InitialRows: 24 },
    )
    const tab2 = Array.from(container.querySelectorAll('.rpt-tab-label'))
      .find(el => el.textContent?.includes('Host Two'))
    expect(tab2).toBeTruthy()
  })

  it('ignores disconnected live sessions (no tab, no adoption)', async () => {
    vi.mocked(sshmanagerClient.sessionList).mockResolvedValue({
      Items: [
        { SessionId: 'dead-s1', HostId: 'host-1', HostName: 'Host One', HostAddr: '1.1.1.1', User: 'root', Connected: false },
      ],
    })

    await renderLayout()

    // Dead session cannot be adopted: host-1 still opens fresh.
    expect(sshmanagerClient.shellOpen).toHaveBeenCalledTimes(1)
  })
})

describe('AIShellLayout ssh_manager_event close_session', () => {
  let container: HTMLDivElement
  let root: Root
  let emitEvent: ((event: { Kind: string; SessionId?: string; HostId?: string; HostName?: string }) => void) | undefined

  const renderLayout = async () => {
    root = createRoot(container)
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <AIShellLayout />
        </I18nProvider>,
      )
    })
    for (let i = 0; i < 5; i++) {
      await act(async () => { await Promise.resolve() })
    }
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    emitEvent = undefined
    vi.mocked(sshmanagerClient.OnSshManagerEvent).mockImplementation(((_client: unknown, cb: never) => {
      emitEvent = cb as typeof emitEvent
      return () => {}
    }) as never)
    vi.mocked(sshmanagerClient.hostList).mockResolvedValue({
      Items: [
        { Id: 'host-1', Name: 'Host One', Host: '1.1.1.1', Port: 22, User: 'root', AuthMethod: 'password', Group: '', HasCredential: true, AgentInvisible: false },
      ],
      Groups: [],
    })
    vi.mocked(sshmanagerClient.sessionList).mockResolvedValue({ Items: [] })
    vi.mocked(sshmanagerClient.shellClose).mockResolvedValue({} as never)
    vi.mocked(sshmanagerClient.shellOpen).mockResolvedValue({
      SessionId: 'fresh-session-1',
      Connected: true,
    })
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.clearAllMocks()
  })

  it('removes the tab when another client closes the session', async () => {
    await renderLayout()

    // Restore opened host-1 with fresh-session-1.
    const tabBefore = Array.from(container.querySelectorAll('.rpt-tab-label'))
      .find(el => el.textContent?.includes('Host One'))
    expect(tabBefore).toBeTruthy()

    expect(emitEvent).toBeTruthy()
    await act(async () => {
      emitEvent!({ Kind: 'close_session', SessionId: 'fresh-session-1', HostId: 'host-1', HostName: 'Host One' })
    })
    await act(async () => { await Promise.resolve() })

    const tabAfter = Array.from(container.querySelectorAll('.rpt-tab-label'))
      .find(el => el.textContent?.includes('Host One'))
    expect(tabAfter).toBeFalsy()
  })

  it('keeps the tab when it has already moved to a newer session', async () => {
    await renderLayout()

    // Tab already refreshed to a reconnected session id: the stale
    // close_session for the OLD id must not remove it.
    expect(emitEvent).toBeTruthy()
    await act(async () => {
      emitEvent!({ Kind: 'open_session', SessionId: 'reconnected-s2', HostId: 'host-1', HostName: 'Host One' })
    })
    await act(async () => { await Promise.resolve() })
    await act(async () => {
      emitEvent!({ Kind: 'close_session', SessionId: 'fresh-session-1', HostId: 'host-1', HostName: 'Host One' })
    })
    await act(async () => { await Promise.resolve() })

    const tab = Array.from(container.querySelectorAll('.rpt-tab-label'))
      .find(el => el.textContent?.includes('Host One'))
    expect(tab).toBeTruthy()
  })

  it('ignores close_session for unknown session ids', async () => {
    await renderLayout()

    expect(emitEvent).toBeTruthy()
    await act(async () => {
      emitEvent!({ Kind: 'close_session', SessionId: 'never-existed', HostId: 'host-1', HostName: 'Host One' })
    })
    await act(async () => { await Promise.resolve() })

    const tab = Array.from(container.querySelectorAll('.rpt-tab-label'))
      .find(el => el.textContent?.includes('Host One'))
    expect(tab).toBeTruthy()
  })
})
