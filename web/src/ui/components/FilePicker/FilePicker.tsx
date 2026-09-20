import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import type { ReactNode } from 'react'
import { ChevronRight, Folder, File, ArrowUp, X, List, LayoutGrid, Table, HardDrive, Home } from 'lucide-react'
import * as filesystem from '../../../gen-clients/filesystem/client'
import type { FileEntry } from '../../../gen-clients/system/types'
import { client } from '../../../application/generated-client'
import { hasWailsBindings, selectFolder } from '../../../application/wails-bridge'
import './FilePicker.css'
import { useI18n } from '../../../i18n'

export type PickerMode = 'folder' | 'file' | 'multi-file'
export type ViewMode = 'list' | 'grid' | 'details'

export interface FilePickerProps {
  open: boolean
  mode: PickerMode
  onSelect: (paths: string[]) => void
  onCancel: () => void
  /** Starting directory. Ignored in folder mode when Wails native dialog is used
   *  (the native picker chooses its own initial location). */
  initialPath?: string
  /** Extension whitelist e.g. ['.ts', '.tsx']. Ignored in 'folder' mode.
   *  Empty / undefined disables filtering. */
  accept?: string[]
  /** Override default title. */
  title?: string
  /** Default view mode. */
  defaultView?: ViewMode
}

function joinPath(parent: string, name: string): string {
  if (!parent) return name
  return parent.replace(/[/\\]+$/, '') + '/' + name
}

// loadedPath === ROOTS_VIEW renders the filesystem top level (home directory +
// volume roots) instead of a directory listing. The browser cannot enumerate
// volumes, so those entries come from filesystem.roots.
const ROOTS_VIEW = ''

function getParentPath(path: string): string {
  const normalized = path.replace(/[/\\]+$/, '')
  if (!normalized) return ROOTS_VIEW
  // Windows drive root reached ("D:\" / "D:/"): the level above is the roots view.
  if (/^[A-Za-z]:$/.test(normalized)) return ROOTS_VIEW
  const lastSep = Math.max(normalized.lastIndexOf('/'), normalized.lastIndexOf('\\'))
  if (lastSep < 0) return ROOTS_VIEW
  if (lastSep === 0) return normalized.slice(0, 1)
  let parent = normalized.slice(0, lastSep)
  // A slice can leave a bare drive letter ("D:"), which the server would
  // resolve as a drive-relative path — restore the root slash.
  if (/^[A-Za-z]:$/.test(parent)) parent += '/'
  return parent
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso)
    return d.toLocaleString()
  } catch {
    return iso
  }
}

export function FilePicker({
  open,
  mode,
  onSelect,
  onCancel,
  initialPath,
  accept,
  title,
  defaultView = 'list',
}: FilePickerProps) {
  const [loadedPath, setLoadedPath] = useState('')
  const [pathInput, setPathInput] = useState(initialPath ?? '')
  const [parentPath, setParentPath] = useState('')
  const [entries, setEntries] = useState<FileEntry[]>([])
  const [rootEntries, setRootEntries] = useState<FileEntry[] | null>(null)
  const [homePath, setHomePath] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [showAll, setShowAll] = useState(false)
  const [viewMode, setViewMode] = useState<ViewMode>(defaultView)
  const { t } = useI18n()

  const defaultTitle = (mode: PickerMode): string => {
    switch (mode) {
      case 'folder': return t('filePicker.title.folder')
      case 'file': return t('filePicker.title.file')
      case 'multi-file': return t('filePicker.title.files')
    }
  }

  const VIEW_MODES: { key: ViewMode; icon: ReactNode; label: string }[] = [
    { key: 'list', icon: <List size={14} />, label: t('filePicker.view.list') },
    { key: 'grid', icon: <LayoutGrid size={14} />, label: t('filePicker.view.grid') },
    { key: 'details', icon: <Table size={14} />, label: t('filePicker.view.details') },
  ]

  // Hold latest callbacks in a ref so the open/mode effect doesn't re-run
  // when callers pass inline closures.
  const onSelectRef = useRef(onSelect)
  const onCancelRef = useRef(onCancel)
  useEffect(() => {
    onSelectRef.current = onSelect
    onCancelRef.current = onCancel
  }, [onSelect, onCancel])

  // Prevent StrictMode double-fire from calling selectFolder() twice.
  const nativeTriggeredRef = useRef(false)

  const loadPath = useCallback((path: string) => {
    setLoading(true)
    setError('')
    filesystem.listJson(client, { Path: path })
      .then(resp => {
        setLoadedPath(path)
        setPathInput(path)
        setParentPath(getParentPath(path))
        setEntries(resp.Items)
      })
      .catch(() => setError(t('filePicker.listFailed')))
      .finally(() => setLoading(false))
  }, [])

  // Enter the roots view (home + volume roots). Falls back to the server's
  // default root when the roots callable is unavailable.
  const loadRootsView = useCallback(() => {
    setLoading(true)
    setError('')
    filesystem.roots(client)
      .then(resp => {
        const items: FileEntry[] = []
        if (resp.Home) {
          items.push({ Name: resp.Home, IsDir: true, Size: 0, ModTime: '' })
          setHomePath(resp.Home)
        }
        for (const r of resp.Roots) {
          items.push({ Name: r, IsDir: true, Size: 0, ModTime: '' })
        }
        setRootEntries(items)
        setLoadedPath(ROOTS_VIEW)
        setPathInput('')
        setParentPath('')
        setEntries([])
      })
      .catch(() => loadPath(''))
      .finally(() => setLoading(false))
  }, [loadPath])

  // Delegate folder mode to native Wails dialog when available.
  useEffect(() => {
    if (!open) {
      nativeTriggeredRef.current = false
      return
    }
    if (mode === 'folder' && hasWailsBindings()) {
      if (nativeTriggeredRef.current) return
      nativeTriggeredRef.current = true
      selectFolder()
        .then(p => {
          if (p) onSelectRef.current([p])
          else onCancelRef.current()
        })
        .catch(() => onCancelRef.current())
      return
    }
    setSelected(new Set())
    setShowAll(false)
    if (initialPath) loadPath(initialPath)
    else loadRootsView()
  }, [open, mode, initialPath, loadPath, loadRootsView])

  const inRootsView = rootEntries !== null && loadedPath === ROOTS_VIEW

  const filtered = useMemo(() => {
    return entries.filter(e => {
      if (e.IsDir) return true
      if (mode === 'folder') return false
      if (!accept || accept.length === 0 || showAll) return true
      const lower = e.Name.toLowerCase()
      return accept.some(ext => lower.endsWith(ext.toLowerCase()))
    })
  }, [entries, mode, accept, showAll])

  const handleGoUp = () => {
    if (inRootsView) return
    if (parentPath) loadPath(parentPath)
    else loadRootsView()
  }

  const handleEntryClick = (entry: FileEntry) => {
    if (inRootsView) {
      loadPath(entry.Name)
      return
    }
    const entryPath = joinPath(loadedPath, entry.Name)
    if (entry.IsDir) {
      loadPath(entryPath)
      return
    }
    if (mode === 'file') {
      onSelect([entryPath])
      return
    }
    if (mode === 'multi-file') {
      setSelected(prev => {
        const next = new Set(prev)
        if (next.has(entryPath)) next.delete(entryPath)
        else next.add(entryPath)
        return next
      })
    }
  }

  const handlePrimaryAction = () => {
    if (mode === 'folder') {
      onSelect([loadedPath])
      return
    }
    if (mode === 'multi-file') {
      onSelect(Array.from(selected))
      return
    }
  }

  const primaryDisabled = (() => {
    if (mode === 'folder') return !loadedPath
    if (mode === 'multi-file') return selected.size === 0
    return true // file mode: primary button hidden, selection is single-click
  })()

  const primaryLabel = (() => {
    if (mode === 'folder') return t('filePicker.selectFolder')
    if (mode === 'multi-file') return t('filePicker.selectFiles', { count: selected.size })
    return ''
  })()

  if (!open) return null
  // In Wails folder mode the native dialog handles everything — skip the
  // web-based picker entirely (including the first render before the effect
  // fires) to avoid a flash of the web picker and double-callback issues.
  if (mode === 'folder' && hasWailsBindings()) return null

  const hasAcceptFilter = !!accept && accept.length > 0 && mode !== 'folder'

  const footerLabel = (() => {
    if (mode === 'multi-file') {
      if (selected.size === 0) return t('filePicker.noneSelected')
      return t('filePicker.filesSelected', { count: selected.size })
    }
    if (inRootsView) return t('filePicker.computer')
    return loadedPath || t('filePicker.noneSelected')
  })()

  // Entries shown in the body: roots-view entries in the roots view, the
  // filtered directory listing otherwise.
  const viewEntries = inRootsView ? rootEntries! : filtered

  const entryPathOf = (entry: FileEntry) =>
    inRootsView ? entry.Name : joinPath(loadedPath, entry.Name)

  const renderEntryIcon = (entry: FileEntry, size: number) => {
    if (inRootsView) {
      return entry.Name === homePath ? <Home size={size} /> : <HardDrive size={size} />
    }
    return entry.IsDir ? <Folder size={size} /> : <File size={size} />
  }

  const renderList = () => (
    <div className="fp-list">
      {loading ? (
        <div className="fp-empty">{t('filePicker.loading')}</div>
      ) : error ? (
        <div className="fp-empty fp-error">{error}</div>
      ) : viewEntries.length === 0 ? (
        <div className="fp-empty">
          {mode === 'folder' ? t('filePicker.noSubdirs') : t('filePicker.noMatch')}
        </div>
      ) : (
        viewEntries.map(entry => {
          const entryPath = entryPathOf(entry)
          const isChecked = selected.has(entryPath)
          return (
            <button
              key={entryPath}
              className={`fp-item ${isChecked ? 'fp-item-selected' : ''}`}
              onClick={() => handleEntryClick(entry)}
              onDoubleClick={() => {
                if (entry.IsDir) return
                if (mode === 'file') onSelect([entryPath])
              }}
            >
              {mode === 'multi-file' && !entry.IsDir && (
                <input
                  type="checkbox"
                  checked={isChecked}
                  onChange={() => {}}
                  onClick={e => e.stopPropagation()}
                  className="fp-item-check"
                />
              )}
              {renderEntryIcon(entry, 16)}
              <span className="fp-item-name">{entry.Name}</span>
              {entry.IsDir && <ChevronRight size={14} className="fp-item-arrow" />}
            </button>
          )
        })
      )}
    </div>
  )

  const renderGrid = () => (
    <div className="fp-grid">
      {loading ? (
        <div className="fp-empty">{t('filePicker.loading')}</div>
      ) : error ? (
        <div className="fp-empty fp-error">{error}</div>
      ) : viewEntries.length === 0 ? (
        <div className="fp-empty">
          {mode === 'folder' ? t('filePicker.noSubdirs') : t('filePicker.noMatch')}
        </div>
      ) : (
        viewEntries.map(entry => {
          const entryPath = entryPathOf(entry)
          const isChecked = selected.has(entryPath)
          return (
            <button
              key={entryPath}
              className={`fp-grid-item ${isChecked ? 'fp-item-selected' : ''}`}
              onClick={() => handleEntryClick(entry)}
              onDoubleClick={() => {
                if (entry.IsDir) return
                if (mode === 'file') onSelect([entryPath])
              }}
              title={entry.Name}
            >
              {mode === 'multi-file' && !entry.IsDir && (
                <input
                  type="checkbox"
                  checked={isChecked}
                  onChange={() => {}}
                  onClick={e => e.stopPropagation()}
                  className="fp-grid-check"
                />
              )}
              <div className="fp-grid-icon">
                {renderEntryIcon(entry, 32)}
              </div>
              <span className="fp-grid-name">{entry.Name}</span>
            </button>
          )
        })
      )}
    </div>
  )

  const renderDetails = () => (
    <div className="fp-details">
      <div className="fp-details-header">
        <span className="fp-details-col-name">{t('filePicker.col.name')}</span>
        <span className="fp-details-col-size">{t('filePicker.col.size')}</span>
        <span className="fp-details-col-time">{t('filePicker.col.modified')}</span>
      </div>
      <div className="fp-details-body">
        {loading ? (
          <div className="fp-empty">{t('filePicker.loading')}</div>
        ) : error ? (
          <div className="fp-empty fp-error">{error}</div>
        ) : viewEntries.length === 0 ? (
          <div className="fp-empty">
            {mode === 'folder' ? t('filePicker.noSubdirs') : t('filePicker.noMatch')}
          </div>
        ) : (
          viewEntries.map(entry => {
            const entryPath = entryPathOf(entry)
            const isChecked = selected.has(entryPath)
            return (
              <button
                key={entryPath}
                className={`fp-details-row ${isChecked ? 'fp-item-selected' : ''}`}
                onClick={() => handleEntryClick(entry)}
                onDoubleClick={() => {
                  if (entry.IsDir) return
                  if (mode === 'file') onSelect([entryPath])
                }}
              >
                <span className="fp-details-col-name">
                  {mode === 'multi-file' && !entry.IsDir && (
                    <input
                      type="checkbox"
                      checked={isChecked}
                      onChange={() => {}}
                      onClick={e => e.stopPropagation()}
                      className="fp-item-check"
                    />
                  )}
                  {renderEntryIcon(entry, 16)}
                  <span className="fp-details-name-text">{entry.Name}</span>
                </span>
                <span className="fp-details-col-size">
                  {entry.IsDir ? t('filePicker.dash') : formatSize(entry.Size)}
                </span>
                <span className="fp-details-col-time">
                  {formatTime(entry.ModTime)}
                </span>
              </button>
            )
          })
        )}
      </div>
    </div>
  )

  return (
    <div className="fp-overlay" onClick={onCancel}>
      <div className="fp-dialog" onClick={e => e.stopPropagation()}>
        <div className="fp-header">
          <span className="fp-title">{title ?? defaultTitle(mode)}</span>
          <button className="fp-close" onClick={onCancel}>
            <X size={16} />
          </button>
        </div>
        <div className="fp-path-bar">
          <button
            className="fp-up-btn"
            onClick={handleGoUp}
            disabled={inRootsView}
            title={t('filePicker.goUp')}
          >
            <ArrowUp size={14} />
          </button>
          <input
            className="fp-path-input"
            value={pathInput}
            onChange={e => setPathInput(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') loadPath(pathInput) }}
            placeholder={t('filePicker.pathPlaceholder')}
          />
          <button className="fp-go-btn" onClick={() => loadPath(pathInput)}>
            {t('filePicker.go')}
          </button>
        </div>
        <div className="fp-toolbar">
          <div className="fp-view-toggle">
            {VIEW_MODES.map(vm => (
              <button
                key={vm.key}
                className={`fp-view-btn ${viewMode === vm.key ? 'fp-view-btn-active' : ''}`}
                onClick={() => setViewMode(vm.key)}
                title={vm.label}
              >
                {vm.icon}
              </button>
            ))}
          </div>
          <span className="fp-count">{t('filePicker.itemsCount', { count: viewEntries.length })}</span>
        </div>
        {viewMode === 'list' && renderList()}
        {viewMode === 'grid' && renderGrid()}
        {viewMode === 'details' && renderDetails()}
        {hasAcceptFilter && (
          <div className="fp-filter-bar">
            <label className="fp-filter-toggle">
              <input
                type="checkbox"
                checked={showAll}
                onChange={e => setShowAll(e.target.checked)}
              />
              <span>{t('filePicker.showAll')}</span>
            </label>
            <span className="fp-filter-hint">
              {showAll ? t('filePicker.all') : accept!.join(' ')}
            </span>
          </div>
        )}
        <div className="fp-footer">
          <span className="fp-selected-path" title={footerLabel}>
            {footerLabel}
          </span>
          <div className="fp-actions">
            <button className="fp-cancel-btn" onClick={onCancel}>{t('filePicker.cancel')}</button>
            {mode !== 'file' && (
              <button
                className="fp-select-btn"
                onClick={handlePrimaryAction}
                disabled={primaryDisabled}
              >
                {primaryLabel}
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
