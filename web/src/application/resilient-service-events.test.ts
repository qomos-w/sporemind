import { describe, it, expect, vi } from 'vitest'
import { onServiceEventResilient } from './resilient-service-events'
import type { GosporeClient } from '@qomos/gospore-client'

interface FakeSubscription {
  handler: (payload: unknown) => void
  cancelled: boolean
}

function fakeClient() {
  const subscriptions: FakeSubscription[] = []
  const connectedHandlers: Array<(info: { isReconnect: boolean }) => void> = []
  const client = {
    events: {
      onService: vi.fn((_service: string, _kind: string, handler: (payload: unknown) => void) => {
        const sub: FakeSubscription = { handler, cancelled: false }
        subscriptions.push(sub)
        return () => { sub.cancelled = true }
      }),
    },
    onConnected: vi.fn((h: (info: { isReconnect: boolean }) => void) => {
      connectedHandlers.push(h)
      return () => {
        const idx = connectedHandlers.indexOf(h)
        if (idx >= 0) connectedHandlers.splice(idx, 1)
      }
    }),
  }
  const fireConnected = () => { for (const h of [...connectedHandlers]) h({ isReconnect: true }) }
  const liveSubscriptions = () => subscriptions.filter(s => !s.cancelled)
  return { client: client as unknown as GosporeClient, subscriptions, fireConnected, liveSubscriptions }
}

describe('onServiceEventResilient', () => {
  it('subscribes immediately and forwards payloads to the handler', () => {
    const { client, liveSubscriptions } = fakeClient()
    const handler = vi.fn()
    onServiceEventResilient(client, 'browsermanager', 'browser_manager_event', handler)

    expect(liveSubscriptions()).toHaveLength(1)
    liveSubscriptions()[0]!.handler({ Kind: 'open_global' })
    expect(handler).toHaveBeenCalledWith({ Kind: 'open_global' })
  })

  it('re-issues the subscription on every transport connect epoch (reconnect)', () => {
    const { client, fireConnected, liveSubscriptions } = fakeClient()
    const handler = vi.fn()
    const cancel = onServiceEventResilient(client, 'browsermanager', 'browser_manager_event', handler)

    fireConnected()
    fireConnected()

    // Old registrations are cancelled and exactly one live subscription remains.
    expect(liveSubscriptions()).toHaveLength(1)
    liveSubscriptions()[0]!.handler({ Kind: 'created' })
    expect(handler).toHaveBeenCalledWith({ Kind: 'created' })
    cancel()
  })

  it('stops resubscribing after dispose', () => {
    const { client, fireConnected, liveSubscriptions } = fakeClient()
    const handler = vi.fn()
    const cancel = onServiceEventResilient(client, 'browsermanager', 'browser_manager_event', handler)
    cancel()

    fireConnected()
    expect(liveSubscriptions()).toHaveLength(0)
  })

  it('keeps at least one live subscription per (service, kind) when several wrappers swap', () => {
    const { client, fireConnected, liveSubscriptions } = fakeClient()
    const handlerA = vi.fn()
    const handlerB = vi.fn()
    const cancelA = onServiceEventResilient(client, 'browsermanager', 'browser_manager_event', handlerA)
    const cancelB = onServiceEventResilient(client, 'browsermanager', 'browser_manager_event', handlerB)

    fireConnected()

    // Sequential swaps: the surviving set always contains both handlers.
    const live = liveSubscriptions()
    expect(live.length).toBeGreaterThanOrEqual(1)
    const payload = { Kind: 'navigated' }
    for (const sub of live) sub.handler(payload)
    expect(handlerA).toHaveBeenCalledWith(payload)
    expect(handlerB).toHaveBeenCalledWith(payload)
    cancelA()
    cancelB()
  })
})
