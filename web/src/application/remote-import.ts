// Application-layer data source for the "import agent context from a remote
// connection" feature (agent context menu -> RemoteImportModal).
//
// `listRemoteConnections` reads the saved remote connections held by the
// desktop host. The remote-agent listing and the context-replace call are
// PLACEHOLDERS here: [[context-replace-integration]] implements their bodies on
// top of the desktop host bindings (`RemoteAgentList` / `RemoteAgentContextExport`)
// plus the local `local.session_import` callable. Do not implement them in this
// file.
//
// The saved-connection binding (`desktop.ConnectionsList`) ships with the remote
// connections feature (web/src/application/remote-connections.ts). Until that
// module is part of this branch the lookup below is defensive and degrades to
// "no connections" instead of throwing, so the UI can still be built and tested.

import * as desktopApp from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'

/** Minimal view of a saved remote connection (subset of the host's view type). */
export interface RemoteConnectionBrief {
  id: string
  name: string
  host: string
  port: number
}

/** Minimal view of a remote agent (item of the host `RemoteAgentList` binding). */
export interface RemoteAgentBrief {
  ActorId: string
  DisplayName: string
  AgentKind?: string
  Status?: string
  ProjectName?: string
  LastActivity?: string
}

interface ConnectionsListBinding {
  ConnectionsList: () => Promise<RemoteConnectionBrief[]>
}

/**
 * Saved remote connections the user can import from, in host storage order.
 * Returns an empty list when the connections feature is not present.
 */
export async function listRemoteConnections(): Promise<RemoteConnectionBrief[]> {
  const list = (desktopApp as unknown as Partial<ConnectionsListBinding>).ConnectionsList
  if (typeof list !== 'function') return []
  const connections = await list()
  return (connections ?? []).map(c => ({ id: c.id, name: c.name, host: c.host, port: c.port }))
}

/** Remote agents available on a saved connection. Implemented by [[context-replace-integration]]. */
export async function listRemoteAgents(_connId: string): Promise<RemoteAgentBrief[]> {
  throw new Error('not implemented')
}

/** Replaces the local agent's whole conversation history. Implemented by [[context-replace-integration]]. */
export async function remoteImportReplaceContext(
  _connId: string,
  _remoteAgentActorId: string,
  _localAgentActorId: string,
): Promise<{ acceptedTurns: number }> {
  throw new Error('not implemented')
}
