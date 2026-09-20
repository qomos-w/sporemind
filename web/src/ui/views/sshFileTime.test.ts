import { describe, it, expect } from 'vitest'
import { formatSshModified } from './sshFileTime'

function expectedLocal(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

describe('formatSshModified', () => {
  it('normalizes RFC3339 with timezone offset to compact local time', () => {
    const input = '2026-08-14T12:34:56+08:00'
    expect(formatSshModified(input)).toBe(expectedLocal(input))
  })

  it('normalizes RFC3339 UTC (Z) values', () => {
    const input = '2026-01-01T00:00:00Z'
    expect(formatSshModified(input)).toBe(expectedLocal(input))
  })

  it('drops seconds and timezone so the result fits one line', () => {
    const out = formatSshModified('2026-08-14T12:34:56+08:00')
    expect(out).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
    expect(out.length).toBe(16)
  })

  it('passes through GNU long-iso ls dates unchanged', () => {
    expect(formatSshModified('2026-08-14 12:34')).toBe('2026-08-14 12:34')
  })

  it('passes through BusyBox/BSD ls dates unchanged', () => {
    expect(formatSshModified('Aug 14 12:34')).toBe('Aug 14 12:34')
  })

  it('passes through unparseable RFC3339-looking values unchanged', () => {
    expect(formatSshModified('2026-13-45T99:99:99Z')).toBe('2026-13-45T99:99:99Z')
  })

  it('handles empty input', () => {
    expect(formatSshModified(undefined)).toBe('')
    expect(formatSshModified('')).toBe('')
  })
})
