import { describe, it, expect } from 'vitest'
import type { AgentInfo } from '../ai/hooks/agentInfoStore'
import { selectConsoleAgents, selectAutoArrangeAgents } from './consoleAgents'

function makeAgent(partial: Partial<AgentInfo>): AgentInfo {
  return {
    Id: 'agent-1',
    ActorId: 'actor-1',
    DisplayName: 'Agent',
    Title: 'Agent',
    HasTitle: false,
    AgentKind: 'coder',
    ProjectId: 'proj-1',
    ProjectName: 'Project',
    Status: 'idle',
    StatusLabel: 'idle',
    IsWorking: false,
    IsError: false,
    IsCompleted: false,
    IsAskUserPermission: false,
    IsAskUser: false,
    IsAskPermission: false,
    IsPlanApproval: false,
    IsGoalSubmit: false,
    CompactionPolicyLoading: false,
    CanDelete: true,
    ...partial,
  } as AgentInfo
}

const ids = (agents: AgentInfo[]) => agents.map(a => a.Id)

describe('selectConsoleAgents', () => {
  it('keeps regular agents and excludes standalone kanban-kind agents', () => {
    const agents = [
      makeAgent({ Id: 'coder', ActorId: 'A' }),
      makeAgent({ Id: 'worker-root', AgentKind: 'worker' }),
      makeAgent({ Id: 'reviewer-root', AgentKind: 'reviewer' }),
    ]
    expect(ids(selectConsoleAgents(agents))).toEqual(['coder'])
  })

  it('includes sub-agents regardless of kind', () => {
    const agents = [
      makeAgent({ Id: 'parent', ActorId: 'P' }),
      makeAgent({ Id: 'worker-child', AgentKind: 'worker', ParentAgentId: 'P' }),
      makeAgent({ Id: 'reviewer-child', AgentKind: 'reviewer', ParentAgentId: 'P' }),
      makeAgent({ Id: 'dreamer-child', AgentKind: 'dreamer', ParentAgentId: 'P' }),
    ]
    expect(ids(selectConsoleAgents(agents))).toEqual(['parent', 'worker-child', 'reviewer-child', 'dreamer-child'])
  })
})

describe('selectAutoArrangeAgents', () => {
  it('fills working sub-agents alongside their working parents', () => {
    const agents = [
      makeAgent({ Id: 'parent', ActorId: 'P', IsWorking: true }),
      makeAgent({ Id: 'worker-child', AgentKind: 'worker', ParentAgentId: 'P', IsWorking: true }),
      makeAgent({ Id: 'idle-coder', IsWorking: false }),
    ]
    expect(ids(selectAutoArrangeAgents(agents))).toEqual(['parent', 'worker-child'])
  })

  it('fills sub-agents active within the 10-minute window', () => {
    const now = Date.parse('2026-08-21T12:00:00Z')
    const agents = [
      makeAgent({ Id: 'parent', ActorId: 'P', IsWorking: true }),
      makeAgent({ Id: 'worker-child', AgentKind: 'worker', ParentAgentId: 'P', LastActivity: '2026-08-21T11:55:00Z' }),
      makeAgent({ Id: 'stale-worker', AgentKind: 'worker', ParentAgentId: 'P', LastActivity: '2026-08-21T10:00:00Z' }),
    ]
    expect(ids(selectAutoArrangeAgents(agents, now))).toEqual(['parent', 'worker-child'])
  })

  it('tops up with recently idle sessions up to the fill target', () => {
    const now = Date.parse('2026-08-21T12:00:00Z')
    const agents = [
      makeAgent({ Id: 'active', IsWorking: true }),
      makeAgent({ Id: 'recent-1', LastActivity: '2026-08-21T11:30:00Z' }),
      makeAgent({ Id: 'recent-2', LastActivity: '2026-08-21T11:45:00Z' }),
      makeAgent({ Id: 'old', LastActivity: '2026-08-21T09:00:00Z' }),
    ]
    expect(ids(selectAutoArrangeAgents(agents, now))).toEqual(['active', 'recent-2', 'recent-1'])
  })
})
