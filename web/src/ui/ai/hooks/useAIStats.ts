import { useCallback, useEffect, useRef, useState } from 'react'
import { client } from '../../../application/generated-client'
import * as aistats from '../../../gen-clients/aistats/client'
import * as workspace from '../../../gen-clients/workspace/client'
import type {
  AIStatsBucket,
  AIStatsModelStat,
  AIStatsQueryReq,
  AIStatsRecord,
  AIStatsSeriesReq,
  AIStatsSeriesResp,
} from '../../../gen-types/aistats'

export type AIStatsTimeRange = '1h' | '24h' | '7d' | '30d' | 'all'

export interface CustomRange {
  sinceMs: number
  untilMs: number
}

export type AIStatsRange = AIStatsTimeRange | CustomRange

export function isCustomRange(r: AIStatsRange): r is CustomRange {
  return typeof r === 'object'
}

interface RangeCfg {
  bucketMs: number
  sinceMs: number | null
}

const RANGE_CONFIG: Record<AIStatsTimeRange, RangeCfg> = {
  '1h': { bucketMs: 5 * 60_000, sinceMs: 60 * 60_000 },
  '24h': { bucketMs: 60 * 60_000, sinceMs: 24 * 60 * 60_000 },
  '7d': { bucketMs: 6 * 60 * 60_000, sinceMs: 7 * 24 * 60 * 60_000 },
  '30d': { bucketMs: 24 * 60 * 60_000, sinceMs: 30 * 24 * 60 * 60_000 },
  'all': { bucketMs: 24 * 60 * 60_000, sinceMs: null },
}

export function rangeConfig(r: AIStatsRange): RangeCfg {
  if (isCustomRange(r)) {
    // Target ~60 buckets, clamped to [1min, 24h], rounded to whole minutes.
    const span = r.untilMs - r.sinceMs
    const bucket = Math.round(span / 60 / 60_000) * 60_000
    return { bucketMs: Math.max(60_000, Math.min(24 * 60 * 60_000, bucket)), sinceMs: r.sinceMs }
  }
  return RANGE_CONFIG[r]
}

export function toSince(r: AIStatsRange): string | undefined {
  if (isCustomRange(r)) return new Date(r.sinceMs).toISOString()
  const ms = RANGE_CONFIG[r].sinceMs
  if (ms == null) return undefined
  return new Date(Date.now() - ms).toISOString()
}

export function toUntil(r: AIStatsRange): string | undefined {
  if (isCustomRange(r)) return new Date(r.untilMs).toISOString()
  return undefined
}

// Cached actor id with a TTL: the backend aistats actor can be replaced
// (restart), after which invokes to the stale id never reach the actor.
const ACTOR_ID_TTL_MS = 60_000
let cachedActorId: string | null = null
let cachedActorIdAt = 0
let actorIdPromise: Promise<string | null> | null = null

async function resolveActorId(): Promise<string | null> {
  if (cachedActorId && Date.now() - cachedActorIdAt < ACTOR_ID_TTL_MS) return cachedActorId
  if (!actorIdPromise) {
    actorIdPromise = workspace
      .aistatsActorId(client)
      .then(resp => {
        cachedActorId = resp.ActorId || null
        cachedActorIdAt = Date.now()
        actorIdPromise = null
        return cachedActorId
      })
      .catch(() => {
        actorIdPromise = null
        return null
      })
  }
  return actorIdPromise
}

const AUTO_REFRESH_MS = 10_000

export interface UseAIStatsArgs {
  timeRange: AIStatsRange
  autoRefresh: boolean
  selectedModel?: { provider: string; model: string } | null
  /** Case-insensitive substring match on Provider/Model; empty/undefined = no filter. */
  modelFilter?: string
}

export interface UseAIStatsResult {
  series: AIStatsSeriesResp | null
  modelSeries: AIStatsSeriesResp | null
  loading: boolean
  error: string | null
  lastUpdated: number | null
  refresh: () => void
}

export function useAIStats({ timeRange, autoRefresh, selectedModel, modelFilter }: UseAIStatsArgs): UseAIStatsResult {
  const [series, setSeries] = useState<AIStatsSeriesResp | null>(null)
  const [modelSeries, setModelSeries] = useState<AIStatsSeriesResp | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<number | null>(null)
  const [tick, setTick] = useState(0)
  // Latest-run token: only the newest run may clear `loading`. A run cancelled
  // by the next 10s auto-refresh tick would otherwise skip its finally reset
  // (auto-refresh interval < invoke timeout), wedging the panel in Loading.
  const runRef = useRef(0)

  const refresh = useCallback(() => setTick(t => t + 1), [])

  // String key so identity changes (new object on each render) don't refetch.
  const modelKey = selectedModel ? `${selectedModel.provider}/${selectedModel.model}` : ''

  useEffect(() => {
    let cancelled = false
    const run = ++runRef.current
    setLoading(true)
    void (async () => {
      const actorId = await resolveActorId()
      if (!actorId) {
        if (!cancelled) {
          setError('aistats actor unavailable')
        }
        if (runRef.current === run) setLoading(false)
        return
      }
      const since = toSince(timeRange)
      const until = toUntil(timeRange)
      const { bucketMs } = rangeConfig(timeRange)
      const seriesReq: AIStatsSeriesReq = {
        Scope: 'workspace',
        ScopeId: '',
        Since: since,
        Until: until,
        BucketMs: bucketMs,
        ModelFilter: modelFilter,
      }
      try {
        const globalSeries = aistats.series(client, seriesReq, { target: actorId })
        const scoped = modelKey
          ? aistats.series(client, {
              Scope: 'model',
              ScopeId: modelKey,
              Since: since,
              Until: until,
              BucketMs: bucketMs,
              ModelFilter: modelFilter,
            }, { target: actorId })
          : Promise.resolve(null)
        const [g, m] = await Promise.all([globalSeries, scoped])
        if (cancelled) return
        setSeries(g)
        setModelSeries(m)
        setError(null)
        setLastUpdated(Date.now())
      } catch (e: unknown) {
        if (cancelled) return
        setError(e instanceof Error ? e.message : String(e))
      } finally {
        if (runRef.current === run) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [timeRange, tick, modelKey, modelFilter])

  useEffect(() => {
    if (!autoRefresh) return
    const id = setInterval(() => setTick(t => t + 1), AUTO_REFRESH_MS)
    return () => clearInterval(id)
  }, [autoRefresh])

  return { series, modelSeries, loading, error, lastUpdated, refresh }
}

export interface UseAIStatsRecordsArgs {
  since?: string
  pageSize: number
  page: number
}

export interface UseAIStatsRecordsResult {
  records: AIStatsRecord[]
  total: number
  loading: boolean
  error: string | null
  sync: () => void
}

export function useAIStatsRecords({ since, pageSize, page }: UseAIStatsRecordsArgs): UseAIStatsRecordsResult {
  const [records, setRecords] = useState<AIStatsRecord[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [trigger, setTrigger] = useState(0)
  // Latest-run token — same loading-ownership rule as useAIStats.
  const runRef = useRef(0)

  const sync = useCallback(() => setTrigger(t => t + 1), [])

  // Only refetch automatically on mount, page/size change, or explicit sync.
  // Changing the time range requires clicking sync to avoid loading huge ranges
  // unexpectedly.
  useEffect(() => {
    let cancelled = false
    const run = ++runRef.current
    setLoading(true)
    void (async () => {
      const actorId = await resolveActorId()
      if (!actorId) {
        if (!cancelled) {
          setError('aistats actor unavailable')
        }
        if (runRef.current === run) setLoading(false)
        return
      }
      const queryReq: AIStatsQueryReq = {
        Scope: 'workspace',
        ScopeId: '',
        Since: since,
        Limit: pageSize,
        Offset: page * pageSize,
        Order: 'desc',
      }
      try {
        const q = await aistats.query(client, queryReq, { target: actorId })
        if (cancelled) return
        setRecords(q.Records ?? [])
        setTotal(q.Total ?? 0)
        setError(null)
      } catch (e: unknown) {
        if (cancelled) return
        setError(e instanceof Error ? e.message : String(e))
      } finally {
        if (runRef.current === run) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pageSize, page, trigger])

  return { records, total, loading, error, sync }
}

export type { AIStatsBucket, AIStatsModelStat, AIStatsSeriesResp }
