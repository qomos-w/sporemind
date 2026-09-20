import { useState, useEffect, useLayoutEffect, useRef, useCallback } from 'react'
import { Loader2, AlertCircle, RotateCcw, Sparkles, ExternalLink, ArrowRight } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as aimanagerProvider from '../../../gen-clients/aimanager/client'
import type { ProviderModel } from '../../../gen-clients/system/types'
import { Modal } from '../../components/Modal'
import { useProviderConfigs } from '../hooks/useProviderConfigs'
import { PRESETS, effectiveKind, presetLabel, presetDescription } from './providerPresets'
import { ProviderIcon } from './providerIcons'
import { apiKeyFailure } from './apiKeyValidation'
import { DEFAULT_CONTEXT, MODEL_CONTEXT_DEFAULTS, lookupModelDefault } from '../../../../../const/modelContextDefaults'
import { useI18n } from '../../../i18n'
import './ProviderOnboardingModal.css'

const TOUR_TOTAL = 4

interface ProviderOnboardingModalProps {
  onDismiss: () => void
}

export function ProviderOnboardingModal({ onDismiss }: ProviderOnboardingModalProps) {
  const { t } = useI18n()
  const { providers, loading, configure } = useProviderConfigs()

  const [presetId, setPresetId] = useState('')
  const [endpoint, setEndpoint] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [maxConcurrency, setMaxConcurrency] = useState<number | undefined>(undefined)
  const [fetchedModels, setFetchedModels] = useState<ProviderModel[] | null>(null)
  const [checked, setChecked] = useState<Record<string, boolean>>({})
  const [fetching, setFetching] = useState(false)
  const [fetchError, setFetchError] = useState('')
  const [manualText, setManualText] = useState('')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [tourStep, setTourStep] = useState<number | null>(null)
  const [bubblePos, setBubblePos] = useState<{ top: number; left: number } | null>(null)
  const [phase, setPhase] = useState<'intro' | 'form'>('intro')

  const bodyRef = useRef<HTMLDivElement>(null)
  const presetRef = useRef<HTMLDivElement>(null)
  const endpointRef = useRef<HTMLDivElement>(null)
  const keyRef = useRef<HTMLDivElement>(null)
  const modelsRef = useRef<HTMLDivElement>(null)
  const fetchSeq = useRef(0)
  const targetRefs = [presetRef, endpointRef, keyRef, modelsRef]

  const endpointValid = /^https?:\/\/.+/.test(endpoint.trim())
  const preset = PRESETS.find(p => p.id === presetId)
  const keyFailure = apiKeyFailure(apiKey)
  const manualModels = manualText.split(/[,，\n]/).map(s => s.trim()).filter(Boolean)
  const selectedFetched = (fetchedModels ?? []).filter(m => checked[m.Name] !== false)
  const manualMode = fetchedModels === null && fetchError !== ''
  const modelsToSave: ProviderModel[] = manualMode
    ? manualModels.map(name => ({ Name: name, MaxContextLength: lookupModelDefault(name, MODEL_CONTEXT_DEFAULTS) ?? DEFAULT_CONTEXT, MaxTokens: 16384 }))
    : selectedFetched
  const canFetch = endpointValid && !fetching && keyFailure === undefined
  const canFinish = endpointValid && modelsToSave.length > 0 && !saving && keyFailure === undefined

  const doFetch = useCallback(async (ep: string, key: string) => {
    const seq = ++fetchSeq.current
    setFetching(true)
    setFetchError('')
    try {
      const resp = await aimanagerProvider.providerFetchModels(client, {
        Name: effectiveKind('auto', ep),
        Endpoint: ep.trim(),
        Kind: effectiveKind('auto', ep),
        AuthToken: key || undefined,
      })
      if (seq !== fetchSeq.current) return
      if (resp.Models.length === 0) {
        setFetchError(t('settings.provider.fetchError.empty'))
        setFetchedModels(null)
      } else {
        const models = resp.Models.map(m => ({
          Name: m.Name,
          MaxContextLength: lookupModelDefault(m.Name, MODEL_CONTEXT_DEFAULTS) ?? DEFAULT_CONTEXT,
          MaxTokens: 16384,
          Modality: m.Modality,
        }))
        setFetchedModels(models)
        setChecked(Object.fromEntries(models.map(m => [m.Name, true])))
      }
    } catch (err) {
      if (seq !== fetchSeq.current) return
      setFetchError(err instanceof Error ? err.message : String(err))
      setFetchedModels(null)
    } finally {
      if (seq === fetchSeq.current) setFetching(false)
    }
  }, [t])

  // Auto-fetch models once the user has filled in both endpoint and key.
  // The intro page establishes context first, so by the time they reach the
  // form they expect the app to work like any normal provider config dialog.
  useEffect(() => {
    if (phase !== 'form') return
    if (!endpointValid || !apiKey.trim() || keyFailure !== undefined) return
    if (fetchedModels !== null || fetching) return
    const timer = setTimeout(() => { void doFetch(endpoint, apiKey) }, 600)
    return () => clearTimeout(timer)
  }, [phase, endpoint, apiKey, endpointValid, keyFailure, fetchedModels, fetching, doFetch])

  // Tour auto-advance once the current target's field becomes valid.
  useEffect(() => {
    if (tourStep === null) return
    const satisfied =
      (tourStep === 0 && presetId !== '') ||
      (tourStep === 1 && endpointValid) ||
      (tourStep === 2 && apiKey.trim() !== '' && keyFailure === undefined) ||
      (tourStep === 3 && modelsToSave.length > 0)
    if (!satisfied) return
    const timer = setTimeout(() => {
      setTourStep(prev => (prev === null ? null : prev + 1 >= TOUR_TOTAL ? null : prev + 1))
    }, 400)
    return () => clearTimeout(timer)
  }, [tourStep, presetId, endpointValid, apiKey, keyFailure, modelsToSave.length])

  // Position the tour bubble under (or above) the active target.
  useLayoutEffect(() => {
    if (tourStep === null) { setBubblePos(null); return }
    const target = targetRefs[tourStep]?.current
    const body = bodyRef.current
    if (!target || !body) { setBubblePos(null); return }
    const tr = target.getBoundingClientRect()
    const br = body.getBoundingClientRect()
    const below = tr.bottom - br.top + 8
    const fitsBelow = below + 120 <= br.height
    setBubblePos({
      top: fitsBelow ? below : Math.max(0, tr.top - br.top - 120 - 8),
      left: Math.max(0, Math.min(tr.left - br.left, br.width - 280)),
    })
  }, [tourStep, fetching, fetchError, fetchedModels])

  const handlePresetSelect = (id: string) => {
    setPresetId(id)
    const p = PRESETS.find(x => x.id === id)
    if (p) {
      setEndpoint(p.endpoint)
      setFetchedModels(null)
      setFetchError('')
    }
  }

  const enterForm = () => {
    setPhase('form')
    setTourStep(0)
  }

  const handleFinish = async () => {
    setSaving(true)
    setSaveError('')
    try {
      let host = ''
      try { host = new URL(endpoint.trim()).hostname } catch { /* endpoint validated by canFinish */ }
      await configure({
        Name: preset?.label || host || effectiveKind('auto', endpoint),
        Kind: effectiveKind('auto', endpoint),
        Endpoint: endpoint.trim(),
        Models: modelsToSave,
        AuthToken: apiKey.trim() || undefined,
        MaxConcurrency: maxConcurrency || undefined,
      })
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err))
      setSaving(false)
    }
  }

  if (loading || providers.length > 0) return null

  const tourText = tourStep === 0 ? t('onboarding.provider.tour.preset')
    : tourStep === 1 ? t('onboarding.provider.tour.endpoint')
    : tourStep === 2 ? t('onboarding.provider.tour.apiKey')
    : fetching ? t('onboarding.provider.tour.modelsFetching')
    : manualMode ? t('onboarding.provider.tour.modelsManual')
    : fetchedModels && fetchedModels.length > 0 ? t('onboarding.provider.tour.modelsSelect')
    : t('onboarding.provider.tour.modelsIdle')

  const dim = (idx: number) =>
    tourStep === null ? '' : tourStep === idx ? 'onboarding-spotlight' : 'onboarding-dimmed'

  if (phase === 'intro') {
    return (
      <Modal
        open
        size="md"
        dragWindow
        belowTitlebar
        onClose={onDismiss}
        title={
          <div className="onboarding-title">
            <Sparkles size={16} />
            <div>{t('onboarding.provider.title')}</div>
          </div>
        }
        footer={
          <div className="onboarding-footer">
            <button type="button" className="onboarding-later" onClick={onDismiss}>
              {t('onboarding.provider.later')}
            </button>
            <button type="button" className="onboarding-finish" onClick={enterForm}>
              {t('onboarding.provider.intro.cta')}
              <ArrowRight size={14} />
            </button>
          </div>
        }
      >
        <div className="onboarding-intro">
          <div className="onboarding-intro-icon"><Sparkles size={28} /></div>
          <p className="onboarding-intro-desc">{t('onboarding.provider.intro.desc')}</p>
          <ul className="onboarding-intro-steps">
            <li><span className="onboarding-intro-step-num">1</span>{t('onboarding.provider.intro.step1')}</li>
            <li><span className="onboarding-intro-step-num">2</span>{t('onboarding.provider.intro.step2')}</li>
            <li><span className="onboarding-intro-step-num">3</span>{t('onboarding.provider.intro.step3')}</li>
          </ul>
        </div>
      </Modal>
    )
  }

  return (
    <Modal
      open
      size="lg"
      dragWindow
      belowTitlebar
      onClose={onDismiss}
      title={
        <div className="onboarding-title">
          <Sparkles size={16} />
          <div>
            <div>{t('onboarding.provider.title')}</div>
            <div className="onboarding-subtitle">{t('onboarding.provider.subtitle')}</div>
          </div>
        </div>
      }
      footer={
        <div className="onboarding-footer">
          {saveError && <span className="onboarding-save-error">{saveError}</span>}
          <button type="button" className="onboarding-later" onClick={onDismiss}>
            {t('onboarding.provider.later')}
          </button>
          <button
            type="button"
            className="onboarding-finish"
            disabled={!canFinish}
            onClick={() => { void handleFinish() }}
          >
            {saving && <Loader2 size={14} className="onboarding-spin" />}
            {t('onboarding.provider.finish')}
          </button>
        </div>
      }
    >
      <div ref={bodyRef} className="onboarding-body">
        <div ref={presetRef} className={`onboarding-section ${dim(0)}`}>
          <div className="onboarding-label">{t('onboarding.provider.preset.label')}</div>
          <div className="onboarding-chips">
            {PRESETS.map(p => (
              <button
                key={p.id}
                type="button"
                className={`onboarding-chip${presetId === p.id ? ' onboarding-chip--active' : ''}`}
                onClick={() => handlePresetSelect(p.id)}
              >
                <ProviderIcon id={p.icon ?? p.id} size={14} />
                {presetLabel(p, t)}
              </button>
            ))}
            <button
              type="button"
              className={`onboarding-chip${presetId === 'custom' ? ' onboarding-chip--active' : ''}`}
              onClick={() => { setPresetId('custom'); setEndpoint('') }}
            >
              {t('onboarding.provider.preset.custom')}
            </button>
          </div>
          {preset && presetDescription(preset, t) && (
            <p className="onboarding-preset-desc">{presetDescription(preset, t)}</p>
          )}
        </div>

        <div ref={endpointRef} className={`onboarding-section ${dim(1)}`}>
          <div className="onboarding-label">{t('onboarding.provider.endpoint.label')}</div>
          <input
            className="onboarding-input"
            value={endpoint}
            placeholder={t('onboarding.provider.endpoint.placeholder')}
            onChange={e => { setEndpoint(e.target.value); setFetchedModels(null); setFetchError('') }}
            spellCheck={false}
          />
        </div>

        <div ref={keyRef} className={`onboarding-section ${dim(2)}`}>
          <div className="onboarding-label">{t('onboarding.provider.apiKey.label')}</div>
          <input
            className="onboarding-input"
            type="password"
            autoComplete="off"
            value={apiKey}
            placeholder={t('onboarding.provider.apiKey.placeholder')}
            aria-label={t('onboarding.provider.apiKey.label')}
            aria-invalid={keyFailure !== undefined}
            onChange={e => setApiKey(e.target.value)}
            spellCheck={false}
          />
          {keyFailure !== undefined && (
            <p className="onboarding-field-error">
              <AlertCircle size={13} />
              {t(keyFailure === 'blank' ? 'onboarding.provider.apiKey.blank' : 'onboarding.provider.apiKey.illegal')}
            </p>
          )}
          {preset?.keyUrl && (
            <a className="onboarding-key-link" href={preset.keyUrl} target="_blank" rel="noreferrer">
              {t('onboarding.provider.apiKey.getKey', { provider: preset.label })}
              <ExternalLink size={12} />
            </a>
          )}
        </div>

        <details className="onboarding-advanced">
          <summary className="onboarding-advanced-summary">{t('onboarding.provider.advanced.label')}</summary>
          <div className="onboarding-section">
            <div className="onboarding-label">{t('onboarding.provider.maxConcurrency.label')}</div>
            <input
              className="onboarding-input"
              type="number"
              min={0}
              step={1}
              value={maxConcurrency ?? ''}
              placeholder={t('onboarding.provider.maxConcurrency.placeholder')}
              onChange={e => {
                const value = e.target.value
                const num = value === '' ? undefined : parseInt(value, 10)
                setMaxConcurrency(num && num > 0 ? num : undefined)
              }}
              spellCheck={false}
            />
          </div>
        </details>

        <div ref={modelsRef} className={`onboarding-section ${dim(3)}`}>
          <div className="onboarding-label">
            {t('onboarding.provider.models.title')}
            {fetching && <Loader2 size={13} className="onboarding-spin" />}
            {!manualMode && fetchedModels && fetchedModels.length > 0 && (
              <span className="onboarding-count">
                {t('onboarding.provider.models.selectedCount', { count: selectedFetched.length })}
              </span>
            )}
          </div>

          {fetching && <div className="onboarding-models-status">{t('onboarding.provider.models.fetching')}</div>}

          {!fetching && !manualMode && (!fetchedModels || fetchedModels.length === 0) && (
            <div className="onboarding-models-idle">
              <button
                type="button"
                className="onboarding-fetch-btn"
                disabled={!canFetch}
                onClick={() => { void doFetch(endpoint, apiKey) }}
              >
                {t('onboarding.provider.models.fetch')}
              </button>
              <div className="onboarding-models-status">{t('onboarding.provider.models.idle')}</div>
            </div>
          )}

          {!fetching && !manualMode && fetchedModels && fetchedModels.length > 0 && (
            <div className="onboarding-models-list">
              {fetchedModels.map(m => (
                <label key={m.Name} className="onboarding-model-row">
                  <input
                    type="checkbox"
                    checked={checked[m.Name] !== false}
                    onChange={e => setChecked(prev => ({ ...prev, [m.Name]: e.target.checked }))}
                  />
                  <span>{m.Name}</span>
                </label>
              ))}
            </div>
          )}

          {!fetching && manualMode && (
            <div className="onboarding-manual">
              <div className="onboarding-fetch-error">
                <AlertCircle size={13} />
                <span>{t('onboarding.provider.models.fetchError', { error: fetchError })}</span>
              </div>
              <textarea
                className="onboarding-textarea"
                value={manualText}
                placeholder={t('onboarding.provider.models.manual.placeholder')}
                onChange={e => setManualText(e.target.value)}
                rows={3}
                spellCheck={false}
              />
              <div className="onboarding-manual-actions">
                <button type="button" className="onboarding-link-btn" onClick={() => { void doFetch(endpoint, apiKey) }}>
                  <RotateCcw size={12} />
                  {t('onboarding.provider.models.retry')}
                </button>
                {preset && preset.models.length > 0 && (
                  <button
                    type="button"
                    className="onboarding-link-btn"
                    onClick={() => setManualText(preset.models.map(m => m.Name).join(', '))}
                  >
                    {t('onboarding.provider.models.usePresetDefaults')}
                  </button>
                )}
              </div>
            </div>
          )}

        </div>

        {tourStep !== null && bubblePos && (
          <div className="onboarding-bubble" style={{ top: bubblePos.top, left: bubblePos.left }}>
            <div className="onboarding-bubble-step">
              {t('onboarding.provider.tour.step', { step: tourStep + 1, total: TOUR_TOTAL })}
            </div>
            <div className="onboarding-bubble-text">{tourText}</div>
            <div className="onboarding-bubble-actions">
              <button type="button" className="onboarding-link-btn" onClick={() => setTourStep(null)}>
                {t('onboarding.provider.tour.skip')}
              </button>
              <button
                type="button"
                className="onboarding-link-btn"
                onClick={() => setTourStep(prev => (prev === null || prev + 1 >= TOUR_TOTAL ? null : prev + 1))}
              >
                {t('onboarding.provider.tour.next')}
              </button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
