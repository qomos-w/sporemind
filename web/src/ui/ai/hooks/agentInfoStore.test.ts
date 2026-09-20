import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { AgentListItem, AgentRuntimeState, CompactionPolicy } from '../../../gen-clients/system/types'
import { buildAgentInfo, computeAgentStatus, agentStatusClass, AGENT_STATUS_LABELS, aggregateProjectStatus, hasUnreadCompletion, COMPLETION_BADGE_WINDOW_MS, loadAgentCompactionPolicy, patchAgentActorId, type AgentInfo } from './agentInfoStore'

const mockAgentListSnapshot = {
  version: 1,
  full: true,
  items: [] as AgentListItem[],
}

vi.mock('./agentListStore', () => ({
  getAgentListSnapshot: () => mockAgentListSnapshot,
  subscribeAgentListStore: () => () => {},
}))

vi.mock('../../panels/git-store', () => ({
  gitStore: {
    state: { projects: [] },
    subscribe: () => () => {},
    startMountListener: vi.fn(),
    loadProjects: vi.fn(),
  },
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/local/client', () => ({
  compactionConfigure: vi.fn(),
}))

function runtime(partial: Partial<AgentRuntimeState> = {}): AgentRuntimeState {
  return {
    State: 'idle',
    ThinkLevel: '',
    ...partial,
  } as AgentRuntimeState
}

describe('hasUnreadCompletion', () => {
  const now = Date.parse('2026-08-15T10:00:00Z')

  function completedAgent(partial: Partial<AgentInfo> = {}): AgentInfo {
    return {
      IsCompleted: true,
      LastTurnCompletedAt: '2026-08-15T09:58:00Z',
      ...partial,
    } as AgentInfo
  }

  it('is true when completed within the window and never accessed', () => {
    expect(hasUnreadCompletion(completedAgent(), now)).toBe(true)
  })

  it('is hidden while the agent has resumed working (goal cycle)', () => {
    // A goal-driven agent oscillates running→completed→running between turns;
    // a fresh LastTurnCompletedAt while running means the next turn is already
    // in flight — the completion sound cancels in that case, and so must the
    // badge (mirrors the notifier's debounce-time guard).
    expect(hasUnreadCompletion(completedAgent({ IsWorking: true }), now)).toBe(false)
  })

  it('stays lit for a terminal idle state, not only "completed"', () => {
    // The sound fires whenever the agent settles non-working, which includes
    // State="" (idle) after a goal ends; the badge must not require the
    // transient "completed" state (the original sound-without-badge bug).
    expect(hasUnreadCompletion(completedAgent({ IsCompleted: false }), now)).toBe(true)
  })

  it('is hidden while paused or error (interaction badges own those states)', () => {
    expect(hasUnreadCompletion(completedAgent({ Status: 'paused' }), now)).toBe(false)
    expect(hasUnreadCompletion(completedAgent({ IsError: true }), now)).toBe(false)
  })

  it('is false when completion is older than the window', () => {
    const old = new Date(now - COMPLETION_BADGE_WINDOW_MS - 1000).toISOString()
    expect(hasUnreadCompletion(completedAgent({ LastTurnCompletedAt: old }), now)).toBe(false)
  })

  it('is false when accessed after completion', () => {
    expect(hasUnreadCompletion(completedAgent({ LastAccessedAt: '2026-08-15T09:59:00Z' }), now)).toBe(false)
  })

  it('is true when last access predates the completion', () => {
    expect(hasUnreadCompletion(completedAgent({ LastAccessedAt: '2026-08-15T09:57:00Z' }), now)).toBe(true)
  })

  it('is false without a completion timestamp', () => {
    expect(hasUnreadCompletion(completedAgent({ LastTurnCompletedAt: undefined }), now)).toBe(false)
  })
})

describe('buildAgentInfo', () => {
  it('keeps stable identity and sidebar mode for unloaded agents', () => {
    const info = buildAgentInfo({
      Id: 'agent-1',
      ActorId: '019f30e43afd00000000000000000004',
      DisplayName: 'Agent One',
      AgentKind: 'coder',
      ProjectId: 'project-1',
      LoadState: 'unloaded',
      Mode: {
        BoundTaskCardId: 'task-1',
        ActiveWorkflowMapCardId: 'map-1',
        MemoryMounted: true,
      },
      Runtime: runtime({ State: 'paused' }),
      CanDelete: true,
    })
    expect(info.ActorId).toBe('019f30e43afd00000000000000000004')
    expect(info.LoadState).toBe('unloaded')
    expect(info.Status).toBe('paused')
    expect(info.BoundTaskCardId).toBe('task-1')
    expect(info.ActiveWorkflowMapCardId).toBe('map-1')
    expect(info.MemoryMounted).toBe(true)
  })

  it('maps worktree fields from runtime', () => {
    const info = buildAgentInfo({
      Id: 'agent-wt',
      ActorId: 'actor-wt',
      DisplayName: 'Worktree Agent',
      AgentKind: 'coder',
      ProjectId: 'project-1',
      Runtime: runtime({ State: 'running', WorktreeID: 'wt-abc', WorktreeName: 'agent-wt-scratch', WorktreeStatus: 'bound' }),
      CanDelete: true,
    })
    expect(info.WorktreeID).toBe('wt-abc')
    expect(info.WorktreeName).toBe('agent-wt-scratch')
    expect(info.WorktreeStatus).toBe('bound')
  })

  it('falls back to empty worktree fields when runtime has none', () => {
    const info = buildAgentInfo({
      Id: 'agent-plain',
      ActorId: 'actor-plain',
      DisplayName: 'Plain Agent',
      AgentKind: 'coder',
      ProjectId: 'project-1',
      Runtime: runtime({ State: 'idle' }),
      CanDelete: true,
    })
    expect(info.WorktreeID).toBe('')
    expect(info.WorktreeName).toBe('')
    expect(info.WorktreeStatus).toBe('')
  })
})

function baseItem(): AgentListItem {
  return {
    Id: 'id-1',
    ActorId: 'actor-1',
    DisplayName: 'Test Agent',
    Title: 'Test Title',
    AgentKind: 'coder',
    ProjectId: 'project-1',
    AggregatorActorId: 'agg-1',
    CanDelete: true,
  } as AgentListItem
}

describe('computeAgentStatus', () => {
  it('returns idle for missing runtime', () => {
    expect(computeAgentStatus(undefined)).toBe('idle')
  })

  it('maps running state', () => {
    expect(computeAgentStatus(runtime({ State: 'running' }))).toBe('running')
  })

  it('maps paused state', () => {
    expect(computeAgentStatus(runtime({ State: 'paused' }))).toBe('paused')
  })

  it('maps completed state', () => {
    expect(computeAgentStatus(runtime({ State: 'completed' }))).toBe('completed')
  })

  it('maps waiting state (workflow owner parked between turns)', () => {
    expect(computeAgentStatus(runtime({ State: 'waiting' }))).toBe('waiting')
  })

  it('maps failed state to error', () => {
    expect(computeAgentStatus(runtime({ State: 'failed' }))).toBe('error')
  })

  it('prioritizes plan approval over ask user and error', () => {
    expect(computeAgentStatus(runtime({ State: 'failed', AskUserPending: true, PlanApprovalPending: true }))).toBe('plan_approval')
  })

  it('prioritizes ask user over running', () => {
    expect(computeAgentStatus(runtime({ State: 'running', AskUserPending: true }))).toBe('ask_user')
  })

  it('prioritizes approval pending over error', () => {
    expect(computeAgentStatus(runtime({ State: 'failed', ApprovalPending: true }))).toBe('ask_permission')
  })

  it('maps Error string to error', () => {
    expect(computeAgentStatus(runtime({ State: 'idle', Error: 'boom' }))).toBe('error')
  })
})

describe('AGENT_STATUS_LABELS', () => {
  it('has a label for every status', () => {
    expect(AGENT_STATUS_LABELS['idle']).toBe('idle')
    expect(AGENT_STATUS_LABELS['waiting']).toBe('waiting')
    expect(AGENT_STATUS_LABELS['running']).toBe('running')
    expect(AGENT_STATUS_LABELS['paused']).toBe('paused')
    expect(AGENT_STATUS_LABELS['error']).toBe('error')
    expect(AGENT_STATUS_LABELS['completed']).toBe('completed')
    expect(AGENT_STATUS_LABELS['ask_user']).toBe('ask user')
    expect(AGENT_STATUS_LABELS['ask_permission']).toBe('ask permission')
    expect(AGENT_STATUS_LABELS['plan_approval']).toBe('plan approval')
  })
})

describe('aggregateProjectStatus', () => {
  function agent(projectId: string, over: Partial<AgentInfo> = {}): AgentInfo {
    return {
      Id: 'a', ActorId: 'a', DisplayName: 'A', Title: 'A', HasTitle: false,
      AgentKind: 'coder', ProjectId: projectId, ProjectName: projectId,
      AggregatorActorId: 'agg', Status: 'idle', StatusLabel: 'idle',
      IsWorking: false, IsError: false, IsCompleted: false,
      IsAskUserPermission: false, IsAskUser: false, IsAskPermission: false,
      IsPlanApproval: false, CompactionPolicyLoading: false, CanDelete: true,
      ...over,
    } as AgentInfo
  }

  it('omits projects with no working or errored agents', () => {
    const map = aggregateProjectStatus([agent('p1')])
    expect(map.has('p1')).toBe(false)
  })

  it('marks a project working when any agent is working', () => {
    const map = aggregateProjectStatus([
      agent('p1', { IsWorking: true, StatusLabel: 'running' }),
      agent('p1', { IsWorking: false, StatusLabel: 'idle' }),
      agent('p2', { IsWorking: true, StatusLabel: 'running' }),
    ])
    expect(map.get('p1')?.isWorking).toBe(true)
    expect(map.get('p2')?.isWorking).toBe(true)
    expect(map.get('p1')?.statusLabel).toBe('1 running')
  })

  it('counts working agents in the label', () => {
    const map = aggregateProjectStatus([
      agent('p1', { IsWorking: true }),
      agent('p1', { IsWorking: true }),
    ])
    expect(map.get('p1')?.statusLabel).toBe('2 running')
  })

  it('mentions errored count alongside running when both present', () => {
    const map = aggregateProjectStatus([
      agent('p1', { IsWorking: true }),
      agent('p1', { IsError: true }),
    ])
    const st = map.get('p1')
    expect(st?.isWorking).toBe(true)
    expect(st?.isError).toBe(true)
    expect(st?.statusLabel).toBe('1 running, 1 errored')
  })

  it('falls back to errored label when no agent is working', () => {
    const map = aggregateProjectStatus([
      agent('p1', { IsError: true, StatusLabel: 'error' }),
    ])
    expect(map.get('p1')?.isWorking).toBe(false)
    expect(map.get('p1')?.isError).toBe(true)
    expect(map.get('p1')?.statusLabel).toBe('1 errored')
  })
})

describe('buildAgentInfo via public store helpers', () => {
  beforeEach(() => {
    mockAgentListSnapshot.items = []
  })

  it('exposes derived flags from runtime', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    mockAgentListSnapshot.items = [
      { ...baseItem(), Runtime: runtime({ State: 'running' }) },
    ]
    const off = subscribeAgentInfoStore(() => {})
    const snapshot = getAgentInfoSnapshot()
    off()
    const agent = snapshot.items[0] as AgentInfo
    expect(agent.Status).toBe('running')
    expect(agent.IsWorking).toBe(true)
    expect(agent.IsError).toBe(false)
    expect(agent.IsCompleted).toBe(false)
    expect(agent.IsAskUser).toBe(false)
    expect(agent.IsAskPermission).toBe(false)
    expect(agent.IsPlanApproval).toBe(false)
  })

  it('exposes workflow map and bound task card ids from runtime', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    mockAgentListSnapshot.items = [
      {
        ...baseItem(),
        Runtime: runtime({
          State: 'running',
          ActiveWorkflowMapCardId: 'map-card-1',
          BoundTaskCardId: 'task-card-1',
        }),
      },
    ]
    const off = subscribeAgentInfoStore(() => {})
    const snapshot = getAgentInfoSnapshot()
    off()
    const agent = snapshot.items[0] as AgentInfo
    expect(agent.ActiveWorkflowMapCardId).toBe('map-card-1')
    expect(agent.BoundTaskCardId).toBe('task-card-1')
  })

  it('sets ask user, ask permission and plan approval flags independently', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    mockAgentListSnapshot.items = [
      { ...baseItem(), Runtime: runtime({ State: 'running', AskUserPending: true }) },
      { ...baseItem(), Id: 'id-2', ActorId: 'actor-2', Runtime: runtime({ State: 'running', ApprovalPending: true }) },
      { ...baseItem(), Id: 'id-3', ActorId: 'actor-3', Runtime: runtime({ State: 'running', PlanApprovalPending: true }) },
    ]
    const off = subscribeAgentInfoStore(() => {})
    const snapshot = getAgentInfoSnapshot()
    off()
    const [askUserAgent, askPermissionAgent, planApprovalAgent] = snapshot.items as AgentInfo[]
    if (!askUserAgent || !askPermissionAgent || !planApprovalAgent) throw new Error('expected all agents')
    expect(askUserAgent.Status).toBe('ask_user')
    expect(askUserAgent.IsAskUser).toBe(true)
    expect(askUserAgent.IsAskPermission).toBe(false)
    expect(askUserAgent.IsPlanApproval).toBe(false)
    expect(askPermissionAgent.Status).toBe('ask_permission')
    expect(askPermissionAgent.IsAskUser).toBe(false)
    expect(askPermissionAgent.IsAskPermission).toBe(true)
    expect(askPermissionAgent.IsPlanApproval).toBe(false)
    expect(planApprovalAgent.Status).toBe('plan_approval')
    expect(planApprovalAgent.IsAskUser).toBe(false)
    expect(planApprovalAgent.IsAskPermission).toBe(false)
    expect(planApprovalAgent.IsPlanApproval).toBe(true)
  })

  it('falls back to DisplayName when Title is empty', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    mockAgentListSnapshot.items = [
      { ...baseItem(), Title: '', DisplayName: 'Fallback' },
    ]
    const off = subscribeAgentInfoStore(() => {})
    const snapshot = getAgentInfoSnapshot()
    off()
    const agent = snapshot.items[0] as AgentInfo
    expect(agent.Title).toBe('Fallback')
  })

  it('propagates CanDelete from AgentListItem', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    mockAgentListSnapshot.items = [
      { ...baseItem(), CanDelete: true },
      { ...baseItem(), Id: 'id-2', ActorId: 'actor-2', CanDelete: false },
    ]
    const off = subscribeAgentInfoStore(() => {})
    const snapshot = getAgentInfoSnapshot()
    off()
    const [deletable, nonDeletable] = snapshot.items as AgentInfo[]
    expect(deletable?.CanDelete).toBe(true)
    expect(nonDeletable?.CanDelete).toBe(false)
  })

  it('defaults CanDelete to true when backend omits the field', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    const itemWithoutCanDelete = { ...baseItem() } as unknown as AgentListItem
    delete (itemWithoutCanDelete as { CanDelete?: boolean }).CanDelete
    mockAgentListSnapshot.items = [itemWithoutCanDelete]
    const off = subscribeAgentInfoStore(() => {})
    const snapshot = getAgentInfoSnapshot()
    off()
    const agent = snapshot.items[0] as AgentInfo
    expect(agent.CanDelete).toBe(true)
  })

  it('propagates Summary slot from AgentListItem', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    const summarySlot = { Candidates: [{ kind: 'unit', Unit: { model: 'sum-model', provider: 'sum-provider' } }] }
    mockAgentListSnapshot.items = [
      { ...baseItem(), Summary: summarySlot } as AgentListItem,
    ]
    const off = subscribeAgentInfoStore(() => {})
    const snapshot = getAgentInfoSnapshot()
    off()
    const agent = snapshot.items[0] as AgentInfo
    expect(agent.Summary).toEqual(summarySlot)
  })

  it('propagates all model slots from AgentListItem', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    const primarySlot = { Candidates: [{ kind: 'unit', Unit: { model: 'p', provider: 'pv' } }] }
    const fastSlot = { Candidates: [{ kind: 'unit', Unit: { model: 'f', provider: 'fv' } }] }
    const execSlot = { Candidates: [{ kind: 'unit', Unit: { model: 'e', provider: 'ev' } }] }
    const reviewSlot = { Candidates: [{ kind: 'unit', Unit: { model: 'r', provider: 'rv' } }] }
    const summarySlot = { Candidates: [{ kind: 'unit', Unit: { model: 's', provider: 'sv' } }] }
    mockAgentListSnapshot.items = [
      { ...baseItem(), Primary: primarySlot, Fast: fastSlot, Execution: execSlot, Review: reviewSlot, Summary: summarySlot } as AgentListItem,
    ]
    const off = subscribeAgentInfoStore(() => {})
    const snapshot = getAgentInfoSnapshot()
    off()
    const agent = snapshot.items[0] as AgentInfo
    expect(agent.Primary).toEqual(primarySlot)
    expect(agent.Fast).toEqual(fastSlot)
    expect(agent.Execution).toEqual(execSlot)
    expect(agent.Review).toEqual(reviewSlot)
    expect(agent.Summary).toEqual(summarySlot)
  })
})

describe('loadAgentCompactionPolicy', () => {
  it('no-ops when actorId is already loading', async () => {
    const agentCompaction = await import('../../../gen-clients/local/client')
    const policy: CompactionPolicy = {
      Enabled: true,
      Strategy: 'recent',
      TriggerKind: 'turns',
      BudgetMode: 'token',
      TokenBudget: 1000,
      RecentWindow: 10,
      SummaryUnit: { model: '', provider: '' },
      MaxSummaryTokens: 0,
    }
    vi.mocked(agentCompaction.compactionConfigure).mockResolvedValueOnce({ CompactionPolicy: policy, Source: 'instance', SummarySegments: [] })

    await loadAgentCompactionPolicy('actor-1')
    expect(agentCompaction.compactionConfigure).toHaveBeenCalledTimes(1)
  })
})

describe('agentStatusClass', () => {
  it('maps every status to a distinct CSS modifier', () => {
    expect(agentStatusClass('error')).toBe('error')
    expect(agentStatusClass('plan_approval')).toBe('plan-approval')
    expect(agentStatusClass('ask_user')).toBe('ask-user')
    expect(agentStatusClass('ask_permission')).toBe('ask-permission')
    expect(agentStatusClass('paused')).toBe('paused')
    expect(agentStatusClass('running')).toBe('working')
    expect(agentStatusClass('waiting')).toBe('waiting')
    expect(agentStatusClass('completed')).toBe('completed')
    expect(agentStatusClass('idle')).toBe('idle')
  })
})

describe('recompute snapshot identity (event churn)', () => {
  // Simulates a backend event: the items array is replaced wholesale with
  // fresh JSON-decoded objects. Content-equal churn must keep the previous
  // snapshot AND item identities so every useSyncExternalStore subscriber
  // (one CardAgentAvatar per workflow node, three mounted graph panes) skips
  // re-rendering.
  function recomputeCycle(items: AgentListItem[]) {
    mockAgentListSnapshot.items = items
    // Unsubscribe + subscribe stops and restarts the store, which recomputes
    // from the mocked snapshot — the same work an agentListStore event does.
    const off = subscribeAgentInfoStoreRef(() => {})
    off()
  }

  let subscribeAgentInfoStoreRef: typeof import('./agentInfoStore').subscribeAgentInfoStore
  let getAgentInfoSnapshotRef: typeof import('./agentInfoStore').getAgentInfoSnapshot

  beforeEach(async () => {
    ;({ subscribeAgentInfoStore: subscribeAgentInfoStoreRef, getAgentInfoSnapshot: getAgentInfoSnapshotRef } = await import('./agentInfoStore'))
    mockAgentListSnapshot.version = 1
  })

  it('keeps the snapshot reference when an event rebuilds identical content', () => {
    recomputeCycle([
      { ...baseItem(), Runtime: runtime({ State: 'running' }) },
      { ...baseItem(), Id: 'id-2', ActorId: 'actor-2', DisplayName: 'Second Agent' },
    ])
    const before = getAgentInfoSnapshotRef()
    const itemBefore = before.items[0]

    recomputeCycle(JSON.parse(JSON.stringify([
      { ...baseItem(), Runtime: runtime({ State: 'running' }) },
      { ...baseItem(), Id: 'id-2', ActorId: 'actor-2', DisplayName: 'Second Agent' },
    ])) as AgentListItem[])

    const after = getAgentInfoSnapshotRef()
    expect(after).toBe(before)
    expect(after.items[0]).toBe(itemBefore)
  })

  it('produces a new snapshot when one agent actually changed', () => {
    recomputeCycle([
      { ...baseItem(), Runtime: runtime({ State: 'idle' }) },
      { ...baseItem(), Id: 'id-2', ActorId: 'actor-2', DisplayName: 'Second Agent' },
    ])
    const before = getAgentInfoSnapshotRef()
    const untouchedBefore = before.items[1]

    recomputeCycle([
      { ...baseItem(), Runtime: runtime({ State: 'running' }) },
      { ...baseItem(), Id: 'id-2', ActorId: 'actor-2', DisplayName: 'Second Agent' },
    ])

    const after = getAgentInfoSnapshotRef()
    expect(after).not.toBe(before)
    expect(after.items[0]).not.toBe(before.items[0])
    expect(after.items[0]!.Status).toBe('running')
    // The unchanged agent keeps its identity: its subscribers skip re-render.
    expect(after.items[1]!).toBe(untouchedBefore)
  })

  it('produces a new snapshot when the order changes', () => {
    const first = { ...baseItem(), Runtime: runtime({ State: 'idle' }) }
    const second = { ...baseItem(), Id: 'id-2', ActorId: 'actor-2', DisplayName: 'Second Agent' }
    recomputeCycle([first, second])
    const before = getAgentInfoSnapshotRef()

    recomputeCycle(JSON.parse(JSON.stringify([second, first])) as AgentListItem[])

    const after = getAgentInfoSnapshotRef()
    expect(after).not.toBe(before)
    expect(after.items[0]!.Id).toBe('id-2')
    expect(after.items[1]!.Id).toBe('id-1')
    // Same content, reused identities — only the array order is new.
    expect(after.items[0]!).toBe(before.items[1]!)
    expect(after.items[1]!).toBe(before.items[0]!)
  })
})

describe('patchAgentActorId', () => {
  beforeEach(() => {
    mockAgentListSnapshot.items = [{ ...baseItem(), ActorId: 'actor-1' }]
  })

  it('updates ActorId and produces a new snapshot reference so subscribers re-render', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    const off = subscribeAgentInfoStore(() => {})
    const before = getAgentInfoSnapshot()
    const agentBefore = before.byId.get('id-1')
    if (!agentBefore) throw new Error('agent missing before patch')

    patchAgentActorId('id-1', 'actor-1-respawned')

    const after = getAgentInfoSnapshot()
    off()

    // New snapshot object reference — useSyncExternalStore (Object.is) must see
    // a change. Before the fix the snapshot reference was reused, so consumers
    // skipped re-rendering.
    expect(after).not.toBe(before)
    // Old actorId entry removed, new one present, same Id resolves.
    expect(after.byActorId.has('actor-1')).toBe(false)
    expect(after.byActorId.get('actor-1-respawned')?.Id).toBe('id-1')
    expect(after.byId.get('id-1')?.ActorId).toBe('actor-1-respawned')
    // A brand-new AgentInfo object (not the mutated original).
    expect(after.byId.get('id-1')).not.toBe(agentBefore)
  })

  it('no-ops when the actor id is unchanged', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    const off = subscribeAgentInfoStore(() => {})
    const before = getAgentInfoSnapshot()

    patchAgentActorId('id-1', 'actor-1')

    expect(getAgentInfoSnapshot()).toBe(before)
    off()
  })

  it('no-ops when the agent id is unknown', async () => {
    const { getAgentInfoSnapshot, subscribeAgentInfoStore } = await import('./agentInfoStore')
    const off = subscribeAgentInfoStore(() => {})
    const before = getAgentInfoSnapshot()

    patchAgentActorId('unknown-id', 'whatever')

    expect(getAgentInfoSnapshot()).toBe(before)
    off()
  })
})
