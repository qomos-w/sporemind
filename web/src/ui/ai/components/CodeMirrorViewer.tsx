import React, { useEffect, useMemo, useRef, useState } from 'react'
import { EditorView, keymap, lineNumbers, highlightActiveLineGutter, highlightSpecialChars, drawSelection, dropCursor, rectangularSelection, crosshairCursor, highlightActiveLine, scrollPastEnd } from '@codemirror/view'
import { EditorState, EditorSelection, Prec, Transaction } from '@codemirror/state'
import { setDiagnostics, lintGutter } from '@codemirror/lint'
import { history } from '@codemirror/commands'
import { indentOnInput, syntaxHighlighting, bracketMatching, foldGutter } from '@codemirror/language'
import { javascript } from '@codemirror/lang-javascript'
import { python } from '@codemirror/lang-python'
import { json } from '@codemirror/lang-json'
import { go } from '@codemirror/lang-go'
import { rust } from '@codemirror/lang-rust'
import { java } from '@codemirror/lang-java'
import { cpp } from '@codemirror/lang-cpp'
import { markdown } from '@codemirror/lang-markdown'
import { html } from '@codemirror/lang-html'
import { css } from '@codemirror/lang-css'
import { sql } from '@codemirror/lang-sql'
import { xml } from '@codemirror/lang-xml'
import { spore } from '../../editor/sporeLanguage'
import { HighlightStyle } from '@codemirror/language'
import { tags } from '@lezer/highlight'
import { oneDarkTheme } from '@codemirror/theme-one-dark'
import { client as gatewayClient } from '../../../application/generated-client'
import { LspClient, lspGotoDefinition, pathToUri, uriToPath, ReferencesPopup, type Location } from '../../editor/lsp'
import { useEditorKeymap } from '../../editor/EditorKeymapContext'
import { editingExtensions, type FindReplaceHandlers } from '../../editor/editing-extensions'
import { FindReplacePanel, useFindReplacePanel } from '../../editor/FindReplacePanel'
import * as fsClientMod from '../../../gen-clients/filesystem/client'
import './CodeMirrorViewer.css'

const SOURCE_LINE_VIEWPORT_RATIO = 1 / 3

const projectHighlight = HighlightStyle.define([
  { tag: tags.keyword, color: 'var(--syntax-keyword)' },
  { tag: [tags.name, tags.deleted, tags.character, tags.propertyName, tags.macroName], color: 'var(--syntax-name)' },
  { tag: [tags.function(tags.variableName), tags.labelName], color: 'var(--accent-primary)' },
  { tag: [tags.color, tags.constant(tags.name), tags.standard(tags.name)], color: 'var(--syntax-constant)' },
  { tag: [tags.definition(tags.name), tags.separator], color: 'var(--syntax-variable)' },
  { tag: [tags.typeName, tags.className, tags.number, tags.changed, tags.annotation, tags.modifier, tags.self, tags.namespace], color: 'var(--syntax-type)' },
  { tag: [tags.operator, tags.operatorKeyword, tags.url, tags.escape, tags.regexp, tags.link, tags.special(tags.string)], color: 'var(--syntax-operator)' },
  { tag: [tags.meta, tags.comment], color: 'var(--text-tertiary)' },
  { tag: tags.strong, fontWeight: 'bold' },
  { tag: tags.emphasis, fontStyle: 'italic' },
  { tag: tags.link, color: 'var(--accent-primary)', textDecoration: 'underline' },
  { tag: tags.heading, fontWeight: 'bold', color: 'var(--accent-primary)' },
  { tag: [tags.atom, tags.bool, tags.special(tags.variableName)], color: 'var(--syntax-constant)' },
  { tag: [tags.processingInstruction, tags.string, tags.inserted], color: 'var(--status-success)' },
  { tag: tags.invalid, color: 'var(--syntax-name)' },
  { tag: tags.variableName, color: 'var(--syntax-variable)' },
  { tag: tags.attributeName, color: 'var(--syntax-name)' },
  { tag: tags.tagName, color: 'var(--syntax-name)' },
  { tag: tags.attributeValue, color: 'var(--syntax-constant)' },
  { tag: tags.documentMeta, color: 'var(--text-tertiary)' },
  { tag: tags.angleBracket, color: 'var(--text-tertiary)' },
  { tag: tags.content, color: 'var(--syntax-variable)' },
  { tag: tags.punctuation, color: 'var(--text-secondary)' },
  { tag: tags.literal, color: 'var(--syntax-constant)' },
  { tag: tags.unit, color: 'var(--syntax-type)' },
  { tag: tags.null, color: 'var(--syntax-keyword)' },
  { tag: tags.controlKeyword, color: 'var(--syntax-keyword)' },
  { tag: tags.definitionKeyword, color: 'var(--syntax-keyword)' },
  { tag: tags.moduleKeyword, color: 'var(--syntax-keyword)' },
  { tag: tags.strikethrough, textDecoration: 'line-through' },
])

interface CodeMirrorViewerProps {
  content: string
  filePath: string
  initialLine?: number
  initialLineEnd?: number
  /** 0-based UTF-16 column (offset within initialLine) to start the selection at. */
  initialColumn?: number
  /** Exclusive end column within initialLine; defaults to end of line. */
  initialColumnEnd?: number
  editable?: boolean
  onChange?: (value: string) => void
  onSave?: () => void
  onCursorChange?: (pos: number, selFrom: number, selTo: number) => void
  restoreCursorPos?: number | null
  restoreSelectionFrom?: number | null
  restoreSelectionTo?: number | null
  cursorRestoreKey?: number
  projectRootPath?: string
  onGotoDefinition?: (filePath: string, line: number) => void
  onShowReferences?: (locations: Location[], anchor: { x: number; y: number }) => void
  /** Absolute root paths of the other open workspace projects, for workspace-scope references. */
  workspaceProjectRoots?: string[]
}

function isGoFile(path: string): boolean {
  return path.toLowerCase().endsWith('.go')
}

const LSP_TYPESCRIPT_EXTS = ['.ts', '.tsx', '.js', '.jsx', '.mts', '.cts']

function isTypeScriptFile(path: string): boolean {
  const lower = path.toLowerCase()
  return LSP_TYPESCRIPT_EXTS.some((ext) => lower.endsWith(ext))
}

function isLspFile(path: string): boolean {
  return isGoFile(path) || isTypeScriptFile(path)
}

function isAbsolutePath(path: string): boolean {
  return /^([a-zA-Z]:[\\/]|\/|\\\\)/.test(path)
}

/**
 * gopls addresses files by absolute URI; the file browser hands us
 * project-relative paths like `pkg/actor/x.go`, which pathToUri would
 * mangle into `file:///pkg/...`. Resolve against the project root first.
 */
function resolveAgainstRoot(filePath: string, projectRootPath?: string): string {
  if (!projectRootPath || isAbsolutePath(filePath)) return filePath
  const root = projectRootPath.replace(/[\\/]+$/, '')
  return `${root}/${filePath.replace(/^[\\/]+/, '')}`
}

function langFromPath(path: string) {
  const ext = path.split('.').pop()?.toLowerCase()
  switch (ext) {
    case 'js':
    case 'jsx':
    case 'mjs':
    case 'cjs':
      return javascript({ jsx: ext === 'jsx' })
    case 'ts':
    case 'tsx':
    case 'mts':
    case 'cts':
      return javascript({ typescript: true, jsx: ext === 'tsx' })
    case 'py':
    case 'pyw':
      return python()
    case 'json':
      return json()
    case 'go':
      return go()
    case 'rs':
      return rust()
    case 'java':
      return java()
    case 'cpp':
    case 'cc':
    case 'cxx':
    case 'c':
    case 'h':
    case 'hpp':
      return cpp()
    case 'md':
    case 'markdown':
      return markdown()
    case 'html':
    case 'htm':
      return html()
    case 'css':
    case 'scss':
    case 'sass':
      return css()
    case 'sql':
      return sql()
    case 'xml':
    case 'svg':
      return xml()
    case 'spore':
      return spore()
    default:
      return null
  }
}

function isDarkTheme(): boolean {
  return document.documentElement.getAttribute('data-theme') === 'dark'
}

export const CodeMirrorViewer: React.FC<CodeMirrorViewerProps> = ({
  content,
  filePath,
  initialLine,
  initialLineEnd,
  initialColumn,
  initialColumnEnd,
  editable = false,
  onChange,
  onSave,
  onCursorChange,
  restoreCursorPos,
  restoreSelectionFrom,
  restoreSelectionTo,
  cursorRestoreKey,
  projectRootPath,
  onGotoDefinition,
  onShowReferences,
  workspaceProjectRoots,
}) => {
  const containerRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const [view, setView] = useState<EditorView | null>(null)
  const onChangeRef = useRef(onChange)
  const onSaveRef = useRef(onSave)
  const onCursorChangeRef = useRef(onCursorChange)
  const onGotoDefinitionRef = useRef(onGotoDefinition)
  const onShowReferencesRef = useRef(onShowReferences)
  const workspaceRootsRef = useRef(workspaceProjectRoots)
  const lastRestoreKeyRef = useRef<number>(0)
  const [refsPopup, setRefsPopup] = useState<{
    locations: Location[]
    anchor: { x: number; y: number }
    queryPos: { uri: string; line: number; character: number }
  } | null>(null)
  onChangeRef.current = onChange
  onSaveRef.current = onSave
  onCursorChangeRef.current = onCursorChange
  onGotoDefinitionRef.current = onGotoDefinition
  onShowReferencesRef.current = onShowReferences
  workspaceRootsRef.current = workspaceProjectRoots

  const { keymapExtension } = useEditorKeymap()

  const {
    open: findReplaceOpen,
    mode: findReplaceMode,
    focusKey: findReplaceFocusKey,
    openPanel: openFindReplace,
    closePanel: closeFindReplace,
    closePanelOnEscape: closeFindReplaceOnEscape,
    setMode: setFindReplaceMode,
  } = useFindReplacePanel()

  const findReplaceHandlers = useMemo<FindReplaceHandlers>(
    () => ({ onOpen: openFindReplace, onClose: closeFindReplaceOnEscape }),
    [openFindReplace, closeFindReplaceOnEscape],
  )

  useEffect(() => {
    if (!containerRef.current) return

    const lang = langFromPath(filePath)
    const dark = isDarkTheme()

    const extensions = [
      history(),
      drawSelection(),
      dropCursor(),
      indentOnInput(),
      bracketMatching(),
      rectangularSelection(),
      crosshairCursor(),
      highlightActiveLineGutter(),
      highlightSpecialChars(),
      lineNumbers(),
      foldGutter(),
      highlightActiveLine(),
      scrollPastEnd(),
    ]

    if (editable) {
      extensions.push(
        EditorView.updateListener.of(update => {
          if (!update.docChanged) return
          if (update.transactions.every(tr => tr.isUserEvent('external'))) return
          onChangeRef.current?.(update.state.doc.toString())
        }),
        Prec.highest(keymap.of([{
          key: 'Mod-s',
          preventDefault: true,
          run: () => {
            onSaveRef.current?.()
            return true
          },
        }])),
      )
    } else {
      extensions.push(
        EditorView.editable.of(false),
        EditorState.readOnly.of(true),
      )
    }

    extensions.push(
      EditorView.updateListener.of(update => {
        if (!update.selectionSet) return
        const { main } = update.state.selection
        onCursorChangeRef.current?.(main.head, main.from, main.to)
      }),
    )

    extensions.push(
      keymapExtension,
      ...editingExtensions({ editable, findReplace: findReplaceHandlers }),
      EditorView.theme({
        '&': { backgroundColor: 'transparent' },
        '.cm-scroller': { fontFamily: 'var(--font-mono, monospace)', fontSize: '0.8125rem', lineHeight: '1.6' },
        '.cm-gutters': { backgroundColor: 'transparent', borderRight: 'none', color: 'var(--text-tertiary)' },
        // Semi-transparent: the selection layer draws BELOW line backgrounds
        // (z-index -1). An opaque --bg-hover here would fully cover drag
        // selections on the cursor line (stock CM uses ~26% alpha).
        '.cm-activeLineGutter': { backgroundColor: 'color-mix(in srgb, var(--bg-hover) 35%, transparent)' },
        '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--bg-hover) 35%, transparent)' },
      }),
    )

    if (lang) {
      extensions.push(lang)
    }

    if (dark) {
      extensions.push(oneDarkTheme)
    }
    extensions.push(syntaxHighlighting(projectHighlight))

    const lspClient = isLspFile(filePath) && projectRootPath
      ? new LspClient({ client: gatewayClient, rootUri: pathToUri(projectRootPath) })
      : null
    const lspFilePath = resolveAgainstRoot(filePath, projectRootPath)

    if (lspClient) {
      extensions.push(lintGutter())
    }

    if (lspClient && onGotoDefinitionRef.current) {
      extensions.push(lspGotoDefinition({
        client: lspClient,
        filePath: lspFilePath,
        onGotoDefinition: onGotoDefinitionRef.current,
        onShowReferences: (locations, anchor, queryPos) => setRefsPopup({ locations, anchor, queryPos }),
      }))
    }

    const state = EditorState.create({ doc: content, extensions })
    const view = new EditorView({ state, parent: containerRef.current })
    viewRef.current = view
    setView(view)

    let unsubDiagnostics: (() => void) | null = null
    if (lspClient) {
      const docUri = pathToUri(lspFilePath)
      unsubDiagnostics = lspClient.onDiagnostics((uri, diags) => {
        if (uri !== docUri) return
        const current = viewRef.current
        if (!current) return
        const cmDiags = diags.flatMap((d) => {
          const lineCount = current.state.doc.lines
          const startLine = current.state.doc.line(Math.min(d.range.start.line + 1, lineCount))
          const endLine = current.state.doc.line(Math.min(d.range.end.line + 1, lineCount))
          const from = Math.min(startLine.from + d.range.start.character, startLine.to)
          const to = Math.min(endLine.from + d.range.end.character, endLine.to)
          if (from > to) return []
          const severity = (['error', 'warning', 'info', 'info'] as const)[(d.severity ?? 1) - 1] ?? 'info'
          return [{
            from,
            to,
            severity,
            message: d.message,
            source: d.source ?? 'gopls',
          }]
        })
        setDiagnostics(current.state, cmDiags)
      })

      void lspClient.didOpen(lspFilePath, content).catch(() => {
        // LSP is best-effort; a failed didOpen should not crash the viewer.
      })
    }

    return () => {
      unsubDiagnostics?.()
      view.destroy()
      viewRef.current = null
      setView(null)
      lastRestoreKeyRef.current = 0
      if (lspClient) {
        void lspClient.didClose(lspFilePath).finally(() => lspClient.disconnect())
      }
    }
  }, [filePath, editable, projectRootPath, keymapExtension, findReplaceHandlers])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    if (view.state.doc.toString() === content) return
    // External content reloads must not pollute the undo stack: otherwise the
    // user could Ctrl+Z back to stale content and auto-save would clobber the
    // external change.
    view.dispatch({
      changes: { from: 0, to: view.state.doc.length, insert: content },
      annotations: [Transaction.addToHistory.of(false)],
      userEvent: 'external',
    })
  }, [content])

  useEffect(() => {
    const view = viewRef.current
    if (!view || !initialLine || initialLine <= 0) return
    const lineCount = view.state.doc.lines
    if (lineCount === 0) return
    const targetLine = Math.min(initialLine, lineCount)
    const linePos = view.state.doc.line(targetLine)
    const endLine = initialLineEnd && initialLineEnd > initialLine ? Math.min(initialLineEnd, lineCount) : targetLine
    const endPos = view.state.doc.line(endLine)
    // Column support: clamp the selection into the target line when a match
    // column was supplied (global find results jump to file+line+column).
    let selFrom = linePos.from
    let selTo = endPos.to
    if (initialColumn != null && initialColumn >= 0) {
      selFrom = Math.min(linePos.from + initialColumn, linePos.to)
      selTo = Math.min(linePos.from + (initialColumnEnd != null && initialColumnEnd > initialColumn ? initialColumnEnd : linePos.to - linePos.from), linePos.to)
      if (selTo < selFrom) selTo = selFrom
    }
    view.dispatch({
      selection: EditorSelection.range(selFrom, selTo),
    })
    const lineRect = view.coordsAtPos(linePos.from)
    if (!lineRect) return
    const scroller = view.scrollDOM
    const scrollerRect = scroller.getBoundingClientRect()
    const targetTop = scroller.scrollTop + lineRect.top - scrollerRect.top - scroller.clientHeight * SOURCE_LINE_VIEWPORT_RATIO
    scroller.scrollTo({
      top: Math.max(0, targetTop),
      behavior: 'instant',
    })
  }, [initialLine, initialLineEnd, initialColumn, initialColumnEnd, content])

  // Restore cursor position after a navigation back/forward. Fires when
  // cursorRestoreKey changes (on restore) or when content loads. The
  // lastRestoreKeyRef guard ensures the cursor is applied once per restore,
  // not on every subsequent content edit.
  useEffect(() => {
    const view = viewRef.current
    if (!view || restoreCursorPos == null) return
    if (lastRestoreKeyRef.current === (cursorRestoreKey ?? 0)) return
    if (view.state.doc.length === 0) return
    lastRestoreKeyRef.current = cursorRestoreKey ?? 0
    const len = view.state.doc.length
    const pos = Math.min(restoreCursorPos, len)
    const from = Math.min(restoreSelectionFrom ?? pos, len)
    const to = Math.min(restoreSelectionTo ?? pos, len)
    view.dispatch({
      selection: EditorSelection.range(from, to),
      scrollIntoView: true,
    })
  }, [cursorRestoreKey, content, restoreCursorPos, restoreSelectionFrom, restoreSelectionTo])

  return (
    <div ref={containerRef} className="cmv-container">
      <FindReplacePanel
        view={view}
        open={findReplaceOpen}
        mode={findReplaceMode}
        focusKey={findReplaceFocusKey}
        editable={editable}
        onClose={closeFindReplace}
        onModeChange={setFindReplaceMode}
      />
      {refsPopup && (
        <ReferencesPopup
          locations={refsPopup.locations}
          anchor={refsPopup.anchor}
          queryPos={refsPopup.queryPos}
          projectRootPath={projectRootPath}
          fetchLines={(path, offset, limit) =>
            fsClientMod.read(gatewayClient, { Path: path, Offset: offset, Limit: limit })}
          onFetchWorkspaceRefs={(queryPos) => {
            const roots = (workspaceRootsRef.current ?? []).filter(Boolean)
            const rootPath = projectRootPath ?? ''
            const targets = [rootPath, ...roots.filter((r) => r !== rootPath)]
            return Promise.all(
              targets.map((root) =>
                new LspClient({ client: gatewayClient, rootUri: pathToUri(root) })
                  .references(uriToPath(queryPos.uri), queryPos.line, queryPos.character)
                  .catch(() => [] as Location[]),
              ),
            ).then((perRoot) => {
              const seen = new Set<string>()
              const merged: Location[] = []
              for (const locs of perRoot) {
                for (const loc of locs) {
                  const key = `${loc.uri}:${loc.range.start.line}:${loc.range.start.character}`
                  if (!seen.has(key)) {
                    seen.add(key)
                    merged.push(loc)
                  }
                }
              }
              return merged
            })
          }}
          onClose={() => setRefsPopup(null)}
          onJump={(filePath, line) => onGotoDefinitionRef.current?.(filePath, line)}
        />
      )}
    </div>
  )
}
