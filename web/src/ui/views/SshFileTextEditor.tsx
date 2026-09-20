import { useCallback, useEffect, useRef, useState } from 'react'
import type { GosporeClient } from '@qomos/gospore-client'
import { AlertCircle, FileCode, Loader2, Save } from 'lucide-react'
import * as sshmanagerFile from '../../gen-clients/sshmanager/client'
import { useI18n } from '../../i18n'
import { Modal } from '../components/Modal'
import './SshFileTextEditor.css'

export interface SshFileTextEditorProps {
  client: GosporeClient
  sessionId: string
  /** Remote file path shown in the title and sent to file_read/file_write. */
  path: string
  /** Remote file size in bytes, displayed in the title. */
  size: number
  /** Called when the modal is dismissed (after unsaved-changes confirmation). */
  onClose: () => void
  /** Called after a successful save so the parent can refresh its file list. */
  onSaved: () => void
}

/**
 * Pure dirty check: the editor has unsaved changes when the current content
 * differs from the content loaded from the server. A null original (still
 * loading or load failed) is never dirty.
 */
export function isContentDirty(original: string | null, current: string): boolean {
  return original !== null && original !== current
}

function formatError(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e)
}

function formatBytes(n: number): string {
  if (n === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(n) / Math.log(k))
  return parseFloat((n / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

/**
 * Self-contained SSH remote text file editor modal: loads the file on open,
 * lets the user edit it in a monospace textarea, and saves it back via
 * sshmanagerFile.fileWrite. Tracks unsaved changes and confirms before
 * closing; guards against binary files (fallback for callers that skipped
 * the pre-check).
 */
export function SshFileTextEditor({ client, sessionId, path, size, onClose, onSaved }: SshFileTextEditorProps) {
  const { t } = useI18n()
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const [originalContent, setOriginalContent] = useState<string | null>(null)
  const [content, setContent] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [isBinary, setIsBinary] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [toast, setToast] = useState<string | null>(null)

  const dirty = isContentDirty(originalContent, content)

  // Load file content when opened (or when the target path changes).
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setLoadError(null)
    setIsBinary(false)
    setOriginalContent(null)
    setContent('')
    setSaveError(null)
    sshmanagerFile
      .fileRead(client, { SessionId: sessionId, Path: path })
      .then(resp => {
        if (cancelled) return
        if (resp.IsBinary) {
          setIsBinary(true)
        } else {
          setOriginalContent(resp.Content)
          setContent(resp.Content)
        }
      })
      .catch(err => {
        if (cancelled) return
        setLoadError(formatError(err))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [client, sessionId, path])

  // Focus the editor once content is ready.
  useEffect(() => {
    if (!loading && !loadError && !isBinary) textareaRef.current?.focus()
  }, [loading, loadError, isBinary])

  const showToast = useCallback((msg: string) => {
    setToast(msg)
    if (toastTimer.current) clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToast(null), 2500)
  }, [])

  useEffect(() => {
    return () => {
      if (toastTimer.current) clearTimeout(toastTimer.current)
    }
  }, [])

  // Warn before the browser/window is closed with unsaved changes.
  useEffect(() => {
    if (!dirty) return
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault()
    }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [dirty])

  const handleSave = useCallback(async () => {
    if (!dirty || saving) return
    setSaving(true)
    setSaveError(null)
    try {
      await sshmanagerFile.fileWrite(client, { SessionId: sessionId, Path: path, Content: content })
      setOriginalContent(content)
      showToast(t('sshDialog.fileSaved'))
      onSaved()
    } catch (err) {
      setSaveError(formatError(err))
    } finally {
      setSaving(false)
    }
  }, [client, content, dirty, onSaved, path, saving, sessionId, showToast, t])

  const handleClose = useCallback(() => {
    if (saving) return
    if (dirty && !window.confirm(t('sshDialog.unsavedConfirm'))) return
    onClose()
  }, [dirty, onClose, saving, t])

  // Save shortcut: Ctrl/Cmd + S anywhere inside the modal.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
        e.preventDefault()
        void handleSave()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [handleSave])

  const editable = !loading && !loadError && !isBinary

  return (
    <Modal
      open
      title={
        <div className="ssh-file-editor-title">
          <FileCode size={16} className="ssh-file-editor-title-icon" />
          <span className="ssh-file-editor-title-text">{t('sshDialog.editorTitle')}</span>
          <span className="ssh-file-editor-path" title={path}>{path}</span>
          <span className="ssh-file-editor-size">({formatBytes(size)})</span>
        </div>
      }
      onClose={handleClose}
      size="lg"
      closeOnEscape
      closeOnOverlayClick={false}
      disableClose={saving}
      className="ssh-file-editor-modal"
      footer={
        editable ? (
          <>
            <span className="ssh-file-editor-dirty" role="status">
              {dirty ? t('sshDialog.unsavedChanges') : ''}
            </span>
            <button
              type="button"
              className="modal-action modal-action--secondary"
              onClick={handleClose}
              disabled={saving}
            >
              {t('sshDialog.button.cancel')}
            </button>
            <button
              type="button"
              className="modal-action modal-action--primary"
              onClick={handleSave}
              disabled={!dirty || saving}
            >
              {saving ? <Loader2 size={14} className="ssh-file-editor-spin" /> : <Save size={14} />}
              {t('sshDialog.save')}
            </button>
          </>
        ) : undefined
      }
    >
      <div className="ssh-file-editor-body">
        {loading && (
          <div className="ssh-file-editor-center" role="status">
            <Loader2 size={24} className="ssh-file-editor-spin" />
          </div>
        )}
        {loadError && (
          <div className="ssh-file-editor-state ssh-file-editor-state--error" role="alert">
            <AlertCircle size={20} />
            <span>{t('sshDialog.loadFailed')}: {loadError}</span>
            <button type="button" className="modal-action modal-action--secondary" onClick={onClose}>
              {t('common.close')}
            </button>
          </div>
        )}
        {isBinary && (
          <div className="ssh-file-editor-state" role="alert">
            <AlertCircle size={20} />
            <span>{t('sshDialog.binaryFile')}</span>
            <button type="button" className="modal-action modal-action--secondary" onClick={onClose}>
              {t('common.close')}
            </button>
          </div>
        )}
        {editable && (
          <>
            <textarea
              ref={textareaRef}
              className="ssh-file-editor-textarea"
              value={content}
              onChange={e => setContent(e.target.value)}
              spellCheck={false}
              aria-label={`${t('sshDialog.editorTitle')} ${path}`}
            />
            {saveError && (
              <div className="ssh-file-editor-save-error" role="alert">
                <AlertCircle size={14} />
                <span>{t('sshDialog.saveFailed')}: {saveError}</span>
              </div>
            )}
          </>
        )}
      </div>
      {toast && (
        <div className="ssh-file-editor-toast" role="status">
          {toast}
        </div>
      )}
    </Modal>
  )
}