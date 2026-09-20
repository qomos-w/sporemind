import type { AgentInfo } from '../ai/hooks/agentInfoStore'

/** Kanban-scoped kinds: workflow/fork children auto-created by the system. */
const KANBAN_ROLES = ['worker', 'reviewer']

const RECENT_WINDOW_MS = 10 * 60 * 1000
const EXTENDED_WINDOW_MS = 60 * 60 * 1000
const EXTENDED_FILL_TARGET = 4

/**
 * Agents that can occupy a multi-console cell: every user-facing agent plus
 * sub-agents (children carrying ParentAgentId, including kanban workers and
 * reviewers spawned by workflows or agent forks). Standalone kanban-kind
 * agents are excluded — those kinds only ever exist as children.
 */
export function selectConsoleAgents(agents: AgentInfo[]): AgentInfo[] {
  return agents.filter(a => !KANBAN_ROLES.includes(a.AgentKind) || !!a.ParentAgentId)
}

function hasRecentActivity(agent: AgentInfo, cutoff: number): boolean {
  if (!agent.LastActivity) return false
  const lastActivity = Date.parse(agent.LastActivity)
  return Number.isFinite(lastActivity) && lastActivity >= cutoff
}

/**
 * Agent order produced by the auto-arrange button: agents that are working or
 * had activity within the recent window (sub-agents included, they compete on
 * the same IsWorking/activity criteria as their parents), topped up with
 * recently-idle sessions when the grid would otherwise look empty.
 */
export function selectAutoArrangeAgents(agents: AgentInfo[], now: number = Date.now()): AgentInfo[] {
  const activeAgents = agents.filter(agent => agent.IsWorking || hasRecentActivity(agent, now - RECENT_WINDOW_MS))
  const arranged = [...activeAgents]
  if (activeAgents.length <= 2 && activeAgents.length < EXTENDED_FILL_TARGET) {
    const recentSessions = agents
      .filter(agent => !agent.IsWorking && !activeAgents.includes(agent) && agent.LastActivity)
      .map(agent => ({ agent, lastActivity: Date.parse(agent.LastActivity as string) }))
      .filter(item => Number.isFinite(item.lastActivity) && item.lastActivity >= now - EXTENDED_WINDOW_MS)
      .sort((a, b) => b.lastActivity - a.lastActivity)
      .slice(0, EXTENDED_FILL_TARGET - activeAgents.length)
    arranged.push(...recentSessions.map(item => item.agent))
  }
  return arranged
}
