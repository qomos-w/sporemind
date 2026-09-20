import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ChevronRight, ChevronDown,
  Cloud, Folder, FolderOpen, File, RefreshCw, Upload, Download, Trash2, FolderPlus, FilePlus,
  X, AlertTriangle, Server, HardDrive, Loader2,
} from 'lucide-react'
import { client } from '../../application/generated-client'
import * as dbclientApi from '../../gen-clients/dbclient/client'
import type { DbObjectEntry, DbObjectListResp, DbObjectReadResp } from '../../gen-types/dbclient'
import { useI18n } from '../../i18n'
import { useBrowserOverlay } from '../ai/browserOverlay'
import { useMenuDismiss } from '../ai/hooks/useMenuDismiss'
import { useMarqueeSelection, type MarqueeSelectionResult } from '../ai/hooks/useMarqueeSelection'
import { collectOsDroppedEntries, type OsDroppedEntry } from '../os-file-dnd'
import { bytesToBase64 } from '../os-file-dnd'
import './ObjectStorageSessionView.css'

/**
 * Object-storage session view (oss | webdav). A right-panel tab registered
 * inside the database-session framework, with its own tree, file list, and
 * transfer panel.
 *
 * Data source: dbclient.object_* callables (added in 第二阶段 oss/webdav
 * 对象浏览会话视图). Reads flow through the existing `persist` backend the
 * profile was dialed against, so storage behaviour (tunnel, credentials,
 * TLS) is shared with the rest of the persist subsystem.
 *
 * Path conventions (see schemas/dbclient._5920.spore):
 *   oss:    ""           -> bucket list
 *           "<bucket>"   -> objects/prefixes under the bucket (dirs collapsed)
 *           "<bucket>/<key>" -> file object
 *   webdav: ""           -> root
 *           "<dir>"      -> dir children
 *           "<dir>/<file>" -> file
 */

export interface ObjectStorageSessionViewProps {
  profileId: string
  profileName: string
  /** Backend discriminator (oss | webdav). Used for icon + empty-state copy. */
  backend: 'oss' | 'webdav' | string
  /** Optional starting path; defaults to "" (bucket/root). */
  initialPath?: string
  /** Whether this tab is the active, visible right-panel tab. */
  isActive?: boolean
}

/** A transfer record shown in the floating transfer panel. */
interface ObjectTransfer {
  id: number
  name: string
  direction: 'download' | 'upload'
  status: 'running' | 'done' | 'error' | 'cancelled'
  size?: number
  error?: string
  /** Optional reference to the in-flight read/write for cancellation. */
  cancel?: () => void
}

interface TreeNodeState {
  /** Loaded children; undefined = not yet fetched. */
  children?: DbObjectEntry[]
  loading: boolean
  error?: string
  cursor?: string
  hasMore?: boolean
}

interface DialogState {
  kind: 'newfile' | 'newfolder'
  parentPath: string
}
interface ConfirmState {
  kind: 'delete'
  entries: DbObjectEntry[]
}

function formatError(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e)
}

function joinPath(parent: string, child: string): string {
  if (!parent) return child
  if (parent.endsWith('/')) return parent + child
  return parent + '/' + child
}

function basename(path: string): string {
  if (!path) return ''
  const i = path.lastIndexOf('/')
  return i >= 0 ? path.slice(i + 1) : path
}

function parentPath(path: string): string {
  if (!path) return ''
  const i = path.lastIndexOf('/')
  return i > 0 ? path.slice(0, i) : ''
}

function formatBytes(n: number | undefined): string {
  if (n === undefined || n < 0) return '-'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function formatModified(s: string | undefined): string {
  if (!s) return '-'
  try {
    const d = new Date(s)
    if (isNaN(d.getTime())) return s
    return d.toLocaleString()
  } catch {
    return s
  }
}

const BackendIcon = ({ backend }: { backend: string }) =>
  backend === 'oss' ? <Server size={14} /> : <HardDrive size={14} />

export function ObjectStorageSessionView({
  profileId: profileIdProp,
  profileName,
  backend,
  initialPath = '',
  isActive = true,
}: ObjectStorageSessionViewProps) {
  const { t } = useI18n()

  // Mirrors of props for use inside async callbacks that should always see
  // the latest value (same pattern as DbSessionView).
  const profileIdRef = useRef(profileIdProp)
  profileIdRef.current = profileIdProp
  const isActiveRef = useRef(isActive)
  isActiveRef.current = isActive

  // Current working directory in the right pane. Initialised from initialPath
  // (so external callers — e.g. an editor tab for a specific key — can deep
  // link to a starting location).
  const [currentPath, setCurrentPath] = useState(initialPath)
  // Tree cache keyed by path; root is the empty string.
  const [tree, setTree] = useState<Record<string, TreeNodeState>>({ root: { loading: false } })
  // Flat listing for the current path (right pane). Recomputed on navigate
  // so the listing is independent of the tree cache.
  const [entries, setEntries] = useState<DbObjectEntry[]>([])
  const [loadingList, setLoadingList] = useState(false)
  const [listError, setListError] = useState<string | null>(null)
  // Selection
  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const [selectedEntries, setSelectedEntries] = useState<Set<string>>(new Set())
  const [lastSelectedPath, setLastSelectedPath] = useState<string | null>(null)
  const [expandedPaths, setExpandedPaths] = useState<Set<string>>(new Set())

  // Context menu / dialog state
  const [menuTarget, setMenuTarget] = useState<{ entry: DbObjectEntry; x: number; y: number } | null>(null)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<ConfirmState | null>(null)
  const [dialogName, setDialogName] = useState('')
  const [dialogErr, setDialogErr] = useState<string | null>(null)
  const [toast, setToast] = useState<string | null>(null)

  // Connection health (display only — dbclient dials lazily).
  const [connState, setConnState] = useState<'idle' | 'testing' | 'ok' | 'err'>('idle')
  const [connDetail, setConnDetail] = useState<{ latencyMs?: number; error?: string }>({})

  // Transfer queue
  const [transfers, setTransfers] = useState<ObjectTransfer[]>([])
  const transferIdRef = useRef(0)
  // In-flight cancel tokens for the current download/upload call. Holding
  // them on a ref lets the transfer row's × button cancel the underlying
  // promise without leaking a listener back into React state.
  const transferCancelRef = useRef<Record<number, () => void>>({})

  // References for the marquee selection hook.
  const mainContentRef = useRef<HTMLDivElement>(null)
  const filesListRef = useRef<HTMLDivElement>(null)

  useBrowserOverlay(menuTarget !== null || confirmDelete !== null || dialog !== null)
  useMenuDismiss(mainContentRef, () => setMenuTarget(null), menuTarget ? 'object-menu' : null)
  useMenuDismiss(mainContentRef, () => setDialog(null), dialog ? 'object-dialog' : null)
  useMenuDismiss(mainContentRef, () => setConfirmDelete(null), confirmDelete ? 'object-confirm' : null)

  const showToast = useCallback((msg: string) => {
    setToast(msg)
    const timer = setTimeout(() => setToast(null), 2500)
    return () => clearTimeout(timer)
  }, [])

  // Reset state when the profile changes.
  useEffect(() => {
    setCurrentPath(initialPath)
    setTree({ root: { loading: false } })
    setEntries([])
    setSelectedPath(null)
    setSelectedEntries(new Set())
    setLastSelectedPath(null)
    setExpandedPaths(new Set())
    setListError(null)
    setConnState('idle')
    setConnDetail({})
    setMenuTarget(null)
    setDialog(null)
    setConfirmDelete(null)
    setTransfers([])
  }, [profileIdProp, initialPath])

  /* ─── list current directory ───────────────────────────────────────── */

  const loadList = useCallback(async (path: string, silent = false) => {
    if (!silent) {
      setLoadingList(entries.length === 0)
      setListError(null)
    }
    try {
      const resp = await dbclientApi.objectList(client, { ProfileId: profileIdRef.current, Path: path, Limit: 1000 })
      setEntries(resp.Entries ?? [])
    } catch (e) {
      setListError(formatError(e))
    } finally {
      setLoadingList(false)
    }
  }, [entries.length])

  useEffect(() => {
    if (!isActive) return
    void loadList(currentPath)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentPath, isActive, profileIdProp])

  /* ─── tree loading ─────────────────────────────────────────────────── */

  const loadTreeDir = useCallback(async (path: string) => {
    setTree(prev => ({ ...prev, [path]: { ...(prev[path] ?? { loading: true }), loading: true, error: undefined } }))
    try {
      const resp: DbObjectListResp = await dbclientApi.objectList(client, { ProfileId: profileIdRef.current, Path: path, Limit: 1000 })
      setTree(prev => ({
        ...prev,
        [path]: {
          children: resp.Entries ?? [],
          loading: false,
          cursor: resp.Cursor,
          hasMore: resp.HasMore,
        },
      }))
    } catch (e) {
      setTree(prev => ({ ...prev, [path]: { ...(prev[path] ?? { loading: false }), loading: false, error: formatError(e) } }))
    }
  }, [])

  // Eagerly load the root when the tab becomes visible, so the user sees the
  // top-level structure without an extra click.
  useEffect(() => {
    if (!isActive) return
    const root = tree.root
    if (root && !root.children && !root.loading && !root.error) {
      void loadTreeDir('')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isActive, profileIdProp])

  /* ─── navigation ───────────────────────────────────────────────────── */

  const handleTreeClick = useCallback(async (entry: DbObjectEntry) => {
    if (!entry.IsDir) return
    setSelectedPath(entry.Path)
    setSelectedEntries(new Set())
    setLastSelectedPath(null)
    setExpandedPaths(prev => {
      const next = new Set(prev)
      if (next.has(entry.Path)) next.delete(entry.Path)
      else next.add(entry.Path)
      return next
    })
    if (!tree[entry.Path]?.children) {
      void loadTreeDir(entry.Path)
    }
    setCurrentPath(entry.Path)
  }, [tree, loadTreeDir])

  const handleFileClick = useCallback((entry: DbObjectEntry) => {
    if (entry.IsDir) {
      setCurrentPath(entry.Path)
      setSelectedEntries(new Set())
      setLastSelectedPath(null)
      setSelectedPath(entry.Path)
      return
    }
    // Plain click on a file: set selection anchor (used for shift-range).
    setSelectedPath(entry.Path)
    setLastSelectedPath(entry.Path)
    setSelectedEntries(prev => {
      const next = new Set(prev)
      next.delete(entry.Path)
      return next
    })
  }, [])

  const handleFileCtrlClick = useCallback((entry: DbObjectEntry) => {
    setLastSelectedPath(entry.Path)
    setSelectedEntries(prev => {
      const next = new Set(prev)
      if (next.has(entry.Path)) next.delete(entry.Path)
      else next.add(entry.Path)
      return next
    })
  }, [])

  const handleFileShiftClick = useCallback((entry: DbObjectEntry) => {
    if (lastSelectedPath === null) {
      setSelectedPath(entry.Path)
      setLastSelectedPath(entry.Path)
      return
    }
    const startIdx = entries.findIndex(e => e.Path === lastSelectedPath)
    const endIdx = entries.findIndex(e => e.Path === entry.Path)
    if (startIdx === -1 || endIdx === -1) return
    const from = Math.min(startIdx, endIdx)
    const to = Math.max(startIdx, endIdx)
    const range = new Set<string>()
    for (let i = from; i <= to; i++) range.add(entries[i]!.Path)
    setSelectedEntries(range)
    setLastSelectedPath(entry.Path)
  }, [entries, lastSelectedPath])

  /* ─── marquee selection ────────────────────────────────────────────── */

  useMarqueeSelection({
    containerRef: filesListRef,
    itemSelector: '.oss-item-hit',
    getSelectedPaths: () => new Set(entries.filter(e => selectedEntries.has(e.Path)).map(e => e.Path)),
    onPreview: (r: MarqueeSelectionResult) => {
      if (!r.next) return
      const next = new Set(r.next)
      setSelectedEntries(next)
    },
    onHit: (r: MarqueeSelectionResult) => {
      if (r.isClick && r.mode === 'replace') {
        setSelectedEntries(new Set())
        setSelectedPath(null)
      }
    },
  })

  /* ─── connection test (best-effort probe via dial_test) ────────────── */

  const handleTestConnection = useCallback(async () => {
    setConnState('testing')
    setConnDetail({})
    try {
      const resp = await dbclientApi.dialTest(client, { ProfileId: profileIdRef.current })
      if (resp.Ok) {
        setConnState('ok')
        setConnDetail({ latencyMs: resp.LatencyMs })
      } else {
        setConnState('err')
        setConnDetail({ error: resp.Error ?? 'failed' })
      }
    } catch (e) {
      setConnState('err')
      setConnDetail({ error: formatError(e) })
    }
  }, [])

  /* ─── transfer helpers ─────────────────────────────────────────────── */

  const updateTransfer = useCallback((id: number, patch: Partial<ObjectTransfer>) => {
    setTransfers(prev => prev.map(tr => (tr.id === id ? { ...tr, ...patch } : tr)))
  }, [])

  const dismissTransfer = useCallback((id: number) => {
    setTransfers(prev => prev.filter(tr => tr.id !== id))
    delete transferCancelRef.current[id]
  }, [])

  // Generic transfer runner. fn receives a cancellation hook and returns the
  // transferred byte size (or undefined when the size is unknown, e.g.
  // webdav upload via base64 — we read the byte length off the request body
  // in that case). The cancel hook is registered on a ref so the × button
  // can abort even while fn is mid-await.
  const runTransfer = useCallback(async (
    name: string,
    direction: ObjectTransfer['direction'],
    fn: (signal: { aborted: boolean }) => Promise<number | undefined>,
  ): Promise<boolean> => {
    const id = ++transferIdRef.current
    const abort = { aborted: false }
    transferCancelRef.current[id] = () => { abort.aborted = true }
    setTransfers(prev => [...prev, { id, name, direction, status: 'running', cancel: () => { abort.aborted = true } }])
    try {
      const size = await fn(abort)
      if (abort.aborted) {
        updateTransfer(id, { status: 'cancelled' })
        setTimeout(() => dismissTransfer(id), 3000)
        return false
      }
      updateTransfer(id, { status: 'done', size })
      setTimeout(() => dismissTransfer(id), 3000)
      return true
    } catch (e) {
      if (abort.aborted) {
        updateTransfer(id, { status: 'cancelled' })
        setTimeout(() => dismissTransfer(id), 3000)
        return false
      }
      const msg = formatError(e)
      updateTransfer(id, { status: 'error', error: msg })
      setTimeout(() => dismissTransfer(id), 6000)
      return false
    }
  }, [updateTransfer, dismissTransfer])

  const cancelTransfer = useCallback((id: number) => {
    const hook = transferCancelRef.current[id]
    if (hook) hook()
  }, [])

  /* ─── file ops: download / upload / delete / mkdir ─────────────────── */

  // Download a file by calling dbclient.object_read. Bytes are base64 in the
  // wire payload; we decode and surface a Blob URL for the <a download>.
  const handleDownload = useCallback(async (entry: DbObjectEntry) => {
    const ok = await runTransfer(entry.Name, 'download', async () => {
      const resp = await dbclientApi.objectRead(client, { ProfileId: profileIdRef.current, Path: entry.Path })
      const blob = base64ToBlob(resp.Content, resp.ContentType ?? guessMime(entry.Name))
      triggerBrowserDownload(blob, entry.Name)
      return resp.Size
    })
    if (!ok) return
    showToast(t('objectStorage.toast.downloadStarted', { name: entry.Name }))
  }, [runTransfer, showToast, t])

  // Upload one OS-dropped entry via object_write. Reads the file as bytes,
  // base64-encodes inline, and writes via the actor callable.
  const handleUploadEntry = useCallback(async (entry: OsDroppedEntry, parent: string) => {
    const filePath = joinPath(parent, entry.name)
    const ok = await runTransfer(entry.name, 'upload', async () => {
      const bytes = await entry.readBytes()
      const b64 = bytesToBase64(bytes)
      await dbclientApi.objectWrite(client, {
        ProfileId: profileIdRef.current,
        Path: filePath,
        Content: b64,
        ContentType: guessMime(entry.name),
      })
      return bytes.byteLength
    })
    if (ok) {
      showToast(t('objectStorage.toast.uploaded', { name: entry.name }))
      void loadList(currentPath)
    }
  }, [runTransfer, showToast, t, currentPath, loadList])

  // Resolve OS drops anywhere in the right pane — uploads land in currentPath.
  const handleOsDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault()
    const dropped = await collectOsDroppedEntries(e.dataTransfer)
    if (dropped.length === 0) return
    for (const d of dropped) {
      void handleUploadEntry(d, currentPath)
    }
  }, [currentPath, handleUploadEntry])

  const handleCreate = useCallback(async () => {
    if (!dialog) return
    const name = dialogName.trim()
    if (!name) {
      setDialogErr(t('objectStorage.dialog.nameRequired'))
      return
    }
    setDialogErr(null)
    try {
      await dbclientApi.objectMkdir(client, {
        ProfileId: profileIdRef.current,
        ParentPath: dialog.parentPath,
        Name: name,
      })
      setDialog(null)
      setDialogName('')
      showToast(t('objectStorage.toast.created', { name }))
      // If we created in the current dir, refresh; otherwise refresh the parent.
      const refreshPath = dialog.parentPath === currentPath ? currentPath : dialog.parentPath
      void loadList(refreshPath)
      if (dialog.parentPath !== currentPath) {
        void loadTreeDir(dialog.parentPath)
      }
    } catch (e) {
      setDialogErr(formatError(e))
    }
  }, [dialog, dialogName, t, currentPath, loadList, loadTreeDir, showToast])

  const handleDelete = useCallback(async (entriesToDelete: DbObjectEntry[]) => {
    for (const e of entriesToDelete) {
      try {
        await dbclientApi.objectDelete(client, { ProfileId: profileIdRef.current, Path: e.Path })
      } catch (err) {
        showToast(t('objectStorage.toast.deleteFailed', { name: e.Name, error: formatError(err) }))
      }
    }
    void loadList(currentPath)
    void loadTreeDir(currentPath)
    setSelectedEntries(new Set())
  }, [currentPath, loadList, loadTreeDir, showToast, t])

  /* ─── context-menu actions ─────────────────────────────────────────── */

  const openContextMenu = useCallback((e: React.MouseEvent, entry: DbObjectEntry) => {
    e.preventDefault()
    e.stopPropagation()
    setMenuTarget({ entry, x: e.clientX, y: e.clientY })
    // If the right-clicked entry is not in the selection, snap selection to it.
    setSelectedEntries(prev => {
      if (prev.has(entry.Path)) return prev
      return new Set([entry.Path])
    })
    setSelectedPath(entry.Path)
  }, [])

  /* ─── rendered tree (recursive) ────────────────────────────────────── */

  const renderTreeNode = useCallback((node: DbObjectEntry, depth: number): React.ReactNode => {
    const state = tree[node.Path]
    const expanded = expandedPaths.has(node.Path)
    const isActive = currentPath === node.Path
    return (
      <div key={node.Path} className="oss-tree-node">
        <div
          className={`oss-tree-row ${state?.loading ? 'oss-tree-row-loading' : ''} ${isActive ? 'oss-tree-row-active' : ''}`}
          style={{ paddingLeft: 6 + depth * 12 }}
          onClick={() => handleTreeClick(node)}
          onContextMenu={(e) => openContextMenu(e, node)}
        >
          <span className="oss-tree-toggle">
            {expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          </span>
          <span className="oss-tree-icon">
            {expanded ? <FolderOpen size={13} /> : <Folder size={13} />}
          </span>
          <span className="oss-tree-label" title={node.Path}>{node.Name}</span>
        </div>
        {state?.loading && <div className="oss-tree-loading" style={{ paddingLeft: 6 + (depth + 1) * 12 }}>…</div>}
        {state?.error && <div className="oss-tree-error" style={{ paddingLeft: 6 + (depth + 1) * 12 }}>{state.error}</div>}
        {expanded && state?.children && (
          <div className="oss-tree-children">
            {state.children.filter(c => c.IsDir).map(child => renderTreeNode(child, depth + 1))}
          </div>
        )}
      </div>
    )
  }, [tree, expandedPaths, currentPath, handleTreeClick, openContextMenu])

  /* ─── list of selected entries for the context menu ────────────────── */

  const contextEntries = useMemo<DbObjectEntry[]>(() => {
    if (!menuTarget) return []
    const ids = selectedEntries.has(menuTarget.entry.Path) ? selectedEntries : new Set([menuTarget.entry.Path])
    return entries.filter(e => ids.has(e.Path))
  }, [menuTarget, selectedEntries, entries])

  /* ─── context menu and dialog rendering ────────────────────────────── */

  return (
    <div className="object-storage-session" onDragOver={(e) => { e.preventDefault() }} onDrop={handleOsDrop}>
      <div className="object-storage-header">
        <div className="object-storage-header-left">
          <Cloud size={14} />
          <span className="object-storage-header-name" title={profileName}>{profileName}</span>
          <span className="object-storage-header-backend">{backend}</span>
          <span className={`object-storage-header-status object-status-${connState}`}>
            {connState === 'ok' && (connDetail.latencyMs !== undefined ? `OK · ${connDetail.latencyMs}ms` : 'OK')}
            {connState === 'err' && t('db.status.error')}
            {connState === 'testing' && t('db.status.testing')}
            {connState === 'idle' && t('db.status.idle')}
          </span>
          {connState === 'err' && connDetail.error && (
            <span className="object-storage-header-error" title={connDetail.error}>
              <AlertTriangle size={11} /> {connDetail.error}
            </span>
          )}
        </div>
        <div className="object-storage-header-right">
          <button type="button" className="object-storage-header-btn" onClick={() => void handleTestConnection()} title={t('db.actions.test')}>
            <RefreshCw size={12} />
          </button>
          <button type="button" className="object-storage-header-btn" onClick={() => { setDialog({ kind: 'newfolder', parentPath: currentPath }); setDialogName(''); setDialogErr(null) }} title={t('objectStorage.actions.newFolder')}>
            <FolderPlus size={12} />
          </button>
          <button type="button" className="object-storage-header-btn" onClick={() => setMenuTarget(null)} title={t('objectStorage.actions.uploadHint')}>
            <label className="object-storage-upload-label" title={t('objectStorage.actions.upload')}>
              <Upload size={12} />
              <span>{t('objectStorage.actions.upload')}</span>
              <input
                type="file"
                multiple
                style={{ display: 'none' }}
                onChange={async (e) => {
                  const files = Array.from(e.target.files ?? [])
                  for (const f of files) {
                    const buf = new Uint8Array(await f.arrayBuffer())
                    await handleUploadEntry({
                      name: f.name,
                      relPath: f.name,
                      isDir: false,
                      size: f.size,
                      readBytes: async () => buf,
                    }, currentPath)
                  }
                  e.target.value = ''
                }}
              />
            </label>
          </button>
        </div>
      </div>

      <div className="object-storage-body">
        <div className="object-storage-tree">
          <div className="object-storage-tree-title">
            <BackendIcon backend={backend} /> {t('objectStorage.tree.title')}
          </div>
          <div className="object-storage-tree-scroll">
            {tree.root?.loading && !tree.root.children && (
              <div className="object-storage-tree-loading"><Loader2 size={12} className="spin" /> {t('common.loading')}</div>
            )}
            {tree.root?.error && (
              <div className="object-storage-tree-error">{tree.root.error}</div>
            )}
            {tree.root?.children && tree.root.children.length === 0 && (
              <div className="object-storage-tree-empty">{t('objectStorage.tree.empty')}</div>
            )}
            {tree.root?.children?.map(n => renderTreeNode(n, 0))}
          </div>
        </div>

        <div className="object-storage-right" ref={mainContentRef}>
          <div className="object-storage-pathbar">
            <span className="object-storage-pathbar-label" title={currentPath || '/'}>{currentPath || '/'}</span>
          </div>
          <div className="object-storage-list" ref={filesListRef}>
            <div className="object-storage-list-header">
              <span className="col-name">{t('objectStorage.col.name')}</span>
              <span className="col-size">{t('objectStorage.col.size')}</span>
              <span className="col-modified">{t('objectStorage.col.modified')}</span>
            </div>
            {loadingList && entries.length === 0 && (
              <div className="object-storage-list-loading"><Loader2 size={12} className="spin" /> {t('common.loading')}</div>
            )}
            {listError && <div className="object-storage-list-error"><AlertTriangle size={12} /> {listError}</div>}
            {!loadingList && !listError && entries.length === 0 && (
              <div className="object-storage-list-empty">{t('objectStorage.list.empty')}</div>
            )}
            <div className="object-storage-list-rows">
              {entries.map(entry => {
                const sel = selectedEntries.has(entry.Path) || selectedPath === entry.Path
                return (
                  <div
                    key={entry.Path}
                    className={`object-storage-row oss-item-hit ${sel ? 'object-storage-row-selected' : ''} ${entry.IsDir ? 'object-storage-row-dir' : ''}`}
                    onClick={(e) => {
                      if (e.shiftKey) handleFileShiftClick(entry)
                      else if (e.ctrlKey || e.metaKey) handleFileCtrlClick(entry)
                      else handleFileClick(entry)
                    }}
                    onDoubleClick={() => entry.IsDir && setCurrentPath(entry.Path)}
                    onContextMenu={(e) => openContextMenu(e, entry)}
                  >
                    <span className="col-name">
                      <span className="object-storage-row-icon">
                        {entry.IsDir ? <Folder size={13} /> : <File size={13} />}
                      </span>
                      <span className="object-storage-row-name" title={entry.Path}>{entry.Name}</span>
                    </span>
                    <span className="col-size">{entry.IsDir ? '-' : formatBytes(entry.Size)}</span>
                    <span className="col-modified">{formatModified(entry.Modified)}</span>
                  </div>
                )
              })}
            </div>
          </div>
        </div>
      </div>

      {/* transfer panel */}
      {transfers.length > 0 && (
        <div className="object-storage-transfers">
          <div className="object-storage-transfers-title">{t('objectStorage.transfers.title')}</div>
          <div className="object-storage-transfers-list">
            {transfers.map(tr => (
              <div key={tr.id} className={`object-storage-transfer object-status-${tr.status}`}>
                <span className="object-storage-transfer-dir">
                  {tr.direction === 'download' ? <Download size={11} /> : <Upload size={11} />}
                </span>
                <span className="object-storage-transfer-name" title={tr.name}>{tr.name}</span>
                <span className="object-storage-transfer-status">
                  {tr.status === 'running' && t('objectStorage.transfers.running')}
                  {tr.status === 'done' && (tr.size !== undefined ? formatBytes(tr.size) : t('objectStorage.transfers.done'))}
                  {tr.status === 'error' && (tr.error ? tr.error.slice(0, 60) : t('objectStorage.transfers.error'))}
                  {tr.status === 'cancelled' && t('objectStorage.transfers.cancelled')}
                </span>
                <button
                  type="button"
                  className="object-storage-transfer-x"
                  onClick={() => tr.status === 'running' ? cancelTransfer(tr.id) : dismissTransfer(tr.id)}
                  title={tr.status === 'running' ? t('objectStorage.transfers.cancel') : t('common.close')}
                >
                  <X size={11} />
                </button>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* context menu */}
      {menuTarget && (
        <div className="object-storage-menu" style={{ left: menuTarget.x, top: menuTarget.y }} onClick={(e) => e.stopPropagation()}>
          {contextEntries.length === 1 && !contextEntries[0]!.IsDir && (
            <button type="button" className="object-storage-menu-item" onClick={() => { handleDownload(contextEntries[0]!); setMenuTarget(null) }}>
              <Download size={12} /> {t('objectStorage.menu.download')}
            </button>
          )}
          {contextEntries.length > 0 && (
            <button type="button" className="object-storage-menu-item" onClick={() => { setConfirmDelete({ kind: 'delete', entries: contextEntries }); setMenuTarget(null) }}>
              <Trash2 size={12} /> {t('objectStorage.menu.delete')}
            </button>
          )}
        </div>
      )}

      {/* dialog (new folder/file) */}
      {dialog && (
        <div className="object-storage-dialog-backdrop" onClick={() => setDialog(null)}>
          <div className="object-storage-dialog" onClick={(e) => e.stopPropagation()}>
            <div className="object-storage-dialog-title">
              {dialog.kind === 'newfolder' ? <FolderPlus size={14} /> : <FilePlus size={14} />}
              {dialog.kind === 'newfolder' ? t('objectStorage.dialog.newFolder') : t('objectStorage.dialog.newFile')}
            </div>
            <div className="object-storage-dialog-body">
              <label className="object-storage-dialog-label">{t('objectStorage.dialog.name')}</label>
              <input
                type="text"
                value={dialogName}
                onChange={(e) => setDialogName(e.target.value)}
                autoFocus
                onKeyDown={(e) => { if (e.key === 'Enter') void handleCreate() }}
                placeholder={dialog.kind === 'newfolder' ? t('objectStorage.dialog.placeholderFolder') : t('objectStorage.dialog.placeholderFile')}
              />
              <div className="object-storage-dialog-path">{t('objectStorage.dialog.parent')}: {dialog.parentPath || '/'}</div>
              {dialogErr && <div className="object-storage-dialog-err">{dialogErr}</div>}
            </div>
            <div className="object-storage-dialog-actions">
              <button type="button" className="object-storage-dialog-cancel" onClick={() => setDialog(null)}>{t('common.cancel')}</button>
              <button type="button" className="object-storage-dialog-confirm" onClick={() => void handleCreate()}>{t('common.create')}</button>
            </div>
          </div>
        </div>
      )}

      {/* confirm delete */}
      {confirmDelete && (
        <div className="object-storage-dialog-backdrop" onClick={() => setConfirmDelete(null)}>
          <div className="object-storage-dialog" onClick={(e) => e.stopPropagation()}>
            <div className="object-storage-dialog-title">
              <Trash2 size={14} /> {t('objectStorage.confirmDelete.title')}
            </div>
            <div className="object-storage-dialog-body">
              <div className="object-storage-confirm-list">
                {confirmDelete.entries.map(e => (
                  <div key={e.Path} className="object-storage-confirm-row">
                    {e.IsDir ? <Folder size={12} /> : <File size={12} />}
                    <span>{e.Name}</span>
                  </div>
                ))}
              </div>
              <div className="object-storage-confirm-hint">{t('objectStorage.confirmDelete.hint')}</div>
            </div>
            <div className="object-storage-dialog-actions">
              <button type="button" className="object-storage-dialog-cancel" onClick={() => setConfirmDelete(null)}>{t('common.cancel')}</button>
              <button
                type="button"
                className="object-storage-dialog-confirm object-storage-dialog-danger"
                onClick={() => { const list = confirmDelete.entries; setConfirmDelete(null); void handleDelete(list) }}
              >
                {t('common.delete')}
              </button>
            </div>
          </div>
        </div>
      )}

      {toast && <div className="object-storage-toast">{toast}</div>}
    </div>
  )
}

// ── helpers (file IO) ─────────────────────────────────────────────────────

function base64ToBlob(b64: string, mime: string): Blob {
  const bin = atob(b64)
  const buf = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i)
  return new Blob([buf], { type: mime || 'application/octet-stream' })
}

function triggerBrowserDownload(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  setTimeout(() => {
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }, 0)
}

const MIME_HINTS: Record<string, string> = {
  txt: 'text/plain',
  json: 'application/json',
  md: 'text/markdown',
  html: 'text/html',
  htm: 'text/html',
  css: 'text/css',
  js: 'application/javascript',
  ts: 'application/typescript',
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  svg: 'image/svg+xml',
  pdf: 'application/pdf',
  zip: 'application/zip',
  tar: 'application/x-tar',
  gz: 'application/gzip',
  mp4: 'video/mp4',
  mp3: 'audio/mpeg',
}

function guessMime(name: string): string {
  const i = name.lastIndexOf('.')
  if (i < 0) return 'application/octet-stream'
  const ext = name.slice(i + 1).toLowerCase()
  return MIME_HINTS[ext] ?? 'application/octet-stream'
}

// helper to read a response payload via dbclient.object_read; exported for
// tests / external callers that want the bytes without going through the UI.
export async function downloadObjectBytes(profileId: string, path: string): Promise<DbObjectReadResp> {
  return await dbclientApi.objectRead(client, { ProfileId: profileId, Path: path })
}

// re-export basename/parentPath for tests
export const _internal = { basename, parentPath, formatBytes, formatModified, joinPath }
