// Transport selection rule shared by the client bootstrap and tests.
//
// The Wails raw transport is hardwired to the in-process LOCAL gateway; any
// explicit ?server= target (dedicated remote-connection window, mobile
// iframe) must dial over WebSocket instead.

import { getRuntime, isWails } from './runtime'
import { getDesktopConfig } from './desktop-config'

export function useWailsRawTransport(): boolean {
  if (!isWails()) return false
  if (getRuntime().signals.serverParam) return false
  const cfg = getDesktopConfig()
  return cfg?.transport !== 'ws'
}
