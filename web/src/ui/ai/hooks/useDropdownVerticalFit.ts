import { useLayoutEffect, type RefObject } from 'react'

/**
 * Keeps a dropdown menu within the vertical viewport.
 *
 * `direction` controls the natural open direction:
 *  - `'up'`   — dropdown uses `bottom: calc(100% + gap)` (opens upward)
 *  - `'down'` — dropdown uses `top: calc(100% + gap)` (opens downward)
 *
 * When the dropdown would overflow the viewport edge it either **flips**
 * to the opposite direction (adding `.fit-down` or `.fit-up`) when more
 * room exists there, or **clamps** `max-height` so content scrolls.
 *
 * Inline `max-height` and the flip class are reset on close.
 */
export function useDropdownVerticalFit(
  open: boolean,
  ref: RefObject<HTMLElement | null>,
  direction: 'up' | 'down' = 'up',
) {
  useLayoutEffect(() => {
    if (!open) return
    const el = ref.current
    if (!el) return

    const MARGIN = 8
    const GAP = 6

    const apply = () => {
      el.style.maxHeight = ''
      el.classList.remove('fit-down', 'fit-up')

      const rect = el.getBoundingClientRect()

      if (direction === 'up') {
        if (rect.top >= MARGIN) return
        const spaceAbove = rect.bottom - MARGIN
        const spaceBelow = window.innerHeight - rect.bottom - GAP - MARGIN
        if (spaceBelow > spaceAbove) {
          el.classList.add('fit-down')
          const r = el.getBoundingClientRect()
          if (r.bottom > window.innerHeight - MARGIN) {
            el.style.maxHeight = `${Math.max(80, window.innerHeight - r.top - MARGIN)}px`
          }
        } else {
          el.style.maxHeight = `${Math.max(80, spaceAbove)}px`
        }
      } else {
        if (rect.bottom <= window.innerHeight - MARGIN) return
        const spaceBelow = window.innerHeight - rect.top - MARGIN
        const spaceAbove = rect.top - GAP - MARGIN
        if (spaceAbove > spaceBelow) {
          el.classList.add('fit-up')
          const r = el.getBoundingClientRect()
          if (r.top < MARGIN) {
            el.style.maxHeight = `${Math.max(80, r.bottom - MARGIN)}px`
          }
        } else {
          el.style.maxHeight = `${Math.max(80, spaceBelow)}px`
        }
      }
    }

    apply()
    window.addEventListener('resize', apply)
    const vv = window.visualViewport
    if (vv) {
      vv.addEventListener('resize', apply)
      vv.addEventListener('scroll', apply)
    }
    return () => {
      window.removeEventListener('resize', apply)
      if (vv) {
        vv.removeEventListener('resize', apply)
        vv.removeEventListener('scroll', apply)
      }
      el.style.maxHeight = ''
      el.classList.remove('fit-down', 'fit-up')
    }
  }, [open, ref, direction])
}
