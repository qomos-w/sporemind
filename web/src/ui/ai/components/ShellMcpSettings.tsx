import { useCallback, useEffect, useState, type ReactNode } from 'react'
import {
  Plus,
  X,
  Trash2,
  Edit2,
  Plug,
  Unplug,
  Server,
  ChevronDown,
  ChevronUp,
  Copy,
  Eye,
  EyeOff,
  KeyRound,
  RefreshCw,
  Cookie,
} from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as mcp from '../../../gen-clients/mcp/client'
import * as cookiebridge from '../../../gen-clients/cookiebridge/client'
import type { CookieBridgePairingInfoResp } from '../../../gen-types/cookiebridge'
import { OnMcpServerStatus } from '../../../gen-clients/mcpmanager/client'
import type {
  McpServerView,
  McpServerConfig,
  McpServerStatus,
  McpToolView,
} from '../../../gen-types/mcp'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge, Button, Input, Switch, Textarea, Field, FieldLabel, FieldDescription, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'
import { Modal } from '../../components/Modal'

function McpField({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      {children}
    </Field>
  )
}

type TransportKind = 'stdio' | 'http'

interface KvRow {
  key: string
  value: string
  configured: boolean
}

interface DialogState {
  mode: 'add' | 'edit'
  server?: McpServerView
}

function formatError(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e)
}

function statusOf(status: McpServerStatus, t: (key: I18nKey) => string): { tone: 'ok' | 'off' | 'err'; label: string } {
  if (status.Error) return { tone: 'err', label: t('settings.mcp.status.error') }
  if (status.Connected) return { tone: 'ok', label: t('settings.mcp.status.connected') }
  return { tone: 'off', label: t('settings.mcp.status.disconnected') }
}

function endpointOf(server: McpServerView): string {
  if (server.Transport === 'http') return server.Http?.Url ?? ''
  return server.Stdio?.Command ?? ''
}

interface CardProps {
  server: McpServerView
  busy: boolean
  onConnect: () => void
  onDisconnect: () => void
  onEdit: () => void
  onRemove: () => void
}

function McpServerCard({ server, busy, onConnect, onDisconnect, onEdit, onRemove }: CardProps) {
  const { t } = useI18n()
  const { tone, label } = statusOf(server.Status, t)
  const endpoint = endpointOf(server)
  const [expanded, setExpanded] = useState(false)
  const [tools, setTools] = useState<McpToolView[] | null>(null)
  const [toolsLoading, setToolsLoading] = useState(false)
  const [toolsError, setToolsError] = useState<string | null>(null)

  const toggleTools = useCallback(async () => {
    const next = !expanded
    setExpanded(next)
    if (!next) return
    if (!server.Status.Connected) {
      setTools(null)
      setToolsError(null)
      return
    }
    setToolsLoading(true)
    setToolsError(null)
    try {
      const resp = await mcp.discoverTools(client)
      setTools(resp.Servers?.find(s => s.Id === server.Id)?.Tools ?? [])
    } catch (e) {
      setToolsError(formatError(e))
      setTools(null)
    } finally {
      setToolsLoading(false)
    }
  }, [expanded, server.Id, server.Status.Connected])

  const dotTone = tone === 'ok' ? 'bg-primary' : tone === 'err' ? 'bg-destructive' : 'bg-muted-foreground'

  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border px-3 py-2.5">
      <div className="flex items-center gap-2">
        <span className={`size-2 shrink-0 rounded-full ${dotTone}`} />
        <span className="truncate text-sm font-medium" title={server.Name}>{server.Name}</span>
        <Badge variant={tone === 'ok' ? 'secondary' : tone === 'err' ? 'destructive' : 'outline'}>{label}</Badge>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="ml-auto shrink-0"
          onClick={() => void toggleTools()}
          title={t('settings.mcp.toolsToggle')}
        >
          {expanded ? <ChevronUp size={12} /> : <ChevronDown size={12} />}
        </Button>
      </div>
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Badge variant="outline">{server.Transport}</Badge>
        <span className="truncate font-mono" title={endpoint}>{endpoint}</span>
        <span className="shrink-0">{t('settings.mcp.tools', { count: server.Status.ToolCount })}</span>
      </div>
      {server.Status.Error && (
        <div className="truncate rounded border border-destructive/30 bg-destructive/10 px-2 py-1 text-xs text-destructive" title={server.Status.Error}>
          {server.Status.Error}
        </div>
      )}
      {expanded && (
        <div className="flex flex-col gap-1.5 rounded-md border border-border/60 bg-muted/30 px-2.5 py-2">
          {!server.Status.Connected ? (
            <span className="text-xs text-muted-foreground">{t('settings.mcp.toolsNotConnected')}</span>
          ) : toolsLoading ? (
            <span className="text-xs text-muted-foreground">{t('settings.mcp.toolsLoading')}</span>
          ) : toolsError ? (
            <span className="text-xs text-destructive" title={toolsError}>{t('settings.mcp.toolsError')}</span>
          ) : tools && tools.length === 0 ? (
            <span className="text-xs text-muted-foreground">{t('settings.mcp.toolsEmpty')}</span>
          ) : tools?.map(tool => (
            <div key={tool.Name} className="flex flex-col gap-0.5">
              <span className="font-mono text-xs font-medium">{tool.Name}</span>
              {tool.Description && (
                <span className="break-words text-xs text-muted-foreground">{tool.Description}</span>
              )}
            </div>
          ))}
        </div>
      )}
      <div className="flex items-center gap-1">
        {server.Status.Connected ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={onDisconnect}
            title={t('settings.mcp.action.disconnect')}
          >
            <Unplug size={12} /> {t('settings.mcp.action.disconnect')}
          </Button>
        ) : (
          <Button
            type="button"
            variant="default"
            size="sm"
            disabled={busy}
            onClick={onConnect}
            title={t('settings.mcp.action.connect')}
            data-guide-id="settings/mcp/connect"
          >
            <Plug size={12} /> {t('settings.mcp.action.connect')}
          </Button>
        )}
        <Button type="button" variant="ghost" size="icon-sm" disabled={busy} onClick={onEdit} title={t('common.edit')}>
          <Edit2 size={12} />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          disabled={busy}
          onClick={onRemove}
          title={t('settings.mcp.action.delete')}
          className="text-destructive hover:text-destructive"
        >
          <Trash2 size={12} />
        </Button>
      </div>
    </div>
  )
}

interface KvEditorProps {
  title: string
  addLabel: string
  rows: KvRow[]
  keyLabel: string
  valueLabel: string
  keepHint?: string
  guidePrefix?: string
  onChange: (rows: KvRow[]) => void
}

function KvEditor({ title, addLabel, rows, keyLabel, valueLabel, keepHint, guidePrefix, onChange }: KvEditorProps) {
  const { t } = useI18n()
  const addRow = useCallback(() => {
    onChange([...rows, { key: '', value: '', configured: false }])
  }, [rows, onChange])
  const updateRow = useCallback((idx: number, patch: Partial<KvRow>) => {
    onChange(rows.map((r, i) => (i === idx ? { ...r, ...patch } : r)))
  }, [rows, onChange])
  const removeRow = useCallback((idx: number) => {
    onChange(rows.filter((_, i) => i !== idx))
  }, [rows, onChange])

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium">{title}</span>
        {keepHint && <span className="text-xs text-muted-foreground">{keepHint}</span>}
        <Button type="button" variant="ghost" size="sm" onClick={addRow} data-guide-id={guidePrefix ? `${guidePrefix}/add` : undefined}>
          <Plus size={12} /> {addLabel}
        </Button>
      </div>
      {rows.length === 0 && (
        <div className="text-xs text-muted-foreground">{t('settings.mcp.dialog.noEntries')}</div>
      )}
      {rows.length > 0 && (
        <div className="flex flex-col gap-1.5">
          {rows.map((row, i) => (
            <div className="flex items-center gap-1.5" key={i}>
              <Input
                value={row.key}
                onChange={e => updateRow(i, { key: e.target.value })}
                placeholder={keyLabel}
                data-guide-id={guidePrefix ? `${guidePrefix}/row-${i}-key` : undefined}
              />
              <Input
                className="font-mono"
                value={row.value}
                onChange={e => updateRow(i, { value: e.target.value })}
                placeholder={valueLabel}
                data-guide-id={guidePrefix ? `${guidePrefix}/row-${i}-value` : undefined}
              />
              {row.configured && (
                <Badge variant="secondary">{t('settings.mcp.dialog.configuredBadge')}</Badge>
              )}
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                onClick={() => removeRow(i)}
                title={t('settings.mcp.dialog.removeEntry')}
                data-guide-id={guidePrefix ? `${guidePrefix}/row-${i}-remove` : undefined}
              >
                <Trash2 size={13} />
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

interface DialogProps {
  mode: 'add' | 'edit'
  existing?: McpServerView
  busy: boolean
  onSubmit: (config: McpServerConfig) => void
  onCancel: () => void
}

function McpServerDialog({ mode, existing, busy, onSubmit, onCancel }: DialogProps) {
  const { t } = useI18n()
  const isEdit = mode === 'edit'
  const [name, setName] = useState(existing?.Name ?? '')
  const [transport, setTransport] = useState<TransportKind>(
    existing?.Transport === 'http' ? 'http' : 'stdio',
  )
  const [enabled, setEnabled] = useState(existing?.Enabled ?? true)
  const [command, setCommand] = useState(existing?.Stdio?.Command ?? '')
  const [argsText, setArgsText] = useState((existing?.Stdio?.Args ?? []).join('\n'))
  const [envRows, setEnvRows] = useState<KvRow[]>(() =>
    (existing?.Stdio?.Env ?? []).map(e => ({ key: e.Key, value: '', configured: e.Configured })),
  )
  const [url, setUrl] = useState(existing?.Http?.Url ?? '')
  const [proxy, setProxy] = useState(existing?.Http?.Proxy ?? '')
  const [headerRows, setHeaderRows] = useState<KvRow[]>(() =>
    (existing?.Http?.Headers ?? []).map(h => ({ key: h.Key, value: '', configured: h.Configured })),
  )

  const valid =
    name.trim().length > 0 &&
    (transport === 'stdio' ? command.trim().length > 0 : url.trim().length > 0)

  const buildConfig = useCallback((): McpServerConfig => {
    const args = argsText.split('\n').map(s => s.trim()).filter(s => s.length > 0)
    const toMap = (rows: KvRow[]): Record<string, string> => {
      const out: Record<string, string> = {}
      for (const row of rows) {
        const key = row.key.trim()
        if (!key) continue
        out[key] = row.value
      }
      return out
    }
    if (transport === 'stdio') {
      return {
        Id: existing?.Id ?? '',
        Name: name.trim(),
        Transport: 'stdio',
        Stdio: { Command: command.trim(), Args: args, Env: toMap(envRows) },
        Enabled: enabled,
      }
    }
    return {
      Id: existing?.Id ?? '',
      Name: name.trim(),
      Transport: 'http',
      Http: { Url: url.trim(), Headers: toMap(headerRows), Proxy: proxy.trim() || undefined },
      Enabled: enabled,
    }
  }, [existing, name, transport, enabled, command, argsText, envRows, url, proxy, headerRows])

  return (
    <Modal
      open
      onClose={onCancel}
      title={
        mode === 'add'
          ? t('settings.mcp.dialog.title.add')
          : t('settings.mcp.dialog.title.edit', { name: existing?.Name ?? '' })
      }
      size="md"
      disableClose={busy}
      footer={
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" size="sm" onClick={onCancel} disabled={busy}>
            {t('common.cancel')}
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={() => onSubmit(buildConfig())}
            disabled={!valid || busy}
            data-guide-id="settings/mcp/save"
          >
            {mode === 'add' ? t('common.create') : t('common.save')}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-3">
        <McpField label={t('settings.mcp.dialog.name')}>
            <Input
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder={t('settings.mcp.dialog.namePlaceholder')}
              data-guide-id="settings/mcp/name"
            />
          </McpField>
          <McpField label={t('settings.mcp.dialog.transport')}>
            <SelectRoot
              value={transport}
              onValueChange={(v) => setTransport(v as TransportKind)}
              items={[{ value: 'stdio', label: 'stdio' }, { value: 'http', label: 'HTTP' }]}
            >
              <SelectTrigger data-guide-id="settings/mcp/transport"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="stdio" data-guide-id="settings/mcp/transport/stdio"><SelectItemText>stdio</SelectItemText></SelectItem>
                <SelectItem value="http" data-guide-id="settings/mcp/transport/http"><SelectItemText>HTTP</SelectItemText></SelectItem>
              </SelectContent>
            </SelectRoot>
          </McpField>
          {transport === 'stdio' ? (
            <>
              <McpField label={t('settings.mcp.dialog.command')}>
                <Input
                  value={command}
                  onChange={e => setCommand(e.target.value)}
                  placeholder={t('settings.mcp.dialog.commandPlaceholder')}
                  className="font-mono"
                  data-guide-id="settings/mcp/command"
                />
              </McpField>
              <McpField label={t('settings.mcp.dialog.args')}>
                <Textarea
                  rows={3}
                  value={argsText}
                  onChange={e => setArgsText(e.target.value)}
                  placeholder={t('settings.mcp.dialog.argsPlaceholder')}
                  data-guide-id="settings/mcp/args"
                />
              </McpField>
              <KvEditor
                title={t('settings.mcp.dialog.env')}
                addLabel={t('settings.mcp.dialog.addEnv')}
                rows={envRows}
                keyLabel={t('settings.mcp.dialog.envKey')}
                valueLabel={t('settings.mcp.dialog.envValue')}
                keepHint={isEdit ? t('settings.mcp.dialog.keepHint') : undefined}
                guidePrefix="settings/mcp/env"
                onChange={setEnvRows}
              />
            </>
          ) : (
            <>
              <McpField label={t('settings.mcp.dialog.url')}>
                <Input
                  value={url}
                  onChange={e => setUrl(e.target.value)}
                  placeholder={t('settings.mcp.dialog.urlPlaceholder')}
                  className="font-mono"
                  data-guide-id="settings/mcp/url"
                />
              </McpField>
              <McpField label={t('settings.mcp.dialog.proxy')}>
                <Input
                  value={proxy}
                  onChange={e => setProxy(e.target.value)}
                  placeholder={t('settings.mcp.dialog.proxyPlaceholder')}
                  className="font-mono"
                  data-guide-id="settings/mcp/proxy"
                />
                <FieldDescription>{t('settings.mcp.dialog.proxyDescription')}</FieldDescription>
              </McpField>
              <KvEditor
                title={t('settings.mcp.dialog.headers')}
                addLabel={t('settings.mcp.dialog.addHeader')}
                rows={headerRows}
                keyLabel={t('settings.mcp.dialog.headerKey')}
                valueLabel={t('settings.mcp.dialog.headerValue')}
                keepHint={isEdit ? t('settings.mcp.dialog.keepHint') : undefined}
                guidePrefix="settings/mcp/headers"
                onChange={setHeaderRows}
              />
            </>
          )}
          <McpField label={t('settings.mcp.dialog.enabled')}>
            <Switch
              checked={enabled}
              onCheckedChange={setEnabled}
              data-guide-id="settings/mcp/enabled"
            />
          </McpField>
        </div>
    </Modal>
  )
}
function CookieBridgeCopyRow({
  label,
  value,
  secret = false,
  copyLabel,
  copiedLabel,
  showLabel,
  hideLabel,
}: {
  label: string
  value: string
  secret?: boolean
  copyLabel: string
  copiedLabel: string
  showLabel: string
  hideLabel: string
}) {
  const [revealed, setRevealed] = useState(!secret)
  const [copied, setCopied] = useState(false)
  const doCopy = async () => {
    if (!value) return
    await navigator.clipboard.writeText(value)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <div className="flex items-center gap-2">
      <span className="w-20 shrink-0 text-xs text-muted-foreground">{label}</span>
      <code
        className="min-w-0 flex-1 truncate rounded bg-muted/50 px-2 py-1 font-mono text-xs"
        title={value}
      >
        {revealed ? value : '•'.repeat(24)}
      </code>
      {secret && (
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={() => setRevealed(v => !v)}
          title={revealed ? hideLabel : showLabel}
        >
          {revealed ? <EyeOff size={12} /> : <Eye size={12} />}
        </Button>
      )}
      <Button type="button" variant="ghost" size="sm" onClick={() => void doCopy()}>
        <Copy size={12} /> {copied ? copiedLabel : copyLabel}
      </Button>
    </div>
  )
}

function CookieBridgeCard() {
  const { t } = useI18n()
  const [info, setInfo] = useState<CookieBridgePairingInfoResp | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const resp = await cookiebridge.pairingInfo(client, {})
      setInfo(resp)
      setError(null)
    } catch (e) {
      setError(formatError(e))
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const handleRegen = async () => {
    if (!window.confirm(t('settings.mcp.cookieBridge.regenConfirm'))) return
    setBusy(true)
    try {
      const resp = await cookiebridge.regenToken(client, {})
      setInfo(prev => (prev ? { ...prev, PairingToken: resp.PairingToken } : prev))
      await load()
    } catch (e) {
      setError(formatError(e))
    } finally {
      setBusy(false)
    }
  }

  const labels = {
    copyLabel: t('settings.mcp.cookieBridge.copy'),
    copiedLabel: t('settings.mcp.cookieBridge.copied'),
    showLabel: t('settings.mcp.cookieBridge.show'),
    hideLabel: t('settings.mcp.cookieBridge.hide'),
  }

  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border bg-muted/20 px-3 py-2.5">
      <div className="flex items-center gap-2">
        <Cookie size={14} className="shrink-0 text-muted-foreground" />
        <span className="text-sm font-medium">{t('settings.mcp.cookieBridge.title')}</span>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="ml-auto shrink-0"
          disabled={busy || !info}
          onClick={() => void handleRegen()}
          title={t('settings.mcp.cookieBridge.regen')}
        >
          <RefreshCw size={12} /> {t('settings.mcp.cookieBridge.regen')}
        </Button>
      </div>
      {error ? (
        <div className="flex items-center gap-2 text-xs text-muted-foreground" role="status">
          <KeyRound size={12} className="shrink-0" />
          <span>{t('settings.mcp.cookieBridge.unavailable')}</span>
        </div>
      ) : info ? (
        <div className="flex flex-col gap-1.5">
          <p className="text-xs text-muted-foreground">{t('settings.mcp.cookieBridge.desc')}</p>
          <CookieBridgeCopyRow label={t('settings.mcp.cookieBridge.mcpUrl')} value={info.McpUrl} {...labels} />
          <CookieBridgeCopyRow label={t('settings.mcp.cookieBridge.extUrl')} value={info.PendingUrl} {...labels} />
          <CookieBridgeCopyRow label={t('settings.mcp.cookieBridge.token')} value={info.PairingToken} secret {...labels} />
        </div>
      ) : (
        <div className="text-xs text-muted-foreground">{t('settings.mcp.loading')}</div>
      )}
    </div>
  )
}

export function ShellMcpSettings() {
  const { t } = useI18n()
  const [servers, setServers] = useState<McpServerView[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [dialog, setDialog] = useState<DialogState | null>(null)

  const load = useCallback(async () => {
    try {
      const resp = await mcp.listServers(client)
      setServers(resp.Items ?? [])
      setError(null)
    } catch (e) {
      setError(formatError(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  // Live status refresh: mcpinstance emits mcp.server_status on every
  // connection state change; the generated handler keeps the list in sync.
  useEffect(() => {
    return OnMcpServerStatus(client, (payload) => {
      setServers(prev => prev.map(s => (s.Id === payload.Status.Id ? { ...s, Status: payload.Status } : s)))
    })
  }, [])

  const wrap = useCallback(async (id: string, fn: () => Promise<unknown>) => {
    setBusy(id)
    setError(null)
    try {
      await fn()
      await load()
    } catch (e) {
      setError(formatError(e))
    } finally {
      setBusy(null)
    }
  }, [load])

  const handleConnect = useCallback((id: string) => {
    return wrap(id, () => mcp.connect(client, { Id: id }))
  }, [wrap])

  const handleDisconnect = useCallback((id: string) => {
    return wrap(id, () => mcp.disconnect(client, { Id: id }))
  }, [wrap])

  const handleRemove = useCallback((id: string, name: string) => {
    if (!window.confirm(t('settings.mcp.removeConfirm', { name }))) return
    return wrap(id, () => mcp.removeServer(client, { Id: id }))
  }, [wrap, t])

  const handleDialogSubmit = useCallback(async (config: McpServerConfig) => {
    if (!dialog) return
    setBusy(dialog.mode === 'add' ? '__add__' : (dialog.server?.Id ?? '__edit__'))
    setError(null)
    try {
      if (dialog.mode === 'add') {
        await mcp.addServer(client, { Config: config })
      } else if (dialog.server) {
        await mcp.updateServer(client, { Id: dialog.server.Id, Config: config })
      }
      setDialog(null)
      await load()
    } catch (e) {
      setError(formatError(e))
    } finally {
      setBusy(null)
    }
  }, [dialog, load])

  return (
    <FeatureCard
      icon={<Server size={16} />}
      title={t('settings.mcp.title')}
      description={t('settings.mcp.desc')}
      action={
        <Button
          type="button"
          size="sm"
          onClick={() => setDialog({ mode: 'add' })}
          data-guide-id="settings/mcp/add"
          disabled={busy !== null}
        >
          <Plus size={14} /> {t('settings.mcp.addServer')}
        </Button>
      }
    >
      <CookieBridgeCard />

      {error && (
        <div className="flex items-center justify-between gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
          <span>{error}</span>
          <Button type="button" variant="ghost" size="icon-sm" onClick={() => setError(null)} title={t('common.close')}>
            <X size={14} />
          </Button>
        </div>
      )}

      {dialog && (
        <McpServerDialog
          mode={dialog.mode}
          existing={dialog.server}
          busy={busy !== null}
          onSubmit={handleDialogSubmit}
          onCancel={() => setDialog(null)}
        />
      )}

      {loading ? (
        <div className="py-8 text-center text-sm text-muted-foreground">{t('settings.mcp.loading')}</div>
      ) : servers.length === 0 ? (
        <div className="py-8 text-center text-sm text-muted-foreground">{t('settings.mcp.noServers')}</div>
      ) : (
        <div className="flex flex-col gap-2">
          {servers.map(s => (
            <McpServerCard
              key={s.Id}
              server={s}
              busy={busy === s.Id}
              onConnect={() => handleConnect(s.Id)}
              onDisconnect={() => handleDisconnect(s.Id)}
              onEdit={() => setDialog({ mode: 'edit', server: s })}
              onRemove={() => handleRemove(s.Id, s.Name)}
            />
          ))}
        </div>
      )}
    </FeatureCard>
  )
}
