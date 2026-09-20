import { useCallback, useEffect, useRef, useState } from 'react'
import { FileCode, Loader2, RefreshCw, Save } from 'lucide-react'
import { CodeMirrorViewer } from './CodeMirrorViewer.tsx'
import { client } from '../../../application/generated-client'
import * as sshmanagerFile from '../../../gen-clients/sshmanager/client'
import { useI18n } from '../../../i18n'
import './FileDiffViewer.css'
import './SshRemoteFileEditor.css'

interface SshRemoteFileEditorProps {
  sessionId: string
  /** Absolute path of the file on the remote host. */
  path: string
}

/**
 * Right-panel editor for a small text file on a connected SSH host. Loads via
 * sshmanager.file_read, edits in CodeMirror, and writes back with
 * sshmanager.file_write — toolbar save button or Ctrl+S.
 */
export function SshRemoteFileEditor({ sessionId, path }: SshRemoteFileEditorProps) {
  const { t } = useI18n()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [binary, setBinary] = useState(false)
  const [content, setContent] = useState('')
  const [savedContent, setSavedContent] = useState('')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [savedFlash, setSavedFlash] = useState(false)
  const flashTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const [refreshTick, setRefreshTick] = useState(0)
  const dirty = content !== savedContent
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    setBinary(false)
    sshmanagerFile
      .fileRead(client, { SessionId: sessionId, Path: path })
      .then(resp => {
        if (cancelled) return
        if (resp.IsBinary) {
          setBinary(true)
          return
        }
        setContent(resp.Content)
        setSavedContent(resp.Content)
      })
      .catch(err => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [sessionId, path, refreshTick])

  const refresh = useCallback(() => {
    setRefreshTick(n => n + 1)
  }, [])

  useEffect(() => () => {
    if (flashTimer.current) clearTimeout(flashTimer.current)
  }, [])

  const save = useCallback(async () => {
    if (!dirtyRef.current || saving) return
    setSaving(true)
    setSaveError(null)
    try {
      await sshmanagerFile.fileWrite(client, { SessionId: sessionId, Path: path, Content: content })
      setSavedContent(content)
      setSavedFlash(true)
      if (flashTimer.current) clearTimeout(flashTimer.current)
      flashTimer.current = setTimeout(() => setSavedFlash(false), 1500)
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [content, path, saving, sessionId])

  const editable = !loading && !error && !binary

  return (
    <div className="fdv-container">
      <div className="fdv-toolbar">
        <div className="fdv-file-info">
          <FileCode size={14} />
          <span className="fdv-file-path" title={path}>{path}</span>
          <button
            type="button"
            className="srfe-refresh"
            onClick={refresh}
            disabled={loading}
            title={t('sshDialog.reloadFile')}
          >
            {loading ? <Loader2 size={13} className="srfe-spin" /> : <RefreshCw size={13} />}
          </button>
        </div>
        <div className="fdv-toolbar-right">
          <div className="fdv-actions">
            {saveError ? (
              <span className="fdv-save-error" title={saveError} role="alert">!</span>
            ) : saving ? (
              <span className="fdv-status-saving">{t('fileDiff.saving')}</span>
            ) : savedFlash ? (
              <span className="fdv-status-saving">{t('sshDialog.fileSaved')}</span>
            ) : dirty ? (
              <span className="fdv-dirty-indicator" aria-label="unsaved changes" />
            ) : null}
          </div>
          <button
            type="button"
            className="srfe-save"
            onClick={save}
            disabled={!dirty || saving || !editable}
            title="Ctrl+S"
          >
            {saving ? <Loader2 size={13} className="srfe-spin" /> : <Save size={13} />}
            {t('sshDialog.save')}
          </button>
        </div>
      </div>
      <div className="fdv-body">
        {loading ? (
          <div className="fdv-loading">{t('common.loading')}...</div>
        ) : error ? (
          <div className="fdv-error">{t('sshDialog.loadFailed')}: {error}</div>
        ) : binary ? (
          <div className="fdv-error">{t('sshDialog.binaryFile')}</div>
        ) : (
          <CodeMirrorViewer
            content={content}
            filePath={path}
            editable
            onChange={setContent}
            onSave={save}
          />
        )}
      </div>
    </div>
  )
}
