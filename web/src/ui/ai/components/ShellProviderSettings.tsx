import { useState, useEffect, useCallback, useMemo, useRef, type ChangeEvent, type ReactNode } from 'react'
import {
  Plus,
  Pencil,
  Trash2,
  Copy,
  Loader2,
  AlertCircle,
  Check,
  X,
  ChevronDown,
  Download,
  Upload,
  ChevronUp,
  Clock,
  Gauge,
  RotateCcw,
  Radar,
  Server,
  Layers,
  SlidersHorizontal,
} from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as aimanagerProvider from '../../../gen-clients/aimanager/client'
import * as aimanagerAggregator from '../../../gen-clients/aimanager/client'
import * as aimanagerConfig from '../../../gen-clients/aimanager/client'
import * as aiaggregator from '../../../gen-clients/aiaggregator/client'
import type { Provider, ProviderModel, ProviderDisableWindow, ModelDefault } from '../../../gen-clients/system/types'
import type { AggregatorDescriptor, AICallableUnitView } from '../../../gen-clients/system/types'
import { useProviderConfigs } from '../hooks/useProviderConfigs'
import { DEFAULT_CONTEXT, lookupModelDefault } from '../../../../../const/modelContextDefaults'
import {
  isBuiltinDefault,
  isUnsetMaxTokens,
  isUnsetModality,
  modelDefaultsLookupTable,
} from './modelDefaults'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { ShellAggregatorDialog } from './ShellAggregatorDialog'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge, Button, Input, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'
import { Switch } from '../../settings/shadcn/ui/switch'
import { useBrowserOverlay } from '../browserOverlay'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n'
import './ShellProviderSettings.css'

import { PRESETS, effectiveKind, PROVIDER_KINDS, presetLabel, type ProviderKind } from './providerPresets'
import { ProviderIcon, iconKeyForModel, iconKeyForModelRow, iconKeyForUnit, iconKeysForModels } from './providerIcons'
import {
  isCoolingDown,
  isDisabled,
  formatCountdown,
  healthReasonLabelKey,
  recoveryModeLabelKey,
  type HealthProjection,
} from '../hooks/healthStatus'
import { formatRelativeTime } from '../../lib/format-time'

const DEFAULT_PRESET = PRESETS[0]!
const SYSTEM_AGGREGATOR_ID = 'system'

function strategyDisplay(strategy: string | undefined): string {
  // snake_case to camelCase for i18n key: round_robin -> roundRobin
  const key = (strategy || 'round_robin').replace(/_([a-z])/g, (_, c) => c.toUpperCase())
  return `aggregator.strategy.${key}`
}

// datetime-local inputs have no timezone, so RFC3339 must be converted to the browser's local wall time.
function rfc3339ToLocalInput(value: string | undefined): string {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// datetime-local values are local wall time; emit RFC3339 so the backend stores an unambiguous instant.
function localInputToRfc3339(value: string): string {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  return d.toISOString()
}

const DISABLE_WINDOW_TIME_PATTERN = /^(?:[01]\d|2[0-3]):[0-5]\d$/

const DISABLE_WINDOW_DAY_KEYS: I18nKey[] = [
  'settings.provider.disableWindows.day1',
  'settings.provider.disableWindows.day2',
  'settings.provider.disableWindows.day3',
  'settings.provider.disableWindows.day4',
  'settings.provider.disableWindows.day5',
  'settings.provider.disableWindows.day6',
  'settings.provider.disableWindows.day7',
]

function isValidDisableWindowTime(value: string): boolean {
  return DISABLE_WINDOW_TIME_PATTERN.test(value)
}

interface ProviderFormData {
  presetId: string
  name: string
  kind: 'auto' | ProviderKind
  endpoint: string
  apiKey: string
  userAgent: string
  proxy: string
  maxConcurrency?: number
  disableWindows: ProviderDisableWindow[]
  isTokenPlan: boolean
  tokenPlanExpiresAt: string
  tokenPlanRemainingPct: number
  tokenPlanWindowMs: number
}

type UnitProbeState =
  | { status: 'probing' }
  | { status: 'ok'; latencyMs: number }
  | { status: 'failed'; error: string }

function unitProbeKey(providerName: string, model: string): string {
  return `${providerName}::${model}`
}

function isImageModel(model: ProviderModel): boolean {
  return (model.Modality ?? 'chat') === 'image'
}

function isVideoModel(model: ProviderModel): boolean {
  return (model.Modality ?? 'chat') === 'video'
}

function fmtCompactNum(n: number | undefined): string {
  if (!n) return ''
  if (n >= 1000000) return `${Math.round(n / 100000) / 10}M`
  if (n >= 1000) return `${Math.round(n / 100) / 10}k`
  return String(n)
}

// Client-side cap for provider-batch probing when the provider has no
// MaxConcurrency configured (backend gate is then unlimited).
const PROBE_DEFAULT_CONCURRENCY = 4

function buildDefaultForm(): ProviderFormData {
  return {
    presetId: DEFAULT_PRESET.id,
    name: DEFAULT_PRESET.label,
    kind: DEFAULT_PRESET.kind,
    endpoint: DEFAULT_PRESET.endpoint,
    apiKey: '',
    userAgent: '',
    proxy: '',
    maxConcurrency: undefined,
    isTokenPlan: false,
    tokenPlanExpiresAt: '',
    tokenPlanRemainingPct: 100,
    tokenPlanWindowMs: 60000,
    disableWindows: [],
  }
}

function providerToForm(provider: Provider): ProviderFormData {
  const preset = PRESETS.find(p => p.kind === provider.Kind && p.endpoint === provider.Endpoint)
  const knownKind = (PROVIDER_KINDS as readonly string[]).includes(provider.Kind) ? (provider.Kind as ProviderKind) : undefined
  return {
    presetId: preset?.id ?? 'custom',
    name: provider.Name,
    kind: preset ? preset.kind : knownKind ?? 'auto',
    endpoint: provider.Endpoint,
    apiKey: '',
    userAgent: provider.UserAgent ?? '',
    proxy: provider.Proxy ?? '',
    maxConcurrency: provider.MaxConcurrency,
    isTokenPlan: provider.IsTokenPlan ?? false,
    tokenPlanExpiresAt: rfc3339ToLocalInput(provider.TokenPlanExpiresAt),
    tokenPlanRemainingPct: provider.TokenPlanRemainingPct ?? 100,
    tokenPlanWindowMs: provider.TokenPlanWindowMs ?? 60000,
    disableWindows: provider.DisableWindows ? provider.DisableWindows.map(w => ({ Start: w.Start, End: w.End, Days: w.Days ?? [] })) : [],
  }
}

function isInferredModality(value: string | undefined): boolean {
  // 'chat' is the implicit default when a model does not advertise a modality.
  return value === undefined || value === 'chat'
}

function isInferredProtocol(value: string | undefined, kind: string): boolean {
  // Protocol is inferred from the provider kind/endpoint, or unset until the first fetch.
  return value === undefined || value === kind
}

/**
 * Merge upstream fetched models into an existing local model list.
 *
 * Merge semantics:
 * 1. Exact name match: the existing model keeps every user-configurable field
 *    (MaxContextLength, MaxTokens, all cost fields, cost tiers, probe state, etc.).
 * 2. New upstream models (names not already present) are appended.
 * 3. Local models that are absent from the upstream response are kept unchanged.
 * 4. Inferred-only fields Modality and Protocol are refreshed from upstream ONLY
 *    when the local value is still at its inferred/default value:
 *      - Modality: undefined or 'chat'
 *      - Protocol: undefined or the current effective provider kind
 *    This lets upstream announcements (e.g. a chat model gaining image support,
 *    or a new protocol flag) flow through without clobbering explicit user edits.
 */
export function mergeProviderModels(
  existing: ProviderModel[],
  fetched: ProviderModel[],
  kind: ProviderKind,
): ProviderModel[] {
  const existingMap = new Map(existing.map(m => [m.Name, m]))
  const fetchedNames = new Set(fetched.map(m => m.Name))

  const merged = fetched.map(m => {
    const current = existingMap.get(m.Name)
    if (!current) return m

    const next: ProviderModel = { ...current }

    if (m.Modality !== undefined && isInferredModality(current.Modality)) {
      next.Modality = m.Modality
    }

    if (m.Protocol !== undefined && isInferredProtocol(current.Protocol, kind)) {
      next.Protocol = m.Protocol
    }

    return next
  })

  for (const m of existing) {
    if (!fetchedNames.has(m.Name)) {
      merged.push(m)
    }
  }

  return merged
}

/**
 * Preset picker with brand icons on every menu row. Native <option> elements
 * cannot render SVG, so this is a custom listbox dropdown. The open menu is an
 * HTML layer that may float above the embedded browser window — it registers
 * with useBrowserOverlay(open) per project constraint.
 */
function PresetSelect({ value, onChange, disabled }: {
  value: string
  onChange: (presetId: string) => void
  disabled?: boolean
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  useBrowserOverlay(open)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onPointerDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  const selected = PRESETS.find(p => p.id === value)
  const selectedLabel = selected ? presetLabel(selected, t) : t('settings.provider.dialog.custom')
  const triggerIcon = selected ? (selected.icon ?? selected.id) : undefined

  const pick = (presetId: string) => {
    onChange(presetId)
    setOpen(false)
  }

  return (
    <div className="shell-provider-select-wrap shell-provider-select-wrap--preset shell-provider-combobox" ref={ref}>
      <button
        type="button"
        className="shell-provider-select shell-provider-select-btn"
        onClick={() => setOpen(v => !v)}
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        <ProviderIcon id={triggerIcon} size={16} className="shell-provider-select-lead" />
        <span className="shell-provider-select-value">{selectedLabel}</span>
        <ChevronDown size={14} className={`shell-provider-select-icon${open ? ' shell-provider-select-icon--open' : ''}`} />
      </button>
      {open && !disabled && (
        <div className="shell-provider-combobox-menu" role="listbox">
          <button
            type="button"
            role="option"
            aria-selected={value === 'custom'}
            className={`shell-provider-combobox-item${value === 'custom' ? ' active' : ''}`}
            onClick={() => pick('custom')}
          >
            <ProviderIcon size={16} className="shell-provider-combobox-item-icon" />
            <span className="shell-provider-combobox-item-label">{t('settings.provider.dialog.custom')}</span>
            {value === 'custom' && <Check size={12} className="shell-provider-combobox-item-check" />}
          </button>
          {PRESETS.map(p => (
            <button
              key={p.id}
              type="button"
              role="option"
              aria-selected={value === p.id}
              className={`shell-provider-combobox-item${value === p.id ? ' active' : ''}`}
              onClick={() => pick(p.id)}
            >
              <ProviderIcon id={p.icon ?? p.id} size={16} className="shell-provider-combobox-item-icon" />
              <span className="shell-provider-combobox-item-label">{presetLabel(p, t)}</span>
              {value === p.id && <Check size={12} className="shell-provider-combobox-item-check" />}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

export function ShellProviderSettings({ section = 'providers', userAgentVisible = false }: { section?: 'providers' | 'aggregators' | 'defaults'; userAgentVisible?: boolean }) {
  const { t, locale } = useI18n()
  const { providers, loading, error: providerError, configure, remove, reload } = useProviderConfigs()
  const [aggregators, setAggregators] = useState<AggregatorDescriptor[]>([])
  const [systemUnits, setSystemUnits] = useState<AICallableUnitView[]>([])
  /** aggregatorId → member model names (for brand icon stacks on cards). */
  const [aggregatorModels, setAggregatorModels] = useState<Record<string, string[]>>({})
  const [aggregatorsLoading, setAggregatorsLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState('')
  const [saved, setSaved] = useState(false)
  const importInputRef = useRef<HTMLInputElement>(null)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingName, setEditingName] = useState<string | null>(null)
  // Name of the provider whose model list seeded the dialog (edit + copy).
  // While set, typing apiKey/endpoint must never reset the list to
  // preset/empty — that silently deleted configured models on save.
  const [seededProviderName, setSeededProviderName] = useState<string | null>(null)
  const [form, setForm] = useState<ProviderFormData>(buildDefaultForm)
  const [tokenPlanOpen, setTokenPlanOpen] = useState(false)
  const [disableWindowError, setDisableWindowError] = useState('')
  const [fetchedModels, setFetchedModels] = useState<ProviderModel[] | null>(null)
  const [fetchingModels, setFetchingModels] = useState(false)
  const [fetchError, setFetchError] = useState('')

  const [aggregatorDialogOpen, setAggregatorDialogOpen] = useState(false)
  const [editingAggregatorId, setEditingAggregatorId] = useState<string | null>(null)

  // Editable model-defaults table (global, backend-owned). `defaults` is the
  // working copy shown in the tab; `defaultsOriginal` is the last snapshot
  // loaded from model_defaults_get, used to compute dirty state.
  const [defaults, setDefaults] = useState<ModelDefault[]>([])
  const [defaultsOriginal, setDefaultsOriginal] = useState<ModelDefault[]>([])
  const [defaultsLoading, setDefaultsLoading] = useState(false)
  const [defaultsSaving, setDefaultsSaving] = useState(false)
  const [defaultsError, setDefaultsError] = useState('')
  const [defaultsSaved, setDefaultsSaved] = useState(false)

  const [confirmOpen, setConfirmOpen] = useState(false)
  const [confirmConfig, setConfirmConfig] = useState<{
    title: string
    description: string
    confirmLabel?: string
    danger?: boolean
    onConfirm: () => void
  } | null>(null)

  useBrowserOverlay(dialogOpen)

  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  const loadAggregators = useCallback(async () => {
    setAggregatorsLoading(true)
    try {
      const resp = await aimanagerAggregator.aggregatorList(client)
      setAggregators(resp.Items)
      // Member models of each custom aggregator (for the title icon stack),
      // recursing into nested child-aggregator refs so pooled brands from any
      // depth surface. Cycle-safe via a visited set per root traversal.
      const custom = resp.Items.filter(a => a.Id !== SYSTEM_AGGREGATOR_ID)
      const actorIdByAggId = new Map(resp.Items.map(a => [a.Id, a.ActorId]))
      const modelMap: Record<string, string[]> = {}
      const collectModels = async (actorId: string, seen: Set<string>): Promise<string[]> => {
        if (seen.has(actorId)) return []
        seen.add(actorId)
        try {
          const st = await aiaggregator.status(client, { target: actorId })
          const units = st.Units ?? []
          const own = units
            .filter(u => !u.OnDemand && !u.aggregatorID)
            .map(u => u.Model)
          const nested = await Promise.all(
            units
              .filter(u => !u.OnDemand && u.aggregatorID)
              .map(u => actorIdByAggId.get(u.aggregatorID!))
              .filter((id): id is string => !!id)
              .map(id => collectModels(id, seen)),
          )
          return [...own, ...nested.flat()]
        } catch {
          return []
        }
      }
      await Promise.all(custom.map(async a => {
        modelMap[a.Id] = await collectModels(a.ActorId, new Set())
      }))
      setAggregatorModels(modelMap)
      const system = resp.Items.find(a => a.Id === SYSTEM_AGGREGATOR_ID)
      if (system) {
        // Fetch runtime status (includes health fields) via aiaggregator.status.
        try {
          const status = await aiaggregator.status(client, { target: system.ActorId })
          setSystemUnits(status.Units ?? [])
        } catch {
          setSystemUnits([])
        }
      } else {
        setSystemUnits([])
      }
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setAggregatorsLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadAggregators()
  }, [loadAggregators])

  const loadModelDefaults = useCallback(async () => {
    setDefaultsLoading(true)
    setDefaultsError('')
    try {
      const resp = await aimanagerConfig.modelDefaultsGet(client)
      setDefaults(resp.Items)
      setDefaultsOriginal(resp.Items)
    } catch (err) {
      setDefaultsError(err instanceof Error ? err.message : String(err))
    } finally {
      setDefaultsLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadModelDefaults()
  }, [loadModelDefaults])

  const updateDefaultRow = useCallback((index: number, patch: Partial<ModelDefault>) => {
    setDefaults(prev => prev.map((row, i) => (i === index ? { ...row, ...patch } : row)))
    setDefaultsSaved(false)
  }, [])

  const removeDefaultRow = useCallback((index: number) => {
    setDefaults(prev => prev.filter((_, i) => i !== index))
    setDefaultsSaved(false)
  }, [])

  const addDefaultRow = useCallback(() => {
    setDefaults(prev => [...prev, { Prefix: '', MaxContextLength: 0 }])
    setDefaultsSaved(false)
  }, [])

  const saveModelDefaults = useCallback(async () => {
    setDefaultsError('')
    const seen = new Set<string>()
    for (const row of defaults) {
      const prefix = row.Prefix.trim()
      if (!prefix) {
        setDefaultsError(t('settings.provider.modelDefaults.prefixRequired'))
        return
      }
      if (seen.has(prefix)) {
        setDefaultsError(t('settings.provider.modelDefaults.prefixDuplicate'))
        return
      }
      seen.add(prefix)
      if (!row.MaxContextLength || row.MaxContextLength <= 0) {
        setDefaultsError(t('settings.provider.modelDefaults.contextRequired'))
        return
      }
    }
    setDefaultsSaving(true)
    try {
      // Persist only the non-builtin (user-edited) rows: pure baseline copies
      // are re-merged server-side and would otherwise freeze the shipped
      // baseline into user state.
      const items = defaults
        .filter(row => !isBuiltinDefault(row))
        .map(row => {
          const out: ModelDefault = {
            Prefix: row.Prefix.trim(),
            MaxContextLength: row.MaxContextLength,
          }
          if (!isUnsetMaxTokens(row.MaxTokens)) out.MaxTokens = row.MaxTokens
          if (!isUnsetModality(row.Modality)) out.Modality = row.Modality
          if (row.CostInput !== undefined) out.CostInput = row.CostInput
          if (row.CostOutput !== undefined) out.CostOutput = row.CostOutput
          return out
        })
      const resp = await aimanagerConfig.modelDefaultsSet(client, { Items: items })
      if (!resp.Ok) {
        setDefaultsError(resp.Error || t('settings.provider.modelDefaults.saveFailed'))
        return
      }
      await loadModelDefaults()
      setDefaultsSaved(true)
      setTimeout(() => setDefaultsSaved(false), 2000)
    } catch (err) {
      setDefaultsError(err instanceof Error ? err.message : String(err))
    } finally {
      setDefaultsSaving(false)
    }
  }, [defaults, loadModelDefaults, t])

  const systemAggregator = useMemo(
    () => aggregators.find(a => a.Id === SYSTEM_AGGREGATOR_ID) ?? null,
    [aggregators]
  )

  const customAggregators = useMemo(
    () => aggregators.filter(a => a.Id !== SYSTEM_AGGREGATOR_ID),
    [aggregators]
  )

  // Merged prefix→context table for prefix matching on fetch. Combines the
  // shipped baseline (MODEL_CONTEXT_DEFAULTS) with backend-loaded user rows.
  const modelDefaultsTable = useMemo<Record<string, number>>(
    () => modelDefaultsLookupTable(defaults),
    [defaults]
  )

  const defaultsDirty = useMemo(
    () => JSON.stringify(defaults) !== JSON.stringify(defaultsOriginal),
    [defaults, defaultsOriginal],
  )

  const probeTarget = systemAggregator?.ActorId ?? ''
  const [expandedProvider, setExpandedProvider] = useState<string | null>(null)
  const [unitProbes, setUnitProbes] = useState<Record<string, UnitProbeState>>({})
  const [probingProviderName, setProbingProviderName] = useState<string | null>(null)
  const [editingUnit, setEditingUnit] = useState<{ provider: string; model: string } | null>(null)
  const [unitDraft, setUnitDraft] = useState({
    name: '',
    protocol: 'openai',
    maxContextLength: '',
    maxTokens: '',
  })

  const toggleProviderExpanded = useCallback((name: string) => {
    setExpandedProvider(prev => (prev === name ? null : name))
  }, [])

  // Periodically refresh provider and aggregator data so cooldown status stays current.
  // Use silent reload to avoid toggling loading state (which would collapse expanded rows
  // and interrupt browsing). Skip entirely when a dialog is open or a row is expanded.
  useEffect(() => {
    const id = setInterval(() => {
      if (dialogOpen || confirmOpen || aggregatorDialogOpen || editingUnit || expandedProvider) return
      void reload(true)
      void loadAggregators()
    }, 15000)
    return () => clearInterval(id)
  }, [reload, loadAggregators, dialogOpen, confirmOpen, aggregatorDialogOpen, editingUnit, expandedProvider])

  const runUnitProbe = useCallback(async (providerName: string, model: string) => {
    const key = unitProbeKey(providerName, model)
    setUnitProbes(prev => ({ ...prev, [key]: { status: 'probing' } }))
    const started = Date.now()
    try {
      if (!probeTarget) {
        throw new Error(t('settings.provider.probeNoAggregator'))
      }
      await aiaggregator.probeTokens(client, {
        SessionId: '',
        Unit: { provider: providerName, model },
        Messages: [{ Id: '', Role: 'user', Content: [{ Type: 'text', Text: 'ping' }] }],
      }, { target: probeTarget })
      const latencyMs = Date.now() - started
      setUnitProbes(prev => ({ ...prev, [key]: { status: 'ok', latencyMs } }))
      // Persist the outcome so it survives reloads; the live view above still
      // works when persistence itself fails.
      await aimanagerProvider.providerRecordProbe(client, {
        ProviderName: providerName,
        Model: model,
        Ok: true,
        LatencyMs: latencyMs,
      }).catch(() => {})
    } catch (err) {
      const raw = err instanceof Error ? err.message : String(err)
      const error = raw.replace(/^aiaggregator\.probe_tokens:\s*/, '')
      setUnitProbes(prev => ({ ...prev, [key]: { status: 'failed', error } }))
      await aimanagerProvider.providerRecordProbe(client, {
        ProviderName: providerName,
        Model: model,
        Ok: false,
        Error: error,
      }).catch(() => {})
    }
  }, [probeTarget, t])

  const handleProbeProvider = useCallback(async (provider: Provider) => {
    if (probingProviderName) return
    setExpandedProvider(provider.Name)
    setProbingProviderName(provider.Name)
    // Fire probes concurrently; the backend's per-provider gate
    // (MaxConcurrency) queues them server-side, so the client just launches
    // all of them. Unset (0) means "no limit" on the backend — cap locally
    // anyway to avoid flooding the UI with hundreds of in-flight rows.
    const concurrency = provider.MaxConcurrency && provider.MaxConcurrency > 0
      ? provider.MaxConcurrency
      : PROBE_DEFAULT_CONCURRENCY
    try {
      const models = provider.Models.filter(m => !isImageModel(m) && !isVideoModel(m))
      let cursor = 0
      const workers = Array.from({ length: Math.min(concurrency, models.length) }, async () => {
        while (cursor < models.length) {
          const model = models[cursor++]!
          await runUnitProbe(provider.Name, model.Name)
        }
      })
      await Promise.all(workers)
    } finally {
      setProbingProviderName(null)
      // Probes report health back to aimanager and persist per-unit state;
      // refresh both projections. Silent to keep scroll position.
      await Promise.all([reload(true), loadAggregators()])
    }
  }, [probingProviderName, runUnitProbe, reload, loadAggregators])

  const handleEditUnit = useCallback((provider: Provider, model: ProviderModel) => {
    setEditingUnit({ provider: provider.Name, model: model.Name })
    setUnitDraft({
      name: model.Name,
      protocol: model.Protocol || provider.Kind || 'openai',
      maxContextLength: model.MaxContextLength ? String(model.MaxContextLength) : '',
      maxTokens: model.MaxTokens ? String(model.MaxTokens) : '',
    })
  }, [])

  const handleCancelEditUnit = useCallback(() => {
    setEditingUnit(null)
  }, [])

  const handleSaveUnit = useCallback(async (provider: Provider, model: ProviderModel) => {
    if (!unitDraft.name.trim()) {
      setActionError(t('settings.provider.dialog.modelNameRequired'))
      return
    }
    if (provider.Models.some(m => m !== model && m.Name === unitDraft.name.trim())) {
      setActionError(t('settings.provider.dialog.modelNameDuplicate'))
      return
    }
    setSaving(true)
    setActionError('')
    try {
      const parseNum = (v: string) => {
        const n = parseInt(v, 10)
        return v === '' || Number.isNaN(n) ? undefined : n
      }
      const models = provider.Models.map(m => m === model ? {
        ...m,
        Name: unitDraft.name.trim(),
        Protocol: unitDraft.protocol,
        MaxContextLength: parseNum(unitDraft.maxContextLength),
        MaxTokens: parseNum(unitDraft.maxTokens),
      } : m)
      await configure({
        Name: provider.Name,
        Kind: provider.Kind,
        Endpoint: provider.Endpoint,
        Models: models,
        AuthToken: undefined,
        UserAgent: provider.UserAgent || undefined,
        Proxy: provider.Proxy || undefined,
        MaxConcurrency: provider.MaxConcurrency || undefined,
        DisableWindows: provider.DisableWindows,
        IsTokenPlan: provider.IsTokenPlan,
        TokenPlanExpiresAt: provider.IsTokenPlan ? provider.TokenPlanExpiresAt : undefined,
        TokenPlanRemainingPct: provider.IsTokenPlan ? provider.TokenPlanRemainingPct : undefined,
        TokenPlanWindowMs: provider.IsTokenPlan ? provider.TokenPlanWindowMs : undefined,
      })
      setEditingUnit(null)
      await loadAggregators()
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
      window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [unitDraft, configure, loadAggregators, t])

  const handleOpenAdd = useCallback(() => {
    setEditingName(null)
    setSeededProviderName(null)
    setForm(buildDefaultForm())
    setTokenPlanOpen(false)
    setFetchedModels(DEFAULT_PRESET.models)
    setFetchError('')
    setDisableWindowError('')
    setDialogOpen(true)
  }, [])

  const handleOpenEdit = useCallback((provider: Provider) => {
    setEditingName(provider.Name)
    setSeededProviderName(provider.Name)
    setForm(providerToForm(provider))
    setTokenPlanOpen(provider.IsTokenPlan ?? false)
    setFetchedModels(provider.Models)
    setFetchError('')
    setDisableWindowError('')
    setDialogOpen(true)
  }, [])

  const handleCopy = useCallback((provider: Provider) => {
    const existingNames = new Set(providers.map(p => p.Name))
    let copyName = `${provider.Name} (copy)`
    let i = 1
    while (existingNames.has(copyName)) {
      i += 1
      copyName = `${provider.Name} (copy ${i})`
    }
    const nextForm = providerToForm(provider)
    nextForm.name = copyName
    nextForm.apiKey = ''
    setEditingName(null)
    setSeededProviderName(provider.Name)
    setForm(nextForm)
    setTokenPlanOpen(provider.IsTokenPlan ?? false)
    setFetchedModels(provider.Models)
    setFetchError('')
    setDisableWindowError('')
    setDialogOpen(true)
  }, [providers])

  const showConfirm = useCallback((
    title: string,
    description: string,
    onConfirm: () => void,
    options?: { confirmLabel?: string; danger?: boolean },
  ) => {
    setConfirmConfig({ title, description, onConfirm, ...options })
    setConfirmOpen(true)
  }, [])

  const handleResetHealth = useCallback(async (provider: Provider) => {
    setSaving(true)
    setActionError('')
    try {
      const resp = await aimanagerProvider.providerResetHealth(client, { ProviderName: provider.Name })
      if (!resp.Ok) {
        setActionError(resp.Error || t('health.reset'))
        return
      }
      await reload(true)
      await loadAggregators()
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
      window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [reload, loadAggregators, t])

  const handleToggleProviderDisabled = useCallback(async (provider: Provider) => {
    setSaving(true)
    setActionError('')
    try {
      const resp = await aimanagerProvider.providerSetDisabled(client, {
        ProviderName: provider.Name,
        Disabled: !provider.Disabled,
      })
      if (!resp.Ok) {
        setActionError(resp.Error || t('settings.provider.disable'))
        return
      }
      await reload(true)
      await loadAggregators()
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
      window.dispatchEvent(new CustomEvent('sporemind:providers-changed'))
      window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [reload, loadAggregators, t])

  const handleToggleUnitDisabled = useCallback(async (provider: Provider, model: ProviderModel) => {
    setSaving(true)
    setActionError('')
    try {
      const resp = await aimanagerProvider.providerSetDisabled(client, {
        ProviderName: provider.Name,
        Model: model.Name,
        Disabled: !model.Disabled,
      })
      if (!resp.Ok) {
        setActionError(resp.Error || t('settings.provider.disable'))
        return
      }
      await reload(true)
      await loadAggregators()
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
      window.dispatchEvent(new CustomEvent('sporemind:providers-changed'))
      window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [reload, loadAggregators, t])

  const handleDelete = useCallback((provider: Provider) => {
    showConfirm(
      t('settings.provider.confirm.deleteProvider'),
      t('settings.provider.confirm.deleteProviderDesc', { name: provider.Name }),
      async () => {
        setSaving(true)
        setActionError('')
        try {
          await remove(provider.Name)
          await loadAggregators()
          setSaved(true)
          setTimeout(() => setSaved(false), 2000)
          window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
        } catch (err) {
          setActionError(err instanceof Error ? err.message : String(err))
        } finally {
          setSaving(false)
          setConfirmOpen(false)
        }
      }
    )
  }, [remove, loadAggregators, showConfirm, t])

  const handleRemoveUnit = useCallback((provider: Provider, model: ProviderModel) => {
    showConfirm(
      t('settings.provider.confirm.deleteUnit'),
      t('settings.provider.confirm.deleteUnitDesc', { name: provider.Name, model: model.Name }),
      async () => {
        setSaving(true)
        setActionError('')
        try {
          await configure({
            Name: provider.Name,
            Kind: provider.Kind,
            Endpoint: provider.Endpoint,
            Models: provider.Models.filter(m => m.Name !== model.Name),
            AuthToken: undefined,
            UserAgent: provider.UserAgent || undefined,
            Proxy: provider.Proxy || undefined,
            MaxConcurrency: provider.MaxConcurrency || undefined,
            DisableWindows: provider.DisableWindows,
            IsTokenPlan: provider.IsTokenPlan,
            TokenPlanExpiresAt: provider.IsTokenPlan ? provider.TokenPlanExpiresAt : undefined,
            TokenPlanRemainingPct: provider.IsTokenPlan ? provider.TokenPlanRemainingPct : undefined,
            TokenPlanWindowMs: provider.IsTokenPlan ? provider.TokenPlanWindowMs : undefined,
          })
          await loadAggregators()
          setSaved(true)
          setTimeout(() => setSaved(false), 2000)
          window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
        } catch (err) {
          setActionError(err instanceof Error ? err.message : String(err))
        } finally {
          setSaving(false)
          setConfirmOpen(false)
        }
      }
    )
  }, [configure, loadAggregators, showConfirm, t])

  const handleSave = async () => {
    setActionError('')
    setSaved(false)
    const invalidWindow = form.disableWindows.find(
      w => !isValidDisableWindowTime(w.Start) || !isValidDisableWindowTime(w.End),
    )
    if (invalidWindow) {
      setDisableWindowError(t('settings.provider.disableWindows.invalid'))
      return
    }
    setDisableWindowError('')
    setSaving(true)
    try {
      const models = resolveModels()
      const finalName = form.name || effectiveKind(form.kind, form.endpoint)
      await configure({
        Name: finalName,
        // Rename: editingName is the provider's current name; without it the
        // backend's name-keyed upsert would append a duplicate instead of
        // updating the original entry.
        PreviousName: editingName && editingName !== finalName ? editingName : undefined,
        Kind: effectiveKind(form.kind, form.endpoint),
        Endpoint: form.endpoint,
        Models: models,
        AuthToken: form.apiKey || undefined,
        UserAgent: form.userAgent || undefined,
        Proxy: form.proxy || undefined,
        MaxConcurrency: form.maxConcurrency || undefined,
        DisableWindows: form.disableWindows.length > 0
          ? form.disableWindows.map(w => {
              const days = w.Days ?? []
              const compact = days.length === 0 || days.length === 7 ? undefined : [...new Set(days)].sort((a, b) => a - b)
              return compact === undefined
                ? { Start: w.Start, End: w.End }
                : { Start: w.Start, End: w.End, Days: compact }
            })
          : undefined,
        IsTokenPlan: form.isTokenPlan,
        TokenPlanExpiresAt: form.isTokenPlan ? (form.tokenPlanExpiresAt ? localInputToRfc3339(form.tokenPlanExpiresAt) : undefined) : undefined,
        TokenPlanRemainingPct: form.isTokenPlan ? form.tokenPlanRemainingPct : undefined,
        TokenPlanWindowMs: form.isTokenPlan ? form.tokenPlanWindowMs : undefined,
      })
      await loadAggregators()
      setDialogOpen(false)
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
      window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  const handlePresetChange = (presetId: string) => {
    if (presetId === 'custom') {
      setForm(prev => ({
        ...prev,
        presetId: 'custom',
        kind: 'auto',
      }))
      setFetchedModels([])
      setFetchError('')
      return
    }
    const preset = PRESETS.find(p => p.id === presetId)
    if (!preset) return
    setForm(prev => ({
      ...prev,
      presetId: preset.id,
      name: editingName ? prev.name : presetLabel(preset, t),
      kind: preset.kind,
      endpoint: preset.endpoint,
    }))
    setFetchedModels(preset.models)
    setFetchError('')
  }

  const handleFetchModels = async () => {
    if (!form.endpoint) {
      setFetchError(t('settings.provider.fetchError.noEndpoint'))
      return
    }
    setFetchingModels(true)
    setFetchError('')
    try {
      const resp = await aimanagerProvider.providerFetchModels(client, {
        Name: form.name || effectiveKind(form.kind, form.endpoint),
        Endpoint: form.endpoint,
        Kind: effectiveKind(form.kind, form.endpoint),
        AuthToken: form.apiKey || undefined,
        Proxy: form.proxy || undefined,
      })
      if (resp.Models.length === 0) {
        // Keep the current list: an empty upstream response must not wipe
        // configured models (e.g. endpoints without a /models listing).
        setFetchError(t('settings.provider.fetchError.empty'))
        return
      }
      const fetched = resp.Models.map(m => ({
        Name: m.Name,
        MaxContextLength: lookupModelDefault(m.Name, modelDefaultsTable) ?? DEFAULT_CONTEXT,
        MaxTokens: 16384,
        Modality: m.Modality,
        Protocol: m.Protocol,
      }))
      setFetchedModels(prev => {
        if (!prev || prev.length === 0) return fetched
        // Merge semantics are documented in mergeProviderModels: existing
        // entries keep their configured fields, new upstream models are
        // appended, and Modality/Protocol refresh only when still inferred.
        return mergeProviderModels(prev, fetched, effectiveKind(form.kind, form.endpoint))
      })
    } catch (err) {
      // A failed fetch (e.g. testing a new API key) must not clear the list.
      setFetchError(err instanceof Error ? err.message : String(err))
    } finally {
      setFetchingModels(false)
    }
  }

  const resolveModels = (): ProviderModel[] => {
    if (fetchedModels !== null) {
      return fetchedModels
    }
    // Editing/copying an existing provider must never fall back to a preset
    // (or empty) list — that is how a stray null silently wiped models on save.
    if (seededProviderName) {
      const seeded = providers.find(p => p.Name === seededProviderName)
      if (seeded) return seeded.Models
    }
    const preset = PRESETS.find(p => p.id === form.presetId)
    return preset?.models ?? []
  }

  const handleAddModel = useCallback(() => {
    const protocol = effectiveKind(form.kind, form.endpoint)
    setFetchedModels(prev => [...(prev ?? []), {
      Name: '',
      MaxContextLength: DEFAULT_CONTEXT,
      MaxTokens: 16384,
      Protocol: protocol,
    }])
  }, [form.kind, form.endpoint])

  const handleAddDisableWindow = useCallback(() => {
    setForm(prev => ({
      ...prev,
      disableWindows: [...prev.disableWindows, { Start: '09:00', End: '17:00' }],
    }))
    setDisableWindowError('')
  }, [])

  const handleRemoveDisableWindow = useCallback((index: number) => {
    setForm(prev => ({
      ...prev,
      disableWindows: prev.disableWindows.filter((_, itemIndex) => itemIndex !== index),
    }))
    setDisableWindowError('')
  }, [])

  const handleUpdateDisableWindow = useCallback((index: number, field: keyof ProviderDisableWindow, value: string) => {
    setForm(prev => ({
      ...prev,
      disableWindows: prev.disableWindows.map((window, itemIndex) => (
        itemIndex === index ? { ...window, [field]: value } : window
      )),
    }))
    setDisableWindowError('')
  }, [])

  const handleToggleDisableWindowDay = useCallback((index: number, day: number) => {
    setForm(prev => ({
      ...prev,
      disableWindows: prev.disableWindows.map((window, itemIndex) => {
        if (itemIndex !== index) return window
        const current = window.Days ?? []
        const selected = current.includes(day)
        const days = selected ? current.filter(d => d !== day) : [...current, day]
        return { ...window, Days: days }
      }),
    }))
    setDisableWindowError('')
  }, [])

  const handleRemoveModel = useCallback((index: number) => {
    setFetchedModels(prev => {
      if (!prev) return prev
      const next = [...prev]
      next.splice(index, 1)
      return next
    })
  }, [])

  const handleUpdateModelName = useCallback((index: number, value: string) => {
    setFetchedModels(prev => {
      if (!prev) return prev
      const next = [...prev]
      next[index] = { ...next[index]!, Name: value }
      return next
    })
  }, [])

  const handleUpdateModelContext = useCallback((index: number, value: string) => {
    const num = parseInt(value, 10)
    setFetchedModels(prev => {
      if (!prev) return prev
      const next = [...prev]
      next[index] = { ...next[index]!, MaxContextLength: Number.isNaN(num) ? 0 : num }
      return next
    })
  }, [])

  const handleUpdateModelMaxTokens = useCallback((index: number, value: string) => {
    const num = parseInt(value, 10)
    setFetchedModels(prev => {
      if (!prev) return prev
      const next = [...prev]
      next[index] = { ...next[index]!, MaxTokens: Number.isNaN(num) ? 0 : num }
      return next
    })
  }, [])

  const handleUpdateModelProtocol = useCallback((index: number, value: string) => {
    setFetchedModels(prev => {
      if (!prev) return prev
      const next = [...prev]
      next[index] = { ...next[index]!, Protocol: value }
      return next
    })
  }, [])

  const handleOpenAddAggregator = useCallback(() => {
    setEditingAggregatorId(null)
    setAggregatorDialogOpen(true)
  }, [])

  const handleOpenEditAggregator = useCallback((aggregator: AggregatorDescriptor) => {
    setEditingAggregatorId(aggregator.Id)
    setAggregatorDialogOpen(true)
  }, [])

  const handleDeleteAggregator = useCallback((aggregator: AggregatorDescriptor) => {
    showConfirm(
      t('settings.provider.confirm.deleteAggregator'),
      t('settings.provider.confirm.deleteAggregatorDesc', { name: aggregator.Name }),
      async () => {
        setSaving(true)
        setActionError('')
        try {
          await aimanagerAggregator.aggregatorConfigure(client, {
            Id: aggregator.Id,
            Name: '',
            Units: [],
          })
          await loadAggregators()
          window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
        } catch (err) {
          setActionError(err instanceof Error ? err.message : String(err))
        } finally {
          setSaving(false)
          setConfirmOpen(false)
        }
      }
    )
  }, [loadAggregators, showConfirm, t])

  const doToggleAggregatorDisabled = useCallback(async (aggregator: AggregatorDescriptor) => {
    setSaving(true)
    setActionError('')
    try {
      const resp = await aimanagerAggregator.aggregatorSetDisabled(client, {
        Id: aggregator.Id,
        Disabled: !aggregator.Disabled,
      })
      if (!resp.Ok) {
        setActionError(resp.Error || t('settings.provider.disable'))
        return
      }
      await loadAggregators()
      await reload(true)
      window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [loadAggregators, reload, t])

  const handleToggleAggregatorDisabled = useCallback((aggregator: AggregatorDescriptor) => {
    if (aggregator.Id === SYSTEM_AGGREGATOR_ID && !aggregator.Disabled) {
      showConfirm(
        t('settings.provider.confirm.disableSystemAggregator'),
        t('settings.provider.confirm.disableSystemAggregatorDesc'),
        () => void doToggleAggregatorDisabled(aggregator),
        { confirmLabel: t('settings.provider.disable'), danger: true },
      )
      return
    }
    void doToggleAggregatorDisabled(aggregator)
  }, [doToggleAggregatorDisabled, showConfirm, t])

  const handleExport = useCallback(async () => {
    setSaving(true)
    setActionError('')
    try {
      const resp = await aimanagerConfig.configExport(client)
      const blob = new Blob([resp.Data], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `sporemind-model-settings-${new Date().toISOString().slice(0, 10)}.json`
      link.click()
      URL.revokeObjectURL(url)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [])

  const handleImportFile = useCallback(async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return

    try {
      const data = await file.text()
      JSON.parse(data)
      showConfirm(
        t('settings.provider.importConfirmTitle'),
        t('settings.provider.importConfirmDesc'),
        async () => {
          setSaving(true)
          setActionError('')
          try {
            await aimanagerConfig.configImport(client, { Data: data })
            await Promise.all([reload(true), loadAggregators()])
            setSaved(true)
            setTimeout(() => setSaved(false), 2000)
            window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
          } catch (err) {
            setActionError(err instanceof Error ? err.message : String(err))
          } finally {
            setSaving(false)
            setConfirmOpen(false)
          }
        },
        { confirmLabel: t('settings.provider.import'), danger: true },
      )
    } catch (err) {
      setActionError(err instanceof Error ? err.message : t('settings.provider.importInvalid'))
    }
  }, [loadAggregators, reload, showConfirm, t])

  const handleAggregatorSaved = useCallback(async () => {
    await loadAggregators()
    window.dispatchEvent(new CustomEvent('sporemind:aggregators-changed'))
  }, [loadAggregators])

  return (
    <FeatureCard
      icon={section === 'aggregators' ? <Layers size={16} /> : section === 'defaults' ? <SlidersHorizontal size={16} /> : <Server size={16} />}
      title={section === 'aggregators' ? t('settings.provider.aggregators') : section === 'defaults' ? t('settings.provider.modelDefaults') : t('settings.provider.providers')}
      description={section === 'aggregators' ? t('settings.provider.aggregatorsDesc') : section === 'defaults' ? t('settings.provider.modelDefaults.desc') : t('settings.provider.desc')}
      action={
        <div className="flex items-center gap-2">
          {section === 'providers' ? (
            <>
              <Badge variant="secondary" className="text-xs">{providers.length}</Badge>
              <input
                ref={importInputRef}
                type="file"
                accept="application/json,.json"
                hidden
                onChange={(event) => void handleImportFile(event)}
                data-testid="provider-import-input"
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => importInputRef.current?.click()}
                disabled={saving || loading}
                data-testid="provider-import-btn"
                data-guide-id="settings/model-providers/import"
              >
                <Upload size={14} />
                {t('settings.provider.import')}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => void handleExport()}
                disabled={saving || loading}
                data-testid="provider-export-btn"
                data-guide-id="settings/model-providers/export"
              >
                <Download size={14} />
                {t('settings.provider.export')}
              </Button>
              <Button
                type="button"
                size="sm"
                onClick={handleOpenAdd}
                disabled={saving || loading}
                data-testid="provider-add-btn"
                data-guide-id="settings/model-providers/add"
              >
                <Plus size={14} />
                {t('settings.provider.addProvider')}
              </Button>
            </>
          ) : section === 'aggregators' ? (
            <>
              <Badge variant="secondary" className="text-xs">{customAggregators.length}</Badge>
              <Button
                type="button"
                size="sm"
                onClick={handleOpenAddAggregator}
                disabled={saving || aggregatorsLoading}
                data-guide-id="settings/model-aggregators/add"
              >
                {t('settings.provider.addCustom')}
              </Button>
            </>
          ) : (
            <>
              <Badge variant="secondary" className="text-xs">{defaults.length}</Badge>
              {defaultsSaved && (
                <span className="flex items-center gap-1 text-xs text-primary">
                  <Check size={14} />
                  {t('settings.provider.modelDefaults.saved')}
                </span>
              )}
              <Button
                type="button"
                size="sm"
                onClick={() => void saveModelDefaults()}
                disabled={!defaultsDirty || defaultsSaving || defaultsLoading}
                data-testid="defaults-save-btn"
                data-guide-id="settings/model-defaults/save"
              >
                {defaultsSaving ? <Loader2 size={14} className="spin" /> : <Check size={14} />}
                {t('settings.provider.modelDefaults.save')}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={addDefaultRow}
                disabled={defaultsSaving || defaultsLoading}
                data-testid="defaults-add-btn"
                data-guide-id="settings/model-defaults/add"
              >
                <Plus size={14} />
                {t('settings.provider.modelDefaults.add')}
              </Button>
            </>
          )}
        </div>
      }
    >
      {(providerError || actionError) && (
        <div className="shell-provider-settings-error">
          <AlertCircle size={14} />
          <span>{actionError || providerError}</span>
        </div>
      )}

      {section === 'providers' ? (
        <div className="shell-provider-page" role="tabpanel" data-testid="provider-models-panel">

          {loading ? (
            <div className="shell-provider-settings-loading">
              <Loader2 size={16} className="spin" />
              <span>{t('settings.provider.loading')}</span>
            </div>
          ) : (
            <>
            {providers.length === 0 ? (
              <div className="shell-provider-empty">
                {t('settings.provider.emptyProvider')}
              </div>
            ) : (
              <div className="shell-provider-list">
                {providers.map(provider => {
                  const proj: HealthProjection = {
                    healthState: provider.HealthState,
                    healthReason: provider.HealthReason,
                    cooldownUntil: provider.DisableUntil || provider.CooldownUntil,
                    lastFailureAt: provider.LastFailureAt,
                    recoveryMode: provider.RecoveryMode,
                  }
                  const cd = isCoolingDown(proj, now) && proj.cooldownUntil
                    ? formatCountdown(proj.cooldownUntil, now)
                    : null
                  const scheduleLabel = cd && provider.DisableUntil && (!provider.CooldownUntil || provider.DisableUntil >= provider.CooldownUntil)
                    ? t('health.reason.disable_window') : ''
                  const cooldownTitle = cd
                    ? scheduleLabel
                      ? `${t('health.coolingDown', { countdown: cd })} · ${scheduleLabel}`
                      : t('health.coolingDown', { countdown: cd })
                    : ''
                  const dis = isDisabled(proj)
                  const reason = dis
                    ? t(`health.reason.${healthReasonLabelKey(provider.HealthReason)}` as any)
                    : ''
                  const reasonLabel = provider.HealthReason === 'provider_offline' ? t('health.reason.provider_offline' as any) : reason
                  const recoveryLabel = dis && provider.RecoveryMode
                    ? t(`health.recovery.${recoveryModeLabelKey(provider.RecoveryMode)}` as any)
                    : ''
                  const expanded = expandedProvider === provider.Name
                  const probeTarget = aggregators.length > 0
                  return (
                    <div className={`shell-provider-card${provider.Disabled ? ' is-disabled' : ''}${dis ? ' is-unhealthy' : ''}${expanded ? ' is-open' : ''}`} key={provider.Name}>
                      <div className="shell-provider-card-header"
                        role="button"
                        tabIndex={0}
                        aria-expanded={expanded}
                        data-testid={`provider-card-${provider.Name}`}
                        onClick={() => toggleProviderExpanded(provider.Name)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault()
                            toggleProviderExpanded(provider.Name)
                          }
                        }}
                      >
                        <div className="shell-provider-card-header-info">
                          <span className="shell-provider-card-chevron">
                            <ChevronDown size={14} className={`${expanded ? 'is-open' : ''}`} />
                          </span>
                          <span className="shell-provider-card-name">
                            <ProviderIcon id={iconKeyForUnit(provider.Name, undefined, provider.Endpoint)} size={16} />
                            <span className="shell-provider-card-name-text" title={provider.Name}>
                              {provider.Name}
                            </span>
                            <span className="shell-provider-kind">{provider.Kind}</span>
                          </span>
                          <span className="shell-provider-card-meta">
                            <span className="shell-provider-card-endpoint" title={provider.Endpoint}>
                              {provider.Endpoint}
                            </span>
                            <span className="shell-provider-card-models">{provider.Models.length}</span>
                            <span className={`shell-provider-card-key ${provider.HasAuthToken ? 'ok' : 'missing'}`}>
                              {provider.HasAuthToken ? t('settings.provider.keyConfigured') : t('settings.provider.keyNotConfigured')}
                            </span>
                          </span>
                        </div>
                        <span className="shell-provider-card-badges">
                          {cd && (
                            <span className="shell-provider-cooldown-badge" title={cooldownTitle} aria-label={cooldownTitle}>
                              <Clock size={12} />
                              {cd}
                            </span>
                          )}
                          {dis && (
                            <span className="shell-provider-disabled-badge" title={`${reasonLabel}${recoveryLabel ? ' · ' + recoveryLabel : ''}`} aria-label={`${reasonLabel}${recoveryLabel ? ' · ' + recoveryLabel : ''}`}>
                              <AlertCircle size={12} />
                              {reasonLabel}
                            </span>
                          )}
                        </span>
                        <span className="shell-provider-card-actions">
                          <span
                            className="shell-provider-card-switch"
                            onClick={(e) => e.stopPropagation()}
                            title={provider.Disabled ? t('settings.provider.enable') : t('settings.provider.disable')}
                          >
                            <Switch
                              size="sm"
                              checked={!provider.Disabled}
                              disabled={saving}
                              onCheckedChange={() => void handleToggleProviderDisabled(provider)}
                              onClick={(e) => e.stopPropagation()}
                              aria-label={provider.Disabled ? t('settings.provider.enable') : t('settings.provider.disable')}
                              data-testid={`provider-disable-switch-${provider.Name}`}
                            />
                          </span>
                          {(dis || cd) && (
                            <button
                              type="button"
                              className="shell-provider-action"
                              onClick={(e) => { e.stopPropagation(); void handleResetHealth(provider) }}
                              disabled={saving}
                              title={t('health.reset')}
                              aria-label={t('health.reset')}
                              data-testid={`provider-reset-health-btn-${provider.Name}`}
                            >
                              <RotateCcw size={14} />
                            </button>
                          )}
                          <button
                            type="button"
                            className="shell-provider-action"
                            onClick={(e) => { e.stopPropagation(); void handleProbeProvider(provider) }}
                            disabled={!probeTarget || probingProviderName !== null || provider.Models.length === 0}
                            title={probeTarget ? t('settings.provider.probe') : t('settings.provider.probeNoAggregator')}
                            aria-label={t('settings.provider.probe')}
                            data-testid={`provider-probe-btn-${provider.Name}`}
                          >
                            {probingProviderName === provider.Name
                              ? <Loader2 size={14} className="spin" />
                              : <Radar size={14} />}
                          </button>
                          <button
                            type="button"
                            className="shell-provider-action"
                            onClick={(e) => { e.stopPropagation(); void handleCopy(provider) }}
                            disabled={saving}
                            title={t('common.copy')}
                            data-testid={`provider-copy-btn-${provider.Name}`}
                          >
                            <Copy size={14} />
                          </button>
                          <button
                            type="button"
                            className="shell-provider-action"
                            onClick={(e) => { e.stopPropagation(); handleOpenEdit(provider) }}
                            disabled={saving}
                            title={t('common.edit')}
                            data-testid={`provider-edit-btn-${provider.Name}`}
                          >
                            <Pencil size={14} />
                          </button>
                          <button
                            type="button"
                            className="shell-provider-action"
                            onClick={(e) => { e.stopPropagation(); void handleDelete(provider) }}
                            disabled={saving}
                            title={t('common.delete')}
                          >
                            <Trash2 size={14} />
                          </button>
                        </span>
                      </div>
                      {expanded && (
                        <div className="shell-provider-card-body">
                      {provider.Models.length === 0 && (
                          <div className="shell-provider-unit-empty">
                            {t('settings.provider.noModels')}
                          </div>
                      )}
                      {provider.Models.map(model => {
                        const unitView = systemUnits.find(
                          u => u.ProviderName === provider.Name && u.Model === model.Name
                        )
                        const unitProj: HealthProjection = {
                          healthState: unitView?.HealthState,
                          healthReason: unitView?.HealthReason,
                          cooldownUntil: unitView?.DisableUntil || unitView?.CooldownUntil,
                          lastFailureAt: unitView?.LastFailureAt,
                          recoveryMode: unitView?.RecoveryMode,
                        }
                        const ucd = isCoolingDown(unitProj, now) && unitProj.cooldownUntil
                          ? formatCountdown(unitProj.cooldownUntil, now)
                          : null
                        const scheduleLabel = ucd && unitView?.DisableUntil && (!unitView?.CooldownUntil || unitView.DisableUntil >= unitView.CooldownUntil)
                          ? t('health.reason.disable_window') : ''
                        const cooldownTitle = ucd
                          ? scheduleLabel
                            ? `${t('health.coolingDown', { countdown: ucd })} · ${scheduleLabel}`
                            : t('health.coolingDown', { countdown: ucd })
                          : ''
                        const udis = isDisabled(unitProj)
                        const ureason = udis
                          ? t(`health.reason.${healthReasonLabelKey(unitView?.HealthReason)}` as any)
                          : ''
                        const isImage = isImageModel(model)
                        const probe = unitProbes[unitProbeKey(provider.Name, model.Name)]
                        const isEditing = editingUnit?.provider === provider.Name && editingUnit.model === model.Name

                        if (isEditing) {
                          return (
                              <div className="shell-provider-unit-row is-editing" data-testid={`provider-unit-editing-${provider.Name}-${model.Name}`} key={model.Name}>
                                <span className="shell-provider-unit-name">
                                  <input
                                    className="shell-provider-unit-input"
                                    value={unitDraft.name}
                                    onChange={(e) => setUnitDraft(prev => ({ ...prev, name: e.target.value }))}
                                    disabled={saving}
                                    placeholder={t('settings.provider.dialog.modelNamePlaceholder')}
                                    aria-label={t('settings.provider.dialog.modelName')}
                                    data-testid={`provider-unit-name-input-${provider.Name}`}
                                  />
                                </span>
                                <span className="shell-provider-unit-kind">
                                  <select
                                    className="shell-provider-unit-input shell-provider-unit-select"
                                    value={unitDraft.protocol}
                                    onChange={(e) => setUnitDraft(prev => ({ ...prev, protocol: e.target.value }))}
                                    disabled={saving}
                                    aria-label={t('settings.provider.dialog.modelProtocol')}
                                  >
                                    <option value="openai">OpenAI</option>
                                    <option value="anthropic">Anthropic</option>
                                    <option value="gemini">Gemini</option>
                                    <option value="endpoint">Endpoint</option>
                                    <option value="responses">Responses</option>
                                  </select>
                                </span>
                                <span className="shell-provider-unit-probe" />
                                <span className="shell-provider-unit-meta">
                                  <input
                                    type="number"
                                    min={0}
                                    step={1024}
                                    className="shell-provider-unit-input"
                                    value={unitDraft.maxContextLength}
                                    onChange={(e) => setUnitDraft(prev => ({ ...prev, maxContextLength: e.target.value }))}
                                    disabled={saving}
                                    placeholder={t('settings.provider.dialog.modelContext')}
                                    aria-label={t('settings.provider.dialog.modelContext')}
                                  />
                                </span>
                                <span className="shell-provider-unit-meta">
                                  <input
                                    type="number"
                                    min={0}
                                    step={1024}
                                    className="shell-provider-unit-input"
                                    value={unitDraft.maxTokens}
                                    onChange={(e) => setUnitDraft(prev => ({ ...prev, maxTokens: e.target.value }))}
                                    disabled={saving}
                                    placeholder={t('settings.provider.dialog.modelMaxTokens')}
                                    aria-label={t('settings.provider.dialog.modelMaxTokens')}
                                  />
                                </span>
                                <span className="shell-provider-unit-actions">
                                  <button
                                    type="button"
                                    className="shell-provider-unit-btn"
                                    onClick={() => void handleSaveUnit(provider, model)}
                                    disabled={saving}
                                    title={t('common.save')}
                                    data-testid={`provider-unit-save-${provider.Name}`}
                                  >
                                    <Check size={13} />
                                  </button>
                                  <button
                                    type="button"
                                    className="shell-provider-unit-btn"
                                    onClick={handleCancelEditUnit}
                                    disabled={saving}
                                    title={t('common.cancel')}
                                    data-testid={`provider-unit-cancel-${provider.Name}`}
                                  >
                                    <X size={13} />
                                  </button>
                                </span>
                              </div>
                          )
                        }

                        let probeStatus: ReactNode = null
                        let probeTitle: string | undefined
                        const probeAgo = model.ProbeAt
                          ? formatRelativeTime(new Date(model.ProbeAt * 1000).toISOString(), locale)
                          : ''
                        if (probe?.status === 'probing') {
                          probeStatus = (
                            <>
                              <Loader2 size={12} className="spin" />
                              {t('settings.provider.probing')}
                            </>
                          )
                        } else if (probe?.status === 'ok') {
                          probeStatus = (
                            <>
                              <Check size={12} />
                              {t('settings.provider.probeOk', { ms: probe.latencyMs })}
                            </>
                          )
                        } else if (probe?.status === 'failed') {
                          probeStatus = (
                            <>
                              <AlertCircle size={12} />
                              {t('settings.provider.probeFailed')}
                            </>
                          )
                          probeTitle = probe.error
                        } else if (model.ProbeState === 'ok') {
                          probeStatus = (
                            <>
                              <Check size={12} />
                              {t('settings.provider.probeOk', { ms: model.ProbeLatencyMs ?? 0 })}
                              {probeAgo && <span className="shell-provider-unit-probe-ago">· {probeAgo}</span>}
                            </>
                          )
                        } else if (model.ProbeState === 'failed') {
                          probeStatus = (
                            <>
                              <AlertCircle size={12} />
                              {t('settings.provider.probeFailed')}
                              {probeAgo && <span className="shell-provider-unit-probe-ago">· {probeAgo}</span>}
                            </>
                          )
                          probeTitle = model.ProbeError
                        }
                        return (
                          <div className={`shell-provider-unit-row${model.Disabled ? ' is-disabled' : ''}`} key={model.Name} data-testid={`provider-unit-${provider.Name}-${model.Name}`}>
                              <span className="shell-provider-unit-name">
                                {(() => {
                                  const k = iconKeyForModelRow(model.Name, provider.Endpoint, provider.Name)
                                  return k ? <ProviderIcon id={k} size={13} className="shell-provider-unit-icon" /> : null
                                })()}
                                <span className="shell-provider-unit-name-text" title={model.Name}>{model.Name}</span>
                                {isImage && (
                                  <span className="shell-provider-unit-image-badge">
                                    {t('settings.provider.dialog.modelBadgeImage')}
                                  </span>
                                )}
                                {isVideoModel(model) && (
                                  <span className="shell-provider-unit-image-badge">
                                    {t('settings.provider.dialog.modelBadgeVideo')}
                                  </span>
                                )}
                              </span>
                              <span className="shell-provider-unit-kind">
                                <span className="shell-provider-kind">{model.Protocol || provider.Kind}</span>
                              </span>
                              <span className="shell-provider-unit-probe">
                                <span className={`shell-provider-unit-probe-status ${probe?.status ?? model.ProbeState ?? ''}`} title={probeTitle}>
                                  {probeStatus}
                                </span>
                                {ucd && (
                                  <span className="shell-provider-unit-badge shell-provider-unit-badge--cooldown" title={cooldownTitle} aria-label={cooldownTitle}>
                                    <Clock size={11} />
                                    {ucd}
                                  </span>
                                )}
                                {udis && (
                                  <span className="shell-provider-unit-badge shell-provider-unit-badge--disabled" title={ureason}>
                                    <AlertCircle size={11} />
                                    {ureason}
                                  </span>
                                )}
                              </span>
                              <span className="shell-provider-unit-meta" title={t('settings.provider.dialog.modelContext')}>
                                {fmtCompactNum(model.MaxContextLength)}
                              </span>
                              <span className="shell-provider-unit-meta" title={t('settings.provider.dialog.modelMaxTokens')}>
                                {fmtCompactNum(model.MaxTokens)}
                              </span>
                              <span className="shell-provider-unit-actions">
                                <span
                                  className="shell-provider-unit-switch"
                                  onClick={(e) => e.stopPropagation()}
                                  title={model.Disabled ? t('settings.provider.enable') : t('settings.provider.disable')}
                                >
                                  <Switch
                                    size="sm"
                                    checked={!model.Disabled}
                                    disabled={saving}
                                    onCheckedChange={() => void handleToggleUnitDisabled(provider, model)}
                                    onClick={(e) => e.stopPropagation()}
                                    aria-label={model.Disabled ? t('settings.provider.enable') : t('settings.provider.disable')}
                                    data-testid={`provider-unit-disable-switch-${provider.Name}-${model.Name}`}
                                  />
                                </span>
                                <button
                                  type="button"
                                  className="shell-provider-unit-btn"
                                  onClick={() => void runUnitProbe(provider.Name, model.Name)}
                                  disabled={isImage || !probeTarget || probingProviderName !== null}
                                  title={isImage ? t('settings.provider.dialog.modelBadgeImage') : t('settings.provider.probe')}
                                >
                                  <Radar size={12} />
                                </button>
                                <button
                                  type="button"
                                  className="shell-provider-unit-btn"
                                  onClick={() => handleEditUnit(provider, model)}
                                  disabled={saving}
                                  title={t('common.edit')}
                                  data-testid={`provider-unit-edit-${provider.Name}-${model.Name}`}
                                >
                                  <Pencil size={12} />
                                </button>
                                <button
                                  type="button"
                                  className="shell-provider-unit-btn"
                                  onClick={() => void handleRemoveUnit(provider, model)}
                                  disabled={saving}
                                  title={t('common.delete')}
                                  data-testid={`provider-unit-delete-${provider.Name}-${model.Name}`}
                                >
                                  <Trash2 size={12} />
                                </button>
                              </span>
                            </div>
                        )
                      })}
                    </div>
                )}
              </div>
            )
          })}
        </div>
            )}
            </>
          )}
        </div>
      ) : section === 'aggregators' ? (
        <div className="shell-provider-page" role="tabpanel" data-testid="provider-aggregators-panel">

            {aggregatorsLoading ? (
              <div className="shell-provider-loading-inline">
                <Loader2 size={14} className="spin" />
                <span>{t('settings.provider.loading')}</span>
              </div>
            ) : (
              <div className="shell-aggregator-list">
                {/* The system auto aggregator is intentionally NOT listed here:
                    it is not user-managed; its models show under each provider
                    and it keeps serving routes regardless. */}

                {customAggregators.map(aggregator => (
                  <div key={aggregator.Id} className={`shell-aggregator-card${aggregator.Disabled ? ' is-disabled' : ''}`} data-testid={`aggregator-card-${aggregator.Id}`}>
                    <div className="shell-aggregator-card-main">
                      <div className="shell-aggregator-card-title">
                        <span>{aggregator.Name}</span>
                        {(() => {
                          const keys = iconKeysForModels(aggregatorModels[aggregator.Id] ?? [], 3)
                          return keys.length ? (
                            <span className="shell-aggregator-icon-stack" aria-hidden="true">
                              {keys.map(k => <ProviderIcon key={k} id={k} size={13} />)}
                            </span>
                          ) : null
                        })()}
                        {aggregator.Disabled && (
                          <span className="shell-aggregator-badge shell-aggregator-badge--disabled" data-testid={`aggregator-disabled-badge-${aggregator.Id}`}>
                            {t('settings.provider.disabled')}
                          </span>
                        )}
                        <span className="shell-aggregator-badge shell-aggregator-badge--custom">{t('settings.provider.customBadge')}</span>
                        <span className="shell-aggregator-badge shell-aggregator-badge--strategy">{t(strategyDisplay(aggregator.Strategy) as any)}</span>
                      </div>
                      <div className="shell-aggregator-card-meta">{aggregator.ActorId}</div>
                    </div>
                    <div className="shell-aggregator-card-actions">
                      <Switch
                        size="sm"
                        checked={!aggregator.Disabled}
                        disabled={saving}
                        onCheckedChange={() => void handleToggleAggregatorDisabled(aggregator)}
                        title={aggregator.Disabled ? t('settings.provider.enable') : t('settings.provider.disable')}
                        aria-label={aggregator.Disabled ? t('settings.provider.enable') : t('settings.provider.disable')}
                        data-testid={`aggregator-disable-switch-${aggregator.Id}`}
                      />
                      <button
                        type="button"
                        className="shell-provider-action"
                        onClick={() => handleOpenEditAggregator(aggregator)}
                        disabled={saving}
                        title={t('common.edit')}
                      >
                        <Pencil size={14} />
                      </button>
                      <button
                        type="button"
                        className="shell-provider-action"
                        onClick={() => void handleDeleteAggregator(aggregator)}
                        disabled={saving}
                        title={t('common.delete')}
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  </div>
                ))}

                {customAggregators.length === 0 && (
                  <div className="shell-provider-empty">
                    {t('settings.provider.emptyAggregator')}
                  </div>
                )}
              </div>
            )}
        </div>
      ) : (
        <div className="shell-provider-page" role="tabpanel" data-testid="provider-defaults-panel">

          {defaultsError && (
            <div className="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert" data-testid="defaults-error">
              <AlertCircle size={14} />
              {defaultsError}
            </div>
          )}

          {defaultsLoading && defaults.length === 0 ? (
            <div className="flex items-center justify-center gap-2 py-8 text-sm text-muted-foreground" data-testid="defaults-loading">
              <Loader2 size={16} className="animate-spin" />
              {t('settings.provider.loading')}
            </div>
          ) : defaults.length === 0 ? (
            <div className="py-8 text-center text-sm text-muted-foreground" data-testid="defaults-empty">
              {t('settings.provider.modelDefaults.empty')}
            </div>
          ) : (
            <div className="flex flex-col gap-1.5">
              <div className="grid grid-cols-[1fr_96px_96px_120px_72px_32px] gap-1.5 px-0.5 text-xs text-muted-foreground" role="rowgroup">
                <span>{t('settings.provider.modelDefaults.col.prefix')}</span>
                <span>{t('settings.provider.modelDefaults.col.context')}</span>
                <span>{t('settings.provider.modelDefaults.col.maxTokens')}</span>
                <span>{t('settings.provider.modelDefaults.col.modality')}</span>
                <span>{t('settings.provider.modelDefaults.col.source')}</span>
                <span />
              </div>
              {defaults.map((row, index) => (
                <div
                  key={`${row.Prefix}-${index}`}
                  className="grid grid-cols-[1fr_96px_96px_120px_72px_32px] items-center gap-1.5"
                >
                  <div className="flex min-w-0 items-center gap-1.5">
                    {(() => {
                      const k = iconKeyForModel(row.Prefix)
                      return k ? <ProviderIcon id={k} size={13} className="shrink-0" /> : null
                    })()}
                    <Input
                      type="text"
                      className="font-mono min-w-0"
                      value={row.Prefix}
                      placeholder="glm-5.2"
                      onChange={(e) => updateDefaultRow(index, { Prefix: e.target.value })}
                      aria-label={t('settings.provider.modelDefaults.col.prefix')}
                      data-testid={`defaults-prefix-${index}`}
                      disabled={defaultsSaving}
                    />
                  </div>
                  <Input
                    type="number"
                    value={row.MaxContextLength || ''}
                    min={1}
                    onChange={(e) => updateDefaultRow(index, { MaxContextLength: Number(e.target.value) })}
                    aria-label={t('settings.provider.modelDefaults.col.context')}
                    data-testid={`defaults-context-${index}`}
                    disabled={defaultsSaving}
                  />
                  <Input
                    type="number"
                    value={isUnsetMaxTokens(row.MaxTokens) ? '' : row.MaxTokens}
                    min={0}
                    placeholder={t('settings.provider.modelDefaults.optional')}
                    onChange={(e) =>
                      updateDefaultRow(index, { MaxTokens: e.target.value === '' ? undefined : Number(e.target.value) })
                    }
                    aria-label={t('settings.provider.modelDefaults.col.maxTokens')}
                    data-testid={`defaults-maxtokens-${index}`}
                    disabled={defaultsSaving}
                  />
                  <SelectRoot
                    value={isUnsetModality(row.Modality) ? '' : row.Modality}
                    onValueChange={(v) => updateDefaultRow(index, { Modality: !v ? undefined : v })}
                    disabled={defaultsSaving}
                    items={[
                      { value: '', label: t('settings.provider.modelDefaults.modalityUnset') },
                      { value: 'chat', label: t('settings.provider.modelDefaults.modalityChat') },
                      { value: 'image', label: t('settings.provider.modelDefaults.modalityImage') },
                      { value: 'video', label: t('settings.provider.modelDefaults.modalityVideo') },
                    ]}
                  >
                    <SelectTrigger aria-label={t('settings.provider.modelDefaults.col.modality')} data-testid={`defaults-modality-${index}`}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value=""><SelectItemText>{t('settings.provider.modelDefaults.modalityUnset')}</SelectItemText></SelectItem>
                      <SelectItem value="chat"><SelectItemText>{t('settings.provider.modelDefaults.modalityChat')}</SelectItemText></SelectItem>
                      <SelectItem value="image"><SelectItemText>{t('settings.provider.modelDefaults.modalityImage')}</SelectItemText></SelectItem>
                      <SelectItem value="video"><SelectItemText>{t('settings.provider.modelDefaults.modalityVideo')}</SelectItemText></SelectItem>
                    </SelectContent>
                  </SelectRoot>
                  <Badge variant={isBuiltinDefault(row) ? 'secondary' : 'outline'}>
                    {isBuiltinDefault(row)
                      ? t('settings.provider.modelDefaults.builtin')
                      : t('settings.provider.modelDefaults.user')}
                  </Badge>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => removeDefaultRow(index)}
                    disabled={defaultsSaving}
                    title={t('common.delete')}
                    aria-label={t('common.delete')}
                    data-testid={`defaults-remove-${index}`}
                    className="text-destructive hover:text-destructive"
                  >
                    <Trash2 size={14} />
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {dialogOpen && (
        <div className="shell-provider-dialog-overlay">
          <div className="shell-provider-dialog">
            <div className="shell-provider-dialog-header">
              <span>{editingName ? t('settings.provider.dialog.editTitle') : t('settings.provider.dialog.addTitle')}</span>
              <button
                type="button"
                className="shell-provider-dialog-close"
                onClick={() => setDialogOpen(false)}
                disabled={saving}
              >
                <X size={16} />
              </button>
            </div>
            <div className="shell-provider-dialog-body">
              <label className="shell-provider-field">
                <span className="shell-provider-label">{t('settings.provider.dialog.preset')}</span>
                <PresetSelect
                  value={form.presetId}
                  onChange={handlePresetChange}
                  disabled={!!editingName || saving}
                />
              </label>

              {form.presetId === 'custom' && (
                <div className="shell-provider-field">
                  <span className="shell-provider-label">{t('settings.provider.dialog.kind')}</span>
                  <div className="shell-provider-select-wrap">
                    <select
                      className="shell-provider-select"
                      value={form.kind}
                      onChange={(e) => setForm(prev => ({ ...prev, kind: e.target.value as ProviderFormData['kind'] }))}
                      disabled={saving}
                    >
                      <option value="auto">{t('settings.provider.dialog.kindAuto')}</option>
                      <option value="openai">{t('settings.provider.dialog.kindOpenAI')}</option>
                      <option value="anthropic">{t('settings.provider.dialog.kindAnthropic')}</option>
                      <option value="gemini">{t('settings.provider.dialog.kindGemini')}</option>
                      <option value="responses">{t('settings.provider.dialog.kindResponses')}</option>
                    </select>
                    <ChevronDown size={14} className="shell-provider-select-icon" />
                  </div>
                </div>
              )}

              <label className="shell-provider-field">
                <span className="shell-provider-label">{t('settings.provider.dialog.name')}</span>
                <input
                  className="shell-provider-input"
                  type="text"
                  value={form.name}
                  onChange={(e) => setForm(prev => ({ ...prev, name: e.target.value }))}
                  placeholder={t('settings.provider.dialog.namePlaceholder')}
                  disabled={saving}
                />
              </label>

              <label className="shell-provider-field">
                <span className="shell-provider-label">{t('settings.provider.dialog.endpoint')}</span>
                <input
                  className="shell-provider-input"
                  type="text"
                  value={form.endpoint}
                  onChange={(e) => {
                    const endpoint = e.target.value
                    setForm(prev => prev.presetId === 'custom'
                      ? { ...prev, endpoint, kind: 'auto' }
                      : { ...prev, endpoint })
                    // Editing/copying keeps the model list; only the add flow
                    // resets to preset defaults.
                    if (!seededProviderName) setFetchedModels(null)
                    setFetchError('')
                  }}
                  placeholder={t('settings.provider.dialog.endpointPlaceholder')}
                  disabled={saving}
                />
              </label>

              <label className="shell-provider-field">
                <span className="shell-provider-label">{t('settings.provider.dialog.apiKey')}</span>
                <input
                  className="shell-provider-input"
                  type="password"
                  value={form.apiKey}
                  onChange={(e) => {
                    setForm(prev => ({ ...prev, apiKey: e.target.value }))
                    // Editing/copying keeps the model list; only the add flow
                    // resets to preset defaults.
                    if (!seededProviderName) setFetchedModels(null)
                    setFetchError('')
                  }}
                  placeholder={editingName ? t('settings.provider.dialog.apiKeyEditPlaceholder') : t('settings.provider.dialog.apiKeyPlaceholder')}
                  disabled={saving}
                />
              </label>

              {userAgentVisible && (
                <label className="shell-provider-field">
                  <span className="shell-provider-label">{t('settings.provider.dialog.userAgent')}</span>
                  <input
                    className="shell-provider-input"
                    type="text"
                    value={form.userAgent}
                    onChange={(e) => setForm(prev => ({ ...prev, userAgent: e.target.value }))}
                    placeholder={t('settings.provider.dialog.userAgentPlaceholder')}
                    disabled={saving}
                  />
                </label>
              )}

              <label className="shell-provider-field">
                <span className="shell-provider-label">{t('settings.provider.dialog.proxy')}</span>
                <input
                  className="shell-provider-input"
                  type="text"
                  value={form.proxy}
                  onChange={(e) => setForm(prev => ({ ...prev, proxy: e.target.value }))}
                  placeholder={t('settings.provider.dialog.proxyPlaceholder')}
                  disabled={saving}
                />
              </label>

              <label className="shell-provider-field">
                <span className="shell-provider-label">{t('settings.provider.dialog.maxConcurrency')}</span>
                <input
                  className="shell-provider-input"
                  type="number"
                  min={0}
                  step={1}
                  value={form.maxConcurrency ?? ''}
                  onChange={(e) => {
                    const value = e.target.value
                    const num = value === '' ? undefined : parseInt(value, 10)
                    setForm(prev => ({ ...prev, maxConcurrency: num && num > 0 ? num : undefined }))
                  }}
                  placeholder={t('settings.provider.dialog.maxConcurrencyPlaceholder')}
                  disabled={saving}
                />
              </label>

              <div className="shell-provider-token-plan">
                <button
                  type="button"
                  className="shell-provider-token-plan-toggle"
                  onClick={() => setTokenPlanOpen(prev => !prev)}
                  disabled={saving}
                  aria-expanded={tokenPlanOpen}
                >
                  <Gauge size={14} />
                  <span>{t('settings.provider.tokenPlan' as any)}</span>
                  <span className={`shell-provider-token-plan-state ${form.isTokenPlan ? 'is-enabled' : 'is-disabled'}`}>
                    {form.isTokenPlan ? t('settings.provider.tokenPlan.enabled' as any) : t('settings.provider.tokenPlan.disabled' as any)}
                  </span>
                  {tokenPlanOpen ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
                </button>
                {tokenPlanOpen && (
                  <div className="shell-provider-token-plan-body">
                    <label className="shell-provider-field shell-provider-checkbox">
                      <span className="shell-provider-label">{t('settings.provider.tokenPlan.enabled' as any)}</span>
                      <input
                        className="shell-provider-input shell-provider-checkbox-input"
                        type="checkbox"
                        checked={form.isTokenPlan}
                        onChange={(e) => setForm(prev => ({ ...prev, isTokenPlan: e.target.checked }))}
                        disabled={saving}
                      />
                    </label>
                    {form.isTokenPlan && (
                      <>
                        <label className="shell-provider-field">
                          <span className="shell-provider-label">{t('settings.provider.tokenPlan.expiresAt' as any)}</span>
                          <input
                            className="shell-provider-input"
                            type="datetime-local"
                            value={form.tokenPlanExpiresAt}
                            onChange={(e) => setForm(prev => ({ ...prev, tokenPlanExpiresAt: e.target.value }))}
                            disabled={saving}
                          />
                        </label>
                        <div className="shell-provider-token-plan-row">
                          <label className="shell-provider-field">
                            <span className="shell-provider-label">{t('settings.provider.tokenPlan.remainingPct' as any)}</span>
                            <input
                              className="shell-provider-input"
                              type="number"
                              min={0}
                              max={100}
                              step={1}
                              value={form.tokenPlanRemainingPct}
                              onChange={(e) => {
                                const num = parseInt(e.target.value, 10)
                                setForm(prev => ({ ...prev, tokenPlanRemainingPct: Number.isNaN(num) ? 0 : num }))
                              }}
                              disabled={saving}
                            />
                          </label>
                          <label className="shell-provider-field">
                            <span className="shell-provider-label">{t('settings.provider.tokenPlan.windowMs' as any)}</span>
                            <input
                              className="shell-provider-input"
                              type="number"
                              min={0}
                              step={1}
                              value={form.tokenPlanWindowMs}
                              onChange={(e) => {
                                const num = parseInt(e.target.value, 10)
                                setForm(prev => ({ ...prev, tokenPlanWindowMs: Number.isNaN(num) ? 0 : num }))
                              }}
                              disabled={saving}
                            />
                          </label>
                        </div>
                      </>
                    )}
                  </div>
                )}
              </div>

              <div className="shell-provider-field">
                <div className="shell-provider-field-header">
                  <span className="shell-provider-label">{t('settings.provider.disableWindows')}</span>
                  <button
                    type="button"
                    className="shell-provider-add-model-btn"
                    onClick={handleAddDisableWindow}
                    disabled={saving}
                    data-testid="provider-disable-window-add"
                  >
                    <Plus size={12} />
                    {t('settings.provider.disableWindows.add')}
                  </button>
                </div>
                <span className="shell-provider-label-hint">{t('settings.provider.disableWindows.hint')}</span>
                {disableWindowError && (
                  <div className="shell-provider-fetch-error" role="alert" data-testid="provider-disable-window-error">
                    {disableWindowError}
                  </div>
                )}
                {form.disableWindows.length === 0 ? (
                  <div className="shell-provider-models-empty">
                    {t('settings.provider.disableWindows.empty')}
                  </div>
                ) : (
                  <div className="shell-provider-disable-window-list">
                    {form.disableWindows.map((window, index) => (
                      <div key={index} className="shell-provider-disable-window-row">
                        <label className="shell-provider-disable-window-field">
                          <span>{t('settings.provider.disableWindows.start')}</span>
                          <input
                            type="time"
                            className="shell-provider-disable-window-input"
                            value={window.Start}
                            onChange={(e) => handleUpdateDisableWindow(index, 'Start', e.target.value)}
                            disabled={saving}
                            aria-label={t('settings.provider.disableWindows.start')}
                          />
                        </label>
                        <span className="shell-provider-disable-window-sep" aria-hidden="true">-</span>
                        <label className="shell-provider-disable-window-field">
                          <span>{t('settings.provider.disableWindows.end')}</span>
                          <input
                            type="time"
                            className="shell-provider-disable-window-input"
                            value={window.End}
                            onChange={(e) => handleUpdateDisableWindow(index, 'End', e.target.value)}
                            disabled={saving}
                            aria-label={t('settings.provider.disableWindows.end')}
                          />
                        </label>
                        <div className="shell-provider-disable-window-days">
                          {[1, 2, 3, 4, 5, 6, 7].map(day => {
                            const selected = (window.Days ?? []).includes(day)
                            const key = DISABLE_WINDOW_DAY_KEYS[day - 1]!
                            return (
                              <button
                                key={day}
                                type="button"
                                className={`shell-provider-disable-window-day${selected ? ' shell-provider-disable-window-day--selected' : ''}`}
                                onClick={() => handleToggleDisableWindowDay(index, day)}
                                disabled={saving}
                                aria-pressed={selected}
                                title={t(key)}
                                aria-label={t(key)}
                                data-testid={`provider-disable-window-day-${index}-${day}`}
                              >
                                {day}
                              </button>
                            )
                          })}
                        </div>
                        <button
                          type="button"
                          className="shell-provider-model-remove"
                          onClick={() => handleRemoveDisableWindow(index)}
                          disabled={saving}
                          title={t('settings.provider.disableWindows.remove')}
                          aria-label={t('settings.provider.disableWindows.remove')}
                          data-testid={`provider-disable-window-remove-${index}`}
                        >
                          <Trash2 size={12} />
                        </button>
                      </div>
                    ))}
                  </div>
                )}
              </div>

              <div className="shell-provider-field">
                <div className="shell-provider-field-header">
                  <span className="shell-provider-label">{t('settings.provider.dialog.models')}</span>
                  <button
                    type="button"
                    className="shell-provider-fetch-btn"
                    onClick={() => void handleFetchModels()}
                    disabled={fetchingModels || saving || !form.endpoint}
                  >
                    {fetchingModels ? <Loader2 size={14} className="spin" /> : <Download size={14} />}
                    {fetchingModels ? t('settings.provider.dialog.fetching') : t('settings.provider.dialog.fetchModels')}
                  </button>
                </div>
                {fetchError && (
                  <div className="shell-provider-fetch-error">{fetchError}</div>
                )}
                {fetchedModels !== null ? (
                  <div className="shell-provider-model-list">
                    {fetchedModels.length === 0 && (
                      <div className="shell-provider-models-empty">
                        {t('settings.provider.noModels')}
                      </div>
                    )}
                    {fetchedModels.map((model, index) => (
                      <div key={index} className="shell-provider-model-row">
                        <label className="shell-provider-model-input-label">
                          <span>{t('settings.provider.dialog.modelName')}</span>
                          <input
                            type="text"
                            className="shell-provider-model-input"
                            value={model.Name}
                            onChange={(e) => handleUpdateModelName(index, e.target.value)}
                            placeholder={t('settings.provider.dialog.modelNamePlaceholder')}
                            disabled={saving}
                          />
                        </label>
                        <label className="shell-provider-model-input-label">
                          <span>{t('settings.provider.dialog.modelProtocol')}</span>
                          <select
                            className="shell-provider-model-select"
                            value={model.Protocol ?? 'openai'}
                            onChange={(e) => handleUpdateModelProtocol(index, e.target.value)}
                            disabled={saving}
                          >
                            <option value="openai">OpenAI</option>
                            <option value="anthropic">Anthropic</option>
                            <option value="gemini">Gemini</option>
                            <option value="responses">Responses</option>
                          </select>
                        </label>
                        <label className="shell-provider-model-input-label">
                          <span>{t('settings.provider.dialog.modelContext')}</span>
                          <input
                            type="number"
                            min={0}
                            step={1024}
                            className="shell-provider-model-input"
                            value={model.MaxContextLength ?? ''}
                            onChange={(e) => handleUpdateModelContext(index, e.target.value)}
                            disabled={saving}
                          />
                        </label>
                        <label className="shell-provider-model-input-label">
                          <span>{t('settings.provider.dialog.modelMaxTokens')}</span>
                          <input
                            type="number"
                            min={0}
                            step={1024}
                            className="shell-provider-model-input"
                            value={model.MaxTokens ?? ''}
                            onChange={(e) => handleUpdateModelMaxTokens(index, e.target.value)}
                            disabled={saving}
                          />
                        </label>
                        <button
                          type="button"
                          className="shell-provider-model-remove"
                          onClick={() => handleRemoveModel(index)}
                          disabled={saving}
                          title={t('settings.provider.dialog.removeModel')}
                        >
                          <Trash2 size={14} />
                        </button>
                      </div>
                    ))}
                    <div className="shell-provider-model-list-footer">
                      <button
                        type="button"
                        className="shell-provider-add-model-btn"
                        onClick={() => handleAddModel()}
                        disabled={saving}
                      >
                        <Plus size={14} />
                        {t('settings.provider.dialog.addModel')}
                      </button>
                    </div>
                  </div>
                ) : (
                  <div className="shell-provider-models-fallback">
                    {t('settings.provider.dialog.modelsFallback', { models: resolveModels().map(m => m.Name).join('、') || t('settings.provider.dialog.modelsFallbackEmpty') })}
                  </div>
                )}
              </div>
            </div>
            <div className="shell-provider-dialog-footer">
              <button
                type="button"
                className="shell-provider-btn-secondary"
                onClick={() => setDialogOpen(false)}
                disabled={saving}
              >
                {t('settings.provider.dialog.cancel')}
              </button>
              <button
                type="button"
                className="shell-provider-btn-primary"
                onClick={() => void handleSave()}
                disabled={saving}
              >
                {saving ? <Loader2 size={14} className="spin" /> : <Check size={14} />}
                {saved ? t('settings.provider.saved') : t('settings.provider.dialog.save')}
              </button>
            </div>
          </div>
        </div>
      )}

      <ShellAggregatorDialog
        open={aggregatorDialogOpen}
        aggregatorId={editingAggregatorId}
        providers={providers}
        onClose={() => setAggregatorDialogOpen(false)}
        onSaved={handleAggregatorSaved}
      />

      <ConfirmDialog
        open={confirmOpen}
        title={confirmConfig?.title ?? ''}
        description={confirmConfig?.description ?? ''}
        confirmLabel={confirmConfig?.confirmLabel ?? t('settings.provider.confirm.delete')}
        cancelLabel={t('settings.provider.confirm.cancel')}
        danger={confirmConfig?.danger ?? true}
        loading={saving}
        onConfirm={() => confirmConfig?.onConfirm()}
        onCancel={() => setConfirmOpen(false)}
      />
    </FeatureCard>
  )
}
