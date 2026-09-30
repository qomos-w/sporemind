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

interface FetchCall { url: string; init: RequestInit }
let calls: FetchCall[]

describe('local-account (remote-window local login channel)', () => {
  beforeEach(() => {
    __resetLocalAccountCacheForTests()
    vi.restoreAllMocks()
    calls = []
    vi.stubGlobal('fetch', vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(url), init: init ?? {} })
      return new Response(JSON.stringify({ ok: true }), { status: 200 })
    }))
  })

  it('POSTs to the LOCAL gateway with a fresh admin bearer token', async () => {
    await localAccountSnapshot()
    expect(calls).toHaveLength(1)
    expect(calls[0]?.url).toBe('http://127.0.0.1:18080/api/workspace.account')
    expect(calls[0]?.init.method).toBe('POST')
    expect((calls[0]?.init.headers as Record<string, string>).Authorization).toBe('Bearer local-admin-token')
  })

  it('routes cloud status/unlink/link callables locally with request bodies', async () => {
    await localCloudStatus()
    await localCloudUnlink()
    await localCloudLink({ AccountId: 'a', AccessToken: 't', RefreshToken: 'r', ExpiresIn: 0 })
    expect(calls.map(c => c.url)).toEqual([
      'http://127.0.0.1:18080/api/cloudaccount.status',
      'http://127.0.0.1:18080/api/cloudaccount.unlink',
      'http://127.0.0.1:18080/api/cloudaccount.link',
    ])
    expect(JSON.parse(String(calls[2]?.init.body))).toMatchObject({ AccountId: 'a' })
  })

  it('surfaces non-200 responses as errors', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('nope', { status: 403 })))
    __resetLocalAccountCacheForTests()
    await expect(localCloudStatus()).rejects.toThrow('cloudaccount.status failed (HTTP 403)')
  })
})
