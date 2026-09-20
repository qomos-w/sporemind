export const ICON_ARROW_ANIMATION_MS = 420
export const SPINNER_EXIT_ANIMATION_MS = 170
export const ARROW_REVEAL_DELAY_MS = 40
export const ARROW_HOLD_DELAY_MS = 400
export const AUTO_COLLAPSE_DELAY_MS = 500
/** How long an auto-expanded body stays open after the (possibly still
 * running) CSS unfold completes. Auto-collapse must wait out the full
 * expansion transition or the user never sees the open state. */
export const COLLAPSE_WAIT_AFTER_UNFOLD_MS = 1000
/** Title-first reveal: how long the collapsed step row (tool title) stays
 * on screen before the body unfolds. Applies to every auto-expanding tool
 * step, shell/run-command included. */
export const TOOL_TITLE_DELAY_MS = 320
/** CSS transition duration for .ai-collapsible (grid-template-rows). The unmount
 *  timer adds a small buffer so children stay alive during the closing animation. */
export const COLLAPSE_TRANSITION_MS = 260

export type IconPhase = 'idle' | 'spinning' | 'exiting' | 'arrow' | 'entering'
export type ArrowVisualState = 'icon' | 'right' | 'down' | 'to-right' | 'to-down'
export type TimelineExpansionMode = 'manual' | 'auto-while-active' | 'auto-expand-once' | 'open'
