import React, { act } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { AnchoredOverlay } from './AnchoredOverlay'

vi.mock('./anchor', () => ({
  resolveAnchor: vi.fn(),
  useAnchorRect: vi.fn(),
}))

vi.mock('../browserOverlay', () => ({
  useBrowserOverlay: vi.fn(),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root
const originalGetBoundingClientRect = Element.prototype.getBoundingClientRect

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)

  // happy-dom does not lay out inline width/height, so derive popup size from
  // the rendered style so AnchoredOverlay can measure its portal content.
  Element.prototype.getBoundingClientRect = function (this: Element) {
    const el = this as HTMLElement
    const width = parseFloat(el.style.width) || 0
    const height = parseFloat(el.style.height) || 0
    return new DOMRect(0, 0, width, height)
  }
})

afterEach(() => {
  act(() => {
    root.unmount()
  })
  container.remove()
  vi.clearAllMocks()
  Element.prototype.getBoundingClientRect = originalGetBoundingClientRect
})

async function render(node: React.ReactNode) {
  await act(async () => {
    root.render(node)
  })
}

describe('AnchoredOverlay', () => {
  it('renders nothing when closed', async () => {
    const { resolveAnchor } = await import('./anchor')
    ;(resolveAnchor as ReturnType<typeof vi.fn>).mockResolvedValue(document.createElement('div'))

    await render(
      <AnchoredOverlay open={false} anchor="guide:composer.send" side="bottom">
        <span>tip</span>
      </AnchoredOverlay>
    )

    expect(document.body.querySelector('.anchored-overlay')).toBeNull()
  })

  it('positions the popup below the anchor using placement math', async () => {
    const { resolveAnchor, useAnchorRect } = await import('./anchor')
    const anchorEl = document.createElement('div')
    document.body.appendChild(anchorEl)
    ;(resolveAnchor as ReturnType<typeof vi.fn>).mockResolvedValue(anchorEl)
    ;(useAnchorRect as ReturnType<typeof vi.fn>).mockReturnValue(
      new DOMRect(100, 100, 50, 30)
    )

    await render(
      <AnchoredOverlay
        open
        anchor="guide:composer.send"
        side="bottom"
        align="start"
        offset={8}
        viewportPadding={0}
      >
        <div data-testid="popup" style={{ width: '120px', height: '60px' }}>
          tip
        </div>
      </AnchoredOverlay>
    )

    const popup = document.body.querySelector('.anchored-overlay') as HTMLElement
    expect(popup).not.toBeNull()
    // anchor.x + align start = 100, anchor.y + height + offset = 100 + 30 + 8
    expect(popup.style.left).toBe('100px')
    expect(popup.style.top).toBe('138px')
  })

  it('flips placement when the preferred side does not fit', async () => {
    const { resolveAnchor, useAnchorRect } = await import('./anchor')
    const anchorEl = document.createElement('div')
    document.body.appendChild(anchorEl)
    ;(resolveAnchor as ReturnType<typeof vi.fn>).mockResolvedValue(anchorEl)
    // Anchor very close to top edge.
    ;(useAnchorRect as ReturnType<typeof vi.fn>).mockReturnValue(
      new DOMRect(100, 10, 50, 30)
    )

    await render(
      <AnchoredOverlay
        open
        anchor="guide:near-top"
        side="top"
        align="start"
        offset={8}
        viewportPadding={20}
      >
        <div style={{ width: '80px', height: '50px' }}>tip</div>
      </AnchoredOverlay>
    )

    const popup = document.body.querySelector('.anchored-overlay') as HTMLElement
    expect(popup).not.toBeNull()
    // Should flip to bottom: y = 10 + 30 + 8 = 48
    expect(popup.style.top).toBe('48px')
  })

  it('calls onAnchorMissing when anchor resolution returns null', async () => {
    const { resolveAnchor } = await import('./anchor')
    ;(resolveAnchor as ReturnType<typeof vi.fn>).mockResolvedValue(null)

    const onAnchorMissing = vi.fn()
    await render(
      <AnchoredOverlay
        open
        anchor="guide:missing"
        side="bottom"
        onAnchorMissing={onAnchorMissing}
      >
        tip
      </AnchoredOverlay>
    )

    await act(async () => {})
    expect(onAnchorMissing).toHaveBeenCalled()
  })

  it('registers with the browser overlay manager based on open state', async () => {
    const { resolveAnchor, useAnchorRect } = await import('./anchor')
    ;(resolveAnchor as ReturnType<typeof vi.fn>).mockResolvedValue(document.createElement('div'))
    ;(useAnchorRect as ReturnType<typeof vi.fn>).mockReturnValue(new DOMRect())
    const { useBrowserOverlay } = await import('../browserOverlay')

    await render(
      <AnchoredOverlay open={false} anchor="guide:x" side="bottom">
        tip
      </AnchoredOverlay>
    )
    expect(useBrowserOverlay).toHaveBeenLastCalledWith(false)

    await render(
      <AnchoredOverlay open anchor="guide:x" side="bottom">
        tip
      </AnchoredOverlay>
    )
    expect(useBrowserOverlay).toHaveBeenLastCalledWith(true)
  })
})
