import { useEffect, useState } from 'react'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { I18nProvider } from '../../../i18n'
import { RightPanelTabs, type RightTabItem } from './RightPanelTabs'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

class ResizeObserverStub {
  observe() {}
  disconnect() {}
}

;(globalThis as any).ResizeObserver = ResizeObserverStub
;(HTMLElement.prototype as any).scrollIntoView = vi.fn()

describe('RightPanelTabs keepAlive', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('keeps a stateful tab mounted while another tab is active', () => {
    const mounted = vi.fn()
    const unmounted = vi.fn()

    function StatefulPane() {
      useEffect(() => {
        mounted()
        return () => unmounted()
      }, [])
      return <div data-testid="stateful-pane">stateful</div>
    }

    function Harness() {
      const [activeTabId, setActiveTabId] = useState('ssh')
      const tabs: RightTabItem[] = [
        { id: 'ssh', label: 'SSH', keepAlive: true, render: () => <StatefulPane /> },
        { id: 'other', label: 'Other', render: () => <div>other</div> },
      ]
      return (
        <I18nProvider initialLocale="en-US">
          <RightPanelTabs
            tabs={tabs}
            activeTabId={activeTabId}
            onSwitchTab={setActiveTabId}
            onCloseTab={() => {}}
          />
        </I18nProvider>
      )
    }

    act(() => root.render(<Harness />))
    expect(mounted).toHaveBeenCalledTimes(1)

    const otherTab = Array.from(container.querySelectorAll('.rpt-tab'))
      .find((tab) => tab.textContent?.includes('Other'))
    act(() => otherTab?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(mounted).toHaveBeenCalledTimes(1)
    expect(unmounted).not.toHaveBeenCalled()
    expect(container.querySelector('.rpt-tab-pane.hidden [data-testid="stateful-pane"]')).not.toBeNull()

    const sshTab = Array.from(container.querySelectorAll('.rpt-tab'))
      .find((tab) => tab.textContent?.includes('SSH'))
    act(() => sshTab?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(mounted).toHaveBeenCalledTimes(1)
    expect(unmounted).not.toHaveBeenCalled()
  })

  it('does not move a keepAlive pane in the DOM when tabs are reordered', () => {
    // Moving an <iframe> (plugin app panel) in the DOM tree discards its
    // browsing context and reloads it — keepAlive panes must therefore keep a
    // stable DOM position regardless of the tabbar order.
    const paneA = <div data-testid="pane-a">a</div>
    const paneB = <div data-testid="pane-b">b</div>
    const paneC = <div data-testid="pane-c">c</div>

    function Harness({ order, activeId }: { order: string[]; activeId: string }) {
      const all: Record<string, RightTabItem> = {
        a: { id: 'plugin:a', label: 'A', keepAlive: true, render: () => paneA },
        b: { id: 'plugin:b', label: 'B', keepAlive: true, render: () => paneB },
        c: { id: 'plugin:c', label: 'C', keepAlive: true, render: () => paneC },
      }
      return (
        <I18nProvider initialLocale="en-US">
          <RightPanelTabs
            tabs={order.map((id) => all[id]!)}
            activeTabId={activeId}
            onSwitchTab={() => {}}
            onCloseTab={() => {}}
          />
        </I18nProvider>
      )
    }

    act(() => root.render(<Harness order={['a', 'b', 'c']} activeId="plugin:a" />))
    const panesBefore = Array.from(container.querySelectorAll('.rpt-tab-pane [data-testid]'))
      .map((el) => el.getAttribute('data-testid'))
    expect(panesBefore).toEqual(['pane-a', 'pane-b', 'pane-c'])

    // Reorder b to front and activate it: pane DOM order must stay a, b, c.
    act(() => root.render(<Harness order={['b', 'a', 'c']} activeId="plugin:b" />))
    const panesAfter = Array.from(container.querySelectorAll('.rpt-tab-pane [data-testid]'))
      .map((el) => el.getAttribute('data-testid'))
    expect(panesAfter).toEqual(['pane-a', 'pane-b', 'pane-c'])
    expect(container.querySelector('.rpt-tab-pane.active [data-testid="pane-b"]')).not.toBeNull()
  })
})

describe('RightPanelTabs drag insertion indicator', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  /** Minimal DataTransfer stand-in for jsdom (no native DnD). */
  function makeDataTransfer() {
    const store: Record<string, string> = {}
    return {
      setData: (k: string, v: string) => { store[k] = v },
      getData: (k: string) => store[k] ?? '',
      get types() { return Object.keys(store) },
      effectAllowed: 'none' as string,
      dropEffect: 'none' as string,
    }
  }

  /** Dispatch a (mouse-based) drag event with dataTransfer + drag event opts. */
  function fireDrag(
    el: Element,
    type: string,
    dt: ReturnType<typeof makeDataTransfer>,
    opts?: { clientX?: number; relatedTarget?: EventTarget | null },
  ) {
    const evt = new MouseEvent(type, {
      bubbles: true,
      cancelable: true,
      clientX: opts?.clientX ?? 0,
      relatedTarget: opts?.relatedTarget ?? null,
    })
    Object.defineProperty(evt, 'dataTransfer', { value: dt, configurable: true })
    act(() => { el.dispatchEvent(evt) })
    return evt
  }

  /** Stub per-tab getBoundingClientRect so drop-edge resolution is deterministic. */
  function stubTabRects(rects: Array<{ left: number; right: number }>) {
    const tabs = Array.from(container.querySelectorAll('.rpt-tab')) as HTMLElement[]
    tabs.forEach((tab, i) => {
      const r = rects[i] ?? { left: 0, right: 0 }
      vi.spyOn(tab, 'getBoundingClientRect').mockReturnValue({
        x: r.left,
        y: 0,
        top: 0,
        left: r.left,
        right: r.right,
        bottom: 40,
        width: r.right - r.left,
        height: 40,
        toJSON: () => ({}),
      } as DOMRect)
    })
    return tabs
  }

  function renderReorderable() {
    const tabs: RightTabItem[] = [
      { id: 'a', label: 'A', render: () => <div>a</div> },
      { id: 'b', label: 'B', render: () => <div>b</div> },
    ]
    act(() => root.render(
      <I18nProvider initialLocale="en-US">
        <RightPanelTabs
          tabs={tabs}
          activeTabId="a"
          onSwitchTab={() => {}}
          onCloseTab={() => {}}
          onReorderTabs={vi.fn()}
        />
      </I18nProvider>
    ))
    return tabs
  }

  it('keeps the drop indicator set when dragleave stays inside the tabbar (no blink)', () => {
    renderReorderable()
    const tabA = container.querySelector('.rpt-tab[data-tab-id="a"]') as HTMLElement
    const tabB = container.querySelector('.rpt-tab[data-tab-id="b"]') as HTMLElement
    // A = [10,60], B = [70,120]. clientX 100 → nearest edge is B's right (idx 2).
    stubTabRects([{ left: 10, right: 60 }, { left: 70, right: 120 }])

    const dt = makeDataTransfer()
    fireDrag(tabA, 'dragstart', dt)
    fireDrag(tabA, 'dragover', dt, { clientX: 100 })
    expect(tabB.classList.contains('drop-after')).toBe(true)

    // Internal leave: pointer crosses from tab A into tab B (still inside the
    // tabbar). The indicator must persist — this is the pre-fix flicker frame.
    fireDrag(tabA, 'dragleave', dt, { relatedTarget: tabB })
    expect(tabB.classList.contains('drop-after')).toBe(true)

    // Pointer settles on B: a subsequent dragover keeps it set.
    fireDrag(tabB, 'dragover', dt, { clientX: 100 })
    expect(tabB.classList.contains('drop-after')).toBe(true)
  })

  it('clears the drop indicator when dragleave leaves the tabbar', () => {
    renderReorderable()
    const tabA = container.querySelector('.rpt-tab[data-tab-id="a"]') as HTMLElement
    const tabB = container.querySelector('.rpt-tab[data-tab-id="b"]') as HTMLElement
    stubTabRects([{ left: 10, right: 60 }, { left: 70, right: 120 }])

    const dt = makeDataTransfer()
    fireDrag(tabA, 'dragstart', dt)
    fireDrag(tabA, 'dragover', dt, { clientX: 100 })
    expect(tabB.classList.contains('drop-after')).toBe(true)

    // Leaving into the body (outside the tabbar) must clear the indicator.
    const body = container.querySelector('.rpt-body') as HTMLElement
    fireDrag(tabA, 'dragleave', dt, { relatedTarget: body })
    expect(tabB.classList.contains('drop-after')).toBe(false)
  })

  it('clears the drop indicator when dragleave leaves the window (relatedTarget null)', () => {
    renderReorderable()
    const tabA = container.querySelector('.rpt-tab[data-tab-id="a"]') as HTMLElement
    const tabB = container.querySelector('.rpt-tab[data-tab-id="b"]') as HTMLElement
    stubTabRects([{ left: 10, right: 60 }, { left: 70, right: 120 }])

    const dt = makeDataTransfer()
    fireDrag(tabA, 'dragstart', dt)
    fireDrag(tabA, 'dragover', dt, { clientX: 100 })
    expect(tabB.classList.contains('drop-after')).toBe(true)

    fireDrag(tabA, 'dragleave', dt, { relatedTarget: null })
    expect(tabB.classList.contains('drop-after')).toBe(false)
  })

  it('calls onReorderTabs(fromId, idx) on drop (contract unchanged)', () => {
    const reorder = vi.fn()
    const tabs: RightTabItem[] = [
      { id: 'a', label: 'A', render: () => <div>a</div> },
      { id: 'b', label: 'B', render: () => <div>b</div> },
    ]
    act(() => root.render(
      <I18nProvider initialLocale="en-US">
        <RightPanelTabs
          tabs={tabs}
          activeTabId="a"
          onSwitchTab={() => {}}
          onCloseTab={() => {}}
          onReorderTabs={reorder}
        />
      </I18nProvider>
    ))
    const tabA = container.querySelector('.rpt-tab[data-tab-id="a"]') as HTMLElement
    const tabB = container.querySelector('.rpt-tab[data-tab-id="b"]') as HTMLElement
    stubTabRects([{ left: 10, right: 60 }, { left: 70, right: 120 }])

    const dt = makeDataTransfer()
    fireDrag(tabA, 'dragstart', dt)
    fireDrag(tabA, 'dragover', dt, { clientX: 100 })
    fireDrag(tabB, 'drop', dt, { clientX: 100 })

    expect(reorder).toHaveBeenCalledWith('a', 2)
  })

  it('floating bottom slot does not block the body scrollbar behind the composer', () => {
    const css = readFileSync(resolve(__dirname, 'RightPanelTabs.css'), 'utf-8')
    const slotRule = /\.rpt-container\.rpt-floating-mode \.rpt-bottom-slot\s*\{[^}]*\}/.exec(css)?.[0] ?? ''
    expect(slotRule).toContain('position: absolute')
    expect(slotRule).toContain('pointer-events: none')
  })
})
