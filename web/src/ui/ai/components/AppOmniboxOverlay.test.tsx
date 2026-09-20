import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AppOmniboxOverlay, filterOmniboxActions } from './AppOmniboxOverlay'
import type { AppOmniboxAction } from '../hooks/useAppOmnibox'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  useDropdownVerticalFit: vi.fn(() => {}),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../hooks/useDropdownVerticalFit', () => ({
  useDropdownVerticalFit: hoisted.useDropdownVerticalFit,
}))

const actions: AppOmniboxAction[] = [
  { id: 'a', section: 'command', label: 'Action A', keywords: [], score: 0, run: () => {} },
]

describe('filterOmniboxActions', () => {
  const slashActions: AppOmniboxAction[] = [
    { id: 'clear', section: 'command', label: '/clear', keywords: ['clear conversation'], score: 0, run: () => {} },
    { id: 'commit', section: 'command', label: '/commit', keywords: ['commit changes'], score: 0, run: () => {} },
  ]

  it('does not match non-consecutive characters', () => {
    expect(filterOmniboxActions(slashActions, 'ce')).toEqual([])
  })

  it('keeps continuous prefix and substring matches', () => {
    expect(filterOmniboxActions(slashActions, 'cle').map(action => action.id)).toEqual(['clear'])
    expect(filterOmniboxActions(slashActions, 'mmit').map(action => action.id)).toEqual(['commit'])
  })
})

describe('AppOmniboxOverlay', () => {
  let container: HTMLDivElement
  let root: Root
  const originalInnerHeight = window.innerHeight

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    Object.defineProperty(window, 'visualViewport', {
      value: {
        height: 600,
        width: 320,
        offsetLeft: 0,
        offsetTop: 0,
        pageLeft: 0,
        pageTop: 0,
        scale: 1,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      },
      writable: true,
      configurable: true,
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'innerHeight', { value: originalInnerHeight, writable: true })
    Object.defineProperty(window, 'visualViewport', { value: null, writable: true, configurable: true })
    act(() => root?.unmount())
    container.remove()
    vi.restoreAllMocks()
  })

  const render = async (props: Partial<React.ComponentProps<typeof AppOmniboxOverlay>> = {}) => {
    await act(async () => {
      root = createRoot(container)
      root.render(
        <AppOmniboxOverlay
          open
          onClose={() => {}}
          actions={actions}
          {...props}
        />,
      )
    })
  }

  it('positions below topbar when position is top', async () => {
    Object.defineProperty(window, 'innerHeight', { value: 600, writable: true })
    await render({ position: 'top' })

    const overlay = container.querySelector('.app-omnibox-container') as HTMLElement
    expect(overlay).toBeTruthy()
    expect(overlay.style.top).not.toBe('')
    expect(parseInt(overlay.style.top, 10)).toBeGreaterThanOrEqual(0)
  })

  it('repositions when visualViewport shrinks', async () => {
    Object.defineProperty(window, 'innerHeight', { value: 600, writable: true })
    const vv = window.visualViewport as any
    let resizeHandler: (() => void) | undefined
    vv.addEventListener = vi.fn((type: string, handler: () => void) => {
      if (type === 'resize') resizeHandler = handler
    })

    await render({ position: 'top' })
    const overlay = container.querySelector('.app-omnibox-container') as HTMLElement
    const topBefore = parseInt(overlay.style.top, 10)

    Object.defineProperty(window, 'visualViewport', {
      value: { ...vv, height: 200 },
      writable: true,
      configurable: true,
    })
    await act(async () => {
      resizeHandler?.()
    })

    const topAfter = parseInt(overlay.style.top, 10)
    expect(topAfter).toBeLessThanOrEqual(topBefore)
  })
})
