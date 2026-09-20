/**
 * Workbench terminal card — public surface.
 *
 * The card base (workbench surface + registry) consumes:
 *   - `createTerminalCardDescriptor()` — a WorkbenchCardDescriptor the registry
 *     renders in the `term` card (compact metadata + real expanded body).
 *   - `useTerminalPresence()` — the hidden-by-default / keyword-summon /
 *     auto-retreat controller for the terminal card's slot placement.
 *
 * The body ({@link WorkbenchTerminalPanel}) reuses the existing shell actor
 * session flow (`shell.session_open` + `shell.session_output` + `session_fetch`)
 * — no new backend or protocol.
 */
export { WorkbenchTerminalPanel } from './WorkbenchTerminalPanel'
export type { WorkbenchTerminalPanelProps } from './WorkbenchTerminalPanel'

export { createTerminalCardDescriptor, TERMINAL_CARD_ID } from './terminalCard'
export type {
  WorkbenchCardDescriptor,
  WorkbenchCardKind,
  WorkbenchCardCompactMeta,
  TerminalCardOptions,
} from './terminalCard'

export { useShellSession } from './useShellSession'
export type {
  ShellSessionPhase,
  ShellSessionState,
  UseShellSessionOptions,
  UseShellSessionResult,
} from './useShellSession'

export { useTerminalPresence } from './useTerminalPresence'
export type { TerminalPresenceController, UseTerminalPresenceOptions } from './useTerminalPresence'

export {
  reduceTerminalPresence,
  initialTerminalPresence,
  matchesTerminalKeyword,
  defaultTerminalPresencePolicy,
  DEFAULT_TERMINAL_KEYWORDS,
  DEFAULT_TERMINAL_RETREAT_IDLE_MS,
} from './terminalPresence'
export type {
  TerminalPresenceEvent,
  TerminalPresencePolicy,
  TerminalPresenceState,
  TerminalSummonReason,
} from './terminalPresence'
