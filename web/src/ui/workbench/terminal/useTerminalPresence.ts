import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react'

import {
  DEFAULT_TERMINAL_KEYWORDS,
  DEFAULT_TERMINAL_RETREAT_IDLE_MS,
  initialTerminalPresence,
  reduceTerminalPresence,
  type TerminalPresenceEvent,
  type TerminalPresencePolicy,
  type TerminalPresenceState,
  type TerminalSummonReason,
} from './terminalPresence'

export interface UseTerminalPresenceOptions {
  /** Summon keywords; defaults to {@link DEFAULT_TERMINAL_KEYWORDS}. */
  keywords?: readonly string[]
  /** Idle window before auto-retreat; defaults to 30s. */
  idleMs?: number
  /** Tick cadence while visible; defaults to min(idleMs, 5s). */
  tickMs?: number
  /** Start summoned (e.g. the user launched the terminal from the dock). */
  initiallyVisible?: boolean
  /** Clock override for deterministic tests; defaults to Date.now. */
  now?: () => number
}

export interface TerminalPresenceController {
  /** True when the terminal card should be retreated. */
  hidden: boolean
  /** Why the terminal card is currently visible. */
  reason: TerminalSummonReason
  /** Epoch ms of the last summon/output activity. */
  lastActivityAt: number
  summon: (reason?: TerminalSummonReason) => void
  hide: () => void
  /** Feed message/turn text; summons on a keyword hit. */
  noteText: (text: string, source?: 'user' | 'agent') => void
  /** Feed a shell output chunk; summons or keeps visible and resets the idle clock. */
  noteOutput: () => void
}

function presenceReducer(
  policyRef: { current: TerminalPresencePolicy },
  state: TerminalPresenceState,
  event: TerminalPresenceEvent,
): TerminalPresenceState {
  return reduceTerminalPresence(state, event, policyRef.current)
}

/**
 * React binding for {@link reduceTerminalPresence}. Owns the idle timer while
 * the terminal is visible; the reducer stays the single source of truth.
 */
export function useTerminalPresence(options: UseTerminalPresenceOptions = {}): TerminalPresenceController {
  const nowFn = options.now ?? Date.now
  const nowRef = useRef(nowFn)
  nowRef.current = nowFn

  const policy = useMemo<TerminalPresencePolicy>(
    () => ({
      keywords: options.keywords ?? DEFAULT_TERMINAL_KEYWORDS,
      idleMs: options.idleMs ?? DEFAULT_TERMINAL_RETREAT_IDLE_MS,
    }),
    [options.keywords, options.idleMs],
  )
  const policyRef = useRef(policy)
  policyRef.current = policy

  const [state, dispatch] = useReducer(
    (s: TerminalPresenceState, e: TerminalPresenceEvent) => presenceReducer(policyRef, s, e),
    undefined,
    () => {
      const base = initialTerminalPresence(nowRef.current())
      if (!options.initiallyVisible) return base
      return { ...base, hidden: false, summonedAt: base.lastActivityAt, reason: 'launch' as const }
    },
  )

  const hidden = state.hidden
  const idleMs = policy.idleMs
  const tickMs = options.tickMs ?? Math.min(idleMs, 5000)

  useEffect(() => {
    if (hidden) return
    const id = setInterval(() => dispatch({ type: 'tick', at: nowRef.current() }), tickMs)
    return () => clearInterval(id)
  }, [hidden, tickMs])

  const summon = useCallback((reason: TerminalSummonReason = 'manual') => {
    dispatch({ type: 'summon', reason, at: nowRef.current() })
  }, [])

  const hide = useCallback(() => {
    dispatch({ type: 'hide' })
  }, [])

  const noteText = useCallback((text: string, source?: 'user' | 'agent') => {
    dispatch({ type: 'text', text, source, at: nowRef.current() })
  }, [])

  const noteOutput = useCallback(() => {
    dispatch({ type: 'output', at: nowRef.current() })
  }, [])

  return useMemo(
    () => ({
      hidden: state.hidden,
      reason: state.reason,
      lastActivityAt: state.lastActivityAt,
      summon,
      hide,
      noteText,
      noteOutput,
    }),
    [state.hidden, state.reason, state.lastActivityAt, summon, hide, noteText, noteOutput],
  )
}
