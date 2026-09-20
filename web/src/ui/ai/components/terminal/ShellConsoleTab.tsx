import { useCallback, useEffect, useRef, useState } from 'react'
import { Terminal as XtermTerminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import { Copy, ClipboardPaste, Eraser, Search, TextSelect, ChevronUp, ChevronDown, X } from 'lucide-react'

import '@xterm/xterm/css/xterm.css'
import { client } from '../../../../application/generated-client'
import * as shellClient from '../../../../gen-clients/shell/client'
import type { ShellSessionOutputEvent } from '../../../../gen-clients/system/types'
import { useI18n } from '../../../../i18n'
import { useMenuDismiss } from '../../hooks/useMenuDismiss'
import { useBrowserOverlay } from '../../browserOverlay'
import type { TerminalTabBodyProps } from './tabRegistry'

/**
 * Real interactive shell console for the terminal panel's `shell` tab type.
 *
 * Lifecycle:
 *   mount → xterm(FitAddon + SearchAddon, SSH-view theme) → fit →
 *   subscribe to shell.session_output (service-level, filtered by SessionId)
 *   before opening the session, so no prompt output is lost in the window
 *   between session_open and subscription establishment →
 *   shell.session_open(cols/rows from the fitted geometry) →
 *   shell.session_resize(initial fit) →
 *   shell.session_fetch(0) replay from the per-session ring buffer, applying
 *   each chunk idempotently by its monotonic Idx.
 *   keystrokes → term.onData → shell.session_write
 *   ResizeObserver + debounce → fit → shell.session_resize
 *   unmount/close → unsubscribe events, dispose onData, shell.session_close,
 *   dispose the terminal.
 *
 * A session that exits (kind=exit) marks the tab as exited: an ANSI banner is
 * written into the scrollback, an HTML banner strip is shown, and the tab
 * title gets an "exited" suffix via onSetTabTitle. Reopening the tab starts a
 * brand-new session and clears the stale suffix.
 */

// Below these sizes the container is effectively hidden (collapsed panel /
// mid-transition); fitting then clamps cols to 2 and lossy-reflows the buffer.
const MIN_FIT_WIDTH_PX = 64
const MIN_FIT_HEIGHT_PX = 24

/** ANSI wrap decorating pipe-mode stderr chunks so they stand out from stdout. */
const STDERR_ANSI_OPEN = '\x1b[31m'
const STDERR_ANSI_CLOSE = '\x1b[0m'
const DIM_ANSI_OPEN = '\x1b[90m'
const DIM_ANSI_CLOSE = '\x1b[0m'

/** Maximum events buffered while the session id is still unknown. */
const MAX_PENDING_EVENTS = 64

/** Search highlight colors (same palette as the SSH terminal search). */
const SEARCH_DECORATIONS = {
  matchBackground: '#4d3a00',
  matchBorder: '#888800',
  matchOverviewRuler: '#aaaa00',
  activeMatchBackground: '#cc8800',
  activeMatchBorder: '#ffaa00',
  activeMatchColorOverviewRuler: '#ffaa00',
}

export function ShellConsoleTab({ tab, onSetTabTitle, projectRoot }: TerminalTabBodyProps) {
  const { t } = useI18n()
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XtermTerminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const sessionIdRef = useRef<string | null>(null)
  const onSetTabTitleRef = useRef(onSetTabTitle)
  onSetTabTitleRef.current = onSetTabTitle
  const exitedRef = useRef(false)
  const [exited, setExited] = useState(false)
  const [exitInfo, setExitInfo] = useState<{ code: number; reason: string } | null>(null)
  const [openError, setOpenError] = useState<string | null>(null)
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; hasSelection: boolean } | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [searchResultCount, setSearchResultCount] = useState(0)
  const [searchResultIndex, setSearchResultIndex] = useState(-1)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  const searchInputRef = useRef<HTMLInputElement>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

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

  const handleTermContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    setContextMenu({ x: e.clientX, y: e.clientY, hasSelection: !!termRef.current?.getSelection() })
  }, [])

  const handleCopySelection = useCallback(async () => {
    const sel = termRef.current?.getSelection()
    if (!sel) {
      showToast(t('terminalPanel.shell.noSelection'))
      return
    }
    try {
      await navigator.clipboard.writeText(sel)
      termRef.current?.clearSelection()
      showToast(t('terminalPanel.shell.selectionCopied'))
    } catch {
      showToast(t('terminalPanel.shell.clipboardFailed'))
    }
  }, [showToast, t])

  const handlePaste = useCallback(async () => {
    try {
      const text = await navigator.clipboard.readText()
      if (!text) return
      const sid = sessionIdRef.current
      if (!sid || exitedRef.current) return
      void shellClient.sessionWrite(client, { SessionId: sid, Data: text }).catch(() => {})
      termRef.current?.focus()
      showToast(t('terminalPanel.shell.pasted'))
    } catch {
      showToast(t('terminalPanel.shell.clipboardFailed'))
    }
  }, [showToast, t])

  const handleClearTerminal = useCallback(() => {
    termRef.current?.clear()
    termRef.current?.focus()
  }, [])

  const handleSelectAll = useCallback(() => {
    termRef.current?.selectAll()
  }, [])

  // ---- terminal search overlay (SearchAddon) ----

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

  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    let cancelled = false
    let unsubscribe: (() => void) | null = null
    exitedRef.current = false
    setExited(false)
    setExitInfo(null)
    setOpenError(null)

    // Initialise xterm.js exactly like SshSessionView: FitAddon + SearchAddon,
    // same font/scrollback/transparency options, same app-background theme sync.
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
    // Reset stale result counters from a previous terminal instance.
    setSearchResultCount(0)
    setSearchResultIndex(-1)
    // Track match count/index reported by the addon so the overlay can show
    // a live "x / y" indicator. resultIndex is -1 when the highlight limit
    // (1000) is exceeded.
    const searchResultsDisp = searchAddon.onDidChangeResults((e) => {
      setSearchResultCount(e.resultCount)
      setSearchResultIndex(e.resultIndex)
    })
    // Ctrl/Cmd+F opens the search overlay instead of the browser's find bar.
    term.attachCustomKeyEventHandler((ev) => {
      if (
        ev.type === 'keydown' &&
        (ev.ctrlKey || ev.metaKey) &&
        !ev.altKey &&
        ev.key.toLowerCase() === 'f'
      ) {
        ev.preventDefault()
        setSearchOpen(true)
        return false
      }
      return true
    })
    term.open(container)
    // Guard: a collapsed/transitioning container still reports a few px of
    // size; fit() would clamp cols to 2 and lossy-reflow the buffer.
    const containerTooSmall = () =>
      container.clientWidth < MIN_FIT_WIDTH_PX || container.clientHeight < MIN_FIT_HEIGHT_PX
    if (!containerTooSmall()) {
      try {
        fit.fit()
      } catch {
        /* container gone */
      }
    }
    termRef.current = term
    fitRef.current = fit

    // While the app background image is active, make the terminal canvas fully
    // transparent so the image shows through (same as the SSH view; the dark
    // tint lives on .terminal-shell-body).
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

    const markExited = (ev: ShellSessionOutputEvent) => {
      exitedRef.current = true
      setExited(true)
      setExitInfo({ code: ev.ExitCode ?? 0, reason: ev.Data })
      term.write(
        `\r\n${DIM_ANSI_OPEN}[${t('terminalPanel.shell.exitBanner', { code: ev.ExitCode ?? 0 })}]${DIM_ANSI_CLOSE}\r\n`,
      )
      onSetTabTitleRef.current?.(tab.id, `${t('terminalPanel.tab.shell')}${t('terminalPanel.shell.exitedSuffix')}`)
    }

    // Idempotent output application: each session assigns a monotonically
    // increasing Idx; replay and live events are de-duplicated by it.
    let lastIdx = -1
    const pendingEvents: ShellSessionOutputEvent[] = []

    const applyEvent = (ev: ShellSessionOutputEvent) => {
      if (ev.SessionId !== sessionIdRef.current) return
      if (typeof ev.Idx !== 'number') return
      if (ev.Idx <= lastIdx) return
      lastIdx = ev.Idx
      if (ev.Kind === 'stdout') {
        term.write(ev.Data)
      } else if (ev.Kind === 'stderr') {
        // Pipe mode only (pty merges stderr into stdout): red decoration.
        term.write(`${STDERR_ANSI_OPEN}${ev.Data}${STDERR_ANSI_CLOSE}`)
      } else if (ev.Kind === 'exit') {
        markExited(ev)
      }
    }

    const flushPendingEvents = () => {
      const sid = sessionIdRef.current
      if (!sid) return
      for (const ev of pendingEvents) {
        if (ev.SessionId === sid) applyEvent(ev)
      }
      pendingEvents.length = 0
    }

    const onOutputEvent = (ev: ShellSessionOutputEvent) => {
      if (sessionIdRef.current === null) {
        // The session id is not known yet (subscription was established
        // before session_open). Buffer a small window of events from all
        // sessions and drain the ones belonging to our session once the id
        // arrives. fetch(0) covers the bulk of history; this buffer catches
        // any live events that race ahead of the open response.
        pendingEvents.push(ev)
        if (pendingEvents.length > MAX_PENDING_EVENTS) {
          pendingEvents.shift()
        }
        return
      }
      applyEvent(ev)
    }

    // Subscribe *before* opening the session so no window-of-creation output
    // is lost. The handler is service-level; we filter by session id below.
    unsubscribe = shellClient.OnShellSessionOutput(client, onOutputEvent)

    void (async () => {
      try {
        const resp = await shellClient.sessionOpen(client, {
          Cols: term.cols,
          Rows: term.rows,
          WorkingDirectory: projectRoot,
        })
        if (cancelled) {
          // Unmounted while opening: close the freshly opened session so it
          // does not leak a shell process.
          void shellClient.sessionClose(client, { SessionId: resp.SessionId }).catch(() => {})
          return
        }
        const sessionId = resp.SessionId
        sessionIdRef.current = sessionId
        // Reopening an exited tab starts a fresh session — drop the stale
        // "exited" title suffix so the tab bar falls back to the default label.
        onSetTabTitleRef.current?.(tab.id, undefined)
        // Send the initial geometry so the PTY matches the fitted xterm size.
        void shellClient.sessionResize(client, {
          SessionId: sessionId,
          Cols: term.cols,
          Rows: term.rows,
        }).catch(() => { /* session may be closing */ })
        // Replay any chunks emitted before/during the subscription window.
        // Backend returns the whole retained ring in one call (NextIdx is the
        // next ordinal to expect from live events; Truncated just means some
        // older history was overwritten).
        const replay = await shellClient.sessionFetch(client, {
          SessionId: sessionId,
          FromIdx: 0,
        })
        for (const chunk of replay.Chunks) {
          applyEvent(chunk)
        }
        // Apply any live events that arrived before we knew our session id.
        flushPendingEvents()
      } catch (e) {
        if (!cancelled) {
          const msg = e instanceof Error ? e.message : String(e)
          setOpenError(msg)
          term.write(
            `\r\n${STDERR_ANSI_OPEN}${t('terminalPanel.shell.openError')}: ${msg}${STDERR_ANSI_CLOSE}\r\n`,
          )
        }
        if (unsubscribe) {
          unsubscribe()
          unsubscribe = null
        }
      }
    })()

    // Forward keystrokes to the session stdin. Writes are dropped while no
    // session exists yet or after the session has exited.
    const inputDisp = term.onData((data) => {
      const sid = sessionIdRef.current
      if (!sid || exitedRef.current) return
      void shellClient.sessionWrite(client, { SessionId: sid, Data: data }).catch(() => {})
    })

    // Debounce + suppress spurious resize: the panel's CSS transitions fire
    // many ResizeObserver callbacks as the container animates. Each fit+resize
    // sends SIGWINCH to the PTY; debounce and skip identical geometry (same
    // technique as SshSessionView).
    let resizeTimer: ReturnType<typeof setTimeout> | null = null
    let lastSentCols = term.cols
    let lastSentRows = term.rows
    const sendResize = () => {
      const sid = sessionIdRef.current
      if (!sid) return
      void shellClient.sessionResize(client, {
        SessionId: sid,
        Cols: term.cols,
        Rows: term.rows,
      }).catch(() => { /* session may be closing */ })
    }
    const resizeObs = new ResizeObserver(() => {
      if (containerTooSmall()) return
      if (resizeTimer) clearTimeout(resizeTimer)
      resizeTimer = setTimeout(() => {
        resizeTimer = null
        // Re-check inside the debounced callback: the container may have
        // shrunk back below the threshold during the debounce window.
        if (containerTooSmall()) return
        try {
          fit.fit()
          if (
            term.cols >= 10 && term.rows >= 2 &&
            (term.cols !== lastSentCols || term.rows !== lastSentRows)
          ) {
            lastSentCols = term.cols
            lastSentRows = term.rows
            sendResize()
          }
        } catch {
          /* container gone */
        }
      }, 80)
    })
    resizeObs.observe(container)

    term.focus()

    return () => {
      cancelled = true
      inputDisp.dispose()
      searchResultsDisp.dispose()
      if (resizeTimer) clearTimeout(resizeTimer)
      resizeObs.disconnect()
      appBgObserver.disconnect()
      if (unsubscribe) {
        unsubscribe()
        unsubscribe = null
      }
      const sid = sessionIdRef.current
      if (sid) {
        sessionIdRef.current = null
        void shellClient.sessionClose(client, { SessionId: sid }).catch(() => {
          /* session already exited server-side */
        })
      }
      term.dispose()
      termRef.current = null
      fitRef.current = null
      searchAddonRef.current = null
    }
  }, [tab.id, t])

  return (
    <div className="terminal-shell-body" onContextMenu={handleTermContextMenu}>
      <div className="terminal-shell-xterm" ref={containerRef} />
      {openError && (
        <div className="terminal-shell-error" role="alert">
          {t('terminalPanel.shell.openError')}
        </div>
      )}
      {exited && exitInfo && (
        <div className="terminal-shell-exit" role="status">
          <span className="terminal-shell-exit-banner">
            {t('terminalPanel.shell.exitBanner', { code: exitInfo.code })}
          </span>
          <span className="terminal-shell-exit-hint">{t('terminalPanel.shell.exitHint')}</span>
        </div>
      )}
      {toast && <div className="terminal-shell-toast">{toast}</div>}
      {searchOpen && (
        <div className="terminal-shell-search-overlay">
          <input
            ref={searchInputRef}
            className="terminal-shell-search-input"
            value={searchQuery}
            placeholder={t('terminalPanel.shell.searchPlaceholder')}
            spellCheck={false}
            onChange={(e) => onSearchChange(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') { e.preventDefault(); closeSearch() }
              else if (e.key === 'Enter') { e.preventDefault(); runSearch(e.shiftKey ? 'prev' : 'next') }
            }}
          />
          <span className="terminal-shell-search-count">
            {searchQuery && searchResultCount > 0
              ? (searchResultIndex >= 0
                ? t('terminalPanel.shell.searchResultCount', { index: searchResultIndex + 1, total: searchResultCount })
                : t('terminalPanel.shell.searchManyResults', { total: searchResultCount }))
              : (searchQuery ? t('terminalPanel.shell.searchNoResults') : '')
            }
          </span>
          <button
            className="terminal-shell-search-btn"
            title={t('terminalPanel.shell.searchPrev')}
            onClick={() => runSearch('prev')}
          >
            <ChevronUp size={13} />
          </button>
          <button
            className="terminal-shell-search-btn"
            title={t('terminalPanel.shell.searchNext')}
            onClick={() => runSearch('next')}
          >
            <ChevronDown size={13} />
          </button>
          <button
            className="terminal-shell-search-btn"
            title={t('terminalPanel.shell.searchClose')}
            onClick={closeSearch}
          >
            <X size={13} />
          </button>
        </div>
      )}
      {contextMenu && (
        <ShellConsoleContextMenu
          menu={contextMenu}
          onClose={() => setContextMenu(null)}
          onCopy={handleCopySelection}
          onPaste={handlePaste}
          onClear={handleClearTerminal}
          onFind={toggleSearch}
          onSelectAll={handleSelectAll}
        />
      )}
    </div>
  )
}

interface ShellConsoleContextMenuProps {
  menu: { x: number; y: number; hasSelection: boolean }
  onClose: () => void
  onCopy: () => void
  onPaste: () => void
  onClear: () => void
  onFind: () => void
  onSelectAll: () => void
}

function ShellConsoleContextMenu({ menu, onClose, onCopy, onPaste, onClear, onFind, onSelectAll }: ShellConsoleContextMenuProps) {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)
  useMenuDismiss(menuRef, onClose, menu)
  useBrowserOverlay(true)

  const items = [
    { icon: Copy, label: t('terminalPanel.shell.copy'), action: onCopy, disabled: !menu.hasSelection },
    { icon: ClipboardPaste, label: t('terminalPanel.shell.paste'), action: onPaste, disabled: false },
    { icon: Eraser, label: t('terminalPanel.shell.clear'), action: onClear, disabled: false },
    { icon: TextSelect, label: t('terminalPanel.shell.selectAll'), action: onSelectAll, disabled: false },
    { icon: Search, label: t('terminalPanel.shell.find'), action: onFind, disabled: false },
  ]

  return (
    <div
      ref={menuRef}
      className="terminal-shell-context-menu"
      style={{ left: menu.x, top: menu.y }}
      onClick={(e) => e.stopPropagation()}
    >
      {items.map((item) => {
        const Icon = item.icon
        return (
          <button
            key={item.label}
            className="terminal-shell-context-menu-item"
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
