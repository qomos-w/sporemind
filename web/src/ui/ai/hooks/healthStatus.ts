/**
 * Shared health-status projection utilities for provider/cooldown UI.
 *
 * All functions are pure and side-effect free so they can be unit-tested
 * without React or network. Time is always passed as a parameter (epoch ms)
 * to keep them deterministic.
 */

export type HealthState = 'healthy' | 'cooling_down' | 'disabled'

export type HealthReason =
  | 'rate_limit'
  | 'availability'
  | 'quota_exhausted'
  | 'authentication_failed'
  | 'authorization_failed'
  | 'configuration_error'
  | 'model_deprecated'
  | 'disable_window'

export type RecoveryMode = 'cooldown' | 'manual_or_balance_refresh' | 'probe'

/**
 * Normalised read-only projection that both `Provider` and
 * `AICallableUnitView` can be narrowed into.
 */
export interface HealthProjection {
  healthState?: string
  healthReason?: string
  /** Epoch seconds, matching backend projection. */
  cooldownUntil?: number
  lastFailureAt?: number
  recoveryMode?: string
}

/** Returns true when the unit is in active cooling down (has a future deadline). */
export function isCoolingDown(p: HealthProjection, nowMs: number): boolean {
  if (p.healthState === 'cooling_down' && p.cooldownUntil) {
    return p.cooldownUntil * 1000 > nowMs
  }
  return false
}

/** Returns true when the unit is long-term disabled. */
export function isDisabled(p: HealthProjection): boolean {
  return p.healthState === 'disabled'
}

/** Returns true when the unit should be blocked from selection. */
export function isUnavailable(p: HealthProjection, nowMs: number): boolean {
  return isCoolingDown(p, nowMs) || isDisabled(p)
}

/**
 * Compact countdown formatter.
 * - < 1 min  → `0:42`
 * - < 1 hour → `01:24`
 * - ≥ 1 hour → `1:02:18`
 */
export function formatCountdown(deadlineEpochSec: number, nowMs: number): string {
  const remainingSec = Math.max(0, Math.ceil(deadlineEpochSec * 1000 - nowMs) / 1000)
  const totalSec = Math.floor(remainingSec)
  const hours = Math.floor(totalSec / 3600)
  const minutes = Math.floor((totalSec % 3600) / 60)
  const seconds = totalSec % 60
  if (hours > 0) {
    const mm = String(minutes).padStart(2, '0')
    const ss = String(seconds).padStart(2, '0')
    return `${hours}:${mm}:${ss}`
  }
  const mm = String(minutes).padStart(2, '0')
  const ss = String(seconds).padStart(2, '0')
  return `${mm}:${ss}`
}

/**
 * Remaining-fraction of a cooldown, clamped to [0, 1].
 * `startEpochSec` is optional; when absent the function returns 0 (no
 * progress information available — the caller may fall back to a full bar
 * or record its own start point on first observation).
 */
export function cooldownRatio(
  startEpochSec: number | undefined,
  deadlineEpochSec: number,
  nowMs: number,
): number {
  if (!startEpochSec) return 0
  const total = deadlineEpochSec - startEpochSec
  if (total <= 0) return 0
  const elapsed = nowMs / 1000 - startEpochSec
  return Math.max(0, Math.min(1, elapsed / total))
}

/**
 * Returns the i18n key suffix for a health reason, suitable for
 * `t('health.reason.' + suffix)`. Empty-string reasons (aggregator-ref rows
 * projecting child unavailability) map to 'unknown' so the dropdown never
 * renders a missing-key fallback like "health.reason.".
 */
export function healthReasonLabelKey(reason: string | undefined): string {
  return reason || 'unknown'
}

/**
 * Returns the i18n key suffix for a recovery mode.
 */
export function recoveryModeLabelKey(mode: string | undefined): string {
  return mode ?? 'cooldown'
}
