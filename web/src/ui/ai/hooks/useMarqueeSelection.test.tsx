import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useRef, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import {
  useMarqueeSelection,
  computeMarqueeRect,
  rectsIntersect,
  classifyMarqueeMode,
  applyMarqueeToSelection,
  type MarqueeSelectionResult,
} from './useMarqueeSelection'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

/* ===================== rAF stub (deterministic frames) ===================== */

let rafQueue: Array<{ cb: FrameRequestCallback; id: number }> = []
let rafNextId = 1

const flushFrame = () => {
  const q = rafQueue
  rafQueue = []
  for (const { cb } of q) cb(performance.now())
}

/* ===================== harness ===================== */

interface HarnessProps {
  onHit: (r: MarqueeSelectionResult) => void
  onPreview?: (r: MarqueeSelectionResult) => void
  getSelectedPaths?: () => string[]
  ignoreSelector?: string
  autoScroll?: boolean
  extra?: ReactNode
}

function TestHarness({ onHit, onPreview, getSelectedPaths, ignoreSelector, autoScroll, extra }: HarnessProps) {
  const ref = useRef<HTMLDivElement>(null)
  useMarqueeSelection({
    containerRef: ref,
    itemSelector: '.mq-item',
    onHit,
    onPreview,
    getSelectedPaths,
    ignoreSelector,
    autoScroll,
  })
  return (
    <div ref={ref} className="mq-container">
      <div className="mq-item" data-mq-path="/a">a</div>
      <div className="mq-item" data-mq-path="/b">b</div>
      <div className="mq-item" data-mq-path="/c">c</div>
      <div className="mq-item" data-mq-path="/d">d</div>
      {extra}
    </div>
  )
}

/* ===================== helpers ===================== */

const domRect = (left: number, top: number, width: number, height: number): DOMRect =>
  ({
    left, top, right: left + width, bottom: top + height, width, height, x: left, y: top,
    toJSON: () => ({}),
  }) as DOMRect

/**
 * Layout: container 400x300 at origin; four 24px rows stacked from the top
 * (a: 0-24, b: 24-48, c: 48-72, d: 72-96). Everything below y=96 is empty
 * area. `vScrollbarWidth` shrinks clientWidth to emulate a scrollbar.
 */
function mockLayout(container: HTMLElement, vScrollbarWidth = 0) {
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue(domRect(0, 0, 400, 300))
  Object.defineProperty(container, 'clientWidth', { value: 400 - vScrollbarWidth, configurable: true })
  Object.defineProperty(container, 'clientHeight', { value: 300, configurable: true })
  Array.from(container.querySelectorAll<HTMLElement>('.mq-item')).forEach((el, i) => {
    vi.spyOn(el, 'getBoundingClientRect').mockReturnValue(domRect(0, i * 24, 400, 24))
  })
}

interface MouseInit2 {
  clientX?: number
  clientY?: number
  button?: number
  ctrlKey?: boolean
  altKey?: boolean
  shiftKey?: boolean
  metaKey?: boolean
}

const mouse = (type: string, target: EventTarget, init: MouseInit2 = {}) => {
  const ev = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    clientX: init.clientX ?? 10,
    clientY: init.clientY ?? 10,
    button: init.button ?? 0,
    ctrlKey: !!init.ctrlKey,
    altKey: !!init.altKey,
    shiftKey: !!init.shiftKey,
    metaKey: !!init.metaKey,
  })
  target.dispatchEvent(ev)
  return ev
}

const move = (x: number, y: number) => mouse('mousemove', document, { clientX: x, clientY: y })
const up = () => mouse('mouseup', document)

/** Drag from empty area (10,200) across rows c and d → hits ['/c','/d']. */
const dragOverCD = (mods: MouseInit2 = {}) => {
  mouse('mousedown', document.querySelector('.mq-container')!, { clientX: 10, clientY: 200, ...mods })
  move(300, 60)
  flushFrame()
}

/* ===================== pure helpers ===================== */

describe('computeMarqueeRect', () => {
  it('normalizes down-right drags', () => {
    expect(computeMarqueeRect(10, 20, 110, 70)).toEqual({ left: 10, top: 20, width: 100, height: 50 })
  })

  it('normalizes up-left drags', () => {
    expect(computeMarqueeRect(110, 70, 10, 20)).toEqual({ left: 10, top: 20, width: 100, height: 50 })
  })

  it('handles zero-size rects', () => {
    expect(computeMarqueeRect(5, 5, 5, 5)).toEqual({ left: 5, top: 5, width: 0, height: 0 })
  })
})

describe('rectsIntersect', () => {
  const a = { left: 0, top: 0, right: 100, bottom: 100 }
  it('returns true for overlap', () => {
    expect(rectsIntersect(a, { left: 50, top: 50, right: 150, bottom: 150 })).toBe(true)
  })
  it('returns true for containment', () => {
    expect(rectsIntersect(a, { left: 10, top: 10, right: 20, bottom: 20 })).toBe(true)
  })
  it('returns false for edge-touching boxes', () => {
    expect(rectsIntersect(a, { left: 100, top: 0, right: 200, bottom: 100 })).toBe(false)
    expect(rectsIntersect(a, { left: 0, top: 100, right: 100, bottom: 200 })).toBe(false)
  })
  it('returns false for disjoint boxes', () => {
    expect(rectsIntersect(a, { left: 200, top: 200, right: 300, bottom: 300 })).toBe(false)
  })
})

describe('classifyMarqueeMode', () => {
  const mods = (o: Partial<MarqueeSelectionResult['modifiers']> = {}) =>
    ({ ctrl: false, meta: false, shift: false, alt: false, ...o })

  it('maps modifiers with alt > ctrl/meta > shift > none priority', () => {
    expect(classifyMarqueeMode(mods())).toBe('replace')
    expect(classifyMarqueeMode(mods({ ctrl: true }))).toBe('add')
    expect(classifyMarqueeMode(mods({ meta: true }))).toBe('add')
    expect(classifyMarqueeMode(mods({ shift: true }))).toBe('range')
    expect(classifyMarqueeMode(mods({ alt: true }))).toBe('subtract')
    // priority combinations
    expect(classifyMarqueeMode(mods({ ctrl: true, alt: true }))).toBe('subtract')
    expect(classifyMarqueeMode(mods({ shift: true, ctrl: true }))).toBe('add')
    expect(classifyMarqueeMode(mods({ shift: true, alt: true }))).toBe('subtract')
  })
})

describe('applyMarqueeToSelection', () => {
  it('replace returns the hit set (base not required)', () => {
    expect(applyMarqueeToSelection(null, ['/a', '/b'], 'replace')).toEqual(['/a', '/b'])
    expect(applyMarqueeToSelection(['/x'], ['/a'], 'replace')).toEqual(['/a'])
  })

  it('add unions base and hits, base order first, deduplicated', () => {
    expect(applyMarqueeToSelection(['/a', '/b'], ['/b', '/c'], 'add')).toEqual(['/a', '/b', '/c'])
  })

  it('add without a base is unresolvable (null)', () => {
    expect(applyMarqueeToSelection(null, ['/a'], 'add')).toBeNull()
  })

  it('subtract removes hits from base', () => {
    expect(applyMarqueeToSelection(['/a', '/b', '/c'], ['/b'], 'subtract')).toEqual(['/a', '/c'])
    expect(applyMarqueeToSelection(['/a'], ['/a'], 'subtract')).toEqual([])
  })

  it('subtract without a base is unresolvable (null)', () => {
    expect(applyMarqueeToSelection(null, ['/a'], 'subtract')).toBeNull()
  })

  it('range is always left to the consumer (null)', () => {
    expect(applyMarqueeToSelection(['/a'], ['/b'], 'range')).toBeNull()
  })
})

/* ===================== hook ===================== */

describe('useMarqueeSelection', () => {
  let mountDiv: HTMLDivElement
  let root: Root
  let container: HTMLDivElement
  let onHit: ReturnType<typeof vi.fn<(r: MarqueeSelectionResult) => void>>
  let onPreview: ReturnType<typeof vi.fn<(r: MarqueeSelectionResult) => void>>
  let getSelected: ReturnType<typeof vi.fn<() => string[]>>

  const render = async (props: Partial<HarnessProps> = {}) => {
    await act(async () => {
      root = createRoot(mountDiv)
      root.render(
        <TestHarness
          onHit={onHit}
          onPreview={onPreview}
          getSelectedPaths={getSelected}
          autoScroll={false}
          {...props}
        />,
      )
    })
    container = document.querySelector('.mq-container') as HTMLDivElement
    mockLayout(container)
  }

  beforeEach(() => {
    rafQueue = []
    rafNextId = 1
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
      rafQueue.push({ cb, id: rafNextId })
      return rafNextId++
    })
    vi.stubGlobal('cancelAnimationFrame', (id: number) => {
      rafQueue = rafQueue.filter(e => e.id !== id)
    })
    mountDiv = document.createElement('div')
    document.body.appendChild(mountDiv)
    onHit = vi.fn<(r: MarqueeSelectionResult) => void>()
    onPreview = vi.fn<(r: MarqueeSelectionResult) => void>()
    getSelected = vi.fn<() => string[]>(() => [])
  })

  afterEach(() => {
    act(() => root?.unmount())
    mountDiv.remove()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('plain drag over empty area selects hit rows with replace semantics', async () => {
    await render()
    dragOverCD()

    expect(onPreview).toHaveBeenCalledTimes(1)
    expect(onPreview).toHaveBeenCalledWith(expect.objectContaining({ paths: ['/c', '/d'], mode: 'replace' }))

    up()
    expect(onHit).toHaveBeenCalledTimes(1)
    const r = onHit.mock.calls[0]![0] as MarqueeSelectionResult
    expect(r).toMatchObject({ paths: ['/c', '/d'], mode: 'replace', isClick: false })
    expect(r.next).toEqual(['/c', '/d'])
    expect(r.modifiers).toEqual({ ctrl: false, meta: false, shift: false, alt: false })
    // rectangle cleaned up afterwards
    expect(container.querySelector('.mq-marquee-rect')).toBeNull()
    expect(container.classList.contains('mq-selecting')).toBe(false)
  })

  it('ctrl drag unions with the selection snapshot', async () => {
    getSelected = vi.fn((): string[] => ['/a'])
    await render({ getSelectedPaths: getSelected })
    dragOverCD({ ctrlKey: true })
    up()

    expect(onHit).toHaveBeenCalledWith(expect.objectContaining({ mode: 'add', paths: ['/c', '/d'] }))
    expect(onHit.mock.calls[0]![0].next).toEqual(['/a', '/c', '/d'])
  })

  it('alt drag subtracts hits from the selection snapshot', async () => {
    getSelected = vi.fn((): string[] => ['/a', '/c', '/d'])
    await render({ getSelectedPaths: getSelected })
    dragOverCD({ altKey: true })
    up()

    expect(onHit).toHaveBeenCalledWith(expect.objectContaining({ mode: 'subtract', paths: ['/c', '/d'] }))
    expect(onHit.mock.calls[0]![0].next).toEqual(['/a'])
  })

  it('shift drag reports range mode and leaves resolution to the consumer', async () => {
    getSelected = vi.fn((): string[] => ['/a'])
    await render({ getSelectedPaths: getSelected })
    dragOverCD({ shiftKey: true })
    up()

    const r = onHit.mock.calls[0]![0] as MarqueeSelectionResult
    expect(r.mode).toBe('range')
    expect(r.paths).toEqual(['/c', '/d'])
    expect(r.next).toBeNull()
  })

  it('add/subtract without getSelectedPaths yield next=null', async () => {
    await render({ getSelectedPaths: undefined })
    dragOverCD({ ctrlKey: true })
    up()
    expect(onHit.mock.calls[0]![0].next).toBeNull()
  })

  it('positions the rectangle in content coordinates and sets relative positioning', async () => {
    await render()
    mouse('mousedown', container, { clientX: 10, clientY: 200 })
    move(300, 60)
    flushFrame()

    const rect = container.querySelector('.mq-marquee-rect') as HTMLDivElement
    expect(rect).not.toBeNull()
    expect(rect.style.left).toBe('10px')
    expect(rect.style.top).toBe('60px')
    expect(rect.style.width).toBe('290px')
    expect(rect.style.height).toBe('140px')
    expect(container.style.position).toBe('relative')
    expect(container.classList.contains('mq-selecting')).toBe(true)

    up()
    expect(container.querySelector('.mq-marquee-rect')).toBeNull()
  })

  it('preview only fires when the hit set changes', async () => {
    await render()
    mouse('mousedown', container, { clientX: 10, clientY: 200 })
    move(300, 60) // hits c,d
    flushFrame()
    move(300, 61) // same hit set
    flushFrame()
    move(300, 80) // hits d only
    flushFrame()
    up()

    expect(onPreview).toHaveBeenCalledTimes(2)
    expect(onPreview.mock.calls[0]![0].paths).toEqual(['/c', '/d'])
    expect(onPreview.mock.calls[1]![0].paths).toEqual(['/d'])
  })

  it('mousedown on an item row never starts a marquee (drag/click stay with the row)', async () => {
    await render()
    const item = container.querySelector('.mq-item') as HTMLDivElement
    const ev = mouse('mousedown', item, { clientX: 10, clientY: 10 })
    move(300, 60)
    flushFrame()
    up()

    expect(ev.defaultPrevented).toBe(false)
    expect(onHit).not.toHaveBeenCalled()
    expect(onPreview).not.toHaveBeenCalled()
    expect(container.querySelector('.mq-marquee-rect')).toBeNull()
  })

  it('ignores non-left buttons', async () => {
    await render()
    mouse('mousedown', container, { clientX: 10, clientY: 200, button: 2 })
    move(300, 60)
    flushFrame()
    up()
    expect(onHit).not.toHaveBeenCalled()
    expect(container.querySelector('.mq-marquee-rect')).toBeNull()
  })

  it('ignores presses on the scrollbar strip', async () => {
    await render()
    mockLayout(container, 16) // clientWidth 384, scrollbar at x>384
    mouse('mousedown', container, { clientX: 392, clientY: 200 })
    move(300, 60)
    flushFrame()
    up()
    expect(onHit).not.toHaveBeenCalled()
  })

  it('ignores presses on interactive elements and custom ignoreSelector', async () => {
    // A custom ignoreSelector replaces the default list, so include the
    // interactive elements explicitly alongside the region selector.
    await render({ ignoreSelector: '.mq-header, button', extra: <div className="mq-header"><span>hdr</span></div> })
    const header = container.querySelector('.mq-header') as HTMLDivElement
    mouse('mousedown', header, { clientX: 10, clientY: 10 })
    move(300, 60)
    flushFrame()
    up()

    const button = document.createElement('button')
    container.appendChild(button)
    mouse('mousedown', button, { clientX: 10, clientY: 12 })
    move(300, 60)
    flushFrame()
    up()

    expect(onHit).not.toHaveBeenCalled()
    expect(container.querySelector('.mq-marquee-rect')).toBeNull()
  })

  it('plain empty-area click clears the selection (isClick + replace + empty paths)', async () => {
    getSelected = vi.fn((): string[] => ['/a', '/b'])
    await render({ getSelectedPaths: getSelected })
    mouse('mousedown', container, { clientX: 10, clientY: 200 })
    move(11, 201) // below threshold
    flushFrame()
    up()

    expect(onPreview).not.toHaveBeenCalled()
    expect(onHit).toHaveBeenCalledTimes(1)
    expect(onHit).toHaveBeenCalledWith(expect.objectContaining({
      paths: [], mode: 'replace', isClick: true, next: [],
    }))
  })

  it('Escape cancels the gesture without firing onHit', async () => {
    await render()
    mouse('mousedown', container, { clientX: 10, clientY: 200 })
    move(300, 60)
    flushFrame()
    expect(container.querySelector('.mq-marquee-rect')).not.toBeNull()

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(container.querySelector('.mq-marquee-rect')).toBeNull()
    expect(container.classList.contains('mq-selecting')).toBe(false)

    up() // late mouseup after cancel
    expect(onHit).not.toHaveBeenCalled()
  })

  it('re-entrant mousedown during an active drag is ignored', async () => {
    await render()
    mouse('mousedown', container, { clientX: 10, clientY: 200 })
    move(300, 60)
    flushFrame()
    // A second empty-area press mid-gesture must not orphan the live rect.
    mouse('mousedown', container, { clientX: 5, clientY: 250 })
    expect(container.querySelectorAll('.mq-marquee-rect')).toHaveLength(1)

    up()
    expect(onHit).toHaveBeenCalledTimes(1)
    expect(container.querySelector('.mq-marquee-rect')).toBeNull()
  })

  it('empty-area mousedown preventDefaults (text selection) but contextmenu still fires', async () => {
    await render()
    const onCtx = vi.fn()
    container.addEventListener('contextmenu', onCtx)

    const ev = mouse('mousedown', container, { clientX: 10, clientY: 200 })
    expect(ev.defaultPrevented).toBe(true)

    container.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }))
    expect(onCtx).toHaveBeenCalledTimes(1)

    up()
  })

  it('auto-scrolls the container when the pointer nears an edge', async () => {
    await render({ autoScroll: true })
    mouse('mousedown', container, { clientX: 10, clientY: 200 })
    move(390, 60) // within 24px of the right edge (400)
    flushFrame()
    expect(container.scrollLeft).toBeGreaterThan(0)
    up()
  })

  it('enabled=false attaches no listeners', async () => {
    await act(async () => {
      root = createRoot(mountDiv)
      root.render(<DisabledHarness />)
    })
    const el = document.querySelector('.mq-container-disabled') as HTMLDivElement
    mockLayout(el)
    const ev = mouse('mousedown', el, { clientX: 10, clientY: 200 })
    move(300, 60)
    flushFrame()
    up()
    expect(ev.defaultPrevented).toBe(false)
    expect(el.querySelector('.mq-marquee-rect')).toBeNull()
  })

  it('unmount during a drag cleans up without firing onHit', async () => {
    await render()
    mouse('mousedown', container, { clientX: 10, clientY: 200 })
    move(300, 60)
    flushFrame()
    act(() => root.unmount())
    expect(onHit).not.toHaveBeenCalled()
  })

  function DisabledHarness() {
    const ref = useRef<HTMLDivElement>(null)
    useMarqueeSelection({ containerRef: ref, itemSelector: '.mq-item', onHit, enabled: false })
    return <div ref={ref} className="mq-container-disabled"><div className="mq-item" data-mq-path="/a">a</div></div>
  }
})
