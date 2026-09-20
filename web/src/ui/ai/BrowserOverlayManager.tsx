import { useCallback, useMemo, useRef, useState } from 'react'
import { BrowserOverlayProvider, BrowserOverlayOpenProvider } from './browserOverlay'
import { hideAllBrowserWindows, showAllBrowserWindows } from '../../application/wails-bridge'

export function BrowserOverlayManager({ children }: { children: React.ReactNode }) {
  const countRef = useRef(0)
  const preHideRef = useRef<(() => Promise<void>) | null>(null)
  const [, setTick] = useState(0)

  const pushOverlay = useCallback(() => {
    countRef.current++
    if (countRef.current === 1) {
      // Run the pre-hide capture first so screenshots are taken while the
      // browser windows are still visible; hide only after it settles.
      const capture = preHideRef.current
      if (capture) {
        void capture()
          .catch(() => {})
          .finally(() => {
            // The capture can outlive a quick open→close cycle: all overlays
            // may have been popped (windows re-shown) before it settles.
            // Hiding then would strand hidden windows with no overlay open.
            if (countRef.current > 0) {
              void hideAllBrowserWindows()
            }
          })
      } else {
        void hideAllBrowserWindows()
      }
    }
    setTick((t) => t + 1)
  }, [])

  const popOverlay = useCallback(() => {
    countRef.current = Math.max(0, countRef.current - 1)
    if (countRef.current === 0) {
      void showAllBrowserWindows()
    }
    setTick((t) => t + 1)
  }, [])

  const setPreHideCapture = useCallback((fn: (() => Promise<void>) | null) => {
    preHideRef.current = fn
  }, [])

  // Keep the push/pop API object stable so useBrowserOverlay effects are not
  // invalidated when the overlay count changes.
  const api = useMemo(() => ({ pushOverlay, popOverlay, setPreHideCapture }), [pushOverlay, popOverlay, setPreHideCapture])
  const overlayOpen = countRef.current > 0

  return (
    <BrowserOverlayProvider value={api}>
      <BrowserOverlayOpenProvider value={overlayOpen}>
        {children}
      </BrowserOverlayOpenProvider>
    </BrowserOverlayProvider>
  )
}
