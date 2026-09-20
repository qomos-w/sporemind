// ── App registration permission detection ──
// Mirrors registrationCallableIDs in pkg/actor/agent/turn_engine_audit.go:
// the callables that install or register an app into the host. The turn
// engine forces these through the interactive permission step in every
// permission mode; the frontend uses the same set to detect that
// interception and render the authorization overlay.

import type { PermissionRequestFrame } from './frame-types'

export const APP_REGISTRATION_CALLABLES: ReadonlySet<string> = new Set([
  'appmanager.register',
  'appmanager.register_project',
  'appmanager.install_local',
  'cloudaccount.content_install',
])

export function isRegistrationCallable(callableId: string): boolean {
  return APP_REGISTRATION_CALLABLES.has(callableId)
}

/** True when the pending permission frame intercepts an app registration. */
export function isRegistrationPermission(frame: PermissionRequestFrame): boolean {
  return frame.toolCalls.some(call => isRegistrationCallable(call.callableId))
}
