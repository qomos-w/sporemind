import type { ReactNode } from 'react'
import './tier-badges.css'

/**
 * Tier badge primitives plus the pure account/content gating logic shared by
 * the cloud-account settings, the content store and the plugin panel.
 *
 * Colors come exclusively from the theme tokens --tier-gold (EXP/Insider) and
 * --tier-paid (paid/Ultimate); components never hardcode hex values and never
 * reuse --warning / --accent-primary for tier semantics.
 */

/** Minimal shape of the Pass struct the desktop actor passes through. */
export interface PassViewLike {
  Type?: string
  StartsAt?: string
  EndsAt?: string
}

/** Whether an Insider pass window is active at `now`. An empty Type means "no
 *  pass"; a missing boundary is treated as open-ended. Parsing failures make
 *  the boundary inactive rather than active. */
export function isPassActive(pass: PassViewLike | undefined, now: Date = new Date()): boolean {
  if (!pass || !pass.Type) return false
  const nowMs = now.getTime()
  if (pass.StartsAt) {
    const startsAtMs = new Date(pass.StartsAt).getTime()
    if (Number.isNaN(startsAtMs) || startsAtMs > nowMs) return false
  }
  if (pass.EndsAt) {
    const endsAtMs = new Date(pass.EndsAt).getTime()
    if (Number.isNaN(endsAtMs) || endsAtMs < nowMs) return false
  }
  return true
}

/** Cloud's "permanent" convention: the pass expires in year >= 9999 (UTC). */
export function isPermanentExpiry(endsAt: string | undefined): boolean {
  if (!endsAt) return false
  const year = new Date(endsAt).getUTCFullYear()
  return !Number.isNaN(year) && year >= 9999
}

export type AccountTierState =
  | { kind: 'ultimate' }
  | { kind: 'insider'; endsAt?: string; permanent: boolean }
  | { kind: 'free' }

/** Three-state subscription badge resolution: Ultimate wins over any pass;
 *  an active Insider pass shows gold; everything else falls back to the gray
 *  free badge. */
export function resolveAccountTierState(
  tier: string | undefined,
  pass: PassViewLike | undefined,
  now: Date = new Date(),
): AccountTierState {
  if (tier === 'ultimate') return { kind: 'ultimate' }
  if (isPassActive(pass, now)) {
    return { kind: 'insider', endsAt: pass?.EndsAt, permanent: isPermanentExpiry(pass?.EndsAt) }
  }
  return { kind: 'free' }
}

export interface ContentAccess {
  hasExperimentalAccess: boolean
  hasPaidAccess: boolean
}

/** Whether the current account may install experimental and paid content:
 *  experimental requires Ultimate or an active Insider pass; paid requires
 *  Ultimate. */
export function resolveContentAccess(
  tier: string | undefined,
  pass: PassViewLike | undefined,
  now: Date = new Date(),
): ContentAccess {
  return {
    hasExperimentalAccess: tier === 'ultimate' || isPassActive(pass, now),
    hasPaidAccess: tier === 'ultimate',
  }
}

export type InstallGate = 'installable' | 'locked_experimental' | 'locked_paid'

/** Install-button gate for one content item. experimental items are locked for
 *  accounts without Insider/Ultimate access; paid items are locked for
 *  everyone below Ultimate. */
export function resolveInstallGate(
  item: { Visibility?: string; Pricing?: string },
  access: ContentAccess,
): InstallGate {
  if (item.Visibility === 'experimental' && !access.hasExperimentalAccess) return 'locked_experimental'
  if (item.Pricing === 'paid' && !access.hasPaidAccess) return 'locked_paid'
  return 'installable'
}

/** Small gold "EXP" badge (experimental / Insider). */
export function ExpBadge({
  label = 'EXP',
  title,
  className,
}: {
  label?: ReactNode
  title?: string
  className?: string
}) {
  return (
    <span className={`tier-badge exp${className ? ` ${className}` : ''}`} title={title}>
      {label}
    </span>
  )
}

/** Small purple "paid" badge (paid-only content; Ultimate users see it off). */
export function PaidBadge({
  label = 'PAID',
  title,
  className,
}: {
  label?: ReactNode
  title?: string
  className?: string
}) {
  return (
    <span className={`tier-badge paid${className ? ` ${className}` : ''}`} title={title}>
      {label}
    </span>
  )
}

/** Small tier badge: Ultimate renders in the paid-purple variant, free in the
 *  muted gray variant, anything else in the neutral variant. */
export function TierBadge({
  tier,
  label,
  className,
}: {
  tier?: string
  label: ReactNode
  className?: string
}) {
  const variant = tier === 'ultimate' ? 'paid' : tier === 'free' ? 'free' : ''
  return (
    <span className={`tier-badge${variant ? ` ${variant}` : ''}${className ? ` ${className}` : ''}`}>{label}</span>
  )
}