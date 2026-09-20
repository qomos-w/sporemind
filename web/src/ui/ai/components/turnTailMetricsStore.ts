/**
 * Visibility of the turn-tail header metrics (budget bar, token badge,
 * duration badge). Persisted via the workspace UI model (actor-owned backend
 * state); AIShellLayout loads/saves it from settings and pushes updates here,
 * so every TurnTail instance re-renders without prop drilling through
 * MessageStream.
 *
 * A plain module-level store is enough: same pattern as workflowLocateStore /
 * composerDraftStore — the persisted source of truth lives in the backend,
 * this is just the client-side fan-out.
 */
import { useSyncExternalStore } from 'react'
import { defaultTurnTailMetricsVisible, type TurnTailMetricsVisible } from '../../../application/workspace-ui-state'

let state: TurnTailMetricsVisible = defaultTurnTailMetricsVisible
const listeners = new Set<() => void>()

/** Current visibility flags (snapshot). */
export function getTurnTailMetricsVisible(): TurnTailMetricsVisible {
  return state
}

/** Replace the visibility flags and notify subscribers. */
export function setTurnTailMetricsVisible(next: TurnTailMetricsVisible): void {
  state = next
  for (const cb of listeners) cb()
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb)
  return () => { listeners.delete(cb) }
}

/** React hook: subscribe to turn-tail metric visibility. */
export function useTurnTailMetricsVisible(): TurnTailMetricsVisible {
  return useSyncExternalStore(subscribe, getTurnTailMetricsVisible)
}
