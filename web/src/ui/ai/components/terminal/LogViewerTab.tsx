import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useI18n } from '../../../../i18n'
import { client } from '../../../../application/generated-client'
import { logsQuery, OnWorkspaceLog } from '../../../../gen-clients/workspace/client'
import type { WorkspaceLogEntry, WorkspaceLogStreamEntry } from '../../../../gen-clients/system/types'
import './LogViewerTab.css'

// ── Constants ──

const PAGE_SIZE = 200
const MAX_PAGES_PER_TRIGGER = 3
const SCROLL_TOP_THRESHOLD = 80
const SCROLL_BOTTOM_THRESHOLD = 40
const ROW_HEIGHT = 36
// Upper bound used as the virtual-window overscan buffer and for newly
// expanded rows before the per-entry estimate is available.
const MAX_EXPANDED_ROW_HEIGHT = 500

// ── Types ──

export interface LogViewerTabProps {
  tab?: { id: string; type: string; title?: string }
  onSetTabTitle?: (tabId: string, title: string | undefined) => void
  onOpenFile?: (filePath: string, line?: number) => void
}

export type Level = 'debug' | 'info' | 'warn' | 'error'

export interface LogEntry {
  key: string
  timestamp: string
  level: Level
  callerFile: string
  callerLine: number
  message: string
  fields: Record<string, unknown>
  source: 'backend' | 'console'
  seq: number
}

interface CursorState {
  backend: { before: string | null; hasMore: boolean }
  console: { before: string | null; hasMore: boolean }
}

// ── Helpers (pure, exported for testing) ──

export const LEVEL_COLORS: Record<Level, string> = {
  debug: 'var(--text-tertiary)',
  info: 'var(--accent-blue)',
  warn: 'var(--accent-amber)',
  error: 'var(--accent-red)',
}

export const LEVELS: Level[] = ['debug', 'info', 'warn', 'error']

export function normalizeLevel(raw: string): Level {
  const l = raw.toLowerCase()
  if (l === 'debug' || l === 'info' || l === 'warn' || l === 'error') return l
  return 'info'
}

export function formatTimestamp(utc: string): string {
  try {
    const d = new Date(utc)
    if (isNaN(d.getTime())) return utc
    return d.toLocaleString(undefined, {
      year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
      hour12: false,
    })
  } catch {
    return utc
  }
}

export function formatFieldValue(v: unknown): string {
  if (v === null) return 'null'
  if (v === undefined) return ''
  if (typeof v === 'object') {
    try { return JSON.stringify(v) } catch { return String(v) }
  }
  return String(v)
}

export function truncateFieldValue(v: string, max = 60): string {
  return v.length > max ? v.slice(0, max) + '…' : v
}

/** Content-based dedup key shared by query and stream forms of the same
 *  backend entry, so an entry loaded via logs_query is not duplicated when
 *  the live stream later delivers the same line. */
export function contentKey(e: {
  source: string
  timestamp: string
  callerFile: string
  callerLine: number
  message: string
}): string {
  return `${e.source}:${e.timestamp}:${e.callerFile}:${e.callerLine}:${e.message}`
}

export function normalizeStreamEntry(e: WorkspaceLogStreamEntry): LogEntry {
  const base = {
    timestamp: e.Timestamp,
    level: normalizeLevel(e.Level),
    callerFile: e.CallerFile || '',
    callerLine: e.CallerLine ?? 0,
    message: e.Message,
    fields: (e.Fields ?? {}) as Record<string, unknown>,
    source: e.Source === 'console' ? 'console' as const : 'backend' as const,
    seq: e.Seq,
  }
  return { ...base, key: contentKey(base) }
}

export function normalizeQueryEntry(e: WorkspaceLogEntry): LogEntry {
  const base = {
    timestamp: e.Timestamp,
    level: normalizeLevel(e.Level),
    callerFile: e.CallerFile ?? '',
    callerLine: e.CallerLine ?? 0,
    message: e.Message,
    fields: (e.Fields ?? {}) as Record<string, unknown>,
    source: 'backend' as const,
    seq: 0,
  }
  return { ...base, key: contentKey(base) }
}

/** Estimate the rendered height (px) of an expanded row based on content
 *  length. Pure function, exported for testing. */
export function estimateExpandedRowHeight(entry: { message: string; fields: Record<string, unknown> }): number {
  const lineH = 18
  const pad = 36
  const msgLines = Math.max(1, Math.ceil(entry.message.length / 100))
  let fieldLines = 0
  for (const v of Object.values(entry.fields)) {
    const s = formatFieldValue(v)
    fieldLines += Math.max(1, Math.ceil(s.length / 90))
  }
  const headerH = Object.keys(entry.fields).length > 0 ? 22 + 16 : 4
  return Math.min(pad + msgLines * lineH + fieldLines * lineH + headerH, MAX_EXPANDED_ROW_HEIGHT)
}

/** Row height for a given entry — fixed ROW_HEIGHT when collapsed, estimated
 *  when expanded. */
export function rowHeightFor(entry: { message: string; fields: Record<string, unknown> }, isExpanded: boolean): number {
  return isExpanded ? estimateExpandedRowHeight(entry) : ROW_HEIGHT
}

/** Merge two chronologically-sorted arrays (by timestamp) into one,
 *  deduplicating by entry key. Pure function, exported for testing. */
export function mergeEntries(prev: LogEntry[], incoming: LogEntry[]): LogEntry[] {
  const seen = new Set(prev.map((e) => e.key))
  const fresh = incoming.filter((e) => !seen.has(e.key))
  if (fresh.length === 0) return prev
  const merged: LogEntry[] = []
  let i = 0, j = 0
  while (i < prev.length && j < fresh.length) {
    const a = prev[i]!
    const b = fresh[j]!
    if (a.timestamp <= b.timestamp) { merged.push(a); i++ }
    else { merged.push(b); j++ }
  }
  while (i < prev.length) { merged.push(prev[i]!); i++ }
  while (j < fresh.length) { merged.push(fresh[j]!); j++ }
  return merged
}

/** Build prefix-sum offsets for virtualization. Returns the cumulative height
 *  at the END of each row (i.e. offsets[i] = total height of rows 0..i). */
export function buildPrefixHeights(entries: LogEntry[], expanded: Set<string>): number[] {
  const heights: number[] = new Array(entries.length)
  let acc = 0
  for (let i = 0; i < entries.length; i++) {
    const e = entries[i]!
    acc += rowHeightFor(e, expanded.has(e.key))
    heights[i] = acc
  }
  return heights
}

/** Compute the visible row window via binary search on prefix heights. */
export function computeVisibleRange(
  prefixHeights: number[],
  scrollTop: number,
  viewportHeight: number,
): { start: number; end: number } {
  const n = prefixHeights.length
  if (n === 0) return { start: 0, end: 0 }
  // Binary search: first row whose END offset >= scrollTop
  let lo = 0, hi = n
  while (lo < hi) {
    const mid = (lo + hi) >>> 1
    if (prefixHeights[mid]! < scrollTop) lo = mid + 1
    else hi = mid
  }
  const start = lo
  // End: first row whose END offset > scrollTop + viewport (plus buffer)
  const endBottom = scrollTop + viewportHeight + MAX_EXPANDED_ROW_HEIGHT
  let endIdx = start
  while (endIdx < n && prefixHeights[endIdx]! <= endBottom) {
    endIdx++
  }
  return { start, end: Math.min(endIdx + 1, n) }
}

// ── Component ──

export function LogViewerTab({ tab: _tab, onSetTabTitle: _onSetTabTitle, onOpenFile }: LogViewerTabProps) {
  const { t } = useI18n()

  const [entries, setEntries] = useState<LogEntry[]>([])
  const [loadingInitial, setLoadingInitial] = useState(true)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [filterLevels, setFilterLevels] = useState<Set<Level>>(new Set())
  const [filterSource, setFilterSource] = useState<'all' | 'backend' | 'console'>('all')
  const [filterCaller, setFilterCaller] = useState('')
  const [filterMessage, setFilterMessage] = useState('')

  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const [stickToBottom, setStickToBottom] = useState(true)
  const scrollRef = useRef<HTMLDivElement>(null)
  const prevScrollHeightRef = useRef(0)
  const prevScrollTopRef = useRef(0)
  const loadingOlderRef = useRef(false)
  const hasMoreOlderRef = useRef(true)
  const entriesRef = useRef<LogEntry[]>([])
  const cursorStateRef = useRef<CursorState>({
    backend: { before: null, hasMore: true },
    console: { before: null, hasMore: false },
  })

  const [viewTop, setViewTop] = useState(0)
  const [viewHeight, setViewHeight] = useState(600)

  // ── Filtered view ──

  const filteredEntries = useMemo(() => {
    return entries.filter((e) => {
      if (filterLevels.size > 0 && !filterLevels.has(e.level)) return false
      if (filterSource !== 'all' && e.source !== filterSource) return false
      if (filterCaller && !e.callerFile.toLowerCase().includes(filterCaller.toLowerCase())) return false
      if (filterMessage && !e.message.toLowerCase().includes(filterMessage.toLowerCase())) return false
      return true
    })
  }, [entries, filterLevels, filterSource, filterCaller, filterMessage])

  // ── Load older page (defined before scroll handler so it's in scope) ──

  const loadOlderPage = useCallback(async () => {
    if (loadingOlderRef.current || !hasMoreOlderRef.current) return
    loadingOlderRef.current = true
    setLoadingOlder(true)

    const el = scrollRef.current
    if (el) {
      prevScrollHeightRef.current = el.scrollHeight
      prevScrollTopRef.current = el.scrollTop
    }

    try {
      const cursor = cursorStateRef.current
      let lastBefore = cursor.backend.before
      if (!lastBefore) {
        cursor.backend.hasMore = false
        hasMoreOlderRef.current = false
        return
      }

      // Server-side filter params (mirrors the front-end filter state so
      // upward paging narrows the log window the same way the loaded data is
      // filtered). Level is single-valued server-side: pass it only when
      // exactly one level is selected; the multi-select case filters on the
      // client. Caller/Message are substring filters and always forwarded.
      const singleLevel = filterLevels.size === 1 ? [...filterLevels][0] : undefined
      const reqLevel = singleLevel ?? undefined

      let pages = 0
      let accumulated = 0

      while (pages < MAX_PAGES_PER_TRIGGER && accumulated < PAGE_SIZE) {
        const resp = await logsQuery(client, {
          Source: filterSource === 'console' ? 'console' : 'backend',
          Level: reqLevel,
          Caller: filterCaller || undefined,
          Message: filterMessage || undefined,
          Before: lastBefore,
          Limit: PAGE_SIZE,
        })
        const items = resp.Items.map(normalizeQueryEntry)

        if (items.length === 0) {
          cursor.backend.hasMore = false
          hasMoreOlderRef.current = false
          break
        }

        // Compute fresh count from the ref snapshot (state updaters must stay
        // pure — React may invoke them twice in dev StrictMode).
        const merged = mergeEntries(entriesRef.current, items)
        accumulated += merged.length - entriesRef.current.length
        entriesRef.current = merged
        setEntries(merged)

        lastBefore = resp.NextBefore ?? items[0]!.timestamp
        cursor.backend.before = lastBefore
        cursor.backend.hasMore = resp.NextBefore !== null || items.length >= PAGE_SIZE
        hasMoreOlderRef.current = cursor.backend.hasMore
        pages++
      }
    } catch (err) {
      console.error('Failed to load older log entries:', err)
    } finally {
      loadingOlderRef.current = false
      setLoadingOlder(false)

      requestAnimationFrame(() => {
        if (el) {
          const heightDiff = el.scrollHeight - prevScrollHeightRef.current
          el.scrollTop = prevScrollTopRef.current + heightDiff
        }
      })
    }
  }, [filterSource, filterLevels, filterCaller, filterMessage])

  // ── Initial load ──

  useEffect(() => {
    let cancelled = false
    hasMoreOlderRef.current = true

    async function loadInitial() {
      setLoadingInitial(true)
      setError(null)
      entriesRef.current = []
      setEntries([])
      try {
        const sources: Array<'backend' | 'console'> =
          filterSource === 'all' ? ['backend', 'console'] : [filterSource]
        const results = await Promise.all(sources.map(async (src) => {
          const resp = await logsQuery(client, { Source: src, Tail: PAGE_SIZE })
          const items = resp.Items.map(normalizeQueryEntry)
          return { src, items, nextBefore: resp.NextBefore ?? null }
        }))

        const all: LogEntry[] = []
        for (const r of results) {
          all.push(...r.items)
          if (r.src === 'backend') {
            cursorStateRef.current.backend = {
              before: r.items.length > 0 ? r.items[0]!.timestamp : null,
              hasMore: r.nextBefore !== null || r.items.length >= PAGE_SIZE,
            }
          } else {
            cursorStateRef.current.console = { before: null, hasMore: false }
            // When only console source is loaded, don't try to page backend
            if (filterSource === 'console') {
              cursorStateRef.current.backend.hasMore = false
            }
          }
        }
        // Sort + dedup
        all.sort((a, b) => a.timestamp.localeCompare(b.timestamp))
        const seen = new Set<string>()
        const deduped: LogEntry[] = []
        for (const e of all) {
          if (seen.has(e.key)) continue
          seen.add(e.key)
          deduped.push(e)
        }
        if (!cancelled) {
          entriesRef.current = deduped
          setEntries(deduped)
          hasMoreOlderRef.current = cursorStateRef.current.backend.hasMore
          setStickToBottom(true)
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      } finally {
        if (!cancelled) setLoadingInitial(false)
      }
    }

    loadInitial()
    return () => { cancelled = true }
  }, [filterSource])

  // ── Live subscription ──

  useEffect(() => {
    const cancel = OnWorkspaceLog(client, (event) => {
      const live = event.Entries.map(normalizeStreamEntry)
      if (live.length === 0) return
      setEntries((prev) => {
        const merged = mergeEntries(prev, live)
        entriesRef.current = merged
        return merged
      })
    })
    return cancel
  }, [])

  // ── Auto-scroll to bottom ──

  useEffect(() => {
    if (stickToBottom && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [entries.length, stickToBottom])

  // ── Viewport size observer ──

  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const ro = new ResizeObserver((entries) => {
      const entry = entries[0]
      if (entry) setViewHeight(entry.contentRect.height)
    })
    ro.observe(el)
    setViewHeight(el.clientHeight)
    return () => ro.disconnect()
  }, [])

  // ── Unified scroll handler (auto-scroll + paging + virtualization) ──

  const handleScroll = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    const { scrollTop, scrollHeight, clientHeight } = el
    setViewTop(scrollTop)
    setStickToBottom(scrollHeight - scrollTop - clientHeight < SCROLL_BOTTOM_THRESHOLD)
    if (scrollTop < SCROLL_TOP_THRESHOLD && !loadingOlderRef.current && hasMoreOlderRef.current) {
      void loadOlderPage()
    }
  }, [loadOlderPage])

  // ── Toggle expand ──

  const toggleExpand = useCallback((key: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])

  // ── File:line jump ──

  const handleCallerClick = useCallback((filePath: string, line: number) => {
    if (filePath && onOpenFile) {
      onOpenFile(filePath, line)
    }
  }, [onOpenFile])

  // ── Virtualization ──

  const prefixHeights = useMemo(
    () => buildPrefixHeights(filteredEntries, expanded),
    [filteredEntries, expanded],
  )
  const totalHeight = prefixHeights.length > 0 ? prefixHeights[prefixHeights.length - 1] : 0

  const visibleRange = useMemo(
    () => computeVisibleRange(prefixHeights, viewTop, viewHeight),
    [prefixHeights, viewTop, viewHeight],
  )

  // ── Render a single row ──

  const renderRow = useCallback((entry: LogEntry, idx: number) => {
    const isExp = expanded.has(entry.key)
    const offset = idx > 0 ? prefixHeights[idx - 1] : 0
    const h = rowHeightFor(entry, isExp)
    const localTime = formatTimestamp(entry.timestamp)
    const callerStr = entry.callerFile
      ? `${entry.callerFile.split('/').pop()}:${entry.callerLine}`
      : ''

    return (
      <div
        key={entry.key}
        className={`log-row${isExp ? ' expanded' : ''}`}
        style={{ position: 'absolute', top: offset, height: h, left: 0, right: 0 }}
        onClick={() => toggleExpand(entry.key)}
      >
        <span className="log-cell log-ts">{localTime}</span>
        <span className="log-cell log-level" style={{ color: LEVEL_COLORS[entry.level] }}>
          {entry.level.toUpperCase()}
        </span>
        {callerStr ? (
          <span
            className="log-cell log-caller"
            title={`${entry.callerFile}:${entry.callerLine}`}
            onClick={(e) => {
              e.stopPropagation()
              handleCallerClick(entry.callerFile, entry.callerLine)
            }}
          >
            {callerStr}
          </span>
        ) : (
          <span className="log-cell log-caller log-caller-empty">—</span>
        )}
        <span className={`log-cell log-message${isExp ? ' expanded' : ''}`}>{entry.message}</span>
        {!isExp && Object.keys(entry.fields).length > 0 && (
          <span className="log-cell log-fields">
            {Object.entries(entry.fields).map(([k, v]) => (
              <span key={k} className="log-field-badge" title={`${k}=${formatFieldValue(v)}`}>
                {k}={truncateFieldValue(formatFieldValue(v))}
              </span>
            ))}
          </span>
        )}
        {isExp && (
          <div className="log-expanded-detail">
            <div className="log-expanded-section">
              <span className="log-expanded-label">Message</span>
              <pre className="log-expanded-message">{entry.message}</pre>
            </div>
            {Object.keys(entry.fields).length > 0 && (
              <div className="log-expanded-section">
                <span className="log-expanded-label">Fields</span>
                <table className="log-expanded-fields-table">
                  <tbody>
                    {Object.entries(entry.fields).map(([k, v]) => (
                      <tr key={k}>
                        <td className="log-expanded-field-key">{k}</td>
                        <td className="log-expanded-field-value">
                          <pre>{formatFieldValue(v)}</pre>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        )}
      </div>
    )
  }, [expanded, prefixHeights, toggleExpand, handleCallerClick])

  // ── Render ──

  return (
    <div className="log-viewer">
      <div className="log-filter-bar">
        <div className="log-filter-group">
          <span className="log-filter-label">{t('terminalPanel.logViewer.filter.levels')}</span>
          <div className="log-filter-levels">
            {LEVELS.map((level) => (
              <button
                key={level}
                className={`log-filter-level-btn${filterLevels.has(level) ? ' active' : ''}`}
                style={{ '--level-color': LEVEL_COLORS[level] } as React.CSSProperties}
                onClick={() => {
                  setFilterLevels((prev) => {
                    const next = new Set(prev)
                    if (next.has(level)) next.delete(level)
                    else next.add(level)
                    return next
                  })
                }}
              >
                {level}
              </button>
            ))}
          </div>
        </div>
        <div className="log-filter-group">
          <span className="log-filter-label">{t('terminalPanel.logViewer.filter.source')}</span>
          <div className="log-filter-source">
            {(['all', 'backend', 'console'] as const).map((src) => (
              <button
                key={src}
                className={`log-filter-source-btn${filterSource === src ? ' active' : ''}`}
                onClick={() => setFilterSource(src)}
              >
                {src}
              </button>
            ))}
          </div>
        </div>
        <div className="log-filter-group">
          <input
            className="log-filter-input"
            type="text"
            placeholder={t('terminalPanel.logViewer.filter.caller')}
            value={filterCaller}
            onChange={(e) => setFilterCaller(e.target.value)}
          />
        </div>
        <div className="log-filter-group">
          <input
            className="log-filter-input"
            type="text"
            placeholder={t('terminalPanel.logViewer.filter.message')}
            value={filterMessage}
            onChange={(e) => setFilterMessage(e.target.value)}
          />
        </div>
        <span className="log-filter-count">
          {t('terminalPanel.logViewer.loadedCount', { count: filteredEntries.length })}
        </span>
      </div>

      <div className="log-body">
        {loadingInitial && (
          <div className="log-loading">{t('terminalPanel.logViewer.loading')}</div>
        )}
        {error && (
          <div className="log-error">{error}</div>
        )}
        {!loadingInitial && !error && filteredEntries.length === 0 && (
          <div className="log-empty">{t('terminalPanel.logViewer.noEntries')}</div>
        )}
        {!loadingInitial && !error && filteredEntries.length > 0 && (
          <div className="log-scroll-container" ref={scrollRef} onScroll={handleScroll}>
            <div className="log-virtual-sizer" style={{ height: totalHeight }}>
              {filteredEntries
                .slice(visibleRange.start, visibleRange.end)
                .map((entry, i) => renderRow(entry, visibleRange.start + i))}
            </div>
            {loadingOlder && (
              <div className="log-loading-older">{t('terminalPanel.logViewer.loading')}</div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}