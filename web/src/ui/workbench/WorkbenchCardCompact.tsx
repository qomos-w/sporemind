import type { WorkbenchCardDescriptor } from './cardTypes'
import { workbenchCardIcon } from './workbenchIcon'
import '../ai/components/MonoCardCompact.css'

export interface WorkbenchCardCompactProps {
  card: WorkbenchCardDescriptor
}

/**
 * Compact host-metadata card (spec §2). Reuses the MonoCardCompact shape
 * (`.mono-card-compact*` classes + the visual palette) instead of forking the
 * MonoCard family: an icon tile, the title, and a one-line status. Plugin
 * cards deliberately do not embed their iframe here — an iframe cannot be
 * thumbnailed; the expanded body in the main slot mounts it full-size.
 */
export function WorkbenchCardCompact({ card }: WorkbenchCardCompactProps) {
  return (
    <article
      className="mono-card-compact mono-card-compact--compact wb-compact"
      data-kind={card.kind}
      data-card-id={card.id}
      style={card.color ? { borderColor: card.color } : undefined}
    >
      <div className="mono-card-compact-header">
        <span className="mono-card-compact-type-icon">{workbenchCardIcon(card, 16)}</span>
        <h3 className="mono-card-compact-title" title={card.title}>
          {card.title}
        </h3>
      </div>
      <div className="mono-card-compact-tags-row mono-card-compact-tags-row--compact">
        <span className="wb-compact-status" title={card.compactMeta.statusText}>
          {card.compactMeta.statusText}
        </span>
      </div>
    </article>
  )
}
