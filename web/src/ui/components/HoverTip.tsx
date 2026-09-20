import { useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

/**
 * Shared hover-tooltip anchor: portals a fixed-position tip to <body> so it
 * escapes scrollable panels and modal layering. Consumers live inside host
 * HTML (no native browser window is visible there), so no useBrowserOverlay
 * registration is needed.
 */
export function HoverTip({ testId, disabled, className, children, tip }: {
  testId?: string
  disabled?: boolean
  className?: string
  children: ReactNode
  tip: ReactNode
}) {
  const [show, setShow] = useState(false)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  const anchorRef = useRef<HTMLSpanElement>(null)

  const open = () => {
    if (disabled) return
    const rect = anchorRef.current?.getBoundingClientRect()
    if (!rect) return
    const width = 300
    setPos({ left: Math.min(rect.left, window.innerWidth - width - 8), top: rect.bottom + 6 })
    setShow(true)
  }

  return (
    <span
      ref={anchorRef}
      className={className}
      onMouseEnter={open}
      onMouseLeave={() => setShow(false)}
    >
      {children}
      {show && !disabled && pos && createPortal(
        <div className="plugin-schema-tip" style={{ left: pos.left, top: pos.top }} data-testid={testId}>
          {tip}
        </div>,
        document.body,
      )}
    </span>
  )
}
