import type { MonoCardListItem } from '../../../domain/mono-types'
import { deriveMountSpecs, isVirtualMountNode, mountTargetForCard } from '../../../domain/mono-types'
import { resolveStatusAliases } from '../../../domain/task-status'
import { isBuiltinCard, tagMatchesCard } from '../../../domain/builtin-cards'

/** Resolve a card's tags to parent card ids using the same id / builtin-canonical
 *  matching rules as the knowledge base (`tagMatchesCard`). This is the single
 *  source of truth for tag-derived card relationships, shared by the topology
 *  edge builder (`buildTopologyEdges`) and the subtree / lineage filters so the
 *  rendered graph and the filters agree on what counts as a parent.
 *
 *  Tags that don't resolve to any card in `cards`, or that resolve back to the
 *  card itself, are ignored. Multiple tags resolving to the same parent are
 *  de-duplicated. */
export function tagParentIds(card: MonoCardListItem, cards: readonly MonoCardListItem[]): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const tag of card.tags ?? []) {
    const parent = cards.find(candidate => tagMatchesCard(tag, candidate))
    if (!parent) continue
    if (parent.id === card.id) continue
    if (seen.has(parent.id)) continue
    seen.add(parent.id)
    result.push(parent.id)
  }
  return result
}

/** Resolve a card's type-driven virtual mount parent, if any. Mirrors the
 *  backend `applyTypeMounting` and the type edges in `buildTopologyEdges`. */
export function typeParentId(card: MonoCardListItem, cards: readonly MonoCardListItem[]): string | undefined {
  const specs = deriveMountSpecs(cards)
  const aliases = resolveStatusAliases(cards)
  const target = mountTargetForCard(card.type, card.status, specs, aliases)
  if (!target || target === card.id || isVirtualMountNode(card.id)) return undefined
  return target
}

function nonBuiltinTagParentIds(card: MonoCardListItem, cards: readonly MonoCardListItem[]): string[] {
  return tagParentIds(card, cards).filter(id => !isVirtualMountNode(id) && !isBuiltinCard(id))
}

/** Build a child map that combines all relationship sources a card can have:
 *  explicit `list`, explicit `parent`, type-driven virtual mount, and tag-derived
 *  parents. Tag-derived relationships to builtin virtual mount nodes are excluded
 *  because type-driven mounting already produces those edges; this keeps the map
 *  consistent with the rendered graph. */
export function buildChildMap(cards: MonoCardListItem[]): Map<string, Set<string>> {
  const map = new Map<string, Set<string>>()
  const ensure = (id: string): Set<string> => {
    let s = map.get(id)
    if (!s) { s = new Set(); map.set(id, s) }
    return s
  }
  for (const c of cards) {
    ensure(c.id)
    for (const childId of c.list ?? []) {
      ensure(c.id).add(childId)
      ensure(childId)
    }
    if (c.parent) ensure(c.parent).add(c.id)
    const typeParent = typeParentId(c, cards)
    if (typeParent) ensure(typeParent).add(c.id)
    for (const parentId of nonBuiltinTagParentIds(c, cards)) ensure(parentId).add(c.id)
  }
  return map
}

/** Inverse of `buildChildMap`: every parent of a card (explicit `list` reverse,
 *  explicit `parent`, type mount, and non-builtin tag-derived parents). The
 *  `list` reverse must be derived here because a child may declare no `parent`
 *  of its own; without it the ancestor walk (lineage filter) breaks for any
 *  relationship expressed only via the parent's `list` field. */
export function buildParentMap(cards: MonoCardListItem[]): Map<string, Set<string>> {
  const map = new Map<string, Set<string>>()
  const ensure = (id: string): Set<string> => {
    let s = map.get(id)
    if (!s) { s = new Set(); map.set(id, s) }
    return s
  }
  for (const c of cards) {
    ensure(c.id)
    for (const childId of c.list ?? []) ensure(childId).add(c.id)
    if (c.parent) ensure(c.id).add(c.parent)
    const typeParent = typeParentId(c, cards)
    if (typeParent) ensure(c.id).add(typeParent)
    for (const parentId of nonBuiltinTagParentIds(c, cards)) ensure(c.id).add(parentId)
  }
  return map
}

export function collectDescendants(rootId: string, childMap: Map<string, Set<string>>): Set<string> {
  const result = new Set<string>()
  const stack = [rootId]
  const visited = new Set<string>()
  while (stack.length > 0) {
    const id = stack.pop()!
    if (visited.has(id)) continue
    visited.add(id)
    if (id !== rootId) result.add(id)
    for (const child of childMap.get(id) ?? []) {
      if (!visited.has(child)) stack.push(child)
    }
  }
  return result
}

export function collectAncestors(rootId: string, parentMap: Map<string, Set<string>>): Set<string> {
  const result = new Set<string>()
  const stack = [...(parentMap.get(rootId) ?? [])]
  const visited = new Set<string>([rootId])
  while (stack.length > 0) {
    const id = stack.pop()!
    if (visited.has(id)) continue
    visited.add(id)
    result.add(id)
    for (const parent of parentMap.get(id) ?? []) {
      if (!visited.has(parent)) stack.push(parent)
    }
  }
  return result
}

export function lineageSet(
  rootId: string,
  childMap: Map<string, Set<string>>,
  parentMap: Map<string, Set<string>>,
): Set<string> {
  const result = new Set<string>([rootId])
  for (const id of collectDescendants(rootId, childMap)) result.add(id)
  for (const id of collectAncestors(rootId, parentMap)) result.add(id)
  return result
}
