import { act } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'

// Pins the App branch wiring for the crash-recovery overlay (G3): CrashOverlay
// must be mounted in every auth branch (so a pre-login boot crash report is
// never dropped) and, in the logged-in branch, inside BrowserOverlayManager so
// its registration actually drives native browser window hiding.

const hoisted = vi.hoisted(() => ({
  bootReport: null as unknown,
  urlTokenPending: false,
  urlTokenOk: false,
  wailsAutoLoginOk: false,
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
vi.mock('./application/runtime', () => ({ isWails: () => false }))
vi.mock('./application/useKeyboardHeight', () => ({ useKeyboardHeight: vi.fn() }))
vi.mock('./i18n', () => ({ useI18n: () => ({ setLocale: vi.fn() }) }))
vi.mock('./ui/ai/AIShell', () => ({ AIShell: () => <div data-testid="ai-shell" /> }))
vi.mock('./ui/screenshot/ScreenshotOverlay', () => ({ default: () => <div data-testid="screenshot" /> }))
vi.mock('./ui/auth/LoginPage', () => ({ LoginPage: () => <div data-testid="login-page" /> }))
vi.mock('./ui/auth/LoadingOverlay', () => ({ LoadingOverlay: () => <div data-testid="loading-overlay" /> }))
vi.mock('./ui/auth/SplashScreen', () => ({ SplashScreen: () => <div data-testid="splash" /> }))
vi.mock('./application/generated-client', () => ({
  client: {},
  onReconnectStateChange: () => () => {},
  forceReconnectClient: vi.fn(),
  reconnectIfDisconnected: vi.fn(),
  connectClient: async () => {},
  waitForClientReady: async () => {},
  AUTH_CONNECTION_TIMEOUT_MS: 1000,
}))
vi.mock('./gen-clients/workspace/client', () => ({ session: async () => ({ SessionId: 's' }) }))
vi.mock('./ui/ai/hooks/agentListStore', () => ({
  prefetchAgentList: async () => {},
  isAgentListFetched: () => true,
}))
vi.mock('./application/shell-context-prefetch', () => ({
  prefetchShellContext: async () => {},
  isShellContextFetched: () => true,
}))
vi.mock('./application/app-registry', () => ({ appRegistry: { clear: vi.fn() } }))
vi.mock('./application/app-registry-sync', () => ({ startAppRegistrySync: () => () => {} }))
vi.mock('./application/auth-store', () => ({
  tryUrlTokenLogin: () => (hoisted.urlTokenPending ? new Promise(() => {}) : Promise.resolve(hoisted.urlTokenOk)),
  tryWailsAutoLogin: () => Promise.resolve(hoisted.wailsAutoLoginOk),
  isLoggedIn: () => false,
  clearAuth: vi.fn(),
  setupCapacitorTokenBridge: vi.fn(),
  isCapacitorMode: () => false,
  requestCapacitorToken: async () => ({ ok: false }),
}))
// CrashOverlay's own dependencies (kept real so the overlay's DOM proves it
// mounted).
vi.mock('./application/wails-runtime', () => ({
  isWailsAvailable: () => true,
  onWailsEvent: () => () => {},
}))
vi.mock('./bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  BootCrashReport: () => Promise.resolve(hoisted.bootReport),
  AckCrashRecords: () => Promise.resolve(),
}))
vi.mock('./application/wails-bridge', () => ({
  destroyAllBrowserWindows: vi.fn(() => Promise.resolve()),
  notifySplashDone: vi.fn(),
  hideAllBrowserWindows: vi.fn(() => Promise.resolve()),
  showAllBrowserWindows: vi.fn(() => Promise.resolve()),
}))

import { App } from './App'
import { hideAllBrowserWindows } from './application/wails-bridge'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const bootReport = {
  abnormalExit: true,
  records: [{ id: 'rec-1', kind: 'panic', summary: 'boom' }],
  errorsTail: [],
  logTail: [],
}

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  vi.clearAllMocks()
  hoisted.bootReport = bootReport
  hoisted.urlTokenPending = false
  hoisted.urlTokenOk = false
  hoisted.wailsAutoLoginOk = false
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
  // Flush auth + BootCrashReport promise chains and the ensuing renders.
  await act(async () => {})
  await act(async () => {})
}

describe('App crash-overlay branch wiring', () => {
  it('mounts CrashOverlay pre-auth and renders the boot crash report', async () => {
    hoisted.urlTokenPending = true // keep the app in the !authReady branch
    await render()

    expect(container.querySelector('[data-testid="loading-overlay"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="login-page"]')).toBeNull()
    expect(container.querySelector('[data-testid="ai-shell"]')).toBeNull()
    // The pre-login crash report is no longer dropped.
    expect(container.textContent).toContain('Previous session crashed')
    // No manager pre-login: nothing to hide.
    expect(hideAllBrowserWindows).not.toHaveBeenCalled()
  })

  it('mounts CrashOverlay on the pre-login branch', async () => {
    hoisted.urlTokenOk = false
    hoisted.wailsAutoLoginOk = false
    await render()

    expect(container.querySelector('[data-testid="login-page"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="ai-shell"]')).toBeNull()
    expect(container.textContent).toContain('Previous session crashed')
    expect(hideAllBrowserWindows).not.toHaveBeenCalled()
  })

  it('mounts CrashOverlay inside BrowserOverlayManager on the logged-in branch', async () => {
    hoisted.wailsAutoLoginOk = true
    await render()

    expect(container.querySelector('[data-testid="ai-shell"]')).toBeTruthy()
    expect(container.textContent).toContain('Previous session crashed')
    // Inside the manager the boot crash report registers an overlay, which
    // hides the native browser child windows.
    expect(hideAllBrowserWindows).toHaveBeenCalled()
  })
})
