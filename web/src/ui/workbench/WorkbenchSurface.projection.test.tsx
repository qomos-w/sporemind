import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'

import { I18nProvider } from '../../i18n'
import { WorkbenchSurface } from './WorkbenchSurface'
import type { WorkbenchSnapshot } from '../../gen-clients/system/types'

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

// The workbench projection + write surface the board consumes.
const wbState = vi.hoisted(() => ({
  snapshot: { Cards: [], Generation: 0 } as any,
  agents: [] as any[],
  promote: vi.fn(),
  setPinned: vi.fn(),
  setHidden: vi.fn(),
  upsertCard: vi.fn(),
}))

vi.mock('../../gen-clients/workbench/projection-client', () => ({
  getSnapshot: async () => wbState.snapshot,
  // eslint-disable-next-line require-yield
  watchSnapshot: async function* () {},
}))

vi.mock('../../gen-clients/workbench/client', () => ({
  promote: (...a: unknown[]) => wbState.promote(...a),
  setPinned: (...a: unknown[]) => wbState.setPinned(...a),
  setHidden: (...a: unknown[]) => wbState.setHidden(...a),
  upsertCard: (...a: unknown[]) => wbState.upsertCard(...a),
}))

vi.mock('../../gen-clients/workspace/projection-client', () => ({
  getAgents: async () => wbState.agents,
  // eslint-disable-next-line require-yield
  watchAgents: async function* () {},
}))

vi.mock('../../gen-clients/shell/client', () => ({
  sessionOpen: vi.fn().mockResolvedValue({ SessionId: 's', Mode: 'pty' }),
  sessionFetch: vi.fn().mockResolvedValue({ Chunks: [], NextIdx: 0, Truncated: false }),
  sessionWrite: vi.fn().mockResolvedValue({}),
  sessionResize: vi.fn().mockResolvedValue({}),
  sessionClose: vi.fn().mockResolvedValue({}),
  OnShellSessionOutput: () => () => {},
}))

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    loadAddon() {}
    open() {}
    onData() {
      return { dispose() {} }
    }
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

function card(id: string, kind: string, score: number, slot: string) {
  return { Id: id, Kind: kind, Title: id, Icon: '', Score: score, Slot: slot, Pinned: false }
}

function snapshot(cards: unknown[], generation: number): WorkbenchSnapshot {
  return { Cards: cards, Generation: generation } as WorkbenchSnapshot
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

beforeEach(() => {
  registryState.apps = []
  registryState.listeners.clear()
  wbState.snapshot = snapshot([], 0)
  wbState.agents = []
  wbState.promote.mockReset().mockImplementation(async () => wbState.snapshot)
  wbState.setPinned.mockReset().mockImplementation(async () => wbState.snapshot)
  wbState.setHidden.mockReset().mockImplementation(async () => wbState.snapshot)
  wbState.upsertCard.mockReset().mockImplementation(async () => wbState.snapshot)
})

describe('WorkbenchSurface (projection-driven)', () => {
  it('declares app cards to the actor with real manifest metadata', async () => {
    registryState.apps = [app('com.example.notes', 'Notes')]
    renderSurface()
    await act(async () => {})

    expect(wbState.upsertCard).toHaveBeenCalledWith(
      {},
      expect.objectContaining({
        Id: 'app:com.example.notes',
        Kind: 'app',
        Title: 'Notes',
        Icon: 'notebook-pen',
        Color: '#c9b47f',
      }),
    )
  })

  it('routes a side-card click to workbench.promote and renders the returned attention', async () => {
    registryState.apps = [app('com.example.notes', 'Notes'), app('com.example.music', 'Music')]
    wbState.snapshot = snapshot(
      [card('app:com.example.notes', 'app', 30, 'main'), card('app:com.example.music', 'app', 20, 'side')],
      1,
    )
    const { container } = renderSurface()
    await act(async () => {})

    // The actor's leader occupies the main slot.
    expect(
      container.querySelector('.wb-card[data-zone="main"]')!.getAttribute('data-card-id'),
    ).toBe('app:com.example.notes')

    // The backend answers the promotion with a re-scored snapshot (46 > 30 + HYSTERESIS).
    wbState.promote.mockImplementation(async () =>
      snapshot(
        [card('app:com.example.notes', 'app', 30, 'side'), card('app:com.example.music', 'app', 46, 'main')],
        2,
      ),
    )

    act(() => {
      container.querySelector<HTMLElement>('.wb-card[data-card-id="app:com.example.music"]')!.click()
    })
    await act(async () => {})

    expect(wbState.promote).toHaveBeenCalledWith({}, { Id: 'app:com.example.music' })
    const leader = container.querySelector<HTMLElement>('.wb-card[data-zone="main"]')!
    expect(leader.getAttribute('data-card-id')).toBe('app:com.example.music')
    // The attribution line reflects the backend score, proving the projection drove the board.
    expect(leader.querySelector('.wb-card-attr')?.textContent).toContain('46')
  })

  it('fills an agent card title from the workspace Agents projection', async () => {
    wbState.agents = [{ Id: 'ag-1', ActorId: 'actor-1', DisplayName: 'Ada', AgentKind: 'general' }]
    wbState.snapshot = snapshot([{ Id: 'agent:actor-1', Kind: 'chat', Title: 'Agent', Icon: 'bot', Score: 5, Slot: 'side', Pinned: false }], 1)
    renderSurface()
    await act(async () => {})
    await act(async () => {})

    expect(wbState.upsertCard).toHaveBeenCalledWith(
      {},
      expect.objectContaining({ Id: 'agent:actor-1', Kind: 'chat', Title: 'Ada' }),
    )
  })

  it('fills titles from the lowerFirst projection wire shape (gospore snapshotStruct contract)', async () => {
    // Production wire: the workspace Agents projection and the workbench
    // Snapshot component both carry lowerFirst field keys.
    wbState.agents = [{ id: 'ag-1', actorId: 'actor-1', displayName: 'Grace', agentKind: 'general' }] as any
    wbState.snapshot = {
      Cards: [{ Id: 'agent:actor-1', Kind: 'chat', Title: 'Agent', Icon: 'bot', Score: 5, Slot: 'side', Pinned: false }],
      Generation: 1,
    } as any
    renderSurface()
    await act(async () => {})
    await act(async () => {})

    expect(wbState.upsertCard).toHaveBeenCalledWith(
      {},
      expect.objectContaining({ Id: 'agent:actor-1', Title: 'Grace' }),
    )
  })

  it('shows the terminal card when the actor summons it (step keyword loop)', async () => {
    registryState.apps = [app('com.example.notes', 'Notes')]
    wbState.snapshot = snapshot(
      [card('app:com.example.notes', 'app', 10, 'main'), { Id: 'terminal', Kind: 'terminal', Title: 'Terminal', Icon: 'terminal', Score: 34, Slot: 'term', Pinned: false }],
      1,
    )
    const { container } = renderSurface()
    await act(async () => {})

    expect(container.querySelector('.wb-term')).not.toBeNull()
    expect(container.querySelector('.wb-card[data-kind="terminal"][data-zone="term"]')).not.toBeNull()
  })

  it('renders a projected chat card as a compact session tile, expands it, and jumps to the conversation', async () => {
    wbState.agents = [
      { Id: 'ag-1', ActorId: 'actor-1', DisplayName: 'Ada', AgentKind: 'general', ProjectId: 'proj-1', Status: 'running' },
    ]
    wbState.snapshot = snapshot(
      [
        card('app:com.example.notes', 'app', 30, 'main'),
        { Id: 'agent:actor-1', Kind: 'chat', Title: 'Ada', Icon: 'bot', Score: 12, Slot: 'side', Pinned: false },
      ],
      1,
    )
    const { container } = renderSurface()
    await act(async () => {})
    await act(async () => {})

    // The chat card renders as a compact host-metadata tile on the side band.
    const chat = container.querySelector<HTMLElement>('.wb-card[data-kind="chat"]')!
    expect(chat).not.toBeNull()
    expect(chat.getAttribute('data-card-id')).toBe('agent:actor-1')
    expect(chat.getAttribute('data-zone')).toBe('side')
    expect(chat.querySelector('.wb-compact')).not.toBeNull()

    // Clicking the compact tile promotes it through the actor (attention owner).
    wbState.promote.mockImplementation(async () => {
      const promoted = snapshot(
        [
          card('app:com.example.notes', 'app', 30, 'side'),
          { Id: 'agent:actor-1', Kind: 'chat', Title: 'Ada', Icon: 'bot', Score: 46, Slot: 'main', Pinned: false },
        ],
        2,
      )
      wbState.snapshot = promoted
      return promoted
    })
    await act(async () => {
      chat.click()
    })
    await act(async () => {})

    expect(wbState.promote).toHaveBeenCalledWith({}, { Id: 'agent:actor-1' })

    // The promoted chat card fills the main slot and mounts the session panel.
    const leader = container.querySelector<HTMLElement>('.wb-card[data-zone="main"]')!
    expect(leader.getAttribute('data-card-id')).toBe('agent:actor-1')
    const open = leader.querySelector<HTMLButtonElement>('.wb-chat-open')!
    expect(open).not.toBeNull()

    // The panel's jump entry drives the shell ContentMode switch contract.
    const handler = vi.fn()
    window.addEventListener('sporemind:open-agent-chat', handler)
    act(() => {
      open.click()
    })
    window.removeEventListener('sporemind:open-agent-chat', handler)
    expect(handler).toHaveBeenCalledTimes(1)
    expect((handler.mock.calls[0]![0] as CustomEvent).detail).toEqual({
      projectId: 'proj-1',
      agentActorId: 'actor-1',
    })
    await act(async () => {})
  })
})
