import { useEffect, useRef, useState } from 'react'
import { client } from '../../application/generated-client'
import { pluginLogs } from '../../gen-clients/pluginhost/client'
import type { PluginLogEntry } from '../../gen-clients/system/types'
import { Trash2, AlertCircle, Info, AlertTriangle, Bug } from 'lucide-react'
import './PluginLogPanel.css'

const POLL_INTERVAL_MS = 2000
const POLL_LIMIT = 500

const LEVELS = ['debug', 'info', 'warn', 'error'] as const
type LogLevel = (typeof LEVELS)[number]

const LEVEL_ICONS: Record<LogLevel, typeof Info> = {
  debug: Bug,
  info: Info,
  warn: AlertTriangle,
  error: AlertCircle,
}

const LEVEL_CLASS: Record<LogLevel, string> = {
  debug: 'plugin-log-level-debug',
  info: 'plugin-log-level-info',
  warn: 'plugin-log-level-warn',
  error: 'plugin-log-level-error',
}

const SOURCE_CLASS: Record<string, string> = {
  frontend: 'plugin-log-source-frontend',
  backend: 'plugin-log-source-backend',
}

function levelClass(level: string): string {
  return LEVEL_CLASS[level.toLowerCase() as LogLevel] ?? 'plugin-log-level-debug'
}

function sourceClass(source: string): string {
  return SOURCE_CLASS[source.toLowerCase()] ?? 'plugin-log-source-backend'
}

function formatTime(time: string): string {
  const d = new Date(time)
  if (isNaN(d.getTime())) return time
  const h = String(d.getHours()).padStart(2, '0')
  const m = String(d.getMinutes()).padStart(2, '0')
  const s = String(d.getSeconds()).padStart(2, '0')
  return `${h}:${m}:${s}`
}

export interface PluginLogPanelProps {
  pluginID: string
  visible: boolean
}

/**
 * Live log viewer for a plugin process. Polls pluginhost.plugin_logs on a
 * 2-second cadence and renders a merged frontend+backend log stream with
 * per-level filtering and a local clear. Polling starts/stops with the
 * `visible` prop; cleanup clears the interval on unmount or hide.
 */
export function PluginLogPanel({ pluginID, visible }: PluginLogPanelProps) {
  const [entries, setEntries] = useState<PluginLogEntry[]>([])
  const [enabledLevels, setEnabledLevels] = useState<Set<string>>(new Set(LEVELS))
  const [processState, setProcessState] = useState<string | undefined>(undefined)
  const [crash, setCrash] = useState<string | undefined>(undefined)
  const [dropped, setDropped] = useState<number>(0)
  const [error, setError] = useState<string | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const [height, setHeight] = useState<number | null>(null)

  // Drag-to-resize on the top edge handle. The clamp keeps the frame above
  // the panel at least a quarter of the tab (and never below one header row).
  const onHandlePointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    const panel = panelRef.current
    if (!panel) return
    const startHeight = panel.getBoundingClientRect().height
    const parentHeight = panel.parentElement?.clientHeight ?? 0
    const maxY = parentHeight > 0 ? parentHeight * 0.75 : 480
    const startY = e.clientY
    const el = e.currentTarget
    el.setPointerCapture(e.pointerId)
    const onMove = (ev: PointerEvent) => {
      setHeight(Math.min(Math.max(startHeight + (startY - ev.clientY), 56), maxY))
    }
    const onUp = (ev: PointerEvent) => {
      el.removeEventListener('pointermove', onMove)
      el.removeEventListener('pointerup', onUp)
      el.removeEventListener('pointercancel', onUp)
      try { el.releasePointerCapture(ev.pointerId) } catch {}
    }
    el.addEventListener('pointermove', onMove)
    el.addEventListener('pointerup', onUp)
    el.addEventListener('pointercancel', onUp)
  }

  useEffect(() => {
    if (!visible) return

    let cancelled = false

    const poll = async () => {
      try {
        const resp = await pluginLogs(client, { PluginId: pluginID, Limit: POLL_LIMIT })
        if (cancelled) return
        setEntries(resp.Entries ?? [])
        setProcessState(resp.ProcessState)
        setCrash(resp.Crash)
        setDropped(resp.Dropped ?? 0)
        setError(null)
      } catch (err) {
        if (cancelled) return
        setError(err instanceof Error ? err.message : String(err))
      }
    }

    void poll()
    const id = window.setInterval(() => { void poll() }, POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [pluginID, visible])

  // Auto-scroll to bottom when entries change.
  useEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [entries])

  if (!visible) return null

  const toggleLevel = (level: string) => {
    setEnabledLevels((prev) => {
      const next = new Set(prev)
      if (next.has(level)) next.delete(level)
      else next.add(level)
      return next
    })
  }

  const enableAll = () => {
    setEnabledLevels(new Set(LEVELS))
  }

  const clearEntries = () => {
    setEntries([])
  }

  const allEnabled = LEVELS.every((l) => enabledLevels.has(l))
  const filteredEntries = entries.filter((e) => enabledLevels.has(e.Level.toLowerCase()))
  const crashed = crash !== undefined && crash !== ''

  return (
    <div
      className="plugin-log-panel"
      ref={panelRef}
      style={height !== null ? { height } : undefined}
      data-testid="plugin-log-panel"
    >
      <div
        className="plugin-log-resize"
        role="separator"
        aria-orientation="horizontal"
        aria-label="Resize log panel"
        data-testid="plugin-log-resize"
        onPointerDown={onHandlePointerDown}
      />
      <div className="plugin-log-header">
        <span className="plugin-log-title">Logs</span>
        <div className="plugin-log-filters">
          <button
            type="button"
            className={`plugin-toolbar-btn${allEnabled ? ' plugin-toolbar-btn-active' : ''}`}
            title="Show all levels"
            onClick={enableAll}
          >
            <span style={{ fontSize: 10, fontWeight: 600 }}>All</span>
          </button>
          {LEVELS.map((level) => {
            const Icon = LEVEL_ICONS[level]
            const active = enabledLevels.has(level)
            return (
              <button
                key={level}
                type="button"
                className={`plugin-toolbar-btn${active ? ' plugin-toolbar-btn-active' : ''}`}
                title={`${active ? 'Hide' : 'Show'} ${level} logs`}
                onClick={() => toggleLevel(level)}
              >
                <Icon size={14} />
              </button>
            )
          })}
        </div>
        <span className="plugin-log-count" data-testid="plugin-log-count">{filteredEntries.length}</span>
        <button
          type="button"
          className="plugin-toolbar-btn"
          title="Clear displayed logs"
          onClick={clearEntries}
        >
          <Trash2 size={14} />
        </button>
      </div>
      {crashed && (
        <div className="plugin-log-crash-banner" data-testid="plugin-log-crash">
          <AlertCircle size={12} />
          <span className="plugin-log-crash-text">
            {crash}
            {processState && processState !== 'crashed' ? ` (${processState})` : ''}
          </span>
        </div>
      )}
      {dropped > 0 && (
        <div className="plugin-log-dropped-banner" data-testid="plugin-log-dropped">
          <AlertTriangle size={12} />
          <span>{dropped} log entries dropped (ring full)</span>
        </div>
      )}
      {error && (
        <div className="plugin-log-error-banner">
          <AlertCircle size={12} />
          <span>{error}</span>
        </div>
      )}
      <div className="plugin-log-list" ref={scrollRef}>
        {filteredEntries.map((entry, i) => (
          <div className="plugin-log-row" key={`${entry.Time}-${entry.Level}-${i}`}>
            <span className="plugin-log-time">{formatTime(entry.Time)}</span>
            <span className={`plugin-log-level ${levelClass(entry.Level)}`}>{entry.Level}</span>
            <span className={`plugin-log-source ${sourceClass(entry.Source)}`}>{entry.Source}</span>
            <span className="plugin-log-message">{entry.Message}</span>
          </div>
        ))}
        {filteredEntries.length === 0 && !error && (
          <div className="plugin-log-empty">No log entries</div>
        )}
      </div>
    </div>
  )
}
