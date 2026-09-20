import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { HTTPTransport } from '@qomos/gospore-client'

describe('HTTPTransport', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    fetchSpy = vi.spyOn(globalThis, 'fetch')
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('sends POST to /invoke with correct body shape', async () => {
    fetchSpy.mockResolvedValue({
      ok: true,
      text: async () => '{"result":1}',
    } as Response)

    const transport = new HTTPTransport({ baseURL: 'http://localhost:18080' })
    await transport.invoke('workspace.list_project', undefined)

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    const [url, init] = fetchSpy.mock.calls[0]!
    expect(url).toBe('http://localhost:18080/invoke')
    expect((init as RequestInit).method).toBe('POST')
    expect((init as RequestInit).headers).toMatchObject({
      'Content-Type': 'application/json',
    })
    const body = JSON.parse((init as RequestInit).body as string)
    expect(body).toMatchObject({ callID: 'workspace.list_project' })
  })

  it('includes custom headers on every request', async () => {
    fetchSpy.mockResolvedValue({
      ok: true,
      text: async () => '',
    } as Response)

    const transport = new HTTPTransport({
      baseURL: 'http://localhost:18080',
      headers: { 'X-Role': 'admin' },
    })
    await transport.invoke('workspace.mount', { path: '/tmp' })

    const [, init] = fetchSpy.mock.calls[0]!
    expect((init as RequestInit).headers).toMatchObject({
      'Content-Type': 'application/json',
      'X-Role': 'admin',
    })
  })

  it('throws on HTTP error status', async () => {
    fetchSpy.mockResolvedValue({
      ok: false,
      status: 403,
      text: async () => 'forbidden',
    } as Response)

    const transport = new HTTPTransport({ baseURL: 'http://localhost:18080' })
    await expect(transport.invoke('workspace.mount', {}))
      .rejects.toThrow('invoke workspace.mount failed: 403 forbidden')
  })

  it('throws a descriptive error on timeout', async () => {
    // Mock fetch that respects AbortSignal so the timeout path fires.
    fetchSpy.mockImplementation((_url: string, init: RequestInit) => {
      return new Promise((_resolve, reject) => {
        const signal = (init as RequestInit).signal
        if (signal?.aborted) {
          reject(new DOMException('Aborted', 'AbortError'))
          return
        }
        signal?.addEventListener('abort', () => {
          reject(new DOMException('Aborted', 'AbortError'))
        })
        // Never resolve otherwise — the timeout will abort the signal.
      })
    })

    const transport = new HTTPTransport({
      baseURL: 'http://localhost:18080',
      timeoutMs: 50,
    })

    await expect(transport.invoke('workspace.mount', {}))
      .rejects.toThrow('timed out after 50ms')
  })

  it('returns null on empty response body', async () => {
    fetchSpy.mockResolvedValue({
      ok: true,
      text: async () => '',
    } as Response)

    const transport = new HTTPTransport({ baseURL: 'http://localhost:18080' })
    const result = await transport.invoke('workspace.list_project', undefined)
    expect(result).toBeNull()
  })

  it('strips trailing slash from baseURL', async () => {
    fetchSpy.mockResolvedValue({
      ok: true,
      text: async () => '',
    } as Response)

    const transport = new HTTPTransport({ baseURL: 'http://localhost:18080/' })
    await transport.invoke('test', undefined)

    const [url] = fetchSpy.mock.calls[0]!
    expect(url).toBe('http://localhost:18080/invoke')
  })
})
