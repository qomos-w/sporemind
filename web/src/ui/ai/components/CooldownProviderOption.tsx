import React, { useRef } from 'react'
import { Check, Clock, Loader2, Zap } from 'lucide-react'
import type { DispatchActivity } from '../../../gen-clients/system/types'
import type { ProviderOption } from './AIComposer'
import { ProviderIcon, iconKeyForModelRow } from './providerIcons'
import {
  isCoolingDown,
  isDisabled,
  formatCountdown,
  healthReasonLabelKey,
  recoveryModeLabelKey,
  type HealthProjection,
} from '../hooks/healthStatus'

export interface CooldownProviderOptionProps {
  option: ProviderOption
  active: boolean
  /** Current time in epoch ms (parent ticks this every second). */
  nowMs: number
  onSelect: () => void
  t: (key: string, params?: Record<string, string | number>) => string
}

/** Badge showing an aggregator's dispatch activity (waiting/trying/in_use).
 *  Shared by model rows, ref rows, and route headers so an activity that
 *  bubbles up from a nested subtree renders identically at every level. */
export const DispatchActivityBadge: React.FC<{
  activity: DispatchActivity
  t: (key: string, params?: Record<string, string | number>) => string
}> = ({ activity, t }) => {
  const state = activity.State
  let label: string
  switch (state) {
    case 'waiting': label = t('composer.activity.waiting'); break
    case 'trying': label = t('composer.activity.trying'); break
    case 'in_use': label = t('composer.activity.inUse'); break
    default: return null
  }
  return (
    <span className={`ai-composer-provider-activity is-${state}`} title={label}>
      {state === 'waiting' && <Clock size={11} />}
      {state === 'trying' && <Loader2 size={11} className="spin" />}
      {state === 'in_use' && <Zap size={11} />}
      <span className="ai-composer-provider-activity-label">{label}</span>
    </span>
  )
}

/**
 * Shared provider option renderer for both custom-route and Auto-route
 * branches of the composer provider dropdown.
 *
 * All units are always selectable by the user. Health state is purely advisory
 * — it affects aggregator auto-strategy selection and failover, but never
 * blocks user choice:
 *
 * - `cooling_down`: semi-transparent progress overlay shrinking left→right,
 *   compact countdown on the right.
 * - `disabled`: static mask + reason label, no fake countdown.
 * - `healthy`: normal item.
 */
export const CooldownProviderOption: React.FC<CooldownProviderOptionProps> = ({
  option,
  active,
  nowMs,
  onSelect,
  t,
}) => {
  const proj: HealthProjection = {
    healthState: option.healthState,
    healthReason: option.healthReason,
    cooldownUntil: option.cooldownUntil,
    lastFailureAt: option.lastFailureAt,
    recoveryMode: option.recoveryMode,
  }

  const cooling = isCoolingDown(proj, nowMs)
  const disabled = isDisabled(proj)

  // Record the total cooldown duration the first time we observe this
  // deadline, so we can derive a progress fraction without a backend start
  // timestamp. In-memory only — never persisted.
  const initialRemainingRef = useRef<number | null>(null)
  if (cooling && option.cooldownUntil && initialRemainingRef.current === null) {
    initialRemainingRef.current = Math.max(0, option.cooldownUntil * 1000 - nowMs)
  }
  if (!cooling && initialRemainingRef.current !== null) {
    initialRemainingRef.current = null
  }

  let countdown: string | null = null
  let remainingFraction = 1
  let ariaDescription = ''

  if (cooling && option.cooldownUntil) {
    countdown = formatCountdown(option.cooldownUntil, nowMs)
    const total = initialRemainingRef.current
    if (total && total > 0) {
      const remaining = Math.max(0, option.cooldownUntil * 1000 - nowMs)
      remainingFraction = remaining / total
    }
    ariaDescription = t('health.coolingDown', { countdown })
  }

  let reasonLabel = ''
  if (disabled) {
    reasonLabel = t(`health.reason.${healthReasonLabelKey(option.healthReason)}`)
    ariaDescription = reasonLabel
    if (option.recoveryMode) {
      ariaDescription += ' ' + t(`health.recovery.${recoveryModeLabelKey(option.recoveryMode)}`)
    }
  } else if (cooling && option.healthReason === 'disable_window') {
    reasonLabel = t('health.reason.disable_window')
    ariaDescription = `${t('health.coolingDown', { countdown: countdown ?? '' })} · ${reasonLabel}`
  }

  const descId = `provider-health-desc-${option.id}`

  return (
    <>
      <button
        type="button"
        className={[
          'ai-composer-provider-item',
          active ? 'active' : '',
          cooling ? 'is-cooling' : '',
          disabled ? 'is-disabled' : '',
        ].filter(Boolean).join(' ')}
        aria-describedby={(cooling || disabled) ? descId : undefined}
        onClick={onSelect}
      >
        {cooling && (
          <span
            className="ai-composer-cooldown-overlay"
            style={{ width: `${remainingFraction * 100}%` }}
            aria-hidden="true"
          />
        )}
        {disabled && <span className="ai-composer-disabled-overlay" aria-hidden="true" />}

        {(() => {
          const iconKey = iconKeyForModelRow(option.label, option.endpoint, option.subtitle)
          return iconKey ? <ProviderIcon id={iconKey} size={14} className="ai-composer-provider-item-icon" /> : null
        })()}
        <span className="ai-composer-provider-item-label">{option.label}</span>
        {option.subtitle && (
          <span className="ai-composer-provider-item-subtitle">{option.subtitle}</span>
        )}

        {option.dispatchActivity?.State && (
          <DispatchActivityBadge activity={option.dispatchActivity} t={t} />
        )}

        {cooling && countdown && (
          <span className="ai-composer-cooldown-countdown" aria-hidden="true">{countdown}</span>
        )}
        {disabled && reasonLabel && (
          <span className="ai-composer-disabled-reason">{reasonLabel}</span>
        )}

        {active && <Check size={14} />}
      </button>
      {(cooling || disabled) && (
        <span id={descId} className="sr-only">{ariaDescription}</span>
      )}
    </>
  )
}
