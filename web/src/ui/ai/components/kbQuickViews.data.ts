/**
 * Default data channel for the knowledge-base quick views ([[kb-quick-views]]).
 *
 * The views are deliberately data-injected: each accepts a
 * {@link KbQuickViewsDataSource} prop and this module supplies the production
 * implementation, so the components stay controlled (navigation and every
 * network effect flow through an explicit prop) while still being independently
 * testable with a stub source — no generated-client mocking required.
 *
 * Cross-project reads reuse the shared channel from `../kb/kbData` (the util
 * extracted for [[kb-left-outline]]): project enumeration, the
 * `workspace.wiki_list_starred` aggregation added by [[kb-star-backend]], and
 * the per-project `target`-routed content search. "Recently opened" is the only
 * cross-project read with no backend aggregate, so it fans out
 * `project.wiki_get_open_cards` here and merges the per-project lists
 * round-robin.
 */

import { client } from '../../../application/generated-client'
import * as project from '../../../gen-clients/project/client'
import type * as systemTypes from '../../../gen-clients/system/types'
import {
  listKbProjects,
  listKbStarred,
  searchKbAcrossProjects,
  setCardStarred,
} from '../kb/kbData'
import {
  interleaveRecentRefs,
  makeCardRef,
  sortProjectsByRecent,
  type KbCardRef,
  type KbProjectRef,
  type KbSearchHit,
} from './kbQuickViews.logic'

/**
 * Injectable data source for the three quick views. Every method is async so a
 * stub can model latency; failures reject and are rendered as an error state.
 */
export interface KbQuickViewsDataSource {
  /** Enumerate every mounted project (workspace.list_project). */
  listProjects(): Promise<KbProjectRef[]>
  /** All starred cards across projects (workspace.wiki_list_starred). */
  listStarred(): Promise<KbCardRef[]>
  /** Recently opened cards across projects, merged and capped at `max`. */
  listRecent(projects: readonly KbProjectRef[], max: number): Promise<KbCardRef[]>
  /** Cross-project content search (project.wiki_search_card_content fan-out). */
  search(
    query: string,
    projects: readonly KbProjectRef[],
    limitPerProject?: number,
  ): Promise<KbSearchHit[]>
  /** Star or un-star one card; resolves once the project actor persisted it. */
  setStarred(projectId: string, cardId: string, starred: boolean): Promise<void>
}

const DEFAULT_RECENT_MAX = 8
const DEFAULT_SEARCH_LIMIT = 20

/** Production data source backed by the shared `kbData` channel. */
export const defaultKbQuickViewsDataSource: KbQuickViewsDataSource = {
  async listProjects(): Promise<KbProjectRef[]> {
    const projects = await listKbProjects()
    return projects
      .filter((p): p is typeof p & { ActorId: string } => !!p.ActorId)
      .map(p => ({ projectId: p.ActorId, projectName: p.Name, lastOpenedAt: p.LastOpenedAt }))
  },

  async listStarred(): Promise<KbCardRef[]> {
    const entries = await listKbStarred()
    return entries.map(e => makeCardRef(e.ProjectID, e.ProjectName, e.CardID))
  },

  async listRecent(projects: readonly KbProjectRef[], max: number): Promise<KbCardRef[]> {
    const limit = max > 0 ? max : DEFAULT_RECENT_MAX
    const ordered = sortProjectsByRecent(projects)
    const settled = await Promise.allSettled(
      ordered.map(p =>
        project.wikiGetOpenCards(client, {}, { target: p.projectId }).then(resp =>
          (resp.OpenCards ?? []).map(cardId => makeCardRef(p.projectId, p.projectName, cardId)),
        ),
      ),
    )
    const groups: KbCardRef[][] = []
    for (const result of settled) {
      if (result.status === 'fulfilled') groups.push(result.value)
    }
    return interleaveRecentRefs(groups, limit)
  },

  async search(
    query: string,
    projects: readonly KbProjectRef[],
    limitPerProject = DEFAULT_SEARCH_LIMIT,
  ): Promise<KbSearchHit[]> {
    // The shared search util routes by ProjectRef.ActorId/Name; Path/Root are
    // required by the generated type but unused for routing.
    const refs: systemTypes.ProjectRef[] = projects.map(p => ({
      Name: p.projectName,
      Path: '',
      Root: false,
      ActorId: p.projectId,
    }))
    const hits = await searchKbAcrossProjects(query, refs, limitPerProject)
    return hits.map(h => ({
      projectId: h.projectId,
      projectName: h.projectName,
      cardId: h.card.Id,
      line: h.line,
      snippet: h.snippet,
    }))
  },

  async setStarred(projectId: string, cardId: string, starred: boolean): Promise<void> {
    await setCardStarred(projectId, cardId, starred)
  },
}
