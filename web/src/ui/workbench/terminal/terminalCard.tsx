import { WorkbenchTerminalPanel } from './WorkbenchTerminalPanel'
import type { WorkbenchCardDescriptor } from '../cardTypes'
import { TERMINAL_CARD_ID } from './ids'

// The canonical workbench card contract lives in the card base
// (`../cardTypes`); the terminal card is one descriptor implementation of it.
export type { WorkbenchCardDescriptor, WorkbenchCardKind, WorkbenchCardCompactMeta } from '../cardTypes'

/** Registry key for the workbench terminal card (blackboard `term`). */
export { TERMINAL_CARD_ID }

export interface TerminalCardOptions {
  id?: string
  title?: string
  icon?: string
  color?: string
  /** Working directory handed to the shell session. */
  projectRoot?: string
  /** One-line compact status (from the presence controller / session state). */
  statusText?: string
  /** Whether the shell session should be open. */
  active?: boolean
  /** Fired on each new shell output chunk (presence controller input). */
  onActivity?: () => void
}

/**
 * Build the workbench terminal card descriptor. The card base registers this in
 * its card registry; the `render` body is the real interactive shell
 * ({@link WorkbenchTerminalPanel}), not a placeholder.
 */
export function createTerminalCardDescriptor(options: TerminalCardOptions = {}): WorkbenchCardDescriptor {
  const {
    id = TERMINAL_CARD_ID,
    title = 'Terminal',
    icon = 'terminal',
    color,
    projectRoot,
    statusText = 'Terminal',
    active = true,
    onActivity,
  } = options

  return {
    id,
    kind: 'terminal',
    title,
    icon,
    ...(color ? { color } : {}),
    score: 34,
    pinned: false,
    compactMeta: { statusText },
    render: (expanded: boolean) => (
      <WorkbenchTerminalPanel
        expanded={expanded}
        active={active}
        projectRoot={projectRoot}
        onActivity={onActivity}
      />
    ),
  }
}
