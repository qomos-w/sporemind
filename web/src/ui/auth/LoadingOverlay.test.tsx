import { act } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { LoadingOverlay, type OverlayState } from './LoadingOverlay'

// Pins the LoadingOverlay browser-overlay wiring (G2): the full-screen
// loading/error layers must register with the overlay manager so native browser
// child windows are hidden behind them; the transparent, non-occluding
// reconnecting layer and the idle state must not.

const hoisted = vi.hoisted(() => ({ useBrowserOverlay: vi.fn() }))

vi.mock('../ai/browserOverlay', () => ({ useBrowserOverlay: hoisted.useBrowserOverlay }))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  vi.clearAllMocks()
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => {
    root.unmount()
  })
  container.remove()
})

async function render(state: OverlayState) {
  await act(async () => {
    root.render(<LoadingOverlay state={state} />)
  })
}

describe('LoadingOverlay browser-overlay wiring', () => {
  it('registers for the loading state', async () => {
    await render('loading')
    expect(hoisted.useBrowserOverlay).toHaveBeenLastCalledWith(true)
  })

  it('registers for the error state', async () => {
    await render('error')
    expect(hoisted.useBrowserOverlay).toHaveBeenLastCalledWith(true)
  })

  it('does not register for the transparent reconnecting state', async () => {
    await render('reconnecting')
    expect(hoisted.useBrowserOverlay).toHaveBeenLastCalledWith(false)
  })

  it('does not register for the idle state', async () => {
    await render('idle')
    expect(hoisted.useBrowserOverlay).toHaveBeenLastCalledWith(false)
  })

  it('drops the registration when loading transitions to idle', async () => {
    await render('loading')
    expect(hoisted.useBrowserOverlay).toHaveBeenLastCalledWith(true)
    await render('idle')
    expect(hoisted.useBrowserOverlay).toHaveBeenLastCalledWith(false)
  })
})
