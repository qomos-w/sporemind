import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  loadDesktopConfig,
  getDesktopConfig,
  saveDesktopConfig,
  setDesktopConfigForTest,
} from './desktop-config'
import { GATEWAY_DEFAULT_ADDR } from './gateway'
import { recomputeRuntime } from './runtime'

vi.mock('../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  GetDesktopTransport: vi.fn(),
  GetRawGatewayAddr: vi.fn(),
  GetGatewayBindAddrs: vi.fn(),
  SetDesktopTransport: vi.fn(),
  SetGatewayAddr: vi.fn(),
  SetGatewayBindAddrs: vi.fn(),
  SaveConfig: vi.fn(),
}))

import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'

const mockedDesktop = desktop as unknown as {
  GetDesktopTransport: ReturnType<typeof vi.fn>
  GetRawGatewayAddr: ReturnType<typeof vi.fn>
  GetGatewayBindAddrs: ReturnType<typeof vi.fn>
  SetDesktopTransport: ReturnType<typeof vi.fn>
  SetGatewayAddr: ReturnType<typeof vi.fn>
  SetGatewayBindAddrs: ReturnType<typeof vi.fn>
  SaveConfig: ReturnType<typeof vi.fn>
}

beforeEach(() => {
  setDesktopConfigForTest(null)
  vi.clearAllMocks()
})

afterEach(() => {
  setDesktopConfigForTest(null)
  delete (window as any).chrome
  recomputeRuntime()
})

describe('loadDesktopConfig', () => {
  it('returns defaults in non-Wails environment', async () => {
    const cfg = await loadDesktopConfig()
    expect(cfg.transport).toBe('wails')
    expect(cfg.gatewayAddr).toBe(GATEWAY_DEFAULT_ADDR)
    expect(getDesktopConfig()).toEqual(cfg)
  })

  it('loads ws transport from Wails bindings', async () => {
    ;(window as any).chrome = { webview: { postMessage: vi.fn() } }
    recomputeRuntime()
    mockedDesktop.GetDesktopTransport.mockResolvedValue('ws')
    mockedDesktop.GetRawGatewayAddr.mockResolvedValue(':18082')
    mockedDesktop.GetGatewayBindAddrs.mockResolvedValue([])

    const cfg = await loadDesktopConfig()
    expect(cfg.transport).toBe('ws')
    expect(cfg.gatewayAddr).toBe(':18082')
    expect(cfg.gatewayBindAddrs).toEqual([])
  })

  it('loads bind addrs from Wails bindings', async () => {
    ;(window as any).chrome = { webview: { postMessage: vi.fn() } }
    recomputeRuntime()
    mockedDesktop.GetDesktopTransport.mockResolvedValue('wails')
    mockedDesktop.GetRawGatewayAddr.mockResolvedValue('127.0.0.1:18080')
    mockedDesktop.GetGatewayBindAddrs.mockResolvedValue(['192.168.1.5:18080'])

    const cfg = await loadDesktopConfig()
    expect(cfg.gatewayAddr).toBe('127.0.0.1:18080')
    expect(cfg.gatewayBindAddrs).toEqual(['192.168.1.5:18080'])
  })

  it('falls back to wails for unknown transport values', async () => {
    ;(window as any).chrome = { webview: { postMessage: vi.fn() } }
    recomputeRuntime()
    mockedDesktop.GetDesktopTransport.mockResolvedValue('unknown')
    mockedDesktop.GetRawGatewayAddr.mockResolvedValue(':18081')

    const cfg = await loadDesktopConfig()
    expect(cfg.transport).toBe('wails')
  })
})

describe('saveDesktopConfig', () => {
  it('rejects outside Wails mode', async () => {
    await expect(
      saveDesktopConfig({ transport: 'ws', gatewayAddr: ':18081', gatewayBindAddrs: [] })
    ).rejects.toThrow('Wails desktop mode')
  })

  it('writes config through Wails bindings', async () => {
    ;(window as any).chrome = { webview: { postMessage: vi.fn() } }
    recomputeRuntime()
    mockedDesktop.SaveConfig.mockResolvedValue(undefined)

    await saveDesktopConfig({ transport: 'ws', gatewayAddr: ':18083', gatewayBindAddrs: ['192.168.1.5:18083'] })

    expect(mockedDesktop.SetDesktopTransport).toHaveBeenCalledWith('ws')
    expect(mockedDesktop.SetGatewayAddr).toHaveBeenCalledWith(':18083')
    expect(mockedDesktop.SetGatewayBindAddrs).toHaveBeenCalledWith(['192.168.1.5:18083'])
    expect(mockedDesktop.SaveConfig).toHaveBeenCalled()
    expect(getDesktopConfig()).toEqual({ transport: 'ws', gatewayAddr: ':18083', gatewayBindAddrs: ['192.168.1.5:18083'] })
  })
})
