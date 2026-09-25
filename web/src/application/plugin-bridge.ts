import { client } from './generated-client'
import { getHttpUrl } from './gateway'
import { getToken } from './auth-store'
import * as appmanagerSessionClient from '../gen-clients/appmanager/client'
import * as appmanagerAgentClient from '../gen-clients/appmanager/client'
import * as pluginhostClient from '../gen-clients/pluginhost/client'
import type {
  PluginSurfaceAgentActionReq,
  PluginSurfaceAgentActionResp,
  PluginSurfaceConsoleLogReq,
  PluginSurfaceUIActionReq,
  PluginSurfaceUIActionResp,
} from '../gen-types/plugin.surface'

/**
 * The plugin bridge is a MANAGEMENT-plane channel only. The data plane
 * (invoke / event streaming) travels directly between the plugin iframe and
 * the plugin process's own same-origin HTTP listener (POST /invoke/{id},
 * GET /events SSE) — see the generated client.gen.ts and the SDK http server.
 * What remains here: session bind/revoke, console-log capture forwarding,
 * DOM snapshot routing, ui-action dispatch (openView/theme/…), and
 * agent-action proxying. All of these are host-shell concerns that must not
 * ride the plugin process's data listener.
 */

/** Minimal message-target surface used by the bridge host (Window | null). */
export interface PluginBridgeSource {
  postMessage(message: unknown, options: { targetOrigin: string } | string): void
}

interface SessionInfo {
  AppId: string
  ViewId: string
  AgentId?: string
  ProjectId?: string
}

/**
 * Full credential set captured at session_create time. Stored per source so
 * the bridge can validate origin claims and revoke by token. BackendUrl /
 * CookieToken feed the direct-HTTP bootstrap: the cookie token is minted from
 * the same per-instance secret the plugin process verifies against and is
 * handed to the iframe so the subprocess can Set-Cookie on its own origin.
 */
interface BoundSession {
  sessionId: string
  token: string
  session: SessionInfo
  origin: string
  expiresAt: number
  nonce: string
  generation: number
}

interface PortState {
  source: PluginBridgeSource
  session: SessionInfo
  // One-shot marker: the first console frame on this port proves the whole
  // capture pipeline (client patch → port → host → plugin_log_put) is live.
  captureAnnounced?: boolean
}

const bridgeSessions = new WeakMap<object, BoundSession>()
const activePorts = new Map<MessagePort, PortState>()

/**
 * pluginID → host-side port of the plugin's first mounted view. Kept separate
 * from activePorts (keyed by port) so the interfacemanager panel-op /
 * dom-snapshot triggers can reach the right iframe. A plugin with multiple
 * mounted views targets the first — these are app-level aids, not per-view
 * state.
 */
const appBridgePorts = new Map<string, MessagePort>()

/** Result of binding a plugin iframe source to a host-managed session. */
export interface PluginBridgeBinding {
  /** Revokes the bound session (by its current token) — call on unmount. */
  revoke: () => Promise<void>
  /**
   * Synchronous fire-and-forget revoke for page unload: React's unmount
   * cleanup never runs on pagehide/beforeunload, so the session would leak
   * until its 24h TTL expires. Uses fetch keepalive against the gateway's
   * HTTP invoke endpoint (same ?token= auth as the WS transport) so the
   * request survives the page going away.
   */
  revokeKeepalive: () => void
}

export async function bindPluginBridgeSource(source: PluginBridgeSource, appID: string, viewID: string): Promise<PluginBridgeBinding> {
  const created = await appmanagerSessionClient.sessionCreate(client, { AppId: appID, ViewId: viewID })
  const bound: BoundSession = {
    sessionId: created.SessionId,
    token: created.Token,
    session: {
      AppId: created.AppId,
      ViewId: created.ViewId,
      AgentId: created.AgentId,
      ProjectId: created.ProjectId,
    },
    origin: created.Origin,
    expiresAt: created.ExpiresAt,
    nonce: created.Nonce,
    generation: created.Generation,
  }
  bridgeSessions.set(source as object, bound)
  return {
    revoke: async () => {
      // Revoke whatever binding is CURRENT for this source: a later rebind of
      // the same iframe must not outlive the unmount cleanup.
      const existing = bridgeSessions.get(source as object)
      if (!existing) return
      bridgeSessions.delete(source as object)
      await appmanagerSessionClient.sessionRevoke(client, { Token: existing.token })
    },
    revokeKeepalive: () => {
      const existing = bridgeSessions.get(source as object)
      if (!existing) return
      bridgeSessions.delete(source as object)
      const auth = getToken()
      const url = `${getHttpUrl()}/api/appmanager.session_revoke${auth ? `?token=${encodeURIComponent(auth)}` : ''}`
      void fetch(url, {
        method: 'POST',
        keepalive: true,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ Token: existing.token }),
      }).catch(() => {})
    },
  }
}

async function resolveBridgeSource(source: PluginBridgeSource | null): Promise<{ bound: BoundSession }> {
  if (!source) throw new Error('plugin bridge source is required')
  const bound = bridgeSessions.get(source as object)
  if (!bound) throw new Error('plugin bridge source is not bound')
  const resolved = await appmanagerSessionClient.sessionResolve(client, { Token: bound.token })
  // Refresh agent/project binding from the authoritative resolve response.
  bound.session.AgentId = resolved.AgentId
  bound.session.ProjectId = resolved.ProjectId
  return { bound }
}

function postPortReply(port: MessagePort, msg: unknown) {
  try {
    port.postMessage(msg)
  } catch {
    // Port may be closing; ignore.
  }
}

function buildAgentActionErrorResponse(req: PluginSurfaceAgentActionReq, err: unknown): PluginSurfaceAgentActionResp {
  return { Type: 'sporemind.agent-action-response', RequestId: req.RequestId, Ok: false, Result: undefined, Error: String(err) }
}

function buildUIActionResponse(req: PluginSurfaceUIActionReq): PluginSurfaceUIActionResp {
  return { Type: 'sporemind.ui-action-response', RequestId: req.RequestId, Ok: true }
}

async function handleAgentAction(req: PluginSurfaceAgentActionReq, state: PortState, port: MessagePort) {
  try {
    const resp = await appmanagerAgentClient.agentAction(client, {
      Id: state.session.AppId,
      Action: req.Action,
      Kind: req.Kind,
      TargetAgentId: req.TargetAgentId,
      Payload: req.Payload as Record<string, unknown> | undefined,
      AgentId: state.session.AgentId,
      ProjectId: state.session.ProjectId,
      RequestId: req.RequestId,
    })
    postPortReply(port, { Type: 'sporemind.agent-action-response', RequestId: req.RequestId, Ok: true, Result: resp.Accepted })
  } catch (err) {
    postPortReply(port, buildAgentActionErrorResponse(req, err))
  }
}

function handleUIAction(req: PluginSurfaceUIActionReq, state: PortState, port: MessagePort) {
  const detail = buildUIActionDetail(req, state)
  window.dispatchEvent(new CustomEvent(`sporemind:${req.Action}`, { detail }))
  postPortReply(port, buildUIActionResponse(req))
}

function buildUIActionDetail(req: PluginSurfaceUIActionReq, state: PortState) {
  switch (req.Action) {
    case 'open-view':
      return { viewId: req.ViewId, viewType: req.ViewId, title: req.Title, zone: req.Zone }
    case 'close-view':
      return { viewId: req.ViewId }
    case 'set-title':
      return { title: req.Title, pluginID: state.session.AppId, viewID: req.ViewId }
    case 'set-badge':
      return { text: req.Text, count: req.Count }
    case 'notify':
      return { title: req.Title, body: req.Body, level: req.Level }
    default:
      return { viewId: req.ViewId, title: req.Title, text: req.Text, level: req.Level, body: req.Body, zone: req.Zone }
  }
}

const ENVELOPE_RESPONSE_TYPES: Record<string, string> = {
  'sporemind.agent-action': 'sporemind.agent-action-response',
  'sporemind.ui-action': 'sporemind.ui-action-response',
}

interface EnvelopeValidation {
  ok: boolean
  error?: string
}

/**
 * Validate an incoming port message envelope against the bound session state.
 * The management plane carries no replayable authority (the only credential,
 * the invoke session token, left with the data plane), but every frame must
 * still carry a PluginId matching the bound session — the port binding itself
 * is the identity, and a forged frame must not be able to attribute console
 * logs or UI actions to another plugin. ViewId is checked as a source
 * identity for agent-action/console-log, but NOT for ui-action (where ViewId
 * is a *target* — the view to open/close — not a source claim).
 */
function validateEnvelope(msg: any, state: PortState): EnvelopeValidation {
  if (msg.PluginId === undefined) {
    return { ok: false, error: 'envelope missing PluginId origin claim' }
  }
  if (msg.PluginId !== state.session.AppId) {
    return { ok: false, error: `envelope origin mismatch: PluginId=${msg.PluginId}, expected=${state.session.AppId}` }
  }
  if (msg.Type !== 'sporemind.ui-action' && msg.ViewId !== undefined && msg.ViewId !== state.session.ViewId) {
    return { ok: false, error: `envelope source mismatch: ViewId=${msg.ViewId}, expected=${state.session.ViewId}` }
  }
  return { ok: true }
}

function buildEnvelopeErrorResponse(msg: any, error: string): unknown {
  const responseType = ENVELOPE_RESPONSE_TYPES[msg?.Type]
  if (!responseType) return { Type: 'sporemind.error', Error: error }
  return { Type: responseType, RequestId: msg.RequestId, Ok: false, Error: error }
}

async function handlePortMessage(port: MessagePort, msg: unknown) {
  const state = activePorts.get(port)
  if (!state) return

  const m = msg as any
  if (!m || typeof m !== 'object') return

  // DOM snapshot push from the plugin iframe: a fire-and-forget observability
  // frame, not a call — plugin-bridge-client sends it with lowercase type/
  // snapshot fields and no envelope (Seq/RequestId), so it is handled before
  // envelope validation. The port binding itself is the identity.
  const frameType: unknown = m.Type ?? m.type
  if (frameType === 'sporemind:dom-snapshot') {
    const snapshot = typeof m.Snapshot === 'string' ? m.Snapshot : m.snapshot
    if (typeof snapshot === 'string') {
      void pluginhostClient
        .pluginDomPut(client, { PluginId: state.session.AppId, Snapshot: snapshot, Ts: Date.now() })
        .catch(() => {})
    }
    return
  }
  // Panel op result push from the plugin iframe: same lowercase non-envelope
  // convention as the dom snapshot, but matched by requestId (an op can fail
  // without a result, so Ok/Reason ride along).
  if (frameType === 'sporemind:panel-op-result') {
    const requestId: unknown = m.RequestId ?? m.requestId
    if (typeof requestId === 'string' && requestId !== '') {
      void pluginhostClient
        .panelOpPut(client, {
          PluginId: state.session.AppId,
          RequestId: requestId,
          Ok: m.Ok === true || m.ok === true,
          Result: typeof m.Result === 'string' ? m.Result : (typeof m.result === 'string' ? m.result : undefined),
          Reason: typeof m.Reason === 'string' ? m.Reason : (typeof m.reason === 'string' ? m.reason : undefined),
          Ts: Date.now(),
        })
        .catch(() => {})
    }
    return
  }

  if (!m.Type) return

  // Validate the origin claim before dispatching to a management handler.
  const validation = validateEnvelope(m, state)
  if (!validation.ok) {
    if (m.RequestId) postPortReply(port, buildEnvelopeErrorResponse(m, validation.error!))
    return
  }

  switch (m.Type) {
    case 'sporemind.agent-action':
      await handleAgentAction(m as PluginSurfaceAgentActionReq, state, port)
      break
    case 'sporemind.ui-action':
      handleUIAction(m as PluginSurfaceUIActionReq, state, port)
      break
    case 'sporemind.console-log': {
      // Fire-and-forget frontend log capture: persist the iframe console
      // entry into the pluginhost per-plugin ring so plugin_logs shows one
      // merged backend+frontend tail. Never answered on the port.
      if (!state.captureAnnounced) {
        state.captureAnnounced = true
        console.info(`[plugin-bridge] capture_on app=${state.session.AppId}`)
      }
      const logReq = m as PluginSurfaceConsoleLogReq
      void pluginhostClient
        .pluginLogPut(client, { PluginId: state.session.AppId, Level: logReq.Level, Message: logReq.Message, ViewId: logReq.ViewId })
        .catch(() => {})
      break
    }
  }
}

/**
 * Accepts a MessageChannel port from a plugin iframe after the handshake.
 * Resolves the source's bridge session and starts routing port messages.
 */
export async function handlePluginBridgeHandshake(source: PluginBridgeSource, port: MessagePort): Promise<void> {
  let bound: BoundSession
  try {
    const result = await resolveBridgeSource(source)
    bound = result.bound
  } catch (err) {
    console.error('[plugin-bridge] handshake_err', err)
    port.close()
    return
  }

  const state: PortState = {
    source,
    session: { ...bound.session },
  }
  console.info(`[plugin-bridge] handshake_ok app=${state.session.AppId} view=${state.session.ViewId}`)
  activePorts.set(port, state)
  if (!appBridgePorts.has(state.session.AppId)) {
    appBridgePorts.set(state.session.AppId, port)
  }
  port.onmessage = (e: MessageEvent) => {
    void handlePortMessage(port, e.data)
  }
  port.start()

  port.addEventListener('messageerror', (err) => {
    console.error('[plugin-bridge] port error', err)
  })
}

/**
 * Detach a port previously registered by `handlePluginBridgeHandshake`.
 * Called by PluginIframe on unmount/iframe reload.
 */
export function detachPluginBridgePort(port: MessagePort): void {
  activePorts.delete(port)
  for (const [appID, snapshotPort] of appBridgePorts) {
    if (snapshotPort !== port) continue
    appBridgePorts.delete(appID)
    // Promote another active port of the same app so a surviving view keeps
    // answering snapshot requests.
    for (const [candidatePort, candidateState] of activePorts) {
      if (candidateState.session.AppId === appID) {
        appBridgePorts.set(appID, candidatePort)
        break
      }
    }
  }
  port.close()
}

/**
 * Ask the mounted plugin iframe for a DOM snapshot. Fire-and-forget: the
 * iframe replies with a 'sporemind:dom-snapshot' port frame which the bridge
 * pushes to pluginhost.plugin_dom_put. Returns false when no view of the
 * plugin is mounted (panel closed or iframe not yet handshaken).
 */
export function requestPluginDomSnapshot(pluginID: string): boolean {
  const port = appBridgePorts.get(pluginID)
  if (!port) return false
  try {
    port.postMessage({ type: 'sporemind:dom-snapshot-request' })
    return true
  } catch {
    return false
  }
}

/** Panel op envelope relayed to the plugin iframe over the bridge port. */
export interface PanelOpRelay {
  requestId: string
  op: string
  selector?: string
  text?: string
  expr?: string
  timeoutMs?: number
  maxChars?: number
}

/**
 * Relay a panel operation to the mounted plugin iframe. Fire-and-forget like
 * the dom snapshot: the iframe replies with a 'sporemind:panel-op-result'
 * port frame which the bridge pushes to pluginhost.panel_op_put, matched by
 * requestId. Returns false when no view of the plugin is mounted — callers
 * should push an immediate failure result so the backend poll does not burn
 * its whole budget.
 */
export function requestPluginPanelOp(pluginID: string, op: PanelOpRelay): boolean {
  const port = appBridgePorts.get(pluginID)
  if (!port) return false
  try {
    port.postMessage({ type: 'sporemind:panel-op', ...op })
    return true
  } catch {
    return false
  }
}

/**
 * Push an immediate panel-op failure to pluginhost.panel_op_put (used when no
 * bridge port exists — the backend poll then fails fast instead of burning
 * its whole budget).
 */
export function pushPanelOpFailure(pluginID: string, requestId: string, reason: string): void {
  void pluginhostClient
    .panelOpPut(client, { PluginId: pluginID, RequestId: requestId, Ok: false, Reason: reason, Ts: Date.now() })
    .catch(() => {})
}

/** Test-only: close and drop every registered bridge port and snapshot binding. */
export function _resetPluginBridgePortsForTest(): void {
  for (const port of activePorts.keys()) port.close()
  activePorts.clear()
  appBridgePorts.clear()
}
