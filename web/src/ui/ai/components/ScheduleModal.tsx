import React, { useEffect, useMemo, useRef, useState } from 'react'
import { useI18n } from '../../../i18n'
import { ScheduleCronEditor } from './ScheduleCronEditor'
import { Modal } from '../../components/Modal'
import type { MonoCardListItem } from '../../../domain/mono-types'
import { cronToDraft, type ScheduleDraft } from './scheduledTasks'
import { client } from '../../../application/generated-client'
import * as workspace from '../../../gen-clients/workspace/client'
import * as aiaggregator from '../../../gen-clients/aiaggregator/client'
import type { AggregatorDescriptor } from '../../../gen-types/aigen'
import type { AgentKindInfo, AICallableUnitView, ModelSlot } from '../../../gen-clients/system/types'
import { SYSTEM_AGGREGATOR_ID, slotToSelection, type SlotSelection } from '../hooks/modelSlot'
import {
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  SelectItemText,
  Field,
  FieldLabel,
} from '../../settings/shadcn/ui'

/** Agent choice for a scheduler task: create a fresh agent of a kind (with an
 *  optional primary model slot) at each fire, or leave the card's existing
 *  executor fields untouched. */
export type SchedulerAgentSelection =
  | { mode: 'unset' }
  | { mode: 'create'; kind: string; selection?: SlotSelection }

export interface ScheduleModalProps {
  card: MonoCardListItem | null
  draft: ScheduleDraft | null
  onDraftChange: (d: ScheduleDraft) => void
  onApply: (agent: SchedulerAgentSelection) => void
  onClose: () => void
  saving?: boolean
  error?: string
  /** Aggregator descriptors: the system one backs the auto model pool of the
   *  model selector, custom ones appear as aggregator options. */
  aggregators?: AggregatorDescriptor[]
}

const AGG_PREFIX = 'agg::'

/** The card data stores model-slot overrides as a JSON string under
 *  `model_slots` (an object whose `primary` key holds the primary ModelSlot).
 *  parseStoredSelection pulls the primary slot out and converts it to the
 *  unified SlotSelection, parsing defensively so legacy/foreign values stay
 *  undefined. */
export function parseStoredSelection(value: unknown): SlotSelection | undefined {
  if (typeof value !== 'string' || !value) return undefined
  try {
    const parsed = JSON.parse(value) as { primary?: ModelSlot }
    const slot = parsed?.primary
    return slot && Array.isArray(slot.Candidates) ? slotToSelection(slot) : undefined
  } catch {
    return undefined
  }
}

/** Seed the agent selection from the card's data block: agent_kind wins
 *  (create-new), else unset. Bound-agent references are no longer used by the
 *  scheduler modal. */
function seedSelection(card: MonoCardListItem | null): SchedulerAgentSelection {
  const data = (card?.data ?? {}) as Record<string, unknown>
  const kind = typeof data.agent_kind === 'string' ? data.agent_kind : ''
  if (kind) return { mode: 'create', kind, selection: parseStoredSelection(data.model_slots) }
  return { mode: 'unset' }
}

export const ScheduleModal: React.FC<ScheduleModalProps> = ({ card, draft, onDraftChange, onApply, onClose, saving, error, aggregators = [] }) => {
  const { t } = useI18n()
  const [selection, setSelection] = useState<SchedulerAgentSelection>({ mode: 'unset' })
  const [agentKinds, setAgentKinds] = useState<AgentKindInfo[]>([])
  const [availableModels, setAvailableModels] = useState<AICallableUnitView[]>([])

  // Agent kinds power the "create new agent" selector — the FULL list from the
  // workspace registry (including non-functional kinds like dreamer), so the
  // options are never a hardcoded subset.
  useEffect(() => {
    if (!card) return
    let cancelled = false
    workspace.listAgentKinds(client)
      .then(resp => { if (!cancelled) setAgentKinds(resp.Items ?? []) })
      .catch(() => { if (!cancelled) setAgentKinds([]) })
    return () => { cancelled = true }
  }, [card])

  // Seed (and re-seed on card switch) the agent selection from the card data.
  useEffect(() => {
    setSelection(seedSelection(card))
  }, [card])

  const systemAggActorId = useMemo(
    () => aggregators.find(a => a.Id === SYSTEM_AGGREGATOR_ID)?.ActorId ?? '',
    [aggregators],
  )
  const slotAggregators = useMemo(
    () => aggregators.filter(a => a.Id !== SYSTEM_AGGREGATOR_ID),
    [aggregators],
  )

  // The system aggregator's pool is the fixed unit list of the model selector;
  // fetched only while a create-new-agent kind is selected.
  const wantsModels = selection.mode === 'create'
  useEffect(() => {
    if (!wantsModels || !systemAggActorId) {
      setAvailableModels([])
      return
    }
    let cancelled = false
    aiaggregator.status(client, { target: systemAggActorId })
      .then(resp => { if (!cancelled) setAvailableModels(resp.Units ?? []) })
      .catch(() => { if (!cancelled) setAvailableModels([]) })
    return () => { cancelled = true }
  }, [wantsModels, systemAggActorId])

  // The cron editor's apply button fires synchronously; keep the live
  // selection reachable from the callback via a ref.
  const selectionRef = useRef(selection)
  selectionRef.current = selection

  // Slot <SelectItem> value encodes its kind without exposing it to the user:
  //   "agg::<configId>"      → custom aggregator
  //   "<provider>::<model>"  → a model unit from the system aggregator pool
  //   ""                     → [auto] (system aggregator, no pinned unit)
  const encodeUnit = (u: AICallableUnitView) => `${u.ProviderName ?? ''}::${u.Model}`
  const unitLabel = (u: AICallableUnitView): string => `${u.Model} (${u.ProviderName})`
  const aggregatorLabel = (a: AggregatorDescriptor): string => a.Name || a.ActorId
  const selectionToValue = (sel?: SlotSelection): string => {
    if (!sel) return ''
    if (sel.type === 'aggregator') return AGG_PREFIX + sel.aggregatorId
    // A unit served by a custom aggregator is represented by that aggregator
    // (its unit isn't in the system pool the dropdown lists), so the select
    // keeps showing the aggregator it belongs to when the modal reopens.
    if (sel.aggregatorId && sel.aggregatorId !== SYSTEM_AGGREGATOR_ID) {
      return AGG_PREFIX + sel.aggregatorId
    }
    return `${sel.unit.provider}::${sel.unit.model}`
  }
  const valueToSelection = (value: string): SlotSelection | undefined => {
    if (!value) return undefined
    if (value.startsWith(AGG_PREFIX)) {
      const configId = value.slice(AGG_PREFIX.length)
      const agg = slotAggregators.find(a => a.Id === configId)
      return agg ? { type: 'aggregator', aggregatorId: agg.Id } : undefined
    }
    const unit = availableModels.find(u => encodeUnit(u) === value)
    return unit ? { type: 'unit', unit: { model: unit.Model, provider: unit.ProviderName ?? '' } } : undefined
  }

  const handleKindChange = (value: string | null) => {
    if (value === null || value === '') return
    const kind = value
    setSelection(prev => ({
      mode: 'create',
      kind,
      selection: prev.mode === 'create' ? prev.selection : undefined,
    }))
  }

  const handleModelChange = (value: string | null) => {
    setSelection(prev => {
      if (prev.mode !== 'create') return prev
      return { mode: 'create', kind: prev.kind, selection: valueToSelection(value ?? '') }
    })
  }

  const kindValue = selection.mode === 'create' ? selection.kind : ''
  const kindDisplayName = selection.mode === 'create'
    ? agentKinds.find(k => k.Kind === selection.kind)?.DisplayName || selection.kind
    : ''
  const modelValue = selection.mode === 'create' ? selectionToValue(selection.selection) : ''
  const modelDisplayName = useMemo(() => {
    if (selection.mode !== 'create') return ''
    const sel = selection.selection
    if (!sel) return t('scheduled.agent.modelAuto')
    if (sel.type === 'aggregator') {
      const agg = slotAggregators.find(a => a.Id === sel.aggregatorId)
      return agg ? `${t('scheduled.agent.aggregatorTag')} · ${aggregatorLabel(agg)}` : ''
    }
    const unit = availableModels.find(u => encodeUnit(u) === `${sel.unit.provider}::${sel.unit.model}`)
    return unit ? unitLabel(unit) : `${sel.unit.model} (${sel.unit.provider})`
  }, [selection, slotAggregators, availableModels, t])

  // Stable fallback for the brief window where `draft` is null (closing
  // transition): creating a fresh object every render would remount the cron
  // editor and flicker it.
  const fallbackDraft = useMemo(() => cronToDraft('0 9 * * *', '0 9 * * *'), [])

  return (
    <Modal
      open={!!card && !!draft}
      title={card ? card.id.replace(/^sched:/, '') : ''}
      onClose={onClose}
      closeOnOverlayClick
      size="md"
    >
      <div className="space-y-4">
        <Field>
          <FieldLabel>{t('scheduled.agent.title')}</FieldLabel>
          <SelectRoot value={kindValue} onValueChange={handleKindChange}>
            <SelectTrigger className="w-full" data-guide-id="schedule-kind-trigger">
              <SelectValue placeholder={t('scheduled.agent.placeholder')}>
                {kindDisplayName}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              {agentKinds.length === 0 && (
                <SelectItem value="__empty__" disabled>
                  <SelectItemText>{t('scheduled.agent.noKinds')}</SelectItemText>
                </SelectItem>
              )}
              {agentKinds.map(k => (
                <SelectItem key={k.Kind} value={k.Kind} data-guide-id={`schedule-kind-${k.Kind}`}>
                  <SelectItemText>{k.DisplayName || k.Kind}</SelectItemText>
                </SelectItem>
              ))}
            </SelectContent>
          </SelectRoot>
        </Field>

        {selection.mode === 'create' && (
          <Field>
            <FieldLabel>{t('scheduled.agent.model')}</FieldLabel>
            <SelectRoot value={modelValue} onValueChange={handleModelChange}>
              <SelectTrigger className="w-full" data-guide-id="schedule-model-trigger">
                <SelectValue placeholder={t('scheduled.agent.modelAuto')}>
                  {modelDisplayName}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="" data-guide-id="schedule-model-auto">
                  <SelectItemText>{t('scheduled.agent.modelAuto')}</SelectItemText>
                </SelectItem>
                {slotAggregators.map(a => (
                  <SelectItem key={`agg-${a.ActorId}`} value={AGG_PREFIX + a.Id} data-guide-id={`schedule-model-agg::${a.Id}`}>
                    <SelectItemText>{t('scheduled.agent.aggregatorTag')} · {aggregatorLabel(a)}</SelectItemText>
                  </SelectItem>
                ))}
                {availableModels.map(u => (
                  <SelectItem key={`m-${u.Model}-${u.ProviderName}`} value={encodeUnit(u)} data-guide-id={`schedule-model-${encodeUnit(u)}`}>
                    <SelectItemText>{unitLabel(u)}</SelectItemText>
                  </SelectItem>
                ))}
              </SelectContent>
            </SelectRoot>
          </Field>
        )}

        <ScheduleCronEditor
          draft={draft ?? fallbackDraft}
          onDraftChange={onDraftChange}
          onApply={() => onApply(selectionRef.current)}
          onCancel={onClose}
          saving={saving}
          error={error}
        />
      </div>
    </Modal>
  )
}
