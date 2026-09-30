import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { recomputeRuntime } from './runtime'

vi.mock('../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  GetAdminToken: vi.fn(),
  RemoteAuthLogin: vi.fn(),
  ConnectionsList: vi.fn(),
  ConnectionsSave: vi.fn(),
  ConnectionsDelete: vi.fn(),
  ConnectionsProbe: vi.fn(),
  SwitchConnection: vi.fn(),
  GetActiveConnection: vi.fn(),
}))

vi.mock('./generated-client', () => ({
  client: {},
  waitWailsReady: vi.fn(),
  connectClient: vi.fn(async () => {}),
  waitForClientReady: vi.fn(async () => {}),
  AUTH_CONNECTION_TIMEOUT_MS: 10_000,
}))

vi.mock('../gen-clients/user/client', () => ({
  authMe: vi.fn(async () => ({ Id: 'u1', Username: 'admin', Roles: ['admin'] })),
}))

import { remoteConnectionId, isRemoteConnectionWindow } from './remote-connections'
import { useWailsRawTransport } from './transport-selection'
import { tryWailsAutoLogin, tryRemoteAutoLogin, clearAuth, getToken, getRefreshToken } from './auth-store'
import * as wailsApp from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'

const origWindow = globalThis.window

function setWindow(opts: { wails?: boolean; search?: string }): void {
  const w: Record<string, unknown> = {
    location: { href: 'http://app/', host: 'app', origin: 'http://app', protocol: 'http:', search: opts.search ?? '' },
    self: null,
    top: null,
  }
  w.self = w
  w.top = w
  if (opts.wails) {
    w.chrome = { webview: { postMessage: () => {} } }
  }
  globalThis.window = w as unknown as Window & typeof globalThis
  recomputeRuntime()
}

describe('remote-connection window detection', () => {
  beforeEach(() => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(console, 'debug').mockImplementation(() => {})
  })

  afterEach(() => {
    globalThis.window = origWindow
    vi.restoreAllMocks()
  })

  it('detects a remote-connection window (wails + ?server= + ?conn=)', () => {
    setWindow({ wails: true, search: '?server=ws%3A%2F%2F192.168.1.5%3A18080%2Fws&conn=abc123' })
    expect(isRemoteConnectionWindow()).toBe(true)
    expect(remoteConnectionId()).toBe('abc123')
  })

  it('main local window is not a remote-connection window', () => {
    setWindow({ wails: true, search: '' })
    expect(isRemoteConnectionWindow()).toBe(false)
    expect(remoteConnectionId()).toBeNull()
  })

  it('?server= without ?conn= (mobile iframe contract) is not a remote-connection window', () => {
    setWindow({ wails: true, search: '?server=ws%3A%2F%2F192.168.1.5%3A18080%2Fws' })
    expect(isRemoteConnectionWindow()).toBe(false)
  })

  it('non-wails web windows are never remote-connection windows', () => {
    setWindow({ wails: false, search: '?server=ws%3A%2F%2Fx%2Fws&conn=abc' })
    expect(isRemoteConnectionWindow()).toBe(false)
  })
})

describe('transport selection', () => {
  afterEach(() => {
    globalThis.window = origWindow
    vi.restoreAllMocks()
    recomputeRuntime()
  })

  it('?server= forces WebSocket transport even in wails mode', () => {
    setWindow({ wails: true, search: '?server=ws%3A%2F%2Fremote%3A18080%2Fws&conn=abc' })
    expect(useWailsRawTransport()).toBe(false)
  })

  it('local wails window without desktop config uses the raw transport', () => {
    setWindow({ wails: true, search: '' })
    expect(useWailsRawTransport()).toBe(true)
  })

  it('local wails window with transport=ws config uses WebSocket', async () => {
    const desktopConfig = await import('./desktop-config')
    vi.spyOn(desktopConfig, 'getDesktopConfig').mockReturnValue({ transport: 'ws' } as never)
    setWindow({ wails: true, search: '' })
    expect(useWailsRawTransport()).toBe(false)
  })
})

describe('auth gating in remote-connection windows', () => {
  beforeEach(() => {
    clearAuth()
    vi.spyOn(console, 'log').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
  })

  afterEach(() => {
    globalThis.window = origWindow
    vi.restoreAllMocks()
  })

  it('tryWailsAutoLogin never mints a local admin token in a remote window', async () => {
    setWindow({ wails: true, search: '?server=ws%3A%2F%2Fremote%3A18080%2Fws&conn=abc' })
    const ok = await tryWailsAutoLogin()
    expect(ok).toBe(false)
    expect(wailsApp.GetAdminToken).not.toHaveBeenCalled()
  })

  it('tryRemoteAutoLogin applies tokens from the host-side login', async () => {
    setWindow({ wails: true, search: '?server=ws%3A%2F%2Fremote%3A18080%2Fws&conn=abc' })
    ;(wailsApp.RemoteAuthLogin as ReturnType<typeof vi.fn>).mockResolvedValue({
      Token: 'remote-token',
      RefreshToken: 'remote-refresh',
      Account: { Username: 'admin', Roles: ['admin'] },
    })
    const ok = await tryRemoteAutoLogin()
    expect(ok).toBe(true)
    expect(wailsApp.RemoteAuthLogin).toHaveBeenCalledWith('abc')
    expect(getToken()).toBe('remote-token')
    expect(getRefreshToken()).toBe('remote-refresh')
  })

  it('tryRemoteAutoLogin falls back to manual login when credentials fail', async () => {
    setWindow({ wails: true, search: '?server=ws%3A%2F%2Fremote%3A18080%2Fws&conn=abc' })
    ;(wailsApp.RemoteAuthLogin as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('401'))
    const ok = await tryRemoteAutoLogin()
    expect(ok).toBe(false)
    expect(getToken()).toBeNull()
  })
})
