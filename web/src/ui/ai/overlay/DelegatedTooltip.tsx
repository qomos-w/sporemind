import React, {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { createPortal } from 'react-dom'
import { useI18n } from '../../../i18n'
import { guideManager } from '../../../application/guide-manager'
import { tooltipRegistry, type GuideId } from '../guide-ids'
import { useBrowserOverlay } from '../browserOverlay'
import { Spotlight } from './Spotlight'
import { useAnchorRect } from './anchor'
import { computePlacement, type Rect, type Size, type Viewport } from './placement'
import './DelegatedTooltip.css'

type TooltipMode = 'plain' | 'spotlight'
type TooltipSide = 'top' | 'bottom' | 'left' | 'right' | 'auto'

interface TooltipState {
  key: string
  content: string
  mode: TooltipMode
  side: TooltipSide
  target: HTMLElement
}

const DEFAULT_SIDE: TooltipSide = 'bottom'

function useGuideActive(): boolean {
  const [active, setActive] = useState(() => guideManager.getState().visible)
  useEffect(() => {
    return guideManager.subscribe((state) => setActive(state.visible))
  }, [])
  return active
}

function resolveContentKey(el: HTMLElement): string | null {
  const tooltipKey = el.getAttribute('data-tooltip-key')
  if (tooltipKey) return tooltipKey
  const guideId = el.getAttribute('data-guide-id') as GuideId | null
  if (guideId) {
    const mapped = tooltipRegistry[guideId]
    if (mapped) return mapped
  }
  return null
}

function parseMode(value: string | null): TooltipMode {
  if (value === 'spotlight') return 'spotlight'
  return 'plain'
}

function parseSide(value: string | null): TooltipSide {
  switch (value) {
    case 'top':
    case 'bottom':
    case 'left':
    case 'right':
      return value
    default:
      return DEFAULT_SIDE
  }
}

function toRect(domRect: DOMRect): Rect {
  return {
    x: domRect.x,
    y: domRect.y,
    width: domRect.width,
    height: domRect.height,
  }
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

/**
 * Global single-instance delegated tooltip.
 *
 * Attaches document-level mouse/focus listeners and shows a tooltip for any
 * element carrying `data-tooltip-key` or `data-guide-id` (when the guide id is
 * registered in `tooltipRegistry`). Supports `data-tooltip-mode` (`plain` |
 * `spotlight`) and `data-tooltip-side` (`top` | `bottom` | `left` | `right` |
 * `auto`).
 *
 * Spotlight mode reuses the shared `<Spotlight>` component and registers with
 * `BrowserOverlayManager` so native browser windows are hidden while the tooltip
 * is visible. When a guide tour is active, spotlight tooltips are downgraded to
 * plain (guide > spotlight tooltip > plain tooltip).
 */
export const DelegatedTooltip: React.FC = () => {
  const { t } = useI18n()
  const guideActive = useGuideActive()
  const [state, setState] = useState<TooltipState | null>(null)
  const tooltipRef = useRef<HTMLDivElement>(null)
  const [popupSize, setPopupSize] = useState<Size>({ width: 0, height: 0 })
  const viewport = useViewport()

  const open = state !== null
  useBrowserOverlay(open)

  const effectiveMode =
    guideActive && state?.mode === 'spotlight' ? 'plain' : state?.mode ?? 'plain'

  const show = useCallback(
    (el: HTMLElement) => {
      const key = resolveContentKey(el)
      if (!key) return
      const content = t(key as any)
      if (content === key) return
      const mode = parseMode(el.getAttribute('data-tooltip-mode'))
      const side = parseSide(el.getAttribute('data-tooltip-side'))
      setState((prev) => {
        if (
          prev &&
          prev.target === el &&
          prev.key === key &&
          prev.mode === mode &&
          prev.side === side
        ) {
          return prev
        }
        return { key, content, mode, side, target: el }
      })
    },
    [t]
  )

  const hide = useCallback(() => {
    setState(null)
  }, [])

  const stateRef = useRef(state)
  stateRef.current = state

  useEffect(() => {
    const handleMouseOver = (e: MouseEvent) => {
      const target = e.target as Element | null
      if (!target) return
      const el = target.closest<HTMLElement>('[data-tooltip-key],[data-guide-id]')
      if (!el) return
      show(el)
    }

    const handleMouseOut = (e: MouseEvent) => {
      const current = stateRef.current
      if (!current) return
      const related = e.relatedTarget as Element | null
      if (related && current.target.contains(related)) return
      hide()
    }

    const handleFocusIn = (e: FocusEvent) => {
      const target = e.target as Element | null
      if (!target) return
      const el = target.closest<HTMLElement>('[data-tooltip-key],[data-guide-id]')
      if (!el) return
      show(el)
    }

    const handleFocusOut = (e: FocusEvent) => {
      const current = stateRef.current
      if (!current) return
      const related = e.relatedTarget as Element | null
      if (related && current.target.contains(related)) return
      hide()
    }

    document.addEventListener('mouseover', handleMouseOver, true)
    document.addEventListener('mouseout', handleMouseOut, true)
    document.addEventListener('focusin', handleFocusIn, true)
    document.addEventListener('focusout', handleFocusOut, true)

    return () => {
      document.removeEventListener('mouseover', handleMouseOver, true)
      document.removeEventListener('mouseout', handleMouseOut, true)
      document.removeEventListener('focusin', handleFocusIn, true)
      document.removeEventListener('focusout', handleFocusOut, true)
    }
  }, [show, hide])

  const anchorRect = useAnchorRect(state?.target ?? null)

  useLayoutEffect(() => {
    const el = tooltipRef.current
    if (!el) return
    const measure = (node: Element): Size => {
      const rect = node.getBoundingClientRect()
      return { width: rect.width, height: rect.height }
    }
    let next = measure(el)
    if (next.width === 0 && next.height === 0 && el.firstElementChild) {
      next = measure(el.firstElementChild)
    }
    setPopupSize(next)
  }, [state?.content, state?.key, open])

  const placement = useMemo(() => {
    if (!state || popupSize.width === 0 || popupSize.height === 0) {
      return null
    }
    const rect = toRect(anchorRect)
    const side = state.side === 'auto' ? DEFAULT_SIDE : state.side
    return computePlacement(
      rect,
      popupSize,
      { side, align: 'center', offset: 8, viewportPadding: 8 },
      viewport
    )
  }, [state, anchorRect, popupSize, viewport])

  if (!state) {
    return null
  }

  return createPortal(
    <>
      {effectiveMode === 'spotlight' && placement && (
        <Spotlight open target={toRect(anchorRect)} holePadding={4} holeRadius={6} />
      )}
      <div
        ref={tooltipRef}
        className="delegated-tooltip"
        role="tooltip"
        style={{
          left: placement?.x ?? -9999,
          top: placement?.y ?? -9999,
          visibility: placement ? 'visible' : 'hidden',
        }}
      >
        {state.content}
      </div>
    </>,
    document.body
  )
}
