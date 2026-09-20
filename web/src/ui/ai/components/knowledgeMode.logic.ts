/**
 * Pure display logic for the integrated knowledge-base mode ([[kb-mode-integration]],
 * part of [[kb-two-column-refactor]]).
 *
 * The mode is a two-column container: the global left outline
 * ([[kb-left-outline]]) plus a right column that is the active project's card
 * stack ([[kb-swimlane]]) or one of the quick views ([[kb-quick-views]]).
 * This module holds the React-free pieces so they stay unit-testable per the
 * CLAUDE.md testing-boundary clause:
 *
 * 1. lane resolution — flatten the project's toc subtree into the display
 *    stack, mirroring the backend tree's mounting rules (list membership,
 *    parent edges, well-known and parentless-wiki default mounts under toc) so
 *    the right column shows exactly the cards the left tree shows;
 * 2. fold seeding — the default collapsed set plus the focus rule that expands
 *    the card the user navigated to;
 * 3. lane identity helpers used to guard durable-state writes.
 *
 * No I/O lives here; card loading is `kbData.loadProjectLaneCards` and durable
 * state is `application/workspace-ui-state`.
 */

import { isVirtualMountNode, normalizeMonoCardType } from '../../../domain/mono-types'
import {
  defaultCollapsedIds,
  setCardCollapsed,
  swimlaneCardIds,
  type SwimlaneCard,
} from './knowledgeSwimlane.logic'

/** The builtin wiki container every project's card tree is rooted at. It is a
 *  mount target, never rendered as a card itself. */
export const KB_PROJECT_ROOT_CARD_ID = 'toc'

/** Well-known card ids the backend mounts under toc when they have no explicit
 *  parent (mirror of the backend wellKnownCardParents table). */
const KB_WELL_KNOWN_TOC_CHILDREN = new Set(['project_info'])

/** A lane target: the project plus the focused card (absent = project top). */
export interface KbLaneTarget {
  projectId: string
  projectName: string
  cardId?: string | undefined
}

/** One card of the display stack, annotated with its tree depth (0 = directly
 *  under toc) for indentation. */
export interface LaneStackCard extends SwimlaneCard {
  depth: number
}

/** A resolved lane: the toc container plus its flattened, depth-annotated stack. */
export interface KbLaneCards {
  root: SwimlaneCard
  cards: LaneStackCard[]
}

/** Stable identity of a lane (project + focused card). */
export function laneKey(target: KbLaneTarget | null | undefined): string {
  if (!target) return ''
  return `${target.projectId}\u0000${target.cardId ?? ''}`
}

/** True when a stack candidate is a renderable card (not a virtual mount
 *  bucket, component/runtime card, or standalone card). */
export function isLaneChild(card: SwimlaneCard | undefined): card is SwimlaneCard {
  if (!card) return false
  if (isVirtualMountNode(card.id)) return false
  return !card.standalone && card.visibility !== 'component' && card.visibility !== 'runtime'
}

/**
 * Direct children of `parentId` under the same mounting semantics as the
 * backend tree index: the parent's `list` first, then parent-attached cards,
 * then (for toc) parentless wiki cards default-mounted under toc — including
 * the well-known cards. Children are then sorted by modified desc, mirroring
 * the backend's default sibling ordering.
 */
function laneChildIds(
  parentId: string,
  byId: Map<string, SwimlaneCard>,
  listedIds: Set<string>,
): string[] {
  const ids: string[] = [...(byId.get(parentId)?.list ?? [])]
  const seen = new Set(ids)
  const push = (id: string) => {
    if (!seen.has(id)) {
      seen.add(id)
      ids.push(id)
    }
  }
  for (const card of byId.values()) {
    if (card.parent === parentId) push(card.id)
  }
  if (parentId === KB_PROJECT_ROOT_CARD_ID) {
    for (const card of byId.values()) {
      if (card.id === KB_PROJECT_ROOT_CARD_ID) continue
      if (isVirtualMountNode(card.id) || card.id.startsWith('wiki:')) continue
      if (!KB_WELL_KNOWN_TOC_CHILDREN.has(card.id) && normalizeMonoCardType(card.type) !== 'wiki') continue
      if (card.parent || listedIds.has(card.id)) continue
      push(card.id)
    }
  }
  const renderable = ids.filter(id => isLaneChild(byId.get(id)))
  return renderable.sort((a, b) => {
    const ma = byId.get(a)?.modified ?? ''
    const mb = byId.get(b)?.modified ?? ''
    if (ma === mb) return 0
    return ma < mb ? 1 : -1 // modified desc, stable
  })
}

/**
 * Resolve the project lane from its hydrated cards: the toc container plus the
 * full subtree flattened depth-first (each level sorted by modified desc),
 * skipping non-renderable cards and guarding against cycles. Returns null when
 * the project has no `toc` container.
 */
export function resolveLaneCards(cards: readonly SwimlaneCard[]): KbLaneCards | null {
  const byId = new Map<string, SwimlaneCard>()
  for (const card of cards) byId.set(card.id, card)

  const root = byId.get(KB_PROJECT_ROOT_CARD_ID)
  if (!root) return null

  const listedIds = new Set<string>()
  for (const card of cards) {
    for (const id of card.list ?? []) listedIds.add(id)
  }

  const stack: LaneStackCard[] = []
  const visited = new Set<string>([root.id])
  const walk = (parentId: string, depth: number) => {
    for (const childId of laneChildIds(parentId, byId, listedIds)) {
      if (visited.has(childId)) continue
      visited.add(childId)
      const child = byId.get(childId)
      if (!child) continue
      stack.push({ ...child, depth })
      walk(childId, depth + 1)
    }
  }
  walk(root.id, 0)
  return { root, cards: stack }
}

/**
 * Default accordion fold set for a lane, optionally opening one card (the
 * navigation focus) immediately. Every card starts collapsed; the focused card
 * is expanded and, via accordion normalization, is the only expanded one.
 */
export function laneCollapsedIds(
  cards: readonly LaneStackCard[],
  expandId?: string | null,
): Set<string> {
  const base = defaultCollapsedIds(cards)
  if (!expandId) return base
  const ids = swimlaneCardIds(cards)
  if (!ids.includes(expandId)) return base
  return setCardCollapsed(base, ids, expandId, false)
}
