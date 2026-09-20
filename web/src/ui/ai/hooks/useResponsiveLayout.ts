import { useEffect, useState, useRef, useCallback } from 'react'
import type { ViewMode } from '../model/ai-types'

export interface ResponsiveLayoutResult {
  viewMode: ViewMode
  containerRef: React.RefCallback<HTMLElement>
}

function widthToMode(w: number): ViewMode {
  if (w < 768) return 'mobile'
  return 'desktop'
}

export function useResponsiveLayout(): ResponsiveLayoutResult {
  const containerElRef = useRef<HTMLElement | null>(null)
  const [viewMode, setViewMode] = useState<ViewMode>('desktop')

  // ResizeObserver
  const containerRef = useCallback((el: HTMLElement | null) => {
    containerElRef.current = el
    if (!el) return

    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const w = entry.contentBoxSize?.[0]?.inlineSize ?? entry.contentRect.width
        setViewMode(widthToMode(w))
      }
    })
    ro.observe(el)
    setViewMode(widthToMode(el.offsetWidth))

    ;(el as any).__resizeObserver = ro
  }, [])

  // Cleanup observer on unmount
  useEffect(() => {
    return () => {
      const el = containerElRef.current
      if (el) {
        const ro = (el as any).__resizeObserver as ResizeObserver | undefined
        ro?.disconnect()
      }
    }
  }, [])

  return {
    viewMode,
    containerRef,
  }
}
