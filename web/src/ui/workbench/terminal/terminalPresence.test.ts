import { describe, it, expect } from 'vitest'

import {
  DEFAULT_TERMINAL_KEYWORDS,
  initialTerminalPresence,
  matchesTerminalKeyword,
  reduceTerminalPresence,
  type TerminalPresencePolicy,
} from './terminalPresence'

const policy: TerminalPresencePolicy = { keywords: DEFAULT_TERMINAL_KEYWORDS, idleMs: 1000 }

describe('matchesTerminalKeyword', () => {
  it('matches case-insensitive substrings', () => {
    expect(matchesTerminalKeyword('Please BUILD the project', policy.keywords)).toBe(true)
    expect(matchesTerminalKeyword('run go test ./...', policy.keywords)).toBe(true)
    expect(matchesTerminalKeyword('terminal', policy.keywords)).toBe(true)
  })

  it('returns false for unrelated text and empty input', () => {
    expect(matchesTerminalKeyword('let us talk about the weather', policy.keywords)).toBe(false)
    expect(matchesTerminalKeyword('', policy.keywords)).toBe(false)
  })
})

describe('reduceTerminalPresence', () => {
  it('starts hidden by default (blackboard semantics)', () => {
    const state = initialTerminalPresence(0)
    expect(state.hidden).toBe(true)
    expect(state.reason).toBe('initial')
  })

  it('summons on a keyword mention', () => {
    const state = reduceTerminalPresence(initialTerminalPresence(0), { type: 'text', text: 'please fix the build', at: 5 }, policy)
    expect(state.hidden).toBe(false)
    expect(state.reason).toBe('keyword')
    expect(state.summonedAt).toBe(5)
  })

  it('ignores non-matching text', () => {
    const initial = initialTerminalPresence(0)
    const state = reduceTerminalPresence(initial, { type: 'text', text: 'just chatting', at: 5 }, policy)
    expect(state).toBe(initial)
  })

  it('summons on output and resets the idle clock while visible', () => {
    let state = reduceTerminalPresence(initialTerminalPresence(0), { type: 'output', at: 10 }, policy)
    expect(state.hidden).toBe(false)
    expect(state.reason).toBe('output')
    expect(state.lastActivityAt).toBe(10)

    state = reduceTerminalPresence(state, { type: 'output', at: 900 }, policy)
    expect(state.lastActivityAt).toBe(900)
    expect(state.hidden).toBe(false)
  })

  it('keeps the summon reason while output continues', () => {
    let state = reduceTerminalPresence(initialTerminalPresence(0), { type: 'text', text: 'npm run build', at: 1 }, policy)
    state = reduceTerminalPresence(state, { type: 'output', at: 2 }, policy)
    expect(state.reason).toBe('keyword')
  })

  it('retreats on tick only after the idle window elapses', () => {
    const visible = reduceTerminalPresence(initialTerminalPresence(0), { type: 'output', at: 100 }, policy)
    const early = reduceTerminalPresence(visible, { type: 'tick', at: 500 }, policy)
    expect(early.hidden).toBe(false)
    const late = reduceTerminalPresence(visible, { type: 'tick', at: 1400 }, policy)
    expect(late.hidden).toBe(true)
  })

  it('does not retreat a hidden card', () => {
    const hidden = initialTerminalPresence(0)
    const state = reduceTerminalPresence(hidden, { type: 'tick', at: 999999 }, policy)
    expect(state).toBe(hidden)
  })

  it('re-summons after retreat when output resumes', () => {
    let state = reduceTerminalPresence(initialTerminalPresence(0), { type: 'output', at: 100 }, policy)
    state = reduceTerminalPresence(state, { type: 'tick', at: 2000 }, policy)
    expect(state.hidden).toBe(true)
    state = reduceTerminalPresence(state, { type: 'output', at: 2100 }, policy)
    expect(state.hidden).toBe(false)
    expect(state.reason).toBe('output')
  })

  it('honors explicit hide and summon', () => {
    let state = reduceTerminalPresence(initialTerminalPresence(0), { type: 'summon', reason: 'launch', at: 7 }, policy)
    expect(state.hidden).toBe(false)
    expect(state.reason).toBe('launch')
    state = reduceTerminalPresence(state, { type: 'hide' }, policy)
    expect(state.hidden).toBe(true)
  })
})
