/**
 * Integrated knowledge-base mode ([[kb-mode-integration]]).
 *
 * Replaces the old project-bound notes render (`MonoCard` + its
 * `MonoCardKnowledgeSidebar`). The mode is a two-column container that is NOT
 * bound to the active project:
 *
 * - left: the global outline ([[kb-left-outline]]) — every project + lazy TOC,
 *   home/search/starred entries;
 * - right: either the active project/card swimlane ([[kb-swimlane]]) or one of
 *   the quick views ([[kb-quick-views]]).
 *
 * Durable preferences (selected view, active lane, expanded project ids) live in
 * the workspace actor's UI state via `application/workspace-ui-state`
 * (`getKbNotesUIState` / `saveKbNotesUIState`) — never localStorage. Cross-project
 * reads go through `kb/kbData` with the per-project `{target}` routing header
 * ([[kb-cross-project-access-research]]).
 *
 * The component is still controllable: the parent passes an `openRequest` to
 * navigate into a target project/card (sidebar card click, right-panel "open in
 * notes", save-text-to-card), and every lane/view switch is mirrored to durable
 * state.
 */

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useI18n } from '../../../i18n'
import { KnowledgeLeftOutline } from './KnowledgeLeftOutline'
import { KnowledgeSwimlane } from './KnowledgeSwimlane'
import { KnowledgeHomeView, KnowledgeSearchView, KnowledgeStarredView } from './KnowledgeQuickViews'
import {
  toKbOutlineProject,
  type KbOutlineProject,
  type KbQuickView,
} from './knowledgeLeftOutline.logic'
import type { KbCardRef, KbProjectRef } from './kbQuickViews.logic'
import type { SwimlaneCard } from './knowledgeSwimlane.logic'
import type * as systemTypes from '../../../gen-clients/system/types'
import {
  laneKey,
  resolveLaneCards,
  laneCollapsedIds,
  type KbLaneTarget,
} from './knowledgeMode.logic'
import {
  listKbProjects,
  loadProjectToc,
  loadProjectLaneCards,
  listKbStarred,
  setCardStarred,
  createKbCard,
  type KbProject,
} from '../kb/kbData'
import {
  getKbNotesUIState,
  saveKbNotesUIState,
  type KbNotesLane,
} from '../../../application/workspace-ui-state'
import './KnowledgeModeView.css'

/**
 * A programmatic navigation request from the parent shell. `projectId` is the
 * project actor id; `cardId` may be omitted to open the project's own lane.
 * `key` must change for every request so repeated clicks re-fire navigation.
 */
export interface KnowledgeModeOpenRequest {
  key: number
  projectId?: string | null
  cardId?: string | null
}

export interface KnowledgeModeViewProps {
  /** Open a target lane; changing `key` re-fires navigation. */
  openRequest?: KnowledgeModeOpenRequest | null
  /**
   * Called once each time an `openRequest.key` has been applied, so the parent
   * can clear its stored request. Without this the last request would be
   * replayed whenever the mode remounts (mode tabs are not keep-alive),
   * overriding the durable lane the user picked afterwards.
   */
  onOpenRequestConsumed?: (key: number) => void
  /** Notifies the parent of the active lane (drives sidebar highlight). */
  onActiveLaneChange?: (lane: KbLaneTarget | null) => void
  /** Open a card in the right sidebar panel. */
  onOpenCardInRightPanel?: (cardId: string, projectId?: string | null) => void
  isMobile?: boolean
  className?: string
}

/** Map a workspace project ref to the outline's project shape. */
function outlineProjectsFromRefs(refs: readonly KbProject[]): KbOutlineProject[] {
  const out: KbOutlineProject[] = []
  for (const ref of refs) {
    const project = toKbOutlineProject(ref)
    if (project) out.push(project)
  }
  return out
}

/**
 * Display name for a lane. A stored name equal to the project id means "not
 * resolved yet" (an early navigation request landing before the project list),
 * so it is backfilled from the loaded projects instead of persisting a raw id.
 */
function laneDisplayName(
  projectId: string,
  storedName: string | undefined,
  projectList: readonly KbOutlineProject[],
): string {
  if (storedName && storedName !== projectId) return storedName
  return projectList.find(p => p.projectId === projectId)?.name || storedName || projectId
}

/** projectId → set of starred card ids (from the cross-project aggregate). */
function starIndex(entries: readonly { ProjectID: string; CardID: string }[]): Record<string, Set<string>> {
  const index: Record<string, Set<string>> = {}
  for (const entry of entries) {
    if (!entry.ProjectID || !entry.CardID) continue
    ;(index[entry.ProjectID] ??= new Set<string>()).add(entry.CardID)
  }
  return index
}

export const KnowledgeModeView: React.FC<KnowledgeModeViewProps> = ({
  openRequest,
  onOpenRequestConsumed,
  onActiveLaneChange,
  onOpenCardInRightPanel,
  isMobile = false,
  className,
}) => {
  const { t } = useI18n()

  const [projects, setProjects] = useState<KbOutlineProject[]>([])
  const [tocByProject, setTocByProject] = useState<Record<string, readonly systemTypes.WikiCardTreeNode[] | undefined>>({})
  const [loadingProjectIds, setLoadingProjectIds] = useState<Set<string>>(new Set())
  const [errorByProject, setErrorByProject] = useState<Record<string, string | undefined>>({})
  const [expandedProjectIds, setExpandedProjectIds] = useState<Set<string>>(new Set())

  const [view, setView] = useState<KbQuickView>('home')
  const [lane, setLane] = useState<KbLaneTarget | null>(null)

  const [laneCards, setLaneCards] = useState<Record<string, SwimlaneCard[] | undefined>>({})
  const [laneLoadingId, setLaneLoadingId] = useState<string | null>(null)
  const [laneErrorId, setLaneErrorId] = useState<string | null>(null)

  const [collapsedIds, setCollapsedIds] = useState<Set<string>>(new Set())
  const [starredByProject, setStarredByProject] = useState<Record<string, Set<string>>>({})
  // Bumped on every focus navigation so the swimlane re-scrolls even when the
  // focused card id itself did not change.
  const [focusKey, setFocusKey] = useState(0)

  const tocRequestedRef = useRef<Set<string>>(new Set())
  const laneRequestedRef = useRef<Set<string>>(new Set())
  const laneResetRef = useRef<string>('')
  const pendingExpandRef = useRef<string | null>(null)
  // Serialized-navigation bookkeeping: a parent request is held until the
  // initial durable/project load settles, then applied once (request wins over
  // the durable lane) and reported back as consumed.
  const durableLoadedRef = useRef(false)
  const pendingRequestRef = useRef<KnowledgeModeOpenRequest | null>(null)
  const consumedKeyRef = useRef<number | null>(null)
  const onOpenRequestConsumedRef = useRef(onOpenRequestConsumed)
  onOpenRequestConsumedRef.current = onOpenRequestConsumed

  /* ── data loading ── */

  const requestToc = useCallback((projectId: string) => {
    if (tocRequestedRef.current.has(projectId)) return
    tocRequestedRef.current.add(projectId)
    setLoadingProjectIds(prev => new Set(prev).add(projectId))
    void loadProjectToc(projectId)
      .then(nodes => {
        setTocByProject(prev => ({ ...prev, [projectId]: nodes }))
        setErrorByProject(prev => ({ ...prev, [projectId]: undefined }))
      })
      .catch(err => {
        // Allow a retry: re-expanding the project re-issues the load.
        tocRequestedRef.current.delete(projectId)
        setErrorByProject(prev => ({ ...prev, [projectId]: err instanceof Error ? err.message : String(err) }))
      })
      .finally(() => {
        setLoadingProjectIds(prev => {
          const next = new Set(prev)
          next.delete(projectId)
          return next
        })
      })
  }, [])

  const ensureLaneCards = useCallback((projectId: string) => {
    if (laneRequestedRef.current.has(projectId)) return
    laneRequestedRef.current.add(projectId)
    setLaneLoadingId(projectId)
    void loadProjectLaneCards(projectId)
      .then(cards => {
        setLaneCards(prev => ({ ...prev, [projectId]: cards }))
        setLaneErrorId(prev => (prev === projectId ? null : prev))
      })
      .catch(() => {
        // Allow a retry: re-entering the lane re-issues the load.
        laneRequestedRef.current.delete(projectId)
        setLaneErrorId(projectId)
      })
      .finally(() => setLaneLoadingId(prev => (prev === projectId ? null : prev)))
  }, [])

  const refreshStars = useCallback(() => {
    void listKbStarred()
      .then(entries => setStarredByProject(starIndex(entries)))
      .catch(() => {})
  }, [])

  // Force-refresh both per-project caches after a mutation (card creation).
  const reloadProjectToc = useCallback((projectId: string) => {
    tocRequestedRef.current.delete(projectId)
    requestToc(projectId)
  }, [requestToc])

  const reloadProjectLaneCards = useCallback((projectId: string) => {
    laneRequestedRef.current.delete(projectId)
    ensureLaneCards(projectId)
  }, [ensureLaneCards])

  /* ── navigation handlers ── */

  // Open the project's lane, optionally focusing one card: the lane always
  // shows the project's full card stack; the focused card is expanded (fold
  // seeding) and scrolled into view. `cardId` is the durable focus, never a
  // re-rooting — the stack stays put while navigation moves the focus.
  const openLane = useCallback((target: KbLaneTarget) => {
    pendingExpandRef.current = target.cardId ?? null
    setLane(target)
    setFocusKey(key => key + 1)
    ensureLaneCards(target.projectId)
    const durable: KbNotesLane = { projectId: target.projectId, projectName: target.projectName }
    if (target.cardId) durable.cardId = target.cardId
    void saveKbNotesUIState({ lane: durable })
  }, [ensureLaneCards])

  /**
   * Apply a parent navigation request. Runs only after the initial load settled
   * (see the initial-load effect), so the project list is available and the
   * durable lane is never written with a raw actor id as the display name.
   */
  const applyOpenRequest = useCallback((request: KnowledgeModeOpenRequest, projectList: readonly KbOutlineProject[]) => {
    const projectId = request.projectId
    if (!projectId) return
    openLane({ projectId, projectName: laneDisplayName(projectId, undefined, projectList), cardId: request.cardId ?? undefined })
  }, [openLane])

  const selectView = useCallback((next: KbQuickView) => {
    setView(next)
    setLane(null)
    void saveKbNotesUIState({ view: next, lane: null })
  }, [])

  const handleOpenProject = useCallback((project: KbOutlineProject) => {
    openLane({ projectId: project.projectId, projectName: project.name })
  }, [openLane])

  const handleOpenCard = useCallback((project: KbOutlineProject, cardId: string) => {
    openLane({ projectId: project.projectId, projectName: project.name, cardId })
  }, [openLane])

  const handleOpenQuickCard = useCallback((cardRef: KbCardRef) => {
    openLane({ projectId: cardRef.projectId, projectName: cardRef.projectName, cardId: cardRef.cardId })
  }, [openLane])

  const handleOpenQuickProject = useCallback((project: KbProjectRef) => {
    openLane({ projectId: project.projectId, projectName: project.projectName })
  }, [openLane])

  // Create a card under the project directory: parentless wiki card → the
  // backend mounts it under toc; both caches refresh and the new card is
  // focused in the stack. Failures surface in the outline's per-project error
  // slot (the same one TOC load errors use).
  const handleCreateCard = useCallback((project: KbOutlineProject, name: string) => {
    const trimmed = name.trim()
    if (!trimmed) return
    const loadExisting = laneCards[project.projectId] !== undefined
      ? Promise.resolve(laneCards[project.projectId]!.map(c => c.id))
      : loadProjectLaneCards(project.projectId).then(cards => {
          setLaneCards(prev => ({ ...prev, [project.projectId]: cards }))
          return cards.map(c => c.id)
        })
    void loadExisting
      .then(existing => createKbCard(project.projectId, trimmed, existing))
      .then(slug => {
        reloadProjectToc(project.projectId)
        reloadProjectLaneCards(project.projectId)
        openLane({ projectId: project.projectId, projectName: project.name, cardId: slug })
      })
      .catch(err => {
        setErrorByProject(prev => ({
          ...prev,
          [project.projectId]: err instanceof Error ? err.message : String(err),
        }))
      })
  }, [laneCards, reloadProjectToc, reloadProjectLaneCards, openLane])

  // The starred quick view un-stars a card itself; mirror the removal into the
  // global star index so the left outline's badge disappears immediately
  // (refreshStars would race the view's own optimistic update).
  const handleQuickUnstar = useCallback((cardRef: KbCardRef) => {
    setStarredByProject(prev => {
      const set = new Set(prev[cardRef.projectId] ?? [])
      set.delete(cardRef.cardId)
      return { ...prev, [cardRef.projectId]: set }
    })
  }, [])

  const handleExpandedChange = useCallback((next: Set<string>) => {
    setExpandedProjectIds(next)
    void saveKbNotesUIState({ expandedProjectIds: Array.from(next) })
  }, [])

  // Initial load: durable preferences + project list + global star index. The
  // lane is a single serialized decision — a pending navigation request wins
  // over the durable lane, so the two writers never race (the request used to be
  // applied first and then silently overwritten by the durable restore).
  useEffect(() => {
    let alive = true
    void (async () => {
      const [state, refs] = await Promise.all([
        getKbNotesUIState().catch(() => null),
        listKbProjects().catch(() => [] as KbProject[]),
      ])
      if (!alive) return
      const projectList = outlineProjectsFromRefs(refs)
      setProjects(projectList)
      refreshStars()
      durableLoadedRef.current = true
      if (state) {
        setView(state.view)
        setExpandedProjectIds(new Set(state.expandedProjectIds))
        for (const id of state.expandedProjectIds) requestToc(id)
      }
      const pending = pendingRequestRef.current
      if (pending && consumedKeyRef.current !== pending.key) {
        consumedKeyRef.current = pending.key
        pendingRequestRef.current = null
        applyOpenRequest(pending, projectList)
        onOpenRequestConsumedRef.current?.(pending.key)
        return
      }
      if (state?.lane) {
        setLane(state.lane)
        // Restore where the user was reading: the stored focus card is expanded
        // (fold seeding) instead of merely scrolled to.
        pendingExpandRef.current = state.lane.cardId ?? null
        ensureLaneCards(state.lane.projectId)
      }
    })()
    return () => { alive = false }
  }, [requestToc, refreshStars, ensureLaneCards, applyOpenRequest])

  // Consume a parent navigation request (sidebar / right panel / save-to-card).
  // It is held until the initial load settles, applied exactly once, and then
  // reported as consumed so the parent can clear its stored request — otherwise
  // remounting the mode (switching mode tabs is not keep-alive) would replay it.
  useEffect(() => {
    const projectId = openRequest?.projectId
    if (!openRequest || !projectId) return
    if (consumedKeyRef.current === openRequest.key) return
    pendingRequestRef.current = openRequest
    if (!durableLoadedRef.current) return
    consumedKeyRef.current = openRequest.key
    pendingRequestRef.current = null
    applyOpenRequest(openRequest, projects)
    onOpenRequestConsumedRef.current?.(openRequest.key)
  }, [openRequest, projects, applyOpenRequest])

  // Durable-lane display name fallback: an early request (or a legacy persisted
  // state) may carry the actor id as the project name. Once the project list is
  // known, backfill the real display name instead of persisting the raw id.
  useEffect(() => {
    if (!lane) return
    const resolved = laneDisplayName(lane.projectId, lane.projectName, projects)
    if (resolved === lane.projectName) return
    const next: KbLaneTarget = { projectId: lane.projectId, projectName: resolved }
    if (lane.cardId) next.cardId = lane.cardId
    setLane(next)
    const durable: KbNotesLane = { projectId: next.projectId, projectName: next.projectName }
    if (next.cardId) durable.cardId = next.cardId
    void saveKbNotesUIState({ lane: durable })
  }, [projects, lane])

  const isCardStarred = useCallback(
    (projectId: string, cardId: string) => starredByProject[projectId]?.has(cardId) ?? false,
    [starredByProject],
  )

  // Report the active lane to the parent (sidebar highlight / external readers).
  useEffect(() => {
    onActiveLaneChange?.(lane)
  }, [lane, onActiveLaneChange])

  const handleToggleStar = useCallback((projectId: string, cardId: string, starred: boolean) => {
    setStarredByProject(prev => {
      const set = new Set(prev[projectId] ?? [])
      if (starred) set.add(cardId)
      else set.delete(cardId)
      return { ...prev, [projectId]: set }
    })
    void setCardStarred(projectId, cardId, starred)
      .then(list => setStarredByProject(prev => ({ ...prev, [projectId]: new Set(list) })))
      .catch(() => {
        // Revert the optimistic toggle on failure.
        setStarredByProject(prev => {
          const set = new Set(prev[projectId] ?? [])
          if (starred) set.delete(cardId)
          else set.add(cardId)
          return { ...prev, [projectId]: set }
        })
      })
  }, [])

  /* ── lane resolution + fold seeding ── */

  const activeLaneCards = useMemo(
    () => (lane ? resolveLaneCards(laneCards[lane.projectId] ?? []) : null),
    [lane, laneCards],
  )

  useEffect(() => {
    if (!lane || !activeLaneCards) return
    const key = `${laneKey(lane)}::${activeLaneCards.cards.map(c => c.id).join(',')}`
    const expandId = pendingExpandRef.current
    // Same stack: keep the user's fold toggles, but still honour a fresh focus
    // seed — re-focusing the card that is already focused leaves the lane key
    // unchanged, yet must expand that card in place again.
    if (laneResetRef.current === key && expandId === null) return
    laneResetRef.current = key
    pendingExpandRef.current = null
    setCollapsedIds(laneCollapsedIds(activeLaneCards.cards, expandId))
  }, [lane, activeLaneCards])

  // Wikiword navigation moves the focus (expand + scroll) within the current
  // project's stack; unknown words are ignored.
  const handleWikiWord = useCallback((_cardId: string, word: string) => {
    const current = lane
    if (!current) return
    const cards = laneCards[current.projectId] ?? []
    if (!cards.some(card => card.id === word)) return
    openLane({ projectId: current.projectId, projectName: current.projectName, cardId: word })
  }, [lane, laneCards, openLane])

  const handleOpenInRightPanel = useCallback((cardId: string) => {
    onOpenCardInRightPanel?.(cardId, lane?.projectId)
  }, [onOpenCardInRightPanel, lane?.projectId])

  /* ── render ── */

  const laneLoading = lane !== null && laneCards[lane.projectId] === undefined && laneLoadingId === lane.projectId
  const laneErrored = lane !== null && laneCards[lane.projectId] === undefined && laneErrorId === lane.projectId

  const renderLane = () => {
    if (!lane) return null
    if (laneLoading) return <div className="kb-mode-placeholder">{t('knowledgeMode.lane.loading')}</div>
    if (laneErrored) return <div className="kb-mode-placeholder kb-mode-placeholder--error">{t('knowledgeMode.lane.error')}</div>
    if (!activeLaneCards) return <div className="kb-mode-placeholder">{t('knowledgeMode.lane.empty')}</div>
    return (
      <KnowledgeSwimlane
        cards={activeLaneCards.cards}
        focusCardId={lane.cardId ?? null}
        focusKey={focusKey}
        onWikiWord={handleWikiWord}
        collapsedIds={collapsedIds}
        onCollapsedIdsChange={setCollapsedIds}
        onOpenInRightPanel={handleOpenInRightPanel}
      />
    )
  }

  const renderQuickView = () => {
    switch (view) {
      case 'search':
        return <KnowledgeSearchView onOpenCard={handleOpenQuickCard} />
      case 'starred':
        return <KnowledgeStarredView onOpenCard={handleOpenQuickCard} onUnstar={handleQuickUnstar} />
      case 'home':
      default:
        return (
          <KnowledgeHomeView
            onOpenCard={handleOpenQuickCard}
            onOpenProject={handleOpenQuickProject}
            onSelectView={selectView}
          />
        )
    }
  }

  return (
    <div className={`kb-mode${isMobile ? ' kb-mode--mobile' : ''}${className ? ` ${className}` : ''}`}>
      <div className="kb-mode-outline">
        <KnowledgeLeftOutline
          projects={projects}
          activeView={view}
          onSelectView={selectView}
          expandedProjectIds={expandedProjectIds}
          onExpandedProjectIdsChange={handleExpandedChange}
          tocByProject={tocByProject}
          loadingProjectIds={loadingProjectIds}
          errorByProject={errorByProject}
          onRequestProjectToc={requestToc}
          activeProjectId={lane?.projectId ?? null}
          activeCardId={lane?.cardId ?? null}
          isCardStarred={isCardStarred}
          onOpenProject={handleOpenProject}
          onOpenCard={handleOpenCard}
          onCreateCard={handleCreateCard}
          onToggleStar={handleToggleStar}
        />
      </div>
      <div className="kb-mode-main">{lane ? renderLane() : renderQuickView()}</div>
    </div>
  )
}
