import type { AgentStatus } from './hooks/agentInfoStore'

export interface ComposerPlaceholderContext {
  agentStatus?: AgentStatus
  boundTaskCardId?: string
  hasConversation: boolean
  mountedModeCardIds: string[]
}

export type ComposerPlaceholderResolver = (ctx: ComposerPlaceholderContext) => string | undefined

interface RegistryEntry {
  id: string
  resolver: ComposerPlaceholderResolver
  priority: number
}

const registry = new Map<string, RegistryEntry>()

export function registerComposerPlaceholderResolver(
  id: string,
  resolver: ComposerPlaceholderResolver,
  opts?: { priority?: number },
): void {
  registry.set(id, { id, resolver, priority: opts?.priority ?? 0 })
}

export function resolveComposerPlaceholderKey(ctx: ComposerPlaceholderContext): string {
  const entries = [...registry.values()].sort((a, b) => b.priority - a.priority)
  for (const entry of entries) {
    const key = entry.resolver(ctx)
    if (key !== undefined) return key
  }
  return 'composer.placeholder.default'
}

// --- Built-in resolvers (registered at module load) ---

const MODE_PRIORITY = 100
const IN_TASK_PRIORITY = 50

const IN_TASK_STATUSES: ReadonlySet<AgentStatus> = new Set([
  'running',
  'waiting',
  'paused',
  'ask_user',
  'ask_permission',
  'plan_approval',
  'goal_submit',
])

function makeModeResolver(modeCardId: string, placeholderKey: string): ComposerPlaceholderResolver {
  return (ctx) => (ctx.mountedModeCardIds.includes(modeCardId) ? placeholderKey : undefined)
}

registerComposerPlaceholderResolver(
  'builtin:mode:workflow',
  makeModeResolver('builtin:mode:workflow', 'composer.placeholder.workflow'),
  { priority: MODE_PRIORITY },
)
registerComposerPlaceholderResolver(
  'builtin:mode:goal',
  makeModeResolver('builtin:mode:goal', 'composer.placeholder.goal'),
  { priority: MODE_PRIORITY },
)
registerComposerPlaceholderResolver(
  'builtin:mode:memory',
  makeModeResolver('builtin:mode:memory', 'composer.placeholder.memory'),
  { priority: MODE_PRIORITY },
)
registerComposerPlaceholderResolver(
  'builtin:inTask',
  (ctx) => {
    const statusMatch = ctx.agentStatus !== undefined && IN_TASK_STATUSES.has(ctx.agentStatus)
    const taskMatch = !!ctx.boundTaskCardId
    if ((statusMatch || taskMatch) && ctx.hasConversation) {
      return 'composer.placeholder.inTask'
    }
    return undefined
  },
  { priority: IN_TASK_PRIORITY },
)
