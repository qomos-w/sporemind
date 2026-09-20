import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AboutOverlay } from './AboutOverlay'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../browserOverlay', () => ({
  useBrowserOverlay: vi.fn(() => {}),
}))

vi.mock('../../../config/buildConfig', () => ({
  buildVersion: '1.2.3',
  buildType: 'release',
  buildFlavor: 'default',
}))

// The dynamic binding import in useBuildInfo would pull in @wailsio/runtime,
// whose drag module schedules a timer that can outlive the jsdom teardown
// (unhandled "window is not defined" on CI). Serve a stub instead.
vi.mock('../../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  GetBuildInfo: vi.fn(async () => ({
    version: '1.2.3',
    build_type: 'release',
    build_flavor: 'default',
    commit: 'deadbee',
    build_time: '2026-01-01T00:00:00Z',
    dirty: false,
    public_version: '0.5.1',
    channel: 'stable',
  })),
}))

describe('AboutOverlay', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(() => {
    act(() => root?.unmount())
    container.remove()
    vi.restoreAllMocks()
  })

  const render = async (open: boolean, onClose = vi.fn()) => {
    root = createRoot(container)
    await act(async () => root.render(<AboutOverlay open={open} onClose={onClose} />))
    return onClose
  }

  it('renders nothing while closed', async () => {
    await render(false)
    expect(container.querySelector('.about-overlay-root')).toBeNull()
  })

  it('shows the app name and software description when open', async () => {
    await render(true)
    expect(container.querySelector('.about-overlay-root')).toBeTruthy()
    expect(container.querySelector('.about-overlay-app-name')?.textContent).toBe('sporemind')
    expect(container.querySelector('.about-overlay-desc')?.textContent).toBe('settings.about.softwareDesc')
  })

  it('calls onClose from the backdrop and the close button', async () => {
    const onClose = await render(true)
    const backdrop = container.querySelector('.about-overlay-backdrop') as HTMLDivElement
    await act(async () => backdrop.click())
    expect(onClose).toHaveBeenCalledTimes(1)
    const closeBtn = container.querySelector('.about-overlay-close') as HTMLButtonElement
    await act(async () => closeBtn.click())
    expect(onClose).toHaveBeenCalledTimes(2)
  })
})
