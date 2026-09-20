import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { buildAssistantTurnSummary, TurnTail } from './TurnTail'
import { AIShellContext } from '../context/AIShellContext'
import { I18nProvider } from '../../../i18n/provider'
import { FileReferenceProvider } from './parts/file-reference'
import { consumePendingWorkflowLocate } from './workflowLocateStore'
import { setTurnTailMetricsVisible } from './turnTailMetricsStore'
import type { TurnEnvelope } from '../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const mockT = (key: string) => key

// isWails defaults to false (test env is not the desktop runtime); individual
// tests can flip it to true to exercise the desktop-only token-rate monitor.
const hoisted = vi.hoisted(() => ({ isWails: vi.fn(() => false) }))
vi.mock('../../../application/runtime', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../application/runtime')>()
  return { ...actual, isWails: hoisted.isWails }
})

// useAgentInfo is stubbed so workflow/worktree rows can be exercised without
// the backend-backed agent list store.
const agentInfoHoisted = vi.hoisted(() => ({
  agent: undefined as { ActiveWorkflowMapCardId?: string; WorktreeID?: string; ProjectId?: string; Runtime?: { WorktreeID?: string; WorktreeName?: string; WorktreeStatus?: string } } | undefined,
}))
vi.mock('../hooks/agentInfoStore', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../hooks/agentInfoStore')>()
  return { ...actual, useAgentInfo: () => agentInfoHoisted.agent }
})

// The worktree row jump is stubbed so the row test stays free of the
// git-store/backend import chain.
const worktreeJumpHoisted = vi.hoisted(() => ({ jump: vi.fn() }))
vi.mock('../hooks/worktreeGitJump', () => ({
  jumpToWorktreeGit: worktreeJumpHoisted.jump,
}))

// useMonoStore is stubbed so the workflow progress mini chart can be fed
// synthetic map/task cards without the backend-backed mono store.
const monoHoisted = vi.hoisted(() => ({
  cards: [] as Array<{ id: string; type?: string; status?: string; parent?: string; tags: string[]; list: string[]; modified: string }>,
}))
vi.mock('../hooks/useMonoStore', () => ({
  useMonoStore: (selector?: (s: { cards: typeof monoHoisted.cards }) => unknown) =>
    selector ? selector({ cards: monoHoisted.cards }) : { cards: monoHoisted.cards },
}))

function taskCard(id: string, status: string, parent: string) {
  return { id, type: 'task', status, parent, tags: [], list: [], modified: '' }
}

describe('buildAssistantTurnSummary', () => {
  it('uses turnState failed over frame-derived success', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'all good', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'failed' },
    }
    const summary = buildAssistantTurnSummary(envelope, true, 'failed', mockT)
    expect(summary.outcome).toBe('error')
    expect(summary.title).toBe('')
  })

  it('shows error detail as title when turn has failed with error', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'failed', error: 'tool call failed: exec error' },
    }
    const summary = buildAssistantTurnSummary(envelope, true, 'failed', mockT)
    expect(summary.outcome).toBe('error')
    expect(summary.title).toBe('tool call failed: exec error')
  })

  it('uses turnState cancelled over frame-derived success', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'all good', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'cancelled' },
    }
    const summary = buildAssistantTurnSummary(envelope, true, 'cancelled', mockT)
    expect(summary.outcome).toBe('cancelled')
  })

  it('maps waiting turnState to the waiting outcome instead of success', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'done for now', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'waiting' },
    }
    const summary = buildAssistantTurnSummary(envelope, true, 'waiting', mockT)
    expect(summary.outcome).toBe('waiting')
  })

  it('falls back to frame-derived error when turnState is completed', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [
        { id: 'f1', type: 'text', content: 'ok', status: 'completed' },
        { id: 'f2', type: 'error', message: 'boom', status: 'completed', code: 'exec_error' },
      ],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'completed' },
    }
    const summary = buildAssistantTurnSummary(envelope, true, 'completed', mockT)
    expect(summary.outcome).toBe('error')
  })

  it('marks success when turnState is completed and last frame is not an error', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'done', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'completed' },
    }
    const summary = buildAssistantTurnSummary(envelope, true, 'completed', mockT)
    expect(summary.outcome).toBe('success')
  })

  it('returns null outcome while turn is running', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    const summary = buildAssistantTurnSummary(envelope, false, 'running', mockT)
    expect(summary.outcome).toBeNull()
  })

  it('shows exploring title for running fork_explore tool', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'tool', toolName: 'fork_explore', input: '{}', status: 'running', progress: { phase: 'searching' } }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    const summary = buildAssistantTurnSummary(envelope, false, 'running', mockT)
    expect(summary.title).toBe('ai.step.exploringPhase')
  })

  it('shows reviewing title for running fork_review tool', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'tool', toolName: 'fork_review', input: '{}', status: 'running', progress: { phase: 'reading' } }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    const summary = buildAssistantTurnSummary(envelope, false, 'running', mockT)
    expect(summary.title).toBe('ai.step.reviewingPhase')
  })

  it('shows parallel execution title for running fork_general tool', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'tool', toolName: 'fork_general', input: '{}', status: 'running', progress: { phase: 'working' } }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    const summary = buildAssistantTurnSummary(envelope, false, 'running', mockT)
    expect(summary.title).toBe('ai.step.parallelExecution')
  })

  it('shows parallel execution title for fork_general even without progress', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'tool', toolName: 'fork_general', input: '{}', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    const summary = buildAssistantTurnSummary(envelope, false, 'running', mockT)
    expect(summary.title).toBe('ai.step.parallelExecution')
  })

  it('shows waiting title for a running pending plan approval frame', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'plan', content: 'p', status: 'running', approvalStatus: 'pending' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    const summary = buildAssistantTurnSummary(envelope, false, 'running', mockT)
    expect(summary.title).toBe('ai.step.waitingForApproval')
  })

  it('shows waiting title for a running pending goal_submit frame', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'goal_submit', condition: 'c', interpretedGoal: 'g', requestId: 'r1', status: 'running', approvalStatus: 'pending' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    const summary = buildAssistantTurnSummary(envelope, false, 'running', mockT)
    expect(summary.title).toBe('ai.step.waitingForApproval')
  })

  it('does not treat a pending plan frame as a user pause even when turnState is paused', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'plan', content: 'p', status: 'running', approvalStatus: 'pending' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'paused' },
    }
    const summary = buildAssistantTurnSummary(envelope, false, 'paused', mockT)
    expect(summary.isUserPaused).toBe(false)
  })

  it('does not treat a pending goal_submit frame as a user pause even when turnState is paused', () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'goal_submit', condition: 'c', interpretedGoal: 'g', requestId: 'r1', status: 'running', approvalStatus: 'pending' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'paused' },
    }
    const summary = buildAssistantTurnSummary(envelope, false, 'paused', mockT)
    expect(summary.isUserPaused).toBe(false)
  })
})

describe('TurnTail rendering', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    hoisted.isWails.mockReturnValue(false)
    agentInfoHoisted.agent = undefined
    monoHoisted.cards = []
    vi.clearAllMocks()
    setTurnTailMetricsVisible({ budgetBar: false, tokenBadge: false, duration: false, throughput: false })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const ctxValue = {
    providers: [],
    activeUnit: null,
    activeProviderId: '',
    onProviderChange: vi.fn(),
    thinkingLevels: new Map(),
    activeThinkingLevel: null,
    onThinkingLevelChange: vi.fn(),
    onOpenFile: vi.fn(),
    onOpenCard: vi.fn(),
    onOpenText: vi.fn(),
  }

  const renderTail = async (
    envelope: TurnEnvelope,
    isComplete: boolean,
    agentActorId?: string,
    onCancelPendingSubmit?: (clientId: string) => void,
    isLast?: boolean,
  ) => {
    await act(async () => {
      root.render(
        <I18nProvider>
          <AIShellContext.Provider value={ctxValue}>
            <TurnTail
              envelope={envelope}
              isComplete={isComplete}
              agentActorId={agentActorId}
              onCancelPendingSubmit={onCancelPendingSubmit}
              isLast={isLast ?? true}
            />
          </AIShellContext.Provider>
        </I18nProvider>,
      )
    })
  }

  it('shows context budget bar when only contextBudget metadata is present and the budget-bar toggle is on', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: {
        turnId: 't1',
        turnState: 'running',
        contextBudget: { estimatedTokens: 500, contextWindowSize: 8000, tokenBudget: 4000 },
      },
    }
    setTurnTailMetricsVisible({ budgetBar: true, tokenBadge: true, duration: false, throughput: false })
    await renderTail(envelope, false)
    expect(container.querySelector('.ai-turn-usage-track')).not.toBeNull()
  })

  it('hides budget bar, token badge, and duration badge by default', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: {
        turnId: 't1',
        turnState: 'running',
        usage: { inputTokens: 100, outputTokens: 50 },
        contextBudget: { estimatedTokens: 500, contextWindowSize: 8000, tokenBudget: 4000 },
        startedAt: new Date(Date.now() - 5000).toISOString(),
      },
    }
    await renderTail(envelope, false)
    expect(container.querySelector('.ai-turn-usage-badge')).toBeNull()
    expect(container.querySelector('.ai-turn-duration-badge')).toBeNull()
  })

  it('shows the budget bar without token text when only the budget-bar toggle is on', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: {
        turnId: 't1',
        turnState: 'running',
        usage: { inputTokens: 100, outputTokens: 50 },
        contextBudget: { estimatedTokens: 500, contextWindowSize: 8000, tokenBudget: 4000 },
      },
    }
    setTurnTailMetricsVisible({ budgetBar: true, tokenBadge: false, duration: false, throughput: false })
    await renderTail(envelope, false)
    expect(container.querySelector('.ai-turn-usage-track')).not.toBeNull()
    expect(container.querySelector('.ai-turn-usage-text-row')).toBeNull()
  })

  it('shows the token badge text without the budget bar when only the token-badge toggle is on', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: {
        turnId: 't1',
        turnState: 'running',
        usage: { inputTokens: 100, outputTokens: 50 },
        contextBudget: { estimatedTokens: 500, contextWindowSize: 8000, tokenBudget: 4000 },
      },
    }
    setTurnTailMetricsVisible({ budgetBar: false, tokenBadge: true, duration: false, throughput: false })
    await renderTail(envelope, false)
    expect(container.querySelector('.ai-turn-usage-track')).toBeNull()
    expect(container.querySelector('.ai-turn-usage-text-row')).not.toBeNull()
  })

  it('renders pending submits with cancel buttons and invokes onCancelPendingSubmit', async () => {
    const onCancel = vi.fn()
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: {
        turnId: 't1',
        turnState: 'running',
        pendingSubmits: [
          { id: 'pending-1', text: 'Queue this message', timestamp: '1' },
          { id: 'pending-2', text: 'Another pending message', timestamp: '2' },
        ],
      },
    }
    await renderTail(envelope, false, undefined, onCancel)
    const list = container.querySelector('.ai-pending-anchor')
    expect(list).not.toBeNull()
    expect(container.textContent).toContain('Queue this message')
    expect(container.textContent).toContain('Another pending message')
    const buttons = container.querySelectorAll('.ai-pending-anchor-cancel')
    expect(buttons.length).toBe(2)
    await act(async () => { (buttons[0] as HTMLButtonElement).click() })
    expect(onCancel).toHaveBeenCalledWith('pending-1')
    await act(async () => { (buttons[1] as HTMLButtonElement).click() })
    expect(onCancel).toHaveBeenCalledWith('pending-2')
  })

  it('hides pending submits when the turn is effectively complete', async () => {
    const onCancel = vi.fn()
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'done', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: {
        turnId: 't1',
        turnState: 'completed',
        pendingSubmits: [{ id: 'pending-1', text: 'Queue this message', timestamp: '1' }],
      },
    }
    await renderTail(envelope, true, undefined, onCancel)
    expect(container.querySelector('.ai-pending-anchor')).toBeNull()
  })

  it('renders active goal row when envelope.goal is present', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
      goal: { Condition: 'implement goal display', MaxTurns: 10, TurnCount: 3 },
    }
    await renderTail(envelope, false)
    expect(container.querySelector('.ai-turn-goal-row')).not.toBeNull()
    expect(container.textContent).toContain('Goal')
    expect(container.textContent).toContain('implement goal display')
    // 3/10 is below 90% of MaxTurns, so the progress counter stays hidden.
    expect(container.textContent).not.toContain('3/10')
  })

  it('shows goal turn progress when nearing 90% of MaxTurns', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
      goal: { Condition: 'implement goal display', MaxTurns: 10, TurnCount: 9 },
    }
    await renderTail(envelope, false)
    expect(container.textContent).toContain('9/10')
  })

  it('opens a temporary text tab when a plain-text goal row is clicked', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
      goal: { Condition: 'plain text goal', MaxTurns: 10, TurnCount: 3 },
    }
    await renderTail(envelope, false)
    const header = container.querySelector('.ai-turn-goal-header') as HTMLButtonElement
    await act(async () => { header.click() })
    expect(ctxValue.onOpenText).toHaveBeenCalledWith(expect.any(String), 'plain text goal')
    expect(ctxValue.onOpenCard).not.toHaveBeenCalled()
  })

  it('clicking the worktree row jumps to git mode on that agent\'s worktree', async () => {
    agentInfoHoisted.agent = {
      ProjectId: 'proj-1',
      Runtime: { WorktreeID: 'wt-9', WorktreeName: 'agent-branch', WorktreeStatus: 'bound' },
    }
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await renderTail(envelope, false, 'actor-1')
    const row = container.querySelector('.ai-turn-worktree-row')
    expect(row).not.toBeNull()
    expect(container.textContent).toContain('agent-branch')
    const header = row!.querySelector('.ai-turn-goal-header') as HTMLButtonElement
    await act(async () => { header.click() })
    expect(worktreeJumpHoisted.jump).toHaveBeenCalledWith('proj-1', 'wt-9')
  })

  it('opens the bound card when a card-bound goal row is clicked', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
      goal: { Condition: 'card goal', MaxTurns: 10, TurnCount: 3, BoundTaskCardId: 'SomeTaskCard' },
    }
    await renderTail(envelope, false)
    const header = container.querySelector('.ai-turn-goal-header') as HTMLButtonElement
    await act(async () => { header.click() })
    expect(ctxValue.onOpenCard).toHaveBeenCalledWith('SomeTaskCard')
    expect(ctxValue.onOpenText).not.toHaveBeenCalled()
  })

  it('renders a workflow row when the agent owns an active workflow map', async () => {
    agentInfoHoisted.agent = { ActiveWorkflowMapCardId: 'MyWorkflowMap' }
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await renderTail(envelope, false, 'actor-1')
    const row = container.querySelector('.ai-turn-workflow-row')
    expect(row).not.toBeNull()
    expect(row!.textContent).toContain('MyWorkflowMap')
  })

  it('hides the workflow row when the agent has no active workflow map', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await renderTail(envelope, false, 'actor-1')
    expect(container.querySelector('.ai-turn-workflow-row')).toBeNull()
  })

  it('opens the workflow mode and requests a locate when the workflow row is clicked', async () => {
    agentInfoHoisted.agent = { ActiveWorkflowMapCardId: 'MyWorkflowMap' }
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    const modes: string[] = []
    const handler = (e: Event) => modes.push((e as CustomEvent).detail)
    window.addEventListener('sporemind:set-shell-content-mode', handler)
    try {
      await renderTail(envelope, false, 'actor-1')
      const header = container.querySelector('.ai-turn-workflow-row .ai-turn-goal-header') as HTMLButtonElement
      await act(async () => { header.click() })
      expect(modes).toEqual(['workflow'])
      expect(consumePendingWorkflowLocate()).toEqual({ mapId: 'MyWorkflowMap', selectStart: true })
    } finally {
      window.removeEventListener('sporemind:set-shell-content-mode', handler)
    }
  })

  it('shows a done/total count and per-task bars when the workflow map tasks are loaded', async () => {
    agentInfoHoisted.agent = { ActiveWorkflowMapCardId: 'ProgressMap' }
    monoHoisted.cards = [
      { id: 'ProgressMap', type: 'map', tags: [], list: [], modified: '' },
      // Deliberately unordered: bars must render sorted by status rank
      // (done → pending_review → doing → todo → backlog), stable within a rank.
      taskCard('t1', 'backlog', 'ProgressMap'),
      taskCard('t2', 'doing', 'ProgressMap'),
      taskCard('t3', 'done', 'ProgressMap'),
      taskCard('t4', 'done', 'ProgressMap'),
      taskCard('t5', 'backlog', 'ProgressMap'),
      taskCard('t6', 'done', 'ProgressMap'),
    ]
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await renderTail(envelope, false, 'actor-1')
    const row = container.querySelector('.ai-turn-workflow-row')
    expect(row).not.toBeNull()
    expect(row!.querySelector('.ai-turn-workflow-progress-count')!.textContent).toBe('3/6')
    const bars = Array.from(row!.querySelectorAll('.ai-turn-workflow-bar'))
    expect(bars.length).toBe(6)
    expect(bars.map(b => Array.from(b.classList).find(c => c.startsWith('ai-turn-workflow-bar--')))).toEqual([
      'ai-turn-workflow-bar--done',
      'ai-turn-workflow-bar--done',
      'ai-turn-workflow-bar--done',
      'ai-turn-workflow-bar--doing',
      'ai-turn-workflow-bar--backlog',
      'ai-turn-workflow-bar--backlog',
    ])
    // First sight of the map records the baseline: no delta badge yet.
    expect(row!.querySelector('.ai-turn-workflow-progress-delta')).toBeNull()
  })

  it('shows a +N delta when the done count grows since the row last saw the map', async () => {
    agentInfoHoisted.agent = { ActiveWorkflowMapCardId: 'DeltaMap' }
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    monoHoisted.cards = [
      { id: 'DeltaMap', type: 'map', tags: [], list: [], modified: '' },
      taskCard('t1', 'done', 'DeltaMap'),
      taskCard('t2', 'doing', 'DeltaMap'),
    ]
    await renderTail(envelope, false, 'actor-1')
    expect(container.querySelector('.ai-turn-workflow-progress-delta')).toBeNull()

    monoHoisted.cards = [
      { id: 'DeltaMap', type: 'map', tags: [], list: [], modified: '' },
      taskCard('t1', 'done', 'DeltaMap'),
      taskCard('t2', 'done', 'DeltaMap'),
    ]
    // TurnTail is React.memo: a fresh envelope object is required to make the
    // re-render reach WorkflowRow (the mono-cards change alone cannot, since
    // the mocked useMonoStore is not reactive).
    await renderTail({ ...envelope, id: 't2' }, false, 'actor-1')
    const delta = container.querySelector('.ai-turn-workflow-progress-delta')
    expect(delta).not.toBeNull()
    expect(delta!.textContent).toContain('+1')
    expect(container.querySelector('.ai-turn-workflow-progress-count')!.textContent).toBe('2/2')
  })

  it('hides the progress mini chart when the map has no loaded task cards', async () => {
    agentInfoHoisted.agent = { ActiveWorkflowMapCardId: 'EmptyMap' }
    monoHoisted.cards = [{ id: 'EmptyMap', type: 'map', tags: [], list: [], modified: '' }]
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await renderTail(envelope, false, 'actor-1')
    const row = container.querySelector('.ai-turn-workflow-row')
    expect(row).not.toBeNull()
    expect(row!.querySelector('.ai-turn-workflow-progress')).toBeNull()
    expect(row!.querySelector('.ai-turn-workflow-bars')).toBeNull()
  })

  it('hides usage badge when neither usage nor contextBudget is present', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await renderTail(envelope, false)
    expect(container.querySelector('.ai-turn-usage-track')).toBeNull()
  })

  it('hides the token-rate monitor on non-desktop runtimes', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    setTurnTailMetricsVisible({ budgetBar: false, tokenBadge: false, duration: false, throughput: true })
    await renderTail(envelope, false)
    expect(container.querySelector('.ai-token-rate-bar')).toBeNull()
  })

  it('hides the token-rate monitor on desktop when the throughput toggle is off', async () => {
    hoisted.isWails.mockReturnValue(true)
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'streaming output', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await renderTail(envelope, false)
    expect(container.querySelector('.ai-token-rate-bar')).toBeNull()
  })

  it('renders the token-rate monitor (desktop) while streaming', async () => {
    hoisted.isWails.mockReturnValue(true)
    setTurnTailMetricsVisible({ budgetBar: false, tokenBadge: false, duration: false, throughput: true })
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'streaming output', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await renderTail(envelope, false)
    const bar = container.querySelector('.ai-token-rate-bar')
    expect(bar).not.toBeNull()
    expect(bar!.classList.contains('active')).toBe(true)
    expect(bar!.textContent).toContain('tok/s')
  })

  it('collapses the sparkline by default and expands on click (desktop)', async () => {
    hoisted.isWails.mockReturnValue(true)
    setTurnTailMetricsVisible({ budgetBar: false, tokenBadge: false, duration: false, throughput: true })
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'streaming output', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await renderTail(envelope, false)
    const bar = container.querySelector('.ai-token-rate-bar') as HTMLButtonElement
    expect(bar).not.toBeNull()
    // Collapsed by default: no sparkline svg, aria-expanded=false.
    expect(bar.getAttribute('aria-expanded')).toBe('false')
    expect(bar.classList.contains('expanded')).toBe(false)
    expect(container.querySelector('.ai-token-rate-spark')).toBeNull()
    // Click to expand.
    await act(async () => { bar.click() })
    const barAfter = container.querySelector('.ai-token-rate-bar') as HTMLButtonElement
    expect(barAfter.getAttribute('aria-expanded')).toBe('true')
    expect(barAfter.classList.contains('expanded')).toBe(true)
  })

  it('shows outputTokens/(elapsedSeconds - tool time) summary when the turn is complete (desktop)', async () => {
    hoisted.isWails.mockReturnValue(true)
    setTurnTailMetricsVisible({ budgetBar: false, tokenBadge: false, duration: false, throughput: true })
    // elapsedSeconds=12, but 4s was spent in a completed tool → net 8s of LLM
    // generation. outputTokens=2400 → 2400/8 = 300 tok/s (not 2400/12=200).
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [
        { id: 'f1', type: 'text', content: 'done', status: 'completed' },
        { id: 'tool1', type: 'tool', toolName: 'shell', input: '{}', status: 'completed', durationSeconds: 4 },
      ],
      timestamp: '1',
      completed: true,
      metadata: {
        turnId: 't1',
        turnState: 'completed',
        elapsedSeconds: 12,
        usage: { inputTokens: 1000, outputTokens: 2400, totalTokens: 3400 },
      },
    }
    await renderTail(envelope, true)
    const bar = container.querySelector('.ai-token-rate-bar')
    expect(bar).not.toBeNull()
    expect(bar!.classList.contains('active')).toBe(false)
    // netElapsed = 12 - 4 = 8s; summary = 2400 / 8 = 300 tok/s.
    expect(bar!.getAttribute('title')).toContain('2400 output tokens / 8s')
    expect(bar!.getAttribute('title')).toContain('excl. 4s tools')
    expect(bar!.getAttribute('title')).toContain('300 tok/s')
  })

  it('hides the token-rate monitor on historical turns (desktop)', async () => {
    hoisted.isWails.mockReturnValue(true)
    setTurnTailMetricsVisible({ budgetBar: false, tokenBadge: false, duration: false, throughput: true })
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'done', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: {
        turnId: 't1',
        turnState: 'completed',
        elapsedSeconds: 8,
        usage: { inputTokens: 1000, outputTokens: 2400, totalTokens: 3400 },
      },
    }
    await renderTail(envelope, true, undefined, undefined, false)
    expect(container.querySelector('.ai-token-rate-bar')).toBeNull()
  })

  it('shows file changes for a running turn, not only when complete', async () => {
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'running', status: 'running' }],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
      fileChanges: [
        { filepath: 'src/foo.ts', filename: 'foo.ts', additions: 5, deletions: 1 },
      ],
    }
    await renderTail(envelope, false)
    expect(container.querySelector('.ai-turn-file-changes')).not.toBeNull()
    expect(container.textContent).toContain('foo.ts')
  })

  it('truncates file changes to the latest when there are too many', async () => {
    const fileChanges = Array.from({ length: 8 }, (_, i) => ({
      filepath: `src/file${i}.ts`,
      filename: `file${i}.ts`,
      additions: 1,
    }))
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'done', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'completed' },
      fileChanges,
    }
    await renderTail(envelope, true)
    const items = container.querySelectorAll('.ai-turn-file-change-item')
    expect(items.length).toBe(6)
    expect(container.textContent).toContain('file7.ts')
    expect(container.textContent).not.toContain('file0.ts')
    expect(container.textContent).not.toContain('file1.ts')
    const toggle = container.querySelector('.ai-turn-truncate-toggle')
    expect(toggle).not.toBeNull()
    expect(toggle!.textContent).toContain('2')
  })

  it('expands all file changes when the toggle is clicked', async () => {
    const fileChanges = Array.from({ length: 8 }, (_, i) => ({
      filepath: `src/file${i}.ts`,
      filename: `file${i}.ts`,
      additions: 1,
    }))
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'done', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'completed' },
      fileChanges,
    }
    await renderTail(envelope, true)
    expect(container.querySelectorAll('.ai-turn-file-change-item').length).toBe(6)
    const toggle = container.querySelector('.ai-turn-truncate-toggle') as HTMLButtonElement
    await act(async () => { toggle.click() })
    expect(container.querySelectorAll('.ai-turn-file-change-item').length).toBe(8)
    expect(container.textContent).toContain('file0.ts')
  })

  it('truncates tasks keeping in_progress and the latest', async () => {
    const tasks = [
      ...Array.from({ length: 5 }, (_, i) => ({ id: `c${i}`, subject: `Completed ${i}`, status: 'completed' as const })),
      { id: 'ip', subject: 'Active task', status: 'in_progress' as const },
      ...Array.from({ length: 3 }, (_, i) => ({ id: `c${i + 5}`, subject: `Completed ${i + 5}`, status: 'completed' as const })),
    ]
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'done', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'completed' },
      tasks,
    }
    await renderTail(envelope, true)
    // 1 in_progress + 4 latest completed = 5 visible, 4 hidden
    expect(container.querySelectorAll('.ai-turn-task-row').length).toBe(5)
    expect(container.textContent).toContain('Active task')
    expect(container.textContent).toContain('Completed 7')
    expect(container.textContent).not.toContain('Completed 0')
    expect(container.textContent).not.toContain('Completed 1')
    const toggle = container.querySelector('.ai-turn-truncate-toggle')
    expect(toggle).not.toBeNull()
    expect(toggle!.textContent).toContain('4')
  })

  it('expands all tasks when the toggle is clicked', async () => {
    const tasks = [
      ...Array.from({ length: 5 }, (_, i) => ({ id: `c${i}`, subject: `Completed ${i}`, status: 'completed' as const })),
      { id: 'ip', subject: 'Active task', status: 'in_progress' as const },
      ...Array.from({ length: 3 }, (_, i) => ({ id: `c${i + 5}`, subject: `Completed ${i + 5}`, status: 'completed' as const })),
    ]
    const envelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'done', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'completed' },
      tasks,
    }
    await renderTail(envelope, true)
    expect(container.querySelectorAll('.ai-turn-task-row').length).toBe(5)
    const toggle = container.querySelector('.ai-turn-truncate-toggle') as HTMLButtonElement
    await act(async () => { toggle.click() })
    expect(container.querySelectorAll('.ai-turn-task-row').length).toBe(9)
    expect(container.textContent).toContain('Completed 0')
  })

  describe('file change context menu', () => {
    const fileOpsHoisted = vi.hoisted(() => ({
      openInSystem: vi.fn(async () => true),
      revealInSystem: vi.fn(async () => true),
    }))
    vi.mock('./file-ops', async (importOriginal) => {
      const actual = await importOriginal<typeof import('./file-ops')>()
      return { ...actual, openInSystem: fileOpsHoisted.openInSystem, revealInSystem: fileOpsHoisted.revealInSystem }
    })

    const fileChangeEnvelope: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [{ id: 'f1', type: 'text', content: 'done', status: 'completed' }],
      timestamp: '1',
      completed: true,
      metadata: { turnId: 't1', turnState: 'completed' },
      fileChanges: [
        { filepath: 'src/foo.ts', filename: 'foo.ts', additions: 5, deletions: 1, diffContent: '@@ -1,1 +1,2 @@\n+line' },
      ],
    }

    const renderTailWithProject = async (projectId: string | null, agentActorId?: string) => {
      await act(async () => {
        root.render(
          <I18nProvider initialLocale="zh-CN">
            <AIShellContext.Provider value={ctxValue}>
              <FileReferenceProvider projectId={projectId} projectRoot={null}>
                <TurnTail envelope={fileChangeEnvelope} isComplete agentActorId={agentActorId} />
              </FileReferenceProvider>
            </AIShellContext.Provider>
          </I18nProvider>,
        )
      })
    }

    const openMenu = async (): Promise<HTMLElement> => {
      const item = container.querySelector('.ai-turn-file-change-item') as HTMLButtonElement
      await act(async () => {
        item.dispatchEvent(new MouseEvent('contextmenu', {
          bubbles: true, cancelable: true, clientX: 100, clientY: 100,
        }))
      })
      const menu = document.querySelector('.ai-turn-file-menu') as HTMLElement | null
      expect(menu).not.toBeNull()
      return menu!
    }

    const menuItem = (menu: HTMLElement, labelKey: string): HTMLButtonElement => {
      const items = Array.from(menu.querySelectorAll('.ai-turn-file-menu-item'))
      const match = items.find(el => el.textContent === labelKey)
      expect(match).toBeDefined()
      return match as HTMLButtonElement
    }

    it('opens a portaled menu on right-click with open/reveal/copy actions', async () => {
      await renderTailWithProject('proj-1')
      const menu = await openMenu()
      expect(menu).not.toBeNull()
      expect(menu.textContent).toContain('打开')
      expect(menu.textContent).toContain('在系统中打开')
      expect(menu.textContent).toContain('在系统中显示')
      expect(menu.textContent).toContain('复制路径')
      expect(menu.querySelector('.ai-turn-file-menu-title')!.textContent).toBe('foo.ts')
    })

    it('re-opens the menu targeting the right-clicked file and calls in-app open', async () => {
      await renderTailWithProject('proj-1')
      const menu = await openMenu()
      await act(async () => { menuItem(menu, '打开').click() })
      expect(ctxValue.onOpenFile).toHaveBeenCalledWith('src/foo.ts', fileChangeEnvelope.fileChanges![0]!.diffContent, 1, undefined, 'diff')
      expect(document.querySelector('.ai-turn-file-menu')).toBeNull()
    })

    it('opens the file with the OS default application via the project shell', async () => {
      await renderTailWithProject('proj-1')
      const menu = await openMenu()
      await act(async () => { menuItem(menu, '在系统中打开').click() })
      expect(fileOpsHoisted.openInSystem).toHaveBeenCalledWith('proj-1', 'src/foo.ts', undefined)
    })

    it('reveals the file in the system file manager via the project shell', async () => {
      await renderTailWithProject('proj-1')
      const menu = await openMenu()
      await act(async () => { menuItem(menu, '在系统中显示').click() })
      expect(fileOpsHoisted.revealInSystem).toHaveBeenCalledWith('proj-1', 'src/foo.ts', undefined)
    })

    it('copies the file path to the clipboard', async () => {
      const writeText = vi.fn(async () => {})
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
      await renderTailWithProject('proj-1')
      const menu = await openMenu()
      await act(async () => { menuItem(menu, '复制路径').click() })
      expect(writeText).toHaveBeenCalledWith('src/foo.ts')
    })

    it('disables system actions when no project context is available', async () => {
      await renderTailWithProject(null)
      const menu = await openMenu()
      const openBtn = menuItem(menu, '在系统中打开') as HTMLButtonElement
      const revealBtn = menuItem(menu, '在系统中显示') as HTMLButtonElement
      expect(openBtn.disabled).toBe(true)
      expect(revealBtn.disabled).toBe(true)
      expect(menuItem(menu, '打开').disabled).toBe(false)
    })

    it('passes the agent worktree id through to system open/reveal', async () => {
      agentInfoHoisted.agent = { WorktreeID: 'wt-1' }
      await renderTailWithProject('proj-1', 'actor-1')
      const menu = await openMenu()
      await act(async () => { menuItem(menu, '在系统中打开').click() })
      expect(fileOpsHoisted.openInSystem).toHaveBeenCalledWith('proj-1', 'src/foo.ts', 'wt-1')
    })

    it('closes the menu on Escape', async () => {
      await renderTailWithProject('proj-1')
      const menu = await openMenu()
      expect(menu).not.toBeNull()
      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      })
      expect(document.querySelector('.ai-turn-file-menu')).toBeNull()
    })
  })
})
