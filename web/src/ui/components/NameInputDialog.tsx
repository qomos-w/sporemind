import React, { useEffect, useRef } from 'react'
import { Loader2, X } from 'lucide-react'
import { useI18n } from '../../i18n'
import './NameInputDialog.css'

export interface NameInputDialogProps {
  open: boolean
  title: string
  placeholder?: string
  value: string
  onChange: (value: string) => void
  onConfirm: () => void
  onCancel: () => void
  confirmLabel?: string
  cancelLabel?: string
  loading?: boolean
}

export const NameInputDialog: React.FC<NameInputDialogProps> = ({
  open,
  title,
  placeholder,
  value,
  onChange,
  onConfirm,
  onCancel,
  confirmLabel,
  cancelLabel,
  loading = false,
}) => {
  const { t } = useI18n()
  const dialogRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
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
    const timer = window.setTimeout(() => {
      inputRef.current?.focus()
    }, 50)
    return () => {
      document.removeEventListener('keydown', handleKey)
      window.clearTimeout(timer)
    }
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

  if (!open) return null

  return (
    <div className="name-input-dialog-overlay">
      <div ref={dialogRef} className="name-input-dialog" role="dialog" aria-modal="true">
        <div className="name-input-dialog-header">
          <h3 className="name-input-dialog-title">{title}</h3>
          <button type="button" className="name-input-dialog-close" onClick={onCancel} aria-label={t('dialog.confirm.close')}>
            <X size={14} />
          </button>
        </div>
        <div className="name-input-dialog-body">
          <input
            ref={inputRef}
            type="text"
            className="name-input-dialog-input"
            value={value}
            placeholder={placeholder}
            onChange={(e) => onChange(e.target.value)}
            disabled={loading}
          />
        </div>
        <div className="name-input-dialog-footer">
          <button type="button" className="name-input-dialog-btn cancel" onClick={onCancel} disabled={loading}>
            {cancelLabel ?? t('common.cancel')}
          </button>
          <button
            ref={confirmBtnRef}
            type="button"
            className="name-input-dialog-btn confirm"
            onClick={onConfirm}
            disabled={loading || value.trim() === ''}
          >
            {loading ? (
              <>
                <Loader2 size={14} className="name-input-dialog-spinner" />
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
