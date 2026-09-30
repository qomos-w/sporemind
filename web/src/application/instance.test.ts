import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { Transport } from '@qomos/gospore-client'
import type { ActiveConnection } from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/models'

// The Wails Events bridge is a native runtime we cannot exercise under
// happy-dom; capture handlers so tests can drive connections:active.
const eventListeners: Record<string, Array<(e: unknown) => void>> = {}

vi.mock('@wailsio/runtime', () => ({
  Events: {
    On: vi.fn((name: string, handler: (e: unknown) => void) => {
      ;(eventListeners[name] ||= []).push(handler)
      return () => {
        eventListeners[name] = (eventListeners[name] || []).filter((h) => h !== handler)
      }
    }),
  },
}))

vi.mock('../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  SwitchConnection: vi.fn(),
  GetActiveConnection: vi.fn(),
  GetGatewayAddr: vi.fn(async () => '127.0.0.1:18080'),
}))

vi.mock('./generated-client', () => ({
  createTransport: vi.fn(),
  rebindClient: vi.fn(),
}))

import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import { createTransport, rebindClient } from './generated-client'
import { recomputeRuntime } from './runtime'
import {
  __resetGatewayCacheForTests,
  getGatewayOverride,
  getGatewayUrls,
  setGatewayOverride,
} from './gateway'
import {
  __resetInstanceForTests,
  getActiveInstanceId,
  onInstanceSwap,
  registerAuthProviders,
  startInstanceTracking,
  switchInstance,
  type InstanceAuthProviders,
} from './instance'

const origWindow = globalThis.window

function setWindow(opts: { wails?: boolean; search?: string } = {}): void {
  const search = opts.search ?? ''
  const w: Record<string, unknown> = {
    location: {
      href: `http://app/${search}`,
      host: 'app:18080',
      origin: 'http://app:18080',
      protocol: 'http:',
      search,
    },
  }
  w.self = w
  w.top = w
  if (opts.wails) w.chrome = { webview: { postMessage: () => {} } }
  globalThis.window = w as unknown as Window & typeof globalThis
}

function emitActive(target: string): void {
  const evt = { data: { target } }
  for (const h of eventListeners['connections:active'] ?? []) h(evt)
}

function makeTransport(): Transport {
  return {
    invoke: async () => undefined,
    subscribe: () => (async function* () {})(),
    close: () => {},
  }
}

function remoteActive(id: string): ActiveConnection {
  return {
    target: id,
    local: false,
    name: `remote ${id}`,
    conn: {
      id,
      name: `remote ${id}`,
      host: '192.168.1.9',
      port: 18080,
      username: 'u',
      updatedAt: '',
      hasPassword: true,
    },
  } as unknown as ActiveConnection
}

interface AuthSpies {
  authenticateLocal: ReturnType<typeof vi.fn>
  authenticateRemote: ReturnType<typeof vi.fn>
  snapshotAuthState: ReturnType<typeof vi.fn>
  restoreAuthState: ReturnType<typeof vi.fn>
}

function installAuth(overrides: Partial<AuthSpies> = {}): AuthSpies {
  const spies: AuthSpies = {
    authenticateLocal: vi.fn(async () => {}),
    authenticateRemote: vi.fn(async () => {}),
    snapshotAuthState: vi.fn(() => ({ token: 'old-token' })),
    restoreAuthState: vi.fn(),
    ...overrides,
  }
  registerAuthProviders(spies as unknown as InstanceAuthProviders)
  return spies
}

beforeEach(() => {
  setWindow({ search: '' })
  recomputeRuntime()
  vi.clearAllMocks()
  vi.mocked(createTransport).mockImplementation(() => makeTransport())
  vi.mocked(desktop.SwitchConnection).mockResolvedValue(undefined)
  vi.mocked(desktop.GetActiveConnection).mockResolvedValue(remoteActive('c1'))
  __resetGatewayCacheForTests()
  __resetInstanceForTests()
  // Delete after reset: cancelling the tracked listener may leave an empty key.
  for (const k of Object.keys(eventListeners)) delete eventListeners[k]
})

afterEach(() => {
  globalThis.window = origWindow
  recomputeRuntime()
  vi.restoreAllMocks()
})

describe('initial active instance', () => {
  it('defaults to local', () => {
    setWindow({ search: '' })
    __resetInstanceForTests()
    expect(getActiveInstanceId()).toBe('local')
  })

  it('parses ?conn= as a remote instance id', () => {
    setWindow({ search: '?conn=abc123' })
    __resetInstanceForTests()
    expect(getActiveInstanceId()).toBe('abc123')
  })
})

describe('gateway override precedence', () => {
  it('beats ?server= and is cleared by setGatewayOverride(null)', () => {
    setWindow({ search: '?server=http%3A%2F%2Fparam-host%3A18080' })
    recomputeRuntime()
    __resetGatewayCacheForTests()
    expect(getGatewayUrls().source).toBe('query-param')

    setGatewayOverride('ws://override.example:9999/ws')
    const urls = getGatewayUrls()
    expect(urls.source).toBe('override')
    expect(urls.ws).toBe('ws://override.example:9999/ws')
    expect(urls.http).toBe('http://override.example:9999')

    setGatewayOverride(null)
    expect(getGatewayUrls().source).toBe('query-param')
  })
})

describe('switchInstance', () => {
  it('swaps to a remote instance: host call, override, rebind, auth, notify', async () => {
    const auth = installAuth()
    const swap = vi.fn()
    onInstanceSwap(swap)

    await switchInstance('c1')

    expect(desktop.SwitchConnection).toHaveBeenCalledWith('c1')
    expect(getActiveInstanceId()).toBe('c1')
    expect(getGatewayOverride()).toBe('ws://192.168.1.9:18080/ws')
    expect(rebindClient).toHaveBeenCalledTimes(1)
    expect(auth.authenticateRemote).toHaveBeenCalledWith('c1')
    expect(auth.authenticateLocal).not.toHaveBeenCalled()
    expect(swap).toHaveBeenCalledWith({ from: 'local', to: 'c1' })
  })

  it('clears the override and authenticates local when switching back', async () => {
    const auth = installAuth()
    await switchInstance('c1')

    await switchInstance('local')

    expect(desktop.SwitchConnection).toHaveBeenLastCalledWith('local')
    expect(getActiveInstanceId()).toBe('local')
    expect(getGatewayOverride()).toBeNull()
    expect(auth.authenticateLocal).toHaveBeenCalledTimes(1)
    expect(rebindClient).toHaveBeenCalledTimes(2)
  })

  it('is a no-op when the target is already active', async () => {
    installAuth()

    await switchInstance('local')

    expect(desktop.SwitchConnection).not.toHaveBeenCalled()
    expect(rebindClient).not.toHaveBeenCalled()
  })

  it('rolls back and leaves state untouched when the host refuses the switch', async () => {
    const auth = installAuth()
    vi.mocked(desktop.SwitchConnection).mockRejectedValue(new Error('cannot reach host'))

    await expect(switchInstance('c1')).rejects.toThrow('cannot reach host')

    expect(getActiveInstanceId()).toBe('local')
    expect(getGatewayOverride()).toBeNull()
    expect(rebindClient).not.toHaveBeenCalled()
    expect(auth.authenticateRemote).not.toHaveBeenCalled()
    expect(auth.restoreAuthState).not.toHaveBeenCalled()
  })

  it('rolls back override, client and auth when authentication fails', async () => {
    const auth = installAuth({
      authenticateRemote: vi.fn(async () => {
        throw new Error('auth boom')
      }),
    })

    await expect(switchInstance('c1')).rejects.toThrow('auth boom')

    expect(getActiveInstanceId()).toBe('local')
    expect(getGatewayOverride()).toBeNull()
    // forward rebind + rollback rebuild
    expect(rebindClient).toHaveBeenCalledTimes(2)
    expect(auth.snapshotAuthState).toHaveBeenCalledTimes(1)
    expect(auth.restoreAuthState).toHaveBeenCalledWith({ token: 'old-token' })
  })

  it('restores the previous remote override when a second remote swap fails', async () => {
    setWindow({ search: '?conn=c1' })
    recomputeRuntime()
    __resetInstanceForTests()
    expect(getActiveInstanceId()).toBe('c1')
    setGatewayOverride('ws://192.168.1.9:18080/ws')
    installAuth({
      authenticateRemote: vi.fn(async () => {
        throw new Error('boom')
      }),
    })
    vi.mocked(desktop.GetActiveConnection).mockResolvedValue(remoteActive('c2'))

    await expect(switchInstance('c2')).rejects.toThrow('boom')

    expect(getActiveInstanceId()).toBe('c1')
    expect(getGatewayOverride()).toBe('ws://192.168.1.9:18080/ws')
  })
})

describe('connections:active host sync', () => {
  it('follows a switch initiated elsewhere', async () => {
    setWindow({ wails: true, search: '' })
    recomputeRuntime()
    __resetInstanceForTests()
    const auth = installAuth()
    vi.mocked(desktop.GetActiveConnection).mockResolvedValue(remoteActive('c2'))

    startInstanceTracking()
    emitActive('c2')

    await vi.waitFor(() => expect(getActiveInstanceId()).toBe('c2'))
    expect(desktop.SwitchConnection).toHaveBeenCalledWith('c2')
    expect(auth.authenticateRemote).toHaveBeenCalledWith('c2')
  })

  it('ignores an event for the instance already active', async () => {
    setWindow({ wails: true, search: '' })
    recomputeRuntime()
    __resetInstanceForTests()
    installAuth()

    startInstanceTracking()
    emitActive('local')
    await Promise.resolve()

    expect(desktop.SwitchConnection).not.toHaveBeenCalled()
  })

  it('does not subscribe outside Wails mode', () => {
    __resetInstanceForTests()
    startInstanceTracking()
    expect(Object.keys(eventListeners)).toHaveLength(0)
  })
})

describe('onInstanceSwap', () => {
  it('stops delivering after unsubscribe', async () => {
    installAuth()
    const cb = vi.fn()
    const off = onInstanceSwap(cb)
    off()

    await switchInstance('c1')

    expect(cb).not.toHaveBeenCalled()
  })
})
