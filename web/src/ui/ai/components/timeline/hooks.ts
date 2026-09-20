import { useState, useRef, useEffect, useCallback } from 'react'
import type { SlotStatus, TurnEnvelope } from '../../model/frame-types'
import {
  SPINNER_EXIT_ANIMATION_MS,
  ARROW_REVEAL_DELAY_MS,
  ARROW_HOLD_DELAY_MS,
  AUTO_COLLAPSE_DELAY_MS,
  COLLAPSE_TRANSITION_MS,
  COLLAPSE_WAIT_AFTER_UNFOLD_MS,
  type IconPhase,
  type TimelineExpansionMode,
} from './types'

export function useTimelineExpansionController({
  status,
  expandable,
  expansionMode,
  turnStreaming,
  choreography = true,
}: {
  status: SlotStatus
  expandable: boolean
  expansionMode: TimelineExpansionMode
  /** Turn-level streaming state. Fast tools (run-command, edit) often reach
   *  the UI already completed; seeding from this keeps the title-first
   *  unfold working for them while history loads stay collapsed. */
  turnStreaming?: boolean
  /** When false, skip the expansion choreography entirely: mount straight at
   *  the configured terminal state with no title-first delay, no auto-expand /
   *  auto-collapse timers, and no icon-phase transitions. Driven by the
   *  smoothStream preference so a disabled-smooth card displays directly. */
  choreography?: boolean
}) {
  const isActive = status === 'running' || status === 'pending'
  const baselineOpen = expansionMode === 'open'
  const autoOpenWhileActive = expansionMode === 'auto-while-active' || expansionMode === 'auto-expand-once'
  const autoCollapseAfterDone = expansionMode === 'auto-while-active'
  // History-loaded frames (turn NOT streaming, frame not active) mount
  // straight at the configured terminal state: fold-* ends collapsed,
  // always-open ends open. No title delay, no choreography replay.
  const historyLoaded = turnStreaming === false && !isActive
  // Choreography disabled (smoothStream off): every frame displays directly.
  const skipChoreography = choreography === false
  // Auto-expanded tools begin closed; the title-first visual hold is handled by CSS.
  const terminalOpen = autoOpenWhileActive && !autoCollapseAfterDone ? true : baselineOpen
  // Direct-display open state: the same open/closed states as the choreography
  // (auto kinds open live while running, resting at the terminal state when
  // done) with all delays and animations removed.
  const directOpen = isActive
    ? (autoOpenWhileActive ? true : baselineOpen)
    : terminalOpen
  const [isOpen, setIsOpen] = useState(() => skipChoreography ? directOpen : historyLoaded ? terminalOpen : baselineOpen)
  const [iconPhase, setIconPhase] = useState<IconPhase>(() =>
    historyLoaded ? 'idle' : isActive ? 'spinning' : 'idle'
  )
  const collapseTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const iconTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const isOpenAtLeastOnceRef = useRef(false)
  const [titleFirst, setTitleFirst] = useState(false)
  const prevActiveRef = useRef(isActive)
  const completedAutoCloseRef = useRef(false)

  useEffect(() => {
    return () => {
      if (collapseTimerRef.current) {
        clearTimeout(collapseTimerRef.current)
        collapseTimerRef.current = null
      }
      if (iconTimerRef.current) {
        clearTimeout(iconTimerRef.current)
        iconTimerRef.current = null
      }
    }
  }, [])

  const mountHandledRef = useRef(false)
  const prevIsActiveRef = useRef(false)
  // Last direct-display open state applied while choreography is disabled;
  // guards against overriding a manual toggle when unrelated deps re-run.
  const prevDirectOpenRef = useRef(directOpen)

  useEffect(() => {
    if (!expandable) return
    // Choreography disabled: direct display. The open state tracks the
    // active edge instantly — no title-first delay, no timers, no icon
    // phases. Manual toggling still works between active-edge changes.
    if (skipChoreography) {
      if (directOpen !== prevDirectOpenRef.current) {
        prevDirectOpenRef.current = directOpen
        setIsOpen(directOpen)
      }
      return
    }
    if (autoOpenWhileActive && (isActive || turnStreaming)) {
      // Only genuinely-active frames, or completed frames on their very first
      // mount, may auto-open. turnStreaming is conversation-wide and persists
      // across turns; a later turn re-activating streaming must NOT re-expand a
      // step that already completed and collapsed during a previous turn.
      const justCompleted = !isActive && prevIsActiveRef.current
      if (!isActive && mountHandledRef.current && !justCompleted) return
      mountHandledRef.current = true
      prevIsActiveRef.current = isActive

      // Only a genuinely-active frame may cancel a pending auto-collapse:
      // turnStreaming stays true for the whole turn, and effect re-runs on
      // unrelated dep changes (status flips of OTHER frames) would
      // otherwise keep disarming the collapse chain armed at completion.
      if (isActive) {
        completedAutoCloseRef.current = false
        if (collapseTimerRef.current) {
          clearTimeout(collapseTimerRef.current)
          collapseTimerRef.current = null
        }
      }
      if (!isOpenAtLeastOnceRef.current) {
        isOpenAtLeastOnceRef.current = true
        setTitleFirst(true)
        setIsOpen(true)
        if (autoCollapseAfterDone && !isActive) {
          completedAutoCloseRef.current = true
          setIconPhase('exiting')
          iconTimerRef.current = setTimeout(() => {
            setIconPhase('arrow')
            iconTimerRef.current = setTimeout(() => {
              setIconPhase('entering')
              iconTimerRef.current = setTimeout(() => {
                setIconPhase('idle')
                iconTimerRef.current = null
              }, 260)
            }, ARROW_HOLD_DELAY_MS)
            collapseTimerRef.current = setTimeout(() => {
              setIsOpen(false)
              completedAutoCloseRef.current = false
              collapseTimerRef.current = setTimeout(() => {
                collapseTimerRef.current = null
              }, 260)
            }, AUTO_COLLAPSE_DELAY_MS + COLLAPSE_WAIT_AFTER_UNFOLD_MS)
          }, SPINNER_EXIT_ANIMATION_MS + ARROW_REVEAL_DELAY_MS)
        }
        return
      }
      setIsOpen(true)
    }
  }, [autoOpenWhileActive, expandable, isActive, turnStreaming, skipChoreography, directOpen])

  useEffect(() => {
    if (!expandable) {
      const wasActive = prevActiveRef.current
      prevActiveRef.current = isActive
      if (wasActive && !isActive) {
        setIconPhase('idle')
      } else if (!wasActive && isActive) {
        setIconPhase('spinning')
      }
      return
    }

    const wasActive = prevActiveRef.current
    prevActiveRef.current = isActive

    const clearTimers = () => {
      if (iconTimerRef.current) {
        clearTimeout(iconTimerRef.current)
        iconTimerRef.current = null
      }
      if (collapseTimerRef.current) {
        clearTimeout(collapseTimerRef.current)
        collapseTimerRef.current = null
      }
    }

    // History mount (or choreography disabled): nothing to choreograph. An
    // active frame keeps its spinner so the running state stays visible; a
    // terminal frame rests idle. Skip the completion-edge logic entirely.
    if (historyLoaded || skipChoreography) {
      clearTimers()
      setIconPhase(isActive ? 'spinning' : 'idle')
      return
    }

    if (isActive) {
      completedAutoCloseRef.current = false
      clearTimers()
      setIconPhase('spinning')
      return
    }

    // An armed auto-close choreography is the only thing that advances
    // iconPhase past 'exiting'. The mount effect arms it for a completed
    // frame mounting mid-stream and this effect's mount run lands right
    // after; clearing the timers here would kill the chain and strand
    // iconPhase in 'exiting' forever — the exit spinner has already faded
    // out, the tool icon never re-enters and the chevron stays suppressed,
    // leaving the expanded step's icon slot blank for the rest of the turn.
    if (completedAutoCloseRef.current && (iconTimerRef.current !== null || collapseTimerRef.current !== null)) {
      return
    }

    clearTimers()

    if (wasActive) {
      if (autoOpenWhileActive && !baselineOpen) {
        if (autoCollapseAfterDone) {
          completedAutoCloseRef.current = true
          setIconPhase('exiting')
          // The auto-collapse choreography must not overlap an in-flight
          // title-first unfold: a fast tool that finished inside the 320ms
          // title delay would otherwise arm the collapse BEFORE the expand
          // even starts (expand fires at +320ms, collapse at +710ms while
          // the 1s CSS transition is still running) — net effect: no
          // visible expansion. Chain the collapse off the title timer
          // instead of the completion edge.
          const collapseChainStart = () => {
            iconTimerRef.current = setTimeout(() => {
              setIconPhase('arrow')
              iconTimerRef.current = setTimeout(() => {
                setIconPhase('entering')
                iconTimerRef.current = setTimeout(() => {
                  setIconPhase('idle')
                  iconTimerRef.current = null
                }, 260)
              }, ARROW_HOLD_DELAY_MS)
              collapseTimerRef.current = setTimeout(() => {
                setIsOpen(false)
                setIconPhase('entering')
                completedAutoCloseRef.current = false
                collapseTimerRef.current = setTimeout(() => {
                  setIconPhase('idle')
                  collapseTimerRef.current = null
                }, 260)
              }, AUTO_COLLAPSE_DELAY_MS + COLLAPSE_WAIT_AFTER_UNFOLD_MS)
            }, SPINNER_EXIT_ANIMATION_MS + ARROW_REVEAL_DELAY_MS)
          }
          collapseChainStart()
          return
        } else {
          // fold-expand: body stays open after completion.
          setIconPhase('exiting')
          iconTimerRef.current = setTimeout(() => {
            setIconPhase('entering')
            iconTimerRef.current = setTimeout(() => {
              setIconPhase('idle')
              iconTimerRef.current = null
            }, 260)
          }, SPINNER_EXIT_ANIMATION_MS)
        }
      } else {
        setIconPhase('exiting')
        iconTimerRef.current = setTimeout(() => {
          setIsOpen(baselineOpen)
          setIconPhase('entering')
          iconTimerRef.current = setTimeout(() => {
            setIconPhase('idle')
            iconTimerRef.current = null
          }, 260)
        }, SPINNER_EXIT_ANIMATION_MS)
      }
      return
    }

    if (autoOpenWhileActive && wasActive) {
      setIsOpen(true)
      return
    }

    setIsOpen(autoOpenWhileActive && (wasActive || turnStreaming) ? true : baselineOpen)
    setIconPhase('idle')
  }, [autoOpenWhileActive, baselineOpen, expandable, isActive, historyLoaded, skipChoreography])

  const toggle = useCallback(() => {
    if (!expandable || isActive) return
    completedAutoCloseRef.current = false
    if (collapseTimerRef.current) {
      clearTimeout(collapseTimerRef.current)
      collapseTimerRef.current = null
    }
    if (iconTimerRef.current) {
      clearTimeout(iconTimerRef.current)
      iconTimerRef.current = null
    }
    setIconPhase('idle')
    setIsOpen(v => !v)
  }, [expandable, isActive])

  // History load: snap straight to the configured terminal state. With the
  // JS title timer removed, isOpen is already set deterministically by the
  // mount effect; this effect handles the transition where a frame was
  // mounted mid-stream (isOpen=true) and then the turn completes.
  const prevHistoryLoadedRef = useRef(historyLoaded)
  useEffect(() => {
    if (!prevHistoryLoadedRef.current && historyLoaded) {
      setIsOpen(terminalOpen)
    }
    prevHistoryLoadedRef.current = historyLoaded
  }, [historyLoaded, terminalOpen])

  // React to expansionMode changes that happen after mount (e.g. a completed
  // assistant turn stops being the last turn when a new turn starts). When
  // the mode changes to a more collapsed policy and the step is not active,
  // fold it immediately; otherwise keep it open so active-stream choreo still
  // works. Clear any pending timers to avoid fighting an in-flight collapse.
  const prevExpansionModeRef = useRef(expansionMode)
  useEffect(() => {
    if (!expandable) return
    if (prevExpansionModeRef.current === expansionMode) return
    prevExpansionModeRef.current = expansionMode
    if (isActive) return

    if (iconTimerRef.current) {
      clearTimeout(iconTimerRef.current)
      iconTimerRef.current = null
    }
    if (collapseTimerRef.current) {
      clearTimeout(collapseTimerRef.current)
      collapseTimerRef.current = null
    }

    const newBaselineOpen = expansionMode === 'open'
    const newAutoOpenWhileActive = expansionMode === 'auto-while-active' || expansionMode === 'auto-expand-once'
    const newAutoCollapseAfterDone = expansionMode === 'auto-while-active'
    const newTerminalOpen = newAutoOpenWhileActive && !newAutoCollapseAfterDone ? true : newBaselineOpen
    const newDirectOpen = newAutoOpenWhileActive ? true : newTerminalOpen
    setIsOpen(historyLoaded ? newTerminalOpen : skipChoreography ? newDirectOpen : newTerminalOpen)
    setIconPhase('idle')
  }, [expansionMode, expandable, isActive, historyLoaded, skipChoreography])

  return { isOpen, iconPhase, toggle, isActive, historyLoaded, titleFirst }
}

/** Deferred mount/unmount that stays in sync with the CSS grid-rows transition.
 *
 * When isOpen flips true: mount children immediately, then apply the `open` CSS
 * class on the next frame so the 0fr→1fr transition fires. When isOpen flips
 * false: remove the `open` class immediately (transition starts), and unmount
 * children after the transition completes.
 *
 * Returns `mounted` (whether to render children) and `renderedOpen` (whether to
 * apply the `open` CSS class). */
export function useDeferredMount(isOpen: boolean): { mounted: boolean; renderedOpen: boolean } {
  const [mounted, setMounted] = useState(isOpen)
  const [renderedOpen, setRenderedOpen] = useState(isOpen)
  const firstRunRef = useRef(true)

  useEffect(() => {
    // First run with isOpen already true (history mount at terminal state):
    // both state vars initialized to it — skip the double-rAF open beat,
    // the body renders fully open with no transition replay.
    if (firstRunRef.current) {
      firstRunRef.current = false
      return
    }
    if (isOpen) {
      setMounted(true)
      let raf2 = 0
      const raf1 = requestAnimationFrame(() => {
        raf2 = requestAnimationFrame(() => setRenderedOpen(true))
      })
      return () => { cancelAnimationFrame(raf1); cancelAnimationFrame(raf2) }
    }
    setRenderedOpen(false)
    const timer = setTimeout(() => setMounted(false), COLLAPSE_TRANSITION_MS + 20)
    return () => clearTimeout(timer)
  }, [isOpen])

  return { mounted, renderedOpen }
}

export function useLiveDuration(status: SlotStatus, slotId: string) {
  const [liveSeconds, setLiveSeconds] = useState(0)

  useEffect(() => {
    if (status !== 'running') {
      setLiveSeconds(0)
      return
    }
    setLiveSeconds(0)
    const interval = setInterval(() => {
      setLiveSeconds(s => s + 1)
    }, 1000)
    return () => clearInterval(interval)
  }, [status, slotId])

  return liveSeconds
}

export function useTurnLiveDuration(opts: {
  startedAt?: string
  completedAt?: string
  turnState?: 'running' | 'paused' | 'waiting' | 'failed' | 'completed' | 'cancelled' | 'abandoned'
  paused?: boolean
}): number {
  const { startedAt, completedAt, turnState, paused } = opts
  const [now, setNow] = useState(() => Date.now())

  // Tick only while the turn is actively running. Terminal states pin to a
  // constant (completedAt - startedAt); paused states freeze the last value.
  useEffect(() => {
    if (turnState !== 'running') return
    if (paused) return
    setNow(Date.now())
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [turnState, paused])

  if (!startedAt) return 0
  const startMs = Date.parse(startedAt)
  if (Number.isNaN(startMs)) return 0

  const isTerminal = turnState === 'waiting' || turnState === 'completed' || turnState === 'failed' || turnState === 'cancelled' || turnState === 'abandoned'
  if (isTerminal) {
    if (!completedAt) return 0
    const endMs = Date.parse(completedAt)
    if (Number.isNaN(endMs)) return 0
    return Math.max(0, Math.floor((endMs - startMs) / 1000))
  }

  // 'running' or 'paused' or undefined. While paused we freeze the displayed
  // number at the last tick — `now` doesn't advance because the interval is
  // paused. Effectively this means the timer holds at the value it had when
  // the turn entered the wait state.
  return Math.max(0, Math.floor((now - startMs) / 1000))
}

/** Normalized 0..1 streaming-activity rate.
 *
 * Counts envelope reference changes during streaming (each text/reasoning/output
 * delta produces a new envelope clone via the reducer). Sampled every 150ms,
 * normalized against a 15-events/sec ceiling, EMA-smoothed (0.7 prev + 0.3 new).
 * Decays naturally when no new envelopes arrive. */
export function useTurnTokenSpeed(envelope: TurnEnvelope | undefined, isStreaming: boolean, paused = false): number {
  const [speed, setSpeed] = useState(0)
  const stateRef = useRef({
    eventCount: 0,
    sampledCount: 0,
    sampleTs: 0,
    smoothed: 0,
  })

  useEffect(() => {
    if (!isStreaming || paused) return
    stateRef.current.eventCount += 1
  }, [envelope, isStreaming, paused])

  useEffect(() => {
    if (!isStreaming) {
      stateRef.current = { eventCount: 0, sampledCount: 0, sampleTs: 0, smoothed: 0 }
      setSpeed(0)
      return
    }
    stateRef.current.sampleTs = performance.now()
    stateRef.current.sampledCount = stateRef.current.eventCount

    const id = setInterval(() => {
      const st = stateRef.current
      const now = performance.now()
      const dt = (now - st.sampleTs) / 1000
      if (dt < 0.05) return
      const events = paused ? 0 : st.eventCount - st.sampledCount
      const rate = events / dt
      const normalized = Math.max(0, Math.min(1, rate / 15))
      st.smoothed = paused ? st.smoothed * 0.85 : st.smoothed * 0.7 + normalized * 0.3
      st.sampledCount = st.eventCount
      st.sampleTs = now
      setSpeed(st.smoothed)
    }, 150)

    return () => clearInterval(id)
  }, [isStreaming, paused])

  return speed
}

/** Estimate token count from text using a CJK-aware heuristic.
 *  CJK characters count as ~1 token each; other characters ~4 chars/token.
 *  Purely a frontend visual estimate — does not enter cost/cache/requestStats. */
export function estimateTokensFromText(s: string): number {
  let cjk = 0
  let other = 0
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    if (
      (c >= 0x4E00 && c <= 0x9FFF) || // CJK Unified Ideographs
      (c >= 0x3400 && c <= 0x4DBF) || // CJK Extension A
      (c >= 0x3000 && c <= 0x30FF) || // CJK symbols / Hiragana / Katakana
      (c >= 0xFF00 && c <= 0xFFEF) || // Fullwidth forms
      (c >= 0xAC00 && c <= 0xD7AF)    // Hangul Syllables
    ) {
      cjk++
    } else {
      other++
    }
  }
  return cjk + other / 4
}

/** True when a tool frame is currently executing. Tool-call time is excluded
 *  from throughput: the LLM is not generating while a tool runs, so the
 *  timeline freezes (holds its last value) until the tool returns. */
function hasRunningTool(env: TurnEnvelope | undefined): boolean {
  if (!env) return false
  return env.frames.some(f => f.type === 'tool' && f.status === 'running')
}

/** Rolling-window length (seconds) for the throughput timeline. */
const THROUGHPUT_WINDOW_SEC = 30
/** Sampling interval (ms) — one point per tick on the timeline. */
const THROUGHPUT_SAMPLE_MS = 200
const THROUGHPUT_POINTS = Math.round((THROUGHPUT_WINDOW_SEC * 1000) / THROUGHPUT_SAMPLE_MS)

export interface TokenThroughput {
  /** Ordered oldest→newest sampled token/s, length ≤ THROUGHPUT_POINTS.
   *  Scrolls while streaming; frozen (last window) once the turn ends. */
  history: number[]
  /** Most recent sampled token/s (0 before the first sample). */
  rate: number
}

interface ThroughputState {
  frameLens: Map<string, number>
  deltaTokens: number
  sampleTs: number
  /** tok/s per sampled tick (ring buffer). */
  ring: number[]
  /** performance.now() of each tick (ring buffer, parallel to ring). */
  ringTs: number[]
  /** dt in seconds each tick covers (ring buffer, parallel to ring). */
  ringDt: number[]
  /** True when the tick held its value (tool running / paused). */
  ringHeld: boolean[]
  head: number
  filled: number
  latest: number
  wasInTool: boolean
  /** performance.now() of the last observed token growth — base for
   *  retroactive tool-argument attribution. */
  lastActiveTs: number
  /** Mirrors the hook's last-render isStreaming; a still-streaming entry
   *  survives unmount (agent switch), a finished one is dropped. */
  streaming: boolean
  lastTouch: number
}

function freshThroughputState(): ThroughputState {
  return {
    frameLens: new Map(),
    deltaTokens: 0,
    sampleTs: 0,
    ring: new Array<number>(THROUGHPUT_POINTS).fill(0),
    ringTs: new Array<number>(THROUGHPUT_POINTS).fill(0),
    ringDt: new Array<number>(THROUGHPUT_POINTS).fill(0),
    ringHeld: new Array<boolean>(THROUGHPUT_POINTS).fill(false),
    head: 0,
    filled: 0,
    latest: 0,
    wasInTool: false,
    lastActiveTs: performance.now(),
    streaming: false,
    lastTouch: Date.now(),
  }
}

/** Turn timelines keyed by envelope id, surviving TurnTail unmount/remount:
 *  switching agents and back must resume the measurement, not restart from
 *  zero. Entries of turns that stopped streaming are dropped on detach;
 *  orphaned streaming entries are pruned by age. */
const throughputStore = new Map<string, ThroughputState>()
const THROUGHPUT_STORE_TTL_MS = 10 * 60_000

/** Growth source for the token estimate: text/reasoning content plus the
 *  tool-call `input` JSON — the model generates those argument tokens too.
 *  Tool RESULTS (frame.output) are input-side tokens for the next request,
 *  not generated output, so they are never counted. */
function frameTokenSource(f: TurnEnvelope['frames'][number]): string | undefined {
  if (f.type === 'text' || f.type === 'reasoning') return (f as { content?: string }).content
  if (f.type === 'tool') return (f as { input?: string }).input
  return undefined
}

/** Adopt or create the store entry for an envelope, re-seeding frame lengths
 *  to the CURRENT content: tokens that arrived while unmounted are never
 *  re-counted as a one-tick burst (the away span simply stays unsampled). */
function attachThroughput(id: string | undefined, envelope: TurnEnvelope | undefined): ThroughputState {
  if (throughputStore.size > 8) {
    const cutoff = Date.now() - THROUGHPUT_STORE_TTL_MS
    for (const [k, st] of throughputStore) {
      if (st.lastTouch < cutoff) throughputStore.delete(k)
    }
  }
  const now = performance.now()
  let st = id ? throughputStore.get(id) : undefined
  if (!st) {
    st = freshThroughputState()
    if (id) throughputStore.set(id, st)
  }
  st.frameLens.clear()
  if (envelope) {
    for (const f of envelope.frames) {
      const src = frameTokenSource(f)
      if (src != null) st.frameLens.set(f.id, src.length)
    }
  }
  st.deltaTokens = 0
  st.sampleTs = now
  st.lastActiveTs = now
  st.lastTouch = Date.now()
  return st
}

function detachThroughput(id: string | undefined, st: ThroughputState | null): void {
  if (!id || !st) return
  if (!st.streaming) throughputStore.delete(id)
}

/** Tool-call arguments arrive as one block (the backend buffers the argument
 *  stream and emits the tool_use block only when complete), so they cannot be
 *  sampled tick-by-tick. Attribute the burst retroactively: spread it as a
 *  constant rate over the silent window since the last token growth, so the
 *  zero samples that window wrote become an honest plateau instead of a
 *  single-tick spike that would dwarf the chart scale. */
function backfillToolTokens(st: ThroughputState, tokens: number): void {
  const now = performance.now()
  const windowSec = Math.max(0, (now - st.lastActiveTs) / 1000)
  if (windowSec < 0.4) {
    st.deltaTokens += tokens
    return
  }
  const rate = tokens / windowSec
  for (let i = 0; i < st.filled; i++) {
    const idx = (st.head - st.filled + i + THROUGHPUT_POINTS) % THROUGHPUT_POINTS
    if (st.ringHeld[idx] === true) continue
    if ((st.ringTs[idx] ?? 0) <= st.lastActiveTs) continue
    st.ring[idx] = (st.ring[idx] ?? 0) + rate
  }
  // Window span not yet sampled (since the last tick): credit it to the
  // current accumulation so the next sample carries it.
  st.deltaTokens += rate * Math.min(Math.max(0, (now - st.sampleTs) / 1000), windowSec)
  if (st.filled > 0) st.latest = st.ring[(st.head - 1 + THROUGHPUT_POINTS) % THROUGHPUT_POINTS] ?? 0
}

/** Test-only: clear the cross-mount throughput store for isolation. */
export function resetThroughputStoreForTests(): void {
  throughputStore.clear()
}

/** Live output token throughput timeline (tokens/sec), estimated purely on the
 *  frontend from growth of assistant text/reasoning/tool-call-argument
 *  content. Tool-call arguments count; tool results do not.
 *
 *  The backend does not stream per-delta token counts (usage only arrives on
 *  step.closed / delivery), so we estimate from content growth. The estimate is
 *  a visual monitor only — it never feeds cost/cache/requestStats.
 *
 *  Returns a 30s rolling window of sampled token/s for a sparkline timeline,
 *  plus the latest sample. The history is frozen (not cleared) when streaming
 *  stops, so a just-completed turn keeps its last window visible. State lives
 *  in a module store keyed by envelope id, so unmounting (agent switch) and
 *  remounting resumes the same timeline instead of restarting from zero. */
export function useTokenThroughput(
  envelope: TurnEnvelope | undefined,
  isStreaming: boolean,
  paused = false,
): TokenThroughput {
  const [, setTick] = useState(0)
  const envIdRef = useRef<string | undefined>(envelope?.id)
  const envelopeRef = useRef(envelope)
  envelopeRef.current = envelope
  const stateRef = useRef<ThroughputState | null>(null)

  // Attach during render so the returned history/rate are available on the
  // first render after a mount or envelope-id switch.
  if (stateRef.current === null || envIdRef.current !== envelope?.id) {
    const prev = stateRef.current
    const prevId = envIdRef.current
    envIdRef.current = envelope?.id
    stateRef.current = attachThroughput(envelope?.id, envelope)
    if (prevId != null && prevId !== envelope?.id) detachThroughput(prevId, prev)
  }
  const st = stateRef.current!

  // Recorded from the last render so the unmount cleanup can distinguish
  // "switched away mid-stream" (keep entry) from "turn finished" (drop).
  st.streaming = isStreaming

  // Drop the store entry on unmount when the turn already ended.
  useEffect(() => () => {
    detachThroughput(envIdRef.current, stateRef.current)
  }, [])

  // Accumulate token deltas from new content appended to text/reasoning
  // frames, and tool-call argument blocks when they arrive.
  useEffect(() => {
    if (!isStreaming || paused || !envelope) return
    let grew = false
    for (const f of envelope.frames) {
      const content = frameTokenSource(f)
      if (content == null) continue
      const prev = st.frameLens.get(f.id) ?? 0
      if (content.length > prev) {
        const deltaTokens = estimateTokensFromText(content.slice(prev))
        if (f.type === 'tool') backfillToolTokens(st, deltaTokens)
        else st.deltaTokens += deltaTokens
        st.lastActiveTs = performance.now()
        grew = true
      }
      st.frameLens.set(f.id, content.length)
    }
    if (grew) setTick(n => (n + 1) & 0xffff)
  }, [envelope, isStreaming, paused, st])

  // Sample every THROUGHPUT_SAMPLE_MS: convert accumulated delta → token/s and
  // push into the rolling ring buffer. While a tool is running (or paused) the
  // timeline freezes — it repeats the last sampled value instead of dropping
  // to 0, and the sampling clock is reset so the frozen interval is excluded
  // from the next active sample's dt. History is not cleared on stop (frozen).
  useEffect(() => {
    if (!isStreaming) return
    st.sampleTs = performance.now()
    const id = setInterval(() => {
      const now = performance.now()
      const dt = (now - st.sampleTs) / 1000
      if (dt < 0.05) return
      const inTool = hasRunningTool(envelopeRef.current)
      const prev = st.filled > 0
        ? st.ring[(st.head - 1 + THROUGHPUT_POINTS) % THROUGHPUT_POINTS] ?? 0
        : 0
      let tokPerSec: number
      const held = paused || inTool
      if (held) {
        // Frozen: hold the last value; reset clock so the frozen span is not
        // charged to the next active sample.
        tokPerSec = prev
        st.deltaTokens = 0
        st.sampleTs = now
      } else if (st.wasInTool) {
        // Just exited a tool call: reset the clock so the next active dt covers
        // only post-tool generation; hold the value for this tick.
        tokPerSec = prev
        st.deltaTokens = 0
        st.sampleTs = now
      } else {
        tokPerSec = st.deltaTokens / dt
        st.deltaTokens = 0
        st.sampleTs = now
      }
      st.wasInTool = paused ? st.wasInTool : inTool
      st.ring[st.head] = tokPerSec
      st.ringTs[st.head] = now
      st.ringDt[st.head] = dt
      st.ringHeld[st.head] = held
      st.head = (st.head + 1) % THROUGHPUT_POINTS
      if (st.filled < THROUGHPUT_POINTS) st.filled++
      st.latest = tokPerSec
      st.lastTouch = Date.now()
      setTick(n => (n + 1) & 0xffff)
    }, THROUGHPUT_SAMPLE_MS)
    return () => clearInterval(id)
  }, [isStreaming, paused, st])

  const len = st.filled
  let history: number[]
  if (len === 0) {
    history = []
  } else {
    const start = (st.head - len + THROUGHPUT_POINTS) % THROUGHPUT_POINTS
    history = new Array(len)
    for (let i = 0; i < len; i++) {
      history[i] = st.ring[(start + i) % THROUGHPUT_POINTS] ?? 0
    }
  }
  return { history, rate: st.latest }
}
