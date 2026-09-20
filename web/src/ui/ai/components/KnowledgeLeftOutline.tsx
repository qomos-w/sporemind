import React, { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react'
import {
  ChevronRight,
  CornerUpRight,
  Folder,
  FolderOpen,
  Home,
  MoreHorizontal,
  Plus,
  Search,
  Star,
} from 'lucide-react'
import { useI18n } from '../../../i18n'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import { useBrowserOverlay } from '../browserOverlay'
import { getCardTypeMeta } from './mono-card-meta'
import type { MonoCardType } from '../../../domain/mono-types'
import type * as systemTypes from '../../../gen-clients/system/types'
import {
  outlineRootsFromTocNodes,
  setExpanded,
  type KbOutlineNode,
  type KbOutlineProject,
  type KbQuickView,
} from './knowledgeLeftOutline.logic'
import './KnowledgeLeftOutline.css'

export interface KnowledgeLeftOutlineProps {
  /** Every mounted project, in display order (workspace.list_project). */
  projects: readonly KbOutlineProject[]
  /** Currently selected right-column view. */
  activeView?: KbQuickView
  /** Top entries: home / search / starred — switch the right-column view. */
  onSelectView?: (view: KbQuickView) => void

  /**
   * Expanded project ids (controlled). Omit to let the outline own the
   * expansion set; pass it to persist expansion in actor-backed state.
   */
  expandedProjectIds?: ReadonlySet<string>
  onExpandedProjectIdsChange?: (next: Set<string>) => void

  /** Lazily-loaded TOC tree per project (`undefined` = not loaded yet). */
  tocByProject?: Readonly<Record<string, readonly systemTypes.WikiCardTreeNode[] | undefined>>
  /** Projects whose TOC is currently loading. */
  loadingProjectIds?: ReadonlySet<string>
  /** Per-project load errors, rendered in place of the tree. */
  errorByProject?: Readonly<Record<string, string | undefined>>
  /** Fired once when a project is first expanded, to trigger the lazy load. */
  onRequestProjectToc?: (projectId: string) => void

  /** Highlight the project whose lane is open in the right column. */
  activeProjectId?: string | null
  /** Highlight the card whose lane is open in the right column. */
  activeCardId?: string | null

  /** Star state lookup (star state is per project actor, keyed by card id). */
  isCardStarred?: (projectId: string, cardId: string) => boolean
  /** Open a project lane in the right column. */
  onOpenProject?: (project: KbOutlineProject) => void
  /** Focus a card in the right column (project stack + expand + scroll). */
  onOpenCard?: (project: KbOutlineProject, cardId: string) => void
  /** Create a card under the project directory (mounts under toc). */
  onCreateCard?: (project: KbOutlineProject, name: string) => void
  /** Star / un-star a card. */
  onToggleStar?: (projectId: string, cardId: string, starred: boolean) => void

  className?: string
}

type OutlineMenuTarget =
  | { kind: 'project'; project: KbOutlineProject; x: number; y: number }
  | { kind: 'card'; project: KbOutlineProject; cardId: string; title: string; starred: boolean; x: number; y: number }

/**
 * Shared context menu for outline nodes (right-click and the ⋯ button open the
 * same menu). Only cards can be starred: a project node has no star entry (there
 * is no project-level star state), so its menu carries the open action alone.
 * Mirrors ComponentBadgeContextMenu: it registers with `useBrowserOverlay(open)`
 * so the native right-panel browser window is hidden while the menu is on
 * screen, and delegates outside-dismissal to `useMenuDismiss`. No z-index hacks,
 * no direct window hide/show calls.
 */
const OutlineNodeMenu: React.FC<{
  target: OutlineMenuTarget | null
  onClose: () => void
  onOpen: (target: OutlineMenuTarget) => void
  onToggleStar: (target: OutlineMenuTarget) => void
  onNewCard?: (target: Extract<OutlineMenuTarget, { kind: 'project' }>) => void
}> = ({ target, onClose, onOpen, onToggleStar, onNewCard }) => {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)

  useBrowserOverlay(!!target)
  useMenuDismiss(menuRef, onClose, target)

  // Position after the real size is known, flipping at the window edges.
  useLayoutEffect(() => {
    if (!target || !menuRef.current) return
    const menu = menuRef.current
    const rect = menu.getBoundingClientRect()
    const viewportW = document.documentElement.clientWidth
    const viewportH = document.documentElement.clientHeight
    const padding = 8

    let left = target.x
    let top = target.y
    if (left + rect.width > viewportW - padding) left = target.x - rect.width
    if (left < padding) left = padding
    if (top + rect.height > viewportH - padding) top = target.y - rect.height
    if (top < padding) top = padding

    menu.style.left = `${left}px`
    menu.style.top = `${top}px`
  }, [target])

  if (!target) return null

  const header = target.kind === 'project' ? target.project.name : target.title
  // Only cards are starrable: projects have no star state (no project-level
  // star entry in the menu — a project node must not show a dead menu item).
  const showStar = target.kind === 'card'
  const showNewCard = target.kind === 'project' && !!onNewCard

  return (
    <div
      ref={menuRef}
      className="kb-outline-menu"
      style={{ left: target.x, top: target.y }}
      role="menu"
      tabIndex={-1}
    >
      <div className="kb-outline-menu-header">
        <span className="kb-outline-menu-title">{header}</span>
      </div>
      <div className="kb-outline-menu-list">
        <button
          type="button"
          className="kb-outline-menu-item"
          onClick={() => { onOpen(target); onClose() }}
        >
          <span className="kb-outline-menu-icon"><CornerUpRight size={13} /></span>
          <span className="kb-outline-menu-label">{t('knowledgeOutline.menu.open')}</span>
        </button>
        {showNewCard && (
          <button
            type="button"
            className="kb-outline-menu-item"
            onClick={() => { onNewCard?.(target as Extract<OutlineMenuTarget, { kind: 'project' }>); onClose() }}
          >
            <span className="kb-outline-menu-icon"><Plus size={13} /></span>
            <span className="kb-outline-menu-label">{t('knowledgeOutline.menu.newCard')}</span>
          </button>
        )}
        {showStar && (
          <button
            type="button"
            className="kb-outline-menu-item"
            onClick={() => { onToggleStar(target); onClose() }}
          >
            <span className="kb-outline-menu-icon">
              <Star size={13} fill={target.starred ? 'currentColor' : 'none'} />
            </span>
            <span className="kb-outline-menu-label">
              {target.starred ? t('knowledgeOutline.menu.unstar') : t('knowledgeOutline.menu.star')}
            </span>
          </button>
        )}
      </div>
    </div>
  )
}

/** Recursive card node with its own expand caret, label and ⋯ menu trigger. */
const OutlineCardNode: React.FC<{
  node: KbOutlineNode
  project: KbOutlineProject
  depth: number
  expandedCards: ReadonlySet<string>
  activeCardId?: string | null
  isCardStarred?: (projectId: string, cardId: string) => boolean
  onToggleCard: (key: string) => void
  onOpenCard?: (project: KbOutlineProject, cardId: string) => void
  onOpenMenu: (project: KbOutlineProject, node: KbOutlineNode, x: number, y: number) => void
}> = ({ node, project, depth, expandedCards, activeCardId, isCardStarred, onToggleCard, onOpenCard, onOpenMenu }) => {
  const { t } = useI18n()
  const key = `${project.projectId}::${node.id}`
  const hasChildren = node.children.length > 0
  const isExpanded = expandedCards.has(key)
  const isActive = activeCardId != null && activeCardId === node.id
  const typeMeta = getCardTypeMeta((node.type || undefined) as MonoCardType | undefined)
  const isStarred = !!isCardStarred?.(project.projectId, node.id)

  const openMenuAt = (el: HTMLElement) => {
    const rect = el.getBoundingClientRect()
    onOpenMenu(project, node, rect.right, rect.bottom)
  }

  return (
    <div className="kb-outline-node" role="treeitem" aria-expanded={hasChildren ? isExpanded : undefined}>
      <div
        className={`kb-outline-row kb-outline-row--card${isActive ? ' kb-outline-row--active' : ''}`}
        style={{ paddingLeft: `${6 + depth * 12}px` }}
        onContextMenu={(e) => { e.preventDefault(); onOpenMenu(project, node, e.clientX, e.clientY) }}
      >
        {hasChildren ? (
          <button
            type="button"
            className="kb-outline-caret"
            aria-label={isExpanded ? t('knowledgeOutline.collapse') : t('knowledgeOutline.expand')}
            onClick={() => onToggleCard(key)}
          >
            <ChevronRight size={12} className={isExpanded ? 'open' : ''} />
          </button>
        ) : (
          <span className="kb-outline-caret kb-outline-caret--spacer" />
        )}
        <button
          type="button"
          className="kb-outline-label"
          title={node.title}
          onClick={() => onOpenCard?.(project, node.id)}
        >
          <span className="kb-outline-icon">{typeMeta.icon}</span>
          <span className="kb-outline-title">{node.title}</span>
          {isStarred && <Star size={11} className="kb-outline-starred" fill="currentColor" />}
        </button>
        <button
          type="button"
          className="kb-outline-more"
          aria-label={t('knowledgeOutline.more')}
          title={t('knowledgeOutline.more')}
          onClick={(e) => openMenuAt(e.currentTarget)}
        >
          <MoreHorizontal size={14} />
        </button>
      </div>
      {hasChildren && isExpanded && (
        <div className="kb-outline-children">
          {node.children.map(child => (
            <OutlineCardNode
              key={child.id}
              node={child}
              project={project}
              depth={depth + 1}
              expandedCards={expandedCards}
              activeCardId={activeCardId}
              isCardStarred={isCardStarred}
              onToggleCard={onToggleCard}
              onOpenCard={onOpenCard}
              onOpenMenu={onOpenMenu}
            />
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * Global knowledge-base left outline ([[kb-left-outline]]).
 *
 * Controlled props component: it owns no project data and performs no I/O — the
 * parent enumerates projects (workspace.list_project) and lazily loads each
 * project's TOC tree via the `{target}` routing header (kbData), then feeds the
 * results back in. The three top entries select the right-column view
 * ([[kb-quick-views]]); expanding a project reveals its card tree; project and
 * card nodes share one right-click / ⋯ menu.
 */
export const KnowledgeLeftOutline: React.FC<KnowledgeLeftOutlineProps> = ({
  projects,
  activeView,
  onSelectView,
  expandedProjectIds,
  onExpandedProjectIdsChange,
  tocByProject,
  loadingProjectIds,
  errorByProject,
  onRequestProjectToc,
  activeProjectId,
  activeCardId,
  isCardStarred,
  onOpenProject,
  onOpenCard,
  onCreateCard,
  onToggleStar,
  className,
}) => {
  const { t } = useI18n()

  const controlledExpanded = expandedProjectIds !== undefined
  const [internalExpanded, setInternalExpanded] = useState<Set<string>>(new Set())
  const expanded = controlledExpanded ? expandedProjectIds! : internalExpanded

  const [expandedCards, setExpandedCards] = useState<Set<string>>(new Set())
  const [menuTarget, setMenuTarget] = useState<OutlineMenuTarget | null>(null)
  // Inline new-card draft: the project id the input is attached to (null = no
  // draft) plus the current value.
  const [draftProjectId, setDraftProjectId] = useState<string | null>(null)
  const [draftValue, setDraftValue] = useState('')

  const commitExpanded = useCallback((next: Set<string>) => {
    if (controlledExpanded) onExpandedProjectIdsChange?.(next)
    else setInternalExpanded(next)
  }, [controlledExpanded, onExpandedProjectIdsChange])

  const handleToggleProject = useCallback((project: KbOutlineProject, willExpand: boolean) => {
    commitExpanded(setExpanded(expanded, project.projectId, willExpand))
    // Lazy load: request the TOC the first time a project is expanded.
    if (willExpand && onRequestProjectToc && tocByProject?.[project.projectId] === undefined) {
      onRequestProjectToc(project.projectId)
    }
  }, [commitExpanded, expanded, onRequestProjectToc, tocByProject])

  const handleToggleCard = useCallback((key: string) => {
    setExpandedCards(prev => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])

  const closeMenu = useCallback(() => setMenuTarget(null), [])

  const openProjectMenu = useCallback((project: KbOutlineProject, x: number, y: number) => {
    setMenuTarget({ kind: 'project', project, x, y })
  }, [])

  const openCardMenu = useCallback((
    project: KbOutlineProject,
    node: KbOutlineNode,
    x: number,
    y: number,
  ) => {
    setMenuTarget({
      kind: 'card',
      project,
      cardId: node.id,
      title: node.title,
      starred: !!isCardStarred?.(project.projectId, node.id),
      x,
      y,
    })
  }, [isCardStarred])

  const handleMenuOpen = useCallback((target: OutlineMenuTarget) => {
    if (target.kind === 'project') onOpenProject?.(target.project)
    else onOpenCard?.(target.project, target.cardId)
  }, [onOpenProject, onOpenCard])

  const handleMenuToggleStar = useCallback((target: OutlineMenuTarget) => {
    if (target.kind === 'card') {
      onToggleStar?.(target.project.projectId, target.cardId, !target.starred)
    }
  }, [onToggleStar])

  const handleMenuNewCard = useCallback((target: Extract<OutlineMenuTarget, { kind: 'project' }>) => {
    setDraftProjectId(target.project.projectId)
    setDraftValue('')
  }, [])

  const submitDraft = useCallback((project: KbOutlineProject) => {
    const value = draftValue
    setDraftProjectId(null)
    setDraftValue('')
    if (value.trim()) onCreateCard?.(project, value)
  }, [draftValue, onCreateCard])

  const views: readonly { id: KbQuickView; label: string; icon: React.ReactNode }[] = useMemo(() => [
    { id: 'home', label: t('knowledgeOutline.home'), icon: <Home size={14} /> },
    { id: 'search', label: t('knowledgeOutline.search'), icon: <Search size={14} /> },
    { id: 'starred', label: t('knowledgeOutline.starred'), icon: <Star size={14} /> },
  ], [t])

  return (
    <div className={`kb-outline${className ? ` ${className}` : ''}`}>
      <div className="kb-outline-views" role="tablist" aria-label={t('knowledgeOutline.views')}>
        {views.map(view => (
          <button
            key={view.id}
            type="button"
            role="tab"
            aria-selected={activeView === view.id}
            className={`kb-outline-view${activeView === view.id ? ' active' : ''}`}
            onClick={() => onSelectView?.(view.id)}
          >
            <span className="kb-outline-view-icon">{view.icon}</span>
            <span className="kb-outline-view-label">{view.label}</span>
          </button>
        ))}
      </div>

      <div className="kb-outline-projects" role="tree" aria-label={t('knowledgeOutline.projects')}>
        {projects.length === 0 ? (
          <div className="kb-outline-empty">{t('knowledgeOutline.empty')}</div>
        ) : (
          projects.map(project => {
            const isExpanded = expanded.has(project.projectId)
            const rawNodes = tocByProject?.[project.projectId]
            const roots = rawNodes ? outlineRootsFromTocNodes(rawNodes) : []
            const isLoading = loadingProjectIds?.has(project.projectId) ?? false
            const loadError = errorByProject?.[project.projectId]
            const isActive = activeProjectId === project.projectId

            return (
              <div key={project.projectId} className="kb-outline-project">
                <div
                  className={`kb-outline-row kb-outline-row--project${isActive ? ' kb-outline-row--active' : ''}`}
                  role="treeitem"
                  aria-expanded={isExpanded}
                  onContextMenu={(e) => { e.preventDefault(); openProjectMenu(project, e.clientX, e.clientY) }}
                >
                  <button
                    type="button"
                    className="kb-outline-caret"
                    aria-label={isExpanded ? t('knowledgeOutline.collapse') : t('knowledgeOutline.expand')}
                    onClick={() => handleToggleProject(project, !isExpanded)}
                  >
                    <ChevronRight size={12} className={isExpanded ? 'open' : ''} />
                  </button>
                  <button
                    type="button"
                    className="kb-outline-label"
                    title={project.name}
                    onClick={() => onOpenProject?.(project)}
                  >
                    <span className="kb-outline-icon">
                      {isExpanded ? <FolderOpen size={14} /> : <Folder size={14} />}
                    </span>
                    <span className="kb-outline-title">{project.name}</span>
                  </button>
                  <button
                    type="button"
                    className="kb-outline-more"
                    aria-label={t('knowledgeOutline.more')}
                    title={t('knowledgeOutline.more')}
                    onClick={(e) => {
                      const rect = e.currentTarget.getBoundingClientRect()
                      openProjectMenu(project, rect.right, rect.bottom)
                    }}
                  >
                    <MoreHorizontal size={14} />
                  </button>
                </div>

                {draftProjectId === project.projectId && (
                  <div className="kb-outline-newcard">
                    <input
                      // eslint-disable-next-line jsx-a11y/no-autofocus -- the draft input only mounts on explicit user action
                      autoFocus
                      className="kb-outline-newcard-input"
                      value={draftValue}
                      placeholder={t('knowledgeOutline.newCard.placeholder')}
                      aria-label={t('knowledgeOutline.menu.newCard')}
                      onChange={(e) => setDraftValue(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          e.preventDefault()
                          submitDraft(project)
                        } else if (e.key === 'Escape') {
                          e.preventDefault()
                          setDraftProjectId(null)
                          setDraftValue('')
                        }
                      }}
                      onBlur={() => { setDraftProjectId(null); setDraftValue('') }}
                    />
                  </div>
                )}

                {isExpanded && (
                  <div className="kb-outline-children">
                    {isLoading ? (
                      <div className="kb-outline-note">{t('knowledgeOutline.loading')}</div>
                    ) : loadError ? (
                      <div className="kb-outline-note kb-outline-note--error">
                        {t('knowledgeOutline.loadError')}
                      </div>
                    ) : rawNodes === undefined ? null : roots.length === 0 ? (
                      <div className="kb-outline-note">{t('knowledgeOutline.emptyProject')}</div>
                    ) : (
                      roots.map(node => (
                        <OutlineCardNode
                          key={node.id}
                          node={node}
                          project={project}
                          depth={0}
                          expandedCards={expandedCards}
                          activeCardId={activeCardId}
                          isCardStarred={isCardStarred}
                          onToggleCard={handleToggleCard}
                          onOpenCard={onOpenCard}
                          onOpenMenu={openCardMenu}
                        />
                      ))
                    )}
                  </div>
                )}
              </div>
            )
          })
        )}
      </div>

      <OutlineNodeMenu
        target={menuTarget}
        onClose={closeMenu}
        onOpen={handleMenuOpen}
        onToggleStar={handleMenuToggleStar}
        onNewCard={onCreateCard ? handleMenuNewCard : undefined}
      />
    </div>
  )
}
