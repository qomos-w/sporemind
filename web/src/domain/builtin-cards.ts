import type { I18nKey } from '../i18n/types'
import { cardIdFromTitle } from './mono-types'

export const BUILTIN_PREFIX = '__builtin_'
export const BUILTIN_SUFFIX = '__'

export function isBuiltinCard(id: string | null | undefined): boolean {
  return typeof id === 'string' && id.startsWith(BUILTIN_PREFIX) && id.endsWith(BUILTIN_SUFFIX)
}

export function getBuiltinCanonical(id: string): string | null {
  if (!isBuiltinCard(id)) return null
  return id.slice(BUILTIN_PREFIX.length, id.length - BUILTIN_SUFFIX.length)
}

export function makeBuiltinId(name: string): string {
  if (isBuiltinCard(name)) return name
  const def = BUILTIN_CARD_DEFS.find(d => d.canonical === name)
  if (def) return def.id
  return `${BUILTIN_PREFIX}${name}${BUILTIN_SUFFIX}`
}

interface BuiltinCardDef {
  id: string
  canonical: string
  i18nKey: I18nKey
  /** Whether this canonical also serves as a user-facing tag (isBuiltinTag).
   *  Mount/status nodes are display-only and must not pollute tag resolution. */
  tag: boolean
}

/** Builtin card registry: id, short canonical tag, and localized display name.
 *  The special root TOC card uses the unprefixed id "toc" because it is an
 *  ordinary top-level card, not a __builtin_* virtual node. */
export const BUILTIN_CARD_DEFS: ReadonlyArray<BuiltinCardDef> = [
  { id: 'toc', canonical: 'toc', i18nKey: 'shell.sidebar.cards.toc' as I18nKey, tag: true },
  { id: '__builtin_skill__', canonical: 'skill', i18nKey: 'shell.sidebar.cards.skill' as I18nKey, tag: true },
  { id: '__builtin_plugin__', canonical: 'plugin', i18nKey: 'shell.sidebar.cards.plugin' as I18nKey, tag: true },
  { id: '__builtin_agent__', canonical: 'agent', i18nKey: 'shell.sidebar.cards.agent' as I18nKey, tag: true },
  { id: '__builtin_component__', canonical: 'component', i18nKey: 'shell.sidebar.cards.component' as I18nKey, tag: true },
  { id: '__builtin_prompt__', canonical: 'prompt', i18nKey: 'shell.sidebar.cards.prompt' as I18nKey, tag: true },
  { id: '__builtin_bundle__', canonical: 'bundle', i18nKey: 'shell.sidebar.cards.bundle' as I18nKey, tag: true },
  // Task aggregator and per-status virtual nodes — display-only, not tags.
  { id: '__builtin_task__', canonical: 'task', i18nKey: 'shell.sidebar.cards.task' as I18nKey, tag: false },
  { id: '__builtin_backlog__', canonical: 'backlog', i18nKey: 'shell.sidebar.cards.backlog' as I18nKey, tag: false },
  { id: '__builtin_todo__', canonical: 'todo', i18nKey: 'shell.sidebar.cards.todo' as I18nKey, tag: false },
  { id: '__builtin_doing__', canonical: 'doing', i18nKey: 'shell.sidebar.cards.doing' as I18nKey, tag: false },
  { id: '__builtin_pending_review__', canonical: 'pending_review', i18nKey: 'shell.sidebar.cards.pendingReview' as I18nKey, tag: false },
  { id: '__builtin_done__', canonical: 'done', i18nKey: 'shell.sidebar.cards.done' as I18nKey, tag: false },
  { id: '__builtin_blocked__', canonical: 'blocked', i18nKey: 'shell.sidebar.cards.blocked' as I18nKey, tag: false },
  { id: '__builtin_cancelled__', canonical: 'cancelled', i18nKey: 'shell.sidebar.cards.cancelled' as I18nKey, tag: false },
  // Type-driven mount nodes — display-only, not tags.
  { id: '__builtin_concept__', canonical: 'concept', i18nKey: 'shell.sidebar.cards.concept' as I18nKey, tag: false },
  { id: '__builtin_callable__', canonical: 'callable', i18nKey: 'shell.sidebar.cards.callable' as I18nKey, tag: false },
  { id: '__builtin_capability_module__', canonical: 'capability_module', i18nKey: 'shell.sidebar.cards.capabilityModule' as I18nKey, tag: false },
  { id: '__builtin_scheduler__', canonical: 'scheduler', i18nKey: 'shell.sidebar.cards.scheduler' as I18nKey, tag: false },
  { id: '__builtin_workflow__', canonical: 'workflow', i18nKey: 'shell.sidebar.cards.workflow' as I18nKey, tag: false },
]

/** Tag-type builtins: short canonical names used in card tags. */
export const BUILTIN_TAG_DEFS: ReadonlyArray<{ canonical: string; i18nKey: I18nKey }> =
  BUILTIN_CARD_DEFS.filter(d => d.tag).map(d => ({ canonical: d.canonical, i18nKey: d.i18nKey }))

export const BUILTIN_TAG_CANONICALS: readonly string[] = BUILTIN_TAG_DEFS.map(d => d.canonical)

export function isBuiltinTag(tag: string): boolean {
  return BUILTIN_TAG_CANONICALS.includes(tag)
}

/** Map a localized display name, canonical tag, or full builtin id to the full builtin id. */
export function resolveBuiltinAlias(input: string, t: (key: I18nKey) => string): string | null {
  const lower = input.trim().toLowerCase()
  for (const def of BUILTIN_CARD_DEFS) {
    if (def.id === lower) return def.id
    if (def.canonical === lower) return def.id
    if (t(def.i18nKey).trim().toLowerCase() === lower) return def.id
  }
  return null
}

export function getBuiltinTagDisplayName(tag: string, t: (key: I18nKey) => string): string {
  const def = BUILTIN_TAG_DEFS.find(d => d.canonical === tag)
  return def ? t(def.i18nKey) : tag
}

/** Unified tag-to-card matching: checks id and builtin canonical name only. */
export function tagMatchesCard(tag: string, card: { id: string }): boolean {
  if (card.id === tag) return true
  if (isBuiltinCard(card.id) && getBuiltinCanonical(card.id) === tag) return true
  return false
}

/** Override display title for builtin cards with the localized i18n name. */
export function overrideBuiltinTitle<T extends { id: string }>(card: T, t: (key: I18nKey) => string): T & { title?: string } {
  if (!isBuiltinCard(card.id)) return card as T & { title?: string }
  const def = BUILTIN_CARD_DEFS.find(d => d.id === card.id)
  return def ? { ...card, title: t(def.i18nKey) } : (card as T & { title?: string })
}

export interface CardListItemLike {
  id: string
}

/**
 * Resolve a user-provided card reference (title, wiki word, or builtin alias) to a stable title.
 *
 * Resolution order:
 * 1. exact title match
 * 2. builtin id or builtin alias/localized display name
 * 3. title slug match (so CamelCase links resolve to cards created with that title)
 */
export function resolveCardId(
  input: string,
  cards: readonly CardListItemLike[],
  t: (key: I18nKey) => string,
): string | null {
  const trimmed = input.trim()
  if (!trimmed) return null
  if (cards.some(c => c.id === trimmed)) return trimmed
  if (isBuiltinCard(trimmed)) return trimmed
  const builtinId = resolveBuiltinAlias(trimmed, t)
  if (builtinId) return builtinId
  const byId = cards.find(c => c.id === trimmed)
  if (byId) return byId.id
  const slug = cardIdFromTitle(trimmed)
  if (slug && slug !== 'untitled') {
    const bySlug = cards.find(c => cardIdFromTitle(c.id || '') === slug)
    if (bySlug) return bySlug.id
  }
  return null
}
