import { useState, useEffect, useCallback, useRef } from 'react'
import { createPortal } from 'react-dom'
import { Save, Loader2, Volume2, Play, Plus, Pencil, Trash2, Check, X, Mic, Bell, Wand2, FileAudio } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as voice from '../../../gen-clients/voice/client'
import type { VoiceAccountView, VoiceAccountCreateReq, VoiceAccountUpdateReq, VoiceNotifyConfig, VoiceCloneReq } from '../../../gen-clients/system/types'
import {
  NOTIFY_SOUNDS,
  DEFAULT_NOTIFY_CONFIG,
  previewSound,
  previewErrorSound,
  previewInteractionSound,
  previewAllCompleteSound,
  unlockAudio,
  loadNotifyConfig,
  setCachedNotifyConfig,
} from '../../ai/notify-sound'
import { useI18n } from '../../../i18n'
import { FeatureCard, SettingRow } from '../../settings/shadcn/composites'
import { Badge, Button, Field, FieldLabel, Input, Switch, Slider, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'
import './VoiceSettingsPanel.css'

// STT (speech-to-text) providers — recognize() input.
const STT_PROVIDERS = [
  { id: 'openai', needsKey: true },
  { id: 'openai_custom', needsKey: true },
  { id: 'xiaomi_mimo', needsKey: true },
  { id: 'web_speech', needsKey: false },
  { id: 'minimax', needsKey: true },
  { id: 'baidu', needsKey: true },
  { id: 'qwen', needsKey: true },
  { id: 'doubao', needsKey: true },
  { id: 'glm', needsKey: true },
]
const STT_DEFAULT_MODELS: Record<string, string> = {
  glm: 'glm-asr-2512',
  minimax: 'asr-1.0',
  openai: 'whisper-1',
  openai_custom: 'whisper-1',
  xiaomi_mimo: 'mimo-v2.5-asr',
  baidu: '',
  qwen: 'qwen-audio-3.0-asr-flash',
  doubao: 'doubao-seed-asr-2.0',
}

// TTS (text-to-speech) providers — synthesize() output.
const TTS_PROVIDERS = [
  { id: 'glm', needsKey: true },
  { id: 'minimax', needsKey: true },
  { id: 'openai_custom', needsKey: true },
  { id: 'xiaomi_mimo', needsKey: true },
  { id: 'qwen', needsKey: true },
  { id: 'doubao', needsKey: true },
]
const TTS_DEFAULT_MODELS: Record<string, string> = {
  glm: 'glm-tts',
  minimax: 'speech-02-hd',
  openai_custom: 'tts-1',
  xiaomi_mimo: 'mimo-v2.5-tts',
  qwen: 'qwen-audio-3.0-tts-flash',
  doubao: 'doubao-seed-tts',
}

// Per-provider TTS voice candidates. The select lists these; users may also
// type a custom voice name by editing the account if their endpoint supports
// voices outside this list. minimax lists system voices; cloned voice ids are
// free-form and entered as text.
const TTS_VOICES_BY_PROVIDER: Record<string, string[]> = {
  glm: ['tongtong'],
  minimax: [
    'male-qn-qingse',
    'male-qn-jingying',
    'male-qn-badao',
    'male-qn-daxuesheng',
    'male-qn-deyun1',
    'male-shaonv',
    'female-shaonv',
    'female-yujie',
    'female-chengshu',
    'female-tianmei',
    'narrator-female-1',
    'presenter_female',
    'presenter_male',
  ],
  openai_custom: ['alloy', 'echo', 'fable', 'onyx', 'nova', 'shimmer'],
  xiaomi_mimo: ['Chloe', 'default_zh'],
  // Qwen TTS: the qwen-audio-3.0-tts-* models use the "longan*" preset voices;
  // the qwen3-tts-* models use the English name presets. Both families are
  // reachable through the "qwen" provider — pick the voice that matches the
  // chosen model.
  qwen: [
    'longanfengyue',
    'longanlingxin',
    'longanlufeng',
    'longanxiaoxin',
    'longanhuan',
    'longanyuanfei',
    'longanlingxi',
    'xunanchuan',
    'Cherry',
    'Serena',
    'Ethan',
    'Chelsie',
  ],
  // Doubao (Volcengine Seed-TTS): the default resource speaks the 1.0 preset
  // voices; a Seed-TTS 2.0 resource (X-Api-Resource-Id "seed-tts-2.0") speaks
  // the "*_uranus_bigtts" voices. Both families are reachable through the
  // "doubao" provider — pick the voice that matches the chosen model.
  doubao: [
    'zh_female_qingxin',
    'zh_female_vv_uranus_bigtts',
    'zh_female_cancan_uranus_bigtts',
    'zh_female_shuangkuaisisi_uranus_bigtts',
    'zh_female_tianmeixiaoyuan_uranus_bigtts',
    'zh_female_xiaohe_uranus_bigtts',
    'zh_male_m191_uranus_bigtts',
    'en_female_dacey_uranus_bigtts',
    'en_male_tim_uranus_bigtts',
    'zh_male_aojiaobazong_moon_bigtts',
    'zh_female_sajiaonvyou_moon_bigtts',
    'zh_female_gaolengyujie_moon_bigtts',
  ],
}

// Providers that accept a custom OpenAI-compatible base URL.
const PROVIDERS_NEED_BASE_URL = new Set(['openai_custom', 'minimax', 'xiaomi_mimo', 'qwen', 'doubao'])

const LANGUAGES = [
  'zh-CN', 'zh-TW', 'en-US', 'en-GB', 'ja-JP', 'ko-KR', 'fr-FR', 'de-DE', 'es-ES', 'ru-RU',
]

type AccountKind = 'stt' | 'tts'

interface ProviderConfig {
  providers: { id: string; needsKey: boolean }[]
  defaultModels: Record<string, string>
}

const KIND_CONFIG: Record<AccountKind, ProviderConfig> = {
  stt: { providers: STT_PROVIDERS, defaultModels: STT_DEFAULT_MODELS },
  tts: { providers: TTS_PROVIDERS, defaultModels: TTS_DEFAULT_MODELS },
}

export function VoiceSettingsPanel() {
  return (
    <div className="flex flex-col gap-6">
      <AccountSection kind="stt" />
      <AccountSection kind="tts" />
      <MiniMaxVoiceLab />
      <VoiceNotifySettings />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Account list section (shared by STT and TTS)
// ---------------------------------------------------------------------------

function AccountSection({ kind }: { kind: AccountKind }) {
  const { t } = useI18n()
  const [accounts, setAccounts] = useState<VoiceAccountView[]>([])
  const [activeId, setActiveId] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<VoiceAccountView | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const resp = await voice.listAccounts(client, { Kind: kind })
      setAccounts(resp.Items || [])
      setActiveId(resp.ActiveId || '')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [kind])

  useEffect(() => {
    void load()
  }, [load])

  const handleActivate = async (id: string) => {
    if (id === activeId) return
    setError('')
    try {
      const resp = await voice.activateAccount(client, { Kind: kind, Id: id })
      setActiveId(resp.ActiveId)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const handleDelete = async (id: string) => {
    if (!window.confirm(t('settings.voice.deleteAccountConfirm'))) return
    setError('')
    try {
      await voice.deleteAccount(client, { Id: id })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const openCreate = () => {
    setEditing(null)
    setDialogOpen(true)
  }

  const openEdit = (acc: VoiceAccountView) => {
    setEditing(acc)
    setDialogOpen(true)
  }

  return (
    <FeatureCard
      icon={kind === 'stt' ? <Mic size={16} /> : <Volume2 size={16} />}
      title={kind === 'stt' ? t('settings.voice.inputTitle') : t('settings.voice.synthesisTitle')}
      description={kind === 'stt' ? t('settings.voice.inputDesc') : t('settings.voice.synthesisDesc')}
      action={
        <Button variant="outline" size="sm" onClick={openCreate} data-guide-id={`settings/voice/${kind}/add`}>
          <Plus />
          {t('settings.voice.addAccount')}
        </Button>
      }
    >
      {error && <p className="text-sm text-destructive">{error}</p>}

      {loading ? (
        <p className="text-sm text-muted-foreground">{t('settings.voice.loading')}</p>
      ) : accounts.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('settings.voice.accountEmpty')}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {accounts.map(acc => (
            <div
              key={acc.Id}
              role="button"
              tabIndex={0}
              className={`flex items-center gap-2 rounded-lg border px-3 py-2 cursor-pointer transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50${acc.Id === activeId ? ' border-primary bg-primary/5' : ' border-border'}`}
              onClick={() => void handleActivate(acc.Id)}
              onKeyDown={(e) => {
                if (e.target !== e.currentTarget) return
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  void handleActivate(acc.Id)
                }
              }}
              title={t('settings.voice.setActive')}
              data-guide-id={`settings/voice/${kind}/account-${acc.Id}`}
            >
              <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
                <span className="flex items-center gap-2 text-sm font-medium">
                  {acc.Name}
                  {acc.Id === activeId && (
                    <Badge variant="secondary" className="gap-1">
                      <Check />
                      {t('settings.voice.activeAccount')}
                    </Badge>
                  )}
                </span>
                <span className="text-xs text-muted-foreground">
                  {t(`settings.voice.provider.${acc.Provider}` as any)} · {acc.HasApiKey ? t('settings.voice.keySet') : t('settings.voice.noKey')}
                </span>
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={(e) => { e.stopPropagation(); openEdit(acc) }}
                title={t('settings.voice.editAccount')}
                data-guide-id={`settings/voice/${kind}/edit-${acc.Id}`}
              >
                <Pencil />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={(e) => { e.stopPropagation(); void handleDelete(acc.Id) }}
                title={t('settings.voice.deleteAccount')}
                data-guide-id={`settings/voice/${kind}/delete-${acc.Id}`}
              >
                <Trash2 />
              </Button>
            </div>
          ))}
        </div>
      )}

      {dialogOpen && createPortal(
        <AccountDialog
          kind={kind}
          editing={editing}
          onClose={() => setDialogOpen(false)}
          onSaved={() => {
            setDialogOpen(false)
            void load()
          }}
        />,
        document.body,
      )}
    </FeatureCard>
  )
}

// ---------------------------------------------------------------------------
// Add / edit account dialog
// ---------------------------------------------------------------------------

interface AccountDialogProps {
  kind: AccountKind
  editing: VoiceAccountView | null
  onClose: () => void
  onSaved: () => void
}

function AccountDialog({ kind, editing, onClose, onSaved }: AccountDialogProps) {
  const { t } = useI18n()
  const cfg = KIND_CONFIG[kind]
  const providers = cfg.providers
  const defaultModels = cfg.defaultModels
  const initialProvider = editing?.Provider || providers[0]?.id || ''

  const [name, setName] = useState(editing?.Name || '')
  const [provider, setProvider] = useState(initialProvider)
  const [apiKey, setApiKey] = useState('')
  const [model, setModel] = useState(editing?.Model || '')
  const [voiceName, setVoiceName] = useState(editing?.Voice || '')
  const [language, setLanguage] = useState(editing?.Language || 'zh-CN')
  const [baseUrl, setBaseUrl] = useState(editing?.BaseUrl || '')
  const [proxy, setProxy] = useState(editing?.Proxy || '')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const providerInfo = providers.find(p => p.id === provider)
  const needsKey = providerInfo?.needsKey ?? false
  const isTTS = kind === 'tts'
  const needsBaseUrl = PROVIDERS_NEED_BASE_URL.has(provider)

  const handleSave = async () => {
    setSaving(true)
    setError('')
    try {
      if (editing) {
        const req: VoiceAccountUpdateReq = { Id: editing.Id }
        if (name) req.Name = name
        if (provider) req.Provider = provider
        if (apiKey) req.ApiKey = apiKey
        if (model) req.Model = model
        if (isTTS && voiceName) req.Voice = voiceName
        if (language) req.Language = language
        if (baseUrl) req.BaseUrl = baseUrl
        if (proxy) req.Proxy = proxy
        await voice.updateAccount(client, req)
      } else {
        const req: VoiceAccountCreateReq = {
          Kind: kind,
          Name: name,
          Provider: provider,
        }
        if (apiKey) req.ApiKey = apiKey
        if (model) req.Model = model
        if (isTTS && voiceName) req.Voice = voiceName
        if (language) req.Language = language
        if (baseUrl) req.BaseUrl = baseUrl
        if (proxy) req.Proxy = proxy
        await voice.createAccount(client, req)
      }
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="voice-dialog-overlay" onClick={onClose}>
      <div className="voice-dialog" onClick={(e) => e.stopPropagation()}>
        <div className="voice-dialog-header">
          <h4 className="text-sm font-medium">
            {editing ? t('settings.voice.editAccount') : t('settings.voice.addAccount')}
          </h4>
          <button type="button" className="voice-dialog-close" onClick={onClose}>
            <X size={16} />
          </button>
        </div>

        {error && <p className="text-sm text-destructive">{error}</p>}

        <div className="voice-dialog-body">
          <Field>
            <FieldLabel>{t('settings.voice.accountName')}</FieldLabel>
            <Input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('settings.voice.accountNamePlaceholder')}
              data-guide-id="settings/voice/account-name"
            />
          </Field>

          <Field>
            <FieldLabel>{t('settings.voice.provider')}</FieldLabel>
            <SelectRoot
              value={provider}
              onValueChange={(v) => {
                const p = v as string
                setProvider(p)
                if (!editing) setModel(defaultModels[p] || '')
              }}
              items={providers.map(p => ({ value: p.id, label: t(`settings.voice.provider.${p.id}` as any) }))}
            >
              <SelectTrigger data-guide-id="settings/voice/provider">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {providers.map(p => (
                  <SelectItem key={p.id} value={p.id} data-guide-id={`settings/voice/provider/${p.id}`}>
                    <SelectItemText>{t(`settings.voice.provider.${p.id}` as any)}</SelectItemText>
                  </SelectItem>
                ))}
              </SelectContent>
            </SelectRoot>
          </Field>

          {needsBaseUrl && (
            <Field>
              <FieldLabel>{t('settings.voice.baseUrl')}</FieldLabel>
              <Input
                type="text"
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                placeholder={t('settings.voice.baseUrlPlaceholder')}
                data-guide-id="settings/voice/base-url"
              />
            </Field>
          )}

          <Field>
            <FieldLabel>{t('settings.provider.dialog.proxy')}</FieldLabel>
            <Input
              type="text"
              value={proxy}
              onChange={(e) => setProxy(e.target.value)}
              placeholder={t('settings.provider.dialog.proxyPlaceholder')}
              data-guide-id="settings/voice/proxy"
            />
          </Field>

          {needsKey && (
            <Field>
              <FieldLabel>{t('settings.voice.apiKey')}</FieldLabel>
              <Input
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder={editing?.HasApiKey ? t('settings.voice.apiKeyEditPlaceholder') : (provider === 'baidu' ? t('settings.voice.baiduApiKeyPlaceholder') : t('settings.voice.apiKeyPlaceholder'))}
                data-guide-id="settings/voice/api-key"
              />
            </Field>
          )}

          {needsKey && (isTTS || provider !== 'baidu') && (
            <Field>
              <FieldLabel>{t('settings.voice.model')}</FieldLabel>
              <Input
                type="text"
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder={defaultModels[provider] || t('settings.voice.modelPlaceholder')}
                data-guide-id="settings/voice/model"
              />
            </Field>
          )}

          {isTTS && provider !== 'minimax' && (
            <Field>
              <FieldLabel>{t('settings.voice.ttsVoice')}</FieldLabel>
              <SelectRoot
                value={voiceName}
                onValueChange={(v) => setVoiceName(v as string)}
                items={[
                  { value: '', label: t('settings.voice.ttsVoiceDefault') },
                  ...(TTS_VOICES_BY_PROVIDER[provider] || []).map(v => ({ value: v, label: v })),
                ]}
              >
                <SelectTrigger data-guide-id="settings/voice/tts-voice">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="" data-guide-id="settings/voice/tts-voice/default">
                    <SelectItemText>{t('settings.voice.ttsVoiceDefault')}</SelectItemText>
                  </SelectItem>
                  {(TTS_VOICES_BY_PROVIDER[provider] || []).map(v => (
                    <SelectItem key={v} value={v} data-guide-id={`settings/voice/tts-voice/${v}`}>
                      <SelectItemText>{v}</SelectItemText>
                    </SelectItem>
                  ))}
                </SelectContent>
              </SelectRoot>
            </Field>
          )}

          {isTTS && provider === 'minimax' && (
            <Field>
              <FieldLabel>{t('settings.voice.ttsVoice')}</FieldLabel>
              <Input
                type="text"
                value={voiceName}
                onChange={(e) => setVoiceName(e.target.value)}
                placeholder={t('settings.voice.ttsVoiceMinimaxPlaceholder')}
                list="minimax-voice-options"
                data-guide-id="settings/voice/tts-voice"
              />
              <datalist id="minimax-voice-options">
                {(TTS_VOICES_BY_PROVIDER.minimax || []).map(v => (
                  <option key={v} value={v} />
                ))}
              </datalist>
            </Field>
          )}

          {!isTTS && (
            <Field>
              <FieldLabel>{t('settings.voice.language')}</FieldLabel>
              <SelectRoot
                value={language}
                onValueChange={(v) => setLanguage(v as string)}
                items={LANGUAGES.map(l => ({ value: l, label: t(`settings.voice.language.${l}` as any) }))}
              >
                <SelectTrigger data-guide-id="settings/voice/language">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {LANGUAGES.map(l => (
                    <SelectItem key={l} value={l} data-guide-id={`settings/voice/language/${l}`}>
                      <SelectItemText>{t(`settings.voice.language.${l}` as any)}</SelectItemText>
                    </SelectItem>
                  ))}
                </SelectContent>
              </SelectRoot>
            </Field>
          )}
        </div>

        <div className="voice-dialog-footer">
          <Button type="button" variant="outline" size="sm" onClick={onClose}>
            {t('settings.voice.cancel')}
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={() => void handleSave()}
            disabled={saving || (!editing && !name)}
            data-guide-id="settings/voice/save-account"
          >
            {saving ? <Loader2 size={14} className="spin" /> : <Save size={14} />}
            {t('common.save')}
          </Button>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// MiniMax voice lab: clone a voice from a sample or design one from a text
// description. Both run against a tts minimax account and produce a voice id
// that can be applied to that account's Voice field.
// ---------------------------------------------------------------------------

function MiniMaxVoiceLab() {
  const { t } = useI18n()
  const [accounts, setAccounts] = useState<VoiceAccountView[]>([])

  useEffect(() => {
    voice.listAccounts(client, { Kind: 'tts' })
      .then(resp => setAccounts((resp.Items || []).filter(a => a.Provider === 'minimax')))
      .catch(() => {})
  }, [])

  if (accounts.length === 0) return null

  return (
    <FeatureCard icon={<Wand2 size={16} />} title={t('settings.voice.labTitle')} description={t('settings.voice.labDesc')}>
      <MiniMaxCloneCard accounts={accounts} />
      <MiniMaxDesignCard accounts={accounts} />
    </FeatureCard>
  )
}

function LabAccountPicker({ accounts, value, onChange }: { accounts: VoiceAccountView[]; value: string; onChange: (id: string) => void }) {
  const { t } = useI18n()
  return (
    <Field>
      <FieldLabel>{t('settings.voice.labAccount')}</FieldLabel>
      <SelectRoot value={value} onValueChange={(v) => onChange(v as string)} items={accounts.map(a => ({ value: a.Id, label: a.Name }))}>
        <SelectTrigger data-guide-id="settings/voice/lab/account">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {accounts.map(a => (
            <SelectItem key={a.Id} value={a.Id}>
              <SelectItemText>{a.Name}</SelectItemText>
            </SelectItem>
          ))}
        </SelectContent>
      </SelectRoot>
    </Field>
  )
}

function useLabAccount(accounts: VoiceAccountView[]) {
  const [selected, setSelected] = useState(accounts[0]?.Id || '')
  const effective = accounts.some(a => a.Id === selected) ? selected : (accounts[0]?.Id || '')
  return { accountId: effective, setAccountId: setSelected }
}

function LabFileField({ label, accept, file, onChange, guideId }: {
  label: string
  accept: string
  file: File | null
  onChange: (f: File | null) => void
  guideId: string
}) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      <label className="flex items-center gap-2 cursor-pointer" data-guide-id={guideId}>
        <span className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs text-muted-foreground hover:bg-muted/50">
          <FileAudio size={14} />
        </span>
        <span className="truncate text-sm text-muted-foreground">{file ? file.name : '—'}</span>
        <input
          type="file"
          accept={accept}
          className="hidden"
          onChange={(e) => onChange(e.target.files?.[0] || null)}
        />
      </label>
    </Field>
  )
}

function MiniMaxCloneCard({ accounts }: { accounts: VoiceAccountView[] }) {
  const { t } = useI18n()
  const { accountId, setAccountId } = useLabAccount(accounts)
  const [file, setFile] = useState<File | null>(null)
  const [promptFile, setPromptFile] = useState<File | null>(null)
  const [promptText, setPromptText] = useState('')
  const [previewText, setPreviewText] = useState('')
  const [noise, setNoise] = useState(false)
  const [volume, setVolume] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<{ voiceId: string; demoUrl: string } | null>(null)
  const [applied, setApplied] = useState(false)

  const run = async () => {
    if (!file || !accountId) return
    setBusy(true)
    setError('')
    setResult(null)
    setApplied(false)
    try {
      const req: VoiceCloneReq = {
        AccountId: accountId,
        AudioData: new Uint8Array(await file.arrayBuffer()),
        AudioName: file.name,
        PreviewText: previewText || undefined,
        NeedNoiseReduction: noise || undefined,
        NeedVolumeNormalization: volume || undefined,
      }
      if (promptFile) {
        req.PromptAudioData = new Uint8Array(await promptFile.arrayBuffer())
        req.PromptAudioName = promptFile.name
      }
      if (promptText) req.PromptText = promptText
      const resp = await voice.clone(client, req)
      setResult({ voiceId: resp.VoiceId, demoUrl: resp.DemoAudioUrl || '' })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const applyVoice = async () => {
    if (!result || !accountId) return
    setError('')
    try {
      await voice.updateAccount(client, { Id: accountId, Voice: result.voiceId })
      setApplied(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border p-3" data-guide-id="settings/voice/lab/clone">
      <h5 className="text-sm font-medium">{t('settings.voice.labCloneTitle')}</h5>
      {error && <p className="text-sm text-destructive">{error}</p>}
      <LabAccountPicker accounts={accounts} value={accountId} onChange={setAccountId} />
      <LabFileField
        label={t('settings.voice.labCloneAudio')}
        accept="audio/*"
        file={file}
        onChange={setFile}
        guideId="settings/voice/lab/clone-audio"
      />
      <LabFileField
        label={t('settings.voice.labClonePromptAudio')}
        accept="audio/*"
        file={promptFile}
        onChange={setPromptFile}
        guideId="settings/voice/lab/clone-prompt-audio"
      />
      <Field>
        <FieldLabel>{t('settings.voice.labPromptText')}</FieldLabel>
        <Input type="text" value={promptText} onChange={(e) => setPromptText(e.target.value)} data-guide-id="settings/voice/lab/clone-prompt-text" />
      </Field>
      <Field>
        <FieldLabel>{t('settings.voice.labPreviewText')}</FieldLabel>
        <Input type="text" value={previewText} onChange={(e) => setPreviewText(e.target.value)} data-guide-id="settings/voice/lab/clone-preview-text" />
      </Field>
      <SettingRow
        label={t('settings.voice.labNoiseReduction')}
        control={<Switch checked={noise} onCheckedChange={setNoise} data-guide-id="settings/voice/lab/clone-noise" />}
      />
      <SettingRow
        label={t('settings.voice.labVolumeNormalization')}
        control={<Switch checked={volume} onCheckedChange={setVolume} data-guide-id="settings/voice/lab/clone-volume" />}
      />
      <div>
        <Button variant="outline" size="sm" disabled={busy || !file || !accountId} onClick={() => void run()} data-guide-id="settings/voice/lab/clone-run">
          {busy ? <Loader2 size={14} className="spin" /> : <Wand2 size={14} />}
          {t('settings.voice.labCloneButton')}
        </Button>
      </div>
      {result && (
        <div className="flex flex-col gap-1 rounded-md bg-muted/40 p-2 text-sm">
          <span className="break-all">
            {t('settings.voice.labVoiceIdLabel')}: <code>{result.voiceId}</code>
          </span>
          {result.demoUrl && (
            <a className="text-primary underline" href={result.demoUrl} target="_blank" rel="noreferrer">
              {t('settings.voice.labDemoAudio')}
            </a>
          )}
          <Button variant="outline" size="sm" className="self-start" disabled={applied} onClick={() => void applyVoice()} data-guide-id="settings/voice/lab/clone-apply">
            {applied ? <Check size={14} /> : <Volume2 size={14} />}
            {applied ? t('settings.voice.labVoiceApplied') : t('settings.voice.labUseVoice')}
          </Button>
        </div>
      )}
    </div>
  )
}

function MiniMaxDesignCard({ accounts }: { accounts: VoiceAccountView[] }) {
  const { t } = useI18n()
  const { accountId, setAccountId } = useLabAccount(accounts)
  const [prompt, setPrompt] = useState('')
  const [previewText, setPreviewText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<{ voiceId: string; audioUrl: string } | null>(null)
  const [applied, setApplied] = useState(false)

  useEffect(() => {
    return () => {
      if (result) URL.revokeObjectURL(result.audioUrl)
    }
  }, [result])

  const run = async () => {
    if (!prompt.trim() || !previewText.trim() || !accountId) return
    setBusy(true)
    setError('')
    setResult(null)
    setApplied(false)
    try {
      const resp = await voice.design(client, { AccountId: accountId, Prompt: prompt, PreviewText: previewText })
      setResult({
        voiceId: resp.VoiceId,
        audioUrl: URL.createObjectURL(new Blob([resp.TrialAudio], { type: 'audio/mpeg' })),
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const applyVoice = async () => {
    if (!result || !accountId) return
    setError('')
    try {
      await voice.updateAccount(client, { Id: accountId, Voice: result.voiceId })
      setApplied(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border p-3" data-guide-id="settings/voice/lab/design">
      <h5 className="text-sm font-medium">{t('settings.voice.labDesignTitle')}</h5>
      {error && <p className="text-sm text-destructive">{error}</p>}
      <LabAccountPicker accounts={accounts} value={accountId} onChange={setAccountId} />
      <Field>
        <FieldLabel>{t('settings.voice.labDesignPrompt')}</FieldLabel>
        <Input type="text" value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder={t('settings.voice.labDesignPromptPlaceholder')} data-guide-id="settings/voice/lab/design-prompt" />
      </Field>
      <Field>
        <FieldLabel>{t('settings.voice.labPreviewText')}</FieldLabel>
        <Input type="text" value={previewText} onChange={(e) => setPreviewText(e.target.value)} data-guide-id="settings/voice/lab/design-preview" />
      </Field>
      <div>
        <Button variant="outline" size="sm" disabled={busy || !prompt.trim() || !previewText.trim()} onClick={() => void run()} data-guide-id="settings/voice/lab/design-run">
          {busy ? <Loader2 size={14} className="spin" /> : <Wand2 size={14} />}
          {t('settings.voice.labDesignButton')}
        </Button>
      </div>
      {result && (
        <div className="flex flex-col gap-1 rounded-md bg-muted/40 p-2 text-sm">
          <span className="break-all">
            {t('settings.voice.labVoiceIdLabel')}: <code>{result.voiceId}</code>
          </span>
          <audio controls src={result.audioUrl} className="h-8 w-full" />
          <Button variant="outline" size="sm" className="self-start" disabled={applied} onClick={() => void applyVoice()} data-guide-id="settings/voice/lab/design-apply">
            {applied ? <Check size={14} /> : <Volume2 size={14} />}
            {applied ? t('settings.voice.labVoiceApplied') : t('settings.voice.labUseVoice')}
          </Button>
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Notification sound settings (task-completion alerts)
// ---------------------------------------------------------------------------

function VoiceNotifySettings() {
  const { t } = useI18n()
  const [cfg, setCfg] = useState<VoiceNotifyConfig>(DEFAULT_NOTIFY_CONFIG)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const cfgRef = useRef(cfg)
  cfgRef.current = cfg
  const saveSeqRef = useRef(0)

  // Persist the latest config to the voice actor. A monotonic sequence guard
  // ignores responses from saves superseded by a newer edit so an in-flight
  // response can't clobber a later change.
  const persist = useCallback(async (next: VoiceNotifyConfig) => {
    const seq = ++saveSeqRef.current
    setError('')
    try {
      const resp = await voice.setNotifyConfig(client, next)
      if (seq !== saveSeqRef.current) return
      if (resp.Config) {
        cfgRef.current = resp.Config
        setCfg(resp.Config)
        setCachedNotifyConfig(resp.Config)
      }
    } catch (err) {
      if (seq !== saveSeqRef.current) return
      setError(err instanceof Error ? err.message : String(err))
    }
  }, [])

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const c = await loadNotifyConfig(true)
      cfgRef.current = c
      setCfg(c)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // Apply a patch and persist immediately — used by switches, dropdowns,
  // file selection and the volume slider's commit (drag stop).
  const update = useCallback((patch: Partial<VoiceNotifyConfig>) => {
    const next = { ...cfgRef.current, ...patch }
    cfgRef.current = next
    setCfg(next)
    void persist(next)
  }, [persist])

  // Update local state only — used while the volume slider is being dragged
  // so the UI tracks the thumb without firing a save on every pixel.
  const updateLocal = useCallback((patch: Partial<VoiceNotifyConfig>) => {
    const next = { ...cfgRef.current, ...patch }
    cfgRef.current = next
    setCfg(next)
  }, [])

  if (loading) {
    return <p className="text-sm text-muted-foreground">{t('settings.voice.loading')}</p>
  }

  return (
    <FeatureCard icon={<Bell size={16} />} title={t('settings.voice.notifyTitle')} description={t('settings.voice.notifyDesc')}>
      {error && <p className="text-sm text-destructive">{error}</p>}

      <SettingRow
        label={t('settings.voice.notifyEnabled')}
        description={t('settings.voice.notifyEnabledDesc')}
        control={
          <Switch
            checked={cfg.Enabled}
            onCheckedChange={(v) => update({ Enabled: v })}
            data-guide-id="settings/voice/notify-enabled"
          />
        }
      />

      <SoundSelectRow
        id="complete"
        label={t('settings.voice.notifyOnComplete')}
        soundValue={cfg.Sound}
        onSoundChange={(s) => update({ Sound: s })}
        customValue={cfg.CustomSoundComplete}
        onCustomChange={(v) => update({ CustomSoundComplete: v })}
        volume={cfg.VolumeComplete}
        globalVolume={cfg.Volume}
        onVolumeChange={(v) => updateLocal({ VolumeComplete: v })}
        onVolumeCommit={(v) => update({ VolumeComplete: v })}
        onPreview={() => {
          unlockAudio()
          const vol = cfg.VolumeComplete || cfg.Volume
          previewSound(cfg.Sound || 'chime', vol, cfg.CustomSoundComplete || undefined)
        }}
        t={t}
        enabled={cfg.OnComplete}
        onEnabledChange={(v) => update({ OnComplete: v })}
      />

      <SoundSelectRow
        id="error"
        label={t('settings.voice.notifyOnError')}
        soundValue={cfg.SoundError || ''}
        onSoundChange={(s) => update({ SoundError: s })}
        customValue={cfg.CustomSoundError}
        onCustomChange={(v) => update({ CustomSoundError: v })}
        volume={cfg.VolumeError}
        globalVolume={cfg.Volume}
        onVolumeChange={(v) => updateLocal({ VolumeError: v })}
        onVolumeCommit={(v) => update({ VolumeError: v })}
        onPreview={() => {
          unlockAudio()
          const vol = cfg.VolumeError || cfg.Volume
          previewErrorSound(vol, cfg.SoundError || '', cfg.CustomSoundError || undefined)
        }}
        t={t}
        enabled={cfg.OnError}
        onEnabledChange={(v) => update({ OnError: v })}
      />

      <SoundSelectRow
        id="interaction"
        label={t('settings.voice.notifyOnInteraction')}
        soundValue={cfg.SoundInteraction || ''}
        onSoundChange={(s) => update({ SoundInteraction: s })}
        customValue={cfg.CustomSoundInteraction}
        onCustomChange={(v) => update({ CustomSoundInteraction: v })}
        volume={cfg.VolumeInteraction}
        globalVolume={cfg.Volume}
        onVolumeChange={(v) => updateLocal({ VolumeInteraction: v })}
        onVolumeCommit={(v) => update({ VolumeInteraction: v })}
        onPreview={() => {
          unlockAudio()
          const vol = cfg.VolumeInteraction || cfg.Volume
          previewInteractionSound(vol, cfg.SoundInteraction || '', cfg.CustomSoundInteraction || undefined)
        }}
        t={t}
        enabled={cfg.OnInteraction}
        onEnabledChange={(v) => update({ OnInteraction: v })}
      />

      <SoundSelectRow
        id="all-complete"
        label={t('settings.voice.notifyOnAllComplete')}
        soundValue={cfg.SoundAllComplete || ''}
        onSoundChange={(s) => update({ SoundAllComplete: s })}
        customValue={cfg.CustomSoundAllComplete}
        onCustomChange={(v) => update({ CustomSoundAllComplete: v })}
        volume={cfg.VolumeAllComplete}
        globalVolume={cfg.Volume}
        onVolumeChange={(v) => updateLocal({ VolumeAllComplete: v })}
        onVolumeCommit={(v) => update({ VolumeAllComplete: v })}
        onPreview={() => {
          unlockAudio()
          const vol = cfg.VolumeAllComplete || cfg.Volume
          previewAllCompleteSound(vol, cfg.SoundAllComplete || '', cfg.CustomSoundAllComplete || undefined)
        }}
        t={t}
        enabled={cfg.OnAllComplete}
        onEnabledChange={(v) => update({ OnAllComplete: v })}
      />
    </FeatureCard>
  )
}

// ---------------------------------------------------------------------------
// Sound select row — preset dropdown + custom audio file option
// ---------------------------------------------------------------------------

interface SoundSelectRowProps {
  id: string
  label: string
  soundValue: string
  onSoundChange: (s: string) => void
  customValue: string | undefined
  onCustomChange: (v: string) => void
  volume: number | undefined
  globalVolume: number
  onVolumeChange: (v: number) => void
  onVolumeCommit: (v: number) => void
  onPreview: () => void
  t: ReturnType<typeof useI18n>['t']
  enabled?: boolean
  onEnabledChange?: (v: boolean) => void
}

function SoundSelectRow({
  id, label, soundValue, onSoundChange, customValue, onCustomChange, volume, globalVolume, onVolumeChange, onVolumeCommit, onPreview, t,
  enabled, onEnabledChange,
}: SoundSelectRowProps) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const hasCustom = !!customValue
  const effectiveVolume = volume && volume > 0 ? volume : globalVolume
  const isEnabled = enabled ?? true

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      onCustomChange(reader.result as string)
    }
    reader.readAsDataURL(file)
    e.target.value = ''
  }

  const handleSelectChange = (val: string) => {
    if (val === '__custom_file__' || val === '__select_file__') {
      fileInputRef.current?.click()
    } else if (val === '__builtin_default__') {
      onCustomChange('')
      onSoundChange('')
    } else {
      onSoundChange(val)
    }
  }

  const selectValue = hasCustom ? '__custom_file__' : (soundValue || '__builtin_default__')

  const soundItems = [
    { value: '__builtin_default__', label: t('settings.voice.builtinDefault') },
    ...NOTIFY_SOUNDS.map(s => ({ value: s.id, label: t(`settings.voice.sound.${s.id}` as any) })),
    ...(hasCustom ? [{ value: '__custom_file__', label: t('settings.voice.customAudioSelected') }] : []),
    { value: '__select_file__', label: t('settings.voice.selectAudioFile') },
  ]

  return (
    <div className={`flex flex-col gap-1.5${isEnabled ? '' : ' opacity-50'}`}>
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm font-medium">{label}</span>
        {onEnabledChange && (
          <Switch
            checked={isEnabled}
            onCheckedChange={(v) => onEnabledChange(v)}
            data-guide-id={`settings/voice/notify-${id}-enabled`}
          />
        )}
      </div>
      <div className="flex items-center gap-2">
        <SelectRoot value={selectValue} onValueChange={(v) => handleSelectChange(v as string)} items={soundItems}>
          <SelectTrigger className="w-40" data-guide-id={`settings/voice/notify-${id}-sound`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {soundItems.map(item => (
              <SelectItem key={item.value} value={item.value} data-guide-id={`settings/voice/notify-${id}-sound/${item.value}`}>
                <SelectItemText>{item.label}</SelectItemText>
              </SelectItem>
            ))}
          </SelectContent>
        </SelectRoot>
        <Button
          variant="outline"
          size="icon-sm"
          onClick={onPreview}
          title={t('settings.voice.notifyPreview')}
          data-guide-id={`settings/voice/notify-${id}-preview`}
        >
          <Play />
        </Button>
        <Volume2 className="size-4 shrink-0 text-muted-foreground" />
        <Slider
          min={0} max={100} step={1} value={effectiveVolume}
          onValueChange={(v) => onVolumeChange(Array.isArray(v) ? v[0] : v)}
          onValueCommitted={(v) => onVolumeCommit(Array.isArray(v) ? v[0] : v)}
          className="flex-1"
          aria-label={t('settings.voice.notifyVolume')}
          data-guide-id={`settings/voice/notify-${id}-volume`}
        />
        <span className="w-9 shrink-0 text-right text-xs text-muted-foreground">{effectiveVolume}%</span>
        {hasCustom && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onCustomChange('')}
            data-guide-id={`settings/voice/notify-${id}-clear-custom`}
          >
            {t('settings.voice.clearCustomAudio')}
          </Button>
        )}
      </div>
      <input
        ref={fileInputRef}
        type="file"
        accept="audio/*"
        style={{ display: 'none' }}
        onChange={handleFileSelect}
      />
    </div>
  )
}
