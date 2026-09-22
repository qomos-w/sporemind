import { useCallback, useEffect, useMemo, useState } from 'react'
import { Loader2, Shield, Zap, AlertTriangle, X } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as policy from '../../../gen-clients/policy/client'
import * as aimanagerProvider from '../../../gen-clients/aimanager/client'
import type { ModelUnit, PolicyAnswer, PolicyConfigureReq, PolicyDecideResp, PolicyLLMConfig, PolicyQuestion, PolicyStatusResp, Provider } from '../../../gen-clients/system/types'
import { useI18n } from '../../../i18n'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge, Button, Input, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'

const DEMO_STATE = '演示：客户明天有演示但无法登录'
const DEMO_QUESTIONS: Record<string, PolicyQuestion> = {
  urgency: { Type: 'noul', Instructions: 'The situation is urgent' },
  severity: { Type: 'score', Instructions: 'How severe the problem is', Levels: ['Minor', 'Moderate', 'Critical'] },
}

const BACKEND_OPTIONS = [
  { value: 'auto', labelKey: 'settings.policy.backendAuto' },
  { value: 'jev', labelKey: 'settings.policy.backendJev' },
  { value: 'llm', labelKey: 'settings.policy.backendLlm' },
] as const

function unitKey(u: ModelUnit): string {
  return `${u.provider}|${u.model}`
}

function answerValue(a: PolicyAnswer): string {
  if (a.Choice) return a.Choice
  if (typeof a.Score === 'number') return String(a.Score)
  if (typeof a.Noul === 'number') return a.Noul.toFixed(2)
  return '—'
}

export function PolicySettingsPanel() {
  const { t } = useI18n()
  const [status, setStatus] = useState<PolicyStatusResp | null>(null)
  const [llmProviders, setLlmProviders] = useState<Provider[]>([])
  const [backendDraft, setBackendDraft] = useState('auto')
  // undefined = 未改动（沿用已配置值）；null = 显式清除
  const [unitDraft, setUnitDraft] = useState<ModelUnit | null | undefined>(undefined)
  const [apiKey, setApiKey] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState('')
  const [testing, setTesting] = useState(false)
  const [testError, setTestError] = useState('')
  const [result, setResult] = useState<PolicyDecideResp | null>(null)

  const applyStatus = useCallback((st: PolicyStatusResp) => {
    setStatus(st)
    setBackendDraft(st.Backend || 'auto')
    setUnitDraft(st.LLMUnit && st.LLMUnit.model ? { model: st.LLMUnit.model, provider: st.LLMUnit.provider } : null)
  }, [])

  const load = useCallback(async () => {
    setError('')
    try {
      const st = await policy.status(client, {})
      applyStatus(st)
      // Best-effort: aimanager 不可用时仅禁用下拉，不阻塞面板。
      try {
        const resp = await aimanagerProvider.providerList(client)
        setLlmProviders(resp.Items || [])
      } catch {
        setLlmProviders([])
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : t('settings.policy.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [applyStatus, t])

  useEffect(() => { void load() }, [load])

  const unitOptions = useMemo(() => {
    const opts: ModelUnit[] = llmProviders.flatMap(p => (p.Models || []).map(m => ({ model: m.Name, provider: p.Name })))
    const cur = status?.LLMUnit
    if (cur && cur.model && cur.provider && !opts.some(o => o.provider === cur.provider && o.model === cur.model)) {
      opts.unshift({ model: cur.model, provider: cur.provider })
    }
    return opts
  }, [llmProviders, status])

  const selectedKey = unitDraft ? unitKey(unitDraft) : ''

  const handleSave = async () => {
    setSaving(true)
    setError('')
    setSaved(false)
    try {
      const req: PolicyConfigureReq = { Backend: backendDraft }
      if (apiKey) req.Jev = { ApiKey: apiKey }
      let llm: PolicyLLMConfig | undefined
      if (unitDraft) llm = { Unit: unitDraft }
      else if (unitDraft === null && status?.LLMConfigured) llm = {} // 空对象 = 清除 Unit
      if (llm) req.LLM = llm
      const resp = await policy.configure(client, req)
      applyStatus(resp.Status)
      setApiKey('')
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
    } catch (e) {
      setError(e instanceof Error ? e.message : t('settings.policy.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  const canTest = Boolean(apiKey || unitDraft || status?.JevConfigured || status?.LLMConfigured)

  const runTest = async () => {
    setTesting(true)
    setTestError('')
    setResult(null)
    try {
      const resp = await policy.decide(client, { State: DEMO_STATE, Questions: DEMO_QUESTIONS })
      setResult(resp)
    } catch (e) {
      setTestError(e instanceof Error ? e.message : t('settings.policy.testFailed'))
    } finally {
      setTesting(false)
    }
  }

  if (loading) {
    return (
      <FeatureCard icon={<Shield size={16} />} title={t('settings.policy.title')} description={t('settings.policy.desc')}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--text-secondary)', fontSize: 'var(--text-sm)' }}>
          <Loader2 size={16} className="sp-spin" /> {t('settings.policy.loading')}
        </div>
      </FeatureCard>
    )
  }

  return (
    <FeatureCard
      icon={<Shield size={16} />}
      title={t('settings.policy.title')}
      description={t('settings.policy.desc')}
      action={
        <Button type="button" size="sm" onClick={() => void handleSave()} disabled={saving} data-guide-id="settings/policy/save">
          {saving ? <><Loader2 size={14} className="sp-spin" />{t('common.saving')}</> : t('common.save')}
        </Button>
      }
    >
      {error && <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{error}</p>}
      {saved && <p className="text-xs text-muted-foreground">{t('settings.policy.saved')}</p>}

      <div className="flex flex-col gap-4">
        <label className="flex flex-col items-start gap-1.5">
          <span className="text-sm font-medium">{t('settings.policy.backend')}</span>
          <SelectRoot
            value={backendDraft}
            onValueChange={(v) => setBackendDraft(v as string)}
            items={BACKEND_OPTIONS.map(o => ({ value: o.value, label: t(o.labelKey) }))}
          >
            <SelectTrigger data-guide-id="settings/policy/backend"><SelectValue /></SelectTrigger>
            <SelectContent>
              {BACKEND_OPTIONS.map(o => (
                <SelectItem key={o.value} value={o.value} data-guide-id={`settings/policy/backend/${o.value}`}>
                  <SelectItemText>{t(o.labelKey)}</SelectItemText>
                </SelectItem>
              ))}
            </SelectContent>
          </SelectRoot>
        </label>

        <label className="flex flex-col items-start gap-1.5">
          <span className="flex items-center gap-2 text-sm font-medium">
            {t('settings.policy.jevApiKey')}
            {status?.JevConfigured && <Badge variant="secondary" className="text-xs">{t('settings.policy.jevKeySet')}</Badge>}
          </span>
          <Input
            type="password"
            value={apiKey}
            onChange={e => setApiKey(e.target.value)}
            placeholder={status?.JevConfigured ? t('settings.policy.jevApiKeyEditPlaceholder') : t('settings.policy.jevApiKeyPlaceholder')}
            data-guide-id="settings/policy/jev-api-key"
          />
        </label>

        <div className="flex flex-col items-start gap-1.5">
          <span className="flex items-center gap-2 text-sm font-medium">
            {t('settings.policy.llmUnit')}
            {status?.LLMConfigured && status.LLMUnit && !unitDraft && (
              <span className="text-xs text-muted-foreground">{status.LLMUnit.provider} · {status.LLMUnit.model}</span>
            )}
          </span>
          <div className="flex w-full items-center gap-2">
            <div className="min-w-0 flex-1">
              <SelectRoot
                value={selectedKey}
                onValueChange={(v) => {
                  const found = unitOptions.find(o => unitKey(o) === v)
                  if (found) setUnitDraft(found)
                }}
                items={unitOptions.map(o => ({ value: unitKey(o), label: `${o.provider} · ${o.model}` }))}
              >
                <SelectTrigger data-guide-id="settings/policy/llm-unit"><SelectValue placeholder={t('settings.policy.llmUnitPlaceholder')} /></SelectTrigger>
                <SelectContent>
                  {unitOptions.map(o => (
                    <SelectItem key={unitKey(o)} value={unitKey(o)} data-guide-id={`settings/policy/llm-unit/${unitKey(o)}`}>
                      <SelectItemText>{o.provider} · {o.model}</SelectItemText>
                    </SelectItem>
                  ))}
                </SelectContent>
              </SelectRoot>
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setUnitDraft(null)}
              disabled={unitDraft == null}
              title={t('settings.policy.llmUnitClear')}
              data-guide-id="settings/policy/llm-unit-clear"
            >
              <X size={14} />
            </Button>
          </div>
        </div>

        <div className="flex flex-col gap-2 border-t pt-3">
          <div className="flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => void runTest()} disabled={!canTest || testing} data-guide-id="settings/policy/test-run">
              {testing ? <><Loader2 size={14} className="sp-spin" />{t('settings.policy.testing')}</> : <><Zap size={14} />{t('settings.policy.testRun')}</>}
            </Button>
            {!canTest && <span className="text-xs text-muted-foreground">{t('settings.policy.testDisabledHint')}</span>}
          </div>

          {testError && <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{testError}</p>}

          {result && (
            <div className="flex flex-col gap-2 rounded-lg border border-border px-3 py-2" data-guide-id="settings/policy/test-result">
              <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <Badge variant="outline" className="text-xs">{result.Backend}</Badge>
                <span>{t('settings.policy.testLatency')} {result.LatencyMs} ms</span>
                {result.Degraded && (
                  <Badge className="gap-1 bg-yellow-500/15 text-yellow-600 border-yellow-500/40">
                    <AlertTriangle size={12} />{t('settings.policy.degraded')}
                  </Badge>
                )}
              </div>
              {result.Error && <p className="text-sm text-destructive">{result.Error}</p>}
              <div className="flex flex-col gap-1">
                {Object.entries(result.Answers || {}).map(([name, a]) => (
                  <div key={name} className="flex flex-wrap items-center gap-2 text-sm">
                    <span className="font-medium">{name}</span>
                    <span>{answerValue(a)}</span>
                    {typeof a.Confidence === 'number' && <span className="text-xs text-muted-foreground">{t('settings.policy.confidence')} {Math.round(a.Confidence * 100)}%</span>}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
    </FeatureCard>
  )
}
