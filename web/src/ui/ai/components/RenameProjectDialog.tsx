import React, { useEffect, useRef, useState } from 'react'
import { useI18n } from '../../../i18n'
import { Modal } from '../../components/Modal'

interface RenameProjectDialogProps {
  open: boolean
  currentName: string
  onClose: () => void
  onSubmit: (name: string) => void
}

export const RenameProjectDialog: React.FC<RenameProjectDialogProps> = ({
  open,
  currentName,
  onClose,
  onSubmit,
}) => {
  const [name, setName] = useState(currentName)
  const inputRef = useRef<HTMLInputElement>(null)
  const { t } = useI18n()

  useEffect(() => {
    if (open) {
      setName(currentName)
      setTimeout(() => inputRef.current?.focus(), 0)
    }
  }, [open, currentName])

  const handleSubmit = (event: React.FormEvent) => {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed && trimmed !== currentName) {
      onSubmit(trimmed)
    }
    onClose()
  }

  return (
    <Modal open={open} title={t('dialog.renameProject.title')} onClose={onClose} size="sm">
      <form onSubmit={handleSubmit} className="rename-project-dialog-form">
        <input
          ref={inputRef}
          type="text"
          className="rename-project-dialog-input"
          value={name}
          onChange={e => setName(e.target.value)}
          placeholder={t('dialog.renameProject.placeholder')}
        />
        <div className="rename-project-dialog-actions">
          <button type="button" className="rename-project-dialog-btn secondary" onClick={onClose}>
            {t('common.cancel')}
          </button>
          <button type="submit" className="rename-project-dialog-btn primary">
            {t('common.save')}
          </button>
        </div>
      </form>
    </Modal>
  )
}
