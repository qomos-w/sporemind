import { useState, useCallback, useEffect, useRef, useMemo, type DragEvent } from 'react'
import {
  Plus, Trash2, Edit2, Terminal, Server, RefreshCw, X,
  ChevronRight, ChevronDown, FolderOpen, EyeOff,
} from 'lucide-react'
import { client } from '../../application/generated-client'
import * as sshApi from '../../gen-clients/sshmanager/client'
import type { SshSessionInfo } from '../../gen-types/sshmanager'
import type { SshHostView } from '../../gen-types/sshmanager.view'
import { FilePicker } from '../components/FilePicker'
import { useMenuDismiss } from '../ai/hooks/useMenuDismiss'
import { useBrowserOverlay } from '../ai/browserOverlay'
import { getFolderIcon, getEmptyFileIcon } from '../panels/file-icons'
import { useI18n } from '../../i18n'
import './SshManagerPanel.css'

function formatError(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e)
}

interface GroupState {
  [group: string]: boolean
}

export interface SshManagerPanelProps {
  onOpenSession: (sessionId: string, hostId: string, hostName: string) => void
  /** Whether the SSH mode is the active, visible content pane. When false, session polling is paused. */
  isActive?: boolean
}

export function SshManagerPanel({ onOpenSession, isActive = true }: SshManagerPanelProps) {
  const { t } = useI18n()
  const [hosts, setHosts] = useState<SshHostView[]>([])
  const [folders, setFolders] = useState<string[]>([])
  const [sessions, setSessions] = useState<SshSessionInfo[]>([])
  const [loadingHosts, setLoadingHosts] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [rootExpanded, setRootExpanded] = useState(true)
  const [groupExpanded, setGroupExpanded] = useState<GroupState>({})

  const dragRef = useRef<{ kind: 'host'; id: string } | { kind: 'folder'; name: string } | null>(null)
  const [dropTarget, setDropTarget] = useState<string | null>(null)

  const [ctxMenu, setCtxMenu] = useState<
    | { type: 'root'; x: number; y: number }
    | { type: 'group'; group: string; x: number; y: number }
    | { type: 'host'; hostId: string; x: number; y: number }
    | null
  >(null)
  const menuRef = useRef<HTMLDivElement>(null)
  useMenuDismiss(menuRef, () => setCtxMenu(null), ctxMenu)

  const [hostDialogOpen, setHostDialogOpen] = useState(false)
  const [editingHost, setEditingHost] = useState<SshHostView | null>(null)
  const [hName, setHName] = useState('')
  const [hAddr, setHAddr] = useState('')
  const [hPort, setHPort] = useState(22)
  const [hUser, setHUser] = useState('')
  const [hAuth, setHAuth] = useState<'password' | 'key'>('password')
  const [hGroup, setHGroup] = useState('')
  const [hAgentInvisible, setHAgentInvisible] = useState(false)
  const [hPass, setHPass] = useState('')
  const [hKeyPath, setHKeyPath] = useState('')
  const [hKeyData, setHKeyData] = useState('')
  const [keyPickerOpen, setKeyPickerOpen] = useState(false)

  const [folderDialog, setFolderDialog] = useState<{ mode: 'create' } | { mode: 'rename'; from: string } | null>(null)
  const [gName, setGName] = useState('')

  useBrowserOverlay(ctxMenu !== null || hostDialogOpen || folderDialog !== null || keyPickerOpen)

  const refreshHosts = useCallback(async () => {
    setLoadingHosts(true)
    try {
      const resp = await sshApi.hostList(client, {})
      setHosts(resp.Items ?? [])
      setFolders(resp.Groups ?? [])
    } catch (e) {
      setError(formatError(e))
    } finally {
      setLoadingHosts(false)
    }
  }, [])

  const refreshSessions = useCallback(async () => {
    try {
      const resp = await sshApi.sessionList(client, {})
      setSessions(resp.Items ?? [])
    } catch { /* silent */ }
  }, [])

  useEffect(() => {
    refreshHosts()
  }, [refreshHosts])

  useEffect(() => {
    if (!isActive) return
    refreshSessions()
    const iv = setInterval(refreshSessions, 5000)
    return () => clearInterval(iv)
  }, [isActive, refreshSessions])

  // derive groups (in persisted folder order) + ungrouped hosts
  const { groups, ungrouped } = useMemo(() => {
    const byGroup = new Map<string, SshHostView[]>()
    const u: SshHostView[] = []
    for (const h of hosts) {
      if (h.Group) {
        const arr = byGroup.get(h.Group) ?? []
        arr.push(h)
        byGroup.set(h.Group, arr)
      } else {
        u.push(h)
      }
    }
    const ordered = folders.map((name): [string, SshHostView[]] => [name, byGroup.get(name) ?? []])
    return { groups: ordered, ungrouped: u }
  }, [hosts, folders])

  const handleOpenShell = async (hostId: string) => {
    const host = hosts.find(h => h.Id === hostId)
    if (!host) return
    let sid = sessions.find(s => s.HostId === hostId && s.Connected)?.SessionId
    if (!sid) {
      try {
        const resp = await sshApi.shellOpen(client, { HostId: hostId, InitialCols: 80, InitialRows: 24 })
        if (!resp.SessionId || !resp.Connected) {
          setError(resp.Error || t('settings.ssh.error.openSessionFailed'))
          return
        }
        sid = resp.SessionId
        refreshSessions()
      } catch (e) {
        setError(formatError(e))
        return
      }
    }
    onOpenSession(sid!, hostId, host.Name)
  }

  const handleRemoveHost = async (id: string) => {
    if (!window.confirm(t('settings.ssh.confirmRemove'))) return
    try {
      await sshApi.hostRemove(client, { Id: id })
      refreshHosts()
    } catch (e) {
      setError(formatError(e))
    }
  }

  const handleRemoveFolder = async (name: string) => {
    if (!window.confirm(t('settings.ssh.confirmRemoveGroup'))) return
    try {
      await sshApi.folderRemove(client, { Name: name })
      refreshHosts()
    } catch (e) {
      setError(formatError(e))
    }
  }

  const handleFolderSubmit = async () => {
    const name = gName.trim()
    if (!name || !folderDialog) return
    try {
      if (folderDialog.mode === 'rename') {
        await sshApi.folderRename(client, { From: folderDialog.from, To: name })
      } else {
        await sshApi.folderCreate(client, { Name: name })
        setGroupExpanded(s => ({ ...s, [name]: true }))
      }
      setFolderDialog(null)
      refreshHosts()
    } catch (e) {
      setError(formatError(e))
    }
  }

  const handleMoveHost = async (hostId: string, group: string) => {
    const h = hosts.find(x => x.Id === hostId)
    if (!h || (h.Group || '') === group) return
    try {
      await sshApi.hostUpdate(client, {
        Id: h.Id, Name: h.Name, Host: h.Host, Port: h.Port || 22,
        User: h.User, AuthMethod: h.AuthMethod, AgentInvisible: h.AgentInvisible, Group: group || undefined,
      })
      if (group) setGroupExpanded(s => ({ ...s, [group]: true }))
      refreshHosts()
    } catch (e) {
      setError(formatError(e))
    }
  }

  const handleFolderDropReorder = async (dragged: string, target: string) => {
    const order = folders.filter(f => f !== dragged)
    const idx = order.indexOf(target)
    order.splice(idx < 0 ? order.length : idx, 0, dragged)
    setFolders(order)
    try {
      await sshApi.folderReorder(client, { Names: order })
    } catch (e) {
      setError(formatError(e))
    }
    refreshHosts()
  }

  const handleDrop = (target: string) => (e: DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setDropTarget(null)
    const d = dragRef.current
    dragRef.current = null
    if (!d) return
    if (d.kind === 'host') {
      handleMoveHost(d.id, target)
    } else if (target !== '' && d.name !== target) {
      handleFolderDropReorder(d.name, target)
    }
  }

  const dropTargetProps = (target: string, accept: 'host' | 'both') => ({
    onDragOver: (e: DragEvent) => {
      const d = dragRef.current
      if (!d) return
      if (accept === 'host' && d.kind !== 'host') return
      if (d.kind === 'folder' && d.name === target) return
      e.preventDefault()
      e.stopPropagation()
      e.dataTransfer.dropEffect = 'move'
      setDropTarget(target)
    },
    onDragLeave: () => setDropTarget(t => (t === target ? null : t)),
    onDrop: handleDrop(target),
  })

  const openHostDialog = (h?: SshHostView, presetGroup?: string) => {
    setHostDialogOpen(true)
    if (h) {
      setEditingHost(h)
      setHName(h.Name); setHAddr(h.Host); setHPort(h.Port || 22)
      setHUser(h.User); setHAuth(h.AuthMethod as 'password' | 'key')
      setHGroup(h.Group || ''); setHAgentInvisible(h.AgentInvisible); setHPass(''); setHKeyPath(''); setHKeyData('')
    } else {
      setEditingHost(null)
      setHName(''); setHAddr(''); setHPort(22); setHUser('')
      setHAuth('password'); setHGroup(presetGroup ?? ''); setHAgentInvisible(false); setHPass(''); setHKeyPath(''); setHKeyData('')
    }
  }

  const handleHostSubmit = async () => {
    if (!hName.trim()) { setError(t('settings.ssh.error.nameRequired')); return }
    if (!hAddr.trim()) { setError(t('settings.ssh.error.hostRequired')); return }
    if (!hUser.trim()) { setError(t('settings.ssh.error.userRequired')); return }
    try {
      if (editingHost) {
        await sshApi.hostUpdate(client, {
          Id: editingHost.Id, Name: hName, Host: hAddr, Port: hPort,
          User: hUser, AuthMethod: hAuth, AgentInvisible: hAgentInvisible, Group: hGroup || undefined,
          Password: hPass || undefined, KeyPath: hKeyPath || undefined, KeyData: hKeyData || undefined,
        })
      } else {
        await sshApi.hostCreate(client, {
          Name: hName, Host: hAddr, Port: hPort, User: hUser, AuthMethod: hAuth,
          AgentInvisible: hAgentInvisible, Group: hGroup || undefined,
          Password: hPass || undefined, KeyPath: hKeyPath || undefined, KeyData: hKeyData || undefined,
        })
      }
      setHostDialogOpen(false)
      refreshHosts()
    } catch (e) {
      setError(formatError(e))
    }
  }

  const renderHostRow = (h: SshHostView) => {
    return (
      <div
        key={h.Id}
        className="ssh-host-item"
        draggable
        onDragStart={(e) => {
          dragRef.current = { kind: 'host', id: h.Id }
          e.dataTransfer.effectAllowed = 'move'
          e.dataTransfer.setData('text/plain', h.Name)
        }}
        onDragEnd={() => { dragRef.current = null; setDropTarget(null) }}
        onContextMenu={(e) => { e.preventDefault(); e.stopPropagation(); setCtxMenu({ type: 'host', hostId: h.Id, x: e.clientX, y: e.clientY }) }}
        onDoubleClick={() => handleOpenShell(h.Id)}
      >
        <span className="ssh-tree-icon">{getEmptyFileIcon()}</span>
        <span className="ssh-host-item-name">{h.Name}</span>
        {h.AgentInvisible && (
          <span className="ssh-host-item-hidden" title={t('settings.ssh.agentInvisible')}>
            <EyeOff size={12} />
          </span>
        )}
        <span className="ssh-host-item-addr">{h.User || 'root'}@{h.Host}</span>
        <span className="ssh-host-item-actions">
          <button className="ssh-host-btn primary" title={t('settings.ssh.openShell')} onClick={() => handleOpenShell(h.Id)}>
            <Terminal size={13} />
          </button>
          <button className="ssh-host-btn" title={t('common.edit')} onClick={() => openHostDialog(h)}>
            <Edit2 size={13} />
          </button>
          <button className="ssh-host-btn danger" title={t('common.delete')} onClick={() => handleRemoveHost(h.Id)}>
            <Trash2 size={13} />
          </button>
        </span>
      </div>
    )
  }

  return (
    <div className="ssh-manager-panel">
      <div className="ssh-manager-header">
        <div className="ssh-manager-title">
          <Server size={14} />
          {t('settings.ssh.title')}
        </div>
        <div className="ssh-manager-actions">
          <button className="ssh-manager-action" onClick={() => openHostDialog()} title={t('settings.ssh.addHost')}><Plus size={14} /></button>
          <button className="ssh-manager-action" onClick={refreshHosts} title={t('settings.ssh.refresh')} disabled={loadingHosts}>
            <RefreshCw size={14} className={loadingHosts ? 'ssh-spin' : ''} />
          </button>
        </div>
      </div>

      {error && (
        <div className="ssh-error-banner">
          <span>{error}</span>
          <button className="ssh-error-dismiss" onClick={() => setError(null)}><X size={12} /></button>
        </div>
      )}

      <div className="ssh-tree-container">
        {/* root node */}
        <div
          className={`ssh-tree-root ${dropTarget === '' ? 'ssh-drop-target' : ''}`}
          onClick={() => setRootExpanded(v => !v)}
          onContextMenu={(e) => { e.preventDefault(); e.stopPropagation(); setCtxMenu({ type: 'root', x: e.clientX, y: e.clientY }) }}
          {...dropTargetProps('', 'host')}
        >
          <span className="ssh-tree-chevron">
            {rootExpanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
          </span>
          <span className="ssh-tree-icon">{getFolderIcon(rootExpanded)}</span>
          <span className="ssh-tree-root-label">{t('settings.ssh.connections')}</span>
          <span className="ssh-tree-root-count">{hosts.length}</span>
        </div>

        {rootExpanded && (
          <div className="ssh-tree-children">
            {hosts.length === 0 && (
              <div className="ssh-tree-empty">{t('settings.ssh.empty')}</div>
            )}

            {/* group folders */}
            {groups.map(([group, groupHosts]) => {
              const expanded = groupExpanded[group] ?? false
              return (
                <div key={group}>
                  <div
                    className={`ssh-tree-group ${dropTarget === group ? 'ssh-drop-target' : ''}`}
                    draggable
                    onDragStart={(e) => {
                      dragRef.current = { kind: 'folder', name: group }
                      e.dataTransfer.effectAllowed = 'move'
                      e.dataTransfer.setData('text/plain', group)
                    }}
                    onDragEnd={() => { dragRef.current = null; setDropTarget(null) }}
                    onClick={() => setGroupExpanded(s => ({ ...s, [group]: !s[group] }))}
                    onContextMenu={(e) => { e.preventDefault(); e.stopPropagation(); setCtxMenu({ type: 'group', group, x: e.clientX, y: e.clientY }) }}
                    {...dropTargetProps(group, 'both')}
                  >
                    <span className="ssh-tree-chevron">
                      {expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                    </span>
                    <span className="ssh-tree-icon">{getFolderIcon(expanded)}</span>
                    <span className="ssh-tree-group-label">{group}</span>
                    <span className="ssh-tree-group-count">{groupHosts.length}</span>
                  </div>
                  {expanded && (
                    <div className="ssh-tree-children">
                      {groupHosts.map(renderHostRow)}
                    </div>
                  )}
                </div>
              )
            })}

            {/* ungrouped hosts */}
            {ungrouped.map(renderHostRow)}
          </div>
        )}
      </div>

      {/* context menu */}
      {ctxMenu && (
        <div ref={menuRef} className="ssh-context-menu" style={{ left: ctxMenu.x, top: ctxMenu.y }}>
          {ctxMenu.type === 'root' && (
            <>
              <button className="ssh-ctx-item" onClick={() => { setCtxMenu(null); openHostDialog() }}>
                <Plus size={12} /> {t('settings.ssh.addHost')}
              </button>
              <button className="ssh-ctx-item" onClick={() => { setCtxMenu(null); setFolderDialog({ mode: 'create' }); setGName('') }}>
                <Plus size={12} /> {t('settings.ssh.addGroup')}
              </button>
              <div className="ssh-ctx-sep" />
              <button className="ssh-ctx-item" onClick={() => { setCtxMenu(null); refreshHosts() }}>
                <RefreshCw size={12} /> {t('settings.ssh.refresh')}
              </button>
            </>
          )}
          {ctxMenu.type === 'group' && (
            <>
              <button className="ssh-ctx-item" onClick={() => { setCtxMenu(null); openHostDialog(undefined, ctxMenu.group) }}>
                <Plus size={12} /> {t('settings.ssh.addHost')}
              </button>
              <button className="ssh-ctx-item" onClick={() => { setCtxMenu(null); setFolderDialog({ mode: 'rename', from: ctxMenu.group }); setGName(ctxMenu.group) }}>
                <Edit2 size={12} /> {t('settings.ssh.renameGroup')}
              </button>
              <div className="ssh-ctx-sep" />
              <button className="ssh-ctx-item danger" onClick={() => { setCtxMenu(null); handleRemoveFolder(ctxMenu.group) }}>
                <Trash2 size={12} /> {t('common.delete')}
              </button>
            </>
          )}
          {ctxMenu.type === 'host' && (
            <>
              <button className="ssh-ctx-item" onClick={() => { setCtxMenu(null); handleOpenShell(ctxMenu.hostId) }}>
                <Terminal size={12} /> {t('settings.ssh.openShell')}
              </button>
              <div className="ssh-ctx-sep" />
              <button className="ssh-ctx-item" onClick={() => { setCtxMenu(null); openHostDialog(hosts.find(h => h.Id === ctxMenu.hostId)!) }}>
                <Edit2 size={12} /> {t('common.edit')}
              </button>
              <button className="ssh-ctx-item danger" onClick={() => { setCtxMenu(null); handleRemoveHost(ctxMenu.hostId) }}>
                <Trash2 size={12} /> {t('common.delete')}
              </button>
            </>
          )}
        </div>
      )}

      {/* host dialog */}
      {hostDialogOpen && (
        <div className="ssh-dialog-overlay" onClick={() => setHostDialogOpen(false)}>
          <div className="ssh-dialog" onClick={e => e.stopPropagation()}>
            <h3 className="ssh-dialog-title">{t(editingHost ? 'settings.ssh.editHostTitle' : 'settings.ssh.addHostTitle')}</h3>
            <div className="ssh-dialog-body">
              <label className="ssh-dialog-label">{t('settings.ssh.name')}</label>
              <input className="ssh-dialog-input" value={hName} onChange={e => setHName(e.target.value)} placeholder={t('settings.ssh.namePlaceholder')} />
              <label className="ssh-dialog-label">{t('settings.ssh.hostAddress')}</label>
              <input className="ssh-dialog-input" value={hAddr} onChange={e => setHAddr(e.target.value)} placeholder={t('settings.ssh.hostPlaceholder')} />
              <div className="ssh-dialog-row">
                <div>
                  <label className="ssh-dialog-label">{t('settings.ssh.port')}</label>
                  <input className="ssh-dialog-input" type="number" value={hPort} onChange={e => setHPort(Number(e.target.value))} />
                </div>
                <div>
                  <label className="ssh-dialog-label">{t('settings.ssh.user')}</label>
                  <input className="ssh-dialog-input" value={hUser} onChange={e => setHUser(e.target.value)} placeholder={t('settings.ssh.userPlaceholder')} />
                </div>
              </div>
              <label className="ssh-dialog-label">{t('settings.ssh.group')}</label>
              <input className="ssh-dialog-input" value={hGroup} onChange={e => setHGroup(e.target.value)} placeholder={t('settings.ssh.groupPlaceholder')} />
              <label className="ssh-dialog-check">
                <input type="checkbox" checked={hAgentInvisible} onChange={e => setHAgentInvisible(e.target.checked)} />
                {t('settings.ssh.agentInvisible')}
              </label>
              <span className="ssh-dialog-label">{t('settings.ssh.agentInvisibleHint')}</span>
              <label className="ssh-dialog-label">{t('settings.ssh.authMethod')}</label>
              <select className="ssh-dialog-select" value={hAuth} onChange={e => setHAuth(e.target.value as 'password' | 'key')}>
                <option value="password">{t('settings.ssh.password')}</option>
                <option value="key">{t('settings.ssh.sshKey')}</option>
              </select>
              {hAuth === 'password' ? (
                <>
                  <label className="ssh-dialog-label">{t('settings.ssh.password')}</label>
                  <input className="ssh-dialog-input" type="password" value={hPass} onChange={e => setHPass(e.target.value)} />
                </>
              ) : (
                <>
                  <label className="ssh-dialog-label">{t('settings.ssh.keyPath')}</label>
                  <div className="ssh-dialog-input-row">
                    <input className="ssh-dialog-input" value={hKeyPath} onChange={e => setHKeyPath(e.target.value)} placeholder={t('settings.ssh.keyPathPlaceholder')} />
                    <button type="button" className="ssh-btn" title={t('settings.ssh.browse')} onClick={() => setKeyPickerOpen(true)}>
                      <FolderOpen size={14} />
                    </button>
                  </div>
                  <label className="ssh-dialog-label">{t('settings.ssh.keyData')}</label>
                  <textarea className="ssh-dialog-input" value={hKeyData} onChange={e => setHKeyData(e.target.value)} rows={3} placeholder={t('settings.ssh.keyDataPlaceholder')} />
                </>
              )}
            </div>
            <div className="ssh-dialog-actions">
              <button className="ssh-btn" onClick={() => setHostDialogOpen(false)}>{t('common.cancel')}</button>
              <button className="ssh-btn ssh-btn-primary" onClick={handleHostSubmit}>{t(editingHost ? 'common.save' : 'common.create')}</button>
            </div>
          </div>
        </div>
      )}

      {/* key file picker */}
      <FilePicker
        open={keyPickerOpen}
        mode="file"
        onSelect={paths => { if (paths[0]) setHKeyPath(paths[0]); setKeyPickerOpen(false) }}
        onCancel={() => setKeyPickerOpen(false)}
      />

      {/* folder dialog — name only */}
      {folderDialog && (
        <div className="ssh-dialog-overlay" onClick={() => setFolderDialog(null)}>
          <div className="ssh-dialog" onClick={e => e.stopPropagation()}>
            <h3 className="ssh-dialog-title">
              {t(folderDialog.mode === 'rename' ? 'settings.ssh.renameGroup' : 'settings.ssh.addGroupTitle')}
            </h3>
            <div className="ssh-dialog-body">
              <label className="ssh-dialog-label">{t('settings.ssh.groupName')}</label>
              <input
                className="ssh-dialog-input"
                autoFocus
                value={gName}
                onChange={e => setGName(e.target.value)}
                onKeyDown={e => { if (e.key === 'Enter') handleFolderSubmit() }}
              />
            </div>
            <div className="ssh-dialog-actions">
              <button className="ssh-btn" onClick={() => setFolderDialog(null)}>{t('common.cancel')}</button>
              <button className="ssh-btn ssh-btn-primary" onClick={handleFolderSubmit} disabled={!gName.trim()}>
                {t(folderDialog.mode === 'rename' ? 'common.save' : 'common.create')}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
