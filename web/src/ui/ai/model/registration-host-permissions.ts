// ── Registration host-permission derivation ──
// The turn engine resolves the manifest a pending registration callable
// would install (via appmanager.registration_preview, authoritative
// bound-project resolution included) and attaches appId/appName/permissions
// to the permission event at interception time. The panel derives the
// host-capability surface synchronously from the attached fields — no
// client round-trip, no loading state. When the backend could not resolve
// (older host, service unavailable, malformed manifest) it leaves the
// fields empty and the panel shows the unavailable warning with the
// backend-supplied reason instead of silently hiding the permission surface.

import { isRegistrationCallable } from './app-registration'

export interface RegistrationHostPermissions {
  callId: string
  callableId: string
  status: 'resolved' | 'unavailable'
  appId?: string
  appName?: string
  /** Host capability ids the app declared (manifest Permissions). */
  permissions: string[]
  /** Why permissions could not be resolved (shown in the unavailable note). */
  note?: string
}

/** Element shape produced by steps-to-envelopes from the permission frame. */
export interface PermissionFrameToolCall {
  id: string
  callableId: string
  input?: string
  appId?: string
  appName?: string
  permissions?: string[]
  permissionNote?: string
}

/**
 * Derive the host-permission surface for one registration tool call from the
 * fields the backend attached to the permission frame. Non-registration calls
 * and unresolved registrations degrade to 'unavailable'.
 */
export function registrationHostPermissionsFromFrame(
  call: PermissionFrameToolCall,
): RegistrationHostPermissions {
  if (!isRegistrationCallable(call.callableId)) {
    return {
      callId: call.id,
      callableId: call.callableId,
      status: 'unavailable',
      permissions: [],
    }
  }
  // A present appId means the backend resolved the manifest (an app may
  // legitimately declare zero permissions → resolved with empty list).
  if (call.appId) {
    const permissions = Array.isArray(call.permissions)
      ? call.permissions.filter((p): p is string => typeof p === 'string')
      : []
    return {
      callId: call.id,
      callableId: call.callableId,
      status: 'resolved',
      appId: call.appId,
      appName: call.appName,
      permissions,
    }
  }
  return {
    callId: call.id,
    callableId: call.callableId,
    status: 'unavailable',
    permissions: [],
    note: call.permissionNote,
  }
}
