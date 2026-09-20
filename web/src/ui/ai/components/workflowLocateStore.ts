/**
 * Pending "locate workflow" requests for the workflow graph view. The workflow
 * mode's registered click action (see modeClickRegistry) requests a locate
 * before/as the workflow view opens; TopologyModeView consumes the pending
 * request on mount and forwards it to WorkflowGraph as a locateRequest.
 *
 * A plain module-level store is enough: requests are transient (consumed once)
 * and the only subscriber is the workflow topology view.
 */

/** A pending locate request carried across the mode-open boundary. `selectStart`
 *  asks the workflow view to also select the map's start node and ensure the
 *  workflow is expanded (used by the badge/row entry points); '*' as `mapId`
 *  fits the whole graph. */
export interface WorkflowLocateIntent {
  mapId: string
  selectStart?: boolean
}

let pendingLocate: WorkflowLocateIntent | null = null
const listeners = new Set<() => void>()

/** Request that the workflow view locate (zoom-to-fit) the given workflow map. */
export function requestWorkflowLocate(intent: WorkflowLocateIntent): void {
  pendingLocate = intent
  for (const cb of listeners) cb()
}

/** Take the pending locate request, clearing it. Null when none is pending. */
export function consumePendingWorkflowLocate(): WorkflowLocateIntent | null {
  const intent = pendingLocate
  pendingLocate = null
  return intent
}

/** Subscribe to locate requests; returns an unsubscribe function. */
export function subscribeWorkflowLocate(cb: () => void): () => void {
  listeners.add(cb)
  return () => { listeners.delete(cb) }
}

/** Open the workflow mode and locate (zoom-to-fit) the given workflow map.
 *  When the workflow view is not mounted yet it consumes the pending request
 *  on mount; when it is already open it reacts via subscribeWorkflowLocate.
 *  The locate also asks the view to select the map's start node and ensure the
 *  workflow is expanded (both entry points — badge action and TurnTail row —
 *  share this behaviour, so it is encoded here rather than at each call site). */
export function openWorkflowAndLocate(mapId: string | undefined): void {
  window.dispatchEvent(new CustomEvent('sporemind:set-shell-content-mode', { detail: 'workflow' }))
  if (mapId) requestWorkflowLocate({ mapId, selectStart: true })
}

/** Request that the workflow view toggle its layout direction (LTR ↔ TTB).
 *  TopologyModeView subscribes and applies the toggle. */
export function requestWorkflowDirectionToggle(): void {
  window.dispatchEvent(new CustomEvent('sporemind:workflow-direction-toggle'))
}

/** Subscribe to direction-toggle requests; returns an unsubscribe function. */
export function subscribeWorkflowDirectionToggle(cb: () => void): () => void {
  const handler = () => cb()
  window.addEventListener('sporemind:workflow-direction-toggle', handler)
  return () => window.removeEventListener('sporemind:workflow-direction-toggle', handler)
}
