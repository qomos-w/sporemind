import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { CornerDownLeft } from 'lucide-react'
import { client } from '../../../../application/generated-client'
import * as workspace from '../../../../gen-clients/workspace/client'
import type { SlashCommand } from '../../../../gen-clients/system/types'
import { useI18n } from '../../../../i18n'
import { useBrowserOverlay } from '../../browserOverlay'
import { splitDebugInput } from './debugInput'
import './DebugConsoleTab.css'

export interface DebugConsoleRecord {
  id: number
  prompt: string
  output: string
  error: boolean
  pending: boolean
}

/**
 * Debug command console tab — the real implementation behind the `debug`
 * terminal tab type (registered in {@link terminalTabRegistry}).
 *
 * Interaction model mirrors a classic console (reference: go-ebitor console):
 *   - Every executed line becomes a prompt line (`> help`) plus a pre-wrapped
 *     output block; failed executions render the error message in red.
 *   - History lives in memory only (never persisted): ArrowUp/ArrowDown walk
 *     the executed lines, staging the in-progress edit so ArrowDown returns to
 *     it; empty lines are not recorded and a command repeated back-to-back is
 *     recorded only once.
 *   - Completion: the command list (workspace.debug_commands_list) is fetched
 *     once on mount; typing `/` or pressing Tab opens a completion menu that
 *     filters commands by the input prefix and shows name + shortHelp. Tab
 *     cycles through the matches (live-filling the input), ArrowUp/ArrowDown
 *     navigate the menu while it is open, Enter submits the input line as
 *     typed (workspace.debug_command_exec, args tokenized by splitDebugInput),
 *     Escape closes the menu.
 */
export function DebugConsoleTab() {
  const { t } = useI18n()
  const [commands, setCommands] = useState<SlashCommand[]>([])
  const [records, setRecords] = useState<DebugConsoleRecord[]>([])
  const [input, setInput] = useState('')
  const [completionOpen, setCompletionOpen] = useState(false)
  const [completionIndex, setCompletionIndex] = useState(0)

  // In-memory command history (newest last). cursor 0 = live draft position;
  // cursor n > 0 = n steps back into history.
  const historyRef = useRef<string[]>([])
  const historyCursorRef = useRef(0)
  const stagedDraftRef = useRef('')
  // Last value the user actually typed (menu fills overwrite the input without
  // going through onChange, so this preserves the original query for restore).
  const queryDraftRef = useRef('')
  const recordIdRef = useRef(0)
  const outputRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const completionRef = useRef<HTMLDivElement>(null)
  // Guards async continuations against updating state after unmount.
  const aliveRef = useRef(true)

  // Fetch the command list once on mount. The tab body is mounted only while
  // the tab is active, so a single fetch is enough; unmount cancels it.
  useEffect(() => {
    aliveRef.current = true
    workspace
      .debugCommandsList(client)
      .then((resp) => {
        if (aliveRef.current) setCommands(resp.Commands ?? [])
      })
      .catch(() => {
        if (aliveRef.current) setCommands([])
      })
    return () => {
      aliveRef.current = false
    }
  }, [])

  // Keep the output area scrolled to the bottom whenever new records land.
  useEffect(() => {
    const el = outputRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [records])

  // Register the completion menu as an overlay and close it on outside click.
  // The document listener is removed on unmount via the effect cleanup.
  useBrowserOverlay(completionOpen)
  useEffect(() => {
    if (!completionOpen) return
    const handleMousedown = (e: MouseEvent) => {
      if (completionRef.current && !completionRef.current.contains(e.target as Node)) {
        setCompletionOpen(false)
        setCompletionIndex(-1)
      }
    }
    document.addEventListener('mousedown', handleMousedown)
    return () => document.removeEventListener('mousedown', handleMousedown)
  }, [completionOpen])

  // Commands matching the last typed prefix (queryDraftRef, not the input
  // which may be overwritten by menu fills). A leading "/" (slash-command
  // style) is ignored when matching; empty prefix shows every command.
  const matches = useMemo(() => {
    const query = queryDraftRef.current
    const prefix = query.startsWith('/') ? query.slice(1) : query
    const trimmed = prefix.trim().toLowerCase()
    if (!trimmed) return commands
    return commands.filter((c) => c.Name.toLowerCase().startsWith(trimmed))
  }, [commands, input])

  const handleInputChange = useCallback((value: string) => {
    queryDraftRef.current = value
    setInput(value)
    if (!value) {
      setCompletionOpen(false)
      setCompletionIndex(-1)
    } else if (value.startsWith('/')) {
      setCompletionOpen(true)
      setCompletionIndex(-1)
    }
  }, [])

  const closeCompletion = useCallback(() => {
    setCompletionOpen(false)
    setCompletionIndex(-1)
  }, [])

  const submitLine = useCallback(async (rawLine: string) => {
    const line = rawLine.trim()
    if (!line) return

    // go-ebitor history semantics: empty lines are not recorded and a command
    // repeated back-to-back is recorded only once.
    const history = historyRef.current
    if (history[history.length - 1] !== line) history.push(line)
    historyCursorRef.current = 0
    stagedDraftRef.current = ''
    setInput('')
    closeCompletion()

    const args = splitDebugInput(line)
    const name = (args[0] ?? '').replace(/^\//, '')
    if (!name) return

    const id = ++recordIdRef.current
    setRecords((prev) => [...prev, { id, prompt: line, output: '', error: false, pending: true }])
    try {
      const resp = await workspace.debugCommandExec(client, { Name: name, Args: args.slice(1) })
      if (aliveRef.current) {
        setRecords((prev) =>
          prev.map((r) => (r.id === id ? { ...r, output: resp.Output ?? '', pending: false } : r)),
        )
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      if (aliveRef.current) {
        setRecords((prev) =>
          prev.map((r) => (r.id === id ? { ...r, output: message, error: true, pending: false } : r)),
        )
      }
    }
  }, [closeCompletion])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'Tab') {
        e.preventDefault()
        if (!input.trim()) return
        if (matches.length === 0) {
          setCompletionOpen(true)
          setCompletionIndex(-1)
          return
        }
        const nextIndex = completionOpen ? (completionIndex + 1) % matches.length : 0
        setCompletionOpen(true)
        setCompletionIndex(nextIndex)
        setInput(matches[nextIndex]!.Name)
        return
      }

      if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
        // While the completion menu is open and has matches, the arrows
        // navigate the menu (live-filling the input with the highlighted
        // command name) instead of walking history.
        if (completionOpen && matches.length > 0) {
          e.preventDefault()
          const dir = e.key === 'ArrowUp' ? -1 : 1
          // Clamped movement; -1 means "nothing applied yet" (the input still
          // shows the typed query, which is restored when navigating back up).
          const next = Math.max(-1, Math.min(completionIndex + dir, matches.length - 1))
          setCompletionIndex(next)
          setInput(next >= 0 ? matches[next]!.Name : queryDraftRef.current)
          return
        }
        e.preventDefault()
        const history = historyRef.current
        const cursor = historyCursorRef.current
        if (e.key === 'ArrowUp') {
          if (cursor >= history.length) return
          if (cursor === 0) stagedDraftRef.current = input
          const next = cursor + 1
          historyCursorRef.current = next
          setInput(history[history.length - next] ?? '')
        } else {
          if (cursor === 0) return
          const next = cursor - 1
          historyCursorRef.current = next
          setInput(next === 0 ? stagedDraftRef.current : (history[history.length - next] ?? ''))
        }
        return
      }

      if (e.key === 'Enter') {
        e.preventDefault()
        submitLine(input)
        return
      }

      if (e.key === 'Escape' && completionOpen) {
        closeCompletion()
      }
    },
    [input, completionOpen, completionIndex, matches, submitLine, closeCompletion],
  )

  const fillCommand = useCallback(
    (name: string) => {
      setInput(name)
      closeCompletion()
      inputRef.current?.focus()
    },
    [closeCompletion],
  )

  return (
    <div className="debug-console">
      <div className="debug-console-output" ref={outputRef} role="log" aria-live="polite">
        {records.length === 0 && (
          <div className="debug-console-hint">{t('terminalPanel.debug.hint')}</div>
        )}
        {records.map((record) => (
          <div key={record.id} className={`debug-console-record${record.error ? ' error' : ''}`}>
            <div className="debug-console-prompt">&gt; {record.prompt}</div>
            <div className="debug-console-output-block">
              {record.pending ? <span className="debug-console-pending" /> : record.output}
            </div>
          </div>
        ))}
      </div>

      <div className="debug-console-input-row">
        {completionOpen && (
          <div
            className="debug-console-completion"
            ref={completionRef}
            id="debug-console-completion"
            role="listbox"
            aria-label={t('terminalPanel.tab.debug')}
          >
            {commands.length === 0 ? (
              <div className="debug-console-completion-empty">{t('terminalPanel.debug.noCommands')}</div>
            ) : matches.length === 0 ? (
              <div className="debug-console-completion-empty">{t('terminalPanel.debug.noMatch')}</div>
            ) : (
              matches.map((command, index) => (
                <button
                  key={command.Name}
                  type="button"
                  role="option"
                  aria-selected={index === completionIndex}
                  className={`debug-console-completion-item${index === completionIndex ? ' active' : ''}`}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    fillCommand(command.Name)
                  }}
                >
                  <span className="debug-console-completion-name">{command.Name}</span>
                  {command.ShortHelp && (
                    <span className="debug-console-completion-help">{command.ShortHelp}</span>
                  )}
                </button>
              ))
            )}
          </div>
        )}
        <CornerDownLeft size={14} className="debug-console-input-icon" />
        <input
          ref={inputRef}
          className="debug-console-input"
          value={input}
          onChange={(e) => handleInputChange(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={t('terminalPanel.debug.inputPlaceholder')}
          spellCheck={false}
          autoComplete="off"
          autoCapitalize="off"
          autoCorrect="off"
          role="combobox"
          aria-expanded={completionOpen}
          aria-controls="debug-console-completion"
          aria-autocomplete="list"
          aria-label={t('terminalPanel.tab.debug')}
        />
      </div>
    </div>
  )
}
