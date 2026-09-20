import { useEffect } from 'react'

function isInputElement(el: EventTarget | null): el is HTMLElement {
  if (!el || !(el instanceof HTMLElement)) return false
  return el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable
}

/**
 * Scroll an element into view within its nearest scrollable ancestor.
 * Falls back to document-level scrollIntoView if no scroll parent is found.
 */
function scrollIntoViewInScrollParent(el: Element): void {
  let scrollParent: HTMLElement | null = el.parentElement
  while (scrollParent) {
    const style = window.getComputedStyle(scrollParent)
    if (
      style.overflow === 'auto' || style.overflow === 'scroll' ||
      style.overflowY === 'auto' || style.overflowY === 'scroll'
    ) {
      const elRect = el.getBoundingClientRect()
      const parentRect = scrollParent.getBoundingClientRect()
      const isBelow = elRect.bottom > parentRect.bottom
      const isAbove = elRect.top < parentRect.top
      if (isBelow || isAbove) {
        const targetTop =
          scrollParent.scrollTop +
          elRect.top -
          parentRect.top -
          parentRect.height / 2 +
          elRect.height / 2
        scrollParent.scrollTo({ top: Math.max(0, targetTop), behavior: 'smooth' })
      }
      return
    }
    scrollParent = scrollParent.parentElement
  }
  // No scrollable ancestor found — fall back to document scroll
  el.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
}

/**
 * Capacitor iframe keyboard bridge.
 *
 * - Receives keyboard height from parent via postMessage (sourced from both
 *   Capacitor Keyboard plugin and visualViewport shrinkage) and sets the
 *   --keyboard-height CSS variable.
 * - When the keyboard appears and an input is focused, scrolls the input into
 *   view inside its nearest scrollable ancestor (e.g. .ai-message-stream).
 * - On mount, requests the current keyboard height from parent.
 */
export function useKeyboardHeight(): void {
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      const msg = event.data || {}
      if (msg.type === 'keyboard-height') {
        const h = typeof msg.height === 'number' ? msg.height : 0
        document.documentElement.style.setProperty('--keyboard-height', `${h}px`)
        if (h > 0) {
          const el = document.activeElement
          if (el && isInputElement(el)) {
            setTimeout(() => scrollIntoViewInScrollParent(el), 300)
          }
        }
      }
    }

    window.addEventListener('message', onMessage)

    if (window.parent !== window) {
      window.parent.postMessage({ type: 'request-keyboard-height' }, '*')
    }

    return () => {
      window.removeEventListener('message', onMessage)
      document.documentElement.style.removeProperty('--keyboard-height')
    }
  }, [])
}
