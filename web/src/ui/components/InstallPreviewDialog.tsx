import React, { useEffect, useRef } from 'react'
import { Package, AlertTriangle, X, Loader2 } from 'lucide-react'
import { useI18n } from '../../i18n'
import { useBrowserOverlay } from '../ai/browserOverlay'
import { getCapabilityInfo } from '../../application/capability-catalog'
import type { AppManifest } from '../../gen-types/app'
import './InstallPreviewDialog.css'

export interface InstallPreviewDialogProps {
  open: boolean
  manifest: AppManifest | null
  loading?: boolean
  error?: string | null
  onConfirm: () => void
  onCancel: () => void
}

const RISK_ORDER: Record<string, number> = {
  high: 0,
  medium: 1,
  low: 2,
  unknown: 3,
}

function riskClass(risk: string): string {
  switch (risk.toLowerCase()) {
    case 'high':
      return 'risk-high'
    case 'medium':
      return 'risk-medium'
    default:
      return 'risk-low'
  }
}

interface PermissionRow {
  id: string
  titleKey: string
  descriptionKey: string
  risk: string
  known: boolean
}

function buildPermissionRows(permissions: string[] | undefined): PermissionRow[] {
  const rows = (permissions ?? []).map((id): PermissionRow => {
    const info = getCapabilityInfo(id)
    if (info) {
      return {
        id,
        titleKey: info.titleKey,
        descriptionKey: info.descriptionKey,
        risk: info.riskLevel,
        known: true,
      }
    }
    return {
      id,
      titleKey: '',
      descriptionKey: '',
      risk: 'unknown',
      known: false,
    }
  })
  return [...rows].sort((a, b) => {
    const orderA = RISK_ORDER[a.risk] ?? 3
    const orderB = RISK_ORDER[b.risk] ?? 3
    if (orderA !== orderB) return orderA - orderB
    return a.id.localeCompare(b.id)
  })
}

export const InstallPreviewDialog: React.FC<InstallPreviewDialogProps> = ({
  open,
  manifest,
  loading = false,
  error,
  onConfirm,
  onCancel,
}) => {
  const { t } = useI18n()
  useBrowserOverlay(open)
  const dialogRef = useRef<HTMLDivElement>(null)
  const confirmBtnRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onCancel()
      }
      if (event.key === 'Enter') {
        event.preventDefault()
        onConfirm()
      }
    }
    document.addEventListener('keydown', handleKey)
    setTimeout(() => confirmBtnRef.current?.focus(), 50)
    return () => document.removeEventListener('keydown', handleKey)
  }, [open, onConfirm, onCancel])

  useEffect(() => {
    if (!open) return
    const handlePointerDown = (event: MouseEvent) => {
      if (dialogRef.current && !dialogRef.current.contains(event.target as Node)) {
        onCancel()
      }
    }
    document.addEventListener('mousedown', handlePointerDown)
    return () => document.removeEventListener('mousedown', handlePointerDown)
  }, [open, onCancel])

  if (!open || !manifest) return null

  const permissions = buildPermissionRows(manifest.Permissions)

  return (
    <div className="install-preview-overlay">
      <div ref={dialogRef} className="install-preview-dialog" role="alertdialog" aria-modal="true">
        <div className="install-preview-header">
          <div className="install-preview-icon">
            <Package size={20} />
          </div>
          <button
            type="button"
            className="install-preview-close"
            onClick={onCancel}
            aria-label={t('dialog.confirm.close')}
          >
            <X size={14} />
          </button>
        </div>
        <div className="install-preview-body">
          <h3 className="install-preview-title">{t('installPreview.title')}</h3>

          <div className="install-preview-meta">
            <div className="install-preview-meta-row">
              <span className="install-preview-meta-label">{t('installPreview.appId')}</span>
              <span className="install-preview-meta-value">{manifest.Id}</span>
            </div>
            <div className="install-preview-meta-row">
              <span className="install-preview-meta-label">{t('installPreview.name')}</span>
              <span className="install-preview-meta-value">{manifest.Name || '—'}</span>
            </div>
            <div className="install-preview-meta-row">
              <span className="install-preview-meta-label">{t('installPreview.version')}</span>
              <span className="install-preview-meta-value">{manifest.Version || '—'}</span>
            </div>
            <div className="install-preview-meta-row">
              <span className="install-preview-meta-label">{t('installPreview.runtime')}</span>
              <span className="install-preview-meta-value">{manifest.Runtime || '—'}</span>
            </div>
          </div>

          <h4 className="install-preview-section-title">{t('installPreview.permissions')}</h4>
          {permissions.length === 0 ? (
            <p className="install-preview-empty">{t('installPreview.noPermissions')}</p>
          ) : (
            <ul className="install-preview-list">
              {permissions.map(row => (
                <li key={row.id} className="install-preview-item">
                  <div className="install-preview-item-header">
                    <span className="install-preview-item-title">
                      {row.known ? t(row.titleKey as any) : row.id}
                    </span>
                    <span className={`install-preview-risk ${riskClass(row.risk)}`}>
                      {row.risk === 'high' && <AlertTriangle size={12} />}
                      {t(`capability.risk.${row.risk}` as any)}
                    </span>
                  </div>
                  {row.known ? (
                    <p className="install-preview-item-desc">{t(row.descriptionKey as any)}</p>
                  ) : (
                    <p className="install-preview-item-desc">{t('installPreview.unknownCapability')}</p>
                  )}
                </li>
              ))}
            </ul>
          )}

          {error && <p className="install-preview-error">{error}</p>}
        </div>
        <div className="install-preview-footer">
          <button
            type="button"
            className="install-preview-btn cancel"
            onClick={onCancel}
            disabled={loading}
          >
            {t('common.cancel')}
          </button>
          <button
            ref={confirmBtnRef}
            type="button"
            className="install-preview-btn confirm"
            onClick={onConfirm}
            disabled={loading}
          >
            {loading ? (
              <>
                <Loader2 size={14} className="install-preview-spinner" />
                {t('installPreview.installing')}
              </>
            ) : (
              t('installPreview.install')
            )}
          </button>
        </div>
      </div>
    </div>
  )
}
