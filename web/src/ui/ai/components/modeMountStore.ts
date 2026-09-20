/**
 * Pending "mount builtin mode" requests for the composer. Quick-start cards
 * (ProjectNewChatPage quick actions, QuickStartActions) request a mode mount
 * here; the composer that owns the target agent consumes the request and
 * mounts the mode via the same path as typing `/<mode>` + Enter (see
 * AIComposer enterMatchedMode → onEnterMode → agent.component.mount RPC).
 *
 * A plain module-level store is enough: requests are transient (consumed
 * once) and each composer instance checks the agentActorId to decide whether
 * the request is addressed to it.
 */

/** A pending mode-mount request carried from a quick-start card to a composer. */
export interface ModeMountRequest {
  /** Full builtin mode card id, e.g. "builtin:mode:workflow". */
  cardId: string
  /** Actor id of the agent the mode should mount on. */
  agentActorId: string
}

let pendingRequest: ModeMountRequest | null = null
const listeners = new Set<() => void>()

/** Request that the composer owning `agentActorId` mount the builtin mode `cardId`. */
export function requestModeMount(cardId: string, agentActorId: string): void {
  pendingRequest = { cardId, agentActorId }
  for (const cb of listeners) cb()
}

/** Take the pending mode-mount request, clearing it. Null when none is pending. */
export function consumePendingModeMount(): ModeMountRequest | null {
  const request = pendingRequest
  pendingRequest = null
  return request
}

/** Subscribe to mode-mount requests; returns an unsubscribe function. */
export function subscribeModeMount(cb: () => void): () => void {
  listeners.add(cb)
  return () => { listeners.delete(cb) }
}
