// TabBadgeProvider — supplies optional badges (e.g. dirty markers) for tabs in
// TabContainer. Decouples the shell from any specific editor/document model.
//
// sporemind wires this to its editorStore (Monaco dirty state); other consumers
// can return null for everything or implement their own semantics.

import type { TabItem } from '../types/layout'

export type TabBadge = 'dirty' | null

export interface TabBadgeProvider {
  /** Subscribe to badge changes; returns an unsubscribe function. */
  subscribe(fn: () => void): () => void
  /** Monotonic version counter for useSyncExternalStore. */
  getVersion(): number
  /** Return the current badge for the given tab, or null. */
  getBadge(tab: TabItem): TabBadge
}

/** No-op badge provider — every tab returns null. */
export const noBadgeProvider: TabBadgeProvider = {
  subscribe() { return () => {} },
  getVersion() { return 0 },
  getBadge() { return null },
}
