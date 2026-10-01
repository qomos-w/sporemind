// Application-layer data source for the "import agent context from a remote
// connection" feature (agent context menu -> RemoteImportModal).
//
// `listRemoteConnections` reads the saved remote connections held by the
// desktop host. `listRemoteAgents` lists the agents visible on one saved
// connection, and `remoteImportReplaceContext` performs the actual replace: it
// pulls the remote agent's full snapshot (`RemoteAgentContextExport`) and feeds
// it into the local `local.session_import` callable, overwriting the local
// agent's conversation history.
//
// The remote side runs entirely in the desktop host process: saved credentials
// never reach the frontend, and the remote token lives only inside the host
// call. The local write goes through the app's gateway client (the window's own
// instance) via the generated `session_import` client — no hand-written wire
// protocol.

import * as desktopApp from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import { client } from './generated-client'
import * as localClient from '../gen-clients/local/client'
import * as workspace from '../gen-clients/workspace/client'
import type { AgentSessionImportReq } from '../gen-types/aigen'

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
  Title?: string
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
 * Returns an empty list when the connections binding is unavailable (e.g. the
 * web build without the desktop host).
 */
export async function listRemoteConnections(): Promise<RemoteConnectionBrief[]> {
  const list = (desktopApp as unknown as Partial<ConnectionsListBinding>).ConnectionsList
  if (typeof list !== 'function') return []
  const connections = await list()
  return (connections ?? []).map(c => ({ id: c.id, name: c.name, host: c.host, port: c.port }))
}

/**
 * Remote agents available on a saved connection. The host logs into the remote
 * gateway with the saved credentials and returns only the projected briefs; its
 * JSON field names are camelCase, mapped here to the PascalCase shape the import
 * UI renders.
 */
export async function listRemoteAgents(connId: string): Promise<RemoteAgentBrief[]> {
  const agents = await desktopApp.RemoteAgentList(connId)
  return (agents ?? []).map(a => ({
    ActorId: a.actorId,
    DisplayName: a.displayName,
    Title: a.title,
    AgentKind: a.agentKind,
    Status: a.status,
    ProjectName: a.projectName,
    LastActivity: a.lastActivity,
  }))
}

/**
 * Options for the title mirror: the local agent's workspace registry Id (the
 * `AgentId` workspace.update_agent matches) and the remote agent's
 * conversation title captured at pick time.
 */
export interface RemoteImportTitleOptions {
  localAgentId?: string
  remoteTitle?: string
}

/**
 * Replaces the local agent's whole conversation history with a remote agent's
 * snapshot.
 *
 * Two hops: the host process fetches the remote snapshot
 * (`RemoteAgentContextExport` -> the remote's `local.session_fork` full export),
 * then the app's own gateway client writes it into the local agent with
 * `local.session_import` scoped to `localAgentActorId`. The fork response shape
 * matches `AgentSessionImportReq` field-for-field, so no mapping is needed
 * except dropping `Goal`: the goal is a live mode-component runtime state, so
 * importing it across instances is harmful (the clone precedent drops it too).
 *
 * When both title options are present the remote conversation title is mirrored
 * onto the local agent via `workspace.update_agent` (best-effort: a failure is
 * logged and never fails the already-completed import). An empty remote title
 * leaves the local title untouched.
 *
 * After the write the local agent's timeline is re-fetched so an already-open
 * conversation converges to the imported history. The import callable emits a
 * `turn.history_imported` turn event, but the timeline's handler for it only
 * reconciles while the timeline is still empty (the clone/fork case); a
 * replace over a populated timeline needs an explicit reconcile, so we call the
 * existing `reconcile(actorId)` (the same convergence used after other
 * server-side session changes) via the module singleton. It is a safe no-op for
 * an untracked agent — a fresh open loads the new history from the backend
 * anyway.
 */
export async function remoteImportReplaceContext(
  connId: string,
  remoteAgentActorId: string,
  localAgentActorId: string,
  title?: RemoteImportTitleOptions,
): Promise<{ acceptedTurns: number }> {
  const snapshot = await desktopApp.RemoteAgentContextExport(connId, remoteAgentActorId)

  // Field-for-field from the fork response; Goal is deliberately omitted.
  const req = {
    Session: snapshot.Session,
    SummarySegments: snapshot.SummarySegments,
    ExploreResults: snapshot.ExploreResults,
    Steps: snapshot.Steps,
    NextIdx: snapshot.NextIdx,
    NextSeq: snapshot.NextSeq,
    NextTurnOrder: snapshot.NextTurnOrder,
  } as unknown as AgentSessionImportReq

  const resp = await localClient.sessionImport(client, req, { target: localAgentActorId })

  if (title?.localAgentId && title.remoteTitle) {
    try {
      await workspace.updateAgent(client, { AgentId: title.localAgentId, Title: title.remoteTitle })
    } catch (err) {
      console.warn('[remote-import] title mirror skipped:', err)
    }
  }

  // Best-effort UI convergence: the import already succeeded, so a refresh
  // failure must not surface as an import failure.
  try {
    const { getTimelineManager } = await import('../ui/ai/hooks/useTimelineManager')
    getTimelineManager().reconcile(localAgentActorId)
  } catch (err) {
    console.warn('[remote-import] timeline refresh skipped:', err)
  }

  return { acceptedTurns: resp?.AcceptedTurns ?? 0 }
}
