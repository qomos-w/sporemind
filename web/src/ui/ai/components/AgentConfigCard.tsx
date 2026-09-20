import type { ReactNode } from 'react'
import { Check, Edit2 } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { CardIcon } from './CardIcon'
import './AgentConfigCard.css'

/** Uniform collapsed-card size within a panel grid; expanded cards opt out. */
export const agentConfigCardSize = 'h-[136px] w-[220px] overflow-hidden'
/** Shorter variant for cards with a one-line description (component bundles). */
export const agentConfigCardSizeCompact = 'agent-config-card--compact h-[100px] w-[220px] overflow-hidden'
/** Shortest variant for title+tag only cards (auto-allow tools). */
export const agentConfigCardSizeSlim = 'h-[72px] w-[220px] overflow-hidden'

export interface AgentConfigCardProps {
  icon?: string
  iconFallback?: ReactNode
  title: string
  description?: string
  tags?: string[]
  selected?: boolean
  checked?: boolean
  disabled?: boolean
  onToggle?: () => void
  onClick?: () => void
  onEdit?: () => void
  footer?: ReactNode
  children?: ReactNode
  className?: string
  accent?: string
}

export function AgentConfigCard({
  icon,
  iconFallback,
  title,
  description,
  tags = [],
  selected = false,
  checked,
  disabled = false,
  onToggle,
  onClick,
  onEdit,
  footer,
  children,
  className,
  accent,
}: AgentConfigCardProps) {
  const { t } = useI18n()
  const active = selected || checked === true
  const clickable = !disabled && (onClick || onToggle)

  const handleClick = () => {
    if (disabled) return
    if (onToggle) onToggle()
    else if (onClick) onClick()
  }

  return (
    <article
      className={`agent-config-card${active ? ' agent-config-card--active' : ''}${disabled ? ' agent-config-card--disabled' : ''}${clickable ? ' agent-config-card--clickable' : ''}${className ? ` ${className}` : ''}`}
      onClick={handleClick}
      role={clickable ? 'button' : undefined}
      tabIndex={clickable ? 0 : undefined}
      onKeyDown={clickable ? (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleClick() } } : undefined}
      style={accent ? { borderLeftColor: accent } : undefined}
    >
      <div className="agent-config-card-header">
        <span className="agent-config-card-icon" style={accent ? { color: accent } : undefined}>
          {icon ? <CardIcon name={icon} size={16} color="currentColor" /> : iconFallback}
        </span>
        <h3 className="agent-config-card-title">{title}</h3>
        <div className="agent-config-card-actions">
          {onEdit && !disabled && (
            <button
              type="button"
              className="agent-config-card-action"
              title={t('common.edit')}
              onClick={(e) => { e.stopPropagation(); onEdit() }}
            >
              <Edit2 size={13} />
            </button>
          )}
          {onToggle && (
            <button
              type="button"
              className={`agent-config-card-toggle${active ? ' agent-config-card-toggle--active' : ''}`}
              title={active ? t('common.enabled') : t('common.disabled')}
              disabled={disabled}
              onClick={(e) => { e.stopPropagation(); if (!disabled) onToggle() }}
            >
              <Check size={12} />
            </button>
          )}
        </div>
      </div>
      {description && (
        <p className="agent-config-card-description">{description}</p>
      )}
      {children}
      {(tags.length > 0 || footer) && (
        <div className="agent-config-card-footer">
          {tags.length > 0 && (
            <div className="agent-config-card-tags">
              {tags.map((tag) => (
                <span key={tag} className="agent-config-card-tag">{tag}</span>
              ))}
            </div>
          )}
          {footer}
        </div>
      )}
    </article>
  )
}
