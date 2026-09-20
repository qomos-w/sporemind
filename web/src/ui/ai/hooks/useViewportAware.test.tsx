import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act, cleanup } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { createRef } from 'react'
import { ViewportObserverProvider, clearViewportHeightCache } from './useViewportAware'
import { ViewportSlot } from '../components/ViewportSlot'

class IntersectionObserverStub {
  static instances: IntersectionObserverStub[] = []
  callback: IntersectionObserverCallback
  options: IntersectionObserverInit | undefined
  targets = new Set<Element>()

  constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    this.callback = callback
    this.options = options
    IntersectionObserverStub.instances.push(this)
  }

  observe(target: Element) { this.targets.add(target) }
  unobserve(target: Element) { this.targets.delete(target) }
  disconnect() { this.targets.clear() }

  fire(target: Element, isIntersecting: boolean) {
    this.callback([{ target, isIntersecting } as IntersectionObserverEntry], this as unknown as IntersectionObserver)
  }
}

class ResizeObserverStub {
  static instances: ResizeObserverStub[] = []
  callback: ResizeObserverCallback
  targets = new Set<Element>()

  constructor(callback: ResizeObserverCallback) {
    this.callback = callback
    ResizeObserverStub.instances.push(this)
  }

  observe(target: Element) { this.targets.add(target) }
  unobserve(target: Element) { this.targets.delete(target) }
  disconnect() { this.targets.clear() }

  fire(target: Element) {
    this.callback([{ target } as ResizeObserverEntry], this as unknown as ResizeObserver)
  }
}

function enterObserver(): IntersectionObserverStub | undefined {
  return IntersectionObserverStub.instances.filter(o => o.options?.rootMargin === '1000px 0px').pop()
}
function exitObserver(): IntersectionObserverStub | undefined {
  return IntersectionObserverStub.instances.filter(o => o.options?.rootMargin === '1200px 0px').pop()
}
function roFor(el: Element): ResizeObserverStub | undefined {
  return ResizeObserverStub.instances.find(o => o.targets.has(el))
}

function mount(scopeKey: string | undefined, id = 'env-1') {
  const scrollRef = createRef<HTMLDivElement>()
  render(
    <>
      <div ref={scrollRef} />
      <ViewportObserverProvider scrollContainerRef={scrollRef} scopeKey={scopeKey}>
        <ViewportSlot id={id}>
          <span data-testid="real">real</span>
        </ViewportSlot>
      </ViewportObserverProvider>
    </>,
  )
  return document.querySelector(`[data-vp-id="${id}"]`) as HTMLElement
}

function placeholderOf(el: HTMLElement): HTMLElement | null {
  return el.querySelector('div[style]') as HTMLElement | null
}

function hideViaExit(el: HTMLElement) {
  act(() => { exitObserver()!.fire(el, false) })
}

beforeEach(() => {
  IntersectionObserverStub.instances = []
  ResizeObserverStub.instances = []
  ;(globalThis as unknown as { IntersectionObserver: unknown }).IntersectionObserver = IntersectionObserverStub
  ;(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub
  clearViewportHeightCache()
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('useViewportSlot hysteresis', () => {
  it('creates enter (1000px) and exit (1200px) observers observing the slot element', () => {
    const el = mount('agent-a')
    expect(enterObserver()).toBeDefined()
    expect(exitObserver()).toBeDefined()
    expect(enterObserver()!.targets.has(el)).toBe(true)
    expect(exitObserver()!.targets.has(el)).toBe(true)
  })

  it('hides only via the exit observer: enter-non-intersecting does not unmount', () => {
    const el = mount('agent-a')
    // Element leaves the ENTER band but is still inside the EXIT band: the
    // enter observer fires isIntersecting=false — must stay mounted.
    act(() => { enterObserver()!.fire(el, false) })
    expect(screen.getByTestId('real')).toBeInTheDocument()
    // Only crossing beyond the EXIT margin (exit observer non-intersecting)
    // swaps to the placeholder.
    hideViaExit(el)
    expect(screen.queryByTestId('real')).not.toBeInTheDocument()
    expect(placeholderOf(el)).not.toBeNull()
  })

  it('shows only via the enter observer: exit-intersecting does not mount', () => {
    const el = mount('agent-a')
    hideViaExit(el)
    expect(screen.queryByTestId('real')).not.toBeInTheDocument()
    // Still outside the ENTER band (exit observer sees it again): no mount.
    act(() => { exitObserver()!.fire(el, true) })
    expect(screen.queryByTestId('real')).not.toBeInTheDocument()
    act(() => { enterObserver()!.fire(el, true) })
    expect(screen.getByTestId('real')).toBeInTheDocument()
  })

  it('renders the cached height as the placeholder', () => {
    const el = mount('agent-a')
    Object.defineProperty(el, 'offsetHeight', { configurable: true, value: 4242 })
    act(() => { roFor(el)!.fire(el) })
    hideViaExit(el)
    expect(placeholderOf(el)!.style.height).toBe('4242px')
  })
})

describe('useViewportSlot height cache scoping', () => {
  it('does not leak a measured height across scopes with the same slot id', () => {
    const el = mount('agent-a')
    Object.defineProperty(el, 'offsetHeight', { configurable: true, value: 4242 })
    act(() => { roFor(el)!.fire(el) })
    hideViaExit(el)
    expect(placeholderOf(el)!.style.height).toBe('4242px')
    cleanup()

    // Same envelope id in a different agent's timeline: no stale height —
    // the placeholder falls back to the default.
    const elB = mount('agent-b')
    hideViaExit(elB)
    expect(placeholderOf(elB)!.style.height).toBe('200px')
  })

  it('reuses the height within the same scope after remount', () => {
    const el = mount('agent-a')
    Object.defineProperty(el, 'offsetHeight', { configurable: true, value: 4242 })
    act(() => { roFor(el)!.fire(el) })
    cleanup()

    const el2 = mount('agent-a')
    hideViaExit(el2)
    expect(placeholderOf(el2)!.style.height).toBe('4242px')
  })
})
