import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'

/** Envelope-height cache for virtualization placeholders. Keys are scoped by
 *  conversation (`scopeKey#id`) so a height measured in one agent's timeline is
 *  never reused as a placeholder for an envelope with a colliding id in
 *  another agent's timeline — a stale cross-scope placeholder makes the real
 *  content swap change scrollHeight, which the stream's follow logic then
 *  "corrects" with a visible scroll. */
const heightCache = new Map<string, number>()

/** Visibility band, in px beyond the stream viewport. An envelope mounts when
 *  it comes within ENTER and stays mounted until it is beyond EXIT. The band
 *  between the two margins is hysteresis: with a single threshold (the old
 *  one-margin observer) an element sitting at the boundary flips
 *  mounted/placeholder on every 1px geometry change, and each swap changes
 *  scrollHeight, feeding the stream's ResizeObserver another growth tick —
 *  the idle scroll churn. */
const ENTER_MARGIN_PX = 1000
const EXIT_MARGIN_PX = 1200

/** The last tail card inside the content column — the element smooth-follow
 * counter-shifts to stay pinned at the bottom. */
function pinnedTailEl(content: HTMLElement | null): HTMLElement | null {
  if (!content) return null
  const tails = content.querySelectorAll<HTMLElement>('.ai-turn-tail')
  return tails.length > 0 ? tails[tails.length - 1]! : null
}

interface ObserverHandle {
  enterObserver: IntersectionObserver | null
  exitObserver: IntersectionObserver | null
  items: Map<string, React.Dispatch<React.SetStateAction<boolean>>>
  scopeKey: string
}

const ViewportObserverCtx = createContext<ObserverHandle | null>(null)

export function ViewportObserverProvider({
  scrollContainerRef,
  scopeKey,
  children,
}: {
  scrollContainerRef: React.RefObject<HTMLElement | null>
  /** Conversation identity (agentActorId): scopes the placeholder height cache. */
  scopeKey?: string
  children: ReactNode
}) {
  const [handle] = useState<ObserverHandle>(() => ({ enterObserver: null, exitObserver: null, items: new Map(), scopeKey: '' }))

  // useLayoutEffect (not useEffect) so the observer exists before children's
  // useEffect runs — React fires child passive effects before parent's, so
  // creating the observer in useEffect would leave it null when children try
  // to register.
  useLayoutEffect(() => {
    const root = scrollContainerRef.current
    if (!root) return

    handle.enterObserver = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (!e.isIntersecting) continue
          const id = (e.target as HTMLElement).dataset.vpId
          if (!id) continue
          handle.items.get(id)?.(true)
        }
      },
      { root, rootMargin: `${ENTER_MARGIN_PX}px 0px`, threshold: 0 },
    )
    handle.exitObserver = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) continue
          const id = (e.target as HTMLElement).dataset.vpId
          if (!id) continue
          handle.items.get(id)?.(false)
        }
      },
      { root, rootMargin: `${EXIT_MARGIN_PX}px 0px`, threshold: 0 },
    )

    return () => {
      handle.enterObserver!.disconnect()
      handle.exitObserver!.disconnect()
      handle.enterObserver = null
      handle.exitObserver = null
      handle.items.clear()
    }
  }, [scrollContainerRef, handle])

  // Keep the scope live in the handle without tearing the observers down on a
  // scope switch; slots pick up the new cache keys on their next render.
  useLayoutEffect(() => {
    handle.scopeKey = scopeKey ?? ''
  }, [scopeKey, handle])

  return (
    <ViewportObserverCtx.Provider value={handle}>
      {children}
    </ViewportObserverCtx.Provider>
  )
}

function heightCacheKey(scopeKey: string, id: string): string {
  return scopeKey ? `${scopeKey}#${id}` : id
}

export function useViewportSlot(id: string, alwaysVisible?: boolean) {
  const handle = useContext(ViewportObserverCtx)
  const [isVisible, setIsVisible] = useState(false)
  const itemRef = useRef<HTMLDivElement>(null)
  const cacheKey = heightCacheKey(handle?.scopeKey ?? '', id)

  // Synchronously check visibility on mount *and* when an active envelope
  // completes. While streaming, MessageStream sets alwaysVisible=true; the
  // terminal turn event flips it to false in the same commit that folds the
  // assistant frames. Rechecking here prevents that transition from using the
  // initial false state until IntersectionObserver catches up after layout.
  //
  // The smooth-follow lag rides as a translate3d on the content column. That
  // transform is purely visual — it must not flip virtualization decisions,
  // or envelopes at the viewport edge flicker between content and cached
  // placeholder as the lag ebbs. Strip it (and the pinned tail counter-shift)
  // for the measurement, then restore.
  useLayoutEffect(() => {
    if (alwaysVisible) {
      setIsVisible(true)
      return
    }
    const el = itemRef.current
    if (!el) return
    const content = el.closest('.ai-message-content') as HTMLElement | null
    const pinnedTail = pinnedTailEl(content)
    const cT = content?.style.transform ?? ''
    const tT = pinnedTail?.style.transform ?? ''
    if (content) content.style.transform = 'none'
    if (pinnedTail) pinnedTail.style.transform = 'none'
    const rect = el.getBoundingClientRect()
    if (content) content.style.transform = cT
    if (pinnedTail) pinnedTail.style.transform = tT
    // Initial classification uses the ENTER margin; inside the hysteresis
    // band the item stays a placeholder until the enter observer fires.
    setIsVisible(rect.bottom > -ENTER_MARGIN_PX && rect.top < window.innerHeight + ENTER_MARGIN_PX)
  }, [alwaysVisible])

  useEffect(() => {
    if (alwaysVisible || !handle?.enterObserver || !handle.exitObserver) return
    const el = itemRef.current
    if (!el) return

    handle.items.set(id, setIsVisible)
    handle.enterObserver.observe(el)
    handle.exitObserver.observe(el)

    return () => {
      handle.items.delete(id)
      handle.enterObserver?.unobserve(el)
      handle.exitObserver?.unobserve(el)
    }
  }, [id, alwaysVisible, handle])

  useEffect(() => {
    const el = itemRef.current
    if (!el || alwaysVisible) return

    if (isVisible) {
      const ro = new ResizeObserver(() => {
        heightCache.set(cacheKey, el.offsetHeight)
      })
      ro.observe(el)
      return () => ro.disconnect()
    }
  }, [cacheKey, isVisible, alwaysVisible])

  return {
    ref: itemRef,
    isVisible: alwaysVisible || isVisible,
    cachedHeight: heightCache.get(cacheKey),
  }
}

export function clearViewportHeightCache() {
  heightCache.clear()
}
