import { useCallback, useEffect, useLayoutEffect, useState } from 'react'
import {
  Check,
  HardDrive,
  Pencil,
  Plus,
  RefreshCw,
  Server,
  Trash2,
} from 'lucide-react'
import { useI18n } from '../../../i18n'
import { isWails } from '../../../application/runtime'
import {
  deleteConnection,
  listConnections,
  probeConnection,
  saveConnection,
  type RemoteConnectionView,
} from '../../../application/remote-connections'
import { switchInstance, useActiveInstanceId } from '../../../application/instance'
import { client } from '../../../application/generated-client'
import * as toastApi from '../../../gen-clients/toast/client'
import { useBrowserOverlay } from '../browserOverlay'
import { Modal } from '../../../ui/components/Modal'
import './ConnectionSwitcher.css'

/** Bottom-left connection switcher (Wails desktop only).
 *
 *  Single-window model: the shell stays resident and switching a connection
 *  rebuilds the "instance" (gateway client + derived state) in place — see
 *  [[instance-swap-refactor]]. The action calls switchInstance(target); the host
 *  probes the target and refuses self-connections, and any refusal (or a failed
 *  re-auth) surfaces as an error toast while the current instance stays put.
 *  The active check reads useActiveInstanceId(), so the UI follows committed
 *  swaps (host broadcasts and ?conn= windows) without a window reload. */
export function ConnectionSwitcher() {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [menuRef, setMenuRef] = useState<HTMLDivElement | null>(null)
  const [popoverStyle, setPopoverStyle] = useState<{ left: number; bottom: number } | null>(null)
  const [connections, setConnections] = useState<RemoteConnectionView[]>([])
  const active = useActiveInstanceId()
  const [editing, setEditing] = useState<RemoteConnectionView | null>(null)
  const [creating, setCreating] = useState(false)

  useBrowserOverlay(open)

  // Refresh the saved-connection list whenever the popover opens. The active
  // instance is NOT read here — it comes from useActiveInstanceId() (the host
  // broadcast / committed swap), so it stays correct without a window reload.
  const refresh = useCallback(async () => {
    try {
      const conns = await listConnections()
      setConnections(conns ?? [])
    } catch (err) {
      console.warn('[ConnectionSwitcher] refresh failed:', err)
    }
  }, [])

  // Load the saved-connection list on mount too, not only when the popover
  // opens: this component lives inside the instance-keyed subtree, so every
  // swap remounts it. Without the mount fetch the trigger icon falls back to
  // the local drive even when the active instance is a remote connection.
  useEffect(() => {
    void refresh()
  }, [refresh])
  useEffect(() => {
    if (!open) return
    void refresh()
  }, [open, refresh])

  const updatePopoverPosition = useCallback(() => {
    if (!menuRef) return
    const rect = menuRef.getBoundingClientRect()
    setPopoverStyle({ left: rect.left, bottom: window.innerHeight - rect.top + 8 })
  }, [menuRef])

  useLayoutEffect(() => {
    if (!open) return
    updatePopoverPosition()
    const handleResize = () => updatePopoverPosition()
    window.addEventListener('resize', handleResize)
    return () => window.removeEventListener('resize', handleResize)
  }, [open, updatePopoverPosition])

  useEffect(() => {
    if (!open) return
    const handlePointerDown = (event: MouseEvent) => {
      if (menuRef && !menuRef.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handlePointerDown)
    return () => document.removeEventListener('mousedown', handlePointerDown)
  }, [open, menuRef])

  const handleSwitch = useCallback(async (target: string) => {
    setOpen(false)
    try {
      await switchInstance(target)
    } catch (err) {
      // The host refused the switch (unreachable gateway / self-connection) or
      // the swap rolled back: the window stays on the current instance. Surface
      // the failure as an error toast rather than silently swallowing it.
      console.warn('[ConnectionSwitcher] switch failed:', err)
      void toastApi
        .show(client, {
          Title: t('connections.title'),
          Body: errorText(err),
          Kind: 'error',
        })
        .catch((toastErr) => {
          console.warn('[ConnectionSwitcher] failed to show switch error toast:', toastErr)
        })
    }
  }, [t])

  const handleSaved = useCallback(() => {
    setEditing(null)
    setCreating(false)
    void refresh()
  }, [refresh])

  const handleDeleted = useCallback(() => {
    setEditing(null)
    void refresh()
  }, [refresh])

  if (!isWails()) return null

  const activeConn = connections.find(c => c.id === active)

  return (
    <div className="ai-sidebar-connection-menu" ref={setMenuRef}>
      <button
        type="button"
        className="ai-sidebar-footer-btn ai-sidebar-connection-btn"
        data-testid="connection-switcher-button"
        title={activeConn ? `${activeConn.name} (${activeConn.host}:${activeConn.port})` : t('connections.local')}
        aria-label={t('connections.title')}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen(value => !value)}
      >
        {activeConn ? <Server size={14} /> : <HardDrive size={14} />}
      </button>
      {open && popoverStyle && (
        <div
          role="menu"
          className="ai-sidebar-account-popover ai-sidebar-connection-popover"
          style={{ position: 'fixed', left: popoverStyle.left, bottom: popoverStyle.bottom }}
          data-testid="connection-switcher-menu"
        >
          <div className="ai-sidebar-connection-label">{t('connections.title')}</div>
          <button
            type="button"
            role="menuitem"
            className={`ai-sidebar-connection-item${active === 'local' ? ' active' : ''}`}
            data-testid="connection-item-local"
            onClick={() => { if (active !== 'local') void handleSwitch('local') }}
          >
            <HardDrive size={14} />
            <span className="ai-sidebar-connection-item-text">
              <span className="ai-sidebar-connection-item-name">{t('connections.local')}</span>
            </span>
            {(active === 'local') && <Check size={14} className="ai-sidebar-connection-check" />}
          </button>
          {connections.map(conn => (
            <button
              key={conn.id}
              type="button"
              role="menuitem"
              className={`ai-sidebar-connection-item${active === conn.id ? ' active' : ''}`}
              data-testid={`connection-item-${conn.id}`}
              onClick={() => { if (active !== conn.id) void handleSwitch(conn.id) }}
            >
              <Server size={14} />
              <span className="ai-sidebar-connection-item-text">
                <span className="ai-sidebar-connection-item-name">{conn.name}</span>
                <span className="ai-sidebar-connection-item-host">{conn.host}:{conn.port}</span>
              </span>
              <Pencil
                size={13}
                className="ai-sidebar-connection-edit"
                role="button"
                aria-label={t('connections.edit')}
                onClick={e => { e.stopPropagation(); setEditing(conn) }}
              />
              {active === conn.id && <Check size={14} className="ai-sidebar-connection-check" />}
            </button>
          ))}
          <div className="ai-sidebar-account-popover-separator" />
          <button
            type="button"
            role="menuitem"
            className="ai-sidebar-connection-item"
            data-testid="connection-add"
            onClick={() => setCreating(true)}
          >
            <Plus size={14} />
            <span className="ai-sidebar-connection-item-text">
              <span className="ai-sidebar-connection-item-name">{t('connections.add')}</span>
            </span>
          </button>
        </div>
      )}
      <ConnectionModal
        open={creating || editing !== null}
        connection={editing}
        onClose={() => { setCreating(false); setEditing(null) }}
        onSaved={handleSaved}
        onDeleted={handleDeleted}
      />
    </div>
  )
}


interface ProbeState {
  kind: 'idle' | 'busy' | 'reachable' | 'unreachable' | 'isSelf' | 'legacy' | 'error'
  message?: string
}

/** Add / edit connection dialog with a pre-save probe against /instance/info. */
function ConnectionModal({
  open,
  connection,
  onClose,
  onSaved,
  onDeleted,
}: {
  open: boolean
  connection: RemoteConnectionView | null
  onClose: () => void
  onSaved: () => void
  onDeleted: () => void
}) {
  const { t } = useI18n()
  const [name, setName] = useState('')
  const [host, setHost] = useState('')
  const [port, setPort] = useState(18080)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [probe, setProbe] = useState<ProbeState>({ kind: 'idle' })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setName(connection?.name ?? '')
    setHost(connection?.host ?? '')
    setPort(connection?.port ?? 18080)
    setUsername(connection?.username ?? '')
    setPassword('')
    setProbe({ kind: 'idle' })
    setError('')
  }, [open, connection])

  const handleProbe = useCallback(async () => {
    if (!host.trim()) return
    setProbe({ kind: 'busy' })
    try {
      const result = await probeConnection(host.trim(), port)
      if (result.isSelf) {
        setProbe({ kind: 'isSelf' })
      } else if (result.instanceId) {
        setProbe({ kind: 'reachable', message: result.instanceId })
      } else if (result.reachable) {
        setProbe({ kind: 'legacy', message: result.message })
      } else {
        setProbe({ kind: 'unreachable', message: result.message })
      }
    } catch (err) {
      setProbe({ kind: 'error', message: String((err as Error)?.message ?? err) })
    }
  }, [host, port])

  const handleSave = useCallback(async () => {
    const trimmedHost = host.trim()
    if (!trimmedHost || !name.trim()) {
      setError(t('connections.validationRequired'))
      return
    }
    if (probe.kind === 'isSelf') {
      setError(t('connections.isSelf'))
      return
    }
    setSaving(true)
    setError('')
    try {
      await saveConnection(
        connection
          ? { id: connection.id, name: name.trim(), host: trimmedHost, port, username: username.trim(), instanceId: probe.kind === 'reachable' ? probe.message : connection.instanceId }
          : { name: name.trim(), host: trimmedHost, port, username: username.trim(), instanceId: probe.kind === 'reachable' ? probe.message : '' },
        password,
      )
      onSaved()
    } catch (err) {
      setError(String((err as Error)?.message ?? err))
    } finally {
      setSaving(false)
    }
  }, [connection, host, name, password, port, probe, t, username, onSaved])

  const handleDelete = useCallback(async () => {
    if (!connection) return
    if (!window.confirm(t('connections.deleteConfirm', { name: connection.name }))) return
    try {
      await deleteConnection(connection.id)
      onDeleted()
    } catch (err) {
      setError(String((err as Error)?.message ?? err))
    }
  }, [connection, onDeleted, t])

  const probeTag = (() => {
    switch (probe.kind) {
      case 'busy': return <span className="ai-sidebar-connection-probe busy"><RefreshCw size={12} className="spin" />{t('connections.testing')}</span>
      case 'reachable': return <span className="ai-sidebar-connection-probe ok">{t('connections.reachable')}</span>
      case 'legacy': return <span className="ai-sidebar-connection-probe warn">{t('connections.legacyRemote')}</span>
      case 'unreachable': return <span className="ai-sidebar-connection-probe bad" title={probe.message}>{t('connections.unreachable')}</span>
      case 'isSelf': return <span className="ai-sidebar-connection-probe bad">{t('connections.isSelf')}</span>
      case 'error': return <span className="ai-sidebar-connection-probe bad" title={probe.message}>{t('connections.probeFailed')}</span>
      default: return null
    }
  })()

  return (
    <Modal
      open={open}
      title={connection ? t('connections.edit') : t('connections.add')}
      onClose={onClose}
      size="sm"
      footer={
        <div className="ai-sidebar-connection-modal-footer">
          {connection && (
            <button type="button" className="modal-action modal-action--secondary ai-sidebar-connection-delete" onClick={() => { void handleDelete() }} data-testid="connection-delete">
              <Trash2 size={14} />
              {t('connections.delete')}
            </button>
          )}
          <div className="ai-sidebar-connection-modal-footer-spacer" />
          <button type="button" className="modal-action modal-action--secondary" onClick={onClose}>{t('connections.cancel')}</button>
          <button
            type="button"
            className="modal-action modal-action--primary"
            disabled={saving || probe.kind === 'isSelf'}
            onClick={() => { void handleSave() }}
            data-testid="connection-save"
          >
            {t('connections.save')}
          </button>
        </div>
      }
    >
      <div className="ai-sidebar-connection-form">
        <label className="ai-sidebar-connection-field">
          <span>{t('connections.name')}</span>
          <input value={name} onChange={e => setName(e.target.value)} data-testid="connection-field-name" />
        </label>
        <div className="ai-sidebar-connection-field-row">
          <label className="ai-sidebar-connection-field grow">
            <span>{t('connections.host')}</span>
            <input value={host} onChange={e => { setHost(e.target.value); setProbe({ kind: 'idle' }) }} placeholder="192.168.1.5" data-testid="connection-field-host" />
          </label>
          <label className="ai-sidebar-connection-field port">
            <span>{t('connections.port')}</span>
            <input
              type="number"
              value={port}
              min={1}
              max={65535}
              onChange={e => { setPort(Number(e.target.value) || 0); setProbe({ kind: 'idle' }) }}
              data-testid="connection-field-port"
            />
          </label>
        </div>
        <div className="ai-sidebar-connection-field-row">
          <button type="button" className="btn-ghost ai-sidebar-connection-probe-btn" onClick={() => { void handleProbe() }} disabled={!host.trim() || probe.kind === 'busy'} data-testid="connection-probe">
            <RefreshCw size={12} className={probe.kind === 'busy' ? 'spin' : ''} />
            {probe.kind === 'busy' ? t('connections.testing') : t('connections.test')}
          </button>
          {probeTag}
        </div>
        <label className="ai-sidebar-connection-field">
          <span>{t('connections.username')}</span>
          <input value={username} onChange={e => setUsername(e.target.value)} autoComplete="off" data-testid="connection-field-username" />
        </label>
        <label className="ai-sidebar-connection-field">
          <span>{t('connections.password')}</span>
          <input
            type="password"
            value={password}
            onChange={e => setPassword(e.target.value)}
            placeholder={connection ? t('connections.passwordKeep') : ''}
            autoComplete="new-password"
            data-testid="connection-field-password"
          />
        </label>
        {error && <div className="ai-sidebar-connection-error" role="alert" data-testid="connection-error">{error}</div>}
      </div>
    </Modal>
  )
}

/** Best-effort human-readable text for a thrown switch error. */
function errorText(err: unknown): string {
  if (err instanceof Error && err.message) return err.message
  return String(err ?? '')
}
