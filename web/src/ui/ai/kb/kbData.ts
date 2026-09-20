/**
 * Shared data channel for the global knowledge-base views ([[kb-two-column-refactor]]).
 *
 * The knowledge base is not bound to the active project: projects are enumerated
 * via workspace.list_project, and each project's wiki is read through the
 * per-project `target` routing header (same mechanism as mono-store's
 * `target()` / foreign-project calls). Star state lives in each project actor's
 * .ropen record and is aggregated by workspace.wiki_list_starred — one round
 * trip for the global starred view, no client-side fan-out.
 */

import { client } from '../../../application/generated-client'
import * as project from '../../../gen-clients/project/client'
import * as workspace from '../../../gen-clients/workspace/client'
import type * as systemTypes from '../../../gen-clients/system/types'
import { cardIdFromTitle, formatMonoCard, parseMonoCard, type MonoCard } from '../../../domain/mono-types'

export type KbProject = systemTypes.ProjectRef
export type KbCardTreeNode = systemTypes.WikiCardTreeNode
export type KbMonoCardListItem = systemTypes.MonoCardListItem
export type KbStarredEntry = systemTypes.WikiStarredEntry

/** One search hit, tagged with the project that owns the card. */
export interface KbSearchHit {
  projectId: string
  projectName: string
  card: systemTypes.MonoCardListItem
  line?: number | undefined
  snippet?: string | undefined
}

/**
 * Enumerate every mounted user project (workspace.list_project).
 *
 * The workspace's internal system meta project (`System: true`, the mount that
 * holds builtin cards) is filtered out: it must never render in any user-facing
 * project picker (same policy as `AIShellSidebar`, which is explicit that the
 * system meta project never renders). The filter lives here so the left
 * outline, the home view's project shortcuts and the cross-project search all
 * inherit it from the single enumeration point.
 */
export async function listKbProjects(): Promise<KbProject[]> {
  const resp = await workspace.listProject(client)
  return (resp.Items ?? []).filter(p => p.System !== true)
}

/**
 * Lazy-load one project's TOC tree (rooted at the builtin `toc` card).
 * Returns the structured node tree for the left-column outline.
 */
export async function loadProjectToc(projectId: string): Promise<KbCardTreeNode[]> {
  const resp = await project.wikiListCards(
    client,
    { RootId: 'toc' },
    { target: projectId },
  )
  return resp.Nodes ?? []
}

/**
 * Cross-project content search: fans out wiki_search_card_content to each
 * project (bounded per-project limit) and merges the hits, each tagged with
 * its owning project. Unreachable projects are skipped rather than failing
 * the whole query.
 */
export async function searchKbAcrossProjects(
  query: string,
  projects: readonly KbProject[],
  limitPerProject = 20,
): Promise<KbSearchHit[]> {
  const searchable = projects.filter(p => p.ActorId)
  const settled = await Promise.allSettled(
    searchable.map(p =>
      project.wikiSearchCardContent(
        client,
        { Query: query, Limit: limitPerProject },
        { target: p.ActorId! },
      ).then(resp => ({ project: p, resp })),
    ),
  )
  const hits: KbSearchHit[] = []
  for (const result of settled) {
    if (result.status !== 'fulfilled') continue
    for (const match of result.value.resp.Matches ?? []) {
      hits.push({
        projectId: result.value.project.ActorId!,
        projectName: result.value.project.Name,
        card: match.Card,
        line: match.Line,
        snippet: match.Snippet,
      })
    }
  }
  return hits
}

/** Global starred list across every mounted project (single round trip). */
export async function listKbStarred(): Promise<KbStarredEntry[]> {
  const resp = await workspace.wikiListStarred(client, {})
  return resp.Items ?? []
}

/** Starred card ids of one project, most-recently-starred first. */
export async function getProjectStarred(projectId: string): Promise<string[]> {
  const resp = await project.wikiGetStarred(client, {}, { target: projectId })
  return resp.Starred ?? []
}

/** Star or un-star one card in one project; returns the updated list. */
export async function setCardStarred(
  projectId: string,
  cardId: string,
  starred: boolean,
): Promise<string[]> {
  const resp = await project.wikiSetStarred(
    client,
    { Id: cardId, Starred: starred },
    { target: projectId },
  )
  return resp.Starred ?? []
}

/**
 * Every card of one project, hydrated with its Markdown body, for the right
 * column swimlane. One flat `project.wiki_list_cards` round trip per project
 * (IncludeRaw) via the `{target}` routing header — no dependence on the
 * singleton mono-store, whose caches are keyed to the active project.
 */
export async function loadProjectLaneCards(projectId: string): Promise<MonoCard[]> {
  const resp = await project.wikiListCards(
    client,
    { Flat: true, IncludeRaw: true, IncludeBuiltin: true, Limit: -1 },
    { target: projectId },
  )
  const cards: MonoCard[] = []
  for (const item of resp.Cards ?? []) {
    // parseMonoCard drops raw text with no frontmatter (empty meta), but the
    // backend lists such legacy notes (type defaults to wiki, mounted under
    // toc) — dropping them desynced the right column from the left tree, so a
    // click on them visibly did nothing. Fall back to the list item's own
    // metadata fields.
    const card = item.Raw === undefined
      ? monoCardFromListItem(item)
      : parseMonoCard(item.Id, item.Raw) ?? monoCardFromListItem(item)
    if (card) cards.push(card)
  }
  return cards
}

/** Build a MonoCard from a list item when its raw body carries no frontmatter. */
function monoCardFromListItem(item: KbMonoCardListItem): MonoCard {
  const body = parseMonoCardBodyFallback(item.Raw)
  return {
    id: item.Id,
    type: item.Type,
    source: item.Source,
    storage: item.Storage,
    visibility: item.Visibility,
    protected: item.Protected,
    editable: item.Editable,
    deletable: item.Deletable,
    tags: item.Tags ?? [],
    list: item.List ?? [],
    created: item.Created ?? '',
    modified: item.Modified,
    due: item.Due,
    priority: item.Priority as MonoCard['priority'],
    status: item.Status,
    parent: item.Parent,
    standalone: item.Standalone,
    data: item.Data,
    body,
    raw: item.Raw ?? '',
  }
}

/** Extract the body (frontmatter stripped when present) from a raw card. */
function parseMonoCardBodyFallback(raw: string | undefined): string {
  if (!raw) return ''
  const fm = raw.match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n?/)
  return fm ? raw.slice(fm[0].length) : raw
}

/**
 * Create a wiki card in one project from a display name. The card is
 * intentionally parentless: the backend default-mounts parentless wiki cards
 * under toc, so it lands in the project's knowledge base out of the box.
 * Returns the created card's slug id (de-duplicated against `existingIds`).
 */
export async function createKbCard(
  projectId: string,
  name: string,
  existingIds: readonly string[] = [],
): Promise<string> {
  const base = cardIdFromTitle(name)
  const taken = new Set(existingIds)
  let slug = base
  let suffix = 1
  while (taken.has(slug)) {
    slug = `${base}-${suffix}`
    suffix++
  }
  const now = new Date().toISOString()
  const raw = formatMonoCard({
    id: slug,
    type: 'wiki',
    tags: [],
    list: [],
    created: now,
    modified: now,
    body: '',
    raw: '',
  })
  await project.wikiCreateCard(client, { Id: slug, Raw: raw }, { target: projectId })
  return slug
}
