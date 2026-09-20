/**
 * Terminal card presence policy for the workbench (spec §5).
 *
 * The workbench terminal card mirrors the blackboard reference: it is HIDDEN by
 * default and is summoned either by a keyword mention (agent/user text talks
 * about the terminal, builds, commands, errors…) or by live shell output, then
 * automatically retreats back to hidden once no new output has arrived for
 * `idleMs`.
 *
 * This module is a pure state machine so the policy is unit-testable without a
 * DOM; the React binding lives in `useTerminalPresence`. The workbench surface
 * (card base) feeds `text` events from the message/turn stream and `output`
 * events from the terminal panel, then reads `hidden` to drive slot placement.
 */

/**
 * Default summon keywords. Intentionally ASCII-only: localized terms (e.g. the
 * localized card title) are merged in by the caller at runtime so no
 * untranslated CJK literal is embedded in source.
 */
export const DEFAULT_TERMINAL_KEYWORDS: readonly string[] = [
  'terminal',
  'shell',
  'console',
  'bash',
  'command',
  'build',
  'compile',
  'make',
  'test',
  'error',
  'stdout',
  'stderr',
  'run ',
  'npm',
  'go test',
]

/** Idle window after which a visible terminal retreats to hidden. */
export const DEFAULT_TERMINAL_RETREAT_IDLE_MS = 30_000

/** Why the terminal card is currently visible. */
export type TerminalSummonReason = 'initial' | 'keyword' | 'manual' | 'launch' | 'output'

export interface TerminalPresenceState {
  /** True when the terminal card is retreated (hidden); false when summoned. */
  hidden: boolean
  /** Epoch ms of the last summon or shell output activity. */
  lastActivityAt: number
  /** Epoch ms of the last explicit summon (keyword/manual/launch). 0 when never. */
  summonedAt: number
  /** Why the terminal card is currently visible. */
  reason: TerminalSummonReason
}

export interface TerminalPresencePolicy {
  keywords: readonly string[]
  idleMs: number
}

export function defaultTerminalPresencePolicy(): TerminalPresencePolicy {
  return { keywords: DEFAULT_TERMINAL_KEYWORDS, idleMs: DEFAULT_TERMINAL_RETREAT_IDLE_MS }
}

export function initialTerminalPresence(now = 0): TerminalPresenceState {
  return { hidden: true, lastActivityAt: now, summonedAt: 0, reason: 'initial' }
}

export type TerminalPresenceEvent =
  /** Message/turn text. Summons on a keyword hit. */
  | { type: 'text'; text: string; source?: 'user' | 'agent'; at?: number }
  /** New shell output chunk. Summons (or keeps visible) and resets the idle clock. */
  | { type: 'output'; at?: number }
  /** Explicit summon (keyword driven elsewhere, panel launch, user action). */
  | { type: 'summon'; reason?: TerminalSummonReason; at?: number }
  /** Explicit retreat (user hid the terminal). */
  | { type: 'hide' }
  /** Clock tick; retreats a visible terminal once the idle window elapsed. */
  | { type: 'tick'; at?: number }

/**
 * Case-insensitive substring keyword match. Substring (not word-boundary)
 * matching keeps multi-word phrases (`go test`) and partial tokens
 * (`build-desktop`) working, matching the blackboard reference semantics.
 */
export function matchesTerminalKeyword(text: string, keywords: readonly string[]): boolean {
  if (!text) return false
  const haystack = text.toLowerCase()
  for (const keyword of keywords) {
    if (!keyword) continue
    if (haystack.includes(keyword.toLowerCase())) return true
  }
  return false
}

/** Apply one presence event. Pure; `at` defaults to the current activity clock. */
export function reduceTerminalPresence(
  state: TerminalPresenceState,
  event: TerminalPresenceEvent,
  policy: TerminalPresencePolicy = defaultTerminalPresencePolicy(),
): TerminalPresenceState {
  switch (event.type) {
    case 'text': {
      if (!matchesTerminalKeyword(event.text, policy.keywords)) return state
      const at = event.at ?? state.lastActivityAt
      if (!state.hidden && state.reason === 'keyword') {
        return { ...state, lastActivityAt: at }
      }
      return { hidden: false, lastActivityAt: at, summonedAt: at, reason: 'keyword' }
    }
    case 'output': {
      const at = event.at ?? state.lastActivityAt
      if (!state.hidden) {
        // Already visible: just reset the idle clock.
        return { ...state, lastActivityAt: at }
      }
      // Output on a retreated card re-summons it.
      return { hidden: false, lastActivityAt: at, summonedAt: at, reason: 'output' }
    }
    case 'summon': {
      const at = event.at ?? state.lastActivityAt
      return { hidden: false, lastActivityAt: at, summonedAt: at, reason: event.reason ?? 'manual' }
    }
    case 'hide':
      return state.hidden ? state : { ...state, hidden: true }
    case 'tick': {
      if (state.hidden) return state
      const at = event.at ?? state.lastActivityAt
      if (at - state.lastActivityAt >= policy.idleMs) {
        return { ...state, hidden: true }
      }
      return state
    }
  }
}
