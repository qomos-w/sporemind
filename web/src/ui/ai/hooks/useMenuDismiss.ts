import { useEffect, useRef, type RefObject } from 'react'

/**
 * Open-guard windows. Outside events arriving within this many milliseconds
 * of the menu opening are ignored, so they cannot tear the menu straight back
 * down. This is what fixes the touch/iframe long-press flicker — see below.
 */
const MOUSEDOWN_GUARD_MS = 200
const CONTEXTMENU_GUARD_MS = 400

/**
 * Shared outside-interaction dismissal for the floating context menus
 * (Agent / Project / Topology).
 *
 * Registers three document listeners while `activeKey` is non-null:
 *   - `mousedown` (left button only) outside  → close
 *   - `contextmenu` (capture phase)   outside  → close
 *   - `keydown` Escape                         → close
 *
 * Why the open-guard matters (the Capacitor / touch-iframe bug):
 * On a touch WebView a long-press is delivered twice — first our own
 * `useLongPress` timer opens the menu, then ~80–200ms later the browser
 * synthesises a native `contextmenu` event for the same gesture. Because the
 * menu is already mounted by then, its capture-phase `contextmenu` listener
 * fires before the trigger element's own handler, sees the trigger outside the
 * menu, and closes it — so the menu flashes open then vanishes. Guarding both
 * close paths for a short window after open lets that synthesised event pass
 * harmlessly. Desktop right-click is unaffected: its reopen gesture is a
 * deliberate second action that always lands well past the guard.
 *
 * `activeKey` doubles as both the enabled flag (null = closed, no listeners)
 * and the reset trigger: changing it (e.g. a new target / new open position)
 * re-runs the effect and re-stamps the open time. `onClose` is read through a
 * ref so an unstable callback cannot reset the guard by retriggering the effect.
 */
export function useMenuDismiss(
  menuRef: RefObject<HTMLElement | null>,
  onClose: () => void,
  activeKey: unknown,
): void {
  const openedAtRef = useRef(0)
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose

  useEffect(() => {
    if (activeKey == null) return
    openedAtRef.current = performance.now()

    const withinGuard = (ms: number) => performance.now() - openedAtRef.current < ms
    const isOutside = (target: EventTarget | null) =>
      !!menuRef.current && !menuRef.current.contains(target as Node)

    const onMouseDown = (event: MouseEvent) => {
      // Right/middle buttons are ignored: a right-click fires mousedown then a
      // separate contextmenu; closing here would unmount before contextmenu and
      // cause the "have to click twice" flicker on desktop.
      if (event.button !== 0) return
      if (withinGuard(MOUSEDOWN_GUARD_MS)) return
      if (isOutside(event.target)) onCloseRef.current()
    }
    const onContextMenu = (event: MouseEvent) => {
      if (withinGuard(CONTEXTMENU_GUARD_MS)) return
      if (isOutside(event.target)) onCloseRef.current()
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onCloseRef.current()
    }

    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('contextmenu', onContextMenu, true)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('contextmenu', onContextMenu, true)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [activeKey, menuRef])
}
