import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { PluginTabView } from './PluginToolbar'
import { appRegistry } from '../../application/app-registry'
import { __setGatewayUrlsForTest, type GatewayUrls } from '../../application/gateway'
import {
  beginLocalFileDragSession,
  endLocalFileDragSession,
  getLocalFileDragPayload,
  LOCAL_FILE_DND_MIME,
  type FileDragPayload,
} from '../file-dnd'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('../../application/generated-client', () => ({
  client: {},
}))

// The storage-directory button is desktop-only (Wails reveal binding) and
// resolves the grant dir through pluginhost.appdata_usage; both edges are
// mocked here. isWails is override-able so one suite covers web + desktop.
const runtimeState = vi.hoisted(() => ({ wails: false }))
const storageMocks = vi.hoisted(() => ({
  appdataUsage: vi.fn(),
  openDirectory: vi.fn(),
}))

vi.mock('../../application/runtime', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../application/runtime')>()
  return { ...actual, isWails: () => runtimeState.wails }
})

vi.mock('../../gen-clients/pluginhost/client', () => ({
  appdataUsage: storageMocks.appdataUsage,
}))

vi.mock('../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app')>()
  return { ...actual, OpenDirectory: storageMocks.openDirectory }
})

describe('PluginTabView toolbar', () => {
  beforeEach(() => {
    __setGatewayUrlsForTest({ ws: 'ws://127.0.0.1:18080/ws', http: 'http://127.0.0.1:18080', source: 'default', resolvedAt: Date.now() } satisfies GatewayUrls)
    appRegistry.clear()
    endLocalFileDragSession()
  })

  it('shows running status and remounts the iframe on refresh click', () => {
    act(() => {
      appRegistry.upsert({ id: 'com.example.test', runtime: 'native', state: 'running', version: '1.0.0', namespace: 'testapp', entrypoints: [], generation: 1 })
    })

    const container = document.createElement('div')
    let root: Root | undefined
    act(() => {
      root = createRoot(container)
      root.render(<PluginTabView pluginID="com.example.test" viewID="test.view" route="/" />)
    })

    expect(container.querySelector('[data-testid="plugin-toolbar-name"]')?.textContent).toBe('testapp')
    expect(container.querySelector('[data-testid="plugin-toolbar-label"]')?.textContent).toBe('Running')
    const first = container.querySelector('iframe')
    expect(first).not.toBeNull()

    const btn = container.querySelector<HTMLButtonElement>('.plugin-toolbar-btn:not(.plugin-toolbar-toggle)')!
    expect(btn.disabled).toBe(false)
    act(() => { btn.click() })

    const second = container.querySelector('iframe')
    expect(second).not.toBeNull()
    // Remount: the DOM node was recreated, so the browser re-fetches the document.
    expect(second).not.toBe(first)

    act(() => { root?.unmount() })
  })

  it('clears the refresh spin when the remounted iframe document loads', () => {
    act(() => {
      appRegistry.upsert({ id: 'com.example.test', runtime: 'native', state: 'running', version: '1.0.0', namespace: 'testapp', entrypoints: [], generation: 1 })
    })

    const container = document.createElement('div')
    let root: Root | undefined
    act(() => {
      root = createRoot(container)
      root.render(<PluginTabView pluginID="com.example.test" viewID="test.view" route="/" />)
    })

    const btn = () => container.querySelector<HTMLButtonElement>('.plugin-toolbar-btn:not(.plugin-toolbar-toggle)')!
    expect(btn().disabled).toBe(false)
    act(() => { btn().click() })

    // While refreshing: spinning and disabled.
    expect(btn().disabled).toBe(true)

    // The refresh remounted the iframe (new element); its load event must
    // reset the spin. Regression: the load listener used to stay bound to
    // the discarded element, leaving the toolbar stuck refreshing forever.
    const frame = container.querySelector('iframe')!
    act(() => { frame.dispatchEvent(new Event('load')) })
    expect(btn().disabled).toBe(false)

    act(() => { root?.unmount() })
  })

  it('expands to show callables, events and bundles', () => {
    act(() => {
      appRegistry.upsert({
        id: 'com.example.test',
        runtime: 'native',
        state: 'running',
        version: '1.0.0',
        namespace: 'testapp',
        entrypoints: [],
        callables: [{ Id: 'codes', Description: 'TOTP 验证码生成', RequestSchema: 'codes_request', ResponseSchema: 'codes_response', Effect: 'read' }],
        events: [{ Id: 'tick', PayloadSchema: 'tick_payload' }],
        bundles: [{ Title: 'Auth tools', Description: 'TOTP helpers', Tools: [{ CallableId: 'codes', ToolName: 'totp_codes', Effect: 'read' }] }],
        schemaDescriptors: {
          codes_request: { Kind: 'class', Name: 'codes_request', Fields: [{ Name: 'AccountId', Type: { Kind: 'primitive', Name: 'string' }, Optional: true }] },
          codes_response: { Kind: 'class', Name: 'codes_response', Fields: [{ Name: 'Accounts', Type: { Kind: 'array', Element: { Kind: 'class', Name: 'TotpAccount' } } }] },
        },
      })
    })

    const container = document.createElement('div')
    let root: Root | undefined
    act(() => {
      root = createRoot(container)
      root.render(<PluginTabView pluginID="com.example.test" viewID="test.view" route="/" />)
    })

    // Collapsed by default: no details panel.
    expect(container.querySelector('[data-testid="plugin-details"]')).toBeNull()

    const toggle = container.querySelector<HTMLButtonElement>('[data-testid="plugin-toolbar-toggle"]')!
    act(() => { toggle.click() })

    const details = container.querySelector('[data-testid="plugin-details"]')
    expect(details).not.toBeNull()
    const text = details!.textContent ?? ''
    expect(text).toContain('codes')
    // Description moved out of the row (it doubled the row height): it must
    // only appear in the hover tooltip, not inline.
    expect(text).not.toContain('TOTP 验证码生成')
    expect(text).toContain('codes_request → codes_response')
    expect(text).toContain('tick')
    expect(text).toContain('Auth tools')
    expect(text).toContain('totp_codes')

    // Hovering the callable id shows its description in a portaled tooltip.
    const idAnchor = container.querySelector<HTMLElement>('.plugin-callable-id')!
    expect(idAnchor.classList.contains('has-desc')).toBe(true)
    expect(document.body.querySelector('[data-testid="callable-tip-codes"]')).toBeNull()
    act(() => { idAnchor.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })) })
    const callTip = document.body.querySelector('[data-testid="callable-tip-codes"]')
    expect(callTip).not.toBeNull()
    expect(callTip!.textContent).toContain('TOTP 验证码生成')
    act(() => { idAnchor.dispatchEvent(new MouseEvent('mouseout', { bubbles: true })) })
    expect(document.body.querySelector('[data-testid="callable-tip-codes"]')).toBeNull()

    // Bundle tool renders as a badge button; clicking it locates the callable.
    const badge = container.querySelector<HTMLButtonElement>('.plugin-tool-badge')!
    expect(badge).not.toBeNull()
    const callableRow = container.querySelector('[data-callable-id="codes"]')!
    expect(callableRow.classList.contains('flash')).toBe(false)
    act(() => { badge.click() })
    expect(container.querySelector('[data-callable-id="codes"]')!.classList.contains('flash')).toBe(true)

    // Hovering a schema name shows its JSON-schema fields in a tooltip.
    const reqName = Array.from(container.querySelectorAll<HTMLElement>('.plugin-schema-name'))
      .find((el) => el.textContent === 'codes_request')!
    expect(reqName.classList.contains('has-schema')).toBe(true)
    expect(document.body.querySelector('[data-testid="schema-tip-codes_request"]')).toBeNull()
    act(() => { reqName.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })) })
    const tip = document.body.querySelector('[data-testid="schema-tip-codes_request"]')
    expect(tip).not.toBeNull()
    expect(tip!.textContent).toContain('AccountId')
    expect(tip!.textContent).toContain('string')
    // Leaving the name hides the tooltip.
    act(() => { reqName.dispatchEvent(new MouseEvent('mouseout', { bubbles: true })) })
    expect(document.body.querySelector('[data-testid="schema-tip-codes_request"]')).toBeNull()

    act(() => { root?.unmount() })
  })

  it('shows crashed status and disables refresh when the app is not running', () => {
    act(() => {
      appRegistry.upsert({ id: 'com.example.test', runtime: 'native', state: 'crashed', version: '1.0.0', entrypoints: [], error: 'plugin process exited: EOF' })
    })

    const container = document.createElement('div')
    let root: Root | undefined
    act(() => {
      root = createRoot(container)
      root.render(<PluginTabView pluginID="com.example.test" viewID="test.view" route="/" />)
    })

    expect(container.querySelector('[data-testid="plugin-toolbar-label"]')?.textContent).toBe('Crashed')
    const btn = container.querySelector<HTMLButtonElement>('.plugin-toolbar-btn:not(.plugin-toolbar-toggle)')!
    expect(btn.disabled).toBe(true)

    act(() => { root?.unmount() })
  })

  it('mounts a drop overlay during in-app file drags and forwards the drop payload', async () => {
    act(() => {
      appRegistry.upsert({ id: 'com.example.test', runtime: 'native', state: 'running', version: '1.0.0', namespace: 'testapp', entrypoints: [], generation: 1 })
    })

    const container = document.createElement('div')
    let root: Root | undefined
    act(() => {
      root = createRoot(container)
      root.render(<PluginTabView pluginID="com.example.test" viewID="test.view" route="/" />)
    })

    // No drag session → no overlay; the iframe keeps receiving events.
    expect(container.querySelector('[data-testid="plugin-tab-dnd-overlay"]')).toBeNull()

    act(() => { beginLocalFileDragSession() })
    const overlay = container.querySelector('[data-testid="plugin-tab-dnd-overlay"]') as HTMLElement
    expect(overlay).toBeTruthy()

    const events: unknown[] = []
    const onPreviewOpen = (e: Event) => { events.push((e as CustomEvent).detail) }
    window.addEventListener('sporemind:file-preview-open', onPreviewOpen)

    // Drop with a local FileBrowser payload: forwarded as a window event.
    // The forward chain (forwardFileDropToPlugin) is async — in this
    // environment no plugin panel acks, so the read-only preview event fires
    // from the .then; flush microtasks via the async act before asserting.
    const payload: FileDragPayload = { origin: 'local', projectId: 'proj-1', path: 'docs/a.png', name: 'a.png', isDir: false }
    const dt = {
      getData: (mime: string) => (mime === LOCAL_FILE_DND_MIME ? JSON.stringify(payload) : ''),
    }
    await act(async () => {
      const drop = new Event('drop', { bubbles: true, cancelable: true })
      Object.defineProperty(drop, 'dataTransfer', { value: dt, configurable: true })
      overlay.dispatchEvent(drop)
    })
    expect(events).toEqual([payload])
    // The same DataTransfer still decodes through the shared helper.
    expect(getLocalFileDragPayload(dt as unknown as DataTransfer)).toEqual(payload)

    // Session end removes the overlay again.
    act(() => { endLocalFileDragSession() })
    expect(container.querySelector('[data-testid="plugin-tab-dnd-overlay"]')).toBeNull()

    window.removeEventListener('sporemind:file-preview-open', onPreviewOpen)
    act(() => { root?.unmount() })
  })

  it('toggles the plugin log panel from the toolbar logs button', () => {
    act(() => {
      appRegistry.upsert({ id: 'com.example.test', runtime: 'native', state: 'running', version: '1.0.0', namespace: 'testapp', entrypoints: [], generation: 1 })
    })

    const container = document.createElement('div')
    let root: Root | undefined
    act(() => {
      root = createRoot(container)
      root.render(<PluginTabView pluginID="com.example.test" viewID="test.view" route="/" />)
    })

    const logsBtn = () => container.querySelector<HTMLButtonElement>('[data-testid="plugin-toolbar-logs"]')!
    expect(logsBtn().getAttribute('aria-pressed')).toBe('false')
    expect(container.querySelector('[data-testid="plugin-log-panel"]')).toBeNull()

    act(() => { logsBtn().click() })
    expect(logsBtn().getAttribute('aria-pressed')).toBe('true')
    expect(logsBtn().classList.contains('plugin-toolbar-btn-active')).toBe(true)
    expect(container.querySelector('[data-testid="plugin-log-panel"]')).not.toBeNull()

    act(() => { logsBtn().click() })
    expect(logsBtn().getAttribute('aria-pressed')).toBe('false')
    expect(container.querySelector('[data-testid="plugin-log-panel"]')).toBeNull()

    act(() => { root?.unmount() })
  })

  it('opens the plugin storage directory from the toolbar button', async () => {
    // Web runtime: no Wails bridge, the button stays disabled.
    act(() => {
      appRegistry.upsert({ id: 'com.example.test', runtime: 'native', state: 'running', version: '1.0.0', namespace: 'testapp', entrypoints: [], generation: 1 })
    })
    const container = document.createElement('div')
    let root: Root | undefined
    act(() => {
      root = createRoot(container)
      root.render(<PluginTabView pluginID="com.example.test" viewID="test.view" route="/" />)
    })
    expect(container.querySelector<HTMLButtonElement>('[data-testid="plugin-toolbar-storage"]')!.disabled).toBe(true)
    act(() => { root?.unmount() })

    runtimeState.wails = true
    storageMocks.appdataUsage.mockResolvedValue({
      Items: [{ PluginId: 'com.example.test', DataDir: 'D:/nomos/appdata', Bytes: 42, Files: 3, Loaded: true }],
      TotalBytes: 42,
      SoftLimitBytes: 1024,
    })
    storageMocks.openDirectory.mockResolvedValue(undefined)
    try {
      const desktopContainer = document.createElement('div')
      act(() => {
        root = createRoot(desktopContainer)
        root.render(<PluginTabView pluginID="com.example.test" viewID="test.view" route="/" />)
      })
      const btn = desktopContainer.querySelector<HTMLButtonElement>('[data-testid="plugin-toolbar-storage"]')!
      expect(btn.disabled).toBe(false)

      await act(async () => { btn.click() })
      expect(storageMocks.appdataUsage).toHaveBeenCalledWith({}, { PluginId: 'com.example.test' })
      expect(storageMocks.openDirectory).toHaveBeenCalledWith('D:/nomos/appdata')

      act(() => { root?.unmount() })
    } finally {
      runtimeState.wails = false
      storageMocks.appdataUsage.mockReset()
      storageMocks.openDirectory.mockReset()
    }
  })
})
