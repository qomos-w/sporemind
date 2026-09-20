import { useCallback, useEffect, useSyncExternalStore } from 'react'
import { gitStore } from '../../panels/git-store'
import { client } from '../../../application/generated-client'
import * as agentCompaction from '../../../gen-clients/local/client'
import type { AgentChildRef, AgentListItem, AgentRuntimeState, CompactionPolicy, ModelSlot } from '../../../gen-clients/system/types'
import { getAgentListSnapshot, subscribeAgentListStore } from './agentListStore'

export type AgentStatus = 'idle' | 'waiting' | 'running' | 'paused' | 'error' | 'completed' | 'ask_user' | 'ask_permission' | 'plan_approval' | 'goal_submit'

export const AGENT_STATUS_LABELS: Record<AgentStatus, string> = {
  idle: 'idle',
  waiting: 'waiting',
  running: 'running',
  paused: 'paused',
  error: 'error',
  completed: 'completed',
  ask_user: 'ask user',
  ask_permission: 'ask permission',
  plan_approval: 'plan approval',
  goal_submit: 'goal submit',
}

export interface AgentInfo {
  Id: string
  ActorId: string
  DisplayName: string
  Title: string
  HasTitle: boolean
  AgentKind: string
  ProjectId: string
  ProjectName: string
  LoadState?: string
  Primary?: ModelSlot
  Fast?: ModelSlot
  Execution?: ModelSlot
  Review?: ModelSlot
  Summary?: ModelSlot
  ThinkLevel?: string
  Status: AgentStatus
  StatusLabel: string
  IsWorking: boolean
  IsError: boolean
  IsCompleted: boolean
  IsAskUserPermission: boolean
  IsAskUser: boolean
  IsAskPermission: boolean
  IsPlanApproval: boolean
  IsGoalSubmit: boolean
  ErrorMessage?: string
  ActiveTurnRef?: string
  ActiveWorkflowMapCardId?: string
  BoundTaskCardId?: string
  LastActivity?: string
  LastTurnCompletedAt?: string
  LastAccessedAt?: string
  Degraded?: boolean
  DegradedReason?: string
  Runtime?: AgentRuntimeState
  CompactionPolicy?: CompactionPolicy
  CompactionPolicyLoading: boolean
  CompactionPolicyError?: string
  CompactionPolicySource?: string
  CanDelete: boolean
  ParentAgentId?: string
  LifecycleScope?: string
  DeletionStatus?: string
  ConversationTarget?: string
  Children?: AgentChildRef[]
  // Dedicated plugin-agent binding (AgentKind === 'plugin'): the owning app id
  // and slot name. The sidebar groups these agents under a virtual per-app
  // directory; both empty for all other agents.
  BoundAppId?: string
  BoundAppSlot?: string
  MemoryMounted?: boolean
  PermissionMode?: string
  WorktreeID?: string
  WorktreeName?: string
  WorktreeStatus?: string
}

export interface AgentInfoSnapshot {
  version: number
  loading: boolean
  items: AgentInfo[]
  byActorId: Map<string, AgentInfo>
  byId: Map<string, AgentInfo>
}

interface CompactionEntry {
  policy?: CompactionPolicy
  source?: string
  loading: boolean
  error?: string
}

export interface ProjectStatus {
  isWorking: boolean
  isError: boolean
  statusLabel: string
}

export function aggregateProjectStatus(agents: AgentInfo[]): Map<string, ProjectStatus> {
  const counts = new Map<string, { working: number; error: number }>()
  for (const a of agents) {
    const c = counts.get(a.ProjectId) ?? { working: 0, error: 0 }
    if (a.IsWorking) c.working += 1
    if (a.IsError) c.error += 1
    counts.set(a.ProjectId, c)
  }
  const out = new Map<string, ProjectStatus>()
  for (const [pid, c] of counts) {
    if (c.working === 0 && c.error === 0) continue
    const statusLabel = c.working > 0
      ? `${c.working} running${c.error > 0 ? `, ${c.error} errored` : ''}`
      : `${c.error} errored`
    out.set(pid, {
      isWorking: c.working > 0,
      isError: c.error > 0,
      statusLabel,
    })
  }
  return out
}

let snapshot: AgentInfoSnapshot = {
  version: 0,
  loading: true,
  items: [],
  byActorId: new Map(),
  byId: new Map(),
}
let listeners = new Set<() => void>()
let started = false
let offAgentList: (() => void) | null = null
let offGit: (() => void) | null = null
const compactionPolicies = new Map<string, CompactionEntry>()

function emit() {
  for (const cb of listeners) {
    cb()
  }
}

function getProjectName(projectId: string): string {
  const project = gitStore.state.projects.find(p => p.ProjectID === projectId)
  return project?.Name ?? projectId
}

export function computeAgentStatus(runtime: AgentRuntimeState | undefined): AgentStatus {
  if (!runtime) return 'idle'
  if (runtime.PlanApprovalPending) return 'plan_approval'
  if (runtime.GoalSubmitPending) return 'goal_submit'
  if (runtime.AskUserPending) return 'ask_user'
  if (runtime.ApprovalPending) return 'ask_permission'
  if (runtime.Error || runtime.State === 'failed') return 'error'
  if (runtime.State === 'paused') return 'paused'
  if (runtime.State === 'running') return 'running'
  // A workflow map owner's finished turn parks in State='waiting' — its
  // normal steady state between turns. It is alive and bound to a live
  // workflow (the updater re-wakes it), so it is neither idle nor completed.
  if (runtime.State === 'waiting') return 'waiting'
  if (runtime.State === 'completed') return 'completed'
  return 'idle'
}

// Window during which a freshly completed agent shows an "unread completion"
// badge until the user opens its conversation.
export const COMPLETION_BADGE_WINDOW_MS = 5 * 60 * 1000

// hasUnreadCompletion reports whether the agent finished a turn within the
// badge window, is no longer working on it, and the user has not opened its
// conversation since that completion.
//
// The guard deliberately mirrors the completion sound's fire condition in
// useTurnCompleteNotifier (which plays only when the agent stays non-working
// past the goal debounce): a goal-driven agent oscillates
// running→completed→running between autonomous turns, so a turn's
// LastTurnCompletedAt may be fresh while the next turn is already running —
// the sound cancels in that case, and so must the badge. Conversely the
// terminal state can be "completed" OR plain "idle" (State=""), and the sound
// fires in both — gating the badge on IsCompleted missed the idle variant
// (the "sound without badge" report). Neither side keys on State directly;
// both key on "not actively working".
// Pure so the sidebar and avatar bar share one rule; `now` is injectable for
// tests and periodic re-render ticks.
export function hasUnreadCompletion(agent: AgentInfo, now = Date.now()): boolean {
  if (agent.IsWorking || agent.IsError || agent.Status === 'paused') return false
  if (!agent.LastTurnCompletedAt) return false
  const completedAt = Date.parse(agent.LastTurnCompletedAt)
  if (Number.isNaN(completedAt)) return false
  if (now - completedAt < 0 || now - completedAt > COMPLETION_BADGE_WINDOW_MS) return false
  if (!agent.LastAccessedAt) return true
  const accessedAt = Date.parse(agent.LastAccessedAt)
  return Number.isNaN(accessedAt) || accessedAt < completedAt
}

// agentStatusClass maps a status to the single CSS modifier shared across the
// sidebar, avatar bar, and topology view. Concentrating this in one place stops
// the three consumers from silently diverging (e.g. the avatar dropdown once
// omitted "paused", so paused agents rendered as idle).
export function agentStatusClass(status: AgentStatus): string {
  switch (status) {
    case 'error': return 'error'
    case 'plan_approval': return 'plan-approval'
    case 'goal_submit': return 'goal-submit'
    case 'ask_user': return 'ask-user'
    case 'ask_permission': return 'ask-permission'
    case 'paused': return 'paused'
    case 'running': return 'working'
    case 'waiting': return 'waiting'
    case 'completed': return 'completed'
    default: return 'idle'
  }
}

export function buildAgentInfo(item: AgentListItem): AgentInfo {
  const rt = item.Runtime
  const status = computeAgentStatus(rt)
  const compaction = compactionPolicies.get(item.ActorId || item.Id)
  return {
    Id: item.Id,
    ActorId: item.ActorId,
    DisplayName: item.DisplayName,
    Title: item.Title || item.DisplayName,
    HasTitle: !!item.Title,
    AgentKind: item.AgentKind,
    ProjectId: item.ProjectId,
    ProjectName: getProjectName(item.ProjectId),
    LoadState: item.LoadState ?? 'unloaded',
    Primary: item.Primary,
    Fast: item.Fast,
    Execution: item.Execution,
    Review: item.Review,
    Summary: item.Summary,
    ThinkLevel: item.ThinkLevel || rt?.ThinkLevel,
    Status: status,
    StatusLabel: AGENT_STATUS_LABELS[status],
    IsWorking: status === 'running' || status === 'ask_user' || status === 'ask_permission' || status === 'plan_approval' || status === 'goal_submit',
    IsError: status === 'error',
    IsCompleted: status === 'completed',
    IsAskUserPermission: status === 'ask_user' || status === 'ask_permission' || status === 'plan_approval' || status === 'goal_submit',
    IsAskUser: status === 'ask_user',
    IsAskPermission: status === 'ask_permission',
    IsPlanApproval: status === 'plan_approval',
    IsGoalSubmit: status === 'goal_submit',
    ErrorMessage: rt?.Error,
    ActiveTurnRef: rt?.ActiveTurnRef,
    ActiveWorkflowMapCardId: item.Mode?.ActiveWorkflowMapCardId || rt?.ActiveWorkflowMapCardId,
    BoundTaskCardId: item.Mode?.BoundTaskCardId || rt?.BoundTaskCardId,
    LastActivity: item.LastActivity || rt?.LastActivity,
    LastTurnCompletedAt: rt?.LastTurnCompletedAt,
    LastAccessedAt: rt?.LastAccessedAt,
    Degraded: item.Degraded,
    DegradedReason: item.DegradedReason,
    Runtime: rt,
    CompactionPolicy: item.CompactionPolicy || compaction?.policy,
    CompactionPolicyLoading: compaction?.loading ?? false,
    CompactionPolicyError: compaction?.error,
    CompactionPolicySource: compaction?.source,
    CanDelete: (item as { CanDelete?: boolean }).CanDelete ?? true,
    ParentAgentId: item.ParentAgentId,
    LifecycleScope: item.LifecycleScope,
    DeletionStatus: item.DeletionStatus,
    ConversationTarget: item.ConversationTarget,
    Children: item.Children,
    BoundAppId: item.BoundAppId,
    BoundAppSlot: item.BoundAppSlot,
    MemoryMounted: item.Mode?.MemoryMounted ?? item.Runtime?.MemoryMounted ?? false,
    PermissionMode: item.PermissionMode ?? item.Runtime?.PermissionMode ?? '',
    WorktreeID: item.Runtime?.WorktreeID || '',
    WorktreeName: item.Runtime?.WorktreeName || '',
    WorktreeStatus: item.Runtime?.WorktreeStatus || '',
  }
}

/** Content equality between two AgentInfo objects. Backend events rebuild the
 *  whole items array as fresh JSON objects, so reference equality is useless
 *  here; field values (all JSON-safe: primitives or decoded backend objects)
 *  are compared via serialization. Objects that survive this check keep their
 *  previous identity so useSyncExternalStore (Object.is) skips re-rendering
 *  every subscriber — one avatar per workflow node times every status/git
 *  event otherwise re-renders the whole graph. */
function sameAgentInfo(a: AgentInfo, b: AgentInfo): boolean {
  const ka = Object.keys(a)
  const kb = Object.keys(b)
  if (ka.length !== kb.length) return false
  for (const key of ka) {
    if (!(key in b)) return false
    if (JSON.stringify(a[key as keyof AgentInfo]) !== JSON.stringify(b[key as keyof AgentInfo])) return false
  }
  return true
}

function recompute() {
  const agentSnapshot = getAgentListSnapshot()
  const prevById = snapshot.byId
  const items = agentSnapshot.items.map(item => {
    const next = buildAgentInfo(item)
    const prev = prevById.get(next.Id)
    return prev && sameAgentInfo(prev, next) ? prev : next
  })
  // Keep the previous snapshot object when nothing observable changed: same
  // item count and every position holds the reused identity. A reorder or any
  // rebuilt item produces a new reference so consumers re-render. The stale
  // `version` field is harmless — nothing reads it after `loading` is derived.
  const unchanged =
    items.length === snapshot.items.length &&
    items.every((info, i) => info === snapshot.items[i])
  const loading = agentSnapshot.version === 0
  if (unchanged && snapshot.loading === loading) return
  const byActorId = new Map<string, AgentInfo>()
  const byId = new Map<string, AgentInfo>()
  for (const info of items) {
    byActorId.set(info.ActorId, info)
    byId.set(info.Id, info)
  }
  snapshot = {
    version: agentSnapshot.version,
    loading: agentSnapshot.version === 0,
    items,
    byActorId,
    byId,
  }
}

function start() {
  if (started) return
  started = true

  recompute()

  offAgentList = subscribeAgentListStore(() => {
    recompute()
    emit()
  })

  offGit = gitStore.subscribe(() => {
    recompute()
    emit()
  })

  gitStore.startMountListener()
  if (gitStore.state.projects.length === 0) {
    void gitStore.loadProjects()
  }
}

function stop() {
  if (!started) return
  started = false
  offAgentList?.()
  offAgentList = null
  offGit?.()
  offGit = null
}

export function resetAgentInfoStore(): void {
  stop()
  snapshot = {
    version: 0,
    loading: true,
    items: [],
    byActorId: new Map(),
    byId: new Map(),
  }
  compactionPolicies.clear()
}

export function subscribeAgentInfoStore(cb: () => void): () => void {
  start()
  listeners.add(cb)
  return () => {
    listeners.delete(cb)
    if (listeners.size === 0) {
      stop()
    }
  }
}

export function getAgentInfoSnapshot(): AgentInfoSnapshot {
  return snapshot
}

export function getAgentInfoList(): AgentInfo[] {
  return snapshot.items
}

export function getAgentInfoByActorId(actorId: string | undefined): AgentInfo | undefined {
  if (!actorId) return undefined
  return snapshot.byActorId.get(actorId)
}

export function getAgentInfoById(id: string | undefined): AgentInfo | undefined {
  if (!id) return undefined
  return snapshot.byId.get(id)
}

export function patchAgentActorId(id: string, actorId: string): void {
  const info = snapshot.byId.get(id)
  if (!info || info.ActorId === actorId) return
  // Copy into a new object + new snapshot reference so useSyncExternalStore
  // (Object.is) detects the change. Mutating in place left the same object
  // reference, so useAgentInfo(actorId) and useAgentInfoList() skipped
  // re-rendering until the next unrelated agentListStore event — lazy-spawned
  // agents didn't update the sidebar until then.
  //
  // NOTE: do NOT call recompute() here. recompute() rebuilds from
  // getAgentListSnapshot(), whose ActorId is not yet updated for a lazy spawn
  // (the patch is an optimistic local update); recompute would overwrite it.
  const next: AgentInfo = { ...info, ActorId: actorId }
  const byActorId = new Map(snapshot.byActorId)
  byActorId.delete(info.ActorId)
  byActorId.set(actorId, next)
  const byId = new Map(snapshot.byId)
  byId.set(id, next)
  const items = snapshot.items.slice()
  const idx = items.findIndex(item => item.Id === id)
  if (idx >= 0) items[idx] = next
  snapshot = { ...snapshot, items, byActorId, byId }
  emit()
}

export async function loadAgentCompactionPolicy(actorId: string): Promise<void> {
  const existing = compactionPolicies.get(actorId)
  if (existing?.loading) return

  compactionPolicies.set(actorId, { ...existing, loading: true, error: undefined })
  recompute()
  emit()

  try {
    const resp = await agentCompaction.compactionConfigure(
      client,
      { CompactionPolicy: undefined },
      { target: actorId },
    )
    compactionPolicies.set(actorId, {
      policy: resp.CompactionPolicy,
      source: resp.Source,
      loading: false,
      error: undefined,
    })
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err)
    compactionPolicies.set(actorId, { ...existing, loading: false, error: message })
  }

  recompute()
  emit()
}

export async function saveAgentCompactionPolicy(actorId: string, policy: CompactionPolicy): Promise<void> {
  const existing = compactionPolicies.get(actorId)
  compactionPolicies.set(actorId, { ...existing, loading: true, error: undefined })
  recompute()
  emit()

  try {
    const resp = await agentCompaction.compactionConfigure(
      client,
      { CompactionPolicy: policy },
      { target: actorId },
    )
    compactionPolicies.set(actorId, {
      policy: resp.CompactionPolicy,
      source: resp.Source,
      loading: false,
      error: undefined,
    })
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err)
    compactionPolicies.set(actorId, { ...existing, loading: false, error: message })
  }

  recompute()
  emit()
}

export function useAgentInfoList(): AgentInfoSnapshot {
  const subscribe = useCallback((cb: () => void) => subscribeAgentInfoStore(cb), [])
  return useSyncExternalStore(subscribe, getAgentInfoSnapshot, getAgentInfoSnapshot)
}

export function useAgentInfo(actorId: string | null | undefined): AgentInfo | undefined {
  const subscribe = useCallback((cb: () => void) => subscribeAgentInfoStore(cb), [])
  const getSnapshot = useCallback(() => {
    return actorId ? snapshot.byActorId.get(actorId) : undefined
  }, [actorId])
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}

export function useAgentInfoWithCompaction(actorId: string | null | undefined): AgentInfo | undefined {
  const info = useAgentInfo(actorId)

  useEffect(() => {
    if (!actorId) return
    if (info && !info.CompactionPolicy && !info.CompactionPolicyLoading && !info.CompactionPolicyError) {
      void loadAgentCompactionPolicy(actorId)
    }
  }, [actorId, info?.CompactionPolicy, info?.CompactionPolicyLoading, info?.CompactionPolicyError])

  return info
}
