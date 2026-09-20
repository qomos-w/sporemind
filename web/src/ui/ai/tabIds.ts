/**
 * Stable tab-id computation for the right-panel tab bar in AIShellLayout.
 *
 * These are pure, side-effect-free functions so tab dedup semantics are
 * unit-testable without assembling the full layout (project constraint
 * "测试边界"). All call sites (handleOpenSshSession, the SSH restore flow,
 * handleOpenPluginView) must reference these helpers rather than inlining
 * template literals, so the keys can never drift apart.
 */

/** SSH session tab id, keyed on hostId (not sessionId) so the tab stays stable
 *  across reconnects and the restore path converges with the ssh_manager_event
 *  path onto the same tab. */
export function sshTabId(hostId: string): string {
  return `ssh-host-${hostId}`
}

/** Database session tab id, keyed on profileId. Mirrors sshTabId: one tab per
 *  dbmanager profile; opening the same profile again just activates the
 *  existing tab instead of stacking duplicates. */
export function dbTabId(profileId: string): string {
  return `db-profile-${profileId}`
}

/** Object-storage session tab id, keyed on profileId. Mirrors dbTabId but
 *  routes through a separate right-panel union variant so the
 *  ObjectStorageSessionView can ship its own tree + transfer panel without
 *  inheriting the SQL/JSON query editor baked into DbSessionView. */
export function objectStorageTabId(profileId: string): string {
  return `object-storage-${profileId}`
}

/** Plugin view tab id, namespaced by pluginID so two plugins exposing the same
 *  entrypoint id (e.g. "main") cannot collide. */
export function pluginTabId(pluginID: string, viewID: string): string {
  return `plugin-view-${pluginID}-${viewID}`
}

/** Right-panel "Apps" picker tab id. Singleton (no payload): opening the
 *  picker again focuses the existing list instead of stacking duplicates. */
export const APP_LIST_TAB_ID = 'app-list'