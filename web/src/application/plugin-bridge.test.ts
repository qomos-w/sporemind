import { describe, it, expect, vi, beforeEach } from 'vitest'
import { handlePluginBridgeHandshake, bindPluginBridgeSource, requestPluginDomSnapshot, detachPluginBridgePort, _resetPluginBridgePortsForTest, type PluginBridgeSource } from './plugin-bridge'
import * as appmanagerSessionClient from '../gen-clients/appmanager/client'
import * as appmanagerAgentClient from '../gen-clients/appmanager/client'
import * as pluginhostClient from '../gen-clients/pluginhost/client'

// Mock the backend client modules so no real WebSocket transport is needed.
vi.mock('./generated-client', () => ({
  client: {},
}))

vi.mock('../gen-clients/appmanager/client', () => ({
  sessionCreate: vi.fn(),
  sessionResolve: vi.fn(),
  sessionRevoke: vi.fn(),
  agentAction: vi.fn(),
}))

vi.mock('../gen-clients/pluginhost/client', () => ({
  pluginLogPut: vi.fn(),
  pluginDomPut: vi.fn(),
}))

const SESSION_CREATE_RESP = {
  SessionId: 'session-1',
  AppId: 'trusted.app',
  ViewId: 'main',
  AgentId: 'agent-1',
  ProjectId: 'proj-1',
  Origin: 'http://localhost:3000',
  ExpiresAt: 0,
  Nonce: 'nonce-1',
  Generation: 1,
  Token: 'token-signed-1',
}

const SESSION_RESOLVE_RESP = {
  SessionId: 'session-1',
  AppId: 'trusted.app',
  ViewId: 'main',
  AgentId: 'agent-1',
  ProjectId: 'proj-1',
  Origin: 'http://localhost:3000',
  ExpiresAt: 0,
  Nonce: 'nonce-1',
  Generation: 1,
}

/** Capture messages posted to a port for assertion. */
function makePort() {
  const messages: unknown[] = []
  const listeners: Array<(e: MessageEvent) => void> = []
  let started = false
  const port = {
    postMessage: vi.fn((msg: unknown) => messages.push(msg)),
    onmessage: null as null | ((e: MessageEvent) => void),
    addEventListener: vi.fn((type: string, cb: (e: MessageEvent) => void) => {
      if (type === 'message') listeners.push(cb)
    }),
    removeEventListener: vi.fn(),
    start: vi.fn(() => {
      started = true
    }),
    close: vi.fn(),
    dispatch(msg: unknown) {
      const event = { data: msg } as MessageEvent
      if (port.onmessage) port.onmessage(event)
      for (const cb of listeners) cb(event)
    },
    get isStarted() {
      return started
    },
    messages,
  }
  return port
}

function lastReply(messages: unknown[]) {
  return messages[messages.length - 1] as any
}

async function makeBoundPort() {
  const port = makePort()
  const src: PluginBridgeSource = {
    postMessage: vi.fn(),
  }
  await bindPluginBridgeSource(src, 'trusted.app', 'main')
  await handlePluginBridgeHandshake(src, port as unknown as MessagePort)
  return { port, src }
}

describe('plugin-bridge host: management plane (console-log / dom-snapshot / ui-action)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    _resetPluginBridgePortsForTest()
    vi.mocked(appmanagerSessionClient.sessionCreate).mockResolvedValue(SESSION_CREATE_RESP)
    vi.mocked(appmanagerSessionClient.sessionResolve).mockResolvedValue(SESSION_RESOLVE_RESP)
    vi.mocked(appmanagerSessionClient.sessionRevoke).mockResolvedValue({})
    vi.mocked(appmanagerAgentClient.agentAction).mockReset()
    vi.mocked(pluginhostClient.pluginLogPut).mockClear()
  })

  it('forwards console-log frames to pluginhost.plugin_log_put without a port reply', async () => {
    const infoSpy = vi.spyOn(console, 'info').mockImplementation(() => {})
    try {
      const { port } = await makeBoundPort()
      vi.mocked(pluginhostClient.pluginLogPut).mockResolvedValue({} as never)

      port.dispatch({
        Type: 'sporemind.console-log',
        RequestId: 'log-1',
        Level: 'info',
        Message: 'hello from plugin',
        PluginId: 'trusted.app',
        ViewId: 'main',
      })

      await vi.waitFor(() => {
        expect(pluginhostClient.pluginLogPut).toHaveBeenCalled()
      })
      const req = vi.mocked(pluginhostClient.pluginLogPut).mock.calls[0]![1]
      expect(req).toMatchObject({ PluginId: 'trusted.app', Level: 'info', Message: 'hello from plugin' })
      // Console-log frames are fire-and-forget: no reply is posted back.
      expect(port.messages).toHaveLength(0)

      // capture_on is a one-shot per port: announced on the first frame only.
      const announcements = infoSpy.mock.calls.filter((c) => String(c[0]).includes('capture_on app=trusted.app'))
      expect(announcements).toHaveLength(1)
      port.dispatch({
        Type: 'sporemind.console-log',
        RequestId: 'log-2',
        Level: 'warn',
        Message: 'second frame',
        PluginId: 'trusted.app',
        ViewId: 'main',
      })
      await vi.waitFor(() => {
        expect(vi.mocked(pluginhostClient.pluginLogPut).mock.calls.length).toBeGreaterThanOrEqual(2)
      })
      expect(infoSpy.mock.calls.filter((c) => String(c[0]).includes('capture_on app=trusted.app'))).toHaveLength(1)
    } finally {
      infoSpy.mockRestore()
    }
  })

  it('still routes agent-action messages to appmanager.agent.action', async () => {
    const { port } = await makeBoundPort()
    vi.mocked(appmanagerAgentClient.agentAction).mockResolvedValue({ Accepted: true } as never)

    port.dispatch({
      Type: 'sporemind.agent-action',
      RequestId: 'ag-1',
      Action: 'create',
      PluginId: 'trusted.app',
      Kind: 'worker',
      Payload: { task: 'x' },
    })

    await vi.waitFor(() => {
      expect(appmanagerAgentClient.agentAction).toHaveBeenCalled()
    })
    const req = vi.mocked(appmanagerAgentClient.agentAction).mock.calls[0]![1]
    expect(req).toMatchObject({
      Id: 'trusted.app',
      Action: 'create',
      Kind: 'worker',
      AgentId: 'agent-1',
      ProjectId: 'proj-1',
      RequestId: 'ag-1',
    })
  })

  it('ignores malformed messages', async () => {
    const { port } = await makeBoundPort()
    port.dispatch(null)
    port.dispatch(42)
    port.dispatch({ noType: true })
    // No throws, no backend calls.
    expect(port.messages).toHaveLength(0)
    expect(pluginhostClient.pluginLogPut).not.toHaveBeenCalled()
  })

  it('rejects handshakes from unbound sources', async () => {
    const port = makePort()
    await handlePluginBridgeHandshake({ postMessage: vi.fn() }, port as unknown as MessagePort)
    expect(port.close).toHaveBeenCalled()
    expect(port.isStarted).toBe(false)
  })

  it('leaves a grep-able trace of handshake outcomes in the console', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const infoSpy = vi.spyOn(console, 'info').mockImplementation(() => {})
    try {
      const { src } = await makeBoundPort()
      expect(infoSpy).toHaveBeenCalledWith('[plugin-bridge] handshake_ok app=trusted.app view=main')

      infoSpy.mockClear()
      const bad = makePort()
      await handlePluginBridgeHandshake({ postMessage: src.postMessage }, bad as unknown as MessagePort)
      expect(bad.close).toHaveBeenCalled()
      expect(errSpy).toHaveBeenCalled()
      const logged = errSpy.mock.calls.map((c) => String(c[0]))
      expect(logged.some((l) => l.includes('[plugin-bridge] handshake_err'))).toBe(true)
    } finally {
      errSpy.mockRestore()
      infoSpy.mockRestore()
    }
  })
})

describe('plugin-bridge host: session bind', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    _resetPluginBridgePortsForTest()
    vi.mocked(appmanagerSessionClient.sessionCreate).mockResolvedValue(SESSION_CREATE_RESP)
    vi.mocked(appmanagerSessionClient.sessionResolve).mockResolvedValue(SESSION_RESOLVE_RESP)
    vi.mocked(appmanagerSessionClient.sessionRevoke).mockResolvedValue({})
  })

  it('binds a session without exposing any direct-HTTP fields', async () => {
    vi.mocked(appmanagerSessionClient.sessionCreate).mockResolvedValue({
      ...SESSION_CREATE_RESP,
      BackendUrl: 'http://127.0.0.1:4001',
      CookieToken: 'session-1.abcdef',
    })
    const source = { postMessage: vi.fn() }
    const binding = await bindPluginBridgeSource(source, 'trusted.app', 'main')
    expect('backendUrl' in binding).toBe(false)
    expect('cookieToken' in binding).toBe(false)
    expect(typeof binding.revoke).toBe('function')
  })

  it('revokes by token (not SessionId) on unmount', async () => {
    const source = { postMessage: vi.fn() }
    const binding = await bindPluginBridgeSource(source, 'trusted.app', 'main')
    await binding.revoke()
    expect(appmanagerSessionClient.sessionRevoke).toHaveBeenCalledWith(
      expect.anything(),
      { Token: 'token-signed-1' },
    )
    const revokeCall = vi.mocked(appmanagerSessionClient.sessionRevoke).mock.calls[0]
    expect(revokeCall?.[1]).not.toHaveProperty('SessionId')
  })
})

describe('plugin-bridge host: DOM snapshot routing', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    _resetPluginBridgePortsForTest()
    vi.mocked(appmanagerSessionClient.sessionCreate).mockResolvedValue(SESSION_CREATE_RESP)
    vi.mocked(appmanagerSessionClient.sessionResolve).mockResolvedValue(SESSION_RESOLVE_RESP)
    vi.mocked(pluginhostClient.pluginDomPut).mockClear()
  })

  it('returns false when no view of the plugin is mounted', () => {
    expect(requestPluginDomSnapshot('trusted.app')).toBe(false)
  })

  it('posts the request frame on the registered port and pushes the reply to plugin_dom_put', async () => {
    const { port } = await makeBoundPort()
    expect(requestPluginDomSnapshot('trusted.app')).toBe(true)
    expect(port.messages[0]).toMatchObject({ type: 'sporemind:dom-snapshot-request' })

    vi.mocked(pluginhostClient.pluginDomPut).mockResolvedValue({} as never)
    port.dispatch({ type: 'sporemind:dom-snapshot', snapshot: '<html>...</html>' })

    await vi.waitFor(() => {
      expect(pluginhostClient.pluginDomPut).toHaveBeenCalled()
    })
    const req = vi.mocked(pluginhostClient.pluginDomPut).mock.calls[0]![1]
    expect(req).toMatchObject({ PluginId: 'trusted.app', Snapshot: '<html>...</html>' })
    expect(typeof req.Ts).toBe('number')
  })

  it('accepts the envelope-shaped reply variant (Type/Snapshot fields)', async () => {
    const { port } = await makeBoundPort()
    requestPluginDomSnapshot('trusted.app')
    vi.mocked(pluginhostClient.pluginDomPut).mockResolvedValue({} as never)
    port.dispatch({ Type: 'sporemind:dom-snapshot', Snapshot: 'envelope-shaped' })
    await vi.waitFor(() => {
      expect(pluginhostClient.pluginDomPut).toHaveBeenCalled()
    })
    expect(vi.mocked(pluginhostClient.pluginDomPut).mock.calls[0]![1].Snapshot).toBe('envelope-shaped')
  })

  it('snapshots the first mounted view and forgets detached ports', async () => {
    const first = await makeBoundPort()
    const second = await makeBoundPort()
    expect(requestPluginDomSnapshot('trusted.app')).toBe(true)
    expect(first.port.messages).toHaveLength(1)
    expect(second.port.messages).toHaveLength(0)

    detachPluginBridgePort(first.port as unknown as MessagePort)
    expect(requestPluginDomSnapshot('trusted.app')).toBe(true)
    expect(second.port.messages).toHaveLength(1)
  })
})

describe('plugin-bridge host: envelope origin validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    _resetPluginBridgePortsForTest()
    vi.mocked(appmanagerSessionClient.sessionCreate).mockResolvedValue(SESSION_CREATE_RESP)
    vi.mocked(appmanagerSessionClient.sessionResolve).mockResolvedValue(SESSION_RESOLVE_RESP)
    vi.mocked(appmanagerSessionClient.sessionRevoke).mockResolvedValue({})
    vi.mocked(appmanagerAgentClient.agentAction).mockReset()
    vi.mocked(pluginhostClient.pluginLogPut).mockClear()
  })

  it('rejects console-log frames without PluginId', async () => {
    const { port } = await makeBoundPort()
    port.dispatch({ Type: 'sporemind.console-log', RequestId: 'x', Level: 'info', Message: 'no origin' })
    await new Promise((r) => setTimeout(r, 10))
    expect(pluginhostClient.pluginLogPut).not.toHaveBeenCalled()
  })

  it('rejects console-log frames with a wrong PluginId', async () => {
    const { port } = await makeBoundPort()
    port.dispatch({
      Type: 'sporemind.console-log',
      RequestId: 'x',
      Level: 'info',
      Message: 'forged',
      PluginId: 'evil.app',
    })
    await new Promise((r) => setTimeout(r, 10))
    expect(pluginhostClient.pluginLogPut).not.toHaveBeenCalled()
  })

  it('rejects agent-action frames with a wrong ViewId source claim', async () => {
    const { port } = await makeBoundPort()
    port.dispatch({
      Type: 'sporemind.agent-action',
      RequestId: 'x',
      Action: 'create',
      PluginId: 'trusted.app',
      ViewId: 'not-main',
    })
    await new Promise((r) => setTimeout(r, 10))
    expect(appmanagerAgentClient.agentAction).not.toHaveBeenCalled()
  })

  it('rejects ui-action frames without PluginId', async () => {
    const { port } = await makeBoundPort()
    port.dispatch({ Type: 'sporemind.ui-action', RequestId: 'x', Action: 'set-title', Title: 't' })
    await new Promise((r) => setTimeout(r, 10))
    expect(lastReply(port.messages)).toMatchObject({
      Type: 'sporemind.ui-action-response',
      RequestId: 'x',
      Ok: false,
    })
  })

  it('treats ui-action ViewId as a target, not a source claim', async () => {
    const { port } = await makeBoundPort()
    port.dispatch({
      Type: 'sporemind.ui-action',
      RequestId: 'open-1',
      Action: 'open-view',
      PluginId: 'trusted.app',
      ViewId: 'some-other-view',
    })
    await new Promise((r) => setTimeout(r, 10))
    expect(lastReply(port.messages)).toMatchObject({
      Type: 'sporemind.ui-action-response',
      RequestId: 'open-1',
      Ok: true,
    })
  })
})

describe('plugin-bridge host: UI action handlers via port', () => {
  let dispatched: Array<{ type: string; detail: unknown }>
  let dispatchSpy: (event: Event) => void

  beforeEach(() => {
    vi.clearAllMocks()
    _resetPluginBridgePortsForTest()
    vi.mocked(appmanagerSessionClient.sessionCreate).mockResolvedValue(SESSION_CREATE_RESP)
    vi.mocked(appmanagerSessionClient.sessionResolve).mockResolvedValue(SESSION_RESOLVE_RESP)
    vi.mocked(appmanagerSessionClient.sessionRevoke).mockResolvedValue({})
    dispatched = []
    dispatchSpy = (event: Event) => {
      dispatched.push({ type: event.type, detail: (event as CustomEvent).detail })
    }
    window.addEventListener('sporemind:open-view', dispatchSpy)
    window.addEventListener('sporemind:close-view', dispatchSpy)
    window.addEventListener('sporemind:set-title', dispatchSpy)
    window.addEventListener('sporemind:set-badge', dispatchSpy)
    window.addEventListener('sporemind:notify', dispatchSpy)
  })

  function cleanup() {
    window.removeEventListener('sporemind:open-view', dispatchSpy)
    window.removeEventListener('sporemind:close-view', dispatchSpy)
    window.removeEventListener('sporemind:set-title', dispatchSpy)
    window.removeEventListener('sporemind:set-badge', dispatchSpy)
    window.removeEventListener('sporemind:notify', dispatchSpy)
  }

  it('open-view dispatches a sporemind:open-view event and acks', async () => {
    const { port } = await makeBoundPort()
    port.dispatch({
      Type: 'sporemind.ui-action',
      RequestId: 'open-1',
      Action: 'open-view',
      PluginId: 'trusted.app',
      ViewId: 'settings',
      Title: 'Settings',
      Zone: 'right',
    })
    await vi.waitFor(() => {
      expect(dispatched.some((d) => d.type === 'sporemind:open-view')).toBe(true)
    })
    const evt = dispatched.find((d) => d.type === 'sporemind:open-view')
    expect(evt?.detail).toMatchObject({ viewId: 'settings', title: 'Settings', zone: 'right' })
    const reply = lastReply(port.messages)
    expect(reply).toMatchObject({ Type: 'sporemind.ui-action-response', RequestId: 'open-1', Ok: true })
    cleanup()
  })

  it('close-view dispatches a sporemind:close-view event and acks', async () => {
    const { port } = await makeBoundPort()
    port.dispatch({
      Type: 'sporemind.ui-action',
      RequestId: 'close-1',
      Action: 'close-view',
      PluginId: 'trusted.app',
      ViewId: 'settings',
    })
    await vi.waitFor(() => {
      expect(dispatched.some((d) => d.type === 'sporemind:close-view')).toBe(true)
    })
    const evt = dispatched.find((d) => d.type === 'sporemind:close-view')
    expect(evt?.detail).toMatchObject({ viewId: 'settings' })
    const reply = lastReply(port.messages)
    expect(reply).toMatchObject({ Type: 'sporemind.ui-action-response', RequestId: 'close-1', Ok: true })
    cleanup()
  })

  it('set-title dispatches a sporemind:set-title event and acks', async () => {
    const { port } = await makeBoundPort()
    port.dispatch({
      Type: 'sporemind.ui-action',
      RequestId: 'title-1',
      Action: 'set-title',
      PluginId: 'trusted.app',
      Title: 'New Title',
    })
    await vi.waitFor(() => {
      expect(dispatched.some((d) => d.type === 'sporemind:set-title')).toBe(true)
    })
    const evt = dispatched.find((d) => d.type === 'sporemind:set-title')
    expect(evt?.detail).toMatchObject({ title: 'New Title', pluginID: 'trusted.app' })
    const reply = lastReply(port.messages)
    expect(reply).toMatchObject({ Type: 'sporemind.ui-action-response', RequestId: 'title-1', Ok: true })
    cleanup()
  })

  it('set-badge dispatches a sporemind:set-badge event and acks', async () => {
    const { port } = await makeBoundPort()
    port.dispatch({
      Type: 'sporemind.ui-action',
      RequestId: 'badge-1',
      Action: 'set-badge',
      PluginId: 'trusted.app',
      Text: '5',
    })
    await vi.waitFor(() => {
      expect(dispatched.some((d) => d.type === 'sporemind:set-badge')).toBe(true)
    })
    const evt = dispatched.find((d) => d.type === 'sporemind:set-badge')
    expect(evt?.detail).toMatchObject({ text: '5' })
    const reply = lastReply(port.messages)
    expect(reply).toMatchObject({ Type: 'sporemind.ui-action-response', RequestId: 'badge-1', Ok: true })
    cleanup()
  })

  it('notify dispatches a sporemind:notify event and acks', async () => {
    const { port } = await makeBoundPort()
    port.dispatch({
      Type: 'sporemind.ui-action',
      RequestId: 'notify-1',
      Action: 'notify',
      PluginId: 'trusted.app',
      Level: 'info',
      Title: 'Hi',
      Body: 'Body',
    })
    await vi.waitFor(() => {
      expect(dispatched.some((d) => d.type === 'sporemind:notify')).toBe(true)
    })
    const evt = dispatched.find((d) => d.type === 'sporemind:notify')
    expect(evt?.detail).toMatchObject({ title: 'Hi', body: 'Body', level: 'info' })
    const reply = lastReply(port.messages)
    expect(reply).toMatchObject({ Type: 'sporemind.ui-action-response', RequestId: 'notify-1', Ok: true })
    cleanup()
  })
})

describe('plugin-bridge host: agent-action error path', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    _resetPluginBridgePortsForTest()
    vi.mocked(appmanagerSessionClient.sessionCreate).mockResolvedValue(SESSION_CREATE_RESP)
    vi.mocked(appmanagerSessionClient.sessionResolve).mockResolvedValue(SESSION_RESOLVE_RESP)
    vi.mocked(appmanagerAgentClient.agentAction).mockReset()
  })

  it('surfaces backend agent-action errors as a failed response', async () => {
    const { port } = await makeBoundPort()
    vi.mocked(appmanagerAgentClient.agentAction).mockRejectedValue(new Error('not allowed'))
    port.dispatch({
      Type: 'sporemind.agent-action',
      RequestId: 'ag-err',
      Action: 'create',
      PluginId: 'trusted.app',
    })
    await vi.waitFor(() => {
      expect(lastReply(port.messages)).toMatchObject({ Type: 'sporemind.agent-action-response', RequestId: 'ag-err', Ok: false })
    })
    expect(String(lastReply(port.messages).Error)).toContain('not allowed')
  })
})
