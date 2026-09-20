/**
 * Registers the click actions of builtin bundle modes (modeClickRegistry).
 * Imported for its side effect by the shell layout so registration happens
 * once at app init.
 *
 * Workflow mode: open the workflow view and locate the workflow map bound to
 * the composer agent that owns the clicked badge. When that agent has no
 * active workflow the view opens without a locate.
 *
 * Worktree mode: open git mode on the composer agent's bound worktree and
 * select its HEAD commit (jumpToWorktreeGit — safe while the agent is
 * mid-turn; it never routes through the agent actor).
 *
 * Memory mode: dispatch a 'sporemind:open-brain' event that the shell layout
 * listens for, opening the brain topology view for the composer agent (the
 * shell handles the memory-mount check and confirmation when needed).
 *
 * Scheduler mode: open the scheduled view and locate the scheduler card bound
 * to the composer agent (data.bound_agent matches the agent). When the agent
 * has no bound task the view opens without a locate.
 *
 * Goal mode: dispatch a 'sporemind:open-goal' event that the shell layout
 * listens for, opening the same target as the turn-tail goal row click — the
 * bound task card in the right panel, or a read-only goal text tab.
 */
import { registerModeClickAction } from './modeClickRegistry'
import { openWorkflowAndLocate } from './components/workflowLocateStore'
import { getAgentListItems } from './hooks/agentListStore'
import { jumpToWorktreeGit } from './hooks/worktreeGitJump'
import { openScheduledAndLocate } from './components/scheduledLocateStore'

/** The active workflow map of the given agent (stamped on its mode state
 *  while it owns/orchestrates a workflow map). */
export function findAgentWorkflowMapId(agentActorId: string | undefined): string | undefined {
  if (!agentActorId) return undefined
  const found = getAgentListItems().find(a => a.ActorId === agentActorId)
  return found?.Mode?.ActiveWorkflowMapCardId || found?.Runtime?.ActiveWorkflowMapCardId || undefined
}

registerModeClickAction('builtin:mode:workflow', (ctx) => {
  openWorkflowAndLocate(findAgentWorkflowMapId(ctx?.agentActorId))
})

/** The worktree the given agent is currently bound to (stamped on its runtime
 *  state while it runs in an isolated worktree). */
export function findAgentWorktreeId(agentActorId: string | undefined): string | undefined {
  if (!agentActorId) return undefined
  const found = getAgentListItems().find(a => a.ActorId === agentActorId)
  return found?.Runtime?.WorktreeID || undefined
}

registerModeClickAction('builtin:mode:worktree', (ctx) => {
  const agent = ctx?.agentActorId
    ? getAgentListItems().find(a => a.ActorId === ctx.agentActorId)
    : undefined
  if (!agent?.ProjectId) return
  void jumpToWorktreeGit(agent.ProjectId, agent.Runtime?.WorktreeID)
})

registerModeClickAction('builtin:mode:memory', (ctx) => {
  window.dispatchEvent(new CustomEvent('sporemind:open-brain', { detail: ctx?.agentActorId }))
})

/** Scheduler card bound to the given agent: any scheduler card whose
 *  data.bound_agent matches the agent's Id or ActorId. Resolved lazily via a
 *  dynamic import so this static registration module never drags the card
 *  store (and its client/registry chain) into the app-init import graph. */
export async function findAgentSchedulerCardId(agentActorId: string | undefined): Promise<string | undefined> {
  if (!agentActorId) return undefined
  const agent = getAgentListItems().find(a => a.ActorId === agentActorId)
  if (!agent) return undefined
  const { monoStore } = await import('../panels/mono-store')
  for (const card of monoStore.getState().cards) {
    if (card.type !== 'scheduler') continue
    const data = (card.data ?? {}) as Record<string, unknown>
    const bound = typeof data.bound_agent === 'string' ? data.bound_agent : ''
    if (!bound) continue
    const ref = bound.startsWith('agent:') ? bound.slice(6) : bound
    if (ref === agent.Id || ref === agent.ActorId) return card.id
  }
  return undefined
}

registerModeClickAction('builtin:mode:goal', (ctx) => {
  window.dispatchEvent(new CustomEvent('sporemind:open-goal', { detail: ctx?.agentActorId }))
})

registerModeClickAction('builtin:mode:scheduler', (ctx) => {
  void findAgentSchedulerCardId(ctx?.agentActorId).then(cardId => {
    openScheduledAndLocate(cardId)
  })
})
