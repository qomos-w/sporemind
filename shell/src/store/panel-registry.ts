// ── PanelRegistry: auto-register singleton panels, generate menu items ──

import type { DockZone } from '../types/layout'

export interface PanelMeta {
  id: string
  title: string
  /** Icon element — use lucide-react icons */
  icon: unknown // ReactNode — avoiding React import to keep store pure
  dockZone?: DockZone
  defaultMode?: 'floating' | 'pinned'
  defaultVisible?: boolean
  defaultSize?: { w: number; h: number }
}

export interface ViewMeta {
  id: string
  title: string
}

type Entry =
  | { kind: 'panel'; meta: PanelMeta }
  | { kind: 'view'; meta: ViewMeta }

const entries: Entry[] = []

/** Register a side/dock panel. Idempotent — duplicate IDs are ignored. */
export function registerPanel(meta: PanelMeta): void {
  if (entries.some(e => e.kind === 'panel' && e.meta.id === meta.id)) return
  entries.push({ kind: 'panel', meta })
}

/** Register a main-tab view (e.g. DAG). Idempotent. */
export function registerView(meta: ViewMeta): void {
  if (entries.some(e => e.kind === 'view' && e.meta.id === meta.id)) return
  entries.push({ kind: 'view', meta })
}

/** Get all registered panel metadata. */
export function getAllPanels(): PanelMeta[] {
  return entries.filter((e): e is { kind: 'panel'; meta: PanelMeta } => e.kind === 'panel')
    .map(e => e.meta)
}

/** Get a specific panel's metadata. */
export function getPanel(id: string): PanelMeta | undefined {
  const entry = entries.find(e => e.kind === 'panel' && e.meta.id === id)
  return entry?.kind === 'panel' ? entry.meta : undefined
}

/** Get all registered view metadata. */
export function getAllViews(): ViewMeta[] {
  return entries.filter((e): e is { kind: 'view'; meta: ViewMeta } => e.kind === 'view')
    .map(e => e.meta)
}

/** Parse a menu item ID to extract the panel ID. Returns undefined if not a panel menu item. */
export function parsePanelMenuItem(menuItemId: string): string | undefined {
  const prefix = 'view.panel.'
  if (menuItemId.startsWith(prefix)) return menuItemId.slice(prefix.length)
  return undefined
}

/** Parse a menu item ID to extract the view ID. Returns undefined if not a view menu item. */
export function parseViewMenuItem(menuItemId: string): string | undefined {
  const prefix = 'view.open.'
  if (menuItemId.startsWith(prefix)) return menuItemId.slice(prefix.length)
  return undefined
}
