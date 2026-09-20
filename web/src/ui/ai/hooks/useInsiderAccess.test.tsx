import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { useInsiderAccess, refreshInsiderAccess } from './useInsiderAccess'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  status: vi.fn(),
  getEntitlements: vi.fn(),
}))

vi.mock('../../../application/generated-client', () => ({ client: {} }))

vi.mock('../../../gen-clients/cloudaccount/client', () => ({
  status: (...args: unknown[]) => hoisted.status(...(args as [])),
  getEntitlements: (...args: unknown[]) => hoisted.getEntitlements(...(args as [])),
}))

const linkedStatus = (tier: string) => ({ Linked: true, Tier: tier })

function Probe({ onChange }: { onChange: (v: boolean) => void }) {
  const v = useInsiderAccess()
  onChange(v)
  return null
}

describe('useInsiderAccess', () => {
  let container: HTMLDivElement
  let root: Root
  let latest: boolean | null

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    latest = null
    vi.clearAllMocks()
    hoisted.getEntitlements.mockResolvedValue({ Entitlements: {} })
  })

  const renderProbe = async () => {
    await act(async () => {
      root.render(<Probe onChange={(v) => { latest = v }} />)
    })
  }

  it('resolves false for a free account without a pass', async () => {
    hoisted.status.mockResolvedValue(linkedStatus('free'))
    await renderProbe()
    expect(latest).toBe(false)
  })

  it('resolves true for an active insider pass', async () => {
    hoisted.status.mockResolvedValue(linkedStatus('pro'))
    hoisted.getEntitlements.mockResolvedValue({ Entitlements: { Pass: { Type: 'experimental', EndsAt: '2099-01-01T00:00:00Z' } } })
    await renderProbe()
    expect(latest).toBe(true)
  })

  it('resolves true for ultimate even when entitlements fail', async () => {
    hoisted.status.mockResolvedValue(linkedStatus('ultimate'))
    hoisted.getEntitlements.mockRejectedValue(new Error('degraded'))
    await renderProbe()
    expect(latest).toBe(true)
  })

  it('fails closed when the status fetch rejects', async () => {
    hoisted.status.mockRejectedValue(new Error('offline'))
    await renderProbe()
    expect(latest).toBe(false)
  })

  it('refreshInsiderAccess updates subscribers when the tier changes', async () => {
    hoisted.status.mockResolvedValue(linkedStatus('free'))
    await renderProbe()
    expect(latest).toBe(false)

    hoisted.status.mockResolvedValue(linkedStatus('ultimate'))
    await act(async () => { await refreshInsiderAccess() })
    expect(latest).toBe(true)

    hoisted.status.mockResolvedValue(linkedStatus('free'))
    await act(async () => { await refreshInsiderAccess() })
    expect(latest).toBe(false)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })
})
