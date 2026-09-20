import { Lock, LockOpen, X } from 'lucide-react'
import { useI18n } from '../../i18n'
import type { WorkbenchCardDescriptor } from './cardTypes'
import type { WorkbenchPlacement } from './cardLayout'
import { WorkbenchCardCompact } from './WorkbenchCardCompact'

export interface WorkbenchCardProps {
  card: WorkbenchCardDescriptor
  placement: WorkbenchPlacement
  /** Render the expanded body (main slot / terminal strip). */
  expanded: boolean
  focused: boolean
  pinned: boolean
  maximized: boolean
  proposing: boolean
  /** Debug: render the attention score beside the title. */
  showScore?: boolean
  /** Effective attention score (actor projection or descriptor + local boost). */
  attentionScore?: number
  /** Hover attribution line (score / why), mirroring the reference `.attr`. */
  attribution?: string
  onSelect: () => void
  /** Explicit pin toggle (header lock button); omitted when pinning is unavailable. */
  onTogglePin?: () => void
  /** Optional retreat affordance (the terminal card closes back to hidden). */
  onClose?: () => void
}

/**
 * Generic workbench card wrapper (spec §2): one component renders every card
 * kind, combining the placement slot, the compact/expanded body switch, and
 * the board affordance (the 🔒 lock marker on a pinned main card). Card-specific
 * behaviour lives entirely in the descriptor's `render`.
 */
export function WorkbenchCard({
  card,
  placement,
  expanded,
  focused,
  pinned,
  maximized,
  proposing,
  showScore,
  attentionScore,
  attribution,
  onSelect,
  onTogglePin,
  onClose,
}: WorkbenchCardProps) {
  const { t } = useI18n()
  const { slot } = placement
  const className = [
    'wb-card',
    focused ? 'wb-card--focused' : '',
    maximized ? 'wb-card--maxed' : '',
    pinned ? 'wb-card--locked' : '',
    proposing ? 'wb-card--proposing' : '',
    placement.zone === 'rail' ? 'wb-card--railed' : '',
  ]
    .filter(Boolean)
    .join(' ')

  return (
    <section
      className={className}
      data-card-id={card.id}
      data-kind={card.kind}
      data-zone={placement.zone}
      style={{ left: `${slot.x}%`, top: `${slot.y}%`, width: `${slot.w}%`, height: `${slot.h}%` }}
      onClick={onSelect}
    >
      <header className="wb-card-head">
        <span className="wb-card-title" title={card.title}>
          {card.title}
        </span>
        {showScore && (
          <span className="wb-card-score" title={`score ${attentionScore ?? card.score}`}>
            {Math.round(attentionScore ?? card.score)}
          </span>
        )}
        {(focused || pinned) && onTogglePin && (
          <button
            type="button"
            className={`wb-card-pin${pinned ? ' on' : ''}`}
            title={t('workbench.surface.pin')}
            aria-label={t('workbench.surface.pin')}
            aria-pressed={pinned}
            onClick={e => {
              e.stopPropagation()
              onTogglePin()
            }}
          >
            {pinned ? <Lock size={11} /> : <LockOpen size={11} />}
          </button>
        )}
        {onClose && (
          <button
            type="button"
            className="wb-card-close"
            aria-label={card.title}
            title={card.title}
            onClick={e => {
              e.stopPropagation()
              onClose()
            }}
          >
            <X size={12} />
          </button>
        )}
      </header>
      <div className={`wb-card-body${expanded ? ' wb-card-body--expanded' : ''}`}>
        {expanded ? card.render(true) : <WorkbenchCardCompact card={card} />}
      </div>
      {attribution && <div className="wb-card-attr">{attribution}</div>}
    </section>
  )
}
