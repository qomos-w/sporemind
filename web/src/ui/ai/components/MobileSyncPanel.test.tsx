import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { MobileSyncPanel } from './MobileSyncPanel'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${JSON.stringify(params)}` : key
  ),
  isWails: vi.fn(() => false),
  list: vi.fn(),
  create: vi.fn(),
  start: vi.fn(),
  stop: vi.fn(),
  remove: vi.fn(),
  getLocalUrl: vi.fn(() => Promise.resolve('')),
  toDataURL: vi.fn(() => Promise.resolve('data:image/png;base64,abc')),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/runtime', () => ({
  isWails: hoisted.isWails,
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/frpmanager/client', () => ({
  list: hoisted.list,
  create: hoisted.create,
  start: hoisted.start,
  stop: hoisted.stop,
  remove: hoisted.remove,
}))

vi.mock('../../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  GetLocalNetworkGatewayURL: hoisted.getLocalUrl,
  GetLocalIPv4: vi.fn(() => Promise.resolve('192.168.1.100')),
}))

vi.mock('../../../application/auth-store', () => ({
  isAdmin: vi.fn(() => false),
  isDeveloper: vi.fn(() => false),
}))

vi.mock('qrcode', () => ({
  default: { toDataURL: hoisted.toDataURL },
}))

describe('MobileSyncPanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.list.mockResolvedValue({
      GatewayPort: 3000,
      Items: [
        {
          Config: {
            Id: 'tunnel-1',
            Name: 'mobile-sync',
            ServerAddr: 'frp.example.com:7000',
            Proxies: [{ Name: 'gateway', Kind: 'tcp', LocalIP: '127.0.0.1', LocalPort: 3000, RemotePort: 8080 }],
          },
          Status: { Id: 'tunnel-1', Running: true },
        },
        {
          Config: {
            Id: 'tunnel-2',
            Name: 'other-tunnel',
            ServerAddr: 'frp.example.com:7000',
            Proxies: [{ Name: 'gateway', Kind: 'tcp', LocalIP: '127.0.0.1', LocalPort: 3000, RemotePort: 9090 }],
          },
          Status: { Id: 'tunnel-2', Running: false, Error: 'connection refused' },
        },
      ],
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.unstubAllGlobals()
  })

  const renderPanel = async () => {
    await act(async () => {
      root.render(<MobileSyncPanel open onClose={() => {}} />)
    })
    await vi.waitFor(() => expect(hoisted.list).toHaveBeenCalled())
  }

  const setInputValue = (placeholder: string, value: string) => {
    const input = container.querySelector(`input[placeholder="${placeholder}"]`) as HTMLInputElement
    expect(input).not.toBeNull()
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }

  it('renders FRP history list with status and remote URL', async () => {
    await renderPanel()
    expect(container.textContent).toContain('mobile-sync')
    expect(container.textContent).toContain('other-tunnel')
    expect(container.textContent).toContain('mobileSync.frpRunning')
    expect(container.textContent).toContain('mobileSync.frpError')
    expect(container.textContent).toContain('http://frp.example.com:8080')
  })

  it('generates online URL from WebProxy tcp mode when no proxy matches gateway port', async () => {
    hoisted.list.mockResolvedValue({
      GatewayPort: 3000,
      Items: [
        {
          Config: {
            Id: 'tunnel-wp',
            Name: 'webproxy-tunnel',
            ServerAddr: 'frp.example.com:7000',
            Proxies: [],
            WebProxy: { Enabled: true, Name: 'web', Mode: 'tcp', RemotePort: 6080 },
          },
          Status: { Id: 'tunnel-wp', Running: true, Connected: true },
        },
      ],
    })
    await renderPanel()
    expect(container.textContent).toContain('http://frp.example.com:6080')
  })

  it('does not guess a URL for WebProxy http mode', async () => {
    hoisted.list.mockResolvedValue({
      GatewayPort: 3000,
      Items: [
        {
          Config: {
            Id: 'tunnel-wph',
            Name: 'webproxy-http-tunnel',
            ServerAddr: 'frp.example.com:7000',
            Proxies: [],
            WebProxy: { Enabled: true, Name: 'web', Mode: 'http', RemotePort: 6080 },
          },
          Status: { Id: 'tunnel-wph', Running: true, Connected: true },
        },
      ],
    })
    await renderPanel()
    expect(container.textContent).toContain('webproxy-http-tunnel')
    expect(container.textContent).not.toContain('http://frp.example.com:6080')
  })

  it('creates a tunnel with a custom name when the name input is filled', async () => {
    await renderPanel()
    await act(async () => {
      setInputValue('mobileSync.frpName', 'my-tunnel')
      setInputValue('mobileSync.frpServerHost', 'frp.example.com')
      setInputValue('mobileSync.frpRemotePort', '6080')
    })
    const createBtn = container.querySelector('.mobile-sync-primary-btn') as HTMLButtonElement
    expect(createBtn).not.toBeNull()
    expect(createBtn.disabled).toBe(false)
    await act(async () => {
      createBtn.click()
    })
    await vi.waitFor(() => expect(hoisted.create).toHaveBeenCalled())
    expect(hoisted.create).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ Name: 'my-tunnel' }))
  })

  it('falls back to the default name when the name input is left empty', async () => {
    await renderPanel()
    await act(async () => {
      setInputValue('mobileSync.frpServerHost', 'frp.example.com')
      setInputValue('mobileSync.frpRemotePort', '6080')
    })
    const createBtn = container.querySelector('.mobile-sync-primary-btn') as HTMLButtonElement
    await act(async () => {
      createBtn.click()
    })
    await vi.waitFor(() => expect(hoisted.create).toHaveBeenCalled())
    expect(hoisted.create).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ Name: 'mobile-sync' }))
  })

  it('starts a stopped tunnel', async () => {
    await renderPanel()
    const startBtn = container.querySelector('[title="mobileSync.frpStart"]')
    expect(startBtn).not.toBeNull()
    await act(async () => {
      ;(startBtn as HTMLButtonElement).click()
    })
    await vi.waitFor(() => expect(hoisted.start).toHaveBeenCalledWith(expect.anything(), { Id: 'tunnel-2' }))
  })

  it('stops a running tunnel', async () => {
    await renderPanel()
    const stopBtn = container.querySelector('[title="mobileSync.frpStop"]')
    expect(stopBtn).not.toBeNull()
    await act(async () => {
      ;(stopBtn as HTMLButtonElement).click()
    })
    await vi.waitFor(() => expect(hoisted.stop).toHaveBeenCalledWith(expect.anything(), { Id: 'tunnel-1' }))
  })

  it('removes a tunnel after confirmation', async () => {
    vi.stubGlobal('confirm', vi.fn(() => true))
    await renderPanel()
    const removeBtn = container.querySelectorAll('.mobile-sync-history-btn.danger')[0]
    expect(removeBtn).not.toBeNull()
    await act(async () => {
      ;(removeBtn as HTMLButtonElement).click()
    })
    await vi.waitFor(() => expect(hoisted.remove).toHaveBeenCalledWith(expect.anything(), { Id: 'tunnel-1' }))
  })
})
