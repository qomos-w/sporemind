import { act } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach, type Mock } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { I18nProvider } from '../../../i18n'
import { BrowserOverlayProvider, type BrowserOverlayApi } from '../browserOverlay'
import { DelegatedTooltip } from './DelegatedTooltip'
import { guideManager } from '../../../application/guide-manager'
import { GuideIds } from '../guide-ids'

vi.mock('./anchor', () => ({
  useAnchorRect: vi.fn(),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root
let pushOverlay: Mock
let popOverlay: Mock
let overlayApi: BrowserOverlayApi
let anchorRect: DOMRect
const originalGetBoundingClientRect = Element.prototype.getBoundingClientRect

function tooltipRect(): DOMRect {
  return new DOMRect(0, 0, 120, 60)
}

beforeEach(async () => {
  container = document.createElement('div')
  document.body.appendChild(container)
  pushOverlay = vi.fn()
  popOverlay = vi.fn()
  overlayApi = { pushOverlay, popOverlay }
  anchorRect = new DOMRect(100, 100, 50, 30)
  const { useAnchorRect } = await import('./anchor')
  vi.mocked(useAnchorRect).mockReturnValue(anchorRect)
  guideManager.hideGuide()

  Element.prototype.getBoundingClientRect = function (this: Element) {
    if (this.classList.contains('delegated-tooltip')) {
      return tooltipRect()
    }
    return originalGetBoundingClientRect.call(this)
  }
})

afterEach(() => {
  act(() => root?.unmount())
  if (container.parentNode) document.body.removeChild(container)
  guideManager.hideGuide()
  vi.restoreAllMocks()
  Element.prototype.getBoundingClientRect = originalGetBoundingClientRect
})

async function render() {
  await act(async () => {
    root = createRoot(container)
    root.render(
      <I18nProvider initialLocale="en-US">
        <BrowserOverlayProvider value={overlayApi}>
          <DelegatedTooltip />
        </BrowserOverlayProvider>
      </I18nProvider>
    )
  })
}

function makeAnchor(key: string, attrs: Record<string, string> = {}): HTMLElement {
  const el = document.createElement('button')
  el.setAttribute('data-tooltip-key', key)
  for (const [k, v] of Object.entries(attrs)) {
    el.setAttribute(k, v)
  }
  el.getBoundingClientRect = () => anchorRect
  document.body.appendChild(el)
  return el
}

function makeGuideAnchor(guideId: string, attrs: Record<string, string> = {}): HTMLElement {
  const el = document.createElement('button')
  el.setAttribute('data-guide-id', guideId)
  for (const [k, v] of Object.entries(attrs)) {
    el.setAttribute(k, v)
  }
  el.getBoundingClientRect = () => anchorRect
  document.body.appendChild(el)
  return el
}

async function hover(el: Element) {
  await act(async () => {
    el.dispatchEvent(new MouseEvent('mouseover', { bubbles: true, cancelable: true }))
  })
}

async function unhover(el: Element, related?: Element) {
  await act(async () => {
    el.dispatchEvent(
      new MouseEvent('mouseout', {
        bubbles: true,
        cancelable: true,
        relatedTarget: related ?? document.body,
      })
    )
  })
}

async function focus(el: Element) {
  await act(async () => {
    el.dispatchEvent(new FocusEvent('focusin', { bubbles: true, cancelable: true }))
  })
}

async function blur(el: Element, related?: Element) {
  await act(async () => {
    el.dispatchEvent(
      new FocusEvent('focusout', {
        bubbles: true,
        cancelable: true,
        relatedTarget: related ?? document.body,
      })
    )
  })
}

describe('DelegatedTooltip', () => {
  it('shows a tooltip on mouseover and hides on mouseout', async () => {
    await render()
    const el = makeAnchor('tooltip.composer.send')
    await hover(el)
    const tooltip = document.querySelector('.delegated-tooltip')
    expect(tooltip).not.toBeNull()
    expect(tooltip?.textContent).toBe('Send the current message')
    await unhover(el)
    expect(document.querySelector('.delegated-tooltip')).toBeNull()
  })

  it('shows a tooltip on focusin and hides on focusout', async () => {
    await render()
    const el = makeAnchor('tooltip.composer.send')
    await focus(el)
    expect(document.querySelector('.delegated-tooltip')).not.toBeNull()
    await blur(el)
    expect(document.querySelector('.delegated-tooltip')).toBeNull()
  })

  it('keeps the tooltip open when moving into a child of the same target', async () => {
    await render()
    const el = document.createElement('div')
    el.setAttribute('data-tooltip-key', 'tooltip.composer.send')
    el.getBoundingClientRect = () => anchorRect
    const child = document.createElement('span')
    el.appendChild(child)
    document.body.appendChild(el)

    await hover(el)
    expect(document.querySelector('.delegated-tooltip')).not.toBeNull()
    await unhover(el, child)
    expect(document.querySelector('.delegated-tooltip')).not.toBeNull()
  })

  it('does not show when the translation is missing', async () => {
    await render()
    const el = makeAnchor('tooltip.test.missing')
    await hover(el)
    expect(document.querySelector('.delegated-tooltip')).toBeNull()
  })

  it('resolves content via the guide-id registry', async () => {
    await render()
    const el = makeGuideAnchor(GuideIds.composer_send)
    await hover(el)
    const tooltip = document.querySelector('.delegated-tooltip')
    expect(tooltip).not.toBeNull()
    expect(tooltip?.textContent).toBe('Send the current message')
  })

  it('does not show for a guide id that is not in the registry', async () => {
    await render()
    const el = makeGuideAnchor('composer.input')
    await hover(el)
    expect(document.querySelector('.delegated-tooltip')).toBeNull()
  })

  it('defaults to plain mode and bottom placement', async () => {
    await render()
    const el = makeAnchor('tooltip.composer.send')
    await hover(el)
    expect(document.querySelector('.spotlight-ring')).toBeNull()
    const tooltip = document.querySelector('.delegated-tooltip') as HTMLElement
    expect(tooltip).not.toBeNull()
    // anchor.y (100) + anchor.height (30) + offset (8)
    expect(tooltip.style.top).toBe('138px')
  })

  it('parses data-tooltip-mode=spotlight and renders the spotlight ring', async () => {
    await render()
    const el = makeAnchor('tooltip.composer.send', { 'data-tooltip-mode': 'spotlight' })
    await hover(el)
    expect(document.querySelector('.spotlight-ring')).not.toBeNull()
    expect(document.querySelector('.delegated-tooltip')).not.toBeNull()
  })

  it('parses data-tooltip-side and positions accordingly', async () => {
    await render()
    const el = makeAnchor('tooltip.composer.send', { 'data-tooltip-side': 'right' })
    await hover(el)
    const tooltip = document.querySelector('.delegated-tooltip') as HTMLElement
    expect(tooltip).not.toBeNull()
    // anchor.x (100) + anchor.width (50) + offset (8)
    expect(tooltip.style.left).toBe('158px')
  })

  it('downgrades spotlight to plain when a guide is active', async () => {
    await render()
    await act(async () => {
      guideManager.showGuide(
        [{ TargetGuideId: 'composer.input', Title: 'Step', Body: 'Body' }],
        false
      )
    })
    const el = makeAnchor('tooltip.composer.send', { 'data-tooltip-mode': 'spotlight' })
    await hover(el)
    expect(document.querySelector('.spotlight-ring')).toBeNull()
    expect(document.querySelector('.delegated-tooltip')).not.toBeNull()
  })

  it('registers with the browser overlay manager while open', async () => {
    await render()
    expect(pushOverlay).not.toHaveBeenCalled()
    const el = makeAnchor('tooltip.composer.send')
    await hover(el)
    expect(pushOverlay).toHaveBeenCalledOnce()
    await unhover(el)
    expect(popOverlay).toHaveBeenCalledOnce()
  })
})
