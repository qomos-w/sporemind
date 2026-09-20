import type { AppEntry } from './app-registry'

/**
 * States in which an app's view entrypoints may be opened. Mirrors the
 * launcher tile gating (SidebarLauncher.buildLauncherApps): running apps,
 * plus crashed/failed ones whose panel renders the error + restart
 * affordance. A stopped/unloaded app has no live entrypoint, so opening it
 * would start a dead view.
 */
export const OPENABLE_APP_STATES: ReadonlySet<string> = new Set(['running', 'crashed', 'failed'])

/** True when the app exposes at least one view entrypoint and its state is openable. */
export function isAppOpenable(app: Pick<AppEntry, 'state' | 'entrypoints'>): boolean {
  return app.entrypoints.some(entry => entry.kind === 'view') && OPENABLE_APP_STATES.has(app.state)
}
