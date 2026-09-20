import { useEffect, useMemo, useRef, useState } from 'react'
import { RefreshCw, Gauge, AlertOctagon, X, ChevronLeft, ChevronRight, Maximize2, Minimize2, ChevronDown, ChevronUp, ArrowUp, ArrowDown, ArrowUpDown, Search } from 'lucide-react'
import {
  useAIStats,
  useAIStatsRecords,
  toSince,
  rangeConfig,
  isCustomRange,
  type AIStatsRange,
  type AIStatsTimeRange,
  type CustomRange,
} from '../hooks/useAIStats'
import { ECharts } from './ECharts'
import type { EChartsOption } from 'echarts'
import type {
  AIStatsBucket,
  AIStatsModelStat,
  AIStatsRecord,
} from '../../../gen-types/aistats'
import { useI18n, type I18nKey } from '../../../i18n'
import './AIStatsDashboard.css'

const TIME_RANGES: AIStatsTimeRange[] = ['1h', '24h', '7d', '30d', 'all']
const PAGE_SIZES = [20, 50, 100]

// Collapsed pill badges only surface models with at least this many requests;
// the expanded table always shows every model.
const PILL_MIN_REQUESTS = 30

const ERROR_CODE_COLORS: Record<string, string> = {
  rate_limit: '#f59e0b',
  overloaded: '#f97316',
  auth: '#ec4899',
  timeout: '#06b6d4',
  server_error: '#ef4444',
  client_error: '#8b5cf6',
  content_filter: '#10b981',
  unknown: '#94a3b8',
}

function errCodeLabel(t: (key: I18nKey) => string, code: string): string {
  return t(`aistats.err.${code in ERROR_CODE_COLORS ? code : 'unknown'}` as I18nKey)
}

const C_INPUT = '#3b82f6'
const C_OUTPUT = '#10b981'
const C_CACHE = '#f59e0b'
const C_COST = '#8b5cf6'
const C_ERR = '#ef4444'
const C_P50 = '#f59e0b'
const C_P95 = '#ef4444'
const C_TTFT = '#06b6d4'

function fmtInt(n: number): string {
  return Math.round(n).toLocaleString()
}

function fmtCompact(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return fmtInt(n)
}

function fmtCost(n: number): string {
  if (n === 0) return '$0'
  if (n < 1) return `$${n.toFixed(4)}`
  if (n < 100) return `$${n.toFixed(2)}`
  return `$${fmtCompact(n)}`
}

function fmtMs(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}s`
  return `${Math.round(n)}ms`
}

function fmtPct(n: number): string {
  return `${n.toFixed(1)}%`
}

function bucketLabel(ts: number, range: AIStatsRange): string {
  const d = new Date(ts)
  const pad = (x: number) => String(x).padStart(2, '0')
  const md = `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  if (isCustomRange(range)) return rangeConfig(range).bucketMs >= 24 * 60 * 60_000 ? md : `${md} ${hm}`
  if (range === '1h' || range === '24h') return hm
  return `${md} ${hm}`
}

function currentTheme(): 'light' | 'dark' {
  if (typeof document === 'undefined') return 'light'
  return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light'
}

function sparseLabels(buckets: AIStatsBucket[], range: AIStatsRange): string[] {
  const n = buckets.length
  if (n === 0) return []
  const step = Math.max(1, Math.ceil(n / 8))
  const labels: string[] = []
  for (let i = 0; i < n; i++) {
    labels.push(i % step === 0 || i === n - 1 ? bucketLabel(buckets[i]!.Start, range) : '')
  }
  return labels
}

// ---------------------------------------------------------------------------
// Custom range calendar picker
// ---------------------------------------------------------------------------

function pad2(x: number): string {
  return String(x).padStart(2, '0')
}

function dayKey(d: Date): string {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
}

function fmtCustomShort(r: CustomRange): string {
  const s = new Date(r.sinceMs)
  const e = new Date(r.untilMs)
  return `${pad2(s.getMonth() + 1)}-${pad2(s.getDate())}~${pad2(e.getMonth() + 1)}-${pad2(e.getDate())}`
}

function parseTime(t: string): [number, number] {
  const m = /^(\d{1,2}):(\d{2})$/.exec(t.trim())
  if (!m) return [0, 0]
  return [Math.min(23, Number(m[1])), Math.min(59, Number(m[2]))]
}

function CustomRangePicker({ initial, onApply, onClose }: {
  initial: CustomRange | null
  onApply: (r: CustomRange) => void
  onClose: () => void
}) {
  const { t, locale } = useI18n()
  const weekdays = useMemo(() => [0, 1, 2, 3, 4, 5, 6].map(i => t(`aistats.cal.wd${i}` as I18nKey)), [t])
  const ref = useRef<HTMLDivElement>(null)
  const [month, setMonth] = useState(() => {
    const base = initial ? new Date(initial.sinceMs) : new Date()
    return new Date(base.getFullYear(), base.getMonth(), 1)
  })
  const [startDay, setStartDay] = useState<string | null>(() =>
    initial ? dayKey(new Date(initial.sinceMs)) : dayKey(new Date(Date.now() - 7 * 24 * 60 * 60_000)))
  const [endDay, setEndDay] = useState<string | null>(() =>
    initial ? dayKey(new Date(initial.untilMs)) : dayKey(new Date()))
  const [startTime, setStartTime] = useState(() => {
    const d = initial ? new Date(initial.sinceMs) : null
    return d ? `${pad2(d.getHours())}:${pad2(d.getMinutes())}` : '00:00'
  })
  const [endTime, setEndTime] = useState(() => {
    const d = initial ? new Date(initial.untilMs) : null
    return d ? `${pad2(d.getHours())}:${pad2(d.getMinutes())}` : '23:59'
  })

  useEffect(() => {
    function onDown(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose()
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [onClose])

  const first = new Date(month.getFullYear(), month.getMonth(), 1)
  const daysInMonth = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate()
  const cells: (Date | null)[] = Array.from({ length: first.getDay() }, () => null)
  for (let d = 1; d <= daysInMonth; d++) cells.push(new Date(month.getFullYear(), month.getMonth(), d))

  function pick(d: Date) {
    const k = dayKey(d)
    if (!startDay || endDay) {
      setStartDay(k)
      setEndDay(null)
      return
    }
    if (k < startDay) {
      setStartDay(k)
      return
    }
    setEndDay(k)
  }

  function applyRange() {
    if (!startDay || !endDay) return
    const [sy, sm, sd] = startDay.split('-').map(Number)
    const [ey, em, ed] = endDay.split('-').map(Number)
    const [sh, smin] = parseTime(startTime)
    const [eh, emin] = parseTime(endTime)
    const since = new Date(sy!, sm! - 1, sd!, sh, smin).getTime()
    const until = new Date(ey!, em! - 1, ed!, eh, emin).getTime()
    if (since >= until) return
    onApply({ sinceMs: since, untilMs: until })
  }
  const valid = !!startDay && !!endDay && (() => {
    const [sy, sm, sd] = startDay.split('-').map(Number)
    const [ey, em, ed] = endDay.split('-').map(Number)
    const [sh, smin] = parseTime(startTime)
    const [eh, emin] = parseTime(endTime)
    return new Date(sy!, sm! - 1, sd!, sh, smin).getTime() < new Date(ey!, em! - 1, ed!, eh, emin).getTime()
  })()

  return (
    <div className="ai-aistats-cal-pop" ref={ref}>
      <div className="ai-aistats-cal-head">
        <button className="ai-aistats-cal-nav" onClick={() => setMonth(m => new Date(m.getFullYear(), m.getMonth() - 1, 1))}>
          <ChevronLeft size={14} />
        </button>
        <span className="ai-aistats-cal-title">{month.toLocaleString(locale, { month: 'long', year: 'numeric' })}</span>
        <button className="ai-aistats-cal-nav" onClick={() => setMonth(m => new Date(m.getFullYear(), m.getMonth() + 1, 1))}>
          <ChevronRight size={14} />
        </button>
      </div>
      <div className="ai-aistats-cal-grid">
        {weekdays.map(w => <span key={w} className="ai-aistats-cal-wd">{w}</span>)}
        {cells.map((d, i) => {
          if (!d) return <span key={`e${i}`} className="ai-aistats-cal-empty" />
          const k = dayKey(d)
          const inRange = startDay && endDay && k >= startDay && k <= endDay
          const cls = [
            'ai-aistats-cal-day',
            k === startDay ? 'is-start' : '',
            k === endDay ? 'is-end' : '',
            inRange ? 'in-range' : '',
            k === dayKey(new Date()) ? 'is-today' : '',
          ].filter(Boolean).join(' ')
          return (
            <button key={k} className={cls} data-date={k} onClick={() => pick(d)}>
              {d.getDate()}
            </button>
          )
        })}
      </div>
      <div className="ai-aistats-cal-times">
        <label>
          <span>{t('aistats.cal.start')}</span>
          <input type="time" value={startTime} onChange={e => setStartTime(e.target.value)} />
        </label>
        <label>
          <span>{t('aistats.cal.end')}</span>
          <input type="time" value={endTime} onChange={e => setEndTime(e.target.value)} />
        </label>
      </div>
      <div className="ai-aistats-cal-foot">
        <button className="ai-aistats-cal-btn" onClick={onClose}>{t('aistats.cal.cancel')}</button>
        <button className="ai-aistats-cal-btn primary" disabled={!valid} onClick={applyRange}>{t('aistats.cal.apply')}</button>
      </div>
    </div>
  )
}

function modelKey(m: { Provider: string; Model: string }): string {
  return `${m.Provider}/${m.Model}`
}

interface SelectedModel {
  provider: string
  model: string
}

type Tab = 'overview' | 'records'

function echartsTheme(theme: 'light' | 'dark') {
  const isDark = theme === 'dark'
  return {
    axisLabel: isDark ? '#999' : '#666',
    axisLine: isDark ? '#444' : '#ddd',
    splitLine: isDark ? '#333' : '#eee',
    tooltipBg: isDark ? '#1a1a2e' : '#fff',
    tooltipBorder: isDark ? '#444' : '#ddd',
    tooltipText: isDark ? '#ddd' : '#333',
  }
}

export function AIStatsDashboard() {
  const { t } = useI18n()
  const [timeRange, setTimeRange] = useState<AIStatsRange>('24h')
  const [calOpen, setCalOpen] = useState(false)
  const [selectedModel, setSelectedModel] = useState<SelectedModel | null>(null)
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [tab, setTab] = useState<Tab>('overview')
  // Committed (blur-submitted) model name filter. It drives useAIStats so the
  // backend filters both buckets (charts) and ModelStats (model list), keeping
  // the dashboard and model list in sync. Typing only updates a local draft
  // inside OverviewTab until the input loses focus.
  const [modelFilter, setModelFilter] = useState('')

  const { series, modelSeries, loading, error, lastUpdated, refresh } = useAIStats({
    timeRange,
    autoRefresh,
    selectedModel,
    modelFilter,
  })

  const theme = currentTheme()

  return (
    <div className="ai-aistats">
      <div className="ai-aistats-toolbar">
        <div className="ai-aistats-title">
          <Gauge size={15} />
          <span>{t('aistats.title')}</span>
        </div>
        <div className="ai-aistats-ranges-wrap">
          <div className="ai-aistats-ranges">
            {TIME_RANGES.map(r => (
              <button
                key={r}
                className={`ai-aistats-range-btn ${r === timeRange ? 'active' : ''}`}
                onClick={() => setTimeRange(r)}
              >{r}</button>
            ))}
            <button
              className={`ai-aistats-range-btn ai-aistats-range-custom ${isCustomRange(timeRange) ? 'active' : ''}`}
              onClick={() => setCalOpen(o => !o)}
              title={t('aistats.customTitle')}
            >
              {isCustomRange(timeRange) ? fmtCustomShort(timeRange) : t('aistats.custom')}
            </button>
          </div>
          {calOpen && (
            <CustomRangePicker
              initial={isCustomRange(timeRange) ? timeRange : null}
              onApply={r => {
                setTimeRange(r)
                setCalOpen(false)
              }}
              onClose={() => setCalOpen(false)}
            />
          )}
        </div>
        <div className="ai-aistats-actions">
          <label className="ai-aistats-autorefresh">
            <input type="checkbox" checked={autoRefresh} onChange={e => setAutoRefresh(e.target.checked)} />
            <span>{t('aistats.auto')}</span>
          </label>
          <button className="ai-aistats-sync-btn" onClick={refresh} title={t('aistats.syncTitle')} disabled={loading}>
            <RefreshCw size={14} /> {t('aistats.sync')}
          </button>
          {lastUpdated && <span className="ai-aistats-updated">{new Date(lastUpdated).toLocaleTimeString()}</span>}
        </div>
      </div>

      {selectedModel && (
        <div className="ai-aistats-filter">
          <span>{t('aistats.filtered')}: <strong>{selectedModel.provider}/{selectedModel.model}</strong></span>
          <button onClick={() => setSelectedModel(null)}><X size={12} /> {t('aistats.clear')}</button>
        </div>
      )}

      <div className="ai-aistats-tabs">
        <button className={`ai-aistats-tab ${tab === 'overview' ? 'active' : ''}`} onClick={() => setTab('overview')}>
          {t('aistats.tabOverview')}
        </button>
        <button className={`ai-aistats-tab ${tab === 'records' ? 'active' : ''}`} onClick={() => setTab('records')}>
          {t('aistats.tabRecords')}
        </button>
      </div>

      {error && <div className="ai-aistats-error"><AlertOctagon size={14} /> {error}</div>}

      {tab === 'overview' ? (
        <OverviewTab
          series={series}
          modelSeries={modelSeries}
          loading={loading}
          theme={theme}
          timeRange={timeRange}
          selectedModel={selectedModel}
          onSelectModel={setSelectedModel}
          modelFilter={modelFilter}
          onCommitFilter={setModelFilter}
        />
      ) : (
        <RecordsTab />
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Overview tab — powered by backend series
// ---------------------------------------------------------------------------

function OverviewTab({
  series,
  modelSeries,
  loading,
  theme,
  timeRange,
  selectedModel,
  onSelectModel,
  modelFilter,
  onCommitFilter,
}: {
  series: ReturnType<typeof useAIStats>['series']
  modelSeries: ReturnType<typeof useAIStats>['modelSeries']
  loading: boolean
  theme: 'light' | 'dark'
  timeRange: AIStatsRange
  selectedModel: SelectedModel | null
  onSelectModel: (m: SelectedModel | null) => void
  /** Committed (blur-submitted) model name filter; drives useAIStats. */
  modelFilter: string
  /** Commit the draft filter (called on input blur / Enter). */
  onCommitFilter: (v: string) => void
}) {
  const { t } = useI18n()
  const [modelsExpanded, setModelsExpanded] = useState(false)
  const [pillSort, setPillSort] = useState<string>('requests')
  const [pillSortDir, setPillSortDir] = useState<'asc' | 'desc'>('desc')
  const [tableSort, setTableSort] = useState<string>('requests')
  const [tableSortDir, setTableSortDir] = useState<'asc' | 'desc'>('desc')
  // Local draft of the name filter; committed to the parent (and thus to
  // useAIStats + the model list) only on blur/Enter so charts and list stay
  // in sync instead of filtering per keystroke.
  const [filterDraft, setFilterDraft] = useState(modelFilter)
  // ModelStats always comes from the global series so the models list/pills
  // keep showing every unit; buckets switch to the model-scoped series when a
  // unit is selected so charts reflect that unit only.
  const modelStats = series?.ModelStats ?? []
  const buckets = selectedModel
    ? (modelSeries?.Buckets ?? [])
    : (series?.Buckets ?? [])

  const statMap = useMemo(() => {
    const m = new Map<string, AIStatsModelStat>()
    for (const s of modelStats) {
      m.set(`${s.Provider}/${s.Model}`, s)
    }
    return m
  }, [modelStats])

  const xLabels = useMemo(() => sparseLabels(buckets, timeRange), [buckets, timeRange])

  // --- Chart options (consume pre-computed backend buckets) ---

  const requestsAreaOption = useMemo<EChartsOption | null>(() => {
    if (buckets.length === 0) return null
    const ct = echartsTheme(theme)
    return {
      grid: { left: 44, right: 8, top: 6, bottom: 26 },
      xAxis: { type: 'category', data: xLabels, boundaryGap: false, axisLabel: { fontSize: 10, color: ct.axisLabel, interval: 0 }, axisLine: { lineStyle: { color: ct.axisLine } }, axisTick: { show: false }, splitLine: { show: false } },
      yAxis: { type: 'value', name: 'req', nameTextStyle: { fontSize: 10, color: ct.axisLabel }, axisLabel: { fontSize: 10, color: ct.axisLabel }, splitLine: { lineStyle: { color: ct.splitLine } }, axisLine: { show: false }, axisTick: { show: false }, min: 0 },
      series: [{ type: 'line', data: buckets.map(b => b.Requests), smooth: false, showSymbol: false, lineStyle: { width: 1.5, color: C_INPUT }, areaStyle: { color: C_INPUT, opacity: 0.15 }, itemStyle: { color: C_INPUT } }],
      tooltip: { trigger: 'axis', backgroundColor: ct.tooltipBg, borderColor: ct.tooltipBorder, textStyle: { fontSize: 11, color: ct.tooltipText } },
      animation: true,
    }
  }, [buckets, xLabels, theme])

  const latencyLineOption = useMemo<EChartsOption | null>(() => {
    if (buckets.length === 0) return null
    const ct = echartsTheme(theme)
    return {
      grid: { left: 44, right: 8, top: 6, bottom: 26 },
      xAxis: { type: 'category', data: xLabels, boundaryGap: false, axisLabel: { fontSize: 10, color: ct.axisLabel, interval: 0 }, axisLine: { lineStyle: { color: ct.axisLine } }, axisTick: { show: false }, splitLine: { show: false } },
      yAxis: { type: 'value', name: 'ms', nameTextStyle: { fontSize: 10, color: ct.axisLabel }, axisLabel: { fontSize: 10, color: ct.axisLabel }, splitLine: { lineStyle: { color: ct.splitLine } }, axisLine: { show: false }, axisTick: { show: false }, min: 0 },
      series: [
        { name: 'TTFT', type: 'line', data: buckets.map(b => b.TtftP50), smooth: false, showSymbol: false, lineStyle: { width: 1.5, color: C_TTFT }, itemStyle: { color: C_TTFT } },
        { name: 'p50', type: 'line', data: buckets.map(b => b.LatencyP50), smooth: false, showSymbol: false, lineStyle: { width: 1.5, color: C_P50 }, itemStyle: { color: C_P50 } },
        { name: 'p95', type: 'line', data: buckets.map(b => b.LatencyP95), smooth: false, showSymbol: false, lineStyle: { width: 1.5, color: C_P95 }, itemStyle: { color: C_P95 } },
      ],
      tooltip: { trigger: 'axis', backgroundColor: ct.tooltipBg, borderColor: ct.tooltipBorder, textStyle: { fontSize: 11, color: ct.tooltipText } },
      animation: true,
    }
  }, [buckets, xLabels, theme])

  const tokenSpeedLineOption = useMemo<EChartsOption | null>(() => {
    if (buckets.length === 0) return null
    const ct = echartsTheme(theme)
    const speeds = buckets.map(b => b.LatencySumMs > 0 ? b.OutputTokens / (b.LatencySumMs / 1000) : 0)
    return {
      grid: { left: 44, right: 8, top: 6, bottom: 26 },
      xAxis: { type: 'category', data: xLabels, boundaryGap: false, axisLabel: { fontSize: 10, color: ct.axisLabel, interval: 0 }, axisLine: { lineStyle: { color: ct.axisLine } }, axisTick: { show: false }, splitLine: { show: false } },
      yAxis: { type: 'value', name: 'tok/s', nameTextStyle: { fontSize: 10, color: ct.axisLabel }, axisLabel: { fontSize: 10, color: ct.axisLabel }, splitLine: { lineStyle: { color: ct.splitLine } }, axisLine: { show: false }, axisTick: { show: false }, min: 0 },
      series: [{ type: 'line', data: speeds, smooth: false, showSymbol: false, lineStyle: { width: 1.5, color: C_OUTPUT }, itemStyle: { color: C_OUTPUT } }],
      tooltip: { trigger: 'axis', backgroundColor: ct.tooltipBg, borderColor: ct.tooltipBorder, textStyle: { fontSize: 11, color: ct.tooltipText } },
      animation: true,
    }
  }, [buckets, xLabels, theme])

  const costAreaOption = useMemo<EChartsOption | null>(() => {
    if (buckets.length === 0) return null
    const ct = echartsTheme(theme)
    return {
      grid: { left: 44, right: 8, top: 6, bottom: 26 },
      xAxis: { type: 'category', data: xLabels, boundaryGap: false, axisLabel: { fontSize: 10, color: ct.axisLabel, interval: 0 }, axisLine: { lineStyle: { color: ct.axisLine } }, axisTick: { show: false }, splitLine: { show: false } },
      yAxis: { type: 'value', name: '$', nameTextStyle: { fontSize: 10, color: ct.axisLabel }, axisLabel: { fontSize: 10, color: ct.axisLabel }, splitLine: { lineStyle: { color: ct.splitLine } }, axisLine: { show: false }, axisTick: { show: false }, min: 0 },
      series: [{ type: 'line', data: buckets.map(b => b.Cost), smooth: false, showSymbol: false, lineStyle: { width: 1.5, color: C_COST }, areaStyle: { color: C_COST, opacity: 0.15 }, itemStyle: { color: C_COST } }],
      tooltip: { trigger: 'axis', backgroundColor: ct.tooltipBg, borderColor: ct.tooltipBorder, textStyle: { fontSize: 11, color: ct.tooltipText } },
      animation: true,
    }
  }, [buckets, xLabels, theme])

  const errorRateLineOption = useMemo<EChartsOption | null>(() => {
    if (buckets.length === 0) return null
    const ct = echartsTheme(theme)
    const rates = buckets.map(b => b.Requests > 0 ? (b.Errors / b.Requests) * 100 : 0)
    return {
      grid: { left: 44, right: 8, top: 6, bottom: 26 },
      xAxis: { type: 'category', data: xLabels, boundaryGap: false, axisLabel: { fontSize: 10, color: ct.axisLabel, interval: 0 }, axisLine: { lineStyle: { color: ct.axisLine } }, axisTick: { show: false }, splitLine: { show: false } },
      yAxis: { type: 'value', name: '%', nameTextStyle: { fontSize: 10, color: ct.axisLabel }, axisLabel: { fontSize: 10, color: ct.axisLabel }, splitLine: { lineStyle: { color: ct.splitLine } }, axisLine: { show: false }, axisTick: { show: false }, min: 0 },
      series: [{ type: 'line', data: rates, smooth: false, showSymbol: false, lineStyle: { width: 1.5, color: C_ERR }, itemStyle: { color: C_ERR } }],
      tooltip: { trigger: 'axis', backgroundColor: ct.tooltipBg, borderColor: ct.tooltipBorder, textStyle: { fontSize: 11, color: ct.tooltipText } },
      animation: true,
    }
  }, [buckets, xLabels, theme])

  const cacheHitLineOption = useMemo<EChartsOption | null>(() => {
    if (buckets.length === 0) return null
    const ct = echartsTheme(theme)
    const rates = buckets.map(b => {
      const denom = b.CacheRead + b.InputTokens
      return denom > 0 ? (b.CacheRead / denom) * 100 : 0
    })
    return {
      grid: { left: 44, right: 8, top: 6, bottom: 26 },
      xAxis: { type: 'category', data: xLabels, boundaryGap: false, axisLabel: { fontSize: 10, color: ct.axisLabel, interval: 0 }, axisLine: { lineStyle: { color: ct.axisLine } }, axisTick: { show: false }, splitLine: { show: false } },
      yAxis: { type: 'value', name: '%', nameTextStyle: { fontSize: 10, color: ct.axisLabel }, axisLabel: { fontSize: 10, color: ct.axisLabel }, splitLine: { lineStyle: { color: ct.splitLine } }, axisLine: { show: false }, axisTick: { show: false }, min: 0 },
      series: [{ type: 'line', data: rates, smooth: false, showSymbol: false, lineStyle: { width: 1.5, color: C_CACHE }, itemStyle: { color: C_CACHE } }],
      tooltip: { trigger: 'axis', backgroundColor: ct.tooltipBg, borderColor: ct.tooltipBorder, textStyle: { fontSize: 11, color: ct.tooltipText } },
      animation: true,
    }
  }, [buckets, xLabels, theme])

  // Error code distribution from bucket ErrorCodes maps.
  const errorCodeDist = useMemo<{ option: EChartsOption | null; items: { code: string; label: string; color: string; count: number }[] }>(() => {
    if (buckets.length === 0) return { option: null, items: [] as { code: string; label: string; color: string; count: number }[] }
    const counts = new Map<string, number>()
    for (const b of buckets) {
      for (const [code, cnt] of Object.entries(b.ErrorCodes ?? {})) {
        counts.set(code, (counts.get(code) ?? 0) + cnt)
      }
    }
    const items = [...counts.entries()]
      .map(([code, count]) => {
        const meta = ERROR_CODE_COLORS[code] ?? ERROR_CODE_COLORS.unknown!
        return { code, count, label: errCodeLabel(t, code), color: meta }
      })
      .sort((a, b) => b.count - a.count)
    if (items.length === 0) return { option: null, items }
    const ct = echartsTheme(theme)
    return {
      option: {
        series: [{
          type: 'pie',
          radius: ['50%', '70%'],
          center: ['50%', '50%'],
          avoidLabelOverlap: true,
          itemStyle: { borderRadius: 4, borderColor: ct.tooltipBorder, borderWidth: 2 },
          label: { show: false },
          emphasis: { label: { show: true, fontSize: 12, fontWeight: 'bold' } },
          data: items.map(it => ({ name: it.label, value: it.count, itemStyle: { color: it.color } })),
        }],
        tooltip: { trigger: 'item', formatter: '{b}: {c}' },
      },
      items,
    }
  }, [buckets, theme, t])

  const costDonut = useMemo<{ option: EChartsOption | null; items: { key: string; cost: number; share: number }[] }>(() => {
    const ct = echartsTheme(theme)
    const donut = (data: { name: string; value: number }[]): EChartsOption => ({
      series: [{
        type: 'pie',
        radius: ['50%', '70%'],
        center: ['50%', '50%'],
        avoidLabelOverlap: true,
        itemStyle: { borderRadius: 4, borderColor: ct.tooltipBorder, borderWidth: 2 },
        label: { show: false },
        emphasis: { label: { show: true, fontSize: 12, fontWeight: 'bold' } },
        data,
      }],
      tooltip: { trigger: 'item', formatter: '{b}: \${c}' },
    })
    if (selectedModel) {
      const s = statMap.get(`${selectedModel.provider}/${selectedModel.model}`)
      const raw = [
        { key: t('aistats.costKey.input'), cost: s?.CostInput ?? 0 },
        { key: t('aistats.costKey.output'), cost: s?.CostOutput ?? 0 },
        { key: t('aistats.costKey.cacheRead'), cost: s?.CostCacheRead ?? 0 },
        { key: t('aistats.costKey.cacheWrite'), cost: s?.CostCacheWrite ?? 0 },
      ]
      const total = raw.reduce((s, it) => s + it.cost, 0)
      const items = raw
        .filter(it => it.cost > 0)
        .map(it => ({ ...it, share: total > 0 ? (it.cost / total) * 100 : 0 }))
      if (items.length === 0 || total === 0) return { option: null, items }
      return { option: donut(items.map(it => ({ name: it.key, value: it.cost }))), items }
    }
    const total = modelStats.reduce((s, m) => s + (m.CostTotal ?? 0), 0)
    const items = modelStats
      .map(m => ({ key: modelKey(m), cost: m.CostTotal ?? 0 }))
      .filter(it => it.cost > 0)
      .sort((a, b) => b.cost - a.cost)
      .map(it => ({ ...it, share: total > 0 ? (it.cost / total) * 100 : 0 }))
    if (items.length === 0 || total === 0) return { option: null, items }
    return { option: donut(items.map(it => ({ name: it.key, value: it.cost }))), items }
  }, [modelStats, statMap, selectedModel, theme, t])

  // Summary metrics — derived from series (time-range aware)
  const wsTotals = (() => {
    let requests = 0, input = 0, output = 0, cacheRead = 0, cacheWrite = 0, cost = 0, errors = 0
    for (const b of buckets) {
      requests += b.Requests
      input += b.InputTokens
      output += b.OutputTokens
      cacheRead += b.CacheRead
      cacheWrite += b.CacheWrite
      cost += b.Cost
      errors += b.Errors
    }
    return { requests, input, output, cacheRead, cacheWrite, cost, errors }
  })()
  const selectedStat = selectedModel
    ? statMap.get(`${selectedModel.provider}/${selectedModel.model}`)
    : undefined
  const ws = selectedStat
    ? { requests: selectedStat.Requests, input: selectedStat.InputTokens, output: selectedStat.OutputTokens, cacheRead: selectedStat.CacheRead, cacheWrite: selectedStat.CacheWrite, cost: selectedStat.CostTotal, errors: selectedStat.Errors }
    : wsTotals
  const overall = selectedModel ? modelSeries : series
  const p50 = overall?.OverallLatencyP50 ?? 0
  const p95 = overall?.OverallLatencyP95 ?? 0
  const ttftP50 = overall?.OverallTtftP50 ?? 0
  const tokRate = overall && overall.OverallLatencySumMs > 0
    ? overall.OverallOutputTokens / (overall.OverallLatencySumMs / 1000)
    : 0
  const errRate = ws.requests > 0 ? (ws.errors / ws.requests) * 100 : 0
  const cacheHitRate = ws.cacheRead + ws.input > 0 ? (ws.cacheRead / (ws.cacheRead + ws.input)) * 100 : 0
  const costPerReq = ws.requests > 0 ? ws.cost / ws.requests : 0

  const models = modelStats

  const filteredModelStats = useMemo(() => {
    const q = modelFilter.trim().toLowerCase()
    if (!q) return modelStats
    return modelStats.filter(s =>
      s.Provider.toLowerCase().includes(q) ||
      s.Model.toLowerCase().includes(q) ||
      `${s.Provider}/${s.Model}`.toLowerCase().includes(q),
    )
  }, [modelStats, modelFilter])

  // Shared sort key extractor. Keys map to both pill and table sort options.
  const sortVal = (s: AIStatsModelStat, key: string): number => {
    switch (key) {
      case 'tokens':     return s.InputTokens + s.OutputTokens
      case 'success':    return s.Requests > 0 ? (s.Requests - s.Errors) / s.Requests : 0
      case 'latency':    return s.LatencyP95
      case 'ttft':       return s.TtftP50
      case 'tokrate':    return s.LatencySumMs > 0 ? s.OutputTokens / (s.LatencySumMs / 1000) : 0
      case 'fails':      return s.Errors
      case 'cache':      return s.CacheRead + s.InputTokens > 0 ? s.CacheRead / (s.CacheRead + s.InputTokens) : 0
      case 'cost':       return s.CostTotal
      case 'costPerReq': return s.Requests > 0 ? s.CostTotal / s.Requests : 0
      case 'input':      return s.InputTokens
      case 'output':     return s.OutputTokens
      default:           return s.Requests
    }
  }

  const sortModels = (list: AIStatsModelStat[], key: string, dir: 'asc' | 'desc') => {
    const sorted = [...list].sort((a, b) => sortVal(a, key) - sortVal(b, key))
    return dir === 'desc' ? sorted : sorted.reverse()
  }

  const sortedModels = useMemo(
    () => sortModels(filteredModelStats, pillSort, pillSortDir),
    [filteredModelStats, pillSort, pillSortDir],
  )
  // Collapsed pills hide low-volume models (< PILL_MIN_REQUESTS requests);
  // the expanded table renders tableSortedModels with every model.
  const pillModels = useMemo(
    () => sortedModels.filter(s => s.Requests >= PILL_MIN_REQUESTS),
    [sortedModels],
  )
  const tableSortedModels = useMemo(
    () => sortModels(filteredModelStats, tableSort, tableSortDir),
    [filteredModelStats, tableSort, tableSortDir],
  )

  const PILL_SORTS: { key: string; label: string }[] = [
    { key: 'requests', label: t('aistats.sort.requests') },
    { key: 'tokens', label: t('aistats.sort.tokens') },
    { key: 'success', label: t('aistats.sort.success') },
    { key: 'latency', label: t('aistats.sort.latency') },
    { key: 'cache', label: t('aistats.sort.cache') },
    { key: 'cost', label: t('aistats.sort.cost') },
  ]

  // Table column definitions with sort keys.
  type SortCol = { label: string; sortKey?: string }
  const TABLE_COLS: SortCol[] = [
    { label: t('aistats.col.provider'), sortKey: 'provider' },
    { label: t('aistats.col.model'), sortKey: 'model' },
    { label: t('aistats.col.requests'), sortKey: 'requests' },
    { label: t('aistats.col.in'), sortKey: 'input' },
    { label: t('aistats.col.out'), sortKey: 'output' },
    { label: t('aistats.col.cachePct'), sortKey: 'cache' },
    { label: t('aistats.col.latency'), sortKey: 'latency' },
    { label: t('aistats.col.ttft'), sortKey: 'ttft' },
    { label: t('aistats.col.tokRate'), sortKey: 'tokrate' },
    { label: t('aistats.col.fails'), sortKey: 'fails' },
    { label: t('aistats.col.successPct'), sortKey: 'success' },
    { label: t('aistats.col.cost'), sortKey: 'cost' },
    { label: t('aistats.col.costPerReq'), sortKey: 'costPerReq' },
  ]

  const handleTableSort = (key: string) => {
    if (tableSort === key) {
      setTableSortDir(d => d === 'desc' ? 'asc' : 'desc')
    } else {
      setTableSort(key)
      setTableSortDir('desc')
    }
  }

  if (loading && !series) {
    return <div className="ai-aistats-empty">{t('aistats.loading')}</div>
  }

  return (
    <div className={`ai-aistats-overview ${loading && series ? 'is-refetching' : ''}`}>
      {loading && series && (
        <div className="ai-aistats-refetch-overlay">
          <span className="ai-aistats-spinner" />
        </div>
      )}
      {loading && !series && (
        <div className="ai-aistats-chart-empty">{t('aistats.loading')}</div>
      )}
      <section className={`ai-aistats-card ai-aistats-models ${modelsExpanded ? 'expanded' : 'collapsed'}`}>
        <h4 className="ai-aistats-models-header">
          <span className="ai-aistats-models-title">
            {t('aistats.models')}
            <span className="ai-aistats-models-count" title={t('aistats.modelsCountTitle', { n: models.length })}>{fmtCompact(models.length)}</span>
            <span className="ai-aistats-models-total" title={t('aistats.totalRequests')}>{fmtCompact(models.reduce((s, m) => s + (m.Requests ?? 0), 0))}</span>
          </span>
          <div className="ai-aistats-models-controls">
            <div className="ai-aistats-model-filter">
              <Search size={12} />
              <input
                type="search"
                className="ai-aistats-filter-input"
                placeholder={t('aistats.filterPlaceholder')}
                aria-label={t('aistats.filterAria')}
                value={filterDraft}
                onChange={e => setFilterDraft(e.target.value)}
                onBlur={() => onCommitFilter(filterDraft)}
                onKeyDown={e => { if (e.key === 'Enter') onCommitFilter(filterDraft) }}
              />
            </div>
            {!modelsExpanded && (
              <div className="ai-aistats-models-sort">
                <select
                  className="ai-aistats-sort-select"
                  value={pillSort}
                  onChange={e => setPillSort(e.target.value)}
                  title={t('aistats.sortBy')}
                >
                  {PILL_SORTS.map(s => <option key={s.key} value={s.key}>{s.label}</option>)}
                </select>
                <button
                  className="ai-aistats-sort-dir-btn"
                  onClick={() => setPillSortDir(d => d === 'desc' ? 'asc' : 'desc')}
                  title={pillSortDir === 'desc' ? t('aistats.desc') : t('aistats.asc')}
                >
                  {pillSortDir === 'desc' ? <ArrowDown size={13} /> : <ArrowUp size={13} />}
                </button>
              </div>
            )}
          </div>
          <button
            className="ai-aistats-models-toggle"
            onClick={() => setModelsExpanded(e => !e)}
            title={modelsExpanded ? t('aistats.collapseModels') : t('aistats.expandModels')}
          >
            {modelsExpanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
            {modelsExpanded ? t('aistats.collapse') : t('aistats.expand')}
          </button>
        </h4>
        {modelsExpanded ? (
          <div className="ai-aistats-table-wrap">
            <table className="ai-aistats-table">
              <thead>
                <tr>
                  {TABLE_COLS.map(col => {
                    const isSorted = col.sortKey === tableSort
                    return (
                      <th
                        key={col.label}
                        className={`num ${isSorted ? 'sorted' : ''} ${col.sortKey ? 'sortable' : ''}`}
                        onClick={col.sortKey ? () => handleTableSort(col.sortKey!) : undefined}
                      >
                        {col.label}
                        {col.sortKey && (
                          <span className="ai-aistats-sort-indicator">
                            {isSorted
                              ? (tableSortDir === 'desc' ? <ArrowDown size={11} /> : <ArrowUp size={11} />)
                              : <ArrowUpDown size={11} />}
                          </span>
                        )}
                      </th>
                    )
                  })}
                </tr>
              </thead>
              <tbody>
                {tableSortedModels.map((s: AIStatsModelStat) => {
                  const cachePct = s.CacheRead + s.InputTokens > 0 ? (s.CacheRead / (s.CacheRead + s.InputTokens)) * 100 : 0
                  const errPct = s.Requests > 0 ? (s.Errors / s.Requests) * 100 : 0
                  const tokRateM = s.LatencySumMs > 0 ? s.OutputTokens / (s.LatencySumMs / 1000) : 0
                  const active = selectedModel?.provider === s.Provider && selectedModel?.model === s.Model
                  return (
                    <tr key={modelKey(s)} className={active ? 'active' : ''} onClick={() => onSelectModel(active ? null : { provider: s.Provider, model: s.Model })}>
                      <td>{s.Provider}</td><td>{s.Model}</td>
                      <td className="num">{fmtInt(s.Requests)}</td>
                      <td className="num">{fmtCompact(s.InputTokens)}</td>
                      <td className="num">{fmtCompact(s.OutputTokens)}</td>
                      <td className="num">{fmtPct(cachePct)}</td>
                      <td className="num">{s.LatencyP95 > 0 ? fmtMs(s.LatencyP95) : '-'}</td>
                      <td className="num">{s.TtftP50 > 0 ? fmtMs(s.TtftP50) : '-'}</td>
                      <td className="num">{tokRateM > 0 ? `${fmtCompact(tokRateM)}/s` : '-'}</td>
                      <td className={`num ${s.Errors > 0 ? 'err' : ''}`}>{fmtInt(s.Errors)}</td>
                      <td className="num">{s.Requests > 0 ? fmtPct(100 - errPct) : '-'}</td>
                      <td className="num">{fmtCost(s.CostTotal)}</td>
                      <td className="num">{s.Requests > 0 ? fmtCost(s.CostTotal / s.Requests) : '-'}</td>
                    </tr>
                  )
                })}
                {tableSortedModels.length === 0 && (
                  <tr><td colSpan={13} className="ai-aistats-empty-cell">{t('aistats.noModelData')}</td></tr>
                )}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="ai-aistats-models-pills-section">
            <div className="ai-aistats-models-pills">
              {sortedModels.length === 0 && <span className="ai-aistats-models-empty">{t('aistats.noModelData')}</span>}
              {pillModels.map((s: AIStatsModelStat) => {
                const active = selectedModel?.provider === s.Provider && selectedModel?.model === s.Model
                return (
                  <button
                    key={modelKey(s)}
                    className={`ai-aistats-model-pill ${active ? 'active' : ''}`}
                    onClick={() => onSelectModel(active ? null : { provider: s.Provider, model: s.Model })}
                    title={t('aistats.pillTitle', { name: `${s.Provider}/${s.Model}`, count: fmtInt(s.Requests) })}
                  >
                    <span className="ai-aistats-model-pill-name">{s.Provider}/{s.Model}</span>
                    <span className="ai-aistats-model-pill-count">{fmtCompact(s.Requests)}</span>
                  </button>
                )
              })}
            </div>
          </div>
        )}
      </section>

      <div className="ai-aistats-summary">
        <Metric label={t('aistats.metric.requests')} value={fmtInt(ws.requests)} sub={<span className={errRate > 0 ? 'err' : ''}>{fmtPct(errRate)} {t('aistats.metric.error')}</span>} />
        <Metric label={t('aistats.metric.inputTokens')} value={fmtCompact(ws.input)} sub={<span><i style={{ background: C_CACHE }} />{t('aistats.metric.cache', { n: fmtCompact(ws.cacheRead) })}</span>} />
        <Metric label={t('aistats.metric.outputTokens')} value={fmtCompact(ws.output)} sub={`${fmtCompact(tokRate)}/s`} />
        <Metric label={t('aistats.metric.cost')} value={fmtCost(ws.cost)} sub={t('aistats.metric.perReq', { v: fmtCost(costPerReq) })} />
        <Metric label={t('aistats.metric.cacheHit')} value={fmtPct(cacheHitRate)} sub={t('aistats.metric.readWrite', { r: fmtCompact(ws.cacheRead), w: fmtCompact(ws.cacheWrite) })} />
        <Metric label={t('aistats.metric.latency')} value={`p50 ${fmtMs(p50)}`} sub={<span><i style={{ background: C_TTFT }} />{t('aistats.metric.ttft', { v: fmtMs(ttftP50) })} . <i style={{ background: C_P95 }} />{t('aistats.metric.p95', { v: fmtMs(p95) })}</span>} />
      </div>

      <div className="ai-aistats-grid">
        <section className="ai-aistats-card">
          <h4>{t('aistats.chart.requests')}</h4>
          {requestsAreaOption ? <ECharts option={requestsAreaOption} theme={theme} className="ai-aistats-chart" /> : <Empty />}
        </section>
        <section className="ai-aistats-card">
          <h4>{t('aistats.chart.latency')} <Legend colors={[['TTFT', C_TTFT], ['p50', C_P50], ['p95', C_P95]]} /></h4>
          {latencyLineOption ? <ECharts option={latencyLineOption} theme={theme} className="ai-aistats-chart" /> : <Empty />}
        </section>
        <section className="ai-aistats-card">
          <h4>{t('aistats.chart.tokenSpeed')} <Legend colors={[[t('aistats.legend.outputRate'), C_OUTPUT]]} /></h4>
          {tokenSpeedLineOption ? <ECharts option={tokenSpeedLineOption} theme={theme} className="ai-aistats-chart" /> : <Empty />}
        </section>
        <section className="ai-aistats-card">
          <h4>{t('aistats.chart.cost')}</h4>
          {costAreaOption ? <ECharts option={costAreaOption} theme={theme} className="ai-aistats-chart" /> : <Empty />}
        </section>
        <section className="ai-aistats-card">
          <h4>{t('aistats.chart.errorRate')}</h4>
          {errorRateLineOption ? <ECharts option={errorRateLineOption} theme={theme} className="ai-aistats-chart" /> : <Empty />}
        </section>
        <section className="ai-aistats-card">
          <h4>{t('aistats.chart.cacheHitRate')} <Legend colors={[[t('aistats.legend.cacheHit'), C_CACHE]]} /></h4>
          {cacheHitLineOption ? <ECharts option={cacheHitLineOption} theme={theme} className="ai-aistats-chart" /> : <Empty />}
        </section>
        <section className="ai-aistats-card ai-aistats-cost-card">
          <h4>{selectedModel ? t('aistats.chart.costComposition') : t('aistats.chart.costByModel')}</h4>
          {costDonut.option ? (
            <div className="ai-aistats-donut-row">
              <ECharts option={costDonut.option} theme={theme} className="ai-aistats-donut-chart" />
              <ul className="ai-aistats-legend-list">
                {costDonut.items.slice(0, 8).map(it => (
                  <li key={it.key}><span>{it.key}</span><span>{fmtCost(it.cost)} . {fmtPct(it.share)}</span></li>
                ))}
              </ul>
            </div>
          ) : <Empty />}
        </section>
        <section className="ai-aistats-card ai-aistats-errcode-card">
          <h4>{t('aistats.chart.errCodeDist')}</h4>
          {errorCodeDist.option ? (
            <div className="ai-aistats-donut-row">
              <ECharts option={errorCodeDist.option} theme={theme} className="ai-aistats-donut-chart" />
              <ul className="ai-aistats-legend-list">
                {errorCodeDist.items.map(it => (
                  <li key={it.code}><span><i style={{ background: it.color }} />{it.label}</span><span>{fmtInt(it.count)}</span></li>
                ))}
              </ul>
            </div>
          ) : <Empty />}
        </section>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Records tab — paginated, latest-first
// ---------------------------------------------------------------------------

function RecordsTab() {
  const { t } = useI18n()
  const [recordsTimeRange, setRecordsTimeRange] = useState<AIStatsTimeRange>('24h')
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(50)
  const [expanded, setExpanded] = useState(false)
  const since = toSince(recordsTimeRange)
  const { records, total, loading, error, sync } = useAIStatsRecords({ since, pageSize, page })

  const hasMore = records.length >= pageSize

  type Cell = (r: AIStatsRecord, u: AIStatsRecord['Usage']) => React.ReactNode
  type Col = { key: string; label: string; cls?: string; cell: Cell }

  const compactColumns: Col[] = [
    { key: 'time', label: t('aistats.rcol.time'), cell: r => r.CompletedAt ? new Date(r.CompletedAt).toLocaleString() : '-' },
    { key: 'model', label: t('aistats.rcol.model'), cell: r => `${r.Provider}/${r.Model}` },
    { key: 'stop', label: t('aistats.rcol.stop'), cell: r => r.StopReason ?? '-' },
    { key: 'errcode', label: t('aistats.rcol.errcode'), cell: r => r.ErrorCode ?? '-' },
    { key: 'ttft', label: t('aistats.rcol.ttft'), cls: 'num', cell: r => r.FirstTokenMs != null ? fmtMs(r.FirstTokenMs) : '-' },
    { key: 'latency', label: t('aistats.rcol.latency'), cls: 'num', cell: r => r.LatencyMs != null ? fmtMs(r.LatencyMs) : '-' },
    { key: 'in', label: t('aistats.rcol.in'), cls: 'num', cell: (_, u) => u ? fmtCompact(u.InputTokens ?? 0) : '-' },
    { key: 'out', label: t('aistats.rcol.out'), cls: 'num', cell: (_, u) => u ? fmtCompact(u.OutputTokens ?? 0) : '-' },
    { key: 'cache', label: t('aistats.rcol.cache'), cls: 'num', cell: (_, u) => u && u.CacheReadInputTokens ? fmtCompact(u.CacheReadInputTokens) : '-' },
    { key: 'cost', label: t('aistats.rcol.cost'), cls: 'num', cell: (_, u) => u ? fmtCost(u.CostTotal ?? 0) : '-' },
  ]

  const expandedColumns: Col[] = [
    { key: 'id', label: 'ID', cell: r => r.Id },
    { key: 'requestId', label: 'RequestID', cell: r => r.RequestId || '-' },
    { key: 'turnId', label: 'TurnID', cell: r => r.TurnId || '-' },
    { key: 'sessionId', label: 'SessionID', cell: r => r.SessionId || '-' },
    { key: 'workspaceId', label: 'WorkspaceID', cell: r => r.WorkspaceId || '-' },
    { key: 'projectId', label: 'ProjectID', cell: r => r.ProjectId || '-' },
    { key: 'agentId', label: 'AgentID', cell: r => r.AgentId || '-' },
    { key: 'provider', label: 'Provider', cell: r => r.Provider },
    { key: 'model', label: 'Model', cell: r => r.Model },
    { key: 'responseModel', label: 'ResponseModel', cell: r => r.ResponseModel || '-' },
    { key: 'responseId', label: 'ResponseID', cell: r => r.ResponseId || '-' },
    { key: 'clientRequestId', label: 'ClientRequestID', cell: r => r.ClientRequestId || '-' },
    { key: 'startedAt', label: 'StartedAt', cell: r => r.StartedAt ? new Date(r.StartedAt).toLocaleString() : '-' },
    { key: 'completedAt', label: 'CompletedAt', cell: r => r.CompletedAt ? new Date(r.CompletedAt).toLocaleString() : '-' },
    { key: 'stop', label: 'StopReason', cell: r => r.StopReason ?? '-' },
    { key: 'errorCode', label: 'ErrorCode', cell: r => r.ErrorCode ?? '-' },
    { key: 'errorMessage', label: 'ErrorMessage', cell: r => r.ErrorMessage || '-' },
    { key: 'latency', label: 'LatencyMs', cls: 'num', cell: r => r.LatencyMs != null ? fmtMs(r.LatencyMs) : '-' },
    { key: 'firstToken', label: 'FirstTokenMs', cls: 'num', cell: r => r.FirstTokenMs != null ? fmtMs(r.FirstTokenMs) : '-' },
    { key: 'inputTokens', label: 'InputTokens', cls: 'num', cell: (_, u) => u ? fmtCompact(u.InputTokens ?? 0) : '-' },
    { key: 'outputTokens', label: 'OutputTokens', cls: 'num', cell: (_, u) => u ? fmtCompact(u.OutputTokens ?? 0) : '-' },
    { key: 'cacheRead', label: 'CacheRead', cls: 'num', cell: (_, u) => u ? fmtCompact(u.CacheReadInputTokens ?? 0) : '-' },
    { key: 'cacheWrite', label: 'CacheWrite', cls: 'num', cell: (_, u) => u ? fmtCompact(u.CacheCreationInputTokens ?? 0) : '-' },
    { key: 'reasoning', label: 'Reasoning', cls: 'num', cell: (_, u) => u ? fmtCompact(u.ReasoningTokens ?? 0) : '-' },
    { key: 'cost', label: 'Cost', cls: 'num', cell: (_, u) => u ? fmtCost(u.CostTotal ?? 0) : '-' },
    { key: 'tags', label: 'Tags', cell: r => Object.entries(r.Tags ?? {}).map(([k, v]) => `${k}=${v}`).join(', ') || '-' },
  ]

  const columns = expanded ? expandedColumns : compactColumns

  return (
    <section className={`ai-aistats-card ai-aistats-records-tab ${expanded ? 'expanded' : ''}`}>
      <div className="ai-aistats-records-header">
        <h4>
          {t('aistats.records.title')}
          <span className="ai-aistats-note">
            {loading ? ` ${t('aistats.loading')}` : ` ${t('aistats.records.meta', { n: fmtInt(total) })}`}
          </span>
        </h4>
        <div className="ai-aistats-records-controls">
          <div className="ai-aistats-ranges ai-aistats-ranges-inline">
            {TIME_RANGES.map(r => (
              <button
                key={r}
                className={`ai-aistats-range-btn ${r === recordsTimeRange ? 'active' : ''}`}
                onClick={() => setRecordsTimeRange(r)}
                title={t('aistats.records.rangeTitle')}
              >{r}</button>
            ))}
          </div>
          <div className="ai-aistats-page-size">
            <label>{t('aistats.records.perPage')}</label>
            <select value={pageSize} onChange={e => { setPageSize(Number(e.target.value)); setPage(0) }}>
              {PAGE_SIZES.map(n => <option key={n} value={n}>{n}</option>)}
            </select>
          </div>
          <button
            className="ai-aistats-expand-btn"
            onClick={() => setExpanded(e => !e)}
            title={expanded ? t('aistats.records.collapseTitle') : t('aistats.records.expandTitle')}
          >
            {expanded ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
            {expanded ? ` ${t('aistats.collapse')}` : ` ${t('aistats.expand')}`}
          </button>
          <button className="ai-aistats-sync-btn" onClick={sync} title={t('aistats.records.syncTitle')} disabled={loading}>
            <RefreshCw size={14} /> {t('aistats.sync')}
          </button>
        </div>
      </div>

      {error && <div className="ai-aistats-error"><AlertOctagon size={14} /> {error}</div>}

      <div className="ai-aistats-table-wrap">
        <table className="ai-aistats-table ai-aistats-records-table">
          <thead>
            <tr>
              {columns.map(c => <th key={c.key} className={c.cls}>{c.label}</th>)}
            </tr>
          </thead>
          <tbody>
            {loading && records.length === 0 && (
              <tr><td colSpan={columns.length} className="ai-aistats-empty-cell">{t('aistats.loading')}</td></tr>
            )}
            {!loading && records.map((r: AIStatsRecord) => {
              const u = r.Usage
              return (
                <tr key={r.Id}>
                  {columns.map(c => <td key={c.key} className={c.cls}>{c.cell(r, u)}</td>)}
                </tr>
              )
            })}
            {!loading && records.length === 0 && (
              <tr><td colSpan={columns.length} className="ai-aistats-empty-cell">{t('aistats.records.empty')}</td></tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="ai-aistats-pagination">
        <button
          className="ai-aistats-page-btn"
          disabled={page === 0}
          onClick={() => setPage(p => Math.max(0, p - 1))}
        >
          <ChevronLeft size={14} /> {t('aistats.records.prev')}
        </button>
        <span className="ai-aistats-page-info">
          {total > 0 ? t('aistats.records.page', { n: page + 1 }) : t('aistats.records.none')}
        </span>
        <button
          className="ai-aistats-page-btn"
          disabled={!hasMore}
          onClick={() => setPage(p => p + 1)}
        >
          {t('aistats.records.next')} <ChevronRight size={14} />
        </button>
      </div>
    </section>
  )
}

// ---------------------------------------------------------------------------
// Small components
// ---------------------------------------------------------------------------

function Metric({ label, value, sub }: { label: string; value: string; sub: React.ReactNode }) {
  return (
    <div className="ai-aistats-metric">
      <span className="ai-aistats-metric-label">{label}</span>
      <span className="ai-aistats-metric-value">{value}</span>
      <span className="ai-aistats-metric-sub">{sub}</span>
    </div>
  )
}

function Legend({ colors }: { colors: [string, string][] }) {
  return (
    <span className="ai-aistats-legend">
      {colors.map(([name, color]) => (
        <span key={name}><i style={{ background: color }} />{name}</span>
      ))}
    </span>
  )
}

function Empty() {
  const { t } = useI18n()
  return <div className="ai-aistats-chart-empty">{t('aistats.empty')}</div>
}
