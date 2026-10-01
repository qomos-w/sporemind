import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// The workspace UI model cache must reset on an instance swap: the right-panel
// tab list boot-restores from AiShell.LayoutJson via loadWorkspaceUIModel, and
// a stale cache keeps serving the PREVIOUS instance's layout after a swap.

const eventListeners: Record<string, Array<(e: unknown) => void>> = {}

vi.mock('@wailsio/runtime', () => ({
  Events: {
    On: vi.fn((name: string, handler: (e: unknown) => void) => {
      ;(eventListeners[name] ||= []).push(handler)
      return () => {
        eventListeners[name] = (eventListeners[name] || []).filter((h) => h !== handler)
      }
    }),
  },
}))

vi.mock('../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  SwitchConnection: vi.fn(),
  GetActiveConnection: vi.fn(),
  GetGatewayAddr: vi.fn(async () => '127.0.0.1:18080'),
}))

vi.mock('./generated-client', () => ({
  client: { tag: 'client' },
  createTransport: vi.fn(),
  rebindClient: vi.fn(),
}))

const uiGet = vi.fn()

vi.mock('../gen-clients/workspace/client', () => ({
  uiGet: (...args: unknown[]) => uiGet(...args),
  uiSaveAiShell: vi.fn(),
  uiSaveAiShell_meta: { callable: 'uiSaveAiShell' },
  uiSaveDock: vi.fn(),
  uiSaveDock_meta: { callable: 'uiSaveDock' },
  uiSaveExplorer: vi.fn(),
  uiSaveExplorer_meta: { callable: 'uiSaveExplorer' },
  uiSaveLayout: vi.fn(),
  uiSaveLayout_meta: { callable: 'uiSaveLayout' },
  uiSavePanels: vi.fn(),
  uiSavePanels_meta: { callable: 'uiSavePanels' },
  uiSaveProjectCardBrowser: vi.fn(),
  uiSaveProjectCardBrowser_meta: { callable: 'uiSaveProjectCardBrowser' },
}))

import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import { recomputeRuntime } from './runtime'
import { __resetGatewayCacheForTests } from './gateway'
import {
  __resetInstanceForTests,
  onInstanceSwap,
  registerAuthProviders,
  switchInstance,
} from './instance'
import { loadWorkspaceUIModel, resetWorkspaceUICacheForSwap } from './workspace-ui-client'
import { saveAIShellLayoutState } from './workspace-ui-state'
import { uiSaveAiShell } from '../gen-clients/workspace/client'
import type { ActiveConnection } from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/models'

const origWindow = globalThis.window

function setWindow(search = ''): void {
  const w: Record<string, unknown> = {
    location: {
      href: `http://app/${search}`,
      host: 'app:18080',
      origin: 'http://app:18080',
      protocol: 'http:',
      search,
    },
  }
  w.self = w
  w.top = w
  globalThis.window = w as unknown as Window & typeof globalThis
}

function remoteActive(id: string): ActiveConnection {
  return {
    target: id,
    local: false,
    name: `remote ${id}`,
    conn: { id, name: `remote ${id}`, host: '192.168.1.9', port: 18080, username: 'u', updatedAt: '', hasPassword: true },
  } as unknown as ActiveConnection
}

function model(tag: string): Record<string, unknown> {
  return { WorkspaceId: `ws-${tag}`, Version: 1, AiShell: { LayoutJson: JSON.stringify({ tag }) } }
}

beforeEach(() => {
  setWindow()
  recomputeRuntime()
  vi.clearAllMocks()
  uiGet.mockReset()
  __resetGatewayCacheForTests()
  __resetInstanceForTests()
  // __resetInstanceForTests drops import-time swap listeners (including the
  // production registration inside workspace-ui-client) — re-wire it here so
  // the swap fires the cache reset exactly as it does in the app.
  onInstanceSwap(resetWorkspaceUICacheForSwap)
  resetWorkspaceUICacheForSwap()
  registerAuthProviders({
    authenticateLocal: vi.fn(async () => {}),
    authenticateRemote: vi.fn(async () => {}),
    snapshotAuthState: vi.fn(() => ({})),
    restoreAuthState: vi.fn(),
  } as never)
  vi.mocked(desktop.SwitchConnection).mockResolvedValue(undefined)
  vi.mocked(desktop.GetActiveConnection).mockResolvedValue(remoteActive('c1'))
  for (const k of Object.keys(eventListeners)) delete eventListeners[k]
})

afterEach(() => {
  globalThis.window = origWindow
  recomputeRuntime()
  vi.restoreAllMocks()
})

describe('workspace UI model cache across instance swaps', () => {
  it('serves the cache until a swap, then re-fetches for the new instance', async () => {
    uiGet.mockResolvedValueOnce(model('local')).mockResolvedValueOnce(model('remote'))

    const first = await loadWorkspaceUIModel()
    const cached = await loadWorkspaceUIModel()
    expect(uiGet).toHaveBeenCalledTimes(1)
    expect(cached).toBe(first)

    await switchInstance('c1')

    const afterSwap = await loadWorkspaceUIModel()
    expect(uiGet).toHaveBeenCalledTimes(2)
    expect(afterSwap.WorkspaceId).toBe('ws-remote')
  })

  it('re-fetches again when switching back to local', async () => {
    uiGet.mockResolvedValue(model('whoever'))
    await loadWorkspaceUIModel()
    await switchInstance('c1')
    await loadWorkspaceUIModel()

    vi.mocked(desktop.GetActiveConnection).mockResolvedValue({
      target: 'local',
      local: true,
      name: 'local',
      conn: null,
    } as unknown as ActiveConnection)
    await switchInstance('local')
    await loadWorkspaceUIModel()

    expect(uiGet).toHaveBeenCalledTimes(3)
  })

  it('guest sessions never persist UI state; local sessions do', async () => {
    uiGet.mockResolvedValue(model('local'))
    await loadWorkspaceUIModel()

    await switchInstance('c1')
    await saveAIShellLayoutState({ sidebarVisible: true })
    expect(uiSaveAiShell).not.toHaveBeenCalled()

    vi.mocked(desktop.GetActiveConnection).mockResolvedValue({
      target: 'local',
      local: true,
      name: 'local',
      conn: null,
    } as unknown as ActiveConnection)
    await switchInstance('local')
    await saveAIShellLayoutState({ sidebarVisible: true })
    expect(uiSaveAiShell).toHaveBeenCalledTimes(1)
  })
})
