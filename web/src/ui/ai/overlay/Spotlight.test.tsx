import React, { act } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { Spotlight } from './Spotlight'

vi.mock('../browserOverlay', () => ({
  useBrowserOverlay: vi.fn(),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => {
    root.unmount()
  })
  container.remove()
  vi.clearAllMocks()
})

async function render(node: React.ReactNode) {
  await act(async () => {
    root.render(node)
  })
}

describe('Spotlight', () => {
  it('renders nothing when closed', async () => {
    await render(<Spotlight open={false} target={{ x: 0, y: 0, width: 100, height: 50 }} />)
    expect(document.querySelector('.spotlight-ring')).toBeNull()
  })

  it('renders a non-blocking highlight ring around the target', async () => {
    await render(
      <Spotlight
        open
        target={{ x: 100, y: 200, width: 80, height: 40 }}
        holePadding={8}
        holeRadius={12}
      />,
    )

    const ring = document.querySelector('.spotlight-ring') as HTMLDivElement
    expect(ring).not.toBeNull()
    // Geometry: target + padding on every side.
    expect(ring.style.left).toBe('92px') // 100 - 8
    expect(ring.style.top).toBe('192px') // 200 - 8
    expect(ring.style.width).toBe('96px') // 80 + 8 * 2
    expect(ring.style.height).toBe('56px') // 40 + 8 * 2
    expect(ring.style.borderRadius).toBe('12px')
  })

  it('never intercepts pointer events — the guide must not block UI interaction', async () => {
    await render(<Spotlight open target={{ x: 10, y: 20, width: 30, height: 40 }} />)

    const ring = document.querySelector('.spotlight-ring') as HTMLDivElement
    expect(ring).not.toBeNull()
    // The ring itself is paint-only.
    expect(ring.style.pointerEvents).toBe('none')
    // No full-screen backdrop or hit rects exist — nothing else is rendered.
    expect(document.querySelector('.spotlight-root')).toBeNull()
    expect(document.querySelector('.spotlight-backdrop')).toBeNull()
    expect(document.querySelectorAll('.spotlight-hit')).toHaveLength(0)
    expect(ring.getAttribute('aria-hidden')).toBe('true')
  })

  it('clamps negative padding so the ring never inverts', async () => {
    await render(
      <Spotlight open target={{ x: 5, y: 5, width: 10, height: 10 }} holePadding={-20} />,
    )
    const ring = document.querySelector('.spotlight-ring') as HTMLDivElement
    // pad clamped to 0 → ring == target.
    expect(ring.style.left).toBe('5px')
    expect(ring.style.width).toBe('10px')
  })

  it('registers with the browser overlay manager when open', async () => {
    const { useBrowserOverlay } = await import('../browserOverlay')

    await render(<Spotlight open={false} target={{ x: 0, y: 0, width: 10, height: 10 }} />)
    expect(useBrowserOverlay).toHaveBeenLastCalledWith(false)

    await render(<Spotlight open target={{ x: 0, y: 0, width: 10, height: 10 }} />)
    expect(useBrowserOverlay).toHaveBeenLastCalledWith(true)
  })
})
