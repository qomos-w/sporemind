import { useState, useEffect, useCallback, useMemo } from 'react'
import { Loader2, Globe, Plus, Pencil, Trash2, X, KeyRound, Search } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as websearch from '../../../gen-clients/websearch/client'
import * as aimanagerProvider from '../../../gen-clients/aimanager/client'
import type { WebSearchAccountView, WebSearchProviderInfo, WebSearchAccountCreateReq, WebSearchAccountUpdateReq, Provider } from '../../../gen-clients/system/types'
import { useI18n } from '../../../i18n'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge, Button, Input, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'
import './WebSearchSettingsPanel.css'

const LLM_DOMAIN_BY_SEARCH_PROVIDER: Record<string, string> = {
  zhipu: 'bigmodel.cn',
  deepseek: 'api.deepseek.com',
}

// Mirrors the backend providerDomain/matchEndpoint pair: an LLM provider whose
// endpoint contains the search provider's domain shares its API key.
function detectLlmKey(llmProviders: Provider[], searchProviderId: string): Provider | undefined {
  const domain = LLM_DOMAIN_BY_SEARCH_PROVIDER[searchProviderId]
  if (!domain) return undefined
  return llmProviders.find(p => p.HasAuthToken && (p.Endpoint || '').includes(domain))
}

export function WebSearchSettingsPanel() {
  const { t } = useI18n()
  const [accounts, setAccounts] = useState<WebSearchAccountView[]>([])
  const [activeId, setActiveId] = useState('')
  const [providers, setProviders] = useState<WebSearchProviderInfo[]>([])
  const [llmProviders, setLlmProviders] = useState<Provider[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<WebSearchAccountView | null>(null)

  const load = useCallback(async () => {
    setError('')
    try {
      const [listResp, providerResp] = await Promise.all([
        websearch.accountList(client, {}),
        websearch.providerList(client),
      ])
      let items = listResp.Items || []
      // Detection is best-effort: a missing aimanager must not break the panel.
      let llm: Provider[] = []
      try {
        const llmResp = await aimanagerProvider.providerList(client)
        llm = llmResp.Items || []
      } catch {
        llm = []
      }
      setLlmProviders(llm)
      setProviders(providerResp.Providers || [])
      // Auto-import: for each provider whose key is reusable from model
      // settings, ensure an account exists (empty key — resolved at search
      // time from aimanager). Never overwrites user-created accounts.
      const existing = new Set(items.map(acc => acc.Provider))
      let imported = false
      for (const p of providerResp.Providers || []) {
        if (existing.has(p.Id) || !detectLlmKey(llm, p.Id)) continue
        try {
          await websearch.accountCreate(client, { Name: p.Name, Provider: p.Id })
          imported = true
        } catch {
          // best-effort import; the user can still add it manually
        }
      }
      if (imported) {
        const refresh = await websearch.accountList(client, {})
        items = refresh.Items || []
      }
      setAccounts(items)
      setActiveId(listResp.ActiveId || '')
    } catch (e: any) {
      setError(e?.message || t('settings.websearch.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const handleActivate = async (id: string) => {
    if (id === activeId) return
    setError('')
    try {
      const resp = await websearch.accountActivate(client, { Id: id })
      setActiveId(resp.ActiveId)
    } catch (e: any) {
      setError(e?.message || t('settings.websearch.saveFailed'))
    }
  }

  const handleDelete = async (id: string) => {
    if (!window.confirm(t('settings.websearch.deleteAccountConfirm'))) return
    setError('')
    try {
      await websearch.accountDelete(client, { Id: id })
      await load()
    } catch (e: any) {
      setError(e?.message || t('settings.websearch.saveFailed'))
    }
  }

  const providerName = (id: string) => providers.find(p => p.Id === id)?.Name || id
  const detectedLlmKey = useCallback(
    (searchProviderId: string) => detectLlmKey(llmProviders, searchProviderId),
    [llmProviders],
  )
  const autoKeyProviderIds = useMemo(
    () => providers.filter(p => detectLlmKey(llmProviders, p.Id)).map(p => p.Id),
    [providers, llmProviders],
  )

  if (loading) {
    return (
      <FeatureCard icon={<Search size={16} />} title={t('settings.websearch.title')} description={t('settings.websearch.desc')}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--text-secondary)', fontSize: 'var(--text-sm)' }}>
          <Loader2 size={16} className="sp-spin" /> {t('settings.websearch.loading')}
        </div>
      </FeatureCard>
    )
  }

  return (
    <FeatureCard
      icon={<Search size={16} />}
      title={t('settings.websearch.title')}
      description={t('settings.websearch.desc')}
      action={
        <Button type="button" size="sm" onClick={() => { setEditing(null); setDialogOpen(true) }} data-guide-id="settings/websearch/add">
          <Plus size={14} />
          {t('settings.websearch.addAccount')}
        </Button>
      }
    >
      {error && <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{error}</p>}

      {accounts.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {t('settings.websearch.accountEmpty')}
        </p>
      ) : (
        <div className="flex flex-col gap-2">
          {accounts.map(acc => {
            const isActive = acc.Id === activeId
            const autoKey = autoKeyProviderIds.includes(acc.Provider)
            return (
              <div
                key={acc.Id}
                role="button"
                tabIndex={0}
                className={`flex items-center gap-2 rounded-lg border px-3 py-2 cursor-pointer transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50${isActive ? ' border-primary bg-primary/5' : ' border-border'}`}
                onClick={() => void handleActivate(acc.Id)}
                onKeyDown={(e) => {
                  if (e.target !== e.currentTarget) return
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    void handleActivate(acc.Id)
                  }
                }}
                title={t('settings.websearch.setActive')}
                data-guide-id={`settings/websearch/${acc.Id}/activate`}
              >
                <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
                  <span className="flex items-center gap-2 text-sm font-medium">
                    <Globe size={14} className="shrink-0 text-muted-foreground" />
                    {acc.Name}
                    {isActive && <Badge variant="secondary" className="text-xs">{t('settings.websearch.activeAccount')}</Badge>}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {providerName(acc.Provider)} · {acc.HasApiKey ? t('settings.websearch.keySet') : autoKey ? t('settings.websearch.keyAuto') : t('settings.websearch.noKey')}
                  </span>
                </div>
                <Button type="button" variant="ghost" size="icon-sm" onClick={(e) => { e.stopPropagation(); setEditing(acc); setDialogOpen(true) }} title={t('settings.websearch.editAccount')} data-guide-id={`settings/websearch/${acc.Id}/edit`}>
                  <Pencil size={14} />
                </Button>
                <Button type="button" variant="ghost" size="icon-sm" onClick={(e) => { e.stopPropagation(); void handleDelete(acc.Id) }} title={t('settings.websearch.deleteAccount')} data-guide-id={`settings/websearch/${acc.Id}/delete`}>
                  <Trash2 size={14} />
                </Button>
              </div>
            )
          })}
        </div>
      )}

      <div className="ws-hints">
        <span>{t('settings.websearch.zhipuHint')}</span>
        <span>{t('settings.websearch.deepseekHint')}</span>
      </div>

      {dialogOpen && (
        <AccountDialog
          providers={providers}
          editing={editing}
          detectedLlmKey={detectedLlmKey}
          onClose={() => setDialogOpen(false)}
          onSaved={() => { setDialogOpen(false); void load() }}
        />
      )}
    </FeatureCard>
  )
}

// ---------------------------------------------------------------------------
// Add / edit account dialog
// ---------------------------------------------------------------------------

interface AccountDialogProps {
  providers: WebSearchProviderInfo[]
  editing: WebSearchAccountView | null
  detectedLlmKey: (searchProviderId: string) => Provider | undefined
  onClose: () => void
  onSaved: () => void
}

function AccountDialog({ providers, editing, detectedLlmKey, onClose, onSaved }: AccountDialogProps) {
  const { t } = useI18n()
  const [name, setName] = useState(editing?.Name || '')
  const [provider, setProvider] = useState(editing?.Provider || providers[0]?.Id || '')
  const [apiKey, setApiKey] = useState('')
  const [endpoint, setEndpoint] = useState(editing?.Endpoint || '')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const providerInfo = providers.find(p => p.Id === provider)
  const needsKey = providerInfo?.RequiresKey ?? false
  const detected = provider ? detectedLlmKey(provider) : undefined

  const handleSave = async () => {
    setSaving(true)
    setError('')
    try {
      if (editing) {
        const req: WebSearchAccountUpdateReq = { Id: editing.Id }
        if (name) req.Name = name
        if (provider) req.Provider = provider
        if (apiKey) req.ApiKey = apiKey
        if (endpoint) req.Endpoint = endpoint
        await websearch.accountUpdate(client, req)
      } else {
        const req: WebSearchAccountCreateReq = { Name: name, Provider: provider }
        if (apiKey) req.ApiKey = apiKey
        if (endpoint) req.Endpoint = endpoint
        await websearch.accountCreate(client, req)
      }
      onSaved()
    } catch (e: any) {
      setError(e?.message || t('settings.websearch.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="ws-dialog-overlay" onClick={onClose}>
      <div className="ws-dialog" onClick={event => event.stopPropagation()}>
        <div className="ws-dialog-header">
          <h4 className="ws-section-title">
            {editing ? t('settings.websearch.editAccount') : t('settings.websearch.addAccount')}
          </h4>
          <button type="button" className="ws-dialog-close" onClick={onClose} title={t('common.cancel')}>
            <X size={16} />
          </button>
        </div>

        {error && <p>{error}</p>}

        <div className="ws-dialog-body">
          <label className="ws-field">
            <span>{t('settings.websearch.accountName')}</span>
            <Input value={name} onChange={e => setName(e.target.value)} placeholder={t('settings.websearch.accountNamePlaceholder')} data-guide-id="settings/websearch/account-name" />
          </label>

          <label className="ws-field">
            <span>{t('settings.websearch.provider')}</span>
            <SelectRoot
              value={provider}
              onValueChange={(v) => setProvider(v as string)}
              items={providers.map(p => ({ value: p.Id, label: p.Name }))}
            >
              <SelectTrigger data-guide-id="settings/websearch/provider"><SelectValue /></SelectTrigger>
              <SelectContent>
                {providers.map(p => (
                  <SelectItem key={p.Id} value={p.Id} data-guide-id={`settings/websearch/provider/${p.Id}`}>
                    <SelectItemText>{p.Name}</SelectItemText>
                  </SelectItem>
                ))}
              </SelectContent>
            </SelectRoot>
            {detected && (
              <span className="ws-key-detected">
                <KeyRound size={12} />
                {t('settings.websearch.keyAutoDetected')}
              </span>
            )}
          </label>

          {needsKey && (
            <label className="ws-field">
              <span>
                {t('settings.websearch.apiKey')}
                {detected && !apiKey && <span style={{ fontSize: 'var(--text-xs)', opacity: 0.7 }}> ({t('settings.websearch.apiKeyOptionalAuto')})</span>}
              </span>
              <Input
                type="password"
                value={apiKey}
                onChange={e => setApiKey(e.target.value)}
                placeholder={editing?.HasApiKey ? t('settings.websearch.apiKeyEditPlaceholder') : t('settings.websearch.apiKeyPlaceholder')}
                data-guide-id="settings/websearch/api-key"
              />
            </label>
          )}

          <label className="ws-field">
            <span>
              {t('settings.websearch.endpoint')}
              {' '}
              <span style={{ fontSize: 'var(--text-xs)', opacity: 0.7 }}>{t('settings.websearch.endpointOptional')}</span>
            </span>
            <Input value={endpoint} onChange={e => setEndpoint(e.target.value)} placeholder="https://..." data-guide-id="settings/websearch/endpoint" />
          </label>
        </div>

        <div className="ws-dialog-footer">
          <Button type="button" variant="outline" size="sm" onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="button" size="sm" disabled={saving || !name || !provider} onClick={() => void handleSave()} data-guide-id="settings/websearch/save-account">
            {saving ? <><Loader2 size={14} className="sp-spin" />{t('common.saving')}</> : t('common.save')}
          </Button>
        </div>
      </div>
    </div>
  )
}
