import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ShellCloudAccountSettings } from './ShellCloudAccountSettings'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  status: vi.fn(),
  getEntitlements: vi.fn(),
  unlink: vi.fn(),
  redeem: vi.fn(),
  sync: vi.fn(),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../application/runtime', () => ({
  isWails: () => true,
}))

vi.mock('../../../gen-clients/cloudaccount/client', () => ({
  status: hoisted.status,
  getEntitlements: hoisted.getEntitlements,
  unlink: hoisted.unlink,
  redeem: hoisted.redeem,
  sync: hoisted.sync,
}))

vi.mock('./CloudLoginOverlay', () => ({
  CloudLoginOverlay: () => null,
}))

function findBtn(container: HTMLElement, text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(
    (b) => b.textContent?.includes(text),
  ) as HTMLButtonElement | undefined
}

function findInputByPlaceholder(container: HTMLElement, placeholder: string): HTMLInputElement | undefined {
  return Array.from(container.querySelectorAll('input')).find(
    (i) => i.placeholder === placeholder,
  ) as HTMLInputElement | undefined
}

function setInputValue(container: HTMLElement, placeholder: string, value: string) {
  const input = findInputByPlaceholder(container, placeholder)!
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  act(() => {
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

async function renderPanel() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<ShellCloudAccountSettings />)
  })
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0))
  })
  return { container, root }
}

describe('ShellCloudAccountSettings', () => {
  let roots: Root[] = []
  let container: HTMLDivElement | undefined

  beforeEach(() => {
    roots = []
    container = undefined
    hoisted.status.mockReset()
    hoisted.getEntitlements.mockReset()
    hoisted.unlink.mockReset()
    hoisted.redeem.mockReset()
    hoisted.sync.mockReset()
    hoisted.status.mockResolvedValue({ Linked: true, Tier: 'free', DisplayName: 'Test User' })
    hoisted.getEntitlements.mockResolvedValue({ Entitlements: { Tier: 'free' }, Degraded: false })
  })

  afterEach(async () => {
    for (const root of roots) {
      await act(async () => {
        root.unmount()
      })
    }
    container?.remove()
  })

  it('renders the redeem input and hides removed credits/store/account-center entries when linked', async () => {
    const res = await renderPanel()
    roots.push(res.root)
    container = res.container

    expect(findInputByPlaceholder(res.container, 'settings.cloudAccount.redeem.placeholder')).toBeTruthy()
    expect(res.container.textContent).not.toContain('settings.cloudAccount.credits')
    expect(res.container.textContent).not.toContain('settings.cloudAccount.store')
    expect(res.container.textContent).not.toContain('settings.cloudAccount.accountCenter')
    expect(res.container.querySelector('a.cloud-account-link')).toBeNull()
  })

  it('submits the code via cloudaccount.redeem and refreshes', async () => {
    hoisted.redeem.mockResolvedValue({
      ProductName: 'Insider 30 天',
      PeriodDays: 30,
      StartsAt: '2026-09-01T00:00:00Z',
      EndsAt: '2026-10-01T00:00:00Z',
    })
    const res = await renderPanel()
    roots.push(res.root)
    container = res.container

    const submit = findBtn(res.container, 'settings.cloudAccount.redeem.submit')!
    expect(submit.disabled).toBe(true)

    setInputValue(res.container, 'settings.cloudAccount.redeem.placeholder', 'SPX-AAAAA-BBBBB-CCCCC')
    await act(async () => {
      submit.click()
    })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })

    expect(hoisted.redeem).toHaveBeenCalledWith({}, { Code: 'SPX-AAAAA-BBBBB-CCCCC' })
    // status + getEntitlements are each fetched again by refresh() after redeem.
    expect(hoisted.status.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(res.container.textContent).toContain('settings.cloudAccount.redeem.success')
  })

  it('shows the cloud error message when redeem fails and does not clear the input', async () => {
    hoisted.redeem.mockRejectedValue(new Error('cdkey_not_found'))
    const res = await renderPanel()
    roots.push(res.root)
    container = res.container

    setInputValue(res.container, 'settings.cloudAccount.redeem.placeholder', 'SPX-BAD')
    const submit = findBtn(res.container, 'settings.cloudAccount.redeem.submit')!
    await act(async () => {
      submit.click()
    })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })

    expect(res.container.textContent).toContain('cdkey_not_found')
    const input = findInputByPlaceholder(res.container, 'settings.cloudAccount.redeem.placeholder')!
    expect(input.value).toBe('SPX-BAD')
  })

  it('does not render the redeem box when no account is linked', async () => {
    hoisted.status.mockResolvedValue({ Linked: false, Tier: 'free' })
    const res = await renderPanel()
    roots.push(res.root)
    container = res.container
    expect(res.container.textContent).not.toContain('settings.cloudAccount.redeem.title')
  })

  it('shows the degraded badge with a resync action and clears it after a successful sync', async () => {
    // First read: linked but degraded (stale entitlements cache).
    hoisted.status.mockResolvedValue({ Linked: true, Tier: 'free', EntitlementsDegraded: true, DisplayName: 'Test User' })
    hoisted.sync.mockResolvedValue({ Linked: true, Tier: 'pro', EntitlementsDegraded: false, DisplayName: 'Test User' })
    hoisted.getEntitlements.mockResolvedValue({ Entitlements: { Tier: 'pro' }, Degraded: false })
    const res = await renderPanel()
    roots.push(res.root)
    container = res.container

    expect(res.container.textContent).toContain('settings.cloudAccount.degraded')

    const resync = res.container.querySelector('.cloud-account-badge-resync') as HTMLButtonElement
    expect(resync).toBeTruthy()
    await act(async () => {
      resync.click()
    })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })

    expect(hoisted.sync).toHaveBeenCalled()
    expect(res.container.textContent).not.toContain('settings.cloudAccount.degraded')
  })

  it('keeps the degraded badge when the manual sync fails to recover', async () => {
    hoisted.status.mockResolvedValue({ Linked: true, Tier: 'free', EntitlementsDegraded: true, DisplayName: 'Test User' })
    hoisted.sync.mockResolvedValue({ Linked: true, Tier: 'free', EntitlementsDegraded: true, DisplayName: 'Test User' })
    hoisted.getEntitlements.mockResolvedValue({ Entitlements: { Tier: 'free' }, Degraded: true })
    const res = await renderPanel()
    roots.push(res.root)
    container = res.container

    const resync = res.container.querySelector('.cloud-account-badge-resync') as HTMLButtonElement
    await act(async () => {
      resync.click()
    })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })

    expect(res.container.textContent).toContain('settings.cloudAccount.degraded')
  })

  it('does not show a resync control when entitlements are fresh', async () => {
    hoisted.status.mockResolvedValue({ Linked: true, Tier: 'pro', EntitlementsDegraded: false, DisplayName: 'Test User' })
    const res = await renderPanel()
    roots.push(res.root)
    container = res.container
    expect(res.container.querySelector('.cloud-account-badge-resync')).toBeNull()
  })
})