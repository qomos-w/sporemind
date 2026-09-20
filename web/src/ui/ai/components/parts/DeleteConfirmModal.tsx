import React, { useEffect, useState } from 'react'
import { AlertTriangle, Brain, Trash2, X } from 'lucide-react'
import { useI18n } from '../../../../i18n'
import './DeleteConfirmModal.css'

interface DeleteConfirmModalProps {
  open: boolean
  agentName: string
  title?: string
  message?: string
  // When true, the agent has memory mounted: show a memory-loss warning and
  // require the user to type the agent name before the delete button enables.
  // This is the "double confirmation" for destructive deletion of memory.
  requireTypeName?: boolean
  // When true, the agent has a worktree bound: show a worktree-loss warning
  // (the worktree will be permanently deleted along with the agent).
  hasWorktree?: boolean
  onClose: () => void
  onConfirm: () => void
}

export const DeleteConfirmModal: React.FC<DeleteConfirmModalProps> = ({
  open,
  agentName,
  title,
  message,
  requireTypeName,
  hasWorktree,
  onClose,
  onConfirm,
}) => {
  const { t } = useI18n()
  const [typed, setTyped] = useState('')

  // Reset the type-to-confirm field every time the dialog (re)opens or the
  // target agent changes, so a stale match from a previous session never
  // pre-enables the delete button.
  useEffect(() => {
    if (open) setTyped('')
  }, [open, agentName])

  if (!open) return null

  const nameMatches = typed.trim().toLowerCase() === agentName.trim().toLowerCase()

  return (
    <div className="delete-confirm-backdrop" onClick={onClose}>
      <div className="delete-confirm-dialog" onClick={e => e.stopPropagation()}>
        <div className="delete-confirm-header">
          <div className="delete-confirm-header-left">
            <AlertTriangle size={14} />
            <span>{title ?? t('dialog.deleteConfirm.title')}</span>
          </div>
          <button className="delete-confirm-close-btn" type="button" onClick={onClose} title={t('dialog.confirm.close')}>
            <X size={14} />
          </button>
        </div>
        <div className="delete-confirm-body">
          {message ?? t('dialog.deleteConfirm.message', { name: agentName })}
        </div>
        {requireTypeName ? (
          <div className="delete-confirm-memory-warning">
            <Brain size={14} />
            <span>{t('dialog.deleteConfirm.memoryWarning')}</span>
          </div>
        ) : null}
        {hasWorktree ? (
          <div className="delete-confirm-memory-warning">
            <AlertTriangle size={14} />
            <span>{t('dialog.deleteConfirm.worktreeWarning')}</span>
          </div>
        ) : null}
        {requireTypeName ? (
          <div className="delete-confirm-type-confirm">
            <label className="delete-confirm-type-label">
              {t('dialog.deleteConfirm.typeToConfirm')}
            </label>
            <input
              className="delete-confirm-type-input"
              type="text"
              value={typed}
              autoFocus
              autoComplete="off"
              spellCheck={false}
              onChange={e => setTyped(e.target.value)}
              onKeyDown={e => {
                if (e.key === 'Enter' && nameMatches) {
                  e.preventDefault()
                  onConfirm()
                }
              }}
            />
          </div>
        ) : null}
        <div className="delete-confirm-actions">
          <button className="delete-confirm-cancel-btn" type="button" onClick={onClose}>
            {t('common.cancel')}
          </button>
          <button
            className="delete-confirm-delete-btn"
            type="button"
            onClick={onConfirm}
            disabled={requireTypeName ? !nameMatches : false}
          >
            <Trash2 size={12} />
            {t('common.delete')}
          </button>
        </div>
      </div>
    </div>
  )
}
