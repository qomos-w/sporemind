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
import { getRuntime, isWails } from './runtime'

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

/** True inside a dedicated remote-connection window (Wails + ?server= + ?conn=). */
export function isRemoteConnectionWindow(): boolean {
  if (!isWails()) return false
  const s = getRuntime().signals
  return Boolean(s.serverParam) && remoteConnectionId() !== null
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
