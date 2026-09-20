/**
 * Workbench board body-level mount (spec §6).
 *
 * Mirrors the app-background pattern (`app-background.ts`): the board's ambient
 * backdrop — the aurora wash and the paper grain — is staged by a body-level
 * marker class instead of a shell-local wrapper, so it composes against the
 * document the same way the app background image does.
 * `workbench-surface.css` consumes the class.
 */
export const WORKBENCH_SURFACE_CLASS = 'workbench-surface-active'

/** Toggle the body-level workbench marker class. */
export function applyWorkbenchSurface(active: boolean): void {
  const body = document.body
  if (active) {
    body.classList.add(WORKBENCH_SURFACE_CLASS)
  } else {
    body.classList.remove(WORKBENCH_SURFACE_CLASS)
  }
}
