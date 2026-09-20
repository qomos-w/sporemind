import { describe, expect, it, vi, beforeEach } from 'vitest'
import { executeAppCommand } from './app-commands'
import * as appmanagerClient from '../gen-clients/appmanager/client'

vi.mock('./generated-client', () => ({
  client: {},
}))

vi.mock('../gen-clients/appmanager/client', () => ({
  invoke: vi.fn(),
}))

describe('executeAppCommand', () => {
  beforeEach(() => {
    vi.mocked(appmanagerClient.invoke).mockReset()
  })

  it('invokes the real appmanager.invoke callable with the command id', async () => {
    const payload = new Uint8Array([1, 2, 3])
    vi.mocked(appmanagerClient.invoke).mockResolvedValue({ Payload: payload })

    const result = await executeAppCommand('quick.demo', 'refresh')

    expect(appmanagerClient.invoke).toHaveBeenCalledTimes(1)
    const req = vi.mocked(appmanagerClient.invoke).mock.calls[0]![1]
    expect(req.Id).toBe('quick.demo')
    expect(req.Callable).toBe('refresh')
    // Command entrypoints carry no payload by design.
    expect(req.Payload).toBeInstanceOf(Uint8Array)
    expect(req.Payload.length).toBe(0)
    expect(result).toBe(payload)
  })

  it('returns empty bytes when the response payload is missing', async () => {
    vi.mocked(appmanagerClient.invoke).mockResolvedValue({ Payload: undefined as unknown as Uint8Array })

    const result = await executeAppCommand('quick.demo', 'noop')

    expect(result).toBeInstanceOf(Uint8Array)
    expect(result.length).toBe(0)
  })

  it('propagates backend errors (e.g. permission denied) to the caller', async () => {
    vi.mocked(appmanagerClient.invoke).mockRejectedValue(new Error('permission denied'))

    await expect(executeAppCommand('quick.demo', 'admin-only')).rejects.toThrow('permission denied')
  })
})
