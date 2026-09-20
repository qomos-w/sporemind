import { useCallback, useEffect, useState } from 'react'
import { useI18n } from '../../i18n/provider'

export function formatTime(iso: string | undefined, suffix = ' ago'): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso

  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffSec = Math.floor(diffMs / 1000)
  if (diffSec < 0) return date.toLocaleString()
  if (diffSec < 60) return `${diffSec}s${suffix}`
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin}m${suffix}`
  const diffHour = Math.floor(diffMin / 60)
  if (diffHour < 24) return `${diffHour}h${suffix}`
  const diffDay = Math.floor(diffHour / 24)
  if (diffDay < 7) return `${diffDay}d${suffix}`
  return date.toLocaleDateString()
}

const SECOND_MS = 1000
const MINUTE_MS = 60 * SECOND_MS
const HOUR_MS = 60 * MINUTE_MS
const DAY_MS = 24 * HOUR_MS
const MONTH_MS = 30 * DAY_MS
const YEAR_MS = 365 * DAY_MS

/** Locale-aware relative time ("3 hours ago" / "3小时前"). Pure — the hook
 *  below adds only the periodic refresh. */
export function formatRelativeTime(iso: string | undefined, locale: string, now = Date.now()): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso

  const diffMs = now - date.getTime()
  if (diffMs < 0) return date.toLocaleString()

  const rtf =
    typeof Intl !== 'undefined' && 'RelativeTimeFormat' in Intl
      ? new Intl.RelativeTimeFormat(locale, { numeric: 'always' })
      : null

  const format = (value: number, unit: Intl.RelativeTimeFormatUnit) => {
    if (rtf) {
      return rtf.format(value, unit)
    }
    const abs = Math.abs(value)
    const unitEn = abs === 1 ? unit.slice(0, -1) : unit
    return `${abs} ${unitEn}${value < 0 ? ' ago' : ''}`
  }

  if (diffMs < MINUTE_MS) {
    return format(-Math.floor(diffMs / SECOND_MS), 'seconds')
  }
  if (diffMs < HOUR_MS) {
    return format(-Math.floor(diffMs / MINUTE_MS), 'minutes')
  }
  if (diffMs < DAY_MS) {
    return format(-Math.floor(diffMs / HOUR_MS), 'hours')
  }
  if (diffMs < MONTH_MS) {
    return format(-Math.floor(diffMs / DAY_MS), 'days')
  }
  if (diffMs < YEAR_MS) {
    return format(-Math.floor(diffMs / MONTH_MS), 'months')
  }
  return format(-Math.floor(diffMs / YEAR_MS), 'years')
}

/** Ticking clock for time-windowed UI (e.g. badges that expire). Re-renders
 *  the caller every intervalMs. */
export function useNow(intervalMs = 30000): number {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])

  return now
}

export function useRelativeTime(intervalMs = 30000) {
  const { locale } = useI18n()
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])

  return useCallback(
    (iso: string | undefined): string => formatRelativeTime(iso, locale, now),
    [locale, now]
  )
}
