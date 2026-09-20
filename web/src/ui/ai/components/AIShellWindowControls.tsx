import React from 'react'
import { Copy, Minus, Square, X } from 'lucide-react'
import { wailsWindowController } from '../../../application/window-controller'
import { useI18n } from '../../../i18n'

interface AIShellWindowControlsProps {
  maximised: boolean
  onMaximisedChange: (v: boolean) => void
}

export const AIShellWindowControls: React.FC<AIShellWindowControlsProps> = ({ maximised, onMaximisedChange }) => {
  const { t } = useI18n()
  return (
    <div className="ai-shell-win-controls">
      <button className="ai-shell-win-btn" onClick={() => { void wailsWindowController.minimise() }} title={t('shell.topbar.window.minimize')} type="button">
        <Minus size={14} />
      </button>
      <button
        className="ai-shell-win-btn"
        onClick={() => { wailsWindowController.toggleMaximise().then(() => wailsWindowController.isMaximised().then(onMaximisedChange)) }}
        title={maximised ? t('shell.topbar.window.restore') : t('shell.topbar.window.maximize')}
        type="button"
      >
        {maximised
          ? <Copy size={14} />
          : <Square size={13} />
        }
      </button>
      <button className="ai-shell-win-btn close" onClick={() => { void wailsWindowController.quit() }} title={t('shell.topbar.window.close')} type="button">
        <X size={15} />
      </button>
    </div>
  )
}
