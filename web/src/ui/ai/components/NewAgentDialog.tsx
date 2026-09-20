import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ChevronDown, ChevronRight, Save, Trash2 } from 'lucide-react'
import type { AgentRef, AggregatorDescriptor } from '../../../gen-types/aigen'
import type { ProjectRef, CompactionPolicy, RandomNameConfig } from '../../../gen-clients/system/types'
import { client } from '../../../application/generated-client'
import { projectDisplayName } from '../../../application/project-adapter'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n/types'
import * as aiaggregator from '../../../gen-clients/aiaggregator/client'
import type { AICallableUnitView } from '../../../gen-clients/system/types'
import type { AgentInfo } from '../hooks/agentInfoStore'
import { slotToSelection, type SlotSelection } from '../hooks/modelSlot'
import { useModelPresets } from '../hooks/useModelPresets'
import { Modal } from '../../components/Modal'
import './NewAgentDialog.css'

/** Reserved aggregator id for the system aggregator; its pool is the "auto" model list. */
const SYSTEM_AGGREGATOR_ID = 'system'

export interface AgentKindOption {
  kind: string
  displayName: string
  randomName: RandomNameConfig | undefined
  namePool?: string[]
}

type Scope = 'global' | 'project'

export interface AgentDialogInput {
  displayName: string
  title?: string
  agentKind: string
  projectId?: string
  primarySelection?: SlotSelection
  fastSelection?: SlotSelection
  executionSelection?: SlotSelection
  reviewSelection?: SlotSelection
  summarySelection?: SlotSelection
  compactionPolicy?: CompactionPolicy
}

interface CloneSource {
  displayName: string
  agentKind: string
  projectId?: string
}

interface NewAgentDialogProps {
  open: boolean
  mode?: 'create' | 'edit' | 'clone'
  editAgent?: AgentRef
  cloneSource?: CloneSource
  /** When set alongside clone mode, the dialog shows fork title/action and uses fork suffix. */
  forkTurnId?: string
  projects: ProjectRef[]
  aggregators: AggregatorDescriptor[]
  agentKinds: AgentKindOption[]
  agents: AgentInfo[]
  defaultAgentKind: string
  defaultProjectId?: string
  defaultDisplayName?: string
  creating: boolean
  error: string
  /** Compaction policy from the agent instance (edit mode). Undefined in create/clone mode. */
  compactionPolicy?: CompactionPolicy
  compactionSource?: string
  onCancel: () => void
  onSubmit?: (input: AgentDialogInput) => Promise<void> | void
  /** @deprecated Use onSubmit instead */
  onCreate?: (input: AgentDialogInput) => Promise<void> | void
}

function defaultCompactionPolicy(): CompactionPolicy {
  return {
    Enabled: false,
    Strategy: '',
    TriggerKind: '',
    BudgetMode: 'percentage',
    TokenBudget: 75,
    RecentWindow: 20,
    SummaryUnit: undefined,
    MaxSummaryTokens: 4000,
  }
}

function normalizeCompactionPolicy(cp: CompactionPolicy | undefined): CompactionPolicy {
  const d = defaultCompactionPolicy()
  if (!cp) return { ...d }
  return {
    ...cp,
    BudgetMode: cp.BudgetMode ?? d.BudgetMode,
    TokenBudget: cp.TokenBudget ?? d.TokenBudget,
    RecentWindow: cp.RecentWindow ?? d.RecentWindow,
    SummaryUnit: cp.SummaryUnit ?? d.SummaryUnit,
    MaxSummaryTokens: cp.MaxSummaryTokens ?? d.MaxSummaryTokens,
  }
}

function compactionPoliciesEqual(a: CompactionPolicy, b: CompactionPolicy): boolean {
  return a.Enabled === b.Enabled
    && a.BudgetMode === b.BudgetMode
    && a.TokenBudget === b.TokenBudget
    && a.RecentWindow === b.RecentWindow
    && a.SummaryUnit?.model === b.SummaryUnit?.model
    && a.SummaryUnit?.provider === b.SummaryUnit?.provider
    && a.MaxSummaryTokens === b.MaxSummaryTokens
}

/** Non-durable seed for a dialog session. Selection state lives in component state;
 *  it is not persisted to localStorage (constraint: 前端状态后端化). */

function aggregatorLabel(aggregator: AggregatorDescriptor): string {
  return aggregator.Name || aggregator.ActorId
}

function kindScope(kind: string): Scope | null {
  if (kind === 'coder') return 'project'
  return null
}

interface ValidationInput {
  mode: 'create' | 'edit' | 'clone'
  displayName: string
  projectId: string
  selectedKind: AgentKindOption | null
  hasAgentKinds: boolean
  hasAnyAgentKinds: boolean
  hasProjects: boolean
  unavailableKindReason?: string
  t: (key: I18nKey, params?: Record<string, string | number>) => string
}

function validate(input: ValidationInput): string | null {
  const { mode, displayName, projectId, selectedKind, hasAgentKinds, hasAnyAgentKinds, hasProjects, unavailableKindReason, t } = input
  if (mode !== 'edit') {
    if (!hasAnyAgentKinds) return t('dialog.newAgent.error.noKinds')
    if (!hasAgentKinds) return t('dialog.newAgent.error.allKindsExist')
    if (!selectedKind) return t('dialog.newAgent.error.selectKind')
    if (unavailableKindReason) return unavailableKindReason
    if (kindScope(selectedKind.kind) === 'project' && (!hasProjects || !projectId)) return t('dialog.newAgent.error.selectProject')
  }
  if (!displayName.trim()) return t('dialog.newAgent.error.displayNameRequired')
  return null
}

function generateRandomName(randomName?: RandomNameConfig, namePool?: string[]): string {
  if (randomName && randomName.Enabled) {
    const prefixes = randomName.Prefixes ?? []
    const suffixes = randomName.Suffixes ?? []
    if (prefixes.length > 0 && suffixes.length > 0) {
      const prefix = prefixes[Math.floor(Math.random() * prefixes.length)]!
      const suffix = suffixes[Math.floor(Math.random() * suffixes.length)]!
      return prefix + ' ' + suffix
    }
  }
  if (namePool && namePool.length > 0) {
    return namePool[Math.floor(Math.random() * namePool.length)]!
  }
  return ''
}

interface AgentSessionSeed {
  displayName: string
  title: string
  displayNameTouched: boolean
  agentKind: string
  projectId: string
  primarySelection?: SlotSelection
  fastSelection?: SlotSelection
  executionSelection?: SlotSelection
  reviewSelection?: SlotSelection
  summarySelection?: SlotSelection
  draftCompaction: CompactionPolicy
}

function pickAgentKind(props: NewAgentDialogProps): string {
  const { mode, editAgent, cloneSource, defaultAgentKind, agentKinds } = props
  if (mode === 'edit' && editAgent) return editAgent.AgentKind
  if (mode === 'clone' && cloneSource) return cloneSource.agentKind
  if (defaultAgentKind && agentKinds.some(o => o.kind === defaultAgentKind)) return defaultAgentKind
  return agentKinds[0]?.kind ?? ''
}

function pickProjectId(
  mode: 'create' | 'edit' | 'clone' | undefined,
  editAgent: AgentRef | undefined,
  cloneSource: CloneSource | undefined,
  defaultProjectId: string | undefined,
  forcedScope: Scope | null,
  projects: ProjectRef[],
): string {
  if (mode === 'edit' && editAgent) return editAgent.ProjectId ?? ''
  if (mode === 'clone' && cloneSource) return cloneSource.projectId ?? ''
  if (defaultProjectId) return defaultProjectId
  if (forcedScope === 'project') return projects.find(p => !p.System)?.ActorId ?? ''
  return ''
}

function computeSessionSeed(props: NewAgentDialogProps, t: (key: I18nKey, params?: Record<string, string | number>) => string): AgentSessionSeed {
  const { mode, editAgent, cloneSource, agentKinds, projects } = props
  const isEdit = mode === 'edit'
  const isClone = mode === 'clone'

  const agentKind = pickAgentKind(props)
  const forcedScope = kindScope(agentKind)
  const projectId = pickProjectId(mode, editAgent, cloneSource, props.defaultProjectId, forcedScope, projects)

  let displayName: string
  let displayNameTouched: boolean
  if (isEdit && editAgent) {
    displayName = editAgent.DisplayName
    displayNameTouched = true
  } else if (isClone && cloneSource) {
    const suffix = props.forkTurnId ? t('dialog.newAgent.forkSuffix') : t('dialog.newAgent.cloneSuffix')
    displayName = cloneSource.displayName + ' ' + suffix
    displayNameTouched = true
  } else if (props.defaultDisplayName) {
    displayName = props.defaultDisplayName
    displayNameTouched = true
  } else {
    const selectedKind = agentKinds.find(o => o.kind === agentKind)
    displayName = selectedKind ? generateRandomName(selectedKind.randomName, selectedKind.namePool) : ''
    displayNameTouched = false
  }

  const primarySelection = isEdit && editAgent ? slotToSelection(editAgent.Primary) : undefined
  const fastSelection = isEdit && editAgent ? slotToSelection(editAgent.Fast) : undefined
  const executionSelection = isEdit && editAgent ? slotToSelection(editAgent.Execution) : undefined
  const reviewSelection = isEdit && editAgent ? slotToSelection(editAgent.Review) : undefined
  // Summary is a full slot like the others. Agents configured before this
  // change stored their summary model as a CompactionPolicy.SummaryUnit
  // override; migrate it into a unit-kind selection so it stays editable.
  const summarySlotSel = isEdit && editAgent ? slotToSelection(editAgent.Summary) : undefined
  const overrideUnit = props.compactionPolicy?.SummaryUnit
  const summarySelection = summarySlotSel
    ?? (overrideUnit && overrideUnit.model ? { type: 'unit', unit: overrideUnit } : undefined)

  const draftCompaction = normalizeCompactionPolicy(props.compactionPolicy)
  // Summary is now owned by the Summary slot, so the draft never carries a
  // SummaryUnit override (it is migrated into summarySelection above).
  draftCompaction.SummaryUnit = undefined

  return {
    displayName,
    displayNameTouched,
    title: isEdit && editAgent ? (editAgent.Title ?? '') : '',
    agentKind,
    projectId,
    primarySelection,
    fastSelection,
    executionSelection,
    reviewSelection,
    summarySelection,
    draftCompaction,
  }
}

export function NewAgentDialog(props: NewAgentDialogProps) {
  if (!props.open) return null
  return <NewAgentDialogInner {...props} />
}

function NewAgentDialogInner(props: NewAgentDialogProps) {
  const { t } = useI18n()
  const {
    open,
    mode,
    projects,
    aggregators,
    agentKinds,
    defaultProjectId,
    creating,
    error,
    compactionPolicy,
    compactionSource,
    onCancel,
    onSubmit,
    onCreate,
  } = props
  const isEdit = mode === 'edit'
  const isClone = mode === 'clone'

  // Seed: re-computed when the edited/cloned agent identity changes so the dialog
  // reflects the current agent. In create mode the identity is stable and the seed
  // is computed once on mount.
  const seed = useMemo(
    () => computeSessionSeed(props, t),
    [props.mode, props.editAgent?.ActorId, props.cloneSource?.displayName, props.cloneSource?.projectId, props.forkTurnId, t],
  )

  const [displayName, setDisplayName] = useState(seed.displayName)
  const [agentKind, setAgentKind] = useState(seed.agentKind)
  const [projectId, setProjectId] = useState(seed.projectId)
  const [title, setTitle] = useState(seed.title)
  const displayNameTouchedRef = useRef(seed.displayNameTouched)

  // Compaction policy editing state. In edit mode seeded from the agent's current
  // override; in create/clone mode seeded from defaults so the user can opt-in.
  const [draftCompaction, setDraftCompaction] = useState<CompactionPolicy>(seed.draftCompaction)
  const [compactionOpen, setCompactionOpen] = useState(false)

  // Per-slot selection: a named aggregator OR a pinned model unit OR empty ([auto]).
  const [primarySelection, setPrimarySelection] = useState<SlotSelection | undefined>(seed.primarySelection)
  const [fastSelection, setFastSelection] = useState<SlotSelection | undefined>(seed.fastSelection)
  const [executionSelection, setExecutionSelection] = useState<SlotSelection | undefined>(seed.executionSelection)
  const [reviewSelection, setReviewSelection] = useState<SlotSelection | undefined>(seed.reviewSelection)
  const [summarySelection, setSummarySelection] = useState<SlotSelection | undefined>(seed.summarySelection)

  // Presets: saved 4-slot snapshots, stored in the workspace actor (not localStorage).
  // The preset field is a single editable combobox: its value is both the selected
  // preset name (when it matches an existing one) and the name a new preset will be
  // saved under. Save always acts on the typed name (create or overwrite); delete
  // only appears when the current text matches a stored preset.
  const { presets, recent, recentLoaded, savePreset, deletePreset, applyPreset, saveRecent, applyRecent } = useModelPresets(aggregators)
  const [presetName, setPresetName] = useState('')
  const [presetOpen, setPresetOpen] = useState(false)
  const presetComboRef = useRef<HTMLDivElement>(null)
  const matchedPreset = presets.find(p => p.name === presetName.trim()) ?? null
  const canSave = presetName.trim().length > 0 && !creating
  // Tracks whether the user has manually changed any model slot this session, so the
  // recent-selection prefill (loaded async) does not clobber an intentional choice.
  const slotsTouchedRef = useRef(false)
  const markSlotsTouched = useCallback(() => { slotsTouchedRef.current = true }, [])
  const setAllSelections = useCallback((slots: { primary?: SlotSelection; fast?: SlotSelection; execution?: SlotSelection; review?: SlotSelection; summary?: SlotSelection }) => {
    setPrimarySelection(slots.primary)
    setFastSelection(slots.fast)
    setExecutionSelection(slots.execution)
    setReviewSelection(slots.review)
    setSummarySelection(slots.summary)
  }, [])
  const handlePickPreset = (name: string) => {
    setPresetName(name)
    setPresetOpen(false)
    const preset = presets.find(p => p.name === name)
    if (preset) {
      const snap = applyPreset(preset)
      setAllSelections(snap)
      markSlotsTouched()
    }
  }
  // Close the preset dropdown on outside click.
  useEffect(() => {
    if (!presetOpen) return
    const onDown = (e: MouseEvent) => {
      if (presetComboRef.current && !presetComboRef.current.contains(e.target as Node)) setPresetOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [presetOpen])
  const handleSavePreset = async () => {
    const name = presetName.trim()
    if (!name) return
    await savePreset(name, { primary: primarySelection, fast: fastSelection, execution: executionSelection, review: reviewSelection, summary: summarySelection })
  }
  const handleDeletePreset = async () => {
    if (!matchedPreset) return
    if (!window.confirm(t('dialog.newAgent.presetConfirmDelete', { name: matchedPreset.name }))) return
    await deletePreset(matchedPreset.name)
    setPresetName('')
  }

  // Reset all form fields when the seed changes (agent identity switched while
  // the dialog stayed open, e.g. editing a different agent without closing).
  useEffect(() => {
    setDisplayName(seed.displayName)
    setAgentKind(seed.agentKind)
    setProjectId(seed.projectId)
    setTitle(seed.title)
    displayNameTouchedRef.current = seed.displayNameTouched
    slotsTouchedRef.current = false
    setDraftCompaction(seed.draftCompaction)
    setPrimarySelection(seed.primarySelection)
    setFastSelection(seed.fastSelection)
    setExecutionSelection(seed.executionSelection)
    setReviewSelection(seed.reviewSelection)
    setSummarySelection(seed.summarySelection)
  }, [seed])

  // Recent selection memory: in create/clone mode, prefill the 5 slots from the last
  // submitted selection (stored in workspace preferences). Skipped in edit mode (which
  // seeds from the agent's own config) and after the user touches any slot.
  useEffect(() => {
    if (isEdit || !recentLoaded || !recent) return
    if (slotsTouchedRef.current) return
    const snap = applyRecent(recent)
    setAllSelections(snap)
  }, [recentLoaded, recent, isEdit, applyRecent, setAllSelections])

  const [availableModels, setAvailableModels] = useState<AICallableUnitView[]>([])

  const resolvedMode = mode ?? 'create'

  const unavailableKindReasons = useMemo(() => {
    const reasons = new Map<string, string>()
    if (resolvedMode !== 'create') return reasons
    // Coordinator is a global singleton, not user-creatable here.
    // It is created via the onboarding flow.
    reasons.set('coordinator', t('dialog.newAgent.error.coordinatorAuto'))
    return reasons
  }, [resolvedMode, t])

  const availableAgentKindOptions = useMemo(() => {
    return agentKinds.filter(option => !unavailableKindReasons.has(option.kind))
  }, [agentKinds, unavailableKindReasons])

  // The system aggregator's pool is the "auto" model list shown in every slot
  // dropdown. Custom aggregators (excluding system) appear as slot options too.
  const systemAggActorId = useMemo(
    () => aggregators.find(a => a.Id === SYSTEM_AGGREGATOR_ID)?.ActorId ?? '',
    [aggregators],
  )
  const slotAggregators = useMemo(
    () => aggregators.filter(a => a.Id !== SYSTEM_AGGREGATOR_ID),
    [aggregators],
  )

  const forcedScope = kindScope(agentKind)
  const effectiveScope: Scope = forcedScope ?? (projectId ? 'project' : 'global')

  // Effect A: agentKinds async-loads after mount with no match → fall back to first available.
  useEffect(() => {
    if (isEdit || isClone) return
    if (availableAgentKindOptions.length === 0) return
    if (availableAgentKindOptions.some(o => o.kind === agentKind)) return
    setAgentKind(availableAgentKindOptions[0]?.kind || '')
  }, [agentKind, availableAgentKindOptions, isEdit, isClone])

  // Effect D: project scope requires a project; if the current projectId is empty
  // or no longer matches the available list, fall back to the default or first project.
  useEffect(() => {
    if (isEdit || isClone) return
    if (projects.length === 0) return
    const fallbackProjects = projects.filter(p => !p.System)
    if (projectId && fallbackProjects.some(p => p.ActorId === projectId)) return
    if (forcedScope !== 'project') return
    const fallback = defaultProjectId && fallbackProjects.some(p => p.ActorId === defaultProjectId)
      ? defaultProjectId
      : (fallbackProjects[0]?.ActorId || '')
    setProjectId(fallback)
  }, [isEdit, isClone, forcedScope, projectId, projects, defaultProjectId])

  // Effect C: user changes agentKind → regenerate random display name (unless touched).
  useEffect(() => {
    if (displayNameTouchedRef.current) return
    if (isClone) return
    const selected = agentKinds.find(o => o.kind === agentKind)
    if (!selected) return
    const random = generateRandomName(selected.randomName, selected.namePool)
    if (random) setDisplayName(random)
  }, [agentKind, agentKinds, isClone])

  // Fetch the system aggregator's unit pool once — this is the fixed "auto" model
  // list for every slot, independent of any per-slot aggregator selection.
  useEffect(() => {
    if (!open || !systemAggActorId) {
      setAvailableModels([])
      return
    }
    let cancelled = false
    aiaggregator.status(client, { target: systemAggActorId })
      .then(resp => {
        if (!cancelled) setAvailableModels(resp.Units ?? [])
      })
      .catch(() => {
        if (!cancelled) setAvailableModels([])
      })
    return () => { cancelled = true }
  }, [open, systemAggActorId])

  const hasAnyAgentKinds = agentKinds.length > 0
  const hasAgentKinds = availableAgentKindOptions.length > 0
  // The system meta project is internal storage; it must never appear as a
  // selectable project for new agents.
  const userProjects = useMemo(() => projects.filter(p => !p.System), [projects])
  const hasProjects = userProjects.length > 0

  const selectedKind = availableAgentKindOptions.find(option => option.kind === agentKind) ?? null
  const unavailableKindReason = unavailableKindReasons.get(agentKind)

  // A slot's <option> value encodes its kind without exposing it to the user:
  //   "agg::<ActorId>"          → custom aggregator
  //   "<provider>::<model>"     → a model unit from the system aggregator pool
  //   ""                         → [auto] (system aggregator, no pinned unit)
  const AGG_PREFIX = 'agg::'
  // Encode by the aggregator's CONFIG ID (AggregatorDescriptor.Id), NOT its
  // actor ID: the backend resolves ModelRef.AggregatorID against the
  // aimanager config-ID-keyed aggregator cache, so storing the actor ID would
  // make aggregator-kind slots unresolvable.
  const encodeAggregator = (configId: string) => AGG_PREFIX + configId
  const encodeUnit = (u: AICallableUnitView) => `${u.ProviderName ?? ''}::${u.Model}`
  const unitLabel = (u: AICallableUnitView): string => {
    const base = `${u.Model} (${u.ProviderName})`
    if (u.CooldownUntil && u.CooldownUntil > Math.floor(Date.now() / 1000)) {
      return `${base} · ${t('dialog.newAgent.cooldown')}`
    }
    return base
  }
  const selectionToValue = (sel?: SlotSelection): string => {
    if (!sel) return ''
    if (sel.type === 'aggregator') return encodeAggregator(sel.aggregatorId)
    // A unit served by a custom aggregator is represented by that aggregator
    // (its unit isn't in the system pool the dropdown lists), so the slot keeps
    // showing the aggregator it belongs to when the dialog reopens.
    if (sel.aggregatorId && sel.aggregatorId !== SYSTEM_AGGREGATOR_ID) {
      return encodeAggregator(sel.aggregatorId)
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

  const validationError = validate({
    mode: resolvedMode,
    displayName,
    projectId,
    selectedKind,
    hasAgentKinds,
    hasAnyAgentKinds,
    hasProjects,
    unavailableKindReason,
    t,
  })
  const resolvedError = validationError || error
  const canCreate = !creating && !validationError

  const compactionModified = !compactionPoliciesEqual(draftCompaction, compactionPolicy || defaultCompactionPolicy())

  const handleSubmit = async () => {
    if (creating) return
    const submitError = validate({
      mode: resolvedMode,
      displayName,
      projectId,
      selectedKind,
      hasAgentKinds,
      hasAnyAgentKinds,
      hasProjects,
      unavailableKindReason,
      t,
    })
    if (submitError) return
    const submitFn = onSubmit ?? onCreate
    await submitFn?.({
      displayName: displayName.trim(),
      title: isEdit ? (title.trim() || undefined) : undefined,
      agentKind,
      projectId: !isEdit && effectiveScope === 'project' ? projectId : undefined,
      primarySelection,
      fastSelection,
      executionSelection,
      reviewSelection,
      summarySelection,
      // draftCompaction never carries a SummaryUnit (summary is now a slot).
      // When the agent had a legacy SummaryUnit override, compactionModified is
      // true (the draft's empty SummaryUnit differs), so submitting it clears it.
      compactionPolicy: compactionModified ? draftCompaction : undefined,
    })
    // Remember this selection as the default for the next create/clone.
    void saveRecent({ primary: primarySelection, fast: fastSelection, execution: executionSelection, review: reviewSelection, summary: summarySelection })
  }

  const isFork = isClone && !!props.forkTurnId
  const scopeLabel = effectiveScope === 'global' ? t('dialog.newAgent.scope.global') : t('dialog.newAgent.scope.project')
  const titleText = isEdit
    ? t('dialog.newAgent.title.edit')
    : isFork
      ? t('dialog.newAgent.title.fork')
      : isClone
        ? t('dialog.newAgent.title.clone')
        : t('dialog.newAgent.title.new')
  const actionText = isEdit
    ? (creating ? t('dialog.newAgent.action.saving') : t('dialog.newAgent.action.save'))
    : isFork
      ? (creating ? t('dialog.newAgent.action.forking') : t('dialog.newAgent.action.fork'))
      : isClone
        ? (creating ? t('dialog.newAgent.action.cloning') : t('dialog.newAgent.action.clone'))
        : (creating ? t('dialog.newAgent.action.creating') : t('dialog.newAgent.action.create'))

  return (
    <Modal
      open={open}
      title={
        <>
          <span className="new-agent-dialog-title">{titleText}</span>
          {!isEdit && <span className="new-agent-dialog-subtitle">{scopeLabel}</span>}
        </>
      }
      onClose={onCancel}
      disableClose={creating}
      size="md"
      className="new-agent-dialog"
      footer={
        <>
          <button type="button" className="modal-action modal-action--secondary" onClick={onCancel} disabled={creating}>{t('dialog.newAgent.cancel')}</button>
          <button type="button" className="modal-action modal-action--primary" onClick={() => void handleSubmit()} disabled={!canCreate}>
            {actionText}
          </button>
        </>
      }
    >
      {!isEdit && !isClone && (
        <label className="new-agent-dialog-field">
          <span className="new-agent-dialog-label">{t('dialog.newAgent.agentKind')}</span>
          <select
            className="new-agent-dialog-select"
            value={agentKind}
            onChange={event => setAgentKind(event.target.value)}
            disabled={creating || !hasAgentKinds}
          >
            {hasAgentKinds ? availableAgentKindOptions.map(option => (
              <option key={option.kind} value={option.kind}>{option.displayName}</option>
            )) : (
              <option value="">{t('dialog.newAgent.noCreatableKind')}</option>
            )}
          </select>
        </label>
      )}

      {isClone && selectedKind && (
        <div className="new-agent-dialog-field">
          <span className="new-agent-dialog-label">{t('dialog.newAgent.agentKind')}</span>
          <span className="new-agent-dialog-input" style={{ display: 'flex', alignItems: 'center', opacity: 0.7 }}>{selectedKind.displayName}</span>
        </div>
      )}

      {!isEdit && !isClone && effectiveScope === 'project' && (
        <label className="new-agent-dialog-field">
          <span className="new-agent-dialog-label">{t('dialog.newAgent.project')}</span>
          <select
            className="new-agent-dialog-select"
            value={projectId}
            onChange={event => setProjectId(event.target.value)}
            disabled={creating || !hasProjects}
          >
            {hasProjects ? userProjects.map(p => (
              <option key={p.ActorId ?? p.Name} value={p.ActorId ?? ""}>{projectDisplayName(p, t)}</option>
            )) : (
              <option value="">{t('dialog.newAgent.noProjects')}</option>
            )}
          </select>
        </label>
      )}

      {isClone && effectiveScope === 'project' && (
        <div className="new-agent-dialog-field">
          <span className="new-agent-dialog-label">{t('dialog.newAgent.project')}</span>
          <span className="new-agent-dialog-input" style={{ display: 'flex', alignItems: 'center', opacity: 0.7 }}>
            {projects.find(p => p.ActorId === projectId)?.Name || projectId || t('dialog.newAgent.unknown')}
          </span>
        </div>
      )}

      {isEdit && effectiveScope === 'project' && (
        <div className="new-agent-dialog-field">
          <span className="new-agent-dialog-label">{t('dialog.newAgent.project')}</span>
          <span className="new-agent-dialog-input" style={{ display: 'flex', alignItems: 'center', opacity: 0.7 }}>
            {projects.find(p => p.ActorId === projectId)?.Name || projectId || t('dialog.newAgent.unknown')}
          </span>
        </div>
      )}

      {isEdit && (
        <label className="new-agent-dialog-field">
          <span className="new-agent-dialog-label">{t('dialog.newAgent.sessionTitle')}</span>
          <input
            className="new-agent-dialog-input"
            value={title}
            onChange={event => setTitle(event.target.value)}
            disabled={creating}
            onKeyDown={event => {
              if (event.key === 'Enter' && canCreate) void handleSubmit()
            }}
          />
        </label>
      )}

      <label className="new-agent-dialog-field">
        <span className="new-agent-dialog-label">{t('dialog.newAgent.displayName')}</span>
        <input
          className="new-agent-dialog-input"
          value={displayName}
          onChange={event => {
            setDisplayName(event.target.value)
            displayNameTouchedRef.current = true
          }}
          placeholder={t('dialog.newAgent.displayNamePlaceholder')}
          disabled={creating}
          onKeyDown={event => {
            if (event.key === 'Enter' && canCreate) void handleSubmit()
          }}
        />
      </label>

      {(availableModels.length > 0 || slotAggregators.length > 0) && (
        <div className="new-agent-dialog-preset">
          <div className="new-agent-dialog-preset-header">
            <span className="new-agent-dialog-label">{t('dialog.newAgent.preset')}</span>
          </div>
          <div className="new-agent-dialog-preset-row">
            <div className="new-agent-dialog-preset-combobox" ref={presetComboRef}>
              <input
                className="new-agent-dialog-input new-agent-dialog-preset-field"
                value={presetName}
                onChange={event => setPresetName(event.target.value)}
                onFocus={() => setPresetOpen(true)}
                placeholder={t('dialog.newAgent.presetNamePlaceholder')}
                disabled={creating}
              />
              <button
                type="button"
                className="new-agent-dialog-preset-caret"
                onClick={() => setPresetOpen(o => !o)}
                tabIndex={-1}
                disabled={creating || presets.length === 0}
                aria-label={t('dialog.newAgent.preset')}
              >
                <ChevronDown size={14} />
              </button>
              {presetOpen && presets.length > 0 && (
                <ul className="new-agent-dialog-preset-list">
                  {presets.map(p => (
                    <li key={p.name} className={p.name === presetName.trim() ? 'new-agent-dialog-preset-list-active' : undefined}>
                      <button type="button" onClick={() => handlePickPreset(p.name)} disabled={creating}>
                        {p.name}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <button
              type="button"
              className="new-agent-dialog-icon-btn"
              title={t('dialog.newAgent.presetSave')}
              onClick={() => void handleSavePreset()}
              disabled={!canSave}
            >
              <Save size={15} />
            </button>
            {matchedPreset && (
              <button
                type="button"
                className="new-agent-dialog-icon-btn new-agent-dialog-icon-btn-danger"
                title={t('dialog.newAgent.presetDelete')}
                onClick={() => void handleDeletePreset()}
                disabled={creating}
              >
                <Trash2 size={15} />
              </button>
            )}
          </div>
        </div>
      )}

      {(availableModels.length > 0 || slotAggregators.length > 0) && (
        <label className="new-agent-dialog-field">
          <span className="new-agent-dialog-label">{t('dialog.newAgent.primaryModel')}</span>
          <select
            className="new-agent-dialog-select"
            value={selectionToValue(primarySelection)}
            onChange={event => { setPrimarySelection(valueToSelection(event.target.value)); markSlotsTouched() }}
            disabled={creating}
          >
            <option value="">{t('dialog.newAgent.autoFirst')}</option>
            {slotAggregators.map(a => (
              <option key={`agg-${a.ActorId}`} value={encodeAggregator(a.Id)}>{t('dialog.newAgent.aggregatorTag')} · {aggregatorLabel(a)}</option>
            ))}
            {availableModels.map(u => (
              <option key={`m-${u.Model}-${u.ProviderName}`} value={encodeUnit(u)}>{unitLabel(u)}</option>
            ))}
          </select>
        </label>
      )}

      {(availableModels.length > 0 || slotAggregators.length > 0) && (
        <div className="new-agent-dialog-models-row">
          <label className="new-agent-dialog-field">
            <span className="new-agent-dialog-label">{t('dialog.newAgent.fastModel')}</span>
            <select
              className="new-agent-dialog-select"
              value={selectionToValue(fastSelection)}
              onChange={event => { setFastSelection(valueToSelection(event.target.value)); markSlotsTouched() }}
              disabled={creating}
            >
              <option value="">{t('dialog.newAgent.auto')}</option>
              {slotAggregators.map(a => (
                <option key={`fast-agg-${a.ActorId}`} value={encodeAggregator(a.Id)}>{t('dialog.newAgent.aggregatorTag')} · {aggregatorLabel(a)}</option>
              ))}
              {availableModels.map(u => (
                <option key={`fast-m-${u.Model}-${u.ProviderName}`} value={encodeUnit(u)}>{unitLabel(u)}</option>
              ))}
            </select>
          </label>
          <label className="new-agent-dialog-field">
            <span className="new-agent-dialog-label">{t('dialog.newAgent.executionModel')}</span>
            <select className="new-agent-dialog-select" value={selectionToValue(executionSelection)} onChange={event => { setExecutionSelection(valueToSelection(event.target.value)); markSlotsTouched() }} disabled={creating}>
              <option value="">{t('dialog.newAgent.auto')}</option>
              {slotAggregators.map(a => (
                <option key={`exec-agg-${a.ActorId}`} value={encodeAggregator(a.Id)}>{t('dialog.newAgent.aggregatorTag')} · {aggregatorLabel(a)}</option>
              ))}
              {availableModels.map(u => <option key={`exec-m-${u.Model}-${u.ProviderName}`} value={encodeUnit(u)}>{unitLabel(u)}</option>)}
            </select>
          </label>
          <label className="new-agent-dialog-field">
            <span className="new-agent-dialog-label">{t('dialog.newAgent.reviewModel')}</span>
            <select className="new-agent-dialog-select" value={selectionToValue(reviewSelection)} onChange={event => { setReviewSelection(valueToSelection(event.target.value)); markSlotsTouched() }} disabled={creating}>
              <option value="">{t('dialog.newAgent.auto')}</option>
              {slotAggregators.map(a => (
                <option key={`review-agg-${a.ActorId}`} value={encodeAggregator(a.Id)}>{t('dialog.newAgent.aggregatorTag')} · {aggregatorLabel(a)}</option>
              ))}
              {availableModels.map(u => <option key={`review-m-${u.Model}-${u.ProviderName}`} value={encodeUnit(u)}>{unitLabel(u)}</option>)}
            </select>
          </label>
          <label className="new-agent-dialog-field">
            <span className="new-agent-dialog-label">{t('dialog.newAgent.summaryModel')}</span>
            <select className="new-agent-dialog-select" value={selectionToValue(summarySelection)} onChange={event => { setSummarySelection(valueToSelection(event.target.value)); markSlotsTouched() }} disabled={creating}>
              <option value="">{t('dialog.newAgent.auto')}</option>
              {slotAggregators.map(a => (
                <option key={`summary-agg-${a.ActorId}`} value={encodeAggregator(a.Id)}>{t('dialog.newAgent.aggregatorTag')} · {aggregatorLabel(a)}</option>
              ))}
              {availableModels.map(unit => <option key={`summary-m-${unit.Model}-${unit.ProviderName}`} value={encodeUnit(unit)}>{unitLabel(unit)}</option>)}
            </select>
          </label>
        </div>
      )}

      <div className="new-agent-dialog-compaction">
        <button
          type="button"
          className="new-agent-dialog-compaction-header"
          onClick={() => setCompactionOpen(prev => !prev)}
        >
          {compactionOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
          <span>{t('dialog.newAgent.compactionOverride')}</span>
          {compactionSource && (
            <span className="new-agent-dialog-compaction-source">{compactionSource}</span>
          )}
        </button>
        {compactionOpen && (
          <div className="new-agent-dialog-compaction-body">
            <label className="new-agent-dialog-compaction-toggle">
              <input
                type="checkbox"
                checked={draftCompaction.Enabled}
                onChange={event => setDraftCompaction(prev => ({ ...prev, Enabled: event.target.checked }))}
              />
              <span>{t('dialog.newAgent.enabled')}</span>
            </label>
            <div className="new-agent-dialog-compaction-grid">
              <label className="new-agent-dialog-field">
                <span className="new-agent-dialog-label">{t('dialog.newAgent.budgetMode')}</span>
                <select
                  className="new-agent-dialog-select"
                  value={draftCompaction.BudgetMode || 'absolute'}
                  onChange={event => setDraftCompaction(prev => ({ ...prev, BudgetMode: event.target.value }))}
                >
                  <option value="absolute">{t('dialog.newAgent.budgetAbsolute')}</option>
                  <option value="percentage">{t('dialog.newAgent.budgetPercentage')}</option>
                </select>
              </label>
              <label className="new-agent-dialog-field">
                <span className="new-agent-dialog-label">
                  {draftCompaction.BudgetMode === 'percentage' ? t('dialog.newAgent.tokenBudgetPercent') : t('dialog.newAgent.tokenBudget')}
                </span>
                <input
                  className="new-agent-dialog-input"
                  type="text"
                  inputMode="numeric"
                  value={draftCompaction.TokenBudget ?? ''}
                  onChange={event => setDraftCompaction(prev => ({ ...prev, TokenBudget: event.target.value === '' ? undefined : Number(event.target.value) }))}
                />
              </label>
              <label className="new-agent-dialog-field">
                <span className="new-agent-dialog-label">{t('dialog.newAgent.recentWindow')}</span>
                <input
                  className="new-agent-dialog-input"
                  type="text"
                  inputMode="numeric"
                  value={draftCompaction.RecentWindow ?? ''}
                  onChange={event => setDraftCompaction(prev => ({ ...prev, RecentWindow: event.target.value === '' ? undefined : Number(event.target.value) }))}
                />
              </label>
              <label className="new-agent-dialog-field">
                <span className="new-agent-dialog-label">{t('dialog.newAgent.maxSummaryTokens')}</span>
                <input
                  className="new-agent-dialog-input"
                  type="text"
                  inputMode="numeric"
                  value={draftCompaction.MaxSummaryTokens ?? ''}
                  onChange={event => setDraftCompaction(prev => ({ ...prev, MaxSummaryTokens: event.target.value === '' ? undefined : Number(event.target.value) }))}
                />
              </label>
            </div>
          </div>
        )}
      </div>

      {resolvedError ? <div className="new-agent-dialog-error">{resolvedError}</div> : null}
    </Modal>
  )
}
