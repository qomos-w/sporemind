import { useCallback, useEffect, useState } from 'react'
import { Check, ChevronDown, Loader2, Pencil, Plus, Trash2, X, Image, Video, Eye } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as aimanager from '../../../gen-clients/aimanager/client'
import * as media from '../../../gen-clients/media/client'
import type {
  MediaAccountCreateReq,
  MediaAccountUpdateReq,
  MediaAccountView,
  Provider,
} from '../../../gen-clients/system/types'
import { useI18n } from '../../../i18n'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge, Button, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'
import { useBrowserOverlay } from '../browserOverlay'
import './MediaSettingsPanel.css'
import './ShellProviderSettings.css'

type AccountKind = 'image' | 'video' | 'vision'

// Vision is binding-only: no media accounts, just the provider-model picker.
const isVisionKind = (kind: AccountKind) => kind === 'vision'

interface ProviderConfig {
  id: string
  endpoint?: string
  models?: string[]
}

const GEMINI_ENDPOINT = 'https://generativelanguage.googleapis.com'
const DASHSCOPE_ENDPOINT = 'https://dashscope.aliyuncs.com'
const ARK_ENDPOINT = 'https://ark.cn-beijing.volces.com/api/v3'

const KIND_CONFIG: Record<AccountKind, { providers: ProviderConfig[]; models: string[] }> = {
  image: {
    providers: [
      { id: 'openai', endpoint: 'https://api.openai.com/v1', models: ['gpt-image-2', 'gpt-image-2-pro'] },
      { id: 'openai_custom' },
      { id: 'gemini', endpoint: GEMINI_ENDPOINT, models: ['gemini-2.5-flash-image', 'gemini-3.1-flash-image-preview', 'gemini-3-pro-image-preview'] },
      { id: 'minimax', endpoint: 'https://api.minimaxi.com/v1', models: ['image-01'] },
      // Qwen (Alibaba Cloud Model Studio / DashScope) native image API.
      { id: 'qwen', endpoint: DASHSCOPE_ENDPOINT, models: ['qwen-image-3.0-pro', 'qwen-image-3.0', 'qwen-image-2.0-pro', 'qwen-image-max', 'qwen-image-plus', 'qwen-image-edit', 'wan2.7-image-pro', 'wanx2.1-t2i-turbo'] },
      // Doubao (Volcengine Ark) images/generations rides the same Ark platform
      // as the video providers; exposed under its own brand-facing id.
      { id: 'doubao', endpoint: ARK_ENDPOINT, models: ['doubao-seedream-3.0-t2i', 'seedream-4.0', 'doubao-seedream-5-0-260128'] },
    ],
    models: ['gpt-image-2', 'gpt-image-2-pro', 'gemini-2.5-flash-image', 'nano-banana', 'grok-imagine-image', 'qwen-image-3.0-pro', 'doubao-seedream-3.0-t2i'],
  },
  video: {
    providers: [
      { id: 'openai_custom' },
      { id: 'glm', models: ['cogvideox-3'] },
      { id: 'gemini', endpoint: GEMINI_ENDPOINT, models: ['veo-3.0-generate-001'] },
      // Qwen (DashScope) Wan video-synthesis async-task flow.
      { id: 'qwen', endpoint: DASHSCOPE_ENDPOINT, models: ['wan-2.5-t2v-720p', 'wan2.5-t2v-preview'] },
      { id: 'ark', endpoint: ARK_ENDPOINT, models: ['dreamina-seedance-2-0-260128'] },
      // Doubao (Volcengine Ark) shares the `ark` backend — Seedance 2.0 rides
      // the same contents/generations async-task API. Exposed under its own
      // provider id so the label matches the model family users pick.
      { id: 'doubao', endpoint: ARK_ENDPOINT, models: ['doubao-seedance-2.0-720p-video'] },
    ],
    models: ['doubao-seedance-2.0-720p-video'],
  },
  vision: { providers: [], models: [] },
}

// isVisionCandidate mirrors the backend llmclient.ModelSupportsImageInput
// heuristic (pkg/llmclient/openai.go): chat-modality models are vision-capable
// unless their name belongs to a known text-only model. Within the deepseek
// family the v4.1 line and the *vision* experimental variant accept images,
// while chat / reasoner / r1 / v3 / v4-flash / v4-pro stay text-only. If the
// heuristic misjudges, the aggregator's runtime 400 learning corrects it.
function isVisionCandidate(modelName: string, modality?: string): boolean {
  if (modality && modality !== 'chat') return false
  const m = modelName.toLowerCase()
  if (!m.includes('deepseek')) return true
  return m.includes('vision') || m.includes('v4.1')
}

// Mirrors the backend providerDomain/matchEndpoint pair in
// pkg/actor/media/actor.go: an LLM provider whose endpoint contains the media
// account's domain shares its API key (resolved at generation time via
// aimanager; never copied into the media account).
function urlHost(rawUrl: string): string {
  try {
    return new URL(rawUrl.includes('://') ? rawUrl : `https://${rawUrl}`).hostname
  } catch {
    return ''
  }
}

// providerDomain reproduces the backend switch so detection stays in sync with
// the runtime credential resolution: the account's own base URL host wins, and
// well-known providers fall back to their public endpoint domain.
function providerDomain(providerId: string, baseUrl?: string): string {
  const host = baseUrl ? urlHost(baseUrl) : ''
  if (host) return host
  switch (providerId) {
    case 'openai':
    case 'openai_custom':
      return 'api.openai.com'
    case 'gemini':
      return 'generativelanguage.googleapis.com'
    case 'glm':
      return 'bigmodel.cn'
    case 'minimax':
      return 'api.minimaxi.com'
    case 'ark':
    case 'doubao':
      return 'volcengine.com'
    default:
      return ''
  }
}

// detectLlmKey returns true when an LLM provider with a token shares the same
// endpoint domain as the media provider/account.
function detectLlmKey(llmProviders: Provider[], providerId: string, baseUrl?: string): boolean {
  return matchedLlmProvider(llmProviders, providerId, baseUrl) !== undefined
}

// matchedLlmProvider returns the LLM provider sharing the media provider's
// endpoint domain when it has a token, else undefined. The provider's Proxy is
// inherited by auto-created media accounts so media generation honors the same
// egress as chat.
function matchedLlmProvider(llmProviders: Provider[], providerId: string, baseUrl?: string): Provider | undefined {
  const domain = providerDomain(providerId, baseUrl)
  if (!domain) return undefined
  return llmProviders.find(p => p.HasAuthToken && (p.Endpoint || '').includes(domain))
}

export function MediaSettingsPanel() {
  return (
    <>
      <AccountSection kind="image" />
      <AccountSection kind="video" />
      <AccountSection kind="vision" />
    </>
  )
}

function AccountSection({ kind }: { kind: AccountKind }) {
  const { t } = useI18n()
  const [accounts, setAccounts] = useState<MediaAccountView[]>([])
  const [activeId, setActiveId] = useState('')
  const [bound, setBound] = useState({ provider: '', model: '' })
  const [boundAgg, setBoundAgg] = useState('')
  const [aggregators, setAggregators] = useState<{ Id: string; Name: string }[]>([])
  const [llmProviders, setLlmProviders] = useState<Provider[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<MediaAccountView | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const response = await media.listAccounts(client, { Kind: kind })
      let items = response.Items || []
      const activeIdVal = response.ActiveId || ''
      setBound({ provider: response.BoundProvider || '', model: response.BoundModel || '' })
      setBoundAgg(response.BoundAggregator || '')

      // Vision's dropdown also lists user-configured aggregators (recognition
      // can route through a named pool). Best-effort: a missing aimanager
      // must not break the panel.
      if (isVisionKind(kind)) {
        try {
          const aggResp = await aimanager.aggregatorList(client)
          setAggregators((aggResp.Items || []).map(d => ({ Id: d.Id, Name: d.Name || d.Id })))
        } catch {
          setAggregators([])
        }
      }

      // Best-effort fetch of LLM providers for same-domain key detection.
      // A missing aimanager must not break the panel.
      let llm: Provider[] = []
      try {
        const llmResp = await aimanager.providerList(client)
        llm = llmResp.Items || []
      } catch {
        llm = []
      }
      setLlmProviders(llm)

      // Auto-create an empty-key placeholder account for every configured
      // provider whose LLM counterpart (same endpoint domain) already has a
      // token. Only providers with a well-known endpoint in KIND_CONFIG are
      // auto-created — openai_custom is excluded because there is no default
      // base URL to make it functional. Never overwrites existing accounts
      // and never copies the LLM token into the media account — the key is
      // resolved at generation time by the media actor (resolveProviderToken).
      const existing = new Set(items.map(acc => acc.Provider))
      let imported = false
      for (const p of KIND_CONFIG[kind].providers) {
        if (!p.endpoint) continue
        const matched = matchedLlmProvider(llm, p.id, p.endpoint)
        if (existing.has(p.id) || !matched) continue
        const models = p.models?.length ? p.models : KIND_CONFIG[kind].models
        try {
          await media.createAccount(client, {
            Kind: kind,
            Name: t(`settings.media.provider.${p.id}` as any),
            Provider: p.id,
            BaseUrl: p.endpoint,
            Model: models[0] || '',
            Proxy: matched.Proxy || '',
          })
          imported = true
        } catch {
          // best-effort import; the user can still add it manually
        }
      }
      if (imported) {
        const refresh = await media.listAccounts(client, { Kind: kind })
        items = refresh.Items || []
        setActiveId(refresh.ActiveId || '')
      } else {
        setActiveId(activeIdVal)
      }
      setAccounts(items)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [kind, t])

  useEffect(() => {
    void load()
  }, [load])

  const activate = async (id: string) => {
    if (id === activeId) return
    setError('')
    try {
      const response = await media.activateAccount(client, { Kind: kind, Id: id })
      setActiveId(response.ActiveId)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const remove = async (id: string) => {
    if (!window.confirm(t('settings.media.deleteAccountConfirm'))) return
    setError('')
    try {
      await media.deleteAccount(client, { Id: id })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  // Provider models detected for this kind: every (Provider, Model) pair whose
  // runtime modality matches. The same model on two providers yields two
  // independent rows; each row activates a provider-model binding that routes
  // the agent's aggregator fallback (mutually exclusive with media accounts).
  // Vision candidates come from chat-modality models that pass the
  // vision-capable heuristic.
  const providerModels = llmProviders.flatMap(p =>
    (p.Models || []).filter(m => isVisionKind(kind) ? isVisionCandidate(m.Name, m.Modality) : m.Modality === kind).map(m => ({ provider: p.Name, model: m.Name }))
  )

  const selectProviderModel = async (provider: string, model: string) => {
    setError('')
    try {
      const isActive = bound.provider === provider && bound.model === model
      const response = await media.providerModelSet(client, isActive
        ? { Kind: kind }
        : { Kind: kind, Provider: provider, Model: model })
      setBound({ provider: response.Provider || '', model: response.Model || '' })
      // Activating a binding deactivates the account — refresh both states.
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  // Vision selects its binding from one dropdown: auto (no binding), a
  // (provider, model) pair, or a named aggregator pool. Values are encoded as
  // '' | 'pm:<provider>|<model>' | 'agg:<id>'.
  const visionValue = boundAgg ? `agg:${boundAgg}` : bound.provider && bound.model ? `pm:${bound.provider}|${bound.model}` : ''

  const selectVisionBinding = async (value: string) => {
    setError('')
    try {
      const req: { Kind: string; Provider?: string; Model?: string; Aggregator?: string } = { Kind: 'vision' }
      if (value.startsWith('pm:')) {
        const sep = value.indexOf('|')
        req.Provider = value.slice(3, sep)
        req.Model = value.slice(sep + 1)
      } else if (value.startsWith('agg:')) {
        req.Aggregator = value.slice(4)
      }
      const response = await media.providerModelSet(client, req)
      setBound({ provider: response.Provider || '', model: response.Model || '' })
      setBoundAgg(response.Aggregator || '')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <FeatureCard
      icon={kind === 'image' ? <Image size={16} /> : kind === 'video' ? <Video size={16} /> : <Eye size={16} />}
      title={t(`settings.media.${kind}Title`)}
      description={t(`settings.media.${kind}Desc`)}
      action={isVisionKind(kind) ? undefined : (
        <Button type="button" size="sm" onClick={() => { setEditing(null); setDialogOpen(true) }} data-guide-id={`settings/media/${kind}/add`}>
          <Plus size={14} />
          {t('settings.media.addAccount')}
        </Button>
      )}
    >
      {error && <p>{error}</p>}
      {loading ? (
        <div><Loader2 size={15} className="media-spinner" /> {t('settings.media.loading')}</div>
      ) : isVisionKind(kind) ? null : accounts.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('settings.media.accountEmpty')}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {accounts.map(account => (
            <div
              key={account.Id}
              role="button"
              tabIndex={0}
              className={`flex items-center gap-2 rounded-lg border px-3 py-2 cursor-pointer transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50${account.Id === activeId ? ' border-primary bg-primary/5' : ' border-border'}`}
              onClick={() => void activate(account.Id)}
              onKeyDown={(e) => {
                if (e.target !== e.currentTarget) return
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  void activate(account.Id)
                }
              }}
              title={t('settings.media.setActive')}
              data-guide-id={`settings/media/${kind}/${account.Id}/activate`}
            >
              <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
                <span className="flex items-center gap-2 text-sm font-medium">
                  {account.Name}
                  {account.Id === activeId && <Badge variant="secondary" className="text-xs">{t('settings.media.activeAccount')}</Badge>}
                </span>
                <span className="text-xs text-muted-foreground">
                  {t(`settings.media.provider.${account.Provider}` as any)} · {account.HasApiKey ? t('settings.media.keySet') : detectLlmKey(llmProviders, account.Provider, account.BaseUrl) ? t('settings.media.keyAuto') : t('settings.media.noKey')}
                </span>
              </div>
              <Button type="button" variant="ghost" size="icon-sm" onClick={(e) => { e.stopPropagation(); setEditing(account); setDialogOpen(true) }} title={t('settings.media.editAccount')} data-guide-id={`settings/media/${kind}/${account.Id}/edit`}>
                <Pencil size={14} />
              </Button>
              <Button type="button" variant="ghost" size="icon-sm" onClick={(e) => { e.stopPropagation(); void remove(account.Id) }} title={t('settings.media.deleteAccount')} data-guide-id={`settings/media/${kind}/${account.Id}/delete`}>
                <Trash2 size={14} />
              </Button>
            </div>
          ))}
        </div>
      )}
      {isVisionKind(kind) ? (
        <div className="mt-3 flex flex-col gap-2" data-testid={`media-provider-models-${kind}`}>
          <div className="flex items-baseline justify-between gap-2">
            <span className="text-xs font-medium text-muted-foreground">{t('settings.media.visionModelLabel')}</span>
            <span className="text-xs text-muted-foreground">{t('settings.media.providerModelsHint')}</span>
          </div>
          <VisionBindingSelect
            value={visionValue}
            onSelect={v => { void selectVisionBinding(v) }}
            providerModels={providerModels}
            aggregators={aggregators}
          />
        </div>
      ) : providerModels.length > 0 && (
        <div className="mt-3 flex flex-col gap-2" data-testid={`media-provider-models-${kind}`}>
          <div className="flex items-baseline justify-between gap-2">
            <span className="text-xs font-medium text-muted-foreground">{t('settings.media.providerModelsTitle')}</span>
            <span className="text-xs text-muted-foreground">{t('settings.media.providerModelsHint')}</span>
          </div>
          <div className="flex flex-col gap-2">
            {providerModels.map(({ provider, model }) => {
              const isActive = !activeId && bound.provider === provider && bound.model === model
              return (
                <div
                  key={`${provider}::${model}`}
                  role="button"
                  tabIndex={0}
                  className={`flex items-center gap-2 rounded-lg border px-3 py-2 cursor-pointer transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50${isActive ? ' border-primary bg-primary/5' : ' border-border'}`}
                  onClick={() => void selectProviderModel(provider, model)}
                  onKeyDown={(e) => {
                    if (e.target !== e.currentTarget) return
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      void selectProviderModel(provider, model)
                    }
                  }}
                  title={isActive ? t('settings.media.unsetActive') : t('settings.media.setActive')}
                  data-guide-id={`settings/media/${kind}/provider-model/${provider}/${model}`}
                >
                  <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
                    <span className="flex items-center gap-2 text-sm font-medium">
                      {model}
                      {isActive && <Badge variant="secondary" className="text-xs">{t('settings.media.activeAccount')}</Badge>}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      {t('settings.media.providerModelsSource', { provider })}
                    </span>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}
      {dialogOpen && !isVisionKind(kind) && (
        <AccountDialog kind={kind} editing={editing} detectedModels={providerModels.map(pm => pm.model)} onClose={() => setDialogOpen(false)} onSaved={() => { setDialogOpen(false); void load() }} />
      )}
    </FeatureCard>
  )
}

// Vision binding dropdown: '' (auto), 'pm:<provider>|<model>', or 'agg:<id>'.
// Rendered as a Base UI Select popup instead of a native <select> because the
// native popup direction is OS-controlled and overflows the window when the
// panel sits near the bottom; the Base UI Positioner measures the space around
// the trigger, flips the popup upward when needed, and scrolls long lists
// within the available height. The popup is an HTML layer that may float above
// the embedded native browser window, so it registers with
// useBrowserOverlay(open) (project constraint) — no z-index workarounds.
const VISION_AUTO_VALUE = 'auto'

function VisionBindingSelect({ value, onSelect, providerModels, aggregators }: {
  value: string
  onSelect: (value: string) => void
  providerModels: { provider: string; model: string }[]
  aggregators: { Id: string; Name: string }[]
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  useBrowserOverlay(open)

  const items = [
    { value: VISION_AUTO_VALUE, label: t('settings.media.visionAuto') },
    ...providerModels.map(({ provider, model }) => ({ value: `pm:${provider}|${model}`, label: `${model} · ${provider}` })),
    ...aggregators.map(agg => ({ value: `agg:${agg.Id}`, label: agg.Name })),
  ]

  return (
    <SelectRoot
      open={open}
      onOpenChange={setOpen}
      items={items}
      value={value || VISION_AUTO_VALUE}
      onValueChange={v => onSelect(v === VISION_AUTO_VALUE ? '' : v as string)}
    >
      <SelectTrigger size="sm" className="w-full" data-guide-id="settings/media/vision/select">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={VISION_AUTO_VALUE}>
          <SelectItemText>{t('settings.media.visionAuto')}</SelectItemText>
        </SelectItem>
        {providerModels.length > 0 && (
          <div className="px-1.5 pt-1 pb-0.5 text-[11px] font-medium text-muted-foreground">{t('settings.media.visionModelsGroup')}</div>
        )}
        {providerModels.map(({ provider, model }) => (
          <SelectItem key={`pm:${provider}|${model}`} value={`pm:${provider}|${model}`}>
            <SelectItemText>{model} · {provider}</SelectItemText>
          </SelectItem>
        ))}
        {aggregators.length > 0 && (
          <div className="px-1.5 pt-1 pb-0.5 text-[11px] font-medium text-muted-foreground">{t('settings.media.visionAggregatorsGroup')}</div>
        )}
        {aggregators.map(agg => (
          <SelectItem key={`agg:${agg.Id}`} value={`agg:${agg.Id}`}>
            <SelectItemText>{agg.Name}</SelectItemText>
          </SelectItem>
        ))}
      </SelectContent>
    </SelectRoot>
  )
}

function AccountDialog({ kind, editing, detectedModels, onClose, onSaved }: { kind: AccountKind; editing: MediaAccountView | null; detectedModels: string[]; onClose: () => void; onSaved: () => void }) {
  useBrowserOverlay(true)
  const { t } = useI18n()
  const config = KIND_CONFIG[kind]
  const initialProvider = editing?.Provider || config.providers[0]?.id || ''
  const initialProviderCfg = config.providers.find(item => item.id === initialProvider)
  const initialModels = initialProviderCfg?.models?.length ? initialProviderCfg.models : config.models
  const [name, setName] = useState(editing?.Name || '')
  const [provider, setProvider] = useState(initialProvider)
  const [apiKey, setApiKey] = useState('')
  const [model, setModel] = useState(editing?.Model || initialModels[0] || '')
  const [baseUrl, setBaseUrl] = useState(editing?.BaseUrl || initialProviderCfg?.endpoint || '')
  const [proxy, setProxy] = useState(editing?.Proxy || '')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const activeProviderCfg = config.providers.find(item => item.id === provider)
  const activeModels = activeProviderCfg?.models?.length ? activeProviderCfg.models : config.models
  // Merge provider-detected models into the suggestion list so manually added
  // accounts can reference them without retyping.
  const suggestedModels = Array.from(new Set([...activeModels, ...detectedModels]))
  const modelsId = `media-models-${kind}`

  const save = async () => {
    setSaving(true)
    setError('')
    try {
      if (editing) {
        const request: MediaAccountUpdateReq = { Id: editing.Id, Name: name, Provider: provider, Model: model, BaseUrl: baseUrl, Proxy: proxy }
        if (apiKey) request.ApiKey = apiKey
        await media.updateAccount(client, request)
      } else {
        const request: MediaAccountCreateReq = { Kind: kind, Name: name, Provider: provider, Model: model }
        if (apiKey) request.ApiKey = apiKey
        if (baseUrl) request.BaseUrl = baseUrl
        if (proxy) request.Proxy = proxy
        await media.createAccount(client, request)
      }
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="shell-provider-dialog-overlay">
      <div className="shell-provider-dialog">
        <div className="shell-provider-dialog-header">
          <span>{editing ? t('settings.media.editAccount') : t('settings.media.addAccount')}</span>
          <button type="button" className="shell-provider-dialog-close" onClick={onClose} disabled={saving} title={t('settings.media.cancel')} data-guide-id="settings/media/close-dialog">
            <X size={16} />
          </button>
        </div>
        <div className="shell-provider-dialog-body">
          {error && <p className="media-dialog-error">{error}</p>}
          <label className="shell-provider-field">
            <span className="shell-provider-label">{t('settings.media.provider')}</span>
            <div className="shell-provider-select-wrap">
              <select
                className="shell-provider-select"
                value={provider}
                onChange={(e) => {
                  const p = e.target.value
                  setProvider(p)
                  if (!editing) {
                    const pc = config.providers.find(item => item.id === p)
                    const models = pc?.models?.length ? pc.models : config.models
                    setBaseUrl(pc?.endpoint || '')
                    setModel(models[0] || '')
                  }
                }}
                disabled={!!editing || saving}
                data-guide-id="settings/media/provider"
              >
                {config.providers.map(item => (
                  <option key={item.id} value={item.id}>{t(`settings.media.provider.${item.id}` as any)}</option>
                ))}
              </select>
              <ChevronDown size={14} className="shell-provider-select-icon" />
            </div>
          </label>
          <label className="shell-provider-field">
            <span className="shell-provider-label">{t('settings.media.accountName')}</span>
            <input className="shell-provider-input" type="text" value={name} onChange={event => setName(event.target.value)} placeholder={t('settings.media.accountNamePlaceholder')} disabled={saving} data-guide-id="settings/media/account-name" />
          </label>
          <label className="shell-provider-field">
            <span className="shell-provider-label">{t('settings.media.baseUrl')}</span>
            <input className="shell-provider-input" type="text" value={baseUrl} onChange={event => setBaseUrl(event.target.value)} placeholder="https://maiai.ai/v1" disabled={saving} data-guide-id="settings/media/base-url" />
          </label>
          <label className="shell-provider-field">
            <span className="shell-provider-label">{t('settings.provider.dialog.proxy')}</span>
            <input className="shell-provider-input" type="text" value={proxy} onChange={event => setProxy(event.target.value)} placeholder={t('settings.provider.dialog.proxyPlaceholder')} disabled={saving} data-guide-id="settings/media/proxy" />
          </label>
          <label className="shell-provider-field">
            <span className="shell-provider-label">{t('settings.media.apiKey')}</span>
            <input className="shell-provider-input" type="password" value={apiKey} onChange={event => setApiKey(event.target.value)} placeholder={editing?.HasApiKey ? t('settings.media.apiKeyEditPlaceholder') : t('settings.media.apiKeyPlaceholder')} disabled={saving} data-guide-id="settings/media/api-key" />
          </label>
          <label className="shell-provider-field">
            <span className="shell-provider-label">{t('settings.media.model')}</span>
            <input className="shell-provider-input" list={modelsId} value={model} onChange={event => setModel(event.target.value)} placeholder={t('settings.media.modelPlaceholder')} disabled={saving} data-guide-id="settings/media/model" />
            <datalist id={modelsId}>{suggestedModels.map(item => <option key={item} value={item} />)}</datalist>
          </label>
        </div>
        <div className="shell-provider-dialog-footer">
          <button type="button" className="shell-provider-btn-secondary" onClick={onClose} disabled={saving}>
            {t('settings.media.cancel')}
          </button>
          <button type="button" className="shell-provider-btn-primary" onClick={() => void save()} disabled={saving || !name} data-guide-id="settings/media/save-account">
            {saving ? <Loader2 size={14} className="spin" /> : <Check size={14} />}
            {saving ? t('settings.media.saving') : t('settings.media.save')}
          </button>
        </div>
      </div>
    </div>
  )
}
