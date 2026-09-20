/**
 * Pure display/search logic for the global knowledge-base left outline
 * ([[kb-left-outline]], part of [[kb-two-column-refactor]]).
 *
 * The outline is not bound to the active project: it lists every project and,
 * lazily, each project's TOC tree. This module holds the React-free pieces so
 * they stay unit-testable per the CLAUDE.md testing-boundary clause:
 *
 * 1. TOC tree normalization — the per-project `project.wiki_list_cards`
 *    tree response returns exactly one root node (the builtin `toc`
 *    container). {@link outlineRootsFromTocNodes} unwraps it to its children
 *    and drops virtual mount nodes (`__builtin_*__` buckets such as
 *    todo/done/task), which are structural, not cards.
 * 2. Expansion helpers — immutable id-set toggles for the project expansion
 *    set (owned durably by the parent via actor-backed state).
 * 3. Cross-project search — a local filter over already-loaded outlines plus
 *    an aggregation seam that only hits the per-project search callable for
 *    projects whose TOC is not loaded yet (the concrete remote fan-out is
 *    `kbData.searchKbAcrossProjects`).
 *
 * Data access lives in `web/src/ui/ai/kb/kbData.ts`; nothing here performs I/O.
 */

import type * as systemTypes from '../../../gen-clients/system/types'

/** View selected by the outline's three top entries. The union is declared once
 *  in [[kb-quick-views]] (`kbQuickViews.logic`) and re-exported here so the
 *  outline keeps a stable import path without a second declaration. */
export type { KbQuickView } from './kbQuickViews.logic'

/** The builtin wiki root card every project's TOC tree is rooted at. */
export const KB_TOC_ROOT_ID = 'toc'

/** A project as the outline needs it: routing target + label. */
export interface KbOutlineProject {
  /** Routing target: the project actor id, used as the `{target}` header. */
  projectId: string
  /** Display label. */
  name: string
}

/** One normalized outline node (project TOC card). */
export interface KbOutlineNode {
  id: string
  title: string
  type: string
  status: string
  modified: string
  children: KbOutlineNode[]
}

/** A single search hit tagged with the project that owns the card. */
export interface KbOutlineSearchHit {
  projectId: string
  projectName: string
  cardId: string
  title: string
  /** `local` = matched already-loaded outline data; `remote` = per-project callable. */
  origin: 'local' | 'remote'
}

/** Already-loaded outline data for one project, fed to the local search filter. */
export interface KbLoadedOutline {
  projectId: string
  projectName: string
  nodes: readonly KbOutlineNode[]
}

/** Map a workspace project ref to the outline's project shape (null when unmounted). */
export function toKbOutlineProject(ref: systemTypes.ProjectRef): KbOutlineProject | null {
  const projectId = ref.ActorId ?? ''
  if (!projectId) return null
  return { projectId, name: ref.Name || projectId }
}

/** Virtual mount nodes (`__builtin_task__`, `__builtin_todo__`, …) are index
 *  buckets, not cards, and must not appear in the outline. */
export function isVirtualMountNode(id: string): boolean {
  return /^__builtin_.*__$/.test(id)
}

function normalizeNode(node: systemTypes.WikiCardTreeNode | undefined | null): KbOutlineNode | null {
  if (!node || !node.Id || isVirtualMountNode(node.Id)) return null
  const children = (node.Children ?? [])
    .map(normalizeNode)
    .filter((child): child is KbOutlineNode => child !== null)
  return {
    id: node.Id,
    title: node.Title || node.Id,
    type: node.Type || '',
    status: node.Status || '',
    modified: node.Modified || '',
    children,
  }
}

/**
 * Visible outline roots for one project's TOC tree response.
 *
 * `project.wiki_list_cards {RootId:'toc'}` returns exactly one root node — the
 * `toc` container. Its children are the first visible outline entries, so the
 * container itself is unwrapped. A response that already carries the entries
 * (no `toc` root) is normalized as-is.
 */
export function outlineRootsFromTocNodes(nodes: readonly systemTypes.WikiCardTreeNode[]): KbOutlineNode[] {
  const roots = (nodes ?? [])
    .map(normalizeNode)
    .filter((node): node is KbOutlineNode => node !== null)
  const first = roots[0]
  if (roots.length === 1 && first && first.id === KB_TOC_ROOT_ID) {
    return first.children
  }
  return roots
}

/** Depth-first flatten of a normalized outline tree. */
export function flattenOutlineNodes(nodes: readonly KbOutlineNode[]): KbOutlineNode[] {
  const out: KbOutlineNode[] = []
  const walk = (list: readonly KbOutlineNode[]) => {
    for (const node of list) {
      out.push(node)
      walk(node.children)
    }
  }
  walk(nodes)
  return out
}

/** Immutable toggle of an id's membership in a set. */
export function toggleId(set: ReadonlySet<string>, id: string): Set<string> {
  const next = new Set(set)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  return next
}

/** Immutable explicit set/unset of an id in a set. */
export function setExpanded(set: ReadonlySet<string>, id: string, expanded: boolean): Set<string> {
  const next = new Set(set)
  if (expanded) next.add(id)
  else next.delete(id)
  return next
}

/** Case-insensitive match of a node's title or id against a query. */
export function nodeMatchesQuery(node: KbOutlineNode, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return node.title.toLowerCase().includes(q) || node.id.toLowerCase().includes(q)
}

/**
 * Local tree filter: keeps a node when it matches, or when any descendant
 * matches (matching subtrees are retained so context stays visible).
 */
export function filterOutlineNodes(nodes: readonly KbOutlineNode[], query: string): KbOutlineNode[] {
  if (!query.trim()) return [...nodes]
  const result: KbOutlineNode[] = []
  for (const node of nodes) {
    const children = filterOutlineNodes(node.children, query)
    if (nodeMatchesQuery(node, query) || children.length > 0) {
      result.push({ ...node, children })
    }
  }
  return result
}

/** Flat local search over already-loaded project outlines. */
export function searchLoadedOutlines(
  query: string,
  loaded: readonly KbLoadedOutline[],
): KbOutlineSearchHit[] {
  if (!query.trim()) return []
  const hits: KbOutlineSearchHit[] = []
  for (const entry of loaded) {
    for (const node of flattenOutlineNodes(entry.nodes)) {
      if (nodeMatchesQuery(node, query)) {
        hits.push({
          projectId: entry.projectId,
          projectName: entry.projectName,
          cardId: node.id,
          title: node.title,
          origin: 'local',
        })
      }
    }
  }
  return hits
}

/**
 * Cross-project search aggregation.
 *
 * Projects whose outline is already loaded are answered locally; only the
 * not-yet-loaded ones are delegated to `remoteSearch` (the concrete fan-out is
 * `kbData.searchKbAcrossProjects`, one `project.wiki_search_card_content` call
 * per project via the `{target}` routing header). A failing remote batch is
 * swallowed so local results still surface.
 */
export async function aggregateKbSearch(
  query: string,
  projects: readonly KbOutlineProject[],
  loadedByProject: Readonly<Record<string, readonly KbOutlineNode[] | undefined>>,
  remoteSearch: (
    query: string,
    projects: readonly KbOutlineProject[],
  ) => Promise<readonly KbOutlineSearchHit[]>,
): Promise<KbOutlineSearchHit[]> {
  const loaded: KbLoadedOutline[] = []
  const unloaded: KbOutlineProject[] = []
  for (const project of projects) {
    const nodes = loadedByProject[project.projectId]
    if (nodes === undefined) unloaded.push(project)
    else loaded.push({ projectId: project.projectId, projectName: project.name, nodes })
  }

  const localHits = searchLoadedOutlines(query, loaded)
  if (unloaded.length === 0) return localHits

  let remoteHits: readonly KbOutlineSearchHit[] = []
  try {
    remoteHits = await remoteSearch(query, unloaded)
  } catch {
    remoteHits = []
  }
  return [...localHits, ...remoteHits]
}
