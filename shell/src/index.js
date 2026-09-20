// @qomos/sporemind-shell — reusable IDE-style desktop shell.
//
// This package contains the dock layout, tab/split system, title bar, activity
// bar, floating panels, and the layout/panel/dock-sizes stores. It does NOT
// depend on any sporemind backend callable — all domain coupling goes through the
// three adapter interfaces in `./adapters`.
export * from './types/layout';
export * from './adapters/storage';
export * from './adapters/window';
export * from './adapters/tab-badge';
export * from './i18n/tab-context-menu';
export * from './lib/dock-utils';
export * from './lib/console-patch';
export { LayoutStore } from './store/layout';
export { PanelManager } from './store/panels';
export { DockSizesStore } from './store/dock-sizes';
export { registerPanel, registerView, getAllPanels, getPanel, getAllViews, parsePanelMenuItem, parseViewMenuItem, } from './store/panel-registry';
export * from './components';
