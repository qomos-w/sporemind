import { useCallback, useEffect, useState } from 'react'
import { client } from '../../application/generated-client'
import * as glassDebug from '../../gen-clients/glass_interact/client'
import * as glassEvents from '../../gen-clients/glassinteract/client'
import type {
  GlassDebugState,
  GlassDebugTimelineEntry,
  GlassLifecycleEvent,
  GlassRenderEvent,
  GlassRenderFrame,
  GlassSpeakEvent,
  GlassTranscriptEvent,
} from '../../gen-clients/system/types'

/**
 * Poll interval for the debug snapshot. The snapshot callable is the only
 * source of audio ACK + aggregate stats; events keep frame/session/timeline
 * live between polls.
 */
export const GLASS_DEBUG_REFRESH_MS = 3000
/** Local timeline cap, mirroring the server's bounded timeline. */
export const GLASS_DEBUG_TIMELINE_MAX = 32
/** Local lifecycle event cap shown in the connection section. */
export const GLASS_CONNECTION_EVENTS_MAX = 8

export interface UseGlassDebugResult {
  state: GlassDebugState | null
  loading: boolean
  error: string | null
  lastUpdated: number | null
  autoRefresh: boolean
  setAutoRefresh: (v: boolean) => void
  refresh: () => void
  connectionEvents: GlassLifecycleEvent[]
}

function prependTimeline(timeline: GlassDebugTimelineEntry[], entry: GlassDebugTimelineEntry): GlassDebugTimelineEntry[] {
  const next = [entry, ...timeline]
  if (next.length > GLASS_DEBUG_TIMELINE_MAX) {
    return next.slice(0, GLASS_DEBUG_TIMELINE_MAX)
  }
  return next
}

function entry(kind: string, detail: string | undefined, timestamp: string | undefined): GlassDebugTimelineEntry {
  return { Kind: kind, Detail: detail, Timestamp: timestamp }
}

function renderDetail(frame: GlassRenderFrame): string {
  const parts: string[] = []
  if (frame.Text) parts.push(`text=${frame.Text}`)
  if (frame.ImageUrl) parts.push('image=yes')
  if (frame.Layout) parts.push(`layout=${frame.Layout}`)
  return parts.join(' ') || 'render'
}

export function useGlassDebug(autoRefresh = true): UseGlassDebugResult {
  const [state, setState] = useState<GlassDebugState | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<number | null>(null)
  const [autoRefreshEnabled, setAutoRefreshEnabled] = useState(autoRefresh)
  const [tick, setTick] = useState(0)
  const [connectionEvents, setConnectionEvents] = useState<GlassLifecycleEvent[]>([])

  const refresh = useCallback(() => setTick(t => t + 1), [])

  // Snapshot fetch through the authenticated /ws callable surface.
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    glassDebug
      .debugState(client, {})
      .then(resp => {
        if (cancelled) return
        setState(resp.State)
        setError(null)
        setLastUpdated(Date.now())
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setError(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [tick])

  // Poll so audio ACK + aggregate stats stay current between events.
  useEffect(() => {
    if (!autoRefreshEnabled) return
    const id = setInterval(() => setTick(t => t + 1), GLASS_DEBUG_REFRESH_MS)
    return () => clearInterval(id)
  }, [autoRefreshEnabled])

  // Live event subscriptions over the existing authenticated /ws transport.
  useEffect(() => {
    const recordConnection = (ev: GlassLifecycleEvent, apply: (prev: GlassDebugState) => GlassDebugState) => {
      setState(prev => (prev ? apply(prev) : prev))
      setConnectionEvents(prev => [...prev, ev].slice(-GLASS_CONNECTION_EVENTS_MAX))
    }
    const offs = [
      glassEvents.OnGlassOnline(client, (ev: GlassLifecycleEvent) => {
        recordConnection(ev, prev => ({
          ...prev,
          Session: prev.Session ? { ...prev.Session, Online: true, LastSeen: ev.Timestamp } : prev.Session,
        }))
      }),
      glassEvents.OnGlassOffline(client, (ev: GlassLifecycleEvent) => {
        recordConnection(ev, prev => ({
          ...prev,
          Session: prev.Session ? { ...prev.Session, Online: false, OfflineAt: ev.Timestamp } : prev.Session,
        }))
      }),
      glassEvents.OnGlassReconnected(client, (ev: GlassLifecycleEvent) => {
        recordConnection(ev, prev => ({
          ...prev,
          Session: prev.Session ? { ...prev.Session, Online: true, LastSeen: ev.Timestamp } : prev.Session,
        }))
      }),
      glassEvents.OnGlassReplaced(client, (ev: GlassLifecycleEvent) => {
        recordConnection(ev, prev => ({
          ...prev,
          Session: prev.Session
            ? { ...prev.Session, SessionId: ev.SessionId, DeviceId: ev.DeviceId, Generation: ev.Generation, Online: true, LastSeen: ev.Timestamp }
            : prev.Session,
        }))
      }),
      glassEvents.OnGlassRender(client, (ev: GlassRenderEvent) => {
        setState(prev =>
          prev
            ? {
                ...prev,
                CurrentFrame: ev.Frame,
                Timeline: prependTimeline(prev.Timeline, entry('render', renderDetail(ev.Frame), ev.Timestamp)),
              }
            : prev,
        )
      }),
      glassEvents.OnGlassSpeak(client, (ev: GlassSpeakEvent) => {
        setState(prev =>
          prev
            ? {
                ...prev,
                Timeline: prependTimeline(prev.Timeline, entry(ev.Duplicate ? 'speak_deduped' : 'speak', ev.MessageId, ev.Timestamp)),
              }
            : prev,
        )
      }),
      glassEvents.OnGlassTranscript(client, (ev: GlassTranscriptEvent) => {
        setState(prev =>
          prev
            ? {
                ...prev,
                Timeline: prependTimeline(
                  prev.Timeline,
                  entry('transcript', ev.Transcript?.Text ?? 'transcript', ev.Transcript?.Timestamp),
                ),
              }
            : prev,
        )
      }),
    ]
    return () => {
      for (const off of offs) {
        try {
          off()
        } catch {
          // unsubscribe is best-effort on teardown
        }
      }
    }
    // Event subscriptions are mounted once; state updates use functional
    // setState so no stale closures are needed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return {
    state,
    loading,
    error,
    lastUpdated,
    autoRefresh: autoRefreshEnabled,
    setAutoRefresh: setAutoRefreshEnabled,
    refresh,
    connectionEvents,
  }
}
