import type { GosporeClient } from '@qomos/gospore-client'

type ServiceEventHandler = (payload: unknown) => void

// Reconnect-resilient service-event subscription.
//
// The vendored gospore client's events pump terminates permanently when its
// streaming subscription errors: the gateway tears down a dying session by
// sending one error frame per subscription, the shared bucket is deleted, and
// resumeSubscriptions() has no live channel entry left to replay after the
// transport reconnects. One-shot effect subscriptions then stay dead until a
// page reload — silently dropping every event of that kind.
//
// This wrapper re-issues the subscription on every transport connect epoch
// (including reconnects), so a killed pump is always replaced. Concurrent
// registrations for the same (service, kind) never empty the vendored shared
// bucket during the swap, so the underlying stream does not churn.
export function onServiceEventResilient(
  client: GosporeClient,
  serviceName: string,
  kind: string,
  handler: ServiceEventHandler,
): () => void {
  let disposed = false
  let cancelStream: (() => void) | null = null

  const resubscribe = () => {
    if (disposed) return
    cancelStream?.()
    cancelStream = client.events.onService(serviceName, kind, handler)
  }

  resubscribe()
  const offConnected = client.onConnected(() => resubscribe())

  return () => {
    disposed = true
    offConnected()
    cancelStream?.()
    cancelStream = null
  }
}
