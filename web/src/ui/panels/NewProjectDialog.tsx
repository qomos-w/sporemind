import { useState, useCallback, useEffect } from 'react'
import { FolderOpen } from 'lucide-react'
import { client } from '../../application/generated-client'
import { loadPreference, savePreference } from '../../application/theme-persist'
import * as workspace from '../../gen-clients/workspace/client'
import type { ProjectRef } from '../../gen-types/workspace'
import { FilePicker } from '../components/FilePicker'
import { Modal } from '../components/Modal'
import { useI18n } from '../../i18n'
import './NewProjectDialog.css'

const PARENT_PATH_PREF_KEY = 'ui.newProject.parentPath.v1'
const parentPathPrefVersion = { v: 0 }

function persistParentPath(path: string): void {
  void savePreference(PARENT_PATH_PREF_KEY, path, 'npd-parent-path', parentPathPrefVersion).catch(() => {})
}

type Mode = 'create' | 'open'

interface NewProjectDialogProps {
  open: boolean
  onCreated: (ref: ProjectRef) => void | Promise<void>
  onCancel: () => void
  initialMode?: Mode
}

function basenameOf(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, '')
  if (!trimmed) return ''
  const seg = trimmed.split(/[\\/]/).pop() ?? ''
  return seg
}

export function NewProjectDialog({ open, onCreated, onCancel, initialMode = 'create' }: NewProjectDialogProps) {
  const [mode, setMode] = useState<Mode>(initialMode)
  const [parentPath, setParentPath] = useState('')
  const [openPath, setOpenPath] = useState('')
  const [name, setName] = useState('')
  const [initGit, setInitGit] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [folderPickerOpen, setFolderPickerOpen] = useState(false)
  const { t } = useI18n()

  useEffect(() => {
    let cancelled = false
    loadPreference(PARENT_PATH_PREF_KEY)
      .then(raw => {
        const p = raw?.trim()
        if (!cancelled && p) setParentPath(prev => prev || p)
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [])

  const switchMode = useCallback((next: Mode) => {
    setMode(next)
    setError('')
  }, [])

  const handleBrowse = useCallback(() => {
    setFolderPickerOpen(true)
  }, [])

  const handleFolderSelect = useCallback((paths: string[]) => {
    if (!paths[0]) {
      setFolderPickerOpen(false)
      return
    }
    const p = paths[0]
    if (mode === 'open') {
      setOpenPath(p)
      setName(basenameOf(p))
    } else {
      setParentPath(p)
      persistParentPath(p)
    }
    setFolderPickerOpen(false)
  }, [mode])

  const handleSubmit = async () => {
    setSubmitting(true)
    setError('')
    try {
      if (mode === 'create') {
        if (!name.trim()) {
          setSubmitting(false)
          return
        }
        const ref = await workspace.create(client, {
          Path: parentPath.trim(),
          Name: name.trim(),
          InitGit: initGit,
        })
        persistParentPath(parentPath.trim())
        await onCreated(ref)
      } else {
        if (!openPath.trim()) {
          setSubmitting(false)
          return
        }
        const ref = await workspace.mount(client, {
          Path: openPath.trim(),
          Name: name.trim() || basenameOf(openPath.trim()),
        })
        await onCreated(ref)
      }
    } catch (e: any) {
      setError(e?.message ?? (mode === 'create' ? t('dialog.newProject.errorCreate') : t('dialog.newProject.errorOpen')))
    } finally {
      setSubmitting(false)
    }
  }

  const nameValid = /^[^\\/:*?"<>|]+$/.test(name.trim()) && name.trim() !== '.' && name.trim() !== '..'
  const trimmedPath = (mode === 'create' ? parentPath : openPath).trim()
  const trimmedName = name.trim()
  const canSubmit = mode === 'create'
    ? (!!trimmedName && !!trimmedPath && nameValid && !submitting)
    : (!!trimmedPath && (!trimmedName || nameValid) && !submitting)

  const titleLabel = mode === 'create' ? t('dialog.newProject.title.create') : t('dialog.newProject.title.open')
  const submitLabel = submitting
    ? (mode === 'create' ? t('dialog.newProject.submit.creating') : t('dialog.newProject.submit.opening'))
    : (mode === 'create' ? t('dialog.newProject.submit.create') : t('dialog.newProject.submit.open'))

  const tabs = (
    <div className="modal-tabs" role="tablist">
      <button
        type="button"
        role="tab"
        aria-selected={mode === 'create'}
        className={`modal-tab ${mode === 'create' ? 'modal-tab--active' : ''}`}
        onClick={() => switchMode('create')}
      >
        {t('dialog.newProject.tab.create')}
      </button>
      <button
        type="button"
        role="tab"
        aria-selected={mode === 'open'}
        className={`modal-tab ${mode === 'open' ? 'modal-tab--active' : ''}`}
        onClick={() => switchMode('open')}
      >
        {t('dialog.newProject.tab.open')}
      </button>
    </div>
  )

  const footer = (
    <>
      <button className="modal-action modal-action--secondary" onClick={onCancel} disabled={submitting}>
        {t('common.cancel')}
      </button>
      <button
        className="modal-action modal-action--primary"
        onClick={handleSubmit}
        disabled={!canSubmit}
      >
        {submitLabel}
      </button>
    </>
  )

  return (
    <>
      <Modal
        open={open}
        title={<span className="npd-title">{titleLabel}</span>}
        onClose={onCancel}
        headerExtra={tabs}
        footer={footer}
        disableClose={submitting}
        closeOnOverlayClick
        className="npd-dialog"
      >
        <label className="npd-field">
            <span className="npd-label">
              {mode === 'create' ? t('dialog.newProject.pathLabel.create') : t('dialog.newProject.pathLabel.open')}
            </span>
            <div className="npd-path-row">
              <input
                className="npd-input npd-path-input"
                value={mode === 'create' ? parentPath : openPath}
                onChange={e => {
                  if (mode === 'create') {
                    setParentPath(e.target.value)
                  } else {
                    setOpenPath(e.target.value)
                    setName('')
                  }
                }}
                placeholder={mode === 'create' ? t('dialog.newProject.pathPlaceholder.create') : t('dialog.newProject.pathPlaceholder.open')}
              />
              <button className="npd-browse-btn" onClick={handleBrowse} title={t('dialog.newProject.browse')}>
                <FolderOpen size={16} />
              </button>
            </div>
          </label>

        <label className="npd-field">
          <span className="npd-label">
            {mode === 'create' ? t('dialog.newProject.nameLabel.create') : t('dialog.newProject.nameLabel.open')}
          </span>
          <input
            className="npd-input"
            value={name}
            onChange={e => setName(e.target.value)}
            placeholder={mode === 'create' ? t('dialog.newProject.namePlaceholder.create') : t('dialog.newProject.namePlaceholder.open')}
            onKeyDown={e => { if (e.key === 'Enter' && canSubmit) handleSubmit() }}
          />
          {name && !nameValid && (
            <span className="npd-hint npd-hint-error">{t('dialog.newProject.invalidName')}</span>
          )}
        </label>

        {mode === 'create' && (
          <label className="npd-checkbox-field">
            <input
              type="checkbox"
              checked={initGit}
              onChange={e => setInitGit(e.target.checked)}
            />
            <span>{t('dialog.newProject.gitSetup')}</span>
          </label>
        )}

        {error && <div className="npd-error">{error}</div>}
      </Modal>

      <FilePicker
        open={folderPickerOpen}
        mode="folder"
        onSelect={handleFolderSelect}
        onCancel={() => setFolderPickerOpen(false)}
      />
    </>
  )
}
