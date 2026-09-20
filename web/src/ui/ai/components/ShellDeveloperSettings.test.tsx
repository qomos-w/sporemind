import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ShellDeveloperSettings } from './ShellDeveloperSettings'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  flags: { labMode: false },
  loadDesktopConfig: vi.fn(),
  getDesktopConfig: vi.fn(),
  saveDesktopConfig: vi.fn(),
  // Test seam: the imported `buildType` symbol is read once at module
  // evaluation, so this hoisted getter lets us flip the value between tests
  // by reassigning `.buildType`.
  buildType: 'release' as 'release' | 'beta' | 'dev',
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../config/featureFlags', () => ({
  featureFlags: hoisted.flags,
}))

vi.mock('../../../application/desktop-config', () => ({
  loadDesktopConfig: hoisted.loadDesktopConfig,
  getDesktopConfig: hoisted.getDesktopConfig,
  saveDesktopConfig: hoisted.saveDesktopConfig,
}))

vi.mock('../../../application/runtime', () => ({
  isWails: () => true,
}))

vi.mock('../../../application/auth-store', () => ({
  isAdmin: () => false,
}))

vi.mock('../../../config/buildConfig', () => ({
  get buildType() { return hoisted.buildType },
  get buildFlavor() { return 'default' },
  get buildVersion() { return 'test' },
  get wailsProduction() { return false },
}))

vi.mock('./settings-data', () => ({
  ThemeModeButton: ({ label }: { label?: string }) => (
    <button type="button">{label}</button>
  ),
}))

async function renderPanel(props?: {
  developerMode?: boolean
  onDeveloperModeChange?: (v: boolean) => void
  providerUserAgentVisible?: boolean
  onProviderUserAgentVisibleChange?: (v: boolean) => void
}) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <ShellDeveloperSettings
        developerMode={props?.developerMode}
        onDeveloperModeChange={props?.onDeveloperModeChange}
        providerUserAgentVisible={props?.providerUserAgentVisible}
        onProviderUserAgentVisibleChange={props?.onProviderUserAgentVisibleChange}
      />,
    )
  })
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0))
  })
  return { container, root }
}

describe('ShellDeveloperSettings developer-mode availability', () => {
  let roots: Root[] = []

  beforeEach(() => {
    roots = []
    hoisted.flags.labMode = false
    hoisted.buildType = 'release'
    hoisted.loadDesktopConfig.mockReset()
    hoisted.loadDesktopConfig.mockResolvedValue({
      transport: 'wails',
      gatewayAddr: '127.0.0.1:18080',
      gatewayBindAddrs: [],
    })
    hoisted.getDesktopConfig.mockReset()
    hoisted.getDesktopConfig.mockReturnValue(null)
    hoisted.saveDesktopConfig.mockReset()
    hoisted.saveDesktopConfig.mockResolvedValue(undefined)
  })

  it('enables the toggle for a free account with no pass', async () => {
    const { container, root } = await renderPanel({ onDeveloperModeChange: () => {} })
    roots.push(root)
    const toggle = container.querySelector('[role="switch"]') as HTMLElement
    expect(toggle).toBeTruthy()
    expect(toggle.getAttribute('aria-disabled')).not.toBe('true')
    expect(container.querySelector('[role="note"]')).toBeNull()
  })

  afterEach(async () => {
    for (const root of roots) {
      await act(async () => {
        root.unmount()
      })
    }
  })
})

describe('ShellDeveloperSettings transport switch visibility', () => {
  let roots: Root[] = []

  beforeEach(() => {
    roots = []
    hoisted.flags.labMode = false
    hoisted.buildType = 'release'
    hoisted.loadDesktopConfig.mockReset()
    hoisted.loadDesktopConfig.mockResolvedValue({
      transport: 'wails',
      gatewayAddr: '127.0.0.1:18080',
      gatewayBindAddrs: [],
    })
    hoisted.getDesktopConfig.mockReset()
    hoisted.getDesktopConfig.mockReturnValue(null)
  })

  afterEach(async () => {
    for (const root of roots) {
      await act(async () => {
        root.unmount()
      })
    }
  })

  it('shows the ws/wails transport switch on a release build when Developer Mode is on', async () => {
    const { container, root } = await renderPanel({ developerMode: true })
    roots.push(root)
    expect(container.textContent).toContain('settings.developer.transport')
    expect(container.textContent).toContain('settings.developer.wailsTransport')
    expect(container.textContent).toContain('settings.developer.wsTransport')
    expect(container.textContent).toContain('settings.developer.save')
    expect(hoisted.loadDesktopConfig).toHaveBeenCalled()
  })

  it('hides the transport switch on a release build when Developer Mode is off', async () => {
    const { container, root } = await renderPanel({ developerMode: false })
    roots.push(root)
    expect(container.textContent).not.toContain('settings.developer.transport')
    expect(hoisted.loadDesktopConfig).not.toHaveBeenCalled()
  })

  it('hides the transport switch on lab builds when Developer Mode is off', async () => {
    hoisted.flags.labMode = true
    const { container, root } = await renderPanel({ developerMode: false })
    roots.push(root)
    expect(container.textContent).not.toContain('settings.developer.transport')
    expect(hoisted.loadDesktopConfig).not.toHaveBeenCalled()
  })

  it('shows the transport switch on lab builds when Developer Mode is on', async () => {
    hoisted.flags.labMode = true
    const { container, root } = await renderPanel({ developerMode: true })
    roots.push(root)
    expect(container.textContent).toContain('settings.developer.transport')
    expect(hoisted.loadDesktopConfig).toHaveBeenCalled()
  })
})

describe('ShellDeveloperSettings user-agent experimental option', () => {
  let roots: Root[] = []

  beforeEach(() => {
    roots = []
    hoisted.flags.labMode = false
    hoisted.buildType = 'release'
    hoisted.loadDesktopConfig.mockReset()
    hoisted.loadDesktopConfig.mockResolvedValue({
      transport: 'wails',
      gatewayAddr: '127.0.0.1:18080',
      gatewayBindAddrs: [],
    })
    hoisted.getDesktopConfig.mockReset()
    hoisted.getDesktopConfig.mockReturnValue(null)
  })

  afterEach(async () => {
    for (const root of roots) {
      await act(async () => {
        root.unmount()
      })
    }
  })

  it('hides the user-agent row when Developer Mode is off', async () => {
    const { container, root } = await renderPanel({ developerMode: false })
    roots.push(root)
    expect(container.textContent).not.toContain('settings.developer.userAgent')
  })

  it('shows the user-agent row when Developer Mode is on', async () => {
    const { container, root } = await renderPanel({ developerMode: true, providerUserAgentVisible: true })
    roots.push(root)
    expect(container.textContent).toContain('settings.developer.userAgent')
  })

  it('reflects the providerUserAgentVisible state on the switch', async () => {
    const { container, root } = await renderPanel({
      developerMode: true,
      providerUserAgentVisible: true,
      onProviderUserAgentVisibleChange: () => {},
    })
    roots.push(root)
    const switches = container.querySelectorAll('[role="switch"]')
    const uaSwitch = Array.from(switches).find(
      (s) => s.getAttribute('aria-label') === 'settings.developer.userAgent',
    ) as HTMLElement
    expect(uaSwitch).toBeTruthy()
    expect(uaSwitch.getAttribute('aria-checked')).toBe('true')
  })
})

