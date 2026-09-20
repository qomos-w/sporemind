/**
 * Pure helpers for rendering plugin bundle details in the settings UI.
 * Kept free of React and side effects so they can be unit-tested easily.
 */

export interface HashDisplay {
  short: string
  full: string
}

export function formatHashShort(hash?: string): HashDisplay {
  if (!hash) return { short: '', full: '' }
  const short = hash.length > 8 ? `${hash.slice(0, 8)}…` : hash
  return { short, full: hash }
}

export interface CapabilityGroup {
  granted: string[]
  denied: string[]
}

export function groupCapabilities(
  permissions: readonly string[] | undefined,
  grantedCapabilities: readonly string[] | undefined,
): CapabilityGroup {
  const grantedSet = new Set(grantedCapabilities ?? [])
  const granted: string[] = []
  const denied: string[] = []
  for (const cap of permissions ?? []) {
    if (grantedSet.has(cap)) granted.push(cap)
    else denied.push(cap)
  }
  return { granted, denied }
}

export function isCapabilityGranted(
  capabilityId: string,
  grantedCapabilities: readonly string[] | undefined,
): boolean {
  return (grantedCapabilities ?? []).includes(capabilityId)
}
