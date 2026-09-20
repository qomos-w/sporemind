import React, { useMemo } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { splitFrontmatter, type MonoCardListItem, type MonoCardType } from '../../../domain/mono-types'
import { getCardTypeMeta, PRIORITY_META, STATUS_META } from './mono-card-meta.tsx'
import { processWikiWords, wikiUrlTransform } from './parts/wikiword.ts'
import './MonoCardInline.css'

const MAX_BODY_LINES = 6
const MAX_BODY_CHARS = 500

export interface MonoCardInlineProps {
  card: MonoCardListItem
  /** Optional raw content to extract body preview. Falls back to card.raw. */
  raw?: string
  onClick?: () => void
  className?: string
}

/**
 * Compact read-only card display for use in timeline tool-call results.
 * Shows type icon, title, tags, and a short body preview with wikiword rendering.
 */
export function MonoCardInline({ card, raw, onClick, className }: MonoCardInlineProps) {
  const meta = getCardTypeMeta(card.type as MonoCardType | undefined)

  const bodyPreview = useMemo(() => {
    const rawSource = raw ?? card.raw
    if (!rawSource) return ''
    const { body } = splitFrontmatter(rawSource)
    if (!body?.trim()) return ''
    // Limit to first N lines and chars
    const lines = body.split('\n').filter(l => l.trim().length > 0)
    const truncated = lines.slice(0, MAX_BODY_LINES).join('\n')
    const capped = truncated.length > MAX_BODY_CHARS ? truncated.slice(0, MAX_BODY_CHARS) + '…' : truncated
    // Check if there's more content
    const hasMore = lines.length > MAX_BODY_LINES || body.length > MAX_BODY_CHARS
    return processWikiWords(capped) + (hasMore ? '\n\n…' : '')
  }, [raw, card.raw])

  const isOverdue = card.due && new Date(card.due) < new Date()

  return (
    <article
      className={`mono-card-inline${onClick ? ' mono-card-inline--clickable' : ''}${className ? ` ${className}` : ''}`}
      onClick={onClick}
      data-card-id={card.id}
    >
      <div className="mono-card-inline-header">
        <span className="mono-card-inline-type-icon" title={meta.label}>
          {meta.icon}
        </span>
        <h4 className="mono-card-inline-title">{card.id}</h4>
        {card.priority && (
          <span className="mono-card-inline-priority" style={{ color: PRIORITY_META[card.priority]?.color ?? 'var(--text-tertiary)' }}>
            {PRIORITY_META[card.priority]?.label ?? card.priority}
          </span>
        )}
        {card.status && (
          <span
            className="mono-card-inline-status"
            style={{ '--wiki-status-color': STATUS_META[card.status]?.color ?? 'var(--text-tertiary)' } as React.CSSProperties}
          >
            {STATUS_META[card.status]?.label ?? card.status}
          </span>
        )}
      </div>

      {card.tags.length > 0 && (
        <div className="mono-card-inline-tags">
          {card.tags.map(tag => (
            <span key={tag} className="mono-card-inline-tag">{tag}</span>
          ))}
          {card.due && (
            <span className={`mono-card-inline-due${isOverdue ? ' overdue' : ''}`}>{card.due}</span>
          )}
        </div>
      )}

      {bodyPreview && (
        <div className="mono-card-inline-body markdown-content">
          <Markdown
            remarkPlugins={[remarkGfm]}
            urlTransform={wikiUrlTransform}
          >
            {bodyPreview}
          </Markdown>
        </div>
      )}
    </article>
  )
}
