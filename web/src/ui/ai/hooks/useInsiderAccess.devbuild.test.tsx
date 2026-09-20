import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { useInsiderAccess } from './useInsiderAccess'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../config/buildConfig', () => ({ buildType: 'dev' }))

const hoisted = vi.hoisted(() => ({
  status: vi.fn(),
  getEntitlements: vi.fn(),
}))

vi.mock('../../../application/generated-client', () => ({ client: {} }))

vi.mock('../../../gen-clients/cloudaccount/client', () => ({
  status: (...args: unknown[]) => hoisted.status(...(args as [])),
  getEntitlements: (...args: unknown[]) => hoisted.getEntitlements(...(args as [])),
}))

function Probe({ onChange }: { onChange: (v: boolean) => void }) {
  const v = useInsiderAccess()
  onChange(v)
  return null
}

describe('useInsiderAccess (dev build)', () => {
  let container: HTMLDivElement
  let root: Root
  let latest: boolean | null

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    latest = null
    vi.clearAllMocks()
  })

  const renderProbe = async () => {
    await act(async () => {
      root.render(<Probe onChange={(v) => { latest = v }} />)
    })
  }

  it('opens the gate for a free account in a dev build', async () => {
    hoisted.status.mockResolvedValue({ Linked: true, Tier: 'free' })
    await renderProbe()
    expect(latest).toBe(true)
  })

  it('opens the gate without hitting the account endpoint at all', async () => {
    hoisted.status.mockRejectedValue(new Error('offline'))
    await renderProbe()
    expect(latest).toBe(true)
    expect(hoisted.status).not.toHaveBeenCalled()
    expect(hoisted.getEntitlements).not.toHaveBeenCalled()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })
})
