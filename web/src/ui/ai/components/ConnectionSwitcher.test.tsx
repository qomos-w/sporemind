import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { I18nProvider } from '../../../i18n'
import { BrowserOverlayProvider, type BrowserOverlayApi } from '../browserOverlay'
import { recomputeRuntime, isWails } from '../../../application/runtime'
import { ConnectionSwitcher } from './ConnectionSwitcher'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../application/runtime', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../application/runtime')>()
  return { ...actual, isWails: vi.fn(() => true) }
})

const switchMock = vi.fn(async (_target: string) => {})
const saveMock = vi.fn(async (_conn: unknown, _password: string) => ({ id: 'c1', name: 'Lab', host: '192.168.1.5', port: 18080, username: 'admin', instanceId: '', updatedAt: '', hasPassword: true }))
const probeMock = vi.fn(async (_host: string, _port: number) => ({ reachable: true, instanceId: 'fp-remote', isSelf: false, message: '' }))
const listMock = vi.fn(async () => [
  { id: 'c1', name: 'Lab', host: '192.168.1.5', port: 18080, username: 'admin', instanceId: 'fp-1', updatedAt: '', hasPassword: true },
])

vi.mock('../../../application/remote-connections', () => ({
  remoteConnectionId: () => 'c1',
  isRemoteConnectionWindow: () => true,
  listConnections: () => listMock(),
  saveConnection: (conn: unknown, password: string) => saveMock(conn, password),
  deleteConnection: vi.fn(async () => {}),
  probeConnection: (host: string, port: number) => probeMock(host, port),
  switchConnection: (target: string) => switchMock(target),
  getActiveConnection: vi.fn(async () => ({ target: 'c1', name: 'Lab', host: '192.168.1.5', port: 18080 })),
}))

function makeWindow(): void {
  // Proxy over the real happy-dom window so component code keeps working
  // (addEventListener, innerHeight, DOM prototypes) while runtime detection
  // sees a Wails window pointed at a ?server= remote target.
  const base = globalThis.window as unknown as Record<string, unknown>
  const fakeLocation = {
    href: 'http://app/?server=ws%3A%2F%2F192.168.1.5%3A18080%2Fws&conn=c1',
    host: 'app',
    origin: 'http://app',
    protocol: 'http:',
    search: '?server=ws%3A%2F%2F192.168.1.5%3A18080%2Fws&conn=c1',
  }
  const w = new Proxy(base, {
    get(target, prop, receiver) {
      if (prop === 'location') return fakeLocation
      if (prop === 'chrome') return { webview: { postMessage: () => {} } }
      if (prop === 'self' || prop === 'top') return receiver
      const value = Reflect.get(target, prop, target)
      // Bind plain methods but leave constructors (Event, HTMLInputElement,
      // …) untouched — binding strips their .prototype.
      if (typeof value === 'function' && /^[a-z]/.test(String(prop))) {
        return (value as (...a: unknown[]) => unknown).bind(target)
      }
      return value
    },
  })
  globalThis.window = w as unknown as Window & typeof globalThis
  recomputeRuntime()
}

describe('ConnectionSwitcher', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.clearAllMocks()
    makeWindow()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(() => {
    act(() => { root?.unmount() })
    container.remove()
    vi.restoreAllMocks()
  })

  const renderSwitcher = async () => {
    const overlayApi: BrowserOverlayApi = { pushOverlay: vi.fn(), popOverlay: vi.fn() }
    await act(async () => {
      root = createRoot(container)
      root.render(
        <I18nProvider initialLocale="en-US">
          <BrowserOverlayProvider value={overlayApi}>
            <ConnectionSwitcher />
          </BrowserOverlayProvider>
        </I18nProvider>,
      )
    })
  }

  const openMenu = async () => {
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[data-testid="connection-switcher-button"]')!.click()
    })
  }

  it('renders nothing outside wails', async () => {
    ;(isWails as unknown as ReturnType<typeof vi.fn>).mockReturnValueOnce(false)
    await renderSwitcher()
    expect(container.querySelector('.ai-sidebar-connection-menu')).toBeNull()
  })

  it('lists local + saved connections and switches window target on click', async () => {
    await renderSwitcher()
    await openMenu()
    const local = container.querySelector('[data-testid="connection-item-local"]') as HTMLButtonElement
    expect(local).toBeTruthy()
    expect(container.querySelector('[data-testid="connection-item-c1"]')).toBeTruthy()
    expect(container.textContent).toContain('192.168.1.5:18080')
    await act(async () => { local.click() })
    expect(switchMock).toHaveBeenCalledWith('local')
  })

  it('opens the add modal, probes, and saves with the learned fingerprint', async () => {
    await renderSwitcher()
    await openMenu()
    await act(async () => {
      ;(container.querySelector('[data-testid="connection-add"]') as HTMLButtonElement).click()
    })
    const set = (testid: string, value: string) => {
      const el = container.querySelector<HTMLInputElement>(`[data-testid="${testid}"]`)!
      fireEventCompat(el, value)
    }
    await act(async () => {
      set('connection-field-host', '10.0.0.9')
      set('connection-field-name', 'Office')
      ;(container.querySelector('[data-testid="connection-probe"]') as HTMLButtonElement).click()
    })
    await act(async () => {})
    expect(probeMock).toHaveBeenCalledWith('10.0.0.9', 18080)
    await act(async () => {
      ;(container.querySelector('[data-testid="connection-save"]') as HTMLButtonElement).click()
    })
    expect(saveMock).toHaveBeenCalledTimes(1)
    const [payload, password] = saveMock.mock.calls[0] as unknown as [{ name: string; host: string; instanceId?: string }, string]
    expect(payload.name).toBe('Office')
    expect(payload.host).toBe('10.0.0.9')
    expect(payload.instanceId).toBe('fp-remote')
    expect(password).toBe('')
  })

  it('blocks saving when the probe reports this same instance', async () => {
    probeMock.mockResolvedValueOnce({ reachable: true, instanceId: 'self', isSelf: true, message: '' })
    await renderSwitcher()
    await openMenu()
    await act(async () => {
      ;(container.querySelector('[data-testid="connection-add"]') as HTMLButtonElement).click()
    })
    await act(async () => {
      fireEventCompat(container.querySelector<HTMLInputElement>('[data-testid="connection-field-host"]')!, '127.0.0.1')
      ;(container.querySelector('[data-testid="connection-probe"]') as HTMLButtonElement).click()
    })
    await act(async () => {})
    expect(container.textContent).toContain('Cannot connect to this same instance')
    const saveBtn = container.querySelector<HTMLButtonElement>('[data-testid="connection-save"]')!
    expect(saveBtn.disabled).toBe(true)
    expect(saveMock).not.toHaveBeenCalled()
  })
})

function fireEventCompat(el: HTMLInputElement, value: string): void {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  setter.call(el, value)
  el.dispatchEvent(new window.Event('input', { bubbles: true }))
}
