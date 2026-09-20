import React, { useEffect, useRef } from 'react'
import { AlertTriangle, Loader2, X } from 'lucide-react'
import { useI18n } from '../../i18n'
import { useBrowserOverlay } from '../ai/browserOverlay'
import './ConfirmDialog.css'

export interface ConfirmDialogProps {
  open: boolean
  title: string
  description: string
  confirmLabel?: string
  cancelLabel?: string
  danger?: boolean
  loading?: boolean
  confirmOnEnter?: boolean
  overlayClassName?: string
  onConfirm: () => void
  onCancel: () => void
}

export const ConfirmDialog: React.FC<ConfirmDialogProps> = ({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel,
  danger = false,
  loading = false,
  confirmOnEnter = true,
  overlayClassName,
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
      if (event.key === 'Enter' && confirmOnEnter) {
        event.preventDefault()
        onConfirm()
      }
    }
    document.addEventListener('keydown', handleKey)
    // Auto-focus confirm button on open
    if (confirmOnEnter) setTimeout(() => confirmBtnRef.current?.focus(), 50)
    return () => document.removeEventListener('keydown', handleKey)
  }, [open, onConfirm, onCancel, confirmOnEnter])

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

  if (!open) return null

  return (
    <div className={`confirm-dialog-overlay${overlayClassName ? ` ${overlayClassName}` : ''}`}>
      <div ref={dialogRef} className="confirm-dialog" role="alertdialog" aria-modal="true">
        <div className="confirm-dialog-header">
          <div className={`confirm-dialog-icon${danger ? ' danger' : ''}`}>
            <AlertTriangle size={20} />
          </div>
          <button type="button" className="confirm-dialog-close" onClick={onCancel} aria-label={t('dialog.confirm.close')}>
            <X size={14} />
          </button>
        </div>
        <div className="confirm-dialog-body">
          <h3 className="confirm-dialog-title">{title}</h3>
 <p className="confirm-dialog-desc">{description}</p>
        </div>
        <div className="confirm-dialog-footer">
          <button type="button" className="confirm-dialog-btn cancel" onClick={onCancel} disabled={loading}>
            {cancelLabel ?? t('common.cancel')}
          </button>
          <button
            ref={confirmBtnRef}
            type="button"
            className={`confirm-dialog-btn confirm${danger ? ' danger' : ''}`}
            onClick={onConfirm}
            disabled={loading}
          >
            {loading ? (
              <>
                <Loader2 size={14} className="confirm-dialog-spinner" />
                {confirmLabel ?? t('common.confirm')}
              </>
            ) : (
              confirmLabel ?? t('common.confirm')
            )}
          </button>
        </div>
      </div>
    </div>
  )
}
