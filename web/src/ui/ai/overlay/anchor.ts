import { useCallback, useEffect, useRef, useState } from 'react'

const PREFIX_ATTR = {
  guide: 'data-guide-id',
  card: 'data-card-id',
  slot: 'data-slot-id',
  envelope: 'data-envelope-id',
} as const

const VIRTUAL_PLACEHOLDER_ATTR = 'data-vp-id'

const DEFAULT_TIMEOUT_MS = 4000

/**
 * Find the first element carrying `attr` whose exact attribute value equals
 * `id`. This avoids building a CSS attribute selector, which would require
 * escaping quotes/backslashes and is fragile across DOM implementations.
 */
function findByAttr(attr: string, id: string): HTMLElement | null {
  for (const el of document.querySelectorAll<HTMLElement>(`[${attr}]`)) {
    if (el.getAttribute(attr) === id) return el
  }
  return null
}

/**
 * Reject selectors that would change the matching scope in dangerous ways.
 * A leading combinator (`>`, `+`, `~`) would make the selector relative to
 * an arbitrary context node rather than the document root.
 */
function isSafeSelector(selector: string): boolean {
  const trimmed = selector.trimStart()
  if (trimmed.length === 0) return false
  if (/^[>+~]/.test(trimmed)) return false
  return true
}

function isAnchorPrefix(prefix: string): prefix is keyof typeof PREFIX_ATTR {
  return prefix in PREFIX_ATTR
}

/**
 * Resolves an anchor target to a DOM element.
 *
 * Supported target forms:
 * - `guide:<id>`  -> `[data-guide-id="<id>"]`
 * - `card:<id>`   -> `[data-card-id="<id>"]`
 * - `slot:<id>`   -> `[data-slot-id="<id>"]`
 * - `envelope:<id>` -> `[data-envelope-id="<id>"]`
 * - Any other string is treated as a CSS selector and passed to
 *   `document.querySelector` after a safety check.
 *
 * If the element is not present, the function waits for it to be mounted
 * using a `MutationObserver` and resolves with `null` after the timeout.
 *
 * For virtualized `card` targets, when the real card is absent but a
 * `[data-vp-id]` placeholder with the same id exists, the placeholder is
 * scrolled into view and resolution continues to wait for the real card.
 */
interface PrefixTarget {
  prefix: keyof typeof PREFIX_ATTR
  id: string
}

function parseAnchorTarget(target: string): PrefixTarget | { kind: 'selector'; selector: string } {
  const firstColon = target.indexOf(':')
  if (firstColon > 0) {
    const rawPrefix = target.slice(0, firstColon)
    const id = target.slice(firstColon + 1)
    if (id && isAnchorPrefix(rawPrefix)) {
      return { prefix: rawPrefix, id }
    }
  }
  return { kind: 'selector', selector: target }
}

function querySelectorSafe(selector: string): HTMLElement | null {
  try {
    return document.querySelector<HTMLElement>(selector)
  } catch {
    return null
  }
}

function resolvePrefixTarget({ prefix, id }: PrefixTarget): HTMLElement | null {
  const attr = PREFIX_ATTR[prefix]
  const el = findByAttr(attr, id)
  if (el) return el

  if (prefix === 'card') {
    const placeholder = findByAttr(VIRTUAL_PLACEHOLDER_ATTR, id)
    if (placeholder) {
      placeholder.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    }
  }
  return null
}

function waitForElement(
  tryResolve: () => HTMLElement | null,
  timeoutMs: number,
): Promise<HTMLElement | null> {
  return new Promise<HTMLElement | null>((resolve) => {
    let observer: MutationObserver | null = null
    let timer: ReturnType<typeof setTimeout> | null = null

    function cleanup() {
      if (timer) {
        clearTimeout(timer)
        timer = null
      }
      if (observer) {
        observer.disconnect()
        observer = null
      }
    }

    const found = tryResolve()
    if (found) {
      resolve(found)
      return
    }

    observer = new MutationObserver(() => {
      const el = tryResolve()
      if (el) {
        cleanup()
        resolve(el)
      }
    })

    observer.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
    })

    timer = setTimeout(() => {
      cleanup()
      resolve(null)
    }, timeoutMs)
  })
}

export function resolveAnchor(
  target: string,
  timeoutMs = DEFAULT_TIMEOUT_MS,
): Promise<HTMLElement | null> {
  const parsed = parseAnchorTarget(target)

  if ('prefix' in parsed) {
    const found = resolvePrefixTarget(parsed)
    if (found) return Promise.resolve(found)
    return waitForElement(() => resolvePrefixTarget(parsed), timeoutMs)
  }

  if (!isSafeSelector(parsed.selector)) return Promise.resolve(null)
  const found = querySelectorSafe(parsed.selector)
  if (found) return Promise.resolve(found)
  return waitForElement(() => querySelectorSafe(parsed.selector), timeoutMs)
}

function makeEmptyRect(): DOMRect {
  return new DOMRect()
}

function rectEqual(a: DOMRect, b: DOMRect): boolean {
  return a.left === b.left && a.top === b.top && a.width === b.width && a.height === b.height
}

/**
 * Tracks the geometry of an anchor element.
 *
 * Observes the element itself plus every ancestor with a single
 * `ResizeObserver`, and attaches capture-phase scroll listeners to every
 * ancestor and the window. All geometry changes are batched into one
 * `requestAnimationFrame` tick to avoid multiple re-renders per frame.
 *
 * The returned `DOMRect` is in viewport coordinates (`getBoundingClientRect()`)
 * and updates whenever the element resizes, scrolls, or the window resizes.
 *
 * All observers and listeners are torn down when the element changes or the
 * hook unmounts to prevent leaks and stale callbacks.
 */
export function useAnchorRect(el: HTMLElement | null): DOMRect {
  const [rect, setRect] = useState<DOMRect>(() => makeEmptyRect())
  const rafRef = useRef<number | null>(null)
  const roRef = useRef<ResizeObserver | null>(null)
  const lastRectRef = useRef<DOMRect | null>(null)

  const tick = useCallback(() => {
    rafRef.current = null
    if (!el) return
    const next = el.getBoundingClientRect()
    const prev = lastRectRef.current
    if (!prev || !rectEqual(prev, next)) {
      lastRectRef.current = next
      setRect(next)
    }
  }, [el])

  const schedule = useCallback(() => {
    if (rafRef.current !== null) return
    rafRef.current = requestAnimationFrame(tick)
  }, [tick])

  useEffect(() => {
    if (!el) {
      setRect(makeEmptyRect())
      lastRectRef.current = null
      return
    }

    const initial = el.getBoundingClientRect()
    lastRectRef.current = initial
    setRect(initial)

    const observed: Element[] = []
    let cur: Element | null = el
    while (cur) {
      observed.push(cur)
      cur = cur.parentElement
    }

    roRef.current = new ResizeObserver(() => schedule())
    for (const node of observed) {
      roRef.current.observe(node)
    }

    const ancestors = observed.slice(1)
    const handleScroll = () => schedule()
    for (const node of ancestors) {
      node.addEventListener('scroll', handleScroll, { passive: true, capture: true })
    }
    window.addEventListener('resize', handleScroll)
    window.addEventListener('scroll', handleScroll, { passive: true, capture: true })

    return () => {
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current)
        rafRef.current = null
      }
      roRef.current?.disconnect()
      roRef.current = null
      for (const node of ancestors) {
        node.removeEventListener('scroll', handleScroll, { capture: true })
      }
      window.removeEventListener('resize', handleScroll)
      window.removeEventListener('scroll', handleScroll, { capture: true })
    }
  }, [el, schedule, tick])

  return rect
}
