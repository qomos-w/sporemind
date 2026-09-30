// Transport selection rule shared by the client bootstrap and tests.
//
// The Wails raw transport is hardwired to the in-process LOCAL gateway; any
// remote target must dial over WebSocket instead. Two remote shapes exist:
// an explicit ?server= target (dedicated remote-connection window, mobile
// iframe) and an in-window instance swap (instance.ts sets a gateway
// override before rebinding the client).

import { getRuntime, isWails } from './runtime'
import { getDesktopConfig } from './desktop-config'
import { getGatewayOverride } from './gateway'

export function useWailsRawTransport(): boolean {
  if (!isWails()) return false
  if (getRuntime().signals.serverParam) return false
  // Swapped to a remote instance: the override points at a remote gateway,
  // and the wails-raw bridge authenticates with LOCAL process trust — the
  // remote rejects it ("user: not authenticated") and every call 401s.
  if (getGatewayOverride()) return false
  const cfg = getDesktopConfig()
  return cfg?.transport !== 'ws'
}
