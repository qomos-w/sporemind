import { describe, it, expect, vi, beforeEach, afterEach, type Mock } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { I18nProvider } from '../../../i18n'
import { BrowserOverlayProvider, type BrowserOverlayApi } from '../browserOverlay'
import { ToolGuideOverlay } from './ToolGuideOverlay'
import { toolGuideManager } from './tool-guide-manager'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('ToolGuideOverlay', () => {
  let container: HTMLDivElement
  let root: Root
  let overlayApi: BrowserOverlayApi
  let pushOverlay: Mock
  let popOverlay: Mock

  const render = async () => {
    pushOverlay = vi.fn()
    popOverlay = vi.fn()
    overlayApi = {
      pushOverlay,
      popOverlay,
    }
    await act(async () => {
      root = createRoot(container)
      root.render(
        <I18nProvider initialLocale="en-US">
          <BrowserOverlayProvider value={overlayApi}>
            <ToolGuideOverlay />
          </BrowserOverlayProvider>
        </I18nProvider>,
      )
    })
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    // Reset manager state
    toolGuideManager.hideToolGuide()
  })

  afterEach(() => {
    act(() => root?.unmount())
    if (container.parentNode) document.body.removeChild(container)
    toolGuideManager.hideToolGuide()
    vi.restoreAllMocks()
  })

  describe('visibility', () => {
    it('renders nothing when not visible', async () => {
      await render()
      expect(container.querySelector('.tool-guide-overlay-root')).toBeNull()
    })

    it('renders overlay when toolGuideManager.showToolGuide() is called', async () => {
      await render()
      await act(async () => {
        toolGuideManager.showToolGuide()
      })
      expect(container.querySelector('.tool-guide-overlay-root')).toBeTruthy()
      expect(container.querySelector('.tool-guide-overlay-panel')).toBeTruthy()
    })

    it('hides overlay when toolGuideManager.hideToolGuide() is called', async () => {
      await render()
      await act(async () => {
        toolGuideManager.showToolGuide()
      })
      expect(container.querySelector('.tool-guide-overlay-root')).toBeTruthy()

      await act(async () => {
        toolGuideManager.hideToolGuide()
      })
      expect(container.querySelector('.tool-guide-overlay-root')).toBeNull()
    })
  })

  describe('close button', () => {
    it('renders a close button when visible', async () => {
      await render()
      await act(async () => {
        toolGuideManager.showToolGuide()
      })
      expect(container.querySelector('.tool-guide-overlay-close')).toBeTruthy()
    })

    it('hides the overlay when close button is clicked', async () => {
      await render()
      await act(async () => {
        toolGuideManager.showToolGuide()
      })
      const closeBtn = container.querySelector('.tool-guide-overlay-close') as HTMLButtonElement
      act(() => closeBtn?.click())

      await act(async () => {})
      expect(container.querySelector('.tool-guide-overlay-root')).toBeNull()
    })

    it('hides the overlay when backdrop is clicked', async () => {
      await render()
      await act(async () => {
        toolGuideManager.showToolGuide()
      })
      const backdrop = container.querySelector('.tool-guide-overlay-backdrop') as HTMLDivElement
      act(() => backdrop?.click())

      await act(async () => {})
      expect(container.querySelector('.tool-guide-overlay-root')).toBeNull()
    })
  })

  describe('collapsible cards', () => {
    beforeEach(async () => {
      await render()
      await act(async () => {
        toolGuideManager.showToolGuide()
      })
    })

    it('renders a card for each tool guide entry', async () => {
      const cards = container.querySelectorAll('.tool-guide-card')
      expect(cards.length).toBeGreaterThanOrEqual(5)
    })

    it('cards are collapsed by default (no body visible)', async () => {
      const bodies = container.querySelectorAll('.tool-guide-card-body')
      expect(bodies.length).toBe(0)
    })

    it('expands card body when header is clicked', async () => {
      const headers = container.querySelectorAll('.tool-guide-card-header')
      expect(headers.length).toBeGreaterThan(0)

      act(() => headers[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

      await act(async () => {})

      const bodies = container.querySelectorAll('.tool-guide-card-body')
      expect(bodies.length).toBe(1)
      // First card should have the expanded class
      const firstCard = container.querySelector('.tool-guide-card')
      expect(firstCard?.classList.contains('tool-guide-card--expanded')).toBe(true)
    })

    it('collapses card when clicking the same header again', async () => {
      const headers = container.querySelectorAll('.tool-guide-card-header')

      // Expand
      act(() => headers[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
      await act(async () => {})
      expect(container.querySelectorAll('.tool-guide-card-body').length).toBe(1)

      // Collapse
      act(() => headers[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
      await act(async () => {})
      expect(container.querySelectorAll('.tool-guide-card-body').length).toBe(0)
    })

    it('only one card is expanded at a time', async () => {
      const headers = container.querySelectorAll('.tool-guide-card-header')
      expect(headers.length).toBeGreaterThanOrEqual(2)

      // Expand first
      act(() => headers[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
      await act(async () => {})
      expect(container.querySelectorAll('.tool-guide-card-body').length).toBe(1)

      // Expand second — first should collapse
      act(() => headers[1]?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
      await act(async () => {})
      expect(container.querySelectorAll('.tool-guide-card-body').length).toBe(1)
      const cards = container.querySelectorAll('.tool-guide-card')
      expect(cards[0]?.classList.contains('tool-guide-card--expanded')).toBe(false)
      expect(cards[1]?.classList.contains('tool-guide-card--expanded')).toBe(true)
    })

    it('updates aria-expanded attribute on toggle', async () => {
      const headers = container.querySelectorAll('.tool-guide-card-header') as NodeListOf<HTMLElement>
      expect(headers[0]?.getAttribute('aria-expanded')).toBe('false')

      act(() => headers[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
      await act(async () => {})
      expect(headers[0]?.getAttribute('aria-expanded')).toBe('true')
    })
  })

  describe('useBrowserOverlay registration', () => {
    it('registers overlay (pushOverlay) when shown', async () => {
      await render()
      // Nothing pushed when hidden
      expect(pushOverlay).not.toHaveBeenCalled()

      await act(async () => {
        toolGuideManager.showToolGuide()
      })
      expect(pushOverlay).toHaveBeenCalledTimes(1)
    })

    it('deregisters overlay (popOverlay) when hidden', async () => {
      await render()
      await act(async () => {
        toolGuideManager.showToolGuide()
      })
      expect(pushOverlay).toHaveBeenCalledTimes(1)
      pushOverlay.mockClear()

      await act(async () => {
        toolGuideManager.hideToolGuide()
      })
      expect(popOverlay).toHaveBeenCalledTimes(1)
    })
  })
})
