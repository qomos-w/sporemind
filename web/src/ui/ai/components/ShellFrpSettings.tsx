import { useState, useCallback, useEffect } from 'react'
import {
  Plus,
  X,
  Trash2,
  Play,
  Square,
  Edit2,
  ChevronDown,
  ChevronUp,
  Globe,
  Network,
} from 'lucide-react'
import { client } from '../../../application/generated-client'
import { GATEWAY_DEFAULT_PORT } from '../../../application/gateway'
import * as frpmanager from '../../../gen-clients/frpmanager/client'
import type { FrpInstance, FrpProxy, FrpWebProxy } from '../../../gen-types/frp'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge, Button, Input, Switch, Textarea, Field, FieldLabel, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'
import type { ReactNode } from 'react'

function FrpField({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      {children}
    </Field>
  )
}

type WebProxyMode = 'tcp' | 'https'

function webProxyIsHttps(web: FrpWebProxy | null | undefined): boolean {
  return !!web && web.Mode === 'https'
}

function narrowWebProxyMode(m: string | undefined): WebProxyMode {
  return m === 'https' ? 'https' : 'tcp'
}

type DialogMode = 'add' | 'edit'

interface DialogState {
  mode: DialogMode
  instance?: FrpInstance
}

function formatError(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e)
}

// statusOf maps an instance to badge tone/label. Disabled is checked first:
// a deliberate stop is the user's intent, so a stopped tunnel never shows a
// stale runtime error (the backend also clears lastErr on stop — this is the
// second line of defense for the transient stop-in-flight window).
//
// Running alone does NOT mean connected: it only says the frpc process is
// alive. The tunnel-up judgment is Status.Connected (backend: every proxy in
// the "running" phase). A running-but-not-connected tunnel is "connecting";
// a proxy stuck in "start error"/"check failed" surfaces as an error.
const PROXY_ERR_PHASES = new Set(['start error', 'check failed'])

export function proxyErrorOf(inst: FrpInstance): string | null {
  if (inst.Status.Error) return inst.Status.Error
  for (const p of inst.Status.Proxies ?? []) {
    if (p.Err && PROXY_ERR_PHASES.has(p.Status)) return `${p.Name}: ${p.Err}`
  }
  return null
}

export function statusOf(inst: FrpInstance, t: (key: I18nKey) => string): { tone: 'ok' | 'off' | 'err' | 'wait'; label: string } {
  if (inst.Config.Disabled) return { tone: 'off', label: t('settings.frp.status.stopped') }
  if (proxyErrorOf(inst)) return { tone: 'err', label: t('settings.frp.status.error') }
  if (inst.Status.Connected) return { tone: 'ok', label: t('settings.frp.status.running') }
  if (inst.Status.Running) return { tone: 'wait', label: t('settings.frp.status.connecting') }
  return { tone: 'off', label: t('settings.frp.status.idle') }
}

// startable reports whether the Start action makes sense: stopped tunnels
// (Disabled) and crashed ones (error, not running — e.g. failed login) both
// start/retry via frpmanager.start, which re-pushes configure and restarts.
function startable(inst: FrpInstance): boolean {
  return inst.Config.Disabled || (!!inst.Status.Error && !inst.Status.Running)
}

interface CardProps {
  instance: FrpInstance
  gatewayPort: number
  busy: boolean
  onStart: () => void
  onStop: () => void
  onEdit: () => void
  onRemove: () => void
}

function FrpInstanceCard({ instance, gatewayPort, busy, onStart, onStop, onEdit, onRemove }: CardProps) {
  const [expanded, setExpanded] = useState(false)
  const { t } = useI18n()
  const { tone, label } = statusOf(instance, t)
  const cfg = instance.Config

  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border px-3 py-2.5">
      <div className="flex items-center gap-2">
        <span className={`size-2 shrink-0 rounded-full ${tone === 'ok' ? 'bg-primary' : tone === 'err' ? 'bg-destructive' : tone === 'wait' ? 'animate-pulse bg-muted-foreground' : 'bg-muted-foreground'}`} />
        <span className="truncate text-sm font-medium" title={cfg.Name}>{cfg.Name}</span>
        <Badge variant={tone === 'ok' ? 'secondary' : tone === 'err' ? 'destructive' : 'outline'}>{label}</Badge>
      </div>
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span className="truncate font-mono" title={cfg.ServerAddr}>{cfg.ServerAddr}</span>
        {cfg.Tls && <Badge variant="outline">TLS</Badge>}
        {cfg.WebProxy?.Enabled && (
          <Badge
            variant="outline"
            title={
              webProxyIsHttps(cfg.WebProxy)
                ? t('settings.frp.webUiTooltipHttps', {
                    domains: (cfg.WebProxy.CustomDomains ?? []).join(', ') || t('settings.frp.webUiNoDomains'),
                  })
                : t('settings.frp.webUiTooltipTcp', { port: String(cfg.WebProxy.RemotePort) })
            }
          >
            <Globe size={10} /> {webProxyIsHttps(cfg.WebProxy) ? 'HTTPS' : 'WEB'}
          </Badge>
        )}
        <Badge variant="outline">{(cfg.Proxies ?? []).length} {t((cfg.Proxies ?? []).length === 1 ? 'settings.frp.proxy' : 'settings.frp.proxies')}</Badge>
      </div>
      {(() => {
        const err = proxyErrorOf(instance)
        return err ? (
          <div className="truncate rounded border border-destructive/30 bg-destructive/10 px-2 py-1 text-xs text-destructive" title={err}>
            {err}
          </div>
        ) : null
      })()}
      <div className="flex items-center gap-1">
        {startable(instance) ? (
          <Button
            type="button"
            variant="default"
            size="sm"
            disabled={busy}
            onClick={onStart}
            title={t('settings.frp.action.startTunnel')}
          >
            <Play size={12} /> {t('settings.frp.action.start')}
          </Button>
        ) : (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={onStop}
            title={t('settings.frp.action.stopTunnel')}
          >
            <Square size={12} /> {t('settings.frp.action.stop')}
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
          title={t('settings.frp.action.removeTunnel')}
          className="text-destructive hover:text-destructive"
        >
          <Trash2 size={12} />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={() => setExpanded(v => !v)}
          title={expanded ? t('settings.frp.action.collapse') : t('settings.frp.action.expandProxies')}
        >
          {expanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
        </Button>
      </div>
      {expanded && ((cfg.Proxies ?? []).length > 0 || cfg.WebProxy?.Enabled) && (
        <div className="flex flex-col gap-1 rounded-lg border border-border px-3 py-2 text-xs">
          <div className="grid grid-cols-4 gap-2 font-medium text-muted-foreground">
            <span>{t('settings.frp.col.name')}</span>
            <span>{t('settings.frp.col.kind')}</span>
            <span>{t('settings.frp.col.local')}</span>
            <span>{t('settings.frp.col.remote')}</span>
          </div>
          {cfg.WebProxy?.Enabled && (
            <div className="grid grid-cols-4 gap-2">
              <span title={cfg.WebProxy.Name}>
                <Globe size={10} /> {cfg.WebProxy.Name}
              </span>
              <span>{webProxyIsHttps(cfg.WebProxy) ? 'https' : 'web'}</span>
              <span>127.0.0.1:{gatewayPort}</span>
              <span title={
                webProxyIsHttps(cfg.WebProxy)
                  ? (cfg.WebProxy.CustomDomains ?? []).join(', ')
                  : `:${cfg.WebProxy.RemotePort}`
              }>
                {webProxyIsHttps(cfg.WebProxy)
                  ? (cfg.WebProxy.CustomDomains?.[0] ?? '—')
                  : `:${cfg.WebProxy.RemotePort}`}
              </span>
            </div>
          )}
          {(cfg.Proxies ?? []).map((p, i) => (
            <div className="grid grid-cols-[1fr_90px_1fr_80px_80px_32px] items-center gap-1.5" key={`${p.Name}-${i}`}>
              <span title={p.Name}>{p.Name}</span>
              <span>{p.Kind}</span>
              <span>{p.LocalIP || '127.0.0.1'}:{p.LocalPort}</span>
              <span>:{p.RemotePort}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

interface DialogProps {
  mode: DialogMode
  existing?: FrpInstance
  gatewayPort: number
  busy: boolean
  onSubmit: (cfg: {
    name: string
    serverAddr: string
    token: string
    tls: boolean
    legacyMode: boolean
    proxies: FrpProxy[]
    webProxy: FrpWebProxy | null
  }) => void
  onCancel: () => void
}

function newProxyRow(): FrpProxy {
  return { Name: '', Kind: 'tcp', LocalIP: '127.0.0.1', LocalPort: 0, RemotePort: 0 }
}

function FrpInstanceDialog({ mode, existing, gatewayPort, busy, onSubmit, onCancel }: DialogProps) {
  const { t } = useI18n()
  const [name, setName] = useState(existing?.Config.Name ?? '')
  const [serverHost, setServerHost] = useState(() => {
    const raw = existing?.Config.ServerAddr ?? ''
    if (!raw) return ''
    const idx = raw.lastIndexOf(':')
    return idx > 0 ? raw.slice(0, idx) : raw
  })
  const [serverPort, setServerPort] = useState(() => {
    const raw = existing?.Config.ServerAddr ?? ''
    if (!raw) return 7000
    const idx = raw.lastIndexOf(':')
    if (idx > 0) {
      const p = parseInt(raw.slice(idx + 1), 10)
      return isNaN(p) ? 7000 : p
    }
    return 7000
  })
  const serverAddr = serverHost.trim() + (serverPort ? ':' + serverPort : '')
  const [token, setToken] = useState('')
  const [tls, setTls] = useState(existing?.Config.Tls ?? false)
  const [legacyMode, setLegacyMode] = useState(existing?.Config.LegacyMode ?? false)
  const [detectBusy, setDetectBusy] = useState(false)
  const [detectResult, setDetectResult] = useState<string | null>(null)
  const [proxies, setProxies] = useState<FrpProxy[]>(
    existing?.Config.Proxies?.map(p => ({ ...p })) ?? [],
  )
  const [webEnabled, setWebEnabled] = useState(existing?.Config.WebProxy?.Enabled ?? false)
  const [webMode, setWebMode] = useState<WebProxyMode>(
    narrowWebProxyMode(existing?.Config.WebProxy?.Mode),
  )
  const [webName, setWebName] = useState(existing?.Config.WebProxy?.Name ?? 'sporemind-web')
  const [webRemotePort, setWebRemotePort] = useState(existing?.Config.WebProxy?.RemotePort ?? 0)
  const [webCustomDomains, setWebCustomDomains] = useState(
    (existing?.Config.WebProxy?.CustomDomains ?? []).join(', '),
  )
  const [webCertPem, setWebCertPem] = useState(existing?.Config.WebProxy?.CertPem ?? '')
  const [webKeyPem, setWebKeyPem] = useState('')
  const [webHostHeaderRewrite, setWebHostHeaderRewrite] = useState(
    existing?.Config.WebProxy?.HostHeaderRewrite ?? '',
  )

  const addProxy = useCallback(() => {
    setProxies(prev => [...prev, newProxyRow()])
  }, [])

  const updateProxy = useCallback((idx: number, patch: Partial<FrpProxy>) => {
    setProxies(prev => prev.map((p, i) => (i === idx ? { ...p, ...patch } : p)))
  }, [])

  const removeProxy = useCallback((idx: number) => {
    setProxies(prev => prev.filter((_, i) => i !== idx))
  }, [])

  const parsedDomains = webCustomDomains
    .split(',')
    .map(s => s.trim())
    .filter(s => s.length > 0)

  const isAdd = mode === 'add'

  const webValid =
    !webEnabled ||
    (webName.trim().length > 0 &&
      (webMode === 'tcp'
        ? webRemotePort > 0 && webRemotePort <= 65535
        : parsedDomains.length > 0 &&
          webCertPem.trim().length > 0 &&
          (isAdd ? webKeyPem.trim().length > 0 : true)))

  const valid =
    name.trim().length > 0 &&
    serverHost.trim().length > 0 &&
    serverPort > 0 && serverPort <= 65535 &&
    (proxies.length > 0 || webEnabled) &&
    proxies.every(p =>
      p.Name.trim().length > 0 &&
      (p.Kind === 'tcp' || p.Kind === 'udp') &&
      p.LocalPort > 0 && p.LocalPort <= 65535 &&
      p.RemotePort > 0 && p.RemotePort <= 65535,
    ) &&
    webValid

  const handleDetect = useCallback(async () => {
    if (!serverAddr.trim()) {
      setDetectResult(t('settings.frp.dialog.detectPrompt'))
      return
    }
    setDetectBusy(true)
    setDetectResult(null)
    try {
      const resp = await frpmanager.detect(client, {
        ServerAddr: serverAddr.trim(),
        Token: token || undefined,
      })
      if (resp.LegacyMode) {
        setLegacyMode(true)
        setDetectResult(t('settings.frp.dialog.detectLegacy'))
      } else {
        setLegacyMode(false)
        setDetectResult(t('settings.frp.dialog.detectModern'))
      }
    } catch (e) {
      setDetectResult(t('settings.frp.dialog.detectFailed', { message: formatError(e) }))
    } finally {
      setDetectBusy(false)
    }
  }, [serverHost, serverPort, token, t])

  const handleSubmit = useCallback(() => {
    const parsedDomains = webCustomDomains
      .split(',')
      .map(s => s.trim())
      .filter(s => s.length > 0)
    const hasAny =
      webEnabled ||
      webName.trim() !== '' ||
      webRemotePort > 0 ||
      parsedDomains.length > 0 ||
      webCertPem.trim() !== '' ||
      webKeyPem.trim() !== ''
    let webProxy: FrpWebProxy | null = null
    if (hasAny) {
      webProxy = {
        Enabled: webEnabled,
        Name: webName.trim(),
        Mode: webMode,
      }
      if (webMode === 'tcp') {
        webProxy.RemotePort = webRemotePort
      } else {
        webProxy.CustomDomains = parsedDomains
        if (webCertPem.trim() !== '') webProxy.CertPem = webCertPem
        if (webKeyPem.trim() !== '') webProxy.KeyPem = webKeyPem
        if (webHostHeaderRewrite.trim() !== '') {
          webProxy.HostHeaderRewrite = webHostHeaderRewrite.trim()
        }
      }
    }
    const fullAddr = serverHost.trim() + (serverPort ? ':' + serverPort : '')
    onSubmit({
      name: name.trim(),
      serverAddr: fullAddr.trim(),
      token,
      tls,
      legacyMode,
      proxies: proxies.map(p => ({
        Name: p.Name.trim(),
        Kind: p.Kind,
        LocalIP: p.LocalIP.trim() || '127.0.0.1',
        LocalPort: p.LocalPort,
        RemotePort: p.RemotePort,
      })),
      webProxy,
    })
  }, [
    name, serverHost, serverPort, token, tls, legacyMode, proxies,
    webEnabled, webName, webMode, webRemotePort,
    webCustomDomains, webCertPem, webKeyPem, webHostHeaderRewrite,
    onSubmit,
  ])

  return (
    <div className="flex flex-col rounded-lg border border-border">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <span className="text-sm font-medium">{mode === 'add' ? t('settings.frp.dialog.title.new') : t('settings.frp.dialog.title.edit', { name: existing?.Config.Name ?? '' })}</span>
        <Button type="button" variant="ghost" size="icon-sm" onClick={onCancel} title={t('common.close')}><X size={16} /></Button>
      </div>
      <div className="flex flex-col gap-3 px-4 py-3">
          <FrpField label={t('settings.frp.dialog.name')}>
            <Input
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder={t('settings.frp.dialog.namePlaceholder')}
              autoFocus
              data-guide-id="settings/frp/name"
            />
          </FrpField>
          <FrpField label={t('settings.frp.dialog.host')}>
            <Input
              value={serverHost}
              onChange={e => setServerHost(e.target.value)}
              placeholder={t('settings.frp.dialog.hostPlaceholder')}
              data-guide-id="settings/frp/host"
            />
          </FrpField>
          <FrpField label={t('settings.frp.dialog.port')}>
            <Input
              type="number"
              value={serverPort}
              onChange={e => setServerPort(parseInt(e.target.value, 10) || 0)}
              placeholder={t('settings.frp.dialog.portPlaceholder')}
              data-guide-id="settings/frp/port"
            />
          </FrpField>
          <FrpField label={
            <>{t('settings.frp.dialog.token')} {mode === 'edit' && <span className="text-xs font-normal text-muted-foreground">{t('settings.frp.dialog.tokenHintEdit')}</span>}</>
          }>
            <Input
              type="password"
              value={token}
              onChange={e => setToken(e.target.value)}
              placeholder={mode === 'edit' ? t('settings.frp.dialog.tokenPlaceholderUnchanged') : t('settings.frp.dialog.tokenPlaceholderOptional')}
              data-guide-id="settings/frp/token"
            />
          </FrpField>
          <FrpField label={t('settings.frp.dialog.enableTls')}>
            <Switch
              checked={tls}
              onCheckedChange={setTls}
              data-guide-id="settings/frp/tls"
            />
          </FrpField>
          <FrpField label={t('settings.frp.dialog.legacyMode')}>
            <div className="flex items-center gap-2">
              <Switch
                checked={legacyMode}
                onCheckedChange={setLegacyMode}
                data-guide-id="settings/frp/legacy-mode"
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={handleDetect}
                disabled={detectBusy || !serverAddr.trim()}
                data-guide-id="settings/frp/detect"
              >
                {detectBusy ? t('settings.frp.dialog.detecting') : t('settings.frp.dialog.detect')}
              </Button>
            </div>
          </FrpField>
          {detectResult && (
            <div className={`rounded border px-2 py-1 text-xs ${detectResult.includes('failed') ? 'border-destructive/30 bg-destructive/10 text-destructive' : 'border-border bg-muted/50 text-muted-foreground'}`}>
              {detectResult}
            </div>
          )}
          <div className="flex flex-col gap-3 rounded-lg border border-border px-3 py-3">
            <div className="flex items-center gap-2">
              <Globe size={14} className="text-muted-foreground" />
              <span className="flex-1 text-sm font-medium">{t('settings.frp.dialog.webUiTitle')}</span>
              <span className="flex items-center gap-2 text-sm text-muted-foreground">
                {t('settings.frp.dialog.webUiExpose')}
                <Switch
                  checked={webEnabled}
                  onCheckedChange={setWebEnabled}
                  data-guide-id="settings/frp/web-enabled"
                />
              </span>
            </div>
            <div className="text-xs text-muted-foreground">
              {webMode === 'tcp'
                ? t('settings.frp.dialog.webUiHintTcp', { port: String(gatewayPort) })
                : t('settings.frp.dialog.webUiHintHttps', { port: String(gatewayPort) })}
            </div>
            <div className="grid grid-cols-2 gap-3">
              <FrpField label={t('settings.frp.dialog.proxyName')}>
                <Input
                  value={webName}
                  disabled={!webEnabled}
                  onChange={e => setWebName(e.target.value)}
                  placeholder={t('settings.frp.dialog.proxyNamePlaceholder')}
                  data-guide-id="settings/frp/web-name"
                />
              </FrpField>
              <FrpField label={t('settings.frp.dialog.mode')}>
                <SelectRoot
                  disabled={!webEnabled}
                  value={webMode}
                  onValueChange={(v) => setWebMode(v as WebProxyMode)}
                  items={[{ value: 'tcp', label: 'tcp' }, { value: 'https', label: 'https' }]}
                >
                  <SelectTrigger data-guide-id="settings/frp/web-mode"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="tcp" data-guide-id="settings/frp/web-mode/tcp"><SelectItemText>tcp</SelectItemText></SelectItem>
                    <SelectItem value="https" data-guide-id="settings/frp/web-mode/https"><SelectItemText>https</SelectItemText></SelectItem>
                  </SelectContent>
                </SelectRoot>
              </FrpField>
            </div>
            {webMode === 'tcp' ? (
              <FrpField label={t('settings.frp.dialog.remotePort')}>
                <Input
                  type="number"
                  min={1}
                  max={65535}
                  disabled={!webEnabled}
                  value={webRemotePort || ''}
                  onChange={e => setWebRemotePort(Number(e.target.value) || 0)}
                  placeholder={t('settings.frp.dialog.remotePortPlaceholder')}
                  data-guide-id="settings/frp/web-remote-port"
                />
              </FrpField>
            ) : (
              <>
                <FrpField label={
                  <>{t('settings.frp.dialog.customDomains')} <span className="text-xs font-normal text-muted-foreground">{t('settings.frp.dialog.customDomainsHint')}</span></>
                }>
                  <Input
                    disabled={!webEnabled}
                    value={webCustomDomains}
                    onChange={e => setWebCustomDomains(e.target.value)}
                    placeholder={t('settings.frp.dialog.customDomainsPlaceholder')}
                    data-guide-id="settings/frp/web-custom-domains"
                  />
                </FrpField>
                <FrpField label={
                  <>{t('settings.frp.dialog.hostHeaderRewrite')} <span className="text-xs font-normal text-muted-foreground">{t('settings.frp.dialog.hostHeaderRewriteHint')}</span></>
                }>
                  <Input
                    disabled={!webEnabled}
                    value={webHostHeaderRewrite}
                    onChange={e => setWebHostHeaderRewrite(e.target.value)}
                    placeholder={t('settings.frp.dialog.hostHeaderRewritePlaceholder')}
                    data-guide-id="settings/frp/web-host-header-rewrite"
                  />
                </FrpField>
                <FrpField label={t('settings.frp.dialog.certificatePem')}>
                  <Textarea
                    disabled={!webEnabled}
                    value={webCertPem}
                    onChange={e => setWebCertPem(e.target.value)}
                    placeholder={t('settings.frp.dialog.certificatePlaceholder')}
                    rows={4}
                    data-guide-id="settings/frp/web-cert-pem"
                  />
                </FrpField>
                <FrpField label={
                  <>{t('settings.frp.dialog.privateKeyPem')}
                    {mode === 'edit' && <span className="text-xs font-normal text-muted-foreground">{t('settings.frp.dialog.privateKeyHintEdit')}</span>}
                  </>
                }>
                  <Textarea
                    disabled={!webEnabled}
                    value={webKeyPem}
                    onChange={e => setWebKeyPem(e.target.value)}
                    placeholder={mode === 'edit'
                      ? t('settings.frp.dialog.privateKeyPlaceholderStored')
                      : t('settings.frp.dialog.privateKeyPlaceholder')}
                    rows={4}
                    data-guide-id="settings/frp/web-key-pem"
                  />
                </FrpField>
              </>
            )}
          </div>
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center gap-2">
              <label className="flex-1 text-sm font-medium">{t('settings.frp.dialog.proxies')}</label>
              <Button type="button" variant="ghost" size="sm" onClick={addProxy} data-guide-id="settings/frp/add-proxy">
                <Plus size={12} /> {t('settings.frp.dialog.addProxy')}
              </Button>
            </div>
            {proxies.length === 0 && (
              <div className="text-xs text-muted-foreground">{t('settings.frp.dialog.noProxies')}</div>
            )}
            {proxies.length > 0 && (
              <div className="flex flex-col gap-1.5">
                <div className="grid grid-cols-[1fr_90px_1fr_80px_80px_32px] gap-1.5 px-0.5 text-xs text-muted-foreground">
                  <span>{t('settings.frp.dialog.proxyCol.name')}</span>
                  <span>{t('settings.frp.dialog.proxyCol.kind')}</span>
                  <span>{t('settings.frp.dialog.proxyCol.localIP')}</span>
                  <span>{t('settings.frp.dialog.proxyCol.local')}</span>
                  <span>{t('settings.frp.dialog.proxyCol.remote')}</span>
                  <span />
                </div>
                {proxies.map((p, i) => (
                  <div className="grid grid-cols-[1fr_90px_1fr_80px_80px_32px] items-center gap-1.5" key={i}>
                    <Input
                      value={p.Name}
                      onChange={e => updateProxy(i, { Name: e.target.value })}
                      placeholder={t('settings.frp.dialog.proxyRowNamePlaceholder')}
                      
                      data-guide-id={`settings/frp/proxy-${i}-name`}
                    />
                    <SelectRoot
                      value={p.Kind}
                      onValueChange={(v) => updateProxy(i, { Kind: v as string })}
                      items={[{ value: 'tcp', label: 'tcp' }, { value: 'udp', label: 'udp' }]}
                    >
                      <SelectTrigger data-guide-id={`settings/frp/proxy-${i}-kind`}><SelectValue /></SelectTrigger>
                      <SelectContent>
                        <SelectItem value="tcp"><SelectItemText>tcp</SelectItemText></SelectItem>
                        <SelectItem value="udp"><SelectItemText>udp</SelectItemText></SelectItem>
                      </SelectContent>
                    </SelectRoot>
                    <Input
                      value={p.LocalIP}
                      onChange={e => updateProxy(i, { LocalIP: e.target.value })}
                      placeholder={t('settings.frp.dialog.proxyRowLocalIpPlaceholder')}
                      
                      data-guide-id={`settings/frp/proxy-${i}-local-ip`}
                    />
                    <Input
                      type="number"
                      min={1}
                      max={65535}
                      value={p.LocalPort || ''}
                      onChange={e => updateProxy(i, { LocalPort: Number(e.target.value) || 0 })}
                      placeholder={t('settings.frp.dialog.proxyRowLocalPortPlaceholder')}
                      
                      data-guide-id={`settings/frp/proxy-${i}-local-port`}
                    />
                    <Input
                      type="number"
                      min={1}
                      max={65535}
                      value={p.RemotePort || ''}
                      onChange={e => updateProxy(i, { RemotePort: Number(e.target.value) || 0 })}
                      placeholder={t('settings.frp.dialog.proxyRowRemotePortPlaceholder')}
                      
                      data-guide-id={`settings/frp/proxy-${i}-remote-port`}
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => removeProxy(i)}
                      title={t('settings.frp.action.removeProxy')}
                      data-guide-id={`settings/frp/proxy-${i}-remove`}
                    >
                      <Trash2 size={13} />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          <Button type="button" variant="outline" size="sm" onClick={onCancel}>{t('common.cancel')}</Button>
          <Button
            type="button"
            size="sm"
            onClick={handleSubmit}
            disabled={!valid || busy}
            data-guide-id="settings/frp/save"
          >
            {mode === 'add' ? t('common.create') : t('common.save')}
          </Button>
        </div>
      </div>
    )
  }

export function ShellFrpSettings() {
  const { t } = useI18n()
  const [instances, setInstances] = useState<FrpInstance[]>([])
  const [gatewayPort, setGatewayPort] = useState<number>(GATEWAY_DEFAULT_PORT)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    try {
      const resp = await frpmanager.list(client)
      setInstances(resp.Items)
      if (resp.GatewayPort > 0) setGatewayPort(resp.GatewayPort)
      setError(null)
    } catch (e) {
      setError(formatError(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  // Poll for live tunnel status: frpc login failures land on the child actor
  // seconds after start/update, so a one-shot load would keep showing the
  // stale "running" state until the next manual action.
  useEffect(() => {
    const id = setInterval(() => { void load() }, 3000)
    return () => clearInterval(id)
  }, [load])

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

  const handleStart = useCallback((id: string) => {
    return wrap(id, () => frpmanager.start(client, { Id: id }))
  }, [wrap])

  const handleStop = useCallback((id: string) => {
    return wrap(id, () => frpmanager.stop(client, { Id: id }))
  }, [wrap])

  const handleRemove = useCallback((id: string) => {
    if (!confirm(t('settings.frp.dialog.removeConfirm', { name: id }))) return
    return wrap(id, () => frpmanager.remove(client, { Id: id }))
  }, [wrap, t])

  const handleDialogSubmit = useCallback(async (form: {
    name: string
    serverAddr: string
    token: string
    tls: boolean
    legacyMode: boolean
    proxies: FrpProxy[]
    webProxy: FrpWebProxy | null
  }) => {
    if (!dialog) return
    setBusy(dialog.mode === 'add' ? '__add__' : dialog.instance?.Config.Id ?? '__edit__')
    setError(null)
    try {
      if (dialog.mode === 'add') {
        await frpmanager.create(client, {
          Name: form.name,
          ServerAddr: form.serverAddr,
          Token: form.token || undefined,
          Tls: form.tls || undefined,
          LegacyMode: form.legacyMode || undefined,
          Proxies: form.proxies,
          WebProxy: form.webProxy ?? undefined,
        })
      } else if (dialog.instance) {
        await frpmanager.update(client, {
          Id: dialog.instance.Config.Id,
          Config: {
            Id: dialog.instance.Config.Id,
            Name: form.name,
            ServerAddr: form.serverAddr,
            Token: form.token,
            Tls: form.tls,
            LegacyMode: form.legacyMode || undefined,
            Proxies: form.proxies,
            WebProxy: form.webProxy ?? undefined,
          },
        })
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
      icon={<Network size={16} />}
      title={t('settings.frp.title')}
      description={t('settings.frp.desc')}
      action={
        <Button
          type="button"
          size="sm"
          onClick={() => setDialog({ mode: 'add' })}
          data-guide-id="settings/frp/add"
          disabled={!!busy || loading}
        >
          <Plus size={14} />
          {t('settings.frp.addTunnel')}
        </Button>
      }
    >
      {error && (
        <div className="flex items-center justify-between gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
          <span>{error}</span>
          <Button type="button" variant="ghost" size="icon-sm" onClick={() => setError(null)} title={t('common.close')}><X size={14} /></Button>
        </div>
      )}

      {dialog && (
        <FrpInstanceDialog
          mode={dialog.mode}
          existing={dialog.instance}
          gatewayPort={gatewayPort}
          busy={busy === '__add__' || busy === dialog.instance?.Config.Id}
          onSubmit={handleDialogSubmit}
          onCancel={() => setDialog(null)}
        />
      )}

      <div className="flex flex-col gap-2">
        {loading ? (
          <div className="py-8 text-center text-sm text-muted-foreground">{t('settings.frp.loading')}</div>
        ) : instances.length === 0 ? (
          <div className="py-8 text-center text-sm text-muted-foreground">{t('settings.frp.noTunnels')}</div>
        ) : (
          instances.map(inst => (
            <FrpInstanceCard
              key={inst.Config.Id}
              instance={inst}
              gatewayPort={gatewayPort}
              busy={busy === inst.Config.Id}
              onStart={() => { void handleStart(inst.Config.Id) }}
              onStop={() => { void handleStop(inst.Config.Id) }}
              onEdit={() => setDialog({ mode: 'edit', instance: inst })}
              onRemove={() => { void handleRemove(inst.Config.Id) }}
            />
          ))
        )}
      </div>
    </FeatureCard>
  )
}
