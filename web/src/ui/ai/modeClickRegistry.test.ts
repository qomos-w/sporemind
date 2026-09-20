import { describe, expect, it, vi } from 'vitest'
import {
  getModeClickAction,
  registerModeClickAction,
  runModeClickAction,
  unregisterModeClickAction,
} from './modeClickRegistry'

describe('modeClickRegistry', () => {
  it('runs a registered action and reports handled', () => {
    const action = vi.fn()
    registerModeClickAction('builtin:mode:test', action)
    expect(getModeClickAction('builtin:mode:test')).toBe(action)
    expect(runModeClickAction('builtin:mode:test')).toBe(true)
    expect(action).toHaveBeenCalledTimes(1)
    unregisterModeClickAction('builtin:mode:test')
    expect(getModeClickAction('builtin:mode:test')).toBeUndefined()
  })

  it('passes the click context through to the action', () => {
    const action = vi.fn()
    registerModeClickAction('builtin:mode:ctx', action)
    runModeClickAction('builtin:mode:ctx', { agentActorId: 'actor-9' })
    expect(action).toHaveBeenCalledWith({ agentActorId: 'actor-9' })
    unregisterModeClickAction('builtin:mode:ctx')
  })

  it('reports unhandled for unknown card ids', () => {
    expect(runModeClickAction('builtin:mode:unknown')).toBe(false)
  })

  it('replaces an existing registration', () => {
    const first = vi.fn()
    const second = vi.fn()
    registerModeClickAction('builtin:mode:replace', first)
    registerModeClickAction('builtin:mode:replace', second)
    runModeClickAction('builtin:mode:replace')
    expect(first).not.toHaveBeenCalled()
    expect(second).toHaveBeenCalledTimes(1)
    unregisterModeClickAction('builtin:mode:replace')
  })
})
