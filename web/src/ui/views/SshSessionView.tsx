import { useState, useCallback, useEffect, useRef, useLayoutEffect, useMemo } from 'react'
import { Terminal as XtermTerminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'

import '@xterm/xterm/css/xterm.css'
import {
  Terminal, X,
  ArrowUp, FolderOpen, HardDrive, Microchip, MemoryStick, Activity, ArrowDownWideNarrow, Network, RefreshCw,
  PanelBottomClose, PanelBottomOpen,
  LayoutGrid, List, ChevronRight, ChevronLeft, ChevronDown, ListTree,
  FilePlus, FolderPlus, Copy, Pencil, Trash2, Download, Upload, Scissors, FileCode,
  History, Search, ClipboardPaste, ChevronUp, Eraser, Zap,
} from 'lucide-react'
import { client } from '../../application/generated-client'
import * as sshmanagerShell from '../../gen-clients/sshmanager/client'
import * as sshmanagerFile from '../../gen-clients/sshmanager/client'
import * as sshmanagerStatus from '../../gen-clients/sshmanager/client'
import * as projectClient from '../../gen-clients/project/client'
import type { SshFileEntry, SshStatus } from '../../gen-types/sshmanager'
import { getComposedFileIcon, getFolderIcon } from '../panels/file-icons'
import { ResizeHandle } from '../components/ResizeHandle'
import { useMenuDismiss } from '../ai/hooks/useMenuDismiss'
import { useMarqueeSelection, type MarqueeSelectionResult } from '../ai/hooks/useMarqueeSelection'
import { useI18n } from '../../i18n'
import { SshFileTextEditor } from './SshFileTextEditor'
import { shouldOpenForEdit } from './sshFileEditGate'
import {
  setFileDragPayload,
  hasForeignFileDrag,
  getForeignFileDragPayload,
  type FileDragPayload,
} from '../file-dnd'
import {
  collectOsDroppedEntries,
  startOsFileDragOut,
  bytesToBase64,
  type OsDroppedEntry,
  OS_DROP_TARGET_ATTR,
} from '../os-file-dnd'
import { isWails } from '../../application/runtime'
import {
  loadSshSessionUIPrefs,
  saveSshSessionUIPrefs,
  type SshFileViewMode,
  type SshBottomTab,
  type SshSessionUIPrefs,
} from '../../application/ssh-ui-state'
import { SshCommandPanel } from './SshCommandPanel'
import {
  FILE_COLUMN_ORDER,
  FILE_COLUMN_LABELS,
  FILE_COLUMN_MIN,
  FILE_COLUMN_DEFAULT,
  clampColumnWidth,
  buildFileGridCols,
  type FileColumnKey,
  type FileColumnWidths,
} from './sshFileColumns'
import { formatSshModified } from './sshFileTime'
import './SshSessionView.css'

/**
 * Decoration colours applied by the xterm SearchAddon. The terminal body uses a
 * dark canvas (#000000) so the palette is tuned for contrast: dim amber for
 * non-active matches, bright amber for the active match, with overview-ruler
 * markers so matches are visible in the scrollbar.
 */
// Below these sizes the container is effectively hidden (e.g. the right
// sidebar is collapsed to width 0 but padding still counts toward
// clientWidth). Fitting then would clamp cols to 2 and reflow the buffer.
const MIN_FIT_WIDTH_PX = 64
const MIN_FIT_HEIGHT_PX = 24

const SEARCH_DECORATIONS = {
  matchBackground: '#4d3a00',
  matchBorder: '#888800',
  matchOverviewRuler: '#aaaa00',
  activeMatchBackground: '#cc8800',
  activeMatchBorder: '#ffaa00',
  activeMatchColorOverviewRuler: '#ffaa00',
}

function formatError(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e)
}

/**
 * Classify a backend archive error string into a localized i18n key for known
 * failure patterns (size limit exceeded, decompression failure). Returns null
 * for unrecognised errors so the caller can show the raw message.
 */
function classifyArchiveUploadError(msg: string): 'sshTransfer.archiveTooLarge' | 'sshTransfer.decompressFailed' | null {
  const lower = msg.toLowerCase()
  if (lower.includes('exceeds') && (lower.includes('limit') || lower.includes('byte') || lower.includes('entry'))) {
    return 'sshTransfer.archiveTooLarge'
  }
  if (lower.includes('gzip') || lower.includes('tar') || lower.includes('decompress') || lower.includes('extract') || lower.includes('base64')) {
    return 'sshTransfer.decompressFailed'
  }
  return null
}

function formatBytes(n: number): string {
  if (n === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(n) / Math.log(k))
  return parseFloat((n / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

function memPercent(used: number, total: number): number {
  return total > 0 ? Math.round((used / total) * 100) : 0
}

function Sparkline({ data, max, color, height = 32 }: { data: number[]; max: number; color: string; height?: number }) {
  if (data.length < 2) return <div style={{ height }} />
  const w = 100
  const pts = data.map((v, i) => {
    const x = (i / (data.length - 1)) * w
    const y = height - (Math.min(v, max) / max) * height
    return `${x.toFixed(1)},${y.toFixed(1)}`
  })
  const areaPts = `0,${height} ${pts.join(' ')} ${w},${height}`
  return (
    <svg viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none" style={{ width: '100%', height }}>
      <polygon points={areaPts} fill={color} fillOpacity={0.15} />
      <polyline points={pts.join(' ')} fill="none" stroke={color} strokeWidth={1.5} strokeLinejoin="round" />
    </svg>
  )
}

function joinRemotePath(dir: string, name: string): string {
  if (dir === '/') return '/' + name
  return dir + '/' + name
}

function remoteParentOf(path: string): string {
  if (path === '/') return '/'
  const idx = path.lastIndexOf('/')
  if (idx <= 0) return '/'
  return path.slice(0, idx)
}

/** Synthetic SshFileEntry for the tree's root "/" row. */
const ROOT_ENTRY: SshFileEntry = { Name: '/', FullPath: '/', IsDir: true, Size: 0, Mode: '', Modified: '' }

/** Decode a base64 string into a Blob and trigger a browser download.
 *  Returns the decoded byte length so callers can report transfer size. */
function downloadBase64Blob(base64: string, fileName: string): number {
  const byteChars = atob(base64)
  const bytes = new Uint8Array(byteChars.length)
  for (let i = 0; i < byteChars.length; i++) bytes[i] = byteChars.charCodeAt(i)
  const blob = new Blob([bytes])
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = fileName
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
  return bytes.length
}

/** Generate a unique remote temp file name under /tmp for bulk tar.gz. */
function bulkTempArchiveName(): string {
  const uid = (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function')
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
  return `/tmp/sporemind_bulk_${uid}.tar.gz`
}

/** SshFileEntry augmented with click coordinates for the context menu. */
interface SshMenuTarget {
  entry: SshFileEntry
  x: number
  y: number
}

/** One tracked file transfer shown in the floating transfer panel. */
interface SshTransfer {
  id: number
  name: string
  direction: 'download' | 'upload'
  status: 'running' | 'done' | 'error' | 'cancelled'
  size?: number
  error?: string
  abortController?: AbortController
}

type SshDialogState =
  | { kind: 'newfile'; dir: string }
  | { kind: 'newfolder'; dir: string }
  | { kind: 'rename'; entry: SshFileEntry }
  | { kind: 'delete'; entries: SshFileEntry[] }

/* ===================== column width resizer ===================== */

interface ColumnResizerProps {
  /** Width of the column at drag start (px). Re-read on every render. */
  startWidth: number
  /** Hard minimum; the column cannot be dragged narrower. */
  minWidth: number
  /** Receives the next clamped width on every pointer move. */
  onWidth: (next: number) => void
  /** Notified when a drag starts/ends so the host can suppress text selection. */
  onDragStateChange: (active: boolean) => void
}

/**
 * Pointer-driven column separator rendered at the right edge of a header cell.
 * Uses document-level pointermove listeners (with pointer capture) so the drag
 * keeps tracking even when the cursor leaves the thin handle. It calls
 * `preventDefault` + `stopPropagation` on pointerdown so it never initiates an
 * HTML5 row drag, and the host toggles a `resizing` class to disable text
 * selection while dragging.
 */
function ColumnResizer({ startWidth, minWidth, onWidth, onDragStateChange }: ColumnResizerProps) {
  const drag = useRef<{ startX: number; startW: number } | null>(null)

  const onPointerDown = useCallback((e: React.PointerEvent) => {
    e.preventDefault()
    e.stopPropagation()
    const el = e.currentTarget as HTMLElement
    try { el.setPointerCapture(e.pointerId) } catch { /* pointer already captured */ }
    drag.current = { startX: e.clientX, startW: startWidth }
    onDragStateChange(true)

    const onMove = (ev: PointerEvent) => {
      const d = drag.current
      if (!d) return
      const next = Math.max(minWidth, d.startW + (ev.clientX - d.startX))
      onWidth(Math.round(next))
    }
    const onUp = () => {
      drag.current = null
      onDragStateChange(false)
      document.removeEventListener('pointermove', onMove)
      document.removeEventListener('pointerup', onUp)
      document.removeEventListener('pointercancel', onUp)
    }
    document.addEventListener('pointermove', onMove)
    document.addEventListener('pointerup', onUp)
    document.addEventListener('pointercancel', onUp)
  }, [startWidth, minWidth, onWidth, onDragStateChange])

  return (
    <span
      className="ssh-col-resizer"
      role="separator"
      aria-orientation="vertical"
      onPointerDown={onPointerDown}
    />
  )
}

export interface SshSessionViewProps {
  sessionId: string
  hostId: string
  hostName: string
  /**
   * Open a remote file for editing in the host layout (e.g. as a right-panel
   * editor tab). When absent, the built-in modal editor is used instead.
   */
  onOpenRemoteFile?: (entry: SshFileEntry) => void
  /**
   * Called whenever the active session id changes (e.g. after reconnect).
   * The parent tab uses this so its close handler always closes the live
   * session rather than a stale one. KeepAlive-safe: this is a state-change
   * notification, NOT an unmount-close side effect.
   */
  onSessionIdChange?: (sessionId: string) => void
  /** Whether this tab is the active, visible right-panel tab. When false, polling is paused. */
  isActive?: boolean
}

export function SshSessionView({ sessionId: initialSessionId, hostId, hostName, onSessionIdChange, onOpenRemoteFile, isActive = true }: SshSessionViewProps) {
  const { t } = useI18n()
  const isDesktop = isWails()
  // Active shell session id — starts from the prop, replaced on reconnect.
  // File/terminal operations target this id so a reconnected session is used.
  const [sessionId, setSessionId] = useState(initialSessionId)
  useEffect(() => { setSessionId(initialSessionId) }, [initialSessionId])
  // Notify the parent of the live session id so its tab-close handler closes
  // the correct (possibly reconnected) session instead of a stale one. The
  // callback is read through a ref so a new parent-rendered closure cannot
  // retrigger the effect and cause a render loop — it fires only when the
  // active session id actually changes.
  const onSessionIdChangeRef = useRef(onSessionIdChange)
  onSessionIdChangeRef.current = onSessionIdChange
  useEffect(() => { onSessionIdChangeRef.current?.(sessionId) }, [sessionId])
  // Terminal (xterm.js)
  const termContainerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XtermTerminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const [sessionError, setSessionError] = useState<string | null>(null)

  // Files
  const [filePath, setFilePath] = useState('/')
  const [fileEntries, setFileEntries] = useState<SshFileEntry[]>([])
  const [loadingFiles, setLoadingFiles] = useState(false)
  const [fileError, setFileError] = useState<string | null>(null)
  // Mirrors for stable callback identities: rapid tree navigation must not
  // recreate loadFiles (and restart the poll interval) on every keystroke.
  const filePathRef = useRef(filePath)
  filePathRef.current = filePath
  const fileCountRef = useRef(fileEntries.length)
  fileCountRef.current = fileEntries.length
  // Sequence guard: only the latest loadFiles call may commit state, so a
  // slow response for directory A cannot clobber the listing after the user
  // has already navigated to B.
  const fileReqSeqRef = useRef(0)
  const [fileViewMode, setFileViewMode] = useState<SshFileViewMode>('icons')
  const [prefsLoaded, setPrefsLoaded] = useState(false)

  // Resizable file-list columns. Widths are shared with the header via a CSS
  // custom property so header and rows stay pixel-aligned.
  const [colWidths, setColWidths] = useState<FileColumnWidths>(FILE_COLUMN_DEFAULT)
  const [colResizing, setColResizing] = useState(false)
  const fileGridCols = buildFileGridCols(colWidths)
  // While a column is being dragged, suppress text selection + force a
  // col-resize cursor across the whole document so the gesture feels solid.
  useEffect(() => {
    if (!colResizing) return
    document.body.classList.add('ssh-col-resizing')
    return () => { document.body.classList.remove('ssh-col-resizing') }
  }, [colResizing])
  const resizeColumn = useCallback((key: FileColumnKey, next: number) => {
    setColWidths(prev => ({ ...prev, [key]: clampColumnWidth(key, next) }))
  }, [])

  // Mirror colWidths into a ref so the column-resize drag-end handler can read
  // the final widths synchronously (the pointerup listener is outside React).
  const colWidthsRef = useRef(colWidths)
  useEffect(() => { colWidthsRef.current = colWidths })

  // Persist a partial patch of the shared SSH UI prefs (read-modify-write so
  // concurrent writers in other components don't wipe each other's fields).
  const persistUiPrefs = useCallback((patch: Partial<SshSessionUIPrefs>) => {
    saveSshSessionUIPrefs(patch).catch(() => {})
  }, [])

  // Column resize: toggle the resizing class live, persist the final widths
  // once when the drag ends (pointerup fires onDragStateChange(false)).
  const onColDragStateChange = useCallback((active: boolean) => {
    setColResizing(active)
    if (!active) persistUiPrefs({ colWidths: colWidthsRef.current })
  }, [persistUiPrefs])

  // Directory tree
  const [treeData, setTreeData] = useState<Record<string, SshFileEntry[]>>({})
  const [expandedPaths, setExpandedPaths] = useState<Set<string>>(new Set())
  const [selectedPath, setSelectedPath] = useState('/')
  const [selectedEntries, setSelectedEntries] = useState<Set<string>>(new Set())
  const [lastSelectedPath, setLastSelectedPath] = useState<string | null>(null)
  const [loadingTreePaths, setLoadingTreePaths] = useState<Set<string>>(new Set())

  // Status
  const [statusData, setStatusData] = useState<SshStatus | null>(null)
  const [loadingStatus, setLoadingStatus] = useState(false)
  const [statusError, setStatusError] = useState<string | null>(null)
  const [cpuHistory, setCpuHistory] = useState<number[]>([])
  const [netRxHistory, setNetRxHistory] = useState<number[]>([])
  const [netTxHistory, setNetTxHistory] = useState<number[]>([])

  // Layout
  const [statusCollapsed, setStatusCollapsed] = useState(false)
  const [filesCollapsed, setFilesCollapsed] = useState(false)
  const [bottomTab, setBottomTab] = useState<SshBottomTab>('ftp')
  const [statusWidth, setStatusWidth] = useState(240)
  const [filesTreeWidth, setFilesTreeWidth] = useState(180)
  const [filesHeight, setFilesHeight] = useState(260)
  const [treeSidebarOpen, setTreeSidebarOpen] = useState(true)
  const [commandTreeWidth, setCommandTreeWidth] = useState<number | null>(null)
  // Set true once a persisted files height is loaded so the one-time
  // auto-centering layout effect does not clobber the saved value.
  const filesHeightInitRef = useRef(false)
  const statusRef = useRef<HTMLDivElement>(null)
  const filesTreeRef = useRef<HTMLDivElement>(null)

  // Remote clipboard for copy/cut/paste within the FTP panel.
  const [fileClipboard, setFileClipboard] = useState<{ entries: SshFileEntry[]; mode: 'copy' | 'cut' } | null>(null)
  const filesPaneRef = useRef<HTMLDivElement>(null)
  // Marquee selection containers: the visible FTP view (list or icons) owns
  // the rubber-band gesture; only one is mounted at a time.
  const filesListRef = useRef<HTMLDivElement>(null)
  const filesIconsRef = useRef<HTMLDivElement>(null)

  // Drag-and-drop move (remote file/folder → remote directory)
  const draggedEntryRef = useRef<SshFileEntry | null>(null)
  const [dragOverPath, setDragOverPath] = useState<string | null>(null)

  // Context menu & dialogs
  const [menuTarget, setMenuTarget] = useState<SshMenuTarget | null>(null)
  const [sshDialog, setSshDialog] = useState<SshDialogState | null>(null)
  const [editingFile, setEditingFile] = useState<SshFileEntry | null>(null)
  const [sshToast, setSshToast] = useState<string | null>(null)
  const sshToastTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const showSshToast = useCallback((msg: string) => {
    setSshToast(msg)
    if (sshToastTimer.current) clearTimeout(sshToastTimer.current)
    sshToastTimer.current = setTimeout(() => setSshToast(null), 2500)
  }, [])

  // Transfer queue (download/upload progress overlay)
  const [transfers, setTransfers] = useState<SshTransfer[]>([])
  const transferIdRef = useRef(0)

  // Command bar — history / reconnect / copy / paste / search entry
  const [connected, setConnected] = useState(true)
  const [reconnecting, setReconnecting] = useState(false)
  const [cmdValue, setCmdValue] = useState('')
  const [historyOpen, setHistoryOpen] = useState(false)
  const [historyItems, setHistoryItems] = useState<string[]>([])
  const [loadingHistory, setLoadingHistory] = useState(false)
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [searchResultCount, setSearchResultCount] = useState(0)
  const [searchResultIndex, setSearchResultIndex] = useState(-1)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  const searchInputRef = useRef<HTMLInputElement>(null)
  const cmdInputRef = useRef<HTMLInputElement>(null)
  const historyDropdownRef = useRef<HTMLDivElement>(null)
  useMenuDismiss(historyDropdownRef, () => setHistoryOpen(false), historyOpen ? 'ssh-history' : null)

  const updateTransfer = useCallback((id: number, patch: Partial<SshTransfer>) => {
    setTransfers(prev => prev.map(tr => (tr.id === id ? { ...tr, ...patch } : tr)))
  }, [])

  const dismissTransfer = useCallback((id: number) => {
    setTransfers(prev => prev.filter(tr => tr.id !== id))
  }, [])

  // Runs `fn` as a tracked transfer; fn returns the transferred byte size.
  // The optional AbortController signal can be used by `fn` to abort early.
  const runTransfer = useCallback(async (
    name: string,
    direction: SshTransfer['direction'],
    fn: (signal: AbortSignal) => Promise<number | undefined>,
  ): Promise<boolean> => {
    const id = ++transferIdRef.current
    const abortController = new AbortController()
    setTransfers(prev => [...prev, { id, name, direction, status: 'running', abortController }])
    try {
      const size = await fn(abortController.signal)
      updateTransfer(id, { status: 'done', size, abortController: undefined })
      setTimeout(() => dismissTransfer(id), 4000)
      return true
    } catch (e) {
      if (abortController.signal.aborted) {
        updateTransfer(id, { status: 'cancelled', abortController: undefined })
        setTimeout(() => dismissTransfer(id), 4000)
        return false
      }
      console.error('[ssh-transfer] failed', e)
      const errMsg = formatError(e)
      const cls = classifyArchiveUploadError(errMsg)
      updateTransfer(id, { status: 'error', error: cls ? t(cls) : errMsg, abortController: undefined })
      return false
    }
  }, [updateTransfer, dismissTransfer, t])

  const cancelTransfer = useCallback((id: number) => {
    setTransfers(prev => prev.map(tr => {
      if (tr.id === id && tr.status === 'running' && tr.abortController) {
        tr.abortController.abort()
        return { ...tr, status: 'cancelled' as const, abortController: undefined }
      }
      return tr
    }))
  }, [])

  useEffect(() => {
    return () => { if (sshToastTimer.current) clearTimeout(sshToastTimer.current) }
  }, [])

  // Initialise xterm.js terminal and subscribe to the raw PTY stream.
  useEffect(() => {
    const container = termContainerRef.current
    if (!container) return
    setConnected(true)

    const term = new XtermTerminal({
      cursorBlink: true,
      fontFamily: 'Menlo, Consolas, "DejaVu Sans Mono", monospace',
      fontSize: 13,
      scrollback: 5000,
      // Allows the theme background to blend with the app background image.
      allowTransparency: true,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    const searchAddon = new SearchAddon()
    term.loadAddon(searchAddon)
    searchAddonRef.current = searchAddon
    // Reset stale result counters from a previous (possibly reconnected) terminal.
    setSearchResultCount(0)
    setSearchResultIndex(-1)
    // Track match count/index reported by the addon so the overlay can show
    // a live "x / y" indicator. resultIndex is -1 when the highlight limit
    // (1000) is exceeded.
    const searchResultsDisp = searchAddon.onDidChangeResults(e => {
      setSearchResultCount(e.resultCount)
      setSearchResultIndex(e.resultIndex)
    })
    term.open(container)
    // Guard: a collapsed right panel still leaves the body a few px wide
    // (padding), and fit() would then clamp cols to 2 and lossy-reflow the
    // buffer. Skip fitting until the container can hold real content.
    const containerTooSmall = () =>
      container.clientWidth < MIN_FIT_WIDTH_PX || container.clientHeight < MIN_FIT_HEIGHT_PX
    if (!containerTooSmall()) {
      fit.fit()
    }
    termRef.current = term
    fitRef.current = fit

    // On remount, rely solely on the backend replay buffer (delivered via
    // shellStream) to restore recent output. The backend caps replay at
    // 64 KiB, so content never accumulates across open/close cycles.

    // While the app background image is active, make the terminal canvas
    // fully transparent so the image shows through; the translucent dark
    // tint lives on .ssh-terminal-body (fills the whole rounded rectangle,
    // including the strip under the floating command bar).
    const syncTerminalBackground = () => {
      term.options.theme = {
        background: document.body.classList.contains('app-bg-active')
          ? 'rgba(0, 0, 0, 0)'
          : '#000000',
      }
    }
    syncTerminalBackground()
    const appBgObserver = new MutationObserver(syncTerminalBackground)
    appBgObserver.observe(document.body, { attributes: true, attributeFilter: ['class'] })

    // Forward keystrokes to the remote shell.
    const inputDisp = term.onData(async (data) => {
      try {
        await sshmanagerShell.shellInput(client, { SessionId: sessionId, Data: data })
      } catch (e) {
        setSessionError(formatError(e))
      }
    })

    // Send initial resize so the PTY matches the fitted geometry.
    const doResize = () => {
      sshmanagerShell.shellResize(client, {
        SessionId: sessionId,
        Cols: term.cols,
        Rows: term.rows,
      }).catch(() => { /* ignore — session may be closing */ })
    }
    doResize()
    // Debounce + suppress spurious resize: the right-panel CSS transition
    // fires many ResizeObserver callbacks as the container animates between
    // 0 and its full width. Each fit+resize sends a SIGWINCH to the PTY,
    // and the shell re-emits its prompt on every SIGWINCH, accumulating
    // duplicate output. We (1) debounce so only one resize fires after the
    // size stabilises, and (2) track the last dims sent to the PTY and skip
    // the resize if the final cols/rows match — this covers the close→reopen
    // case where the container went to 0 and back but the PTY geometry is
    // unchanged.
    let resizeTimer: ReturnType<typeof setTimeout> | null = null
    let lastSentCols = term.cols
    let lastSentRows = term.rows
    const resizeObs = new ResizeObserver(() => {
      if (containerTooSmall()) return
      if (resizeTimer) clearTimeout(resizeTimer)
      resizeTimer = setTimeout(() => {
        resizeTimer = null
        // Re-check inside the debounced callback: the container may have
        // shrunk back below the threshold during the 80ms window.
        if (containerTooSmall()) return
        try {
          fit.fit()
          // Skip resize if the fitted geometry is implausibly small (the
          // container is mid-transition) or identical to what the PTY
          // already has.
          if (term.cols >= 10 && term.rows >= 2 &&
              (term.cols !== lastSentCols || term.rows !== lastSentRows)) {
            lastSentCols = term.cols
            lastSentRows = term.rows
            doResize()
          }
        } catch { /* container gone */ }
      }, 80)
    })
    resizeObs.observe(container)

    // Consume the raw output stream.
    let cancelled = false
    let replayStarted = false
    const stream = sshmanagerShell.shellStream(client, { SessionId: sessionId })
    void (async () => {
      try {
        for await (const chunk of stream) {
          if (cancelled) break
          if (chunk.Replay) {
            // Before replaying, reset the terminal so we start from a clean
            // state. Without this, ANSI cursor movements in the replay can
            // accumulate content across open/close cycles because xterm's
            // scrollback grows with each replay.
            if (!replayStarted) {
              term.reset()
              replayStarted = true
            }
          }
          if (chunk.Data) term.write(chunk.Data)
          if (chunk.Exited) {
            term.write('\r\n\x1b[90m[remote session closed]\x1b[0m\r\n')
            setSessionError(t('ssh.placeholder.sessionDisconnected'))
            setConnected(false)
            break
          }
        }
      } catch (e) {
        if (!cancelled) { setSessionError(formatError(e)); setConnected(false) }
      }
    })()

    term.focus()

    return () => {
      cancelled = true
      // Explicitly close the stream so the backend detects unsubscribe and
      // stops delivering data to a dead subscriber.
      void (stream as AsyncGenerator).return?.(undefined)
      if (resizeTimer) clearTimeout(resizeTimer)
      appBgObserver.disconnect()
      inputDisp.dispose()
      resizeObs.disconnect()
      searchResultsDisp.dispose()
      searchAddonRef.current = null
      term.dispose()
      termRef.current = null
      fitRef.current = null
    }
  }, [sessionId, t])

  // Status
  const loadStatus = useCallback(async (silent = false) => {
    if (!silent) setLoadingStatus(true)
    if (!silent) setStatusError(null)
    try {
      const resp = await sshmanagerStatus.statusGet(client, { HostId: hostId })
      if (resp.Status) {
        setStatusData(resp.Status)
        setCpuHistory(prev => [...prev, resp.Status.CpuPercent].slice(-30))
        const totalRx = (resp.Status.Net ?? []).reduce((s, n) => s + Math.max(0, n.RxBps), 0)
        const totalTx = (resp.Status.Net ?? []).reduce((s, n) => s + Math.max(0, n.TxBps), 0)
        setNetRxHistory(prev => [...prev, totalRx].slice(-30))
        setNetTxHistory(prev => [...prev, totalTx].slice(-30))
      }
    } catch (e) {
      if (!silent) setStatusError(formatError(e))
    } finally {
      if (!silent) setLoadingStatus(false)
    }
  }, [hostId])

  useEffect(() => {
    if (!isActive || statusCollapsed) return
    loadStatus()
    const iv = setInterval(() => loadStatus(true), 5000)
    return () => clearInterval(iv)
  }, [isActive, statusCollapsed, loadStatus])

  // Load the shared SSH UI prefs (handle positions, view mode, pane
  // expand/collapse) once on mount. Applies to every SSH session tab — the
  // values are global, not per host — so the layout restored here is the same
  // one saved by any other SSH session view. Files/tree load is gated on
  // prefsLoaded to avoid a view-mode layout jump before the saved value lands.
  useEffect(() => {
    let cancelled = false
    loadSshSessionUIPrefs().then(prefs => {
      if (cancelled) return
      if (prefs) {
        setFileViewMode(prefs.fileViewMode)
        setStatusWidth(prefs.statusWidth)
        setFilesHeight(prefs.filesHeight)
        setFilesTreeWidth(prefs.filesTreeWidth)
        setColWidths(prefs.colWidths)
        setStatusCollapsed(prefs.statusCollapsed)
        setFilesCollapsed(prefs.filesCollapsed)
        setTreeSidebarOpen(prefs.treeSidebarOpen)
        setBottomTab(prefs.bottomTab)
        setCommandTreeWidth(prefs.commandTreeWidth)
        // A saved files height exists — skip the one-time auto-centering.
        filesHeightInitRef.current = true
      }
      setPrefsLoaded(true)
    }).catch(() => setPrefsLoaded(true))
    return () => { cancelled = true }
  }, [])

  const setView = useCallback((v: SshFileViewMode) => {
    setFileViewMode(v)
    persistUiPrefs({ fileViewMode: v })
  }, [persistUiPrefs])

  // Files
  const loadFiles = useCallback(async (path?: string, silent = false, cachedEntries?: SshFileEntry[]) => {
    const seq = ++fileReqSeqRef.current
    const targetPath = path ?? filePathRef.current
    // Commit the path immediately: navigation feels instant and the silent
    // poller targets the newest directory even while this request is in flight.
    if (path && path !== filePathRef.current) {
      filePathRef.current = path
      setFilePath(path)
    }
    // Only show the blocking "Loading..." state when there is nothing to
    // display yet; otherwise keep the stale list visible to avoid flicker.
    if (!silent) setLoadingFiles(fileCountRef.current === 0)
    if (!silent) setFileError(null)

    let entries: SshFileEntry[]
    if (cachedEntries) {
      // Reuse entries already fetched by loadTreeDir during the same click —
      // avoids a duplicate fileList call that made the right panel lag behind
      // the tree by half a second.
      entries = cachedEntries
    } else {
      try {
        const resp = await sshmanagerFile.fileList(client, { SessionId: sessionId, Path: targetPath })
        if (seq !== fileReqSeqRef.current) return // superseded by a newer request
        entries = resp.Entries ?? []
      } catch (e) {
        if (seq !== fileReqSeqRef.current) return
        if (!silent) setFileError(formatError(e))
        if (seq === fileReqSeqRef.current && !silent) setLoadingFiles(false)
        return
      }
    }

    const e = entries
    setFileEntries(prev => {
      if (prev.length === e.length
        && prev.every((pe, i) => pe.FullPath === e[i]!.FullPath
          && pe.Size === e[i]!.Size && pe.Modified === e[i]!.Modified)) return prev
      return e
    })
    // Single fetch updates both the file list and the tree node for this path.
    // Always populate treeData (even for first expansion) so the tree children
    // and the right-panel file list appear from the same response.
    setTreeData(prev => {
      const old = prev[targetPath]
      if (old && old.length === e.length
        && old.every((oe, i) => oe.FullPath === e[i]!.FullPath)) return prev
      return { ...prev, [targetPath]: e }
    })

    if (seq === fileReqSeqRef.current && !silent) setLoadingFiles(false)
  }, [sessionId])

  useEffect(() => {
    if (!isActive || !prefsLoaded || filesCollapsed || bottomTab !== 'ftp') return
    loadFiles()
  }, [isActive, prefsLoaded, filesCollapsed, bottomTab, loadFiles])

  // Center the terminal/files split by default: when the files pane first
  // opens, size it to half of the right stack. One-time only — after the user
  // drags the divider the chosen height persists for the session.
  useLayoutEffect(() => {
    if (!prefsLoaded || filesCollapsed || filesHeightInitRef.current) return
    filesHeightInitRef.current = true
    const stack = filesPaneRef.current?.closest('.ssh-right-stack') as HTMLElement | null
    if (!stack) return
    const handle = filesPaneRef.current?.previousElementSibling as HTMLElement | null
    const available = stack.clientHeight - (handle?.offsetHeight ?? 0)
    if (available > 240) setFilesHeight(Math.round(available / 2))
  }, [prefsLoaded, filesCollapsed])

  // Tree
  const parentDir = (p: string): string => {
    if (p === '/' || p === '') return '/'
    const parts = p.split('/').slice(0, -1)
    return parts.join('/') || '/'
  }

  const loadTreeDir = useCallback(async (path: string, force = false): Promise<SshFileEntry[]> => {
    if (!force && treeData[path]) return treeData[path]
    setLoadingTreePaths(prev => new Set(prev).add(path))
    try {
      const resp = await sshmanagerFile.fileList(client, { SessionId: sessionId, Path: path })
      const entries = resp.Entries ?? []
      // Overwrite in place; skip the update when unchanged so polling does
      // not re-render the whole tree.
      setTreeData(prev => {
        const old = prev[path]
        if (old
          && old.length === entries.length
          && old.every((oe, i) => oe.FullPath === entries[i]!.FullPath)) return prev
        return { ...prev, [path]: entries }
      })
      return entries
    } catch {
      return []
    } finally {
      setLoadingTreePaths(prev => {
        const next = new Set(prev)
        next.delete(path)
        return next
      })
    }
  }, [sessionId, treeData])

  useEffect(() => {
    if (!prefsLoaded || filesCollapsed || bottomTab !== 'ftp') return
    loadTreeDir('/')
  }, [prefsLoaded, filesCollapsed, bottomTab, loadTreeDir])

  // Manual refresh (Refresh button): force-refresh the root and all expanded
  // tree paths. No background polling — navigation fetches what it needs.
  const refreshTreeRef = useRef<() => void>(() => {})
  refreshTreeRef.current = () => {
    void loadTreeDir('/', true)
    for (const p of expandedPaths) void loadTreeDir(p, true)
  }

  const toggleTreePath = async (path: string) => {
    const next = new Set(expandedPaths)
    if (next.has(path)) {
      next.delete(path)
      setExpandedPaths(next)
    } else {
      await loadTreeDir(path)
      next.add(path)
      setExpandedPaths(next)
    }
  }

  const handleTreeClick = async (e: React.MouseEvent, entry: SshFileEntry) => {
    // Alt+click removes the tree node from the current selection.
    if (e.altKey) {
      setSelectedEntries(prev => {
        if (!prev.has(entry.FullPath)) return prev
        const next = new Set(prev)
        next.delete(entry.FullPath)
        return next
      })
      return
    }
    // Ctrl/Cmd toggles the tree node in/out of the selection (no navigation).
    if (e.ctrlKey || e.metaKey) {
      setSelectedEntries(prev => {
        const next = new Set(prev)
        if (next.has(entry.FullPath)) {
          next.delete(entry.FullPath)
        } else {
          next.add(entry.FullPath)
        }
        return next
      })
      return
    }
    // Plain click: navigate (existing behavior).
    setSelectedPath(entry.FullPath)
    setSelectedEntries(new Set())
    setLastSelectedPath(null)
    // Optimistically expand so cached tree children (if any) appear instantly;
    // loadFiles will refresh them. No separate tree fetch — loadFiles updates
    // both fileEntries and treeData from a single response.
    setExpandedPaths(prev => {
      if (prev.has(entry.FullPath)) return prev
      const next = new Set(prev)
      next.add(entry.FullPath)
      return next
    })
    void loadTreeDir(parentDir(entry.FullPath), true) // refresh parent for new siblings
    await loadFiles(entry.FullPath)
  }

  const handleFileClick = (e: React.MouseEvent, entry: SshFileEntry) => {
    // Alt+click removes the entry from the current selection (no navigation,
    // no addition). Works for files and directories alike.
    if (e.altKey) {
      setSelectedEntries(prev => {
        if (!prev.has(entry.FullPath)) return prev
        const next = new Set(prev)
        next.delete(entry.FullPath)
        return next
      })
      return
    }

    // Ctrl/Cmd toggles an entry in/out of the selection. Directories do NOT
    // navigate under a modifier — they join the multi-selection instead.
    if (e.ctrlKey || e.metaKey) {
      setSelectedEntries(prev => {
        const next = new Set(prev)
        if (next.has(entry.FullPath)) {
          next.delete(entry.FullPath)
        } else {
          next.add(entry.FullPath)
        }
        return next
      })
      setLastSelectedPath(entry.FullPath)
      return
    }

    // Shift selects a contiguous range from the anchor; directories included.
    if (e.shiftKey && lastSelectedPath !== null) {
      const startIdx = fileEntries.findIndex(fe => fe.FullPath === lastSelectedPath)
      const endIdx = fileEntries.findIndex(fe => fe.FullPath === entry.FullPath)
      if (startIdx !== -1 && endIdx !== -1) {
        const from = Math.min(startIdx, endIdx)
        const to = Math.max(startIdx, endIdx)
        setSelectedEntries(new Set(fileEntries.slice(from, to + 1).map(fe => fe.FullPath)))
      }
      setLastSelectedPath(entry.FullPath)
      return
    }

    // Plain click on a directory navigates into it (selection cleared).
    if (entry.IsDir) {
      setSelectedEntries(new Set())
      setLastSelectedPath(null)
      loadFiles(entry.FullPath)
      setSelectedPath(entry.FullPath)
      setExpandedPaths(prev => {
        const next = new Set(prev)
        next.add(entry.FullPath)
        return next
      })
      loadTreeDir(entry.FullPath)
      void loadTreeDir(parentDir(entry.FullPath), true)
      return
    }

    // Plain click on a file: single selection.
    setSelectedEntries(new Set([entry.FullPath]))
    setLastSelectedPath(entry.FullPath)
  }

  // Apply a marquee result to the FTP selection. `next` is fully resolved by
  // the hook for replace/add/subtract; `range` has no anchor logic here so it
  // degrades to replace (the hook's documented escape hatch).
  const applyMarqueeResult = useCallback((r: MarqueeSelectionResult) => {
    if (r.next) {
      setSelectedEntries(new Set(r.next))
    } else if (r.mode === 'range') {
      setSelectedEntries(new Set(r.paths))
    }
    if (r.paths.length > 0) setLastSelectedPath(r.paths[r.paths.length - 1]!)
  }, [])

  // Rubber-band marquee on the FTP list/icons. Each container mounts its own
  // gesture, gated by which view is current; the containers only exist while
  // the FTP tab is visible, so the `enabled` flag re-arms the listeners when
  // the tab/tree/panes re-appear (the hook effect re-runs on `enabled`).
  // Rows are draggable (HTML5 drag / Alt+drag-out): the hook only starts on
  // empty-area presses, so row drags are never hijacked.
  const marqueeEnabled = bottomTab === 'ftp' && !filesCollapsed
  useMarqueeSelection({
    containerRef: filesListRef,
    itemSelector: '.ssh-item-hit',
    ignoreSelector: '.ssh-files-header',
    enabled: marqueeEnabled && fileViewMode === 'list',
    getSelectedPaths: () => selectedEntries,
    onPreview: applyMarqueeResult,
    onHit: applyMarqueeResult,
  })
  useMarqueeSelection({
    containerRef: filesIconsRef,
    itemSelector: '.ssh-item-hit',
    enabled: marqueeEnabled && fileViewMode === 'icons',
    getSelectedPaths: () => selectedEntries,
    onPreview: applyMarqueeResult,
    onHit: applyMarqueeResult,
  })

  const handleFileUp = () => {
    const parent = parentDir(filePath)
    loadFiles(parent)
    setSelectedPath(parent)
    setSelectedEntries(new Set())
    setLastSelectedPath(null)
    void loadTreeDir(parentDir(parent), true)
  }

  const handleFileMove = async (entry: SshFileEntry, targetDir: string) => {
    // Can't drop onto self
    if (entry.FullPath === targetDir) { setDragOverPath(null); return }
    // Can't move a directory into itself or its own descendant
    if (entry.IsDir && targetDir.startsWith(entry.FullPath + '/')) { setDragOverPath(null); return }
    const destPath = joinRemotePath(targetDir, entry.Name)
    // No-op: source already at destination
    if (destPath === entry.FullPath) { setDragOverPath(null); return }
    try {
      await sshmanagerFile.fileRename(client, { SessionId: sessionId, From: entry.FullPath, To: destPath })
      // Refresh current listing (silent: keep the stale list visible) and
      // force-reload affected tree dirs in place — never delete cache entries,
      // that collapses expanded subtrees until the reload lands (flicker).
      await loadFiles(undefined, true)
      const affected = [filePath, targetDir, ...(entry.IsDir ? [entry.FullPath] : [])]
      await Promise.all(affected.filter((d, i, arr) => arr.indexOf(d) === i).map(d => loadTreeDir(d, true)))
    } catch (e) {
      setFileError(formatError(e))
    }
  }

  // ---- cross-panel upload (local → remote) via tar.gz archive transfer ----
  // Directories and single files (including binary) are packed into a tar.gz by
  // project.archive_export and extracted into the drop target directory by
  // sshmanager.archive_import. The archive preserves the top-level entry name,
  // so the import path is the target directory itself (not a joined child path).
  // This unifies directories and binary single files while keeping original bytes.
  const handleLocalUpload = useCallback(async (payload: FileDragPayload, targetDir: string) => {
    if (!payload.projectId) { setFileError(t('sshTransfer.missingProject')); return }
    setFileError(null)
    const ok = await runTransfer(payload.name, 'upload', async (signal) => {
      // InvokeOptions has no abort signal support; race the invoke against the
      // abort so a cancelled transfer stops waiting (the backend call itself
      // keeps running server-side but the UI frees immediately).
      const withAbort = <T,>(p: Promise<T>): Promise<T> => new Promise<T>((resolve, reject) => {
        const onAbort = () => reject(new DOMException('Aborted', 'AbortError'))
        if (signal.aborted) { onAbort(); return }
        signal.addEventListener('abort', onAbort, { once: true })
        p.then(resolve, reject).finally(() => signal.removeEventListener('abort', onAbort))
      })
      const exp = await withAbort(projectClient.archiveExport(client, { Path: payload.path }, { target: payload.projectId }))
      await withAbort(sshmanagerFile.archiveImport(client, { SessionId: sessionId, Path: targetDir, Content: exp.Content }))
      return exp.Size
    })
    if (!ok) return
    // Refresh current listing if the target is the open directory (silent so
    // the list does not flash "Loading...").
    if (targetDir === filePath) {
      await loadFiles(undefined, true)
    }
    // Force-reload the tree cache in place; never delete the entry — deleting
    // collapses the expanded subtree until the reload lands (visible flicker).
    loadTreeDir(targetDir, true)
  }, [sessionId, filePath, loadFiles, loadTreeDir, runTransfer, t])

  // ---- OS-level file drop (native OS files/folders dropped from the OS) ----
  // Dropped OS entries are written to the remote target directory: directories
  // are created via sshmanager fileMkdir (backend uses MkdirAll, so one call
  // per directory creates the whole subtree), files are read lazily and
  // uploaded via sshmanager fileWriteBase64. fs.Write does not create parent
  // directories, so a nested file's parent is mkdir'd first (idempotent).
  // collectOsDroppedEntries emits dirs before their children, which keeps the
  // ordering correct. After all entries are written, the remote listing and
  // tree cache are refreshed.
  const handleOsFileDrop = useCallback(async (entries: OsDroppedEntry[], targetDir: string) => {
    if (entries.length === 0) return
    const transferName = entries.length === 1
      ? entries[0]!.name
      : t('sshTransfer.osDropItems', { count: entries.length })
    const ok = await runTransfer(transferName, 'upload', async (signal) => {
      let total = 0
      // Directories already created in this drop; targetDir pre-seeded so
      // top-level files never trigger a redundant mkdir.
      const createdDirs = new Set<string>([targetDir])
      for (const ent of entries) {
        if (signal.aborted) break
        const remotePath = joinRemotePath(targetDir, ent.relPath)
        if (ent.isDir) {
          await sshmanagerFile.fileMkdir(client, { SessionId: sessionId, Path: remotePath })
          createdDirs.add(remotePath)
          continue
        }
        const parent = remoteParentOf(remotePath)
        if (!createdDirs.has(parent)) {
          await sshmanagerFile.fileMkdir(client, { SessionId: sessionId, Path: parent })
          createdDirs.add(parent)
        }
        const bytes = await ent.readBytes()
        await sshmanagerFile.fileWriteBase64(client, {
          SessionId: sessionId,
          Path: remotePath,
          Content: bytesToBase64(bytes),
        })
        total += ent.size
      }
      return total
    })
    if (!ok) return
    showSshToast(t('sshTransfer.osDropComplete', { count: entries.length }))
    // Refresh current listing if the target is the current dir (silent so the
    // list does not flash "Loading..."), and force-reload the tree cache in
    // place (never delete — deleting collapses the expanded subtree).
    if (targetDir === filePath) {
      await loadFiles(undefined, true)
    }
    loadTreeDir(targetDir, true)
  }, [sessionId, filePath, loadFiles, loadTreeDir, runTransfer, showSshToast, t])

  /**
   * Whether a drag carries native OS files (as opposed to an in-app HTML5 drag
   * between panels, which only sets the custom x-sporemind MIME types). OS file
   * drags advertise the standard `Files` type; `types` is readable during
   * `dragover`, unlike payload values.
   */
  const isOsFileDrag = (dt: DataTransfer | null): boolean => {
    return !!dt && Array.from(dt.types).includes('Files')
  }

  /**
   * Normalizes a drop's OS entries (attachOsFileDrop's normalization logic,
   * invoked inline so the entry list can be captured during the drop event)
   * and routes them to the remote target directory. Returns false when the
   * drop carried no OS files (caller falls back to in-app drag semantics).
   */
  const handleOsDropIfPresent = useCallback((e: React.DragEvent, targetDir: string): boolean => {
    if (!isOsFileDrag(e.dataTransfer)) return false
    e.preventDefault()
    void collectOsDroppedEntries(e.dataTransfer)
      .then((entries) => handleOsFileDrop(entries, targetDir))
      .catch((err: unknown) => setFileError(formatError(err)))
    return true
  }, [handleOsFileDrop])

  // ---- context menu actions ----

  const handleOpenFileEdit = useCallback((entry: SshFileEntry) => {
    const gate = shouldOpenForEdit(entry)
    if (!gate.ok) {
      if (gate.reason === 'tooLarge') showSshToast(t('sshToast.fileTooLarge'))
      return
    }
    setSelectedEntries(new Set())
    setLastSelectedPath(null)
    if (onOpenRemoteFile) {
      onOpenRemoteFile(entry)
      return
    }
    setEditingFile(entry)
  }, [showSshToast, t, onOpenRemoteFile])

  const handleFileContextMenu = useCallback((e: React.MouseEvent, entry: SshFileEntry) => {
    e.preventDefault()
    e.stopPropagation()
    setSelectedPath(entry.FullPath)
    // If the entry is not already selected in multi-select, select only this entry
    setSelectedEntries(prev => {
      if (prev.has(entry.FullPath)) return prev
      return new Set([entry.FullPath])
    })
    setMenuTarget({ entry, x: e.clientX, y: e.clientY })
  }, [])

  const handleBgContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    // Background menu only offers new file / new folder in the current dir.
    setMenuTarget({ entry: { Name: '', FullPath: filePath, IsDir: true, Size: 0 }, x: e.clientX, y: e.clientY })
  }, [filePath])

  const refreshAfterMutation = useCallback(async (dir?: string) => {
    // Load the new listing first, then overwrite the tree cache in place.
    // Never delete treeData entries: emptying the cache makes expanded
    // subtrees vanish until the reload lands, causing a visible flicker.
    await loadFiles(dir, true)
    const invalidate = [filePath, dir].filter((d): d is string => !!d)
    await Promise.all(invalidate.map(d => loadTreeDir(d, true)))
  }, [filePath, loadFiles, loadTreeDir])

  const handleSshDelete = useCallback(async (entries: SshFileEntry[]) => {
    if (entries.length === 0) return
    setFileError(null)
    const deletedPaths = new Set(entries.map(e => e.FullPath))
    try {
      // Per-entry file_delete: SFTP removal is recursive for directories, so
      // one call per path is enough — no backend batch API needed.
      for (const entry of entries) {
        await sshmanagerFile.fileDelete(client, { SessionId: sessionId, Path: entry.FullPath })
      }
      showSshToast(entries.length > 1
        ? t('sshToast.deletedN', { count: entries.length })
        : entries[0]!.IsDir
          ? t('sshToast.folderDeleted')
          : t('sshToast.fileDeleted'))
      // Drop the deleted paths from the FTP clipboard (copy/cut sources) and
      // from the selection, so a later paste/cut doesn't reference ghosts.
      setFileClipboard(prev => {
        if (!prev) return prev
        const remaining = prev.entries.filter(ce => !deletedPaths.has(ce.FullPath))
        if (remaining.length === 0) return null
        return remaining.length === prev.entries.length ? prev : { ...prev, entries: remaining }
      })
      setSelectedEntries(prev => {
        if (![...prev].some(p => deletedPaths.has(p))) return prev
        const next = new Set(prev)
        for (const p of deletedPaths) next.delete(p)
        return next
      })
      await refreshAfterMutation()
    } catch (e) {
      setFileError(formatError(e))
    }
  }, [sessionId, refreshAfterMutation, showSshToast, t])

  const handleSshRename = useCallback(async (entry: SshFileEntry, newName: string) => {
    const dest = joinRemotePath(remoteParentOf(entry.FullPath), newName)
    if (dest === entry.FullPath) return
    setFileError(null)
    try {
      await sshmanagerFile.fileRename(client, { SessionId: sessionId, From: entry.FullPath, To: dest })
      showSshToast(t('sshToast.renamed'))
      await refreshAfterMutation()
    } catch (e) {
      setFileError(formatError(e))
    }
  }, [sessionId, refreshAfterMutation, showSshToast, t])

  const handleSshNewFile = useCallback(async (dir: string, name: string) => {
    const fullPath = joinRemotePath(dir, name)
    setFileError(null)
    try {
      await sshmanagerFile.fileWrite(client, { SessionId: sessionId, Path: fullPath, Content: '' })
      showSshToast(t('sshToast.fileCreated'))
      await refreshAfterMutation()
    } catch (e) {
      setFileError(formatError(e))
    }
  }, [sessionId, refreshAfterMutation, showSshToast, t])

  const handleSshNewFolder = useCallback(async (dir: string, name: string) => {
    const fullPath = joinRemotePath(dir, name)
    setFileError(null)
    try {
      await sshmanagerFile.fileMkdir(client, { SessionId: sessionId, Path: fullPath })
      showSshToast(t('sshToast.folderCreated'))
      await refreshAfterMutation()
    } catch (e) {
      setFileError(formatError(e))
    }
  }, [sessionId, refreshAfterMutation, showSshToast, t])

  const handleSshDownload = useCallback(async (entry: SshFileEntry) => {
    setFileError(null)
    await runTransfer(entry.Name, 'download', async (signal) => {
      const withAbort = <T,>(p: Promise<T>): Promise<T> => new Promise<T>((resolve, reject) => {
        const onAbort = () => reject(new DOMException('Aborted', 'AbortError'))
        if (signal.aborted) { onAbort(); return }
        signal.addEventListener('abort', onAbort, { once: true })
        p.then(resolve, reject).finally(() => signal.removeEventListener('abort', onAbort))
      })
      const resp = await withAbort(sshmanagerFile.fileDownload(client, { SessionId: sessionId, Path: entry.FullPath }))
      return Number(resp.Size) || downloadBase64Blob(resp.Content, resp.Name || entry.Name)
    })
  }, [sessionId, runTransfer])

  const handleSshDownloadBulk = useCallback(async (entries: SshFileEntry[]) => {
    if (entries.length === 0) return
    setFileError(null)
    const transferName = entries.length === 1 ? `${entries[0]!.Name}.tar.gz` : 'archive.tar.gz'
    await runTransfer(transferName, 'download', async (signal) => {
      const withAbort = <T,>(p: Promise<T>): Promise<T> => new Promise<T>((resolve, reject) => {
        const onAbort = () => reject(new DOMException('Aborted', 'AbortError'))
        if (signal.aborted) { onAbort(); return }
        signal.addEventListener('abort', onAbort, { once: true })
        p.then(resolve, reject).finally(() => signal.removeEventListener('abort', onAbort))
      })

      let downloadPath = ''
      let archiveContent = ''
      let archiveSize = 0
      try {
        if (entries.length === 1) {
          const resp = await withAbort(sshmanagerFile.archiveExport(client, { SessionId: sessionId, Path: entries[0]!.FullPath }))
          archiveContent = resp.Content
          archiveSize = resp.Size
        } else {
          downloadPath = bulkTempArchiveName()
          const quotedPaths = entries.map(e => `"${e.FullPath.replace(/"/g, '\\"')}"`).join(' ')
          const tarResp = await withAbort(sshmanagerShell.exec(client, {
            HostId: hostId,
            Command: `tar czf "${downloadPath}" -- ${quotedPaths}`,
            Timeout: 120000,
          }))
          if (tarResp.ExitCode !== 0) {
            throw new Error(tarResp.Stderr.trim() || `tar failed with exit code ${tarResp.ExitCode}`)
          }
          const dlResp = await withAbort(sshmanagerFile.fileDownload(client, { SessionId: sessionId, Path: downloadPath }))
          archiveContent = dlResp.Content
          archiveSize = dlResp.Size
        }
        const size = Number(archiveSize) || downloadBase64Blob(archiveContent, transferName)
        return size
      } finally {
        if (downloadPath) {
          try {
            await sshmanagerShell.exec(client, { HostId: hostId, Command: `rm -f -- "${downloadPath}"` })
          } catch (e) {
            console.error('[ssh-bulk-download] cleanup failed', e)
          }
        }
      }
    })
  }, [sessionId, hostId, runTransfer])

  const handleSshCopyPath = useCallback(async (entry: SshFileEntry) => {
    try {
      await navigator.clipboard.writeText(entry.FullPath)
      showSshToast(t('sshToast.pathCopied'))
    } catch {
      showSshToast(entry.FullPath)
    }
  }, [showSshToast, t])

  // ---- remote file clipboard (copy / cut / paste) ----
  // Uses sshmanager.exec to run cp -r / mv on the remote host, so the
  // operation stays server-side and works for directories.

  const handleFileCopy = useCallback((entry: SshFileEntry) => {
    const entries = selectedEntries.has(entry.FullPath)
      ? fileEntries.filter(fe => selectedEntries.has(fe.FullPath))
      : [entry]
    setFileClipboard({ entries, mode: 'copy' })
    showSshToast(entries.length > 1
      ? t('sshToast.copiedN', { count: entries.length })
      : t('sshToast.copied', { name: entry.Name }))
  }, [fileEntries, selectedEntries, showSshToast, t])

  const handleFileCut = useCallback((entry: SshFileEntry) => {
    const entries = selectedEntries.has(entry.FullPath)
      ? fileEntries.filter(fe => selectedEntries.has(fe.FullPath))
      : [entry]
    setFileClipboard({ entries, mode: 'cut' })
    showSshToast(entries.length > 1
      ? t('sshToast.cutN', { count: entries.length })
      : t('sshToast.cut', { name: entry.Name }))
  }, [fileEntries, selectedEntries, showSshToast, t])

  const handleFilePaste = useCallback(async (targetDir: string) => {
    if (!fileClipboard || fileClipboard.entries.length === 0) return
    const { entries, mode } = fileClipboard
    setFileError(null)
    try {
      for (const entry of entries) {
        const dest = joinRemotePath(targetDir, entry.Name)
        if (dest === entry.FullPath) continue // no-op: paste onto self
        const cmd = mode === 'copy'
          ? `cp -r -- "${entry.FullPath}" "${dest}"`
          : `mv -- "${entry.FullPath}" "${dest}"`
        const resp = await sshmanagerShell.exec(client, { HostId: hostId, Command: cmd })
        if (resp.ExitCode !== 0) {
          throw new Error(resp.Stderr.trim() || `Command failed with exit code ${resp.ExitCode}`)
        }
      }
      setFileClipboard(null)
      setSelectedEntries(new Set())
      showSshToast(mode === 'copy' ? t('sshToast.pasted') : t('sshToast.moved'))
      await refreshAfterMutation(targetDir)
    } catch (e) {
      setFileError(formatError(e))
    }
  }, [fileClipboard, hostId, refreshAfterMutation, setSelectedEntries, showSshToast, t])

  const canPaste = fileClipboard != null && fileClipboard.entries.length > 0

  // Resolve selected paths to full entries across both the current listing and
  // the directory tree, so multi-selection built from tree nodes works even
  // when the selected folders are not in the currently open directory.
  const entryLookup = useMemo(() => {
    const map = new Map<string, SshFileEntry>()
    for (const fe of fileEntries) map.set(fe.FullPath, fe)
    for (const list of Object.values(treeData)) {
      for (const e of list) if (!map.has(e.FullPath)) map.set(e.FullPath, e)
    }
    return map
  }, [fileEntries, treeData])

  const handleDialogConfirm = useCallback(async (value: string) => {
    if (!sshDialog) return
    const d = sshDialog
    setSshDialog(null)
    switch (d.kind) {
      case 'newfile':   await handleSshNewFile(d.dir, value); break
      case 'newfolder': await handleSshNewFolder(d.dir, value); break
      case 'rename':    await handleSshRename(d.entry, value); break
      case 'delete':    await handleSshDelete(d.entries); break
    }
  }, [sshDialog, handleSshNewFile, handleSshNewFolder, handleSshRename, handleSshDelete])

  // ---- command bar: history / reconnect / copy / paste / search ----

  const loadHistory = useCallback(async () => {
    setLoadingHistory(true)
    try {
      const resp = await sshmanagerShell.historyList(client, { Limit: 50 })
      setHistoryItems(resp.Items ?? [])
    } catch (e) {
      // Surface the failure instead of silently showing "no history", so a
      // broken/unauthorized history_list is distinguishable from an empty one.
      setSessionError(formatError(e))
    } finally {
      setLoadingHistory(false)
    }
  }, [])

  const toggleHistory = useCallback(() => {
    setHistoryOpen(prev => {
      const next = !prev
      if (next) void loadHistory()
      return next
    })
  }, [loadHistory])

  const applyHistoryItem = useCallback((cmd: string) => {
    setCmdValue(cmd)
    setHistoryOpen(false)
    cmdInputRef.current?.focus()
  }, [])

  const sendCommand = useCallback(async (text: string) => {
    try {
      await sshmanagerShell.shellInput(client, { SessionId: sessionId, Data: text + '\n' })
      setCmdValue('')
      termRef.current?.focus()
      // The backend records the command on the trailing newline; refresh the
      // open dropdown so the just-sent command is visible immediately rather
      // than only after the next open.
      if (historyOpen) void loadHistory()
    } catch (e) {
      setSessionError(formatError(e))
    }
  }, [sessionId, historyOpen, loadHistory])

  const handleReconnect = useCallback(async () => {
    setReconnecting(true)
    setSessionError(null)
    const oldSessionId = sessionId
    try {
      const resp = await sshmanagerShell.shellOpen(client, {
        HostId: hostId,
        InitialCols: termRef.current?.cols,
        InitialRows: termRef.current?.rows,
      })
      if (resp.SessionId) {
        setSessionId(resp.SessionId)        // re-runs the terminal stream effect
        setConnected(true)
        showSshToast(t('sshToast.reconnected'))
        // Best-effort close the previous session so it does not leak. Failures
        // (already-closed / network) must not affect the newly opened session.
        if (resp.SessionId !== oldSessionId) {
          void sshmanagerShell.shellClose(client, { SessionId: oldSessionId }).catch(() => {})
        }
      } else {
        setSessionError(resp.Error || t('sshToast.reconnectFailed'))
        showSshToast(t('sshToast.reconnectFailed'))
      }
    } catch (e) {
      setSessionError(formatError(e))
      showSshToast(t('sshToast.reconnectFailed'))
    } finally {
      setReconnecting(false)
    }
  }, [hostId, sessionId, showSshToast, t])

  const [termMenu, setTermMenu] = useState<{ x: number; y: number; hasSelection: boolean } | null>(null)
  const handleClearTerminal = useCallback(() => {
    termRef.current?.clear()
    termRef.current?.focus()
  }, [])
  const handleTermContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    setTermMenu({ x: e.clientX, y: e.clientY, hasSelection: !!termRef.current?.getSelection() })
  }, [])

  const handleCopySelection = useCallback(async () => {
    const sel = termRef.current?.getSelection()
    if (!sel) {
      showSshToast(t('sshToast.noSelection'))
      return
    }
    try {
      await navigator.clipboard.writeText(sel)
      termRef.current?.clearSelection()
      showSshToast(t('sshToast.selectionCopied'))
    } catch {
      showSshToast(t('sshToast.clipboardFailed'))
    }
  }, [showSshToast, t])

  const handlePaste = useCallback(async () => {
    try {
      const text = await navigator.clipboard.readText()
      if (!text) return
      await sshmanagerShell.shellInput(client, { SessionId: sessionId, Data: text })
      termRef.current?.focus()
      showSshToast(t('sshToast.pasted'))
    } catch {
      showSshToast(t('sshToast.clipboardFailed'))
    }
  }, [sessionId, showSshToast, t])

  // ---- terminal search overlay ----

  // Focus the search input as soon as the overlay mounts.
  useEffect(() => {
    if (searchOpen) searchInputRef.current?.focus()
  }, [searchOpen])

  const runSearch = useCallback((direction: 'next' | 'prev') => {
    const addon = searchAddonRef.current
    if (!addon || !searchQuery) return
    if (direction === 'next') {
      addon.findNext(searchQuery, { decorations: SEARCH_DECORATIONS })
    } else {
      addon.findPrevious(searchQuery, { decorations: SEARCH_DECORATIONS })
    }
  }, [searchQuery])

  const onSearchChange = useCallback((query: string) => {
    setSearchQuery(query)
    const addon = searchAddonRef.current
    if (!addon) return
    if (!query) {
      addon.clearDecorations()
      setSearchResultCount(0)
      setSearchResultIndex(-1)
      return
    }
    // incremental: expand the active selection if it still matches, giving
    // the live-highlight behaviour as the user types.
    addon.findNext(query, { decorations: SEARCH_DECORATIONS, incremental: true })
  }, [])

  const closeSearch = useCallback(() => {
    searchAddonRef.current?.clearDecorations()
    setSearchQuery('')
    setSearchResultCount(0)
    setSearchResultIndex(-1)
    setSearchOpen(false)
    termRef.current?.focus()
  }, [])

  const toggleSearch = useCallback(() => {
    if (searchOpen) {
      closeSearch()
    } else {
      setSearchOpen(true)
    }
  }, [searchOpen, closeSearch])

  // Tree rendering
  const renderTreeNode = (path: string, depth: number): React.ReactNode[] => {
    const entries = treeData[path]
    if (!entries) return []
    const dirs = entries.filter(e => e.IsDir).sort((a, b) => a.Name.localeCompare(b.Name))
    return dirs.flatMap(entry => {
      const isExpanded = expandedPaths.has(entry.FullPath)
      const isSelected = selectedPath === entry.FullPath || selectedEntries.has(entry.FullPath)
      const isLoading = loadingTreePaths.has(entry.FullPath)
      const node = (
        <div
          key={entry.FullPath}
          className={`ssh-file-tree-row ${isSelected ? 'selected' : ''} ${fileClipboard?.mode === 'cut' && fileClipboard.entries.some(ce => ce.FullPath === entry.FullPath) ? 'cut-source' : ''} ${dragOverPath === entry.FullPath ? 'drag-over' : ''}`}
          style={{ paddingLeft: depth * 16 + 4 }}
          draggable
          title={isDesktop ? t('sshTransfer.altDragOutHint') : undefined}
          onDragStart={(e) => {
            if (e.altKey && isDesktop) {
              e.preventDefault()
              void startOsFileDragOut([{ kind: 'remote', sessionId, remotePath: entry.FullPath }])
              return
            }
            e.dataTransfer.effectAllowed = 'copyMove'
            draggedEntryRef.current = entry
            setFileDragPayload(e.dataTransfer, { origin: 'remote', sessionId, path: entry.FullPath, name: entry.Name, isDir: entry.IsDir })
          }}
          onDragEnd={() => { draggedEntryRef.current = null; setDragOverPath(null) }}
          onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = hasForeignFileDrag(e.dataTransfer, 'remote') || isOsFileDrag(e.dataTransfer) ? 'copy' : 'move'; setDragOverPath(entry.FullPath) }}
          onDragLeave={() => { if (dragOverPath === entry.FullPath) setDragOverPath(null) }}
          onDrop={(e) => {
            e.preventDefault(); e.stopPropagation()
            if (handleOsDropIfPresent(e, entry.FullPath)) { draggedEntryRef.current = null; setDragOverPath(null); return }
            const foreign = getForeignFileDragPayload(e.dataTransfer, 'remote')
            const d = draggedEntryRef.current
            draggedEntryRef.current = null; setDragOverPath(null)
            if (foreign) { void handleLocalUpload(foreign, entry.FullPath); return }
            if (d) handleFileMove(d, entry.FullPath)
          }}
          onClick={(e) => handleTreeClick(e, entry)}
          onContextMenu={(e) => handleFileContextMenu(e, entry)}
        >
          <span
            className="ssh-file-tree-chevron ssh-file-tree-chevron--clickable"
            onClick={(e) => {
              e.stopPropagation()
              void toggleTreePath(entry.FullPath)
            }}
          >
            {isLoading ? (
              <RefreshCw size={16} className="ssh-spin" />
            ) : isExpanded ? (
              <ChevronDown size={16} />
            ) : (
              <ChevronRight size={16} />
            )}
          </span>
          <span className="ssh-file-tree-icon">{getFolderIcon(isExpanded)}</span>
          <span className="ssh-file-tree-name">{entry.Name}</span>
        </div>
      )
      const children = isExpanded ? renderTreeNode(entry.FullPath, depth + 1) : []
      return [node, ...children]
    })
  }

  return (
    <div className="ssh-session-view">
      <div className={`ssh-session-content ${statusCollapsed ? 'status-collapsed' : ''} ${filesCollapsed ? 'files-collapsed' : ''}`}>
        {/* Status pane */}
        <div
          ref={statusRef}
          className="ssh-status-pane"
          style={statusCollapsed ? undefined : { width: statusWidth }}
        >
          {!statusCollapsed && (
            <div className="ssh-status-pane-body">
              {statusError && (
                <div className="ssh-status-error">{statusError}</div>
              )}
              {loadingStatus && !statusData && (
                <div className="ssh-empty-state" style={{ height: 'auto', padding: 8 }}>Loading...</div>
              )}
              {!statusData && !loadingStatus && !statusError && (
                <div className="ssh-empty-state" style={{ height: 'auto', padding: 8 }}>No status</div>
              )}
              {statusData && (
                <>
                  <div className="ssh-status-card">
                    <div className="ssh-status-card-title">
                      <Microchip size={12} /> CPU
                      <b className="ssh-status-card-value">{Math.round(statusData.CpuPercent)}%</b>
                    </div>
                    <Sparkline data={cpuHistory} max={100} color="var(--accent-primary)" />
                  </div>

                  <div className="ssh-status-card">
                    <div className="ssh-status-card-title">
                      <Activity size={12} /> Network
                      <b className="ssh-status-card-value">
                        ↓{formatBytes(netRxHistory[netRxHistory.length - 1] ?? 0)}/s
                        {' '}↑{formatBytes(netTxHistory[netTxHistory.length - 1] ?? 0)}/s
                      </b>
                    </div>
                    <div className="ssh-net-chart-labels">
                      <span className="ssh-net-label rx">↓ RX</span>
                      <span className="ssh-net-label tx">↑ TX</span>
                    </div>
                    <Sparkline data={netRxHistory} max={Math.max(...netRxHistory, 1024)} color="var(--status-running)" />
                    <Sparkline data={netTxHistory} max={Math.max(...netTxHistory, 1024)} color="var(--accent-primary)" height={20} />
                  </div>

                  <div className="ssh-status-card">
                    <div className="ssh-status-card-title"><MemoryStick size={12} /> Memory</div>
                    <div className="ssh-status-mini-grid">
                      <div>
                        <span>Used</span>
                        <b>{formatBytes(statusData.MemUsed)} / {formatBytes(statusData.MemTotal)}</b>
                        <div className="ssh-status-bar">
                          <div
                            className={`ssh-status-bar-fill ${
                              memPercent(statusData.MemUsed, statusData.MemTotal) > 80
                                ? 'danger'
                                : memPercent(statusData.MemUsed, statusData.MemTotal) > 60
                                  ? 'warning'
                                  : ''
                            }`}
                            style={{ width: `${memPercent(statusData.MemUsed, statusData.MemTotal)}%` }}
                          />
                        </div>
                      </div>
                    </div>
                  </div>

                  <div className="ssh-status-card">
                    <div className="ssh-status-card-title"><Network size={12} /> Interfaces</div>
                    <div className="ssh-status-list">
                      {(statusData.Net ?? []).map((net, i) => (
                        <div key={i} className="ssh-status-list-item">
                          <span>{net.Name}</span>
                          <span>↓{formatBytes(Math.max(0, net.RxBps))}/s ↑{formatBytes(Math.max(0, net.TxBps))}/s</span>
                        </div>
                      ))}
                    </div>
                  </div>

                  <div className="ssh-status-card">
                    <div className="ssh-status-card-title"><ArrowDownWideNarrow size={12} /> Top Procs</div>
                    <div className="ssh-status-list">
                      {(statusData.Procs ?? []).slice(0, 5).map((proc, i) => (
                        <div key={i} className="ssh-status-list-item">
                          <span>{proc.Command}</span>
                          <span>{Math.round(proc.Cpu)}%</span>
                        </div>
                      ))}
                    </div>
                  </div>

                  <div className="ssh-status-card">
                    <div className="ssh-status-card-title"><HardDrive size={12} /> Disks</div>
                    <div className="ssh-status-list">
                      {(statusData.Disks ?? []).map((disk, i) => (
                        <div key={i} className="ssh-status-list-item">
                          <span>{disk.MountPoint}</span>
                          <span>{Math.round(((disk.Total - disk.Available) / disk.Total) * 100)}%</span>
                        </div>
                      ))}
                    </div>
                  </div>
                </>
              )}
            </div>
          )}
        </div>

        {!statusCollapsed && (
          <ResizeHandle
            direction="horizontal"
            className="ssh-divider-handle"
            targetRef={statusRef}
            minSize={160}
            onResize={size => { const w = Math.max(160, size); setStatusWidth(w); persistUiPrefs({ statusWidth: w }) }}
            onResizeLive={size => setStatusWidth(Math.max(160, size))}
          />
        )}

        {/* Collapse toggle floating on the left/right divider */}
        <button
          className={`ssh-pane-toggle ${statusCollapsed ? 'collapsed' : ''}`}
          style={{ left: statusCollapsed ? 4 : statusWidth }}
          onClick={() => { const next = !statusCollapsed; setStatusCollapsed(next); persistUiPrefs({ statusCollapsed: next }) }}
          title={statusCollapsed ? 'Expand status' : 'Collapse status'}
        >
          {statusCollapsed ? <ChevronRight size={12} /> : <ChevronLeft size={12} />}
        </button>

        {/* Right stack: terminal (top) + files (bottom) */}
        <div className="ssh-right-stack">
          <div className="ssh-terminal-pane">
            <div className="ssh-pane-header">
              <Terminal size={12} /> <span>Terminal</span>
              <span className="ssh-pane-header-meta">
                {hostName} · {sessionId.slice(0, 8)}
              </span>
              <span className="ssh-pane-header-spacer" />
              <button
                className="ssh-pane-collapse"
                onClick={() => { const next = !filesCollapsed; setFilesCollapsed(next); persistUiPrefs({ filesCollapsed: next }) }}
                title={filesCollapsed ? 'Expand files' : 'Collapse files'}
              >
                {filesCollapsed ? <PanelBottomOpen size={14} /> : <PanelBottomClose size={14} />}
              </button>
            </div>
            <div className="ssh-terminal-body" onContextMenu={handleTermContextMenu}>
              <div className="ssh-terminal-canvas" ref={termContainerRef} />
              {sessionError && (
                <div className="ssh-session-error-overlay">
                  <span>{sessionError}</span>
                  <button className="ssh-error-dismiss" onClick={() => setSessionError(null)}><X size={12} /></button>
                </div>
              )}
              {searchOpen && (
                <div className="ssh-search-overlay">
                  <input
                    ref={searchInputRef}
                    className="ssh-search-input"
                    value={searchQuery}
                    placeholder={t('ssh.searchPlaceholder')}
                    spellCheck={false}
                    onChange={(e) => onSearchChange(e.currentTarget.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Escape') { e.preventDefault(); closeSearch() }
                      else if (e.key === 'Enter') { e.preventDefault(); runSearch(e.shiftKey ? 'prev' : 'next') }
                    }}
                  />
                  <span className="ssh-search-count">
                    {searchQuery && searchResultCount > 0
                      ? (searchResultIndex >= 0
                        ? t('ssh.searchResultCount', { index: searchResultIndex + 1, total: searchResultCount })
                        : t('ssh.searchManyResults', { total: searchResultCount }))
                      : (searchQuery ? t('ssh.searchNoResults') : '')
                    }
                  </span>
                  <button
                    className="ssh-search-btn"
                    title={t('ssh.searchPrev')}
                    onClick={() => runSearch('prev')}
                  >
                    <ChevronUp size={13} />
                  </button>
                  <button
                    className="ssh-search-btn"
                    title={t('ssh.searchNext')}
                    onClick={() => runSearch('next')}
                  >
                    <ChevronDown size={13} />
                  </button>
                  <button
                    className="ssh-search-btn"
                    title={t('ssh.searchClose')}
                    onClick={closeSearch}
                  >
                    <X size={13} />
                  </button>
                </div>
              )}
              <div className="ssh-cmd-bar">
                <div className="ssh-cmd-history-wrap" ref={historyDropdownRef}>
                  <button
                    className={`ssh-cmdbar-btn ${historyOpen ? 'active' : ''}`}
                    title={t('ssh.history')}
                    onClick={toggleHistory}
                  >
                    <History size={16} />
                  </button>
                  {historyOpen && (
                    <div className="ssh-cmd-dropdown">
                      {loadingHistory && historyItems.length === 0 && (
                        <div className="ssh-cmd-dropdown-empty">
                          <RefreshCw size={12} className="ssh-spin" />
                        </div>
                      )}
                      {!loadingHistory && historyItems.length === 0 && (
                        <div className="ssh-cmd-dropdown-empty">{t('ssh.noHistory')}</div>
                      )}
                      {historyItems.map((cmd, i) => (
                        <div
                          key={i}
                          className="ssh-cmd-dropdown-item"
                          onClick={() => applyHistoryItem(cmd)}
                          title={cmd}
                        >
                          {cmd}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
                <input
                  ref={cmdInputRef}
                  className="ssh-cmd-input"
                  value={cmdValue}
                  placeholder={t('ssh.placeholder.typeCommand')}
                  onChange={(e) => setCmdValue(e.currentTarget.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && e.currentTarget.value.trim()) {
                      void sendCommand(e.currentTarget.value)
                    }
                  }}
                />
                <button
                  className={`ssh-cmdbar-btn ssh-conn-btn ${connected ? 'connected' : 'disconnected'} ${reconnecting ? 'busy' : ''}`}
                  title={connected ? t('ssh.connected') : t('ssh.disconnected')}
                  disabled={reconnecting}
                  onClick={() => void handleReconnect()}
                >
                  <Zap size={16} className={reconnecting ? 'ssh-spin' : ''} />
                </button>
                <button
                  className="ssh-cmdbar-btn"
                  title={t('ssh.copy')}
                  onClick={() => void handleCopySelection()}
                >
                  <Copy size={16} />
                </button>
                <button
                  className="ssh-cmdbar-btn"
                  title={t('ssh.paste')}
                  onClick={() => void handlePaste()}
                >
                  <ClipboardPaste size={16} />
                </button>
                <button
                  className={`ssh-cmdbar-btn ${searchOpen ? 'active' : ''}`}
                  title={t('ssh.search')}
                  onClick={toggleSearch}
                >
                  <Search size={16} />
                </button>
              </div>
            </div>
          </div>

          {!filesCollapsed && (
            <ResizeHandle
              direction="vertical"
              targetRef={filesPaneRef}
              minSize={120}
              negateDelta
              onResize={size => { const h = Math.max(120, size); setFilesHeight(h); persistUiPrefs({ filesHeight: h }) }}
            />
          )}
          {!filesCollapsed && (
            <div ref={filesPaneRef} className="ssh-files-pane" style={{ height: filesHeight }}>
              <div className="ssh-bottom-tabbar">
                <button
                  className={`ssh-bottom-tab ${bottomTab === 'ftp' ? 'active' : ''}`}
                  onClick={() => { setBottomTab('ftp'); persistUiPrefs({ bottomTab: 'ftp' }) }}
                >
                  <FolderOpen size={16} />
                  {t('ssh.ftp')}
                </button>
                <button
                  className={`ssh-bottom-tab ${bottomTab === 'commands' ? 'active' : ''}`}
                  onClick={() => { setBottomTab('commands'); persistUiPrefs({ bottomTab: 'commands' }) }}
                >
                  <Terminal size={16} />
                  {t('ssh.commands')}
                </button>
              </div>
              <div className="ssh-bottom-tab-content">
              {bottomTab === 'ftp' && (
                <>
              {treeSidebarOpen && (
                <>
              <div
                ref={filesTreeRef}
                className="ssh-files-tree"
                style={{ width: filesTreeWidth }}
                {...{ [OS_DROP_TARGET_ATTR]: '' }}
              >
                <div className="ssh-files-tree-body">
                  <div
                    className={`ssh-file-tree-row ${selectedPath === '/' || selectedEntries.has('/') ? 'selected' : ''} ${dragOverPath === '/' ? 'drag-over' : ''}`}
                    style={{ paddingLeft: 4 }}
                    onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = hasForeignFileDrag(e.dataTransfer, 'remote') || isOsFileDrag(e.dataTransfer) ? 'copy' : 'move'; setDragOverPath('/') }}
                    onDragLeave={() => { if (dragOverPath === '/') setDragOverPath(null) }}
                    onDrop={(e) => {
                      e.preventDefault(); e.stopPropagation()
                      if (handleOsDropIfPresent(e, '/')) { draggedEntryRef.current = null; setDragOverPath(null); return }
                      const foreign = getForeignFileDragPayload(e.dataTransfer, 'remote')
                      const d = draggedEntryRef.current
                      draggedEntryRef.current = null; setDragOverPath(null)
                      if (foreign) { void handleLocalUpload(foreign, '/'); return }
                      if (d) handleFileMove(d, '/')
                    }}
                    onClick={(e) => handleTreeClick(e, ROOT_ENTRY)}
                  >
                    <span
                      className="ssh-file-tree-chevron ssh-file-tree-chevron--clickable"
                      onClick={(e) => {
                        e.stopPropagation()
                        void toggleTreePath('/')
                      }}
                    >
                      {expandedPaths.has('/') ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                    </span>
                    <span className="ssh-file-tree-icon">{getFolderIcon(expandedPaths.has('/'))}</span>
                    <span className="ssh-file-tree-name">/</span>
                  </div>
                  {renderTreeNode('/', 1)}
                </div>
              </div>

              <ResizeHandle
                direction="horizontal"
                targetRef={filesTreeRef}
                minSize={120}
                onResize={size => { const w = Math.max(120, size); setFilesTreeWidth(w); persistUiPrefs({ filesTreeWidth: w }) }}
              />
                </>
              )}

              <div
                className="ssh-files-explorer"
                {...{ [OS_DROP_TARGET_ATTR]: '' }}
                onContextMenu={handleBgContextMenu}
                onDragOver={(e) => {
                  if (hasForeignFileDrag(e.dataTransfer, 'remote') || isOsFileDrag(e.dataTransfer)) {
                    e.preventDefault()
                    e.dataTransfer.dropEffect = 'copy'
                  }
                }}
                onDrop={(e) => {
                  if (handleOsDropIfPresent(e, filePath)) return
                  const foreign = getForeignFileDragPayload(e.dataTransfer, 'remote')
                  if (foreign) {
                    e.preventDefault()
                    void handleLocalUpload(foreign, filePath)
                  }
                }}
              >
                <div className="ssh-files-toolbar">
                  <button
                    className={`ssh-btn-icon ${dragOverPath === '__parent__' ? 'drag-over' : ''}`}
                    onClick={handleFileUp}
                    onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = hasForeignFileDrag(e.dataTransfer, 'remote') || isOsFileDrag(e.dataTransfer) ? 'copy' : 'move'; setDragOverPath('__parent__') }}
                    onDragLeave={() => { if (dragOverPath === '__parent__') setDragOverPath(null) }}
                    onDrop={(e) => {
                      e.preventDefault()
                      const parent = filePath.split('/').slice(0, -1).join('/') || '/'
                      if (handleOsDropIfPresent(e, parent)) { draggedEntryRef.current = null; setDragOverPath(null); return }
                      const foreign = getForeignFileDragPayload(e.dataTransfer, 'remote')
                      const d = draggedEntryRef.current
                      draggedEntryRef.current = null; setDragOverPath(null)
                      if (foreign) { void handleLocalUpload(foreign, parent); return }
                      if (d) handleFileMove(d, parent)
                    }}
                    title="Parent"
                  ><ArrowUp size={18} /></button>
                  <input className="ssh-files-path" value={filePath} readOnly />
                  <button
                    className="ssh-btn-icon"
                    onClick={() => { loadFiles(); refreshTreeRef.current() }}
                    title="Refresh"
                  >
                    <RefreshCw size={16} />
                  </button>
                  <span className="ssh-files-toolbar-spacer" />
                  <button
                    className={`ssh-btn-icon ${treeSidebarOpen ? 'active' : ''}`}
                    onClick={() => { const next = !treeSidebarOpen; setTreeSidebarOpen(next); persistUiPrefs({ treeSidebarOpen: next }) }}
                    title={t('ssh.ftpTreeToggle')}
                  >
                    <ListTree size={18} />
                  </button>
                  <button
                    className={`ssh-btn-icon ${fileViewMode === 'list' ? 'active' : ''}`}
                    onClick={() => setView('list')}
                    title="List view"
                  >
                    <List size={18} />
                  </button>
                  <button
                    className={`ssh-btn-icon ${fileViewMode === 'icons' ? 'active' : ''}`}
                    onClick={() => setView('icons')}
                    title="Icon view"
                  >
                    <LayoutGrid size={18} />
                  </button>
                </div>

                {fileError && (
                  <div className="ssh-error-banner">
                    <span>{fileError}</span>
                    <button className="ssh-error-dismiss" onClick={() => setFileError(null)}><X size={12} /></button>
                  </div>
                )}

                {fileViewMode === 'list' ? (
                  <div
                    ref={filesListRef}
                    className={`ssh-files-list${colResizing ? ' resizing' : ''}`}
                    style={{ '--ssh-file-grid-cols': fileGridCols } as React.CSSProperties}
                  >
                    <div className="ssh-files-header" data-mq-ignore>
                      {FILE_COLUMN_ORDER.map(key => (
                        <span key={key} className="ssh-col-header">
                          {FILE_COLUMN_LABELS[key]}
                          <ColumnResizer
                            startWidth={colWidths[key]}
                            minWidth={FILE_COLUMN_MIN[key]}
                            onWidth={(w) => resizeColumn(key, w)}
                            onDragStateChange={onColDragStateChange}
                          />
                        </span>
                      ))}
                    </div>
                    {loadingFiles ? (
                      <div className="ssh-empty-state">Loading...</div>
                    ) : fileEntries.length === 0 ? (
                      <div className="ssh-empty-state">Empty directory</div>
                    ) : (
                      fileEntries.map(entry => (
                        <div
                          key={entry.FullPath}
                          className={`ssh-file-row ${entry.IsDir && dragOverPath === entry.FullPath ? 'drag-over' : ''}${selectedEntries.has(entry.FullPath) ? ' selected' : ''}${fileClipboard?.mode === 'cut' && fileClipboard.entries.some(ce => ce.FullPath === entry.FullPath) ? ' cut-source' : ''}`}
                          title={isDesktop ? t('sshTransfer.altDragOutHint') : undefined}
                        >
                          <div
                            className={`ssh-file-row-name ssh-item-hit ${entry.IsDir ? 'ssh-file-row-dir' : ''}`}
                            data-mq-path={entry.FullPath}
                            draggable
                            onDragStart={(e) => {
                              if (e.altKey && isDesktop) {
                                e.preventDefault()
                                void startOsFileDragOut([{ kind: 'remote', sessionId, remotePath: entry.FullPath }])
                                return
                              }
                              e.dataTransfer.effectAllowed = 'copyMove'
                              draggedEntryRef.current = entry
                              setFileDragPayload(e.dataTransfer, { origin: 'remote', sessionId, path: entry.FullPath, name: entry.Name, isDir: entry.IsDir })
                            }}
                            onDragEnd={() => { draggedEntryRef.current = null; setDragOverPath(null) }}
                            onDragOver={entry.IsDir ? (e) => { e.preventDefault(); e.dataTransfer.dropEffect = hasForeignFileDrag(e.dataTransfer, 'remote') || isOsFileDrag(e.dataTransfer) ? 'copy' : 'move'; setDragOverPath(entry.FullPath) } : undefined}
                            onDragLeave={entry.IsDir ? () => { if (dragOverPath === entry.FullPath) setDragOverPath(null) } : undefined}
                            onDrop={entry.IsDir ? (e) => {
                              e.preventDefault(); e.stopPropagation()
                              if (handleOsDropIfPresent(e, entry.FullPath)) { draggedEntryRef.current = null; setDragOverPath(null); return }
                              const foreign = getForeignFileDragPayload(e.dataTransfer, 'remote')
                              const d = draggedEntryRef.current
                              draggedEntryRef.current = null; setDragOverPath(null)
                              if (foreign) { void handleLocalUpload(foreign, entry.FullPath); return }
                              if (d) handleFileMove(d, entry.FullPath)
                            } : undefined}
                            onClick={(e) => handleFileClick(e, entry)}
                            onDoubleClick={() => {
                              if (entry.IsDir) { loadFiles(entry.FullPath); return }
                              handleOpenFileEdit(entry)
                            }}
                            onContextMenu={(e) => handleFileContextMenu(e, entry)}
                          >
                            {entry.IsDir ? getFolderIcon(false) : getComposedFileIcon(entry.Name)}
                            {entry.Name}
                          </div>
                          <span>{entry.IsDir ? '--' : formatBytes(entry.Size)}</span>
                          <span>{entry.Mode}</span>
                          <span title={entry.Modified}>{formatSshModified(entry.Modified)}</span>
                        </div>
                      ))
                    )}
                  </div>
                ) : (
                  <div ref={filesIconsRef} className="ssh-files-icons">
                    {loadingFiles ? (
                      <div className="ssh-empty-state">Loading...</div>
                    ) : fileEntries.length === 0 ? (
                      <div className="ssh-empty-state">Empty directory</div>
                    ) : (
                      fileEntries.map(entry => (
                        <div
                          key={entry.FullPath}
                          className={`ssh-file-icon-item ${entry.IsDir ? 'dir' : ''} ${entry.IsDir && dragOverPath === entry.FullPath ? 'drag-over' : ''}${selectedEntries.has(entry.FullPath) ? ' selected' : ''}${fileClipboard?.mode === 'cut' && fileClipboard.entries.some(ce => ce.FullPath === entry.FullPath) ? ' cut-source' : ''}`}
                          title={isDesktop ? t('sshTransfer.altDragOutHint') : undefined}
                        >
                          <div
                            className="ssh-item-hit"
                            data-mq-path={entry.FullPath}
                            draggable
                            onDragStart={(e) => {
                              if (e.altKey && isDesktop) {
                                e.preventDefault()
                                void startOsFileDragOut([{ kind: 'remote', sessionId, remotePath: entry.FullPath }])
                                return
                              }
                              e.dataTransfer.effectAllowed = 'copyMove'
                              draggedEntryRef.current = entry
                              setFileDragPayload(e.dataTransfer, { origin: 'remote', sessionId, path: entry.FullPath, name: entry.Name, isDir: entry.IsDir })
                            }}
                            onDragEnd={() => { draggedEntryRef.current = null; setDragOverPath(null) }}
                            onDragOver={entry.IsDir ? (e) => { e.preventDefault(); e.dataTransfer.dropEffect = hasForeignFileDrag(e.dataTransfer, 'remote') || isOsFileDrag(e.dataTransfer) ? 'copy' : 'move'; setDragOverPath(entry.FullPath) } : undefined}
                            onDragLeave={entry.IsDir ? () => { if (dragOverPath === entry.FullPath) setDragOverPath(null) } : undefined}
                            onDrop={entry.IsDir ? (e) => {
                              e.preventDefault(); e.stopPropagation()
                              if (handleOsDropIfPresent(e, entry.FullPath)) { draggedEntryRef.current = null; setDragOverPath(null); return }
                              const foreign = getForeignFileDragPayload(e.dataTransfer, 'remote')
                              const d = draggedEntryRef.current
                              draggedEntryRef.current = null; setDragOverPath(null)
                              if (foreign) { void handleLocalUpload(foreign, entry.FullPath); return }
                              if (d) handleFileMove(d, entry.FullPath)
                            } : undefined}
                            onClick={(e) => handleFileClick(e, entry)}
                            onDoubleClick={() => {
                              if (entry.IsDir) { loadFiles(entry.FullPath); return }
                              handleOpenFileEdit(entry)
                            }}
                            onContextMenu={(e) => handleFileContextMenu(e, entry)}
                          >
                            <div className="ssh-file-icon-thumb">
                              {entry.IsDir ? getFolderIcon(false) : getComposedFileIcon(entry.Name)}
                            </div>
                            <span className="ssh-file-icon-name" title={entry.Name}>{entry.Name}</span>
                          </div>
                        </div>
                      ))
                    )}
                  </div>
                )}
              </div>
                </>
              )}
              {bottomTab === 'commands' && (
                <SshCommandPanel sessionId={sessionId} initialTreeWidth={commandTreeWidth ?? undefined} />
              )}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Terminal context menu */}
      {termMenu && (
        <SshTermContextMenu
          menu={termMenu}
          onClose={() => setTermMenu(null)}
          onCopy={handleCopySelection}
          onPaste={handlePaste}
          onClear={handleClearTerminal}
          onFind={toggleSearch}
        />
      )}

      {/* Context menu */}
      {menuTarget && (
        <SshFileContextMenu
          target={menuTarget}
          onClose={() => setMenuTarget(null)}
          onOpen={(entry) => {
            if (!entry.IsDir) return
            setSelectedEntries(new Set())
            setLastSelectedPath(null)
            loadFiles(entry.FullPath)
          }}
          onEdit={handleOpenFileEdit}
          onNewFile={(dir) => setSshDialog({ kind: 'newfile', dir })}
          onNewFolder={(dir) => setSshDialog({ kind: 'newfolder', dir })}
          onCopyPath={handleSshCopyPath}
          onDownload={handleSshDownload}
          onDownloadBulk={handleSshDownloadBulk}
          onRename={(entry) => setSshDialog({ kind: 'rename', entry })}
          onDelete={(entries) => setSshDialog({ kind: 'delete', entries })}
          onCopy={handleFileCopy}
          onCut={handleFileCut}
          onPaste={handleFilePaste}
          canPaste={canPaste}
          currentDir={filePath}
          selectedEntries={selectedEntries}
          entryLookup={entryLookup}
        />
      )}

      {/* Dialog */}
      {sshDialog && (
        <SshFileDialog
          state={sshDialog}
          onConfirm={handleDialogConfirm}
          onCancel={() => setSshDialog(null)}
        />
      )}

      {/* Remote text file editor */}
      {editingFile && (
        <SshFileTextEditor
          client={client}
          sessionId={sessionId}
          path={editingFile.FullPath}
          size={editingFile.Size}
          onClose={() => setEditingFile(null)}
          onSaved={() => { void loadFiles(undefined, true) }}
        />
      )}

      {/* Toast */}
      {sshToast && <div className="ssh-toast">{sshToast}</div>}
      {transfers.length > 0 && (
        <div className="ssh-transfer-panel">
          <div className="ssh-transfer-title">{t('sshTransfer.title')}</div>
          {transfers.map(tr => (
            <div key={tr.id} className={`ssh-transfer-item ${tr.status}`}>
              {tr.direction === 'download' ? <Download size={12} /> : <Upload size={12} />}
              <span className="ssh-transfer-name" title={tr.name}>{tr.name}</span>
              {tr.status === 'running' && (
                <>
                  <span className="ssh-transfer-bar"><span className="ssh-transfer-bar-fill" /></span>
                  <button
                    type="button"
                    className="ssh-transfer-dismiss"
                    title={t('sshTransfer.cancel')}
                    onClick={() => cancelTransfer(tr.id)}
                  ><X size={12} /></button>
                </>
              )}
              {tr.status === 'cancelled' && (
                <span className="ssh-transfer-cancelled">{t('sshTransfer.cancelled')}</span>
              )}
              {tr.status === 'done' && (
                <span className="ssh-transfer-size">{tr.size != null ? formatBytes(tr.size) : ''}</span>
              )}
              {tr.status === 'error' && (
                <>
                  <span className="ssh-transfer-error" title={tr.error}>{tr.error}</span>
                  <button
                    type="button"
                    className="ssh-transfer-dismiss"
                    onClick={() => dismissTransfer(tr.id)}
                  ><X size={12} /></button>
                </>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/* ===================== SSH file context menu ===================== */

interface SshTermContextMenuProps {
  menu: { x: number; y: number; hasSelection: boolean }
  onClose: () => void
  onCopy: () => void
  onPaste: () => void
  onClear: () => void
  onFind: () => void
}

function SshTermContextMenu({ menu, onClose, onCopy, onPaste, onClear, onFind }: SshTermContextMenuProps) {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)
  useMenuDismiss(menuRef, onClose, menu)

  const items = [
    { icon: Copy, label: t('sshTermMenu.copy'), action: onCopy, disabled: !menu.hasSelection },
    { icon: ClipboardPaste, label: t('sshTermMenu.paste'), action: onPaste, disabled: false },
    { icon: Eraser, label: t('sshTermMenu.clear'), action: onClear, disabled: false },
    { icon: Search, label: t('sshTermMenu.find'), action: onFind, disabled: false },
  ]

  return (
    <div
      ref={menuRef}
      className="ssh-context-menu"
      style={{ left: menu.x, top: menu.y }}
      onClick={(e) => e.stopPropagation()}
    >
      {items.map((item) => {
        const Icon = item.icon
        return (
          <button
            key={item.label}
            className="ssh-context-menu-item"
            disabled={item.disabled}
            onClick={() => {
              item.action()
              onClose()
            }}
          >
            <Icon size={13} />
            <span>{item.label}</span>
          </button>
        )
      })}
    </div>
  )
}

interface SshFileContextMenuProps {
  target: SshMenuTarget
  onClose: () => void
  onOpen: (entry: SshFileEntry) => void
  onEdit: (entry: SshFileEntry) => void
  onNewFile: (dir: string) => void
  onNewFolder: (dir: string) => void
  onCopyPath: (entry: SshFileEntry) => void
  onDownload: (entry: SshFileEntry) => void
  onDownloadBulk: (entries: SshFileEntry[]) => void
  onRename: (entry: SshFileEntry) => void
  onDelete: (entries: SshFileEntry[]) => void
  onCopy: (entry: SshFileEntry) => void
  onCut: (entry: SshFileEntry) => void
  onPaste: (targetDir: string) => void
  canPaste: boolean
  currentDir: string
  selectedEntries: Set<string>
  entryLookup: ReadonlyMap<string, SshFileEntry>
}

function SshFileContextMenu({
  target, onClose, onOpen, onEdit, onNewFile, onNewFolder, onCopyPath, onDownload, onDownloadBulk, onRename, onDelete, onCopy, onCut, onPaste, canPaste, currentDir, selectedEntries, entryLookup,
}: SshFileContextMenuProps) {
  const menuRef = useRef<HTMLDivElement>(null)
  const { t } = useI18n()
  useMenuDismiss(menuRef, onClose, target)

  useLayoutEffect(() => {
    if (!menuRef.current) return
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

  const { entry, x, y } = target
  const isBackground = entry.Name === '' // synthetic background target
  const run = (fn: () => void) => () => { fn(); onClose() }
  const selectedItems = selectedEntries.has(entry.FullPath)
    ? [...selectedEntries].map(p => entryLookup.get(p)).filter((e): e is SshFileEntry => !!e)
    : [entry]

  return (
    <div
      ref={menuRef}
      className="ssh-context-menu"
      style={{ left: x, top: y }}
      role="menu"
      tabIndex={-1}
    >
      <div className="ssh-context-menu-header">
        <span className="ssh-context-menu-title">
          {isBackground ? t('sshContextMenu.currentDir') : entry.Name}
        </span>
      </div>
      <div className="ssh-context-menu-list">
        {!isBackground && entry.IsDir && (
          <>
            <button type="button" className="ssh-context-menu-item" onClick={run(() => onOpen(entry))}>
              <span className="ssh-context-menu-icon"><FolderOpen size={13} /></span>
              {t('sshContextMenu.open')}
            </button>
          </>
        )}

        {entry.IsDir && (
          <>
            <button type="button" className="ssh-context-menu-item" onClick={run(() => onNewFile(entry.FullPath))}>
              <span className="ssh-context-menu-icon"><FilePlus size={13} /></span>
              {t('sshContextMenu.newFile')}
            </button>
            <button type="button" className="ssh-context-menu-item" onClick={run(() => onNewFolder(entry.FullPath))}>
              <span className="ssh-context-menu-icon"><FolderPlus size={13} /></span>
              {t('sshContextMenu.newFolder')}
            </button>
            <div className="ssh-context-menu-sep" />
          </>
        )}

        {!isBackground && !entry.IsDir && selectedItems.length === 1 && (
          <button type="button" className="ssh-context-menu-item" onClick={run(() => onEdit(entry))}>
            <span className="ssh-context-menu-icon"><FileCode size={13} /></span>
            {t('sshContextMenu.edit')}
          </button>
        )}

        {!isBackground && (
          <>
            <button type="button" className="ssh-context-menu-item" onClick={run(() => onCopyPath(entry))}>
              <span className="ssh-context-menu-icon"><Copy size={13} /></span>
              {t('sshContextMenu.copyPath')}
            </button>
            <button type="button" className="ssh-context-menu-item" onClick={run(() => onCopy(entry))}>
              <span className="ssh-context-menu-icon"><Copy size={13} /></span>
              {selectedEntries.has(entry.FullPath) && selectedEntries.size > 1
                ? t('sshContextMenu.copyN', { count: selectedEntries.size })
                : t('sshContextMenu.copy')}
            </button>
            <button type="button" className="ssh-context-menu-item" onClick={run(() => onCut(entry))}>
              <span className="ssh-context-menu-icon"><Scissors size={13} /></span>
              {selectedEntries.has(entry.FullPath) && selectedEntries.size > 1
                ? t('sshContextMenu.cutN', { count: selectedEntries.size })
                : t('sshContextMenu.cut')}
            </button>
            {selectedItems.length > 1 || entry.IsDir ? (
              <button type="button" className="ssh-context-menu-item" onClick={run(() => onDownloadBulk(selectedItems))}>
                <span className="ssh-context-menu-icon"><Download size={13} /></span>
                {selectedItems.length > 1
                  ? t('sshContextMenu.downloadN', { count: selectedItems.length })
                  : t('sshContextMenu.download')}
              </button>
            ) : (
              <button type="button" className="ssh-context-menu-item" onClick={run(() => onDownload(entry))}>
                <span className="ssh-context-menu-icon"><Download size={13} /></span>
                {t('sshContextMenu.download')}
              </button>
            )}
            <div className="ssh-context-menu-sep" />
            <button type="button" className="ssh-context-menu-item" onClick={run(() => onRename(entry))}>
              <span className="ssh-context-menu-icon"><Pencil size={13} /></span>
              {t('sshContextMenu.rename')}
            </button>
            <button type="button" className="ssh-context-menu-item danger" onClick={run(() => onDelete(selectedItems))}>
              <span className="ssh-context-menu-icon"><Trash2 size={13} /></span>
              {selectedItems.length > 1
                ? t('sshContextMenu.deleteN', { count: selectedItems.length })
                : t('sshContextMenu.delete')}
            </button>
          </>
        )}
        {canPaste && (
          <>
            <div className="ssh-context-menu-sep" />
            <button type="button" className="ssh-context-menu-item" onClick={run(() => onPaste(entry.IsDir ? entry.FullPath : currentDir))}>
              <span className="ssh-context-menu-icon"><ClipboardPaste size={13} /></span>
              {t('sshContextMenu.paste')}
            </button>
          </>
        )}
      </div>
    </div>
  )
}

/* ===================== SSH file dialog ===================== */

interface SshFileDialogProps {
  state: SshDialogState
  onConfirm: (value: string) => void
  onCancel: () => void
}

function SshFileDialog({ state, onConfirm, onCancel }: SshFileDialogProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const { t } = useI18n()
  const [value, setValue] = useState(
    state.kind === 'rename' ? state.entry.Name : '',
  )

  useEffect(() => {
    inputRef.current?.focus()
    if (state.kind === 'rename') inputRef.current?.select()
  }, [state.kind])

  const isPrompt = state.kind === 'newfile' || state.kind === 'newfolder' || state.kind === 'rename'

  const titles: Record<SshDialogState['kind'], string> = {
    newfile: t('sshDialog.title.newFile'),
    newfolder: t('sshDialog.title.newFolder'),
    rename: t('sshDialog.title.rename'),
    delete: t('sshDialog.title.delete'),
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (isPrompt && !value.trim()) return
    onConfirm(value.trim())
  }

  return (
    <div className="ssh-dialog-overlay" onClick={onCancel}>
      <form className="ssh-dialog" onClick={e => e.stopPropagation()} onSubmit={handleSubmit}>
        <div className="ssh-dialog-title">{titles[state.kind]}</div>
        {state.kind === 'delete' ? (
          <div className="ssh-dialog-message">
            {state.entries.length > 1
              ? t('sshDialog.deleteConfirmMany', { count: state.entries.length })
              : state.entries[0]!.IsDir
                ? t('sshDialog.deleteConfirmFolder', { name: state.entries[0]!.Name })
                : t('sshDialog.deleteConfirmFile', { name: state.entries[0]!.Name })}
            <br />{t('sshDialog.deleteWarning')}
          </div>
        ) : (
          <input
            ref={inputRef}
            className="ssh-dialog-input"
            type="text"
            placeholder={state.kind === 'newfolder'
              ? t('sshDialog.placeholder.folderName')
              : t('sshDialog.placeholder.fileName')}
            value={value}
            onChange={e => setValue(e.target.value)}
          />
        )}
        <div className="ssh-dialog-actions">
          <button type="button" className="ssh-dialog-btn" onClick={onCancel}>{t('sshDialog.button.cancel')}</button>
          <button
            type="submit"
            className={`ssh-dialog-btn ${state.kind === 'delete' ? 'danger' : 'primary'}`}
          >
            {state.kind === 'delete'
              ? t('sshDialog.button.delete')
              : state.kind === 'rename'
                ? t('sshDialog.button.rename')
                : t('sshDialog.button.create')}
          </button>
        </div>
      </form>
    </div>
  )
}
