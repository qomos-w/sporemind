import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fetchRouteGroups } from './useAIShellProviders'
import type {
  AICallableUnitView,
  AggregatorDescriptorListResp,
  AIAggregatorStatusResp,
  AIManagerUnitHealthListResp,
} from '../../../gen-clients/system/types'

vi.mock('../../../application/generated-client', () => ({ client: {} }))

const aggregatorListMock = vi.fn()
const unitHealthListMock = vi.fn()
vi.mock('../../../gen-clients/aimanager/client', () => ({
  aggregatorList: (..._args: unknown[]) => aggregatorListMock(..._args),
  unitHealthList: (..._args: unknown[]) => unitHealthListMock(..._args),
}))

const statusMock = vi.fn()
vi.mock('../../../gen-clients/aiaggregator/client', () => ({
  status: (..._args: unknown[]) => statusMock(..._args),
}))

// Avoid the local/client (thinking registry) call path — not used by
// fetchRouteGroups, but the module import side-effects should not break.
vi.mock('../../../gen-clients/local/client', () => ({}))

function unit(opts: Partial<AICallableUnitView>): AICallableUnitView {
  return {
    Id: '',
    Model: '',
    Endpoint: '',
    ProviderName: '',
    Protocol: '',
    ...opts,
  }
}

function aggregatorListResp(items: { Id: string; Name: string; ActorId: string }[]): AggregatorDescriptorListResp {
  return { Items: items as any } as AggregatorDescriptorListResp
}

function statusResp(units: AICallableUnitView[]): AIAggregatorStatusResp {
  return {
    Id: '',
    Name: '',
    Version: 0,
    UnitCount: units.length,
    Units: units,
  } as AIAggregatorStatusResp
}

function healthResp(items: AICallableUnitView[]): AIManagerUnitHealthListResp {
  return { Items: items } as AIManagerUnitHealthListResp
}

beforeEach(() => {
  aggregatorListMock.mockReset()
  unitHealthListMock.mockReset()
  statusMock.mockReset()
})

describe('fetchRouteGroups — dual-source compositing', () => {
  it('overlays health from the global cooldown source, ignoring aggregator status health', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    // Status reports the unit as cooling_down; the global source is empty
    // (healthy). The composed option must be healthy — aggregator status is
    // no longer the cooldown source.
    statusMock.mockResolvedValue(statusResp([
      unit({
        Id: 'p::m',
        Model: 'm',
        ProviderName: 'p',
        HealthState: 'cooling_down',
        CooldownUntil: 9999,
      }),
    ]))

    const groups = await fetchRouteGroups()
    const auto = groups.find(g => g.isAuto)!
    expect(auto.models).toHaveLength(1)
    expect(auto.models[0]!.healthState).toBeUndefined()
    expect(auto.models[0]!.cooldownUntil).toBeUndefined()
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([
      unit({
        Id: 'p::m',
        Model: 'm',
        ProviderName: 'p',
        HealthState: 'cooling_down',
        HealthReason: 'rate_limit',
        CooldownUntil: 1234,
      }),
    ]))
    statusMock.mockResolvedValue(statusResp([
      unit({
        Id: 'p::m',
        Model: 'm',
        ProviderName: 'p',
        HealthState: 'healthy',
      }),
    ]))

    const groups2 = await fetchRouteGroups()
    const auto2 = groups2.find(g => g.isAuto)!
    expect(auto2.models[0]!.healthState).toBe('cooling_down')
    expect(auto2.models[0]!.healthReason).toBe('rate_limit')
    expect(auto2.models[0]!.cooldownUntil).toBe(1234)
  })

  it('keeps ref health from the aggregator status projection (aggregator-availability, not unit cooldown)', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
      { Id: 'agg-child', Name: 'Child', ActorId: 'child-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    // The parent (system) status surfaces a child-aggregator ref entry whose
    // projected HealthState is 'disabled'. Refs are aggregator-availability
    // signals, not unit cooldown, so they must keep the status projection.
    statusMock.mockImplementation((_client: unknown, opts?: { target?: string }) => {
      if (opts?.target === 'sys-actor') {
        return Promise.resolve(statusResp([
          unit({ aggregatorID: 'agg-child', Model: '', ProviderName: '', HealthState: 'disabled', HealthReason: 'authorization_failed' }),
        ]))
      }
      return Promise.resolve(statusResp([]))
    })

    const groups = await fetchRouteGroups()
    const system = groups.find(g => g.isAuto)!
    expect(system.refs).toHaveLength(1)
    expect(system.refs![0]!.aggregatorId).toBe('agg-child')
    expect(system.refs![0]!.healthState).toBe('disabled')
    expect(system.refs![0]!.healthReason).toBe('authorization_failed')
  })

  it('passes DispatchActivity through from the aggregator status unit when the agent matches', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    statusMock.mockResolvedValue(statusResp([
      unit({
        Id: 'p::m',
        Model: 'm',
        ProviderName: 'p',
        DispatchActivity: { State: 'in_use', SessionId: 's1', AgentId: 'a1', Depth: 0 },
      }),
    ]))

    const groups = await fetchRouteGroups('a1')
    const auto = groups.find(g => g.isAuto)!
    expect(auto.models[0]!.dispatchActivity?.State).toBe('in_use')
    expect(auto.models[0]!.dispatchActivity?.SessionId).toBe('s1')
  })

  it('filters out DispatchActivity belonging to a different agent', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    statusMock.mockResolvedValue(statusResp([
      unit({
        Id: 'p::m',
        Model: 'm',
        ProviderName: 'p',
        DispatchActivity: { State: 'in_use', SessionId: 's1', AgentId: 'other-agent', Depth: 0 },
      }),
    ]))

    const groups = await fetchRouteGroups('my-agent')
    const auto = groups.find(g => g.isAuto)!
    expect(auto.models[0]!.dispatchActivity).toBeUndefined()
  })

  it('filters out all DispatchActivity when no agent is selected', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    statusMock.mockResolvedValue(statusResp([
      unit({
        Id: 'p::m',
        Model: 'm',
        ProviderName: 'p',
        DispatchActivity: { State: 'in_use', SessionId: 's1', AgentId: 'a1', Depth: 0 },
      }),
    ]))

    const groups = await fetchRouteGroups()
    const auto = groups.find(g => g.isAuto)!
    expect(auto.models[0]!.dispatchActivity).toBeUndefined()
  })

  it('returns [] when both aggregator list and global health are empty', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    const groups = await fetchRouteGroups()
    expect(groups).toEqual([])
  })

  it('returns [] when no aggregators exist even if global cooling units are present', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([]))
    unitHealthListMock.mockResolvedValue(healthResp([
      unit({ Id: 'p::m', Model: 'm', ProviderName: 'p', HealthState: 'cooling_down', CooldownUntil: 50 }),
    ]))
    // Cooling units only overlay health on registered models — they never add
    // new entries, so with no aggregators the list is empty.
    const groups = await fetchRouteGroups()
    expect(groups).toEqual([])
  })

  it('skips on-demand units so cooling units do not leak into sibling aggregators', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
      { Id: 'agg-a', Name: 'A', ActorId: 'a-actor' },
      { Id: 'agg-b', Name: 'B', ActorId: 'b-actor' },
    ]))
    // A1 is cooling globally; it is registered in A but appears as an
    // on-demand entry in B's status (and would have appeared in A's too).
    unitHealthListMock.mockResolvedValue(healthResp([
      unit({ Id: 'p::a1', Model: 'a1', ProviderName: 'p', HealthState: 'cooling_down', CooldownUntil: 60 }),
    ]))
    statusMock.mockImplementation((_client: unknown, opts?: { target?: string }) => {
      if (opts?.target === 'a-actor') {
        return Promise.resolve(statusResp([
          unit({ Id: 'p::a1', Model: 'a1', ProviderName: 'p' }),
        ]))
      }
      if (opts?.target === 'b-actor') {
        return Promise.resolve(statusResp([
          unit({ Id: 'p::b1', Model: 'b1', ProviderName: 'p' }),
          // On-demand injection of the cooling A1 into B's status.
          unit({ Id: 'p::a1', Model: 'a1', ProviderName: 'p', OnDemand: true, HealthState: 'cooling_down', CooldownUntil: 60 }),
        ]))
      }
      return Promise.resolve(statusResp([]))
    })

    const groups = await fetchRouteGroups()
    const a = groups.find(g => g.routeId === 'agg-a')!
    const b = groups.find(g => g.routeId === 'agg-b')!
    // A1 appears only in A (its registrant), with cooling overlay.
    expect(a.models).toHaveLength(1)
    expect(a.models[0]!.id).toBe('p::a1')
    expect(a.models[0]!.healthState).toBe('cooling_down')
    // B must not show A1 — the on-demand entry is skipped.
    expect(b.models).toHaveLength(1)
    expect(b.models[0]!.id).toBe('p::b1')
    expect(b.models.find(m => m.id === 'p::a1')).toBeUndefined()
  })

  it('deduplicates duplicate (provider, model) units across aggregators preferring the unhealthy one', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
      { Id: 'custom', Name: 'Custom', ActorId: 'custom-actor' },
    ]))
    // The unit is cooling globally, so both status views overlay to cooling.
    unitHealthListMock.mockResolvedValue(healthResp([
      unit({ Id: 'p::m', Model: 'm', ProviderName: 'p', HealthState: 'cooling_down', CooldownUntil: 7 }),
    ]))
    statusMock.mockImplementation((_client: unknown, opts?: { target?: string }) => {
      if (opts?.target === 'sys-actor') {
        return Promise.resolve(statusResp([unit({ Id: 'p::m', Model: 'm', ProviderName: 'p' })]))
      }
      return Promise.resolve(statusResp([unit({ Id: 'p::m', Model: 'm', ProviderName: 'p' })]))
    })

    const groups = await fetchRouteGroups()
    const auto = groups.find(g => g.isAuto)!
    const custom = groups.find(g => !g.isAuto)!
    // Each group keeps its own copy — dedup is within a single partition.
    expect(auto.models).toHaveLength(1)
    expect(custom.models).toHaveLength(1)
    expect(auto.models[0]!.healthState).toBe('cooling_down')
    expect(custom.models[0]!.healthState).toBe('cooling_down')
  })

  it('captures DispatchActivity on ref entries when the agent matches', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
      { Id: 'agg-child', Name: 'Child', ActorId: 'child-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    statusMock.mockImplementation((_client: unknown, opts?: { target?: string }) => {
      if (opts?.target === 'sys-actor') {
        return Promise.resolve(statusResp([
          unit({
            aggregatorID: 'agg-child',
            Model: '',
            ProviderName: '',
            DispatchActivity: { State: 'in_use', SessionId: 's1', AgentId: 'a1', Depth: 1, AggregatorId: 'agg-child' },
          }),
        ]))
      }
      return Promise.resolve(statusResp([]))
    })

    const groups = await fetchRouteGroups('a1')
    const system = groups.find(g => g.isAuto)!
    expect(system.refs![0]!.dispatchActivity?.State).toBe('in_use')
    expect(system.refs![0]!.dispatchActivity?.AggregatorId).toBe('agg-child')
  })

  it('filters out DispatchActivity on ref entries when the agent does not match', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
      { Id: 'agg-child', Name: 'Child', ActorId: 'child-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    statusMock.mockImplementation((_client: unknown, opts?: { target?: string }) => {
      if (opts?.target === 'sys-actor') {
        return Promise.resolve(statusResp([
          unit({ aggregatorID: 'agg-child', DispatchActivity: { State: 'in_use', AgentId: 'other-agent', Depth: 1 } }),
        ]))
      }
      return Promise.resolve(statusResp([]))
    })

    const groups = await fetchRouteGroups('a1')
    const system = groups.find(g => g.isAuto)!
    expect(system.refs![0]!.dispatchActivity).toBeUndefined()
  })

  it('keeps custom aggregators referenced only by the system pool as roots', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
      { Id: 'pool-child', Name: 'Pool Child', ActorId: 'pool-child-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    statusMock.mockImplementation((_client: unknown, opts?: { target?: string }) => {
      if (opts?.target === 'sys-actor') {
        // The system pool references a custom aggregator, but the pool has no
        // parent UI of its own — the child must stay a top-level route.
        return Promise.resolve(statusResp([
          unit({ aggregatorID: 'pool-child', Model: '', ProviderName: '' }),
        ]))
      }
      return Promise.resolve(statusResp([]))
    })

    const groups = await fetchRouteGroups()
    const poolChild = groups.find(g => g.routeId === 'pool-child')!
    expect(poolChild.isRoot).toBe(true)
  })

  it('keeps nested aggregators as root and bubbles unit activity up to the parent route', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
      { Id: 'parent', Name: 'Parent', ActorId: 'parent-actor' },
      { Id: 'child', Name: 'Child', ActorId: 'child-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    statusMock.mockImplementation((_client: unknown, opts?: { target?: string }) => {
      switch (opts?.target) {
        case 'parent-actor':
          // Parent surfaces a ref to the child whose deepest activity is `trying`.
          return Promise.resolve(statusResp([
            unit({
              aggregatorID: 'child',
              Model: '',
              ProviderName: '',
              DispatchActivity: { State: 'trying', AgentId: 'a1', Depth: 2, AggregatorId: 'child' },
            }),
            unit({ Id: 'p::m1', Model: 'm1', ProviderName: 'p' }),
          ]))
        case 'child-actor':
          // Child's own model is in_use — the more important state, so it must
          // win when bubbled up to the parent route.
          return Promise.resolve(statusResp([
            unit({
              Id: 'p::m2',
              Model: 'm2',
              ProviderName: 'p',
              DispatchActivity: { State: 'in_use', AgentId: 'a1', Depth: 0 },
            }),
          ]))
        default:
          return Promise.resolve(statusResp([]))
      }
    })

    const groups = await fetchRouteGroups('a1')
    const parent = groups.find(g => g.routeId === 'parent')!
    const child = groups.find(g => g.routeId === 'child')!
    expect(child.isRoot).toBe(true)
    expect(parent.isRoot).toBe(true)
    expect(parent.refs![0]!.dispatchActivity?.State).toBe('trying')
    // in_use (child model) outranks trying (parent ref projection).
    expect(parent.bubbledActivity?.State).toBe('in_use')
  })

  it('does not surface child activity on its top-level row when the parent ref row already carries it', async () => {
    aggregatorListMock.mockResolvedValue(aggregatorListResp([
      { Id: 'system', Name: 'Auto', ActorId: 'sys-actor' },
      { Id: 'parent', Name: 'Parent', ActorId: 'parent-actor' },
      { Id: 'child', Name: 'Child', ActorId: 'child-actor' },
    ]))
    unitHealthListMock.mockResolvedValue(healthResp([]))
    statusMock.mockImplementation((_client: unknown, opts?: { target?: string }) => {
      switch (opts?.target) {
        case 'parent-actor':
          // Parent's ref row surfaces the child's in-flight state (A/B in use).
          return Promise.resolve(statusResp([
            unit({
              aggregatorID: 'child',
              Model: '',
              ProviderName: '',
              DispatchActivity: { State: 'in_use', AgentId: 'a1', SessionId: 's1', Depth: 1, AggregatorId: 'child' },
            }),
          ]))
        case 'child-actor':
          // The same dispatch (session/agent/state) is reported on the child's
          // own model — it must not leak onto the child's top-level row.
          return Promise.resolve(statusResp([
            unit({
              Id: 'p::m2',
              Model: 'm2',
              ProviderName: 'p',
              DispatchActivity: { State: 'in_use', AgentId: 'a1', SessionId: 's1', Depth: 0 },
            }),
          ]))
        default:
          return Promise.resolve(statusResp([]))
      }
    })

    const groups = await fetchRouteGroups('a1')
    const parent = groups.find(g => g.routeId === 'parent')!
    const child = groups.find(g => g.routeId === 'child')!
    // Parent context carries the activity via its ref row…
    expect(parent.refs![0]!.dispatchActivity?.State).toBe('in_use')
    expect(parent.bubbledActivity?.State).toBe('in_use')
    // …and the child's sibling top-level row does not duplicate it.
    expect(child.bubbledActivity).toBeUndefined()
  })
})
