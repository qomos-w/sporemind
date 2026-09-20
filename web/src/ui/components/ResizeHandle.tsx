import React, { useCallback, useRef } from 'react'
import './ResizeHandle.css'

export interface ResizeHandleProps {
  direction: 'horizontal' | 'vertical'
  /** Called with the final size (px) on pointerup. */
  onResize: (size: number) => void
  /** Called with each frame's size while dragging. */
  onResizeLive?: (size: number) => void
  className?: string
  /** Optional ref to the element whose size should be mutated directly during drag.
   *  When set, drag updates the DOM via requestAnimationFrame and onResize is called
   *  once on pointerup with the final absolute size. */
  targetRef?: React.RefObject<HTMLElement | null>
  minSize?: number
  negateDelta?: boolean
  /** Called when a drag starts (pointerdown) and ends (pointerup). */
  onDragStateChange?: (dragging: boolean) => void
}

export const ResizeHandle: React.FC<ResizeHandleProps> = ({
  direction,
  onResize,
  onResizeLive,
  className,
  targetRef,
  minSize = 120,
  negateDelta = false,
  onDragStateChange,
}) => {
  const drag = useRef<{
    startPos: number
    startSize: number
  } | null>(null)
  const rafPending = useRef(false)
  const lastDelta = useRef(0)

  const scheduleDOMUpdate = useCallback(
    (currentPos: number) => {
      if (!drag.current || !targetRef?.current) return
      if (rafPending.current) return
      rafPending.current = true
      requestAnimationFrame(() => {
        rafPending.current = false
        const d = drag.current
        if (!d || !targetRef.current) return
        const delta = negateDelta ? -(currentPos - d.startPos) : currentPos - d.startPos
        lastDelta.current = delta
        const nextSize = Math.max(minSize, d.startSize + delta)
        if (direction === 'horizontal') {
          targetRef.current.style.width = `${nextSize}px`
        } else {
          targetRef.current.style.height = `${nextSize}px`
        }
        onResizeLive?.(nextSize)
      })
    },
    [direction, targetRef, minSize, negateDelta, onResizeLive]
  )

  const onPointerDown = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault()
      e.stopPropagation()
      const el = e.currentTarget
      el.setPointerCapture(e.pointerId)

      const startPos = direction === 'horizontal' ? e.clientX : e.clientY

      if (targetRef?.current) {
        const target = targetRef.current
        const computed = direction === 'horizontal' ? getComputedStyle(target).width : getComputedStyle(target).height
        const startSize = computed.endsWith('px') ? parseFloat(computed) : (direction === 'horizontal' ? target.offsetWidth : target.offsetHeight)
        drag.current = {
          startPos,
          startSize,
        }
        lastDelta.current = 0
        onDragStateChange?.(true)

        const onMove = (ev: PointerEvent) => {
          const current = direction === 'horizontal' ? ev.clientX : ev.clientY
          scheduleDOMUpdate(current)
        }
        const onUp = () => {
          const d = drag.current
          if (d && targetRef?.current) {
            const currentSize = direction === 'horizontal' ? targetRef.current.offsetWidth : targetRef.current.offsetHeight
            onResize(currentSize)
          }
          drag.current = null
          lastDelta.current = 0
          onDragStateChange?.(false)
          document.removeEventListener('pointermove', onMove)
          document.removeEventListener('pointerup', onUp)
          document.removeEventListener('pointercancel', onUp)
        }
        document.addEventListener('pointermove', onMove)
        document.addEventListener('pointerup', onUp)
        document.addEventListener('pointercancel', onUp)
      } else {
        // Fallback delta mode.
        let last = startPos
        const onMove = (ev: PointerEvent) => {
          const current = direction === 'horizontal' ? ev.clientX : ev.clientY
          const delta = current - last
          last = current
          onResize(delta)
        }
        const onUp = () => {
          document.removeEventListener('pointermove', onMove)
          document.removeEventListener('pointerup', onUp)
          document.removeEventListener('pointercancel', onUp)
        }
        document.addEventListener('pointermove', onMove)
        document.addEventListener('pointerup', onUp)
        document.addEventListener('pointercancel', onUp)
      }
    },
    [direction, onResize, targetRef, scheduleDOMUpdate]
  )

  return (
    <div
      className={`resize-handle ${direction} ${className || ''}`}
      onPointerDown={onPointerDown}
    />
  )
}
