import { useCallback, useEffect, useState } from 'react'
import type { AggregatorDescriptor } from '../../../gen-types/aigen'
import type { ModelUnit } from '../../../gen-clients/system/types'
import { client } from '../../../application/generated-client'
import * as workspace_preferences from '../../../gen-clients/workspace/client'
import { savePreference } from '../../../application/theme-persist'
import type { SlotSelection } from './modelSlot'

/**
 * Model slot presets are a user-level preference: a named snapshot of the 5
 * NewAgentDialog slot selections (primary / fast / execution / review / summary).
 *
 * Persisted via the workspace actor's account preferences (constraint: durable
 * UI state must be actor-owned, never localStorage). Aggregators are stored by
 * their stable descriptor `Id` (e.g. "system"); on apply we map back to the
 * live `ActorId`, which may change across restarts.
 */

const PRESET_KEY = 'model-presets.v1'
const RECENT_KEY = 'model-recent.v1'

/** A single slot in persisted form. aggregatorId is AggregatorDescriptor.Id (stable).
 *  For a unit-kind slot, aggregatorId (when set) is the serving aggregator — the
 *  unit dispatches through it rather than the system aggregator. */
export type PresetSlot =
  | { type: 'aggregator'; aggregatorId: string }
  | { type: 'unit'; unit: ModelUnit; aggregatorId?: string }

export interface ModelPreset {
  name: string
  primary?: PresetSlot
  fast?: PresetSlot
  execution?: PresetSlot
  review?: PresetSlot
  /** Summary slot, persisted as a PresetSlot (aggregator or unit), like the other slots. */
  summary?: PresetSlot
}

export type SlotKey = 'primary' | 'fast' | 'execution' | 'review'
export type SlotMap = Record<SlotKey, SlotSelection | undefined>

/** Slot selections plus the summary slot, all parts of a preset snapshot. */
export interface PresetSnapshot extends SlotMap {
  summary?: SlotSelection
}

/**
 * Persisted form of the most-recent slot selections (the "last used" default for
 * the next create/clone). Same shape as a preset minus the name; aggregatorId is
 * AggregatorDescriptor.Id (stable).
 */
export interface RecentSlotSnapshot {
  primary?: PresetSlot
  fast?: PresetSlot
  execution?: PresetSlot
  review?: PresetSlot
  /** Summary slot, persisted as a PresetSlot. */
  summary?: PresetSlot
}

// RequestId counter for savePreference dedup; version is resolved inside savePreference.
const writeVersion = { v: 0 }

// SlotSelection.aggregatorId is the aggregator's CONFIG id (AggregatorDescriptor.Id),
// the same form persisted in PresetSlot — so the only conversion is validating the
// aggregator is still live. (An earlier version mapped through ActorId, which
// mismatched NewAgentDialog's value encoding and dropped aggregator selections.)
function slotSelectionToPreset(sel: SlotSelection | undefined, aggregators: AggregatorDescriptor[]): PresetSlot | undefined {
  if (!sel) return undefined
  if (sel.type === 'unit') {
    const servingAlive = !sel.aggregatorId || aggregators.some(a => a.Id === sel.aggregatorId)
    const out: PresetSlot = { type: 'unit', unit: sel.unit }
    if (sel.aggregatorId && servingAlive) out.aggregatorId = sel.aggregatorId // drop stale serving agg, keep the unit (system-served fallback)
    return out
  }
  if (!aggregators.some(a => a.Id === sel.aggregatorId)) return undefined // aggregator vanished since selection; drop rather than store a stale id
  return { type: 'aggregator', aggregatorId: sel.aggregatorId }
}

function presetSlotToSelection(slot: PresetSlot | undefined, aggregators: AggregatorDescriptor[]): SlotSelection | undefined {
  if (!slot) return undefined
  if (slot.type === 'unit') {
    const servingAlive = !slot.aggregatorId || aggregators.some(a => a.Id === slot.aggregatorId)
    const out: SlotSelection = { type: 'unit', unit: slot.unit }
    if (slot.aggregatorId && servingAlive) out.aggregatorId = slot.aggregatorId
    return out
  }
  if (!aggregators.some(a => a.Id === slot.aggregatorId)) return undefined // aggregator not present in live list; fall back to [auto]
  return { type: 'aggregator', aggregatorId: slot.aggregatorId }
}

/**
 * Coerce a persisted summary value into a PresetSlot. New presets store a
 * PresetSlot (with a `type` discriminator); older presets stored a bare
 * ModelUnit ({ provider, model }) before summary became a full slot. This
 * normalizes both shapes so legacy data does not break apply/save.
 */
function normalizeSummaryPreset(raw: unknown): PresetSlot | undefined {
  if (!raw || typeof raw !== 'object') return undefined
  const r = raw as Record<string, unknown>
  if (r.type === 'aggregator' && typeof r.aggregatorId === 'string') {
    return { type: 'aggregator', aggregatorId: r.aggregatorId }
  }
  if (r.type === 'unit') {
    const unit = r.unit as ModelUnit | undefined
    if (unit && typeof unit.model === 'string' && unit.model && typeof unit.provider === 'string' && unit.provider) {
      const out: PresetSlot = { type: 'unit', unit }
      if (typeof r.aggregatorId === 'string') out.aggregatorId = r.aggregatorId
      return out
    }
    return undefined
  }
  // Legacy bare ModelUnit shape { provider, model } — requires both fields.
  if (typeof r.model === 'string' && r.model && typeof r.provider === 'string' && r.provider) {
    return { type: 'unit', unit: { model: r.model, provider: r.provider } }
  }
  return undefined
}

export function useModelPresets(aggregators: AggregatorDescriptor[]) {
  const [presets, setPresets] = useState<ModelPreset[]>([])
  const [recent, setRecent] = useState<RecentSlotSnapshot | null>(null)
  const [loaded, setLoaded] = useState(false)

  const load = useCallback(async () => {
    try {
      const snap = await workspace_preferences.preferencesGet(client)
      writeVersion.v = snap.Version ?? 0
      const raw = snap.Preferences?.[PRESET_KEY]
      const parsed = raw ? (JSON.parse(raw) as ModelPreset[]) : []
      const normalized = (Array.isArray(parsed) ? parsed : []).map(p => ({ ...p, summary: normalizeSummaryPreset(p.summary) }))
      setPresets(normalized)
      const recentRaw = snap.Preferences?.[RECENT_KEY]
      const recentParsed = recentRaw ? (JSON.parse(recentRaw) as RecentSlotSnapshot) : null
      setRecent(recentParsed ? { ...recentParsed, summary: normalizeSummaryPreset(recentParsed.summary) } : null)
    } catch {
      setPresets([])
      setRecent(null)
    }
    setLoaded(true)
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const persist = useCallback(async (next: ModelPreset[]) => {
    await savePreference(PRESET_KEY, JSON.stringify(next), 'model-presets', writeVersion)
    setPresets(next)
  }, [])

  /** Save current slot selections (incl. summary unit) under `name`. Overwrites if a preset with the same name exists. */
  const savePreset = useCallback(async (name: string, snapshot: PresetSnapshot) => {
    const preset: ModelPreset = {
      name,
      primary: slotSelectionToPreset(snapshot.primary, aggregators),
      fast: slotSelectionToPreset(snapshot.fast, aggregators),
      execution: slotSelectionToPreset(snapshot.execution, aggregators),
      review: slotSelectionToPreset(snapshot.review, aggregators),
      summary: slotSelectionToPreset(snapshot.summary, aggregators),
    }
    const idx = presets.findIndex(p => p.name === name)
    const next = idx >= 0
      ? presets.map((p, i) => (i === idx ? preset : p))
      : [...presets, preset]
    await persist(next)
  }, [presets, aggregators, persist])

  const deletePreset = useCallback(async (name: string) => {
    await persist(presets.filter(p => p.name !== name))
  }, [presets, persist])

  /** Resolve a preset's stored descriptor.Ids back to live SlotSelections + summary unit. */
  const applyPreset = useCallback((preset: ModelPreset): PresetSnapshot => ({
    primary: presetSlotToSelection(preset.primary, aggregators),
    fast: presetSlotToSelection(preset.fast, aggregators),
    execution: presetSlotToSelection(preset.execution, aggregators),
    review: presetSlotToSelection(preset.review, aggregators),
    summary: presetSlotToSelection(preset.summary, aggregators),
  }), [aggregators])

  /** Persist the most-recent slot selections as the default for the next create/clone. */
  const saveRecent = useCallback(async (snapshot: PresetSnapshot) => {
    const r: RecentSlotSnapshot = {
      primary: slotSelectionToPreset(snapshot.primary, aggregators),
      fast: slotSelectionToPreset(snapshot.fast, aggregators),
      execution: slotSelectionToPreset(snapshot.execution, aggregators),
      review: slotSelectionToPreset(snapshot.review, aggregators),
      summary: slotSelectionToPreset(snapshot.summary, aggregators),
    }
    await savePreference(RECENT_KEY, JSON.stringify(r), 'model-recent', writeVersion)
  }, [aggregators])

  /** Resolve a recent snapshot's stored descriptor.Ids back to live SlotSelections + summary unit. */
  const applyRecent = useCallback((r: RecentSlotSnapshot): PresetSnapshot => ({
    primary: presetSlotToSelection(r.primary, aggregators),
    fast: presetSlotToSelection(r.fast, aggregators),
    execution: presetSlotToSelection(r.execution, aggregators),
    review: presetSlotToSelection(r.review, aggregators),
    summary: presetSlotToSelection(r.summary, aggregators),
  }), [aggregators])

  return { presets, loaded, recent, recentLoaded: loaded, savePreset, deletePreset, applyPreset, saveRecent, applyRecent }
}
