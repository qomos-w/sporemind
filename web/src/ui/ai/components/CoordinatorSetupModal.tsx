import { useEffect, useMemo, useState } from 'react'
import { AtSign, Megaphone, Loader2 } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as workspace from '../../../gen-clients/workspace/client'
import * as aimanager_aggregator from '../../../gen-clients/aimanager/client'
import * as aiaggregator from '../../../gen-clients/aiaggregator/client'
import type { AggregatorDescriptor } from '../../../gen-types/aigen'
import type { AICallableUnitView, AgentRef } from '../../../gen-clients/system/types'
import { Modal } from '../../components/Modal'
import { useI18n } from '../../../i18n'
import { selectionToSlot, type SlotSelection } from '../hooks/modelSlot'
import { SYSTEM_AGGREGATOR_ID } from '../hooks/modelSlot'
import './CoordinatorSetupModal.css'

const AGG_PREFIX = 'agg::'

interface CoordinatorSetupModalProps {
  onDismiss: () => void
  onCreated: (agent: AgentRef) => void
}

export function CoordinatorSetupModal({ onDismiss, onCreated }: CoordinatorSetupModalProps) {
  const { t } = useI18n()
  const [nickname, setNickname] = useState('')
  const [unitValue, setUnitValue] = useState('')
  const [aggregators, setAggregators] = useState<AggregatorDescriptor[]>([])
  const [units, setUnits] = useState<AICallableUnitView[]>([])
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  const systemAggActorId = useMemo(
    () => aggregators.find(a => a.Id === SYSTEM_AGGREGATOR_ID)?.ActorId ?? '',
    [aggregators],
  )
  const slotAggregators = useMemo(
    () => aggregators.filter(a => a.Id !== SYSTEM_AGGREGATOR_ID),
    [aggregators],
  )

  useEffect(() => {
    let cancelled = false
    aimanager_aggregator.aggregatorList(client)
      .then(resp => { if (!cancelled) setAggregators(resp.Items ?? []) })
      .catch(() => { if (!cancelled) setAggregators([]) })
    return () => { cancelled = true }
  }, [])

  useEffect(() => {
    if (!systemAggActorId) { setUnits([]); return }
    let cancelled = false
    aiaggregator.status(client, { target: systemAggActorId })
      .then(resp => {
        if (cancelled) return
        const next = resp.Units ?? []
        setUnits(next)
        setUnitValue(prev => prev || (next[0] ? `${next[0].ProviderName ?? ''}::${next[0].Model}` : ''))
      })
      .catch(() => { if (!cancelled) setUnits([]) })
    return () => { cancelled = true }
  }, [systemAggActorId])

  const encodeUnit = (u: AICallableUnitView) => `${u.ProviderName ?? ''}::${u.Model}`
  const unitLabel = (u: AICallableUnitView) => `${u.Model} (${u.ProviderName})`

  const selectionFromValue = (value: string): SlotSelection | undefined => {
    if (!value) return undefined
    if (value.startsWith(AGG_PREFIX)) {
      const configId = value.slice(AGG_PREFIX.length)
      return slotAggregators.find(a => a.Id === configId)
        ? { type: 'aggregator', aggregatorId: configId }
        : undefined
    }
    const u = units.find(x => encodeUnit(x) === value)
    return u ? { type: 'unit', unit: { model: u.Model, provider: u.ProviderName ?? '' } } : undefined
  }

  const nicknameTrim = nickname.trim()
  const canCreate = nicknameTrim.length > 0 && !!unitValue && !creating

  const handleSubmit = async () => {
    if (!canCreate) return
    setCreating(true)
    setError('')
    try {
      const sel = selectionFromValue(unitValue)
      const agent = await workspace.createAgent(client, {
        ProjectId: '',
        DisplayName: nicknameTrim,
        AgentKind: 'coordinator',
        Primary: selectionToSlot(sel),
      })
      onCreated(agent)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setCreating(false)
    }
  }

  return (
    <Modal
      open
      size="md"
      dragWindow
      belowTitlebar
      onClose={onDismiss}
      title={
        <div className="coordinator-title">
          <Megaphone size={16} />
          <div>
            <div>{t('onboarding.coordinator.title')}</div>
            <div className="coordinator-subtitle">{t('onboarding.coordinator.subtitle')}</div>
          </div>
        </div>
      }
      footer={
        <div className="coordinator-footer">
          {error && <span className="coordinator-error">{error}</span>}
          <button
            type="button"
            className="coordinator-create"
            disabled={!canCreate}
            onClick={() => { void handleSubmit() }}
          >
            {creating && <Loader2 size={14} className="coordinator-spin" />}
            {t('onboarding.coordinator.create')}
          </button>
        </div>
      }
    >
      <div className="coordinator-body">
        <div className="coordinator-field">
          <label className="coordinator-label">{t('onboarding.coordinator.nickname.label')}</label>
          <div className="coordinator-nickname-wrap">
            <span className="coordinator-nickname-prefix"><AtSign size={15} /></span>
            <input
              className="coordinator-nickname-input"
              value={nickname}
              placeholder={t('onboarding.coordinator.nickname.placeholder')}
              onChange={e => setNickname(e.target.value)}
              spellCheck={false}
              autoFocus
            />
          </div>
          <div className="coordinator-hint">
            <Megaphone size={12} />
            <span>{t('onboarding.coordinator.nickname.hint')}</span>
          </div>
        </div>

        <div className="coordinator-field">
          <label className="coordinator-label">{t('onboarding.coordinator.unit.label')}</label>
          <select
            className="coordinator-select"
            value={unitValue}
            onChange={e => setUnitValue(e.target.value)}
          >
            {unitValue === '' && <option value="">{t('onboarding.coordinator.unit.auto')}</option>}
            {slotAggregators.map(a => (
              <option key={`agg-${a.ActorId}`} value={AGG_PREFIX + a.Id}>
                {t('onboarding.coordinator.unit.aggregatorTag')} · {a.Name || a.ActorId}
              </option>
            ))}
            {units.map(u => (
              <option key={`m-${u.Model}-${u.ProviderName}`} value={encodeUnit(u)}>{unitLabel(u)}</option>
            ))}
          </select>
          {units.length === 0 && slotAggregators.length === 0 && (
            <div className="coordinator-hint coordinator-hint--warn">
              {t('onboarding.coordinator.unit.empty')}
            </div>
          )}
        </div>
      </div>
    </Modal>
  )
}
