import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Search } from 'lucide-react'
import { useI18n } from '../../../../i18n'
import type { I18nKey } from '../../../../i18n/types'
import { client } from '../../../../application/generated-client'
import * as workspaceClient from '../../../../gen-clients/workspace/client'
import type { WorkspaceLogEntry } from '../../../../gen-clients/system/types'
import type { TerminalTabBodyProps } from './tabRegistry'
import './AppConsoleTab.css'

/**
 * AppConsoleTab renders persisted frontend console.* logs captured by the
 * desktop (Wails) bridge. It is the real implementation behind the
 * "app-console" terminal tab type.
 *
 * Data source: {@link workspaceClient.logsQuery} with Source="console". The
 * backend {@code ConsoleStore} exposes a Tail (latest) and a Before-cursor
 * (older) paging surface; this component renders the latest page on mount,
 * pages backwards when the user scrolls to the top, and polls for new entries
 * while the tab is active (paused when hidden via unmount or {@link active}).
 *
 * Level + text filtering is performed client-side over the loaded data, as the
 * backend paging is cursor-driven and not filter-aware for the console source.
 */

const PAGE_SIZE = 200
/** Live-poll cadence for new console entries (>= 2s per task spec). */
const POLL_INTERVAL_MS = 3000
const SCROLL_TOP_THRESHOLD = 48
const SCROLL_BOTTOM_THRESHOLD = 48

/** Levels exposed as toggle filters. Unknown levels still render, just unfiltered. */
const FILTER_LEVELS = ['error', 'warn', 'info', 'debug'] as const
type FilterLevel = (typeof FILTER_LEVELS)[number]

type ConsoleEntry = WorkspaceLogEntry

function entryKey(e: ConsoleEntry): string {
  // Console entries carry no stable id; the composite of timestamp + level +
  // message + caller is unique enough for a viewer and powers dedupe on poll.
  return `${e.Timestamp}|${e.Level}|${e.Message}|${e.Caller ?? ''}`
}

function normalizeLevel(level: string): string {
  return level.toLowerCase()
}

function levelKey(lv: FilterLevel): I18nKey {
  return `terminalPanel.appConsole.level.${lv}` as I18nKey
}

function formatTime(ts: string): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  const pad = (n: number, l = 2) => String(n).padStart(l, '0')
  // Display layer: render in the user's local timezone (data is UTC).
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`
}

/** Pretty-prints a JSON object/array payload; otherwise returns the raw text. */
function prettyMessage(msg: string): string {
  const trimmed = msg.trim()
  if (
    (trimmed.startsWith('{') && trimmed.endsWith('}')) ||
    (trimmed.startsWith('[') && trimmed.endsWith(']'))
  ) {
    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2)
    } catch {
      // not valid JSON — fall through to the raw message
    }
  }
  return msg
}

export interface AppConsoleTabProps extends TerminalTabBodyProps {
  /**
   * When false, live polling pauses (e.g. the tab is hidden). Defaults to true.
   * TerminalPanel only mounts the active tab body, so hidden tabs are also
   * unmounted and this flag is belt-and-suspenders for explicit pause/testing.
   */
  active?: boolean
}

export function AppConsoleTab({ active = true }: AppConsoleTabProps) {
  const { t } = useI18n()

  const [entries, setEntries] = useState<ConsoleEntry[]>([])
  const [nextBefore, setNextBefore] = useState<string | null>(null)
  const [noMore, setNoMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [degraded, setDegraded] = useState(false)
  const [ready, setReady] = useState(false)
  const [selectedLevels, setSelectedLevels] = useState<Set<string>>(new Set())
  const [textFilter, setTextFilter] = useState('')
  const [following, setFollowing] = useState(true)
  const [reloadKey, setReloadKey] = useState(0)

  const scrollRef = useRef<HTMLDivElement>(null)
  const nextBeforeRef = useRef<string | null>(null)
  const noMoreRef = useRef(false)
  const loadingOlderRef = useRef(false)
  const followingRef = useRef(true)
  const pendingPrependRef = useRef<number | null>(null)

  useEffect(() => { followingRef.current = following }, [following])

  const loadInitial = useCallback(async (signal: { cancelled: boolean }) => {
    setLoading(true)
    setError(null)
    setDegraded(false)
    try {
      const resp = await workspaceClient.logsQuery(client, { Source: 'console', Limit: PAGE_SIZE })
      if (signal.cancelled) return
      setEntries(resp.Items ?? [])
      if (resp.Truncated && resp.NextBefore) {
        nextBeforeRef.current = resp.NextBefore
        setNextBefore(resp.NextBefore)
        noMoreRef.current = false
        setNoMore(false)
      } else {
        nextBeforeRef.current = null
        setNextBefore(null)
        noMoreRef.current = true
        setNoMore(true)
      }
    } catch (err) {
      if (signal.cancelled) return
      const msg = err instanceof Error ? err.message : String(err)
      if (msg.includes('console log source not available')) {
        setDegraded(true)
      } else {
        setError(msg)
      }
    } finally {
      if (!signal.cancelled) {
        setLoading(false)
        setReady(true)
      }
    }
  }, [])

  // Initial / retry load.
  useEffect(() => {
    const signal = { cancelled: false }
    void loadInitial(signal)
    return () => { signal.cancelled = true }
  }, [loadInitial, reloadKey])

  const loadOlder = useCallback(async () => {
    if (loadingOlderRef.current) return
    const cursor = nextBeforeRef.current
    if (!cursor || noMoreRef.current) return
    loadingOlderRef.current = true
    setLoadingOlder(true)
    const el = scrollRef.current
    const prevHeight = el ? el.scrollHeight : 0
    if (el) pendingPrependRef.current = prevHeight
    try {
      const resp = await workspaceClient.logsQuery(client, {
        Source: 'console',
        Limit: PAGE_SIZE,
        Before: cursor,
      })
      setEntries(prev => {
        const existing = new Set(prev.map(entryKey))
        const older = (resp.Items ?? []).filter(e => !existing.has(entryKey(e)))
        if (older.length === 0) return prev
        return [...older, ...prev]
      })
      if (resp.Truncated && resp.NextBefore) {
        nextBeforeRef.current = resp.NextBefore
        setNextBefore(resp.NextBefore)
      } else {
        nextBeforeRef.current = null
        setNextBefore(null)
        noMoreRef.current = true
        setNoMore(true)
      }
    } catch {
      // Keep the cursor; the user can retry by scrolling again.
    } finally {
      loadingOlderRef.current = false
      setLoadingOlder(false)
    }
  }, [])

  // Restore the scroll anchor after a prepend so the viewport doesn't jump.
  useEffect(() => {
    const anchor = pendingPrependRef.current
    if (anchor === null) return
    pendingPrependRef.current = null
    const el = scrollRef.current
    if (!el) return
    el.scrollTop = el.scrollHeight - anchor + el.scrollTop
  }, [entries])

  // Keep the viewport pinned to the bottom while following new logs.
  useEffect(() => {
    if (!followingRef.current) return
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [entries])

  const refreshTail = useCallback(async () => {
    if (degraded) return
    try {
      const resp = await workspaceClient.logsQuery(client, { Source: 'console', Limit: PAGE_SIZE })
      setEntries(prev => {
        if (prev.length === 0) return resp.Items ?? []
        const existing = new Set(prev.map(entryKey))
        const fresh = (resp.Items ?? []).filter(e => !existing.has(entryKey(e)))
        if (fresh.length === 0) return prev
        return [...prev, ...fresh]
      })
    } catch {
      // Transient poll failure; the next tick retries.
    }
  }, [degraded])

  // Live polling — paused when the tab is hidden (active=false) or on the
  // degraded (no console source) path. Unmount also clears the interval.
  useEffect(() => {
    if (!active || !ready || degraded) return
    const id = window.setInterval(() => { void refreshTail() }, POLL_INTERVAL_MS)
    return () => { window.clearInterval(id) }
  }, [active, ready, degraded, refreshTail])

  const handleScroll = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < SCROLL_BOTTOM_THRESHOLD
    if (atBottom !== followingRef.current) {
      followingRef.current = atBottom
      setFollowing(atBottom)
    }
    if (el.scrollTop < SCROLL_TOP_THRESHOLD) {
      void loadOlder()
    }
  }, [loadOlder])

  const toggleLevel = useCallback((lv: string) => {
    setSelectedLevels(prev => {
      const next = new Set(prev)
      if (next.has(lv)) next.delete(lv)
      else next.add(lv)
      return next
    })
  }, [])

  const clearLevels = useCallback(() => setSelectedLevels(new Set()), [])

  const filtered = useMemo(() => {
    if (selectedLevels.size === 0 && !textFilter) return entries
    const needle = textFilter.trim().toLowerCase()
    return entries.filter(e => {
      const lv = normalizeLevel(e.Level)
      if (selectedLevels.size > 0 && !selectedLevels.has(lv)) return false
      if (needle) {
        const hay = `${e.Message} ${e.Caller ?? ''}`.toLowerCase()
        if (!hay.includes(needle)) return false
      }
      return true
    })
  }, [entries, selectedLevels, textFilter])

  const retry = useCallback(() => {
    setReady(false)
    setEntries([])
    nextBeforeRef.current = null
    setNextBefore(null)
    noMoreRef.current = false
    setNoMore(false)
    setReloadKey(k => k + 1)
  }, [])

  const hasFields = (e: ConsoleEntry): boolean =>
    !!e.Fields && Object.keys(e.Fields).length > 0

  return (
    <div className="app-console-tab">
      <div className="app-console-filterbar">
        <div className="app-console-filter-levels">
          <span className="app-console-filter-label">{t('terminalPanel.appConsole.filter.levels')}</span>
          {FILTER_LEVELS.map(lv => {
            const selected = selectedLevels.has(lv)
            return (
              <button
                key={lv}
                type="button"
                className={`app-console-level-chip${selected ? ' active' : ''}`}
                onClick={() => toggleLevel(lv)}
                aria-pressed={selected}
              >
                <span className={`app-console-level-dot level-${lv}`} />
                {t(levelKey(lv))}
              </button>
            )
          })}
          {selectedLevels.size > 0 && (
            <button
              type="button"
              className="app-console-filter-clear"
              onClick={clearLevels}
              aria-label={t('terminalPanel.appConsole.filter.text')}
            >
              ×
            </button>
          )}
        </div>
        <div className="app-console-filter-text">
          <Search size={13} />
          <input
            type="text"
            value={textFilter}
            onChange={e => setTextFilter(e.target.value)}
            placeholder={t('terminalPanel.appConsole.filter.text')}
            aria-label={t('terminalPanel.appConsole.filter.text')}
          />
        </div>
      </div>

      {degraded ? (
        <div className="terminal-placeholder app-console-empty">
          <div className="terminal-placeholder-title">{t('terminalPanel.appConsole.empty.title')}</div>
          <div className="terminal-placeholder-hint">{t('terminalPanel.appConsole.empty.hint')}</div>
        </div>
      ) : loading && entries.length === 0 ? (
        <div className="app-console-empty">{t('terminalPanel.appConsole.loading')}</div>
      ) : error ? (
        <div className="terminal-placeholder app-console-empty">
          <div className="terminal-placeholder-title">{t('terminalPanel.appConsole.error')}</div>
          <div className="terminal-placeholder-hint">{error}</div>
          <button type="button" className="app-console-retry" onClick={retry}>
            {t('terminalPanel.appConsole.retry')}
          </button>
        </div>
      ) : (
        <div
          className="app-console-scroll"
          ref={scrollRef}
          onScroll={handleScroll}
          data-testid="app-console-scroll"
        >
          <div className="app-console-load-older">
            {loadingOlder ? (
              <span className="app-console-load-hint">{t('terminalPanel.appConsole.loading')}</span>
            ) : noMore ? (
              <span className="app-console-load-hint app-console-no-more">{t('terminalPanel.appConsole.noMore')}</span>
            ) : nextBefore ? (
              <button type="button" className="app-console-load-btn" onClick={() => void loadOlder()}>
                {t('terminalPanel.appConsole.loadOlder')}
              </button>
            ) : null}
          </div>

          {filtered.length === 0 ? (
            <div className="terminal-placeholder app-console-empty">
              <div className="terminal-placeholder-title">
                {entries.length === 0
                  ? t('terminalPanel.appConsole.empty.title')
                  : t('terminalPanel.appConsole.empty.filtered')}
              </div>
              {entries.length === 0 && (
                <div className="terminal-placeholder-hint">{t('terminalPanel.appConsole.empty.hint')}</div>
              )}
            </div>
          ) : (
            filtered.map(e => {
              const lv = normalizeLevel(e.Level)
              return (
                <div key={entryKey(e)} className="app-console-row">
                  <span className="app-console-time">{formatTime(e.Timestamp)}</span>
                  <span className={`app-console-level level-${lv}`}>{lv}</span>
                  <span className="app-console-msg">{prettyMessage(e.Message)}</span>
                  {(e.Caller || hasFields(e)) && (
                    <div className="app-console-meta">
                      {e.Caller && <span className="app-console-caller">{e.Caller}</span>}
                      {hasFields(e) && (
                        <span className="app-console-fields">{JSON.stringify(e.Fields)}</span>
                      )}
                    </div>
                  )}
                </div>
              )
            })
          )}
        </div>
      )}
    </div>
  )
}
