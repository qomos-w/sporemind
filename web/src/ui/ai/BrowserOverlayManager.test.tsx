import React, { act } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { BrowserOverlayManager } from './BrowserOverlayManager'
import { useBrowserOverlay, useBrowserPreHideCapture, decodeSnapshotImage, afterNextPaint } from './browserOverlay'
import { hideAllBrowserWindows, showAllBrowserWindows } from '../../application/wails-bridge'

vi.mock('../../application/wails-bridge', () => ({
  hideAllBrowserWindows: vi.fn(() => Promise.resolve()),
  showAllBrowserWindows: vi.fn(() => Promise.resolve()),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root

const OverlayProbe: React.FC<{ open: boolean }> = ({ open }) => {
  useBrowserOverlay(open)
  return null
}

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

async function renderTree(children: React.ReactNode) {
  await act(async () => {
    root.render(<BrowserOverlayManager>{children}</BrowserOverlayManager>)
  })
}

describe('BrowserOverlayManager', () => {
  it('runs the pre-hide capture before hiding, and hides only after it settles', async () => {
    const order: string[] = []
    ;(hideAllBrowserWindows as ReturnType<typeof vi.fn>).mockImplementation(async () => {
      order.push('hide')
    })
    const Harness: React.FC = () => {
      useBrowserPreHideCapture(async () => {
        order.push('capture')
      })
      useBrowserOverlay(true)
      return null
    }
    await renderTree(<Harness />)
    await act(async () => {})

    expect(order).toEqual(['capture', 'hide'])
  })

  it('hides immediately when no pre-hide capture is registered', async () => {
    await renderTree(<OverlayProbe open />)
    expect(hideAllBrowserWindows).toHaveBeenCalledTimes(1)
    expect(showAllBrowserWindows).not.toHaveBeenCalled()
  })

  it('still hides when the pre-hide capture rejects', async () => {
    const Harness: React.FC = () => {
      useBrowserPreHideCapture(async () => {
        throw new Error('capture failed')
      })
      useBrowserOverlay(true)
      return null
    }
    await renderTree(<Harness />)
    await act(async () => {})
    expect(hideAllBrowserWindows).toHaveBeenCalledTimes(1)
  })

  it('restores windows when the last overlay closes', async () => {
    await renderTree(<OverlayProbe open />)
    expect(showAllBrowserWindows).not.toHaveBeenCalled()
    await renderTree(<OverlayProbe open={false} />)
    expect(showAllBrowserWindows).toHaveBeenCalledTimes(1)
  })

  it('does not re-hide windows when the capture outlives the overlay', async () => {
    let settleCapture: (() => void) | null = null
    const Harness: React.FC<{ open: boolean }> = ({ open }) => {
      useBrowserPreHideCapture(() => new Promise<void>((resolve) => { settleCapture = resolve }))
      useBrowserOverlay(open)
      return null
    }
    await renderTree(<Harness open />)
    await renderTree(<Harness open={false} />)
    expect(showAllBrowserWindows).toHaveBeenCalledTimes(1)

    // The slow capture settles after everything already popped: hiding now
    // would strand the browser windows with no overlay open.
    await act(async () => { settleCapture?.() })
    expect(hideAllBrowserWindows).not.toHaveBeenCalled()
    expect(showAllBrowserWindows).toHaveBeenCalledTimes(1)
  })

  it('does not hide again for nested overlays and only shows after the last pop', async () => {
    await renderTree(<><OverlayProbe open /><OverlayProbe open /></>)
    expect(hideAllBrowserWindows).toHaveBeenCalledTimes(1)
    expect(showAllBrowserWindows).not.toHaveBeenCalled()

    await renderTree(<><OverlayProbe open={false} /><OverlayProbe open /></>)
    expect(showAllBrowserWindows).not.toHaveBeenCalled()

    await renderTree(<><OverlayProbe open={false} /><OverlayProbe open={false} /></>)
    expect(hideAllBrowserWindows).toHaveBeenCalledTimes(1)
    expect(showAllBrowserWindows).toHaveBeenCalledTimes(1)
  })
})

describe('decodeSnapshotImage', () => {
  const realImage = globalThis.Image

  afterEach(() => {
    ;(globalThis as any).Image = realImage
    vi.unstubAllGlobals()
  })

  it('sets the src and resolves once decode() resolves', async () => {
    const decode = vi.fn(async () => {})
    class FakeImage {
      src = ''
      decode = decode
    }
    ;(globalThis as any).Image = FakeImage

    await decodeSnapshotImage('data:image/png;base64,xyz')

    expect(decode).toHaveBeenCalledTimes(1)
    const img = (decode.mock.contexts?.[0] ?? decode.mock.instances?.[0]) as FakeImage | undefined
    expect(img?.src).toBe('data:image/png;base64,xyz')
  })

  it('swallows decode failures so the hide still proceeds', async () => {
    class FakeImage {
      src = ''
      decode = async () => { throw new Error('decode failed') }
    }
    ;(globalThis as any).Image = FakeImage

    await expect(decodeSnapshotImage('data:image/png;base64,xyz')).resolves.toBeUndefined()
  })
})

describe('afterNextPaint', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('resolves only after two animation frames (commit + paint)', async () => {
    const pending: Array<() => void> = []
    const raf = vi.fn((cb: FrameRequestCallback) => {
      pending.push(() => cb(0))
      return pending.length
    })
    vi.stubGlobal('requestAnimationFrame', raf)

    let settled = false
    const p = afterNextPaint(1000).then(() => { settled = true })

    expect(settled).toBe(false)
    pending.splice(0).forEach((run) => run())
    expect(settled).toBe(false)
    pending.splice(0).forEach((run) => run())
    await p
    expect(settled).toBe(true)
  })

  it('falls back to the timeout when rAF never fires', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('requestAnimationFrame', vi.fn(() => 1))

    let settled = false
    const p = afterNextPaint(150).then(() => { settled = true })

    await vi.advanceTimersByTimeAsync(149)
    expect(settled).toBe(false)
    await vi.advanceTimersByTimeAsync(1)
    await p
    expect(settled).toBe(true)
  })
})
