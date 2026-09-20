import { Target } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { isBuiltinTag, getBuiltinTagDisplayName } from '../../../domain/builtin-cards'
import type { MonoCardListItem, MonoCardType } from '../../../domain/mono-types'
import { CATEGORY_META, PRIORITY_META, STATUS_META, getCardTypeMeta } from './mono-card-meta.tsx'
import { CardAgentAvatar } from './CardAgentAvatar'
import { requestOpenAgentChat } from '../pendingAgentChat'
import './MonoCardCompact.css'
import { CardIcon } from './CardIcon'
import { cardVisual, getCardVisualStyle } from './cardVisual'

export interface MonoCardCompactProps {
  card: MonoCardListItem
  compact?: boolean
  onClick?: () => void
  className?: string
  /** Explicit agent reference for the header avatar; overrides data.agent. */
  agentRef?: string
  /** Render the status label as a corner badge instead of inline in the info line. */
  cornerStatus?: boolean
  /** Colored dot rendered before the title (e.g. bound agent kind). */
  leadingDot?: { color: string; title?: string }
  /** Icon rendered before the title. */
  leadingIcon?: 'target'
  /** Text chip rendered at the top-right corner (e.g. orchestrator kind). */
  cornerChip?: { label: string; color: string }
  /** Display title override (e.g. scheduler cards strip the "sched:" prefix). */
  titleOverride?: string
  /** Visual variant: 'workflow' moves the agent avatar to the bottom-right
   *  corner and hides the tags row (card.tags data is preserved). */
  variant?: 'default' | 'workflow'
}

export function MonoCardCompact({ card, compact, onClick, className, agentRef: agentRefProp, cornerStatus, leadingDot, leadingIcon, cornerChip, titleOverride, variant = 'default' }: MonoCardCompactProps) {
  const { t } = useI18n()
  const type = (card.type as MonoCardType) || 'wiki'
  const isOverdue = card.due && new Date(card.due) < new Date()

  const hasMeta = card.tags.length > 0 || card.priority || card.status || card.due
  const meta = getCardTypeMeta(type)
  const agentRef = agentRefProp ?? (typeof card.data?.agent === 'string' ? card.data.agent : undefined)
  const visual = cardVisual(card)
  const visualStyle = getCardVisualStyle(visual)
  const hasVisual = !!visual.icon || !!visual.accent || !!visual.emphasis || !!visual.background || !!visual.border
  const statusMeta = card.status ? STATUS_META[card.status] : undefined
  const statusColor = statusMeta?.color ?? 'var(--text-tertiary)'

  const rawCategory = (card.data as Record<string, unknown> | undefined)?.category
  const categoryMeta = typeof rawCategory === 'string' ? CATEGORY_META[rawCategory as keyof typeof CATEGORY_META] : undefined

  return (
    <>
    <article
      className={`mono-card-compact mono-card-compact--${type}${compact ? ' mono-card-compact--compact' : ''}${className ? ` ${className}` : ''}`}
      onClick={onClick}
      data-card-id={card.id}
      style={hasVisual ? { background: visualStyle.background, borderColor: visualStyle.border, borderWidth: visualStyle.borderWidth, boxShadow: visualStyle.shadow } : undefined}
    >
      {compact && card.status && (
        <div className="mono-card-compact-status-bar" style={{ background: statusColor }} />
      )}
      {type === 'task' && categoryMeta && (
        <div className="mono-card-compact-category-bar" style={{ background: categoryMeta.color }} title={categoryMeta.label} />
      )}
      <div className="mono-card-compact-header">
        <span className="mono-card-compact-type-icon" title={meta.label} style={{ color: visualStyle.icon }}>
          {visual.icon ? <CardIcon name={visual.icon} size={16} color={visualStyle.icon} /> : meta.icon}
        </span>
        {leadingDot && (
          <span className="mono-card-compact-dot" title={leadingDot.title} style={{ background: leadingDot.color }} />
        )}
        {leadingIcon === 'target' && <Target className="mono-card-compact-leading-icon" size={14} aria-hidden="true" />}
        <h3 className="mono-card-compact-title" title={card.id}>{titleOverride ?? card.id}</h3>
        {agentRef && variant !== 'workflow' && <CardAgentAvatar agentRef={agentRef} />}
      </div>
      {(compact || hasMeta) && variant !== 'workflow' && (
        <div className={`mono-card-compact-tags-row${compact ? ' mono-card-compact-tags-row--compact' : ''}`}>
          {compact && card.status && !cornerStatus && (
            <span
              className="mono-card-compact-status mono-card-compact-status--compact"
              style={{ '--wiki-status-color': statusColor } as React.CSSProperties}
              title={statusMeta?.label ?? card.status}
            >
              {statusMeta?.icon}
              <span className="mono-card-compact-status-label">{statusMeta?.label ?? card.status}</span>
            </span>
          )}
          {card.tags.map(tag => (
            <span key={tag} className={`mono-card-compact-tag${isBuiltinTag(tag) ? ' mono-card-compact-tag--builtin' : ''}`}>
              {isBuiltinTag(tag) ? getBuiltinTagDisplayName(tag, t) : tag}
            </span>
          ))}
          {!compact && card.priority && (
            <span className="mono-card-compact-priority" style={{ color: PRIORITY_META[card.priority]?.color ?? 'var(--text-tertiary)' }}>
              {PRIORITY_META[card.priority]?.label ?? card.priority}
            </span>
          )}
          {!compact && card.status && (
            <span
              className="mono-card-compact-status"
              style={{ '--wiki-status-color': STATUS_META[card.status]?.color ?? 'var(--text-tertiary)' } as React.CSSProperties}
            >
              {STATUS_META[card.status]?.label ?? card.status}
            </span>
          )}
          {!compact && card.due && (
            <span className={`mono-card-compact-due${isOverdue ? ' overdue' : ''}`}>{card.due}</span>
          )}
        </div>
      )}
      {agentRef && variant === 'workflow' && (
        <div className="mono-card-compact-agent-corner">
          <CardAgentAvatar
            agentRef={agentRef}
            onClick={(agent) => {
              if (!agent) return
              requestOpenAgentChat(agent.ProjectId, agent.ActorId || agent.Id)
            }}
          />
        </div>
      )}
    </article>
    {/* Corner badges straddle the card's top border, so they must be siblings
        of the article: workflow cards clip children (overflow:hidden) to round
        the category bar, which would cut off anything above the top edge. */}
    {cornerChip && (
      <span
        className="mono-card-compact-status mono-card-compact-status--compact mono-card-compact-corner-status"
        style={{ '--wiki-status-color': cornerChip.color } as React.CSSProperties}
      >
        <span className="mono-card-compact-status-label">{cornerChip.label}</span>
      </span>
    )}
    {compact && cornerStatus && card.status && (
      <span
        className="mono-card-compact-status mono-card-compact-status--compact mono-card-compact-corner-status"
        style={{ '--wiki-status-color': statusColor } as React.CSSProperties}
        title={statusMeta?.label ?? card.status}
      >
        {statusMeta?.icon}
        <span className="mono-card-compact-status-label">{statusMeta?.label ?? card.status}</span>
      </span>
    )}
    </>
  )
}
