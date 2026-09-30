// Remote connection management — desktop host bindings + window-target helpers.
//
// The desktop client runs one app window per connection target: the main
// window is the local instance; each saved remote connection gets its own
// window loaded with ?server=<ws url>&conn=<id>, giving complete state
// isolation while both clients keep running (hidden windows are preserved).
//
// All persistence (profiles, encrypted passwords, fingerprints) lives in the
// desktop host process; this module is only a typed facade.

import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import type { ActiveConnection, ProbeResult, RemoteConnectionView } from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/models'
import { isIframe, isWails } from './runtime'
import { getActiveInstanceId, LOCAL_INSTANCE_ID } from './instance'

export interface RemoteConnectionInput {
  id?: string
  name: string
  host: string
  port?: number
  username?: string
  instanceId?: string
}

/** The ?conn= id when this window belongs to a saved remote connection. */
export function remoteConnectionId(): string | null {
  if (typeof window === 'undefined') return null
  const conn = new URLSearchParams(window.location.search).get('conn')
  return conn && conn.trim() ? conn : null
}

/** True when this window is bound to a non-local instance.
 *
 *  Two cases:
 *   1. An explicit ?conn= target in the URL — the mobile-shell / plugin iframe
 *      contract (`isIframe()`) and the desktop window (`isWails()`) both carry
 *      it. A mobile iframe with ?server= but no ?conn= is NOT remote: the native
 *      shell authenticates it with a handed-over token.
 *   2. The single-window shell whose active instance has been swapped away from
 *      local (`getActiveInstanceId() !== 'local'`) — the transport now dials the
 *      remote gateway, so a LOCAL admin token must never be minted here.
 */
export function isRemoteConnectionWindow(): boolean {
  if (remoteConnectionId() !== null && (isWails() || isIframe())) return true
  return getActiveInstanceId() !== LOCAL_INSTANCE_ID
}

export async function listConnections(): Promise<RemoteConnectionView[]> {
  return desktop.ConnectionsList()
}

export async function saveConnection(conn: RemoteConnectionInput, password: string): Promise<RemoteConnectionView> {
  const payload = {
    id: conn.id ?? '',
    name: conn.name,
    host: conn.host,
    port: conn.port ?? 18080,
    username: conn.username ?? '',
    instanceId: conn.instanceId ?? '',
    updatedAt: '',
    hasPassword: false,
  }
  return desktop.ConnectionsSave(
    payload as unknown as RemoteConnectionView,
    password,
  )
}

export async function deleteConnection(id: string): Promise<void> {
  return desktop.ConnectionsDelete(id)
}

export async function probeConnection(host: string, port: number): Promise<ProbeResult> {
  return desktop.ConnectionsProbe(host, port)
}

export async function switchConnection(target: string): Promise<void> {
  return desktop.SwitchConnection(target)
}

export async function getActiveConnection(): Promise<ActiveConnection> {
  return desktop.GetActiveConnection()
}

export type { ActiveConnection, ProbeResult, RemoteConnectionView }
