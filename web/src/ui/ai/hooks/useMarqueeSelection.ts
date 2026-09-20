import { useEffect, useRef, type RefObject } from 'react'
import './marquee-selection.css'

/**
 * Shared rubber-band (marquee) selection hook for file listings.
 *
 * Consumed by FileBrowser and SshSessionView so the drag-rectangle, hit
 * testing and modifier semantics live in exactly one place. Item rows opt in
 * by matching `itemSelector` and carrying the selection key in a data
 * attribute (default `data-mq-path`).
 *
 * Gesture rules (see the integration cards for wiring details):
 * - Only left button (`e.button === 0`) starts a marquee.
 * - The press must land on "empty" area: not on an item row, not on an
 *   element matching `ignoreSelector`, and not on the container's scrollbar.
 *   Presses on rows are left entirely to the rows' own click / HTML5 drag
 *   handlers, so existing drag-and-drop (`file-dnd.ts`, `os-file-dnd.ts`)
 *   is never hijacked.
 * - `mousedown` is `preventDefault()`-ed only to suppress native text
 *   selection; `contextmenu` is a distinct event and keeps working.
 * - Escape (or window blur) cancels the gesture without firing `onHit`.
 * - The marquee rectangle is a plain absolutely-positioned div inside the
 *   container (never a window-level overlay), so `useBrowserOverlay` is not
 *   involved. No persistence is used (no localStorage/sessionStorage).
 *
 * Modifier semantics (resolved from the modifiers held at mousedown;
 * priority alt > ctrl/meta > shift):
 * - none  → `replace`  : hit set replaces the selection
 * - ctrl/meta → `add`  : union with the selection at gesture start
 * - alt   → `subtract` : hit set is removed from the selection at gesture start
 * - shift → `range`    : anchor-range semantics are component-specific; the
 *   hook reports the mode and leaves resolution to the consumer (treat as
 *   `replace` when no anchor logic exists).
 *
 * When `getSelectedPaths` is supplied, `result.next` carries the fully
 * resolved next selection for replace/add/subtract so consumers can apply it
 * verbatim (`setSelected(new Set(result.next))`); it is `null` for `range`
 * and for add/subtract when no `getSelectedPaths` was provided.
 */

/** Modifier semantics for one finished marquee gesture. */
export type MarqueeSelectionMode = 'replace' | 'add' | 'subtract' | 'range'

/** Raw modifier-key snapshot captured at mousedown. */
export interface MarqueeModifierState {
  ctrl: boolean
  meta: boolean
  shift: boolean
  alt: boolean
}

/** Axis-aligned box in client (viewport) coordinates. */
export interface MarqueeBox {
  left: number
  top: number
  right: number
  bottom: number
}

/** Payload delivered to `onPreview` (during the drag) and `onHit` (at mouseup). */
export interface MarqueeSelectionResult {
  /** Hit paths in DOM order, deduplicated. Empty for an empty-area click. */
  paths: string[]
  /** Modifier semantics resolved from the mousedown modifiers. */
  mode: MarqueeSelectionMode
  /** Raw modifier flags captured at mousedown. */
  modifiers: MarqueeModifierState
  /**
   * True when the pointer never moved beyond `startThreshold` — a plain
   * empty-area click. Combined with `mode: 'replace'` and `paths: []` this
   * naturally means "clear the selection".
   */
  isClick: boolean
  /**
   * Fully resolved next selection for replace/add/subtract (computed against
   * the selection snapshot taken at mousedown). `null` for `range`, and for
   * add/subtract when `getSelectedPaths` was not supplied.
   */
  next: string[] | null
}

export interface MarqueeSelectionOptions {
  /**
   * The element that owns the gesture — ideally the scrolling element that
   * contains the item rows (e.g. `.fb-main-content`, `.ssh-files-list`).
   * The marquee rectangle is appended inside it and absolutely positioned,
   * so the hook promotes `position: static` to `relative` (once, in place).
   */
  containerRef: RefObject<HTMLElement | null>
  /** Selector matching the item rows that participate in hit testing. */
  itemSelector: string
  /**
   * Attribute on item rows carrying the selection key (the "path").
   * Default `'data-mq-path'`. Rows without this attribute are skipped.
   */
  pathAttribute?: string
  /**
   * Elements matching this selector never start a marquee even when they are
   * not item rows (toolbars, headers, separators…). Defaults to interactive
   * controls plus the escape hatch `[data-mq-ignore]`; tag anything else
   * (e.g. `.ssh-files-header`) with `data-mq-ignore` or extend this selector.
   */
  ignoreSelector?: string
  /**
   * Drag distance (px) that must be exceeded before the rectangle becomes
   * visible and previews fire. Default 3 — below it the gesture is a click.
   */
  startThreshold?: number
  /** Master switch; while false no listeners are attached. Default true. */
  enabled?: boolean
  /**
   * Scroll the container when the pointer approaches its edges during the
   * drag. Default true. No-op when the container does not scroll.
   */
  autoScroll?: boolean
  /**
   * Snapshot source for the current selection, read once at mousedown.
   * Enables the resolved `result.next` for add/subtract.
   */
  getSelectedPaths?: () => Iterable<string> | null | undefined
  /** Live feedback while dragging (fires on hit-set changes, after threshold). */
  onPreview?: (result: MarqueeSelectionResult) => void
  /** Called once when the gesture ends without cancellation. */
  onHit: (result: MarqueeSelectionResult) => void
}

const DEFAULT_PATH_ATTRIBUTE = 'data-mq-path'
const DEFAULT_IGNORE_SELECTOR =
  'button, input, select, textarea, a, label, [contenteditable], [role="separator"], [role="menu"], [role="menuitem"], [role="slider"], [data-mq-ignore]'
const DEFAULT_START_THRESHOLD = 3
const AUTOSCROLL_EDGE_PX = 24
const AUTOSCROLL_STEP_PX = 14

interface DragState {
  startClientX: number
  startClientY: number
  curClientX: number
  curClientY: number
  modifiers: MarqueeModifierState
  /** Selection snapshot from `getSelectedPaths` at mousedown; null when unavailable. */
  base: string[] | null
  exceededThreshold: boolean
  dirty: boolean
  rectEl: HTMLDivElement
  lastPreviewKey: string
}

/* ===================== pure helpers (unit-tested) ===================== */

/** Normalize a drag into a left/top/width/height rect regardless of direction. */
export function computeMarqueeRect(
  startX: number,
  startY: number,
  curX: number,
  curY: number,
): { left: number; top: number; width: number; height: number } {
  const left = Math.min(startX, curX)
  const top = Math.min(startY, curY)
  return { left, top, width: Math.abs(curX - startX), height: Math.abs(curY - startY) }
}

/** Strict overlap test — boxes that merely touch at an edge do NOT intersect. */
export function rectsIntersect(a: MarqueeBox, b: MarqueeBox): boolean {
  return a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top
}

/**
 * Resolve modifier semantics. Priority: alt (subtract) > ctrl/meta (add) >
 * shift (range) > none (replace) — e.g. ctrl+alt subtracts, shift+ctrl adds.
 */
export function classifyMarqueeMode(mods: MarqueeModifierState): MarqueeSelectionMode {
  if (mods.alt) return 'subtract'
  if (mods.ctrl || mods.meta) return 'add'
  if (mods.shift) return 'range'
  return 'replace'
}

/**
 * Apply marquee semantics to a base selection.
 * - replace → the hit set (works without a base)
 * - add     → base ∪ hits, base order first (null base → null)
 * - subtract→ base minus hits (null base → null)
 * - range   → null (component-specific anchor logic owns it)
 */
export function applyMarqueeToSelection(
  base: Iterable<string> | null | undefined,
  hits: readonly string[],
  mode: MarqueeSelectionMode,
): string[] | null {
  if (mode === 'range') return null
  if (mode === 'replace') return [...hits]
  if (base == null) return null
  if (mode === 'add') return [...new Set([...base, ...hits])]
  const hitSet = new Set(hits)
  return [...base].filter(p => !hitSet.has(p))
}

/* ===================== internal helpers ===================== */

/**
 * Whether a client-space point sits on one of the container's classic
 * scrollbars. `clientWidth/Height` exclude the scrollbar, the bounding rect
 * does not, so points beyond the client box but inside the rect are over a
 * scrollbar. Overlay scrollbars (client == rect) report false.
 */
function isOverScrollbar(el: HTMLElement, clientX: number, clientY: number): boolean {
  const rect = el.getBoundingClientRect()
  if (rect.width > el.clientWidth && clientX > rect.left + el.clientWidth) return true
  if (rect.height > el.clientHeight && clientY > rect.top + el.clientHeight) return true
  return false
}

/** Collect the paths of item rows whose client rect intersects `box`, in DOM order. */
function collectHitPaths(
  container: HTMLElement,
  itemSelector: string,
  pathAttribute: string,
  box: MarqueeBox,
): string[] {
  const items = container.querySelectorAll<HTMLElement>(itemSelector)
  const paths: string[] = []
  const seen = new Set<string>()
  for (const el of items) {
    const path = el.getAttribute(pathAttribute)
    if (!path || seen.has(path)) continue
    const r = el.getBoundingClientRect()
    if (rectsIntersect(box, { left: r.left, top: r.top, right: r.right, bottom: r.bottom })) {
      seen.add(path)
      paths.push(path)
    }
  }
  return paths
}

function buildResult(drag: DragState, paths: string[], isClick: boolean): MarqueeSelectionResult {
  const mode = classifyMarqueeMode(drag.modifiers)
  return {
    paths,
    mode,
    modifiers: drag.modifiers,
    isClick,
    next: applyMarqueeToSelection(drag.base, paths, mode),
  }
}

/* ===================== hook ===================== */

/**
 * Wire-up:
 *
 * ```tsx
 * const containerRef = useRef<HTMLDivElement>(null)
 * useMarqueeSelection({
 *   containerRef,
 *   itemSelector: '.fb-list-row, .fb-grid-item', // rows carry data-mq-path={path}
 *   getSelectedPaths: () => selectedPaths,
 *   onPreview: (r) => r.next && setSelectedPaths(new Set(r.next)),
 *   onHit: (r) => {
 *     if (r.mode === 'range') { /* component anchor logic /* }
 *     else if (r.next) setSelectedPaths(new Set(r.next))
 *   },
 * })
 * ```
 */
export function useMarqueeSelection(options: MarqueeSelectionOptions): void {
  // Options are read through a ref (useMenuDismiss pattern) so an unstable
  // callback cannot retrigger the effect mid-gesture or drop listeners.
  const optionsRef = useRef(options)
  optionsRef.current = options

  const dragRef = useRef<DragState | null>(null)
  const rafRef = useRef(0)

  useEffect(() => {
    const getOpts = () => optionsRef.current

    const paint = (container: HTMLElement, drag: DragState) => {
      const opts = getOpts()
      // Client-space marquee box for hit testing.
      const m = computeMarqueeRect(drag.startClientX, drag.startClientY, drag.curClientX, drag.curClientY)
      const box: MarqueeBox = { left: m.left, top: m.top, right: m.left + m.width, bottom: m.top + m.height }
      // Content-space box for rendering: the rectangle stays anchored to the
      // press point when the container scrolls during the drag.
      const cr = container.getBoundingClientRect()
      drag.rectEl.style.left = `${m.left - cr.left + container.scrollLeft}px`
      drag.rectEl.style.top = `${m.top - cr.top + container.scrollTop}px`
      drag.rectEl.style.width = `${m.width}px`
      drag.rectEl.style.height = `${m.height}px`

      if (!drag.exceededThreshold) return
      const paths = collectHitPaths(
        container,
        opts.itemSelector,
        opts.pathAttribute ?? DEFAULT_PATH_ATTRIBUTE,
        box,
      )
      const key = paths.join('\u0000')
      if (key !== drag.lastPreviewKey) {
        drag.lastPreviewKey = key
        opts.onPreview?.(buildResult(drag, paths, false))
      }
    }

    const autoScrollTick = (container: HTMLElement, drag: DragState) => {
      const r = container.getBoundingClientRect()
      let dx = 0
      let dy = 0
      if (drag.curClientX < r.left + AUTOSCROLL_EDGE_PX) dx = -AUTOSCROLL_STEP_PX
      else if (drag.curClientX > r.right - AUTOSCROLL_EDGE_PX) dx = AUTOSCROLL_STEP_PX
      if (drag.curClientY < r.top + AUTOSCROLL_EDGE_PX) dy = -AUTOSCROLL_STEP_PX
      else if (drag.curClientY > r.bottom - AUTOSCROLL_EDGE_PX) dy = AUTOSCROLL_STEP_PX
      if (dx) { container.scrollLeft += dx; drag.dirty = true }
      if (dy) { container.scrollTop += dy; drag.dirty = true }
    }

    const frame = () => {
      const drag = dragRef.current
      const container = getOpts().containerRef.current
      if (!drag || !container) return
      if (getOpts().autoScroll !== false) autoScrollTick(container, drag)
      if (drag.dirty) {
        drag.dirty = false
        paint(container, drag)
      }
      rafRef.current = requestAnimationFrame(frame)
    }

    const onMouseMove = (e: MouseEvent) => {
      const drag = dragRef.current
      if (!drag) return
      drag.curClientX = e.clientX
      drag.curClientY = e.clientY
      const threshold = getOpts().startThreshold ?? DEFAULT_START_THRESHOLD
      if (!drag.exceededThreshold) {
        const dx = e.clientX - drag.startClientX
        const dy = e.clientY - drag.startClientY
        if (dx * dx + dy * dy >= threshold * threshold) {
          drag.exceededThreshold = true
          drag.rectEl.style.display = ''
        }
      }
      drag.dirty = true
    }

    const onMouseUp = () => {
      const drag = dragRef.current
      const opts = getOpts()
      const container = opts.containerRef.current
      if (!drag || !container) return

      // Final hit computation is synchronous (never rAF-dependent).
      const m = computeMarqueeRect(drag.startClientX, drag.startClientY, drag.curClientX, drag.curClientY)
      const box: MarqueeBox = { left: m.left, top: m.top, right: m.left + m.width, bottom: m.top + m.height }
      const paths = drag.exceededThreshold
        ? collectHitPaths(container, opts.itemSelector, opts.pathAttribute ?? DEFAULT_PATH_ATTRIBUTE, box)
        : []
      const result = buildResult(drag, paths, !drag.exceededThreshold)
      cleanupDrag()
      opts.onHit(result)
    }

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') cancelDrag()
    }

    /** Cancel without firing onHit (Escape / window blur / unmount / disable). */
    const cancelDrag = () => {
      if (!dragRef.current) return
      cleanupDrag()
    }

    const cleanupDrag = () => {
      const drag = dragRef.current
      if (drag) {
        drag.rectEl.remove()
        dragRef.current = null
      }
      cancelAnimationFrame(rafRef.current)
      document.removeEventListener('mousemove', onMouseMove)
      document.removeEventListener('mouseup', onMouseUp)
      document.removeEventListener('keydown', onKeyDown, true)
      window.removeEventListener('blur', cancelDrag)
      getOpts().containerRef.current?.classList.remove('mq-selecting')
    }

    const onMouseDown = (e: MouseEvent) => {
      const opts = getOpts()
      const container = opts.containerRef.current
      if (!container || e.button !== 0) return
      // A gesture is already active (e.g. the previous mouseup was lost when
      // the window lost capture) — ignore the re-entry instead of orphaning
      // the live rectangle.
      if (dragRef.current) return
      // Another handler already claimed this press (e.g. a resizer).
      if (e.defaultPrevented) return
      const target = e.target
      if (!(target instanceof Element)) return
      // Item rows own their presses: click, HTML5 dragstart, context menu.
      if (target.closest(opts.itemSelector)) return
      const ignore = opts.ignoreSelector ?? DEFAULT_IGNORE_SELECTOR
      if (ignore && target.closest(ignore)) return
      if (isOverScrollbar(container, e.clientX, e.clientY)) return

      // Empty-area press: claim it. preventDefault only suppresses native
      // text selection — contextmenu is a separate event and is unaffected.
      e.preventDefault()
      const pos = getComputedStyle(container).position
      if (pos !== 'absolute' && pos !== 'fixed' && pos !== 'sticky') {
        container.style.position = 'relative'
      }

      const rectEl = document.createElement('div')
      rectEl.className = 'mq-marquee-rect'
      rectEl.style.display = 'none'
      rectEl.setAttribute('aria-hidden', 'true')
      container.appendChild(rectEl)
      container.classList.add('mq-selecting')

      const base = opts.getSelectedPaths ? [...(opts.getSelectedPaths() ?? [])] : null
      dragRef.current = {
        startClientX: e.clientX,
        startClientY: e.clientY,
        curClientX: e.clientX,
        curClientY: e.clientY,
        modifiers: { ctrl: e.ctrlKey, meta: e.metaKey, shift: e.shiftKey, alt: e.altKey },
        base,
        exceededThreshold: false,
        dirty: false,
        rectEl,
        lastPreviewKey: '',
      }

      document.addEventListener('mousemove', onMouseMove)
      document.addEventListener('mouseup', onMouseUp)
      document.addEventListener('keydown', onKeyDown, true)
      window.addEventListener('blur', cancelDrag)
      rafRef.current = requestAnimationFrame(frame)
    }

    if (options.enabled === false) return
    const container = options.containerRef.current
    container?.addEventListener('mousedown', onMouseDown)
    return () => {
      cancelDrag()
      container?.removeEventListener('mousedown', onMouseDown)
    }
  }, [options.containerRef, options.enabled])
}
