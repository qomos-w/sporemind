import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { userEvent } from '@testing-library/user-event'
import { ImSettingsPanel } from './ImSettingsPanel'
import { I18nProvider } from '../../../i18n'
import { BrowserOverlayProvider } from '../browserOverlay'
import * as imAccount from '../../../gen-clients/im.account/client'
import * as imRoute from '../../../gen-clients/im.route/client'
import * as im from '../../../gen-clients/im/client'
import * as workspace from '../../../gen-clients/workspace/client'
import type { ImAccountView, ImRoute, AgentRef } from '../../../gen-clients/system/types'

(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true
// Polyfill for Base UI's animation frames (normally provided by test-setup.ts)
globalThis.requestAnimationFrame = (cb) => setTimeout(cb, 0) as unknown as number
globalThis.cancelAnimationFrame = (id) => clearTimeout(id)

vi.mock('../../../application/generated-client', () => ({ client: {} }))

vi.mock('../../../gen-clients/im.account/client', () => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  _delete: vi.fn(),
}))
vi.mock('../../../gen-clients/im.route/client', () => ({
  list: vi.fn(),
  set: vi.fn(),
  _delete: vi.fn(),
}))
vi.mock('../../../gen-clients/im/client', () => ({
  status: vi.fn(),
  send: vi.fn(),
}))
vi.mock('../../../gen-clients/workspace/client', () => ({
  agents: vi.fn(),
}))

const account: ImAccountView = {
  Id: 'a1',
  Name: 'Acc One',
  Provider: 'telegram',
  Enabled: true,
  HasToken: true,
}

const account2: ImAccountView = {
  ...account,
  Id: 'a2',
  Name: 'Acc Two',
}

const statusItem: ImAccountView = {
  ...account,
  Status: 'connected',
  StatusDetail: 'long polling ok',
  BotUsername: 'mybot',
}

const statusItem2: ImAccountView = {
  ...account2,
  Status: 'connected',
  StatusDetail: 'long polling ok',
  BotUsername: 'mybot2',
}

const mountedRoute: ImRoute = {
  Id: 'r1',
  AccountId: 'a1',
  AgentActorId: 'actor-1',
  MountedAt: '2026-01-02T03:04:05Z',
}

const agent1: AgentRef = {
  Id: 'ag1',
  ActorId: 'actor-1',
  ProjectId: 'p1',
  DisplayName: 'Coder One',
  AgentKind: 'coder',
}

const agent2: AgentRef = {
  ...agent1,
  Id: 'ag2',
  ActorId: 'actor-2',
  DisplayName: 'Coder Two',
}

let container: HTMLDivElement
let root: Root
let pushes = 0
let pops = 0
let realConfirm: ((message?: string) => boolean) | undefined
// Simulated backend mount table shared across the im.route mocks.
let routes: ImRoute[]

function renderPanel() {
  root.render(
    <I18nProvider initialLocale="en-US">
      <BrowserOverlayProvider value={{
        pushOverlay: () => { pushes++ },
        popOverlay: () => { pops++ },
      }}>
        <ImSettingsPanel />
      </BrowserOverlayProvider>
    </I18nProvider>,
  )
}

function q(sel: string): HTMLElement {
  const el = document.querySelector(sel)
  if (!el) throw new Error(`element not found: ${sel}`)
  return el as HTMLElement
}

function setInput(el: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), 'value')!.set!
  setter.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

beforeEach(() => {
  routes = [{ ...mountedRoute }]
  vi.mocked(imAccount.list).mockResolvedValue({ Items: [account, account2] })
  vi.mocked(im.status).mockResolvedValue({ Items: [statusItem, statusItem2] })
  vi.mocked(imRoute.list).mockImplementation(async () => ({ Items: routes.map(r => ({ ...r })) }))
  vi.mocked(workspace.agents).mockResolvedValue({ Items: [agent1, agent2] })
  vi.mocked(imAccount.create).mockResolvedValue({ Account: account })
  vi.mocked(imAccount.update).mockResolvedValue({ Account: account })
  vi.mocked(imRoute.set).mockImplementation(async (_c, req) => {
    const route: ImRoute = {
      Id: 'r2',
      AccountId: req.AccountId,
      AgentActorId: req.AgentActorId,
      MountedAt: '2026-02-03T04:05:06Z',
    }
    routes = routes.filter(r => r.AccountId !== req.AccountId).concat(route)
    return { Route: route }
  })
  vi.mocked(imAccount._delete).mockResolvedValue({})
  vi.mocked(imRoute._delete).mockImplementation(async (_c, req) => {
    routes = routes.filter(r => r.Id !== req.Id)
    return {}
  })
  realConfirm = window.confirm
  window.confirm = () => true
  pushes = 0
  pops = 0
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(async () => {
  await act(async () => { root.unmount() })
  container.remove()
  if (realConfirm) window.confirm = realConfirm
  vi.restoreAllMocks()
})

describe('ImSettingsPanel', () => {
  it('renders accounts with live status and account-level agent mounts', async () => {
    await act(async () => { renderPanel() })
    const text = container.textContent || ''
    expect(text).toContain('Acc One')
    expect(text).toContain('Acc Two')
    expect(text).toContain('Connected')
    expect(text).toContain('@mybot')
    // a1 is mounted: agent identity + MountedAt (UTC stamp rendered locally)
    expect(text).toContain('Coder One')
    expect(text).toContain('actor-1')
    expect(text).toContain('Mounted at')
    expect(text).toContain('2026')
    // a2 is unmounted
    expect(text).toContain('Not mounted')
  })

  it('creates an account with the entered token and registers the dialog overlay', async () => {
    await act(async () => { renderPanel() })
    await act(async () => { q('[data-guide-id="settings/im/add"]').click() })
    expect(pushes).toBe(1)
    await act(async () => {
      setInput(q('[data-guide-id="settings/im/account-name"]') as HTMLInputElement, 'New Bot')
      setInput(q('[data-guide-id="settings/im/token"]') as HTMLInputElement, 'tk123')
    })
    await act(async () => { q('[data-guide-id="settings/im/save-account"]').click() })
    expect(vi.mocked(imAccount.create).mock.calls[0]?.[1]).toEqual({
      Name: 'New Bot',
      Provider: 'telegram',
      Token: 'tk123',
    })
    // Dialog closed after save — the overlay must be released.
    expect(pops).toBe(1)
  })

  it('edit with an empty token keeps the stored secret', async () => {
    await act(async () => { renderPanel() })
    await act(async () => { q('[data-guide-id="settings/im/account-a1/edit"]').click() })
    await act(async () => { q('[data-guide-id="settings/im/save-account"]').click() })
    expect(vi.mocked(imAccount.update).mock.calls[0]?.[1]).toEqual({ Id: 'a1', Name: 'Acc One' })
  })

  it('mounts an unmounted account onto a picked agent via im.route.set', async () => {
    await act(async () => { renderPanel() })
    // Open a2's agent selector — the dropdown registers as a browser overlay.
    await act(async () => { await userEvent.click(q('[data-guide-id="settings/im/mount-a2/agent"]')) })
    expect(pushes).toBe(1)
    const item = q('[data-guide-id="settings/im/mount-a2/agent/actor-2"]')
    await act(async () => { await userEvent.click(item) })
    expect(vi.mocked(imRoute.set).mock.calls[0]?.[1]).toEqual({
      AccountId: 'a2',
      AgentActorId: 'actor-2',
    })
    // Dropdown closed + row reloaded into the mounted state — overlay released.
    expect(pops).toBeGreaterThanOrEqual(1)
    const text = container.textContent || ''
    expect(text).toContain('Coder Two')
    expect(text).toContain('actor-2')
  })

  it('hides agents already mounted to another account from the selector', async () => {
    await act(async () => { renderPanel() })
    await act(async () => { await userEvent.click(q('[data-guide-id="settings/im/mount-a2/agent"]')) })
    // actor-1 is occupied by a1 — excluded from a2's candidates; actor-2 remains.
    expect(document.querySelector('[data-guide-id="settings/im/mount-a2/agent/actor-1"]')).toBeNull()
    expect(document.querySelector('[data-guide-id="settings/im/mount-a2/agent/actor-2"]')).not.toBeNull()
    // Close again so the overlay refcount drains within the test.
    await act(async () => { await userEvent.click(q('[data-guide-id="settings/im/mount-a2/agent"]')) })
    expect(pops).toBe(1)
  })

  it('unmounting releases the agent so it becomes selectable again', async () => {
    await act(async () => { renderPanel() })
    await act(async () => { q('[data-guide-id="settings/im/mount-a1/unmount"]').click() })
    expect(vi.mocked(imRoute._delete).mock.calls[0]?.[1]).toEqual({ Id: 'r1' })
    // After reload a1 shows as unmounted…
    const text = container.textContent || ''
    expect(text).toContain('Acc One')
    // …and actor-1 is back in a2's selector candidates.
    await act(async () => { await userEvent.click(q('[data-guide-id="settings/im/mount-a2/agent"]')) })
    expect(document.querySelector('[data-guide-id="settings/im/mount-a2/agent/actor-1"]')).not.toBeNull()
    expect(document.querySelector('[data-guide-id="settings/im/mount-a2/agent/actor-2"]')).not.toBeNull()
  })
})
