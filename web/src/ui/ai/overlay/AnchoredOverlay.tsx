import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { createPortal } from 'react-dom'
import { resolveAnchor, useAnchorRect } from './anchor'
import { computePlacement, unionRect, type Rect, type Size, type Viewport } from './placement'
import { useBrowserOverlay } from '../browserOverlay'
import './overlay.css'

export interface AnchoredOverlayProps {
  /** Resolver string for the anchor element. */
  anchor: string
  /** Preferred side of the anchor. */
  side: 'top' | 'bottom' | 'left' | 'right'
  /** Alignment along the side. */
  align?: 'start' | 'center' | 'end'
  /** Gap between anchor and overlay. */
  offset?: number
  /** Viewport padding for placement clamping. */
  viewportPadding?: number
  /** Whether the overlay is visible. */
  open: boolean
  /** Content rendered inside the overlay. */
  children: React.ReactNode
  /** Called when anchor resolution times out. */
  onAnchorMissing?: () => void
  /**
   * Rect used for placement when the anchor is unresolved or has a
   * zero-size rect (e.g. it lives inside a collapsed container). Pass a
   * viewport rect to keep the card somewhere readable instead of the
   * top-left corner that a zero rect would clamp to.
   */
  fallbackRect?: Rect | null
  /**
   * Transient rect to additionally clear — e.g. the dropdown menu that
   * expands from the anchor while a gated step waits for a click inside
   * it. The anchor rect is grown to the union of both before placement,
   * so the overlay parks beside the anchor+menu instead of underneath
   * the menu. Live updates reposition automatically.
   */
  avoidRect?: Rect | null
  /** Optional className on the positioned wrapper. */
  className?: string
}

function makeEmptyRect(): Rect {
  return { x: 0, y: 0, width: 0, height: 0 }
}

function useViewport(): Viewport {
  const [viewport, setViewport] = useState<Viewport>(() => ({
    width: window.innerWidth,
    height: window.innerHeight,
  }))

  useEffect(() => {
    const handle = () => {
      setViewport({ width: window.innerWidth, height: window.innerHeight })
    }
    window.addEventListener('resize', handle)
    return () => window.removeEventListener('resize', handle)
  }, [])

  return viewport
}

export function AnchoredOverlay({
  anchor,
  side,
  align = 'center',
  offset = 0,
  viewportPadding = 0,
  open,
  children,
  onAnchorMissing,
  fallbackRect,
  avoidRect,
  className,
}: AnchoredOverlayProps) {
  useBrowserOverlay(open)
  const [target, setTarget] = useState<HTMLElement | null>(null)
  const anchorRect = useAnchorRect(target)
  const popupRef = useRef<HTMLDivElement>(null)
  const [popupSize, setPopupSize] = useState<Size>({ width: 0, height: 0 })
  const viewport = useViewport()

  useEffect(() => {
    if (!open) {
      setTarget(null)
      return
    }

    let cancelled = false
    let retry: ReturnType<typeof setInterval> | null = null
    let missingReported = false

    const attempt = () => {
      resolveAnchor(anchor).then((el) => {
        if (cancelled) return
        if (el) {
          if (retry) {
            clearInterval(retry)
            retry = null
          }
          setTarget(el)
          return
        }
        setTarget(null)
        if (!missingReported) {
          missingReported = true
          onAnchorMissing?.()
        }
        // The anchor may appear later (e.g. the user expands a collapsed
        // container); keep retrying cheaply until it does.
        if (!retry) {
          retry = setInterval(attempt, 2000)
        }
      })
    }
    attempt()

    return () => {
      cancelled = true
      if (retry) {
        clearInterval(retry)
      }
    }
  }, [open, anchor, onAnchorMissing])

  useLayoutEffect(() => {
    const el = popupRef.current
    if (!el) return
    const measure = (node: Element): Size => {
      const rect = node.getBoundingClientRect()
      return { width: rect.width, height: rect.height }
    }
    let next = measure(el)
    // Some test environments (happy-dom) do not lay out parent boxes, so fall
    // back to the first child when the wrapper reports zero size.
    if (next.width === 0 && next.height === 0 && el.firstElementChild) {
      next = measure(el.firstElementChild)
    }
    setPopupSize(next)
  }, [children, open])

  const placement = useMemo(() => {
    if (!open || popupSize.width === 0 || popupSize.height === 0) {
      return null
    }
    let rect: Rect
    if (anchorRect.width === 0 && anchorRect.height === 0) {
      rect = fallbackRect ?? makeEmptyRect()
    } else {
      rect = {
        x: anchorRect.x,
        y: anchorRect.y,
        width: anchorRect.width,
        height: anchorRect.height,
      }
    }
    if (avoidRect && (avoidRect.width > 0 || avoidRect.height > 0)) {
      rect = unionRect(rect, avoidRect)
    }
    return computePlacement(
      rect,
      popupSize,
      { side, align, offset, viewportPadding },
      viewport
    )
  }, [open, anchorRect, popupSize, side, align, offset, viewportPadding, viewport, fallbackRect, avoidRect])

  if (!open) {
    return null
  }

  return createPortal(
    <div
      ref={popupRef}
      className={['anchored-overlay', className].filter(Boolean).join(' ')}
      style={{
        position: 'fixed',
        left: placement?.x ?? 0,
        top: placement?.y ?? 0,
      }}
    >
      {children}
    </div>,
    document.body
  )
}
