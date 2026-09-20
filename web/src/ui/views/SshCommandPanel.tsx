import { useState, useCallback, useEffect, useMemo, useRef } from 'react'
import {
  Send, Save, Trash2, Plus, RefreshCw, Search,
  ChevronRight, ChevronDown, Folder,
} from 'lucide-react'
import { client } from '../../application/generated-client'
import * as ssh from '../../gen-clients/sshmanager/client'
import type { SshCommandSnippet } from '../../gen-types/sshmanager'
import { useI18n } from '../../i18n'
import { ResizeHandle } from '../components/ResizeHandle'
import { saveSshSessionUIPrefs } from '../../application/ssh-ui-state'
import './SshCommandPanel.css'

export interface SshCommandPanelProps {
  /** Active SSH session; the "Send" action writes editor content into this PTY. */
  sessionId: string
  /**
   * Persisted shared SSH UI pref for the tree width. When provided it
   * overrides the default until the user drags the handle again.
   */
  initialTreeWidth?: number
}

/** Sentinel key used to bucket commands whose Category is blank. */
const UNCATEGORIZED_KEY = '__uncategorized__'

function formatError(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e)
}

/**
 * Shared command-favourites editor: a category-grouped tree on the left, a
 * command editor on the right, and Send / Save / Delete actions at the bottom.
 *
 * All command snippets are global (shared across SSH hosts) and are read &
 * written through the `sshmanager.command_*` CRUD API. "Send" pushes the editor
 * content into the PTY of the bound `sessionId` via `shell_input`.
 */
export function SshCommandPanel({ sessionId, initialTreeWidth }: SshCommandPanelProps) {
  const { t } = useI18n()

  const [commands, setCommands] = useState<SshCommandSnippet[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [category, setCategory] = useState('')
  const [content, setContent] = useState('')
  const [search, setSearch] = useState('')
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [treeWidth, setTreeWidth] = useState(initialTreeWidth ?? 180)
  const nameInputRef = useRef<HTMLInputElement>(null)
  const treeRef = useRef<HTMLDivElement>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const showToast = useCallback((msg: string) => {
    setToast(msg)
    if (toastTimer.current) clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToast(null), 2500)
  }, [])

  useEffect(() => () => { if (toastTimer.current) clearTimeout(toastTimer.current) }, [])

  const loadCommands = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const resp = await ssh.commandList(client, {})
      setCommands(resp.Items ?? [])
    } catch (e) {
      setError(formatError(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void loadCommands() }, [loadCommands])

  // Group flat snippet list by Category (blank → Uncategorized), apply search.
  const grouped = useMemo(() => {
    const q = search.trim().toLowerCase()
    const filtered = q
      ? commands.filter(c =>
          c.Name.toLowerCase().includes(q) ||
          c.Content.toLowerCase().includes(q) ||
          c.Category.toLowerCase().includes(q))
      : commands
    const map = new Map<string, SshCommandSnippet[]>()
    for (const c of filtered) {
      const key = c.Category.trim() || UNCATEGORIZED_KEY
      const arr = map.get(key) ?? []
      arr.push(c)
      map.set(key, arr)
    }
    return [...map.entries()].sort((a, b) => a[0].localeCompare(b[0]))
  }, [commands, search])

  const categoryLabel = useCallback(
    (key: string) => key === UNCATEGORIZED_KEY ? t('sshCmd.uncategorized') : key,
    [t],
  )

  const toggleCategory = useCallback((key: string) => {
    setCollapsed(prev => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])

  const selectCommand = useCallback((c: SshCommandSnippet) => {
    setSelectedId(c.Id)
    setName(c.Name)
    setCategory(c.Category)
    setContent(c.Content)
    setError(null)
  }, [])

  const newCommand = useCallback(() => {
    setSelectedId(null)
    setName('')
    setCategory('')
    setContent('')
    setError(null)
    nameInputRef.current?.focus()
  }, [])

  const handleSend = useCallback(async () => {
    if (!content.trim() || busy) return
    setBusy(true)
    setError(null)
    try {
      const data = content.endsWith('\n') ? content : content + '\n'
      await ssh.shellInput(client, { SessionId: sessionId, Data: data })
      showToast(t('sshCmd.sent'))
    } catch (e) {
      setError(formatError(e))
    } finally {
      setBusy(false)
    }
  }, [content, busy, sessionId, showToast, t])

  const handleSave = useCallback(async () => {
    const trimmedName = name.trim()
    if (!trimmedName) { setError(t('sshCmd.nameRequired')); return }
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      if (selectedId) {
        await ssh.commandUpdate(client, {
          Id: selectedId,
          Name: trimmedName,
          Content: content,
          Category: category.trim(),
        })
      } else {
        const resp = await ssh.commandCreate(client, {
          Name: trimmedName,
          Content: content,
          Category: category.trim(),
        })
        if (resp.Item) setSelectedId(resp.Item.Id)
      }
      await loadCommands()
      showToast(t('sshCmd.saved'))
    } catch (e) {
      setError(formatError(e))
    } finally {
      setBusy(false)
    }
  }, [name, content, category, selectedId, busy, loadCommands, showToast, t])

  const handleDelete = useCallback(async () => {
    if (!selectedId || busy) return
    setBusy(true)
    setError(null)
    try {
      await ssh.commandRemove(client, { Id: selectedId })
      setSelectedId(null)
      setName('')
      setCategory('')
      setContent('')
      await loadCommands()
      showToast(t('sshCmd.deleted'))
    } catch (e) {
      setError(formatError(e))
    } finally {
      setBusy(false)
    }
  }, [selectedId, busy, loadCommands, showToast, t])

  const canSend = content.trim().length > 0 && !busy
  const canDelete = !!selectedId && !busy

  return (
    <div className="ssh-cmd-panel">
      <div className="ssh-cmd-panel-body">
        {/* ── Category tree ── */}
        <div className="ssh-cmd-tree" ref={treeRef} style={{ width: treeWidth }}>
          <div className="ssh-cmd-tree-search">
            <Search size={16} />
            <input
              type="text"
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder={t('sshCmd.search')}
              spellCheck={false}
            />
            <button
              type="button"
              className="ssh-cmd-icon-btn"
              onClick={newCommand}
              title={t('sshCmd.newCommand')}
              aria-label={t('sshCmd.newCommand')}
            >
              <Plus size={14} />
            </button>
            <button
              type="button"
              className="ssh-cmd-icon-btn"
              onClick={() => void loadCommands()}
              disabled={loading}
              title={t('common.refresh')}
              aria-label={t('common.refresh')}
            >
              <RefreshCw size={14} />
            </button>
          </div>
          <div className="ssh-cmd-tree-list">
            {grouped.length === 0 ? (
              <div className="ssh-cmd-tree-empty">
                {search ? t('sshCmd.emptyFiltered') : t('sshCmd.empty')}
              </div>
            ) : (
              grouped.map(([key, items]) => {
                const open = !collapsed.has(key)
                return (
                  <div key={key} className="ssh-cmd-cat">
                    <button
                      type="button"
                      className="ssh-cmd-cat-header"
                      onClick={() => toggleCategory(key)}
                    >
                      {open
                        ? <ChevronDown size={16} />
                        : <ChevronRight size={16} />}
                      <Folder size={16} />
                      <span className="ssh-cmd-cat-name">{categoryLabel(key)}</span>
                      <span className="ssh-cmd-cat-count">{items.length}</span>
                    </button>
                    {open && items.map(c => (
                      <button
                        key={c.Id}
                        type="button"
                        className={'ssh-cmd-item' + (c.Id === selectedId ? ' selected' : '')}
                        onClick={() => selectCommand(c)}
                        title={c.Content}
                      >
                        <span className="ssh-cmd-item-name">{c.Name}</span>
                      </button>
                    ))}
                  </div>
                )
              })
            )}
          </div>
        </div>

        {/* ── Editor ── */}
        <ResizeHandle
        direction="horizontal"
        targetRef={treeRef}
        minSize={120}
        onResize={size => { const w = Math.max(120, Math.min(320, size)); setTreeWidth(w); saveSshSessionUIPrefs({ commandTreeWidth: w }).catch(() => {}) }}
      />

      <div className="ssh-cmd-editor">
          <div className="ssh-cmd-editor-fields">
            <label className="ssh-cmd-field">
              <span className="ssh-cmd-field-label">{t('sshCmd.name')}</span>
              <input
                type="text"
                className="ssh-command-panel-input"
                value={name}
                onChange={e => setName(e.target.value)}
                placeholder={t('sshCmd.name')}
                ref={nameInputRef}
              />
            </label>
            <label className="ssh-cmd-field">
              <span className="ssh-cmd-field-label">{t('sshCmd.category')}</span>
              <input
                type="text"
                className="ssh-command-panel-input"
                value={category}
                onChange={e => setCategory(e.target.value)}
                placeholder={t('sshCmd.category')}
              />
            </label>
          </div>

          <div className="ssh-cmd-editor-content">
            <textarea
              className="ssh-cmd-textarea"
              value={content}
              onChange={e => setContent(e.target.value)}
              placeholder={t('sshCmd.contentPlaceholder')}
              spellCheck={false}
            />
          </div>

          {error && <div className="ssh-cmd-error">{error}</div>}

          <div className="ssh-cmd-editor-actions">
            <button
              type="button"
              className="ssh-cmd-btn primary"
              onClick={() => void handleSend()}
              disabled={!canSend}
            >
              <Send size={16} />
              {t('common.send')}
            </button>
            <button
              type="button"
              className="ssh-cmd-btn"
              onClick={() => void handleSave()}
              disabled={busy}
            >
              <Save size={16} />
              {t('common.save')}
            </button>
            <button
              type="button"
              className="ssh-cmd-btn danger"
              onClick={() => void handleDelete()}
              disabled={!canDelete}
            >
              <Trash2 size={16} />
              {t('common.delete')}
            </button>
          </div>
        </div>
      </div>

      {toast && <div className="ssh-cmd-toast">{toast}</div>}
    </div>
  )
}
