import { act } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'

// Reproduces the reported bug: after switching to a remote instance the right
// sidebar kept the LOCAL plugin tabs — i.e. the App Registry sync effect did
// not re-run on the instance swap. Mounts the real App + real instance.ts and
// drives switchInstance('conn-1') through the registered (mocked) auth
// providers, asserting the registry is cleared and re-synced for the new
// instance.

const hoisted = vi.hoisted(() => ({
  wailsAutoLoginOk: true,
  startSyncCalls: [] as unknown[],
}))

vi.mock('@qomos/sporemind-shell', () => ({ installConsolePatch: () => true }))
vi.mock('@qomos/sporemind-theme', () => ({ applyTheme: vi.fn() }))
vi.mock('./application/theme-persist', () => ({
  initialTheme: () => ({ mode: 'dark' }),
  loadTheme: async () => ({ mode: 'dark' }),
}))
vi.mock('./application/app-background', () => ({
  applyAppBackground: vi.fn(),
  loadAppBackground: async () => null,
}))
vi.mock('./application/locale-persist', () => ({ loadLocale: async () => null }))
vi.mock('./ui/lib/error-capture', () => ({ enableErrorCapture: vi.fn() }))
vi.mock('./application/console-log-persist', () => ({ installConsoleLogPersist: vi.fn() }))
vi.mock('./ui/inspector/agentPromptInspectorRegistration', () => ({}))
vi.mock('./application/runtime', () => ({ isWails: () => false, isIframe: () => false }))
vi.mock('./application/useKeyboardHeight', () => ({ useKeyboardHeight: vi.fn() }))
vi.mock('./i18n', () => ({ useI18n: () => ({ setLocale: vi.fn() }) }))
vi.mock('./ui/ai/AIShell', () => ({ AIShell: () => <div data-testid="ai-shell" /> }))
vi.mock('./ui/screenshot/ScreenshotOverlay', () => ({ default: () => <div data-testid="screenshot" /> }))
vi.mock('./ui/auth/LoginPage', () => ({ LoginPage: () => <div data-testid="login-page" /> }))
vi.mock('./ui/auth/LoadingOverlay', () => ({ LoadingOverlay: () => <div data-testid="loading-overlay" /> }))
vi.mock('./ui/auth/SplashScreen', () => ({ SplashScreen: () => <div data-testid="splash" /> }))
vi.mock('./gen-clients/workspace/client', () => ({ session: async () => ({ SessionId: 's' }) }))
vi.mock('./ui/ai/hooks/agentListStore', () => ({
  prefetchAgentList: async () => {},
  isAgentListFetched: () => true,
}))
vi.mock('./application/shell-context-prefetch', () => ({
  prefetchShellContext: async () => {},
  isShellContextFetched: () => true,
}))
vi.mock('./application/app-registry', () => ({
  appRegistry: { clear: vi.fn(), upsert: vi.fn(), remove: vi.fn() },
}))
vi.mock('./application/app-registry-sync', () => ({
  startAppRegistrySync: (client: unknown) => {
    hoisted.startSyncCalls.push(client)
    return () => {}
  },
}))
vi.mock('./application/auth-store', () => ({
  tryUrlTokenLogin: () => Promise.resolve(false),
  tryWailsAutoLogin: () => Promise.resolve(hoisted.wailsAutoLoginOk),
  tryRemoteAutoLogin: async () => false,
  authenticateLocal: async () => true,
  authenticateRemote: async () => true,
  snapshotAuthState: vi.fn(() => ({})),
  restoreAuthState: vi.fn(),
  isLoggedIn: () => false,
  clearAuth: vi.fn(),
  setupCapacitorTokenBridge: vi.fn(),
  isCapacitorMode: () => false,
  requestCapacitorToken: async () => ({ ok: false }),
}))
vi.mock('./application/wails-runtime', () => ({
  isWailsAvailable: () => false,
  onWailsEvent: () => () => {},
}))
vi.mock('./bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  BootCrashReport: () => Promise.resolve(null),
  AckCrashRecords: () => Promise.resolve(),
  SwitchConnection: () => Promise.resolve(),
  GetActiveConnection: () =>
    Promise.resolve({
      target: 'conn-1',
      local: false,
      name: 'Remote box',
      conn: { id: 'conn-1', name: 'Remote box', host: '10.0.0.5', port: 18080 },
    }),
}))
vi.mock('./application/wails-bridge', () => ({
  destroyAllBrowserWindows: vi.fn(() => Promise.resolve()),
  notifySplashDone: vi.fn(),
  hideAllBrowserWindows: vi.fn(() => Promise.resolve()),
  showAllBrowserWindows: vi.fn(() => Promise.resolve()),
}))
vi.mock('@qomos/gospore-client', () => ({
  GosporeClient: class {
    constructor(public transport: unknown) {}
  },
}))
vi.mock('./application/generated-client', () => ({
  // Plain object mock: `client` cannot live-rebind here, but the swap path
  // calls rebindClient (mocked) — this test only proves the App effect re-runs
  // on the instance change.
  client: { tag: 'initial' },
  createTransport: () => ({ tag: 'transport' }),
  getLocalGatewayClient: () => ({ tag: 'local' }),
  rebindClient: vi.fn(),
  onReconnectStateChange: () => () => {},
  forceReconnectClient: vi.fn(),
  reconnectIfDisconnected: vi.fn(),
  connectClient: async () => {},
  waitForClientReady: async () => {},
  AUTH_CONNECTION_TIMEOUT_MS: 1000,
}))

import { App } from './App'
import {
  switchInstance,
  getActiveInstanceId,
  registerAuthProviders,
  __resetInstanceForTests,
} from './application/instance'
import { rebindClient } from './application/generated-client'
import { appRegistry } from './application/app-registry'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  vi.clearAllMocks()
  __resetInstanceForTests()
  // App.tsx registered its providers at module-load time; the reset above
  // cleared them, so re-register the (mocked) ones the swap will call.
  registerAuthProviders({
    authenticateLocal: async () => {},
    authenticateRemote: async () => {},
  })
  hoisted.startSyncCalls.length = 0
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => {
    root.unmount()
  })
  container.remove()
})

async function render() {
  await act(async () => {
    root.render(<App />)
  })
  await act(async () => {})
  await act(async () => {})
}

describe('instance swap re-syncs the app registry', () => {
  it('re-runs startAppRegistrySync after switching to a remote instance', async () => {
    await render()
    expect(container.querySelector('[data-testid="ai-shell"]')).toBeTruthy()
    expect(hoisted.startSyncCalls).toHaveLength(1)

    await act(async () => {
      await switchInstance('conn-1')
    })
    // Flush the re-render + effect triggered by the instance id change.
    await act(async () => {})

    expect(getActiveInstanceId()).toBe('conn-1')
    expect(rebindClient).toHaveBeenCalled()
    expect(hoisted.startSyncCalls).toHaveLength(2)
    expect(appRegistry.clear).toHaveBeenCalled()
  })
})
