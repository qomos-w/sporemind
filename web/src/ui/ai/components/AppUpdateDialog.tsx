import React from 'react'
import { Download, RefreshCw, X } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { useBrowserOverlay } from '../browserOverlay'
import type { UpdateStatus } from './useAppUpdate'
import './AppUpdateDialog.css'

interface Props {
  open: boolean
  status: UpdateStatus | null
  onClose: () => void
  onCheck: () => void
  onDownload: () => void
  onInstall: () => void
  onReveal: () => void
  onDismiss: () => void
}

/**
 * Update dialog — opened from the topbar AppUpdateButton. Shows the current
 * and remote versions, the changelog notes when present, and the primary
 * action appropriate to the current phase.
 */
export const AppUpdateDialog: React.FC<Props> = ({
  open,
  status,
  onClose,
  onCheck,
  onDownload,
  onInstall,
  onReveal,
  onDismiss,
}) => {
  const { t } = useI18n()
  useBrowserOverlay(open)

  if (!open || !status) return null

  const phase = status.phase
  const remote = status.remote

  const renderBody = () => {
    switch (phase) {
      case 'available':
        return (
          <>
            <div className="app-update-row">
              <span className="app-update-label">{t('appUpdate.current')}</span>
              <span className="app-update-value">{status.current_version || '—'}</span>
            </div>
            <div className="app-update-row">
              <span className="app-update-label">{t('appUpdate.latest')}</span>
              <span className="app-update-value app-update-latest">{remote?.public_version ?? '—'}</span>
            </div>
            {remote?.notes ? <p className="app-update-notes">{remote.notes}</p> : null}
          </>
        )
      case 'downloading':
        return (
          <>
            <div className="app-update-row">
              <span className="app-update-label">{t('appUpdate.latest')}</span>
              <span className="app-update-value">{remote?.public_version ?? '—'}</span>
            </div>
            <div className="app-update-progress">
              <div
                className="app-update-progress-fill"
                style={{ width: `${status.progress}%` }}
              />
            </div>
            <p className="app-update-desc">{t('appUpdate.downloading', { progress: status.progress })}</p>
          </>
        )
      case 'ready':
        return (
          <>
            <div className="app-update-row">
              <span className="app-update-label">{t('appUpdate.latest')}</span>
              <span className="app-update-value">{remote?.public_version ?? '—'}</span>
            </div>
            <p className="app-update-desc">{t('appUpdate.readyDesc')}</p>
            <p className="app-update-path">{status.path}</p>
          </>
        )
      case 'error':
        return (
          <>
            <div className="app-update-row">
              <span className="app-update-label">{t('appUpdate.latest')}</span>
              <span className="app-update-value">{remote?.public_version ?? '—'}</span>
            </div>
            <p className="app-update-error">{status.error}</p>
          </>
        )
      default:
        return <p className="app-update-desc">{t('appUpdate.idleDesc')}</p>
    }
  }

  const renderActions = () => {
    switch (phase) {
      case 'available':
        return (
          <>
            <button type="button" className="app-update-btn secondary" onClick={onDismiss}>
              {t('appUpdate.later')}
            </button>
            <button type="button" className="app-update-btn primary" onClick={onDownload}>
              <Download size={14} />
              {t('appUpdate.download')}
            </button>
          </>
        )
      case 'downloading':
        return (
          <button type="button" className="app-update-btn secondary" disabled>
            <RefreshCw size={14} className="ai-shell-update-spin" />
            {t('appUpdate.downloading', { progress: status.progress })}
          </button>
        )
      case 'ready':
        return (
          <>
            <button type="button" className="app-update-btn secondary" onClick={onReveal}>
              {t('appUpdate.reveal')}
            </button>
            <button type="button" className="app-update-btn primary" onClick={onInstall}>
              {t('appUpdate.install')}
            </button>
          </>
        )
      case 'error':
        return (
          <>
            <button type="button" className="app-update-btn secondary" onClick={onClose}>
              {t('common.close')}
            </button>
            <button type="button" className="app-update-btn primary" onClick={onCheck}>
              {t('appUpdate.retry')}
            </button>
          </>
        )
      default:
        return (
          <button type="button" className="app-update-btn primary" onClick={onCheck}>
            {t('appUpdate.checkNow')}
          </button>
        )
    }
  }

  return (
    <div className="app-update-overlay" role="dialog" aria-modal="true" aria-label={t('appUpdate.title')}>
      <div className="app-update-backdrop" onClick={onClose} />
      <div className="app-update-panel">
        <div className="app-update-header">
          <h3 className="app-update-title">{t('appUpdate.title')}</h3>
          <button type="button" className="app-update-close" onClick={onClose} aria-label={t('dialog.confirm.close')}>
            <X size={14} />
          </button>
        </div>
        <div className="app-update-body">{renderBody()}</div>
        <div className="app-update-footer">{renderActions()}</div>
      </div>
    </div>
  )
}
