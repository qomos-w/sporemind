import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { resolveAnchor, useAnchorRect } from './anchor'

describe('resolveAnchor', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('resolves guide:<id>', async () => {
    const el = document.createElement('button')
    el.dataset.guideId = 'composer.send'
    document.body.appendChild(el)

    const found = await resolveAnchor('guide:composer.send')
    expect(found).toBe(el)
  })

  it('resolves card:<id>', async () => {
    const el = document.createElement('article')
    el.dataset.cardId = 'refactor-plan'
    document.body.appendChild(el)

    const found = await resolveAnchor('card:refactor-plan')
    expect(found).toBe(el)
  })

  it('resolves slot:<id>', async () => {
    const el = document.createElement('div')
    el.dataset.slotId = 'turn-1-goal-review'
    document.body.appendChild(el)

    const found = await resolveAnchor('slot:turn-1-goal-review')
    expect(found).toBe(el)
  })

  it('resolves envelope:<id>', async () => {
    const el = document.createElement('div')
    el.dataset.envelopeId = 'env-1'
    document.body.appendChild(el)

    const found = await resolveAnchor('envelope:env-1')
    expect(found).toBe(el)
  })

  it('falls back to a CSS selector after validation', async () => {
    const el = document.createElement('div')
    el.id = 'fallback-target'
    document.body.appendChild(el)

    const found = await resolveAnchor('#fallback-target')
    expect(found).toBe(el)
  })

  it('rejects unsafe selectors (leading combinator)', async () => {
    document.body.innerHTML = '<div id="anchor"></div>'
    expect(await resolveAnchor('> #anchor')).toBeNull()
    expect(await resolveAnchor('+ #anchor')).toBeNull()
    expect(await resolveAnchor('~ #anchor')).toBeNull()
  })

  it('escapes attribute values to avoid selector injection', async () => {
    const el = document.createElement('div')
    // The literal id contains quotes and a backslash, which should still match.
    el.dataset.cardId = 'a"b\\c'
    document.body.appendChild(el)

    const found = await resolveAnchor('card:a"b\\c')
    expect(found).toBe(el)
  })

  it('waits for an element to mount and resolves when it appears', async () => {
    const promise = resolveAnchor('card:lazy-card', 200)

    const el = document.createElement('article')
    el.dataset.cardId = 'lazy-card'
    document.body.appendChild(el)

    const found = await promise
    expect(found).toBe(el)
  })

  it('times out and returns null when the element never mounts', async () => {
    const start = performance.now()
    const found = await resolveAnchor('card:missing-card', 50)
    const elapsed = performance.now() - start

    expect(found).toBeNull()
    expect(elapsed).toBeGreaterThanOrEqual(49)
  })

  it('scrolls a virtualized placeholder into view then resolves the real card', async () => {
    const placeholder = document.createElement('div')
    placeholder.dataset.vpId = 'vp-card'
    placeholder.style.height = '200px'
    document.body.appendChild(placeholder)

    const scrollSpy = vi.spyOn(placeholder, 'scrollIntoView')

    const promise = resolveAnchor('card:vp-card', 200)

    // First mutation tick should trigger scrollIntoView on the placeholder.
    await new Promise((r) => setTimeout(r, 10))
    expect(scrollSpy).toHaveBeenCalled()

    const real = document.createElement('article')
    real.dataset.cardId = 'vp-card'
    document.body.appendChild(real)

    const found = await promise
    expect(found).toBe(real)
  })

  it('resolves immediately for a selector that already exists', async () => {
    const el = document.createElement('div')
    el.className = 'existing'
    document.body.appendChild(el)

    const found = await resolveAnchor('.existing')
    expect(found).toBe(el)
  })
})

describe('useAnchorRect', () => {
  type ResizeHandler = (entries: unknown[]) => void

  interface MockObserver {
    handler: ResizeHandler
    observe: ReturnType<typeof vi.fn>
    disconnect: ReturnType<typeof vi.fn>
  }

  let instances: MockObserver[] = []
  let observations: { target: Element; instance: MockObserver }[] = []
  let originalRO: typeof ResizeObserver

  beforeEach(() => {
    instances = []
    observations = []
    originalRO = globalThis.ResizeObserver
    globalThis.ResizeObserver = vi.fn(function (this: ResizeObserver, handler: ResizeHandler) {
      const instance: MockObserver = {
        handler,
        observe: vi.fn((target: Element) => {
          observations.push({ target, instance })
        }),
        disconnect: vi.fn(),
      }
      instances.push(instance)
      return instance
    }) as unknown as typeof ResizeObserver
  })

  afterEach(() => {
    globalThis.ResizeObserver = originalRO
  })

  it('returns an empty rect when no element is provided', () => {
    const { result } = renderHook(() => useAnchorRect(null))

    expect(result.current.left).toBe(0)
    expect(result.current.top).toBe(0)
    expect(result.current.width).toBe(0)
    expect(result.current.height).toBe(0)
  })

  it('reads the initial bounding client rect', async () => {
    const el = document.createElement('div')
    el.getBoundingClientRect = vi.fn(() => new DOMRect(10, 20, 100, 50))
    document.body.appendChild(el)

    const { result } = renderHook(() => useAnchorRect(el))

    await act(async () => {
      await new Promise((r) => requestAnimationFrame(r))
    })

    expect(result.current.left).toBe(10)
    expect(result.current.top).toBe(20)
    expect(result.current.width).toBe(100)
    expect(result.current.height).toBe(50)
  })

  it('observes the element and its ancestors', async () => {
    const parent = document.createElement('section')
    const el = document.createElement('div')
    parent.appendChild(el)
    document.body.appendChild(parent)

    el.getBoundingClientRect = vi.fn(() => new DOMRect(0, 0, 10, 10))

    renderHook(() => useAnchorRect(el))
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })

    const observed = observations.map((o) => o.target)
    expect(observed).toContain(el)
    expect(observed).toContain(parent)
    expect(observed).toContain(document.body)
  })

  it('updates rect on resize observer callbacks', async () => {
    const el = document.createElement('div')
    let rect = new DOMRect(0, 0, 100, 50)
    el.getBoundingClientRect = vi.fn(() => rect)
    document.body.appendChild(el)

    const { result } = renderHook(() => useAnchorRect(el))
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })

    rect = new DOMRect(0, 0, 200, 75)
    const observation = observations.find((o) => o.target === el)
    expect(observation).toBeTruthy()

    await act(async () => {
      observation!.instance.handler([{}])
      await new Promise((r) => requestAnimationFrame(r))
    })

    expect(result.current.width).toBe(200)
    expect(result.current.height).toBe(75)
  })

  it('does not re-render when the rect is unchanged', async () => {
    const el = document.createElement('div')
    const rect = new DOMRect(0, 0, 100, 50)
    el.getBoundingClientRect = vi.fn(() => rect)
    document.body.appendChild(el)

    let renderCount = 0
    const { result } = renderHook(() => {
      renderCount += 1
      return useAnchorRect(el)
    })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })

    const before = renderCount
    const observation = observations.find((o) => o.target === el)!

    await act(async () => {
      observation.instance.handler([{}])
      observation.instance.handler([{}])
      await new Promise((r) => requestAnimationFrame(r))
    })

    expect(renderCount).toBe(before)
    expect(result.current).toEqual(expect.objectContaining({ width: 100, height: 50 }))
  })

  it('disconnects observers when the element changes', async () => {
    const a = document.createElement('div')
    const b = document.createElement('div')
    document.body.appendChild(a)
    document.body.appendChild(b)

    a.getBoundingClientRect = vi.fn(() => new DOMRect(0, 0, 1, 1))
    b.getBoundingClientRect = vi.fn(() => new DOMRect(10, 10, 2, 2))

    const { result, rerender } = renderHook(({ target }: { target: HTMLElement | null }) => useAnchorRect(target), {
      initialProps: { target: a },
    })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })

    const observationA = observations.find((o) => o.target === a)
    expect(observationA).toBeTruthy()
    const disconnectSpy = observationA!.instance.disconnect

    await act(async () => {
      rerender({ target: b })
      await new Promise((r) => setTimeout(r, 0))
    })

    expect(disconnectSpy).toHaveBeenCalled()
    expect(result.current.left).toBe(10)
  })
})
