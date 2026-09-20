import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Search, SquarePen, X, ArrowRight, Check, AlertCircle, Loader2, FileCode } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { client } from '../../../application/generated-client'
import * as projectClient from '../../../gen-clients/project/client'
import type { FileSystemGrepMatch } from '../../../gen-clients/system/types'
import { useBrowserOverlay } from '../browserOverlay'
import './GlobalFindReplaceOverlay.css'
import './parts/parts.css'

export interface FindMatchDetail {
  file: string
  line: number
  content: string
  start: number
  end: number
}

export interface FindFileGroup {
  file: string
  matches: FindMatchDetail[]
}

export interface ReplacePreviewItem {
  file: string
  line: number
  before: string
  after: string
  changed: boolean
  column: number
  end: number
}

export interface GlobalFindReplaceOverlayProps {
  open: boolean
  mode: 'find' | 'replace'
  onModeChange: (mode: 'find' | 'replace') => void
  onClose: () => void
  projectId: string | null
  onOpenFile: (filePath: string, line?: number, lineEnd?: number, column?: number, columnEnd?: number) => void
}

/** Escape a literal string so it matches itself when used as a RegExp. */
export function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

export interface SearchPatternResult {
  jsSource: string
  jsFlags: string
  goPattern: string
  wholeWord: boolean
}

export function buildSearchPattern(
  query: string,
  { regex, ignoreCase, wholeWord }: { regex: boolean; ignoreCase: boolean; wholeWord: boolean },
): SearchPatternResult | null {
  if (!query) return null
  let source = regex ? query : escapeRegExp(query)
  if (wholeWord) {
    source = `(?:\\b${source}\\b)`
  }
  const jsFlags = (ignoreCase ? 'i' : '') + 'g'
  try {
    new RegExp(source, jsFlags.replace('g', ''))
  } catch {
    return null
  }
  const goPattern = (ignoreCase ? '(?i)' : '') + source
  return { jsSource: source, jsFlags, goPattern, wholeWord }
}

/** Compute the matched spans inside a single line using a JS regex. */
export function findLineSpans(line: string, re: RegExp): Array<{ start: number; end: number }> {
  const spans: Array<{ start: number; end: number }> = []
  const flags = re.flags.includes('g') ? re.flags : re.flags + 'g'
  const g = new RegExp(re.source, flags)
  g.lastIndex = 0
  let m: RegExpExecArray | null
  let safety = 0
  while ((m = g.exec(line)) !== null) {
    if (m[0].length === 0) {
      g.lastIndex++
      if (g.lastIndex > line.length) break
      if (++safety > line.length) break
      continue
    }
    spans.push({ start: m.index, end: m.index + m[0].length })
    safety = 0
  }
  return spans
}

export function applyRegexToLine(line: string, re: RegExp, replacement: string): string {
  const fresh = new RegExp(re.source, re.flags)
  return line.replace(fresh, replacement)
}

export interface ApplyResult {
  output: string
  replacements: number
  skippedLines: number
}

export function applyReplacementToContent(
  content: string,
  lineNumbers: number[],
  re: RegExp,
  replacement: string,
): ApplyResult {
  const hadCRLF = content.includes('\r\n')
  const normalized = hadCRLF ? content.replace(/\r\n/g, '\n') : content
  const lines = normalized.split('\n')
  let replacements = 0
  let skippedLines = 0
  for (const ln of lineNumbers) {
    const idx = ln - 1
    const line = lines[idx]
    if (idx < 0 || line === undefined) {
      skippedLines++
      continue
    }
    const after = applyRegexToLine(line, re, replacement)
    if (after !== line) {
      lines[idx] = after
      replacements++
    } else {
      skippedLines++
    }
  }
  let output = lines.join('\n')
  if (hadCRLF) output = output.replace(/\n/g, '\r\n')
  return { output, replacements, skippedLines }
}

function decodeExactText(content: string): { text: string; hadCRLF: boolean } | null {
  try {
    const bin = atob(content)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    const decoder = new TextDecoder('utf-8', { fatal: true })
    const text = decoder.decode(bytes)
    return { text, hadCRLF: text.includes('\r\n') }
  } catch {
    return null
  }
}

const ROW_LIMIT = 15
const SEARCH_LIMIT = 500

type ApplyStatus = { status: 'idle' } | { status: 'applying'; done: number; total: number } | { status: 'done'; summary: string; errors: string[] }

export function GlobalFindReplaceOverlay({
  open,
  mode,
  onModeChange,
  onClose,
  projectId,
  onOpenFile,
}: GlobalFindReplaceOverlayProps) {
  useBrowserOverlay(open)
  const { t } = useI18n()

  const [query, setQuery] = useState('')
  const [replacement, setReplacement] = useState('')
  const [regex, setRegex] = useState(false)
  const [ignoreCase, setIgnoreCase] = useState(false)
  const [wholeWord, setWholeWord] = useState(false)

  const [results, setResults] = useState<FindFileGroup[] | null>(null)
  const [previews, setPreviews] = useState<ReplacePreviewItem[] | null>(null)
  const [searching, setSearching] = useState(false)
  const [searchError, setSearchError] = useState<string | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [numMatches, setNumMatches] = useState(0)
  const [expandedFiles, setExpandedFiles] = useState<Record<string, boolean>>({})
  const [selectedFiles, setSelectedFiles] = useState<Record<string, boolean>>({})
  const [applyState, setApplyState] = useState<ApplyStatus>({ status: 'idle' })

  const containerRef = useRef<HTMLDivElement>(null)
  const queryInputRef = useRef<HTMLInputElement>(null)
  const replaceInputRef = useRef<HTMLInputElement>(null)
  const [pos, setPos] = useState<{ top: number; right: number } | null>(null)

  useEffect(() => {
    if (!open) {
      setPos(null)
      return
    }
    const apply = () => {
      const topbar = document.querySelector('.ai-shell-topbar') as HTMLElement | null
      const top = topbar ? topbar.getBoundingClientRect().bottom + 6 : 80
      const content = document.querySelector('.ai-shell-content') as HTMLElement | null
      const cRect = content?.getBoundingClientRect()
      const right = cRect ? cRect.right - 16 : window.innerWidth - 16
      setPos({ top, right })
    }
    apply()
    window.addEventListener('resize', apply)
    return () => window.removeEventListener('resize', apply)
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  useEffect(() => {
    if (open) {
      setSearchError(null)
      setApplyState({ status: 'idle' })
      queryInputRef.current?.focus()
    }
  }, [open, mode])

  const currentPattern = useMemo(
    () => buildSearchPattern(query, { regex, ignoreCase, wholeWord }),
    [query, regex, ignoreCase, wholeWord],
  )

  const runSearch = useCallback(async () => {
    if (!projectId || !currentPattern) return
    setSearching(true)
    setSearchError(null)
    setResults(null)
    setPreviews(null)
    setApplyState({ status: 'idle' })
    try {
      const resp = await projectClient.grep(
        client,
        {
          Pattern: currentPattern.goPattern,
          Path: '',
          Output_mode: 'content',
          Head_limit: SEARCH_LIMIT,
          Ignore_case: ignoreCase,
          Word_regexp: wholeWord,
        },
        { target: projectId },
      )
      setTruncated(resp.Truncated ?? false)
      setNumMatches(resp.NumMatches ?? 0)

      const jsRe = new RegExp(currentPattern.jsSource, currentPattern.jsFlags)
      const grouped: Record<string, FindFileGroup> = {}
      const previewItems: ReplacePreviewItem[] = []

      for (const m of (resp.Matches as FileSystemGrepMatch[]) ?? []) {
        const spans = findLineSpans(m.Content, jsRe)
        const first = spans[0]
        if (!first) continue
        let group = grouped[m.File]
        if (!group) {
          group = { file: m.File, matches: [] }
          grouped[m.File] = group
        }
        group.matches.push({
          file: m.File,
          line: m.Line,
          content: m.Content,
          start: first.start,
          end: first.end,
        })
        if (mode === 'replace') {
          const after = applyRegexToLine(m.Content, jsRe, replacement)
          previewItems.push({
            file: m.File,
            line: m.Line,
            before: m.Content,
            after,
            changed: after !== m.Content,
            column: first.start,
            end: first.end,
          })
        }
      }

      const groups = Object.values(grouped).sort((a, b) => a.file.localeCompare(b.file))
      setResults(groups)
      if (mode === 'replace') setPreviews(previewItems)
      const allExpanded: Record<string, boolean> = {}
      for (const g of groups) allExpanded[g.file] = true
      setExpandedFiles(allExpanded)
      const allSelected: Record<string, boolean> = {}
      for (const g of groups) allSelected[g.file] = true
      setSelectedFiles(allSelected)
    } catch (e) {
      setSearchError(e instanceof Error ? e.message : String(e))
      setResults(null)
      setPreviews(null)
    } finally {
      setSearching(false)
    }
  }, [projectId, currentPattern, ignoreCase, wholeWord, mode, replacement])

  const applyAll = useCallback(async () => {
    if (!projectId || !currentPattern || !results) return
    const jsRe = new RegExp(currentPattern.jsSource, currentPattern.jsFlags)
    const files = results.filter((g) => selectedFiles[g.file] !== false)
    if (files.length === 0) return
    setApplyState({ status: 'applying', done: 0, total: files.length })
    const errors: string[] = []
    let totalReplacements = 0
    let changedFiles = 0

    for (let i = 0; i < files.length; i++) {
      const group = files[i]
      if (!group) continue
      setApplyState({ status: 'applying', done: i, total: files.length })
      try {
        const b64 = await projectClient.readBase64(client, { Path: group.file }, { target: projectId })
        const decoded = decodeExactText(b64.Content ?? '')
        if (!decoded) {
          errors.push(`${group.file}: ${t('findReplace.error.decode')}`)
          continue
        }
        const { output, replacements } = applyReplacementToContent(
          decoded.text,
          group.matches.map((m) => m.line),
          jsRe,
          replacement,
        )
        if (replacements > 0) {
          await projectClient.write(client, { Path: group.file, Content: output }, { target: projectId })
          totalReplacements += replacements
          changedFiles++
        }
      } catch (e) {
        errors.push(`${group.file}: ${e instanceof Error ? e.message : String(e)}`)
      }
    }
    const summary = t('findReplace.applySummary', { files: changedFiles, replacements: totalReplacements })
    setApplyState({ status: 'done', summary, errors })
  }, [projectId, currentPattern, results, selectedFiles, replacement, t])

  const handleQueryKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      void runSearch()
    }
  }
  const handleReplaceKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      void runSearch()
    }
  }

  const toggleExpanded = (file: string) => {
    setExpandedFiles((prev) => ({ ...prev, [file]: !prev[file] }))
  }
  const toggleSelectedFile = (file: string) => {
    setSelectedFiles((prev) => ({ ...prev, [file]: !prev[file] }))
  }

  const totalSelectedReplacements = useMemo(() => {
    if (!results) return 0
    return results.reduce((sum, g) => sum + (selectedFiles[g.file] !== false ? g.matches.length : 0), 0)
  }, [results, selectedFiles])

  if (!open) return null
  if (!pos) return null

  const headerIcon = mode === 'find' ? <Search size={16} /> : <SquarePen size={16} />

  return (
    <div
      ref={containerRef}
      className="global-find-replace-overlay"
      style={{ top: pos.top, right: window.innerWidth - pos.right }}
      onClick={(e) => e.stopPropagation()}
    >
      <div className="gfr-header">
        <div className="gfr-title">
          {headerIcon}
          <span>{mode === 'find' ? t('findReplace.titleFind') : t('findReplace.titleReplace')}</span>
        </div>
        <div className="gfr-mode-switch">
          <button
            type="button"
            className={mode === 'find' ? 'active' : ''}
            onClick={() => onModeChange('find')}
            title={t('findReplace.modeFind')}
          >
            {t('findReplace.modeFind')}
          </button>
          <button
            type="button"
            className={mode === 'replace' ? 'active' : ''}
            onClick={() => onModeChange('replace')}
            title={t('findReplace.modeReplace')}
          >
            {t('findReplace.modeReplace')}
          </button>
        </div>
        <button type="button" className="gfr-close" onClick={onClose} aria-label={t('common.close')}>
          <X size={16} />
        </button>
      </div>

      <div className="gfr-input-row">
        <Search size={16} className="gfr-input-icon" />
        <input
          ref={queryInputRef}
          className="gfr-input"
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={handleQueryKeyDown}
          placeholder={t('findReplace.findPlaceholder')}
        />
      </div>

      {mode === 'replace' && (
        <div className="gfr-input-row">
          <ArrowRight size={16} className="gfr-input-icon" />
          <input
            ref={replaceInputRef}
            className="gfr-input"
            type="text"
            value={replacement}
            onChange={(e) => setReplacement(e.target.value)}
            onKeyDown={handleReplaceKeyDown}
            placeholder={t('findReplace.replacePlaceholder')}
          />
        </div>
      )}

      <div className="gfr-toolbar">
        <button
          type="button"
          className={`gfr-toggle ${ignoreCase ? 'active' : ''}`}
          onClick={() => setIgnoreCase((v) => !v)}
          title={t('findReplace.caseInsensitive')}
        >
          <span>Aa</span>
        </button>
        <button
          type="button"
          className={`gfr-toggle ${regex ? 'active' : ''}`}
          onClick={() => setRegex((v) => !v)}
          title={t('findReplace.regexMode')}
        >
          <span>.*</span>
        </button>
        <button
          type="button"
          className={`gfr-toggle ${wholeWord ? 'active' : ''}`}
          onClick={() => setWholeWord((v) => !v)}
          title={t('findReplace.wholeWord')}
        >
          <span>ab</span>
        </button>
        <div className="gfr-toolbar-spacer" />
        <button
          type="button"
          className="gfr-search-btn"
          disabled={!currentPattern || !projectId || searching}
          onClick={() => void runSearch()}
        >
          {searching ? <Loader2 size={14} className="gfr-spin" /> : <Search size={14} />}
          {t('findReplace.search')}
        </button>
      </div>

      {searchError && (
        <div className="gfr-status gfr-status-error">
          <AlertCircle size={14} />
          {searchError}
        </div>
      )}

      {results && !searchError && (
        <div className="gfr-status">
          <span>{t('findReplace.resultSummary', { files: results.length, matches: numMatches })}</span>
          {truncated && <span className="gfr-truncated">{t('findReplace.truncated')}</span>}
        </div>
      )}

      <div className="gfr-results">
        {results && results.length === 0 && !searching && (
          <div className="gfr-empty">{t('findReplace.noResults')}</div>
        )}
        {results?.map((group) => {
          const expanded = expandedFiles[group.file] !== false
          const selected = selectedFiles[group.file] !== false
          return (
            <div key={group.file} className={`gfr-file ${selected ? '' : 'gfr-file-disabled'}`}>
              <div className="gfr-file-header">
                {mode === 'replace' && (
                  <input
                    type="checkbox"
                    checked={selected}
                    onChange={() => toggleSelectedFile(group.file)}
                    onClick={(e) => e.stopPropagation()}
                  />
                )}
                <span className="gfr-file-icon">
                  <FileCode size={14} />
                </span>
                <button type="button" className="gfr-file-path" onClick={() => toggleExpanded(group.file)}>
                  <span className="gfr-file-name">{group.file}</span>
                  <span className="gfr-file-count">{group.matches.length}</span>
                </button>
                <button type="button" className="gfr-file-toggle" onClick={() => toggleExpanded(group.file)}>
                  {expanded ? '−' : '+'}
                </button>
              </div>
              {expanded && (
                <div className="ai-tool-match-list">
                  {group.matches.slice(0, ROW_LIMIT).map((m) => (
                    <button
                      key={`${m.file}:${m.line}:${m.start}`}
                      type="button"
                      className="ai-tool-match-item gfr-match-row"
                      onClick={() => onOpenFile(m.file, m.line, m.line, m.start, m.end)}
                    >
                      <span className="ai-tool-match-loc">
                        {m.file}:{m.line}:{m.start + 1}
                      </span>
                      {': '}
                      <span className="gfr-match-content">
                        <HighlightSpans text={m.content} spans={findLineSpans(m.content, new RegExp(currentPattern?.jsSource ?? '', currentPattern?.jsFlags ?? 'g'))} />
                      </span>
                    </button>
                  ))}
                  {group.matches.length > ROW_LIMIT && (
                    <button
                      type="button"
                      className="ai-tool-code-expander"
                      onClick={() => toggleExpanded(group.file)}
                    >
                      {t('findReplace.collapse')}
                    </button>
                  )}
                </div>
              )}
              {expanded && mode === 'replace' && previews && (
                <div className="gfr-previews">
                  {previews
                    .filter((p) => p.file === group.file)
                    .slice(0, ROW_LIMIT)
                    .map((p) => (
                      <div key={`${p.file}:${p.line}`} className="gfr-preview-row">
                        <div className="gfr-preview-line">
                          <span className="gfr-preview-label">{t('findReplace.before')}</span>
                          <span className="gfr-preview-del">{p.before}</span>
                        </div>
                        <div className="gfr-preview-line">
                          <span className="gfr-preview-label">{t('findReplace.after')}</span>
                          <span className="gfr-preview-ins">{p.after}</span>
                        </div>
                      </div>
                    ))}
                </div>
              )}
            </div>
          )
        })}
      </div>

      {mode === 'replace' && results && results.length > 0 && applyState.status !== 'applying' && applyState.status !== 'done' && (
        <div className="gfr-footer">
          <button
            type="button"
            className="gfr-replace-btn"
            disabled={totalSelectedReplacements === 0}
            onClick={() => void applyAll()}
          >
            {t('findReplace.replaceAll', { count: totalSelectedReplacements })}
          </button>
        </div>
      )}

      {applyState.status === 'applying' && (
        <div className="gfr-footer gfr-footer-info">
          <Loader2 size={14} className="gfr-spin" />
          {t('findReplace.applying', { done: applyState.done, total: applyState.total })}
        </div>
      )}

      {applyState.status === 'done' && (
        <div className="gfr-footer gfr-footer-info">
          <Check size={14} />
          {applyState.summary}
          {applyState.errors.length > 0 && (
            <div className="gfr-apply-errors">
              {applyState.errors.map((err, i) => (
                <div key={i}>{err}</div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function HighlightSpans({ text, spans }: { text: string; spans: Array<{ start: number; end: number }> }) {
  if (spans.length === 0) return <>{text}</>
  const parts: React.ReactNode[] = []
  let last = 0
  for (let i = 0; i < spans.length; i++) {
    const span = spans[i]
    if (!span) continue
    const { start, end } = span
    if (start > last) parts.push(<span key={`pre-${i}`}>{text.slice(last, start)}</span>)
    parts.push(
      <mark key={`mark-${i}`} className="gfr-match-mark">
        {text.slice(start, end)}
      </mark>,
    )
    last = end
  }
  if (last < text.length) parts.push(<span key="tail">{text.slice(last)}</span>)
  return <>{parts}</>
}
