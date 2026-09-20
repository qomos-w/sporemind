/**
 * Pure display logic for the knowledge-base swimlane ([[kb-swimlane]]).
 *
 * The lane renders the project's card stack: every card of the toc subtree,
 * one row each, in tree order (children directly below their parent, indented
 * by depth). Fold state is modelled as a *collapsed-id set* — a collapsed card
 * shows only its summary line — with accordion normalization so at most one
 * card body is expanded at a time:
 *
 * 1. default fold set — every card starts collapsed;
 * 2. accordion normalization — at most one card may be expanded at a time;
 * 3. focus expansion — opening a card (left-tree click) expands exactly it.
 *
 * No React, no DOM, no i18n: these are pure transformations and are covered by
 * `knowledgeSwimlane.logic.test.ts` per the CLAUDE.md testing-boundary clause.
 */

import type { MonoCardListItem } from '../../../domain/mono-types'

/** A card in the swimlane: a card list item plus its hydrated Markdown body. */
export interface SwimlaneCard extends MonoCardListItem {
  /** Raw Markdown body (after frontmatter). Absent when the card is not hydrated. */
  body?: string
}

/**
 * Ordered ids of every card in the lane. Duplicate ids are dropped so the lane
 * never renders (or tracks fold state for) the same card twice.
 */
export function swimlaneCardIds(cards: readonly SwimlaneCard[]): string[] {
  const ids: string[] = []
  const seen = new Set<string>()
  for (const card of cards) {
    if (seen.has(card.id)) continue
    seen.add(card.id)
    ids.push(card.id)
  }
  return ids
}

/**
 * Default collapsed-id set for a lane: every card starts collapsed. Ids are
 * de-duplicated through {@link swimlaneCardIds}.
 */
export function defaultCollapsedIds(cards: readonly SwimlaneCard[]): Set<string> {
  return new Set(swimlaneCardIds(cards))
}

/**
 * Accordion normalization: ensure at most one card is expanded (i.e. absent from
 * the collapsed set), and drop ids that are no longer in the lane.
 *
 * When several cards are expanded, the first one in lane order wins and every
 * later one is collapsed. When none is expanded the set is left untouched (all
 * cards collapsed is a valid state).
 */
export function normalizeAccordion(collapsed: ReadonlySet<string>, orderedIds: readonly string[]): Set<string> {
  const next = new Set<string>()
  let expandedSeen = false
  for (const id of orderedIds) {
    if (collapsed.has(id)) {
      next.add(id)
    } else if (!expandedSeen) {
      expandedSeen = true // keep the first expanded card
    } else {
      next.add(id) // collapse any additional expanded card
    }
  }
  return next
}

/**
 * Explicitly set one card's fold state (used by the 折叠/展开 buttons).
 * Expanding a card collapses every other card (accordion); collapsing a card
 * leaves the set otherwise untouched. Unknown ids are ignored.
 */
export function setCardCollapsed(
  collapsed: ReadonlySet<string>,
  orderedIds: readonly string[],
  id: string,
  nextCollapsed: boolean,
): Set<string> {
  if (!orderedIds.includes(id)) return new Set(collapsed)
  if (nextCollapsed) {
    const next = new Set(collapsed)
    next.add(id)
    return next
  }
  return new Set(orderedIds.filter(x => x !== id))
}

/** Toggle a card's fold state with accordion semantics. */
export function toggleCollapsed(
  collapsed: ReadonlySet<string>,
  orderedIds: readonly string[],
  id: string,
): Set<string> {
  return setCardCollapsed(collapsed, orderedIds, id, !collapsed.has(id))
}

/** True when the given card is collapsed. */
export function isCardCollapsed(collapsed: ReadonlySet<string>, id: string): boolean {
  return collapsed.has(id)
}

/**
 * First meaningful line of a card body, used as the collapsed summary row.
 * Strips leading heading/list/quote markers, emphasis and link syntax so the
 * summary reads as plain text.
 */
export function summarizeBody(body: string | undefined): string {
  if (!body) return ''
  for (const rawLine of body.split(/\r?\n/)) {
    const line = rawLine
      .replace(/^\s*#{1,6}\s+/, '')
      .replace(/^\s*[>\-*+]\s+/, '')
      .replace(/^\s*\d+\.\s+/, '')
      .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/[*_`~]/g, '')
      .trim()
    if (line) return line
  }
  return ''
}
