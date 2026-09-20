import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import '@testing-library/jest-dom/vitest'
import { ShellNoGitModeSettings } from './ShellNoGitModeSettings'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string, params?: Record<string, unknown>) => {
    if (params && typeof params === 'object' && 'error' in params) {
      return `${key}: ${params.error}`
    }
    return key
  }),
  noGitModeGet: vi.fn(),
  noGitModeSet: vi.fn(),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/project/client', () => ({
  noGitModeGet: hoisted.noGitModeGet,
  noGitModeSet: hoisted.noGitModeSet,
}))

vi.mock('../../panels/git-store', () => ({
  gitStore: {
    state: { projectId: 'proj-1' },
    subscribe: (_fn: () => void) => () => {},
    getVersion: () => 0,
  },
}))

function flushPromises() {
  return new Promise<void>(r => setTimeout(() => r(), 0))
}

function switchByGuide(): HTMLElement {
  const el = document.querySelector('[data-guide-id="settings/git/noGitMode/toggle"]')
  if (!(el instanceof HTMLElement)) {
    throw new Error('no-git-mode switch element not found')
  }
  return el
}

describe('ShellNoGitModeSettings', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    hoisted.noGitModeGet.mockResolvedValue({ NoGitMode: false, HasGitRepo: true })
    hoisted.noGitModeSet.mockImplementation((_: unknown, req: { NoGitMode: boolean }) =>
      Promise.resolve({ NoGitMode: req.NoGitMode }),
    )
  })

  afterEach(() => {
    root.unmount()
    container.remove()
    vi.clearAllMocks()
  })

  it('loads the no-git mode state and reflects a checked switch', async () => {
    hoisted.noGitModeGet.mockResolvedValue({ NoGitMode: true, HasGitRepo: true })
    await act(async () => {
      root.render(<ShellNoGitModeSettings />)
    })
    await act(async () => {
      await flushPromises()
    })

    expect(hoisted.noGitModeGet).toHaveBeenCalledTimes(1)
    expect(hoisted.noGitModeGet).toHaveBeenCalledWith(expect.anything(), {}, { target: 'proj-1' })
    expect(switchByGuide().hasAttribute('data-checked')).toBe(true)
    expect(container.textContent).toContain('settings.git.noGitMode')
  })

  it('shows an unchecked switch when no-git mode is off', async () => {
    await act(async () => {
      root.render(<ShellNoGitModeSettings />)
    })
    await act(async () => {
      await flushPromises()
    })

    expect(switchByGuide().hasAttribute('data-unchecked')).toBe(true)
    expect(container.querySelector('[data-guide-id="settings/git/noGitMode/noGitRepoHint"]')).toBeNull()
  })

  it('shows the auto-activation hint when the project root has no git repo', async () => {
    hoisted.noGitModeGet.mockResolvedValue({ NoGitMode: false, HasGitRepo: false })
    await act(async () => {
      root.render(<ShellNoGitModeSettings />)
    })
    await act(async () => {
      await flushPromises()
    })

    const hint = container.querySelector('[data-guide-id="settings/git/noGitMode/noGitRepoHint"]')
    expect(hint).toBeTruthy()
    expect(container.textContent).toContain('settings.git.noGitMode.noGitRepoHint')
  })

  it('persists the toggle via no_git_mode_set and updates state', async () => {
    await act(async () => {
      root.render(<ShellNoGitModeSettings />)
    })
    await act(async () => {
      await flushPromises()
    })

    const sw = switchByGuide()
    expect(sw.hasAttribute('data-unchecked')).toBe(true)
    await act(async () => {
      sw.click()
      await flushPromises()
    })

    expect(hoisted.noGitModeSet).toHaveBeenCalledWith(expect.anything(), { NoGitMode: true }, { target: 'proj-1' })
    expect(switchByGuide().hasAttribute('data-checked')).toBe(true)
  })

  it('displays an error when saving fails', async () => {
    hoisted.noGitModeSet.mockRejectedValue(new Error('save failed'))
    await act(async () => {
      root.render(<ShellNoGitModeSettings />)
    })
    await act(async () => {
      await flushPromises()
    })

    await act(async () => {
      switchByGuide().click()
      await flushPromises()
    })

    expect(container.textContent).toContain('settings.git.noGitMode.error: save failed')
  })
})