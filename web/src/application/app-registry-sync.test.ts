import { describe, expect, it, vi, beforeEach } from 'vitest'
import { startAppRegistrySync } from './app-registry-sync'
import { appRegistry } from './app-registry'
import type { GosporeClient } from '@qomos/gospore-client'
import type { ReconnectState } from './generated-client'

let stateCallbacks: Array<(state: ReconnectState) => void> = []

vi.mock('./generated-client', () => ({
  onReconnectStateChange: vi.fn((cb: (state: ReconnectState) => void) => {
    stateCallbacks.push(cb)
    return () => {
      stateCallbacks = stateCallbacks.filter((c) => c !== cb)
    }
  }),
}))

vi.mock('./app-registry', () => ({
  appRegistry: {
    load: vi.fn(),
    watch: vi.fn(),
    watchLifecycle: vi.fn(),
    clear: vi.fn(),
  },
}))

function emitState(state: ReconnectState) {
  for (const cb of stateCallbacks) cb(state)
}

const fakeClient = {} as GosporeClient

describe('startAppRegistrySync', () => {
  beforeEach(() => {
    stateCallbacks = []
    vi.mocked(appRegistry.load).mockReset()
    vi.mocked(appRegistry.watch).mockReset()
    vi.mocked(appRegistry.watchLifecycle).mockReset()
  })

  it('loads and watches on first connect', async () => {
    let watchResolve: (() => void) | undefined
    vi.mocked(appRegistry.watch).mockImplementation(() => new Promise<void>((resolve) => { watchResolve = resolve }))
    vi.mocked(appRegistry.watchLifecycle).mockImplementation(() => new Promise<void>(() => {}))

    const stop = startAppRegistrySync(fakeClient)
    emitState('connected')

    await vi.waitFor(() => expect(appRegistry.load).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(appRegistry.watch).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(appRegistry.watchLifecycle).toHaveBeenCalledTimes(1))

    stop()
    watchResolve?.()
  })

  it('subscribes the lifecycle watch before loading the snapshot', async () => {
    // Lifecycle events have no replay: the subscription must be initiated
    // first so a reloaded event fired between subscribe and the snapshot
    // fetch is not lost. Both calls happen in the same synchronous block of
    // runLoop; assert the invocation order.
    let watchResolve: (() => void) | undefined
    vi.mocked(appRegistry.watch).mockImplementation(() => new Promise<void>((resolve) => { watchResolve = resolve }))
    vi.mocked(appRegistry.watchLifecycle).mockImplementation(() => new Promise<void>(() => {}))
    vi.mocked(appRegistry.load).mockResolvedValue(undefined)

    const stop = startAppRegistrySync(fakeClient)
    emitState('connected')

    await vi.waitFor(() => expect(appRegistry.load).toHaveBeenCalledTimes(1))
    const lifecycleOrder = vi.mocked(appRegistry.watchLifecycle).mock.invocationCallOrder[0]
    const loadOrder = vi.mocked(appRegistry.load).mock.invocationCallOrder[0]
    expect(lifecycleOrder).toBeDefined()
    expect(loadOrder).toBeDefined()
    expect(lifecycleOrder!).toBeLessThan(loadOrder!)

    stop()
    watchResolve?.()
  })

  it('reloads and re-watches after reconnect', async () => {
    let watchResolve: (() => void) | undefined
    vi.mocked(appRegistry.watch).mockImplementation(() => new Promise<void>((resolve) => { watchResolve = resolve }))
    vi.mocked(appRegistry.watchLifecycle).mockImplementation(() => new Promise<void>(() => {}))

    const onStateChange = vi.fn()
    const stop = startAppRegistrySync(fakeClient, { onStateChange })

    emitState('connected')
    await vi.waitFor(() => expect(appRegistry.load).toHaveBeenCalledTimes(1))

    emitState('reconnecting')
    expect(onStateChange).toHaveBeenLastCalledWith('reconnecting')

    emitState('connected')
    await vi.waitFor(() => expect(appRegistry.load).toHaveBeenCalledTimes(2))

    stop()
    watchResolve?.()
  })
})
