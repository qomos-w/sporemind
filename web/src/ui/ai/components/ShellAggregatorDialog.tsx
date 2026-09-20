import { useState, useEffect, useCallback, useMemo } from 'react'
import { Loader2, AlertCircle, X, ChevronUp, ChevronDown, Clock, Layers, RotateCcw } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as aimanagerProvider from '../../../gen-clients/aimanager/client'
import * as aimanagerAggregator from '../../../gen-clients/aimanager/client'
import type { ManualCallableUnit } from '../../../gen-clients/system/types'
import type { Provider } from '../../../gen-clients/system/types'
import type { AggregatorDescriptor } from '../../../gen-clients/system/types'
import { Modal } from '../../components/Modal'
import { useI18n } from '../../../i18n'
import {
  isCoolingDown,
  isDisabled,
  formatCountdown,
  healthReasonLabelKey,
  recoveryModeLabelKey,
  type HealthProjection,
} from '../hooks/healthStatus'
import './ShellAggregatorDialog.css'

interface ShellAggregatorDialogProps {
  open: boolean
  aggregatorId: string | null
  providers: Provider[]
  onClose: () => void
  onSaved: () => void
}

function generateId(): string {
  return `custom-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

/**
 * Collects every aggregator id reachable from `fromId` following reference
 * edges (an edge exists when an aggregator's Units contains an entry with a
 * non-empty aggregatorID). Cycle-safe: each id is visited at most once.
 */
export function collectDescendantIds(
  edges: Readonly<Record<string, string[]>>,
  fromId: string,
): Set<string> {
  const seen = new Set<string>()
  const stack = [fromId]
  while (stack.length > 0) {
    const current = stack.pop()!
    for (const childId of edges[current] ?? []) {
      if (!seen.has(childId)) {
        seen.add(childId)
        stack.push(childId)
      }
    }
  }
  return seen
}

export function ShellAggregatorDialog({
  open,
  aggregatorId,
  providers,
  onClose,
  onSaved,
}: ShellAggregatorDialogProps) {
  const { t } = useI18n()
  const isCreate = aggregatorId === null
  const [name, setName] = useState('')
  const [units, setUnits] = useState<ManualCallableUnit[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const [strategy, setStrategy] = useState<string>('round_robin')

  const [selectedProvider, setSelectedProvider] = useState('')
  const [selectedModel, setSelectedModel] = useState('')
  const [selectedAggregatorRef, setSelectedAggregatorRef] = useState('')
  const [now, setNow] = useState(() => Date.now())

  // Reference-selector data: every known aggregator (id → name) plus the
  // child-reference graph built from each aggregator's stored units.
  const [aggregatorItems, setAggregatorItems] = useState<AggregatorDescriptor[]>([])
  const [aggregatorEdges, setAggregatorEdges] = useState<Record<string, string[]>>({})

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  useEffect(() => {
    if (!open) return
    setError('')
    if (isCreate) {
      setName('')
      setUnits([])
      setStrategy('round_robin')
      setLoading(false)
      setSelectedProvider('')
      setSelectedModel('')
      return
    }
    setLoading(true)
    let cancelled = false
    aimanagerAggregator.aggregatorGet(client, { Id: aggregatorId! })
      .then(resp => {
        if (cancelled) return
        setName(resp.Name)
        setUnits(resp.Units)
        setStrategy(resp.Strategy || 'round_robin')
      })
      .catch(err => {
        if (cancelled) return
        setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [open, aggregatorId, isCreate])

  // Load the aggregator catalogue (id → name for reference rows) and the
  // reference graph (each aggregator's Units entries with a non-empty
  // aggregatorID are child-reference edges). This is auxiliary data: failures
  // degrade to "no reference options" rather than blocking the dialog.
  useEffect(() => {
    if (!open) return
    let cancelled = false
    setAggregatorItems([])
    setAggregatorEdges({})
    aimanagerAggregator.aggregatorList(client)
      .then(async (resp) => {
        if (cancelled) return
        setAggregatorItems(resp.Items)
        const entries = await Promise.all(resp.Items.map(async (item) => {
          try {
            const cfg = await aimanagerAggregator.aggregatorGet(client, { Id: item.Id })
            const children = cfg.Units
              .map(u => u.aggregatorID)
              .filter((id): id is string => Boolean(id))
            return [item.Id, children] as const
          } catch {
            return [item.Id, [] as string[]] as const
          }
        }))
        if (cancelled) return
        setAggregatorEdges(Object.fromEntries(entries))
      })
      .catch(() => { /* non-fatal */ })
    return () => { cancelled = true }
  }, [open])

  const availableProviders = useMemo(() => {
    return providers.filter(p => p.Models.length > 0)
  }, [providers])

  const currentProvider = useMemo(() => {
    return availableProviders.find(p => p.Name === selectedProvider)
  }, [availableProviders, selectedProvider])

  const availableModels = useMemo(() => {
    if (!currentProvider) return []
    return currentProvider.Models
      .map(m => m.Name)
      .filter(name =>
        !units.some(u => u.ProviderName === currentProvider.Name && u.Model === name)
      )
  }, [currentProvider, units])

  useEffect(() => {
    setSelectedModel('')
  }, [selectedProvider])

  const handleAddUnit = useCallback(() => {
    if (!currentProvider || !selectedModel) return
    const exists = units.some(
      u => u.ProviderName === currentProvider.Name && u.Model === selectedModel
    )
    if (exists) return
    setUnits(prev => [
      ...prev,
      {
        Model: selectedModel,
        Endpoint: currentProvider.Endpoint,
        ProviderName: currentProvider.Name,
        Protocol: currentProvider.Kind,
      },
    ])
    setSelectedModel('')
  }, [currentProvider, selectedModel, units])

  const referencedAggIds = useMemo(() => {
    const ids = new Set<string>()
    for (const u of units) {
      if (u.aggregatorID) ids.add(u.aggregatorID)
    }
    return ids
  }, [units])

  const descendantIds = useMemo(() => {
    if (!aggregatorId) return new Set<string>()
    return collectDescendantIds(aggregatorEdges, aggregatorId)
  }, [aggregatorId, aggregatorEdges])

  const aggregatorNameById = useMemo(() => {
    return new Map(aggregatorItems.map(a => [a.Id, a.Name]))
  }, [aggregatorItems])

  // Candidates for the reference selector: exclude the aggregator being
  // edited (self), ids already referenced in the pool, and any descendant of
  // the edited aggregator (would construct a cycle A→…→A).
  const refOptions = useMemo(() => {
    return aggregatorItems.filter(a =>
      a.Id !== aggregatorId &&
      !referencedAggIds.has(a.Id) &&
      !descendantIds.has(a.Id)
    )
  }, [aggregatorItems, aggregatorId, referencedAggIds, descendantIds])

  const handleAddAggregatorRef = useCallback(() => {
    if (!selectedAggregatorRef) return
    // Dedup by aggregatorID; the option list already hides referenced ids as a
    // guard, this check covers a stale selection.
    const exists = units.some(u => u.aggregatorID === selectedAggregatorRef)
    if (exists) return
    // Store the reference exactly like the backend does: only aggregatorID is
    // populated, the concrete-unit fields stay empty.
    setUnits(prev => [
      ...prev,
      {
        Model: '',
        Endpoint: '',
        ProviderName: '',
        Protocol: '',
        aggregatorID: selectedAggregatorRef,
      },
    ])
    setSelectedAggregatorRef('')
  }, [selectedAggregatorRef, units])

  const handleMoveUnit = useCallback((index: number, dir: number, jumpToEnd = false) => {
    setUnits(prev => {
      if (jumpToEnd) {
        // Shift+click: move to top (dir < 0) or bottom (dir > 0) directly.
        const target = dir < 0 ? 0 : prev.length - 1
        if (index === target) return prev
        const next = [...prev]
        const [moved] = next.splice(index, 1)
        next.splice(target, 0, moved!)
        return next
      }
      const target = index + dir
      if (target < 0 || target >= prev.length) return prev
      const next = [...prev]
      const a = next[index]!
      const b = next[target]!
      next[index] = b
      next[target] = a
      return next
    })
  }, [])

  const handleRemoveUnit = useCallback((index: number) => {
    setUnits(prev => prev.filter((_, i) => i !== index))
  }, [])

  const handleUnitEffort = useCallback((index: number, effort: string) => {
    setUnits(prev => prev.map((u, i) =>
      i === index ? { ...u, ReasoningEffort: effort } : u
    ))
  }, [])

  // Health (cooldown / disabled) is recorded per provider in the shared
  // llmclient snapshot and the reset callable is provider-scoped, so clicking
  // reset on one unit row clears every unit of that provider — same semantics
  // as the provider settings reset button.
  const handleResetUnitHealth = useCallback(async (unit: ManualCallableUnit) => {
    setError('')
    try {
      const resp = await aimanagerProvider.providerResetHealth(client, { ProviderName: unit.ProviderName })
      if (resp.Error) {
        setError(resp.Error)
        return
      }
      setUnits(prev => prev.map(u => u.ProviderName === unit.ProviderName
        ? {
            ...u,
            HealthState: undefined,
            HealthReason: undefined,
            RecoveryMode: undefined,
            CooldownUntil: undefined,
          }
        : u))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }, [])

  const handleSave = useCallback(async () => {
    setSaving(true)
    setError('')
    try {
      await aimanagerAggregator.aggregatorConfigure(client, {
        Id: isCreate ? generateId() : aggregatorId!,
        Name: name.trim() || t('aggregator.customDefault'),
        Units: units,
        Strategy: strategy,
      })
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [aggregatorId, isCreate, name, onClose, onSaved, units, strategy])

  const providerOptions = availableProviders.map(p => (
    <option key={p.Name} value={p.Name}>
      {p.Name} ({p.Kind})
    </option>
  ))

  const modelOptions = availableModels.map(m => (
    <option key={m} value={m}>{m}</option>
  ))

  // Shared up/down/remove controls for both concrete unit rows and aggregator
  // reference rows, so the two row kinds behave identically.
  const renderRowActions = (index: number) => (
    <>
      <button
        type="button"
        className="sad-unit-move"
        onClick={(e) => handleMoveUnit(index, -1, e.shiftKey)}
        disabled={saving || (index === 0 && units.length < 2)}
        title={t('aggregator.moveUp')}
      >
        <ChevronUp size={14} />
      </button>
      <button
        type="button"
        className="sad-unit-move"
        onClick={(e) => handleMoveUnit(index, 1, e.shiftKey)}
        disabled={saving || (index === units.length - 1 && units.length < 2)}
        title={t('aggregator.moveDown')}
      >
        <ChevronDown size={14} />
      </button>
      <button
        type="button"
        className="sad-unit-remove"
        onClick={() => handleRemoveUnit(index)}
        disabled={saving}
        title={t('aggregator.remove')}
      >
        <X size={14} />
      </button>
    </>
  )

  return (
    <Modal
      open={open}
      title={isCreate ? t('aggregator.createTitle') : t('aggregator.editTitle')}
      onClose={onClose}
      size="lg"
      closeOnOverlayClick={false}
      disableClose={saving}
      footer={
        <>
          <button
            type="button"
            className="modal-action modal-action--secondary"
            onClick={onClose}
            disabled={saving}
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            className="modal-action modal-action--primary"
            onClick={() => void handleSave()}
            disabled={saving || units.length === 0}
          >
            {saving ? t('common.saving') : t('common.save')}
          </button>
        </>
      }
    >
      {loading ? (
        <div className="sad-loading">
          <Loader2 size={18} className="spin" />
          <span>{t('common.loading')}...</span>
        </div>
      ) : (
        <div className="sad-body">
          {error && (
            <div className="sad-error">
              <AlertCircle size={14} />
              <span>{error}</span>
            </div>
          )}

          <label className="sad-field">
            <span className="sad-label">{t('aggregator.name')}</span>
            <input
              type="text"
              className="sad-input"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('aggregator.namePlaceholder')}
              disabled={saving}
            />
          </label>

          <label className="sad-field">
            <span className="sad-label">{t('aggregator.strategy')}</span>
            <select
              className="sad-select"
              value={strategy}
              onChange={(e) => setStrategy(e.target.value)}
              disabled={saving}
            >
              <option value="fallback">{t('aggregator.strategy.fallback')}</option>
              <option value="round_robin">{t('aggregator.strategy.roundRobin')}</option>
              <option value="smart">{t('aggregator.strategy.smart')}</option>
              <option value="standard">{t('aggregator.strategy.standard')}</option>
            </select>
            <div className="sad-hint">{t('aggregator.strategyHint')}</div>
          </label>

          <div className="sad-field">
            <span className="sad-label">{t('aggregator.addModel')}</span>
            <div className="sad-add-row">
              <select
                className="sad-select"
                value={selectedProvider}
                onChange={(e) => setSelectedProvider(e.target.value)}
                disabled={saving || availableProviders.length === 0}
              >
                <option value="">{t('aggregator.selectProvider')}</option>
                {providerOptions}
              </select>
              <select
                className="sad-select"
                value={selectedModel}
                onChange={(e) => setSelectedModel(e.target.value)}
                disabled={saving || !selectedProvider}
              >
                <option value="">{t('aggregator.selectModel')}</option>
                {modelOptions}
              </select>
              <button
                type="button"
                className="sad-add-btn"
                onClick={handleAddUnit}
                disabled={saving || !selectedModel}
              >
                {t('aggregator.add')}
              </button>
            </div>
            {availableProviders.length === 0 && (
              <div className="sad-hint">{t('aggregator.addProviderHint')}</div>
            )}
          </div>

          <div className="sad-field">
            <span className="sad-label">{t('aggregator.addAggregatorRef')}</span>
            <div className="sad-add-row">
              <select
                className="sad-select"
                value={selectedAggregatorRef}
                onChange={(e) => setSelectedAggregatorRef(e.target.value)}
                disabled={saving || refOptions.length === 0}
              >
                <option value="">{t('aggregator.selectAggregatorRef')}</option>
                {refOptions.map(a => (
                  <option key={a.Id} value={a.Id}>{a.Name}</option>
                ))}
              </select>
              <button
                type="button"
                className="sad-add-btn"
                onClick={handleAddAggregatorRef}
                disabled={saving || !selectedAggregatorRef}
              >
                {t('aggregator.add')}
              </button>
            </div>
            {refOptions.length === 0 && (
              <div className="sad-hint">{t('aggregator.noRefOptionsHint')}</div>
            )}
          </div>

          <div className="sad-field">
            <span className="sad-label">{t('aggregator.modelUnits')}</span>
            {units.length === 0 ? (
              <div className="sad-empty">{t('aggregator.noModels')}</div>
            ) : (
              <div className="sad-units">
                {units.map((unit, index) => {
                  if (unit.aggregatorID) {
                    const refName = aggregatorNameById.get(unit.aggregatorID) ?? unit.aggregatorID
                    return (
                      <div
                        key={`ref-${unit.aggregatorID}-${index}`}
                        className="sad-unit sad-unit--ref"
                      >
                        <div className="sad-unit-main">
                          <span className="sad-unit-ref-name" title={unit.aggregatorID}>
                            <Layers size={14} className="sad-unit-ref-icon" />
                            {refName}
                          </span>
                          <span className="sad-unit-ref-sub">
                            <span className="sad-unit-badge sad-unit-badge--ref">
                              {t('aggregator.reference')}
                            </span>
                          </span>
                        </div>
                        <div className="sad-unit-meta">
                          {renderRowActions(index)}
                        </div>
                      </div>
                    )
                  }
                  const proj: HealthProjection = {
                    healthState: unit.HealthState,
                    healthReason: unit.HealthReason,
                    cooldownUntil: unit.CooldownUntil,
                    recoveryMode: unit.RecoveryMode,
                  }
                  const cooling = isCoolingDown(proj, now)
                  const disabled = isDisabled(proj)
                  const cd = cooling && unit.CooldownUntil ? formatCountdown(unit.CooldownUntil, now) : null
                  const scheduleReason = cooling && unit.HealthReason === 'disable_window'
                    ? t('health.reason.disable_window')
                    : null
                  const reason = disabled
                    ? t(`health.reason.${healthReasonLabelKey(unit.HealthReason)}` as any)
                    : null
                  const recovery = disabled && unit.RecoveryMode
                    ? t(`health.recovery.${recoveryModeLabelKey(unit.RecoveryMode)}` as any)
                    : null
                  return (
                    <div
                      key={`${unit.ProviderName}-${unit.Model}-${index}`}
                      className={[
                        'sad-unit',
                        cooling ? 'is-cooldown' : '',
                        disabled ? 'is-disabled' : '',
                      ].filter(Boolean).join(' ')}
                    >
                      <div className="sad-unit-main">
                        <span className="sad-unit-model" title={unit.Model}>{unit.Model}</span>
                        <span className="sad-unit-provider" title={unit.ProviderName}>{unit.ProviderName}</span>
                      </div>
                      <div className="sad-unit-meta">
                        <span className="sad-unit-protocol">{unit.Protocol}</span>
                        {cooling && cd && (
                          <span
                            className="sad-unit-badge sad-unit-badge--cooling"
                            title={scheduleReason
                              ? `${t('health.coolingDown', { countdown: cd })} · ${scheduleReason}`
                              : t('health.coolingDown', { countdown: cd })}
                          >
                            <Clock size={11} />
                            {cd}
                          </span>
                        )}
                        {disabled && reason && (
                          <span
                            className="sad-unit-badge sad-unit-badge--disabled"
                            title={recovery ? `${reason} · ${recovery}` : reason}
                          >
                            <AlertCircle size={11} />
                            {reason}
                          </span>
                        )}
                        {(disabled || cooling) && (
                          <button
                            type="button"
                            className="sad-unit-reset"
                            onClick={() => void handleResetUnitHealth(unit)}
                            disabled={saving}
                            title={t('health.reset')}
                            aria-label={t('health.reset')}
                            data-testid={`aggregator-unit-reset-health-btn-${unit.ProviderName}-${unit.Model}`}
                          >
                            <RotateCcw size={12} />
                          </button>
                        )}
                        <select
                          className="sad-unit-effort"
                          value={unit.ReasoningEffort ?? ''}
                          onChange={(e) => handleUnitEffort(index, e.target.value)}
                          disabled={saving}
                          title={t('aggregator.effortHint')}
                        >
                          <option value="">{t('aggregator.effortDefault')}</option>
                          <option value="none">{t('aggregator.effortNone')}</option>
                          <option value="low">{t('aggregator.effortLow')}</option>
                          <option value="medium">{t('aggregator.effortMedium')}</option>
                          <option value="high">{t('aggregator.effortHigh')}</option>
                          <option value="xhigh">{t('aggregator.effortXhigh')}</option>
                          <option value="max">{t('aggregator.effortMax')}</option>
                          <option value="ultra">{t('aggregator.effortUltra')}</option>
                        </select>
                        {renderRowActions(index)}
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        </div>
      )}
    </Modal>
  )
}
