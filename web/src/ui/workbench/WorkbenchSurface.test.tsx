import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'

import { I18nProvider } from '../../i18n'
import { WorkbenchSurface } from './WorkbenchSurface'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as any).ResizeObserver = ResizeObserverStub

vi.mock('../../application/generated-client', () => ({ client: {} }))

const registryState = vi.hoisted(() => ({
  apps: [] as any[],
  listeners: new Set<() => void>(),
}))

vi.mock('../../application/app-registry', () => ({
  appRegistry: {
    subscribe: (listener: () => void) => {
      registryState.listeners.add(listener)
      return () => registryState.listeners.delete(listener)
    },
    getAll: () => registryState.apps,
    get: (id: string) => registryState.apps.find(a => a.id === id),
  },
}))

vi.mock('../components/PluginIframe', () => ({
  PluginIframe: ({ pluginID }: { pluginID: string }) => <div className="plugin-iframe" data-plugin={pluginID} />,
}))

vi.mock('../ai/components/appIconResolver', () => ({ resolveAppIcon: () => null }))

const voiceState = vi.hoisted(() => ({
  setActive: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('../ai/voice-api', () => ({
  voiceAPI: { setActive: (...a: unknown[]) => voiceState.setActive(...a) },
}))

const shellState = vi.hoisted(() => ({
  sessionOpen: vi.fn(),
  sessionFetch: vi.fn(),
  sessionWrite: vi.fn(),
  sessionResize: vi.fn(),
  sessionClose: vi.fn(),
}))

vi.mock('../../gen-clients/shell/client', () => ({
  sessionOpen: (...a: unknown[]) => shellState.sessionOpen(...a),
  sessionFetch: (...a: unknown[]) => shellState.sessionFetch(...a),
  sessionWrite: (...a: unknown[]) => shellState.sessionWrite(...a),
  sessionResize: (...a: unknown[]) => shellState.sessionResize(...a),
  sessionClose: (...a: unknown[]) => shellState.sessionClose(...a),
  OnShellSessionOutput: () => () => {},
}))

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    loadAddon() {}
    open() {}
    onData() { return { dispose() {} } }
    write() {}
    focus() {}
    clear() {}
    dispose() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))

function app(id: string, name: string, route = 'index.html') {
  return {
    id,
    name,
    runtime: 'native',
    state: 'running',
    version: '1.0.0',
    entrypoints: [{ id: 'main', kind: 'view', title: name, route }],
    icon: 'notebook-pen',
    color: '#c9b47f',
  }
}

function renderSurface() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <WorkbenchSurface projectRoot="/proj" />
      </I18nProvider>,
    )
  })
  return { root, container }
}

function typeInto(input: HTMLInputElement, value: string) {
  act(() => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
    setter?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

beforeEach(() => {
  registryState.apps = []
  registryState.listeners.clear()
  shellState.sessionOpen.mockReset().mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
  shellState.sessionFetch.mockReset().mockResolvedValue({ Chunks: [], NextIdx: 0, Truncated: false })
  shellState.sessionWrite.mockReset().mockResolvedValue({})
  shellState.sessionResize.mockReset().mockResolvedValue({})
  shellState.sessionClose.mockReset().mockResolvedValue({})
  voiceState.setActive.mockReset().mockResolvedValue(undefined)
})

describe('WorkbenchSurface (card board)', () => {
  it('shows the launcher and no card when the board is empty, then summons the terminal', async () => {
    const { container } = renderSurface()
    await act(async () => {})
    expect(container.querySelector('.wb-launcher')).not.toBeNull()
    expect(container.querySelector('.wb-card')).toBeNull()
    expect(container.querySelector('.wb-term')).toBeNull()

    act(() => {
      container.querySelector<HTMLButtonElement>('.wb-launcher')!.click()
    })
    expect(container.querySelector('.wb-term')).not.toBeNull()
    await act(async () => {})
    expect(shellState.sessionOpen).toHaveBeenCalledTimes(1)
  })

  it('renders app cards: the leader mounts its iframe, the rest stay compact tiles', async () => {
    registryState.apps = [app('com.example.notes', 'Notes'), app('com.example.music', 'Music')]
    const { container } = renderSurface()
    await act(async () => {})

    const cards = container.querySelectorAll('.wb-card')
    expect(cards).toHaveLength(2)

    // The stable-order leader fills the main slot and mounts the plugin iframe.
    const leader = container.querySelector<HTMLElement>('.wb-card[data-zone="main"]')!
    expect(leader.getAttribute('data-card-id')).toBe('app:com.example.notes')
    expect(leader.querySelector('.plugin-iframe')?.getAttribute('data-plugin')).toBe('com.example.notes')

    // Non-focused cards are compact host-metadata tiles (no iframe).
    const side = container.querySelector<HTMLElement>('.wb-card[data-zone="side"]')!
    expect(side.querySelector('.wb-compact')).not.toBeNull()
    expect(side.querySelector('.plugin-iframe')).toBeNull()
  })

  it('summons the terminal on a keyword mention and retreats it on close', async () => {
    registryState.apps = [app('com.example.notes', 'Notes')]
    const { container } = renderSurface()
    await act(async () => {})

    const input = container.querySelector<HTMLInputElement>('.wb-dock-input')!
    typeInto(input, 'check the build output')
    act(() => {
      container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })
    expect(container.querySelector('.wb-term')).not.toBeNull()
    await act(async () => {})

    act(() => {
      container.querySelector<HTMLButtonElement>('.wb-card[data-kind="terminal"] .wb-card-close')!.click()
    })
    expect(container.querySelector('.wb-term')).toBeNull()
    expect(shellState.sessionClose).toHaveBeenCalledWith({}, { SessionId: 'sess-1' })
  })

  it('wires the dock mic to the voice API: results stream into the dock input', async () => {
    const { container } = renderSurface()
    await act(async () => {})

    const mic = container.querySelector<HTMLButtonElement>('.wb-mic')!
    act(() => { mic.click() })
    await act(async () => {})

    expect(container.querySelector('.wb-dock')!.className).toContain('listening')
    expect(voiceState.setActive).toHaveBeenCalledWith(true, expect.objectContaining({
      onResult: expect.any(Function),
    }))

    const opts = voiceState.setActive.mock.calls.find(c => c[0] === true)![1] as { onResult: (text: string) => void }
    act(() => { opts.onResult('hello') })
    const input = container.querySelector<HTMLInputElement>('.wb-dock-input')!
    expect(input.value).toBe('hello')
    act(() => { opts.onResult('world') })
    expect(input.value).toBe('hello world')

    act(() => { mic.click() })
    await act(async () => {})
    expect(voiceState.setActive).toHaveBeenLastCalledWith(false, undefined)
    expect(container.querySelector('.wb-dock')!.className).not.toContain('listening')
  })

  it('keeps card transitions off while the projection hydrates, then settles', async () => {
    vi.useFakeTimers()
    try {
      const { container } = renderSurface()
      const board = () => container.querySelector('.wb-board')
      // First paint happens before the projection answers (app/agent cards are
      // declared in the same beat) — no animation for that settle.
      expect(board()?.classList.contains('settling')).toBe(true)
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1700)
      })
      expect(board()?.classList.contains('settling')).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })

  it('reorders the board when a side card is promoted by a click', async () => {
    registryState.apps = [app('com.example.notes', 'Notes'), app('com.example.music', 'Music')]
    const { container } = renderSurface()
    await act(async () => {})

    act(() => {
      container.querySelector<HTMLElement>('.wb-card[data-card-id="app:com.example.music"]')!.click()
    })
    await act(async () => {})

    const leader = container.querySelector<HTMLElement>('.wb-card[data-zone="main"]')!
    expect(leader.getAttribute('data-card-id')).toBe('app:com.example.music')
    expect(leader.querySelector('.plugin-iframe')?.getAttribute('data-plugin')).toBe('com.example.music')
  })
})
