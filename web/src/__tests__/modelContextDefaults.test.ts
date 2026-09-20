import { describe, it, expect } from 'vitest'
import { lookupModelDefault, DEFAULT_CONTEXT } from '../../../const/modelContextDefaults'

describe('lookupModelDefault', () => {
  it('returns the value for an exact key match', () => {
    expect(lookupModelDefault('glm-5.2', { 'glm-5.2': 200 * 1024 })).toBe(200 * 1024)
  })

  it('falls back to a prefix match so versioned variants inherit parent defaults', () => {
    const table = {
      'glm-5.2': 200 * 1024,
      'glm-5': 131072,
      'gpt-5.4': 262144,
      'gpt-5': 100000,
    }
    expect(lookupModelDefault('glm-5.2-0733', table)).toBe(200 * 1024)
    expect(lookupModelDefault('glm-5.1-whatever', table)).toBe(131072)
    expect(lookupModelDefault('gpt-5.4-mini', table)).toBe(262144)
    expect(lookupModelDefault('gpt-5-mini', table)).toBe(100000)
  })

  it('prefers the longest matching prefix over a shorter one', () => {
    const table = { 'gpt-5': 100, 'gpt-5.4': 200 }
    // 'gpt-5.4-mini' starts with both 'gpt-5' and 'gpt-5.4'; the longest wins.
    expect(lookupModelDefault('gpt-5.4-mini', table)).toBe(200)
    expect(lookupModelDefault('gpt-5-mini', table)).toBe(100)
  })

  it('does not treat a non-prefix key as a match even when it is a substring', () => {
    // 'glm-5-turbo' is NOT a prefix of 'glm-5.2-0733', so it must not win.
    const table = { 'glm-5': 131072, 'glm-5-turbo': 999999 }
    expect(lookupModelDefault('glm-5.2-0733', table)).toBe(131072)
  })

  it('returns undefined when no key is a prefix of the name', () => {
    expect(lookupModelDefault('unknown-model', { 'glm-5.2': 200 * 1024 })).toBeUndefined()
  })

  it('returns undefined for an empty table', () => {
    expect(lookupModelDefault('glm-5.2', {})).toBeUndefined()
  })

  it('returns undefined for an empty name when no key is empty', () => {
    expect(lookupModelDefault('', { 'glm-5.2': 200 * 1024 })).toBeUndefined()
  })

  it('lets the caller supply its own fallback via DEFAULT_CONTEXT', () => {
    expect(lookupModelDefault('unknown-model', {}) ?? DEFAULT_CONTEXT).toBe(DEFAULT_CONTEXT)
  })

  it('resolves real versioned GLM variants against the shipped defaults', async () => {
    const { MODEL_CONTEXT_DEFAULTS } = await import('../../../const/modelContextDefaults')
    expect(lookupModelDefault('glm-5.2-0733', MODEL_CONTEXT_DEFAULTS)).toBe(1048576)
    // Exact match still works.
    expect(lookupModelDefault('gpt-5.4', MODEL_CONTEXT_DEFAULTS)).toBe(1050000)
  })
})
