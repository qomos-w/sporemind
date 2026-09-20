/**
 * Knowledge-base quick views ([[kb-quick-views]]): the three non-swimlane views
 * shown in the right column of the notes mode.
 *
 * - `KnowledgeHomeView`   — starred cards + recently opened cards + project shortcuts
 * - `KnowledgeStarredView`— every starred card across projects (project name shown)
 * - `KnowledgeSearchView` — cross-project content search with a result list
 *
 * Every view is controlled: navigation is reported through callbacks
 * (`onOpenCard` / `onOpenProject` / `onSelectView`) and the data channel is an
 * injectable {@link KbQuickViewsDataSource} prop defaulting to the shared
 * production source. That keeps the components free of global state and lets
 * them be rendered in isolation with a stub source.
 */

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Clock, Folder, Search, Star, X } from 'lucide-react'
import { useI18n } from '../../../i18n'
import {
  defaultKbQuickViewsDataSource,
  type KbQuickViewsDataSource,
} from './kbQuickViews.data'
import type { KbCardRef, KbProjectRef, KbSearchHit } from './kbQuickViews.logic'
import './kbQuickViews.css'

export type { KbQuickView, KbCardRef, KbProjectRef, KbSearchHit } from './kbQuickViews.logic'
export { KB_QUICK_VIEWS, isKbQuickView } from './kbQuickViews.logic'

/** Shared props for the three quick views. */
export interface KbQuickViewCommonProps {
  /** Data channel; defaults to the shared production source. */
  dataSource?: KbQuickViewsDataSource
  className?: string
}

/** A single cross-project card row: card title, owning project, optional snippet. */
const KbCardRow: React.FC<{
  cardRef: KbCardRef
  snippet?: string | undefined
  line?: number | undefined
  onOpen?: ((cardRef: KbCardRef) => void) | undefined
  trailing?: React.ReactNode
}> = ({ cardRef, snippet, line, onOpen, trailing }) => (
  <li className="kb-quick-item" data-card-id={cardRef.cardId} data-project-id={cardRef.projectId}>
    <button
      type="button"
      className="kb-quick-item-main"
      onClick={() => onOpen?.(cardRef)}
      disabled={!onOpen}
    >
      <span className="kb-quick-item-title">{cardRef.cardId}</span>
      <span className="kb-quick-item-project">{cardRef.projectName}</span>
      {snippet ? (
        <span className="kb-quick-item-snippet">
          {line ? `${line}: ` : ''}
          {snippet}
        </span>
      ) : null}
    </button>
    {trailing}
  </li>
)

/** Small empty/loading/error placeholder used inside a section list. */
const KbPlaceholder: React.FC<{ children: React.ReactNode }> = ({ children }) => (
  <div className="kb-quick-empty">{children}</div>
)

/* ─────────────────────────────── Home ─────────────────────────────── */

export interface KnowledgeHomeViewProps extends KbQuickViewCommonProps {
  /** Max starred cards shown before the "see all" affordance. */
  starredLimit?: number
  /** Max recently opened cards shown. */
  recentLimit?: number
  onOpenCard?: (cardRef: KbCardRef) => void
  onOpenProject?: (project: KbProjectRef) => void
  onSelectView?: (view: 'starred' | 'search') => void
}

/**
 * Home quick view: starred cards, recently opened cards and a project shortcut
 * list. Clicking a project opens that project's swimlane (via `onOpenProject`).
 */
export const KnowledgeHomeView: React.FC<KnowledgeHomeViewProps> = ({
  dataSource,
  className,
  starredLimit = 5,
  recentLimit = 6,
  onOpenCard,
  onOpenProject,
  onSelectView,
}) => {
  const { t } = useI18n()
  const ds = dataSource ?? defaultKbQuickViewsDataSource
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [projects, setProjects] = useState<KbProjectRef[]>([])
  const [starred, setStarred] = useState<KbCardRef[]>([])
  const [recent, setRecent] = useState<KbCardRef[]>([])

  useEffect(() => {
    let alive = true
    setLoading(true)
    setError(false)
    void (async () => {
      try {
        const nextProjects = await ds.listProjects()
        if (!alive) return
        setProjects(nextProjects)
        const [nextStarred, nextRecent] = await Promise.all([
          ds.listStarred(),
          ds.listRecent(nextProjects, recentLimit),
        ])
        if (!alive) return
        setStarred(nextStarred)
        setRecent(nextRecent)
      } catch {
        if (alive) setError(true)
      } finally {
        if (alive) setLoading(false)
      }
    })()
    return () => {
      alive = false
    }
  }, [ds, recentLimit])

  const shownStarred = useMemo(() => starred.slice(0, starredLimit), [starred, starredLimit])

  return (
    <div className={`kb-quick-view kb-quick-view--home${className ? ` ${className}` : ''}`}>
      <section className="kb-quick-section">
        <header className="kb-quick-section-head">
          <span className="kb-quick-section-title">
            <Star size={13} />
            {t('knowledgeQuickViews.home.starredSection')}
          </span>
          {onSelectView && starred.length > 0 ? (
            <button
              type="button"
              className="kb-quick-section-action"
              onClick={() => onSelectView('starred')}
            >
              {t('knowledgeQuickViews.home.seeAll')}
            </button>
          ) : null}
        </header>
        {loading ? (
          <KbPlaceholder>{t('knowledgeQuickViews.loading')}</KbPlaceholder>
        ) : error ? (
          <KbPlaceholder>{t('knowledgeQuickViews.error')}</KbPlaceholder>
        ) : shownStarred.length === 0 ? (
          <KbPlaceholder>{t('knowledgeQuickViews.home.starredEmpty')}</KbPlaceholder>
        ) : (
          <ul className="kb-quick-list">
            {shownStarred.map(cardRef => (
              <KbCardRow
                key={`${cardRef.projectId}::${cardRef.cardId}`}
                cardRef={cardRef}
                onOpen={onOpenCard}
              />
            ))}
          </ul>
        )}
      </section>

      <section className="kb-quick-section">
        <header className="kb-quick-section-head">
          <span className="kb-quick-section-title">
            <Clock size={13} />
            {t('knowledgeQuickViews.home.recentSection')}
          </span>
        </header>
        {loading ? (
          <KbPlaceholder>{t('knowledgeQuickViews.loading')}</KbPlaceholder>
        ) : error ? (
          <KbPlaceholder>{t('knowledgeQuickViews.error')}</KbPlaceholder>
        ) : recent.length === 0 ? (
          <KbPlaceholder>{t('knowledgeQuickViews.home.recentEmpty')}</KbPlaceholder>
        ) : (
          <ul className="kb-quick-list">
            {recent.map(cardRef => (
              <KbCardRow
                key={`${cardRef.projectId}::${cardRef.cardId}`}
                cardRef={cardRef}
                onOpen={onOpenCard}
              />
            ))}
          </ul>
        )}
      </section>

      <section className="kb-quick-section">
        <header className="kb-quick-section-head">
          <span className="kb-quick-section-title">
            <Folder size={13} />
            {t('knowledgeQuickViews.home.projectsSection')}
          </span>
        </header>
        {loading ? (
          <KbPlaceholder>{t('knowledgeQuickViews.loading')}</KbPlaceholder>
        ) : error ? (
          <KbPlaceholder>{t('knowledgeQuickViews.error')}</KbPlaceholder>
        ) : projects.length === 0 ? (
          <KbPlaceholder>{t('knowledgeQuickViews.home.projectsEmpty')}</KbPlaceholder>
        ) : (
          <ul className="kb-quick-list">
            {projects.map(project => (
              <li className="kb-quick-item" key={project.projectId} data-project-id={project.projectId}>
                <button
                  type="button"
                  className="kb-quick-item-main kb-quick-item-main--project"
                  onClick={() => onOpenProject?.(project)}
                  disabled={!onOpenProject}
                >
                  <Folder size={13} className="kb-quick-item-icon" />
                  <span className="kb-quick-item-title">{project.projectName}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

/* ────────────────────────────── Starred ────────────────────────────── */

export interface KnowledgeStarredViewProps extends KbQuickViewCommonProps {
  onOpenCard?: (cardRef: KbCardRef) => void
  /** Called after a card is un-starred (e.g. so a parent can refresh counts). */
  onUnstar?: (cardRef: KbCardRef) => void
}

/**
 * Starred quick view: every starred card across all projects (via
 * `workspace.wiki_list_starred`). Each row shows its owning project name;
 * clicking a row opens that card's swimlane. Rows can be un-starred in place.
 */
export const KnowledgeStarredView: React.FC<KnowledgeStarredViewProps> = ({
  dataSource,
  className,
  onOpenCard,
  onUnstar,
}) => {
  const { t } = useI18n()
  const ds = dataSource ?? defaultKbQuickViewsDataSource
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [items, setItems] = useState<KbCardRef[]>([])

  useEffect(() => {
    let alive = true
    setLoading(true)
    setError(false)
    void (async () => {
      try {
        const next = await ds.listStarred()
        if (!alive) return
        setItems(next)
      } catch {
        if (alive) setError(true)
      } finally {
        if (alive) setLoading(false)
      }
    })()
    return () => {
      alive = false
    }
  }, [ds])

  const handleUnstar = useCallback(
    (cardRef: KbCardRef) => {
      setItems(prev => prev.filter(r => !(r.projectId === cardRef.projectId && r.cardId === cardRef.cardId)))
      void ds.setStarred(cardRef.projectId, cardRef.cardId, false).catch(() => {
        // Restore the row if the actor rejected the write.
        setItems(prev => (prev.some(r => r.projectId === cardRef.projectId && r.cardId === cardRef.cardId) ? prev : [...prev, cardRef]))
      })
      onUnstar?.(cardRef)
    },
    [ds, onUnstar],
  )

  return (
    <div className={`kb-quick-view kb-quick-view--starred${className ? ` ${className}` : ''}`}>
      <ul className="kb-quick-list kb-quick-list--full">
        {loading ? (
          <KbPlaceholder>{t('knowledgeQuickViews.loading')}</KbPlaceholder>
        ) : error ? (
          <KbPlaceholder>{t('knowledgeQuickViews.starred.error')}</KbPlaceholder>
        ) : items.length === 0 ? (
          <KbPlaceholder>{t('knowledgeQuickViews.starred.empty')}</KbPlaceholder>
        ) : (
          items.map(cardRef => (
            <KbCardRow
              key={`${cardRef.projectId}::${cardRef.cardId}`}
              cardRef={cardRef}
              onOpen={onOpenCard}
              trailing={
                <button
                  type="button"
                  className="kb-quick-item-tail"
                  title={t('knowledgeQuickViews.starred.unstar')}
                  aria-label={t('knowledgeQuickViews.starred.unstar')}
                  onClick={() => handleUnstar(cardRef)}
                >
                  <Star size={13} fill="currentColor" />
                </button>
              }
            />
          ))
        )}
      </ul>
    </div>
  )
}

/* ────────────────────────────── Search ────────────────────────────── */

export interface KnowledgeSearchViewProps extends KbQuickViewCommonProps {
  onOpenCard?: (cardRef: KbCardRef) => void
  /** Input debounce before a search is issued (ms). */
  debounceMs?: number
  /** Per-project result cap passed to the aggregation. */
  limitPerProject?: number
  autoFocus?: boolean
}

/**
 * Search quick view: a query box plus cross-project results. The search reuses
 * the shared aggregation (`kbData.searchKbAcrossProjects`) through the injected
 * data source; each result shows its owning project name and matched snippet,
 * and clicking a result opens that card's swimlane.
 */
export const KnowledgeSearchView: React.FC<KnowledgeSearchViewProps> = ({
  dataSource,
  className,
  onOpenCard,
  debounceMs = 250,
  limitPerProject,
  autoFocus = false,
}) => {
  const { t } = useI18n()
  const ds = dataSource ?? defaultKbQuickViewsDataSource
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<KbSearchHit[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(false)
  const projectsRef = useRef<KbProjectRef[] | null>(null)

  useEffect(() => {
    const trimmed = query.trim()
    if (!trimmed) {
      setResults([])
      setLoading(false)
      setError(false)
      return
    }
    let alive = true
    const timer = setTimeout(() => {
      setLoading(true)
      setError(false)
      void (async () => {
        try {
          let projects = projectsRef.current
          if (!projects) {
            projects = await ds.listProjects()
            if (!alive) return
            projectsRef.current = projects
          }
          const next = await ds.search(trimmed, projects, limitPerProject)
          if (!alive) return
          setResults(next)
        } catch {
          if (alive) setError(true)
        } finally {
          if (alive) setLoading(false)
        }
      })()
    }, debounceMs)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [query, ds, debounceMs, limitPerProject])

  const handleClear = useCallback(() => setQuery(''), [])
  const showPrompt = query.trim().length === 0

  return (
    <div className={`kb-quick-view kb-quick-view--search${className ? ` ${className}` : ''}`}>
      <div className="kb-quick-search">
        <Search size={14} className="kb-quick-search-icon" />
        <input
          className="kb-quick-search-input"
          type="search"
          value={query}
          placeholder={t('knowledgeQuickViews.search.placeholder')}
          aria-label={t('knowledgeQuickViews.search.placeholder')}
          autoFocus={autoFocus}
          onChange={e => setQuery(e.target.value)}
        />
        {query ? (
          <button
            type="button"
            className="kb-quick-search-clear"
            title={t('knowledgeQuickViews.search.clear')}
            aria-label={t('knowledgeQuickViews.search.clear')}
            onClick={handleClear}
          >
            <X size={13} />
          </button>
        ) : null}
      </div>

      {showPrompt ? (
        <KbPlaceholder>{t('knowledgeQuickViews.search.prompt')}</KbPlaceholder>
      ) : loading ? (
        <KbPlaceholder>{t('knowledgeQuickViews.searching')}</KbPlaceholder>
      ) : error ? (
        <KbPlaceholder>{t('knowledgeQuickViews.search.error')}</KbPlaceholder>
      ) : results.length === 0 ? (
        <KbPlaceholder>{t('knowledgeQuickViews.search.empty')}</KbPlaceholder>
      ) : (
        <>
          <div className="kb-quick-search-count">
            {t('knowledgeQuickViews.search.resultCount', { count: results.length })}
          </div>
          <ul className="kb-quick-list kb-quick-list--full">
            {results.map(hit => (
              <KbCardRow
                key={`${hit.projectId}::${hit.cardId}::${hit.line ?? 0}`}
                cardRef={hit}
                snippet={hit.snippet}
                line={hit.line}
                onOpen={onOpenCard}
              />
            ))}
          </ul>
        </>
      )}
    </div>
  )
}
