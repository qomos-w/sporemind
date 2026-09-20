import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { I18nProvider } from '../../../i18n'
import { GuideOverlay } from './GuideOverlay'
import { useAnchorRect } from '../overlay/anchor'
import type { GuideStep } from '../../../gen-types/interfacemanager'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  useBrowserOverlay: vi.fn(),
  resolveAnchor: vi.fn(),
}))

vi.mock('../browserOverlay', () => ({
  useBrowserOverlay: hoisted.useBrowserOverlay,
}))

vi.mock('../overlay/anchor', async () => {
  const actual = await vi.importActual('../overlay/anchor') as any
  return {
    ...actual,
    resolveAnchor: hoisted.resolveAnchor,
    useAnchorRect: vi.fn(() => new DOMRect()),
  }
})

// Mock guide manager
vi.mock('../../../application/guide-manager', () => ({
  guideManager: {
    reportTargetMissing: vi.fn().mockResolvedValue(undefined),
    subscribe: vi.fn(() => () => {}),
    getState: vi.fn(() => ({ steps: [], currentIndex: 0, visible: false })),
  },
}))

describe('GuideOverlay', () => {
  let container: HTMLDivElement
  let root: Root

  const mockSteps: GuideStep[] = [
    { TargetGuideId: 'composer.input', Title: 'Step 1', Body: 'This is step 1' },
    { TargetGuideId: 'composer.send', Title: 'Step 2', Body: 'This is step 2' },
  ]

  const render = async (props: Partial<React.ComponentProps<typeof GuideOverlay>> = {}) => {
    await act(async () => {
      root = createRoot(container)
      root.render(
        <I18nProvider initialLocale="en-US">
          <GuideOverlay
            steps={mockSteps}
            currentIndex={0}
            visible={false}
            onNext={vi.fn()}
            onPrev={vi.fn()}
            onComplete={vi.fn()}
            onSkip={vi.fn()}
            {...props}
          />
        </I18nProvider>,
      )
    })
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    // Add mock target elements
    const inputEl = document.createElement('textarea')
    inputEl.setAttribute('data-guide-id', 'composer.input')
    document.body.appendChild(inputEl)
    const sendEl = document.createElement('button')
    sendEl.setAttribute('data-guide-id', 'composer.send')
    document.body.appendChild(sendEl)
    hoisted.useBrowserOverlay.mockClear()
    hoisted.resolveAnchor.mockImplementation(async (target: string) => {
      const id = target.replace(/^guide:/, '')
      return document.querySelector<HTMLElement>(`[data-guide-id="${id}"]`)
    })
  })

  afterEach(() => {
    act(() => root?.unmount())
    document.body.removeChild(container)
    // Clean up guide-id elements and any portal leftovers
    document.querySelectorAll('[data-guide-id]').forEach(el => el.remove())
    document.querySelectorAll('.anchored-overlay').forEach(el => el.remove())
    vi.restoreAllMocks()
  })

  describe('visibility', () => {
    it('renders nothing when not visible', async () => {
      await render({ visible: false })
      expect(document.querySelector('.guide-card')).toBeNull()
    })

    it('renders nothing when steps are empty', async () => {
      await render({ steps: [], visible: true })
      expect(document.querySelector('.guide-card')).toBeNull()
    })

    it('renders card when visible and steps exist', async () => {
      await render({ visible: true })
      expect(document.querySelector('.guide-card')).toBeTruthy()
      expect(document.querySelector('.anchored-overlay')).toBeTruthy()
    })
  })

  describe('anchored rendering', () => {
    it('resolves the anchor for the current step', async () => {
      await render({ visible: true, currentIndex: 0 })
      await act(async () => {})
      expect(hoisted.resolveAnchor).toHaveBeenCalledWith('guide:composer.input')
    })

    it('renders the card anchored with the requested placement', async () => {
      const steps: GuideStep[] = [
        { TargetGuideId: 'composer.send', Title: 'Step 2', Body: 'Body', Placement: 'right' },
      ]
      await render({ visible: true, currentIndex: 0, steps })
      await act(async () => {})
      expect(hoisted.resolveAnchor).toHaveBeenCalledWith('guide:composer.send')
      expect(document.querySelector('.guide-card')).toBeTruthy()
    })
  })

  describe('browser overlay registration', () => {
    it('calls useBrowserOverlay with true when visible', async () => {
      await render({ visible: true })
      expect(hoisted.useBrowserOverlay).toHaveBeenCalledWith(true)
    })

    it('calls useBrowserOverlay with false when hidden', async () => {
      await render({ visible: false })
      expect(hoisted.useBrowserOverlay).toHaveBeenCalledWith(false)
    })
  })

  describe('progress bar', () => {
    it('renders progress bar when multiple steps', async () => {
      await render({ visible: true, currentIndex: 0 })
      expect(document.querySelector('.guide-card-progress')).toBeTruthy()
    })

    it('does not render progress bar for single step', async () => {
      const singleStep: GuideStep[] = [
        { TargetGuideId: 'composer.input', Title: 'Only', Body: 'Single step' },
      ]
      await render({ visible: true, steps: singleStep, currentIndex: 0 })
      expect(document.querySelector('.guide-card-progress')).toBeNull()
    })

    it('sets fill width to 50% on first of two steps', async () => {
      await render({ visible: true, currentIndex: 0 })
      const fill = document.querySelector('.guide-card-progress-fill') as HTMLElement
      expect(fill.style.width).toBe('50%')
    })

    it('sets fill width to 100% on last step', async () => {
      await render({ visible: true, currentIndex: 1 })
      const fill = document.querySelector('.guide-card-progress-fill') as HTMLElement
      expect(fill.style.width).toBe('100%')
    })

    it('sets aria attributes on progress bar', async () => {
      await render({ visible: true, currentIndex: 0 })
      const bar = document.querySelector('.guide-card-progress')
      expect(bar?.getAttribute('role')).toBe('progressbar')
      expect(bar?.getAttribute('aria-valuenow')).toBe('1')
      expect(bar?.getAttribute('aria-valuemin')).toBe('1')
      expect(bar?.getAttribute('aria-valuemax')).toBe('2')
    })
  })

  describe('step content', () => {
    it('renders step title and body', async () => {
      await render({ visible: true, currentIndex: 0 })
      expect(document.querySelector('.guide-card-title')?.textContent).toBe('Step 1')
      expect(document.querySelector('.guide-card-body')?.textContent).toBe('This is step 1')
    })

    it('shows step counter using i18n format', async () => {
      await render({ visible: true, currentIndex: 0, steps: mockSteps })
      expect(document.querySelector('.guide-card-counter')?.textContent).toBe('Step 1 of 2')
    })

    it('shows step counter with category label', async () => {
      await render({ visible: true, currentIndex: 0, steps: mockSteps, categoryLabel: 'Basics' })
      expect(document.querySelector('.guide-card-counter')?.textContent).toBe('Basics · Step 1 of 2')
    })
  })

  describe('navigation buttons', () => {
    it('shows Next button on non-last step', async () => {
      await render({ visible: true, currentIndex: 0 })
      const nextBtn = document.querySelector('.guide-btn--primary')
      expect(nextBtn?.textContent?.trim()).toBe('Next')
    })

    it('shows Done button on last step', async () => {
      await render({ visible: true, currentIndex: 1 })
      const doneBtn = document.querySelector('.guide-btn--primary')
      expect(doneBtn?.textContent?.trim()).toBe('Done')
    })

    it('shows Back button on non-first step', async () => {
      await render({ visible: true, currentIndex: 1 })
      expect(document.querySelector('.guide-btn--secondary')?.textContent?.trim()).toBe('Back')
    })

    it('hides Back button on first step', async () => {
      await render({ visible: true, currentIndex: 0 })
      expect(document.querySelector('.guide-btn--secondary')).toBeNull()
    })

    it('always shows Skip button', async () => {
      await render({ visible: true, currentIndex: 0 })
      expect(document.querySelector('.guide-btn--skip')).toBeTruthy()
      expect(document.querySelector('.guide-btn--skip')?.textContent?.trim()).toBe('Skip guide')
    })
  })

  describe('button callbacks', () => {
    it('calls onNext when Next button is clicked', async () => {
      const onNext = vi.fn()
      await render({ visible: true, currentIndex: 0, onNext })
      const btn = document.querySelector('.guide-btn--primary') as HTMLButtonElement
      act(() => { btn?.click() })
      expect(onNext).toHaveBeenCalledTimes(1)
    })

    it('calls onPrev when Back button is clicked', async () => {
      const onPrev = vi.fn()
      await render({ visible: true, currentIndex: 1, onPrev })
      const btn = document.querySelector('.guide-btn--secondary') as HTMLButtonElement
      act(() => { btn?.click() })
      expect(onPrev).toHaveBeenCalledTimes(1)
    })

    it('calls onComplete when Done button is clicked', async () => {
      const onComplete = vi.fn()
      await render({ visible: true, currentIndex: 1, onComplete })
      const btn = document.querySelector('.guide-btn--primary') as HTMLButtonElement
      act(() => { btn?.click() })
      expect(onComplete).toHaveBeenCalledTimes(1)
    })

    it('calls onSkip when Skip button is clicked', async () => {
      const onSkip = vi.fn()
      await render({ visible: true, currentIndex: 0, onSkip })
      const btn = document.querySelector('.guide-btn--skip') as HTMLButtonElement
      act(() => { btn?.click() })
      expect(onSkip).toHaveBeenCalledTimes(1)
    })
  })

  describe('target missing', () => {
    it('shows warning when target element is missing', async () => {
      hoisted.resolveAnchor.mockResolvedValue(null)

      await render({ visible: true, currentIndex: 0 })
      await act(async () => {})

      expect(document.querySelector('.guide-card--missing')).toBeTruthy()
      expect(document.querySelector('.guide-card-target-missing')?.textContent).toContain('not visible')
    })

    it('still shows full step content when target is missing', async () => {
      hoisted.resolveAnchor.mockResolvedValue(null)

      await render({ visible: true, currentIndex: 0 })
      await act(async () => {})

      // Title and body still visible
      expect(document.querySelector('.guide-card-title')?.textContent).toBe('Step 1')
      expect(document.querySelector('.guide-card-body')?.textContent).toBe('This is step 1')
      // Buttons still present
      expect(document.querySelector('.guide-btn--primary')).toBeTruthy()
      expect(document.querySelector('.guide-btn--skip')).toBeTruthy()
    })

    it('calls reportTargetMissing when target is not found', async () => {
      hoisted.resolveAnchor.mockResolvedValue(null)
      const { guideManager } = await import('../../../application/guide-manager')

      await render({ visible: true, currentIndex: 0 })
      await act(async () => {})

      expect(guideManager.reportTargetMissing).toHaveBeenCalledWith('composer.input', 'Step 1')
    })
  })

  describe('action gating', () => {
    const gatedSteps: GuideStep[] = [
      { TargetGuideId: 'composer.input', Title: 'Gated', Body: 'Do something', ExpectedInteraction: 'submit' },
      { TargetGuideId: 'composer.send', Title: 'Final', Body: 'Final step' },
    ]

    it('disables Next and shows hint when gate is not met', async () => {
      await render({ visible: true, currentIndex: 0, steps: gatedSteps, gateMet: false })
      const nextBtn = document.querySelector('.guide-btn--primary') as HTMLButtonElement
      expect(nextBtn.disabled).toBe(true)
      expect(document.querySelector('.guide-card-gate-hint')?.textContent).toBe('Complete the action above to continue')
    })

    it('enables Next and hides hint when gate is met', async () => {
      await render({ visible: true, currentIndex: 0, steps: gatedSteps, gateMet: true })
      const nextBtn = document.querySelector('.guide-btn--primary') as HTMLButtonElement
      expect(nextBtn.disabled).toBe(false)
      expect(document.querySelector('.guide-card-gate-hint')).toBeNull()
    })

    it('last step stays enabled even when gateMet is false (no gate expected)', async () => {
      await render({ visible: true, currentIndex: 1, steps: gatedSteps, gateMet: false })
      const doneBtn = document.querySelector('.guide-btn--primary') as HTMLButtonElement
      expect(doneBtn.disabled).toBe(false)
    })
  })

  describe('spotlight highlight', () => {
    beforeEach(() => {
      vi.mocked(useAnchorRect).mockImplementation(() => new DOMRect(10, 20, 100, 40))
    })

    afterEach(() => {
      vi.mocked(useAnchorRect).mockImplementation(() => new DOMRect())
    })

    it('renders a non-blocking highlight ring around the resolved target', async () => {
      vi.mocked(useAnchorRect).mockImplementation(
        () => new DOMRect(10, 20, 100, 40) as DOMRect,
      )
      await render({ visible: true, currentIndex: 0 })
      await act(async () => {})

      const ring = document.querySelector('.spotlight-ring') as HTMLDivElement
      expect(ring).toBeTruthy()
      // target (10,20,100,40) + holePadding(4): ring box = (6,16,108,48).
      expect(ring.style.left).toBe('6px')
      expect(ring.style.top).toBe('16px')
      expect(ring.style.width).toBe('108px')
      expect(ring.style.height).toBe('48px')
      // The guide must not block UI interaction.
      expect(ring.style.pointerEvents).toBe('none')
      expect(ring.getAttribute('aria-hidden')).toBe('true')
    })

    it('treats a zero-size rect (collapsed container) as unreachable, not anchored at (0,0)', async () => {
      // Resolved element whose rect is zero (e.g. sidebar collapsed):
      // no ring, missing hint shown, card styled as missing.
      vi.mocked(useAnchorRect).mockImplementation(() => new DOMRect())
      await render({ visible: true, currentIndex: 0 })
      await act(async () => {})

      expect(document.querySelector('.spotlight-ring')).toBeNull()
      expect(document.querySelector('.guide-card--missing')).toBeTruthy()
      expect(
        document.querySelector('.guide-card-target-missing'),
      ).toBeTruthy()
    })

    it('re-resolves an unresolved anchor and lights the ring when it appears', async () => {
      vi.useFakeTimers()
      try {
        vi.mocked(useAnchorRect).mockImplementation(
          () => new DOMRect(10, 20, 100, 40) as DOMRect,
        )
        const el = document.querySelector('[data-guide-id="composer.input"]')
        hoisted.resolveAnchor.mockReset()
        // Both AnchoredOverlay (child, effects run first) and GuideOverlay
        // resolve the anchor on mount; both must see the first miss.
        hoisted.resolveAnchor
          .mockResolvedValueOnce(null)
          .mockResolvedValueOnce(null)
          .mockResolvedValue(el)
        await render({ visible: true, currentIndex: 0 })
        await act(async () => {})
        // First attempt failed: missing state, retry timer armed.
        expect(document.querySelector('.spotlight-ring')).toBeNull()
        expect(document.querySelector('.guide-card--missing')).toBeTruthy()

        await act(async () => {
          vi.advanceTimersByTime(2000)
        })
        // Retry found the anchor: ring appears, card re-anchors.
        expect(document.querySelector('.spotlight-ring')).not.toBeNull()
        expect(document.querySelector('.guide-card--missing')).toBeNull()
      } finally {
        vi.useRealTimers()
      }
    })

    it('hides the spotlight when the target is missing', async () => {
      hoisted.resolveAnchor.mockResolvedValue(null)

      await render({ visible: true, currentIndex: 0 })
      await act(async () => {})

      expect(document.querySelector('.spotlight-ring')).toBeNull()
      expect(document.querySelector('.guide-card--missing')).toBeTruthy()
    })

    it('hides the spotlight when the overlay is not visible', async () => {
      await render({ visible: false, currentIndex: 0 })
      await act(async () => {})

      expect(document.querySelector('.spotlight-ring')).toBeNull()
    })
  })
})
