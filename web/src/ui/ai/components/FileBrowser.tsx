import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import {
  ChevronRight, ChevronDown, Loader2, List, LayoutGrid,
  ArrowLeft, ArrowRight, FolderPlus, FilePlus, Copy, ExternalLink, Pencil, Trash2,
  FolderOpen, ListTree, RefreshCw, Scissors, Clipboard, Download, Eye, Star, X, MoreHorizontal, Folder,
} from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as projectClient from '../../../gen-clients/project/client'
import * as sshmanagerClient from '../../../gen-clients/sshmanager/client'
import * as filesystemClient from '../../../gen-clients/filesystem/client'
import type { FileEntry } from '../../../gen-clients/system/types'
import { parseListText } from './parts/list-text.ts'
import { isWails } from '../../../application/runtime'
import { getFolderIcon, getComposedFileIcon } from '../../panels/file-icons'
import { savePreference, loadPreference } from '../../../application/theme-persist'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import { useBrowserOverlay } from '../browserOverlay'
import { useMarqueeSelection, type MarqueeSelectionResult } from '../hooks/useMarqueeSelection'
import { useI18n } from '../../../i18n'
import { useFileClipboard } from './file-clipboard-context'
import { base64ToBytes } from './ImageViewer'
import { mimeFromExt } from './parts/image-utils'
import {
  setFileDragPayload,
  hasForeignFileDrag,
  getForeignFileDragPayload,
  beginLocalFileDragSession,
  endLocalFileDragSession,
  type FileDragPayload,
} from '../../file-dnd'
import {
  collectOsDroppedEntries,
  startOsFileDragOut,
  bytesToBase64,
  joinOsPath,
  OS_DROP_TARGET_ATTR,
} from '../../os-file-dnd'
import './FileBrowser.css'

/* ===================== helpers ===================== */

type ViewMode = 'list' | 'grid'

interface MenuEntry {
  name: string
  path: string
  isDir: boolean
}

interface MenuTarget extends MenuEntry {
  x: number
  y: number
}

type DialogState =
  | { kind: 'newfile'; dir: string }
  | { kind: 'newfolder'; dir: string }
  | { kind: 'rename'; path: string; name: string; isDir: boolean }
  | { kind: 'delete'; path: string; name: string; isDir: boolean }
  /** Multi-entry delete: all paths are removed on confirm. */
  | { kind: 'delete-multi'; paths: string[] }

/** One favorited (pinned) path shown in the favorites toolbar. */
interface FavoriteEntry {
  path: string
  isDir: boolean
}

/** Parse a persisted favorites JSON array; malformed entries are dropped. */
function parseFavorites(raw: string | undefined): FavoriteEntry[] {
  if (!raw) return []
  try {
    const arr: unknown = JSON.parse(raw)
    if (!Array.isArray(arr)) return []
    return arr.filter((e): e is FavoriteEntry =>
      !!e && typeof e === 'object'
      && typeof (e as FavoriteEntry).path === 'string'
      && typeof (e as FavoriteEntry).isDir === 'boolean')
  } catch {
    return []
  }
}

function joinPath(parent: string, name: string): string {
  if (parent === '.' || parent === '') return name
  return `${parent}/${name}`
}

function parentOf(path: string): string {
  if (path === '.' || path === '') return '.'
  const idx = path.lastIndexOf('/')
  if (idx < 0) return '.'
  return path.slice(0, idx) || '.'
}

/** Basename of a path (last segment). */
function baseName(path: string): string {
  if (path === '.' || path === '') return '.'
  const idx = path.lastIndexOf('/')
  return idx < 0 ? path : path.slice(idx + 1)
}

/**
 * Whether moving `sourcePath` into directory `targetDir` is valid.
 * Rejects: drop onto self, drop into own descendant, and no-op
 * (source already lives directly inside target).
 */
function canMove(sourcePath: string, targetDir: string): boolean {
  if (!sourcePath || sourcePath === targetDir) return false
  // target is the source itself or nested under it → would move a folder into its own child
  if (targetDir === sourcePath || targetDir.startsWith(sourcePath + '/')) return false
  // already directly inside the target directory
  if (parentOf(sourcePath) === targetDir) return false
  return true
}

function pathSegments(path: string): Array<{ name: string; path: string }> {
  const segs: Array<{ name: string; path: string }> = [{ name: '~', path: '.' }]
  if (path === '.' || path === '') return segs
  let acc = ''
  for (const part of path.split('/')) {
    acc = acc ? `${acc}/${part}` : part
    segs.push({ name: part, path: acc })
  }
  return segs
}

function sortEntries(entries: FileEntry[]): FileEntry[] {
  return [...entries].sort((a, b) => {
    if (a.IsDir === b.IsDir) return a.Name.localeCompare(b.Name)
    return a.IsDir ? -1 : 1
  })
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/** Display-layer conversion: ModTime is stored UTC ISO, rendered in local time (no seconds). */
function formatMtime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime())
    ? ''
    : d.toLocaleString(undefined, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function detectPlatform(): 'win' | 'mac' | 'linux' {
  const ua = typeof navigator !== 'undefined'
    ? ((navigator as { userAgentData?: { platform?: string } }).userAgentData?.platform || navigator.platform || '')
    : ''
  const p = ua.toLowerCase()
  if (p.includes('win')) return 'win'
  if (p.includes('mac')) return 'mac'
  return 'linux'
}

/**
 * Classify a backend archive error string into a localized i18n key for known
 * failure patterns (size limit exceeded, decompression failure). Returns null
 * for unrecognised errors so the caller can fall back to the generic message.
 */
function classifyArchiveDownloadError(msg: string): 'fileBrowser.toast.archiveTooLarge' | 'fileBrowser.toast.decompressFailed' | null {
  const lower = msg.toLowerCase()
  if (lower.includes('exceeds') && (lower.includes('limit') || lower.includes('byte') || lower.includes('entry'))) {
    return 'fileBrowser.toast.archiveTooLarge'
  }
  if (lower.includes('gzip') || lower.includes('tar') || lower.includes('decompress') || lower.includes('extract') || lower.includes('base64')) {
    return 'fileBrowser.toast.decompressFailed'
  }
  return null
}

/** Last non-empty stderr line, capped for toast display. */
function errDetail(stderr: string): string {
  const lines = stderr.split(/\r?\n/).map(l => l.trim()).filter(Boolean)
  if (lines.length === 0) return ''
  const last = lines[lines.length - 1]!
  return last.length > 160 ? last.slice(0, 157) + '...' : last
}

/** Drain a streaming shell_exec and return the final exit code plus a trimmed
 * stderr tail so failure toasts can state the underlying cause (e.g. mkdir's
 * "File exists" when a same-name file blocks folder creation). */
async function runShell(projectId: string, command: string, args: string[], dir?: string): Promise<{ code: number; stderr: string }> {
  let code = 1
  let stderr = ''
  try {
    for await (const chunk of projectClient.shellExec(
      client,
      { Command: command, Args: args, Dir: dir },
      { target: projectId },
    )) {
      if (chunk.ExitCode != null) code = chunk.ExitCode
      if (chunk.Stderr) stderr += chunk.Stderr
    }
  } catch {
    code = 1
  }
  return { code, stderr: errDetail(stderr) }
}

/** Whether a drag session carries OS-originated files (in-app drags only carry custom MIME / text). */
function hasOsFiles(dt: DataTransfer): boolean {
  return dt.types.includes('Files')
}

/**
 * Alt+dragstart hands the row over to the native OS drag-out (desktop only):
 * the HTML5 drag is suppressed and `startOsFileDragOut` resolves the
 * project-relative path to an absolute one. Returns true when the OS path was
 * taken; otherwise the caller proceeds with the normal in-app drag semantics.
 */
function handleRowDragStart(e: React.DragEvent, projectId: string, relPath: string): boolean {
  if (!e.altKey || !isWails()) return false
  e.preventDefault()
  void startOsFileDragOut([{ kind: 'local', projectId, relPath }])
  return true
}

/* ===================== tree node ===================== */

interface TreeNodeProps {
  projectId: string
  path: string
  name: string
  isDir: boolean
  depth: number
  showHidden: boolean
  selectedPath: string | null
  selectedPaths: Set<string>
  reloadKey: number
  onOpenFile?: (path: string) => void
  onSelect: (e: React.MouseEvent, path: string) => void
  onNavigate: (path: string) => void
  onContextMenu: (e: React.MouseEvent, entry: MenuEntry) => void
  initialExpanded?: boolean
  dragSourcePath: string | null
  dragOverDir: string | null
  cutSourcePaths: Set<string>
  favoritePaths: Set<string>
  onToggleFavorite: (target: { path: string; isDir: boolean }) => void
  onItemDragStart: (path: string) => void
  onItemDragEnd: () => void
  onDirDrop: (e: React.DragEvent, dir: string) => void
  onDragOverDirChange: (dir: string | null) => void
}

function TreeNode({
  projectId, path, name, isDir, depth, showHidden, selectedPath, selectedPaths, reloadKey,
  onOpenFile, onSelect, onNavigate, onContextMenu, initialExpanded,
  dragSourcePath, dragOverDir, cutSourcePaths, favoritePaths, onToggleFavorite,
  onItemDragStart, onItemDragEnd, onDirDrop, onDragOverDirChange,
}: TreeNodeProps) {
  const [expanded, setExpanded] = useState(!!initialExpanded)
  const [children, setChildren] = useState<FileEntry[] | null>(null)
  const [loading, setLoading] = useState(false)
  const { t } = useI18n()

  useEffect(() => {
    if (!expanded || !isDir) return
    let cancelled = false
    setLoading(true)
    projectClient.list(client, { Path: path, Depth: 0, All: showHidden, No_ignore: showHidden, Detail: true }, { target: projectId })
      .then(text => {
        if (!cancelled) {
          setChildren(sortEntries(parseListText(text).entries))
          setLoading(false)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setChildren([])
          setLoading(false)
        }
      })
    return () => { cancelled = true }
  }, [expanded, isDir, path, projectId, reloadKey, showHidden])

  const handleClick = (e: React.MouseEvent) => {
    const isModifier = e.ctrlKey || e.metaKey || e.altKey || e.shiftKey
    if (!isModifier) {
      if (isDir) {
        onNavigate(path)
        setExpanded(v => !v)
      } else {
        onOpenFile?.(path)
      }
    }
    onSelect(e, path)
  }

  const handleChevronClick = (e: React.MouseEvent) => {
    e.stopPropagation()
    setExpanded(v => !v)
  }

  const handleContextMenu = (e: React.MouseEvent) => {
    e.preventDefault()
    e.stopPropagation()
    onContextMenu(e, { name, path, isDir })
  }

  const indent = depth * 14 + 4
  const isDragging = dragSourcePath === path
  const isDragOver = isDir && dragOverDir === path
  const isCutSource = cutSourcePaths.has(path)
  const altDragHint = isWails()

  return (
    <div className="fb-tree-node">
      <div
        className={`fb-tree-node-row${selectedPath === path || selectedPaths.has(path) ? ' selected' : ''}${isDragOver ? ' drag-over' : ''}${isDragging ? ' dragging' : ''}${isCutSource ? ' cut-source' : ''}`}
        style={{ paddingLeft: indent }}
        data-mq-path={path}
        draggable
        onClick={handleClick}
        onContextMenu={handleContextMenu}
        onDragStart={(e) => {
          e.stopPropagation()
          if (handleRowDragStart(e, projectId, path)) return
          e.dataTransfer.setData('text/plain', path)
          setFileDragPayload(e.dataTransfer, { origin: 'local', projectId, path, name, isDir })
          e.dataTransfer.effectAllowed = 'copyMove'
          onItemDragStart(path)
        }}
        onDragEnd={onItemDragEnd}
        onDragOver={isDir ? (e) => {
          if (hasOsFiles(e.dataTransfer)) {
            e.preventDefault()
            e.stopPropagation()
            e.dataTransfer.dropEffect = 'copy'
            if (dragOverDir !== path) onDragOverDirChange(path)
            return
          }
          if (hasForeignFileDrag(e.dataTransfer, 'local')) {
            e.preventDefault()
            e.stopPropagation()
            e.dataTransfer.dropEffect = 'copy'
            if (dragOverDir !== path) onDragOverDirChange(path)
            return
          }
          if (dragSourcePath && !canMove(dragSourcePath, path)) return
          e.preventDefault()
          e.stopPropagation()
          e.dataTransfer.dropEffect = 'move'
          if (dragOverDir !== path) onDragOverDirChange(path)
        } : undefined}
        onDragLeave={isDir && isDragOver ? () => onDragOverDirChange(null) : undefined}
        onDrop={isDir ? (e) => {
          e.preventDefault()
          e.stopPropagation()
          onDirDrop(e, path)
        } : undefined}
        title={altDragHint ? `${path}\n\n${t('fileBrowser.dragOutHint')}` : path}
      >
        <span
          className={`fb-tree-chevron${isDir ? '' : ' placeholder'}`}
          onClick={isDir ? handleChevronClick : undefined}
        >
          {isDir && (loading
            ? <Loader2 size={14} className="fb-spin" />
            : expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />)}
        </span>
        <span className="fb-tree-icon">
          {isDir ? getFolderIcon(false) : getComposedFileIcon(name)}
        </span>
        <span className="fb-tree-name">{name}</span>
        {favoritePaths.has(path) && (
          <button
            type="button"
            className="fb-tree-star"
            title={t('fileBrowserContextMenu.unfavorite')}
            onClick={(e) => { e.stopPropagation(); onToggleFavorite({ path, isDir }) }}
          >
            <Star size={10} fill="currentColor" strokeWidth={0} />
          </button>
        )}
      </div>
      {expanded && isDir && (
        <div className="fb-tree-children">
          {loading && <div className="fb-loading" style={{ paddingLeft: indent + 20 }}>{t('fileBrowser.loading')}</div>}
          {children?.map(child => (
            <TreeNode
              key={child.Name}
              projectId={projectId}
              path={joinPath(path, child.Name)}
              name={child.Name}
              isDir={child.IsDir}
              depth={depth + 1}
              showHidden={showHidden}
              selectedPath={selectedPath}
              selectedPaths={selectedPaths}
              reloadKey={reloadKey}
              onOpenFile={onOpenFile}
              onSelect={onSelect}
              onNavigate={onNavigate}
              onContextMenu={onContextMenu}
              dragSourcePath={dragSourcePath}
              dragOverDir={dragOverDir}
              cutSourcePaths={cutSourcePaths}
              favoritePaths={favoritePaths}
              onToggleFavorite={onToggleFavorite}
              onItemDragStart={onItemDragStart}
              onItemDragEnd={onItemDragEnd}
              onDirDrop={onDirDrop}
              onDragOverDirChange={onDragOverDirChange}
            />
          ))}
        </div>
      )}
    </div>
  )
}

/* ===================== tree view root ===================== */

interface TreeViewProps {
  projectId: string
  reloadKey: number
  showHidden: boolean
  selectedPath: string | null
  selectedPaths: Set<string>
  onOpenFile?: (path: string) => void
  onSelect: (e: React.MouseEvent, path: string) => void
  onNavigate: (path: string) => void
  onContextMenu: (e: React.MouseEvent, entry: MenuEntry) => void
  dragSourcePath: string | null
  dragOverDir: string | null
  cutSourcePaths: Set<string>
  favoritePaths: Set<string>
  onToggleFavorite: (target: { path: string; isDir: boolean }) => void
  onItemDragStart: (path: string) => void
  onItemDragEnd: () => void
  onDirDrop: (e: React.DragEvent, dir: string) => void
  onDragOverDirChange: (dir: string | null) => void
}

function TreeView({ projectId, reloadKey, showHidden, selectedPath, selectedPaths, onOpenFile, onSelect, onNavigate, onContextMenu, dragSourcePath, dragOverDir, cutSourcePaths, favoritePaths, onToggleFavorite, onItemDragStart, onItemDragEnd, onDirDrop, onDragOverDirChange }: TreeViewProps) {
  const [rootEntries, setRootEntries] = useState<FileEntry[] | null>(null)
  const [loading, setLoading] = useState(false)
  const { t } = useI18n()

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setRootEntries(null)
    projectClient.list(client, { Path: '.', Depth: 0, All: showHidden, No_ignore: showHidden, Detail: true }, { target: projectId })
      .then(text => {
        if (!cancelled) {
          setRootEntries(sortEntries(parseListText(text).entries))
          setLoading(false)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setRootEntries([])
          setLoading(false)
        }
      })
    return () => { cancelled = true }
  }, [projectId, reloadKey, showHidden])

  if (loading) return <div className="fb-loading">{t('fileBrowser.loading')}</div>
  if (rootEntries && rootEntries.length === 0) return <div className="fb-empty">{t('fileBrowser.empty.noFiles')}</div>

  return (
    <div className="fb-tree">
      {rootEntries?.map(child => (
        <TreeNode
          key={child.Name}
          projectId={projectId}
          path={child.Name}
          name={child.Name}
          isDir={child.IsDir}
          depth={0}
          showHidden={showHidden}
          selectedPath={selectedPath}
          selectedPaths={selectedPaths}
          reloadKey={reloadKey}
          onOpenFile={onOpenFile}
          onSelect={onSelect}
          onNavigate={onNavigate}
          onContextMenu={onContextMenu}
          dragSourcePath={dragSourcePath}
          dragOverDir={dragOverDir}
          cutSourcePaths={cutSourcePaths}
          favoritePaths={favoritePaths}
          onToggleFavorite={onToggleFavorite}
          onItemDragStart={onItemDragStart}
          onItemDragEnd={onItemDragEnd}
          onDirDrop={onDirDrop}
          onDragOverDirChange={onDragOverDirChange}
        />
      ))}
    </div>
  )
}

/* ===================== context menu ===================== */

interface ContextMenuProps {
  target: MenuTarget | null
  onClose: () => void
  onOpen: (entry: MenuEntry) => void
  onNewFile: (dir: string) => void
  onNewFolder: (dir: string) => void
  onCopy: (entry: MenuEntry) => void
  onCut: (entry: MenuEntry) => void
  onPaste: (target: MenuEntry) => void
  canPaste: boolean
  onCopyPath: (path: string) => void
  onReveal: (path: string) => void
  onRename: (entry: MenuEntry) => void
  onDelete: (entry: MenuEntry) => void
  onRefresh: () => void
  onDownload: (entry: MenuEntry) => void
  onToggleFavorite: (entry: MenuEntry) => void
  favorited: boolean
  selectedCount: number
}

function FileBrowserContextMenu({
  target, onClose, onOpen, onNewFile, onNewFolder, onCopy, onCut, onPaste, canPaste, onCopyPath, onReveal, onRename, onDelete, onRefresh, onDownload, onToggleFavorite, favorited, selectedCount,
}: ContextMenuProps) {
  const menuRef = useRef<HTMLDivElement>(null)
  const { t } = useI18n()
  useMenuDismiss(menuRef, onClose, target)

  useLayoutEffect(() => {
    if (!target || !menuRef.current) return
    const menu = menuRef.current
    const rect = menu.getBoundingClientRect()
    const vw = document.documentElement.clientWidth
    const vh = document.documentElement.clientHeight
    const pad = 8
    let left = target.x
    let top = target.y
    if (left + rect.width > vw - pad) left = target.x - rect.width
    if (left < pad) left = pad
    if (top + rect.height > vh - pad) top = target.y - rect.height
    if (top < pad) top = pad
    menu.style.left = `${left}px`
    menu.style.top = `${top}px`
  }, [target])

  if (!target) return null

  const { name, path, isDir, x, y } = target

  const run = (fn: () => void) => () => { fn(); onClose() }

  return (
    <div
      ref={menuRef}
      className="fb-context-menu"
      style={{ left: x, top: y }}
      role="menu"
      tabIndex={-1}
    >
      <div className="fb-context-menu-header">
        <span className="fb-context-menu-title">{selectedCount > 1 ? `${name} (+${selectedCount - 1})` : name}</span>
      </div>
      <div className="fb-context-menu-list">
        <button type="button" className="fb-context-menu-item" onClick={run(() => onOpen(target))}>
          <span className="fb-context-menu-icon">{isDir ? <FolderOpen size={13} /> : <ExternalLink size={13} />}</span>
          {t('fileBrowserContextMenu.open')}
        </button>

        <button type="button" className="fb-context-menu-item" onClick={run(() => onToggleFavorite(target))}>
          <span className="fb-context-menu-icon">
            <Star size={13} fill={favorited ? 'currentColor' : 'none'} />
          </span>
          {favorited ? t('fileBrowserContextMenu.unfavorite') : t('fileBrowserContextMenu.favorite')}
        </button>

        {isDir && (
          <>
            <button type="button" className="fb-context-menu-item" onClick={run(() => onNewFile(path))}>
              <span className="fb-context-menu-icon"><FilePlus size={13} /></span>
              {t('fileBrowserContextMenu.newFile')}
            </button>
            <button type="button" className="fb-context-menu-item" onClick={run(() => onNewFolder(path))}>
              <span className="fb-context-menu-icon"><FolderPlus size={13} /></span>
              {t('fileBrowserContextMenu.newFolder')}
            </button>
          </>
        )}

        <button type="button" className="fb-context-menu-item" onClick={run(() => onCopy(target))}>
          <span className="fb-context-menu-icon"><Copy size={13} /></span>
          {t('fileBrowserContextMenu.copy')}
        </button>
        <button type="button" className="fb-context-menu-item" onClick={run(() => onCut(target))}>
          <span className="fb-context-menu-icon"><Scissors size={13} /></span>
          {t('fileBrowserContextMenu.cut')}
        </button>
        {canPaste && (
          <button type="button" className="fb-context-menu-item" onClick={run(() => onPaste(target))}>
            <span className="fb-context-menu-icon"><Clipboard size={13} /></span>
            {t('fileBrowserContextMenu.paste')}
          </button>
        )}
        <button type="button" className="fb-context-menu-item" onClick={run(() => onDownload(target))}>
          <span className="fb-context-menu-icon"><Download size={13} /></span>
          {t('fileBrowserContextMenu.download')}
        </button>

        <div className="fb-context-menu-sep" />

        <button type="button" className="fb-context-menu-item" onClick={run(() => onCopyPath(path))}>
          <span className="fb-context-menu-icon"><Copy size={13} /></span>
          {t('fileBrowserContextMenu.copyPath')}
        </button>
        <button type="button" className="fb-context-menu-item" onClick={run(() => onReveal(path))}>
          <span className="fb-context-menu-icon"><ExternalLink size={13} /></span>
          {t('fileBrowserContextMenu.revealInSystem')}
        </button>
        <button type="button" className="fb-context-menu-item" onClick={run(() => onRefresh())}>
          <span className="fb-context-menu-icon"><RefreshCw size={13} /></span>
          {t('fileBrowser.toolbar.refresh')}
        </button>

        <div className="fb-context-menu-sep" />

        <button type="button" className="fb-context-menu-item" onClick={run(() => onRename(target))}>
          <span className="fb-context-menu-icon"><Pencil size={13} /></span>
          {t('fileBrowserContextMenu.rename')}
        </button>
        <button type="button" className="fb-context-menu-item danger" onClick={run(() => onDelete(target))}>
          <span className="fb-context-menu-icon"><Trash2 size={13} /></span>
          {t('fileBrowserContextMenu.delete')}
        </button>
      </div>
    </div>
  )
}

/* ===================== dialog ===================== */

interface DialogProps {
  state: DialogState
  onConfirm: (value: string) => void
  onCancel: () => void
}

function FileBrowserDialog({ state, onConfirm, onCancel }: DialogProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const { t } = useI18n()
  const [value, setValue] = useState(
    state.kind === 'rename' ? state.name : '',
  )

  useEffect(() => {
    inputRef.current?.focus()
    if (state.kind === 'rename') inputRef.current?.select()
  }, [state.kind])

  const isPrompt = state.kind === 'newfile' || state.kind === 'newfolder' || state.kind === 'rename'
  const isDelete = state.kind === 'delete' || state.kind === 'delete-multi'

  const titles: Record<DialogState['kind'], string> = {
    newfile: t('fileBrowserDialog.title.newFile'),
    newfolder: t('fileBrowserDialog.title.newFolder'),
    rename: t('fileBrowserDialog.title.rename'),
    delete: t('fileBrowserDialog.title.delete'),
    'delete-multi': t('fileBrowserDialog.title.deleteMulti', { count: state.kind === 'delete-multi' ? state.paths.length : 0 }),
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (isPrompt && !value.trim()) return
    onConfirm(value.trim())
  }

  return (
    <div className="fb-dialog-overlay" onClick={onCancel}>
      <form className="fb-dialog" onClick={e => e.stopPropagation()} onSubmit={handleSubmit}>
        <div className="fb-dialog-title">{titles[state.kind]}</div>
        {isDelete ? (
          <div className="fb-dialog-message">
            {state.kind === 'delete-multi'
              ? t('fileBrowserDialog.deleteConfirmMulti', {
                  name: baseName(state.paths[0] ?? ''),
                  count: state.paths.length - 1,
                })
              : state.isDir
                ? t('fileBrowserDialog.deleteConfirmFolder', { name: state.name })
                : t('fileBrowserDialog.deleteConfirmFile', { name: state.name })}
            <br />{t('fileBrowserDialog.deleteWarning')}
          </div>
        ) : (
          <input
            ref={inputRef}
            className="fb-dialog-input"
            type="text"
            placeholder={state.kind === 'newfolder'
              ? t('fileBrowserDialog.placeholder.folderName')
              : t('fileBrowserDialog.placeholder.fileName')}
            value={value}
            onChange={e => setValue(e.target.value)}
          />
        )}
        <div className="fb-dialog-actions">
          <button type="button" className="fb-dialog-btn" onClick={onCancel}>{t('fileBrowserDialog.button.cancel')}</button>
          <button
            type="submit"
            className={`fb-dialog-btn ${isDelete ? 'danger' : 'primary'}`}
          >
            {isDelete
              ? t('fileBrowserDialog.button.delete')
              : state.kind === 'rename'
                ? t('fileBrowserDialog.button.rename')
                : t('fileBrowserDialog.button.create')}
          </button>
        </div>
      </form>
    </div>
  )
}

/* ===================== favorites bar ===================== */

interface FavoritesBarProps {
  favorites: FavoriteEntry[]
  onOpenEntry: (path: string, isDir: boolean) => void
  onRemove: (path: string) => void
}

/** Fixed horizontal budget subtracted before chip fitting: bar padding (16),
 * leading star icon (19) and the collapsed-overflow button with its gap (40). */
const FAV_BAR_RESERVE = 16 + 19 + 36 + 4
const FAV_CHIP_GAP = 4

/**
 * Second toolbar row under the main file-mode toolbar: favorited paths render
 * as chips; chips that exceed the available width collapse into a "+N"
 * overflow menu. Chip widths are measured in a first pass that renders every
 * chip (masked by overflow hidden), then the row re-renders truncated.
 */
function FavoritesBar({ favorites, onOpenEntry, onRemove }: FavoritesBarProps) {
  const { t } = useI18n()
  const barRef = useRef<HTMLDivElement>(null)
  const itemsRef = useRef<HTMLDivElement>(null)
  const moreRef = useRef<HTMLDivElement>(null)
  /** null while a measure pass is pending; measured chip widths otherwise. */
  const [widths, setWidths] = useState<number[] | null>(null)
  const [barWidth, setBarWidth] = useState(0)
  const [moreOpen, setMoreOpen] = useState(false)
  const skipFirstInvalidate = useRef(true)
  useMenuDismiss(moreRef, () => setMoreOpen(false), moreOpen)

  // A changed favorite list invalidates the measured widths (skipped on mount:
  // the passive effect runs after the initial measure pass and would null the
  // fresh measurement without the layout effect re-triggering).
  useEffect(() => {
    if (skipFirstInvalidate.current) { skipFirstInvalidate.current = false; return }
    setWidths(null)
  }, [favorites])

  useLayoutEffect(() => {
    if (widths !== null) return
    const itemsEl = itemsRef.current
    if (!itemsEl) return
    setWidths(Array.from(itemsEl.children).map(el => (el as HTMLElement).offsetWidth))
  }, [widths, favorites])

  useLayoutEffect(() => {
    const bar = barRef.current
    if (!bar || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => setBarWidth(bar.clientWidth))
    ro.observe(bar)
    setBarWidth(bar.clientWidth)
    return () => ro.disconnect()
  }, [])

  const visibleCount = (() => {
    // barWidth 0 = unmeasured (or headless test env): render everything.
    if (widths === null || barWidth <= 0) return favorites.length
    const budget = barWidth - FAV_BAR_RESERVE
    let acc = 0
    let count = 0
    for (const w of widths) {
      const next = acc + (count > 0 ? FAV_CHIP_GAP : 0) + w
      if (next > budget) break
      acc = next
      count++
    }
    return Math.max(0, count)
  })()
  const overflowCount = Math.max(0, favorites.length - visibleCount)
  const hidden = favorites.slice(visibleCount)

  const chipIcon = (f: FavoriteEntry) =>
    f.isDir ? getFolderIcon(false) : getComposedFileIcon(baseName(f.path))

  const renderChip = (f: FavoriteEntry) => (
    <div key={f.path} className="fb-fav-chip" title={f.path}>
      <button
        type="button"
        className="fb-fav-chip-open"
        onClick={() => onOpenEntry(f.path, f.isDir)}
      >
        <span className="fb-fav-chip-icon">{chipIcon(f)}</span>
        <span className="fb-fav-chip-name">{baseName(f.path)}</span>
      </button>
      <button
        type="button"
        className="fb-fav-chip-remove"
        title={t('fileBrowser.favorites.remove')}
        onClick={(e) => { e.stopPropagation(); onRemove(f.path) }}
      >
        <X size={10} />
      </button>
    </div>
  )

  return (
    <div className="fb-fav-bar" ref={barRef}>
      <Star size={11} className="fb-fav-bar-star" fill="currentColor" strokeWidth={0} />
      <div className="fb-fav-items" ref={itemsRef}>
        {(widths === null ? favorites : favorites.slice(0, visibleCount)).map(renderChip)}
      </div>
      {overflowCount > 0 && (
        <div className="fb-fav-more-wrap" ref={moreRef}>
          <button
            type="button"
            className="fb-fav-more"
            title={t('fileBrowser.favorites.more')}
            onClick={() => setMoreOpen(v => !v)}
          >
            <MoreHorizontal size={13} />
            <span className="fb-fav-more-count">{overflowCount}</span>
          </button>
          {moreOpen && (
            <div className="fb-fav-more-menu">
              {hidden.map(f => (
                <button
                  key={f.path}
                  type="button"
                  className="fb-fav-more-item"
                  title={f.path}
                  onClick={() => { setMoreOpen(false); onOpenEntry(f.path, f.isDir) }}
                >
                  <span className="fb-fav-chip-icon">{chipIcon(f)}</span>
                  <span className="fb-fav-more-name">{baseName(f.path)}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/* ===================== project picker ===================== */

/**
 * Toolbar dropdown that selects the browsed project directory. Used in the
 * self-managed selection mode (files mode) where the browser's target is
 * independent of the agent's active project.
 */
function ProjectPicker({
  projects,
  value,
  onSelect,
}: {
  projects: FileBrowserProjectOption[]
  value: string
  onSelect: (projectId: string) => void
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const btnRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  useMenuDismiss(menuRef, () => setOpen(false), open)
  // Native right-panel browser windows must hide while the dropdown floats.
  useBrowserOverlay(open)

  // Anchor the menu below the trigger button, clamped to the viewport.
  useLayoutEffect(() => {
    if (!open || !btnRef.current || !menuRef.current) return
    const menu = menuRef.current
    const rect = btnRef.current.getBoundingClientRect()
    const vw = document.documentElement.clientWidth
    let left = rect.left
    if (left + menu.offsetWidth > vw - 8) left = Math.max(8, vw - menu.offsetWidth - 8)
    menu.style.left = `${left}px`
    menu.style.top = `${rect.bottom + 4}px`
  }, [open])

  const current = projects.find(p => p.ProjectID === value)

  return (
    <div className="fb-project-picker">
      <button
        ref={btnRef}
        type="button"
        className="fb-project-btn"
        onClick={() => setOpen(v => !v)}
        title={t('fileBrowser.toolbar.project')}
      >
        <Folder size={13} />
        <span className="fb-project-name">{current?.Name ?? t('fileBrowser.toolbar.project')}</span>
        <ChevronDown size={12} className="fb-project-caret" />
      </button>
      {open && createPortal(
        <div ref={menuRef} className="fb-project-menu" role="menu">
          {projects.map(p => (
            <button
              key={p.ProjectID}
              type="button"
              role="menuitem"
              className={`fb-project-item${p.ProjectID === value ? ' active' : ''}`}
              onClick={() => { setOpen(false); onSelect(p.ProjectID) }}
            >
              <Folder size={13} />
              <span className="fb-project-item-name">{p.Name}</span>
            </button>
          ))}
        </div>,
        document.body,
      )}
    </div>
  )
}

/* ===================== main component ===================== */

export interface FileBrowserProps {
  /** Controlled project target. When omitted, the browser manages its own
   *  selection (files mode): resolved from the persisted preference against
   *  the `projects` list and switchable from the toolbar project picker. */
  projectId?: string
  /** Selectable projects for the toolbar picker. Provide to enable the
   *  browser-managed selection mode; ignored when `projectId` is controlled. */
  projects?: FileBrowserProjectOption[]
  /** True while the host is still loading its projects list: the browser
   *  shows its loading hint instead of the no-project placeholder, so files
   *  mode never flashes an empty prompt during startup. */
  projectsLoading?: boolean
  /** Notifies the host when the browser-managed project changes (user switches
   *  from the toolbar picker — not the initial resolution, so hosts restoring
   *  a navigation path are not clobbered). */
  onProjectChange?: (projectId: string) => void
  /** Called when a file is left-clicked. The layout wires this to open the right panel. */
  onOpenFile?: (filePath: string, projectId: string) => void
  className?: string
  initialView?: ViewMode
  /** Controlled current directory path. When omitted, the component manages its own path state. */
  path?: string
  /** Called when the current directory path changes due to user navigation. */
  onPathChange?: (path: string) => void
  /** Controlled selected path. When omitted, the component manages its own selection state. */
  selectedPath?: string | null
  /** Called when a file or directory is selected or deselected. */
  onSelectionChange?: (path: string | null) => void
}

/** A project entry selectable from the file-browser toolbar picker. */
export interface FileBrowserProjectOption {
  ProjectID: string
  Name: string
  IsOpen?: boolean
}

export function FileBrowser({
  projectId: projectIdProp,
  projects,
  projectsLoading = false,
  onProjectChange,
  onOpenFile,
  className,
  initialView = 'grid',
  path: pathProp,
  onPathChange,
  selectedPath: selectedPathProp,
  onSelectionChange,
}: FileBrowserProps) {
  const { t } = useI18n()
  /** Browser-managed project (files mode): resolved once from the persisted
   *  preference, then only changed through the toolbar picker. */
  const [internalProjectId, setInternalProjectId] = useState<string | null>(null)
  const projectId = projectIdProp ?? internalProjectId ?? ''
  const [view, setViewState] = useState<ViewMode>(initialView)
  const [uncontrolledPath, setUncontrolledPath] = useState(pathProp ?? '.')
  const [entries, setEntries] = useState<FileEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [uncontrolledSelection, setUncontrolledSelection] = useState<string | null>(null)
  const [history, setHistory] = useState<string[]>(['.'])
  const [historyIndex, setHistoryIndex] = useState(0)
  const [reloadKey, setReloadKey] = useState(0)
  const [treeReloadKey, setTreeReloadKey] = useState(0)
  const [treeSidebarOpen, setTreeSidebarOpen] = useState(true)
  const [showHidden, setShowHidden] = useState(false)
  const [menuTarget, setMenuTarget] = useState<MenuTarget | null>(null)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [dragSourcePath, setDragSourcePath] = useState<string | null>(null)
  const [dragOverDir, setDragOverDir] = useState<string | null>(null)
  const [selectedPaths, setSelectedPaths] = useState<Set<string>>(new Set())
  const [lastSelectedPath, setLastSelectedPath] = useState<string | null>(null)
  const [cutSourcePaths, setCutSourcePaths] = useState<Set<string>>(new Set())
  const mainContentRef = useRef<HTMLDivElement>(null)
  /** Last anchor value pushed via onSelectionChange; guards the controlled-prop sync effect. */
  const selfAnchorPushRef = useRef<string | null>(null)
  const { clipboard: clipboardRef, setClipboard, clipboardVersion } = useFileClipboard()
  void clipboardVersion
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const prefWriteVer = useRef({ v: 0 })
  const pathSaveTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [prefsLoaded, setPrefsLoaded] = useState(false)
  const [favorites, setFavorites] = useState<FavoriteEntry[]>([])
  const favoriteSet = useMemo(() => new Set(favorites.map(f => f.path)), [favorites])

  const currentPath = pathProp !== undefined ? pathProp : uncontrolledPath
  const selectedPath = selectedPathProp !== undefined ? selectedPathProp : uncontrolledSelection

  const setCurrentPath = useCallback(
    (next: string) => {
      if (pathProp === undefined) {
        setUncontrolledPath(next)
      }
      onPathChange?.(next)
    },
    [pathProp, onPathChange],
  )

  const setAnchor = useCallback(
    (next: string | null, clearMulti = false) => {
      // Record the push so the controlled-prop echo (parent feeding the same
      // value back through selectedPath) is not mistaken for an external
      // selection change — otherwise the sync effect below collapses the
      // multi-selection to the single anchor (marquee/Ctrl-click bug).
      selfAnchorPushRef.current = next
      // Mirror the push so a later controlled→uncontrolled flip (consumer passing
      // undefined for "no selection") resumes from the last pushed anchor instead
      // of a stale pre-control value.
      setUncontrolledSelection(next)
      onSelectionChange?.(next)
      if (clearMulti) {
        if (next) {
          setSelectedPaths(new Set([next]))
          setLastSelectedPath(next)
        } else {
          setSelectedPaths(new Set())
          setLastSelectedPath(null)
        }
      }
    },
    [selectedPathProp, onSelectionChange],
  )

  const setView = useCallback((v: ViewMode) => {
    setViewState(v)
    savePreference('fileBrowser.viewMode.v1', v, 'fb-viewmode', prefWriteVer.current).catch(() => {})
  }, [])

  /* --- marquee (rubber-band) selection over list/grid rows ---
   * The hook owns the empty-area press detection, drag rectangle and modifier
   * semantics; rows opt in via data-mq-path. Tree sidebar rows also carry the
   * attribute but live outside this container, so they only participate in
   * modifier-click selection, not the marquee drag itself. */
  const applyMarqueeResult = useCallback((result: MarqueeSelectionResult) => {
    const { mode, paths, isClick } = result
    if (isClick && mode === 'replace') {
      // plain empty-area click: clear everything
      setSelectedPaths(new Set())
      setLastSelectedPath(null)
      setAnchor(null)
      return
    }
    if (mode === 'range') {
      // shift+marquee: span from the anchor through all hit rows
      const list = entries
      if (list.length > 0 && lastSelectedPath) {
        const anchorIdx = list.findIndex(ent => joinPath(currentPath, ent.Name) === lastSelectedPath)
        const hitIdxs = paths
          .map(p => list.findIndex(ent => joinPath(currentPath, ent.Name) === p))
          .filter(i => i >= 0)
        if (anchorIdx >= 0 && hitIdxs.length > 0) {
          const start = Math.min(anchorIdx, ...hitIdxs)
          const end = Math.max(anchorIdx, ...hitIdxs)
          const range = new Set<string>()
          for (let i = start; i <= end; i++) range.add(joinPath(currentPath, list[i]!.Name))
          setSelectedPaths(range)
          return
        }
      }
      setSelectedPaths(new Set(paths))
      return
    }
    // replace / add / subtract: the hook resolved `next` against the base
    if (result.next) {
      const nextSet = new Set(result.next)
      setSelectedPaths(nextSet)
      if (nextSet.size === 0) {
        setLastSelectedPath(null)
        setAnchor(null)
      } else if (!nextSet.has(selectedPath ?? '')) {
        const last = [...nextSet].at(-1) ?? null
        setLastSelectedPath(last)
        setAnchor(last)
      }
    }
  }, [entries, currentPath, lastSelectedPath, selectedPath, setAnchor])

  useMarqueeSelection({
    containerRef: mainContentRef,
    itemSelector: '.fb-item-hit',
    getSelectedPaths: () => selectedPaths,
    onPreview: (r) => { if (r.next) setSelectedPaths(new Set(r.next)) },
    onHit: applyMarqueeResult,
  })

  /* --- browser-managed project selection (files mode) ---
   * Resolve once from the persisted preference, validated against the current
   * projects list; fall back to the first open project, then the first entry.
   * Deliberately independent of the agent's active project: switching agents
   * or projects elsewhere in the shell must not re-target the file browser. */
  const projectsKey = projects?.map(p => p.ProjectID).join(',') ?? ''
  useEffect(() => {
    if (projectIdProp !== undefined) return
    const list = projects ?? []
    if (list.length === 0) {
      setInternalProjectId(null)
      return
    }
    let cancelled = false
    loadPreference('fileBrowser.activeProject.v1').then(saved => {
      if (cancelled) return
      setInternalProjectId(prev => {
        if (prev && list.some(p => p.ProjectID === prev)) return prev
        if (saved && list.some(p => p.ProjectID === saved)) return saved
        return (list.find(p => p.IsOpen) ?? list[0])!.ProjectID
      })
    }).catch(() => {
      if (!cancelled) setInternalProjectId(prev => prev ?? (list.find(p => p.IsOpen) ?? list[0])!.ProjectID)
    })
    return () => { cancelled = true }
  }, [projectIdProp, projectsKey]) // eslint-disable-line react-hooks/exhaustive-deps

  /** User picked a project from the toolbar: retarget the browser, reset the
   *  navigation state to the new root (the per-project prefs effect below
   *  restores its persisted path/favorites) and persist the choice. */
  const selectProject = useCallback((id: string) => {
    if (!id || id === projectId) return
    setInternalProjectId(id)
    savePreference('fileBrowser.activeProject.v1', id, 'fb-project', prefWriteVer.current).catch(() => {})
    setHistory(['.'])
    setHistoryIndex(0)
    setCurrentPath('.')
    setAnchor(null, true)
    onProjectChange?.(id)
  }, [projectId, setCurrentPath, setAnchor, onProjectChange])

  // Load persisted view mode + hidden-files toggle + favorites + path on mount
  // and whenever the project changes (the component is not keyed by project in
  // files mode, so a project switch must reload the per-project state).
  useEffect(() => {
    if (!projectId) return
    let cancelled = false
    Promise.all([
      loadPreference('fileBrowser.viewMode.v1'),
      loadPreference('fileBrowser.showHidden.v1'),
      loadPreference(`fileBrowser.favorites.${projectId}.v1`),
      pathProp === undefined ? loadPreference(`fileBrowser.path.${projectId}.v1`) : Promise.resolve(undefined),
    ]).then(([savedView, savedHidden, savedFavorites, savedPath]) => {
      if (cancelled) return
      if (savedView === 'list' || savedView === 'grid') setViewState(savedView)
      if (savedHidden === 'true') setShowHidden(true)
      setFavorites(parseFavorites(savedFavorites))
      if (pathProp === undefined) {
        // Uncontrolled: adopt the per-project path, or reset to the root so a
        // path left over from the previous project does not leak across.
        const start = savedPath || '.'
        setHistory([start])
        setHistoryIndex(0)
        setCurrentPath(start)
      }
      setPrefsLoaded(true)
    }).catch(() => setPrefsLoaded(true))
    return () => { cancelled = true }
  }, [projectId]) // eslint-disable-line react-hooks/exhaustive-deps

  // Persist the hidden-files toggle (debounce-free: value flips are rare).
  useEffect(() => {
    if (!prefsLoaded) return
    savePreference('fileBrowser.showHidden.v1', showHidden ? 'true' : 'false', 'fb-showhidden', prefWriteVer.current).catch(() => {})
  }, [showHidden, prefsLoaded])

  // Persist current path (debounced) for list/grid (only when path is not controlled externally).
  useEffect(() => {
    if (!prefsLoaded) return
    if (pathProp !== undefined) return
    if (pathSaveTimer.current) clearTimeout(pathSaveTimer.current)
    pathSaveTimer.current = setTimeout(() => {
      savePreference(`fileBrowser.path.${projectId}.v1`, currentPath, `fb-path-${projectId}`, prefWriteVer.current).catch(() => {})
    }, 500)
    return () => { if (pathSaveTimer.current) clearTimeout(pathSaveTimer.current) }
  }, [currentPath, projectId, prefsLoaded, pathProp])

  // Sync multi-selection state when the controlled selected path prop changes externally.
  // Echoes of our own onSelectionChange pushes are skipped so marquee/Ctrl-click
  // multi-selections survive the round-trip through the parent.
  useEffect(() => {
    if (selectedPathProp !== undefined) {
      if (selfAnchorPushRef.current === selectedPathProp) return
      if (selectedPathProp) {
        setSelectedPaths(new Set([selectedPathProp]))
        setLastSelectedPath(selectedPathProp)
      } else {
        setSelectedPaths(new Set())
        setLastSelectedPath(null)
      }
    }
  }, [selectedPathProp])
  // Use refs to avoid spurious calls when the consumer passes a new callback
  // reference on every parent render; we only want to notify on value changes.
  const onPathChangeRef = useRef(onPathChange)
  const onSelectionChangeRef = useRef(onSelectionChange)
  useEffect(() => { onPathChangeRef.current = onPathChange }, [onPathChange])
  useEffect(() => { onSelectionChangeRef.current = onSelectionChange }, [onSelectionChange])
  useEffect(() => {
    onPathChangeRef.current?.(currentPath)
  }, [currentPath])
  useEffect(() => {
    onSelectionChangeRef.current?.(selectedPath)
  }, [selectedPath])

  const showToast = useCallback((msg: string) => {
    setToast(msg)
    if (toastTimer.current) clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToast(null), 2500)
  }, [])

  /* --- favorites (pinned paths, persisted per project) --- */
  const persistFavorites = useCallback((next: FavoriteEntry[]) => {
    savePreference(`fileBrowser.favorites.${projectId}.v1`, JSON.stringify(next), `fb-fav-${projectId}`, prefWriteVer.current).catch(() => {})
  }, [projectId])

  const toggleFavorite = useCallback((target: { path: string; isDir: boolean }) => {
    const next = favoriteSet.has(target.path)
      ? favorites.filter(f => f.path !== target.path)
      : [...favorites, { path: target.path, isDir: target.isDir }]
    setFavorites(next)
    persistFavorites(next)
  }, [favorites, favoriteSet, persistFavorites])

  /** Drop favorites pointing at deleted paths (or nested inside them). */
  const pruneFavorites = useCallback((deleted: string[]) => {
    if (favorites.length === 0) return
    const next = favorites.filter(f => !deleted.some(d => f.path === d || f.path.startsWith(d + '/')))
    if (next.length === favorites.length) return
    setFavorites(next)
    persistFavorites(next)
  }, [favorites, persistFavorites])

  /** Remap favorites after a rename (the entry itself plus descendants). */
  const remapFavoritePath = useCallback((oldPath: string, newPath: string) => {
    if (favorites.length === 0) return
    const touched = favorites.some(f => f.path === oldPath || f.path.startsWith(oldPath + '/'))
    if (!touched) return
    const next = favorites.map(f =>
      f.path === oldPath ? { ...f, path: newPath }
        : f.path.startsWith(oldPath + '/') ? { ...f, path: newPath + f.path.slice(oldPath.length) }
          : f)
    setFavorites(next)
    persistFavorites(next)
  }, [favorites, persistFavorites])

  useEffect(() => {
    return () => { if (toastTimer.current) clearTimeout(toastTimer.current) }
  }, [])

  /* --- list/grid directory loading --- */
  useEffect(() => {
    if (!projectId) return
    let cancelled = false
    setLoading(true)
    projectClient.list(client, { Path: currentPath, Depth: 0, All: showHidden, No_ignore: showHidden, Detail: true }, { target: projectId })
      .then(text => {
        if (!cancelled) {
          setEntries(sortEntries(parseListText(text).entries))
          setLoading(false)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setEntries([])
          setLoading(false)
        }
      })
    return () => { cancelled = true }
  }, [currentPath, projectId, reloadKey, showHidden])

  const refresh = useCallback(() => {
    setReloadKey(k => k + 1)
    setTreeReloadKey(k => k + 1)
  }, [])

  /* --- navigation --- */
  const navigateToWithHistory = useCallback((path: string) => {
    setCurrentPath(path)
    setAnchor(null, true)
    setHistory(prev => {
      const newHistory = prev.slice(0, historyIndex + 1)
      newHistory.push(path)
      setHistoryIndex(newHistory.length - 1)
      return newHistory
    })
  }, [historyIndex])

  const navigateTo = useCallback((path: string) => {
    navigateToWithHistory(path)
  }, [navigateToWithHistory])

  const navigateBack = useCallback(() => {
    if (historyIndex > 0) {
      const newIndex = historyIndex - 1
      const path = history[newIndex]
      if (path === undefined) return
      setHistoryIndex(newIndex)
      setCurrentPath(path)
      setAnchor(null, true)
    }
  }, [history, historyIndex])

  const navigateForward = useCallback(() => {
    if (historyIndex < history.length - 1) {
      const newIndex = historyIndex + 1
      const path = history[newIndex]
      if (path === undefined) return
      setHistoryIndex(newIndex)
      setCurrentPath(path)
      setAnchor(null, true)
    }
  }, [history, historyIndex])

  const canGoBack = historyIndex > 0
  const canGoForward = historyIndex < history.length - 1

  /* --- context menu open handler --- */
  const handleMenuOpen = useCallback((entry: MenuEntry) => {
    if (entry.isDir) {
      navigateTo(entry.path)
    } else {
      onOpenFile?.(entry.path, projectId)
    }
  }, [navigateTo, onOpenFile, projectId])

  /* --- copy path --- */
  const handleCopyPath = useCallback(async (path: string) => {
    try {
      await navigator.clipboard.writeText(path)
      showToast(t('fileBrowser.toast.copied'))
    } catch {
      showToast(t('fileBrowser.toast.copyFailed'))
    }
  }, [showToast])

  /* --- internal clipboard: copy / cut / paste --- */
  const handleCopy = useCallback((entry: MenuEntry) => {
    const paths = selectedPaths.size > 0 ? Array.from(selectedPaths) : [entry.path]
    setClipboard({ paths, isCut: false })
    setCutSourcePaths(new Set())
    showToast(t('fileBrowser.toast.fileCopied', { name: entry.name }))
  }, [showToast, t, setClipboard, selectedPaths])

  const handleCut = useCallback((entry: MenuEntry) => {
    const paths = selectedPaths.size > 0 ? Array.from(selectedPaths) : [entry.path]
    setClipboard({ paths, isCut: true })
    setCutSourcePaths(new Set(paths))
    showToast(t('fileBrowser.toast.fileCut', { name: entry.name }))
  }, [showToast, t, setClipboard, selectedPaths])

  const handlePaste = useCallback(async (target: MenuEntry) => {
    const clip = clipboardRef.current
    if (!clip || clip.paths.length === 0) return
    const targetDir = target.isDir ? target.path : currentPath

    let pasted = 0
    let conflicts = 0
    let invalid = 0
    const failed: string[] = []
    const movedPaths: string[] = []

    for (const srcPath of clip.paths) {
      if (clip.isCut) {
        if (!canMove(srcPath, targetDir)) {
          invalid++
          continue
        }
      } else {
        // copy into own descendant would be invalid
        if (targetDir === srcPath || targetDir.startsWith(srcPath + '/')) {
          invalid++
          continue
        }
      }
      const dest = joinPath(targetDir, baseName(srcPath))
      // conflict check: refuse if a same-name entry already exists at the destination
      const { code: existsCode } = await runShell(projectId, 'test', ['-e', dest])
      if (existsCode === 0) {
        conflicts++
        continue
      }
      const { code } = clip.isCut
        ? await runShell(projectId, 'mv', [srcPath, dest])
        : await runShell(projectId, 'cp', ['-r', srcPath, dest])
      if (code !== 0) {
        // a per-item failure does not abort the batch — report it and continue
        failed.push(baseName(srcPath))
        continue
      }
      pasted++
      if (clip.isCut) movedPaths.push(srcPath)
    }

    if (failed.length > 0) {
      showToast(t('fileBrowser.toast.pasteFailed', { name: failed[0] ?? '', count: failed.length }))
      if (clip.isCut && movedPaths.length > 0) {
        // prune the successfully-moved sources so a re-paste only retries the rest
        const moved = new Set(movedPaths)
        const remaining = clip.paths.filter(p => !moved.has(p))
        setClipboard(remaining.length > 0 ? { ...clip, paths: remaining } : null)
        setCutSourcePaths(new Set(remaining))
      }
      if (pasted > 0) refresh()
      return
    }

    if (conflicts > 0) {
      showToast(t('fileBrowser.toast.pasteConflict'))
      return
    }
    if (invalid > 0 && pasted === 0) {
      showToast(t('fileBrowser.toast.pasteInvalid'))
      return
    }

    if (clip.isCut) {
      setClipboard(null)
      setCutSourcePaths(new Set())
    }
    showToast(t('fileBrowser.toast.pasted'))
    refresh()
  }, [currentPath, projectId, refresh, showToast, t, clipboardRef, setClipboard])

  /* --- download --- */
  const handleDownload = useCallback(async (entry: MenuEntry) => {
    const paths = selectedPaths.size > 0 ? Array.from(selectedPaths) : [entry.path]

    // single file: direct download
    if (paths.length === 1 && !entry.isDir) {
      const path = paths[0]
      if (!path) return
      try {
        const resp = await projectClient.readBase64(client, { Path: path }, { target: projectId })
        if (!resp.Content) {
          showToast(t('fileBrowser.toast.operationFailed'))
          return
        }
        const bytes = base64ToBytes(resp.Content)
        const blob = new Blob([bytes], { type: mimeFromExt(path) || 'application/octet-stream' })
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = baseName(path)
        a.click()
        URL.revokeObjectURL(url)
        showToast(t('fileBrowser.toast.downloaded', { name: entry.name }))
      } catch {
        showToast(t('fileBrowser.toast.operationFailed'))
      }
      return
    }

    // multi-file or directory: archive via tar.gz
    try {
      const tmpDir = '.sporecode/tmp'
      const archiveName = `download-${Date.now()}-${Math.random().toString(36).slice(2)}.tar.gz`
      const archivePath = `${tmpDir}/${archiveName}`
      await runShell(projectId, 'mkdir', ['-p', tmpDir])
      const { code } = await runShell(projectId, 'tar', ['-czf', archivePath, ...paths])
      if (code !== 0) { showToast(t('fileBrowser.toast.operationFailed')); return }

      const resp = await projectClient.readBase64(client, { Path: archivePath }, { target: projectId })
      // clean up archive
      await runShell(projectId, 'rm', ['-f', archivePath])
      if (!resp.Content) {
        showToast(t('fileBrowser.toast.operationFailed'))
        return
      }
      const bytes = base64ToBytes(resp.Content)
      const blob = new Blob([bytes], { type: 'application/gzip' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = archiveName
      a.click()
      URL.revokeObjectURL(url)
      showToast(t('fileBrowser.toast.downloaded', { name: entry.name }))
    } catch {
      showToast(t('fileBrowser.toast.operationFailed'))
    }
  }, [projectId, showToast, t, selectedPaths])

  /* --- reveal in system --- */
  const handleReveal = useCallback(async (path: string) => {
    const platform = detectPlatform()
    let cmd: string
    let args: string[]
    if (platform === 'win') {
      cmd = 'bash'
      args = ['-c', `explorer.exe /select,"$(cygpath -w "$PWD/${path}" 2>/dev/null || echo "${path}")"`]
    } else if (platform === 'mac') {
      cmd = 'bash'
      args = ['-c', `open -R "$PWD/${path}"`]
    } else {
      cmd = 'bash'
      args = ['-c', `xdg-open "$(dirname "$PWD/${path}")"`]
    }
    const { code } = await runShell(projectId, cmd, args)
    if (code !== 0) showToast(t('fileBrowser.toast.fileManagerError'))
  }, [projectId, showToast])

  /* --- new file --- */
  const handleNewFile = useCallback((dir: string) => {
    setDialog({ kind: 'newfile', dir })
  }, [])

  const handleNewFolder = useCallback((dir: string) => {
    setDialog({ kind: 'newfolder', dir })
  }, [])

  /* --- rename --- */
  const handleRename = useCallback((entry: MenuEntry) => {
    setDialog({ kind: 'rename', path: entry.path, name: entry.name, isDir: entry.isDir })
  }, [])

  /* --- delete --- */
  const handleDelete = useCallback((entry: MenuEntry) => {
    // When the clicked entry is part of a multi-selection, delete all selected
    // paths; otherwise delete the single entry.
    if (selectedPaths.size > 1 && selectedPaths.has(entry.path)) {
      setDialog({ kind: 'delete-multi', paths: Array.from(selectedPaths) })
    } else {
      setDialog({ kind: 'delete', path: entry.path, name: entry.name, isDir: entry.isDir })
    }
  }, [selectedPaths])

  /**
   * Drop deleted paths from the cut-source highlight and from the clipboard
   * (a cut whose sources are gone must not paste stale entries).
   */
  const cleanupDeletedPaths = useCallback((deleted: Iterable<string>) => {
    const del = new Set(deleted)
    setCutSourcePaths(prev => {
      if (prev.size === 0) return prev
      const next = new Set<string>()
      for (const p of prev) if (!del.has(p)) next.add(p)
      return next
    })
    const clip = clipboardRef.current
    if (clip) {
      const remaining = clip.paths.filter(p => !del.has(p))
      if (remaining.length !== clip.paths.length) {
        setClipboard(remaining.length > 0 ? { ...clip, paths: remaining } : null)
      }
    }
  }, [clipboardRef, setClipboard])

  /* --- drag & drop move (reuses local mv) --- */
  const handleItemDragStart = useCallback((path: string) => {
    setDragSourcePath(path)
    // Bracket the in-app drag session so iframe-hosting targets (plugin
    // panels) can mount a drop overlay while the drag is live.
    beginLocalFileDragSession()
  }, [])

  const handleItemDragEnd = useCallback(() => {
    setDragSourcePath(null)
    setDragOverDir(null)
    endLocalFileDragSession()
  }, [])

  /* --- cross-panel download (remote → local) via tar.gz archive transfer ---
   * Directories and single files (including binary) are packed into a tar.gz by
   * sshmanager.archive_export and extracted into the drop target directory by
   * project.archive_import. The archive preserves the top-level entry name, so
   * the import path is the target directory itself (not a joined child path).
   * This unifies directories and binary single files while keeping original bytes. */
  const handleRemoteDownload = useCallback(async (payload: FileDragPayload, targetDir: string) => {
    if (!payload.sessionId) { showToast(t('fileBrowser.toast.operationFailed')); return }
    try {
      const exp = await sshmanagerClient.archiveExport(client, { SessionId: payload.sessionId, Path: payload.path })
      console.log('[file-dnd] archiveExport response:', exp, 'type:', typeof exp, 'Content?', exp?.Content?.length)
      if (!exp || !exp.Content) {
        showToast(`${t('fileBrowser.toast.operationFailed')}: archiveExport returned empty response`)
        return
      }
      await projectClient.archiveImport(client, { Path: targetDir, Content: exp.Content }, { target: projectId })
      showToast(t('fileBrowser.toast.downloaded', { name: payload.name }))
      refresh()
    } catch (e) {
      console.error('[file-dnd] remote archive transfer failed', e)
      const errMsg = e instanceof Error ? e.message : String(e)
      const cls = classifyArchiveDownloadError(errMsg)
      showToast(cls ? t(cls) : `${t('fileBrowser.toast.operationFailed')}: ${errMsg}`)
    }
  }, [projectId, refresh, showToast, t])

  /* --- OS file drop (drag from OS/desktop into the project) ---
   * collectOsDroppedEntries (os-file-dnd) flattens the dropped tree — dirs
   * before children, depth-first. Directories are created via mkdir -p in the
   * project shell (paths relative to the project root, same as New Folder);
   * files are written through filesystem.writeBase64 with an absolute path
   * resolved from project.info Roots[0] (the project actor has no binary
   * write; write_base64 creates missing parent dirs itself). */
  const handleOsDrop = useCallback(async (dt: DataTransfer, targetDir: string) => {
    let entries
    try {
      entries = await collectOsDroppedEntries(dt)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      showToast(`${t('fileBrowser.toast.operationFailed')}: ${msg}`)
      return
    }
    if (entries.length === 0) return
    let root = ''
    try {
      const info = await projectClient.info(client, { target: projectId })
      root = info.Roots[0]?.Path ?? ''
    } catch {
      // keep empty root — the write below fails per-file and is reported
    }
    const failures: string[] = []
    for (const ent of entries) {
      const rel = joinPath(targetDir, ent.relPath)
      if (ent.isDir) {
        const { code } = await runShell(projectId, 'mkdir', ['-p', rel])
        if (code !== 0) failures.push(ent.relPath)
        continue
      }
      try {
        const bytes = await ent.readBytes()
        await filesystemClient.writeBase64(client, {
          Path: root ? joinOsPath(root, rel) : rel,
          Content: bytesToBase64(bytes),
        })
      } catch {
        failures.push(ent.relPath)
      }
    }
    refresh()
    if (failures.length > 0) {
      showToast(t('fileBrowser.toast.osDropFailed', { count: failures.length }))
      return
    }
    showToast(t('fileBrowser.toast.osDropComplete', { count: entries.length }))
  }, [projectId, refresh, showToast, t])

  const handleDirDrop = useCallback(async (e: React.DragEvent, targetDir: string) => {
    // Cross-panel: remote → local download
    const foreign = getForeignFileDragPayload(e.dataTransfer, 'local')
    if (foreign) {
      await handleRemoteDownload(foreign, targetDir)
      return
    }
    // OS drop (dragged from the OS/desktop) into this directory
    if (hasOsFiles(e.dataTransfer)) {
      await handleOsDrop(e.dataTransfer, targetDir)
      return
    }
    // Internal move
    const source = dragSourcePath
    setDragSourcePath(null)
    setDragOverDir(null)
    if (!source) return
    if (!canMove(source, targetDir)) {
      showToast(t('fileBrowser.toast.moveInvalid'))
      return
    }
    const dest = joinPath(targetDir, baseName(source))
    const { code } = await runShell(projectId, 'mv', [source, dest])
    if (code !== 0) { showToast(t('fileBrowser.toast.moveFailed')); return }
    showToast(t('fileBrowser.toast.moved'))
    refresh()
  }, [dragSourcePath, projectId, refresh, showToast, t, handleRemoteDownload, handleOsDrop])

  /* --- dialog confirm --- */
  const handleDialogConfirm = useCallback(async (value: string) => {
    if (!dialog) return
    const d = dialog
    setDialog(null)

    try {
      if (d.kind === 'newfile') {
        const newPath = joinPath(d.dir, value)
        await projectClient.write(client, { Path: newPath, Content: '' }, { target: projectId })
        showToast(t('fileBrowser.toast.created', { name: value }))
      } else if (d.kind === 'newfolder') {
        const newPath = joinPath(d.dir, value)
        const { code, stderr } = await runShell(projectId, 'mkdir', ['-p', newPath])
        if (code !== 0) {
          showToast(stderr
            ? t('fileBrowser.toast.folderCreateErrorDetail', { detail: stderr })
            : t('fileBrowser.toast.folderCreateError'))
          return
        }
        showToast(t('fileBrowser.toast.created', { name: value }))
      } else if (d.kind === 'rename') {
        const dir = parentOf(d.path)
        const newPath = joinPath(dir, value)
        if (newPath === d.path) return
        const { code, stderr } = await runShell(projectId, 'mv', [d.path, newPath])
        if (code !== 0) {
          showToast(stderr
            ? t('fileBrowser.toast.renameFailedDetail', { detail: stderr })
            : t('fileBrowser.toast.renameFailed'))
          return
        }
        remapFavoritePath(d.path, newPath)
        showToast(t('fileBrowser.toast.renamed'))
      } else if (d.kind === 'delete' || d.kind === 'delete-multi') {
        const paths = d.kind === 'delete' ? [d.path] : d.paths
        // Multi-delete spans files and directories; Recursive + Force covers
        // both, so dirs need no per-path lookup.
        for (const p of paths) {
          await projectClient.rm(
            client,
            { Path: p, Recursive: true, Confirm: true, Force: true },
            { target: projectId },
          )
        }
        showToast(t('fileBrowser.toast.deleted', { count: paths.length }))
        cleanupDeletedPaths(paths)
        pruneFavorites(paths)
        // prune deleted paths out of the live selection and cut-source state
        const delSet = new Set(paths)
        setSelectedPaths(prev => new Set([...prev].filter(p => !delSet.has(p))))
        setLastSelectedPath(prev => (prev && delSet.has(prev) ? null : prev))
        if (selectedPath && delSet.has(selectedPath)) setAnchor(null)
      }
    } catch {
      showToast(t('fileBrowser.toast.operationFailed'))
    }
    refresh()
  }, [dialog, projectId, refresh, showToast, selectedPath, setAnchor, cleanupDeletedPaths, pruneFavorites, remapFavoritePath])

  /* --- item click (list/grid) --- */
  const handleItemClick = useCallback((e: React.MouseEvent, entry: FileEntry) => {
    const path = joinPath(currentPath, entry.Name)
    const isCtrl = e.ctrlKey || e.metaKey
    const isShift = e.shiftKey
    const isAlt = e.altKey

    // Alt+click: subtract the item from the current selection (no toggle-add).
    if (isAlt) {
      if (selectedPaths.has(path)) {
        const next = new Set(selectedPaths)
        next.delete(path)
        setSelectedPaths(next)
        if (next.size === 0) {
          setLastSelectedPath(null)
          setAnchor(null)
        }
      }
      return
    }

    if (isShift && lastSelectedPath && entries.length > 0) {
      const lastIdx = entries.findIndex(ent => joinPath(currentPath, ent.Name) === lastSelectedPath)
      const currentIdx = entries.findIndex(ent => ent.Name === entry.Name)
      if (lastIdx >= 0 && currentIdx >= 0) {
        const start = Math.min(lastIdx, currentIdx)
        const end = Math.max(lastIdx, currentIdx)
        const range = new Set<string>()
        for (let i = start; i <= end; i++) {
          range.add(joinPath(currentPath, entries[i]!.Name))
        }
        setSelectedPaths(range)
        return
      }
    }

    if (isCtrl) {
      const next = new Set(selectedPaths)
      if (next.has(path)) {
        next.delete(path)
        setSelectedPaths(next)
        setAnchor(null)
      } else {
        next.add(path)
        setSelectedPaths(next)
        setLastSelectedPath(path)
        setAnchor(path)
      }
      return
    }

    // normal click: single select and open/navigate
    setAnchor(path, true)
    if (entry.IsDir) {
      navigateTo(path)
    } else {
      onOpenFile?.(path, projectId)
    }
  }, [currentPath, navigateTo, onOpenFile, projectId, entries, lastSelectedPath, selectedPaths, setAnchor])

  /* --- tree item click (modifier-aware multi-select in the sidebar) --- */
  const handleTreeItemClick = useCallback((e: React.MouseEvent, path: string) => {
    if (e.altKey) {
      if (selectedPaths.has(path)) {
        const next = new Set(selectedPaths)
        next.delete(path)
        setSelectedPaths(next)
        if (next.size === 0) {
          setLastSelectedPath(null)
          setAnchor(null)
        }
      }
      return
    }
    if (e.ctrlKey || e.metaKey) {
      const next = new Set(selectedPaths)
      if (next.has(path)) {
        next.delete(path)
        setSelectedPaths(next)
        setAnchor(null)
      } else {
        next.add(path)
        setSelectedPaths(next)
        setLastSelectedPath(path)
        setAnchor(path)
      }
      return
    }
    // plain click (shift included — no linear range in the tree): single select
    setAnchor(path, true)
  }, [selectedPaths, setAnchor])

  /* --- context menu for list/grid items --- */
  const handleItemContextMenu = useCallback((e: React.MouseEvent, entry: FileEntry) => {
    e.preventDefault()
    e.stopPropagation()
    const path = joinPath(currentPath, entry.Name)
    if (!selectedPaths.has(path)) {
      setSelectedPaths(new Set([path]))
      setAnchor(path, true)
    }
    setLastSelectedPath(path)
    setMenuTarget({ name: entry.Name, path, isDir: entry.IsDir, x: e.clientX, y: e.clientY })
  }, [currentPath, selectedPaths, setAnchor])

  /* --- tree context menu --- */
  const handleTreeContextMenu = useCallback((e: React.MouseEvent, entry: MenuEntry) => {
    setMenuTarget({ ...entry, x: e.clientX, y: e.clientY })
  }, [])

  /* --- empty-area context menu (create in current dir) --- */
  const handleAreaContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    setMenuTarget({
      name: currentPath === '.' ? t('fileBrowser.rootLabel') : currentPath.split('/').pop() || '.',
      path: currentPath,
      isDir: true,
      x: e.clientX,
      y: e.clientY,
    })
  }, [currentPath])

  const crumbs = pathSegments(currentPath)
  const altDragHint = isWails()

  // No resolvable project yet (self-managed mode). While the host's projects
  // list is still loading — or a non-empty list exists but the persisted
  // target has not resolved yet — show the loading hint; only a settled empty
  // list falls through to the no-project placeholder.
  if (!projectId) {
    const resolving = projectsLoading || (projects?.length ?? 0) > 0
    return (
      <div className={`file-browser fb-no-project${className ? ` ${className}` : ''}`}>
        <div className="fb-no-project-hint">
          {resolving ? t('fileBrowser.loading') : t('fileBrowser.project.pickPrompt')}
        </div>
      </div>
    )
  }

  return (
    <div className={`file-browser${className ? ` ${className}` : ''}`} {...{ [OS_DROP_TARGET_ATTR]: '' }}>
      {/* Toolbar (merged with breadcrumb) */}
      <div className="fb-toolbar">
        {/* Group 0: Project picker (self-managed project mode only) */}
        {!projectIdProp && projects && projects.length > 0 && (
          <ProjectPicker projects={projects} value={projectId} onSelect={selectProject} />
        )}

        {/* Group 1: Navigation */}
        <button
          type="button"
          className="fb-view-btn"
          onClick={navigateBack}
          disabled={!canGoBack}
          style={!canGoBack ? { opacity: 0.3, cursor: 'default' } : undefined}
          title={t('fileBrowser.toolbar.back')}
        >
          <ArrowLeft size={15} />
        </button>
        <button
          type="button"
          className="fb-view-btn"
          onClick={navigateForward}
          disabled={!canGoForward}
          style={!canGoForward ? { opacity: 0.3, cursor: 'default' } : undefined}
          title={t('fileBrowser.toolbar.forward')}
        >
          <ArrowRight size={15} />
        </button>

        {/* Group 2: Divider */}
        <div className="fb-toolbar-divider" />

        {/* Group 3: Path bar */}
        <div className="fb-toolbar-path">
          {crumbs.map((seg, i) => {
            return (
            <React.Fragment key={seg.path}>
              {i > 0 && <ChevronRight size={11} className="fb-crumb-sep" />}
              <button
                type="button"
                className={`fb-crumb${i === crumbs.length - 1 ? ' current' : ''}${dragOverDir === seg.path ? ' drag-over' : ''}`}
                onClick={() => navigateTo(seg.path)}
                onDragOver={(e) => {
                  if (hasOsFiles(e.dataTransfer)) {
                    e.preventDefault()
                    e.dataTransfer.dropEffect = 'copy'
                    if (dragOverDir !== seg.path) setDragOverDir(seg.path)
                    return
                  }
                  if (hasForeignFileDrag(e.dataTransfer, 'local')) {
                    e.preventDefault()
                    e.dataTransfer.dropEffect = 'copy'
                    if (dragOverDir !== seg.path) setDragOverDir(seg.path)
                    return
                  }
                  if (dragSourcePath && !canMove(dragSourcePath, seg.path)) return
                  e.preventDefault()
                  e.dataTransfer.dropEffect = 'move'
                  if (dragOverDir !== seg.path) setDragOverDir(seg.path)
                }}
                onDragLeave={dragOverDir === seg.path ? () => setDragOverDir(null) : undefined}
                onDrop={(e) => {
                  e.preventDefault()
                  e.stopPropagation()
                  handleDirDrop(e, seg.path)
                }}
              >
                {seg.name}
              </button>
            </React.Fragment>
            )
          })}
        </div>

        {/* Group 4: Action buttons (icon only) */}
        <button type="button" className="fb-view-btn" onClick={() => handleNewFile(currentPath)} title={t('fileBrowserContextMenu.newFile')}>
          <FilePlus size={14} />
        </button>
        <button type="button" className="fb-view-btn" onClick={() => handleNewFolder(currentPath)} title={t('fileBrowserContextMenu.newFolder')}>
          <FolderPlus size={14} />
        </button>
        <button
          type="button"
          className="fb-view-btn"
          onClick={refresh}
          title={t('fileBrowser.toolbar.refresh')}
        >
          <RefreshCw size={14} />
        </button>
        <button
          type="button"
          className={`fb-view-btn${showHidden ? ' active' : ''}`}
          onClick={() => setShowHidden(v => !v)}
          title={t('fileBrowser.toolbar.showHidden')}
        >
          <Eye size={14} />
        </button>

        {/* Group 5: Divider */}
        <div className="fb-toolbar-divider" />

        {/* Group 6: View switch */}
        <div className="fb-view-switch">
          <button
            type="button"
            className={`fb-view-btn${treeSidebarOpen ? ' active' : ''}`}
            onClick={() => setTreeSidebarOpen(v => !v)}
            title={t('fileBrowser.toolbar.treeSidebar')}
          >
            <ListTree size={15} />
          </button>
          <button
            type="button"
            className={`fb-view-btn${view === 'list' ? ' active' : ''}`}
            onClick={() => setView('list')}
            title={t('fileBrowser.toolbar.listView')}
          >
            <List size={15} />
          </button>
          <button
            type="button"
            className={`fb-view-btn${view === 'grid' ? ' active' : ''}`}
            onClick={() => setView('grid')}
            title={t('fileBrowser.toolbar.gridView')}
          >
            <LayoutGrid size={15} />
          </button>
        </div>
      </div>

      {/* Favorites toolbar: chips for starred paths, overflow collapses into a "+N" menu */}
      {favorites.length > 0 && (
        <FavoritesBar
          favorites={favorites}
          onOpenEntry={(path, isDir) => {
            if (isDir) navigateTo(path)
            else onOpenFile?.(path, projectId)
          }}
          onRemove={(path) => {
            const fav = favorites.find(f => f.path === path)
            if (fav) toggleFavorite(fav)
          }}
        />
      )}

      {/* Content */}
      <div
        className="fb-content"
        onContextMenu={handleAreaContextMenu}
        onDragOver={(e) => {
          // OS file drop (from OS/desktop) onto the content area
          if (hasOsFiles(e.dataTransfer)) {
            e.preventDefault()
            e.dataTransfer.dropEffect = 'copy'
            return
          }
          // cross-panel drop (remote → local) onto empty area
          if (hasForeignFileDrag(e.dataTransfer, 'local')) {
            e.preventDefault()
            e.dataTransfer.dropEffect = 'copy'
            return
          }
          // allow drop onto empty area → move into current directory
          if (dragSourcePath && !canMove(dragSourcePath, currentPath)) return
          e.preventDefault()
          e.dataTransfer.dropEffect = 'move'
        }}
        onDrop={(e) => {
          if (hasForeignFileDrag(e.dataTransfer, 'local') || dragSourcePath) {
            e.preventDefault()
            handleDirDrop(e, currentPath)
            return
          }
          // OS drop: always to current dir
          if (hasOsFiles(e.dataTransfer)) {
            e.preventDefault()
            void handleOsDrop(e.dataTransfer, currentPath)
          }
        }}
      >
        {treeSidebarOpen && (
          <div className="fb-tree-sidebar">
            <TreeView
              projectId={projectId}
              reloadKey={treeReloadKey}
              showHidden={showHidden}
              selectedPath={selectedPath}
              selectedPaths={selectedPaths}
              onOpenFile={onOpenFile ? (p) => { onOpenFile(p, projectId) } : undefined}
              onSelect={handleTreeItemClick}
              onNavigate={(path) => { navigateToWithHistory(path) }}
              onContextMenu={handleTreeContextMenu}
              dragSourcePath={dragSourcePath}
              dragOverDir={dragOverDir}
              cutSourcePaths={cutSourcePaths}
              favoritePaths={favoriteSet}
              onToggleFavorite={toggleFavorite}
              onItemDragStart={handleItemDragStart}
              onItemDragEnd={handleItemDragEnd}
              onDirDrop={handleDirDrop}
              onDragOverDirChange={setDragOverDir}
            />
          </div>
        )}
        <div className="fb-main-content" ref={mainContentRef}>
          {loading ? (
            <div className="fb-loading">{t('fileBrowser.loading')}</div>
          ) : entries.length === 0 ? (
            <div className="fb-empty">{t('fileBrowser.empty.folderEmpty')}</div>
          ) : view === 'list' ? (
          <div className="fb-list">
            {entries.map(entry => {
              const path = joinPath(currentPath, entry.Name)
              return (
                <div
                  key={entry.Name}
                  className={`fb-list-row${selectedPaths.has(path) ? ' selected' : ''}${entry.IsDir && dragOverDir === path ? ' drag-over' : ''}${dragSourcePath === path ? ' dragging' : ''}${cutSourcePaths.has(path) ? ' cut-source' : ''}`}
                  title={altDragHint ? `${path}\n\n${t('fileBrowser.dragOutHint')}` : path}
                >
                  <div
                    className="fb-item-hit"
                    data-mq-path={path}
                    draggable
                    onClick={(e) => handleItemClick(e, entry)}
                    onContextMenu={e => handleItemContextMenu(e, entry)}
                    onDragStart={(e) => {
                      if (handleRowDragStart(e, projectId, path)) return
                      e.dataTransfer.setData('text/plain', path)
                      setFileDragPayload(e.dataTransfer, { origin: 'local', projectId, path, name: entry.Name, isDir: entry.IsDir })
                      e.dataTransfer.effectAllowed = 'copyMove'
                      handleItemDragStart(path)
                    }}
                    onDragEnd={handleItemDragEnd}
                    onDragOver={entry.IsDir ? (e) => {
                      if (hasOsFiles(e.dataTransfer)) {
                        e.preventDefault()
                        e.dataTransfer.dropEffect = 'copy'
                        if (dragOverDir !== path) setDragOverDir(path)
                        return
                      }
                      if (hasForeignFileDrag(e.dataTransfer, 'local')) {
                        e.preventDefault()
                        e.dataTransfer.dropEffect = 'copy'
                        if (dragOverDir !== path) setDragOverDir(path)
                        return
                      }
                      if (dragSourcePath && !canMove(dragSourcePath, path)) return
                      e.preventDefault()
                      e.dataTransfer.dropEffect = 'move'
                      if (dragOverDir !== path) setDragOverDir(path)
                    } : undefined}
                    onDragLeave={entry.IsDir && dragOverDir === path ? () => setDragOverDir(null) : undefined}
                    onDrop={entry.IsDir ? (e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      handleDirDrop(e, path)
                    } : undefined}
                  >
                    <span className="fb-list-icon">
                      {entry.IsDir ? getFolderIcon(false) : getComposedFileIcon(entry.Name)}
                    </span>
                    <span className="fb-list-name">{entry.Name}</span>
                    {!entry.IsDir && entry.Size > 0 && (
                      <span className="fb-list-size">{formatSize(entry.Size)}</span>
                    )}
                    {entry.ModTime && (
                      <span className="fb-list-mtime">{formatMtime(entry.ModTime)}</span>
                    )}
                  </div>
                  {favoriteSet.has(path) && (
                    <button
                      type="button"
                      className="fb-star-badge"
                      title={t('fileBrowserContextMenu.unfavorite')}
                      onClick={(e) => { e.stopPropagation(); toggleFavorite({ path, isDir: entry.IsDir }) }}
                    >
                      <Star size={10} fill="currentColor" strokeWidth={0} />
                    </button>
                  )}
                </div>
              )
            })}
          </div>
        ) : (
          <div className="fb-grid">
            {entries.map(entry => {
              const path = joinPath(currentPath, entry.Name)
              return (
                <div
                  key={entry.Name}
                  className={`fb-grid-item${selectedPaths.has(path) ? ' selected' : ''}${entry.IsDir && dragOverDir === path ? ' drag-over' : ''}${dragSourcePath === path ? ' dragging' : ''}${cutSourcePaths.has(path) ? ' cut-source' : ''}`}
                  title={altDragHint ? `${path}\n\n${t('fileBrowser.dragOutHint')}` : path}
                >
                  <div
                    className="fb-item-hit"
                    data-mq-path={path}
                    draggable
                    onClick={(e) => handleItemClick(e, entry)}
                    onContextMenu={e => handleItemContextMenu(e, entry)}
                    onDragStart={(e) => {
                      if (handleRowDragStart(e, projectId, path)) return
                      e.dataTransfer.setData('text/plain', path)
                      setFileDragPayload(e.dataTransfer, { origin: 'local', projectId, path, name: entry.Name, isDir: entry.IsDir })
                      e.dataTransfer.effectAllowed = 'copyMove'
                      handleItemDragStart(path)
                    }}
                    onDragEnd={handleItemDragEnd}
                    onDragOver={entry.IsDir ? (e) => {
                      if (hasOsFiles(e.dataTransfer)) {
                        e.preventDefault()
                        e.dataTransfer.dropEffect = 'copy'
                        if (dragOverDir !== path) setDragOverDir(path)
                        return
                      }
                      if (hasForeignFileDrag(e.dataTransfer, 'local')) {
                        e.preventDefault()
                        e.dataTransfer.dropEffect = 'copy'
                        if (dragOverDir !== path) setDragOverDir(path)
                        return
                      }
                      if (dragSourcePath && !canMove(dragSourcePath, path)) return
                      e.preventDefault()
                      e.dataTransfer.dropEffect = 'move'
                      if (dragOverDir !== path) setDragOverDir(path)
                    } : undefined}
                    onDragLeave={entry.IsDir && dragOverDir === path ? () => setDragOverDir(null) : undefined}
                    onDrop={entry.IsDir ? (e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      handleDirDrop(e, path)
                    } : undefined}
                  >
                    <span className="fb-grid-icon">
                      {entry.IsDir ? getFolderIcon(false) : getComposedFileIcon(entry.Name)}
                    </span>
                    <span className="fb-grid-name">{entry.Name}</span>
                  </div>
                  {favoriteSet.has(path) && (
                    <button
                      type="button"
                      className="fb-star-badge"
                      title={t('fileBrowserContextMenu.unfavorite')}
                      onClick={(e) => { e.stopPropagation(); toggleFavorite({ path, isDir: entry.IsDir }) }}
                    >
                      <Star size={10} fill="currentColor" strokeWidth={0} />
                    </button>
                  )}
                </div>
              )
            })}
          </div>
          )}
        </div>
      </div>

      {/* Context menu */}
      <FileBrowserContextMenu
        target={menuTarget}
        onClose={() => setMenuTarget(null)}
        onOpen={handleMenuOpen}
        onNewFile={handleNewFile}
        onNewFolder={handleNewFolder}
        onCopy={handleCopy}
        onCut={handleCut}
        onPaste={handlePaste}
        canPaste={menuTarget?.isDir === true && clipboardRef.current !== null}
        onCopyPath={handleCopyPath}
        onReveal={handleReveal}
        onRename={handleRename}
        onDelete={handleDelete}
        onRefresh={refresh}
        onDownload={handleDownload}
        onToggleFavorite={(entry) => toggleFavorite(entry)}
        favorited={menuTarget ? favoriteSet.has(menuTarget.path) : false}
        selectedCount={selectedPaths.size > 0 ? selectedPaths.size : 1}
      />

      {/* Dialog — portaled to body so `position: fixed` centers on the viewport
          even when an ancestor creates a containing block (transform/filter). */}
      {dialog && createPortal(
        <FileBrowserDialog
          state={dialog}
          onConfirm={handleDialogConfirm}
          onCancel={() => setDialog(null)}
        />,
        document.body,
      )}

      {/* Toast */}
      {toast && <div className="fb-toast">{toast}</div>}
    </div>
  )
}

export default FileBrowser
