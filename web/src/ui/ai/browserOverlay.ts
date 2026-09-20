import { createContext, useContext, useEffect, useRef } from 'react'

export interface BrowserOverlayApi {
  pushOverlay: () => void
  popOverlay: () => void
  /**
   * Registers a callback the manager invokes once, immediately before hiding
   * the native browser windows on the first overlay opening. Used to capture
   * a screenshot placeholder while the window is still visible; the windows
   * are hidden only after the callback settles.
   */
  setPreHideCapture?: (fn: (() => Promise<void>) | null) => void
}

const BrowserOverlayContext = createContext<BrowserOverlayApi | null>(null)
const BrowserOverlayOpenContext = createContext<boolean>(false)

export const BrowserOverlayProvider = BrowserOverlayContext.Provider
export const BrowserOverlayOpenProvider = BrowserOverlayOpenContext.Provider

/**
 * Registers an overlay (modal, dropdown, context menu) with the browser
 * screenshot-replacement system. When the first overlay opens all active
 * right-panel native browser windows are hidden so HTML overlays are not
 * occluded; when the last overlay closes the windows are restored.
 *
 * Uses a ref-count so overlapping overlays are handled correctly.
 */
export function useBrowserOverlay(open: boolean) {
  const api = useContext(BrowserOverlayContext)
  const pushedRef = useRef(false)

  useEffect(() => {
    if (!api) return
    if (open && !pushedRef.current) {
      api.pushOverlay()
      pushedRef.current = true
    } else if (!open && pushedRef.current) {
      api.popOverlay()
      pushedRef.current = false
    }
    return () => {
      if (pushedRef.current) {
        api.popOverlay()
        pushedRef.current = false
      }
    }
  }, [open, api])
}

/**
 * Returns whether any overlay is currently registered with the unified
 * browser overlay manager. Useful for orchestrating native browser window
 * visibility in ancestor components.
 */
export function useBrowserOverlayOpen(): boolean {
  return useContext(BrowserOverlayOpenContext)
}

/**
 * Pre-decodes a snapshot data URL before it is committed to the tree, so the
 * <img> placeholder paints in the same frame it mounts. Without this the
 * native window is hidden while the browser is still decoding the capture,
 * showing a blank frame (the "snapshot flicker").
 */
export async function decodeSnapshotImage(src: string): Promise<void> {
  try {
    const img = new Image()
    img.src = src
    await img.decode()
  } catch {
    // Non-fatal: the <img> tag will decode at paint time instead.
  }
}

/**
 * Resolves after the next commit+paint window (two animation frames), with a
 * timeout cap so callers never stall when rAF is throttled or suspended
 * (minimized window, occlusion). The native browser window must only be
 * hidden after this resolves, so it is replaced by the already-painted
 * snapshot instead of a blank frame.
 */
export function afterNextPaint(timeoutMs = 150): Promise<void> {
  return new Promise((resolve) => {
    let done = false
    const finish = () => {
      if (done) return
      done = true
      resolve()
    }
    requestAnimationFrame(() => requestAnimationFrame(finish))
    setTimeout(finish, timeoutMs)
  })
}

/**
 * Registers a pre-hide capture callback with the browser overlay manager.
 * The callback runs once before the native browser windows are hidden (when
 * the first overlay opens), while the browser window is still on screen, so
 * a screenshot placeholder can be captured.
 */
export function useBrowserPreHideCapture(capture: () => Promise<void>) {
  const api = useContext(BrowserOverlayContext)
  const captureRef = useRef(capture)
  captureRef.current = capture
  useEffect(() => {
    const register = api?.setPreHideCapture
    if (!register) return
    register(() => captureRef.current())
    return () => register(null)
  }, [api])
}
