import { useCallback, useRef } from 'react'

import { useI18n } from '../../../i18n'
import { useShellSession } from './useShellSession'
import './workbenchTerminal.css'

export interface WorkbenchTerminalPanelProps {
  /** Expanded cards fill the main slot; compact cards render host metadata only. */
  expanded?: boolean
  /** Whether the shell session should be open (false → idle, no session). */
  active?: boolean
  /** Working directory for shell.session_open. */
  projectRoot?: string
  /** Fired on every new shell output chunk (drives the presence controller). */
  onActivity?: () => void
  className?: string
}

/**
 * Workbench terminal card body.
 *
 * Renders a real interactive shell (reusing the shell actor session flow) as a
 * dark scrolling console, matching the blackboard reference `.termbody` skin.
 * Output streams into the xterm scrollback chunk by chunk via the existing
 * `shell.session_output` event channel — no new protocol.
 */
export function WorkbenchTerminalPanel({
  expanded = false,
  active = true,
  projectRoot,
  onActivity,
  className,
}: WorkbenchTerminalPanelProps) {
  const { t } = useI18n()
  const onActivityRef = useRef(onActivity)
  onActivityRef.current = onActivity

  const handleOutput = useCallback(() => {
    onActivityRef.current?.()
  }, [])

  const session = useShellSession({ projectRoot, enabled: active, onOutput: handleOutput })

  const { phase, exitCode, error } = session.state
  let status: string | null = null
  if (phase === 'error') {
    status = error ? `${t('terminalPanel.shell.openError')}: ${error}` : t('terminalPanel.shell.openError')
  } else if (phase === 'exited') {
    status = t('terminalPanel.shell.exitBanner', { code: exitCode ?? 0 })
  }

  const classes = ['wb-term', expanded ? 'wb-term--expanded' : 'wb-term--compact', className]
    .filter(Boolean)
    .join(' ')

  return (
    <div className={classes} data-phase={phase}>
      <div
        className="wb-term-body"
        ref={session.containerRef}
        onMouseDown={() => session.focus()}
      />
      {status && (
        <div className="wb-term-status" data-phase={phase}>
          {status}
        </div>
      )}
    </div>
  )
}
