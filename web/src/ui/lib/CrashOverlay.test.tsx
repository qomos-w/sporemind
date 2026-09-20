import { act } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { CrashOverlay } from './CrashOverlay'
import { BrowserOverlayManager } from '../ai/BrowserOverlayManager'
import { hideAllBrowserWindows, showAllBrowserWindows } from '../../application/wails-bridge'

// These tests pin the crash-recovery overlay wiring: while either crash path
// (live event or boot report) is open the overlay must be registered with the
// BrowserOverlayManager so the native browser child windows are hidden, and it
// must be released when the overlay closes.

const hoisted = vi.hoisted(() => ({
  crashHandler: null as ((...args: unknown[]) => void) | null,
  bootReport: null as unknown,
  acked: [] as string[],
}))

vi.mock('../../application/wails-runtime', () => ({
  isWailsAvailable: () => true,
  onWailsEvent: (_name: string, cb: (...args: unknown[]) => void) => {
    hoisted.crashHandler = cb
    return () => {
      hoisted.crashHandler = null
    }
  },
}))

vi.mock('../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  BootCrashReport: () => Promise.resolve(hoisted.bootReport),
  AckCrashRecords: (ids: string[]) => {
    hoisted.acked.push(...ids)
    return Promise.resolve()
  },
}))

vi.mock('../../application/wails-bridge', () => ({
  hideAllBrowserWindows: vi.fn(() => Promise.resolve()),
  showAllBrowserWindows: vi.fn(() => Promise.resolve()),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const bootReport = {
  abnormalExit: true,
  records: [{ id: 'rec-1', kind: 'panic', summary: 'boom' }],
  errorsTail: [],
  logTail: [],
}

let container: HTMLDivElement
let root: Root | null

beforeEach(() => {
  vi.clearAllMocks()
  hoisted.crashHandler = null
  hoisted.bootReport = null
  hoisted.acked = []
  container = document.createElement('div')
  document.body.appendChild(container)
})

afterEach(() => {
  act(() => {
    root?.unmount()
  })
  root = null
  container.remove()
})

async function render() {
  root = createRoot(container)
  await act(async () => {
    root!.render(
      <BrowserOverlayManager>
        <CrashOverlay />
      </BrowserOverlayManager>,
    )
  })
  // Flush the async BootCrashReport resolution and the ensuing render.
  await act(async () => {})
}

describe('CrashOverlay browser-overlay wiring', () => {
  it('registers an overlay while the boot crash report is open and releases it on dismiss', async () => {
    hoisted.bootReport = bootReport
    await render()

    expect(container.textContent).toContain('Previous session crashed')
    expect(hideAllBrowserWindows).toHaveBeenCalledTimes(1)
    expect(showAllBrowserWindows).not.toHaveBeenCalled()

    const dismiss = Array.from(container.querySelectorAll('button')).find((b) => b.textContent === 'Dismiss')
    expect(dismiss).toBeTruthy()
    await act(async () => {
      dismiss!.click()
    })
    await act(async () => {})

    expect(hoisted.acked).toContain('rec-1')
    expect(showAllBrowserWindows).toHaveBeenCalledTimes(1)
  })

  it('registers an overlay when the live crash event arrives', async () => {
    await render()
    expect(hideAllBrowserWindows).not.toHaveBeenCalled()

    await act(async () => {
      hoisted.crashHandler?.({ error: 'boom', time: 't', stack: 's' })
    })

    expect(container.textContent).toContain('Application Crash')
    expect(hideAllBrowserWindows).toHaveBeenCalledTimes(1)
  })

  it('releases the overlay when unmounted with a live crash open', async () => {
    await render()
    await act(async () => {
      hoisted.crashHandler?.({ error: 'boom', time: 't', stack: 's' })
    })
    expect(hideAllBrowserWindows).toHaveBeenCalledTimes(1)

    await act(async () => {
      root!.unmount()
    })
    root = null

    expect(showAllBrowserWindows).toHaveBeenCalledTimes(1)
  })

  it('registers nothing when no crash is present', async () => {
    await render()
    expect(hideAllBrowserWindows).not.toHaveBeenCalled()
    expect(showAllBrowserWindows).not.toHaveBeenCalled()
  })
})
