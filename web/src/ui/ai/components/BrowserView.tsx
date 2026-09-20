import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowLeft,
  ArrowRight,
  Home,
  RotateCw,
  Globe,
  History,
  Search,
  X,
  Plus,
  Trash2,
  Pencil,
  FileUp,
} from 'lucide-react'
import {
  closeBrowserSession,
  goBackBrowserWindow,
  goForwardBrowserWindow,
  navigateBrowserSession,
  openBrowserSession,
  setBrowserWindowVisible,
  splashDone,
  updateBrowserWindow as updateBrowserWindowBounds,
} from '../../../application/wails-bridge'
import { onWailsEvent } from '../../../application/wails-runtime'
import {
  listBrowserWindows,
  openBrowserWindow,
  closeBrowserWindow,
  updateBrowserWindow,
  onBrowserManagerEvent,
  updateBrowserWindowUrl,
  openBrowserInstance,
  exportBrowserCookies,
  importBrowserCookies,
  effectiveProxyMode,
} from '../../../application/browser-manager'
import type { BrowserInstance, BrowserManagerEvent } from '../../../gen-types/browser'
import type { BrowserProxyMode } from '../../../application/browser-manager'
import { parseCookieFile } from './cookieImport'
import { useBrowserOverlay } from '../browserOverlay'
import { useI18n } from '../../../i18n'
import './BrowserView.css'

export type BrowserKind = 'global' | 'independent' | 'app'

interface BrowserViewProps {
  sessionId: string
  kind: BrowserKind
  initialUrl?: string
  onUrlChange?: (url: string) => void
  onTitleChange?: (title: string, favicon?: string) => void
  onFaviconChange?: (favicon?: string) => void
  onBgColorChange?: (bg: string) => void
  snapshot?: string | null
  onOpenRightTab?: (sessionId: string, kind: BrowserKind, url: string, label: string) => void
  onOpenBrowserTab?: (url?: string) => void
  onSaveCreateDefaults?: (scope: 'normal' | 'independent') => void
  defaultCreateScope?: 'normal' | 'independent'
  onCloseSelf?: () => void
}

function normalizeUrl(input: string): string {
  const trimmed = input.trim()
  if (!trimmed) return ''
  if (/^file:/i.test(trimmed)) {
    // WHATWG normalization also fixes hand-typed two-slash drive forms
    // (file://D:/x.html → file:///D:/x.html), matching Chromium's canonical URL.
    try {
      return new URL(trimmed).href
    } catch {
      return trimmed
    }
  }
  if (/^(https?:|about:)/i.test(trimmed)) return trimmed
  // Bare Windows drive path (D:\x\page.html or D:/x/page.html) → file URL.
  if (/^[a-zA-Z]:[\\/]/.test(trimmed)) {
    try {
      return new URL('file:///' + trimmed.replace(/\\/g, '/')).href
    } catch {
      return trimmed
    }
  }
  if (trimmed.includes('.') && !trimmed.includes(' ')) {
    return `https://${trimmed}`
  }
  return `https://www.google.com/search?q=${encodeURIComponent(trimmed)}`
}

function isBrowserInternalURL(url: string): boolean {
  try {
    const parsed = new URL(url)
    // Allow normal web/file URLs and about:blank; everything else
    // (chrome-error, chrome://, edge://, data:, etc.) is internal and should
    // not pollute the address bar or persisted state.
    if (parsed.protocol === 'http:' || parsed.protocol === 'https:' || parsed.protocol === 'file:') return false
    if (parsed.protocol === 'about:' && parsed.pathname === 'blank') return false
    return true
  } catch {
    return true
  }
}

// History timestamps are persisted as UTC RFC3339 (schema contract); the
// omnibox renders them in the user's local timezone (display-layer convert).
function formatHistoryTime(visitedAt: string): string {
  const d = new Date(visitedAt)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString(undefined, { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

export const BrowserView: React.FC<BrowserViewProps> = ({ sessionId, kind, initialUrl, onUrlChange, onTitleChange, onFaviconChange, onBgColorChange, snapshot, onOpenRightTab, onOpenBrowserTab, onSaveCreateDefaults, defaultCreateScope, onCloseSelf }) => {
  const { t } = useI18n()
  const frameRef = useRef<HTMLDivElement>(null)
  // Tracks whether this BrowserView instance is still mounted. Async open()
  // operations check this after each await to avoid showing a browser child
  // window after the user has already switched away to another tab.
  const mountedRef = useRef(true)
  const startUrl = initialUrl || ''
  const [input, setInput] = useState(startUrl)
  const [currentUrl, setCurrentUrl] = useState(startUrl)
  // `loading` is a pure projection of the backend navigation status (contract
  // §5: loading = (status == loading)). It is set/cleared exclusively by
  // browser:state — never by optimistic command-side writes and never
  // borrowed from the native-window visibility (that is what `shown` is for).
  const [loading, setLoading] = useState(false)
  // `shown` only reflects whether the native child window is on screen. It
  // drives the loading-overlay *cover* (the overlay must stay up until the
  // native surface actually covers it) but is independent of the navigation
  // status. Reset to false on each mount because the window is hidden until
  // the backend reports first paint.
  const [shown, setShown] = useState(false)
  const [canBack, setCanBack] = useState(false)
  const [canForward, setCanForward] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [navigationError, setNavigationError] = useState<string | null>(null)
  const [repositioning, setRepositioning] = useState(false)
  const [instances, setInstances] = useState<BrowserInstance[]>([])
  const [isModalOpen, setIsModalOpen] = useState(false)
  const [editingInstance, setEditingInstance] = useState<BrowserInstance | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<BrowserInstance | null>(null)
  const [formName, setFormName] = useState('')
  const [formUrl, setFormUrl] = useState('')
  const [formScope, setFormScope] = useState<'normal' | 'independent'>(defaultCreateScope ?? 'normal')
  const [formProxy, setFormProxy] = useState('')
  const [formProxyMode, setFormProxyMode] = useState<BrowserProxyMode>('system')
  const [submitting, setSubmitting] = useState(false)
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; instanceId: string } | null>(null)
  const [cookieAction, setCookieAction] = useState<'idle' | 'exporting' | 'importing'>('idle')
  const [dragOverInstance, setDragOverInstance] = useState<string | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const contextMenuRef = useRef<HTMLDivElement>(null)
  // Address-bar history omnibox: only independent tabs have persisted history
  // (kind 'independent' == a browsermanager instance whose Id == sessionId).
  const [historyOpen, setHistoryOpen] = useState(false)
  const [historyIndex, setHistoryIndex] = useState(-1)
  useBrowserOverlay(historyOpen)

  const syncRect = useCallback(() => {
    const el = frameRef.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    if (rect.width === 0 || rect.height === 0) {
      // Don't toggle visibility here: AIShellLayout's unified state machine
      // (active-tab + right-panel-open effects) owns native-window visibility.
      // A 0x0 rect can happen during remount / layout transitions; hiding would
      // race with the active tab's open() and produce flicker.
      return
    }
    void updateBrowserWindowBounds(sessionId, Math.round(rect.x), Math.round(rect.y), Math.round(rect.width), Math.round(rect.height))
  }, [sessionId])

  // rAF-coalesced syncRect: capture-phase scroll events fire far more often
  // than the display refreshes (per-wheel-tick, per-pixel), and each syncRect
  // is an IPC round-trip ending in a Win32 SetWindowPos. Coalescing to at
  // most one sync per frame keeps the child window tracking smoothly without
  // flooding the bridge.
  const syncRafRef = useRef<number | null>(null)
  const scheduleSyncRect = useCallback(() => {
    if (syncRafRef.current != null) return
    syncRafRef.current = requestAnimationFrame(() => {
      syncRafRef.current = null
      syncRect()
    })
  }, [syncRect])

  // open creates (or re-focuses) the native browser child window. When called
  // with no argument it uses currentUrl; the urlOverride lets the first
  // address-bar submit from an empty new-tab create the session with the
  // submitted URL in one step (navigate() would silently no-op because the
  // session does not exist yet).
  const open = useCallback(async (urlOverride?: string) => {
    const url = urlOverride ?? currentUrl
    if (!url) return
    const el = frameRef.current
    if (!el) return
    await splashDone()
    if (!mountedRef.current) return
    const rect = el.getBoundingClientRect()
    const x = Math.round(rect.x)
    const y = Math.round(rect.y)
    const width = Math.round(rect.width)
    const height = Math.round(rect.height)
    try {
      setError(null)
      // Navigation must NOT toggle native-window visibility (contract invariant
      // "导航不触发 Hide/Show 副作用"). The backend creates the child window
      // Hidden:true and reveals it only after first paint, so the React loading
      // overlay is visible during the initial load without the frontend having
      // to flip wanted=false. Hiding here would strand the window (wanted=false
      // blocks the paint-reveal) or cause a hide/show flicker on re-open.
      //
      // loading is NOT set optimistically here: it is driven exclusively by
      // browser:state (status field). The overlay stays up via `!shown`
      // until the window reports first paint.
      if (!mountedRef.current) return
      await openBrowserSession(sessionId, kind, url, x, y, width, height)
    } catch (e) {
      setError(String(e))
    }
  }, [sessionId, kind, currentUrl])
  // Guards the currentUrl effect below: when true, a currentUrl change must NOT
  // re-open the window. Set by the browser:state handler (confirmedURL
  // arrives from the backend which has already navigated).
  const skipNavigateRef = useRef(false)
  const navigate = useCallback((url: string) => {
    const next = normalizeUrl(url)
    if (!next) return
    // No optimistic state write (contract §5 / §10.B.5): the address bar and
    // loading indicator are driven exclusively by browser:state. The
    // backend's NavigationStarting will push status=loading; its commit will
    // push confirmedURL + status=loaded/failed.
    void navigateBrowserSession(sessionId, next)
  }, [sessionId])

  // Settings page (home) of an independent window. Config.Url is persisted
  // separately from the current navigation page; the Home button returns to it.
  const homeUrl = useMemo(() => {
    if (kind !== 'independent') return ''
    return instances.find((i) => i.Config.Id === sessionId)?.Config.Url ?? ''
  }, [kind, sessionId, instances])

  const handleHome = useCallback(() => {
    if (!homeUrl) return
    if (!currentUrl) {
      void open(homeUrl)
      return
    }
    navigate(homeUrl)
  }, [homeUrl, currentUrl, navigate, open])

  // Newest-first visit history for this independent instance, sourced from the
  // browsermanager list (refreshed on every manager event, including the
  // navigated event the backend emits after each history append).
  const historyEntries = useMemo(() => {
    if (kind !== 'independent') return []
    const inst = instances.find((i) => i.Config.Id === sessionId)
    return [...(inst?.Config.State?.History ?? [])].reverse()
  }, [kind, sessionId, instances])

  const historyMatches = useMemo(() => {
    const q = input.trim().toLowerCase()
    const src = q
      ? historyEntries.filter((e) => e.Url.toLowerCase().includes(q) || e.Title.toLowerCase().includes(q))
      : historyEntries
    return src.slice(0, 8)
  }, [historyEntries, input])

  const selectHistory = useCallback((url: string) => {
    setHistoryOpen(false)
    setHistoryIndex(-1)
    setInput(url)
    if (!currentUrl) {
      void open(url)
      return
    }
    navigate(url)
  }, [currentUrl, navigate, open])

  const handleBack = useCallback(() => {
    if (!canBack) return
    void goBackBrowserWindow(sessionId)
  }, [sessionId, canBack])

  const handleForward = useCallback(() => {
    if (!canForward) return
    void goForwardBrowserWindow(sessionId)
  }, [sessionId, canForward])

  const handleReload = useCallback(() => {
    // loading is driven by browser:state (status field), not set here.
    // NavigateBrowserSession triggers the native navigation whose
    // NavigationStarting/Completed push the state transitions.
    void navigateBrowserSession(sessionId, currentUrl)
  }, [sessionId, currentUrl])

  const handleSubmit = useCallback((e: React.FormEvent) => {
    e.preventDefault()
    setHistoryOpen(false)
    // A keyboard-highlighted omnibox entry wins over the raw input text.
    if (historyIndex >= 0 && historyMatches[historyIndex]) {
      selectHistory(historyMatches[historyIndex].Url)
      return
    }
    const normalized = normalizeUrl(input)
    if (normalized === currentUrl) {
      handleReload()
      return
    }
    if (!currentUrl) {
      // First address-bar submit from an empty new-tab: no native session
      // exists yet (open() early-returned on the empty URL at mount). open()
      // creates the child window AND navigates to the target URL in one step,
      // whereas navigate() would hit the backend's session-not-found guard
      // and silently return.
      void open(normalized)
      return
    }
    navigate(input)
  }, [input, currentUrl, handleReload, navigate, open, historyIndex, historyMatches, selectHistory])

  const handleClose = useCallback(() => {
    void closeBrowserSession(sessionId)
  }, [sessionId])

  const openRef = useRef(open)
  useEffect(() => { openRef.current = open }, [open])

  useEffect(() => {
    mountedRef.current = true
    void openRef.current()

    const handleResize = () => scheduleSyncRect()
    window.addEventListener('resize', handleResize)
    window.addEventListener('scroll', handleResize, { passive: true, capture: true })

    let ro: ResizeObserver | null = null
    if (frameRef.current && typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(() => scheduleSyncRect())
      ro.observe(frameRef.current)
    }

    return () => {
      mountedRef.current = false
      if (syncRafRef.current != null) {
        cancelAnimationFrame(syncRafRef.current)
        syncRafRef.current = null
      }
      window.removeEventListener('resize', handleResize)
      window.removeEventListener('scroll', handleResize, true)
      ro?.disconnect()
      // Don't hide on unmount: AIShellLayout's active-tab effect already hides
      // inactive browser sessions, and unmount-time hide would race with it
      // (unmount hide + tab-switch hide + new session open).
    }
  }, [scheduleSyncRect, sessionId])

  // When currentUrl changes after mount: open or hide the child window.
  const firstUrlRef = useRef(true)
  useEffect(() => {
    if (firstUrlRef.current) {
      firstUrlRef.current = false
      return
    }
    if (skipNavigateRef.current) {
      skipNavigateRef.current = false
      return
    }
    if (!currentUrl) {
      void setBrowserWindowVisible(sessionId, false)
      return
    }
    void openRef.current()
  }, [currentUrl, sessionId])

  useEffect(() => {
    setInput(currentUrl)
  }, [currentUrl])

  const loadInstances = useCallback(async () => {
    try {
      const items = await listBrowserWindows()
      setInstances(items)
    } catch (e) {
      // Silent failure; the newtab is optional enhancement.
    }
  }, [])

  useEffect(() => {
    void loadInstances()
    const cancel = onBrowserManagerEvent((_e: BrowserManagerEvent) => {
      void loadInstances()
    })
    return cancel
  }, [loadInstances])

  const showToast = useCallback((msg: string) => {
    setToast(msg)
    if (toastTimer.current) clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToast(null), 3000)
  }, [])

  useEffect(() => {
    return () => { if (toastTimer.current) clearTimeout(toastTimer.current) }
  }, [])

  const closeContextMenu = useCallback(() => setContextMenu(null), [])

  // Close the context menu on outside click / Esc (same pattern as RightPanelTabs).
  useEffect(() => {
    if (!contextMenu) return
    const handleClick = (e: MouseEvent) => {
      if (contextMenuRef.current && !contextMenuRef.current.contains(e.target as Node)) {
        closeContextMenu()
      }
    }
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeContextMenu()
    }
    document.addEventListener('mousedown', handleClick, true)
    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('mousedown', handleClick, true)
      document.removeEventListener('keydown', handleKey)
    }
  }, [contextMenu, closeContextMenu])

  const handleCardContextMenu = useCallback((e: React.MouseEvent, instanceId: string) => {
    e.preventDefault()
    e.stopPropagation()
    setContextMenu({ x: e.clientX, y: e.clientY, instanceId })
  }, [])

  // Cookie JSON export follows the FileBrowser download pattern: blob ->
  // objectURL -> <a download>.
  const handleExportCookies = useCallback(async (instanceId: string) => {
    if (cookieAction !== 'idle') return
    setCookieAction('exporting')
    closeContextMenu()
    try {
      const cookies = await exportBrowserCookies(instanceId)
      const json = JSON.stringify(cookies, null, 2)
      const blob = new Blob([json], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `cookies-${instanceId}-${Date.now()}.json`
      a.click()
      URL.revokeObjectURL(url)
      showToast(t('browserView.exportCookiesSuccess'))
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      showToast(`${t('browserView.exportCookiesFailed')}: ${msg}`)
    } finally {
      setCookieAction('idle')
    }
  }, [cookieAction, closeContextMenu, showToast, t])

  const triggerImportCookies = useCallback((instanceId: string) => {
    if (cookieAction !== 'idle') return
    closeContextMenu()
    if (fileInputRef.current) {
      fileInputRef.current.dataset.instanceId = instanceId
      fileInputRef.current.value = ''
      fileInputRef.current.click()
    }
  }, [cookieAction, closeContextMenu])

  // Shared by the hidden file input and drag-and-drop: parse (sporemind JSON /
  // EditThisCookie JSON / Netscape cookies.txt, see cookieImport.ts) then import.
  const importCookieFile = useCallback(async (instanceId: string, file: File) => {
    if (cookieAction !== 'idle') return
    setCookieAction('importing')
    try {
      const text = await file.text()
      const { result, skipped } = parseCookieFile(text)
      if (skipped === -1) {
        showToast(t('browserView.importCookiesInvalidFormat'))
        return
      }
      if (Object.keys(result).length === 0) {
        showToast(skipped > 0 ? t('browserView.importCookiesEmpty') : t('browserView.importCookiesInvalidFormat'))
        return
      }
      const imported = await importBrowserCookies(instanceId, result)
      showToast(t('browserView.importCookiesSuccess', { count: String(imported), skipped: String(skipped) }))
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      showToast(`${t('browserView.importCookiesFailed')}: ${msg}`)
    } finally {
      setCookieAction('idle')
    }
  }, [cookieAction, showToast, t])

  const handleFileSelected = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    const instanceId = e.target.dataset.instanceId
    e.target.value = ''
    if (!file || !instanceId) return
    await importCookieFile(instanceId, file)
  }, [importCookieFile])

  const openCreateModal = useCallback(() => {
    setEditingInstance(null)
    setFormName('')
    setFormUrl('')
    setFormProxy('')
    setFormProxyMode('system')
    setFormScope(defaultCreateScope ?? 'normal')
    setIsModalOpen(true)
  }, [defaultCreateScope])

  const openEditModal = useCallback((inst: BrowserInstance) => {
    setEditingInstance(inst)
    setFormName(inst.Config.Name)
    setFormUrl(inst.Config.Url)
    setFormProxy(inst.Config.Proxy)
    setFormProxyMode(effectiveProxyMode(inst.Config))
    setIsModalOpen(true)
  }, [])

  const closeModal = useCallback(() => {
    setIsModalOpen(false)
    setEditingInstance(null)
  }, [])

  const openDeleteConfirm = useCallback((inst: BrowserInstance) => {
    setConfirmDelete(inst)
  }, [])

  const closeDeleteConfirm = useCallback(() => {
    setConfirmDelete(null)
  }, [])

  const handleConfirmDelete = useCallback(async () => {
    if (!confirmDelete) return
    try {
      await closeBrowserWindow(confirmDelete.Config.Id)
      await loadInstances()
    } catch (e) {
      // ignore
    } finally {
      setConfirmDelete(null)
    }
  }, [confirmDelete, loadInstances])

  const normalizeInputUrl = useCallback((raw: string) => {
    const trimmed = raw.trim()
    if (!trimmed) return ''
    return normalizeUrl(trimmed)
  }, [])

  const handleModalSubmit = useCallback(async () => {
    const name = formName.trim()
    const raw = formUrl.trim()
    if (!raw) return
    if (!editingInstance && formScope === 'independent' && !name) return
    const url = normalizeInputUrl(raw)
    const proxy = formProxyMode === 'custom' ? formProxy.trim() : ''
    setSubmitting(true)
    try {
      if (editingInstance) {
        const cfg = { ...editingInstance.Config, Name: name, Url: url, Mode: 'tab', Proxy: proxy, ProxyMode: formProxyMode }
        await updateBrowserWindow({ Id: editingInstance.Config.Id, Config: cfg })
      } else if (formScope === 'normal') {
        onOpenBrowserTab?.(url)
      } else {
        const inst = await openBrowserWindow(name, url, proxy, formProxyMode)
        if (inst.Instance?.Config?.Id && onOpenRightTab) {
          onOpenRightTab(inst.Instance.Config.Id, 'independent', url, name)
        }
        onCloseSelf?.()
      }
      if (!editingInstance) {
        onSaveCreateDefaults?.(formScope)
      }
      await loadInstances()
      closeModal()
    } finally {
      setSubmitting(false)
    }
  }, [formName, formUrl, formProxy, formProxyMode, formScope, editingInstance, onOpenRightTab, onOpenBrowserTab, onSaveCreateDefaults, loadInstances, closeModal, normalizeInputUrl])

  const handleOpenInstanceTab = useCallback(async (inst: BrowserInstance) => {
    if (!inst.Status.Open) {
      await openBrowserInstance(inst.Config.Id)
    }
    const url = inst.Status.Url || inst.Config.Url
    if (onOpenRightTab) {
      onOpenRightTab(inst.Config.Id, 'independent', url, inst.Config.Name)
    }
    onCloseSelf?.()
  }, [onCloseSelf, onOpenRightTab])

  const onUrlChangeRef = useRef(onUrlChange)
  useEffect(() => {
    onUrlChangeRef.current = onUrlChange
  }, [onUrlChange])

  const onTitleChangeRef = useRef(onTitleChange)
  useEffect(() => {
    onTitleChangeRef.current = onTitleChange
  }, [onTitleChange])

  const onFaviconChangeRef = useRef(onFaviconChange)
  useEffect(() => {
    onFaviconChangeRef.current = onFaviconChange
  }, [onFaviconChange])

  const onBgColorChangeRef = useRef(onBgColorChange)
  useEffect(() => {
    onBgColorChangeRef.current = onBgColorChange
  }, [onBgColorChange])

  useEffect(() => {
    onUrlChangeRef.current?.(currentUrl)
  }, [currentUrl])

  useEffect(() => {
    if (kind !== 'independent' || !currentUrl) return
    void updateBrowserWindowUrl(sessionId, currentUrl)
  }, [kind, sessionId, currentUrl])

  useEffect(() => {
    // Visibility is owned by the backend state machine (wanted + main-window
    // active + painted + resize flag). These events only drive the placeholder
    // state and final bounds sync so that right-panel handle drags (pure DOM
    // resizes) do not trigger hide/show cycles.
    const offChanging = onWailsEvent('browser:layout-changing', () => {
      setRepositioning(true)
    })
    const offSettled = onWailsEvent('browser:layout-settled', async () => {
      setRepositioning(false)
      await syncRect()
    })
    const offShown = onWailsEvent(`browser:shown:${sessionId}`, () => {
      // `shown` only marks the native child window as on screen. It is a pure
      // visibility signal: it does not drive loading or the address bar, only
      // the loading-overlay cover (overlay = loading || !shown).
      setShown(true)
    })
    // browser:state is the SOLE navigation status / address-bar projection
    // (contract §5). The frontend derives loading (= status == loading), the
    // address bar (= confirmedURL), the title and the error indicator
    // exclusively from this event. The legacy navigated / navigation-error /
    // page-loaded events are NOT listened to — they are transition-compat on
    // the backend side only and must not drive frontend state.
    const offState = onWailsEvent(`browser:state:${sessionId}`, (event: unknown) => {
      const s = (event as { data?: { status?: string; confirmedURL?: string; attemptedURL?: string; title?: string; error?: string } })?.data
      if (!s) return
      // loading = (status == loading) — the sole projection.
      setLoading(s.status === 'loading')
      // Address bar binds to confirmedURL only (contract §5 / §10.B.5: no
      // optimistic write). The backend already navigated, so suppress the
      // currentUrl re-open effect.
      if (typeof s.confirmedURL === 'string' && s.confirmedURL && !isBrowserInternalURL(s.confirmedURL)) {
        skipNavigateRef.current = true
        setCurrentUrl(s.confirmedURL)
        // `input` syncs to currentUrl via the existing useEffect.
      }
      // Failed navigation: the webview is sitting on the attempted URL's error
      // page, so the address bar must render attemptedURL (not the stale
      // confirmedURL) to faithfully reflect the page state. Same no-reopen
      // suppression as confirmedURL.
      if (s.status === 'failed' && typeof s.attemptedURL === 'string' && s.attemptedURL && !isBrowserInternalURL(s.attemptedURL)) {
        skipNavigateRef.current = true
        setCurrentUrl(s.attemptedURL)
      }
      // Error indicator: failed status surfaces the error; any other status
      // clears it.
      setNavigationError(s.status === 'failed' ? (typeof s.error === 'string' ? s.error : '') : null)
      // Title projection (independent tabs persist their own title via the
      // backend; for global/app tabs surface it to the parent).
      if (s.title && kind !== 'independent') {
        onTitleChangeRef.current?.(s.title)
      }
    })
    const offPageInfo = onWailsEvent(`browser:page-info:${sessionId}`, (event: unknown) => {
      // page-info carries favicon/background only (contract §5); title comes
      // from browser:state.
      const info = (event as { data?: { u?: string; ic?: string; bg?: string } })?.data
      if (info?.u && isBrowserInternalURL(info.u)) return
      if (info?.ic !== undefined) {
        onFaviconChangeRef.current?.(info.ic)
      }
      if (info?.bg !== undefined) {
        onBgColorChangeRef.current?.(info.bg)
      }
    })
    const offHistoryState = onWailsEvent(`browser:history-state:${sessionId}`, (event: unknown) => {
      const payload = (event as { data?: { canBack?: boolean; canForward?: boolean } })?.data
      if (payload) {
        setCanBack(payload.canBack ?? false)
        setCanForward(payload.canForward ?? false)
      }
    })
    const offNewTab = onWailsEvent(`browser:new-tab:${sessionId}`, (event: unknown) => {
      const url = (event as { data?: unknown })?.data
      if (typeof url === 'string' && url) {
        onOpenBrowserTab?.(url)
      }
    })
    return () => {
      offChanging?.()
      offSettled?.()
      offShown?.()
      offState?.()
      offPageInfo?.()
      offHistoryState?.()
      offNewTab?.()
    }
  }, [syncRect, sessionId])

  const canGoBack = canBack
  const canGoForward = canForward

  return (
    <div className="browser-view">
      {kind !== 'app' && (
        <div className="browser-toolbar">
          <div className="browser-nav-group">
            <button
              type="button"
              className="browser-toolbar-btn"
              onClick={handleBack}
              disabled={!canGoBack}
              title={t('browser.back')}
            >
              <ArrowLeft size={14} />
            </button>
            <button
              type="button"
              className="browser-toolbar-btn"
              onClick={handleForward}
              disabled={!canGoForward}
              title={t('browser.forward')}
            >
              <ArrowRight size={14} />
            </button>
            <button
              type="button"
              className="browser-toolbar-btn"
              onClick={handleReload}
              title="Reload"
            >
              <RotateCw size={14} className={loading ? 'spin' : ''} />
            </button>
            {navigationError && (
              <span
                className="browser-error-indicator"
                title={`Load failed: ${navigationError}`}
              >
                !
              </span>
            )}
            {kind === 'independent' && homeUrl && (
              <button
                type="button"
                className="browser-toolbar-btn"
                onClick={handleHome}
                title={t('browser.home')}
              >
                <Home size={14} />
              </button>
            )}
          </div>
          <form className="browser-address-bar" onSubmit={handleSubmit}>
            <Globe size={14} className="browser-address-icon" />
            <input
              type="text"
              className="browser-address-input"
              value={input}
              onChange={(e) => {
                setInput(e.target.value)
                setHistoryIndex(-1)
              }}
              onFocus={() => setHistoryOpen(true)}
              onBlur={() => {
                setHistoryOpen(false)
                setHistoryIndex(-1)
              }}
              onKeyDown={(e) => {
                if (!historyOpen || historyMatches.length === 0) return
                if (e.key === 'ArrowDown') {
                  e.preventDefault()
                  setHistoryIndex((i) => Math.min(i + 1, historyMatches.length - 1))
                } else if (e.key === 'ArrowUp') {
                  e.preventDefault()
                  setHistoryIndex((i) => Math.max(i - 1, 0))
                } else if (e.key === 'Escape') {
                  setHistoryOpen(false)
                }
              }}
              placeholder={t('browser.placeholder.url')}
              aria-autocomplete="list"
              aria-expanded={historyOpen && historyMatches.length > 0}
              role="combobox"
            />
            <button type="submit" className="browser-address-go" title="Go">
              <Search size={14} />
            </button>
            {historyOpen && historyMatches.length > 0 && (
              <div className="browser-history-dropdown" role="listbox" aria-label="Address history">
                {historyMatches.map((entry, i) => (
                  <button
                    key={`${entry.Url}|${entry.VisitedAt}|${i}`}
                    type="button"
                    role="option"
                    aria-selected={i === historyIndex}
                    className={`browser-history-item${i === historyIndex ? ' active' : ''}`}
                    // Keep focus in the input so the click lands before blur.
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => selectHistory(entry.Url)}
                    title={entry.Title || entry.Url}
                  >
                    <History size={13} className="browser-history-item-icon" />
                    <span className="browser-history-item-url">{entry.Url}</span>
                    {entry.Title && <span className="browser-history-item-title">{entry.Title}</span>}
                    <span className="browser-history-item-time">{formatHistoryTime(entry.VisitedAt)}</span>
                  </button>
                ))}
              </div>
            )}
          </form>
          <button
            type="button"
            className="browser-toolbar-btn"
            onClick={handleClose}
            title="Close browser window"
          >
            <X size={14} />
          </button>
        </div>
      )}

      <div ref={frameRef} className="browser-frame">
        {!currentUrl && !error ? (
          <div className="browser-newtab">
            <div className="browser-newtab-content">
              <div className="browser-newtab-grid">
                <button
                  type="button"
                  className="browser-newtab-card browser-newtab-add"
                  onClick={openCreateModal}
                  title={t('browserView.newInstance')}
                >
                  <div className="browser-newtab-add-icon">
                    <Plus size={28} />
                  </div>
                  <span className="browser-newtab-add-label">{t('browserView.new_')}</span>
                </button>
                {instances.map((inst) => (
                  <div
                    key={inst.Config.Id}
                    className={`browser-newtab-card ${inst.Status.Open ? 'open' : ''} ${dragOverInstance === inst.Config.Id ? 'drag-over' : ''}`}
                    onClick={() => handleOpenInstanceTab(inst)}
                    onContextMenu={(e) => handleCardContextMenu(e, inst.Config.Id)}
                    onDragOver={(e) => {
                      if (!e.dataTransfer.types.includes('Files')) return
                      e.preventDefault()
                      e.stopPropagation()
                      e.dataTransfer.dropEffect = 'copy'
                      setDragOverInstance(inst.Config.Id)
                    }}
                    onDragLeave={(e) => {
                      if (dragOverInstance !== inst.Config.Id) return
                      if (e.currentTarget.contains(e.relatedTarget as Node | null)) return
                      setDragOverInstance(null)
                    }}
                    onDrop={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      setDragOverInstance(null)
                      const file = e.dataTransfer.files?.[0]
                      if (file) void importCookieFile(inst.Config.Id, file)
                    }}
                    title={inst.Status.Open ? t('browserView.alreadyOpen') : t('browserView.openTab')}
                  >
                    <div className="browser-newtab-card-header">
                      <Globe size={16} />
                      <span className="browser-newtab-card-name" title={inst.Config.Name}>
                        {inst.Config.Name}
                      </span>
                      <div className="browser-newtab-card-tools">
                        <button
                          type="button"
                          className="browser-newtab-card-tool"
                          onClick={(e) => { e.stopPropagation(); openEditModal(inst) }}
                          title={t('browserView.edit')}
                        >
                          <Pencil size={14} />
                        </button>
                        <button
                          type="button"
                          className="browser-newtab-card-tool"
                          onClick={(e) => { e.stopPropagation(); triggerImportCookies(inst.Config.Id) }}
                          disabled={cookieAction !== 'idle'}
                          title={t('browserView.importCookies')}
                        >
                          <FileUp size={14} />
                        </button>
                        <button
                          type="button"
                          className="browser-newtab-card-tool danger"
                          onClick={(e) => { e.stopPropagation(); openDeleteConfirm(inst) }}
                          title={t('browserView.remove')}
                        >
                          <Trash2 size={14} />
                        </button>
                      </div>
                    </div>
                    <div className="browser-newtab-card-url" title={inst.Status.Url || inst.Config.Url}>
                      {inst.Status.Url || inst.Config.Url}
                    </div>
                    {inst.Status.Title && (
                      <div className="browser-newtab-card-title" title={inst.Status.Title}>
                        {inst.Status.Title}
                      </div>
                    )}
                    {(() => {
                      const pm = effectiveProxyMode(inst.Config)
                      if (pm === 'system') return null
                      return (
                        <div className="browser-newtab-card-proxy" title={t('browser.card.proxyBadge')}>
                          <Globe size={10} />
                          <span>{pm === 'custom' ? inst.Config.Proxy : t('browser.card.proxyNone')}</span>
                        </div>
                      )
                    })()}
                    {dragOverInstance === inst.Config.Id && (
                      <div className="browser-newtab-card-drop-hint">{t('browserView.importCookiesDropHint')}</div>
                    )}
                  </div>
                ))}
              </div>
              {instances.length === 0 && (
                <span className="browser-newtab-hint">{t('browserView.empty')}</span>
              )}
            </div>

            {contextMenu && (
              <div
                className="browser-card-ctx-menu"
                ref={contextMenuRef}
                style={{ left: Math.min(contextMenu.x, window.innerWidth - 200), top: Math.min(contextMenu.y, window.innerHeight - 72) }}
              >
                <button
                  className="browser-card-ctx-item"
                  onClick={() => handleExportCookies(contextMenu.instanceId)}
                  disabled={cookieAction !== 'idle'}
                >
                  <span className="browser-card-ctx-label">{t('browserView.exportCookies')}</span>
                </button>
                <button
                  className="browser-card-ctx-item"
                  onClick={() => triggerImportCookies(contextMenu.instanceId)}
                  disabled={cookieAction !== 'idle'}
                >
                  <span className="browser-card-ctx-label">{t('browserView.importCookies')}</span>
                </button>
              </div>
            )}

            <input
              type="file"
              accept=".json,.txt"
              ref={fileInputRef}
              className="browser-card-file-input"
              onChange={handleFileSelected}
              style={{ display: 'none' }}
            />

            {toast && <div className="browser-card-toast">{toast}</div>}

            {isModalOpen && (
              <div className="browser-modal-overlay">
                <div className="browser-modal">
                  <div className="browser-modal-header">
                    <span>{editingInstance ? t('browserView.editInstance') : t('browserView.createInstance')}</span>
                  </div>
                  <div className="browser-modal-body">
                    {!editingInstance && (
                      <div className="browser-modal-label">
                        <div className="browser-modal-row">
                          <div className="browser-modal-col">
                            <span>{t('browserView.type')}</span>
                            <div className="browser-newtab-scope">
                              <button
                                type="button"
                                className={`browser-newtab-mode-btn ${formScope === 'normal' ? 'active' : ''}`}
                                onClick={() => setFormScope('normal')}
                                title={t('browserView.normalTab')}
                              >
                                {t('browserView.normal')}
                              </button>
                              <button
                                type="button"
                                className={`browser-newtab-mode-btn ${formScope === 'independent' ? 'active' : ''}`}
                                onClick={() => setFormScope('independent')}
                                title={t('browserView.saveToList')}
                              >
                                {t('browserView.independent')}
                              </button>
                            </div>
                          </div>
                        </div>
                      </div>
                    )}
                    {(editingInstance || formScope === 'independent') && (
                      <label className="browser-modal-label">
                        {t('browserView.name')}
                        <input
                          type="text"
                          className="browser-modal-input"
                          placeholder={t('browser.placeholder.name')}
                          value={formName}
                          onChange={(e) => setFormName(e.target.value)}
                          onKeyDown={(e) => e.key === 'Enter' && handleModalSubmit()}
                        />
                      </label>
                    )}
                    <label className="browser-modal-label">
                      {t('browserView.url')}
                      <input
                        type="text"
                        className="browser-modal-input"
                        placeholder={t('browser.placeholder.urlOrSearch')}
                        value={formUrl}
                        onChange={(e) => setFormUrl(e.target.value)}
                        onKeyDown={(e) => e.key === 'Enter' && handleModalSubmit()}
                      />
                    </label>
                    {(editingInstance || formScope === 'independent') && (
                      <div className="browser-modal-label">
                        {t('browser.dialog.proxy')}
                        <div className="browser-newtab-mode">
                          <button
                            type="button"
                            className={`browser-newtab-mode-btn ${formProxyMode === 'system' ? 'active' : ''}`}
                            onClick={() => setFormProxyMode('system')}
                            title={t('browser.proxyMode.systemTitle')}
                          >
                            {t('browser.proxyMode.system')}
                          </button>
                          <button
                            type="button"
                            className={`browser-newtab-mode-btn ${formProxyMode === 'none' ? 'active' : ''}`}
                            onClick={() => setFormProxyMode('none')}
                            title={t('browser.proxyMode.noneTitle')}
                          >
                            {t('browser.proxyMode.none')}
                          </button>
                          <button
                            type="button"
                            className={`browser-newtab-mode-btn ${formProxyMode === 'custom' ? 'active' : ''}`}
                            onClick={() => setFormProxyMode('custom')}
                            title={t('browser.proxyMode.customTitle')}
                          >
                            {t('browser.proxyMode.custom')}
                          </button>
                        </div>
                        {formProxyMode === 'custom' && (
                          <input
                            type="text"
                            className="browser-modal-input"
                            placeholder={t('browser.placeholder.proxyUrl')}
                            value={formProxy}
                            onChange={(e) => setFormProxy(e.target.value)}
                            onKeyDown={(e) => e.key === 'Enter' && handleModalSubmit()}
                          />
                        )}
                      </div>
                    )}
                  </div>
                  <div className="browser-modal-footer">
                    <button type="button" className="browser-modal-btn secondary" onClick={closeModal}>
                      {t('browserView.cancel')}
                    </button>
                    <button
                      type="button"
                      className="browser-modal-btn primary"
                      onClick={handleModalSubmit}
                      disabled={submitting || !formUrl.trim() || (editingInstance || formScope === 'independent' ? !formName.trim() : false)}
                    >
                      {submitting ? t('browserView.saving') : t('browserView.save')}
                    </button>
                  </div>
                </div>
              </div>
            )}

            {confirmDelete && (
              <div className="browser-modal-overlay" onClick={closeDeleteConfirm}>
                <div className="browser-modal" onClick={(e) => e.stopPropagation()}>
                  <div className="browser-modal-header">
                    <span>{t('browserView.deleteConfirmTitle')}</span>
                  </div>
                  <div className="browser-modal-body">
                    <p>{t('browserView.deleteConfirmMessage', { name: confirmDelete.Config.Name })}</p>
                  </div>
                  <div className="browser-modal-footer">
                    <button type="button" className="browser-modal-btn secondary" onClick={closeDeleteConfirm}>
                      {t('browserView.cancel')}
                    </button>
                    <button type="button" className="browser-modal-btn danger" onClick={handleConfirmDelete}>
                      {t('browserView.delete')}
                    </button>
                  </div>
                </div>
              </div>
            )}
          </div>
        ) : error ? (
          <div className="browser-error">{error}</div>
        ) : repositioning ? (
          <div className="browser-placeholder">
            <span className="browser-placeholder-text">Repositioning…</span>
          </div>
        ) : loading || !shown ? (
          <>
            {/* The overlay covers the frame while a navigation is in progress
                (loading) OR until the native window is shown (!shown). `loading`
                is navigation status; `shown` is native-window visibility; the two
                are decoupled so the initial-load gap between NavigationCompleted
                and first paint never shows a blank frame. */}
            <div className="browser-placeholder">
              <div className="browser-loading">
                <div className="browser-loading-dot" />
                <div className="browser-loading-dot" />
                <div className="browser-loading-dot" />
              </div>
              <span className="browser-placeholder-text">{t('browserView.loading')}</span>
            </div>
          </>
        ) : null}
        {snapshot && <img className="browser-snapshot" src={snapshot} alt="" draggable={false} decoding="sync" />}
      </div>
    </div>
  )
}
