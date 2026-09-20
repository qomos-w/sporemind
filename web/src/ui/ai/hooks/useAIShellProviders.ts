import { useEffect, useMemo, useState, useCallback, useRef } from 'react'
import type { AgentRef } from '../../../gen-types/aigen'
import type { AICallableUnitView, ModelUnit, DispatchActivity } from '../../../gen-clients/system/types'
import type { AggregatorDescriptor } from '../../../gen-types/aigen'
import * as aiaggregator from '../../../gen-clients/aiaggregator/client'
import * as aimanager_aggregator from '../../../gen-clients/aimanager/client'
import * as agentThinking from '../../../gen-clients/local/client'
import { client } from '../../../application/generated-client'
import type { AIShellContextValue } from '../context/AIShellContext'
import type { ProviderOption, ProviderGroup, ProviderItem, RefOption } from '../components/AIComposer'
import type { ThinkingLevel } from '../../../gen-clients/system/types'
import { slotToRoute, SYSTEM_AGGREGATOR_ID } from './modelSlot'

/** Returns true when a status unit is a child-aggregator reference (not a
 *  concrete selectable model). Reference entries carry an `aggregatorID` and
 *  have empty Model/ProviderName. Concrete units have empty `aggregatorID`. */
function isRefUnit(u: AICallableUnitView): boolean {
  return !!(u.aggregatorID && u.aggregatorID.trim() && !u.Model && !u.ProviderName)
}

/** Convert a status unit into a ModelUnit. Reference entries are skipped by
 *  the caller — this function assumes a concrete unit. */
function unitToModelUnit(u: AICallableUnitView): ModelUnit {
  return { model: u.Model, provider: u.ProviderName ?? '' }
}

function modelUnitKey(u: ModelUnit): string {
  return `${u.provider}::${u.model}`
}

/** Look up the global health entry for a concrete unit and project its health
 *  fields onto a `ProviderOption`. When the unit is not in the global cooldown
 *  map (i.e. healthy / never recorded) every health field is left undefined
 *  — the option renders as a healthy selectable row. The aggregator status
 *  health fields are deliberately NOT consumed: the global cooldown source is
 *  authoritative for unit cooldown state, regardless of tree membership. */
function healthFromGlobal(
  unitKey: string,
  healthMap: Map<string, AICallableUnitView>,
): Pick<ProviderOption, 'healthState' | 'healthReason' | 'cooldownUntil' | 'lastFailureAt' | 'recoveryMode'> {
  const entry = healthMap.get(unitKey)
  if (!entry) return {}
  return {
    healthState: entry.HealthState as ProviderOption['healthState'],
    healthReason: entry.HealthReason,
    cooldownUntil: entry.CooldownUntil,
    lastFailureAt: entry.LastFailureAt,
    recoveryMode: entry.RecoveryMode,
  }
}

function isUnhealthyEntry(health: ReturnType<typeof healthFromGlobal>): boolean {
  return health.healthState === 'cooling_down' || health.healthState === 'disabled'
}

/** Partition status units into concrete models and child-aggregator references.
 *  Reference entries (aggregatorID set, empty model) are excluded from the
 *  models list so they never produce empty-label pseudo-options.
 *
 *  Health is overlaid exclusively from the global cooldown map (keyed by
 *  `provider::model`); the status unit's own health fields are ignored so
 *  aggregator status is no longer the cooldown source. DispatchActivity is
 *  carried through as aggregator-local in-flight state. */
/** Returns true when the DispatchActivity belongs to the given agent. When no
 *  agentActorId is provided (e.g. new-chat page with no active agent), all
 *  dispatch activities are filtered out — the composer is agent-scoped, so
 *  showing another agent's in-flight state would be misleading. */
function matchesAgentActivity(
  activity: AICallableUnitView['DispatchActivity'],
  agentActorId?: string | null,
): boolean {
  if (!activity || !activity.State) return false
  if (!agentActorId) return false
  return activity.AgentId === agentActorId
}

function partitionUnits(
  units: AICallableUnitView[],
  aggregatorNameMap: Map<string, string>,
  healthMap: Map<string, AICallableUnitView>,
  agentActorId?: string | null,
): { models: ProviderOption[]; refs: RefOption[]; items: ProviderItem[] } {
  const modelOptionByKey = new Map<string, ProviderOption>()
  const refs: RefOption[] = []
  const items: ProviderItem[] = []

  const buildModelOption = (u: AICallableUnitView): ProviderOption => {
    const unit = unitToModelUnit(u)
    const health = healthFromGlobal(modelUnitKey(unit), healthMap)
    return {
      id: modelUnitKey(unit),
      label: u.Model,
      subtitle: u.ProviderName,
      unit,
      endpoint: u.Endpoint,
      ...health,
      dispatchActivity: matchesAgentActivity(u.DispatchActivity, agentActorId) ? (u.DispatchActivity as DispatchActivity | undefined) : undefined,
    }
  }

  for (const u of units) {
    // Skip on-demand units: the backend injects non-healthy units that are not
    // in the aggregator's pool into every aggregator's status response. These
    // should not be displayed as models — the global health overlay handles
    // surfacing cooling/disabled state on the unit's actual registrant.
    if (u.OnDemand) continue

    if (isRefUnit(u)) {
      // Refs represent child-aggregator availability (parent status projection).
      // Unit-level cooldown for ref rows is not a global-source concern — the
      // global query is unit-keyed, not aggregator-keyed — so the ref badge
      // keeps using the aggregator status projection.
      const ref: RefOption = {
        aggregatorId: u.aggregatorID!,
        label: aggregatorNameMap.get(u.aggregatorID!) || u.aggregatorID!,
        healthState: u.HealthState as RefOption['healthState'],
        healthReason: u.HealthReason,
        cooldownUntil: u.CooldownUntil,
        lastFailureAt: u.LastFailureAt,
        recoveryMode: u.RecoveryMode,
        dispatchActivity: matchesAgentActivity(u.DispatchActivity, agentActorId) ? (u.DispatchActivity as DispatchActivity | undefined) : undefined,
      }
      refs.push(ref)
      items.push({ kind: 'ref', option: ref })
      continue
    }
    const key = modelUnitKey(unitToModelUnit(u))
    const existing = modelOptionByKey.get(key)
    if (!existing) {
      const opt = buildModelOption(u)
      modelOptionByKey.set(key, opt)
      items.push({ kind: 'model', option: opt })
    } else {
      // When the same (provider, model) appears from multiple aggregators,
      // prefer keeping the unhealthy record so it stays visible.
      const candidate = buildModelOption(u)
      if (isUnhealthyEntry(candidate) && !isUnhealthyEntry(existing)) {
        Object.assign(existing, candidate)
      }
    }
  }
  const models: ProviderOption[] = items
    .filter((it): it is { kind: 'model'; option: ProviderOption } => it.kind === 'model')
    .map(it => it.option)
  return { models, refs, items }
}

function aggregatorLabel(a: AggregatorDescriptor): string {
  return a.Name || a.Id || a.ActorId
}

/** Rank dispatch activities so the most "active" one wins when bubbling up:
 *  in_use (currently serving) > trying (attempting) > waiting > anything else.
 *  Depth breaks ties in favour of the deeper activity, which is more
 *  representative of the nested subtree state. */
const ACTIVITY_RANK: Record<string, number> = { in_use: 0, trying: 1, waiting: 2, resolved: 3 }

function rankActivity(a: DispatchActivity): number {
  return ACTIVITY_RANK[a.State] ?? 99
}

/** Recursively compute the dispatch activity that should surface on each route
 *  header. The backend already projects child in-flight state onto a parent
 *  ref's `DispatchActivity`, but the route header must also reflect activity in
 *  its direct models and deeper descendants — this combines both sources so a
 *  collapsed parent still shows what its subtree is doing. */
function computeBubbledActivities(
  groups: ProviderGroup[],
): Map<string, DispatchActivity | undefined> {
  const groupMap = new Map(groups.map(g => [g.routeId, g]))
  const memo = new Map<string, DispatchActivity | undefined>()

  const visit = (routeId: string, ancestors: Set<string>): DispatchActivity | undefined => {
    if (memo.has(routeId)) return memo.get(routeId)
    if (ancestors.has(routeId)) { memo.set(routeId, undefined); return undefined }
    const g = groupMap.get(routeId)
    if (!g) { memo.set(routeId, undefined); return undefined }

    const candidates: DispatchActivity[] = []
    for (const m of g.models) {
      if (m.dispatchActivity) candidates.push(m.dispatchActivity)
    }
    for (const r of g.refs ?? []) {
      if (r.dispatchActivity) candidates.push(r.dispatchActivity)
      const child = visit(r.aggregatorId, new Set(ancestors).add(routeId))
      if (child) candidates.push(child)
    }

    let activity: DispatchActivity | undefined
    if (candidates.length > 0) {
      activity = candidates.slice().sort((a, b) => {
        const d = rankActivity(a) - rankActivity(b)
        return d !== 0 ? d : (b.Depth ?? 0) - (a.Depth ?? 0)
      })[0]
    }
    memo.set(routeId, activity)
    return activity
  }

  for (const g of groups) visit(g.routeId, new Set())
  return memo
}

/** Fetch the shared global cooldown snapshot via `aimanager.unit_health_list`
 *  and index it by the canonical `provider::model` key. Only non-healthy units
 *  (active cooldown or disabled) appear in this map; healthy units are absent,
 *  so a missing key means healthy. This is the authoritative cooldown source —
 *  aggregator status is no longer consulted for unit health. */
async function fetchGlobalHealthMap(): Promise<Map<string, AICallableUnitView>> {
  const map = new Map<string, AICallableUnitView>()
  try {
    const resp = await aimanager_aggregator.unitHealthList(client)
    for (const item of resp.Items ?? []) {
      // The backend sets Id = provider::model for global items; fall back to
      // recomputing the key when Id is missing (defensive — never persists).
      const key = item.Id || modelUnitKey({ model: item.Model, provider: item.ProviderName ?? '' })
      map.set(key, item)
    }
  } catch {
    // No global health reachable — treat every unit as healthy.
  }
  return map
}

/**
 * Fetch the route groups for the top-bar selector:
 *  - the system/auto pool (all provider models)
 *  - each custom aggregator's model pool
 *
 * Two data sources are composited:
 *  1. **Shared global cooldown** — a single `aimanager.unit_health_list` query,
 *     independent of the aggregator tree. Any cooling/disabled unit appears here
 *     regardless of tree membership, so cooldown badges are globally effective.
 *  2. **Aggregator selection & in-flight** — per-aggregator `aiaggregator.status`
 *     providing pool structure (models + child-aggregator refs) and the
 *     aggregator-local DispatchActivity (trying/in_use).
 *
 * Globally-cooling units are only used for health overlay on registered
 * models — they never add new entries to the provider list. Units that are
 * not in the aggregator's pool (OnDemand) are skipped by partitionUnits.
 */
export async function fetchRouteGroups(agentActorId?: string | null): Promise<ProviderGroup[]> {
  const [list, healthMap] = await Promise.all([
    aimanager_aggregator.aggregatorList(client).then(r => r.Items ?? []).catch(() => [] as AggregatorDescriptor[]),
    fetchGlobalHealthMap(),
  ])
  if (list.length === 0) {
    return []
  }

  const systemAgg = list.find(a => a.Id === SYSTEM_AGGREGATOR_ID) ?? list[0]!
  const customs = list.filter(a => a.Id !== systemAgg.Id)

  // Build a name map (ID → display name) so reference entries can resolve
  // child aggregator names without an extra network round-trip.
  const aggregatorNameMap = new Map<string, string>()
  for (const a of list) {
    aggregatorNameMap.set(a.Id, aggregatorLabel(a))
  }

  const fetchUnits = async (actorId: string): Promise<AICallableUnitView[]> => {
    try {
      const resp = await aiaggregator.status(client, { target: actorId })
      return resp.Units ?? []
    } catch {
      return []
    }
  }

  const [systemUnits, ...customUnitsList] = await Promise.all([
    fetchUnits(systemAgg.ActorId),
    ...customs.map(c => fetchUnits(c.ActorId)),
  ])

  const systemPartition = partitionUnits(systemUnits, aggregatorNameMap, healthMap, agentActorId)
  const groups: ProviderGroup[] = [{
    routeId: SYSTEM_AGGREGATOR_ID,
    label: systemAgg.Name || 'Auto',
    isAuto: true,
    models: systemPartition.models,
    refs: systemPartition.refs,
    items: systemPartition.items,
  }]
  customs.forEach((c, i) => {
    const partition = partitionUnits(customUnitsList[i] ?? [], aggregatorNameMap, healthMap, agentActorId)
    groups.push({
      routeId: c.Id,
      label: aggregatorLabel(c),
      isAuto: false,
      models: partition.models,
      refs: partition.refs,
      items: partition.items,
    })
  })

  // Every custom aggregator is exposed as a top-level route so it can be
  // selected independently, even when it is also referenced as a child by
  // another aggregator. Bubbled activity is still computed so route headers can
  // surface descendant retry/in-use state without expanding child refs.
  const bubbledMap = computeBubbledActivities(groups)
  // Attribute in-flight activity to the parent context first: when a child
  // aggregator's activity is already surfaced by a parent's ref row (same
  // session/agent/state), the child's own top-level row should not duplicate
  // it — state propagates from the parent downward, never leaking out to a
  // sibling top-level row. Only refs from *other* groups are considered, so a
  // group's own ref-row activity (which already belongs to it) does not suppress
  // its route header.
  const sameActivity = (a: DispatchActivity | undefined, b: DispatchActivity | undefined): boolean =>
    !!a && !!b && a.SessionId === b.SessionId && a.AgentId === b.AgentId && a.State === b.State
  for (const g of groups) {
    if (g.isAuto) continue
    const act = bubbledMap.get(g.routeId)
    if (!act) continue
    const attributedToParent = groups.some(parent =>
      parent !== g && !parent.isAuto &&
      (parent.refs ?? []).some(r => r.aggregatorId === g.routeId && sameActivity(r.dispatchActivity, act)),
    )
    if (attributedToParent) bubbledMap.set(g.routeId, undefined)
  }
  for (const g of groups) {
    g.isRoot = true
    g.bubbledActivity = bubbledMap.get(g.routeId)
  }

  return groups
}

export function useAIShellProviders(
  agentRef: AgentRef | null,
  currentUnit?: ModelUnit | null,
): AIShellContextValue {
  const [groups, setGroups] = useState<ProviderGroup[]>([])
  const [thinkingLevels, setThinkingLevels] = useState<Map<string, ThinkingLevel>>(new Map())
  const prevActorIdRef = useRef<string | undefined>(undefined)
  const [refreshTick, setRefreshTick] = useState(0)

  useEffect(() => {
    const handler = () => setRefreshTick(t => t + 1)
    window.addEventListener('sporemind:aggregators-changed', handler)
    return () => window.removeEventListener('sporemind:aggregators-changed', handler)
  }, [])

  useEffect(() => {
    const actorId = agentRef?.ActorId
    prevActorIdRef.current = actorId

    let cancelled = false
    fetchRouteGroups(agentRef?.ActorId)
      .then(next => { if (!cancelled) setGroups(next) })
      .catch(() => { if (!cancelled) setGroups([]) })

    if (agentRef?.ActorId) {
      agentThinking.thinkingGetRegistry(client, { target: agentRef.ActorId })
        .then(resp => {
          if (cancelled) return
          const m = new Map<string, ThinkingLevel>()
          for (const e of resp.Entries ?? []) {
            m.set(modelUnitKey(e.Unit), e.Level)
          }
          setThinkingLevels(m)
        })
        .catch(() => {})
    }

    return () => { cancelled = true }
  }, [agentRef?.ActorId, refreshTick])

  // Active route derived from the primary slot (authoritative source).
  const activeRoute = useMemo(() => slotToRoute(agentRef?.Primary), [agentRef?.Primary?.Candidates])

  // Flat list = system pool (keeps legacy consumers like GitQuickbar working).
  const providers = useMemo(() => {
    const autoGroup = groups.find(g => g.isAuto)
    return autoGroup?.models ?? []
  }, [groups])

  // The unit the thinking column / send path key off: the pinned unit when
  // pinned, else the aggregator-resolved current unit (β).
  const activeUnit = useMemo<ModelUnit | null>(() => {
    if (activeRoute.kind === 'unit') return activeRoute.unit
    if (currentUnit && currentUnit.model) return currentUnit
    return null
  }, [activeRoute, currentUnit])

  const activeProviderId = useMemo(() => activeUnit ? modelUnitKey(activeUnit) : '', [activeUnit])

  const activeThinkingLevel = useMemo(() => {
    if (!activeUnit) return null
    return thinkingLevels.get(modelUnitKey(activeUnit)) ?? null
  }, [thinkingLevels, activeUnit])

  const onThinkingLevelChange = useCallback((level: ThinkingLevel) => {
    if (!agentRef?.ActorId || !activeUnit) return
    setThinkingLevels(prev => {
      const next = new Map(prev)
      next.set(modelUnitKey(activeUnit), level)
      return next
    })
    agentThinking.thinkingSetLevel(client, {
      Unit: activeUnit,
      Level: level,
    }, { target: agentRef.ActorId }).catch(() => {})
  }, [agentRef?.ActorId, activeUnit])

  return useMemo(() => ({
    providers,
    groups,
    activeRoute,
    currentUnit: currentUnit ?? null,
    activeUnit,
    activeProviderId,
    onProviderChange: () => {},
    thinkingLevels,
    activeThinkingLevel,
    onThinkingLevelChange,
  }), [providers, groups, activeRoute, currentUnit, activeUnit, activeProviderId, thinkingLevels, activeThinkingLevel, onThinkingLevelChange])
}
