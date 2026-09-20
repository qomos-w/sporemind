/**
 * Context-driven suggestion chips.
 *
 * The suggestion set is a pure, declarative registry: each entry carries a
 * predicate over SuggestionContext, so surfaces (project new-chat page, agent
 * empty-conversation welcome row) render exactly the chips that fit the
 * current state. Display order is the array order.
 *
 * On top of the registry, the user's favorited composer history entries
 * (ctx.favoritePrompts) are rendered as raw-text chips FIRST — a favorited
 * message outranks every static suggestion.
 */
export interface SuggestionContext {
  hasProject: boolean
  isHomeMode: boolean
  hasGitRepo?: boolean
  agentKind?: string
  mountedModeCardIds?: string[]
  mountedBundleIds?: string[]
  permissionMode?: string
  /** User-favorited composer history texts, most recent first. */
  favoritePrompts?: string[]
}

export interface Suggestion {
  id: string
  /** i18n key; t(labelKey) is BOTH the chip label AND the inserted prompt.
   *  Unused when text is set. */
  labelKey: string
  /** Raw prompt text from a user-favorited history entry: chip label and
   *  inserted prompt, verbatim (no i18n). */
  text?: string
  predicate: (ctx: SuggestionContext) => boolean
}

export const SUGGESTIONS: Suggestion[] = [
  {
    id: 'reviewRepo',
    labelKey: 'agent.welcome.reviewRepo',
    predicate: c => c.hasProject && !c.isHomeMode,
  },
  {
    id: 'reviewCommit',
    labelKey: 'agent.welcome.reviewCommit',
    predicate: c => c.hasProject && !c.isHomeMode && !!c.hasGitRepo,
  },
  {
    id: 'nextStep',
    labelKey: 'agent.welcome.nextStep',
    predicate: c => c.hasProject && !c.isHomeMode,
  },
  {
    id: 'explain',
    labelKey: 'agent.welcome.explain',
    predicate: c => c.agentKind === 'coder',
  },
  {
    id: 'refactor',
    labelKey: 'agent.welcome.refactor',
    predicate: c => c.agentKind === 'coder',
  },
]

/** Cap on favorite chips so a heavily-favorited history cannot flood the row. */
export const MAX_FAVORITE_CHIPS = 3

export function selectSuggestions(ctx: SuggestionContext): Suggestion[] {
  const favorites: Suggestion[] = (ctx.favoritePrompts ?? [])
    .map(text => text.trim())
    .filter(text => text.length > 0)
    .slice(0, MAX_FAVORITE_CHIPS)
    .map((text, i) => ({ id: `favorite:${i}`, labelKey: '', text, predicate: () => true }))
  return [...favorites, ...SUGGESTIONS.filter(s => s.predicate(ctx))]
}
