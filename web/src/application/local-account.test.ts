import { describe, it, expect, beforeEach, vi } from 'vitest'

vi.mock('../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  GetGatewayAddr: vi.fn(async () => '127.0.0.1:18080'),
  GetAdminToken: vi.fn(async () => 'local-admin-token'),
}))

import {
  localAccountSnapshot,
  localCloudStatus,
  localCloudUnlink,
  localCloudLink,
  __resetLocalAccountCacheForTests,
} from './local-account'

function lastCall(): { url: string; init: RequestInit } {
  return (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.at(-1) as [{ url: string; init: RequestInit }] extends never ? never : { url: string; init: RequestInit }
}

describe('local-account (remote-window local login channel)', () => {
  beforeEach(() => {
    __resetLocalAccountCacheForTests()
    vi.restoreAllMocks()
    const calls: Array<{ url: string; init: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init })
      ;(fetch as unknown as { _calls: typeof calls })._calls = calls
      return new Response(JSON.stringify({ ok: true }), { status: 200 })
    }))
  })

  it('POSTs to the LOCAL gateway with a fresh admin bearer token', async () => {
    await localAccountSnapshot()
    const calls = (fetch as unknown as { _calls: Array<{ url: string; init: RequestInit }> })._calls
    expect(calls).toHaveLength(1)
    expect(calls[0].url).toBe('http://127.0.0.1:18080/api/workspace.account')
    expect(calls[0].init.method).toBe('POST')
    expect((calls[0].init.headers as Record<string, string>).Authorization).toBe('Bearer local-admin-token')
  })

  it('routes cloud status/unlink/link callables locally with request bodies', async () => {
    await localCloudStatus()
    await localCloudUnlink()
    await localCloudLink({ AccountId: 'a', AccessToken: 't', RefreshToken: 'r', ExpiresIn: 0 })
    const calls = (fetch as unknown as { _calls: Array<{ url: string; init: RequestInit }> })._calls
    expect(calls.map(c => c.url)).toEqual([
      'http://127.0.0.1:18080/api/cloudaccount.status',
      'http://127.0.0.1:18080/api/cloudaccount.unlink',
      'http://127.0.0.1:18080/api/cloudaccount.link',
    ])
    expect(JSON.parse(String(calls[2].init.body))).toMatchObject({ AccountId: 'a' })
  })

  it('surfaces non-200 responses as errors', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('nope', { status: 403 })))
    __resetLocalAccountCacheForTests()
    await expect(localCloudStatus()).rejects.toThrow('cloudaccount.status failed (HTTP 403)')
  })
})
