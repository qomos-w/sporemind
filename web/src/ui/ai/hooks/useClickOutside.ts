import { useEffect, useRef, type RefObject } from 'react'

/**
 * Invoke `handler` when a pointer-down occurs outside the referenced element.
 * Pass `enabled=false` to keep the listener detached while a popover is closed.
 */
export function useClickOutside<T extends HTMLElement>(
  ref: RefObject<T | null>,
  handler: () => void,
  enabled = true,
) {
  const handlerRef = useRef(handler)
  handlerRef.current = handler
  useEffect(() => {
    if (!enabled) return
    function onDown(e: MouseEvent) {
      const el = ref.current
      if (!el || el.contains(e.target as Node)) return
      handlerRef.current()
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [ref, enabled])
}
