import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { SidebarAccountMenu } from './AIShellSidebar'
import { I18nProvider } from '../../../i18n/provider'
import type { CloudAccountStatus } from '../../../gen-clients/system/types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// ── Mocks ────────────────────────────────────────────────────────────────────
// SidebarAccountMenu lives in AIShellSidebar.tsx, so importing it pulls the
// whole module tree. Mirror the mock scaffold of AIShellSidebar.test.tsx and
// additionally stub the cloud-account client, the insider-access refresh and
// the CloudLoginOverlay so the reconnect flow is observable.

const hoisted = vi.hoisted(() => ({
  getExpandedProjectIds: vi.fn(async () => new Set<string>()),
  toggleExpandedProjectId: vi.fn(async () => {}),
  getCategoryExpansion: vi.fn(async () => ({})),
  toggleCategoryExpansion: vi.fn(async () => {}),
  wikiListCards: vi.fn(async () => ({ Cards: [] })),
  list: vi.fn(async () => ''),
  monoStore: {
    subscribe: vi.fn(() => () => {}),
    getState: vi.fn(() => ({ projectId: null, cards: [] })),
    setProjectId: vi.fn(),
    load: vi.fn(),
    loadOpenCards: vi.fn(),
  },
  emptyMonoState: { projectId: null, cards: [] },
  persistLocale: vi.fn(async () => {}),
  status: vi.fn(),
  sync: vi.fn(),
  unlink: vi.fn(),
  refreshInsiderAccess: vi.fn(async () => {}),
  overlayOpens: [] as boolean[],
}))

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/project/client', () => ({
  wikiListCards: hoisted.wikiListCards,
  list: hoisted.list,
}))
vi.mock('../../../application/workspace-ui-state', () => ({
  getExpandedProjectIds: hoisted.getExpandedProjectIds,
  toggleExpandedProjectId: hoisted.toggleExpandedProjectId,
  getCategoryExpansion: hoisted.getCategoryExpansion,
  toggleCategoryExpansion: hoisted.toggleCategoryExpansion,
}))
vi.mock('../../../application/locale-persist', () => ({ persistLocale: hoisted.persistLocale }))
vi.mock('../../../gen-clients/cloudaccount/client', () => ({
  status: hoisted.status,
  sync: hoisted.sync,
  unlink: hoisted.unlink,
}))
vi.mock('../hooks/useInsiderAccess', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../hooks/useInsiderAccess')>()
  return { ...actual, refreshInsiderAccess: hoisted.refreshInsiderAccess }
})
vi.mock('../../../application/runtime', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../application/runtime')>()
  return { ...actual, isWails: () => true }
})
vi.mock('./CloudLoginOverlay', () => ({
  CloudLoginOverlay: ({ open }: { open: boolean }) => {
    hoisted.overlayOpens.push(open)
    return null
  },
}))

const buildCfg = vi.hoisted(() => ({
  buildType: 'release' as 'release' | 'beta' | 'dev',
  wailsProduction: false,
}))
vi.mock('../../../config/buildConfig', () => ({
  get buildType() { return buildCfg.buildType },
  get wailsProduction() { return buildCfg.wailsProduction },
  buildFlavor: 'default',
  buildVersion: 'test',
}))
vi.mock('../../panels/mono-store', () => ({ monoStore: hoisted.monoStore }))
vi.mock('../hooks/useMonoStore', () => ({ useMonoStore: () => hoisted.emptyMonoState }))

// ── Helpers ──────────────────────────────────────────────────────────────────

function makeStatus(over: Partial<CloudAccountStatus> = {}): CloudAccountStatus {
  return { Linked: true, DisplayName: 'Test User', ...over } as CloudAccountStatus
}

let container: HTMLDivElement
let root: Root

async function renderMenu() {
  container = document.createElement('div')
  document.body.appendChild(container)
  await act(async () => {
    root = createRoot(container)
    root.render(
      <I18nProvider initialLocale="en-US">
        <SidebarAccountMenu account={null} />
      </I18nProvider>,
    )
  })
  // Flush the mount-time refreshCloud() effect.
  await act(async () => {})
  await act(async () => {})
}

function accountRow(): HTMLElement {
  return container.querySelector('.ai-sidebar-account-button') as HTMLElement
}

function accountButton(): HTMLButtonElement {
  return container.querySelector('.ai-sidebar-account-main') as HTMLButtonElement
}

function reconnectIcon(): HTMLButtonElement | null {
  return container.querySelector<HTMLButtonElement>('.ai-sidebar-account-reconnect')
}

async function openMenu() {
  await act(async () => {
    accountButton().click()
  })
}

function itemByLabel(label: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll<HTMLButtonElement>('.ai-sidebar-menu-item')).find(
    (b) => b.querySelector('.ai-sidebar-menu-label')?.textContent === label,
  )
}

function menuLabels(): string[] {
  return Array.from(container.querySelectorAll('.ai-sidebar-menu-label')).map((e) => e.textContent ?? '')
}

async function clickItem(label: string) {
  await act(async () => {
    itemByLabel(label)!.click()
  })
  await act(async () => {})
}

beforeEach(() => {
  hoisted.status.mockReset()
  hoisted.sync.mockReset()
  hoisted.unlink.mockReset()
  hoisted.refreshInsiderAccess.mockClear()
  hoisted.overlayOpens.length = 0
  hoisted.status.mockResolvedValue(makeStatus({ EntitlementsDegraded: true }))
  hoisted.sync.mockResolvedValue(makeStatus({ EntitlementsDegraded: true }))
  hoisted.unlink.mockResolvedValue({})
})

afterEach(() => {
  act(() => root?.unmount())
  container?.remove()
  vi.restoreAllMocks()
})

// ── Tests ────────────────────────────────────────────────────────────────────

describe('SidebarAccountMenu degraded reconnect actions', () => {
  it('offers an explicit reconnect entry (plus resync and logout) when degraded', async () => {
    await renderMenu()
    await openMenu()

    const labels = menuLabels()
    expect(labels).toContain('Resync')
    expect(labels).toContain('Disconnect and sign in again')
    expect(labels).toContain('Log Out')
    expect(itemByLabel('Disconnect and sign in again')).toBeTruthy()
  })

  it('does not offer the reconnect entry when entitlements are fresh', async () => {
    hoisted.status.mockResolvedValue(makeStatus({ EntitlementsDegraded: false }))
    await renderMenu()
    await openMenu()

    const labels = menuLabels()
    expect(labels).not.toContain('Disconnect and sign in again')
    expect(labels).not.toContain('Resync')
  })

  it('marks the collapsed button degraded and clickable, and opening it reveals the menu', async () => {
    await renderMenu()

    const row = accountRow()
    expect(row.className).toContain('degraded')
    expect(accountButton().getAttribute('aria-label')).toBe('Subscription data stale — open to reconnect')
    expect(container.querySelector('.ai-sidebar-account-reconnect')).toBeTruthy()

    await openMenu()
    expect(container.querySelector('.ai-sidebar-account-popover')).toBeTruthy()
  })

  it('does not mark the collapsed button degraded when entitlements are fresh', async () => {
    hoisted.status.mockResolvedValue(makeStatus({ EntitlementsDegraded: false }))
    await renderMenu()

    expect(accountRow().className).not.toContain('degraded')
    expect(container.querySelector('.ai-sidebar-account-reconnect')).toBeNull()
  })

  it('surfaces an inline failure notice when resync leaves the account degraded', async () => {
    await renderMenu()
    await openMenu()

    expect(container.querySelector('.ai-sidebar-account-cloud-failed')).toBeNull()
    await clickItem('Resync')

    expect(hoisted.sync).toHaveBeenCalled()
    expect(container.querySelector('.ai-sidebar-account-cloud-failed')).toBeTruthy()
    expect(container.textContent).toContain('Sync failed. Check your connection and try again.')
  })

  it('surfaces the inline failure notice when resync throws', async () => {
    hoisted.sync.mockRejectedValue(new Error('network down'))
    await renderMenu()
    await openMenu()

    await clickItem('Resync')

    expect(container.querySelector('.ai-sidebar-account-cloud-failed')).toBeTruthy()
  })

  it('clears the failure notice and the degraded state after a successful resync', async () => {
    hoisted.sync.mockResolvedValue(makeStatus({ EntitlementsDegraded: false, Tier: 'pro' }))
    await renderMenu()
    await openMenu()

    await clickItem('Resync')

    expect(container.querySelector('.ai-sidebar-account-cloud-failed')).toBeNull()
    expect(container.textContent).not.toContain('Subscription data stale')
  })

  it('disconnects and opens the login overlay on reconnect', async () => {
    await renderMenu()
    await openMenu()

    await clickItem('Disconnect and sign in again')

    expect(hoisted.unlink).toHaveBeenCalledWith({})
    // The popover closes and the (mocked) login overlay flips to open.
    expect(container.querySelector('.ai-sidebar-account-popover')).toBeNull()
    expect(hoisted.overlayOpens).toContain(true)
  })

  it('shows the inline reconnect icon next to the chevron when degraded', async () => {
    await renderMenu()

    const icon = reconnectIcon()
    expect(icon).toBeTruthy()
    expect(icon!.getAttribute('aria-label')).toBe('Disconnect and sign in again')
  })

  it('hides the inline reconnect icon when entitlements are fresh', async () => {
    hoisted.status.mockResolvedValue(makeStatus({ EntitlementsDegraded: false }))
    await renderMenu()

    expect(reconnectIcon()).toBeNull()
  })

  it('clicking the inline reconnect icon reconnects without opening the menu', async () => {
    await renderMenu()

    await act(async () => {
      reconnectIcon()!.click()
    })
    await act(async () => {})

    expect(hoisted.unlink).toHaveBeenCalledWith({})
    expect(container.querySelector('.ai-sidebar-account-popover')).toBeNull()
    expect(hoisted.overlayOpens).toContain(true)
  })
})
