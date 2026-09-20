import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AIShellTopbar } from './AIShellTopbar'
import type { ContentMode } from './AIShellSidebar'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => {
  const companionListeners = new Set<() => void>()
  const companion = { visible: true }
  return {
    t: vi.fn((key: string, opts?: any) => opts?.defaultValue ?? key),
    isHostMode: vi.fn(() => false),
    useBrowserOverlay: vi.fn(() => {}),
    getMultiConsoleGridConfig: vi.fn(async () => ({ columns: 2, rows: 2 })),
    saveMultiConsoleGridConfig: vi.fn(async () => {}),
    useResolvedContentModes: vi.fn(() => [
      { id: 'conversation' as ContentMode, label: 'Conversation', pinnable: false, Icon: () => null },
      { id: 'notes' as ContentMode, label: 'Notes', pinnable: true, Icon: () => null },
      { id: 'multiconsole' as ContentMode, label: 'Multi-Console', pinnable: true, Icon: () => null },
    ]),
    appUpdateStatus: null as null | {
      phase: string
      current_version: string
      channel: string
      update_available: boolean
      remote?: { public_version: string; channel: string; platform: string; file: string; url: string; sha256: string; size: number; notes: string } | null
      progress: number
      path: string
      error: string
    },
    appUpdateFeatureEnabled: false as boolean,
    companion,
    subscribeCompanionVisible: vi.fn((cb: () => void) => {
      companionListeners.add(cb)
      return () => {
        companionListeners.delete(cb)
      }
    }),
    getCompanionVisible: vi.fn(() => companion.visible),
    ensureCompanionVisibleLoaded: vi.fn(async () => {}),
    setCompanionVisible: vi.fn((v: boolean) => {
      companion.visible = v
      for (const l of companionListeners) l()
    }),
  }
})

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/window-controller', () => ({
  wailsWindowController: { isHostMode: hoisted.isHostMode },
}))

vi.mock('./useAppUpdate', () => ({
  useAppUpdate: () => ({
    status: hoisted.appUpdateStatus,
    featureEnabled: hoisted.appUpdateFeatureEnabled,
    isWails: false,
    check: vi.fn(),
    download: vi.fn(),
    install: vi.fn(),
    dismiss: vi.fn(),
    reveal: vi.fn(),
  }),
}))

vi.mock('../browserOverlay', () => ({
  useBrowserOverlay: hoisted.useBrowserOverlay,
}))

vi.mock('../../../application/workspace-ui-state', () => ({
  getMultiConsoleGridConfig: hoisted.getMultiConsoleGridConfig,
  saveMultiConsoleGridConfig: hoisted.saveMultiConsoleGridConfig,
}))

vi.mock('../content-modes', () => ({
  useResolvedContentModes: hoisted.useResolvedContentModes,
}))

vi.mock('../../../application/companion-visibility', () => ({
  subscribeCompanionVisible: hoisted.subscribeCompanionVisible,
  getCompanionVisible: hoisted.getCompanionVisible,
  ensureCompanionVisibleLoaded: hoisted.ensureCompanionVisibleLoaded,
  setCompanionVisible: hoisted.setCompanionVisible,
}))

describe('AIShellTopbar mobile view options', () => {
  let container: HTMLDivElement
  let root: Root
  let onContentModeChange: any
  let onShellModeChange: any

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    onContentModeChange = vi.fn() as any
    onShellModeChange = vi.fn() as any
    hoisted.companion.visible = true
  })

  afterEach(() => {
    act(() => root?.unmount())
    container.remove()
    vi.restoreAllMocks()
  })

  const render = async (props: Partial<React.ComponentProps<typeof AIShellTopbar>> = {}) => {
    await act(async () => {
      root = createRoot(container)
      root.render(
        <AIShellTopbar
          maximised={false}
          onMaximisedChange={() => {}}
          isMobile
          contentMode="conversation"
          onContentModeChange={onContentModeChange}
          onShellModeChange={onShellModeChange}
          {...props}
        />,
      )
    })
  }

  it('renders the view options button on mobile', async () => {
    await render()
    expect(container.querySelector('.ai-shell-mc-btn')).toBeTruthy()
  })

  it('opens the mobile view options dropdown when clicked', async () => {
    await render()
    const btn = container.querySelector('.ai-shell-mc-btn') as HTMLButtonElement
    await act(async () => btn.click())
    expect(container.querySelector('.ai-shell-mc-dropdown')).toBeTruthy()
  })

  it('switches content mode from the dropdown', async () => {
    await render()
    const btn = container.querySelector('.ai-shell-mc-btn') as HTMLButtonElement
    await act(async () => btn.click())

    const item = Array.from(container.querySelectorAll('.ai-shell-mc-dropdown-item'))
      .find((el) => el.textContent?.includes('Notes')) as HTMLButtonElement
    expect(item).toBeTruthy()

    await act(async () => item.click())
    expect(onContentModeChange).toHaveBeenCalledWith('notes')
  })

  it('triggers shell mode from the dropdown', async () => {
    await render()
    const btn = container.querySelector('.ai-shell-mc-btn') as HTMLButtonElement
    await act(async () => btn.click())

    const item = Array.from(container.querySelectorAll('.ai-shell-mc-dropdown-item'))
      .find((el) => el.textContent?.includes('workbench.options.workbench')) as HTMLButtonElement
    expect(item).toBeTruthy()

    await act(async () => item.click())
    expect(onShellModeChange).toHaveBeenCalled()
  })
})

describe('AIShellTopbar desktop navigation and menu bar', () => {
  let container: HTMLDivElement
  let root: Root
  let onGoBack: any
  let onGoForward: any
  let onFileMenuAction: any
  let onViewMenuAction: any
  let onHelpMenuAction: any
  let onAboutAction: any

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    onGoBack = vi.fn()
    onGoForward = vi.fn()
    onFileMenuAction = vi.fn()
    onViewMenuAction = vi.fn()
    onHelpMenuAction = vi.fn()
    onAboutAction = vi.fn()
  })

  afterEach(() => {
    act(() => root?.unmount())
    container.remove()
    vi.restoreAllMocks()
    hoisted.appUpdateStatus = null
    hoisted.appUpdateFeatureEnabled = false
  })

  const render = async (props: Partial<React.ComponentProps<typeof AIShellTopbar>> = {}) => {
    await act(async () => {
      root = createRoot(container)
      root.render(
        <AIShellTopbar
          maximised={false}
          onMaximisedChange={() => {}}
          isMobile={false}
          contentMode="conversation"
          onContentModeChange={() => {}}
          canGoBack={false}
          canGoForward={false}
          onGoBack={onGoBack}
          onGoForward={onGoForward}
          onFileMenuAction={onFileMenuAction}
          onViewMenuAction={onViewMenuAction}
          onHelpMenuAction={onHelpMenuAction}
          onAboutAction={onAboutAction}
          {...props}
        />,
      )
    })
  }

  it('renders back/forward buttons on desktop', async () => {
    await render()
    const buttons = container.querySelectorAll('.ai-shell-history-btn')
    expect(buttons.length).toBe(2)
    expect((buttons[0] as HTMLButtonElement).disabled).toBe(true)
    expect((buttons[1] as HTMLButtonElement).disabled).toBe(true)
  })

  it('enables back/forward buttons based on props', async () => {
    await render({ canGoBack: true, canGoForward: true })
    const buttons = container.querySelectorAll('.ai-shell-history-btn')
    expect((buttons[0] as HTMLButtonElement).disabled).toBe(false)
    expect((buttons[1] as HTMLButtonElement).disabled).toBe(false)
  })

  it('calls onGoBack and onGoForward when clicked', async () => {
    await render({ canGoBack: true, canGoForward: true })
    const buttons = container.querySelectorAll('.ai-shell-history-btn')
    await act(async () => (buttons[0] as HTMLButtonElement).click())
    expect(onGoBack).toHaveBeenCalled()
    await act(async () => (buttons[1] as HTMLButtonElement).click())
    expect(onGoForward).toHaveBeenCalled()
  })

  it('renders the menu bar on desktop', async () => {
    await render()
    const triggers = container.querySelectorAll('.ai-shell-menu-bar-trigger')
    expect(triggers.length).toBe(4)
    expect(Array.from(triggers).map((t) => t.textContent)).toEqual([
      'shell.topbar.menu.file',
      'shell.topbar.menu.edit',
      'shell.topbar.menu.view',
      'shell.topbar.menu.about',
    ])
  })

  describe('launcher-mode switch', () => {
    it('renders left of the back button on desktop and calls onLauncherModeChange', async () => {
      const onLauncherModeChange = vi.fn()
      await render({ launcherMode: 'code', onLauncherModeChange })
      const left = container.querySelector('.ai-shell-topbar-left')!
      const modeSwitch = left.querySelector('.ai-sidebar-mode-switch')
      expect(modeSwitch).toBeTruthy()
      const children = Array.from(left.children)
      const backBtn = children.find(el => el.classList.contains('ai-shell-history-btn'))
      expect(backBtn).toBeTruthy()
      expect(children.indexOf(modeSwitch as Element)).toBeLessThan(children.indexOf(backBtn as Element))
      await act(async () => {
        const appSegment = Array.from((modeSwitch as HTMLElement).querySelectorAll('button'))
          .find(b => b.textContent?.includes('shell.sidebar.launcherMode.app'))
        appSegment?.click()
      })
      expect(onLauncherModeChange).toHaveBeenCalledWith('app')
    })

    it('renders no launcher-mode switch without props or on mobile', async () => {
      await render()
      expect(container.querySelector('.ai-sidebar-mode-switch')).toBeNull()
      await act(async () => root?.unmount())
      await render({ isMobile: true, launcherMode: 'code', onLauncherModeChange: vi.fn() })
      expect(container.querySelector('.ai-sidebar-mode-switch')).toBeNull()
    })

    it('flags the topbar as sidebar-collapsed when the sidebar is hidden, so the switch can animate shut', async () => {
      await render({ launcherMode: 'code', onLauncherModeChange: vi.fn(), sidebarVisible: false })
      const topbar = container.querySelector('.ai-shell-topbar')!
      expect(topbar.classList.contains('sidebar-collapsed')).toBe(true)
      await act(async () => root?.unmount())
      await render({ launcherMode: 'code', onLauncherModeChange: vi.fn(), sidebarVisible: true })
      expect(container.querySelector('.ai-shell-topbar')!.classList.contains('sidebar-collapsed')).toBe(false)
    })
  })

  it('renders the update button in Wails mode when the autoUpdate flag is on and a newer version exists', async () => {
    hoisted.isHostMode.mockReturnValue(true)
    hoisted.appUpdateFeatureEnabled = true
    hoisted.appUpdateStatus = {
      phase: 'available',
      current_version: '0.20',
      channel: 'stable',
      update_available: true,
      remote: {
        public_version: '0.21',
        channel: 'stable',
        platform: 'windows-x64',
        file: 'sporemind-0.21-stable.exe',
        url: 'https://example.com/sporemind-0.21-stable.exe',
        sha256: '',
        size: 0,
        notes: '',
      },
      progress: 0,
      path: '',
      error: '',
    }
    await render()
    // The button gates on the mocked useAppUpdate hook; verify the hook's
    // state drives the render.
    const btn = container.querySelector('.ai-shell-update-btn') as HTMLButtonElement | null
    expect(btn).toBeTruthy()
    expect(btn?.textContent).toContain('appUpdate.button.available')
  })

  it('hides the update button when the autoUpdate flag is off', async () => {
    hoisted.isHostMode.mockReturnValue(true)
    hoisted.appUpdateFeatureEnabled = false
    hoisted.appUpdateStatus = {
      phase: 'available',
      current_version: '0.20',
      channel: 'stable',
      update_available: true,
      remote: null,
      progress: 0,
      path: '',
      error: '',
    }
    await render()
    expect(container.querySelector('.ai-shell-update-btn')).toBeNull()
  })

  it('hides the update button outside Wails mode', async () => {
    hoisted.isHostMode.mockReturnValue(false)
    hoisted.appUpdateFeatureEnabled = true
    hoisted.appUpdateStatus = {
      phase: 'available',
      current_version: '0.20',
      channel: 'stable',
      update_available: true,
      remote: null,
      progress: 0,
      path: '',
      error: '',
    }
    await render()
    expect(container.querySelector('.ai-shell-update-btn')).toBeNull()
  })

  it('shows the toast mute toggle on desktop only, seeded from the visibility store', async () => {
    // Mobile: the toggle is suppressed with the other panel toggles.
    await render({ isMobile: true })
    expect(container.querySelector('[title="shell.topbar.companion.hide"]')).toBeNull()

    // Desktop: toggle appears, reflecting the persisted preference.
    await act(async () => root?.unmount())
    hoisted.companion.visible = true
    await render({ isMobile: false })
    const btn = container.querySelector('[title="shell.topbar.companion.hide"]') as HTMLButtonElement | null
    expect(btn).toBeTruthy()
    expect(btn?.getAttribute('aria-pressed')).toBe('true')
  })

  it('toggles the toast overlay through the visibility store and persists the state', async () => {
    hoisted.companion.visible = true
    await render({ isMobile: false })
    await act(async () => {})

    const btn = container.querySelector('[title="shell.topbar.companion.hide"]') as HTMLButtonElement
    expect(btn).toBeTruthy()

    // Visible → clicking mutes the overlay.
    await act(async () => btn.click())
    expect(hoisted.setCompanionVisible).toHaveBeenLastCalledWith(false)
    expect(container.querySelector('[title="shell.topbar.companion.show"]')).toBeTruthy()

    // Muted → clicking again unmutes.
    const showBtn = container.querySelector('[title="shell.topbar.companion.show"]') as HTMLButtonElement
    await act(async () => showBtn.click())
    expect(hoisted.setCompanionVisible).toHaveBeenLastCalledWith(true)
    expect(container.querySelector('[title="shell.topbar.companion.hide"]')).toBeTruthy()
  })

  it('opens the About dropdown and fires onAboutAction from its entry', async () => {
    await render()
    const triggers = container.querySelectorAll('.ai-shell-menu-bar-trigger')
    await act(async () => (triggers[3] as HTMLButtonElement).click())
    const dropdown = container.querySelector('.ai-shell-menu-bar-dropdown')
    expect(dropdown).toBeTruthy()
    const items = dropdown?.querySelectorAll('.ai-shell-menu-bar-dropdown-item') ?? []
    expect(Array.from(items).map((i) => i.textContent)).toEqual([
      'onboarding.tutorial.category.basics.label',
      'onboarding.tutorial.category.workflow.label',
      'onboarding.tutorial.category.tools.label',
      'onboarding.tutorial.category.settings.label',
      'shell.topbar.menu.help.toolGuide',
      'shell.topbar.menu.about.software',
    ])
    const softwareItem = Array.from(items)
      .find((el) => el.textContent?.includes('shell.topbar.menu.about.software')) as HTMLButtonElement
    await act(async () => softwareItem.click())
    expect(onAboutAction).toHaveBeenCalledTimes(1)
    expect(onHelpMenuAction).not.toHaveBeenCalled()
  })

  it('opens a dropdown when a menu trigger is clicked', async () => {
    await render()
    const triggers = container.querySelectorAll('.ai-shell-menu-bar-trigger')
    await act(async () => (triggers[0] as HTMLButtonElement).click())
    expect(container.querySelector('.ai-shell-menu-bar-dropdown')).toBeTruthy()
  })

  it('fires onFileMenuAction when a File menu item is clicked', async () => {
    await render()
    const triggers = container.querySelectorAll('.ai-shell-menu-bar-trigger')
    await act(async () => (triggers[0] as HTMLButtonElement).click())

    const item = Array.from(container.querySelectorAll('.ai-shell-menu-bar-dropdown-item'))
      .find((el) => el.textContent?.includes('shell.topbar.menu.file.newProject')) as HTMLButtonElement
    expect(item).toBeTruthy()

    await act(async () => item.click())
    expect(onFileMenuAction).toHaveBeenCalledWith('new-project')
  })

  it('fires onHelpMenuAction when a Help menu tutorial item is clicked', async () => {
    await render()
    const triggers = container.querySelectorAll('.ai-shell-menu-bar-trigger')
    await act(async () => (triggers[3] as HTMLButtonElement).click())

    const item = Array.from(container.querySelectorAll('.ai-shell-menu-bar-dropdown-item'))
      .find((el) => el.textContent?.includes('onboarding.tutorial.category.basics.label')) as HTMLButtonElement
    expect(item).toBeTruthy()

    await act(async () => item.click())
    expect(onHelpMenuAction).toHaveBeenCalledWith('tutorial-basics')
  })

  it('fires onHelpMenuAction when the tool-guide item is clicked', async () => {
    await render()
    const triggers = container.querySelectorAll('.ai-shell-menu-bar-trigger')
    await act(async () => (triggers[3] as HTMLButtonElement).click())

    const item = Array.from(container.querySelectorAll('.ai-shell-menu-bar-dropdown-item'))
      .find((el) => el.textContent?.includes('shell.topbar.menu.help.toolGuide')) as HTMLButtonElement
    expect(item).toBeTruthy()

    await act(async () => item.click())
    expect(onHelpMenuAction).toHaveBeenCalledWith('tool-guide')
  })

  it('does not render the menu bar or nav buttons on mobile', async () => {
    await render({ isMobile: true })
    expect(container.querySelector('.ai-shell-menu-bar')).toBeFalsy()
    expect(container.querySelector('.ai-shell-history-btn')).toBeFalsy()
  })
})
