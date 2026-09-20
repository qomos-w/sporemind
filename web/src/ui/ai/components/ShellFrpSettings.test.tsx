import { describe, expect, it } from 'vitest'
import type { FrpInstance, FrpProxyStatus } from '../../../gen-types/frp'
import { proxyErrorOf, statusOf } from './ShellFrpSettings'

function proxy(name: string, status: string, err?: string): FrpProxyStatus {
  return { Name: name, Status: status, Err: err, RemoteAddr: '1.2.3.4:6022' }
}

function inst(config: Partial<FrpInstance['Config']>, status: Partial<FrpInstance['Status']>): FrpInstance {
  return {
    Config: { Id: 'inst-1', Name: 't', ServerAddr: 'a:7000', Proxies: [], ...config },
    Status: { Id: 'inst-1', Running: false, ...status },
  } as FrpInstance
}

const t = (key: string) => key

describe('statusOf', () => {
  it('disabled wins over a stale error: stopped tunnels show "stopped"', () => {
    expect(statusOf(inst({ Disabled: true }, { Error: 'old crash' }), t)).toEqual({
      tone: 'off',
      label: 'settings.frp.status.stopped',
    })
  })

  it('failed run (enabled, not running, error) shows "error"', () => {
    expect(statusOf(inst({}, { Error: 'login failed' }), t)).toEqual({
      tone: 'err',
      label: 'settings.frp.status.error',
    })
  })

  it('connected shows "running"', () => {
    expect(
      statusOf(inst({}, { Running: true, Connected: true, Proxies: [proxy('ssh', 'running')] }), t),
    ).toEqual({ tone: 'ok', label: 'settings.frp.status.running' })
  })

  it('running but not yet connected shows "connecting", not "running"', () => {
    expect(
      statusOf(inst({}, { Running: true, Connected: false, Proxies: [proxy('ssh', 'new')] }), t),
    ).toEqual({ tone: 'wait', label: 'settings.frp.status.connecting' })
  })

  it('running process with a proxy start error shows "error"', () => {
    expect(
      statusOf(
        inst({}, { Running: true, Connected: false, Proxies: [proxy('ssh', 'start error', 'port already used')] }),
        t,
      ),
    ).toEqual({ tone: 'err', label: 'settings.frp.status.error' })
  })

  it('legacy running-only status (no Connected field) is not reported as running', () => {
    expect(statusOf(inst({}, { Running: true }), t)).toEqual({
      tone: 'wait',
      label: 'settings.frp.status.connecting',
    })
  })

  it('enabled, not running, no error shows "idle"', () => {
    expect(statusOf(inst({}, {}), t)).toEqual({ tone: 'off', label: 'settings.frp.status.idle' })
  })
})

describe('proxyErrorOf', () => {
  it('returns instance error first', () => {
    expect(proxyErrorOf(inst({}, { Error: 'login failed' }))).toBe('login failed')
  })

  it('surfaces a per-proxy start error with the proxy name', () => {
    expect(
      proxyErrorOf(inst({}, { Running: true, Proxies: [proxy('web', 'start error', 'port conflict')] })),
    ).toBe('web: port conflict')
  })

  it('ignores non-error phases even if err text lingers', () => {
    expect(proxyErrorOf(inst({}, { Running: true, Proxies: [proxy('web', 'running', 'stale')] }))).toBeNull()
  })
})
