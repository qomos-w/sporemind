import React from 'react'
import { Download, RefreshCw } from 'lucide-react'
import { useI18n } from '../../../i18n'
import type { UpdateStatus } from './useAppUpdate'

interface Props {
  status: UpdateStatus | null
  onClick: () => void
}

/**
 * Titlebar button that surfaces the update state. Rendered only in Wails
 * desktop mode when the autoUpdate feature flag is on. Hidden in phases where
 * there's nothing actionable (idle / disabled).
 */
export const AppUpdateButton: React.FC<Props> = ({ status, onClick }) => {
  const { t } = useI18n()
  if (!status) return null

  const phase = status.phase
  if (phase === 'idle' || phase === '' || phase === 'checking') return null

  if (phase === 'available') {
    const label = t('appUpdate.button.available', { version: status.remote?.public_version ?? '' })
    return (
      <button
        type="button"
        className="ai-shell-update-btn available"
        onClick={onClick}
        title={label}
        aria-label={label}
      >
        <Download size={14} />
        <span className="ai-shell-update-btn-label">{label}</span>
      </button>
    )
  }

  if (phase === 'downloading') {
    const label = t('appUpdate.button.downloading', { progress: status.progress })
    return (
      <button
        type="button"
        className="ai-shell-update-btn downloading"
        onClick={onClick}
        title={label}
        aria-label={label}
      >
        <RefreshCw size={14} className="ai-shell-update-spin" />
        <span className="ai-shell-update-btn-label">{label}</span>
      </button>
    )
  }

  if (phase === 'ready') {
    const label = t('appUpdate.button.ready')
    return (
      <button
        type="button"
        className="ai-shell-update-btn ready"
        onClick={onClick}
        title={label}
        aria-label={label}
      >
        <Download size={14} />
        <span className="ai-shell-update-btn-label">{label}</span>
      </button>
    )
  }

  if (phase === 'error') {
    const label = t('appUpdate.button.error')
    return (
      <button
        type="button"
        className="ai-shell-update-btn error"
        onClick={onClick}
        title={label}
        aria-label={label}
      >
        <RefreshCw size={14} />
        <span className="ai-shell-update-btn-label">{label}</span>
      </button>
    )
  }

  return null
}
