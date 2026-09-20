import { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronRight, ChevronDown, Database, Play, RefreshCw, Folder, FolderOpen, Table2, FileText, Hash, KeyRound, Layers, Server } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as dbclientApi from '../../../gen-clients/dbclient/client'
import type { DbRows, DbTreeNode, DbDescribeResp } from '../../../gen-types/dbclient'
import { useI18n } from '../../../i18n'
import { useBrowserOverlay } from '../browserOverlay'
import './ShellDbClientView.css'

/**
 * Database client session view (right-panel db-session tab) — a React port of
 * the sporecloud sql-client layout: connection status header, filterable
 * browse tree, and a per-selection 数据 / 结构 / 查询 tab strip. Connections
 * themselves are managed by DbManagerSettings (settings page); this view is
 * bound to one profile and only exercises the read-only dbclient callables
 * (dial_test / tree / read / query / describe).
 *
 * Path conventions (schemas/dbclient._6160.spore):
 *   mysql/postgres: "" -> databases, "db" -> tables, "db.table" -> rows
 *   mongo:          "" -> databases, "db" -> collections, "db.coll" -> docs
 *   redis:          "" -> db indexes, "N" -> keys, "N/key" -> value
 *   etcd:           "" -> top-level, "a/b" -> one dir/key level
 */

export type DbBackendKind =
  | 'mysql'
  | 'postgres'
  | 'mongo'
  | 'redis'
  | 'etcd'
  | 'oss'
  | 'webdav'
  | string

export interface ShellDbClientViewProps {
  profileId: string
  profileName: string
  backend: DbBackendKind
  /** Whether this tab is the active, visible right-panel tab. */
  isActive?: boolean
}

interface TreeNodeState {
  children?: DbTreeNode[]
  loading: boolean
  error?: string
  cursor?: string
  hasMore?: boolean
}

type TabId = 'data' | 'structure' | 'query'

interface GridState {
  kind: 'empty' | 'loading' | 'rows' | 'error'
  rows?: DbRows
  error?: string
  elapsedMs?: number
}

const DATA_LIMITS = [50, 100, 200, 500] as const

const KIND_ICONS: Record<string, React.ReactNode> = {
  database: <Folder size={13} />,
  table: <Table2 size={13} />,
  collection: <Layers size={13} />,
  key: <KeyRound size={13} />,
  keyprefix: <FolderOpen size={13} />,
  dir: <FolderOpen size={13} />,
  bucket: <Server size={13} />,
  object: <FileText size={13} />,
}

function formatError(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/** Backend language hint for the editor + placeholder. */
export function queryMode(backend: DbBackendKind): 'sql' | 'json' | 'cmd' | 'none' {
  switch (backend) {
    case 'mysql':
    case 'postgres':
      return 'sql'
    case 'mongo':
      return 'json'
    case 'redis':
    case 'etcd':
      return 'cmd'
    case 'oss':
    case 'webdav':
      return 'none'
    default:
      return 'sql'
  }
}

/** Per-backend default query editor seed text. */
export function defaultQueryText(backend: DbBackendKind, path: string): string {
  switch (queryMode(backend)) {
    case 'sql': {
      if (path.includes('.')) {
        const [db, table] = path.split('.')
        return `SELECT * FROM ${db}.${table} LIMIT 100;`
      }
      return path ? `-- path "${path}" is not a table (db|table expected)\n` : '-- enter SELECT / WITH / SHOW / EXPLAIN\n'
    }
    case 'json': {
      if (path.includes('.')) {
        const [db, coll] = path.split('.')
        return JSON.stringify({ collection: `${db}.${coll}`, filter: {}, limit: 100 }, null, 2)
      }
      return '{\n  "collection": "<db>.<coll>",\n  "filter": {},\n  "limit": 100\n}\n'
    }
    case 'cmd': {
      if (backend === 'redis') return path ? `-- redis key scan / get\nGET ${path}\n` : '-- redis read command (GET / HGETALL / LRANGE / ZRANGE / XRANGE)\n'
      return path ? `-- etcd range get\nGET ${path}\n` : '-- etcd range get (KEYS/RANGE)\n'
    }
    case 'none':
      return '-- browse only: this backend has no query editor\n'
  }
}

/** Whether the given kind is a leaf we can preview via dbclient.read. */
export function canPreviewLeaf(_backend: DbBackendKind, kind: string): boolean {
  return kind === 'table' || kind === 'collection' || kind === 'key' || kind === 'object'
}

function isSQLBackend(backend: string): boolean {
  return backend === 'mysql' || backend === 'postgres'
}

function renderCell(value: string): React.ReactNode {
  if (value === 'null') return <span className="dbc-cell-null">null</span>
  return value
}

export function ShellDbClientView({ profileId, profileName, backend, isActive = true }: ShellDbClientViewProps) {
  const { t } = useI18n()

  // ── connection status ──
  const [connState, setConnState] = useState<'idle' | 'testing' | 'ok' | 'err'>('idle')
  const [connDetail, setConnDetail] = useState<{ latencyMs?: number; version?: string; error?: string }>({})

  // ── tree ──
  const [tree, setTree] = useState<Record<string, TreeNodeState>>({ root: { loading: false } })
  const [filter, setFilter] = useState('')
  const [selection, setSelection] = useState<{ path: string; label: string; kind: string } | null>(null)
  const [tab, setTab] = useState<TabId>('data')

  // ── panes ──
  const [dataLimit, setDataLimit] = useState<number>(200)
  const [dataState, setDataState] = useState<GridState>({ kind: 'empty' })
  const [structState, setStructState] = useState<{ kind: 'empty' | 'loading' | 'done' | 'error'; resp?: DbDescribeResp; error?: string }>({ kind: 'empty' })
  const [queryText, setQueryText] = useState<string>(() => defaultQueryText(backend, ''))
  const [queryState, setQueryState] = useState<GridState>({ kind: 'empty' })

  // ── overlays (native browser window must hide under them) ──
  const [expandedCell, setExpandedCell] = useState<{ col: string; value: string } | null>(null)
  useBrowserOverlay(expandedCell !== null)

  const profileIdRef = useRef(profileId)
  profileIdRef.current = profileId
  const selectionRef = useRef(selection)
  selectionRef.current = selection

  // Reset local state when the profile changes (same tab reused across profiles).
  useEffect(() => {
    setConnState('idle')
    setConnDetail({})
    setTree({ root: { loading: false } })
    setSelection(null)
    setTab('data')
    setDataState({ kind: 'empty' })
    setStructState({ kind: 'empty' })
    setQueryText(defaultQueryText(backend, ''))
    setQueryState({ kind: 'empty' })
  }, [profileId, backend])

  const handleTestConnection = useCallback(async () => {
    setConnState('testing')
    setConnDetail({})
    try {
      const resp = await dbclientApi.dialTest(client, { ProfileId: profileIdRef.current })
      if (resp.Ok) {
        setConnState('ok')
        setConnDetail({ latencyMs: resp.LatencyMs, version: resp.ServerVersion })
      } else {
        setConnState('err')
        setConnDetail({ error: resp.Error ?? t('db.status.error') })
      }
    } catch (e) {
      setConnState('err')
      setConnDetail({ error: formatError(e) })
    }
  }, [t])

  const loadChildren = useCallback(async (path: string, cursor?: string) => {
    const key = path || 'root'
    setTree(prev => ({ ...prev, [key]: { ...(prev[key] ?? { loading: true }), loading: true, error: undefined } }))
    try {
      const resp = await dbclientApi.tree(client, { ProfileId: profileIdRef.current, Path: path, ...(cursor ? { Cursor: cursor } : {}) })
      setTree(prev => {
        const existing = prev[key]?.children ?? []
        const merged = cursor ? [...existing, ...(resp.Nodes ?? [])] : (resp.Nodes ?? [])
        // Cursor pages can repeat nodes (redis SCAN): dedupe by canonical path.
        const seen = new Set<string>()
        const unique = merged.filter(n => {
          const k = n.Path || n.Label
          if (seen.has(k)) return false
          seen.add(k)
          return true
        })
        return {
          ...prev,
          [key]: {
            children: unique,
            loading: false,
            cursor: resp.Cursor,
            hasMore: resp.HasMore,
          },
        }
      })
    } catch (e) {
      setTree(prev => ({ ...prev, [key]: { ...(prev[key] ?? { loading: false }), loading: false, error: formatError(e) } }))
    }
  }, [])

  // Eagerly load the root + refresh the connection LED once the tab becomes
  // visible; isActive protects background tabs from restore storms.
  useEffect(() => {
    if (!isActive) return
    const root = tree.root
    if (!root?.children && !root?.loading && !root?.error) void loadChildren('', undefined)
    if (connState === 'idle') void handleTestConnection()
    // initial-trigger only: tree.root / connState intentionally omitted.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isActive, profileId])

  const handleToggleNode = useCallback((node: DbTreeNode) => {
    const key = node.Path || 'root'
    const state = tree[key]
    if (state?.children) {
      setTree(prev => {
        const next = { ...prev }
        delete next[key]
        return next
      })
      return
    }
    if (state?.loading) return
    void loadChildren(node.Path, undefined)
  }, [tree, loadChildren])

  const handleLoadMore = useCallback((path: string) => {
    const key = path || 'root'
    const node = tree[key]
    if (!node?.cursor || !node.hasMore) return
    void loadChildren(path, node.cursor)
  }, [tree, loadChildren])

  const loadData = useCallback(async () => {
    const sel = selectionRef.current
    const id = profileIdRef.current
    if (!sel || !id) return
    setDataState({ kind: 'loading' })
    const started = performance.now()
    try {
      const rows = await dbclientApi.read(client, { ProfileId: id, Path: sel.path, Limit: dataLimit })
      setDataState({ kind: 'rows', rows, elapsedMs: Math.round(performance.now() - started) })
    } catch (e) {
      setDataState({ kind: 'error', error: formatError(e) })
    }
  }, [dataLimit])

  const loadStructure = useCallback(async () => {
    const sel = selectionRef.current
    const id = profileIdRef.current
    if (!sel || !id) return
    setStructState({ kind: 'loading' })
    try {
      const resp = await dbclientApi.describe(client, { ProfileId: id, Path: sel.path })
      setStructState({ kind: 'done', resp })
    } catch (e) {
      setStructState({ kind: 'error', error: formatError(e) })
    }
  }, [])

  const handleSelectLeaf = useCallback((node: DbTreeNode) => {
    setSelection({ path: node.Path, label: node.Label, kind: node.Kind })
    setTab('data')
    setQueryText(defaultQueryText(backend, node.Path))
  }, [backend])

  // Data/structure reload on selection / limit change.
  useEffect(() => {
    if (!selection) return
    void loadData()
    if (isSQLBackend(backend) && selection.kind === 'table') void loadStructure()
    else setStructState({ kind: 'empty' })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selection, dataLimit])

  const handleRunQuery = useCallback(async () => {
    const id = profileIdRef.current
    const text = queryText.trim()
    if (!id || !text) return
    setQueryState({ kind: 'loading' })
    setSelection(null)
    const started = performance.now()
    try {
      const mode = queryMode(backend)
      const rows = await dbclientApi.query(client, {
        ProfileId: id,
        Text: text,
        ...(mode === 'none' ? {} : { Mode: mode }),
      })
      setQueryState({ kind: 'rows', rows, elapsedMs: Math.round(performance.now() - started) })
    } catch (e) {
      setQueryState({ kind: 'error', error: formatError(e) })
    }
  }, [queryText, backend])

  const filterText = filter.trim().toLowerCase()
  const matchesFilter = useCallback((label: string) => !filterText || label.toLowerCase().includes(filterText), [filterText])

  const mode = queryMode(backend)
  const showQuery = mode !== 'none'
  const sqlBackend = isSQLBackend(backend)
  const rootState = tree.root

  const renderNode = useCallback((node: DbTreeNode, depth: number) => {
    const key = node.Path || 'root'
    const state = tree[key]
    const expanded = !!state?.children
    const isLeaf = canPreviewLeaf(backend, node.Kind)
    if (filterText && !matchesFilter(node.Label) && !expanded) return null
    return (
      <div key={key} className="dbc-node">
        <div
          className={`dbc-row ${state?.loading ? 'dbc-row-loading' : ''} ${selection?.path === node.Path ? 'dbc-row-on' : ''}`}
          style={{ paddingLeft: 6 + depth * 14 }}
          onClick={() => {
            if (state?.loading) return
            if (isLeaf) handleSelectLeaf(node)
            else handleToggleNode(node)
          }}
        >
          <span className="dbc-row-toggle">
            {isLeaf ? <span className="dbc-leaf-dot" /> : expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          </span>
          <span className="dbc-row-icon">{KIND_ICONS[node.Kind] ?? <Hash size={12} />}</span>
          <span className="dbc-row-label" title={node.Path}>{node.Label}</span>
        </div>
        {state?.loading && <div className="dbc-sub-loading" style={{ paddingLeft: 6 + (depth + 1) * 14 }}>…</div>}
        {state?.error && <div className="dbc-sub-error" style={{ paddingLeft: 6 + (depth + 1) * 14 }} title={state.error}>{state.error}</div>}
        {expanded && state.children && (
          <div className="dbc-children">
            {state.children.map(child => renderNode(child, depth + 1))}
            {state.hasMore && (
              <button
                type="button"
                className="dbc-more"
                style={{ paddingLeft: 6 + (depth + 1) * 14 }}
                onClick={e => { e.stopPropagation(); handleLoadMore(node.Path) }}
              >
                {t('db.more')}
              </button>
            )}
          </div>
        )}
      </div>
    )
  }, [tree, backend, selection, filterText, matchesFilter, handleSelectLeaf, handleToggleNode, handleLoadMore, t])

  return (
    <div className="dbc-root">
      <header className="dbc-header">
        <div className="dbc-brand">
          <Database size={14} />
          <span className="dbc-brand-name" title={profileName}>{profileName}</span>
          <span className="dbc-brand-backend">{backend || 'unknown'}</span>
        </div>
        <div className={`dbc-conn dbc-conn-${connState}`} title={connDetail.version ?? connDetail.error ?? ''}>
          <span className="dbc-led" />
          <span>
            {connState === 'ok' && (connDetail.version || `OK · ${connDetail.latencyMs ?? '?'}ms`)}
            {connState === 'err' && (connDetail.error ?? t('db.status.error'))}
            {connState === 'testing' && t('db.status.testing')}
            {connState === 'idle' && t('db.status.idle')}
          </span>
        </div>
        <div className="dbc-spacer" />
        <button
          className="dbc-btn dbc-btn-ghost"
          onClick={() => { void handleTestConnection(); setTree({ root: { loading: false } }); void loadChildren('') }}
          title={t('db.actions.test')}
        >
          <RefreshCw size={12} />
        </button>
      </header>

      <main className="dbc-main">
        <aside className="dbc-side">
          <div className="dbc-side-tools">
            <input
              value={filter}
              onChange={e => setFilter(e.target.value)}
              placeholder={t('db.client.filterPlaceholder')}
              aria-label={t('db.client.filterPlaceholder')}
              spellCheck={false}
            />
          </div>
          <div className="dbc-tree">
            {rootState?.loading && !rootState?.children && <div className="dbc-empty">{t('common.loading')}</div>}
            {rootState?.error && <div className="dbc-tree-error" title={rootState.error}>{rootState.error}</div>}
            {rootState?.children && rootState.children.length === 0 && (
              <div className="dbc-empty">{t('db.tree.empty')}</div>
            )}
            {rootState?.children?.filter(n => !filterText || matchesFilter(n.Label)).length === 0 && rootState?.children && rootState.children.length > 0 && (
              <div className="dbc-empty">{t('db.client.treeNoMatch')}</div>
            )}
            {rootState?.children?.map(n => renderNode(n, 0))}
            {rootState?.hasMore && (
              <button
                type="button"
                className="dbc-more"
                onClick={() => handleLoadMore('')}
              >
                {t('db.more')}
              </button>
            )}
          </div>
        </aside>

        <section className="dbc-work">
          <div className="dbc-crumbs">
            <span className="dbc-path">{selection ? selection.path : '—'}</span>
          </div>

          <nav className="dbc-tabs">
            <button className={`dbc-tab ${tab === 'data' ? 'dbc-tab-on' : ''}`} onClick={() => setTab('data')}>
              {t('db.client.tab.data')}
            </button>
            <button
              className={`dbc-tab ${tab === 'structure' ? 'dbc-tab-on' : ''}`}
              onClick={() => setTab('structure')}
              disabled={!sqlBackend}
              title={sqlBackend ? undefined : t('db.client.structureUnsupported')}
            >
              {t('db.client.tab.structure')}
            </button>
            {showQuery && (
              <button className={`dbc-tab ${tab === 'query' ? 'dbc-tab-on' : ''}`} onClick={() => setTab('query')}>
                {t('db.client.tab.query')}
              </button>
            )}
          </nav>

          {tab === 'data' && (
            <div className="dbc-page">
              <div className="dbc-toolbar">
                <span className="dbc-muted">{t('db.client.autoSelect')}</span>
                <label htmlFor="dbc-data-limit">{t('db.client.limit')}</label>
                <select id="dbc-data-limit" value={dataLimit} onChange={e => setDataLimit(Number(e.target.value))}>
                  {DATA_LIMITS.map(n => <option key={n} value={n}>{n}</option>)}
                </select>
                <button className="dbc-btn dbc-btn-ghost" disabled={!selection || dataState.kind === 'loading'} onClick={loadData}>
                  <RefreshCw size={12} />
                </button>
                <div className="dbc-spacer" />
                {dataState.kind === 'rows' && dataState.rows && (
                  <span className="dbc-muted">
                    {t('db.client.rowsInfo', { n: dataState.rows.Rows?.length ?? 0, ms: dataState.elapsedMs ?? 0 })}
                    {dataState.rows.Truncated ? ` · ${t('db.client.truncated')}` : ''}
                  </span>
                )}
              </div>
              <div className="dbc-grid-wrap">
                {!selection && <div className="dbc-empty">{t('db.client.selectTable')}</div>}
                {selection && dataState.kind === 'loading' && <div className="dbc-empty">{t('common.loading')}</div>}
                {selection && dataState.kind === 'error' && <div className="dbc-error">{dataState.error}</div>}
                {selection && dataState.kind === 'rows' && dataState.rows && (
                  <ResultGrid rows={dataState.rows} onExpandCell={setExpandedCell} />
                )}
              </div>
            </div>
          )}

          {tab === 'structure' && (
            <div className="dbc-page">
              <div className="dbc-struct-wrap">
                {!selection && <div className="dbc-empty">{t('db.client.structureEmpty')}</div>}
                {selection && structState.kind === 'loading' && <div className="dbc-empty">{t('common.loading')}</div>}
                {selection && structState.kind === 'error' && <div className="dbc-error">{structState.error}</div>}
                {selection && structState.kind === 'done' && structState.resp && (
                  <>
                    <h3>{t('db.client.columns')} ({structState.resp.Columns.length})</h3>
                    <div className="dbc-grid-card">
                      <table className="dbc-grid">
                        <thead>
                          <tr><th>Name</th><th>Type</th><th>Nullable</th><th>Default</th><th>Key</th><th>Extra</th></tr>
                        </thead>
                        <tbody>
                          {structState.resp.Columns.map(c => (
                            <tr key={c.Name}>
                              <td>{c.Name}</td>
                              <td>{c.DataType}</td>
                              <td>{c.Nullable ? 'YES' : 'NO'}</td>
                              <td>{c.Default ? renderCell(c.Default) : ''}</td>
                              <td>{c.Key ?? ''}</td>
                              <td>{c.Extra ?? ''}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                    <h3>{t('db.client.indexes')} ({structState.resp.Indexes.length})</h3>
                    <div className="dbc-grid-card">
                      <table className="dbc-grid">
                        <thead>
                          <tr><th>Name</th><th>Columns</th><th>Unique</th></tr>
                        </thead>
                        <tbody>
                          {structState.resp.Indexes.map(i => (
                            <tr key={i.Name}>
                              <td>{i.Name}</td>
                              <td>{i.Columns}</td>
                              <td>{i.Unique ? '✔' : ''}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </>
                )}
              </div>
            </div>
          )}

          {tab === 'query' && showQuery && (
            <div className="dbc-page">
              <div className="dbc-query-bar">
                <span className={`dbc-mode dbc-mode-${mode}`}>{mode.toUpperCase()}</span>
                <span className="dbc-muted">{t(`db.editor.hint.${mode}`)}</span>
                <div className="dbc-spacer" />
                <span className="dbc-muted">
                  {queryState.kind === 'rows' && queryState.rows
                    ? t('db.client.rowsInfo', { n: queryState.rows.Rows?.length ?? 0, ms: queryState.elapsedMs ?? 0 }) +
                      (queryState.rows.Truncated ? ` · ${t('db.client.truncated')}` : '')
                    : ''}
                </span>
                <button className="dbc-btn" disabled={!queryText.trim() || queryState.kind === 'loading'} onClick={handleRunQuery}>
                  <Play size={12} /> {t('db.actions.run')}
                </button>
              </div>
              <textarea
                className="dbc-sql"
                aria-label={t('db.client.tab.query')}
                value={queryText}
                onChange={e => setQueryText(e.target.value)}
                spellCheck={false}
                placeholder={t(`db.editor.placeholder.${mode}`)}
                onKeyDown={e => {
                  if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                    e.preventDefault()
                    void handleRunQuery()
                  }
                }}
              />
              <div className="dbc-grid-wrap">
                {queryState.kind === 'empty' && <div className="dbc-empty">{t('db.result.emptyHint')}</div>}
                {queryState.kind === 'loading' && <div className="dbc-empty">{t('common.loading')}</div>}
                {queryState.kind === 'error' && <div className="dbc-error">{queryState.error}</div>}
                {queryState.kind === 'rows' && queryState.rows && (
                  <>
                    <ResultGrid rows={queryState.rows} onExpandCell={setExpandedCell} />
                    {queryState.rows.Message && <div className="dbc-result-message">{queryState.rows.Message}</div>}
                  </>
                )}
              </div>
            </div>
          )}
        </section>
      </main>

      {expandedCell && (
        <div className="dbc-modal" role="dialog" aria-modal="true" onClick={() => setExpandedCell(null)}>
          <div className="dbc-modal-box dbc-modal-cell" onClick={e => e.stopPropagation()}>
            <div className="dbc-modal-head">
              <h2>{expandedCell.col}</h2>
              <div className="dbc-spacer" />
              <button className="dbc-btn dbc-btn-ghost" onClick={() => setExpandedCell(null)} aria-label="close">✕</button>
            </div>
            <pre className="dbc-cell-full">{expandedCell.value}</pre>
          </div>
        </div>
      )}
    </div>
  )
}

const LONG_CELL = 60

/** Shared results grid: sticky header, ellipsized cells, click-to-expand long cells. */
function ResultGrid({ rows, onExpandCell }: { rows: DbRows; onExpandCell: (cell: { col: string; value: string }) => void }) {
  if (!rows.Columns?.length && !rows.Rows?.length) {
    return <div className="dbc-empty">∅</div>
  }
  return (
    <table className="dbc-grid">
      <thead>
        <tr>{(rows.Columns ?? []).map(c => <th key={c}>{c}</th>)}</tr>
      </thead>
      <tbody>
        {(rows.Rows ?? []).map((row, i) => (
          <tr key={i}>
            {row.map((cell, j) => {
              const col = rows.Columns[j] ?? String(j)
              const isLong = cell.length > LONG_CELL
              return (
                <td
                  key={j}
                  className={isLong ? 'dbc-cell-long' : undefined}
                  title={isLong ? undefined : cell}
                  onClick={isLong ? () => onExpandCell({ col, value: cell }) : undefined}
                >
                  {renderCell(cell)}
                </td>
              )
            })}
          </tr>
        ))}
      </tbody>
    </table>
  )
}
