import { describe, it, expect } from 'vitest'
import { SUGGESTIONS, selectSuggestions, MAX_FAVORITE_CHIPS } from './suggestionRegistry'
import type { SuggestionContext } from './suggestionRegistry'

const coderCtx: SuggestionContext = {
  hasProject: true,
  isHomeMode: false,
  hasGitRepo: true,
  agentKind: 'coder',
}

describe('selectSuggestions', () => {
  it('returns empty when there is no project', () => {
    expect(selectSuggestions({ hasProject: false, isHomeMode: false })).toEqual([])
  })

  it('includes reviewCommit only when hasGitRepo is true', () => {
    const withGit = selectSuggestions(coderCtx)
    expect(withGit.map(s => s.id)).toContain('reviewCommit')

    const withoutGit = selectSuggestions({ ...coderCtx, hasGitRepo: false })
    expect(withoutGit.map(s => s.id)).not.toContain('reviewCommit')
  })

  it('includes explain/refactor only for coder agents', () => {
    const forCoder = selectSuggestions({ ...coderCtx, hasGitRepo: false })
    expect(forCoder.map(s => s.id)).toContain('explain')
    expect(forCoder.map(s => s.id)).toContain('refactor')

    const forOther = selectSuggestions({ ...coderCtx, agentKind: 'researcher', hasGitRepo: false })
    expect(forOther.map(s => s.id)).not.toContain('explain')
    expect(forOther.map(s => s.id)).not.toContain('refactor')
  })

  it('excludes the 3 repo-review chips in home mode', () => {
    const home = selectSuggestions({ ...coderCtx, isHomeMode: true })
    const ids = home.map(s => s.id)
    expect(ids).not.toContain('reviewRepo')
    expect(ids).not.toContain('reviewCommit')
    expect(ids).not.toContain('nextStep')
  })

  it('preserves display order', () => {
    const ids = selectSuggestions(coderCtx).map(s => s.id)
    expect(ids).toEqual(['reviewRepo', 'reviewCommit', 'nextStep', 'explain', 'refactor'])
  })

  it('renders favorited prompts first, ahead of the static chips', () => {
    const picks = selectSuggestions({ ...coderCtx, favoritePrompts: ['  fix the login bug  ', 'deploy staging'] })
    expect(picks[0]).toMatchObject({ id: 'favorite:0', text: 'fix the login bug' })
    expect(picks[1]).toMatchObject({ id: 'favorite:1', text: 'deploy staging' })
    expect(picks.slice(2).map(s => s.id)).toEqual(['reviewRepo', 'reviewCommit', 'nextStep', 'explain', 'refactor'])
  })

  it('caps favorite chips and drops blank texts', () => {
    const picks = selectSuggestions({ ...coderCtx, favoritePrompts: ['a', '  ', '', 'b', 'c', 'd'] })
    const favorites = picks.filter(s => s.id.startsWith('favorite:'))
    expect(favorites.map(s => s.text)).toEqual(['a', 'b', 'c'])
    expect(favorites.length).toBe(MAX_FAVORITE_CHIPS)
  })

  it('keeps favorites in home mode', () => {
    const picks = selectSuggestions({ ...coderCtx, isHomeMode: true, favoritePrompts: ['hello'] })
    expect(picks.map(s => s.id)).toEqual(['favorite:0', 'explain', 'refactor'])
  })

  it('registry entries expose non-empty i18n labelKeys', () => {
    for (const s of SUGGESTIONS) {
      expect(s.labelKey.length).toBeGreaterThan(0)
      expect(s.labelKey.startsWith('agent.welcome.')).toBe(true)
    }
  })
})
