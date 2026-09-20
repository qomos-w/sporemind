/**
 * Registry for bundle/mode click actions. Each bundle mode may register a
 * frontend click action for its composer badge; clicking the badge dispatches
 * the registered action (e.g. the workflow mode opens the workflow view and
 * locates the active workflow, zoomed to fit the screen).
 *
 * Registration is module-level and static: actions are registered once at app
 * init (registerModeClickActions.ts) and looked up when badges render.
 */

export interface ModeClickContext {
  /** ActorId of the composer agent the clicked badge belongs to. Actions that
   *  target "the current agent" (e.g. the workflow mode locating the composer
   *  agent's bound workflow map) read it from here. */
  agentActorId?: string
}

export type ModeClickAction = (ctx?: ModeClickContext) => void

const registry = new Map<string, ModeClickAction>()

/** Register (or replace) the click action for a bundle/mode card id. */
export function registerModeClickAction(cardId: string, action: ModeClickAction): void {
  registry.set(cardId, action)
}

/** Remove the click action for a bundle/mode card id. */
export function unregisterModeClickAction(cardId: string): void {
  registry.delete(cardId)
}

/** Get the registered click action for a bundle/mode card id, if any. */
export function getModeClickAction(cardId: string): ModeClickAction | undefined {
  return registry.get(cardId)
}

/** Run the registered click action for a bundle/mode card id.
 *  Returns true when an action was registered and ran. */
export function runModeClickAction(cardId: string, ctx?: ModeClickContext): boolean {
  const action = registry.get(cardId)
  if (!action) return false
  action(ctx)
  return true
}
