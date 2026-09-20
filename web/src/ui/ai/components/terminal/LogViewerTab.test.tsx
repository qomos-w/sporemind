import { describe, expect, it, vi } from 'vitest'

// The LogViewerTab module imports application/generated-client which
// requires @qomos/gospore-client (file: dep unavailable in worktree).
// Mock both so the module graph resolves cleanly for pure-helper tests.
vi.mock('@qomos/gospore-client', () => ({
  GosporeClient: class {},
  WebSocketTransport: class {},
  SchemaRegistry: class {},
  BinaryCodec: class {},
}))
vi.mock('../../../../application/generated-client', () => ({
  client: {},
}))

import {
  normalizeLevel,
  formatTimestamp,
  formatFieldValue,
  truncateFieldValue,
  contentKey,
  normalizeStreamEntry,
  normalizeQueryEntry,
  estimateExpandedRowHeight,
  rowHeightFor,
  mergeEntries,
  buildPrefixHeights,
  computeVisibleRange,
  type LogEntry,
  type Level,
} from './LogViewerTab'

// ── normalizeLevel ──

describe('normalizeLevel', () => {
  it('normalizes known levels', () => {
    expect(normalizeLevel('debug')).toBe('debug')
    expect(normalizeLevel('info')).toBe('info')
    expect(normalizeLevel('warn')).toBe('warn')
    expect(normalizeLevel('error')).toBe('error')
  })

  it('handles case-insensitive input', () => {
    expect(normalizeLevel('DEBUG')).toBe('debug')
    expect(normalizeLevel('Info')).toBe('info')
    expect(normalizeLevel('WARN')).toBe('warn')
    expect(normalizeLevel('ERROR')).toBe('error')
  })

  it('falls back to info for unknown levels', () => {
    expect(normalizeLevel('')).toBe('info')
    expect(normalizeLevel('trace')).toBe('info')
    expect(normalizeLevel('fatal')).toBe('info')
  })
})

// ── formatTimestamp ──

describe('formatTimestamp', () => {
  it('returns a non-empty string for valid RFC3339Nano', () => {
    const result = formatTimestamp('2026-08-21T10:30:00.123456789Z')
    expect(result.length).toBeGreaterThan(0)
    // Should contain date parts
    expect(result).toMatch(/2026/)
  })

  it('returns the raw string on parse failure', () => {
    expect(formatTimestamp('')).toBe('')
    expect(formatTimestamp('not-a-date')).toBe('not-a-date')
  })
})

// ── formatFieldValue ──

describe('formatFieldValue', () => {
  it('handles null and undefined', () => {
    expect(formatFieldValue(null)).toBe('null')
    expect(formatFieldValue(undefined)).toBe('')
  })

  it('stringifies objects', () => {
    const obj = { foo: 'bar' }
    expect(formatFieldValue(obj)).toBe('{"foo":"bar"}')
  })

  it('returns strings as-is', () => {
    expect(formatFieldValue('hello')).toBe('hello')
  })

  it('converts numbers', () => {
    expect(formatFieldValue(42)).toBe('42')
  })
})

// ── truncateFieldValue ──

describe('truncateFieldValue', () => {
  it('returns short strings unchanged', () => {
    expect(truncateFieldValue('short')).toBe('short')
  })

  it('truncates long strings with ellipsis', () => {
    const long = 'a'.repeat(100)
    const result = truncateFieldValue(long, 60)
    expect(result.length).toBe(61) // 60 chars + ellipsis
    expect(result).toMatch(/…$/)
  })
})

// ── contentKey ──

describe('contentKey', () => {
  it('produces a stable key from source/timestamp/caller/line/message', () => {
    const key1 = contentKey({
      source: 'backend', timestamp: '2026-08-21T10:00:00Z',
      callerFile: 'foo.go', callerLine: 42, message: 'hello',
    })
    const key2 = contentKey({
      source: 'backend', timestamp: '2026-08-21T10:00:00Z',
      callerFile: 'foo.go', callerLine: 42, message: 'hello',
    })
    expect(key1).toBe(key2)
    const key3 = contentKey({
      source: 'console', timestamp: '2026-08-21T10:00:00Z',
      callerFile: 'bar.js', callerLine: 7, message: 'world',
    })
    expect(key3).not.toBe(key1)
  })
})

// ── normalizeStreamEntry ──

describe('normalizeStreamEntry', () => {
  it('converts a WorkspaceLogStreamEntry to a LogEntry', () => {
    const entry = normalizeStreamEntry({
      Timestamp: '2026-08-21T10:00:00Z',
      Level: 'warn',
      CallerFile: 'server/handler.go',
      CallerLine: 123,
      Message: 'request timed out',
      Fields: { duration_ms: 5000 },
      Source: 'backend',
      Seq: 42,
    })
    expect(entry.level).toBe('warn')
    expect(entry.timestamp).toBe('2026-08-21T10:00:00Z')
    expect(entry.callerFile).toBe('server/handler.go')
    expect(entry.callerLine).toBe(123)
    expect(entry.message).toBe('request timed out')
    expect(entry.fields).toEqual({ duration_ms: 5000 })
    expect(entry.source).toBe('backend')
    expect(entry.seq).toBe(42)
    expect(entry.key).toContain('backend')
    expect(entry.key).toContain('server/handler.go')
  })

  it('handles missing fields', () => {
    const entry = normalizeStreamEntry({
      Timestamp: '2026-08-21T10:00:00Z',
      Level: 'info',
      CallerFile: '',
      CallerLine: 0,
      Message: 'test',
      Source: 'console',
      Seq: 1,
    })
    expect(entry.callerFile).toBe('')
    expect(entry.fields).toEqual({})
  })
})

// ── normalizeQueryEntry ──

describe('normalizeQueryEntry', () => {
  it('converts a WorkspaceLogEntry to a LogEntry', () => {
    const entry = normalizeQueryEntry({
      Timestamp: '2026-08-21T09:59:00Z',
      Level: 'error',
      Caller: 'server/db.go:88',
      Message: 'connection failed',
      Fields: { err: 'timed out' },
      CallerFile: 'server/db.go',
      CallerLine: 88,
    })
    expect(entry.level).toBe('error')
    expect(entry.callerFile).toBe('server/db.go')
    expect(entry.callerLine).toBe(88)
    expect(entry.message).toBe('connection failed')
    expect(entry.seq).toBe(0)
    expect(entry.source).toBe('backend')
  })

  it('falls back to empty callerFile/callerLine when missing', () => {
    const entry = normalizeQueryEntry({
      Timestamp: '2026-08-21T09:59:00Z',
      Level: 'info',
      Caller: '',
      Message: 'no caller',
    })
    expect(entry.callerFile).toBe('')
    expect(entry.callerLine).toBe(0)
  })
})

// ── estimateExpandedRowHeight ──

describe('estimateExpandedRowHeight', () => {
  it('returns a minimum height for short messages', () => {
    const h = estimateExpandedRowHeight({ message: 'hi', fields: {} })
    expect(h).toBeGreaterThanOrEqual(40)
    expect(h).toBeLessThan(500)
  })

  it('grows with message length', () => {
    const short = estimateExpandedRowHeight({ message: 'hi', fields: {} })
    const long = estimateExpandedRowHeight({ message: 'a'.repeat(500), fields: {} })
    expect(long).toBeGreaterThan(short)
  })

  it('grows with field content', () => {
    const noFields = estimateExpandedRowHeight({ message: 'test', fields: {} })
    const withFields = estimateExpandedRowHeight({ message: 'test', fields: { k1: 'v1', k2: 'v2' } })
    expect(withFields).toBeGreaterThan(noFields)
  })

  it('caps at MAX_EXPANDED_ROW_HEIGHT', () => {
    const huge = estimateExpandedRowHeight({
      message: 'x'.repeat(10000),
      fields: { big: 'y'.repeat(10000) },
    })
    expect(huge).toBeLessThanOrEqual(500)
  })
})

// ── rowHeightFor ──

describe('rowHeightFor', () => {
  it('returns ROW_HEIGHT when collapsed', () => {
    const h = rowHeightFor({ message: 'x', fields: {} }, false)
    expect(h).toBe(36)
  })

  it('returns estimated height when expanded', () => {
    const h = rowHeightFor({ message: 'x', fields: {} }, true)
    expect(h).toBeGreaterThan(36)
  })
})

// ── mergeEntries ──

describe('mergeEntries', () => {
  function makeEntry(timestamp: string, seq: number, msg = 'm'): LogEntry {
    return {
      key: contentKey({ source: 'backend', timestamp, callerFile: 'f.go', callerLine: 1, message: msg }),
      timestamp, level: 'info' as Level, callerFile: 'f.go', callerLine: 1,
      message: msg, fields: {}, source: 'backend' as const, seq,
    }
  }

  it('merges two sorted arrays in chronological order', () => {
    const prev = [makeEntry('2026-01-01T00:00:01Z', 1), makeEntry('2026-01-01T00:00:03Z', 3)]
    const incoming = [makeEntry('2026-01-01T00:00:02Z', 2), makeEntry('2026-01-01T00:00:04Z', 4)]
    const merged = mergeEntries(prev, incoming)
    expect(merged.map((e) => e.seq)).toEqual([1, 2, 3, 4])
  })

  it('deduplicates by key', () => {
    const prev = [makeEntry('2026-01-01T00:00:01Z', 1)]
    const incoming = [makeEntry('2026-01-01T00:00:01Z', 1)] // same timestamp + file + line + message
    const merged = mergeEntries(prev, incoming)
    expect(merged.length).toBe(1)
  })

  it('returns prev unchanged when all incoming are duplicates', () => {
    const prev = [makeEntry('2026-01-01T00:00:01Z', 1)]
    const merged = mergeEntries(prev, [makeEntry('2026-01-01T00:00:01Z', 1)])
    expect(merged).toBe(prev) // same reference
  })
})

// ── buildPrefixHeights ──

describe('buildPrefixHeights', () => {
  function makeEntry(ts: string): LogEntry {
    return {
      key: contentKey({ source: 'backend', timestamp: ts, callerFile: 'f.go', callerLine: 1, message: 'm' }),
      timestamp: ts, level: 'info' as Level, callerFile: 'f.go', callerLine: 1,
      message: 'm', fields: {}, source: 'backend' as const, seq: 0,
    }
  }

  it('returns cumulative heights for collapsed rows', () => {
    const entries = [makeEntry('a'), makeEntry('b'), makeEntry('c')]
    const heights = buildPrefixHeights(entries, new Set())
    expect(heights).toEqual([36, 72, 108])
  })

  it('accounts for expanded rows', () => {
    const entries = [makeEntry('a'), makeEntry('b'), makeEntry('c')]
    const expanded = new Set([entries[1]!.key])
    const heights = buildPrefixHeights(entries, expanded)
    expect(heights[0]).toBe(36)
    expect(heights[1]).toBeGreaterThan(72) // expanded row
    expect(heights[2]).toBe(heights[1]! + 36) // row 2 is collapsed
  })
})

// ── computeVisibleRange ──

describe('computeVisibleRange', () => {
  it('returns empty range for empty input', () => {
    expect(computeVisibleRange([], 0, 600)).toEqual({ start: 0, end: 0 })
  })

  it('returns visible range based on scrollTop and viewport', () => {
    // 10 rows, 36px each = 360px total
    const heights = [36, 72, 108, 144, 180, 216, 252, 288, 324, 360]
    const range = computeVisibleRange(heights, 0, 100)
    expect(range.start).toBe(0)
    expect(range.end).toBeGreaterThan(0)
    expect(range.end).toBeLessThanOrEqual(10)
  })

  it('finds correct start row for mid-viewport scroll', () => {
    const heights = [36, 72, 108, 144, 180, 216, 252, 288, 324, 360]
    // scrollTop = 80 → rows 2 (72) and later are visible
    const range = computeVisibleRange(heights, 80, 100)
    expect(range.start).toBe(2) // row 2 starts at offset 72, ends at 108; 72 < 80 ≤ 108
  })
})

// ── Virtualization at scale ──

describe('virtualization at 10k entries', () => {
  function makeEntry(i: number): LogEntry {
    const ts = `2026-08-21T00:00:${String(Math.floor(i / 60)).padStart(2, '0')}.${String(i % 60).padStart(3, '0')}Z`
    return {
      key: `k${i}`,
      timestamp: ts,
      level: 'info' as Level,
      callerFile: `pkg/worker/worker_${i % 50}.go`,
      callerLine: (i % 900) + 1,
      message: `handling item ${i}`,
      fields: { idx: i },
      source: 'backend' as const,
      seq: i,
    }
  }

  it('computes prefix heights and a bounded visible window for 10k entries', () => {
    const entries = Array.from({ length: 10_000 }, (_, i) => makeEntry(i))
    const expanded = new Set<string>()

    const t0 = performance.now()
    const heights = buildPrefixHeights(entries, expanded)
    const t1 = performance.now()

    expect(heights.length).toBe(10_000)
    expect(heights[heights.length - 1]).toBe(10_000 * 36)

    // Scan to the middle of the list
    const midScrollTop = Math.floor(heights[5000] ?? 180000)
    const range = computeVisibleRange(heights, midScrollTop, 600)
    const t2 = performance.now()

    // The rendered window must be a small, constant fraction of the list
    expect(range.end - range.start).toBeLessThan(50)
    // Computing the full prefix + window must be fast (< 100ms is generous
    // for a 10k list; in practice it is < 5ms)
    expect(t1 - t0).toBeLessThan(100)
    expect(t2 - t1).toBeLessThan(100)

    // Rows at the end of the list also resolve correctly
    const tailRange = computeVisibleRange(heights, heights[9999]! - 200, 600)
    expect(tailRange.end).toBe(10_000)
  })

  it('keeps the visible window bounded even with many expanded rows', () => {
    const entries = Array.from({ length: 10_000 }, (_, i) => makeEntry(i))
    const expanded = new Set(entries.slice(0, 50).map((e) => e.key)) // expand first 50
    const heights = buildPrefixHeights(entries, expanded)
    const range = computeVisibleRange(heights, 0, 600)
    expect(range.end - range.start).toBeLessThan(50)
  })
})