import { useCallback, useEffect, useMemo, useState } from 'react'
import { Plus, Pencil, Trash2, ExternalLink, Check } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as dbApi from '../../../gen-clients/dbmanager/client'
import * as sshApi from '../../../gen-clients/sshmanager/client'
import type { DbProfileView } from '../../../gen-types/dbmanager'
import type { SshHostView } from '../../../gen-clients/system/types'
import { useI18n } from '../../../i18n'
import { Modal } from '../../components/Modal'
import { FeatureCard } from '../../settings/shadcn/composites'
import {
  Button,
  Field,
  FieldGroup,
  FieldLabel,
  Input,
  SelectContent,
  SelectItem,
  SelectItemText,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from '../../settings/shadcn/ui'

/**
 * Backend choices surfaced in the form (must match persist backend identifiers
 * the dbclient actor accepts; see schemas/dbmanager._5776.spore:26).
 */
const BACKEND_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'mysql', label: 'MySQL' },
  { value: 'postgres', label: 'PostgreSQL' },
  { value: 'mongo', label: 'MongoDB' },
  { value: 'redis', label: 'Redis' },
  { value: 'etcd', label: 'etcd' },
  { value: 'webdav', label: 'WebDAV' },
  { value: 'oss', label: 'OSS (S3)' },
]

export interface DbManagerSettingsProps {
  /**
   * Called when the user clicks "Open client" on a profile. The parent
   * (typically AIShellLayout) wires this to handleOpenDbSession which opens
   * the right-panel tab. Kept as a callback so this component stays
   * unaware of right-tabs internals (single responsibility: list + CRUD).
   *
   * The callback receives the profile so the parent can route oss/webdav
   * profiles to ObjectStorageSessionView (handleOpenObjectStorage) and the
   * rest to DbSessionView (handleOpenDbSession).
   */
  onOpenClient?: (profile: DbProfileView) => void
}

function isObjectStorageBackend(backend: string): boolean {
  return backend === 'oss' || backend === 'webdav'
}

interface DbEditForm {
  id: string
  name: string
  backend: string
  endpoint: string
  database: string
  tunnel: string
  username: string
  accessKey: string
  password: string
  secret: string
  token: string
}

function emptyForm(): DbEditForm {
  return {
    id: '',
    name: '',
    backend: 'postgres',
    endpoint: '',
    database: '',
    tunnel: '',
    username: '',
    accessKey: '',
    password: '',
    secret: '',
    token: '',
  }
}

function formatError(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e)
}

export function DbManagerSettings({ onOpenClient }: DbManagerSettingsProps) {
  const { t } = useI18n()
  const [profiles, setProfiles] = useState<DbProfileView[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<DbEditForm | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null)
  const [pendingDelete, setPendingDelete] = useState<DbProfileView | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const resp = await dbApi.profileList(client, {})
      setProfiles(resp.Items ?? [])
    } catch (e) {
      setError(formatError(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void refresh() }, [refresh])

  const handleEdit = useCallback(async (p: DbProfileView) => {
    setBusy(`load-${p.Id}`)
    setNotice(null)
    try {
      const resp = await dbApi.profileGet(client, { Id: p.Id })
      const v = resp.Profile
      setEditing({
        id: v.Id,
        name: v.Name,
        backend: v.Backend,
        endpoint: v.Endpoint ?? '',
        database: v.Database ?? '',
        tunnel: v.TunnelRef ?? '',
        username: v.Username ?? '',
        accessKey: v.AccessKey ?? '',
        password: '',
        secret: '',
        token: '',
      })
    } catch (e) {
      setNotice({ kind: 'err', text: formatError(e) })
    } finally {
      setBusy(null)
    }
  }, [])

  const handleNew = useCallback(() => {
    setNotice(null)
    setEditing(emptyForm())
  }, [])

  const handleSave = useCallback(async () => {
    if (!editing) return
    if (!editing.name.trim() || !editing.backend.trim()) {
      setNotice({ kind: 'err', text: t('settings.dbmanager.required') })
      return
    }
    setBusy('save')
    setNotice(null)
    try {
      await dbApi.profileSave(client, {
        Id: editing.id,
        Name: editing.name.trim(),
        Backend: editing.backend,
        Endpoint: editing.endpoint.trim() || undefined,
        Database: editing.database.trim() || undefined,
        TunnelRef: editing.tunnel.trim() || undefined,
        Username: editing.username.trim() || undefined,
        AccessKey: editing.accessKey.trim() || undefined,
        // Empty secret fields mean "keep existing" (matches sshmanager
        // host_update rule, documented on DbProfileSaveReq).
        Password: editing.password || undefined,
        Secret: editing.secret || undefined,
        Token: editing.token || undefined,
      })
      setNotice({ kind: 'ok', text: t('settings.dbmanager.saved') })
      setEditing(null)
      await refresh()
    } catch (e) {
      setNotice({ kind: 'err', text: formatError(e) })
    } finally {
      setBusy(null)
    }
  }, [editing, refresh, t])

  const handleConfirmDelete = useCallback(async () => {
    if (!pendingDelete) return
    setBusy(`del-${pendingDelete.Id}`)
    setNotice(null)
    try {
      await dbApi.profileRemove(client, { Id: pendingDelete.Id })
      setNotice({ kind: 'ok', text: t('settings.dbmanager.removed') })
      setPendingDelete(null)
      await refresh()
    } catch (e) {
      setNotice({ kind: 'err', text: formatError(e) })
    } finally {
      setBusy(null)
    }
  }, [pendingDelete, refresh, t])

  const sortedProfiles = useMemo(
    () => [...profiles].sort((a, b) => a.Name.localeCompare(b.Name)),
    [profiles],
  )

  return (
    <FeatureCard
      icon={<Plus size={16} />}
      title={t('settings.dbmanager.title')}
      description={t('settings.dbmanager.desc')}
      action={
        <Button
          type="button"
          size="sm"
          onClick={handleNew}
          data-guide-id="settings/dbmanager/add"
        >
          <Plus size={12} />
          <span>{t('settings.dbmanager.add')}</span>
        </Button>
      }
    >
      {notice && (
        <div
          className={`rounded-lg border px-3 py-2 text-sm ${
            notice.kind === 'ok'
              ? 'border-status-success/30 bg-status-success/10 text-status-success'
              : 'border-destructive/30 bg-destructive/10 text-destructive'
          }`}
        >
          {notice.text}
        </div>
      )}
      {error && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}
      {loading ? (
        <p className="text-sm text-muted-foreground">{t('common.loading')}</p>
      ) : sortedProfiles.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('settings.dbmanager.empty')}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {sortedProfiles.map((p) => {
            const isLoading = busy === `load-${p.Id}` || busy === `del-${p.Id}`
            return (
              <div
                key={p.Id}
                className="flex items-center justify-between gap-2 rounded-lg border border-border px-3 py-2"
                data-guide-id={`settings/dbmanager/profile/${p.Id}`}
              >
                <div className="flex min-w-0 flex-1 flex-col">
                  <div className="flex items-center gap-2">
                    <span className="truncate font-medium">{p.Name}</span>
                    <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wide text-muted-foreground">
                      {p.Backend}
                    </span>
                    {p.HasPassword && <span className="rounded bg-status-success/10 px-1.5 py-0.5 text-[10px] text-status-success">PWD</span>}
                    {p.HasSecret && <span className="rounded bg-status-success/10 px-1.5 py-0.5 text-[10px] text-status-success">SEC</span>}
                    {p.HasToken && <span className="rounded bg-status-success/10 px-1.5 py-0.5 text-[10px] text-status-success">TOK</span>}
                  </div>
                  <span className="truncate text-xs text-muted-foreground">
                    {[p.Endpoint, p.Database].filter(Boolean).join(' / ') || '—'}
                  </span>
                </div>
                <div className="flex items-center gap-1">
                  <Button
                    type="button"
                    size="sm"
                    variant="default"
                    onClick={() => onOpenClient?.(p)}
                    disabled={isLoading}
                    title={isObjectStorageBackend(p.Backend) ? t('settings.dbmanager.openBrowse') : t('settings.dbmanager.openTitle')}
                    data-guide-id={`settings/dbmanager/open/${p.Id}`}
                  >
                    <ExternalLink size={12} />
                    <span>{isObjectStorageBackend(p.Backend) ? t('settings.dbmanager.openBrowse') : t('settings.dbmanager.open')}</span>
                  </Button>
                  <Button
                    type="button"
                    size="icon-sm"
                    variant="ghost"
                    onClick={() => void handleEdit(p)}
                    disabled={isLoading}
                    title={t('settings.dbmanager.edit')}
                    data-guide-id={`settings/dbmanager/edit/${p.Id}`}
                  >
                    <Pencil size={12} />
                  </Button>
                  <Button
                    type="button"
                    size="icon-sm"
                    variant="ghost"
                    onClick={() => { setPendingDelete(p); setNotice(null) }}
                    disabled={isLoading}
                    title={t('settings.dbmanager.delete')}
                    data-guide-id={`settings/dbmanager/delete/${p.Id}`}
                  >
                    <Trash2 size={12} />
                  </Button>
                </div>
              </div>
            )
          })}
        </div>
      )}
      {editing && (
        <DbEditModal
          form={editing}
          busy={busy === 'save'}
          onChange={setEditing}
          onSave={() => void handleSave()}
          onCancel={() => { setEditing(null); setNotice(null) }}
        />
      )}
      {pendingDelete && (
        <Modal
          open
          size="sm"
          title={t('settings.dbmanager.delete')}
          onClose={() => setPendingDelete(null)}
          disableClose={busy === `del-${pendingDelete.Id}`}
          footer={
            <>
              <Button type="button" variant="ghost" size="sm" onClick={() => setPendingDelete(null)} disabled={busy === `del-${pendingDelete.Id}`}>
                {t('settings.dbmanager.cancel')}
              </Button>
              <Button type="button" variant="default" size="sm" onClick={() => void handleConfirmDelete()} disabled={busy === `del-${pendingDelete.Id}`}>
                <Check size={12} /> {t('settings.dbmanager.delete')}
              </Button>
            </>
          }
        >
          <p className="text-sm text-muted-foreground">
            {t('settings.dbmanager.deleteConfirm', { name: pendingDelete.Name })}
          </p>
        </Modal>
      )}
    </FeatureCard>
  )
}

function DbEditModal({ form, busy, onChange, onSave, onCancel }: {
  form: DbEditForm
  busy: boolean
  onChange: (f: DbEditForm) => void
  onSave: () => void
  onCancel: () => void
}) {
  const { t } = useI18n()
  const isUpdate = !!form.id
  const update = (patch: Partial<DbEditForm>) => onChange({ ...form, ...patch })
  const [sshHosts, setSshHosts] = useState<SshHostView[]>([])

  useEffect(() => {
    let cancelled = false
    void sshApi.hostList(client, {})
      .then((resp) => {
        if (!cancelled) setSshHosts(resp.Items ?? [])
      })
      .catch(() => {
        // Host list unavailable: the stale-id fallback option below keeps
        // the field from silently dropping an existing TunnelRef.
      })
    return () => { cancelled = true }
  }, [])

  const backendLabel = BACKEND_OPTIONS.find(o => o.value === form.backend)?.label ?? form.backend
  const tunnelLabel = useMemo(() => {
    if (!form.tunnel) return t('settings.dbmanager.tunnelNone')
    const host = sshHosts.find(h => h.Id === form.tunnel)
    return host ? `${host.Name} (${host.User}@${host.Host})` : form.tunnel
  }, [form.tunnel, sshHosts, t])

  return (
    <Modal
      open
      size="lg"
      title={isUpdate ? t('settings.dbmanager.formEdit') : t('settings.dbmanager.formNew')}
      onClose={onCancel}
      disableClose={busy}
      footer={
        <>
          <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
            {t('settings.dbmanager.cancel')}
          </Button>
          <Button type="button" variant="default" size="sm" onClick={onSave} disabled={busy}>
            <Check size={12} /> {t('settings.dbmanager.save')}
          </Button>
        </>
      }
    >
      <FieldGroup>
        <Field>
          <FieldLabel>
            {t('settings.dbmanager.title').replace('管理', '名称')}
            <span className="text-destructive">*</span>
          </FieldLabel>
          <Input
            value={form.name}
            onChange={(e) => update({ name: e.target.value })}
            placeholder={t('settings.dbmanager.namePlaceholder')}
          />
        </Field>
        <Field>
          <FieldLabel>
            {t('settings.dbmanager.backend')}
            <span className="text-destructive">*</span>
          </FieldLabel>
          <SelectRoot value={form.backend} onValueChange={(v) => { if (v) update({ backend: v }) }}>
            <SelectTrigger className="w-full">
              <SelectValue>{backendLabel}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              {BACKEND_OPTIONS.map(o => (
                <SelectItem key={o.value} value={o.value}>
                  <SelectItemText>{o.label}</SelectItemText>
                </SelectItem>
              ))}
            </SelectContent>
          </SelectRoot>
        </Field>
        <Field>
          <FieldLabel>{t('settings.dbmanager.endpoint')}</FieldLabel>
          <Input
            value={form.endpoint}
            onChange={(e) => update({ endpoint: e.target.value })}
            placeholder={t('settings.dbmanager.endpointPlaceholder')}
          />
        </Field>
        <Field>
          <FieldLabel>{t('settings.dbmanager.database')}</FieldLabel>
          <Input
            value={form.database}
            onChange={(e) => update({ database: e.target.value })}
          />
        </Field>
        <Field>
          <FieldLabel>{t('settings.dbmanager.tunnel')}</FieldLabel>
          <SelectRoot value={form.tunnel} onValueChange={(v) => update({ tunnel: v ?? '' })}>
            <SelectTrigger className="w-full">
              <SelectValue>{tunnelLabel}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="">
                <SelectItemText>{t('settings.dbmanager.tunnelNone')}</SelectItemText>
              </SelectItem>
              {sshHosts.map((h) => (
                <SelectItem key={h.Id} value={h.Id}>
                  <SelectItemText>{h.Name} ({h.User}@{h.Host})</SelectItemText>
                </SelectItem>
              ))}
              {form.tunnel && !sshHosts.some((h) => h.Id === form.tunnel) && (
                <SelectItem value={form.tunnel}>
                  <SelectItemText>{form.tunnel}</SelectItemText>
                </SelectItem>
              )}
            </SelectContent>
          </SelectRoot>
        </Field>
        <Field>
          <FieldLabel>{t('settings.dbmanager.username')}</FieldLabel>
          <Input
            value={form.username}
            onChange={(e) => update({ username: e.target.value })}
          />
        </Field>
        <Field>
          <FieldLabel>{t('settings.dbmanager.accessKey')}</FieldLabel>
          <Input
            value={form.accessKey}
            onChange={(e) => update({ accessKey: e.target.value })}
          />
        </Field>
        <Field>
          <FieldLabel>
            {t('settings.dbmanager.password')}
            {isUpdate && <span className="font-normal text-muted-foreground"> · {t('settings.dbmanager.hasPassword')}</span>}
          </FieldLabel>
          <Input
            type="password"
            value={form.password}
            onChange={(e) => update({ password: e.target.value })}
            autoComplete="new-password"
          />
        </Field>
        <Field>
          <FieldLabel>
            {t('settings.dbmanager.secret')}
            {isUpdate && <span className="font-normal text-muted-foreground"> · {t('settings.dbmanager.hasSecret')}</span>}
          </FieldLabel>
          <Input
            type="password"
            value={form.secret}
            onChange={(e) => update({ secret: e.target.value })}
            autoComplete="new-password"
          />
        </Field>
        <Field>
          <FieldLabel>
            {t('settings.dbmanager.token')}
            {isUpdate && <span className="font-normal text-muted-foreground"> · {t('settings.dbmanager.hasToken')}</span>}
          </FieldLabel>
          <Input
            type="password"
            value={form.token}
            onChange={(e) => update({ token: e.target.value })}
            autoComplete="new-password"
          />
        </Field>
      </FieldGroup>
    </Modal>
  )
}
