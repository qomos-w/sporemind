import { describe, expect, it, vi, beforeEach } from 'vitest'
import type { AgentRef } from '../../gen-clients/system/types'

const hoisted = vi.hoisted(() => ({
  agents: [] as AgentRef[],
  cards: [] as { id: string; type: string; data?: Record<string, unknown> }[],
}))

vi.mock('./hooks/agentListStore', () => ({
  getAgentListItems: () => hoisted.agents,
}))

// The worktree jump is stubbed so this test stays free of the git-store /
// backend import chain.
const worktreeJumpHoisted = vi.hoisted(() => ({ jump: vi.fn() }))
vi.mock('./hooks/worktreeGitJump', () => ({
  jumpToWorktreeGit: worktreeJumpHoisted.jump,
}))
vi.mock('../panels/mono-store', () => ({
  monoStore: { getState: () => ({ cards: hoisted.cards }) },
}))

// Importing registers the builtin mode click actions as a side effect.
import './registerModeClickActions'
import { runModeClickAction } from './modeClickRegistry'
import { findAgentWorkflowMapId, findAgentWorktreeId, findAgentSchedulerCardId } from './registerModeClickActions'
import { consumePendingWorkflowLocate } from './components/workflowLocateStore'
import { consumePendingScheduledLocate } from './components/scheduledLocateStore'

function agent(actorId: string, activeWorkflowMapCardId?: string): AgentRef {
  return {
    Id: `id-${actorId}`,
    ActorId: actorId,
    ProjectId: 'proj-1',
    DisplayName: actorId,
    AgentKind: 'coder',
    Mode: activeWorkflowMapCardId ? { ActiveWorkflowMapCardId: activeWorkflowMapCardId } : undefined,
  } as AgentRef
}

function worktreeAgent(actorId: string, worktreeId?: string): AgentRef {
  return {
    ...agent(actorId),
    Runtime: worktreeId ? { WorktreeID: worktreeId } : undefined,
  } as AgentRef
}

describe('registerModeClickActions workflow badge', () => {
  beforeEach(() => {
    hoisted.agents = []
    consumePendingWorkflowLocate()
  })

  it('findAgentWorkflowMapId resolves the agent\'s own active workflow map', () => {
    hoisted.agents = [agent('a1', 'MapA'), agent('a2', 'MapB')]
    expect(findAgentWorkflowMapId('a1')).toBe('MapA')
    expect(findAgentWorkflowMapId('a2')).toBe('MapB')
  })

  it('findAgentWorkflowMapId returns undefined for unknown or unbound agents', () => {
    hoisted.agents = [agent('a1', 'MapA'), agent('a2')]
    expect(findAgentWorkflowMapId('a2')).toBeUndefined()
    expect(findAgentWorkflowMapId('nobody')).toBeUndefined()
    expect(findAgentWorkflowMapId(undefined)).toBeUndefined()
  })

  it('findAgentWorkflowMapId falls back to Runtime.ActiveWorkflowMapCardId when Mode is unset', () => {
    hoisted.agents = [{ ...agent('a1'), Runtime: { ActiveWorkflowMapCardId: 'RtMap' } } as AgentRef]
    expect(findAgentWorkflowMapId('a1')).toBe('RtMap')
  })

  it('findAgentWorkflowMapId prefers Mode over Runtime when both are set', () => {
    hoisted.agents = [{ ...agent('a1', 'ModeMap'), Runtime: { ActiveWorkflowMapCardId: 'RtMap' } } as AgentRef]
    expect(findAgentWorkflowMapId('a1')).toBe('ModeMap')
  })

  it('clicking the badge locates the composer agent\'s bound workflow, not another agent\'s', () => {
    hoisted.agents = [agent('other', 'OtherMap'), agent('composer', 'ComposerMap')]
    const modeEvents: string[] = []
    const handler = (e: Event) => modeEvents.push((e as CustomEvent).detail)
    window.addEventListener('sporemind:set-shell-content-mode', handler)
    try {
      expect(runModeClickAction('builtin:mode:workflow', { agentActorId: 'composer' })).toBe(true)
      expect(modeEvents).toEqual(['workflow'])
      expect(consumePendingWorkflowLocate()).toEqual({ mapId: 'ComposerMap', selectStart: true })
    } finally {
      window.removeEventListener('sporemind:set-shell-content-mode', handler)
    }
  })

  it('clicking the badge on an unbound agent opens the view without a locate', () => {
    hoisted.agents = [agent('other', 'OtherMap'), agent('composer')]
    const handler = vi.fn()
    window.addEventListener('sporemind:set-shell-content-mode', handler)
    try {
      expect(runModeClickAction('builtin:mode:workflow', { agentActorId: 'composer' })).toBe(true)
      expect(handler).toHaveBeenCalledTimes(1)
      expect(consumePendingWorkflowLocate()).toBeNull()
    } finally {
      window.removeEventListener('sporemind:set-shell-content-mode', handler)
    }
  })

  it('clicking without a composer agent context opens the view without a locate', () => {
    hoisted.agents = [agent('other', 'OtherMap')]
    expect(runModeClickAction('builtin:mode:workflow')).toBe(true)
    expect(consumePendingWorkflowLocate()).toBeNull()
  })
})

describe('registerModeClickActions worktree badge', () => {
  beforeEach(() => {
    hoisted.agents = []
    vi.clearAllMocks()
  })

  it('findAgentWorktreeId resolves the agent\'s bound worktree', () => {
    hoisted.agents = [worktreeAgent('a1', 'wt-1'), worktreeAgent('a2')]
    expect(findAgentWorktreeId('a1')).toBe('wt-1')
    expect(findAgentWorktreeId('a2')).toBeUndefined()
    expect(findAgentWorktreeId('nobody')).toBeUndefined()
    expect(findAgentWorktreeId(undefined)).toBeUndefined()
  })

  it('clicking the badge jumps to git mode on the composer agent\'s worktree', () => {
    hoisted.agents = [worktreeAgent('other', 'wt-other'), worktreeAgent('composer', 'wt-mine')]
    expect(runModeClickAction('builtin:mode:worktree', { agentActorId: 'composer' })).toBe(true)
    expect(worktreeJumpHoisted.jump).toHaveBeenCalledWith('proj-1', 'wt-mine')
  })

  it('clicking the badge on an agent without a worktree jumps to the project root context', () => {
    hoisted.agents = [worktreeAgent('composer')]
    expect(runModeClickAction('builtin:mode:worktree', { agentActorId: 'composer' })).toBe(true)
    expect(worktreeJumpHoisted.jump).toHaveBeenCalledWith('proj-1', undefined)
  })

  it('clicking without a resolvable agent does nothing', () => {
    expect(runModeClickAction('builtin:mode:worktree', { agentActorId: 'ghost' })).toBe(true)
    expect(worktreeJumpHoisted.jump).not.toHaveBeenCalled()
  })
})

describe('registerModeClickActions memory badge', () => {
  it('clicking the badge dispatches sporemind:open-brain with the agent ActorId', () => {
    const events: (string | undefined)[] = []
    const handler = (e: Event) => events.push((e as CustomEvent<string | undefined>).detail)
    window.addEventListener('sporemind:open-brain', handler)
    try {
      expect(runModeClickAction('builtin:mode:memory', { agentActorId: 'composer-1' })).toBe(true)
      expect(events).toEqual(['composer-1'])
    } finally {
      window.removeEventListener('sporemind:open-brain', handler)
    }
  })

  it('clicking without an agent context still dispatches the event', () => {
    const events: unknown[] = []
    const handler = (e: Event) => events.push((e as CustomEvent).detail)
    window.addEventListener('sporemind:open-brain', handler)
    try {
      expect(runModeClickAction('builtin:mode:memory')).toBe(true)
      expect(events).toHaveLength(1)
    } finally {
      window.removeEventListener('sporemind:open-brain', handler)
    }
  })
})

describe('registerModeClickActions goal badge', () => {
  it('clicking the badge dispatches sporemind:open-goal with the agent ActorId', () => {
    const events: (string | undefined)[] = []
    const handler = (e: Event) => events.push((e as CustomEvent<string | undefined>).detail)
    window.addEventListener('sporemind:open-goal', handler)
    try {
      expect(runModeClickAction('builtin:mode:goal', { agentActorId: 'composer-1' })).toBe(true)
      expect(events).toEqual(['composer-1'])
    } finally {
      window.removeEventListener('sporemind:open-goal', handler)
    }
  })
})

describe('registerModeClickActions scheduler badge', () => {
  function schedulerCard(id: string, boundAgent = ''): { id: string; type: string; data?: Record<string, unknown> } {
    const data: Record<string, unknown> = {}
    if (boundAgent) data.bound_agent = boundAgent
    return { id, type: 'scheduler', data }
  }

  beforeEach(() => {
    hoisted.agents = []
    hoisted.cards = []
    consumePendingScheduledLocate()
  })

  it('findAgentSchedulerCardId resolves the scheduler card bound to the agent by id', async () => {
    hoisted.agents = [agent('a1'), agent('a2')]
    hoisted.cards = [
      schedulerCard('sched:ForB', 'agent:id-a2'),
      schedulerCard('sched:ForA', 'id-a1'),
      { id: 'card:other', type: 'workflow', data: { bound_agent: 'id-a1' } },
    ]
    expect(await findAgentSchedulerCardId('a1')).toBe('sched:ForA')
    expect(await findAgentSchedulerCardId('a2')).toBe('sched:ForB')
  })

  it('findAgentSchedulerCardId returns undefined for unbound agents', async () => {
    hoisted.agents = [agent('a1')]
    hoisted.cards = [schedulerCard('sched:Ephemeral')]
    expect(await findAgentSchedulerCardId('a1')).toBeUndefined()
    expect(await findAgentSchedulerCardId('nobody')).toBeUndefined()
    expect(await findAgentSchedulerCardId(undefined)).toBeUndefined()
  })

  it('clicking the scheduler badge opens the scheduled view and locates the bound task', async () => {
    hoisted.agents = [agent('composer')]
    hoisted.cards = [schedulerCard('sched:TaskA', 'agent:id-composer')]
    const modeEvents: string[] = []
    const handler = (e: Event) => modeEvents.push((e as CustomEvent).detail)
    window.addEventListener('sporemind:set-shell-content-mode', handler)
    try {
      expect(runModeClickAction('builtin:mode:scheduler', { agentActorId: 'composer' })).toBe(true)
      await vi.waitFor(() => { expect(modeEvents).toEqual(['scheduled']) })
      expect(consumePendingScheduledLocate()).toEqual({ cardId: 'sched:TaskA' })
    } finally {
      window.removeEventListener('sporemind:set-shell-content-mode', handler)
    }
  })

  it('clicking the scheduler badge on an unbound agent opens the view without a locate', async () => {
    hoisted.agents = [agent('composer')]
    hoisted.cards = [schedulerCard('sched:Ephemeral')]
    const handler = vi.fn()
    window.addEventListener('sporemind:set-shell-content-mode', handler)
    try {
      runModeClickAction('builtin:mode:scheduler', { agentActorId: 'composer' })
      await vi.waitFor(() => { expect(handler).toHaveBeenCalledTimes(1) })
      expect(consumePendingScheduledLocate()).toBeNull()
    } finally {
      window.removeEventListener('sporemind:set-shell-content-mode', handler)
    }
  })
})
