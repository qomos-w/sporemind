import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { ChevronDown, ChevronUp, PanelRight } from 'lucide-react'
import { useI18n } from '../../../i18n'
import type { MonoCardType } from '../../../domain/mono-types'
import { getBuiltinTagDisplayName, isBuiltinTag } from '../../../domain/builtin-cards'
import { getCardTypeMeta, PRIORITY_META, STATUS_META } from './mono-card-meta.tsx'
import { CardIcon } from './CardIcon'
import { cardVisual, getCardVisualStyle } from './cardVisual'
import { processWikiWords, wikiUrlTransform } from './parts/wikiword'
import type { LaneStackCard } from './knowledgeMode.logic'
import {
  defaultCollapsedIds,
  normalizeAccordion,
  setCardCollapsed,
  summarizeBody,
  swimlaneCardIds,
} from './knowledgeSwimlane.logic'
import './KnowledgeSwimlane.css'

export interface KnowledgeSwimlaneProps {
  /** The project's display stack: every renderable card of the toc subtree,
   *  depth-first, each annotated with its tree depth for indentation. */
  cards: LaneStackCard[]
  /** Card to bring into view (scroll + expanded) — the navigation focus. */
  focusCardId?: string | null
  /** Changes on every focus navigation so a repeat focus on the same card
   *  still scrolls it back into view. */
  focusKey?: number
  /** Called when a wikiword link inside a card body is clicked. */
  onWikiWord?: (cardId: string, word: string) => void
  /** Controlled collapsed-id set (accordion). Omit to let the lane own its fold state. */
  collapsedIds?: ReadonlySet<string>
  /** Called with the next collapsed-id set whenever a fold button is pressed. */
  onCollapsedIdsChange?: (next: Set<string>) => void
  /** Open a card in the right sidebar panel. */
  onOpenInRightPanel?: (cardId: string) => void
  className?: string
}

/** Minimal Markdown body renderer mirroring MonoCardDetail's wikiword handling. */
const SwimlaneMarkdown: React.FC<{
  body: string
  cardId: string
  onWikiWord?: (cardId: string, word: string) => void
}> = React.memo(({ body, cardId, onWikiWord }) => {
  const processed = useMemo(() => processWikiWords(body), [body])
  const components = useMemo(() => ({
    a: ({ href, children, ...props }: React.ComponentProps<'a'>) => {
      if (href?.startsWith('wiki:')) {
        return (
          <a
            className="wiki-word-link"
            href="#"
            onClick={(e) => {
              e.preventDefault()
              onWikiWord?.(cardId, decodeURIComponent(href.slice(5)))
            }}
          >
            {children}
          </a>
        )
      }
      return <a href={href} target="_blank" rel="noreferrer" {...props}>{children}</a>
    },
  }), [cardId, onWikiWord])
  return (
    <Markdown remarkPlugins={[remarkGfm]} urlTransform={wikiUrlTransform} components={components}>
      {processed}
    </Markdown>
  )
})

const SwimlaneCardView: React.FC<{
  card: LaneStackCard
  focused: boolean
  collapsed: boolean
  onToggleFold: (id: string) => void
  onWikiWord?: (cardId: string, word: string) => void
  onOpenInRightPanel?: (cardId: string) => void
}> = ({ card, focused, collapsed, onToggleFold, onWikiWord, onOpenInRightPanel }) => {
  const { t } = useI18n()
  const type = (card.type as MonoCardType) || 'wiki'
  const typeMeta = getCardTypeMeta(type)
  const visual = cardVisual(card)
  const visualStyle = getCardVisualStyle(visual)
  const hasVisual = !!visual.icon || !!visual.accent || !!visual.emphasis || !!visual.background || !!visual.border
  const isOverdue = card.due ? new Date(card.due) < new Date() : false
  const summary = collapsed ? summarizeBody(card.body) : ''

  const handleMainClick = useCallback((e: React.MouseEvent) => {
    // Links (including wikiword/file refs) and controls keep their own
    // behaviour; clicking a collapsed card anywhere else expands it.
    const target = e.target as HTMLElement
    if (target.closest('a, button, input, textarea, select')) return
    if (!collapsed) return
    onToggleFold(card.id)
  }, [collapsed, onToggleFold, card.id])

  return (
    <article
      className={`kb-swimlane-card${focused ? ' kb-swimlane-card--focused' : ''}${collapsed ? ' kb-swimlane-card--collapsed' : ''}`}
      data-card-id={card.id}
      data-depth={card.depth}
      data-collapsed={collapsed ? 'true' : 'false'}
      style={{ '--kb-card-depth': card.depth } as React.CSSProperties}
    >
      <div
        className="kb-swimlane-card-main"
        onClick={handleMainClick}
        style={hasVisual ? { background: visualStyle.background, borderColor: visualStyle.border, boxShadow: visualStyle.shadow } : undefined}
      >
        <div className="kb-swimlane-card-header">
          <span className="kb-swimlane-card-type-icon" title={typeMeta.label} style={{ color: visualStyle.icon }}>
            {visual.icon ? <CardIcon name={visual.icon} size={16} color={visualStyle.icon} /> : typeMeta.icon}
          </span>
          <h3 className="kb-swimlane-card-title">{card.id}</h3>
          <div className="kb-swimlane-card-meta">
            {card.status && (
              <span
                className="kb-swimlane-card-chip kb-swimlane-card-status"
                style={{ '--wiki-status-color': STATUS_META[card.status]?.color ?? 'var(--text-tertiary)' } as React.CSSProperties}
              >
                {STATUS_META[card.status]?.icon}
                {STATUS_META[card.status]?.label ?? card.status}
              </span>
            )}
            {card.priority && (
              <span className="kb-swimlane-card-chip kb-swimlane-card-priority" style={{ color: PRIORITY_META[card.priority]?.color ?? 'var(--text-tertiary)' }}>
                {PRIORITY_META[card.priority]?.label ?? card.priority}
              </span>
            )}
            {card.due && (
              <span className={`kb-swimlane-card-chip kb-swimlane-card-due${isOverdue ? ' overdue' : ''}`}>{card.due}</span>
            )}
          </div>
          <button
            type="button"
            className="kb-swimlane-card-toggle"
            aria-expanded={!collapsed}
            aria-label={collapsed ? t('knowledgeSwimlane.expand') : t('knowledgeSwimlane.collapse')}
            title={collapsed ? t('knowledgeSwimlane.expand') : t('knowledgeSwimlane.collapse')}
            onClick={() => onToggleFold(card.id)}
          >
            {collapsed ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
          </button>
          {onOpenInRightPanel && (
            <button
              type="button"
              className="kb-swimlane-card-open-right"
              aria-label={t('knowledgeSwimlane.openInRightPanel')}
              title={t('knowledgeSwimlane.openInRightPanel')}
              onClick={() => onOpenInRightPanel(card.id)}
            >
              <PanelRight size={14} />
            </button>
          )}
        </div>

        {card.tags.length > 0 && (
          <div className="kb-swimlane-card-tags">
            {card.tags.map(tag => (
              <span key={tag} className={`kb-swimlane-card-tag${isBuiltinTag(tag) ? ' kb-swimlane-card-tag--builtin' : ''}`}>
                {isBuiltinTag(tag) ? getBuiltinTagDisplayName(tag, t) : tag}
              </span>
            ))}
          </div>
        )}

        {collapsed ? (
          summary ? <p className="kb-swimlane-card-summary">{summary}</p> : null
        ) : (
          <div className="kb-swimlane-card-body markdown-content">
            {card.body ? <SwimlaneMarkdown body={card.body} cardId={card.id} onWikiWord={onWikiWord} /> : null}
          </div>
        )}
      </div>
    </article>
  )
}

/**
 * Knowledge-base right-column stack: every card of the project's toc subtree,
 * one row each (children below their parent, indented by depth). The toc
 * container itself is never rendered as a card. Fold behaviour follows the
 * accordion rules in {@link ./knowledgeSwimlane.logic}; the focused card is
 * scrolled into view.
 */
export const KnowledgeSwimlane: React.FC<KnowledgeSwimlaneProps> = ({
  cards,
  focusCardId,
  focusKey,
  onWikiWord,
  collapsedIds,
  onCollapsedIdsChange,
  onOpenInRightPanel,
  className,
}) => {
  const { t } = useI18n()
  const controlled = collapsedIds !== undefined
  const ids = useMemo(() => swimlaneCardIds(cards), [cards])

  const [internalCollapsed, setInternalCollapsed] = useState<Set<string>>(() => defaultCollapsedIds(cards))

  // Re-seed the default fold state when the lane switches to another project's
  // stack. The key guard keeps user toggles stable across ordinary re-renders
  // even if the parent passes fresh array/card identities.
  const resetKeyRef = useRef(cards.map(c => c.id).join('\u0000'))
  useEffect(() => {
    const key = cards.map(c => c.id).join('\u0000')
    if (resetKeyRef.current === key) return
    resetKeyRef.current = key
    if (!controlled) setInternalCollapsed(defaultCollapsedIds(cards))
  }, [cards, controlled])

  const collapsed = useMemo(
    () => normalizeAccordion(controlled ? collapsedIds! : internalCollapsed, ids),
    [controlled, collapsedIds, internalCollapsed, ids],
  )

  const commit = useCallback((next: Set<string>) => {
    if (controlled) onCollapsedIdsChange?.(next)
    else setInternalCollapsed(next)
  }, [controlled, onCollapsedIdsChange])

  const handleToggleFold = useCallback((id: string) => {
    commit(normalizeAccordion(setCardCollapsed(collapsed, ids, id, !collapsed.has(id)), ids))
  }, [collapsed, ids, commit])

  // Bring the focused card into view (the fold seeding already expanded it).
  const focusRef = useRef<number | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    if (!focusCardId || focusRef.current === focusKey) return
    focusRef.current = focusKey ?? null
    const el = containerRef.current?.querySelector(`[data-card-id="${CSS.escape(focusCardId)}"]`)
    el?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [focusCardId, focusKey, cards])

  return (
    <div className={`kb-swimlane${className ? ` ${className}` : ''}`} ref={containerRef}>
      {cards.length === 0 ? (
        <div className="kb-swimlane-empty">{t('knowledgeOutline.emptyProject')}</div>
      ) : (
        cards.map(card => (
          <SwimlaneCardView
            key={card.id}
            card={card}
            focused={focusCardId === card.id}
            collapsed={collapsed.has(card.id)}
            onToggleFold={handleToggleFold}
            onWikiWord={onWikiWord}
            onOpenInRightPanel={onOpenInRightPanel}
          />
        ))
      )}
    </div>
  )
}
