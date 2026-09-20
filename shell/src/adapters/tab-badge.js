// TabBadgeProvider — supplies optional badges (e.g. dirty markers) for tabs in
// TabContainer. Decouples the shell from any specific editor/document model.
//
// sporemind wires this to its editorStore (Monaco dirty state); other consumers
// can return null for everything or implement their own semantics.
/** No-op badge provider — every tab returns null. */
export const noBadgeProvider = {
    subscribe() { return () => { }; },
    getVersion() { return 0; },
    getBadge() { return null; },
};
