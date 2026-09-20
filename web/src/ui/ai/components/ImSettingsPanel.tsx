import { useState, useEffect, useCallback, useRef } from 'react'
import { Loader2, MessageCircle, Plus, Pencil, Trash2, X, Bot, Link2Off } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as imAccount from '../../../gen-clients/im.account/client'
import * as imRoute from '../../../gen-clients/im.route/client'
import * as im from '../../../gen-clients/im/client'
import * as workspace from '../../../gen-clients/workspace/client'
import type { ImAccountView, ImRoute, AgentRef, ImAccountUpdateReq } from '../../../gen-clients/system/types'
import { useI18n } from '../../../i18n'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge, Button, Input, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText, Switch } from '../../settings/shadcn/ui'
import { useBrowserOverlay } from '../browserOverlay'
import './ImSettingsPanel.css'

// v1 provider list — mirrors pkg/actor/im adapter registry (telegram first).
const IM_PROVIDERS = [{ id: 'telegram' }]

const STATUS_ORDER = ['connected', 'connecting', 'disconnected', 'error'] as const

function statusKey(status: string | undefined): string {
  if (status && (STATUS_ORDER as readonly string[]).includes(status)) return status
  return 'unknown'
}

// Known providers get a localized label; unknown future ids fall back to raw.
function providerLabel(t: ReturnType<typeof useI18n>['t'], id: string): string {
  if (IM_PROVIDERS.some(p => p.id === id)) return t(`settings.im.provider.${id}` as any)
  return id
}

// Splits a comma/space separated user list into the AllowUsers array.
function parseAllowUsers(raw: string): string[] | undefined {
  const items = raw.split(/[,，\s]+/).map(s => s.trim()).filter(Boolean)
  return items.length ? items : undefined
}

export function ImSettingsPanel() {
  const { t } = useI18n()
  const [accounts, setAccounts] = useState<ImAccountView[]>([])
  const [routes, setRoutes] = useState<ImRoute[]>([])
  const [agents, setAgents] = useState<AgentRef[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [listResp, statusResp, routeResp] = await Promise.all([
        imAccount.list(client, {}),
        im.status(client, {}),
        imRoute.list(client, {}),
      ])
      // Live connection state comes from im.status; merge it onto the list view.
      const statusById = new Map((statusResp.Items || []).map(v => [v.Id, v]))
      const merged = (listResp.Items || []).map(v => {
        const s = statusById.get(v.Id)
        if (!s) return v
        return { ...v, Status: s.Status, StatusDetail: s.StatusDetail, BotUsername: s.BotUsername }
      })
      setAccounts(merged)
      setRoutes(routeResp.Items || [])
      // Agent list is best-effort: an unavailable workspace must not break the panel.
      try {
        const agentResp = await workspace.agents(client)
        setAgents(agentResp.Items || [])
      } catch {
        setAgents([])
      }
    } catch (e: any) {
      setError(e?.message || t('settings.im.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  if (loading) {
    return (
      <FeatureCard icon={<MessageCircle size={16} />} title={t('settings.im.title')} description={t('settings.im.desc')}>
        <div className="im-loading">
          <Loader2 size={16} className="im-spin" /> {t('settings.im.loading')}
        </div>
      </FeatureCard>
    )
  }

  return (
    <div className="flex flex-col gap-6">
      {error && (
        <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{error}</p>
      )}
      <AccountSection accounts={accounts} onChanged={load} onClearError={() => setError('')} />
      <MountSection accounts={accounts} routes={routes} agents={agents} onChanged={load} onClearError={() => setError('')} />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Account management
// ---------------------------------------------------------------------------

function AccountSection({ accounts, onChanged, onClearError }: {
  accounts: ImAccountView[]
  onChanged: () => Promise<void>
  onClearError: () => void
}) {
  const { t } = useI18n()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<ImAccountView | null>(null)
  const [error, setError] = useState('')
  const [togglingId, setTogglingId] = useState('')

  const handleToggle = async (acc: ImAccountView, enabled: boolean) => {
    setTogglingId(acc.Id)
    setError('')
    try {
      await imAccount.update(client, { Id: acc.Id, Enabled: enabled })
      await onChanged()
    } catch (e: any) {
      setError(e?.message || t('settings.im.saveFailed'))
    } finally {
      setTogglingId('')
    }
  }

  const handleDelete = async (id: string) => {
    if (!window.confirm(t('settings.im.deleteAccountConfirm'))) return
    onClearError()
    setError('')
    try {
      await imAccount._delete(client, { Id: id })
      await onChanged()
    } catch (e: any) {
      setError(e?.message || t('settings.im.saveFailed'))
    }
  }

  return (
    <FeatureCard
      icon={<MessageCircle size={16} />}
      title={t('settings.im.accountsTitle')}
      description={t('settings.im.accountsDesc')}
      action={
        <Button variant="outline" size="sm" onClick={() => { setEditing(null); setDialogOpen(true) }} data-guide-id="settings/im/add">
          <Plus />
          {t('settings.im.addAccount')}
        </Button>
      }
    >
      {error && <p className="text-sm text-destructive">{error}</p>}

      {accounts.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('settings.im.accountEmpty')}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {accounts.map(acc => {
            const sKey = statusKey(acc.Status)
            return (
              <div
                key={acc.Id}
                className="flex items-center gap-2 rounded-lg border border-border px-3 py-2"
                data-guide-id={`settings/im/account-${acc.Id}`}
              >
                <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
                  <span className="flex items-center gap-2 text-sm font-medium">
                    {acc.Name}
                    <Badge variant="secondary" className={`im-status-badge im-status-${sKey}`}>
                      {t(`settings.im.status.${sKey}` as any)}
                    </Badge>
                    {!acc.Enabled && (
                      <Badge variant="outline" className="text-xs">{t('settings.im.disabled')}</Badge>
                    )}
                  </span>
                  <span className="text-xs text-muted-foreground" title={acc.StatusDetail || undefined}>
                    {providerLabel(t, acc.Provider)} · {acc.HasToken ? t('settings.im.keySet') : t('settings.im.noKey')}
                    {acc.BotUsername ? ` · @${acc.BotUsername}` : ''}
                  </span>
                </div>
                <Switch
                  checked={acc.Enabled}
                  onCheckedChange={(v) => { void handleToggle(acc, v) }}
                  disabled={togglingId === acc.Id}
                  aria-label={t('settings.im.enabled')}
                  data-guide-id={`settings/im/account-${acc.Id}/enabled`}
                />
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => { setEditing(acc); setDialogOpen(true) }}
                  title={t('settings.im.editAccount')}
                  data-guide-id={`settings/im/account-${acc.Id}/edit`}
                >
                  <Pencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => { void handleDelete(acc.Id) }}
                  title={t('settings.im.deleteAccount')}
                  data-guide-id={`settings/im/account-${acc.Id}/delete`}
                >
                  <Trash2 />
                </Button>
              </div>
            )
          })}
        </div>
      )}

      {dialogOpen && (
        <AccountDialog
          editing={editing}
          onClose={() => setDialogOpen(false)}
          onSaved={() => { setDialogOpen(false); void onChanged() }}
        />
      )}
    </FeatureCard>
  )
}

interface AccountDialogProps {
  editing: ImAccountView | null
  onClose: () => void
  onSaved: () => void
}

function AccountDialog({ editing, onClose, onSaved }: AccountDialogProps) {
  useBrowserOverlay(true)
  const { t } = useI18n()
  const [name, setName] = useState(editing?.Name || '')
  const [provider, setProvider] = useState(editing?.Provider || IM_PROVIDERS[0]?.id || '')
  const [token, setToken] = useState('')
  const [allowUsers, setAllowUsers] = useState((editing?.AllowUsers || []).join(', '))
  const [apiBase, setApiBase] = useState(editing?.ApiBase || '')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const handleSave = async () => {
    setSaving(true)
    setError('')
    try {
      if (editing) {
        // Redacted edit flow: an empty token keeps the stored secret.
        const req: ImAccountUpdateReq = { Id: editing.Id }
        if (name) req.Name = name
        if (token) req.Token = token
        const users = parseAllowUsers(allowUsers)
        if (users) req.AllowUsers = users
        if (apiBase) req.ApiBase = apiBase
        await imAccount.update(client, req)
      } else {
        if (!token) {
          setError(t('settings.im.tokenRequired'))
          setSaving(false)
          return
        }
        const users = parseAllowUsers(allowUsers)
        await imAccount.create(client, {
          Name: name,
          Provider: provider,
          Token: token,
          ...(users ? { AllowUsers: users } : {}),
          ...(apiBase ? { ApiBase: apiBase } : {}),
        })
      }
      onSaved()
    } catch (e: any) {
      setError(e?.message || t('settings.im.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  const canSave = editing ? Boolean(name) : Boolean(name && provider && token)

  return (
    <div className="im-dialog-overlay" onClick={onClose}>
      <div className="im-dialog" onClick={e => e.stopPropagation()}>
        <div className="im-dialog-header">
          <h4 className="text-sm font-medium">
            {editing ? t('settings.im.editAccount') : t('settings.im.addAccount')}
          </h4>
          <button type="button" className="im-dialog-close" onClick={onClose} title={t('settings.im.cancel')}>
            <X size={16} />
          </button>
        </div>

        {error && <p className="text-sm text-destructive">{error}</p>}

        <div className="im-dialog-body">
          <label className="im-field">
            <span>{t('settings.im.accountName')}</span>
            <Input
              type="text"
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder={t('settings.im.accountNamePlaceholder')}
              data-guide-id="settings/im/account-name"
            />
          </label>

          <label className="im-field">
            <span>{t('settings.im.provider')}</span>
            {editing ? (
              <Input type="text" value={providerLabel(t, provider)} disabled />
            ) : (
              <SelectRoot
                value={provider}
                onValueChange={(v) => setProvider(v as string)}
                items={IM_PROVIDERS.map(p => ({ value: p.id, label: t(`settings.im.provider.${p.id}` as any) }))}
              >
                <SelectTrigger data-guide-id="settings/im/provider"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {IM_PROVIDERS.map(p => (
                    <SelectItem key={p.id} value={p.id} data-guide-id={`settings/im/provider/${p.id}`}>
                      <SelectItemText>{t(`settings.im.provider.${p.id}` as any)}</SelectItemText>
                    </SelectItem>
                  ))}
                </SelectContent>
              </SelectRoot>
            )}
          </label>

          <label className="im-field">
            <span>{t('settings.im.token')}</span>
            <Input
              type="password"
              value={token}
              onChange={e => setToken(e.target.value)}
              placeholder={editing ? t('settings.im.tokenEditPlaceholder') : t('settings.im.tokenPlaceholder')}
              data-guide-id="settings/im/token"
            />
          </label>

          <label className="im-field">
            <span>{t('settings.im.allowUsers')}</span>
            <Input
              type="text"
              value={allowUsers}
              onChange={e => setAllowUsers(e.target.value)}
              placeholder={t('settings.im.allowUsersPlaceholder')}
              data-guide-id="settings/im/allow-users"
            />
            <span className="im-field-hint">{t('settings.im.allowUsersHint')}</span>
          </label>

          <label className="im-field">
            <span>
              {t('settings.im.apiBase')} <span className="im-field-hint-inline">{t('settings.im.apiBaseOptional')}</span>
            </span>
            <Input
              type="text"
              value={apiBase}
              onChange={e => setApiBase(e.target.value)}
              placeholder={t('settings.im.apiBasePlaceholder')}
              data-guide-id="settings/im/api-base"
            />
          </label>
        </div>

        <div className="im-dialog-footer">
          <Button variant="outline" size="sm" onClick={onClose} disabled={saving}>
            {t('settings.im.cancel')}
          </Button>
          <Button size="sm" onClick={() => void handleSave()} disabled={saving || !canSave} data-guide-id="settings/im/save-account">
            {saving && <Loader2 size={14} className="im-spin" />}
            {saving ? t('settings.im.saving') : t('settings.im.save')}
          </Button>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Account → agent mounts
// ---------------------------------------------------------------------------

// MountedAt is persisted as a UTC RFC3339 stamp; display converts it to the
// user's local timezone (project constraint: UTC storage, local display).
function formatMountedAt(value: string | undefined): string {
  if (!value) return ''
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString()
}

function MountSection({ accounts, routes, agents, onChanged, onClearError }: {
  accounts: ImAccountView[]
  routes: ImRoute[]
  agents: AgentRef[]
  onChanged: () => Promise<void>
  onClearError: () => void
}) {
  const { t } = useI18n()
  const [busyKey, setBusyKey] = useState('')
  const [toast, setToast] = useState<string | null>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const showToast = useCallback((msg: string) => {
    setToast(msg)
    if (toastTimer.current) clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToast(null), 4000)
  }, [])

  useEffect(() => () => { if (toastTimer.current) clearTimeout(toastTimer.current) }, [])

  const agentName = (actorId: string) => agents.find(a => a.ActorId === actorId)?.DisplayName || actorId

  // Each account carries at most one mount; map account id → its route.
  const routeByAccount = new Map(routes.map(r => [r.AccountId, r]))

  // Agents already occupied by another account's mount are hidden from the
  // selectors. This is display-only filtering — the exclusive-mount rule is
  // enforced by im.route.set on the backend; its rejection surfaces as a
  // toast below.
  const occupiedAgents = new Set(routes.map(r => r.AgentActorId))

  const handleMount = async (accountId: string, agentActorId: string) => {
    setBusyKey(`mount-${accountId}`)
    onClearError()
    try {
      await imRoute.set(client, { AccountId: accountId, AgentActorId: agentActorId })
      await onChanged()
    } catch (e: any) {
      showToast(`${t('settings.im.mountFailed')}: ${e?.message || t('settings.im.saveFailed')}`)
    } finally {
      setBusyKey('')
    }
  }

  const handleUnmount = async (key: string, routeId: string) => {
    if (!window.confirm(t('settings.im.unmountConfirm'))) return
    setBusyKey(key)
    onClearError()
    try {
      await imRoute._delete(client, { Id: routeId })
      await onChanged()
    } catch (e: any) {
      showToast(`${t('settings.im.unmountFailed')}: ${e?.message || t('settings.im.saveFailed')}`)
    } finally {
      setBusyKey('')
    }
  }

  // Mounts whose account has been deleted keep an unmount control so the
  // occupied agent can still be released.
  const orphanRoutes = routes.filter(r => !accounts.some(a => a.Id === r.AccountId))

  const mountedDetail = (route: ImRoute) => (
    <span className="text-xs text-muted-foreground" title={route.AgentActorId}>
      {agentName(route.AgentActorId)} · {route.AgentActorId} · {t('settings.im.mountedAt', { time: formatMountedAt(route.MountedAt) })}
    </span>
  )

  return (
    <FeatureCard
      icon={<Bot size={16} />}
      title={t('settings.im.routesTitle')}
      description={t('settings.im.routesDesc')}
    >
      {toast && <div className="im-toast" role="status">{toast}</div>}

      {accounts.length === 0 && orphanRoutes.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('settings.im.accountEmpty')}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {accounts.map(acc => {
            const route = routeByAccount.get(acc.Id)
            return (
              <div
                key={acc.Id}
                className="flex items-center gap-2 rounded-lg border border-border px-3 py-2"
                data-guide-id={`settings/im/mount-${acc.Id}`}
              >
                <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
                  <span className="truncate text-sm font-medium">{acc.Name}</span>
                  {route ? mountedDetail(route) : (
                    <span className="text-xs text-muted-foreground">{t('settings.im.notMounted')}</span>
                  )}
                </div>
                {route ? (
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => { void handleUnmount(`mount-${acc.Id}`, route.Id) }}
                    title={t('settings.im.unmount')}
                    disabled={busyKey === `mount-${acc.Id}`}
                    data-guide-id={`settings/im/mount-${acc.Id}/unmount`}
                  >
                    <Link2Off />
                  </Button>
                ) : (
                  <AgentMountSelect
                    account={acc}
                    agents={agents}
                    occupiedAgents={occupiedAgents}
                    disabled={busyKey === `mount-${acc.Id}`}
                    onMount={handleMount}
                  />
                )}
              </div>
            )
          })}
          {orphanRoutes.map(route => (
            <div
              key={`orphan-${route.Id}`}
              className="flex items-center gap-2 rounded-lg border border-border px-3 py-2"
              data-guide-id={`settings/im/mount-orphan-${route.Id}`}
            >
              <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
                <span className="truncate text-sm font-medium">{t('settings.im.accountMissing')}</span>
                {mountedDetail(route)}
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => { void handleUnmount(`orphan-${route.Id}`, route.Id) }}
                title={t('settings.im.unmount')}
                disabled={busyKey === `orphan-${route.Id}`}
                data-guide-id={`settings/im/mount-orphan-${route.Id}/unmount`}
              >
                <Link2Off />
              </Button>
            </div>
          ))}
        </div>
      )}

      <div className="im-hints">
        <span>{t('settings.im.unboundHint')}</span>
      </div>
    </FeatureCard>
  )
}

// Per-account agent picker for mounting. The dropdown popup is an HTML layer
// that may float above the embedded native browser window, so it registers
// with useBrowserOverlay(open) (project constraint) — no z-index workarounds.
function AgentMountSelect({ account, agents, occupiedAgents, disabled, onMount }: {
  account: ImAccountView
  agents: AgentRef[]
  occupiedAgents: Set<string>
  disabled: boolean
  onMount: (accountId: string, agentActorId: string) => Promise<void>
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  useBrowserOverlay(open)

  const candidates = agents.filter(a => !occupiedAgents.has(a.ActorId))

  if (candidates.length === 0) {
    return (
      <span className="text-xs text-muted-foreground">
        {agents.length === 0 ? t('settings.im.agentsEmpty') : t('settings.im.allAgentsOccupied')}
      </span>
    )
  }

  return (
    <SelectRoot
      open={open}
      onOpenChange={(next) => setOpen(next)}
      items={candidates.map(a => ({ value: a.ActorId, label: a.DisplayName }))}
      onValueChange={(v) => { void onMount(account.Id, v as string) }}
    >
      <SelectTrigger size="sm" disabled={disabled} data-guide-id={`settings/im/mount-${account.Id}/agent`}>
        <SelectValue placeholder={t('settings.im.mountAgent')} />
      </SelectTrigger>
      <SelectContent>
        {candidates.map(a => (
          <SelectItem
            key={a.ActorId}
            value={a.ActorId}
            data-guide-id={`settings/im/mount-${account.Id}/agent/${a.ActorId}`}
          >
            <SelectItemText>{a.DisplayName}</SelectItemText>
          </SelectItem>
        ))}
      </SelectContent>
    </SelectRoot>
  )
}
