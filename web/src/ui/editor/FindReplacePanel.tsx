import React, { useCallback, useEffect, useRef, useState } from 'react'
import { EditorView } from '@codemirror/view'
import { StateEffect } from '@codemirror/state'
import {
  SearchQuery,
  findNext,
  findPrevious,
  getSearchQuery,
  openSearchPanel,
  replaceAll,
  replaceNext,
  searchPanelOpen,
  setSearchQuery,
} from '@codemirror/search'
import { ChevronDown, ChevronUp, X } from 'lucide-react'
import { useBrowserOverlay } from '../ai/browserOverlay'
import { useI18n } from '../../i18n'
import './FindReplacePanel.css'

/**
 * IDEA-style local find/replace floating panel.
 *
 * Takes over CodeMirror's search state: the panel drives `SearchQuery` via
 * `setSearchQuery` and runs `findNext` / `findPrevious` / `replaceNext` /
 * `replaceAll` directly against the view. The built-in CM search panel is
 * suppressed at the extension level (see editing-extensions.ts), so this
 * component is the only find/replace UI for the editor.
 *
 * While open the panel registers with `useBrowserOverlay` so it is never
 * occluded by the embedded native browser window.
 */

/** Panel mode: find only (Ctrl+F) or find + replace (Ctrl+R), IDEA-style. */
export type FindReplaceMode = 'find' | 'replace'

/** Opens the panel in the given mode (bound to Ctrl+F / Ctrl+R in the editor keymap). */
export type FindReplaceOpener = (mode: FindReplaceMode) => void

export interface FindReplacePanelProps {
  view: EditorView | null
  open: boolean
  mode: FindReplaceMode
  /** Bumped on every open request so re-pressing Ctrl+F refocuses the find input. */
  focusKey: number
  /** Whether the document is writable — replace actions are disabled when false. */
  editable: boolean
  onClose: () => void
  onModeChange: (mode: FindReplaceMode) => void
}

export interface FindReplacePanelApi {
  open: boolean
  mode: FindReplaceMode
  focusKey: number
  openPanel: FindReplaceOpener
  closePanel: () => void
  /** For the editor Escape binding: closes the panel, reports whether it was open. */
  closePanelOnEscape: () => boolean
  setMode: (mode: FindReplaceMode) => void
}

interface PanelState {
  open: boolean
  mode: FindReplaceMode
  focusKey: number
}

/**
 * Open/close state for one FindReplacePanel instance. Editors keep this hook
 * next to their EditorView and pass the values down to the panel.
 */
export function useFindReplacePanel(): FindReplacePanelApi {
  const [state, setState] = useState<PanelState>({ open: false, mode: 'find', focusKey: 0 })
  const openRef = useRef(false)
  const openPanel = useCallback((mode: FindReplaceMode) => {
    openRef.current = true
    setState(prev => ({ open: true, mode, focusKey: prev.focusKey + 1 }))
  }, [])
  const closePanel = useCallback(() => {
    openRef.current = false
    setState(prev => (prev.open ? { ...prev, open: false } : prev))
  }, [])
  const closePanelOnEscape = useCallback(() => {
    if (!openRef.current) return false
    openRef.current = false
    setState(prev => ({ ...prev, open: false }))
    return true
  }, [])
  const setMode = useCallback((mode: FindReplaceMode) => {
    setState(prev => ({ ...prev, mode }))
  }, [])
  return {
    open: state.open,
    mode: state.mode,
    focusKey: state.focusKey,
    openPanel,
    closePanel,
    closePanelOnEscape,
    setMode,
  }
}

export interface MatchInfo {
  count: number
  activeIndex: number
  /** True when counting stopped at MATCH_COUNT_CAP (display "N+"). */
  capped: boolean
}

/** Matches beyond this count are not enumerated; the display shows "N+". */
export const MATCH_COUNT_CAP = 1000

/** Minimal shape of SearchCursor / RegExpCursor used by computeMatchInfo. */
interface QueryCursor {
  done: boolean
  next(): unknown
  value: { from: number; to: number }
}

const EMPTY_QUERY = new SearchQuery({ search: '' })

/**
 * Count matches of the view's current search query and locate the active one
 * (the match containing the cursor head, or the first match after it).
 */
export function computeMatchInfo(view: EditorView): MatchInfo {
  const query = getSearchQuery(view.state)
  if (!query.search || !query.valid) return { count: 0, activeIndex: 0, capped: false }
  const head = view.state.selection.main.head
  const cursor = query.getCursor(view.state, 0, view.state.doc.length) as unknown as QueryCursor
  let count = 0
  let activeIndex = -1
  let beforeHead = 0
  let capped = false
  while (!cursor.done) {
    cursor.next()
    if (cursor.done) break
    const { from, to } = cursor.value
    if (activeIndex < 0) {
      if (from <= head && head <= to) {
        activeIndex = count
      } else if (to < head) {
        beforeHead = count + 1
      }
    }
    count++
    if (count >= MATCH_COUNT_CAP) {
      capped = true
      break
    }
  }
  if (activeIndex < 0) activeIndex = Math.min(beforeHead, Math.max(count - 1, 0))
  return { count, activeIndex, capped }
}

/** Tracks views that already carry this panel's doc/selection listener. */
const panelViews = new WeakSet<EditorView>()

/** Max length of a selection that is seeded into the find field on open. */
const SEED_MAX_LEN = 200

export const FindReplacePanel: React.FC<FindReplacePanelProps> = ({
  view,
  open,
  mode,
  focusKey,
  editable,
  onClose,
  onModeChange,
}) => {
  const { t } = useI18n()
  useBrowserOverlay(open)

  const [search, setSearch] = useState('')
  const [replace, setReplace] = useState('')
  const [caseSensitive, setCaseSensitive] = useState(false)
  const [wholeWord, setWholeWord] = useState(false)
  const [regexp, setRegexp] = useState(false)
  const [matchInfo, setMatchInfo] = useState<MatchInfo>({ count: 0, activeIndex: 0, capped: false })

  const findInputRef = useRef<HTMLInputElement>(null)
  const replaceInputRef = useRef<HTMLInputElement>(null)
  const notifyRef = useRef<() => void>(() => {})

  const buildQuery = useCallback(() => new SearchQuery({
    search,
    replace,
    caseSensitive,
    // Non-regex searches are true literal searches (no \n/\t interpolation),
    // matching IDEA semantics; regex mode covers advanced patterns.
    literal: !regexp,
    regexp,
    wholeWord,
  }), [search, replace, caseSensitive, regexp, wholeWord])

  const queryValid = search.length === 0 || buildQuery().valid
  const noMatch = search.length > 0 && queryValid && matchInfo.count === 0

  const refreshMatchInfo = useCallback(() => {
    if (!view) return
    setMatchInfo(computeMatchInfo(view))
  }, [view])

  // Latest callback for the view-attached listener (assigned during render so
  // the closure captured at attach time always reaches the current panel).
  notifyRef.current = open ? refreshMatchInfo : () => {}

  // Attach one doc/selection listener per view so the match counter tracks
  // external edits and findNext/findPrevious selections. appendConfig cannot
  // be undone, so never attach twice for the same view instance.
  useEffect(() => {
    if (!view || panelViews.has(view)) return
    panelViews.add(view)
    view.dispatch({
      effects: StateEffect.appendConfig.of(
        EditorView.updateListener.of(update => {
          if (update.docChanged || update.selectionSet) notifyRef.current()
        }),
      ),
    })
  }, [view])

  // On open: activate CM's search state so cm-searchMatch highlighting is on
  // (openSearchPanel here shows the suppressed empty panel, never the default
  // UI), then seed the find field from a single-line selection (IDEA behavior).
  // On close: clear the query so match highlighting disappears with the panel.
  const openRef = useRef(false)
  useEffect(() => {
    if (!open) {
      openRef.current = false
      view?.dispatch({ effects: setSearchQuery.of(EMPTY_QUERY) })
      return
    }
    const firstOpen = !openRef.current
    openRef.current = true
    if (!view) return
    // searchPanelOpen can be false after an editor-scoped Escape ran the stock
    // closeSearchPanel — re-open (the suppressed panel stays invisible).
    if (firstOpen && !searchPanelOpen(view.state)) openSearchPanel(view)
    if (view && firstOpen) {
      const sel = view.state.selection.main
      if (!sel.empty) {
        const text = view.state.sliceDoc(sel.from, sel.to)
        if (text && !text.includes('\n') && text.length <= SEED_MAX_LEN) setSearch(text)
      }
    }
  }, [open, view])

  // Focus (and select) the find input whenever the panel opens or the open
  // shortcut is pressed again.
  useEffect(() => {
    if (!open) return
    const input = findInputRef.current
    if (!input) return
    input.focus()
    input.select()
  }, [open, focusKey])

  // Debounced sync: push the current query into CM's search state (this also
  // drives the cm-searchMatch highlighting) and refresh the counter.
  useEffect(() => {
    if (!open || !view) return
    const timer = setTimeout(() => {
      view.dispatch({ effects: setSearchQuery.of(buildQuery()) })
      refreshMatchInfo()
    }, 120)
    return () => clearTimeout(timer)
  }, [open, view, search, replace, caseSensitive, wholeWord, regexp, buildQuery, refreshMatchInfo])

  /** Dispatch the latest query (flushing any pending debounce), run a command, refresh. */
  const runCommand = useCallback((command: (v: EditorView) => boolean) => {
    if (!view) return
    const query = buildQuery()
    if (!query.valid) return
    view.dispatch({ effects: setSearchQuery.of(query) })
    command(view)
    refreshMatchInfo()
  }, [view, buildQuery, refreshMatchInfo])

  const handlePanelKeyDown = (e: React.KeyboardEvent<HTMLDivElement>): void => {
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
      return
    }
    const mod = e.ctrlKey || e.metaKey
    if (!mod) return
    if (e.key === 'f' || e.key === 'F') {
      // Keep the browser's native find bar from opening while the panel is up.
      e.preventDefault()
      const input = findInputRef.current
      input?.focus()
      input?.select()
    } else if (e.key === 'r' || e.key === 'R') {
      e.preventDefault()
      onModeChange('replace')
      requestAnimationFrame(() => replaceInputRef.current?.focus())
    }
  }

  const handleFindKeyDown = (e: React.KeyboardEvent<HTMLInputElement>): void => {
    if (e.key === 'Enter') {
      e.preventDefault()
      runCommand(e.shiftKey ? findPrevious : findNext)
    }
  }

  const handleReplaceKeyDown = (e: React.KeyboardEvent<HTMLInputElement>): void => {
    if (e.key === 'Enter') {
      e.preventDefault()
      if (editable) runCommand(replaceNext)
    }
  }

  if (!open) return null

  const canReplace = editable && queryValid && matchInfo.count > 0

  return (
    <div className="frp-panel" role="search" onKeyDown={handlePanelKeyDown}>
      <div className="frp-row">
        <div className={`frp-field${!queryValid || noMatch ? ' frp-field--alert' : ''}`}>
          <input
            ref={findInputRef}
            className="frp-input"
            type="text"
            value={search}
            placeholder={t('findReplace.find')}
            spellCheck={false}
            aria-label={t('findReplace.find')}
            onChange={(e) => setSearch(e.target.value)}
            onKeyDown={handleFindKeyDown}
          />
          <div className="frp-toggles" role="group" aria-label={t('findReplace.searchOptions')}>
            <button
              type="button"
              className={`frp-toggle${caseSensitive ? ' frp-toggle--on' : ''}`}
              aria-pressed={caseSensitive}
              title={t('findReplace.matchCase')}
              onClick={() => setCaseSensitive(v => !v)}
            >Aa</button>
            <button
              type="button"
              className={`frp-toggle${wholeWord ? ' frp-toggle--on' : ''}`}
              aria-pressed={wholeWord}
              title={t('findReplace.wholeWords')}
              onClick={() => setWholeWord(v => !v)}
            >ab</button>
            <button
              type="button"
              className={`frp-toggle${regexp ? ' frp-toggle--on' : ''}`}
              aria-pressed={regexp}
              title={t('findReplace.regex')}
              onClick={() => setRegexp(v => !v)}
            >.*</button>
          </div>
        </div>
        <span className={`frp-count${noMatch ? ' frp-count--none' : ''}`} aria-live="polite">
          {!queryValid ? '—' : matchInfo.count > 0
            ? `${matchInfo.activeIndex + 1}/${matchInfo.count}${matchInfo.capped ? '+' : ''}`
            : search.length > 0 ? t('findReplace.noResults') : ''}
        </span>
        <button
          type="button"
          className="frp-icon-btn"
          title={t('findReplace.previousMatch')}
          aria-label={t('findReplace.previousMatch')}
          onClick={() => runCommand(findPrevious)}
        ><ChevronUp size={14} /></button>
        <button
          type="button"
          className="frp-icon-btn"
          title={t('findReplace.nextMatch')}
          aria-label={t('findReplace.nextMatch')}
          onClick={() => runCommand(findNext)}
        ><ChevronDown size={14} /></button>
        <button
          type="button"
          className="frp-icon-btn"
          title={t('findReplace.closePanel')}
          aria-label={t('findReplace.closeFindPanel')}
          onClick={onClose}
        ><X size={14} /></button>
      </div>
      {mode === 'replace' && (
        <div className="frp-row">
          <input
            ref={replaceInputRef}
            className="frp-input frp-input--replace"
            type="text"
            value={replace}
            placeholder={t('findReplace.replaceWith')}
            spellCheck={false}
            aria-label={t('findReplace.replaceWith')}
            onChange={(e) => setReplace(e.target.value)}
            onKeyDown={handleReplaceKeyDown}
          />
          <div className="frp-actions">
            <button
              type="button"
              className="frp-btn"
              disabled={!canReplace}
              onClick={() => runCommand(replaceNext)}
            >{t('findReplace.replace')}</button>
            <button
              type="button"
              className="frp-btn"
              disabled={!canReplace}
              onClick={() => runCommand(replaceAll)}
            >{t('findReplace.replaceAllOccurrences')}</button>
          </div>
        </div>
      )}
    </div>
  )
}
