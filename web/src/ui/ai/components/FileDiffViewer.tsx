import React, { useState, useEffect, useCallback, useRef } from 'react'
import { File, FileText, FileCode, FileImage, FileJson, FileTerminal, FileSpreadsheet, FileAudio, FileVideo, FileArchive, FileCog } from 'lucide-react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { DiffBlock } from './parts/DiffBlock.tsx'
import { materializeHtml, assetIOFor } from './parts/local-html-embed.ts'
import { CodeMirrorViewer } from './CodeMirrorViewer.tsx'
import { HexViewer } from '../../editor/HexViewer'
import { client } from '../../../application/generated-client'
import * as filesystem from '../../../gen-clients/filesystem/client'
import * as projectClient from '../../../gen-clients/project/client'
import { useI18n } from '../../../i18n'
import './FileDiffViewer.css'

type ViewMode = 'source' | 'diff' | 'preview' | 'hex'

interface FileDiffViewerProps {
  filePath: string
  projectId?: string
  diffContent?: string
  sourceContent?: string
  defaultMode?: 'source' | 'diff'
  initialLine?: number
  initialLineEnd?: number
  /** 0-based UTF-16 column within initialLine (global find jump support). */
  initialColumn?: number
  initialColumnEnd?: number
  onCursorChange?: (pos: number, selFrom: number, selTo: number) => void
  onViewModeChange?: (mode: ViewMode) => void
  restoreCursorPos?: number | null
  restoreSelectionFrom?: number | null
  restoreSelectionTo?: number | null
  cursorRestoreKey?: number
  projectRootPath?: string
  onGotoDefinition?: (filePath: string, line: number) => void
  /** Absolute root paths of open workspace projects, for workspace-scope references. */
  workspaceProjectRoots?: string[]
}

function isMarkdownFile(path: string): boolean {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  return ext === 'md' || ext === 'markdown' || ext === 'mdx'
}

function isSvgFile(path: string): boolean {
  return path.split('.').pop()?.toLowerCase() === 'svg'
}

function isHtmlFile(path: string): boolean {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  return ext === 'html' || ext === 'htm'
}

function normalizePath(path: string): string {
  return path.replace(/\\/g, '/').replace(/^\/+/, '').toLowerCase()
}

const CODE_EXTS = new Set([
  'js', 'jsx', 'mjs', 'cjs', 'ts', 'tsx', 'go', 'rs', 'java', 'cpp', 'cc', 'cxx', 'c', 'h', 'hpp',
  'css', 'scss', 'sass', 'less', 'sql', 'xml', 'html', 'htm', 'php', 'rb', 'swift', 'kt', 'lua',
  'r', 'dart', 'scala', 'groovy', 'clj', 'cljs', 'ex', 'exs', 'elm', 'hs', 'erl', 'fs', 'fsx',
  'cs', 'vb', 'pas', 'lisp', 'scm', 'ss', 'jl', 'cr', 'nim', 'v', 'zig', 'd', 'ada', 'asm', 's',
  'pl', 'pm', 't', 'proto', 'graphql', 'prisma', 'tf', 'hcl', 'nix', 'purs', 'lhs', 'idr', 'agda',
  'lean', 'tex', 'bib',
])
const SCRIPT_EXTS = new Set(['sh', 'bash', 'zsh', 'ps1', 'bat', 'cmd'])
const CONFIG_EXTS = new Set(['yml', 'yaml', 'toml', 'ini', 'cfg', 'env', 'conf', 'config'])
const TEXT_EXTS = new Set(['md', 'markdown', 'txt', 'log'])
const IMAGE_EXTS = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'ico'])
const VIDEO_EXTS = new Set(['mp4', 'avi', 'mov', 'mkv', 'webm', 'flv'])
const AUDIO_EXTS = new Set(['mp3', 'wav', 'ogg', 'flac', 'aac', 'm4a'])
const ARCHIVE_EXTS = new Set(['zip', 'tar', 'gz', 'tgz', 'bz2', 'rar', '7z'])
const SPREADSHEET_EXTS = new Set(['csv', 'xlsx', 'xls'])

function FileTypeIcon({ path, size = 14 }: { path: string; size?: number }) {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  if (CODE_EXTS.has(ext)) return <FileCode size={size} />
  if (SCRIPT_EXTS.has(ext)) return <FileTerminal size={size} />
  if (CONFIG_EXTS.has(ext)) return <FileCog size={size} />
  if (TEXT_EXTS.has(ext)) return <FileText size={size} />
  if (IMAGE_EXTS.has(ext)) return <FileImage size={size} />
  if (VIDEO_EXTS.has(ext)) return <FileVideo size={size} />
  if (AUDIO_EXTS.has(ext)) return <FileAudio size={size} />
  if (ARCHIVE_EXTS.has(ext)) return <FileArchive size={size} />
  if (SPREADSHEET_EXTS.has(ext)) return <FileSpreadsheet size={size} />
  if (ext === 'json') return <FileJson size={size} />
  return <File size={size} />
}

export const FileDiffViewer: React.FC<FileDiffViewerProps> = ({
  filePath,
  projectId,
  diffContent,
  sourceContent: initialSource,
  defaultMode,
  initialLine,
  initialLineEnd,
  initialColumn,
  initialColumnEnd,
  onCursorChange,
  onViewModeChange,
  restoreCursorPos,
  restoreSelectionFrom,
  restoreSelectionTo,
  cursorRestoreKey,
  projectRootPath,
  onGotoDefinition,
  workspaceProjectRoots,
}) => {
  const { t } = useI18n()
  const hasDiff = !!diffContent
  const markdown = isMarkdownFile(filePath)
  const svg = isSvgFile(filePath)
  const html = isHtmlFile(filePath)

  const [viewMode, setViewModeState] = useState<ViewMode>(
    defaultMode ?? (hasDiff ? 'diff' : 'source')
  )
  const onViewModeChangeRef = useRef(onViewModeChange)
  onViewModeChangeRef.current = onViewModeChange
  const setViewMode = useCallback((mode: ViewMode) => {
    setViewModeState(mode)
    onViewModeChangeRef.current?.(mode)
  }, [])
  useEffect(() => {
    setViewModeState(defaultMode ?? (hasDiff ? 'diff' : 'source'))
  }, [defaultMode, hasDiff])
  const [source, setSource] = useState<string | undefined>(initialSource)
  const [dirty, setDirty] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [isBinary, setIsBinary] = useState(false)
  const dirtyRef = useRef(false)
  dirtyRef.current = dirty
  // Timestamp of the last save we initiated ourselves. fsnotify echoes our
  // own write back as a file_changed event; within this grace window we treat
  // such events as self-echo and skip the redundant reload.
  const lastSelfSaveAtRef = useRef(0)
  const SELF_SAVE_ECHO_MS = 1500
  const loadGenRef = useRef(0)

  const loadSource = useCallback(async (force = false) => {
    if (!force && source !== undefined) return
    const gen = ++loadGenRef.current
    setLoading(true)
    setError(null)
    try {
      // Single round-trip: backend already detects binary files (NUL byte
      // check in handleRead) and rejects them with a "binary file" error,
      // which we catch below and route to the hex view.
      const resp = projectId
        ? await projectClient.read(client, { Path: filePath }, { target: projectId })
        : await filesystem.read(client, { Path: filePath })
      if (gen !== loadGenRef.current) return
      setSource(resp.Content)
      setDirty(false)
      setIsBinary(false)
    } catch (e) {
      if (gen !== loadGenRef.current) return
      const msg = e instanceof Error ? e.message : String(e)
      // Backend rejects binary files with a NUL-byte content check. Switch to
      // the hex view rather than showing a dead-end error.
      if (/binary file/i.test(msg)) {
        setIsBinary(true)
        setViewModeState('hex')
      } else {
        setError(msg || t('fileDiff.readError'))
      }
    } finally {
      if (gen === loadGenRef.current) {
        setLoading(false)
      }
    }
  }, [filePath, source, t, projectId])

  useEffect(() => {
    if (!projectId) return
    return projectClient.OnFileChanged(client, projectId, (event) => {
      if (normalizePath(event.Path) !== normalizePath(filePath)) return
      if (dirtyRef.current) return
      // fsnotify echoes our own save back as a file_changed event. Skip the
      // redundant reload within the grace window after a self-save.
      if (Date.now() - lastSelfSaveAtRef.current < SELF_SAVE_ECHO_MS) return
      setSource(undefined)
      void loadSource(true)
    })
  }, [projectId, filePath, loadSource])

  // Reset per-file state and cancel any in-flight load when the path changes.
  // The initial mount keeps initialSource as-is; only subsequent path swaps
  // clear it so the new file's content is fetched.
  const isFirstPathRef = useRef(true)
  useEffect(() => {
    if (isFirstPathRef.current) {
      isFirstPathRef.current = false
      return
    }
    loadGenRef.current++
    setSource(undefined)
    setIsBinary(false)
    setError(null)
    setLoading(false)
    setViewModeState(defaultMode ?? (hasDiff ? 'diff' : 'source'))
  }, [filePath]) // eslint-disable-line react-hooks/exhaustive-deps

  // The backend watcher only watches the .sporecode subtree plus on-demand
  // files; request a watch for this file while it is open so external edits
  // still surface. Paired add/remove in cleanup — the backend refcounts
  // parent-directory watches shared between viewers.
  useEffect(() => {
    if (!projectId) return
    void projectClient.watchFile(client, { Path: filePath }, { target: projectId }).catch(() => {})
    return () => {
      void projectClient.watchFile(client, { Path: filePath, Remove: true }, { target: projectId }).catch(() => {})
    }
  }, [projectId, filePath])

  useEffect(() => {
    if ((viewMode === 'source' || viewMode === 'preview') && source === undefined) {
      loadSource()
    }
  }, [viewMode, source, loadSource])

  // Auto-save refs. saveSourceRef always holds the latest saveSource so the
  // stable scheduleAutoSave callback never closes over a stale one.
  const saveSourceRef = useRef<() => void>(() => {})
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const scheduleAutoSave = useCallback(() => {
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current)
    saveTimerRef.current = setTimeout(() => {
      saveTimerRef.current = null
      void saveSourceRef.current()
    }, 1000)
  }, [])

  const handleChange = useCallback((value: string) => {
    setSource(value)
    setDirty(true)
    scheduleAutoSave()
  }, [scheduleAutoSave])

  const saveSource = useCallback(async () => {
    // Cancel any pending debounce so Ctrl+S and the timer never double-fire.
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
    // Nothing to write when there are no unsaved edits (keeps load-time and
    // post-save no-op calls from hitting the filesystem).
    if (source === undefined || !dirtyRef.current) return
    setSaving(true)
    setSaveError(null)
    try {
      if (projectId) {
        // project.write overwrites existing files and resolves against the
        // project root (filesystem.write refuses existing files and resolves
        // against the primary mount only).
        await projectClient.write(client, { Path: filePath, Content: source }, { target: projectId })
      } else {
        await filesystem.write(client, { Path: filePath, Content: source })
      }
      lastSelfSaveAtRef.current = Date.now()
      setDirty(false)
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : t('fileDiff.saveError'))
    } finally {
      setSaving(false)
    }
  }, [filePath, source, t, projectId])
  saveSourceRef.current = saveSource

  // Flush pending edits when the viewer unmounts (tab close / switch) so
  // auto-save never silently drops changes.
  useEffect(() => {
    return () => {
      const timer = saveTimerRef.current
      if (timer) {
        clearTimeout(timer)
        saveTimerRef.current = null
      }
      if (dirtyRef.current) {
        void saveSourceRef.current()
      }
    }
  }, [])

  const showSource = viewMode === 'source'
  const showPreview = viewMode === 'preview'

  // HTML preview: rewrite relative subresources (css/js/img) to data: URLs and
  // run page scripts. srcDoc has no base URL, so without materialization every
  // relative reference 404s; allow-scripts keeps the iframe opaque-origin so
  // scripts execute without gaining app-origin access.
  const [previewHtml, setPreviewHtml] = useState('')
  useEffect(() => {
    if (!showPreview || !html || source === undefined) return
    let cancelled = false
    const norm = filePath.replace(/\\/g, '/')
    const idx = norm.lastIndexOf('/')
    materializeHtml(source, idx < 0 ? '' : norm.slice(0, idx), assetIOFor(projectId))
      .then(res => { if (!cancelled) setPreviewHtml(res.html) })
      .catch(() => { if (!cancelled) setPreviewHtml(source) })
    return () => { cancelled = true }
  }, [showPreview, html, source, filePath, projectId])

  return (
    <div className="fdv-container">
      <div className="fdv-toolbar">
        <div className="fdv-file-info">
          <FileTypeIcon path={filePath} size={14} />
          <span className="fdv-file-path" title={filePath}>
            {filePath}
          </span>
        </div>
        <div className="fdv-toolbar-right">
        <div className="fdv-mode-switch">
          <button
            type="button"
            className={showSource ? 'active' : ''}
            disabled={(!filePath && !loading) || isBinary}
            onClick={() => setViewMode('source')}
          >
            {t('fileDiff.sourceFile')}
          </button>
          <button
            type="button"
            className={viewMode === 'diff' ? 'active' : ''}
            disabled={!hasDiff}
            onClick={() => setViewMode('diff')}
          >
            Diff
          </button>
          <button
            type="button"
            className={showPreview ? 'active' : ''}
            disabled={!markdown && !svg && !html}
            onClick={() => setViewMode('preview')}
          >
            Preview
          </button>
          <button
            type="button"
            className={viewMode === 'hex' ? 'active' : ''}
            disabled={!filePath}
            onClick={() => setViewMode('hex')}
            title={t('fileDiff.hex')}
          >
            Hex
          </button>
        </div>
        {showSource ? (
          <div className="fdv-actions">
            {saveError ? (
              <span className="fdv-save-error" title={saveError} aria-label="save error">!</span>
            ) : saving ? (
              <span className="fdv-status-saving">{t('fileDiff.saving')}</span>
            ) : dirty ? (
              <span className="fdv-dirty-indicator" aria-label="unsaved changes" />
            ) : null}
          </div>
        ) : null}
        </div>
      </div>
      <div className="fdv-body">
        {viewMode === 'hex' ? (
          <HexViewer filePath={filePath} projectId={projectId} />
        ) : loading ? (
          <div className="fdv-loading">{t('common.loading')}...</div>
        ) : error ? (
          <div className="fdv-error">{error}</div>
        ) : showSource ? (
          source !== undefined ? (
            <CodeMirrorViewer
              content={source}
              filePath={filePath}
              initialLine={initialLine}
              initialLineEnd={initialLineEnd}
              initialColumn={initialColumn}
              initialColumnEnd={initialColumnEnd}
              editable
              onChange={handleChange}
              onSave={saveSource}
              onCursorChange={onCursorChange}
              restoreCursorPos={restoreCursorPos}
              restoreSelectionFrom={restoreSelectionFrom}
              restoreSelectionTo={restoreSelectionTo}
              cursorRestoreKey={cursorRestoreKey}
              projectRootPath={projectRootPath}
              onGotoDefinition={onGotoDefinition}
              workspaceProjectRoots={workspaceProjectRoots}
            />
          ) : (
            <div className="fdv-empty">{t('fileDiff.originalMissing')}</div>
          )
        ) : showPreview ? (
          source !== undefined ? (
            html ? (
              <iframe className="fdv-html-preview" sandbox="allow-scripts" srcDoc={previewHtml} title={filePath} />
            ) : (
            <div className="fdv-preview">
              {svg ? (
                <div className="fdv-svg-preview">
                  <img src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(source)}`} alt={filePath} />
                </div>
              ) : (
                <div className="fdv-preview-content">
                  <Markdown remarkPlugins={[remarkGfm]}>{source}</Markdown>
                </div>
              )}
            </div>
            )
          ) : (
            <div className="fdv-empty">{t('fileDiff.originalMissing')}</div>
          )
        ) : diffContent ? (
          <DiffBlock diffContent={diffContent} filePath={filePath} />
        ) : (
          <div className="fdv-empty">{t('fileDiff.noDiffContent')}</div>
        )}
      </div>
    </div>
  )
}
