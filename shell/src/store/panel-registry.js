// ── PanelRegistry: auto-register singleton panels, generate menu items ──
const entries = [];
/** Register a side/dock panel. Idempotent — duplicate IDs are ignored. */
export function registerPanel(meta) {
    if (entries.some(e => e.kind === 'panel' && e.meta.id === meta.id))
        return;
    entries.push({ kind: 'panel', meta });
}
/** Register a main-tab view (e.g. DAG). Idempotent. */
export function registerView(meta) {
    if (entries.some(e => e.kind === 'view' && e.meta.id === meta.id))
        return;
    entries.push({ kind: 'view', meta });
}
/** Get all registered panel metadata. */
export function getAllPanels() {
    return entries.filter((e) => e.kind === 'panel')
        .map(e => e.meta);
}
/** Get a specific panel's metadata. */
export function getPanel(id) {
    const entry = entries.find(e => e.kind === 'panel' && e.meta.id === id);
    return entry?.kind === 'panel' ? entry.meta : undefined;
}
/** Get all registered view metadata. */
export function getAllViews() {
    return entries.filter((e) => e.kind === 'view')
        .map(e => e.meta);
}
/** Parse a menu item ID to extract the panel ID. Returns undefined if not a panel menu item. */
export function parsePanelMenuItem(menuItemId) {
    const prefix = 'view.panel.';
    if (menuItemId.startsWith(prefix))
        return menuItemId.slice(prefix.length);
    return undefined;
}
/** Parse a menu item ID to extract the view ID. Returns undefined if not a view menu item. */
export function parseViewMenuItem(menuItemId) {
    const prefix = 'view.open.';
    if (menuItemId.startsWith(prefix))
        return menuItemId.slice(prefix.length);
    return undefined;
}
