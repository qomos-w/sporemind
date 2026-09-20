import React, { useEffect, useMemo, useRef, useState } from 'react'
import {
  EditorView,
  lineNumbers,
  highlightActiveLineGutter,
  highlightSpecialChars,
  drawSelection,
  dropCursor,
  rectangularSelection,
  crosshairCursor,
  highlightActiveLine,
  scrollPastEnd,
  placeholder as cmPlaceholder,
} from '@codemirror/view'
import { EditorState, Compartment } from '@codemirror/state'
import { history } from '@codemirror/commands'
import { indentOnInput, syntaxHighlighting, bracketMatching, foldGutter } from '@codemirror/language'
import { markdown } from '@codemirror/lang-markdown'
import { sporeLanguageDescription } from './sporeLanguage'
import { HighlightStyle } from '@codemirror/language'
import { tags } from '@lezer/highlight'
import { oneDarkTheme } from '@codemirror/theme-one-dark'
import { useEditorKeymap } from './EditorKeymapContext'
import { editingExtensions, type FindReplaceHandlers } from './editing-extensions'
import { FindReplacePanel, useFindReplacePanel } from './FindReplacePanel'
import './CodeMirrorMarkdownEditor.css'

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

const baseExtensions = [
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
  EditorView.theme({
    '&': { backgroundColor: 'transparent', height: '100%' },
    '.cm-scroller': { fontFamily: 'var(--font-mono, monospace)', fontSize: '0.8125rem', lineHeight: '1.6' },
    '.cm-gutters': { backgroundColor: 'transparent', borderRight: 'none', color: 'var(--text-tertiary)' },
    '.cm-activeLineGutter': { backgroundColor: 'var(--bg-hover)' },
    '.cm-activeLine': { backgroundColor: 'var(--bg-hover)' },
    '.cm-placeholder': { color: 'var(--text-tertiary)' },
  }),
]

function isDarkTheme(): boolean {
  return document.documentElement.getAttribute('data-theme') === 'dark'
}

function themeExtension() {
  return isDarkTheme() ? oneDarkTheme : []
}

export interface CodeMirrorMarkdownEditorProps {
  value: string
  onChange: (value: string) => void
  readOnly?: boolean
  className?: string
  placeholder?: string
}

export const CodeMirrorMarkdownEditor: React.FC<CodeMirrorMarkdownEditorProps> = ({
  value,
  onChange,
  readOnly = false,
  className,
  placeholder,
}) => {
  const containerRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const [view, setView] = useState<EditorView | null>(null)
  const themeCompartmentRef = useRef(new Compartment())
  const editableCompartmentRef = useRef(new Compartment())
  const keymapCompartmentRef = useRef(new Compartment())
  const editingCompartmentRef = useRef(new Compartment())
  const readOnlyRef = useRef(readOnly)

  readOnlyRef.current = readOnly

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

    const state = EditorState.create({
      doc: value,
      extensions: [
        ...baseExtensions,
        keymapCompartmentRef.current.of(keymapExtension),
        editingCompartmentRef.current.of(editingExtensions({ editable: !readOnly, findReplace: findReplaceHandlers })),
        themeCompartmentRef.current.of(themeExtension()),
        editableCompartmentRef.current.of(EditorView.editable.of(!readOnly)),
        markdown({ codeLanguages: [sporeLanguageDescription] }),
        placeholder ? cmPlaceholder(placeholder) : [],
        syntaxHighlighting(projectHighlight),
        EditorView.updateListener.of(update => {
          if (!update.docChanged) return
          if (update.transactions.every(tr => tr.isUserEvent('external'))) return
          if (readOnlyRef.current) return
          onChange(update.state.doc.toString())
        }),
      ],
    })

    const view = new EditorView({ state, parent: containerRef.current })
    viewRef.current = view
    setView(view)

    return () => {
      view.destroy()
      viewRef.current = null
      setView(null)
    }
  }, [])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const current = view.state.doc.toString()
    if (current === value) return
    view.dispatch({
      changes: { from: 0, to: view.state.doc.length, insert: value },
      userEvent: 'external',
    })
  }, [value])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    view.dispatch({
      effects: [
        editableCompartmentRef.current.reconfigure(EditorView.editable.of(!readOnly)),
        editingCompartmentRef.current.reconfigure(editingExtensions({ editable: !readOnly, findReplace: findReplaceHandlers })),
      ],
    })
  }, [readOnly])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    view.dispatch({
      effects: keymapCompartmentRef.current.reconfigure(keymapExtension),
    })
  }, [keymapExtension])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return

    const observer = new MutationObserver(() => {
      view.dispatch({
        effects: themeCompartmentRef.current.reconfigure(themeExtension()),
      })
    })

    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    return () => observer.disconnect()
  }, [])

  return (
    <div ref={containerRef} className={`cm-md-editor ${className ?? ''}`}>
      <FindReplacePanel
        view={view}
        open={findReplaceOpen}
        mode={findReplaceMode}
        focusKey={findReplaceFocusKey}
        editable={!readOnly}
        onClose={closeFindReplace}
        onModeChange={setFindReplaceMode}
      />
    </div>
  )
}
