import { describe, expect, it } from 'vitest'
import type { AgentStatus } from './hooks/agentInfoStore'
import {
  registerComposerPlaceholderResolver,
  resolveComposerPlaceholderKey,
  type ComposerPlaceholderContext,
} from './composerPlaceholderRegistry'

const neutralCtx: ComposerPlaceholderContext = {
  agentStatus: 'idle',
  boundTaskCardId: undefined,
  hasConversation: false,
  mountedModeCardIds: [],
}

function ctxWithSentinel(sentinel: string): ComposerPlaceholderContext {
  return { ...neutralCtx, mountedModeCardIds: [sentinel] }
}

function sentinelMatcher(sentinel: string, key: string) {
  return (ctx: ComposerPlaceholderContext): string | undefined =>
    ctx.mountedModeCardIds.includes(sentinel) ? key : undefined
}

describe('composerPlaceholderRegistry — built-in mode resolvers', () => {
  it('returns workflow key when workflow mode is mounted', () => {
    expect(
      resolveComposerPlaceholderKey({ ...neutralCtx, mountedModeCardIds: ['builtin:mode:workflow'] }),
    ).toBe('composer.placeholder.workflow')
  })

  it('returns goal key when goal mode is mounted', () => {
    expect(
      resolveComposerPlaceholderKey({ ...neutralCtx, mountedModeCardIds: ['builtin:mode:goal'] }),
    ).toBe('composer.placeholder.goal')
  })

  it('returns memory key when memory mode is mounted', () => {
    expect(
      resolveComposerPlaceholderKey({ ...neutralCtx, mountedModeCardIds: ['builtin:mode:memory'] }),
    ).toBe('composer.placeholder.memory')
  })
})

describe('composerPlaceholderRegistry — in-task resolver', () => {
  const inTaskStatuses: AgentStatus[] = [
    'running',
    'waiting',
    'paused',
    'ask_user',
    'ask_permission',
    'plan_approval',
    'goal_submit',
  ]

  for (const status of inTaskStatuses) {
    it(`returns inTask key when status=${status} and hasConversation`, () => {
      expect(
        resolveComposerPlaceholderKey({ ...neutralCtx, agentStatus: status, hasConversation: true }),
      ).toBe('composer.placeholder.inTask')
    })
  }

  it('returns inTask key when boundTaskCardId is set and hasConversation', () => {
    expect(
      resolveComposerPlaceholderKey({ ...neutralCtx, boundTaskCardId: 'task-42', hasConversation: true }),
    ).toBe('composer.placeholder.inTask')
  })

  it('does NOT return inTask key when hasConversation is false', () => {
    expect(
      resolveComposerPlaceholderKey({ ...neutralCtx, agentStatus: 'running', hasConversation: false }),
    ).toBe('composer.placeholder.default')
  })

  it('does NOT return inTask key for non-task statuses', () => {
    const nonTaskStatuses: AgentStatus[] = ['idle', 'error', 'completed']
    for (const status of nonTaskStatuses) {
      expect(
        resolveComposerPlaceholderKey({ ...neutralCtx, agentStatus: status, hasConversation: true }),
      ).toBe('composer.placeholder.default')
    }
  })
})

describe('composerPlaceholderRegistry — priority / composition', () => {
  it('mode-specific resolver beats in-task resolver (workflow + running)', () => {
    expect(
      resolveComposerPlaceholderKey({
        agentStatus: 'running',
        boundTaskCardId: undefined,
        hasConversation: true,
        mountedModeCardIds: ['builtin:mode:workflow'],
      }),
    ).toBe('composer.placeholder.workflow')
  })

  it('higher-priority custom resolver wins over lower-priority', () => {
    registerComposerPlaceholderResolver('test:prio-high', sentinelMatcher('test:prio', 'test.high'), {
      priority: 200,
    })
    registerComposerPlaceholderResolver('test:prio-low', sentinelMatcher('test:prio', 'test.low'), {
      priority: 10,
    })
    expect(resolveComposerPlaceholderKey(ctxWithSentinel('test:prio'))).toBe('test.high')
  })

  it('same-priority resolvers resolve in registration order (first wins)', () => {
    registerComposerPlaceholderResolver('test:reg-first', sentinelMatcher('test:reg', 'test.first'), {
      priority: 200,
    })
    registerComposerPlaceholderResolver('test:reg-second', sentinelMatcher('test:reg', 'test.second'), {
      priority: 200,
    })
    expect(resolveComposerPlaceholderKey(ctxWithSentinel('test:reg'))).toBe('test.first')
  })
})

describe('composerPlaceholderRegistry — default fallback', () => {
  it('returns default key when no resolver matches', () => {
    expect(resolveComposerPlaceholderKey(neutralCtx)).toBe('composer.placeholder.default')
  })
})

describe('composerPlaceholderRegistry — re-registration overwrites', () => {
  it('re-registering the same id replaces the resolver', () => {
    registerComposerPlaceholderResolver('test:override', sentinelMatcher('test:override', 'test.original'), {
      priority: 300,
    })
    expect(resolveComposerPlaceholderKey(ctxWithSentinel('test:override'))).toBe('test.original')

    registerComposerPlaceholderResolver('test:override', sentinelMatcher('test:override', 'test.updated'), {
      priority: 300,
    })
    expect(resolveComposerPlaceholderKey(ctxWithSentinel('test:override'))).toBe('test.updated')
  })
})
