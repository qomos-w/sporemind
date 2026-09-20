import { useI18n } from '../../../../i18n'

/**
 * Placeholder empty-state components for the four built-in terminal tab
 * types. Each is a lightweight guidance card rendered until the real
 * implementation lands in a follow-up task. A future tab implementation
 * only needs to ship its own component file and register one entry in
 * {@link terminalTabRegistry} — these placeholders are not required to
 * change.
 */

function PlaceholderShell() {
  const { t } = useI18n()
  return (
    <div className="terminal-placeholder">
      <div className="terminal-placeholder-title">{t('terminalPanel.placeholder.shell.title')}</div>
      <div className="terminal-placeholder-hint">{t('terminalPanel.placeholder.shell.hint')}</div>
    </div>
  )
}

function PlaceholderBackendLog() {
  const { t } = useI18n()
  return (
    <div className="terminal-placeholder">
      <div className="terminal-placeholder-title">{t('terminalPanel.placeholder.backendLog.title')}</div>
      <div className="terminal-placeholder-hint">{t('terminalPanel.placeholder.backendLog.hint')}</div>
    </div>
  )
}

function PlaceholderAppConsole() {
  const { t } = useI18n()
  return (
    <div className="terminal-placeholder">
      <div className="terminal-placeholder-title">{t('terminalPanel.placeholder.appConsole.title')}</div>
      <div className="terminal-placeholder-hint">{t('terminalPanel.placeholder.appConsole.hint')}</div>
    </div>
  )
}

function PlaceholderDebug() {
  const { t } = useI18n()
  return (
    <div className="terminal-placeholder">
      <div className="terminal-placeholder-title">{t('terminalPanel.placeholder.debug.title')}</div>
      <div className="terminal-placeholder-hint">{t('terminalPanel.placeholder.debug.hint')}</div>
    </div>
  )
}

export {
  PlaceholderShell,
  PlaceholderBackendLog,
  PlaceholderAppConsole,
  PlaceholderDebug,
}
