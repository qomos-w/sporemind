/**
 * Pending "locate scheduled task" requests for the ScheduledView. The
 * scheduler mode's registered click action (see modeClickRegistry +
 * registerModeClickActions.ts) requests a locate before/as the scheduled
 * view opens; ScheduledView consumes the pending request on mount and reacts
 * to later ones via subscription (same pattern as workflowLocateStore).
 *
 * A plain module-level store is enough: requests are transient (consumed once)
 * and the only subscriber is the scheduled view.
 */

/** A pending locate request carried across the mode-open boundary. */
export interface ScheduledLocateIntent {
  cardId: string
}

let pendingLocate: ScheduledLocateIntent | null = null
const listeners = new Set<() => void>()

/** Request that the scheduled view select (locate) the given scheduler card. */
export function requestScheduledLocate(intent: ScheduledLocateIntent): void {
  pendingLocate = intent
  for (const cb of listeners) cb()
}

/** Take the pending locate request, clearing it. Null when none is pending. */
export function consumePendingScheduledLocate(): ScheduledLocateIntent | null {
  const intent = pendingLocate
  pendingLocate = null
  return intent
}

/** Subscribe to locate requests; returns an unsubscribe function. */
export function subscribeScheduledLocate(cb: () => void): () => void {
  listeners.add(cb)
  return () => { listeners.delete(cb) }
}

/** Open the scheduled view and locate the given scheduler card. When cardId
 *  is undefined the view just opens without a locate (mirrors the workflow
 *  badge behaviour for agents without a bound workflow). */
export function openScheduledAndLocate(cardId: string | undefined): void {
  window.dispatchEvent(new CustomEvent('sporemind:set-shell-content-mode', { detail: 'scheduled' }))
  if (cardId) requestScheduledLocate({ cardId })
}