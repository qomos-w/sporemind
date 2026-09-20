import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import '@testing-library/jest-dom/vitest'
import { ShellGitSettings } from './ShellGitSettings'

const nativeInputValueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set

function setInputValue(input: HTMLInputElement, value: string) {
  // React's value tracker swallows naive .value writes; go through the
  // native setter so the dispatched 'input' event updates the component.
  nativeInputValueSetter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function inputByGuide(guideId: string): HTMLInputElement {
  const el = document.querySelector(`[data-guide-id="${guideId}"]`)
  if (!(el instanceof HTMLInputElement)) {
    throw new Error(`input not found: ${guideId}`)
  }
  return el
}

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string, params?: Record<string, unknown>) => {
    if (params && typeof params === 'object' && 'error' in params) {
      return `${key}: ${params.error}`
    }
    return key
  }),
  gitConfigGet: vi.fn(),
  gitConfigSet: vi.fn(),
  gitRemoteList: vi.fn(),
  gitRemoteAdd: vi.fn(),
  gitRemoteRemove: vi.fn(),
  gitStore: {
    state: {
      projectId: 'project-1',
      worktreeId: null,
    },
    subscribe: vi.fn(() => () => {}),
    getVersion: vi.fn(() => 1),
  },
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/workspace/client', () => ({
  gitConfigGet: hoisted.gitConfigGet,
  gitConfigSet: hoisted.gitConfigSet,
  gitRemoteList: hoisted.gitRemoteList,
  gitRemoteAdd: hoisted.gitRemoteAdd,
  gitRemoteRemove: hoisted.gitRemoteRemove,
}))

vi.mock('../../panels/git-store', () => ({
  gitStore: hoisted.gitStore,
}))

function flushPromises() {
  return new Promise<void>(r => setTimeout(() => r(), 0))
}

describe('ShellGitSettings', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    hoisted.gitStore.state.projectId = 'project-1'
    hoisted.gitStore.state.worktreeId = null
    hoisted.gitConfigGet.mockImplementation((_: unknown, req: { Key: string }) => {
      const defaults: Record<string, string> = {
        'user.name': 'Test User',
        'user.email': 'test@example.com',
        'http.proxy': 'http://proxy.example.com:8080',
        'https.proxy': 'https://proxy.example.com:8080',
      }
      return Promise.resolve({ Value: defaults[req.Key] ?? '' })
    })
    hoisted.gitConfigSet.mockResolvedValue(undefined)
    hoisted.gitRemoteList.mockResolvedValue({
      Remotes: [{ Name: 'origin', Urls: ['https://github.com/test/repo.git'] }],
    })
    hoisted.gitRemoteAdd.mockResolvedValue(undefined)
    hoisted.gitRemoteRemove.mockResolvedValue(undefined)
  })

  afterEach(() => {
    root.unmount()
    container.remove()
    vi.clearAllMocks()
  })

  it('renders no-project placeholder when no project is selected', async () => {
    hoisted.gitStore.state.projectId = ''
    await act(async () => {
      root.render(<ShellGitSettings />)
    })
    expect(container.textContent).toContain('settings.git.noProject')
  })

  it('loads and displays account, proxy and remotes on mount', async () => {
    await act(async () => {
      root.render(<ShellGitSettings />)
    })
    await act(async () => {
      await flushPromises()
    })

    const nameInput = inputByGuide('settings/git/account/name')
    const emailInput = inputByGuide('settings/git/account/email')
    const httpInput = inputByGuide('settings/git/proxy/http')
    const httpsInput = inputByGuide('settings/git/proxy/https')
    expect(nameInput).toHaveValue('Test User')
    expect(emailInput).toHaveValue('test@example.com')
    expect(httpInput).toHaveValue('http://proxy.example.com:8080')
    expect(httpsInput).toHaveValue('https://proxy.example.com:8080')
    expect(container.textContent).toContain('origin')
    expect(container.textContent).toContain('https://github.com/test/repo.git')
  })

  it('saves account and proxy settings when save buttons are clicked', async () => {
    await act(async () => {
      root.render(<ShellGitSettings />)
    })
    await act(async () => {
      await flushPromises()
    })

    const httpInput = inputByGuide('settings/git/proxy/http')
    const httpsInput = inputByGuide('settings/git/proxy/https')

    await act(async () => {
      setInputValue(inputByGuide('settings/git/account/name'), 'New User')
      setInputValue(inputByGuide('settings/git/account/email'), 'new@example.com')
    })

    const buttons = container.querySelectorAll('button')
    const accountSave = Array.from(buttons).find(b => b.textContent?.includes('settings.git.account.save'))
    expect(accountSave).toBeTruthy()
    await act(async () => {
      accountSave!.click()
      await flushPromises()
    })

    expect(hoisted.gitConfigSet).toHaveBeenCalledWith(expect.anything(), {
      ProjectId: 'project-1',
      WorktreeID: undefined,
      Key: 'user.name',
      Value: 'New User',
      Global: false,
    })
    expect(hoisted.gitConfigSet).toHaveBeenCalledWith(expect.anything(), {
      ProjectId: 'project-1',
      WorktreeID: undefined,
      Key: 'user.email',
      Value: 'new@example.com',
      Global: false,
    })

    await act(async () => {
      setInputValue(httpInput, 'http://new-proxy.example.com:8080')
      setInputValue(httpsInput, 'https://new-proxy.example.com:8080')
    })

    const proxySave = Array.from(buttons).find(b => b.textContent?.includes('settings.git.proxy.save'))
    expect(proxySave).toBeTruthy()
    await act(async () => {
      proxySave!.click()
      await flushPromises()
    })

    expect(hoisted.gitConfigSet).toHaveBeenCalledWith(expect.anything(), {
      ProjectId: 'project-1',
      WorktreeID: undefined,
      Key: 'http.proxy',
      Value: 'http://new-proxy.example.com:8080',
      Global: false,
    })
    expect(hoisted.gitConfigSet).toHaveBeenCalledWith(expect.anything(), {
      ProjectId: 'project-1',
      WorktreeID: undefined,
      Key: 'https.proxy',
      Value: 'https://new-proxy.example.com:8080',
      Global: false,
    })
  })

  it('adds a new remote when add button is clicked', async () => {
    await act(async () => {
      root.render(<ShellGitSettings />)
    })
    await act(async () => {
      await flushPromises()
    })

    const nameInput = inputByGuide('settings/git/remotes/name')
    const urlInput = inputByGuide('settings/git/remotes/url')

    await act(async () => {
      setInputValue(nameInput, 'upstream')
      setInputValue(urlInput, 'https://github.com/upstream/repo.git')
    })

    const addButton = Array.from(container.querySelectorAll('button')).find(
      b => b.textContent?.includes('settings.git.remotes.add'),
    )
    expect(addButton).toBeTruthy()
    await act(async () => {
      addButton!.click()
      await flushPromises()
    })

    expect(hoisted.gitRemoteAdd).toHaveBeenCalledWith(expect.anything(), {
      ProjectId: 'project-1',
      WorktreeID: undefined,
      Name: 'upstream',
      Url: 'https://github.com/upstream/repo.git',
    })
  })

  it('removes a remote when remove button is clicked', async () => {
    await act(async () => {
      root.render(<ShellGitSettings />)
    })
    await act(async () => {
      await flushPromises()
    })

    const removeButton = container.querySelector('[data-guide-id="settings/git/remotes/remove/origin"]')
    expect(removeButton).toBeTruthy()
    await act(async () => {
      (removeButton as HTMLElement).click()
      await flushPromises()
    })

    expect(hoisted.gitRemoteRemove).toHaveBeenCalledWith(expect.anything(), {
      ProjectId: 'project-1',
      WorktreeID: undefined,
      Name: 'origin',
    })
  })

  it('displays an error message when loading fails', async () => {
    hoisted.gitConfigGet.mockRejectedValue(new Error('load failed'))
    await act(async () => {
      root.render(<ShellGitSettings />)
    })
    await act(async () => {
      await flushPromises()
    })

    expect(container.textContent).toContain('settings.git.error: load failed')
  })
})
