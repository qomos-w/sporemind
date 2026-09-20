/**
 * Pure display logic and view-switch protocol for the knowledge-base quick
 * views ([[kb-quick-views]]).
 *
 * The quick views are the right-column counterpart of a swimlane: instead of a
 * single project's card tree they surface cross-project aggregates — starred
 * cards, recently opened cards, project shortcuts and content search. Every
 * ordering / filtering / de-duplication rule lives here as a plain function (no
 * React, no RPC, no i18n) so it is unit-testable in isolation, mirroring
 * `knowledgeSwimlane.logic.ts` per the CLAUDE.md testing-boundary clause.
 */

/**
 * The three quick views. The left outline ([[kb-left-outline]]) owns the active
 * selection; the right column renders the matching component. The string values
 * are the shared protocol between both cards and [[kb-mode-integration]].
 */
export type KbQuickView = 'home' | 'search' | 'starred'

/** Ordered view ids, e.g. for a segmented switcher in the left outline. */
export const KB_QUICK_VIEWS: readonly KbQuickView[] = ['home', 'search', 'starred']

/** Narrow an unknown value to a {@link KbQuickView}. */
export function isKbQuickView(value: unknown): value is KbQuickView {
  return value === 'home' || value === 'search' || value === 'starred'
}

/** A card addressed together with the project that owns it (cross-project ref). */
export interface KbCardRef {
  projectId: string
  projectName: string
  cardId: string
}

/** A cross-project search hit: a card ref plus the matched line/snippet. */
export interface KbSearchHit extends KbCardRef {
  line?: number | undefined
  snippet?: string | undefined
}

/** A project shortcut entry shown on the home view. */
export interface KbProjectRef {
  projectId: string
  projectName: string
  lastOpenedAt?: string | undefined
}

/** Build a cross-project card ref. */
export function makeCardRef(projectId: string, projectName: string, cardId: string): KbCardRef {
  return { projectId, projectName, cardId }
}

/** Stable identity of a card ref: the card is unique within its owning project. */
export function cardRefKey(ref: KbCardRef): string {
  return `${ref.projectId}\u0000${ref.cardId}`
}

/** Drop duplicate refs (same project + card), keeping the first occurrence. */
export function dedupeCardRefs(refs: readonly KbCardRef[]): KbCardRef[] {
  const seen = new Set<string>()
  const out: KbCardRef[] = []
  for (const ref of refs) {
    const key = cardRefKey(ref)
    if (seen.has(key)) continue
    seen.add(key)
    out.push(ref)
  }
  return out
}

/**
 * Case-insensitive filter over the card id and its owning project name.
 * An empty / whitespace-only query returns every ref (in order).
 */
export function filterCardRefs(refs: readonly KbCardRef[], query: string): KbCardRef[] {
  const q = query.trim().toLowerCase()
  if (!q) return [...refs]
  return refs.filter(ref =>
    ref.cardId.toLowerCase().includes(q) || ref.projectName.toLowerCase().includes(q),
  )
}

/**
 * Most-recently-opened projects first. Projects without a `lastOpenedAt` sort
 * last; ties break by project name so the order is deterministic.
 */
export function sortProjectsByRecent(projects: readonly KbProjectRef[]): KbProjectRef[] {
  return [...projects].sort((a, b) => {
    const ta = a.lastOpenedAt ? Date.parse(a.lastOpenedAt) : Number.NaN
    const tb = b.lastOpenedAt ? Date.parse(b.lastOpenedAt) : Number.NaN
    const va = Number.isNaN(ta) ? 0 : ta
    const vb = Number.isNaN(tb) ? 0 : tb
    if (va !== vb) return vb - va
    return a.projectName.localeCompare(b.projectName)
  })
}

/**
 * Interleave per-project recent-card groups round-robin so the merged list
 * mixes projects instead of draining one project first. Duplicates (and a card
 * repeated across groups) are dropped; the result is capped at `max`.
 *
 * Each group is expected to be ordered most-recent-first within its project.
 */
export function interleaveRecentRefs(
  groups: readonly (readonly KbCardRef[])[],
  max: number,
): KbCardRef[] {
  if (max <= 0) return []
  const out: KbCardRef[] = []
  const seen = new Set<string>()
  let index = 0
  for (;;) {
    let progressed = false
    for (const group of groups) {
      const ref = group[index]
      if (!ref) continue
      progressed = true
      const key = cardRefKey(ref)
      if (seen.has(key)) continue
      seen.add(key)
      out.push(ref)
      if (out.length >= max) return out
    }
    if (!progressed) return out
    index++
  }
}
