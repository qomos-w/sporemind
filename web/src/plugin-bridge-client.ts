/**
 * Runs inside a plugin iframe and exposes the management-plane
 * `window.sporemind` surface to the host shell over a MessageChannel port.
 *
 * This script is loaded via a separate Vite entry (emitted at
 * /assets/plugin-bridge-client.js; the entryFileNames pattern puts every
 * rollup entry under assets/) so no function source strings are injected into
 * the iframe document. The host shell loads it cross-origin via the bootstrap
 * postMessage (see pkg/pluginhost/assets.go and the SDK's static file server,
 * which both inject the bootstrap snippet into served HTML).
 *
 * DATA PLANE NOTICE: invoke/stream/cast/emit no longer travel this channel.
 * The panel loads through the host gateway (/plugin/{id}), which reverse-
 * proxies to the plugin process's own HTTP listener; the generated
 * client.gen.ts awaits the appBase gate and talks to that listener through
 * the gateway — POST {base}/invoke/{id} and GET {base}/events SSE. What
 * remains here is management: console-log capture, DOM snapshot response,
 * ui-action (openView/…), theme, locale, agent-action, and event subscription
 * over the direct SSE stream.
 */

import type {
  PluginSurfaceAgentActionReq,
  PluginSurfaceConsoleLogReq,
  PluginSurfaceUIActionReq,
} from './gen-types/plugin.surface'

/**
 * SCOPE: the entire body is wrapped in an IIFE so the built bundle carries no
 * top-level lexical declarations — it is injected into plugin pages that may
 * declare their own globals (`$`, `S`, …) as classic scripts, and a bare
 * top-level `const` here would collide and kill the whole script
 * (SyntaxError: Identifier '$' has already been declared).
 */
;(() => {
interface BridgeContext {
  pluginID: string
  viewID: string
  projectID?: string
  agentID?: string
  parentOrigin?: string
  /** Gateway base path ("/plugin/{id}") for the generated client.gen.ts. */
  appBase?: string
}

const context: BridgeContext = (window as any).__sporemindBridgeContext ?? {
  pluginID: '',
  viewID: '',
}

const channel = new MessageChannel()
const port = channel.port1

/** Monotonic request id source for the request/response management frames. */
let requestCounter = 0

function makeRequestID(): string {
  requestCounter = (requestCounter + 1) % Number.MAX_SAFE_INTEGER
  return `${Date.now().toString(36)}-${requestCounter.toString(36)}`
}

port.onmessage = (e: MessageEvent) => {
  const msg = e.data
  if (msg && typeof msg === 'object' && msg.type === 'sporemind:dom-snapshot-request') {
    try {
      port.postMessage({ type: 'sporemind:dom-snapshot', snapshot: serializeDocument(document) })
    } catch {
      // Port may be closing; snapshot must never break app code.
    }
    return
  }
  if (msg && typeof msg === 'object' && msg.type === 'sporemind:panel-op' && typeof msg.requestId === 'string') {
    const req = msg as PanelOpMsg
    void runPanelOp(req)
      .then((res) => {
        try {
          port.postMessage({ type: 'sporemind:panel-op-result', requestId: req.requestId, ok: res.ok, result: res.result, reason: res.reason })
        } catch { /* port closing */ }
      })
      .catch((err: unknown) => {
        try {
          port.postMessage({ type: 'sporemind:panel-op-result', requestId: req.requestId, ok: false, reason: String(err) })
        } catch { /* port closing */ }
      })
    return
  }
  if (msg && typeof msg === 'object' && msg.type === 'sporemind:dispose') {
    dispose(msg.reason ?? 'host detach')
    return
  }
}
port.start()

port.onmessageerror = () => {
  dispose('port messageerror')
}

function dispose(_reason?: string): void {
  // No pending data-plane promises exist anymore; teardown is listener
  // cleanup: drop event subscriptions so a disposed bridge releases its
  // window-level event channel refs (the channel refcounts and closes the
  // connection itself when the last subscriber leaves).
  for (const unsub of channelUnsubscribes) unsub()
  channelUnsubscribes.length = 0
  eventListeners.clear()
}

// --- event subscription (shared per-URL WebSocket channel) -------------------
// Legacy surface kept for hand-written plugin frontends: events arrive on the
// plugin process's GET /events stream. The connection is a window-level
// singleton keyed by URL and shared with the generated client.gen.ts: both
// modules used to open their own permanent EventSource, and browser
// HTTP/1.1 pools cap at 6 connections per host — three bridged panels'
// duplicate streams starved every transient invoke in the panel pool
// (2026-09-14 incident). Transport is WS-first (outside the HTTP pool) with
// an SSE fallback when the upgrade handshake is refused (older SDK).

type EventHandler = (payload: unknown) => void

const eventListeners = new Map<string, Set<EventHandler>>()
const channelUnsubscribes: Array<() => void> = []

interface EventChannel {
  subscribe(eventKind: string, cb: (eventKind: string, payload: unknown) => void): () => void
  /** Host-driven suspend: close any live connection and stop dialing while
   * the panel is hidden; the channel and its subscribers stay intact. */
  suspend(): void
  /** Resume after a suspend: re-dial if anyone still subscribes. */
  resume(): void
}

function eventChannel(url: string): EventChannel {
  const w = window as any
  if (!w.__sporemindEventChannels) w.__sporemindEventChannels = new Map()
  const existing = w.__sporemindEventChannels.get(url)
  if (existing) return existing as EventChannel
  const ch = createEventChannel(url)
  w.__sporemindEventChannels.set(url, ch)
  return ch
}

function eventBackoff(attempt: number): number {
  const exp = Math.min(30_000, 500 * Math.pow(2, Math.min(attempt, 6)))
  return exp * (0.7 + 0.6 * Math.random())
}

function createEventChannel(url: string): EventChannel {
  const subs = new Map<string, Set<(eventKind: string, payload: unknown) => void>>()
  let stopped = false
  let fellBack = false
  let suspended = false
  let everOpened = false
  let attempt = 0
  let ws: WebSocket | null = null
  let es: EventSource | null = null
  const attachedKinds = new Set<string>()

  const wsUrl =
    url.startsWith('/')
      ? (location.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + location.host + url
      : url.replace(/^http/, 'ws')

  const teardown = () => {
    stopped = true
    fellBack = true
    ws?.close()
    es?.close()
    const w = window as any
    w.__sporemindEventChannels?.delete(url)
  }

  const dial = () => {
    if (stopped || fellBack || suspended) return
    let sock: WebSocket
    try {
      sock = new WebSocket(wsUrl)
    } catch {
      attempt++
      setTimeout(dial, eventBackoff(attempt))
      return
    }
    ws = sock
    sock.onopen = () => {
      everOpened = true
      attempt = 0
    }
    sock.onmessage = (ev) => {
      try {
        const env = JSON.parse(String(ev.data)) as { event?: string; data?: unknown }
        if (env.event) {
          subs.get(env.event)?.forEach((cb) => cb(env.event!, env.data))
          subs.get('*')?.forEach((cb) => cb(env.event!, env.data))
        }
      } catch {
        // Malformed frame: skip.
      }
    }
    sock.onclose = () => {
      if (stopped || suspended) return
      // First dial failing without ever opening means the handshake was
      // refused (older SDK answering a plain 200 stream): degrade to SSE.
      if (!everOpened && attempt === 0) {
        fellBack = true
        es = new EventSource(url, { withCredentials: true })
        for (const kind of subs.keys()) attachSseKind(es, kind)
        return
      }
      attempt++
      setTimeout(dial, eventBackoff(attempt))
    }
  }

  const attachSseKind = (target: EventSource, kind: string) => {
    if (attachedKinds.has(kind)) return
    attachedKinds.add(kind)
    target.addEventListener(kind, (ev) => {
      const msg = ev as MessageEvent
      let payload: unknown
      if (typeof msg.data === 'string' && msg.data !== '') {
        try {
          payload = JSON.parse(msg.data)
        } catch {
          payload = msg.data
        }
      }
      subs.get(kind)?.forEach((cb) => cb(kind, payload))
    })
  }

  dial()

  return {
    suspend() {
      if (stopped || suspended) return
      suspended = true
      if (ws) {
        // Clear handlers before close: an in-flight frame can still land
        // between close() and the socket actually dying.
        ws.onmessage = null
        ws.onclose = null
        ws.close()
      }
      es?.close()
    },
    resume() {
      if (stopped || !suspended) return
      suspended = false
      if (fellBack) {
        // The SSE fallback never reconnects natively: rebuild it with the
        // currently-subscribed kinds.
        es = new EventSource(url, { withCredentials: true })
        for (const kind of subs.keys()) attachSseKind(es, kind)
        return
      }
      everOpened = false
      attempt = 0
      if (subs.size > 0) dial()
    },
    subscribe(eventKind: string, cb: (eventKind: string, payload: unknown) => void) {
      let set = subs.get(eventKind)
      if (!set) {
        set = new Set()
        subs.set(eventKind, set)
        if (es) attachSseKind(es, eventKind)
      }
      set.add(cb)
      return () => {
        set!.delete(cb)
        if (set!.size === 0) {
          subs.delete(eventKind)
          if (subs.size === 0) teardown()
        }
      }
    },
  }
}

function subscribe(eventKind: string, cb: EventHandler): () => void {
  let set = eventListeners.get(eventKind)
  if (!set) {
    set = new Set()
    eventListeners.set(eventKind, set)
  }
  const wrapped = (_kind: string, payload: unknown) => {
    try {
      cb(payload)
    } catch (err) {
      console.error('[sporemind-bridge] event handler threw', err)
    }
  }
  set.add(cb)
  const unsub = eventChannel('/events').subscribe(eventKind, wrapped)
  channelUnsubscribes.push(unsub)
  return () => {
    const listeners = eventListeners.get(eventKind)
    if (!listeners) return
    listeners.delete(cb)
    if (listeners.size === 0) {
      eventListeners.delete(eventKind)
    }
    unsub()
  }
}

const host = {
  pluginID: context.pluginID,
  viewID: context.viewID,
  context,

  subscribe,

  async agentAction(
    action: 'create' | 'switch' | 'message',
    opts?: { kind?: string; targetAgentId?: string; payload?: Record<string, unknown> },
  ): Promise<boolean> {
    const req = {
      Type: 'sporemind.agent-action',
      RequestId: makeRequestID(),
      Action: action,
      PluginId: context.pluginID,
      Kind: opts?.kind,
      TargetAgentId: opts?.targetAgentId,
      Payload: opts?.payload,
    } as PluginSurfaceAgentActionReq
    return new Promise<boolean>((resolve) => {
      const timer = setTimeout(() => resolve(false), 10_000)
      const onReply = (e: MessageEvent) => {
        const msg = e.data
        if (!msg || typeof msg !== 'object' || msg.Type !== 'sporemind.agent-action-response') return
        if (msg.RequestId !== req.RequestId) return
        clearTimeout(timer)
        port.removeEventListener('message', onReply)
        resolve(Boolean(msg.Ok && msg.Result))
      }
      port.addEventListener('message', onReply)
      try {
        port.postMessage(req)
      } catch {
        clearTimeout(timer)
        port.removeEventListener('message', onReply)
        resolve(false)
      }
    })
  },

  openView(viewID: string) {
    const req = {
      Type: 'sporemind.ui-action', RequestId: makeRequestID(), Action: 'open-view', PluginId: context.pluginID, ViewId: viewID,
    } as PluginSurfaceUIActionReq
    port.postMessage(req)
  },

  closeView(viewID?: string) {
    const req = {
      Type: 'sporemind.ui-action', RequestId: makeRequestID(), Action: 'close-view', PluginId: context.pluginID, ViewId: viewID,
    } as PluginSurfaceUIActionReq
    port.postMessage(req)
  },

  setTitle(title: string) {
    const req = {
      Type: 'sporemind.ui-action', RequestId: makeRequestID(), Action: 'set-title', PluginId: context.pluginID, Title: title,
    } as PluginSurfaceUIActionReq
    port.postMessage(req)
  },

  setBadge(text: string) {
    const req = {
      Type: 'sporemind.ui-action', RequestId: makeRequestID(), Action: 'set-badge', PluginId: context.pluginID, Text: text,
    } as PluginSurfaceUIActionReq
    port.postMessage(req)
  },

  notify(level: 'info' | 'warn' | 'error', title: string, body?: string) {
    const req = {
      Type: 'sporemind.ui-action', RequestId: makeRequestID(), Action: 'notify', PluginId: context.pluginID, Level: level, Title: title, Body: body,
    } as PluginSurfaceUIActionReq
    port.postMessage(req)
  },

  dispose,

  theme: {
    get mode(): 'light' | 'dark' {
      const t = document.documentElement.getAttribute('data-theme')
      return t === 'dark' ? 'dark' : 'light'
    },
  },

  // Host locale as a BCP47 tag (e.g. "zh-CN"). Mirrors theme.mode: the
  // bootstrap snippet applies the host's locale to <html lang> (kept live by
  // sporemind:locale-update); in standalone dev (no host handshake) it falls
  // back to the browser language. Subscribe to changes with your own
  // MutationObserver on the lang attribute.
  get locale(): string {
    return (
      document.documentElement.getAttribute('lang') ||
      navigator.language ||
      'en-US'
    )
  },

  log(level: 'debug' | 'info' | 'warn' | 'error', message: string, ...args: unknown[]) {
    const prefix = `[plugin:${context.pluginID}]`
    // Patched console methods forward to the host log ring automatically.
    switch (level) {
      case 'debug': console.debug(prefix, message, ...args); break
      case 'info': console.info(prefix, message, ...args); break
      case 'warn': console.warn(prefix, message, ...args); break
      case 'error': console.error(prefix, message, ...args); break
    }
  },
}

// --- console capture -------------------------------------------------------
// App-frontend observability: console output and uncaught errors from the
// plugin iframe are forwarded to the host shell (fire-and-forget
// 'sporemind.console-log' frames), which persists them into the pluginhost
// per-plugin log ring via pluginhost.plugin_log_put. The ring is the surface
// agents query with pluginhost.plugin_logs, so both handler logs (sdk.Log)
// and frontend logs land in one tail. Messages are pre-truncated here (the
// sender-side truncation rule).

const CONSOLE_LOG_MAX = 512

function formatArgs(args: unknown[]): string {
  const parts: string[] = []
  for (const a of args) {
    if (typeof a === 'string') {
      parts.push(a)
    } else {
      try {
        parts.push(JSON.stringify(a))
      } catch {
        parts.push(String(a))
      }
    }
  }
  let s = parts.join(' ')
  if (s.length > CONSOLE_LOG_MAX) s = s.slice(0, CONSOLE_LOG_MAX) + '...(truncated)'
  return s
}

function sendConsoleLog(level: 'debug' | 'info' | 'warn' | 'error', ...args: unknown[]) {
  const message = formatArgs(args)
  if (!message) return
  const req = {
    Type: 'sporemind.console-log',
    RequestId: makeRequestID(),
    Level: level,
    Message: message,
    PluginId: context.pluginID,
    ViewId: context.viewID,
  } as PluginSurfaceConsoleLogReq
  try {
    port.postMessage(req)
  } catch {
    // Port may be closing; console output must never break app code.
  }
}

function installConsoleCapture() {
  const original = {
    log: console.log.bind(console),
    info: console.info.bind(console),
    warn: console.warn.bind(console),
    error: console.error.bind(console),
    debug: console.debug.bind(console),
  }
  const forward =
    (level: 'debug' | 'info' | 'warn' | 'error') =>
    (...args: unknown[]) => {
      sendConsoleLog(level, ...args)
    }
  console.log = (...args: unknown[]) => { original.log(...args); forward('info')(...args) }
  console.info = (...args: unknown[]) => { original.info(...args); forward('info')(...args) }
  console.warn = (...args: unknown[]) => { original.warn(...args); forward('warn')(...args) }
  console.error = (...args: unknown[]) => { original.error(...args); forward('error')(...args) }
  console.debug = (...args: unknown[]) => { original.debug(...args); forward('debug')(...args) }
  window.addEventListener('error', (e) => {
    sendConsoleLog('error', `window.onerror: ${e.message} @ ${e.filename}:${e.lineno}:${e.colno}`)
  })
  window.addEventListener('unhandledrejection', (e) => {
    sendConsoleLog('error', `unhandledrejection: ${String(e.reason)}`)
  })
}

installConsoleCapture()

// --- DOM snapshot ---------------------------------------------------------
// Host-side observability: on a 'sporemind:dom-snapshot-request' arriving on
// the MessagePort, serialize the current document into a bounded text outline
// and reply 'sporemind:dom-snapshot' on the same port. Caps keep the payload
// bounded at the source (project rule: truncate long fields sender-side).
// Per element: tag, #id, .classes, [data-*] hooks, and own text (≤120 chars).
// Caps: max depth 12, max 800 elements, total string ≤ 32KB — when any cap
// trips, stop and append '...(truncated)'. Script/style subtrees are listed
// as tags only, no text.

const SNAPSHOT_MAX_DEPTH = 12
const SNAPSHOT_MAX_ELEMENTS = 800
const SNAPSHOT_MAX_SIZE = 32 * 1024
const SNAPSHOT_TEXT_MAX = 120
const SNAPSHOT_TRUNCATION_MARKER = '\n...(truncated)'

function truncateStr(s: string, max: number): string {
  if (s.length <= max) return s
  return s.slice(0, max) + '...(truncated)'
}

function ownText(el: Element): string {
  const parts: string[] = []
  for (const child of Array.from(el.childNodes)) {
    if (child.nodeType === 3) {
      const t = (child.textContent || '').trim()
      if (t) parts.push(t)
    }
  }
  return parts.join(' ')
}

// No `export` anywhere in this file: the host injects it as a classic
// <script> (no type="module"), so ESM syntax is a parse error.
function serializeDocument(doc: Document): string {
  if (!doc.documentElement) return ''
  const lines: string[] = []
  let totalLen = 0
  let elementCount = 0
  let truncated = false

  function addLine(line: string): boolean {
    const sep = lines.length > 0 ? 1 : 0
    if (totalLen + sep + line.length + SNAPSHOT_TRUNCATION_MARKER.length > SNAPSHOT_MAX_SIZE) {
      truncated = true
      return false
    }
    lines.push(line)
    totalLen += sep + line.length
    return true
  }

  function walk(el: Element, depth: number): void {
    if (truncated) return
    if (depth > SNAPSHOT_MAX_DEPTH) { truncated = true; return }
    if (elementCount >= SNAPSHOT_MAX_ELEMENTS) { truncated = true; return }
    elementCount++

    const tag = el.tagName.toLowerCase()
    const indent = '  '.repeat(depth)
    let desc = indent + tag
    if (el.id) desc += `#${el.id}`
    if (el.classList.length > 0) desc += `.${Array.from(el.classList).join('.')}`
    for (const attr of Array.from(el.attributes)) {
      if (attr.name.startsWith('data-')) {
        const v = attr.value ? `=${truncateStr(attr.value, SNAPSHOT_TEXT_MAX)}` : ''
        desc += ` [${attr.name}${v}]`
      }
    }
    if (tag !== 'script' && tag !== 'style') {
      const text = ownText(el)
      if (text) desc += ` "${truncateStr(text, SNAPSHOT_TEXT_MAX)}"`
    }

    if (!addLine(desc)) return

    if (tag !== 'script' && tag !== 'style') {
      for (const child of Array.from(el.children)) {
        walk(child, depth + 1)
        if (truncated) return
      }
    }
  }

  walk(doc.documentElement, 0)

  let result = lines.join('\n')
  if (truncated) result += SNAPSHOT_TRUNCATION_MARKER
  return result
}

// --- Panel operations ------------------------------------------------------
// Dev-loop precise panel control (pluginhost.panel_op relayed by the host
// page over this port). Same trust model as the dom snapshot: ops only ever
// arrive on the host-side MessagePort, which exists solely after the host
// handshake — the host gates the op (dev-registered plugins only). All
// results are bounded sender-side (project rule) and matched by requestId.

const PANEL_OP_TIMEOUT_DEFAULT_MS = 5000
const PANEL_OP_TIMEOUT_MAX_MS = 15000
const PANEL_OP_DOM_DEFAULT_CHARS = 16384
const PANEL_OP_EVAL_DEFAULT_CHARS = 4096
const PANEL_OP_MAX_CHARS = 131072
const PANEL_OP_DOM_MAX_DEPTH = 14
const PANEL_OP_DOM_MAX_ELEMENTS = 1200
const PANEL_OP_TEXT_MAX = 160

interface PanelOpResult {
  ok: boolean
  result?: string
  reason?: string
}

interface PanelOpMsg {
  requestId: string
  op: string
  selector?: string
  text?: string
  expr?: string
  timeoutMs?: number
  maxChars?: number
}

function clampTimeout(ms: number | undefined): number {
  if (!ms || ms <= 0) return PANEL_OP_TIMEOUT_DEFAULT_MS
  return Math.min(ms, PANEL_OP_TIMEOUT_MAX_MS)
}

function clampChars(chars: number | undefined, dflt: number): number {
  if (!chars || chars <= 0) return dflt
  return Math.min(chars, PANEL_OP_MAX_CHARS)
}

function safeJSONStringify(value: unknown, maxChars: number): string {
  const seen = new WeakSet<object>()
  let s: string
  try {
    s = JSON.stringify(value, (_k, v: unknown) => {
      if (typeof v === 'object' && v !== null) {
        if (seen.has(v as object)) return '[Circular]'
        seen.add(v as object)
      }
      return v
    }) ?? 'null'
  } catch (err) {
    s = `unserializable: ${String(value)} (${String(err)})`
  }
  return truncateStr(s, maxChars)
}

function describeElement(el: Element): string {
  let desc = el.tagName.toLowerCase()
  if (el.id) desc += `#${el.id}`
  if (el.classList.length > 0) desc += `.${Array.from(el.classList).slice(0, 3).join('.')}`
  return desc
}

function findSelector(selector: string): Element | null {
  try {
    return document.querySelector(selector)
  } catch {
    return null
  }
}

function isInteractive(el: Element): boolean {
  const tag = el.tagName.toLowerCase()
  if (tag === 'a' || tag === 'button' || tag === 'input' || tag === 'select' || tag === 'textarea' || tag === 'option' || tag === 'label') return true
  if (el.hasAttribute('contenteditable')) return true
  const role = el.getAttribute('role')
  if (role === 'button' || role === 'link' || role === 'tab' || role === 'menuitem' || role === 'checkbox' || role === 'radio' || role === 'switch' || role === 'textbox') return true
  return el.hasAttribute('onclick')
}

// dom op serializer: like serializeDocument but with [k] action markers on
// interactive elements (so a follow-up click/type op can target a stable
// selector), a caller-chosen root, and a caller-chosen char budget.
function serializePanelDom(root: Element, maxChars: number): string {
  const lines: string[] = []
  let totalLen = 0
  let elementCount = 0
  let marker = 0
  let truncated = false

  function addLine(line: string): boolean {
    const sep = lines.length > 0 ? 1 : 0
    if (totalLen + sep + line.length + SNAPSHOT_TRUNCATION_MARKER.length > maxChars) {
      truncated = true
      return false
    }
    lines.push(line)
    totalLen += sep + line.length
    return true
  }

  function walk(el: Element, depth: number): void {
    if (truncated) return
    if (depth > PANEL_OP_DOM_MAX_DEPTH) { truncated = true; return }
    if (elementCount >= PANEL_OP_DOM_MAX_ELEMENTS) { truncated = true; return }
    elementCount++

    const tag = el.tagName.toLowerCase()
    const indent = '  '.repeat(depth)
    let desc = tag
    if (el.id) desc += `#${el.id}`
    if (el.classList.length > 0) desc += `.${Array.from(el.classList).slice(0, 4).join('.')}`
    for (const attr of Array.from(el.attributes)) {
      if (attr.name.startsWith('data-')) desc += ` [${attr.name}${attr.value ? `=${truncateStr(attr.value, PANEL_OP_TEXT_MAX)}` : ''}]`
    }
    if (tag === 'a' && el instanceof HTMLAnchorElement && el.getAttribute('href')) {
      desc += ` ->${truncateStr(el.getAttribute('href') || '', PANEL_OP_TEXT_MAX)}`
    }
    if (tag !== 'script' && tag !== 'style') {
      const text = ownText(el)
      if (text) desc += ` "${truncateStr(text, PANEL_OP_TEXT_MAX)}"`
    }
    if (isInteractive(el)) {
      marker++
      desc = `${indent}[${marker - 1}] ${desc}`
    } else {
      desc = indent + desc
    }
    if (!addLine(desc)) return

    if (tag !== 'script' && tag !== 'style') {
      for (const child of Array.from(el.children)) {
        walk(child, depth + 1)
        if (truncated) return
      }
    }
  }

  walk(root, 0)

  let out = lines.join('\n')
  if (truncated) out += SNAPSHOT_TRUNCATION_MARKER
  return out
}

function opDom(msg: PanelOpMsg): PanelOpResult {
  const root = msg.selector ? findSelector(msg.selector) : document.documentElement
  if (!root) return { ok: false, reason: `selector not found: ${msg.selector}` }
  return { ok: true, result: serializePanelDom(root, clampChars(msg.maxChars, PANEL_OP_DOM_DEFAULT_CHARS)) }
}

async function opEval(msg: PanelOpMsg): Promise<PanelOpResult> {
  if (!msg.expr) return { ok: false, reason: 'eval requires expr' }
  const timeoutMs = clampTimeout(msg.timeoutMs)
  const stmtStart = /^\s*(?:return|const|let|var|function|for|while|if|switch|try|class|throw)\b/
  const body = !stmtStart.test(msg.expr) && !msg.expr.includes(';')
    ? `return (${msg.expr})`
    : msg.expr
  let fn: () => Promise<unknown>
  try {
    // eslint-disable-next-line no-new-func
    fn = new Function(`"use strict";\nreturn (async () => {\n${body}\n})()`) as () => Promise<unknown>
  } catch (err) {
    return { ok: false, reason: `syntax error: ${String(err)}` }
  }
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    const value = await Promise.race([
      fn(),
      new Promise<never>((_resolve, reject) => {
        timer = setTimeout(() => reject(new Error(`eval timeout after ${timeoutMs}ms`)), timeoutMs)
      }),
    ])
    return { ok: true, result: safeJSONStringify(value, clampChars(msg.maxChars, PANEL_OP_EVAL_DEFAULT_CHARS)) }
  } catch (err) {
    return { ok: false, reason: err instanceof Error ? err.message : String(err) }
  } finally {
    if (timer !== undefined) clearTimeout(timer)
  }
}

function opClick(msg: PanelOpMsg): PanelOpResult {
  if (!msg.selector) return { ok: false, reason: 'click requires selector' }
  const el = findSelector(msg.selector)
  if (!el) return { ok: false, reason: `selector not found: ${msg.selector}` }
  try {
    el.scrollIntoView({ block: 'center', inline: 'center' })
  } catch { /* detached or unsupported; the synthetic events still carry coordinates */ }
  const r = el.getBoundingClientRect()
  const base: MouseEventInit = {
    bubbles: true,
    cancelable: true,
    view: window,
    clientX: r.x + r.width / 2,
    clientY: r.y + r.height / 2,
    button: 0,
  }
  const mk = (type: string): Event => {
    if (typeof PointerEvent === 'function') return new PointerEvent(type, { ...base, pointerId: 1, pointerType: 'mouse', isPrimary: true })
    return new MouseEvent(type, base)
  }
  el.dispatchEvent(mk('pointerdown'))
  el.dispatchEvent(new MouseEvent('mousedown', base))
  if (typeof (el as HTMLElement).focus === 'function') (el as HTMLElement).focus()
  el.dispatchEvent(mk('pointerup'))
  el.dispatchEvent(new MouseEvent('mouseup', base))
  el.dispatchEvent(new MouseEvent('click', base))
  return { ok: true, result: `clicked ${describeElement(el)}` }
}

function fireInputEvents(el: Element): void {
  el.dispatchEvent(new Event('input', { bubbles: true }))
  el.dispatchEvent(new Event('change', { bubbles: true }))
}

function opType(msg: PanelOpMsg): PanelOpResult {
  if (!msg.selector) return { ok: false, reason: 'type requires selector' }
  if (msg.text === undefined) return { ok: false, reason: 'type requires text' }
  const el = findSelector(msg.selector)
  if (!el) return { ok: false, reason: `selector not found: ${msg.selector}` }
  if (typeof (el as HTMLElement).focus === 'function') (el as HTMLElement).focus()
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
    // Native value setter so React/Vue controlled components observe the change.
    const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
    const setter = Object.getOwnPropertyDescriptor(proto, 'value')?.set
    if (setter) setter.call(el, msg.text)
    else el.value = msg.text
    fireInputEvents(el)
    return { ok: true, result: `typed ${msg.text.length} chars into ${describeElement(el)}` }
  }
  if (el.hasAttribute('contenteditable')) {
    el.textContent = msg.text
    fireInputEvents(el)
    return { ok: true, result: `typed ${msg.text.length} chars into contenteditable ${describeElement(el)}` }
  }
  return { ok: false, reason: `not a typeable element: ${describeElement(el)}` }
}

async function opWait(msg: PanelOpMsg): Promise<PanelOpResult> {
  if (!msg.selector) return { ok: false, reason: 'wait requires selector' }
  const timeoutMs = clampTimeout(msg.timeoutMs)
  const start = Date.now()
  for (;;) {
    const el = findSelector(msg.selector)
    if (el) {
      const textOk = !msg.text || (el.textContent || '').includes(msg.text)
      const visible = (el as HTMLElement).offsetParent !== null || el.getClientRects().length > 0
      if (textOk && visible) {
        return { ok: true, result: `matched ${describeElement(el)} after ${Date.now() - start}ms` }
      }
    }
    if (Date.now() - start >= timeoutMs) {
      return { ok: false, reason: `timeout after ${timeoutMs}ms waiting for ${msg.selector}${msg.text ? ` containing "${truncateStr(msg.text, 60)}"` : ''}` }
    }
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
}

async function runPanelOp(msg: PanelOpMsg): Promise<PanelOpResult> {
  switch (msg.op) {
    case 'dom': return opDom(msg)
    case 'eval': return opEval(msg)
    case 'click': return opClick(msg)
    case 'type': return opType(msg)
    case 'wait': return opWait(msg)
    default: return { ok: false, reason: `unknown op: ${msg.op}` }
  }
}

;(window as any).sporemind = host
window.dispatchEvent(new CustomEvent('sporemind:ready', { detail: host }))

const targetOrigin = context.parentOrigin ?? window.location.origin
window.parent.postMessage({ type: 'sporemind:handshake', pluginID: context.pluginID, viewID: context.viewID }, targetOrigin, [channel.port2])
})()
