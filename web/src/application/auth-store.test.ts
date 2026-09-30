import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  clearAuth,
  tryWailsAutoLogin,
  authenticateRemote,
  snapshotAuthState,
  restoreAuthState,
  getToken,
  getRefreshToken,
  getAccount,
  setToken,
  setRefreshToken,
  setAccount,
} from './auth-store'
import { waitWailsReady, connectClient, waitForClientReady } from './generated-client'
import { GetAdminToken, RemoteAuthLogin } from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import { authMe } from '../gen-clients/user/client'

vi.mock('./generated-client', () => ({
  client: { invoke: vi.fn(), getTransport: vi.fn(() => ({ connect: vi.fn() })) },
  waitWailsReady: vi.fn(),
  connectClient: vi.fn(),
  waitForClientReady: vi.fn(),
  AUTH_CONNECTION_TIMEOUT_MS: 10_000,
}))

vi.mock('../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  GetAdminToken: vi.fn(),
  RemoteAuthLogin: vi.fn(),
}))

vi.mock('../gen-clients/user/client', () => ({
  authMe: vi.fn(),
}))

describe('tryWailsAutoLogin', () => {
  beforeEach(() => {
    clearAuth()
    vi.clearAllMocks()
  })

  it('returns false when not in Wails mode', async () => {
    vi.mocked(waitWailsReady).mockResolvedValue(false)

    const result = await tryWailsAutoLogin()

    expect(result).toBe(false)
    expect(getToken()).toBeNull()
    expect(getAccount()).toBeNull()
  })

  it('succeeds when Wails binding returns a valid token', async () => {
    vi.mocked(waitWailsReady).mockResolvedValue(true)
    vi.mocked(GetAdminToken).mockResolvedValue('wails-token')
    vi.mocked(waitForClientReady).mockResolvedValue(undefined)
    vi.mocked(authMe).mockResolvedValue({ id: '1', username: 'admin' } as any)

    const result = await tryWailsAutoLogin()

    expect(result).toBe(true)
    expect(getToken()).toBe('wails-token')
    expect(getAccount()).toEqual({ id: '1', username: 'admin' })
    expect(connectClient).toHaveBeenCalledTimes(1)
    expect(waitForClientReady).toHaveBeenCalledTimes(1)
    expect(authMe).toHaveBeenCalledTimes(1)
  })

  it('throws when Wails binding returns empty string', async () => {
    vi.mocked(waitWailsReady).mockResolvedValue(true)
    vi.mocked(GetAdminToken).mockResolvedValue('')

    await expect(tryWailsAutoLogin()).rejects.toThrow(
      'GetAdminToken returned empty token in Wails mode'
    )
    expect(getToken()).toBeNull()
    expect(connectClient).not.toHaveBeenCalled()
  })

  it('throws when Wails binding returns null', async () => {
    vi.mocked(waitWailsReady).mockResolvedValue(true)
    vi.mocked(GetAdminToken).mockResolvedValue(null as any)

    await expect(tryWailsAutoLogin()).rejects.toThrow(
      'GetAdminToken returned empty token in Wails mode'
    )
    expect(getToken()).toBeNull()
  })

  it('throws when Wails binding throws', async () => {
    vi.mocked(waitWailsReady).mockResolvedValue(true)
    vi.mocked(GetAdminToken).mockRejectedValue(new Error('user actor not found'))

    await expect(tryWailsAutoLogin()).rejects.toThrow('user actor not found')
    expect(getToken()).toBeNull()
  })

  it('throws when WebSocket connection fails', async () => {
    vi.mocked(waitWailsReady).mockResolvedValue(true)
    vi.mocked(GetAdminToken).mockResolvedValue('wails-token')
    vi.mocked(waitForClientReady).mockRejectedValue(new Error('ws auth failed'))

    await expect(tryWailsAutoLogin()).rejects.toThrow('ws auth failed')
    expect(getToken()).toBe('wails-token')
    expect(authMe).not.toHaveBeenCalled()
  })

  it('keeps token when me() fails', async () => {
    vi.mocked(waitWailsReady).mockResolvedValue(true)
    vi.mocked(GetAdminToken).mockResolvedValue('wails-token')
    vi.mocked(waitForClientReady).mockResolvedValue(undefined)
    vi.mocked(authMe).mockRejectedValue(new Error('me failed'))

    const result = await tryWailsAutoLogin()

    expect(result).toBe(true)
    expect(getToken()).toBe('wails-token')
    expect(getAccount()).toMatchObject({
      Id: 'local-admin',
      Username: 'admin',
      DisplayName: 'Admin (Desktop)',
      Roles: ['admin'],
    })
  })
})

describe('authenticateRemote', () => {
  beforeEach(() => {
    clearAuth()
    vi.clearAllMocks()
  })

  it('applies tokens from the host-side login for the given connection id', async () => {
    vi.mocked(RemoteAuthLogin).mockResolvedValue({
      Token: 'remote-token',
      RefreshToken: 'remote-refresh',
      Account: { Username: 'admin', Roles: ['admin'] },
    } as any)
    vi.mocked(authMe).mockResolvedValue({ id: '1', username: 'admin' } as any)

    const ok = await authenticateRemote('c1')

    expect(ok).toBe(true)
    expect(RemoteAuthLogin).toHaveBeenCalledWith('c1')
    expect(getToken()).toBe('remote-token')
    expect(getRefreshToken()).toBe('remote-refresh')
    expect(connectClient).toHaveBeenCalledTimes(1)
  })

  it('returns false when the host-side login is rejected', async () => {
    vi.mocked(RemoteAuthLogin).mockRejectedValue(new Error('401'))

    const ok = await authenticateRemote('c1')

    expect(ok).toBe(false)
    expect(getToken()).toBeNull()
    expect(connectClient).not.toHaveBeenCalled()
  })

  it('returns false when the host-side login yields no token', async () => {
    vi.mocked(RemoteAuthLogin).mockResolvedValue({ Token: '' } as any)

    expect(await authenticateRemote('c1')).toBe(false)
    expect(getToken()).toBeNull()
  })
})

describe('snapshotAuthState / restoreAuthState', () => {
  beforeEach(() => {
    clearAuth()
  })

  it('round-trips token, refresh token and account', () => {
    setToken('t')
    setRefreshToken('r')
    setAccount({ Id: 'a' } as any)

    const snap = snapshotAuthState()
    clearAuth()

    restoreAuthState(snap)

    expect(getToken()).toBe('t')
    expect(getRefreshToken()).toBe('r')
    expect(getAccount()).toEqual({ Id: 'a' })
  })

  it('ignores a null snapshot', () => {
    setToken('t')
    restoreAuthState(null)
    expect(getToken()).toBe('t')
  })
})

