import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { PluginSettingsSection, pluginStateView, isAppOpenable, type PluginStateView } from './PluginSettingsSection'
import { appRegistry } from '../../../application/app-registry'
import type { AppEntry } from '../../../application/app-registry'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  pluginLoad: vi.fn(),
  pluginUnload: vi.fn(),
  unregister: vi.fn(),
  selectAndPreviewZip: vi.fn(),
  selectAndPreviewFolder: vi.fn(),
  confirmInstall: vi.fn(),
  grantConsentAndRetry: vi.fn(),
  closePreview: vi.fn(),
  closeConsent: vi.fn(),
  handleBrowserFileSelect: vi.fn(),
  grantHostCapabilities: vi.fn(),
  revokeHostCapabilities: vi.fn(),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/appmanager/client', () => ({
  pluginLoad: hoisted.pluginLoad,
  pluginUnload: hoisted.pluginUnload,
  unregister: hoisted.unregister,
  installLocal: vi.fn(),
  capabilityGrant: vi.fn(),
}))

vi.mock('../hooks/useLocalAppInstall', () => ({
  useLocalAppInstall: () => ({
    state: {
      previewOpen: false,
      previewManifest: null,
      previewPath: null,
      previewPackageData: null,
      consentOpen: false,
      consent: null,
      loading: false,
      error: null,
      success: null,
    },
    fileInputRef: { current: null },
    selectAndPreviewZip: hoisted.selectAndPreviewZip,
    selectAndPreviewFolder: hoisted.selectAndPreviewFolder,
    handleBrowserFileSelect: hoisted.handleBrowserFileSelect,
    confirmInstall: hoisted.confirmInstall,
    grantConsentAndRetry: hoisted.grantConsentAndRetry,
    closePreview: hoisted.closePreview,
    closeConsent: hoisted.closeConsent,
    reset: vi.fn(),
  }),
}))

vi.mock('../../components/InstallPreviewDialog', () => ({
  InstallPreviewDialog: () => null,
}))

vi.mock('../../../application/capability-catalog', () => ({
  getCapabilityInfo: vi.fn((id: string) => ({
    id,
    riskLevel: 'low',
    titleKey: `capability.${id}.title`,
    descriptionKey: `capability.${id}.description`,
  })),
  KNOWN_CAPABILITY_IDS: ['fs.read', 'fs.write', 'llm.invoke'],
  CAPABILITY_RISK_LEVEL: { 'fs.read': 'low', 'fs.write': 'high', 'llm.invoke': 'medium' },
}))

const pluginEntry = (id: string, state: string, error?: string): AppEntry => ({
  id,
  runtime: 'native',
  state,
  version: '1.0.0',
  entrypoints: [],
  error,
})

const keyForRetry: Record<string, 'load' | 'unload'> = {
  failed: 'load',
  unload_failed: 'unload',
}

describe('pluginStateView matrix', () => {
  const cases: Array<[string, Partial<PluginStateView>]> = [
    ['running', { action: 'unload', blocked: false, restartBadge: null, busy: false, retry: null, errorLabel: null }],
    ['stopped', { action: 'load', blocked: false, restartBadge: null, busy: false, retry: null, errorLabel: null }],
    ['unload_pending', { action: null, blocked: true, restartBadge: 'unload_pending', busy: false, retry: null, errorLabel: null }],
    ['restart_pending', { action: null, blocked: true, restartBadge: 'restart_pending', busy: false, retry: null, errorLabel: null }],
    ['failed', { action: null, blocked: false, restartBadge: null, busy: false, retry: 'load', errorLabel: 'settings.plugins.state.failed' }],
    ['unload_failed', { action: null, blocked: false, restartBadge: null, busy: false, retry: 'unload', errorLabel: 'settings.plugins.state.unload_failed' }],
    ['unloading', { action: null, blocked: true, restartBadge: null, busy: true, retry: null, errorLabel: null }],
    ['protocol_cleanup_pending', { action: null, blocked: true, restartBadge: null, busy: true, retry: null, errorLabel: null }],
    ['cleanup_pending', { action: null, blocked: true, restartBadge: null, busy: true, retry: null, errorLabel: null }],
    ['unknown-state', { action: 'load', blocked: false, restartBadge: null, busy: false, retry: null, errorLabel: null }],
  ]

  it.each(cases)('maps %s', (state, expected) => {
    expect(pluginStateView(state)).toEqual(expected)
  })
})

describe('PluginSettingsSection state rendering and button enablement', () => {
  let container: HTMLDivElement
  let root: Root

  const buttons = () => Array.from(container.querySelectorAll('button'))
  const buttonByText = (text: string) => buttons().find(b => b.textContent?.includes(text))
  const disabled = (btn: HTMLButtonElement | undefined) => btn ? btn.disabled : (() => { throw new Error('button not found') })()

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    vi.spyOn(appRegistry, 'load').mockResolvedValue(undefined)
    appRegistry.clear()
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
    appRegistry.clear()
  })

  const renderSection = (props: { developerMode?: boolean; onOpenAppView?: (app: AppEntry) => void } = {}) => act(() => { root.render(<PluginSettingsSection developerMode={props.developerMode} onOpenAppView={props.onOpenAppView} />) })

  it('running: unload enabled, load disabled, no badge', () => {
    act(() => { appRegistry.upsert(pluginEntry('app.r', 'running')) })
    renderSection()
    expect(disabled(buttonByText('settings.plugins.load'))).toBe(true)
    expect(disabled(buttonByText('settings.plugins.unload'))).toBe(false)
    expect(container.textContent).not.toContain('settings.plugins.restartHint')
    expect(container.textContent).not.toContain('settings.plugins.state.restart_pending')
  })

  it('stopped: load enabled, unload disabled', () => {
    act(() => { appRegistry.upsert(pluginEntry('app.s', 'stopped')) })
    renderSection()
    expect(disabled(buttonByText('settings.plugins.load'))).toBe(false)
    expect(disabled(buttonByText('settings.plugins.unload'))).toBe(true)
  })

  it('unload_pending: all buttons disabled + restart hint badge', () => {
    act(() => { appRegistry.upsert(pluginEntry('app.u', 'unload_pending')) })
    renderSection()
    expect(disabled(buttonByText('settings.plugins.load'))).toBe(true)
    expect(disabled(buttonByText('settings.plugins.unload'))).toBe(true)
    expect(container.textContent).toContain('settings.plugins.restartHint')
  })

  it('restart_pending: all buttons disabled + restart badge', () => {
    act(() => { appRegistry.upsert(pluginEntry('app.rp', 'restart_pending')) })
    renderSection()
    expect(disabled(buttonByText('settings.plugins.load'))).toBe(true)
    expect(disabled(buttonByText('settings.plugins.unload'))).toBe(true)
    expect(container.textContent).toContain('settings.plugins.state.restart_pending')
  })

  it.each([
    ['unloading', 'unloading'],
    ['protocol_cleanup_pending', 'protocol_cleanup_pending'],
    ['cleanup_pending', 'cleanup_pending'],
  ])('%s: spinner shown, all buttons disabled', (_label, state) => {
    act(() => { appRegistry.upsert(pluginEntry('app.x', state)) })
    renderSection()
    expect(container.querySelector('[class*="animate-spin"]')).toBeTruthy()
    expect(disabled(buttonByText('settings.plugins.load'))).toBe(true)
    expect(disabled(buttonByText('settings.plugins.unload'))).toBe(true)
  })

  it.each([
    ['failed', 'load'],
    ['unload_failed', 'unload'],
  ] as Array<[string, 'load' | 'unload']>)('%s: error label + enabled retry that calls plugin_%s', async (state) => {
    hoisted.pluginLoad.mockResolvedValue({ Status: { Id: 'app.f', Runtime: 'native', State: 'running', Version: '1.0.0', Entrypoints: [] } })
    hoisted.pluginUnload.mockResolvedValue({ Status: { Id: 'app.f', Runtime: 'native', State: 'stopped', Version: '1.0.0', Entrypoints: [] } })
    act(() => { appRegistry.upsert(pluginEntry('app.f', state, 'boom')) })
    renderSection()

    expect(container.textContent).toContain(`settings.plugins.state.${state}`)
    expect(container.textContent).toContain('boom')
    const retry = buttonByText('settings.plugins.state.retry')
    expect(retry).toBeTruthy()
    expect(disabled(retry)).toBe(false)
    // Only the retry button in error states — no load/unload shortcuts.
    expect(buttonByText('settings.plugins.load')).toBeUndefined()
    expect(buttonByText('settings.plugins.unload')).toBeUndefined()

    await act(async () => { retry!.click() })
    const expected = keyForRetry[state]
    if (expected === 'load') {
      expect(hoisted.pluginLoad).toHaveBeenCalledWith({}, { Id: 'app.f' })
      expect(hoisted.pluginUnload).not.toHaveBeenCalled()
    } else {
      expect(hoisted.pluginUnload).toHaveBeenCalledWith({}, { Id: 'app.f' })
      expect(hoisted.pluginLoad).not.toHaveBeenCalled()
    }
  })

  it('running app with residual error still shows unload-enabled row and the error detail', () => {
    act(() => { appRegistry.upsert(pluginEntry('app.e', 'running', 'stale error')) })
    renderSection()
    expect(disabled(buttonByText('settings.plugins.unload'))).toBe(false)
    expect(container.textContent).toContain('stale error')
    expect(container.textContent).not.toContain('settings.plugins.state.failed')
  })

  it('spore apps render without load/unload buttons', () => {
    act(() => {
      appRegistry.upsert({ id: 'spore.app', runtime: 'spore', state: 'running', version: '1', entrypoints: [] })
    })
    renderSection()
    expect(buttonByText('settings.plugins.load')).toBeUndefined()
    expect(buttonByText('settings.plugins.unload')).toBeUndefined()
  })

  it('unregister: two-step confirm calls unregister and removes the row', async () => {
    hoisted.unregister.mockResolvedValue(undefined)
    act(() => { appRegistry.upsert(pluginEntry('app.del', 'stopped')) })
    renderSection()

    const unregister = buttonByText('settings.plugins.unregister')
    expect(unregister).toBeTruthy()
    expect(disabled(unregister)).toBe(false)
    expect(container.textContent).not.toContain('settings.plugins.unregisterConfirm')

    await act(async () => { unregister!.click() })
    expect(hoisted.unregister).not.toHaveBeenCalled()
    expect(buttonByText('settings.plugins.unregisterConfirm')).toBeTruthy()

    // Cancel restores the single-button state without a backend call.
    await act(async () => { buttonByText('settings.plugins.unregisterCancel')!.click() })
    expect(buttonByText('settings.plugins.unregisterConfirm')).toBeUndefined()

    await act(async () => { buttonByText('settings.plugins.unregister')!.click() })
    await act(async () => { buttonByText('settings.plugins.unregisterConfirm')!.click() })
    expect(hoisted.unregister).toHaveBeenCalledWith({}, { Id: 'app.del' })
    expect(appRegistry.getAll().map(a => a.id)).not.toContain('app.del')
  })

  it('unregister error keeps the row and surfaces the message', async () => {
    hoisted.unregister.mockRejectedValue(new Error('unload_failed: retry_cleanup'))
    act(() => { appRegistry.upsert(pluginEntry('app.keep', 'stopped')) })
    renderSection()

    await act(async () => { buttonByText('settings.plugins.unregister')!.click() })
    await act(async () => { buttonByText('settings.plugins.unregisterConfirm')!.click() })
    expect(container.textContent).toContain('unload_failed: retry_cleanup')
    expect(appRegistry.getAll().map(a => a.id)).toContain('app.keep')
  })

  it('spore apps also expose the unregister action', () => {
    act(() => {
      appRegistry.upsert({ id: 'spore.app2', runtime: 'spore', state: 'running', version: '1', entrypoints: [] })
    })
    renderSection()
    expect(buttonByText('settings.plugins.unregister')).toBeTruthy()
  })

  it('install buttons are disabled and show hint when developer mode is off', () => {
    renderSection()
    const zipBtn = buttonByText('installPreview.installZip')
    const folderBtn = buttonByText('installPreview.installFolder')
    expect(zipBtn).toBeTruthy()
    expect(folderBtn).toBeTruthy()
    expect(disabled(zipBtn)).toBe(true)
    expect(disabled(folderBtn)).toBe(true)
    expect(container.textContent).toContain('settings.plugins.devModeRequired')
  })

  it('install buttons are enabled and hint hidden when developer mode is on', () => {
    renderSection({ developerMode: true })
    const zipBtn = buttonByText('installPreview.installZip')
    const folderBtn = buttonByText('installPreview.installFolder')
    expect(disabled(zipBtn)).toBe(false)
    expect(disabled(folderBtn)).toBe(false)
    expect(container.textContent).not.toContain('settings.plugins.devModeRequired')
  })

  it('clicking install buttons in developer mode triggers the matching picker', async () => {
    renderSection({ developerMode: true })
    await act(async () => { buttonByText('installPreview.installZip')!.click() })
    expect(hoisted.selectAndPreviewZip).toHaveBeenCalledWith({})
    await act(async () => { buttonByText('installPreview.installFolder')!.click() })
    expect(hoisted.selectAndPreviewFolder).toHaveBeenCalledWith({})
  })

  it('expanding a row reveals bundle metadata, callables and entrypoints', async () => {
    act(() => {
      appRegistry.upsert({
        id: 'detail.app',
        runtime: 'native',
        state: 'running',
        version: '2.0.0',
        namespace: 'detail.test',
        isolation: 'process',
        trustClass: 'trusted',
        packageHash: 'deadbeef00112233445566778899aabbccdd',
        callables: [{ Id: 'ping', RequestSchema: 'Any', ResponseSchema: 'Any', Permission: 'app.state' }],
        entrypoints: [{ id: 'home', kind: 'view', title: 'Home', route: '/home' }],
        permissions: ['app.state'],
        grantedCapabilities: ['app.state'],
      } as AppEntry)
    })
    renderSection()

    const expand = buttons().find(b => b.getAttribute('aria-expanded') === 'false')
    expect(expand).toBeTruthy()
    await act(async () => { expand!.click() })

    expect(container.textContent).toContain('detail.app')
    expect(container.textContent).toContain('detail.test')
    expect(container.textContent).toContain('2.0.0')
    expect(container.textContent).toContain('process')
    expect(container.textContent).toContain('trusted')
    expect(container.textContent).toContain('deadbeef')
    expect(container.textContent).toContain('ping')
    expect(container.textContent).toContain('app.state')
    expect(container.textContent).toContain('/home')
  })

  it('renders declared capabilities as a read-only list (declaration is authorization)', async () => {
    act(() => {
      appRegistry.upsert({
        id: 'ro.app',
        runtime: 'native',
        state: 'running',
        version: '1.0.0',
        entrypoints: [],
        permissions: ['fs.read'],
        grantedCapabilities: [],
      } as AppEntry)
    })
    renderSection()

    const expand = buttons().find(b => b.getAttribute('aria-expanded') === 'false')
    await act(async () => { expand!.click() })

    // No grant/revoke switches remain; capabilities are declared-only.
    expect(container.querySelectorAll('[role="switch"]').length).toBe(0)
    expect(container.textContent).toContain('capability.fs.read.title')
  })
})

const viewEntry = (id: string, title: string, route?: string) => ({ id, kind: 'view', title, route })

describe('isAppOpenable matrix', () => {
  it.each([
    ['running with a view entrypoint', 'running', [viewEntry('home', 'Home')], true],
    ['crashed with a view entrypoint', 'crashed', [viewEntry('home', 'Home')], true],
    ['failed with a view entrypoint', 'failed', [viewEntry('home', 'Home')], true],
    ['running without entrypoints', 'running', [], false],
    ['running with only a non-view entrypoint', 'running', [{ id: 'hook', kind: 'event', title: 'Hook' }], false],
    ['stopped with a view entrypoint', 'stopped', [viewEntry('home', 'Home')], false],
    ['unloaded with a view entrypoint', 'unloaded', [viewEntry('home', 'Home')], false],
  ])('%s', (_label, state, entrypoints, expected) => {
    expect(isAppOpenable({ state, entrypoints })).toBe(expected)
  })
})

describe('PluginSettingsSection open-in-right-panel button', () => {
  let container: HTMLDivElement
  let root: Root

  const openApp = vi.fn()
  const buttons = () => Array.from(container.querySelectorAll('button'))
  const openButton = () => buttons().find(b => b.textContent?.includes('settings.plugins.open'))

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    vi.spyOn(appRegistry, 'load').mockResolvedValue(undefined)
    appRegistry.clear()
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
    appRegistry.clear()
  })

  const render = (props: { onOpenAppView?: (app: AppEntry) => void } = {}) => act(() => { root.render(<PluginSettingsSection onOpenAppView={props.onOpenAppView} />) })
  const upsert = (id: string, state: string, entrypoints: AppEntry['entrypoints']) => act(() => {
    appRegistry.upsert({ id, runtime: 'spore', state, version: '1', entrypoints })
  })

  it('opens the app view when a running app with a view entrypoint is clicked', async () => {
    upsert('open.app', 'running', [viewEntry('home', 'Home', '/home')])
    render({ onOpenAppView: openApp })

    const btn = openButton()
    expect(btn).toBeTruthy()
    expect(btn!.disabled).toBe(false)

    await act(async () => { btn!.click() })
    expect(openApp).toHaveBeenCalledTimes(1)
    expect(openApp).toHaveBeenCalledWith(expect.objectContaining({ id: 'open.app', state: 'running' }))
  })

  it('hides the open button without an onOpenAppView handler', () => {
    upsert('open.app', 'running', [viewEntry('home', 'Home')])
    render()
    expect(openButton()).toBeUndefined()
  })

  it('hides the open button when the app exposes no view entrypoint', () => {
    upsert('open.app', 'running', [])
    render({ onOpenAppView: openApp })
    expect(openButton()).toBeUndefined()
  })

  it('hides the open button when the app is not running', () => {
    upsert('open.app', 'stopped', [viewEntry('home', 'Home')])
    render({ onOpenAppView: openApp })
    expect(openButton()).toBeUndefined()
  })
})