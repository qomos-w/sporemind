import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useRef } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { useDropdownVerticalFit } from './useDropdownVerticalFit'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

function TestHarness({ open, direction }: { open: boolean; direction: 'up' | 'down' }) {
  const ref = useRef<HTMLDivElement>(null)
  useDropdownVerticalFit(open, ref, direction)
  return <div ref={ref} data-testid="dropdown" style={{ position: 'absolute', top: 0, left: 0, height: 400 }} />
}

describe('useDropdownVerticalFit', () => {
  let container: HTMLDivElement
  let root: Root
  let originalInnerHeight: number

  beforeEach(() => {
    originalInnerHeight = window.innerHeight
    listeners.resize.clear()
    listeners.scroll.clear()
    Object.defineProperty(window, 'visualViewport', { value: visualViewport, writable: true, configurable: true })
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(() => {
    Object.defineProperty(window, 'innerHeight', { value: originalInnerHeight, writable: true })
    Object.defineProperty(window, 'visualViewport', { value: null, writable: true, configurable: true })
    act(() => root?.unmount())
    container.remove()
    vi.restoreAllMocks()
  })

  const listeners: Record<'resize' | 'scroll', Set<EventListenerOrEventListenerObject>> = { resize: new Set(), scroll: new Set() }
  const visualViewport = {
    height: 800,
    width: 320,
    offsetLeft: 0,
    offsetTop: 0,
    pageLeft: 0,
    pageTop: 0,
    scale: 1,
    addEventListener: (type: 'resize' | 'scroll', handler: EventListenerOrEventListenerObject) => {
      listeners[type].add(handler)
    },
    removeEventListener: (type: 'resize' | 'scroll', handler: EventListenerOrEventListenerObject) => {
      listeners[type].delete(handler)
    },
    dispatchEvent: (event: Event) => {
      const key = event.type as 'resize' | 'scroll'
      listeners[key].forEach((handler) => {
        if (typeof handler === 'function') handler(event)
        else handler.handleEvent?.(event)
      })
      return true
    },
  }

  function setViewport(height: number) {
    Object.defineProperty(window, 'innerHeight', { value: height, writable: true })
    visualViewport.height = height
  }

  const render = async (props: { open: boolean; direction: 'up' | 'down' }) => {
    await act(async () => {
      root = createRoot(container)
      root.render(<TestHarness {...props} />)
    })
  }

  it('does nothing when dropdown fits downward', async () => {
    setViewport(800)
    await render({ open: true, direction: 'down' })
    const el = container.querySelector('[data-testid="dropdown"]') as HTMLDivElement
    vi.spyOn(el, 'getBoundingClientRect').mockReturnValue({
      top: 0, left: 0, right: 200, bottom: 400, width: 200, height: 400, x: 0, y: 0,
    } as DOMRect)

    expect(el.classList.contains('fit-up')).toBe(false)
    expect(el.style.maxHeight).toBe('')
  })

  it('flips upward when downward overflows and upward has more room', async () => {
    setViewport(500)
    await render({ open: true, direction: 'down' })
    const el = container.querySelector('[data-testid="dropdown"]') as HTMLDivElement
    vi.spyOn(el, 'getBoundingClientRect').mockImplementation(() => {
      const flipped = el.classList.contains('fit-up')
      return {
        top: flipped ? -100 : 300,
        left: 0,
        right: 200,
        bottom: flipped ? 300 : 700,
        width: 200,
        height: 400,
        x: 0,
        y: flipped ? -100 : 300,
      } as DOMRect
    })

    await act(async () => {
      window.dispatchEvent(new Event('resize'))
    })

    expect(el.classList.contains('fit-up')).toBe(true)
    expect(parseInt(el.style.maxHeight, 10)).toBeGreaterThan(0)
  })

  it('clamps max-height when upward overflows', async () => {
    setViewport(500)
    await render({ open: true, direction: 'up' })
    const el = container.querySelector('[data-testid="dropdown"]') as HTMLDivElement
    vi.spyOn(el, 'getBoundingClientRect').mockReturnValue({
      top: -100, left: 0, right: 200, bottom: 300, width: 200, height: 400, x: 0, y: -100,
    } as DOMRect)

    await act(async () => {
      window.dispatchEvent(new Event('resize'))
    })

    expect(el.style.maxHeight).not.toBe('')
    expect(parseInt(el.style.maxHeight, 10)).toBeGreaterThan(0)
  })

  it('reacts to visualViewport resize', async () => {
    setViewport(800)
    await render({ open: true, direction: 'down' })
    const el = container.querySelector('[data-testid="dropdown"]') as HTMLDivElement
    vi.spyOn(el, 'getBoundingClientRect').mockImplementation(() => {
      const flipped = el.classList.contains('fit-up')
      return {
        top: flipped ? -100 : 300,
        left: 0,
        right: 200,
        bottom: flipped ? 300 : 700,
        width: 200,
        height: 400,
        x: 0,
        y: flipped ? -100 : 300,
      } as DOMRect
    })

    await act(async () => {
      window.dispatchEvent(new Event('resize'))
    })
    expect(el.classList.contains('fit-up')).toBe(false)

    setViewport(500)
    await act(async () => {
      window.visualViewport!.dispatchEvent(new Event('resize'))
    })

    expect(el.classList.contains('fit-up')).toBe(true)
  })
})
